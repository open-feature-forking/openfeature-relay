---
description: how an Extension Author runs the contract tests of a component
---

# Contract tests

The contract tests check that an extension behaves as a distribution expects, before the extension is built into one.
Each component type has a runner in the core module. The Extension Author hands the runner the component's factory and
the test inputs of its type, from an ordinary Go test:

```go
func TestContract(t *testing.T) {
    synctest.RunFetcher(t, myFactory{}, synctest.FetcherInputs{
        Source: sync.SourceConfig{URI: "example://flags"},
        Change: func(testing.TB) { source.Set(changedFlags) },
        Break:  func(testing.TB) { source.Fail(true) },
    })
}
```

## What every runner does

- **One entry point.** `Run(t *testing.T, factory, inputs)` gives one subtest per contract case. `Check(t, factory, inputs)`
  returns a `contracttest.Report` without failing a test, for the runner's own tests. `synctest` has a pair for each of its
  three component types: `RunFetcher`/`CheckFetcher`, `RunTrigger`/`CheckTrigger` and `RunStreaming`/`CheckStreaming`.
- **Declarations first.** The runner checks what the factory declares with `ValidateDeclarations` and aborts the run, naming
  the rule, when it is broken.
- **A run that cannot exercise the contract fails.** A missing test input, invalid declarations or a factory that cannot
  create the component abort the run, and the failure names the input and the cases that need it. No case is skipped.
- **A failed case states the expected and the observed behaviour.**
- **The component is created only through its factory**, with what a distribution injects.

## Cases that do not apply

A component may lack an optional feature. Where a runner has `Without`, the author names the features the component does not
have. A feature is either supplied through its test input or named in `Without`; one that is neither fails its case, naming
the input, and one that is both aborts the run. A case behind a named feature is reported as "not applicable", with the
feature's name, and does not fail the run.

Some components differ in what a case asserts. These are declared as modes, with the same names in every runner, and a case
that runs in a mode states the mode in its expected text:

- `NoConfig`: the component takes no configuration, so the invalid-configuration case checks that it accepts any;
- `NoBackend`: the telemetry provider delivers nothing anywhere, as the noop provider does;
- `NeverAnswers`: the middleware passes every request on.

## Runners

| Component | Runner | Test inputs | `Without` features | Modes | Example |
|---|---|---|---|---|---|
| Configuration format | `formattest.Run` | `Samples`: `Valid`, `Changed`, `Invalid`, and optional `Incremental`, `Dropped` | `Incremental`, `Dropped` | | `test/configformat-example` |
| Evaluator | `evaluatortest.Run` | `Samples`: `Static`, `Setup`, `Config` and optional samples | `Disabled`, `AbsentDefault`, `Attribute`, `Paired`, `Shared` | | `test/configformat-example/evaluator` |
| Fetcher | `synctest.RunFetcher` | `FetcherInputs`: `Source`, `Change`, `Break` | | | `test/sync-example` |
| Fetch trigger | `synctest.RunTrigger` | `TriggerInputs`: `Signal`, `Interrupt`, `Config` | | | `test/sync-example` |
| Streaming sync provider | `synctest.RunStreaming` | `StreamingInputs`: `Source`, `Expect`, `Change`, `Break`, `Restore`, `Remove`, `OnRemove`, `Unavailable`, `Available` | | | `sync/flagd-grpc` (its own tests) |
| Service endpoint | `servicetest.Run` | `Inputs`: `PortKey`, `Config`, `Connect`, and the inputs of the features | `Documents`, `Validator`, `Listen`, `SendEvents`, `HTTP` | | `test/service-example` |
| Middleware | `middlewaretest.Run` | `Inputs`: `Config`, `Passed`, `Answered`, `Binds`, `InvalidConfig`, `FaultyValue` | | `NeverAnswers`, `NoConfig` | `test/middleware-example` |
| Telemetry provider | `telemetrytest.Run` | `Inputs`: `Config`, `InvalidConfig`, `FaultyValue`, `StallBackend`, `Observed` | | `NoBackend`, `NoConfig` | `test/telemetry-example` |
| Event sink | `eventsinktest.Run` | `Inputs`: `Config`, `InvalidConfig`, `FaultyValue`, `Received`, `Stall`, `Refuse` | | `NoConfig` | `test/eventsink-example` |

The fetch trigger's contract checks that it signals for its change signal and for nothing else, that it signals again after it
lost the signal, and that it stops on shutdown. That it keeps the other fetch triggers of its source running when it fails
is the fetching sync provider's, and is tested there.

## Running outside the repository

The example modules depend on the core module only, and `make test-contract-external` runs their contract tests from a copy
outside the checkout, with the core module fetched by version: no workspace, no submodule and no path into the repository.
That is how an Extension Author's module runs them. See `test/contract-external.sh` for the list of modules and how the core
module is fetched for a commit that is not on the remote.

The evaluator contract for flagd's configuration format, the testbed's evaluator suite, runs from `test/evaluator-testkit`
only inside this repository, because the testbed is a pinned submodule that carries no licence to redistribute it. An
Extension Author whose evaluator reads flagd's configuration format checks it against the suite in a checkout of this
repository.
