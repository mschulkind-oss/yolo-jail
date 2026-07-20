# Mac runbook — verify the Go port on real Apple hardware

**Audience:** an agent (or human) on a real Mac (Apple Silicon, macOS ≥ 14).
**Goal:** certify the macOS-only surface of the Go `yolo` port that CANNOT be
tested on Linux or inside a jail — so Python can eventually be deleted. Everything
here is `DEFER`red in the Linux review because it needs Darwin + real hardware.

**Prereqs on the Mac:** the yolo-jail repo cloned, Go toolchain, `nix` (flakes),
and — for the two backends — either Apple Container (`container`) or the
macos-user path. You are the security boundary; some steps use `sudo`.

**Bail-back is armed:** Python is still installed. If any Go step misbehaves,
the same command under `uv run python -m src.cli …` is the reference. Several
steps below are explicitly **diff Go against Python** — that IS the test.

---

## 0. Build the Go binaries for darwin

```sh
cd <repo>
GOOS=darwin GOARCH=arm64 ./scripts/build-go.sh      # → dist-go/darwin-arm64/
# sanity: the front door runs and self-identifies as Go
YOLO_IMPL=go YOLO_GO_BIN_DIR="$PWD/dist-go/darwin-arm64" \
  dist-go/darwin-arm64/yolo internal 2>&1 | head -1
#   expect: "usage: yolo internal <config-dump> ..."  (Go-only command; Python has no `internal`)
```

To run the whole flow the way a user would, install the standalone `yolo-go`
wrapper (bakes the four-var shim; does NOT flip the default `yolo`):

```sh
just install-go        # creates ~/.local/bin/yolo-go
yolo-go internal | head -1   # Go
yolo internal 2>&1 | head -1 # Python (typer usage) — proves both coexist
```

Everything below assumes you invoke the Go path via `yolo-go` (or the four-var
shim). **Never** bare-export `YOLO_IMPL=go` — that drops bundled loopholes.

---

## 1. macos-user backend — dry-run parity (no privilege, do this first)

The dry-run is pure (no nix build, no sudo) and prints the full run plan. It's
already goldened on Linux; on the Mac confirm it renders and matches Python.

```sh
mkdir -p /tmp/mac-macostest && cd /tmp/mac-macostest
printf '{ "runtime": "macos-user", "agents": ["claude"] }\n' > yolo-jail.jsonc

# Go plan
yolo-go run --dry-run -- echo hi > go-plan.txt 2>&1
# Python plan (reference)
YOLO_RUNTIME=macos-user uv run --project <repo> python -m src.cli run --dry-run -- echo hi > py-plan.txt 2>&1

# Info-parity: word-multiset identical (rich soft-wrapping of long paths is the
# only accepted difference — the byte-pinned ARTIFACTS inside are what matter).
diff <(tr -s ' \t\n' '\n' < go-plan.txt | sort) \
     <(tr -s ' \t\n' '\n' < py-plan.txt | sort) && echo "PLAN PARITY OK"
```

**PASS:** the two plans match (modulo rich wrapping). The plan should show the
Seatbelt (SBPL) profile, the `sudo … dscl` provisioning commands, the
`sudo --login … env -i … sandbox-exec … /bin/zsh -c 'cd … && exec …'` launch
argv, and the generated Python bootstrap.

**⚠ Record:** whether `interpreter:` resolves (the plan prints candidates like
`/opt/homebrew/bin/python3`). On a bare Mac it may be `<unresolved>` — that's a
precondition, not a Go bug.

---

## 2. macos-user backend — real launch (OQ-1, the load-bearing unknown)

This is the step that has NEVER been verified — the Go port reproduces the
bootstrap + login-rc writes byte-for-byte, but the *runtime effect* of the
`path_helper` PATH fix (OQ-1) can only be seen on a Mac.

```sh
# One-time sandbox-user provisioning (writes root-owned files; sudo prompts):
yolo-go macos-setup

# Launch a real agent under Seatbelt as the sandbox user:
cd <a real project>
yolo-go -- claude       # or: yolo-go -- bash -lc 'echo IN-SANDBOX; which node; echo $PATH'
```

**PASS criteria:**
1. The sandbox launches (no `sandbox-exec` error, no missing-interpreter abort).
2. **OQ-1 — the PATH fix works:** inside the sandbox, `echo $PATH` starts with
   `/Users/_yolojail/.yolo-shims:…` (the yolo shims win) — NOT reordered behind
   macOS `path_helper`'s defaults. Run `which node`/`which yolo` and confirm they
   resolve to the jail shims, not `/usr/bin`. **This is the OQ-1 assertion.**
