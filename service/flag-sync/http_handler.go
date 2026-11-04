package sync

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	flagdService "github.com/open-feature-forking/openfeature-relay/service/transport"
	"go.uber.org/zap"
)

const (
	flagsPath = "/v1/flags"
	// only used by http.ServeContent to sniff the content type
	contentName = "flags.json"
)

// modTime records the last observed flag change. Global, since a per-selector table would be keyed
// by client input and grow unbounded.
type modTime struct {
	unix atomic.Int64
}

func (m *modTime) set(t time.Time) {
	m.unix.Store(t.Unix())
}

// get reports the zero time until the first snapshot lands, so ServeContent omits Last-Modified.
func (m *modTime) get() time.Time {
	sec := m.unix.Load()
	if sec == 0 {
		return time.Time{}
	}
	return time.Unix(sec, 0)
}

// httpHandler serves the flag configuration document, not the FetchAllFlags envelope around it, so
// flagd's own HTTP sync source can consume it.
type httpHandler struct {
	store   store.IStore
	log     *logger.Logger
	modTime *modTime
	formats []string
}

func (h httpHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Sampled before the read: a later stamp would strand the client on stale flags.
	lastModified := h.modTime.get()

	format, err := requestedFormat(r.Header, h.formats)
	if err != nil {
		h.writeError(w, err)
		return
	}
	if format != "" {
		h.serveFormat(w, r, format, lastModified)
		return
	}

	body, err := fetchAllFlags(r.Context(), h.store, resolveSelector(r.Header, ""))
	if err != nil {
		h.writeError(w, err)
		return
	}

	w.Header().Set("ETag", etagOf(body))
	w.Header().Set("Vary", flagdService.FLAGD_SELECTOR_HEADER)

	// ServeContent applies RFC 9110 precedence: If-None-Match wins when both validators are sent.
	http.ServeContent(w, r, contentName, lastModified, bytes.NewReader(body))
}

// serveFormat serves the one document of a configuration format other than flagd's, unchanged.
func (h httpHandler) serveFormat(w http.ResponseWriter, r *http.Request, format string, lastModified time.Time) {
	_, text, err := fetchFormatDocument(r.Context(), h.store, format, resolveSelector(r.Header, ""))
	if err != nil {
		h.writeError(w, err)
		return
	}
	body := []byte(text)

	w.Header().Set("ETag", etagOf(body))
	w.Header().Set("Vary", flagdService.FLAGD_SELECTOR_HEADER+", "+FormatHeader)
	http.ServeContent(w, r, contentName, lastModified, bytes.NewReader(body))
}

// writeError maps a fetch failure onto a status. No branch echoes the expression back: it is
// unescaped client input.
func (h httpHandler) writeError(w http.ResponseWriter, err error) {
	fetchErr, ok := errors.AsType[fetchError](err)
	if !ok {
		fetchErr = fetchError{kind: fetchStoreRead, cause: err}
	}

	switch fetchErr.kind {
	case fetchSelectorMalformed:
		http.Error(w, "malformed selector", http.StatusBadRequest)
	case fetchBoundSelectorInvalid:
		h.log.Error("invalid bound selector", zap.Error(fetchErr.cause))
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
	case fetchMarshal:
		h.log.Error("error marshalling flags", zap.Error(fetchErr.cause))
		http.Error(w, "error marshalling flags", http.StatusInternalServerError)
	case fetchFormatUnknown, fetchDocumentAmbiguous:
		http.Error(w, fetchErr.cause.Error(), http.StatusBadRequest)
	case fetchDocumentMissing:
		http.Error(w, fetchErr.cause.Error(), http.StatusNotFound)
	case fetchDocumentNoText:
		h.log.Error("document without a text", zap.Error(fetchErr.cause))
		http.Error(w, fetchErr.cause.Error(), http.StatusInternalServerError)
	default:
		h.log.Error("error retrieving flags from store", zap.Error(fetchErr.cause))
		http.Error(w, "error retrieving flags from store", http.StatusInternalServerError)
	}
}

// etagOf hashes the bytes served, which is stable because encoding/json sorts map keys.
func etagOf(body []byte) string {
	sum := sha256.Sum256(body)
	return `"` + hex.EncodeToString(sum[:]) + `"`
}
