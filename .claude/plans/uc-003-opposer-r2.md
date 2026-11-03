# UC-003 plan: opposer findings (revision 1)

Plan: `.claude/plans/uc-003.md` on `origin/main` (draft, revision 1). Checked against `origin/uc-014/pr-6-evaluator-format-modules` (extracted read-only to a scratch directory; nothing in a worktree was changed), the spec on `origin/main` (`docs/requirements.md`, `docs/glossary.md`, `UC-003`, `UC-008`, `UC-019`, `docs/research/cross-vendor-evaluation.md`), `docs/concepts/syncs.md`, and the Go in-process provider in `go-sdk-contrib` (read-only). File references below are on the UC-014 ref unless marked otherwise.

Counts: **1 blocker, 12 major, 7 minor.**

## What holds (verified)

- Q7's leak is real. `GOWORK=off go list -deps ./...` in `sync/azblob` lists `google.golang.org/api/googleapi`, `github.com/googleapis/gax-go/v2`, `google.golang.org/grpc`, `go.opencensus.io/...`, `cloud.google.com/go/compute/metadata` and `cloud.google.com/go/auth/internal` (via `gocloud.dev/internal/retry`, `gocloud.dev/internal/oc`).
- Q2 is described correctly: `http_sync.go:126` and `blob_sync.go:97` return the initial fetch's error, and `runtime.go` runs every `Sync` in the errgroup, so flagd exits.
- The core's only forbidden module in the module graph today is `robfig/cron/v3` (`GOWORK=off go mod graph` in `core`), so the "core graph" gate is a real change from red to green in PR 2, not a vacuous one.
- `bloburi.Join` is the exact inverse of `Split` (`bloburi.go:31`), so the source name of a blob delivery equals the configured URI (see finding 8).
- The proposed splits of FR-045/053/054/055 match the glossary and UC-003 BR-003/BR-004 where the plan follows them.

## Blocker

### 1. PR 2 cannot be green as written (2c vs the providers that still exist)

- **Step:** 2c, with the PR 2 / PR 3 split.
- **Evidence:** 2c rewrites `CronPoller` into a `Trigger` (`Run(ctx, signal func()) error`), drops `robfig/cron` and deletes `core/pkg/sync/testing/mock_cron.go`. But until PR 3, `sync/http`, `sync/blob`, `sync/builder` and their tests still use the old API: `polling.Poller` (`http_sync.go:28,282`, `blob_sync.go:61`), `polling.NewSourcePoller` / `*polling.CronPoller` (`blob_sync.go:36`, `syncbuilder.go:225-226`), `Poller.Start(ctx, callback)` and `Offset()` (`blob_sync.go:105,107`), and `synctesting.NewMockPoller()` (`http_sync_test.go:19,49,93,123`; `blob_sync_test.go`). Every module resolves core through a `replace => ../../core`, so these modules compile against the PR 2 core.
- **Why it matters:** with 2c as written, `sync/http`, `sync/blob` and `sync/builder` stop compiling in PR 2 (and so do `flagd` and `flagd-proxy`, which import the builder). The plan's own check, "`go mod graph | grep robfig` prints nothing", also needs the cron dependency removed in the same PR that still has callers.
- **Fix:** PR 2 adds the timer-based trigger next to the old API: keep `Poller`, `NewCronPoller`, `NewSourcePoller` and `MockPoller` (re-implement `CronPoller` on `time.Timer`, same signature, no `robfig`), and delete them in the PR 3 commit that moves the last caller. State this in 2c and in the PR 2 check.

## Major

### 2. The plan's gates omit the CI checks that the new packages break

