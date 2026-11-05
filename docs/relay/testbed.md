---
description: The flagd testbed built by the relay builder, and how its control module drives a runtime
---

# The testbed

The provider test suites run against a flagd testbed: flagd plus a control API on port 8080 that the suites call to start, stop, restart and change flagd ([UC-022](../use_cases/UC-022-run-provider-test-suites-against-the-testbed.md)).
In this repository the testbed is a distribution, built by the relay builder from `distributions/flagd-testbed/manifest.yaml`.
It is a drop-in for the flagd-testbed image: the suites, the compose file, the flags and the configurations of `test-harness` are used unchanged, and CI runs the RPC and in-process suites against both images and compares the scenarios that pass.

The testbed is a test harness.
No standard distribution contains a module under `testbed/` (NFR-015).

## The pieces

| Piece | Module | What it does |
|-------|--------|--------------|
| control module | `testbed/control` | serves the Launchpad API (`/start`, `/stop`, `/restart`, `/change`) on its own port, holds the combined flags in memory, renders them to `allFlags.json`, and drives a runtime through a control target |
| listener gate | `service/transport/gate` | the layer through which the runtime opens its listeners; closing it closes every port the way a killed process does |
| runtime reset | `runtime` (`Runtime.Reset`) | empties the store and loads every source again, so every start begins from the sources' flags |
| testbed source | `testbed/source` | a sync provider with the scheme `testbed:` that serves the combined flags from memory, so the runtime reads no file the testbed changes |
| flagd target | `testbed/cmd` | the control target for flagd: the gate, the reset, the testbed source and a configuration switch |

The manifest holds only what the pinned suites use: the file and testbed sync providers, flagd's configuration format and evaluator, the flag evaluation and flag sync endpoints, and noop telemetry.
`TestTestbedManifestIsMinimal` fails when a component is added without a suite that needs it.

## The listener gate

`/stop` and `/restart` must look like a stopped process to a provider: new connections are refused and open streams break.
A middleware that rejects requests would leave the port open and the streams alive, and providers would take another code path.

The runtime opens every listener through the gate's `Listen`, which has the signature of `net.Listen`.

- **Close** closes every listener, so new connections are refused, and closes every accepted connection with a plain close: a FIN, with no `GOAWAY` or other goodbye at the application level.
- **Open** binds the same addresses again, a port chosen by the system included.
- The runtime keeps running behind a closed gate, and the control port is outside it.

## The reset

A `/start` with the configuration in use keeps the process and the runtime.
The testbed closes the gate, calls `Runtime.Reset`, and opens the gate once the runtime is ready again.

`Reset` removes every flag and document in one step, takes the runtime out of readiness, and has every sync provider deliver its full flag configuration again, an unchanged source included.
It returns when every source is accepted again, or names the sources that were not when the start timeout ends.
A `/start` with another configuration builds a new runtime in the same process instead.

`/change` toggles `changing-flag` in the control module's in-memory state and hands the new document to the target.
The target fires the testbed source and waits until the store holds that version (`Runtime.WaitAccepted`), and only then does the control module render `allFlags.json`, atomically.
The raw flag files are never written.

## The control module and its target

The control module knows no flagd type, so the testbed of another provider SDK or vendor can reuse the server, its texts and timeouts, and its tests.
It drives what is under test through `control.Target`:

```go
type Target interface {
    Start(ctx context.Context, config string) error              // clean and ready, still closed to clients
    Open(ctx context.Context) error                              // open to clients, after the render
    Stop(ctx context.Context) error                              // closed to clients, still running
    Load(ctx context.Context, document []byte, version string) error // after /change: return once held
    Ready() bool
}
```

The server calls one method at a time.
`Start` returns an error wrapping `control.ErrUnknownConfiguration` for a configuration the target does not have; the server answers it at once and leaves the target as it was.
A target reads the combined flags from `Server.Document` when it loads that source.

A second target needs only those five methods:

```go
type myTarget struct{ srv *mytestbed.Server }

func (t *myTarget) Start(ctx context.Context, config string) error { return t.srv.Restart(ctx, config) }
func (t *myTarget) Open(context.Context) error                     { return t.srv.Listen() }
func (t *myTarget) Stop(context.Context) error                     { return t.srv.CloseListeners() }
func (t *myTarget) Load(ctx context.Context, doc []byte, v string) error { return t.srv.Apply(ctx, doc, v) }
func (t *myTarget) Ready() bool                                    { return t.srv.Healthy() }

func main() {
    s := control.New(control.Config{}, &myTarget{srv: mytestbed.New()})
    log.Fatal(s.Serve(context.Background()))
}
```

`control.Config` defaults reproduce the launchpad of flagd-testbed v3.11.0: port 8080, `/configs`, `/rawflags`, `/flags/allFlags.json`, a start timeout of 10 seconds and a change timeout of 5 seconds.
The flagd testbed reads overrides from `TESTBED_*` environment variables.

Four launchpad quirks are fixed (UC-022 BR-001): a failed start keeps the last good configuration, an unknown configuration is answered at once, an unknown configuration takes no port down and cancels no pending restart, and `/change` rewrites no file.

## Running it

| Command | What it runs |
|---------|--------------|
| `make testbed-relay-image` | builds the relay testbed image `flagd-testbed:local` |
| `make testbed-contract-both` | the launchpad contract against the flagd-testbed image and the relay testbed |
| `make testbed-suites` | the RPC and in-process suites against both images, with the passing scenarios compared |
| `make testbed-rpc-outages` | the RPC outage scenarios (`@reconnect`, `@events`, `@grace`); in CI with `FULL_CI` or a manual run |

## Not in the testbed yet

- Fault injection and `/faults`: [#194](https://github.com/open-feature-forking/openfeature-relay/issues/194).
- A public `/fetch` call: [#195](https://github.com/open-feature-forking/openfeature-relay/issues/195).
- An event sink that records evaluation events: [#196](https://github.com/open-feature-forking/openfeature-relay/issues/196).
- OFREP, the other languages' suites in CI, and the `@fractional-v1` and `@fractional-v2` scenarios ([#47](https://github.com/open-feature-forking/openfeature-relay/issues/47)).
