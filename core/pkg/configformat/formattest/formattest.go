// Package formattest is the contract every configuration format has to pass. A format's tests call
// Run with its factory and samples; each failure prints the expected and the observed result.
package formattest

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/configformat"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/model"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
)

const (
	sourceA = "contract-source-a"
	sourceB = "contract-source-b"
)

// Set is the expected document of one flag set. FlagSet is "" for the unnamed flag set.
type Set struct {
	FlagSet string
	Keys    []string
}

// Delivery is a flag configuration and the documents it yields.
type Delivery struct {
	Data string
	Sets []Set
}

// Change is a delivery that follows Samples.Valid on the same source and changes one flag set.
type Change struct {
	Delivery
	FlagSet string
	Added   []string
	Updated []string
	Removed []string
}

// Faulty is a flag configuration the format must reject, naming one of Entries.
type Faulty struct {
	Data    string
	Entries []string
}

// Samples are the format's own inputs for the contract. Incremental and Dropped are optional; the
// checks that need them are skipped when they are nil.
type Samples struct {
	Valid   Delivery
	Changed Change
	Invalid Faulty
	// Incremental holds two deliveries for different flag sets, to be applied one after the other.
	Incremental *[2]Delivery
	// Dropped is a delivery in which the format skips the entries named in Entries and accepts the rest.
	Dropped *Faulty
}

// Run checks the format created by f against the contract.
func Run(t testing.TB, f configformat.Factory, s Samples) {
	t.Helper()
	checks := []struct {
		name string
		run  func(testing.TB, configformat.Factory, Samples)
	}{
		{"accepts a valid delivery", checkAcceptsValid},
		{"reports a stable version", checkVersionStable},
		{"reports changes", checkChanges},
		{"rejects an invalid delivery naming source and entry", checkRejects},
		{"leaves the store unchanged after a rejection", checkRejectionKeepsStore},
		{"replaces only the delivering source", checkOnlyDeliveringSource},
		{"replaces only the flag sets of an incremental update", checkIncremental},
		{"names dropped entries", checkDropped},
	}
	for _, c := range checks {
		if tt, ok := t.(*testing.T); ok {
			tt.Run(c.name, func(t *testing.T) { c.run(t, f, s) })
			continue
		}
		c.run(t, f, s)
	}
}

func setup(t testing.TB, f configformat.Factory) (configformat.ConfigurationFormat, *store.Store) {
	t.Helper()
	log := logger.NewLogger(nil, false)
	st, err := store.NewStore(log, []string{sourceA, sourceB})
	if err != nil {
		t.Errorf("cannot create the store: %v", err)
		return nil, nil
	}
	format, err := f.Create(configformat.Settings{Store: st, Logger: log})
	if err != nil {
		t.Errorf("factory %q cannot create the format: %v", f.Type(), err)
		return nil, nil
	}
	return format, st
}

func deliver(t testing.TB, format configformat.ConfigurationFormat, source, data string, incremental bool) (configformat.Result, bool) {
	t.Helper()
	res, err := format.Accept(context.Background(), configformat.Input{Source: source, Data: data, IncrementalUpdate: incremental})
	if err != nil {
		t.Errorf("expected the delivery to be accepted\nobserved error: %v", err)
		return res, false
	}
	return res, true
}

// flagRows returns the flags the store holds for a source.
func flagRows(t testing.TB, st *store.Store, source string) []model.Flag {
	t.Helper()
	sel := store.Selector{}.WithSource(source)
	flags, _, err := st.GetAll(context.Background(), &sel)
	if err != nil {
		t.Errorf("cannot read the flags of source %q: %v", source, err)
	}
	return flags
}

func keys(k []string) []string {
	out := append([]string{}, k...)
	sort.Strings(out)
	return out
}

func expect(t testing.TB, what string, want, got any) bool {
	t.Helper()
	if !reflect.DeepEqual(want, got) {
		t.Errorf("%s\nexpected: %v\nobserved: %v", what, want, got)
		return false
	}
	return true
}

