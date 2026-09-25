---
status: current
verified: 2026-09-21
verified_commit: 753bcb88
covers:
  - internal/entrypoint/darwinhomelayout.go
  - internal/entrypoint/darwin.go
  - internal/entrypoint/packhooks.go
  - internal/macosuser/macosuser.go
  - internal/macosuser/runplan.go
  - internal/macosuser/orchestrator.go
  - internal/macosuser/seatbelt.go
  - internal/macosuser/seatbeltcapture.go
  - internal/paths/paths.go
  - internal/packload/packload.go
  - internal/cli/run/assemble.go
  - internal/cli/run/assemble_parts.go
  - internal/cli/run/macoshomeoverlay.go
  - internal/cli/run/backendlimits.go
  - internal/cli/run/loopholeinert.go
  - internal/cli/run/flock.go
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
| Where it runs in the native bootstrap | `internal/entrypoint/darwin.go` (`RunDarwinBootstrap`, its first generator step) |
| The two tier lists it reads | `internal/packload/packload.go` (`WritableDirs`, `SharedDirs`), declared per pack in `packs/*/pack.json` |
| The directory names both backends share | `internal/paths/paths.go` (`HomeSurfaces`, `HomeFileRedirects`, `WorkspaceHomeState`) |
| The shared-tier hooks whose links must keep resolving | `internal/entrypoint/packhooks.go` (`linkSharedCredential`, `linkSharedDirectory`) |
| The account, the mise store, the staged trees | `internal/macosuser/macosuser.go` (`SandboxHome`, `SandboxMiseData`, `StagedHomeOverlay`) |
| The sidecar's crossing, and the refusal when it is absent | `internal/macosuser/runplan.go` (`buildBootstrapEnv`, `PlanInvariants`) |
| The confinement primitive | `internal/macosuser/seatbelt.go` (`SeatbeltProfile`) |
| Content delivery over the layout | `internal/cli/run/macoshomeoverlay.go`, `internal/entrypoint/darwin.go` (`InstallHomeOverlay`) |

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
`packload.SharedDirs`.

| Group | What it is | Contents |
| :--- | :--- | :--- |
| `Dirs` | created first, in the sidecar and in the account home | every link target, plus the real `scope: machine` directory each mirror points at |
| `Links` | the workspace tier — an account-home path pointing INTO the sidecar | `paths.HomeSurfaces()` (`npm-global→.npm-global`, `local→.local`, `go→go`), `yolo-bin→.yolo/bin`, `config→.config`, and each `packload.WritableDirs` entry with its leading `.` trimmed for the sidecar name |
| `Mirrors` | the machine tier as the SIDECAR sees it | `<sidecar>/<dir> → <home>/<dir>` for each `packload.SharedDirs` entry |
| `FileRedirects` | home-ROOT files that are symlinks into a per-workspace directory | `paths.HomeFileRedirects()`, filtered to the ones whose holding directory THIS launch actually laid |

**The list is the container's, not a new one.** Every entry cites the mount it mirrors, and that
is a constraint rather than tidiness: a directory added to the podman mount table and not here
(or the reverse) is a per-backend answer to *"where does my agent's state live"*. The sidecar
targets are spelled **absolutely**, which is not in tension with the relative link a pack hook
writes — these are the launcher's own layout, the "make it appear at this path" half of a bind
done with a symlink, and nothing pack-facing reads them.

**What is deliberately NOT linked stays machine-wide, because the container keeps it machine-wide
too.** `~/.cache` is `paths.GlobalCache()` on podman, and the mise data dir is a machine-wide
store the container mounts at `/mise`. Neither is per-workspace anywhere.

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
by construction: `packs/claude` gets `../.claude-shared-credentials/.credentials.json` and
`packs/pi` gets `../../.pi-shared-npm`. Both hooks run on `macos-user` unchanged —
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
| The layout applies above genStep #1 | it is the first `genStep` in `RunDarwinBootstrap`; only `LoadJailPacks`, which supplies its two tier lists, runs earlier |
| `Dirs` before `Links` | `Apply` walks the fields in declaration order; `MkdirAll` THROUGH a dangling symlink fails (`Stat` misses, `Mkdir` hits `EEXIST`, `Lstat` says "not a directory") |
| The `SharedDirs` mirror before anything RESOLVES one | `Mirrors` is applied in the same step as the `Links`, above every generator |
| `MISE_DATA_DIR` names a path outside the workspace tier | `macosuser.SandboxMiseData` is the one function the launch env, the bootstrap env and the PATH's shims dir all read; `assertOutsideTheWorkspaceTier` asks the deriver |
| `InstallHomeOverlay` must not destroy the layout | `installOverlayTree` **descends through a symlink** and replaces at the first real directory |
| A redirect is laid only when this launch lays the directory that holds it | `DeriveDarwinHomeLayout` tracks the home-relative dirs it laid and filters `paths.HomeFileRedirects()` against them |

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

