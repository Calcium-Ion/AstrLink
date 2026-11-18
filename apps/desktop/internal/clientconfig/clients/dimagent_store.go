package clients

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/astrlink/desktop/internal/clientconfig/format"
	"github.com/QuantumNous/astrlink/desktop/internal/realpath"

	// SQLite 3.54.1, which fixes the read of uninitialized page-cache memory
	// through a -wal file of another page size (CVE-2026-119375).
	_ "modernc.org/sqlite"
)

// Transactions over DimAgent's provider settings. Session and message
// tables stay under DimAgent's storage implementation.

var (
	errDimAgentSettings = errors.New("invalid provider settings")
	errDimAgentAccess   = errors.New("unable to access DimAgent settings; close DimAgent and try again")
	errDimAgentFormat   = errors.New("unsupported DimAgent settings format; no settings were changed")
	errDimAgentSnapshot = errors.New("invalid DimAgent settings snapshot")
	errDimAgentProvider = errors.New("invalid DimAgent provider settings")
)

// dimAgentLongest is the longest text a column may hold for AstrLink to
// read it.
const dimAgentLongest = 4_194_304

var dimAgentJSONColumns = []string{
	"auth", "credential", "accountUsageSettings", "headers", "retryConfig",
	"models", "modelConfigOverrides", "capabilityDefaults", "metadata",
}

var dimAgentMetaColumns = []string{"providerId", "version", "createdAt", "updatedAt"}

// dimAgentSchema is the stable client's provider columns; nil for the Rust
// builds' records.
type dimAgentSchema struct {
	native  bool
	columns []string
}

func (s dimAgentSchema) idColumn() string {
	if s.native {
		return "provider_id"
	}
	return "providerId"
}

func dimAgentColumns(ctx context.Context, tx *sql.Tx, table string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, "SELECT name FROM pragma_table_info(?)", table)
	if err != nil {
		return nil, errDimAgentAccess
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var name string
		if rows.Scan(&name) != nil {
			return nil, errDimAgentAccess
		}
		columns = append(columns, name)
	}
	if rows.Err() != nil {
		return nil, errDimAgentAccess
	}
	return columns, nil
}

func dimAgentSchemaOf(ctx context.Context, tx *sql.Tx) (dimAgentSchema, error) {
	providers, err := dimAgentColumns(ctx, tx, "providers")
	if err != nil {
		return dimAgentSchema{}, err
	}
	selection, err := dimAgentColumns(ctx, tx, "provider_selections")
	if err != nil {
		return dimAgentSchema{}, err
	}
	has := func(columns []string, names ...string) bool {
		for _, name := range names {
			if !slices.Contains(columns, name) {
				return false
			}
		}
		return true
	}
	if has(providers, "providerId", "source", "displayName", "driverKind", "baseUrl", "auth", "credential",
		"headers", "models", "activeModelId", "modelsSource", "enabled", "createdAt", "updatedAt", "version") &&
		has(selection, "selectionId", "scope", "cwd", "providerId", "modelId", "createdAt", "updatedAt", "version") {
		return dimAgentSchema{columns: providers}, nil
	}
	if slices.Equal(providers, []string{"provider_id", "version", "record"}) &&
		has(selection, "scope", "version", "provider_id", "model_id") {
		// The published Rust formats have the same provider representation.
		// A future format needs review before it can receive writes here.
		rows, err := tx.QueryContext(ctx, "SELECT version FROM agent_store_format")
		if err != nil {
			return dimAgentSchema{}, errDimAgentAccess
		}
		defer rows.Close()
		var versions []int64
		for rows.Next() {
			var version int64
			if rows.Scan(&version) != nil {
				return dimAgentSchema{}, errDimAgentAccess
			}
			versions = append(versions, version)
		}
		if rows.Err() != nil {
			return dimAgentSchema{}, errDimAgentAccess
		}
		if len(versions) == 1 && (versions[0] == 13 || versions[0] >= 15 && versions[0] <= 34) {
			return dimAgentSchema{native: true}, nil
		}
	}
	return dimAgentSchema{}, errDimAgentFormat
}

