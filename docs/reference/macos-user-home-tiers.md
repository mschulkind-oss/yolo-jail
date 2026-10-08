---
status: current
next: "Rule OQ-HT5 (leaning (a): keep refusing a real home-root file where a host_files link belongs, with sudo rm of that file) and OQ-HT6 (leaning (a): keep refusing a second workspace's launch while a session holds the account home, HT-D15)"
verified: 2026-09-21
verified_commit: 753bcb88
covers:
  - internal/entrypoint/darwinhomelayout.go
  - internal/entrypoint/darwin.go
  - internal/entrypoint/darwinoverlay.go
  - internal/entrypoint/hostfiles.go
  - internal/entrypoint/packhooks.go
  - internal/macosuser/macosuser.go
  - internal/macosuser/runplan.go
  - internal/macosuser/orchestrator.go
  - internal/macosuser/seatbelt.go
  - internal/macosuser/homereadonly.go
  - internal/macosuser/seatbeltcapture.go
  - internal/paths/paths.go
  - internal/packload/packload.go
  - internal/cli/run/assemble.go
  - internal/cli/run/assemble_parts.go
  - internal/cli/run/macoshomeoverlay.go
  - internal/cli/run/backendlimits.go
  - internal/cli/run/loopholeinert.go
  - internal/cli/run/flock.go
  - internal/cli/run/accounthomehold.go
  - internal/cli/stop.go
  - packs/claude/pack.json
tags: [macos-user, jail-home, tiers, backend-parity, seatbelt, credentials]
---

# The macos-user home has a workspace tier, and it is symlinks into the workspace's own sidecar

