# UC-001 opposer findings (revision 1)

Plan: `.claude/plans/uc-001.md` revision 1. Sources:
- code: `origin/main` @ 43742b6 and `origin/uc-004/pr-3-runtime-endpoints` (#71), read with `git show`, without checkout;
- spec: `docs/use_cases/UC-001`, `UC-009`, `UC-010`, `UC-011`;
- the rules in `CLAUDE.md` @ 0ad0c16: stack ahead; CI is the full gate run.

**Result: 1 blocker, 3 major, 6 minor.** The manifest, the generator and the Builder CLI (PRs 2 and 4) are sound. The blocker is the plan's central claim: that after PR 5 the runtime imports no component, so the generated `main` alone decides what a binary contains. On #71 the runtime also hard-codes the three official endpoints and flagd's evaluator, and the plan neither moves them out nor checks for them.

## Findings

### 1. blocker: the runtime keeps hard-coded imports of the endpoints and flagd's evaluator, so BR-005 and Q8 fail

- **Plan step:** Q6, Q7, Q8; 5a, 5b, 5e (row `runtime:./...` forbids only `openfeature-relay/(sync|evaluator|configformat|telemetry)/`, `go.opentelemetry.io`, `prometheus`); `TestOnlySelectedComponents`; `TestEndpointCombinations`.
- **Evidence on #71:**
  - **Endpoint settings in the runtime.** `from_config.go:251-272` builds each official endpoint's settings from its concrete types:
    - `case flageval.Type: …flageval.EndpointConfig{…}`
    - `case ofrep.Type: …ofrep.EndpointConfig{Service: ofrep.SvcConfiguration{…}}`
    - `case flagsync.Type: …flagsync.SvcConfigurations{…}`

    `runtime.go:32-33` also keeps `SyncService flagsync.ISyncService` and `OfrepService ofrep.IOfrepService`. After Q7's split, `R/runtime` therefore requires all three endpoint modules. An OFREP-only distribution links flag evaluation and flag sync, and `TestOnlySelectedComponents` fails. The 5e row doesn't forbid `openfeature-relay/service/`, so nothing would catch it.
  - **flagd's evaluator in `pkg/evaluation`, which Q7 moves into `R/runtime`.** Its API is built on `flagdeval.JSON` and `flagdformat.Type`: `NewEvaluator`, `NewRoutedEvaluator(…, opts ...flagdeval.JSONEvaluatorOption)`, and the `json *flagdeval.JSON` field (evaluation.go:21-67). Q6 keeps "`NewRoutedEvaluator`'s `SetState` fallback … for `Runtime` literals", and that fallback **is** the JSON evaluator. `from_config.go:160-163` (`flagdeval.WithStrictNumericRange`) and `:196` (`DefaultFormat: flagdformat.NewFormat(…)`) import `evaluator/flagd` and `configformat/flagd` too. 5a moves the selection to `Components`, but the imports stay, and the 5e row would then fail.
  - **The CLI flags.** `flagd/cmd/start.go` (moving to `runtime/cmd`) maps flagd-full's CLI flags onto exactly these concrete settings. UC-010 step 4 requires every standard distribution to accept "the unchanged start command", and step 5 requires it to check every setting against the components it contains. Settings mapped inside the runtime with the endpoints' Go types can do neither for a distribution that lacks an endpoint.
- **Fix:**
  - Each endpoint factory decodes its own settings from `Settings.Config map[string]any`. `runtime/cmd` puts the flag and file values there under documented keys (the CLI pins 1b keep the names), and the runtime holds no endpoint type.
  - UC-004 3f removes the typed `Runtime` fields.
  - Move the `SetState` fallback and `NewRoutedEvaluator`'s JSON parts out of `R/runtime`: into `evaluator/flagd`, or into a test helper for the UC-018 `Runtime` literals.
  - Extend the 5e row with `openfeature-relay/service/` (except `service/transport`) and the two flagd modules.
  - Add a test that builds an OFREP-only manifest and checks `go version -m`.

### 2. major: the repository's manifests must replace every relay module in the build list, not only the components

- **Plan step:** Q2 (`replaces:` per component), Q9 (`runtime:` redirectable), Q11 (every in-repo manifest uses `v0.0.0` plus `replaces`), 4a (generated `go.mod` "requires, `replace` lines").
- **Evidence:**
  - Relay modules require each other at `v0.0.0-00010101000000-000000000000` and replace locally (UC-014's Q7). Replaces in a dependency's `go.mod` don't apply to the generated main module. Its `go mod tidy` therefore tries to fetch `R/core`, `R/runtime`, `R/service/transport`, `R/sync/builder`, `R/core/...` siblings and so on from the proxy, and fails, unless the generated `go.mod` replaces them all.
  - The plan's example lists only the components, and `replaces` paths are relative to the manifest, while the generated `go.mod` lives in `output_path`.
- **Fix:** state the rule, which is that the generated `go.mod` replaces every `R/...` module in the resolved build list. Either the Builder derives the replacements (a `replace_root: ../..` mapping `R/<dir>` to `<root>/<dir>`), or each manifest lists them all. Rewrite replacement paths relative to `output_path`. Test it with `TestBuildIgnoresWorkspace` on the flagd-full manifest.

### 3. major: the probe breaks cross-compilation and doubles the compile

- **Plan step:** Q5 (`go run ./probe` after resolution), Q13 (the Builder passes `GOOS`, `GOARCH`, `GOFLAGS` and `GOFIPS140` through), Q10 (`CGO_ENABLED=0 -trimpath`), NFR-008 (under 3 min).
- **Evidence:**
  - With `GOOS`/`GOARCH` passed through, `go run ./probe` builds for the target and then can't execute it on the host. A darwin or arm64 build from a linux CI runner fails at the probe. That is UC-010's platform matrix, and the reason Q13 passes the environment.
  - The probe also builds without `-trimpath` and `CGO_ENABLED=0`, so its build-cache entries differ from the final build's. Core and every component (cloud SDKs included) then compile twice, and NFR-008's measurement takes the hit.
- **Fix:** run the probe with the host's `GOOS`/`GOARCH` (`GOOS= GOARCH=` unset) and without `GOFIPS140`. Use the final build's other flags (`-trimpath`, `CGO_ENABLED=0`), so most packages share the cache. Add `TestCrossBuildRunsProbeOnHost`.

### 4. major: the PR slicing waits for merges, against `CLAUDE.md`'s stack-ahead rule

- **Plan step:** section 7 (PR 3 "`main` after #71"; PR 4 "PR 2 with PR 3 merged"; PR 5 "`main` after #71, PR 1, PR 3, step 6 T4"; PR 6 "PR 4 with PR 5").
- **Evidence:** `CLAUDE.md` (0ad0c16, The loop) says the implementer "cuts it from the parent's branch as soon as that branch exists", review runs on every PR of a stack at once, and new parent heads are merged upward. PRs 3–6 instead wait for merges, and PRs 4 and 6 have two parents each, which one PR base can't express.
- **Fix:**
  - Give one parent branch per PR:
    - PR 3 on #71's branch;
    - PR 4 on PR 3 (PR 2 merged in, or PR 2 under PR 3);
    - PR 5 on PR 4 (or PR 3), with T4's branch merged in;
    - PR 6 on PR 5.
  - Name the merge-in for every second parent. That keeps everything startable once its parent branch exists.
  - Change the gates table to the new split: CI runs `make test`, lint, coverage, boundaries and integration. Locally, only targeted tests and the gates CI doesn't run (benchmarks, the `x/exp` and evaluator-boundary greps, spec lint, and `builder-e2e` if it's not a CI job yet).

### 5. minor: `flagd version` mixes the release version and the manifest version

- **Plan step:** Q10.
- **Evidence:**
  - UC-009 step 8: `flagd version` names "flagd-full, its manifest version **and the target release**".
  - UC-011 BR-004: the components listing uses the same identity as `version`.
  - Q10 stamps one `version` through `-ldflags`, and adds `distribution: <name> <manifest version>`. It doesn't say whether the first line's `<version>` is the manifest version or the relay release. For flagd-full they can differ, for example manifest 1.0.0 built on runtime v0.14.0.
- **Fix:** first line `flagd: <runtime release version> (<revision>) …` (C-003, scripts parse it), and `distribution: <name> <manifest version>`. `flagd components` reports both.

### 6. minor: UC-011's module versions should come from the binary, not from the manifest

- **Plan step:** Q8 (`Inventory []ComponentInfo{Group, Type, Module, Version}`, generated), and section 2 (UC-011).
- **Evidence:**
  - With `replaces`, which every repository manifest uses (Q11), the generated version is `v0.0.0`.
  - UC-011 step 5 shows "the component module and version it comes from".
  - `runtime/debug.ReadBuildInfo()` holds the version actually linked, including replacements.
- **Fix:** generate `Group`, `Type` and `Module`, and fill `Version` (and replacement) from the build info at runtime. Add a test with a replaced module.

### 7. minor: BR-005's `go version -m` check needs an allowlist of the non-component relay modules

- **Plan step:** `TestOnlySelectedComponents` ("exactly the selected modules plus runtime and core").
- **Evidence:** a binary also links `R/service/transport` (Q7), `R/sync/builder` (5b keeps dispatch there; `start.go` uses `syncbuilder.ParseSources` and `ParseSyncProviderURIs`), and core's own sub-packages.
- **Fix:** list the infrastructure modules allowed besides the selected components, and fail on any other `R/...` module.

### 8. minor: `sync/builder` keeps every sync module in its module graph through its tests

- **Plan step:** 5b (`sync/builder` keeps only dispatch), and Q8's "transitive boundary row".
- **Evidence:** UC-003's pins in `sync/builder` (`blob_sources_test.go`, `file_delivery_test.go`, `http_delivery_test.go`, `source_mapping_test.go`, `triggers_test.go` on #71) import the sync modules. Test imports keep them in `sync/builder/go.mod`, so `R/runtime`'s `go mod graph` contains every sync module and the cloud SDKs, even when no package links them.
- **Fix:** make the runtime row a **package** row (`go list -deps`), which is what BR-005 needs. Or move those pins into a test module, if a module-graph row is wanted.

### 9. minor: `NewFactories()` without arguments depends on step 6's T4 for flagd-grpc's telemetry

- **Plan step:** Q3, 5b.
- **Evidence:** on #71 the runtime injects into the official sync list (from_config.go:303-306):
  - `syncbuilder.OfficialWith(OfficialOptions{StreamMetrics: …, GRPCDialOptions: []googlegrpc.DialOption{telemetry.GRPCClientDialOption()}})`.

  A generated `components.go` calling `flagdgrpc.NewFactories()` has nothing to pass. Stream metrics and gRPC client instrumentation then only survive if T4 has moved them into `sync.Settings` (step 6, T4d).
- **Fix:** name T4d as a precondition of 5b. Run UC-014's `TestSyncMetricsCharacterization` against a runtime built from flagd's `components.go`.

### 10. minor: the gates table prescribes full local runs

- **Plan step:** section 5 ("`make workspace-init && make test` | all", lint, coverage and boundaries "all").
- **Evidence:** `CLAUDE.md`'s "Local runs and CI": CI runs those, and local runs stay targeted.
- **Fix:** as in finding 4.

## Verified, no finding

- **Module paths.** `R/builder` with no relay dependency, `-trimpath`/`CGO_ENABLED=0`, and `GOWORK=off` match how this repository's modules resolve. `flagd/cmd/start.go` on #71 imports only `fips`, `logger`, `sync`, `sync/builder` and the runtime, so moving `Official()` out (5b) is mechanical once finding 1 is resolved.
- **Q6.** It is required for UC-010's flagd-sync (NFR-011) and UC-019. The evidence above confirms that the runtime builds flagd's format and evaluator unconditionally today.
- **UC-009's needs.** `distributions/flagd-full/manifest.yaml`, a hand-written `components.go` equal to the generated one, and the module inventory pin (1c) match UC-009's steps 1–8, apart from the release version in finding 5.
