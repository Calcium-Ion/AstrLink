package sqlite

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
)

// gatewayRewriteColumns selects, for the root record named root, the provider
// a failover moved away from and what the gateway rewrote, in the order
// gatewayRewriteRow scans them. A failover is the newest failed attempt at
// another provider than the one that ended the call. The rewrites read the
// record's own fields; the global prompt, the reasoning level and the client
// identity come from the gateway_rewrite diagnostics.
func gatewayRewriteColumns(root string) string {
	return fmt.Sprintf(`CASE WHEN %[1]s.attempt_index > 1 THEN (
    SELECT failed.service_id FROM request_records AS failed
    WHERE failed.parent_request_id = %[1]s.id AND failed.service_id IS NOT %[1]s.service_id
    ORDER BY failed.attempt_index DESC LIMIT 1) END,
  NULLIF(%[1]s.model_redirect_json, '') IS NOT NULL
    OR COALESCE(NULLIF(json_extract(NULLIF(%[1]s.recovery_json, ''), '$.upstream_model'), ''), '')
      NOT IN ('', COALESCE(NULLIF(json_extract(NULLIF(%[1]s.model_redirect_json, ''), '$.to'), ''), %[1]s.requested_model, '')),
  COALESCE(%[1]s.privacy_decision = '%[2]s', 0),
  EXISTS (SELECT 1 FROM json_each(NULLIF(%[1]s.events_json, ''))
    WHERE json_extract(value, '$.kind') = '%[3]s'
      AND instr(COALESCE(json_extract(value, '$.summary'), ''), ' · 0/') = 0),
  COALESCE(json_extract(NULLIF(%[1]s.plan_json, ''), '$.plan_type') = '%[4]s'
    OR json_extract(NULLIF(%[1]s.plan_json, ''), '$.upstream_protocol') <> %[1]s.input_protocol, 0),
  (SELECT group_concat(json_extract(value, '$.path')) FROM json_each(NULLIF(%[1]s.conversion_diagnostics_json, ''))
    WHERE json_extract(value, '$.code') = '%[5]s')`,
		root, contract.PrivacyDecisionRedact, contract.RequestEventImageCompression, contract.PlanTypeRelayKit,
		contract.GatewayRewriteDiagnostic)
}

// noGatewayRewriteColumns stands in for gatewayRewriteColumns when a read
// does not need them.
const noGatewayRewriteColumns = `NULL, 0, 0, 0, 0, NULL`

// gatewayRewriteRow holds the columns of gatewayRewriteColumns.
type gatewayRewriteRow struct {
	failedService                         sql.NullString
	redirect, privacy, images, conversion bool
	noted                                 sql.NullString
}

func (row *gatewayRewriteRow) targets() []any {
	return []any{&row.failedService, &row.redirect, &row.privacy, &row.images, &row.conversion, &row.noted}
}

// rewrites lists what the gateway rewrote, in the order it applies them.
func (row gatewayRewriteRow) rewrites() []contract.GatewayRewrite {
	noted := strings.Split(row.noted.String, ",")
	has := func(rewrite contract.GatewayRewrite) bool {
		switch rewrite {
		case contract.GatewayRewriteModelRedirect:
			return row.redirect
		case contract.GatewayRewritePrivacy:
			return row.privacy
		case contract.GatewayRewriteImageCompression:
			return row.images
		case contract.GatewayRewriteConversion:
			return row.conversion
		default:
			for _, path := range noted {
				if path == string(rewrite) {
					return true
				}
			}
			return false
		}
	}
	rewrites := []contract.GatewayRewrite{}
	for _, rewrite := range contract.GatewayRewrites {
		if has(rewrite) {
			rewrites = append(rewrites, rewrite)
		}
	}
	return rewrites
}