**`InstallHomeOverlay` descending through a symlink is what makes content delivery land at a bind
mount's granularity.** The host composes the same trees the container mounts, stages them
root-owned at `/var/yolo-jail/home-overlay/<cname>`, and the bootstrap copies them over the home.
That copy used to `RemoveAll` the overlay's TOP-level entry, which for a skills destination of
`.claude/skills` is `~/.claude` — the whole state dir, credential symlink included. Under the
layout the same `RemoveAll` would unlink the sidecar symlink and leave a real directory, so the
next boot's layout refuses: a backend that bricks itself after one launch. A symlink in the home
is a LAYOUT link, marking a path the home merely passes through; the first real directory is the
destination, and replacing it wholesale is what makes a skills dir a pack stopped shipping
disappear.

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
behaving). The same measurement is why `cache_relocations` has no "just symlink it" workaround
here, and why [`declaration-parity.md`](../design/declaration-parity.md) settled `DP-L1` on a copy
rather than a staged symlink.

## Seatbelt does the read-only half of a bind, and the launcher does the other

The reason this backend drops features is stated everywhere as *"it has no bind mounts"*, and for
the home tiers that sentence is too coarse. A `:ro` bind does two separable things: it makes a
file **appear** at a path, and it makes that path **unwritable**. Seatbelt does the second
natively, and the launcher runs **outside** the sandbox, so it can do the first by copying. Four
features already ride that route — `workspace_readonly` (which used to accept the key and silently
do nothing, until `readonlyDenies` gave it a kernel deny), skills and briefings, source-bearing
`host_files`, and a pack's `reads-host` layer.

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
- **`cache_relocations`** — a bind onto other storage, and the "just symlink it" workaround is
  refuted by target evaluation plus the profile's `/Volumes` read-deny. Warned on the same
  condition, from the same function.
- **`writable_home_dirs`** — not a gap: the home is natively writable, so the knob has no target.
- **No PID, network or mount namespace**, and every jail runs as the same `_yolojail` uid, so a
  host daemon cannot tell which jail is calling. Concurrent jails with *different* profiles do
  work; a macos-user jail launching another one does not, which is an equality constraint in
  `sandbox_apply` rather than a policy gap.

## No migration: an occupied path refuses the launch

