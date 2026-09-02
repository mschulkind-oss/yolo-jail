# macOS support matrix — every runtime × builder × config

**Status:** LIVE TRACKER — reconciled against the tree **2026-08-23**. Cells
carry the date they were last checked; an undated cell is from the 2026-07 era
and has not been re-measured. Written from a Linux jail: **no cell here was
verified on a Mac by this pass** — [M] cells restate what a Mac session
recorded, with its date.

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

> [!WARNING]
> **A ✅ in this matrix is a dated measurement, not a standing guarantee.** Two
> of the 2026-07 ✅ cells describe a build the maintainer's Mac no longer runs:
> at 2026-08-19 that machine's installed `yolo` was **531 commits stale** and its
> config still used the **removed `agents` key**, so no current `yolo` launches
> there at all. Re-read every [M] cell as *"true of the build tested on the date
> shown."*

---

## 0. The platform deadline — `x86_64-darwin` is on a clock

**This is the most important row in the document, because it has an expiry date
and nothing else here does.** Verified against `flake.nix` 2026-08-23.

- **nixpkgs 26.11 has DROPPED `x86_64-darwin`.** It does not deprecate it; it
  **throws** — `Nixpkgs 26.11 has dropped support for x86_64-darwin`. Because
  the flake's `pkgs` is evaluated for every system `flake-utils` enumerates,
  that throw took out *every* host-side nix call on an Intel Mac, `nix eval
  .#installPrefix` included — which is what the integration suite's staleness
  oracle runs.
- **26.05 is the LAST branch supporting it**, and is security-fixed only to the
  **end of 2026**. The flake pins it for that one system and deliberately not
  for `aarch64-darwin`, so real Mac users stay on 26.11:
  `nixpkgs-x86-darwin.url = "github:nixos/nixpkgs/nixpkgs-26.05-darwin"`
  (`flake.nix:42`, selected at `flake.nix:50`; commit `927fb9f`, 2026-08-18).
- **Why this is CI and not a hypothetical Intel-Mac user.** The macOS nightly
  must run on `macos-26-intel` (`.github/workflows/nightly-macos.yml:50`)
  because GitHub's Apple Silicon runners cannot nest a VM for Podman Machine —
  so the only hosted runner exercising the macOS code paths is x86_64. GitHub
  plans to retire Intel runner images around late 2027 (`nightly-macos.yml:44-49`),
  and macOS 26 (Tahoe) is the last Intel macOS — so **two independent clocks run
  on this runner**, nixpkgs 26.05's security window and GitHub's image retirement.
- **When 26.05 lapses the choice is binary:** a self-hosted arm64 Mac runner, or
  dropping the container-backend macOS tests to macos-user only. **A deadline,
  not a bug — it needs a decision before the end of 2026.**

> [!WARNING]
> **The recorded diagnosis for this was exactly backwards for 29 nights.** The
> nightly went red on 2026-07-21 (the flake.lock bump that crossed 26.11) and
> stayed red for 29 consecutive nights while the roadmap recorded *"nix is broken
> on that runner, not in our tree."* It was our flake, on every Intel Mac,
> reproducible in 0.2s with `nix eval .#installPrefix`. Four nights were spent
> re-triggering a run that could not have passed. **Reach for the 0.2s eval
> before blaming a runner.**

## 0.1 CI status — the macOS nightly

| Fact | Value | Checked |
|---|---|---|
| Nightly | **GREEN** — run `32623453131`, `build-image` success, `integration-macos` success, suite `ok … 3915.734s` | 2026-08-23 |
| Previous night | `32557449248` — `FAIL … 5073.141s` | 2026-08-22 |
| The real 08-22 failure | `TestExtraPackageLibFarm` at **1216.11s** against the job's 1200s cap — the only one. Fixed by an explicit 40-minute `withTimeout(nixBuildJailTimeout)` on both `packages:`-setting tests (`01a51dc4`, `integration/packages_test.go`); the lib farm then passed at 812.37s | 2026-08-23 |
| **Darwin warmup: SKIPPED** | `warmJail` returns early on `GOOS == "darwin"` — `integration/harness_test.go:147-153`, commit `e5b60902` (2026-08-23). A warmup pre-pays a *container start*; on darwin every launch **realises an image** (a loaded image can never match a darwin `nix eval`), so the warmup was a full nix build wearing a warmup's name — **12m0s of waste per night**. The first container test absorbs the one-time cost instead. Linux CI keeps the warmup, where the premise holds and it earns its 1m56s | 2026-08-23 |
| Image-skew oracle on darwin | **auto-downgraded to `warn`** — a Linux-runner-built image can never match a darwin `nix eval`, so on a Mac you do **not** get the stale-image protection. Check by hand | 2026-08-23 |

