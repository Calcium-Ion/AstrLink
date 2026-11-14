package taskplugin

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
)

// Bounds of what a client uploads.
const (
	// maxUploadBytes bounds one uploaded file, as new-api's
	// MAX_FILE_DOWNLOAD_MB does by default, and maxUploadsBytes all of
	// them.
	maxUploadBytes  = 64 << 20
	maxUploadsBytes = 128 << 20
	maxUploads      = 32
	maxFieldsBytes  = 1 << 20
	// maxJSONBodyBytes bounds a JSON or form body.
	maxJSONBodyBytes = 32 << 20
)

// Body kinds of a decoded request body.
const (
	BodyJSON      = "json"
	BodyForm      = "form"
	BodyMultipart = "multipart"
	BodyNone      = "none"
)

// Upload is a file a client uploaded with a multipart request.
type Upload struct {
	Field    string
	Filename string
	MimeType string
	Data     []byte
	// ref names it to the hooks: request_file:<field> for the first file
	// of a field, request_file:<field>#<index> for a later one.
	ref string
}

// Body is a client's request body as the decoders read it: JSON, a form,
// a multipart form with its files, or none.
type Body struct {
	Kind   string
	JSON   json.RawMessage
	Fields map[string][]string
	Files  []Upload
	// order is the fields' names in the order they first appear.
	order []string
}

// FieldNames are the names of a form body's text fields, in the order they
// first appear.
func (body *Body) FieldNames() []string {
	if len(body.order) > 0 {
		return slices.Clone(body.order)
	}
	names := make([]string, 0, len(body.Fields))
	for name := range body.Fields {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// ReadBody reads a request body by its media type. limit bounds a JSON or
// form body.
func ReadBody(request *http.Request, limit int64) (*Body, error) {
	if limit <= 0 || limit > maxJSONBodyBytes {
		limit = maxJSONBodyBytes
	}
	contentType := request.Header.Get("Content-Type")
	media, params, _ := mime.ParseMediaType(contentType)
	if request.Body == nil || request.Body == http.NoBody || (request.ContentLength == 0 && contentType == "") {
		return &Body{Kind: BodyNone}, nil
	}
	switch media {
	case "multipart/form-data":
		return readMultipart(request.Body, params["boundary"])
	case "application/x-www-form-urlencoded":
		data, err := readLimited(request.Body, limit)
		if err != nil {
			return nil, err
		}
		values, err := url.ParseQuery(string(data))
		if err != nil {
			return nil, failure(http.StatusBadRequest, "invalid_request", "the form body cannot be read")
		}
		return &Body{Kind: BodyForm, Fields: values}, nil
	}
	data, err := readLimited(request.Body, limit)
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return &Body{Kind: BodyNone}, nil
	}
	if !json.Valid(data) {
		return nil, failure(http.StatusBadRequest, "invalid_request", "the request body is not JSON")
	}
	return &Body{Kind: BodyJSON, JSON: data}, nil
}

func readLimited(reader io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, failure(http.StatusBadRequest, "invalid_request", "the request body cannot be read")
	}
	if int64(len(data)) > limit {
		return nil, failure(http.StatusRequestEntityTooLarge, "request_too_large", "the request body is larger than %d MiB", limit>>20)
	}
	return data, nil
}

func readMultipart(body io.Reader, boundary string) (*Body, error) {
	if boundary == "" {
		return nil, failure(http.StatusBadRequest, "invalid_request", "the multipart body has no boundary")
	}
	reader := multipart.NewReader(body, boundary)
	result := &Body{Kind: BodyMultipart, Fields: map[string][]string{}}
	counts := map[string]int{}
	var fieldBytes, fileBytes int64
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, failure(http.StatusBadRequest, "invalid_request", "the multipart body cannot be read")
		}
		name := part.FormName()
		if name == "" {
			part.Close()
			continue
		}
		if part.FileName() == "" {
			value, err := io.ReadAll(io.LimitReader(part, maxFieldsBytes-fieldBytes+1))
			part.Close()
			fieldBytes += int64(len(value))
			if err != nil || fieldBytes > maxFieldsBytes {
				return nil, failure(http.StatusRequestEntityTooLarge, "request_too_large", "the form fields are larger than 1 MiB")
			}
			if _, seen := result.Fields[name]; !seen {
				result.order = append(result.order, name)
			}
			result.Fields[name] = append(result.Fields[name], string(value))
			continue
		}
		if len(result.Files) == maxUploads {
			part.Close()
			return nil, failure(http.StatusBadRequest, "invalid_request", "a request uploads at most %d files", maxUploads)
		}
		data, err := io.ReadAll(io.LimitReader(part, maxUploadBytes+1))
		part.Close()
		if err != nil {
			return nil, failure(http.StatusBadRequest, "invalid_request", "an uploaded file cannot be read")
		}
		if len(data) > maxUploadBytes {
			return nil, failure(http.StatusRequestEntityTooLarge, "request_too_large", "an uploaded file is larger than %d MiB", maxUploadBytes>>20)
		}
		if fileBytes += int64(len(data)); fileBytes > maxUploadsBytes {
			return nil, failure(http.StatusRequestEntityTooLarge, "request_too_large", "the uploaded files are larger than %d MiB", maxUploadsBytes>>20)
		}
		mimeType := part.Header.Get("Content-Type")
		if mimeType == "" {
			mimeType = http.DetectContentType(data)
		}
		ref := "request_file:" + name
		if index := counts[name]; index > 0 {
			ref += "#" + strconv.Itoa(index)
		}
		counts[name]++
		result.Files = append(result.Files, Upload{Field: name, Filename: part.FileName(), MimeType: mimeType, Data: data, ref: ref})
	}
	return result, nil
}

