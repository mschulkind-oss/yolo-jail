---
title: "Sketch: host-run's first build slice"
date: 2026-10-09
status: draft
stage: SKETCH
next: "Promote against the tree once OQ-HX1 is ruled"
depends-on:
  - host-run-mailbox.md#OQ-HX1
tags: [plan, host, mailbox, stopgap]
---

# Sketch: host-run's first build slice

**Status:** 2026-10-09 — incomplete, and unstable while questions are open. Not a hand-off:
do not build from this while it is a sketch.

This is the parking lot for [`host-run-mailbox.md`](host-run-mailbox.md). **The design wins on
behavior**; nothing here decides any. Blocked on [OQ-HX1](host-run-mailbox.md#OQ-HX1) as a whole.

## File map (to verify against the tree before building)

| File | Holds |
| :--- | :--- |
| `internal/hostrun/token.go` | mint, parse, the base32 grammar, the 4 h / 5 min window, the canonical-JSON digest |
| `internal/hostrun/checks.go` | the refusal checks `ask` and the host form share: `termsafe.HasUnsafe`, `unicode.Cf`, `unicode.Zl`, `unicode.Zp`, the fish-ambiguous backslash, the 300-character line, recursion; a refusal formatter naming position and code point |
| `internal/hostrun/ask.go` | cwd translation through `YOLO_HOST_DIR` (default omitted), the line via `shquote.Join` plus forced quoting of a leading `=` or `%`, mailbox creation, the unwritable-marker check, jail-side housekeeping |
| `internal/hostrun/mailbox.go` | the host writer: `paths.OpenStateDirRoot` on `.yolo`, `paths.OpenStateSubdirRoot` for `host-run` and the token; one create helper using `O_CREATE\|O_EXCL\|O_NOFOLLOW\|O_NONBLOCK` through `Root.OpenFile`, `Root.Rename` for JSON; `refused.json`; the unwritable marker. Not `WriteWorkspaceStateFile` (it truncates an existing inode) |
| `internal/hostrun/resolve.go` | program resolution over absolute `PATH` entries, refusing a relative entry or one under a `.yolo`-holding directory or `paths.GlobalStorage()`; the env disclosure list; the agent-writable path scan over cwd and argv words |
| `internal/hostrun/run.go` | TTY check on fds 0-2, exec, foreground process group, tee to the terminal plus an in-memory spool capped at 16 MiB per stream, `signal.Notify` for INT/TERM/HUP, heartbeat by `WriteAt` on its own fd, delivery after exit, `result.json` on every path, the audit append |
| `internal/hostrun/wait.go` | `wait` and `show`: lstat polling, the state table, the 100-minute default timeout, framed output via `termsafe.VisibleLines` |
| `internal/cli/hostrun.go` | the verb: `ask`/`wait`/`show` in a jail, the host form outside, each refusing on the wrong side |
| `internal/cli/cli.go` | the early route beside `gh` |
| `internal/cli/dispatch.go`, `internal/cli/help.go` | the verb row and its help line |
| `internal/brokeraudit/brokeraudit.go` | a `WorkspaceReported` field; `Set`'s comment names `human-run` |
| `internal/ghbroker/daemon.go` | the exit-77 message names `yolo host-run ask`, with the old instruction as fallback |
| `packs/github/briefing/gh.md` | the write bullet, likewise |
| `internal/jailcontent/briefing.go` | `lockedConfigRequest`'s text, likewise; one always-on paragraph: use `ask`, then `wait` in the background and re-arm on 75; the line must start with bare `yolo host-run` |
| `internal/jailcontent/builtinskills/configuring-the-jail/SKILL.md` | the locked-config loop and `yolo loopholes enable`, likewise |
| `CHANGELOG.md` | one `### Added` bold-lead entry |

The gh credential list is host-run's own small table; nothing is exported from `internal/ghbroker`.

## Tests

- token round-trip; every grammar, age and skew rejection;
- each shared check refuses in `ask` and again in the host form fed a hand-written line;
- digest mismatch writes `refused.json` and runs nothing;
- the generated line, re-parsed by `bash -c 'printf "%s\0" "$@"' _ <line>`, yields the argv; the
  same in fish and zsh where installed, with words holding `'`, `\d`, a leading `=` and `%`;
- stdin, stdout or stderr not a terminal refuses at step 2 (a pty harness for the positive case);
- a `PATH` with a relative entry, or a workspace directory, refuses; the disclosure prints the
  resolved program path; `EvalSymlinks` cwd is printed resolved;
- a link planted at `.yolo`, at `.yolo/host-run` and at the token directory each refuses; a
  hardlink or FIFO planted at `started.json`, `stdout` and a temporary name fails the create; no
  write outside the mailbox;
- a pre-existing `started.json` or `refused.json` refuses; two concurrent pastes run once;
- during a run, the mailbox holds no `stdout` or `stderr`; after SIGINT, a start failure and a
  normal exit, all three files appear with `result.json` last;
- the truncation marker and byte counts at the cap;
- `wait` states: 64 unknown, 69 refused, 69 marker, 69 expired unused, 69 stale heartbeat, 75 at
  its timeout, 0 on a result whose command exited 75;
- call-site tests that fail if deleted: the early route in `Main`, the dispatch and help rows, the
  briefing paragraph, each changed refusal text, the audit append in the host form;
- the host form refuses with `YOLO_VERSION` set; tests run it with `YOLO_VERSION` unset;
- one integration test: launch a jail, `ask -- echo hi` inside, run the host form from the test
  process under a pty with `YOLO_VERSION` unset, and `wait` inside prints `hi` and exits 0.

## Verification beyond the gate

- `just check-ci`, the integration test above, and a nested-jail launch of the freshly built
  `dist-go` binary from `/tmp/yolo-nested`.
- One real paste on a rootless-podman host, and one each from an Apple Container jail and a
  macos-user session (the design's backends table), before the stage moves past `DECIDED`.
- The wrap measurement: a 300-character line copied from a Claude Code session at 80 columns,
  pasted into bash, zsh and fish.