**Still unobserved:** nobody has watched a nightly run *with* the warmup skip in
place. The expectation is `integration-macos` losing ~12 minutes of wall clock
and the first container test growing by roughly the image realisation. Needs the
nightly.

---

## 1. The three macOS runtimes (where the agent runs)

| Runtime | Kind | Status | Notes |
|---|---|---|---|
| **podman** | container in one shared podman-machine VM | ✅ [L/CI] | first-class; the reference macOS container path |
| **Apple Container (AC)** | one lightweight VM per container | 🟡 [M] | default macOS runtime; early-stage — hit limits this session (bind-mount cap, `:ro` ignored, no `--net=host`, OCI convert) |
| **macos-user** | native macOS user + Seatbelt, NO VM | ✅ [M] *(2026-07-21 build)* + ⚠ | **PROVEN on real HW 2026-07-21** (macOS 26.5 arm64): Seatbelt launch runs the agent as `_yolojail`, `packages:` via native darwin nix (buildEnv) with `which just` → `/nix/store/…` (OQ-1 path_helper fix holds), finding-6 password set, fresh-inode re-exec (no SIGKILL), host creds invisible, teardown idempotent. **Runbook → docs/plans/runbooks/mac-macos-user-e2e.md; results → mac-sandvault-session.md §6b.** **⚠ The backend has changed underneath that proof (see the row below).** |
| ↳ *macos-user, since 2026-07-21* | — | ⚠ [M] | **What moved, and what it means for the ✅ above.** (a) **Pack staging was wired 2026-08-12** and has never run on a Mac — before that, this backend rendered ZERO pack surfaces silently, so the 07-21 proof was of a backend that configured nothing from packs. (b) **The confinement half was re-measured 2026-08-19** by running the work under the profile a real `--dry-run` emits: `go build ./...`, full `go test -short ./...` (58 pkgs) and `just test-fast` pass; SSH keys/`~/.claude`/`~/.aws`/keychains all `Operation not permitted`. (c) **The `sudo -u _yolojail` launch itself is still unproven end-to-end** — that is the open half. (d) **`workspace_readonly` was a silent no-op here until 2026-08-23** (see §3). 📄 [../plans/handoff-guest-notch-macos.md](../plans/handoff-guest-notch-macos.md) §2 |

## 2. Builder (how the Linux image / packages get built) — CONTAINER RUNTIMES ONLY

macos-user needs **no builder** (native darwin nix, no Linux image). The builder
question exists only for podman/AC.

| Builder | Runtime | Status | Notes |
|---|---|---|---|
| **Cachix / prebuilt download** | any | 🟡 | THE happy path. **"Account deferred" is stale (checked 2026-08-23):** the substituter + public key are live (`flake.nix:13-16`, `730c258`), the `yolo-jail` cache and the `CACHIX_AUTH_TOKEN` secret both exist, and yolo passes `--accept-flake-config` on every nix invocation so the flake's own cache is actually consulted (`internal/image/nixflags.go:35`, `internal/darwinpkg/darwinpkg.go:91`). Remaining: the **Mac download proof**, and — disputed — the first push: `docs/plans/README.md:64` (repinned 2026-09-02) says CI has already pushed, `handoff-cachix-cache.md` still lists it as remaining. **Not checkable from a Linux jail.** No build → no builder needed. |
| **Container builder** (nix+sshd container on the runtime) | **podman** | ✅ [L] | **proven end-to-end in-jail**: image built, `ssh-ng` build ran inside container, result read back. `packages.builderImage` in flake. |
| **Container builder** | **Apple Container** | ✅ [M] | **PROVEN on real HW 2026-07-17** (macOS 26.5 arm64, AC 0.12.3, nix 2.34.7): AC pulled the GHCR image, ran it with internal-network IP `192.168.64.2:22`, host nix `store info` → `Trusted: 1`, proof build returned `AC-CONTAINER-BUILDER-WORKS`. No `-p` needed — AC's per-container VM IP is directly reachable. **Runbook → docs/plans/runbooks/mac-ac-container-builder.md.** |
| **Container builder → CLI wiring** | podman + AC | ✅ [L] *(2026-08-23)* | **The "Go-port gap" in §5 item 3 is CLOSED.** `internal/containerbuilder` was resurrected (`8abb67ce`) and wired into the image path (`c2f0b941`), both 2026-07-21: `internal/image/autoload.go:13` imports it and `:219` calls it through the `BuildOffload` seam, which is a nil-returning stub on Linux and in tests (`autoload.go:105-106`). |
| **QEMU `darwin.linux-builder`** | any container rt | ❌ removed | **DROPPED — Open Decision #3 RESOLVED 2026-07-23** (see [../design/linux-builder-lifecycle.md](../design/linux-builder-lifecycle.md)). The container builder is the sole shipped builder on both runtimes. The AC-can't-host-sshd unknown that justified keeping QEMU as a fallback is discharged (AC hosting PROVEN 2026-07-17, row above). `internal/builder` + the `yolo builder` commands are being deleted, not reworked. **Done — verified 2026-08-23: `internal/builder` does not exist**, and `yolo check`'s Image Build section plus `nixdiag.LinuxBuilderRemedy` are rewired onto the container builder. |
| nix-darwin `linux-builder` | any | ⬜ | user-side; only if they already run nix-darwin. Documented, not ours to install. |

