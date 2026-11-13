package updates

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	// maxManifestBytes and maxPackageBytes are MyGo's limits of what an app
	// downloads (internal/update).
	maxManifestBytes = 1 << 20
	maxPackageBytes  = 1 << 30
)

// manifest is what `mygo build` publishes as update-<target>.json.
type manifest struct {
	Version   string  `json:"version"`
	Notes     string  `json:"notes,omitempty"`
	Date      string  `json:"date,omitempty"`
	URL       string  `json:"url"`
	Size      int64   `json:"size"`
	Signature string  `json:"signature"`
	Deltas    []delta `json:"deltas,omitempty"`
}

// delta updates the app of version From with a smaller download.
type delta struct {
	From      string `json:"from"`
	URL       string `json:"url"`
	Size      int64  `json:"size"`
	Signature string `json:"signature"`
}

// packageFile is a file of an update, signed like the archive.
type packageFile struct {
	URL       string
	Size      int64
	Signature string
}

// parseManifest reads a manifest of the release tagged tag: its version
// must be version and the files it names must be the release's.
func parseManifest(body []byte, tag string, version semVersion) (manifest, error) {
	var m manifest
	if err := json.Unmarshal(body, &m); err != nil {
		return manifest{}, failure("manifest", "%v", err)
	}
	v, ok := parseVersion(m.Version)
	if !ok || v.compare(version) != 0 {
		return manifest{}, failure("manifest", "Update manifest does not match the selected release")
	}
	if !officialAsset(m.URL, tag) || m.Signature == "" || m.Size <= 0 || m.Size > maxPackageBytes {
		return manifest{}, failure("manifest", "Update manifest does not name a supported release asset")
	}
	for _, d := range m.Deltas {
		if !officialAsset(d.URL, tag) || d.Signature == "" || d.Size <= 0 || d.Size > maxPackageBytes {
			return manifest{}, failure("manifest", "Update manifest does not name a supported release asset")
		}
	}
	return m, nil
}

// firstFile is what installing the update downloads first: the delta of
// the running version when there is one, which MyGo's updater tries before
// the whole archive, else the archive.
func (m manifest) firstFile(current string) packageFile {
	for _, d := range m.Deltas {
		if d.From == current {
			return packageFile{URL: d.URL, Size: d.Size, Signature: d.Signature}
		}
	}
	return packageFile{URL: m.URL, Size: m.Size, Signature: m.Signature}
}

// parsePublicKey decodes the base64 Ed25519 key of updates.publicKey.
func parsePublicKey(text string) (ed25519.PublicKey, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(text))
	if err != nil || len(key) != ed25519.PublicKeySize {
		return nil, errors.New("invalid update public key")
	}
	return ed25519.PublicKey(key), nil
}

// verifyDigest checks the signature of a file's SHA-256 digest, as MyGo
// signs update archives and deltas.
func verifyDigest(key ed25519.PublicKey, digest []byte, signature string) error {
	sig, err := base64.StdEncoding.DecodeString(signature)
	if err != nil || len(key) != ed25519.PublicKeySize || !ed25519.Verify(key, digest, sig) {
		return errors.New("the update is not signed with the app's key")
	}
	return nil
}

// cache keeps the verified download of one update. The directory belongs
// to the updater alone.
type cache struct{ dir string }

// path is the file of f: named after its URL, size and signature, so a
// republished file never reads as the earlier one.
func (c cache) path(f packageFile) string {
	sum := sha256.Sum256(fmt.Appendf(nil, "%s\x00%d\x00%s", f.URL, f.Size, f.Signature))
	return filepath.Join(c.dir, hex.EncodeToString(sum[:])+".update")
}

// verified checks the cached file of f against its size and signature.
func (c cache) verified(f packageFile, key ed25519.PublicKey) error {
	file, err := os.Open(c.path(f))
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.Size() != f.Size {
		return fmt.Errorf("the cached update has %d bytes, want %d", info.Size(), f.Size)
	}
	sum := sha256.New()
	if _, err := io.Copy(sum, file); err != nil {
		return err
	}
	return verifyDigest(key, sum.Sum(nil), f.Signature)
}

// clear removes cached and partial downloads.
func (c cache) clear() { c.clearExcept("") }

// clearExcept removes cached and partial downloads but keep.
func (c cache) clearExcept(keep string) {
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		path := filepath.Join(c.dir, entry.Name())
		if ext := filepath.Ext(entry.Name()); (ext == ".update" || ext == ".part") && path != keep {
			_ = os.Remove(path)
		}
	}
}
