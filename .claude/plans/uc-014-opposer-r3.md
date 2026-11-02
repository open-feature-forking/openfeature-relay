# UC-014 opposer findings, revision 3

Plan: `.claude/plans/uc-014.md` revision 3 (main @ 450aefe). Commands ran in WSL Ubuntu with `GOTOOLCHAIN=go1.27.3`. The flagd-testbed submodule was read from a read-only clone at its pinned commit 9d8dace in `/tmp`; the shared checkout was not touched.

**Result: 1 blocker, 3 major, 5 minor.**

## Findings

### 1. blocker: the metrics design misses the gRPC provider's own OTel instruments

- **Plan step:** section 4 ("flagd-full keeps today's telemetry (C-003, no gap)"), step 2 (check: `go list -deps ./core/pkg/sync/grpc` has no `go.opentelemetry.io`), and the `sync/grpc` row of the forbidden lists.
- **Evidence:** besides `syncmetrics.Recorder` and `otelgrpc`, `core/pkg/sync/grpc/client_stream_metrics.go` creates its own OTel instruments:
  - **Instruments:**
    - `feature_flag.flagd.sync.client.stream.active` (`Int64UpDownCounter`)
    - `feature_flag.flagd.sync.client.stream.reconnects` (`Int64Counter`)
  - **Meter:** `github.com/open-feature/flagd/core/pkg/sync/grpc` (`clientStreamMeterName`), taken from `otel.GetMeterProvider()` in `Init` (grpc_sync.go:74).
  - **Recorded at:** grpc_sync.go:167, 173, 188, 191 and 259.
  - **Imports:** `go.opentelemetry.io/otel`, `/attribute`, `/metric` and `/metric/noop`.

  Section 4's `MetricsRecorder` has only `RecordFlagConfigReceived`, and section 2's "Sync metrics today" doesn't mention these instruments.
- **What goes wrong:** step 2's own check can't go green while this file stays. The implementer either deletes the instruments, which breaks C-003 because operators lose the stream-active gauge and the reconnect counter, or invents a design the plan doesn't have.
- **Fix:**
  - Extend the design. Either `MetricsRecorder` gains `RecordStreamOpened`, `RecordStreamClosed` and `RecordReconnect(ctx, uri, selector)`, or `sync/grpc` gets its own small `StreamMetricsRecorder` interface, injected the same way.
  - `telemetry/otel` implements it with scope `github.com/open-feature/flagd/core/pkg/sync/grpc` as a string literal, the same names and descriptions, no unit (see the Prometheus comment in client_stream_metrics.go), and the same dual attribute set.
  - Section 2 and the 1d pin name both scopes.

### 2. major: "no OTel in the module graph" can't hold for anything that requires gRPC

- **Plan step:** the `check-module-boundary` forbidden lists (`sync/grpc`: `go.opentelemetry.io` "in packages and module graph"), and step 5's `TestModuleGraph` (`filegrpc`: "still no OTel").
- **Evidence:**
  - The gRPC library's own `go.mod` requires `go.opentelemetry.io/otel`, `/metric`, `/sdk`, `/sdk/metric` and `/trace` (google.golang.org/grpc go.mod:18-22, the version this repository resolves).
  - Every module that requires gRPC therefore has OTel in its `go mod graph`.
  - At the package level it's clean: `go list -deps google.golang.org/grpc google.golang.org/grpc/credentials google.golang.org/grpc/resolver | grep -c ^go.opentelemetry.io` prints 0. So Q2 is achievable for packages once `syncmetrics`, `client_stream_metrics.go` and `otelgrpc` are gone.
- **Fix:**
  - For `sync/grpc` and `filegrpc`, forbid OTel in the **package** graph (`go list -deps`) only, and say why in the plan.
  - Keep module-graph checks for what a module graph can rule out: `sync/file` and the `file` example against gRPC, OTel, k8s and gocloud; core against everything heavy.
  - If Simon's Q2 meant the module graph, that's impossible with grpc-go, and he has to hear that.

### 3. major: the sync metrics pin (1d) can't observe anything the way it's specified

