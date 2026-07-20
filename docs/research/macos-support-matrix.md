# macOS support matrix — every runtime × builder × config

> **NOTE:** The Go port is complete. This matrix remains the authoritative
> state-of-the-macOS-backend; keep it updated as the source of truth for
> what's built/proven/pending.

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
| **Container builder** | **Apple Container** | ✅ [M] | **PROVEN on real HW 2026-07-17** (macOS 26.5 arm64, AC 0.12.3, nix 2.34.7): AC pulled the GHCR image, ran it with internal-network IP `192.168.64.2:22`, host nix `store info` → `Trusted: 1`, proof build returned `AC-CONTAINER-BUILDER-WORKS`. No `-p` needed — AC's per-container VM IP is directly reachable. **Runbook → docs/guides/runbooks/mac-ac-container-builder.md.** |
| **QEMU `darwin.linux-builder`** | any container rt | 🔜 (roadmap/fallback) | standard nix tool; launchd daemon. Fallback if the container builder can't host on a given runtime. builder.py currently half-implements a worse version (detached Popen) — to be reworked. |
| nix-darwin `linux-builder` | any | ⬜ | user-side; only if they already run nix-darwin. Documented, not ours to install. |

## 3. Feature × runtime coverage (does each yolo capability work per runtime?)

| Capability | podman | Apple Container | macos-user |
|---|---|---|---|
| Run agent in jail | ✅ [L] | 🟡 [M] | 🟡 [L] |
| `packages:` (nix) | ✅ via image | 🟡 via image | 🟡 native darwin buildEnv [L] |
| Build when uncached | ✅ container builder [L] | ✅ container builder [M] | ⬜ (native, no build offload) |
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

**The single most important Mac test — ✅ DONE (2026-07-17), AC column unblocked:**
> Can Apple Container run `builderImage` with its sshd reachable by the host nix
> daemon over ssh-ng? **YES.** AC pulled the GHCR image directly (no OCI-convert
> needed), ran it with an internal-network IP, and host nix built through it
> (`Trusted: 1` → `AC-CONTAINER-BUILDER-WORKS`). AC is now a fully-supported
> container-builder path alongside podman. Next: wire the CLI orchestration
> (roadmap #3).

**Other Mac-only gates:** macos-user end-to-end (Seatbelt launch, real agent
run); the whole "run agent in jail" row for AC under current session's fixes.

## 5. Roadmap (ordered)

1. ✅ **[M] Prove the AC container builder** — DONE 2026-07-17 (real HW, see §4
   and the runbook). AC joins podman as a proven container-builder path.
2. **[M] macos-user end-to-end** on real hardware (Seatbelt launch + darwin nix
   build). Specifically the **path_helper login-shell PATH fix** (2026-07-17):
   the bootstrap now writes `.zprofile`/`.zshrc`/`.bash_profile` re-prepending
   the sandbox PATH after macOS path_helper, so `which jq` → `/nix/store/…/bin/jq`
   (not Homebrew's `/usr/local/bin/jq`). Cannot be tested on Linux; runbook
   `mac-macos-user-e2e` §5 is the gate.
3. 🔜 **Wire the container builder into the CLI run/check path** — regressed to
   roadmap (revival plan **J3**). It shipped in Python 2026-07-17
   (`container_builder.py`'s `builder_session`: pull → run → wait-reachable →
   yield nix `--builders` line → ephemeral teardown, threaded through
   `image.auto_load_image`), proven end-to-end against real podman + nix in-jail.
   **Go-port gap (2026-07-19):** the Go port never wired the on-demand container
   builder into its image path — there is no Go equivalent of the builder-session
   threading. `internal/containerbuilder` was a straight port of the session
   logic but had zero importers, so it was deleted (`b3477fb`); resurrect it from
   git history when the CLI run/check wiring lands (J3).
4. ✅ **Publish `builderImage` to GHCR** — DONE + LIVE + PUBLIC. The
   `push-builder-image` job ran on the v0.6.0 release and pushed
   `ghcr.io/mschulkind-oss/yolo-jail-builder:{0.6.0,latest}` (arm64/linux,
   verified: anonymous pull HTTP 200, sshd :22). Package was flipped Public
   once in GHCR settings — visibility is a persistent per-PACKAGE property, so
   every future release's tags inherit it; no per-release action needed. (The
   auto-PATCH step was removed: it 404'd on the one case that mattered — first
   creation, which the default token can't admin — and was a no-op otherwise.)
5. **Rework builder.py off the detached-Popen/`nix run` model** → either the
   container builder (primary) or a launchd plist for the QEMU fallback.
6. **Turn on Cachix** (deferred) — removes the builder entirely for cached images.
7. QEMU `darwin.linux-builder` as the documented fallback (roadmap).

## 6. Cross-refs
- **[runbooks/mac-ac-container-builder.md](../guides/runbooks/mac-ac-container-builder.md)** — Mac test (zero-sudo) for the gating AC-builder cell.
- **[runbooks/mac-macos-user-e2e.md](../guides/runbooks/mac-macos-user-e2e.md)** — Mac test (you-drive) for the macos-user backend.
- [macos-container-builder-exploration.md](macos-container-builder-exploration.md) — why container-builder, image sourcing, AC risk.
- [macos-linux-builder-explained.md](macos-linux-builder-explained.md) — the Linux-person's explainer of the whole builder question.
- [macos-no-vm-direction.md](../design/macos-no-vm-direction.md) — the "pursue both backends" decision.
- [macos-revival-and-distribution-plan.md](../plans/macos-revival-and-distribution-plan.md) — the current macos-user + distribution roadmap of record.
- [handoff-cachix-cache.md](../plans/handoff-cachix-cache.md) — the prebuilt-download happy path.
