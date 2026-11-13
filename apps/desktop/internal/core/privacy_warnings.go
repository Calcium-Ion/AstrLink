package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/QuantumNous/astrlink/desktop/internal/control"
	"github.com/QuantumNous/astrlink/desktop/internal/prefs"
)

// Native notifications for requests the privacy policy's warn action let
// through. Core keeps the recent ones in an in-memory feed and marks those
// that carried a value it had not warned about before, so a secret resent
// with every call of a conversation notifies once. The observer loop polls
// the feed and raises at most one notification per poll.

// PrivacyWarningsPath is Core's feed of recent privacy warnings.
const PrivacyWarningsPath = "/control/v1/privacy/warnings"

// warningKindOrder is the order the desktop lists kinds in. Must match
// PRIVACY_KIND_ORDER in the frontend.
var warningKindOrder = []string{
	"email",
	"phone",
	"account",
	"payment_card",
	"ip_address",
	"url",
	"common_secret",
	"private_address",
	"private_date",
	"private_person",
}

// unknownWarningKind stands in for a kind from a newer Core.
const unknownWarningKind = "unknown"

// warningPage is the part of Core's feed after the sequence the desktop
// asked for.
type warningPage struct {
	Epoch  string `json:"epoch"`
	Events []struct {
		Sequence uint64 `json:"sequence"`
		Kinds    []struct {
			Kind string `json:"kind"`
		} `json:"kinds"`
		Notifiable bool `json:"notifiable"`
	} `json:"events"`
}

func parseWarningPage(body []byte) (warningPage, error) {
	var page warningPage
	if err := json.Unmarshal(body, &page); err != nil {
		return warningPage{}, fmt.Errorf("privacy warnings returned invalid JSON: %v", err)
	}
	return page, nil
}

// WarningCursor is how far the desktop has read Core's feed. The epoch
// names one Core process, whose sequence starts over when it restarts.
type WarningCursor struct {
	epoch    *string
	sequence uint64
}

// warningStep is what one read of the feed decided: restart (Core
// restarted and its sequence began again; read it from the start), or move
// to cursor and notify about kinds, if any.
type warningStep struct {
	restart bool
	cursor  WarningCursor
	kinds   []string
}

// stepWarnings folds every warning that carried a new value into one
// notification's kinds.
func stepWarnings(cursor WarningCursor, page warningPage) warningStep {
	if cursor.epoch != nil && *cursor.epoch != page.Epoch {
		return warningStep{restart: true}
	}
	epoch := page.Epoch
	next := WarningCursor{epoch: &epoch, sequence: cursor.sequence}
	var found []string
	for _, event := range page.Events {
		if event.Sequence <= cursor.sequence {
			continue
		}
		next.sequence = max(next.sequence, event.Sequence)
		if event.Notifiable {
			for _, kind := range event.Kinds {
				found = append(found, kind.Kind)
			}
		}
	}
	return warningStep{cursor: next, kinds: orderedWarningKinds(found)}
}

// orderedWarningKinds is the known kinds in display order, then one
// stand-in for any kind this build does not know.
func orderedWarningKinds(found []string) []string {
	kinds := []string{}
	for _, kind := range warningKindOrder {
		if slices.Contains(found, kind) {
			kinds = append(kinds, kind)
		}
	}
	if slices.ContainsFunc(found, func(kind string) bool { return !slices.Contains(warningKindOrder, kind) }) {
		kinds = append(kinds, unknownWarningKind)
	}
	return kinds
}

// warningNotificationText names kinds only; a value never reaches the
// desktop.
func warningNotificationText(locale prefs.Locale, kinds []string) (title, body string) {
	labels := make([]string, len(kinds))
	for i, kind := range kinds {
		labels[i] = prefs.T(locale, "privacy."+kind)
	}
	separator := prefs.T(locale, "host.privacyWarning.kindSeparator")
	title = prefs.T(locale, "host.privacyWarning.title", "kinds", strings.Join(labels, separator))
	return title, prefs.T(locale, "host.privacyWarning.body")
}

// PollPrivacyWarnings reads what Core warned about since the last poll and
// notifies once for the warnings that carried a new value. A failed read
// keeps the cursor: Core may be restarting or predate the feed, and the
// next poll tries again.
func PollPrivacyWarnings(ctx context.Context, caller control.Caller, cursor *WarningCursor, locale prefs.Locale, notify func(title, body string)) {
	// A restarted Core takes a second read, from the start of its feed.
	for range 2 {
		response, err := control.Send(ctx, caller, control.Request{
			Method: http.MethodGet,
			Path:   PrivacyWarningsPath + "?since=" + strconv.FormatUint(cursor.sequence, 10),
		})
		if err != nil {
			return
		}
		page, err := parseWarningPage(response.Body)
		if err != nil {
			return
		}
		step := stepWarnings(*cursor, page)
		if step.restart {
			*cursor = WarningCursor{}
			continue
		}
		*cursor = step.cursor
		if len(step.kinds) > 0 {
			notify(warningNotificationText(locale, step.kinds))
		}
		return
	}
}
