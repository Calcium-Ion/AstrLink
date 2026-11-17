package ingress

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"math/rand"
	"net/http"
	"slices"
	"strings"
	"testing"

	moewebp "github.com/MoeForge/moego/webp"
	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/imagecompress"
	"golang.org/x/image/webp"
)

// testScreenshot draws flat panels and text-like strokes.
func testScreenshot(width, height int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	draw.Draw(img, img.Bounds(), &image.Uniform{color.NRGBA{246, 247, 249, 255}}, image.Point{}, draw.Src)
	random := rand.New(rand.NewSource(11))
	for y := 10; y+10 < height; y += 16 {
		for x := 12; x < width-30; x += 6 + random.Intn(36) {
			ink := color.NRGBA{uint8(20 + random.Intn(60)), uint8(30 + random.Intn(60)), uint8(50 + random.Intn(90)), 255}
			draw.Draw(img, image.Rect(x, y, min(x+4+random.Intn(28), width), y+2+random.Intn(7)), &image.Uniform{ink}, image.Point{}, draw.Src)
		}
	}
	return img
}

func testPNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, img); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func testJPEG(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 160, 120))
	random := rand.New(rand.NewSource(5))
	for index := range img.Pix {
		img.Pix[index] = uint8(100 + random.Intn(60))
	}
	var buffer bytes.Buffer
	if err := jpeg.Encode(&buffer, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func losslessCompression() imageCompression {
	settings := contract.DefaultRoutingSettings()
	settings.ImageCompression.Enabled = true
	return imageCompressionFrom(settings)
}

func TestImageCompressionRewritesOnlyInlineImagesOfEachProtocol(t *testing.T) {
	source := testScreenshot(320, 200)
	pngData := testPNG(t, source)
	webpData, ok := new(imagecompress.Compressor).WebP(context.Background(), pngData, imagecompress.Options{})
	if !ok {
		t.Fatal("test screenshot does not compress")
	}
	pngBase64, webpBase64 := base64.StdEncoding.EncodeToString(pngData), base64.StdEncoding.EncodeToString(webpData)
	jpegURL := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(testJPEG(t))
	for _, test := range []struct {
		name     string
		protocol contract.ProtocolID
		// {IMG} is the screenshot as the protocol carries it and {TYPE} its
		// media type; {JPEG} is a photo lossless mode keeps.
		template   string
		dataURL    bool
		images     int
		compressed int
	}{
		{
			"chat", contract.ProtocolOpenAIChat,
			`{"model":"model-test","messages":[{"role":"user","content":[{"type":"text","text":"look"},` +
				`{"type":"image_url","image_url":{"url":"{IMG}","detail":"high"}},{"type":"image_url","image_url":"{IMG}"},` +
				`{"type":"image_url","image_url":{"url":"https://example.com/cat.png"}},{"type":"image_url","image_url":{"url":"{JPEG}"}}]}]}`,
			true, 3, 2,
		},
		{
			"responses", contract.ProtocolOpenAIResponses,
			`{"model":"model-test","input":[{"role":"user","content":[{"type":"input_image","image_url":"{IMG}","detail":"auto"}]},` +
				`{"type":"function_call_output","call_id":"call_1","output":[{"type":"input_image","image_url":"{IMG}"}]},` +
				`{"type":"computer_call_output","call_id":"call_2","output":{"type":"computer_screenshot","image_url":"{IMG}"}},` +
				`{"role":"user","content":[{"type":"input_image","file_id":"file_1"},{"type":"input_text","text":"{\"image_url\":\"data:image/png;base64,AAAA\"}"}]}]}`,
			true, 3, 3,
		},
		{
			"anthropic", contract.ProtocolAnthropicMessages,
			`{"model":"model-test","max_tokens":64,"messages":[{"role":"user","content":[` +
				`{"type":"image","source":{"type":"base64","media_type":"{TYPE}","data":"{IMG}"}},` +
				`{"type":"tool_result","tool_use_id":"tool_1","content":[{"type":"image","source":{"data":"{IMG}","media_type":"{TYPE}","type":"base64"}}]},` +
				`{"type":"image","source":{"type":"url","url":"https://example.com/cat.png"}},` +
				`{"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"JVBERi0xLjQK"}}]}]}`,
			false, 2, 2,
		},
		{
			"gemini", contract.ProtocolGoogleGenerateContent,
			`{"contents":[{"role":"user","parts":[{"inlineData":{"mimeType":"{TYPE}","data":"{IMG}"}},` +
				`{"inline_data":{"data":"{IMG}","mime_type":"{TYPE}"}},{"fileData":{"mimeType":"image/png","fileUri":"gs://bucket/cat.png"}},` +
				`{"inlineData":{"mimeType":"audio/wav","data":"UklGRg=="}}]}]}`,
			false, 2, 2,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			sent, kept := pngBase64, webpBase64
			if test.dataURL {
				sent, kept = "data:image/png;base64,"+sent, "data:image/webp;base64,"+kept
			}
			body := strings.NewReplacer("{IMG}", sent, "{TYPE}", "image/png", "{JPEG}", jpegURL).Replace(test.template)
			want := strings.NewReplacer("{IMG}", kept, "{TYPE}", "image/webp", "{JPEG}", jpegURL).Replace(test.template)
			got, result := compressRequestImages(context.Background(), new(imagecompress.Compressor), test.protocol, []byte(body), losslessCompression())
			if string(got) != want {
				t.Fatalf("body changed outside its inline images:\n%.400s", got)
			}
			if result.images != test.images || result.compressed != test.compressed || result.after >= result.before {
				t.Fatalf("result = %+v", result)
			}
		})
	}
	decoded, err := webp.Decode(bytes.NewReader(webpData))
	if err != nil {
		t.Fatal(err)
	}
	pixels := image.NewNRGBA(decoded.Bounds())
	draw.Draw(pixels, pixels.Bounds(), decoded, image.Point{}, draw.Src)
	if !bytes.Equal(pixels.Pix, source.Pix) {
		t.Fatal("the forwarded WebP differs from the client's screenshot")
	}
}

