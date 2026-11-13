package webstore

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"strings"
)

// WebView2 keeps localStorage in Chromium's LevelDB database. This reads
// its write-ahead logs and its tables, the version of each key with the
// highest sequence number winning, without LevelDB.

var castagnoli = crc32.MakeTable(crc32.Castagnoli)

// unmaskCRC undoes LevelDB's masking of stored checksums.
func unmaskCRC(masked uint32) uint32 {
	rot := masked - 0xa282ead8
	return rot>>17 | rot<<15
}

// entry is one version of a key: its value, or its deletion.
type entry struct {
	sequence uint64
	deleted  bool
	value    []byte
}

type levelDB map[string]entry

func (db levelDB) put(key []byte, sequence uint64, deleted bool, value []byte) {
	if current, ok := db[string(key)]; ok && current.sequence > sequence {
		return
	}
	db[string(key)] = entry{sequence: sequence, deleted: deleted, value: append([]byte(nil), value...)}
}

func readLevelDB(dir string) (levelDB, error) {
	names, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	db := levelDB{}
	for _, name := range names {
		path := filepath.Join(dir, name.Name())
		var err error
		switch filepath.Ext(name.Name()) {
		case ".ldb", ".sst":
			err = db.readTable(path)
		case ".log":
			// LOG and LOG.old are LevelDB's diagnostics, not logs of writes.
			err = db.readLog(path)
		default:
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name.Name(), err)
		}
	}
	return db, nil
}

// readLog replays a log's write batches. A torn or corrupt record ends it,
// as it ends LevelDB's recovery.
func (db levelDB) readLog(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	const blockSize = 32 * 1024
	var record []byte
	for block := 0; block < len(data); block += blockSize {
		end := min(block+blockSize, len(data))
		for pos := block; pos+7 <= end; {
			length := int(binary.LittleEndian.Uint16(data[pos+4:]))
			kind := data[pos+6]
			if kind == 0 && length == 0 {
				break // preallocated zeros
			}
			if pos+7+length > end {
				return nil
			}
			payload := data[pos+7 : pos+7+length]
			crc := crc32.Update(crc32.Checksum([]byte{kind}, castagnoli), castagnoli, payload)
			if crc != unmaskCRC(binary.LittleEndian.Uint32(data[pos:])) {
				return nil
			}
			pos += 7 + length
			switch kind {
			case 1: // full
				db.applyBatch(payload)
				record = nil
			case 2: // first
				record = append(record[:0], payload...)
			case 3: // middle
				record = append(record, payload...)
			case 4: // last
				db.applyBatch(append(record, payload...))
				record = nil
			default:
				return nil
			}
		}
	}
	return nil
}

func (db levelDB) applyBatch(batch []byte) {
	if len(batch) < 12 {
		return
	}
	sequence := binary.LittleEndian.Uint64(batch)
	count := binary.LittleEndian.Uint32(batch[8:])
	rest := batch[12:]
	for i := uint32(0); i < count && len(rest) > 0; i++ {
		kind := rest[0]
		rest = rest[1:]
		key, ok := lengthPrefixed(&rest)
		if !ok {
			return
		}
		switch kind {
		case 1:
			value, ok := lengthPrefixed(&rest)
			if !ok {
				return
			}
			db.put(key, sequence+uint64(i), false, value)
		case 0:
			db.put(key, sequence+uint64(i), true, nil)
		default:
			return
		}
	}
}

func uvarint(b []byte) (uint64, int) {
	var v uint64
	for i := 0; i < len(b) && i < 10; i++ {
		v |= uint64(b[i]&0x7f) << (7 * i)
		if b[i] < 0x80 {
			return v, i + 1
		}
	}
	return 0, 0
}

func lengthPrefixed(b *[]byte) ([]byte, bool) {
	n, m := uvarint(*b)
	if m == 0 || uint64(len(*b)-m) < n {
		return nil, false
	}
	value := (*b)[m : m+int(n)]
	*b = (*b)[m+int(n):]
	return value, true
}

const tableMagic = 0xdb4775248b80fb57

type blockHandle struct{ offset, size uint64 }

func readHandle(b []byte) (blockHandle, int, bool) {
	offset, n := uvarint(b)
	if n == 0 {
		return blockHandle{}, 0, false
	}
	size, m := uvarint(b[n:])
	if m == 0 {
		return blockHandle{}, 0, false
	}
	return blockHandle{offset, size}, n + m, true
}

// readTable reads every entry of a sorted table through its index.
func (db levelDB) readTable(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(data) < 48 || binary.LittleEndian.Uint64(data[len(data)-8:]) != tableMagic {
		return errors.New("not a LevelDB table")
	}
	footer := data[len(data)-48:]
	_, n, ok := readHandle(footer)
	if !ok {
		return errors.New("the table's footer is corrupt")
	}
	indexHandle, _, ok := readHandle(footer[n:])
	if !ok {
		return errors.New("the table's footer is corrupt")
	}
	index, err := tableBlock(data, indexHandle)
	if err != nil {
		return err
	}
	return blockEntries(index, func(_, value []byte) error {
		handle, _, ok := readHandle(value)
		if !ok {
			return errors.New("the table's index is corrupt")
		}
		block, err := tableBlock(data, handle)
		if err != nil {
			return err
		}
		return blockEntries(block, func(key, value []byte) error {
			if len(key) < 8 {
				return errors.New("a table key is too short")
			}
			trailer := binary.LittleEndian.Uint64(key[len(key)-8:])
			switch trailer & 0xff {
			case 0:
				db.put(key[:len(key)-8], trailer>>8, true, nil)
			case 1:
				db.put(key[:len(key)-8], trailer>>8, false, value)
			}
			return nil
		})
	})
}

