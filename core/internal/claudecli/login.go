package claudecli

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/astrlink/core/contract"
)

const (
	// LoginTimeout bounds one `claude auth login` run.
	LoginTimeout = 10 * time.Minute
	// loginURLTimeout bounds the wait for the CLI to print its sign-in page.
	loginURLTimeout = 45 * time.Second
	maxLoginOutput  = 64 << 10
)

// Error codes of a failed official login.
const (
	ErrCodeLoginFailed   = "claude_login_failed"
	ErrCodeNotInstalled  = "claude_cli_missing"
	ErrCodeCLITooOld     = "claude_cli_outdated"
	ErrCodeLoginNoURL    = "claude_login_no_url"
	ErrCodeLoginRejected = "claude_login_rejected"
	ErrCodeLoginCanceled = "claude_login_cancelled"
)

var (
	// ErrLoginNotFound means no login exists for the service.
	ErrLoginNotFound = errors.New("no Claude Code login exists for this service")
	// ErrLoginNotPending means the login already ended.
	ErrLoginNotPending = errors.New("Claude Code login is not pending")
	// ErrLoginNoURL means the CLI exited or stalled before showing a page.
	ErrLoginNoURL = errors.New("Claude Code did not show a sign-in page")

	escapeSequence = regexp.MustCompile("\x1b\\][^\x07\x1b]*(?:\x07|\x1b\\\\)|\x1b\\[[0-9;?]*[ -/]*[@-~]")
	loginURL       = regexp.MustCompile(`https://[^\s"'<>]+`)
)

// LoginCompletion persists a finished login. Its error fails the session and
// signs the directory out again.
type LoginCompletion func(ctx context.Context, id contract.ServiceID, session contract.AuthorizationSessionID, status Status) error

// Logins runs at most one `claude auth login` per account.
type Logins struct {
	accounts Accounts
	complete LoginCompletion
	now      func() time.Time

	mu       sync.Mutex
	sessions map[contract.ServiceID]*login
}

type login struct {
	session contract.AuthorizationSession
	stdin   io.WriteCloser
	run     context.Context
	cancel  context.CancelFunc
	done    chan struct{}
}

// NewLogins wires the login runner. complete is called once per successful
// login, after `claude auth status` confirms it.
func NewLogins(accounts Accounts, complete LoginCompletion, now func() time.Time) *Logins {
	if now == nil {
		now = time.Now
	}
	return &Logins{accounts: accounts, complete: complete, now: now, sessions: make(map[contract.ServiceID]*login)}
}

// Begin starts `claude auth login --claudeai` in the account's directory and
// returns once the CLI shows its sign-in page. On a desktop the CLI also
// opens the browser itself and finishes through its local callback; anywhere
// else the page shows a code to pass to SubmitCode.
func (logins *Logins) Begin(ctx context.Context, id contract.ServiceID) (contract.AuthorizationSession, error) {
	configDir, err := logins.accounts.Layout.EnsureConfigDir(id)
	if err != nil {
		return contract.AuthorizationSession{}, err
	}
	binary, err := logins.accounts.Locator.Find(ctx)
	if err != nil {
		return contract.AuthorizationSession{}, err
	}
	_, _ = logins.Cancel(id)
	sessionID, err := newSessionID()
	if err != nil {
		return contract.AuthorizationSession{}, err
	}
	runCtx, cancel := context.WithTimeout(context.Background(), LoginTimeout)
	tunnel, err := logins.accounts.Tunnel(id)
	if err != nil {
		cancel()
		return contract.AuthorizationSession{}, err
	}
	handedOff := false
	defer func() {
		if !handedOff {
			_ = tunnel.Close()
		}
	}()
	command := exec.CommandContext(runCtx, binary.Path, "auth", "login", "--claudeai")
	env := withEnv(LoginEnv(logins.accounts.Environ, logins.accounts.Layout.Home(), configDir), tunnel.Env())
	command.Env = AddPathDir(env, dirOf(binary.Path))
	command.Dir = configDir
	command.WaitDelay = 5 * time.Second
	output, writer := io.Pipe()
	command.Stdout, command.Stderr = writer, writer
	stdin, err := command.StdinPipe()
	if err != nil {
		cancel()
		return contract.AuthorizationSession{}, err
	}
	if err := command.Start(); err != nil {
		cancel()
		return contract.AuthorizationSession{}, fmt.Errorf("start claude auth login: %w", err)
	}
	now := logins.now().UTC()
	current := &login{
		session: contract.AuthorizationSession{
			ID: sessionID, Provider: contract.SubscriptionProviderClaudeCode,
			Status: contract.AuthorizationSessionStatusPending, Flow: contract.AuthorizationFlowClaudeCLI,
			ServiceID: id, ExpiresAt: now.Add(LoginTimeout), CreatedAt: now, UpdatedAt: now,
		},
		stdin: stdin, run: runCtx, cancel: cancel, done: make(chan struct{}),
	}
	found := make(chan string, 1)
	transcript := &loginTranscript{}
	go func() {
		scanner := bufio.NewScanner(output)
		scanner.Buffer(make([]byte, 0, 4096), maxLoginOutput)
		sent := false
		for scanner.Scan() {
			line := escapeSequence.ReplaceAllString(scanner.Text(), "")
			transcript.add(line)
			if !sent {
				if page := sanitizeLoginURL(line); page != "" {
					found <- page
					sent = true
				}
			}
		}
		_, _ = io.Copy(io.Discard, output)
	}()
	exited := make(chan error, 1)
	go func() {
		err := command.Wait()
		_ = writer.Close()
		exited <- err
	}()
	timer := time.NewTimer(loginURLTimeout)
	defer timer.Stop()
	select {
	case page := <-found:
		current.session.AuthorizationURL = page
	case err := <-exited:
		cancel()
		return contract.AuthorizationSession{}, fmt.Errorf("%w: %s", ErrLoginNoURL, loginFailure(err, transcript))
	case <-timer.C:
		cancel()
		<-exited
		return contract.AuthorizationSession{}, ErrLoginNoURL
	case <-ctx.Done():
		cancel()
		<-exited
		return contract.AuthorizationSession{}, ctx.Err()
	}
	logins.mu.Lock()
	logins.sessions[id] = current
	logins.mu.Unlock()
	handedOff = true
	go logins.finish(id, current, configDir, exited, transcript, tunnel)
	return current.session, nil
}

