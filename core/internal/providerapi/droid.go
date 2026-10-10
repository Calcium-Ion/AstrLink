package providerapi

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// DroidEndpointHeader is the authorizer overlay naming the Factory API host an
// account's organization is served from (the EU region). It is consumed here
// and stripped before forwarding.
const DroidEndpointHeader = "X-AstrLink-Factory-Endpoint"

// DroidSystemPromptLine opens every system prompt the Droid CLI sends.
// Factory's gateway serves a plan's requests only when the system prompt
// opens with it, so a caller's own prompt is placed after it.
const DroidSystemPromptLine = "You are Droid, an AI software engineering agent built by Factory."

const (
	droidMaxBody            = 32 << 20
	droidOpenAIPlatformOrg  = "org-bHuLtG1fGmYk5YaOihAAXFBw"
	droidAnthropicVersion   = "2023-06-01"
	droidFastModeBeta       = "fast-mode-2026-02-01"
	droidAnthropicAPIKey    = "placeholder"
	droidSystemPromptPrefix = DroidSystemPromptLine + "\n"
)

// droidKeptHeaders are the only headers a Factory call keeps besides the ones
// DroidRequest derives. The Droid CLI sends no others, so a caller's own
// fingerprint (X-Stainless-*, originator, session headers) never travels
// beside the Droid User-Agent.
var droidKeptHeaders = []string{
	"Authorization", "User-Agent", "X-Factory-Client", "X-Client-Version", "X-Factory-Org-Id",
	"Content-Type", "Accept", "Accept-Encoding",
}

// droidDroppedBetas are Anthropic feature betas tied to Claude Code's own
// OAuth session. Factory authenticates with its own key behind the gateway.
var droidDroppedBetas = []string{"claude-code-", "oauth-"}

// DroidRequest prepares one Factory LLM gateway call the way the Droid CLI
// makes it: AstrLink's OpenAI and Anthropic paths map onto Factory's
// /api/llm roots, only the client headers above survive, the vendor,
// session and message headers are derived from the body, and the system
// prompt opens with Droid's line. endpoint, when set, is the regional host
// the account's organization is served from.
func DroidRequest(req *http.Request, endpoint string) error {
	var surface string
	switch req.URL.Path {
	case "/v1/chat/completions", "/chat/completions":
		surface = "chat"
	case "/v1/responses", "/responses":
		surface = "responses"
	case "/v1/messages":
		surface = "messages"
	}
	if surface == "" || req.Method != http.MethodPost {
		return fmt.Errorf("unsupported Factory Droid request path")
	}
	if endpoint != "" {
		host, err := url.Parse(endpoint)
		if err != nil || host.Scheme != "https" || host.Host == "" {
			return fmt.Errorf("invalid Factory API endpoint")
		}
		req.URL.Scheme, req.URL.Host, req.Host = host.Scheme, host.Host, ""
	}
	kept := http.Header{}
	for _, name := range droidKeptHeaders {
		if values := req.Header.Values(name); len(values) > 0 {
			kept[name] = values
		}
	}
	if req.Body == nil {
		return fmt.Errorf("missing Factory Droid request body")
	}
	body, err := io.ReadAll(io.LimitReader(req.Body, droidMaxBody+1))
	_ = req.Body.Close()
	if err != nil {
		return err
	}
	if len(body) > droidMaxBody {
		return fmt.Errorf("Factory Droid request exceeds size limit")
	}
	if !gjson.ValidBytes(body) || !gjson.ParseBytes(body).IsObject() {
		return fmt.Errorf("invalid Factory Droid request body")
	}
	model := gjson.GetBytes(body, "model").String()
	provider := DroidAPIProvider(model)
	turns := gjson.GetBytes(body, "messages")
	if surface == "responses" {
		turns = gjson.GetBytes(body, "input")
		if turns.Type == gjson.String {
			turns = gjson.Parse(`[{"role":"user","content":` + turns.Raw + `}]`)
		}
	}
	switch surface {
	case "messages":
		if values := req.Header.Values("Anthropic-Version"); len(values) > 0 {
			kept["Anthropic-Version"] = values
		} else {
			kept.Set("Anthropic-Version", droidAnthropicVersion)
		}
		betas := droidBetas(req.Header.Values("Anthropic-Beta"))
		if provider == "anthropic" && strings.HasSuffix(strings.ToLower(model), "-fast") && !containsString(betas, droidFastModeBeta) {
			betas = append(betas, droidFastModeBeta)
		}
		if len(betas) > 0 {
			kept.Set("Anthropic-Beta", strings.Join(betas, ","))
		}
		// Droid's Anthropic client is built with this key, sent beside the
		// bearer token.
		kept.Set("X-Api-Key", droidAnthropicAPIKey)
	case "responses":
		if provider == "openai" {
			kept.Set("Openai-Platform", droidOpenAIPlatformOrg)
		}
	}
	if body, err = openAsDroid(surface, body); err != nil {
		return err
	}
	kept.Set("X-Api-Provider", provider)
	kept.Set("X-Session-Id", droidSessionID(req.Header.Get("Authorization"), turns))
	kept.Set("X-Assistant-Message-Id", randomUUID())
	kept.Set("Content-Type", "application/json")
	req.URL.RawPath = ""
	req.URL.Path = map[string]string{
		"chat":      "/api/llm/o/v1/chat/completions",
		"responses": "/api/llm/o/v1/responses",
		"messages":  "/api/llm/a/v1/messages",
	}[surface]
	req.Header = kept
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
	req.ContentLength = int64(len(body))
	return nil
}

