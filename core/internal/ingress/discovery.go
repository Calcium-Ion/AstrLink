package ingress

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/planner"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

// maxConcurrentDiscoveryFetches bounds the model discovery fan-out. The limit
// is deliberately small and fixed; additional capable endpoints wait for a
// free worker instead of opening unbounded concurrent upstream connections.
const maxConcurrentDiscoveryFetches = 4

var errDiscoveryResponseInvalid = errors.New("upstream model discovery response cannot be aggregated")

type discoveryOutcome uint8

const (
	// discoveryOutcomeExcluded covers candidates rejected before upstream I/O:
	// plan/capability, credential, configuration, privacy, or a refused
	// circuit admission. No health outcome is recorded for them.
	discoveryOutcomeExcluded discoveryOutcome = iota
	// discoveryOutcomeAborted marks client cancellation. An admitted attempt
	// is abandoned rather than counted for or against the circuit.
	discoveryOutcomeAborted
	// discoveryOutcomeFailed marks an admitted fetch that did not produce a
	// usable model list. It records exactly one circuit failure.
	discoveryOutcomeFailed
	// discoveryOutcomeFetched marks an admitted fetch that produced a usable
	// model list. It records exactly one circuit success.
	discoveryOutcomeFetched
)

type discoveryResult struct {
	outcome    discoveryOutcome
	entries    []discoveryEntry
	warning    string
	failure    executionFailure
	privacyErr error
}

// discoveryEntry keeps the upstream's original entry bytes together with the
// public model ID used for conflict handling.
type discoveryEntry struct {
	id  string
	raw json.RawMessage
}

// aggregateModelDiscovery serves a model listing from every capable enabled
// candidate instead of relaying to a single one. Candidates arrive in the
// resolver's deterministic routing order — native before delegated, then
// Endpoint ID, unless an explicit Route matched — and the first candidate
// returning a public model ID wins any conflict, so discovery names the same
// upstream that routing would select for that ID. Each request fans out
// fresh; there is no discovery cache in Alpha.
func (handler *Handler) aggregateModelDiscovery(
	writer http.ResponseWriter,
	request *http.Request,
	classified Request,
	candidates []endpoint.Resolved,
) {
	if request.Context().Err() != nil {
		return
	}
	results := make([]discoveryResult, len(candidates))
	indexes := make(chan int)
	var group sync.WaitGroup
	for range min(maxConcurrentDiscoveryFetches, len(candidates)) {
		group.Add(1)
		go func() {
			defer group.Done()
			for index := range indexes {
				results[index] = handler.fetchModelDiscovery(request, classified, candidates[index])
			}
		}()
	}
	for index := range candidates {
		indexes <- index
	}
	close(indexes)
	group.Wait()

	if request.Context().Err() != nil {
		return
	}
	// Privacy stays fail closed for the whole request, matching the execution
	// path: an unavailable or blocking policy must not be silently narrowed
	// into skipping one endpoint's contribution.
	for _, result := range results {
		if result.privacyErr != nil {
			handler.writePrivacyError(writer, request, result.privacyErr)
			return
		}
	}
	mergeInput := results
	if lister, ok := handler.resolver.(endpoint.AliasLister); ok {
		aliases, aliasErr := lister.ListAliasModels(request.Context(), classified.Protocol)
		if aliasErr != nil {
			handler.writeResolveError(writer, request, classified, aliasErr)
			return
		}
		if len(aliases) > 0 {
			aliasEntries, encodeErr := synthesizeAliasDiscoveryEntries(classified.Protocol, aliases)
			if encodeErr != nil {
				writeInferenceError(
					writer,
					http.StatusInternalServerError,
					"discovery_aggregation_failed",
					"aggregated model list could not be encoded",
					true,
					nil,
				)
				return
			}
			// Prepend so the existing first-wins dedupe keeps the alias over
			// any upstream entry that reuses the same public ID.
			mergeInput = make([]discoveryResult, 0, len(results)+1)
			mergeInput = append(mergeInput, discoveryResult{
				outcome: discoveryOutcomeFetched,
				entries: aliasEntries,
			})
			mergeInput = append(mergeInput, results...)
		}
	}
	merged, succeeded := mergeDiscoveryEntries(mergeInput)
	if succeeded == 0 {
		handler.writeDiscoveryFailure(writer, request, classified, results)
		return
	}
	body, err := encodeDiscoveryList(classified.Protocol, merged)
	if err != nil {
		writeInferenceError(
			writer,
			http.StatusInternalServerError,
			"discovery_aggregation_failed",
			"aggregated model list could not be encoded",
			true,
			nil,
		)
		return
	}
	header := writer.Header()
	for _, result := range results {
		if result.outcome == discoveryOutcomeFetched && result.warning != "" {
			header.Set(PolicyWarningHeader, result.warning)
		}
	}
	header.Set("Cache-Control", "no-store")
	header.Set("Content-Type", "application/json")
	header.Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(body)
	if session := recordSessionFromContext(request.Context()); session != nil {
		session.noteSucceeded()
	}
}

