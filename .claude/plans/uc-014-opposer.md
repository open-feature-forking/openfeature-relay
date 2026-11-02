# UC-014 opposer findings

Plan: `.claude/plans/uc-014.md` (main @ dbce7cb). Commands ran in WSL Ubuntu with `GOTOOLCHAIN=go1.27.3`. The carve was tried in a full scratch copy of the repository (`/tmp/relay-carve`, rsync without `.git`/`.claude`): all 11 modules of the plan's design table carved, each with a `go.mod` requiring `core` and its siblings at `v0.0.0-00010101000000-000000000000` with `replace`, and the same requires and replaces in `flagd`, `flagd-proxy` and `test/integration`.

**Result: 0 blockers, 4 major, 4 minor.** The central mechanics work (see "Verified"). The majors are gates and build paths that go silently vacuous, or red, after the carve.

## Findings

### 1. major: from PR 2 on, `make test`, the benchmarks and `test-fips` stop running the carved modules

- **Plan step:** 2 (gate `make test lint`), 3 (`test-core` "if `./core/pkg/...` no longer reaches them…"), and the risk "`go test ./core/pkg/...` in workspace mode".
- **Evidence:**
  - In workspace mode, a directory pattern doesn't descend into a nested workspace module. In the scratch copy after the carve, `go list ./core/pkg/...` returns **13** packages; today `cd core && go list ./...` returns 33.
  - `go list ./core/... | grep -c core/pkg/evaluator$` prints 0.
  - A toy reproduction shows the same: `go list ./core/...` leaves out `core/pkg/ev`.
- **Affected targets:**
  - `test-core` (Makefile:56-57, `./core/pkg/...`), run in CI by `make test` (build.yaml:66).
  - `flagd-benchmark-test` (Makefile:85-86, `./core/...`), which loses every evaluator benchmark. The NFR-001 gate passes with nothing measured.
  - `test-fips` (Makefile:72-73, `./core/...`, CI build.yaml:87), which loses `sync/grpc/credentials`, the TLS path that docs/reference/fips-140-3.md:126 lists.
- **What goes wrong:** PR 2 carves the sync providers, `service` and `telemetry`, so its own green `make test` already skips their tests. The plan names the fix only for PR 3, and only as an "if".
- **Fix:** in PR 2, have each of these targets list every module directory explicitly, for example a `CORE_MODULE_DIRS` variable derived from `ALL_GO_MOD_DIRS` under `./core`. Add a gate that compares the package count `make test-core` reaches with the 33 of `cd core && go list ./...` on the branch point. NFR-001 can then check that `benchmark.txt` contains `core/pkg/evaluator`.

### 2. major: the Dockerfiles' download layer fails, and the plan's risk text says the opposite

