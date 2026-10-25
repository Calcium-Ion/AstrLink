package autoclassifier

import "github.com/QuantumNous/astrlink/core/contract"

// FallbackReason values classifier sources add beyond the constants in
// client.go. They live here, in the parent package, so every source and both
// consumers (the inference path and the control-plane dry-run) share one
// reason domain instead of re-declaring it per adapter.
const (
	// FallbackRefusal marks a provider that answered with a refusal instead
	// of a classification.
	FallbackRefusal = "classifier_refusal"
	// FallbackInvalidOutput marks an answer that failed strict validation.
	FallbackInvalidOutput = "classifier_invalid_output"
)

// FailureResult is the mapping of one failed classification onto the record
// domain. Code always carries the diagnostic reason as a stable record code
// (classifier_timeout, ...), never the raw provider reason string.
//
// There is deliberately no configured behaviour here. A classification that
// produced no usable label leaves its fact unknown, so every rule testing it
// skips and the group's default member serves the request; the code below is
// what the request history shows for why. Fail-open is not negotiable:
// nothing here may block the caller's request.
type FailureResult struct {
	Code         string
	DurationMS   int
	ClassifierID contract.AutoClassifierID
}

// FailureTaskType maps a failed classification onto its record code. It is the
// one implementation shared by the inference path and the control-plane
// dry-run, so both report the same code for the same failure.
func FailureTaskType(outcome Outcome, durationMS int) FailureResult {
	var code string
	switch outcome.FallbackReason {
	case FallbackBusy:
		code = "classifier_busy"
	case FallbackTimeout:
		code = "classifier_timeout"
	case FallbackRefusal:
		code = "classifier_refusal"
	case FallbackInvalidOutput:
		code = "classifier_invalid_output"
	default:
		code = FallbackUnavailable
	}
	return FailureResult{Code: code, DurationMS: durationMS, ClassifierID: outcome.ClassifierID}
}