// references lists the uploads as the hooks see them.
func (body *Body) references() []fileReference {
	references := []fileReference{}
	if body == nil {
		return references
	}
	for _, file := range body.Files {
		references = append(references, fileReference{Ref: file.ref, Field: file.Field, Filename: file.Filename, MimeType: file.MimeType, Size: int64(len(file.Data))})
	}
	return references
}

// upload finds the file ref names.
func (body *Body) upload(ref string) (Upload, bool) {
	if body == nil {
		return Upload{}, false
	}
	for _, file := range body.Files {
		if file.ref == ref {
			return file, true
		}
	}
	return Upload{}, false
}

// decoded is the body as a decoder reads it. A JSON value lists its
// members by key unless the plugin keeps the client's order.
func (body *Body) decoded(clientOrder bool) map[string]any {
	if body == nil {
		return map[string]any{"kind": BodyNone}
	}
	switch body.Kind {
	case BodyJSON:
		value := body.JSON
		if !clientOrder {
			value = sortedJSON(value)
		}
		return map[string]any{"kind": BodyJSON, "value": value}
	case BodyForm:
		return map[string]any{"kind": BodyForm, "fields": nonNilFields(body.Fields)}
	case BodyMultipart:
		return map[string]any{"kind": BodyMultipart, "fields": nonNilFields(body.Fields), "files": body.references()}
	}
	return map[string]any{"kind": BodyNone}
}

func nonNilFields(fields map[string][]string) map[string][]string {
	if fields == nil {
		return map[string][]string{}
	}
	return fields
}

// sortedJSON re-encodes a JSON text with the members of every object
// sorted by key, each number as written.
func sortedJSON(raw json.RawMessage) json.RawMessage {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return raw
	}
	encoded, err := marshalOrdered(value)
	if err != nil {
		return raw
	}
	return encoded
}

// TopLevelModel reads the model a JSON body names at its top level, ""
// for none.
func (body *Body) TopLevelModel() (string, bool) {
	if body == nil {
		return "", false
	}
	switch body.Kind {
	case BodyJSON:
		var object map[string]json.RawMessage
		if json.Unmarshal(body.JSON, &object) != nil {
			return "", false
		}
		raw, ok := object["model"]
		if !ok {
			return "", false
		}
		var model string
		if json.Unmarshal(raw, &model) != nil {
			return "", false
		}
		return model, true
	case BodyForm, BodyMultipart:
		values := body.Fields["model"]
		if len(values) != 1 {
			return "", false
		}
		return values[0], true
	}
	return "", false
}

// flag reads a boolean member of a JSON body.
func (body *Body) flag(name string) bool {
	if body == nil || body.Kind != BodyJSON {
		return false
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(body.JSON, &object) != nil {
		return false
	}
	var value bool
	return json.Unmarshal(object[name], &value) == nil && value
}

// Stream reports whether a JSON body sets stream: true.
func (body *Body) Stream() bool { return body.flag("stream") }

// Background reports whether a JSON body sets background: true.
func (body *Body) Background() bool { return body.flag("background") }

// jsonNode is a JSON value that keeps the order of its object members, for
// a request body to be rewritten without reordering what the plugin built.
type jsonNode struct {
	// kind is '{', '[' or 0 for a scalar in raw.
	kind     byte
	keys     []string
	children []*jsonNode
	raw      json.RawMessage
}

func parseNode(raw []byte) (*jsonNode, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	node, err := readNode(decoder)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("trailing data after the JSON value")
	}
	return node, nil
}

