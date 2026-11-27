---
title: "One home per workspace, shared by every setup, with a small slice for what must differ"
date: 2026-11-23
status: accepted
stage: DECIDED
next: "Slice 1 (container setups) is built. Build slice 2, the guest joins ([its plan](unified-workspace-home-guest-plan.md)); the Mac agent runs W-UH1, W-UH5 and W-UH6 against main, and W-UH2 and W-UH3 before the alias slice is built"
tags: [design, setups, home, sessions, concurrency, migration, macos-user]
summary: "Every setup of a workspace shares one unified home under .yolo for sessions, history, plugins and every other state an agent keeps; each setup keeps a small slice (installs, the files yolo generates at boot, yolo's own state) at the path it has today. A setup's state directory becomes a real directory in its slice whose children link into the unified home. One container setup plus one guest may run together; two container setups are refused; stranded and legacy homes are merged, never lost. Sessions are never shared with the host, nor across workspaces unless the owner declares a set."
vantage:
  status-chip: true
---

# One home per workspace, shared by every setup

**Status:** 2026-11-23; [OQ-UH1](#OQ-UH1) to [OQ-UH5](#OQ-UH5) ruled 2026-11-24. **Slice 1 (container setups) built 2026-11-27** (`8bac7a046`, `a5662a9fa`, `08d8ef1a3`); guest slices 2 to 5 are not. Evidence was read at `09ba35cd9`, before the build
([Appendix A](#appendix-a-evidence)). What only the Mac can answer is UNMEASURED and named in
[§13.3](#mac-checks).

> **In short.** The per-setup home of [named setups](named-setups.md#9-state-per-setup) makes switching from Apple
> Container to Podman look like losing every session. The fix is not one shared directory but one **unified home**
> holding everything a setup can safely share, plus a small **slice** per setup holding only what must differ, joined
> by links yolo lays at launch.

**Why it matters.** Since `57ed9e5ae` every setup binds its own home, so a user who switches technology, or whose first
launch after the upgrade picked another setup, finds sessions, history and plugins in a home the new setup does not
read. The owner's colleague is in that state today.

**The shape.** The unified home (`.yolo/unified-home`), the slice (today's `.yolo/setups/<name>/home`), the machine
tier as today; one pure merge that fills the unified home from every home on disk; a launch rule built on `jailRuns`.

**Cost.** A launch lays links in each state directory; two container setups can no longer run at once in one
workspace; a guest and a container resume each other's sessions only through a per-agent alias nobody has measured.

**Start at [§3](#3-the-target-model)**, then [§5](#5-one-layout-and-how-a-setup-reaches-the-home).

**Ruled 2026-11-24:** [OQ-UH1](#OQ-UH1) to [OQ-UH5](#OQ-UH5), each as leaned; no question remains open.

**Reads with:** [`named-setups.md`](named-setups.md) (its section 9 and SU-D6 are what this amends),
[`named-setups-plan.md`](named-setups-plan.md) (SUP-D9 to SUP-D36, the move this generalizes),
[`jail-home.md`](../reference/jail-home.md) (mount stack and the layout split),
[`happy-path-principle.md`](../reference/happy-path-principle.md) (every refusal here names a next step),
[`no-symlink-guessing-principle.md`](../reference/no-symlink-guessing-principle.md) (the merge and the alias),
[`fill-the-matrix-principle.md`](../reference/fill-the-matrix-principle.md) (one mechanism per backend cell).

---

## 1. What the owner ruled, and what it asks

The owner's words are the only rulings behind this document (chat, 2026-11-23):

> "If you switch between container and podman. You should keep your sessions. I know we did this because we wanted
> multiple setups running in the same directory simultaneously, but let's revisit that decision and see if we can
> amend this a bit."

> "I want to get as close to a single home as possible. I don't think we'd ever want to share sessions between hosts
> and jails. That's going too far, and we don't want to share them between workspaces, probably. Although, if as a
> bonus we could declare sets of workspaces that share the same sessions within their jail... Maybe that's worth it.
> That would be purely a bonus. […] I just want to get as much as possible […] while still keeping multiple setups
> safe. The only real multi-setups that I could envision […] is having one container-based setup and one guest
> setup, like different users, seatbelt or whatever. I don't know that container and Podman is truly a use case, and
> we could certainly just say no you can't do that. I don't even know if the macOS user plus Podman is a real use
> case but I'd at least like to try to support it."

("Container and Podman" is Apple Container and Podman, from speech-to-text; the five rulings are rows UH-R1 to UH-R5
of the [ledger](#decision-ledger).) Earlier owner words still bind: the move is silent and waits for stopped jails
([OQ-SU5](named-setups.md#OQ-SU5)); the swap race under `.yolo` is an accepted risk
([OQ-SU7](named-setups.md#OQ-SU7)); and on 2026-11-06, *"right now basically all storage is in the workspace"*, which
is why the unified home sits under `.yolo` and not in a machine directory.

**Non-goals.**

- **No sharing of installs**, even between two Linux setups: the bytes are per technology and architecture.
- **No sharing of generated files**, which differ per setup by construction ([§4](#4-what-goes-where)).
- **No merging by content.** A file two homes both hold is kept twice ([§9.2](#92-the-merge)).
- **No change to machine-tier state**: logins and caches stay where they are.
- **No fix for a login kept inside a generated file** (Copilot's `config.json`): that pack must move it to a
  machine-scope directory.

## 2. Words used here

- **Setup** — a named set of walls and run-as account ([named setups](named-setups.md#3-the-noun)). A **container
  setup** runs in Podman or Apple Container. A **guest** runs as a macOS user under Seatbelt (`seatbelt-self`,
  `seatbelt-yolo-account`), no container. `yolo host` is neither and has no home of its own.
- **Unified home** *(coined here)* — the one directory per workspace, `<workspace>/.yolo/unified-home`, holding
  every path the setups of that workspace share, laid out as the agent sees them (`.claude/projects/…`). Not a
  `$HOME`: no jail uses it as one. Not the legacy `<workspace>/.yolo/home`, and not what the macos-user bootstrap
  calls its "workspace-home layout", which is the slice.
- **Slice** *(coined here)* — a setup's own home directory, `<workspace>/.yolo/setups/<name>/home`, at the path it has
  today: what that jail binds, or uses as `HOME`. After this design it holds only what must differ.
- **Generated path** *(coined here)* — a path yolo writes at boot: a pack's `config`, `skills`, `briefing` or `files`
  destination, a core file (bootstrap scripts, the `~/.yolo/bin` anchor, the shell rc) or an installed program's bytes
  (`paths.InstalledProgramSurfaces`, which prune and capture already use). Not what an agent writes by use.
- **Composed directory** *(coined here)* — a real directory in a slice standing in for a pack's state directory: its
  children are links into the unified home, except generated and carved-out paths, which stay real and private.

## 3. The target model

```text
<workspace>/.yolo/
  unified-home/            ONE per workspace, shared by every setup            (UH-D1)
    .claude/projects/…         sessions, history, plugins, caches: dotted, as the agent sees the path
    .codex/sessions/…  .pi/agent/sessions/…
  setups/<name>/             ONE per setup, as today
    home/                    the SLICE: bound (Podman, Apple Container) or HOME (guest)
      .claude/                 composed: settings.json real; projects -> unified-home/.claude/projects
      .local  .npm-global  go  .config  .yolo/bin  …    installs and generated files, real
    prism/  config-boot.json  config-assembled*.json  pack-files-mountpoints.json   this setup's render
  durable/  logs/  …         per project, as today
```

| Tier | Holds | Reached by |
| :--- | :--- | :--- |
| **Unified home** | Sessions, transcripts, history, plugins, caches and every agent file yolo does not generate | A link in each setup's composed directories |
| **Slice** | Install prefixes, generated files, yolo-internal state, databases until measured | The setup's own bind or `HOME`, as today |
| **Machine** | Logins (`.claude-shared-credentials`, …), `.cache`, the mise store, embedded packs | As today ([`jail-home.md`](../reference/jail-home.md)) |

The slice path does not move, so no install tree (18 GB in the measured home, [Appendix A](#appendix-a-evidence)) is
touched: the migration moves only the shared paths up, one rename each.

## 4. What goes where

The default is **shared** ([§6.2](#62-the-default-for-an-undeclared-path)): a path is in the slice only because it is
generated, an install, a database no one has measured, or carved out by its pack.

| State | Tier | Why |
| :--- | :--- | :--- |
| `.local`, `.npm-global`, `go`, the mise store, a pack's installed payload (`.codex/packages/standalone`, pi's `agent/npm`, `git`, `bin`, `tools`) | Slice | ELF on Linux, Mach-O on macOS: two setups would write incompatible bytes to one path |
| `~/.yolo/bin/block` and `launch`, bootstrap and venv scripts, perf and socat logs, `agent-env`, launcher receipts | Slice | Emptied and regenerated each boot (`resetAnchorDir`); a second boot would remove a live peer's blockers, which are first on `PATH` |
| `prism/`, `config-boot.json`, `config-assembled*.json`, the mountpoint record, the staged pack tree | Slice | Each describes what *this setup* rendered ([SUP-D35](named-setups-plan.md#SUP-D35), [SUP-D37](named-setups-plan.md#SUP-D37)) |
| core `.config`, `.ssh`, `bash_history`, `venv-shadows`, `writable-home` | Slice | Generated per setup; a single-file bind whose inode a link cannot replace; per-side by design; user-declared dirs of unknown content |
| `.cache`, `/mise`, `.*-shared-credentials` | Machine | Unchanged |
| **claude** `projects`, `file-history`, `todos`, `plans`, `shell-snapshots`, `session-env`, `paste-cache`, `plugins`, `mods`, `jail-history/`, and the caches (`stats-cache.json`, `policy-limits.json*`, `remote-settings.json*`, `cache`, `telemetry`, `backups`, `debug`) | Unified home | The sessions UH-R1 asks to keep; the rest is regenerable, and a list of names would rot. Native code in `plugins` is UNVERIFIED ([W-UH3](#mac-checks)) |
| claude `settings.json`, `.claude.json`, `yolo-managed-mcp-servers.json`, staged `skills` and briefing | Slice | Generated: the status line names `{yolo.setup}` and a guest renders other managed keys |
| claude `.credentials.json`, `history.jsonl` | Link per setup | Links a hook lays each boot (`shared_credentials`, `per_jail_history`); the targets are the machine tier and `jail-history/` |
| **codex** `sessions`, `auth.json`, history | Unified home | `auth.json` sits per workspace today; session layout is UNVERIFIED |
| codex `config.toml`, `yolo-model-list.json`, `AGENTS.md` | Slice | Generated |
| **copilot** `session-state/<uuid>/` | Unified home | One directory per session |
| copilot `session-store.db`, `-wal`, `-shm` | Slice | A database ([UH-D7](#decision-ledger)) |
| copilot `config.json`, `settings.json`, `lsp-config.json`, `mcp-config.json` | Slice | Generated; `config.json` also holds the login, so the login does not follow |
| **pi** `agent/sessions`, `trust.json`, `run-history.jsonl` | Unified home | Sessions and history |
| pi `agent/settings.json`, `models.json`, `mcp.json`, `yolo-*.json`; `.yolo-sessions/<id>/` | Slice | Generated; per-terminal profile dirs, not transcripts |
| **omp** `sessions/`, `blobs/`, `memories/`, `agents/`, … | Unified home | Sessions |
| omp `*.db`, `models.yml`, `config.yml` | Slice | Databases; generated |
| **agy** `projects.json`, `trustedFolders.json`, `oauth_creds.json`, other children | Unified home | `oauth_creds.json` is outside the shared credentials dir today, so it follows the workspace |
| agy `antigravity-cli/settings.json`, `mcp_config.json`, `cache/onboarding.json` | Slice | Generated |
| **muse-code** | Machine | Declares only a machine-scope credentials dir |
| **opencode, kilo, muse-planner** | Slice | Declare no state directory (files under `.config` and `.local`); their sessions follow once the pack declares one |
| A fetched or third-party pack's `[[state]]` | Unified home | The default applies; the pack may carve out |

Sources: the packs' `[[state]]`, `[[config]]` and `[[hook]]` declarations (`rg -n '^\[\[state\]\]' -A3
packs/*/pack.toml`) and the measured home in [Appendix A](#appendix-a-evidence). A name not in a declaration (codex
`sessions`) is UNVERIFIED until [W-UH2](#mac-checks) lists it.

## 5. One layout, and how a setup reaches the home

### 5.1 The dotted and undotted split, settled

Podman binds each child of the home separately from a dot-stripped source (`claude/`, mounted at `~/.claude`).
Apple Container binds the whole slice and the guest uses it as `HOME`, so both keep dots (`.claude/`)
(`wsStateHomeRel`, [`jail-home.md`](../reference/jail-home.md#backend-differences),
[MMP-D5](macos-user-model-plan.md#MMP-D5)). A home laid by one shows no sessions to the other; that is why a switch
loses them.

**One canonical layout: the unified home is dotted and mirrors the path the agent sees** ([UH-D3](#decision-ledger)).
**Slices keep the layout their backend reads.** The two never meet by name: a link in a composed directory is named for
the child (`projects`), which has no dot to strip. Only the composed directory's own name (`claude/` or `.claude/`)
differs, and that is the slice's business. Making slices uniform was rejected: Podman's dot-strip belongs to its `:ro`
skeleton ([`DIR-BH2`](../reference/jail-home.md#dir-bh2)) and Apple Container's whole-home bind to its mount-count
limit, and either change renames install trees for no sharing gain.

### 5.2 Composition: what a launch does to its own slice

For each workspace-scope state directory `D` of a **selected** pack the launch makes `D` a composed directory in its
slice and links every shared child to the unified home. The plan is a pure function over directory listings, and a
thin shell applies it.

1. Ensure `unified-home/D` exists.
2. Each **real** child of the slice's `D` that is not generated, not carved out and not a link moves into
   `unified-home/D` by a rename that refuses to replace, or merges ([§9.2](#92-the-merge)) when the name exists
   there; a link takes its place.
3. Each child of `unified-home/D` absent from the slice's `D` gets a link.
4. A child that is a **prefix** of a generated or carved-out path is itself composed (pi's `agent/` holds shared
   `sessions/` and private `npm/`).
5. A link already in the slice (a credential link, `history.jsonl`, the user's own) is never adopted, followed or
   rewritten; it stays per setup.

| Backend | The composed directory is reached by | A link names |
| :--- | :--- | :--- |
| Podman | the slice's `claude/` bound at `~/.claude`, as today | `/workspace/.yolo/unified-home/.claude/projects` |
| Apple Container | the whole slice bound at `/home/agent` | the same absolute path (the workspace is at `/workspace`) |
| Guest | the slice is `HOME` | relative to the link's own directory (`../../../../unified-home/.claude/projects`), so a moved workspace keeps it ([UHG-D2](unified-workspace-home-guest-plan.md#UHG-D2)) |

Links, not mounts: Apple Container has a mount-count limit and the guest has no mounts, so a link is the one mechanism
all three hold ([fill the matrix](../reference/fill-the-matrix-principle.md)). Podman keeps binding the composed
directory because its home is a `:ro` skeleton.

**Failure and degenerate cases.**

- **A slice that cannot hold links, or `workspace_readonly` covering `.yolo`:** composition declines, the setup keeps a
  private whole state directory as today, and the launch prints one line saying so; it never refuses.
- **No selected pack declares a state directory:** nothing is composed.
- **A rename that cannot run (EXDEV, no refuse-to-replace rename):** that child stays and the line names the one `mv -n`.
- **A child an agent creates later** stays real in the slice until the next launch adopts it; nothing is lost, it
  reaches the other setups later.
- **A deselected pack's links** stay in the slice, as a leftover directory does today; `yolo check` lists them.
- **One writer:** a launch writes only its own slice and adds to the unified home; it never deletes or overwrites.

Host-side readers (prune, the agent-log purge, `yolo config`, the Claude view) would follow an absolute `/workspace/…`
link to nothing; they read agent state from the unified home and install state from the slice, which amends
[SUP-D34](named-setups-plan.md#SUP-D34).

Alternatives, each with its verdict, are in [Appendix B](#appendix-b-alternatives-considered).

## 6. The declaration change

### 6.1 What `scope = "workspace"` now means

`[[state]] at = ".claude"` with the default `scope = "workspace"` now declares a **composed directory whose shared
children live in the unified home**. No shipped pack changes: every one already says `workspace`, so their sessions
become shared at once ([UH-D2](#decision-ledger)). `scope = "machine"` is unchanged. Generated paths need no
declaration: yolo knows every destination a selected pack renders and every core file it writes, so those are slice by
construction.

### 6.2 The default for an undeclared path

**Shared.** A wrongly shared generated file costs one re-render at the next boot; a wrongly split transcript
directory costs the user's sessions silently, which is the stranding bug this design ends. A list of shared names
would rot as agents add directories and fetched packs would never join it. Every backend and pack then behaves the
same with no per-pack work ([backend parity](backend-parity.md)).

### 6.3 The carve-out key

```toml
per_setup_state = [{ dir = ".pi", paths = ["agent/npm", "agent/git", "agent/bin", "agent/tools"] }]
```

A **top-level** manifest key, as `session_homes` is: an older yolo's decoder ignores an unknown top-level key but
drops a whole contribution that carries an unknown field (`internal/packdecl/sessionhome.go`, SP-D13). Names are
`dir`-relative and exact, `/` between segments, no `..`, no absolute path, no glob (the validator
`SessionHomeNameProblem` exists). A pack with nothing to carve out says nothing. An older yolo shares and composes
nothing, which is today's behavior.

**Databases are carved out until measured** ([UH-D7](#decision-ledger)): a SQLite store used from a container's VM and
a macOS process crosses two kernels and a virtual filesystem, where lock behavior is unmeasured. Copilot's
`session-store.db` and omp's `*.db` declare it.

### 6.4 DIR-BH1: no tension

[`DIR-BH1`](../reference/jail-home.md#dir-bh1) (*"a non-selected pack can never have an impact"*) worried that a home
bound whole, as on Apple Container and the guest, would show packs the setup did not select. The unified home is never
bound or used as a home, and composition lays links only for the **selected** packs' directories. An unselected pack's
directory there is reachable only by the path any workspace file is, `/workspace/.yolo/unified-home/…`, as other
setups' slices are today ([OQ-SU7](named-setups.md#OQ-SU7)); it has no impact on the jail's home. Name reservation
still follows the selection ([`OQ-BH14`](../reference/jail-home.md#oq-bh14)). `DIR-BH2` rejected a machine-wide base
home shared across workspaces; this is per workspace and is no base.

## 7. Running together

### 7.1 The rule

At most one **container** setup runs per workspace. A container setup and a **guest** may run together. A second
guest is refused ([OQ-UH1](#OQ-UH1)). The same setup twice is not a peer: it attaches or opens another session as
today ([UH-D5](#decision-ledger)).

| This launch | A peer running | Result |
| :--- | :--- | :--- |
| container | container (either runtime) | Refuse |
| container | guest | Allow |
| guest | container | Allow |
| guest | guest | Refuse ([OQ-UH1](#OQ-UH1)) |
| any | the same setup | Attach, unchanged |
| any | a peer that cannot be asked | Refuse, unless every keeper and session record says not running and only the runtime could not be asked: then proceed with one line ([OQ-UH3](#OQ-UH3)) |

Two container setups share the most (the same `/workspace` slug, the same generated-state races, the same per-workspace
host services) and nobody asked for the pair. A refusal is cheap to loosen and unsafe to tighten after a bad write.
macos-user plus Podman, the pair the owner wants tried, is allowed.

### 7.2 Where it runs

The answer exists: **`jailRuns(cname)`** returns `running`, `stopped` or `unknown` from the keeper flock, the session
records and a query of each runtime on `PATH`, for the container key and the guest keys alike. Three changes, no new
probe:

1. It also returns the **class** of the witness that answered (container key or runtime, versus a guest key), so a
   peer's class comes from what ran and not from a setup definition that may since have been removed.
2. The check runs at the **commit point**, beside `commitHome`, after every refusal and before the first write, and not
   on attach (the same name is found earlier in `Run`).
3. The **project lock** (`holdHomeLock`), taken today only while a legacy home exists, is taken by every fresh launch
   and held until the jail answers, so two launches of different setups cannot both pass. Launches of different
   setups of one workspace queue through jail-up; that is the cost.

**Unknown refuses**, as the attach decision does, naming the command that settles it, with one exception
([OQ-UH3](#OQ-UH3)): when every keeper lock and session record says the peer is not running and only its runtime
could not be asked (a stopped Podman machine), the launch proceeds and prints one line naming the setup it could
not ask. The accepted miss is a jail whose keeper died while its runtime is unreachable. A refusal reads:

> Refusing to launch: could not ask Podman whether `podman-self` is running for this workspace. Run `podman ps -a`
> to see, then launch again.

A known peer:

> Refusing to launch: `podman-self` is running for this workspace and shares its home with `apple-container-self`.
> Stop it with `yolo stop --setup podman-self`, then launch again.

No lock file in `.yolo` and no new hatch. The liveness facts are kernel flocks that die with their holder, so a crashed
jail leaves nothing stale; a file in `.yolo` is jail-writable and cannot be flocked reliably across the Apple Container
VM boundary. A jail whose keeper died is caught by the runtime query (`refuseUnkeptJail`,
[JL-D13](jail-lifetime-last-session-wins.md#JL-D13)). The rule extends
[JL-D28](jail-lifetime-last-session-wins.md#JL-D28), one keeper per name, to one container class per workspace.

**Race.** Launch X passes under the project lock and starts its keeper before releasing it; launch Y then sees X's
keeper flock. That ordering is an inference; the builder pins it with a test.

### 7.3 What stays per setup so a container and a guest are safe together

Install prefixes and native trees; `~/.yolo/bin` and its generated dirs, the staged pack tree, the prism baseline and
overlay, `config-boot.json`; every generated surface; databases and the in-home flock files (they serialize one jail's
sessions, not two kernels); `bash_history` and `.ssh`. The pair shares only the unified home: transcripts, history and
files an agent writes by use, added by rename or written in place. Two live agents write different files, and a file two
homes both hold is kept twice.

### 7.4 The yolo account

`seatbelt-yolo-account` runs as another macOS user. Its slice has an inherited ACL
([SUP-D31](named-setups-plan.md#SUP-D31)); the unified home must too, or the account cannot write what the user
created and the reverse. Until [W-UH4](#mac-checks) shows both directions work, that setup keeps a private whole
state directory and says so on the launch line ([UH-D13](#decision-ledger)).

## 8. The guest's path to the workspace

Containers see the workspace at `/workspace`; the guest sees the real path (`/Users/x/code/proj`). An agent that keys
sessions by directory name writes different names on the two. Mirroring the real path into containers is ruled out
([`workspace-path-mirroring.md`](../reference/workspace-path-mirroring.md), [OQ-WP8](../reference/workspace-path-mirroring.md#OQ-WP8)),
as is a root `/workspace` on macOS (`/etc/synthetic.conf`, a reboot, per machine).

| Agent | How it keys a session | Status |
| :--- | :--- | :--- |
| claude | `projects/<path with / as ->`, so `-workspace`; a subdirectory has its own slug | Shape MEASURED in a listing here; the full rule (dots, non-ASCII, length) UNVERIFIED |
| pi | `sessions/--<path mangled>--/` | MEASURED in a listing here; the rule UNVERIFIED |
| copilot, agy | a `cwd` field in a file, not a directory name; agy's `projects.json` maps `/workspace` to a name | MEASURED; whether resume filters by it UNVERIFIED |
| codex, omp, opencode, kilo, muse | not evidenced | UNVERIFIED |

Containers always agree, so Podman and Apple Container need no path work. The guest is the one case.

**Recommendation: a yolo-owned alias set in the shared store** ([UH-D10](#decision-ledger)). For each directory-keyed
agent path, a guest launch lays beside the container's `-workspace` entry a relative link named for the guest's
real-path slug that points at it (`-Users-x-code-proj → -workspace`), plus one per subdirectory slug found by listing
children with the container prefix. The container's slug is canonical because `/workspace` is the same on every
machine and the real path is not.

- **A pointer, never data.** Deleting an alias loses nothing.
- **Self-healing.** A real directory at an alias name (an agent replaced the link) merges into the canonical one by
  [§9.2](#92-the-merge) and the link is laid again at the next launch.
- **Opt-in per pack**, since core knows no agent: a pack declares `cwd_keys = [{ dir = ".claude/projects", scheme = … }]`,
  a scheme core names only after it is measured. Without it the guest keeps its own sessions.
- **Prefix safety.** Matching by prefix is sound because the unified home is per workspace, so every slug is a cwd
  inside it. With sets ([§11](#11-workspace-sets-a-later-slice)) each member needs its own prefix.
- **Symlink principle.** [No symlink guessing](../reference/no-symlink-guessing-principle.md) governs a user's file that
  is already a link. The alias is a link yolo lays by a name rule it owns, in a store it owns; yolo never follows or
  interprets a link it did not lay. The owner ruled that reading ([OQ-UH4](#OQ-UH4)): yolo may lay these links.

Three agent behaviors decide whether aliases work: does the agent follow a symlinked project directory, does its
cleanup replace the link with a directory, does it resolve the path and write the real name (harmless).
[W-UH2](#mac-checks) and [W-UH3](#mac-checks) measure them. **Until measured, or if the agent does not follow
the link, the guest keeps its own sessions** and containers share theirs; nothing else here waits on it.

Other ways to give the guest a stable path are in [Appendix B](#appendix-b-alternatives-considered).

## 9. Migration: one merge for every home on disk

### 9.1 What exists, and what the merge reads

A workspace may hold the legacy `.yolo/home` and any number of `.yolo/setups/<name>/home` (`paths.WorkspaceHomeStates`).
A source may hold `claude/` (Podman), `.claude/` (Apple Container, guest) or both, as an Apple Container home does after
the bug `09ba35cd9` fixed. Every source is treated alike, and for each **selected** pack's directory `D` both spellings
are read. There is no table of shipped directory names ([UH-D9](#decision-ledger)): an unselected pack's directory stays
where it is, and `packload.Embedded*` is not read to reserve a name. Composition ([§5.2](#52-composition-what-a-launch-does-to-its-own-slice))
is this merge with the launching slice as the source; the same function runs over the other sources, so a stranded
setup's sessions arrive the same way.

### 9.2 The merge

Per child of `D`, walking beneath `paths.OpenWorkspaceHomeRoot` (which refuses a link at `.yolo` or below) with
`O_NOFOLLOW`:

1. **Absent at the destination:** a rename that refuses to replace. Most of a transcript tree takes this branch: one
   rename, no copy, no second disk copy.
2. **A directory on both sides:** recurse; the result is the union.
3. **Regular files, identical bytes** (size, then hash): already present; the source is left and counted.
4. **Regular files, different bytes: keep both.** The newer by modification time keeps the name; the other becomes
   `<name>.from-<setup>` (`.2`, `.3` on a further clash). An agent that lists by extension stops listing it, so no
   duplicate session appears, yet no byte is gone. A prefix-subsumed shorter transcript is not dropped: "every input
   byte string still exists" is simpler and stronger.
5. **A link on either side, or a file against a directory:** moved as a link under its own name, never followed or
   read ([no symlink guessing](../reference/no-symlink-guessing-principle.md)); a clash keeps both.
6. **Sockets, fifos, lock and pid files:** left and counted.
7. **Never merged by content:** `settings.json`, `.claude.json`, `history.jsonl`, databases.

**Properties.**

- **Never loses a file.** Nothing is deleted or overwritten; every step is a rename or a skip.
- **Union**, order independent up to which of two colliding files keeps the name.
- **Crash-convergent.** The source tree is its own worklist, a half-merged unified home is a valid one, and a rerun
  continues; the refuse-to-replace rename and the `.from-` suffix make a second pass a no-op.
- **Silent at launch**, through the plan and commit split of [`commitHome`](named-setups-plan.md#SUP-D9) (`09ba35cd9`):
  `selectHome` plans and mutates nothing, each arm commits after its last refusal and before its first write, so a
  declined or failed launch moves nothing. One informational line, also in `launch.log`, names counts per source and any
  `.from-` copies; no prompt ([OQ-SU5](named-setups.md#OQ-SU5)).
- **Refused while any jail runs** for the legacy home and every *other* setup's slice; "could not ask" counts as
  running and the line names `yolo stop`. The launching setup's *own* slice is composed on every launch: it is stopped
  and the merge only adds to the unified home. A deferral loses nothing; the sessions appear at the next launch with
  no jail running.
- **Also left, with the `mv -n` to run:** a link at `.yolo` or on the `setups` chain; a source that is not a real
  directory owned by the user (or, for the yolo account, by it, [SUP-D31](named-setups-plan.md#SUP-D31)); a source that is
  its own mount.

### 9.3 The legacy home, in two steps

1. **Shared children** of every selected pack's directories merge into the unified home, unconditionally, so sessions
   never wait behind an attribution refusal.
2. **The remainder** (installs, generated files) moves to the attributed setup's slice by the existing
   `adoptLegacyHome` rules, which `09ba35cd9` made late and layout-aware. [SUP-D20](named-setups-plan.md#SUP-D20)'s
   attribution now covers only this remainder.

### 9.4 Stranded users, and what they see

Merging covers every home on disk, so a user whose sessions sit in the wrong setup's home needs no special case and no
knowledge of which release stranded them. Sam's workspace holds `podman-self/home/claude/projects/b.jsonl`, and
`apple-container-self/home` holds `.claude/projects/c.jsonl` and a Podman-laid `claude/projects/a.jsonl`. The first
launch with no jail running leaves `unified-home/.claude/projects` holding a, b and c, and prints one line such as
"Merged 3 sessions into this workspace's shared home." With a jail running it names the source that waits and
`yolo stop`. `yolo check` shows what waits, a read-only plan. A launch is the only thing that merges; there is no
`yolo home merge` verb ([OQ-UH2](#OQ-UH2)).

### 9.5 What is reused

The plan and commit split with its three pin tests, `projectJails` and `jailRuns`, `holdHomeLock`,
`OpenWorkspaceHomeRoot` and its no-link chain, `renameNoReplaceBetween` and the `legacyHomeRename` test seam, the
`left(...)` message shape, the owner check. The merge is a pure package taking an open source root, a destination root
and a classifier; `legacyhome.go` becomes its caller. Not reused: whole-home attribution as a verdict on sessions, and
[SUP-D21](named-setups-plan.md#SUP-D21)'s "keep using the legacy home until moved": a deferral now binds the setup's
slice, and the merge later is a union over a populated unified home.

## 10. Host and jail never share

Sessions are never shared between `yolo host` and a jail (UH-R3). Today they are not: the host notch renders into the
real `$HOME` and has no setup home, and a guest's `HOME` is its slice even in run-as-you mode (`seatbelt-self`). The
design keeps that by construction:

- **One path function.** Only `paths.UnifiedHome(workspace)` names the unified home, under `<workspace>/.yolo`; no
  call takes `$HOME`.
- **Link targets** are confined to `<workspace>/.yolo/unified-home`.
- **The host renderer** does not import the composition package.
- **Seeding.** `basehome` seeds a jail from host state and reads `.claude/projects` only to exclude it.

**The test, written first.** Over every built-in setup, through `Run`'s commit point and not only the callee: a host
setup yields no composed directory and no path under the unified home; a jail setup's every link target resolves under
the unified home; a guest's `HOME` is under `.yolo/setups`, never the invoking user's home; and an import test fails
if `internal/render` or the host-apply path reaches the composition package. It fails if the call site is deleted.

## 11. Workspace sets, a later slice

The owner's bonus needs nothing from the core except that a link's target is a variable.

- **Declared in user config**, since a set names several workspaces and the ceiling lives there
  ([config scopes](../reference/config/README.md)): `workspace_sets`, a name and workspace paths. A project may ask for
  membership with `yolo request`; only the user's config grants it.
- **A pack declares which children are sessions** (`session_paths`), the one place a list is needed, because a set
  shares sessions and not plugins or settings ([UH-D12](#decision-ledger)).
- **The store** is the machine tier, `~/.local/share/yolo-jail/sets/<name>/`, outside every workspace, so
  `refuseWorkspaceScope` is untouched and a workspace cannot read a set it is not in.
- **The mechanism:** a member's composed directory links its `session_paths` children to the set store instead.
- **Semantics.** In containers every member is `/workspace`, so a set is a merge: `-workspace` is one list for all
  members and resume shows every member's sessions. For a guest each member's real-path slug aliases the canonical one.
- **Joining is one-way:** a member's existing sessions merge in and cannot be told apart by name after it leaves. The
  launch that applies a new set says so before it merges.
- **Host guard:** the store is never resolvable from the real home; the [§10](#10-host-and-jail-never-share) test extends.

## 12. Rulings this amends

| Ruling | Who | This design |
| :--- | :--- | :--- |
| [SU-D6](named-setups.md#SU-D6) and the "two setups never share a home; run side by side" of its section 9 | Agent; no owner quote, and the owner's goal was config (2026-11-04) | **Superseded**; section 9 gets a pointer |
| [OQ-SU5](named-setups.md#OQ-SU5), [OQ-SU7](named-setups.md#OQ-SU7) | **Owner** | **Stand.** Only the destination changes; the owner is asked to note it, not re-argue |
| [DIR-BH1](../reference/jail-home.md#dir-bh1), [OQ-BH14](../reference/jail-home.md#oq-bh14), [OQ-BH15](../reference/jail-home.md#oq-bh15), [DIR-BH2](../reference/jail-home.md#dir-bh2) | **Owner** | **Stand**, satisfied ([§6.4](#64-dir-bh1-no-tension)) |
| [SUP-D31](named-setups-plan.md#SUP-D31) | **Owner** ("yes, A") | **Stands** for the owner check on sources |
| [OQ-WP8](../reference/workspace-path-mirroring.md#OQ-WP8), no mirroring | **Owner** | **Stands**; the alias avoids mirroring |
| [SUP-D9](named-setups-plan.md#SUP-D9), [SUP-D21](named-setups-plan.md#SUP-D21) | Agent | **Superseded**: the merge, and a deferral binds the slice |
| [SUP-D20](named-setups-plan.md#SUP-D20) | Agent | **Amended**: attribution covers only the remainder |
| [SUP-D33](named-setups-plan.md#SUP-D33), [SUP-D35](named-setups-plan.md#SUP-D35) | Agent | **Moot** for slices, which never move; true of the legacy remainder |
| [SUP-D34](named-setups-plan.md#SUP-D34) | Agent | **Amended**: agent state is read from the unified home, install state from the slice |
| [SUP-D36](named-setups-plan.md#SUP-D36) | Agent | **Extended** to every fresh launch ([§7.2](#72-where-it-runs)) |
| [MMP-D5](macos-user-model-plan.md#MMP-D5), [OQ-BH12](../reference/jail-home.md#oq-bh12) | Agent | **Kept**; "the home" now means the slice |
| [MMP-D32](macos-user-model-plan.md#MMP-D32) | Agent | **Narrowed** to the legacy home a guest can still meet |
| [JL-D28](jail-lifetime-last-session-wins.md#JL-D28) | Agent | **Extended** to one container class per workspace |

Prose that calls `.yolo/home` or a per-setup home THE per-workspace state, and the guide lines saying installs and logins
are kept with the project, are rewritten when the design lands (`jail-home.md`, `storage-and-config.md`,
`macos-user-home-tiers.md`, `settings-per-setup.md`). "Session home" in [per-session profiles](per-session-profiles.md)
means a per-terminal config directory and is unrelated.

## 13. Verification

### 13.1 Unit tests (Linux, in process, no containers, no sleeps)

On `EvalSymlinks(t.TempDir())` trees, test first, events recorded through the rename seam.

- **Conservation.** A randomized tree of sources with collisions: every input byte string exists afterwards in the
  unified home, a `.from-` name or a left source.
- **The three-home case** of [§9.4](#94-stranded-users-and-what-they-see); identical, different and type-clash
  collisions; two successive `.from-` names.
- **Links:** one in a source and one on the `setups` chain; a sentinel target outside `.yolo` stays byte-identical and
  the link moves as a link (the shape of `TestALinkedConfigIsMovedAndItsTargetIsNeverTouched`).
- **Idempotence:** fail the Nth rename through the seam, rerun, same union; loop over N.
- **Composition:** shared children linked, generated and carved-out paths real, a prefix carve-out composed
  recursively, a user link neither adopted nor followed.
- **The rule:** each row of the [§7.1](#71-the-rule) table through a fake `jailRuns`, unknown included.
- **Call sites:** a test fails if an arm's merge is deleted or hoisted above the config prompt (the shape of
  `TestEveryArmCommitsTheHomeBeforeItWritesIt`), and if the exclusion check is deleted from the commit point.
- **Refusals:** running, unknown, linked chain, wrong owner and EXDEV each move nothing and print a runnable command.
- **The host guard** of [§10](#10-host-and-jail-never-share).

### 13.2 Integration (nested jail, Podman)

A `podman-self` launch composes its slice and the next launch finds the sessions through the links. With a held keeper
flock for a fake `apple-container-self` jail, a `podman-self` launch is refused naming `yolo stop --setup
apple-container-self`; with a held `.guest` key it proceeds (the flock is the event, no timing). A prepared
stranded-homes workspace migrates. A nested jail cannot verify reachability or rootless-only paths and nothing here
depends on either. The full suite runs once per landing, on the combined tree.

The Mac checks, W-UH1 to W-UH6, are [their own section](#mac-checks) below.

## 14. Slices

Smallest finished first; each lands alone.

1. **Merge and composition for container setups, with the exclusion rule:** the pure merge, the unified home,
   composition for Podman and Apple Container, the stranded-home migration, the project lock for every launch, the
   two-container refusal and the host guard test. It gives the owner's first want. Mac: W-UH1, W-UH5, W-UH6.
2. **The guest joins:** links with relative targets, container plus guest allowed, the yolo-account gate. Mac: W-UH4, W-UH5, W-UH7 to W-UH10. Built by [its plan](unified-workspace-home-guest-plan.md).
3. **The alias set** ([OQ-UH4](#OQ-UH4)), gated on W-UH2 and W-UH3.
4. **Workspace sets** ([§11](#11-workspace-sets-a-later-slice)).
5. **Retire the stale prose** of [§12](#12-rulings-this-amends); graduate this document to a reference.

Generated settings files stay per setup ([OQ-UH5](#OQ-UH5)); no slice moves them. The build hand-off for slice 1 is
[`unified-workspace-home-plan.md`](unified-workspace-home-plan.md) and for slice 2
[`unified-workspace-home-guest-plan.md`](unified-workspace-home-guest-plan.md); slices 3 to 5 have no plan yet.

## Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="UH-R1"></a>UH-R1 | **Switching setups keeps your sessions.** The owner: *"If you switch between container and podman. You should keep your sessions."* | 2026-11-23 | [§1](#1-what-the-owner-ruled-and-what-it-asks) | Built for container setups (slice 1); the guest is slice 2 |
| <a id="UH-R2"></a>UH-R2 | **As close to a single home as possible.** The owner: *"I want to get as close to a single home as possible."* | 2026-11-23 | [§3](#3-the-target-model) | Built for container setups (slice 1) |
| <a id="UH-R3"></a>UH-R3 | **Sessions are never shared between the host and jails.** The owner: *"I don't think we'd ever want to share sessions between hosts and jails. That's going too far."* | 2026-11-23 | [§10](#10-host-and-jail-never-share) | Built (slice 1): `TestHostSetupNeverReachesTheUnifiedHome`, `TestHostRendererDoesNotImportTheComposition` |
| <a id="UH-R4"></a>UH-R4 | **Not shared across workspaces by default; declared sets are a bonus.** The owner: *"we don't want to share them between workspaces, probably. Although, if as a bonus we could declare sets of workspaces… That would be purely a bonus."* | 2026-11-23 | [§11](#11-workspace-sets-a-later-slice) | — |
| <a id="UH-R5"></a>UH-R5 | **The real multi-setup is one container setup and one guest; container plus container may be refused; macos-user plus Podman is tried.** The owner's words in [§1](#1-what-the-owner-ruled-and-what-it-asks). | 2026-11-23 | [§7](#7-running-together) | Container plus container refused (slice 1); the guest half is slice 2 |
| <a id="UH-D1"></a>UH-D1 | **The unified home is `<workspace>/.yolo/unified-home`; the slice stays at `.yolo/setups/<name>/home`.** A design decision: a new name cannot be mistaken for the legacy `.yolo/home`, which is recognized by name, never by sniffing, and the unchanged slice path moves no install tree. | 2026-11-23 | [§2](#2-words-used-here), [§3](#3-the-target-model) | Built (slice 1): `paths.UnifiedHome` |
| <a id="UH-D2"></a>UH-D2 | **`scope = "workspace"` means the unified home; generated paths are the slice; `scope = "machine"` is unchanged.** A design decision: no shipped manifest changes, and yolo already knows every generated path. | 2026-11-23 | [§6.1](#61-what-scope--workspace-now-means) | Built (slice 1): `stayPaths` reads each selected pack's declarations |
| <a id="UH-D3"></a>UH-D3 | **The unified home is dotted and mirrors the in-jail path; slices keep their backend's layout.** A design decision: only links cross the boundary and they are named for the child. | 2026-11-23 | [§5.1](#51-the-dotted-and-undotted-split-settled) | Built (slice 1) |
| <a id="UH-D4"></a>UH-D4 | **A setup reaches the unified home by links inside a composed directory,** an absolute `/workspace` target in containers and a relative target in the guest ([UHG-D2](unified-workspace-home-guest-plan.md#UHG-D2)); Podman binds the composed directory. A design decision: one mechanism all three backends hold, with no extra mounts. | 2026-11-23 | [§5.2](#52-composition-what-a-launch-does-to-its-own-slice) | Built for container setups (slice 1); the guest's relative link is slice 2 |
| <a id="UH-D5"></a>UH-D5 | **At most one container setup per workspace; container plus guest allowed; same-setup attach unchanged.** The owner's UH-R5 words, built on `jailRuns` with the witness class and the project lock for every fresh launch; no lock file in `.yolo`. | 2026-11-23 | [§7.1](#71-the-rule), [§7.2](#72-where-it-runs) | Container refusal built (slice 1): `refuseContainerPeer`; a running guest does not refuse a container launch, and the guest launch itself is slice 2 |
| <a id="UH-D6"></a>UH-D6 | **A peer that cannot be asked refuses the launch, naming the command that settles it.** A design decision, relaxed by [OQ-UH3](#OQ-UH3) for a runtime that cannot be asked when the keeper evidence says not running. | 2026-11-23 | [§7.2](#72-where-it-runs) | Built (slice 1): `decidePeers` |
| <a id="UH-D7"></a>UH-D7 | **A database is carved out until a measurement shows two kernels can share it.** A design decision: a SQLite store across a VM boundary has unmeasured lock behavior. | 2026-11-23 | [§6.3](#63-the-carve-out-key) | Built (slice 1): the `per_setup_state` pack key; `pi`, `copilot` and `omp` declare it |
| <a id="UH-D8"></a>UH-D8 | **Migration is composition applied to every home on disk:** a rename that refuses to replace, keep-both `.from-<setup>`, links moved as links, nothing deleted, silent through the plan and commit split, refused while any jail runs for other homes. A design decision under [OQ-SU5](named-setups.md#OQ-SU5). | 2026-11-23 | [§9](#9-migration-one-merge-for-every-home-on-disk) | Built (slice 1): `unifiedhome.Merge`, called by `unifyHome` |
| <a id="UH-D9"></a>UH-D9 | **No table of shipped directory names:** a source is read by the selected packs' declarations only. A design decision: it keeps `packload.Embedded*` out of name reservation and an unselected pack's directory untouched. | 2026-11-23 | [§9.1](#91-what-exists-and-what-the-merge-reads) | Built (slice 1) |
| <a id="UH-D10"></a>UH-D10 | **The guest's session names alias the container's `-workspace` entries by links in the shared store, opt-in per pack and gated on measurement.** A design decision: `/workspace` is the same on every machine and the real path is not. [OQ-UH4](#OQ-UH4) ruled the principle. | 2026-11-23 | [§8](#8-the-guests-path-to-the-workspace) | — |
| <a id="UH-D11"></a>UH-D11 | **Host and jail are separated by construction and a test:** one path function, link targets confined to the unified home, an import test on the host renderer. | 2026-11-23 | [§10](#10-host-and-jail-never-share) | Built (slice 1) |
| <a id="UH-D12"></a>UH-D12 | **Sets share sessions only,** through a per-pack `session_paths`, in a machine-tier store, with merge semantics. A design decision answering the owner's *"share the same sessions."* | 2026-11-23 | [§11](#11-workspace-sets-a-later-slice) | — |
| <a id="UH-D13"></a>UH-D13 | **`seatbelt-yolo-account` joins the unified home only after W-UH4 passes;** until then it keeps a private whole state directory and says so. A design decision: its files are another user's. | 2026-11-23 | [§7.4](#74-the-yolo-account) | — |
| <a id="UH-D14"></a>UH-D14 | **`DIR-BH1` is not in tension:** composition links selected packs only, and the unified home is never a home. | 2026-11-23 | [§6.4](#64-dir-bh1-no-tension) | Built (slice 1) |
| <a id="UH-D15"></a>UH-D15 | **`bash_history`, `.ssh`, core `.config`, `venv-shadows` and `writable-home` stay in the slice.** A design decision: single-file bind inodes, generated content, or unknown content. | 2026-11-23 | [§4](#4-what-goes-where) | Built (slice 1) |
| <a id="OQ-UH1"></a>[OQ-UH1](#OQ-UH1) | **A second guest is refused:** `seatbelt-self` and `seatbelt-yolo-account` never run at once in one workspace. The owner: *"A (Refuse the second)."* | 2026-11-24 | [§7.1](#71-the-rule) | — |
| <a id="OQ-UH2"></a>[OQ-UH2](#OQ-UH2) | **A launch is the only thing that merges old homes;** no `yolo home merge` verb, and `yolo check` shows what waits. The owner: *"A (Launch only)."* | 2026-11-24 | [§9.4](#94-stranded-users-and-what-they-see) | Built (slice 1): `yolo check` reports a waiting merge |
| <a id="OQ-UH3"></a>[OQ-UH3](#OQ-UH3) | **A peer whose runtime cannot be asked does not refuse when every keeper and session record says it is not running;** the launch proceeds with one line naming that setup. The owner: *"B (Proceed on keeper evidence)."* Amends [UH-D6](#UH-D6). | 2026-11-24 | [§7.2](#72-where-it-runs) | Built (slice 1): the `RuntimeOnly` note in `decidePeers` |
| <a id="OQ-UH4"></a>[OQ-UH4](#OQ-UH4) | **yolo may lay alias links in its own shared store** so a guest resumes a container's sessions, gated on W-UH2 and W-UH3. The owner: *"A (Lay the alias links)."* | 2026-11-24 | [§8](#8-the-guests-path-to-the-workspace) | — |
| <a id="OQ-UH5"></a>[OQ-UH5](#OQ-UH5) | **Edits to a generated settings file stay per setup;** edits through `yolo config` already reach every setup. The owner: *"A (No, they stay per setup)."* | 2026-11-24 | [§14](#14-slices) | Built (slice 1): no slice moves a generated file |

## Mac checks

The owner's Mac agent runs these on main after a slice lands; none holds anything off main and a failure is fixed
forward. Setup for all: a fresh workspace under `/Users/Shared/yolo/uh`, the installed `yolo` or a dev bundle from main,
`packs = ["claude"]`. Each reports its pasted output. Every commit is "main `<the slice's landing commit>` or later".

- **W-UH1 (slice 1), sessions follow a switch.** Settles [UH-D2](#decision-ledger) and [UH-D3](#decision-ledger).
  1. Lay a session file from Apple Container:

     ```text
     yolo --setup apple-container-self -- sh -c 'mkdir -p ~/.claude/projects/-workspace && echo ac > ~/.claude/projects/-workspace/w-uh1.jsonl'
     ```

  2. Read it from Podman:

     ```text
     yolo --setup podman-self -- cat /home/agent/.claude/projects/-workspace/w-uh1.jsonl
     ```

     Expected: `ac`.
  3. List its one real copy on the host:

     ```text
     ls -la .yolo/unified-home/.claude/projects/-workspace/
     ```

     Expected: `w-uh1.jsonl` once, and `projects` a link in `.yolo/setups/apple-container-self/home/.claude`. Report all
     three outputs and the Podman machine's architecture.
- **W-UH2 (before the alias slice), how each agent keys a session by directory.** Settles the [§8](#8-the-guests-path-to-the-workspace)
  table and which packs declare `cwd_keys`. In a seatbelt home where agents were used, list the names; no agent runs:

  ```text
  ls -a .yolo/setups/seatbelt-self/home/.claude/projects .yolo/setups/seatbelt-self/home/.pi/agent/sessions
  ```

  Expected: names derived from the real path. Report them with the output of `yolo --setup seatbelt-self -- pwd -P`, and
  one Copilot `workspace.yaml` and one agy `projects.json` if present.

  *Tried 2026-11-24 at `042a00ab2`* (lab report, retired): no `seatbelt-self` home on the Mac held agent sessions, so
  there was nothing to list. It waits until the owner has used an agent under `seatbelt-self`.
- **W-UH3 (before the alias slice), whether an agent follows an alias.** By hand, not an automated test: with an alias
  laid for the guest, start `claude` once in `seatbelt-self`, run `/resume`, and report whether a container's session
  shows; clear a project and report whether the link survived. Settles the gate of [OQ-UH4](#OQ-UH4) and the `plugins`
  row of [§4](#4-what-goes-where).
- **W-UH4 (slice 2), the yolo account and the unified home.** Settles [UH-D13](#decision-ledger). The yolo account's
  launch is not joined until this passes, so it cannot be asked to write through `~/.claude/projects`; the check
  writes the unified home by its absolute path instead, from each user in turn (the pair is refused together,
  [OQ-UH1](#OQ-UH1), so run them in sequence). In `/Users/Shared/yolo/uh`, after one `seatbelt-self` launch has made
  `.yolo/unified-home/.claude/projects`:

  ```text
  yolo --setup seatbelt-yolo-account -- sh -c 'echo a > "$YOLO_HOST_DIR/.yolo/unified-home/.claude/projects/w-uh4-a"'
  ```

  ```text
  cat .yolo/unified-home/.claude/projects/w-uh4-a && echo u >> .yolo/unified-home/.claude/projects/w-uh4-a
  ```

  ```text
  yolo --setup seatbelt-yolo-account -- sh -c 'cat "$YOLO_HOST_DIR/.yolo/unified-home/.claude/projects/w-uh4-a" && echo a2 >> "$YOLO_HOST_DIR/.yolo/unified-home/.claude/projects/w-uh4-a"'
  ```

  Then the move composition makes, by hand, on a directory the account made:

  ```text
  yolo --setup seatbelt-yolo-account -- sh -c 'mkdir -p ~/.claude/probe && echo f > ~/.claude/probe/f'
  ```

  and, as yourself:

  ```text
  mv -n .yolo/setups/seatbelt-yolo-account/home/.claude/probe .yolo/unified-home/.claude/probe && ln -s ../../../../unified-home/.claude/probe .yolo/setups/seatbelt-yolo-account/home/.claude/probe
  ```

  Expected: every command succeeds. Report each output, `ls -le .yolo/unified-home/.claude/projects`, and
  `ls -lde .yolo/setups/seatbelt-yolo-account/home/.claude`. A pass opens the gate with a one-line commit
  ([UHG-D8](unified-workspace-home-guest-plan.md#UHG-D8)).
- **W-UH5 (slices 1 and 2), the pairing rule.** Settles [§7.1](#71-the-rule) and [OQ-UH3](#OQ-UH3). Terminal 1:

  ```text
  yolo --setup apple-container-self -- sleep 600
  ```

  Terminal 2, expected refused with a message naming `yolo stop --setup apple-container-self`:

  ```text
  yolo --setup podman-self -- true
  ```

  Terminal 3, expected to run:

  ```text
  yolo --setup seatbelt-self -- true
  ```

  Then stop the Podman machine, stop terminal 1's jail, and launch `apple-container-self` again; report whether it is
  refused and with which line.
- **W-UH6 (slice 1), the stranded home.** Settles [§9.4](#94-stranded-users-and-what-they-see). In a workspace holding a
  Podman session and an Apple Container session, run `yolo stop`, then one launch. Expected: one `Merged …` line, every
  session file under `unified-home`, none left in the old homes, no `.from-` name unless two files differed. Report
  the line and `find .yolo -name '*.jsonl'` before and after.

## Appendix A. Evidence

Read at `09ba35cd9` (2026-11-23). **MEASURED** means bytes looked at in this jail's own Podman home; **UNMEASURED** and
**UNVERIFIED** mean nothing in the tree or the home shows it.

**Checked in code, by name.**

- `selectHome`, `commitHome`, `currentHome`, `projectJails`, `projectJailNames`, `jailRuns`, `holdHomeLock`,
  `adoptLegacyHome`, `legacyHomeIsLaidFor`, `podmanLaidHome`, `appleContainerLaidHome` in `internal/cli/run`
  (`legacyhome.go`, `flock.go`), and the three pin tests named in `legacyhome.go`'s header.
- `paths.SetupHomeState`, `LegacyHomeState`, `WorkspaceHomeStates`, `SetupStateNames`, `OpenWorkspaceHomeRoot`,
  `HomeSurfaces`, `InstalledProgramSurfaces`.
- Layout: `wsStateHomeRel` and `prepareWsState` (`internal/cli/run/prepare.go`); `DeriveDarwinWorkspaceHome`
  (`internal/entrypoint/darwinhomelayout.go`; its comment says undotted sidecar directories are "left in place and
  unused"); `macosuser.LaunchHomes`.
- Hooks `shared_credentials` and `per_jail_history` (`packs/claude/pack.toml`, `internal/entrypoint/packhooks.go`);
  `session_homes` as the top-level-key precedent (`internal/packdecl/sessionhome.go`).
- State declarations: `rg -n '^\[\[state\]\]' -A3 packs/*/pack.toml` gives claude, codex, copilot, pi, omp and agy
  workspace-scope directories and four machine-scope credential directories; opencode, kilo and muse-planner declare none.

**Measured in this jail's home** (`.yolo/setups/podman-self/home`, a self-hosting workspace, so unusually large):
`local` 18 GB, `claude` 7.2 GB (`projects` 7.1 GB), `pi` 4.7 GB (`agent/sessions` 4.1 GB), `go` 4.4 GB, `npm-global`
1.2 GB, `codex` 948 MB (all `packages/`), `copilot` 22 MB; `claude/projects/-workspace` is the container slug;
`copilot/session-state/<uuid>/workspace.yaml` carries `cwd: /workspace`; `gemini/projects.json` is
`{"projects": {"/workspace": "workspace"}}`; no legacy `.yolo/home`, one setup directory.

**UNVERIFIED, and what settles each.**

| Claim | Settled by |
| :--- | :--- |
| The guest's Claude slug is the mangled real path | [W-UH2](#mac-checks) |
| An agent follows or preserves a symlinked project directory | [W-UH3](#mac-checks) |
| Claude writes one file per session, so concurrent writers do not clash | A listing of `projects/` and one concurrent-writer run |
| `plugins/` holds no native code | [W-UH3](#mac-checks) listing |
| The yolo account and the user read and write each other's files in the unified home | [W-UH4](#mac-checks) |
| The keeper is registered before the project lock is released | The builder reads `run.go` and pins it with a test |
| Podman Machine and Apple Container share an architecture on every Mac | W-UH1's report |
| Codex, omp, opencode, kilo and muse key sessions by directory | [W-UH2](#mac-checks) |

**The bug this builds on.** Before `09ba35cd9` the legacy home moved before a launch could still fail, and an Apple
Container setup took a home Podman had laid; the fix made the move late and attribution layout-aware. This design
replaces the per-setup rule that made both possible.

## Appendix B. Alternatives considered

**How the shared state is reached.**

| Alternative | Verdict |
| :--- | :--- |
| Share each state directory whole, let generated files fight | Rejected: a container and a guest rewrite `settings.json` against each other, and it holds `{yolo.setup}` |
| Share an allow-list of transcript paths | Rejected: each new agent directory strands data until a pack is edited; fetched packs never opt in |
| Bind the unified home whole as the home | Rejected: reopens the layout split and puts two operating systems' installs in one tree |
| Bind each shared child on Apple Container | Rejected: one mount per child against the device limit |
| A `yolo home adopt` verb only | Kept as the recovery path ([§9](#9-migration-one-merge-for-every-home-on-disk)), not the design: two copies diverge after every switch |

**How the guest gets a stable path.**

| Other mechanism | Verdict |
| :--- | :--- |
| A per-user stable symlink the guest `cd`s through | Rejected: `getcwd` returns the resolved path on macOS |
| An environment variable naming the project key | None found; `CLAUDE_CONFIG_DIR`, `CODEX_HOME` and `PI_CODING_AGENT_DIR` move the whole config, not the key |
| Rename the other side's slug at boot | Rejected as steady state: the live peer recreates the old name; kept only inside the merge |
