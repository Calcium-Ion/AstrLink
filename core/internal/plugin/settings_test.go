package plugin

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
)

func number(value float64) *float64 { return &value }

func settingsMeta() Meta {
	meta := validMeta()
	meta.Settings = []Setting{
		{Key: "serverUrl", Label: "服务器地址", Type: SettingServer, Group: "连接", Required: true, Credential: "token"},
		{Key: "quotas.atomic-memory", Label: "原子记忆", Type: SettingNumber, Group: "召回", Row: "每轮条数", Default: json.RawMessage(`4`), Min: number(0), Max: number(20), Step: number(1), Unit: "条"},
		{Key: "quotas.topic-memory", Label: "主题记忆", Type: SettingNumber, Group: "召回", Row: "每轮条数", Default: json.RawMessage(`3`), Min: number(0), Max: number(20)},
		{Key: HostSettingWait, Group: "召回"},
		{Key: "askProject", Label: "没有项目时询问", Type: SettingSwitch, Group: "会话", Default: json.RawMessage(`true`)},
		{Key: "style", Label: "风格", Type: SettingChoice, Group: "会话", Options: []SettingOption{{Value: "brief", Label: "简短"}, {Value: "full", Label: "完整"}}},
		{Key: HostSettingTrusted, Group: "隐私"},
		{Key: "note", Label: "备注", Type: SettingText, Advanced: true, Placeholder: "可选"},
		{Key: HostSettingOrigins, Advanced: true},
		{Key: HostSettingClients, Group: "会话"},
		{Key: HostSettingWorkspace, Group: "会话"},
		{Key: HostSettingRefresh, Group: "会话"},
	}
	meta.Storage = []StorageItem{{Label: "项目对应的服务器编号", Retention: "停用插件时可以清除"}}
	meta.Credits = []Credit{{Name: "PowerContext", Owner: "OceanBase", License: "Apache-2.0", URL: "https://github.com/oceanbase/powercontext"}}
	return meta
}

func TestSettingsAndCreditsValidate(t *testing.T) {
	if err := validateMeta(settingsMeta(), "powercontext"); err != nil {
		t.Fatalf("validateMeta: %v", err)
	}
}

func TestSettingsAndCreditsRejects(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*Meta)
		want   string
	}{
		{"unknown host setting", func(meta *Meta) { meta.Settings[3].Key = "$color" }, "not a host setting"},
		{"host setting with a label", func(meta *Meta) { meta.Settings[3].Label = "等待" }, "takes only group and advanced"},
		{"bad key", func(meta *Meta) { meta.Settings[1].Key = "quotas..atomic" }, "words of letters"},
		{"key twice", func(meta *Meta) { meta.Settings[2].Key = "quotas.atomic-memory" }, "declared twice"},
		{"key inside another", func(meta *Meta) { meta.Settings[2].Key = "quotas" }, "lies inside"},
		{"no label", func(meta *Meta) { meta.Settings[1].Label = "" }, "label"},
		{"unknown type", func(meta *Meta) { meta.Settings[1].Type = "slider" }, "type"},
		{"server with a default", func(meta *Meta) { meta.Settings[0].Default = json.RawMessage(`"https://memory.test"`) }, "no default"},
		{"server binding an undeclared credential", func(meta *Meta) { meta.Settings[0].Credential = "secret" }, "not declared"},
		{"credential on a number", func(meta *Meta) { meta.Settings[1].Credential = "token" }, "only a server"},
		{"two servers sharing a credential", func(meta *Meta) {
			meta.Settings = append(meta.Settings, Setting{Key: "mirror", Label: "备用", Type: SettingServer, Credential: "token"})
		}, "two servers"},
		{"server without the network", func(meta *Meta) {
			meta.Capabilities = slices.DeleteFunc(meta.Capabilities, func(capability string) bool { return capability == CapabilityNetwork })
			meta.Network, meta.Credentials = nil, nil
			meta.Settings = []Setting{{Key: "serverUrl", Label: "服务器地址", Type: SettingServer}}
		}, "a server needs the network capability"},
		{"min above max", func(meta *Meta) { meta.Settings[1].Min = number(30) }, "min is above max"},
		{"step of zero", func(meta *Meta) { meta.Settings[1].Step = number(0) }, "step"},
		{"unit on a switch", func(meta *Meta) { meta.Settings[4].Unit = "个" }, "for numbers"},
		{"default out of range", func(meta *Meta) { meta.Settings[1].Default = json.RawMessage(`99`) }, "default must be from 0 to 20"},
		{"default of another type", func(meta *Meta) { meta.Settings[4].Default = json.RawMessage(`"yes"`) }, "default must be true or false"},
		{"choice without options", func(meta *Meta) { meta.Settings[5].Options = nil }, "takes options"},
		{"option twice", func(meta *Meta) {
			meta.Settings[5].Options = append(meta.Settings[5].Options, SettingOption{Value: "brief", Label: "又一个"})
		}, "twice"},
		{"required switch", func(meta *Meta) { meta.Settings[4].Required = true }, "never empty"},
		{"split row", func(meta *Meta) { meta.Settings[4].Row = "每轮条数" }, "not together"},
		{"row across groups", func(meta *Meta) { meta.Settings[2].Group = "其他" }, "not in one group"},
		{"long help", func(meta *Meta) { meta.Settings[1].Help = strings.Repeat("说", 501) }, "help"},
		{"credit without https", func(meta *Meta) { meta.Credits[0].URL = "http://github.com/oceanbase/powercontext" }, "https"},
		{"credit with a bad license", func(meta *Meta) { meta.Credits[0].License = "Apache 2.0" }, "SPDX"},
		{"credit without a name", func(meta *Meta) { meta.Credits[0].Name = "" }, "credits[0].name"},
		{"workspace without an attribute", func(meta *Meta) { meta.Metadata = nil }, "$workspace needs an attribute"},
		{"refresh without injecting", func(meta *Meta) {
			meta.Capabilities = slices.DeleteFunc(meta.Capabilities, func(capability string) bool { return capability == CapabilityInject })
		}, "$refresh needs the context.inject capability"},
		{"clients with a label", func(meta *Meta) { meta.Settings[9].Label = "客户端" }, "takes only group and advanced"},
		{"storage without the capability", func(meta *Meta) {
			meta.Capabilities = slices.DeleteFunc(meta.Capabilities, func(capability string) bool { return capability == CapabilityStorage })
		}, "meta.storage needs the storage capability"},
		{"storage without a retention", func(meta *Meta) { meta.Storage[0].Retention = "" }, "meta.storage[0]"},
		{"too much storage", func(meta *Meta) {
			for range 8 {
				meta.Storage = append(meta.Storage, meta.Storage[0])
			}
		}, "meta.storage has more than 8"},
	} {
		t.Run(test.name, func(t *testing.T) {
			meta := settingsMeta()
			test.change(&meta)
			err := validateMeta(meta, "powercontext")
			var loadErr *LoadError
			if !errors.As(err, &loadErr) || loadErr.Code != CodeInvalidMeta || !strings.Contains(loadErr.Message, test.want) {
				t.Fatalf("validateMeta = %v, want %q", err, test.want)
			}
		})
	}
}

