package ingress

import (
	"errors"
	"net/url"
	"regexp"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

const (
	upstreamUnavailableFallback = "upstream request could not be completed"
	upstreamTimeoutFallback     = "upstream request timed out before its response started"
	streamInterruptedFallback   = "upstream failed after the response started; the client response is incomplete"
	discoveryFailedFallback     = "model discovery failed on every capable endpoint"
	discoveryTimeoutFallback    = "model discovery timed out on every capable endpoint"
)

var (
	embeddedURLPattern = regexp.MustCompile(`https?://[^\s"']+`)

	transportWrapperMessages = map[string]struct{}{
		"upstream request failed before the response started": {},
		"upstream response was interrupted after it started":  {},
		"invalid upstream target":                             {},
	}
)

func operatorTransportMessage(err error, fallback string) string {
	text := transportCauseText(err)
	if text == "" {
		text = fallback
	}
	return clampErrorMessage(redactTransportSecrets(text))
}

func interruptedStreamMessage(err error) string {
	cause := transportCauseText(err)
	if cause == "" {
		return streamInterruptedFallback
	}
	return clampErrorMessage(streamInterruptedFallback + ": " + redactTransportSecrets(cause))
}

func discoveryAggregateMessage(results []discoveryResult, allTimedOut bool) string {
	base := discoveryFailedFallback
	if allTimedOut {
		base = discoveryTimeoutFallback
	}
	seen := make(map[string]struct{})
	parts := make([]string, 0)
	for _, result := range results {
		if result.outcome != discoveryOutcomeFailed {
			continue
		}
		cause := transportCauseText(result.failure.err)
		if cause == "" {
			continue
		}
		cause = strings.Join(strings.Fields(redactTransportSecrets(cause)), " ")
		if cause == "" {
			continue
		}
		if _, exists := seen[cause]; exists {
			continue
		}
		seen[cause] = struct{}{}
		parts = append(parts, cause)
	}
	if len(parts) == 0 {
		return base
	}
	return clampErrorMessage(base + ": " + strings.Join(parts, "; "))
}

func transportCauseText(err error) string {
	cause := unwrapTransportCause(err)
	if cause == nil {
		return ""
	}
	text := strings.TrimSpace(cause.Error())
	if text == "" {
		return ""
	}
	if _, wrapped := transportWrapperMessages[text]; wrapped {
		return ""
	}
	return text
}

func unwrapTransportCause(err error) error {
	for err != nil {
		var (
			upstream *transport.UpstreamError
			response *transport.ResponseError
			target   *transport.TargetError
		)
		switch {
		case errors.As(err, &upstream) && upstream.Unwrap() != nil:
			err = upstream.Unwrap()
		case errors.As(err, &response) && response.Unwrap() != nil:
			err = response.Unwrap()
		case errors.As(err, &target) && target.Unwrap() != nil:
			err = target.Unwrap()
		default:
			return err
		}
	}
	return err
}

func redactTransportSecrets(raw string) string {
	if raw == "" {
		return ""
	}
	cleaned := embeddedURLPattern.ReplaceAllStringFunc(raw, redactEmbeddedURL)
	cleaned = previewSecretPattern.ReplaceAllString(cleaned, "…")
	cleaned = placeholderPattern.ReplaceAllString(cleaned, "…")
	return strings.TrimSpace(strings.Join(strings.Fields(cleaned), " "))
}

func redactEmbeddedURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return raw
	}
	return redactURL(parsed)
}

func clampErrorMessage(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return contract.ClampRunes(value, 1024)
}
