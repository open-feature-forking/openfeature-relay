# test/testbed

The launchpad contract (UC-022): what a flagd testbed image does on its control port, as the provider test suites rely
on it. The test is parameterized by image, so the same cases run against the upstream testbed image today and against
the relay testbed later.

```bash
make testbed-launchpad-image                       # builds testbed-launchpad:local from the test-harness submodule
make testbed-contract IMAGE=testbed-launchpad:local
make testbed-contract IMAGE=<relay testbed image> KIND=relay
```

`KIND` is `launchpad` (the default) or `relay`. Cases marked launchpad-only pin quirks of the upstream launchpad that the
relay testbed fixes on purpose (the remembered failed `/start`, the 10 s answer for an unknown configuration, and the
layout of the rewritten `rawflags/changing-flag.json`); they run only for `KIND=launchpad`. Every other case runs on
both.

The goldens in `testdata/` are the bytes of `/flags/allFlags.json` at start and after one and two `/change` calls.
`TESTBED_UPDATE_GOLDEN=1` rewrites them from the image under test; review the diff before committing.

The test talks to the container's bridge address, so it needs a Docker daemon on the same host (Linux, or WSL).
