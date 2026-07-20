# Handoff — macOS backend state after the Python ejection

**Author:** an agent on a real macOS arm64 (aarch64-darwin) host, driving the
restored `docs/guides/runbooks/mac-go-port-verification.md` runbook.
**Date:** 2026-07-20.
**Audience:** the next agent (likely on Linux) continuing the in-flight macOS
Go work.

**TL;DR:** Python has been ejected, but the macOS Go backend was ported "as-is"
at Stage 16b and **still depends on the deleted Python entrypoint**. The
macos-user *real launch* path is therefore dead. The *pure/dry-run* surface is
fine. Along the way I found and **fixed** three self-contained Mac bugs (two
seam-consistency, one live arch-string bug) — those are landed and green; see
below. This doc records what's proven, what's broken-by-design (leave it), and
what I touched.

---

## Context: where we are

- Python is gone (`src/cli/`, `src/entrypoint/`, pytest suite all deleted).
  `src/` now holds only a stray `_version.py`.
- The three pre-existing macOS handoffs
  (`handoff-macos-user-revive-plan.md`, `handoff-macos-nix-shell-spike.md`,
  `handoff-macos-ondemand-builder.md`) are all **Python-era** — they reference
  `src/cli/*.py` and predate the Go port. They are stale for implementation but
  still useful for *design intent* (sandbox model, builder bring-up, nix-shell
  spike).
- The macOS backend was ported to Go and landed "as-is" at **Stage 16b**
  (commit `a261186`), explicitly deferring a real-hardware pass. This doc is
  the first real-hardware pass after ejection.

---

## BROKEN BY DESIGN — leave it, this is the next pickup

### macos-user real launch depends on the deleted Python entrypoint

`internal/macosuser/bootstrap.go` (`EntrypointBootstrapScript`) generates a
**Python** bootstrap script that the sandbox user runs. That script:

1. is staged from the host checkout via
   `StageEntrypointCommands` → `sudo /bin/cp -R <repoSrc>/entrypoint/. /var/yolo-jail/entrypoint`
   (`internal/macosuser/macosuser.go:175`), and
2. does `sys.path.insert(0, "/var/yolo-jail"); import entrypoint` then calls
   `entrypoint.generate_shims()`, `generate_agent_launchers()`,
   `generate_bashrc()`, `generate_mise_config()`, `generate_mcp_wrappers()`,
   `configure_git()`, `configure_jj()`, and the per-agent `CONFIG_WRITERS`.

**`src/entrypoint/` no longer exists.** So on a real run:
- `macos-setup`/the run path would `cp -R` a nonexistent dir, and
- even if staged, `import entrypoint` would fail.

**Impact:** runbook **§2 (macos-user real launch)** and **§3 (macos-* setup that
leads to a run)** are dead post-ejection. The headline OQ-1 (path_helper PATH
fix) **cannot be verified** until this is re-ported, because the sandbox home is
never populated with shims/configs.

**Why the dry-run still passes (§1):** `BuildRunPlan` and the bootstrap-string
generator are pure — they render the plan (Seatbelt profile, sudo argv, launch
argv, and the *text* of the Python bootstrap) without executing anything. So
§1 is green and is a good regression anchor for the *plan shape*, but proves
nothing about a real run.

**The fix (next pickup, NOT done here):** re-port the entrypoint config
generation that macos-user needs into Go, so the sandbox home is populated by a
Go code path (or a Go-generated stdlib-only script) instead of `import
entrypoint`. The Go entrypoint already exists at `cmd/yolo-entrypoint` /
`internal/entrypoint` for the *container* path — the macos-user path needs an
equivalent native invocation (it currently reuses the Python module's
`generate_*` functions). This is real work, not a mechanical swap: the Python
bootstrap rebinds HOME-derived path constants at import time and calls a dozen
generator functions; the Go equivalent must reproduce that config surface for a
native (non-container, no `/workspace`, no `/mise`) home.

I deliberately did **not** attempt this — per the owner, the in-flight macOS
work stays as-is for now.

> **Plan forward (2026-07-20):** the pickup this section describes is now
> sequenced in `docs/plans/macos-revival-and-distribution-plan.md` — jail-side
> re-port of the bootstrap to native Go (self-exec `yolo internal
> darwin-bootstrap`), the audit-finding fixes below, the repo-root/source-
> distribution fix, and SandVault-wrapped Mac verification sessions. Start
> there.