func checkReport(t testing.TB, res configformat.Result, sets []Set) {
	t.Helper()
	want := make([]Set, len(sets))
	for i, s := range sets {
		want[i] = Set{FlagSet: s.FlagSet, Keys: keys(s.Keys)}
	}
	sort.Slice(want, func(i, j int) bool { return want[i].FlagSet < want[j].FlagSet })

	got := make([]Set, len(res.Documents))
	for i, d := range res.Documents {
		got[i] = Set{FlagSet: d.FlagSet, Keys: keys(d.FlagKeys)}
		if d.Version == "" {
			t.Errorf("the document of flag set %q needs a version\nexpected: a non-empty version\nobserved: %q", d.FlagSet, d.Version)
		}
	}
	sort.Slice(got, func(i, j int) bool { return got[i].FlagSet < got[j].FlagSet })
	expect(t, "documents of the delivery (flag set and flag keys)", want, got)
}

func report(res configformat.Result, flagSet string) (configformat.DocumentReport, bool) {
	for _, d := range res.Documents {
		if d.FlagSet == flagSet {
			return d, true
		}
	}
	return configformat.DocumentReport{}, false
}

func checkAcceptsValid(t testing.TB, f configformat.Factory, s Samples) {
	t.Helper()
	format, st := setup(t, f)
	if format == nil {
		return
	}
	expect(t, "type of the format the factory created", f.Type(), format.Type())
	res, ok := deliver(t, format, sourceA, s.Valid.Data, false)
	if !ok {
		return
	}
	checkReport(t, res, s.Valid.Sets)

	stored := st.Documents(format.Type(), sourceA)
	reported := map[string]string{}
	for _, d := range res.Documents {
		reported[d.FlagSet] = d.Version
	}
	storedVersions := map[string]string{}
	for _, d := range stored {
		storedVersions[d.FlagSet] = d.Version
		expect(t, fmt.Sprintf("format and source of the stored document of flag set %q", d.FlagSet),
			[2]string{format.Type(), sourceA}, [2]string{d.Format, d.Source})
	}
	expect(t, "versions the store holds against the versions reported", reported, storedVersions)
	var storedSets []Set
	for _, d := range stored {
		storedSets = append(storedSets, Set{FlagSet: d.FlagSet, Keys: keys(d.FlagKeys)})
	}
	var want []Set
	for _, set := range s.Valid.Sets {
		want = append(want, Set{FlagSet: set.FlagSet, Keys: keys(set.Keys)})
	}
	sort.Slice(want, func(i, j int) bool { return want[i].FlagSet < want[j].FlagSet })
	expect(t, fmt.Sprintf("documents the store holds for format %q and source %q", format.Type(), sourceA), want, storedSets)

	for _, set := range s.Valid.Sets {
		if d, ok := report(res, set.FlagSet); ok {
			expect(t, fmt.Sprintf("keys added by the first delivery of flag set %q", set.FlagSet), keys(set.Keys), keys(d.Added))
		}
	}
}

func checkVersionStable(t testing.TB, f configformat.Factory, s Samples) {
	t.Helper()
	format, _ := setup(t, f)
	if format == nil {
		return
	}
	first, ok := deliver(t, format, sourceA, s.Valid.Data, false)
	if !ok {
		return
	}
	second, ok := deliver(t, format, sourceA, s.Valid.Data, false)
	if !ok {
		return
	}
	for _, d := range first.Documents {
		again, found := report(second, d.FlagSet)
		if !found {
			t.Errorf("the repeated delivery lost flag set %q", d.FlagSet)
			continue
		}
		expect(t, fmt.Sprintf("version of flag set %q after the same input", d.FlagSet), d.Version, again.Version)
		expect(t, fmt.Sprintf("changes of flag set %q after the same input", d.FlagSet),
			[][]string{{}, {}, {}}, [][]string{keys(again.Added), keys(again.Updated), keys(again.Removed)})
	}
}

func checkChanges(t testing.TB, f configformat.Factory, s Samples) {
	t.Helper()
	format, _ := setup(t, f)
	if format == nil {
		return
	}
	first, ok := deliver(t, format, sourceA, s.Valid.Data, false)
	if !ok {
		return
	}
	res, ok := deliver(t, format, sourceA, s.Changed.Data, false)
	if !ok {
		return
	}
	checkReport(t, res, s.Changed.Sets)
	d, found := report(res, s.Changed.FlagSet)
	if !found {
		t.Errorf("the changed delivery reports no document for flag set %q", s.Changed.FlagSet)
		return
	}
	before, _ := report(first, s.Changed.FlagSet)
	if d.Version == before.Version {
		t.Errorf("version of flag set %q after a change\nexpected: a value other than %q\nobserved: %q", s.Changed.FlagSet, before.Version, d.Version)
	}
	expect(t, "added keys", keys(s.Changed.Added), keys(d.Added))
	expect(t, "updated keys", keys(s.Changed.Updated), keys(d.Updated))
	expect(t, "removed keys", keys(s.Changed.Removed), keys(d.Removed))
}

