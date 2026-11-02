# UC-018 opposer findings

Plan: `.claude/plans/uc-018.md`. Code: `main` = `origin/main` @ 722a83d (phase 1 PRs A, B, C1, C2 merged; D still open on `origin/phase-1/pr-d-boundary`). Commands ran in WSL Ubuntu with `GOTOOLCHAIN=go1.27.3`.

**Result: 1 blocker, 4 major, 6 minor.**

## Findings

### 1. blocker: step 7 breaks the runtime tests the plan says stay unchanged, including the phase-1 span pin

- **Plan step:** 7 ("`updateAndEmit` calls the source's format instead of `Evaluator.SetState`"), with the gate "Step-1 runtime test unchanged and green" and the Must-not-change row "Evaluation and flag sync spans | Phase 1 span tests".
- **Evidence:**
  - `flagd/pkg/runtime/runtime_test.go:150-152` drives readiness through a gomock `IEvaluator` with `EXPECT().SetState(...)` three times. Once `updateAndEmit` stops calling `SetState`, gomock fails the test on missing calls.
  - `flagd/pkg/runtime/runtime_span_test.go:99-104` (the merged phase-1 `flagSync` pin) builds `Runtime{Evaluator: evaluation.NewEvaluator(...), ...}` as a struct literal with no format map. After step 7 that runtime has no format for `spanTestSource`: either it panics, or it rejects, or the flags never reach the store.
  - The plan's own `TestUpdateAndEmit` "uses a real `evaluator.NewJSON`" in the same literal style, so it has the same problem.
  - The `flagSync` span currently wraps `r.Evaluator.SetState` (runtime.go:147). Step 7 doesn't say the span moves to wrap the format call.
- **What goes wrong:** the implementer must edit the pinned tests. Then the C-003 proof for readiness, Emit and the `flagSync` span is gone exactly in the PR that changes them.
- **Fix:** before implementation, the plan has to decide one of these:
  - (a) A source without a registered format (including a `Runtime` with a nil map) goes through `Evaluator.SetState`. The default flagd format is then reached through the evaluator's compat wrapper, and every existing literal keeps working.
  - (b) The runtime gets a constructor that the step-1 tests already use in PR 1. The tests then go through that constructor, never a literal, and runtime_test.go's mock test is rewritten in PR 1, against today's code.

  Either way, step 7 has to say that the `flagSync` span (scope `jsonEvaluator`, attr `feature_flag.source`, status `flagSync error`) wraps the format call.

### 2. major: "same table, two runners" can't be built from a `_test.go` file

- **Plan step:** 1 ("the table is a package-level `var`… PR 3 reruns it unchanged through flagd's configuration format") and 5, plus the Characterization gate ("test files byte-identical to PR 1").
- **Evidence:** the table lives in `core/pkg/evaluator/setstate_characterization_test.go`. Identifiers in a `_test.go` file belong to that package's test build only, so `core/pkg/configformat/flagd`'s tests can't import them. Running the table through `flagd.Accept` means either copying it, so it is no longer the same table, or moving it, so PR 1's file is no longer byte-identical.
- **Fix:** put the table and the runner in a non-test package from PR 1, for example `core/internal/flagconfigtest/table.go`. Both `evaluator` and `configformat/flagd` tests import it, and the byte-identical gate covers that file.

### 3. major: `Settings.Store *store.Store` doesn't fit the evaluator's `store.IStore`

- **Plan step:** 3 (`Apply` / `Documents` on `*Store`) and 5 ("`JSON.SetState` becomes `flagd.New(...).Accept` over the evaluator's own store").
- **Evidence:**
  - The evaluator holds `store.IStore` (json.go:77, 83), and `IStore` has only `Get`, `GetAll`, `Watch` and `Update` (store.go:24-29).
  - `flagd/pkg/evaluation/evaluation.go:11` and the flagd services also pass `IStore`.
  - The compat wrapper therefore can't hand its store to a format that needs `*Store`.
- **Fix:** state the choice in the plan, and check that the in-process provider compiles under it:
  - (a) `SetState` type-asserts to `*store.Store` and keeps today's `Update` path otherwise. This is safe for external `IStore` implementers.
  - (b) `IStore` gains `Apply` and `Documents`. This breaks any external implementer; core is v0, but it needs a `!`.

  The in-process provider uses `*store.Store` from `store.NewFlags()` (go-sdk-contrib in_process/service.go:39, 145), so it compiles under either choice.

