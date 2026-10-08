package ingress

import (
	"encoding/json"
	"strings"

	"github.com/QuantumNous/astrlink/convo"
)

// Standalone search accepts Responses-shaped input, but its commands and
// output are not a Responses generation. Reuse only the input inspection;
// never treat a search result as a response continuation or invent usage.
func inspectAlphaSearchMetadata(metadata *requestMetadata, fields map[string]json.RawMessage) {
	metadata.PreviousResponseID = ""
	if summary, err := conversationPolicy.InspectFields(convo.OpenAIResponses, fields); err == nil {
		metadata.Conversation = summary
		metadata.ConversationID = conversationCursor(summary, "")
		metadata.InputPreview = sanitizePreview(summary.LastUserText)
	}
	// Codex uses SearchRequest.id as its session id and may omit the
	// session headers. A header, when present, still takes precedence in classify.
	if cursor := extractProtocolCursor(fields, "id"); cursor != "" {
		metadata.Conversation.SessionCursor = cursor
		metadata.ConversationID = cursor
	}
	var commands struct {
		SearchQuery []struct {
			Query string `json:"q"`
		} `json:"search_query"`
		ImageQuery []struct {
			Query string `json:"q"`
		} `json:"image_query"`
	}
	if json.Unmarshal(fields["commands"], &commands) != nil {
		return
	}
	for _, queries := range [][]struct {
		Query string `json:"q"`
	}{commands.SearchQuery, commands.ImageQuery} {
		for _, query := range queries {
			if strings.TrimSpace(query.Query) != "" {
				metadata.InputPreview = sanitizePreview(query.Query)
				return
			}
		}
	}
}
