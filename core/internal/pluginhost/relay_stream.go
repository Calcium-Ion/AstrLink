package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"time"

	"github.com/gorilla/websocket"

	"github.com/QuantumNous/astrlink/core/internal/plugin"
	"github.com/QuantumNous/astrlink/core/internal/transport"
	"github.com/QuantumNous/new-api/pluginkit"
)

// A converted answer read as a stream goes through convertStreamEvent
// event by event: the server-sent events, lines or AWS event stream
// messages of an HTTP response, or the frames of an upstream WebSocket
// session. The host converts before it answers the client, until the
// hook returns its first output, an error, or the stream ends; an error
// before then answers the client as an error of convertResponse does.

// Bounds of a converted stream.
const (
	// convertedStreamIdle fails a stream that sends nothing this long.
	convertedStreamIdle = 5 * time.Minute
)

// conversionStart is how a converted stream begins: its response once it
// has output, or the error before any.
type conversionStart struct {
	response *http.Response
	failure  *convertError
	err      error
}

// eventSource reads an upstream's events, calling each until it says done
// or the stream ends.
type eventSource func(each func(pluginkit.StreamEvent) (bool, error)) error

// conversion converts one stream.
type conversion struct {
	ctx    context.Context
	relay  *relayTarget
	hook   pluginkit.HookContext
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
	return &conversion{ctx: ctx, relay: send.relay, hook: send.hookContext(attempt.ClientPath, nil), state: state, client: attempt.Stream,
		reader: reader, out: out, ready: make(chan conversionStart, 1)}
}

// run converts the events of source in the background, and returns once
// the conversion has output, failed, or ended; done releases the source.
func (conversion *conversion) run(source eventSource, done func()) conversionStart {
	go func() {
		defer done()
		err := source(func(event pluginkit.StreamEvent) (bool, error) { return conversion.step(&event) })
		if err == nil && !conversion.ended && conversion.failure == nil {
			_, err = conversion.step(nil)
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

// step has convertStreamEvent convert one event, or end the stream for
// nil, and writes its output.
func (conversion *conversion) step(event *pluginkit.StreamEvent) (bool, error) {
	hookCtx := conversion.hook
	hookCtx.State = conversion.state
	type converted struct {
		result pluginkit.StreamResult
		state  json.RawMessage
		failed *convertError
	}
	output, err := callProvider(conversion.ctx, conversion.relay, "convertStreamEvent", func(ctx context.Context, loaded *plugin.NewAPIPlugin) (converted, error) {
		result, state, failed, err := loaded.CallConvertStreamEvent(ctx, hookCtx, event, event == nil)
		return converted{result, state, failed}, err
	})
	if err != nil {
		return true, fmt.Errorf("convertStreamEvent: %w", err)
	}
	step := output.result
	conversion.state = output.state
	if output.failed != nil {
		if !conversion.started {
			conversion.failure = output.failed
			return true, nil
		}
		return true, errors.New(truncateRunes(output.failed.Message, 4096))
	}
	switch {
	case conversion.client && (len(step.Chunks) > 0 || step.HasBody):
		return true, errors.New("convertStreamEvent returned chunks or a body for a streaming client")
	case !conversion.client && len(step.Events) > 0:
		return true, errors.New("convertStreamEvent returned events for a client that does not stream")
	case step.HasBody && conversion.chunked:
		return true, errors.New("convertStreamEvent returned a body after chunks")
	case (len(step.Send) > 0 || step.Close) && conversion.socket == nil:
		return true, errors.New("convertStreamEvent sent to a WebSocket session the request did not open")
	}
	for _, event := range step.Events {
		conversion.begin("text/event-stream")
		if err := writeServerSentText(conversion.out, event.Event, event.Data); err != nil {
			return true, err
		}
	}
	for _, data := range step.Chunks {
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
	if step.HasBody {
		contentType := step.ContentType
		if contentType == "" {
			contentType = "application/json"
		}
		conversion.begin(contentType)
		if _, err := conversion.out.Write(step.Body); err != nil {
			return true, err
		}
		// A body is the whole response: the upstream is closed.
		conversion.ended = true
	}
	if conversion.socket != nil && !conversion.closed {
		for _, message := range step.Send {
			if err := writeSocketMessage(conversion.socket, message); err != nil {
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
	return func(each func(pluginkit.StreamEvent) (bool, error)) error {
		read := func(event map[string]any) (bool, error) {
			data, _ := event["data"].(string)
			name, _ := event["event"].(string)
			return each(pluginkit.StreamEvent{Event: name, Data: data})
		}
		media, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
		if media == "application/vnd.amazon.eventstream" {
			return readAWSEvents(response.Body, read)
		}
		mode := streamLines
		if media == "text/event-stream" {
			mode = streamSSE
		}
		return readEvents(response.Body, mode, read)
	}
}

// writeSocketMessage sends a message to an upstream WebSocket: text, or
// binary.
func writeSocketMessage(conn *websocket.Conn, message pluginkit.WebSocketMessage) error {
	if message.IsBinary {
		return conn.WriteMessage(websocket.BinaryMessage, message.Binary)
	}
	return conn.WriteMessage(websocket.TextMessage, []byte(message.Text))
}

// socketEvents reads the frames of an upstream WebSocket session, text as
// {data} and binary as {dataBase64}, until either side closes it.
func socketEvents(conn *websocket.Conn) eventSource {
	return func(each func(pluginkit.StreamEvent) (bool, error)) error {
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
			event := pluginkit.StreamEvent{Data: string(data)}
			if kind == websocket.BinaryMessage {
				event = pluginkit.StreamEvent{Binary: data}
			}
			if done, err := each(event); err != nil || done {
				return err
			}
		}
	}
}

// converseWebSocket talks to the vendor over the WebSocket session
// convertRequest asked for, and answers in the route's protocol.
func (host *Host) converseWebSocket(ctx context.Context, writer http.ResponseWriter, target transport.Target, attempt ProviderAttempt, send *relaySend, request pluginkit.WebSocketRequest, state json.RawMessage) error {
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
		for _, message := range request.Messages {
			if err := writeSocketMessage(conn, message); err != nil {
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