func (handler *Handler) fetchModelDiscovery(
	request *http.Request,
	classified Request,
	candidate endpoint.Resolved,
) discoveryResult {
	candidate.Service = candidate.CanonicalService()
	candidate.BaseURL = candidate.EffectiveBaseURL()
	if request.Context().Err() != nil {
		return discoveryResult{outcome: discoveryOutcomeAborted}
	}
	mode := candidate.Mode
	if !mode.Valid() {
		// Preserve the original M1 seam: an omitted mode is native.
		mode = contract.CapabilityModeNative
	}
	if _, planErr := planner.BuildAlpha(planner.AlphaInput{
		Service:   candidate.Service,
		Protocol:  classified.Protocol,
		Mode:      mode,
		Streaming: classified.Streaming,
	}); planErr != nil {
		var capabilityErr *planner.CapabilityUnavailableError
		if errors.As(planErr, &capabilityErr) ||
			errors.Is(planErr, planner.ErrEndpointDisabled) {
			if capabilityErr == nil {
				capabilityErr = &planner.CapabilityUnavailableError{
					Protocol:  classified.Protocol,
					Mode:      mode,
					Streaming: classified.Streaming,
				}
			}
			return discoveryResult{outcome: discoveryOutcomeExcluded, failure: executionFailure{
				kind:       executionFailureCapability,
				err:        planErr,
				endpointID: candidate.Service.ID,
				capability: capabilityErr,
			}}
		}
		return discoveryResult{outcome: discoveryOutcomeExcluded, failure: executionFailure{
			kind:       executionFailureConfiguration,
			err:        planErr,
			endpointID: candidate.Service.ID,
		}}
	}

	// The whole per-endpoint fetch shares the response-start bound: an
	// aggregated listing is a small bounded document, so unlike inference it
	// never carries a long-lived stream that must outlive the timeout.
	fetchContext, cancelFetch := context.WithTimeout(request.Context(), handler.responseStartTimeout)
	defer cancelFetch()
	fetchRequest := request.Clone(fetchContext)
	fetchRequest.Body = http.NoBody
	fetchRequest.GetBody = nil
	fetchRequest.ContentLength = 0
	// The aggregate reply is synthesized locally from one bounded page per
	// endpoint; conditional or partial upstream responses cannot be merged.
	fetchRequest.Header.Del("If-None-Match")
	fetchRequest.Header.Del("If-Modified-Since")
	fetchRequest.Header.Del("Range")

	recorder := newDiscoveryResponseRecorder(maxMetadataBytes)
	finishPrivacy, _, privacyErr := handler.applyPrivacy(
		recorder,
		fetchRequest,
		classified,
		candidate.Service.ID,
	)
	finishPrivacy()
	if request.Context().Err() != nil {
		return discoveryResult{outcome: discoveryOutcomeAborted}
	}
	if privacyErr != nil {
		return discoveryResult{outcome: discoveryOutcomeExcluded, privacyErr: privacyErr}
	}

	authorizationEndpoint, authorizeErr := candidate.AuthorizationEndpoint()
	var headers http.Header
	if authorizeErr == nil {
		headers, authorizeErr = handler.authorizer.Headers(request.Context(), authorizationEndpoint)
	}
	if authorizeErr != nil {
		if request.Context().Err() != nil {
			return discoveryResult{outcome: discoveryOutcomeAborted}
		}
		return discoveryResult{outcome: discoveryOutcomeExcluded, failure: executionFailure{
			kind:       executionFailureCredential,
			err:        authorizeErr,
			endpointID: candidate.Service.ID,
		}}
	}
	baseURL, parseErr := url.Parse(candidate.BaseURL)
	if parseErr != nil {
		return discoveryResult{outcome: discoveryOutcomeExcluded, failure: executionFailure{
			kind:       executionFailureConfiguration,
			err:        parseErr,
			endpointID: candidate.Service.ID,
		}}
	}
	if candidate.Service.Kind.IsSubscription() {
		fetchRequest.URL.Path = strings.TrimPrefix(fetchRequest.URL.Path, "/v1")
		if fetchRequest.URL.RawPath != "" {
			fetchRequest.URL.RawPath = strings.TrimPrefix(fetchRequest.URL.RawPath, "/v1")
		}
	}

	controller, healthAware := handler.resolver.(endpoint.AttemptController)
	if healthAware && !controller.BeginAttempt(candidate) {
		return discoveryResult{outcome: discoveryOutcomeExcluded}
	}
	health := newAttemptHealthOutcome(controller, candidate, healthAware)

	forwardErr := handler.forwarder.Forward(recorder, fetchRequest, transport.Target{
		BaseURL:        baseURL,
		RequestHeaders: headers,
	})
	if request.Context().Err() != nil {
		health.Abandon()
		return discoveryResult{outcome: discoveryOutcomeAborted}
	}
	var targetErr *transport.TargetError
	if errors.As(forwardErr, &targetErr) {
		// The target was rejected before upstream I/O: configuration, not health.
		health.Abandon()
		return discoveryResult{outcome: discoveryOutcomeExcluded, failure: executionFailure{
			kind:       executionFailureConfiguration,
			err:        forwardErr,
			endpointID: candidate.Service.ID,
		}}
	}
	if forwardErr != nil {
		health.Failure()
		failureErr := forwardErr
		if errors.Is(fetchContext.Err(), context.DeadlineExceeded) &&
			!errors.Is(forwardErr, context.DeadlineExceeded) {
			failureErr = fmt.Errorf("%w: upstream model discovery", context.DeadlineExceeded)
		}
		return discoveryResult{outcome: discoveryOutcomeFailed, failure: executionFailure{
			kind:       executionFailureUpstream,
			err:        failureErr,
			endpointID: candidate.Service.ID,
		}}
	}
	if recorder.status < http.StatusOK || recorder.status >= http.StatusMultipleChoices {
		health.Failure()
		return discoveryResult{outcome: discoveryOutcomeFailed, failure: executionFailure{
			kind:       executionFailureUpstream,
			err:        fmt.Errorf("upstream model discovery returned status %d", recorder.status),
			endpointID: candidate.Service.ID,
		}}
	}
	entries, entriesErr := parseDiscoveryEntries(classified.Protocol, recorder.body.Bytes())
	if entriesErr != nil {
		health.Failure()
		return discoveryResult{outcome: discoveryOutcomeFailed, failure: executionFailure{
			kind:       executionFailureUpstream,
			err:        entriesErr,
			endpointID: candidate.Service.ID,
		}}
	}
	health.Success()
	return discoveryResult{
		outcome: discoveryOutcomeFetched,
		entries: entries,
		warning: recorder.Header().Get(PolicyWarningHeader),
	}
}

