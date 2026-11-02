package claudecli

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/claudecli/fakeclaude"
)

func TestMain(m *testing.M) {
	fakeclaude.Main()
	os.Exit(m.Run())
}

type fakeCLI struct {
	binDir   string
	accounts Accounts
}

func newFakeCLI(t *testing.T, settings fakeclaude.Settings) fakeCLI {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake claude is linked by symlink")
	}
	binDir := t.TempDir()
	binary := fakeclaude.Install(t, binDir, settings)
	root := t.TempDir()
	return fakeCLI{binDir: binDir, accounts: Accounts{
		Locator: &Locator{Configured: binary},
		Layout:  Layout{Root: root, WorkDir: filepath.Join(root, "work")},
		Environ: []string{
			"PATH=/usr/bin:/bin", "HOME=" + root, "HTTPS_PROXY=http://proxy.test:8080",
			"ANTHROPIC_API_KEY=sk-parent", "ANTHROPIC_BASE_URL=http://parent.test",
			"CLAUDE_CODE_OAUTH_TOKEN=parent-token", "CLAUDE_CONFIG_DIR=/parent/config", "LC_ALL=C",
		},
	}}
}

func readEnv(t *testing.T, configDir, subcommand string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(fakeclaude.EnvFile(configDir, subcommand))
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		name, value, _ := strings.Cut(line, "=")
		env[name] = value
	}
	return env
}

type completion struct {
	id     contract.ServiceID
	status Status
}

func recordCompletions(err error) (LoginCompletion, chan completion) {
	completed := make(chan completion, 1)
	return func(_ context.Context, id contract.ServiceID, _ contract.AuthorizationSessionID, status Status) error {
		completed <- completion{id: id, status: status}
		return err
	}, completed
}

const testAccountID contract.ServiceID = "service_official"

var testAccount = fakeclaude.Account{
	Email: "person@example.com", OrganizationID: "org-1", OrganizationName: "Example", SubscriptionType: "max",
}

func TestLoginWithPastedCode(t *testing.T) {
	cli := newFakeCLI(t, fakeclaude.Settings{Code: "good-code", Account: testAccount})
	complete, completed := recordCompletions(nil)
	logins := NewLogins(cli.accounts, complete, nil)

	session, err := logins.Begin(context.Background(), testAccountID)
	if err != nil {
		t.Fatal(err)
	}
	if session.Flow != contract.AuthorizationFlowClaudeCLI || session.Status != contract.AuthorizationSessionStatusPending {
		t.Fatalf("session = %+v", session)
	}
	if session.AuthorizationURL != fakeclaude.LoginURL {
		t.Fatalf("authorization URL = %q, want %q", session.AuthorizationURL, fakeclaude.LoginURL)
	}
	if err := session.Validate(); err != nil {
		t.Fatalf("session does not validate: %v", err)
	}
	if _, err := logins.SubmitCode(testAccountID, "authorization_session_other", "good-code"); !errors.Is(err, ErrLoginNotFound) {
		t.Fatalf("SubmitCode(other session) error = %v", err)
	}
	if _, err := logins.SubmitCode(testAccountID, session.ID, "two\nlines"); err == nil {
		t.Fatal("SubmitCode accepted a code with a line break")
	}
	if _, err := logins.SubmitCode(testAccountID, session.ID, " good-code "); err != nil {
		t.Fatal(err)
	}
	<-logins.Done(testAccountID)

	got := <-completed
	if got.id != testAccountID || !got.status.LoggedIn || got.status.Email != testAccount.Email ||
		got.status.SubscriptionType != "max" || got.status.AccountID() != "org-1:person@example.com" {
		t.Fatalf("completion = %+v", got)
	}
	final, ok := logins.Get(testAccountID)
	if !ok || final.Status != contract.AuthorizationSessionStatusCompleted || final.AuthorizationURL != "" || final.Error != nil {
		t.Fatalf("final session = %+v", final)
	}
	if err := final.Validate(); err != nil {
		t.Fatalf("final session does not validate: %v", err)
	}

	configDir, _ := cli.accounts.Layout.ConfigDir(testAccountID)
	env := readEnv(t, configDir, "login")
	if env["CLAUDE_CONFIG_DIR"] != configDir {
		t.Fatalf("CLAUDE_CONFIG_DIR = %q, want %q", env["CLAUDE_CONFIG_DIR"], configDir)
	}
	if env["LC_ALL"] != "C" {
		t.Fatalf("login env lost the user's settings: %v", env)
	}
	// The CLI's own traffic goes through its tunnel, not the parent's proxy.
	tunnel, err := url.Parse(env["HTTPS_PROXY"])
	if err != nil || tunnel.Hostname() != "127.0.0.1" || tunnel.User == nil || env["https_proxy"] != env["HTTPS_PROXY"] ||
		env["NO_PROXY"] != "127.0.0.1,localhost,::1" || env["no_proxy"] != env["NO_PROXY"] {
		t.Fatalf("login proxy env = %v", env)
	}
	// The tunnel ends with the login.
	if conn, err := net.Dial("tcp", tunnel.Host); err == nil {
		_ = conn.Close()
		t.Fatal("the login's tunnel outlived it")
	}
	for name := range env {
		if strings.HasPrefix(name, "ANTHROPIC_") || name == "CLAUDE_CODE_OAUTH_TOKEN" {
			t.Fatalf("login env leaked %s", name)
		}
	}
	if info, err := os.Stat(configDir); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("config dir mode = %v, %v", info, err)
	}
}

