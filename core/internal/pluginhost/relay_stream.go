package pluginhost

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"mime"
	"net"
	"net/http"
	"time"

	"github.com/gorilla/websocket"

	"github.com/QuantumNous/astrlink/core/internal/transport"
)

// A converted answer read as a stream goes through convertStreamEvent
// event by event: the server-sent events, lines or AWS event stream
// messages of an HTTP response, or the frames of an upstream WebSocket
// session. The host converts before it answers the client, until the
// hook returns its first output, an error, or the stream ends; an error
// before then answers the client as an error of convertResponse does.

// Bounds of a converted stream.
const (
	maxStreamEvents    = 1024
	maxSocketMessages  = 1024
	maxSocketProtocols = 16
	// convertedStreamIdle fails a stream that sends nothing this long.
	convertedStreamIdle = 5 * time.Minute
)

// convertedStep is what convertStreamEvent returns.
type convertedStep struct {
	Events []struct {
		Event string          `json:"event"`
		Data  json.RawMessage `json:"data"`
	} `json:"events"`
	Chunks []struct {
		BodyBase64 string `json:"bodyBase64"`
	} `json:"chunks"`
	Body        json.RawMessage   `json:"body"`
	BodyBase64  *string           `json:"bodyBase64"`
	ContentType string            `json:"contentType"`
	Send        []json.RawMessage `json:"send"`
	Close       bool              `json:"close"`
	State       json.RawMessage   `json:"state"`
	Error       *convertError     `json:"error"`
}

// conversionStart is how a converted stream begins: its response once it
// has output, or the error before any.
type conversionStart struct {
	response *http.Response
	failure  *convertError
	err      error
}

// eventSource reads an upstream's events, calling each until it says done
// or the stream ends.
type eventSource func(each func(map[string]any) (bool, error)) error

// conversion converts one stream.
type conversion struct {
	ctx    context.Context
	relay  *relayTarget
	view   map[string]any
	state  json.RawMessage
	client bool
	socket *websocket.Conn
	reader *io.PipeReader
	out    *io.PipeWriter
	ready  chan conversionStart
	// started is set once the client has the response; chunked once it
	// has binary output; ended once a body ended the stream.
	started, chunked, ended, closed bool
	failure                         *convertError
}

func (host *Host) newConversion(ctx context.Context, send *relaySend, attempt ProviderAttempt, state json.RawMessage) *conversion {
	reader, out := io.Pipe()
	return &conversion{ctx: ctx, relay: send.relay, view: send.view(attempt.ClientPath), state: state, client: attempt.Stream,
		reader: reader, out: out, ready: make(chan conversionStart, 1)}
}

// run converts the events of source in the background, and returns once
// the conversion has output, failed, or ended; done releases the source.
func (conversion *conversion) run(source eventSource, done func()) conversionStart {
	go func() {
		defer done()
		err := source(conversion.step)
		if err == nil && !conversion.ended && conversion.failure == nil {
			_, err = conversion.step(map[string]any{"done": true})
		}
		conversion.finish(err)
	}()
	select {
	case start := <-conversion.ready:
		return start
	case <-conversion.ctx.Done():
		_ = conversion.reader.CloseWithError(conversion.ctx.Err())
		return conversionStart{err: conversion.ctx.Err()}
	}
}

// begin answers the client with the output that follows.
func (conversion *conversion) begin(contentType string) {
	if conversion.started {
		return
	}
	conversion.started = true
	conversion.ready <- conversionStart{response: &http.Response{
		StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}}, Body: conversion.reader, ContentLength: -1,
	}}
}

// finish ends the stream: after output an error ends it as an upstream
// read error does; before, the caller answers it.
func (conversion *conversion) finish(err error) {
	switch {
	case conversion.started:
		_ = conversion.out.CloseWithError(err)
	case err != nil:
		conversion.ready <- conversionStart{err: err}
	case conversion.failure != nil:
		conversion.ready <- conversionStart{failure: conversion.failure}
	case conversion.client:
		// A stream that converted to no event.
		conversion.begin("text/event-stream")
		_ = conversion.out.Close()
	default:
		conversion.ready <- conversionStart{err: errors.New("the stream ended without chunks or a body for a client that does not stream")}
	}
}

