# Step 10 plan: opposer findings (revision 1)

Plan: `.claude/plans/step-10.md` (revision 1, uncommitted). Checked against `origin/main` @ 53128cf3, `docs/use_cases/UC-010`, `UC-011`, `UC-017`, `UC-021`, and `CLAUDE.md`'s drop-in rule. File references are on `origin/main`.

Counts: **0 blockers, 4 major, 6 minor.**

## What holds (verified)

- **Q2's premise is right, and the spec says so.** flagd-proxy reads the request's selector as a source URI and starts a sync for it on demand: `flagd-proxy/pkg/service/subscriptions/manager.go:195` (`SyncFromURI`), with `official.Factories()` at line 58. Its `sync_active_streams` gauge is built on `go.opentelemetry.io/otel/sdk/metric` (`flagd-proxy/pkg/service/sync_metrics.go:9,25`). UC-017 BR-007 ("Sources Are Fixed at Startup … it stays a separate program") and UC-010 BR-007 make flagd-sync and flagd-proxy different programs. flagd-sync is drop-in for flagd-full's flag sync endpoint (UC-017 BR-005), not for flagd-proxy.
- **The module-graph survey is right.** Requirement lines (not replaces) on main:

  | Module | Requires |
  |---|---|
  | `runtime/go.mod` | `evaluator/flagd`, jsonlogic, `k8s.io/client-go`, the three cloud SDKs, `otel/sdk` |
  | `sync/builder/go.mod` | `k8s.io/client-go`, `otel/sdk` |
  | `service/flag-evaluation`, `service/ofrep` | `evaluator/flagd`, jsonlogic, `otel/sdk` |
  | `service/flag-sync`, `service/transport` | `otel/sdk` only. Their `evaluator/flagd` lines are `replace` directives, which don't reach another module's graph |

  The only other relay `go.mod` files that require a forbidden module are `sync/gcs` (`otel/sdk`, which only goes into flagd-cloud), `sync/openfeature-operator` (`k8s.io`) and `telemetry/otel`. None of them is in flagd-minimal or breaks NFR-011 for flagd-sync, so G's list of modules is complete.
- **`runtime/cmd` imports no telemetry module.** It names the OpenTelemetry settings only as strings, so flagd-minimal can link without the OTel SDK once G is done.
- `service.Settings.Formats` exists (`core/pkg/service/endpoint.go:51-52`), as P2 assumes.
- Q6 (a hidden command) keeps `cli.golden` and UC-009's `--help` scenario byte-identical.

## Major

### 1. flagd-proxy is not "untouched", and nothing compares it with today's flagd-proxy

- **Step:** Q2 ("flagd-proxy stays unchanged: same module, code path, CLI, image …"), R5 ("C6 proves that flagd-proxy is untouched apart from an import path"), C6.
- **Evidence:**
  - flagd-proxy builds its syncs from this repository's sync providers (`manager.go:58`, `sync/builder/official`).
  - Since UC-003 those providers behave differently from upstream's, as #78's sync items record: a failed first fetch or connect no longer exits; every fetch has a default deadline; unchanged file content is not delivered again; a closed file watch reconnects; a file read or decode error keeps the current flags.
  - flagd-proxy's syncs also get no `sync.Settings.Telemetry`, so `flagd-grpc`'s stream metrics and gRPC client instrumentation depend on what its builder passes.
  - UC-009's upgrade differential covers flagd only, and C6 pins flagd-proxy as it is on `main`, after those changes, not as upstream ships it.
  - So "the same behaviour as today" is not established for flagd-proxy. UC-010 BR-007 says it "is not affected by adopting a standard distribution", which is narrower.
