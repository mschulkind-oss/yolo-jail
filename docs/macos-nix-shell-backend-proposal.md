# Proposal: a no-VM macOS backend built on native Nix devShells

**Status:** PROPOSAL for sign-off — not built. Supersedes the excised
`macos-user` approach and answers the goal set in
[macos-no-vm-direction.md](macos-no-vm-direction.md).
**Date:** 2026-07-14
**Reads with:** [macos-no-vm-direction.md](macos-no-vm-direction.md) (why),
[macos-backend-direction.md](macos-backend-direction.md) (the excision),
[happy-path-principle.md](happy-path-principle.md).

## The goal (from the maintainer, verbatim intent)

> A macOS user should be able to spin up something **consistent** and
> **dep-pinned** *without installing stuff on their system* — **fast and
> convenient**, no guessing RAM ahead of time, no permanently-stolen RAM, no
> VM problems. On Linux it takes two seconds and just works; on macOS today
> it's bad enough to reach for another tool.

Explicitly **not** required: byte-identical-to-Linux tooling. "Consistent
across Macs + pinned + nothing installed system-wide" is the bar.

And the guardrail: **it must still be yolo, not a SandVault clone.** We borrow
SandVault's isolation *mechanism* but keep yolo's nix-pinned, declarative,
per-workspace identity — otherwise there's no reason to exist over SandVault.

## The key research finding that makes this work

Everything hinges on one fact, now confirmed: **`/nix/store` is world-readable
by default** (dir `1775`, contents `0444`/`0555`). A separate, unprivileged
macOS user can **execute the entire Nix-provided toolchain with zero
permission changes.** Combined with:

- **`nix develop` / devShells** put a pinned set of packages on `PATH` from
  `/nix/store` only — nothing in `~`, `/usr/local`, or the system. Native on
  `aarch64-darwin`, no VM.
- **`nix print-dev-env`** materializes that devShell's closure into the store
  and emits the resolved env (PATH + vars) as a sourceable script — the *one*
  step that needs the nix daemon.
- **`sandbox-exec`** (Seatbelt) can then run the agent with a **frozen PATH**
  into the store, no `nix` binary or daemon socket needed inside the sandbox.

That yields the architecture below: **materialize outside, run inside.**

## Architecture: "materialize outside, run inside"

```
  ┌─ HOST side (your normal user, has nix-daemon access) ──────────────┐
  │ 1. Generate a per-workspace flake from yolo-jail.jsonc `packages:`  │
  │    against the SAME locked nixpkgs (flake.lock) yolo already pins.  │
  │ 2. Validate each package builds on aarch64-darwin; report any that  │
  │    don't (aggregated, actionable).                                  │
  │ 3. `nix print-dev-env <genflake>#devShell` → realize closure into   │
  │    /nix/store, capture PATH + env vars.  [only daemon-touching step]│
  └────────────────────────────────────────────────────────────────────┘
                    │ frozen PATH into /nix/store + env
                    ▼
  ┌─ SANDBOX side (dedicated _yolojail user + Seatbelt, NO nix, NO VM) ─┐
  │ sudo -u _yolojail env -i HOME=… PATH=<store paths> <env> \          │
  │   sandbox-exec -f profile.sb -- <agent> ...                         │
  │   • runs natively as arm64 macОS processes — sub-process-launch     │
  │     fast, no VM boot, no RAM ceiling, no RAM permanently held       │
  │   • reads tools from world-readable /nix/store                      │
  │   • writes only to the neutral shared workspace + its own home      │
  └────────────────────────────────────────────────────────────────────┘
