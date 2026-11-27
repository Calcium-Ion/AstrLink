package taskplugin

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/internal/plugin"
	"github.com/QuantumNous/new-api/pluginkit"
)

// Bounds of a submission read as server-sent events.
const (
	maxEventBytes = 1 << 20
	// streamIdle ends a stream that sent nothing for this long.
	streamIdle = 30 * time.Second
)

// sseEvent is the SubmitEvent a hook reads.
type sseEvent struct {
	Event string `json:"event"`
	ID    string `json:"id"`
	Data  string `json:"data"`
}

// readEvents reads server-sent events from body, calling handle for each
// until it says the stream is done. A stream that ends first, or sends
// nothing for streamIdle, fails.
func readEvents(ctx context.Context, cancel context.CancelFunc, body io.Reader, handle func(sseEvent) (bool, error)) error {
	idle := time.AfterFunc(streamIdle, cancel)
	defer idle.Stop()
	reader := bufio.NewReaderSize(body, 64<<10)
	var event sseEvent
	var data strings.Builder
	hasData := false
	size := 0
	dispatch := func() (bool, error) {
		if !hasData {
			event = sseEvent{}
			return false, nil
		}
		event.Data = data.String()
		if event.Event == "" {
			event.Event = "message"
		}
		done, err := handle(event)
		event, hasData, size = sseEvent{ID: event.ID}, false, 0
		data.Reset()
		return done, err
	}
	for {
		line, err := readLine(reader)
		if err != nil {
			if ctx.Err() != nil {
				return errors.New("the upstream stream sent nothing for 30 seconds")
			}
			if errors.Is(err, io.EOF) {
				return errors.New("the upstream stream ended before the plugin said it was done")
			}
			return fmt.Errorf("the upstream stream could not be read: %w", err)
		}
		idle.Reset(streamIdle)
		if size += len(line); size > maxEventBytes {
			return errors.New("an upstream event is larger than 1 MiB")
		}
		if line == "" {
			done, err := dispatch()
			if err != nil || done {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "data":
			if hasData {
				data.WriteByte('\n')
			}
			data.WriteString(value)
			hasData = true
		case "event":
			event.Event = value
		case "id":
			if !strings.ContainsRune(value, 0) {
				event.ID = value
			}
		}
	}
}

// readLine reads one line ended by \n, \r\n or \r.
func readLine(reader *bufio.Reader) (string, error) {
	var line []byte
	for {
		char, err := reader.ReadByte()
		if err != nil {
			if errors.Is(err, io.EOF) && len(line) > 0 {
				return string(line), nil
			}
			return "", err
		}
		switch char {
		case '\n':
			return string(line), nil
		case '\r':
			if next, err := reader.Peek(1); err == nil && next[0] == '\n' {
				_, _ = reader.ReadByte()
			}
			return string(line), nil
		}
		if len(line) > maxEventBytes {
			return "", errors.New("an upstream line is larger than 1 MiB")
		}
		line = append(line, char)
	}
}

// streamSubmission reads a submission answered as server-sent events: the
// plugin's event hook reads each event until it says the stream is done,
// and what it built becomes the body parseSubmitResponse reads.
func (call *exchange) streamSubmission(ctx context.Context, cancel context.CancelFunc, response *http.Response, driver driverContext) (json.RawMessage, error) {
	loaded := call.meta
	hook := "parseSubmitEvent"
	var accumulated *pluginkit.JSONState
	if pluginkit.UsesSubmitEventDelta(loaded.Meta, loaded.Engine) {
		hook = "parseSubmitEventDelta"
		accumulated = pluginkit.NewJSONState(pluginkit.MaxJSONToolBytes)
	}
	driverValue := plugin.NewAPIObject(driver)
	var state any
	var built json.RawMessage
	err := readEvents(ctx, cancel, response.Body, func(event sseEvent) (bool, error) {
		result, err := callHook(ctx, call, hook, func(ctx context.Context, loaded *plugin.NewAPIPlugin) (pluginkit.SubmitEventResult, error) {
			return loaded.CallSubmitEvent(ctx, hook, driverValue, pluginkit.SubmitEvent{Event: event.Event, ID: event.ID, Data: event.Data}, state)
		})
		if err != nil {
			return false, err
		}
		if accumulated != nil {
			if err := accumulated.Apply(ctx, result.Changes); err != nil {
				return false, fmt.Errorf("%s: %w", hook, err)
			}
		}
		// The state goes back to the next event as plain JSON.
		state = pluginkit.RawJSON(result.StateJSON)
		if !result.Done {
			return false, nil
		}
		if accumulated == nil {
			built = result.StateJSON
			return true, nil
		}
		value, err := accumulated.Value()
		if err == nil {
			built, err = marshalOrdered(value)
		}
		return true, err
	})
	if err != nil {
		var known *Error
		if errors.As(err, &known) {
			return nil, known
		}
		return nil, &Error{Status: http.StatusBadGateway, Code: "upstream_stream_failed", Message: err.Error(), err: err}
	}
	return jsonOrNull(built), nil
}
