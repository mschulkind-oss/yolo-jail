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

- **Holding (the leaning, and the first slice either way).** A new branch after the fresh
  arm's own exec returns: if the session lock is held by others, print the holding line, restore
  cooked mode, and wait for `LOCK_EX` on a second open file description. The SIGHUP arm
  (`onTerminate` today calls `stopJail` first) gains the same test and, when others hold the
  lock, redirects output to `launch.log` and returns to the wait. macOS podman and Apple
  Container have no signal arm ([`proxy_other.go`](../../internal/cli/run/proxy_other.go)), so
  they need one first.
- **Provisioning moves to the first session.** `provisionScript` leaves the container's command
  tail and runs in the first session's exec, where `[ -t 0 ]` still sees the terminal. It writes a
  done marker; an attach waits on it before its exec.
- **The keeper's verb, if [OQ-JL1](jail-lifetime-last-session-wins.md#OQ-JL1) rules A.** A hidden subcommand in the daemon group, `yolo
  internal daemon jail-keeper`, not a new binary (AGENTS.md: *"Host daemons are hidden
  self-exec subcommands of `yolo` (`yolo internal daemon <name>`)"*). So it needs no `flake.nix`
  or `stage-source-bundle.sh` entry.
- **Spawn shape, if A.** `startDetached`
  ([`scratchremoval.go`](../../internal/cli/run/scratchremoval.go)) is the starting point:
  Setsid, stdio on `/dev/null`, one inherited fd as fd 3. The progress pipe and a
  `launchservice.Lifeline`-style descriptor need more inherited fds, so that helper grows an
  argument. `execx.SelfExecArgv` resolves `os.Executable`, a path, at spawn time, and the spawn
  comes after the nix build, which can take minutes. So open `/proc/self/exe` at process start
  on Linux and spawn from that. macOS has no equivalent; the window is stated. Where systemd is
  present, run the keeper in its own transient scope.
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
- **Death pipe (local Linux podman only).** `podman exec --preserve-fds=N` (the list form
  `--preserve-fd` is documented as crun-only; neither exists on the remote client), with the write end held
  only by the session launcher. EOF inside the jail means the launcher is gone, and the in-jail
  side then sends SIGHUP to the session's process group. The research lens measured the EOF on
  2026-09-29.
- **Window A and the linger probe, if A.** Both key on the `podman run` client's exit in the
  foreground. They move to the keeper, or they are retired by name.
- **The reaper.** It takes the session lock `LOCK_EX|LOCK_NB` itself and holds it across
  `stopJail` and `stopLoopholes`; the order in which it reads the owner PID and the lock does not
  matter once it holds the lock.
- **Tests the landing needs.** Each of these has to fail if its call site is deleted:
  - `TestTeardownChainEmitsShutdownSpans`, re-pointed at the owner's chain wherever it runs;
  - a unit test that the attach signal arm never calls `stopJail`;
  - a unit test that the holding launcher's SIGHUP arm does not call `stopJail` while the
    session lock is held by others;
  - a unit test that the reaper declines a jail whose session lock it cannot take exclusively.

  Integration tests: two execs into one held jail, quit the first, and the second still answers.
  Also `kill -9` of the owner, followed by an arrival, which is refused.
