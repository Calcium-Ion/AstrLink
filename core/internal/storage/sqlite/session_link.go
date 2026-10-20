package sqlite

import (
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	storagecontract "github.com/QuantumNous/astrlink/core/internal/storage"
)

// Each value/direction branch walks an ordered index and stops at its first
// eligible root. The final sort has at most four candidates per input value,
// regardless of how many calls have reused a conversation id. Keep producers
// ahead of consumers even when a consumer is newer, including legacy columns.
func sessionLinkQuery(kind contract.SessionCursorKind, values []string, scope storagecontract.SessionCursorScope) (string, []any) {
	const fields = `r.session_id, r.turn_index, r.turn_user_messages, r.turn_user_fingerprint, r.status, r.started_at, r.id`
	branches := make([]string, 0, 4*len(values))
	args := make([]any, 0, 16*len(values))
	directions := []string{"out"}
	if kind == contract.SessionCursorExplicit {
		directions = append(directions, "in")
	}
	for _, value := range values {
		for priority, direction := range directions {
			index := "request_record_cursors_latest_idx"
			if scope.SamePrincipal {
				index = "request_record_cursors_principal_latest_idx"
			}
			branch := `SELECT * FROM (SELECT ` + fields + `, c.value AS value, ? AS priority
FROM request_record_cursors c INDEXED BY ` + index + `
CROSS JOIN request_records r ON r.id = c.request_id
WHERE c.value = ? AND c.kind = ? AND c.direction = ?
  AND r.parent_request_id IS NULL AND r.session_id IS NOT NULL`
			args = append(args, priority, value, string(kind), direction)
			clause, scoped := sessionLinkScope("c", scope)
			branch += clause + ` ORDER BY c.started_at DESC, c.request_id DESC LIMIT 1)`
			args = append(args, scoped...)
			branches = append(branches, branch)
		}
		if kind != contract.SessionCursorExplicit {
			continue
		}
		for priority, column := range []string{"output_response_id", "previous_response_id"} {
			index := "request_records_output_latest_idx"
			if priority == 1 {
				index = "request_records_previous_latest_idx"
			}
			if scope.SamePrincipal {
				index = strings.TrimSuffix(index, "_latest_idx") + "_principal_latest_idx"
			}
			branch := `SELECT * FROM (SELECT ` + fields + `, r.` + column + ` AS value, ? AS priority
FROM request_records r INDEXED BY ` + index + `
WHERE r.` + column + ` = ? AND r.parent_request_id IS NULL AND r.session_id IS NOT NULL`
			args = append(args, priority, value)
			clause, scoped := sessionLinkScope("r", scope)
			branch += clause + ` ORDER BY r.started_at DESC, r.id DESC LIMIT 1)`
			args = append(args, scoped...)
			branches = append(branches, branch)
		}
	}
	return `SELECT session_id, turn_index, turn_user_messages, turn_user_fingerprint, value, status
FROM (` + strings.Join(branches, " UNION ALL ") + `)
ORDER BY priority ASC, started_at DESC, id DESC LIMIT 1`, args
}

func sessionLinkScope(alias string, scope storagecontract.SessionCursorScope) (string, []any) {
	var clause strings.Builder
	args := make([]any, 0, 2)
	if scope.SamePrincipal {
		clause.WriteString(" AND " + alias + ".local_access_token_id IS ?")
		args = append(args, scope.LocalAccessTokenID)
	}
	if !scope.NotBefore.IsZero() {
		clause.WriteString(" AND " + alias + ".started_at >= ?")
		args = append(args, scope.NotBefore.UTC().Format(time.RFC3339Nano))
	}
	return clause.String(), args
}
