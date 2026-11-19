package taskplugin

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

// Artifact is an output of a finished task the plugin lists: a key, its
// kind and media type.
type Artifact struct {
	Key      string `json:"key"`
	Type     string `json:"type"`
	MimeType string `json:"mimeType,omitempty"`
}

// artifactLink is an artifact as ctx.artifacts holds it for a renderer.
type artifactLink struct {
	Key      string `json:"key"`
	Type     string `json:"type"`
	MimeType string `json:"mimeType,omitempty"`
	URL      string `json:"url"`
}

// resultArtifact is the key of the one artifact a task without
// listArtifacts has: the result its parse hook named.
const resultArtifact = "result"

// Artifacts lists a finished task's outputs: what the plugin's
// listArtifacts says, else the result its parse hook named.
func (engine *Engine) Artifacts(ctx context.Context, task storage.PluginTask) ([]Artifact, error) {
	if task.Status != contract.PluginTaskSuccess {
		return []Artifact{}, nil
	}
	target, _, err := engine.plugin(task.PluginKey)
	if err != nil {
		return nil, err
	}
	return engine.listArtifacts(ctx, target, task)
}

func (engine *Engine) listArtifacts(ctx context.Context, target *plugin.Plugin, task storage.PluginTask) ([]Artifact, error) {
	artifacts := []Artifact{}
	if target.HasTaskHook("listArtifacts") {
		argument := map[string]any{"taskId": task.ID, "status": task.Status, "action": task.Action, "data": jsonOrNull(task.Data), "producerVersion": task.PluginVersion}
		raw, err := engine.renderCall(ctx, target, "listArtifacts", argument)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &artifacts); err != nil {
			return nil, failure(http.StatusBadGateway, "plugin_error", "listArtifacts returned something other than a list of artifacts")
		}
		valid := artifacts[:0]
		for _, artifact := range artifacts {
			if artifact.Key != "" && len(artifact.Key) <= 64 && !strings.ContainsAny(artifact.Key, "/?#") {
				valid = append(valid, artifact)
			}
		}
		if len(valid) > 0 || task.ResultURL == "" {
			return valid, nil
		}
		artifacts = valid
	}
	if task.ResultURL != "" {
		kind, mediaType := "file", ""
		if stored, ok := storedResult(task); ok {
			mediaType = stored
		}
		switch {
		case strings.HasPrefix(mediaType, "video/") || task.Surface == plugin.TaskProtocolVideo:
			kind = "video"
		case strings.HasPrefix(mediaType, "image/") || task.Surface == plugin.TaskProtocolImage:
			kind = "image"
		case strings.HasPrefix(mediaType, "audio/"):
			kind = "audio"
		}
		artifacts = append(artifacts, Artifact{Key: resultArtifact, Type: kind, MimeType: mediaType})
	}
	return artifacts, nil
}

// artifactLinks are the artifacts of a finished task as a renderer reads
// them, each with the link links makes; nil when there are none.
func (engine *Engine) artifactLinks(ctx context.Context, target *plugin.Plugin, task storage.PluginTask, links func(key string) string) map[string]artifactLink {
	if links == nil {
		return nil
	}
	artifacts, err := engine.listArtifacts(ctx, target, task)
	if err != nil {
		engine.logf("task %s: listArtifacts: %v", task.ID, err)
		return map[string]artifactLink{}
	}
	result := map[string]artifactLink{}
	for _, artifact := range artifacts {
		result[artifact.Key] = artifactLink{Key: artifact.Key, Type: artifact.Type, MimeType: artifact.MimeType, URL: links(artifact.Key)}
	}
	return result
}

// Access is the capability that opens one artifact of one task without the
// owner's token: a signature of both, which does not expire.
func (engine *Engine) Access(id, key string) string {
	mac := hmac.New(sha256.New, engine.signingKey)
	mac.Write([]byte("artifact\x00" + id + "\x00" + key))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// VerifyAccess reports whether access opens the artifact key of task id.
func (engine *Engine) VerifyAccess(id, key, access string) bool {
	return access != "" && hmac.Equal([]byte(engine.Access(id, key)), []byte(access))
}

// ArtifactURL is the link that serves an artifact under base, the address
// the client reached the gateway at.
func (engine *Engine) ArtifactURL(base, id, key string) string {
	return strings.TrimRight(base, "/") + "/v1/tasks/" + url.PathEscape(id) + "/artifacts/" + url.PathEscape(key) + "/content?access=" + engine.Access(id, key)
}

// contentHeaders are the client headers a download passes on, and the
// answer headers it passes back.
var (
	contentRequestHeaders = []string{"Range", "If-Range", "If-None-Match", "If-Modified-Since", "If-Match", "If-Unmodified-Since"}
	contentAnswerHeaders  = []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "Etag", "Last-Modified", "Content-Disposition", "Cache-Control"}
)

