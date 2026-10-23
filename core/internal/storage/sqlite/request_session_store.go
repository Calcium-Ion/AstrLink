package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	storagecontract "github.com/QuantumNous/astrlink/core/internal/storage"
)

// maxSessionLinkValues bounds one lookup so a hostile history cannot turn the
// query into a scan over hundreds of bound parameters.
const maxSessionLinkValues = 64

// FindSessionLink picks the most recent root whose cursors of kind include one
// of values. Explicit cursors match in both directions and also against the
// legacy previous_response_id / output_response_id columns written before the
// cursor table existed; echo ids and fingerprints match only what a response
// produced. Children (demoted retries) never anchor a session.
func (store *Store) FindSessionLink(
	ctx context.Context,
	kind contract.SessionCursorKind,
	values []string,
	scope storagecontract.SessionCursorScope,
) (storagecontract.SessionLinkMatch, bool, error) {
	if !kind.Valid() {
		return storagecontract.SessionLinkMatch{}, false, fmt.Errorf("%w: session cursor kind", storagecontract.ErrInvalidArgument)
	}
	cleaned := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || len([]rune(value)) > contract.MaxProtocolCursorRunes {
			continue
		}
		cleaned = append(cleaned, value)
		if len(cleaned) == maxSessionLinkValues {
			break
		}
	}
	if len(cleaned) == 0 {
		return storagecontract.SessionLinkMatch{}, false, nil
	}
	query, args := sessionLinkQuery(kind, cleaned, scope)

	var (
		sessionID, turnUserFingerprint, value, status sql.NullString
		turnIndex, turnUserMessages                   sql.NullInt64
	)
	err := store.db.QueryRowContext(ctx, query, args...).Scan(
		&sessionID, &turnIndex, &turnUserMessages, &turnUserFingerprint, &value, &status,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return storagecontract.SessionLinkMatch{}, false, nil
		}
		return storagecontract.SessionLinkMatch{}, false, fmt.Errorf("lookup session link: %w", err)
	}
	if !sessionID.Valid || sessionID.String == "" {
		return storagecontract.SessionLinkMatch{}, false, nil
	}
	id := contract.SessionID(sessionID.String)
	if err := id.Validate(); err != nil {
		return storagecontract.SessionLinkMatch{}, false, fmt.Errorf("%w: session %q", storagecontract.ErrInvalidRecord, sessionID.String)
	}
	match := storagecontract.SessionLinkMatch{SessionID: id, Value: value.String, Status: contract.RequestStatus(status.String)}
	if turnIndex.Valid && turnIndex.Int64 >= 1 {
		turn := int(turnIndex.Int64)
		match.TurnIndex = &turn
		if turnUserMessages.Valid && turnUserMessages.Int64 >= 0 {
			users := int(turnUserMessages.Int64)
			match.TurnUserMessages = &users
		}
		if turnUserFingerprint.Valid {
			match.TurnUserFingerprint = turnUserFingerprint.String
		}
	}
	return match, true, nil
}

func (store *Store) ListRequestSessions(
	ctx context.Context,
	options storagecontract.RequestSessionListOptions,
) (storagecontract.RequestSessionPage, error) {
	return cachedHistoryRead(ctx, store.db, &store.requestSessions, options, func(conn *sql.Conn) (storagecontract.RequestSessionPage, error) {
		return listRequestSessions(ctx, conn, options)
	})
}

