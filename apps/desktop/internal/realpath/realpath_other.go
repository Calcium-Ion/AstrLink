//go:build !windows

package realpath

func finalPath(string) (string, bool) { return "", false }
