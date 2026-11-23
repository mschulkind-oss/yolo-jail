---
title: "Tool skills: a mise tool's skills as one more skills layer, delivered a launch later"
date: 2026-11-22
status: in-review
tags: [skills, mise, packs, delivery, macos-user]
summary: "The jail asks mise which skills the user's own tools declare, snapshots them into yolo's state directory, and the host composes that snapshot as the lowest-priority layer of the skills delivery that already exists. No new transport, view or agent knowledge."
stage: DESIGN
next: "Build slice 1 (allowlist) and slice 2 (discovery) in parallel"
---

# Tool skills: a mise tool's skills as one more skills layer, delivered a launch later

**Status:** 2026-11-22. Nothing built. Repository evidence read at `fe37424b3`; upstream mise contract read from
source on the same date ([Appendix A](#appendix-a-evidence)). The macOS rows are UNMEASURED and wait on the owner's Mac.

> **In short.** mise already records which skills the active tools declare; the only work is to read that list
> inside the jail after `mise install`, keep a confined copy, and let the host's existing skills composition
> pick the copy up as its last layer. The cost is one launch of lag, which every forced constraint
> ([§5](#5-the-three-forced-points)) turns out to share.

**Why it matters.** `mise skills sync` cannot work in a jail: every skills destination is a read-only mount the host builds.

**Cost.** A tool's skills reach the agent one session after the tool or its version changes, not in the session that installed it.

**Start at [§4](#4-the-design).**

**Needs your ruling:** nothing; every mechanism is in the [ledger](#11-decision-ledger).

**Reads with:** [`agent-briefings.md`](../reference/agent-briefings.md) (the skills layers this adds one to),
[`happy-path-principle.md`](../reference/happy-path-principle.md) (every stop names the next step),
[`pack-system.md`](../reference/pack-system.md) (packs own every per-agent fact).

---

## 1. Goal and non-goals

**Goal.** A mise tool the user named in their own `install.mise` may ship agent skills. Those skills appear
in every selected agent's skills directory, at the versions mise resolves for the workspace, and a repository can
change a version but never add a tool's skills.

**Non-goals.**

- No Claude plugin folder or `CLAUDE_CODE_PLUGIN_DIRS`, no writable skills mount, no watcher, no mid-session refresh.
- No installing or managing mise backend plugins: yolo accepts any `install.mise` key and mise does the rest.
- No change to the synchronous `mise install` before the first command, and no host-notch (`yolo host`) delivery.

## 2. What mise gives us

**Skill** — a directory holding `SKILL.md` and files it references (the Agent Skills format). **packslip** — mise's
name for the backend (`packslip:`) and release manifest through which a tool declares skills; mise also reads a
declaration a custom backend writes into its install directory. Neither is ours.

| Fact | Value | Source |
| :--- | :--- | :--- |
| Command | `mise skills ls --json` (`-C <dir>` runs it as if from `<dir>`); mise 2026.9.2 or later | `mise skills ls --help`, probed in this jail on 2026.10.17 |
| JSON | an array of objects with exactly `name`, `tool`, `version`, `path` (all strings) | `Skill` struct, [Appendix A](#appendix-a-evidence) |
| `tool` | the backend identity as mise spells it, for example `packslip:github.com/jdx/hk` | the table `mise skills ls` prints in this jail |
| `path` | the directory holding `SKILL.md`; absolute (the table form uses `~`, the JSON is the raw path) | `ls` source; UNMEASURED for a non-empty list |
| Order | **not specified**; it is the toolset's iteration order | `ls` sorts nothing |
| Missing skills | **absent from the JSON**; mise prints one `WARN` line per missing skill on stderr, before the JSON | `ls` source |
| Empty | `[]` (measured here) | probe |

The parser accepts only that shape, rejects an entry whose `path` is not absolute, and never relies on order.
The text of a missing-skill line is the one unknown: witness [W1](#appendix-a-evidence).

## 3. Vocabulary

- **Allowlist** *(coined here)* — the ordered list of tool names the user's own configuration puts under
  `install.mise`. Not the merged table `YOLO_MISE_TOOLS` carries, which also holds workspace additions.
- **Tool-skills snapshot** *(coined here)* — a confined, content-addressed copy of the skills of allowlisted tools,
  written in the jail and read by the host. Not a view of the mise store: no link points into `/mise`.
- **Layer** — one source of skills the host composes into a destination ([`agent-briefings.md`](../reference/agent-briefings.md)). Not a pack: a pack declares a destination, a layer fills names.

## 4. The design

One writer per piece of state. The jail writes the snapshot; the host writes every skills tree.

1. **Host, at launch and at every attach.** Compute the allowlist from the user scope ([MS-D1](#11-decision-ledger)),
   put it on the per-entry session channel as `YOLO_MISE_SKILL_TOOLS`, and compose the existing skills trees.
   The composition gains one layer: the snapshot, if one exists and was written for an allowlisted tool.
   The digest of what it delivered rides the same channel as `YOLO_TOOL_SKILLS_DELIVERED`.
2. **Jail, once per session entry, after `mise install`** (the first session's provisioning stage, a later session's
   reinstall) and before the command starts. If the allowlist is empty, do nothing. Otherwise run
   `mise -C /workspace skills ls --json` ([MS-D2](#11-decision-ledger)), keep entries whose `tool` equals an allowlist
   entry, order them by allowlist position then name, copy each skill directory through a confined reader, and publish
   the result as the snapshot ([MS-D4](#11-decision-ledger)).
3. **Jail, same step.** If the new snapshot's content digest differs from `YOLO_TOOL_SKILLS_DELIVERED`, print one
   line saying the skills arrive with the next session ([§6.2](#62-what-the-user-reads)). Never fail, never wait for the host.
4. **Host, on the next launch or attach.** Step 1 composes the new snapshot into each destination's tree. A running
   jail sees it live, because the destination is a bind of the host directory the existing sync already rewrites
   in place; a new agent session then loads it.

The snapshot lives in `<workspace>/.yolo/tool-skills/`, guarded by the snapshot token ([MS-D9](#11-decision-ledger)). It is yolo's state
directory, beside `.yolo/logs/`, never a tracked path: nothing in the repository's own files changes.

## 5. The three forced points

Each of the three constraints the owner named is real, and each is met by the cheapest move that fits.

**5.1 Timing: host composition runs before the in-jail install.** Discovery needs the installed bytes, so it must run
in the jail after install; the host cannot run mise (it has no mise call anywhere, and Apple Container's tool disk is
not host-visible). The cheapest fit is to let the snapshot lag one session: the first launch after a change delivers
the previous snapshot and says what is coming ([§6.2](#62-what-the-user-reads)). A host-to-jail rendezvous would be the
alternative and buys only freshness ([§9](#9-alternatives-rejected)).

**5.2 Allowlist: `YOLO_MISE_TOOLS` is merged.** The user-only list has to be taken from the user scope before the
workspace is merged over it. `config.UserScopeConfig` is already read host-side during launch
(`internal/cli/run/launchinputmcp.go:63`) and `config.OwnMiseTools` reads one scope's table in declaration order
(`internal/config/readableconsumers.go:47`). The user graph (user file, includes, `config.d`, `--user-layer`) is the
user's; workspace project and workspace-local files are not. A value of `off` is not an entry. The list is empty for a
sealed or capture jail (they withhold the user's tools, `internal/config/derived.go:134`) and for a launch run from
inside a jail, whose "user scope" is the outer jail's effective config and so cannot be trusted as the user's.
The host applies the same list again when it reads the snapshot, so a snapshot cannot widen it.

**5.3 Read-only mounts: skills destinations are `:ro` binds of host trees.** The jail never writes one. Delivery is the
host composing a tree it already owns, so the mount stays read-only and no pack declaration, destination or mount
changes. This is also why no agent-specific route exists: every selected pack's destination receives the layer through
the loop that fans out today (`packSkillTargets`, `internal/cli/run/prepare.go:920`), with no switch on an agent or tool name.

## 6. Rules

### 6.1 Names, conflicts, order

- **Lowest priority.** The layer claims only names still free after every other layer, including a pack's reserved
  children (`reserved`) and the repository's own mirrored skills. A name an earlier layer holds stays with it.
- **Between tools, the allowlist order wins**; mise's own order is never used. The loser is named.
- **A skill name** is one path component, not starting with `.`, no control characters, at most 128 bytes. Otherwise the
  skill is skipped and named, with the name sanitized.

### 6.2 What the user reads

Every line names the next step ([`happy-path-principle.md`](../reference/happy-path-principle.md)); the first two come
from the host at launch and attach, the rest from the jail step. Tool names print as the user spelled them. Control
characters are stripped from everything interpolated.

| Situation | Line |
| :--- | :--- |
| Delivered, nothing skipped | none: the skills are simply there |
| Name already provided | `yolo: mise skill "NAME" (TOOL) not delivered: PROVIDER already provides a skill by that name and wins. Rename that skill to use the tool's.` |
| Two tools, one name | `yolo: mise skill "NAME" from TOOL2 not delivered: TOOL1 comes first in your install.mise and wins. Reorder install.mise (<config verb>) to change that.` |
| Snapshot changed this boot | `yolo: mise skills changed (TOOL 1.2 -> 1.3); they reach the agent with the next session: relaunch or attach.` |
| First snapshot | `yolo: mise skills: N skills from TOOL are ready; they reach the agent with the next session: relaunch or attach.` |
| Declared but not installed | `yolo: mise skills: TOOL declares skills that are not installed (MISE'S LINE). Run `mise install` in the workspace, then start a new session.` |
| `mise skills` absent | `yolo: mise skills: this mise has no `skills` command. Update mise to 2026.9.2 or later.` |
| Listing failed or timed out | `yolo: mise skills: could not list (REASON); the last snapshot stays. Run `mise skills ls` in the workspace to see why.` |

The config verb in the second line is the implementer's to spell (`TestConfigRefTOMLSectionNamesTheVerbsTheCLIHas` pins it); other wording is theirs provided each line keeps its next step.

### 6.3 Failure and degenerate inputs

| Input or failure | Behavior |
| :--- | :--- |
| Allowlist empty (the default) | The whole feature is inert: no process, no file, no line. |
| `mise` absent, failing, or non-JSON | One line; the previous snapshot stays; the session starts. |
| Listing hangs | Killed with its process group after 15 s; one line. Output past 1 MiB is a failure. |
| Zero allowlisted entries returned | The snapshot is replaced by an empty one, so a removed tool's skills drop one session later. |
| A `path` outside the mise installs directory, a link escaping the skill directory, a FIFO or device, a vanished directory | That skill is skipped and named; others continue. |
| A skill past the size cap | Skipped and named. The cap is the workspace layer's (32 MiB, 4096 entries) applied to the whole snapshot. |
| Two sessions enter at once | Serialized by one lock in the jail's main state, as `mise-install.lock` does; the second finds an unchanged digest and prints nothing. |
| Snapshot half-written when the host reads | Cannot occur: a snapshot is a directory named by its digest, made complete first, and `current` (a file naming it) is replaced by rename. The host reads `current`, then that directory. |

## 7. Backends, nested and sealed jails

- **Container backends (podman, Apple Container).** The jail step is part of the shared provisioning steps, so it runs
  wherever `mise install` does. The snapshot sits in the workspace, which both runtimes already bind. Apple Container's
  tool disk is per workspace rather than a shared `/mise`; the snapshot is unaffected.
- **macos-user (both modes).** Same step, same place, in the confined provisioning stage, never in the privileged
  bootstrap: mise reads workspace configuration that can run code, and `InstallHomeOverlay` must not. The host's overlay
  copies the composed tree as it copies every skill. UNMEASURED: [W2](#appendix-a-evidence), [W3](#appendix-a-evidence).
- **Sealed and capture jails, and launches inside a jail.** Empty allowlist, so nothing is discovered or delivered.

## 8. The request's open questions

| Question | Answer |
| :--- | :--- |
| Does mise finish installing before the agent starts? | In the first session, yes: provisioning is synchronous and precedes the command. A later session reinstalls first. Discovery follows both. A slow install still delays the start exactly as it does today; making it asynchronous is a separate change. |
| Wait for the install, or deliver next launch? | Never wait: deliver on the next session ([§5](#5-the-three-forced-points)). |
| Plugin tools in `[install.mise]`? | Any key is accepted today (validation checks values only). The backend plugin must already be in mise; yolo manages none. |
| Mid-session changes; per-jail staging? | No watcher: a new session picks changes up. The snapshot is per workspace; each jail's delivered copies are the per-pack trees it already stages. |

## 9. Alternatives rejected

- **Return the discovery to the host and compose before the agent starts.** Rejected: it needs a jail-to-host transport and a
  bounded wait on the host for every launch, to gain one session of freshness.
- **A declared extra skills path per agent (`~/.agents/skills`, a plugin directory, an env or settings key).** Rejected:
  Claude has no home path, Pi's is unmeasured ([`agent-briefings.md`](../reference/agent-briefings.md#where-each-agent-reads-skills)),
  every spelling would be new per-agent knowledge, and skills would bypass the layer priority.
- **`mise skills sync` into the mounts, or host-side `mise skills ls`.** Rejected: the mounts are read-only, and the host has no mise call, a different platform's store on macOS, and no view of Apple Container's tool disk.

## 10. Open questions

None open. The draft's one question (may the snapshot live in `.yolo`, where a repository could commit one?) is closed by a
mechanism instead of a trade-off: [MS-D9](#11-decision-ledger).

**Snapshot token** *(coined here)*: a random value the host mints once per workspace and keeps in its own state
directory, outside the workspace. It rides the per-entry channel as `YOLO_TOOL_SKILLS_TOKEN`; the jail writes it into
the snapshot index, and the host composes a snapshot only when the index carries the current token. A snapshot a
repository commits cannot carry it, so "a repository never adds a tool's skills" holds, the snapshot stays in
`.yolo/tool-skills/` on every backend, and no mount is added. A process inside the jail can read the token, but such a
process can already write the workspace's own skills layer, so it gains nothing.

## 11. Decision Ledger

Mechanism choices the designer owns (none needed the owner), tier 1 (structural) unless noted.

| ID | Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| MS-D1 | The allowlist is `OwnMiseTools` of the user scope, in declaration order, `off` excluded, empty when sealed, captured or nested; carried as `YOLO_MISE_SKILL_TOOLS` on the per-entry channel | 2026-11-22 | [§5](#5-the-three-forced-points) | — |
| MS-D2 | Discovery is `mise -C /workspace skills ls --json` run in the jail after install, 15 s and 1 MiB bounds, own process group | 2026-11-22 | [§4](#4-the-design), [§6.3](#63-failure-and-degenerate-inputs) | — |
| MS-D3 | Match is exact string equality of `tool` with the allowlist key; order is allowlist position then name | 2026-11-22 | [§4](#4-the-design), [§6.1](#61-names-conflicts-order) | — |
| MS-D4 | The snapshot is a digest-named directory plus a `current` file, replaced by rename; skills are copied (never linked) through an `O_NOFOLLOW` walk with the workspace layer's caps | 2026-11-22 | [§4](#4-the-design), [§6.3](#63-failure-and-degenerate-inputs) | — |
| MS-D5 | The host layer has the lowest priority and re-applies the allowlist, names and caps; it never trusts the index's order | 2026-11-22 | [§6.1](#61-names-conflicts-order) | — |
| MS-D6 | A failed refresh keeps the previous snapshot; a successful empty one clears it | 2026-11-22 | [§6.3](#63-failure-and-degenerate-inputs) | — |
| MS-D7 | Staleness is a content digest compared with `YOLO_TOOL_SKILLS_DELIVERED`, so a branch-named version that changes bytes is noticed | 2026-11-22 | [§4](#4-the-design) | — |
| MS-D8 | Bare names in every selected destination; no plugin folder, no settings key | 2026-11-22 | [§9](#9-alternatives-rejected) | — |
| MS-D9 | The snapshot stays in `.yolo/tool-skills/`; the host composes it only when its index carries the per-workspace snapshot token, minted and stored host-side outside the workspace and carried as `YOLO_TOOL_SKILLS_TOKEN` | 2026-11-23 | [§10](#10-open-questions) | — |

## 12. Test-first slices

Smallest shippable first; each slice lands with its tests. Every test names the production call site it pins and must
fail if that call is deleted. All are event-driven (a blocked fake `mise` released by a channel, an injected deadline,
never a sleep or an elapsed bound) and none starts an agent or touches the network. A fake `mise` is a script on `PATH`.

| # | Slice (files) | New tests | Call site pinned |
| :--- | :--- | :--- | :--- |
| 1 | Allowlist and token: `internal/config/readableconsumers.go`, `internal/cli/run/sessioninputs.go`, the caller in `profilechannel.go` | token minted once per workspace outside it and stable across launches; user tool + workspace version override admitted; workspace-only tool absent; `off`; legacy spelling; order; empty when sealed, captured, nested; value present in the channel body | the real `composeSessionInputs` call in the profile channel build, asserted on the emitted channel text |
| 2 | Discovery: new package `internal/toolskills` | a real sampled JSON; unknown fields ignored; relative `path` refused; filter and order by allowlist; nonzero, malformed, oversized, unsupported command; hang killed on an injected deadline; the W1 stderr sample | the provisioning step that invokes it (slice 3) |
| 3 | Snapshot and step: `internal/toolskills`, `internal/provision`, `internal/entrypoint/misereprovision.go`, `internal/macosuser/provision.go` | confined copy: symlink escape, link swap, FIFO, cycle, oversize, vanished dir; digest naming and atomic `current`; nothing written outside `.yolo/tool-skills`; step runs after mise install in a fresh and a later session and never gates the bootstrap; same on the macos-user script | the generated provisioning script and `reprovisionMise`, asserted by running both |
| 4 | Host layer: `internal/jailcontent/skills.go` (new layer), `internal/cli/run/prepare.go` | pack, built-in, reserved and repository-mirror names all beat it; first tool wins and the loser is named; an unknown third-party destination receives it with no name switch; the allowlist is re-applied; tampered index ignored; an index without the current token ignored; no write into the snapshot or workspace | `refreshJailBriefings` on both the launch and the attach path |
| 5 | In-jail disclosure: `YOLO_TOOL_SKILLS_DELIVERED` and the lines of [§6.2](#62-what-the-user-reads) | one test per row; digest equal prints nothing; second concurrent session prints nothing | the step's call in slice 3 and the channel value of slice 1 |
| 6 | Integration, no agent: `integration/` | synthetic tool skills reach the destination bytes after a second session; version override changes the bytes; workspace-only tool delivers nothing; failed listing starts the session; `git status` unchanged | none new: it exercises slices 1 to 5 together |

Mac-only checks ([W2](#appendix-a-evidence), [W3](#appendix-a-evidence)) go to the owner's Mac agent through a design doc, not to the reporter.

## Appendix A. Evidence

**Upstream** (read 2026-11-22, `main`):

- `src/packslip.rs`: `Skill { name, tool, version, path: PathBuf }` derives `Serialize` with no renames;
  `MissingSkill { name, tool, version, why }` has no serde derive; `active_skills` returns `DeclaredSkills { found, missing }`.
  <https://raw.githubusercontent.com/jdx/mise/main/src/packslip.rs>
- `src/cli/skills/ls.rs`: `--json` prints `serde_json::to_string_pretty(&skills.found)`; each `skills.missing` entry is a
  `warn!` before any output; the command still succeeds. <https://raw.githubusercontent.com/jdx/mise/main/src/cli/skills/ls.rs>
- `src/backend/packslip.rs`: `STATEMENT_FILE = ".mise-packslip.json"`.
- <https://mise.jdx.dev/dev-tools/packslip-resources.html>: sync's conflict rule (never replaces what it did not make); reasons a skill is missing
  (`skills.fetch` off, `packslip.exec` off, download failed).
- Not established: the order of `found`, the text of a `MissingSkill` line, a non-empty JSON sample. Probed locally: `[]`.

**Repository** (verified at `fe37424b3`): `OwnMiseTools` (`internal/config/readableconsumers.go:47`); `JailMiseTools`
(`internal/config/derived.go:134`); `composeSessionInputs` and its one non-seal caller (`internal/cli/run/profilechannel.go:317`);
skills mounts `:ro` (`internal/cli/run/assemble.go:930`); `packSkillTargets` (`internal/cli/run/prepare.go:920`);
`confinedTree` (`internal/jailcontent/workspaceskills.go:1086`); `reprovisionMise` and `runProvisionStage`
(`internal/entrypoint/misereprovision.go:47`, `jailmain.go:555`); no host-side `mise` execution exists in `internal/` or `cmd/`;
`packs/claude/pack.toml` sets no `CLAUDE_CODE_PLUGIN_DIRS`.

**Witnesses owed** (each a measurement, not a ruling):

- **W1.** Run `mise skills ls --json` with a packslip tool whose skill failed to fetch (`skills.fetch=false`): record the
  stderr text and a non-empty JSON sample; slice 2's fixtures are copied from it.
- **W2 (owner's Mac).** On macos-user in both modes, the confined provisioning stage can write `<workspace>/.yolo/tool-skills`
  and the launcher reads it.
- **W3 (owner's Mac).** Apple Container: the same snapshot reaches the copied skills tree on the next launch.
