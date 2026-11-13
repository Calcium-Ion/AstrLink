package clientconfig

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

const linkToken = "astr_VVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVA"
const linkOrigin = "http://127.0.0.1:8317"

func TestImportLinksMatchTheirClientsHandlers(t *testing.T) {
	for _, tc := range []struct{ client, protocol, want string }{
		{"cherry_studio", "openai.chat", "cherrystudio://providers/api-keys?v=1&data=eyJpZCI6ImFzdHJsaW5rIiwibmFtZSI6IkFzdHJMaW5rIiwidHlwZSI6Im9wZW5haSIsImJhc2VVcmwiOiJodHRwOi8vMTI3LjAuMC4xOjgzMTciLCJhcGlLZXkiOiJhc3RyX1ZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVkEifQ%3D%3D"},
		{"cherry_studio", "openai.responses", "cherrystudio://providers/api-keys?v=1&data=eyJpZCI6ImFzdHJsaW5rLXJlc3BvbnNlcyIsIm5hbWUiOiJBc3RyTGluayBSZXNwb25zZXMiLCJ0eXBlIjoib3BlbmFpLXJlc3BvbnNlIiwiYmFzZVVybCI6Imh0dHA6Ly8xMjcuMC4wLjE6ODMxNyIsImFwaUtleSI6ImFzdHJfVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWQSJ9"},
		{"cherry_studio", "anthropic.messages", "cherrystudio://providers/api-keys?v=1&data=eyJpZCI6ImFzdHJsaW5rLWFudGhyb3BpYyIsIm5hbWUiOiJBc3RyTGluayBBbnRocm9waWMiLCJ0eXBlIjoiYW50aHJvcGljIiwiYmFzZVVybCI6Imh0dHA6Ly8xMjcuMC4wLjE6ODMxNyIsImFwaUtleSI6ImFzdHJfVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWQSJ9"},
		{"cherry_studio", "google.generate_content", "cherrystudio://providers/api-keys?v=1&data=eyJpZCI6ImFzdHJsaW5rLWdlbWluaSIsIm5hbWUiOiJBc3RyTGluayBHZW1pbmkiLCJ0eXBlIjoiZ2VtaW5pIiwiYmFzZVVybCI6Imh0dHA6Ly8xMjcuMC4wLjE6ODMxNyIsImFwaUtleSI6ImFzdHJfVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWQSJ9"},
		{"aionui", "openai.chat", "aionui://provider/add?v=1&data=eyJwbGF0Zm9ybSI6ImN1c3RvbSIsImJhc2VfdXJsIjoiaHR0cDovLzEyNy4wLjAuMTo4MzE3L3YxIiwiYXBpX2tleSI6ImFzdHJfVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWQSIsIm5hbWUiOiJBc3RyTGluayJ9"},
		{"aionui", "anthropic.messages", "aionui://provider/add?v=1&data=eyJwbGF0Zm9ybSI6IkFudGhyb3BpYyIsImJhc2VfdXJsIjoiaHR0cDovLzEyNy4wLjAuMTo4MzE3IiwiYXBpX2tleSI6ImFzdHJfVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWVlZWQSIsIm5hbWUiOiJBc3RyTGluayJ9"},
	} {
		got, err := ImportLink(tc.client, tc.protocol, linkOrigin, linkToken)
		if err != nil || got != tc.want {
			t.Errorf("%s %s: %s %v", tc.client, tc.protocol, got, err)
		}
	}
	for _, tc := range [][2]string{{"aionui", "google.generate_content"}, {"claude", "anthropic.messages"}} {
		if _, err := ImportLink(tc[0], tc[1], linkOrigin, linkToken); err == nil {
			t.Errorf("%v: a link", tc)
		}
	}
}

func TestPlusSlashAndPaddingAreEscaped(t *testing.T) {
	for in, want := range map[string]string{"?>>": "Pz4%2B", "??": "Pz8%3D", "???": "Pz8%2F"} {
		if got := encodeLinkData(in); got != want {
			t.Errorf("%q: %s", in, got)
		}
	}
	link, _ := ImportLink("cherry_studio", "openai.chat", linkOrigin, "astr_?>>?")
	_, data, _ := strings.Cut(link, "data=")
	if strings.ContainsAny(data, "+/=") {
		t.Fatal(data)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.NewReplacer("%2B", "+", "%2F", "/", "%3D", "=").Replace(data))
	if err != nil {
		t.Fatal(err)
	}
	var provider map[string]any
	if json.Unmarshal(decoded, &provider) != nil || provider["apiKey"] != "astr_?>>?" {
		t.Fatalf("%s", decoded)
	}
}
