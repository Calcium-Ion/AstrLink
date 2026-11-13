package core

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
)

// Ready is the line Core prints on stdout once both listeners are bound.
type Ready = contract.ReadyEvent

// parseReady reads Core's ready line, rejecting one this desktop cannot
// talk to.
func parseReady(line string) (Ready, error) {
	var ready Ready
	decoder := json.NewDecoder(strings.NewReader(line))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&ready); err != nil {
		return Ready{}, fmt.Errorf("invalid astrlink-core ready announcement: %w", err)
	}
	if err := ready.Validate(); err != nil {
		return Ready{}, fmt.Errorf("invalid astrlink-core ready announcement: %w", err)
	}
	return ready, nil
}
