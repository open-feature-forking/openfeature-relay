# Cross-Vendor Evaluation: flagd, Flagsmith, GO Feature Flag

Does the flagd-builder component model carry flagd, Flagsmith and GO Feature Flag (GOFF), so that the heavy lifting is shared and each vendor keeps its flexibility?

**Short answer:** yes, with five changes.
The sync provider, store, OFREP and telemetry/middleware slots fit all three almost unchanged.
The configuration-format/evaluator pairing fits, but both vendors ship format and evaluator in one Go module, and neither evaluates one typed flag at a time.
The biggest gap is evaluation data: GOFF exporters and notifiers and Flagsmith analytics have no home.
The proposed home is one new component type, the **event sink**, fed by an event bus that carries more than configuration changes.

## Sources

| Short name | Repository                                     | Commit             | Notes                                                             |
|------------|------------------------------------------------|--------------------|-------------------------------------------------------------------|
| `flagd`    | this checkout (`flagd-builder`, branch `spec`) | `628f4b7`          | `core/`, `flagd/`                                                 |
| `edge`     | github.com/Flagsmith/edge-proxy                | `2612bfd`          | paths under `src/edge_proxy/`                                     |
| `fsgo`     | github.com/Flagsmith/flagsmith-go-client       | `adc44cb`          | engine under `flagengine/`                                        |
| `goff`     | github.com/thomaspoignant/go-feature-flag      | `157db90`          | relay proxy under `cmd/relayproxy/`, engine under `modules/core/` |
| `contrib`  | local `go-sdk-contrib/providers/`              | working tree       | `flagsmith/`, `go-feature-flag/`                                  |
| testbeds   | local `flagsmith-testbed`, `goff-testbed`      | working tree       | `FINDINGS.md` in each                                             |

The Flagsmith Edge Proxy is Python and evaluates with the Python engine (`edge:environments.py:9-13`).
A Flagsmith vendor distribution would use the Go engine `flagengine`; both engines are assumed to agree, which this study did not verify.

## 1. Architecture mapping

### 1.1 flagd (reference)

| Component type        | flagd today                                                                                                                                                                                                  |
|-----------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| Sync provider         | `ISync` push model: `Init`, `Sync(ctx, chan<- DataSync)`, `ReSync`, `IsReady` (`core/pkg/sync/isync.go:13-27`); `DataSync.FlagData` is a string (`:30-41`). HTTP sync does conditional fetches with `If-None-Match` (`core/pkg/sync/http/http_sync.go:194,221`). Scheme regexes in `core/pkg/sync/builder/syncbuilder.go:51-59`. |
| Configuration format  | Not separate yet: `JSON.SetState` parses and writes to the store (`core/pkg/evaluator/json.go:138-157`). YAML is decoded by a shared helper (`core/pkg/utils/yaml.go`).                                     |
| Evaluator             | `IResolver`: five typed methods, `ResolveAsAnyValue`, `ResolveAllValues` (`core/pkg/evaluator/ievaluator.go:42-81`). Context is `map[string]any`.                                                           |
| Store                 | memdb indexed by key, source, flagSetId; source order gives precedence (`core/pkg/store/store.go:24-31,41-43`); `Watch(selector)` drives flag sync and SSE (`:348`).                                       |
| Change notification   | Key-level diff `notifications.NewFromFlags` (`core/pkg/notifications/notifications.go:15`); runtime calls `SyncService.Emit` after `SetState` (`flagd/pkg/runtime/runtime.go:135-158`).                  |
| Service endpoints     | Evaluation gRPC/Connect v1+v2, OFREP with bulk ETag and ADR-0008 `eventStreams` (`flagd/pkg/service/flag-evaluation/ofrep/handler.go:57-63,252-284`), SSE stream (`.../ofrep/sse/service.go`), flag sync gRPC+HTTP (`flagd/pkg/service/flag-sync/handler.go:39-148`). |
| Context enrichment    | Static values and header mappings merged per request (`flagd/pkg/service/flag-evaluation/context_utils.go:29-55`).                                                                                          |
| Middleware, telemetry | CORS, compression, h2c, metrics (`flagd/pkg/service/middleware/`); OTel tracer inside the evaluator (`core/pkg/evaluator/json.go:139-143`).                                                                 |

flagd's own code already has two things the spec does not name: the **OFREP event stream** (SSE) and a **per-selector configuration version** (`sse/tracker.go:160`), computed by fingerprinting `model.Flag` (`sse/tracker.go:203`), so it only works for flagd's format.

### 1.2 Flagsmith (Edge Proxy + Go engine)