// mergeDiscoveryEntries deduplicates public model IDs by first appearance in
// candidate order and returns the union sorted by ID, so repeated requests
// against unchanged upstreams produce identical bytes.
func mergeDiscoveryEntries(results []discoveryResult) ([]discoveryEntry, int) {
	succeeded := 0
	merged := make([]discoveryEntry, 0)
	seen := make(map[string]struct{})
	for _, result := range results {
		if result.outcome != discoveryOutcomeFetched {
			continue
		}
		succeeded++
		for _, entry := range result.entries {
			if _, duplicate := seen[entry.id]; duplicate {
				continue
			}
			seen[entry.id] = struct{}{}
			merged = append(merged, entry)
		}
	}
	sort.Slice(merged, func(left, right int) bool {
		return merged[left].id < merged[right].id
	})
	return merged, succeeded
}

// parseDiscoveryEntries reads one upstream listing. /v1/models accepts both
// the OpenAI list envelope and the Anthropic list shape — both carry a "data"
// array of objects with a string "id" — while /v1beta/models reads the Gemini
// "models" array keyed by "name". A missing or null array is an empty list;
// a malformed envelope or entry fails that endpoint instead of being dropped
// silently.
func parseDiscoveryEntries(protocol contract.ProtocolID, body []byte) ([]discoveryEntry, error) {
	listKey, identityKey := "data", "id"
	if protocol == contract.ProtocolGoogleModels {
		listKey, identityKey = "models", "name"
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil || envelope == nil {
		return nil, errDiscoveryResponseInvalid
	}
	var elements []json.RawMessage
	if rawList, exists := envelope[listKey]; exists {
		if err := json.Unmarshal(rawList, &elements); err != nil {
			return nil, errDiscoveryResponseInvalid
		}
	}
	entries := make([]discoveryEntry, 0, len(elements))
	for _, element := range elements {
		var identity map[string]json.RawMessage
		if err := json.Unmarshal(element, &identity); err != nil || identity == nil {
			return nil, errDiscoveryResponseInvalid
		}
		var id string
		if err := json.Unmarshal(identity[identityKey], &id); err != nil || id == "" {
			return nil, errDiscoveryResponseInvalid
		}
		entries = append(entries, discoveryEntry{id: id, raw: element})
	}
	return entries, nil
}

// openAIModelList is the aggregate /v1/models envelope. The single path
// serves OpenAI-style and Anthropic-style clients, so it carries the OpenAI
// list marker together with the Anthropic pagination terminator. Every field
// belongs to one of those wire protocols; none identifies AstrLink or the
// endpoint that supplied an entry.
type openAIModelList struct {
	Object  string            `json:"object"`
	Data    []json.RawMessage `json:"data"`
	FirstID *string           `json:"first_id"`
	HasMore bool              `json:"has_more"`
	LastID  *string           `json:"last_id"`
}

type googleModelList struct {
	Models []json.RawMessage `json:"models"`
}

func encodeDiscoveryList(protocol contract.ProtocolID, entries []discoveryEntry) ([]byte, error) {
	list := make([]json.RawMessage, 0, len(entries))
	for _, entry := range entries {
		list = append(list, entry.raw)
	}
	if protocol == contract.ProtocolGoogleModels {
		return json.Marshal(googleModelList{Models: list})
	}
	envelope := openAIModelList{Object: "list", Data: list}
	if len(entries) > 0 {
		envelope.FirstID = &entries[0].id
		envelope.LastID = &entries[len(entries)-1].id
	}
	return json.Marshal(envelope)
}

type openAIAliasDiscoveryModel struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int    `json:"created"`
	OwnedBy string `json:"owned_by"`
}

