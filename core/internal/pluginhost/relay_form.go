package pluginhost

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"mime"
	"mime/multipart"
	"net/textproto"
	"slices"
)

// A transcription, translation or image edit comes as a multipart form.
// convertRequest reads its fields and the names of its files, not their
// content, and its result sends the files: in a JSON body through $file
// markers, in a form the host writes, or as the whole body.

// maxConvertedBody bounds the body of a converted request.
const maxConvertedBody = 64 << 20

// clientFile is a file of the client's form.
type clientFile struct {
	field       string
	name        string
	filename    string
	contentType string
	data        []byte
}

// clientForm is the client's multipart form.
type clientForm struct {
	fields map[string][]string
	files  []clientFile
}

// readClientForm reads a multipart body; it is nil for any other.
func readClientForm(contentType string, body []byte) (*clientForm, error) {
	media, params, err := mime.ParseMediaType(contentType)
	if err != nil || media != "multipart/form-data" || params["boundary"] == "" {
		return nil, nil
	}
	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	form := &clientForm{fields: map[string][]string{}}
	counts := map[string]int{}
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			return form, nil
		}
		if err != nil {
			return nil, fmt.Errorf("the client's form cannot be read: %w", err)
		}
		data, err := io.ReadAll(part)
		if err != nil {
			return nil, fmt.Errorf("the client's form cannot be read: %w", err)
		}
		field := part.FormName()
		if part.FileName() == "" {
			form.fields[field] = append(form.fields[field], string(data))
			continue
		}
		// A field's first file is named by the field, a later one by
		// field#index from zero.
		name := field
		if index := counts[field]; index > 0 {
			name = fmt.Sprintf("%s#%d", field, index)
		}
		counts[field]++
		contentType := part.Header.Get("Content-Type")
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		form.files = append(form.files, clientFile{field: field, name: name, filename: part.FileName(), contentType: contentType, data: data})
	}
}

// view is what convertRequest reads of the form: the fields, a field with
// several values as an array, the upstream model in model, and the files
// without their content.
func (form *clientForm) view(model string) map[string]any {
	fields := map[string]any{}
	for name, values := range form.fields {
		if len(values) == 1 {
			fields[name] = values[0]
		} else {
			fields[name] = values
		}
	}
	if _, ok := fields["model"]; ok {
		fields["model"] = model
	}
	files := []map[string]any{}
	for _, file := range form.files {
		files = append(files, map[string]any{"field": file.field, "filename": file.filename, "contentType": file.contentType, "size": len(file.data)})
	}
	return map[string]any{"fields": fields, "files": files}
}

// file is the client file a result names.
func (form *clientForm) file(name string) (clientFile, error) {
	if form != nil {
		for _, file := range form.files {
			if file.name == name {
				return file, nil
			}
		}
	}
	return clientFile{}, fmt.Errorf("the conversion names the file %q, which the client did not send", name)
}

// payloadOf is the bytes and content type a converted request sends: its
// body with the $file markers replaced, its form, or a client file. body
// is the body as prepareRequest left it.
func payloadOf(request convertedRequest, body []byte, form *clientForm) ([]byte, string, error) {
	members := 0
	for _, raw := range []json.RawMessage{request.Body, request.Form, request.File, request.Websocket} {
		if present(raw) {
			members++
		}
	}
	if members > 1 {
		return nil, "", errors.New("a converted request has more than one of body, form, file and websocket")
	}
	var payload []byte
	contentType := request.ContentType
	switch {
	case present(request.Form):
		written, boundary, err := writeConvertedForm(request.Form, form)
		if err != nil {
			return nil, "", err
		}
		payload, contentType = written, "multipart/form-data; boundary="+boundary
	case present(request.File):
		var name string
		if err := json.Unmarshal(request.File, &name); err != nil {
			return nil, "", errors.New("a converted request's file is the name of a client file")
		}
		file, err := form.file(name)
		if err != nil {
			return nil, "", err
		}
		payload = file.data
		if contentType == "" {
			contentType = file.contentType
		}
	default:
		replaced, err := replaceFileMarkers(request.Body, body, form)
		if err != nil {
			return nil, "", err
		}
		payload = replaced
		if contentType == "" {
			contentType = "application/json"
		}
	}
	if len(payload) > maxConvertedBody {
		return nil, "", fmt.Errorf("the converted request is larger than %d MiB", maxConvertedBody>>20)
	}
	return payload, contentType, nil
}

