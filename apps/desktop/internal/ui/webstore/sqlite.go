package webstore

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"unicode/utf16"
)

// WebKit keeps an origin's localStorage in a SQLite database with one
// table, ItemTable(key TEXT, value BLOB), the value being UTF-16LE. This
// reads it without SQLite: the pages of the file, with the committed
// frames of its write-ahead log over them, walked as the table's B-tree.

const sqliteMagic = "SQLite format 3\x00"

type sqliteFile struct {
	data     []byte
	pageSize int
	usable   int
	encoding uint32
	// wal holds the pages the write-ahead log committed, by number.
	wal map[uint32][]byte
}

func openSQLite(path string) (*sqliteFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	f := &sqliteFile{data: data, wal: map[uint32][]byte{}}
	if wal, err := os.ReadFile(path + "-wal"); err == nil {
		f.applyWAL(wal)
	}
	header := data
	if page, ok := f.wal[1]; ok {
		header = page
	}
	if len(header) < 100 || string(header[:16]) != sqliteMagic {
		return nil, errors.New("not a SQLite database")
	}
	f.pageSize = int(binary.BigEndian.Uint16(header[16:18]))
	if f.pageSize == 1 {
		f.pageSize = 65536
	}
	if f.pageSize < 512 || f.pageSize&(f.pageSize-1) != 0 {
		return nil, fmt.Errorf("invalid SQLite page size %d", f.pageSize)
	}
	f.usable = f.pageSize - int(header[20])
	f.encoding = binary.BigEndian.Uint32(header[56:60])
	for number, page := range f.wal {
		if len(page) != f.pageSize {
			delete(f.wal, number)
		}
	}
	return f, nil
}

// applyWAL keeps the pages of the log's committed transactions, the last
// version of each. A frame from an earlier generation of the log, or one
// whose checksum fails, ends it.
func (f *sqliteFile) applyWAL(wal []byte) {
	if len(wal) < 32 {
		return
	}
	magic := binary.BigEndian.Uint32(wal[0:4])
	if magic&^1 != 0x377f0682 {
		return
	}
	bigEndian := magic&1 == 1
	pageSize := int(binary.BigEndian.Uint32(wal[8:12]))
	if pageSize < 512 || pageSize > 65536 {
		return
	}
	salt1, salt2 := binary.BigEndian.Uint32(wal[16:20]), binary.BigEndian.Uint32(wal[20:24])
	s0, s1 := walChecksum(bigEndian, 0, 0, wal[0:24])
	if s0 != binary.BigEndian.Uint32(wal[24:28]) || s1 != binary.BigEndian.Uint32(wal[28:32]) {
		return
	}
	pending := map[uint32][]byte{}
	for off := 32; off+24+pageSize <= len(wal); off += 24 + pageSize {
		frame := wal[off : off+24]
		page := wal[off+24 : off+24+pageSize]
		if binary.BigEndian.Uint32(frame[8:12]) != salt1 || binary.BigEndian.Uint32(frame[12:16]) != salt2 {
			return
		}
		s0, s1 = walChecksum(bigEndian, s0, s1, frame[0:8])
		s0, s1 = walChecksum(bigEndian, s0, s1, page)
		if s0 != binary.BigEndian.Uint32(frame[16:20]) || s1 != binary.BigEndian.Uint32(frame[20:24]) {
			return
		}
		pending[binary.BigEndian.Uint32(frame[0:4])] = page
		if binary.BigEndian.Uint32(frame[4:8]) != 0 {
			for number, committed := range pending {
				f.wal[number] = committed
			}
			clear(pending)
		}
	}
}

func walChecksum(bigEndian bool, s0, s1 uint32, data []byte) (uint32, uint32) {
	order := binary.ByteOrder(binary.LittleEndian)
	if bigEndian {
		order = binary.BigEndian
	}
	for i := 0; i+8 <= len(data); i += 8 {
		s0 += order.Uint32(data[i:]) + s1
		s1 += order.Uint32(data[i+4:]) + s0
	}
	return s0, s1
}

