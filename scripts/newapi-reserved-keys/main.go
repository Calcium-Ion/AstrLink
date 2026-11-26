// Command newapi-reserved-keys prints, as a JSON array, the plugin keys
// new-api refuses because they would collide with its own paths: api, the
// first segments of the routes it registers before plugin routes and of
// jsplugin's reserved route namespaces, and the route prefixes its built-in
// plugins own (ali, suno). It runs in a module that replaces new-api with a
// checkout, which scripts/refresh-newapi-conformance.sh sets up; the
// checkout's path is its argument.
package main

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/pkg/jsplugin"
	_ "github.com/QuantumNous/new-api/plugins"
	"github.com/QuantumNous/new-api/router"
	"github.com/gin-gonic/gin"
)

func main() {
	gin.SetMode(gin.ReleaseMode)
	engine := gin.New()
	router.SetApiRouter(engine)
	router.SetDashboardRouter(engine)
	router.SetRelayRouter(engine)
	router.SetTaskPluginProtocolRouter(engine)
	router.SetVideoRouter(engine)
	router.SetTaskRouter(engine)
	// The routers SetRouter registers before SetPluginRouter, whose routes
	// the plugin router reads as host segments.
	keys := map[string]string{"api": "host"}
	for _, route := range engine.Routes() {
		first, _, _ := strings.Cut(strings.TrimPrefix(route.Path, "/"), "/")
		if first != "" && !strings.HasPrefix(first, ":") && !strings.HasPrefix(first, "*") {
			keys[first] = "host"
		}
	}
	for _, namespace := range reservedNamespaces(os.Args[1]) {
		first, _, _ := strings.Cut(strings.TrimPrefix(namespace, "/"), "/")
		keys[first] = "host"
	}
	generation := jsplugin.DefaultRegistry.Generation()
	for _, plugin := range generation.Plugins() {
		if !generation.FactoryKey(plugin.Meta.Key) {
			continue
		}
		for _, binding := range jsplugin.PluginRouteBindings(plugin) {
			if binding.OwnPrefix {
				first, _, _ := strings.Cut(strings.TrimPrefix(binding.Route.Path, "/"), "/")
				if first != plugin.Meta.Key {
					keys[first] = plugin.Meta.Key
				}
			}
		}
	}
	names := make([]string, 0, len(keys))
	for name := range keys {
		names = append(names, name)
	}
	sort.Strings(names)
	_ = json.NewEncoder(os.Stdout).Encode(names)
}

// reservedNamespaces reads jsplugin's unexported reservedRouteNamespaces
// from the checkout's source.
func reservedNamespaces(checkout string) []string {
	file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(checkout, "pkg/jsplugin/routing.go"), nil, 0)
	if err != nil {
		panic(err)
	}
	var namespaces []string
	ast.Inspect(file, func(node ast.Node) bool {
		spec, ok := node.(*ast.ValueSpec)
		if !ok || len(spec.Names) != 1 || spec.Names[0].Name != "reservedRouteNamespaces" {
			return true
		}
		for _, element := range spec.Values[0].(*ast.CompositeLit).Elts {
			value, _ := strconv.Unquote(element.(*ast.BasicLit).Value)
			namespaces = append(namespaces, value)
		}
		return false
	})
	if len(namespaces) == 0 {
		panic("reservedRouteNamespaces not found")
	}
	return namespaces
}