// dimAgentOpen opens the database for one transaction. A read opens it
// read-only and never creates it.
func dimAgentOpen(path string, write bool) (*sql.DB, error) {
	mode := "ro"
	if write {
		mode = "rw"
	}
	uri := "file:" + filepath.ToSlash(path)
	if runtime.GOOS == "windows" {
		uri = "file:///" + filepath.ToSlash(path)
	}
	query := "?mode=" + mode + "&_pragma=busy_timeout(2000)"
	if write {
		// BEGIN IMMEDIATE: nobody writes between the check and the commit.
		query += "&_txlock=immediate"
	}
	db, err := sql.Open("sqlite", uri+query)
	if err != nil {
		return nil, errDimAgentAccess
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

func dimAgentIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// dimAgentText is a column's text, refused when it is longer than
// dimAgentLongest or not UTF-8.
func dimAgentText(value any) (string, bool) {
	var text string
	switch v := value.(type) {
	case string:
		text = v
	case []byte:
		text = string(v)
	default:
		return "", false
	}
	return text, len(text) <= dimAgentLongest && utf8.ValidString(text)
}

func dimAgentSnapshot(ctx context.Context, tx *sql.Tx, schema dimAgentSchema) (map[string]any, error) {
	providers := map[string]any{}
	for _, route := range dimAgentRoutes {
		record, err := dimAgentProviderOf(ctx, tx, schema, route.id)
		if err != nil {
			return nil, err
		}
		if record != nil {
			providers[route.id] = record
		}
	}
	query := "SELECT providerId,modelId FROM provider_selections WHERE scope='global'"
	if schema.native {
		query = "SELECT provider_id,model_id FROM provider_selections WHERE scope='global'"
	}
	var provider string
	var model sql.NullString
	result := map[string]any{}
	switch err := tx.QueryRowContext(ctx, query).Scan(&provider, &model); {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return nil, errDimAgentAccess
	default:
		selection := map[string]any{"providerId": provider, "modelId": nil}
		if model.Valid {
			selection["modelId"] = model.String
		}
		result["selection"] = selection
	}
	if len(providers) > 0 {
		result["providers"] = providers
	}
	return result, nil
}

func dimAgentProviderOf(ctx context.Context, tx *sql.Tx, schema dimAgentSchema, id string) (map[string]any, error) {
	if schema.native {
		var version int64
		var raw any
		switch err := tx.QueryRowContext(ctx, "SELECT version,record FROM providers WHERE provider_id=?", id).Scan(&version, &raw); {
		case errors.Is(err, sql.ErrNoRows):
			return nil, nil
		case err != nil:
			return nil, errDimAgentAccess
		}
		text, ok := dimAgentText(raw)
		if !ok {
			return nil, errDimAgentAccess
		}
		decoded, err := format.DecodeJSON(text)
		record, isObject := decoded.(map[string]any)
		if err != nil || !isObject {
			return nil, errDimAgentProvider
		}
		if record["version"] != format.Number(strconv.FormatInt(version, 10)) {
			return nil, errors.New("DimAgent provider version mismatch")
		}
		delete(record, "version")
		return record, nil
	}
	names := make([]string, len(schema.columns))
	for i, column := range schema.columns {
		names[i] = dimAgentIdentifier(column)
	}
	values := make([]any, len(schema.columns))
	pointers := make([]any, len(values))
	for i := range values {
		pointers[i] = &values[i]
	}
	switch err := tx.QueryRowContext(ctx, "SELECT "+strings.Join(names, ",")+" FROM providers WHERE providerId=?", id).Scan(pointers...); {
	case errors.Is(err, sql.ErrNoRows):
		return nil, nil
	case err != nil:
		return nil, errDimAgentAccess
	}
	record := map[string]any{}
	for i, name := range schema.columns {
		if slices.Contains(dimAgentMetaColumns, name) {
			continue
		}
		switch v := values[i].(type) {
		case nil:
			continue
		case int64:
			if name == "enabled" {
				record[name] = v != 0
			} else {
				record[name] = format.Number(strconv.FormatInt(v, 10))
			}
		case float64:
			record[name] = format.Number(format.FormatFloat(v))
		case string, []byte:
			text, ok := dimAgentText(v)
			if !ok {
				return nil, errDimAgentAccess
			}
			if slices.Contains(dimAgentJSONColumns, name) {
				decoded, err := format.DecodeJSON(text)
				if err != nil {
					return nil, errDimAgentAccess
				}
				record[name] = decoded
			} else {
				record[name] = text
			}
		default:
			return nil, errDimAgentAccess
		}
	}
	return record, nil
}

// dimAgentRead is the rows AstrLink manages as JSON; nil when the database
// is missing.
func dimAgentRead(path string) (*string, error) {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	db, err := dimAgentOpen(path, false)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, errDimAgentAccess
	}
	defer func() { _ = tx.Rollback() }()
	schema, err := dimAgentSchemaOf(ctx, tx)
	if err != nil {
		return nil, err
	}
	snapshot, err := dimAgentSnapshot(ctx, tx, schema)
	if err != nil {
		return nil, err
	}
	text := format.EncodePretty(snapshot)
	return &text, nil
}

func dimAgentParse(text *string) (map[string]any, error) {
	raw := "{}"
	if text != nil {
		raw = *text
	}
	decoded, err := format.DecodeJSON(raw)
	root, ok := decoded.(map[string]any)
	if err != nil || !ok {
		return nil, errDimAgentSnapshot
	}
	for key := range root {
		if key != "providers" && key != "selection" {
			return nil, errDimAgentSnapshot
		}
	}
	if providers, present := root["providers"]; present {
		entries, ok := providers.(map[string]any)
		if !ok {
			return nil, errors.New("invalid DimAgent providers")
		}
		for id, provider := range entries {
			if _, isObject := provider.(map[string]any); !dimAgentOwned(id) || !isObject {
				return nil, errors.New("invalid DimAgent providers")
			}
		}
	}
	if selection, present := root["selection"]; present {
		object, _ := selection.(map[string]any)
		_, provider := object["providerId"].(string)
		model, hasModel := object["modelId"]
		_, modelText := model.(string)
		if !provider || !hasModel || model != nil && !modelText {
			return nil, errors.New("invalid DimAgent selection")
		}
	}
	return root, nil
}

func dimAgentProviders(root map[string]any) map[string]any {
	providers, _ := root["providers"].(map[string]any)
	return providers
}

func dimAgentNow() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func dimAgentWriteProvider(ctx context.Context, tx *sql.Tx, schema dimAgentSchema, id string, provider map[string]any) error {
	if schema.native {
		var version int64
		if err := tx.QueryRowContext(ctx, "SELECT version FROM providers WHERE provider_id=?", id).Scan(&version); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return errDimAgentAccess
		}
		if version >= 1<<32-1 {
			return errors.New("DimAgent provider version overflow")
		}
		version++
		record := map[string]any{}
		for key, value := range provider {
			record[key] = value
		}
		record["version"] = format.Number(strconv.FormatInt(version, 10))
		_, err := tx.ExecContext(ctx, "INSERT INTO providers(provider_id,version,record) VALUES (?,?,?) "+
			"ON CONFLICT(provider_id) DO UPDATE SET version=excluded.version,record=excluded.record", id, version, format.Encode(record))
		if err != nil {
			return errDimAgentAccess
		}
		return nil
	}
	var version int64
	var created string
	previous := true
	switch err := tx.QueryRowContext(ctx, "SELECT version,createdAt FROM providers WHERE providerId=?", id).Scan(&version, &created); {
	case errors.Is(err, sql.ErrNoRows):
		previous = false
	case err != nil:
		return errDimAgentAccess
	}
	if version >= 1<<32-1 {
		return errors.New("DimAgent provider version overflow")
	}
	version++
	now := dimAgentNow()
	if !previous {
		created = now
	}
	values := make([]any, len(schema.columns))
	names := make([]string, len(schema.columns))
	var updates []string
	for i, name := range schema.columns {
		names[i] = dimAgentIdentifier(name)
		if name != "providerId" && name != "createdAt" {
			updates = append(updates, names[i]+"=excluded."+names[i])
		}
		switch name {
		case "providerId":
			values[i] = id
		case "version":
			values[i] = version
		case "createdAt":
			values[i] = created
		case "updatedAt":
			values[i] = now
		default:
			values[i] = dimAgentColumnValue(name, provider[name])
		}
	}
	query := "INSERT INTO providers (" + strings.Join(names, ",") + ") VALUES (" +
		strings.TrimSuffix(strings.Repeat("?,", len(names)), ",") + ") ON CONFLICT(providerId) DO UPDATE SET " + strings.Join(updates, ",")
	if _, err := tx.ExecContext(ctx, query, values...); err != nil {
		return errDimAgentAccess
	}
	return nil
}