`ensureLayoutSymlink` never removes a real file or directory — that is [OQ-HT2](#oq-ht2) as code.
A symlink yolo itself wrote IS replaced, because the sidecar it named moved; anything else makes
`Apply` collect the path and refuse. Every occupied path is reported **together**, because one at
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
Four things are still one-per-machine, and each is deliberate or named.

- **The home ROOT is shared, so the login rc files are.** `.zprofile`, `.zshrc` and
  `.bash_profile` sit below every symlink the layout lays and are read by the shell from `$HOME`.
  `WriteLoginRC` therefore writes an **indirection** rather than a value: it re-prepends
  `$YOLO_DARWIN_LOGIN_PATH`, which the launch exports from the same `SandboxPath` call that builds
  `PATH`. A literal there would be one workspace's `packages:` store dirs in the next workspace's
  login shell — the same race the sidecar closed for briefings, in three files nobody would look
  at. Unset (a shell yolo did not launch) leaves `PATH` alone.
- **The account home holds ONE link set.** A second launch in a different workspace repoints it,
  which is correct for sequential use and self-healing (`ensureLayoutSymlink` repoints a link yolo
  wrote). Two **concurrent** launches in different workspaces still contend: the last one wins,
  and the earlier session's `~/.claude` then names a directory its own profile denies reading.
  Reasoned from two measured facts rather than observed. The per-workspace launch lock does not
  cover it — `run.AcquireWorkspaceLockFor` is keyed per workspace and released before the agent
  starts, because holding it across a session would make a second terminal in the same workspace
  block until the first ended.
- **`yolo stop` has nothing to stop and there is no attach.** Every invocation is a fresh sandbox
  (`internal/cli/stop.go`), so two launches on one workspace really do run two bootstraps and two
  provisioning stages. That window — bootstrap through stage, since the bootstrap generates the
  very script the stage execs into the same sidecar — is what the workspace lock covers. A lock
  that cannot be taken warns and degrades rather than refusing the launch.
- **The overlay copy is agent-writable where a bind is `:ro`.** `noteMacosUserContentGaps` says so
  on every launch that selected a pack — a packless launch delivers no content and prints nothing.
  That is the one difference the layout cannot close, because it is about the enforcement primitive
  rather than the location.

> [!CAUTION]
> **A transient `LoadJailPacks` failure permanently poisons the account home — open, and it must
> never be automated.** The link set is derived from the loaded packs, and a load error does not
> abort the bootstrap (A12: every step still runs), so the layout lays an EMPTY link set,
> `install_home_overlay` creates a REAL `~/.claude`, and every later launch refuses forever with a
> remedy that destroys the machine tier the shared-credentials hook exists to preserve. Reaching
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
| `MISE_DATA_DIR` names a real machine-tier store that mise populates | **Hardware, 2026-09-12** — [runbook item 9](../plans/runbooks/macos-user-manual-checks.md#9-mise_tools-and-lsp_servers-actually-arrive--new-2026-09-12-never-run): two tools installed under `/Users/_yolojail/.yolo/mise/installs`, a real directory, while its sibling `~/.yolo/bin` is a symlink |
| The login-rc re-prepend still beats `path_helper` now that its value arrives by variable | **Hardware, 2026-09-12** — [runbook item 3](../plans/runbooks/macos-user-manual-checks.md#3-the-acceptance-bar--packages-reaches-the-agent) re-run, `fzf` resolving into the store profile with Homebrew's copy present |
| An occupied MIRROR refuses the launch, names a remedy that reaches it, and does not delete the directory it declined to migrate | `integration/TestMacosUserLayoutRefusesAnOccupiedSidecarMirror` (hardware + nightly); the message half by `TestDarwinHomeLayoutRefusalPrescribesARemedyThatReachesTheOccupiedPath` on Linux |
| Seatbelt judges a symlink's TARGET | **Hardware, 2026-09-13** — `sandbox-exec` probe, both spellings denied, three controls behaving |
| `..` resolves physically on darwin as on Linux | **Hardware, 2026-09-11** — the fixture rebuilt on macOS 26.5 |
| The boot ordering, and a credential resolving through the layout the bootstrap just laid | Linux unit gate against a real filesystem, driving `RunDarwinBootstrap` itself (`internal/entrypoint/darwinhomelayout_test.go`) |
| The tier SOURCING, and the refusal's two groups | the same gate through `InstallDarwinHomeLayout` — the real boot entry rather than `Apply` directly, so the tier lists come from a pack manifest and the refusal is one a launch would really print |
| The deriver, idempotence and the repointing, the stale-link pair | the same gate calling `DeriveDarwinHomeLayout(…).Apply()` directly |
| The probe script and its parser that the Mac test depends on | `TestMacosUserHomeTierProbeReadsARealLayout`, a Linux preflight applying the REAL deriver |
| An occupied ACCOUNT-HOME path refuses | Linux only (`TestDarwinHomeLayoutRefusesToReplaceRealDirectories`); not separately exercised on hardware |
| A concurrent second workspace leaves the first session pointing at a denied directory | **Not measured** — reasoned from the one-link-set fact plus target evaluation |
| The pack-load poisoning route | **Not measured, deliberately, and must stay that way** (see the caution above) |

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

## Why it is this way

Rulings a future change would otherwise re-derive or undo, with the ids source comments and other
documents cite.

| Ruling | Why it holds |
| :--- | :--- |
| <a id="oq-ht1"></a>[**OQ-HT1**](#oq-ht1) — the pack's declared `scope` decides the tier, and this backend honors it rather than re-deciding it | `scope: machine` → `packload.SharedDirs`, `scope: workspace` → `packload.WritableDirs`, and the container mount assembler consumes exactly those two lists. So credentials are machine tier and history and transcripts are workspace tier because `packs/claude` says so, not because this backend chose. A backend that shared a `scope: workspace` dir, or split a `scope: machine` one, would be the feature-detection failure of [OQ-HT4](#oq-ht4) seen from the pack's side. Pinned by `TestTheHomeLayoutsTiersComeFromThePackDeclaration`, which drives the real boot entry with a synthetic pack no hardcoded list could contain — substituting literal slices for the two accessors left the suite fully green. |
| <a id="oq-ht2"></a>[**OQ-HT2**](#oq-ht2) — **no migration; wiping `/Users/_yolojail` is a supported reset** | *"Nobody is using it. No transition needed. If I need to wipe it first, that's fine."* A real file or directory where a link belongs is never removed, renamed or copied: the launch names every offender and the remedy that reaches it. What is given up is real — the old shared home holds the workspace tier for every workspace that ever launched here, and transcripts are user work product where a re-fetchable token is not — and it was accepted on measured grounds: this backend's only session at the time was yolo-generated content. The precedent was already priced in `linkThroughShared`, which accepts losing one login on a layout change. What it buys is that the layout shipped with no one-shot mutation, no `.pre-tiers-<date>` directory and no first-launch copy path. ⚠ Do not reuse the ruling for a different home — for the podman base, which somebody IS using, [`base-home-legacy-state.md`](../design/base-home-legacy-state.md) leaves the legacy bytes in place, unmounted and unread, rather than discarding them ([§2.9](../design/base-home-legacy-state.md#29-backends)). |
| <a id="oq-ht3"></a>[**OQ-HT3**](#oq-ht3) — per-workspace, not per-session | The same-workspace overwrite is **convergent**: two launches on one workspace compose identical content from identical config, packs and briefing, which is what the container's attach already relies on. A per-session tier is a mechanism no other backend has, buying nothing the workspace tier does not, and it would have multiplied [OQ-HT2](#oq-ht2)'s surface by every session ever run. The container's courtesy flock was a moved call rather than a design point, and it moved: `run.AcquireWorkspaceLockFor` exists for this one caller. |
| <a id="oq-ht4"></a>[**OQ-HT4**](#oq-ht4) — `HOME` stays `/Users/_yolojail`; the workspace tier is a symlink layout into the sidecar, and `SharedDirs` stay put and are mirrored back | The constraint that outranks the layout choice is **one mechanism on every backend**: *"it's going to be just identical to how you share them in container jails … otherwise you're just fragmenting the utility of this tool and you can't really share things, because you'd have to detect features and stuff and it would be awful."* A per-workspace `HOME` (the recorded runner-up) reaches credentials some other way, which makes *"where are my credentials"* a per-backend question every pack touching them must feature-detect. What may differ between backends is only the **primitive that enforces the boundary** — a bind mount on podman, an SBPL rule here — because that is invisible to a pack and to a user. In [`backend-parity.md`](../design/backend-parity.md)'s vocabulary the target disposition is **HonoredBy**: the same outcome by a named different primitive, never a different mechanism. A per-workspace `HOME` would also have moved the install prefixes out of reach of the `agent_updates` lock the shared home provides, and overturned [`macos-user-nix-and-features.md`](macos-user-nix-and-features.md)'s standing refusal. |
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
| mise data dir | `<home>/.yolo/mise`, crossed as `MISE_DATA_DIR`, not overridable from the launch env | `internal/macosuser/macosuser.go` (`SandboxMiseData`) |
| Machine-wide cache | `~/.cache` in the account home — deliberately not linked | `internal/entrypoint/darwinhomelayout.go` (by absence); container analogue `paths.GlobalCache` |
| Login-rc PATH indirection | `YOLO_DARWIN_LOGIN_PATH`, re-prepended in `.zprofile`, `.zshrc`, `.bash_profile` | `internal/entrypoint/darwinhomelayout.go` (`DarwinLoginPathEnv`), `internal/entrypoint/darwin.go` (`WriteLoginRC`) |
| Staged content tree | `/var/yolo-jail/home-overlay/<cname>`, root-owned, named by `YOLO_DARWIN_HOME_OVERLAY` | `internal/macosuser/macosuser.go` (`StagedHomeOverlay`, `StageHomeOverlayCommands`), copied by `internal/entrypoint/darwin.go` (`InstallHomeOverlay`) |
| Per-workspace launch lock | `<global storage>/locks/<cname>.lock`, held bootstrap-through-stage, released before the agent | `internal/cli/run/flock.go` (`AcquireWorkspaceLockFor`), seam `internal/macosuser/orchestrator.go` (`Deps.LockWorkspace`) |
| The supported reset | `sudo rm -rf /Users/_yolojail && yolo macos-setup` | no single site prints both halves: the `rm -rf` by `occupiedLayoutError` (`internal/entrypoint/darwinhomelayout.go`), the reprovision by the missing-home refusal (`internal/macosuser/orchestrator.go`, which is what makes the second half necessary), and the two joined only in [runbook item 5](../plans/runbooks/macos-user-manual-checks.md#5-the-per-workspace-home-layout--new-2026-09-12-never-run) |
