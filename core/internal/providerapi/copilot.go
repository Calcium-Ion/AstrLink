package providerapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const copilotMaxBody = 32 << 20

// copilotKeptHeaders are the only headers a GitHub Copilot call keeps besides
// the ones CopilotRequest derives. The OpenCode client sends no others, so a
// caller's own fingerprint (X-Stainless-*, originator, session headers) never
// travels beside the OpenCode User-Agent.
var copilotKeptHeaders = []string{
	"Authorization", "User-Agent", "X-Github-Api-Version", "Openai-Intent",
	"Content-Type", "Accept", "Accept-Encoding",
}

// Messages calls also keep the Anthropic version and feature betas.
var copilotMessagesHeaders = []string{"Anthropic-Version", "Anthropic-Beta"}

// CopilotRequest prepares one GitHub Copilot API call the way OpenCode makes
// it: AstrLink's OpenAI paths map onto Copilot's unversioned roots (Messages
// keeps /v1), only the client headers above survive, and the initiator,
// vision and interaction headers are derived from the body. Messages bodies
// lose the few fields Copilot rejects as extra inputs.
func CopilotRequest(req *http.Request) error {
	var surface string
	switch req.URL.Path {
	case "/v1/models", "/models":
		surface = "models"
	case "/v1/chat/completions", "/chat/completions":
		surface = "chat"
	case "/v1/responses", "/responses":
		surface = "responses"
	case "/v1/messages":
		surface = "messages"
	}
	if surface == "" || (surface == "models") != (req.Method == http.MethodGet) || (surface != "models" && req.Method != http.MethodPost) {
		return fmt.Errorf("unsupported GitHub Copilot request path")
	}
	kept := http.Header{}
	for _, name := range copilotKeptHeaders {
		if values := req.Header.Values(name); len(values) > 0 {
			kept[name] = values
		}
	}
	if surface == "messages" {
		for _, name := range copilotMessagesHeaders {
			if values := req.Header.Values(name); len(values) > 0 {
				kept[name] = values
			}
		}
		if kept.Get("Anthropic-Version") == "" {
			kept.Set("Anthropic-Version", "2023-06-01")
		}
	}
	req.URL.RawPath = ""
	if surface == "models" {
		req.URL.Path = "/models"
		req.Header = kept
		return nil
	}
	req.URL.Path = map[string]string{"chat": "/chat/completions", "responses": "/responses", "messages": "/v1/messages"}[surface]
	if req.Body == nil {
		return fmt.Errorf("missing GitHub Copilot request body")
	}
	body, err := io.ReadAll(io.LimitReader(req.Body, copilotMaxBody+1))
	_ = req.Body.Close()
	if err != nil {
		return err
	}
	if len(body) > copilotMaxBody {
		return fmt.Errorf("GitHub Copilot request exceeds size limit")
	}
	if !gjson.ValidBytes(body) || !gjson.ParseBytes(body).IsObject() {
		return fmt.Errorf("invalid GitHub Copilot request body")
	}
	turns := gjson.GetBytes(body, "messages")
	if surface == "responses" {
		turns = gjson.GetBytes(body, "input")
		if turns.Type == gjson.String {
			turns = gjson.Parse(`[{"role":"user","content":` + turns.Raw + `}]`)
		}
	}
	if surface == "messages" {
		if body, err = stripCopilotMessagesFields(body); err != nil {
			return err
		}
	}
	initiator := "user"
	if !copilotUserPrompt(lastArrayItem(turns)) {
		initiator = "agent"
	}
	kept.Set("X-Initiator", initiator)
	kept.Set("X-Interaction-Id", copilotInteractionID(req.Header.Get("Authorization"), turns))
	if copilotHasImage(turns) {
		kept.Set("Copilot-Vision-Request", "true")
	}
	kept.Set("Content-Type", "application/json")
	req.Header = kept
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
	req.ContentLength = int64(len(body))
	return nil
}

func lastArrayItem(items gjson.Result) gjson.Result {
	array := items.Array()
	if len(array) == 0 {
		return gjson.Result{}
	}
	return array[len(array)-1]
}

// copilotUserPrompt reports a prompt the user typed rather than the agent
// loop feeding a tool result back: Copilot's x-initiator is user only for
// the request that opens a user turn. OpenAI tool results arrive as a tool
// role or a Responses function output item without a role; Anthropic ones as
// tool_result blocks, which make the message a loop iteration even beside
// text such as system reminders. An empty conversation counts as a prompt.
func copilotUserPrompt(message gjson.Result) bool {
	if !message.Exists() {
		return true
	}
	if message.Get("role").String() != "user" {
		return false
	}
	toolResult := false
	message.Get("content").ForEach(func(_, part gjson.Result) bool {
		toolResult = part.Get("type").String() == "tool_result"
		return !toolResult
	})
	return !toolResult
}

// copilotHasImage reports any image part: OpenAI image_url and input_image,
// Anthropic image blocks, and images nested in an Anthropic tool_result.
func copilotHasImage(turns gjson.Result) bool {
	found := false
	var visit func(parts gjson.Result)
	visit = func(parts gjson.Result) {
		parts.ForEach(func(_, part gjson.Result) bool {
			switch part.Get("type").String() {
			case "image", "image_url", "input_image":
				found = true
			case "tool_result":
				visit(part.Get("content"))
			}
			return !found
		})
	}
	turns.ForEach(func(_, message gjson.Result) bool {
		visit(message.Get("content"))
		return !found
	})
	return found
}

