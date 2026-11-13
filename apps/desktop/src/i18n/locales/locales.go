// Package locales embeds the frontend's message catalogs for the Go host,
// which shows the messages of their host section itself.
package locales

import "embed"

// FS holds en.json, ja.json and zh-CN.json.
//
//go:embed en.json ja.json zh-CN.json
var FS embed.FS
