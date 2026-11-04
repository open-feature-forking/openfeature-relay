package sse

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
)

// stream is a server-sent event stream the tests control: it can send raw text to every open connection, end the
// connections, answer another status, and it remembers the requests it got.
type stream struct {
	srv *httptest.Server

	mu    sync.Mutex
	conns map[*conn]struct{}

	connections atomic.Int32
	status      atomic.Int32 // the status to answer; zero means 200 with a stream
	contentType atomic.Value // string; empty means text/event-stream
	redirect    atomic.Value // string; a location to redirect to

	lastHeader atomic.Value // map[string][]string
	lastQuery  atomic.Value // string
}

type conn struct {
	out chan string
	end chan struct{}
}

func newStream(t *testing.T) *stream {
	t.Helper()
	s := &stream{conns: map[*conn]struct{}{}}
	s.srv = httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *stream) URL() string { return s.srv.URL + "/events?token=secret-in-the-query" }

func (s *stream) handle(w http.ResponseWriter, r *http.Request) {
	s.connections.Add(1)
	s.lastHeader.Store(map[string][]string(r.Header.Clone()))
	s.lastQuery.Store(r.URL.RawQuery)
	if loc, _ := s.redirect.Load().(string); loc != "" {
		http.Redirect(w, r, loc, http.StatusFound)
		return
	}
	if code := int(s.status.Load()); code != 0 {
		w.WriteHeader(code)
		return
	}
	contentType, _ := s.contentType.Load().(string)
	if contentType == "" {
		contentType = "text/event-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(http.StatusOK)
	flusher := w.(http.Flusher)
	flusher.Flush()

	c := &conn{out: make(chan string, 1024), end: make(chan struct{})}
	s.mu.Lock()
	s.conns[c] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.conns, c)
		s.mu.Unlock()
	}()
	for {
		select {
		case raw := <-c.out:
			if _, err := io.WriteString(w, raw); err != nil {
				return
			}
			flusher.Flush()
		case <-c.end:
			return
		case <-r.Context().Done():
			return
		}
	}
}

// send writes raw text to every open connection; with none open, it is lost, as an event is for a client that is not
// connected.
func (s *stream) send(raw string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for c := range s.conns {
		select {
		case c.out <- raw:
		default:
		}
	}
}

// event sends one dispatched event.
func (s *stream) event(t testing.TB) { t.Helper(); s.send("event: update\ndata: changed\n\n") }

// end closes every open connection from the server, cleanly.
func (s *stream) end(testing.TB) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for c := range s.conns {
		select {
		case <-c.end:
		default:
			close(c.end)
		}
	}
}

// drop closes the connections of the server abruptly, as a lost network does.
func (s *stream) drop(testing.TB) { s.srv.CloseClientConnections() }

func (s *stream) open() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.conns)
}
