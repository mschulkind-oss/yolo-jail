---
title: "Last session wins: implementation sketch"
date: 2026-09-29
status: draft
tags: [plan, sketch, lifecycle, attach, sessions, keeper]
summary: "The parking lot for implementation material from jail-lifetime-last-session-wins.md. Not a hand-off; do not build from it while it is stamped SKETCH."
vantage:
  status-chip: true
---

# Last session wins: implementation sketch

**Status:** SKETCH, 2026-09-29. It is incomplete.
[OQ-JL5](jail-lifetime-last-session-wins.md#OQ-JL5) to
[OQ-JL8](jail-lifetime-last-session-wins.md#OQ-JL8), which it waited on, were ruled on
2026-09-29.
[OQ-JL1](jail-lifetime-last-session-wins.md#OQ-JL1) was directed on 2026-09-29, and every note
below assumes its answer: a **keeper**, one small background process per running container jail,
owns the jail's host services and ends itself
([§9](jail-lifetime-last-session-wins.md#9-the-keeper-design-2026-09-29)). No terminal holds
the jail for the others. Step 2 of the design's
[§7](jail-lifetime-last-session-wins.md#7-what-i-would-build-in-order) is built (its entry there
names the code and the tests), and the notes below that it settled say so.

This is the companion sketch of
[`jail-lifetime-last-session-wins.md`](jail-lifetime-last-session-wins.md). **The design wins on
behavior.** Nothing here is a decision. Every entry is settled-but-boring material, or it names
the question it waits on. The terms (keeper, hold process, liveness lock, lifeline, Window A) are
the design's ([§1.1](jail-lifetime-last-session-wins.md#11-terms)), and so is the session lock, the
host-side shared lock each session holds while it runs
([§4.2](jail-lifetime-last-session-wins.md#42-the-count-a-host-side-session-lock)).

## Notes parked here

- **No holding branch.** The direction on [OQ-JL1](jail-lifetime-last-session-wins.md#OQ-JL1)
  rejected a first terminal that waits. So when the fresh arm's own exec returns, it is an
  ordinary session's quit: drop the session lock, print the one line if other sessions remain,
  and exit with the agent's code
  ([JL-D16](jail-lifetime-last-session-wins.md#JL-D16)). The SIGHUP arm (`onTerminate` today
  calls `stopJail` first) never calls `stopJail`, for any session, the first included
  ([JL-D4](jail-lifetime-last-session-wins.md#JL-D4)). An attach's arm already ends only its own
  session, by hanging it up in the jail
  ([JL-D52](jail-lifetime-last-session-wins.md#JL-D52)); the first session's is what is left. The
  launch's arm and an attach's run on the Mac too (`runArmedSession` in
  [`proxy_other.go`](../../internal/cli/run/proxy_other.go)), unmeasured there.
- **Provisioning moves to the first session.** `provisionScript` leaves the container's command
  tail and runs in the first session's exec, where `[ -t 0 ]` still sees the terminal. It runs
  under an in-jail flock and records an outcome, *done* or *refused*. Every other session waits
  on that outcome, never on a done marker, and reads a free flock with no outcome as *abandoned*
  ([JL-D33](jail-lifetime-last-session-wins.md#JL-D33)). Built at step 2 (`sessionGate`): the
  stage rides pid 1's argv, and the flock and the outcome are in the jail's `/run/yolo/main`.
- **The keeper's verb.** A hidden subcommand in the daemon group, `yolo internal daemon
  jail-keeper`, not a new binary (AGENTS.md: *"Host daemons are hidden self-exec subcommands of
  `yolo` (`yolo internal daemon <name>`)"*). So it needs no `flake.nix` or
  `stage-source-bundle.sh` entry.
- **Spawn shape.** `startDetached`
  ([`scratchremoval.go`](../../internal/cli/run/scratchremoval.go)) is the starting point:
  Setsid, stdio on `/dev/null`, one inherited fd as fd 3. The progress pipe, the lifeline and the
  handed launch lock need more inherited fds, so that helper grows an argument, and the keeper
  marks each one close-on-exec first thing
  ([JL-D29](jail-lifetime-last-session-wins.md#JL-D29),
  [JL-D31](jail-lifetime-last-session-wins.md#JL-D31)).
  - On Linux the launch spawns the keeper by exec of `/proc/self/exe`, which still names the
    running binary after `just install` has replaced the file at its path, so nothing is opened
    early. Not `execx.SelfExecArgv`: it re-resolves `os.Executable()`, a path, and the spawn comes
    after the nix build, which can take minutes. The keeper's own later self-execs, the scratch
    remover included, are built the same way.
  - macOS has no such file, so there a keeper of another build refuses the plan by its build
    stamp ([JL-D5](jail-lifetime-last-session-wins.md#JL-D5),
    [JL-D20](jail-lifetime-last-session-wins.md#JL-D20)).
  - Where systemd is present, the keeper moves itself into a transient scope with
    `StartTransientUnit` before it starts anything. Not `systemd-run --user --scope`, which would
    stop the keeper being the launch's child.
- **Lock file location.** It must be host state no jail mounts. It must never go under `cache/`,
  which every jail mounts read-write. `paths.GlobalStorage()`'s `owners/` directory is the
  precedent (`ownerPIDDir`, [`lifecycle.go`](../../internal/cli/run/lifecycle.go)). Go opens files
  `O_CLOEXEC`, so a session's lock is not inherited by the `podman exec` client unless it is put in
  `ExtraFiles`.
- **No pending-name trick for the liveness lock.** `servicessession.go`'s pending-name-then-rename
  does not carry over. The session-lock and liveness-lock files are one per container name and
  are never renamed over or unlinked
  ([JL-D28](jail-lifetime-last-session-wins.md#JL-D28)), and nothing acts on a free liveness lock
  without holding the session lock exclusively or the launch lock
  ([JL-D18](jail-lifetime-last-session-wins.md#JL-D18)).
- **`jailSessionCount`.** Drop its `+1` once the main process is the hold process
  ([`contracttags.go`](../../internal/cli/run/contracttags.go)). Done at step 2,
  keyed on the jail's `YOLO_JAIL_MAIN`, so a jail launched before keeps its `+1`.
- **Step 3 undoes one step-2 coupling.** The hold also ends when the first session's process
  does ([JL-D46](jail-lifetime-last-session-wins.md#JL-D46)), and the fresh launch stops the jail
  itself when it has not (`awaitJailMainEnd`); the keeper replaces both with its drain.
- **Detach keys.** Verified on podman 5.8.7 for both `run` and `exec`, and built at step 1
  ([JL-D54](jail-lifetime-last-session-wins.md#JL-D54)). Apple Container is still to check.
- **Death pipe (local Linux podman only).** `podman exec --preserve-fds=N` (the list form
  `--preserve-fd` is documented as crun-only; neither exists on the remote client), with the write end held
  only by the session launcher. EOF inside the jail means the launcher is gone, and the in-jail
  side then sends SIGHUP to the session's process group. The research lens measured the EOF on
  2026-09-29.
- **Window A and the linger probe.** Both key on the `podman run` client's exit in the
  foreground. They move to the keeper, or they are retired by name.
- **The reaper.** It takes the jail's liveness lock and then its session lock, each
  `LOCK_EX|LOCK_NB`, and holds both across `stopJail` and `stopLoopholes`; the order in which it
  reads the owner PID and the locks does not matter once it holds them
  ([JL-D7](jail-lifetime-last-session-wins.md#JL-D7)).
- **Tests the landing needs.** Each of these has to fail if its call site is deleted:
  - `TestTeardownChainEmitsShutdownSpans`, re-pointed at the keeper's chain;
  - a unit test that no session's signal arm calls `stopJail`, the first session's included;
  - a unit test that the reaper declines a jail whose session lock it cannot take exclusively.

  Integration tests: two sessions in one jail, quit the first, and the second still answers while
  the first terminal has its prompt back. Also `kill -9` of the keeper, followed by an arrival,
  which is refused, as [OQ-JL7](jail-lifetime-last-session-wins.md#OQ-JL7) ruled
  ([JL-D13](jail-lifetime-last-session-wins.md#JL-D13)).
