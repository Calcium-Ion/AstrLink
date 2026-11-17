package keystore

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	fileKeyHex   = "a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90"
	storedKeyHex = "0f1e2d3c4b5a69788796a5b4c3d2e1f00f1e2d3c4b5a69788796a5b4c3d2e1f0"
)

type fakeVault struct {
	entries  map[string][]byte
	failGet  bool
	failSet  bool
	dropSets bool // accepts writes without keeping them
	sets     int
}

func newVault() *fakeVault { return &fakeVault{entries: map[string][]byte{}} }

func vaultWith(account, value string) *fakeVault {
	v := newVault()
	v.entries[account] = []byte(value)
	return v
}

func (v *fakeVault) Get(account string) ([]byte, error) {
	if v.failGet {
		return nil, errors.New("OSStatus -25308")
	}
	value, ok := v.entries[account]
	if !ok {
		return nil, ErrNotFound
	}
	return append([]byte(nil), value...), nil
}

func (v *fakeVault) Set(account string, value []byte) error {
	v.sets++
	if v.failSet {
		return errors.New("OSStatus -128")
	}
	if !v.dropSets {
		v.entries[account] = append([]byte(nil), value...)
	}
	return nil
}

type kekCase struct {
	t    *testing.T
	data string
	logs []string
}

func newKEKCase(t *testing.T) *kekCase {
	data := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(data, 0o700); err != nil {
		t.Fatal(err)
	}
	return &kekCase{t: t, data: data}
}

func (c *kekCase) account() string      { return KeychainAccount(c.data) }
func (c *kekCase) keyFile() string      { return filepath.Join(c.data, keyFileName) }
func (c *kekCase) fallbackFile() string { return filepath.Join(c.data, fallbackKeyFileName) }

func (c *kekCase) write(path, content string) {
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		c.t.Fatal(err)
	}
}

func (c *kekCase) resolve(v Vault) ResolvedKey {
	return Resolve(v, c.data, func(line string) { c.logs = append(c.logs, line) })
}