func (f *sqliteFile) page(number uint32) ([]byte, error) {
	if page, ok := f.wal[number]; ok {
		return page, nil
	}
	start := (int(number) - 1) * f.pageSize
	if number == 0 || start+f.pageSize > len(f.data) {
		return nil, fmt.Errorf("SQLite page %d is out of range", number)
	}
	return f.data[start : start+f.pageSize], nil
}

// varint reads a SQLite varint: big-endian, seven bits a byte, the ninth
// byte whole.
func varint(b []byte) (uint64, int) {
	var v uint64
	for i := 0; i < 8; i++ {
		if i >= len(b) {
			return 0, 0
		}
		v = v<<7 | uint64(b[i]&0x7f)
		if b[i] < 0x80 {
			return v, i + 1
		}
	}
	if len(b) < 9 {
		return 0, 0
	}
	return v<<8 | uint64(b[8]), 9
}

// walkTable calls visit with the payload of every row of the table whose
// B-tree starts at root.
func (f *sqliteFile) walkTable(root uint32, visit func(payload []byte) error) error {
	seen := map[uint32]bool{}
	var walk func(number uint32) error
	walk = func(number uint32) error {
		if seen[number] {
			return errors.New("SQLite B-tree has a cycle")
		}
		seen[number] = true
		page, err := f.page(number)
		if err != nil {
			return err
		}
		header := 0
		if number == 1 {
			header = 100
		}
		if header+8 > len(page) {
			return errors.New("SQLite page is truncated")
		}
		kind := page[header]
		cells := int(binary.BigEndian.Uint16(page[header+3:]))
		switch kind {
		case 0x0d:
			pointers := header + 8
			for i := range cells {
				if pointers+2*i+2 > len(page) {
					return errors.New("SQLite cell pointer out of range")
				}
				cell := int(binary.BigEndian.Uint16(page[pointers+2*i:]))
				payload, err := f.leafPayload(page, cell)
				if err != nil {
					return err
				}
				if err := visit(payload); err != nil {
					return err
				}
			}
			return nil
		case 0x05:
			pointers := header + 12
			for i := range cells {
				if pointers+2*i+2 > len(page) {
					return errors.New("SQLite cell pointer out of range")
				}
				cell := int(binary.BigEndian.Uint16(page[pointers+2*i:]))
				if cell+4 > len(page) {
					return errors.New("SQLite cell out of range")
				}
				if err := walk(binary.BigEndian.Uint32(page[cell:])); err != nil {
					return err
				}
			}
			return walk(binary.BigEndian.Uint32(page[header+8:]))
		default:
			return fmt.Errorf("SQLite page %d is not a table page", number)
		}
	}
	return walk(root)
}

// leafPayload reads a table leaf cell's payload, following its overflow
// pages.
func (f *sqliteFile) leafPayload(page []byte, cell int) ([]byte, error) {
	if cell >= len(page) {
		return nil, errors.New("SQLite cell out of range")
	}
	size, n := varint(page[cell:])
	if n == 0 {
		return nil, errors.New("SQLite cell is truncated")
	}
	_, m := varint(page[cell+n:])
	if m == 0 {
		return nil, errors.New("SQLite cell is truncated")
	}
	start := cell + n + m
	total := int(size)
	if size > 1<<30 {
		return nil, errors.New("SQLite payload is too large")
	}
	local := total
	if maxLocal := f.usable - 35; total > maxLocal {
		minLocal := (f.usable-12)*32/255 - 23
		local = minLocal + (total-minLocal)%(f.usable-4)
		if local > maxLocal {
			local = minLocal
		}
	}
	if start+local > len(page) {
		return nil, errors.New("SQLite payload out of range")
	}
	payload := append([]byte(nil), page[start:start+local]...)
	if local == total {
		return payload, nil
	}
	if start+local+4 > len(page) {
		return nil, errors.New("SQLite overflow pointer out of range")
	}
	next := binary.BigEndian.Uint32(page[start+local:])
	seen := map[uint32]bool{}
	for len(payload) < total {
		if next == 0 || seen[next] {
			return nil, errors.New("SQLite overflow chain is broken")
		}
		seen[next] = true
		overflow, err := f.page(next)
		if err != nil {
			return nil, err
		}
		chunk := overflow[4:f.usable]
		if rest := total - len(payload); len(chunk) > rest {
			chunk = chunk[:rest]
		}
		payload = append(payload, chunk...)
		next = binary.BigEndian.Uint32(overflow[0:4])
	}
	return payload, nil
}