// dimAgentColumnValue is how the stable client stores a provider field.
func dimAgentColumnValue(name string, value any) any {
	if value == nil {
		return nil
	}
	if slices.Contains(dimAgentJSONColumns, name) {
		return format.Encode(value)
	}
	switch v := value.(type) {
	case string:
		return v
	case bool:
		if v {
			return int64(1)
		}
		return int64(0)
	case format.Number:
		if n, err := strconv.ParseInt(string(v), 10, 64); err == nil {
			return n
		}
		if f, err := strconv.ParseFloat(string(v), 64); err == nil {
			return f
		}
	}
	return nil
}

func dimAgentSelect(ctx context.Context, tx *sql.Tx, schema dimAgentSchema, value any) error {
	selection, ok := value.(map[string]any)
	if !ok {
		if _, err := tx.ExecContext(ctx, "DELETE FROM provider_selections WHERE scope='global'"); err != nil {
			return errDimAgentAccess
		}
		return nil
	}
	provider, _ := selection["providerId"].(string)
	var model any
	if text, ok := selection["modelId"].(string); ok {
		model = text
	}
	var err error
	if schema.native {
		_, err = tx.ExecContext(ctx, "INSERT INTO provider_selections(scope,version,provider_id,model_id) VALUES ('global',1,?,?) "+
			"ON CONFLICT(scope) DO UPDATE SET version=version+1,provider_id=excluded.provider_id,model_id=excluded.model_id", provider, model)
	} else {
		now := dimAgentNow()
		_, err = tx.ExecContext(ctx, "INSERT INTO provider_selections(selectionId,scope,providerId,modelId,createdAt,updatedAt,version) "+
			"VALUES ('global','global',?,?,?,?,1) ON CONFLICT(selectionId) DO UPDATE SET providerId=excluded.providerId,"+
			"modelId=excluded.modelId,updatedAt=excluded.updatedAt,version=version+1", provider, model, now, now)
	}
	if err != nil {
		return errDimAgentAccess
	}
	return nil
}

