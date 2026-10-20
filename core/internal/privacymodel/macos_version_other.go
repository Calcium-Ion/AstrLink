//go:build !darwin

package privacymodel

func macOSVersion() (string, string) {
	return "", ""
}
