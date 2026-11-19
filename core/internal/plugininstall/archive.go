package plugininstall

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

// archiveLimits bound what an archive may write.
type archiveLimits struct {
	files int
	bytes int64
}

// archiveCommit is the commit a repository archive holds: git archive
// names it in the archive's global header and dates every entry with the
// time it was made.
type archiveCommit struct {
	id          string
	committedAt time.Time
}

// taskExtras are the files outside a plugin's directory a task plugin's
// install reads, by their path in the repository: the marketplace index at
// its root, and the icon beside the plugin's versions.
func taskExtras(subdir string) map[string]string {
	extras := map[string]string{"index.json": "index.json"}
	if parent := path.Dir(subdir); subdir != "" && parent != "." {
		for _, name := range iconFiles {
			extras[parent+"/"+name] = name
		}
	}
	return extras
}

// maxExtraBytes bounds a file of extras.
const maxExtraBytes = 8 << 20

// extractSource writes the files of a repository archive under subdir into
// dest, dropping the archive's top directory, and the files extras names
// into extrasDest. Links, devices and what a repository should not hold,
// node_modules and .git, stay out. found is false when the archive has
// nothing under subdir.
func extractSource(reader io.Reader, subdir, dest string, limits archiveLimits, extras map[string]string, extrasDest string) (found bool, commit archiveCommit, err error) {
	written := &archiveCount{limits: limits}
	headers := func(header *tar.Header) {
		switch {
		case header.Typeflag == tar.TypeXGlobalHeader:
			commit.id = strings.ToLower(strings.TrimSpace(header.PAXRecords["comment"]))
		case commit.committedAt.IsZero() && !header.ModTime.IsZero():
			commit.committedAt = header.ModTime.UTC()
		}
	}
	extra := &archiveCount{limits: archiveLimits{files: len(extras), bytes: int64(len(extras)) * maxExtraBytes}}
	err = walkArchive(reader, headers, func(name string, header *tar.Header, body io.Reader) error {
		if target, ok := extras[name]; ok && header.Size <= maxExtraBytes {
			if err := extra.write(extrasDest, target, header, body); err != nil {
				return err
			}
			return nil
		}
		if subdir != "" {
			if name != subdir && !strings.HasPrefix(name, subdir+"/") {
				return nil
			}
			name = strings.TrimPrefix(strings.TrimPrefix(name, subdir), "/")
		}
		found = true
		if name == "" || skippedSource(name) {
			return nil
		}
		return written.write(dest, name, header, body)
	})
	return found, commit, err
}

// skippedSource reports whether a repository file stays out of the build:
// what installs and version control leave in a checkout.
func skippedSource(name string) bool {
	for segment := range strings.SplitSeq(name, "/") {
		if segment == "node_modules" || segment == ".git" {
			return true
		}
	}
	return false
}

// packageFiles is what extractPackage found in a package.
type packageFiles struct {
	bytes int64
	// native names a native module the package holds, "" for none.
	native string
}

// extractPackage writes an npm package archive into dest, dropping the
// archive's top directory, package/ as a rule.
func extractPackage(reader io.Reader, dest string, limits archiveLimits) (packageFiles, error) {
	written := &archiveCount{limits: limits}
	var files packageFiles
	err := walkArchive(reader, nil, func(name string, header *tar.Header, body io.Reader) error {
		if name == "" {
			return nil
		}
		if strings.HasSuffix(name, ".node") || name == "binding.gyp" {
			files.native = name
		}
		return written.write(dest, name, header, body)
	})
	files.bytes = written.total
	return files, err
}

// walkArchive calls visit for each entry of a gzipped tar archive with its
// name past the top directory, cleaned, after headers, when set, saw its
// header as the archive has it. An entry whose name could reach outside the
// directory it is written to fails the archive.
func walkArchive(reader io.Reader, headers func(*tar.Header), visit func(name string, header *tar.Header, body io.Reader) error) error {
	unzipped, err := gzip.NewReader(reader)
	if err != nil {
		return err
	}
	defer unzipped.Close()
	archive := tar.NewReader(unzipped)
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if headers != nil {
			headers(header)
		}
		switch header.Typeflag {
		case tar.TypeXGlobalHeader, tar.TypeXHeader, tar.TypeGNULongName, tar.TypeGNULongLink:
			continue
		}
		name := header.Name
		if strings.ContainsAny(name, "\\\x00") || strings.HasPrefix(name, "/") {
			return fail(contract.PluginInstallUnavailable, "the archive names a file outside its directory")
		}
		if strings.Contains(name, ":") {
			// Windows cannot name such a file; nothing a build needs does.
			continue
		}
		_, rest, _ := strings.Cut(strings.TrimPrefix(name, "./"), "/")
		if rest == "" {
			continue
		}
		clean := path.Clean(rest)
		if clean == ".." || strings.HasPrefix(clean, "../") {
			return fail(contract.PluginInstallUnavailable, "the archive names a file outside its directory")
		}
		if clean == "." {
			clean = ""
		}
		if err := visit(clean, header, archive); err != nil {
			return err
		}
	}
}

// archiveCount writes an archive's files and directories, within limits.
type archiveCount struct {
	limits archiveLimits
	files  int
	total  int64
}

func (count *archiveCount) write(dest, name string, header *tar.Header, body io.Reader) error {
	target := filepath.Join(dest, filepath.FromSlash(name))
	switch header.Typeflag {
	case tar.TypeDir:
		return os.MkdirAll(target, 0o755)
	case tar.TypeReg, tar.TypeRegA:
	default:
		// Links, devices and fifos never leave the archive.
		return nil
	}
	count.files++
	if count.files > count.limits.files {
		return fail(contract.PluginInstallTooLarge, "files")
	}
	if header.Size < 0 || count.total+header.Size > count.limits.bytes {
		return fail(contract.PluginInstallTooLarge, "bytes")
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	copied, err := io.Copy(file, io.LimitReader(body, header.Size))
	closeErr := file.Close()
	count.total += copied
	if err != nil {
		return err
	}
	return closeErr
}
