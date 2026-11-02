// Package fakeclaude is a scripted stand-in for the claude CLI. Tests link
// the running test binary as an executable named claude; TestMain calls Main,
// which runs the fake when the binary was started under that name.
package fakeclaude

import (
	"bufio"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// LoginURL is the sign-in page the fake prints.
const LoginURL = "https://claude.com/cai/oauth/authorize?code=true&client_id=fake&state=fake"

// Login behaviours.
const (
	// LoginCode waits for a pasted code on stdin.
	LoginCode = ""
	// LoginBrowser completes at once, as when the browser callback succeeds.
	LoginBrowser = "browser"
	// LoginNoURL fails before showing a page.
	LoginNoURL = "no-url"
)

// Account is the signed-in account `auth status --json` reports.
type Account struct {
	Email            string `json:"email"`
	OrganizationID   string `json:"orgId"`
	OrganizationName string `json:"orgName"`
	SubscriptionType string `json:"subscriptionType"`
}

// Settings script the fake. They live next to the executable.
type Settings struct {
	Version string  `json:"version"`
	Login   string  `json:"login"`
	Code    string  `json:"code"`
	Account Account `json:"account"`
	// Token is the Authorization a serving fake sends; empty uses FakeToken.
	Token string `json:"token"`
	// OwnTraffic makes the fake send its own HTTPS requests, as the real CLI
	// does: a token exchange after login, and a token refresh and one more
	// request when serving starts.
	OwnTraffic bool `json:"own_traffic"`
}

// Hosts of the fake's own HTTPS requests. They never resolve, and the fake
// only reaches them through HTTPS_PROXY.
const (
	TokenHost   = "platform.claude.test"
	RefreshHost = "api.claude.test"
	OtherHost   = "downloads.claude.test"
)

// OwnTrafficFile lists the outcome of each of the fake's own requests, one
// "host result" line each.
func OwnTrafficFile(configDir string) string {
	return filepath.Join(configDir, "fake-own-traffic.txt")
}

// reachOut sends one HTTPS request of the CLI's own through HTTPS_PROXY and
// records its outcome. Without a proxy it sends nothing.
func reachOut(settings Settings, configDir, host string) {
	if !settings.OwnTraffic || configDir == "" || os.Getenv("HTTPS_PROXY") == "" {
		return
	}
	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		// The capture servers behind the test proxy use test certificates.
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}}
	result := "ok"
	response, err := client.Get("https://" + host + "/")
	if err != nil {
		result = "error"
	} else {
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK {
			result = response.Status
		}
	}
	file, err := os.OpenFile(OwnTrafficFile(configDir), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer file.Close()
	fmt.Fprintf(file, "%s %s\n", host, result)
}

const settingsName = "fake-claude.json"

// Main runs the fake and exits when the binary was started as claude.
func Main() {
	if strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe") != "claude" {
		return
	}
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// Install links the test binary as dir/claude with the given settings and
// returns the executable path.
func Install(t testing.TB, dir string, settings Settings) string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	name := "claude"
	if runtime.GOOS == "windows" {
		name = "claude.exe"
	}
	target := filepath.Join(dir, name)
	if runtime.GOOS == "windows" {
		raw, err := os.ReadFile(self)
		if err != nil {
			t.Fatal(err)
		}
		err = os.WriteFile(target, raw, 0o755)
		if err != nil {
			t.Fatal(err)
		}
	} else if err := os.Symlink(self, target); err != nil {
		t.Fatal(err)
	}
	Configure(t, dir, settings)
	return target
}

// Configure replaces the settings of an installed fake.
func Configure(t testing.TB, dir string, settings Settings) {
	t.Helper()
	if settings.Version == "" {
		settings.Version = "2.1.300"
	}
	raw, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, settingsName), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

// EnvFile is where the fake records the environment of a subcommand run in
// configDir, one VAR=value per line.
func EnvFile(configDir, subcommand string) string {
	return filepath.Join(configDir, "fake-env-"+subcommand+".txt")
}

// AccountFile is where the fake keeps its signed-in account.
func AccountFile(configDir string) string {
	return filepath.Join(configDir, "fake-account.json")
}

func loadSettings() Settings {
	var settings Settings
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(os.Args[0]), settingsName))
	if err == nil {
		_ = json.Unmarshal(raw, &settings)
	}
	return settings
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	settings := loadSettings()
	if len(args) == 1 && args[0] == "--version" {
		fmt.Fprintf(stdout, "%s (Claude Code)\n", settings.Version)
		return 0
	}
	if serving(args) {
		return serve(settings, args, stdin, stdout, stderr)
	}
	configDir := os.Getenv("CLAUDE_CONFIG_DIR")
	if len(args) >= 2 && args[0] == "auth" {
		recordEnv(configDir, args[1])
		switch args[1] {
		case "status":
			return status(configDir, stdout)
		case "login":
			return login(settings, configDir, stdin, stdout)
		case "logout":
			_ = os.Remove(AccountFile(configDir))
			fmt.Fprintln(stdout, "Successfully logged out from your Anthropic account.")
			return 0
		}
	}
	fmt.Fprintf(stderr, "fake claude: unsupported arguments %q\n", args)
	return 2
}

func recordEnv(configDir, subcommand string) {
	if configDir == "" {
		return
	}
	_ = os.WriteFile(EnvFile(configDir, subcommand), []byte(strings.Join(os.Environ(), "\n")+"\n"), 0o600)
}

func status(configDir string, stdout io.Writer) int {
	raw, err := os.ReadFile(AccountFile(configDir))
	if err != nil {
		fmt.Fprintln(stdout, `{"loggedIn":false,"authMethod":"none","apiProvider":"firstParty"}`)
		return 1
	}
	var account Account
	if err := json.Unmarshal(raw, &account); err != nil {
		return 1
	}
	document := map[string]any{
		"loggedIn": true, "authMethod": "claude.ai", "apiProvider": "firstParty",
		"email": account.Email, "orgId": account.OrganizationID, "orgName": account.OrganizationName,
		"subscriptionType": account.SubscriptionType,
	}
	_ = json.NewEncoder(stdout).Encode(document)
	return 0
}

func login(settings Settings, configDir string, stdin io.Reader, stdout io.Writer) int {
	if settings.Login == LoginNoURL {
		fmt.Fprintln(stdout, "Error: unable to reach the sign-in service")
		return 1
	}
	fmt.Fprintln(stdout, "Opening browser to sign in…")
	// The real CLI wraps the page in an OSC 8 hyperlink.
	fmt.Fprintf(stdout, "If the browser didn't open, visit: \x1b]8;;%s\x07%s\x1b]8;;\x07\n", LoginURL, LoginURL)
	fmt.Fprint(stdout, "Paste code here if prompted > ")
	if settings.Login != LoginBrowser {
		line, err := bufio.NewReader(stdin).ReadString('\n')
		if err != nil {
			fmt.Fprintln(stdout, "\nLogin cancelled")
			return 1
		}
		if strings.TrimSpace(line) != settings.Code {
			fmt.Fprintln(stdout, "\nOAuth error: Invalid code")
			return 1
		}
	}
	reachOut(settings, configDir, TokenHost)
	raw, _ := json.Marshal(settings.Account)
	if err := os.WriteFile(AccountFile(configDir), raw, 0o600); err != nil {
		return 1
	}
	fmt.Fprintln(stdout, "\nLogin successful.")
	return 0
}
