package clientconfig

import (
	"context"
	"errors"
	"net/url"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/QuantumNous/astrlink/desktop/internal/agents"
	"github.com/QuantumNous/astrlink/desktop/internal/clientconfig/format"
)

// What a write or removal would change, for the dialog: a masked diff per
// file and the keys counted.

// SecretName reports whether a key's value is a credential or otherwise
// private, judged by its name.
func SecretName(name string) bool {
	name = strings.ToLower(name)
	for _, word := range []string{"key", "token", "secret", "password", "passwd", "auth", "credential", "bearer", "cookie", "session"} {
		if strings.Contains(name, word) {
			return name != "selectedtype"
		}
	}
	return false
}

// DisplayedValue is a value as a confirmation shows it: a credential by its
// ends only, as the diff does; an address without the user, password and
// query that may carry one; anything else as it is. nil for a value that
// is not one line of text, like an object, or is empty.
func DisplayedValue(path, value string) *string {
	const longest = 200
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsAny(value, "\n\r") || strings.HasPrefix(value, "{") || strings.HasPrefix(value, "[") {
		return nil
	}
	name := path[strings.LastIndexByte(path, '.')+1:]
	if SecretName(name) {
		return Some(mask(value))
	}
	shown := value
	if address, err := url.Parse(value); err == nil &&
		slices.Contains([]string{"http", "https", "ws", "wss"}, address.Scheme) && address.Host != "" &&
		(address.User != nil || address.RawQuery != "" || address.ForceQuery || address.Fragment != "") {
		address.User = nil
		address.RawQuery = ""
		address.ForceQuery = false
		address.Fragment = ""
		address.RawFragment = ""
		if address.Path == "" {
			address.Path = "/"
		}
		shown = address.String()
	}
	if chars := []rune(shown); len(chars) > longest {
		shown = string(chars[:longest]) + "…"
	}
	return &shown
}

// mask shows a secret by its ends only.
func mask(value string) string {
	if strings.Contains(value, "…") {
		return value
	}
	chars := []rune(value)
	switch n := len(chars); {
	case n == 0:
		return ""
	case n >= 12:
		return string(chars[:4]) + "…" + string(chars[n-3:])
	case n > 4:
		return "…" + string(chars[n-2:])
	}
	return "…"
}