func readNode(decoder *json.Decoder) (*jsonNode, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	switch value := token.(type) {
	case json.Delim:
		node := &jsonNode{kind: byte(value)}
		for decoder.More() {
			if node.kind == '{' {
				key, err := decoder.Token()
				if err != nil {
					return nil, err
				}
				text, ok := key.(string)
				if !ok {
					return nil, errors.New("an object key is not a string")
				}
				node.keys = append(node.keys, text)
			}
			child, err := readNode(decoder)
			if err != nil {
				return nil, err
			}
			node.children = append(node.children, child)
		}
		if _, err := decoder.Token(); err != nil {
			return nil, err
		}
		return node, nil
	default:
		encoded, err := marshalOrdered(value)
		if err != nil {
			return nil, err
		}
		return &jsonNode{raw: encoded}, nil
	}
}

func (node *jsonNode) member(name string) (*jsonNode, bool) {
	for index, key := range node.keys {
		if key == name {
			return node.children[index], true
		}
	}
	return nil, false
}

func (node *jsonNode) write(buffer *bytes.Buffer) {
	switch node.kind {
	case '{':
		buffer.WriteByte('{')
		for index, key := range node.keys {
			if index > 0 {
				buffer.WriteByte(',')
			}
			name, _ := marshalOrdered(key)
			buffer.Write(name)
			buffer.WriteByte(':')
			node.children[index].write(buffer)
		}
		buffer.WriteByte('}')
	case '[':
		buffer.WriteByte('[')
		for index, child := range node.children {
			if index > 0 {
				buffer.WriteByte(',')
			}
			child.write(buffer)
		}
		buffer.WriteByte(']')
	default:
		buffer.Write(node.raw)
	}
}

func (node *jsonNode) encode() json.RawMessage {
	var buffer bytes.Buffer
	node.write(&buffer)
	return buffer.Bytes()
}

// placeholder is a FilePlaceholder: an uploaded file a JSON request body
// carries encoded.
type placeholder struct {
	Ref      string `json:"__fileRef"`
	Encoding string `json:"encoding"`
	MimeType string `json:"mimeType"`
	MaxBytes int64  `json:"maxBytes"`
}

// inlineFiles replaces each file placeholder in a JSON body with the file
// it names, in base64 or as a data URL.
func inlineFiles(raw json.RawMessage, body *Body) (json.RawMessage, error) {
	if !bytes.Contains(raw, []byte(`"__fileRef"`)) {
		return raw, nil
	}
	root, err := parseNode(raw)
	if err != nil {
		return nil, err
	}
	var inlined int64
	var walk func(node *jsonNode) error
	walk = func(node *jsonNode) error {
		if node.kind == '{' {
			if _, ok := node.member("__fileRef"); ok {
				replaced, size, err := inlineFile(node, body)
				if err != nil {
					return err
				}
				if inlined += size; inlined > maxUploadBytes {
					return fmt.Errorf("the files a request inlines are larger than %d MiB", maxUploadBytes>>20)
				}
				*node = *replaced
				return nil
			}
		}
		for _, child := range node.children {
			if err := walk(child); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(root); err != nil {
		return nil, err
	}
	return root.encode(), nil
}

func inlineFile(node *jsonNode, body *Body) (*jsonNode, int64, error) {
	allowed := map[string]bool{"__fileRef": true, "encoding": true, "mimeType": true, "maxBytes": true}
	keys := append([]string(nil), node.keys...)
	sort.Strings(keys)
	for _, key := range keys {
		if !allowed[key] {
			return nil, 0, fmt.Errorf("a file placeholder has the member %q", key)
		}
	}
	var found placeholder
	if err := json.Unmarshal(node.encode(), &found); err != nil {
		return nil, 0, errors.New("a file placeholder is malformed")
	}
	file, ok := body.upload(found.Ref)
	if !ok {
		return nil, 0, fmt.Errorf("the file placeholder names %q, which the request did not upload", found.Ref)
	}
	if found.MaxBytes > 0 && int64(len(file.Data)) > found.MaxBytes {
		return nil, 0, fmt.Errorf("the file %s is larger than %d bytes", file.Filename, found.MaxBytes)
	}
	encoded := base64.StdEncoding.EncodeToString(file.Data)
	switch found.Encoding {
	case "base64":
	case "dataUrl":
		mimeType := found.MimeType
		if mimeType == "" {
			mimeType = file.MimeType
		}
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
		encoded = "data:" + mimeType + ";base64," + encoded
	default:
		return nil, 0, fmt.Errorf("a file placeholder's encoding is %q, not base64 or dataUrl", found.Encoding)
	}
	text, _ := json.Marshal(encoded)
	return &jsonNode{raw: text}, int64(len(file.Data)), nil
}