- **Step:** section 4 (gates), 2d/2e, 4b, 4d, 5d.
- **Evidence:** the CI `lint` job runs `make check-package-coverage` and `make check-module-boundary` (`.github/workflows/build.yaml:39-40`). `check-package-coverage` diffs `go list ./...` of every `GO_LIB_MODULE_DIRS` module against `testdata/go-packages.txt`. PRs 2 (`core/pkg/sync/fetching`, `synctest`, removal of `sync/testing`), 3, 4 (`test/sync-example`) and 5 (removal of `sync/blob`, `sync/blob/bloburi`) all change that list; none of the steps mentions `testdata/go-packages.txt`. Neither check is part of `make lint` or of the plan's gate table.
- **Related:** `testdata/module-boundaries.txt` and `testdata/module-graph-boundaries.txt` already carry the rows this plan wants to change: the `sync/blob`, `sync/gcs`, `sync/azblob`, `sync/s3` rows, and a comment calling "native clients per cloud" a named gap. The plan instead specifies one-off `grep` commands (4b, 5a-5c), which would not be enforced after the PR.
- **Fix:** add `testdata/go-packages.txt` updates to 2d/2e/4b/5d; add `make check-package-coverage check-module-boundary` to the gate table for every PR; replace the ad hoc greps by new or tightened rows in the two boundary files (`test/sync-example` core only; `sync/gcs`, `sync/azblob`, `sync/s3` each forbidding the other two clouds and `gocloud.dev`; azblob forbidding `google.golang.org/(api|grpc)`).

### 3. Single-flight fetching with no fetch deadline can silently stop a source (C-003)

- **Step:** 2d (`TestSingleFlightOneTrailing`), 3a.
- **Evidence:** today `http.Client.Timeout` is `time.Second * hs.timeoutS` (`http_sync.go:267`) and `timeoutS` defaults to 0, which means no timeout; gocloud calls carry no deadline either. `robfig/cron` (`poller.go:91,108`, no `SkipIfStillRunning`) starts each run in its own goroutine, so a hung fetch today does not stop later polls. BR-004 and 2d allow one fetch at a time: one hung request blocks every later poll, trigger signal and `ReSync`, forever, while readiness stays true (it is set after the first success).
- **Fix:** the fetching sync provider gives every `Fetch` a context deadline (a documented default when `timeoutS` is 0, for example the greater of the interval and 30 s) and a test that a never-returning fetcher does not block the next one after the deadline.

### 4. A failed first fetch is retried only if a poller happens to be attached

- **Step:** 2d (`TestFailedFirstFetchStaysUpNotReady`), Q2, Q4, Q6.
- **Evidence:** 2d fetches once before the triggers run and "stays up, not ready". The only thing that fetches again is a trigger. Q6 gives a `file` source only the file watch by default, and `triggers` may be configured without the poller (FR-053 says "and still polls as a fallback", the plan does not require it). With Q4, a permission error on the first read leaves the source not ready until the file next changes. An SSE-only source behaves the same.
- **Fix:** until the first success the provider retries by itself with the backoff of Q3, independent of the attached triggers; add `TestFirstFetchRetriesWithoutPoller`. For `file`, the trigger must also signal on chmod/permission change (today only a delete is derived from `Chmod`, `filepath_sync.go:144-149`).

### 5. A missing file at startup fails flagd today; Q4's "as today" is wrong for startup

- **Step:** Q4, 3b, PR 1 `sync/file` characterization.
- **Evidence:** `file.Sync.Init` adds the path to the watcher before any fetch (`filepath_sync.go` `Init`). Verified on a scratch copy: `NewFileSync("/nonexistent/flags.json", ...).Init` returns `error adding watcher ...: no such file or directory` for fsnotify and for fileinfo, so `Runtime.Start` returns "sync provider Init returned error" and flagd exits (`runtime.go`). The `{}`-on-error behaviour only exists after startup (`sendDataSync`). No existing test pins the startup failure (`filepath_sync_test.go` has none), and PR 1's file table does not list it. Q4 says a nonexistent file "is a successful fetch of an empty flag configuration (`{}`), as today", and 3b moves the watch into a trigger, so a mistyped `--uri file:...` would start flagd ready with no flags.
- **Fix:** add the startup case to PR 1; decide it explicitly. Recommend keeping fail-fast for a path that does not exist at startup (an existence check in the file factory), and `{}` only for a file that disappears later. If the plan wants the change, list it with Q2 as a behaviour change.

