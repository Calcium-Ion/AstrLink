// Package builtintools executes explicitly configured Responses hosted tools.
package builtintools

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/networkproxy"
	"github.com/QuantumNous/astrlink/core/internal/relaykitbridge"
	"github.com/QuantumNous/astrlink/core/internal/secretstore"
)

type Object = map[string]any

const MaxImageBytes = 20 << 20
const MaxResultBytes = 32 << 20

type Invocation struct {
	Kind      string
	Arguments Object
	Options   Object
	Images    []string
}

type Result struct {
	Items   []Object
	Output  string
	Images  []string
	Sources []Object
	Usage   Object
}

type Executor struct {
	Secrets secretstore.SecretStore
	Client  *http.Client
	Native  func(context.Context, contract.BuiltinTool, Object) (Object, error)
	// Images posts to a configured provider's Images API path with that
	// provider's own credential, proxy and header policy.
	Images  func(ctx context.Context, config contract.BuiltinTool, path, contentType string, body io.Reader) (Object, error)
	Inspect func(context.Context, Object) (Object, error)
}

func ID(prefix string) string {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		panic(err)
	}
	return prefix + hex.EncodeToString(data[:])
}

func String(value any) string { result, _ := value.(string); return result }
func Map(value any) Object    { result, _ := value.(map[string]any); return result }
func Array(value any) []any   { result, _ := value.([]any); return result }
func Clone(value Object) Object {
	body, _ := json.Marshal(value)
	var result Object
	_ = json.Unmarshal(body, &result)
	return result
}
func Text(value any) string { data, _ := json.Marshal(value); return string(data) }

