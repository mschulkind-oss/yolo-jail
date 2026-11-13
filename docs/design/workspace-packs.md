---
title: "May a repository's committed config name packs, and which ones?"
date: 2026-10-23
status: in-review
stage: DESIGN
next: "Rule OQ-WP1 (the subset) first; OQ-WP2 to OQ-WP5 shape what a collaborator sees and can refuse, and each holds under (b)"
tags: [design, packs, workspace, scope, trust, config, mcp]
summary: "OQ-PK1 was ruled on 2026-10-23: no pack may be named at workspace scope for now, mcp_presets is deleted, and the workspace subset gets its own design first. This is that design. It argues for the maintainer's instinct, option (b): a workspace config may name a pack yolo ships, by its bare name, when every contribution that pack makes stays inside the jail. The line is host reach, credential steering and cross-workspace state, judged per contribution kind, with every future kind refused until it states a verdict. The collaborator already approves every workspace config edit at the config-change gate, so the new grant arrives through an existing consent step; what is open is what that step shows, whether a user can veto one workspace pack, what a launch does with an entry it cannot admit, and whether yolo host honors any of it."
---

# May a repository's committed config name packs, and which ones?

**Status:** 2026-10-23. Nothing built; a workspace `packs` key refuses every launch today. Evidence
verified against the tree at `7424d681a`, citing symbols rather than line numbers.

> **In short.** A workspace config may select a pack yolo ships, by bare name, when every
> contribution in it stays inside the jail. Shipped content is yolo's own and a bare name fetches
> nothing, so a repository gets to *choose* among content yolo already vouches for, and nothing
> more. The collaborator approves that choice in the config-change prompt they already answer.