// dimAgentWrite commits after once the rows still read as before, in one
// transaction; nil after removes AstrLink's rows. A failure changes
// nothing.
func dimAgentWrite(path string, before, after *string) error {
	if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
		return errors.New("open DimAgent once before writing its settings")
	}
	resolved, err := realpath.Resolve(path)
	if err != nil {
		return errors.New("unable to locate DimAgent settings")
	}
	expected, err := dimAgentParse(before)
	if err != nil {
		return err
	}
	next, err := dimAgentParse(after)
	if err != nil {
		return err
	}
	if selection, ok := next["selection"].(map[string]any); ok {
		if id, ok := selection["providerId"].(string); ok && dimAgentOwned(id) {
			if _, kept := dimAgentProviders(next)[id]; !kept {
				return errors.New("DimAgent still selects this provider; switch its model before removing it")
			}
		}
	}
	db, err := dimAgentOpen(resolved, true)
	if err != nil {
		return err
	}
	defer db.Close()
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return errDimAgentAccess
	}
	defer func() { _ = tx.Rollback() }()
	if err := dimAgentApply(ctx, tx, resolved, expected, next); err != nil {
		return err
	}
	if tx.Commit() != nil {
		return errDimAgentAccess
	}
	return nil
}

func dimAgentApply(ctx context.Context, tx *sql.Tx, path string, expected, next map[string]any) error {

	schema, err := dimAgentSchemaOf(ctx, tx)
	if err != nil {
		return err
	}
	current, err := dimAgentSnapshot(ctx, tx, schema)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(current, expected) {
		return errors.New("DimAgent settings changed while being edited; try again")
	}
	if err := dimAgentSecure(path); err != nil {
		return err
	}
	old, updated := dimAgentProviders(current), dimAgentProviders(next)
	for _, route := range dimAgentRoutes {
		was, had := old[route.id]
		now, has := updated[route.id]
		if had == has && reflect.DeepEqual(was, now) {
			continue
		}
		if has {
			if err := dimAgentWriteProvider(ctx, tx, schema, route.id, now.(map[string]any)); err != nil {
				return err
			}
			continue
		}
		column := schema.idColumn()
		var referenced bool
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM provider_selections WHERE "+column+"=? AND scope<>'global')", route.id).Scan(&referenced); err != nil {
			return errDimAgentAccess
		}
		if referenced {
			return errors.New("a DimAgent workspace still selects this provider; switch that workspace before removing it")
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM providers WHERE "+column+"=?", route.id); err != nil {
			return errDimAgentAccess
		}
	}
	if !reflect.DeepEqual(current["selection"], next["selection"]) {
		return dimAgentSelect(ctx, tx, schema, next["selection"])
	}
	return nil
}

// dimAgentSecure keeps the database and its journals owner-only: they hold
// the token.
func dimAgentSecure(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		if _, err := os.Stat(path + suffix); err != nil {
			continue
		}
		if os.Chmod(path+suffix, 0o600) != nil {
			return errors.New("unable to protect DimAgent credentials")
		}
	}
	return nil
}
