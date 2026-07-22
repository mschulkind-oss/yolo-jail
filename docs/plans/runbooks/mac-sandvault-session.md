# Mac SandVault session — the M0 working recipe

**Date:** 2026-07-21. **Status:** M0 **PASSED** + M1 **PASSED** on real Apple
Silicon (M1 results in §6b).
**Audience:** an agent (or human) bootstrapping the Track-M verification loop on
a Mac. **Role:** this is the M0 deliverable called for by the plan
([../macos-revival-and-distribution-plan.md](../macos-revival-and-distribution-plan.md)
§M0) and sequenced by [mac-agent-guide.md](mac-agent-guide.md) — it records the
recipe that actually works and the exact inside-vs-outside labor split SandVault
imposes.

---

## 1. What M0 is for

Get the Mac ready so a **sandboxed** agent can do the quota-light dev loop —
build Go, run `go test`, reach the nix daemon, run `yolo check` / `--dry-run` —
while every **privileged** step (sudo/dscl/ACL, the real Seatbelt launch) falls
to a human outside the sandbox. M0 both installs the pieces and *measures* what
the SandVault profile blocks, which is what fixes the labor split for M1.

## 2. Machine state (verified 2026-07-21)

- **Apple Silicon**, macOS 26.5 (`uname -m` → `arm64`).
- Sandbox user is **`sandvault-matt`** (SandVault 1.23.0, `sv` / `sv-clone` on
  PATH via Homebrew). **This dev session already runs inside that sandbox** — a
  write to `/Library` returns `Operation not permitted` while a write into the
  workspace succeeds, and `sudo` is not even on the exec allowlist
  (`operation not permitted: sudo`). So the M0 smoke tests below *are* the
  SandVault verification; there is no separate "enter the sandbox" step.
- nix 2.34.7 present (daemon at `/nix/var/nix/profiles/default/bin/nix`).
- Git remote `git@github.com:mschulkind-oss/yolo-jail.git`; `~/.ssh` holds the
  sandbox user's own key material (host creds stay invisible, jail rule parity).

## 3. The recipe (what to run, in order)

Everything here is **zero-sudo** and runs inside the sandbox.

1. **Toolchain via mise** — Go/node/just are not global; the repo's `mise.toml`
   pins them (`go = "1.26"`). `go`, `container`, etc. are absent from a bare
   PATH.
   ```
   cd <repo>
   mise trust && mise install          # installs go 1.26, node 24, just, staticcheck
   eval "$(mise env bash)"             # puts go on PATH for this shell
   go version                          # → go1.26.2 darwin/arm64
   ```
   Every command below assumes `eval "$(mise env bash)"` has run in the shell.

2. **Short TMPDIR — required.** macOS's default `TMPDIR`
   (`/var/folders/kq/.../T/`) is long enough that `internal/broker`'s test unix
   sockets exceed the 104-char `sun_path` limit and `test-fast` fails with
   `bind: invalid argument` (a 106-char path was measured). Export a short one
   for **all** Go test / build / check commands:
   ```
   mkdir -p /tmp/yj-test
   export TMPDIR=/tmp/yj-test
   ```

3. **`just deploy`** — installs the host ship set (`yolo` only), writes
   `repo_path` into `~/.config/yolo-jail/config.jsonc`, and primes the Claude
   OAuth broker.
   ```
   just deploy
   yolo --version                      # → yolo-jail 0.7.1+…
   ```
   The Claude-broker step may print *"Broker failed to become live"* — that is a
   loophole daemon, **not** an M0 gate; ignore it for verification.

4. **Enable nix experimental features** (user-level, zero-sudo). The daemon is
   reachable but `nix-command`/`flakes` were off, so `nix store info` and
   `yolo check` reported "connection failed":
   ```
   mkdir -p ~/.config/nix
   printf 'experimental-features = nix-command flakes\n' > ~/.config/nix/nix.conf
   nix store info                      # → Store URL: daemon, Version: 2.34.7
   ```

## 4. Smoke-test results (the M0 pass signal)

| Check | Command | Result |
|---|---|---|
| Go build | `go build ./...` | **PASS** |
| Unit tests | `TMPDIR=/tmp/yj-test just test-fast` | **PASS** (green only with the short TMPDIR — see §3.2) |
| Darwin cross-build | `TMPDIR=/tmp/yj-test GOOS=darwin GOARCH=arm64 ./scripts/build-go.sh` | **PASS** (all 5 binaries) |
| nix daemon | `nix store info` | **PASS** — connects, `Trusted: 1` (after the §5.2 fix) |
| **Native aarch64-darwin build** | `YOLO_EXTRA_PACKAGES='["jq"]' nix build --impure --no-link --print-out-paths '.#packages.aarch64-darwin.yoloDarwinPackages'` | **PASS** — materializes a store path with a runnable `bin/jq` (see §5.4). This is the M1 §5 acceptance-bar build path (`darwinpkg.Materialize`), now de-risked from the sandbox. |
| macos-user dry-run | `YOLO_RUNTIME=macos-user yolo --dry-run -- bash` | **PASS** — prints full run plan + generated Seatbelt profile, zero sudo |
| Sandbox boundary | write `/Library` vs workspace; `sudo` | **PASS** — `/Library` + `sudo` blocked, workspace writable |