## 3. Feature × runtime coverage (does each yolo capability work per runtime?)

Rows marked **(2026-08-23)** were re-derived from the tree by this pass; the rest
are from the 2026-07 era and were not re-measured.

| Capability | podman | Apple Container | macos-user |
|---|---|---|---|
| Run agent in jail | ✅ [L] | 🟡 [M] | ✅ [M] *(07-21 build; see §1 ↳ row)* |
| `packages:` (nix) | ✅ via image | 🟡 via image | ✅ native nix buildEnv [M] — attr is now `yoloNoncontainerPackages`, system from `NativeSystem()`, **not** hardcoded `aarch64-darwin` (2026-08-23) |
| Build when uncached | ✅ container builder [L] | ✅ container builder [M] | ⬜ (native, no build offload) |
| **Bind mounts at all** | ✅ | ✅ (with the `:ro` caveat below) | ❌ **none — structural** (2026-08-23). `internal/macosuser/runplan.go:186`, `seatbelt.go:25`. Every row below that depends on a mount inherits this. |
| `/ctx/` mounts read-only | ✅ (`:ro` honored) | ❌ AC ignores `:ro` → **skipped w/ warning** [L] | ❌ **not honored — no mounts exist here** (2026-08-23). The old "🟡 Seatbelt subpath deny" cell conflated `workspace_readonly` with config `mounts`; they are different keys. |
| `workspace_readonly` | ✅ (`-v …:ro`, `internal/cli/run/mounts.go`) | ❌ not enforced → **warns** [L] | ✅ [L] **since 2026-08-23 only** (`d0961f2c`) — and it was a **silent no-op before that**. Now native SBPL: **one** `(deny file-write* …)` form carrying one `(subpath <ws>/<rel>)` clause per entry (not one deny per entry — corrected 2026-08-23), emitted AFTER the writable-set allow (last-match-wins), `internal/macosuser/seatbelt.go:15-34,91,120-124`. Absolute/escaping entries are dropped, not emitted. |
| `per_side_paths` | ✅ (per-side shadow mounts; root `.venv` + `node_modules` by default) | 🟡 [M] (container pipeline; unverified on AC) | ❌ **unenforceable → WARNS** (2026-08-23). Per-side shadowing needs a mount namespace and Seatbelt cannot fork a path — `internal/macosuser/orchestrator.go:150-165`. Warning shipped in the same commit as the row above, on the rule that a new default silently absent on one backend repeats the defect being fixed. |
| `host_files` | ✅ | 🟡 [M] (container pipeline; unverified on AC) | 🟡 **partial, accepted deficiency** (2026-08-23). Source-**less** entries stage; **source-bearing entries are FILTERED OUT** rather than rendered with an empty host layer — there is no `/ctx/host-user` to carry a host file into. Pinned at `internal/macosuser/macosuser_test.go:294-300`. |
| **pack surfaces** | ✅ [L] via `:ro` `/ctx/packs` | 🟡 [M] (container pipeline; unverified on AC) | 🟡 **[M] wired 2026-08-12, NEVER RUN ON A MAC**. Staged root-owned to `/var/yolo-jail/packs/<session>`; `--dry-run` prints the `packs:` line (`internal/macosuser/orchestrator.go:326`). The unmeasured part is the `sudo -u _yolojail` stage + the sandbox-uid read. |
| **skills / briefings** | ✅ (bind mounts) | 🟡 [M] (container pipeline; unverified on AC) | ❌ **do not reach this home at all** — they cross into a container as bind mounts, and there are none. Unclaimed gap, not a defect with an owner. |
| MCP preset wrappers | ✅ | 🟡 [M] (container pipeline; unverified on AC) | ⚠ **generated but Linux-absolute** (2026-08-23). `internal/entrypoint/darwin.go:59` runs `GenerateMCPWrappers` unconditionally; bodies hardcode `/usr/bin/chromium`, `exec /bin/node`, `/etc/fonts` (`mcp_wrappers.go:26-27,39,72-74`), no `GOOS` guard. Three dead files in a Mac home; harmless until exec'd. |
| loopholes / host services | ✅ | 🟡 | ❌ **none start here** (2026-08-23). `startLoopholesDisclosed` is called once, at `internal/cli/run/run.go:569`, inside `runContainer`; the macos-user arm returns above it. One primitive is built and **uncalled**: `macosuser.EndpointGrantCommands` (`macosuser.go:430`, zero **production** call sites — its only callers are `macosuser_test.go:382,447`). Track L part 1. |
| `env_sources` (API keys) | ✅ | ✅ | ✅ [L] (fixed 2026-07) |
| `security.blocked_tools` | ✅ | ✅ | ✅ [L] (baked into bootstrap) |
| `mise_tools`/`mcp`/`lsp` | ✅ | ✅ | ✅ [L] (baked into bootstrap) — but see the MCP-wrapper row |
| bridge networking (nested podman) | ✅ [L] | ⬜ (AC networks internally) | ⬜ (no container) |
| GPU passthrough | ⬜ macOS has none | ⬜ | ⬜ |
| Resource limits | via machine VM | ✅ native `--cpus/--memory` | ❌ Seatbelt has no cgroups |
| Isolation strength | VM | VM (per-container) | Seatbelt (weaker; documented) |

