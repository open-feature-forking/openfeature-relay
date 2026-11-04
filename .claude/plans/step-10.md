# Step 10 plan: Inspect Distribution Components, Adopt Standard Distribution, Run Configuration Proxy, Export Evaluation Data (UC-011, UC-010, UC-017, UC-021)

**Decision (2026-11-04):** revision 2 approved by the project lead; every recommendation (Q1–Q21) accepted.

**Status: revision 2.** It resolves `step-10-opposer.md` (0 blockers, 4 major, 6 minor); each finding's outcome is in section 8. Section 0 holds the questions for the project lead. Every recommendation keeps what flagd-full shows today. Q3 changed its recommendation; Q20 and Q21 are new.

**Base.** `main` @ 53128cf3 (UC-009 merged: the `flagd` module is the Builder's output for `distributions/flagd-full/manifest.yaml`, checked by `make check-flagd-full`). Issues: UC-011 #23, UC-010 #22, UC-017 #29, UC-021 #33. "R" is `github.com/open-feature-forking/openfeature-relay`.

**What exists.**

- **`core/pkg/distribution`:** `Components`, `Info` (name plus the stamped version, commit and date) and `Inventory` (group, type and module, with versions from the build info). The generated `flagd/components` package fills both.
- **`runtime/cmd`:**
  - `flagd version --verbose` prints `distribution: <name> <version>` and `runtime: <version>` (UC-001 Q10).
  - The flag-to-component table of UC-001 Q15 is `runtime/endpoint_settings.go` plus `withTelemetryConfig` in `start.go`. The runtime ignores settings of absent components (N3).
  - It names the OpenTelemetry settings only as strings and imports no telemetry module.
- **Schemes:** sync factories expose `Schemes()` at run time (`core/pkg/sync/factory.go`), so `flagd components` reads schemes from `Components.Sync`; the Builder does not change.
- **A distribution without an evaluator starts today.** The runtime builds the evaluation router only when an endpoint needs it (`runtime/endpoints.go`). `service/flag-sync` declares `Needs{}`. `service.Settings.Formats` exists (`core/pkg/service/endpoint.go`).
- **`service/flag-sync`:**
  - It serves only flagd flags from `store.Watch`.
  - `store.Documents(format, source)` exists, but no request can name a format (FR-044 is open).
  - The selector takes one `key=value` pair (`flagSetId`, `source`), or a bare string read as a source.
- **`flagd-proxy`** is its own module and program:
  - The request's selector is a **source URI**.
  - `subscriptions.Coordinator` starts a sync for it on demand, through `syncbuilder.SyncFromURI` over `sync/builder/official.Factories()`, which are **this repository's** sync providers. Since UC-003 they behave as #78's sync items describe.
  - Its `sync_active_streams` gauge is built on the OpenTelemetry SDK directly.
- **Events:**
  - Packages: `core/pkg/events` (the bus, `Evaluation` with origin, `ConfigurationChange`, `Tracking`), `core/pkg/eventsink` (the pipeline, and `feature_flag.flagd.event_sink.dropped` counted through the telemetry provider), and `runtime/events.go` (start, and flush on shutdown).
  - The generated source lists event sinks as `eventsink.Configured{Factory}` with no configuration, and every listed sink subscribes.
  - Event kinds are fixed by the factory.
  - `test/eventsink-example` is a webhook sink. There is no official event sink and no collector endpoint (FR-039).
- **Tests and acceptance:**
  - `test/distributions` builds every manifest under `distributions/*` and `test/distributions/*`. `TestMinimalShapeLinksNoPlatformSDK` checks the **linked** modules (`go version -m`).
  - `test/upgrade/accepted.yaml` must hold one entry per item of #78 (`TestAcceptedMatchesIssue`; `make refresh-issue-78`).

**Module graphs today.** Through their tests, these `go.mod` files require modules that NFR-004 or NFR-011 forbid. Verified by the opposer; the requirement lines count, the `replace` lines don't:

| `go.mod` | requires | tests that pull it in |
|---|---|---|
| `runtime` | `evaluator/flagd`, jsonlogic, `k8s.io/client-go`, cloud SDKs, `otel/sdk` | 8 internal (`package runtime`), among them UC-018's span pin and step 6's middleware and selector pins; 7 test packages in subdirectories |
| `sync/builder` | `k8s.io/client-go`, `otel/sdk` | 3 internal |
| `service/flag-evaluation`, `service/ofrep` | `evaluator/flagd`, jsonlogic, `otel/sdk` | 14 internal, among them the endpoint benchmarks CI compares |
| `service/flag-sync`, `service/transport` | `otel/sdk` | 2 internal, 1 external |

The linked modules and the package graph are clean. `runtime` and the endpoints import none of these outside tests, so a flagd-minimal or flagd-sync binary links none of them. Q3 decides which of the two NFR-004 and NFR-011 measure.

## 0. Open questions for the project lead

The recommendation comes first.

- **Q1, slicing and parallelism.**
  - **Recommendation:** four stacks plus a characterization PR. Section 7 has the branches.
    - **Runs in parallel now:** C (characterization), I (UC-011, `flagd components`), P2 (UC-017, flag sync format requests) and E2 (UC-021, the webhook event sink module).
    - **After C:** S (UC-010 BR-001, settings of absent components), then E1 (operator settings for event sinks). Both edit `runtime/cmd/start.go`.
    - **After S:** D (UC-010, the four standard manifests and their tests).
    - **After D and P2:** P3 (flagd-sync end to end).
    - **After E1–E3:** E4 (flagd-full exports).
    - **Last:** closing PRs, one per use case.
    - The critical path is C → S → E1 → E3 → E4 → close. Under Q3's alternative, stack G (appendix A) runs beside it and gates D.
  - *Alternative:* one stack per use case, strictly in sequence. Simpler to review, about twice as long.

- **Q2, flagd-proxy and flagd-sync (UC-017).**
  - **The facts:**
    - Today's flagd-proxy reads the selector as a source URI.
    - It starts a sync per distinct selector on demand.
    - It validates nothing.
    - It has its own CLI and image.

    flagd-sync reads sources fixed at startup, validates them with flagd's configuration format, and reads the selector as flagd does. The same request means different things on the two programs, so flagd-sync **cannot** replace flagd-proxy as a drop-in. The spec already says so (glossary, vision, UC-010 BR-007, UC-012 BR-013, UC-017 BR-007).
  - **Recommendation:**
    - **flagd-sync does not replace flagd-proxy.** This step changes no flagd-proxy file: its module, code path, CLI, image and `sync_active_streams` metric stay as they are on `main`.
    - **flagd-proxy is not "unchanged from today's release".** It syncs through this repository's sync providers, so it already shows #78's sync items: a failed first fetch no longer exits; fetch deadlines; no redelivery of unchanged file content; watch reconnects; a read or decode error keeps the flags. #78 says so (Q20).
    - **flagd-sync is drop-in for flagd-full's flag sync endpoint.** An in-process provider that reads flagd-full's flag sync (port 8015) switches to flagd-sync with the same sources, the same settings and no client change (BR-005). C5 and P3 pin that byte for byte.
    - **Spec edits:** UC-003's row "flagd-proxy keeps `SyncFromURI`; it moves to the flag sync endpoint in UC-017" and UC-006's row "flagd-proxy's own sync metrics | UC-017" contradict BR-007. Both become "not delivered: flagd-proxy stays a separate program (UC-017 BR-007, UC-012 BR-013)".
  - *Alternative:* rebuild flagd-proxy on the runtime with a "dynamic source" sync provider. Its validation, readiness and metrics would change, and it needs a component type the spec does not have.

- **Q3, what NFR-004 and NFR-011 measure. (changed in revision 2)**
  - **Recommendation:** amend both NFRs from "module graph" to "the binary links no module … and the distribution's packages import none". Check them with `go version -m` and `go list -deps` on each built standard distribution.
    - **This is what scanners of a release see:** Trivy and Grype read a Go binary's embedded build info, which lists the linked modules only.
    - **Making the build list clean as well is stack G (appendix A):** about 30 test files, 24 of them internal. It rewrites pinned characterization tests (UC-018's span pin, step 6's middleware and selector pins) or exports a test seam from `runtime`. It also moves the endpoint benchmarks that CI's allocs/op comparison runs on base and head. That cost buys nothing an operator or a binary scanner sees.
    - **UC-001's hand-over is closed by the amendment.** Its gap was "a module graph that does not carry the tests of the runtime and the sync builder", which makes every repository manifest replace all sync modules. What remains: repository manifests keep their long `replaces`, and a tagged-release build downloads modules it does not link.
  - *Alternative:* keep the NFRs as written and run stack G before D (appendix A, with the opposer's conditions: an implementer step, a `runtime/runtimetest` seam documented as unstable, every moved pin listed, and your decision on each rewrite as in UC-001 5a0).

- **Q4, the contents of the standard distributions.**
  - **Recommendation:** flagd's configuration format everywhere, flagd's evaluator in each evaluating distribution, and a binary named `flagd`.

    | Distribution | Sync providers | Service endpoints | Telemetry |
    |---|---|---|---|
    | flagd-minimal | file, http, flagd-grpc | flag evaluation, OFREP, flag sync | noop |
    | flagd-cloud | file, http, flagd-grpc, gcs, azblob, s3 | all three | OpenTelemetry |
    | flagd-kubernetes | file, http, flagd-grpc, openfeature-operator | all three | OpenTelemetry |
    | flagd-sync | all of flagd-full's | flag sync | OpenTelemetry (Q5) |
    | flagd-full | unchanged | unchanged, plus the inert additions of Q15 and Q16 | unchanged |

  - *Alternative:* flagd-minimal without flagd-grpc and flag sync (the `minimal-shape` test manifest), a smaller binary.

- **Q5, flagd-sync's telemetry provider.** UC-017 A2's example ("an OpenTelemetry collector address") implies the noop provider; flagd-full's flag sync endpoint records spans and serves `/metrics` on 8014 today.
  - **Recommendation:** OpenTelemetry. An Operator moving from flagd-full's flag sync endpoint keeps the metrics and spans. UC-017 A2's example becomes "an OFREP port".
  - *Alternative:* noop. flagd-sync then answers `/metrics` with 404.

- **Q6, `flagd components` and the help text.**
  - **Recommendation:** a hidden command (`Hidden: true`) in every distribution. `cli.golden` and UC-009's `--help` scenario stay byte-identical, and the docs describe the command.
    - It is still a visible change to flagd-full: `flagd components` exits 0 instead of failing as an unknown command. That goes to #78 with an `accepted.yaml` entry (I1).
  - *Alternative:* list it in help, one more line in `cli.golden`.

- **Q7, the startup error naming `flagd components` (UC-011 A2). (mechanism added in revision 2)**
  - **Recommendation:** the hint is a property of the generated `main`, not a name check.
    - **Mechanism:** the manifest takes `dist.components_hint` (default `true`). The Builder stamps it into `distribution.Info.ComponentsHint`. flagd-full's manifest sets `false`.
    - **With the hint on**, both the unknown-scheme error and stack S's absent-setting errors end with "run `flagd components` to list the components of this distribution".
    - **With it off**, the unknown-scheme error keeps today's text (`invalid sync provider: …, must be one of with …`).
    - **flagd-full** never meets an absent-setting error.
    - **Tests:** C2 pins flagd-full unchanged; S2 pins the hint on in a test distribution. UC-011 BR-003 governs the listing's layout, not error texts, so a per-manifest switch does not break it.
  - *Alternative:* the hint everywhere, flagd-full included. A changed log line, recorded in #78 with an `accepted.yaml` entry.

- **Q8, the listing's layout (UC-011 steps 2–5, A3, BR-003, BR-004).**
  - **Recommendation:**
    - **Header:** `distribution: <name> <version>` and `runtime: <version>`, identical to `flagd version --verbose`.
    - **Body:** one section per component type in a fixed order: sync providers (fetch triggers listed there with no schemes), configuration formats, evaluators, service endpoints, telemetry, middleware, event sinks. Each line holds type, module and version, plus schemes for sync providers.
    - **JSON:** `--output json` prints the same content as one document, `{"distribution":{"name","version"},"runtime":{"version"},"components":{"sync_providers":[{"type","module","version","schemes"}],…}}`. The keys are the `distribution.Group` values, and an empty group is an empty list.
    - **No runtime:** the command reads only `Components` and `Inventory`. It reads no configuration and opens no port (BR-002).
  - *Alternative:* `--json`, or YAML as well.

- **Q9, settings of absent components (UC-010 BR-001, A3; UC-017 A2, BR-006). (classification and "explicit" sharpened in revision 2)**
  - **Recommendation:** the table in `runtime/cmd` maps each flagd-full flag, environment variable and configuration file key to a **set** of component types, or to the runtime.
    - **When startup stops:** a setting that is explicitly set, and none of whose components the distribution contains. For example, `--context-from-header` belongs to {flag evaluation, OFREP}, so it stops flagd-sync. `--cors-origin`, `--context-value` and `--max-request-*` belong to all three endpoints.
    - **What never stops startup:** the runtime's own settings: `--log-format`, `--management-port`, `--debug`, `--config`, `--sources`/`--uri`.
    - **Explicitly set, per source:**
      - a flag: `pflag.Changed`;
      - an environment variable: `os.LookupEnv` of its bound name;
      - the configuration file: its own keys, read once with a separate `viper.New()` on the same file and `AllKeys()`, never the merged view.

      A configuration key set to its default value still counts as set.
    - **The error:** every such setting with its components, sorted, in one error; exit status 1 as today's fatal errors.
    - **Defaults** never trigger it (N3).
    - **flagd-full** contains every component, so its startup cannot change; C3 pins that. C3 also checks that each setting's component set equals the endpoint types `endpoint_settings.go` hands its key to.
  - *Alternative:* fail on defaults too. That breaks N3 and every default start of a smaller distribution.

- **Q10, how a flag sync request names a configuration format (FR-044).** A proto change is not possible here: the schemas are `buf.build/open-feature/flagd`, outside containment, and C-005 applies.
  - **Recommendation:** the header `Flagd-Configuration-Format` on `SyncFlags`, `FetchAllFlags` and the flags route.
    - Absent or `flagd` takes today's code path, byte-identical.
    - A format the distribution lacks is `InvalidArgument` (HTTP 400), naming the formats it has.
    - Old servers ignore the header and answer flagd documents.
  - *Alternative:* a `format=` selector key. It needs the multi-key selector (#1708 upstream), and today's flagd rejects it as `invalid selector key`.

- **Q11, the payload for a vendor format. (spec amendment added in revision 2)**
  - **Recommendation:**
    - The request resolves to exactly one document of the format: by `source=`, by `flagSetId=`, or because only one exists. `flag_configuration` carries its bytes unchanged.
    - Several matches are `InvalidArgument` naming them. Vendor formats have no shared merge rule; BR-005's merging is flagd's.
    - The stream re-sends the document when its version changes.
    - Spec edit (Q19, X-017): UC-017 step 9, step 11 and BR-004 say "documents", plural. They gain "for a configuration format other than flagd's, exactly one document, chosen by the selector".
  - *Alternative:* a JSON array of every matching document. The steps stand as written, but every vendor client has to unwrap the array.

- **Q12, operator settings for event sinks and other extension components (UC-021 steps 2–5, A2). (key style added in revision 2)**
  - **Recommendation:** configuration file keys only, no new CLI flags, so both help goldens stay identical.
    - **Key style:** top-level keys are kebab-case like the flags' keys (`ofrep-port`). Keys inside list entries are camelCase like `sources` entries and the core's existing sink keys (`bufferSize`).

      ```yaml
      event-sinks:
        - type: webhook
          kinds: [evaluation, configuration_change]    # Q13
          bufferSize: 10000                            # eventsink.Options keys
          url: https://hooks.example.com/flagd
          headers: {Authorization: {env: WEBHOOK_TOKEN}}   # Q14
      middleware:
        - type: example                                # list order is chain order
      endpoint-settings:
        <extension endpoint type>: {...}               # Config.EndpointSettings
      ```

    - **The generated source lists available factories** (`Components.EventSinkFactories`, `Components.MiddlewareFactories`). `runtime/cmd` builds `Components.EventSinks` and `Components.Middleware` from the entries.
    - **An unknown `type`** stops startup and names the types the distribution has (A2).
    - **No entries:** nothing subscribes, and NFR-013's no-cost path holds.
    - **flagd-full** without these keys starts exactly as today. That the keys now mean something goes to #78 (C4, E1).
  - *Alternative:* event sinks only; middleware and extension endpoints stay UC-001 gaps.

- **Q13, event kinds per sink.**
  - **Recommendation:** `kinds` narrows the factory's kinds. An undeclared kind stops startup; no `kinds` means the factory's.
  - *Alternative:* the factory's kinds only, and UC-021 step 2 is amended.

- **Q14, credentials (UC-021 BR-006).**
  - **Recommendation:** a setting value may be `{env: NAME}` or `{file: PATH}`, which the core resolves before `Create`.
    - No string ever resolves, so a literal value cannot collide with a reference.
    - An unset variable or unreadable file stops startup, naming the setting and the sink, never the value.
    - Resolved values are never logged.
    - FR-028 for sources stays Open.
  - *Alternative:* `${VAR}` interpolation, which collides with literals that contain `$`.

- **Q15, an official event sink in flagd-full (UC-021 step 1).**
  - **Recommendation:** promote `test/eventsink-example` to `R/eventsink/webhook` and list it in flagd-full. It stays inert until `event-sinks` names it. flagd-full links one more module (`go version -m`), which goes to #78 with an `accepted.yaml` entry (E4).
  - *Alternative:* no official sink. UC-021 runs only on custom distributions, and step 1 is amended.

- **Q16, the collector endpoint (FR-039, UC-021 A3, A4). (contract shape added in revision 2)**
  - **Recommendation:** an official `R/service/event-collector` endpoint, listed in flagd-full.
    - **Inert by default:** without `event-collector: {port: N}`, its `Serve` opens no port and blocks until `ctx` ends, as UC-004's contract requires.
    - **With a port:** it serves `POST /events/v1`, a JSON batch of `evaluation` and `tracking` events with a documented, provisional, relay-specific schema.
    - **Publishing:** every accepted event goes onto the bus with origin `in-process`. Invalid items are rejected per item (400 with indices and reasons) and nothing is published for them.
    - **Shared behaviour:** CORS and the size limits are the same as OFREP's.
    - **Tests:** `servicetest.Run` runs with a port set. `TestFlagdFullOpensTodaysPorts` checks that flagd-full without the key listens on exactly 8013, 8014, 8015 and 8016.
  - *Alternative:* defer FR-039. UC-021 A3 and A4 then need a delivering use case the spec does not have.

- **Q17, standard distributions' outputs, images and FIPS builds.**
  - **Recommendation:**
    - The four new manifests generate into an ignored `build/`, and only flagd-full's output is committed.
    - Images, archives and release names are UC-002 and UC-012 gaps.
    - A FIPS compile of each manifest joins the gated FIPS job.
  - *Alternative:* commit every generated module.

- **Q18, evaluation parity (UC-010 step 9, BR-006; UC-017 BR-002, BR-005).**
  - **Recommendation:** a differential test in `test/distributions` against the built flagd-full on the test-harness flags.
    - Evaluating distributions: ConnectRPC and OFREP, single and bulk, compared field by field.
    - flagd-sync: C5's flag sync goldens, compared byte for byte, and the invalid contract samples rejected alike.
  - *Alternative:* the Gherkin suites per distribution, about 10 minutes each in CI.

- **Q19, spec corrections.**
  - **Recommendation:** fold into the closing PRs:
    - FR-021 says "relay"; it becomes flagd-sync.
    - UC-017 A2's example (Q5).
    - The UC-003 and UC-006 flagd-proxy rows (Q2).
    - UC-017 step 9, step 11 and BR-004 for vendor formats (Q11).
    - NFR-004 and NFR-011's wording (Q3).
    - UC-021 A5 if Q21 takes its alternative.
    - `docs/relay/distributions.md`'s contents table (Q4).

- **Q20, flagd-proxy against today's flagd-proxy. (new in revision 2)**
  - **Recommendation:**
    - **#78:** state that its sync items apply to flagd-proxy too, which goes into `accepted.yaml` after `make refresh-issue-78`.
    - **A differential row in UC-009's harness (C8):** upstream's flagd-proxy image against this tree's, with one file source and one subscriber. It covers the first delivery, a change, a deleted file, an unreachable source and `sync_active_streams`.
    - It runs where the upgrade differential runs. Every difference must match an `accepted.yaml` entry.
  - *Alternative:* no flagd-proxy differential, named as an accepted gap owned by UC-012 (flagd-proxy's separate release, BR-013).

- **Q21, event sinks without a telemetry provider that serves metrics (UC-021 A5). (new in revision 2)** UC-021 A5 reports drops "on the management port". The pipeline counts them through the telemetry provider, and with the noop provider nothing is reported.
  - **Recommendation:** refuse `event-sinks` at startup when the distribution's telemetry provider serves no `/metrics`. The error names the setting and the reason, in the style of A2. flagd-full is not affected.
  - *Alternative:* allow it with a startup warning, and amend A5 to "counts the drop through the telemetry provider".

## 1. Scope

**Realizes:**

| Requirement | Delivered by |
|---|---|
| FR-023, FR-034 (listing) | I |
| FR-021, NFR-003, NFR-004, NFR-011 | D (Q3 decides what the NFRs measure) |
| FR-022, UC-010 BR-001 | today's scheme check plus S |
| FR-024 | flagd-minimal with the noop telemetry provider |
| FR-025, FR-026, FR-029, FR-044, FR-048 (flag sync half) | P2, P3 |
| FR-037, FR-038, FR-039, FR-028 (sinks only), NFR-013 | E1–E4 |
| FR-052 | existing staleness readiness, exercised by D |

**Use case coverage:**

| Flow | Test |
|---|---|
| UC-011 main 1–6, BR-001, BR-003, BR-004 | `runtime/cmd` `TestComponentsListsInventory` (golden), `TestComponentsIdentityMatchesVersionVerbose`; `test/distributions` `TestComponentsMatchesManifest` per built manifest |
| UC-011 A1 | C1 pins it on flagd-full before I |
| UC-011 A2 | `TestAbsentSettingErrorNamesComponentsCommand`, `TestUnknownSchemeHint` (hint on; Q7) |
| UC-011 A3 | `TestComponentsJSONSameContentAsText` |
| UC-011 BR-002, failure postcondition | `TestComponentsOpensNoPortAndReadsNoConfig` |
| UC-011 A4 | no system behaviour |
| UC-010 main 4–7, 9–10, BR-002, BR-006 | `TestStandardDistributionParity`, `TestStartsWithFlagdFullDefaults` per manifest |
| UC-010 step 8 | `TestVersionVerboseNamesEachDistribution` |
| UC-010 A2 | `TestUnknownSchemeListsSchemes` on flagd-minimal |
| UC-010 A3, BR-001 | `TestAbsentComponentSettingStopsStartup`: table over flag, environment variable, configuration key, and a configuration key at its default value; noop telemetry with `--metrics-exporter`; no OFREP with `--ofrep-port`; flagd-sync with `--context-from-header` |
| UC-010 A5 | P3 |
| UC-010 BR-003 | `TestMinimalRecordsNoTelemetry` |
| UC-010 BR-004, NFR-003, NFR-004, BR-007, NFR-011 | `TestMinimalSizeRatio`, `TestStandardModuleGraphs` (Q3's measure) |
| UC-010 A1, BR-005 | UC-002/UC-012 gap, plus the FIPS compile (Q17) |
| UC-010 A4, A6 | no system behaviour |
| UC-017 main 1–12 | `TestFlagSyncProxyServesInProcessProvider` on the built flagd-sync |
| UC-017 A1, A2 | `TestUnknownSchemeListsSchemes`, `TestAbsentComponentSettingStopsStartup` on flagd-sync |
| UC-017 A3, A5, BR-002, BR-003 | `TestProxyRejectsWhatFlagdFullRejects`, `TestRejectedChangeKeepsLastAccepted` |
| UC-017 A4 | `TestUnavailableSourceNotReady` |
| UC-017 A6 | `TestClientReconnectsAfterRestart` |
| UC-017 A7 | `test/service-example`'s vendor endpoint in test distribution `sync-vendor` |
| UC-017 A8 | `TestNoEvaluationEndpoint` (8013 and 8016 refuse) |
| UC-017 A9 | `TestAbsentFormatSkipsSource` |
| UC-017 BR-004, FR-044 | P2's format tests |
| UC-017 BR-005 | C5's goldens replayed against flagd-sync |
| UC-021 main 1–12 | `TestExportToWebhook` on the built flagd-full |
| UC-021 A1 | no system behaviour |
| UC-021 A2 | `TestInvalidEventSinkSettingStops` (unknown type, undeclared kind, missing env, unreadable file, invalid URL, and Q21's noop case) |
| UC-021 A3, A4 | `TestCollectorPublishesInProcessEvents`, `TestCollectorRejectsInvalidEvents` |
| UC-021 A5, A6, BR-001, BR-005 | `TestFullBufferDropsAndReportsOnMetrics`, `TestReceiverDownKeepsReadiness` |
| UC-021 BR-002 | CI's allocs/op comparison with no `event-sinks` |
| UC-021 BR-003, BR-004 | UC-004's pins, plus E4's binary test |
| UC-021 BR-006 | `TestCredentialNeverLogged` |
| UC-021 BR-007 | `TestSinkReceivesEveryFlagSet` |

**Leaves out:**
- images, archives and release names (UC-002, UC-012);
- vendor documents on the flag evaluation event stream (UC-019);
- flagd-proxy's code (Q2; Q20 compares its behaviour only);
- FR-028 for sources;
- UC-015 and UC-016.

## 2. Characterization (PR C, base `main`, first commit before any change)

Already pinned: `flagd/testdata/cli.golden`, `startup_logs.golden`, `runtime/cmd` `start_flags.golden` and `start_config.golden`, the version characterization, `runtime/testdata/official_components.golden` and `endpoint_settings.golden`, the management port and shutdown pins, the flag-sync route tests, `flagd-proxy/pkg/service` tests, and UC-009's upgrade differential.

| Pin | Test | Pins |
|---|---|---|
| C1 | `flagd` `TestUnknownCommandExit` | `flagd components` on flagd-full today: stderr and exit 1. I changes it, with an `accepted.yaml` entry |
| C2 | `flagd` `TestUnknownSchemeStartupError` | Text and exit status of `flagd start --uri foo://x`. Stays green through S2 (Q7) |
| C3 | `runtime/cmd` `TestEveryFlagdFullSettingHasAComponent` | Every flag, environment variable and configuration key maps to a component set or the runtime (Q9). Each set equals the endpoint types `endpoint_settings.go` hands the key to. A new flag without an entry fails |
| C4 | `runtime/cmd` `TestUnknownConfigKeysIgnored` | A configuration file with `event-sinks`, `middleware`, `endpoint-settings` and `event-collector` starts flagd-full today and changes nothing. E1 and E3 change it, with an `accepted.yaml` entry |
| C5 | `test/distributions` `TestFlagSyncPayloadCharacterization` | flagd-full, two file sources with flag sets and metadata. Goldens of `SyncFlags`' first message (with and without a selector), `FetchAllFlags`, `GetMetadata`, and the flags route's body, ETag and headers |
| C6 | `flagd-proxy/cmd` `TestCLICharacterization`, `flagd-proxy/pkg/service` `TestSubscriptionBySourceURI` | flagd-proxy's help and selector-as-URI behaviour on `main`, so nothing in step 10 changes it unnoticed |
| C7 | `test/distributions` `TestFlagdFullLinkedModules` | `go version -m` of flagd-full. E4 adds two modules, with an `accepted.yaml` entry |
| C8 | `test/upgrade` flagd-proxy scenario (Q20) | Upstream flagd-proxy against this tree's, with differences matched to `accepted.yaml`. Dropped if Q20 takes its alternative |

## 3. Steps

Every commit builds and passes `make test`. A PR that adds a #78 item updates the issue, runs `make refresh-issue-78` and adds the `accepted.yaml` entry (`covered_by` the new pins) in the same PR.

### Stack I (UC-011; base `main`, parallel now)

| Step | Model | Files | Change | Check |
|---|---|---|---|---|
| I1 | implementer | `runtime/cmd/components.go`, `root.go`; #78, `test/upgrade/accepted.yaml`, `testdata/issue-78.txt` | Hidden `components` command (Q6), text and `--output json` (Q8), a shared identity helper with `version --verbose`. The #78 item "`flagd components` lists the components instead of failing" | `TestComponentsListsInventory`, `TestComponentsJSONSameContentAsText`, `TestComponentsIdentityMatchesVersionVerbose`, `TestComponentsOpensNoPortAndReadsNoConfig`; `cli.golden` unchanged; `TestAcceptedMatchesIssue` |
| I2 | implementer | `test/distributions` | `TestComponentsMatchesManifest` | green on every built manifest |
| I3 | mechanic | `docs/reference/flagd-cli/`, `flagd/cmd/doc` | Generated doc for the hidden command | `make docs-check` |

### Stack S (UC-010 BR-001; base C)

| Step | Model | Files | Change | Check |
|---|---|---|---|---|
| S1 | implementer | `runtime/cmd/settings.go`, `start.go` | Q9's table with component sets; "explicit" per source; one error with the hint when `Info.ComponentsHint` is set | `TestAbsentComponentSettingStopsStartup` (all rows of section 1), `TestDefaultsNeverStop` (N3), C3 green, `start_flags.golden` unchanged |
| S2 | implementer | `core/pkg/distribution` (`Info.ComponentsHint`), `builder/internal/manifest` and the templates (`dist.components_hint`, default true), `distributions/flagd-full/manifest.yaml` (`false`), regenerated `flagd/`, `sync/builder/resolve.go`, `runtime/cmd/start.go` | Q7's mechanism; the hint on the unknown-scheme error when it is on | `TestUnknownSchemeHint` (test distribution), C2 unchanged, `make check-flagd-full`, the Builder's manifest tests |

### Stack D (UC-010; base S)

| Step | Model | Files | Change | Check |
|---|---|---|---|---|
| D1 | mechanic | `distributions/flagd-{minimal,cloud,kubernetes,sync}/manifest.yaml`, `.gitignore` | Q4's contents, `binary: flagd`, every `replaces` line | `TestRepositoryManifestsReplaceEveryRelayModule`; built in `make test-distributions` |
| D2 | implementer | `test/distributions` | `TestStandardModuleGraphs` (Q3's measure: `go version -m` and `go list -deps`), `TestMinimalSizeRatio`, `TestMinimalRecordsNoTelemetry`, `TestVersionVerboseNamesEachDistribution`, `TestStartsWithFlagdFullDefaults` | green; measure NFR-003 in the first commit (R2) |
| D3 | implementer | `test/distributions/parity_test.go` | `TestStandardDistributionParity` (Q18), `TestUnknownSchemeListsSchemes` | green |
| D4 | mechanic | `Makefile` FIPS job | FIPS compile per manifest (Q17) | gated job green on a manual `build` run |

### Stack P (UC-017)

| Step | Model | Base | Files | Change | Check |
|---|---|---|---|---|---|
| P2 | implementer | `main` (now) | `service/flag-sync/handler.go`, `fetch.go`, `http_handler.go`, new `format.go` | Q10's header; absent or `flagd` keeps today's code path; another contained format serves one document unchanged and re-sends on a version change (Q11) | `TestNoFormatHeaderServesFlagdUnchanged` (existing handler tests unchanged), `TestFormatHeaderServesVendorDocument`, `TestSeveralDocumentsInvalidArgument`, `TestUnknownFormatInvalidArgument`, `TestUnchangedVersionSendsNothing`; flag-sync allocs/op unchanged |
| P3 | implementer | D1 + P2 | `test/distributions/flagdsync_test.go`, test distribution `sync-vendor` (flagd-sync plus `configformat-example` and `service-example`) | Section 1's UC-017 rows on the built binaries; C5 replayed against flagd-sync | all green |

### Stack E (UC-021)

| Step | Model | Base | Files | Change | Check |
|---|---|---|---|---|---|
| E1 | implementer | S | `core/pkg/distribution` (`EventSinkFactories`, `MiddlewareFactories`), `core/pkg/eventsink` (`KeyKinds`, credential references), `runtime/cmd/extensions.go`, builder templates, regenerated `flagd/components`; #78 and `accepted.yaml` (C4's keys) | Q12–Q14 and Q21. Hand-written mains keep `Components.EventSinks` and `Middleware` | `TestNoEventSinksConfiguredNothingSubscribes`, `TestInvalidEventSinkSettingStops`, `TestKindsNarrowFactoryKinds`, `TestCredentialReference`, `TestCredentialNeverLogged`, `TestEventSinksNeedMetricsTelemetry`; C4 changed on purpose; `make check-flagd-full` |
| E2 | implementer | `main` (now) | new module `R/eventsink/webhook` | Q15 | `eventsinktest.Run`; boundary row: core and the standard library only |
| E3 | implementer | E1 | new module `R/service/event-collector` | Q16 | `servicetest.Run` with a port, `TestCollectorPublishesInProcessEvents`, `TestCollectorRejectsInvalidEvents`, `TestCollectorServeBlocksWithoutPort` |
| E4 | implementer | E1 + E2 + E3 | `distributions/flagd-full/manifest.yaml`, regenerated `flagd/`, `test/distributions/export_test.go`; #78 and `accepted.yaml` (C7's two modules) | List the webhook sink and the collector in flagd-full; section 1's UC-021 rows on the binary | all green; `TestFlagdFullOpensTodaysPorts`; upgrade differential green with the new entry; allocs/op unchanged |

### Closing PRs

| PR | Model | Content |
|---|---|---|
| X-011 | mechanic | UC-011 `Implemented`; FR-023, FR-034 statuses |
| X-010 | mechanic | UC-010 `Implemented`, with A1 and BR-005 as UC-002/UC-012 gaps; FR-021 text; NFR-004/NFR-011 wording (Q3); the `distributions.md` table; requirement statuses |
| X-017 | mechanic | UC-017 `Implemented`; A2's example; step 9, step 11 and BR-004 (Q11); the UC-003 and UC-006 flagd-proxy rows; the UC-014 A5 and UC-018 BR-002 flag sync gaps closed |
| X-021 | mechanic | UC-021 `Implemented`; A5 if Q21 takes its alternative; the UC-020 and UC-001 operator-settings gaps closed; FR-039's provisional protocol |

## 4. Gates

In WSL from the repository root (`export GOTOOLCHAIN=go1.27.3`; in a worktree `export GOWORK=$PWD/go.work`; for lint `export GOPATH=$(go env GOPATH)`). CI runs everything except spec lint and ns/op. Implementers run the tests of the packages they touched.

| Gate | Command | Slices |
|---|---|---|
| Unit tests | `make workspace-init && make test` (includes `test-distributions` and `test-flagd-proxy`) | all |
| Lint | `make lint` | all |
| Boundaries, coverage | `make check-module-boundary check-package-coverage` | all; E2 and E3 add rows |
| Evaluator boundary | `cd core && go list -deps ./pkg/evaluator \| grep -E 'opentelemetry\|grpc\|connectrpc\|k8s.io\|gocloud'` prints nothing | all |
| flagd-full generated | `make check-flagd-full` | S2, E1, E4 |
| Graphs | `TestStandardModuleGraphs`; by hand on D's PR: `go version -m distributions/flagd-sync/build/flagd \| grep -E 'evaluator/flagd\|jsonlogic'` prints nothing, and the same for flagd-minimal with NFR-004's prefixes | D |
| Upgrade differential and `#78` | CI's upgrade job; `TestAcceptedMatchesIssue` | I1, E1, E4, C8 |
| Acceptance | `make flagd-integration-test` | S, P2, E4 |
| Benchmarks | CI's allocs/op comparison; the reviewer checks ns/op of flag sync on P2 and of the endpoints on E1 and E4 | P2, E1, E4 |
| Dependencies | the two `golang.org/x/exp` greps | all |
| FIPS | FIPS job with `FULL_CI=true` | D4 |
| Spec | `spec_lint.py --docs docs` on Windows | closing PRs |

## 5. Risks

- **R1, the NFR measure (Q3).** If the project lead keeps the NFRs as written, stack G (appendix A) becomes the largest piece of the step. It runs before D, rewrites or relocates pinned tests and the CI benchmarks, and touches every shared file. Q3's recommendation avoids it.
- **R2, NFR-003 may not hold.** D2 measures it in its first commit. If it fails, Q4's alternative is the lever.
- **R3, "explicit" settings (S).** Viper's merged view reports defaults for bound keys, so Q9 reads each source on its own. Tests: N3, C3, and the configuration-key-at-default row.
- **R4, C-003 surface.** Each item has its own commit and `accepted.yaml` entry:
  - `flagd components` exits 0 (I1);
  - four configuration keys gain meaning (E1, E3);
  - two more linked modules (E4).

  These stay identical: `cli.golden`, the unknown-scheme text in flagd-full (Q7), the flag sync path without the header (C5), and the four ports (Q16).
- **R5, flagd-proxy.** Operators may read "configuration proxy" as "replaces flagd-proxy". Q2's spec edits and the docs table say it does not. flagd-proxy's code is untouched in step 10 (C6). Its sync behaviour already differs from upstream through #78's sync items, which Q20 records and, if accepted, compares.
- **R6, the Builder field (Q7).** `dist.components_hint` is a manifest key that only flagd-full sets to `false`. A custom distribution copied from flagd-full's manifest inherits it; the Builder's manifest docs say what it does.
- **R7, the provisional collector protocol (Q16).** It is versioned (`/events/v1`), marked provisional and inert by default.
- **R8, the generated source changes (E1).** Event sinks and middleware become "available" factories. No repository manifest lists any yet, so only the templates and their goldens change.
- **R9, CI time.** `test-distributions` builds four more distributions plus the parity and flagd-sync runs, against a 30-minute timeout. Measure on D's PR.
- **Boundaries and protobuf.** NFR-005 and NFR-006: E2's and E3's modules carry boundary rows. C-005: no protobuf change, because Q10 uses a header.

## 6. Hand-overs this step closes

| From | Gap | Closed by |
|---|---|---|
| UC-001 | Standard manifests and flagd-minimal's NFR-003/004; settings of absent components stop startup | D, S |
| UC-001 | A module graph without the tests of the runtime and the sync builder | Q3: amended (recommended), or G |
| UC-001 | `flagd components` | I |
| UC-001, UC-020 | Operator settings for extension components and event sinks; distributions with middleware or event sinks | E1 |
| UC-004, UC-020 | Official collector endpoint (FR-039) | E3 |
| UC-004, UC-007, UC-014, UC-018 | Flag sync of vendor documents, FR-044 | P2 |
| UC-006 | A distribution with no observability code | D (flagd-minimal) |
| UC-003, UC-006 | flagd-proxy's `SyncFromURI` and metrics | not delivered: amended (Q2) |

## 7. PR split

| PR | Branch | Base | Content | Runs |
|---|---|---|---|---|
| C | `step-10/pr-1-characterization` | `main` | C1–C8 | now |
| I | `uc-011/pr-1-components-command` | `main` | I1–I3 | now |
| P2 | `uc-017/pr-1-format-requests` | `main` | P2 | now |
| E2 | `uc-021/pr-2-webhook-sink` | `main` | E2 | now |
| S | `uc-010/pr-2-absent-settings` | C | S1–S2 | after C |
| E1 | `uc-021/pr-1-operator-settings` | S | E1 | after S |
| E3 | `uc-021/pr-3-event-collector` | E1 | E3 | after E1 |
| D | `uc-010/pr-3-standard-distributions` | S | D1–D4 | after S |
| P3 | `uc-017/pr-2-flagd-sync` | D + P2 | P3 | after D |
| E4 | `uc-021/pr-4-flagd-full-export` | E3 + E2 | E4 | after E3 |
| G (only under Q3's alternative) | `uc-010/pr-1-module-graphs` | `main` | appendix A | now; D then bases on G + S |
| X-011, X-010, X-017, X-021 | `uc-0xx/pr-n-close` | their last PR | status and spec | last |

Every PR description starts with `Part of #23`, `#22`, `#29` or `#33`; C names all four.

## 8. Changelog (revision 2, `step-10-opposer.md`)

| Finding | Severity | Outcome |
|---|---|---|
| 1 flagd-proxy is not "untouched"; nothing compares it with upstream | major | Resolved. Q2 and R5 now say what is true: step 10 changes no flagd-proxy file; its sync behaviour already follows #78's sync items, which #78 states. New Q20: a flagd-proxy row in UC-009's differential (C8), or an accepted gap owned by UC-012 |
| 2 G1 is not mechanical and rewrites pins | major | Resolved by Q3's new recommendation: amend the NFRs to linked modules, so G is not needed. The survey grew to show 24 internal tests and the CI benchmarks. If the lead keeps the NFRs, appendix A applies the opposer's conditions in full |
| 3 multi-endpoint settings skipped silently | major | Resolved. Q9 maps each setting to a component set and stops when none is present; only the runtime's own settings never stop. C3 checks the sets against `endpoint_settings.go`; a `--context-from-header` row on flagd-sync |
| 4 "except flagd-full" needs a mechanism | major | Resolved. Q7: `dist.components_hint` in the manifest, stamped into `Info.ComponentsHint`, `false` only in flagd-full's manifest (S2). No name check. C2 and S2 pin both sides. R6 names the inheritance risk |
| 5 Q11 contradicts UC-017 step 9 | minor | Resolved. Q11, Q19 and X-017 amend step 9, step 11 and BR-004 for formats other than flagd's |
| 6 `accepted.yaml` entries per #78 item | minor | Resolved. Section 3's preamble, I1, E1 and E4 list the #78 item, `make refresh-issue-78` and the entry. R4 lists `flagd components` exiting 0 |
| 7 UC-021 A5 under noop telemetry | minor | Resolved. New Q21: refuse `event-sinks` without a metrics-serving telemetry provider (recommended), or warn and amend A5 |
| 8 configuration key style | minor | Resolved. Q12: top-level keys in kebab-case like the flags' keys; camelCase inside entries like `sources` and the core's sink keys |
| 9 inert collector and the endpoint contract | minor | Resolved. Q16: an inert `Serve` blocks until `ctx` ends; `servicetest.Run` with a port; `TestFlagdFullOpensTodaysPorts` |
| 10 "explicit" for the configuration file | minor | Resolved. Q9 reads the file's own keys with a separate `viper.New()` and `AllKeys()`; a key at its default value counts as set and has its own row |

## Appendix A: stack G (only if Q3 takes its alternative)

| Step | Model | Change | Check |
|---|---|---|---|
| G0 | implementer | `runtime/runtimetest`: the seam the moved internal tests need (`UpdateAndEmit`, `SourceReadiness`, shutdown hooks, `Bus`), documented as unstable and outside the compatibility promise | depguard row: only test modules import it |
| G1 | implementer | Moves to `test/runtime`. Byte-identical apart from the package line and imports: the seven test packages in subdirectories (`fromconfig_test`, `grpcclient_test`, `managementport`, `propagation_test`, `syncmetrics_test`, `telemetrystartup_test`, `tracingoff_test`). Rewritten onto `runtimetest`, each in its own commit and **each needing the project lead's decision as in UC-001 5a0**: `runtime_span_test.go` (UC-018's pin), `middleware_characterization_test.go`, `selector_characterization_test.go`, `official_components_characterization_test.go` (step 6 and UC-001 pins), `bound_selector_test.go`, `official_endpoints_test.go`, `eventsink_end_to_end_test.go`, `endpoints_test.go` | Each pin's assertions diff empty apart from the receiver of the seam; `runtime/go.mod` requires no forbidden module |
| G2 | implementer | `sync/builder/official` becomes `R/sync/official`; flagd-proxy's import moves (C6 green); `official_test.go`, `source_mapping_test.go` and `syncbuilder_test.go` move to `test/sync-builder` | `sync/builder/go.mod` requires no sync module |
| G3 | implementer | The 14 internal tests of `service/flag-evaluation` and `service/ofrep`, and the 2 of `service/flag-sync`, move to `test/endpoints` behind exported test seams, or are rewritten over stubs. The benchmarks (`bench_test.go`, `http_bench_test.go`) move with their names unchanged, and CI's allocs/op job lists the new path for base and head. `service/transport/metrics_test.go` moves as is | each `go.mod` free of `otel/sdk`, and of `evaluator/flagd` where the package does not import it; allocs/op job compares the same benchmark names |
| G4 | mechanic | Boundary rows for the `go.mod` graphs, `Makefile`, `go.work`, `testdata/go-packages.txt`, every manifest's `replaces`; coverage counted with `-coverpkg` | `make check-module-boundary check-package-coverage`; `TestRepositoryManifestsReplaceEveryRelayModule` |