### 6. Q3's backoff is not jittered, changes single-blip recovery and leaves alignment undefined

- **Step:** Q3, 2c (`TestBackoff`).
- **Evidence:** glossary (`Fetch Trigger`) promises "an interval with jitter and backoff". Today's jitter is the deterministic `intervalSeed` offset, and `docs/reference/sync-configuration.md:56` says that without a seed the offset is 0, so every instance polls on the same wall-clock tick. Q3 adds `interval × 2^n` with no randomness, so after a shared outage all instances retry at identical times and recover together on the same tick (a herd on the origin). It also delays the retry after the first failure (a 5 s source waits 10 s after one 500, the next poll today is at 5 s). The plan does not say whether the backed-off wait is aligned to `OffsetSchedule` or measured from the failure.
- **Fix:** keep the first retry at the normal schedule, back off from the second consecutive failure, add jitter to the backoff delay only (the happy path stays deterministic, so C-003 holds), and specify alignment.

### 7. Extension schemes cannot be used with `--uri` / `-f`, and BR-002 selection is untested

- **Step:** 3e, 4a, 4c, use case coverage table (main flow).
- **Evidence:** `ParseSyncProviderURIs` (`sync/builder/utils.go:34-90`) maps a URI to a provider with hard-coded regexes and rewrites the source on the way: `file:` and `core.openfeature.dev/` are stripped, `grpc://` is stripped, `grpcs://` is stripped and sets `TLS: true`, the custom resolver prefix `(envoy|dns|uds|xds)` is kept. 3e keeps it and its error text unchanged, so a distribution containing an `example://` fetcher (UC-003 BR-002 and step 8) cannot be addressed with `--uri example://...`; only `--sources` with an explicit provider name works. `Schemes()` returning plain prefixes also cannot express the rewriting. The plan's tests never exercise the selection: the main flow runs `RunFetcher` on the factory, and `TestExtensionTriggerAttached` uses a file source.
- **Fix:** give factories a `SourceFromURI(uri) (SourceConfig, bool)` (or equivalent) used by `ParseSyncProviderURIs`, keep the official error text when only official factories are present, and add a builder test that an extension factory's scheme resolves through `--uri` and through `--sources`.

### 8. The source name is returned by the factory, but the runtime keys everything on the configured URI

- **Step:** 2b (`CreateFetcher(Settings) (Fetcher, string, error)`), 3c, risk "C-003, source names".
- **Evidence:** readiness (`sourceReadiness[provider.URI]`, `from_config.go:129-133`; `runtime.go:168-169`) and the format lookup (`formats[provider.URI]`, `from_config.go:320`; `Formats[payload.Source]` in `runtime.go`) are keyed by the configured `SourceConfig.URI`, and the comment at `from_config.go:123-124` states the sync implementation "must emit DataSync.Source matching this exact string". `bloburi.Join(Split(uri))` is the identity (`bloburi.go:31`), so blob never needed a different name. A factory that returns any other string leaves the source never ready and bypasses its configuration format, silently. The plan also says Join "drops the query" (PR 1 table), which is wrong.
- **Fix:** drop the string result; the provider always delivers `Source = SourceConfig.URI`; `synctest` asserts it (UC-003 BR-006). Correct the PR 1 row.

### 9. The official streaming providers do not meet the contract the plan runs them against, and fixing it changes readiness (C-003)

