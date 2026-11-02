package controlapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/claudecli"
	"github.com/QuantumNous/astrlink/core/internal/claudecli/fakeclaude"
)

func TestMain(m *testing.M) {
	fakeclaude.Main()
	os.Exit(m.Run())
}

func TestNewClaudeSubscriptionsDefaultToOfficial(t *testing.T) {
	_, handler := newServiceHandler(t, "service_claude_default", "service_claude_legacy", "service_codex_legacy",
		"service_rejected_1", "service_rejected_2", "service_rejected_3", "service_rejected_4")

	official := createServiceForTest(t, handler, `{"name":"Claude","kind":"claude_subscription","models":["claude-sonnet-4-5"]}`)
	if official.Subscription == nil || official.Subscription.Mode != contract.SubscriptionModeOfficial {
		t.Fatalf("default Claude subscription = %+v", official.Subscription)
	}
	legacy := createServiceForTest(t, handler, `{"name":"Claude","kind":"claude_subscription","subscription_mode":"legacy","models":["claude-sonnet-4-5"]}`)
	if legacy.Subscription == nil || legacy.Subscription.Mode != contract.SubscriptionModeLegacy {
		t.Fatalf("legacy Claude subscription = %+v", legacy.Subscription)
	}
	codex := createServiceForTest(t, handler, `{"name":"Codex","kind":"codex_subscription","subscription_mode":"legacy","models":["gpt-5"]}`)
	if codex.Subscription == nil || codex.Subscription.Mode != "" {
		t.Fatalf("Codex subscription = %+v", codex.Subscription)
	}
	for _, body := range []string{
		`{"name":"Codex","kind":"codex_subscription","subscription_mode":"official","models":["gpt-5"]}`,
		`{"name":"Claude","kind":"claude_subscription","subscription_mode":"cli","models":["claude-sonnet-4-5"]}`,
		`{"name":"Claude","kind":"claude_subscription","subscription_mode":"","models":["claude-sonnet-4-5"]}`,
		`{"name":"gw","kind":"newapi","subscription_mode":"legacy","http":{"base_url":"https://gateway.example/v1","auth":{"scheme":"none"}},"capabilities":[{"protocol":"openai.chat","mode":"native","streaming":true}]}`,
	} {
		response := serviceRequestForTest(t, handler, http.MethodPost, ServicesPath, "application/json", body, "")
		if response.Code != http.StatusUnprocessableEntity {
			t.Fatalf("create %s status=%d body=%s", body, response.Code, response.Body.String())
		}
	}

	authorize := func(id contract.ServiceID, body string) *http.Response {
		contentType := ""
		if body != "" {
			contentType = "application/json"
		}
		return serviceRequestForTest(t, handler, http.MethodPost, ServicesPath+"/"+string(id)+"/authorization", contentType, body, "").Result()
	}
	if response := authorize(official.ID, `{"flow":"authorization_code"}`); response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("official authorization_code status=%d", response.StatusCode)
	}
	if response := authorize(legacy.ID, `{"flow":"claude_cli"}`); response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("legacy claude_cli status=%d", response.StatusCode)
	}
	// This handler's manager has no CLI runner.
	if response := authorize(official.ID, ""); response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("official sign-in without the runner status=%d", response.StatusCode)
	}
}

