# Mac agent guide — how to run the Track M verification

**Date:** 2026-07-21. **Audience:** an agent (or human) working on a real Apple
Silicon Mac, tasked with the runtime verification an in-jail agent structurally
cannot do. **Role of this doc:** it is the top-level *sequencer* for the
Track-M runbooks against the code that just landed (J2 + J3). It does **not**
restate their steps — each runbook is the source of truth for its own procedure;
this doc says *what order to run them in, what each one now verifies, which
landed code it exercises, and the pass/fail signal*. Follow the links for the
actual commands.

Roadmap context: [../ROADMAP.md](../ROADMAP.md) "What unblocks the gated lanes"
and [../macos-revival-and-distribution-plan.md](../macos-revival-and-distribution-plan.md)
Track M (M0 → M1 → M2).

---

## 1. Goal and the split — what is done vs. what remains

The macOS backend re-port (Track J: **J2** native-Go macos-user bootstrap,
**J3** container-builder offload) is **CODE-COMPLETE and jail-verified**:

- `just test-fast` green (unit + short-gated compile);
- `GOOS=darwin GOARCH=arm64 ./scripts/build-go.sh` cross-build of all binaries
  green;
- nested-jail sanity run (`yolo -- bash`) confirms the shared Linux entrypoint
  generators still boot a container;
- hermetic in-jail `nix build .#ociImage` proves the flake still evaluates.

What **cannot** be verified from inside a jail is **runtime behavior on real
Apple Silicon** — a hidden macOS user, `sudo`/`dscl`, Seatbelt (`sandbox-exec`),
macOS `path_helper`, Mach-O signature caching, native `aarch64-darwin` nix, and
Apple Container networking. That is this task. Every behavior below is ported
byte-for-byte or unit-tested but has **never been observed executing on
hardware**; M1 is the sole place OQ-1 (path_helper PATH) and finding-6 (password
apply) get observed at all.

**You are the security boundary.** The macos-user commands self-escalate per-op
(they prompt for your admin password); do **not** wrap `yolo` in `sudo` — the
CLI refuses to run as root, and running as root would grant the shared-workspace
ACL to `root` instead of you.

---

## 2. Prerequisites for the Mac agent

- **Apple Silicon Mac** (arm64), macOS ≥ 14. Confirm `uname -m` → `arm64`.
- **An admin user, NOT root.** The privileged steps self-escalate.
- **nix with flakes** — required for the native `aarch64-darwin` package build
  (the acceptance bar) and for the J3 offload's host-side ssh-ng build.
- **The `yolo` binary installed** — `brew install`, `go install ./cmd/yolo`, or
  built from source. The host ship set is `{yolo}` only; the macos-user launch
  self-stages this same binary, so no other binary is needed on the Mac.
- **The source / image.** How an installed `yolo` finds the repo to build the
  OCI image is [../../research/repo-root-and-distribution.md](../../research/repo-root-and-distribution.md).
  Current state: a from-source install resolves via `repo_path` (written by
  `just deploy`, D1) or the shipped source bundle (D3, brew/release archive).
  **Cachix (D4) is enabled in `flake.nix` (`nixConfig` substituter live) but the
  cache only fills on a `v*` release or a manual `just cachix-push`** — so today
  the Mac most likely **builds from source**. macos-user with empty `packages:`
  needs no image at all; a from-source `packages:` build is exactly what the J3
  offload (step **J3** below) exists to accelerate.

---

## 3. The sequence

Run in this order. M0 is independent and startable now; M1 is the main event and
gates M2. The J3 builder cell is agent-runnable (zero sudo) and can be run
alongside M1.

### M0 — SandVault bootstrap (base Mac setup)

- **Verifies:** the Mac is ready — nix + a git checkout with its own push
  credentials (deploy key; host creds stay invisible) + `just deploy` +
  `repo_path` set, and SandVault installed so the sandboxed agent can build Go,
  run `go test`, reach the nix-daemon socket, and drive the `container`/AC CLI.
- **Driver:** Track M §M0 in
  [../macos-revival-and-distribution-plan.md](../macos-revival-and-distribution-plan.md).
  Deliverable is a new `mac-sandvault-session.md` recording the working recipe
  (does not exist yet — write it here when M0 runs).
- **Exercises:** the D1 `repo_path` path (`yolo internal write-repo-path`,
  `internal/repopath`) so an installed `yolo` finds the repo from any dir.
- **Independent:** does not wait on the jail thread. Whatever SandVault's profile
  blocks (nix daemon, AC) moves to the human column.

### M1 — macos-user end-to-end (the whole J2 re-port on hardware)

