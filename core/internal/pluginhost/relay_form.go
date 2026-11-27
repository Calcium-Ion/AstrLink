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

	"github.com/QuantumNous/new-api/pluginkit"
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

// input is what convertRequest reads of the form: the fields, the
// upstream model in model, and the files without their content.
func (form *clientForm) input(model string) *pluginkit.RequestForm {
	fields := maps.Clone(form.fields)
	if _, ok := fields["model"]; ok {
		fields["model"] = []string{model}
	}
	converted := &pluginkit.RequestForm{Fields: fields, Files: []pluginkit.RequestFile{}}
	for _, file := range form.files {
		converted.Files = append(converted.Files, pluginkit.RequestFile{Field: file.field, Filename: file.filename, ContentType: file.contentType, Size: int64(len(file.data))})
	}
	return converted
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
	var payload []byte
	contentType := request.ContentType
	switch {
	case request.Form != nil:
		written, boundary, err := writeConvertedForm(request.Form, form)
		if err != nil {
			return nil, "", err
		}
		payload, contentType = written, "multipart/form-data; boundary="+boundary
	case request.File != "":
		file, err := form.file(request.File)
		if err != nil {
			return nil, "", err
		}
		payload = file.data
		if contentType == "" {
			contentType = file.contentType
		}
	case body != nil:
		replaced, err := replaceFileMarkers(body, form)
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

// replaceFileMarkers replaces each {"$file": name, "encoding": ...} in a
// JSON body with the file's content, base64 or a data: URL. Any other body
// is sent as it is.
func replaceFileMarkers(body []byte, form *clientForm) ([]byte, error) {
	if !bytes.Contains(body, []byte(`"$file"`)) || !json.Valid(body) {
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
func writeConvertedForm(converted *pluginkit.ConvertedForm, form *clientForm) ([]byte, string, error) {
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	for _, name := range slices.Sorted(maps.Keys(converted.Fields)) {
		for _, value := range converted.Fields[name] {
			if err := writer.WriteField(name, value); err != nil {
				return nil, "", err
			}
		}
	}
	for _, entry := range converted.Files {
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