| Component type       | Flagsmith                                                                                                                                                                                                                                                                                         | Fit                    |
|----------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|------------------------|
| Sync provider        | `GET {api_url}/environment-document/` with `X-Environment-Key`, `If-Modified-Since` from `updated_at`, 304 accepted (`edge:environments.py:181-213`); poll every 10 s (`edge:settings.py:118-132`, `edge:server.py:32-43`). The Go client follows `Link` pagination for identity overrides (`fsgo:client.go:599-639`) and re-fetches on a realtime SSE ping that carries only `updated_at` (`fsgo:realtime.go:98-136`). | Good: poll with conditional fetch, plus an external refetch trigger. |
| Source               | One environment document per server-side key; the proxy serves many environments, keyed by the paired client key (`edge:settings.py:71-73`, `edge:cache.py:45-69`).                                                                                                                         | Good: one source per environment, credential per source (FR-028). |
| Configuration format | Environment document: `feature_states`, `project.segments`, `identity_overrides`, `updated_at` (`fsgo:flagengine/environments/models.go:11-19`). The engine prepares it once with `MapEnvironmentDocumentToEvaluationContext` (`fsgo:flagengine/engine_eval/mappers.go:21`); identity overrides become synthetic segments (`:49-58,198-284`). | Good, but the prepared form is an engine type (§3.5). |
| Evaluator            | One entry point, `GetEvaluationResult(*EngineEvaluationContext) EvaluationResult`, which evaluates **all** flags for one identity (`fsgo:flagengine/engine.go:119`). Result per flag: `Enabled`, `Value any`, `Reason`, `Variant`, `Metadata{FeatureID}` (`fsgo:flagengine/engine_eval/result.go:12-27`). Reasons are `DEFAULT`, `TARGETING_MATCH; segment=<name>`, `SPLIT; weight=<w>` (`fsgo:flagengine/engine.go:104-108,156`). | Partial: bulk-native, untyped, non-standard reason strings (§3). |
| Identity / traits    | `targetingKey` → identifier, other attributes → traits; identity key is `envKey_identifier` (`fsgo:engine_eval/mappers.go:291-322`, `contrib:flagsmith/pkg/provider.go:49-75`).                                                                                                                 | Vendor-internal.       |
| Store                | Dict keyed by client key; whole-document comparison returns "changed" (`edge:cache.py:11-26`).                                                                                                                                                                                                 | Good.                  |
| Service endpoints    | Vendor API: `GET /api/v1/flags/`, `POST /api/v1/identities/` with traits, `GET /api/v1/environment-document` (raw document for local-evaluation SDKs) (`edge:server.py:95-139`). Wire format drops reason and variant (`edge:mappers.py:10-24`). No OFREP (none in the testbed either). | OFREP is new value; vendor API is an endpoint extension. |
| Middleware           | Key auth that maps `ser.` keys to client keys and 401s unknown keys (`edge:server.py:49-57`); **a client key hides server-only features and, optionally, disabled flags** (`edge:environments.py:92-126`); CORS, GZip (`edge:server.py:142-149`). | Pressure: the credential selects the environment *and* changes results (§3.6). |
| Readiness            | 503 until loaded **or when data is older than poll interval + grace** (`edge:server.py:60-87`).                                                                                                                                                                                                 | Gap: staleness.        |
| Analytics            | Go client counts evaluations per feature and POSTs `{name: count}` to `analytics/flags/` every 10 s (`fsgo:analytics.go:12-85`); newer `EventProcessor` sends `$flag_exposure` events (`fsgo:events.go:21-52`). Edge Proxy: none.                                                              | Gap: no component type. |

### 1.3 GO Feature Flag (relay proxy + `modules/core`)