func (c *kekCase) assertNoKeyInLogs() {
	c.t.Helper()
	for _, line := range c.logs {
		for _, key := range []string{fileKeyHex, storedKeyHex} {
			if strings.Contains(strings.ToLower(line), key[:16]) {
				c.t.Fatalf("log line carries key material: %s", line)
			}
		}
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestKeychainHitInjectsTheStoredKey(t *testing.T) {
	c := newKEKCase(t)
	v := vaultWith(c.account(), storedKeyHex)
	r := c.resolve(v)
	if r != (ResolvedKey{Storage: StorageKeychain, StdinHex: storedKeyHex}) {
		t.Fatalf("resolved %+v", r)
	}
	if v.sets != 0 || exists(c.keyFile()) || len(c.logs) != 0 {
		t.Fatalf("sets %d, key file %v, logs %q", v.sets, exists(c.keyFile()), c.logs)
	}
}

func TestKeyFileMovesIntoTheKeychainAndIsDeleted(t *testing.T) {
	c := newKEKCase(t)
	c.write(c.keyFile(), strings.ToUpper(fileKeyHex)+"\n")
	v := newVault()
	r := c.resolve(v)
	if r.Storage != StorageKeychain || r.StdinHex != fileKeyHex {
		t.Fatalf("resolved %+v", r)
	}
	if string(v.entries[c.account()]) != fileKeyHex {
		t.Fatalf("entry %q", v.entries[c.account()])
	}
	if exists(c.keyFile()) {
		t.Fatal("local.key survived the move")
	}
	if len(c.logs) != 0 {
		t.Fatalf("logs %q", c.logs)
	}
}

func TestInterruptedMoveFinishesByDeletingTheMatchingFile(t *testing.T) {
	c := newKEKCase(t)
	c.write(c.keyFile(), fileKeyHex)
	v := vaultWith(c.account(), fileKeyHex)
	r := c.resolve(v)
	if r.Storage != StorageKeychain || r.StdinHex != fileKeyHex || v.sets != 0 || exists(c.keyFile()) {
		t.Fatalf("resolved %+v, sets %d", r, v.sets)
	}
}

func TestFirstStartGeneratesAKeychainKeyWithoutAFile(t *testing.T) {
	c := newKEKCase(t)
	v := newVault()
	r := c.resolve(v)
	stored := string(v.entries[c.account()])
	if r.Storage != StorageKeychain || len(stored) != 64 || strings.ToLower(stored) != stored || parseKey([]byte(stored)) == nil {
		t.Fatalf("resolved %+v, stored %q", r, stored)
	}
	if r.StdinHex != stored || exists(c.keyFile()) {
		t.Fatalf("stdin %q, stored %q", r.StdinHex, stored)
	}
	// The next start reads the same key back.
	if again := c.resolve(v); again.StdinHex != stored {
		t.Fatalf("second start got %q", again.StdinHex)
	}
}

func TestFailedKeychainWriteLetsCoreCreateTheFile(t *testing.T) {
	c := newKEKCase(t)
	v := newVault()
	v.failSet = true
	r := c.resolve(v)
	if r != coreDefault(StorageKeychainUnavailable) {
		t.Fatalf("resolved %+v", r)
	}
	if exists(c.keyFile()) {
		t.Fatal("the desktop wrote a key file")
	}
	if len(c.logs) != 1 {
		t.Fatalf("logs %q", c.logs)
	}
	c.assertNoKeyInLogs()

	// Once the keychain works again the file Core made moves in.
	c.write(c.keyFile(), fileKeyHex)
	r = c.resolve(newVault())
	if r.Storage != StorageKeychain || r.StdinHex != fileKeyHex || exists(c.keyFile()) {
		t.Fatalf("resolved %+v", r)
	}
}

func TestFailedOrDroppedWriteKeepsTheKeyFile(t *testing.T) {
	for name, v := range map[string]*fakeVault{
		"fail": {entries: map[string][]byte{}, failSet: true},
		"drop": {entries: map[string][]byte{}, dropSets: true},
	} {
		t.Run(name, func(t *testing.T) {
			c := newKEKCase(t)
			c.write(c.keyFile(), fileKeyHex)
			r := c.resolve(v)
			if r != coreDefault(StorageKeychainUnavailable) {
				t.Fatalf("resolved %+v", r)
			}
			if got := read(t, c.keyFile()); got != fileKeyHex {
				t.Fatalf("key file %q", got)
			}
			c.assertNoKeyInLogs()
		})
	}
}

func TestUnreadableKeychainUsesASeparateFallbackFile(t *testing.T) {
	c := newKEKCase(t)
	v := vaultWith(c.account(), storedKeyHex)
	v.failGet = true
	r := c.resolve(v)
	if r.Storage != StorageKeychainUnavailable || r.StdinHex != "" || r.KeyFile != c.fallbackFile() || v.sets != 0 {
		t.Fatalf("resolved %+v", r)
	}
	c.assertNoKeyInLogs()

	// An existing local.key is still used as Core's default.
	c.write(c.keyFile(), fileKeyHex)
	if r := c.resolve(v); r.KeyFile != "" || read(t, c.keyFile()) != fileKeyHex {
		t.Fatalf("resolved %+v", r)
	}

	// Before the data directory exists there is no data to protect.
	if r := Resolve(v, filepath.Join(c.data, "not-yet"), func(string) {}); r.KeyFile != "" {
		t.Fatalf("resolved %+v", r)
	}
}

func TestAnOutageKeyMovesIntoTheKeychainOnceItRecovers(t *testing.T) {
	// The keychain cannot be read and has no entry: Core gets the fallback
	// file and creates its key there.
	c := newKEKCase(t)
	outage := newVault()
	outage.failGet = true
	if r := c.resolve(outage); r.KeyFile != c.fallbackFile() {
		t.Fatalf("resolved %+v", r)
	}
	c.write(c.fallbackFile(), fileKeyHex)
	c.logs = nil

	// The keychain works again: the fallback key moves in, no new key.
	v := newVault()
	r := c.resolve(v)
	if r != keychainKey(parseKey([]byte(fileKeyHex))) || v.sets != 1 || string(v.entries[c.account()]) != fileKeyHex {
		t.Fatalf("resolved %+v, sets %d", r, v.sets)
	}
	if exists(c.fallbackFile()) || exists(c.keyFile()) || len(c.logs) != 0 {
		t.Fatalf("fallback %v, key file %v, logs %q", exists(c.fallbackFile()), exists(c.keyFile()), c.logs)
	}
	if again := c.resolve(v); again.StdinHex != fileKeyHex || v.sets != 1 {
		t.Fatalf("again %+v, sets %d", again, v.sets)
	}
}

func TestARefusedMoveKeepsCoreOnTheFallbackFile(t *testing.T) {
	for name, v := range map[string]*fakeVault{
		"fail": {entries: map[string][]byte{}, failSet: true},
		"drop": {entries: map[string][]byte{}, dropSets: true},
	} {
		t.Run(name, func(t *testing.T) {
			c := newKEKCase(t)
			c.write(c.fallbackFile(), fileKeyHex)
			r := c.resolve(v)
			if r != fallbackFile(c.fallbackFile()) {
				t.Fatalf("resolved %+v", r)
			}
			if read(t, c.fallbackFile()) != fileKeyHex || exists(c.keyFile()) {
				t.Fatal("Core was sent to a new local.key")
			}
			c.assertNoKeyInLogs()
		})
	}

	// A damaged fallback file is left for Core to report; nothing moves.
	c := newKEKCase(t)
	c.write(c.fallbackFile(), fileKeyHex[:40])
	v := newVault()
	if r := c.resolve(v); r.KeyFile != c.fallbackFile() || v.sets != 0 || !exists(c.fallbackFile()) {
		t.Fatalf("resolved %+v, sets %d", r, v.sets)
	}
}

func TestTheKeychainEntryWinsOverAFallbackFile(t *testing.T) {
	// The same key: a move stopped after the keychain write.
	c := newKEKCase(t)
	c.write(c.fallbackFile(), storedKeyHex)
	if r := c.resolve(vaultWith(c.account(), storedKeyHex)); r.StdinHex != storedKeyHex || exists(c.fallbackFile()) || len(c.logs) != 0 {
		t.Fatalf("resolved %+v, logs %q", r, c.logs)
	}

	// Another key never replaces the entry; the file stays.
	c = newKEKCase(t)
	c.write(c.fallbackFile(), fileKeyHex)
	v := vaultWith(c.account(), storedKeyHex)
	r := c.resolve(v)
	if r.Storage != StorageKeychain || r.StdinHex != storedKeyHex || v.sets != 0 || read(t, c.fallbackFile()) != fileKeyHex || len(c.logs) != 1 {
		t.Fatalf("resolved %+v, sets %d, logs %q", r, v.sets, c.logs)
	}
	c.assertNoKeyInLogs()

	// With local.key and the fallback file both there, Core has been using
	// local.key: it moves in and the other file stays.
	c = newKEKCase(t)
	c.write(c.keyFile(), fileKeyHex)
	c.write(c.fallbackFile(), storedKeyHex)
	r = c.resolve(newVault())
	if r.StdinHex != fileKeyHex || exists(c.keyFile()) || read(t, c.fallbackFile()) != storedKeyHex {
		t.Fatalf("resolved %+v", r)
	}
	c.assertNoKeyInLogs()
}

func TestADifferentKeyFileNeverReplacesTheEntry(t *testing.T) {
	c := newKEKCase(t)
	c.write(c.keyFile(), fileKeyHex)
	v := vaultWith(c.account(), storedKeyHex)
	r := c.resolve(v)
	if r != (ResolvedKey{Storage: StorageKeychain, StdinHex: storedKeyHex}) || v.sets != 0 || len(v.entries) != 1 {
		t.Fatalf("resolved %+v, sets %d", r, v.sets)
	}
	if string(v.entries[c.account()]) != storedKeyHex || read(t, c.keyFile()) != fileKeyHex || len(c.logs) != 1 {
		t.Fatalf("entry %q, logs %q", v.entries[c.account()], c.logs)
	}
	c.assertNoKeyInLogs()
}

func TestInvalidEntriesAndFilesAreNeverOverwritten(t *testing.T) {
	// A damaged keychain entry: use a file, leave the entry.
	c := newKEKCase(t)
	v := vaultWith(c.account(), "not a key")
	if r := c.resolve(v); r.Storage != StorageKeychainUnavailable || v.sets != 0 || string(v.entries[c.account()]) != "not a key" {
		t.Fatalf("resolved %+v", r)
	}

	// A damaged key file and no entry: Core reports it, nothing moves.
	c = newKEKCase(t)
	c.write(c.keyFile(), fileKeyHex[:40])
	v = newVault()
	if r := c.resolve(v); r != coreDefault(StorageFile) || v.sets != 0 || read(t, c.keyFile()) != fileKeyHex[:40] {
		t.Fatalf("resolved %+v", r)
	}

	// A damaged key file beside a good entry: the entry wins, the file stays.
	c = newKEKCase(t)
	c.write(c.keyFile(), "zz")
	if r := c.resolve(vaultWith(c.account(), storedKeyHex)); r.StdinHex != storedKeyHex || !exists(c.keyFile()) {
		t.Fatalf("resolved %+v", r)
	}
	c.assertNoKeyInLogs()
}

func TestKeychainAccountIsStableShortAndPerDirectory(t *testing.T) {
	first := KeychainAccount("/Users/example/Library/Application Support/a")
	if len(first) != 16 || strings.ToLower(first) != first {
		t.Fatalf("account %q", first)
	}
	if first != KeychainAccount("/Users/example/Library/Application Support/a") {
		t.Fatal("account is not stable")
	}
	if first == KeychainAccount("/Users/example/Library/Application Support/b") {
		t.Fatal("two directories share an account")
	}
	// SHA-256("/") begins 8a5edab282632443.
	if got := KeychainAccount("/"); got != "8a5edab282632443" {
		t.Fatalf("account of / = %q", got)
	}
}

func TestBuildsOffMacOSNeverUseTheKeychain(t *testing.T) {
	c := newKEKCase(t)
	if r := ResolveForStart(c.data, func(string) {}); r != coreDefault(StorageFile) {
		t.Fatalf("resolved %+v", r)
	}
}

func TestHexRoundTripsAndRejectsBadDigits(t *testing.T) {
	key := parseKey([]byte("  " + fileKeyHex + "\r\n"))
	if key == nil || keychainKey(key).StdinHex != fileKeyHex {
		t.Fatalf("key %x", key)
	}
	if parseKey([]byte(strings.Repeat("g", 64))) != nil || parseKey([]byte(fileKeyHex[:62])) != nil {
		t.Fatal("accepted a bad key")
	}
}