func listRequestSessions(ctx context.Context, reader historyReader, options storagecontract.RequestSessionListOptions) (storagecontract.RequestSessionPage, error) {
	if options.Kind != "" && options.Kind != "inference" && options.Kind != "discovery" {
		return storagecontract.RequestSessionPage{}, fmt.Errorf("%w: kind must be inference or discovery", storagecontract.ErrInvalidArgument)
	}
	limit := options.Limit
	if limit == 0 {
		limit = defaultListLimit
	}
	if limit < 1 || limit > maxListLimit {
		return storagecontract.RequestSessionPage{}, fmt.Errorf(
			"%w: limit must be between 1 and %d",
			storagecontract.ErrInvalidArgument,
			maxListLimit,
		)
	}
	cursorStarted, cursorID, err := decodeRequestRecordCursor(options.Cursor)
	if err != nil {
		return storagecontract.RequestSessionPage{}, err
	}

	// A session's newest matching root places it in the list, so walking the
	// roots newest first meets the sessions in list order. Grouping every root
	// instead made each one-second poll read the whole history.
	query, args := requestSessionScan(options)
	started := time.Now()
	defer logSlowRequestList("request sessions list slow", started)
	rows, err := reader.QueryContext(ctx, query, args...)
	if err != nil {
		logRequestListFailure("request sessions list failed", err)
		return storagecontract.RequestSessionPage{}, fmt.Errorf("list request sessions: %w", err)
	}
	defer rows.Close()

	seen := make(map[string]struct{}, limit+1)
	matched := make([]sessionAggregate, 0, limit+1)
	for rows.Next() {
		var item sessionAggregate
		if err := rows.Scan(&item.id, &item.lastStartedAt); err != nil {
			logRequestListFailure("request sessions list failed", err)
			return storagecontract.RequestSessionPage{}, err
		}
		// Sessions arrive by their newest root. Past the last one the page
		// needs, only a session tied with it on time can still sort ahead.
		if len(matched) > limit && item.lastStartedAt < matched[limit].lastStartedAt {
			break
		}
		if _, ok := seen[item.id]; ok {
			continue
		}
		seen[item.id] = struct{}{}
		// Earlier pages hold sessions at or after the cursor, by their newest
		// root; an older root of theirs must not bring them back.
		if options.Cursor != "" && (item.lastStartedAt > cursorStarted ||
			item.lastStartedAt == cursorStarted && item.id >= cursorID) {
			continue
		}
		matched = append(matched, item)
	}
	if err := rows.Err(); err != nil {
		logRequestListFailure("request sessions list failed", err)
		return storagecontract.RequestSessionPage{}, fmt.Errorf("iterate request sessions: %w", err)
	}
	if err := rows.Close(); err != nil {
		return storagecontract.RequestSessionPage{}, err
	}
	// The same order as the cursor: newest first, then by descending id.
	slices.SortStableFunc(matched, func(left, right sessionAggregate) int {
		if order := strings.Compare(right.lastStartedAt, left.lastStartedAt); order != 0 {
			return order
		}
		return strings.Compare(right.id, left.id)
	})
	if len(matched) > limit+1 {
		matched = matched[:limit+1]
	}

	page := storagecontract.RequestSessionPage{Items: make([]contract.RequestSession, 0, limit)}
	if len(matched) > limit {
		matched = matched[:limit]
		last := matched[len(matched)-1]
		lastStarted, parseErr := time.Parse(time.RFC3339Nano, last.lastStartedAt)
		if parseErr != nil {
			return storagecontract.RequestSessionPage{}, fmt.Errorf(
				"%w: session cursor timestamp",
				storagecontract.ErrInvalidRecord,
			)
		}
		page.NextCursor = encodeRequestRecordCursor(lastStarted, contract.RequestID(last.id))
	}
	summaries, err := loadSessionSummaries(ctx, reader, matched)
	if err != nil {
		return storagecontract.RequestSessionPage{}, err
	}
	for _, item := range matched {
		page.Items = append(page.Items, summaries[item.id])
	}
	if err := loadSessionRuntimes(ctx, reader, page.Items); err != nil {
		return storagecontract.RequestSessionPage{}, err
	}
	return page, nil
}