**Why it matters.** The motivating case is a colleague's repository committing
`"packs": ["chrome-devtools"]` so that every collaborator's agent gets the browser MCP server.
`mcp_presets` did that until [OQ-PK1](../reference/pack-system.md#oq-pk1)'s ruling deleted it, and
[OQ-MP7](../reference/mcp-configuration.md#OQ-MP7) said not to lose the case for good.

**The shape.** One verdict per contribution kind, stored beside each kind's footprint; one
selection that unions both scopes, with workspace packs ordered lowest; one existing consent step,
the config-change gate.

**Cost.** Every kind, present and future, must carry a workspace verdict. A repository that commits
`packs` refuses to launch for any collaborator still on an older yolo
([§10](#10-existing-state-and-rollout)).

**Start at [§3](#3-the-per-kind-verdict)**, the verdict table. The rest follows from it.

**Needs your ruling:** [OQ-WP1](#OQ-WP1), [OQ-WP2](#OQ-WP2), [OQ-WP3](#OQ-WP3), [OQ-WP4](#OQ-WP4),
[OQ-WP5](#OQ-WP5).

**Reads with:** [`pack-system.md`](../reference/pack-system.md#oq-pk1-background) (the background
to [OQ-PK1](../reference/pack-system.md#oq-pk1), whose options this doc keeps), [OQ-MP7's demotion](../reference/mcp-configuration.md#OQ-MP7)
(the case and [OQ-MP7](../reference/mcp-configuration.md#OQ-MP7)), [`config-safety.md`](../reference/config-safety.md) (the gate the consent
rides on), [`misguided-agent-principle.md`](../reference/misguided-agent-principle.md) (the threat
model).

---

## 1. The verdict

Build option (b) from [OQ-PK1](../reference/pack-system.md#oq-pk1)'s background: **shipped packs, by bare name, whose contributions all
stay inside the jail.** *WP* is the prefix for this doc's questions, coined here for *workspace
packs*.

Two terms, both coined here:

- **Workspace-selected pack** — a pack named in `packs` in a workspace config (`yolo-jail.jsonc` or
  `yolo-jail.local.jsonc`). Not a pack the user selects at user scope that happens to be used in
  this workspace, and not the per-workspace file in `~/.config/yolo-jail/workspaces/`, which is user
  scope ([the per-workspace file](../reference/storage-and-config.md#the-per-workspace-file)).
- **Workspace verdict** — the per-kind answer to "may a workspace-selected pack carry this
  contribution": *admitted* or *refused*, with a reason. Not a trust level and not an approval; it
  is a static fact about the kind, plus a per-instance check for the few kinds where the instance
  decides ([§3.2](#32-per-instance-checks)).

My reasoning, in order:

1. **Bare names leave the repository nothing to author.** Every byte a workspace-selected pack
   delivers is in the yolo build the collaborator installed. The repository's power is selection
   among content yolo ships, which is a much smaller power than content.
2. **The execution is not new.** A workspace config can already name any command in `mcp_servers`
   and any package in `packages`; no user-scope-only refusal in `internal/config` names either key.
   What (b) adds is the install a shipped pack carries, inside the jail.
3. **The line that matters is the one the codebase already draws**: host reach, credential steering
   and state that crosses into other workspaces. [OQ-MP7](../reference/mcp-configuration.md#OQ-MP7)
   said so: the rule *"was to stop a workspace controlling what runs on the host. An MCP server for
   an agent inside the jail is not that."*
4. **Consent already exists for this actor.** The workspace config is the file the config-change
   gate guards, and every edit to it is shown as a diff and asked about at the next fresh launch
   ([config-safety.md P2 and P4](../reference/config-safety.md#principles)).

The honest weakness is in [§9](#9-what-an-agent-editing-the-workspace-config-can-now-cause): (b)
admits agent packs too, so a repository can choose which agent CLIs install in your jail. Core does
not know what an agent is, so no kind-based rule can tell `codex` from `chrome-devtools`. The user
veto ([OQ-WP3](#OQ-WP3)) is the answer I propose to that.

## 2. What exists today

Verified at `7424d681a`.

| Fact | Where |
| :--- | :--- |
| `packs` is read from the user config only, so workspace scope is inexpressible | `LoadPacks` in [`packs.go`](../../internal/config/packs.go) |
| A workspace `packs` key is a validation error, and every launch refuses an invalid config ("Invalid jail config") | `validatePacks` in [`packs.go`](../../internal/config/packs.go); `loadAndValidateConfig` in [`preflight.go`](../../internal/cli/run/preflight.go) |
| `mcp_presets` is still read from the merged config; [OQ-PK1](../reference/pack-system.md#oq-pk1)'s ruling deletes it | [the retired `mcp_presets` key](../reference/mcp-configuration.md#the-retired-mcp_presets-key) |
| A workspace config may name any command in `mcp_servers` and any package in `packages` | `validate.go` has no workspace refusal for either |
| A repository's own skills reach every agent through the workspace layer, with a confined reader, not through a pack | [the workspace layer](../reference/agent-briefings.md#the-workspace-layer), [OQ-WS1](../reference/agent-briefings.md#oq-ws1) |
| A repository's own MCP files (`.mcp.json`, `.vscode/mcp.json`) reach the agents that read them | [`workspace-mcp-sources.md`](workspace-mcp-sources.md) |
| `profiles` and `use_profiles` are user scope only, for credential steering | [OQ-CS5](../reference/providers.md#oq-cs5); `userScopeOnlyMessage` in [`profiles.go`](../../internal/config/profiles.go) |
| Config merge union-merges every list, so a workspace can widen a list and never narrow one | [`loophole-system.md`](../reference/loophole-system.md#principles), the caution under R5 |
| Every workspace config edit is shown and asked about at the next fresh launch, an interactive attach and `yolo config apply`; no terminal and no `--accept-config-changes` refuses | [`config-safety.md`](../reference/config-safety.md#how-the-gate-decides) |
| `yolo host` does not read the workspace config, and the host floor holds only user-scope packs' programs | [`host-agent-environment.md`](../reference/host-agent-environment.md#what-the-floor-holds), [HE-D11](../reference/host-agent-environment.md#he-d11) |
| The selection closure adds packs a selected pack's `needs` names, and a service pack an active profile's `via` names | `SelectedPackDeclarations` in [`selectedpacks.go`](../../internal/config/selectedpacks.go) |
| One pack resolver serves every notch; a bare name loads from the build's embedded packs | `ResolvePack` and `EmbeddedPackEntry` in [`packresolve.go`](../../internal/config/packresolve.go) |

## 3. The per-kind verdict

Four axes. A kind is admitted only when it is clear on all four.

- **In-jail effect** — what it changes inside the jail. Allowed; this is the point.
- **Host reach** — the host reads, fetches, runs or writes something because of it.
- **Credential steering** — it decides where a credential goes, or which one is used.
- **Cross-workspace state** — it writes something another workspace's jail later reads.

### 3.1 The table

The closed kind set is the key set of `footprints` in [`kinds.go`](../../internal/packdecl/kinds.go).

| Kind | Verdict | Reason |
| :--- | :--- | :--- |
| `skills`, `briefing` | Admitted | Content in the jail's home. Shipped content is yolo's own |
| `files` | Admitted, per instance | A plain tree is content. A patched extension is refused ([§3.2](#32-per-instance-checks)) |
| `config`, `config-overlay`, `config-list` | Admitted, per instance | Writes jail-side config surfaces. Refused when it writes a credential-steering key ([§3.2](#32-per-instance-checks)) |
| `env` | Admitted, per instance | Literal strings in the jail. Same provider-key check |
| `models` | Admitted | Model entries are facts about a service the user already declared |
| `requires`, `blocked-tool` | Admitted | An assertion, and a refusing shim inside the jail |
| `program` | Admitted, per instance | Installs into the jail's prefix through its launcher. `via: "brew"` is refused |
| `mcp` | Admitted | An entry in the agent's config file; its command runs inside the jail |
| `autonomy` | Admitted | The pack's own postures; the notch picks one |
| `service`, `adapter` | Admitted | An in-jail daemon and an address. The service kind's own footprint says it crosses no boundary |
| `state` | Admitted at `workspace` scope only | `machine` scope is cross-workspace state by definition |
| `hook` | Admitted for `per_jail_history`, `unshare_directory` | `shared_credentials` and `shared_directory` write a machine-wide store every other workspace reads |
| `loophole` | Refused | A host daemon, intercepts, binds or devices: host reach on every instance |
| `intercept` | Refused | A forwarder into a loophole |
| `reads-host`, `mount` | Refused | A host file or directory crosses into the jail |
| `provider`, `profile` | Refused | Credential steering, already user scope ([OQ-CS5](../reference/providers.md#oq-cs5)) |

> [!NOTE]
> **The "in-jail" column means the jail and macos-user's sandbox, never `yolo host`.** At the host
> notch a `program` installs into the host floor and runs as you outside every sandbox, so every
> admitted row above becomes host reach there. That is [OQ-WP5](#OQ-WP5).

### 3.2 Per-instance checks

Five checks read the instance, not just the kind. Each refuses that contribution, and the refusal
names it and why.

1. **`state`**: the scope must be `workspace`, which is also what an unset `scope` means.
2. **`hook`**: the hook name must be one the table admits. A hook added to `KnownHooks` later is
   refused until it states a verdict ([§7](#7-new-kinds-default-to-refused)).
3. **`program`**: `via: "brew"` is refused, as
   [PT-D10](private-tap-programs.md#46-trust-and-disclosure) already rules for every workspace
   case: it makes the host run brew.
4. **`config`, `config-overlay`, `config-list`, `env`, `autonomy`**: a contribution that writes a
   pointer any selected pack lists under `provider_keys` is refused. `packs/claude` lists
   `/env/ANTHROPIC_BASE_URL` among others, and a base URL is exactly where a subscription bearer
   goes. No pack (b) admits today writes one; the check is what keeps that true.
5. **`files`**: a patched extension (a tree built from an upstream with a series replayed) is
   refused, because it puts a fetch and build the host drives into the launch. No shipped pack
   declares one.

### 3.3 Which shipped packs each option admits

Derived at `7424d681a` from the kinds each `packs/*/pack.json` declares. It drifts with the
manifests, so re-derive it before relying on it:

```console
$ yolo pack footprint <name>     # per pack, the kinds and claims it makes
```

| Option | Admits today |
| :--- | :--- |
| (a) content kinds only | `guardrails`, `muse-planner` |
| (b) everything that stays in the jail | (a)'s, plus `chrome-devtools`, `codex`, `copilot`, `omp`, `opencode`, `wire-bridge` |

Every other shipped pack declares at least one of `loophole`, `intercept`, `provider`, `profile`,
`machine`-scope `state`, or the `shared_credentials` hook. `claude` is refused under every option:
it declares a loophole (`claude-oauth-broker`), `machine`-scope state and `shared_credentials`.

> [!IMPORTANT]
> **This set is larger than [OQ-PK1](../reference/pack-system.md#oq-pk1)'s background says.** At `d4e435a3` the background listed
> `copilot`, `omp` and `wire-bridge` for (b). Since then `codex` and `opencode` lost their
> `profile` contributions (`e32bfe427`, 2026-10-12), `chrome-devtools` shipped (2026-10-05), and
> `muse-planner` shipped as a `files`-only pack (2026-10-12). So (b) now admits four agent packs.

## 4. How workspace packs join the selection

**One key, both scopes.** A workspace writes `packs`, the same key the user config uses. A second
key name would buy nothing: an older yolo refuses an unknown top-level key exactly as it refuses a
workspace `packs` today.

The rules:

1. **Bare-name strings only.** A workspace entry that is a `file://` or `git+` source, or the object
   form, is refused with a message naming the user config as where it can go. A source form reaches
   the host at selection (a fetch with the user's git credentials, or a read of a host directory). The
   object form's filters only narrow, but a second filtered entry for one pack is ambiguous, and the
   user config is where narrowing a pack already lives.
2. **Union, deduplicated by pack name.** A pack named in both scopes is selected once, from the
   user-scope entry, so a user's `only` and `exclude` filters win.
3. **Workspace packs sit lowest in pack order**: workspace entries, then user entries, then packs a
   `needs` pulled in, then the local pack (`~/.config/yolo-jail/local`). The order decides overlays
   (later wins) and skills priority, so the user's own packs always win a conflict with a pack the
   repository chose.
4. **The closure is judged too.** A pack a workspace-selected pack's `needs` pulls in must itself
   pass the verdict, or the workspace entry is refused, naming the pulled pack and its refused kind.
   A pack the user's own selection pulls in is judged by nothing new. No pack (b) admits declares a
   `needs` today.
5. **Both workspace files count**, `yolo-jail.jsonc` and `yolo-jail.local.jsonc`. Both are writable
   from inside the jail and both go through the config-change gate, so they get one rule.
6. **Degenerate inputs.** An empty list selects nothing, the same as an absent key. A duplicate name
   within the workspace scope is selected once, and `yolo check` warns. An entry the launch cannot
   admit, because its name is not shipped by this build or one of its contributions is refused, is
   [OQ-WP4](#OQ-WP4).
7. **The user can veto**, or not: [OQ-WP3](#OQ-WP3). Because lists union-merge, a veto cannot be
   an entry in the user's `packs` list; it has to be a key of its own.

## 5. What a collaborator sees

**At the first launch of a cloned repository**, the config-change gate shows the whole workspace
config as a diff against nothing, `packs` included, and asks y/N (P4 in
[`config-safety.md`](../reference/config-safety.md#principles)). **On every later change** to the
workspace's `packs`, the gate shows the diff again. With no terminal and no
`--accept-config-changes`, the launch refuses. Nothing here is new machinery.

**This is consent, and that does not contradict "disclosure, not consent".** That boundary, in
AGENTS.md and `packhostgrants.go`, is about *user-scope* packs: [OQ-TP9](trust-paths.md#decision-ledger)
deleted their approval gate because writing the user config already takes host user authority.
For a workspace-selected pack the actor changes, since a repository author or the in-jail agent wrote
the selection. That passes [Test 1](../reference/gate-placement-principle.md#test-1--the-authority-test-could-this-actor-already-do-it),
and the gate that already exists for this actor is the right one. No new prompt is proposed.

**What the gate shows is open** ([OQ-WP2](#OQ-WP2)). A diff line reading `"chrome-devtools"` says
nothing about the npm install, the MCP server every agent gets, or the headless-browser flags.

**On every launch**, whatever [OQ-WP2](#OQ-WP2) rules, the launch prints one line per workspace-selected pack,
naming it as chosen by the workspace config and the file it came from, such as
`pack chrome-devtools: selected by yolo-jail.jsonc (the workspace)`. A pack both scopes name is
reported once, as the user's, with `also named by the workspace`. Under the
[no-quiet-mode ruling](../reference/report-tiers.md#why-its-this-way) no flag hides that line.

## 6. Reservation, staging and attach

- **Name reservation follows the selection** ([OQ-BH14](../reference/jail-home.md#reserving-a-name-is-not-creating-a-directory)).
  A workspace-selected pack is selected, so its writable and shared dirs are reserved in that
  workspace. A user's `writable_home_dirs: [".codex"]` therefore passes in one workspace and is
  refused in a repository that selects `codex`, and the refusal names the pack *and* the workspace
  file that selected it. Validation's reader (`resolveSelectedPacks`) and the launch's staged set
  must both see the workspace entries, or the two disagree.
- **Staging is unchanged.** A bare name resolves from `packload.Embedded()` through `ResolvePack`,
  and `stagePacks` copies it into the launch's own tree like any selected pack. No fetch, and no
  host path is read.
- **No shared dirs.** A pack's shared dirs, the machine-wide tier, are its `machine`-scope `state`
  claims, which the verdict refuses. So a workspace-selected pack gets only per-workspace overlay
  dirs, and the launch makes no machine-store source for it.
- **Attach never re-stages.** A `packs` edit made while a jail runs takes effect at the next fresh
  launch, exactly as a user-scope `packs` edit does. An interactive attach runs the config-change
  gate, which shows the edit and says it waits for a restart. A non-interactive attach prints the
  existing `yolo config apply` and `yolo config drift` line. `packs` is not added to live apply.

## 7. New kinds default to refused

The verdict lives on the kind's `Footprint`, beside `MayBeReviewWorthy`, as a field whose zero value
is **refused**. So:

- **A kind added later is refused at workspace scope** until its author writes a verdict, with no
  test to remember.
- **A test fails when a kind's verdict is admitted without a reason string**, so an admission is
  always argued in the registry where the kind is defined.
- **The per-instance checks** ([§3.2](#32-per-instance-checks)) are named in the same entry, so a
  reader of the registry sees the whole rule.
- **`KnownHooks` gets the same shape**: a hook added later is refused at workspace scope until
  it says otherwise.
- **The in-jail tolerant decode is unaffected.** A kind this build does not know is skipped there
  and reported, as today; it can never be admitted, because only the host decides the verdict.

## 8. Notches

| Notch | Workspace-selected packs | Why |
| :--- | :--- | :--- |
| Podman and Apple Container jails | Honored | The container is the boundary every admitted kind stays inside |
| macos-user, both modes | Honored | The sandbox's `HOME` is per launch under the workspace's `.yolo/home`, and the deny-by-default profile applies |
| `yolo host --` | [OQ-WP5](#OQ-WP5) | The workspace config is not read there today, and the host floor takes only user-scope programs |
| `yolo host apply` | Never | It renders into the real home; a cloned repository never writes there ([OQ-CO8](../reference/config-ownership.md#oq-co8)'s condition) |

This is a deliberate difference between notches, not a gap. The maintainer's parity rule asks every
backend and notch for the same behavior from the same declaration, and the declaration here is a
*confined* one: the verdict is "stays inside the jail", and the host notch has no jail.

## 9. What an agent editing the workspace config can now cause

The threat model is [the misguided agent](../reference/misguided-agent-principle.md): an eager
helper that adds `"packs": ["chrome-devtools"]` because it wants a browser. That is the good case,
and the gate shows it to the human before it takes effect.

| It can, after the human's y | It cannot |
| :--- | :--- |
| Install a shipped program in the jail, an agent CLI included (`codex`, `copilot`, `opencode`, `omp`) | Make the host fetch, read a host file, or run anything on the host |
| Add an MCP server to every selected agent's config | Choose a provider, a profile or a base URL |
| Add yolo's briefing prose and skills for that pack | Write state another workspace reads |
| Add refusing shims (`guardrails`) and literal env vars | Put a binary on `yolo host`'s floor, or reach `yolo host apply` |
| Reserve a home dir name, so a user's `writable_home_dirs` entry is refused in this workspace | Select a pack the user vetoed, if [OQ-WP3](#OQ-WP3) gives the user a veto |

Three of those deserve a sentence each:

- **A workspace-selected agent pack can use the user's own login.** `codex` declares an `env`
  contribution served by `openai-auth-broker`. If the user selected `openai-auth` themselves, a
  repository that selects `codex` gets a codex that refreshes through the user's broker. The
  credential still goes only to yolo's broker, which the user installed; the repository chose the
  consumer, not the destination.
- **Readiness can now stop a launch the repository caused.** The boot installs every selected pack's
  programs and stops when one fails, offline included ([program
  readiness](../reference/program-readiness.md#OQ-JR1)). A workspace-selected program is a selected
  program, so an offline collaborator's launch can stop over a pack they did not choose. The
  refusal already names `YOLO_ALLOW_MISSING_PROGRAMS=1`; it should also name the workspace file
  that selected the pack.
- **Execution was already available.** Everything in the left column that runs code, a workspace
  could already run through `mcp_servers` or `packages`. The new grant is the install a shipped
  pack performs, and the agent CLIs it brings.

## 10. Existing state and rollout

- **Older yolo refuses the key.** A collaborator on a yolo from before this ships gets "Invalid jail
  config" and `packs: user-scope only — move it to ~/.config/yolo-jail/config.jsonc`, a next step
  that is wrong for them; the right one is to upgrade. Nothing can fix an installed binary, so the
  user guide says it, and the new refusals ([§11](#11-wording-this-amends)) name the version that
  admits workspace packs.
- **Repositories that committed `mcp_presets`.** [OQ-PK1](../reference/pack-system.md#oq-pk1)'s ruling deletes the key outright. Once
  this ships, the retirement message for a workspace `mcp_presets` names `"packs":
  ["chrome-devtools"]` in the same file as the replacement. Until then it names the user config.
- **Running jails** keep the tree they launched with; nothing changes until a fresh launch.
- **Approval records.** The canonical snapshot already includes `packs`, so no record format
  changes. A workspace that adds `packs` gets one ordinary diff prompt.

## 11. Wording this amends

| Text | Change |
| :--- | :--- |
| [`loophole-system.md`](../reference/loophole-system.md#principles) R5, *"Install is user-scope"* | Becomes a rule about host reach: a workspace may select a shipped pack whose every contribution stays in the jail; anything reaching the host stays user scope. [OQ-MP7](../reference/mcp-configuration.md#OQ-MP7) asked for exactly this |
| [The workspace layer's P3](../reference/agent-briefings.md#the-workspace-layer), *"no workspace config selects a pack"* | Becomes "the layer selects no pack": the skills layer still delivers only bytes in the tree, and workspace pack selection is this doc's rule |
| `validatePacks`' refusal in [`packs.go`](../../internal/config/packs.go), and `LoadPacks`' "workspace scope is inexpressible" | Replaced by the verdict, with a refusal per refused kind or source form naming the user config |
| [`pack-system.md`](../reference/pack-system.md#oq-pk1)'s user-scope rule | Points here |

## 12. Non-goals

- **No repository-carried pack.** Option (c) is not proposed. [Ruling
  2](../reference/pack-system.md#open-rulings-2)'s reason holds: *"A repository needs no
  distribution mechanism to reach files it already owns"*, and the workspace skills layer carries the
  case that wanted it most.
- **No fetched or local pack from a workspace**, under any option.
- **No producer for the compose engine's `workspace` layer.** A workspace-selected pack contributes
  at its pack's own layers, so [that slot](../reference/pack-system.md#config-surfaces-and-the-compose-engine)
  stays empty.
- **No new consent step.** The config-change gate is the one that exists for this actor.
- **No change to user-scope packs**, their disclosure, or [OQ-TP9](trust-paths.md#decision-ledger).

## 13. Alternatives considered

| Alternative | Verdict |
| :--- | :--- |
| **(a) content kinds only** | Rejected as the core subset: it holds back [OQ-MP7](../reference/mcp-configuration.md#OQ-MP7)'s own case, an `mcp` pack, to guard execution a workspace keeps through `mcp_servers`. It is the fallback if [OQ-WP1](#OQ-WP1) rules against executable kinds |
| **(c) (b) plus a pack the repository carries** | Rejected for now ([§12](#12-non-goals)) |
| **An enable switch separate from selection** | Rejected in [OQ-MP7](../reference/mcp-configuration.md#OQ-MP7): it is `packs` under a second name |
| **A per-pack allowlist the user must opt into before any workspace pack works** | Rejected: the motivating case is a collaborator who gets the browser without editing their own config. A veto, not an allowlist ([OQ-WP3](#OQ-WP3)) |
| **Agent packs excluded by name** | Rejected: core knows no agent, by rule. A kind-based proxy (say, `autonomy`) would exclude `chrome-devtools` too |

## 14. Risks

| Risk | Mitigation |
| :--- | :--- |
| A kind is admitted without anyone arguing it | Verdict's zero value is refused; an admission needs a reason string, pinned by a test ([§7](#7-new-kinds-default-to-refused)) |
| A shipped pack gains a refused kind and silently disappears from repositories that named it | [OQ-WP4](#OQ-WP4)'s answer makes it loud either way; `yolo check` reports it |
| A collaborator says y to a diff they do not understand | [OQ-WP2](#OQ-WP2), and the per-launch provenance line |
| Validation and the launch disagree on what is reserved | Both read the same union through one function ([§6](#6-reservation-staging-and-attach)) |
| A pack's config writes a credential-steering key | The `provider_keys` check ([§3.2](#32-per-instance-checks)) |
| The test pins the verdict function and not the call site | The test must fail when `validatePacks` or the launch stops calling it |

## 15. What done looks like

- A repository commits `"packs": ["chrome-devtools"]`. A collaborator's first launch shows it at
  the gate; after a y, every agent in that jail and on macos-user has the browser server, and the
  launch names the pack as the workspace's.
- The same repository committing `"packs": ["claude"]` gets a `yolo check` error naming the
  `loophole`, `machine` state and `shared_credentials` contributions and the user config as where
  `claude` can go; the launch does what [OQ-WP4](#OQ-WP4) rules.
- `"packs": ["file:///x"]` or `"packs": [{"source": "codex"}]` in a workspace config is refused,
  naming the user config.
- A kind added to `kinds.go` with no verdict is refused at workspace scope.
- `yolo host -- claude` in that repository does what [OQ-WP5](#OQ-WP5) rules, and says so.

## Open Questions

1. 💬 <a id="OQ-WP1"></a>**[OQ-WP1](#OQ-WP1): Which packs may a workspace config select?**

   [OQ-PK1](../reference/pack-system.md#oq-pk1)'s three options, kept. The verdict table is [§3](#3-the-per-kind-verdict); what each
   admits today is [§3.3](#33-which-shipped-packs-each-option-admits).

   - **(a) Shipped content packs only.** A collaborator gets `guardrails` from a repository, but not
     the browser: `chrome-devtools` carries a `program` and an `mcp` entry.
   - **(b) Shipped packs whose contributions all stay in the jail.** The browser works. So do
     `codex`, `copilot`, `opencode` and `omp`: a repository can choose your agent CLIs.
   - **(c) (b) plus a pack the repository carries.** Needs a confined reader, and reopens Ruling 2.

   <!-- vantage: oq id=OQ-WP1 leaning="(b). Bare names leave the repository nothing to author, the execution is available today through mcp_servers, and the gate already asks the collaborator. Pair it with a user veto (OQ-WP3) for the agent packs it also admits." -->

   _Leaning:_ **(b).** Bare names leave the repository nothing to author, the execution is
   available today through `mcp_servers`, and the gate already asks the collaborator. Pair it with
   a user veto ([OQ-WP3](#OQ-WP3)) for the agent packs it also admits.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 <a id="OQ-WP2"></a>**[OQ-WP2](#OQ-WP2): What does the config-change prompt show for a pack the workspace newly selects?**

   The y is only as informed as the prompt ([§5](#5-what-a-collaborator-sees)).

   - **(A) The diff alone.** Today's prompt. The user sees `"chrome-devtools"` and nothing about it.
   - **(B) A labeled block above the diff**, like the repository-scope block: each added pack with
     its footprint lines (programs it installs, MCP servers, postures, state). One y approves it all,
     and `--accept-config-changes` prints the block first.
   - **(C) A separate consent per pack**, recorded host-side. A second prompt for a decision the
     first already covers.

   <!-- vantage: oq id=OQ-WP2 leaning="(B): a labeled footprint block in the existing prompt, the shape the repository-scope block already uses, so the y says what it admits without adding a step." -->

   _Leaning:_ **(B):** a labeled footprint block in the existing prompt, the shape the
   repository-scope block already uses, so the y says what it admits without adding a step.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 <a id="OQ-WP3"></a>**[OQ-WP3](#OQ-WP3): Can a user turn off one pack a workspace selects?**

   Answering N at the gate refuses the whole launch, and lists union-merge, so a veto needs a key
   of its own ([§4](#4-how-workspace-packs-join-the-selection)).

   - **(A) No veto.** Say N and ask the repository to change. You cannot use a repository whose
     choice you dislike.
   - **(B) A user-scope `workspace_packs` key** in `host_floor`'s shape: `{"*": true, "codex": false}`.
     A vetoed pack is skipped, with a launch line naming the key. Machine-wide.
   - **(C) (B), plus a per-workspace switch** in the per-workspace file, written by a host command.
     Finer, and one more command.

   <!-- vantage: oq id=OQ-WP3 leaning="(B): one user-scope map, default all admitted, so a user who never wants a repository choosing their agent CLIs says so once. (C) can follow if one workspace needs it." -->

   _Leaning:_ **(B):** one user-scope map, default all admitted, so a user who never wants a
   repository choosing their agent CLIs says so once. (C) can follow if one workspace needs it.

   **Answer:**
   > _(empty — fill in when decided)_

4. 💬 <a id="OQ-WP4"></a>**[OQ-WP4](#OQ-WP4): What does a launch do with a workspace pack it cannot admit?**

   The name may be shipped only by a newer yolo, or the pack may carry a refused kind.

   - **(A) Refuse the launch**, naming the entry, the reason and the fix. Today's behavior for any
     invalid key. One collaborator's older yolo, or a repository's mistake, blocks everyone it reaches.
   - **(B) Skip the entry with a launch line** naming the reason and the next step (upgrade yolo, or
     select it at user scope). `yolo check` still reports it as an error, for the repository's author.

   <!-- vantage: oq id=OQ-WP4 leaning="(B): the repository's choice is a request to the collaborator's launch, not a condition on it; the skip is loud and names the next step, and yolo check keeps the error for the author." -->

   _Leaning:_ **(B):** the repository's choice is a request to the collaborator's launch, not a
   condition on it; the skip is loud and names the next step, and `yolo check` keeps the error for
   the author.

   **Answer:**
   > _(empty — fill in when decided)_

5. 💬 <a id="OQ-WP5"></a>**[OQ-WP5](#OQ-WP5): Does `yolo host --` honor workspace-selected packs?**

   At the host, "stays in the jail" means "runs as you, unsandboxed" ([§8](#8-notches)).

   - **(A) No, with a line** naming each workspace pack left out and the user-scope entry that would
     add it. Matches the standing rule that a workspace never puts a binary on the host floor.
   - **(B) Content kinds only** (skills, briefing, env, blocked tools); programs and MCP servers stay
     out. The browser still does not arrive.
   - **(C) Everything (b) admits.** A cloned repository installs programs you run outside every
     sandbox.

   <!-- vantage: oq id=OQ-WP5 leaning="(A): the host floor's rule (a workspace config must never add a binary to the host) and HE-D11's (a cloned repository does not choose what your tools do outside every sandbox) both stand, and the line names the one-line fix." -->

   _Leaning:_ **(A):** the host floor's rule (a workspace config must never add a binary to the
   host) and [HE-D11](../reference/host-agent-environment.md#he-d11)'s (a cloned repository does not
   choose what your tools do outside every sandbox) both stand, and the line names the one-line fix.

   **Answer:**
   > _(empty — fill in when decided)_
