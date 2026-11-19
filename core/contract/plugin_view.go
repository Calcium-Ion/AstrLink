package contract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"unicode/utf8"
)

// Limits of what a plugin shows through the host. The host renders every
// view with its own components and every string as plain text.
const (
	MaxPluginViewBytes          = 16 << 10
	MaxPluginViewBlocks         = 32
	MaxPluginViewItems          = 50
	MaxPluginTableColumns       = 16
	MaxPluginTableRows          = 200
	MaxPluginViewTextRunes      = 1024
	MaxPluginRecordTitleRunes   = 32
	MaxPluginRecordSummaryRunes = 256
	MaxPluginRecordActions      = 3
	MaxPluginActionLabelRunes   = 16
	MaxPluginActionConfirmRunes = 300
	// A record names at most as many commands as a plugin declares queries.
	MaxPluginRecordCommands = 8
)

type PluginViewBlockType string

const (
	PluginViewKV     PluginViewBlockType = "kv"
	PluginViewTable  PluginViewBlockType = "table"
	PluginViewBadges PluginViewBlockType = "badges"
	PluginViewText   PluginViewBlockType = "text"
)

// PluginViewBlock is one block of a view a plugin returns: label-value rows
// (kv), a table, badges, or a paragraph of text.
type PluginViewBlock struct {
	Type PluginViewBlockType `json:"type"`
	// Items are the rows of a kv block or the badges of a badges block.
	Items []PluginViewItem `json:"items,omitempty"`
	// Columns and Rows make a table block. Each row has one cell a column.
	Columns []string           `json:"columns,omitempty"`
	Rows    [][]PluginViewCell `json:"rows,omitempty"`
	// Text is the text of a text block; it may span lines.
	Text string `json:"text,omitempty"`
}

// PluginViewItem is a kv row (Label, Value, Mono) or a badge (Text, Tone).
type PluginViewItem struct {
	Label string `json:"label,omitempty"`
	Value string `json:"value,omitempty"`
	// Mono shows Value in a monospace font, as for an identifier.
	Mono bool           `json:"mono,omitempty"`
	Text string         `json:"text,omitempty"`
	Tone PluginViewTone `json:"tone,omitempty"`
}

type PluginViewTone string

const (
	PluginViewToneOK    PluginViewTone = "ok"
	PluginViewToneWarn  PluginViewTone = "warn"
	PluginViewToneError PluginViewTone = "error"
)

// PluginViewCell is a table cell: a string, or a number when Number is set.
type PluginViewCell struct {
	Text   string
	Number *float64
}

func (cell PluginViewCell) MarshalJSON() ([]byte, error) {
	if cell.Number != nil {
		return json.Marshal(*cell.Number)
	}
	return json.Marshal(cell.Text)
}

func (cell *PluginViewCell) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) > 0 && data[0] == '"' {
		*cell = PluginViewCell{}
		return json.Unmarshal(data, &cell.Text)
	}
	number, err := strconv.ParseFloat(string(data), 64)
	if err != nil || math.IsInf(number, 0) || math.IsNaN(number) {
		return fmt.Errorf("a table cell must be a string or a number")
	}
	*cell = PluginViewCell{Number: &number}
	return nil
}

// ValidatePluginView checks a view against the limits the host renders it
// within.
func ValidatePluginView(view []PluginViewBlock) error {
	if len(view) > MaxPluginViewBlocks {
		return fmt.Errorf("a plugin view has at most %d blocks", MaxPluginViewBlocks)
	}
	for index, block := range view {
		if err := block.Validate(); err != nil {
			return fmt.Errorf("view block %d: %w", index, err)
		}
	}
	if encoded, err := json.Marshal(view); err != nil || len(encoded) > MaxPluginViewBytes {
		return fmt.Errorf("a plugin view must be at most %d KiB as JSON", MaxPluginViewBytes>>10)
	}
	return nil
}

