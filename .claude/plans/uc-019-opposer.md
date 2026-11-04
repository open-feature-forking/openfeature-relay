# UC-019 opposer findings (revision 1)

Plan: `.claude/plans/uc-019.md` revision 1. Checked against:
- `docs/use_cases/UC-019-serve-vendor-flags.md`, issue #94 and its gap list, and the code on `main` @ 53128cf3;
- `flagsmith-go-client/v5` v5.3.0, downloaded read-only into `/tmp` with `go mod download`;
- the Flagsmith OpenFeature Go provider in the local go-sdk-contrib checkout (read only).

**Result: 1 blocker, 4 major, 5 minor.** The engine survey holds where I checked it (see "Verified"). The blocker is legal: the shared test data has no license. The majors are about what the parity tests can actually prove, the boolean semantics, the SSE trigger's failure mode, and #94's target.

## Findings

### 1. blocker: `engine-test-data` has no license, so Q7's copy can't go into this public repository

- **Plan step:** Q7 ("copy `engine-test-data/test_cases` into `contrib/flagsmith/engine/testdata/…`, … with its LICENSE"), and 3d.
- **Evidence:**
  - `gh api repos/Flagsmith/engine-test-data --jq .license.spdx_id` returns `none`, while `flagsmith-go-client` is `BSD-3-Clause`.
  - Without a license, the data is all rights reserved: committing a copy redistributes it, and there is no LICENSE file to carry along.
- **Fix:**
  - Take Q7's alternative for what is committed: own vectors, plus `fs/fixtures/environment.json` (covered by the client's BSD-3 license).
  - If the shared data is wanted as a test oracle, read it at test time from a read-only clone that is never committed (a `make` target, a cache outside the module, a CI job allowed network access). Ask the project lead whether to request a license from Flagsmith; containment makes contacting the vendor their call.

### 2. major: the plan names #94 part 1 as its target, but defers part 1's core and builds what #94 lists as "Later"

