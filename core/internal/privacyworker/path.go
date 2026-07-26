package privacyworker

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func SiblingExecutablePath() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve core executable: %w", err)
	}
	base := filepath.Base(executable)
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = filepath.Ext(base)
		base = strings.TrimSuffix(base, suffix)
	}
	targetSuffix := ""
	if strings.HasPrefix(base, "astrlink-core") {
		targetSuffix = strings.TrimPrefix(base, "astrlink-core")
	}
	return filepath.Join(
		filepath.Dir(executable),
		"astrlink-privacy-worker"+targetSuffix+suffix,
	), nil
}
