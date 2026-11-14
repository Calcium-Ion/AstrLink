package plugin

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"

	sqlite "modernc.org/sqlite"

	"github.com/MoeForge/moejs"
	"github.com/MoeForge/moejs/engine"
)

// maxDatabases bounds the databases one runtime has open at once.
const maxDatabases = 16

// nodeDatabase is a database of node:sqlite, on one connection, so that
// transactions and temporary tables work as in Node.
type nodeDatabase struct {
	db         *sql.DB
	conn       *sql.Conn
	location   string
	statements map[int]*nodeStatement
	next       int
}

// nodeStatement is a prepared statement of node:sqlite.
type nodeStatement struct {
	stmt    *sql.Stmt
	source  string
	columns []string
}

func (database *nodeDatabase) close() {
	for _, statement := range database.statements {
		_ = statement.stmt.Close()
	}
	database.statements = nil
	_ = database.conn.Close()
	_ = database.db.Close()
}

// sqliteFunctions are the host's side of node:sqlite. A database lives as
// long as the runtime that opened it, across calls.
func (bound *instance) sqliteFunctions() map[string]any {
	dirs := bound.program.dirs
	database := func(realm *moejs.Realm, args []moejs.Value) (*nodeDatabase, error) {
		id, err := realm.ToNumber(moejs.Arg(args, 0))
		if err != nil {
			return nil, err
		}
		found := bound.resources.databases[int(id)]
		if found == nil {
			return nil, sqliteError(realm, "ERR_INVALID_STATE", "database is not open")
		}
		return found, nil
	}
	statement := func(realm *moejs.Realm, args []moejs.Value) (*nodeDatabase, *nodeStatement, error) {
		found, err := database(realm, args)
		if err != nil {
			return nil, nil, err
		}
		id, err := realm.ToNumber(moejs.Arg(args, 1))
		if err != nil {
			return nil, nil, err
		}
		prepared := found.statements[int(id)]
		if prepared == nil {
			return nil, nil, sqliteError(realm, "ERR_INVALID_STATE", "statement has been finalized")
		}
		return found, prepared, nil
	}
	ctx := context.Background()
	return map[string]any{
		"open": bound.function("open", 4, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			name, err := stringArg(realm, args, 0, "path")
			if err != nil {
				return moejs.Undefined(), err
			}
			readOnly := engine.ToBoolean(moejs.Arg(args, 1))
			foreignKeys := engine.ToBoolean(moejs.Arg(args, 2))
			// Every runtime of the plugin opens a database of its own, so
			// they wait for each other's locks unless told otherwise.
			timeout := 5000.0
			if value := moejs.Arg(args, 3); !value.IsUndefined() {
				number, err := realm.ToNumber(value)
				if err != nil || number != number || number < 0 {
					number = 0
				}
				timeout = number
			}
			if len(bound.resources.databases) >= maxDatabases {
				return moejs.Undefined(), sqliteError(realm, "ERR_SQLITE_ERROR", "too many databases are open")
			}
			location := ":memory:"
			if name != ":memory:" {
				at, err := dirs.resolve(name)
				at.close()
				if err == nil && !at.writable {
					readOnly = true
				}
				if err != nil {
					code, detail := fsError(err)
					return moejs.Undefined(), nodeError(realm, code, "open", name, "", detail)
				}
				location = at.abs
			}
			db, err := sql.Open("sqlite", location)
			if err != nil {
				return moejs.Undefined(), sqliteError(realm, "ERR_SQLITE_ERROR", err.Error())
			}
			db.SetMaxOpenConns(1)
			conn, err := db.Conn(ctx)
			if err == nil {
				pragmas := []string{"PRAGMA busy_timeout = " + strconv.Itoa(int(min(timeout, math.MaxInt32)))}
				if foreignKeys {
					pragmas = append(pragmas, "PRAGMA foreign_keys = ON")
				} else {
					pragmas = append(pragmas, "PRAGMA foreign_keys = OFF")
				}
				if readOnly {
					pragmas = append(pragmas, "PRAGMA query_only = ON")
				}
				for _, pragma := range pragmas {
					if _, err = conn.ExecContext(ctx, pragma); err != nil {
						break
					}
				}
			}
			if err != nil {
				_ = db.Close()
				return moejs.Undefined(), sqliteError(realm, "ERR_SQLITE_ERROR", sqliteMessage(err))
			}
			if bound.resources.databases == nil {
				bound.resources.databases = make(map[int]*nodeDatabase)
			}
			bound.resources.nextDB++
			id := bound.resources.nextDB
			bound.resources.databases[id] = &nodeDatabase{db: db, conn: conn, location: location, statements: make(map[int]*nodeStatement)}
			return moejs.Int(int64(id)), nil
		}),
		"close": bound.function("close", 1, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			found, err := database(realm, args)
			if err != nil {
				return moejs.Undefined(), err
			}
			id, _ := realm.ToNumber(moejs.Arg(args, 0))
			delete(bound.resources.databases, int(id))
			found.close()
			return moejs.Undefined(), nil
		}),
		"location": bound.function("location", 1, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			found, err := database(realm, args)
			if err != nil {
				return moejs.Undefined(), err
			}
			if found.location == ":memory:" {
				return moejs.Null(), nil
			}
			return moejs.String(found.location), nil
		}),
		"exec": bound.function("exec", 2, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			found, err := database(realm, args)
			if err != nil {
				return moejs.Undefined(), err
			}
			text, err := stringArg(realm, args, 1, "sql")
			if err != nil {
				return moejs.Undefined(), err
			}
			if found.location != ":memory:" && dirs.full() {
				return moejs.Undefined(), sqliteError(realm, "ERR_SQLITE_ERROR", "database or disk is full")
			}
			bound.blocking(func() { _, err = found.conn.ExecContext(ctx, text) })
			if err != nil {
				return moejs.Undefined(), sqliteError(realm, "ERR_SQLITE_ERROR", sqliteMessage(err))
			}
			return moejs.Undefined(), nil
		}),
		"prepare": bound.function("prepare", 2, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			found, err := database(realm, args)
			if err != nil {
				return moejs.Undefined(), err
			}
			text, err := stringArg(realm, args, 1, "sql")
			if err != nil {
				return moejs.Undefined(), err
			}
			stmt, err := found.conn.PrepareContext(ctx, text)
			if err != nil {
				return moejs.Undefined(), sqliteError(realm, "ERR_SQLITE_ERROR", sqliteMessage(err))
			}
			found.next++
			found.statements[found.next] = &nodeStatement{stmt: stmt, source: text}
			return moejs.Int(int64(found.next)), nil
		}),
		"finalize": bound.function("finalize", 2, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			found, prepared, err := statement(realm, args)
			if err != nil {
				return moejs.Undefined(), nil
			}
			id, _ := realm.ToNumber(moejs.Arg(args, 1))
			delete(found.statements, int(id))
			_ = prepared.stmt.Close()
			return moejs.Undefined(), nil
		}),
		// run(db, statement, positional, named) runs a statement that
		// returns no rows.
		"run": bound.function("run", 4, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			found, prepared, err := statement(realm, args)
			if err != nil {
				return moejs.Undefined(), err
			}
			parameters, err := sqliteParameters(realm, moejs.Arg(args, 2), moejs.Arg(args, 3))
			if err != nil {
				return moejs.Undefined(), err
			}
			if found.location != ":memory:" && dirs.full() {
				return moejs.Undefined(), sqliteError(realm, "ERR_SQLITE_ERROR", "database or disk is full")
			}
			var result sql.Result
			bound.blocking(func() { result, err = prepared.stmt.ExecContext(ctx, parameters...) })
			if err != nil {
				return moejs.Undefined(), sqliteError(realm, "ERR_SQLITE_ERROR", sqliteMessage(err))
			}
			changes, _ := result.RowsAffected()
			last, _ := result.LastInsertId()
			return realm.FromGo(map[string]any{"changes": changes, "lastInsertRowid": last})
		}),
		// query(db, statement, positional, named, limit, arrays, bigints)
		// returns [rows, blobs]: the rows, at most limit of them when limit
		// > 0, and whether a column holds bytes.
		"query": bound.function("query", 7, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			_, prepared, err := statement(realm, args)
			if err != nil {
				return moejs.Undefined(), err
			}
			parameters, err := sqliteParameters(realm, moejs.Arg(args, 2), moejs.Arg(args, 3))
			if err != nil {
				return moejs.Undefined(), err
			}
			limit, err := realm.ToNumber(moejs.Arg(args, 4))
			if err != nil {
				return moejs.Undefined(), err
			}
			arrays := engine.ToBoolean(moejs.Arg(args, 5))
			bigints := engine.ToBoolean(moejs.Arg(args, 6))
			var rows [][]any
			var columns []string
			bound.blocking(func() {
				var cursor *sql.Rows
				if cursor, err = prepared.stmt.QueryContext(ctx, parameters...); err != nil {
					return
				}
				defer cursor.Close()
				if columns, err = cursor.Columns(); err != nil {
					return
				}
				for cursor.Next() && (limit <= 0 || len(rows) < int(limit)) {
					row := make([]any, len(columns))
					pointers := make([]any, len(columns))
					for index := range row {
						pointers[index] = &row[index]
					}
					if err = cursor.Scan(pointers...); err != nil {
						return
					}
					rows = append(rows, row)
				}
				err = cursor.Err()
			})
			if err != nil {
				return moejs.Undefined(), sqliteError(realm, "ERR_SQLITE_ERROR", sqliteMessage(err))
			}
			prepared.columns = columns
			list := make([]moejs.Value, 0, len(rows))
			blobs := false
			for _, row := range rows {
				values := make([]moejs.Value, len(row))
				for index, cell := range row {
					if _, ok := cell.([]byte); ok {
						blobs = true
					}
					value, err := sqliteValue(realm, cell, bigints)
					if err != nil {
						return moejs.Undefined(), err
					}
					values[index] = value
				}
				if arrays {
					list = append(list, engine.ObjectValue(realm.NewArrayFromSlice(values)))
					continue
				}
				object := realm.NewObjectWithProto(nil)
				for index, column := range columns {
					_, _ = object.CreateDataProperty(realm, realm.KeyFromGoString(column), values[index])
				}
				list = append(list, engine.ObjectValue(object))
			}
			return engine.ObjectValue(realm.NewArray(engine.ObjectValue(realm.NewArrayFromSlice(list)), moejs.Bool(blobs))), nil
		}),
		"columns": bound.function("columns", 2, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			_, prepared, err := statement(realm, args)
			if err != nil {
				return moejs.Undefined(), err
			}
			names := make([]any, 0, len(prepared.columns))
			for _, column := range prepared.columns {
				names = append(names, column)
			}
			return realm.FromGo(names)
		}),
	}
}