// requestSessionScan selects the roots the options match, newest first. Its
// order comes from an index, so a reader that stops early reads only the
// newest roots.
func requestSessionScan(options storagecontract.RequestSessionListOptions) (string, []any) {
	query := strings.Builder{}
	query.WriteString(`
SELECT COALESCE(session_id, id), started_at
FROM request_records
WHERE parent_request_id IS NULL`)
	args := make([]any, 0, 16)
	// Classify in SQL so discovery traffic cannot consume call-list pages.
	if options.Kind != "" {
		if options.Kind == "discovery" {
			query.WriteString(` AND input_protocol IN (?, ?)`)
		} else {
			query.WriteString(` AND input_protocol NOT IN (?, ?)`)
		}
		args = append(args, string(contract.ProtocolOpenAIModels), string(contract.ProtocolGoogleModels))
	}
	if options.From != nil {
		query.WriteString(` AND started_at >= ?`)
		args = append(args, options.From.UTC().Format(time.RFC3339Nano))
	}
	if options.To != nil {
		query.WriteString(` AND started_at < ?`)
		args = append(args, options.To.UTC().Format(time.RFC3339Nano))
	}
	appendAccessTokenFilter(&query, &args, options.LocalAccessTokenIDs)
	appendModelFilter(&query, &args, options.Models)
	if options.Protocol != nil {
		query.WriteString(` AND input_protocol = ?`)
		args = append(args, string(*options.Protocol))
	}
	if options.ServiceID != nil {
		query.WriteString(` AND service_id = ?`)
		args = append(args, string(*options.ServiceID))
	}
	if options.Status != nil {
		query.WriteString(` AND status = ?`)
		args = append(args, string(*options.Status))
	}
	query.WriteString(` ORDER BY started_at DESC, id DESC`)
	return query.String(), args
}

func (store *Store) GetRequestSession(
	ctx context.Context,
	rawID string,
) (contract.RequestSessionDetail, error) {
	id := contract.SessionID(rawID)
	if err := id.Validate(); err != nil {
		return contract.RequestSessionDetail{}, fmt.Errorf("%w: %v", storagecontract.ErrInvalidArgument, err)
	}

	turns, err := store.listSessionTurns(ctx, string(id))
	if err != nil {
		return contract.RequestSessionDetail{}, err
	}
	if len(turns) == 0 {
		record, getErr := store.GetRequestRecord(ctx, contract.RequestID(rawID))
		if getErr != nil {
			return contract.RequestSessionDetail{}, getErr
		}
		if record.ParentRequestID != nil {
			return contract.RequestSessionDetail{}, fmt.Errorf("%w: session %q", storagecontract.ErrNotFound, rawID)
		}
		if record.SessionID != nil && string(*record.SessionID) != rawID {
			return store.GetRequestSession(ctx, string(*record.SessionID))
		}
		turns = []contract.RequestRecord{record}
	}
	session, err := sessionFromTurns(turns)
	if err != nil {
		return contract.RequestSessionDetail{}, err
	}
	sessions := []contract.RequestSession{session}
	if err := store.loadSessionRuntimes(ctx, sessions); err != nil {
		return contract.RequestSessionDetail{}, err
	}
	return contract.RequestSessionDetail{RequestSession: sessions[0], Turns: turns}, nil
}

// Sum actual call durations, including demoted retry attempts. Root records
// contain only the final attempt's latency. Idle gaps between calls never count.
// Keep open starts separate so clients can tick without changing idle snapshots.
func (store *Store) loadSessionRuntimes(ctx context.Context, sessions []contract.RequestSession) error {
	return loadSessionRuntimes(ctx, store.db, sessions)
}

