---
title: "The macos-user Home: One Account, Three Tiers That Collapsed Into It"
date: 2026-09-03
status: accepted
tags: [macos-user, jail-home, backend-parity, design]
summary: "macos-user has one sandbox home, /Users/_yolojail, so the machine tier, the workspace tier and the session tier are the same directory. The machine tier is right; the other two collapsing into it is the defect, and since content delivery landed it is a write-write race. The fix keeps HOME where it is and symlinks every directory the container backends bind from <workspace>/.yolo/home/ into that same sidecar — the credential-sharing mechanism already runs here unchanged, and needs only its shared dir mirrored so its relative link resolves."
---

# The macos-user home: one account, three tiers that collapsed into it

**Status:** DESIGN, 2026-09-11 (DESIGN SKETCH 2026-09-03). Nothing built. Audited
against the tree at `61c26c18` on 2026-09-11; three of its four questions are settled
and compacted into the [Decision Ledger](#decision-ledger). **[`OQ-HT2`](#decision-ledger) is the
one that still needs a ruling**, and it is the maintainer's.

> **In short.** The machine tier is **not** colocation: the `shared_credentials` hook
> already makes `~/.claude/.credentials.json` a relative symlink into a pack-declared
> machine-scope directory on this backend exactly as on every other, and the single
> home supplies only that directory's *backing*. What macos-user lacks is the
> per-workspace tier — and it gets it by symlinking the same `<workspace>/.yolo/home/`
> sidecar the container backends bind, with no Seatbelt change and no moved credential.

**Why it matters.** Content delivery (2026-09-03) turned a static leak between
workspaces into a write-write race on the briefing an agent reads as instructions
([§2](#2-what-the-collapse-actually-costs)).

**The shape.** `HOME` stays `/Users/_yolojail`. Every directory the podman argv binds
from `<ws>/.yolo/home/` becomes a symlink from the account home into that sidecar;
every pack-declared machine-scope directory stays in the account home and is
*mirrored* into the sidecar so the hook's relative link keeps resolving
([§5](#5-the-proposal)).

**Cost.** The workspace-scope state already sitting in `/Users/_yolojail` belongs to
every workspace at once, so it has no single destination — [`OQ-HT2`](#decision-ledger).

**Start at [§5](#5-the-proposal)**, then [§3](#3-why-it-has-not-been-fixed-by-simply-splitting)
for the trap the first draft mis-stated.

**Needs your ruling:** **None** — all four closed ([Decision Ledger](#decision-ledger)), the last on 2026-09-11. Ready to build, and it needs no migration step.

> [!NOTE]
> **Terms coined here.** A **tier** is a scope at which jail state is kept
> separate: **machine** (shared by every jail on the host — credentials),
> **workspace** (one project — pack `state`, agent history, composed content), and
> **session** (one launch — generated config). A tier is *not* a directory and *not*
> a confinement notch: the container backends implement all three tiers with two
> directories, and every notch has all three. The words name the separation, not
> its implementation.
>
> **And a home is not a workspace.** Two unrelated paths are in play throughout and
> only one of them moves. `/Users/Shared/yolo` (`SharedRootDefault`) is the neutral
> root your PROJECTS live under — `/Users/Shared/yolo/yolo-jail` is a workspace.
> `/Users/_yolojail` (`SandboxHome`) is the sandbox account's HOME, holding agent
> config, credentials and history. Everything below is about the second.
>
> **The sidecar** *(coined here)* is `<workspace>/.yolo/` — the workspace's own
> gitignored state directory, and `<workspace>/.yolo/home/` within it is the SOURCE of
> every per-workspace directory the container backends bind into `/home/agent`. It is
> not the jail home and not the workspace tree; it is where the workspace tier lives on
> every backend that has one.

**Reads with:** [`jail-home.md`](../reference/jail-home.md) (the container home layout this
converges on — its *Shared credentials* section is the relative-link fact
[§5](#5-the-proposal) rests on), [`backend-parity.md`](backend-parity.md) (its four
dispositions, and [OQ-BP-2](backend-parity.md#decision-ledger), the delivery gap this
follows), [`macos-user-nix-and-features.md`](../reference/macos-user-nix-and-features.md)
(the backend), [`macos-user-provisioning.md`](macos-user-provisioning.md) (whose
[OQ-P3](macos-user-provisioning.md#decision-ledger) takes its answer from this doc's
[§5](#5-the-proposal)), and
[`macos-revival-and-distribution-plan.md`](../plans/macos-revival-and-distribution-plan.md).

---

## 1. The shape of the problem

`macosuser.SandboxHome()` is the constant `/Users/_yolojail`
(`internal/macosuser/macosuser.go:56`, verified 2026-09-11). It has no workspace
component and no session component, so three tiers the other backends keep apart
are one directory here:

| Tier | Container backends | macos-user |
| :--- | :--- | :--- |
| **machine** — credentials shared by every jail | `~/.local/share/yolo-jail/home/` (`paths.GlobalHome`), bound `:ro` as the home base, plus each pack's `scope: machine` dir bound rw from it (`internal/cli/run/assemble_parts.go:107`, `assemble.go:344`) | `/Users/_yolojail` |
| **workspace** — pack `state` dirs, agent history, composed content, installed programs | `<ws>/.yolo/home/{npm-global,local,go,yolo-bin,config}` plus each pack's `scope: workspace` dir, bind-mounted (`assemble_parts.go:106-120`, `assemble.go:336`) | `/Users/_yolojail` |
| **session** — one launch's generated config | regenerated into the workspace binds above on every entry | regenerated into `/Users/_yolojail` on every launch |

The machine tier is the one that is *right*: a single account holding one set of
agent credentials is the whole point of a dedicated sandbox user, and it is what
makes `shared_credentials` work here with no broker at all
([the loopholes section of `macos-user-nix-and-features.md`](../reference/macos-user-nix-and-features.md#loopholes-mostly-moot-and-the-framework-ports-better)).
The other two rows are the defect.

## 2. What the collapse actually costs

Three symptoms, in increasing order of how much they matter.

1. **Pack `state` dirs are machine-wide.** `.claude`, `.codex`, `.gemini`, `.pi`
   are per-workspace on every other backend. Reported at launch by
   `noteMachineWideWorkspaceState` (`internal/cli/run/loopholeinert.go:261`, called
   from the macos-user arm at `run.go:327`). ⚠ **One file in that set is already
   per-workspace here**: the `per_jail_history` hook keys on `YOLO_HOST_DIR`
   (`internal/entrypoint/packhooks.go:169-193`), which this backend sets
   (`internal/macosuser/runplan.go:240`), so `~/.claude/history.jsonl` is already a
   symlink to `~/.claude/jail-history/<hash>.jsonl`. What leaks is everything
   *else* under the state dir — `~/.claude/projects/<workspace>/*.jsonl` transcripts
   above all.
2. **The Seatbelt profile enforces a boundary the home then leaks.** The profile
   denies reading a sibling workspace's files — and
   `~/.claude/projects/<other-workspace>/*.jsonl` is readable anyway, because it
   lives in the shared home rather than under the workspace.
3. **Composed content races between concurrent launches.** This is the new one.
   Skills and briefings are now delivered by copying a composed overlay over the
   home on every entry (`InstallHomeOverlay`, `internal/entrypoint/darwin.go:158`).
   Two workspaces launched concurrently write the same paths, so the second
   replaces the first's — and a briefing is *per-project prose*, so an agent
   mid-session can go on reading a description of a different project. **Two more
   files are in the same class and were not listed before** (found 2026-09-11): the
   login rc files (`WriteLoginRC`, `darwin.go:222-234`) carry this workspace's
   `packages:` store paths, and `~/.config/mise/config.toml` (`ConfigureMisePrism`,
   `darwin.go:85`) carries this workspace's `mise_tools`. Both are written to the
   shared home on every launch.

Symptom 3 is qualitatively worse than 1 and 2. Those leak information between
workspaces; this one feeds an agent instructions for the wrong project, silently,
while it is working. **And nothing guards it**: this backend has no attach
(`run.go:377`), no workspace flock (`acquireWorkspaceLock`'s one production caller
is inside `runContainer`, `run.go:732`), and `yolo stop` is a no-op here
(`internal/cli/stop.go:112-116`) — verified 2026-09-11.

## 3. Why it has not been fixed by simply splitting

### ⚠ Retracted (2026-09-11): "the single home IS the shared-credentials mechanism"

That sentence is in this doc's first draft, in `run.go:325-326`, in
`seatbeltcapture.go:9-13`, in `macos-user-nix-and-features.md` and in the roadmap,
and it is **imprecise in the one way that matters for the fix**. Measured against the
tree 2026-09-11:

- The mechanism is the `shared_credentials` **hook** — `Env.linkSharedCredential`
  (`internal/entrypoint/packhooks.go:109-142`) replaces `~/.claude/.credentials.json`
  with a **relative** symlink (`../.claude-shared-credentials/.credentials.json`) into
  a directory the pack declared at `scope: machine`
  (`packs/claude/pack.json:152-157`, hook at `:163-168`).
- **That hook runs on macos-user**, unchanged: `RunDarwinBootstrap` calls
  `RunPackHooks` (`internal/entrypoint/darwin.go:112`). The link exists in
  `/Users/_yolojail/.claude/` today, pointing at
  `/Users/_yolojail/.claude-shared-credentials/`.
- What the single home supplies is therefore only the **backing** of the shared
  directory — a plain dir in the one home, where the container binds it from
  `GlobalHome`. The mechanism, the location a pack sees, and the declaration are
  already identical on every backend; [§5.0](#50-the-constraint-that-outranks-the-layout-choice-one-mechanism-every-backend)'s
  test is already passed for the machine tier.

So the trap is narrower than "a split breaks credential sharing". **The trap is that
a split has to keep the declared machine-scope directory machine-scope AND keep it
reachable through the hook's relative link** — and the second half is where a naive
symlink layout fails ([§5.3](#53-what-the-credential-tier-then-needs-precisely)).
Give each workspace its own copy of `.claude-shared-credentials` and every workspace
needs its own login; leave it shared but break the relative path and every workspace
gets a dangling link, which `linkThroughShared` then "repairs" by discarding the
local login on the next boot (`internal/entrypoint/claude.go:59`, rule "the shared
file always wins").

That is a design change and not a launch-time patch — which is exactly why
`noteMachineWideWorkspaceState` warns instead of fixing.

## 4. What forced this

Content delivery (2026-09-03). Before it, the collapse cost information leakage
between workspaces — bad, but static. After it, every launch WRITES to the shared
home, so the tier collapse became a write-write race on files an agent reads as
instructions.

## 5. The proposal

Adopt the two-tier structure the container backends already have, in the one
account this backend has — **by reusing the container's own bind table as a symlink
table.** This is alternative **A′** of [§7](#7-alternatives), settled by
[`OQ-HT4`](#decision-ledger).

**Nothing here moves your projects, and `<workspace>/.yolo/` does not go away.**
`/Users/Shared/yolo` stays exactly as it is, and so does every workspace under it.
The sidecar is already gitignored, and on the reviewing Mac it already holds
`claude`, `config`, `go`, `npm-global` and a `bash_history` from container runs.
macos-user writes `prism/` there today and ignores `home/` entirely.

- **`HOME` stays `/Users/_yolojail`.** Nothing in `LaunchArgv`, `SandboxPath` or the
  Seatbelt profile changes its home argument. This is also what keeps
  [`macos-user-nix-and-features.md`](../reference/macos-user-nix-and-features.md)'s
  standing refusal of "a per-workspace home" true rather than overturned.
- **Every directory the podman argv binds from the sidecar becomes a symlink from the
  account home into the sidecar.** The list is the container's, not a new one:
  `npm-global→.npm-global`, `local→.local`, `go→go` (`paths.HomeSurfaces()`,
  `internal/paths/paths.go:438-444`), `yolo-bin→.yolo/bin`, `config→.config`
  (`assemble_parts.go:118-119`), and each pack's `scope: workspace` state dir
  (`packload.WritableDirs`, `assemble.go:336-339`). Same location for a project's
  agent state on every backend; a symlink where podman has a bind.
- **Every pack-declared machine-scope directory stays in the account home — and is
  mirrored into the sidecar.** `packload.SharedDirs` (`assemble.go:344-347`) is the
  list. The mirror is a symlink `<ws>/.yolo/home/.claude-shared-credentials →
  /Users/_yolojail/.claude-shared-credentials`, and it is not optional:
  [§5.3](#53-what-the-credential-tier-then-needs-precisely) is why.
- **What stays machine-wide stays because the container keeps it machine-wide too.**
  `~/.cache` is `paths.GlobalCache()` on podman (`assemble_parts.go:120`) and the mise
  data dir is a machine-wide store mounted at `/mise` (`assemble_parts.go:172-176`) —
  neither is per-workspace anywhere, so neither gets a symlink here. ⚠ The mise data
  dir needs `MISE_DATA_DIR` set explicitly on this backend once `~/.local` is a
  sidecar symlink, because the unset default is `$HOME/.local/share/mise`
  (`internal/entrypoint/env.go:161-165`) — which would land it in the *per-workspace*
  tier, the opposite of every other backend. That is
  [OQ-P3](macos-user-provisioning.md#decision-ledger)'s answer.
- **The Seatbelt profile does not change.** The sidecar sits under the workspace, so
  it is inside `(allow file-write* (subpath <ws>))` and `(allow file-read* (subpath
  <ws>))` already (`internal/macosuser/seatbelt.go:49, 79`), and a *sibling*
  workspace's sidecar is already denied by the `/Users` read-deny with
  `(literal)`-only ancestors (`:74-80`, [§5.2](#52-isolation-is-already-enforced-for-workspaces--the-home-is-the-one-hole-left)).
  Symptom 2 becomes enforced without a profile edit.
- **The migration question shrinks to the workspace-scope state already in the
  account home**, because the machine tier never moves —
  [`OQ-HT2`](#decision-ledger).

**Stated residuals** (holes the first draft left silent, now delegated or named):

- **The login rc files** (`.zprofile`, `.zshrc`, `.bash_profile`) are read by the
  shell from `$HOME`, which stays shared, and they carry per-workspace bytes (this
  launch's store bin dirs, `darwin.go:222-234`). They must stop carrying
  per-workspace content. *How* is the implementer's — indirect the PATH through an
  environment variable the launch already sets, or point `ZDOTDIR` into the sidecar —
  because both give the same observable behaviour: a login shell in workspace A
  never sees workspace B's store paths.
- **The sidecar symlinks are created by the launcher or the bootstrap on every
  launch, idempotently**, before `InstallHomeOverlay` and the pack hooks run — the
  hooks `MkdirAll` through the link and compute their relative targets lexically
  (`packhooks.go:128-137`), so ordering is the only thing that matters. A real
  directory already at a link's path is the pre-existing-state case and is
  [`OQ-HT2`](#decision-ledger).
- **Two launches on one workspace share the sidecar, and that is the end state** —
  per-workspace is the whole tier and there is no per-session one
  ([`OQ-HT3`](#decision-ledger)). It is what the container's attach already does: one
  home per workspace, with the generators re-run inside it
  ([`jail-home.md`](../reference/jail-home.md), *Reuse and attach*). It is safe for the
  reason attach is safe — **the same-workspace overwrite is convergent**: two launches
  on one workspace compose identical content from identical config, packs and briefing,
  so the second write is a no-op in bytes. Symptom 3 of
  [§2](#2-what-the-collapse-actually-costs) is a *cross*-workspace race, and the
  per-workspace tier ends it. What macos-user lacks here is not isolation but the
  container's **courtesy flock** (`internal/cli/run/flock.go:41-57` — a non-blocking
  probe so contention is observable, then a blocking wait with a notice; it waits
  rather than refuses). Taking it on this arm is a moved call, not a design point, and
  belongs in the companion sketch.

## 5.0 The constraint that outranks the layout choice: one mechanism, every backend

**Stated in review 2026-09-11, and it is the strongest argument in this
document — including against this doc's own first proposal:**

> *"You can share [credentials] to the home, but if you don't, you have their own
> homes. It's going to be just identical to how you share them in container jails.
> It seems like we should have the same mechanisms across these things. If
> possible — otherwise you're just fragmenting the utility of this tool and you
> can't really share things, because you'd have to detect features and stuff and it
> would be awful."*

**That is a constraint on the answer, not a preference between answers**, and it
is why [`OQ-HT4`](#decision-ledger) settles on **A′** rather than A. The container backends
already solve credential sharing with a **machine-scope location plus a relative
symlink** (the `shared_credentials` hook, [§3](#3-why-it-has-not-been-fixed-by-simply-splitting));
a per-workspace home that reached credentials some *other* way would make "where
are my credentials" a per-backend question, and every pack that touches them would
need to know which backend it is on. **Feature detection in a pack is the failure
mode** — the same class [`backend-parity.md`](backend-parity.md) exists to make
visible, where a mechanism added on one backend is absent elsewhere with no error.
In its vocabulary the target disposition is **HonoredBy**: the same outcome by a
named different primitive, never a different mechanism.

So the test any layout here must pass: **the symlink mechanism, the location, and
the thing a pack declares are identical on every backend.** What may differ is
only the *primitive that enforces the boundary* — a bind mount on podman, an SBPL
rule on macos-user — because that is invisible to a pack and to a user.

⚠ **[§5.1](#51-the-plumbing-already-exists--this-is-a-parameter-not-a-rewrite) through
[§5.4](#54-seatbelt-can-replace-more-mounts-than-this-one) below were written about
alternative A** (a per-workspace `HOME` under the shared account) before A′ was
weighed. **Their findings are about the enforcement primitive and survive the
choice** — the call sites take a home parameter either way, and the `/Users`
read-deny narrows whatever home it is given. What A′ *removes* is the need to use
them for this fix: with `HOME` constant, [§5.1](#51-the-plumbing-already-exists--this-is-a-parameter-not-a-rewrite)'s
parameter is never passed a different value and
[§5.3](#53-what-the-credential-tier-then-needs-precisely)'s re-allow is never
emitted. They stay because they are true, because A is the recorded runner-up, and
because [§5.4](#54-seatbelt-can-replace-more-mounts-than-this-one) is a live
proposal in its own right.

## 5.1 The plumbing already exists — this is a parameter, not a rewrite

**Measured 2026-09-11, in review, and re-measured in audit the same day.**
`SandboxHome()` is a *default*, not a constraint. Every place that needs a home
already takes one as an argument and falls back to the constant only when passed
`""`:

| Call site | Signature | What a narrower home changes |
| :--- | :--- | :--- |
| `internal/macosuser/macosuser.go:461` | `SandboxPath(home string, prefix []string)` | every PATH entry (`.yolo/bin/block`, `.local/bin`, mise shims, …) |
| `internal/macosuser/macosuser.go:482` | `LaunchArgv(…, workspace, user, home string, …)` | the `HOME`/`USER`/`SHELL`/`PATH` quartet handed to `env -i` |
| `internal/macosuser/seatbelt.go:34` | `SeatbeltProfile(workspace, sandboxHome string, readonlyRels []string)` | the writable subtree **and** the `/Users` read re-allow |
| `internal/macosuser/runplan.go:82` | `DarwinBootstrapArgv(stagedYolo, home string, …)` | the `HOME`/`JAIL_HOME` pair the generators write into |
| `internal/macosuser/runplan.go:236` | `buildBootstrapEnv(…, home string, …)` | the login-rc PATH (`YOLO_DARWIN_LOGIN_PATH`) |

⚠ The first review counted three; there are five in `BuildRunPlan`'s reach alone
(`runplan.go:183, 192, 202, 203`), which is the difference between "pass a string at
three call sites" and "audit every site that spells the constant". **And the
strongest fact was missed entirely: a non-default home is already exercised in
production.** The install-capture path bootstraps a THROWAWAY STAGING HOME through
these same functions (`runplan.go:221-225`; `internal/macosuser/capture.go:106-107,
253-254, 328-329`), and its profile already emits a post-allow
`(deny file-write* (subpath <home>))` over the whole shared home
(`internal/macosuser/seatbeltcapture.go:103-108`) — the exact shape a narrowing under
A would have needed, shipped and pinned.

### 5.2 Isolation is already enforced for workspaces — the home is the one hole left

⚠ **This corrects a claim made earlier in the same review**, that a per-workspace
home would confine writes but leave reads open because the profile opens
`(allow default)`. It does open that way, **and then denies `/Users` wholesale**:

```scheme
(deny file-read* (subpath "/Users"))
(allow file-read*
    (literal "/Users") (literal "/Users/Shared")
    <ancestor literals of the workspace>
    (subpath <workspace>)
    (subpath <home>))
```

Two consequences worth stating plainly:

- **Workspaces are already isolated from each other.** A sibling workspace under
  `/Users/Shared/yolo/<other>` is re-allowed by nothing — the ancestors are granted
  as `(literal)`, which grants the directory entry *without* re-allowing siblings a
  `(subpath)` would. That comment is in the profile (`seatbelt.go:145-148`) and the
  mechanism is live.
- **The home is the single remaining shared surface**, and it is shared only
  because every launch passes the same string. Narrow it and **reads narrow with
  it, for free** — the deny is already written; `(subpath <home>)` is the only
  thing re-allowing it. Under A′ the same isolation reaches the workspace tier by a
  different route: the per-workspace state moves *under the workspace*, where the
  sibling deny already applies.

### 5.3 What the credential tier then needs, precisely

Two different requirements, one per alternative.

**Under A** (runner-up): once `home` is per-workspace, the shared credential store
falls under the `/Users` deny like anything else, so it needs its own re-allow,
**for read and for write** — the OAuth path *refreshes* tokens, so a read-only
carve-out would break the thing it was meant to preserve. That is one
`(subpath …)` in each of the two lists.

**Under A′** (chosen): no profile change — but a layout one, and it is the trap the
first A′ sketch did not see. **The hook's symlink is relative *by design***:
[`jail-home.md`](../reference/jail-home.md) says it is relative *"so it resolves
through whichever mount backs the agent's own state dir into the separately mounted
shared dir"*. Measured on the reviewing jail 2026-09-11:
`<ws>/.yolo/home/claude/.credentials.json → ../.claude-shared-credentials/.credentials.json`
is **dangling from the host's view** and resolves only inside the container, where
`GlobalHome/.claude-shared-credentials` is bound beside it. A bare symlink
`~/.claude → <ws>/.yolo/home/claude` reproduces the host's view: the kernel resolves
`..` physically, to `<ws>/.yolo/home/`, so the credential link points at
`<ws>/.yolo/home/.claude-shared-credentials/.credentials.json` — a path that does
not exist. Two remedies were weighed:

| Remedy | Verdict |
| :--- | :--- |
| Mirror each `SharedDirs` entry into the sidecar as a symlink to the account-home dir | **Chosen.** It is the "make it appear at the path" half of a bind, done with a symlink, launcher-side, and invisible to the pack — the hook's output is byte-identical. |
| Emit an absolute target from `linkSharedCredential` on macos-user | **Rejected** by [§5.0](#50-the-constraint-that-outranks-the-layout-choice-one-mechanism-every-backend): a backend branch in the one hook every backend shares. |

> [!NOTE]
> **CONFIRMED ON DARWIN, 2026-09-11 — the `..` measurement above was made on a Linux jail, and it
> transfers.** The fixture this doc's reasoning rests on
> (`link → real/sub`, `real/sub/via → ../shared/f`) was rebuilt on macOS 26.5 and the read failed
> with `No such file or directory`: darwin resolves `..` **physically**, to `real/`, exactly as
> Linux does. So A′'s mirror-the-shared-dir remedy is necessary here and is not solving a
> Linux-only artifact.
>
> **It needed no sandbox to establish, and that is worth recording as method.**
> [`provisioner-sets.md` §15](provisioner-sets.md#15-what-a-mac-session-should-measure) asked for
> this under a `macos-user` launch, on the reasonable worry that kernel path semantics *under a
> profile* might differ. They cannot differ in the direction that matters: resolution happens in
> the VFS before the policy is consulted, and a Seatbelt profile can only deny an access that
> resolves — never make an unresolvable path resolve. The unsandboxed failure therefore entails the
> sandboxed one, and the item spent no password.

## 5.4 Seatbelt can replace more mounts than this one

**Raised in review 2026-09-11, and the pattern already has TWO shipped precedents in
this backend.** The reason macos-user drops features is stated everywhere as *"it
has no bind mounts"* — twenty-odd places in code and docs, counted 2026-09-11 — but a
`:ro` bind does two separable things: it makes a file *appear* at a path, and it
makes that path *unwritable*. Seatbelt does the second natively, and the **launcher
runs outside the sandbox**, so it can do the first by copying.

**The first precedent is `workspace_readonly`**, and `SeatbeltProfile`'s own
docstring gives the argument:

> Why this exists: the key is delivered as a `-v …:ro` bind on the container
> backends (`internal/cli/run/mounts.go`), and macos-user has no mounts, so it used
> to accept the key and **silently do nothing**. A security key that lies is worse
> than one that refuses.

**The second is skills and briefings themselves**: the host composes the same trees
the container mounts, stages them root-owned at `/var/yolo-jail/home-overlay/<cname>`
(`StageHomeOverlayCommands`, `internal/macosuser/macosuser.go:229-251`), and the
bootstrap copies them over the home (`InstallHomeOverlay`, `darwin.go:158-199`). Only
the *unwritable* half is missing — the copy is agent-writable, which
`noteMacosUserContentGaps` says at every launch (`loopholeinert.go:291`) — and that
half is one `readonlyDenies`-shaped `(deny file-write* …)` per delivered path.

So two features already took the copy route. The census of what could follow,
corrected against the tree 2026-09-11:

| Feature | Container mechanism | macos-user today | Copy + Seatbelt equivalent |
| :--- | :--- | :--- | :--- |
| skills, briefings | `:ro` bind per staged dir | **copied, writable** — warned | add a write deny per delivered path; precedent `seatbeltcapture.go:108` |
| `host_files`, source-less (`content`/`defaults`, any of the four modes) | rendered by `ConfigureHostFiles` under a `:ro` base | **already works** — `darwin.go:119` runs the same generator; `readonly` chmods `0444` here too | add a `(deny file-write* (literal <dest>))` for `mode: readonly` |
| `host_files`, source-bearing | `:ro` `/ctx/host-user` bind carries the host bytes | **dropped, warned** (`loopholeinert.go:365-370`; `SourceLessHostFiles`, `internal/config/hostfiles.go:990`) | launcher copies the host file into the root-owned staging tree; ⚠ the source-bearing read is the user-config-only read that *is* the credential boundary (`hostfiles.go:1010-1012`), so the copy must happen in the host CLI, never in the pure plan builder |
| pack `reads-host` (the host layer) | `:ro` `/ctx` mount | **renders from DEFAULTS, warned** — *"a working config file that is not yours"* (`loopholeinert.go:349-352`) | same as the row above; the most urgent, because its failure looks like success |
| pack `mount`, config `mounts` | `:ro` bind under `/ctx` | **silently ignored, no warning** — the only row with none (`docs/guides/macos.md`, `mounts` row) | copy into the root-owned staging tree as the pack tree already is (`StagePackCommands`); at minimum, warn |

⚠ **The first draft's row "`mode: once` / `copy` — already possible, it is a copy"
was wrong.** Entries are dropped by *source-bearing-ness*, not by mode
(`hostfiles.go:990-997`): a `mode: copy` entry with a `source` is dropped today, and
a `mode: readonly` entry without one works today. There are four modes, not three —
`readonly`, `once`, `copy`, `capture` (`hostfiles.go:59-62`).

⚠ **And on `readonly` this would be STRONGER than the container backends, not a
degraded port.** `yolo config-ref` says of the container path: *"0444 is DAC, not
kernel enforcement: an agent running as root (Claude YOLO does) bypasses the mode
bits. It is a strong signal and a speed bump, not a sandbox."* A Seatbelt
`file-write*` deny is kernel-enforced regardless of uid. So the backend with "no
mounts" would be the one that actually enforces the promise. **One difference
survives and must be stated**: a copy is a snapshot taken at launch, where a bind
reflects a host-side edit live — but `readonly` entries are *re-rendered at boot*
on the container too (`yolo config-ref`, *Modes*), so for `host_files` the two are
equivalent; for a pack `mount` of a live directory they are not.

**What Seatbelt cannot supply**, so the resemblance to a container jail stops
here — these "no mounts" claims **survive** the audit:

- `per_side_paths` — two different contents at one path is a mount-namespace
  capability; Seatbelt filters permissions and cannot fork a path
  (`internal/macosuser/orchestrator.go:193-210`, warned).
- `cache_relocations` — a bind onto other storage; the "just symlink it" workaround
  is refuted by the profile's own `/Volumes` read-deny (`orchestrator.go:227-236`,
  warned). ⚠ Two in-tree docs still repeat the refuted workaround
  (`internal/cli/config_ref.txt`, the `cache_relocations` entry, and
  [`cache-relocation.md`](../plans/cache-relocation.md)); they are not this doc's to
  fix and are listed for the roadmap.
- `writable_home_dirs` — not a gap: the home is natively writable, so the knob has
  no target ([`macos-user-nix-and-features.md`](../reference/macos-user-nix-and-features.md),
  *No bind mounts*).
- No PID, network or mount namespace; and every jail runs as the same `_yolojail`
  uid, so a host daemon cannot tell which jail is calling — the reason
  [`macos-revival-and-distribution-plan.md`](../plans/macos-revival-and-distribution-plan.md)
  rejected the helper-outside-the-sandbox shape. Concurrent jails with *different*
  profiles already work (each launch writes its own profile and passes
  `sandbox-exec -f <path>`); what does not work is a macos-user jail launching
  another one, which is an equality constraint in `sandbox_apply`, not a policy gap.

> [!WARNING]
> **The obvious test for [§5.3](#53-what-the-credential-tier-then-needs-precisely)'s mirror pins NOTHING —
> measured 2026-09-11, after one was written and reverted.** A test that stubs
> `mirrorSharedDirsIntoSidecar` and asserts the link resolves is satisfied by giving the
> **stub** a mirroring body: zero production code changes, and it goes green. That is the
> *"pins the CALLEE while the CALL SITE is unpinned"* shape `AGENTS.md` names — weaker still
> here, because the callee is test-local too.
>
> **So the test this work needs must assert against the real boot path.** The question to ask
> before writing it is AGENTS.md's own: *does it fail if I delete the call site?* Concretely,
> it has to drive whatever lays the layout inside `RunDarwinBootstrap` and observe a link that
> resolves from the account home — not a fixture standing in for it.
>
> ⚠ **And do not pre-write it as a deliberately RED gate.** One was, and it turned `just
> test-fast` and `just done` red for the whole tree — which costs every unrelated commit the
> ability to distinguish *"I broke something"* from *"the known red"*. Reverted in `efe7282c`.
> Write it when the code lands, in the same commit.

## 6. The principles this rests on

**P1. A split must restore every tier it breaks, explicitly.** Colocation is not a
mechanism. The machine tier's *backing* works here by accident — the shared dir is
a plain directory because there is only one home to put it in — and any change
that separates the directories has to replace that accident with something stated.
A fix that repairs the workspace tier and leaves the machine tier to luck has moved
the bug, not removed it.

**P2. The tier of a path is what the pack declares, and a backend honors the
declaration rather than re-deciding it.** `scope: machine` → `packload.SharedDirs`,
the machine-wide tier; `scope: workspace` → `packload.WritableDirs`, the per-workspace
tier (`internal/packload/packload.go:729-738`). The container mount assembler consumes
exactly these two lists, and so does the symlink layout in [§5](#5-the-proposal). A
backend that shared a `scope: workspace` dir, or split a `scope: machine` one, would
be re-deciding a declaration — the feature-detection failure of
[§5.0](#50-the-constraint-that-outranks-the-layout-choice-one-mechanism-every-backend)
seen from the pack's side.

**P2 is also what decides which paths are machine tier** ([`OQ-HT1`](#decision-ledger)),
and it leaves this backend nothing to choose. `~/.claude` is declared `scope: workspace`
(`packs/claude/pack.json:147-151`), so history and transcripts are workspace tier;
`.claude-shared-credentials` is `scope: machine` (`:152-157`), so credentials are machine
tier. Everything the container binds from the sidecar that no pack declares —
`npm-global`, `local`, `go`, `yolo-bin`, `config` — is workspace tier for the same
reason: that is where the container puts it.

> [!WARNING]
> **"Cross-workspace agent history is a thing some people want" is unsourced, and the
> tree contradicts it** (checked 2026-09-11). Every doc treats history isolation as the
> intent — `per_jail_history` is *"belt and braces"* in
> [`jail-home.md`](../reference/jail-home.md) — and nothing records a request to share
> it. Do not reopen it as a macos-user question: a shared-history *feature* would be a
> pack-scope or config change on every backend, which is P2 seen from the other side.

## 7. Alternatives

| Alternative | Verdict |
| :--- | :--- |
| **A. Per-workspace home under the shared account** — `HOME` becomes `/Users/_yolojail/workspaces/<cname>` | **Runner-up**, settled by [`OQ-HT4`](#decision-ledger). Fails [§5.0](#50-the-constraint-that-outranks-the-layout-choice-one-mechanism-every-backend) on *location* (no other backend puts a project's state there); needs the profile narrowed and the credential re-allow of [§5.3](#53-what-the-credential-tier-then-needs-precisely); moves the install prefixes out of reach of the `agent_updates` lock that `runplan.go:251-253` relies on the shared home for; and overturns the reference doc's standing refusal of a per-workspace home. Its one merit — agent state out of the project tree — is a property yolo preserves nowhere else, so it is a principle invented to justify the choice rather than a reason for it. |
| **A′. Symlink the sidecar dirs into the account home** ([§5](#5-the-proposal)) — `HOME` stays `/Users/_yolojail`; every dir podman binds from `<ws>/.yolo/home/` becomes a symlink into it; `SharedDirs` stay put and are mirrored into the sidecar | **Chosen.** Same location, same declaration and same hook output on every backend; zero profile change; credentials never move. Its one trap is the relative link, and [§5.3](#53-what-the-credential-tier-then-needs-precisely) closes it. |
| **B. Split the account** — one `_yolojail` uid per workspace | **Rejected**, in [§9](#9-what-this-does-not-propose). Restores every tier by DAC rather than layout, and costs admin on every new project. |
| **C. Per-session home** | **Rejected**, settled by [`OQ-HT3`](#decision-ledger): a mechanism no other backend has, buying nothing the per-workspace tier does not already buy — and it would have multiplied [`OQ-HT2`](#decision-ledger)'s migration surface by every session ever run. |
| **D. Leave it, keep warning** (today) | **Rejected as an end state.** It was defensible while the cost was leakage between workspaces. Content delivery made it a race on files an agent reads as instructions, which is a different kind of wrong. |

## 8. Risks

| Risk | Mitigation |
| :--- | :--- |
| Migration costs every user a re-login | Cannot happen under A′: the machine-scope dir never moves and the hook's link is regenerated every boot. What *can* be lost is workspace-scope state — the whole of [`OQ-HT2`](#decision-ledger). |
| The hook's relative link dangles through a sidecar symlink | The `SharedDirs` mirror in [§5](#5-the-proposal); pin it with a test that resolves the link *through* the symlinked state dir, since a callee-only test of `linkSharedCredential` stays green without it. |
| The mise data dir silently becomes per-workspace once `~/.local` is a sidecar symlink | Set `MISE_DATA_DIR` explicitly to a machine-wide path on this backend ([§5](#5-the-proposal); [OQ-P3](macos-user-provisioning.md#decision-ledger)). |
| The login rc files keep carrying per-workspace bytes into a shared `$HOME` | Stated residual in [§5](#5-the-proposal); the fix is delegated, the requirement is not. |
| Two jails on one workspace still share | Accepted, and ruled on as [`OQ-HT3`](#decision-ledger) rather than left to be discovered; the same-workspace overwrite is convergent, and the container's courtesy flock ports as a moved call. |
| ⚠ *(retired)* "The Seatbelt writable set has to narrow in step, same commit" | This was an **alternative-A** cost stated as if universal. Under A′ the profile is untouched, because the per-workspace tier moves under a subpath the profile already allows and already isolates from siblings. |

## 9. What this does NOT propose

- **Splitting the *account*.** One `_yolojail` uid per workspace would restore every
  tier by DAC rather than by layout, and it is the wrong trade: account creation
  needs admin, `macos-setup` would become per-workspace, and the credential sharing
  that motivates the single account would need a broker to cross uids —
  reintroducing on macos-user exactly the mechanism this backend's design says it
  does not need.
- **A per-workspace `HOME`.** A′ keeps it constant; the reference doc's refusal
  stands.
- **A backend branch in `linkSharedCredential`**, or in any other pack hook. The
  hook's output must stay byte-identical on every backend; the layout around it is
  what adapts ([§5.3](#53-what-the-credential-tier-then-needs-precisely)).
- **A new list of "which dirs are per-workspace".** The list is the podman mount
  table, and adding a directory to one without the other is the drift this
  proposal exists to end.

## Open Questions

**None — [`OQ-HT2`](#decision-ledger) closed 2026-09-11, and it was the last.** All four rulings are
in the [Decision Ledger](#decision-ledger) and folded into the sections they govern.


## Decision Ledger

| ID | Ruling / Decision | Date | Settled in |
| :--- | :--- | :--- | :--- |
| OQ-HT1 | The pack's declared `scope` decides the tier and this backend honors it: `scope: machine` → `packload.SharedDirs`, `scope: workspace` → `packload.WritableDirs`. Credentials are machine tier, history and transcripts are workspace tier. | 2026-09-11 | [§6 P2](#6-the-principles-this-rests-on) |
| OQ-HT2 | **No migration. Discard the old layout; wiping `/Users/_yolojail` is a supported reset.** *"Nobody is using it. No transition needed. If I need to wipe it first, that's fine."* The leaning's rename-then-copy-per-workspace scheme is machinery written for nobody — see the note below for what that gives up and why it is affordable. | 2026-09-11 | [§5](#5-the-proposal) |
| OQ-HT3 | Per-workspace, not per-session. The same-workspace overwrite is convergent, which is what the container's attach already relies on; per-session is a mechanism no other backend has. The container's courtesy flock ports as a moved call. | 2026-09-11 | [§5](#5-the-proposal), [§7 row C](#7-alternatives) |
| OQ-HT4 | **A′** — `HOME` stays `/Users/_yolojail`; every dir podman binds from `<ws>/.yolo/home/` becomes a symlink into it, and `SharedDirs` stay put and are mirrored back into the sidecar. A is the recorded runner-up. | 2026-09-11 | [§5](#5-the-proposal), [§7 row A′](#7-alternatives) |

> [!NOTE]
> **What discarding gives up, recorded because the leaning weighed it and the ruling overrides it.**
> The old shared home holds the **workspace tier** — pack `state` and agent history — for every
> workspace that ever launched on this backend, and agent transcripts are user work product in a way
> a re-fetchable token is not. That is why the leaning proposed copying rather than dropping.
>
> **The ruling accepts the loss on measured grounds: nobody has used this backend for real work.**
> Its only session was the 2026-09-11 hardware run, whose content is yolo-generated — staged skills,
> a briefing, a capture store entry. There are no transcripts to preserve. The precedent is already
> in the tree and was already priced: `linkThroughShared` accepted losing one login on a layout
> change (`internal/entrypoint/claude.go`).
>
> **What it buys the build:** [§5](#5-the-proposal) needs no migration step at all. The
> per-workspace sidecar is laid on a clean account, so A′ ships with no one-shot mutation, no
> `.pre-tiers-<date>` directory and no first-launch copy path. If the account has content when the
> split lands, `sudo rm -rf /Users/_yolojail` before the first launch **is** the migration.