// sqliteParameters reads the parameters of a statement: positional values,
// and named ones from an object, whose keys may leave out the prefix.
func sqliteParameters(realm *moejs.Realm, positional, named moejs.Value) ([]any, error) {
	var parameters []any
	if positional.IsObject() {
		for _, value := range positional.AsObject().Elements() {
			converted, err := sqliteArgument(realm, value)
			if err != nil {
				return nil, err
			}
			parameters = append(parameters, converted)
		}
	}
	if named.IsObject() {
		keys, err := realm.EnumerableOwnKeys(named.AsObject())
		if err != nil {
			return nil, err
		}
		for _, key := range keys {
			value, err := named.AsObject().Get(realm, key, named)
			if err != nil {
				return nil, err
			}
			converted, err := sqliteArgument(realm, value)
			if err != nil {
				return nil, err
			}
			name := strings.TrimLeft(key.String().GoString(), ":$@")
			parameters = append(parameters, sql.Named(name, converted))
		}
	}
	return parameters, nil
}

// sqliteArgument converts a value bound to a statement.
func sqliteArgument(realm *moejs.Realm, value moejs.Value) (any, error) {
	switch {
	case value.IsNull(), value.IsUndefined():
		return nil, nil
	case value.IsString():
		text, err := realm.ToString(value)
		if err != nil {
			return nil, err
		}
		return text.GoString(), nil
	case value.IsBigInt():
		return realm.ToBigInt64(value)
	case value.IsNumber():
		number, _ := realm.ToNumber(value)
		if number == math.Trunc(number) && math.Abs(number) <= 1<<53 {
			return int64(number), nil
		}
		return number, nil
	case value.IsBool():
		if engine.ToBoolean(value) {
			return int64(1), nil
		}
		return int64(0), nil
	case value.IsObject():
		if data, ok := value.AsObject().BufferData(); ok {
			return append([]byte{}, data...), nil
		}
	}
	return nil, sqliteError(realm, "ERR_INVALID_ARG_TYPE", "Provided value cannot be bound to SQLite parameter")
}

