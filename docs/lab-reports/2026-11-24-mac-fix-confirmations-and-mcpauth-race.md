---
title: "Mac run at 6551bbc6f: the 11-23 report's fixes hold on darwin; the mcpauth two-broker failure is a concurrency race, and the race detector flags the shared fakeClock; no runnable Mac witness is owed"
date: 2026-11-24
tags: [lab-report, macos, testing, config-edit, mcp-auth]
summary: "At main 6551bbc6f on the owner's Mac, from a `yolo host` session with its YOLO_* variables still set: the 16 env-sensitive tests, the 2 load flakes, the matched-bundle fixture and the gitremote wait all pass. The config-edit diff marks every line. The env.set refusal names a verb, but that verb's result differs from removing the key. TestOutcomeTwoBrokersAdoptOneSingleUseToken fails 8 of 10 times as separate processes and 0 of 10 at GOMAXPROCS=1, and -race reports a data race on the fakeClock both brokers share. Every Mac witness still owed needs podman running, Apple Container, a live launch, sudo or an owner sign-in."
---

# Mac run at `6551bbc6f`

**Run 2026-11-24 on the owner's Mac** (darwin/arm64, Go 1.26.7), at main `6551bbc6f`, from a
Claude Code session that runs under `yolo host`, so the shell carries that launch's `YOLO_*`
variables (`YOLO_JAIL=1`, `YOLO_USE_INSTALLED=copilot`, `YOLO_ALLOW_OLDER_UPSTREAM`,
`YOLO_ALLOW_MISSING_PROGRAMS=1`, `YOLO_USE_PROFILES`, `YOLO_PROVIDERS`, `YOLO_PROFILES`).

- Go tests ran from the checkout.
- The config-edit checks used a `yolo` built from the checkout with `go build ./cmd/yolo`. The
  installed `yolo` is still `0.19.0+61.gfe37424b3605`.
- Nothing in yolo was changed.

This run follows up the 2026-11-23 Mac report, which `c0d4ebf85` folded into its owner documents
and retired. It is in git at `28e067f9d`.

## Observed

