---
title: "Tool skills — the skills a mise tool declares, delivered as one more skills layer"
status: accepted
stage: CURRENT
verified: 2026-11-27
verified_commit: 33b200771
covers:
  - internal/toolskills/
  - internal/provision/toolskills.go
  - internal/config/toolskills.go
  - internal/jailcontent/toolskillslayer.go
  - internal/cli/run/sessioninputs.go
  - internal/cli/run/toolskillsnotes.go
  - integration/toolskills_test.go
tags: [skills, mise, packs, delivery, macos-user]
summary: "How the jail asks mise which skills the user's own tools declare, snapshots them into the workspace's yolo state directory, and how the host composes that snapshot as the lowest-priority skills layer, one session later."
---

# Tool skills — the skills a mise tool declares, delivered as one more skills layer

**Status:** Verified 2026-11-27 against `33b200771`. The macOS and Apple Container behavior is
**unverified**: see [Unverified on macOS](#unverified-on-macos).

A mise tool the user named in their own `install.mise` may ship agent skills. mise already records
which skills the active tools declare; yolo reads that list inside the jail after `mise install`,
keeps a confined copy in the workspace's state directory, and lets the host's existing skills
composition pick the copy up as its last layer. A tool's skills therefore reach the agent **one
session after** the tool or its version changes, never in the session that installed it.

**Skill** — a directory holding `SKILL.md` and the files it references (the Agent Skills format).
**packslip** — mise's name for the backend (`packslip:`) and the release manifest through which a
tool declares skills; mise also reads a declaration a custom backend writes into its install
directory. Neither is yolo's. **Layer** — one source of skills the host composes into a
destination, defined in [`agent-briefings.md`](agent-briefings.md); not a pack (a pack declares a
destination, a layer fills names).

**Reads with:** [`agent-briefings.md`](agent-briefings.md) (the skills layers this adds one to),
[`happy-path-principle.md`](happy-path-principle.md) (every line below names a next step),
[`pack-system.md`](pack-system.md) (packs own every per-agent fact).

| Part | Lives in |
| :--- | :--- |
| Discovery (`mise skills ls --json`), selection, the snapshot writer, the provisioning step | `internal/toolskills` |
| The shell step spliced into every provisioning stage | `internal/provision` (`StepToolSkills`) |
| The allowlist and the snapshot token | `internal/config` (`MiseSkillTools`, `ToolSkillsToken`) |
| The session inputs and the host's request for the layer | `internal/cli/run` (`sessioninputs.go`) |
| The host layer (reads the snapshot, delivers it) | `internal/jailcontent` (`toolskillslayer.go`) |
| The host's disclosure lines | `internal/cli/run` (`toolskillsnotes.go`) |
| The in-jail command | `yolo internal tool-skills <workspace> [lock-file]` |

---

## Non-goals

- No Claude plugin folder or `CLAUDE_CODE_PLUGIN_DIRS`, no writable skills mount, no watcher, no
  mid-session refresh.
- yolo installs and manages no mise backend plugin: any `install.mise` key is accepted and mise does
  the rest. The backend plugin must already be present in mise.
- No host-notch (`yolo host`) delivery. The jail step runs only where a provisioning stage runs.
- No change to the synchronous `mise install` before the first command.

## Principles

**One writer per piece of state.** The jail writes the snapshot; the host writes every skills tree.
A skills destination is a `:ro` bind of a host directory, so nothing in the jail could write one
anyway, and no pack declaration, destination or mount changes.

**A repository may change a tool's version but never add a tool's skills.** The allowlist comes from
the user scope alone, and the snapshot is trusted only when it carries a token the repository cannot
know ([Snapshot token](#the-snapshot-token)).

**Never fail, never wait.** Every failure keeps the previous snapshot and starts the session. The
only waits are the bounded listing run and the lock that serializes concurrent refreshes.

**No agent knowledge.** The layer reaches every selected pack's skills destination through the same
loop that fans out every skills layer, with no switch on an agent or tool name.

## How it works

1. **Host, at every launch and every attach.** Compute the allowlist from the user scope, put it on
   the per-entry session channel as `YOLO_MISE_SKILL_TOOLS`, and compose the skills trees. The
   composition gains one layer: the snapshot, when one exists and carries the current token. The
   digest of what it delivered rides the same channel as `YOLO_TOOL_SKILLS_DELIVERED`. The layer is
   read fresh on every composition, never from an attach's frozen state, because the jail writes
   the snapshot after the launch's composition ran.
2. **Jail, after `mise install`.** The provisioning stage runs `yolo internal tool-skills` after
   `mise install` and before the command starts. With an empty allowlist it starts no process at
   all. Otherwise it runs `mise -C <workspace> skills ls --json`, keeps the entries whose `tool`
   equals an allowlist entry, orders them by allowlist position then name, copies each skill
   directory through a confined reader, and publishes the result as the snapshot.
3. **Jail, same step.** When the new snapshot's content digest differs from
   `YOLO_TOOL_SKILLS_DELIVERED`, one line says the skills arrive with the next session.
4. **Host, on the next launch or attach.** Step 1 composes the new snapshot into each destination.
   A running jail sees it live, because the destination is a bind of the host directory the
   existing skills sync rewrites in place; a new agent session then loads it.

The stage that runs the step is the first session's provisioning stage on the container backends
and macos-user. A later session runs it as part of the mise reinstall that session performs **only
when its mise inputs (the tool table and the disabled list) differ from those last installed**; a
later session with unchanged inputs does not refresh, so a tool whose version moved without
changing the table (a branch-named version, an upgrade mise did on its own) is noticed at the next
reinstall, not at every entry.

### What mise provides

| Fact | Value |
| :--- | :--- |
| Command | `mise skills ls --json`, `-C <dir>` runs it as if from `<dir>`; mise 2026.9.2 or later |
| JSON | an array of objects with `name`, `tool`, `version`, `path`, all strings |
| `tool` | the backend identity as mise spells it, for example `packslip:github.com/jdx/hk` |
| `path` | the directory holding `SKILL.md`, absolute |
| Order | not specified: it is the toolset's iteration order, so yolo never relies on it |
| Missing skills | absent from the JSON; mise prints one `WARN` line per missing skill on stderr, before the JSON |
| Empty | `[]` |

The parser accepts only that shape, ignores unknown fields, and refuses an entry with no name, no
tool or a non-absolute `path`. A reworded missing-skill line costs only its disclosure: the stderr
parser matches one measured shape (the sample is `internal/toolskills/testdata/ls-missing.stderr`)
and ignores any other text.

### The allowlist

**Allowlist** *(coined here)* — the ordered list of tool names the user's own configuration puts
under `install.mise`, taken from the user scope before any workspace layer is merged over it. Not
the merged table `YOLO_MISE_TOOLS` carries, which also holds workspace additions.

The user graph (user file, includes, `config.d`, `--user-layer`) is the user's; the workspace project
file and workspace-local file are not. A value of `off` is not an entry; the legacy `mise_tools`
spelling counts. The allowlist is empty for a sealed build, a capture jail, and a launch run from
inside a jail (whose "user scope" is the outer jail's effective config and so is not the user's).
The host applies the same allowlist again when it reads the snapshot, so a snapshot cannot widen it.
An empty allowlist makes the whole feature inert: no process, no file, no line.

### The snapshot

**Tool-skills snapshot** *(coined here)* — a confined, content-addressed copy of the skills of
allowlisted tools, written in the jail and read by the host. Not a view of the mise store: no link
points into `/mise`.

It lives in `<workspace>/.yolo/tool-skills/`, yolo's state directory beside `.yolo/logs/`, never a
tracked path:

```text
.yolo/tool-skills/
  current                    one line: the digest of the snapshot to read
  <digest>/index.json        {version, token, digest, skills: [{tool, version, name}]}
  <digest>/skills/<name>/…   the copied bytes: regular files and directories, never a link
```

A snapshot is a directory named by its digest, made complete first; `current` is replaced by rename.
The host reads `current`, then that directory, so it can never see a half-written snapshot. Stale
digest directories are collected after a publish. Publishing is serialized by one lock (in the
jail's main state directory, as `mise-install.lock` serializes installs; macos-user, which has no
such directory, uses a lock file inside the snapshot directory), so two sessions entering at once
publish once and the second prints nothing.

**The jail's copy is confined**, because everything mise says is untrusted: a tool author chooses a
skill's path and the bytes under it.

- A skill's `path` must lie under mise's installs directory (`$MISE_DATA_DIR/installs`, `/mise` by
  default); otherwise the skill is skipped.
- Each skill is copied by a chain of `O_NOFOLLOW` opens relative to a directory descriptor. Files
  open non-blocking and are checked regular after the open, so a FIFO or device swapped in cannot
  block or be read. The first two components under the installs directory (tool and version) may be
  links, because mise itself links `installs/<tool>/<alias>` into the store; nothing below them is
  followed.
- A link inside a skill that resolves inside that skill is copied as the file or directory it names;
  one that leaves it, is absolute, or loops makes that skill skipped.
- Every write is relative to a descriptor for `.yolo/tool-skills`, opened without following a link,
  so a repository that plants a link at `.yolo` or at `.yolo/tool-skills` is refused and nothing
  lands outside.
- The cap is the workspace skills layer's size and entry cap applied to the whole snapshot (see
  [Current values](#current-values)).

A listing that succeeds and returns no allowlisted skill publishes an **empty** snapshot, so a
removed tool's skills drop one session later. A listing or write that fails keeps the previous
snapshot.

### The snapshot token

**Snapshot token** *(coined here)* — 32 random bytes, hex, minted once per workspace on the host the
first time it is needed and read back unchanged on every later launch. It lives in yolo's own host
storage (`tool-skills-tokens`, named by the resolved workspace, mode 0600 in a 0700 folder), never
under the workspace and never under `cache/`, which every jail mounts read-write. It rides the
per-entry channel as `YOLO_TOOL_SKILLS_TOKEN`; the jail writes it into the snapshot index, and the
host composes a snapshot only when the index carries the current token (compared in constant time).
A snapshot a repository commits cannot carry it, so "a repository never adds a tool's skills"
holds while the snapshot stays in `.yolo/tool-skills/` on every backend and no mount is added. A
process inside the jail can read the token, but such a process can already write the workspace's own
skills layer, so it gains nothing. A token file that is not a regular file, or whose content is not
a token, is replaced, never followed. The token is minted only for a non-empty allowlist; if it
cannot be stored, tool skills are off for that launch and a line says so.

### The host layer

The tool-skills layer is the **lowest** of every destination's layers: below yolo's LSP plugin, the
packs, yolo's built-in skills, a pack's reserved children, and the repository's own mirrored skills.
It fills only names still free after all of them.

The host reads the snapshot as untrusted, as it reads the workspace layer: one descriptor walk with
every component opened `O_NOFOLLOW`, nothing resolved through a link, every size and count bounded,
and a skill containing a link, device or FIFO skipped whole (the writer never produces one). It
re-applies the allowlist and the name rule, and orders by allowlist position then name; it reads the
index for what it names, never for the order the index gives. It copies the layer once per
composition into a private scratch tree and hands it to each destination from there.

Any failure of the read is **no layer**, never a refused launch.

### Names and conflicts

- Between two tools that declare one name, the tool earlier in the allowlist wins; mise's own order
  is never used.
- A skill name is one path component, not starting with `.`, with no control characters, valid
  UTF-8, and at most 128 bytes. Otherwise the skill is skipped.
- A name a higher layer already holds stays with that layer.

### What the user reads

Every line names the next step. Tool names print as the user spelled them, control characters are
stripped from everything interpolated, and the host's lines escape markup (a tool and its skills are
named by a third party). The host's lines go to stderr, from the launch and from every attach. A
delivered skill has no line: the skills are simply there.

| Situation | Said by | Line (shape) |
| :--- | :--- | :--- |
| Name held by a higher layer | host | `yolo: mise skill "NAME" (TOOL) not delivered: PROVIDER already provides a skill by that name and wins. Rename that skill to use the tool's.` |
| Name held by an earlier tool | host | `…TOOL1 comes first in your install.mise and wins. Reorder install.mise (…) to change that.` |
| Skill skipped by the host's read | host | `…not delivered: REASON. Fix that skill in the tool, or remove the tool from your install.mise, then relaunch yolo.` |
| Token could not be stored | host | `yolo: mise skills: tool skills are off for this launch…` naming `yolo check` |
| Snapshot changed this boot | jail | `yolo: mise skills changed (TOOL 1.2 -> 1.3); they reach the agent with the next session: relaunch or attach.` |
| First snapshot | jail | `yolo: mise skills: N skills from TOOL are ready; they reach the agent with the next session: relaunch or attach.` |
| Declared but not installed | jail | `yolo: mise skills: TOOL declares skills that are not installed (MISE'S LINE). Run `mise install` in the workspace, then start a new session.` |
| `mise skills` absent | jail | `yolo: mise skills: this mise has no `skills` command. Update mise to 2026.9.2 or later.` |
| Listing failed or timed out | jail | `yolo: mise skills: could not refresh (REASON); the last snapshot stays. Run `mise skills ls` in the workspace to see why.` |

A change is announced once: the jail says "arrives with the next session" only when the new digest
is neither the one the host delivered nor the one a concurrent session already published. An
unreadable allowlist or a missing token or workspace argument also prints one line and keeps the
last snapshot.

> [!WARNING]
> **Skills the jail's step skips are not named.** The jail's copier skips a skill whose path is
> outside the installs directory, whose copy hits the size cap, or whose links escape it, and the
> selection drops invalid names and the loser of a same-name conflict between two tools. None of
> those lands in a printed line: the skill is simply absent from the snapshot. Only the host's read
> names what it skips. Naming the jail's skips would need the step to print what `Publish` and
> `Select` already return.

### Degenerate inputs

| Input or failure | Behavior |
| :--- | :--- |
| Allowlist empty (the default) | Inert: no process, no file, no line. |
| `mise` absent, failing, or printing non-JSON | One line; the previous snapshot stays; the session starts. |
| Listing hangs | The whole process group is killed at the deadline; one line. |
| Output past the cap | A failure, not a truncation. |
| `mise` too old to have `skills` | Recognized from stderr by a heuristic; a miss is reported as a plain failure, which also keeps the previous snapshot. |
| A `path` outside the installs directory, an escaping link, a FIFO or device, a vanished directory | That skill is skipped; the others continue. |
| Two sessions enter at once | Serialized by the lock; the second finds an unchanged digest and prints nothing. |

### Backends, nested and sealed jails

- **Container backends (podman, Apple Container).** The step is part of the shared provisioning
  stage, so it runs wherever `mise install` does. The snapshot sits in the workspace, which both
  runtimes bind.
- **macos-user (both modes).** The same step, in the confined provisioning stage, never in the
  privileged bootstrap: mise reads workspace configuration that can run code. The allowlist and the
  token are set by the launch on the guest's environment and any composed value of either name
  (an `env_sources` entry a workspace can write) is removed first. The host's overlay copies the
  composed skills tree as it copies every skill.
- **Sealed and capture jails, and launches inside a jail.** Empty allowlist, so nothing is
  discovered or delivered.

## Unverified on macOS

The Linux podman path is covered by the unit tests and by `integration/toolskills_test.go`, which
drives synthetic tool skills through the second session with no agent. These macOS claims have not
been observed on a Mac and wait on the owner's Mac:

- **macos-user, both modes:** the confined provisioning stage can write
  `<workspace>/.yolo/tool-skills` and the launcher reads it.
- **Apple Container:** the same snapshot reaches the copied skills tree on the next launch. Its tool
  disk is per workspace rather than a shared `/mise`; the snapshot lives in the workspace and is
  expected to be unaffected.

The Mac-waiting group of [the roadmap](../plans/roadmap.md#waiting-on-a-mac-ci-a-rootless-host-or-an-outside-account)
lists them.

## Current values

Verified at `33b200771`. The prose above says what each is for; this table is the only place the
numbers and names are stated.

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| Listing deadline | 15 s, process group killed | `toolskills.DefaultDeadline` |
| Listing stdout cap | 1 MiB | `toolskills.DefaultMaxStdout` |
| Missing-skill line cut | 200 bytes | `toolskills.ParseWarnings` |
| Snapshot size and entry caps | 32 MiB, 4096 entries (the workspace layer's) | `toolskills.DefaultMaxBytes`, `DefaultMaxEntries` |
| Index size and skill-count read caps (host) | 1 MiB, 4096 skills | `jailcontent` (`toolskillslayer.go`) |
| Skill name cap | 128 bytes | `toolskills.MaxNameBytes` |
| Link hops, walk depth (writer) | 40, 64 | `toolskills` (`snapshot.go`) |
| Allowlist channel input | `YOLO_MISE_SKILL_TOOLS` (JSON array) | `internal/cli/run/sessioninputs.go` |
| Token channel input | `YOLO_TOOL_SKILLS_TOKEN` | same |
| Delivered-digest channel input | `YOLO_TOOL_SKILLS_DELIVERED` | same |
| Snapshot directory | `<workspace>/.yolo/tool-skills/` | `toolskills.SnapshotDirName` |
| Token store | `tool-skills-tokens` under yolo's global storage | `config.ToolSkillsToken` |
| Lock file (container) | `/run/yolo/main/tool-skills.lock` | `provision.ToolSkillsLockName` |
| Minimum mise | 2026.9.2 | `toolskills.Runner` |

## Why it's this way

Rulings a maintainer reading only the text above might undo. The ids are cited from code comments.

| ID | Ruling |
| :--- | :--- |
| MS-D1 | The allowlist is the user scope's `install.mise` in declaration order, `off` excluded, empty when sealed, captured or nested. A workspace may override a version, never add a tool. |
| MS-D2 | Discovery runs in the jail after install, never on the host: the host has no mise call, a different platform's store on macOS, and no view of Apple Container's tool disk. It is bounded by a deadline, an output cap and its own process group. |
| MS-D3 | A skill matches by exact equality of its `tool` with the allowlist key; order is allowlist position, then name. |
| MS-D4 | The snapshot is a digest-named directory plus a `current` file replaced by rename, copied (never linked) through an `O_NOFOLLOW` walk. |
| MS-D5 | The host layer has the lowest priority and re-applies the allowlist, names and caps; it never trusts the index's order. |
| MS-D6 | A failed refresh keeps the previous snapshot; a successful empty one clears it. |
| MS-D7 | Staleness is a content digest compared with `YOLO_TOOL_SKILLS_DELIVERED`, so a branch-named version that changes bytes is noticed. |
| MS-D8 | Skills arrive as bare names in every selected destination: no plugin folder, no settings key. Claude has no home skills path to use, and a per-agent spelling would be new agent knowledge that bypasses layer priority. |
| MS-D9 | The snapshot stays in `.yolo/tool-skills/`; the host composes it only when its index carries the per-workspace token, minted and stored outside the workspace. |

> [!WARNING]
> **Do not return the discovery to the host to remove the one-session lag.** It needs a
> jail-to-host transport and a bounded host wait on every launch, to gain one session of freshness.
> **Do not run `mise skills sync` into the destinations or `mise skills ls` on the host**: the
> destinations are read-only mounts and the host has no mise.
