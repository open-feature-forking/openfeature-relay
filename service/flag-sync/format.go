package sync

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	msync "sync"

	syncv1 "buf.build/gen/go/open-feature/flagd/protocolbuffers/go/flagd/sync/v1"
	"connectrpc.com/connect"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/configformat"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	"google.golang.org/protobuf/types/known/structpb"
)

// FormatHeader names the configuration format a flag sync request reads. A request without it, or naming flagd's own
// format, takes the flagd path; another configuration format of the distribution is served as one document, unchanged.
const FormatHeader = "Flagd-Configuration-Format"

// documentStore is what the endpoint needs of the store to serve the documents of a configuration format; the
// store of a runtime has it.
type documentStore interface {
	Sources() []string
	Documents(format, source string) []store.Document
}

// requestedFormat returns the configuration format a request names, or "" for flagd's own. A format the distribution
// lacks is an error that names the formats it has.
func requestedFormat(header http.Header, formats []string) (string, error) {
	value := header.Get(FormatHeader)
	if value == "" || value == configformat.DefaultType {
		return "", nil
	}
	if !slices.Contains(formats, value) {
		return "", fetchError{kind: fetchFormatUnknown, cause: fmt.Errorf(
			"unknown configuration format, this distribution has: %s", strings.Join(formats, ", "))}
	}
	return value, nil
}

// formatDocument resolves the one document of the format that the selector names: the document of a source, of a flag
// set, or the only one there is. It returns the document and its text.
func formatDocument(s store.IStore, format string, selector *store.Selector) (store.Document, string, error) {
	docs, ok := s.(documentStore)
	if !ok {
		return store.Document{}, "", fetchError{kind: fetchStoreRead, cause: fmt.Errorf("the store holds no documents")}
	}

	var matches []store.Document
	for _, source := range docs.Sources() {
		if want := selector.Source(); want != "" && want != source {
			continue
		}
		for _, d := range docs.Documents(format, source) {
			if id, named := selector.DocumentFlagSet(); named && d.FlagSet != id {
				continue
			}
			matches = append(matches, d)
		}
	}

	switch len(matches) {
	case 0:
		return store.Document{}, "", fetchError{kind: fetchDocumentMissing, cause: fmt.Errorf("no %s document matches", format)}
	case 1:
	default:
		names := make([]string, len(matches))
		for i, d := range matches {
			names[i] = fmt.Sprintf("source %q flag set %q", d.Source, d.FlagSet)
		}
		return store.Document{}, "", fetchError{kind: fetchDocumentAmbiguous, cause: fmt.Errorf(
			"%d %s documents match, select one with source= or flagSetId=: %s", len(matches), format, strings.Join(names, "; "))}
	}

	text, ok := store.DocumentText(matches[0])
	if !ok {
		return store.Document{}, "", fetchError{kind: fetchDocumentNoText, cause: fmt.Errorf(
			"the %s configuration format keeps its documents in a form flag sync cannot send: "+
				"their values do not implement store.TextDocument", format)}
	}
	return matches[0], text, nil
}

// changeSignal wakes the streams that wait for a configuration change.
type changeSignal struct {
	mu msync.Mutex
	ch chan struct{}
}

func newChangeSignal() *changeSignal { return &changeSignal{ch: make(chan struct{})} }

// wait returns a channel that is closed at the next change. Without a signal it never is.
func (c *changeSignal) wait() <-chan struct{} {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ch
}

func (c *changeSignal) broadcast() {
	c.mu.Lock()
	defer c.mu.Unlock()
	close(c.ch)
	c.ch = make(chan struct{})
}

// syncFormat streams the one document of a configuration format other than flagd's: it sends the document once it
// exists and again whenever its version changes. A change that leaves the version alone sends nothing.
func (s syncHandler) syncFormat(
	ctx context.Context, format string, selector *store.Selector, syncContext *structpb.Struct,
	stream syncFlagsSender, exitReason *string,
) error {
	var sentVersion string
	sent := false
	for {
		changed := s.changes.wait() // taken before the read, so that a change in between is not lost
		doc, text, err := formatDocument(s.store, format, selector)
		if fetchErr, ok := errors.AsType[fetchError](err); ok && fetchErr.kind == fetchDocumentMissing {
			err = nil // the stream waits for the document to appear
		} else if err == nil && (!sent || doc.Version != sentVersion) {
			if err := stream.Send(&syncv1.SyncFlagsResponse{FlagConfiguration: text, SyncContext: syncContext}); err != nil {
				s.log.Debug(fmt.Sprintf("error sending stream response: %v", err))
				*exitReason = "client_disconnect"
				return fmt.Errorf("error sending stream response: %w", err)
			}
			sent, sentVersion = true, doc.Version
		}
		if err != nil {
			*exitReason = "error"
			return s.connectError(err)
		}

		select {
		case <-changed:
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				s.log.Debug(fmt.Sprintf("server-side deadline of %s exceeded, exiting stream request with grpc error code 4", s.deadline.String()))
				*exitReason = "deadline_exceeded"
				return connect.NewError(connect.CodeDeadlineExceeded, errors.New("stream closed due to server-side timeout"))
			}
			s.log.Debug("context complete and exiting stream request")
			*exitReason = "normal_close"
			return nil
		}
	}
}
