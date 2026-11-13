package clientconfig

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/QuantumNous/astrlink/desktop/internal/agents"
)

// Manual setup: the files a write creates for a client that has none.

// fromHome is a path as the user would type it from their home.
func fromHome(home agents.Home, path string) string {
	relative, err := filepath.Rel(home.Dir(), path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return home.Shown(path)
	}
	parts := strings.Split(relative, string(filepath.Separator))
	if home.Windows() {
		return `%USERPROFILE%\` + strings.Join(parts, `\`)
	}
	return "~/" + strings.Join(parts, "/")
}

// SnippetFile is one file of a manual setup.
type SnippetFile struct {
	// Path is where the file goes, from the home.
	Path     string `json:"path"`
	Contents string `json:"contents"`
}

// Snippet is the files Apply writes when a client has none yet, for
// manual setup; a client with several targets shows its first.
func Snippet(client Client, home agents.Home, connection *Connection, details Catalog) ([]SnippetFile, error) {
	target := ""
	if targets := client.Targets(); len(targets) > 0 {
		target = targets[0]
	}
	paths := client.ConfigPaths(home, target, nil)
	plan, err := PlanWrite(client, make([]*string, len(paths)), connection, details, nil)
	if err != nil {
		return nil, err
	}
	files := []SnippetFile{}
	for i, path := range paths {
		if strings.TrimSpace(plan.Contents[i]) != "" {
			files = append(files, SnippetFile{fromHome(home, path), plan.Contents[i]})
		}
	}
	return files, nil
}

// SnippetFiles is the files of a manual setup in the desktop user's home,
// with the token revealed or shown as its hint.
func SnippetFiles(ctx context.Context, gateway Gateway, host, tokenID string, client Client, models *Models, inferenceURL string, reveal bool) ([]SnippetFile, error) {
	origin, err := LocalOrigin(inferenceURL)
	if err != nil {
		return nil, err
	}
	selection, err := Select(models, client)
	if err != nil {
		return nil, err
	}
	var token string
	if reveal {
		token, err = RevealAccessToken(ctx, gateway, tokenID, inferenceURL)
	} else {
		_, token, err = TokenSummary(ctx, gateway, tokenID)
	}
	if err != nil {
		return nil, err
	}
	known := Catalog{}
	if client.ListsModels() {
		officialDetails(ctx, gateway, selection.List, known)
	}
	return Snippet(client, agents.Local(host), selection.Connection("", tokenID, token, origin), catalogFor(selection, known))
}
