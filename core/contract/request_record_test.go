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
		PrivacyRestore: &PrivacyRestoreSummary{
			Enabled: true, MappingCount: 1, RestoredCount: 2,
		},
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
			name: "rejects negative privacy restore count",
			mutate: func(record *RequestRecord) {
				record.PrivacyRestore = &PrivacyRestoreSummary{FallbackCount: -1}
			},
			wantErr: "privacy restore counts",
		},
		{
			name: "accepts request-time privacy hit counts",
			mutate: func(record *RequestRecord) {
				record.PrivacyRestore = &PrivacyRestoreSummary{
					Enabled: true, MappingCount: 3, RestoredCount: 3,
					Hits: []PrivacyHitCount{
						{Kind: CanonicalKindEmail, Count: 2},
						{Kind: CanonicalKindURL, Count: 1},
					},
				}
			},
		},
		{
			name: "rejects invalid privacy hit kind",
			mutate: func(record *RequestRecord) {
				record.PrivacyRestore = &PrivacyRestoreSummary{
					Hits: []PrivacyHitCount{{Kind: "ssn", Count: 1}},
				}
			},
			wantErr: "privacy restore hit kind",
		},
		{
			name: "rejects duplicate privacy hit kinds",
			mutate: func(record *RequestRecord) {
				record.PrivacyRestore = &PrivacyRestoreSummary{
					Hits: []PrivacyHitCount{
						{Kind: CanonicalKindEmail, Count: 1},
						{Kind: CanonicalKindEmail, Count: 2},
					},
				}
			},
			wantErr: "privacy restore hit kinds must be unique",
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
		{
			name: "accepts session trajectory fields",
			mutate: func(record *RequestRecord) {
				sessionID := SessionID("session_01")
				previous := "resp_prev"
				output := "resp_out"
				preview := "创建启动快捷方式"
				ended := started.Add(time.Millisecond)
				record.SessionID = &sessionID
				record.PreviousResponseID = &previous
				record.OutputResponseID = &output
				record.InputPreview = &preview
				record.Events = []RequestEvent{{
					Kind: RequestEventAccepted, StartedAt: started, EndedAt: &ended,
					Status: RequestStatusPending, Summary: "gpt-4.1 · openai.responses",
				}}
			},
		},
		{
			name: "rejects invalid session id",
			mutate: func(record *RequestRecord) {
				sessionID := SessionID("SESSION")
				record.SessionID = &sessionID
			},
			wantErr: "session_id",
		},
		{
			name: "rejects oversized input preview",
			mutate: func(record *RequestRecord) {
				preview := strings.Repeat("字", MaxInputPreviewRunes+1)
				record.InputPreview = &preview
			},
			wantErr: "input_preview",
		},
		{
			name: "rejects unknown event kind",
			mutate: func(record *RequestRecord) {
				record.Events = []RequestEvent{{
					Kind: "tool", StartedAt: started, Status: RequestStatusSucceeded,
				}}
			},
			wantErr: "event kind",
		},
		{
			name: "accepts conversation cursor fields",
			mutate: func(record *RequestRecord) {
				turn, users, fingerprint := 2, 3, "fp1_0123456789abcdef0123456789abcdef"
				record.TurnIndex = &turn
				record.TurnUserMessages = &users
				record.TurnUserFingerprint = &fingerprint
				record.SessionLink = &SessionLink{Kind: SessionCursorEchoID, Value: "call_7f3a9c2e1b4d4e8fa1c2"}
				record.Cursors = []SessionCursor{
					{Kind: SessionCursorExplicit, Direction: SessionCursorIn, Value: "conv_1"},
					{Kind: SessionCursorEchoID, Direction: SessionCursorOut, Value: "call_9a8b7c6d5e4f3a2b1c0d"},
					{Kind: SessionCursorFingerprint, Direction: SessionCursorOut, Value: "fp1_0123456789abcdef0123456789abcdef"},
				}
			},
		},
		{
			name: "rejects zero turn index",
			mutate: func(record *RequestRecord) {
				turn := 0
				record.TurnIndex = &turn
			},
			wantErr: "turn_index",
		},
		{
			name: "rejects turn state without a turn index",
			mutate: func(record *RequestRecord) {
				users := 1
				record.TurnUserMessages = &users
			},
			wantErr: "turn_user_messages requires turn_index",
		},
		{
			name: "rejects negative turn user messages",
			mutate: func(record *RequestRecord) {
				turn, users := 1, -1
				record.TurnIndex = &turn
				record.TurnUserMessages = &users
			},
			wantErr: "turn_user_messages",
		},
		{
			name: "rejects empty turn user fingerprint",
			mutate: func(record *RequestRecord) {
				turn, fingerprint := 1, ""
				record.TurnIndex = &turn
				record.TurnUserFingerprint = &fingerprint
			},
			wantErr: "turn_user_fingerprint",
		},
		{
			name: "rejects unknown session link kind",
			mutate: func(record *RequestRecord) {
				record.SessionLink = &SessionLink{Kind: "guess", Value: "x"}
			},
			wantErr: "session_link",
		},
		{
			name: "rejects cursor with control characters",
			mutate: func(record *RequestRecord) {
				record.Cursors = []SessionCursor{{Kind: SessionCursorEchoID, Direction: SessionCursorOut, Value: "bad\nvalue"}}
			},
			wantErr: "cursors[0]",
		},
		{
			name: "rejects cursor with unknown direction",
			mutate: func(record *RequestRecord) {
				record.Cursors = []SessionCursor{{Kind: SessionCursorEchoID, Direction: "sideways", Value: "call_1"}}
			},
			wantErr: "direction",
		},
		{
			name: "rejects too many cursors",
			mutate: func(record *RequestRecord) {
				record.Cursors = make([]SessionCursor, MaxSessionCursors+1)
				for index := range record.Cursors {
					record.Cursors[index] = SessionCursor{Kind: SessionCursorEchoID, Direction: SessionCursorOut, Value: "call_x"}
				}
			},
			wantErr: "at most",
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

func TestRequestRecordEffectiveStatusTreatsHTTPErrorAsFailed(t *testing.T) {
	ok := 200
	bad := 502
	forbidden := 403
	tests := []struct {
		name   string
		status RequestStatus
		http   *int
		want   RequestStatus
	}{
		{name: "success 200", status: RequestStatusSucceeded, http: &ok, want: RequestStatusSucceeded},
		{name: "legacy success 502", status: RequestStatusSucceeded, http: &bad, want: RequestStatusFailed},
		{name: "legacy success 403", status: RequestStatusSucceeded, http: &forbidden, want: RequestStatusFailed},
		{name: "already failed", status: RequestStatusFailed, http: &bad, want: RequestStatusFailed},
		{name: "blocked 403", status: RequestStatusBlocked, http: &forbidden, want: RequestStatusBlocked},
		{name: "success without http", status: RequestStatusSucceeded, want: RequestStatusSucceeded},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			record := RequestRecord{Status: test.status, HTTPStatus: test.http}
			if got := record.EffectiveStatus(); got != test.want {
				t.Fatalf("EffectiveStatus()=%q want %q", got, test.want)
			}
		})
	}
}

func TestUsageValidation(t *testing.T) {
	if err := (Usage{InputTokens: 1, OutputTokens: 2, TotalTokens: 3}).Validate(); err != nil {
		t.Fatal(err)
	}
	negative := -1
	if err := (Usage{CacheReadTokens: &negative}).Validate(); err == nil {
		t.Fatal("expected negative cache_read_tokens rejection")
	}
	if err := (Usage{CacheWriteTokens: &negative}).Validate(); err == nil {
		t.Fatal("expected negative cache_write_tokens rejection")
	}
}
