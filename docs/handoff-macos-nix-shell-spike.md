# Handoff — Phase 0 spike for the no-VM macOS nix-shell backend

**For:** an agent/human on a **macOS arm64 (aarch64-darwin)** machine with Nix
installed and admin/sudo.
**Goal:** finish the Phase 0 GO/NO-GO spike for the nix-shell macOS backend
proposed in [macos-nix-shell-backend-proposal.md](macos-nix-shell-backend-proposal.md).
The Linux-checkable half is **already proven** (below); only the two genuinely
Mac-specific pieces remain. No `src/cli/` code should land until this spike
passes.

## What the backend is (one paragraph)

A macOS runtime that runs the agent **without a VM**: a dedicated unprivileged
macOS user + Seatbelt sandbox (the isolation, borrowed from SandVault), with
the agent's tools coming from a **native-darwin Nix devShell** generated from
`yolo-jail.jsonc` `packages:` and pinned by `flake.lock` — nothing installed on
the host, everything in the world-readable `/nix/store`. Architecture is
**"materialize outside, run inside"**: resolve the devShell on the trusted side
(the one nix-daemon step), then launch the agent under `sandbox-exec` with a
frozen PATH into the store. Settled decisions (materialization = devShell, keep
mise, allow per-platform `packages` overrides, Seatbelt+user isolation) are in
the proposal.

## Already PROVEN on Linux (you don't need to re-do these)

I ran the core loop end-to-end on the x86_64 Linux dev host. The mechanism is
arch-agnostic, so this de-risks everything except `sandbox-exec` + darwin
package availability. Verbatim results:

1. **Generated flake → devShell works.** A flake pinned to the repo's exact
   nixpkgs rev (`flake.lock`), turning `packages: ["ripgrep","jq"]` into
   `mkShell { packages = lib.attrVals names pkgs; }`, evaluates and builds.
2. **`print-dev-env` materializes + emits the env** (2145-line sourceable
   script). This is the single daemon-touching step.
3. **Frozen-PATH run, fully scrubbed, proven:**
   ```
   env -i PATH=<store paths> HOME=… bash -c 'rg --version; jq .'
   # rg 15.1.0 runs; jq runs; /usr/local NOT on PATH; tools resolve to
   # /nix/store/<hash>-ripgrep-15.1.0/bin/rg  (pinned, content-addressed)
   ```
   This is exactly what `sandbox-exec env -i PATH=… <agent>` will do inside
   the sandbox — no `nix` binary, no daemon, nothing from the host.
4. **darwin-availability validation logic works** (evaluated against
   `aarch64-darwin` *from the Linux host*): the per-package check
   `availableOn { system = "aarch64-darwin"; } p && (tryEval p.drvPath).success`
   correctly classified: `ripgrep`✓ `jq`✓ `util-linux`✓ (nixpkgs DOES ship a
   darwin build — "sounds Linux-only" ≠ "is Linux-only", trust the check not
   your gut) `strace`✗ (availableOn=false) `nonexistent-xyz`✗ (no such attr).
5. **Store perms confirm cross-user exec needs zero changes:** `/nix/store` is
   `drwxrwxr-t` (other=r-x), binaries `-r-xr-xr-x`. A separate macOS user can
   execute the whole toolchain unmodified.
6. **Warm `print-dev-env` = ~402 ms** on this host. Confirms the proposal's
   "cache the env dump" plan matters for instant repeat, but even uncached
   warm entry is sub-second.

The spike scaffold is at `/tmp/nixshell-spike/flake.nix` on the Linux host if
you want to see the exact generated flake; reproduce it on the Mac trivially
(steps below).

## What's LEFT — the Mac-only GO/NO-GO (your job)

Two things could not be tested off a Mac. If both pass, it's GO.

