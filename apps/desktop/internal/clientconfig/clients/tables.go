package clients

import (
	"fmt"

	"github.com/QuantumNous/astrlink/desktop/internal/clientconfig/format"
)

// Tables in TOML files read and written as one JSON key each: `[parent.name]`.

// getTable reads `[parent.name]` as compact JSON.
func getTable(toml *format.TOMLDocument, parent, name string) (string, bool, bool) {
	tables := format.TableLike(toml.Root().Get(parent))
	if tables == nil || tables.Get(name) == nil {
		return "", false, false
	}
	value, ok := format.TOMLJSON(tables.Get(name))
	if !ok {
		return "", false, true
	}
	return format.Encode(value), true, true
}

// setTable writes JSON text as `[parent.name]`; values change in place,
// keeping a table already there where it is.
func setTable(toml *format.TOMLDocument, parent, name, key, value string, rank format.Rank) error {
	decoded, err := format.DecodeJSON(value)
	if err != nil {
		return err
	}
	table, ok := format.TOMLTableOf(decoded, rank)
	if !ok {
		return fmt.Errorf("%s cannot be written as TOML", key)
	}
	tables, err := toml.ParentTable(parent)
	if err != nil {
		return err
	}
	if current, ok := tables.Get(name).(*format.TOMLTable); ok {
		table.TakeLayout(current)
		tables.Replace(name, table)
		return nil
	}
	tables.Insert(name, table)
	return nil
}

// removeTable drops `[parent.name]`, and `[parent]` once it is empty.
func removeTable(toml *format.TOMLDocument, parent, name string) {
	if tables := format.TableLike(toml.Root().Get(parent)); tables != nil {
		tables.Remove(name)
	}
	toml.DropEmptyParent(parent)
}

// Lists of `[[list]]` tables that hold the user's entries beside
// AstrLink's, read and written as AstrLink's entries alone.

// arrayOfTables is the `[[list]]` tables at the root, when list is one.
func arrayOfTables(toml *format.TOMLDocument, list string) *format.TOMLArrayOfTables {
	tables, _ := toml.Root().Get(list).(*format.TOMLArrayOfTables)
	return tables
}

// getOwnedTables reads the tables owned picks as compact JSON; nothing
// when there are none.
func getOwnedTables(toml *format.TOMLDocument, list string, owned func(any) bool) (string, bool, bool) {
	tables := arrayOfTables(toml, list)
	if tables == nil {
		return "", false, false
	}
	var entries []any
	for _, table := range tables.Tables {
		if entry, ok := format.TOMLJSON(table); ok && owned(entry) {
			entries = append(entries, entry)
		}
	}
	if len(entries) == 0 {
		return "", false, false
	}
	return format.Encode(entries), true, true
}

// retainUsers drops the tables owned picks from tables.
func retainUsers(tables *format.TOMLArrayOfTables, owned func(any) bool) {
	tables.Retain(func(table *format.TOMLTable) bool {
		entry, ok := format.TOMLJSON(table)
		return !ok || !owned(entry)
	})
}

// dropOwnedTables removes the tables owned picks, and the list once it is
// empty.
func dropOwnedTables(toml *format.TOMLDocument, list string, owned func(any) bool) {
	tables := arrayOfTables(toml, list)
	if tables == nil {
		return
	}
	retainUsers(tables, owned)
	if len(tables.Tables) == 0 {
		toml.Root().Remove(list)
	}
}

// setOwnedTables replaces the tables owned picks with value's, after the
// user's, keeping the list where it is.
func setOwnedTables(toml *format.TOMLDocument, list, value string, owned func(any) bool, rank format.Rank) error {
	decoded, err := format.DecodeJSON(value)
	entries, isList := decoded.([]any)
	if err != nil || !isList {
		return fmt.Errorf("%s is not a list", list)
	}
	root := toml.Root()
	if !root.ContainsKey(list) {
		root.Insert(list, &format.TOMLArrayOfTables{})
	}
	tables := arrayOfTables(toml, list)
	if tables == nil {
		return fmt.Errorf("has a %s value that is not a list of tables", list)
	}
	retainUsers(tables, owned)
	for _, entry := range entries {
		table, ok := format.TOMLTableOf(entry, rank)
		if !ok {
			return fmt.Errorf("%s holds a value TOML cannot", list)
		}
		tables.Push(table)
	}
	return nil
}
