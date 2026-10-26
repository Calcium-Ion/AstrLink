package ingress

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/privacy"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

// compressBody applies codings in the order a Content-Encoding header lists
// them.
func compressBody(t *testing.T, body []byte, codings ...string) []byte {
	t.Helper()
	for _, coding := range codings {
		var buffer bytes.Buffer
		var writer io.WriteCloser
		switch coding {
		case "gzip", "x-gzip":
			writer = gzip.NewWriter(&buffer)
		case "deflate":
			writer = zlib.NewWriter(&buffer)
		case "br":
			writer = brotli.NewWriter(&buffer)
		case "zstd":
			encoder, err := zstd.NewWriter(&buffer)
			if err != nil {
				t.Fatal(err)
			}
			writer = encoder
		default:
			t.Fatalf("no encoder for %q", coding)
		}
		if _, err := writer.Write(body); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		body = buffer.Bytes()
	}
	return body
}

func TestInferencePlaneDecodesCompressedRequestBodies(t *testing.T) {
	const plain = `{"model":"gpt-5","stream":true,"input":"hello"}`
	for _, codings := range [][]string{{"gzip"}, {"x-gzip"}, {"deflate"}, {"br"}, {"zstd"}, {"zstd", "gzip"}} {
		encoding := strings.Join(codings, ", ")
		t.Run(encoding, func(t *testing.T) {
			var resolved endpoint.ResolveRequest
			forwarded := false
			handler := NewWithDependencies(Dependencies{
				Resolver: resolverFunc(func(_ context.Context, request endpoint.ResolveRequest) (endpoint.Resolved, error) {
					resolved = request
					return endpoint.Resolved{Endpoint: validEndpoint(contract.ProtocolOpenAIResponses, true)}, nil
				}),
				Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
					forwarded = true
					body, err := io.ReadAll(request.Body)
					if err != nil {
						t.Fatal(err)
					}
					if string(body) != plain {
						t.Errorf("upstream body = %q, want the decoded JSON", body)
					}
					if got := request.Header.Get("Content-Encoding"); got != "" {
						t.Errorf("upstream Content-Encoding = %q", got)
					}
					if request.ContentLength != int64(len(plain)) {
						t.Errorf("upstream ContentLength = %d, want %d", request.ContentLength, len(plain))
					}
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Content-Type": {"application/json"}},
						Body:       io.NopCloser(strings.NewReader(`{"id":"resp_01"}`)),
					}, nil
				})),
			})
			request := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(compressBody(t, []byte(plain), codings...)))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Content-Encoding", encoding)
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			if !forwarded || response.Code != http.StatusOK {
				t.Fatalf("forwarded=%t response=%d %s", forwarded, response.Code, response.Body.String())
			}
			if resolved.Model != "gpt-5" || !resolved.Streaming {
				t.Fatalf("resolve request = %#v", resolved)
			}
		})
	}
}

func TestInferencePlaneAppliesTheBodyLimitAfterDecoding(t *testing.T) {
	called := false
	handler := NewWithDependencies(Dependencies{
		MaxRequestBodyMiB: 1,
		Resolver: resolverFunc(func(context.Context, endpoint.ResolveRequest) (endpoint.Resolved, error) {
			called = true
			return endpoint.Resolved{}, nil
		}),
	})
	plain := `{"model":"gpt-5","input":"` + strings.Repeat("x", 1<<20) + `"}`
	compressed := compressBody(t, []byte(plain), "gzip")
	if len(compressed) >= 1<<20 {
		t.Fatalf("compressed body is %d bytes; it must fit the wire limit", len(compressed))
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(compressed))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Content-Encoding", "gzip")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	assertInferenceError(t, response, http.StatusRequestEntityTooLarge, "request_too_large")
	if called {
		t.Fatal("resolver ran for a body over the limit")
	}
}