- **Step:** 3d, 2e (`RunStreaming`), UC-003 BR-008, UC-008 streaming row.
- **Evidence:** BR-008: a streaming sync provider "reconnects and reports itself not ready meanwhile". The gRPC provider sets `ready` on every stream open (`grpc_sync.go:239`) and never clears it (the only `Store` is `true`); it stays ready while disconnected. The operator provider returns an error from a failed initial fetch (`kubernetes_sync.go` `Sync`, "error with the initial fetch"), which stops flagd, sets `ready` only on the informer's ready event and never clears it (`:181`), and delivers nothing on a delete event. 3d says "behaviour is unchanged" and "`RunStreaming` against a bufconn server and a fake dynamic client" must pass; a fake dynamic client cannot lose a watch.
- **Why it matters:** either `RunStreaming` leaves out "not ready while disconnected", "survives an initial failure" and "delivers on source removal" (then BR-008 and UC-008 stay untested for the two official providers), or 3d must change flagd-full's readiness behaviour (a pod would drop out of service during a sync-source outage), which C-003 forbids without a decision.
- **Fix:** write the gaps down as accepted (named, with the owning use case), restrict the contract cases the official providers are held to, and say which cases are expected to fail on them.

### 10. The trigger contract leaves out what `Run` returns, and BR-003 depends on it

- **Step:** 2a (`Trigger.Run(ctx, signal func()) error`), 2d, 3b ("a closed watcher reconnects instead of stopping flagd").
- **Evidence:** the plan never states what a non-nil return of `Run` means. `Sync` runs under the runtime errgroup, which stops the whole distribution on the first error (`runtime.go`). BR-003 says a trigger that loses its signal reconnects by itself and never stops the other triggers of its source. Today a closed fsnotify channel returns `errors.New("filepath notifier closed")` (`filepath_sync.go` `Sync`) and stops flagd; 3b changes that, and it is not in the risks section. `signal func()` also takes no context, so a blocked `signal` during shutdown is not preventable.
- **Fix:** define it in 2a: `Run` returns nil on shutdown and a non-nil error only for an unrecoverable misconfiguration; the provider restarts a failing trigger with backoff, logs it, and keeps the others running. Add the case to `RunTrigger` and `TestSeveralTriggersAnySignalFetches`. Add the file-watch reconnect to the PR description list next to Q2/Q4.

### 11. Q5 (a) closes the gap by a statement the retained code contradicts

- **Step:** Q5, 3d, PR 4e (BR-010 wording).
- **Evidence:** the operator provider delivers `json.Marshal(ff.Spec.FlagSpec)` (`kubernetes_sync.go:336-341`), a round trip through the operator's Go type (`v1beta1.FlagSpec` with typed `Flag{State, DefaultVariant, ...}`, inline `Flags`, `RawMessage` for variants and metadata). That normalises the document (it can drop fields the Go type does not define, such as `$schema` or unknown per-flag keys, and it re-orders keys). The amended BR-010 says a sync provider "never changes that flag configuration". The plan makes the retained conversion the compliant behaviour.
- **Fix:** word the amendment as what the code does ("takes the flag configuration out of `spec.flagSpec` as the operator's typed FlagSpec; fields outside that type are not delivered"), pin it with a characterization test (a resource with an unknown flag-level field, expected output), and keep the gap listed against UC-018 BR-003.

### 12. The native Azure client drops the credential surface flagd documents

- **Step:** Q7, 5b.
- **Evidence:** `docs/concepts/syncs.md:93-106` sends users to gocloud's azureblob environment-variable list. In the gocloud version used (v0.42.0, `blob/azureblob/azureblob.go`), the variables are `AZURE_STORAGE_ACCOUNT`, `AZURE_STORAGE_KEY`, `AZURE_STORAGE_SAS_TOKEN`, `AZURE_STORAGE_CONNECTION_STRING` (and `AZURE_STORAGEBLOB_CONNECTIONSTRING`), `AZURE_STORAGE_DOMAIN`, `AZURE_STORAGE_PROTOCOL`, `AZURE_STORAGE_IS_CDN` and `AZURE_STORAGE_IS_LOCAL_EMULATOR` (`:31-49,189-195,319-324`). The Azure SDK's `DefaultAzureCredential` reads none of the key, SAS, connection string or emulator variables. 5b ("today's `AZURE_STORAGE_ACCOUNT` checks and the SDK's default credential") would break every flagd-full deployment that authenticates with a key or SAS token. S3's "standardized credentials chain" is equivalent in the v2 SDK; the claim "as gocloud uses them" is wrong for Azure.
- **Fix:** list the supported variables in 5b and test each (key, SAS, connection string, domain/protocol, emulator), or drop 5b and keep gocloud for Azure. The same table is needed for gocloud's S3 options (`ssetype`, `kmskeyid`, `accelerate`, `disable_https`, `s3ForcePathStyle`, ...) in the Q7 option list.

