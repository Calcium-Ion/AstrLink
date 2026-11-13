package ingress

import (
	"github.com/QuantumNous/astrlink/core/contract"
)

// maxContextTextRunes bounds each text ContextTexts returns; a rules or
// memory file is far shorter.
const maxContextTextRunes = 200_000

// ContextTexts reads what a captured request body carried, as text, for the
// audit view that already shows the body: its rules files, memory files and
// skills catalogs, the compaction summary it continued from, and what the
// tool calls its history ends with wrote to memory and rules files and read
// of memory files. Names and
// paths read as the records keep them, so a record's items find their text.
// A memory file stays apart from the index line that links it.
func ContextTexts(protocol contract.ProtocolID, raw []byte) contract.RequestContextTexts {
	texts := contract.RequestContextTexts{Items: []contract.ContextItemText{}, Writes: []contract.MemoryWriteText{}, Reads: []contract.MemoryReadText{}}
	w := walkRequestContext(protocol, raw, true)
	if w == nil {
		return texts
	}
	observation := w.finish()
	for _, item := range observation.items {
		if item.kind == contract.ContextItemMCP || item.text == "" {
			continue
		}
		texts.Items = append(texts.Items, contract.ContextItemText{
			Kind: item.kind,
			Name: contextText(item.name, contract.MaxContextNameRunes),
			Path: contextText(homePath(item.path), contract.MaxContextPathRunes),
			Text: contract.ClampRunes(item.text, maxContextTextRunes),
		})
	}
	for _, write := range observation.written {
		name := contextText(write.name, contract.MaxContextNameRunes)
		if name == "" || write.content == "" {
			continue
		}
		texts.Writes = append(texts.Writes, contract.MemoryWriteText{
			Kind:    write.kind,
			Name:    name,
			Path:    contextText(homePath(write.path), contract.MaxContextPathRunes),
			Content: contract.ClampRunes(write.content, maxContextTextRunes),
		})
	}
	for _, read := range observation.reads {
		name := contextText(read.name, contract.MaxContextNameRunes)
		if name == "" || read.content == "" {
			continue
		}
		texts.Reads = append(texts.Reads, contract.MemoryReadText{
			Name:    name,
			Path:    contextText(homePath(read.path), contract.MaxContextPathRunes),
			Content: contract.ClampRunes(read.content, maxContextTextRunes),
		})
	}
	texts.Summary = contract.ClampRunes(w.summary, maxContextTextRunes)
	return texts
}