// step has convertStreamEvent convert one input and writes its output.
func (conversion *conversion) step(input map[string]any) (bool, error) {
	view := maps.Clone(conversion.view)
	view["state"] = jsonOrNull(conversion.state)
	raw, err := conversion.relay.call(conversion.ctx, "convertStreamEvent", view, input)
	if err != nil {
		return true, fmt.Errorf("convertStreamEvent: %w", err)
	}
	var step convertedStep
	if present(raw) {
		if err := json.Unmarshal(raw, &step); err != nil {
			return true, errors.New("convertStreamEvent returned no events")
		}
	}
	if present(step.State) {
		conversion.state = step.State
	}
	if step.Error != nil {
		if !conversion.started {
			conversion.failure = step.Error
			return true, nil
		}
		return true, errors.New(truncateRunes(step.Error.Message, 4096))
	}
	body := present(step.Body) || step.BodyBase64 != nil
	switch {
	case len(step.Events) > maxStreamEvents:
		return true, fmt.Errorf("convertStreamEvent returned more than %d events", maxStreamEvents)
	case conversion.client && (len(step.Chunks) > 0 || body):
		return true, errors.New("convertStreamEvent returned chunks or a body for a streaming client")
	case !conversion.client && len(step.Events) > 0:
		return true, errors.New("convertStreamEvent returned events for a client that does not stream")
	case body && conversion.chunked:
		return true, errors.New("convertStreamEvent returned a body after chunks")
	case (len(step.Send) > 0 || step.Close) && conversion.socket == nil:
		return true, errors.New("convertStreamEvent sent to a WebSocket session the request did not open")
	}
	for _, event := range step.Events {
		conversion.begin("text/event-stream")
		if err := writeServerSentEvent(conversion.out, event.Event, event.Data); err != nil {
			return true, err
		}
	}
	for _, chunk := range step.Chunks {
		data, err := base64.StdEncoding.DecodeString(chunk.BodyBase64)
		if err != nil {
			return true, errors.New("convertStreamEvent returned a chunk that is not base64")
		}
		if !conversion.chunked {
			conversion.chunked = true
			contentType := step.ContentType
			if contentType == "" {
				contentType = "application/octet-stream"
			}
			conversion.begin(contentType)
		}
		if _, err := conversion.out.Write(data); err != nil {
			return true, err
		}
	}
	if body {
		data := convertedBody(step.Body)
		if step.BodyBase64 != nil {
			decoded, err := base64.StdEncoding.DecodeString(*step.BodyBase64)
			if err != nil {
				return true, errors.New("convertStreamEvent returned bodyBase64 that is not base64")
			}
			data = decoded
		}
		contentType := step.ContentType
		if contentType == "" {
			contentType = "application/json"
		}
		conversion.begin(contentType)
		if _, err := conversion.out.Write(data); err != nil {
			return true, err
		}
		// A body is the whole response: the upstream is closed.
		conversion.ended = true
	}
	if conversion.socket != nil && !conversion.closed {
		messages, err := socketMessages(step.Send)
		if err != nil {
			return true, err
		}
		for _, message := range messages {
			if err := message.write(conversion.socket); err != nil {
				return true, transport.NewUpstreamError(err)
			}
		}
		if step.Close || conversion.ended {
			conversion.closed = true
			_ = conversion.socket.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
		}
	}
	return conversion.ended || step.Close, nil
}

// httpEvents reads a streamed HTTP answer: server-sent events, an AWS
// event stream, or else each non-empty line.
func httpEvents(response *http.Response) eventSource {
	return func(each func(map[string]any) (bool, error)) error {
		media, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
		if media == "application/vnd.amazon.eventstream" {
			return readAWSEvents(response.Body, each)
		}
		mode := streamLines
		if media == "text/event-stream" {
			mode = streamSSE
		}
		return readEvents(response.Body, mode, func(event map[string]any) (bool, error) {
			input := map[string]any{"data": event["data"]}
			if name, ok := event["event"]; ok {
				input["event"] = name
			}
			return each(input)
		})
	}
}

// socketMessage is a message for an upstream WebSocket: text, or binary
// from {bodyBase64}.
type socketMessage struct {
	binary bool
	data   []byte
}

func (message socketMessage) write(conn *websocket.Conn) error {
	kind := websocket.TextMessage
	if message.binary {
		kind = websocket.BinaryMessage
	}
	return conn.WriteMessage(kind, message.data)
}

func socketMessages(raw []json.RawMessage) ([]socketMessage, error) {
	if len(raw) > maxSocketMessages {
		return nil, fmt.Errorf("a WebSocket session takes at most %d messages at a time", maxSocketMessages)
	}
	messages := make([]socketMessage, 0, len(raw))
	for _, item := range raw {
		var text string
		if json.Unmarshal(item, &text) == nil {
			messages = append(messages, socketMessage{data: []byte(text)})
			continue
		}
		var binary struct {
			BodyBase64 *string `json:"bodyBase64"`
		}
		if json.Unmarshal(item, &binary) != nil || binary.BodyBase64 == nil {
			return nil, errors.New("a WebSocket message is a string or {bodyBase64}")
		}
		data, err := base64.StdEncoding.DecodeString(*binary.BodyBase64)
		if err != nil {
			return nil, errors.New("a WebSocket message's bodyBase64 is not base64")
		}
		messages = append(messages, socketMessage{binary: true, data: data})
	}
	return messages, nil
}

