---
title: "Local pack-broker fixture: host controls only"
date: 2026-11-04
status: in-review
tags: [pack-broker, local-testing, synthetic]
summary: "Run and inspect the private synthetic CLI/HTTP fixture on the host; broker approval walkthroughs are not implemented yet."
---

# Local pack-broker fixture: host controls only

This guide covers only the host fixture controls currently implemented in
`tools/broker-fixture/main.go`. The service uses synthetic state and
per-run synthetic credentials; it never signs in to CircleCI, Linear, or another public service.
The credential files live in the private state directory and are not printed by `start` or `inspect`.

> [!IMPORTANT]
> These commands operate the synthetic service directly from the host. They do **not** exercise a
> jail front, classifier, approval, or broker executor. The fixture packs in
> `integration/testdata/pack-broker-cli/` and `integration/testdata/pack-broker-http/`
> are not runtime-loaded yet. The end-to-end broker walkthrough belongs to task H and is unfinished.

## Build and start

Build the developer fixture tool, create a fresh private state directory, and start its host-loopback
listener:

```bash
fixture_bin="${XDG_CACHE_HOME:-$HOME/.cache}/yolo-pack-broker/fixture"
mkdir -p "$(dirname "$fixture_bin")"
go build -o "$fixture_bin" ./tools/broker-fixture
state_dir="$(mktemp -d "${TMPDIR:-/tmp}/yolo-pack-broker-demo.XXXXXX")"
"$fixture_bin" start --state-dir "$state_dir"
```

`start` prints the loopback endpoint, the `synthetic-pack-broker-fixture-v1` identity, and the state
directory only after an authenticated readiness probe succeeds; it does not print synthetic client or
host-admin tokens, or the signing key. The listener is pinned to `127.0.0.1`; no
endpoint or credential-destination flag is available on the fixture client. Choose an empty state
directory. A private `state.lock` reserves that directory through the service lifetime, so a second
`start` or direct `serve` cannot replace its endpoint metadata while it is active. The lock is released
by the operating system if the service exits; existing fixture state is never overwritten on restart.
Host controls and the direct client have a finite request timeout, and `stop` waits only a bounded time
for that listener to close.

## Seed, read, change, and inspect

The fixture has two stable projects (`alpha`, `beta`) and two work items (`a1`, `b1`). Host controls
use a distinct private admin token. The direct CLI client uses only the fixture's synthetic client
token; it is a local service client, not a brokered jail client.

```bash
"$fixture_bin" seed --state-dir "$state_dir"
"$fixture_bin" inspect --state-dir "$state_dir"
"$fixture_bin" client --state-dir "$state_dir" list
"$fixture_bin" client --state-dir "$state_dir" get a1
"$fixture_bin" client --state-dir "$state_dir" update a1 done
"$fixture_bin" change --state-dir "$state_dir" a1 reviewed
"$fixture_bin" inspect --state-dir "$state_dir"
```

`inspect` shows the synthetic state and write log, not credentials. A host `change` can affect only
`a1` or `b1`; the example leaves beta's `b1` untouched. `reset` empties only this fixture's projects,
items, and write log. It does not remove the private synthetic credentials or touch yolo approval
state.

## Select a synthetic failure and stop

The host can select the service's fixed synthetic `lookup-failure`, `redirect`, `credential-echo`, or
`oversize` response scenario. The selector is a host-only control; it cannot be supplied by a brokerable
client as an endpoint or credential redirect.

```bash
"$fixture_bin" scenario --state-dir "$state_dir" lookup-failure
"$fixture_bin" inspect --state-dir "$state_dir"
"$fixture_bin" scenario --state-dir "$state_dir" none
"$fixture_bin" reset --state-dir "$state_dir"
"$fixture_bin" stop --state-dir "$state_dir"
rm -rf -- "$state_dir"
```

Run `stop` for the corresponding state directory before removing that private directory. It asks that
fixture listener to shut itself down; it does not signal a PID read from fixture state. A second
fixture run has its own listener, state, and synthetic credentials.