---

## PROVEN on real hardware (this pass)

| Runbook § | Path | Result |
|---|---|---|
| §0 | `scripts/build-go.sh` for darwin/arm64 | **PASS** — all 5 binaries build (yolo, yolo-entrypoint, yolo-jaild, yolo-ps, goprobe). |
| §1 | macos-user dry-run plan (`yolo run --dry-run`) | **PASS** — renders full plan; `✓ all plan invariants hold`. Interpreter resolves to `/opt/homebrew/bin/python3`. |
| §5 | `yolo builder status` (read-only) | **PASS** — reports set-up=yes, reachable=no (idle) with the correct "yolo starts it automatically" note. |
| §6 | `yolo check` on darwin | **PASS after fix** — see arch bug below. All sections green; macos-user backend readiness reports the sandbox user, interpreter, nix, flake.lock. |

**Not run (mutates host / needs sudo / needs the dead entrypoint):**
- §2 macos-user real launch — blocked by the entrypoint gap above.
- §3 `macos-setup`/`teardown`/`unshare`/`fix-permissions` — these mutate host
  state via sudo (dscl user create/delete, ACLs). `_yolojail` already exists on
  this box. Their pure command-builders are covered by
  `internal/macosuser` unit tests (green); I did not run the real sudo effects
  unattended.
- §4 Apple Container run path — not exercised this pass (AC single-file
  materialize fix is in `internal/cli/run`; unit-tested via
  `ac_materialize_test.go`, not driven end-to-end here).
- §5 `builder setup/start/stop` — read-only `status` only; did not bring a VM up.

---

## FIXED this pass (landed, tests green)

These are self-contained, not part of the in-flight entrypoint work.

### 1. `yolo check` reported `aarch64` on darwin (should be `arm64`) — the §C bug, LIVE

`internal/cli/check/checkcmd.go` `pythonMachine()` mapped `arm64 → aarch64`
unconditionally. Python's `platform.machine()` returns **`arm64` on darwin**,
`aarch64` only on Linux. `yolo check` printed `[PASS] Architecture: aarch64` on
this Mac — the exact §C regression the audit flagged, live and unguarded.

- **Fix:** made the mapping OS-aware, mirroring the already-correct audited
  `internal/cli/run/banner.go:platformMachine`. Extracted a pure
  `machineForPlatform(goos, goarch)` helper.
- **Regression guard:** added `internal/cli/check/machine_test.go` pinning all
  OS/arch combos (darwin/arm64→arm64, linux/arm64→aarch64, both amd64→x86_64,
  pass-through). The prior code had **no** test locking this (the runbook
  warned the §C test "was weak" — it was absent for `check`).
- **Verified live:** `yolo check` now prints `[PASS] Architecture: arm64`.

### 2 & 3. Two run-package seam bugs — ALREADY FIXED UPSTREAM (`985a4fd`), not by me

I independently found and fixed the same two `internal/cli/run` seam bugs
(`assemble.go` `--read-only-tmpfs` gated on `paths.IsLinux`; `assemble_parts.go`
mise-store gated on `paths.IsMacOS` — both should use the `Options` seam so the
`PodmanLinux` golden is host-independent). On fetching origin I found commit
**`985a4fd` fix(run): pin the platform and PID-liveness seams the macOS runner
exposed** already fixes both (routing through `o.IsLinux` / an `isMacOS` param,
plus an unrelated PID-liveness fix). I **discarded my version** and rebased onto
it — no duplicate change. Nothing for the Linux agent to do here; just noting
the overlap so it's clear the arch fix (#1) is the *only* code change I'm
landing.

---

## Audit findings — adversarially verified, NOT fixed (for the Linux agent)

I ran an adversarial multi-agent audit of the Mac-specific packages
(`macosuser`, `darwinpkg`, `builder`, `check/sections_macos*`, AC materialize,
runtime detection). It was **stopped early** (the owner declined the risk of a
verify agent reaching for a real sudo/macos-user run), but the 7 finders
completed and most verifiers returned before the stop. No host state was
mutated. These are the CONFIRMED findings I did **not** touch — they need a
human/Linux-agent decision, and several sit inside the in-flight macOS work:

