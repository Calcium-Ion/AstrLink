package taskplugin

import (
	"slices"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
)

// Summary describes a task plugin for the plugin page in language.
func Summary(meta *plugin.TaskMeta, language string) *contract.PluginTask {
	summary := &contract.PluginTask{
		Icon: meta.Icon, Description: meta.Description.In(language), Author: meta.Author.Name, AuthorURL: meta.Author.URL,
		Website: meta.Website, Models: slices.Clone(meta.Models), FetchMode: meta.FetchMode, Auth: string(meta.Auth.Kind()),
		BaseURL: DefaultBaseURL(meta), AllowedHosts: slices.Clone(meta.AllowedHosts), NewAPI: slices.Contains(meta.Upstreams, contract.PluginUpstreamNewAPI),
		Protocols: []contract.PluginTaskProtocol{}, Routes: []contract.PluginTaskRoute{}, SortPriority: meta.SortPriority,
		Capabilities: slices.Clone(meta.RequiredCapabilities),
	}
	for _, claim := range meta.Protocols {
		summary.Protocols = append(summary.Protocols, contract.PluginTaskProtocol{Name: claim.Name, Supports: slices.Clone(claim.Supports), Models: slices.Clone(claim.Models)})
	}
	for _, route := range meta.Routes {
		summary.Routes = append(summary.Routes, contract.PluginTaskRoute{Method: route.Method, Path: route.Path, Type: route.Type, Models: slices.Clone(route.Models)})
	}
	if len(meta.UsageSchema) > 0 || len(meta.UsageExamples) > 0 || len(meta.UsageProfiles) > 0 {
		summary.Usage = &contract.PluginTaskUsage{Schema: meta.UsageSchema, Examples: meta.UsageExamples, Profiles: meta.UsageProfiles}
	}
	return summary
}
