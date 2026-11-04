# Serve Flagsmith flags over OFREP

The flagsmith distribution is a relay that the relay builder builds from `distributions/flagsmith/manifest.yaml`.
It reads the environment document of one or several Flagsmith environments and evaluates it with Flagsmith's own Go engine, so that OpenFeature clients get Flagsmith's flags through OFREP.

It is a small validation that a relay can serve a vendor's flags, not a replacement of the Flagsmith Edge Proxy.
Read [what it does not do](#what-it-does-not-do) before you point a client at it.

## Build it

```shell
make build-flagsmith
```

The binary is `.flagsmith-build/flagsmith`.
It has flagd's command line and configuration.
Building it needs access to the Go module proxy, because Flagsmith's engine comes from there.

There is no FIPS variant: the engine places an identity in a percentage split with MD5.

## Start it

Each environment is one source.
The source names the provider `flagsmith`, the API base of Flagsmith and the format `flagsmith`.
The environment's server-side key goes into the `X-Environment-Key` header, and `headersFrom` reads it from an environment variable or a file, so that it is neither in the configuration nor on the command line.
A key written in `headers` stops the source.

```shell
export FLAGSMITH_KEY_PRODUCTION='ser.…'

flagsmith start --sources '[{
  "uri": "flagsmith+https://edge.api.flagsmith.com/api/v1/",
  "provider": "flagsmith",
  "format": "flagsmith",
  "headersFrom": {"X-Environment-Key": {"env": "FLAGSMITH_KEY_PRODUCTION"}},
  "interval": 60,
  "triggers": [
    {"type": "poller"},
    {"type": "sse",
     "url": "https://realtime.flagsmith.com/sse/environments/<client-side key>/stream",
     "idleTimeout": "5m"}
  ]
}]'
```

List the `poller` next to `sse`.
A source that sets `triggers` replaces the defaults, including the poller, and the poll is what keeps the flags fresh when the stream is down.
The stream is best effort: an unreachable stream does not stop the relay or delay readiness, and every event makes the relay fetch the whole environment document again, so keep the interval moderate.

The relay is ready after the first environment document of every source is accepted.
A source with a `staleAfter` makes the relay not ready when it has not reached Flagsmith for that many seconds.

If Flagsmith refuses the key before the first document was accepted, the relay stops and names the source, without the key.
After that, a refusal is logged, the relay keeps serving the last document and tries again.

## Evaluate

The flag set of an environment is its client-side key.
A request picks the environment with the `Flagd-Selector` header:

```shell
curl -X POST localhost:8016/ofrep/v1/evaluate/flags/banner \
  -H 'Content-Type: application/json' \
  -H 'Flagd-Selector: flagSetId=<client-side key>' \
  -d '{"context": {"targetingKey": "user-1", "plan": "beta"}}'
```

- **Identity and traits:** the targeting key is the Flagsmith identity, and the other attributes of the evaluation context are its traits.
    A request without a targeting key is evaluated at environment level: no identity overrides and no percentage split.
- **Reasons:** an environment default is `STATIC`, a segment or an identity override is `TARGETING_MATCH` with the segment in the metadata, a percentage split is `SPLIT` with its variant and weight, and a disabled flag is `DISABLED`.
- **Values:** every type reads the value of the flag.
    A disabled flag, and a flag without a value, resolve to no value, so that the client's default stands.

## What it does not do

- **It does not check who calls it.**
    The credential middleware for Flagsmith keys exists as a module, but flagd's command line cannot configure middleware yet, so the distribution lists none.
    Any client that can reach the relay can read every environment by sending the selector of that environment.
    Run it only behind something that authenticates its clients.
- **It is not compared with the Edge Proxy.**
    The tests check its results against Flagsmith's Go engine.
    The Edge Proxy evaluates with Flagsmith's Python engine, so results match only as far as the two engines agree.
- **It serves OFREP only.**
    It has no `/api/v1/` endpoints for Flagsmith SDKs, no environment document passthrough, and none of the Edge Proxy's configuration and health endpoints.
- **It does not hide server-side features from client-side keys.**
    The environment document has no marker for them.
    A project that hides disabled flags does so only for requests that carry the client credential kind, which only the middleware sets.
