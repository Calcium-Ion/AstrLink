package pluginhost

import (
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/tidwall/gjson"
)

// Bounds of the text the host reads a workspace from.
const (
	maxWorkspaceScan  = 256 << 10
	maxWorkspacePath  = 4096
	maxWorkspaceName  = 256
	workspaceMessages = 2
)

// workspaceKeyInfo is the HKDF info string that separates the workspace
// fingerprint key from every other use of the audit master key. Bumping it
// orphans every workspace binding.
const workspaceKeyInfo = "astrlink/workspace-fingerprint/v1"

// WorkspaceKey derives the key of workspace fingerprints from the device's
// audit master key, nil when it cannot. The caller clears both after use.
func WorkspaceKey(auditKey []byte) []byte {
	key, err := hkdf.Key(sha256.New, auditKey, nil, workspaceKeyInfo, sha256.Size)
	if err != nil {
		return nil
	}
	return key
}

// workspace is the working directory a coding client names in its requests:
// a keyed fingerprint of its path, and the path's last element for people to
// recognize it. The path itself is never kept.
type workspace struct {
	fingerprint string
	name        string
}

func (place workspace) known() bool { return place.fingerprint != "" }

// workspacePatterns find the working directory in what coding clients send.
// They live here and nowhere else: a client that rewords its prompt costs a
// new session one question, never a wrong binding.
var workspacePatterns = []*regexp.Regexp{
	// Claude Code, OpenCode and others list it in their environment
	// section: "Working directory: /path" or "Primary working directory:
	// /path", on a line of its own.
	regexp.MustCompile(`(?m)^[ \t]*(?:-[ \t]+)?(?:Primary )?[Ww]orking directory:[ \t]*(\S[^\n]*?)[ \t]*$`),
	// Codex puts <cwd>/path</cwd> in its environment context.
	regexp.MustCompile(`<cwd>[ \t]*([^<\n]+?)[ \t]*</cwd>`),
	// Gemini CLI opens with "I'm currently working in the directory: /path".
	regexp.MustCompile(`(?m)working in the directory:[ \t]*(\S[^\n]*?)[ \t]*$`),
	// Cline, Roo Code and Kilo Code: "# Current Working Directory (/path)".
	regexp.MustCompile(`Current Working Directory \(([^)\n]+)\)`),
}

// readWorkspace finds the working directory in a request body: in its system
// prompt, then in its first user messages. key keys the fingerprint; without
// one the host keeps no workspace.
func readWorkspace(key []byte, protocol contract.ProtocolID, body []byte, messages []string) workspace {
	if len(key) == 0 {
		return workspace{}
	}
	texts := append([]string{systemText(protocol, body)}, messages...)
	for _, text := range texts {
		text = clampBytes(text, maxWorkspaceScan)
		for _, pattern := range workspacePatterns {
			if match := pattern.FindStringSubmatch(text); match != nil {
				if place, ok := newWorkspace(key, match[1]); ok {
					return place
				}
			}
		}
	}
	return workspace{}
}

// newWorkspace names the directory at path: quotes and trailing separators
// do not change it.
func newWorkspace(key []byte, path string) (workspace, bool) {
	path = strings.Trim(strings.TrimSpace(path), "`'\"")
	for len(path) > 1 && (strings.HasSuffix(path, "/") || strings.HasSuffix(path, `\`)) && !strings.HasSuffix(path, `:\`) {
		path = path[:len(path)-1]
	}
	if path == "" || len(path) > maxWorkspacePath || !utf8.ValidString(path) || strings.ContainsAny(path, "\x00\r\n") {
		return workspace{}, false
	}
	name := path[strings.LastIndexAny(path, `/\`)+1:]
	if name == "" {
		name = path
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(path))
	return workspace{fingerprint: hex.EncodeToString(mac.Sum(nil)), name: clampBytes(name, maxWorkspaceName)}, true
}

// systemText is the text of a request's system prompt, its parts joined by
// line breaks: what the client's own instructions say, before anything was
// injected.
func systemText(protocol contract.ProtocolID, body []byte) string {
	var texts []string
	add := func(value gjson.Result) {
		switch {
		case value.Type == gjson.String:
			texts = append(texts, value.Str)
		case value.IsArray():
			for _, part := range value.Array() {
				if part.Type == gjson.String {
					texts = append(texts, part.Str)
				} else if text := part.Get("text"); text.Type == gjson.String {
					texts = append(texts, text.Str)
				}
			}
		}
	}
	switch protocol {
	case contract.ProtocolAnthropicMessages:
		add(gjson.GetBytes(body, "system"))
	case contract.ProtocolOpenAIResponses:
		add(gjson.GetBytes(body, "instructions"))
		for _, item := range gjson.GetBytes(body, "input").Array() {
			if role := item.Get("role").Str; role == "system" || role == "developer" {
				add(item.Get("content"))
			}
		}
	case contract.ProtocolOpenAIChat:
		for _, message := range gjson.GetBytes(body, "messages").Array() {
			if role := message.Get("role").Str; role == "system" || role == "developer" {
				add(message.Get("content"))
			}
		}
	case contract.ProtocolGoogleGenerateContent:
		instruction := gjson.GetBytes(body, "systemInstruction")
		if !instruction.Exists() {
			instruction = gjson.GetBytes(body, "system_instruction")
		}
		add(instruction.Get("parts"))
	}
	return strings.Join(texts, "\n")
}
