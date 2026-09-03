package ingress

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/internal/transport"
)

func TestOperatorTransportMessageKeepsHostAndRedactsSecrets(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		fallback  string
		want      string
		wantParts []string
		hide      []string
	}{
		{
			name:     "plain dial",
			err:      errors.New("dial detail"),
			fallback: upstreamUnavailableFallback,
			want:     "dial detail",
		},
		{
			name:     "unwraps upstream wrapper",
			err:      transport.NewUpstreamError(errors.New("dial detail")),
			fallback: upstreamUnavailableFallback,
			want:     "dial detail",
		},
		{
			name:     "wrapper only uses fallback",
			err:      &transport.UpstreamError{},
			fallback: upstreamUnavailableFallback,
			want:     upstreamUnavailableFallback,
		},
		{
			name:     "timeout inner text",
			err:      context.DeadlineExceeded,
			fallback: upstreamTimeoutFallback,
			want:     "context deadline exceeded",
		},
		{
			name:     "empty error uses fallback",
			err:      nil,
			fallback: upstreamUnavailableFallback,
			want:     upstreamUnavailableFallback,
		},
		{
			name: "quoted url with key and host",
			err: errors.New(
				`Get "https://host/v1?key=sk-secret": dial tcp 10.0.0.1:443: connection reset`,
			),
			fallback: upstreamUnavailableFallback,
			wantParts: []string{
				"https://host/v1",
				"10.0.0.1:443",
				"connection reset",
				"key=<redacted>",
			},
			hide: []string{"sk-secret"},
		},
		{
			name:     "userinfo is stripped",
			err:      errors.New(`Get "https://alice:s3cretpass@api.example:443/v1": EOF`),
			fallback: upstreamUnavailableFallback,
			wantParts: []string{
				"https://api.example",
				"EOF",
			},
			hide: []string{"alice", "s3cretpass"},
		},
		{
			name:     "bearer token outside url",
			err:      errors.New("upstream rejected bearer abcdefghijklmnop"),
			fallback: upstreamUnavailableFallback,
			want:     "upstream rejected …",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := operatorTransportMessage(test.err, test.fallback)
			if test.want != "" && got != test.want {
				t.Fatalf("message = %q, want %q", got, test.want)
			}
			for _, part := range test.wantParts {
				if !strings.Contains(got, part) {
					t.Fatalf("message %q missing %q", got, part)
				}
			}
			for _, hidden := range test.hide {
				if strings.Contains(got, hidden) {
					t.Fatalf("message %q still contains %q", got, hidden)
				}
			}
		})
	}
}

func TestInterruptedStreamMessageAppendsCause(t *testing.T) {
	got := interruptedStreamMessage(transport.NewResponseError(errors.New("read: connection reset by peer")))
	if !strings.HasPrefix(got, streamInterruptedFallback+": ") ||
		!strings.Contains(got, "connection reset by peer") {
		t.Fatalf("interrupted = %q", got)
	}
	if got := interruptedStreamMessage(&transport.ResponseError{}); got != streamInterruptedFallback {
		t.Fatalf("wrapper-only interrupted = %q", got)
	}
}

func TestSanitizeSummaryKeepsTransportURL(t *testing.T) {
	raw := `upstream_unavailable · Get "https://host/v1?key=sk-abcdefghijklmnopqrstuvwxyz": dial tcp 10.0.0.1:443: EOF`
	got := sanitizeSummary(raw)
	if !strings.Contains(got, "https://host/v1") || !strings.Contains(got, "10.0.0.1:443") {
		t.Fatalf("summary dropped host: %q", got)
	}
	if strings.Contains(got, "sk-abcdefghijklmnopqrstuvwxyz") {
		t.Fatalf("summary kept secret: %q", got)
	}
	if strings.Contains(sanitizePreview("see https://example.com/path with sk-abcdefghijklmnopqrstuvwxyz"), "https://") {
		t.Fatal("preview must still strip URLs")
	}
}