func TestLoginCompletesThroughBrowser(t *testing.T) {
	cli := newFakeCLI(t, fakeclaude.Settings{Login: fakeclaude.LoginBrowser, Account: testAccount})
	complete, completed := recordCompletions(nil)
	logins := NewLogins(cli.accounts, complete, nil)

	if _, err := logins.Begin(context.Background(), testAccountID); err != nil {
		t.Fatal(err)
	}
	<-logins.Done(testAccountID)
	if got := <-completed; got.status.Email != testAccount.Email {
		t.Fatalf("completion = %+v", got)
	}
	if final, _ := logins.Get(testAccountID); final.Status != contract.AuthorizationSessionStatusCompleted {
		t.Fatalf("final session = %+v", final)
	}
}

func TestCodeAfterTheBrowserSignInWaitsForItsOutcome(t *testing.T) {
	cli := newFakeCLI(t, fakeclaude.Settings{Login: fakeclaude.LoginBrowser, Account: testAccount})
	completing, release := make(chan struct{}), make(chan struct{})
	logins := NewLogins(cli.accounts, func(context.Context, contract.ServiceID, contract.AuthorizationSessionID, Status) error {
		close(completing)
		<-release
		return nil
	}, nil)

	session, err := logins.Begin(context.Background(), testAccountID)
	if err != nil {
		t.Fatal(err)
	}
	// The CLI has exited through the browser; its sign-in is not recorded yet.
	<-completing
	submitted, err := logins.SubmitCode(testAccountID, session.ID, "late-code")
	close(release)
	if err != nil {
		t.Fatalf("a code pasted as the browser sign-in finished failed: %v", err)
	}
	if submitted.ID != session.ID {
		t.Fatalf("submitted session = %+v", submitted)
	}
	<-logins.Done(testAccountID)
	if final, _ := logins.Get(testAccountID); final.Status != contract.AuthorizationSessionStatusCompleted {
		t.Fatalf("final session = %+v", final)
	}
}

func TestLoginWithWrongCodeFails(t *testing.T) {
	cli := newFakeCLI(t, fakeclaude.Settings{Code: "good-code", Account: testAccount})
	complete, completed := recordCompletions(nil)
	logins := NewLogins(cli.accounts, complete, nil)

	session, err := logins.Begin(context.Background(), testAccountID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := logins.SubmitCode(testAccountID, session.ID, "bad-code"); err != nil {
		t.Fatal(err)
	}
	<-logins.Done(testAccountID)
	select {
	case got := <-completed:
		t.Fatalf("wrong code completed the login: %+v", got)
	default:
	}
	final, _ := logins.Get(testAccountID)
	if final.Status != contract.AuthorizationSessionStatusFailed || final.Error == nil ||
		final.Error.Code != ErrCodeLoginFailed || final.Error.Message != "OAuth error: Invalid code" {
		t.Fatalf("final session = %+v (error %+v)", final, final.Error)
	}
	if err := final.Validate(); err != nil {
		t.Fatalf("final session does not validate: %v", err)
	}
	if _, err := logins.SubmitCode(testAccountID, session.ID, "good-code"); !errors.Is(err, ErrLoginNotPending) {
		t.Fatalf("SubmitCode after failure error = %v", err)
	}
}

func TestLoginCancelStopsTheCLI(t *testing.T) {
	cli := newFakeCLI(t, fakeclaude.Settings{Code: "good-code", Account: testAccount})
	complete, completed := recordCompletions(nil)
	logins := NewLogins(cli.accounts, complete, nil)

	if _, err := logins.Begin(context.Background(), testAccountID); err != nil {
		t.Fatal(err)
	}
	cancelled, err := logins.Cancel(testAccountID)
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Status != contract.AuthorizationSessionStatusCancelled {
		t.Fatalf("cancelled session = %+v", cancelled)
	}
	<-logins.Done(testAccountID)
	select {
	case got := <-completed:
		t.Fatalf("cancelled login completed: %+v", got)
	default:
	}
	if final, _ := logins.Get(testAccountID); final.Status != contract.AuthorizationSessionStatusCancelled {
		t.Fatalf("final session = %+v", final)
	}
	if _, err := logins.Cancel(testAccountID); !errors.Is(err, ErrLoginNotPending) {
		t.Fatalf("second Cancel error = %v", err)
	}
}

func TestLoginWithoutPageReportsTheCLIError(t *testing.T) {
	cli := newFakeCLI(t, fakeclaude.Settings{Login: fakeclaude.LoginNoURL})
	logins := NewLogins(cli.accounts, func(context.Context, contract.ServiceID, contract.AuthorizationSessionID, Status) error {
		t.Fatal("login without a page completed")
		return nil
	}, nil)

	_, err := logins.Begin(context.Background(), testAccountID)
	if !errors.Is(err, ErrLoginNoURL) || !strings.Contains(err.Error(), "unable to reach the sign-in service") {
		t.Fatalf("Begin error = %v", err)
	}
	if _, ok := logins.Get(testAccountID); ok {
		t.Fatal("a login without a page was kept")
	}
}

func TestRejectedLoginSignsTheDirectoryOut(t *testing.T) {
	cli := newFakeCLI(t, fakeclaude.Settings{Login: fakeclaude.LoginBrowser, Account: testAccount})
	complete, completed := recordCompletions(errors.New("this Claude account is already connected"))
	logins := NewLogins(cli.accounts, complete, nil)

	if _, err := logins.Begin(context.Background(), testAccountID); err != nil {
		t.Fatal(err)
	}
	<-logins.Done(testAccountID)
	<-completed
	final, _ := logins.Get(testAccountID)
	if final.Status != contract.AuthorizationSessionStatusFailed || final.Error == nil ||
		final.Error.Code != ErrCodeLoginRejected || final.Error.Message != "this Claude account is already connected" {
		t.Fatalf("final session = %+v (error %+v)", final, final.Error)
	}
	configDir, _ := cli.accounts.Layout.ConfigDir(testAccountID)
	if _, err := os.Stat(fakeclaude.AccountFile(configDir)); !os.IsNotExist(err) {
		t.Fatalf("rejected login stayed signed in: %v", err)
	}
	status, err := cli.accounts.Status(context.Background(), testAccountID, configDir)
	if err != nil || status.LoggedIn {
		t.Fatalf("status after rejection = %+v, %v", status, err)
	}
}

func TestLoginRequiresTheCLI(t *testing.T) {
	root := t.TempDir()
	logins := NewLogins(Accounts{
		Locator: &Locator{Configured: filepath.Join(root, "missing", "claude")},
		Layout:  Layout{Root: root},
	}, nil, nil)
	if _, err := logins.Begin(context.Background(), testAccountID); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("Begin error = %v", err)
	}
}