- **Plan step:** status line ("the end-to-end target is #94, part 1 (Flagsmith)"), Q11 ("a later slice after UC-017"), section 1's accepted gaps.
- **Evidence:** #94 part 1 is titled "Edge Proxy replacement as a configuration proxy (no evaluation in the relay)". It asks for:
  - an http fetcher with the SSE trigger and the interval poll as fallback;
  - a Flagsmith-compatible endpoint serving `/api/v1/environment-document/` to SDKs in local evaluation;
  - a test with an SDK in local evaluation mode.

  It lists the Go engine as "**Later**". The plan delivers the engine and OFREP serving (UC-019's text) and defers the passthrough endpoint and endpoint readiness (#94 gap 2). After PR 8, #94 part 1 is therefore still open.
- **Fix:**
  - Say so plainly: this plan realizes UC-019 (Flagsmith behind OFREP), and #94 part 1 stays open with its own slice after UC-017.
  - Or put the question to the project lead. The passthrough needs no engine, so it could come first.
  - Either way, the status line and section 6 must not claim #94.

### 3. major: Q5's boolean premise is wrong, and the recommendation contradicts itself for disabled booleans

- **Plan step:** Q5 ("A boolean request returns `enabled`, which is what the Flagsmith OpenFeature providers do by default"; "A disabled flag returns `DISABLED` with no value").
- **Evidence:** the Flagsmith OpenFeature Go provider (go-sdk-contrib `providers/flagsmith/pkg/provider.go`):
  - It reads booleans from the flag's **value** by default: `usingBooleanConfigValue` is false unless `WithUsingBooleanConfigValue()` is passed (lines 15, 145, 330-334).
  - In value mode, a disabled flag returns the caller's default with reason `DISABLED` (156-161).
  - In enabled mode, it returns `Value: reason != DISABLED`: a disabled flag is the value `false`, not "no value", always with reason `STATIC` (145-153).

  So "boolean = enabled" and "disabled = no value" can't both hold. Under enabled semantics a disabled boolean must be `false`, but the plan returns `HasValue=false`, and the client's default applies, which may be `true`. The TCK note in Q5 (Go and Java read enabled) disagrees with this code's default too.
- **Fix:**
  - Pick one semantics per mode and pin it with `TestBooleanIsEnabled` and `TestDisabled`.
  - Default to the providers' default (value). Make enabled-mode an engine setting (`booleanFromEnabled`); a disabled flag in that mode answers `false` with reason `DISABLED`.
  - Record which mode the oracle (finding 5) was produced in.

### 4. major: the SSE trigger as an `InitializingTrigger` stops the distribution when the realtime stream is down, and `triggers: [sse]` drops the poll

- **Plan step:** Q2 (`sse` "implements `InitializingTrigger`"), 1a ("`Init` connects before the first fetch"), 4a ("an `sse` trigger is configured explicitly"), R7 ("the guide says the poll is the fallback").
- **Evidence:**
  - **Init stops the distribution.** `InitializingTrigger`'s contract is that "an error stops the distribution, as an error of a sync provider's Init does" (core/pkg/sync/fetch.go:59-64). The fetching provider returns any trigger `Init` error from `Init` (fetching.go, `Init`). An unreachable or refused realtime stream at startup, which is an Enterprise-only, external service (R7), therefore aborts startup. A3 says the opposite: keep retrying and keep serving.
  - **An explicit list replaces the poll.** A source's explicit `triggers:` replaces the defaults (sync/builder/resolve.go:31-40); the poller is only the default when a source names none (:57-73, type `poller`). A Flagsmith source configured with `triggers: [{type: sse, …}]` has no poll. If the stream drops events or reconnects slowly, changes then arrive late, and the guide's "poll is the fallback" is false.
- **Fix:**
  - Either `Init` only validates settings and never fails on the connection, or the trigger isn't an `InitializingTrigger` and connects in `Run`, where errors back off (fetch.go:45-51).
  - Every manifest, test and guide example lists `{type: poller}` next to `sse`.
  - Add `TestStartsWhenStreamUnavailable` and `TestPollStillRunsWithSSE` to PR 1 and PR 7.

### 5. major: the Edge Proxy oracle can't check variants and reasons

- **Plan step:** section 1, "Main 7–10 … values, variants and reasons equal the oracle (Q10)"; Q10, 7b.
- **Evidence:**
  - The Edge Proxy answers Flagsmith's own `/api/v1/flags/` and `/api/v1/identities/`. Their flag entries carry the feature, `enabled` and `feature_state_value`; it has no OFREP endpoint.
  - OpenFeature reasons and variants are the plan's own mapping (Q4) of the Go engine's `Reason`/`Variant` (flagengine/engine.go:104-167). The Python Edge Proxy doesn't produce them.
  - So the recorded oracle can confirm enabled and value per flag, identity and traits, but not variant or reason. Verify this against the pinned image when recording.
- **Fix:**
  - Narrow the oracle claim to enabled and value.
  - Pin variants and reasons against `flagengine.GetEvaluationResult` plus Q4's table (3c's adapter test). Shared engine data can add to that if finding 1 allows it.
  - A8's "difference" covers what the oracle compares; say that in the test case (7d).

### 6. minor: the engine links `crypto/md5`, so the vendor distribution can have no FIPS build

- **Plan step:** 3a (boundary rule), R6, gates.
- **Evidence:**
  - `go list -deps` of `flagengine`, `engine_eval` and `environments` (v5.3.0, in a scratch module) includes `crypto/md5`. That is the percentage hash parity depends on.
  - This repository's `fips-140-3` depguard rule denies `crypto/md5` only for direct imports (.golangci.yml:18), so lint stays green.
  - `make test-fips` (`GODEBUG=fips140=only`) would fail on any contrib module added to `GO_LIB_MODULE_DIRS`.
- **Fix:** say that the Flagsmith distribution has no FIPS variant, and why (UC-012 and UC-010 gap). Keep contrib modules out of `test-fips`, with their own `test-…` targets inside `make test`.

### 7. minor: every SSE event triggers a full, paginated download

