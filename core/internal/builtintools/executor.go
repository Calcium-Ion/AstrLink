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
	Images func(ctx context.Context, config contract.BuiltinTool, path, contentType string, body io.Reader) (Object, error)
	// Search posts a standalone web search, the body Codex sends to
	// /alpha/search, to a configured provider's own search API with that
	// provider's credential, proxy and header policy.
	Search  func(ctx context.Context, config contract.BuiltinTool, body Object) (Object, error)
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
	if config.Backend == "service_search" {
		return executor.serviceSearch(ctx, config, call)
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

// deepSeekSearchBase reports whether the external search backend is DeepSeek's tools API.
func deepSeekSearchBase(base string) bool {
	u, err := url.Parse(strings.TrimRight(base, "/"))
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "api.deepseek.com" || strings.HasSuffix(host, ".api.deepseek.com")
}

// deepSeekDomainQuery folds domain filters into query using site: syntax,
// which is the only form DeepSeek honours.
func deepSeekDomainQuery(query string, filters Object) string {
	add := func(raw any) {
		if domain := strings.TrimSpace(String(raw)); domain != "" {
			query += " " + domain
		}
	}
	if allowed := Array(filters["allowed_domains"]); len(allowed) > 0 {
		sites := make([]string, 0, len(allowed))
		for _, raw := range allowed {
			if domain := strings.TrimSpace(String(raw)); domain != "" {
				sites = append(sites, "site:"+domain)
			}
		}
		if len(sites) > 0 {
			add(strings.Join(sites, " OR "))
		}
	}
	for _, raw := range Array(filters["blocked_domains"]) {
		if domain := strings.TrimSpace(String(raw)); domain != "" {
			add("-site:" + domain)
		}
	}
	return query
}

// deepSeekSearchURL completes an API or version root with /v1/tools.
func deepSeekSearchURL(base string) string {
	u, err := url.Parse(strings.TrimRight(base, "/"))
	if err != nil {
		return base
	}
	switch strings.TrimRight(u.Path, "/") {
	case "", "/v1":
		u.Path = "/v1/tools"
	default:
		return base
	}
	return u.String()
}

func (executor Executor) search(ctx context.Context, config contract.BuiltinTool, call Invocation) (Result, error) {
	// Codex declares search_content_types for image-capable models and
	// indexed_web_access in its indexed mode. Tavily searches the live web
	// for every request, including Codex's default cached-only mode.
	if err := checkOptions(call.Options, "type search_context_size filters external_web_access indexed_web_access search_content_types user_location"); err != nil {
		return Result{}, err
	}
	if call.Options["user_location"] != nil {
		return Result{}, fmt.Errorf("Tavily backend does not support precise user_location")
	}
	deepSeek := deepSeekSearchBase(config.BaseURL)
	if deepSeek {
		config.BaseURL = deepSeekSearchURL(config.BaseURL)
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
		if deepSeek {
			// DeepSeek takes domain filters in the query, not as fields.
			query = deepSeekDomainQuery(query, filters)
			payload = Object{"query": query, "count": 5, "need_content": true}
			if call.Options["search_context_size"] == "high" {
				payload["count"] = 10
			}
		} else {
			if value := filters["allowed_domains"]; value != nil {
				payload["include_domains"] = value
			}
			if value := filters["blocked_domains"]; value != nil {
				payload["exclude_domains"] = value
			}
		}
	case "open", "find":
		raw := String(call.Arguments["url"])
		if !publicURL(raw) {
			return Result{}, fmt.Errorf("a public HTTP(S) page URL is required")
		}
		path = "/extract"
		payload = Object{"urls": []string{raw}, "format": "text"}
		if deepSeek {
			path = "/fetch"
			payload = Object{"url": raw}
		}
	default:
		return Result{}, fmt.Errorf("unsupported search action")
	}
	response, err := executor.jsonRequest(ctx, "web_search", config, path, payload)
	if err != nil {
		return Result{}, err
	}
	if deepSeek && action != "search" {
		// DeepSeek returns one page; present it as a result list.
		content := String(response["content"])
		if content == "" {
			return Result{}, fmt.Errorf("page extraction returned no usable content")
		}
		response = Object{"results": []any{Object{"url": call.Arguments["url"], "title": String(response["title"]), "content": content}}}
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

// serviceSearch runs one search, open or find through a provider's own search
// API. Pages are named by URL, which that API takes wherever Codex passes a
// reference ID, so each call stands alone and no search session is kept.
func (executor Executor) serviceSearch(ctx context.Context, config contract.BuiltinTool, call Invocation) (Result, error) {
	if err := checkOptions(call.Options, "type search_context_size filters external_web_access indexed_web_access search_content_types user_location"); err != nil {
		return Result{}, err
	}
	if executor.Search == nil {
		return Result{}, fmt.Errorf("provider search API is unavailable")
	}
	action := String(call.Arguments["action"])
	if action == "" {
		action = "search"
	}
	address := String(call.Arguments["url"])
	var commands, wireAction Object
	switch action {
	case "search":
		query := String(call.Arguments["query"])
		if strings.TrimSpace(query) == "" {
			return Result{}, fmt.Errorf("search query is required")
		}
		commands = Object{"search_query": []any{Object{"q": query}}}
		wireAction = Object{"type": "search", "query": query}
	case "open", "find":
		if !publicURL(address) {
			return Result{}, fmt.Errorf("a public HTTP(S) page URL is required")
		}
		commands = Object{"open": []any{Object{"ref_id": address}}}
		wireAction = Object{"type": "open_page", "url": address}
		if action == "find" {
			pattern := String(call.Arguments["pattern"])
			if pattern == "" {
				return Result{}, fmt.Errorf("find pattern is required")
			}
			commands = Object{"find": []any{Object{"ref_id": address, "pattern": pattern}}}
			wireAction = Object{"type": "find_in_page", "url": address, "pattern": pattern}
		}
	default:
		return Result{}, fmt.Errorf("unsupported search action")
	}
	if err := checkOptions(Map(call.Options["filters"]), "allowed_domains blocked_domains"); err != nil {
		return Result{}, err
	}
	// The hosted tool's settings have the same names and values in a search
	// request.
	settings := Object{}
	for _, key := range []string{"search_context_size", "filters", "user_location", "external_web_access"} {
		if value := call.Options[key]; value != nil {
			settings[key] = value
		}
	}
	body := Object{"id": searchSessionID(), "model": config.Model, "commands": commands}
	if len(settings) != 0 {
		body["settings"] = settings
	}
	response, err := executor.Search(ctx, config, body)
	if err != nil {
		return Result{}, err
	}
	output, ok := response["output"].(string)
	if !ok {
		return Result{}, fmt.Errorf("search provider returned an invalid answer")
	}
	// The output names results by reference ID; the model opens and cites
	// them by URL.
	sources := []Object{}
	references := []Object{}
	for _, raw := range Array(response["results"]) {
		item := Map(raw)
		address := String(item["url"])
		if !publicURL(address) {
			continue
		}
		sources = append(sources, Object{"type": "url", "url": address, "title": String(item["title"])})
		references = append(references, Object{"ref_id": String(item["ref_id"]), "url": address, "title": String(item["title"])})
	}
	if action == "search" {
		wireAction["sources"] = sources
	}
	item := Object{"id": ID("ws_"), "type": "web_search_call", "status": "completed", "action": wireAction}
	return Result{Items: []Object{item}, Output: Text(Object{"output": output, "sources": references, "citation_instruction": "Cite sources using their exact URL in Markdown links. To open or search a page, pass its URL."}), Sources: sources}, nil
}

// searchSessionID returns a random version 4 UUID, the form of the session ID
// Codex sends with its own searches.
func searchSessionID() string {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		panic(err)
	}
	data[6] = data[6]&0x0f | 0x40
	data[8] = data[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", data[0:4], data[4:6], data[6:8], data[8:10], data[10:16])
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
		if encoded == "" && entry["url"] != nil {
			encoded, _, err = relaykitbridge.ResolveImageData(ctx, String(entry["url"]))
		}
		if err != nil {
			return Result{}, fmt.Errorf("could not load generated image")
		}
		data, decodeErr := base64.StdEncoding.DecodeString(encoded)
		mime := http.DetectContentType(data)
		if decodeErr != nil || len(data) == 0 || len(data) > MaxImageBytes || !strings.HasPrefix(mime, "image/") {
			return Result{}, fmt.Errorf("invalid or oversized generated image")
		}
		// The bytes decide the format: MiniMax, for one, answers with JPEG
		// whatever output_format asked for.
		format := strings.TrimPrefix(mime, "image/")
		id := ID("ig_")
		item := Object{"id": id, "type": "image_generation_call", "status": "completed", "result": encoded, "output_format": format, "revised_prompt": entry["revised_prompt"]}
		result.Items = append(result.Items, item)
		result.Images = append(result.Images, "data:"+mime+";base64,"+encoded)
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
