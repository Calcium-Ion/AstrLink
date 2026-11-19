// Package plugins holds the type definitions of the plugin API.
package plugins

import _ "embed"

// TypeDefinitions is astrlink-plugin.d.ts, the types of the plugin API,
// which astrlink plugin new puts in a new project.
//
//go:embed astrlink-plugin.d.ts
var TypeDefinitions string

// ClientTypeDefinitions is astrlink-plugin-client.d.ts, the types of the
// SDK a plugin's interface code imports, for a project with a client entry.
//
//go:embed astrlink-plugin-client.d.ts
var ClientTypeDefinitions string