- **Plan step:** 1d (`TestSyncMetricsCharacterization`: "Install an `sdkmetric.ManualReader` provider globally before building anything… Build the runtime through `FromConfig`").
- **Evidence:**
  - `FromConfig` calls `telemetry.BuildMetricsRecorder` (from_config.go:115). That builds a reader (Prometheus by default, builder.go:146 `prometheus.New()` on the default registerer), and `NewOTelRecorder` then calls `otel.SetMeterProvider(provider)` (metrics.go:242).
  - `syncmetrics.NewRecorder(nil)` (from_config.go:217) and `newClientStreamMetrics(nil)` in the gRPC `Init` read the global *after* that, so they record into flagd's provider, not the test's ManualReader.
  - `FromConfig` also doesn't run the sync providers, so nothing is recorded until the syncs start.
- **Fix:**
  - Let flagd build its own provider and read it through `prometheus.DefaultGatherer`. That is also exactly what operators scrape, which is better for C-003.
  - Start the runtime's syncs (or the runtime on port 0) and wait for one `DataSync` per source.
  - Select series by the two meter scopes and by `rpc.client.*`, not by a `flagd_sync_*` prefix. The OTel names start with `feature_flag.flagd.sync.`, and the Prometheus names with `feature_flag_flagd_sync_`.
  - Call `FromConfig` once per test binary, or give the exporter its own registerer: a second registration on the default registerer collides.

### 4. major: the Q10 runner can't run in CI's `make test` as planned

- **Plan step:** Q10, step 5 (`TestEmbeddedEvaluationSuite` in `test/inprocess-example/file`, `test-inprocess-example` in `test:`).
- **Evidence:**
  - CI's `test` job checks out without submodules (build.yaml:54-66: plain `actions/checkout`, then `make test`). Only two other jobs use `submodules: recursive` (build.yaml:141, 188). `test-harness/gherkin` and `test-harness/flags` are absent there, so the suite fails, or silently skips if written defensively.
  - In the testbed at 9d8dace, `disabled.feature` is tagged `@disabled @in-process @rpc` with no `@file`. A filter on `@file` drops it, even though Q10 lists it.
  - `evaluation.feature` and `targeting.feature` start with `Given an option "cache"… And a stable flagd provider`, and `metadata.feature` uses `a metadata flagd provider`. The runner has to replicate which flag files back each provider kind, which the testbed's launchpad decides (the five files in `flagd-integration-test-harness`, Makefile:87 differ from `metadata-flags.json`).
- **Fix:**
  - Check out submodules in the `test` job, or run the suite in the `integration-test` job, and fail rather than skip when the features are missing.
  - Fix the tag expression explicitly, for example `@in-process && ~@rpc-only && (@file || @disabled)`, or name the scenarios.
  - Pin the step-to-flag-file mapping in the test.

### 5. minor: the interface package needs `ProtoVersionKey`, and flagd uses the implementation's option API

- **Plan step:** section 3 (`core/pkg/evaluator`, "interfaces only: `IEvaluator`, `IResolver`, `AnyValue`, `NewAnyValue`, `mock`"), and step 3.
- **Evidence:**
  - flagd's v1 evaluation service sets `evaluator.ProtoVersionKey` six times (flag_evaluator_v1.go:83, 226, 261, 296, 331, 366). It's defined in json.go next to the implementation.
  - flagd also uses `evaluator.JSONEvaluatorOption` and `WithStrictNumericRange` (from_config.go:147-150) and `NewJSON` (flagd/pkg/evaluation/evaluation.go:11-12).
  - If `ProtoVersionKey` moves with the implementation, the endpoint imports `evaluator/jsonlogic`, which contradicts "endpoints depend on the interface".
- **Fix:** keep `ProtoVersionKey` (and `SelectorMetadataKey` if an endpoint uses it) in `core/pkg/evaluator`, and list them in the table. Note that only `flagd/pkg/evaluation` and `from_config.go` move to `jsonlogic.NewJSON` and the options.

### 6. minor: the pinned-test gate "only import lines change" can't hold for UC-018's table test

- **Plan step:** step 3 and gate "Pinned tests" ("every changed line is an import of `github.com/open-feature/flagd/...`").
- **Evidence:**
  - `core/pkg/evaluator/setstate_characterization_test.go` is `package evaluator` (line 1). Moving it into `evaluator/jsonlogic` changes the package clause.
  - Its call `flagconfigtest.Run(t, flagconfigtest.NewStore, …)` (line 13) changes when `core/internal/flagconfigtest` becomes `flagdtest`. The same applies to `core/pkg/configformat/flagd/flagd_test.go:20`.
  - The phase-1 span tests are fine: they go through `evaluation.NewEvaluator`.
- **Fix:** either import `flagdtest` under the alias `flagconfigtest`, or let the gate also accept the package clause and that one qualifier. Write the exact allowed diff per file into the plan.