| Component type       | GOFF                                                                                                                                                                                                                                                                                              | Fit                    |
|----------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|------------------------|
| Sync provider        | `Retriever{Retrieve(ctx) ([]byte, error)}` — pull only (`goff:retriever/retriever.go:11-14`); optional `FormatHinter` (`:23-25`), `Init`/`Shutdown`/`Status` (`:28-66`). Twelve retrievers, all in the root module. The manager polls every retriever in parallel (default 60 s, ±10 % jitter), and any error fails the refresh (`goff:retriever/manager.go:296-342`, `goff:retriever/background_updater.go:16-30`). | Good, but pull, not push (§3.1). |
| Source precedence    | Later retriever wins per flag key (`goff:retriever/manager.go:348-353`).                                                                                                                                                                                                                       | Same as flagd's source order. |
| Configuration format | Decoding (yaml/json/toml) runs in the manager, after retrieval (`goff:retriever/manager.go:334,360-367`, `goff:internal/cache/cache_manager.go:56-72`). DTO → `InternalFlag` (`goff:modules/core/dto/dto.go:9-61`). Validation **drops invalid flags one by one** (`goff:internal/cache/in_memory_cache.go:68-80`). | Good split; partial acceptance clashes with UC-018 BR-001. |
| Evaluator            | `modules/core` is its own Go module, deps only jsonlogic, go-cmp, nikunjy (`goff:modules/core/go.mod`). `evaluation.Evaluate[T](f, key, evalCtx, flagCtx, expectedType, sdkDefault)` (`goff:modules/core/evaluation/evaluation.go:16-67`). `ResolutionDetails` adds `RuleIndex`, `RuleName`, `Cacheable` (`goff:modules/core/flag/resolution_detail.go:5-29`). Extra reasons `TARGETING_MATCH_SPLIT`, `OFFLINE`; extra error code `FLAG_CONFIG` (`goff:modules/core/flag/resolution_reason.go:10-42`, `error_code.go:8-19`). A WASM build wraps the same module (`goff:cmd/wasm`). | Good: closest to our evaluator; needs the caller's default value. |
| Flag sets            | A flag set is a whole `GoFeatureFlag` instance with its own retrievers, exporters, notifiers, enrichment and API keys, chosen by API key (`goff:cmd/relayproxy/config/flagset.go:13-91`, `goff:cmd/relayproxy/service/flagset_manager.go:23-27,206-229`). | Pressure: per-flag-set pipelines (§2.4). |
| Context enrichment   | `evaluationContextEnrichment` per flag set, plus a reserved `gofeatureflag` attribute (`goff:modules/core/ffcontext/context.go:111-126`).                                                                                                                                                   | Same idea as flagd's static context values. |
| Service endpoints    | Echo HTTP, no gRPC. OFREP single and bulk with ETag and `eventStreams` (`goff:cmd/relayproxy/api/routes_ofrep.go:11-25`); vendor API `/v1/allflags`, `/v1/feature/:key/eval`; raw configuration for in-process providers `POST /v1/flag/configuration` with ETag (`routes_goff.go:21-39`); **data collector** `POST /v1/data/collector` (`.../handler/goff/collect_eval_data.go:104-171`); WS and SSE change streams (`routes_stream.go:19-29`); admin refresh. | Good: every one is a service endpoint. |
| Notifiers            | `Notifier{Notify(DiffCache) error}` with Added/Deleted/Updated (`goff:notifier/notifier.go`, `diff_cache.go:9-20`), fired after each cache swap with a diff (`goff:internal/cache/cache_manager.go:74-96`). Slack, Teams, Discord, webhook, log. The relay's own streams and Prometheus counters are notifiers too (`goff:cmd/relayproxy/service/flagset_manager.go:136`). | Gap: subscriber of configuration changes. |
| Exporters            | `Exporter{IsBulk(); Export(ctx, logger, []ExportableEvent)}` (`goff:exporter/exporter.go:20-32`); feature and tracking events (`feature_event.go:46-87`, `tracking_event.go:14-38`); buffered store, 60 s / 100k flush (`data_exporter.go:14-16`); 13 sinks (S3, GCS, Kafka, Kinesis, Pub/Sub, SQS, webhook, BigQuery, OTel …). | Gap: subscriber of evaluation events. |
| Middleware/telemetry | otel, zap, Prometheus, CORS, API-key auth for user and admin (`goff:cmd/relayproxy/api/server.go:72-177`); OTel autoexport (`api/opentelemetry/otel.go:50-93`).                                                                                                                              | Good.                  |
| Persistence          | `persistentFlagConfigurationFile` fallback when the first retrieval fails (`goff:retriever/manager.go:217-236`).                                                                                                                                                                                 | Same as deferred FR-050/051 snapshot. |
| Providers            | In-process provider imports `modules/core` and polls `/v1/flag/configuration` with ETag, sends batched events to `/v1/data/collector` (`contrib:go-feature-flag/pkg/api/api.go:70-140`); `DataCollectorHook` (`contrib:go-feature-flag/pkg/hook/data_collector_hook.go:23-62`). | Matches UC-014; data return path is new. |

## 2. Common vs vendor-specific

### 2.1 Common: candidates for the shared heavy lifting

| Concern                          | flagd                                  | Flagsmith                                     | GOFF                                       | Verdict |
|----------------------------------|----------------------------------------|-----------------------------------------------|--------------------------------------------|---------|
| Polling with conditional fetch   | HTTP: ETag, interval, seed             | `If-Modified-Since`, 10 s                     | 60 s, ±10 % jitter, per-retriever status    | **Share**: one poller in core; vendors write a fetch function. |
| Watching / push                  | file, gRPC, Kubernetes                 | realtime SSE ping triggers a refetch          | none                                        | **Share** the refetch trigger; the push channel stays per provider. |
| Decoding (JSON/YAML/TOML)        | JSON, YAML helper                      | JSON                                          | JSON/YAML/TOML, `FormatHinter`             | **Share**: decoding is not the format. |
| Store with atomic replace        | memdb, source precedence               | dict per environment                          | map per flag set, atomic swap              | **Share** (exists). |
| Source precedence for a key      | later source wins                      | n/a (environments do not merge)               | later retriever wins                        | **Share** (exists, FR-035). |
| Change diff → event              | key-level diff                         | whole-document "changed"                      | `DiffCache`                                 | **Share** the event; the format computes the diff. |
| Configuration version per scope  | SSE tracker fingerprint                | `updated_at`                                  | body hash / Last-Modified                   | **Share**; the format supplies the version. |
| OFREP single/bulk, ETag, SSE     | yes                                    | no (new value)                                | yes, same ADR-0008 shape                    | **Share**: strongest overlap. |
| Raw configuration for in-process | flag sync gRPC                         | `/api/v1/environment-document`                | `/v1/flag/configuration` with ETag          | Store side **shared**; wire protocol per vendor. |
| Context enrichment               | static + header values                 | —                                             | per flag set enrichment                     | **Share**, per selector. |
| Credential → scope               | selector header                        | key → environment                             | API key → flag set                          | **Share** as middleware setting the selector. |
| Readiness                        | first accepted configuration           | plus staleness                                | `Status()` per retriever                    | **Share**; add staleness. |
| Telemetry, CORS, compression     | OTel                                   | logs only                                     | OTel + Prometheus                           | **Share** (exists). |
| Snapshot / persistence           | deferred FR-050/051                    | —                                             | persistent fallback file                    | **Share** (deferred, now has a second user). |
| Event buffering and batching     | —                                      | analytics every 10 s                          | exporter flush every 60 s / 100k            | **Share**: one buffered event pipeline. |

