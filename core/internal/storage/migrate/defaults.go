package migrate

// DefaultMigrations stores versioned configuration while keeping recoverable
// secrets out of generic JSON documents. Provider credentials and local access
// token values live only in their dedicated secret tables.
func DefaultMigrations() []Migration {
	return []Migration{
		{
			Version: 1,
			Name:    "initial_configuration",
			Statements: []string{
				`CREATE TABLE endpoints (
    id TEXT PRIMARY KEY,
    document_json TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
)`,
				`CREATE TABLE routes (
    id TEXT PRIMARY KEY,
    document_json TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
)`,
				`CREATE TABLE policies (
    id TEXT PRIMARY KEY,
    document_json TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
)`,
			},
		},
		{
			Version: 2,
			Name:    "local_endpoint_credentials",
			Statements: []string{
				`CREATE TABLE endpoint_credentials (
    endpoint_id TEXT PRIMARY KEY REFERENCES endpoints(id) ON DELETE CASCADE,
    credential_value BLOB NOT NULL CHECK(length(credential_value) BETWEEN 1 AND 16384),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
)`,
			},
		},
		{
			Version: 3,
			Name:    "persistent_local_access_tokens",
			Statements: []string{
				`CREATE TABLE local_access_tokens (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL CHECK(length(name) BETWEEN 1 AND 64),
    name_key TEXT NOT NULL UNIQUE CHECK(length(name_key) BETWEEN 1 AND 64),
    token_hash BLOB NOT NULL UNIQUE CHECK(length(token_hash) = 32),
    token_hint TEXT NOT NULL CHECK(length(token_hint) BETWEEN 7 AND 32),
    source TEXT NOT NULL CHECK(source IN ('system_default', 'user')),
    created_at TEXT NOT NULL
)`,
				`CREATE TABLE local_access_token_secrets (
    token_id TEXT PRIMARY KEY REFERENCES local_access_tokens(id) ON DELETE CASCADE,
    token_value TEXT NOT NULL CHECK(length(token_value) = 48)
)`,
				`CREATE TABLE local_access_token_bootstrap_state (
    singleton INTEGER PRIMARY KEY CHECK(singleton = 1),
    completed_at TEXT NOT NULL
)`,
			},
		},
		{
			Version: 4,
			Name:    "default_privacy_policy",
			Statements: []string{
				`INSERT OR IGNORE INTO policies (id, document_json, created_at, updated_at)
VALUES (
    'policy_privacy_default',
    '{"id":"policy_privacy_default","name":"隐私保护","enabled":false,"priority":0,"detector":"regex","min_confidence":0.6,"match":{},"request_action":"redact","response_action":"allow","response_restore":true}',
    strftime('%Y-%m-%dT%H:%M:%fZ', 'now'),
    strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
)`,
			},
		},
		{
			Version: 5,
			Name:    "selectable_local_privacy_models",
			Statements: []string{
				`CREATE TABLE privacy_model_installations (
    id TEXT PRIMARY KEY,
    document_json TEXT NOT NULL,
    manifest_identity TEXT,
    manifest_sha256 TEXT,
    manifest_json TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    CHECK (
        (manifest_identity IS NULL AND manifest_sha256 IS NULL AND manifest_json IS NULL)
        OR
        (manifest_identity IS NOT NULL AND manifest_sha256 IS NOT NULL AND manifest_json IS NOT NULL)
    )
)`,
				`UPDATE policies
SET document_json = json_set(
    json_set(
        document_json,
        '$.detector',
        CASE json_extract(document_json, '$.detector')
            WHEN 'openai_privacy_filter' THEN 'local_model'
            ELSE json_extract(document_json, '$.detector')
        END
    ),
    '$.local_model_id',
    CASE json_extract(document_json, '$.detector')
        WHEN 'openai_privacy_filter' THEN 'model_de5ac42e03b4af887b31a7645d3ce111'
        ELSE NULL
    END
)
WHERE id = 'policy_privacy_default'`,
			},
		},
		{
			Version: 6,
			Name:    "privacy_model_display_metadata",
			Statements: []string{
				`UPDATE privacy_model_installations
SET document_json = json_set(
    document_json,
    '$.catalog_source',
    CASE json_extract(document_json, '$.catalog_id')
        WHEN 'catalog_openai_privacy_filter' THEN 'official'
        WHEN 'catalog_sheltron_ettin_32m' THEN 'community'
        WHEN 'catalog_nym_pii_multilingual_small' THEN 'community'
        ELSE NULL
    END,
    '$.license',
    CASE json_extract(document_json, '$.catalog_id')
        WHEN 'catalog_openai_privacy_filter' THEN 'Apache-2.0'
        WHEN 'catalog_sheltron_ettin_32m' THEN 'Apache-2.0'
        WHEN 'catalog_nym_pii_multilingual_small' THEN 'MIT'
        ELSE json_extract(document_json, '$.license')
    END,
    '$.languages',
    CASE json_extract(document_json, '$.catalog_id')
        WHEN 'catalog_openai_privacy_filter' THEN json('["en"]')
        WHEN 'catalog_sheltron_ettin_32m' THEN json('["en"]')
        WHEN 'catalog_nym_pii_multilingual_small' THEN json('["multilingual","cjk"]')
        ELSE json(COALESCE(json_extract(document_json, '$.languages'), '[]'))
    END
)`,
			},
		},
		{
			Version: 7,
			Name:    "privacy_response_restore",
			Statements: []string{
				`UPDATE policies
SET document_json = json_set(document_json, '$.response_restore', json('true'))
WHERE id = 'policy_privacy_default'
  AND json_extract(document_json, '$.response_restore') IS NULL`,
			},
		},
		{
			Version: 8,
			Name:    "request_records_metadata",
			Statements: []string{
				`CREATE TABLE request_records (
    id TEXT PRIMARY KEY,
    started_at TEXT NOT NULL,
    completed_at TEXT,
    status TEXT NOT NULL CHECK(status IN ('pending', 'succeeded', 'failed', 'cancelled', 'blocked')),
    input_protocol TEXT NOT NULL,
    requested_model TEXT,
    streaming INTEGER NOT NULL CHECK(streaming IN (0, 1)),
    route_id TEXT,
    endpoint_id TEXT,
    local_access_token_id TEXT,
    plan_json TEXT,
    http_status INTEGER,
    latency_ms INTEGER,
    usage_json TEXT,
    error_json TEXT,
    audit_json TEXT NOT NULL,
    created_at TEXT NOT NULL
)`,
				`CREATE INDEX request_records_started_at_idx ON request_records (started_at)`,
				`CREATE INDEX request_records_endpoint_id_idx ON request_records (endpoint_id)`,
			},
		},
		{
			Version: 9,
			Name:    "opt_in_encrypted_body_audit",
			Statements: []string{
				`CREATE TABLE audit_settings (
    id INTEGER PRIMARY KEY CHECK(id = 1),
    request_body_enabled INTEGER NOT NULL CHECK(request_body_enabled IN (0, 1)),
    response_content_enabled INTEGER NOT NULL CHECK(response_content_enabled IN (0, 1)),
    request_body_max_bytes INTEGER NOT NULL,
    response_content_max_bytes INTEGER NOT NULL,
    metadata_retention_days INTEGER NOT NULL,
    content_retention_days INTEGER NOT NULL,
    extensions_json TEXT,
    updated_at TEXT NOT NULL
)`,
				`INSERT INTO audit_settings (
    id, request_body_enabled, response_content_enabled,
    request_body_max_bytes, response_content_max_bytes,
    metadata_retention_days, content_retention_days, extensions_json, updated_at
) VALUES (
    1, 0, 0, 1048576, 4194304, 30, 7, NULL,
    strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
)`,
				`CREATE TABLE audit_keys (
    id INTEGER PRIMARY KEY CHECK(id = 1),
    key_bytes BLOB NOT NULL CHECK(length(key_bytes) = 32),
    created_at TEXT NOT NULL
)`,
				`CREATE TABLE audit_blobs (
    request_id TEXT NOT NULL REFERENCES request_records(id) ON DELETE CASCADE,
    direction TEXT NOT NULL CHECK(direction IN ('request', 'response')),
    media_type TEXT NOT NULL,
    nonce BLOB NOT NULL,
    ciphertext BLOB NOT NULL,
    truncated INTEGER NOT NULL CHECK(truncated IN (0, 1)),
    captured_bytes INTEGER NOT NULL CHECK(captured_bytes >= 0),
    created_at TEXT NOT NULL,
    UNIQUE(request_id, direction)
)`,
				`CREATE INDEX audit_blobs_created_at_idx ON audit_blobs (created_at)`,
			},
		},
		{
			Version: 10,
			Name:    "http_metadata_capture",
			// SQLite cannot ALTER a CHECK constraint, so audit_blobs is
			// rebuilt to accept the http_meta direction (ADR 0008). The
			// runner executes migrations in one transaction, so a partial
			// rebuild is never observable.
			Statements: []string{
				`CREATE TABLE audit_blobs_new (
    request_id TEXT NOT NULL REFERENCES request_records(id) ON DELETE CASCADE,
    direction TEXT NOT NULL CHECK(direction IN ('request', 'response', 'http_meta')),
    media_type TEXT NOT NULL,
    nonce BLOB NOT NULL,
    ciphertext BLOB NOT NULL,
    truncated INTEGER NOT NULL CHECK(truncated IN (0, 1)),
    captured_bytes INTEGER NOT NULL CHECK(captured_bytes >= 0),
    created_at TEXT NOT NULL,
    UNIQUE(request_id, direction)
)`,
				`INSERT INTO audit_blobs_new SELECT * FROM audit_blobs`,
				`DROP TABLE audit_blobs`,
				`ALTER TABLE audit_blobs_new RENAME TO audit_blobs`,
				`CREATE INDEX audit_blobs_created_at_idx ON audit_blobs (created_at)`,
				`ALTER TABLE audit_settings
    ADD COLUMN http_meta_enabled INTEGER NOT NULL DEFAULT 1 CHECK(http_meta_enabled IN (0, 1))`,
			},
		},
		{
			Version: 11,
			Name:    "subscription_accounts",
			Statements: []string{
				`CREATE TABLE subscription_accounts (
    id TEXT PRIMARY KEY,
    document_json TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
)`,
			},
		},
		{
			Version: 12,
			Name:    "unified_api_services",
			Statements: []string{
				`CREATE TABLE services (
    id TEXT PRIMARY KEY,
    document_json TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
)`,
				`INSERT INTO services (id, document_json, created_at, updated_at)
SELECT
    id,
    json_object(
        'id', id,
        'name', json_extract(document_json, '$.name'),
        'kind', json_extract(document_json, '$.kind'),
        'enabled', json(CASE WHEN json_extract(document_json, '$.enabled') THEN 'true' ELSE 'false' END),
        'capabilities', json(COALESCE(json_extract(document_json, '$.capabilities'), '[]')),
        'http', json_object(
            'base_url', json_extract(document_json, '$.base_url'),
            'auth', json(COALESCE(json_extract(document_json, '$.auth'), '{}')),
            'credential_ref', replace(
                COALESCE(json_extract(document_json, '$.credential_ref'), ''),
                'local://endpoint/',
                'local://service/'
            )
        ),
        'created_at', created_at,
        'updated_at', updated_at
    ),
    created_at,
    updated_at
FROM endpoints`,
				`INSERT INTO services (id, document_json, created_at, updated_at)
SELECT
    id,
    json_object(
        'id', id,
        'name', json_extract(document_json, '$.display_name'),
        'kind', 'codex_subscription',
        'enabled', json('true'),
        'capabilities', json(COALESCE(json_extract(document_json, '$.capabilities'), '[]')),
        'subscription', json_object(
            'provider', json_extract(document_json, '$.provider'),
            'status', json_extract(document_json, '$.status'),
            'account_hint', COALESCE(json_extract(document_json, '$.account_hint'), ''),
            'provider_account_id', COALESCE(json_extract(document_json, '$.provider_account_id'), ''),
            'credential_ref', COALESCE(json_extract(document_json, '$.credential_ref'), ''),
            'authorization_boundary', COALESCE(json_extract(document_json, '$.authorization_boundary'), ''),
            'token_expires_at', json_extract(document_json, '$.token_expires_at'),
            'last_refresh_at', json_extract(document_json, '$.last_refresh_at'),
            'last_error', json_extract(document_json, '$.last_error')
        ),
        'created_at', COALESCE(json_extract(document_json, '$.created_at'), created_at),
        'updated_at', COALESCE(json_extract(document_json, '$.updated_at'), updated_at)
    ),
    created_at,
    updated_at
FROM subscription_accounts
WHERE json_extract(document_json, '$.status') IN ('connected', 'needs_reauth')
  AND COALESCE(json_extract(document_json, '$.credential_ref'), '') <> ''`,
				`CREATE TABLE service_credentials (
    service_id TEXT PRIMARY KEY REFERENCES services(id) ON DELETE CASCADE,
    credential_value BLOB NOT NULL CHECK(length(credential_value) BETWEEN 1 AND 16384),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
)`,
				`INSERT INTO service_credentials (service_id, credential_value, created_at, updated_at)
SELECT endpoint_id, credential_value, created_at, updated_at
FROM endpoint_credentials`,
				`DROP TABLE endpoint_credentials`,
				`DROP TABLE endpoints`,
				`DROP TABLE subscription_accounts`,
				`DROP INDEX request_records_endpoint_id_idx`,
				`ALTER TABLE request_records RENAME COLUMN endpoint_id TO service_id`,
				`CREATE INDEX request_records_service_id_idx ON request_records (service_id)`,
			},
		},
		{
			Version: 13,
			Name:    "privacy_restore_diagnostics",
			Statements: []string{
				`ALTER TABLE request_records ADD COLUMN privacy_restore_json TEXT`,
			},
		},
		{
			Version: 14,
			Name:    "independent_upstream_attempt_records",
			// Roots keep a stable client-call id; failed retries are demoted to
			// independent child rows. Audit directions expand for per-attempt
			// upstream request/response/http_meta capture.
			Statements: []string{
				`ALTER TABLE request_records ADD COLUMN parent_request_id TEXT REFERENCES request_records(id) ON DELETE CASCADE`,
				`ALTER TABLE request_records ADD COLUMN attempt_index INTEGER NOT NULL DEFAULT 1 CHECK(attempt_index >= 0)`,
				`CREATE INDEX request_records_parent_id_idx ON request_records (parent_request_id)`,
				`CREATE INDEX request_records_parent_attempt_idx ON request_records (parent_request_id, attempt_index)`,
				`CREATE TABLE audit_blobs_new (
    request_id TEXT NOT NULL REFERENCES request_records(id) ON DELETE CASCADE,
    direction TEXT NOT NULL CHECK(direction IN (
        'request', 'response', 'http_meta',
        'upstream_request', 'upstream_response', 'upstream_http_meta'
    )),
    media_type TEXT NOT NULL,
    nonce BLOB NOT NULL,
    ciphertext BLOB NOT NULL,
    truncated INTEGER NOT NULL CHECK(truncated IN (0, 1)),
    captured_bytes INTEGER NOT NULL CHECK(captured_bytes >= 0),
    created_at TEXT NOT NULL,
    UNIQUE(request_id, direction)
)`,
				`INSERT INTO audit_blobs_new SELECT * FROM audit_blobs`,
				`DROP TABLE audit_blobs`,
				`ALTER TABLE audit_blobs_new RENAME TO audit_blobs`,
				`CREATE INDEX audit_blobs_created_at_idx ON audit_blobs (created_at)`,
			},
		},
		{
			Version: 15,
			Name:    "service_level_model_allowlists",
			Statements: []string{
				`UPDATE services
SET document_json = json_set(
    document_json,
    '$.models', json('[]'),
    '$.capabilities', json(COALESCE((
        SELECT json_group_array(json(json_remove(value, '$.models')))
        FROM json_each(services.document_json, '$.capabilities')
    ), '[]'))
)`,
				`UPDATE services
SET document_json = json_set(
    document_json,
    '$.capabilities',
    json('[{"protocol":"openai.responses","mode":"native","streaming":true},{"protocol":"openai.responses.compact","mode":"native","streaming":false},{"protocol":"openai.models","mode":"native","streaming":false}]')
)
WHERE json_extract(document_json, '$.kind') = 'codex_subscription'`,
			},
		},
		{
			Version: 16,
			Name:    "privacy_policy_custom_regex_rules",
			Statements: []string{
				`UPDATE policies
SET document_json = json_set(
    document_json,
    '$.regex_source',
    COALESCE(json_extract(document_json, '$.regex_source'), 'builtin'),
    '$.custom_regex_rules',
    COALESCE(json_extract(document_json, '$.custom_regex_rules'), json('[]'))
)
WHERE id = 'policy_privacy_default'`,
			},
		},
		{
			Version: 17,
			Name:    "merge_passthrough_capability_modes",
			Statements: []string{
				`UPDATE services
SET document_json = json_set(
    document_json,
    '$.capabilities',
    json(COALESCE((
        SELECT json_group_array(json(json_set(value, '$.mode', 'native')))
        FROM (
            SELECT MIN(value) AS value
            FROM json_each(services.document_json, '$.capabilities')
            GROUP BY json_extract(value, '$.protocol')
        )
    ), '[]'))
)`,
				`UPDATE routes
SET document_json = json_set(
    document_json,
    '$.targets',
    json((
        SELECT json_group_array(
            json(
                CASE json_extract(value, '$.plan_type')
                    WHEN 'delegated' THEN json_set(value, '$.plan_type', 'native')
                    ELSE value
                END
            )
        )
        FROM json_each(routes.document_json, '$.targets')
    ))
)
WHERE json_type(document_json, '$.targets') = 'array'`,
			},
		},
		{
			Version: 18,
			Name:    "request_session_trajectory",
			Statements: []string{
				`ALTER TABLE request_records ADD COLUMN session_id TEXT`,
				`ALTER TABLE request_records ADD COLUMN previous_response_id TEXT`,
				`ALTER TABLE request_records ADD COLUMN output_response_id TEXT`,
				`ALTER TABLE request_records ADD COLUMN input_preview TEXT`,
				`ALTER TABLE request_records ADD COLUMN events_json TEXT`,
				`CREATE INDEX request_records_session_id_idx ON request_records (session_id)`,
				`CREATE INDEX request_records_output_response_id_idx ON request_records (output_response_id)`,
			},
		},
		{
			Version: 19,
			Name:    "privacy_allowlist_and_restore_defaults",
			// kind_rules is deliberately not seeded here: an absent list is
			// filled from the code defaults on read, so the shipped per-kind
			// defaults stay in one place instead of being duplicated in SQL.
			//
			// The allowlist cannot work that way, because an empty list has to
			// keep meaning "allow nothing" for an operator who cleared it.
			//
			// The booleans are written explicitly because absent and false are
			// indistinguishable after JSON decoding, and an install that left
			// them absent would silently lose tool-argument restoration.
			Statements: []string{
				`UPDATE policies
SET document_json = json_set(
    document_json,
    '$.allowlist_rules',
    COALESCE(json_extract(document_json, '$.allowlist_rules'), json('[
        {"type":"domain_suffix","value":"localhost"},
        {"type":"domain_suffix","value":"github.com"},
        {"type":"domain_suffix","value":"githubusercontent.com"},
        {"type":"cidr","value":"127.0.0.0/8"},
        {"type":"cidr","value":"::1/128"},
        {"type":"cidr","value":"10.0.0.0/8"},
        {"type":"cidr","value":"172.16.0.0/12"},
        {"type":"cidr","value":"192.168.0.0/16"},
        {"type":"cidr","value":"169.254.0.0/16"}
    ]')),
    '$.restore_tool_arguments',
    json(CASE
        WHEN json_extract(document_json, '$.restore_tool_arguments') IS NULL THEN 'true'
        WHEN json_extract(document_json, '$.restore_tool_arguments') THEN 'true'
        ELSE 'false'
    END),
    '$.placeholder_notice',
    json(CASE
        WHEN json_extract(document_json, '$.placeholder_notice') IS NULL THEN 'true'
        WHEN json_extract(document_json, '$.placeholder_notice') THEN 'true'
        ELSE 'false'
    END)
)
WHERE id = 'policy_privacy_default'`,
			},
		},
		{
			Version: 20,
			Name:    "service_disabled_model_list",
			// An absent list decodes to nil and would re-encode as JSON null,
			// so existing documents are seeded with an empty array to keep the
			// stored shape identical to what the control API now returns.
			Statements: []string{
				`UPDATE services
SET document_json = json_set(
    document_json,
    '$.disabled_models',
    COALESCE(json_extract(document_json, '$.disabled_models'), json('[]'))
)`,
			},
		},
		{
			Version: 21,
			Name:    "drop_service_disabled_models",
			Statements: []string{
				`UPDATE services
SET document_json = json_remove(document_json, '$.disabled_models')`,
			},
		},
		{
			Version: 22,
			Name:    "conversation_cursors_and_turns",
			// Typed session cursors (ADR 0015). One record stores every value
			// that can link it to others: explicit ids the request named, and
			// echo ids / keyed fingerprints its response produced. The legacy
			// previous_response_id / output_response_id columns stay in place
			// and are still consulted for explicit lookups; they are not
			// backfilled into this table.
			Statements: []string{
				`CREATE TABLE request_record_cursors (
    request_id TEXT NOT NULL REFERENCES request_records(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK(kind IN ('explicit', 'echo_id', 'fingerprint')),
    direction TEXT NOT NULL CHECK(direction IN ('in', 'out')),
    value TEXT NOT NULL,
    PRIMARY KEY (request_id, kind, direction, value)
)`,
				`CREATE INDEX request_record_cursors_value_idx ON request_record_cursors (value, kind)`,
				`ALTER TABLE request_records ADD COLUMN turn_index INTEGER CHECK(turn_index IS NULL OR turn_index >= 1)`,
				`ALTER TABLE request_records ADD COLUMN session_link_json TEXT`,
			},
		},
		{
			Version: 23,
			Name:    "conversation_turn_state",
			// Turns are placed relative to the record a request links to
			// (ADR 0015): a new turn starts when the history holds more user
			// messages than that record's, or the newest user text changed.
			// Both comparison values are stored per record. Rows written
			// before this version keep NULLs; a request linking to one starts
			// its count again at turn 1.
			Statements: []string{
				`ALTER TABLE request_records ADD COLUMN turn_user_messages INTEGER CHECK(turn_user_messages IS NULL OR turn_user_messages >= 0)`,
				`ALTER TABLE request_records ADD COLUMN turn_user_fingerprint TEXT`,
			},
		},
		{
			Version: 24,
			Name:    "request_reasoning_effort",
			Statements: []string{
				`ALTER TABLE request_records ADD COLUMN reasoning_effort TEXT CHECK(reasoning_effort IS NULL OR length(reasoning_effort) BETWEEN 1 AND 32)`,
			},
		},
		{
			Version: 25,
			Name:    "routing_failure_policies",
			Statements: []string{
				`CREATE TABLE routing_settings (id INTEGER PRIMARY KEY CHECK(id = 1), document_json TEXT NOT NULL)`,
				`INSERT INTO routing_settings VALUES (1, '{"default_failure_policy":{"max_retries":1,"initial_delay_ms":500,"max_delay_ms":5000,"network_error":"retry_and_failover","response_timeout":"retry_and_failover","http_status":{"408":"retry_and_failover","429":"retry_and_failover","500":"retry_and_failover","502":"retry_and_failover","503":"retry_and_failover","504":"retry_and_failover","529":"retry_and_failover","401":"failover","403":"failover"}},"allow_unmatched_failover":false,"strategy":"retry_first","max_attempts":6}')`,
				`ALTER TABLE request_records ADD COLUMN recovery_json TEXT`,
				`CREATE TABLE response_affinities (
    principal TEXT NOT NULL, response_id TEXT NOT NULL, service_id TEXT NOT NULL,
    upstream_model TEXT NOT NULL, upstream_protocol TEXT NOT NULL, plan_type TEXT NOT NULL,
    created_at TEXT NOT NULL, PRIMARY KEY (principal, response_id)
)`,
			},
		},
		{Version: 26, Name: "reusable_recovery_paths", Statements: []string{
			`CREATE TABLE recovery_paths (id TEXT PRIMARY KEY, document_json TEXT NOT NULL)`,
		}},
		{Version: 27, Name: "service_priority_order", Statements: []string{
			`ALTER TABLE services ADD COLUMN sort_position INTEGER NOT NULL DEFAULT 0`,
			`WITH positions AS (SELECT id, ROW_NUMBER() OVER (ORDER BY id) - 1 AS position FROM services) UPDATE services SET sort_position = (SELECT position FROM positions WHERE positions.id = services.id)`,
			`CREATE INDEX services_order_idx ON services(sort_position, id)`,
			`UPDATE routing_settings SET document_json = json_set(document_json, '$.strategy', 'failover_only')`,
		}},
	}
}