func loadSessionRuntimes(ctx context.Context, reader historyReader, sessions []contract.RequestSession) error {
	if len(sessions) == 0 {
		return nil
	}
	byID := make(map[string]*contract.RequestSession, len(sessions))
	performance := make(map[string]*sessionPerformance, len(sessions))
	args := make([]any, len(sessions))
	for i := range sessions {
		session := &sessions[i]
		session.DurationMs = 0
		session.ActiveRequestStarts = make([]time.Time, 0)
		byID[string(session.ID)] = session
		performance[string(session.ID)] = &sessionPerformance{}
		args[i] = string(session.ID)
	}
	rows, err := reader.QueryContext(ctx, sessionRuntimeQuery(len(args)), args...)
	if err != nil {
		return fmt.Errorf("load session runtimes: %w", err)
	}
	defer rows.Close()
	var id, startedAt, status string
	var completedAt, errorCode sql.NullString
	var latency sql.NullInt64
	var rootID, callID, protocol string
	var turn, firstToken, outputTokens sql.NullInt64
	var streaming bool
	var incomplete sql.NullBool
	for rows.Next() {
		if err := rows.Scan(&id, &startedAt, &completedAt, &status, &latency, &errorCode,
			&rootID, &turn, &callID, &protocol, &streaming, &firstToken, &outputTokens, &incomplete); err != nil {
			return fmt.Errorf("scan session runtime: %w", err)
		}
		session := byID[id]
		started, err := time.Parse(time.RFC3339Nano, startedAt)
		if err != nil {
			return fmt.Errorf("parse session runtime start: %w", err)
		}
		record := contract.RequestRecord{ID: contract.RequestID(callID), StartedAt: started,
			InputProtocol: contract.ProtocolID(protocol), Streaming: streaming}
		if firstToken.Valid {
			value := int(firstToken.Int64)
			record.FirstTokenMs = &value
		}
		if latency.Valid {
			value := int(latency.Int64)
			record.LatencyMs = &value
		}
		if outputTokens.Valid {
			record.Usage = &contract.Usage{OutputTokens: int(outputTokens.Int64), BillingIncomplete: incomplete.Bool}
		}
		if errorCode.Valid {
			record.Error = &contract.ErrorSummary{Code: errorCode.String}
		}
		if completedAt.Valid && errorCode.String != "core_interrupted" {
			completed, err := time.Parse(time.RFC3339Nano, completedAt.String)
			if err != nil {
				return fmt.Errorf("parse session runtime completion: %w", err)
			}
			record.CompletedAt = &completed
		}
		performance[id].observe(rootID, int(turn.Int64), record)
		if latency.Valid {
			session.DurationMs += max(0, latency.Int64)
		} else if completedAt.Valid {
			// Startup recovery timestamps say when the interruption was discovered,
			// not when execution stopped. Unknown runtime must not include downtime.
			if errorCode.String == "core_interrupted" {
				continue
			}
			session.DurationMs += max(0, record.CompletedAt.Sub(started).Milliseconds())
		} else if status == string(contract.RequestStatusPending) {
			session.ActiveRequestStarts = append(session.ActiveRequestStarts, started)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate session runtimes: %w", err)
	}
	for id, stats := range performance {
		stats.apply(byID[id])
		// Calls arrive in index order; a long session would spill a sort by
		// start time to disk on every poll, and only these starts need it.
		slices.SortFunc(byID[id].ActiveRequestStarts, time.Time.Compare)
	}
	return nil
}

type sessionAggregate struct {
	id            string
	lastStartedAt string
}

func logRequestListFailure(op string, err error) {
	if err == nil {
		return
	}
	log.Printf("%s: %v", op, err)
}

func logSlowRequestList(op string, started time.Time) {
	elapsed := time.Since(started)
	if elapsed < slowListAfter {
		return
	}
	log.Printf("%s: %s", op, elapsed.Round(time.Millisecond))
}

// Lists need one summary per session, not a retained RequestRecord per call.
// Read only summary fields and fold them in index order.
const requestSessionSummaryColumns = `
    id, COALESCE(session_id, id), started_at, completed_at, status, input_protocol,
    requested_model, reasoning_effort, service_id, local_access_token_id,
    http_status, turn_index, input_preview,
    (SELECT COUNT(*) FROM request_records children
     WHERE children.parent_request_id = request_records.id), model_redirect_json, client_type, privacy_decision,
    output_json`

// sessionSummaryQuery reads the root turns of sessions, each session's in
// time order. Without statistics SQLite takes `parent_request_id IS NULL`,
// true of nearly every row, for the selective term and scans every root, so
// the session index is named. Its own order needs no sort.
func sessionSummaryQuery(sessions int) string {
	return `SELECT` + requestSessionSummaryColumns + `
FROM request_records INDEXED BY request_records_session_turns_idx
WHERE parent_request_id IS NULL AND COALESCE(session_id, id) IN (` + sessionPlaceholders(sessions) + `)
ORDER BY COALESCE(session_id, id), started_at ASC, id ASC`
}

// sessionRuntimeQuery reads every call of sessions, retries included, in no
// particular order; see sessionSummaryQuery for the index.
func sessionRuntimeQuery(sessions int) string {
	return `
SELECT COALESCE(root.session_id, root.id), call.started_at, call.completed_at,
       call.status, call.latency_ms, json_extract(call.error_json, '$.code'),
       root.id, root.turn_index, call.id, call.input_protocol, call.streaming, call.first_token_ms,
       json_extract(call.usage_json, '$.output_tokens'), json_extract(call.usage_json, '$.billing_incomplete')
FROM request_records root INDEXED BY request_records_session_turns_idx
JOIN request_records call ON call.id = root.id OR call.parent_request_id = root.id
WHERE root.parent_request_id IS NULL AND COALESCE(root.session_id, root.id) IN (` + sessionPlaceholders(sessions) + `)`
}

func sessionPlaceholders(count int) string {
	return strings.TrimSuffix(strings.Repeat("?,", count), ",")
}

func loadSessionSummaries(ctx context.Context, reader historyReader, items []sessionAggregate) (map[string]contract.RequestSession, error) {
	result := make(map[string]contract.RequestSession, len(items))
	if len(items) == 0 {
		return result, nil
	}
	args := make([]any, len(items))
	for i, item := range items {
		args[i] = item.id
	}
	rows, err := reader.QueryContext(ctx, sessionSummaryQuery(len(items)), args...)
	if err != nil {
		return nil, fmt.Errorf("list session summaries: %w", err)
	}
	defer rows.Close()
	var id, sessionID, startedAt, status, protocol string
	var completedAt, model, reasoning, service, token, preview, redirect, client, decision, output sql.NullString
	// The latest call's output, decoded once per session rather than per call.
	var latestOutput string
	var httpStatus, turn sql.NullInt64
	var children int
	var current contract.RequestSession
	var previousTurn sql.NullInt64
	var hadSuccess bool
	finish := func() error {
		if current.ID == "" {
			return nil
		}
		if current.Title == "" && current.RequestedModel != nil {
			current.Title = contract.ClampRunes(*current.RequestedModel, contract.MaxSessionTitleRunes)
		}
		if current.Status == contract.SessionStatusCancelled && hadSuccess {
			current.Status = contract.SessionStatusInterrupted
		}
		if latestOutput != "" {
			current.Output = &contract.OutputActivity{}
			if err := json.Unmarshal([]byte(latestOutput), current.Output); err != nil {
				return fmt.Errorf("%w: session output", storagecontract.ErrInvalidRecord)
			}
		}
		if err := current.Validate(); err != nil {
			return fmt.Errorf("%w: session summary: %v", storagecontract.ErrInvalidRecord, err)
		}
		result[string(current.ID)] = current
		return nil
	}
	for rows.Next() {
		if err := rows.Scan(&id, &sessionID, &startedAt, &completedAt, &status, &protocol,
			&model, &reasoning, &service, &token, &httpStatus, &turn, &preview,
			&children, &redirect, &client, &decision, &output); err != nil {
			return nil, err
		}
		if string(current.ID) != sessionID {
			if err := finish(); err != nil {
				return nil, err
			}
			current = contract.RequestSession{ID: contract.SessionID(sessionID)}
			previousTurn, hadSuccess = sql.NullInt64{}, false
		}
		started, err := time.Parse(time.RFC3339Nano, startedAt)
		if err != nil {
			return nil, fmt.Errorf("%w: session timestamp", storagecontract.ErrInvalidRecord)
		}
		record := contract.RequestRecord{ID: contract.RequestID(id), StartedAt: started,
			Status: contract.RequestStatus(status), InputProtocol: contract.ProtocolID(protocol),
			Audit: contract.NotCapturedAuditSummary(), ClientType: contract.ClientType(client.String)}
		if completedAt.Valid {
			completed, err := time.Parse(time.RFC3339Nano, completedAt.String)
			if err != nil {
				return nil, fmt.Errorf("%w: session completion", storagecontract.ErrInvalidRecord)
			}
			record.CompletedAt = &completed
		}
		if model.Valid {
			record.RequestedModel = &model.String
		}
		if reasoning.Valid {
			record.ReasoningEffort = &reasoning.String
		}
		if service.Valid {
			value := contract.ServiceID(service.String)
			record.ServiceID = &value
		}
		if token.Valid {
			value := contract.AccessTokenID(token.String)
			record.LocalAccessTokenID = &value
		}
		if httpStatus.Valid {
			value := int(httpStatus.Int64)
			record.HTTPStatus = &value
		}
		if preview.Valid {
			record.InputPreview = &preview.String
		}
		if turn.Valid {
			value := int(turn.Int64)
			record.TurnIndex = &value
		}
		if decision.Valid {
			value := contract.PrivacyDecision(decision.String)
			record.PrivacyDecision = &value
		}
		if redirect.Valid {
			record.ModelRedirect = &contract.RequestModelRedirect{}
			if err := json.Unmarshal([]byte(redirect.String), record.ModelRedirect); err != nil {
				return nil, fmt.Errorf("%w: session model redirect", storagecontract.ErrInvalidRecord)
			}
		}
		if err := record.Validate(); err != nil {
			return nil, fmt.Errorf("%w: session call: %v", storagecontract.ErrInvalidRecord, err)
		}
		if current.CallCount == 0 {
			current.StartedAt = started
		}
		if !turn.Valid || !previousTurn.Valid || turn.Int64 != previousTurn.Int64 {
			current.TurnCount++
		}
		previousTurn = turn
		current.CallCount += 1 + children
		if current.Title == "" && preview.String != "" {
			current.Title = contract.ClampRunes(preview.String, contract.MaxSessionTitleRunes)
		}
		current.LastStartedAt, current.CompletedAt = started, record.CompletedAt
		current.Status = contract.SessionStatusFromRequest(record.EffectiveStatus())
		hadSuccess = hadSuccess || record.EffectiveStatus() == contract.RequestStatusSucceeded
		current.InputProtocol, current.ClientType = record.InputProtocol, record.ClientType
		current.RequestedModel, current.ReasoningEffort = nil, nil
		// NullString is reused by Scan; retain independent values only for the
		// latest row instead of allocating and retaining every historical call.
		if model.Valid {
			value := model.String
			current.RequestedModel = &value
		}
		if reasoning.Valid {
			value := reasoning.String
			current.ReasoningEffort = &value
		}
		current.ServiceID, current.LocalAccessTokenID = record.ServiceID, record.LocalAccessTokenID
		current.ModelRedirect = record.ModelRedirect
		current.PrivacyWarned = current.PrivacyWarned || decision.String == string(contract.PrivacyDecisionWarn)
		latestOutput = output.String
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := finish(); err != nil {
		return nil, err
	}
	return result, nil
}

func (store *Store) listSessionTurns(ctx context.Context, sessionOrRequestID string) ([]contract.RequestRecord, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT`+requestRecordSelectColumns+`
FROM request_records
WHERE parent_request_id IS NULL AND COALESCE(session_id, id) = ?
ORDER BY started_at ASC, id ASC`, sessionOrRequestID)
	if err != nil {
		return nil, fmt.Errorf("list session turns: %w", err)
	}
	defer rows.Close()
	turns := make([]contract.RequestRecord, 0, 4)
	for rows.Next() {
		record, err := scanRequestRecord(rows)
		if err != nil {
			return nil, err
		}
		turns = append(turns, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate session turns: %w", err)
	}
	return turns, nil
}

func sessionFromTurns(turns []contract.RequestRecord) (contract.RequestSession, error) {
	if len(turns) == 0 {
		return contract.RequestSession{}, fmt.Errorf("%w: empty session", storagecontract.ErrInvalidRecord)
	}
	first := turns[0]
	latest := turns[len(turns)-1]
	id := contract.SessionID(latest.ID)
	if latest.SessionID != nil {
		id = *latest.SessionID
	} else if first.SessionID != nil {
		id = *first.SessionID
	}
	title := ""
	for _, turn := range turns {
		if turn.InputPreview != nil && *turn.InputPreview != "" {
			title = contract.ClampRunes(*turn.InputPreview, contract.MaxSessionTitleRunes)
			break
		}
	}
	if title == "" && latest.RequestedModel != nil && *latest.RequestedModel != "" {
		title = contract.ClampRunes(*latest.RequestedModel, contract.MaxSessionTitleRunes)
	}
	callCount := 0
	warned := false
	for _, turn := range turns {
		callCount += 1 + turn.ChildCount
		if turn.PrivacyDecision != nil && *turn.PrivacyDecision == contract.PrivacyDecisionWarn {
			warned = true
		}
	}
	session := contract.RequestSession{
		ClientType:         latest.ClientType,
		ID:                 id,
		Title:              title,
		StartedAt:          first.StartedAt,
		LastStartedAt:      latest.StartedAt,
		CompletedAt:        latest.CompletedAt,
		TurnCount:          countUserTurns(turns),
		CallCount:          callCount,
		Status:             sessionStatus(turns, latest),
		RequestedModel:     latest.RequestedModel,
		ModelRedirect:      latest.ModelRedirect,
		Output:             latest.Output,
		ReasoningEffort:    latest.ReasoningEffort,
		InputProtocol:      latest.InputProtocol,
		ServiceID:          latest.ServiceID,
		LocalAccessTokenID: latest.LocalAccessTokenID,
		PrivacyWarned:      warned,
	}
	if err := session.Validate(); err != nil {
		return contract.RequestSession{}, fmt.Errorf("%w: session %q: %v", storagecontract.ErrInvalidRecord, id, err)
	}
	return session, nil
}

// sessionStatus reports the conversation outcome from its last call, except
// that a trailing client abort cannot erase answers already delivered: an
// agent loop the operator stops after fifty successful calls is interrupted,
// not cancelled. Cancelled stays for a conversation that produced nothing.
// This mirrors the record-level rule in ingress, where a cancel never
// downgrades a call that already succeeded. A trailing failure or block is
// left alone, because those are what the operator needs to see first.
func sessionStatus(turns []contract.RequestRecord, latest contract.RequestRecord) contract.SessionStatus {
	status := contract.SessionStatusFromRequest(latest.EffectiveStatus())
	if status != contract.SessionStatusCancelled {
		return status
	}
	for _, turn := range turns {
		if turn.EffectiveStatus() == contract.RequestStatusSucceeded {
			return contract.SessionStatusInterrupted
		}
	}
	return status
}

// countUserTurns counts user turns from records in time order. Consecutive
// records sharing a turn_index are one turn (an agent loop); a record without
// a turn_index is its own turn, as every record was before turn_index existed.
// A turn_index that falls back to an earlier value also starts a new turn: the
// client trimmed its history, and the conversation moved on regardless.
func countUserTurns(turns []contract.RequestRecord) int {
	count := 0
	var previous *int
	for index, turn := range turns {
		switch {
		case index == 0, turn.TurnIndex == nil, previous == nil, *turn.TurnIndex != *previous:
			count++
		}
		previous = turn.TurnIndex
	}
	return count
}