type googleAliasDiscoveryModel struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
}

func synthesizeAliasDiscoveryEntries(
	protocol contract.ProtocolID,
	aliases []string,
) ([]discoveryEntry, error) {
	entries := make([]discoveryEntry, 0, len(aliases))
	for _, alias := range aliases {
		if protocol == contract.ProtocolGoogleModels {
			raw, err := json.Marshal(googleAliasDiscoveryModel{
				Name:        "models/" + alias,
				DisplayName: alias,
			})
			if err != nil {
				return nil, err
			}
			entries = append(entries, discoveryEntry{id: "models/" + alias, raw: raw})
			continue
		}
		raw, err := json.Marshal(openAIAliasDiscoveryModel{
			ID:      alias,
			Object:  "model",
			Created: 0,
			OwnedBy: "system",
		})
		if err != nil {
			return nil, err
		}
		entries = append(entries, discoveryEntry{id: alias, raw: raw})
	}
	return entries, nil
}

// writeDiscoveryFailure reports an aggregate in which no capable endpoint
// produced a usable listing. Admitted upstream fetch failures dominate;
// otherwise the first excluded candidate in deterministic candidate order
// names the reason.
func (handler *Handler) writeDiscoveryFailure(
	writer http.ResponseWriter,
	request *http.Request,
	classified Request,
	results []discoveryResult,
) {
	failed, timedOut := 0, 0
	for _, result := range results {
		if result.outcome != discoveryOutcomeFailed {
			continue
		}
		failed++
		if errors.Is(result.failure.err, context.DeadlineExceeded) {
			timedOut++
		}
	}
	if failed > 0 {
		status, code, message := http.StatusBadGateway, "upstream_unavailable",
			"model discovery failed on every capable endpoint"
		if timedOut == failed {
			status, code, message = http.StatusGatewayTimeout, "upstream_timeout",
				"model discovery timed out on every capable endpoint"
		}
		writeInferenceError(writer, status, code, message, true, []errorDetail{{
			Protocol: string(classified.Protocol),
			Reason:   "no capable endpoint returned a model list",
		}})
		if session := recordSessionFromContext(request.Context()); session != nil {
			session.noteFailed(errorSummaryFromInference(code, message, true))
		}
		return
	}
	for _, kind := range []executionFailureKind{
		executionFailureCredential,
		executionFailureConfiguration,
		executionFailureCapability,
	} {
		for _, result := range results {
			if result.outcome == discoveryOutcomeExcluded && result.failure.kind == kind {
				handler.writeExecutionFailure(writer, request, classified, result.failure)
				return
			}
		}
	}
	handler.writeResolveError(writer, request, classified, endpoint.ErrNoHealthyEndpoint)
}

// discoveryResponseRecorder buffers one upstream discovery response in
// memory. Writes fail once the shared metadata byte bound is exceeded, which
// fails that endpoint cleanly instead of buffering an unbounded reply.
type discoveryResponseRecorder struct {
	header http.Header
	status int
	body   bytes.Buffer
	limit  int
}

func newDiscoveryResponseRecorder(limit int) *discoveryResponseRecorder {
	return &discoveryResponseRecorder{header: make(http.Header), limit: limit}
}

func (recorder *discoveryResponseRecorder) Header() http.Header {
	return recorder.header
}

func (recorder *discoveryResponseRecorder) WriteHeader(status int) {
	if recorder.status == 0 {
		recorder.status = status
	}
}

func (recorder *discoveryResponseRecorder) Write(chunk []byte) (int, error) {
	if recorder.status == 0 {
		recorder.status = http.StatusOK
	}
	if recorder.body.Len()+len(chunk) > recorder.limit {
		return 0, errMetadataTooLarge
	}
	return recorder.body.Write(chunk)
}
