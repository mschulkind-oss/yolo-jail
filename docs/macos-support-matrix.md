# macOS support matrix — every runtime × builder × config

**Purpose:** track macOS coverage across the whole cross-product so nothing
ships half-done. yolo's rule (happy-path-principle.md) is "fill the matrix, one
path per cell." This is that matrix, with honest status. Update it as cells go
green.

**Legend:** ✅ works + verified · 🟡 built, unverified on real HW · 🔜 designed,
not built · ⬜ N/A / not applicable · ❌ known-broken/blocked.

Verification tiers: **[L]** verified on Linux (unit tests / this jail's real
nix+podman) · **[M]** needs a real Mac · **[CI]** covered by CI.

---

## 1. The three macOS runtimes (where the agent runs)

| Runtime | Kind | Status | Notes |
|---|---|---|---|
| **podman** | container in one shared podman-machine VM | ✅ [L/CI] | first-class; the reference macOS container path |
| **Apple Container (AC)** | one lightweight VM per container | 🟡 [M] | default macOS runtime; early-stage — hit limits this session (bind-mount cap, `:ro` ignored, no `--net=host`, OCI convert) |
| **macos-user** | native macOS user + Seatbelt, NO VM | 🟡 [L] | built + unit-tested; `packages:` via native darwin nix (buildEnv); **never tested on real macOS** |

## 2. Builder (how the Linux image / packages get built) — CONTAINER RUNTIMES ONLY

macos-user needs **no builder** (native darwin nix, no Linux image). The builder
question exists only for podman/AC.

| Builder | Runtime | Status | Notes |
|---|---|---|---|
| **Cachix / prebuilt download** | any | 🔜 | THE happy path — wired, account deferred (handoff-cachix-cache.md). No build → no builder needed. |
| **Container builder** (nix+sshd container on the runtime) | **podman** | ✅ [L] | **proven end-to-end in-jail**: image built, `ssh-ng` build ran inside container, result read back. `packages.builderImage` in flake. |
| **Container builder** | **Apple Container** | 🟡 [M] | image PUBLISHED + public on GHCR (arm64, verified); mechanism identical to podman (proven). UNVERIFIED that AC can pull/run it + expose a reachable sshd port to the host nix daemon. **The gating Mac test → docs/runbooks/mac-ac-container-builder.md.** |
| **QEMU `darwin.linux-builder`** | any container rt | 🔜 (roadmap/fallback) | standard nix tool; launchd daemon. Fallback if the container builder can't host on a given runtime. builder.py currently half-implements a worse version (detached Popen) — to be reworked. |
| nix-darwin `linux-builder` | any | ⬜ | user-side; only if they already run nix-darwin. Documented, not ours to install. |

## 3. Feature × runtime coverage (does each yolo capability work per runtime?)

| Capability | podman | Apple Container | macos-user |
|---|---|---|---|
| Run agent in jail | ✅ [L] | 🟡 [M] | 🟡 [L] |
| `packages:` (nix) | ✅ via image | 🟡 via image | 🟡 native darwin buildEnv [L] |
| Build when uncached | ✅ container builder [L] | 🟡 container builder [M] | ⬜ (native, no build offload) |
| `/ctx/` mounts read-only | ✅ (`:ro` honored) | ❌ AC ignores `:ro` → **skipped w/ warning** [L] | 🟡 Seatbelt subpath deny [L] |
| `workspace_readonly` | ✅ | ❌ not enforced → **warns** [L] | 🟡 Seatbelt [L] |
| `env_sources` (API keys) | ✅ | ✅ | ✅ [L] (fixed this session) |
| `security.blocked_tools` | ✅ | ✅ | ✅ [L] (baked into bootstrap) |
| `mise_tools`/`mcp`/`lsp` | ✅ | ✅ | ✅ [L] (baked into bootstrap) |
| bridge networking (nested podman) | ✅ [L] | ⬜ (AC networks internally) | ⬜ (no container) |
| GPU passthrough | ⬜ macOS has none | ⬜ | ⬜ |
| Resource limits | via machine VM | ✅ native `--cpus/--memory` | ❌ Seatbelt has no cgroups |
| Isolation strength | VM | VM (per-container) | Seatbelt (weaker; documented) |

## 4. What's PROVEN vs. what's the next gate

**Proven on Linux/podman (this session):**
- Container builder image builds from the flake (`packages.builderImage`), and a
  real `ssh-ng` build runs inside it and returns its result. [L]
- macos-user native darwin package layer: buildEnv materialization yields exactly
  the declared packages (verified with real nix). [L]
- All the wiring/config-surface for macos-user (runtime select, dispatch, check
  gating, env_sources, blocked_tools, mcp/lsp). [L] + 1481 unit tests.

**The single most important Mac test (unblocks the AC column):**
> Can Apple Container OCI-convert `builderImage` and run it with its sshd port
> reachable by the host nix daemon over ssh-ng? If yes, the AC container-builder
> cell goes ✅ and AC becomes a fully-supported build path. If no, AC falls back
> to QEMU `darwin.linux-builder`.

**Other Mac-only gates:** macos-user end-to-end (Seatbelt launch, real agent
run); the whole "run agent in jail" row for AC under current session's fixes.

## 5. Roadmap (ordered)

1. **[M] Prove the AC container builder** — the gating test above. (podman side
   already ✅.)
2. **[M] macos-user end-to-end** on real hardware (Seatbelt + darwin nix build).
3. Wire the container builder into the CLI run/check path (currently only the
   image + the ssh remote-builder setup exist; the "start builder container +
   publish port + point nix at it" orchestration is the next code step — reuses
   builder.py's nix.conf/ssh/trusted-users wiring).
4. ✅ **Publish `builderImage` to GHCR** — DONE + LIVE + PUBLIC. The
   `push-builder-image` job ran on the v0.6.0 release and pushed
   `ghcr.io/mschulkind-oss/yolo-jail-builder:{0.6.0,latest}` (arm64/linux,
   verified: anonymous pull HTTP 200, sshd :22). The auto-visibility PATCH
   404'd (GITHUB_TOKEN lacks org-package-admin) → flipped public MANUALLY in
   GHCR settings. TODO: make the visibility step reliable (a PAT with
   `packages` scope, or accept the one-time manual flip per new package).
5. **Rework builder.py off the detached-Popen/`nix run` model** → either the
   container builder (primary) or a launchd plist for the QEMU fallback.
6. **Turn on Cachix** (deferred) — removes the builder entirely for cached images.
7. QEMU `darwin.linux-builder` as the documented fallback (roadmap).

## 6. Cross-refs
- **[runbooks/mac-ac-container-builder.md](runbooks/mac-ac-container-builder.md)** — Mac test (zero-sudo) for the gating AC-builder cell.
- **[runbooks/mac-macos-user-e2e.md](runbooks/mac-macos-user-e2e.md)** — Mac test (you-drive) for the macos-user backend.
- [macos-container-builder-exploration.md](macos-container-builder-exploration.md) — why container-builder, image sourcing, AC risk.
- [macos-linux-builder-explained.md](macos-linux-builder-explained.md) — the Linux-person's explainer of the whole builder question.
- [macos-no-vm-direction.md](macos-no-vm-direction.md) — the "pursue both backends" decision.
- [handoff-macos-user-revive-plan.md](handoff-macos-user-revive-plan.md) — the macos-user implementation.
- [handoff-cachix-cache.md](handoff-cachix-cache.md) — the prebuilt-download happy path.
