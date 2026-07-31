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
	}
}