### 7. minor: the Dockerfile steps are wrong for PR 4 and incomplete for the build step

- **Plan step:** 4a ("download step binds `./core ./sync ./telemetry ./evaluator ./configformat ./flagd`").
- **Evidence:**
  - `evaluator/` and `configformat/` only exist from PR 5 (4b). In PR 4, a `--mount=type=bind,source=./evaluator` fails the image build.
  - The **build** step also binds only `./core` and `./flagd` (flagd/build.Dockerfile:33-36, profile.Dockerfile:35-36, flagd-proxy/build.Dockerfile:37-39). The replaces to `../sync/...` and `../telemetry/otel` then point at missing directories at compile time.
  - There's no `.dockerignore`, so the build context does contain the new directories.
- **Fix:** in each PR, bind exactly the top-level directories that exist, in both steps of all three Dockerfiles.

### 8. minor: `GO_LIB_MODULE_DIRS` by exclusion picks up the testbed submodule

- **Plan step:** 1b.
- **Evidence:**
  - The testbed at the pinned commit contains `launchpad/go.mod`.
  - With the submodule checked out locally, which the Q10 runner needs, a `find`-based list that excludes only `flagd`, `flagd-proxy`, `test/*` and `.claude` includes `./test-harness/launchpad`. CI's `test` job has no submodule, so `check-package-coverage` counts differ between CI and a developer machine.
  - `ALL_GO_MOD_DIRS` (Makefile:4) already has this problem for `go.work` and lint.
- **Fix:** prune `./test-harness`, `./spec`, `./schemas` and `./playground-app` from both lists, or define `GO_LIB_MODULE_DIRS` as an explicit allowlist (`./core ./sync/* ./telemetry/otel ./evaluator/jsonlogic ./configformat/flagd`).

### 9. minor: flagd-proxy needs no injected telemetry

- **Plan step:** section 4 ("flagd-proxy does the same where it builds gRPC syncs").
- **Evidence:**
  - flagd-proxy builds syncs through `syncbuilder.NewSyncBuilder()` with no recorder (subscriptions/manager.go:57).
  - It imports no telemetry package (its core imports are `fips`, `logger`, `service`, `sync` and `sync/builder`), so it never installs an OTel SDK provider. Its `otelgrpc` handler and stream metrics record into the noop global today, and nothing is observable.
  - Injecting `otelgrpc` there would add the OTel SDK to flagd-proxy's graph for no observable change.
- **Fix:** leave flagd-proxy without injection and say so. Keep the moved recorders nil-receiver safe, as `syncmetrics.Recorder` is today, because a typed-nil pointer stored in the interface would pass a `!= nil` check.

## Verified, no finding

- **The published Go provider keeps compiling.** It pins core v0.19.0 from the proxy, and `test/integration` stays a standalone module with no `replace`. On main, no `go.mod` in `test/integration`, `flagd` or `flagd-proxy` has a replace, and no `uc-014` branch exists yet. Q5's reasoning holds: a core `replace` would apply to v0.19.0 too, and the local core no longer has `pkg/evaluator.NewJSON` or `pkg/sync/file`.
- **"Move, not forward" is right.** A forwarder in core would make core require the new modules, and those require core. The old root module `github.com/open-feature/flagd` (its `go.mod` was deleted in ed2993c, 2023-03-10) never had a file under `sync/`, `evaluator/`, `telemetry/` or `configformat/` (`git log --all --name-only`). So the new paths can't be ambiguous with any published module, and revision 2's finding 6 is gone.
- **Core stays light.** Of the packages that stay in core, test files included, only `pkg/evaluator/json.go` and `setstate_characterization_test.go` import a leaving package, and both move. `core/pkg/service` pulls connect only through `Configuration.Options` (iservice.go:35), which only flagd's evaluation `ConnectService` reads (from_config.go:237). flagd's sync service has its own `Options`.
- **The sync providers' OTel today:**
  - `sync/http` imports OTel only through `syncmetrics` (its import list has no OTel).
  - gRPC's own packages import none.
  - Today the stats handler sits on both the default and the override path (grpc_sync.go:83-90), so "`DialOptions` always appended, separate from the override" keeps behaviour.
- **Revision 2's other findings carry over as the plan says:** targets iterating module directories, binding directories in the Dockerfiles (now amended by 7), Q7's replace of core itself (1a), two example modules, per-directory boundary checks, and release-please packages and tags.