> [!WARNING]
> **A config key that is accepted and does nothing is worse than one that
> refuses** — the lesson `workspace_readonly` cost. It was delivered *only* as a
> container `-v …:ro` bind, so macos-user accepted the key and silently provided
> no protection, from the backend's introduction until 2026-08-23. Neither the
> config validator nor this matrix caught it; this matrix in fact recorded it as
> 🟡 *"Seatbelt [L]"*, which was a cell asserting a mechanism that did not exist.
> **When adding a row here, check the key is READ on that backend, not merely
> valid in the schema.** The fix was cheap once seen — Seatbelt already expresses
> the same policy natively — which is the point: the cost was never the fix, it
> was the months of believing the cell.

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

**macos-user end-to-end — ✅ DONE (2026-07-21), macos-user column unblocked:**
> Does the native no-VM backend run an agent as `_yolojail` under Seatbelt with
> `packages:` materialized via native aarch64-darwin nix? **YES.** All four J2
> behaviors (fresh-inode re-exec, Go bootstrap self-exec, finding-6 password,
> OQ-1 path_helper acceptance bar — `which just` → `/nix/store/…`), plus
> creds-invisible and teardown idempotence, observed on real HW (macOS 26.5
> arm64). Results in mac-sandvault-session.md §6b.

**Other Mac-only gates (rewritten 2026-08-23).** The single sentence this
replaces named only the AC "run agent in jail" row. The actual list, in the
order a Mac session should attack it:

1. **The Mac's config still uses the removed `agents` key** (measured
   2026-08-19), so *no* current `yolo` launches on that machine, on any backend
   — `yolo check` included. Its installed `yolo` was also **531 commits stale**,
   which is why the refusal never surfaced in daily use. Renaming the key to
   `packs` is the whole fix; all four names it selects exist as packs. **Nothing
   else on this list can be measured until that is done.**
2. **The `sudo -u _yolojail` pack-staging step** — the surviving half of item
   1.4. The Seatbelt confinement around it was measured 2026-08-19 and passes;
   the user-switch above it never has.
3. **D4's one download proof** (§2 Cachix row).
4. **The AC "run agent in jail" row** — the original entry, still open.
5. **A2's hard error on a genuinely darwin-less package** — never exercised,
   because the only packages the Mac session used (`jq`, `just`) *do* build on
   darwin. It is also not yet implemented (revival plan A2).

📄 [../plans/handoff-guest-notch-macos.md](../plans/handoff-guest-notch-macos.md)
is the single collected list, with the open questions attached.

## 5. Roadmap (ordered)

1. ✅ **[M] Prove the AC container builder** — DONE 2026-07-17 (real HW, see §4
   and the runbook). AC joins podman as a proven container-builder path.