3. The agent's writes land only in the allowed set (workspace, `/Users/_yolojail`,
   `/tmp`) — try writing outside (e.g. `touch /etc/x`) and confirm it's denied.
4. `yolo-go macos-teardown` cleanly removes the sandbox user + profile.

**Diff against Python:** run the same `yolo-go -- bash -lc 'echo $PATH; which node'`
vs `YOLO_RUNTIME=macos-user uv run … python -m src.cli -- bash -lc '…'` and
confirm identical in-sandbox environment. Divergence here is a real Go bug.

**If packages are set** (`"packages": ["fzf"]`): the launch triggers a native
`aarch64-darwin` nix build (streaming stderr). Confirm it builds and the package
is on PATH in the sandbox. This exercises `darwinpkg.Materialize` (the impure
nix streaming build ported this cycle) — verify the build progress streams and
the profile's `bin/` lands on the sandbox PATH.

---

## 3. macos-* commands

Each is macOS-only; verify they run and match Python's effect:

```sh
yolo-go macos-setup            # idempotent; re-run should be a no-op
yolo-go macos-fix-permissions <path>   # ACL fix on a workspace
yolo-go macos-unshare <workspace>      # per-workspace teardown
yolo-go macos-teardown         # full removal
```

**PASS:** each produces the same dscl/ACL effect as the Python equivalent.
Spot-check with `dscl . -read /Users/_yolojail` before/after setup/teardown and
`ls -le <path>` for the ACL ACEs.

---

## 4. Apple Container runtime (`runtime: "container"`)

The container-builder cell is already PROVEN on real HW (2026-07-17, see
`mac-ac-container-builder.md`). Here, verify the **Go run path** drives it:

```sh
cd <project>
printf '{ "runtime": "container" }\n' > yolo-jail.jsonc
yolo-go -- bash -lc 'echo AC-GO-OK; uname -a'
```

**PASS:**
1. The jail launches under Apple Container (a Linux container in AC's per-VM).
2. **AC single-file materialize (fixed this cycle):** confirm `env_sources` vars
   and the agent briefing actually reach the jail — AC can't do single-file
   mounts, so `yolo-user-env.sh` and the AGENTS.md/CLAUDE.md briefing must be
   *materialized into ws_state*. Inside the jail: `env | grep <an env_sources
   var>` and `cat ~/.claude/CLAUDE.md` (or the selected agent's briefing) — both
   must be present. On the pre-fix code these silently vanished on AC.
3. Diff the container argv: `YOLO_DEBUG=1 yolo-go -- true 2>&1 | grep -i 'container run'`
   vs the Python equivalent — should be byte-identical.

---

## 5. Builder VM (`yolo builder …`)

macOS on-demand Linux builder (for container runtimes needing a Linux image build).

```sh
yolo-go builder status
yolo-go builder setup       # first-boot may be interactive (nix run darwin.linux-builder)
yolo-go builder start
yolo-go builder stop
```

**PASS:** `builder setup` state (SSH key install, trusted-users, root script)
matches Python's — diff `yolo-go builder status` output against
`uv run … python -m src.cli builder status`. **⚠ OQ:** the first-boot path runs
`nix run nixpkgs#darwin.linux-builder` in the foreground and treats a
SIGINT-terminated child as success if the key installed — confirm that heuristic
behaves on real hardware (it's ported as-is, unverified).

---

## 6. `check` / `doctor` on macOS

```sh
yolo-go check    # or: yolo-go doctor
```

**PASS:** exit code + ANSI-stripped output match `uv run … python -m src.cli
check` on the same Mac. Pay attention to the macos-user backend-readiness section
and the platform naming (`arm64`, NOT `aarch64` — the Go port keeps `arm64` on
darwin per `platform.machine()`; a wrong `aarch64` here is the §C bug this cycle
was told to guard, and its test was weak — so verify it live).

---

## What to report back

For each section: PASS / FAIL / N-A, the diff output where a diff was requested,
and specifically:
- **§2.2 OQ-1** — does the sandbox PATH put the jail shims first? (the headline
  unknown)
- **§2** — does `darwinpkg.Materialize` build `packages:` natively and land them
  on the sandbox PATH?
- **§4.2** — do `env_sources` + briefings reach the AC jail (the materialize fix)?
- **§6** — does `check` report `arm64` (not `aarch64`) on darwin?
- Any divergence from the Python reference in a diff step — that's a real Go bug
  to file.

These close blocker **F.2 (macOS verified on real hardware)** in
[go-port-remaining-work.md](../../implementation/go-port-remaining-work.md).
Until they pass, macOS is a hard blocker on deleting Python.