### 2.2 Vendor-specific: stays in the vendor's components

- **Flag model and format**: flagd JSON schema; Flagsmith environment document with segments and identity overrides; GOFF DTO with rules, scheduled and progressive rollouts, experimentation windows.
- **Rule languages**: JsonLogic with flagd operators; Flagsmith segment operators with `$.`-JSONPath and `:semver`; GOFF nikunjy or JsonLogic, detected per rule.
- **Bucketing**: flagd `fractional` (murmur3); Flagsmith MD5 over `[segment or feature key, identity key]`; GOFF bucketing key.
- **Identity mapping**: Flagsmith identifier/traits and `envKey_identifier`; GOFF `anonymous` and reserved `gofeatureflag` attribute; flagd `$flagd` enrichment.
- **Visibility rules**: Flagsmith client keys hide server-only features and disabled flags.
- **Vendor client APIs**: Flagsmith `/api/v1/flags` and `/identities`, GOFF `/v1/allflags`, `/v1/feature/eval`.
- **Event payloads and destinations**: Flagsmith analytics counts, GOFF feature/tracking events.

## 3. Gaps in the taxonomy

| Concept                                   | Seen in                     | Home                                                                                                   |
|-------------------------------------------|-----------------------------|--------------------------------------------------------------------------------------------------------|
| Evaluation data export                    | GOFF exporters, Flagsmith analytics | **New component type: event sink.**                                                          |
| Change notification to people/systems     | GOFF notifiers              | **Event sink**, subscribed to configuration change events.                                             |
| Data collection from in-process providers | GOFF `/v1/data/collector`   | **Service endpoint** that publishes evaluation events onto the event bus.                              |
| OFREP event stream (SSE), WS change stream | flagd, GOFF                | **Service endpoint** (part of OFREP); needs a format-supplied version.                                 |
| Realtime refetch trigger                  | Flagsmith realtime          | **Sync provider** concern; shared poller exposes a trigger.                                            |
| Flag set vs environment vs GOFF flag set  | all three                   | **Extend Flag Set and Selector**; per-flag-set pipelines stay out of the first release.               |
| Identity overrides, traits, segments      | Flagsmith                   | **Vendor-internal** (format and evaluator).                                                           |
| Client-key visibility filtering           | Flagsmith                   | **Middleware** sets a request attribute, the **evaluator** honours it.                                 |
| Server-side hooks                         | none of the three           | **No component type.** Middleware and event sinks cover the before and after positions.              |
| Context enrichment per scope              | flagd, GOFF                 | **Runtime feature** of evaluation endpoints, keyed by selector.                                       |
| Encoding vs format                        | GOFF, flagd YAML            | **Shared decoding** in core; glossary term.                                                           |
| Staleness                                 | Flagsmith, GOFF             | **Readiness** extension.                                                                              |
| Admin refresh                             | GOFF                        | Management port command over `ReSync`; low priority.                                                  |
| Stored identities with persisted traits   | Flagsmith hosted API only   | **Out of scope**; the Edge Proxy does not persist traits either.                                      |

### 3.1 Event sink (new component type)

GOFF exporters, GOFF notifiers and Flagsmith analytics are the same shape: they subscribe to events, buffer, and deliver to an external system.
None of them is telemetry: they carry product data (experiment exposure, audit, analytics) that must be delivered, batched and sometimes retried, while telemetry is operational and may be sampled.
Folding them into the telemetry provider would bind product data to the OpenTelemetry SDK and the evaluator-free boundary (NFR-005) would not help.

The event bus then carries three event kinds:

- **Configuration change**: published by the runtime after a format accepts a changed document; carries source, format, flag set, added/updated/removed keys and the new version. Today's `Emit` and `Notify(ConfigurationChange)` become this event.
- **Evaluation**: published by the evaluation service endpoints after each resolution (not by the evaluator, keeping UC-007 BR-005), and by a data-collection endpoint for events relayed from in-process providers.
- **Tracking**: OpenFeature tracking events, relayed the same way.

The shared part is the bus, a bounded buffer per sink with drop counting, batch flushing and retry; vendors write only the delivery (`Export(ctx, []Event) error`).
Publishing must not slow evaluation: no subscriber, no allocation.