// ServeContent writes an artifact of a finished task: through the
// plugin's buildContentRequest, from the result kept beside the task, or
// from the link the vendor named.
func (engine *Engine) ServeContent(ctx context.Context, writer http.ResponseWriter, request *http.Request, task storage.PluginTask, key string) error {
	if task.Status != contract.PluginTaskSuccess {
		return failure(http.StatusNotFound, "artifact_not_found", "the task has not finished")
	}
	target, meta, err := engine.plugin(task.PluginKey)
	if err != nil {
		return err
	}
	artifacts, err := engine.listArtifacts(ctx, target, task)
	if err != nil {
		return err
	}
	var artifact *Artifact
	for index := range artifacts {
		if artifacts[index].Key == key {
			artifact = &artifacts[index]
		}
	}
	if artifact == nil {
		return failure(http.StatusNotFound, "artifact_not_found", "the task has no artifact %s", key)
	}
	method := http.MethodGet
	if request.Method == http.MethodHead {
		method = http.MethodHead
	}
	client := http.Header{}
	for _, name := range contentRequestHeaders {
		if value := request.Header.Get(name); value != "" {
			client.Set(name, value)
		}
	}
	service, err := engine.taskService(ctx, task)
	if err != nil {
		return failure(http.StatusServiceUnavailable, "provider_unavailable", "the provider of the task is gone")
	}
	if key != resultArtifact && target.HasTaskHook("buildContentRequest") {
		found, err := engine.credentialsFor(ctx, service, meta)
		if err != nil {
			return err
		}
		call := &exchange{engine: engine, target: target, service: service, meta: meta, credentials: found}
		driver := map[string]any{
			"requestBody": jsonOrNull(task.Request), "requestHeaders": map[string]string{}, "action": task.Action, "model": task.Model,
			"upstreamModel": task.UpstreamModel, "baseUrl": found.baseURL, "apiKey": found.apiKey, "authHeader": found.authHeader,
			"auth": found.auth, "upstream": found.upstream, "files": []any{}, "publicTaskId": task.ID, "userSetting": settingsOrEmpty(found.settings),
			"artifactKey": key, "data": jsonOrNull(task.Data), "state": jsonOrNull(task.State), "upstreamTaskId": upstreamTaskID(task),
			"clientRequest": map[string]any{"method": method, "headers": firstHeaders(client)},
		}
		raw, err := call.call(ctx, "buildContentRequest", driver)
		if err != nil {
			return err
		}
		desc, err := parseDescriptor("buildContentRequest", raw)
		if err != nil {
			return err
		}
		if len(desc.ContentPath) > 0 {
			path, err := contentPath(desc.ContentPath)
			if err != nil {
				return err
			}
			// The bytes are a value of the reply, so the client's range
			// and validators do not go with the request.
			response, err := call.send(ctx, "buildContentRequest", desc, http.Header{})
			if err != nil {
				return err
			}
			return serveInlineContent(writer, method, response, path, desc.MimeType)
		}
		headers := client
		if !desc.Credentialless {
			// The plugin's own headers carry the key; the client's
			// range comes after.
			headers = client.Clone()
		}
		response, err := call.send(ctx, "buildContentRequest", desc, headers)
		if err != nil {
			return err
		}
		return relayContent(writer, response, artifact)
	}
	if mediaType, ok := storedResult(task); ok {
		file, err := os.Open(engine.resultPath(task.ID))
		if err != nil {
			return failure(http.StatusNotFound, "artifact_not_found", "the result is no longer kept")
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil {
			return failure(http.StatusNotFound, "artifact_not_found", "the result is no longer kept")
		}
		writer.Header().Set("Content-Type", mediaType)
		http.ServeContent(writer, request, "", info.ModTime(), file)
		return nil
	}
	address, err := url.Parse(task.ResultURL)
	if err != nil || (address.Scheme != "http" && address.Scheme != "https") {
		return failure(http.StatusNotFound, "artifact_not_found", "the task has no result to download")
	}
	call := &exchange{engine: engine, target: target, service: service, meta: meta}
	response, err := call.download(ctx, "result", method, address, &descriptor{}, false, client)
	if err != nil {
		return err
	}
	return relayContent(writer, response, artifact)
}

// maxInlineContent bounds the reply of a long-running operation that holds
// an artifact in base64, which is read whole: four thirds of a 64 MiB
// artifact, as new-api's default MAX_FILE_DOWNLOAD_MB, and 1 MiB.
const maxInlineContent = (64<<20)/3*4 + 1<<20

// contentPath reads a contentPath: string keys and non-negative integer
// indices, at most 32 of them.
func contentPath(raw []json.RawMessage) ([]any, error) {
	if len(raw) > 32 {
		return nil, failure(http.StatusBadGateway, "plugin_error", "buildContentRequest returned a contentPath of more than 32 segments")
	}
	path := make([]any, 0, len(raw))
	for _, segment := range raw {
		var key string
		if json.Unmarshal(segment, &key) == nil {
			path = append(path, key)
			continue
		}
		var index int
		if json.Unmarshal(segment, &index) != nil || index < 0 {
			return nil, failure(http.StatusBadGateway, "plugin_error", "buildContentRequest returned a contentPath segment that is neither a key nor an index")
		}
		path = append(path, index)
	}
	return path, nil
}

// serveInlineContent serves the artifact a long-running operation holds in
// base64 at path, as new-api does: an error member answers 502, an
// operation not yet done 409 and a missing value 410. Other statuses are
// read as any download's.
func serveInlineContent(writer http.ResponseWriter, method string, response *http.Response, path []any, mediaType string) error {
	if response.StatusCode != http.StatusOK {
		return relayContent(writer, response, &Artifact{})
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxInlineContent+1))
	if err != nil {
		return &Error{Status: http.StatusBadGateway, Code: "artifact_upstream_error", Message: "the artifact could not be read"}
	}
	if len(body) > maxInlineContent {
		return &Error{Status: http.StatusBadGateway, Code: "artifact_request_rejected", Message: "the artifact is too large"}
	}
	var operation map[string]any
	if json.Unmarshal(body, &operation) != nil {
		return &Error{Status: http.StatusBadGateway, Code: "artifact_upstream_error", Message: "the operation is not JSON"}
	}
	if failed, ok := operation["error"]; ok && failed != nil {
		message := "the operation failed"
		if detail, ok := failed.(map[string]any); ok {
			if text, ok := detail["message"].(string); ok && text != "" {
				message = text
			}
		}
		return &Error{Status: http.StatusBadGateway, Code: "artifact_upstream_error", Message: message}
	}
	if operation["done"] != true && operation["status"] != "completed" {
		return &Error{Status: http.StatusConflict, Code: "artifact_not_ready", Message: "the operation has not finished"}
	}
	var value any = operation
	for _, segment := range path {
		switch key := segment.(type) {
		case string:
			object, _ := value.(map[string]any)
			value = object[key]
		case int:
			array, _ := value.([]any)
			if key >= len(array) {
				value = nil
			} else {
				value = array[key]
			}
		}
	}
	encoded, _ := value.(string)
	if encoded == "" {
		return &Error{Status: http.StatusGone, Code: "artifact_gone", Message: "the operation no longer holds the artifact"}
	}
	var content []byte
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.RawURLEncoding} {
		if content, err = encoding.DecodeString(encoded); err == nil {
			break
		}
	}
	if err != nil {
		return &Error{Status: http.StatusBadGateway, Code: "artifact_upstream_error", Message: "the artifact is not base64"}
	}
	if mediaType == "" {
		mediaType = "video/mp4"
	}
	header := writer.Header()
	header.Set("Content-Type", mediaType)
	header.Set("Content-Length", strconv.Itoa(len(content)))
	writer.WriteHeader(http.StatusOK)
	if method != http.MethodHead {
		_, _ = writer.Write(content)
	}
	return nil
}

