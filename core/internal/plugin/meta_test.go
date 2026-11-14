package plugin

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func validMeta() Meta {
	return Meta{
		APIVersion:   1,
		Key:          "powercontext",
		Name:         "PowerContext 记忆",
		Version:      "1.0.0",
		Capabilities: []string{CapabilityInject, CapabilityRequest, CapabilityNetwork, CapabilityStorage, CapabilityViews},
		Network:      &NetworkUse{Purpose: "访问 PowerContext 服务器，地址由用户在插件页授权"},
		Credentials:  []CredentialSlot{{Name: "token", Label: "PowerContext 令牌", Header: "Authorization", Scheme: "Bearer"}},
		Metadata:     []MetadataAttribute{{Key: "project", Label: "项目", Pattern: "^[a-z0-9][a-z0-9._-]{0,63}$", Marker: "memory-project"}},
		Notice:       "启用后，每个新会话的前几轮会请模型问用户。\n用户回答后，网关据此绑定本会话。",
	}
}

func TestValidateMetaAcceptsTheDesignManifest(t *testing.T) {
	if err := validateMeta(validMeta(), "powercontext"); err != nil {
		t.Fatalf("validateMeta: %v", err)
	}
	minimal := Meta{APIVersion: 1, Key: "team-rules", Name: "Team rules", Version: "0.0.2"}
	if err := validateMeta(minimal, "team-rules"); err != nil {
		t.Fatalf("validateMeta of a plugin without capabilities: %v", err)
	}
}

func TestValidateMetaRejects(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*Meta)
		want   string
	}{
		{"another contract", func(meta *Meta) { meta.APIVersion = 3 }, "apiVersion 3"},
		{"bad key", func(meta *Meta) { meta.Key = "Power_Context" }, "meta.key"},
		{"key of another directory", func(meta *Meta) { meta.Key = "memory" }, "does not match"},
		{"empty name", func(meta *Meta) { meta.Name = "" }, "meta.name"},
		{"control character in name", func(meta *Meta) { meta.Name = "Power\x1bContext" }, "meta.name"},
		{"long version", func(meta *Meta) { meta.Version = strings.Repeat("1", 33) }, "meta.version"},
		{"unknown capability", func(meta *Meta) { meta.Capabilities = append(meta.Capabilities, "filesystem") }, `unknown capability "filesystem"`},
		{"capability twice", func(meta *Meta) { meta.Capabilities = append(meta.Capabilities, CapabilityStorage) }, "twice"},
		{"network without purpose", func(meta *Meta) { meta.Network = nil }, "meta.network.purpose"},
		{"purpose without network", func(meta *Meta) {
			meta.Capabilities = slices.DeleteFunc(meta.Capabilities, func(capability string) bool { return capability == CapabilityNetwork })
		}, "needs the network capability"},
		{"credentials without network", func(meta *Meta) {
			meta.Capabilities = slices.DeleteFunc(meta.Capabilities, func(capability string) bool { return capability == CapabilityNetwork })
			meta.Network = nil
		}, "credentials need the network capability"},
		{"bad credential name", func(meta *Meta) { meta.Credentials[0].Name = "Token" }, "credentials[0].name"},
		{"credential twice", func(meta *Meta) { meta.Credentials = append(meta.Credentials, meta.Credentials[0]) }, `names "token" twice`},
		{"credential into Host", func(meta *Meta) { meta.Credentials[0].Header = "Host" }, "credentials[0].header"},
		{"credential into the gateway namespace", func(meta *Meta) { meta.Credentials[0].Header = "X-AstrLink-Token" }, "credentials[0].header"},
		{"credential into a proxy header", func(meta *Meta) { meta.Credentials[0].Header = "Proxy-Authorization" }, "credentials[0].header"},
		{"bad scheme", func(meta *Meta) { meta.Credentials[0].Scheme = "Bearer token" }, "credentials[0].scheme"},
		{"bad attribute key", func(meta *Meta) { meta.Metadata[0].Key = "Project" }, "metadata[0]: key"},
		{"unanchored pattern", func(meta *Meta) { meta.Metadata[0].Pattern = "[a-z]+" }, "must start with ^"},
		{"pattern that does not compile", func(meta *Meta) { meta.Metadata[0].Pattern = "^[a-z$" }, "does not compile"},
		{"marker naming the gateway", func(meta *Meta) { meta.Metadata[0].Marker = "astrlink-project" }, "must not name the gateway"},
		{"attribute twice", func(meta *Meta) { meta.Metadata = append(meta.Metadata, meta.Metadata[0]) }, `registers "project" twice`},
		{"marker twice", func(meta *Meta) {
			meta.Metadata = append(meta.Metadata, MetadataAttribute{Key: "env", Label: "环境", Pattern: "^(dev|prod)$", Marker: "memory-project"})
		}, "marker"},
		{"long notice", func(meta *Meta) { meta.Notice = strings.Repeat("提", 1001) }, "meta.notice"},
	} {
		t.Run(test.name, func(t *testing.T) {
			meta := validMeta()
			meta.Capabilities = slices.Clone(meta.Capabilities)
			meta.Credentials = slices.Clone(meta.Credentials)
			meta.Metadata = slices.Clone(meta.Metadata)
			test.change(&meta)
			err := validateMeta(meta, "powercontext")
			var loadErr *LoadError
			if !errors.As(err, &loadErr) || loadErr.Code != CodeInvalidMeta || !strings.Contains(loadErr.Message, test.want) {
				t.Fatalf("validateMeta = %v, want invalid_meta mentioning %q", err, test.want)
			}
		})
	}
}

func TestMetaGrantedKeepsOnlyRequestedCapabilities(t *testing.T) {
	meta := Meta{Capabilities: []string{CapabilityRequest, CapabilityNetwork}}
	got := meta.Granted([]string{CapabilityNetwork, CapabilityStorage, CapabilityRequest})
	if !slices.Equal(got, []string{CapabilityRequest, CapabilityNetwork}) {
		t.Fatalf("Granted = %v", got)
	}
}