func (block PluginViewBlock) Validate() error {
	switch block.Type {
	case PluginViewKV:
		if len(block.Items) == 0 || len(block.Items) > MaxPluginViewItems || block.Columns != nil || block.Rows != nil || block.Text != "" {
			return fmt.Errorf("a kv block has 1 to %d items and nothing else", MaxPluginViewItems)
		}
		for _, item := range block.Items {
			if item.Text != "" || item.Tone != "" {
				return fmt.Errorf("a kv item has a label and a value")
			}
			if err := validateBoundedText("label", item.Label, MaxPluginViewTextRunes, false); err != nil {
				return err
			}
			if err := validateBoundedText("value", item.Value, MaxPluginViewTextRunes, true); err != nil {
				return err
			}
		}
	case PluginViewBadges:
		if len(block.Items) == 0 || len(block.Items) > MaxPluginViewItems || block.Columns != nil || block.Rows != nil || block.Text != "" {
			return fmt.Errorf("a badges block has 1 to %d badges and nothing else", MaxPluginViewItems)
		}
		for _, item := range block.Items {
			if item.Label != "" || item.Value != "" || item.Mono {
				return fmt.Errorf("a badge has a text and a tone")
			}
			if err := validateBoundedText("badge", item.Text, MaxPluginViewTextRunes, false); err != nil {
				return err
			}
			if item.Tone != "" && item.Tone != PluginViewToneOK && item.Tone != PluginViewToneWarn && item.Tone != PluginViewToneError {
				return fmt.Errorf("unknown badge tone %q", item.Tone)
			}
		}
	case PluginViewTable:
		if len(block.Columns) == 0 || len(block.Columns) > MaxPluginTableColumns || len(block.Rows) > MaxPluginTableRows || block.Items != nil || block.Text != "" {
			return fmt.Errorf("a table has 1 to %d columns, at most %d rows and nothing else", MaxPluginTableColumns, MaxPluginTableRows)
		}
		for _, column := range block.Columns {
			if err := validateBoundedText("column", column, MaxPluginViewTextRunes, false); err != nil {
				return err
			}
		}
		for _, row := range block.Rows {
			if len(row) != len(block.Columns) {
				return fmt.Errorf("a table row has one cell a column")
			}
			for _, cell := range row {
				if cell.Number != nil && cell.Text != "" {
					return fmt.Errorf("a table cell is a string or a number")
				}
				if err := validateBoundedText("cell", cell.Text, MaxPluginViewTextRunes, true); err != nil {
					return err
				}
			}
		}
	case PluginViewText:
		if block.Items != nil || block.Columns != nil || block.Rows != nil {
			return fmt.Errorf("a text block has only text")
		}
		if err := validateMultilineText("text", block.Text, MaxPluginViewTextRunes); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown view block type %q", block.Type)
	}
	return nil
}

// validateMultilineText is validateBoundedText that also allows line breaks.
func validateMultilineText(field, value string, maxRunes int) error {
	if value == "" {
		return fmt.Errorf("%s must not be empty", field)
	}
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > maxRunes {
		return fmt.Errorf("%s must contain at most %d characters", field, maxRunes)
	}
	for _, runeValue := range value {
		if runeValue < 32 && runeValue != '\t' && runeValue != '\n' {
			return fmt.Errorf("%s must not contain control characters", field)
		}
	}
	return nil
}

type PluginRecordStatus string

const (
	PluginRecordOK      PluginRecordStatus = "ok"
	PluginRecordSkipped PluginRecordStatus = "skipped"
	PluginRecordFailed  PluginRecordStatus = "failed"
)

func (status PluginRecordStatus) Valid() bool {
	return status == PluginRecordOK || status == PluginRecordSkipped || status == PluginRecordFailed
}

// PluginRecord is what a plugin says about its part in a request or about
// itself: a title such as 「记忆」, a status, a one-line summary and a view.
type PluginRecord struct {
	Title   string             `json:"title"`
	Status  PluginRecordStatus `json:"status"`
	Summary string             `json:"summary"`
	View    []PluginViewBlock  `json:"view,omitempty"`
	// Truncated marks a record the host cut to its limits, dropping what
	// did not fit or was not valid.
	Truncated bool `json:"truncated,omitempty"`
	// Actions are the buttons the record shows, such as 「刷新记忆」. A
	// click calls the plugin's onAction hook for the record's session.
	Actions []PluginRecordAction `json:"actions,omitempty"`
}

// PluginRecordAction is a button on a plugin's record. Confirm, when set, is
// asked before the click goes to the plugin.
type PluginRecordAction struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Confirm string `json:"confirm,omitempty"`
}

var pluginActionIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

// ValidPluginActionID reports whether id can name an action of a plugin
// record: a lowercase letter, then up to 31 lowercase letters, digits and
// underscores.
func ValidPluginActionID(id string) bool { return pluginActionIDPattern.MatchString(id) }

func (action PluginRecordAction) Validate() error {
	if !ValidPluginActionID(action.ID) {
		return fmt.Errorf("action id must be a lowercase letter followed by up to 31 lowercase letters, digits or underscores")
	}
	if err := validateBoundedText("action label", action.Label, MaxPluginActionLabelRunes, false); err != nil {
		return err
	}
	return validateBoundedText("action confirm", action.Confirm, MaxPluginActionConfirmRunes, true)
}

