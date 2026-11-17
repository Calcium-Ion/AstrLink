package clientconfig

import (
	"encoding/base64"
	"fmt"
	"slices"
	"strings"

	"github.com/QuantumNous/astrlink/desktop/internal/clientconfig/format"
)

// Clients AstrLink hands the connection to through their own import link.
// The client shows what it imports and saves it itself, so AstrLink writes
// nothing and keeps no record. The link carries the token: it is built
// here, opened by the OS, and never logged.

// linkClient is a client that imports a connection from a link.
type linkClient struct {
	id, name string
	// scheme is the links it opens, whose handler marks it installed.
	scheme string
	// protocols are the protocols a link can carry, preferred first.
	protocols []string
	build     func(protocol, origin, token string) (string, bool)
}

func (l linkClient) entry() CatalogEntry {
	return CatalogEntry{ID: l.id, Name: l.name, Kind: KindLink, Targets: []string{}, Protocols: l.protocols}
}

// links is every link client, in rail order.
var links = []linkClient{
	{
		id:        "cherry_studio",
		name:      "Cherry Studio",
		scheme:    "cherrystudio",
		protocols: []string{"openai.chat", "openai.responses", "anthropic.messages", "google.generate_content"},
		build:     cherryStudio,
	},
	{
		id:        "aionui",
		name:      "AionUi",
		scheme:    "aionui",
		protocols: []string{"openai.chat", "anthropic.messages"},
		build:     aionUI,
	},
}

// ImportLink is the import link that gives client this gateway and token.
func ImportLink(client, protocol, origin, token string) (string, error) {
	i := slices.IndexFunc(links, func(link linkClient) bool { return link.id == client })
	if i < 0 {
		return "", fmt.Errorf("%s takes no import link", client)
	}
	if slices.Contains(links[i].protocols, protocol) {
		if link, ok := links[i].build(protocol, origin, token); ok {
			return link, nil
		}
	}
	return "", fmt.Errorf("%s takes no %s link", client, protocol)
}

// encodeLinkData is base64 the handlers decode, with `+`, `/`, and `=`
// escaped so the query parser keeps them.
func encodeLinkData(json string) string {
	return strings.NewReplacer("+", "%2B", "/", "%2F", "=", "%3D").Replace(base64.StdEncoding.EncodeToString([]byte(json)))
}

// cherryStudio adds a new custom provider per protocol, since Cherry
// Studio matches an existing one by id and cannot switch its type on
// import. Cherry adds `/v1` (`/v1beta` for Gemini) to the bare origin
// itself.
func cherryStudio(protocol, origin, token string) (string, bool) {
	var id, name, kind string
	switch protocol {
	case "openai.chat":
		id, name, kind = "astrlink", "AstrLink", "openai"
	case "openai.responses":
		id, name, kind = "astrlink-responses", "AstrLink Responses", "openai-response"
	case "anthropic.messages":
		id, name, kind = "astrlink-anthropic", "AstrLink Anthropic", "anthropic"
	case "google.generate_content":
		id, name, kind = "astrlink-gemini", "AstrLink Gemini", "gemini"
	default:
		return "", false
	}
	json := format.Encode(format.Object{{Key: "id", Value: id}, {Key: "name", Value: name}, {Key: "type", Value: kind}, {Key: "baseUrl", Value: origin}, {Key: "apiKey", Value: token}})
	return "cherrystudio://providers/api-keys?v=1&data=" + encodeLinkData(json), true
}

// aionUI prefills AionUi's add-model form from snake_case keys. Its custom
// platform appends `/chat/completions` to the address, and its Anthropic
// one `/v1/messages`.
func aionUI(protocol, origin, token string) (string, bool) {
	var platform, baseURL string
	switch protocol {
	case "openai.chat":
		platform, baseURL = "custom", origin+"/v1"
	case "anthropic.messages":
		platform, baseURL = "Anthropic", origin
	default:
		return "", false
	}
	json := format.Encode(format.Object{{Key: "platform", Value: platform}, {Key: "base_url", Value: baseURL}, {Key: "api_key", Value: token}, {Key: "name", Value: "AstrLink"}})
	return "aionui://provider/add?v=1&data=" + encodeLinkData(json), true
}