// relayContent passes a download back to the client: its status, the
// headers of the content, and the bytes.
func relayContent(writer http.ResponseWriter, response *http.Response, artifact *Artifact) error {
	defer response.Body.Close()
	switch {
	case response.StatusCode == http.StatusNotModified, response.StatusCode == http.StatusRequestedRangeNotSatisfiable:
	case response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusGone:
		return failure(http.StatusNotFound, "artifact_not_found", "the vendor no longer has the artifact")
	case response.StatusCode < 200 || response.StatusCode > 299:
		body, _ := io.ReadAll(io.LimitReader(response.Body, maxErrorAnswer))
		message := upstreamMessage(answerValue(body))
		if message == "" {
			message = http.StatusText(response.StatusCode)
		}
		return &Error{Status: http.StatusBadGateway, Code: "upstream_error", Message: fmt.Sprintf("the download failed: %s", message)}
	}
	for _, name := range contentAnswerHeaders {
		if value := response.Header.Get(name); value != "" {
			writer.Header().Set(name, value)
		}
	}
	if writer.Header().Get("Content-Type") == "" && artifact.MimeType != "" {
		writer.Header().Set("Content-Type", artifact.MimeType)
	}
	writer.WriteHeader(response.StatusCode)
	if _, err := io.Copy(writer, response.Body); err != nil && !errors.Is(err, context.Canceled) {
		return nil
	}
	return nil
}
