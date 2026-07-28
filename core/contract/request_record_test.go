package contract

import (
	"strings"
	"testing"
	"time"
)

func TestRequestRecordValidation(t *testing.T) {
	started := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	completed := started.Add(time.Second)
	model := "public-alias"
	status := 200
	latency := 12
	valid := RequestRecord{
		ID:             "request_01",
		StartedAt:      started,
		CompletedAt:    &completed,
		Status:         RequestStatusSucceeded,
		InputProtocol:  ProtocolOpenAIResponses,
		RequestedModel: &model,
		Streaming:      false,
		HTTPStatus:     &status,
		LatencyMs:      &latency,
		Audit:          NotCapturedAuditSummary(),
	}
	tests := []struct {
		name    string
		mutate  func(*RequestRecord)
		wantErr string
	}{
		{name: "accepts valid metadata record"},
		{
			name: "rejects invalid status",
			mutate: func(record *RequestRecord) {
				record.Status = "running"
			},
			wantErr: "unknown request status",
		},
		{
			name: "rejects oversized requested model",
			mutate: func(record *RequestRecord) {
				long := strings.Repeat("m", 257)
				record.RequestedModel = &long
			},
			wantErr: "requested_model",
		},
		{
			name: "rejects http status out of range",
			mutate: func(record *RequestRecord) {
				bad := 99
				record.HTTPStatus = &bad
			},
			wantErr: "http_status",
		},
		{
			name: "rejects negative latency",
			mutate: func(record *RequestRecord) {
				bad := -1
				record.LatencyMs = &bad
			},
			wantErr: "latency_ms",
		},
		{
			name: "rejects invalid error summary code",
			mutate: func(record *RequestRecord) {
				record.Error = &ErrorSummary{
					Category: "gateway", Code: "Bad-Code", Message: "x",
				}
			},
			wantErr: "error code",
		},
		{
			name: "accepts null optional attribution fields",
			mutate: func(record *RequestRecord) {
				record.RouteID = nil
				record.ServiceID = nil
				record.LocalAccessTokenID = nil
				record.Plan = nil
				record.Usage = nil
				record.Error = nil
				record.RequestedModel = nil
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			record := valid
			if test.mutate != nil {
				test.mutate(&record)
			}
			err := record.Validate()
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("Validate() error = %v, want %q", err, test.wantErr)
			}
		})
	}
}

func TestPurgeRequestValidation(t *testing.T) {
	before := time.Date(2026, 7, 25, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		request PurgeRequest
		wantErr string
	}{
		{
			name:    "all requires confirm and omits before",
			request: PurgeRequest{Scope: PurgeScopeAll, Confirm: true},
		},
		{
			name:    "before requires timestamp",
			request: PurgeRequest{Scope: PurgeScopeBefore, Confirm: true, Before: &before},
		},
		{
			name:    "rejects confirm false",
			request: PurgeRequest{Scope: PurgeScopeAll, Confirm: false},
			wantErr: "confirm",
		},
		{
			name:    "rejects before scope without timestamp",
			request: PurgeRequest{Scope: PurgeScopeBefore, Confirm: true},
			wantErr: "before is required",
		},
		{
			name:    "rejects all scope with before",
			request: PurgeRequest{Scope: PurgeScopeAll, Confirm: true, Before: &before},
			wantErr: "before must be omitted",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.request.Validate()
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("Validate() error = %v, want %q", err, test.wantErr)
			}
		})
	}
}

func TestUsageValidation(t *testing.T) {
	if err := (Usage{InputTokens: 1, OutputTokens: 2, TotalTokens: 3}).Validate(); err != nil {
		t.Fatal(err)
	}
	negative := -1
	if err := (Usage{CachedInputTokens: &negative}).Validate(); err == nil {
		t.Fatal("expected negative cached token rejection")
	}
}