**Status:** CURRENT as of 2026-09-21, verified against `753bcb88`. Built 2026-09-12; the layout,
the mirror and the tier separation were exercised on real hardware the same day (Apple Silicon,
macOS 26.5 arm64) and run nightly in CI since. [What is measured, and by
what](#what-is-measured-and-by-what) separates a hardware measurement from a Linux unit gate
from a claim that is still an argument.

> **In short.** `HOME` on `macos-user` is the constant `/Users/_yolojail`, and it is the
> **machine** tier. The **workspace** tier is inside it, as symlinks: every directory the podman
> argv binds from `<workspace>/.yolo/home` is a symlink from the account home into that same
> sidecar. Every directory a pack declared `scope: machine` stays real in the account home and
> is **mirrored** into the sidecar, so the `shared_credentials` hook's deliberately *relative*
> symlink keeps resolving. Nothing about credential sharing, and no Seatbelt rule, is
> per-backend.

| Component | Lives in |
| :--- | :--- |
| The pure deriver and the boot-path entry | `internal/entrypoint/darwinhomelayout.go` (`DeriveDarwinHomeLayout`, `InstallDarwinHomeLayout`) |
| The cache relocations' links in `~/.cache` | `internal/entrypoint/darwinhomelayout.go` (`InstallDarwinCacheRelocations`), named by `internal/macosuser/runplan.go` (`DarwinCacheRelocationsEnv`) from what `internal/cli/run/macosuserrelocations.go` delivers |
| Home-root `host_files` links, and the checked path the host_files step writes | `internal/entrypoint/darwinhomelayout.go` (`DarwinHomeLayout.WithHostFileRedirects`, `homeFileThroughLayout`, and `packHookLinks` for the pack hooks' links the walk follows), `internal/entrypoint/hostfiles.go` (`hostFileDestination`) |
| Where it runs in the native bootstrap | `internal/entrypoint/bootsteps.go` (the `darwin_home_layout` step, the first the macos-user bootstrap runs) |
| The two tier lists it reads | `internal/packload/packload.go` (`WritableDirs`, `SharedDirs`), declared per pack in `packs/*/pack.json` |
| The directory names both backends share | `internal/paths/paths.go` (`HomeSurfaces`, `HomeFileRedirects`, `WorkspaceHomeState`) |
| The shared-tier hooks whose links must keep resolving | `internal/entrypoint/packhooks.go` (`linkSharedCredential`, `linkSharedDirectory`) |
| The account, the mise store, the staged trees | `internal/macosuser/macosuser.go` (`SandboxHome`, `SandboxMiseData`, `StagedHomeOverlay`) |
| The sidecar's crossing, and the refusal when it is absent | `internal/macosuser/runplan.go` (`buildBootstrapEnv`, `PlanInvariants`) |
| The confinement primitive | `internal/macosuser/seatbelt.go` (`SeatbeltProfile`) |
| The write-protection of the staged skills and briefings | `internal/macosuser/homereadonly.go` (`ResolveHomeReadonly`), rendered by `internal/macosuser/seatbelt.go` (`homeReadonlyDenies`) |
| Content delivery over the layout | `internal/cli/run/macoshomeoverlay.go` (`buildMacosHomeOverlay`), `internal/entrypoint/darwin.go` (`InstallHomeOverlay`), `internal/entrypoint/darwinoverlay.go` (`WriteHomeOverlayManifest`, `overlayLinks.route`, `installOverlayDestination`) |

**Reads with:** [`macos-user-nix-and-features.md`](macos-user-nix-and-features.md) (the backend,
and the standing refusal of a per-workspace `HOME` this layout keeps true),
[`jail-home.md`](jail-home.md) (the container home layout this converges on — its *Shared
credentials* section is the relative-link fact the mirror exists for),
[`agent-credentials.md`](agent-credentials.md) (what crosses the boundary, and the per-backend
table), [`../design/declaration-parity.md`](../design/declaration-parity.md) (`DP-L1`, the
copy-plus-profile mechanism and its measured verdict),
[`macos-user-provisioning.md`](macos-user-provisioning.md) (the floor and the confined stage,
whose [`OQ-P3`](macos-user-provisioning.md#oq-p3) takes its answer from the mise-store fact
below),
[`../plans/runbooks/macos-user-manual-checks.md`](../plans/runbooks/macos-user-manual-checks.md)
(item 5 is this layout's hardware check; items 9 and 10 are its tier and refusal checks).

> [!NOTE]
> **Terms.** A **tier** is a scope at which jail state is kept separate: **machine** (shared by
> every jail on the host — credentials, the tool store, the cache), **workspace** (one project —
> pack `state`, agent history, composed content, installed programs), and **session** (one
> launch — generated config). A tier is not a directory and not a confinement notch: the
> container backends implement all three with two directories, and every notch has all three.
>
> **A home is not a workspace.** `/Users/Shared/yolo` (`macosuser.SharedRootDefault`) is the
> neutral root your PROJECTS live under, so `/Users/Shared/yolo/yolo-jail` is a workspace.
> `/Users/_yolojail` (`macosuser.SandboxHome`) is the sandbox account's `HOME`. This document is
> about the second.
>
> **The sidecar** is `<workspace>/.yolo/home` (`paths.WorkspaceHomeState`) — the workspace's own
> gitignored state directory, and the SOURCE of every per-workspace directory the container
> backends bind into `/home/agent`. It is where the workspace tier lives on every backend that
> has one.

---

## The three tiers, and where each one lives

`macosuser.SandboxHome()` is the constant `/Users/_yolojail`: no workspace component and no
session component. The tiers are separated inside it rather than by it.

| Tier | Container backends (podman) | macos-user |
| :--- | :--- | :--- |
| **machine** — credentials, the tool store, the cache | `paths.GlobalHome()` bound `:ro` as the home base (`internal/cli/run/assemble_parts.go`, `podmanBaseMounts`), plus one rw bind per pack `scope: machine` dir (`internal/cli/run/assemble.go`) | the account home itself: every `packload.SharedDirs` entry stays a REAL directory there, alongside `~/.cache` and `~/.yolo/mise` |
| **workspace** — pack `state`, agent history, composed content, installed programs | `<ws>/.yolo/home/{npm-global,local,go,yolo-bin,config}` plus one bind per pack `scope: workspace` dir | the same sidecar, reached by a SYMLINK from the account home per directory |
| **session** — one launch's generated config | regenerated into the workspace binds on every entry | regenerated on every launch, THROUGH those symlinks, so it lands in the sidecar |

**The container column is podman's argv; Apple Container reaches the same two tiers with a
different mount shape.** It binds the whole workspace state dir at `/home/agent` **read-write** in
one mount, which covers the workspace tier without naming a single directory, and then nests one
bind per `scope: machine` dir inside it from `paths.GlobalHome()` (`appleContainerBaseMounts`, in
the same file as `podmanBaseMounts`). The tiers are the same on both; the primitive count is not.

The account home holding the machine tier is not an accident to be repaired: a single account
holding one set of agent credentials is the point of a dedicated sandbox user, and it is why
`shared_credentials` works here with no broker at all. What the account home does **not** hold
is any directory the layout links.

> [!IMPORTANT]
> **The machine tier is what the agent is told about, and the sentence names `SharedDirs`.**
> `run.backendLimits` emits one briefing line on every launch whose packs declare a machine-tier
> directory — *"Your home is SHARED by every workspace on this machine"* — listing
> `packload.SharedDirs(packs)`, and is silent when that list is empty. It named
> `WritableDirs` until DP-B11 corrected it, which was exactly backwards: those are the
> directories the layout makes per-workspace, so the line enumerated the private set and stayed
> silent about the shared one.

## The layout: what is a symlink, what is a mirror

`entrypoint.DeriveDarwinHomeLayout(home, sidecar, writableDirs, sharedDirs)` is a pure function
and produces four ordered groups. `InstallDarwinHomeLayout` is the boot-path entry; it reads the
sidecar from `YOLO_DARWIN_HOME_SIDECAR` and the two tier lists from `packload.WritableDirs` /
`packload.SharedDirs`, and adds a fifth group from the user's config,
`DarwinHomeLayout.WithHostFileRedirects`, read from `YOLO_HOST_FILES`, the variable the launcher
already passes to the bootstrap for the host_files step.

| Group | What it is | Contents |
| :--- | :--- | :--- |
| `Dirs` | created first, in the sidecar and in the account home | every link target, plus the real `scope: machine` directory each mirror points at |
| `Links` | the workspace tier — an account-home path pointing INTO the sidecar | `paths.HomeSurfaces()` (`npm-global→.npm-global`, `local→.local`, `go→go`), `yolo-bin→.yolo/bin`, `config→.config`, and each `packload.WritableDirs` entry with its leading `.` trimmed for the sidecar name |
| `Mirrors` | the machine tier as the SIDECAR sees it | `<sidecar>/<dir> → <home>/<dir>` for each `packload.SharedDirs` entry |
| `FileRedirects` | home-ROOT files that are symlinks into a per-workspace directory | `paths.HomeFileRedirects()`, filtered to the ones whose holding directory THIS launch actually laid |
| `HostFileRedirects` | the user's home-ROOT `host_files` files (`~/.npmrc`), the same links podman's home skeleton lays | one per entry `config.HostFileEntry.StagingFor` gives a symlink, to `SymlinkTarget` (`.config/yolo-home/<slug>`), resolving through the `.config` link; laid dangling, and the directory it names is left to the host_files step ([HT-D9](#ht-d9)). None for a login rc file the bootstrap writes itself (`DarwinLoginRCFiles`, [HT-D12](#ht-d12)) |
| cache relocation links (not a field: `InstallDarwinCacheRelocations`, in the same step) | the MACHINE tier's `~/.cache/<subdir>` pointing at the user's relocation target | one per user-scope `cache_relocations` entry, named in `YOLO_DARWIN_CACHE_RELOCATIONS`; a real directory there refuses, and a link this step laid for a subdir no longer relocated is removed ([HT-D16](#ht-d16)) |

**The list is the container's, not a new one.** Every entry cites the mount it mirrors, and that
is a constraint rather than tidiness: a directory added to the podman mount table and not here
(or the reverse) is a per-backend answer to *"where does my agent's state live"*. The sidecar
targets are spelled **absolutely**, which is not in tension with the relative link a pack hook
writes — these are the launcher's own layout, the "make it appear at this path" half of a bind
done with a symlink, and nothing pack-facing reads them.

**What is deliberately NOT linked stays machine-wide, because the container keeps it machine-wide
too.** `~/.cache` is `paths.GlobalCache()` on podman, and the mise data dir is a machine-wide
store podman mounts at `/mise`. `~/.cache` is per-workspace nowhere; the mise store is
per-workspace only on Apple Container, whose disks attach to one VM at a time
([OQ-MB1](../research/macos-backend-performance.md#OQ-MB1)). The one link inside `~/.cache` is
the user's own: a subdirectory a user-scope `cache_relocations` entry moves is a link to its
target, which is machine tier too, since the key is the user's and not a workspace's
([HT-D16](#ht-d16)).

> [!WARNING]
> **`MISE_DATA_DIR` must be NAMED, or the tool store silently becomes per-workspace.** mise's own
> default is `$HOME/.local/share/mise`, and `~/.local` is a layout link — so leaving the default
> in place puts one machine-wide store inside every workspace's sidecar, which no other backend
> does, and nothing fails. `macosuser.SandboxMiseData` names `<home>/.yolo/mise` and is read by
> the launch env, the bootstrap env and the PATH's shims dir alike.
>
> ⚠ **Its guard asks the layout, not a spelling.** The check used to be
> `strings.Contains(want, "/.local/")` — the spelling its own comment mentions rather than the
> property the comment is about. Measured 2026-09-12: pointing the store at `~/.config/mise-store`
> or `~/go/mise` put it back inside the sidecar with the whole short suite green, because
> `.config` and `go` are links too. `assertOutsideTheWorkspaceTier`
> (`internal/macosuser/misedatadir_test.go`) now asks `DeriveDarwinHomeLayout` whether the path
> resolves through ANY workspace-tier link, so a link added tomorrow is covered without this file
> being edited.

**The sidecar crosses as exactly one variable, and nothing downstream would report its absence.**
`YOLO_DARWIN_HOME_SIDECAR` is set by `macosuser.buildBootstrapEnv`; a bootstrap that is not told
the sidecar lays no layout at all, every pack `state` dir stays in the shared account home, and
the launch looks perfectly healthy. So `macosuser.PlanInvariants` refuses a plan whose bootstrap
argv does not carry it **for this workspace** — checked against `paths.WorkspaceHomeState(plan.Workspace)`
rather than against "some value", because a layout pointed at another workspace's sidecar is the
tier collapse with extra steps.

**Absence is a real mode, not a degraded one.** An install capture bootstraps a throwaway staging
home whose whole contract is that everything an installer writes lands under it — capture walks
`paths.InstalledProgramSurfaces()`, which is `HomeSurfaces` plus the codex standalone payload, to
compute its delta, and `WalkDir` does not follow symlinks — so a capture
must keep the flat home. The capture planner sets no sidecar (`TestCapturePlanNamesNoSidecar`),
and `macosuser.SeatbeltCaptureProfile` adds a post-allow `(deny file-write* (subpath <home>))`
over the account home so the capture cannot reach the machine tier at all.

## The mirror, and the relative credential link it exists for

The mechanism that shares a credential is a **pack hook**, not colocation, and it is identical on
every backend. `Env.linkSharedCredential` replaces the pack's credentials file with a **relative**
symlink into a directory the pack declared at `scope: machine`; `Env.linkSharedDirectory` does the
same for a whole subdirectory. The target is computed with `filepath.Rel`, so it is depth-agnostic
by construction: `packs/claude` gets `../.claude-shared-credentials/.credentials.json`, and
`packs/pi` got `../../.pi-shared-npm` until it stopped sharing its npm prefix on 2026-10-05
([XB-D14](../design/pi-extension-store-builds.md#XB-D14)). Both hooks run on `macos-user` unchanged —
`RunDarwinBootstrap` calls `RunPackHooks` — and only a directory the pack **declared** shared is
reachable through either, so what crosses between workspaces is readable from the manifest.

> [!WARNING]
> **The relative link is why `Mirrors` exists, and it is the trap a bare symlink layout falls
> into.** The kernel resolves `..` **physically**. So through a plain `~/.claude →
> <ws>/.yolo/home/claude` link, `../.claude-shared-credentials/.credentials.json` lands in the
> **sidecar**, not the account home, and dangles. Every `SharedDirs` entry is therefore mirrored
> back into the sidecar as a symlink to the account home's real directory — the "make it appear at
> the path" half of a bind, done launcher-side, with the hook's output byte-identical to every
> other backend's.
>
> Measured on a Linux jail 2026-09-11 and **confirmed on macOS 26.5** the same day: the fixture
> (`link → real/sub`, `real/sub/via → ../shared/f`) fails with `No such file or directory` on
> both kernels. It needed no sandbox to establish, and that is worth keeping as method — path
> resolution happens in the VFS before the policy is consulted, so a Seatbelt profile can only
> deny an access that resolves, never make an unresolvable path resolve. The unsandboxed failure
> entails the sandboxed one.

> [!NOTE]
> **The hook is not what loses the credential; the AGENT is.** A natural reading of the ordering
> constraint is that `linkThroughShared`'s *"the shared file always wins"* rule copies a local
> credential into the dangling location. It does not: the shared path is built **absolutely**,
> nowhere near the link — `filepath.Join(e.Home, h.SharedDir)` in `linkIntoSharedDir`
> (`internal/entrypoint/packhooks.go`), with `filepath.Base(from)` joined onto it by
> `sharedFileNode.sharedPath` (`internal/entrypoint/sharedlink.go`) — and every write targets that
> path, so the hook lands its bytes correctly with no mirror at all. (`linkSharedCredential` is a
> one-line delegation to that function, which is why the attribution is worth stating.) What
> dangles is the link it
> leaves behind, and the loss happens later, when the agent reads or writes through it. The
> constraint is therefore *"the mirror exists before anything resolves through the link"*, which
> applying it with the `Links` satisfies.

The rejected alternative is worth keeping because it is the obvious one: emit an **absolute**
target from `linkSharedCredential` on this backend. That is a backend branch in the one hook every
backend shares — see [OQ-HT4](#oq-ht4).

## Where the layout runs in the boot, and the order it must keep

The layout is the **first generator step** of `RunDarwinBootstrap`, above genStep #1 — not merely
"before the pack hooks". `~/.yolo/bin` is itself one of the links and `GenerateShims` writes
through it, so a shim generated into the account home before the link was laid would be a blocker
in the wrong tier, and a link laid over the directory it had just created would refuse. The ONE
statement ahead of it is the `LoadJailPacks` call, and that is the same constraint from the other
side: the two pack-declared tier lists **are** the layout, so they have to be loaded before it can
be derived.

| Rule | Enforced by |
| :--- | :--- |
| The cache relocations' links are laid in the layout's step, and never through a link | `InstallDarwinHomeLayout` runs `InstallDarwinCacheRelocations` beside `Apply` and reports both refusals together; with a relocation to lay it refuses a `~/.cache` that is a link and writes beneath an `os.Root` on the real one, and with none it ignores a `~/.cache` that is not a real directory ([HT-D16](#ht-d16)) |
| The layout applies above genStep #1 | it is the first `genStep` in `RunDarwinBootstrap`; only `LoadJailPacks`, which supplies its two tier lists, runs earlier |
| `Dirs` before `Links` | `Apply` walks the fields in declaration order; `MkdirAll` THROUGH a dangling symlink fails (`Stat` misses, `Mkdir` hits `EEXIST`, `Lstat` says "not a directory") |
| The `SharedDirs` mirror before anything RESOLVES one | `Mirrors` is applied in the same step as the `Links`, above every generator |
| `MISE_DATA_DIR` names a path outside the workspace tier | `macosuser.SandboxMiseData` is the one function the launch env, the bootstrap env and the PATH's shims dir all read; `assertOutsideTheWorkspaceTier` asks the deriver |
| `InstallHomeOverlay` replaces its destinations and nothing else — never the layout, never a directory above or beside a destination | the host lists every destination beside the tree (`.yolo-home-overlay.json`), and `installOverlayDestination` replaces exactly one listed path at a time ([below](#the-overlay-replaces-its-destinations-and-nothing-else)) |
| Nothing is laid or delivered through a link the layout did not lay | `DarwinHomeLayout.linkedSidecarPaths`, checked first by `Apply` and again by `InstallHomeOverlay`; `overlayLinks.route` for a link at or above each listed destination; `DarwinHomeLayout.homeFileThroughLayout` for the git config `configure_git` writes and for every file the host_files step writes, which also follows a selected pack hook's link while it points at that hook's target ([HT-D14](#ht-d14); [below](#nothing-is-delivered-through-a-link-the-layout-did-not-lay)) |
| A redirect is laid only when this launch lays the directory that holds it | `DeriveDarwinHomeLayout` tracks the home-relative dirs it laid and filters `paths.HomeFileRedirects()` against them |
| A home-root `host_files` link is laid only when THIS launch declares the entry, and a launch that does not leaves it alone | `darwinHomeLayoutFor` hands `WithHostFileRedirects` this launch's `YOLO_HOST_FILES` and nothing else, and nothing removes a link it does not lay ([HT-D9](#ht-d9)) |

> [!WARNING]
> **The redirect rule is the one a reasoned ordering missed, and it bricked three integration
> tests on the first hardware run.** `paths.HomeFileRedirects()` is CORE, while the directories it
> points into are not: `.claude.json` targets `.claude/claude.json`, and `.claude` is a link only
> when a PACK declares it. A launch declaring **no packs** therefore required `~/.claude` to exist
> as a directory — and where an earlier launch had pointed it at a workspace since DELETED, the
> layout failed with `mkdir …/.claude: file exists`, the boot refused **naming no remedy**, and
> every later packless launch hit the same wall.
>
> The rule is [P2](#p2) applied to redirects: the layout manages what THIS launch declares.
> `~/.claude.json` means nothing without a `~/.claude` to hold it, so it is not laid. The stale
> link is **left alone, not removed** — removing it would either break a live sidecar's link or
> leave a real directory [OQ-HT2](#oq-ht2) then refuses forever. A launch that *does* declare the
> pack repoints it, and the paired tests differ only in that
> (`TestDarwinHomeLayoutSurvivesAStaleLinkFromADeletedWorkspace`,
> `…RepointsAStaleLinkThePackStillDeclares`).

### The overlay replaces its destinations, and nothing else

The host composes the same skills trees and briefings the container mounts, lays them out under
one root at their home-relative destinations, stages that root root-owned at
`/var/yolo-jail/home-overlay/<cname>`, and the bootstrap installs it over the home as its last
generator step. A bind mount covers exactly its destination — `~/.pi/agent/skills` — and leaves
every directory above and beside it alone, so the install has to have the same granularity. A
destination is **replaced whole**, which is what makes a skill the host stopped staging
disappear; nothing outside a destination is touched.

**The tree cannot say where a destination starts, so the host lists them.** An overlay holding
`.pi/agent/skills/demo/SKILL.md` does not say whether the destination is `.pi/agent/skills` or
`.pi/agent`. `buildMacosHomeOverlay` records every destination it lays out in
`.yolo-home-overlay.json` at the overlay root, from the same loops that write the tree, and the
install walks that list, never the tree. The list names the roots the tree already spells; it
maps nothing, so it is not a second mount assembler. An overlay without one is refused. It is also
the list the session's Seatbelt profile write-protects ([HT-D7](#ht-d7)).

**A destination is reached only through the layout's own links.** Walking down from the home, the
install passes a layout link only while it is the link this launch laid, to the target it laid,
and refuses any other link on the way, in the account home or in the sidecar; a link AT a
destination is replaced when it is in the sidecar and refused when it is in the account home. The
rules, and why, are [below](#nothing-is-delivered-through-a-link-the-layout-did-not-lay).

> [!CAUTION]
> **Two earlier installs guessed the granularity, and both deleted agent state on every launch.**
> The first (2026-09-03) replaced the overlay's top-level entry: for `.claude/skills` that is
> `~/.claude`, the whole state dir. The second (2026-09-12, with this layout) descended through
> the home's symlinks and replaced the first real directory it met; G14's review narrowed which
> links it would descend through on 2026-09-27, not where it replaced. For claude, codex and copilot
> that directory is the destination itself, because their skills sit directly in the linked state
> dir. For **pi, omp, agy and opencode** it is the directory *above* the destination —
> `~/.pi/agent`, `~/.oh-omp/agent`, `~/.gemini/config`, `~/.config/opencode` — so every launch
> deleted everything in that directory, among it pi's sign-in and sessions and the config files
> the same boot had just generated for pi, omp and opencode, and put back only the skills and the
> briefing (G36). Both releases that carried it lost the same set: 0.9.0 pi's `settings.json` and
> `models.json`, 0.10.0 its `mcp.json` too, and both omp's `models.yml`, opencode's
> `opencode.json`, pi's `auth.json` and sessions, and anything else those four directories held.
> Nothing recovers it but the user's own backup of `<workspace>/.yolo/home`. That state lives in the workspace sidecar, which podman binds at the same paths,
> so the wipe also reached what a podman jail on the same workspace had written there. `TestDarwinOverlayInstallKeepsAgentStateBesideAndAboveEveryDestination`
> drives the real boot for every shipped pack that declares a destination, enumerated from the pack
> manifests, and fails on both earlier installs.

How one destination is installed, each rule a test in `internal/entrypoint/darwinoverlay_test.go`:

| Rule | Why |
| :--- | :--- |
| The replacement is built completely beside the destination (`.<name>.yolo-overlay-new`) before the destination is touched, with a copy that stops at its first error | A failed copy leaves the previous delivery whole, and a partial copy is never renamed into place |
| A file replaces a file in one `rename`; a directory moves the old copy to `.<name>.yolo-overlay-old`, renames the new one in, then removes the old | `rename` cannot replace a non-empty directory. A crash between the two renames leaves no destination and the previous copy under the aside name |
| Both sibling names are removed before an install starts | What a crash left behind is only overlay content, never agent state, so the next launch clears it and delivers |
| A destination that is itself a symbolic link is never followed: in the account home it is refused, and the launch names it; past a layout link, in the sidecar, it is replaced as a link | Following it would replace whatever it points at. In the account home, replacing it would put a real directory where a layout link may belong, which the next boot refuses ([OQ-HT2](#oq-ht2)); in the sidecar the layout lays no links, so it is an occupant like any previous delivery, moved aside and unlinked, and what it names is never touched |
| The directory holding a destination is reached only through this launch's layout links (`overlayLinks.route`), and must also resolve under the sandbox home or this workspace's sidecar | A link the layout did not lay is refused on the way, because the profile protects the path the layout laid ([below](#nothing-is-delivered-through-a-link-the-layout-did-not-lay)). The install runs outside Seatbelt, as the sandbox account, so the resolved directory is checked as well: a link the agent planted could otherwise aim it at a directory the agent itself cannot write, such as another workspace's sidecar |
| Every step after the containment check goes through a handle on the checked directory, opened beneath the home or the sidecar, never through the directory's path: creating it, clearing the working copies, the copy, both renames and the removal | The agent can be running during the install. The launch lock is released before the agent starts, and every workspace shares the one account home, so another session's agent can swap the directory, or one above it, for a link between two steps. A path is resolved again at every step and would follow that link. The handle keeps naming the directory that was checked, and `os.Root` refuses a swapped-in link that leads out of the root it was opened beneath. A swap can make the install fail, or finish in the directory the agent moved, but never write outside |
| The sidecar, and the `.yolo` above it, may not be links | The layout's links name the sidecar by path, so a link at either would carry every destination under it to wherever it points, and the check above would accept that target as the sidecar. The launcher refuses to launch with a link at either, so one found here was made after that check. `InstallHomeOverlay` refuses it first with G14's `LinkedSidecarError`, and the install's roots are opened refusing it again |
| A destination inside another listed destination is dropped, however the list sorts | The outer one's tree already carries it. `-` and `.` sort before `/`, so a sibling such as `skills-extra` can sort between `skills` and `skills/sub` |

The choices behind those rules, made while fixing G36:

1. *Implementation decision.* **The list travels inside the staged tree**, as a file at its root,
   rather than as a variable on the bootstrap's argv or as a list the bootstrap derives from its
   own pack load. One writer produces the tree and the list in the same loops, the root-owned
   staging copies both in one `cp -R`, and neither can describe a different launch than the
   other. A bootstrap-side derivation would be a second list, and a failed pack load would change
   what it replaces.
2. *Implementation decision.* **A missing list refuses** rather than falling back to walking the
   tree. On this backend the host and the bootstrap are the same binary (the launch stages its own
   executable), so only a hand-built overlay lacks one, and the fallback would be the guess that
   caused G36.
3. *Implementation decision.* **The working copies are fixed sibling names in the destination's
   own parent**, not random names and not a directory elsewhere. A sibling is on the same
   filesystem, so `rename` is guaranteed to work, and a fixed name lets the next install clear a
   crash's leftovers without guessing which are its own. Two installs of one destination are
   serialized by the per-workspace launch lock, which covers the bootstrap; a lock that cannot be
   taken degrades to a warning here as it does for everything else it covers.
4. *Implementation decision.* **One destination's failure does not stop the others**; every
   failure is reported together, the same rule `genStep` applies to generators. The boot still
   fails.
5. *Implementation decision.* **The list is a JSON object with one field**, `destinations`, so a
   later field does not need a new file.
6. *Implementation decision.* **Containment is an `os.Root` handle, not a check of a path.** The
   first version of this install resolved the directory, checked it and then wrote by path, and a
   review reproduced the gap: an agent that swapped the directory for a link after the check had
   another workspace's `skills` deleted and yolo's written in its place. The home and the sidecar
   are each opened as a root. A destination's directory is found by resolving its path once and
   is then opened beneath the root that holds it, by its path relative to that root. The
   container backends' host code in jail-writable state follows the same rule
   ([`jail-home.md`](jail-home.md), "Host code touches jail-writable state only beneath an
   `os.Root`").
7. *Implementation decision.* **The staged copy is written beneath the same handle**, with each
   directory made by `Mkdir` and each file created exclusively, so nothing already at a staged
   name, a swapped-in link included, is written through. The overlay itself is read by path,
   because it is root-owned under `/var/yolo-jail` and the sandbox account cannot write it.

## Isolation: the Seatbelt profile needs no change

`macosuser.SeatbeltProfile` opens `(allow default)` and then denies, last match wins. Two of its
denies do the work here:

```scheme
(deny file-write* (subpath "/"))
(allow file-write*
    (subpath <workspace>) (subpath <home>) (subpath "/tmp") … )

(deny file-read* (subpath "/Users"))
(allow file-read*
    (literal "/Users") (literal "/Users/Shared")
    <ancestor literals of the workspace>
    (subpath <workspace>)
    (subpath <home>))
```

The sidecar sits under the workspace, so it is inside both allows already, and a **sibling**
workspace's sidecar is re-allowed by nothing: the workspace's ancestors are granted as
`(literal)`, which grants the directory entry without re-allowing the siblings a `(subpath)` would.
So moving the workspace tier under the workspace makes the leak the profile always denied —
`~/.claude/projects/<other-workspace>/*.jsonl` readable through a shared home — enforced by the
kernel, with no profile edit.

**That conclusion depends on Seatbelt judging a symlink's TARGET, and that is measured** (Apple
Silicon, macOS 26.5, 2026-09-13: a link in an allowed directory pointing into a denied one gave
`Operation not permitted` for both an absolute and a relative spelling, with three controls
behaving). The same measurement is why a symlink YOU make for a cache opens nothing here, why
yolo's own `cache_relocations` delivery opens the link's TARGET in the profile it generates
([HT-D16](#ht-d16)), and why [`declaration-parity.md`](../design/declaration-parity.md) settled
`DP-L1` on a copy rather than a staged symlink.

## Seatbelt does the read-only half of a bind, and the launcher does the other

The reason this backend drops features is stated everywhere as *"it has no bind mounts"*, and for
the home tiers that sentence is too coarse. A `:ro` bind does two separable things: it makes a
file **appear** at a path, and it makes that path **unwritable**. Seatbelt does the second
natively, and the launcher runs **outside** the sandbox, so it can do the first by copying. Four
features already ride that route — `workspace_readonly` (which used to accept the key and silently
do nothing, until `readonlyDenies` gave it a kernel deny), skills and briefings (copied since
2026-09-03, and write-denied only since 2026-09-27 — [below](#the-staged-skills-and-briefings-are-write-protected-at-the-path-the-kernel-sees)),
source-bearing `host_files`, and a pack's `reads-host` layer.

On `readonly` the copy route is **stronger** than the container backends, not a degraded port:
`yolo config-ref` says of the container path that *"0444 is DAC, not kernel enforcement: an agent
running as root (Claude YOLO does) bypasses the mode bits"*, while a Seatbelt `file-write*` deny
holds regardless of uid. One difference survives and must be stated: a copy is a snapshot taken at
launch where a bind reflects a host-side edit live — equivalent for `host_files`, whose `readonly`
entries are re-rendered at boot on the container too, and a real degradation for a `mount` of a
live directory. The per-declaration census and its rulings are
[`declaration-parity.md`](../design/declaration-parity.md)'s, not this document's.

**What Seatbelt cannot supply**, so the resemblance to a container jail stops here:

- **`per_side_paths`** — two different contents at one path is a mount-namespace capability;
  Seatbelt filters permissions and cannot fork a path. Warned when the config DECLARES the key,
  never on a launch that does not mention it (`internal/macosuser/orchestrator.go`): a warning
  about a key nobody wrote is one readers learn to skip.
- **`cache_relocations`** — no longer in this list. A bind onto other storage has no mount here,
  but the launcher does the "appear at this path" half with a link the bootstrap lays at
  `~/.cache/<subdir>`, and the profile opens the target, read and write, after its `/Volumes` and
  `/Users` read denies ([HT-D16](#ht-d16); [`cache-relocation.md`](../plans/cache-relocation.md)).
  Only `~/.cache` moves: a macOS tool caching under `~/Library/Caches` is not covered.
- **Context mounts** (`mounts`, a pack's `mount`) — not delivered, and warned per entry.
  [`context-mounts.md`](../design/context-mounts.md#3-delivering-context-dirs-on-macos-user)
  proposes delivering them by root-owned link plus Seatbelt rules.
- **`writable_home_dirs`** — not a gap: the home is natively writable, so the knob has no target.
- **No PID, network or mount namespace**, and every jail runs as the same `_yolojail` uid, so a
  host daemon cannot tell which jail is calling. Concurrent sessions under *different* profiles do
  work — two sessions of one workspace each run under their own — but two WORKSPACES' sessions
  at once are refused, for the account home's sake rather than the profile's
  ([HT-D15](#ht-d15)); a macos-user jail launching another one does not work, which is an equality
  constraint in `sandbox_apply` rather than a policy gap.

## The staged skills and briefings are write-protected, at the path the kernel sees

Built 2026-09-27 (gap G14 in [`setup-support-gaps.md`](../plans/setup-support-gaps.md)). The
bootstrap copies every staged skills dir and briefing over the home **as the sandbox user**, so the
agent owns those files and their mode protects nothing. What protects them is the session's own
Seatbelt profile, which carries two rules after the writable-set allow, beside
`workspace_readonly`'s:

```scheme
;; #seatbelt-test-id:home-content-write-deny#
(deny file-write*
    (subpath "<ws>/.yolo/home/claude/CLAUDE.md")
    (subpath "<ws>/.yolo/home/claude/skills")
    (subpath "/Users/_yolojail/.claude/CLAUDE.md")
    (subpath "/Users/_yolojail/.claude/skills"))
;; #seatbelt-test-id:home-content-anchor-deny#
(deny file-write-create file-write-unlink
    (literal "<ws>/.yolo")
    (literal "<ws>/.yolo/home")
    (literal "<ws>/.yolo/home/claude")
    (literal "/Users/_yolojail/.claude"))
```

That is the profile a `packs: ["claude"]` launch gets; every other agent's destinations appear the
same way.

- **The paths are PHYSICAL.** `~/.claude` is a layout symlink into the sidecar, the kernel resolves
  it before the policy is consulted, and a rule naming the account-home spelling matches nothing
  while the link stands. So `macosuser.ResolveHomeReadonly` walks each destination the way the
  kernel does, through the same `DeriveDarwinHomeLayout` links the bootstrap lays, and names the
  sidecar path. The account-home spelling is kept too, since it is the one that matches wherever no
  link stands. Only the two BASES — the account home and the workspace — are resolved, through
  their longest existing prefix (the `/var` → `/private/var` class). Everything below them is
  joined as text, because this runs on the host before the bootstrap and on a first launch none of
  it exists; that text is the kernel's path only because the bootstrap lays nothing through a link
  it did not lay ([below](#nothing-is-delivered-through-a-link-the-layout-did-not-lay)).
- **The chain above a destination is anchored.** A path deny protects a path, not an inode: moving
  `~/.claude` aside and putting a directory of the agent's own where it was would leave every
  staged file untouched and point the agent's reader at something else. Every directory and layout
  link between a writable root and a destination therefore refuses unlink (so a rename away) and
  create (so a replacement). Only those two operations, never `file-write*`, so the agent can still
  chmod its state directory and add files to it.
- **What is protected is what was delivered.** The list is the one `buildMacosHomeOverlay` WROTE,
  carried with the tree as one `macosuser.HomeOverlay` — the same list it wrote beside the tree for
  the bootstrap's install ([HT-D7](#ht-d7)). A launch that delivered nothing gets a
  profile byte-identical to one that never had the rule.
- **The copy step is untouched.** The bootstrap's argv carries no `sandbox-exec`, so the next
  launch replaces the delivered trees exactly as it did before.

### Nothing is delivered through a link the layout did not lay

Found by review on 2026-09-27, the day the rules shipped, and fixed the same day. The sidecar is
inside the workspace, which every session can write, so an earlier session whose profile did not
cover these paths — a `packs: []` launch, or one with another pack selection — could leave a
symbolic link in it. The layout's `MkdirAll` and the overlay install both followed one, so the
delivered skills and briefing landed wherever it pointed, where no rule names them, and a link at
a skills destination MERGED into its target, so a skill the agent had planted there was loaded
beside the delivered ones. Reproduced on Linux against the real deriver and install. On the container
backends the delivered tree is a `:ro` bind whose SOURCE is the launcher's staging directory, not
the sidecar, so a planted link cannot change what the bind delivers.

| Where the link is | What the bootstrap does |
| :--- | :--- |
| `<ws>/.yolo`, `<ws>/.yolo/home`, or any directory the layout lays in the sidecar (`<sidecar>/claude`, `<sidecar>/local`, …) | **Refuses**, in BOTH the layout step and the overlay step, because every boot step runs after one fails and `~/.claude` may still be the right link from an earlier launch. The refusal (`entrypoint.LinkedSidecarError`) names each link and its target and offers `sudo rm <link>`, which removes the link and never what it points at; nothing is removed for you, because the target may hold the agent's real history |
| Past a layout link, at a destination or a briefing (`<sidecar>/claude/skills`, `<sidecar>/claude/CLAUDE.md`) | **Replaces** it, like any other occupant of a destination: the link is moved aside and unlinked by the same renames that replace a previous delivery, never its target, and a fresh copy or a regular file is laid in its place. The layout writes no links there, so this destroys nothing yolo made |
| Past a layout link, ABOVE a destination (`<sidecar>/pi/agent` on the way to `~/.pi/agent/skills`) | **Refuses** that destination, naming the link and `sudo rm <link>`. Following it lands where no rule names; replacing it would replace a directory above a destination, which is the one thing the install never does ([G36](#the-overlay-replaces-its-destinations-and-nothing-else), [HT-D8](#ht-d8)) |
| In the account home, where this launch lays no layout link (`~/.claude` on a launch whose packs do not declare it) | **Refuses** the delivery, naming the link and `sudo rm <link>`. It is typically another workspace's layout link, which the layout deliberately leaves alone (removing it would strand a live sidecar, and a real directory there is refused forever by [OQ-HT2](#oq-ht2)); following it wrote this launch's content into that workspace's sidecar |
| A layout path that is not yet the layout's link (a pre-layout real `~/.claude` the layout just refused) | **Left alone.** The install used to replace it wholesale, deleting the transcripts the refusal had just told the reader to move out first |
| Past `~/.config`, on the way to the git config `~/.gitconfig` redirects to (`<sidecar>/config/git`, or the file `<sidecar>/config/git/config` itself) | **Refuses** the git step: no identity and no `safe.directory` entry are written, and the warning names the link and `sudo rm <link>`. `git config --global` follows every link on its way to the file, the file included, and it runs outside Seatbelt, so a link there once carried the identity and the `safe.directory` entry into another workspace's `.git/config`. The path is walked through the layout's own links first (`DarwinHomeLayout.homeFileThroughLayout`), and git is handed the physical file the walk reached. A link swapped in between that walk and git's own write, by a session of the same workspace running at the time, is not covered |
| On the way to a `host_files` destination: for a home-root entry `<sidecar>/config/yolo-home` or the file itself, and for any entry anything below a layout link or in the account home | **Refuses** the host_files step, which is fatal like any entry that cannot be staged: nothing is written, and the refusal names the link and `sudo rm <link>`. The composition engine writes by path and follows every link, and the step runs outside Seatbelt, so a link the agent left below `~/.config` once carried the write into another workspace's `.git` (measured on Linux against the real bootstrap, 2026-10-04). The file is written at the physical path the walk reached ([HT-D10](#ht-d10)). The links the selected packs' hooks lay are not refused: the walk follows each one while it points at its hook's target, so an entry at `~/.claude/.credentials.json` is written into the shared credential, as on podman ([HT-D14](#ht-d14)). ⚠ A link swapped in between the walk and the write is not covered, and who can make that swap depends on where the file is. In the workspace sidecar, as git's is, only a session of the same workspace can. In the account home — a new top-level directory such as `~/.aws/config`, `~/.cache`, a machine-scope shared directory, or the shared file a hook's link leads to — a session of **any** workspace on the Mac can, because every session's profile allows writes to the whole account home, and the unconfined write would then follow it into a directory that session cannot reach. Writing through a handle opened beneath the home or the sidecar, as the overlay install does, would close both; it is not built, because the composition engine takes a path |

So after a bootstrap that did not fail, every component from the workspace down to each
destination is a real directory or a layout link pointing where the layout laid it, and the host's
text join is the kernel's path. `TestTheBootstrapDeliversOnlyWhereTheHostsRulesPoint`
(`internal/macosuser`) pins the two ends together: it plants a link at each position between two
launches, drives the real layout and overlay steps, and requires every file carrying the second
launch's content to sit under a denied path with every directory above it anchored.

> [!NOTE]
> **What a path rule cannot cover is a CONCURRENT session.** The delivered copies live in the
> sidecar, and each session's profile protects only what that session delivered. A second session
> on the same workspace whose selection does not include the pack can write them while the first
> runs; the next launch replaces them. On podman the delivered tree is a bind from the launcher's
> staging directory, outside the workspace, so no session can. Reasoned, not measured.

The launch no longer prints *"briefings and skills are delivered by COPY on macos-user … the agent
can edit its own skills"*. In [`backend-parity.md`](../design/backend-parity.md)'s vocabulary the
disposition moved from **Warned** to **HonoredBy**: the container's `:ro`, done by the policy
instead of a mount, which [OQ-HT4](#oq-ht4) names as the target shape for this backend.

> [!WARNING]
> **The kernel's refusal is not measured yet.** Linux pins the text of both rules, their position,
> and that every shipped pack's destinations are covered and its state directories are not. Two
> Mac tests settle whether the kernel refuses: the policy suite's `home_content_*` cases in
> [`macosuserseatbelt_test.go`](../../integration/macosuserseatbelt_test.go), and a real launch in
> [`macosusercontent_test.go`](../../integration/macosusercontent_test.go). Neither has run.
> Hardware measured the subpath shape alone (a `touch` refused, `claude --version` unaffected;
> [`setup-support-gaps.md` §5.1](../plans/setup-support-gaps.md#51-what-is-now-measured) row 14).
> Three things are open, and the first run will answer the first two:
>
> - **Which operation `rename(2)` is checked as.** Nothing documents it. The anchor rule names both
>   `file-write-unlink` and `file-write-create` so that refusing either the move or the replacement
>   is enough.
> - **Whether an agent state directory survives the anchor at startup.** Measured only for the
>   subpath rule. An agent that deletes and re-creates its own state directory would now fail.
> - **Hard links.** Whether Seatbelt refuses `link(2)` of a denied file, and which name a later write
>   through the second link is judged by, are both unmeasured, and a link that succeeds would be a
>   way around a path rule. The rule was NOT widened to guess at a link operation: a profile naming
>   an operation the kernel does not know fails to load, and that would stop every launch.

## No migration: an occupied path refuses the launch

`ensureLayoutSymlink` never removes a real file or directory — that is [OQ-HT2](#oq-ht2) as code.
A symlink yolo itself wrote IS replaced, because the sidecar it named moved; anything else makes
`Apply` collect the path and refuse. (A symlink where the layout lays a real directory in the
sidecar is a separate refusal, checked before anything is created and reported on its own —
[above](#nothing-is-delivered-through-a-link-the-layout-did-not-lay).) Every occupied path is reported **together**, because one at
a time would make the first launch after this shipped a sequence of refusals about an account the
reader is being told to wipe anyway.

> [!WARNING]
> **The refusal is in TWO GROUPS, because they have two different remedies, and a merged message
> is an actionable-looking lie.** A `Link` or a `FileRedirect` is a path in the ACCOUNT HOME, so
> `sudo rm -rf /Users/_yolojail` reaches it. A `Mirror` is a path in the WORKSPACE SIDECAR, which
> that command does not touch — prescribing it there sends the reader to destroy the machine tier
> the mirror exists to preserve **and** get the identical refusal next launch. `occupiedLayoutError`
> names the account reset beside the sidecar group anyway, because *"the reset does not touch
> these"* is the sentence that stops somebody running it from memory; naming a command and
> prescribing it are different acts.
>
> An occupied mirror is reachable without any mistake: Apple Container mounted no shared dirs
> until 2026-08-24 and bound `/home/agent` straight at the workspace state dir, so an AC jail from
> before then left a real `.claude-shared-credentials` in that workspace's sidecar.

**A home-root `host_files` path is a third group, with a third remedy.** Until 2026-10-04 this
backend rendered home-root files such as `~/.npmrc` as real files in the account home, so the
first launch that lays the link finds that file where the link belongs. It refuses, like every occupied path,
but its remedy is `sudo rm <that file>`, not the account reset: the file is almost always the copy
an older launch rendered, which the per-workspace file replaces, and resetting the account would
cost every workspace's machine tier for it ([HT-D11](#ht-d11)). Whether the layout should replace
such a file itself, for the modes that keep no edits, is open ([OQ-HT5](#oq-ht5)).

The full reset, for an account that predates the layout, is `sudo rm -rf /Users/_yolojail && yolo
macos-setup`. It is safe to follow; it was not until 2026-09-12, when `rm -rf` took the home and
left the dscl record while every home-provisioning step lived in `macos-setup`'s account-CREATION
branch — so setup did nothing, printed *"✓ macos-user backend ready"*, and the NEXT LAUNCH built
the whole native closure before failing twenty generators at once on `mkdir /Users/_yolojail:
permission denied`.

## What is still shared, and what that costs

The layout closes the cross-workspace content race for everything it links — every shipped
briefing and skills destination is under a pack `state` dir or `.config`, so all of them are
per-workspace now, and `noteMachineWideWorkspaceState` was retired along with the defect it named.
What is still one-per-machine, each deliberate or named:

- **The home ROOT is shared, so the login rc files are.** `.zprofile`, `.zshrc` and
  `.bash_profile` sit below every symlink the layout lays and are read by the shell from `$HOME`.
  `WriteLoginRC` therefore writes an **indirection** rather than a value: it re-prepends
  `$YOLO_DARWIN_LOGIN_PATH`, which the launch exports from the same `SandboxPath` call that builds
  `PATH`. A literal there would be one workspace's `packages:` store dirs in the next workspace's
  login shell — the same race the sidecar closed for briefings, in three files nobody would look
  at. Unset (a shell yolo did not launch) leaves `PATH` alone. So a `host_files` entry naming one
  of the three gets no per-workspace link ([HT-D12](#ht-d12)) and is **refused** on this backend,
  naming the next step ([HT-D13](#ht-d13)). Until 2026-10-04 the step wrote it and `WriteLoginRC`
  replaced it later in the same boot, in every mode, with no warning; a `readonly` one left the
  shared file 0444, which the account that owns it cannot open for writing, so every workspace's
  launch would fail at `write_login_rc` (the mode measured on Linux against the real bootstrap,
  and the refused open measured on Linux as a non-root owner; not run on a Mac). Config validation does not know the rule, so `yolo check` passes such an entry and
  the launch refuses it. The fuller answer, not built because it changes `WriteLoginRC`, is to
  stage the entry at `.config/yolo-home/<slug>` like any other home-root entry and have the rc
  file source that copy after its `PATH` line: per-workspace, as podman's is, and no refusal.
- **The account home holds ONE link set, so a second workspace's launch is refused while a session
  holds it.** A launch in a
  different workspace repoints it, which is correct for sequential use and self-healing
  (`ensureLayoutSymlink` repoints a link yolo wrote). Laid while another workspace's session ran,
  it took five of that session's core links (`.npm-global`, `.local`, `go`, `.yolo/bin`, `.config`)
  whatever packs either selected (MEASURED on Linux,
  `TestASecondWorkspaceLayoutRepointsTheFirstsLinks`), and the session's `~/.claude` would then
  name a directory its own profile denies reading (reasoned). So such a launch is **refused** while
  the session runs, before its nix build and before any `sudo` (the hold is asked ahead of the
  context-mount preflight, whose probes may prompt), naming the live workspace and the
  two ways forward: quit that session, or a container runtime for the project
  ([HT-D15](#ht-d15)); whether two workspaces may ever run at once is [OQ-HT6](#oq-ht6). Two
  sessions of ONE workspace are still admitted ([OQ-HT3](#oq-ht3)). The per-workspace launch lock
  never covered this: `run.AcquireWorkspaceLockFor` is keyed per workspace and released before
  the agent starts. The hold's known limits: a SIGKILLed host `yolo` drops it while the session it
  started may run on, since nothing running as the sandbox account can hold a host lock; and it is
  kept per invoking macOS user, so a second macOS user's launch on the same Mac does not see it. The
  home-root `host_files` links resolve through `~/.config`, so they inherit this rule and add
  none.
- **A `host_files` file in a new top-level directory (`~/.aws/config`) is still one per machine.**
  podman stages that directory as a writable subtree of the workspace (`writable_home_dirs`'
  recipe); this layout has no link for it, so the file is rendered into the account home every
  workspace shares, and the last launch's copy is what every session reads. Home-root files are
  per-workspace since 2026-10-04 ([HT-D9](#ht-d9)); this is the half that is not.
- **A workspace that does not declare a home-root entry still has the link another one left.**
  It dangles there, as the file is absent from a podman jail that does not declare it, and a
  program writing the file — `npm config set` with no `~/.npmrc` entry — fails `No such file or
  directory` unless that workspace's `<sidecar>/config/yolo-home` already exists, where podman's
  read-only home fails the same write. Reasoned, not measured.
- **`yolo stop` has nothing to stop and there is no attach.** Every invocation is a fresh sandbox
  (`internal/cli/stop.go`), so two launches on one workspace really do run two bootstraps and two
  provisioning stages. The workspace lock covers that window — bootstrap through stage, since the
  bootstrap generates the very script the stage execs into the same sidecar — and, since
  2026-09-26, everything from pack staging on
  ([concurrent launches](pack-system.md#concurrent-launches-of-one-workspace)). A lock that cannot
  be taken warns and degrades rather than refusing the launch.
- **The overlay copy was agent-writable where a bind is `:ro`**, and said so on every launch, until
  2026-09-27. It is write-protected by the profile now
  ([above](#the-staged-skills-and-briefings-are-write-protected-at-the-path-the-kernel-sees)), and the
  line is retired. The kernel honors the rule (MEASURED 2026-09-28 on a Mac, [run 36437881715](https://github.com/mschulkind-oss/yolo-jail/actions/runs/36437881715)); what survives is
  the hard-link question that warning lists.

> [!CAUTION]
> **A transient `LoadJailPacks` failure permanently poisons the account home — open, and it must
> never be automated.** The link set is derived from the loaded packs, and a load error does not
> abort the bootstrap (A12: every step still runs), so the layout lays an EMPTY link set,
> `install_home_overlay` creates a REAL `~/.claude`, and every later launch refuses forever with a
> remedy that destroys the machine tier the shared-credentials hook exists to preserve. Narrowed
> on 2026-09-27: where an earlier launch had laid `~/.claude`, the install now refuses to deliver
> through a link this launch did not lay instead of writing over it, so the route needs a failure
> on the account's first launch of that pack, while `~/.claude` does not exist yet. Reaching
> it needs a fault injected into `LoadJailPacks` — a seam that does not exist — and no CI job
> should be able to arrive at that state on a Mac that belongs to somebody
> ([runbook item 10](../plans/runbooks/macos-user-manual-checks.md#10-the-two-layout-defects-a-mutation-pass-found--new-2026-09-12-never-run)).
> If you ever see that refusal on an account nobody touched, this is the likely route.

> [!NOTE]
> **Running the twins leaves the account home holding dangling links**, because each test deletes
> the workspace its launch pointed at. That is inherent to a one-account backend, not a defect,
> and it is the same state a human gets by deleting the workspace they last launched. It is also
> why no `macos-user` test may run concurrently with another — the package's no-`t.Parallel()`
> rule is what holds that. The suite is not idempotent about it
> ([`handoff-macos-user-open-threads.md`](../plans/handoff-macos-user-open-threads.md#2-the-twin-suite-poisons-itself-in-test-order--a-persistent-mac-only)).

## What is measured, and by what

The layout is Linux-written code for a backend that cannot run on Linux, so the instrument matters
more than usual.

| Claim | Instrument |
| :--- | :--- |
| Six account-home paths are symlinks into **this** workspace's sidecar; `.claude-shared-credentials` is a real directory with the sidecar mirror pointing back; the credential link is still relative | **Hardware, 2026-09-12** — Apple Silicon, macOS 26.5 arm64, [runbook item 5](../plans/runbooks/macos-user-manual-checks.md#5-the-per-workspace-home-layout--new-2026-09-12-never-run), read from the host |
| The relative chain RESOLVES from inside a loaded Seatbelt profile, and a second workspace repoints the account home **without erasing** the first's state | `integration/TestMacosUserHomeTierIsPerWorkspace`, two launches, a probe file rather than `.credentials.json`; green on hardware 2026-09-12 and nightly on `macos-latest` since |
| The sandbox uid can create `<workspace>/.yolo/home` through the shared-group ACL | implied by both rows above — the links point into it and the probe wrote through it |
| `MISE_DATA_DIR` names a real machine-tier store that mise populates | **Hardware, 2026-09-12** — [runbook item 9](../plans/runbooks/macos-user-manual-checks.md#9-mise_tools-actually-arrive--new-2026-09-12-never-run): two tools installed under `/Users/_yolojail/.yolo/mise/installs`, a real directory, while its sibling `~/.yolo/bin` is a symlink |
| The login-rc re-prepend still beats `path_helper` now that its value arrives by variable | **Hardware, 2026-09-12** — [runbook item 3](../plans/runbooks/macos-user-manual-checks.md#3-the-acceptance-bar--packages-reaches-the-agent) re-run, `fzf` resolving into the store profile with Homebrew's copy present |
| An occupied MIRROR refuses the launch, names a remedy that reaches it, and does not delete the directory it declined to migrate | `integration/TestMacosUserLayoutRefusesAnOccupiedSidecarMirror` (hardware + nightly); the message half by `TestDarwinHomeLayoutRefusalPrescribesARemedyThatReachesTheOccupiedPath` on Linux |
| Seatbelt judges a symlink's TARGET | **Hardware, 2026-09-13** — `sandbox-exec` probe, both spellings denied, three controls behaving |
| `..` resolves physically on darwin as on Linux | **Hardware, 2026-09-11** — the fixture rebuilt on macOS 26.5 |
| The boot ordering, and a credential resolving through the layout the bootstrap just laid | Linux unit gate against a real filesystem, driving `RunDarwinBootstrap` itself (`internal/entrypoint/darwinhomelayout_test.go`) |
| The tier SOURCING, and the refusal's two groups | the same gate through `InstallDarwinHomeLayout` — the real boot entry rather than `Apply` directly, so the tier lists come from a pack manifest and the refusal is one a launch would really print |
| The deriver, idempotence and the repointing, the stale-link pair | the same gate calling `DeriveDarwinHomeLayout(…).Apply()` directly |
| The probe script and its parser that the Mac test depends on | `TestMacosUserHomeTierProbeReadsARealLayout`, a Linux preflight applying the REAL deriver |
| The staged skills and briefings are write-protected at the physical path, the chain above them is anchored, and every shipped pack's state directories are not denied | Linux unit gates on the rendered SBPL: `internal/macosuser/homereadonly_test.go` (including a symlinked base), and `TestEveryShippedDestinationIsWriteProtectedAndNothingElse`, which enumerates the shipped packs' declarations |
| A link the layout did not lay, at each position in the sidecar and in the account home, is refused or replaced and never followed, and every file a launch delivers lands under a path its profile denies | Linux unit gates driving the real bootstrap: `internal/entrypoint/darwinoverlaylinks_test.go`, and `TestTheBootstrapDeliversOnlyWhereTheHostsRulesPoint` in `internal/macosuser`, which checks the delivery against `ResolveHomeReadonly`'s own output; the install-level cases, a link at and above a listed destination, in `internal/entrypoint/darwinoverlay_test.go` |
| The profile protects exactly the destinations the install replaces | `TestHomeOverlayReturnsTheDestinationsItWrote` reads the written list file back against `Dests`, and `TestWorkspaceSkillsReachTheMacosUserHome` requires a mirrored workspace skill's destination in both |
| The kernel REFUSES writes, renames, deletes and a planted skill, and allows the agent's own state | **MEASURED 2026-09-28 on a Mac** ([run 36437881715](https://github.com/mschulkind-oss/yolo-jail/actions/runs/36437881715), commit `650e84b0`): every `home_content_*` case of `TestMacosUserSeatbeltProfileEnforcesItsRules` passed (write, rename, delete, planted skill, briefing write and anchor replace refused; the state dir and the agent's own state usable), and so did the real launch, `TestMacosUserStagedContentIsWriteProtected`. Their scripts' bare halves are also exercised on Linux (`TestMacosUserSeatbeltContentControlsRunUnsandboxed`, `TestMacosUserContentProbeReadsARealLayout`) |
| An occupied ACCOUNT-HOME path refuses | Linux only (`TestDarwinHomeLayoutRefusesToReplaceRealDirectories`); not separately exercised on hardware |
| A home-root `host_files` file is per-workspace across two workspaces in `once`, `copy` and `readonly`; a launch that does not declare it leaves the link; a real file there refuses with `sudo rm` of that file; no host_files write follows a link yolo did not lay; an entry at or below a pack hook's link is written where that link leads, on every launch, while a link there to anything else is refused; and an entry naming a login rc file is refused with its next step and leaves every workspace bootable | Linux unit gate driving `RunDarwinBootstrap` (`internal/entrypoint/hostfileredirect_test.go`; the rc case is `TestALoginRCHostFileEntryIsRefusedAndLeavesEveryWorkspaceBootable`, with `TestDarwinLoginRCFilesAreTheFilesWriteLoginRCWrites` holding the list to what `WriteLoginRC` writes; the hook links are `TestAHostFileAtOrBelowAPackHooksLinkIsWrittenThroughIt`, with `TestPackHookLinksAreTheLinksTheHooksLay` holding `packHookLinks` to the links every shipped pack's hooks lay), and `run.TestTheSkeletonsHostFileLinksAreTheMacosUserLayouts` comparing the two backends' links. The Mac half, a sandboxed session reading its own workspace's file through both links, is `integration/TestMacosUserHomeRootHostFilesArePerWorkspace`: **written 2026-10-04 and not yet run on hardware or in the nightly**. Whether npm rewrites `~/.npmrc` in place through the link, rather than replacing the link with a file, is **not measured** |
| The cache relocations' links are laid, replace a link, refuse a real directory (naming the copy and the removal) and a linked `~/.cache`, and only a recorded link is removed; a launch relocating nothing boots with a linked or file `~/.cache` and writes nothing through it | Linux unit gates against a real filesystem, the first and the last through `RunDarwinBootstrap` itself (`TestDarwinBootstrapLaysEachCacheRelocationLink`, the `TestTheCacheRelocationStep…` set and `TestNoRelocationIgnoresALinkedOrFileCacheDir` in `internal/entrypoint/darwinhomelayout_test.go`) |
| A sandbox write through the link lands at a `/Users/Shared` target, which you can delete afterwards; a populated target without the sandbox's access refuses before the nix build | `integration/TestMacosUserCacheRelocationIsWrittenThereAndStaysYours`, `…RefusesAPopulatedTargetWithoutAccess`, `TestMacosUserSaysResourcesAreIgnoredAndRelocatesTheCache`. **Written 2026-10-05, not yet run on a Mac** |
| A second workspace's layout repoints five of the first's core links, whatever packs either selects | Linux unit gate against a real filesystem (`TestASecondWorkspaceLayoutRepointsTheFirstsLinks` in `internal/entrypoint/darwinhomelayout_test.go`) |
| A second workspace's launch is refused while a session holds the account home, before any build or `sudo`, leaves the links as they were, and launches once the session ends | `integration/TestMacosUserASecondWorkspaceIsRefusedWhileASessionHoldsTheAccountHome`. **Written 2026-10-05 and not yet run on hardware or in the nightly**; the hold's flocks, its refusals and its unknown-is-live rule by Linux unit gates (`internal/cli/run/accounthomehold_test.go`), its call before the nix build by `TestTheAccountHomeHoldGatesTheBuildAndSpansTheSession`, and before the context preflight's `sudo` by `TestARefusedHoldRunsNoContextPreflight` |
| What the first session would have seen, had the second been admitted (`~/.claude` naming a directory its profile denies) | **Not measured** — reasoned from the repointing above plus target evaluation |
| The pack-load poisoning route | **Not measured, deliberately, and must stay that way** (see the caution above) |
| The overlay install leaves the state beside and above every destination, for every shipped pack | Linux unit gate driving `RunDarwinBootstrap` with each pack's real manifest (`TestDarwinOverlayInstallKeepsAgentStateBesideAndAboveEveryDestination`); the install's own rules by `internal/entrypoint/darwinoverlay_test.go`; the host builder's list read by the real install in `internal/cli/run/macoshomeoverlay_test.go` |
| A directory swapped for a link during the install cannot carry it outside the home and the sidecar | Linux unit gate (`TestOverlayInstallStaysInsideTheJailWhenADirectoryIsSwappedMidInstall`): a test hook makes the swap at each of four points, from just after the layout check passed the path to just before the new copy is swapped into place, and the test fails on the install that checked a path. **Not measured against a real concurrent agent on a Mac** |
| The same on a Mac: the list survives the root-owned staging, and the containment check accepts the real `/Users` paths | `integration/TestMacosUserOverlayInstallKeepsTheAgentStateBesideItsSkills`, two launches with the omp pack. **Written 2026-09-27 and not yet run on hardware or in the nightly** |

The nightly job is [`.github/workflows/macos-user.yml`](../../.github/workflows/macos-user.yml) —
`macos-latest`, no jail image, `-run '^TestMacosUser'`. It sets `YOLO_TEST_MACOS_USER=1`, which
makes a run that executed **zero** of these tests exit non-zero: every one of them skips on every
machine that develops this repo, and a skip reads as a pass.

> [!WARNING]
> **The obvious test for the mirror pins NOTHING**, and one was written and reverted before this
> was understood. A test that stubs a mirroring helper and asserts the link resolves is satisfied
> by giving the **stub** a mirroring body: zero production code changes, and it goes green — the
> *"pins the CALLEE while the CALL SITE is unpinned"* shape `AGENTS.md` names, weaker still
> because the callee is test-local too. So
> `TestDarwinBootstrapLaysTheTierAndTheCredentialResolvesThroughIt` drives `RunDarwinBootstrap`
> against a real filesystem with the REAL claude manifest as its pack root, writes a credential
> into the account home's `scope: machine` directory, and reads it back THROUGH `~/.claude`.
> Deleting the mirror loop fails it; deleting the layout step fails it too.
>
> ⚠ **And do not pre-write a test as a deliberately RED gate.** One was, and it turned `just
> test-fast` and `just done` red for the whole tree — which costs every unrelated commit the
> ability to tell *"I broke something"* from *"the known red"*. Reverted in `efe7282c`.

## Concurrent-workspace choice: background and full options

Filed 2026-10-05 with [HT-D15](#ht-d15), which refuses today. The account home holds one
workspace's links, and every session runs as the one account in it, so a second workspace's
launch would repoint the first session's links under it.

- **(a) Keep refusing** while a session of another workspace holds the home (today), naming it,
  with "quit that session, or use a container runtime for this project".
- **(b) Wait instead of refusing**, as a second launch of one workspace waits for the workspace
  lock, until the other session ends. Nothing is lost, but the second terminal hangs for as long
  as the first session runs, which may be hours.
- **(c) A home per workspace**: `HOME` under the account (say `/Users/_yolojail/w/<cname>`),
  each holding its own links. It ends the contention; it reopens
  [OQ-HT4](#oq-ht4)'s one-`HOME` ruling and the profile's home rules.
- **(d) An account per workspace**, from a pool `yolo macos-setup` makes: the uid split per
  workspace as well, at the cost of provisioning and of every grant naming one account.

## Open questions

- 💬 <a id="oq-ht5"></a>**[`OQ-HT5`](#oq-ht5) — may the layout replace a real home-root file
  where a `host_files` link belongs, for the modes that keep no edits?**

  <!-- vantage: question id=OQ-HT5 leaning="(a): keep refusing in every mode. OQ-HT2's ruling covers the case in its own words, and the remedy is one sudo rm per file, once per account." -->

  Filed 2026-10-04 with [HT-D11](#ht-d11), which refuses today. The file is the copy an older
  launch rendered into the shared account home before [HT-D9](#ht-d9); the first launch after an
  upgrade meets it once per entry.

  - **(a) Keep refusing in every mode**, with `sudo rm` of the file (today).
  - **(b) Replace it for `copy` and `readonly`**, whose every launch overwrote that file anyway, so
    it holds yolo's last render unless something edited it since; keep refusing `once` and
    `capture`, where it may hold the agent's edits.
  - **(c) Move it aside in every mode** (`<name>.pre-workspace-tier`) and lay the link.

  _Leaning:_ **(a).** [OQ-HT2](#oq-ht2)'s ruling — *"Nobody is using it. No transition needed.
  If I need to wipe it first, that's fine."* — covers the case in its own words, and the remedy
  is one command per file, once per account. Replacing the file for two modes is the one to take
  if that refusal turns out to be met often. Moving it aside is the one-shot migration
  [OQ-HT2](#oq-ht2) declined.

  **Answer:**
  > _(empty — fill in when decided)_

- 💬 <a id="oq-ht6"></a>**[`OQ-HT6`](#oq-ht6) — may sessions of two workspaces run at once on
  this backend?**

  <!-- vantage: question id=OQ-HT6 leaning="(a): keep refusing (HT-D15) until someone needs two at once; then (c), a home per workspace, which ends the contention instead of scheduling it." -->

  Choose whether to retain the current refusal or permit concurrent workspaces; see the
  [full unchanged options and trade-offs](#concurrent-workspace-choice-background-and-full-options).

  - **(a) Keep refusing:** explicit remedy, no concurrent workspaces.
  - **(b) Wait:** preserves state, but may hang for hours.
  - **(c) A home per workspace:** ends contention, reopens the one-`HOME` ruling.
  - **(d) An account per workspace:** uid isolation, provisioning and grant costs.

  _Leaning:_ **(a)** until someone needs two at once, then **(c)**: it removes the shared state
  rather than scheduling around it, where (b) turns a clear refusal into a silent hang.

  **Answer:**
  > _(empty — fill in when decided)_

## Why it is this way

Rulings a future change would otherwise re-derive or undo, with the ids source comments and other
documents cite.

| Ruling | Why it holds |
| :--- | :--- |
| <a id="oq-ht1"></a>[**OQ-HT1**](#oq-ht1) — the pack's declared `scope` decides the tier, and this backend honors it rather than re-deciding it | `scope: machine` → `packload.SharedDirs`, `scope: workspace` → `packload.WritableDirs`, and the container mount assembler consumes exactly those two lists. So credentials are machine tier and history and transcripts are workspace tier because `packs/claude` says so, not because this backend chose. A backend that shared a `scope: workspace` dir, or split a `scope: machine` one, would be the feature-detection failure of [OQ-HT4](#oq-ht4) seen from the pack's side. Pinned by `TestTheHomeLayoutsTiersComeFromThePackDeclaration`, which drives the real boot entry with a synthetic pack no hardcoded list could contain — substituting literal slices for the two accessors left the suite fully green. |
| <a id="oq-ht2"></a>[**OQ-HT2**](#oq-ht2) — **no migration; wiping `/Users/_yolojail` is a supported reset** | *"Nobody is using it. No transition needed. If I need to wipe it first, that's fine."* A real file or directory where a link belongs is never removed, renamed or copied: the launch names every offender and the remedy that reaches it. What is given up is real — the old shared home holds the workspace tier for every workspace that ever launched here, and transcripts are user work product where a re-fetchable token is not — and it was accepted on measured grounds: this backend's only session at the time was yolo-generated content. The precedent was already priced in `linkThroughShared`, which accepts losing one login on a layout change. What it buys is that the layout shipped with no one-shot mutation, no `.pre-tiers-<date>` directory and no first-launch copy path. ⚠ Do not reuse the ruling for a different home — for the podman base, which somebody IS using, [`base-home-legacy-state.md`](../design/base-home-legacy-state.md) leaves the legacy bytes in place, unmounted and unread, rather than discarding them ([§2.9](../design/base-home-legacy-state.md#29-backends)). |
| <a id="oq-ht3"></a>[**OQ-HT3**](#oq-ht3) — per-workspace, not per-session | The same-workspace overwrite is **convergent**: two launches on one workspace compose identical content from identical config, packs and briefing, which is what the container's attach already relies on. A per-session tier is a mechanism no other backend has, buying nothing the workspace tier does not, and it would have multiplied [OQ-HT2](#oq-ht2)'s surface by every session ever run. The container's courtesy flock was a moved call rather than a design point, and it moved: `run.AcquireWorkspaceLockFor` exists for this one caller. |
| <a id="oq-ht4"></a>[**OQ-HT4**](#oq-ht4) — `HOME` stays `/Users/_yolojail`; the workspace tier is a symlink layout into the sidecar, and `SharedDirs` stay put and are mirrored back | The constraint that outranks the layout choice is **one mechanism on every backend**: *"it's going to be just identical to how you share them in container jails … otherwise you're just fragmenting the utility of this tool and you can't really share things, because you'd have to detect features and stuff and it would be awful."* A per-workspace `HOME` (the recorded runner-up) reaches credentials some other way, which makes *"where are my credentials"* a per-backend question every pack touching them must feature-detect. What may differ between backends is only the **primitive that enforces the boundary** — a bind mount on podman, an SBPL rule here — because that is invisible to a pack and to a user. In [`backend-parity.md`](../design/backend-parity.md)'s vocabulary the target disposition is **HonoredBy**: the same outcome by a named different primitive, never a different mechanism. A per-workspace `HOME` would also have moved the install prefixes out of reach of the `agent_updates` lock the shared home provides, and overturned [`macos-user-nix-and-features.md`](macos-user-nix-and-features.md)'s standing refusal. |
| <a id="ht-d1"></a>[**HT-D1**](#ht-d1) — *Implementation decision.* The content rules name the PHYSICAL path, derived from the layout deriver, and never from an `EvalSymlinks` of the account home at plan time | The kernel judges the resolved path, and on every shipped pack `~/.<agent>` is a layout link into the sidecar. Resolving the live account home would read a directory the launcher cannot rely on reading, and on a first launch the links do not exist yet. The deriver is the one the bootstrap applies (`DeriveDarwinHomeLayout`), fed the same `packload.WritableDirs`, so the two agree about where each destination lands. They agree about the PATH the kernel reports only while nothing below the two resolved bases is a link the layout did not lay, which is not construction but enforcement: [HT-D5](#ht-d5) (corrected 2026-09-27, when a review showed the first wording claimed more than the code did). The account-home spelling rides along because it is the one that matches wherever no link stands (2026-09-27) |
| <a id="ht-d2"></a>[**HT-D2**](#ht-d2) — *Implementation decision.* Every directory and layout link between a writable root and a destination is an anchor, denied `file-write-create` and `file-write-unlink` on its literal path; the workspace and the account home themselves are not | A path deny does not follow an inode, so without anchors the chain could be moved aside and replaced. `file-write*` would also have frozen the agent's own state directory (chmod, utimes, xattrs), which the container's `:ro` bind never did. The two roots need no anchor: the workspace's parent is outside the writable set, and `/Users` is root-owned (2026-09-27) |
| <a id="ht-d3"></a>[**HT-D3**](#ht-d3) — *Implementation decision.* The protected set is what the overlay builder WROTE, carried with the tree and the layout dirs as one `macosuser.HomeOverlay` | The container binds what it stages. Deriving the rules from a second walk of the declarations would let a destination be protected and not delivered, or the reverse, silently. A launch that delivers nothing renders nothing, so its profile is byte-identical to the one it always got (2026-09-27) |
| <a id="ht-d4"></a>[**HT-D4**](#ht-d4) — *Implementation decision.* The "delivered by COPY … can edit its own skills" launch note is retired, not reworded | It describes a gap that is closed. `HonoredBy` owes the launch no line, and a softened sentence would be one more line readers learn to skip. That the kernel honors the rule is asserted by two Mac tests that have not run, and the [warning above](#the-staged-skills-and-briefings-are-write-protected-at-the-path-the-kernel-sees) says so (2026-09-27) |
| <a id="ht-d5"></a>[**HT-D5**](#ht-d5) — *Implementation decision.* The bootstrap, not the host, keeps the content rules' paths true: it refuses a link in the sidecar, replaces one past a layout link, and refuses one in the account home, rather than the host resolving each full destination at plan time | The host computes the rules before the bootstrap runs, so resolving the full path then names whatever a planted link points at, and the bootstrap's own step would still land somewhere else — the two would disagree in the other direction. Only the step that writes the files can make the path it writes the one the rule names. Refusing in the sidecar, rather than replacing, because the link's target may be the agent's real history; replacing past a layout link, because the layout writes nothing there that could be lost; refusing in the account home, because a link there is typically another workspace's live layout link (2026-09-27) |
| <a id="ht-d6"></a>[**HT-D6**](#ht-d6) — *Implementation decision.* The overlay install derives the SAME layout the layout step laid (`darwinHomeLayoutFor`) and follows a link only when its path and its target are both the layout's | One derivation for both steps, so they cannot disagree about which links are yolo's. The target is compared because an account-home link can name the right path and another workspace's sidecar. A layout path whose link is not in place is left untouched, since the layout step has already refused it (2026-09-27) |
| <a id="ht-d7"></a>[**HT-D7**](#ht-d7) — *Implementation decision.* ONE destination list: the one `entrypoint.WriteHomeOverlayManifest` writes beside the tree is the one it returns, and `buildMacosHomeOverlay` hands exactly that on as `macosuser.HomeOverlay.Dests`. The profile protects what the install replaces, by construction | G14 carried `Dests` to the profile and G36 wrote a list for the install, both from the same loop, so they already agreed about the set; they could still have disagreed about a rule, since the list file is cleaned, sorted and stripped of nested destinations and `Dests` was not. Returning the written list removes the second copy rather than adding a test that the two match. `Dests` is therefore sorted rather than in the order written, which changes only the order the profile lists its rules in (2026-09-27) |
| <a id="ht-d8"></a>[**HT-D8**](#ht-d8) — *Implementation decision.* Past a layout link, a link ABOVE a destination is refused; a link AT one is replaced | G14's tree walk replaced the first path past a layout link whatever it was, and for pi that path was `~/.pi/agent`, a directory above the destination — the G36 shape. The list-driven install replaces only a listed destination, so a link above one cannot be replaced without replacing that directory, and following it would land where no content rule names. It is refused with the same `sudo rm` remedy as a link in the account home. A link AT a destination is still replaced, because the destination is what the install replaces anyway (2026-09-27) |
| <a id="ht-d9"></a>[**HT-D9**](#ht-d9) — *Implementation decision, taken under the maintainer's 2026-10-04 delegation ("make them and build it … adjust later"); reversible.* A home-root `host_files` file is a layout link in the account home with podman's own target, chosen by the same `StagingFor` call, and derived in `darwinHomeLayoutFor` from `YOLO_HOST_FILES` | One deciding call and one target on both backends, so `~/.npmrc` is per-workspace on both (`paths.HomeFileRedirects`' rule for core's three files, applied to config). The link is the same relative string for every workspace and resolves through `~/.config`, which every launch repoints, so it needs no repointing and no launch line of its own. A launch that does not declare the entry leaves it dangling, as the file is absent from a podman jail that does not declare it: the layout manages only what this launch declares ([P2](#p2)), and removing the link would let a real file appear there that the next declaring launch refuses ([HT-D11](#ht-d11)). A running session of a workspace that declares it loses the file at that launch either way, since the launch repoints `~/.config` at its own sidecar; that is the concurrency limit the link inherits from `~/.config` ([what is still shared](#what-is-still-shared-and-what-that-costs)), and keeping the link does not change it (since [HT-D15](#ht-d15) that launch is refused while the session runs). It is a group of its own rather than more `FileRedirects`, for the refusal's remedy ([HT-D11](#ht-d11)), and the layout does not create the directory it names, so the unconfined layout step never follows a link planted there ([HT-D10](#ht-d10)) (2026-10-04) |
| <a id="ht-d10"></a>[**HT-D10**](#ht-d10) — *Implementation decision, taken under the maintainer's 2026-10-04 delegation ("make them and build it … adjust later"); reversible.* Every `host_files` file on this backend is written at the physical path `homeFileThroughLayout` reaches, not only one past a layout link | The composition engine writes by path, and its `MkdirAll`, truncating write and `readonly` chmod each follow every link they meet, so a link planted below `~/.config` carried the write into another workspace (measured). Walking every destination keeps one rule — a link the layout did not lay is somebody else's, in the account home as in the sidecar — and costs nothing the layout lays. One cost, stated: for a `capture` entry past a layout link the boot's capture notice names the physical file rather than `~/<path>`. A second, that a destination at or below a pack hook's link was refused too, is lifted by [HT-D14](#ht-d14) (2026-10-05). The packs are loaded by the step itself, as other generators load them, because the boot table hands it none (2026-10-04) |
| <a id="ht-d11"></a>[**HT-D11**](#ht-d11) — *Implementation decision, taken under the maintainer's 2026-10-04 delegation ("make them and build it … adjust later"); reversible.* A real file where a home-root `host_files` link belongs refuses the launch in its own group, with `sudo rm` of that one file as the remedy | [OQ-HT2](#oq-ht2)'s no-migration rule, with a remedy that reaches the path and no further: the file is almost always the copy an older launch rendered into the shared home, and the account reset would cost every workspace's machine tier for it. A directory there is offered `sudo rm -rf` of that directory. The host_files step then refuses too, rather than writing the shared file the layout refused (2026-10-04) |
| <a id="ht-d12"></a>[**HT-D12**](#ht-d12) — *Implementation decision, taken under the maintainer's 2026-10-04 delegation ("make them and build it … adjust later"); reversible.* No home-root `host_files` link is laid at a file the bootstrap writes by path on every launch, `WriteLoginRC`'s `.zprofile`, `.zshrc` and `.bash_profile` (`entrypoint.DarwinLoginRCFiles`); they stay real account-home files, as before [HT-D9](#ht-d9) | A link there, laid by the one workspace that declares the entry, is left by every other launch ([P2](#p2)), and `WriteLoginRC` followed it into the sidecar of whichever workspace launched next. There `.config/yolo-home` need not exist, so a workspace that declared nothing failed its launch at `write_login_rc` with `ENOENT` (found in review, 2026-10-04). It is the one home-root entry the two backends link differently; podman's skeleton still links it, since the container boot does not write the file. What the host_files step does with such an entry is [HT-D13](#ht-d13) (2026-10-04) |
| <a id="ht-d13"></a>[**HT-D13**](#ht-d13) — *Implementation decision, taken under the maintainer's 2026-10-04 delegation ("make them and build it … adjust later"); reversible.* Where the layout is laid, the host_files step refuses an entry naming one of `DarwinLoginRCFiles`, fatally, with the next step: remove it from `host_files`, and for zsh declare `~/.zshenv`, which zsh reads first and yolo does not write | The entry cannot be delivered: `WriteLoginRC` replaces it later in the same boot, so it never reached a shell, and a `readonly` one left the shared file 0444, which would fail every workspace's `write_login_rc` on the Mac. A host_files entry that cannot be delivered is an error, not a warning, by the ruling that a failed config generator refuses the boot ([`A12` in `jail-home.md`](jail-home.md#why-its-this-way)). A refusal is the stopgap: sourcing a per-workspace copy from the rc file would deliver it ([what is still shared](#what-is-still-shared-and-what-that-costs)) and lift this. A launch that declared such an entry and came up with it silently replaced now stops (2026-10-04) |
| <a id="ht-d14"></a>[**HT-D14**](#ht-d14) — *Implementation decision, taken under the maintainer's 2026-10-04 delegation ("make them and build it … adjust later"); reversible.* The host_files walk follows the links the selected packs' hooks lay — claude's shared credential and per-workspace history, agy's shared credential, pi's shared npm store — each only while it points at the target its hook computes (`packHookLinks`), and walks that target by the same rules, the sidecar's mirror included | [HT-D10](#ht-d10) refused them as links the layout did not lay, and named `sudo rm <link>`; the hook runs before the host_files step and laid the link again on every launch, so a valid config (validation reserves none of those paths) was refused forever, where podman, and this backend before HT-D10, wrote through the link. Following only the hook's own target keeps HT-D10's guarantee: a link at that path to anywhere else is still refused, and so is a mirror that is not the layout's link or a link in place of the account home's shared directory. The targets are restated in `darwinhomelayout.go` because the hooks' file was outside the change; `TestPackHookLinksAreTheLinksTheHooksLay` runs every shipped pack's hooks and fails when the two disagree. A `yolo check` finding for such an entry was the alternative, and is not needed once the entry works (found in review; decided 2026-10-05) |
| <a id="ht-d15"></a>[**HT-D15**](#ht-d15) — *Implementation decision, taken under the maintainer's 2026-10-04 delegation ("make them and build it … adjust later"); reversible.* A macos-user launch takes a hold on the account home for its session, and one of ANOTHER workspace is refused while a session holds it: before the nix build and before any `sudo` (ahead of the context-mount preflight), naming the live workspace, with "quit that session" and "a container runtime for this project" as the next steps, and no override. The hold is a `LOCK_SH` per workspace under `<global storage>/locks/macos-user-home/`, taken under a bounded `.mutex` while the others are probed with `LOCK_EX|LOCK_NB`; an answer the probe cannot read counts as live, and no hold file is ever unlinked | In [OQ-JL7](../design/jail-lifetime-last-session-wins.md#OQ-JL7)'s direction — an arrival that cannot be served safely is refused — and for the reason the bullet under [What is still shared](#what-is-still-shared-and-what-that-costs) measures: the second layout repoints the running session's links. Shared per workspace because two sessions of one workspace lay identical links ([OQ-HT3](#oq-ht3)). "Could not count" counts as live and the files outlive their holders for the keeper's rules ([JL-P3](../design/jail-lifetime-last-session-wins.md#JL-P3), [JL-D28](../design/jail-lifetime-last-session-wins.md#JL-D28)). Asked before the build so a refusal costs seconds, not a half-hour build, and before the context preflight's `sudo -u _yolojail` probes so a refused launch never prompts for a password (the hold itself runs no `sudo`); held by host `yolo` because nothing running as the sandbox account can hold a host lock, which is also its first known limit (a SIGKILLed launcher drops it). The second: it is the invoking macOS user's, under that user's state dir as the session records are, so another macOS user's launch on the same Mac is not seen. Whether two workspaces may ever run at once is [OQ-HT6](#oq-ht6) |
| <a id="ht-d16"></a>[**HT-D16**](#ht-d16) — *Implementation decision, taken under the maintainer's 2026-10-04 delegation ("make them and build it … adjust later"); reversible.* A user-scope `cache_relocations` entry is a link at the account home's `~/.cache/<subdir>` to the resolved target, laid by the bootstrap in the layout's own step (`InstallDarwinCacheRelocations`, named by `YOLO_DARWIN_CACHE_RELOCATIONS`), never a name in the context dir. A real file or directory at the link's path refuses the launch, naming a copy into the target as you and `sudo rm -rf` of the old directory; a link there is replaced; `~/.cache` itself must be a real directory, and every write is beneath an `os.Root` on it. A launch that relocates nothing ignores a `~/.cache` that is not a real directory, laying, sweeping and reading nothing (found in review, 2026-10-05). `~/.cache/.yolo-cache-relocations.json` records what was laid, and a later launch removes a recorded link for a subdir no longer relocated only while it still points at the recorded target | The key is the user's, so its link is machine tier, beside the cache it replaces part of, and `~/.cache/<subdir>` is the path every tool already uses, which a `$YOLO_CONTEXT_DIR` name would not be. [OQ-HT2](#oq-ht2)'s no-migration rule decides the occupied case, as for every other layout path: what the sandbox cached there before the relocation was configured is the user's to move. Every session's sandbox may make `~/.cache` a link or a file, so refusing that on a launch with nothing to lay would stop every later launch of every workspace until somebody ran `sudo rm`; with a relocation configured the refusal stands, since laying through the link would put the relocations' links wherever it points. The record exists so a dropped relocation stops, as an unmounted bind does, without the step removing a link somebody else made; it sits in a directory every session's sandbox may write, so it can only make the step remove a link in `~/.cache`, which that sandbox could remove itself. The mechanism, the siting and the probes are [`cache-relocation.md`](../plans/cache-relocation.md#decision-ledger)'s (2026-10-05) |
| <a id="p1"></a>[**P1**](#p1) — a split must restore every tier it breaks, **explicitly** | Colocation is not a mechanism. The machine tier's *backing* works here by accident — the shared dir is a plain directory because there is only one home to put it in — so any change separating the directories has to replace that accident with something stated. A fix that repairs the workspace tier and leaves the machine tier to luck has moved the bug. |
| <a id="p2"></a>[**P2**](#p2) — the tier of a path is what the pack declares, and there is no second list of "which dirs are per-workspace" | The list is the podman mount table, and adding a directory to one without the other is the drift this layout exists to end. Applied to home-root files it also decides the redirect rule: the layout manages what THIS launch declares. |
| <a id="retraction"></a>[**Retracted**](#retraction) — *"the single home IS this backend's shared-credentials mechanism"* | It stood in this doc's first draft, in `run.go`, in `seatbeltcapture.go`, in the backend reference and in the roadmap, and it is wrong in the one way that mattered: the mechanism is the `shared_credentials` **hook**, which runs on every backend, and the home only ever supplied the *backing* of the pack-declared `scope: machine` directory. A carve-out whose stated reason was wrong survived for months because the reason sounded structural ([`declaration-parity.md` DP-D15](../design/declaration-parity.md#7-ruled-divergent-and-the-ones-i-would-re-open)). What actually refuses a per-workspace home is parity. |
| <a id="history-sharing"></a>[**Not reopened**](#history-sharing) — cross-workspace agent history is not a feature anybody asked for | *"Cross-workspace agent history is a thing some people want"* is unsourced and the tree contradicts it: every doc treats history isolation as the intent (`per_jail_history` is *"belt and braces"* in [`jail-home.md`](jail-home.md)) and nothing records a request to share it. A shared-history feature would be a pack-scope or config change on **every** backend, which is [P2](#p2) seen from the other side — not a macos-user question. |

## Current values

Every row names where its value is defined, so a row is checkable against that file rather than
against the stamp at the top.

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| Sandbox account, and its home | `_yolojail`, `/Users/_yolojail` | `internal/macosuser/macosuser.go` (`SandboxUser`, `SandboxHome`) |
| Neutral workspace root | `/Users/Shared/yolo` | `internal/macosuser/macosuser.go` (`SharedRootDefault`) |
| The workspace sidecar | `<workspace>/.yolo/home` | `internal/paths/paths.go` (`WorkspaceHomeState`) |
| The sidecar's crossing | `YOLO_DARWIN_HOME_SIDECAR`; absent ⇒ lay no layout | `internal/entrypoint/darwinhomelayout.go` (`DarwinHomeSidecarEnv`), set in `internal/macosuser/runplan.go` (`buildBootstrapEnv`), required by `PlanInvariants` |
| Installed-program links | `npm-global→.npm-global`, `local→.local`, `go→go` | `internal/paths/paths.go` (`HomeSurfaces`) |
| The other two links | `yolo-bin→.yolo/bin`, `config→.config` | `internal/entrypoint/darwinhomelayout.go` (`DeriveDarwinHomeLayout`), mirroring `internal/cli/run/assemble_parts.go` |
| Workspace-tier pack dirs | the union of every selected pack's `scope: workspace` state dirs | `internal/packload/packload.go` (`WritableDirs`); declared in `packs/*/pack.json` |
| Machine-tier pack dirs, and their mirrors | the union of every selected pack's `scope: machine` state dirs | `internal/packload/packload.go` (`SharedDirs`); declared in `packs/*/pack.json` |
| Home-root file redirects | `.claude.json`, `.gitconfig`, `.bashrc` — laid only when their holding dir is | `internal/paths/paths.go` (`HomeFileRedirects`) |
| Home-root `host_files` links | `~/<path> → .config/yolo-home/<slug>`, one per home-root file entry this launch declares, laid dangling; none at `.zprofile`, `.zshrc`, `.bash_profile` | `internal/config/hostfiles.go` (`StagingFor`, `SymlinkTarget`), laid by `internal/entrypoint/darwinhomelayout.go` (`WithHostFileRedirects`) |
| mise data dir | `<home>/.yolo/mise`, crossed as `MISE_DATA_DIR`, not overridable from the launch env | `internal/macosuser/macosuser.go` (`SandboxMiseData`) |
| Machine-wide cache | `~/.cache` in the account home — deliberately not linked | `internal/entrypoint/darwinhomelayout.go` (by absence); container analogue `paths.GlobalCache` |
| Cache relocation links | `~/.cache/<subdir> → <target>`, one per user-scope `cache_relocations` entry, crossed as `YOLO_DARWIN_CACHE_RELOCATIONS` (JSON, subdir to target); what was laid is recorded in `~/.cache/.yolo-cache-relocations.json` | `internal/entrypoint/darwinhomelayout.go` (`DarwinCacheRelocationsEnv`, `InstallDarwinCacheRelocations`), set in `internal/macosuser/runplan.go` (`BuildRunPlanWithDaemons`) |
| Login-rc PATH indirection | `YOLO_DARWIN_LOGIN_PATH`, re-prepended in `.zprofile`, `.zshrc`, `.bash_profile` | `internal/entrypoint/darwinhomelayout.go` (`DarwinLoginPathEnv`), `internal/entrypoint/darwin.go` (`WriteLoginRC`) |
| Staged content tree | `/var/yolo-jail/home-overlay/<cname>`, root-owned, named by `YOLO_DARWIN_HOME_OVERLAY` | `internal/macosuser/macosuser.go` (`StagedHomeOverlay`, `StageHomeOverlayCommands`), installed by `internal/entrypoint/darwin.go` (`InstallHomeOverlay`) |
| Content write-protection | `(deny file-write* (subpath …))` over each delivered skills dir and briefing, and `(deny file-write-create file-write-unlink (literal …))` over the chain above each; ids `home-content-write-deny`, `home-content-anchor-deny` | `internal/macosuser/homereadonly.go` (`ResolveHomeReadonly`), `internal/macosuser/seatbelt.go` (`homeReadonlyDenies`) |
| A link where the layout lays a directory | refused before anything is laid, at `<workspace>/.yolo`, the sidecar and every link target's chain; remedy `sudo rm <link>` | `internal/entrypoint/darwinhomelayout.go` (`linkedSidecarPaths`, `LinkedSidecarError`), checked by `Apply` and `internal/entrypoint/darwin.go` (`InstallHomeOverlay`) |
| The content tree's destination list | `.yolo-home-overlay.json` at the tree's root, one `destinations` array of home-relative paths; never copied into the home | `internal/entrypoint/darwinoverlay.go` (`HomeOverlayManifestName`, `WriteHomeOverlayManifest`), written by `internal/cli/run/macoshomeoverlay.go` (`buildMacosHomeOverlayFor`) |
| An install's working copies | `.<name>.yolo-overlay-new` and `.<name>.yolo-overlay-old`, beside each destination, gone once the install finishes | `internal/entrypoint/darwinoverlay.go` (`overlayStagedSuffix`, `overlayAsideSuffix`) |
| Per-workspace launch lock | `<global storage>/locks/<cname>.lock`, held from the content staging (skills, briefings, the overlay and context trees) through the bootstrap and stage, released before the agent; not at pack staging, whose tree is per launch | `internal/cli/run/flock.go` (`AcquireWorkspaceLockFor`), seam `internal/macosuser/orchestrator.go` (`Deps.LockWorkspace`) |
| The supported reset | `sudo rm -rf /Users/_yolojail && yolo macos-setup` | no single site prints both halves: the `rm -rf` by `occupiedLayoutError` (`internal/entrypoint/darwinhomelayout.go`), the reprovision by the missing-home refusal (`internal/macosuser/orchestrator.go`, which is what makes the second half necessary), and the two joined only in [runbook item 5](../plans/runbooks/macos-user-manual-checks.md#5-the-per-workspace-home-layout--new-2026-09-12-never-run) |