### 3.2 Flag sets, environments, GOFF flag sets

| System    | Scope unit           | How a request picks it          | What differs per scope                                |
|-----------|----------------------|---------------------------------|-------------------------------------------------------|
| flagd     | flag set (`flagSetId` metadata), source | selector header or body | flags only                                            |
| Flagsmith | environment (one document) | environment key           | flags, segments, visibility rules                     |
| GOFF      | flag set (named instance)  | API key                   | flags, retrievers, exporters, notifiers, enrichment   |

All three map onto **selector** addressing, if the format can declare which flag set each document belongs to (Flagsmith: the environment; GOFF: the flag set name; flagd: `flagSetId`) and middleware can derive the selector from a credential.
Routing by key alone (FR-035) breaks for Flagsmith: two environments of one project hold the same feature names, so precedence would pick the wrong environment.
Routing must be by **(selector, key)**, and a vendor format may require a selector that resolves to exactly one flag set.

Per-flag-set exporters, notifiers and enrichment (GOFF) need sinks and enrichment that filter by selector; full per-flag-set pipelines stay out of the first release.

### 3.3 Server-side hooks

None of the three runs OpenFeature hooks on the server.
GOFF covers "before" with context enrichment and "after" with exporters; flagd covers "before" with header mappings.
Middleware (HTTP level), context enrichment (before) and event sinks (after) cover every observed use, so no hook component type is needed and the glossary's "Hook" stays client-side.

### 3.4 Realtime push

- **Outbound** (server → client): flagd and GOFF both advertise SSE through OFREP `eventStreams`; GOFF also has a WebSocket stream. This is common heavy lifting inside the OFREP endpoint, driven by configuration change events. Its version today is a fingerprint of `model.Flag` (`sse/tracker.go:203`), which a vendor format cannot feed; the version must come from the format.
- **Inbound** (vendor → flagd): Flagsmith realtime carries only `updated_at`; the sync provider refetches. A shared poller with a `Trigger()` covers it; no new type.

### 3.5 Format and evaluator ship together

Both vendors' engines contain the format and the evaluator in one Go module: GOFF `modules/core` holds `dto` (format) and `evaluation`; Flagsmith `flagengine` holds the document models, the mapper to the prepared context and `GetEvaluationResult`.
The Flagsmith format's natural document is the prepared `EngineEvaluationContext`, an engine type.
UC-018 BR-005 ("a configuration format module depends on no evaluator module") and its postcondition "exactly one configuration format" cannot hold for them.
Allow an **engine module**: one component module that provides a configuration format and its paired evaluator.
The evaluator-free promise then stays where it matters: flagd-relay with flagd's format (NFR-011); a vendor configuration proxy carries the vendor engine.

### 3.6 Credential-dependent results

A Flagsmith client key sees fewer features than a server key of the same environment.
This is not a selector (same document, different view).
Middleware attaches request attributes (for example `key_kind=client`) to the request context; the evaluation endpoints pass the context through; the vendor evaluator reads them.
The core needs only a documented context key for request attributes.

## 4. Interface pressure