2. ✅ **[M] macos-user end-to-end** — DONE 2026-07-21 (real HW, see §4 and
   mac-sandvault-session.md §6b). The **path_helper login-shell PATH fix**
   (`.zprofile`/`.zshrc`/`.bash_profile` re-prepend the sandbox PATH after macOS
   path_helper) holds: `which just` → `/nix/store/…/bin/just`, not Homebrew's
   `/usr/local/bin`. Two jail-side fixes landed from the run: `--accept-flake-config`
   in `darwinpkg.nixFlags()` (honor the flake's own cachix substituter) and a
   linux-tag move of `resolveContainerCgroup` (darwin staticcheck FP).
3. ✅ **Wire the container builder into the CLI run/check path** — **DONE
   2026-07-21** (revival plan **J3**): `8abb67ce` resurrected
   `internal/containerbuilder` and added the session lifecycle; `c2f0b941` wired
   the offload into `AutoLoadImage`. Verified 2026-08-23 at
   `internal/image/autoload.go:13` (import) and `:219` (call through
   `BuildOffload`, a nil-returning stub on Linux and in tests, `:105-106`). The
   Go-port-gap narrative below is the 2026-07-19 record of the problem, kept
   because it is why the package exists twice in git history:
   It shipped in Python 2026-07-17
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
5. ✅ **Remove the VM builder** (`internal/builder` + the `yolo builder` commands).
   Superseded by removal (Open Decision #3, 2026-07-23) — the container builder
   is the sole builder, so there is nothing to rework onto launchd.
   **DONE — verified 2026-08-23: `internal/builder` does not exist**, and both
   `yolo check`'s Image Build section and `nixdiag.LinuxBuilderRemedy` point at
   the container builder.
6. 🟡 **Turn on Cachix** — **the substituter is ON** (`flake.nix:13-16`,
   `730c258`, 2026-07-20), and `--accept-flake-config` is passed on every nix
   invocation so it is actually consulted (`internal/image/nixflags.go:35`,
   `internal/darwinpkg/darwinpkg.go:91`). Cache, account and
   `CACHIX_AUTH_TOKEN` all exist. What is left is the Mac **download** proof —
   plus, disputed and not checkable from a Linux jail, whether the first push
   has happened (§2 Cachix row). Removes the builder entirely for cached images.
7. ~~QEMU `darwin.linux-builder` as the documented fallback~~ — REMOVED
   (Open Decision #3, 2026-07-23). No longer a documented fallback; the
   container builder is the sole builder. A user's own nix-darwin
   `linux-builder` remains only as a personal escape hatch (row above).
8. 💬 **Decide what replaces `macos-26-intel` before nixpkgs 26.05 lapses** —
   NEW, added 2026-08-23. See §0: 26.05 is the last branch supporting
   `x86_64-darwin` and is security-fixed only to the end of 2026, while the
   nightly must stay on an Intel runner because GitHub's Apple Silicon runners
   cannot nest a VM for Podman Machine. The choice is a **self-hosted arm64 Mac
   runner** or **macos-user-only macOS tests**. Deadline-driven, not
   defect-driven — it needs a ruling before the window closes, and it is the only
   item in this list with a date attached.

## 6. Cross-refs
- **[runbooks/mac-ac-container-builder.md](../plans/runbooks/mac-ac-container-builder.md)** — Mac test (zero-sudo) for the gating AC-builder cell.
- **[runbooks/mac-macos-user-e2e.md](../plans/runbooks/mac-macos-user-e2e.md)** — Mac test (you-drive) for the macos-user backend.
- [macos-container-builder-exploration.md](macos-container-builder-exploration.md) — why container-builder, image sourcing, AC risk.
- [macos-linux-builder-explained.md](macos-linux-builder-explained.md) — the Linux-person's explainer of the whole builder question.
- [macos-no-vm-direction.md](../design/macos-no-vm-direction.md) — the "pursue both backends" decision.
- [macos-revival-and-distribution-plan.md](../plans/macos-revival-and-distribution-plan.md) — the current macos-user + distribution roadmap of record.
- [handoff-cachix-cache.md](../plans/handoff-cachix-cache.md) — the prebuilt-download happy path.
- **[handoff-guest-notch-macos.md](../plans/handoff-guest-notch-macos.md)** — every Mac-gated item in one place, so one trip to a Mac can close all of it; holds the open questions (`OQ-GN1`–`OQ-GN4`) behind §4's list above.
- [noncontainer-nix-environment.md](../design/noncontainer-nix-environment.md) — §5.1 is where the `x86_64-darwin` drop was first measured probe-by-probe; §8 Option 1 is the (now shipped) nix-profile work.
