package contract

import "testing"

func TestParseGeminiCallTakesBothVersions(t *testing.T) {
	for _, test := range []struct {
		path string
		want GeminiCall
	}{
		{"/v1beta/models/gemini-2.5-pro:generateContent", GeminiCall{Version: "/v1beta", Model: "gemini-2.5-pro", Action: "generateContent", Protocol: ProtocolGoogleGenerateContent}},
		{"/v1/models/gemini-2.5-pro:generateContent", GeminiCall{Version: "/v1", Model: "gemini-2.5-pro", Action: "generateContent", Protocol: ProtocolGoogleGenerateContent}},
		{"/v1/models/gemini-2.5-flash:streamGenerateContent", GeminiCall{Version: "/v1", Model: "gemini-2.5-flash", Action: "streamGenerateContent", Protocol: ProtocolGoogleGenerateContent, Streaming: true}},
		{"/v1/models/gemini-embedding-001:embedContent", GeminiCall{Version: "/v1", Model: "gemini-embedding-001", Action: "embedContent", Protocol: ProtocolGoogleEmbeddings}},
		{"/v1beta/models/gemini-embedding-001:batchEmbedContents", GeminiCall{Version: "/v1beta", Model: "gemini-embedding-001", Action: "batchEmbedContents", Protocol: ProtocolGoogleEmbeddings}},
		{"/v1beta/models/astrlink/auto:generateContent", GeminiCall{Version: "/v1beta", Model: AstrLinkAutoModelID, Action: "generateContent", Protocol: ProtocolGoogleGenerateContent}},
		// A plugin route's template reads the same way.
		{"/v1/models/{model}:batchEmbedContents", GeminiCall{Version: "/v1", Model: "{model}", Action: "batchEmbedContents", Protocol: ProtocolGoogleEmbeddings}},
		// The action is after the last colon, so a model may hold one.
		{"/v1/models/qwen3:8b:generateContent", GeminiCall{Version: "/v1", Model: "qwen3:8b", Action: "generateContent", Protocol: ProtocolGoogleGenerateContent}},
	} {
		got, ok := ParseGeminiCall(test.path)
		if !ok || got != test.want {
			t.Errorf("ParseGeminiCall(%q) = %+v, %v; want %+v", test.path, got, ok, test.want)
		}
	}
	for _, path := range []string{
		"/v1/models",
		"/v1/models/gpt-4o",
		"/v1/models/:generateContent",
		"/v1beta/models/gemini:countTokens",
		"/v1beta/models/publisher/model:generateContent",
		"/v1beta1/models/gemini:generateContent",
		"/v2/models/gemini:generateContent",
	} {
		if got, ok := ParseGeminiCall(path); ok {
			t.Errorf("ParseGeminiCall(%q) = %+v, want no call", path, got)
		}
	}
}