func TestDecodeRequestBody(t *testing.T) {
	const plain = `{"model":"gpt-5"}`
	compressed := compressBody(t, []byte(plain), "br")

	request := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(compressed))
	request.Header.Set("Content-Encoding", "br")
	request.Header.Set("Content-Length", strconv.Itoa(len(compressed)))
	sent, err := decodeRequestBody(request, 1024, errDecodedBodyTooLarge)
	if err != nil {
		t.Fatal(err)
	}
	if sent.Get("Content-Encoding") != "br" || sent.Get("Content-Length") != strconv.Itoa(len(compressed)) {
		t.Fatalf("sent body headers = %#v", sent)
	}
	if request.Header.Get("Content-Encoding") != "" || request.Header.Get("Content-Length") != "" ||
		request.ContentLength != int64(len(plain)) {
		t.Fatalf("decoded request headers = %#v, length %d", request.Header, request.ContentLength)
	}
	for _, read := range []func() (io.ReadCloser, error){
		func() (io.ReadCloser, error) { return request.Body, nil },
		request.GetBody,
	} {
		body, err := read()
		if err != nil {
			t.Fatal(err)
		}
		if decoded, err := io.ReadAll(body); err != nil || string(decoded) != plain {
			t.Fatalf("decoded body = %q, %v", decoded, err)
		}
	}

	// Past the bound, decoding stops with the caller's error.
	request = httptest.NewRequest(http.MethodPost, "/v1/responses",
		bytes.NewReader(compressBody(t, bytes.Repeat([]byte("x"), 1025), "zstd")))
	request.Header.Set("Content-Encoding", "zstd")
	if _, err := decodeRequestBody(request, 1024, errDecodedBodyTooLarge); !errors.Is(err, errDecodedBodyTooLarge) {
		t.Fatalf("oversized decode err = %v", err)
	}

	// A coding it does not know stays for the routes to judge.
	request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-test:streamGenerateContent", strings.NewReader("opaque"))
	request.Header.Set("Content-Encoding", "compress")
	sent, err = decodeRequestBody(request, 1024, errDecodedBodyTooLarge)
	if err != nil || sent != nil || request.Header.Get("Content-Encoding") != "compress" {
		t.Fatalf("unknown coding: sent=%#v err=%v header=%q", sent, err, request.Header.Get("Content-Encoding"))
	}
	if body, _ := io.ReadAll(request.Body); string(body) != "opaque" {
		t.Fatalf("unknown coding body = %q", body)
	}
}

func TestRequestRecordKeepsTheBodyHeadersTheClientSent(t *testing.T) {
	session := &recordSession{
		httpMetaEnabled: true,
		classified: Request{sentBodyHeaders: http.Header{
			"Content-Encoding": {"zstd"},
			"Content-Length":   {"42"},
		}},
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")

	session.captureHTTPRequestMeta(request)

	got := map[string]string{}
	for _, header := range session.httpMeta.RequestHeaders {
		got[header.Name] = header.Value
	}
	if got["content-encoding"] != "zstd" || got["content-length"] != "42" || got["content-type"] != "application/json" {
		t.Fatalf("recorded request headers = %#v", session.httpMeta.RequestHeaders)
	}
	if request.Header.Get("Content-Encoding") != "" {
		t.Fatal("recording changed the request headers")
	}
}

func TestPrivacyInspectsCompressedRequestBodies(t *testing.T) {
	policy, err := privacy.FromContractPolicy(contract.DefaultPrivacyPolicy())
	if err != nil {
		t.Fatal(err)
	}
	const secret = "k8Jd2nQ7xR4mT9pL3vZ6"
	for _, test := range []struct {
		name, path, encoding string
		protocol             contract.ProtocolID
		body                 string
	}{
		{
			name: "responses zstd", path: "/v1/responses", encoding: "zstd",
			protocol: contract.ProtocolOpenAIResponses,
			body:     `{"model":"gpt-5","input":"api_key=` + secret + `"}`,
		},
		{
			// The model is in the path, so this body used to reach privacy
			// still encoded.
			name: "gemini gzip", path: "/v1beta/models/gemini-test:generateContent", encoding: "gzip",
			protocol: contract.ProtocolGoogleGenerateContent,
			body:     `{"contents":[{"role":"user","parts":[{"text":"api_key=` + secret + `"}]}]}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var forwarded []byte
			upstream := validEndpoint(test.protocol, false)
			handler := NewWithDependencies(Dependencies{
				Resolver: resolverFunc(func(context.Context, endpoint.ResolveRequest) (endpoint.Resolved, error) {
					return endpoint.Resolved{Endpoint: upstream}, nil
				}),
				PrivacyFilter: testPrivacyEngine(t, policy, nil),
				Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
					body, err := io.ReadAll(request.Body)
					if err != nil {
						t.Fatal(err)
					}
					forwarded = body
					if got := request.Header.Get("Content-Encoding"); got != "" {
						t.Errorf("upstream Content-Encoding = %q", got)
					}
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Content-Type": {"application/json"}},
						Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
					}, nil
				})),
			})
			request := httptest.NewRequest(http.MethodPost, test.path, bytes.NewReader(compressBody(t, []byte(test.body), test.encoding)))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Content-Encoding", test.encoding)
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			if response.Code != http.StatusOK {
				t.Fatalf("response=%d %s", response.Code, response.Body.String())
			}
			if strings.Contains(string(forwarded), secret) || !strings.Contains(string(forwarded), "api_key=<SECRET_") {
				t.Fatalf("forwarded body = %s, want the credential replaced", forwarded)
			}
		})
	}
}