const copilotBase62 = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// copilotInteractionID keys a conversation the way OpenCode keys its session
// (ses_ + 12 hex + 14 base62). Later requests of a conversation resend its
// first message unchanged, so hashing that message keeps one id per
// conversation; the account credential salts the hash so the same prompt on
// two accounts never shares an id. cache_control markers move between
// requests and are left out.
func copilotInteractionID(salt string, turns gjson.Result) string {
	sum := conversationDigest(salt, turns)
	id := make([]byte, 0, 30)
	id = append(id, "ses_"...)
	id = append(id, hex.EncodeToString(sum[:6])...)
	for _, b := range sum[6:20] {
		id = append(id, copilotBase62[int(b)%len(copilotBase62)])
	}
	return string(id)
}

// conversationDigest hashes a conversation's first non-system message, with
// cache_control markers removed since they move between requests, salted by
// the account credential.
func conversationDigest(salt string, turns gjson.Result) []byte {
	first := ""
	turns.ForEach(func(_, message gjson.Result) bool {
		if role := message.Get("role").String(); role == "system" || role == "developer" {
			return true
		}
		first = message.Raw
		return false
	})
	if stripped, err := deleteCacheControl([]byte(first)); err == nil {
		first = string(stripped)
	}
	hash := sha256.New()
	hash.Write([]byte(salt))
	hash.Write([]byte{0})
	hash.Write([]byte(first))
	return hash.Sum(nil)
}

func deleteCacheControl(message []byte) ([]byte, error) {
	if !gjson.ValidBytes(message) {
		return message, nil
	}
	var err error
	message, err = sjson.DeleteBytes(message, "cache_control")
	if err != nil {
		return nil, err
	}
	var paths []string
	gjson.GetBytes(message, "content").ForEach(func(index, part gjson.Result) bool {
		if part.Get("cache_control").Exists() {
			paths = append(paths, "content."+index.String()+".cache_control")
		}
		return true
	})
	for index := len(paths) - 1; index >= 0; index-- {
		if message, err = sjson.DeleteBytes(message, paths[index]); err != nil {
			return nil, err
		}
	}
	return message, nil
}

// stripCopilotMessagesFields drops what Copilot's /v1/messages answers with
// "Extra inputs are not permitted" and Claude Code sends by default: the
// top-level safeguards, per-message output_config, the scope of cache_control
// markers and eager_input_streaming on tools. Prompts, schemas and media stay
// byte-for-byte; only the known block positions are visited, never a tool's
// own input schema.
func stripCopilotMessagesFields(body []byte) ([]byte, error) {
	paths := []string{"safeguards"}
	scope := func(prefix string, block gjson.Result) {
		if block.Get("cache_control.scope").Exists() {
			paths = append(paths, prefix+".cache_control.scope")
		}
	}
	gjson.GetBytes(body, "system").ForEach(func(index, block gjson.Result) bool {
		scope("system."+index.String(), block)
		return true
	})
	gjson.GetBytes(body, "tools").ForEach(func(index, tool gjson.Result) bool {
		prefix := "tools." + index.String()
		scope(prefix, tool)
		if tool.Get("eager_input_streaming").Exists() {
			paths = append(paths, prefix+".eager_input_streaming")
		}
		return true
	})
	gjson.GetBytes(body, "messages").ForEach(func(index, message gjson.Result) bool {
		prefix := "messages." + index.String()
		if message.Get("output_config").Exists() {
			paths = append(paths, prefix+".output_config")
		}
		message.Get("content").ForEach(func(part, block gjson.Result) bool {
			blockPrefix := prefix + ".content." + part.String()
			scope(blockPrefix, block)
			block.Get("content").ForEach(func(nested, inner gjson.Result) bool {
				scope(blockPrefix+".content."+nested.String(), inner)
				return true
			})
			return true
		})
		return true
	})
	var err error
	// Later paths first, so deleting one never shifts the index of another.
	for index := len(paths) - 1; index >= 0; index-- {
		if !gjson.GetBytes(body, paths[index]).Exists() {
			continue
		}
		if body, err = sjson.DeleteBytes(body, paths[index]); err != nil {
			return nil, err
		}
	}
	return body, nil
}

// CopilotModelEntry is the part of a Copilot /models entry AstrLink reads.
type CopilotModelEntry struct {
	ID                 string   `json:"id"`
	ModelPickerEnabled bool     `json:"model_picker_enabled"`
	SupportedEndpoints []string `json:"supported_endpoints"`
	Policy             *struct {
		State string `json:"state"`
	} `json:"policy"`
}

// Offered reports a model Copilot offers users: shown in its model picker,
// not disabled by policy, and served on a chat endpoint. Embedding and
// retired internal models are not.
func (entry CopilotModelEntry) Offered() bool {
	if entry.ID == "" || !entry.ModelPickerEnabled || (entry.Policy != nil && entry.Policy.State == "disabled") {
		return false
	}
	for _, endpoint := range entry.SupportedEndpoints {
		switch endpoint {
		case "/chat/completions", "/responses", "/v1/messages":
			return true
		}
	}
	return entry.SupportedEndpoints == nil
}