### 13. PR 5's proofs are skipped by `make test-*` and CI

- **Step:** 5b, 5c, risks ("stay `-short`-skipped and the reviewer runs them locally").
- **Evidence:** `make test-modules` and `test-flagd` pass `-short`; Azurite and MinIO tests are skipped by `-short` and the reviewer runs them locally. Nothing in CI then covers the three rewritten fetchers' conditional reads, `NotModified` handling (S3 reports an unmodified `GetObject` as an API error, not a status) or credential selection.
- **Fix:** all three SDKs accept an endpoint override, so each fetcher gets an `httptest` stub that runs under `-short`, covering ETag/generation match, `NotModified`, 404, and the credential selection of finding 12; Docker-backed tests stay as an extra.

## Minor

### 14. The fetching provider starts honouring `incrementalUpdates` for http, file and blob

- **Step:** 2d (`TestIncrementalFlagPassedThrough`), BR-007.
- **Evidence:** only the gRPC provider copies `IncrementalUpdates` into its deliveries today (`syncbuilder.go:214`, `grpc_sync.go:136,261`); http, file and blob ignore the field. Passing it through changes how a source with `"incrementalUpdates": true` behaves (full replace becomes scoped replace).
- **Fix:** list it as a behaviour change (it is marked experimental in `isync.go`) or leave it ignored for official fetchers.

### 15. Metrics: counts and attributes the plan does not pin

- **Step:** PR 1 (`syncmetrics_test` extension), Q4, BR-004.
- **Evidence:** the file provider records `flag_config_received` only for a real payload, not for the `{}` fallback (`filepath_sync.go` `sendDataSync`, `fetched`). Q4 makes `{}` a successful fetch, and BR-004 stops redeliveries of unchanged content, so counts change for file sources. The extension in PR 1 names `file`, `http`, `blob` but not the `kubernetes` type or the gRPC `selector` attribute (`grpc_sync.go:140`, `RecordFlagConfigReceived(..., g.Selector)`).
- **Fix:** add both to PR 1 and to the PR 3 description.

### 16. Consumers: the in-process provider builds `file.Sync` as a struct literal

- **Step:** 3b, risks "C-004".
- **Evidence:** `go-sdk-contrib/providers/flagd/pkg/service/in_process/service.go:734-738` constructs `&file.Sync{URI, Logger, Mux}` for its offline file source and drives it through `isync.ISync` (`:40,126,181`). 3b removes `file.Sync` and `NewFileSync` and offers no one-call replacement; an embedder would have to assemble the fetching provider, the file fetcher and a trigger from three packages. `polling.Poller`, `NewCronPoller` and the other exported polling API also disappear (finding 1).
- **Fix:** provide a one-call constructor in `sync/file` (for example `file.NewSyncProvider(uri, logger, opts...)` returning a `StreamingSyncProvider`) and list the removed names in UC-015's migration input.

### 17. Fetcher lifecycle and vendor pressure