```

**What this delivers against the goal, point by point:**

| Goal | How this meets it |
|---|---|
| Consistent, dep-pinned | Packages resolve against the committed `flake.lock` → same versions on every Mac (same nixpkgs rev → same darwin store paths, fetched from cache). |
| Nothing installed on the system | Tools live only in `/nix/store`; PATH-injected for the agent process. Nothing in `~`, `/usr/local`, no `brew install`. (Nix itself is the one prerequisite — already required by yolo.) |
| Fast | No VM boot. Cost is `print-dev-env` eval (sub-second–seconds warm) done once on the host side + a process launch. Cache the env dump so repeat runs are near-instant (see Speed). |
| No guessing RAM / no permanent steal / no VM | There is **no VM at all.** The agent is ordinary macOS processes; memory is normal process memory, returned on exit. |
| Still yolo, not SandVault | It carries `packages:` (the nix-pinned layer SandVault has no equivalent of) **and** the per-workspace config surface (below). The isolation is the only thing borrowed from SandVault. |

## Why this fixes what `macos-user` got wrong

The excised `macos-user` failed the "still yolo" bar for one concrete reason:
**it never consumed `packages:`** — it generated mise config and shims but had
no nix layer, so it was SandVault with extra steps. This proposal's **entire
center is the nix devShell**: the pinned package set IS the backend's reason
to exist. That's the difference between "a yolo backend" and "an SV clone,"
and it's the non-negotiable acceptance bar for building this.

## Keeping the rest of the yolo config surface

The other yolo config keys map to the native context (the entrypoint already
runs library-style outside a container — proven by `_entrypoint_preflight`):

- **`packages:`** → the generated devShell (the core; above).
- **`mise_tools:`** → either fold into the devShell (nix-provided) or run mise
  in the sandbox as today; decide during build.
- **`mcp_servers` / `lsp_servers` / agent configs** → the entrypoint's
  `CONFIG_WRITERS` run natively, writing into the sandbox user's home (this
  part `macos-user` already did correctly).
- **`security.blocked_tools`** → the shims already work by PATH ordering; same
  mechanism, PATH points at shim dir first.
- **`env_sources`** → resolved and injected into the sandbox launch env.
- **workspace** → neutral shared ground (`/Users/Shared/yolo/<project>`),
  reusing the already-designed non-home model + inheriting-ACL sharing.
- **network / ports** → **not applicable natively** (no container network
  namespace); the agent uses the host network directly. Document this as an
  inherent difference, not a bug.
- **resources (CPU/mem caps)** → **not enforced** natively (no cgroups). A
  documented gap; `taskpolicy`/`ulimit` are weak partial options, defer.

## Honest limitations (name them, don't paper over)

1. **Weaker isolation than a VM.** Seatbelt + separate user, shared kernel — a
   kernel exploit escapes. Same tradeoff we documented for `macos-user`. The
   container backend stays available for anyone who wants the VM boundary.
2. **Darwin package availability.** aarch64-darwin is a Tier-2 Hydra platform:
   ~95%+ of *darwin-targeted* packages cache, but a meaningfully larger slice
   of the *whole* tree is Linux-only. A `packages:` list written for the Linux
   image may include entries with no mac build (or Linux-only transitive
   deps). **Mitigation:** validate with `lib.meta.availableOn` + a
   `builtins.tryEval` on `drvPath` (catches `meta.broken = isDarwin`), and
   emit one aggregated "unavailable on macOS" error — never silently skip.
   Possibly support a per-platform `packages` override in config.
3. **Not byte-identical to Linux** — declaratively identical (same nixpkgs
   attrs, darwin builds). The maintainer has accepted this.
4. **`sandbox-exec` is deprecated** (still used by Nix, Chrome, Bazel, Codex,
   Claude Code; no official replacement). Long-term risk, not a today-blocker.
5. **No network/resource-limit config** (above). Inherent to no-container.
6. **Nix is a hard prerequisite.** Already true for yolo on macOS.

## Speed plan (so it actually feels like "two seconds")

Raw `nix develop` re-evaluates every entry (hundreds of ms–seconds, worse on
a dirty tree). To hit "convenient":

- **Materialize once, cache the env dump.** `print-dev-env` output (PATH +
  vars) is captured to a per-workspace file and reused until `flake.lock` or
  the resolved `packages:` change — the nix-direnv `use flake` pattern, done
  ourselves. Warm launch = read a cached env + `sandbox-exec`, ~instant.
- **Keep a gcroot** on the materialized closure so `nix-collect-garbage`
  doesn't force a cold re-download.
- **Pin the eval to `HEAD`** (not the dirty working tree) so the flake
  eval-cache isn't invalidated by every workspace edit.

## Where Apple Container lands (the Option-A comparison)

We also researched whether Apple Container already makes the VM painless
enough to not need this. Verdict: **it genuinely improves 2 of 3 VM pains but
does not eliminate the VM.**

- **Guess RAM ahead:** better — `--memory` is a lazily-backed *ceiling* per
  container, not an up-front shared reservation; idle containers stay small.
- **Slow VM / disk:** better — sub-second start (vs Podman Machine's ~30s), a
  per-container micro-VM, no one giant shared VM.
- **Permanently steal RAM:** **not fixed** — Apple Container rides
  Virtualization.framework's non-working memory balloon (verified against
  Apple's own Swift source: no balloon is configured). A long-running
  container ratchets to a high-water mark and only fully releases on stop.
  (OrbStack is the only runtime that reclaims live.)

So Apple Container is a real, cheap **near-term** improvement — worth making
the default macOS runtime regardless — but it is **still a VM** with startup,
memory-ratchet, and virtiofs-slow-bind-mount overhead. It does not deliver the
"native, no-VM, Linux-fast" experience the goal asks for. The nix-shell
backend does.

## Recommendation

**Two tracks, not either/or:**

1. **Now, cheap:** confirm Apple Container is the macOS default and tune it
   (its per-container demand-paged memory already softens the worst pains).
   This helps every macOS user immediately, no new backend.
2. **The real answer:** build the **nix-shell backend** (this proposal) as the
   opt-in "fast, no-VM, nothing-installed" path — with the **hard acceptance
   bar that it consumes `packages:` via native darwin nix from day one.** If a
   prototype can't carry the nix layer, stop — that's the line we already
   crossed the wrong way once.

`macos-user` is recoverable (tag `macos-user-experiment`); its account +
Seatbelt + neutral-workspace machinery is directly reusable as the *sandbox
side* of this architecture — only the *package side* changes (nix devShell
instead of nothing). So this is less "build from scratch" than "revive the
sandbox half + add the nix half that was always missing."

## Phase plan (if approved)

- **Phase 0 — spike (on a Mac).** Prove the core loop by hand: generate a
  devShell from a 3-package list against the locked nixpkgs; `print-dev-env`;
  `sudo -u _yolojail sandbox-exec -f p.sb env -i PATH=… claude` runs and sees
  the tools. Confirm world-readable store works cross-user. GO/NO-GO gate.
- **Phase 1 — package layer.** `packages:` → generated flake → materialized
  env; darwin-availability validation + aggregated errors; env-dump cache +
  gcroot.
- **Phase 2 — sandbox layer.** Revive the `macos-user` account/Seatbelt/
  neutral-workspace code; wire the frozen PATH in; run the entrypoint
  `CONFIG_WRITERS` natively.
- **Phase 3 — the rest of the config surface** (mise, mcp/lsp, blocked-tools,
  env_sources) + honest docs on the network/resource gaps.

## Open questions

1. **devShell vs `nix profile --profile <ws>`** for materialization? devShell
   is declarative + gives env vars/hooks; profile gives a stable on-disk
   `bin/` and gcroot but is imperative and version-drift-prone. Leaning
   devShell + cached `print-dev-env`. Decide in Phase 1.
2. **`mise_tools` — nixify or keep mise?** If the devShell can provide the
   tools, mise may be redundant on this backend.
3. **Per-platform `packages` override** in `yolo-jail.jsonc` for the
   Linux-only-package case, or just error and let the user fix it?
4. **Is Seatbelt-grade isolation acceptable** for this backend given the
   container/VM remains for stronger needs? (Same question the excised backend
   raised; still the maintainer's call.)
