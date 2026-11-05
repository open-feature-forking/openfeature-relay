# Flaky tests on CI (#190)

Source: the last 300 runs of `build.yaml` (2026-11-04 11:06 to 2026-11-05 17:58 UTC), every attempt. 124 jobs failed; 68 of them with a `--- FAIL:` or a package `FAIL` line (the rest are lint, release dry run and builder jobs). A failure counts as **flaky** when the same job passed on the same commit in another attempt or run, or when it failed on a PR (or a main commit) that doesn't touch the test's package. Counts are failed jobs, not subtests.

## Ranked

| # | Test | Package | Count | Jobs | Error signature | Likely cause | Status |
|---|------|---------|------:|------|-----------------|--------------|--------|
| 1 | `TestResetRepeatedly` | `runtime` | 9 | test | `"15" is not less than or equal to "14"`: file descriptors leaked | the descriptor count is process-wide; another test's connection moves it | fixed, #171 |
| 2 | `TestEndpointSettingsCharacterization`, `TestMiddlewareSeesH2CRequests`, `TestFromConfigRepeatedly`, `TestShutdownEvaluationEventStream`, `TestShutdownOFREPEventStream`, `TestFailedFromConfigReleasesItsListeners`, `TestUnknownConfigurationFormatSkipsSourceAndReadiness` | `runtime`, `runtime/fromconfig_test` | 11 (3+3+3+1+1+1+1) | test | `Condition never satisfied: the runtime becomes ready` after 10–20 s; `bind: address already in use`; `connection refused` on the SSE port right after readiness | port race: `newPorts` calls `freePort` (listen on `:0`, close) four times | #223 merged (each port once); #230 open (pre-opened listeners) |
| 3 | `TestProxyRejectsWhatFlagdFullRejects` | `test/distributions` | 3 | test | `the recorded verdict differs from flagd-full's`: a valid file not ready | the same port twice in one start; the fatal bind line was filtered out | fixed, #218 (and the retry of #172) |
| 4 | `TestFlagdFullOpensTodaysPorts`, `TestStartsWithFlagdFullDefaults`, `TestFullBufferDropsAndReportsOnMetrics`, `TestClientReconnectsAfterRestart` | `test/distributions` | 5 (2+1+1+1) | test | `flagd-full becomes ready` after 60 s; `bind: address already in use` (sync and management both 41581) | port race in `freePort` | fixed, #218 |
| 5 | `TestStreamDeadlineRecyclesWithoutDelay` | `sync/flagd-grpc` | 3 | FIPS | `Should be empty, but was [4s]`: no delay between a deadline and the next stream | **product bug**: a server-side `DeadlineExceeded` took the 4 s backoff | fixed, #226 / #229 |
| 6 | `TestListenSeamUsedByEveryEndpoint` | `runtime` | 2 | test (main), FIPS (#230) | one `tcp :port` missing from the recorded listeners | readiness taken to mean every listener is up | #230 (failed on #230's own FIPS run 42076460347) |
| 7 | `TestLoadFetchesNow` | `testbed/source` | 2 | test | `no delivery` after 5 s | test race: `Load` before the trigger watched | fixed, #206 |
| 8 | image builds: `integration_test` `TestMain`, `test/upgrade` `TestUpgrade`/`TestSpans`/`TestRollback`/`TestStartupTime` | `test/integration`, `test/upgrade` | 3 | integration-test, Upgrade differential | `go mod download` in the Docker build exits 1; the one full log shows `proxy.golang.org ... stream error: INTERNAL_ERROR` | module proxy hiccup, no retry; `docker build -q` hides the cause | **open**: fix/flake-module-download |
| 9 | `TestManagementPort/inventory` | `runtime/managementport` | 1 | test | golden diff: `reason=client_disconnect` instead of `reason=normal_close` on `sync_server_stream_duration_seconds` | event ordering: a payload that races the client's cancel ends the stream through the failed `Send` | **open**: fix/flake-event-order |
| 10 | `TestStartupLogCharacterization` | `flagd` | 1 | FIPS | `flagd did not become ready` | port race | #223, #230 |
| 11 | `TestChangeThroughTrigger` | `testbed/cmd` | 1 | test | `Condition never satisfied` after 5 s | a 5 s bound; seen once, on the PR that added it (#163) | watch; the file is in #230 |
| 12 | `TestEngineServesBothFormats` | `test/configformat-example/distribution` | 1 | test | `readiness covers both sources` after 20 s | port race (on #134, attempt 2) | #230 |

## Not flaky

| Test or job | Count | Why |
|-------------|------:|-----|
| 37 `test/distributions` tests on #217 | 3 runs | `relay-builder: cannot resolve the module .../runtime/runtime`: the branch's own bug, fixed before merge |
| `test/upgrade` `TestAcceptedMatchesIssue`, `TestGuideCoversAcceptedItems` | 7 runs | `accepted.yaml` and the text of #78 drifted apart; deterministic |
| `TestRepositoryManifestsReplaceEveryRelayModule` | 4 runs | #139 and #147 added modules the manifests did not replace |
| `TestFlagsmithServesAnEnvironmentDocument` | 5 runs | main broken between #124 and #136 (Nov 4, 13:12–13:47), every branch based on it failed |
| FIPS `runtime/cmd` `TestLoadConfig*` | 4 runs | the FIPS job had no `test-harness` submodule; fixed in `build.yaml` |
| `test/release` on #220, #221 | 2 runs | `kr/text` missing from the local proxy with a cold module cache; deterministic in the matrix job. Latent if the matrix comes back |
| `[build failed]` in `sync/flagd-grpc` | 4 runs | `Options.retryOptedIn` declared twice; fixed by #173 |
| integration-test on #139 | 3 runs | `eventsink/webhook` missing from the Dockerfile |
| `runtime/tracingoff_test` | 1 | `signal: terminated`: the run was cancelled |
| `TestUnknownCommandExit`, `TestGRPCSourceFatalCode` | 2 | the PRs' own changes |

## Causes

- **Port races** (rows 2, 3, 4, 10, 12): 21 of the 42 flaky failures. #218 and #223 hand out each port once; #230 removes the close-then-bind window for in-process tests. Out-of-process distributions still pick a port and pass it to flagd, which only #218's de-duplication covers.
- **Readiness taken for every listener up** (row 6, part of row 2's `connection refused`): #230.
- **Hard timeouts on a fast machine** (rows 1, 7, 11): fixed where seen; none open with evidence. Row 11 is one 5 s bound to watch.
- **Event ordering** (row 9): open.
- **Infrastructure** (row 8): module proxy errors in Docker builds; open.
- **Product bugs** (row 5): #226. None other found: every remaining signature is in a test or the build.

Since #229 and #223 merged, no flaky test failure has been recorded on a commit that has them, apart from #230's own run.
