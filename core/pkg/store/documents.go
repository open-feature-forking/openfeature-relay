package store

import (
	"fmt"
	"slices"
	"sort"

	"github.com/hashicorp/go-memdb"
)

const documentsTable = "documents"

// Document is the parsed, validated flags of one flag set from one source, owned by the
// configuration format that accepted them.
type Document struct {
	Format  string
	Source  string
	FlagSet string // "" is the unnamed flag set
	Version string
	// FlagKeys are the keys of the flags the source delivered for this flag set, sorted.
	FlagKeys []string
	// Value is opaque to the store; the owning format reads it back on the next delivery.
	Value any
}

// documentIndexer indexes documents by (format, source, flag set); the flag set may be empty.
type documentIndexer struct{}

func documentKey(args ...string) []byte {
	var key []byte
	for _, a := range args {
		key = append(key, a...)
		key = append(key, 0)
	}
	return key
}

func (documentIndexer) FromObject(raw any) (bool, []byte, error) {
	d, ok := raw.(Document)
	if !ok {
		return false, nil, fmt.Errorf("unexpected type %T in documents table", raw)
	}
	if d.Format == "" || d.Source == "" {
		return false, nil, fmt.Errorf("document needs a format and a source")
	}
	return true, documentKey(d.Format, d.Source, d.FlagSet), nil
}

func (documentIndexer) FromArgs(args ...any) ([]byte, error) {
	strs, err := stringArgs(args, 3)
	if err != nil {
		return nil, err
	}
	return documentKey(strs...), nil
}

func (documentIndexer) PrefixFromArgs(args ...any) ([]byte, error) {
	strs, err := stringArgs(args, 2)
	if err != nil {
		return nil, err
	}
	return documentKey(strs...), nil
}

func stringArgs(args []any, n int) ([]string, error) {
	if len(args) != n {
		return nil, fmt.Errorf("want %d arguments, got %d", n, len(args))
	}
	out := make([]string, n)
	for i, a := range args {
		str, ok := a.(string)
		if !ok {
			return nil, fmt.Errorf("argument %d is %T, not a string", i, a)
		}
		out[i] = str
	}
	return out, nil
}

// Documents returns the documents a format holds for a source, ordered by flag set.
func (s *Store) Documents(format, source string) []Document {
	txn := s.db.Txn(false)
	it, err := txn.Get(documentsTable, idIndex+"_prefix", format, source)
	if err != nil {
		s.logger.Error(fmt.Sprintf("unable to query documents of format %s and source %s: %v", format, source, err))
		return nil
	}
	return collectDocuments(it)
}

func collectDocuments(it memdb.ResultIterator) []Document {
	var docs []Document
	for raw := it.Next(); raw != nil; raw = it.Next() {
		d := raw.(Document)
		d.FlagKeys = slices.Clone(d.FlagKeys)
		docs = append(docs, d)
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].FlagSet < docs[j].FlagSet })
	return docs
}

// applyDocuments writes req.Documents into txn. A full delivery drops the source's documents of
// the format that it does not name again; an incremental one replaces only the named flag sets.
func applyDocuments(txn *memdb.Txn, req ApplyRequest) error {
	if req.Format == "" {
		if len(req.Documents) > 0 {
			return fmt.Errorf("documents need a format")
		}
		return nil
	}

	named := make(map[string]struct{}, len(req.Documents))
	for _, d := range req.Documents {
		if d.Format != req.Format || d.Source != req.Source {
			return fmt.Errorf("document of format %q and source %q in a delivery of format %q and source %q",
				d.Format, d.Source, req.Format, req.Source)
		}
		named[d.FlagSet] = struct{}{}
	}

	if !req.Incremental {
		it, err := txn.Get(documentsTable, idIndex+"_prefix", req.Format, req.Source)
		if err != nil {
			return fmt.Errorf("unable to query documents: %w", err)
		}
		for _, old := range collectDocuments(it) {
			if _, ok := named[old.FlagSet]; ok {
				continue
			}
			if err := txn.Delete(documentsTable, old); err != nil {
				return fmt.Errorf("unable to delete document: %w", err)
			}
		}
	}

	for _, d := range req.Documents {
		d.FlagKeys = slices.Clone(d.FlagKeys)
		if err := txn.Insert(documentsTable, d); err != nil {
			return fmt.Errorf("unable to store document: %w", err)
		}
	}
	return nil
}
