# UC-008 opposer findings

Plan: `.claude/plans/uc-008.md` revision 1 (uncommitted in the main checkout). The plan names 1536d3c as its base; the code read here is `origin/main` @ 43742b6. The open PRs were read from their branches:

| PR | Branch |
|---|---|
| #67 | `uc-005/pr-2-middleware-factory` |
| #69 | `uc-020/pr-2-event-sink-pipeline` |
| #68, #70, #71 | |

The testbed was read at the submodule pin 9d8dace from a local read-only clone. Go commands ran in WSL with `GOTOOLCHAIN=go1.27.3`, in a scratch directory.

**Result: 0 blockers, 6 major, 5 minor.** The direction holds:
- one report shape in a light `core/pkg/contracttest`;
- typed `Inputs` per runner;
- no dispatcher over `any`;
- declaration checks everywhere.

The majors are about four things:
- what the external run can actually prove;
- the testbed copy, which has no licence to carry;
- the opt-out flags, which are not opt-outs;
- PR 1's goldens, which can't be taken as specified for two runners, and the slicing, which doesn't stack ahead.

## Findings

### 1. major: the external run replaces core with the checkout, so it can't catch the failure it's meant for, and a real third-party path already exists

- **Plan step:** Q5 and 3a: copy the example module to a temporary directory, then `go mod edit -replace` `R/core` with the checkout. Also 4c, where the testkit runs "outside the repository".
- **Evidence:**
  - The risk the plan names is `evaluator-testkit` finding the features through `filepath.Dir(file)/../../test-harness`. That path is computed from the **runner's** source file, not the caller's.
  - With a replace to the checkout, the runner's source stays in the checkout. The relative path still resolves, and the run passes falsely. 4c inherits the same flaw.
  - A third party can already use the real path. The repository is public (`gh repo view … --json visibility` → `PUBLIC`), and `core/go.mod` requires no other module of this repository.
  - In a scratch module outside the checkout, `GOWORK=off go get github.com/open-feature-forking/openfeature-relay/core@43742b6` resolved `v0.0.0-20261103173017-43742b6f088a`.
  - A test there importing `core/pkg/sync/synctest` and `core/pkg/configformat/formattest` passed `go vet`, and `go mod graph` held 0 OTel, gRPC, connect, k8s or gocloud modules.
- **Fix:**
  - Make the module cache the default for `test-contract-external`: `GOWORK=off GOPROXY=direct GONOSUMDB=github.com/open-feature-forking/* go get R/core@$(git rev-parse HEAD)` in the copied module, with no replace. That needs the commit pushed, which CI's commits always are.
  - Fall back to a replace onto a read-only `git archive` copy of core outside the checkout, for local runs of unpushed commits.
  - Either way, the runner's sources sit outside the checkout and without submodules. That is what exposes relative paths.
  - Drop the "UC-012 tags core" gap: a pseudo-version is the tag's stand-in already.
  - For 4c the testkit module itself must also come from a pseudo-version, not a replace.

### 2. major: the testbed has no licence to carry, so the embedded copy (Q4, 4b) has no basis

- **Plan step:** Q4 ("The copy carries the testbed's licence and notices") and 4b.
- **Evidence:**
  - `git ls-tree --name-only 9d8dace` in flagd-testbed lists no `LICENSE`, `NOTICE` or `COPYING`.
  - `gh api repos/open-feature/flagd-testbed/license` returns `404 Not Found`, so GitHub detects no licence on the default branch either.
  - This repository has its own `LICENSE`, which doesn't cover the testbed's files. Copying them into a public repository and a module meant for download is redistribution without a stated grant.
  - There is a cleaner route in progress upstream. An unmerged testbed branch, `feat/go-embed-module` (a0e6d08), makes the testbed the Go module `github.com/open-feature/flagd-testbed/v3`, exposing the assets through `embed.FS` (`testbed.Gherkin()`, `testbed.FS()`). go-sdk-contrib's local checkout already requires it as a pseudo-version (`tests/flagd/go.mod:7`).
  - On that branch the embed list is `docker-compose.yaml version.txt gherkin flags ssl/…`. `evaluator/` isn't embedded, and the branch predates the `evaluator/` section (it sits on 3.10.1).
