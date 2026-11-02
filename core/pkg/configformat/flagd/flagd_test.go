package flagd

import (
	"context"
	"errors"
	"reflect"
	"testing"

	flagconfigtest "github.com/open-feature/flagd/core/pkg/configformat/flagd/flagdtest"
	"github.com/open-feature/flagd/core/pkg/configformat"
	"github.com/open-feature/flagd/core/pkg/configformat/formattest"
	"github.com/open-feature/flagd/core/pkg/logger"
	"github.com/open-feature/flagd/core/pkg/store"
	"github.com/open-feature/flagd/core/pkg/sync"
)

const flagBody = `"state":"ENABLED","defaultVariant":"on","variants":{"on":true,"off":false}`

func TestFlagdFormatCharacterization(t *testing.T) {
	flagconfigtest.Run(t, flagconfigtest.NewStore, func(s store.IStore, d sync.DataSync) error {
		format := NewFormat(s.(*store.Store), logger.NewLogger(nil, false))
		_, err := format.Accept(context.Background(), configformat.Input{
			Source: d.Source, Data: d.FlagData, IncrementalUpdate: d.IncrementalUpdates,
		})
		var rejection configformat.Rejection
		if errors.As(err, &rejection) {
			return errors.New(rejection.Reason)
		}
		return err
	})
}

func TestFlagdFormatContract(t *testing.T) {
	formattest.Run(t, Factory{}, formattest.Samples{
		Valid: formattest.Delivery{
			Data: `{"metadata":{"flagSetId":"prod"},"flags":{"a":{` + flagBody + `},"b":{` + flagBody + `},"c":{` + flagBody + `}}}`,
			Sets: []formattest.Set{{FlagSet: "prod", Keys: []string{"a", "b", "c"}}},
		},
		Changed: formattest.Change{
			Delivery: formattest.Delivery{
				Data: `{"metadata":{"flagSetId":"prod"},"flags":{"a":{` + flagBody + `},` +
					`"b":{"state":"DISABLED","defaultVariant":"on","variants":{"on":true,"off":false}},"d":{` + flagBody + `}}}`,
				Sets: []formattest.Set{{FlagSet: "prod", Keys: []string{"a", "b", "d"}}},
			},
			FlagSet: "prod", Added: []string{"d"}, Updated: []string{"b"}, Removed: []string{"c"},
		},
		Invalid: formattest.Faulty{
			Data:    `{"metadata":{"flagSetId":"prod"},"flags":{"a":{"state":"ENABLED","defaultVariant":"nope","variants":{"on":true}}}}`,
			Entries: []string{"a"},
		},
		Incremental: &[2]formattest.Delivery{
			{Data: `{"metadata":{"flagSetId":"s1"},"flags":{"a":{` + flagBody + `}}}`, Sets: []formattest.Set{{FlagSet: "s1", Keys: []string{"a"}}}},
			{Data: `{"metadata":{"flagSetId":"s2"},"flags":{"a":{` + flagBody + `}}}`, Sets: []formattest.Set{{FlagSet: "s2", Keys: []string{"a"}}}},
		},
	})
}

func TestFlagdFormatReportsChanges(t *testing.T) {
	log := logger.NewLogger(nil, false)
	st, err := store.NewStore(log, []string{"src"})
	if err != nil {
		t.Fatal(err)
	}
	format := NewFormat(st, log)
	accept := func(data string) configformat.Result {
		t.Helper()
		res, err := format.Accept(context.Background(), configformat.Input{Source: "src", Data: data})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}

	first := accept(`{"flags":{"u1":{` + flagBody + `},"n1":{` + flagBody + `,"metadata":{"flagSetId":"named"}}}}`)
	var sets []string
	for _, d := range first.Documents {
		sets = append(sets, d.FlagSet)
	}
	if !reflect.DeepEqual(sets, []string{"", "named"}) {
		t.Fatalf("want documents for the unnamed and the named set, got %v", sets)
	}

	empty := accept(`{"metadata":{"flagSetId":"named"},"flags":{}}`)
	if len(empty.Documents) != 1 || empty.Documents[0].FlagSet != "named" || len(empty.Documents[0].FlagKeys) != 0 {
		t.Fatalf("a flag set without flags needs an empty document, got %+v", empty.Documents)
	}
	if got := st.Documents(Type, "src"); len(got) != 1 {
		t.Fatalf("a full delivery drops the documents of flag sets it doesn't name, got %d", len(got))
	}
}

func newTestFormat(t *testing.T, sources ...string) (*Format, *store.Store) {
	t.Helper()
	log := logger.NewLogger(nil, false)
	st, err := store.NewStore(log, sources)
	if err != nil {
		t.Fatal(err)
	}
	return NewFormat(st, log), st
}

func TestFlagdFormatSetMetadataChangeIsAnUpdate(t *testing.T) {
	format, st := newTestFormat(t, "src")
	accept := func(owner string) configformat.DocumentReport {
		t.Helper()
		res, err := format.Accept(context.Background(), configformat.Input{
			Source: "src",
			Data:   `{"metadata":{"flagSetId":"s","owner":"` + owner + `"},"flags":{"f":{` + flagBody + `}}}`,
		})
		if err != nil || len(res.Documents) != 1 {
			t.Fatalf("want one document, got %+v, %v", res.Documents, err)
		}
		return res.Documents[0]
	}

	before := accept("x")
	after := accept("y")

	if before.Version == after.Version {
		t.Fatalf("a changed set-level metadata must change the version, both are %s", before.Version)
	}
	if !reflect.DeepEqual(after.Updated, []string{"f"}) {
		t.Fatalf("flags whose metadata changed are updated, got %v", after.Updated)
	}
	flags, _, err := st.GetAll(context.Background(), &store.Selector{})
	if err != nil || len(flags) != 1 || flags[0].Metadata["owner"] != "y" {
		t.Fatalf("the store holds the new metadata, got %+v, %v", flags, err)
	}
}

func TestFlagdFormatIncrementalDocumentsAgreeWithFlags(t *testing.T) {
	format, st := newTestFormat(t, "src")
	accept := func(data string) {
		t.Helper()
		if _, err := format.Accept(context.Background(), configformat.Input{Source: "src", Data: data, IncrementalUpdate: true}); err != nil {
			t.Fatal(err)
		}
	}

	accept(`{"flags":{"u":{` + flagBody + `}}}`)
	// the set names no flag set, and its only flag moves to s2: the unnamed flag set is not part of this delivery
	accept(`{"metadata":{"flagSetId":""},"flags":{"g":{` + flagBody + `,"metadata":{"flagSetId":"s2"}}}}`)

	got := map[string][]string{}
	for _, d := range st.Documents(Type, "src") {
		got[d.FlagSet] = d.FlagKeys
	}
	want := map[string][]string{"": {"u"}, "s2": {"g"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("documents must agree with the flags table\nwant: %v\n got: %v", want, got)
	}
}
