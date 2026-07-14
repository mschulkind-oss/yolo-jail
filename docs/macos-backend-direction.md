# Decision: the macOS backend direction — is `macos-user` worth keeping?

**Status:** DECISION PENDING — written to get agreement with full context
before any code moves. Nothing has been excised or committed as a result of
this doc yet.
**Date:** 2026-07-14
**Author:** drafted for review by the maintainer.
**Reads with:** [macos.md](macos.md),
[macos-native-user-sandbox-design.md](macos-native-user-sandbox-design.md),
[platform-comparison.md](platform-comparison.md).

## Why this doc exists

We built `runtime: "macos-user"` — a native-macOS backend (dedicated user +
Seatbelt, modeled on [SandVault](https://github.com/webcoyote/sandvault)) — to
avoid emulation overhead on Apple Silicon. A series of hands-on questions then
surfaced a worry, in the maintainer's words:

> "I'm worried we bolted on an SV clone that does everything SV does, and
> nothing yolo does."

This doc lays out whether that's true, what the *actual* goal was, and the
options — so we decide deliberately instead of continuing to patch.

## The goal, restated

The systems being run, in priority order:

1. **mac/arm** — nice to have.
2. **linux/intel** — primary.
3. **linux/arm** — lightly tested; "a little annoying, but not much." A
   reasonable near-term goal to firm up.

Hard constraint from the maintainer:

> "mac/arm would be nice, but if we lose all of the container and nix and
> everything, then I don't want it."

That constraint is the crux — and `macos-user` fails it (see below).

## The fact that changes everything: the container is ALREADY native arm64

The original driver for `macos-user` was **avoiding emulation** on Apple
Silicon. That premise is false for the container backend:

- `flake.nix:43` maps `aarch64-darwin → aarch64-linux`. On an M-series Mac the
  image is built as **`aarch64-linux`**, and Podman Machine / Apple Container
  run a **`linux/arm64`** VM. That's **arm-on-arm — no qemu, no Rosetta.**
- The only emulation you ever hit is pulling an **amd64-only image** (e.g. SQL
  Server, Oracle XE). That is a property of *that image*, not of the backend —
  and **no native backend fixes it**: an x86 database binary emulates on arm64
  regardless of how you launch it.

**So the emulation problem `macos-user` was created to solve does not exist on
the container path.** On mac/arm, `runtime: container` (Apple Container) or
`runtime: podman` already gives native arm64 speed, the full nix image, and
every yolo feature.

## What `macos-user` actually delivers vs. costs (measured, not guessed)

### It delivers
- **No Linux VM at all** — saves the VM's RAM/disk footprint and a few seconds
  of machine startup. Real, but modest.
- **macOS/arm-native *tooling*** — the agent runs actual macOS binaries. This
  only matters for running/testing **macOS-native** software (Xcode, Swift,
  codesigning, `.app` bundles). For the Linux-targeted dev this project is
  built around, it's not a benefit — it's the *opposite* of the reproducible
  Linux target.
- **No emulation** — but this is **not** a differentiator; the container
  already has it on arm64.

### It costs (confirmed by reading the code)
`macos-user`'s run path consumes only **`agents`, `macos_log`, and git
identity** from `yolo-jail.jsonc`. It **silently ignores** the rest of what
makes a yolo jail a yolo jail:

| `yolo-jail.jsonc` capability | container backend | `macos-user` today |
|---|---|---|
| `packages` (nix deps in the image) | ✅ built into the image | ❌ **no-op** — no nix layer at all |
| `network` / `ports` / `forward_host_ports` | ✅ | ❌ **no-op** |
| `mise_tools` | ✅ | ⚠️ only what the bootstrap installs into the shared user home |
| `mcp_servers` / `lsp_servers` | ✅ | ❌ **no-op** |
| `security.blocked_tools` | ✅ | ❌ **no-op** |
| Per-workspace state isolation | ✅ per-workspace `.yolo/home` overlays | ❌ **one shared `_yolojail` user for ALL workspaces** |
| Host `~/.claude/settings.json` sync | ✅ three-way merge | ❌ **not wired** (open bug) |
| Reproducible Linux target | ✅ (the whole point of the nix image) | ❌ it's macOS, by definition |
| Resource caps (CPU/mem/PID) | ✅ (Apple Container) | ❌ none |
| Boundary strength | VM/kernel boundary | weaker: shared kernel, deprecated `sandbox-exec` |

The maintainer's worry is essentially confirmed: `macos-user` reproduces
**SandVault's** model (dedicated user + Seatbelt + one shared home + a shared
`/Users/Shared` workspace) and **drops most of what yolo does** (the nix image,
per-workspace config, packages, networking, MCP/LSP, isolation). It is closer
to "SandVault in the yolo CLI" than to "a yolo backend."

And it directly violates the hard constraint: **you lose the container and nix
and everything.** So by the maintainer's own line — "then I don't want it."

## Options

### A. Excise `macos-user`; make "container on arm64" the macOS answer *(recommended)*
Remove the native backend and its config surface; document that on mac/arm the
container backend is already native (no emulation), so nothing of value is
lost. Re-focus on the three real targets (linux/intel primary, linux/arm
firming up, mac/arm via container).

- **Pro:** removes ~2,100 LOC of backend + ~64 integration refs across 7 shared
  modules that will otherwise keep accreting patches and questions. Honors the
  hard constraint. One coherent story: yolo = nix image in a native-arch
  container, on all three platforms.
- **Con:** loses the (unbuilt-out) macOS-native-tooling capability; throws away
  genuinely good security work (the Seatbelt/ACL model, the design docs).
- **Mitigation:** the work isn't deleted from history — it's tagged/branched
  before removal, so a future macOS-native-tooling effort can resurrect it with
  eyes open. The SandVault-parity design docs stay as reference.

### B. Keep `macos-user`, narrow charter, freeze scope
Stop pursuing parity. Document it as "for macOS-native tooling / no-VM only;
ignores packages/networking/ports/mcp/lsp; one shared sandbox user." Fix
nothing further unless someone adopts it for that narrow purpose.

- **Pro:** keeps the option alive at near-zero ongoing cost; no removal churn.
- **Con:** it still violates "don't lose container+nix," so it's a trap for
  anyone who reaches for it expecting a yolo jail; carrying dead-ish weight and
  the integration refs in shared modules.

### C. Keep `macos-user`, close the gaps to real parity
Build `packages` (nix profiles in the sandbox home), networking/ports,
mcp/lsp/mise, and per-workspace isolation into the native path.

- **Pro:** would make it a "real" backend.
- **Con:** large effort re-implementing, worse, what the container already
  does — to reach a backend whose founding rationale (no emulation) is already
  met elsewhere, and that is still a weaker boundary. **Not recommended.**

## Recommendation

**Option A — excise it, and lead the macOS story with "container is native
arm64."** The emulation goal is already met by the container; `macos-user`
buys only the no-VM footprint + macOS-native tooling (which isn't a stated
need), at the cost of dropping the nix image, per-workspace config, and
isolation — exactly the "lose everything yolo does" outcome the maintainer
ruled out.

**The one thing that would change this recommendation:** a concrete need to run
**macOS-native software** in the sandbox (Xcode/Swift/codesigning/`.app`
testing). If that's a real use case, `macos-user` has a genuine purpose and
Option B (narrow, honest charter) is right instead. If it's not, Option A.

## If we go with A — the excision plan (for a later, approved change)

Not done in this doc; recorded so the scope is agreed.

1. **Tag first:** `git tag macos-user-experiment` at the current commit so the
   work is recoverable, then remove.
2. **Delete:** `src/cli/macos_user.py`, `tests/test_macos_user.py`.
3. **Unwind the runtime seam:** `paths.py` (`NATIVE_RUNTIMES`/`ALL_RUNTIMES` →
   just `SUPPORTED_RUNTIMES`), `runtime.py` (`_native_runtime_check`, the
   `macos-user` branches), `config.py` (`macos-user` runtime value,
   `macos_log`, `macos_shared_root` keys + validation), `run_cmd.py` (the
   `run_macos_user` dispatch + `--dry-run` plumbing that's macos-user-only),
   `check_cmd.py` (`_check_macos_user_backend` + the `is_native_runtime`
   gating), `__init__.py` (`macos-setup`/`teardown`/`unshare`/`fix-permissions`
   commands), `config_ref_cmd.py` (the doc entries).
4. **Docs:** remove/retire the four `macos-*user*`/`macos-native*` docs (or
   collapse to a single "why we tried and removed it" note pointing at the
   tag); rewrite `macos.md` around "container = native arm64, no emulation";
   drop the macos-user rows from `platform-comparison.md`.
5. **Verify:** full suite green; `yolo check` + a real run on linux/intel and a
   nested jail unaffected.

## Open questions to settle before excising

1. **Is there a real macOS-native-software use case?** (The only thing that
   justifies keeping it.) — needs the maintainer's answer.
2. **Firm up linux/arm** instead — is that where the freed effort goes? It's
   the "lightly tested, a little annoying" target and would benefit the
   primary matrix.
3. **The amd64-image emulation caveat** (SQL Server/Oracle for the DB work) is
   independent of this decision and stays either way — document it wherever DB
   support lands.