- **Fix:** take Q4 to the project lead as a licence and containment question, not an implementer check in 4b. The options:
  - (a) **Recommended:** depend on the testbed's Go module once it ships `evaluator/`. That needs the upstream change and a release, both outside this repository's containment, so they're the project lead's call.
  - (b) Keep the testkit in-repo only, and name the third-party case as a gap owned by UC-012 (the plan's alternative).
  - (c) Copy only after the testbed has a licence file.

  Until then, BR-002's "when paired with flagd's format" case stays runnable only inside this repository, and UC-008 says so.

### 3. major: `NoConfig`, `NoBackend` and `NeverAnswers` aren't opt-outs, so mapping them to "not applicable" drops assertions

- **Plan step:** Q3 ("`NoConfig`, `NoBackend`, `NeverAnswers` and the silent skips go; their deprecated fields map onto `Without`") and 2c.
- **Evidence:** each flag switches a case to a **different assertion**; it doesn't skip the case.
  - **`middlewaretest` `NoConfig`** (#67, middlewaretest.go:557-575, `invalidConfig`): with the flag set, the case asserts "a middleware that takes no configuration is created from any configuration". It calls `Create` with an unknown key and fails if that errors.
  - **`middlewaretest` `NeverAnswers`** changes the expectations of the answering and concurrency cases (:363, :402, :484, :517).
  - **`telemetrytest` `NoBackend`** switches branches at telemetrytest.go:300 and :417.
  - **`eventsinktest` `NoConfig`** does the same (#69, :532).

  Reporting these cases as "not applicable (Without: …)" removes checks that pass today. PR 1's goldens record names and failing sets, so they would show the change only as the "named not-applicable lines" the plan pre-approves.
- **Fix:**
  - Separate two things in Q3:
    - **declared modes**, which change a case's expectation (`NoConfig`, `NoBackend`, `NeverAnswers`). They stay as typed mode fields, or `Without` entries whose cases still run with the alternate assertion and report it in the case's `Expected` text.
    - **true opt-outs**, which today are `formattest`'s and `evaluatortest`'s silent skips and `servicetest`'s `Without`.
  - Add a golden column for each case's expected text, so a swapped assertion shows.
  - The UC-008 amendment in Q3 then only covers true opt-outs.

### 4. major: PR 1's goldens can't be taken for `formattest` and `evaluatortest` the way 1a describes

- **Plan step:** 1a ("through a recording `testing.TB` where the runner does not return a report"); section 3 ("the case names in order").
- **Evidence:**
  - **`formattest.Run`** names its cases only through subtests, and only when `t` is a `*testing.T` (formattest.go:75-81). A recording `testing.TB` takes the other branch, where cases run unnamed and only failures produce text.
  - **`evaluatortest.Run`** has no case list at all. Cases are method calls (contract.go:172-190), and a case's name appears only as the first argument of `expect` when it fails. A passing run writes nothing.
  - So "case names in order" can't be recorded for either without changing the runner, which PR 1 (test-only) doesn't allow.
- **Fix:**
  - For `formattest`, take the golden from a real run: `go test -json -run TestContract ./configformat/flagd/...` lists the subtest names. A test can call `testing.T.Run` and collect names through a wrapping parent test.
  - For `evaluatortest`, characterize through its existing broken-stub meta-tests (contract_test.go:192, 212). That pins which stub fails which `expect` name. Take the name inventory in PR 2, in the commit that introduces the case list, and review it against the code there.
  - Say in section 3 that these two inventories start in PR 2.

### 5. major: the slicing waits for merges instead of stacking ahead, which CLAUDE.md now requires

- **Plan step:** base paragraph ("PR 2 starts after #67 and #69 are merged"), 1b ("as soon as #67 and #69 are merged"), risk "Open PRs in flight" ("PR 2 waits for both to merge rather than rebasing them"), section 7.
- **Evidence:**
  - CLAUDE.md:23: "Stack ahead instead of waiting for merges. When a slice builds on another, the implementer cuts it from the parent's branch as soon as that branch exists…"
  - #67 and #69 exist now (`gh pr list`: `67 uc-005/pr-2-middleware-factory -> main`, `69 uc-020/pr-2-event-sink-pipeline -> main`).
  - PR 2 as sliced has three parents (PR 1, #67 and #69), and PR 5 has two (PR 3, PR 4). A PR has one base.
- **Fix:** slice so that each piece has one parent:
  - **PR 1:** goldens for the five merged runners, base `main`.
  - **1b-m and 1b-e:** inventory tests as commits on #67's and #69's branches, or as PRs stacked on them.
  - **PR 2a:** `contracttest` plus the five merged runners, base PR 1.
  - **PR 2b and 2c:** `middlewaretest` and `eventsinktest` conversions, each stacked on #67 and #69, with `contracttest` merged in from 2a by a merge commit, as the CLAUDE.md rule describes.
  - **PR 4:** stack on 2a. It needs only `contracttest.Report` and the testkit move.
  - **PR 5:** stacked on 4, with 3 merged in.

  Every piece then starts now.

### 6. major: 3b underestimates which tests need the flagd module, and moving them into `flagd/pkg/runtime` takes coverage away from the use cases that cite it

- **Plan step:** 3b ("It requires the flagd module (its UC-004 5c test endpoint)… Move that test into `flagd/pkg/runtime`, so the module requires only `R/core`").
- **Evidence:**
  - `git grep` for `open-feature/flagd/flagd/` and `configformat/flagd"` in `test/configformat-example` on main finds `engine_test.go` and `keyvalue_test.go` as well as `endpoint_test.go`. Those hold:
    - `TestEngineServesBothFormats` and `TestShadowedKeys`: UC-007's A5 and shadowed-key coverage.
    - `TestTryInDistribution` and `TestSourceWithoutFormatIsRejectedByFlagd`: UC-018's A2 and A3, which UC-018 runs "in the Extension Author's module".
  - The module also requires `R/configformat/flagd` directly.
  - `test/inprocess-example/engine` requires `test/configformat-example` (go.mod), so the graph question reaches it too.
  - The plan cites BR-001 for this, but UC-008's BR-001 is "one contract per component type". The rule meant is UC-007 BR-009 (dependency boundary).
- **Fix:**
  - Move the distribution-level tests to a sibling test module, for example `test/configformat-example/distribution`, so they stay in the extension's tree, not in flagd's.
  - The engine module then requires only `R/core`, and joins 3a's list.
  - Record the move in UC-007's and UC-018's coverage notes.
  - Cite UC-007 BR-009.

### 7. minor: making `evaluatortest.T` an alias of `testing.TB` breaks its own meta-tests and any caller's fake

- **Plan step:** 2e ("`evaluatortest.T` stays as an alias of `testing.TB`").
- **Evidence:**
  - `evaluatortest.T` is a four-method interface (contract.go:27-32).
  - `contract_test.go:18-30` implements it with a `fakeTB`. `testing.TB` has an unexported method, so no type outside `testing` implements it unless it embeds one.
- **Fix:** keep `T` as the interface. `*testing.T` and `testing.TB` already satisfy it, so `Check(t testing.TB, …)` can still take a `testing.TB`. Or say that the fakes must embed a `testing.TB`.

### 8. minor: the base and the PR list are stale, and two signatures in Q2 are wrong

- **Plan step:** base paragraph, Q2.
- **Evidence:**
  - `origin/main` is 43742b6. #65 (UC-004 PR 5) is merged, but the plan lists it as open.
  - `telemetrytest.Check` already takes `t testing.TB` (telemetrytest.go:136); Q2 lists it as `Check(f, in)`.
  - `middlewaretest.Check(f, in)` (#67, :134) is the one without `t`.
- **Fix:** rebase the facts on the current main, and correct Q2's table.

### 9. minor: `contracttest.ValidateWithout(features, supplied map[Feature]bool)` doesn't fit per-runner typed features

- **Plan step:** 2a, Q3 ("its own typed `Feature` constants").
- **Evidence:** `servicetest.Feature` is its own `string` type (servicetest.go:66). Seven runners with seven `Feature` types can't share one `map[contracttest.Feature]bool` signature without conversions at every call.
- **Fix:** make it generic, `ValidateWithout[F ~string](without []F, supplied map[F]bool) error`, or define every runner's `Feature` as `contracttest.Feature` with typed constants per package.

### 10. minor: "deprecated for one release" has no release to count from

- **Plan step:** Q2, Q3, risk C-004 ("stay one release as deprecated wrappers").
- **Evidence:**
  - Nothing is tagged (`git ls-remote --tags origin` lists none), and releases are UC-012's.
  - Every caller of the old signatures and fields is in this repository: the runners' tests, the example modules and #68/#70.
- **Fix:** either migrate every caller in PR 2 and drop the shims (C-004 allows the move), or keep them with an explicit removal trigger, such as "removed in UC-015's slice", instead of "one release".

### 11. minor: the external run's list misses modules that already qualify, and has no rule for new ones

- **Plan step:** 3a.
- **Evidence:**
  - On main, `test/sync-example`, `test/service-example` and `test/telemetry-example` each require only `R/core`, with one relative replace (go.mod survey).
  - The middleware and event sink examples (#68, #70) aren't merged, so 3a's "each added as its PR merges" depends on someone remembering.
- **Fix:** derive the list. Take every `test/*-example` whose `GOWORK=off go list -m all` has no relay module other than `R/core`, and fail if a module meant for the list (marked in a file) doesn't qualify.

## Verified, no finding

- **No dispatcher over `any` (Q2).** Every runner's `Inputs` is a distinct struct, so typed entry points keep compiler help. The rejected dispatcher would indeed fail at run time.
- **`core/pkg/contracttest` stays light.** The runners on main import only `testing`, the standard library and core packages. A package holding `Case`, `Report` and `Need` adds no module to core's graph. NFR-006 and the `core` row of the module-graph boundaries hold.
- **Declaration validators exist as stated** for sync and service (plural) and telemetry (singular, plus #67 and #69). `configformat` and `evaluator` have none; `evaluatortest` checks `Type()` and `ConfigurationFormat()` inline (contract.go:176-177).
- **The trigger case gap (2f) is real.** `synctest`'s trigger contract has no "never fetches or delivers itself" case. UC-003's `TestFailingTriggerRestartedOthersRun` covers the other half.
- **C-003:** no flagd-full code path changes; the slice touches only test runners, example modules and docs.
