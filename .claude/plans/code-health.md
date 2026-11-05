# Code health: ranked findings (#177)

Measured on `origin/main` at `63a46242`, the Relay's own code only: the 150 non-test, non-generated `.go` files that
`git diff -M50% --name-status $(git merge-base upstream/main HEAD) HEAD` marks as added. Files it marks as renamed
from flagd (moved upstream code, for example `sync/flagd-grpc/nameresolvers`, `core/pkg/logger`, most of
`service/transport`) are out of scope.

Tools: `gocyclo -over 15`, `gocognit -over 20`, an AST count of functions over 80 lines, `dupl -t 100`, a scan
for the same function name in several modules, `deadcode -test` over every workspace module, and per-package
coverage from the `make test` log of CI run 41979163113 (main, green) plus a local `go test -cover ./builder/...`
(the builder module writes no coverage line in CI).

## Fixed on their own branches

| Branch | What |
|--------|------|
| `health/slices-contains` | three hand-rolled `contains` (router, service config decoder, Flagsmith sync) replaced by `slices.Contains` |
| `health/service-config-tests` | `core/pkg/service` config decoders: every conversion and error text tested; package coverage 69.2% → 96.3% |
| `health/transport-tests` | `RequestSelector`, `MatchesNoFlags` and the evaluation span names tested; `service/transport` 41.4% → 73.9% |
| `health/build-run-extract` | `build.Run` split into `componentsOf` and `getModules`: 99 → ~60 lines, cyclomatic 25 → 17, cognitive 35 → 22 |

## Remaining findings, ranked

**Plan** marks a finding that changes code on a hot path, touches observable behaviour or files in flight, and
needs the plan → oppose loop. The rest are low risk once their blocker is gone.

