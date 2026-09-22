package contract

import "testing"

func TestServiceProxyValidation(t *testing.T) {
	for _, value := range []string{"http://localhost:8080", "https://proxy.example", "socks5://[::1]:1080"} {
		if err := (&ServiceProxy{Mode: "custom", URL: value}).Validate("service_one"); err != nil {
			t.Fatal(err)
		}
	}
	for _, value := range []string{"http://user:password@host", "http://host/path", "http://host?secret", "http://host#secret", "http://host:0", "http://host:65536", "http://host:", "ftp://host", "host:123"} {
		if err := ValidateProxyURL(value); err == nil {
			t.Errorf("accepted invalid proxy %q", value)
		}
	}
	for _, mode := range []string{"inherit", "direct"} {
		if err := (&ServiceProxy{Mode: mode}).Validate("service_one"); err != nil {
			t.Fatal(err)
		}
		if err := (&ServiceProxy{Mode: mode, URL: "http://proxy"}).Validate("service_one"); err == nil {
			t.Fatal("non-custom address accepted")
		}
	}
	if err := (&ServiceProxy{Mode: "custom", URL: "http://proxy", CredentialRef: "local://service-proxy/service_two"}).Validate("service_one"); err == nil {
		t.Fatal("cross-instance credential accepted")
	}
}