So the inside-sandbox column (build, test, cross-build, `yolo check --no-build`,
`--dry-run`, the zero-sudo AC-builder runbook) is **fully green**.

## 5. What SandVault / the environment blocks → the human (outside) column

`yolo check --no-build` ends at **2 failed, 1 warning**; these are the split:

1. **No container runtime** — `container` (Apple Container) is absent.
   Installable outside the sandbox with `brew install container`
   (formula `container` 1.1.0 is available) then `container system start`. Needed
   only for the AC/J3 path, **not** for macos-user with empty `packages:`.
2. **nix user not trusted (`Trusted: 0`)** — **RESOLVED 2026-07-21.** The
   sandbox user is `sandvault-matt`, not `matt`. The trap: `/etc/nix/nix.conf`
   has its **own** trailing `trusted-users = root matt` line *after* its two
   `!include /etc/nix/nix.custom.conf` lines, and last-assignment-wins in nix
   config — so editing `nix.custom.conf` alone had no effect. The fix (human,
   root, one-shot) was to add `sandvault-matt` to the line in **both** files and
   restart the daemon:
   ```
   sudo sed -i '' 's/^trusted-users = .*/trusted-users = root matt sandvault-matt/' /etc/nix/nix.custom.conf
   sudo sed -i '' 's/^trusted-users = .*/trusted-users = root matt sandvault-matt/' /etc/nix/nix.conf
   sudo launchctl kickstart -k system/org.nixos.nix-daemon
   ```
   `nix store info` then reports `Trusted: 1`.
3. **All of M1 §3–§7** — `yolo macos-setup` (dscl/ACL), the first real Seatbelt
   launch, teardown: every privileged one-shot. `sudo` is not on the SandVault
   exec allowlist at all (not merely password-gated), so these **cannot** run in
   the sandbox by design — they are the human's column, exactly as the plan
   predicts.

### 5.4 Native aarch64-darwin build — verified working (M1 §5 de-risked)

With `Trusted: 1`, the acceptance-bar build path runs **from the sandbox**:

```
YOLO_EXTRA_PACKAGES='["jq"]' nix build --impure --no-link --print-out-paths \
  '.#packages.aarch64-darwin.yoloDarwinPackages'
# → /nix/store/…-yolo-darwin-packages   (bin/jq present, runs: jq-1.8.2)
```

This is the exact build `darwinpkg.Materialize` drives for M1's `which jq` →
`/nix/store/…` acceptance bar, so the nix side of §5 is proven before the human
launch. **One sandbox-only snag:** nix's libgit2 rejected the flake with
*"repository path … is not owned by current user"* (repo is owned by `matt`,
sandbox runs as `sandvault-matt`). Fixed for the sandbox with
`git config --global --add safe.directory '/Users/Shared/sv-matt/repos/yolo-jail'`.
This will **not** affect M1: the human drives `yolo` as `matt` (the repo owner),
so libgit2 is satisfied there.

## 6. Handoff to M1

M0 is green: the sandboxed dev loop works, `repo_path` is set, nix is trusted,
the darwin cross-build + macos-user dry-run + native aarch64-darwin package
build all pass. Remaining human prerequisites before M1 §3:

- `brew install container && container system start` **iff** the AC/J3 path is
  exercised (macos-user with empty `packages:` needs no runtime).

The nix trusted-users fix (§5.2) is already done. Proceed to
[mac-macos-user-e2e.md](mac-macos-user-e2e.md) per the sequence in
[mac-agent-guide.md](mac-agent-guide.md).

## 6b. M1 results — macos-user e2e, observed on hardware (2026-07-21)

M1 ran per [mac-macos-user-e2e.md](mac-macos-user-e2e.md), `matt` driving the
privileged steps as the repo owner (never under sudo). **All PASSED.** The test
workspace was a second, up-to-date clone at `/Users/Shared/yolo/yolo-jail`
(under the provisioned neutral ground, so `_yolojail` can traverse it — a repo
under `/Users/Shared/sv-matt/…` is NOT reachable by the sandbox uid regardless
of the Seatbelt profile, since `/Users/Shared/sv-matt` is group `sandvault-matt`
0770).

