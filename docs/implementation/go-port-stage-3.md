# Go-port Stage 3 — Broker relay (first production swap) (handoff)

**Status:** landed, all in-jail criteria green. Flag defaults to Python (unset).
**Plan:** [`docs/plans/go-port-plan.md`](../plans/go-port-plan.md) Stage 3.

## What landed (commit b46f400)

- `internal/brokerrelay` + `cmd/yolo-broker-relay`: Go port of
  `src/broker_relay.py`. Raw byte proxy, per-connection broker dial (new-inode
  fix), host-side `jail_id` stamp (overrides client value) via `jsonx`
  (insertion-order-preserving reframe), degrade to bidirectional pipe.
  Dial-failure path: `CloseWrite` + bounded drain → client sees clean EOF, never
  ECONNRESET. SIGTERM unlinks the socket only if dev/ino still match what it
  bound. `--socket/--broker/--jail` argv frozen against the Python argparse
  (missing args → exit 2).
- **Seam #2** in `loopholes_runtime.py` (SAME commit):
  `_relay_spawn_argv` resolves the Go binary from `YOLO_BROKER_RELAY_BIN`
  (identical trailing argv); matchers `_relay_pid_cmdline_matches` and
  `_relay_pgrep` widened to recognize BOTH `broker_relay.py` AND
  `yolo-broker-relay` — the orphan-relay rider.

## Verified

- Go tests `-race` clean: jail_id stamp+override, verbatim forwarding
  (unparseable + valid-non-JSON), broker-down clean-EOF-not-ECONNRESET,
  broker-restart-new-inode, SIGTERM socket unlink.
- **Cross-language mixed-mode** (`tests/test_broker_relay_go_parity.py`): the
  REAL Python terminator (`oauth_broker_jail.ask_host_broker`) + the Python
  `FakeBroker`/`RawUpstream` harness drive the GO binary — 6 tests incl.
  broker-layer attribution.
- 2 new unit tests for the seam wiring + widened identity guard (23 relay unit
  tests green; existing 8 `test_broker_relay.py` unchanged/green).
- **End-to-end through the real `_relay_ensure`**: with
  `YOLO_BROKER_RELAY_BIN` set, the Go binary is spawned, a ping round-trips
  with host-side `jail_id`, the identity guard matches, and `_relay_stop` reaps
  it + unlinks the socket. (Recorded command output in the session; re-runnable
  via the snippet in the Stage 3 commit message context.)

## Human actions

- **Soak:** export `YOLO_BROKER_RELAY_BIN=<repo>/dist-go/<goos>-<goarch>/yolo-broker-relay`
  on the dev host. Revert = unset. When main moves, rebuild `dist-go/`
  (`just build-go`); relay re-ensure picks up the new binary on the next `yolo`
  invocation. Restart isn't needed (each `yolo` re-ensures).
- **CI (§10.7):** confirm the Go tests + `test_broker_relay_go_parity.py` pass on
  both arches.
- Code default flips at Stage 17.
