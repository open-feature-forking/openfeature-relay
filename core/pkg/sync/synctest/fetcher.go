package synctest

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
)

// FetcherInputs are the test inputs a fetcher's contract needs.
type FetcherInputs struct {
	// Source is a source the fetcher can read, with the flag configuration the test starts from.
	Source sync.SourceConfig
	// Change changes the source's flag configuration.
	Change Input
	// Break makes every later fetch of the source fail.
	Break Input
	// Timeout is the time limit of a case; zero means DefaultTimeout.
	Timeout time.Duration
}

// Names of the contract cases of a fetcher (UC-008 BR-002).
const (
	CaseFetchesOnce        = "fetches the flag configuration once per request with its source version"
	CaseReportsUnchanged   = "reports not changed when given the current source version, and never without one"
	CaseNewVersion         = "returns a new source version after the source changed"
	CaseAbandonsOnDone     = "abandons a running fetch when its context is done"
	CaseReportsFailedFetch = "reports a failed fetch without returning a flag configuration"
)

// RunFetcher runs the contract tests of a fetcher: one subtest per contract case.
func RunFetcher(t *testing.T, f sync.FetcherFactory, in FetcherInputs) {
	t.Helper()
	CheckFetcher(t, f, in).report(t)
}

// CheckFetcher runs the contract tests of a fetcher and returns their outcome. t is only handed to the test inputs.
func CheckFetcher(t testing.TB, f sync.FetcherFactory, in FetcherInputs) Report {
	if in.Source.URI == "" || in.Change == nil || in.Break == nil {
		if msg := missing(
			missingInput{"FetcherInputs.Source", in.Source.URI != "", fetcherCases},
			missingInput{"FetcherInputs.Change", in.Change != nil, []string{CaseNewVersion}},
			missingInput{"FetcherInputs.Break", in.Break != nil, []string{CaseReportsFailedFetch}},
		); msg != "" {
			return Report{Aborted: msg}
		}
	}
	if msg := checkSchemes(f); msg != "" {
		return Report{Aborted: msg}
	}

	fetcher, err := f.CreateFetcher(newSettings(in.Source, f.Type(), nil))
	if err != nil || fetcher == nil {
		return Report{Aborted: fmt.Sprintf("the factory cannot create the fetcher (fetcher %v, error %v)", fetcher, err)}
	}
	if closer, ok := fetcher.(io.Closer); ok {
		defer closer.Close()
	}
	if init, ok := fetcher.(sync.InitializingFetcher); ok {
		if err := init.Init(context.Background()); err != nil {
			return Report{Aborted: fmt.Sprintf("the fetcher cannot be initialized: %v", err)}
		}
	}

	limit := timeoutOr(in.Timeout)
	fetch := func(ctx context.Context, previous string) (res sync.FetchResult, err error, returned bool) {
		returned = within(limit, func() { res, err = fetcher.Fetch(ctx, previous) })
		return res, err, returned
	}

	var cases []Case

	// the first fetch, and the version it returns
	res, err, returned := fetch(context.Background(), "")

	cases = append(cases, check(CaseFetchesOnce,
		returned && err == nil && !res.Unchanged && res.Version != "",
		"a fetch without a previous version returns the flag configuration and a non-empty source version",
		describe(res, err, returned)))

	res2, err2, returned2 := fetch(context.Background(), res.Version)
	// a fetch without a previous version is how a forced fetch asks for the full flag configuration
	res2b, err2b, returned2b := fetch(context.Background(), "")
	cases = append(cases, check(CaseReportsUnchanged,
		returned2 && err2 == nil && res2.Unchanged && returned2b && err2b == nil && !res2b.Unchanged,
		"a fetch given the current source version reports Unchanged, and a fetch without a previous version never does",
		describe(res2, err2, returned2)+"; without a previous version: "+describe(res2b, err2b, returned2b)))

	in.Change(t)
	res3, err3, returned3 := fetch(context.Background(), res.Version)
	cases = append(cases, check(CaseNewVersion,
		returned3 && err3 == nil && !res3.Unchanged && res3.Version != "" && res3.Version != res.Version,
		fmt.Sprintf("after the source changed, a fetch given %q returns the changed flag configuration with a new source version", res.Version),
		describe(res3, err3, returned3)))

	done, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, returned4 := fetch(done, res3.Version)
	cases = append(cases, check(CaseAbandonsOnDone, returned4,
		fmt.Sprintf("a fetch with a done context returns within %s", limit),
		fmt.Sprintf("returned within the limit: %v", returned4)))

	in.Break(t)
	res5, err5, returned5 := fetch(context.Background(), res3.Version)
	cases = append(cases, check(CaseReportsFailedFetch,
		returned5 && err5 != nil && len(res5.Data) == 0 && !res5.Unchanged,
		"a fetch of a broken source returns an error and no flag configuration",
		describe(res5, err5, returned5)))

	return Report{Cases: cases}
}

var fetcherCases = []string{CaseFetchesOnce, CaseReportsUnchanged, CaseNewVersion, CaseAbandonsOnDone, CaseReportsFailedFetch}

func describe(res sync.FetchResult, err error, returned bool) string {
	if !returned {
		return "the fetch did not return in time"
	}
	return fmt.Sprintf("error %v, %d bytes, version %q, unchanged %v", err, len(res.Data), res.Version, res.Unchanged)
}