func TestSanitizeLoginURL(t *testing.T) {
	for line, want := range map[string]string{
		"visit: https://claude.com/cai/oauth/authorize?code=true": "https://claude.com/cai/oauth/authorize?code=true",
		"visit: https://platform.claude.com/oauth/code":           "https://platform.claude.com/oauth/code",
		"visit: https://console.anthropic.com/oauth":              "https://console.anthropic.com/oauth",
		"visit: https://claude.com.evil.test/oauth":               "",
		"visit: https://evilclaude.com/oauth":                     "",
		"visit: http://claude.com/oauth":                          "",
		"no page here":                                            "",
	} {
		if got := sanitizeLoginURL(line); got != want {
			t.Errorf("sanitizeLoginURL(%q) = %q, want %q", line, got, want)
		}
	}
}

func TestParseStatus(t *testing.T) {
	status, err := ParseStatus([]byte(`{"loggedIn":true,"authMethod":"claude.ai","emailAddress":" a@example.com ","organizationUuid":"org","subscriptionType":"pro"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !status.LoggedIn || status.Email != "a@example.com" || status.OrganizationID != "org" || status.SubscriptionType != "pro" {
		t.Fatalf("status = %+v", status)
	}
	signedOut, err := ParseStatus([]byte(`{"loggedIn":false,"authMethod":"none"}`))
	if err != nil || signedOut.LoggedIn {
		t.Fatalf("signed out status = %+v, %v", signedOut, err)
	}
	if _, err := ParseStatus([]byte("Not logged in")); err == nil {
		t.Fatal("ParseStatus accepted plain text")
	}
}

func TestLoginTrafficGoesThroughTheAccountProxy(t *testing.T) {
	origin := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(origin.Close)
	upstream := newUpstreamProxy(t, origin.Listener.Addr().String())
	cli := newFakeCLI(t, fakeclaude.Settings{Login: fakeclaude.LoginBrowser, Account: testAccount, OwnTraffic: true})
	cli.accounts.Network = accountProxy("http", upstream.address)
	complete, completed := recordCompletions(nil)
	logins := NewLogins(cli.accounts, complete, nil)

	if _, err := logins.Begin(context.Background(), testAccountID); err != nil {
		t.Fatal(err)
	}
	<-logins.Done(testAccountID)
	<-completed
	configDir, _ := cli.accounts.Layout.ConfigDir(testAccountID)
	raw, err := os.ReadFile(fakeclaude.OwnTrafficFile(configDir))
	if err != nil || strings.TrimSpace(string(raw)) != fakeclaude.TokenHost+" ok" {
		t.Fatalf("the CLI's token exchange = %q, %v", raw, err)
	}
	connects, _ := upstream.seen()
	if len(connects) != 1 || connects[0] != fakeclaude.TokenHost+":443" {
		t.Fatalf("account proxy saw %v", connects)
	}
}
