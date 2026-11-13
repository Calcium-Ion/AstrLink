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
