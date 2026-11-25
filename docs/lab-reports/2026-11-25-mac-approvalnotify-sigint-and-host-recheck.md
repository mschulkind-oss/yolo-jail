---
title: "Mac at e71c48e9c: the approvalnotify flake reproduces every time when SIGINT is ignored at entry; `yolo host -- true` with Copilot selected no longer refuses"
date: 2026-11-25
tags: [lab-report, macos, approvalnotify, flake, yolo-host]
summary: "The hypothesis in approvalnotify-decision-flake is confirmed. With SIGINT ignored when the test starts, as in a background job of a shell without job control, TestDecisionInterruptsAnInFlightCLIRatherThanKillingIt fails 3 of 3 with the recorded signature (status=-1 out=\"\"). The fixture's sh cannot trap an inherited ignored SIGINT. Run normally it passes 10 of 10. Separately, host-launch-invalid-context-recheck passes: `yolo host -- true` with the Copilot pack selected exits 0 with no 'invalid macos-user launch context'. That launch also showed the owner's board program failing to build on darwin, because it calls Linux-only syscalls."
---

# Mac at `e71c48e9c`: the approvalnotify SIGINT flake, and the `yolo host` recheck

**Checked 2026-11-25 on the owner's Mac** (macOS 26.5, darwin/arm64, Go 1.26.7), at main
`e71c48e9c`. There were no new commits since the previous Mac report. CI's failures at
`e71c48e9c` are the Linux integration shards only, so this run took two Mac-runnable backlog
items. Nothing in yolo was changed.

## Observed

### 1. [`approvalnotify-decision-flake`](../plans/BACKLOG.md#approvalnotify-decision-flake): confirmed, and it reproduces every time

**The rule:** a non-interactive POSIX shell ignores SIGINT for an asynchronous command (`cmd &`),
and a `trap` on a signal that was ignored on entry has no effect. The test fixture
(`internal/approvalnotify/main_test.go:261`) is such a `sh` script trapping INT.

**This Mac shows the rule, measured** (`sigint-disposition.txt`):

| Started from | SIGINT seen by the child | Does the `sh` trap fire? |
| :--- | :--- | :--- |
| the agent's shell, foreground | default handler | yes |
| the agent's shell, `&` | default handler: this shell has job control on, so it does not ignore SIGINT | yes |
| `bash -c '… & wait'` (no job control) | `1` (`SIG_IGN`) | **no**: prints only `after-kill` |
| Python with SIGINT set to `SIG_IGN`, then `sh` | inherited | **no** |

**The test against the rule** (`go test -c` binary, `-run
'^TestDecisionInterruptsAnInFlightCLIRatherThanKillingIt$'`):

| How run | Result |
| :--- | :--- |
| foreground | 5 of 5 pass (`fg.txt`) |
| the agent shell's `&` (SIGINT not ignored) | 5 of 5 pass, 0.18–0.19 s (`bg.txt`) |
| inside `bash -c '… & wait'` (SIGINT ignored) | **3 of 3 fail**, 5.18–5.24 s, each `main_test.go:291: status=-1 out="" err=<nil>; want the CLI interrupted and exiting 7` (`bg-sigign.txt`) |

That is the backlog's recorded signature: the cancel sends SIGINT, the trap never runs, and the
CLI is killed after `decisionWaitDelay` (five seconds). Go's `os/exec` passes an ignored
disposition on to the child, so the test binary hands its own ignored SIGINT to the fixture.

So this is not a timing flake. It is deterministic whenever the suite is started with SIGINT
ignored. That happens with a `just test-fast &` from a script, under some CI wrappers, or
under `nohup`.

The same inheritance reaches production. A `yolo` process started with SIGINT ignored passes
that on to the approval CLI `Decision` runs, so the CLI is killed rather than interrupted.
This was not measured.

### 2. [`host-launch-invalid-context-recheck`](../plans/BACKLOG.md#host-launch-invalid-context-recheck): passes

The command: `yolo host -- true </dev/null`, from an empty throwaway workspace. The installed
`yolo` is `0.19.0+148.g3264dd695740`, which contains `662d9103b`. The user config selects
`copilot` (`yolo config why packs`).

| Environment | Exit | `invalid macos-user launch context` |
| :--- | :--- | :--- |
| the agent's session (it inherits `YOLO_*` and `PI_CODING_AGENT_DIR` from the `yolo host` launch it runs under) | 0 | absent (`host-true.txt`) |
| every `YOLO_*` and `PI_CODING_AGENT_DIR` unset, `YOLO_ALLOW_MISSING_PROGRAMS=1` | 0 | absent (`host-true-clean.txt`) |

Both runs ended with `yolo host: starting true (from your PATH, /usr/bin/true)`.

Side effects:
- The first run updated claude in yolo's floor, `2.1.330 → 2.1.332`, as a host launch does.
- It captured claude as `0a9bc61763abc8fd`.

### 3. Seen in passing: the owner's `board` program does not build on darwin

`yolo-packs/board`'s program builds `board` from the owner's dotfiles repository, at commit
`870a5564b72b`. The build fails in the sealed build jail, because
`tools/board-cli/internal/board/drain.go:6088-6124` calls Linux-only syscalls:
- `syscall.InotifyInit1`, `IN_*`, `InotifyAddWatch` and `Pipe2`;
- `syscall.Select` with Linux's two return values.

Because of `YOLO_ALLOW_MISSING_PROGRAMS`, a `yolo host` launch on this Mac proceeds without
`board`. Without it, the launch stops. This is outside this repository. It is noted here
because it blocks every host launch on the Mac.

## Fixed and committed

Nothing. This report only.

## Open

1. **approvalnotify:** the cause is confirmed. *Decision needed:* how to fix it:
   - in the fixture: restore SIGINT before `sh`, for example by having a Go-side helper reset
     it, or start the fixture through a child that resets the disposition;
   - in `Decision` itself, so a `yolo` started with SIGINT ignored still interrupts the approval
     CLI;
   - or both.

   A regression test can run the existing test with SIGINT set to `SIG_IGN` in the parent;
   it fails today every time.
2. **`host-launch-invalid-context-recheck`** reproduces no refusal. *Decision needed:* close
   the item.
3. **`board` on darwin** is the owner's dotfiles repository, not yolo. *Decision needed:* the
   owner's, whether to gate `drain.go` to Linux.

## Evidence

All of it is under `/Users/Shared/yolo/evidence/2026-11-25a/`, captured at `e71c48e9c`
(`HEAD.txt`):

| File | Contents |
| :--- | :--- |
| `sigint-disposition.txt` | the SIGINT disposition probes |
| `fg.txt`, `bg.txt` | the test run in the foreground and from the agent shell's `&` |
| `bg-sigign.txt` | the test run with SIGINT ignored |
| `session-yolo-vars.txt` | the `YOLO_*` names the agent's session inherits |
| `host-true.txt`, `host-true-clean.txt` | the two `yolo host -- true` runs, ending `exit=0` |

The reproducing command:

```sh
bash -c "for i in 1 2 3; do approvalnotify.test -test.run '^TestDecisionInterruptsAnInFlightCLIRatherThanKillingIt\$' -test.count=1 -test.v; done & wait"
```
