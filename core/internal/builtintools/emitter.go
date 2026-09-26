package builtintools

import (
	"fmt"
	"net/http"
	"time"
)

type emitter struct {
	liveIndexes map[int]int
	liveItems   map[string]int
	writer      http.ResponseWriter
	streaming   bool
	sequence    int
	response    Object
	output      []any
	usage       Object
}

func newEmitter(writer http.ResponseWriter, model string, stream bool) *emitter {
	return &emitter{writer: writer, streaming: stream, output: []any{}, usage: Object{}, response: Object{"id": ID("resp_tool_"), "object": "response", "created_at": time.Now().Unix(), "status": "in_progress", "model": model, "output": []any{}, "error": nil, "incomplete_details": nil}}
}
func (e *emitter) event(kind string, fields Object) error {
	if !e.streaming {
		return nil
	}
	if fields == nil {
		fields = Object{}
	}
	fields["type"] = kind
	fields["sequence_number"] = e.sequence
	e.sequence++
	_, err := fmt.Fprintf(e.writer, "event: %s\ndata: %s\n\n", kind, Text(fields))
	if err == nil {
		if f, ok := e.writer.(http.Flusher); ok {
			f.Flush()
		}
	}
	return err
}
func (e *emitter) start() error {
	if !e.streaming {
		return nil
	}
	e.writer.Header().Set("Content-Type", "text/event-stream")
	e.writer.Header().Set("Cache-Control", "no-cache")
	if err := e.event("response.created", Object{"response": e.response}); err != nil {
		return err
	}
	return e.event("response.in_progress", Object{"response": e.response})
}
func (e *emitter) beginTool(item Object, index int) error {
	if err := e.event("response.output_item.added", Object{"output_index": index, "item": item}); err != nil {
		return err
	}
	return e.event("response."+String(item["type"])+".in_progress", Object{"output_index": index, "item_id": item["id"]})
}
func (e *emitter) endTool(item Object, index int) error {
	e.output = append(e.output, item)
	if err := e.event("response."+String(item["type"])+".completed", Object{"output_index": index, "item_id": item["id"]}); err != nil {
		return err
	}
	return e.event("response.output_item.done", Object{"output_index": index, "item": item})
}
func (e *emitter) item(item Object) error {
	if index, ok := e.liveItems[String(item["id"])]; ok {
		e.output[index] = item
		return nil
	}
	index := len(e.output)
	e.output = append(e.output, item)
	initial := Clone(item)
	initial["status"] = "in_progress"
	if item["type"] == "message" {
		initial["content"] = []any{}
	}
	if item["type"] == "function_call" {
		initial["arguments"] = ""
	}
	if err := e.event("response.output_item.added", Object{"output_index": index, "item": initial}); err != nil {
		return err
	}
	if item["type"] == "message" {
		for i, raw := range Array(item["content"]) {
			part := Map(raw)
			fields := Object{"output_index": index, "item_id": item["id"], "content_index": i}
			added := Clone(part)
			if part["type"] == "output_text" {
				added["text"] = ""
				added["annotations"] = []any{}
			}
			fields["part"] = added
			if err := e.event("response.content_part.added", fields); err != nil {
				return err
			}
			if part["type"] == "output_text" {
				if err := e.event("response.output_text.delta", Object{"output_index": index, "item_id": item["id"], "content_index": i, "delta": part["text"]}); err != nil {
					return err
				}
				if err := e.event("response.output_text.done", Object{"output_index": index, "item_id": item["id"], "content_index": i, "text": part["text"]}); err != nil {
					return err
				}
				for j, annotation := range Array(part["annotations"]) {
					if err := e.event("response.output_text.annotation.added", Object{"output_index": index, "item_id": item["id"], "content_index": i, "annotation_index": j, "annotation": annotation}); err != nil {
						return err
					}
				}
			}
			fields["part"] = part
			if err := e.event("response.content_part.done", fields); err != nil {
				return err
			}
		}
	}
	if item["type"] == "function_call" {
		if err := e.event("response.function_call_arguments.delta", Object{"output_index": index, "item_id": item["id"], "delta": item["arguments"]}); err != nil {
			return err
		}
		if err := e.event("response.function_call_arguments.done", Object{"output_index": index, "item_id": item["id"], "arguments": item["arguments"]}); err != nil {
			return err
		}
	}
	return e.event("response.output_item.done", Object{"output_index": index, "item": item})
}

func (e *emitter) beginRound() { e.liveIndexes = map[int]int{}; e.liveItems = map[string]int{} }

// Forward text as it arrives, while withholding private function calls and
// every inner response lifecycle event from the public response stream.
func (e *emitter) liveEvent(event Object, sources []Object) error {
	if !e.streaming {
		return nil
	}
	kind := String(event["type"])
	number, ok := event["output_index"].(float64)
	if !ok {
		return nil
	}
	sourceIndex := int(number)
	if kind == "response.output_item.added" {
		item := Map(event["item"])
		if item["type"] != "message" {
			return nil
		}
		index := len(e.output)
		e.liveIndexes[sourceIndex] = index
		e.liveItems[String(item["id"])] = index
		e.output = append(e.output, Clone(item))
	}
	index, ok := e.liveIndexes[sourceIndex]
	if !ok {
		return nil
	}
	fields := Clone(event)
	delete(fields, "type")
	delete(fields, "sequence_number")
	fields["output_index"] = index
	if kind == "response.output_item.done" {
		original := Map(event["item"])
		item := addCitations(Clone(original), sources)
		e.output[index] = item
		fields["item"] = item
		for i, raw := range Array(item["content"]) {
			part := Map(raw)
			for j, annotation := range Array(part["annotations"]) {
				if i < len(Array(original["content"])) && j < len(Array(Map(Array(original["content"])[i])["annotations"])) {
					continue
				}
				if err := e.event("response.output_text.annotation.added", Object{"output_index": index, "item_id": item["id"], "content_index": i, "annotation_index": j, "annotation": annotation}); err != nil {
					return err
				}
			}
		}
	}
	return e.event(kind, fields)
}
func (e *emitter) addUsage(usage Object) {
	for _, key := range []string{"input_tokens", "output_tokens", "total_tokens"} {
		if n, ok := usage[key].(float64); ok {
			old, _ := e.usage[key].(float64)
			e.usage[key] = old + n
		}
	}
}
func (e *emitter) finish() error {
	if e.response["status"] != "incomplete" {
		e.response["status"] = "completed"
	}
	e.response["output"] = e.output
	e.response["usage"] = e.usage
	if e.streaming {
		return e.event("response."+String(e.response["status"]), Object{"response": e.response})
	}
	e.writer.Header().Set("Content-Type", "application/json")
	_, err := fmt.Fprint(e.writer, Text(e.response))
	return err
}
func (e *emitter) fail(err error) error {
	e.response["status"] = "failed"
	e.response["output"] = e.output
	e.response["error"] = Object{"code": "builtin_tool_failed", "message": err.Error()}
	return e.event("response.failed", Object{"response": e.response})
}