| M1 behavior | Observation | Verdict |
|---|---|---|
| §3 setup / **finding-6 password** | `dscl . -read /Users/_yolojail` → `Password: ********`, `IsHidden: 1`, `UniqueID: 602` — a real ShadowHash, not empty | **PASS** |
| §4 first launch (behavior #2, Go bootstrap self-exec) | `whoami` → `_yolojail`, `pwd` → workspace, prints `yolo-jail macos-user bootstrap ok` (the `internal darwin-bootstrap` self-exec, no Python) | **PASS** |
| §4 **fresh-inode re-stage (behavior #1)** | 2nd identical run re-staged `/var/yolo-jail/yolo` and re-execed cleanly — **no `Killed: 9`/SIGKILL** from the Mach-O vnode signature cache | **PASS** |
| staged binary | `ls -l@ /var/yolo-jail/yolo` → `-rwxr-xr-x root wheel` (world r-x, root-owned); `com.apple.provenance` xattr present but binary runs fine → Gatekeeper/quarantine concern cleared | **PASS** |
| §5 **OQ-1 path_helper (behavior #4)** | `which just` → `/nix/store/…-yolo-darwin-packages/bin/just`; `$PATH` shows the login-rc re-prepend (`.yolo-shims`…`/nix/store/…/bin`) *ahead* of the path_helper tail (`/usr/local/bin`, `/opt/homebrew/bin`) | **PASS** — acceptance bar met |
| native aarch64-darwin build | `packages: ["just"]` materialized `just-1.57.0` from source (Cachix empty), streamed build logs | **PASS** |
| §6 host creds invisible | HOME is `_yolojail`'s scrubbed home; `~/.gitconfig` → *No such file*, `~/.ssh` → *No such file* — `matt`'s identity/keys not visible | **PASS** |
| §7 teardown idempotence | 1st `macos-teardown` removed the user; 2nd → *"does not exist — nothing to do"* (clean no-op); post-teardown run refuses with a clear "run macos-setup" message | **PASS** |

Two fixes landed from M1 observations (jail-side, committed):

- **`fix(darwinpkg): accept flake config …`** — the darwin materialize ran nix
  without `--accept-flake-config`, so the flake's own `yolo-jail.cachix.org`
  substituter was ignored (`ignoring untrusted flake configuration setting`
  warning every run) and packages always built from source. Adding the flag to
  `nixFlags()` silenced the warnings (confirmed gone on a `matt` rerun) and lets
  the cache be consulted once populated. Cache *hits* stay moot until a release
  or `just cachix-push` fills the cache.
- **`refactor(run): move resolveContainerCgroup to a linux-tagged file`** — a
  darwin-only staticcheck false positive (U1000 unused) that made
  `just check-ci` red for Mac developers while CI (Linux) was green; the callee
  was untagged but its only caller is linux-tagged.

**M1 is green → M2 (dogfood flip + docs) is unblocked.**

## 7. Gotchas worth carrying forward

- **`eval "$(mise env bash)"` in every shell** — `go`/`just`(pinned) aren't on a
  bare PATH; the justfile recipes shell out to `go` and fail without it.
- **`export TMPDIR=/tmp/yj-test`** for any Go test/build/check — the default
  macOS TMPDIR breaks `internal/broker`'s unix-socket tests (104-char limit).
  This is an environmental constraint, **not** a code regression.
- **`sv shell -- …` arg passing is awkward** (interactive zsh; `-c 'script'`
  after `--` did not reach a shell cleanly). For non-interactive smoke checks,
  just run commands directly — the session is already sandboxed.
- The **broker "failed to become live"** line from `just deploy` is orthogonal
  to M0; don't chase it as an M0 failure.
- **The M1 test workspace must live under the provisioned shared root**
  (`/Users/Shared/yolo/<name>`), not `/Users/Shared/sv-matt/…`. `macos-setup`
  gives `/Users/Shared/yolo` the setgid + inheriting `_yolojail` ACL; a repo
  under `sv-matt` sits in a `0770 sandvault-matt` dir that uid 602 can't
  traverse — the launch fails on unix perms *before* Seatbelt is even
  consulted. Keep a second clone under `/Users/Shared/yolo` for e2e runs.
- **Two `yolo` installs on the Mac.** `matt` had a stale pre-J2 Python-era
  `yolo` (v0.6.0) winning on PATH; its `--dry-run` emitted a Python
  `import entrypoint` bootstrap, not the Go `internal darwin-bootstrap` path.
  `just deploy` (which runs `migrate-host` to retire the old console-scripts)
  fixed it — verify with `yolo --version` (want ≥ 0.7.1) before trusting a
  dry-run.
