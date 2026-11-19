package pluginhost

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"strings"
)

// An AWS event stream (application/vnd.amazon.eventstream) is a run of
// binary messages: a prelude of the total and header lengths with its
// CRC, the headers, the payload, and the CRC of the whole message. A
// converted route hands each to convertStreamEvent as {event, data}.

// maxAWSMessageBytes bounds one message of an AWS event stream.
const maxAWSMessageBytes = 16 << 20

// readAWSEvents calls each with the messages of an AWS event stream until
// it says done or the stream ends; a bad CRC or a truncated message fails
// the stream.
func readAWSEvents(body io.Reader, each func(map[string]any) (bool, error)) error {
	prelude := make([]byte, 12)
	for {
		if _, err := io.ReadFull(body, prelude); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return errors.New("the event stream ends inside a message")
		}
		total := binary.BigEndian.Uint32(prelude[0:4])
		headersLength := binary.BigEndian.Uint32(prelude[4:8])
		if crc32.ChecksumIEEE(prelude[:8]) != binary.BigEndian.Uint32(prelude[8:12]) {
			return errors.New("the event stream has a message with a bad prelude CRC")
		}
		if total < 16 || total > maxAWSMessageBytes || headersLength > total-16 {
			return fmt.Errorf("the event stream has a message of %d bytes with %d of headers", total, headersLength)
		}
		message := make([]byte, total)
		copy(message, prelude)
		if _, err := io.ReadFull(body, message[12:]); err != nil {
			return errors.New("the event stream ends inside a message")
		}
		if crc32.ChecksumIEEE(message[:total-4]) != binary.BigEndian.Uint32(message[total-4:]) {
			return errors.New("the event stream has a message with a bad CRC")
		}
		headers, err := awsHeaders(message[12 : 12+headersLength])
		if err != nil {
			return err
		}
		payload := message[12+headersLength : total-4]
		event := map[string]any{"data": strings.ToValidUTF8(string(payload), "�")}
		for _, name := range []string{":event-type", ":exception-type", ":error-code"} {
			if value, ok := headers[name]; ok {
				event["event"] = value
				break
			}
		}
		if len(payload) == 0 {
			if message, ok := headers[":error-message"]; ok {
				event["data"] = message
			}
		}
		if done, err := each(event); err != nil || done {
			return err
		}
	}
}

// awsHeaders reads the headers of a message; the string and bytes ones
// come as text, the others are skipped.
func awsHeaders(data []byte) (map[string]string, error) {
	headers := map[string]string{}
	bad := errors.New("the event stream has a message with bad headers")
	for len(data) > 0 {
		nameLength := int(data[0])
		if len(data) < 1+nameLength+1 {
			return nil, bad
		}
		name := string(data[1 : 1+nameLength])
		kind := data[1+nameLength]
		data = data[2+nameLength:]
		size := 0
		switch kind {
		case 0, 1:
		case 2:
			size = 1
		case 3:
			size = 2
		case 4:
			size = 4
		case 5, 8:
			size = 8
		case 9:
			size = 16
		case 6, 7:
			if len(data) < 2 {
				return nil, bad
			}
			length := int(binary.BigEndian.Uint16(data))
			if len(data) < 2+length {
				return nil, bad
			}
			if kind == 7 {
				headers[name] = string(data[2 : 2+length])
			}
			data = data[2+length:]
			continue
		default:
			return nil, bad
		}
		if len(data) < size {
			return nil, bad
		}
		data = data[size:]
	}
	return headers, nil
}
