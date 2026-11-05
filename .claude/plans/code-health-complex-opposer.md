# Code health, the complex functions: opposer findings (revision 1)

Plan: `.claude/plans/code-health-complex.md` (measured at `348c0233`). Checked on `main` @ 2b28ae9f. Q1 (a) and Q2 (a) are decided: the engine benchmark joins CI, and the random choice between two faults stays.

Checked against the sources, not the plan's summary of them:
- the code: `runtime/from_config.go`, `runtime/endpoints.go`, `core/pkg/evaluator/router/router.go`, `contrib/flagsmith/engine/evaluator.go`, `bench_test.go`, `service/event-collector/collector.go`, `sync/sse/trigger.go`, `sync/s3/s3.go`;
- CI's benchmark comparison: the `benchmark` job in `build.yaml`, `run-benchmarks.sh`, `compare-allocs.py`;
- the open PRs, through `gh pr view --json files`.

Counts: **0 blockers, 4 major, 6 minor.**

## What holds (verified)

- **`FromConfig`'s cleanup analysis is right.**
  - Both `defer`s read the named result `rt` (`from_config.go:113-119`, `:192-201`), and every failure after `:163` is `return nil, err`.
  - They run last-in, first-out, so the endpoints close before telemetry shuts down.
  - `Settings.Formats` is a fresh `append` per endpoint (`:222`).
  - `metricsHandler` is called twice (`:121`, `:167`).
  - The `onShutdown` order is sinks, then telemetry (`:252-257`). These are the only two `onShutdown` calls in the package.
- **The precedence rows of the parsers match the code:**
  - `parseEvent`: `optTime` runs before the `kind` switch, and the fields before the `flagKey` check (`collector.go:274-295`).
  - s3: `ssetype` is checked before `configOptions`. `accelerate` is read before the path style keys. `use_path_style=true&s3ForcePathStyle=false` ends with path style off, because the last key read wins (`s3.go:93-128`).
- **The random choice in the three parsers is real:**
  - `fields` is a map (`collector.go:283`);
  - `parseHeadersFrom`'s outer loops and `refFromStrings` iterate maps (`trigger.go:192-246`);
  - `configOptions` ranges over `url.Values` (`s3.go:164`).

  In `parseHeadersFrom`, `{"env": 1, "foo": "x"}` always reports `env`, because `refFromStrings` only runs after the loop.
- **Flagsmith's tie between flag sets:** at equal priority, the first flag set in sorted order wins (`evaluator.go:138-143`). The plan's test pins the right thing.
- **The CI comparison works for the stack:**
  - The `benchmark` job compares against `pull_request.base.sha`, so B2 and B3 stacked on A2 see A2's new benchmarks on their base.
  - The base is measured with the head's `run-benchmarks.sh`, so adding the engine in A2 benchmarks base and head alike.
  - `contrib/flagsmith/engine` has its own `go.mod`, which `ALL_GO_MOD_DIRS` puts into `go.work`.

## Major

### 1. Three open PRs already change these functions, one of them on the evaluation path, before anything measures it (Scope, Slicing)

- **Evidence:**
  - "No open PR touches the six files" no longer holds. Findings 5–7 were opened at 18:54 today:
    - **#239** (`health/request-keys`) edits `Router.Evaluate` (`router.go:76`) and Flagsmith `Evaluate` (`evaluator.go:86`), deletes both `wantedKeys` and adds `Request.WantedKeys()`;
    - **#238** edits `sync/sse/trigger.go`;
    - **#237** edits `service/event-collector/collector.go`.
  - The two `wantedKeys` the plan says stay where they are are not alike: the router presizes its map (`make(..., len)`), Flagsmith's starts as a literal. #239 merges them on a path where the router's multi-route case has no benchmark and Flagsmith's is not compared. Its allocation change would land unmeasured, which is exactly the hole A2 exists to close.
- **Fix:**
  - Fix the order in the plan: **A2 lands before #239**, or #239 rebases onto A2, so CI compares its allocs/op.
  - B2 and B3 start from `main` after #239, and their target shapes use `req.WantedKeys()`.
  - A3 and B4 likewise take #237 and #238 into account (`parseEvent`'s callers, `parseHeaders`).
  - Update the Scope paragraph.

### 2. The "tests-only parent" gate can be fooled and does not run the parent's tests against the refactor (Characterization first, Gates)

- **Evidence:**
  - `git diff <parent> -- '*_test.go'` "adds lines only" breaks as soon as B merges up from `main`. The merged-in test changes, for example #239's `TestRequestWantedKeys` and its edits, show up as changed lines, so the gate either fails for nothing or gets waived.
  - A glob says nothing about *which* tests pin. A refactor that keeps every line of `router_test.go` but adds a `t.Skip` in a new helper file passes it.
  - "One commit per helper" (B1, B4) is not checked commit by commit.
