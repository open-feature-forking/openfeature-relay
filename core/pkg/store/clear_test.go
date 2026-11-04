package store

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/model"
)

func flagsOf(keys ...string) []model.Flag {
	flags := make([]model.Flag, 0, len(keys))
	for _, k := range keys {
		flags = append(flags, model.Flag{Key: k, State: "ENABLED", DefaultVariant: "on", Variants: map[string]any{"on": true}})
	}
	return flags
}

func TestClearEmptiesFlagsAndDocuments(t *testing.T) {
	s := newDocumentsStore(t, "a", "b")
	for _, source := range []string{"a", "b"} {
		err := s.Apply(ApplyRequest{
			Format: "flagd", Source: source, Flags: flagsOf("f1", "f2"),
			Documents: []Document{doc("flagd", source, "", "v1", "f1", "f2")},
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	s.Clear()

	for _, source := range []string{"a", "b"} {
		if got := flagKeys(t, s, source); len(got) != 0 {
			t.Fatalf("source %s still has flags %v", source, got)
		}
		if got := s.Documents("flagd", source); len(got) != 0 {
			t.Fatalf("source %s still has documents %v", source, got)
		}
	}
	if _, _, err := s.Get(context.Background(), "f1", &Selector{}); err == nil {
		t.Fatal("a cleared store still serves f1")
	}
}

func TestClearKeepsTheSourcesAndTakesNewDeliveries(t *testing.T) {
	s := newDocumentsStore(t, "a", "b")
	if err := s.Apply(ApplyRequest{Format: "flagd", Source: "a", Flags: flagsOf("f1")}); err != nil {
		t.Fatal(err)
	}
	s.Clear()

	if got := s.Sources(); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("Clear changed the sources: %v", got)
	}
	// a delivery to a registered source is accepted again, as an unchanged one would be after a restart
	err := s.Apply(ApplyRequest{
		Format: "flagd", Source: "a", Flags: flagsOf("f1"),
		Documents: []Document{doc("flagd", "a", "", "v1", "f1")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := flagKeys(t, s, "a"); len(got) != 1 {
		t.Fatalf("want f1 after the new delivery, got %v", got)
	}
}

func TestClearOfAnEmptyStore(t *testing.T) {
	s := newDocumentsStore(t, "a")
	s.Clear()
	s.Clear()
	if got := flagKeys(t, s, "a"); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}

// Clear is one transaction: a reader sees all of the flags of a delivery or none of them, never some.
func TestClearIsOneTransaction(t *testing.T) {
	s := newDocumentsStore(t, "a")
	keys := []string{"f1", "f2", "f3", "f4", "f5", "f6", "f7", "f8"}

	var stop atomic.Bool
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for !stop.Load() {
			s.Clear()
			if err := s.Apply(ApplyRequest{Format: "flagd", Source: "a", Flags: flagsOf(keys...)}); err != nil {
				t.Error(err)
				return
			}
		}
	}()

	for range 2000 {
		flags, _, err := s.View().GetAll(&Selector{})
		if err != nil {
			t.Fatal(err)
		}
		if n := len(flags); n != 0 && n != len(keys) {
			stop.Store(true)
			wg.Wait()
			t.Fatalf("a reader saw %d of %d flags", n, len(keys))
		}
	}
	stop.Store(true)
	wg.Wait()
}
