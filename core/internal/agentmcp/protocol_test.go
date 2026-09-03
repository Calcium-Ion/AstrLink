package agentmcp

import (
	"bufio"
	"bytes"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestWriteMCPMessageIsNDJSON(t *testing.T) {
	var buffer bytes.Buffer
	if err := writeMCPMessage(&buffer, []byte(`{"jsonrpc":"2.0","id":1}`)); err != nil {
		t.Fatal(err)
	}
	got := buffer.String()
	if strings.Contains(got, "Content-Length") {
		t.Fatalf("stdout must not use Content-Length framing: %q", got)
	}
	if got != "{\"jsonrpc\":\"2.0\",\"id\":1}\n" {
		t.Fatalf("got %q", got)
	}
}

func TestReadMCPMessageAcceptsNDJSONWithoutWaitingForBlankLine(t *testing.T) {
	reader, writer := io.Pipe()
	done := make(chan struct{})
	var payload []byte
	var err error
	go func() {
		defer close(done)
		payload, err = readMCPMessage(bufio.NewReader(reader))
	}()
	if _, writeErr := writer.Write([]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize"}` + "\n")); writeErr != nil {
		t.Fatal(writeErr)
	}
	if closeErr := writer.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("readMCPMessage blocked waiting for a Content-Length blank line")
	}
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != `{"jsonrpc":"2.0","id":1,"method":"initialize"}` {
		t.Fatalf("payload = %s", payload)
	}
}

func TestReadMCPMessageAcceptsContentLength(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":2,"method":"ping"}`
	framed := "Content-Length: " + strconv.Itoa(len(body)) + "\r\n\r\n" + body
	payload, err := readMCPMessage(bufio.NewReader(strings.NewReader(framed)))
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != body {
		t.Fatalf("payload = %s", payload)
	}
}

func TestReadMCPMessageSkipsBlankLinesThenReadsNDJSON(t *testing.T) {
	payload, err := readMCPMessage(bufio.NewReader(strings.NewReader("\n\n{\"id\":3}\n")))
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != `{"id":3}` {
		t.Fatalf("payload = %s", payload)
	}
}