// finish waits for the CLI and records the outcome.
func (logins *Logins) finish(id contract.ServiceID, current *login, configDir string, exited <-chan error, transcript *loginTranscript, tunnel *Tunnel) {
	defer close(current.done)
	err := <-exited
	_ = tunnel.Close()
	defer current.cancel()
	logins.mu.Lock()
	cancelled := current.session.Status != contract.AuthorizationSessionStatusPending
	logins.mu.Unlock()
	if cancelled {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err != nil {
		status := contract.AuthorizationSessionStatusFailed
		if errors.Is(current.run.Err(), context.DeadlineExceeded) {
			status = contract.AuthorizationSessionStatusExpired
		}
		logins.end(current, status, &contract.SubscriptionError{Code: ErrCodeLoginFailed, Message: loginFailure(err, transcript)})
		return
	}
	status, statusErr := logins.accounts.Status(ctx, id, configDir)
	if statusErr != nil || !status.LoggedIn {
		logins.end(current, contract.AuthorizationSessionStatusFailed, &contract.SubscriptionError{
			Code: ErrCodeLoginFailed, Message: "Claude Code finished without a signed-in account; sign in again",
		})
		return
	}
	if err := logins.complete(ctx, id, current.session.ID, status); err != nil {
		_ = logins.accounts.Logout(ctx, id, configDir)
		logins.end(current, contract.AuthorizationSessionStatusFailed, &contract.SubscriptionError{
			Code: ErrCodeLoginRejected, Message: clampMessage(err.Error()),
		})
		return
	}
	logins.end(current, contract.AuthorizationSessionStatusCompleted, nil)
}

func (logins *Logins) end(current *login, status contract.AuthorizationSessionStatus, failure *contract.SubscriptionError) {
	logins.mu.Lock()
	defer logins.mu.Unlock()
	if current.session.Status != contract.AuthorizationSessionStatusPending {
		return
	}
	current.session.Status = status
	current.session.AuthorizationURL = ""
	current.session.Error = failure
	current.session.UpdatedAt = logins.now().UTC()
	_ = current.stdin.Close()
}

// Get returns the latest state of the account's login.
func (logins *Logins) Get(id contract.ServiceID) (contract.AuthorizationSession, bool) {
	logins.mu.Lock()
	defer logins.mu.Unlock()
	current, ok := logins.sessions[id]
	if !ok {
		return contract.AuthorizationSession{}, false
	}
	return current.session, true
}

// Done is closed once the account's current login has ended and its outcome
// is recorded. Without a login it is already closed.
func (logins *Logins) Done(id contract.ServiceID) <-chan struct{} {
	logins.mu.Lock()
	defer logins.mu.Unlock()
	if current, ok := logins.sessions[id]; ok {
		return current.done
	}
	closed := make(chan struct{})
	close(closed)
	return closed
}

// SubmitCode passes the code the sign-in page showed to the waiting CLI.
func (logins *Logins) SubmitCode(id contract.ServiceID, sessionID contract.AuthorizationSessionID, code string) (contract.AuthorizationSession, error) {
	code = strings.TrimSpace(code)
	if code == "" || len(code) > 8192 || strings.ContainsAny(code, "\r\n\x00") {
		return contract.AuthorizationSession{}, fmt.Errorf("authorization code is invalid")
	}
	logins.mu.Lock()
	defer logins.mu.Unlock()
	current, ok := logins.sessions[id]
	if !ok || current.session.ID != sessionID {
		return contract.AuthorizationSession{}, ErrLoginNotFound
	}
	if current.session.Status != contract.AuthorizationSessionStatusPending {
		return contract.AuthorizationSession{}, ErrLoginNotPending
	}
	if _, err := io.WriteString(current.stdin, code+"\n"); err != nil {
		// The CLI stopped reading: it finished on its own, as when the
		// browser sign-in completes first. Its outcome is the answer.
		return current.session, nil
	}
	current.session.UpdatedAt = logins.now().UTC()
	return current.session, nil
}

// Cancel stops a pending login. It reports the session as it ended.
func (logins *Logins) Cancel(id contract.ServiceID) (contract.AuthorizationSession, error) {
	logins.mu.Lock()
	current, ok := logins.sessions[id]
	if !ok {
		logins.mu.Unlock()
		return contract.AuthorizationSession{}, ErrLoginNotFound
	}
	if current.session.Status != contract.AuthorizationSessionStatusPending {
		session := current.session
		logins.mu.Unlock()
		return session, ErrLoginNotPending
	}
	current.session.Status = contract.AuthorizationSessionStatusCancelled
	current.session.AuthorizationURL = ""
	current.session.Error = &contract.SubscriptionError{Code: ErrCodeLoginCanceled, Message: "sign-in cancelled"}
	current.session.UpdatedAt = logins.now().UTC()
	session := current.session
	_ = current.stdin.Close()
	current.cancel()
	logins.mu.Unlock()
	<-current.done
	return session, nil
}

// Forget drops every trace of the account's login, cancelling it first.
func (logins *Logins) Forget(id contract.ServiceID) {
	_, _ = logins.Cancel(id)
	logins.mu.Lock()
	delete(logins.sessions, id)
	logins.mu.Unlock()
}

// sanitizeLoginURL returns the sign-in page on a line of CLI output, or "".
func sanitizeLoginURL(line string) string {
	match := loginURL.FindString(line)
	if match == "" {
		return ""
	}
	parsed, err := url.Parse(match)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return ""
	}
	host := strings.ToLower(parsed.Hostname())
	for _, allowed := range []string{"claude.com", "claude.ai", "anthropic.com"} {
		if host == allowed || strings.HasSuffix(host, "."+allowed) {
			return parsed.String()
		}
	}
	return ""
}