func TestImageCompressionLeavesOtherBodiesAlone(t *testing.T) {
	compressor := new(imagecompress.Compressor)
	pngURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(testPNG(t, testScreenshot(400, 300)))
	for name, body := range map[string]string{
		"text only":        `{"model":"model-test","messages":[{"role":"user","content":"hello"}]}`,
		"invalid JSON":     `{"messages":[{"type":"image_url","image_url":"` + pngURL + `"}`,
		"not base64":       `{"messages":[{"role":"user","content":[{"type":"image_url","image_url":"data:image/png;base64,%%%%"}]}]}`,
		"other data URL":   `{"messages":[{"role":"user","content":[{"type":"image_url","image_url":"data:text/plain;base64,aGVsbG8="}]}]}`,
		"wrong part shape": `{"messages":[{"role":"user","content":[{"type":"input_image","image_url":"` + pngURL + `"}]}]}`,
	} {
		got, result := compressRequestImages(context.Background(), compressor, contract.ProtocolOpenAIChat, []byte(body), losslessCompression())
		if string(got) != body || result.compressed != 0 {
			t.Fatalf("%s: body = %.200s, result %+v", name, got, result)
		}
	}
	// A cancelled request forwards its images as they are.
	body := `{"messages":[{"role":"user","content":[{"type":"image_url","image_url":"` + pngURL + `"}]}]}`
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if got, result := compressRequestImages(cancelled, compressor, contract.ProtocolOpenAIChat, []byte(body), losslessCompression()); string(got) != body || result.images != 1 {
		t.Fatalf("cancelled: result %+v", result)
	}
}

// imageEvents are the record's image_compression events.
func imageEvents(record contract.RequestRecord) []contract.RequestEvent {
	var events []contract.RequestEvent
	for _, event := range record.Events {
		if event.Kind == contract.RequestEventImageCompression {
			events = append(events, event)
		}
	}
	return events
}

// imageSummary is the summary of the record's only image_compression event,
// which must be a closed phase just ahead of the attempt's route event.
func imageSummary(t *testing.T, record contract.RequestRecord) string {
	t.Helper()
	if diagnostics := record.ConversionDiagnostics; len(diagnostics) > 0 {
		t.Fatalf("image compression is not a conversion loss: %+v", diagnostics)
	}
	events := imageEvents(record)
	if len(events) != 1 {
		t.Fatalf("image_compression events = %+v", events)
	}
	kinds := eventKinds(record.Events)
	at := slices.Index(kinds, string(contract.RequestEventImageCompression))
	event := events[0]
	if at+1 >= len(kinds) || kinds[at+1] != string(contract.RequestEventRouted) ||
		event.Status != contract.RequestStatusSucceeded || event.EndedAt == nil || event.EndedAt.Before(event.StartedAt) ||
		event.EndedAt.After(record.Events[at+1].StartedAt) || event.AttemptIndex != record.Events[at+1].AttemptIndex {
		t.Fatalf("image_compression is not the closed phase before routing: %v %+v", kinds, event)
	}
	return event.Summary
}

func TestImageCompressionSummary(t *testing.T) {
	for _, test := range []struct {
		result imageCompressionResult
		want   string
	}{
		{imageCompressionResult{mode: contract.ImageCompressionLossless, quality: 85}, ""},
		{imageCompressionResult{mode: contract.ImageCompressionLossless, quality: 85, images: 2, compressed: 1, before: 75301, after: 37406},
			"lossless · 1/2 images · 75301 → 37406 bytes"},
		{imageCompressionResult{mode: contract.ImageCompressionLossy, quality: 70, images: 1, before: 900, after: 900},
			"lossy q70 · 0/1 images · 900 → 900 bytes"},
	} {
		if got := test.result.summary(); got != test.want {
			t.Errorf("summary(%+v) = %q, want %q", test.result, got, test.want)
		}
	}
}

