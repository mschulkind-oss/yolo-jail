---
title: "The macos-user Home: One Account, Three Tiers That Collapsed Into It"
date: 2026-09-03
status: draft
tags: [macos-user, jail-home, backend-parity, design]
summary: "macos-user has one sandbox home, /Users/_yolojail, so the machine tier, the workspace tier and the session tier are the same directory. That is deliberate for credentials and wrong for everything else — pack state, agent history, and now the composed skills and briefings a second workspace overwrites while the first is mid-session. This proposes a per-workspace home under an explicitly shared machine tier, and records what must be restored rather than merely split."
---

# The macos-user home: one account, three tiers that collapsed into it

**Status:** DESIGN SKETCH, 2026-09-03. Nothing built. Three open questions, and
[`OQ-HT2`](#OQ-HT2) blocks a sibling doc as well as this one.

**The short version.** `SandboxHome()` is the constant `/Users/_yolojail`, so three
tiers every other backend keeps apart are one directory. The **machine tier** is
right and is the point of a dedicated account; the **workspace** and **session**
tiers collapsing into it is the defect, and since content delivery landed it is a
write-write race rather than mere leakage. The fix is the two-tier structure the
container backends already have, inside the one account this backend has —
per-workspace state separated from machine-wide credentials, with symlinks carrying
whichever half does not live where the agent looks for it. **Where the per-workspace
half lives is [`OQ-HT4`](#OQ-HT4)**, and review moved the leaning: `<workspace>/.yolo/home/`,
which is where every other backend already puts it. **The trap is that a
naive split repairs the workspace tier by breaking the machine one** ([§3](#3-why-it-has-not-been-fixed-by-simply-splitting)).

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

**Reads with:** [`jail-home.md`](../reference/jail-home.md) (the container home layout this
should converge on), [`backend-parity.md`](backend-parity.md) (OQ-BP-2, the
delivery gap this follows), [`macos-user-nix-and-features.md`](../reference/macos-user-nix-and-features.md)
(the backend), and [`macos-revival-and-distribution-plan.md`](../plans/macos-revival-and-distribution-plan.md).

---

## 1. The shape of the problem

`macosuser.SandboxHome()` is the constant `/Users/_yolojail`. It has no workspace
component and no session component, so three tiers the other backends keep apart
are one directory here:

| Tier | Container backends | macos-user |
| :--- | :--- | :--- |
| **machine** — credentials shared by every jail | `~/.local/share/yolo-jail/home/` + per-agent shared-credential symlinks | `/Users/_yolojail` |
| **workspace** — pack `state` dirs, agent history, composed content | `<ws>/.yolo/home/`, bind-mounted | `/Users/_yolojail` |
| **session** — one launch's generated config | regenerated into the workspace overlay | `/Users/_yolojail` |

The machine tier is the one that is *right*: a single account holding one set of
agent credentials is the whole point of a dedicated sandbox user, and it is what
makes `shared_credentials` work here with no broker at all
([macos-user-nix-and-features.md](../reference/macos-user-nix-and-features.md) [§3.5](../reference/macos-user-nix-and-features.md#loopholes-mostly-moot-and-the-framework-ports-better)). The other two rows are the defect.

## 2. What the collapse actually costs

Three symptoms, in increasing order of how much they matter.

1. **Pack `state` dirs are machine-wide.** `.claude`, `.codex`, `.gemini`, `.pi`
   are per-workspace on every other backend. A session's history is visible to
   every other workspace you launch. Reported at launch by
   `noteMachineWideWorkspaceState`.
2. **The Seatbelt profile enforces a boundary the home then leaks.** The profile
   denies reading a sibling workspace's files — and
   `~/.claude/projects/<other-workspace>/*.jsonl` is readable anyway, because it
   lives in the shared home rather than under the workspace.
3. **Composed content races between concurrent launches.** This is the new one.
   Skills and briefings are now delivered by copying a composed overlay over the
   home on every entry. Two workspaces launched concurrently write the same paths,
   so the second replaces the first's — and a briefing is *per-project prose*, so
   an agent mid-session can go on reading a description of a different project.

Symptom 3 is qualitatively worse than 1 and 2. Those leak information between
workspaces; this one feeds an agent instructions for the wrong project, silently,
while it is working.

## 3. Why it has not been fixed by simply splitting

Splitting `SandboxHome()` per workspace repairs the workspace tier by **breaking
the machine tier**: the single home *is* the shared-credentials mechanism on this
backend. `~/.claude/.credentials.json` is shared because there is only one home to
put it in. Give each workspace its own and every workspace needs its own login.

So a fix has to restore both tiers explicitly, which is a design change and not a
launch-time patch — which is exactly why `noteMachineWideWorkspaceState` warns
instead of fixing.

## 4. What forced this

Content delivery (2026-09-03). Before it, the collapse cost information leakage
between workspaces — bad, but static. After it, every launch WRITES to the shared
home, so the tier collapse became a write-write race on files an agent reads as
instructions.

## 5. The proposal

Adopt the two-tier structure the container backends already have, in the one
account this backend has.

**Nothing here moves your projects, and `<workspace>/.yolo/` does not go away.**
`/Users/Shared/yolo` stays exactly as it is, and so does every workspace under it.

`<workspace>/.yolo/` is a THIRD path, and naming it is overdue: it is the
workspace's own sidecar — `prism/` (composed config surfaces), `config-snapshot.json`,
and `home/`, which on the container backends is the SOURCE of the per-workspace state
dirs bind-mounted into the jail (`<ws>/.yolo/home/claude` → `/home/agent/.claude`,
`assemble.go`). It is gitignored, and on this Mac it already holds `claude`, `config`,
`go`, `npm-global` and a `bash_history` from container runs. macos-user writes
`prism/` there and ignores `home/` entirely.

That last sentence is the design question this section used to answer without
asking. **The container does not move its home per workspace** — `HOME` is
`/home/agent` always, and specific *subdirectories* are mounted in from the
workspace. So there are two ways to give macos-user the same separation, and [§7](#7-alternatives)
weighs them as [`OQ-HT4`](#OQ-HT4).

- **`/Users/_yolojail/` stays the machine tier.** Credentials live here, exactly as
  now. Nothing about `shared_credentials` changes.
- **`/Users/_yolojail/workspaces/<cname>/` becomes the per-workspace HOME** — the
  home a launch on that workspace gets, not a copy of the workspace. `<cname>` is
  the same `runtime.FromWorkspace` slug pack staging and the Seatbelt profile
  already key on: a short name DERIVED from the workspace path, so a project at
  `/Users/Shared/yolo/yolo-jail` gets a home at
  `/Users/_yolojail/workspaces/yolo-yolo-jail/` while the project itself stays
  where it is. `HOME`/`JAIL_HOME` in the launch and bootstrap env point here.
- **The machine tier is reached by the mechanism it already uses: symlinks.**
  `configureSharedCredentials` already links `~/.claude/.credentials.json` to a
  shared target; the same links, pointed one level up, restore sharing explicitly
  rather than by accident of colocation.
- **The Seatbelt profile's writable set narrows** from `(subpath "/Users/_yolojail")`
  to the workspace home plus the shared credential paths, which makes symptom 2
  enforced rather than merely stated.

## 5.0 The constraint that outranks the layout choice: one mechanism, every backend

**Stated in review 2026-09-11, and it is the strongest argument in this
document — including against [§5](#5-the-proposal)'s own first proposal:**

> *"You can share [credentials] to the home, but if you don't, you have their own
> homes. It's going to be just identical to how you share them in container jails.
> It seems like we should have the same mechanisms across these things. If
> possible — otherwise you're just fragmenting the utility of this tool and you
> can't really share things, because you'd have to detect features and stuff and it
> would be awful."*

**That is a constraint on the answer, not a preference between answers**, and it
is why [`OQ-HT4`](#OQ-HT4) leans **A′** rather than A. The container backends
already solve credential sharing with a **machine-scope location plus symlinks**
(`configureSharedCredentials`); a per-workspace home that reached credentials some
*other* way would make "where are my credentials" a per-backend question, and
every pack that touches them would need to know which backend it is on. **Feature
detection in a pack is the failure mode** — the same class
[`backend-parity.md`](backend-parity.md) exists to make visible, where a mechanism
added on one backend is absent elsewhere with no error.

So the test any layout here must pass: **the symlink mechanism, the location, and
the thing a pack declares are identical on every backend.** What may differ is
only the *primitive that enforces the boundary* — a bind mount on podman, an SBPL
rule on macos-user — because that is invisible to a pack and to a user.

⚠ **This is also the axis on which [§5.1](#51-the-plumbing-already-exists--this-is-a-parameter-not-a-rewrite) through [§5.4](#54-seatbelt-can-replace-more-mounts-than-this-one)
below should be read.** Those sections were written about alternative **A** (a
per-workspace `HOME` under the shared account) before A′ was weighed. **Their
findings are about the enforcement primitive and survive either choice** — the
three call sites take a home parameter either way, and the `/Users` read-deny
narrows whatever home it is given. What they do *not* settle is which layout wins;
that is [`OQ-HT4`](#OQ-HT4), and the parity constraint above is the reason its
leaning is A′.

## 5.1 The plumbing already exists — this is a parameter, not a rewrite

**Measured 2026-09-11, in review.** `SandboxHome()` is a *default*, not a
constraint. Every place that needs a home already takes one as an argument and
falls back to the constant only when passed `""`:

| Call site | Signature | What a narrower home changes |
| :--- | :--- | :--- |
| `internal/macosuser/macosuser.go:461` | `SandboxPath(home string, prefix []string)` | every PATH entry (`.yolo/bin/block`, `.local/bin`, mise shims, …) |
| `internal/macosuser/macosuser.go:481` | `LaunchArgv(…, workspace, user, home string, …)` | the `HOME`/`USER`/`SHELL`/`PATH` quartet handed to `env -i` |
| `internal/macosuser/seatbelt.go:34` | `SeatbeltProfile(workspace, sandboxHome string, readonlyRels []string)` | the writable subtree **and** the `/Users` read re-allow |

So [§5](#5-the-proposal)'s per-workspace home is "pass a different string at three
call sites", not a re-architecture.

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
  `(subpath)` would. That comment is in the profile and the mechanism is live.
- **The home is the single remaining shared surface**, and it is shared only
  because every launch passes the same string. Narrow it and **reads narrow with
  it, for free** — the deny is already written; `(subpath <home>)` is the only
  thing re-allowing it.

### 5.3 What the credential tier then needs, precisely

[§3](#3-why-it-has-not-been-fixed-by-simply-splitting)'s trap — *"a naive split
repairs the workspace tier by breaking the machine one"* — now has an exact
location rather than a warning. Once `home` is per-workspace, the shared
credential store falls under the `/Users` deny like anything else, so it needs its
own re-allow, **for read and for write**: the OAuth path *refreshes* tokens, so a
read-only carve-out would break the thing it was meant to preserve. That is one
`(subpath …)` in each of the two lists, and it is the whole machine tier.

## 5.4 Seatbelt can replace more mounts than this one

**Raised in review 2026-09-11, and the pattern already has a shipped precedent in
this backend.** The reason macos-user drops features is stated everywhere as *"it
has no bind mounts"* — but a `:ro` bind does two separable things: it makes a file
*appear* at a path, and it makes that path *unwritable*. Seatbelt does the second
natively, and the **launcher runs outside the sandbox**, so it can do the first by
copying.

**The precedent is `workspace_readonly`**, and `SeatbeltProfile`'s own docstring
gives the argument:

> Why this exists: the key is delivered as a `-v …:ro` bind on the container
> backends (`internal/cli/run/mounts.go`), and macos-user has no mounts, so it used
> to accept the key and **silently do nothing**. A security key that lies is worse
> than one that refuses.

So one config key already took this route. The candidates that could follow:

| Feature | Container mechanism | macos-user equivalent |
| :--- | :--- | :--- |
| `host_files` `mode: readonly` | `:ro` bind + `0444` | launcher copies the file in; profile emits `(deny file-write* (literal <dest>))` |
| `host_files` `mode: once` / `copy` | staged write | already possible — no host layer needed, it is a copy |
| `reads-host` (the `host` layer) | `:ro` `/ctx` mount | launcher copies to a staging path; profile denies writes to it |

⚠ **And on `readonly` this would be STRONGER than the container backends, not a
degraded port.** `yolo config-ref` says of the container path: *"0444 is DAC, not
kernel enforcement: an agent running as root (Claude YOLO does) bypasses the mode
bits. It is a strong signal and a speed bump, not a sandbox."* A Seatbelt
`file-write*` deny is kernel-enforced regardless of uid. So the backend with "no
mounts" would be the one that actually enforces the promise.

**What Seatbelt cannot supply**, so the resemblance to a container jail stops
here: no PID, network or mount namespace; and every jail runs as the same
`_yolojail` uid, so a host daemon cannot tell which jail is calling — the reason
[`macos-revival-and-distribution-plan.md`](../plans/macos-revival-and-distribution-plan.md)
rejected the helper-outside-the-sandbox shape. Concurrent jails with *different*
profiles already work (each launch writes its own profile and passes
`sandbox-exec -f <path>`); what does not work is a macos-user jail launching
another one, which is an equality constraint in `sandbox_apply`, not a policy gap.

## 6. The principle this rests on

**P1. A split must restore every tier it breaks, explicitly.** Colocation is not a
mechanism. The machine tier works here *by accident* — credentials are shared
because there is only one directory to put them in — and any change that separates
the directories has to replace that accident with something stated. A fix that
repairs the workspace tier and leaves the machine tier to luck has moved the bug,
not removed it.

## 7. Alternatives

| Alternative | Verdict |
| :--- | :--- |
| **A. Per-workspace home under the shared account** ([§5](#5-the-proposal)) — `HOME` becomes `/Users/_yolojail/workspaces/<cname>` | **Now the runner-up**, see [`OQ-HT4`](#OQ-HT4). Keeps agent state out of the project tree, at the cost of putting it somewhere no other backend puts it. |
| **A′. Symlink the per-workspace dirs into `<ws>/.yolo/home/`** — `HOME` stays `/Users/_yolojail`, and `~/.claude` and kin become symlinks into the workspace sidecar | **Probably right, and it was not considered until review asked.** It is what the container backend already does, minus the mount: same LOCATION for a project's agent state on every backend, the same symlink mechanism `configureSharedCredentials` already uses for the machine tier, and credentials never move — which shrinks [`OQ-HT2`](#OQ-HT2) from "migrate an account" to almost nothing. |
| **B. Split the account** — one `_yolojail` uid per workspace | **Rejected**, in [§8](#8-risks). Restores every tier by DAC rather than layout, and costs admin on every new project. |
| **C. Per-session home** | **Rejected for now**, and it is [`OQ-HT3`](#OQ-HT3). Two launches on one workspace sharing a home is what attach does elsewhere; the difference is that macos-user has no attach. |
| **D. Leave it, keep warning** (today) | **Rejected as an end state.** It was defensible while the cost was leakage between workspaces. Content delivery made it a race on files an agent reads as instructions, which is a different kind of wrong. |

## 8. Risks

| Risk | Mitigation |
| :--- | :--- |
| Migration costs every user a re-login | The whole of [`OQ-HT2`](#OQ-HT2), and the reason it blocks. A migration that moves credentials without an auth dance is the bar. |
| The Seatbelt profile's writable set has to narrow in step | Same change, same commit — a per-workspace home with a `/Users/_yolojail`-wide writable set repairs nothing. |
| Two jails on one workspace still share | Accepted, and named as [`OQ-HT3`](#OQ-HT3) rather than left to be discovered. |

## 9. What this does NOT propose

Splitting the *account*. One `_yolojail` uid per workspace would restore every
tier by DAC rather than by layout, and it is the wrong trade: account creation
needs admin, `macos-setup` would become per-workspace, and the credential sharing
that motivates the single account would need a broker to cross uids — reintroducing
on macos-user exactly the mechanism this backend's design says it does not need.

## Open Questions

1. 💬 **OQ-HT1: Which paths are machine tier?** Credentials are certain. Agent
   *history* (`~/.claude/projects/`) is the interesting one: sharing it is symptom 1
   in [§2](#2-what-the-collapse-actually-costs), but cross-workspace history is a thing some people want. This decides how
   much of the shared home survives the split, and therefore how much the migration
   in [`OQ-HT2`](#OQ-HT2) has to move.

   _Leaning:_ Workspace tier, with no override until someone asks for one. History
   that leaks between projects is the reported defect; wanting it shared is a
   preference nobody has stated.

   <!-- vantage: oq id=OQ-HT1 leaning="Workspace tier, with no override until someone asks — history leaking between projects is the reported defect; wanting it shared is a preference nobody has stated." -->

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-HT2: What happens to an existing `/Users/_yolojail`?** A machine that
   has been running this backend has real credentials at the old paths. Migration
   has to move them to the machine tier without a re-login, or the fix costs every
   user an auth dance on upgrade. **This is the blocking question**: it gates this
   design, and it gates [OQ-P3](./macos-user-provisioning.md#OQ-P3) in
   [`macos-user-provisioning.md`](macos-user-provisioning.md), whose stage writes
   into the same home.

   _Leaning:_ A one-shot migration in `macos-setup` — already the command that owns
   this account's layout, already the place a user expects to wait, and already
   privileged. The alternative (migrate lazily on first launch) puts a one-time
   mutation on a hot path forever.

   <!-- vantage: oq id=OQ-HT2 leaning="A one-shot migration in `macos-setup`: already the command that owns this account's layout, already privileged, already where a user expects to wait. Migrating lazily on first launch would put a one-time mutation on a hot path forever." -->

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 **OQ-HT3: Is per-workspace enough, or is per-session needed?** Two launches
   on the SAME workspace still share a home. On the container backends that is
   exactly what attach does, so it is probably correct — but macos-user has no
   attach, so those two launches are genuinely independent processes rather than one
   jail re-entered.

   _Leaning:_ Per-workspace, treating concurrent same-workspace launches as the
   user's business the way an explicit stop-and-relaunch already does. Per-session would also
   multiply the migration surface in [`OQ-HT2`](#OQ-HT2) by every session ever run.

   <!-- vantage: oq id=OQ-HT3 leaning="Per-workspace, treating concurrent same-workspace launches as the user's business the way `yolo --new` already does. Per-session would also multiply OQ-HT2's migration surface by every session ever run." -->

   **Answer:**
   > _(empty — fill in when decided)_

4. 💬 **OQ-HT4: Where does per-workspace state live — under the sandbox account, or
   in `<workspace>/.yolo/home/`?** [§5](#5-the-proposal) assumed the first without noticing it was a
   choice. The second is what the container backends already do: `HOME` is fixed and
   the per-workspace dirs are mounted in from the workspace sidecar. macos-user has
   no mounts, but it has symlinks, and it already uses them for the machine tier.

   This decides how much [`OQ-HT2`](#OQ-HT2) has to migrate. Under A′ credentials never move, so
   the migration is per-workspace state only — or nothing at all, if a fresh symlink
   into an empty sidecar is acceptable.

   <!-- vantage: oq id=OQ-HT4 leaning="A′ — symlink into <ws>/.yolo/home/. Same location as every other backend, same symlink mechanism already used for the machine tier, credentials never move, and the directory already exists and is gitignored. The one thing A has over it is keeping agent state out of the project tree, which is not a property yolo preserves anywhere else." -->

   _Leaning:_ **A′.** Same location as every other backend, same mechanism already in
   use one direction over, and the directory exists and is gitignored today. The one
   thing A has over it — keeping agent state out of the project tree — is not a
   property yolo preserves anywhere else, so it is a principle invented to justify a
   choice rather than a reason for it.

   **Answer:**
   > _(empty — fill in when decided)_

## Decision Ledger

_(empty — no questions settled yet)_