| # | Finding | File | Metric | Why it matters | Suggested fix | Risk |
|---|---------|------|--------|----------------|---------------|------|
| 1 | `FromConfig` is the longest function | `runtime/from_config.go:104` | 156 lines, cyclomatic 26, cognitive 31, `nolint: funlen` | Every new setting lands here; it holds two deferred cleanups (telemetry shutdown, endpoints created before a failure) whose order matters | Extract `sourcesOf` (URIs and readiness), `createEndpoints` (returns what it created so the caller's cleanup still sees it) and the runtime struct literal. Wait for #163 and the `uc-022/pr-6-runtime-reset` branch, which change the file | **Plan** (in flight) |
| 2 | The evaluation paths are the most complex functions | `contrib/flagsmith/engine/evaluator.go:85` `Evaluate`; `core/pkg/evaluator/router/router.go:73` `Evaluate`, `:197` `collect` | cognitive 42, 41, 29; router `Evaluate` 87 lines | Per-request code: a regression costs allocations (NFR-001) and changes results clients see | Split the single-flag, bulk and error branches into helpers, behind the existing contract tests and the allocs/op comparison in CI | **Plan** (hot path) |
| 3 | The contract kits have the longest test functions | `core/pkg/sync/synctest/trigger.go:45` `CheckTrigger`, `streaming.go:108` `CheckStreaming`, `:282` `firstConnectCase`; `configformat/flagd/flagdtest/table.go:47` `Run`; `core/pkg/evaluator/evaluatortest/contract.go:512` | 123 and 112 lines; cyclomatic 21–26; cognitive 21–37 | Extension authors read these when their component fails a case; one long function hides which case failed | One function per case, as `servicetest` does; keep the case names, which are in extension authors' test output | low |
| 4 | Settings parsers are complex, and their error texts are observable | `service/event-collector/collector.go:268` `parseEvent`; `sync/sse/trigger.go:182` `parseHeadersFrom`; `sync/s3/s3.go:162` `configOptions`, `:85` `open`; `sync/flagd-grpc/options.go:47` `OptionsFrom`; `runtime/cmd/extensions.go:49` `loadExtensionSettings`; `builder/internal/manifest/parse.go:18` `Parse`; `builder/internal/needs/needs.go:74` `Check` | cognitive 23–37 | Operators see these messages at startup; a refactor that rewords one breaks the drop-in rule | Pin each error text with a characterization test first, then split per setting. `OptionsFrom` waits for step 14 | **Plan** for `runtime/cmd` and `sync/flagd-grpc` (in flight), low for the rest |
| 5 | The same request-key helper in two modules | `core/pkg/evaluator/router/router.go:225`, `contrib/flagsmith/engine/evaluator.go:159` `wantedKeys` | identical logic | Bulk-request semantics (nil `FlagKeys` means all, a single key otherwise) are defined twice and can drift | A method on `evaluator.Request` in core; it is new public API of the evaluator module, so the lead decides | **Plan** (API) |
| 6 | Four endpoints repeat the shared settings keys | `service/flag-evaluation/factory.go:37`, `service/flag-sync/factory.go:30`, `service/ofrep/factory.go:38`, `service/event-collector/collector.go:90` `decodeConfig` | `dupl` hit between flag-evaluation and flag-sync | `port`, `service-name`, `cors`, `context-values`, `max-request-*` are decoded four times; a new common key needs four edits | A `service.CommonKeys(&cfg)` that returns the shared decoder entries. The key list appears in the unknown-setting error, so pin it first | low |
| 7 | Header parsing twice, with different checks | `eventsink/webhook/webhook.go:140`, `sync/sse/trigger.go:161` `parseHeaders` | same shape | The webhook sink checks names against the token grammar and rejects `Host`/`Content-*`; the SSE source's `checkHeaders` only rejects empty names, spaces, colons and line breaks. The rules may differ by accident | Decide whether the rules should match; then one validator in core with each caller's message prefix | **Plan** (error texts) |
| 8 | Builder coverage gaps | `builder/internal/build` 78.9%, `builder/internal/generate` 79.5% (`WriteProbe` 0%, `OutputDir` 66.7%), `builder/internal/manifest/parse.go:135` `scalar` 50% | coverage | The builder writes the source of every distribution; untested branches are manifest shapes nobody checked | Table tests for `scalar`, `parseReplaces`, `WriteProbe` | low |
| 9 | `runtime/cmd` coverage | `runtime/cmd/settings.go:118` `configFileKeys` 0%, `:193` `absentSettingsError` 47%, `root.go:93` `initConfig` 15% | coverage 77.8% | Startup and configuration-file handling are observable behaviour | Tests after the step 13 and 14 changes to `runtime/cmd` land | low (after merge) |
| 10 | Unused option in the testbed | `testbed/control/server.go:48` `WithLogger` | `deadcode` | No caller sets the logger, so the control server always logs to `slog.Default()` | Wire it from the testbed command or drop it, after #163 | low (after #163) |
| 11 | Deprecated option still exported | `evaluator/flagd/json.go:37` `WithEvaluator` | `deadcode`; doc says "will be removed in the next release" | Public API of the evaluator module that in-process providers may still import | Ask the lead whether removing it is a Go API change worth making now | **Plan** (API) |

## Not findings

- `test/inprocess-example/file` and `filegrpc` share 130 lines (`dupl`). Each is its own module so that it proves
  its own module graph; folding them would defeat the example.
- `test/eventsink-example/example.go:133` repeats `webhook.payloads` (`dupl`). The example is meant to be copied
  without depending on the webhook module.
- The other `deadcode` hits are public extension API (`NewFactories` of the Flagsmith sync provider), examples,
  the generated `cmd` of the test distributions, and `Stringer` methods.
- `splitVersion` in `core/pkg/sync/blobfetch` and `sync/http` is a two-line `strings.Cut`; sharing it would add API.
- The blob sync providers (`azblob`, `gcs`, `s3`) already share `core/pkg/sync/blobfetch`; what is left is per vendor.