func isNameRune(c rune) bool {
	return c < utf8.RuneSelf && (c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') || c == '_' || c == '-' || c == '.'
}

// maskValues masks the values of secret-named keys on one line of a config
// file: `"apiKey": "…"`, `token = "…"`, or `NAME=value`; every value when
// all, for a line of a file that holds credentials only.
func maskValues(line string, all bool) string {
	var out strings.Builder
	rest := line
	for {
		at := strings.IndexAny(rest, ":=")
		if at < 0 {
			break
		}
		before, after := rest[:at], rest[at+1:]
		out.WriteString(before)
		out.WriteByte(rest[at])
		trimmed := strings.TrimRight(strings.TrimRightFunc(before, unicode.IsSpace), `"'`)
		name := trimmed
		if i := strings.LastIndexFunc(trimmed, func(c rune) bool { return !isNameRune(c) }); i >= 0 {
			_, size := utf8.DecodeRuneInString(trimmed[i:])
			name = trimmed[i+size:]
		}
		value := strings.TrimLeftFunc(after, unicode.IsSpace)
		out.WriteString(after[:len(after)-len(value)])
		rest = value
		if name == "" || !(all || SecretName(name)) {
			continue
		}
		var secret, tail string
		quoted := value != "" && (value[0] == '"' || value[0] == '\'')
		switch {
		case quoted:
			if end := format.ClosingQuote(value, rune(value[0])); end >= 0 {
				out.WriteByte(value[0])
				secret, tail = value[1:end], value[end:]
			} else {
				secret, tail = value, ""
			}
		default:
			end := strings.IndexFunc(value, func(c rune) bool { return unicode.IsSpace(c) || strings.ContainsRune(",#}]", c) })
			if end < 0 {
				end = len(value)
			}
			secret, tail = value[:end], value[end:]
		}
		// Objects and other non-text values hold no secret themselves.
		if secret == "" || !quoted && (secret[0] == '{' || secret[0] == '[') {
			continue
		}
		out.WriteString(mask(secret))
		rest = tail
	}
	out.WriteString(rest)
	return out.String()
}

// LineKind is what a diff line is.
type LineKind string

const (
	LineContext LineKind = "context"
	LineAdd     LineKind = "add"
	LineRemove  LineKind = "remove"
	// LineGap stands for unchanged lines left out between two changes.
	LineGap LineKind = "gap"
)

// DiffLine is one line of a diff.
type DiffLine struct {
	Kind LineKind `json:"kind"`
	Text string   `json:"text"`
}

func diffSplit(text string) []string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSuffix(line, "\r")
	}
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// diffLines is the lines of before and after that differ, with two lines
// of context.
func diffLines(before, after string) []DiffLine {
	old, new := diffSplit(before), diffSplit(after)
	prefix := 0
	for prefix < len(old) && prefix < len(new) && old[prefix] == new[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(old)-prefix && suffix < len(new)-prefix && old[len(old)-1-suffix] == new[len(new)-1-suffix] {
		suffix++
	}
	a, b := old[prefix:len(old)-suffix], new[prefix:len(new)-suffix]
	var ops []DiffLine
	for _, line := range old[:prefix] {
		ops = append(ops, DiffLine{LineContext, line})
	}
	// Longest common subsequence of the changed middle; past this size the
	// middle is shown as replaced whole.
	if len(a)*len(b) <= 4_000_000 {
		table := make([][]uint32, len(a)+1)
		for i := range table {
			table[i] = make([]uint32, len(b)+1)
		}
		for i := len(a) - 1; i >= 0; i-- {
			for j := len(b) - 1; j >= 0; j-- {
				if a[i] == b[j] {
					table[i][j] = table[i+1][j+1] + 1
				} else {
					table[i][j] = max(table[i+1][j], table[i][j+1])
				}
			}
		}
		i, j := 0, 0
		for i < len(a) || j < len(b) {
			switch {
			case i < len(a) && j < len(b) && a[i] == b[j]:
				ops = append(ops, DiffLine{LineContext, a[i]})
				i++
				j++
			case i < len(a) && (j == len(b) || table[i+1][j] >= table[i][j+1]):
				ops = append(ops, DiffLine{LineRemove, a[i]})
				i++
			default:
				ops = append(ops, DiffLine{LineAdd, b[j]})
				j++
			}
		}
	} else {
		for _, line := range a {
			ops = append(ops, DiffLine{LineRemove, line})
		}
		for _, line := range b {
			ops = append(ops, DiffLine{LineAdd, line})
		}
	}
	for _, line := range old[len(old)-suffix:] {
		ops = append(ops, DiffLine{LineContext, line})
	}
	const context = 2
	var changed []int
	for i, op := range ops {
		if op.Kind != LineContext {
			changed = append(changed, i)
		}
	}
	shown := func(index int) bool {
		return slices.ContainsFunc(changed, func(at int) bool { return index+context >= at && index <= at+context })
	}
	lines := []DiffLine{}
	skipped := false
	for i, op := range ops {
		if !shown(i) {
			skipped = true
			continue
		}
		if skipped && len(lines) > 0 {
			lines = append(lines, DiffLine{Kind: LineGap})
		}
		skipped = false
		lines = append(lines, op)
	}
	return lines
}

// FileChanges is what a write or removal changes in one file.
type FileChanges struct {
	Path    string `json:"path"`
	Created bool   `json:"created"`
	Deleted bool   `json:"deleted"`
	// Changes counts the keys whose value changes.
	Changes int `json:"changes"`
	// Unit is what a change counts: `line` for `.env`, else `key`.
	Unit string `json:"unit"`
	// Lines show the token as its hint, and other secret values by their
	// ends.
	Lines []DiffLine `json:"lines"`
}

// Preview is what writing or removing one place would change.
type Preview struct {
	Files []FileChanges `json:"files"`
	// Replaced counts values AstrLink did not write that the write replaces
	// or removes.
	Replaced int `json:"replaced"`
	// Restored counts values a removal puts back.
	Restored int `json:"restored"`
}

// Hidden is a secret and what shows in its place.
type Hidden struct{ Secret, Hint string }

func sameText(a, b *string) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

func text(file *string) string {
	if file == nil {
		return ""
	}
	return *file
}

// describe is the changes from before to after per file, masked. keys are
// the keys worth counting.
func describe(client Client, paths []string, before, after []*string, keys []string, secrets []Hidden) []FileChanges {
	old, err := ParseConfig(client, before)
	if err != nil {
		return []FileChanges{}
	}
	new, err := ParseConfig(client, after)
	if err != nil {
		return []FileChanges{}
	}
	changes := make([]int, len(paths))
	slices.Sort(keys)
	for _, key := range slices.Compact(keys) {
		v1, ok1, p1 := old.Get(key)
		v2, ok2, p2 := new.Get(key)
		if p1 != p2 || ok1 != ok2 || v1 != v2 {
			changes[old.Index(key)]++
		}
	}
	files := client.Files()
	out := []FileChanges{}
	for i, path := range paths {
		if sameText(before[i], after[i]) {
			continue
		}
		lines := diffLines(FilePreview(files[i], text(before[i])), FilePreview(files[i], text(after[i])))
		for j := range lines {
			line := lines[j].Text
			for _, hidden := range secrets {
				if hidden.Secret != "" {
					line = strings.ReplaceAll(line, hidden.Secret, hidden.Hint)
				}
			}
			lines[j].Text = maskValues(line, FileSecret(files[i]))
		}
		out = append(out, FileChanges{
			Path:    path,
			Created: before[i] == nil,
			Deleted: after[i] == nil,
			Changes: changes[i],
			Unit:    FileUnit(files[i]),
			Lines:   lines,
		})
	}
	return out
}

// PreviewWrite is a write to preview: what it writes, with the token and
// its hint.
type PreviewWrite struct {
	Selection *Selection
	Secret    Secret
	Hint      string
	Details   Catalog
}

// PreviewChanges is what writing or removing one place would change, for
// the dialog. The token never leaves: it shows as its hint.
func PreviewChanges(ctx context.Context, gateway Gateway, homes *agents.Homes, tokenID *string, place Place, wsl *string, action Action, models *Models, inferenceURL string) (*Preview, error) {
	origin, err := LocalOrigin(inferenceURL)
	if err != nil {
		return nil, err
	}
	var write *PreviewWrite
	if action == ActionWrite {
		if models == nil {
			return nil, errors.New("models are required")
		}
		selection, err := Select(models, place.Client)
		if err != nil {
			return nil, err
		}
		if tokenID == nil {
			return nil, errors.New("access token is required")
		}
		_, hint, err := TokenSummary(ctx, gateway, *tokenID)
		if err != nil {
			return nil, err
		}
		token, err := RevealAccessToken(ctx, gateway, *tokenID, inferenceURL)
		if err != nil {
			return nil, err
		}
		known := Catalog{}
		if place.Client.ListsModels() {
			officialDetails(ctx, gateway, selection.List, known)
		}
		write = &PreviewWrite{selection, Secret{*tokenID, token}, hint, catalogFor(selection, known)}
	}
	home := HomeOf(homes, wsl)
	if home == nil {
		return nil, unavailable(wsl)
	}
	return PreviewPlace(*home, place, origin, write)
}

// PreviewPlace is PreviewChanges for one place in a home, without asking
// the gateway.
func PreviewPlace(home agents.Home, place Place, origin string, write *PreviewWrite) (*Preview, error) {
	client := place.Client
	defer agents.LockHostFiles()()
	records, err := ReadRecords(home)
	if err != nil {
		return nil, err
	}
	record := records.Get(place)
	paths := client.ConfigPaths(home, place.Target, record)
	before, err := ReadFiles(client, paths)
	if err != nil {
		return nil, err
	}
	if write != nil {
		connection := write.Selection.Connection(place.Target, write.Secret.ID, write.Secret.Token, origin)
		plan, planErr := PlanWrite(client, before, connection, write.Details, record)
		if planErr != nil {
			return nil, planErr.At(paths)
		}
		// Files the write leaves missing stay missing, and ones it empties
		// go.
		after := make([]*string, len(plan.Contents))
		for i, contents := range plan.Contents {
			if contents == "" && (before[i] == nil || *before[i] != "") {
				continue
			}
			after[i] = &plan.Contents[i]
		}
		keys := append(slices.Collect(mapKeys(plan.Record.Keys)), plan.Conflicts...)
		if record != nil {
			keys = append(keys, slices.Collect(mapKeys(record.Keys))...)
		}
		return &Preview{
			Files:    describe(client, paths, before, after, keys, []Hidden{{write.Secret.Token, write.Hint}}),
			Replaced: len(plan.Conflicts),
		}, nil
	}
	if record == nil {
		return &Preview{Files: []FileChanges{}}, nil
	}
	removals, restored, planErr := PlanRestore(client, before, record)
	if planErr != nil {
		return nil, planErr.At(paths)
	}
	after := make([]*string, len(removals))
	for i, removal := range removals {
		switch removal.Kind {
		case RemovalUnchanged:
			after[i] = before[i]
		case RemovalWrite:
			after[i] = &removals[i].Contents
		}
	}
	keys := append(slices.Collect(mapKeys(record.Keys)), slices.Collect(mapKeys(record.Backup))...)
	return &Preview{Files: describe(client, paths, before, after, keys, nil), Restored: restored}, nil
}