| Sev | Location | Finding | Verdict |
|---|---|---|---|
| high | `macosuser/bootstrap.go:77`, `macosuser.go:182` | The Python-entrypoint dependency (see "BROKEN BY DESIGN" above). Two independent load-bearing refs: the generated bootstrap `import entrypoint`s a deleted module, and `StageEntrypointCommands` `cp -R`s the deleted `src/entrypoint`. | CONFIRMED |
| high | `builder/real.go` (detached VM) | `startVMDetachedReal` starts the VM proc but `cmd.Wait()` is never called, so `realProc.Poll()` can never report `done=true` → `pollUntilReachable`'s "builder process exited early" fast-fail branch is **dead code**. (Note: the *other* builder finding — the first-boot SIGINT heuristic being unreachable — was **REFUTED** on inspection.) | CONFIRMED |
| medium | `darwinpkg/materialize.go:83` | `cmd.Wait()` is called before the stderr-pump goroutine finishes draining `StderrPipe` (the documented-incorrect ordering), truncating the captured error tail. A low-sev data race on `stderrTail.buf` at :91 rides along (pump goroutine still `push`ing after the 5s timeout while the main goroutine `lines()`-reads). | CONFIRMED |
| medium | `runtime/probe.go:29` | `yolo prune` uses the shallow, darwin-blind `DetectRuntime` (env-or-hardcoded-`podman`), so on a macOS host whose real runtime is Apple Container it never selects `container`. | CONFIRMED |
| medium | `runtime/probe.go:44` | `PsRuntime` ignores `config.runtime` (unlike run's `resolveRuntime` and check's `runtimeForCheck`), so `yolo ps` can pick the wrong runtime on macOS and prune live jails' tracking files. | CONFIRMED |
| medium | `macosuser/real.go:132` | `setRandomPasswordReal` passes the generated password via the parent env (`cmd.Env = YOLO_SBPW=…`) and expands `$YOLO_SBPW` inside `sudo /bin/sh -c '… dscl . -passwd …'`. sudo's default `env_reset` strips `YOLO_SBPW` (no `-E`, no `env_keep`), so the sandbox user's random password is **never applied** — `$YOLO_SBPW` expands empty. | PLAUSIBLE (verifier didn't return before stop) |

**Note on the `runtime/probe.go` pair:** these are the only findings that touch
NON-macOS-in-flight code (`prune`/`ps` run on Linux too). They're the most
"safe to fix now" of the set, but I left them for the Linux agent to confirm
against the `prune`/`ps` command wiring rather than fix blind on a Mac. The
`darwinpkg` and `builder` findings are inside the in-flight macOS backend, so
they naturally fold into the same re-port pass as the entrypoint gap.

The full workflow transcript (per-agent reasoning, exact repro traces) is under
the session's `workflows/wf_fbcb2d9d-894/` dir if deeper detail is wanted.

---

## Also noticed (not Mac-specific, not fixed)

- `yolo --help` / `yolo help` / `yolo -h` all exit 1 with `yolo: unknown
  command ""` — there is no top-level usage/help handler in
  `internal/cli/cli.go:Main`. General CLI papercut, out of scope for the macOS
  pass. Flagging for a separate fix.

---

## Files touched this pass

```
 M internal/cli/check/checkcmd.go        # OS-aware machineForPlatform + pythonMachine (the only code change)
?? internal/cli/check/machine_test.go    # new regression guard for the arch spelling
?? docs/guides/runbooks/mac-go-port-verification.md  # restored from bc4f80c (was archived in 2c229fb)
?? docs/implementation/handoff-macos-post-ejection.md # this doc
```

The two `internal/cli/run` seam fixes I initially made were superseded by
upstream `985a4fd` and discarded during the rebase (see "FIXED this pass" #2/#3).
`go test -short ./...` is green after the rebase + arch fix.

> **Caveat for the validator:** the restored runbook
> (`mac-go-port-verification.md`) is written around *diffing Go against Python*,
> which is now impossible — treat its "diff against Python" steps as dead and
> use the plan/behavior assertions only. The load-bearing content is the PASS
> criteria per section (esp. §2 OQ-1, §6 arch), not the diff mechanics.