| Check | Result |
| :--- | :--- |
| The 16 env-sensitive tests (`7fbcc8a32`) | **Pass** with the `yolo host` variables set |
| `TestDecisionInterruptsAnInFlightCLIRatherThanKillingIt` and `TestGitPreflightEOFWaitsForOrdinaryExitStatus` (`ad9139dd2`, `40b772eff`) | **Pass**: the gitremote test passed 5 of 5 |
| `scripts/test_matched_bundle.py` (`475380a96`) | **Passes**: `OK` |
| The config-edit diff (`787a86a90`) | **Fixed**: every line of a multi-line hunk is marked, see [§1](#1-config-edits) |
| `yolo config unset env.set.<NAME>` (`787a86a90`) | **Names a verb now**, but the verb does not do what removing the key does, see [§1](#1-config-edits) |
| `TestOutcomeTwoBrokersAdoptOneSingleUseToken` | **Still fails**. It is a concurrency race, and `-race` reports a data race on the shared `fakeClock`, see [§2](#2-the-mcpauth-two-broker-race) |
| Owed Mac witnesses | **None is runnable without a precondition the owner holds**, see [§3](#3-owed-mac-witnesses) |

### 1. Config edits

In a scratch workspace with an empty `yolo-jail.toml`, the commands were: set two
`env.secrets.set` keys, unset both, then set and try to unset `env.set.PUBLIC_A`.

```text
@@
+[env.secrets.set]
+AWS_ACCESS_KEY_ID = "<redacted>"
…
@@
-[env.secrets.set]
-AWS_SECRET_ACCESS_KEY = "<redacted>"
```

Every line now carries its marker, the file ends at 0 bytes after the secret unsets, and
`not-a-real` appears nowhere in the output.

`yolo config unset env.set.PUBLIC_A --project` still refuses, now with *"an environment variable is
removed by listing its name in env.unset; run `yolo config add env.unset '"PUBLIC_A"'` (add the
scope flag you used here)"*. Running `yolo config add env.unset '"PUBLIC_A"' --project` exits 0,
and the file becomes:

```toml
env.unset = ["PUBLIC_A"]
[env.set]
PUBLIC_A = "a"
```

`yolo config check` passes (`0 failed`), and `yolo describe --json` shows `PUBLIC_A` in both
`env.set` and `env.unset`. Under [RC-D50](../reference/config/rulings.md#RC-D50)'s walk order
(the merged `env.set`, then the union of `env.unset`), the variable ends up unset. So the suggested
verb hides the variable rather than removing the key, and it differs from a removal in two ways:

- the dead `PUBLIC_A = "a"` line stays in the file;
- the `env.unset` entry also cancels a `PUBLIC_A` that another layer sets, such as the user
  config. Deleting the project's line would let that other layer's value through.

Whether that difference matters is for the config owner to decide.

### 2. The mcpauth two-broker race

The test is in `internal/mcpauth/outcome_concurrency_test.go`, and its backlog entry is
[`mcpauth-two-brokers-darwin`](../plans/BACKLOG.md#mcpauth-two-brokers-darwin).

| Run | Failed |
| :--- | :--- |
| `go test -count=20` in one process | 17 of 20 |
| a compiled test binary, 10 separate processes, `-test.count=1` | 8 of 10 |
| the same, with `-test.cpu 1` (`GOMAXPROCS=1`) | **0 of 10** |
| a `-race` build, 10 separate processes | 9 of 10, with 5 `WARNING: DATA RACE` reports |

Every failure is `slack: openat records.lock: no such file or directory`. So it is not state
shared across `-count` repetitions, which was the backlog's hypothesis (2). It needs real
parallelism.

**The first race report** writes from `(*fakeClock).Sleep` (`internal/mcpauth/source_test.go:18`),
called through `SourceStore.sleep` (`source.go:54`), `SourceStore.lock` (`source.go:235`),
`SourceStore.Update` (`source.go:181`), `OnceBroker.writeBack` (`once.go:470`) and
`OnceBroker.Pass` (`once.go:161`) from the test's broker goroutine
(`outcome_concurrency_test.go:21`).

- `fakeClock` is `struct{ now time.Time }` with no lock, and both brokers in the test share it.
- Its `Sleep` advances the shared time. So while two brokers wait, fake time moves past the
  source lock's `Wait` (2 s) and `Stale` (15 s, set in `fakeClock.source`) faster than either
  broker's own waiting would move it.

That would explain why the failure needs parallelism. It does **not** yet explain the `records.lock`
ENOENT in the canonical directory: that link was not traced. Only the first of the 5 race reports
was read. All of them are in the evidence log.

### 3. Owed Mac witnesses

A read-only sweep of every `## Mac checks` and `## Mac witnesses` section, at `28e067f9d`, found
no owed witness that runs without one of these: podman running (the podman machine has been
stopped for 4 weeks), Apple Container, a live jail or Seatbelt launch, `sudo` for the yolo account,
an owner sign-in (Slack `/mcp`, Keychain), or a destructive step such as `container system stop`.
The owed ones are W-PATH1, W-TMP1's hatch and tests, W-PH2, W-PH3, W-PH4's live half, W-GB1,
W-SU2, W-SU11, W-SU14's stop half, MCP one sign-in checks 1 to 6, install-capture items 3 to 6,
and W-SU5's unresolved mismatch.

## Fixed and committed

Nothing in yolo. This report is the only commit.

## Open

1. [§2](#2-the-mcpauth-two-broker-race): make the test's clock safe for two brokers (a mutex, or
   one clock per broker), rerun on a Mac under `-race`, and see whether the ENOENT goes with it.
   If it does not, trace how a broker's wait reaches the canonical directory's `records.lock`.
2. [§1](#1-config-edits): decide whether `env.set` removal should delete the key, or whether
   hiding it with `env.unset` is the intended answer, and if so, say what it does to other layers.
3. [§3](#3-owed-mac-witnesses): the owed witnesses need the owner to start the podman machine, or
   to allow account-mode and live launches in the Mac agent's session.

## Evidence

In `/Users/Shared/yolo/evidence/2026-11-24/`: `HEAD.txt`, `fixes-dirty-env.log`,
`matched-bundle.log`, `config-edit-raw.log`, `env-unset-add.log`, `env-both-check.log`,
`mcpauth-x20.log`, `mcpauth-separate.log`, `mcpauth-separate-cpu1.log` and `mcpauth-race.log`.
Everything ran against `6551bbc6f`.