| Interface            | Today / planned                                                                 | Pressure from                                                       | Change |
|----------------------|---------------------------------------------------------------------------------|---------------------------------------------------------------------|--------|
| Sync provider        | Push: `Sync(ctx, chan<- DataSync)`, `IsReady`; `FlagData string`                 | GOFF pull retrievers, Flagsmith conditional fetch and realtime ping | Accept **either** a push provider **or** a fetch function `Fetch(ctx, previousVersion) (data []byte, version string, notModified bool, err error)` that the core poller drives (interval, jitter, conditional fetch, backoff, trigger, readiness, metrics). `DataSync` gains `[]byte`, an encoding hint and a source version. |
| Configuration format | Parse + validate → own documents (UC-018)                                        | All three                                                           | Besides the document, return: the **flag keys and flag set** of each document (routing, selectors), a **version** (ETags, SSE), and a **diff** against the previous document (change events). Decoding moves to core. Partial acceptance allowed only if each dropped entry is reported (GOFF). |
| Evaluator            | Typed methods per type, `ResolveAll` (`ievaluator.go:42-81`)                       | Flagsmith untyped + bulk-native; GOFF generic with expected type and SDK default | Core method is **untyped and bulk-capable**: `Evaluate(ctx, Request) []Result`, `Request{Selector, Keys (empty = all), Context, ExpectedType hint, RequestAttributes}`. Typed resolution, type checks and numeric coercion move to one shared adapter in core used by every endpoint. |
| Result               | value, variant, reason, metadata, error                                         | GOFF `Cacheable`, rule name; Flagsmith `enabled` + value, reason detail | `Result{Key, Value (absent = caller's default), Variant, Reason, ErrorCode, ErrorMessage, Metadata}`. "Absent" covers flagd's `FALLBACK`, GOFF's SDK default and disabled flags. Vendor detail (segment, rule name, weight) goes into metadata. |
| Reasons, error codes | OpenFeature reasons/codes (UC-007 BR-007)                                       | Flagsmith `"TARGETING_MATCH; segment=x"`; GOFF `TARGETING_MATCH_SPLIT`, `OFFLINE`, `FLAG_CONFIG` | Reasons: an OpenFeature reason, or a vendor reason string (the spec allows custom reasons) without parameters. Error codes: the closed OpenFeature set only; vendor codes map to `GENERAL` with a message. The evaluator contract checks this. |
| Evaluation context   | `map[string]any` with `targetingKey`                                             | Flagsmith identity + traits; GOFF reserved attribute                | Keep the OpenFeature shape (`targetingKey` + attributes); mapping to identity/traits is the evaluator's job. Enrichment runs before, per selector. |
| Typed booleans       | value must be bool                                                              | Flagsmith: `enabled` vs value; objects as JSON strings              | The `ExpectedType` hint lets the Flagsmith evaluator answer a boolean with `enabled` and decode a JSON string for an object, as the contrib provider's option does (`contrib:flagsmith/pkg/provider.go:145-154,301-327`). |
| Bulk evaluation      | `ResolveAllValues`                                                              | Flagsmith computes segments once for all flags                      | Bulk is the primary path; single = bulk with one key. |
| Service endpoint     | `Serve`, `Notify`, `Shutdown` (`core/pkg/service/iservice.go:52-56`)              | vendor APIs, data collector, SSE                                    | Inject an **evaluation router** (selector + key → evaluator), store read access to a format's documents with version, the event bus (subscribe and publish), and the telemetry provider. A vendor endpoint may ask the router for its format's evaluator and type-assert it for native results. |
| Store                | flags of flagd's model                                                          | opaque vendor documents                                             | Holds documents per (format, source, flag set) with version; `Watch(selector)` reports versions, not `model.Flag`. |
| Middleware           | wraps HTTP handler                                                              | Flagsmith key mapping, GOFF API keys                                | May set the selector and request attributes in the request context. |

## 5. Recommendation

Adopt the model with these changes, in order of impact:

1. **Event sink component type and a richer event bus** (configuration change, evaluation, tracking). Covers GOFF exporters and notifiers, Flagsmith analytics, and GOFF data collection.
2. **Untyped, bulk-capable evaluator interface with a shared typed adapter**, an "absent value" result and the reason/error-code rule. Unblocks Flagsmith and GOFF evaluators without changing flagd's results.
3. **Engine modules**: a component module may provide a format and its paired evaluator.
4. **Format reports keys, flag set, version and diff; store holds versioned opaque documents; routing by (selector, key).** Makes OFREP ETags, SSE and flag sync work for any format.
5. **Pull sync providers driven by a shared poller**, plus shared decoding of encodings.
6. **Middleware may set selector and request attributes**; context enrichment keyed by selector.
7. **Readiness staleness** and partial acceptance with reported entries (smaller).

### 5.1 `docs/vision.md`

Replace the **Powerful** bullet:

```markdown
- **Powerful**: sync providers, configuration formats, evaluators, service endpoints, event sinks, telemetry and middleware are all selectable component types behind factory interfaces. Any combination of service endpoints is possible: OFREP alone, flag evaluation alone, both, or extensions.
```

Replace the **Vendor distribution** bullet:

```markdown
- **Vendor distribution**: another vendor's sync provider, configuration format and evaluator behind OFREP, for example the Flagsmith engine or GO Feature Flag's evaluation module, so flagd can stand in for that vendor's edge or relay proxy for OpenFeature clients. Each configuration format keeps its own documents in the store, so flagd's flag model stays unchanged; a source names its format, flagd's being the default. Polling, the store, change events, OFREP with its event stream, flag sync, telemetry and event delivery are shared; the flag model, rules, bucketing and identity mapping stay the vendor's.
```

Add to **Scope → In scope for the first release**:

```markdown
- an event sink component type, and an event bus that carries configuration change, evaluation and tracking events
- a shared poller that drives sync providers which only fetch, with conditional requests, jitter, backoff and a refetch trigger
```

Add to **Scope → Out of scope**:

```markdown
- server-side OpenFeature hooks; middleware, context enrichment and event sinks cover the positions before and after evaluation
- stored identities with persisted traits, as some vendor flag services keep them
- per-flag-set pipelines (own sync providers, event sinks and enrichment per flag set)
```

Add to **Success**:

```markdown
- A vendor distribution built from a vendor engine module answers OFREP requests with the vendor's values, variants and reasons, and the shared OFREP endpoint serves its ETags and event stream unchanged.
```

### 5.2 `docs/requirements.md`

New rows (IDs chosen from the free ranges):

