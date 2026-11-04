package accountauth

import (
	"fmt"
	"strings"

	"github.com/MoeForge/moego/base64"
)

func decodeRawURLBase64(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, fmt.Errorf("empty base64url segment")
	}
	if decoded, err := base64.RawURLEncoding.DecodeString(value); err == nil {
		return decoded, nil
	}
	return base64.URLEncoding.DecodeString(value)
}