func TestImageCompressionEndToEnd(t *testing.T) {
	pngURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(testPNG(t, testScreenshot(320, 200)))
	jpegURL := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(testJPEG(t))
	body := `{"model":"model-test","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"` + pngURL +
		`"}},{"type":"image_url","image_url":{"url":"` + jpegURL + `"}}]}]}`
	compressed := func(mode contract.ImageCompressionMode) contract.RoutingSettings {
		settings := contract.DefaultRoutingSettings()
		settings.ImageCompression = contract.ImageCompression{Enabled: true, Mode: mode, Quality: 80}
		return settings
	}

	got, record := serveReasoning(t, contract.DefaultRoutingSettings(), contract.ProtocolOpenAIChat, []string{"model-test"}, "/v1/chat/completions", body, nil)
	if got != body || len(imageEvents(record)) != 0 {
		t.Fatalf("off by default, yet the body changed or an event was recorded: %+v", imageEvents(record))
	}

	got, record = serveReasoning(t, compressed(contract.ImageCompressionLossless), contract.ProtocolOpenAIChat, []string{"model-test"}, "/v1/chat/completions", body, nil)
	if strings.Count(got, "data:image/webp;base64,") != 1 || !strings.Contains(got, jpegURL) || len(got) >= len(body) {
		t.Fatalf("lossless upstream body = %.300s", got)
	}
	if summary := imageSummary(t, record); !strings.HasPrefix(summary, "lossless · 1/2 images · ") {
		t.Fatalf("summary = %q", summary)
	}

	got, record = serveReasoning(t, compressed(contract.ImageCompressionLossy), contract.ProtocolOpenAIChat, []string{"model-test"}, "/v1/chat/completions", body, nil)
	if strings.Count(got, "data:image/webp;base64,") != 2 || strings.Contains(got, "image/jpeg") || strings.Contains(got, "image/png") {
		t.Fatalf("lossy upstream body = %.300s", got)
	}
	if summary := imageSummary(t, record); !strings.HasPrefix(summary, "lossy q80 · 2/2 images · ") {
		t.Fatalf("summary = %q", summary)
	}

	text := `{"model":"model-test","messages":[{"role":"user","content":"hello"}]}`
	if _, record = serveReasoning(t, compressed(contract.ImageCompressionLossless), contract.ProtocolOpenAIChat, []string{"model-test"}, "/v1/chat/completions", text, nil); len(imageEvents(record)) != 0 {
		t.Fatalf("a request without images recorded %+v", imageEvents(record))
	}
}

func TestImageCompressionSendsTheSameBytesOnEveryAttempt(t *testing.T) {
	pngURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(testPNG(t, testScreenshot(320, 200)))
	body := `{"model":"model-test","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"` + pngURL + `"}}]}]}`
	_, _, response := nativeProviderExchange(contract.ProtocolOpenAIChat, false)
	first := newSequenceUpstream(t, "application/json", response, http.StatusInternalServerError, http.StatusInternalServerError)
	second := newSequenceUpstream(t, "application/json", response)
	candidates := []endpoint.Resolved{
		apiCandidate("service_a", contract.ServiceKindMagpie, first.url, contract.ProtocolOpenAIChat),
		apiCandidate("service_b", contract.ServiceKindMagpie, second.url, contract.ProtocolOpenAIChat),
	}
	candidates[0].Failover = &contract.FailoverPolicy{Enabled: true, Strategy: contract.RetryFirst, MaxAttempts: 6}
	settings := contract.DefaultRoutingSettings()
	settings.ImageCompression.Enabled = true
	got, store := serveRewrite(t, settings, candidates, "/v1/chat/completions", body, nil)
	if got.Code != http.StatusOK {
		t.Fatalf("response = %d %s", got.Code, got.Body.String())
	}
	attempts := append(first.requests(), second.requests()...)
	if len(first.requests()) < 2 || len(second.requests()) != 1 {
		t.Fatalf("expected a retry and a failover, got %d and %d attempts", len(first.requests()), len(second.requests()))
	}
	for index, attempt := range attempts {
		if attempt != attempts[0] || !strings.Contains(attempt, "data:image/webp;base64,") {
			t.Fatalf("attempt %d differs from the first or kept the PNG", index)
		}
	}
	// Every attempt record, the failed children too, shows its own compression.
	records := store.snapshot()
	if len(records) != len(attempts) {
		t.Fatalf("records = %d, attempts = %d", len(records), len(attempts))
	}
	for _, record := range records {
		if summary := imageSummary(t, record); !strings.HasPrefix(summary, "lossless · 1/1 images · ") {
			t.Fatalf("%s summary = %q", record.ID, summary)
		}
	}
}

func TestGeneratedWebPStillConvertsToPNG(t *testing.T) {
	source := testScreenshot(64, 48)
	var encoded bytes.Buffer
	if err := moewebp.Encode(&encoded, source); err != nil {
		t.Fatal(err)
	}
	converted, err := pngImage("data:image/webp;base64," + base64.StdEncoding.EncodeToString(encoded.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := base64.StdEncoding.DecodeString(converted)
	decoded, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	pixels := image.NewNRGBA(decoded.Bounds())
	draw.Draw(pixels, pixels.Bounds(), decoded, image.Point{}, draw.Src)
	if !bytes.Equal(pixels.Pix, source.Pix) {
		t.Fatal("WebP result changed on its way to PNG")
	}
}