- **Step:** 2a/2b, UC-019, `docs/research/cross-vendor-evaluation.md` (rows for sync provider and trigger).
- **Evidence:** `Fetcher` has no `Init`/`Close`. Native cloud clients (finding 13), GO Feature Flag retrievers and a Flagsmith realtime client hold connections that need closing on shutdown (BR-009); today gocloud opens and closes a bucket on every poll (`blob_sync.go` `getBucket`), the native clients will not. The Flagsmith environment document is fetched conditionally with `If-Modified-Since` from `updated_at` and paginated overrides (cross-vendor doc, "Sync provider"), and its realtime ping carries only `updated_at`; the opaque `Version` string and the payload-free `signal()` cover this, but the official `http` fetcher (3a) does ETag only, so such a vendor must write its own. GO Feature Flag's TOML retriever output stays out (FR-049 gap, already stated).
- **Fix:** let a fetcher optionally implement `io.Closer`, called by the provider on shutdown; say in 3a that `http` supports ETag and body hash only.

### 18. Shutdown can hang on a blocked send

- **Step:** 2d (`TestShutdown`), BR-009.
- **Evidence:** the runtime's reader exits on `gCtx.Done()` (`runtime.go`), but the http poller callback sends with a bare `dataSync <- ...` (`http_sync.go:153,157`) while `Poller.Start` waits for running jobs on stop (`poller.go:112-113`). The gRPC provider already uses `select` with `ctx.Done()` (`grpc_sync.go:138-143`).
- **Fix:** the fetching provider always sends with `select` on `ctx.Done()`; `TestShutdown` includes a delivery blocked on an unread channel.

### 19. Commit-level and naming details

- **Step:** 2a, 3a/3e, section 3 intro ("each step is one reviewable commit").
- **Evidence:** 2a writes `Fetch(ctx, previous SourceVersion)` but `SourceVersion` is not defined (`FetchResult.Version` is a `string`). 3a removes `NewHTTP`, and 3b removes `NewFileSync`, many commits before 3e updates the builder that calls them (`syncbuilder.go:155,170,234`), so those commits do not build on their own.
- **Fix:** define the type (or use `string`), and say either that commits within a PR need not build or order the steps so that each does.

### 20. `bloburi` has three consumers and nowhere to go

- **Step:** 5a, 5d.
- **Evidence:** `bloburi.Split/Join` is used by `blob_sync.go` for all three clouds (`blob_sync.go:41,154`); 5a moves it "into" `sync/gcs`, but `azblob` and `s3` need the same URI handling, and 5d deletes the module that holds it (`sync/blob/bloburi`, listed in `testdata/go-packages.txt:22`).
- **Fix:** put it in the core (a small pure package, within the core boundary rows) or copy it with its tests into each cloud module and say so.

---

# Revision 2 check

Plan: `.claude/plans/uc-003.md` revision 2 on `origin/main` (planned against `main`, where the sync modules now live). I re-read it in full and re-checked the points below against `origin/main` (extracted read-only), gocloud v0.42.0 in the module cache, and the spec rules in `CLAUDE.md`.

**Verdict: the blocker and all 12 majors are resolved. 3 new majors and 3 new minors; no new blocker.**

## Is the blocker resolved?

Yes. 2c now keeps `Poller`, `NewCronPoller`, `NewSourcePoller`, `Start`, `Offset` and `MockPoller` on a `time.Timer` implementation and adds `IntervalTrigger` beside them; the old API goes in 3f, after 3e moved the last caller. On `origin/main`, `robfig/cron` is imported only by `core/pkg/sync/polling/poller.go` (`core/go.mod:8`), and the existing poller tests use no `cron.` identifier, so dropping it from the core in PR 2 is safe. 2f adds `robfig` to the core row of the module-graph file, which makes the claim enforced rather than a one-off grep.

## Majors of revision 1

