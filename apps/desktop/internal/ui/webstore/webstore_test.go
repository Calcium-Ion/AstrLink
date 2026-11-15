package webstore

import (
	"encoding/binary"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The fixtures were written by SQLite (the WebKit tables) and by a LevelDB
// implementation (the WebView2 directory), with the schema and key layout
// of the webviews.

func TestTheWebKitReaderWalksInteriorAndOverflowPages(t *testing.T) {
	items, err := ReadWebKitStorage(filepath.Join("testdata", "pages.localstorage"))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 122 {
		t.Fatalf("read %d items", len(items))
	}
	if items["astrlink.theme"] != "light" || items["astrlink.onboarding.v1"] != "已完成 ✓ 🚀" {
		t.Fatalf("theme %q, onboarding %q", items["astrlink.theme"], items["astrlink.onboarding.v1"])
	}
	layout := items["astrlink.overview.layout.v1"]
	if !strings.HasPrefix(layout, `{"order":["module-000",`) || !strings.HasSuffix(layout, `"module-059"]}`) {
		t.Fatalf("layout %q", layout)
	}
	if _, ok := items["other.007"]; ok || items["other.119"] != "v119" {
		t.Fatal("the rows were not read as stored")
	}
}

func copyFixture(t *testing.T, dir string, names ...string) {
	t.Helper()
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTheWebKitReaderAppliesOnlyCommittedLogFrames(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wal.localstorage")
	copyFixture(t, dir, "wal.localstorage")
	items, err := ReadWebKitStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"astrlink.theme": "system", "astrlink.inspector-tour.v1": "pending"}
	if !maps.Equal(items, want) {
		t.Fatalf("without the log: %v", items)
	}

	copyFixture(t, dir, "wal.localstorage-wal")
	items, err = ReadWebKitStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	if items["astrlink.theme"] != "dark" || items["astrlink.inspector-tour.v1"] != "done" ||
		items["astrlink.serviceTerminal.v1"] != strings.Repeat("x", 9000) {
		t.Fatalf("with the log: theme %q, tour %q, %d terminal bytes", items["astrlink.theme"],
			items["astrlink.inspector-tour.v1"], len(items["astrlink.serviceTerminal.v1"]))
	}

	// Cut the log one frame into its last transaction, as a crash would:
	// the transaction before it stands, the torn one is dropped.
	wal, err := os.ReadFile(path + "-wal")
	if err != nil {
		t.Fatal(err)
	}
	pageSize := int(binary.BigEndian.Uint32(wal[8:12]))
	frame := 24 + pageSize
	end := 32
	for end+frame <= len(wal) {
		end += frame
		if binary.BigEndian.Uint32(wal[end-frame+4:]) != 0 {
			break // the first commit
		}
	}
	if end+2*frame > len(wal) {
		t.Fatal("the fixture's last transaction has one frame")
	}
	if err := os.WriteFile(path+"-wal", wal[:end+frame], 0o600); err != nil {
		t.Fatal(err)
	}
	items, err = ReadWebKitStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	want = map[string]string{"astrlink.theme": "system", "astrlink.inspector-tour.v1": "done"}
	if !maps.Equal(items, want) {
		t.Fatalf("torn log: %v", items)
	}

	// A frame whose checksum fails ends the log there.
	wal[32+24] ^= 0xff
	if err := os.WriteFile(path+"-wal", wal, 0o600); err != nil {
		t.Fatal(err)
	}
	items, err = ReadWebKitStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	if items["astrlink.inspector-tour.v1"] != "pending" {
		t.Fatalf("corrupt log: %v", items)
	}
}

func TestTheWebKitReaderRejectsOtherFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.localstorage")
	if err := os.WriteFile(path, []byte("not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadWebKitStorage(path); err == nil {
		t.Fatal("read a file that is not a database")
	}
}

func TestTheChromiumReaderMergesTablesAndLogsByOrigin(t *testing.T) {
	dir := filepath.Join("testdata", "leveldb")
	items, err := ReadChromiumStorage(dir, "http://tauri.localhost")
	if err != nil {
		t.Fatal(err)
	}
	// The log's newer writes win over the compressed table: an update and
	// a deletion; the last value is longer than a log block.
	if items["astrlink.theme"] != "system" {
		t.Fatalf("theme %q", items["astrlink.theme"])
	}
	if _, ok := items["astrlink.onboarding.v1"]; ok {
		t.Fatal("a deleted item was read")
	}
	if items["astrlink.serviceTerminal.v1"] != strings.Repeat("终", 20000) {
		t.Fatalf("read %d terminal bytes", len(items["astrlink.serviceTerminal.v1"]))
	}
	layout := items["astrlink.overview.layout.v1"]
	if !strings.HasPrefix(layout, `{"order":["module",`) || strings.Count(layout, `"module"`) != 200 {
		t.Fatalf("layout %q", layout)
	}
	if len(items) != 3 {
		t.Fatalf("read %v", maps.Keys(items))
	}
	// Storage keys end the origin with a slash.
	items, err = ReadChromiumStorage(dir, "https://tauri.localhost")
	if err != nil || !maps.Equal(items, map[string]string{"astrlink.service-order-guide.v1": "seen"}) {
		t.Fatalf("https: %v, %v", items, err)
	}
}

func TestChromiumStringsAreLatin1OrUTF16(t *testing.T) {
	for _, c := range []struct {
		in   []byte
		want string
		ok   bool
	}{
		{[]byte{1, 'd', 0xe9}, "dé", true},
		{[]byte{0, 0x3d, 0xd8, 0x80, 0xde}, "🚀", true},
		{[]byte{0, 0x41}, "", false},
		{[]byte{2, 'x'}, "", false},
		{nil, "", false},
	} {
		got, ok := chromiumString(c.in)
		if got != c.want || ok != c.ok {
			t.Fatalf("%v: %q %v", c.in, got, ok)
		}
	}
}

func TestSnappyCopiesMayOverlap(t *testing.T) {
	// "ab" then a copy of 6 bytes from 2 back: "abababab".
	got, err := snappyDecode([]byte{8, 1 << 2, 'a', 'b', 1 | (6-4)<<2, 2})
	if err != nil || string(got) != "abababab" {
		t.Fatalf("%q, %v", got, err)
	}
	for _, bad := range [][]byte{{8, 1 << 2, 'a', 'b', 1 | (6-4)<<2, 9}, {3, 4 << 2, 'a'}, {9, 1 << 2, 'a', 'b'}} {
		if _, err := snappyDecode(bad); err == nil {
			t.Fatalf("decoded %v", bad)
		}
	}
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// webKitOrigin is an origin file as WebKit writes it: the top and the
// frame origin, each a protocol, a host and no port.
func webKitOrigin(protocol, host string) []byte {
	var b []byte
	for range 2 {
		for _, s := range []string{protocol, host} {
			b = binary.LittleEndian.AppendUint32(b, uint32(len(s)))
			b = append(b, 1)
			b = append(b, s...)
		}
		b = append(b, 0)
	}
	return b
}

func TestLegacyFindsTheTauriStoresOfEachPlatform(t *testing.T) {
	const id = "com.astrlink.desktop"
	noEnv := func(string) string { return "" }

	t.Run("linux", func(t *testing.T) {
		home := t.TempDir()
		data := filepath.Join(home, "data")
		env := func(k string) string {
			if k == "XDG_DATA_HOME" {
				return data
			}
			return ""
		}
		writeFile(t, filepath.Join(data, id, "localstorage", "tauri_localhost_0.localstorage"), fixture(t, "pages.localstorage"))
		// A newer WebKit's store for the app's origin, and MyGo's own.
		general := filepath.Join(data, id, "storage")
		writeFile(t, filepath.Join(general, "a", "a", "origin"), webKitOrigin("tauri", "localhost"))
		writeFile(t, filepath.Join(general, "a", "a", "LocalStorage", "localstorage.sqlite3"), fixture(t, "wal.localstorage"))
		writeFile(t, filepath.Join(general, "a", "a", "LocalStorage", "localstorage.sqlite3-wal"), fixture(t, "wal.localstorage-wal"))
		writeFile(t, filepath.Join(general, "b", "b", "origin"), webKitOrigin("mygo", "localhost"))
		writeFile(t, filepath.Join(general, "b", "b", "LocalStorage", "localstorage.sqlite3"), []byte("not read"))

		items, errs := Legacy("linux", home, env, id)
		if len(errs) != 0 {
			t.Fatal(errs)
		}
		// The newer layout wins; other names are not the desktop's.
		if items["astrlink.theme"] != "dark" || items["astrlink.onboarding.v1"] != "已完成 ✓ 🚀" ||
			items["astrlink.inspector-tour.v1"] != "done" {
			t.Fatalf("items %v", items)
		}
		for k := range items {
			if !strings.HasPrefix(k, Prefix) {
				t.Fatalf("kept %q", k)
			}
		}
	})

	t.Run("macOS", func(t *testing.T) {
		home := t.TempDir()
		writeFile(t, filepath.Join(home, "Library", "WebKit", id, "WebsiteData", "LocalStorage", "tauri_localhost_0.localstorage"),
			fixture(t, "pages.localstorage"))
		items, errs := Legacy("darwin", home, noEnv, id)
		if len(errs) != 0 || items["astrlink.theme"] != "light" {
			t.Fatalf("items %v, errors %v", items, errs)
		}
	})

	t.Run("Windows", func(t *testing.T) {
		home := t.TempDir()
		local := filepath.Join(home, "Local")
		dir := filepath.Join(local, id, "EBWebView", "Default", "Local Storage", "leveldb")
		for _, name := range []string{"000002.log", "000004.ldb", "CURRENT", "MANIFEST-000000"} {
			writeFile(t, filepath.Join(dir, name), fixture(t, filepath.Join("leveldb", name)))
		}
		env := func(k string) string {
			if k == "LOCALAPPDATA" {
				return local
			}
			return ""
		}
		items, errs := Legacy("windows", home, env, id)
		if len(errs) != 0 || items["astrlink.theme"] != "system" || items["astrlink.service-order-guide.v1"] != "seen" {
			t.Fatalf("items %v, errors %v", items, errs)
		}
	})

	t.Run("nothing stored", func(t *testing.T) {
		for _, goos := range []string{"linux", "darwin", "windows"} {
			items, errs := Legacy(goos, t.TempDir(), noEnv, id)
			if len(items) != 0 || len(errs) != 0 {
				t.Fatalf("%s: %v, %v", goos, items, errs)
			}
		}
	})

	t.Run("unreadable", func(t *testing.T) {
		home := t.TempDir()
		writeFile(t, filepath.Join(home, ".local", "share", id, "localstorage", "tauri_localhost_0.localstorage"), []byte("torn"))
		if items, errs := Legacy("linux", home, noEnv, id); len(items) != 0 || len(errs) != 1 {
			t.Fatalf("%v, %v", items, errs)
		}
	})
}

func TestSelectKeepsTheDesktopItemsWithinBounds(t *testing.T) {
	items := map[string]string{
		"astrlink.theme":          "dark",
		"astrlink:console-locale": "en",
		"other":                   "x",
		"astrlink.huge":           strings.Repeat("x", maxValue),
	}
	for i := range 5 {
		items["astrlink.big"+string(rune('a'+i))] = strings.Repeat("y", maxValue-100)
	}
	got := Select(items)
	if got["astrlink.theme"] != "dark" {
		t.Fatal("the small item was dropped")
	}
	if _, ok := got["astrlink:console-locale"]; ok {
		t.Fatal("kept the web console's item")
	}
	if _, ok := got["astrlink.huge"]; ok {
		t.Fatal("kept an item over the bound")
	}
	total := 0
	for k, v := range got {
		total += len(k) + len(v)
	}
	if total > maxTotal || len(got) != 5 {
		t.Fatalf("kept %d items, %d bytes", len(got), total)
	}
}
