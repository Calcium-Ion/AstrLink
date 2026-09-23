package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
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
	placeholders := strings.TrimSuffix(strings.Repeat("?, ", len(cleaned)), ", ")
	valueArgs := make([]any, 0, len(cleaned))
	for _, value := range cleaned {
		valueArgs = append(valueArgs, value)
	}

	scopeClause := strings.Builder{}
	scopeArgs := make([]any, 0, 2)
	if scope.SamePrincipal {
		scopeClause.WriteString(` AND r.local_access_token_id IS ?`)
		if scope.LocalAccessTokenID != nil {
			scopeArgs = append(scopeArgs, string(*scope.LocalAccessTokenID))
		} else {
			scopeArgs = append(scopeArgs, nil)
		}
	}
	if !scope.NotBefore.IsZero() {
		scopeClause.WriteString(` AND r.started_at >= ?`)
		scopeArgs = append(scopeArgs, scope.NotBefore.UTC().Format(time.RFC3339Nano))
	}

	// The record that produced a value (direction out / output_response_id)
	// is the true anchor and carries the turn the continuation builds on;
	// records that merely named the same value (direction in) only serve
	// client-chosen ids such as prompt_cache_key that no response emits.
	query := strings.Builder{}
	args := make([]any, 0, 5*len(cleaned)+2*len(scopeArgs)+1)
	query.WriteString(`SELECT session_id, turn_index, turn_user_messages, turn_user_fingerprint, value FROM (
SELECT r.session_id, r.turn_index, r.turn_user_messages, r.turn_user_fingerprint, c.value AS value,
       CASE WHEN c.direction = 'out' THEN 0 ELSE 1 END AS priority,
       r.started_at, r.id
FROM request_record_cursors c
JOIN request_records r ON r.id = c.request_id
WHERE c.kind = ? AND c.value IN (` + placeholders + `)`)
	args = append(args, string(kind))
	args = append(args, valueArgs...)
	if kind != contract.SessionCursorExplicit {
		query.WriteString(` AND c.direction = 'out'`)
	}
	query.WriteString(` AND r.parent_request_id IS NULL AND r.session_id IS NOT NULL`)
	query.WriteString(scopeClause.String())
	args = append(args, scopeArgs...)
	if kind == contract.SessionCursorExplicit {
		// Separate indexed lookups avoid an OR that makes SQLite scan all roots.
		for priority, column := range []string{"output_response_id", "previous_response_id"} {
			query.WriteString(`
UNION ALL
SELECT r.session_id, r.turn_index, r.turn_user_messages, r.turn_user_fingerprint,
       r.` + column + ` AS value, ? AS priority, r.started_at, r.id
FROM request_records r
WHERE r.` + column + ` IN (` + placeholders + `)
  AND r.parent_request_id IS NULL AND r.session_id IS NOT NULL`)
			args = append(args, priority)
			args = append(args, valueArgs...)
			query.WriteString(scopeClause.String())
			args = append(args, scopeArgs...)
		}
	}
	query.WriteString(`
) ORDER BY priority ASC, started_at DESC, id DESC LIMIT 1`)

	var (
		sessionID, turnUserFingerprint, value sql.NullString
		turnIndex, turnUserMessages           sql.NullInt64
	)
	err := store.db.QueryRowContext(ctx, query.String(), args...).Scan(
		&sessionID, &turnIndex, &turnUserMessages, &turnUserFingerprint, &value,
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
	match := storagecontract.SessionLinkMatch{SessionID: id, Value: value.String}
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

	query := strings.Builder{}
	query.WriteString(`
SELECT
  sid,
  MIN(started_at) AS started_at,
  MAX(started_at) AS last_started_at,
  COUNT(*) AS turn_count
FROM (
  SELECT
    COALESCE(session_id, id) AS sid,
    started_at
  FROM request_records
  WHERE parent_request_id IS NULL`)
	args := make([]any, 0, 16)
	// Classify before LIMIT so discovery traffic cannot consume call-list pages.
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
	query.WriteString(`
) grouped
GROUP BY sid`)
	if options.Cursor != "" {
		query.WriteString(` HAVING last_started_at < ? OR (last_started_at = ? AND sid < ?)`)
		args = append(args, cursorStarted, cursorStarted, cursorID)
	}
	query.WriteString(` ORDER BY last_started_at DESC, sid DESC LIMIT ?`)
	args = append(args, limit+1)

	started := time.Now()
	defer logSlowRequestList("request sessions list slow", started)
	rows, err := store.db.QueryContext(ctx, query.String(), args...)
	if err != nil {
		logRequestListFailure("request sessions list failed", err)
		return storagecontract.RequestSessionPage{}, fmt.Errorf("list request sessions: %w", err)
	}
	defer rows.Close()

	matched := make([]sessionAggregate, 0, limit+1)
	for rows.Next() {
		var item sessionAggregate
		if err := rows.Scan(&item.id, &item.startedAt, &item.lastStartedAt, &item.turnCount); err != nil {
			logRequestListFailure("request sessions list failed", err)
			return storagecontract.RequestSessionPage{}, err
		}
		matched = append(matched, item)
		if len(matched) == limit+1 {
			break
		}
	}
	if err := rows.Err(); err != nil {
		logRequestListFailure("request sessions list failed", err)
		return storagecontract.RequestSessionPage{}, fmt.Errorf("iterate request sessions: %w", err)
	}
	if err := rows.Close(); err != nil {
		return storagecontract.RequestSessionPage{}, err
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
	turnsBySession, err := store.loadSessionSummaries(ctx, matched)
	if err != nil {
		return storagecontract.RequestSessionPage{}, err
	}
	for _, item := range matched {
		session, err := sessionFromTurns(turnsBySession[item.id])
		if err != nil {
			logRequestListFailure("request sessions list failed", err)
			return storagecontract.RequestSessionPage{}, err
		}
		page.Items = append(page.Items, session)
	}
	if err := store.loadSessionRuntimes(ctx, page.Items); err != nil {
		return storagecontract.RequestSessionPage{}, err
	}
	return page, nil
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
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(args)), ",")
	rows, err := store.db.QueryContext(ctx, `
SELECT COALESCE(root.session_id, root.id), call.started_at, call.completed_at,
       call.status, call.latency_ms, json_extract(call.error_json, '$.code'),
       root.id, root.turn_index, call.id, call.input_protocol, call.streaming, call.first_token_ms,
       json_extract(call.usage_json, '$.output_tokens'), json_extract(call.usage_json, '$.billing_incomplete')
FROM request_records root
JOIN request_records call ON call.id = root.id OR call.parent_request_id = root.id
WHERE root.parent_request_id IS NULL AND COALESCE(root.session_id, root.id) IN (`+placeholders+`)
ORDER BY call.started_at, call.id`, args...)
	if err != nil {
		return fmt.Errorf("load session runtimes: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, startedAt, status string
		var completedAt, errorCode sql.NullString
		var latency sql.NullInt64
		var rootID, callID, protocol string
		var turn, firstToken, outputTokens sql.NullInt64
		var streaming bool
		var incomplete sql.NullBool
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
	for id, stats := range performance {
		stats.apply(byID[id])
	}
	return rows.Err()
}

type sessionAggregate struct {
	id            string
	startedAt     string
	lastStartedAt string
	turnCount     int
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

// Reuse the record decoder to validate summary fields, while omitting large
// detail-only JSON and cursor lookups. The full projection is used on expansion.
const requestSessionSummaryColumns = `
    id, NULL, attempt_index, started_at, completed_at, status, input_protocol,
    requested_model, reasoning_effort, streaming, NULL, service_id, local_access_token_id, NULL,
    http_status, NULL, NULL, NULL, '{}', NULL,
    session_id, NULL, NULL, input_preview, NULL, created_at,
    turn_index, NULL, NULL, NULL, NULL,
    (SELECT COUNT(*) FROM request_records children
     WHERE children.parent_request_id = request_records.id), NULL, NULL`

func (store *Store) loadSessionSummaries(ctx context.Context, items []sessionAggregate) (map[string][]contract.RequestRecord, error) {
	result := make(map[string][]contract.RequestRecord, len(items))
	if len(items) == 0 {
		return result, nil
	}
	args := make([]any, len(items))
	for i, item := range items {
		args[i] = item.id
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(items)), ",")
	rows, err := store.db.QueryContext(ctx, `SELECT`+requestSessionSummaryColumns+`
FROM request_records
WHERE parent_request_id IS NULL AND COALESCE(session_id, id) IN (`+placeholders+`)
ORDER BY started_at ASC, id ASC`, args...)
	if err != nil {
		return nil, fmt.Errorf("list session summaries: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		record, err := scanRequestRecord(rows)
		if err != nil {
			return nil, err
		}
		id := string(record.ID)
		if record.SessionID != nil {
			id = string(*record.SessionID)
		}
		result[id] = append(result[id], record)
	}
	return result, rows.Err()
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
	title := "未命名会话"
	for _, turn := range turns {
		if turn.InputPreview != nil && *turn.InputPreview != "" {
			title = contract.ClampRunes(*turn.InputPreview, contract.MaxSessionTitleRunes)
			break
		}
	}
	if title == "未命名会话" && latest.RequestedModel != nil && *latest.RequestedModel != "" {
		title = contract.ClampRunes(*latest.RequestedModel, contract.MaxSessionTitleRunes)
	}
	callCount := 0
	for _, turn := range turns {
		callCount += 1 + turn.ChildCount
	}
	session := contract.RequestSession{
		ID:                 id,
		Title:              title,
		StartedAt:          first.StartedAt,
		LastStartedAt:      latest.StartedAt,
		CompletedAt:        latest.CompletedAt,
		TurnCount:          countUserTurns(turns),
		CallCount:          callCount,
		Status:             sessionStatus(turns, latest),
		RequestedModel:     latest.RequestedModel,
		ReasoningEffort:    latest.ReasoningEffort,
		InputProtocol:      latest.InputProtocol,
		ServiceID:          latest.ServiceID,
		LocalAccessTokenID: latest.LocalAccessTokenID,
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
