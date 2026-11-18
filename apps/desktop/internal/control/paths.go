package control

import (
	"net/http"
	"strings"
	"time"
)

// Control API paths the timeout and retry rules single out.
const (
	ServicesPath                 = "/control/v1/services"
	ServiceModelProbesPath       = "/control/v1/service-model-probes"
	ServiceProxyProbesPath       = "/control/v1/service-proxy-probes"
	PrivacyModelCatalogPath      = "/control/v1/privacy-model-catalog"
	PrivacyModelReleasesPath     = "/control/v1/privacy-model-catalog/releases"
	PrivacyModelsPath            = "/control/v1/privacy-models"
	PrivacyModelProbePath        = "/control/v1/privacy-models/probe"
	LocalPrivacyModelProbePath   = "/control/v1/privacy-models/local/probe"
	PrivacyRegexBuiltinRulesPath = "/control/v1/privacy/regex-builtin-rules"
	PolicyDryRunPath             = "/control/v1/policies/policy_privacy_default/dry-run"
	RequestPurgePath             = "/control/v1/requests/purge"

	RawAccessPath    = "/control/v1/audit/raw-access"
	RawSealingPath   = "/control/v1/audit/raw-sealing"
	RawPasswordPath  = "/control/v1/audit/raw-password"
	RawUnlockPath    = "/control/v1/audit/raw-unlock"
	RawLockPath      = "/control/v1/audit/raw-lock"
	RawVerifyPath    = "/control/v1/audit/raw-verify"
	RawBiometricPath = "/control/v1/audit/raw-biometric"
)

const (
	RequestTimeout = 2 * time.Second
	// RequestListTimeout covers record, session and project memory reads.
	RequestListTimeout       = 8 * time.Second
	SubscriptionUsageTimeout = 20 * time.Second
	ServiceModelProbeTimeout = 65 * time.Second
	// PrivacyMutationTimeout covers policy changes, which may synchronously
	// stop a worker for up to five seconds, and model deletion, which also
	// waits for download cancellation and filesystem cleanup.
	PrivacyMutationTimeout = 30 * time.Second
	// PrivacyModelMetadataTimeout covers probe and install, which may pin a
	// large tokenizer and configuration set over a slow connection. The
	// installation call returns before model weights download.
	PrivacyModelMetadataTimeout = 10 * time.Minute
	AuditContentTimeout         = 60 * time.Second
	// RequestPurgeTimeout bounds how long the records page waits for a
	// purge: one transaction deletes the whole selection, and 300,000
	// records with their captured content take about a minute. Core
	// finishes a purge it started even after this wait ends.
	RequestPurgeTimeout = 10 * time.Minute
	// ProofTimeout covers raw proof checks, which run a memory-hard KDF in
	// Core; a slow machine needs longer than an ordinary control call.
	ProofTimeout = 20 * time.Second

	// MaxControlBody bounds an ordinary response. A valid 100-installation
	// model directory can exceed 2 MiB when every installation carries the
	// maximum 256-entry label mapping.
	MaxControlBody = 8 << 20
	// MaxSessionDetailBody bounds one conversation's detail, which carries
	// every call: an agent that ran all day made 1,500 calls and about
	// 10 MiB of them.
	MaxSessionDetailBody = 64 << 20
	// MaxAuditContentBody bounds decrypted audit content, which may reach
	// response_content_max_bytes (64 MiB).
	MaxAuditContentBody = 96 << 20
	maxErrorBody        = 512
)

// RequestPurgeStillRunning is returned when a purge outlasts
// RequestPurgeTimeout. Core is still deleting; the records page matches this
// code to say so.
const RequestPurgeStillRunning = "request_purge_still_running"

// pathOnly drops the query string.
func pathOnly(path string) string {
	if head, _, ok := strings.Cut(path, "?"); ok {
		return head
	}
	return path
}

// IsProofRequest reports a call that carries a raw password or Touch ID
// proof. Each proof is forwarded once and never retried.
func IsProofRequest(method, path string) bool {
	if method != http.MethodPost {
		return false
	}
	switch path {
	case RawPasswordPath, RawUnlockPath, RawVerifyPath, RawBiometricPath:
		return true
	}
	rest, ok := strings.CutPrefix(path, RawAccessPath+"/")
	return ok && strings.HasSuffix(rest, "/decision")
}

func isSubscriptionUsagePath(path string) bool {
	return strings.HasPrefix(path, ServicesPath+"/") &&
		(strings.HasSuffix(path, "/usage") || strings.HasSuffix(path, "/usage/reset"))
}

