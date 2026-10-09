package ingress

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
	"github.com/QuantumNous/astrlink/core/internal/builtintools"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
)

// searchRequest is the body of Codex's standalone web search tool, web.run.
// Its input carries the recent conversation, which the built-in search never
// forwards.
type searchRequest struct {
	ID              string         `json:"id"`
	Settings        searchSettings `json:"settings"`
	MaxOutputTokens uint64         `json:"max_output_tokens"`
}

type searchCommands struct {
	SearchQuery []struct {
		Q       string   `json:"q"`
		Domains []string `json:"domains"`
	} `json:"search_query"`
	Open []struct {
		RefID string `json:"ref_id"`
	} `json:"open"`
	Find []struct {
		RefID   string `json:"ref_id"`
		Pattern string `json:"pattern"`
	} `json:"find"`
	ResponseLength string `json:"response_length"`
}

type searchSettings struct {
	SearchContextSize string `json:"search_context_size"`
	Filters           struct {
		AllowedDomains []string `json:"allowed_domains"`
		BlockedDomains []string `json:"blocked_domains"`
	} `json:"filters"`
	ExternalWebAccess json.RawMessage `json:"external_web_access"`
}

type searchResponse struct {
	EncryptedOutput *string `json:"encrypted_output"`
	Output          string  `json:"output"`
}

// The commands the built-in web search can answer. Everything else web.run
// offers (image_query, click, screenshot, finance, weather, sports, time) is
// reported back to the model as unavailable.
var searchCommandNames = map[string]bool{"search_query": true, "open": true, "find": true, "response_length": true}

const (
	minSearchOutputRunes     = 2_000
	defaultSearchOutputRunes = 40_000
	maxSearchOutputRunes     = 60_000
)

