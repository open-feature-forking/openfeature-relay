package store

import (
	"slices"

	"github.com/hashicorp/go-memdb"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/model"
)

// View is one consistent read of the store: every call sees the state as of the moment View was
// called, however many deliveries the store takes meanwhile. A View is cheap, holds no lock and is
// safe for concurrent reads; it is a value type so that taking one costs no allocation of its own.
type View struct {
	store *Store
	txn   *memdb.Txn
}

// View starts a consistent read of the flags and documents.
func (s *Store) View() View {
	return View{store: s, txn: s.db.Txn(false)}
}

// Get returns the highest-priority flag with the key, or the one the selector names.
func (v View) Get(key string, selector *Selector) (model.Flag, model.Metadata, error) {
	return v.store.get(v.txn, key, selector)
}

// GetAll returns the flags the selector names, one per flag set and key.
func (v View) GetAll(selector *Selector) ([]model.Flag, model.Metadata, error) {
	return v.store.getAll(v.txn, selector)
}

// Documents returns the documents a format holds for a source, ordered by flag set.
func (v View) Documents(format, source string) []Document {
	return v.store.documents(v.txn, format, source)
}

// Sources returns the registered sources in priority order: a later source has the higher priority.
func (s *Store) Sources() []string { return slices.Clone(s.sources) }
