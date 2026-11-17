package core

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOnlyTaskFilesOfTheRunningGatewayCountAsItsFiles(t *testing.T) {
	ready := Ready{InferenceURL: "http://127.0.0.1:8324", ClientInferenceURL: "http://localhost:8324", ControlURL: "http://127.0.0.1:8325"}
	file := "/v1/tasks/resp_1/artifacts/image-1/content?access=abc"
	for _, link := range []string{"http://127.0.0.1:8324" + file, "http://localhost:8324" + file} {
		if _, ok := gatewayFileURL(ready, link); !ok {
			t.Errorf("%s is not a gateway file", link)
		}
	}
	for _, link := range []string{
		"http://127.0.0.1:9999" + file,
		"http://127.0.0.1:8325" + file,
		"https://example.com" + file,
		"http://user:secret@127.0.0.1:8324" + file,
		"http://127.0.0.1:8324/v1/chat/completions",
		"http://127.0.0.1:8324/v1/tasks/resp_1",
	} {
		if _, ok := gatewayFileURL(ready, link); ok {
			t.Errorf("%s counted as a gateway file", link)
		}
	}

	m, _ := newTestManager(t)
	if _, ok := m.GatewayFileURL("http://127.0.0.1:8317" + file); ok {
		t.Error("a file counted while Core is not ready")
	}
	m.setReadyForTest(1, mustReady(t, "http://127.0.0.1:43210"))
	if _, ok := m.GatewayFileURL("http://127.0.0.1:8317" + file); !ok {
		t.Error("a file of the ready gateway did not count")
	}
}

// TestAGatewayFileSavesAsItStreamsAndABrokenOneLeavesNothing serves a whole
// answer, then one that promises more than it sends.
func TestAGatewayFileSavesAsItStreamsAndABrokenOneLeavesNothing(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	authorized := make(chan bool, 2)
	go func() {
		for _, answer := range []struct {
			body   string
			length int
		}{{"image bytes", 11}, {"half a vid", 100}} {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			request, err := http.ReadRequest(bufio.NewReader(conn))
			if err == nil {
				authorized <- request.Header.Get("Authorization") != ""
			}
			fmt.Fprintf(conn, "HTTP/1.1 200 OK\r\nContent-Type: image/png\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", answer.length, answer.body)
			_ = conn.Close()
		}
	}()
	ready := Ready{InferenceURL: "http://" + listener.Addr().String()}
	link, ok := gatewayFileURL(ready, ready.InferenceURL+"/v1/tasks/resp_1/artifacts/image-1/content?access=abc")
	if !ok {
		t.Fatal("the test link is not a gateway file")
	}
	directory := t.TempDir()
	image := filepath.Join(directory, "image-1.png")
	if err := SaveGatewayFile(t.Context(), link, image); err != nil {
		t.Fatal(err)
	}
	if saved, _ := os.ReadFile(image); string(saved) != "image bytes" {
		t.Errorf("saved %q", saved)
	}
	video := filepath.Join(directory, "video.mp4")
	if err := SaveGatewayFile(t.Context(), link, video); err == nil || !strings.Contains(err.Error(), io.ErrUnexpectedEOF.Error()) {
		t.Errorf("a broken download = %v", err)
	}
	if _, err := os.Stat(video); !os.IsNotExist(err) {
		t.Errorf("a broken download left %s: %v", video, err)
	}
	// The link carries its own access: the control token stays home.
	for range 2 {
		if <-authorized {
			t.Error("a gateway file was read with an Authorization header")
		}
	}
}
