---
title: "A pack's files inside another pack's state folder — why agent-addressed delivery is the fix, and what yolo owes the case it cannot address"
date: 2026-10-30
status: in-review
stage: DESIGN
next: "Rule OQ-FS6 (the skip-by-default amendment to P3) first, since the pack-side change rests on it; run the agy layout check in §5.2 and rule OQ-FS1; rule OQ-FS2 to OQ-FS5"
tags: [packs, files, slots, state, macos-user, darwin-home-layout, agy, parity, happy-path]
summary: "The maintainer's matt-agentwork pack delivers an Antigravity subagent definition by path into ~/.gemini/config/agents, a folder the agy pack owns as state. On macos-user that creates a real ~/.gemini in the shared account home, locks every later agy launch out, and the refusal says to wipe the whole account. The fix on the pack side is agent-addressed files through a slot the agy pack declares; on the yolo side, a warning for the shape, a refusal that names only what is in the way, a boot that stops once the layout fails, and housekeeping for dangling layout links."
vantage:
  status-chip: true
---

# A pack's files inside another pack's state folder — why agent-addressed delivery is the fix, and what yolo owes the case it cannot address

**Status:** 2026-10-30. Nothing built. Every code claim was read against the tree at `e4345e69d`
on 2026-10-30; the Antigravity discovery layout is read from Google's own CLI documentation the same
day ([§5.1](#51-what-agy-reads-and-what-nobody-has-measured)) and is **unmeasured**. Every
leaning in this document is the design author's (Fable, the model that wrote it), not the
maintainer's; the maintainer's words are quoted where they ruled.

**Updated 2026-10-30:** re-read against `a078c5999` for the ruling round, leanings unchanged and
no question decided. The macos-user rows now separate account mode from the run-as-you `self`
mode and the [ruled per-workspace `HOME`](macos-user-model.md#7-the-ruled-model-two-modes-one-profile),
which bounds [OQ-FS3](#OQ-FS3) to [OQ-FS5](#OQ-FS5); the layout is the first step that writes,
not the first step ([OQ-FS4](#OQ-FS4)); manifests are TOML and a `home/` folder is path-addressed
`files` too ([OQ-FS2](#OQ-FS2)); [OQ-FS3](#OQ-FS3) names its sibling [OQ-HT5](../reference/macos-user-home-tiers.md#oq-ht5);
[OQ-FS6](#OQ-FS6) names the per-workspace `off` switch; and [§10](#10-the-messages)'s "today's refusal" is
quoted as the code prints it.

> **In short.** A `files` contribution that names a path inside a folder another pack declares as
> `[[state]]` is addressing an agent by a path it does not own, and every defect in the
> 2026-10-30 lab report follows from that one shape. yolo already has the right spelling —
> `files` addressed to an agent, landing in a slot the agent's pack declares — and the agy pack
> is the one agent pack with no such slot. The rest of this design is what yolo owes the shape it
> cannot forbid: say it before it costs anything, refuse naming only what is in the way, and stop
> writing once the layout it writes into has failed.

**Why it matters.** The maintainer runs Antigravity's Argon as a design seat everywhere they work,
and its research child must be discoverable wherever `agy` runs under yolo
([FS-D1](#decision-ledger)). Today the file that makes that true is delivered in a way that, on
`macos-user`, bricks every later launch that selects the agent's own pack, with a next step that
destroys the whole sandbox account.

**The shape.** One slot in `packs/agy` (`[[files]] agent = "agy", into = ".gemini/config/agents"`),
one addressed contribution in `matt-agentwork`, and one amendment to the addressing rule: content
addressed to an agent whose pack is not selected delivers nothing instead of refusing the launch
([§4.2](#42-addressing-an-agent-that-is-not-here-deliver-nothing-by-default)); plus four yolo-side
changes that are each a refusal, a warning or a stop with a next step.

**Cost.** The pack's file lands only in a launch that selects the agy pack; the addressing rule
loses the refusal that caught a well-spelled name for an unselected agent, keeping the one for a
name no pack anywhere declares; one open measurement decides whether the slot's default layout is
enough.

**Start at [§4](#4-the-pack-side-change-address-the-agent)** and
[§5](#5-the-agy-slot-and-the-one-thing-nobody-has-measured); [§6](#6-the-foreign-parent-shape-warn-where-a-pack-is-known-to-keep-state-and-nowhere-else) to
[§8](#8-dangling-layout-links-from-dead-workspaces) are what yolo owes every pack that keeps the old shape.

**Needs your ruling:** [OQ-FS1](#OQ-FS1), [OQ-FS2](#OQ-FS2), [OQ-FS3](#OQ-FS3),
[OQ-FS4](#OQ-FS4), [OQ-FS5](#OQ-FS5), [OQ-FS6](#OQ-FS6).

**Reads with:** the [lab report](../lab-reports/2026-10-30-mac-sandbox-home-collisions-and-check-nudges.md)
(the evidence: [its §2](../lab-reports/2026-10-30-mac-sandbox-home-collisions-and-check-nudges.md#2-dangling-layout-links-from-integration-runs)
and [§3](../lab-reports/2026-10-30-mac-sandbox-home-collisions-and-check-nudges.md#3-a-files-delivery-into-another-packs-state-folder)), [`macos-user-home-tiers.md`](../reference/macos-user-home-tiers.md)
(the layout, the overlay, and the no-migration ruling this design narrows),
[`pack-system.md`](../reference/pack-system.md#files) (the `files` kind and its slot mechanism),
[`slots-and-contributions.md`](slots-and-contributions.md) (the slot shape this extends),
[`backend-parity.md`](backend-parity.md) (the disposition vocabulary [§9](#9-parity-across-backends-and-notches) uses),
[`happy-path-principle.md`](../reference/happy-path-principle.md) (every message in [§10](#10-the-messages) is written to it).

---

## 1. The verdict

Four principles carry the design; later sections cite them by number.

- **P1. A path inside another pack's state folder is not a destination, it is an address.** The
  content pack means "give this to the agent that reads here", and yolo has a spelling for exactly
  that: `files` with `agents`, landing in a slot the agent's pack declares. Core never learns the
  agent's path, the content lands only while the agent's pack is selected, and it leaves when the
  pack is dropped ([`pack-system.md`](../reference/pack-system.md#files)).
- **P2. The shape cannot be forbidden, so it must be said early.** A pack delivering a helper
  script into `~/.claude` is a legitimate use `yolo host apply` was written for
  ([`hostfilestree.go`](../../internal/entrypoint/hostfilestree.go)'s header names
  `~/.claude/file-suggestion.sh`), and when the owning pack is selected the nested delivery works
  on every backend. The defect is the owner's *absence*, and the place to say so is `yolo check`
  and the launch, over the selected set alone ([§6](#6-the-foreign-parent-shape-warn-where-a-pack-is-known-to-keep-state-and-nowhere-else)).
- **P3. A refusal names what is in the way, and only that.** The whole-account reset the layout
  step prints today is the right remedy for a home that predates the layout and the wrong one for
  a single folder a pack made ([§7](#7-the-layout-refusal-name-the-path-stop-the-boot)).
- **P4. Once the layout has failed, nothing writes.** Every later macos-user boot step writes into
  the home the layout lays; running them after it failed is how a refused launch filled the real
  `~/.gemini` with generated config ([§7.2](#72-stop-the-boot)).

## 2. What exists today, stated precisely

### 2.1 The two spellings of `files`

| Written | What it is | Where it lands | Lands when |
| :--- | :--- | :--- | :--- |
| `{kind: files, from: <tree>, into: <path>}` | path-addressed content | `~/<path>`, `:ro` in a jail, written at the host | the pack is selected |
| `{kind: files, agent: <bin>, into: <dir>}` | a **slot**: where content addressed to that agent lands; ships nothing | — | — |
| `{kind: files, from: <tree>, agents: [<bin>]}` | agent-addressed content | `<slot>/<contributing pack>` (`packload.SlotLanding`, [`mergedest.go`](../../internal/packload/mergedest.go)) | the agent's pack is selected **and** declares a slot |
| a file under the pack's `home/` folder | path-addressed content, one `files` entry per file (`discoverHomeFiles`, [`conventions.go`](../../internal/packload/conventions.go)) | `~/<path under home/>` | the pack is selected |

The rows use the abstract shape. A manifest spells them as a `[[files]]` table in `pack.toml`,
with a legacy `pack.json` still read for a few releases
([`pack-manifests.md`](pack-manifests.md)). The `home/` row is convention C6, ruled with
[OQ-RC19](readable-config.md#decision-ledger) and built
([`pack-manifests.md` §3](pack-manifests.md#3-no-manifest-at-all)): it lowers to the first row,
so everything below about a path-addressed destination holds for it. A folder spelling for the
*addressed* row is [`pack-conventions.md`](pack-conventions.md)'s C2, still open as
[OQ-PC1](pack-conventions.md#OQ-PC1).

Both notches resolve addressed content through one function, `packload.ResolveDestinations`:
the jail's mount planner (`packFilesTargets`, [`packfiles.go`](../../internal/cli/run/packfiles.go)),
`yolo host apply` ([`apply.go`](../../internal/cli/apply.go)) and a host session home
([`hostsessionhome.go`](../../internal/cli/hostsessionhome.go)). A slot may `register` each landing
in a list inside one of its own pack's config surfaces and may name the top-level entries it
`expects`, warned about and never refused ([`filesregister.go`](../../internal/packdecl/filesregister.go)).
`packs/pi` is the one slot in the tree (`.pi/agent/yolo-packs`, registering into `/packages`).

Two severities already govern addressing ([`agent-briefings.md`](../reference/agent-briefings.md#two-severities-an-unknown-name-is-fatal-an-unmatched-destination-is-reported)):
an `agents` name no selected pack provides is **fatal** at the launch pre-flight and at
`yolo host apply` (`packload.AgentAudienceProblems`), and a name whose owner declares no slot of
that kind is **reported**, never refused (`reportUnmatchedAudiences`,
[`unmatchedaudience.go`](../../internal/cli/run/unmatchedaudience.go)). `yolo check` runs neither
today. The first severity has already needed one exemption: a fork's or patched extension's build
jail narrows its selection to the building pack, which dropped `pi` from under the `matt` pack's
`agents: ["pi"]` briefing and refused every build jail, so the gate is skipped under a narrowed
selection ([`seal.go`](../../internal/cli/run/seal.go)).
[§4.2](#42-addressing-an-agent-that-is-not-here-deliver-nothing-by-default) amends it.

### 2.2 How each backend materializes a path-addressed `files` destination

| Backend or notch | Delivery | What a destination's missing parent becomes |
| :--- | :--- | :--- |
| podman | `:ro` bind over the staged tree; a mountpoint is made in the per-launch home skeleton, or in the workspace state dir when the path is below a selected pack's state dir (`preparePackFiles`, [`packfiles.go`](../../internal/cli/run/packfiles.go)) | a root-owned directory in the skeleton, rebuilt every launch — this jail's `/home/agent/.gemini` is one |
| Apple Container | the same bind; the whole home is the writable workspace state dir | an ordinary directory in the per-workspace home |
| macos-user, account mode (today's default) | the host composes a home overlay and the bootstrap's `install_home_overlay` step installs it, creating missing parents beneath the account home or the workspace sidecar (`openOverlayParentIn`, [`darwinoverlay.go`](../../internal/entrypoint/darwinoverlay.go)) | **a real top-level directory in the account home every workspace shares** |
| macos-user, `macos_user_mode = "self"` (built, opt-in) | the same overlay; `HOME` is `<workspace>/.yolo/home` itself ([MMP-D5](macos-user-model-plan.md#MMP-D5), `DeriveDarwinWorkspaceHome` in [`darwinhomelayout.go`](../../internal/entrypoint/darwinhomelayout.go)) | an ordinary directory in this workspace's own home, as on Apple Container |
| `yolo host apply` | per-file write into the user's real home, refusing a path the user owns ([`hostfilestree.go`](../../internal/entrypoint/hostfilestree.go)) | a directory in the user's own home, which is what the agent reads there |

Only one row makes a persistent, shared, wrong-tier directory. That row is the lab report, and it
is the row the ruled macos-user model retires. *"Shared by both modes: a per-launch `HOME` under
the workspace's `.yolo/home`"* ([macos-user-model.md §7](macos-user-model.md#7-the-ruled-model-two-modes-one-profile));
its plan flips the default to `self` at slice 11, moves account mode onto the per-workspace
`HOME` at slice 12, and deletes the shared-home routing, `occupiedLayoutError`'s `sudo rm`
remedy included, at
[slice 13](macos-user-model-plan.md#slice-13--delete-what-nothing-uses-after-12-macos-ci).
None of the three has landed.

### 2.3 The layout, and what it refuses

On macos-user the `darwin_home_layout` step is the first step that writes into the home
([`bootsteps.go`](../../internal/entrypoint/bootsteps.go)); only `hydrate_session_env`, which
reads the session env file, and `start_egress_probe`, which starts a background probe that
`report_egress` collects at the end, run above it. In account mode it lays one symlink per
selected pack's workspace-scope `[[state]]` dir from the account home into
`<workspace>/.yolo/home`. In `self` mode those dirs are real directories in the workspace's own
`HOME`, and only machine-scope dirs and caches are links out (`DeriveDarwinWorkspaceHome`), so a
foreign parent there occupies nothing a layout link needs. `ensureLayoutSymlink` ([`darwinhomelayout.go`](../../internal/entrypoint/darwinhomelayout.go))
repoints a link yolo wrote and **never removes a real path**: a real file or directory where a link
belongs is collected, and `occupiedLayoutError` prints every account-home occupant under the heading
*"In the SANDBOX ACCOUNT HOME, which predates this layout"* with one remedy,
`sudo rm -rf /Users/_yolojail`. That is [`OQ-HT2`](../reference/macos-user-home-tiers.md#oq-ht2)'s
no-migration ruling as code, written when the only way a real path could be there was a home older
than the layout.

A boot step's failure is recorded and the boot **continues**, so every problem is reported in one
boot, and the collected set refuses the boot before the agent starts (`genStep` and
`genStepAbout`, then `genFailuresError`, in [`boot.go`](../../internal/entrypoint/boot.go); the
refusal record in [`bootrefusal.go`](../../internal/entrypoint/bootrefusal.go)). That is the
[A12 ruling](../reference/jail-home.md#why-its-this-way), and both boots run it through one
table (`runSteps`). The overlay install honors the same rule per destination.

### 2.4 The packs

- **`packs/agy`** declares a program, a briefing destination, a skills destination with
  `project_dirs = [".agents/skills"]`, config surfaces under `~/.gemini/antigravity-cli/`, autonomy,
  `[[state]] at = ".gemini"`, a machine-scope `.gemini-shared-credentials` and the hook that links the
  OAuth token into it ([`pack.toml`](../../packs/agy/pack.toml)). **No `[[files]]` slot.**
- **`matt-agentwork`** (the maintainer's, outside this tree) ships five skills and one
  path-addressed `files` contribution, `from: files/gemini/config/agents` into
  `.gemini/config/agents`, holding `design-research.md`: frontmatter `subagent: true`,
  `mainAgent: false`, `model: flash`, `commandExecutionPolicy: sandbox`. Its manifest comment says
  why it is machine-wide: agy discovers agents from the home, and a workspace without the file is a
  seat that silently researches on Argon. The `paired-design` skill's brief tells the seat to stop
  on `subagent ... not found or not allowed to be invoked`.
- The maintainer's `matt` pack ships a pi `designer` agent into `~/.pi/agent/agents` the same
  way: path-addressed into pi's state folder. It has never collided only because `pi` is selected
  in every one of their launches. It is the same shape, and [§6](#6-the-foreign-parent-shape-warn-where-a-pack-is-known-to-keep-state-and-nowhere-else)'s
  warning stays quiet for it exactly as long as that holds.

## 3. The diagnosis

Each finding of the lab report, and the one cause under it:

| # | Finding | Cause |
| :--- | :--- | :--- |
| 1 | A launch without agy creates a real `/Users/_yolojail/.gemini`; every later launch selecting agy is refused | the file is path-addressed into a folder whose owner is not selected (P1); macos-user materializes the parent in the shared tier ([§2.2](#22-how-each-backend-materializes-a-path-addressed-files-destination)) |
| 2 | The refusal says `sudo rm -rf /Users/_yolojail` when only `.gemini` is in the way | `occupiedLayoutError` has one remedy for every account-home occupant (P3) |
| 3 | The refused launch wrote agy's `settings.json`, `mcp_config.json`, `cache/onboarding.json` and the token link into the real folder | the boot continues after the layout step fails (P4) |
| 4 | Nothing warns ahead of time | no reader compares a `files` destination against the selected packs' state dirs (P2) |
| 5 | A dangling `.gemini` link from a deleted integration workspace refused a later matt-agentwork launch until `sudo rm` | a layout link this launch does not declare is left alone by design; a path-addressed delivery under it is refused as a link yolo did not lay ([§8](#8-dangling-layout-links-from-dead-workspaces)) |
| — | On Linux podman, a root-owned `/home/agent/.gemini` exists in a jail that does not select agy | the same shape, materialized per launch in the skeleton; harmless today, and the reason the warning in [§6](#6-the-foreign-parent-shape-warn-where-a-pack-is-known-to-keep-state-and-nowhere-else) is not macOS-only |

Finding 1 is the design's center. Findings 2 to 5 are owed to every pack that keeps the shape, and
to the maintainer until theirs moves.

## 4. The pack-side change: address the agent

### 4.1 The contribution

`matt-agentwork` replaces its path-addressed entry with an addressed one (shown in the `pack.toml` form):

```toml
[[files]]
agents = ["agy"]
from = "files/agy"
```

and the manifest comment above it reads:

> The Flash research child the `agy` design seat delegates to. Addressed to the agent, not to a
> path: agy's own pack says where agent definitions live and lays that folder per workspace, so
> this lands there only in a launch that selects `agy`, and leaves when it is dropped. A workspace
> may still override it with its own `.agents/agents/` definition, which agy reads first.

What the tree under `files/agy` holds depends on [OQ-FS1](#OQ-FS1); the README's "Design with an
expensive seat" section replaces its `~/.gemini/config/agents/design-research.md` sentence with the
landing path that question settles, and says the file lands only in a launch that selects the
`agy` pack.

**Why not the per-workspace `.agents/agents/` path.** The maintainer runs Argon through `agy` in
every setting — on the host, inside jails, as a subprocess of another agent and directly — and in
every project ([FS-D1](#decision-ledger)). A copy in each project tree would have to be carried by
every repository they touch, including ones that are not theirs. It stays only as the override agy
already offers, which the comment above keeps.

### 4.2 Addressing an agent that is not here: deliver nothing, by default

Under the rule as it stands, the change in [§4.1](#41-the-contribution) makes every launch that
selects `matt-agentwork` and not `agy` **refuse**: an `agents` name no selected pack provides is
fatal ([§2.1](#21-the-two-spellings-of-files)), and the maintainer's config does not select `agy`
today. The maintainer's direction, dictated 2026-10-30 and recorded here as a ruling in progress:

> I'm thinking we shouldn't refuse the launch with agy not selected, because we need a way to have
> a pack distribute configs for agents in case they're enabled, so we could add an `if` key, but I
> feel like we should just make that the default.

**My opinion: they are right, and the standing rule was aimed at a different case.** A content
pack's reason to address an agent is "for whoever has this agent", which is what `briefing` and
`skills` already mean when they broadcast: they reach the selected agents and say nothing about
the rest. Addressed content is the one place where the agent's absence is a refusal, and the tree
already shows the strain: the build-jail exemption in [§2.1](#21-the-two-spellings-of-files)
exists because a narrowed selection dropped an agent a house-rules pack addressed. The rule's
stated reason — "a typo and an unselected agent are the same mistake" — holds only if yolo cannot
tell them apart, and it can.

**The amendment** (proposed to [P3](../reference/agent-briefings.md#ba-p3) and
[`OQ-BA3`](../reference/agent-briefings.md#oq-ba3); [OQ-FS6](#OQ-FS6) rules it):

| An `agents` name is | Decided from | Severity | What the user sees |
| :--- | :--- | :--- | :--- |
| claimed by a **selected** pack | the selected set | delivered, or reported if the owner declares no slot of the kind (R1, unchanged) | today's behavior |
| claimed by a pack the user **could select** — shipped, or configured in their `packs` — but not by a selected one | the wider vocabulary | **skipped**: the contribution delivers nothing, the launch runs | one `yolo check` `[SKIP]` line naming the pack to add; one launch line in the pack disclosure |
| claimed by **no pack anywhere** | the wider vocabulary | **fatal**, as today, with the candidate list and did-you-mean | today's refusal |
| in a vocabulary yolo could not finish reading (a configured pack not fetched yet, or its tree not checked out) | — | **reported, not refused**: the name may belong to that pack; `yolo check` already reports the broken pack louder and first | one line naming the pack it could not read |

So `agents: ["agyy"]` still stops the launch, and `agents: ["agy"]` in a launch without agy is
exactly what the manifest comment in [§4.1](#41-the-contribution) says it is.

"Could select" includes a pack the user configured and a workspace turned off. The per-workspace
`off = true` on a pack entry is L4's ([`config-user.md` A.4](config-user.md#a4-what-l4-amends)),
kept as a pack switch by the access-approval design under ruling now
([`loophole-access-approval.md`](loophole-access-approval.md), its table of what goes away) and
not built: a pack off in
one workspace runs none of its machinery there. Under the amendment its agent's addressed content
is skipped there too, the same rule from the content side.

**Where the wider vocabulary comes from, and why it is not a reservation.**
[`OQ-BH14`](../reference/jail-home.md#oq-bh14) rules that an unselected pack reserves nothing: its
declarations may not make a user's config entry refused. This reads unselected packs in the
opposite direction — their existence makes a name *acceptable* — and the only refusal left is of a
name no pack the user could select declares, which selecting a pack could never fix. The
`AGENTS.md` rule is against reading `packload.Embedded*` *to reserve a name*; spelling validation
is not that, and there is a precedent: `config.UseProfileCLINames` unions `packload.Embedded()`
deliberately not selection-gated, for a message about the universe ([`packs.go`](../../internal/config/packs.go);
`AgentNames`'s header in [`footprint.go`](../../internal/packload/footprint.go) names it as the
neighboring wider set). No second process-lifetime copy is made: `packload.Embedded()` returns the
process's one leased tree, and a configured pack's declaration is read the way validation already
reads it, `ResolvePack` in declaration mode with a read-only store (`SelectedPackDeclarations`,
[`selectedpacks.go`](../../internal/config/selectedpacks.go)), over every `packs` entry rather than
the selection. The vocabulary is the union of the two sets' agent identities (`AgentNames` over
each), computed once per launch, `yolo host apply` and `yolo check`.

**Default, not an opt-in key.** An `if`/`optional` key would keep today's refusal for packs that do
not write it. Against it: the key is what every content pack would write, since addressing an
agent *means* "if present"; it is a second behavior to document and test; and the cost the key
would guard against, a silently skipped typo, is already caught by the wider vocabulary. The one
case the key would catch and the default does not — a correctly spelled agent the user *meant* to
select — is the case the maintainer wants to work. No inverse `required` key is proposed either;
a pack that cannot work without an agent declares `needs` on its pack.

**What it changes elsewhere.** The build-jail exemption in [`seal.go`](../../internal/cli/run/seal.go)
for this gate becomes unnecessary and may stay; `yolo check` gains the gate it lacks today, with
the skip as `[SKIP]`, which it counts apart from passes; `yolo host apply`'s refusal narrows the
same way. Splitting the agy file into its own pack selected beside agy (a `matt-agy`) was the
earlier way around the refusal; under the amendment it is unnecessary and is not proposed.

## 5. The agy slot, and the one thing nobody has measured

### 5.1 What agy reads, and what nobody has measured

Google's Antigravity CLI documentation describes custom agents as a **directory per agent holding
`agent.md`**: the `/agents` panel's header shows the templates
`{workspace}/.agents/agents/{agent_name}/agent.md` and `~/.gemini/config/agents/{agent_name}/agent.md`,
its instructions say "create a directory matching your agent name and add an `agent.md` file with
YAML frontmatter", its example frontmatter carries `name: code-reviewer`, and its mistakes table
says the scanner "looks specifically inside `agents/` directories"
([Agents command](https://antigravity.google/docs/cli/commands/agents), read 2026-10-30). The
maintainer's flat `agents/design-research.md` works too, by their own report, so agy reads at
least two shapes. The documentation does not say whether the agent's name comes from the
directory or from the frontmatter `name`, and does not say whether the scan recurses.

That gap is exactly the question a slot raises. `SlotLanding` puts a contributing pack's tree at
`<slot>/<pack>`, so with a slot at `.gemini/config/agents`:

| The pack ships | agy sees | Loads if |
| :--- | :--- | :--- |
| `files/agy/design-research.md` (today's file) | `agents/matt-agentwork/design-research.md` | the scan recurses into a directory and reads flat files there — **the least likely reading of the docs** |
| `files/agy/agent.md` with `name: design-research` | `agents/matt-agentwork/agent.md` — the documented layout | the agent's name comes from frontmatter; if it comes from the directory, the agent is called `matt-agentwork` |
| `files/agy/design-research/agent.md` | `agents/matt-agentwork/design-research/agent.md` | the scan recurses one level |

### 5.2 The one check that settles it, and who runs it

No automated test may start an agent (the repository's rule: `--version` probes only), so this is
the maintainer's to run once, on any machine with `agy`:

1. Create `~/.gemini/config/agents/probe-pack/agent.md` with the frontmatter of
   `design-research.md` and `name: probe-by-frontmatter`.
2. Create `~/.gemini/config/agents/probe-pack/probe-nested/agent.md` with `name: probe-nested`,
   and `~/.gemini/config/agents/probe-pack/probe-flat.md` with `name: probe-flat`.
3. Start `agy`, open `/agents`, and read **Available Agents**.
4. Remove `~/.gemini/config/agents/probe-pack`.

What the panel lists decides the row, and the row decides the branch:

| Listed | Meaning | Branch |
| :--- | :--- | :--- |
| `probe-by-frontmatter` | the name comes from frontmatter | **A** — the plain slot is enough |
| `probe-pack` | the name comes from the directory | **B** — the slot needs a way to name the landing |
| `probe-nested` or `probe-flat` as well | the scan recurses | **C** — a pack may ship several agents under one tree |

### 5.3 The slot, in each branch

The slot itself is the same in every branch, and it is a change to a pack yolo ships, so it is a
yolo change ([FS-D3](#decision-ledger)):

```toml
[[files]]
# Where a pack lands an Antigravity agent definition: agy reads a directory per agent holding
# agent.md under this folder (Antigravity CLI docs, "Agents command"), and SlotLanding's
# <slot>/<pack> is that directory. Outside every folder agy's own config generators write.
agent = "agy"
expects = ["agent.md"]
into = ".gemini/config/agents"
```

- **Branch A.** Nothing else. A content pack ships one agent per tree with `agent.md` at the root;
  `expects` warns at `pack lint` when it is missing. One agent per contributing pack is the limit,
  and a pack wanting two ships two packs or waits for C.
- **Branch B.** The agent would be named for the contributing pack. Two ways out, and the design
  prefers the first:
  - **`as` on the addressed contribution** — `{kind: files, agents: ["agy"], from: ..., as: "design-research"}`
    replaces the contributing pack's name in the landing with the name given. It keeps every
    property the per-pack subdirectory exists for (nothing lands at the slot root; the owner's own
    tree is untouched), moves the one collision class from "impossible" to "refused by name" at
    the same pre-flight that refuses two `files` claims on one path today (`PackDestConflicts`),
    and feeds `register` the same landing. `as` is refused on a path-addressed contribution and
    on a slot, must be a single path component, and two addressed trees for one agent with one
    `as` across the selected set are refused naming both packs.
  - **`layout = "flat"` on the slot** — land each contributor's entries directly in the slot. It
    reaches more agents' conventions but gives up the subdirectory, so the pre-flight has to
    refuse any basename two contributors share, and `register` has nothing to list. Kept as the
    fallback for an agent whose convention has no subdirectory at all.
- **Branch C.** The plain slot, and a pack may ship `files/agy/<name>/agent.md` several times over.
  `expects` then names nothing, or the slot drops it.

The slot is spelled as `packs/pi`'s is today. If the slot split
([`slots-and-contributions.md`](slots-and-contributions.md), migration window open as
[OQ-D6](slots-and-contributions.md#OQ-D6)) is built, both are respelled together; nothing here
depends on the spelling.

`register` is not an option here: agy's `settings.json` is an alias table for models and names no
list of agent paths that anyone has found. If one exists it becomes branch A's refinement, not a
new branch.

## 6. The foreign-parent shape: warn where a pack is known to keep state, and nowhere else

**Foreign-parent delivery** *(coined here)* is a path-addressed `files` destination inside a
dot-directory that some pack the user could select — shipped, or configured in their `packs` —
declares as `[[state]]`, in a launch that does not select that pack, whether a `[[files]]` entry
or a file under the pack's `home/` folder spells it ([§2.1](#21-the-two-spellings-of-files)). It is not a nested delivery
into a *selected* pack's state, which works on every backend and stays legal, and it is not a
delivery into a dot-directory no pack anywhere claims, which is nobody's business but the pack's.

### 6.1 What is legitimate here, and why it is not an error

The maintainer asked what the valid use is, and why this is not an error. In plain words:

- **A pack configuring a tool yolo has no pack for.** A pack that ships `~/.tmux/plugins`, a
  `~/.ripgreprc`, or an agent definition for a CLI nobody has written a pack for is doing the only
  thing it can: naming the path. No pack claims the folder, so nothing here fires, and nothing
  should; a warning on every launch of a correct config is noise that teaches people to ignore
  warnings.
- **A pack configuring an agent the user runs unmanaged.** This is the maintainer's own case today:
  `agy` hand-installed in a jail, reading a real `~/.gemini`, and `matt-agentwork` putting the file
  where it reads. At `yolo host apply` the same delivery lands in the user's real home, which is
  exactly where that agent reads. The config is *correct for its author*; what is wrong is only
  that on `macos-user` the folder it makes is in the wrong tier, and that the author had a better
  spelling. That is a warning's shape: the launch runs, the file lands, and the line names the
  spelling that costs nothing now that [§4.2](#42-addressing-an-agent-that-is-not-here-deliver-nothing-by-default)
  makes an absent agent a skip rather than a refusal.
- **Why not an error.** On podman the folder is a per-launch skeleton entry and on Apple Container
  an ordinary directory in the per-workspace home; neither outlives anything or blocks anyone. An
  error would refuse, on every backend, a config that works on two of them and is the intent at the
  host, over a hazard one backend has — a macos-user rule dressed as a pack rule, against the
  parity principle. And the hazard it would guard against is already met twice over: the warning
  names the fix, and [§6.3](#63-on-macos-user-record-what-the-overlay-makes) keeps the folder from
  locking anyone out. The hazard is narrower still than one backend: it is macos-user's account
  mode, whose shared account home the ruled model retires ([§2.2](#22-how-each-backend-materializes-a-path-addressed-files-destination)).
  In `self` mode the folder lands in the workspace's own home, as on Apple Container.

### 6.2 Three readers, one rule

| Reader | Knows | Says |
| :--- | :--- | :--- |
| `yolo pack lint <dir>` (no config) | the one pack, and the shipped set | a **note** when a shipped pack declares the destination's dot-directory as state: address that pack's agent instead ([§10](#10-the-messages)) |
| `yolo check`, Packs section | the selected set and the wider vocabulary | a **warning** when a pack the user could select, and did not, declares the dot-directory as state; silent when a selected pack does (the nested case) or when none does |
| the launch pre-flight (`stagePacks`) and `yolo host apply` | the same | the same warning, once, on stderr beside `reportUnmatchedAudiences`'s line |

The rule reads the same wider vocabulary as [§4.2](#42-addressing-an-agent-that-is-not-here-deliver-nothing-by-default),
and for the same reason it is no breach of [`OQ-BH14`](../reference/jail-home.md#oq-bh14): it
reserves nothing and refuses nothing. It names an unselected pack in *advice*, as
`config.UseProfileCLINames` already does, and the remedy it names is the pack's spelling, not the
user's config. Its one new cost is a warning on the unmanaged-agent case above, which is the case
the warning exists for: that user is one `macos-user` launch away from the lab report.
[OQ-FS2](#OQ-FS2) asks the maintainer to confirm the narrow rule over the broad one (warn on any
undeclared dot-directory) and over an error.

### 6.3 On macos-user: record what the overlay makes

The overlay install already records what it delivered (`writeOverlayInstallRecord`,
[`darwinoverlay.go`](../../internal/entrypoint/darwinoverlay.go)). It additionally records **each
top-level account-home directory it created** for a destination, with the pack and contribution
that asked for it. That record has two readers:

- `occupiedLayoutError` ([§7.1](#71-name-the-path)) names the pack beside the occupying path when
  the record proves what made it.
- The layout step, when a real directory sits where a link belongs **and** the record says the
  overlay made it **and** the directory holds nothing but what the record says was delivered,
  replaces it with the layout link and says so on one line ([§10](#10-the-messages)). Anything
  else in it — an agent's sessions, a hand-written file, the generated config a refused launch
  left — and the directory is left alone and refused by name. [OQ-FS3](#OQ-FS3) asks whether this
  narrows [`OQ-HT2`](../reference/macos-user-home-tiers.md#oq-ht2) acceptably: that ruling protects
  a user's home from migration, and a directory yolo made and nobody else wrote is not the user's.
  [`OQ-HT5`](../reference/macos-user-home-tiers.md#oq-ht5), open and leaning to keep refusing,
  asks the same of a home-root `host_files` file an older launch rendered, so the two are best
  ruled together.

Both the record and the reclaim are account-mode machinery: in `self` mode no layout link sits
where a workspace-scope state dir is, and the ruled model's slice 13 deletes the account-home
routing they extend ([§2.2](#22-how-each-backend-materializes-a-path-addressed-files-destination)).

## 7. The layout refusal: name the path, stop the boot

### 7.1 Name the path

`occupiedLayoutError` drops the whole-account remedy from the account-home group
([FS-D4](#decision-ledger)). Each occupying path is listed with what made it when the overlay
record says, and the remedy is one `sudo rm -rf <path>` per path after "move what you want to
keep". The sidecar and `host_files` groups are unchanged; they already name their paths. The
account-reset sentence survives only as a last line when **every** occupied path is one the record
cannot attribute and there are more than one, since that is the only case left that looks like a
home older than the layout.

### 7.2 Stop the boot

On macos-user, a `darwin_home_layout` failure **ends the bootstrap** after the refusal is written
([OQ-FS4](#OQ-FS4) rules it). It is the first step that writes, so nothing is lost from the
one-boot report: the two steps above it read the session env and start the egress probe, which
the stop leaves uncollected, so that refused boot reports no egress finding. Every later step writes into tiers
the layout did not lay. The refusal record lists the layout failure alone, and the last line says
why nothing else ran ([§10](#10-the-messages)). This narrows [A12](../reference/jail-home.md#why-its-this-way)'s
"after every remaining generator has run" for this one step on this one backend, in both modes
(the `self`-mode layout still refuses a linked sidecar path or an occupied machine link). The
container boot keeps its rule: its tiers are the launcher's mounts, and no step there has this
precondition.

## 8. Dangling layout links from dead workspaces

A layout link whose target sidecar no longer exists is left alone by a launch that does not
declare it (`TestDarwinHomeLayoutSurvivesAStaleLinkFromADeletedWorkspace`) and repointed by one
that does. Both are right. The defect is that nothing ever removes one, and the integration suite
leaves one per launch that selected a stateful pack.

- **Agent-addressed delivery removes the collision**: the file lands only when the owning pack is
  selected, and then the layout repoints the link before the overlay installs through it.
- **Housekeeping for what remains** ([OQ-FS5](#OQ-FS5)): `yolo check` on a macos-user host warns
  per dangling layout link in the account home, naming the workspace path it pointed at, and
  `yolo prune` lists them with `--apply` removing the link and never its target, in prune's
  dry-run-then-apply shape. The integration suite's macos-user helpers remove the links their own
  launches laid when the test ends, which is test hygiene and needs no ruling.

A target on an unmounted volume looks dangling too. Prune is explicit and names each target, so
the user decides; the layout never removes one on its own.

Only account mode lays workspace links in a shared home. The ruled model gives both modes a
per-workspace `HOME` ([§2.2](#22-how-each-backend-materializes-a-path-addressed-files-destination)),
and its [OQ-MM4](macos-user-model.md#OQ-MM4) rules that once launches move, *"nothing migrates,
cleans up or announces the old account"*. So the housekeeping above serves account-mode launches
until the plan's slice 12, and is not a cleanup of the old account after it.

## 9. Parity across backends and notches

Dispositions are [`backend-parity.md`](backend-parity.md#3-the-dispositions--the-most-important-section)'s.

| Behavior | podman | Apple Container | macos-user | `yolo host apply` / `yolo host --` |
| :--- | :--- | :--- | :--- | :--- |
| Agent-addressed `files` lands at `<slot>/<pack>` when agy is selected | Honored (bind) | Honored (bind) | Honored (overlay) | Honored (per-file write) |
| An `agents` name no pack anywhere declares is fatal | Honored | Honored | Honored | Honored |
| An `agents` name for a known, unselected pack is skipped, with one line | Honored | Honored | Honored | Honored |
| Foreign-parent warning | Honored | Honored | Honored | Honored |
| Foreign parent materialized | HonoredBy: per-launch skeleton dir, gone at exit | HonoredBy: dir in the per-workspace home | account mode: **Warned**, and recorded ([§6.3](#63-on-macos-user-record-what-the-overlay-makes)); `self` mode: HonoredBy: dir in the per-workspace home | NotApplicable: the user's home is the right tier |
| Layout refusal names the path | NotApplicable | NotApplicable | Honored | NotApplicable |
| Boot stops after layout failure | NotApplicable: no layout step | NotApplicable | Honored, both modes | NotApplicable |
| Dangling-link housekeeping | NotApplicable | NotApplicable | Honored in account mode; NotApplicable in `self` mode, which lays no workspace link in a shared home | NotApplicable |

Every row a backend honors by a different mechanism names it, as the census requires, and the
annotation test (`backendparity_test.go`) covers any runtime-gated line the build adds.

## 10. The messages

Each is the whole message, with its next step on the following line, per the
[Happy Path Principle](../reference/happy-path-principle.md).

**`yolo check`, Packs section — the foreign-parent warning** (the launch prints the same as
`Warning:` on stderr):

```text
  [WARN] pack matt-agentwork: files into ~/.gemini/config/agents lands inside ~/.gemini, where the agy pack keeps its state, and agy is not selected
       -> On macos-user's account mode this makes ~/.gemini a real directory in the account home every workspace shares, and a later launch that selects agy is refused there. Address the agent instead; the files then land in agy's own folder when agy is selected, and nowhere when it is not. In that [[files]] entry of the pack's manifest, replace the into line with:
            agents = ["agy"]
          then: yolo check
```

**`yolo check`, Packs section — a skipped audience** ([§4.2](#42-addressing-an-agent-that-is-not-here-deliver-nothing-by-default);
the launch prints the first line in its pack disclosure):

```text
  [SKIP] pack matt-agentwork: files for agy not delivered — no selected pack provides agy
       -> If you want it here:  yolo pack add agy
```

**`yolo check` and the launch — a name no pack declares.** Today's refusal, as
`agentAudienceMessage` ([`agentaudience.go`](../../internal/packload/agentaudience.go)) prints it:

```text
Error: packs: pack matt-agentwork: files `agents` names "agyy", which no pack in `packs` provides — did you mean "agy"?. Agents your `packs` provide: claude, pi. Fix the name, or add the pack that provides "agyy"
```

Under the amendment it keeps that shape, names the wider candidate list beside the selected one,
and drops "add the pack", since no pack provides the name:

```text
Error: packs: pack matt-agentwork: files `agents` names "agyy", which no pack you could select provides — did you mean "agy"? Agents your `packs` provide: claude, pi; every pack you could select provides: agy, claude, codex, copilot, pi. Fix the name.
```

**`yolo pack lint`** (a note, exit 0):

```text
  note  files into .gemini/config/agents lands inside .gemini, where the agy pack keeps its state. Address its agent instead, so the files land in agy's folder when agy is selected and nowhere when it is not: in that [[files]] entry, replace the into line with agents = ["agy"]
```

**`darwin_home_layout`, an occupied account-home path** (replacing today's group):

```text
Error: darwin_home_layout: the per-workspace home layout cannot be laid: 1 path is real, where the workspace tier's symlink belongs.
There is no migration (macos-user-home-tiers.md OQ-HT2) — nothing here is copied, renamed or deleted for you.

In the SANDBOX ACCOUNT HOME:
  /Users/_yolojail/.gemini  (made by a launch of workspace /Users/Shared/yolo/launch-check on 2026-10-30, delivering matt-agentwork's files/gemini/config/agents; it now holds other files too)
Move what you want to keep out of each path, then remove it; the rest of the account is left alone:
  sudo rm -rf /Users/_yolojail/.gemini
then launch again.
```

**`darwin_home_layout`, reclaiming a directory the overlay made** ([§6.3](#63-on-macos-user-record-what-the-overlay-makes), one line, not an error):

```text
darwin_home_layout: replaced /Users/_yolojail/.gemini with this workspace's layout link — a launch of /Users/Shared/yolo/launch-check made it for matt-agentwork's files, and nothing else was ever written in it.
```

**The bootstrap, after a layout failure** (the last line of the refusal):

```text
darwin_home_layout failed, so no later step ran: every one of them writes into the home this layout lays. Fix the path above, then launch again.
```

**`yolo check` on a macos-user host, a dangling layout link:**

```text
  [WARN] /Users/_yolojail/.gemini is a layout link to /Users/Shared/yolo/yolo-it-1873416182/.yolo/home/gemini, which no longer exists
       -> A launch of a workspace that was since deleted laid it. Remove it (the target is already gone):
            yolo prune --apply
          then: yolo check
```

## 11. Tests

Each is named so that deleting the production call site fails it, and each says what it asserts.

| Test | Pins | Fails when |
| :--- | :--- | :--- |
| `TestAgyDeclaresAFilesSlotForAgentDefinitions` (`internal/packload`) | `packs/agy` loaded through `packload.Embedded()` declares one `files` slot at `.gemini/config/agents` expecting `agent.md` | the slot is removed or moved |
| `TestAnAgyAddressedTreeLandsAtBothNotches` (`internal/cli/run`, beside `filesslotparity_test.go`) | a fixture pack `{agents: ["agy"]}` lands at `.gemini/config/agents/<pack>` in `packFilesTargets` and in `RenderHostFiles` after `ResolveDestinations`; nothing lands without `agy` selected | either notch stops resolving through `SlotLanding` |
| `TestCheckWarnsOnFilesIntoAKnownUnselectedPacksStateDir` (`internal/cli/check`) | a fixture `files` into `.probe/x`, and a second fixture with `home/.probe/x`, with a *configured but unselected* fixture declaring `[[state]] at = ".probe"`, each warn with the [§10](#10-the-messages) text; selecting that pack silences it; with no pack anywhere declaring `.probe` nothing is printed | the check is deleted, warns on the nested case, or warns on an unclaimed dot-directory |
| `TestAnAddressedTreeForAKnownUnselectedAgentIsSkippedNotRefused` (`internal/cli/run`, and the `yolo host apply` twin in `internal/cli`) | a fixture pack `{agents: ["probe"]}` with the `probe` agent's pack configured and unselected: the launch proceeds, delivers nothing for it, and prints the one skip line | the pre-flight refuses, or delivers |
| `TestAnAgentsNameNoPackDeclaresStaysFatal` | the same fixture with `agents: ["probee"]` refuses, naming both the selected and the selectable candidates and the did-you-mean | the refusal is lost with the amendment |
| `TestAnUnreadableConfiguredPackMakesAnUnknownAudienceAReportNotARefusal` | a configured entry the read-only store cannot resolve: an unmatched name is reported with the pack it could not read, and the launch proceeds | the gate refuses on an incomplete vocabulary |
| `TestCheckCountsASkippedAudienceAsSkipNotPass` (`internal/cli/check`) | the skip line is `[SKIP]`, counted apart from passes, and ends with `yolo pack add <pack>` | the line becomes an `[OK]` or loses its next step |
| `TestTheAudienceVocabularyReadsOneEmbeddedTree` (`internal/packload`) | computing the wider vocabulary twice in one process materializes no second embedded tree | a second process-lifetime copy returns |
| `TestLaunchWarnsOnAForeignParentDelivery` (`internal/cli/run`) | `stagePacks` prints the same warning once, naming the unselected owning pack; the launch proceeds | the pre-flight line is deleted |
| `TestPackLintNotesAForeignParentDelivery` (`internal/cli`) | `yolo pack lint` on a pack delivering into a shipped pack's state dir prints the note naming that pack's agent and exits 0; into an unclaimed dot-directory it prints nothing | the note is deleted, becomes a refusal, or fires on an unclaimed directory |
| `TestOccupiedLayoutRefusalNamesEachPathAndNotTheAccount` (`internal/entrypoint`) | the message carries `sudo rm -rf <path>` per occupant and not `sudo rm -rf <home>`, for one and for several | the account reset returns |
| `TestOccupiedLayoutRefusalNamesThePackThatMadeThePath` | with an overlay record attributing the directory, the line names pack, contribution and workspace | the record is not read |
| `TestTheOverlayRecordsATopLevelHomeDirectoryItCreates` | installing a destination whose top-level parent is absent writes the attribution | the record write is deleted |
| `TestTheLayoutReclaimsADirectoryOnlyTheOverlayWrote` / `…LeavesOneHoldingOtherFiles` | the two [§6.3](#63-on-macos-user-record-what-the-overlay-makes) outcomes, over a real temp home | the reclaim is deleted, or reclaims too much |
| `TestADarwinLayoutRefusalStopsTheBoot` (`internal/entrypoint`) | `runSteps` over a synthetic table with a failing `darwin_home_layout`: no later step runs on the darwin target in either mode, every later step runs on the container target, the last line is the [§10](#10-the-messages) text | the stop is deleted or leaks to the container boot |
| `TestCheckReportsADanglingLayoutLinkAndPruneRemovesIt` | the warning names link and target; `yolo prune` lists it; `--apply` removes the link and leaves a present target alone | either half is deleted |
| `TestNoShippedPackDeliversFilesIntoAnotherShippedPacksState` (`internal/packload`) | over the embedded set, no path-addressed `files` destination lies under another shipped pack's `[[state]]` | a shipped pack adopts the shape |
| `TestMacosUserAForeignParentDoesNotLockOutItsOwner` (`integration`, macos-user hardware, account mode) | launch A: a fixture pack `files` into `.probe/x`, the owning fixture configured and not selected; launch B: a fixture declaring `[[state]] at = ".probe"`, exit 0 and `~/.probe` a link, or (without [OQ-FS3](#OQ-FS3)) a refusal naming only `~/.probe`; and A's launch log carries the warning | any of [§6](#6-the-foreign-parent-shape-warn-where-a-pack-is-known-to-keep-state-and-nowhere-else) and [§7](#7-the-layout-refusal-name-the-path-stop-the-boot) regresses |
| `TestEveryHintedYoloCommandExists` (existing) | every `yolo …` in the new messages is a real command | a message names a verb that is not there |

Nothing here starts `agy`. The one measurement that needs it is [§5.2](#52-the-one-check-that-settles-it-and-who-runs-it)'s, and it is the maintainer's.

## 12. What this does not propose

- No change to how a nested `files` destination under a **selected** pack's state works; it works
  on every backend and this design relies on it.
- No reversal of the fatal half for a name **no pack anywhere declares**: a typo stays a refusal
  with its candidates and did-you-mean. [§4.2](#42-addressing-an-agent-that-is-not-here-deliver-nothing-by-default)
  narrows what counts as unknown; it does not make every name acceptable.
- No `if`/`optional` key on an addressed contribution, and no `required` inverse: skip is the
  default, and a pack that cannot work without an agent says so with `needs`.
- No warning on a path-addressed `files` destination in a dot-directory no pack the user could
  select claims as state.
- No `[[state]]` for `matt-agentwork` over `.gemini`: two packs claiming one state dir is a
  collision, not a fix.
- No general "deliver into the owning pack's location even when it is not selected": that is
  core learning which pack owns a path it was told not to know, and it resurrects the
  shipped-set reservation [`OQ-BH14`](../reference/jail-home.md#oq-bh14) removed.
- No change to the container boot's report-every-problem rule.
- No per-project `.agents/agents/` delivery by yolo ([FS-D1](#decision-ledger)).

## 13. Alternatives considered

| Alternative | Verdict |
| :--- | :--- |
| Keep path-addressed delivery and have the layout migrate a real `.gemini` into the sidecar | Rejected: [`OQ-HT2`](../reference/macos-user-home-tiers.md#oq-ht2) rules out migration of anything that might be the user's; [§6.3](#63-on-macos-user-record-what-the-overlay-makes) reclaims only what the record proves yolo alone wrote |
| Refuse any path-addressed `files` whose parent is another known pack's state | Rejected: refuses, on every backend, a config that is correct at the host and harmless on two backends, over a hazard one backend has ([§6.1](#61-what-is-legitimate-here-and-why-it-is-not-an-error)) |
| Warn on any path-addressed `files` destination in a dot-directory no selected pack declares (the broad rule) | Rejected: a pack configuring a tool yolo has no pack for would warn on every launch forever, and a permanent warning on a correct config trains people to ignore warnings; the hazard is only a folder some pack keeps state in ([§6](#6-the-foreign-parent-shape-warn-where-a-pack-is-known-to-keep-state-and-nowhere-else)) |
| A `register` into an agy settings list of agent paths | Not available: no such list is documented or found; becomes a refinement if one appears |
| Per-project `.agents/agents/` as the primary delivery | Rejected by the maintainer ([FS-D1](#decision-ledger)); kept as agy's own override |
| An opt-in key (`if`, `optional`) making one addressed contribution survivable where its agent is absent | Rejected in favor of the default: every content pack would write it, it is a second behavior to document, and the typo it guards against is caught by the wider vocabulary ([§4.2](#42-addressing-an-agent-that-is-not-here-deliver-nothing-by-default)) |
| Keep the refusal and select `agy` in the maintainer's user config everywhere | Rejected by the maintainer's direction: a pack must be able to ship config for an agent in case it is enabled |
| Split the agy file into its own pack (`matt-agy`) selected beside `agy` | Rejected: a workaround for the refusal the amendment removes; two packs to keep in step for one file |
| A `flat` slot layout as the first answer to branch B | Deferred: `as` keeps the subdirectory's guarantees and solves the same case |

## 14. Risks

| Risk | Mitigation |
| :--- | :--- |
| agy's scanner names the agent for the directory (branch B) and `as` is not built yet | the paired-design brief names the agent `matt-agentwork` until it is; the file still loads |
| A well-spelled `agents` name for a pack the user forgot to select is skipped in silence | the skip is never silent: `yolo check` prints a `[SKIP]` line ending in `yolo pack add <pack>`, and the launch prints one line in its pack disclosure |
| A typo that happens to spell another real agent (`agents: ["pi"]` for `agy`) is skipped rather than refused | it was never a refusal: today that name is accepted when pi is selected and delivered to pi; the amendment does not widen that case |
| The wider vocabulary reads a configured pack's declaration at every launch | validation already reads every selected entry the same way, read-only and without fetching; the extra cost is the unselected entries, read once per launch |
| The reclaim in [§6.3](#63-on-macos-user-record-what-the-overlay-makes) replaces a directory that held agent state the record did not see | the reclaim requires the directory's content to match the record exactly; one unknown entry leaves it alone |
| Stopping the boot hides a second, independent problem until the next launch | the layout is the first step, so the second problem was never visible in the same boot before either; the refusal says why nothing else ran |
| The warning fires on a pack whose foreign parent is deliberate at `yolo host apply` | it is a warning at exit 0, and declaring the dir as the pack's own state silences it |

## 15. Sequencing

In order, each a landable slice: the addressing amendment of [§4.2](#42-addressing-an-agent-that-is-not-here-deliver-nothing-by-default)
with its `yolo check` gate, since the pack-side change cannot land before it; the agy slot and its
parity test; `matt-agentwork` switches to the addressed form and the maintainer runs the
[§5.2](#52-the-one-check-that-settles-it-and-who-runs-it) check (branch B adds `as`); the per-path refusal and the boot stop; the overlay record and the
foreign-parent warning in lint, check and launch; the reclaim and the dangling-link housekeeping,
each behind its ruling. The overlay record, the reclaim and the housekeeping are account-mode
work with a horizon: they extend routing the macos-user model plan's slice 13 deletes
([§2.2](#22-how-each-backend-materializes-a-path-addressed-files-destination)), so what each is
worth depends on how long account mode keeps a shared home. The implementation plan is not seeded here; the planner writes it once the
questions below are ruled.

## Open Questions

Every leaning below is the design author's (Fable). The maintainer's own words appear only in
[OQ-FS6](#OQ-FS6) and the ledger.

1. 💬 **OQ-FS1: Does the plain agy slot deliver the agent, or does the slot need to name the landing?**

   Your pack ships one Antigravity agent definition. With a slot at `.gemini/config/agents`, yolo
   lands the pack's tree at `agents/matt-agentwork/…`. Antigravity's docs show agents as
   `agents/<name>/agent.md` and do not say whether the name comes from the directory or the
   file's `name:` line. One `/agents` panel over the
   [§5.2](#52-the-one-check-that-settles-it-and-who-runs-it) probe files answers it.

   - **A — The plain slot.** The pack ships `agent.md` with `name: design-research`. One agent
     per pack.
   - **B — `as` on the contribution.** The landing is `agents/design-research/`; yolo gains one
     field and one refusal for two packs choosing one `as`.
   - **C — The plain slot, several agents.** Only if the panel lists the nested probe.

   <!-- vantage: question id=OQ-FS1 leaning="A if the panel lists probe-by-frontmatter; B if it lists probe-pack — the documented layout is a directory per agent, and A costs nothing to try first." -->

   _Leaning:_ A if the panel lists `probe-by-frontmatter`; B if it lists `probe-pack`. The
   documented layout is a directory per agent, and A costs nothing to try first.

   **Answer:**

   > _(empty — fill in when decided)_

2. 💬 **OQ-FS2: When a pack puts files inside a folder some other pack keeps state in, should yolo warn only then, warn on any undeclared dot-folder, or refuse?**

   A launch selects `matt-agentwork` and not `agy`. It runs, and only Mac account mode makes a
   real `~/.gemini` in a shared home, which the ruled Mac model retires
   ([§6.1](#61-what-is-legitimate-here-and-why-it-is-not-an-error)). A `home/` folder file is the
   same shape. A pack shipping `~/.tmux/plugins`, a folder no pack claims, has no hazard.

   - **A — Warn only where a known pack keeps state there.** The agy case warns; the tmux case
     is silent.
   - **B — Warn on any dot-folder no selected pack declares.** The tmux pack warns forever.
   - **C — Refuse.** Every backend stops, two of them over nothing.

   <!-- vantage: question id=OQ-FS2 leaning="A — the hazard is a folder some pack keeps state in, and a warning on a correct config forever trains people to ignore warnings." -->

   _Leaning:_ A. The hazard is a folder some pack keeps state in, and a warning on a correct config
   forever trains people to ignore warnings.

   **Answer:**

   > _(empty — fill in when decided)_

3. 💬 **OQ-FS3: May the layout replace a directory yolo itself made for pack files, with nothing else in it?**

   A Mac launch without agy made `~/.gemini` for one file; a launch selecting agy needs a link
   there. The no-migration ruling never removes a real path; the overlay's record shows the user
   wrote nothing here. Account mode only
   ([§6.3](#63-on-macos-user-record-what-the-overlay-makes)); open
   [OQ-HT5](../reference/macos-user-home-tiers.md#oq-ht5) asks this of a stale home-root file.

   - **A — Reclaim it.** The launch replaces the directory with the link and prints one line
     saying what it replaced and why it was safe. Anything unrecorded inside stops it.
   - **B — Never.** The launch refuses, naming the path and its maker, with `sudo rm -rf` of it.

   <!-- vantage: question id=OQ-FS3 leaning="A — the ruling protects the user's data, and a directory yolo made whose every byte yolo delivered is not that; the content check keeps it from reaching anything else." -->

   _Leaning:_ A. The ruling protects the user's data, and a directory yolo made whose every byte
   yolo delivered is not that; the content check keeps it from reaching anything else.

   **Answer:**

   > _(empty — fill in when decided)_

4. 💬 **OQ-FS4: After the home layout fails on a Mac, should the bootstrap stop, or run on and report everything?**

   The lab report's refused launch kept generating agy's config into the real `~/.gemini` after
   the layout refused. Both boots run every step so one boot reports every problem
   ([A12](../reference/jail-home.md#why-its-this-way)); on macos-user, in either mode, the layout
   is the first step that writes, and every later one writes into what it lays
   ([§7.2](#72-stop-the-boot)).

   - **A — Stop.** The refusal is the layout's alone, with a last line saying nothing else ran;
     A12 gains a macos-user exception for this step.
   - **B — Run on, skipping only steps that write under the home.** More is reported; the skip
     list is a second thing to keep true.

   <!-- vantage: question id=OQ-FS4 leaning="A — the layout is the first step that writes, so stopping loses nothing already reported, and every later failure would be the first one wearing a different label." -->

   _Leaning:_ A. The layout is the first step that writes, so stopping loses nothing already reported, and
   every later failure would be the first one wearing a different label.

   **Answer:**

   > _(empty — fill in when decided)_

5. 💬 **OQ-FS5: Who removes a layout link whose workspace is gone?**

   Three account-home links pointed at deleted integration workspaces; one refused a real launch
   until `sudo rm`. A launch not declaring the link leaves it alone, by design. Account mode
   only: `self` mode lays none, and [OQ-MM4](macos-user-model.md#OQ-MM4) rules out
   cleaning the old account once launches move ([§8](#8-dangling-layout-links-from-dead-workspaces)).

   - **A — `yolo check` warns, `yolo prune --apply` removes the link.** Explicit, named, and in
     prune's dry-run shape; the target is never touched.
   - **B — The layout removes a dangling link it finds.** No step for the user, and a link to an
     unmounted volume is removed too.
   - **C — Leave it to the integration suite's cleanup alone.** Fixes the known maker and no
     other.

   <!-- vantage: question id=OQ-FS5 leaning="A, with the suite cleaning up its own links regardless — a dangling link is a judgment about a target yolo cannot see, which belongs in an explicit prune." -->

   _Leaning:_ A, with the suite cleaning up its own links regardless. A dangling link is a
   judgment about a target yolo cannot see, which belongs in an explicit prune.

   **Answer:**

   > _(empty — fill in when decided)_

6. 💬 **OQ-FS6: Should files addressed to an agent whose pack is not selected be skipped by default, instead of refusing the launch?**

   Your words: a pack must be able to "distribute configs for agents in case they're enabled",
   by default, not by an `if` key. Today `agents: ["agy"]` without agy refuses; yolo tells that
   from a typo by the packs you could select, a workspace's `off` one included ([§4.2](#42-addressing-an-agent-that-is-not-here-deliver-nothing-by-default)).

   - **A — Skip by default.** The launch delivers nothing for it; `yolo check` prints a `[SKIP]`
     line ending in `yolo pack add agy`. A name no pack anywhere declares still refuses.
   - **B — An opt-in key.** Only a contribution writing `if` is skipped; the rest refuse.
   - **C — Keep the refusal**; select `agy` everywhere.

   <!-- vantage: question id=OQ-FS6 leaning="A — addressing an agent already means 'for whoever has it', the typo case stays fatal through the wider vocabulary, and the key would be boilerplate every content pack writes." -->

   _Leaning:_ A. Addressing an agent already means "for whoever has it", the typo case stays
   fatal through the wider vocabulary, and the key would be boilerplate every content pack writes.

   **Answer:**

   > _(empty — fill in when decided)_

## Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| FS-D1 | The maintainer runs Argon through `agy` in every setting (host, jail, subprocess, direct) and every project, so the research child must be delivered wherever `agy` runs under yolo, on every notch and backend; a per-project `.agents/agents/` copy is rejected as the primary delivery and kept only as agy's own override. Their words: "I use argon in agy in all scenarios, host and jail subprocess and not. not sure how this could be per-project." | 2026-10-30 | [§4.1](#41-the-contribution) | — |
| FS-D2 | A path into another pack's state folder is an address, and the pack-side fix is `files` with `agents`; a nested delivery under a *selected* pack's state stays legal on every backend. Designer's decision. | 2026-10-30 | [§1](#1-the-verdict), [§4](#4-the-pack-side-change-address-the-agent) | — |
| FS-D3 | The agy `[[files]]` slot at `.gemini/config/agents` is a change to a pack yolo ships, so it is a yolo change, with `expects = ["agent.md"]`. Designer's decision. | 2026-10-30 | [§5.3](#53-the-slot-in-each-branch) | — |
| FS-D4 | `occupiedLayoutError` names each occupying account-home path with its own removal and its maker when known; the whole-account reset survives only when no path can be attributed and there are several. Designer's decision. | 2026-10-30 | [§7.1](#71-name-the-path) | — |
| FS-D5 | No new refusal of the foreign-parent shape on any backend; the same warning on all of them, only where a known pack keeps state in the folder, and the macos-user overlay records what it creates. Designer's decision, confirmed by [OQ-FS2](#OQ-FS2). | 2026-10-30 | [§6](#6-the-foreign-parent-shape-warn-where-a-pack-is-known-to-keep-state-and-nowhere-else), [§9](#9-parity-across-backends-and-notches) | — |

## Appendix: evidence, by symbol

Read 2026-10-30 at `e4345e69d`, and every symbol below re-read at `a078c5999` the same day.
Symbols rather than lines, so a move does not make this false.

- Added at the re-read: the macos-user mode is `macos_user_mode` (`ParseMode`, `LaunchHomes` in
  [`mode.go`](../../internal/macosuser/mode.go); default `account`), and `self` mode's layout is
  `DeriveDarwinWorkspaceHome`, chosen by `darwinHomeLayoutFor` when `HOME` is the sidecar; the
  boot order is the `bootSteps` table; a `home/` folder lowers to `files` contributions in
  `discoverHomeFiles`; today's audience refusal text is `agentAudienceMessage`.

- `files` slot and audience fields: `Contribution.Agent`, `Contribution.Agents`,
  `Contribution.Register`, `Contribution.Expects` in [`contributes.go`](../../internal/packdecl/contributes.go);
  `files` must name `into` or `agents`, never both (`validateContribution`); one slot per agent per
  pack and one addressed tree per agent per pack (`validateFilesDestinations`,
  `validateAddressedFiles`).
- Landing: `packload.SlotLanding` joins `<slot>/<pack>` for `files` only; `Registrations` and
  `ExpectsNotes` read it ([`registration.go`](../../internal/packload/registration.go)).
- Fatal unknown audience: `packload.AgentAudienceProblems`
  ([`agentaudience.go`](../../internal/packload/agentaudience.go)), called from `stagePacks` and
  `yolo host apply`, not from `yolo check`; skipped under a narrowed build-jail selection
  ([`seal.go`](../../internal/cli/run/seal.go), which names the `matt` pack's `agents: ["pi"]`
  briefing as the launch that forced the skip); reported unmatched destination:
  `reportUnmatchedAudiences`. The vocabulary it reads is `AgentNames` over what the caller loaded;
  its header names `config.UseProfileCLINames` as the neighbouring reader that unions
  `packload.Embedded()` without selection gating.
- The standing rule this design amends: [P3](../reference/agent-briefings.md#ba-p3), the
  [two-severities table](../reference/agent-briefings.md#two-severities-an-unknown-name-is-fatal-an-unmatched-destination-is-reported)
  and ledger row [`OQ-BA3`](../reference/agent-briefings.md#oq-ba3) in `agent-briefings.md`.
- Jail mounts: `packFilesTargets` resolves through `ResolveDestinations`; `preparePackFiles` makes
  mountpoints in the skeleton or the workspace state dir; `PackDestConflicts` and
  `PackFilesShadowedSurfaces` are the existing `files` pre-flights, run at the launch and in
  `yolo check`'s Packs section ([`check/packs.go`](../../internal/cli/check/packs.go)).
- Host render: `RenderHostFiles` writes `into` contributions per file; a slot (`from` empty) is
  skipped there.
- macos-user: `InstallDarwinHomeLayout` runs as the first bootstrap step; `ensureLayoutSymlink`
  repoints yolo's links and never removes a real path; `occupiedLayoutError` prints the groups;
  `overlayLinks.route` refuses a link the layout did not lay with the text the lab report quotes;
  `openOverlayParentIn` creates a missing parent beneath the home or the sidecar;
  `writeOverlayInstallRecord` records what was delivered.
- Boot continues after a failed generator: `genStepAbout` warns and records; the refusal record
  is `BootRefusal` in [`bootrefusal.go`](../../internal/entrypoint/bootrefusal.go).
- Selection-only reservation: `resolveSelectedPacks`
  ([`selectedpacks.go`](../../internal/config/selectedpacks.go)) and its header's account of
  [`OQ-BH14`](../reference/jail-home.md#oq-bh14).
- Agy's discovery layout: Google's [Agents command](https://antigravity.google/docs/cli/commands/agents)
  page, as quoted in [§5.1](#51-what-agy-reads-and-what-nobody-has-measured); not measured against a
  running `agy`.
- This jail: `/home/agent/.gemini` is root-owned and holds only `config/agents/design-research.md`;
  `agy` is a root-owned binary in `~/.local/bin`; no agy pack is selected.
