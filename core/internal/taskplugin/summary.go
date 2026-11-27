package taskplugin

import (
	"encoding/json"
	"reflect"
	"slices"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
)

// Summary describes a task plugin for the plugin page in language.
func Summary(loaded *plugin.NewAPIPlugin, language string) *contract.PluginTask {
	meta, auth := loaded.Meta, loaded.AuthKind()
	summary := &contract.PluginTask{
		Icon: meta.Icon, Description: meta.Description.Text(language), Author: meta.Author.Name, AuthorURL: meta.Author.URL,
		Website: meta.Website, Models: slices.Clone(meta.Models), FetchMode: meta.FetchMode, Auth: auth,
		BaseURL: DefaultBaseURL(loaded), AllowedHosts: slices.Clone(meta.AllowedHosts), NewAPI: slices.Contains(meta.Upstreams, contract.PluginUpstreamNewAPI),
		Protocols: []contract.PluginTaskProtocol{}, Routes: []contract.PluginTaskRoute{}, SortPriority: int32(meta.SortPriority),
		Capabilities: slices.Clone(meta.RequiredCapabilities),
	}
	for _, claim := range meta.Protocols {
		summary.Protocols = append(summary.Protocols, contract.PluginTaskProtocol{Name: claim.Name, Supports: slices.Clone(claim.Supports), Models: slices.Clone(claim.Models)})
	}
	for _, route := range meta.Routes {
		summary.Routes = append(summary.Routes, contract.PluginTaskRoute{Method: route.Method, Path: route.Path, Type: string(route.Type), Models: slices.Clone(route.Models)})
	}
	if len(meta.UsageSchema) > 0 || len(meta.UsageExamples) > 0 || len(meta.UsageProfiles) > 0 {
		summary.Usage = &contract.PluginTaskUsage{Schema: rawJSON(meta.UsageSchema), Examples: rawJSON(meta.UsageExamples), Profiles: rawJSON(meta.UsageProfiles)}
	}
	return summary
}

// rawJSON is a member of the manifest as the plugin page reads it, nil when
// the plugin leaves it out.
func rawJSON(value any) json.RawMessage {
	if reflected := reflect.ValueOf(value); reflected.Len() == 0 {
		return nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	return encoded
}
