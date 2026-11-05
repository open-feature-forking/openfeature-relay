# Test coverage gaps: relay vs. upstream flagd

Relay `main` at e45012a5 against upstream flagd at b23f82ec, the commit `main` last merged. Upstream is 3 commits ahead, and one of those commits matters (gap 5).

**Sources.** Package coverage comes from the `test` job logs: relay run 42025577612 (main, cd31bab6, green) and upstream job 124215075065 (b23f82ec). Function-level profiles for the thin packages come from a local `go test -short -covermode=set` run in WSL. CI configuration comes from `.github/workflows/build.yaml` at both commits and from the last manual `build` runs. Spec lint: `spec_lint.py --docs docs` gives 0 errors, 0 warnings and 10 infos. No Docker suite was run locally.

## Ranked gaps

| # | Gap | Evidence | Risk | Suggested test or check |
|---|-----|----------|------|-------------------------|
| 1 | **The upgrade differential skips most behaviour changes.** | The job's trigger pattern covers only Dockerfiles, `distributions/`, `builder/`, `flagd/`, `test/upgrade/`, `test/image/` and `build.yaml`. A PR that changes `core/`, `service/`, `sync/`, `runtime/`, `evaluator/` or `configformat/` changes flagd-full but skips the black-box comparison with today's flagd. | High. The drop-in rule is pinned only on PRs that happen to touch the build, and an endpoint, header or metric drift lands unseen. | Add `\.go$` and `go\.(mod\|sum)$` to the pattern, as the relay testbed job has, or run it on every push to `main`. |
| 2 | **The FIPS 140-3 job is gated and red.** | Upstream runs it on every PR. Here it runs only with `FULL_CI` or a manual run, and it failed in the last three manual runs (41994240327, 41992717953, 41987978938). In each, `runtime/cmd` `TestLoadConfig*` fail with "the test harness is missing", because the job never runs `git submodule update --init test-harness`. | High. `fips140=only` is not checked on any PR, and the job's failures look like noise. | Add the submodule step to the job, and run `make test-fips` on PRs that touch Go code. |
| 3 | **`flagd-proxy/cmd` tests never run in CI.** | `test-module MODULE=flagd-proxy` runs only `./flagd-proxy/pkg/...`, so `flagd-proxy/cmd/cli_characterization_test.go` runs nowhere except in the gated, broken `test-fips`. Of all the directories with `_test.go`, this is the only one that no CI recipe reaches (`test/integration` and `test/testbed` have jobs of their own). | High. flagd-proxy's CLI pin is dead, while UC-012 BR-013 and UC-017 BR-007 keep it a separate program. | Test `./flagd-proxy/...` in the recipe. Add a CI check that every directory with `_test.go` appears in the test log. |
| 4 | **No coverage is tracked, and part of it is never measured.** | The Codecov step fails on every run with "Token required - not valid tokenless upload", and the step stays green. `builder/`, the `test/*-example` modules and `test/release` run without `-cover`. `contrib/*` and `testbed/*` print coverage but write no profile. No job checks coverage against a threshold. | Medium-high. A drop like the ones in the per-package table below stays invisible. | Add `CODECOV_TOKEN`, or keep the profiles as an artifact and compare per-package totals with the base, like `compare-allocs.py`. Add `-cover` to the builder recipe. |
| 5 | **Upstream's port validation is about to be lost in the merge.** | Upstream 3fa8064e (#2107, `fix(cmd)!`) makes a non-numeric `FLAGD_PORT` (and the other port settings) fail at startup, and adds 146 lines to `flagd/cmd/start_test.go`. That file no longer exists here: `runtime/cmd/start.go:320-329` still uses `v.GetUint16`, which turns `tcp://10.0.0.1:8015` into port 0. | Medium-high. The merge will conflict, the test can be dropped quietly, and flagd-full would then bind an ephemeral port where flagd fails. | Port the fix and its table test to `runtime/cmd` when merging. Add a case to `TestStartConfigCharacterization` and to the upgrade differential's `version` scenario. |
| 6 | **Image build and Trivy scan are gated.** | Upstream runs `docker-local` with the Trivy SARIF upload on every PR. Here it runs only with `FULL_CI` or a manual run, and the `image-test` OCI-layout check runs only inside the differential, with gap 1's trigger. | Medium. A dependency bump (`go.mod`) ships an unscanned image. | Run `docker-local` on `go.mod`/`go.sum`/Dockerfile changes and on every push to `main`. |
| 7 | **Outage and gRPC-source suites are manual only.** | `testbed-rpc-outages` (`@reconnect`, `@events`, `@grace` over RPC) and `testbed-grpc-sync` (UC-023: reconnects, a fatal code, a stream deadline) run only with `FULL_CI` or a manual run. The gRPC source job failed in 41992717953. The default integration tags exclude `@reconnect`, `@events` and `@grace` for RPC, as upstream does. | Medium. Reconnect and event-stream behaviour after a restart is the part providers depend on most, and no PR checks it. | Run both jobs nightly on `main`, or on PRs touching `service/`, `sync/flagd-grpc/` or `runtime/`. |
| 8 | **The ConnectRPC event streams of `schema.v1` and `evaluation.v2` are unpinned.** | `service/flag-evaluation`: `OldFlagEvaluationService.EventStream` and `FlagEvaluationServiceV2.EventStream` are at 0.0%, and v1 `EventStream` at 58.3%. The differential's `streams` scenario opens `flagd.evaluation.v1` and `v2`, never the deprecated `schema.v1` stream. | Medium. Old Go, Java and .NET providers still use `schema.v1`, and a regression in its configuration-change events goes unseen. | Table-test both `EventStream` handlers (ready, then a configuration change, then cancel). Add `/schema.v1.Service/EventStream` to `upgrade_test.go` streams. |
| 9 | **OFREP and SSE shutdown paths are thinner than upstream.** | `flagd/pkg/service/flag-evaluation/ofrep` went from 93.6% to `service/ofrep` at 91.5%, and `ofrep/sse` from 95.5% to 92.0%. `factory.Close` and `ofrep_service.Close` are at 0%, SSE `Handler` and `Channel` at 66.7%, and `etag.WriteHeader` at 75%. | Medium. Shutdown with open OFREP event streams is a drop-in surface (`sigterm`), and only the black-box scenario touches it. | Unit-test `Close` with an open SSE subscriber (the stream ends and the goroutine exits), and the `If-None-Match` 304 path through `WriteHeader`. |
| 10 | **Test-harness scenarios that nothing pins.** | They are inherited from upstream and still excluded. RPC: `@unixsocket`, `@targetURI`, `@sync`, `@metadata`, `@customCert`, `@caching`, `@forbidden`. In-process: `@unixsocket`, `@metadata`, `@contextEnrichment`, `@customCert`, `@forbidden`, `@sync-port`, `@sync-payload`. Both: `@fractional-v1` and `@fractional-v2` (#47). UC-022 accepts `@customCert` and `@unixsocket` as manual runs. | Medium. These are flagd behaviours (selector metadata, context enrichment, the sync port, targetURI), and today only unit and characterization tests touch them. | Enable the scenarios the relay testbed can now serve (`@metadata`, `@contextEnrichment`, `@sync-port`, `@sync-payload`, `@targetURI`) in `testbed-suites`, one tag per PR. Run `@unixsocket` in the testbed container. |

## Further gaps (lower risk)

- **Inherited thin packages.** `core/pkg/logger` is at 24.3% (every level method 0%), `sync/flagd-grpc/nameresolvers` (the `envoy://` resolver) at 44.4%, and `flagd-proxy/pkg/service` at 25.5%. Upstream has the same numbers, so these are not regressions. `envoy://` is pinned only through `TestSourceMappingCharacterization`.
- **New code in moved packages.** `core/pkg/evaluator` is at 72.7%: `WithAttributes` and `AttributesFromContext` are at 0% in their own package, and UC-007 A3 says the middleware half is tested with UC-005. In `service/transport` (73.9%), `NewManagementServer`, `SyncServerStreamEnd`, `NoopMetrics` and `WithJSON` are at 0%. In `core/pkg/store`, `FromArgs` and `NewFlags` are at 0%.
- **Relay-only code.**
  - `testbed/cmd` is at 69.6%: the target lifecycle (`Start`, `rebuild`, `Stop`, `Ready`) is 0% in unit tests and runs only in the Docker `Relay testbed` job.
  - `runtime/cmd` is at 77.8%: `initConfig` 15%, `Execute` 0%, `absentSettingsError` 47%.
  - `flagd` (main) reports 0% and is pinned only by its characterization tests.
  - The builder measures 88.1% locally but is never measured in CI.
  - The event sinks (94.6% and 97.9%) and `sync/flagd-grpc` (96.4%) are well covered.
- **CI scripts.** `test_ci_modules_affected.py` runs in no workflow, and only `test_ci_modules.py` does. `render-published-manifest.sh`, `release-build.sh` and `testbed-suites.sh` have no tests of their own; the dry run and the testbed job exercise them.
- **Concurrency.** The step 16 k6 harness is not on `main` yet, and `measure-load.yaml` runs only manually. Nothing runs flagd-full under concurrent load on a PR; `-race` covers only unit tests.
- **Spec traceability.** Lint is clean. UC-022 and UC-023 appear in no test case (infos), so they can't move to `Tested`. The accepted gaps are the after-the-incubator release items of UC-012, UC-019's gaps (#94), UC-022's #194, #195 and #196, and UC-014's fractional exclusion (#47). Every `Test…` name that the use cases, test cases and `surfaces.yaml` cite exists in the code.
- **Fuzzing.** Neither repository has `Fuzz` tests, so this is not a regression. JSON Logic and the configuration format parser are the natural first targets.

## Per-package unit coverage (like for like)

| Upstream package | Upstream | Relay package(s) | Relay |
|------------------|---------:|------------------|------:|
| core/pkg/evaluator | 84.6 | evaluator/flagd · core/pkg/evaluator (new abstraction) | 87.2 · 72.7 |
| core/pkg/store | 84.6 | core/pkg/store | 84.3 ↓ |
| core/pkg/sync/blob | 76.7 | core/pkg/sync/blobfetch · sync/gcs · azblob · s3 | 97.8 · 93.3 · 92.0 · 91.1 |
| core/pkg/sync/builder | 80.8 | sync/builder | 93.4 |
| core/pkg/sync/file | 74.0 | sync/file | 87.0 |
| core/pkg/sync/grpc | 93.4 | sync/flagd-grpc | 96.4 |
| core/pkg/sync/grpc/nameresolvers | 44.4 | sync/flagd-grpc/nameresolvers | 44.4 |
| core/pkg/sync/http | 75.8 | sync/http | 89.7 |
| core/pkg/sync/internal/polling | 80.0 | core/pkg/sync/polling | 100.0 |
| core/pkg/sync/kubernetes | 85.7 | sync/openfeature-operator | 87.0 |
| core/pkg/sync/syncmetrics | 96.7 | core/pkg/sync (metrics.go) | 92.9 |
| core/pkg/telemetry | 87.9 | core/pkg/telemetry · telemetry/otel | 93.4 · 86.3 |
| core/pkg/logger, fips, certreloader, utils, model, notifications, service/ofrep | same | same | same |
| flagd/pkg/runtime | 52.2 | runtime · runtime/cmd | 94.2 · 77.8 |
| flagd/pkg/service | 70.8 | service/transport | 73.9 |
| flagd/pkg/service/flag-evaluation | 73.4 | service/flag-evaluation | 82.1 |
| flagd/pkg/service/flag-evaluation/ofrep | 93.6 | service/ofrep | 91.5 ↓ |
| flagd/pkg/service/flag-evaluation/ofrep/sse | 95.5 | service/ofrep/sse | 92.0 ↓ |
| flagd/pkg/service/flag-sync | 88.0 | service/flag-sync | 88.3 |
| flagd/pkg/service/middleware/{compress,cors,h2c} | 80 / 100 / 100 | service/transport/middleware/… | 80 / 100 / 100 |
| flagd/pkg/service/middleware/metrics | 71.7 | service/transport (metrics.go) · telemetry/otel | see gap list |
| flagd-proxy/pkg/service · subscriptions | 25.5 · 98.2 | same | 25.5 · 98.2 |

Only three packages dropped: `core/pkg/store` (−0.3 points), `service/ofrep` (−2.1) and `service/ofrep/sse` (−3.5).

## Behaviour suites: what each pins

| Suite | Pins | Runs |
|-------|------|------|
| `test/integration` (test-harness Gherkin, released Go provider) | RPC and in-process evaluation, the same tags as upstream | every PR |
| Relay testbed (`testbed-contract-both`, `testbed-suites`) | the launchpad contract; the same scenarios passing against the launchpad image and the relay image | PRs changing Go code |
| `testbed-rpc-outages`, `testbed-grpc-sync` | reconnects, events and grace over RPC; UC-023's gRPC source | `FULL_CI` or a manual run only |
| Contract tests (`core/pkg/*test`, `test/*-example`) | the extension contract of every component type | every PR |
| `test/distributions` | ports, metrics, flagd-sync, export, parity, the Flagsmith distribution, the sync payload | every PR |
| Characterization tests (30, in runtime, runtime/cmd, sync/builder, sync/flagd-grpc, evaluator/flagd, service, flagd, flagd-proxy/cmd) | CLI, configuration, startup log, readiness, middleware chain, selector, sync metrics, tracing, dial options, `SetState` | every PR, except `flagd-proxy/cmd` (gap 3) |
| Upgrade differential (`surfaces.yaml`) | file, http and grpc sources, unreachable source, ConnectRPC, OFREP and flag sync requests and streams, probes and metrics, version, sigterm, flagd-proxy | build changes only (gap 1) |
| Concurrency harness (step 16) | not on `main` | manual (`measure-load`) |

Behaviours that nothing automated pins anymore: none that upstream pinned. Every gap above is either inherited from upstream or a check that is gated or does not run. Gaps 3 and 5 come closest to losing a pin: one runs nowhere, and the other is upstream's newest test, which the next merge drops unless it is ported.
