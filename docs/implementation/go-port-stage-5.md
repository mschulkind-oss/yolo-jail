# Go-port Stage 5 — host-processes daemon (handoff)

**Status:** landed. Behind `YOLO_GO_DAEMONS`. Soak + nested-jail verification
human-gated.
**Plan:** [`docs/plans/go-port-plan.md`](../plans/go-port-plan.md) Stage 5.
**Unblocked by:** `internal/json5` (Spike A, `docs/research/go-port-parity.md`).

## What landed (a5b7751, 38ae732; client df11330)

- `internal/hostprocesses` + `cmd/yolo-host-processes`: ported from
  `src/host_processes.py`. Config load via `json5.Decode`, re-read per request;
  list/tree/pid modes; allowlist argv construction; keeps exec'ing real `ps`.
- `cmd/yolo-ps`: in-jail client (Stage 5/11), ported from `src/yolo_ps.py`.
- seam #2 at `_start_host_service_external`: `cmd[0]` swapped to the Go binary
  when gated via `YOLO_GO_DAEMONS`/`YOLO_GO_BIN_DIR` (missing → console-script
  fallback).

## Verified

- **Config-load parity** byte-diffed vs live Python `_load_config` over a
  corpus (comments, trailing commas, non-str filtering, `or DEFAULT_FIELDS`).
- **End-to-end**: Go `yolo-ps` → Go daemon → fake `ps` (PATH shim) produces the
  exact allowlisted argv (`ps -o pid,comm -C sway -C waykeeper`, sorted comms)
  + access-log line; empty-allowlist exit 3; self-check output.
- **Seam #2** unit test: a real gated Go binary launched through
  `_start_host_service_external` binds the socket (launcher swap works).
- All Python host_service/host_processes/loopholes tests still green (74).

## Human actions / UNVERIFIED

- Soak: `YOLO_GO_DAEMONS=yolo-host-processes` + `YOLO_GO_BIN_DIR=<dist-go>` on
  the dev host; flip/revert + `just build-go` when main moves.
- Nested-jail: `yolo-ps`, `yolo-ps -t`, `yolo-ps --pid N` byte-identical under
  both daemons (byte under fake-ps; structural under live ps); `yolo check`
  daemon probes identical. (The Python `ps`-replay byte-gate + one live-`ps`
  structural run are the plan's exit criteria; done here at the fake-ps level,
  live-ps needs a real jail.)
- CI (§10.7) both arches.
