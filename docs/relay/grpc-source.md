---
description: Settings of a gRPC source that syncs from an upstream flag sync endpoint
---

# Syncing from an upstream flag sync endpoint

A `grpc` source reads its flag configuration from another flag sync endpoint: flagd, a configuration proxy, flagd-proxy or a vendor's.
Without further settings it behaves as it always has.
A `grpc` object on the source opts into the behaviour the Go flagd provider has ([UC-023](../use_cases/UC-023-sync-from-an-upstream-flag-sync-endpoint.md)).

```json
{"uri": "flagd-upstream:8015", "provider": "grpc", "selector": "flagSetId=app",
 "grpc": {"retryBackoffMs": 1000, "retryBackoffMaxMs": 5000, "streamDeadlineMs": 600000,
          "keepAliveTime": 30000, "fatalStatusCodes": ["PERMISSION_DENIED"],
          "selectorHeader": true, "readyOnFirstPayload": true}}
```

The settings have no command line flag; they belong in `--sources` or the configuration file.

## Settings of the `grpc` object

Absent, `0` and `false` mean not set, key by key, and an empty `grpc` object changes nothing.
The object is read strictly: an unknown key, a value of the wrong type, or the object on a source whose provider is not `grpc` stops startup and names the source.

| Key | Meaning |
|-----|---------|
| `retryBackoffMs` | Initial backoff of the connection's retry policy, in milliseconds. Default 1000 when only `retryBackoffMaxMs` is set. |
| `retryBackoffMaxMs` | Maximum backoff of the retry policy, and the delay before a stream is re-opened after an error, in milliseconds. Default 5000 when only `retryBackoffMs` is set. Must not be below `retryBackoffMs`. |
| `streamDeadlineMs` | Re-opens the stream at once when this many milliseconds have passed, with no error and no delay. |
| `keepAliveTime` | HTTP/2 ping interval in milliseconds, also without an open stream, with a timeout of 5 seconds. From 10000 on. |
| `fatalStatusCodes` | Names of gRPC status codes, such as `PERMISSION_DENIED`. A listed code before the source's first payload ends flagd. |
| `selectorHeader` | Sends the source's `selector` as the `flagd-selector` header as well as in the request body. |
| `readyOnFirstPayload` | The sync provider is ready after its first payload, not when its stream opens. |

## Timing

With either `retryBackoff` key set, the connection retries a call up to four times on `UNKNOWN` and `UNAVAILABLE`, doubling from `retryBackoffMs` up to `retryBackoffMaxMs`.
`SyncFlags` then waits for the connection to be ready.
After an error the source waits `retryBackoffMaxMs` before it re-opens the stream.
The layers add up, so read the timings as at least these.
Without either key the source retries on the schedule it always had.

## Differences from the Go flagd provider

- `keepAliveTime` `0` or absent sends no pings.
    In the provider `0` pings every 10 seconds, because gRPC raises any interval below 10 seconds to 10 seconds.
    Here a value from 1 to 9999 is refused.
- An upstream with gRPC's default enforcement, such as flagd-proxy, answers pings faster than every 5 minutes, or without a stream, with `GOAWAY` and `too_many_pings`.
    Set `keepAliveTime` only for an upstream that allows it.
- The provider sends the selector header whenever a selector is set.
    Here it is opt-in with `selectorHeader`, because it changes what the upstream sees.
    A `flagd-selector` entry in the source's `headers` is refused with it.
- A fatal status code ends flagd, naming the source and the code, as for a fetching source that is refused before its first fetch.
    The other sources of the same flagd stop with it.
    After the first payload a listed code is retried like any error.
    Each source has its own set.
- When an embedder replaces the dial options, the selector header applies on top of them.
    The retry policy and the keepalive do not, because the replacement owns the dial.
- The sync provider is ready when its stream opens, as before.
    The runtime's readiness waits for an accepted flag configuration from every source either way.

## Upgrading

Keys spelled as the provider documents them, set directly on a source, are ignored as they always were.
A configuration that already carries a key named `grpc` on a source has it read now, and flagd stops at startup when its content is malformed.
