package builtintools

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

func TestDeepSeekSearchBackend(t *testing.T) {
	var searchBody, fetchBody Object
	executor := Executor{Secrets: testSecrets{}, Client: testClient(t, func(r *http.Request) Object {
		body := decode(t, readBody(t, r))
		switch r.URL.Path {
		case "/v1/tools/search":
			searchBody = body
			return Object{"results": []any{Object{"title": "Source", "url": "https://example.com/fact", "snippet": "short", "content": "Verified fact"}}}
		case "/v1/tools/fetch":
			fetchBody = body
			return Object{"title": "Page", "content": "Page body with the pattern inside."}
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
			return nil
		}
	})}
	config := contract.BuiltinTool{Enabled: true, Backend: "external", BaseURL: "https://api.deepseek.com/v1/tools"}

	result, err := executor.Execute(context.Background(), config, Invocation{Kind: "web_search", Arguments: Object{"action": "search", "query": "fact"}})
	if err != nil {
		t.Fatal(err)
	}
	if searchBody["count"] != float64(5) || searchBody["need_content"] != true || searchBody["query"] != "fact" {
		t.Fatalf("search payload = %v", searchBody)
	}
	if _, ok := searchBody["max_results"]; ok {
		t.Fatal("Tavily-only parameter sent to DeepSeek")
	}
	if len(result.Sources) != 1 || !strings.Contains(result.Output, "Verified fact") {
		t.Fatalf("result = %+v", result)
	}

	result, err = executor.Execute(context.Background(), config, Invocation{Kind: "web_search", Arguments: Object{"action": "find", "url": "https://example.com/doc", "pattern": "pattern"}})
	if err != nil {
		t.Fatal(err)
	}
	if fetchBody["url"] != "https://example.com/doc" {
		t.Fatalf("fetch payload = %v", fetchBody)
	}
	if !strings.Contains(result.Output, "pattern") {
		t.Fatalf("output = %s", result.Output)
	}
}

func TestDeepSeekSearchURLCompletion(t *testing.T) {
	for _, base := range []string{"https://api.deepseek.com", "https://api.deepseek.com/", "https://api.deepseek.com/v1", "https://api.deepseek.com/v1/tools"} {
		if got := deepSeekSearchURL(base); got != "https://api.deepseek.com/v1/tools" {
			t.Fatalf("%s -> %s", base, got)
		}
	}
	if deepSeekSearchBase("https://tools.example/v1") {
		t.Fatal("non-DeepSeek backend treated as DeepSeek")
	}
}

func TestTavilyBackendUnchanged(t *testing.T) {
	var body Object
	executor := Executor{Secrets: testSecrets{}, Client: testClient(t, func(r *http.Request) Object {
		body = decode(t, readBody(t, r))
		if r.URL.Path != "/v1/search" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		return Object{"results": []any{Object{"title": "Source", "url": "https://example.com/fact", "raw_content": "Verified fact"}}}
	})}
	if _, err := executor.Execute(context.Background(), toolConfig(), Invocation{Kind: "web_search", Arguments: Object{"action": "search", "query": "fact"}}); err != nil {
		t.Fatal(err)
	}
	if body["max_results"] != float64(5) || body["search_depth"] != "basic" || body["include_raw_content"] != true {
		t.Fatalf("payload = %v", body)
	}
	if _, ok := body["need_content"]; ok {
		t.Fatal("DeepSeek parameter sent to Tavily")
	}
}

func readBody(t *testing.T, r *http.Request) string {
	t.Helper()
	data, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestDeepSeekDomainFilters(t *testing.T) {
	var body Object
	executor := Executor{Secrets: testSecrets{}, Client: testClient(t, func(r *http.Request) Object {
		body = decode(t, readBody(t, r))
		return Object{"results": []any{}}
	})}
	config := contract.BuiltinTool{Enabled: true, Backend: "external", BaseURL: "https://api.deepseek.com"}
	options := Object{"filters": Object{
		"allowed_domains": []any{"github.com", "linux.do"},
		"blocked_domains": []any{"spam.example"},
	}}
	if _, err := executor.Execute(context.Background(), config, Invocation{Kind: "web_search", Arguments: Object{"action": "search", "query": "AstrLink"}, Options: options}); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["include_domains"]; ok {
		t.Fatal("Tavily domain field sent to DeepSeek")
	}
	if _, ok := body["exclude_domains"]; ok {
		t.Fatal("Tavily exclusion field sent to DeepSeek")
	}
	query, _ := body["query"].(string)
	if !strings.Contains(query, "site:github.com OR site:linux.do") || !strings.Contains(query, "-site:spam.example") {
		t.Fatalf("query = %q", query)
	}
}

func TestDeepSeekHostMatching(t *testing.T) {
	for _, base := range []string{"https://api.deepseek.com", "https://API.DEEPSEEK.COM/v1", "https://api.deepseek.com:443/v1/tools"} {
		if !deepSeekSearchBase(base) {
			t.Fatalf("%s not detected", base)
		}
	}
	for _, base := range []string{"https://api.deepseek.com.evil.com", "https://xapi.deepseek.com", "https://api.tavily.com"} {
		if deepSeekSearchBase(base) {
			t.Fatalf("%s wrongly detected", base)
		}
	}
}