// socketEvents reads the frames of an upstream WebSocket session, text as
// {data} and binary as {dataBase64}, until either side closes it.
func socketEvents(conn *websocket.Conn) eventSource {
	return func(each func(map[string]any) (bool, error)) error {
		for {
			_ = conn.SetReadDeadline(time.Now().Add(convertedStreamIdle))
			kind, data, err := conn.ReadMessage()
			if err != nil {
				var closed *websocket.CloseError
				if errors.As(err, &closed) || errors.Is(err, net.ErrClosed) {
					return nil
				}
				var timeout net.Error
				if errors.As(err, &timeout) && timeout.Timeout() {
					return fmt.Errorf("the upstream WebSocket sent nothing for %s", convertedStreamIdle)
				}
				return err
			}
			input := map[string]any{"data": string(data)}
			if kind == websocket.BinaryMessage {
				input = map[string]any{"dataBase64": base64.StdEncoding.EncodeToString(data)}
			}
			if done, err := each(input); err != nil || done {
				return err
			}
		}
	}
}

// websocketRequest is the session convertRequest asks to open.
type websocketRequest struct {
	Path      string            `json:"path"`
	URL       string            `json:"url"`
	Headers   map[string]string `json:"headers"`
	Protocols []string          `json:"protocols"`
	Messages  []json.RawMessage `json:"messages"`
}

// converseWebSocket talks to the vendor over the WebSocket session
// convertRequest asked for, and answers in the route's protocol.
func (host *Host) converseWebSocket(ctx context.Context, writer http.ResponseWriter, target transport.Target, attempt ProviderAttempt, send *relaySend, raw json.RawMessage, state json.RawMessage) error {
	var request websocketRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return transport.NewUpstreamError(errors.New("convertRequest's websocket is {path, url, headers, protocols, messages}"))
	}
	if len(request.Protocols) > maxSocketProtocols {
		return transport.NewUpstreamError(fmt.Errorf("a WebSocket session takes at most %d subprotocols", maxSocketProtocols))
	}
	messages, err := socketMessages(request.Messages)
	if err != nil {
		return transport.NewUpstreamError(err)
	}
	for {
		address, err := send.addressOf(request.Path, request.URL)
		if err != nil {
			return transport.NewUpstreamError(err)
		}
		header, err := send.headers(request.Headers)
		if err != nil {
			return transport.NewUpstreamError(err)
		}
		header.Del("Content-Type")
		address, credentials, err := send.signed(ctx, http.MethodGet, address, header, nil)
		if err != nil {
			return transport.NewUpstreamError(err)
		}
		dialed := target
		dialed.RequestHeaders = credentials
		conn, response, err := transport.DialWebSocket(ctx, dialed, &address, header, request.Protocols, maxProviderEventBytes*4)
		if err != nil {
			if response == nil {
				return err
			}
			if send.renewable() {
				rejected, kept := send.relay.rejected(ctx, send.baseURL, attempt.ClientPath, response)
				response = kept
				if rejected {
					failed, renewErr := send.renew(ctx)
					if failed != nil {
						_ = response.Body.Close()
						return answerRelay(writer, target, failed)
					}
					if renewErr == nil {
						_ = response.Body.Close()
						continue
					}
				}
			}
			return answerRelay(writer, target, response)
		}
		for _, message := range messages {
			if err := message.write(conn); err != nil {
				_ = conn.Close()
				return transport.NewUpstreamError(err)
			}
		}
		conversion := host.newConversion(ctx, send, attempt, state)
		conversion.socket = conn
		// The session closes when the client leaves.
		stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
		start := conversion.run(socketEvents(conn), func() {
			stop()
			_ = conn.Close()
		})
		switch {
		case start.err != nil:
			return transport.NewUpstreamError(start.err)
		case start.failure != nil && start.failure.Status == http.StatusUnauthorized && send.renewable():
			failed, err := send.renew(ctx)
			if failed != nil {
				return answerRelay(writer, target, failed)
			}
			if err == nil {
				continue
			}
			return answerRelay(writer, target, convertedFailure(*start.failure, http.StatusOK))
		case start.failure != nil:
			return answerRelay(writer, target, convertedFailure(*start.failure, http.StatusOK))
		}
		return answerRelay(writer, target, start.response)
	}
}
