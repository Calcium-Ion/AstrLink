package pluginhost

import (
	"context"
	"net/http"
	"slices"
	"sort"
	"time"
	"unicode/utf16"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
	"github.com/QuantumNous/new-api/pluginkit"
)

// The reply check reads the text of the model's answer before the client
// gets it, to mask parts of it or to stop it. The instances run as a
// pipeline: each reads what the one before released, its masks applied.
// In a stream an instance is called on an output's first text, then each
// time it gained 32 UTF-16 code units, when the output ends, and when an
// event without text arrives behind unread text; it may hold back the end
// of what it read until its next call.

const (
	replyCallUnits = 32
	replyMask      = "***"
)

// ReplyPiece is new text of an output, or its end.
type ReplyPiece struct {
	Index int
	Text  string
	Final bool
}

// ReplyCheck runs the reply hooks of a request's instances over one
// answer.
type ReplyCheck struct {
	run     *RequestRun
	ctx     context.Context
	format  string
	stream  bool
	attempt RequestAttempt
	stages  []*replyStage
	stopped *RequestRefusal
}

type replyStage struct {
	instance *requestInstance
	outputs  map[int]*replyOutput
	passed   bool
	failed   bool
	called   bool
	masked   int
	findings []pluginkit.TransformFinding
	notice   pluginkit.LocalizedText
	stop     string
	started  time.Time
	elapsed  time.Duration
}

type replyOutput struct {
	pending []uint16
	offset  int
	gained  int
	called  bool
	final   bool
	done    bool
}

// Reply starts the reply check of one answer in protocol: nil when no
// instance of the request checks replies.
func (run *RequestRun) Reply(ctx context.Context, protocol contract.ProtocolID, stream bool, attempt RequestAttempt) *ReplyCheck {
	if !run.Serves(contract.RequestStageReply) {
		return nil
	}
	check := &ReplyCheck{run: run, ctx: ctx, format: RequestFormat(protocol), stream: stream, attempt: attempt}
	for _, current := range run.instances {
		if current.meta.HasStage(contract.RequestStageReply) {
			check.stages = append(check.stages, &replyStage{instance: current, outputs: map[int]*replyOutput{}, started: time.Now()})
		}
	}
	return check
}

// Idle reports whether no instance reads the rest of the answer: every
// one passed or was skipped, and none holds text.
func (check *ReplyCheck) Idle() bool {
	if check == nil {
		return true
	}
	for _, stage := range check.stages {
		if !stage.passed && !stage.failed {
			return false
		}
		for _, output := range stage.outputs {
			if len(output.pending) > 0 {
				return false
			}
		}
	}
	return true
}

// Held reports whether an instance holds text of an output.
func (check *ReplyCheck) Held() bool {
	if check == nil {
		return false
	}
	for _, stage := range check.stages {
		for _, output := range stage.outputs {
			if len(output.pending) > 0 {
				return true
			}
		}
	}
	return false
}

// Stopped is the refusal of an instance that stopped the answer.
func (check *ReplyCheck) Stopped() *RequestRefusal {
	if check == nil {
		return nil
	}
	return check.stopped
}

// Feed gives the pipeline new text of outputs, and their ends; force calls
// the instances on what they have not read even when it is short, as an
// event without text arrived behind it. It returns the text released to
// the client, by output, and the refusal of an instance that stopped the
// answer.
func (check *ReplyCheck) Feed(pieces []ReplyPiece, force bool) (map[int]string, *RequestRefusal) {
	if check == nil {
		released := map[int]string{}
		for _, piece := range pieces {
			released[piece.Index] += piece.Text
		}
		return released, nil
	}
	if check.stopped != nil {
		return nil, check.stopped
	}
	for _, stage := range check.stages {
		var refusal *RequestRefusal
		pieces, refusal = check.stage(stage, pieces, force)
		if refusal != nil {
			check.stopped = refusal
			return nil, refusal
		}
	}
	released := map[int]string{}
	for _, piece := range pieces {
		if piece.Text != "" {
			released[piece.Index] += piece.Text
		}
	}
	return released, nil
}