### 4. major: the `flagSetId` derivation can't simply "move"; it ends up in two places

- **Plan step:** 3 ("`Update` stays as a wrapper for existing callers"), 5 ("derives flag sets as the store does today"), and risk C-003 ("The `flagSetId` derivation moves from `store.Update` into flagd's format").
- **Evidence:**
  - `store.Update` derives `flagSetId`, priority and patched metadata (store.go:241-262).
  - `configformat/flagd` imports `store`, so `store.Update` can't call the format without an import cycle.
  - `Update` keeps callers: `store_test.go`, `fractional_test.go`, `hot_path_logging_test.go`, `utils_test.go`, `ofrep/sse/*_test.go` and `flag-sync/handler_test.go`. So the derivation stays in `store` *and* gets written again in the format.
  - Flags without a `flagSetId` get `nilFlagSetId`, a UUID generated per process (query.go:29). If the format "derives as the store does", it reports a random flag set name that differs per replica and per restart (BR-008: the flag set is a reported fact).
- **Fix:**
  - Keep a single derivation in `store`, exported as a helper (for example `store.FlagSetID(flag, setMetadata)`), and have `Apply` compute rows from flags plus set metadata. The format groups by that helper for its reports.
  - Report the unnamed flag set as `""`, and map it to `nilFlagSetId` only inside the store.
  - Add a case to the step-1 table for flags without `flagSetId` next to ones with it in the same source.

### 5. major: the example extension's tests don't run in CI

- **Plan step:** 8, and the "use case done" rule (every flow has an automated test; reviewer `ready` needs CI green).
- **Evidence:** CI runs `make test` (build.yaml:66). That is `test-core test-flagd test-flagd-proxy` (Makefile:55-61), which covers `./core/pkg/...`, `./flagd/pkg/...` and `./flagd-proxy/pkg/...` only. `ALL_GO_MOD_DIRS` adds the new module to `go.work` and to `make lint`, but no test target runs `test/configformat-example`. The only automated proofs of the main scenario through A5 would therefore never run in CI.
- **Fix:** add a `test-configformat-example` target, put it in `test:`, and name it in the gates table for PR 5. If the module tests at all with `-race`, keep `-race` for parity.

### 6. minor: error text isn't pinned, and which entry gets named isn't deterministic

- **Plan step:** 1 (table records "error or not"), and 5 ("error unchanged").
- **Evidence:**
  - The in-process provider puts `err.Error()` into its `PROVIDER_ERROR` event message (in_process/service.go:335-343), and the runtime logs it. The text is user-visible, yet the table doesn't record it.
  - `validateDefaultVariants` returns at the first bad flag (json.go:564-579). Map-form flags come from Go map iteration (json.go:536-542), so with two faulty flags the named key varies from run to run.
- **Fix:** record the exact error string per table row. Use one faulty entry per case. In the contract, require that the named entry is *one of* the faulty ones, or sort keys in flagd's format and say so.

### 7. minor: NFR-011 (no `jsonlogic`) isn't checked for flagd's configuration format

- **Plan step:** 6.
- **Evidence:**
  - NFR-011 forbids the evaluator and `github.com/diegoholiveira/jsonlogic` in the flagd-sync graph.
  - Step 6 denies only `core/pkg/evaluator` in `configformat`. The boundary grep (`opentelemetry|grpc|connectrpc|k8s.io|gocloud`) doesn't look for `jsonlogic`.
  - Today the format's planned imports are clean: `go list -deps` over `store`, `model`, `logger`, flagd-schemas and jsonschema prints 0 matches for jsonlogic or OTel.
- **Fix:** add `github.com/diegoholiveira/jsonlogic` to the `configformat-no-evaluator` deny list. Have `check-evaluator-boundary` grep `jsonlogic` for `./pkg/configformat/...`. Note that `isSafeRange` (json.go:297), used by `warnUnsafeNumericVariants`, has to move or be copied too.

### 8. minor: Q1 contradicts the glossary; settle it in the spec before code

