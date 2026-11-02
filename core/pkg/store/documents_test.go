package store

import (
	"context"
	"reflect"
	"testing"

	"github.com/open-feature/flagd/core/pkg/logger"
	"github.com/open-feature/flagd/core/pkg/model"
)

func newDocumentsStore(t *testing.T, sources ...string) *Store {
	t.Helper()
	s, err := NewStore(logger.NewLogger(nil, false), sources)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func doc(format, source, set, version string, keys ...string) Document {
	return Document{Format: format, Source: source, FlagSet: set, Version: version, FlagKeys: keys}
}

func flagKeys(t *testing.T, s *Store, source string) []string {
	t.Helper()
	sel := Selector{}.WithSource(source)
	flags, _, err := s.GetAll(context.Background(), &sel)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, f := range flags {
		keys = append(keys, f.FlagSetId+"/"+f.Key)
	}
	return keys
}

func TestFlagSetID(t *testing.T) {
	tests := []struct {
		name string
		flag model.Flag
		set  model.Metadata
		want string
	}{
		{"unnamed", model.Flag{}, nil, ""},
		{"set", model.Flag{}, model.Metadata{"flagSetId": "s1"}, "s1"},
		{"flag overrides set", model.Flag{Metadata: model.Metadata{"flagSetId": "s2"}}, model.Metadata{"flagSetId": "s1"}, "s2"},
		{"not a string", model.Flag{}, model.Metadata{"flagSetId": 5}, ""},
		{"flag level not a string does not fall back to the set", model.Flag{Metadata: model.Metadata{"flagSetId": 5}}, model.Metadata{"flagSetId": "s1"}, ""},
		{"flag level empty does not fall back to the set", model.Flag{Metadata: model.Metadata{"flagSetId": ""}}, model.Metadata{"flagSetId": "s1"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FlagSetID(tt.flag, tt.set); got != tt.want {
				t.Fatalf("want %q, got %q", tt.want, got)
			}
		})
	}
}

func TestApplyReplacesOnlySource(t *testing.T) {
	s := newDocumentsStore(t, "a", "b")
	for _, src := range []string{"a", "b"} {
		err := s.Apply(ApplyRequest{
			Format: "fmt", Source: src,
			Documents: []Document{doc("fmt", src, "", "v1", "k"), doc("fmt", src, "s1", "v1", "k")},
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	err := s.Apply(ApplyRequest{Format: "fmt", Source: "a", Documents: []Document{doc("fmt", "a", "s1", "v2", "k", "l")}})
	if err != nil {
		t.Fatal(err)
	}

	got := s.Documents("fmt", "a")
	want := []Document{doc("fmt", "a", "s1", "v2", "k", "l")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("source a: want %v, got %v", want, got)
	}
	if n := len(s.Documents("fmt", "b")); n != 2 {
		t.Fatalf("source b must keep its 2 documents, has %d", n)
	}
}

func TestApplyIncrementalReplacesOnlyNamedFlagSets(t *testing.T) {
	s := newDocumentsStore(t, "a")
	err := s.Apply(ApplyRequest{Format: "fmt", Source: "a", Documents: []Document{
		doc("fmt", "a", "s1", "v1", "k"), doc("fmt", "a", "s2", "v1", "k"),
	}})
	if err != nil {
		t.Fatal(err)
	}

	err = s.Apply(ApplyRequest{Format: "fmt", Source: "a", Incremental: true, Documents: []Document{doc("fmt", "a", "s1", "v2", "k")}})
	if err != nil {
		t.Fatal(err)
	}

	want := []Document{doc("fmt", "a", "s1", "v2", "k"), doc("fmt", "a", "s2", "v1", "k")}
	if got := s.Documents("fmt", "a"); !reflect.DeepEqual(got, want) {
		t.Fatalf("want %v, got %v", want, got)
	}
}

func TestApplyIsAtomic(t *testing.T) {
	s := newDocumentsStore(t, "a")
	flag := model.Flag{Key: "f1", State: "ENABLED", DefaultVariant: "on", Variants: map[string]any{"on": true}}
	if err := s.Apply(ApplyRequest{Source: "a", Flags: []model.Flag{flag}}); err != nil {
		t.Fatal(err)
	}

	other := flag
	other.Key = "f2"
	err := s.Apply(ApplyRequest{
		Format: "fmt", Source: "a", Flags: []model.Flag{other},
		Documents: []Document{doc("fmt", "elsewhere", "", "v1", "f2")},
	})
	if err == nil {
		t.Fatal("a document of another source must be refused")
	}

	if got := flagKeys(t, s, "a"); !reflect.DeepEqual(got, []string{nilFlagSetId + "/f1"}) {
		t.Fatalf("flags changed by a refused delivery: %v", got)
	}
	if n := len(s.Documents("fmt", "a")); n != 0 {
		t.Fatalf("documents changed by a refused delivery: %d", n)
	}
}

func TestApplyWithoutFlagsLeavesFlagsTable(t *testing.T) {
	s := newDocumentsStore(t, "a")
	flag := model.Flag{Key: "f1", State: "ENABLED", DefaultVariant: "on", Variants: map[string]any{"on": true}}
	if err := s.Apply(ApplyRequest{Source: "a", Flags: []model.Flag{flag}}); err != nil {
		t.Fatal(err)
	}

	if err := s.Apply(ApplyRequest{Format: "fmt", Source: "a", Documents: []Document{doc("fmt", "a", "", "v1", "x")}}); err != nil {
		t.Fatal(err)
	}

	if got := flagKeys(t, s, "a"); !reflect.DeepEqual(got, []string{nilFlagSetId + "/f1"}) {
		t.Fatalf("flags table changed by a delivery without flags: %v", got)
	}
	if n := len(s.Documents("fmt", "a")); n != 1 {
		t.Fatalf("want 1 document, got %d", n)
	}
}