func (executor Executor) Execute(ctx context.Context, config contract.BuiltinTool, call Invocation) (Result, error) {
	if err := config.Validate(call.Kind); err != nil {
		return Result{}, err
	}
	if !config.Enabled {
		return Result{}, fmt.Errorf("tool is disabled")
	}
	timeout := 30 * time.Second
	if call.Kind == "image_generation" {
		timeout = 180 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	allowedArgs := "action query url pattern"
	if call.Kind == "image_generation" {
		allowedArgs = "prompt image_indexes"
	}
	if err := checkOptions(call.Arguments, allowedArgs); err != nil {
		return Result{}, err
	}
	if len(call.Images) > 8 {
		return Result{}, fmt.Errorf("at most eight image references are supported")
	}
	if executor.Inspect != nil {
		args, err := executor.Inspect(ctx, call.Arguments)
		if err != nil {
			return Result{}, err
		}
		call.Arguments = args
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if config.Backend == "upstream" {
		return executor.native(ctx, config, call)
	}
	if call.Kind == "web_search" {
		return executor.search(ctx, config, call)
	}
	return executor.image(ctx, config, call)
}

func (executor Executor) request(ctx context.Context, kind, base, path, contentType string, body io.Reader) (Object, error) {
	if executor.Secrets == nil {
		return nil, fmt.Errorf("tool credential store is unavailable")
	}
	secret, err := executor.Secrets.Get(ctx, secretstore.Ref("local://builtin-tool/"+kind))
	if err != nil {
		return nil, fmt.Errorf("tool API key is unavailable")
	}
	defer clear(secret)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/")+path, body)
	if err != nil {
		return nil, fmt.Errorf("invalid tool API URL")
	}
	request.Header.Set("Authorization", "Bearer "+string(secret))
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("User-Agent", "")
	client := executor.Client
	if client == nil {
		client = networkproxy.WrapClient(&http.Client{})
	}
	copyClient := *client
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := copyClient.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("tool API connection failed")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("tool API returned HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, MaxResultBytes+1))
	if err != nil || len(data) > MaxResultBytes {
		return nil, fmt.Errorf("tool response exceeds limit or was interrupted")
	}
	var result Object
	if json.Unmarshal(data, &result) != nil || result == nil {
		return nil, fmt.Errorf("invalid tool API response")
	}
	return result, nil
}

func (executor Executor) jsonRequest(ctx context.Context, kind string, config contract.BuiltinTool, path string, body Object) (Object, error) {
	return executor.request(ctx, kind, config.BaseURL, path, "application/json", strings.NewReader(Text(body)))
}

func (executor Executor) imageRequest(ctx context.Context, config contract.BuiltinTool, path, contentType string, body io.Reader) (Object, error) {
	if config.Backend != "service_images" {
		return executor.request(ctx, "image_generation", config.BaseURL, path, contentType, body)
	}
	if executor.Images == nil {
		return nil, fmt.Errorf("provider Images API is unavailable")
	}
	return executor.Images(ctx, config, path, contentType, body)
}

func checkOptions(options Object, allowed string) error {
	for key := range options {
		if !strings.Contains(" "+allowed+" ", " "+key+" ") {
			return fmt.Errorf("unsupported tool option: %s", key)
		}
	}
	return nil
}

func publicURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil
}

func (executor Executor) search(ctx context.Context, config contract.BuiltinTool, call Invocation) (Result, error) {
	if err := checkOptions(call.Options, "type search_context_size filters external_web_access user_location"); err != nil {
		return Result{}, err
	}
	if call.Options["user_location"] != nil {
		return Result{}, fmt.Errorf("Tavily backend does not support precise user_location")
	}
	if call.Options["external_web_access"] == false {
		return Result{}, fmt.Errorf("Tavily backend does not support offline search")
	}
	action := String(call.Arguments["action"])
	if action == "" {
		action = "search"
	}
	payload := Object{}
	path := "/search"
	switch action {
	case "search":
		query := String(call.Arguments["query"])
		if strings.TrimSpace(query) == "" {
			return Result{}, fmt.Errorf("search query is required")
		}
		payload = Object{"query": query, "max_results": 5, "include_raw_content": true, "include_answer": false, "search_depth": "basic"}
		if call.Options["search_context_size"] == "high" {
			payload["max_results"] = 10
			payload["search_depth"] = "advanced"
		}
		filters := Map(call.Options["filters"])
		if err := checkOptions(filters, "allowed_domains blocked_domains"); err != nil {
			return Result{}, err
		}
		if value := filters["allowed_domains"]; value != nil {
			payload["include_domains"] = value
		}
		if value := filters["blocked_domains"]; value != nil {
			payload["exclude_domains"] = value
		}
	case "open", "find":
		raw := String(call.Arguments["url"])
		if !publicURL(raw) {
			return Result{}, fmt.Errorf("a public HTTP(S) page URL is required")
		}
		path = "/extract"
		payload = Object{"urls": []string{raw}, "format": "text"}
	default:
		return Result{}, fmt.Errorf("unsupported search action")
	}
	response, err := executor.jsonRequest(ctx, "web_search", config, path, payload)
	if err != nil {
		return Result{}, err
	}
	if _, ok := response["results"].([]any); !ok {
		return Result{}, fmt.Errorf("search backend returned invalid results")
	}
	sources := []Object{}
	records := []Object{}
	for _, raw := range Array(response["results"]) {
		item := Map(raw)
		address := String(item["url"])
		if !publicURL(address) {
			continue
		}
		content := String(item["raw_content"])
		if content == "" {
			content = String(item["content"])
		}
		if action == "find" {
			pattern := String(call.Arguments["pattern"])
			if pattern == "" {
				return Result{}, fmt.Errorf("find pattern is required")
			}
			at := strings.Index(strings.ToLower(content), strings.ToLower(pattern))
			if at < 0 {
				content = "Pattern not found in retrieved page."
			} else {
				start := max(0, at-250)
				end := min(len(content), at+len(pattern)+500)
				content = content[start:end]
			}
		}
		if len(content) > 32000 {
			content = string([]rune(content)[:min(len([]rune(content)), 8000)])
		}
		sources = append(sources, Object{"type": "url", "url": address, "title": String(item["title"])})
		records = append(records, Object{"url": address, "title": String(item["title"]), "content": content})
	}
	if action != "search" && len(records) == 0 {
		return Result{}, fmt.Errorf("page extraction returned no usable content")
	}
	wireAction := Object{"type": "search", "query": call.Arguments["query"], "sources": sources}
	if action == "open" {
		wireAction = Object{"type": "open_page", "url": call.Arguments["url"]}
	}
	if action == "find" {
		wireAction = Object{"type": "find_in_page", "url": call.Arguments["url"], "pattern": call.Arguments["pattern"]}
	}
	item := Object{"id": ID("ws_"), "type": "web_search_call", "status": "completed", "action": wireAction}
	return Result{Items: []Object{item}, Output: Text(Object{"results": records, "citation_instruction": "Cite sources using their exact URL in Markdown links."}), Sources: sources, Usage: Map(response["usage"])}, nil
}

func (executor Executor) image(ctx context.Context, config contract.BuiltinTool, call Invocation) (Result, error) {
	if err := checkOptions(call.Options, "type model action size quality background output_format output_compression moderation partial_images input_image_mask"); err != nil {
		return Result{}, err
	}
	if value, ok := call.Options["partial_images"]; ok && value != float64(0) && value != 0 {
		return Result{}, fmt.Errorf("external image backend does not support partial_images")
	}
	prompt := String(call.Arguments["prompt"])
	if strings.TrimSpace(prompt) == "" {
		return Result{}, fmt.Errorf("image prompt is required")
	}
	payload := Object{"model": config.Model, "prompt": prompt, "n": 1}
	for _, key := range []string{"size", "quality", "background", "output_format", "output_compression", "moderation"} {
		if value, ok := call.Options[key]; ok {
			payload[key] = value
		}
	}
	images, err := selectedImages(call)
	if err != nil {
		return Result{}, err
	}
	if call.Options["action"] == "generate" {
		images = nil
	}
	if call.Options["action"] == "edit" && len(images) == 0 {
		return Result{}, fmt.Errorf("image editing requires an input image")
	}
	var response Object
	if len(images) == 0 {
		if call.Options["input_image_mask"] != nil {
			return Result{}, fmt.Errorf("image mask requires an input image")
		}
		response, err = executor.imageRequest(ctx, config, "/images/generations", "application/json", strings.NewReader(Text(payload)))
	} else {
		var buffer bytes.Buffer
		form := multipart.NewWriter(&buffer)
		for key, value := range payload {
			text := String(value)
			if text == "" {
				text = Text(value)
			}
			if err = form.WriteField(key, text); err != nil {
				return Result{}, err
			}
		}
		for _, raw := range images {
			if err = writeImagePart(ctx, form, "image[]", raw); err != nil {
				return Result{}, err
			}
		}
		if mask := Map(call.Options["input_image_mask"]); mask != nil {
			if mask["file_id"] != nil || String(mask["image_url"]) == "" {
				return Result{}, fmt.Errorf("mask requires a resolvable image_url")
			}
			if err = writeImagePart(ctx, form, "mask", String(mask["image_url"])); err != nil {
				return Result{}, err
			}
		}
		if err = form.Close(); err != nil {
			return Result{}, err
		}
		response, err = executor.imageRequest(ctx, config, "/images/edits", form.FormDataContentType(), &buffer)
	}
	if err != nil {
		return Result{}, err
	}
	result := Result{Usage: Map(response["usage"])}
	for _, raw := range Array(response["data"]) {
		entry := Map(raw)
		encoded := String(entry["b64_json"])
		format := String(payload["output_format"])
		if format == "" {
			format = "png"
		}
		if encoded == "" && entry["url"] != nil {
			var mime string
			encoded, mime, err = relaykitbridge.ResolveImageData(ctx, String(entry["url"]))
			format = strings.TrimPrefix(mime, "image/")
		}
		if err != nil {
			return Result{}, fmt.Errorf("could not load generated image")
		}
		data, decodeErr := base64.StdEncoding.DecodeString(encoded)
		if decodeErr != nil || len(data) == 0 || len(data) > MaxImageBytes || !strings.HasPrefix(http.DetectContentType(data), "image/") {
			return Result{}, fmt.Errorf("invalid or oversized generated image")
		}
		id := ID("ig_")
		item := Object{"id": id, "type": "image_generation_call", "status": "completed", "result": encoded, "output_format": format, "revised_prompt": entry["revised_prompt"]}
		result.Items = append(result.Items, item)
		result.Images = append(result.Images, "data:image/"+format+";base64,"+encoded)
	}
	if len(result.Items) == 0 {
		return Result{}, fmt.Errorf("image backend returned no image")
	}
	result.Output = "Image generation succeeded. The generated images are attached to this response."
	return result, nil
}

func selectedImages(call Invocation) ([]string, error) {
	indexes, ok := call.Arguments["image_indexes"]
	if !ok {
		return call.Images, nil
	}
	if _, ok := indexes.([]any); !ok {
		return nil, fmt.Errorf("image_indexes must be an array")
	}
	result := []string{}
	for _, raw := range Array(indexes) {
		index, ok := raw.(float64)
		if !ok || index != float64(int(index)) || index < 0 || int(index) >= len(call.Images) {
			return nil, fmt.Errorf("unknown image reference")
		}
		result = append(result, call.Images[int(index)])
	}
	return result, nil
}

func writeImagePart(ctx context.Context, form *multipart.Writer, field, raw string) error {
	encoded, mime, err := relaykitbridge.ResolveImageData(ctx, raw)
	if err != nil {
		return fmt.Errorf("input image could not be resolved")
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(data) > MaxImageBytes {
		return fmt.Errorf("invalid or oversized image")
	}
	part, err := form.CreateFormFile(field, "image."+strings.TrimPrefix(mime, "image/"))
	if err != nil {
		return err
	}
	_, err = part.Write(data)
	return err
}

func (executor Executor) native(ctx context.Context, config contract.BuiltinTool, call Invocation) (Result, error) {
	if executor.Native == nil {
		return Result{}, fmt.Errorf("native tool executor is unavailable")
	}
	options := Clone(call.Options)
	options["type"] = call.Kind
	content := []any{Object{"type": "input_text", "text": Text(call.Arguments)}}
	if call.Kind == "image_generation" {
		images, err := selectedImages(call)
		if err != nil {
			return Result{}, err
		}
		for _, raw := range images {
			content = append(content, Object{"type": "input_image", "image_url": raw})
		}
	}
	body := Object{"model": config.Model, "input": []any{Object{"role": "user", "content": content}}, "tools": []any{options}, "tool_choice": Object{"type": call.Kind}, "stream": true, "store": false}
	if call.Kind == "web_search" {
		body["include"] = []string{"web_search_call.action.sources"}
	}
	response, err := executor.Native(ctx, config, body)
	if err != nil {
		return Result{}, err
	}
	result := Result{Usage: Map(response["usage"])}
	var texts []string
	for _, raw := range Array(response["output"]) {
		item := Map(raw)
		switch String(item["type"]) {
		case "web_search_call":
			if call.Kind != "web_search" {
				continue
			}
			result.Items = append(result.Items, item)
			for _, source := range Array(Map(item["action"])["sources"]) {
				if value := Map(source); publicURL(String(value["url"])) {
					result.Sources = append(result.Sources, value)
				}
			}
		case "image_generation_call":
			if call.Kind != "image_generation" {
				continue
			}
			encoded := String(item["result"])
			data, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil || len(data) == 0 || len(data) > MaxImageBytes || !strings.HasPrefix(http.DetectContentType(data), "image/") {
				return Result{}, fmt.Errorf("native tool returned invalid image")
			}
			result.Items = append(result.Items, item)
			result.Images = append(result.Images, "data:"+http.DetectContentType(data)+";base64,"+encoded)
		case "message":
			for _, raw := range Array(item["content"]) {
				part := Map(raw)
				if text := String(part["text"]); text != "" {
					texts = append(texts, text)
				}
				for _, raw := range Array(part["annotations"]) {
					annotation := Map(raw)
					if publicURL(String(annotation["url"])) {
						result.Sources = append(result.Sources, Object{"type": "url", "url": annotation["url"], "title": annotation["title"]})
					}
				}
			}
		}
	}
	if len(result.Items) == 0 {
		return Result{}, fmt.Errorf("selected provider did not execute the requested builtin tool")
	}
	for _, item := range result.Items {
		if item["status"] != "completed" {
			return Result{}, fmt.Errorf("native tool did not complete")
		}
	}
	result.Output = strings.Join(texts, "\n")
	if call.Kind == "web_search" {
		result.Output = Text(Object{"result": result.Output, "sources": result.Sources, "citation_instruction": "Cite the exact source URLs using Markdown links."})
	} else {
		result.Output = "Image generation succeeded. The generated images are attached to this response."
	}
	return result, nil
}
