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

func (store *Store) FindSessionLink(ctx context.Context, cursor string) (contract.SessionID, error) {
	if cursor == "" || len([]rune(cursor)) > contract.MaxProtocolCursorRunes {
		return "", fmt.Errorf("%w: session cursor", storagecontract.ErrInvalidArgument)
	}
	var sessionID sql.NullString
	err := store.db.QueryRowContext(ctx, `
SELECT session_id FROM request_records
WHERE parent_request_id IS NULL
  AND session_id IS NOT NULL
  AND (output_response_id = ? OR previous_response_id = ?)
ORDER BY started_at DESC, id DESC
LIMIT 1`, cursor, cursor).Scan(&sessionID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("%w: session cursor", storagecontract.ErrNotFound)
		}
		return "", fmt.Errorf("lookup session link: %w", err)
	}
	if !sessionID.Valid || sessionID.String == "" {
		return "", fmt.Errorf("%w: session cursor", storagecontract.ErrNotFound)
	}
	id := contract.SessionID(sessionID.String)
	if err := id.Validate(); err != nil {
		return "", fmt.Errorf("%w: session %q", storagecontract.ErrInvalidRecord, sessionID.String)
	}
	return id, nil
}

func (store *Store) ListRequestSessions(
	ctx context.Context,
	options storagecontract.RequestSessionListOptions,
) (storagecontract.RequestSessionPage, error) {
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
	if options.From != nil {
		query.WriteString(` AND started_at >= ?`)
		args = append(args, options.From.UTC().Format(time.RFC3339Nano))
	}
	if options.To != nil {
		query.WriteString(` AND started_at < ?`)
		args = append(args, options.To.UTC().Format(time.RFC3339Nano))
	}
	if options.LocalAccessTokenID != nil {
		query.WriteString(` AND local_access_token_id = ?`)
		args = append(args, string(*options.LocalAccessTokenID))
	}
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
	logSlowRequestList("request sessions list slow", started)

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
	for _, item := range matched {
		session, err := store.loadSessionSummary(ctx, item)
		if err != nil {
			logRequestListFailure("request sessions list failed", err)
			return storagecontract.RequestSessionPage{}, err
		}
		page.Items = append(page.Items, session)
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
	return contract.RequestSessionDetail{RequestSession: session, Turns: turns}, nil
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

func (store *Store) loadSessionSummary(ctx context.Context, item sessionAggregate) (contract.RequestSession, error) {
	turns, err := store.listSessionTurns(ctx, item.id)
	if err != nil {
		return contract.RequestSession{}, err
	}
	if len(turns) == 0 {
		return contract.RequestSession{}, fmt.Errorf("%w: session %q", storagecontract.ErrNotFound, item.id)
	}
	return sessionFromTurns(turns)
}

func (store *Store) listSessionTurns(ctx context.Context, sessionOrRequestID string) ([]contract.RequestRecord, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT`+requestRecordSelectColumns+`
FROM request_records
WHERE parent_request_id IS NULL AND (session_id = ? OR (session_id IS NULL AND id = ?))
ORDER BY started_at ASC, id ASC`, sessionOrRequestID, sessionOrRequestID)
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
		TurnCount:          len(turns),
		CallCount:          callCount,
		Status:             latest.Status,
		RequestedModel:     latest.RequestedModel,
		InputProtocol:      latest.InputProtocol,
		ServiceID:          latest.ServiceID,
		LocalAccessTokenID: latest.LocalAccessTokenID,
	}
	if err := session.Validate(); err != nil {
		return contract.RequestSession{}, fmt.Errorf("%w: session %q: %v", storagecontract.ErrInvalidRecord, id, err)
	}
	return session, nil
}