// isRequestAuditReadPath reports the reads that decrypt a call's captured
// body: its audit content and the texts it carried.
func isRequestAuditReadPath(path string) bool {
	return strings.HasPrefix(path, "/control/v1/requests/") &&
		(strings.HasSuffix(path, "/audit") || strings.HasSuffix(path, "/audit/context"))
}

func isRequestRecordListPath(path string) bool {
	if isRequestAuditReadPath(path) {
		return false
	}
	return path == "/control/v1/requests" ||
		strings.HasPrefix(path, "/control/v1/requests/") ||
		path == "/control/v1/request-sessions" ||
		strings.HasPrefix(path, "/control/v1/request-sessions/") ||
		path == "/control/v1/request-memory" ||
		path == "/control/v1/request-memory/entry"
}

// isRequestPurge reports a purge, which must not be sent twice: Core keeps
// deleting after the desktop stops waiting, and a second request would only
// queue behind the first.
func isRequestPurge(method, path string) bool {
	return method == http.MethodPost && pathOnly(path) == RequestPurgePath
}

func isBuiltinToolTest(method, path string) bool {
	if method != http.MethodPost {
		return false
	}
	switch pathOnly(path) {
	case "/control/v1/builtin-tools/web_search/test", "/control/v1/builtin-tools/image_generation/test":
		return true
	}
	return false
}

// retries reports whether a transport failure of this call is sent again.
func retries(method, path string) bool {
	return !isBuiltinToolTest(method, path) && !isRequestPurge(method, path) && !IsProofRequest(method, path)
}

// Timeout is how long one call to path may take, its body included.
func Timeout(method, path string) time.Duration {
	path = pathOnly(path)
	post := method == http.MethodPost
	switch {
	case post && path == RawPasswordPath:
		return RequestPurgeTimeout
	case IsProofRequest(method, path):
		return ProofTimeout
	case isBuiltinToolTest(method, path):
		if strings.Contains(path, "/image_generation/") {
			return 195 * time.Second
		}
		return 45 * time.Second
	case post && strings.HasPrefix(path, ServicesPath+"/") && strings.HasSuffix(path, "/test"):
		return 75 * time.Second
	case post && strings.HasPrefix(path, "/control/v1/pricing/"):
		return 150 * time.Second
	// A sync run does several remote round trips and may retry the merge.
	case post && path == "/control/v1/sync/run":
		return 320 * time.Second
	case post && (path == ServiceModelProbesPath || path == ServiceProxyProbesPath ||
		(strings.HasPrefix(path, ServicesPath+"/") && strings.HasSuffix(path, "/probe-models"))):
		return ServiceModelProbeTimeout
	case post && (path == PrivacyModelProbePath || path == LocalPrivacyModelProbePath || path == PrivacyModelsPath ||
		(strings.HasPrefix(path, PrivacyModelsPath+"/") && strings.HasSuffix(path, "/resume"))),
		method == http.MethodGet && path == PrivacyModelReleasesPath:
		return PrivacyModelMetadataTimeout
	case post && path == PolicyDryRunPath:
		return PrivacyMutationTimeout
	case method == http.MethodPatch && path == "/control/v1/policies/policy_privacy_default",
		method == http.MethodDelete && strings.HasPrefix(path, "/control/v1/privacy-models/"),
		post && strings.HasPrefix(path, PrivacyModelsPath+"/") && strings.HasSuffix(path, "/pause"):
		return PrivacyMutationTimeout
	case method == http.MethodGet && isRequestAuditReadPath(path):
		return AuditContentTimeout
	case (method == http.MethodGet || post) && isSubscriptionUsagePath(path):
		return SubscriptionUsageTimeout
	case method == http.MethodGet && isRequestRecordListPath(path):
		return RequestListTimeout
	case isRequestPurge(method, path):
		return RequestPurgeTimeout
	}
	return RequestTimeout
}

// BodyLimit is the largest response body accepted from path.
func BodyLimit(path string) int64 {
	if isRequestAuditReadPath(path) {
		return MaxAuditContentBody
	}
	if isRequestSessionDetailPath(path) {
		return MaxSessionDetailBody
	}
	return MaxControlBody
}

// isRequestSessionDetailPath reports a read of one conversation.
func isRequestSessionDetailPath(path string) bool {
	id, ok := strings.CutPrefix(path, "/control/v1/request-sessions/")
	return ok && id != "" && !strings.Contains(id, "/")
}