func TestOfficialClaudeSignInThroughControlAPI(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake claude is linked by symlink")
	}
	_, handler := newServiceHandler(t, "service_claude_official")
	root := t.TempDir()
	binDir := t.TempDir()
	handler.subscriptions.EnableOfficialClaude(claudecli.Accounts{
		Locator: &claudecli.Locator{Configured: fakeclaude.Install(t, binDir, fakeclaude.Settings{
			Code:    "pasted-code",
			Account: fakeclaude.Account{Email: "person@example.com", OrganizationID: "org-1", SubscriptionType: "pro"},
		})},
		Layout:  claudecli.Layout{Root: filepath.Join(root, "claude-accounts"), WorkDir: filepath.Join(root, "work")},
		Environ: []string{"PATH=/usr/bin:/bin", "HOME=" + root},
	})
	service := createServiceForTest(t, handler, `{"name":"Claude","kind":"claude_subscription","models":["claude-sonnet-4-5"]}`)

	session := beginServiceAuthorization(t, handler, service.ID)
	if session.Flow != contract.AuthorizationFlowClaudeCLI || session.AuthorizationURL != fakeclaude.LoginURL {
		t.Fatalf("session = %+v", session)
	}
	response := serviceRequestForTest(t, handler, http.MethodPut, ServicesPath+"/"+string(service.ID)+"/authorization",
		"application/json", `{"session_id":"`+string(session.ID)+`","code":"pasted-code"}`, "")
	if response.Code != http.StatusOK {
		t.Fatalf("submit code status=%d body=%s", response.Code, response.Body.String())
	}
	var final contract.AuthorizationSession
	decode(t, response, &final)
	if final.Status != contract.AuthorizationSessionStatusCompleted {
		t.Fatalf("final session = %+v", final)
	}
	if strings.Contains(response.Body.String(), "pasted-code") {
		t.Fatalf("response echoes the code: %s", response.Body.String())
	}

	account, err := handler.subscriptions.Get(context.Background(), service.ID)
	if err != nil {
		t.Fatal(err)
	}
	if account.Status != contract.SubscriptionStatusConnected || account.Mode != contract.SubscriptionModeOfficial ||
		account.CredentialRef != "" || account.PlanType != "pro" {
		t.Fatalf("account = %+v", account)
	}
}

type fakeClaudeCLI struct {
	status  contract.ClaudeCLIStatus
	install error
	calls   int
}

func (cli *fakeClaudeCLI) Status(context.Context) contract.ClaudeCLIStatus { return cli.status }

func (cli *fakeClaudeCLI) Install() error {
	cli.calls++
	if cli.install == nil {
		cli.status.Installing = true
	}
	return cli.install
}

func TestClaudeCLIRoutes(t *testing.T) {
	store, _ := newServiceHandler(t)
	cli := &fakeClaudeCLI{status: contract.ClaudeCLIStatus{MinimumVersion: claudecli.MinimumVersion, InstallAvailable: true}}
	handler, err := NewWithDependencies(contract.DefaultVersionResponse("0.1.0-test", "abc1234"), Dependencies{
		ServiceStore: store, ControlToken: testControlToken, ObserverToken: testObserverToken, ClaudeCLI: cli,
	})
	if err != nil {
		t.Fatal(err)
	}

	response := serviceRequestForTest(t, handler, http.MethodGet, ClaudeCLIPath, "", "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("status route = %d %s", response.Code, response.Body.String())
	}
	var status contract.ClaudeCLIStatus
	decode(t, response, &status)
	if status.Installed || !status.InstallAvailable || status.MinimumVersion != claudecli.MinimumVersion {
		t.Fatalf("status = %+v", status)
	}

	if response := serviceRequestForTest(t, handler, http.MethodGet, ClaudeCLIInstallPath, "", "", ""); response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET install = %d", response.Code)
	}
	response = serviceRequestForTest(t, handler, http.MethodPost, ClaudeCLIInstallPath, "", "", "")
	if response.Code != http.StatusAccepted || cli.calls != 1 {
		t.Fatalf("install = %d %s (calls %d)", response.Code, response.Body.String(), cli.calls)
	}
	decode(t, response, &status)
	if !status.Installing {
		t.Fatalf("install status = %+v", status)
	}

	cli.install = claudecli.ErrInstallUnavailable
	response = serviceRequestForTest(t, handler, http.MethodPost, ClaudeCLIInstallPath, "", "", "")
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "claude_cli_install_unavailable") {
		t.Fatalf("desktop install = %d %s", response.Code, response.Body.String())
	}

	observer := httptestRequestAs(t, handler, http.MethodPost, ClaudeCLIInstallPath, testObserverToken)
	if observer != http.StatusForbidden {
		t.Fatalf("observer install = %d", observer)
	}
}

func httptestRequestAs(t *testing.T, handler http.Handler, method, path, token string) int {
	t.Helper()
	request, err := http.NewRequest(method, path, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder.Code
}