// serveSearch answers Codex's /v1/alpha/search. The web search configured
// under built-in tools comes first. With it turned off, a search in a turn a
// Codex subscription served goes to that subscription.
//
// Codex ends the whole turn when this request fails, so every outcome after
// authentication is a 200 whose output tells the model what happened. The
// request record still shows the failure.
func (handler *Handler) serveSearch(writer http.ResponseWriter, request *http.Request, classified Request, settings contract.RoutingSettings) {
	session := recordSessionFromContext(request.Context())
	reply := func(output string) {
		header := writer.Header()
		header.Set("Cache-Control", "no-store")
		header.Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(writer).Encode(searchResponse{Output: output})
	}
	failed := func(code, message, output string) {
		session.noteFailed(errorSummaryFromInference(code, message, false))
		reply(output)
	}
	limit := handler.maxRequestBodyBytes
	if limit == 0 {
		limit = 128 << 20
	}
	raw, err := io.ReadAll(io.LimitReader(request.Body, limit+1))
	var original map[string]json.RawMessage
	if err != nil || int64(len(raw)) > limit || json.Unmarshal(raw, &original) != nil || original == nil {
		failed("invalid_request", "search request could not be decoded", "The search request could not be read, so nothing was searched.")
		return
	}
	var config contract.BuiltinTool
	if settings.BuiltinTools != nil {
		config = settings.BuiltinTools.WebSearch
	}
	if !config.Enabled || config.Validate("web_search") != nil {
		ref := codexTurnFromHeader(request.Header)
		if ref.sessionID == "" {
			var id string
			_ = json.Unmarshal(original["id"], &id)
			ref.sessionID = codexTurnID(id)
		}
		candidate, binding, ok := handler.codexToolProvider(request.Context(), ref, "web_search")
		if !ok {
			failed("web_search_disabled", "web search is turned off",
				"Web search is turned off for this gateway, so nothing was searched. It can be turned on under Routing → Models & tools → Built-in tools. Answer without web results, and tell the user if the answer needs them.")
			return
		}
		replaceRecoveryRequestBody(request, raw)
		handler.forwardCodexSearch(writer, request, classified, settings, candidate, binding, reply, failed)
		return
	}
	// The built-in search never forwards the conversation Codex attaches, so
	// it is dropped before the privacy check.
	delete(original, "input")
	stripped, err := json.Marshal(original)
	if err != nil {
		failed("invalid_request", "search request could not be decoded", "The search request could not be read, so nothing was searched.")
		return
	}
	replaceRecoveryRequestBody(request, stripped)
	finishPrivacy, _, err := handler.applyPrivacy(writer, request, classified, config.ServiceID)
	defer finishPrivacy()
	if err != nil {
		handler.searchPrivacyFailure(request, err, reply)
		return
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, limit+1))
	var input searchRequest
	var fields map[string]json.RawMessage
	if err != nil || int64(len(body)) > limit || json.Unmarshal(body, &input) != nil || json.Unmarshal(body, &fields) != nil {
		failed("invalid_request", "search request could not be decoded", "The search request could not be read, so nothing was searched.")
		return
	}
	var commandFields map[string]json.RawMessage
	var commands searchCommands
	if raw := fields["commands"]; len(raw) != 0 && string(raw) != "null" {
		if json.Unmarshal(raw, &commandFields) != nil || json.Unmarshal(raw, &commands) != nil {
			failed("invalid_request", "search commands could not be decoded", "The search commands could not be read, so nothing was searched.")
			return
		}
	}
	session.noteAttemptedService(config.ServiceID)
	run := searchRun{
		handler:   handler,
		request:   request,
		session:   session,
		config:    config,
		executor:  handler.builtinExecutor(request, classified),
		options:   searchOptions(input.Settings),
		resultCap: searchResultRunes(commands.ResponseLength),
	}
	// The whole request already passed the privacy policy above.
	run.executor.Inspect = nil
	principal, _ := AccessTokenIDFromContext(request.Context())
	run.refs = searchRefKey{principal: string(principal), session: input.ID}
	run.turn = handler.searchRefs.nextTurn(run.refs, time.Now())
	var output strings.Builder
	for _, query := range commands.SearchQuery {
		run.search(&output, query.Q, query.Domains)
	}
	for _, open := range commands.Open {
		run.open(&output, open.RefID)
	}
	for _, find := range commands.Find {
		run.find(&output, find.RefID, find.Pattern)
	}
	if request.Context().Err() != nil {
		return
	}
	var unsupported []string
	for name, raw := range commandFields {
		if !searchCommandNames[name] && string(raw) != "null" && string(raw) != "[]" {
			unsupported = append(unsupported, name)
		}
	}
	sort.Strings(unsupported)
	if len(unsupported) != 0 {
		fmt.Fprintf(&output, "These web.run commands are not available here: %s. Use search_query, open and find instead.\n\n", strings.Join(unsupported, ", "))
	}
	if run.calls == 0 && len(unsupported) == 0 {
		output.WriteString("No search_query, open or find command was given, so nothing was searched.\n")
	}
	text := strings.TrimSpace(output.String())
	runes := defaultSearchOutputRunes
	if input.MaxOutputTokens > 0 {
		runes = max(int(min(input.MaxOutputTokens, maxSearchOutputRunes/4)*4), minSearchOutputRunes)
	}
	if clipped := []rune(text); len(clipped) > runes {
		text = string(clipped[:runes]) + "\n[output truncated]"
	}
	if run.calls > 0 && run.succeeded == 0 {
		failed("builtin_tool_failed", run.lastError, text)
		return
	}
	reply(text)
	session.noteSucceeded()
}

// searchPrivacyFailure answers a search the privacy policy stopped. The record
// keeps the decision, and Codex gets a 200 so its turn goes on.
func (handler *Handler) searchPrivacyFailure(request *http.Request, err error, reply func(string)) {
	capture := &builtinCapture{header: make(http.Header)}
	handler.writePrivacyError(capture, request, err)
	if request.Context().Err() != nil {
		return
	}
	if errors.Is(err, errPrivacyBlocked) {
		reply("The local privacy policy blocked this search, so nothing was searched. Do not retry it with the same content.")
		return
	}
	reply("The local privacy check could not inspect this search, so nothing was searched.")
}

// maxCodexSearchResponseBytes bounds a search answer from a Codex
// subscription; Codex itself keeps only what fits the model's budget.
const maxCodexSearchResponseBytes = 8 << 20