### Step A — the loop natively on aarch64-darwin (no VM)
Reproduce steps 1–3 above on the Mac and confirm the tools are darwin-native:
```sh
mkdir -p /tmp/nixshell-spike && cd /tmp/nixshell-spike
REV=$(python3 -c "import json;print(json.load(open('$OLDPWD/flake.lock'))['nodes']['nixpkgs']['locked']['rev'])")   # or paste the rev
cat > flake.nix <<EOF
{ inputs.nixpkgs.url = "github:nixos/nixpkgs/\$REV";
  outputs = { self, nixpkgs }:
    let system = builtins.currentSystem;
        pkgs = import nixpkgs { inherit system; };
        names = [ "ripgrep" "jq" ];
    in { devShells.\${system}.default = pkgs.mkShell { packages = pkgs.lib.attrVals names pkgs; }; };
}
EOF
SYS=$(nix eval --impure --raw --expr 'builtins.currentSystem')   # -> aarch64-darwin
nix print-dev-env --impure ".#devShells.$SYS.default" > devenv.sh
FROZEN_PATH=$(bash -c 'source ./devenv.sh >/dev/null 2>&1; echo "$PATH"')
env -i PATH="$FROZEN_PATH" HOME=/tmp/fh bash -c 'rg --version; echo "{}" | jq .; file $(command -v rg)'
```
**Confirm:** `rg`/`jq` run; `file` shows a **Mach-O arm64** binary (darwin-native,
no VM); the store paths are under `/nix/store`. Note warm `print-dev-env` time.

### Step B — run it under a Seatbelt sandbox as a separate user
This is the real isolation test — the part that could not be touched on Linux.
1. Create (or reuse) an unprivileged sandbox user (`_yolojail`), per the
   settled model. The excised `macos-user` code (git tag
   **`macos-user-experiment`**) has ready-to-reuse builders:
   `create_user_commands`, `seatbelt_profile`, the neutral-`/Users/Shared`
   workspace + launch argv. **Reuse the sandbox half; the package side is now
   the nix devShell instead of nothing.**
2. Write a minimal Seatbelt profile: `(deny default)` + `(import "system.sb")`,
   `(allow process-exec)(allow process-fork)`, `(allow file-read* process-exec
   (subpath "/nix/store"))`, read on `/usr` `/System`, writable workspace +
   `$TMPDIR` + `/dev/null`. Use a **coarse `(subpath "/nix/store")`** — do not
   enumerate per-path (blows the profile-size ceiling). Canonicalize paths
   (`/private/tmp` vs `/tmp`, `/private/var` vs `/var`).
3. Launch: `sudo -u _yolojail /usr/bin/env -i PATH="$FROZEN_PATH" HOME=…
   /usr/bin/sandbox-exec -f agent.sb -- rg --version` (then a real agent).
4. **Confirm:** the sandbox user runs the store tools (world-readable store →
   should "just work"); it can read/write the workspace but **cannot** read
   your `~/.ssh`/creds; and the whole thing is **native processes, no VM** —
   check Activity Monitor shows no VM, instant start.

### GO / NO-GO
- **GO** if: darwin-native tools run under the frozen PATH (A) AND run under
  Seatbelt as the separate user with the credential boundary holding (B).
- **NO-GO / rethink** if: `sandbox-exec` can't traverse the store symlink/path
  chain even with the coarse subpath rule, or darwin package coverage for a
  realistic `packages:` list is too thin to be useful.

## Known risks to watch (from research)
- **`sandbox-exec` path matching** is literal — the nix profile/store symlink
  chain must be fully allowed, and `/private/*` canonicalization bites. This is
  the most likely thing to fight in Step B.
- **`sandbox-exec` is deprecated** (still used by Nix, Chrome, Codex, Claude
  Code) — works today, long-term risk.
- **Nested sandboxing fails** — tools that self-sandbox (`swift build`,
  `xcodebuild`) need `--disable-sandbox` since the agent already runs under one.
- **darwin cache coverage** is Tier-2 (~95%+ of darwin-*targeted* pkgs, but a
  larger slice of the whole tree is Linux-only) — a realistic `packages:` list
  may hit gaps; that's what the per-platform override + aggregated error handle.

## After GO — implementation order (from the proposal)
Phase 1 package layer (`packages:` → generated flake → materialized env +
darwin validation + env-dump cache + gcroot) → Phase 2 sandbox layer (revive
the tagged account/Seatbelt/neutral-workspace code, wire the frozen PATH, run
the entrypoint `CONFIG_WRITERS` natively) → Phase 3 rest of the config surface
(keep mise as-is; mcp/lsp/blocked-tools/env_sources) + honest docs on the
network/resource-cap gaps.

## Pointers
- Proposal + settled decisions: [macos-nix-shell-backend-proposal.md](macos-nix-shell-backend-proposal.md)
- Why (the "no VM, not no emulation" reframing): [macos-no-vm-direction.md](macos-no-vm-direction.md)
- Reusable sandbox code: git tag `macos-user-experiment`
- Repo's pinned nixpkgs rev: `flake.lock` (`nodes.nixpkgs.locked.rev`)
