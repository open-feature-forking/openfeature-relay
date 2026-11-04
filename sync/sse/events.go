package sse

import (
	"bufio"
	"bytes"
	"io"
	"strings"
)

// maxLine is the longest line of a stream the trigger reads; a longer one ends the connection.
const maxLine = 1 << 20

// event is one event the stream dispatched.
type event struct {
	// Type is the event field, "message" when the event names none.
	Type string
	// Data is the data fields, joined by a line feed.
	Data string
}

// readEvents reads server-sent events from r and calls dispatch for each event the stream dispatches, in order. It
// returns nil when r ends, and the read error otherwise. An event the stream ends in the middle of is dropped, as is an
// event without data: such an event is not dispatched, so a keepalive or a comment never reaches dispatch.
func readEvents(r io.Reader, dispatch func(event)) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 4096), maxLine)
	scanner.Split(splitLines)

	var (
		eventType string
		data      []string
		first     = true
	)
	for scanner.Scan() {
		line := scanner.Text()
		if first {
			line = strings.TrimPrefix(line, "\ufeff")
			first = false
		}
		if line == "" {
			if len(data) > 0 {
				name := eventType
				if name == "" {
					name = "message"
				}
				dispatch(event{Type: name, Data: strings.Join(data, "\n")})
			}
			eventType, data = "", nil
			continue
		}
		if line[0] == ':' {
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			eventType = value
		case "data":
			data = append(data, value)
		}
		// id and retry matter to a client that reconnects with Last-Event-ID or waits by the server's advice; the
		// trigger reconnects through the fetching sync provider's backoff and fetches in full, so it reads neither
	}
	return scanner.Err()
}

// splitLines splits a stream into lines, which end in a line feed, a carriage return, or both.
func splitLines(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	if i := bytes.IndexAny(data, "\r\n"); i >= 0 {
		if data[i] == '\r' {
			if i+1 == len(data) && !atEOF {
				// a line feed may follow
				return 0, nil, nil
			}
			if i+1 < len(data) && data[i+1] == '\n' {
				return i + 2, data[:i], nil
			}
		}
		return i + 1, data[:i], nil
	}
	if atEOF {
		return len(data), data, nil
	}
	return 0, nil, nil
}
