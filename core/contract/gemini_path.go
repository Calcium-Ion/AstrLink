package contract

import (
	"net/url"
	"strings"
)

// geminiVersions are the Gemini API versions a model call may name. Google
// serves generateContent, streamGenerateContent, embedContent and
// batchEmbedContents under both, and new-api routes both as Gemini.
var geminiVersions = [...]string{"/v1beta", "/v1"}

// GeminiCall is a Gemini model call: {version}/models/{model}:{action}.
type GeminiCall struct {
	// Version is "/v1beta" or "/v1", as the path names it.
	Version string
	// Model is the path's model, unescaped.
	Model     string
	Action    string
	Protocol  ProtocolID
	Streaming bool
}

// GeminiVersionOf is the Gemini API version a models path starts with.
func GeminiVersionOf(path string) (string, bool) {
	for _, version := range geminiVersions {
		if strings.HasPrefix(path, version+"/models/") {
			return version, true
		}
	}
	return "", false
}

// ParseGeminiCall reads a Gemini model call path. The model is one path
// segment, except for the gateway's auto model, and the action one of the
// four the gateway serves. Gateway ingress and the routes of new-api
// plugins read Gemini paths with it alike.
func ParseGeminiCall(path string) (GeminiCall, bool) {
	version, ok := GeminiVersionOf(path)
	if !ok {
		return GeminiCall{}, false
	}
	rest := strings.TrimPrefix(path, version+"/models/")
	if decoded, err := url.PathUnescape(rest); err == nil {
		rest = decoded
	}
	colon := strings.LastIndex(rest, ":")
	if colon <= 0 {
		return GeminiCall{}, false
	}
	call := GeminiCall{Version: version, Model: rest[:colon], Action: rest[colon+1:]}
	if strings.Contains(call.Model, "/") && call.Model != AstrLinkAutoModelID {
		return GeminiCall{}, false
	}
	switch call.Action {
	case "generateContent":
		call.Protocol = ProtocolGoogleGenerateContent
	case "streamGenerateContent":
		call.Protocol, call.Streaming = ProtocolGoogleGenerateContent, true
	case "embedContent", "batchEmbedContents":
		call.Protocol = ProtocolGoogleEmbeddings
	default:
		return GeminiCall{}, false
	}
	return call, true
}
