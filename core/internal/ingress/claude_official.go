package ingress

import (
	"context"
	"errors"
	"net"
	"net/http"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/claudeofficial"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/relaykitbridge"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

// ClaudeOfficial serves official Claude subscription accounts through the
// user's own claude CLI. Its RoundTrip answers the outer request; the CLI's
// model calls come back through ServeHTTP carrying a process secret, which
// Loopback resolves to the outer request they serve. ObserveUsage receives
// the headers of each answer to those calls, which carry the account's plan
// usage.
type ClaudeOfficial interface {
	http.RoundTripper
	Loopback(secret string) (claudeofficial.Call, bool)
	ObserveUsage(ctx context.Context, id contract.ServiceID, header http.Header)
}

var errClaudeOfficialUnavailable = errors.New("official Claude accounts are unavailable in this edition")

// claudeLoopback is a model call one official account's CLI made.
type claudeLoopback struct {
	call claudeofficial.Call
	// authorization is the CLI's own credential, forwarded unchanged and
	// never stored.
	authorization []string
}

type claudeLoopbackKey struct{}

func claudeLoopbackFrom(ctx context.Context) *claudeLoopback {
	loopback, _ := ctx.Value(claudeLoopbackKey{}).(*claudeLoopback)
	return loopback
}

func hasClaudeProcessHeader(request *http.Request) bool {
	return len(request.Header.Values(claudeofficial.ProcessHeader)) > 0
}

// acceptClaudeLoopback admits a call from a serving CLI: it must come from
// this machine with a live process secret. Anything else is refused unrecorded,
// so the header cannot be used to fill the records or reach an account.
func (handler *Handler) acceptClaudeLoopback(writer http.ResponseWriter, request *http.Request) (*http.Request, bool) {
	values := request.Header.Values(claudeofficial.ProcessHeader)
	request.Header.Del(claudeofficial.ProcessHeader)
	var call claudeofficial.Call
	ok := handler.claudeOfficial != nil && len(values) == 1 && sameHost(request)
	if ok {
		call, ok = handler.claudeOfficial.Loopback(values[0])
	}
	if !ok {
		writeInferenceError(writer, http.StatusUnauthorized, "invalid_access_token", "the access token is invalid", false, nil)
		return nil, false
	}
	loopback := &claudeLoopback{call: call, authorization: request.Header.Values("Authorization")}
	ctx := context.WithValue(request.Context(), claudeLoopbackKey{}, loopback)
	if call.AccessTokenID != "" {
		ctx = context.WithValue(ctx, accessTokenIDContextKey{}, call.AccessTokenID)
	}
	return request.WithContext(ctx), true
}

// sameHost reports whether a request came from this machine: over loopback,
// or to a listener on one interface from that interface's own address.
func sameHost(request *http.Request) bool {
	remote := addressIP(request.RemoteAddr)
	if remote == nil {
		return false
	}
	if remote.IsLoopback() {
		return true
	}
	local, _ := request.Context().Value(http.LocalAddrContextKey).(net.Addr)
	return local != nil && remote.Equal(addressIP(local.String()))
}

func addressIP(address string) net.IP {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = address
	}
	return net.ParseIP(host)
}

