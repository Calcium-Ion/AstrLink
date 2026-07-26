package contract

import "testing"

func TestDefaultAuditSettingsAreValid(t *testing.T) {
	settings := DefaultAuditSettings()
	if settings.RequestBodyEnabled || settings.ResponseContentEnabled {
		t.Fatalf("defaults must disable capture: %#v", settings)
	}
	if err := settings.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestAuditSettingsValidation(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*AuditSettings)
		wantErr bool
	}{
		{name: "valid defaults", mutate: func(*AuditSettings) {}, wantErr: false},
		{
			name: "request body max too small",
			mutate: func(settings *AuditSettings) {
				settings.RequestBodyMaxBytes = 1023
			},
			wantErr: true,
		},
		{
			name: "response content max too large",
			mutate: func(settings *AuditSettings) {
				settings.ResponseContentMaxBytes = MaxResponseContentMaxBytes + 1
			},
			wantErr: true,
		},
		{
			name: "content retention too large",
			mutate: func(settings *AuditSettings) {
				settings.ContentRetentionDays = MaxContentRetentionDays + 1
			},
			wantErr: true,
		},
		{
			name: "invalid extension name",
			mutate: func(settings *AuditSettings) {
				settings.Extensions = map[string]any{"bad": true}
			},
			wantErr: true,
		},
		{
			name: "valid extension",
			mutate: func(settings *AuditSettings) {
				settings.Extensions = map[string]any{"x-note": "ok"}
			},
			wantErr: false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			settings := DefaultAuditSettings()
			test.mutate(&settings)
			err := settings.Validate()
			if test.wantErr && err == nil {
				t.Fatal("expected error")
			}
			if !test.wantErr && err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAuditSettingsPatchAckAndRanges(t *testing.T) {
	trueValue := true
	falseValue := false
	tooSmall := 10
	patch := AuditSettingsPatch{}
	if err := patch.Validate(); err == nil {
		t.Fatal("empty patch must fail")
	}
	patch = AuditSettingsPatch{RequestBodyEnabled: &trueValue}
	if err := patch.Validate(); err != nil {
		t.Fatal(err)
	}
	if !patch.EnablesCapture() || patch.Acknowledged() {
		t.Fatalf("enable flags = enables=%t ack=%t", patch.EnablesCapture(), patch.Acknowledged())
	}
	patch.AuditRiskAcknowledged = &trueValue
	if !patch.Acknowledged() {
		t.Fatal("expected acknowledged")
	}
	patch = AuditSettingsPatch{ResponseContentEnabled: &falseValue}
	if patch.EnablesCapture() {
		t.Fatal("disable must not count as enable")
	}
	patch = AuditSettingsPatch{RequestBodyMaxBytes: &tooSmall}
	if err := patch.Validate(); err == nil {
		t.Fatal("expected range error")
	}
}

func TestAuditContentPartValidation(t *testing.T) {
	part := AuditContentPart{MediaType: "application/json", Content: "{}", CapturedBytes: 2}
	if err := part.Validate(); err != nil {
		t.Fatal(err)
	}
	part.MediaType = ""
	if err := part.Validate(); err == nil {
		t.Fatal("expected media_type error")
	}
}
