package plugin

import (
	"encoding/json"
	"sort"
	"strings"
	"unicode/utf16"

	"github.com/MoeForge/moejs"
	"github.com/MoeForge/moejs/engine"
)

// RequestFiles is what the request processing hooks read of their
// request's files and of their instance's keyword lists: utils.attachments,
// utils.media, utils.mediaText and utils.keywords. The host keeps the
// files' bytes; hooks get references, descriptions and capped text.
type RequestFiles struct {
	Attachments []Attachment
	// Text reads a file's text: {text, truncated}, {unchecked: reason}, or
	// nil for a string that is not a reference of the call.
	Text func(ref string, maxChars int) map[string]any
	// Keywords are the instance's keyword lists by property name.
	Keywords map[string][]string
	// Used notes what the hook did with a file: listed, read or sent.
	Used func(ref, how string)
}

// Attachment is one file of a request, as utils.attachments lists it.
type Attachment struct {
	Ref        string `json:"ref,omitempty"`
	Kind       string `json:"kind"`
	Mime       string `json:"mime"`
	Size       *int64 `json:"size,omitempty"`
	Filename   string `json:"filename,omitempty"`
	URL        string `json:"url,omitempty"`
	Path       string `json:"path"`
	Rewritable bool   `json:"rewritable,omitempty"`
	Width      int    `json:"-"`
	Height     int    `json:"-"`
}

// maxKeywordHits bounds what utils.keywords returns.
const maxKeywordHits = 32

// requestUtils are the utils the request processing hooks add to new-api's.
func (bound *instance) requestUtils() map[string]any {
	runtime := bound.runtime
	files := func() *RequestFiles {
		if c := bound.current; c != nil {
			return c.options.RequestFiles
		}
		return nil
	}
	unavailable := func(realm *moejs.Realm, name string) (moejs.Value, error) {
		return moejs.Undefined(), realm.Throw(engine.ObjectValue(realm.NewError(engine.KindError, "utils.%s is not available here", name)))
	}
	find := func(ref string) *Attachment {
		if current := files(); current != nil {
			for index := range current.Attachments {
				if current.Attachments[index].Ref == ref && ref != "" {
					return &current.Attachments[index]
				}
			}
		}
		return nil
	}
	return map[string]any{
		"attachments": runtime.Function("attachments", 0, func(realm *moejs.Realm, _ moejs.Value, _ []moejs.Value) (moejs.Value, error) {
			current := files()
			if current == nil {
				return unavailable(realm, "attachments")
			}
			if current.Used != nil {
				current.Used("", "listed")
			}
			listed := current.Attachments
			if listed == nil {
				listed = []Attachment{}
			}
			encoded, _ := json.Marshal(listed)
			return realm.JSONParseGoString(string(encoded))
		}),
		"media": runtime.Function("media", 1, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			if files() == nil {
				return unavailable(realm, "media")
			}
			ref := moejs.Arg(args, 0)
			if !ref.IsString() {
				return moejs.Null(), nil
			}
			found := find(ref.String())
			if found == nil {
				return moejs.Null(), nil
			}
			described := map[string]any{"kind": found.Kind, "mime": found.Mime}
			if found.Size != nil {
				described["size"] = *found.Size
			}
			if found.Filename != "" {
				described["filename"] = found.Filename
			}
			if found.Width > 0 {
				described["width"], described["height"] = found.Width, found.Height
			}
			if found.Rewritable {
				described["rewritable"] = true
			}
			return realm.FromGo(described)
		}),
		"mediaText": runtime.Function("mediaText", 2, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			current := files()
			if current == nil {
				return unavailable(realm, "mediaText")
			}
			ref := moejs.Arg(args, 0)
			if !ref.IsString() || find(ref.String()) == nil || current.Text == nil {
				return moejs.Null(), nil
			}
			maxChars := 131072
			var options struct {
				MaxChars *int `json:"maxChars"`
			}
			if second := moejs.Arg(args, 1); second.IsObject() && runtime.UnmarshalWith(second, &options, moejs.DecodeOptions{MaxBytes: 1 << 10, MaxNodes: 16}) == nil && options.MaxChars != nil {
				maxChars = min(max(*options.MaxChars, 1), 131072)
			}
			read := current.Text(ref.String(), maxChars)
			if read == nil {
				return moejs.Null(), nil
			}
			return realm.FromGo(read)
		}),
		"keywords": runtime.Function("keywords", 3, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			current := files()
			if current == nil {
				return unavailable(realm, "keywords")
			}
			name, text := moejs.Arg(args, 0), moejs.Arg(args, 1)
			if !name.IsString() || !text.IsString() {
				return moejs.Undefined(), realm.TypeError("utils.keywords: the name and the text must be strings")
			}
			list, ok := current.Keywords[name.String()]
			if !ok {
				return moejs.Undefined(), realm.Throw(engine.ObjectValue(realm.NewError(engine.KindError, "utils.keywords: %q is not a keyword list of the config", name.String())))
			}
			var options struct {
				CaseSensitive bool `json:"caseSensitive"`
			}
			if third := moejs.Arg(args, 2); third.IsObject() {
				_ = runtime.UnmarshalWith(third, &options, moejs.DecodeOptions{MaxBytes: 1 << 10, MaxNodes: 16})
			}
			return realm.FromGo(matchKeywords(list, text.String(), options.CaseSensitive))
		}),
	}
}

// matchKeywords returns the keywords of list that text contains, as the
// list spells them, in the order they end in text, at most 32.
func matchKeywords(list []string, text string, caseSensitive bool) []any {
	haystack := text
	if !caseSensitive {
		haystack = strings.ToLower(text)
	}
	type hit struct {
		keyword string
		end     int
	}
	var hits []hit
	for _, keyword := range list {
		needle := keyword
		if !caseSensitive {
			needle = strings.ToLower(keyword)
		}
		if at := strings.Index(haystack, needle); at >= 0 && needle != "" {
			hits = append(hits, hit{keyword, len(utf16.Encode([]rune(haystack[:at+len(needle)])))})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].end < hits[j].end })
	found := []any{}
	for _, hit := range hits {
		if len(found) == maxKeywordHits {
			break
		}
		found = append(found, hit.keyword)
	}
	return found
}