func checkRejects(t testing.TB, f configformat.Factory, s Samples) {
	t.Helper()
	format, _ := setup(t, f)
	if format == nil {
		return
	}
	_, err := format.Accept(context.Background(), configformat.Input{Source: sourceA, Data: s.Invalid.Data})
	if err == nil {
		t.Errorf("expected the invalid delivery to be rejected\nobserved: accepted")
		return
	}
	var rej configformat.Rejection
	if !errors.As(err, &rej) {
		t.Errorf("expected a configformat.Rejection naming source and entry\nobserved: %T: %v", err, err)
		return
	}
	expect(t, "source named by the rejection", sourceA, rej.Source)
	for _, e := range s.Invalid.Entries {
		if rej.Entry == e {
			return
		}
	}
	t.Errorf("entry named by the rejection\nexpected: one of %v\nobserved: %q", s.Invalid.Entries, rej.Entry)
}

func checkRejectionKeepsStore(t testing.TB, f configformat.Factory, s Samples) {
	t.Helper()
	format, st := setup(t, f)
	if format == nil {
		return
	}
	if _, ok := deliver(t, format, sourceA, s.Valid.Data, false); !ok {
		return
	}
	before := st.Documents(format.Type(), sourceA)
	flagsBefore := flagRows(t, st, sourceA)
	if _, err := format.Accept(context.Background(), configformat.Input{Source: sourceA, Data: s.Invalid.Data}); err == nil {
		t.Errorf("expected the invalid delivery to be rejected\nobserved: accepted")
		return
	}
	expect(t, "documents after a rejected delivery", before, st.Documents(format.Type(), sourceA))
	expect(t, "flags of the source after a rejected delivery", flagsBefore, flagRows(t, st, sourceA))
}

func checkOnlyDeliveringSource(t testing.TB, f configformat.Factory, s Samples) {
	t.Helper()
	format, st := setup(t, f)
	if format == nil {
		return
	}
	for _, src := range []string{sourceA, sourceB} {
		if _, ok := deliver(t, format, src, s.Valid.Data, false); !ok {
			return
		}
	}
	before := st.Documents(format.Type(), sourceB)
	flagsBefore := flagRows(t, st, sourceB)
	if _, ok := deliver(t, format, sourceA, s.Changed.Data, false); !ok {
		return
	}
	expect(t, fmt.Sprintf("documents of source %q after source %q delivered", sourceB, sourceA), before, st.Documents(format.Type(), sourceB))
	expect(t, fmt.Sprintf("flags of source %q after source %q delivered", sourceB, sourceA), flagsBefore, flagRows(t, st, sourceB))
}

func checkIncremental(t testing.TB, f configformat.Factory, s Samples) {
	t.Helper()
	if s.Incremental == nil {
		return
	}
	format, st := setup(t, f)
	if format == nil {
		return
	}
	if _, ok := deliver(t, format, sourceA, s.Incremental[0].Data, true); !ok {
		return
	}
	if _, ok := deliver(t, format, sourceA, s.Incremental[1].Data, true); !ok {
		return
	}
	var want []string
	for _, d := range s.Incremental {
		for _, set := range d.Sets {
			want = append(want, set.FlagSet)
		}
	}
	sort.Strings(want)
	var got []string
	for _, d := range st.Documents(format.Type(), sourceA) {
		got = append(got, d.FlagSet)
	}
	expect(t, "flag sets held after two incremental deliveries", want, got)
}

func checkDropped(t testing.TB, f configformat.Factory, s Samples) {
	t.Helper()
	if s.Dropped == nil {
		return
	}
	format, _ := setup(t, f)
	if format == nil {
		return
	}
	res, ok := deliver(t, format, sourceA, s.Dropped.Data, false)
	if !ok {
		return
	}
	var got []string
	for _, d := range res.Dropped {
		if d.Source != sourceA {
			t.Errorf("source named by a dropped entry\nexpected: %q\nobserved: %q", sourceA, d.Source)
		}
		got = append(got, d.Entry)
	}
	expect(t, "dropped entries", keys(s.Dropped.Entries), keys(got))
}