// column is one value of a record: an integer, or bytes of a text or a
// blob.
type column struct {
	null    bool
	integer int64
	text    bool
	bytes   []byte
}

func record(payload []byte) ([]column, error) {
	headerSize, n := varint(payload)
	if n == 0 || int(headerSize) > len(payload) {
		return nil, errors.New("SQLite record header is truncated")
	}
	var columns []column
	body := int(headerSize)
	for pos := n; pos < int(headerSize); {
		serial, m := varint(payload[pos:])
		if m == 0 {
			return nil, errors.New("SQLite record header is truncated")
		}
		pos += m
		var c column
		size := 0
		switch {
		case serial == 0:
			c.null = true
		case serial <= 6:
			size = []int{0, 1, 2, 3, 4, 6, 8}[serial]
		case serial == 7:
			size = 8
		case serial == 8, serial == 9:
			c.integer = int64(serial - 8)
		case serial >= 12:
			size = int((serial - 12) / 2)
			c.text = serial%2 == 1
		default:
			return nil, fmt.Errorf("SQLite serial type %d is reserved", serial)
		}
		if body+size > len(payload) {
			return nil, errors.New("SQLite record is truncated")
		}
		value := payload[body : body+size]
		body += size
		switch {
		case serial >= 1 && serial <= 6:
			var v int64
			for _, b := range value {
				v = v<<8 | int64(b)
			}
			// Sign-extend.
			shift := 64 - 8*uint(size)
			c.integer = v << shift >> shift
		case serial >= 12:
			c.bytes = value
		}
		columns = append(columns, c)
	}
	return columns, nil
}

func (f *sqliteFile) decodeText(b []byte) string {
	switch f.encoding {
	case 2:
		return utf16String(b, binary.LittleEndian)
	case 3:
		return utf16String(b, binary.BigEndian)
	}
	return string(b)
}

func utf16String(b []byte, order binary.ByteOrder) string {
	units := make([]uint16, len(b)/2)
	for i := range units {
		units[i] = order.Uint16(b[2*i:])
	}
	return string(utf16.Decode(units))
}

// ReadWebKitStorage reads the items of a WebKit localStorage database.
func ReadWebKitStorage(path string) (map[string]string, error) {
	f, err := openSQLite(path)
	if err != nil {
		return nil, err
	}
	root := uint32(0)
	err = f.walkTable(1, func(payload []byte) error {
		row, err := record(payload)
		if err != nil {
			return err
		}
		if len(row) >= 4 && row[0].text && string(row[0].bytes) == "table" &&
			row[1].text && bytes.EqualFold(row[1].bytes, []byte("ItemTable")) {
			root = uint32(row[3].integer)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if root == 0 {
		return nil, errors.New("the database has no ItemTable")
	}
	items := map[string]string{}
	err = f.walkTable(root, func(payload []byte) error {
		row, err := record(payload)
		if err != nil {
			return err
		}
		if len(row) < 2 || row[0].null || row[1].null {
			return nil
		}
		key := f.decodeText(row[0].bytes)
		if row[1].text {
			items[key] = f.decodeText(row[1].bytes)
		} else {
			// WebKit stores the value's UTF-16 code units.
			items[key] = utf16String(row[1].bytes, binary.LittleEndian)
		}
		return nil
	})
	return items, err
}