// DroidAPIProvider is the vendor the Droid CLI names in x-api-provider for a
// model: the model's own vendor for Claude, GPT, Grok, Gemini and Mistral,
// and the host serving each open model (Fireworks, or Baseten for GLM-5.2
// and Nemotron) as Droid's registry lists them.
func DroidAPIProvider(model string) string {
	model = strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.HasPrefix(model, "claude-"):
		return "anthropic"
	case strings.HasPrefix(model, "gpt-"):
		return "openai"
	case strings.HasPrefix(model, "grok-"):
		return "xai"
	case strings.HasPrefix(model, "gemini-"):
		return "google"
	case strings.HasPrefix(model, "mistral-"):
		return "mistral"
	case strings.HasPrefix(model, "glm-5.2"), strings.HasPrefix(model, "nemotron-"):
		return "baseten"
	default:
		return "fireworks"
	}
}

func droidBetas(values []string) []string {
	var betas []string
	for _, value := range values {
		for beta := range strings.SplitSeq(value, ",") {
			beta = strings.TrimSpace(beta)
			if beta == "" || containsString(betas, beta) {
				continue
			}
			dropped := false
			for _, prefix := range droidDroppedBetas {
				if strings.HasPrefix(beta, prefix) {
					dropped = true
					break
				}
			}
			if !dropped {
				betas = append(betas, beta)
			}
		}
	}
	return betas
}

func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