| # | Result | Note |
|---|---|---|
| 2 | Resolved | `make check-package-coverage check-module-boundary` is in the gate table for every PR; 2f, 3h and 5e update `testdata/go-packages.txt`. `test/sync-example` follows the `test/configformat-example` pattern (own `make` target, not in `GO_LIB_MODULE_DIRS`), so it needs no package-list entry; 4d adds its boundary rows |
| 3 | Resolved | Per-fetch deadline and `TestFetchDeadlineUnblocksNextFetch` (2d) |
| 4 | Resolved | `TestFirstFetchRetriesWithoutPoller`; chmod signal (3b) |
| 5 | Resolved | Q4 keeps fail-fast through the fetcher's `Init`, pinned in PR 1 |
| 6 | Resolved | Q3: first retry on schedule, jitter only on the backoff |
| 7 | Resolved with a gap | `ParseURI` per factory and `TestExtensionSchemeSelected`; the call site is missing (new minor 3) |
| 8 | Resolved | Deliveries carry `Settings.Source.URI`, asserted in `synctest` |
| 9 | Resolved by a spec decision | Q2 part 2 (providers stay ready through outages) matches what every official provider does today: http, blob and file do not clear `ready` on a failed poll, gRPC and the operator never clear it. It needs the project lead's decision and the BR-008 amendment in 4e. The gaps it leaves are new major 3 |
| 10 | Resolved | 2a defines `Run`'s return, a non-blocking `signal`, restart with backoff |
| 11 | Resolved | Q5 (c) delivers the unstructured `spec.flagSpec`. Its claim holds: `configformat/flagd/flagd.go:50-51,89-92` logs schema violations as warnings, and the format rejects only unparsable JSON, a broken `$evaluators`, non-object metadata, unparsable `flags` or a default variant that is not a variant |
| 12 | Resolved with a nit | 5c lists every gocloud Azure variable (checked against `azureblob.go:31-49,189-195,319-324`); see new minor 2 for the URL option names |
| 13 | Resolved | `httptest` stubs through each SDK's endpoint override under `-short` |

Minors 14-20 are accepted in the plan as stated; the section 7 outcomes match what the steps now say.

## New findings

### New major 1. 3e switches every behaviour at once, so "own commit per behaviour change" and "green unchanged" cannot both hold

- **Step:** 3e, 3f, 3g, section 2 last paragraph, section 5.
- **Evidence:** 3e makes the builder return fetching sync providers instead of `file.Sync`, `http.Sync` and `blob.Sync`. Everything the fetching provider does differently takes effect in that commit: no exit on a failed first fetch, dedupe of unchanged file content, `{}` counted as a payload, read errors keeping flags, the fetch deadline, `incrementalUpdates` honoured. Yet 3e's check says the characterization tests are "all green unchanged", and 3g then lists the same behaviours as separate follow-up commits that "update their characterization row". Both cannot be true. The existing tests also assert concrete types that no longer exist after 3e: `require.IsType(t, &file.Sync{}, got)` (`sync/builder/syncbuilder_test.go:71,89,249,274`), `syncs[0].(*blob.Sync)` reading `Bucket`, `Object` and `Interval` (`sync/builder/blob_sources_test.go:105`). The PR 1 rows for http, file and blob drive the old `Sync` types (`NewHTTP`, `NewFileSync`), which 3f deletes, so after 3f nothing pins the new path's behaviour and the 3g rows have no test left to update.
- **Why it matters:** the characterization tests are the proof that C-003 holds, and section 5's reviewability argument depends on the order of 3e-3g.
- **Fix:** give the fetching provider explicit switches for the changed behaviours (legacy mode: error on first fetch, redeliver every signal, `{}` on read error, no deadline, `incrementalUpdates` ignored). 3e uses legacy mode so the PR 1 rows pass through the new path, and each 3g commit flips one switch and updates its row. Port the PR 1 rows from the old types to the public entry point (`SyncsFromConfig` plus a fed source), and name the existing builder tests that 3e rewrites (the type assertions) instead of calling them "unchanged".

### New major 2. Extension fetchers get no default fetch trigger