- **Fix:**
  - Reword Q2 and R5 to what is true: flagd-proxy's code path, CLI and image are unchanged in this step, and its sync behaviour follows #78's sync items.
  - Have #78 name flagd-proxy as affected by those items (so UC-009's `TestAcceptedMatchesIssue` and the migration notes see it).
  - Either add a flagd-proxy row to UC-009's upgrade differential (upstream's flagd-proxy image against this tree's, with one file source and one subscriber), or list "no flagd-proxy differential" as an accepted gap owned by UC-012.

### 2. G1 is not mechanical, and it rewrites pinned characterization tests

- **Step:** G1 (model `mechanic`: "internal ones get a small exported test seam or stay if they import nothing forbidden").
- **Evidence:** on `main`, these internal tests (`package runtime`) import component or telemetry modules:
  - `runtime_span_test.go` (UC-018's pin: `configformat/flagd`, `telemetry/otel/oteltest`, `otel/sdk/trace`);
  - `middleware_characterization_test.go` (`oteltest`, `otel/sdk/metric`, three endpoints);
  - `selector_characterization_test.go`, `official_components_characterization_test.go` (`sync/builder/official`, `evaluator/flagd`);
  - `bound_selector_test.go`, `official_endpoints_test.go`, `eventsink_end_to_end_test.go`, `endpoints_test.go`.

  They use unexported internals (`updateAndEmit`, `sourceReadiness`, `hooks`, `runShutdownHooks`, `Bus`). Moving them to `test/runtime` means either exporting a test API from `runtime`, which then becomes public API, or rewriting them. Rewriting UC-018's pins needed the project lead's approval in UC-001 5a0, and the step-6 and UC-004 pins were gated byte-identical.
- **Fix:**
  - Make G1 an implementer step.
  - List every moved pin, and for each one whether it moves byte-identical (external already) or is rewritten.
  - Get the project lead's decision on the rewrites, as in 5a0, and name the exported seam (one `runtime/runtimetest` package, documented as unstable) instead of "a small exported test seam".
  - Pins move in their own commit, with `git diff` of their assertions empty except the package line and imports.

### 3. Settings that "belong to several endpoints" can be skipped silently (UC-010 BR-001, UC-017 A2)

- **Step:** Q9 ("Settings without a single component (… `--context-value`, `--context-from-header`, `--max-request-*`, `--cors-origin`) belong to the runtime or to several endpoints. They never trigger it").
- **Evidence:**
  - UC-010 BR-001 says "A distribution never skips a source or setting silently", and UC-017 A2 says the same.
  - In flagd-sync (Q4: only the flag sync endpoint), `--context-from-header` belongs only to flag evaluation and OFREP. In the runtime's flag-to-key table (`runtime/endpoint_settings.go`), flag sync's settings carry `context-values` but no header mappings.
  - Under Q9 the flag never triggers the check, so `flagd-sync start --context-from-header …` starts and ignores it.
  - Any other setting whose endpoints are all absent in some distribution behaves the same way.
- **Fix:** classify a multi-endpoint setting by the set of components it belongs to, and stop startup when none of them is present. Keep "never triggers" only for settings of the runtime itself (`--log-format`, `--management-port`, `--debug`, `--config`, `--sources`/`--uri`). C3's table test then also checks that each setting's component set matches the keys `endpoint_settings.go` hands out.

### 4. Q7's "except flagd-full" needs a mechanism, and BR-003 constrains it

- **Step:** Q7 ("flagd-full's existing unknown-scheme error keeps today's text … Distributions other than flagd-full append the same hint"), S2.
- **Evidence:**
  - The unknown-scheme error comes from `sync/builder`, which all distributions share. The plan doesn't say how the runtime tells flagd-full apart.
  - Matching `Info.Name == "flagd-full"` would make a component-independent behaviour depend on a distribution's name: a custom distribution named `flagd-full` behaves like flagd-full, and a renamed flagd-full manifest changes an error text.
  - UC-011 BR-003 asks for the same output in every distribution, and UC-011 A2 expects the error to name the command.
- **Fix:** make the hint a property of the generated `main`, not a name check. For example, a manifest field (`dist.startup_hint: false`, default true) that flagd-full's manifest sets, passed through `distribution.Info`. Pin it in C2 (flagd-full unchanged) and in S2 (on, everywhere else). Alternatively, take Q7's alternative and record the changed line in #78.

## Minor

### 5. Q11 contradicts UC-017 step 9 without an amendment

- **Step:** Q11 ("The request must resolve to exactly one document … Several matching documents are `InvalidArgument`"), X-017.
- **Evidence:** UC-017 step 9 sends "the documents of the requested configuration format in the store that match its selector", which is plural. Step 11 and BR-004 say the same. Q11 refuses the plural case. X-017's list of spec edits does not include it.
- **Fix:** add the amendment of step 9, step 11 and BR-004 for vendor formats to Q19 and X-017, or take Q11's alternative.

### 6. Every new #78 item needs its `accepted.yaml` entry in the same PR

- **Step:** R4, R6, E4, C1, C4, C7.
- **Evidence:** UC-009's `TestAcceptedMatchesIssue` fails on a #78 item without an entry in `test/upgrade/accepted.yaml`. This step adds four observable changes that it records in #78:
  - `flagd components` exits 0 instead of an unknown command (C1; also missing from R4's list);
  - four configuration keys gain meaning (C4);
  - two more linked modules (C7, Q15, Q16).
- **Fix:** list in E4 and I1 the `accepted.yaml` entries (`covered_by` the new pins) and the `make refresh-issue-78` run, next to the #78 edit.

### 7. UC-021 A5 cannot hold under the noop telemetry provider

- **Step:** Q12, Q15, D (flagd-minimal with noop telemetry).
- **Evidence:** UC-021 A5 says drops are reported "on the management port". The pipeline counts them through the telemetry provider (`feature_flag.flagd.event_sink.dropped`), and with the noop provider nothing is reported anywhere. Q12 lets flagd-minimal configure `eventSinks` if its manifest listed a sink. Today none does, but a custom distribution can.
- **Fix:** state in Q12 that A5 needs a telemetry provider, and either refuse `eventSinks` with noop telemetry (an A2-style startup error) or name it as a known limit in UC-021.

### 8. Configuration file key style

- **Step:** Q12.
- **Evidence:** flagd's configuration file keys are viper keys, named after the flags in kebab-case (`ofrep-port`, `sync-port`, `metrics-exporter`). Q12 introduces camelCase top-level keys (`eventSinks`, `endpointSettings`, `eventCollector`) and settings (`bufferSize`). Source entries already use camelCase (`certPath`, `providerID`), so both styles exist today.
- **Fix:** choose deliberately and state the rule in Q12. Kebab-case for top-level keys matches the flags, and camelCase inside list entries matches `sources`.

### 9. An inert collector endpoint and the service endpoint contract

- **Step:** Q16, E3.
- **Evidence:** without `eventCollector.port`, the factory "opens no port". If its `Serve` then returns at once, it breaks UC-004's contract shape ("`Serve` blocks while serving and returns nil when `ctx` ends"), and `servicetest`'s start, shutdown and connection cases have nothing to connect to.
- **Fix:** say that the inert `Serve` blocks until `ctx` ends, and run `servicetest.Run` with a port set. Also add a test that a flagd-full without the key opens exactly today's four ports.

### 10. Q9's "explicitly set" for the configuration file

- **Step:** Q9, R3.
- **Evidence:** R3 names the viper pitfalls for flags and environment variables. For the configuration file, "the config file's own keys" needs the parsed file, not viper's merged view, because viper's `IsSet` also reports defaults for bound keys in some versions.
- **Fix:** read the file's keys once (`viper.New()` on the same file, `AllKeys()`), and add a table row in `TestAbsentComponentSettingStopsStartup` with a config file that sets an absent component's key to its default value. That row must still stop startup, because it was set explicitly.