// forwardCodexSearch sends a search to the Codex subscription that served the
// turn, naming the model that served it. A valid answer goes back as is. A
// failure becomes a 200 that tells the model, because a failed search ends
// the whole Codex turn.
func (handler *Handler) forwardCodexSearch(
	writer http.ResponseWriter,
	request *http.Request,
	classified Request,
	settings contract.RoutingSettings,
	candidate endpoint.Resolved,
	binding codexTurnBinding,
	reply func(string),
	failed func(code, message, output string),
) {
	session := recordSessionFromContext(request.Context())
	service := candidate.CanonicalService()
	finishPrivacy, _, err := handler.applyPrivacy(writer, request, classified, service.ID)
	defer finishPrivacy()
	if err != nil {
		handler.searchPrivacyFailure(request, err, reply)
		return
	}
	limit := handler.maxRequestBodyBytes
	if limit == 0 {
		limit = 128 << 20
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, limit+1))
	var fields map[string]json.RawMessage
	if err != nil || int64(len(body)) > limit || json.Unmarshal(body, &fields) != nil || fields == nil {
		failed("invalid_request", "search request could not be decoded", "The search request could not be read, so nothing was searched.")
		return
	}
	if binding.model != "" {
		fields["model"], _ = json.Marshal(binding.model)
	}
	// The turn's session ID was scoped to this account the same way.
	var id string
	if settings.SubscriptionSessionIsolation && json.Unmarshal(fields["id"], &id) == nil && id != "" {
		fields["id"], _ = json.Marshal(accountauth.ScopedSessionID(service.ID, id))
	}
	if body, err = json.Marshal(fields); err != nil {
		failed("invalid_request", "search request could not be encoded", "The search request could not be read, so nothing was searched.")
		return
	}
	session.noteAttemptedService(service.ID)
	started := time.Now()
	var answer []byte
	err = handler.forwardCodexTool(writer, request, candidate, binding, settings, body, func(response *http.Response) error {
		defer response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return fmt.Errorf("the provider returned HTTP %d", response.StatusCode)
		}
		data, err := readCodexToolResponse(response, maxCodexSearchResponseBytes)
		if err != nil {
			return err
		}
		var parsed struct {
			Output *string `json:"output"`
		}
		if json.Unmarshal(data, &parsed) != nil || parsed.Output == nil {
			return errors.New("the provider returned an invalid search answer")
		}
		answer = data
		return nil
	})
	session.noteBuiltinTool("web_search", contract.BuiltinTool{Backend: "provider", ServiceID: service.ID}, started, builtintools.Result{}, err)
	if request.Context().Err() != nil {
		return
	}
	if err != nil {
		failed("web_search_failed", err.Error(),
			"The web search through this conversation's provider failed ("+err.Error()+"), so no results came back. Answer without web results, and tell the user if the answer needs them.")
		return
	}
	header := writer.Header()
	header.Set("Cache-Control", "no-store")
	header.Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(answer)
	session.noteSucceeded()
}

type searchRun struct {
	handler   *Handler
	request   *http.Request
	session   *recordSession
	config    contract.BuiltinTool
	executor  builtintools.Executor
	options   builtintools.Object
	resultCap int
	refs      searchRefKey
	turn      int
	searches  int
	views     int
	calls     int
	succeeded int
	lastError string
}

func (run *searchRun) execute(arguments, options builtintools.Object) (builtintools.Result, error) {
	run.calls++
	started := time.Now()
	result, err := run.executor.Execute(run.request.Context(), run.config, builtintools.Invocation{Kind: "web_search", Arguments: arguments, Options: options})
	run.session.noteBuiltinTool("web_search", run.config, started, result, err)
	if err != nil {
		run.lastError = err.Error()
	} else {
		run.succeeded++
	}
	return result, err
}

func (run *searchRun) search(output *strings.Builder, query string, domains []string) {
	options := builtintools.Clone(run.options)
	if len(domains) != 0 {
		filters := builtintools.Map(options["filters"])
		if filters == nil {
			filters = builtintools.Object{}
		}
		filters["allowed_domains"] = domains
		options["filters"] = filters
	}
	result, err := run.execute(builtintools.Object{"action": "search", "query": query}, options)
	if err != nil {
		fmt.Fprintf(output, "search_query %q failed: %v\n\n", query, err)
		return
	}
	pages, summary := searchPages(result)
	fmt.Fprintf(output, "Results for search_query %q:\n", query)
	if summary != "" {
		output.WriteString(clipRunes(summary, run.resultCap*2) + "\n")
	}
	if len(pages) == 0 && summary == "" {
		output.WriteString("No results.\n")
	}
	for _, page := range pages {
		ref := fmt.Sprintf("turn%dsearch%d", run.turn, run.searches)
		run.searches++
		run.handler.searchRefs.put(run.refs, ref, page.URL, time.Now())
		fmt.Fprintf(output, "\n[%s] %s\n%s\n", ref, page.Title, page.URL)
		if page.Content != "" {
			output.WriteString(clipRunes(page.Content, run.resultCap) + "\n")
		}
	}
	output.WriteString("\n")
}

