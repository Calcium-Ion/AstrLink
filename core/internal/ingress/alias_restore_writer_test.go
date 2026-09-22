package ingress

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestAliasRestoringWriterNonStreaming(t *testing.T) {
	tests := []struct {
		name          string
		publicModel   string
		upstreamModel string
		memberNames   map[string]bool
		contentType   string
		body          string
		wantBody      string
		wantLength    bool
	}{
		{
			name:          "top-level model restored and content-length corrected",
			publicModel:   "public-alias",
			upstreamModel: "upstream-real",
			memberNames:   map[string]bool{"model": true},
			contentType:   "application/json",
			body:          `{"id":"resp","model":"upstream-real","output":"ok"}`,
			wantBody:      `{"id":"resp","model":"public-alias","output":"ok"}`,
			wantLength:    true,
		},
		{
			name:          "gemini modelVersion restored",
			publicModel:   "public-gemini",
			upstreamModel: "gemini-real",
			memberNames:   map[string]bool{"modelVersion": true, "model": true},
			contentType:   "application/json",
			body:          `{"modelVersion":"gemini-real","candidates":[{"content":{"parts":[{"text":"hi"}]}}]}`,
			wantBody:      `{"modelVersion":"public-gemini","candidates":[{"content":{"parts":[{"text":"hi"}]}}]}`,
			wantLength:    true,
		},
		{
			name:          "equality guard leaves other model values untouched",
			publicModel:   "public-alias",
			upstreamModel: "upstream-real",
			memberNames:   map[string]bool{"model": true},
			contentType:   "application/json",
			body:          `{"model":"other-model","note":"upstream-real"}`,
			wantBody:      `{"model":"other-model","note":"upstream-real"}`,
			wantLength:    true,
		},
		{
			name:          "text member containing upstream model name untouched",
			publicModel:   "public-alias",
			upstreamModel: "upstream-real",
			memberNames:   map[string]bool{"model": true},
			contentType:   "application/json",
			body:          `{"model":"upstream-real","text":"please use upstream-real carefully"}`,
			wantBody:      `{"model":"public-alias","text":"please use upstream-real carefully"}`,
			wantLength:    true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			writer := newAliasRestoringWriter(
				recorder,
				test.publicModel,
				test.upstreamModel,
				false,
				test.memberNames,
			)
			writer.Header().Set("Content-Type", test.contentType)
			writer.Header().Set("Content-Length", strconv.Itoa(len(test.body)))
			writer.WriteHeader(http.StatusOK)
			if _, err := writer.Write([]byte(test.body)); err != nil {
				t.Fatalf("Write: %v", err)
			}
			if err := writer.Finish(); err != nil {
				t.Fatalf("Finish: %v", err)
			}
			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
			}
			if recorder.Body.String() != test.wantBody {
				t.Fatalf("body = %q, want %q", recorder.Body.String(), test.wantBody)
			}
			if test.wantLength {
				if recorder.Header().Get("Content-Length") != strconv.Itoa(len(test.wantBody)) {
					t.Fatalf("Content-Length = %q, want %d", recorder.Header().Get("Content-Length"), len(test.wantBody))
				}
			}
		})
	}
}

func TestAliasRestoringWriterStreamingSSECarryAndPassthrough(t *testing.T) {
	const upstream = "upstream-real"
	const alias = "public-alias"
	payload := `{"model":"upstream-real","delta":"x"}`
	fullLine := "data: " + payload + "\n"
	// Split mid-token across three writes: inside the model string value.
	part1 := fullLine[:len("data: {\"model\":\"upst")]
	part2 := fullLine[len(part1) : len(part1)+5]
	part3 := fullLine[len(part1)+5:]

	recorder := httptest.NewRecorder()
	writer := newAliasRestoringWriter(
		recorder,
		alias,
		upstream,
		true,
		map[string]bool{"model": true},
	)
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.WriteHeader(http.StatusOK)

	for _, chunk := range []string{
		"event: response.created\n",
		"id: 1\n",
		"\n",
		part1,
		part2,
		part3,
		"data: [DONE]\n",
	} {
		if _, err := writer.Write([]byte(chunk)); err != nil {
			t.Fatalf("Write(%q): %v", chunk, err)
		}
	}
	if err := writer.Finish(); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	got := recorder.Body.String()
	want := "" +
		"event: response.created\n" +
		"id: 1\n" +
		"\n" +
		"data: {\"model\":\"public-alias\",\"delta\":\"x\"}\n" +
		"data: [DONE]\n"
	if got != want {
		t.Fatalf("sse body =\n%q\nwant\n%q", got, want)
	}
	if strings.Contains(got, upstream) {
		t.Fatalf("upstream model leaked: %q", got)
	}
}

func TestAliasRestoringWriterFailureModes(t *testing.T) {
	t.Run("oversized buffer", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		writer := newAliasRestoringWriter(
			recorder,
			"alias",
			"upstream",
			false,
			map[string]bool{"model": true},
		)
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusOK)
		chunk := bytes.Repeat([]byte("x"), maxResponseInspectionBytes+1)
		_, err := writer.Write(chunk)
		if err != errRestoreAborted {
			t.Fatalf("Write error = %v, want errRestoreAborted", err)
		}
		assertInferenceError(t, recorder, http.StatusBadGateway, "upstream_response_too_large")
	})

	t.Run("non-identity content encoding", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		writer := newAliasRestoringWriter(
			recorder,
			"alias",
			"upstream",
			false,
			map[string]bool{"model": true},
		)
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("Content-Encoding", "gzip")
		writer.WriteHeader(http.StatusOK)
		assertInferenceError(t, recorder, http.StatusBadGateway, "upstream_content_encoding")
	})
}

func TestAliasRestoringWriterFlushErrorDoesNotCommitBufferedBody(t *testing.T) {
	recorder := httptest.NewRecorder()
	writer := newAliasRestoringWriter(
		recorder,
		"alias",
		"upstream-real",
		false,
		map[string]bool{"model": true},
	)
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusOK)
	if _, err := writer.Write([]byte(`{"model":"upstream-real"}`)); err != nil {
		t.Fatal(err)
	}
	if err := writer.FlushError(); err != nil {
		t.Fatalf("FlushError: %v", err)
	}
	if recorder.Body.Len() != 0 || recorder.Code != http.StatusOK && recorder.Body.Len() != 0 {
		// httptest defaults Code to 200 even before WriteHeader to client;
		// ensure no body bytes were committed before Finish.
		if recorder.Body.Len() != 0 {
			t.Fatalf("buffered body flushed early: %q", recorder.Body.String())
		}
	}
	if err := writer.Finish(); err != nil {
		t.Fatal(err)
	}
	if recorder.Body.String() != `{"model":"alias"}` {
		t.Fatalf("body = %q", recorder.Body.String())
	}
	_ = io.Discard
}
