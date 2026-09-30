package providerapi

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// This authorizer overlay is consumed locally and stripped before forwarding.
const AntigravityProjectHeader = "X-AstrLink-Code-Assist-Project"
const antigravityMaxBody = 32 << 20

type AntigravityCatalog struct {
	Models map[string]struct {
		DisplayName string `json:"displayName"`
		QuotaInfo   *struct {
			RemainingFraction float64 `json:"remainingFraction"`
			ResetTime         string  `json:"resetTime"`
		} `json:"quotaInfo"`
	} `json:"models"`
}

func (catalog AntigravityCatalog) IDs() []string {
	ids := make([]string, 0, len(catalog.Models))
	for id := range catalog.Models {
		if strings.TrimSpace(id) == "" || strings.HasPrefix(id, "chat_") || strings.HasPrefix(id, "tab_") {
			continue
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// AntigravityRequest wraps a Gemini request in the Code Assist envelope.
// Cross-protocol conversion remains in RelayKit, outside this adapter.
func AntigravityRequest(req *http.Request, project string) (string, error) {
	if strings.TrimSpace(project) == "" {
		return "", fmt.Errorf("Antigravity project is missing")
	}
	mode := "json"
	var body []byte
	var err error
	if req.Method == http.MethodGet && (req.URL.Path == "/v1/models" || req.URL.Path == "/v1beta/models") {
		mode = "models"
		if req.URL.Path == "/v1beta/models" {
			mode = "google_models"
		}
		body, err = json.Marshal(map[string]string{"project": project})
		req.URL.Path = "/v1internal:fetchAvailableModels"
		req.URL.RawQuery = ""
	} else {
		_, tail, found := strings.Cut(req.URL.Path, "/models/")
		model, action, ok := strings.Cut(tail, ":")
		if !found || !ok || model == "" || req.Method != http.MethodPost || (action != "generateContent" && action != "streamGenerateContent") {
			return "", fmt.Errorf("unsupported Antigravity request path")
		}
		if req.Body == nil {
			return "", fmt.Errorf("missing Gemini request body")
		}
		body, err = io.ReadAll(io.LimitReader(req.Body, antigravityMaxBody+1))
		_ = req.Body.Close()
		if err != nil {
			return "", err
		}
		if len(body) > antigravityMaxBody {
			return "", fmt.Errorf("Antigravity request exceeds size limit")
		}
		if !gjson.ValidBytes(body) || !gjson.ParseBytes(body).IsObject() || !gjson.GetBytes(body, "contents").IsArray() {
			return "", fmt.Errorf("invalid Gemini request body")
		}
		// The endpoint's model is authoritative. Keep prompts, schemas and media
		// byte-preserving; remove only fields rejected by this upstream.
		body, err = sjson.DeleteBytes(body, "model")
		if err != nil {
			return "", err
		}
		if !strings.Contains(model, "claude") {
			body, err = sjson.DeleteBytes(body, "generationConfig.maxOutputTokens")
			if err != nil {
				return "", err
			}
		} else if gjson.GetBytes(body, "tools").Exists() {
			body, err = sjson.SetBytes(body, "toolConfig.functionCallingConfig.mode", "VALIDATED")
			if err != nil {
				return "", err
			}
		}
		random := make([]byte, 16)
		if _, err = rand.Read(random); err != nil {
			return "", err
		}
		// A conversation keeps its session id as later turns are appended.
		if gjson.GetBytes(body, "sessionId").String() == "" {
			seed := random
			gjson.GetBytes(body, "contents").ForEach(func(_, content gjson.Result) bool {
				if content.Get("role").String() == "user" && content.Get("parts.0.text").String() != "" {
					seed = []byte(content.Get("parts.0.text").String())
					return false
				}
				return true
			})
			hash := sha256.Sum256(seed)
			body, err = sjson.SetBytes(body, "sessionId", "-"+strconv.FormatUint(binary.BigEndian.Uint64(hash[:8])&0x7fffffffffffffff, 10))
			if err != nil {
				return "", err
			}
		}
		body, err = json.Marshal(map[string]any{"project": project, "model": model, "request": json.RawMessage(body), "userAgent": "antigravity", "requestType": "agent", "requestId": "agent-" + hex.EncodeToString(random)})
		req.URL.Path = "/v1internal:" + action
		req.URL.RawQuery = ""
		if action == "streamGenerateContent" {
			mode = "stream"
			req.URL.RawQuery = "alt=sse"
		}
	}
	if err != nil {
		return "", err
	}
	req.Method = http.MethodPost
	req.URL.RawPath = ""
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
	req.ContentLength = int64(len(body))
	req.Header.Del("Content-Length")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("Accept", "application/json")
	if mode == "stream" {
		req.Header.Set("Accept", "text/event-stream")
	}
	return mode, nil
}

func AntigravityResponse(res *http.Response, mode string) error {
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil
	}
	if encoding := res.Header.Get("Content-Encoding"); encoding != "" && encoding != "identity" {
		return fmt.Errorf("unexpected Antigravity response encoding")
	}
	res.Header.Del("Content-Length")
	res.ContentLength = -1
	if mode == "stream" {
		if !strings.Contains(res.Header.Get("Content-Type"), "text/event-stream") {
			return fmt.Errorf("Antigravity returned a non-SSE stream")
		}
		reader := &antigravitySSE{source: res.Body, scanner: bufio.NewScanner(res.Body)}
		reader.scanner.Buffer(make([]byte, 4096), 8<<20)
		res.Body = reader
		return nil
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, antigravityMaxBody+1))
	if err != nil {
		return err
	}
	if len(raw) > antigravityMaxBody {
		return fmt.Errorf("Antigravity response exceeds size limit")
	}
	if mode == "json" {
		raw, err = unwrapAntigravity(raw)
	} else {
		var catalog AntigravityCatalog
		if err = json.Unmarshal(raw, &catalog); err != nil {
			return err
		}
		if catalog.Models == nil {
			return fmt.Errorf("Antigravity catalog omitted models")
		}
		entries := make([]map[string]any, 0, len(catalog.Models))
		for _, id := range catalog.IDs() {
			if mode == "google_models" {
				entries = append(entries, map[string]any{"name": "models/" + id, "displayName": catalog.Models[id].DisplayName, "supportedGenerationMethods": []string{"generateContent", "streamGenerateContent"}})
			} else {
				entries = append(entries, map[string]any{"id": id, "object": "model", "owned_by": "google"})
			}
		}
		if mode == "google_models" {
			raw, err = json.Marshal(map[string]any{"models": entries})
		} else {
			raw, err = json.Marshal(map[string]any{"object": "list", "data": entries})
		}
	}
	if err != nil {
		return err
	}
	res.Header.Set("Content-Type", "application/json")
	res.Body = io.NopCloser(bytes.NewReader(raw))
	return nil
}

func unwrapAntigravity(raw []byte) ([]byte, error) {
	if !gjson.ValidBytes(raw) {
		return nil, fmt.Errorf("invalid Antigravity response")
	}
	response := gjson.GetBytes(raw, "response")
	if !response.IsObject() || !(response.Get("candidates").Exists() || response.Get("promptFeedback").Exists() || response.Get("usageMetadata").Exists()) {
		return nil, fmt.Errorf("Antigravity response omitted Gemini payload")
	}
	return []byte(response.Raw), nil
}

type antigravitySSE struct {
	source  io.ReadCloser
	scanner *bufio.Scanner
	pending []byte
	seen    bool
}

func (r *antigravitySSE) Close() error { return r.source.Close() }
func (r *antigravitySSE) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for len(r.pending) == 0 {
		var data []string
		for r.scanner.Scan() {
			line := r.scanner.Text()
			if line == "" && len(data) > 0 {
				break
			}
			if value, ok := strings.CutPrefix(line, "data:"); ok {
				data = append(data, strings.TrimPrefix(value, " "))
			}
		}
		if err := r.scanner.Err(); err != nil {
			return 0, err
		}
		if len(data) == 0 {
			if !r.seen {
				return 0, io.ErrUnexpectedEOF
			}
			return 0, io.EOF
		}
		raw := []byte(strings.Join(data, "\n"))
		if bytes.Equal(raw, []byte("[DONE]")) {
			continue
		}
		raw, err := unwrapAntigravity(raw)
		if err != nil {
			return 0, err
		}
		r.seen = true
		r.pending = append(append([]byte("data: "), raw...), '\n', '\n')
	}
	n := copy(p, r.pending)
	r.pending = r.pending[n:]
	return n, nil
}