- **Runbook (drives every step):** [mac-macos-user-e2e.md](mac-macos-user-e2e.md)
  — you-drive / agent-advises. Preflight (`yolo check`) and `--dry-run` are
  zero-sudo and can run inside SandVault; §3–§7 are the human's privileged
  one-shots.
- **Verifies:** the native no-VM backend actually runs an agent as `_yolojail`
  under Seatbelt with `packages:` materialized via native aarch64-darwin nix.
  This is the first hardware exercise of the entire J2 re-port. The four
  behaviors unverified until now, each with its observation:

  1. **Under-sudo fresh-inode binary staging.**
     `macosuser.StageBinaryCommands` (`internal/macosuser/macosuser.go`) stages
     the running `yolo` (`os.Executable()`) into root-owned `/var/yolo-jail/yolo`
     via copy-to-temp-then-`mv` — an **atomic rename that guarantees a fresh
     inode**. macOS caches Mach-O code signatures per vnode, so overwriting a
     previously staged binary in place gets the next exec **SIGKILLed** (invalid
     signature). `PlanInvariants` (`runplan.go`) statically requires the `mv`.
     **Observe:** run the launch (§4) **twice** — a second run must re-stage and
     still exec, not die with SIGKILL. Confirm `/var/yolo-jail/yolo` is
     root-owned and world-readable+executable (`ls -l@ /var/yolo-jail/yolo`), and
     that it runs clean under Gatekeeper/quarantine (copied ad-hoc-signed Go
     binary — expected fine, verify).
  2. **`yolo internal darwin-bootstrap` self-exec as `_yolojail`.**
     `DarwinBootstrapArgv` (`runplan.go`) builds
     `sudo --user=_yolojail /usr/bin/env -i K=V… /var/yolo-jail/yolo internal
     darwin-bootstrap`; the subcommand `runDarwinBootstrap`
     (`internal/cli/internal.go`) self-sets `JAIL_HOME`/`HOME`, builds an
     `*entrypoint.Env`, and runs `entrypoint.RunDarwinBootstrap`
     (`internal/entrypoint/darwin.go`) — the same shims/launchers/bashrc/mise/
     MCP/identity generators the container boot runs, as the sandbox user.
     **Observe:** the bootstrap prints `yolo-jail macos-user bootstrap ok`; the
     sandbox home has generated `~/.yolo-shims`, `~/.zprofile`, `~/.zshrc`,
     `~/.bash_profile`.
  3. **finding-6 password actually applied.** `setRandomPasswordReal`
     (`internal/macosuser/real.go`) now pipes the password to
     `sudo /bin/sh -c 'read -r pw; dscl . -passwd … "$pw"'` **via stdin** (sudo's
     `env_reset` stripped the old env-var approach, leaving an *empty* password);
     its return value is now wired so failure is loud. **Observe:** after §3
     setup, `dscl . -read /Users/_yolojail` shows the user, and authentication is
     actually set (non-empty) — `dscl` empty-string semantics are the exact
     unknown this checks.
  4. **OQ-1 path_helper — login-rc PATH re-prepend wins.** `WriteLoginRC`
     (`internal/entrypoint/darwin.go`) writes `.zprofile`/`.zshrc`/`.bash_profile`
     that re-prepend the sandbox PATH (`macosuser.SandboxPath`) **after** macOS
     `path_helper` reorders it. **Observe (the acceptance bar):** with
     `"packages": ["jq"]`, `yolo -- bash -lc 'which jq'` must resolve to a
     `/nix/store/…/bin/jq` — NOT `/usr/local/bin/jq` (Homebrew) or `/usr/bin`. A
     Homebrew path here is a real OQ-1 regression; paste `echo $PATH` from inside
     the sandbox.

  The acceptance-bar build itself exercises `darwinpkg.Materialize` (native
  aarch64-darwin nix, streaming `--print-build-logs`); confirm the store `bin/`
  lands on the sandbox PATH.

- **Pass signal:** whoami → `_yolojail`, `which jq` → `/nix/store/…`, the
  password is set, teardown (`yolo macos-teardown`) is clean and idempotent.
- **Gates:** M2. M1 green is the precondition for the dogfood flip.

### J3 builder — Apple Container Linux builder cell (re-confirm)

- **Runbook:** [mac-ac-container-builder.md](mac-ac-container-builder.md) —
  **zero sudo, agent-runnable**, already **PASSED on real HW 2026-07-17**.
