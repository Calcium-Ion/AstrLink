package clientconfig

import "testing"

func TestLocalOriginAcceptsOnlyTheLoopbackGatewayRoot(t *testing.T) {
	for url, want := range map[string]string{
		"http://127.0.0.1:18317/": "http://127.0.0.1:18317",
		"http://localhost:18317":  "http://localhost:18317",
		"HTTP://LocalHost:18317/": "http://localhost:18317",
		"http://127.0.0.1:80/":    "http://127.0.0.1",
		"http://127.0.0.1:018317": "http://127.0.0.1:18317",
	} {
		if got, err := LocalOrigin(url); err != nil || got != want {
			t.Errorf("%s: %q %v", url, got, err)
		}
	}
	for _, url := range []string{
		"https://127.0.0.1:1",
		"http://[::1]:1",
		"http://localhost.example:1",
		"http://localhost:1/v1",
		"http://127.0.0.1:1/v1",
		"http://user@127.0.0.1:1",
		"http://127.0.0.1:1?x",
		"http://127.0.0.1:1?",
		"http://127.0.0.1:1#x",
		"http://127.0.0.1:1#",
		"not a url",
		"http://127.0.0.1:65536",
	} {
		if got, err := LocalOrigin(url); err == nil {
			t.Errorf("%s: accepted as %q", url, got)
		}
	}
}