func (run *searchRun) open(output *strings.Builder, refID string) {
	address, ok := run.resolve(refID)
	if !ok {
		fmt.Fprintf(output, "open %q failed: unknown reference ID; pass a reference ID from an earlier result or the page URL.\n\n", refID)
		return
	}
	result, err := run.execute(builtintools.Object{"action": "open", "url": address}, builtintools.Clone(run.options))
	if err != nil {
		fmt.Fprintf(output, "open %s failed: %v\n\n", address, err)
		return
	}
	pages, summary := searchPages(result)
	ref := fmt.Sprintf("turn%dview%d", run.turn, run.views)
	run.views++
	run.handler.searchRefs.put(run.refs, ref, address, time.Now())
	title := ""
	if len(pages) != 0 {
		title = pages[0].Title
	}
	fmt.Fprintf(output, "[%s] %s\n%s\n", ref, title, address)
	for _, page := range pages {
		if page.Content != "" {
			output.WriteString(clipRunes(page.Content, run.resultCap*4) + "\n")
		}
	}
	if summary != "" {
		output.WriteString(clipRunes(summary, run.resultCap*4) + "\n")
	}
	output.WriteString("\n")
}

func (run *searchRun) find(output *strings.Builder, refID, pattern string) {
	address, ok := run.resolve(refID)
	if !ok {
		fmt.Fprintf(output, "find %q failed: unknown reference ID; pass a reference ID from an earlier result or the page URL.\n\n", refID)
		return
	}
	result, err := run.execute(builtintools.Object{"action": "find", "url": address, "pattern": pattern}, builtintools.Clone(run.options))
	if err != nil {
		fmt.Fprintf(output, "find %q in %s failed: %v\n\n", pattern, address, err)
		return
	}
	pages, summary := searchPages(result)
	fmt.Fprintf(output, "Matches for %q in %s:\n", pattern, address)
	for _, page := range pages {
		if page.Content != "" {
			output.WriteString(clipRunes(page.Content, run.resultCap*2) + "\n")
		}
	}
	if summary != "" {
		output.WriteString(clipRunes(summary, run.resultCap*2) + "\n")
	}
	output.WriteString("\n")
}

// resolve accepts a page URL or a reference ID handed out earlier in the same
// Codex session.
func (run *searchRun) resolve(refID string) (string, bool) {
	if parsed, err := url.Parse(refID); err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != "" {
		return refID, true
	}
	return run.handler.searchRefs.lookup(run.refs, refID, time.Now())
}

type searchPage struct {
	URL, Title, Content string
}

// searchPages reads the two shapes built-in search returns: pages with their
// text from a search API, or a model's written answer with its sources.
func searchPages(result builtintools.Result) ([]searchPage, string) {
	var output struct {
		Results []struct {
			URL     string `json:"url"`
			Title   string `json:"title"`
			Content string `json:"content"`
		} `json:"results"`
		Result  string `json:"result"`
		Sources []struct {
			URL   string `json:"url"`
			Title string `json:"title"`
		} `json:"sources"`
	}
	_ = json.Unmarshal([]byte(result.Output), &output)
	var pages []searchPage
	for _, item := range output.Results {
		pages = append(pages, searchPage{URL: item.URL, Title: item.Title, Content: item.Content})
	}
	seen := map[string]bool{}
	for _, page := range pages {
		seen[page.URL] = true
	}
	for _, source := range output.Sources {
		if source.URL == "" || seen[source.URL] {
			continue
		}
		seen[source.URL] = true
		pages = append(pages, searchPage{URL: source.URL, Title: source.Title})
	}
	return pages, strings.TrimSpace(output.Result)
}