// tableBlock returns a block's contents, checked and decompressed.
func tableBlock(data []byte, h blockHandle) ([]byte, error) {
	if h.offset > uint64(len(data)) || h.size+5 > uint64(len(data))-h.offset {
		return nil, errors.New("a table block is out of range")
	}
	raw := data[h.offset : h.offset+h.size+5]
	contents, kind := raw[:h.size], raw[h.size]
	crc := crc32.Update(crc32.Checksum(contents, castagnoli), castagnoli, []byte{kind})
	if crc != unmaskCRC(binary.LittleEndian.Uint32(raw[h.size+1:])) {
		return nil, errors.New("a table block's checksum does not match")
	}
	switch kind {
	case 0:
		return contents, nil
	case 1:
		return snappyDecode(contents)
	}
	return nil, fmt.Errorf("table blocks compressed with %d are not supported", kind)
}

// blockEntries calls visit with each key and value of a block, the keys
// rebuilt from their shared prefixes.
func blockEntries(block []byte, visit func(key, value []byte) error) error {
	if len(block) < 4 {
		return errors.New("a table block is too short")
	}
	restarts := int(binary.LittleEndian.Uint32(block[len(block)-4:]))
	if restarts < 0 || 4+4*restarts > len(block) {
		return errors.New("a table block's restarts are corrupt")
	}
	entries := block[:len(block)-4-4*restarts]
	var key []byte
	for len(entries) > 0 {
		shared, a := uvarint(entries)
		if a == 0 {
			return errors.New("a table entry is corrupt")
		}
		unshared, b := uvarint(entries[a:])
		if b == 0 {
			return errors.New("a table entry is corrupt")
		}
		valueLength, c := uvarint(entries[a+b:])
		if c == 0 {
			return errors.New("a table entry is corrupt")
		}
		head := a + b + c
		if shared > uint64(len(key)) || unshared+valueLength > uint64(len(entries)-head) {
			return errors.New("a table entry is out of range")
		}
		key = append(key[:shared], entries[head:head+int(unshared)]...)
		value := entries[head+int(unshared) : head+int(unshared)+int(valueLength)]
		if err := visit(key, value); err != nil {
			return err
		}
		entries = entries[head+int(unshared)+int(valueLength):]
	}
	return nil
}

// snappyDecode decompresses a Snappy block.
func snappyDecode(src []byte) ([]byte, error) {
	length, n := uvarint(src)
	if n == 0 || length > 1<<28 {
		return nil, errors.New("a Snappy block's length is corrupt")
	}
	dst := make([]byte, 0, length)
	corrupt := errors.New("a Snappy block is corrupt")
	for s := n; s < len(src); {
		tag := src[s]
		var size, offset int
		switch tag & 3 {
		case 0:
			size = int(tag >> 2)
			s++
			if size >= 60 {
				extra := size - 59
				if s+extra > len(src) {
					return nil, corrupt
				}
				size = 0
				for i := extra - 1; i >= 0; i-- {
					size = size<<8 | int(src[s+i])
				}
				s += extra
			}
			size++
			if size <= 0 || s+size > len(src) {
				return nil, corrupt
			}
			dst = append(dst, src[s:s+size]...)
			s += size
			continue
		case 1:
			if s+2 > len(src) {
				return nil, corrupt
			}
			size = 4 + int(tag>>2&7)
			offset = int(tag&0xe0)<<3 | int(src[s+1])
			s += 2
		case 2:
			if s+3 > len(src) {
				return nil, corrupt
			}
			size = 1 + int(tag>>2)
			offset = int(binary.LittleEndian.Uint16(src[s+1:]))
			s += 3
		case 3:
			if s+5 > len(src) {
				return nil, corrupt
			}
			size = 1 + int(tag>>2)
			offset = int(binary.LittleEndian.Uint32(src[s+1:]))
			s += 5
		}
		if offset <= 0 || offset > len(dst) {
			return nil, corrupt
		}
		// A copy may overlap what it writes.
		for i := 0; i < size; i++ {
			dst = append(dst, dst[len(dst)-offset])
		}
	}
	if uint64(len(dst)) != length {
		return nil, corrupt
	}
	return dst, nil
}

// chromiumString decodes Chromium's localStorage strings: a format byte,
// 0 for UTF-16LE and 1 for Latin-1, then the string.
func chromiumString(b []byte) (string, bool) {
	if len(b) == 0 {
		return "", false
	}
	switch b[0] {
	case 0:
		if len(b)%2 != 1 {
			return "", false
		}
		return utf16String(b[1:], binary.LittleEndian), true
	case 1:
		runes := make([]rune, len(b)-1)
		for i, c := range b[1:] {
			runes[i] = rune(c)
		}
		return string(runes), true
	}
	return "", false
}

// ReadChromiumStorage reads the localStorage items of origin, such as
// http://tauri.localhost, from a Chromium "Local Storage/leveldb"
// directory.
func ReadChromiumStorage(dir, origin string) (map[string]string, error) {
	db, err := readLevelDB(dir)
	if err != nil {
		return nil, err
	}
	// Chromium prefixed the keys with the serialized origin, later with
	// the storage key, which ends in a slash.
	origin = strings.TrimSuffix(origin, "/")
	prefixes := []string{"_" + origin + "\x00", "_" + origin + "/\x00"}
	items := map[string]string{}
	for key, e := range db {
		if e.deleted {
			continue
		}
		for _, prefix := range prefixes {
			name, ok := strings.CutPrefix(key, prefix)
			if !ok {
				continue
			}
			k, okKey := chromiumString([]byte(name))
			v, okValue := chromiumString(e.value)
			if okKey && okValue {
				items[k] = v
			}
		}
	}
	return items, nil
}
