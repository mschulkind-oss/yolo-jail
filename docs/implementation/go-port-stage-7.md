# Go-port Stage 7 — Builtin daemons (cgd + journald) (handoff)

**Status:** Commit B (Go daemons) landed + tested. **Commit A (Python
thread→subprocess carve-out) NOT done** — the risky live-path piece, flagged
below.
**Plan:** [`docs/plans/go-port-plan.md`](../plans/go-port-plan.md) Stage 7.

## What landed (commit 5d1cc37)

- `internal/cgd` + `cmd/yolo-cgd`: cgroup-delegate. Single-line-JSON protocol,
  `_validate_cgroup_name`, `_parse_memory_value`, create_and_join (cpu.max
  quota formula, 1MB memory floor, pid range, warnings), destroy (procs-refusal
  / idempotent-absent / read-error==remove-error), SO_PEERCRED PID + chmod 0777.
- `internal/journald` + `cmd/yolo-journald`: journal bridge. `>BI` framing with
  stream IDs **1/2/3** (not frameproto's 0/1/2), arg validation, user-mode
  `--user` prepend, exit codes 2/127/1.

## Verified

- Go `-race` clean. cgd validate/parse byte-diffed vs the live Python oracle
  (`tools/parity/cgd_oracle.py`); create/join/destroy over a fake cgroup tree +
  a real Unix-socket round trip.
- journald: exact frame-header bytes, stream-id distinctness, arg-validation
  golden, AND an end-to-end run of the real `yolo-journald` binary with a fake
  `journalctl` on PATH → stdout=frame1, stderr=frame2, exit=frame3, `--user`
  prepended.

## REMAINING — Commit A (live-path surgery, needs a focused pass)

The plan's Stage 7 Commit A is a behavior-preserving Python refactor that moves
the cgd + journal **threads** (in `loopholes_runtime.py`) to spawned
subprocesses matching the `_start_host_service_external` lifecycle, **with
`PR_SET_PDEATHSIG(SIGTERM)`** so they still die when `yolo run` dies by any
means incl. SIGKILL (today's thread crash-lifetime). Only after that does the
binary swap ride `YOLO_GO_DAEMONS` at `_start_host_service_external`.

This was deferred because:
- It rewrites the live `yolo run` threading model — higher risk than the pure
  daemon-logic port, and the OOM-prone environment makes big live-path edits
  risky to do without frequent commits.
- Its exit criteria are **not unit-testable** (the plan says so): a **kill -9
  test in thread-mode vs subprocess-mode diffing surviving processes/sockets**,
  and cgroup writes (`cpu.max`/`memory.max`/`pids.max`) diffed identical — both
  require a real nested jail.

**Next session for Stage 7:** do Commit A + the `YOLO_GO_DAEMONS` gate for
`cgroup-delegate`/`journal` (reusing `_daemon_launcher`, already built in
Stage 6), then the nested-jail kill -9 lifetime verification in both modes.

## Human actions

- CI (§10.7): confirm `go test ./internal/cgd ./internal/journald` on both arches.
- Commit A's nested-jail kill -9 verification is a human/nested-jail step.