func present(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null"))
}

// replaceFileMarkers replaces each {"$file": name, "encoding": ...} in a
// JSON body with the file's content, base64 or a data: URL. A string body
// is sent as it is.
func replaceFileMarkers(raw json.RawMessage, body []byte, form *clientForm) ([]byte, error) {
	if trimmed := bytes.TrimSpace(raw); len(trimmed) > 0 && trimmed[0] == '"' {
		return body, nil
	}
	if !bytes.Contains(body, []byte(`"$file"`)) {
		return body, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var document any
	if err := decoder.Decode(&document); err != nil {
		return body, nil
	}
	if form == nil {
		return nil, errors.New("$file markers are valid only for a multipart client request")
	}
	var walk func(any) (any, error)
	walk = func(value any) (any, error) {
		switch typed := value.(type) {
		case map[string]any:
			if name, ok := typed["$file"].(string); ok {
				file, err := form.file(name)
				if err != nil {
					return nil, err
				}
				encoded := base64.StdEncoding.EncodeToString(file.data)
				switch typed["encoding"] {
				case nil, "base64":
					return encoded, nil
				case "dataUrl":
					return "data:" + file.contentType + ";base64," + encoded, nil
				}
				return nil, fmt.Errorf("the $file marker of %q has an encoding other than base64 or dataUrl", name)
			}
			for key, member := range typed {
				replaced, err := walk(member)
				if err != nil {
					return nil, err
				}
				typed[key] = replaced
			}
		case []any:
			for index, member := range typed {
				replaced, err := walk(member)
				if err != nil {
					return nil, err
				}
				typed[index] = replaced
			}
		}
		return value, nil
	}
	replaced, err := walk(document)
	if err != nil {
		return nil, err
	}
	return json.Marshal(replaced)
}

// writeConvertedForm writes the form a result describes: its fields, then
// its files from the client's.
func writeConvertedForm(raw json.RawMessage, form *clientForm) ([]byte, string, error) {
	var converted struct {
		Fields map[string]json.RawMessage `json:"fields"`
		Files  []struct {
			Field       string `json:"field"`
			From        string `json:"from"`
			Filename    string `json:"filename"`
			ContentType string `json:"contentType"`
		} `json:"files"`
	}
	if err := json.Unmarshal(raw, &converted); err != nil {
		return nil, "", errors.New("a converted request's form is {fields, files}")
	}
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	for _, name := range slices.Sorted(maps.Keys(converted.Fields)) {
		var values []string
		var single string
		if err := json.Unmarshal(converted.Fields[name], &single); err == nil {
			values = []string{single}
		} else if err := json.Unmarshal(converted.Fields[name], &values); err != nil {
			return nil, "", fmt.Errorf("the form field %q is a string or an array of strings", name)
		}
		for _, value := range values {
			if err := writer.WriteField(name, value); err != nil {
				return nil, "", err
			}
		}
	}
	for _, entry := range converted.Files {
		if entry.Field == "" {
			return nil, "", errors.New("a file of a converted form has no field")
		}
		file, err := form.file(entry.From)
		if err != nil {
			return nil, "", err
		}
		filename, contentType := file.filename, file.contentType
		if entry.Filename != "" {
			filename = entry.Filename
		}
		if entry.ContentType != "" {
			contentType = entry.ContentType
		}
		header := textproto.MIMEHeader{}
		header.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{"name": entry.Field, "filename": filename}))
		header.Set("Content-Type", contentType)
		part, err := writer.CreatePart(header)
		if err != nil {
			return nil, "", err
		}
		if _, err := part.Write(file.data); err != nil {
			return nil, "", err
		}
		if buffer.Len() > maxConvertedBody {
			return nil, "", fmt.Errorf("the converted request is larger than %d MiB", maxConvertedBody>>20)
		}
	}
	if err := writer.Close(); err != nil {
		return nil, "", err
	}
	return buffer.Bytes(), writer.Boundary(), nil
}