func (record PluginRecord) Validate() error {
	if err := validateBoundedText("title", record.Title, MaxPluginRecordTitleRunes, false); err != nil {
		return err
	}
	if !record.Status.Valid() {
		return fmt.Errorf("unknown plugin record status %q", record.Status)
	}
	if err := validateBoundedText("summary", record.Summary, MaxPluginRecordSummaryRunes, true); err != nil {
		return err
	}
	if len(record.Actions) > MaxPluginRecordActions {
		return fmt.Errorf("a plugin record has at most %d actions", MaxPluginRecordActions)
	}
	for index, action := range record.Actions {
		if err := action.Validate(); err != nil {
			return err
		}
		for _, other := range record.Actions[:index] {
			if other.ID == action.ID {
				return fmt.Errorf("a plugin record lists the action %q twice", action.ID)
			}
		}
	}
	return ValidatePluginView(record.View)
}

// MaxRequestPluginRecords bounds the plugin records kept with one request:
// a few for each plugin's part in it.
const MaxRequestPluginRecords = 48

// Why a plugin hook gave a request nothing: the plugin's own reasons, such
// as no_project or short_query, and the host's.
const (
	PluginSkipTimeout            = "timeout"
	PluginSkipPluginError        = "plugin_error"
	PluginSkipPrivacyBlocked     = "privacy_blocked"
	PluginSkipPrivacyUnavailable = "privacy_unavailable"
	// PluginSkipPausedAll: the user paused every plugin.
	PluginSkipPausedAll = "paused_all"
	// PluginSkipClientDisabled: the plugin is off for the request's client.
	PluginSkipClientDisabled = "client_disabled"
	// PluginSkipSessionPaused: the user paused the plugin in the session.
	PluginSkipSessionPaused = "session_paused"
	// PluginSkipOverBudget: the plugins before this one used up what one
	// request may carry.
	PluginSkipOverBudget = "over_budget"
	// Why a lookup ran no query: its rate, its turn's count, a session
	// without the values the query needs, or input it does not take.
	PluginSkipRateLimited = "rate_limited"
	PluginSkipTurnLimit   = "turn_limit"
	PluginSkipUnbound     = "unbound"
	PluginSkipBadInput    = "invalid_input"
)

// Why the host computed a session's resident block: the session's first
// request since Core started, a binding that changed, the plugin's version,
// settings or language that changed, a compacted history, the user's
// refresh, or the automatic check for newer data.
const (
	PluginTriggerStart   = "start"
	PluginTriggerBound   = "bound"
	PluginTriggerConfig  = "config"
	PluginTriggerCompact = "compact"
	PluginTriggerManual  = "manual"
	PluginTriggerAuto    = "auto"
)

// Where an injected block went.
const (
	PluginPlacementSystem      = "system_append"
	PluginPlacementTurnMessage = "turn_message_prefix"
)

// RequestPluginRecord is a plugin's part in a request: what the plugin
// reported, and what the host observed and did with it.
type RequestPluginRecord struct {
	Plugin        string `json:"plugin"`
	PluginVersion string `json:"plugin_version"`
	Hook          string `json:"hook"`
	// Title, Status, Summary and View are the plugin's own.
	Title     string             `json:"title"`
	Status    PluginRecordStatus `json:"status"`
	Summary   string             `json:"summary"`
	View      []PluginViewBlock  `json:"view,omitempty"`
	Truncated bool               `json:"truncated,omitempty"`
	// SkippedReason says why the hook gave the request nothing.
	SkippedReason string `json:"skipped_reason,omitempty"`
	ElapsedMs     int    `json:"elapsed_ms"`
	// Injection is what the host added to the request for the plugin.
	Injection *PluginInjection   `json:"injection,omitempty"`
	Network   PluginNetworkCount `json:"network"`
	// Observe tells what became of the turn's capture: queued,
	// awaiting_reply, held_unbound (kept until the session has a value,
	// then captured), skipped_unbound, skipped_privacy,
	// skipped_binding_turn, skipped_continuation, queue_full or disabled.
	Observe string `json:"observe,omitempty"`
	// Trigger tells why a resident block was computed, on the record of
	// the request that first carried it.
	Trigger string `json:"trigger,omitempty"`
	// Actions are the plugin's buttons for the record's session.
	Actions []PluginRecordAction `json:"actions,omitempty"`
	// Lookup describes a query the model ran through the lookup outlet,
	// by its size: what it asked and what it got are not kept.
	Lookup *PluginLookup `json:"lookup,omitempty"`
	// Commands name the plugin's queries whose commands its turn block
	// carried, as an ask that offers the model a look at every project.
	Commands []string `json:"commands,omitempty"`
	// Effects say what an extension point's call did to the request or
	// its response, as PluginEffect codes; Tags are the tags onRequest
	// gave the request.
	Effects []string `json:"effects,omitempty"`
	Tags    []string `json:"tags,omitempty"`
	// Events counts the stream events a stream hook saw, and Failures
	// those it failed on, which went on as they came.
	Events   int `json:"events,omitempty"`
	Failures int `json:"failures,omitempty"`
}

