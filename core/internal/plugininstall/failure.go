package plugininstall

import (
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
)

// Failure is why an install cannot go on, for the review to show instead.
type Failure struct {
	contract.PluginInstallFailure
}

func (failure *Failure) Error() string {
	text := failure.Reason
	if failure.Detail != "" {
		text += ": " + failure.Detail
	}
	if len(failure.Messages) > 0 {
		text += "\n" + strings.Join(failure.Messages, "\n")
	}
	return text
}

func fail(reason, detail string) *Failure {
	return &Failure{contract.PluginInstallFailure{Reason: reason, Detail: detail}}
}