- **Re-verifies now that the offload is wired:** the J3 landing wired
  `BuildOffload` into `AutoLoadImage` (`internal/image/autoload.go`) —
  `buildImageWithContainerBuilder` drives `containerbuilder.Session` (start /
  wait-reachable / stop) via `realSessionDeps` + `ensureBuilderKey`
  (`internal/image/builderoffload.go`), so a failed macOS from-source `.#ociImage`
  build now **retries over the ssh-ng container builder** before falling back to a
  cached tar. The runbook proves the make-or-break piece (host nix →
  AC-container sshd → `Trusted: 1` → `AC-CONTAINER-BUILDER-WORKS`).
- **Signal:** a from-source `packages:`/image build **succeeding over the ssh-ng
  container builder** (§6 of the runbook). Because the offload only triggers when
  the plain build fails on macOS and no cache is available, this is the live path
  today (Cachix does not fill until a release).

### M2 — dogfood flip + docs (gated on M1 green)

- **Verifies:** Mac agent sessions become
  `YOLO_RUNTIME=macos-user yolo -- claude` — yolo is now its own sandbox with the
  nix layer; SandVault retires from the loop.
- **Driver:** Track M §M2 in the plan. Update the support-matrix cells
  (`docs/research/macos-support-matrix.md`), then update `docs/guides/macos.md` —
  it already frames macos-user as revived, but its runtime table still lists only
  Podman + Apple Container and does not yet present macos-user as a selectable
  runtime. Do this **after** the launch works, so the guide never advertises a
  backend that hasn't been verified on hardware.
- **Do not start until M1 is green.**

---

## 4. What to report back per step

A precise report turns a failure into a filed bug. Per step:

- **M0:** what SandVault's profile blocked (nix daemon? AC CLI? Go build?) — that
  defines the inside/outside labor split. The working recipe → `mac-sandvault-session.md`.
- **M1 §1/§2:** the `yolo check` "macOS-user backend" section + the full
  `--dry-run` plan + any `PlanInvariants` violation verbatim.
- **M1 §3:** setup output + verdict; whether `dscl . -read /Users/_yolojail`
  shows a non-empty password (finding-6).
- **M1 §4:** does it launch as `_yolojail`? any `sandbox-exec` error verbatim?
  **Does a second run re-stage a fresh inode and still exec** (no SIGKILL)?
- **M1 §5:** **the exact path `which jq` prints** (the acceptance-bar / OQ-1
  signal) + whether it built from source; if it shows `/usr/local/bin`, paste
  `echo $PATH` from inside the sandbox.
- **M1 §6:** agent starts? host `~/.gitconfig`/`~/.ssh` invisible (scrubbed HOME)?
- **J3:** which image path (§2a GHCR pull vs §2b tar); how the host reached the
  container (AC internal IP vs published port); §6 verbatim (`Trusted: N` +
  whether `AC-CONTAINER-BUILDER-WORKS` printed); any AC error at load/run/connect.

Findings come back as a handoff doc; fixes happen in the jail; repeat as needed.

---

## 5. Known rough edges (from the runbooks' own caveats)

- **sudo prompt through a proxied TTY:** the launch runs under yolo's tty-proxy;
  a mid-run sudo prompt that can't be answered is a known risk — report where it
  hung (mac-macos-user-e2e §"rough edges").
- **`/nix` not found / daemon not trusted:** the darwin build needs nix + a
  trusted user; same trusted-users wiring as the container path. Paste the error.
- **darwin build of a package with no aarch64-darwin build:** should
  **warn-and-skip**, not crash (the shipped behavior is warn-and-skip;
  `orchestrator.go` — Open Decision #5 in the plan). If a package vanishes
  silently, report which.
- **AC networking (J3 §5):** AC has no `--net=host` and networks each container
  in its own VM; the verified path is the container's internal-network IP
  (`192.168.64.2:22`, no `-p`). If AC won't expose the sshd to the host nix
  daemon, that's the AC limit — fall back to QEMU `darwin.linux-builder`, a clean
  result, not a session failure.
- **`dscl` empty-password semantics (finding-6)** and **OQ-1 path_helper** are
  the two headline unknowns until M1 observes them — do not assume, report the
  literal output.

---

## 6. Stale runbook — recommend `git rm`

[mac-go-port-verification.md](mac-go-port-verification.md) is **STALE**. Its
method is "diff each Go command against `uv run python -m src.cli …` and bail
back to Python" — dead, because the Python tree was wiped (the doc's own footer
admits it). It already carries a STALE banner. The live gates are
[mac-macos-user-e2e.md](mac-macos-user-e2e.md) and
[mac-ac-container-builder.md](mac-ac-container-builder.md), sequenced by this
guide. **Recommendation: `git rm docs/plans/runbooks/mac-go-port-verification.md`**
— the diff-against-Python method cannot be revived.
