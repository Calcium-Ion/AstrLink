package core

import (
	"os"
	"slices"
	"strings"
)

const maxReportedBackups = 100

// DataBackupFile is a file in the data directory whose name matches
// *.bak*. Such copies are made by hand, outside AstrLink, and can hold every
// secret and captured body in plain text. Settings points them out;
// AstrLink never deletes them.
type DataBackupFile struct {
	Name         string  `json:"name"`
	SizeBytes    uint64  `json:"size_bytes"`
	ModifiedUnix *uint64 `json:"modified_unix"`
}

// ScanBackupFiles lists backup-looking regular files directly inside
// dataDirectory, newest first. Symlinks and directories are skipped, and an
// unreadable directory reports nothing rather than failing the settings
// snapshot.
func ScanBackupFiles(dataDirectory string) []DataBackupFile {
	entries, err := os.ReadDir(dataDirectory)
	if err != nil {
		return []DataBackupFile{}
	}
	files := []DataBackupFile{}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.Contains(strings.ToLower(name), ".bak") {
			continue
		}
		// DirEntry.Info does not follow symlinks.
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		file := DataBackupFile{Name: name, SizeBytes: uint64(info.Size())}
		if seconds := info.ModTime().Unix(); seconds >= 0 {
			modified := uint64(seconds)
			file.ModifiedUnix = &modified
		}
		files = append(files, file)
	}
	slices.SortFunc(files, func(left, right DataBackupFile) int {
		l, r := left.ModifiedUnix, right.ModifiedUnix
		switch {
		case l == nil && r != nil:
			return 1
		case l != nil && r == nil:
			return -1
		case l != nil && r != nil && *l != *r:
			if *l > *r {
				return -1
			}
			return 1
		}
		return strings.Compare(left.Name, right.Name)
	})
	if len(files) > maxReportedBackups {
		files = files[:maxReportedBackups]
	}
	return files
}
