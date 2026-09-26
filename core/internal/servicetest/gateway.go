package servicetest

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/ingress"
)

// gatewayExchange bridges the gateway's streaming writer to the existing test
// parser. Observer fields are set before ready is sent, and never change after
// that handoff: one test has at most one upstream response.
type gatewayExchange struct {
	response       *http.Response
	sentAt         time.Time
	headersMS      *int64
	upstreamStatus int
	credentials    http.Header
}

func (tester *Tester) execute(request *http.Request, service contract.Service, input contract.ServiceTestRequest, responseLimit int) (*gatewayExchange, error) {
	ctx, cancel := context.WithCancel(request.Context())
	request = request.WithContext(ctx)
	reader, writer := io.Pipe()
	ready := make(chan *http.Response, 1)
	done := make(chan struct{})
	capture := &gatewayResponseWriter{header: make(http.Header), body: writer, ready: ready}
	exchange := &gatewayExchange{}
	baseURL := ""
	if service.Subscription != nil && tester.subscriptionBaseURL != nil {
		baseURL = tester.subscriptionBaseURL(service.Subscription.Provider)
	}
	stop := context.AfterFunc(ctx, func() {
		_ = reader.CloseWithError(ctx.Err())
		_ = writer.CloseWithError(ctx.Err())
	})
	go func() {
		defer close(done)
		defer stop()
		err := tester.gateway.ServeServiceTest(capture, request, service, input, baseURL, ingress.ServiceTestObserver{
			Authorization: func(headers http.Header) { exchange.credentials = headers },
			Outbound:      func() { exchange.sentAt = time.Now() },
			Response: func(status int) {
				exchange.upstreamStatus = status
				elapsed := time.Since(exchange.sentAt).Milliseconds()
				exchange.headersMS = &elapsed
			},
			WrapResponseBody: func(body io.ReadCloser) io.ReadCloser {
				return &boundedGatewayBody{ReadCloser: body, remaining: responseLimit + 1}
			},
		})
		if !capture.committed {
			capture.WriteHeader(http.StatusOK)
		}
		_ = writer.CloseWithError(err)
	}()
	select {
	case exchange.response = <-ready:
		exchange.response.Body = &gatewayResponseBody{PipeReader: reader, cancel: cancel, done: done}
		return exchange, nil
	case <-ctx.Done():
		cancel()
		_ = reader.CloseWithError(ctx.Err())
		<-done
		return nil, ctx.Err()
	}
}

type boundedGatewayBody struct {
	io.ReadCloser
	remaining int
}

func (body *boundedGatewayBody) Read(buffer []byte) (int, error) {
	if body.remaining == 0 {
		return 0, errResponseTooLarge
	}
	if len(buffer) > body.remaining {
		buffer = buffer[:body.remaining]
	}
	n, err := body.ReadCloser.Read(buffer)
	body.remaining -= n
	return n, err
}

type gatewayResponseWriter struct {
	header    http.Header
	body      *io.PipeWriter
	ready     chan<- *http.Response
	committed bool
}

func (writer *gatewayResponseWriter) Header() http.Header { return writer.header }

func (writer *gatewayResponseWriter) WriteHeader(status int) {
	if writer.committed || status < 200 {
		return
	}
	writer.committed = true
	writer.ready <- &http.Response{
		StatusCode: status, Status: strconv.Itoa(status) + " " + http.StatusText(status),
		Header: writer.header.Clone(),
	}
}

func (writer *gatewayResponseWriter) Write(data []byte) (int, error) {
	if !writer.committed {
		writer.WriteHeader(http.StatusOK)
	}
	return writer.body.Write(data)
}

func (writer *gatewayResponseWriter) Flush() {
	if !writer.committed {
		writer.WriteHeader(http.StatusOK)
	}
}

type gatewayResponseBody struct {
	*io.PipeReader
	cancel context.CancelFunc
	done   <-chan struct{}
}

func (body *gatewayResponseBody) Close() error {
	err := body.PipeReader.Close()
	body.cancel()
	// Wait for final request-record persistence, also on truncation/cancellation.
	<-body.done
	return err
}