- **Plan step:** Q1.
- **Evidence:**
  - The glossary says a Document is "the parsed, validated form of one source's flag configuration" (glossary.md:41), which means one per source.
  - The plan's Q1 makes a document one flag set of one source. BR-008 allows formats to map documents to flag sets but doesn't change the definition of a document.
  - Store (glossary.md:28) says evaluators read *documents*. In the plan, flagd's `Document.Value` holds a fingerprint map, while its flags stay in the flags table.
- **Fix:** if the project lead picks Q1's first option, change the glossary first (Document, Store) as a spec commit on `main`, and run the spec gate. Otherwise the code drifts from the contract on the first PR.

### 9. minor: documents claim keys that priority shadows, and one priority case isn't pinned

- **Plan step:** 3 and 5 (reports), and 1 (table).
- **Evidence:**
  - The store's `idIndex` is (flagSetId, key) without the source.
  - A lower-priority write over a higher-priority row is skipped (store.go:320-327).
  - A higher-priority source that later drops the key deletes the shared row, and the lower-priority flag isn't restored until that source resends (store.go:296-312).
  - The plan's documents report `FlagKeys` per source, so a lower-priority document lists keys the flags table doesn't serve from it. UC-007's router will route by those keys.
- **Fix:**
  - Pin "higher-priority source removes a shared key → the key is gone" in the step-1 table. That is today's behaviour, and C-003 keeps it.
  - Record in Leaves out that document `FlagKeys` ignore cross-source priority, and that UC-007 decides how the router resolves it.

### 10. minor: the cost of `SetState` rises with no measurement

- **Plan step:** gate "Benchmarks" ("`SetState` isn't benchmarked").
- **Evidence:** after step 5, every sync (flagd-full and each in-process provider) also marshals every flag, hashes it with SHA-256, compares against the previous fingerprint map and writes documents. `make flagd-benchmark-test` covers evaluation only.
- **Fix:** add `BenchmarkSetState` to PR 1, for example with 1,000 flags, and report allocs/op and ns/op for PR 3 against 722a83d (reported, not gating, as NFR-001 says for ns/op).

### 11. minor: the Kubernetes sync is flagd-specific, so BR-003 doesn't hold for it

- **Plan step:** Leaves out.
- **Evidence:** `kubernetes_sync.go:338` marshals `ff.Spec.FlagSpec` from the FeatureFlag CRD, so it shapes the flag configuration instead of delivering it unchanged (BR-003, UC-003 BR-008). A vendor format can't be sourced from it.
- **Fix:** add one row to Leaves out (UC-003). No code change in this slice.

## Verified, no finding

- **Today's code matches the plan's facts.**
  - `JSON.SetState` is at json.go:120.
  - Schema violations only warn (json.go:497-501).
  - It rejects on unparsable JSON, a bad `$evaluators` block, an unparsable `flags` value or a missing default variant (json.go:491-579).
  - `store.Update` is at store.go:222.
  - `SourceConfig` has no format field (isync.go:44).
  - `updateAndEmit` calls `SetState`, then readiness, then `Emit` (runtime.go:139-170).
  - The provider diffs `GetAll` around `SetState` (in_process/service.go:325-370).
- **Mapping a source to its format by URI works.**
  - Every sync provider sets `DataSync.Source` to the configured URI: file :201, http :107, grpc :142, kubernetes :75, and blob through `bloburi.Join`/`Split`.
  - `store.Update` already panics on an unregistered source, so the identity holds wherever flagd runs today.
  - Config-file sources go through `viper.UnmarshalKey` (start.go:196), and mapstructure matches the `Format` field by name, so the config file can name a format.
- **The boundary is reachable.** None of the flagd format's planned imports (`store`, `model`, `logger`, flagd-schemas, jsonschema) pulls in OTel, gRPC, connect, k8s, gocloud, jsonlogic or the evaluator. `store` adds only uuid and memdb.
- **Evaluator types are free to move.** `evaluator.Definition`, `Evaluators` and `Flags` are used nowhere outside the package, in this repository or in go-sdk-contrib.
- **PR order holds.** PR 3 rightly waits for phase-1 D: `.golangci.yml` and the `Makefile` on `main` have no `evaluator-boundary` rule or `check-evaluator-boundary` target yet.