// openAsDroid opens the request's system prompt with Droid's line: Responses'
// instructions, chat completions' first system message, or Anthropic's
// system blocks. A caller's own prompt follows the line, joined with one
// newline as Droid joins its own blocks; without one the line stands alone.
// A prompt that already opens with the line is left byte for byte.
func openAsDroid(surface string, body []byte) ([]byte, error) {
	switch surface {
	case "responses":
		instructions := gjson.GetBytes(body, "instructions")
		switch instructions.Type {
		case gjson.Null:
			return sjson.SetBytes(body, "instructions", DroidSystemPromptLine)
		case gjson.String:
			if strings.HasPrefix(instructions.String(), DroidSystemPromptLine) {
				return body, nil
			}
			return sjson.SetBytes(body, "instructions", droidPrefixed(instructions.String()))
		default:
			return body, nil
		}
	case "chat":
		messages := gjson.GetBytes(body, "messages")
		index := -1
		messages.ForEach(func(position, message gjson.Result) bool {
			if message.Get("role").String() == "system" {
				index = int(position.Int())
				return false
			}
			return true
		})
		if index < 0 {
			var turns []json.RawMessage
			if messages.IsArray() {
				if err := json.Unmarshal([]byte(messages.Raw), &turns); err != nil {
					return nil, err
				}
			}
			system, err := json.Marshal(map[string]string{"role": "system", "content": DroidSystemPromptLine})
			if err != nil {
				return nil, err
			}
			turns = append([]json.RawMessage{system}, turns...)
			encoded, err := json.Marshal(turns)
			if err != nil {
				return nil, err
			}
			return sjson.SetRawBytes(body, "messages", encoded)
		}
		path := fmt.Sprintf("messages.%d.content", index)
		content := gjson.GetBytes(body, path)
		switch {
		case content.Type == gjson.String:
			if strings.HasPrefix(content.String(), DroidSystemPromptLine) {
				return body, nil
			}
			return sjson.SetBytes(body, path, droidPrefixed(content.String()))
		case content.IsArray():
			return prependDroidTextPart(body, path, content)
		case content.Type == gjson.Null || !content.Exists():
			return sjson.SetBytes(body, path, DroidSystemPromptLine)
		default:
			return body, nil
		}
	case "messages":
		system := gjson.GetBytes(body, "system")
		switch {
		case system.Type == gjson.Null || !system.Exists():
			return sjson.SetRawBytes(body, "system", droidTextBlocks())
		case system.Type == gjson.String:
			if strings.HasPrefix(system.String(), DroidSystemPromptLine) {
				return body, nil
			}
			return sjson.SetBytes(body, "system", droidPrefixed(system.String()))
		case system.IsArray():
			return prependDroidTextPart(body, "system", system)
		default:
			return body, nil
		}
	}
	return body, nil
}

func droidPrefixed(prompt string) string {
	if strings.TrimSpace(prompt) == "" {
		return DroidSystemPromptLine
	}
	return droidSystemPromptPrefix + prompt
}

func droidTextBlocks() []byte {
	encoded, _ := json.Marshal([]map[string]string{{"type": "text", "text": DroidSystemPromptLine}})
	return encoded
}

// prependDroidTextPart puts Droid's line before the text parts at path unless
// the first part already opens with it.
func prependDroidTextPart(body []byte, path string, parts gjson.Result) ([]byte, error) {
	var blocks []json.RawMessage
	if err := json.Unmarshal([]byte(parts.Raw), &blocks); err != nil {
		return nil, err
	}
	if len(blocks) > 0 {
		first := gjson.ParseBytes(blocks[0])
		if first.Get("type").String() == "text" && strings.HasPrefix(first.Get("text").String(), DroidSystemPromptLine) {
			return body, nil
		}
	}
	line, err := json.Marshal(map[string]string{"type": "text", "text": DroidSystemPromptLine})
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(append([]json.RawMessage{line}, blocks...))
	if err != nil {
		return nil, err
	}
	return sjson.SetRawBytes(body, path, encoded)
}

// droidSessionID keys a conversation the way Droid keys a session (a UUID):
// later requests of a conversation resend its first message unchanged, so
// hashing that message keeps one id per conversation, salted by the account
// credential so the same prompt on two accounts never shares an id.
func droidSessionID(salt string, turns gjson.Result) string {
	sum := conversationDigest(salt, turns)
	return formatUUID(sum[:16])
}

func randomUUID() string {
	var raw [16]byte
	_, _ = rand.Read(raw[:])
	return formatUUID(raw[:])
}

// formatUUID renders 16 bytes as a version 4 UUID.
func formatUUID(raw []byte) string {
	var id [16]byte
	copy(id[:], raw)
	id[6] = (id[6] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(id[:])
	return encoded[:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:]
}
