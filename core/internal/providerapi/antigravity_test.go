package providerapi_test

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/QuantumNous/astrlink/core/internal/providerapi"
)

func TestAntigravityCatalogOmitsNonChatModels(t *testing.T) {
	var catalog providerapi.AntigravityCatalog
	if err := json.Unmarshal([]byte(`{"models":{
		"":{},"   ":{},"chat_20706":{},"chat_future":{},
		"tab_flash_lite_preview":{},"tab_future":{},
		"gemini-2.5-flash-thinking":{},"gemini-2.5-pro":{},
		"gemini-2.5-flash":{},"gemini-3-flash":{},
		"claude-sonnet-4-6":{},"gpt-oss-120b-medium":{},"future-chat-model":{}
	}}`), &catalog); err != nil {
		t.Fatal(err)
	}
	want := []string{"claude-sonnet-4-6", "future-chat-model", "gemini-2.5-flash", "gemini-2.5-flash-thinking", "gemini-2.5-pro", "gemini-3-flash", "gpt-oss-120b-medium"}
	if got := catalog.IDs(); !slices.Equal(got, want) {
		t.Fatalf("IDs() = %v, want %v", got, want)
	}
}