- **Step:** Q6, 3e, 4a.
- **Evidence:** UC-003 step 8 says that for a fetcher "the Operator attaches fetch triggers to each of its sources, the core's poller by default". Q6 and 3e attach defaults only for the official providers (the poller for `http`, `gcs`, `azblob` and `s3`; the file watch for `file`). A source handled by an extension fetcher with no `triggers` list gets no trigger: it is fetched once and never again. `TestExtensionSchemeSelected` checks only that the scheme resolves, and `TestExtensionTriggerAttached` uses a `file` source.
- **Fix:** the rule is "no `triggers` means the core's poller (interval and seed from the source), unless the factory declares its own default" (`file` declares the file watch). Add a case to `TestExtensionSchemeSelected` that an `example://` source without `triggers` is refetched on the interval.

### New major 3. Two streaming gaps have no owner, which blocks the `Implemented` status the plan asks for

- **Step:** section 1 (known contract gaps), 4e.
- **Evidence:** `CLAUDE.md` lets a use case move to `Implemented` only if every missing step or rule "is listed as an accepted gap naming the use case that delivers it". Section 1 lists two gaps that "have no owner": the operator's reconnect case and the operator's deletion behaviour. The reason given for the first, "a fake dynamic client cannot lose its watch", is doubtful: the fake dynamic client accepts a watch reactor, and stopping a `watch.FakeWatcher` ends the watch, which makes the informer relist; that is the reconnect case of UC-008's streaming row ("reconnects after losing the source"). Deletion is behaviour the operator provider implements (`DefaultEventTypeDelete` is logged and delivers nothing, `kubernetes_sync.go` `watcher`), not a library property.
- **Fix:** implement the reconnect case with a watch reactor (then no gap), and state in UC-003 what a deleted resource does (keep the last flags, as today); or name an owning use case for each gap. Do not move UC-003 to `Implemented` with ownerless gaps.

### New minor 1. Q2 is incomplete for two providers

The gRPC provider's first `SyncFlags` call also returns an error that stops flagd (`sync/flagd-grpc/grpc_sync.go`, "unable to sync flags" before the retry loop), but 3g's Q2 line lists only http, blob and the operator. For the operator, staying up after a failed first fetch needs the informer to start before the first delivery (today it starts in `notify`, after the initial fetch): a restructuring that neither 3d ("behaviour unchanged") nor 3g describes or tests. Add both to the Q2 commit with a test each.

### New minor 2. Option names in 5c and 5d

- 5c: gocloud's Azure URL options are `domain`, `protocol`, `cdn`, `localemu` and `storage_account` (`azureblob.go:211-227`), which differ from the variable names (`AZURE_STORAGE_IS_CDN`, `AZURE_STORAGE_IS_LOCAL_EMULATOR`). "The URL options carry the same names" is wrong for two of them, and `storage_account` is missing. List the URL options exactly.
- 5d: `disableSSL`, `disable_ssl` and `s3_force_path_style` belong to gocloud's AWS SDK v1 path (`aws/aws.go:98-104`); the v2 path used here rejects them as unknown (`aws.go:259`). Accepting them would be a superset of today's behaviour; drop them or say the superset is intended.

### New minor 3. `ParseSyncProviderURIs` is called before any factory exists

`flagd/cmd/start.go:184` calls the package-level `syncbuilder.ParseSyncProviderURIs(uris)` (and `ParseSources` at `:191`) long before `NewSyncBuilder()` exists (`flagd/pkg/runtime/from_config.go:260`; `flagd-proxy/pkg/service/subscriptions/manager.go:57`). 3e says "`ParseSyncProviderURIs` asks each factory's `ParseURI`" but touches only `from_config.go`. The factory list has to reach `start.go` (a `SyncFactories` field in the runtime configuration, like `ConfigurationFormats`) and the function needs a factories parameter. Name `flagd/cmd/start.go` and the configuration field in 3e.

## Summary

Resolved: the blocker and all 12 majors. New: 0 blockers, 3 majors (3e sequencing and tests, default trigger for extension fetchers, ownerless streaming gaps), 3 minors.
