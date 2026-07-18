# Go-port Stage 6 — OAuth broker (handoff)

**Status:** landed, in-jail criteria green. Flag defaults to Python. Soak +
real-claude-login + timing are human-gated (see below).
**Plan:** [`docs/plans/go-port-plan.md`](../plans/go-port-plan.md) Stage 6.

## What landed (commit 8ccde4b)

- `internal/oauthbroker` + `cmd/yolo-claude-oauth-broker-host`: Go port of
  `src/oauth_broker.py`. Byte/behavior contracts held (see the commit body):
  creds-file `indent=2`/no-sort-keys/`0600`/atomic-rename blob; 90s cache;
  `NormalizeOAuth`/`AsOAuthResponse`; `refresh.lock` flock (same path →
  cross-impl exclusion); `DisableCompression` + accept-encoding strip both
  ways; the 300/60/5×12 refresher timing + transient-only fast-retry; the
  action set + unknown→exit(2); openssl-exec cert gen.
- Test-only `YOLO_BROKER_UPSTREAM_URL` + `YOLO_BROKER_STATE_DIR` overrides in
  BOTH impls (the plan's mandated Python-side seam edit for black-box parity).
- Seam #2 daemon resolution: `_daemon_launcher` (`YOLO_GO_DAEMONS` gates,
  `YOLO_GO_BIN_DIR` resolves by explicit dir, missing→fallback), wired into
  `_broker_spawn`.

## Verified

- Byte-diff of action/normalize/write shapes vs the live Python oracle
  (`tools/parity/oauth_broker_oracle.py`) — **also lands the Stage 1 broker
  wire fixtures** (action shapes, every error dict, normalize output,
  `write_tokens` blob).
- Cross-language live black-box (`tests/test_oauth_broker_go_parity.py`, 6
  tests): the REAL Python client drives the Go daemon against a fake upstream —
  ping, cached-miss, refresh-writes-creds (preserved fields), cache-hit,
  unknown-action exit 2, cross-language flock contention.
- 4 `_daemon_launcher` unit tests. Full Go + Python fast suites green.

## Human actions / UNVERIFIED

- **Real `claude` login + refresh** in a live jail on the Go broker — needs a
  real Claude session (no-agent-tests rule keeps it out of CI).
- **≥1 week dev-host soak**: export `YOLO_GO_DAEMONS=yolo-claude-oauth-broker-host`
  + `YOLO_GO_BIN_DIR=<repo>/dist-go/<goos>-<goarch>` on the dev host; flip/revert
  includes `yolo broker restart` (stale-code-in-memory) + `just build-go` when
  main moves.
- **Background-refresher wall-clock timing** (300/60/5×12) is code-frozen but
  not soak-verified.
- **openssl cert gen**: not installed in this jail, so `--init-ca` was exercised
  only via the Python unit tests + fake-cert seeding. Real cert gen on a host
  with openssl still needs a manual smoke.
- CI (§10.7): confirm both suites on both arches.
