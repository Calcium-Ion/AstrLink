package clientconfig

// The clients the dialog lists: the ones AstrLink writes, and the ones it
// can only explain. Icons and copy stay in the frontend, keyed by id.

// Kind is how AstrLink reaches a client.
type Kind string

const (
	// KindFile clients have their config files written by AstrLink.
	KindFile Kind = "file"
	// KindPaste clients have their config files written, with a token the
	// user pastes into the client once.
	KindPaste Kind = "paste"
	// KindLink clients take the client's own import link.
	KindLink Kind = "link"
	// KindGuide clients are told what to fill in, for the user to type.
	KindGuide Kind = "guide"
	// KindUnsupported clients cannot reach the local gateway.
	KindUnsupported Kind = "unsupported"
)

// CatalogEntry is one client the dialog lists.
type CatalogEntry struct {
	ID string `json:"id"`
	// Name is the product name, never translated.
	Name string `json:"name"`
	Kind Kind   `json:"kind"`
	// Targets are the write targets of a client with several; empty for
	// one.
	Targets []string `json:"targets"`
	// Protocols are the gateway protocols the client can be written or
	// filled with, preferred first.
	Protocols []string `json:"protocols"`
	// List reports whether the client switches among a written list of
	// models.
	List bool `json:"list"`
}

// ClientCatalog is every client the dialog lists.
type ClientCatalog struct {
	Clients []CatalogEntry `json:"clients"`
}

func listed(id, name string, kind Kind, protocols ...string) CatalogEntry {
	if protocols == nil {
		protocols = []string{}
	}
	return CatalogEntry{ID: id, Name: name, Kind: kind, Targets: []string{}, Protocols: protocols}
}

// guides are clients set up by hand in their own settings, with the
// protocols they take there.
var guides = []CatalogEntry{
	listed("trae", "TRAE", KindGuide, "anthropic.messages", "openai.responses", "openai.chat"),
	listed("roo_code", "Roo Code", KindGuide, "anthropic.messages", "openai.chat", "google.generate_content"),
	listed("lobehub", "LobeHub", KindGuide, "anthropic.messages", "openai.chat", "google.generate_content"),
	listed("lobsterai", "LobsterAI", KindGuide, "anthropic.messages", "openai.responses", "openai.chat", "google.generate_content"),
	listed("chatwise", "ChatWise", KindGuide, "anthropic.messages", "openai.chat"),
}

// unsupported are clients that send requests from their own servers, or
// whose personal editions take no address.
var unsupported = []CatalogEntry{
	listed("cursor", "Cursor", KindUnsupported),
	listed("warp", "Warp", KindUnsupported),
	listed("amp", "Amp", KindUnsupported),
	listed("trae_plugin", "TRAE Plugin", KindUnsupported),
	listed("comate", "文心快码 Comate", KindUnsupported),
	listed("qoder", "Qoder", KindUnsupported),
}

// CatalogOf is every client the dialog lists, by kind, in rail order.
func CatalogOf() ClientCatalog {
	var entries []CatalogEntry
	for _, client := range All() {
		targets := client.Targets()
		if targets == nil {
			targets = []string{}
		}
		entries = append(entries, CatalogEntry{
			ID:        client.ID(),
			Name:      client.Name(),
			Kind:      client.Kind(),
			Targets:   targets,
			Protocols: client.Protocols(),
			List:      client.ListsModels(),
		})
	}
	for _, link := range links {
		entries = append(entries, link.entry())
	}
	entries = append(entries, guides...)
	return ClientCatalog{Clients: append(entries, unsupported...)}
}

// Installed lists the link clients installed on this computer: those
// whose links opens says the OS opens. The dialog lists them among the
// installed ones.
func Installed(opens func(scheme string) bool) []string {
	installed := []string{}
	for _, link := range links {
		if opens(link.scheme) {
			installed = append(installed, link.id)
		}
	}
	return installed
}
