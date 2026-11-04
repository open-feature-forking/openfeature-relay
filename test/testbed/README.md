# test/testbed

The launchpad contract (UC-022): what a flagd testbed image does on its control port, as the provider test suites rely
on it. The test is parameterized by image, so the same cases run against the upstream testbed image today and against
the relay testbed later.

```bash
make testbed-launchpad-image                       # builds testbed-launchpad:local from the test-harness submodule
make testbed-contract IMAGE=testbed-launchpad:local
make testbed-contract IMAGE=<relay testbed image> KIND=relay
```

`KIND` is `launchpad` (the default) or `relay`. Four cases pin quirks of the upstream launchpad that the relay testbed
fixes on purpose, and each has a branch per kind:

| Quirk | `KIND=launchpad` | `KIND=relay` |
|---|---|---|
| 1. A failed `/start?config=nope` is remembered | a restart and a bare `/start` fail too | `/restart?seconds=0` reaches ready and a bare `/start` answers `200` |
| 2. An unknown configuration answers late | `500` after at least 10 s, "health check timed out" | `500` within 100 ms, `Failed to start flagd: unknown configuration "nope"` |
| 3. `/change` rewrites `rawflags/changing-flag.json` | the layout is a golden | the file is unchanged |
| 4. A failed start leaves nothing listening | 8013, 8014 and 8015 refuse connections | 8013 still serves |

Every other case runs on both kinds. The relay branches run against any `IMAGE` with `KIND=relay`.

The launchpad image is the oracle. `make testbed-launchpad-image` builds the testbed Dockerfile of the test-harness
submodule with its default base, upstream flagd v0.18.1 pinned by digest, and not the way `test/integration` builds flagd.
The ends of the idle event and sync streams during an outage are recorded in `testdata/outage-stream-ends.golden` from
that image, and the relay testbed must end them the same way.

The goldens in `testdata/` are the bytes of `/flags/allFlags.json` at start and after one and two `/change` calls.
`TESTBED_UPDATE_GOLDEN=1` rewrites them from the image under test; review the diff before committing.

The test talks to the container's bridge address, so it needs a Docker daemon on the same host (Linux, or WSL).
