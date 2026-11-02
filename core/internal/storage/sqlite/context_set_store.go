package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

// A root call's context is stored in two parts. Its record keeps the facts
// of that call in context_json: the parts of its input as the body estimates
// them, its digests, marks, and the memory files and skills its history
// shows used. The project it named, its client and the items it carried,
// with their estimates and the skills' names, form a context set that every
// call carrying the same ones shares. Reads put the two back together and
// scale the estimates to the input the call reported.
//
// The first call to end with a set derives what the set says beyond its
// items: the catalog of skill descriptions and MCP tool names, kept once
// for the sets that share it, and the memory index lines its client had not
// carried before, kept once per project, client and file.

// encodeRecordContext is the part of a call's context its record keeps.
func encodeRecordContext(requestContext contract.RequestContext) (string, error) {
	requestContext.Items = nil
	encoded, err := json.Marshal(requestContext)
	if err != nil {
		return "", fmt.Errorf("encode request context: %w", err)
	}
	return string(encoded), nil
}

// contextSetClient is the client a set is kept under: unknown for a record
// that names none.
func contextSetClient(client contract.ClientType) string {
	if client == "" {
		return string(contract.ClientUnknown)
	}
	return string(client)
}

// recordContextSet finds the set of what a root call carried, adding it the
// first time any call carries it, and returns its id. Calls that named no
// project share the empty project. Nil when the record keeps no context.
// The first call to end with the set derives its catalog and memory entries.
func recordContextSet(ctx context.Context, transaction *sql.Tx, record contract.RequestRecord) (any, error) {
	if record.Context == nil {
		return nil, nil
	}
	items := record.Context.Items
	if items == nil {
		items = []contract.ContextItem{}
	}
	encoded, err := json.Marshal(items)
	if err != nil {
		return nil, fmt.Errorf("encode context set: %w", err)
	}
	key := sha256.Sum256(encoded)
	project, client := textOf(record.Project), contextSetClient(record.ClientType)
	var id int64
	var derived bool
	err = transaction.QueryRowContext(ctx, `SELECT id, derived FROM context_sets
WHERE project = ? AND client_type = ? AND items_key = ?`, project, client, key[:]).Scan(&id, &derived)
	if errors.Is(err, sql.ErrNoRows) {
		result, insertErr := transaction.ExecContext(ctx, `INSERT INTO context_sets (project, client_type, items_key, items_json)
VALUES (?, ?, ?, ?)`, project, client, key[:], string(encoded))
		if insertErr != nil {
			return nil, fmt.Errorf("insert context set: %w", insertErr)
		}
		if id, err = result.LastInsertId(); err != nil {
			return nil, fmt.Errorf("read context set id: %w", err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("find context set: %w", err)
	}
	if !derived && record.Status != contract.RequestStatusPending {
		if err := deriveContextSet(ctx, transaction, id, project, client, record); err != nil {
			return nil, err
		}
	}
	return id, nil
}

// deriveContextSet keeps what a set's first call to end says beyond the
// items: its catalog, shared by every set with the same one, and the lines
// of its memory index, when the client's sets derived so far carried no
// such index. The set is marked so later calls skip it.
func deriveContextSet(ctx context.Context, transaction *sql.Tx, id int64, project, client string, record contract.RequestRecord) error {
	var catalogID any
	if catalog := record.Context.Catalog; catalog != nil {
		encoded, err := json.Marshal(catalog)
		if err != nil {
			return fmt.Errorf("encode context catalog: %w", err)
		}
		key := sha256.Sum256(encoded)
		if _, err := transaction.ExecContext(ctx, `INSERT INTO context_catalogs (catalog_key, catalog_json) VALUES (?, ?)
ON CONFLICT (catalog_key) DO NOTHING`, key[:], string(encoded)); err != nil {
			return fmt.Errorf("insert context catalog: %w", err)
		}
		var found int64
		if err := transaction.QueryRowContext(ctx, `SELECT id FROM context_catalogs WHERE catalog_key = ?`, key[:]).Scan(&found); err != nil {
			return fmt.Errorf("find context catalog: %w", err)
		}
		catalogID = found
	}
	if index := memoryIndexItem(record.Context.Items); index != nil {
		if err := syncMemoryEntries(ctx, transaction, id, project, client, *index, record); err != nil {
			return err
		}
	}
	if _, err := transaction.ExecContext(ctx, `UPDATE context_sets SET derived = 1, catalog_id = ? WHERE id = ?`, catalogID, id); err != nil {
		return fmt.Errorf("mark context set derived: %w", err)
	}
	return nil
}

// memoryIndexItem is the memory index among a call's items: the first
// MEMORY.md, else the first memory file.
func memoryIndexItem(items []contract.ContextItem) *contract.ContextItem {
	var found *contract.ContextItem
	for index := range items {
		item := &items[index]
		if item.Kind != contract.ContextItemMemory {
			continue
		}
		if strings.EqualFold(item.Name, "MEMORY.md") {
			return item
		}
		if found == nil {
			found = item
		}
	}
	return found
}

// syncMemoryEntries brings a client's memory entries in a project up to the
// index a set carried, unless a set derived before carried the same
// version: a session that started earlier still sends the index it started
// with. Lines the client's first index listed are preexisting; lines a
// newer index dropped are marked removed, and come back if it lists them
// again. A title or hook withheld from this call keeps the one kept before.
func syncMemoryEntries(ctx context.Context, transaction *sql.Tx, id int64, project, client string, index contract.ContextItem, record contract.RequestRecord) error {
	rows, err := transaction.QueryContext(ctx, `SELECT items_json FROM context_sets
WHERE project = ? AND client_type = ? AND derived = 1 AND id <> ?`, project, client, id)
	if err != nil {
		return fmt.Errorf("read derived context sets: %w", err)
	}
	carried := false
	for rows.Next() {
		var encoded string
		if err := rows.Scan(&encoded); err != nil {
			rows.Close()
			return fmt.Errorf("scan derived context set: %w", err)
		}
		items, err := decodeContextItems(encoded)
		if err != nil {
			rows.Close()
			return fmt.Errorf("decode derived context set: %w", err)
		}
		if earlier := memoryIndexItem(items); earlier != nil {
			carried = true
			if index.Hash != "" && earlier.Hash == index.Hash {
				rows.Close()
				return nil
			}
		}
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close derived context sets: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate derived context sets: %w", err)
	}
	seen := record.StartedAt.UTC().Format(time.RFC3339Nano)
	files := make([]string, 0, len(record.Context.MemoryIndex))
	for _, entry := range record.Context.MemoryIndex {
		files = append(files, entry.File)
		if _, err := transaction.ExecContext(ctx, `INSERT INTO memory_entries
    (project, client_type, file, title, hook, first_seen_at, first_request_id, preexisting)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (project, client_type, file) DO UPDATE SET
    title = CASE WHEN excluded.title <> '' THEN excluded.title ELSE memory_entries.title END,
    hook = CASE WHEN excluded.hook <> '' THEN excluded.hook ELSE memory_entries.hook END,
    removed_at = NULL
WHERE memory_entries.removed_at IS NOT NULL
    OR (excluded.title <> '' AND excluded.title <> memory_entries.title)
    OR (excluded.hook <> '' AND excluded.hook <> memory_entries.hook)`,
			project, client, entry.File, entry.Title, entry.Hook, seen, string(record.ID), !carried); err != nil {
			return fmt.Errorf("upsert memory entry: %w", err)
		}
	}
	listed, err := json.Marshal(files)
	if err != nil {
		return fmt.Errorf("encode memory entry files: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, `UPDATE memory_entries SET removed_at = ?
WHERE project = ? AND client_type = ? AND removed_at IS NULL
    AND file NOT IN (SELECT value FROM json_each(?))`, seen, project, client, string(listed)); err != nil {
		return fmt.Errorf("mark removed memory entries: %w", err)
	}
	return nil
}

// dropUnusedContextSet deletes a set no call points at. A snapshot that was
// not applied leaves the set it added; deleting and repointing calls is left
// to the triggers on request_records.
func dropUnusedContextSet(ctx context.Context, transaction *sql.Tx, id any) error {
	if id == nil {
		return nil
	}
	if _, err := transaction.ExecContext(ctx, `DELETE FROM context_sets WHERE id = ?1
    AND NOT EXISTS (SELECT 1 FROM request_records WHERE context_set_id = ?1)`, id); err != nil {
		return fmt.Errorf("drop unused context set: %w", err)
	}
	return nil
}

// decodeContextItems reads the items of a set, with their estimates and the
// skills' names.
func decodeContextItems(encoded string) ([]contract.ContextItem, error) {
	var items []contract.ContextItem
	if err := json.Unmarshal([]byte(encoded), &items); err != nil {
		return nil, err
	}
	return items, nil
}

// assembleContext puts a stored root call's context back together from the
// facts its record keeps and the items of its set, scaled to the input the
// call reported. Records read back without the skills' names.
func assembleContext(encoded string, items []contract.ContextItem, usage *contract.Usage) (*contract.RequestContext, error) {
	requestContext := &contract.RequestContext{}
	if err := json.Unmarshal([]byte(encoded), requestContext); err != nil {
		return nil, err
	}
	if len(items) > 0 {
		requestContext.Items = make([]contract.ContextItem, len(items))
		for index, item := range items {
			item.Names = nil
			requestContext.Items[index] = item
		}
	}
	if requestContext.Parts != nil && usage != nil && usage.InputTokens > 0 {
		requestContext.Parts, requestContext.Items = scaleContext(*requestContext.Parts, requestContext.Items, usage.InputTokens)
	}
	return requestContext, nil
}

// scaleContext scales the estimated parts to sum to the input tokens the
// call reported, and the items with them: the rules, memory and skills to
// sum to the memory part, the MCP tools by the same factor as the tools.
func scaleContext(parts contract.ContextParts, items []contract.ContextItem, input int) (*contract.ContextParts, []contract.ContextItem) {
	total := parts.Total()
	if total <= 0 {
		return &parts, items
	}
	shares := apportion([]int{parts.History, parts.ToolResults, parts.Tools, parts.System, parts.Memory, parts.Other}, input)
	scaled := contract.ContextParts{
		History: shares[0], ToolResults: shares[1], Tools: shares[2],
		System: shares[3], Memory: shares[4], Other: shares[5],
	}
	if len(items) == 0 {
		return &scaled, items
	}
	items = slices.Clone(items)
	var carried []int
	for _, item := range items {
		if item.Kind != contract.ContextItemMCP {
			carried = append(carried, item.Tokens)
		}
	}
	carried = apportion(carried, scaled.Memory)
	for index := range items {
		item := &items[index]
		if item.Kind == contract.ContextItemMCP {
			item.Tokens = int(int64(item.Tokens) * int64(input) / int64(total))
			continue
		}
		item.Tokens, carried = carried[0], carried[1:]
	}
	return &scaled, items
}

// apportion splits target in proportion to values, rounding by largest
// remainder so the shares sum to target exactly. All-zero values stay zero.
func apportion(values []int, target int) []int {
	sum := int64(0)
	for _, value := range values {
		sum += int64(value)
	}
	shares := make([]int, len(values))
	if sum <= 0 || target <= 0 {
		return shares
	}
	type remainder struct {
		index int
		rest  int64
	}
	rests := make([]remainder, len(values))
	assigned := 0
	for index, value := range values {
		product := int64(value) * int64(target)
		shares[index] = int(product / sum)
		rests[index] = remainder{index: index, rest: product % sum}
		assigned += shares[index]
	}
	slices.SortStableFunc(rests, func(left, right remainder) int {
		switch {
		case left.rest > right.rest:
			return -1
		case left.rest < right.rest:
			return 1
		default:
			return 0
		}
	})
	for index := 0; assigned < target && index < len(rests); index++ {
		shares[rests[index].index]++
		assigned++
	}
	return shares
}