// What an extension point's call did, in RequestPluginRecord.Effects.
const (
	PluginEffectDeny     = "deny"
	PluginEffectTag      = "tag"
	PluginEffectOrder    = "order"
	PluginEffectExclude  = "exclude"
	PluginEffectModel    = "model"
	PluginEffectSystem   = "system"
	PluginEffectTurn     = "turn"
	PluginEffectParams   = "params"
	PluginEffectTools    = "tools"
	PluginEffectBody     = "body"
	PluginEffectHeaders  = "headers"
	PluginEffectRewrite  = "rewrite"
	PluginEffectStream   = "stream"
	PluginEffectObserved = "observed"
)

// Bounds of the tags onRequest gives a request.
const (
	MaxPluginTags     = 8
	MaxPluginTagRunes = 32
)

// PluginLookup is a query the model ran, or a report it made: which, how
// long its input was in characters, how many bytes of result it got, and
// how many hits the plugin said they held.
type PluginLookup struct {
	Tool        string `json:"tool"`
	InputRunes  int    `json:"input_runes"`
	ResultBytes int    `json:"result_bytes"`
	Hits        *int   `json:"hits,omitempty"`
	// Writes marks a report: the model handed the plugin something to
	// keep.
	Writes bool `json:"writes,omitempty"`
}

// PluginInjection is what the host injected into one request for a plugin.
type PluginInjection struct {
	Placement      string `json:"placement"`
	InjectedBytes  int    `json:"injected_bytes"`
	ReplayedBlocks int    `json:"replayed_blocks"`
	DroppedItems   int    `json:"dropped_items"`
	// UsageBytes is the part of InjectedBytes that tells the model how to
	// use the plugin's lookups, which the gateway adds itself.
	UsageBytes int `json:"usage_bytes,omitempty"`
}

func (record RequestPluginRecord) Validate() error {
	if !ValidPluginKey(record.Plugin) {
		return fmt.Errorf("plugin must be a plugin key")
	}
	if err := validateBoundedText("plugin_version", record.PluginVersion, 32, true); err != nil {
		return err
	}
	if err := validateBoundedText("hook", record.Hook, 32, false); err != nil {
		return err
	}
	if err := (PluginRecord{Title: record.Title, Status: record.Status, Summary: record.Summary, View: record.View, Actions: record.Actions}).Validate(); err != nil {
		return err
	}
	if err := validateBoundedText("skipped_reason", record.SkippedReason, 64, true); err != nil {
		return err
	}
	switch record.Trigger {
	case "", PluginTriggerStart, PluginTriggerBound, PluginTriggerConfig, PluginTriggerCompact, PluginTriggerManual, PluginTriggerAuto:
	default:
		return fmt.Errorf("unknown plugin trigger %q", record.Trigger)
	}
	if err := validateBoundedText("observe", record.Observe, 64, true); err != nil {
		return err
	}
	if record.ElapsedMs < 0 || record.Network.Requests < 0 || record.Network.Denied < 0 || record.Network.Errors < 0 || record.Network.WaitMs < 0 {
		return fmt.Errorf("plugin record counts must be non-negative")
	}
	if len(record.Effects) > 16 || len(record.Tags) > MaxPluginTags || record.Events < 0 || record.Failures < 0 {
		return fmt.Errorf("plugin record has too many effects or tags")
	}
	for _, effect := range record.Effects {
		if err := validateBoundedText("plugin effect", effect, 16, false); err != nil {
			return err
		}
	}
	for _, tag := range record.Tags {
		if err := validateBoundedText("plugin tag", tag, MaxPluginTagRunes, false); err != nil {
			return err
		}
	}
	if len(record.Commands) > MaxPluginRecordCommands {
		return fmt.Errorf("plugin record names more than %d commands", MaxPluginRecordCommands)
	}
	for _, name := range record.Commands {
		if err := validateBoundedText("plugin record command", name, 32, false); err != nil {
			return err
		}
	}
	if lookup := record.Lookup; lookup != nil {
		if err := validateBoundedText("lookup tool", lookup.Tool, 32, false); err != nil {
			return err
		}
		if lookup.InputRunes < 0 || lookup.ResultBytes < 0 || (lookup.Hits != nil && *lookup.Hits < 0) {
			return fmt.Errorf("lookup counts must be non-negative")
		}
	}
	if injection := record.Injection; injection != nil {
		if injection.Placement != PluginPlacementSystem && injection.Placement != PluginPlacementTurnMessage {
			return fmt.Errorf("unknown injection placement %q", injection.Placement)
		}
		if injection.InjectedBytes < 0 || injection.ReplayedBlocks < 0 || injection.DroppedItems < 0 || injection.UsageBytes < 0 || injection.UsageBytes > injection.InjectedBytes {
			return fmt.Errorf("injection counts must be non-negative, the usage within what was injected")
		}
	}
	return nil
}
