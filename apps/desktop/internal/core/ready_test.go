package core

import (
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

func readyLine(controlURL string) string {
	return `{"event":"ready","core_version":"0.1.0-dev","control_api_version":"v1","protocol_contract_version":"v1","inference_url":"http://127.0.0.1:8317","client_inference_url":"http://localhost:8317","control_url":"` + controlURL + `"}`
}

func alphaCapabilities() Capabilities {
	return contract.DefaultCapabilitiesResponse()
}

func versionOf(ready Ready) Version {
	return Version{
		CoreVersion:             ready.CoreVersion,
		ControlAPIVersion:       ready.ControlAPIVersion,
		ProtocolContractVersion: ready.ProtocolContractVersion,
		BuildCommit:             "unknown",
	}
}

func TestParsesTheFrozenReadyContract(t *testing.T) {
	ready, err := parseReady(readyLine("http://127.0.0.1:43210"))
	if err != nil {
		t.Fatal(err)
	}
	if ready.Event != "ready" || ready.ControlURL != "http://127.0.0.1:43210" ||
		ready.InferenceURL != "http://127.0.0.1:8317" || ready.ClientInferenceURL != "http://localhost:8317" {
		t.Fatalf("parsed %+v", ready)
	}
}

func TestReadyClientURLMustNameTheSameInferencePort(t *testing.T) {
	line := readyLine("http://127.0.0.1:43210")
	ipv4 := strings.Replace(line, `"client_inference_url":"http://localhost:8317"`, `"client_inference_url":"http://127.0.0.1:8317"`, 1)
	if ready, err := parseReady(ipv4); err != nil || ready.ClientInferenceURL != "http://127.0.0.1:8317" {
		t.Fatalf("an IPv4-only client URL should parse: %v", err)
	}
	for _, clientURL := range []string{"http://localhost:8318", "http://[::1]:8317", "http://localhost:8317/", "http://127.0.0.1:43210"} {
		if _, err := parseReady(strings.Replace(line, "http://localhost:8317", clientURL, 1)); err == nil {
			t.Errorf("%s should be rejected", clientURL)
		}
	}
	if _, err := parseReady(strings.Replace(line, `"client_inference_url":"http://localhost:8317",`, "", 1)); err == nil {
		t.Error("a missing client URL should be rejected")
	}
}

func TestReadyPortBoundaries(t *testing.T) {
	for _, url := range []string{"http://127.0.0.1:1", "http://127.0.0.1:65535"} {
		if ready, err := parseReady(readyLine(url)); err != nil || ready.ControlURL != url {
			t.Errorf("%s should be valid: %v", url, err)
		}
	}
	for _, url := range []string{
		"http://127.0.0.1:0", "http://127.0.0.1:01", "http://127.0.0.1:65536", "http://127.0.0.1:99999",
		"http://localhost:8080", "http://[::1]:8080", "http://127.0.0.2:8080", "http://user:secret@127.0.0.1:8080",
		"https://127.0.0.1:8080", "http://127.0.0.1:8080/", "http://127.0.0.1:8080/path",
		"http://127.0.0.1:8080?query=true", "http://127.0.0.1:8080#fragment",
	} {
		if _, err := parseReady(readyLine(url)); err == nil {
			t.Errorf("%s must be rejected", url)
		}
	}
	if _, err := parseReady(strings.Replace(readyLine("http://127.0.0.1:1"), `"event":"ready"`, `"event":"started"`, 1)); err == nil {
		t.Error("another event must be rejected")
	}
}

func TestVerifyContract(t *testing.T) {
	ready, err := parseReady(readyLine("http://127.0.0.1:43210"))
	if err != nil {
		t.Fatal(err)
	}
	version := "v0.1.1"
	tests := []struct {
		name      string
		mutate    func(*Ready, *Version, *Capabilities)
		wantError string
	}{
		{name: "the Alpha registry", mutate: func(*Ready, *Version, *Capabilities) {}},
		{
			name: "a version mismatch",
			mutate: func(_ *Ready, v *Version, c *Capabilities) {
				v.ProtocolContractVersion = "v2"
				c.Protocols, c.PlanTypes = nil, nil
			},
			wantError: "mismatch",
		},
		{
			name: "consistent but unsupported versions",
			mutate: func(r *Ready, v *Version, c *Capabilities) {
				r.ControlAPIVersion, r.ProtocolContractVersion = "v2", "v2"
				v.ControlAPIVersion, v.ProtocolContractVersion = "v2", "v2"
				c.ProtocolContractVersion = "v2"
			},
			wantError: "unsupported",
		},
		{
			name: "an available RelayKit runtime",
			mutate: func(_ *Ready, _ *Version, c *Capabilities) {
				c.ConversionEngine.Available = true
				c.ConversionEngine.Version = &version
				c.ConversionEngine.Edges = append(c.ConversionEngine.Edges, contract.ConversionEdge{
					From: "openai.chat", To: "openai.responses", Quality: contract.ConversionQualityGood, Streaming: true,
				})
			},
		},
		{
			name:      "an available RelayKit without a version",
			mutate:    func(_ *Ready, _ *Version, c *Capabilities) { c.ConversionEngine.Available = true },
			wantError: "non-empty version",
		},
		{
			name: "an unavailable RelayKit with a version",
			mutate: func(_ *Ready, _ *Version, c *Capabilities) {
				c.ConversionEngine.Version = &version
			},
			wantError: "must not advertise",
		},
		{
			name:      "a missing protocol",
			mutate:    func(_ *Ready, _ *Version, c *Capabilities) { c.Protocols = c.Protocols[1:] },
			wantError: "openai.responses",
		},
		{
			name:      "a changed plan",
			mutate:    func(_ *Ready, _ *Version, c *Capabilities) { c.PlanTypes[0].AvailableInAlpha = false },
			wantError: "native",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, v, c := ready, versionOf(ready), alphaCapabilities()
			tt.mutate(&r, &v, &c)
			err := verifyContract(r, v, c)
			if tt.wantError == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("got %v, want an error with %q", err, tt.wantError)
			}
		})
	}
}