// searchOptions maps web.run settings onto the hosted web_search options the
// built-in executor understands. A search API searches the live web even when
// Codex asks for cached results only.
func searchOptions(settings searchSettings) builtintools.Object {
	options := builtintools.Object{}
	switch settings.SearchContextSize {
	case "low", "medium", "high":
		options["search_context_size"] = settings.SearchContextSize
	}
	filters := builtintools.Object{}
	if len(settings.Filters.AllowedDomains) != 0 {
		filters["allowed_domains"] = settings.Filters.AllowedDomains
	}
	if len(settings.Filters.BlockedDomains) != 0 {
		filters["blocked_domains"] = settings.Filters.BlockedDomains
	}
	if len(filters) != 0 {
		options["filters"] = filters
	}
	var live bool
	if json.Unmarshal(settings.ExternalWebAccess, &live) == nil {
		options["external_web_access"] = live
	}
	return options
}

func searchResultRunes(length string) int {
	switch length {
	case "short":
		return 600
	case "long":
		return 4000
	default:
		return 1500
	}
}

func clipRunes(text string, limit int) string {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit]) + "…"
}

// builtinToolPreview names what a Codex image or search request asks for, as
// the request record's title.
func builtinToolPreview(protocol contract.ProtocolID, fields map[string]json.RawMessage) string {
	switch protocol {
	case contract.ProtocolOpenAIImages:
		var prompt string
		_ = json.Unmarshal(fields["prompt"], &prompt)
		return prompt
	case contract.ProtocolOpenAISearch:
		var commands searchCommands
		_ = json.Unmarshal(fields["commands"], &commands)
		var queries []string
		for _, query := range commands.SearchQuery {
			queries = append(queries, query.Q)
		}
		return strings.Join(queries, " · ")
	}
	return ""
}

// searchRefStore maps the reference IDs in search output back to page URLs,
// so a later open or find in the same Codex session can name a result by ID.
// It holds only URLs, keyed by access token and Codex session.
type searchRefStore struct {
	mu       sync.Mutex
	sessions map[searchRefKey]*searchRefSession
}

type searchRefKey struct{ principal, session string }

type searchRefSession struct {
	turn int
	refs map[string]string
	used time.Time
}

const (
	searchRefTTL          = 24 * time.Hour
	searchRefSessionLimit = 256
	searchRefLimit        = 2048
)

func (store *searchRefStore) nextTurn(key searchRefKey, now time.Time) int {
	store.mu.Lock()
	defer store.mu.Unlock()
	entry := store.entry(key, now)
	turn := entry.turn
	entry.turn++
	return turn
}

func (store *searchRefStore) put(key searchRefKey, ref, address string, now time.Time) {
	store.mu.Lock()
	defer store.mu.Unlock()
	entry := store.entry(key, now)
	if len(entry.refs) >= searchRefLimit {
		entry.refs = map[string]string{}
	}
	entry.refs[ref] = address
}

func (store *searchRefStore) lookup(key searchRefKey, ref string, now time.Time) (string, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	entry := store.sessions[key]
	if entry == nil || now.Sub(entry.used) >= searchRefTTL {
		return "", false
	}
	entry.used = now
	address, ok := entry.refs[ref]
	return address, ok
}

// entry returns the session's references, dropping expired sessions and the
// least recently used one when the store is full. The caller holds store.mu.
func (store *searchRefStore) entry(key searchRefKey, now time.Time) *searchRefSession {
	if store.sessions == nil {
		store.sessions = map[searchRefKey]*searchRefSession{}
	}
	for existing, entry := range store.sessions {
		if now.Sub(entry.used) >= searchRefTTL {
			delete(store.sessions, existing)
		}
	}
	entry := store.sessions[key]
	if entry == nil {
		if len(store.sessions) >= searchRefSessionLimit {
			var oldest searchRefKey
			var oldestUsed time.Time
			for existing, candidate := range store.sessions {
				if oldestUsed.IsZero() || candidate.used.Before(oldestUsed) {
					oldest, oldestUsed = existing, candidate.used
				}
			}
			delete(store.sessions, oldest)
		}
		entry = &searchRefSession{refs: map[string]string{}}
		store.sessions[key] = entry
	}
	entry.used = now
	return entry
}