```markdown
| FR-036 | Write Custom Event Sink        | As an Extension Author, I want to implement an event sink behind the event sink factory interface in my own module so that flagd delivers configuration change, evaluation or tracking events to a system it does not support, such as a vendor's analytics API. | Medium   | Open     |
| FR-037 | Export Evaluation Events       | As an Operator, I want the flag evaluation and OFREP service endpoints to publish an evaluation event per resolved flag to the event sinks the distribution selects so that experimentation and analytics see every evaluation flagd serves. | Medium   | Open     |
| FR-038 | Notify Configuration Changes   | As an Operator, I want event sinks to receive a configuration change event naming the source, flag set, added, updated and removed flag keys whenever a configuration format accepts a changed document so that people and systems learn about flag changes. | Medium   | Open     |
| FR-039 | Collect Provider Events        | As an Operator, I want a service endpoint that accepts evaluation and tracking events from in-process providers and publishes them on the event bus so that in-process evaluations reach the same event sinks as remote ones. | Low      | Open     |
| FR-045 | Write Fetching Sync Provider   | As an Extension Author, I want to implement a sync provider as a fetch function that the core polls, with conditional requests, interval jitter, backoff and an external refetch trigger, so that I write only the vendor-specific request. | High     | Open     |
| FR-046 | Stream Configuration Changes   | As an Operator, I want the OFREP service endpoint to advertise and serve an event stream and answer bulk requests with ETags for every configuration format so that OFREP clients refresh only when the flags of their selector change. | Medium   | Open     |
| FR-047 | Derive Selector From Credential| As an Operator, I want middleware to bind a request to a selector and to request attributes derived from its credential so that one distribution serves several environments or flag sets, each to its own clients. | Medium   | Open     |
| FR-048 | Ship Engine Module             | As an Extension Author, I want to provide a configuration format and its paired evaluator from one component module so that a vendor engine that holds both can be built in unchanged. | High     | Open     |
| FR-049 | Decode Common Encodings        | As an Extension Author, I want the core to decode JSON, YAML and TOML flag configurations before my configuration format sees them so that my format handles only its schema. | Low      | Open     |
| FR-052 | Report Stale Sources           | As an Operator, I want readiness to fail when a source has not delivered within a configured age so that a load balancer stops routing to a flagd serving outdated flags. | Low      | Open     |
```

Changed rows:

```markdown
| FR-002 | Select Components              | As a Distribution Builder, I want to list sync providers, configuration formats, evaluators, service endpoints, event sinks, telemetry providers and middleware in the manifest so that only those are compiled in. | High     | Open     |
| FR-014 | Write Custom Evaluator         | As an Extension Author, I want to implement an evaluator that resolves one, several or all flags of a selector for an evaluation context and returns untyped results, so that flagd evaluates flags with another engine, such as the Flagsmith engine or GO Feature Flag's evaluation module, while type checks stay in the core. | Medium   | Open     |
| FR-019 | Write Configuration Format     | As an Extension Author, I want to implement a configuration format that validates another vendor's flag configurations and reports each document's flag keys, flag set, version and changes so that the store, routing, event streams, a configuration proxy and my evaluator all work with that format. | Medium   | Open     |
| FR-035 | Evaluate Formats Side by Side  | As an Operator, I want the flag evaluation and OFREP service endpoints to resolve each flag of a request's selector with the evaluator of the configuration format that holds it, following today's source precedence for a key held twice within that selector, so that flagd and vendor flags are served by one distribution. | Medium   | Open     |
```

New NFR:

```markdown
| NFR-013 | Event Publishing Overhead     | With no event sink selected, publishing evaluation events adds no allocation to the evaluator benchmarks; with sinks selected, a full sink buffer drops events and counts the drops instead of blocking evaluation. | Performance     | High     | Open   |
```

### 5.3 `docs/glossary.md`

New rows:

```markdown
| Event Sink            | A component type that subscribes to events on the event bus (configuration changes, evaluations, tracking) and delivers them to an external system, buffered and in batches where needed. | exporter, notifier, data collector |
| Evaluation Event      | The event a service endpoint publishes for each resolved flag, or relays from an in-process provider: flag key, flag set, variant, value, reason, targeting key and origin. | feature event |
| Configuration Change Event | The event the runtime publishes when a configuration format accepts a changed document: source, configuration format, flag set, added, updated and removed flag keys, and the new version. | diff |
| Tracking Event        | An OpenFeature tracking event relayed to event sinks. | |
| Engine Module         | A component module that provides a configuration format and the evaluator paired with it, as vendors ship their engines. | |
| Fetching Sync Provider | A sync provider that only fetches a source on request; the core's poller schedules it, sends conditional requests and refetches on a trigger. | retriever |
| Encoding              | The serialization of a flag configuration (JSON, YAML, TOML), decoded by the core before its configuration format validates it; independent of the configuration format. | file format |
| Document Version      | An opaque value a configuration format reports for each document, such as a hash or the vendor's update time; service endpoints derive ETags and event stream events from it. | etag |
| Event Stream          | The OFREP server-sent event stream that announces configuration changes for a selector, advertised in OFREP bulk responses. | SSE endpoint |
| Context Enrichment    | Values flagd adds to an evaluation context before resolution: static values, header mappings, and per-selector values. | |
| Request Attribute     | A value middleware attaches to a request, such as the kind of credential used, that evaluators may read; not part of the evaluation context. | |
| Evaluation Router     | The part of the runtime that picks, for a selector and flag key, the configuration format holding the flag and its evaluator. | |
| Staleness             | The age of a source's last accepted delivery; beyond a configured limit, readiness fails. | |
```