- **Plan step:** Q2 ("signals once per `data:` event"), 4a ("`Unchanged` when the body hash equals `previous`").
- **Evidence:** Flagsmith's own Go client fetches on an event only when the event's `updated_at` is newer than the document's (realtime.go:99-110, `parseUpdatedAtFromSSE`). Here, every event, keepalives included, means a full `environment-document/` download across all `Link` pages (client.go:596-683), whose identity overrides can be large, before the hash says "unchanged".
- **Fix:** let the Flagsmith fetcher skip a fetch when the latest event's `updated_at` isn't newer than the accepted document's. That needs a Flagsmith-aware trigger, or a hint the trigger passes to the fetcher. Or record it as a known cost in R3.

### 8. minor: check G7 against the raw document before accepting the gap

- **Plan step:** G7, Q5 (hiding server-only features as an accepted gap).
- **Evidence:** the Go model's `FeatureModel` is `{ID, Name, Type}` (features/models.go:13-17), and `encoding/json` drops unknown fields. "Not in `fs`'s model" therefore doesn't prove that the API's document lacks a marker. The format decodes the body itself (3b), so it could read one.
- **Fix:** inspect a recorded `environment-document` response (the stub's fixture, or Flagsmith's API docs) for a server-key-only field before naming the gap. If one exists, decode it in 3b.

### 9. minor: the vendor manifest needs an explicit `replaces` list

- **Plan step:** 6a.
- **Evidence:** `relay-builder` has no `local_root`. A manifest carries explicit `replaces` (builder/internal/manifest/manifest.go:63-108), and `distributions/flagd-full/manifest.yaml` lists every relay module. The Flagsmith manifest must also replace the three contrib modules and every relay module in its build list, while `fs` itself resolves from the module proxy, so `builder-e2e` needs network access or a warm cache.
- **Fix:** name that list in 6a, and the network or cache precondition in 6c.

### 10. minor: Q13 stops a serving distribution on any 401/403

- **Plan step:** Q13's recommendation.
- **Evidence:**
  - UC-019 A2 does say "System stops the vendor distribution".
  - UC-003 BR-008 says recoverable source errors don't stop flagd.
  - A key rotation, or a transient 403 from a WAF in front of the API, after the first accepted delivery takes down every environment of the relay.
- **Fix:** put Q13's alternative (only before the first accepted delivery; afterwards log and keep serving, as A3 does) to the project lead as the safer default, or require repeated refusals before stopping.

## Verified, no finding

- **Engine API (fs v5.3.0).**
  - `MapEnvironmentDocumentToEvaluationContext` (engine_eval/mappers.go:21), `MapContextAndIdentityDataToContext`, which sets the identity key to `<env api_key>_<identifier>` (mappers.go:291-320), and `GetEvaluationResult` (engine.go:119) exist as described.
  - Results are keyed by feature name, with `Enabled`, `Value`, `Variant`, `Reason` and `Metadata` (engine_eval/result.go).
  - The reason strings are exactly `DEFAULT`, `TARGETING_MATCH; segment=%s` and `SPLIT; weight=%g` (engine.go:104-156), matching G6 and Q4.
- **Hashing inputs.** Multivariate selection hashes `[featureContext.Key, identityKey]`, and only when an identity is set (engine.go:139-151). Environment-level evaluation, with no targeting key (BR-007), never splits. Numbers are stringified with `FormatFloat(f,'f',-1,64)` (evaluator.go:242-255), as R1 says.
- **Linked dependencies.** They are `blang/semver`, `ohler55/ojg`, `itlightning/dateparse` and `fs/trait`; no `net/http`. `resty` and `x/net` appear only in the module graph. 3a's boundary row is achievable.
- **Pagination (G4).** The client follows `Link: …; rel="next"` with `page_id` (client.go:596-683).
- **`hide_disabled_flags`** is on `ProjectModel` (projects/models.go:11). `EnvironmentModel` has no server-only marker (environments/models.go:11-19).
- **Today's flagd.** No code in `sync/http`, `core/pkg/sync`, `flagd/pkg/runtime` or `flagd/cmd` logs header values or whole `SourceConfig`s, and the http fetcher has no 401/403 handling (it retries like any error). Section 2's pins describe today's behaviour.
- **G3's reference.** UC-003 BR-006 is "Every Delivery Names Its Source"; the matching rule is BR-008 ("Recoverable Source Errors Do Not Stop flagd").
- **Drop-in.** flagd-full's manifest is untouched. `headersFrom` and `ErrSourceRefused` stay inert for today's sources.
