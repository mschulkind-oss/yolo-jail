---
title: "Last session wins: implementation sketch"
date: 2026-09-29
status: draft
tags: [plan, sketch, lifecycle, attach, sessions]
summary: "The parking lot for implementation material from jail-lifetime-last-session-wins.md. Not a hand-off; do not build from it while it is stamped SKETCH."
vantage:
  status-chip: true
---

# Last session wins: implementation sketch

**Status:** SKETCH, 2026-09-29. It is incomplete, and it is unstable while the questions are open.

This is the companion sketch of
[`jail-lifetime-last-session-wins.md`](jail-lifetime-last-session-wins.md). **The design wins on
behavior.** Nothing here is a decision. Every entry is settled-but-boring material, or it names
the question it waits on.

## Notes parked here

- **The keeper's verb.** It is a hidden subcommand, `yolo internal jail-keeper`, not a new binary
  (AGENTS.md: *"Daemons are subcommands, not separate binaries"*). So it needs no `flake.nix` or
  `stage-source-bundle.sh` entry. Blocked on
  [OQ-JL1](jail-lifetime-last-session-wins.md#OQ-JL1).
- **Spawn shape.** Reuse `startDetached`
  ([`scratchremoval.go`](../../internal/cli/run/scratchremoval.go)): Setsid, stdio on
  `/dev/null`, with one inherited fd as fd 3. The progress pipe needs a second inherited fd, or a
  different stdio for the keeper's pre-ready phase, so that helper grows an argument. `execx.SelfExecArgv`
  resolves `os.Executable`, a path. The spawn happens seconds after start, so that is acceptable.
  Opening `/proc/self/exe` at start would pin the inode on Linux if it ever matters.
- **Lock file location.** It must be host state no jail mounts. It must never go under `cache/`,
  which every jail mounts read-write. `paths.GlobalStorage()`'s `owners/` directory is the
  precedent (`ownerPIDDir`, [`lifecycle.go`](../../internal/cli/run/lifecycle.go)). Go opens files
  `O_CLOEXEC`, so a session's lock is not inherited by the `podman exec` client unless it is put in
  `ExtraFiles`.
- **The lock appears already held.** Copy `servicessession.go`'s pending-name-then-rename trick
  for the keeper's liveness lock, so that a reaper never sees it unlocked in the gap between
  create and flock.
- **`jailSessionCount`.** Drop its `+1` once the main process is the hold process
  ([`contracttags.go:430`](../../internal/cli/run/contracttags.go)).
- **Detach keys.** Verify that `--detach-keys=""` disables the sequence on podman 5.x for both
  `run` and `exec` before relying on it. Check Apple Container separately.
- **Death pipe (Linux, local podman only).** `podman exec --preserve-fd`, with the write end held
  only by the session launcher. EOF inside the jail means the launcher is gone, and the in-jail
  side then sends SIGHUP to the session's process group. The research lens measured the EOF on
  2026-09-29.
- **Window A and the linger probe.** Both key on the `podman run` client's exit in the
  foreground. They move to the keeper, or they are retired by name. Blocked on
  [OQ-JL1](jail-lifetime-last-session-wins.md#OQ-JL1).
- **Tests the landing needs.** Each of these has to fail if its call site is deleted:
  - `TestTeardownChainEmitsShutdownSpans`, re-pointed at the keeper's chain;
  - a unit test that the attach signal arm never calls `stopJail`;
  - a unit test that the reaper reads the session lock before the owner PID.

  Integration tests: two execs into one kept jail, quit the first, and the second still answers.
  Also `kill -9` of the keeper, followed by an arrival.
