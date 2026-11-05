---
description: The concurrency load test that keeps flagd-minimal correct while many clients read and its flags change
---

# The load test

The load test checks that flagd-minimal stays correct under concurrent clients while its flags change (NFR-017).
It is pass or fail, not a benchmark: k6 reports latency and throughput, and nothing gates on them.

## What it proves

flagd-minimal, built by the relay builder with `-race`, reads its flags from a file.
A conductor rewrites that file with a new flag generation on a fixed schedule, every change interval, by an atomic rename, while k6 runs these clients at once:

- OFREP single and bulk evaluation;
- RPC evaluation over `flagd.evaluation.v1`, `flagd.evaluation.v2` and `schema.v1`, and their `EventStream` calls;
- flag sync streams, held for the whole run or reopened every few seconds, with and without a selector, and `FetchAllFlags`.

The run passes when:

- every answer comes from a generation inside the change window: one written between the apply bound before the request and the moment the answer came back;
- a bulk answer holds one generation, and a client never sees a generation older than one it saw before;
- every flag sync stream and `EventStream` receives every generation written while it is connected; a stream that lags by more than one change interval fails as `stream_lag`;
- there are no transport or status errors, flagd stays ready, exits cleanly, and the race detector reports nothing.

A late apply is reported as `apply_late` ("slow"), apart from a wrong answer, `stale_answer` ("wrong").
A self-test runs before each smoke and proves every check rejects a wrong answer and accepts a right one.

## The apply bound

The file source polls the file's status once a second (with fsnotify only inside Kubernetes), so a generation reaches the store up to about a second after it is written.
The apply bound `D` is 3.1 s and the change interval `P` is 4.1 s.
`D` covers the one-second poll with room to spare: on a GitHub runner, the worst apply latency over 140 generations against the race build was 1.35 s.
`P` stays above `D` plus the clock slack, so the window holds at most two generations.
`RELAY_LOAD_D` and `RELAY_LOAD_P` (in milliseconds) override them for a measurement.

## Running it

The load runs in Docker: k6 in a container on the host network, pinned by digest (`K6_IMAGE`).
It needs Docker Engine on the same kernel as flagd, as in CI and in WSL; with Docker Desktop k6 cannot reach flagd and its clock is the VM's, so the test skips locally and fails in CI.

| Command | What it runs | In CI |
|---------|--------------|-------|
| `make test-concurrency` | the Docker-free tests: the generator against the checker's fixture, the conductor | `test`, every build |
| `make test-load-smoke` | the self-test, then 60 s of load with a few clients of each kind | `load-smoke`, every build |
| `make test-load` | 10 min of load with about 120 clients, plus k6's stream close behaviour and the negative test (a skipped write must fail the run) | `load-test`, with `FULL_CI=true` or a manual run |

On a failure CI uploads flagd's stderr and k6's summary as an artifact.

The manual `measure-load` workflow measures what the rates and `D` are set from, on a GitHub runner: flagd's CPU at each request rate, and the apply latency over a full run.
Run it from the Actions tab, after a change that could make flagd slower to apply a change or to answer under `-race`.

## Not covered yet

- OFREP's event stream: k6 has no core server-sent events client ([#225](https://github.com/open-feature-forking/openfeature-relay/issues/225)).
- Other distributions, TLS, a second source, and flag changes faster than `P`.