// stage runs one instance over the pieces and returns what it released.
func (check *ReplyCheck) stage(stage *replyStage, pieces []ReplyPiece, force bool) ([]ReplyPiece, *RequestRefusal) {
	order := []int{}
	for _, piece := range pieces {
		output := stage.outputs[piece.Index]
		if output == nil {
			output = &replyOutput{}
			stage.outputs[piece.Index] = output
		}
		units := utf16.Encode([]rune(piece.Text))
		output.pending = append(output.pending, units...)
		output.gained += len(units)
		output.final = output.final || piece.Final
		if !slices.Contains(order, piece.Index) {
			order = append(order, piece.Index)
		}
	}
	if force {
		for index, output := range stage.outputs {
			if len(output.pending) > 0 && !slices.Contains(order, index) {
				order = append(order, index)
			}
		}
	}
	sort.Ints(order)
	release := func(index int) ReplyPiece {
		output := stage.outputs[index]
		text := string(utf16.Decode(output.pending))
		output.offset += len(output.pending)
		output.pending = nil
		output.gained = 0
		piece := ReplyPiece{Index: index, Text: text, Final: output.final}
		output.done = output.final
		return piece
	}
	var out []ReplyPiece
	if stage.passed || stage.failed {
		for _, index := range order {
			out = append(out, release(index))
		}
		return out, nil
	}
	var due []int
	for _, index := range order {
		output := stage.outputs[index]
		if output.done && len(output.pending) == 0 {
			continue
		}
		if output.final || force || !output.called && len(output.pending) > 0 || output.gained >= replyCallUnits || !check.stream {
			due = append(due, index)
		}
	}
	if len(due) == 0 {
		return out, nil
	}
	stage.called = true
	outputs := make([]pluginkit.ReplyOutput, 0, len(due))
	for _, index := range due {
		output := stage.outputs[index]
		output.called = true
		outputs = append(outputs, pluginkit.ReplyOutput{Index: index, Text: string(utf16.Decode(output.pending)), Offset: output.offset, Final: output.final})
	}
	result, err := check.call(stage, outputs)
	if err != nil {
		stage.failed = true
		check.run.failed(stage.instance, contract.RequestStageReply, err)
		if !stage.instance.meta.Meta.Optional {
			return nil, &RequestRefusal{Status: http.StatusInternalServerError, Code: contract.RequestPluginFailedCode, Message: "A request plugin failed"}
		}
		for _, index := range due {
			out = append(out, release(index))
		}
		return out, nil
	}
	for _, mask := range result.Masks {
		if mask.Category != "" {
			result.Findings = append(result.Findings, pluginkit.TransformFinding{Rule: mask.Category, Category: mask.Category, Count: 1})
		}
	}
	stage.findings = append(stage.findings, result.Findings...)
	if len(result.Notice) > 0 {
		stage.notice = result.Notice
	}
	switch result.Action {
	case "pass":
		stage.passed = true
		for _, index := range due {
			out = append(out, release(index))
		}
		return out, nil
	case "stop":
		stage.stop = result.Code
		message := result.Message
		if message == "" {
			message = "Response stopped by policy"
		}
		return nil, &RequestRefusal{Status: http.StatusBadRequest, Code: contract.ResponseRejectedCode, Message: message + " (code: " + result.Code + ")"}
	}
	for _, index := range due {
		output := stage.outputs[index]
		var spans [][2]int
		for _, mask := range result.Masks {
			if mask.Index == index {
				spans = append(spans, [2]int{mask.Start, mask.End})
			}
		}
		stage.masked += len(spans)
		cut := len(output.pending)
		if !output.final {
			cut = max(0, cut-result.Hold)
		}
		// A mask across the cut is released whole; a surrogate pair is not
		// split.
		for _, span := range spans {
			if span[0] < cut && span[1] > cut {
				cut = span[1]
			}
		}
		if cut > 0 && cut < len(output.pending) && utf16.IsSurrogate(rune(output.pending[cut-1])) && output.pending[cut-1] < 0xDC00 {
			cut--
		}
		text := maskUnits(output.pending[:cut], spans)
		output.offset += cut
		output.pending = slices.Clone(output.pending[cut:])
		output.gained = len(output.pending)
		done := output.final && len(output.pending) == 0
		output.done = done
		out = append(out, ReplyPiece{Index: index, Text: text, Final: done})
	}
	return out, nil
}

// maskUnits replaces the spans of units, merged where they overlap, with
// the mask.
func maskUnits(units []uint16, spans [][2]int) string {
	if len(spans) == 0 {
		return string(utf16.Decode(units))
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i][0] < spans[j][0] })
	var merged [][2]int
	for _, span := range spans {
		if span[0] >= len(units) {
			continue
		}
		span[1] = min(span[1], len(units))
		if count := len(merged); count > 0 && span[0] <= merged[count-1][1] {
			merged[count-1][1] = max(merged[count-1][1], span[1])
			continue
		}
		merged = append(merged, span)
	}
	var out []uint16
	at := 0
	for _, span := range merged {
		out = append(out, units[at:span[0]]...)
		out = append(out, utf16.Encode([]rune(replyMask))...)
		at = span[1]
	}
	out = append(out, units[at:]...)
	return string(utf16.Decode(out))
}

// call runs one instance's reply hook.
func (check *ReplyCheck) call(stage *replyStage, outputs []pluginkit.ReplyOutput) (pluginkit.ReplyResult, error) {
	started := time.Now()
	defer func() { stage.elapsed += time.Since(started) }()
	attempt := check.attempt
	tctx := check.run.transformContext(stage.instance, contract.RequestStageReply, check.format, attempt)
	return callRequestHook(check.ctx, stage.instance, contract.RequestStageReply, nil, func(ctx context.Context, loaded *plugin.NewAPIPlugin) (pluginkit.ReplyResult, error) {
		return loaded.CallReply(ctx, tctx, check.stream, outputs)
	})
}

// Finish adds each instance's entry for the whole answer to the request
// record.
func (check *ReplyCheck) Finish() {
	if check == nil {
		return
	}
	for _, stage := range check.stages {
		if !stage.called && !stage.failed && stage.stop == "" {
			continue
		}
		result := &pluginkit.TransformResult{Action: "continue", Findings: stage.findings, Notice: stage.notice}
		status := contract.PluginRecordOK
		var effects []string
		switch {
		case stage.failed && !stage.instance.meta.Meta.Optional:
			status = contract.PluginRecordFailed
		case stage.failed:
			status = contract.PluginRecordFailed
		case stage.stop != "":
			result.Action, result.Code = "reject", stage.stop
			effects = append(effects, contract.PluginEffectDeny)
		}
		if stage.masked > 0 {
			effects = append(effects, contract.PluginEffectRewrite)
		}
		check.run.note(stage.instance, contract.RequestStageReply, status, "", result, time.Now().Add(-stage.elapsed), effects)
	}
}
