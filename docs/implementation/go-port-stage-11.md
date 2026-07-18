# Go-port Stage 11 — Jail-side wave (handoff, partial)

**Status:** 3 of the 4 jail-side binaries ported + tested. Image bake +
per-component flag wiring + nested-jail verification remain (human-gated).
**Plan:** [`docs/plans/go-port-plan.md`](../plans/go-port-plan.md) Stage 11.

## What landed

| Binary | Commit | Source | Notes |
|---|---|---|---|
| `cmd/yolo-ps` | df11330 | `src/yolo_ps.py` | frameproto client; end-to-end verified |
| `cmd/yolo-jail-supervisor` | 5ec30cf | `src/jail_daemon_supervisor.py` | env-in/files-out; ParseEnv cross-lang verified; -race clean |
| `cmd/yolo-oauth-terminator` | 21918ea | `src/oauth_broker_jail.py` | TLS terminator; 2-layer 502 attribution; keep-alive disabled |

## Remaining for Stage 11

- **`cmd/yolo-cglimit` + `cmd/yolo-journalctl`**: the generated Python helper
  scripts → Go binaries with entrypoint-emitted thin exec-wrappers. Not yet
  ported (they're small; the entrypoint wrapper emission is the coupling point
  — better done alongside Stage 10's entrypoint port).
- **Image bake**: all jail-side variants baked, per-component flags correlated
  with `YOLO_ENTRYPOINT_IMPL`, ONE image rebuild for the whole wave
  (`just load && just install` — human action).
- **Verification (human/nested-jail)**: supervisor restart-policy/backoff/
  rotation black-box in a real jail; terminator curl-with-CA parity incl. both
  502 flavors byte-compared; `cmd/yolo-cgd`/`yolo-journald` (Stage 7 Commit B)
  in all-Go vs all-Python jail-side modes; per-component revert without rebuild.

## Verified (in-jail, package + binary level)

- `yolo-ps`: real binary vs a framed fake daemon (mode/pid/jail_id stamped,
  stdout streamed, exit passthrough); both exit-2 error paths.
- `yolo-jail-supervisor`: ParseEnv golden (cross-lang vs Python `_parse_env`),
  restart policies, Run-terminates-always-daemon, 5MB log rotation; `-race`.
- `yolo-oauth-terminator`: relay-vs-broker 502 layer attribution, refresh
  400/200, proxy 502/passthrough, IsRefreshGrant golden; `-race`.

## Human actions

- CI (§10.7); the image rebuild + nested-jail all-Go/all-Python jail-side
  verification; the `cglimit`/`journalctl` helpers pair with the Stage 10
  entrypoint port.