// sqliteValue converts a column of a row.
func sqliteValue(realm *moejs.Realm, cell any, bigints bool) (moejs.Value, error) {
	switch cell := cell.(type) {
	case nil:
		return moejs.Null(), nil
	case int64:
		if bigints {
			return engine.BigIntValue(engine.NewBigIntFromInt64(cell)), nil
		}
		if cell > 1<<53-1 || cell < -(1<<53-1) {
			return moejs.Undefined(), sqliteError(realm, "ERR_OUT_OF_RANGE", "Value is too large to be represented as a JavaScript number")
		}
		return moejs.Int(cell), nil
	case float64:
		return moejs.Number(cell), nil
	case string:
		return moejs.String(cell), nil
	case []byte:
		// An ArrayBuffer, which node/sqlite.js views as a Uint8Array.
		return bytesValue(realm, append([]byte{}, cell...))
	case time.Time:
		layout := "2006-01-02 15:04:05"
		if cell.Nanosecond() != 0 {
			layout += ".999999999"
		}
		if _, offset := cell.Zone(); offset != 0 {
			layout += "-07:00"
		}
		return moejs.String(cell.Format(layout)), nil
	case bool:
		if cell {
			return moejs.Int(1), nil
		}
		return moejs.Int(0), nil
	}
	return moejs.Undefined(), sqliteError(realm, "ERR_SQLITE_ERROR", "a column has a type node:sqlite does not read")
}

// sqliteMessage is the message SQLite gave for err.
func sqliteMessage(err error) string {
	var failure *sqlite.Error
	if errors.As(err, &failure) {
		message := failure.Error()
		if index := strings.LastIndex(message, ": "); index >= 0 && strings.Contains(message, "SQL logic error") {
			return message[index+2:]
		}
		return message
	}
	return err.Error()
}

// sqliteError throws an Error of node:sqlite.
func sqliteError(realm *moejs.Realm, code, message string) error {
	return cryptoError(realm, code, message)
}