type loginTranscript struct {
	mu    sync.Mutex
	lines []string
}

func (transcript *loginTranscript) add(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	transcript.mu.Lock()
	defer transcript.mu.Unlock()
	transcript.lines = append(transcript.lines, line)
	if len(transcript.lines) > 20 {
		transcript.lines = transcript.lines[len(transcript.lines)-20:]
	}
}

// failure is the last line the CLI printed that is not a sign-in page or a
// prompt, which is where it states why it failed.
func (transcript *loginTranscript) failure() string {
	transcript.mu.Lock()
	defer transcript.mu.Unlock()
	for index := len(transcript.lines) - 1; index >= 0; index-- {
		line := transcript.lines[index]
		if loginURL.MatchString(line) {
			continue
		}
		if _, after, found := strings.Cut(line, "Paste code here if prompted >"); found {
			line = strings.TrimSpace(after)
			if line == "" {
				continue
			}
		}
		return line
	}
	return ""
}

func loginFailure(err error, transcript *loginTranscript) string {
	if line := transcript.failure(); line != "" {
		return clampMessage(line)
	}
	if err != nil {
		return "Claude Code sign-in did not finish"
	}
	return "Claude Code exited without a sign-in page"
}

func clampMessage(message string) string {
	message = strings.TrimSpace(message)
	if utf8.RuneCountInString(message) > 200 {
		message = string([]rune(message)[:200])
	}
	if message == "" || (contract.SubscriptionError{Code: ErrCodeLoginFailed, Message: message}).Validate() != nil {
		return "Claude Code sign-in failed"
	}
	return message
}

func newSessionID() (contract.AuthorizationSessionID, error) {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return contract.AuthorizationSessionID("authorization_session_" + hex.EncodeToString(raw[:])), nil
}