- **Plan step:** 2 (Dockerfiles), and the risk "Dockerfiles… the replaces still resolve them because the build context is the repository".
- **Evidence:**
  - flagd/build.Dockerfile:21-27, profile.Dockerfile:23-27 and flagd-proxy/build.Dockerfile:23-29 bind-mount **only** `core/go.mod`, `core/go.sum`, `flagd/go.mod` and `flagd/go.sum` (plus flagd-proxy's own) for `go work init … && go mod download`. Only the later build step binds all of `./core`.
  - Reproduced with exactly those four files and flagd's new replaces:
    ```text
    go: github.com/open-feature/flagd/core/pkg/evaluator@v0.0.0-00010101000000-000000000000 (replaced by ./core/pkg/evaluator): reading core/pkg/evaluator/go.mod: open .../core/pkg/evaluator/go.mod: no such file or directory
    ```
  - CI's integration job builds that image (build.yaml:150-170), so acceptance goes red too.
- **Fix:** in PR 2, bind-mount every nested `go.mod`/`go.sum` in all three Dockerfiles, or bind all of `./core` in the download step and accept the weaker cache. Correct the risk text. The image gate catches the failure; the plan should not present it as safe.

### 3. major: the `GOWORK=off` build gate is red on `main` today, and Q7 replaces too little

- **Plan step:** Q7 ("require each new module… builds work with and without `go.work`"), and the Out-of-workspace gate for `flagd`.
- **Evidence:**
  - `cd flagd && GOWORK=off go build ./...` on untouched main fails with `go: updates to go.mod needed`.
  - flagd/go.mod:15 requires the published `core v0.19.0`, which has no `core/pkg/configformat`. flagd imports that package (pkg/runtime/from_config.go, runtime.go).
  - `flagd-proxy` and `test/integration` pass today.
  - Replaces in a dependency's `go.mod` don't apply to the main module. A consumer therefore needs a `replace` for every module in its transitive build list, not only for the ones it imports. In the scratch copy, `flagd` needed all 11, because `sync/builder` pulls every sync module and `service` pulls `evaluator` and `configformat/flagd`. Without a replace, Go tries to fetch `v0.0.0-00010101000000-000000000000` from the proxy.
- **Fix:**
  - Q7 also adds `replace github.com/open-feature/flagd/core => ../core` to `flagd` and `flagd-proxy`, in PR 1 or PR 2.
  - The rule becomes "replace every carved module in the build list, and core itself".
  - The plan records that the gate is red on the branch point for `flagd`.
  - With these replaces, the scratch copy passes `GOWORK=off go build ./...` in `flagd` and `flagd-proxy` and `go vet ./...` in `test/integration`.

### 4. major: the example can't prove the module-graph postcondition as designed

- **Plan step:** 4 (`test/inprocess-example`, "`sync/grpc` only in a second package"; `TestEmbedDependencies` uses `go list -deps`).
- **Evidence:**
  - UC-014's success postcondition is about the provider's **module graph**: "no telemetry provider, service endpoint or other sync provider", and FR-042.
  - A Go module graph belongs to the module, not to one package. With `sync/grpc` required by a second package of the same module, the module graph of the "file-only" provider contains the gRPC sync module and gRPC.
  - `go list -deps` checks the package graph, which is NFR-005's per-package view, not the postcondition.
- **Fix:**
  - Make it two example modules: `test/inprocess-example/file` and `.../filegrpc`.
  - Assert on `GOWORK=off go mod graph` of each. The file-only module must show no gRPC sync module, no `google.golang.org/grpc`, no `service` or `telemetry` module, and OTel only via `syncmetrics` (Q2).
  - Keep `go list -deps` as an extra check.
  - Both modules need the full replace set from finding 3, and a test target in `make test` (the UC-018 lesson).

### 5. minor: `check-evaluator-boundary` goes silently vacuous for flagd's format, and fails with `GOWORK=off`

- **Plan step:** 1b and 3.
- **Evidence:**
  - Makefile:123 runs `cd core && go list -deps ./pkg/configformat/...`.
  - After PR 3 in the scratch copy, that pattern covers only `configformat` and `formattest`. flagd's format is no longer checked, and the target still exits 0.
  - With `GOWORK=off` the target fails: `main module (github.com/open-feature/flagd/core) does not contain package …/core/pkg/evaluator`.
  - depguard is fine: with lint run per module directory, as `make lint` does through `ALL_GO_MOD_DIRS`, a planted `go.opentelemetry.io/otel` import in the nested evaluator is reported (`depguard: 1`). A single `./core/...` lint run would miss it (`0 issues`).
- **Fix:** have `check-module-boundary` run `go list -deps ./...` and `GOWORK=off go mod graph` *inside* each module directory, and retire the `...` patterns of the old target. `TestBoundaryCatchesForbiddenDependency` should also cover a planted import in an existing embeddable module, not only `testdata/forbidden`.

### 6. minor: the ambiguous-import rule needs to be concrete, and nothing is `go get`-able from here

- **Plan step:** the risk "Ambiguous imports", and Q7.
- **Evidence:**
  - Reproduced with toy modules: a consumer that requires `core v0.19.0` (which still holds `pkg/ev`) and the carved `core/pkg/ev` gets `ambiguous import: found package ex.com/r/core/pkg/ev in multiple modules`.
  - With pseudo-version requirements, nothing forces MVS past the old core. Every carved module requires `core` at `v0.0.0-00010101000000-000000000000`, which is lower than any published core.
  - The module path stays `github.com/open-feature/flagd/...`, so the Go proxy serves upstream `open-feature/flagd`, never this repository. `go get` of the carved modules and per-module tags can't be exercised in the incubator; only `replace` works.
- **Fix:** in the UC-012 handoff, state the rule: each carved module's `go.mod` must require the first core release that no longer contains its packages, and core is released first or together with them. Say in the plan that the precondition "released versions a provider can pin" can't be tested in the incubator at all.

### 7. minor: the directory layout fixes release names now, not in UC-012

- **Plan step:** Leaves out ("no release, tag…"), and named gap UC-012.
- **Evidence:**
  - Go requires a nested module's tags to carry its directory: `core/pkg/evaluator/v0.1.0`, `core/pkg/sync/grpc/v0.1.0`, and so on. A v2 changes the module path to `…/v2`.
  - release-please-config.json has one package each for `core`, `flagd` and `flagd-proxy` (with `include-component-in-tag` and `tag-separator: "/"`).
  - release-please attributes a commit to every package whose path contains a changed file. A change in `core/pkg/evaluator/` therefore also bumps `core`, unless `core` excludes the nested paths.
  - The workflow is disabled here, as CLAUDE.md says.
- **Fix:** add a row to the named gaps covering 11 new release-please packages, with `package-name` set to the directory, and `exclude-paths` on `core`. The tag names (eleven `<dir>/vX.Y.Z` series) belong in issue #26 so Simon sees the release cost of Q1.

### 8. minor: the light core's requirements differ from the plan, and NFR-006 may object

- **Plan step:** design table, `core` row ("zap, memdb, uuid, protobuf `structpb`, yaml, sha3").
- **Evidence:**
  - After the full carve, `core/go.mod`'s direct requirements are uuid, go-memdb, `robfig/cron/v3`, testify, zap, protobuf and `gopkg.in/yaml.v3`.
  - SHA3 is the standard library's `crypto/sha3`, not a module.
  - cron is there only for `sync/internal/polling`, which serves the http and blob sync providers. NFR-006 says core depends only on "the modules its data types need".
- **Fix:** correct the table. Add to the spec questions whether the shared poller (and cron with it) may stay in core, or whether it moves with the fetching sync providers in UC-003.

## Verified, no finding

- **Internal packages across nested modules work.** Toy modules build with and without a workspace. In the scratch copy, `GOWORK=off go build ./...` in `core/pkg/sync/http` (which imports `core/pkg/sync/internal/polling`) prints OK. The evaluator's tests, which import `core/internal/flagconfigtest`, pass. The plan's first risk and step 2's "stop and report" don't trigger.
- **Workspace plus replaces coexist.** `make workspace-init` puts the nested modules into `go.work` (17 `use` entries), and `go build ./flagd/...` passes alongside the consumers' `replace`s.
- **Module graphs after the full carve:**

  | Module | Command | Result |
  |---|---|---|
  | `core` | `GOWORK=off go mod graph`, NFR-006 grep (OTel, gRPC, connect, k8s, gocloud, cloud SDKs) plus jsonlogic | empty |
  | `core/pkg/evaluator` | `go mod graph`, NFR-005 grep | empty |
  | `core/pkg/configformat/flagd` | `go mod graph`, NFR-005 grep plus jsonlogic and the evaluator | empty |

  Every core package outside the plan's carve list is accounted for in its light-core row. None pulls a heavy module, including through tests (`go list -deps -test`).
- **Core importers line up with the PR order.** `service/ofrep` is the only core importer of `evaluator`, `evaluator` the only importer of `configformat/flagd`, and `sync/builder` the only importer of `sync/file` and `sync/grpc`. Carving `service` and `sync/builder` in PR 2 before the evaluator in PR 3 is therefore consistent, and `core` tidies light.
- **PR 1 step 1a works.** With `replace github.com/open-feature/flagd/core => ../../core`, the published provider v0.8.0 (`providers/flagd/pkg/service/in_process`) compiles against main's core. `GOWORK=off go vet ./...` exits 0 both before and after the full carve. The replace covers every core version in the graph, so no ambiguity arises.
- **flagd tests stay green in workspace mode after the carve:** `flagd/pkg/runtime` (including the phase-1 span test), `fromconfig_test` and `flag-evaluation`.
- **Q8 holds.** The evaluator module requiring `configformat/flagd` is the allowed direction, and the format's module graph stays free of the evaluator.

Note: during this check a probe I ran with `-mod=mod` and a CR-mangled script briefly modified `flagd/go.mod`/`go.sum` and `core/go.mod` and created scratch files in the checkout. I restored all of it right away (`git checkout`, removed the strays). `git status` is clean apart from this file.