func TestConfigGetsTheDefaultsItLacks(t *testing.T) {
	meta := settingsMeta()
	for _, test := range []struct{ config, want string }{
		{``, `{"askProject":true,"quotas":{"atomic-memory":4,"topic-memory":3}}`},
		{`{"quotas":{"atomic-memory":0},"extra":[1]}`, `{"askProject":true,"extra":[1],"quotas":{"atomic-memory":0,"topic-memory":3}}`},
		// A value in the way keeps the defaults beneath it out.
		{`{"quotas":5,"askProject":false}`, `{"askProject":false,"quotas":5}`},
	} {
		if got := string(meta.WithDefaults(json.RawMessage(test.config))); got != test.want {
			t.Fatalf("WithDefaults(%s) = %s, want %s", test.config, got, test.want)
		}
	}
	if got := string(validMeta().WithDefaults(json.RawMessage(`{"a":1}`))); got != `{"a":1}` {
		t.Fatalf("a manifest without settings changed the config: %s", got)
	}
}

func TestSwitchOnReadsTheConfigThenTheDefault(t *testing.T) {
	meta := settingsMeta()
	for _, test := range []struct {
		config string
		want   bool
	}{
		{``, true},
		{`{"askProject":false}`, false},
		{`{"askProject":true}`, true},
		{`{"askProject":null}`, true},
		{`{"askProject":"no"}`, false},
		{`[1]`, true},
	} {
		if got := meta.SwitchOn(json.RawMessage(test.config), "askProject"); got != test.want {
			t.Fatalf("SwitchOn(%s) = %v, want %v", test.config, got, test.want)
		}
	}
	// What is no switch of the plugin's is off, and so is a switch without
	// a default that the config leaves out.
	meta.Settings = append(meta.Settings, Setting{Key: "quiet", Label: "安静", Type: SettingSwitch})
	for _, key := range []string{"style", "missing", HostSettingTrusted, "quiet"} {
		if meta.SwitchOn(nil, key) {
			t.Fatalf("SwitchOn(%s) = true", key)
		}
	}
}

func TestCheckConfigNamesTheSettingItGetsWrong(t *testing.T) {
	meta := settingsMeta()
	if err := meta.CheckConfig(json.RawMessage(`{"serverUrl":"http://192.0.2.10:9090/api","quotas":{"atomic-memory":2},"unknown":{"kept":true}}`)); err != nil {
		t.Fatalf("CheckConfig = %v", err)
	}
	for _, test := range []struct{ config, key string }{
		{`{}`, "serverUrl"},
		{`{"serverUrl":"ftp://memory.test"}`, "serverUrl"},
		{`{"serverUrl":"https://memory.test","quotas":{"atomic-memory":21}}`, "quotas.atomic-memory"},
		{`{"serverUrl":"https://memory.test","askProject":"yes"}`, "askProject"},
		{`{"serverUrl":"https://memory.test","style":"loud"}`, "style"},
	} {
		var failure *SettingError
		if err := meta.CheckConfig(json.RawMessage(test.config)); !errors.As(err, &failure) || failure.Setting.Key != test.key {
			t.Fatalf("CheckConfig(%s) = %v, want a fault in %s", test.config, err, test.key)
		}
	}
	if err := meta.CheckConfig(json.RawMessage(`[1]`)); err == nil {
		t.Fatal("CheckConfig accepted an array")
	}
	if servers := meta.Servers(json.RawMessage(`{"serverUrl":"https://memory.test/api"}`)); servers["serverUrl"] != "https://memory.test/api" || len(servers) != 1 {
		t.Fatalf("Servers = %v", servers)
	}
}
