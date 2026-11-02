// Package flagdtest pins which flag configurations are accepted and which are rejected,
// together with the rows they leave in the store. Production code must not import it.
package flagdtest

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/open-feature/flagd/core/pkg/logger"
	"github.com/open-feature/flagd/core/pkg/model"
	"github.com/open-feature/flagd/core/pkg/store"
	"github.com/open-feature/flagd/core/pkg/sync"
)

// UnnamedSet stands for the per-process flag set id of flags without a flagSetId.
const UnnamedSet = "<unnamed>"

// Row is one flag row as it sits in the store.
type Row struct {
	Key            string
	Source         string
	FlagSetID      string
	Priority       int
	State          string
	DefaultVariant string
	Variants       string
	Targeting      string
	Metadata       string
}

// Case applies Deliveries in order. Every delivery but the last must be accepted; WantErr is the
// exact error text of the last one, empty if it is accepted. Where flagd wraps a message of the Go
// JSON decoder, which differs between Go versions, WantErrPrefix names flagd's part of the text.
type Case struct {
	Name          string
	Sources       []string // priority order, highest first
	Deliveries    []sync.DataSync
	WantErr       string
	WantErrPrefix string
	WantRows      []Row
}

// Run applies every case to a fresh store and compares error text and resulting rows.
func Run(t *testing.T, newStore func(sources []string) store.IStore, apply func(store.IStore, sync.DataSync) error) {
	t.Helper()
	unnamed := unnamedSetID()

	for _, tc := range Cases {
		t.Run(tc.Name, func(t *testing.T) {
			s := newStore(tc.Sources)
			for i, d := range tc.Deliveries {
				err := apply(s, d)
				last := i == len(tc.Deliveries)-1
				switch {
				case !last && err != nil:
					t.Fatalf("delivery %d must be accepted, got error: %v", i, err)
				case last && tc.WantErr == "" && tc.WantErrPrefix == "" && err != nil:
					t.Fatalf("want accepted, got error: %v", err)
				case last && tc.WantErr != "" && (err == nil || err.Error() != tc.WantErr):
					t.Fatalf("want error %q, got %v", tc.WantErr, err)
				case last && tc.WantErrPrefix != "" && (err == nil || !strings.HasPrefix(err.Error(), tc.WantErrPrefix)):
					t.Fatalf("want error starting with %q, got %v", tc.WantErrPrefix, err)
				}
			}

			got := rows(t, s, store.Selector{}, unnamed)
			if fmt.Sprint(got) != fmt.Sprint(tc.WantRows) {
				t.Fatalf("rows differ\nwant: %s\n got: %s", format(tc.WantRows), format(got))
			}
			for _, src := range tc.Sources {
				var want []Row
				for _, r := range tc.WantRows {
					if r.Source == src {
						want = append(want, r)
					}
				}
				bySource := rows(t, s, store.Selector{}.WithSource(src), unnamed)
				if fmt.Sprint(bySource) != fmt.Sprint(want) {
					t.Fatalf("rows of source %q differ\nwant: %s\n got: %s", src, format(want), format(bySource))
				}
			}
		})
	}
}

// unnamedSetID asks a scratch store for the id it gives to flags without a flagSetId.
func unnamedSetID() string {
	probe, err := store.NewStore(logger.NewLogger(nil, false), []string{"probe"})
	if err != nil {
		panic(err)
	}
	probe.Update("probe", []model.Flag{{Key: "probe"}}, nil, false)
	flags, _, err := probe.GetAll(context.Background(), &store.Selector{})
	if err != nil || len(flags) != 1 {
		panic(fmt.Sprintf("unable to probe the unnamed flag set id: %v", err))
	}
	return flags[0].FlagSetId
}

func rows(t *testing.T, s store.IStore, sel store.Selector, unnamed string) []Row {
	t.Helper()
	flags, _, err := s.GetAll(context.Background(), &sel)
	if err != nil {
		t.Fatalf("GetAll: %v", err)
	}
	var out []Row
	for _, f := range flags {
		set := f.FlagSetId
		if set == unnamed {
			set = UnnamedSet
		}
		out = append(out, Row{
			Key: f.Key, Source: f.Source, FlagSetID: set, Priority: f.Priority, State: f.State,
			DefaultVariant: f.DefaultVariant, Variants: mustJSON(f.Variants), Targeting: string(f.Targeting),
			Metadata: mustJSON(f.Metadata),
		})
	}
	return out
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func format(rs []Row) string {
	var b strings.Builder
	for _, r := range rs {
		fmt.Fprintf(&b, "\n  %+v", r)
	}
	return b.String()
}

// NewStore is the store every characterization uses.
func NewStore(sources []string) store.IStore {
	s, err := store.NewStore(logger.NewLogger(nil, false), sources)
	if err != nil {
		panic(err)
	}
	return s
}