// serveClaudeLoopback forwards a CLI's model call to its account unchanged:
// the CLI's own credential and identity, the account's network proxy, no
// routing, retries or failover (the CLI retries itself), and no privacy
// filter or gateway rewrite, which already applied to the outer request.
// Its record is a child of the outer request, which alone counts and bills
// the turn.
func (handler *Handler) serveClaudeLoopback(writer http.ResponseWriter, request *http.Request) {
	loopback := claudeLoopbackFrom(request.Context())
	classified, finishMetadata, err := handler.classify(request)
	defer finishMetadata()
	if request.Context().Err() != nil {
		return
	}
	if err != nil {
		writeInferenceError(writer, http.StatusNotFound, "not_found", "inference endpoint not found", false, nil)
		return
	}
	session := handler.startClaudeLoopbackRecord(request, classified, loopback)
	session.persistPending(request.Context(), handler.requestRecords, handler.recordLogger)
	outWriter := session.wrap(writer)
	request = request.WithContext(withRecordSession(request.Context(), session))
	defer func() {
		if request.Context().Err() != nil {
			session.noteCancelled()
		}
		session.finish(context.Background(), handler.requestRecords, handler.auditBlobs, handler.recordLogger)
	}()
	resolver, ok := handler.resolver.(endpoint.ServiceResolver)
	var candidate endpoint.Resolved
	if ok {
		candidate, err = resolver.ResolveService(request.Context(), loopback.call.Service.ID)
	}
	if !ok || err != nil || !claudeofficial.Official(candidate.CanonicalService()) {
		session.captureUnreadRequestBody(request)
		message := "the Claude account is not available"
		writeInferenceError(outWriter, http.StatusServiceUnavailable, "service_unavailable", message, true, nil)
		session.noteFailed(errorSummaryFromInference("service_unavailable", message, true))
		return
	}
	failover := contract.DefaultFailoverPolicy()
	failover.Enabled, failover.MaxAttempts = false, 1
	candidate.Failover = &failover
	handler.executeCandidates(outWriter, request, classified, []endpoint.Resolved{candidate})
}

// startClaudeLoopbackRecord starts the record of a CLI call: a child of the
// outer request, outside any conversation.
func (handler *Handler) startClaudeLoopbackRecord(request *http.Request, classified Request, loopback *claudeLoopback) *recordSession {
	session := newRecordSession(classified, loopback.call.AccessTokenID, handler.loadAuditSettings(request.Context()))
	session.clientType = detectClientType(request.Header)
	session.privacyGated = false
	parent := loopback.call.RecordID
	if parent != "" {
		session.delegatedParent = &parent
	}
	session.routing = &routingTrace{pin: contract.RoutingSelectionClaudeCLI, pinned: loopback.call.Service.ID}
	session.bindPersistence(handler.requestRecords, handler.auditBlobs, handler.recordLogger)
	session.captureHTTPRequestMeta(request)
	session.attachRequestCapture(request)
	return session
}

// claudeOfficialRequest attaches the outer call an official account serves.
func (handler *Handler) claudeOfficialRequest(request *http.Request, candidate endpoint.Resolved, records *recordSession) (*http.Request, error) {
	if handler.claudeOfficial == nil {
		return request, errClaudeOfficialUnavailable
	}
	call := claudeofficial.Call{Service: candidate.Service, BaseURL: candidate.BaseURL}
	if records != nil {
		call.RecordID = records.id
	}
	if id, ok := AccessTokenIDFromContext(request.Context()); ok {
		call.AccessTokenID = id
		// Conversations never cross local access tokens.
		call.SessionKey = string(id)
	}
	return request.WithContext(claudeofficial.WithCall(request.Context(), call)), nil
}

// claudeOfficialDiagnostics lists what the CLI cannot honour in the request
// it is about to serve, for the record.
func claudeOfficialDiagnostics(request *http.Request) []relaykitbridge.ConversionDiagnostic {
	raw, ok := peekClaudeRequestBody(request)
	if !ok {
		return nil
	}
	found := claudeofficial.Unsupported(raw)
	diagnostics := make([]relaykitbridge.ConversionDiagnostic, 0, len(found))
	for _, item := range found {
		diagnostics = append(diagnostics, relaykitbridge.ConversionDiagnostic{
			Code: item.Code, Path: item.Path, Message: item.Message, Severity: item.Severity,
		})
	}
	return diagnostics
}

// forwardClaudeOfficial hands the outer request to the CLI runtime. The
// runtime runs locally; the account's proxy applies to the CLI's own calls on
// the loopback path, so the outer hop binds none.
func (handler *Handler) forwardClaudeOfficial(writer http.ResponseWriter, request *http.Request, target transport.Target) error {
	service := target.Service
	service.Proxy = nil
	target.Service = service
	return handler.claudeOfficialForwarder.Forward(writer, request, target)
}
