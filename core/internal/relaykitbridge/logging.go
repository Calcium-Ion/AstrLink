package relaykitbridge

import (
	"log"
	"regexp"
	"sync"

	"github.com/QuantumNous/new-api/relaykit/relayconvert"
	kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
)

var (
	hostHooksOnce     = new(sync.Once)
	sensitiveLogValue = regexp.MustCompile(`(?i)("?(?:body|media_url|url|arguments|tool_arguments)"?\s*[:=]\s*)(?:"[^"]*"|\S+)`)
)

// InstallHostHooks configures process-wide RelayKit hooks once. RelayKit does
// not retain request bodies itself; redaction here also protects converter
// diagnostics generated from malformed tool calls or media references.
func InstallHostHooks() {
	hostHooksOnce.Do(func() {
		redacted := func(message string) {
			log.Printf("relaykit: %s", redactRelayKitLog(message))
		}
		relayconvert.SetMediaResolver(secureMediaResolver())
		kitutil.SetLogging(redacted, redacted)
		kitutil.SetSystemErrorLogging(redacted)
	})
}

func redactRelayKitLog(message string) string {
	message = sensitiveLogValue.ReplaceAllString(message, "$1***")
	return kitutil.MaskSensitiveInfo(message)
}
