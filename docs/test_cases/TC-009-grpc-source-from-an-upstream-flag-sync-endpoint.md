# Test Case: gRPC Source From an Upstream Flag Sync Endpoint

## Overview

**ID:** TC-009  
**Goal:** An Operator points a `grpc` source of flagd-full at the relay testbed's flag sync endpoint with the provider's gRPC sync settings, and flagd-full validates them, syncs, reconnects after an outage with the configured backoff, recycles the stream at its deadline, and ends when the upstream refuses it with a fatal status code.  
**Priority:** High  
**Status:** Draft

## Roles

- Operator (configures the `grpc` source and starts flagd-full)
- Upstream (the relay testbed's flag sync endpoint on port 8015, or an Envoy proxy that answers `PERMISSION_DENIED`)

## Preconditions

- The images `testbed-relay:grpc-sync` (`distributions/flagd-testbed/Dockerfile`) and `flagd-full:grpc-sync` (`flagd/build.Dockerfile`) are built by `make testbed-grpc-sync`.
- The Envoy image and the configuration `envoyPermissionDenied` of `test/testbed/grpcsync_test.go` provide the refusing upstream on port 9212.
- Docker is available (`testbed-grpc-sync` job).

## Flow

| Step | Name | Description | Test Data | Use Case | Covered by |
|------|------|-------------|-----------|----------|------------|
| 1 | Configure grpc object | The Operator sets the keys of a `grpc` object on a `grpc` source | `"grpc":{"retryBackoffMs":500,"retryBackoffMaxMs":2000}` | [UC-023](../use_cases/UC-023-sync-from-an-upstream-flag-sync-endpoint.md) | `TestGRPCObjectParsedFromSources` (`sync/builder`); `TestGRPCObjectDecodedStrictly`, `TestEmptyGRPCObjectDecodes` (`core/pkg/sync`) |
| 2 | Validate settings | flagd-full validates the object before it starts anything | `"keepAliveTime":5000` rejected | [UC-023](../use_cases/UC-023-sync-from-an-upstream-flag-sync-endpoint.md) | `TestSettingsValidation`, `TestEmptyGRPCObjectChangesNothing` (`sync/flagd-grpc`); `TestConfigFileGRPCObjectIsStrict` (`runtime/cmd`); `TestGRPCObjectRejectedOnOtherProviders` (`sync/builder`) |
| 3 | Sync and verify ready | flagd-full syncs the source, reports it ready as today or after its first payload with `readyOnFirstPayload`, and resolves the synced flags | `"readyOnFirstPayload":true` | [UC-023](../use_cases/UC-023-sync-from-an-upstream-flag-sync-endpoint.md) | `TestReadyWhenStreamOpensByDefault`, `TestReadyOnFirstPayloadOptIn`, `TestReadinessCharacterization` (`sync/flagd-grpc`); end to end: `TestGRPCSourceReconnects` (`test/testbed`) |
| 4 | Send selector and keepalive | flagd-full sends the selector as the `flagd-selector` header and in the request body, and keeps the connection alive with HTTP/2 pings | `"selectorHeader":true`, `"keepAliveTime":10000` | [UC-023](../use_cases/UC-023-sync-from-an-upstream-flag-sync-endpoint.md) | `TestSelectorHeaderOnBothCalls`, `TestKeepaliveParameters` (`sync/flagd-grpc`) |
| 5 | Restart upstream | The testbed's `/restart` closes its listeners; flagd-full re-opens the stream no sooner than `retryBackoffMaxMs` after it ended and keeps its last accepted flags meanwhile | `/restart?seconds=3`, `retryBackoffMaxMs` 2000 | [UC-023](../use_cases/UC-023-sync-from-an-upstream-flag-sync-endpoint.md) | `TestRetryDelayFixedOnceOptedIn`, `TestSingleBackoffKeyFillsTheOther`, `Test_SyncRetry`, `TestRetryPolicyReachesTheDial` (`sync/flagd-grpc`); end to end: `TestGRPCSourceReconnects` (`test/testbed`) |
| 6 | Recycle at deadline | With a stream deadline, flagd-full re-opens the stream at the deadline with no error and no delay | `"streamDeadlineMs":2000,"retryBackoffMaxMs":5000` | [UC-023](../use_cases/UC-023-sync-from-an-upstream-flag-sync-endpoint.md) | `TestStreamDeadlineRecyclesWithoutDelay`, `TestStreamWithoutDeadlineIsNotRecycled`, `TestFatalCodeIgnoredOnRecycle` (`sync/flagd-grpc`); end to end: `TestGRPCSourceStreamDeadline` (`test/testbed`) |
| 7 | Refused with fatal code | Against an upstream that answers `PERMISSION_DENIED` before the first payload, flagd-full ends naming the source and the code; without the code listed it keeps retrying | `"fatalStatusCodes":["PERMISSION_DENIED"]` | [UC-023](../use_cases/UC-023-sync-from-an-upstream-flag-sync-endpoint.md) | `TestFatalCodeOnFirstRecvStopsSync`, `TestFatalCodeAfterFirstPayloadIsRetried`, `TestStatusNameIsWhatTheOperatorWrites` (`sync/flagd-grpc`); `TestRuntimeExitsOnFatalGRPCCode` (`runtime`); end to end: `TestGRPCSourceFatalCode` (`test/testbed`) |
| 8 | Verify exit | flagd-full exits with a non-zero code and logs the source and the code | - | - | `TestGRPCSourceFatalCode` (`test/testbed`); `TestRuntimeExitsOnFatalGRPCCode` (`runtime`) |

The `sync/flagd-grpc`, `sync/builder`, `core`, `runtime` and `runtime/cmd` tests run in the `test` job (`make test`) on every pull request. The `test/testbed` tests (`TestGRPCSource*`) run flagd-full against the relay testbed only in the `testbed-grpc-sync` job (`make testbed-grpc-sync`), with `FULL_CI` or a manual run.

## Validation

1. **Opt-in only**: A `grpc` source without a `grpc` object syncs as before, and an invalid object stops flagd-full before it starts anything.
2. **Provider timing**: The re-opened stream after an outage waits at least `retryBackoffMaxMs`, and a deadline recycle waits nothing.
3. **Fatal refusal**: A listed status code before the first payload ends flagd-full; an unlisted one is retried.

## Postconditions

- The images `testbed-relay:grpc-sync` and `flagd-full:grpc-sync`; the testbed, Envoy and flagd-full containers are removed when the tests end.

## Gaps

None in the flow: every step is covered on every pull request at the level of the sync provider and the runtime. The end-to-end run of flagd-full against the relay testbed is gated (`FULL_CI` or a manual run), and the keepalive pings themselves are left to the gRPC library; the tests check the parameters reach the dial.