- **Fix:**
  - **The gate names the pinning files:** what A added, plus the existing files the plan lists ("Pinned today").
  - **The check runs the parent's tests on the refactored code:** take those files from A's head (`git checkout <A> -- <files>`) onto B's head and run `go test -race -count=3` on the package. A clean run is the gate; a diff of those files is only informative.
  - For B1 and B4: `git rebase -x 'go test ./<package>/...' <A>` passes on every commit.

### 3. The new benchmarks join a strict gate that runs on every PR, without a noise measurement (Q1 (a), A2)

- **Evidence:**
  - `compare-allocs.py` fails a PR on +1 alloc/op for any benchmark that is not in `NOISY`. The existing entries were added only after "6 runs of 3" on identical code.
  - With Q1 (a), `BenchmarkEvaluate` runs on **every PR in the repository**, not only on B3. Its identity context is a 4-entry map (`bench_test.go:80`), and `identityOf` ranges over it into the trait slice (`evaluator.go:181-185`). The trait order is therefore random per call, and the engine's segment evaluation may allocate differently.
  - `BenchmarkRouterEvaluate` builds `winners` and `wanted` maps per call.
  - Neither benchmark has been measured for spread.
- **Fix:**
  - A2 runs both benchmarks 6×3 on unchanged code, as the `NOISY` comment records, and adds an entry with its reason where they spread.
  - A2's own CI is run twice before it merges.
  - Without this, a ±1 on the Flagsmith benchmark makes unrelated PRs red.

### 4. "One of" does not pin Q2 (a) (section 4, Risks)

- **Evidence:** the plan pins the two-fault inputs as "the error is one of the possible texts". A refactor that turns a map loop into a slice loop, the very change the Risks section names, still passes. The lead decided to *keep* the random choice, and no test enforces it.
- **Fix:**
  - For each two-fault row, a test calls the parser 1,000 times and requires **both** texts to appear. The odds of seeing only one are about 2⁻¹⁰⁰⁰, so it does not flake.
  - The test's name says it pins a decided quirk, so a later Q2 (b) changes it on purpose.
  - Otherwise the plan should say that only review guards Q2.

## Minor

### 5. `TestFromConfigCallOrder` should record cleanup and one more branch

- **Close and shutdown:** the recording fakes also record `Close` and `Shutdown` on every component. That pins what is *not* released today:
  - sync providers created before a `staleAfter` refusal (`from_config.go:238-246`);
  - the evaluators when middleware fails (`:209-213`).
- **The branch without a router:** a row where no endpoint needs the router. Then no evaluator is created at all (`endpoints.go:41-49`).
- **Two error texts are missing from `TestFromConfigErrorTexts`:**
  - `the telemetry provider declares: …` (`from_config.go:270`);
  - `the service endpoint %q needs the evaluator of the configuration format %q, which the distribution lacks` (`endpoints.go:59`).

### 6. Router rows that are missing

- `Bulk` with `FlagKeys` set: the keys are restricted, the results sorted by flag set and key, and no `FLAG_NOT_FOUND` is filled in. `router.go:127` fills only for `FlagKeys != nil && !Bulk`.
- An evaluator that returns an error **together with** results and metadata. Both are dropped (`:106-114`), and its error results carry `meta`, not `merged`.
- The multi-route path never returns an error itself.

### 7. The Flagsmith sub-benchmarks mostly exist; keep their names

`BenchmarkEvaluate` already has `single/identity`, `single/environment`, `bulk/identity` and `bulk/environment` (`bench_test.go:82-87`); only explicit keys is missing. A benchmark that exists in only one of the outputs is "listed and never fails", so renaming the four in A2 would leave them uncompared for one PR. Add `keys/…` beside them.

### 8. Parser rows that are missing

- **`parseEvent`:**
  - data after the object is accepted, because `json.Decoder` reads the first value only (`collector.go:270-271`);
  - a duplicate key: the last one wins.
- **s3, a repeated parameter:** the first value wins (`values[0]` at `s3.go:165`, `q.Get` in `open`).
- **s3, the deleted keys:** `open` deletes the keys it handled from the same `url.Values` that `configOptions` then rejects unknown keys from. A `clientOptionsOf` that re-parses `u.Query()` would report `unknown query parameter "accelerate"`. Make sure one row sets every client option to a valid value.

### 9. Equal allocs/op is a review rule, not a CI one

`compare-allocs.py` lists a benchmark that allocates less ("fell") and never fails on it. The plan's "a refactor that allocates less goes back" needs the reviewer to read that table. Say so in the Gates table.

### 10. The escape analysis diff is noisy after extraction

- **Why it is noisy:** new helpers change the `-gcflags=-m` text even when nothing new escapes:
  - new `leaking param` lines;
  - `inlining call to <new name>`;
  - expressions printed under new variable names.

  A literal "no new line" rule fails every extraction.
- **Fix:** the criterion is no new **heap allocation site on the request path**, judged by the reviewer. allocs/op (CI) decides.