Changed rows:

```markdown
| Component Type        | The kind of a component: sync provider, configuration format, evaluator, service endpoint, event sink, telemetry provider or middleware. | |
| Component Module      | A Go module with its own go.mod that provides one component, or a configuration format with its paired evaluator (an engine module). | |
| Evaluator | The component type that resolves one, several or all flags of a selector for an evaluation context from the documents of its paired configuration format, returning untyped results with OpenFeature reasons and error codes; it emits no telemetry. | resolver |
| Configuration Format | A component type that validates flag configurations of one format (flagd's JSON schema, a vendor's format) and keeps them in the store as its own documents, which only its paired evaluator reads; it reports each document's flag keys, flag set, version and changes. | parser |
| Flag Set              | A named group of flags a selector can address. The configuration format decides how its documents map to flag sets: flagd by `flagSetId` metadata, Flagsmith by environment, GO Feature Flag by flag set name. | environment (for the generic term) |
| Selector              | An expression a client sends, or middleware derives from the client's credential, to restrict a query, an evaluation or a flag sync subscription to the flags of certain sources or flag sets. | |
| Event Bus             | The publish/subscribe channel through which the runtime and service endpoints announce configuration change, evaluation and tracking events to service endpoints and event sinks. | |
| Middleware            | A component type that wraps a service endpoint's HTTP handler with cross-cutting behaviour; it may bind a request to a selector and attach request attributes. | interceptor, filter |
| Hook                  | An OpenFeature hook; it runs in the client SDK. flagd has no server-side hooks: middleware, context enrichment and event sinks take their positions. | |
| Store | The in-memory holder of the documents each configuration format accepted from its sources, with their versions; evaluators read their own format's documents from it, and the flag sync and event stream endpoints watch it. | memdb |
| Readiness             | The state in which every configured source has delivered a flag configuration that its configuration format accepted, and none is stale; flagd reports it on the management port. | |
```

### 5.4 Use cases to update

| Use case | Change |
|----------|--------|
| UC-003 Develop Sync Provider Extension | Alternative path: a fetching sync provider (FR-045); contract cases for conditional fetch and refetch trigger. |
| UC-004 Develop Service Endpoint Extension | Endpoint gets the evaluation router, document versions and the event bus; examples: vendor client API, data collector (FR-039). |
| UC-005 Develop Middleware Extension | Middleware that derives a selector and request attributes from a credential (FR-047). |
| UC-007 Develop Evaluator Extension | Untyped bulk interface; rewrite BR-007 (vendor reasons allowed, error codes closed); BR-002/BR-006 for engine modules. |
| UC-014 Embed Evaluator in In-Process Provider | Vendor in-process providers embed an engine module; typed adapter comes from core. |
| UC-016 Recover From Snapshot | Note GOFF's persistent file as prior art; snapshot holds document versions. |
| UC-017 Run Configuration Proxy | Vendor configuration proxy carries the vendor engine module; vendor sync protocols (`/environment-document`, `/v1/flag/configuration`) are endpoint extensions. |
| UC-018 Develop Configuration Format Extension | Report keys, flag set, version and diff; BR-001 partial acceptance with reported entries; BR-005 and the "exactly one" postcondition relaxed for engine modules; decoding moves to core (FR-049). |
| UC-019 Serve Vendor Flags | Several environments in one distribution via key → selector middleware; client-key visibility through request attributes; second example GOFF; staleness in A3. |
| New UC-020 Develop Event Sink Extension | Extension Author, FR-036, contract cases: batching, drop counting, shutdown flush. |
| New UC-021 Export Evaluation Data | Operator wires event sinks for evaluation and change events, FR-037/038/039, NFR-013. |

## 6. Risks and open questions

- **Engine parity**: a Flagsmith distribution uses the Go engine; the Edge Proxy uses the Python engine. Flagsmith's shared engine test data should be part of that evaluator's contract.
- **Edge Proxy wire format**: Flagsmith clients that call `/api/v1/flags` expect `feature.id`, `feature.type`, `enabled`, `feature_state_value`. The vendor endpoint needs native results, which is why the router exposes the evaluator for type assertion.
- **Identity override pagination**: the Edge Proxy does not follow `Link` pagination (`edge:environments.py:181-213` vs `fsgo:client.go:599-639`); a Flagsmith sync provider should.
- **GOFF's monolith**: all retrievers and exporters sit in GOFF's root module, so reusing them as components drags in its whole dependency graph; GOFF components would be thin rewrites over `modules/core`, not imports.
- **Selector-required formats**: making a selector mandatory for a Flagsmith format changes how clients without one are answered; decide between an error and a configured default flag set.
- **Event schema**: the evaluation event needs a stable shape before vendors write sinks; OpenTelemetry's `feature_flag.evaluation` semantic conventions are the natural base.
