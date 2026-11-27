package clients

import (
	"encoding/json"
	"slices"

	"github.com/QuantumNous/astrlink/core/contract"
	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
	"github.com/QuantumNous/astrlink/desktop/internal/clientconfig/format"
)

const CodexCatalogFilename = "astrlink-model-catalog.json"

// The generated companion is owned as one value. Retaining its original
// bytes lets the ordinary per-key backup restore even a hand-edited file.
type codexCatalogFile struct{}

func (codexCatalogFile) Format() format.Format                 { return format.JSONFormat{} }
func (codexCatalogFile) Holds(key string) bool                 { return key == CodexCatalogFilename }
func (codexCatalogFile) Open(existing *string) format.Document { return &catalogText{existing} }

type catalogText struct{ text *string }

func (d *catalogText) Get(key string) (string, bool, bool) {
	if key != CodexCatalogFilename || d.text == nil {
		return "", false, false
	}
	return *d.text, true, true
}
func (d *catalogText) Set(_ string, value string) error { d.text = &value; return nil }
func (d *catalogText) Remove(_ string)                  { d.text = nil }
func (d *catalogText) DropEmpty([]string)               {}
func (d *catalogText) IsEmpty() bool                    { return d.text == nil }
func (d *catalogText) Render() string {
	if d.text == nil {
		return ""
	}
	return *d.text
}

func codexCatalogText(connection *cc.Connection, details cc.Catalog) string {
	models := make([]json.RawMessage, 0, len(connection.List))
	for _, id := range connection.ModelList() {
		info := details[id]
		var window uint64
		if info.ContextWindow != nil {
			window = *info.ContextWindow
		}
		reasoning := contract.ModelReasoning(info.CodexCatalog, info.Reasoning)
		entry, err := contract.CodexCatalogEntry(id, info.CodexCatalog, &reasoning, window)
		if err != nil {
			entry, _ = contract.CodexCatalogEntry(id, nil, &reasoning, window)
		}
		models = append(models, entry)
	}
	contents, _ := json.MarshalIndent(struct {
		Models []json.RawMessage `json:"models"`
	}{models}, "", "  ")
	return string(contents) + "\n"
}

func catalogModelIDs(config *cc.Config) []string {
	raw, ok := config.String(CodexCatalogFilename)
	if !ok {
		return nil
	}
	var catalog struct {
		Models []struct {
			Slug string `json:"slug"`
		} `json:"models"`
	}
	if json.Unmarshal([]byte(raw), &catalog) != nil {
		return nil
	}
	var ids []string
	for _, entry := range catalog.Models {
		if len(ids) == cc.MaxListedModels {
			break
		}
		if id, ok := cc.CleanModel(entry.Slug); ok && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	return ids
}
