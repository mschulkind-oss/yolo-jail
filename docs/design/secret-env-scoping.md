---
title: "A secret is declared in its own place in config, and reaches only the agents whose provider claims it"
date: 2026-11-09
status: accepted
stage: BUILT
next: "Graduate §3 into docs/reference/providers.md#the-credential-gate, then delete this doc and its plan"
tags: [credentials, env_sources, providers, credential-gate, security, parity, config]
summary: "Today one `[env]` list carries every variable, and a dotenv value is withheld from the wrong agents only while some selected pack's provider claims its name; deselect the pack and the same key reaches every process. This design splits the config into a public set, today's `[env]`, whose values reach every process as they do now, and a private set, `[env.secrets]`, whose values reach only the agents whose selected provider claims them. Each name is written once. A provider that finds its key in the public set refuses the launch and names the one command that moves it. One rule for zai, cerebras, kilo, openrouter, bedrock, bedrock-key and any provider a user declares; the same rule at the jail, macos-user and host notches."
vantage:
  status-chip: true
---

# A secret is declared in its own place in config, and reaches only the agents whose provider claims it

**Status:** 2026-11-09. Built, every ledger row; the Mac checks are in the [plan](secret-env-scoping-plan.md#mac-checks). Evidence verified at `d53316b04`. The direction is the owner's
(a Vantage comment of 2026-11-09, quoted in [§3.1](#31-the-owners-direction)); the mechanism choices and
every ledger row are the seat's, pending the owner's review. Breaking changes are permitted by the owner's
words of 2026-11-09 (*"I'm not afraid of a breaking change because I don't know that anybody else is using
this"*), and this change is in the trust-boundary class
[legacy-config-compat.md](legacy-config-compat.md#OQ-LC3) keeps outside its keep-working rule.

> **In short.** Config has two places for environment variables. The **public set** is today's `[env]`:
> plain, safe values that reach every process, named once, exactly as now. The **private set** is a new
> `[env.secrets]` table beside it, taking files and inline values the same way, whose values reach only
> the agents whose selected provider (or MCP server) claims the name, and otherwise nobody. A provider that finds its key
> in the public set refuses to start and prints the one command that moves it. No name is written twice.

**Why it matters.** With `packs = ["claude", "zai"]`, `ZAI_API_KEY` reaches claude on the zai profile and
nothing else. Remove `zai` from `packs` and leave the key in the dotenv: the key is written into the shared
`yolo-user-env.sh` and every shell, agent and MCP server in the jail holds it. The same value's reach flips
on an edit that mentions neither the key nor a credential, and nothing says so.

**The shape.** One new table, `[env.secrets]`, with the same `files` and `set` keys as `[env]`; one bit kept
per hydrated value (which set declared it); one gate rule with four outcomes (public and unclaimed → every
process; public and claimed by a provider → refused; private and claimed → the claiming agents; private and
unclaimed → nobody; one name in both sets → refused); two new verbs that move a name between the sets; no change to the delivery vehicles.

**Cost.** Every provider key a user keeps in `[env]` today is refused once, on the first launch that selects
its provider, with the command that moves it. A user who keeps keys and plain variables in one dotenv file
splits that file, one command per key.

**Start at [§3](#3-the-rule)** — the two sets and the four outcomes. Everything else applies it.

**Ruled 2026-11-09:** [OQ-SE1](#OQ-SE1) (an unclaimed secret starts the launch and prints a line),
[OQ-SE2](#OQ-SE2) (an MCP server's `requires_env` claims too), [OQ-SE3](#OQ-SE3) (a claimed name never
reaches every process from config), and a name in both sets is a fatal error ([SE-D2](#SE-D2)). No question
is open.

**Reads with:** [`providers.md`](../reference/providers.md#the-credential-gate) (the credential gate as
built, whose rule this amends), [`credential-sources-separation.md`](credential-sources-separation.md)
(the `--with-credentials` grant this keeps, and [OQ-ES6](credential-sources-separation.md#OQ-ES6), which
[OQ-SE3](#OQ-SE3) subsumes), [`bedrock-key-provider.md`](bedrock-key-provider.md) (whose
[OQ-BK3](bedrock-key-provider.md#OQ-BK3) this design answers as "the same as every provider"),
[`agent-credentials.md`](../reference/agent-credentials.md#env_sources--the-sanctioned-secret-channel)
(the channel and its vehicles), [`config-commands.md`](config-commands.md) (the verbs the remedies name).
No companion plan is seeded: the owner asked for the design alone.

---

## 1. Words this doc uses

- **The credential gate** — the one function that decides which composed environment values reach which
  process at a launch; built, named in [`providers.md`](../reference/providers.md#the-credential-gate), and
  located in [`credentialscope.go`](../../internal/packload/credentialscope.go) (`ScopeCredentials`). Not a
  file writer: where an answer lands is a vehicle's business.
- **A claim** — a provider's declaration that a variable name carries its credential, its
  `api_key_env_name` ([`packs/zai/pack.toml`](../../packs/zai/pack.toml) claims `ZAI_API_KEY`), widened to
  same-platform siblings ([PP-D9](providers-and-profiles-redesign.md#PP-D9)). From
  [`providers.md`](../reference/providers.md#the-credential-gate). Not a value: a pack never carries one.
  An MCP server's `requires_env` entry is a claim too, for the agents whose config names that server
  ([OQ-SE2](#OQ-SE2)), but only a provider's claim makes a public value a refusal ([SE-D15](#SE-D15)).
- **A selected provider** — a provider in the launch's composed provider table: one a selected pack
  declares, or one the user declares under `providers`. The gate reads claims off that table and nothing
  else (`credentialClaims` in [`credentialscope.go`](../../internal/packload/credentialscope.go)).
- **The public set** *(the owner's words, 2026-11-09)* — every value the `[env]` table declares, in either
  scope, from a file or inline: today's whole channel. A value here reaches every process.
- **The private set** *(the owner's words)* — every value the new `[env.secrets]` table declares, in either
  scope, from a file or inline. A value here reaches only the agents whose selected provider, or whose
  MCP server, claims its name. "Secret" in this doc means "in the private set"; the doc never judges whether a value deserves to be.
- **A delivery vehicle** — the code that writes a launch's environment for one notch or backend: the
  container's shared `yolo-user-env.sh` plus per-agent `agent-env/<agent>.sh` files, macos-user's per-session
  file, the host notch's one exec'd process. From [`providers.md`](../reference/providers.md#the-credential-gate).
- **Reach** *(coined here)* — the set of processes a value's environment delivery includes: one agent's,
  several agents', every process, or none. Not file permissions: every file a vehicle writes is readable by
  every process of the jail's uid ([§8](#8-what-this-does-not-license)).
- **The notches** — the jail (podman, Apple Container), macos-user, and `yolo host --`, the three places yolo
  composes an environment; the term is [`notch-convergence.md`](../plans/notch-convergence.md)'s. The owner's
  parity rule is that each gives the same answer to the same config.

## 2. What exists today, precisely

Verified 2026-11-09 against `d53316b04`. Functions are named rather than lines, so the table stays true
across edits.

| Fact | Where |
| :--- | :--- |
| The gate reads the claims off the **composed** provider table — the selected packs' providers plus the user's `providers` entries — and nothing else | `credentialClaims`, [`credentialscope.go`](../../internal/packload/credentialscope.go) |
| A hydrated value whose name no composed provider claims goes to the **shared** set, which every process receives | the `sharedEnvSources` loop in `ScopeCredentials`, [`credentialscope.go`](../../internal/packload/credentialscope.go); ruled as [CN-D3](../reference/providers.md#CN-D3) |
| A claimed value goes only to agents whose active set resolves to a claimant; with no such agent it reaches nobody and the launch says so | `delivers`, `DisclosureWith`, [`credentialscope.go`](../../internal/packload/credentialscope.go) |
| The shared set is written to `yolo-user-env.sh`, sourced by every shell and the entrypoint; per-agent values to `agent-env/<agent>.sh` | `writeUserEnvFile`, [`userenv.go`](../../internal/cli/run/userenv.go); [`providers.md`](../reference/providers.md#the-credential-gate) |
| macos-user's session file and the host notch's exec read the same gate; the host composes from user scope only | [`envfile.go`](../../internal/macosuser/envfile.go); `composeHostVars`, [`host.go`](../../internal/cli/host.go); [CN-D5](../reference/providers.md#CN-D5) |
| The disclosure lists only **claimed** names, grouped by claimant and recipients, never values; an unclaimed name is never mentioned | `DisclosureWith`, [`credentialscope.go`](../../internal/packload/credentialscope.go); [CN-D16](../reference/providers.md#CN-D16) |
| The `[env]` table has exactly `sources`, `files`, `set` and `unset`, and the readable schema types each of them; a key the schema does not type is refused by the generic verbs | `env.*` rows, [`schema.go`](../../internal/config/schema.go); `parseConfigEditValue`, [`configeditverbs.go`](../../internal/cli/configeditverbs.go) |
| The hydrated map carries **no provenance**: which scope or table produced a value is gone after the merge, and the one check that needs it re-reads the workspace config | `validateEnvSourceScope`, [`envsourcescope.go`](../../internal/config/envsourcescope.go); `ResolveEnvSourcesFull`, [`envsources.go`](../../internal/config/envsources.go) |
| A workspace-scope dotenv entry that resolves outside the workspace is refused, and the refusal spells the same entry for the user config with its `yolo config add env.files … --user` verb, or the legacy `env_sources` form for a JSONC user file | `envSourceScopeRefusal`, [`envsourcescope.go`](../../internal/config/envsourcescope.go); the owner's ruling on [OQ-AS3](../research/agent-safehouse.md#OQ-AS3), 2026-10-29 |
| `yolo config set\|add\|unset <key> [value]` takes one key and one value, writes a key both scopes accept to the project config unless `--user`, `--project`, `--local` or `--file` says otherwise, and adds a missing key with its parent tables | `runConfigEditWithMode`, `addMissingKey`, [`configeditverbs.go`](../../internal/cli/configeditverbs.go); [config-commands.md §4](config-commands.md#4-the-commands) |
| `yolo check` runs the same gate to predict a launch, with no derives | [`envoverrides.go`](../../internal/cli/check/envoverrides.go) |
| The shipped packs a launch did **not** select are already read at one notch, to word what it cannot offer | `unselectedEmbedded`, [`protocolresolution.go`](../../internal/packload/protocolresolution.go) |
| The credential pre-flight asks only about **selected** providers | [`providerpreflight.go`](../../internal/cli/run/providerpreflight.go) |

The hole, stated as the gate sees it: **"claimed" is computed from the selection, so "secret" is too.**
The [OQ-PR2](provider-reachability-and-model-names.md#9-decision-ledger) amendment in
[`provider-reachability-and-model-names.md`](provider-reachability-and-model-names.md#9-decision-ledger)
records the consequence — a claim is launch-wide and withholds a name from every other process while its
pack is selected, and the moment the pack is not, the name is nobody's and goes everywhere.

### 2.1 The measured flip

The owner's case, in the gate's terms. One user config, one dotenv holding `ZAI_API_KEY` under `[env] files`.

| `packs` | `ZAI_API_KEY` claimed? | Reach | Written to | Disclosed? |
| :--- | :--- | :--- | :--- | :--- |
| `["claude", "zai"]`, claude on zai | yes | claude only | `agent-env/claude.sh` | `… (provider zai): claude only` |
| `["claude", "zai"]`, claude on native | yes | nobody | nowhere | `… withheld from every process` |
| `["claude"]` | **no** | **every process** | **`yolo-user-env.sh`** | **nothing** |

The third row is reached by removing one word from `packs`. The same table holds for every shipped
provider key and for any user-declared provider the user later deletes from `providers`.

## 3. The rule

### 3.1 The owner's direction

The owner, on 2026-11-09, speech-to-text, read for meaning:

> I'm wondering if instead we can force secrets to be in a different declared spot in our config. So
> basically we keep the same thing right now for general safe environment variables, and then if you want
> to have a secret, if you put them in there right now and you try to load it with a pack that knows about
> it, it's going to tell you an error that it's in the public set and then you need to move it to the private
> set for this to work. Then you don't have to mention them in two places, and the non-secrets are easy and
> safe. … We should allow for files and inline, just like the other set, both sets. They're just qualified
> with a namespace or whatever in TOML.

### 3.2 Principles

- **P1. Secrecy is declared, in one place.** A value is a secret because the user wrote it under
  `[env.secrets]`, and for no other reason. No name is written in two places, and no pack's presence or
  absence changes what a value is.
- **P2. Nothing leaks** ([OQ-BR4](../reference/providers.md#oq-br4), ruled 2026-09-25). A value's reach
  may narrow or stay when the user edits something unrelated to it; it never widens.
- **P3. A provider's key may not be public.** A selected provider that finds its claimed name in the public
  set stops the launch and says where the name belongs. This is the owner's "it's going to tell you an
  error".
- **P4. Every stop names its one command** ([the happy-path
  principle](../reference/happy-path-principle.md#the-principle)). A line that only reports is a defect,
  and advice names a `yolo` verb, never a file to hand-edit.
- **P5. One gate, three notches, one answer.** The rule lives in the gate's one function, so the jail,
  macos-user and `yolo host` compose the same reach for the same config.
- **P6. No value judgment on files against inline.** The owner declined to make one. Both sets take
  both, and nothing is printed about which a secret came from.

### 3.3 The two sets

```toml
# ~/.config/yolo-jail/config.toml
packs = ["claude", "zai"]

[env]                                             # the public set: every process, as today
files = ["~/.config/yolo-jail/defaults.env"]
set   = { DEBUG = "1" }
unset = ["HTTP_PROXY"]

[env.secrets]                                     # the private set: claimed names only
files = ["~/.config/yolo-jail/secrets.env"]
set   = { OPENROUTER_API_KEY = "sk-or-…" }
```

| Key | Public set `[env]` | Private set `[env.secrets]` |
| :--- | :--- | :--- |
| `files` | a list of dotenv paths, as today | the same |
| `set` | an inline table of values, as today | the same |
| `sources` | the ordered mixed list, kept for the legacy lowering | **absent**: the private set has no legacy spelling to lower ([SE-D3](#SE-D3)) |
| `unset` | a list of names to remove, as today | **absent**: a removal carries no value, so it has no set; `[env] unset` removes a name from both ([SE-D4](#SE-D4)) |
| scope | user and workspace | user and workspace; a workspace file must lie inside the workspace, as today ([SE-D5](#SE-D5)) |
| order | later wins per name: user then workspace; within a file, `sources`, `files`, then `set` | the same, within the private set |

Both sets take both forms, and the launch never says which form a secret came from ([P6](#32-principles)).
A legacy JSONC config has no private-set spelling: a legacy user file is read as it is today, every
`env_sources` value lands in the public set, and the first refusal under [§3.4](#34-the-four-outcomes)
names `yolo config migrate` before the move ([SE-D6](#SE-D6)).

Hydration keeps one fact per value that the merge drops today: **which set declared it** ([SE-D1](#SE-D1)).
A name declared in both sets is the private set's: the public copy is not delivered anywhere, and the launch
says so and names the verb that removes it ([SE-D2](#SE-D2)); a public copy that reached every process
would undo the declaration.

### 3.4 The four outcomes

For every hydrated value, by its set and by whether something in this launch claims its name (a selected
provider, or an MCP server an agent's config names):

| # | Set | Claimed in this launch? | Reach | What is printed |
| :--- | :--- | :--- | :--- | :--- |
| 1 | public | no | every process, as today | nothing, as today — unless a shipped pack this launch did not select claims the name, when one line says so and names the move ([§3.6](#36-the-lines), [SE-D8](#SE-D8)) |
| 2 | public | **yes, by a provider** | **the launch is refused** before anything is written | the refusal of [§3.5](#35-the-refusal) |
| 3 | private | yes | the agents whose active set resolves to a claimant; no other process | today's lines: `ZAI_API_KEY (provider zai): claude only`, or `… withheld from every process — no agent in this launch selected it` |
| 4 | private | no | **nobody**; written to no file | `CEREBRAS_API_KEY: withheld from every process — nothing in this launch claims it.` then the remedy of [§3.6](#36-the-lines) |

Row 1 is today's [CN-D3](../reference/providers.md#CN-D3), now true only of the set the user called public;
a public name only an MCP server claims stays in row 1 ([SE-D15](#SE-D15)). Row 3 is today's gate, with the
MCP claim added. Rows 2 and 4 are the design, and so is one more refusal: a name set in both sets
([§4.8](#48-the-same-name-in-both-sets), [SE-D2](#SE-D2)). A removal (`[env] unset`) is unchanged and
names nothing: it carries no value, so it has no reach.

The refusal of row 2 fires at every entry that runs the gate — a fresh launch, an attach, `yolo check`,
`yolo host --` and `yolo host env` ([SE-D7](#SE-D7)) — and at every notch. Row 4 is not a refusal: the launch
runs and prints the line on every launch ([OQ-SE1](#OQ-SE1)). A claimed name never reaches every process from
config; the per-launch `--with-credentials` grant is the one way to widen it ([OQ-SE3](#OQ-SE3)).

> [!WARNING]
> The intuitive fix — widen the claims to every pack yolo ships, selected or not — keeps the gate guessing
> secrecy from a name list. A key for a provider yolo has not heard of still goes everywhere, and a release
> that ships a new pack changes who receives a value in launches that never selected it
> ([§6](#6-alternatives-considered), A). The shipped set is read only to **word** a line, never to decide a
> reach ([SE-D8](#SE-D8)).

### 3.5 The refusal

Row 2's text, at a launch, an attach or `yolo check`, for a key in a user-scope dotenv:

```text
config.env.files: ZAI_API_KEY is set in your public environment ([env], from
~/.config/yolo-jail/defaults.env), and the zai provider this launch selects claims it as its key. A
public value reaches every process in the jail, so a provider's key may not live there. Move it to the
private set: yolo config secret ZAI_API_KEY --user
```

- The first clause names the set and the file or table that set the name, in the scope that set it, so a
  user with several files knows which one to open if they prefer to.
- The verb is **`yolo config secret <NAME>`** *(proposed here, not built; [SE-D9](#SE-D9))*: it finds the
  public entry that sets `NAME` — an inline `set` value or a line in a listed dotenv file — and moves it
  into the private set of the same scope, an inline value to `[env.secrets] set` and a dotenv line to the
  first file `[env.secrets] files` lists. When that scope lists no private file yet, the verb refuses and
  names the one to run first: `yolo config add env.secrets.files '"~/.config/yolo-jail/secrets.env"'
  --user`. Its inverse, **`yolo config unsecret <NAME>`**, moves a name the other way. Neither prints a
  value. The scope flags are the generic verbs' own: `--user`, `--project`, `--local`, `--file`.
- A workspace-scope entry prints the same text with `--project` (or `--local` for the local sibling), and a
  dotenv it moves must stay inside the workspace, as every workspace file must.
- On a legacy JSONC user config the text names `yolo config migrate` first, then the verb, as the
  workspace-scope refusal already does for its own verb.
- In a jail the user-scope verb cannot write; the text carries the `host-run` line, as every user-scope edit
  from a jail does ([config-commands.md §3](config-commands.md#3-what-a-command-does-to-the-file)).

Nothing is written before the refusal: not the shared file, not a per-agent file. A running jail an attach
is refused into keeps the environment its processes already hold until it restarts.

### 3.6 The lines

Two line kinds join the disclosure, both names-only like every existing line
([CN-D16](../reference/providers.md#CN-D16)):

1. **Row 4, a private name nothing in this launch claims.** The remedy is chosen in this order, the first
   that applies printed:
   - a **shipped pack this launch did not select** declares a provider claiming the name: `To use it as
     cerebras's key: yolo pack add cerebras.`
   - otherwise: `If no provider or MCP server needs it, it belongs in the public set: yolo config unsecret
     FOO_TOKEN --user` (or `--project`, by the scope that set it).
2. **Row 1, a public name a shipped, unselected pack's provider claims.** One line, not a refusal:
   `ZAI_API_KEY reaches every process from your public environment; the zai provider, which this launch
   does not select, claims that name. If it is zai's key: yolo config secret ZAI_API_KEY --user`. A release
   that ships a new pack can therefore add a line to a working launch, never refuse it ([SE-D8](#SE-D8)).

At the host notch the same lines print, with the host's existing additions
([ES-D2, ES-D23](credential-sources-separation.md#10-decision-ledger)): a name the invoking shell also
exports is "not added by yolo", and a claimed name's remedy names `--with-credentials`.

### 3.7 What each notch writes

Nothing about the vehicles changes; only what the gate hands them does.

| Notch | Shared set (row 1) | Per-agent (row 3) | Row 4 |
| :--- | :--- | :--- | :--- |
| podman, Apple Container | `yolo-user-env.sh`, truncated and rewritten on every entry, so a value that moves to the private set leaves it on the next attach | `agent-env/<agent>.sh` | written nowhere |
| macos-user | the per-session file, plus the launched program's own | each other agent's env file | written nowhere |
| `yolo host --` | the one exec'd process | the same process, when the command is an agent on a claiming profile | not added; the shell's own export, if any, passes through |

`yolo check` predicts the same four outcomes from the same gate and prints the same refusal and lines, so a
user sees them before launching.

## 4. Worked examples: every case, as written

Each example shows the files as you would write them, what you select, who receives each name, and
the text the launch prints. Values are placeholders; the launch never prints one. Three spellings here are
proposed, not built: the `[env.secrets]` table (its `files` and `set` keys follow `[env]`'s, which the
readable schema types today, and the generic verbs reach a new key once the schema names it), and the
verbs `yolo config secret <NAME>` and `yolo config unsecret <NAME>`. `yolo config add env.secrets.files
'"<path>"' --user` follows the one-key-one-value shape the generic verb has today. `yolo host -- <cmd>` and macos-user give
the same answers unless an example says otherwise ([§4.22](#422-the-same-cases-at-yolo-host),
[§4.23](#423-the-same-cases-on-macos-user)).

The common files, used unless an example changes them:

```toml
# ~/.config/yolo-jail/config.toml
packs = ["claude", "pi", "zai"]

[env]
files = ["~/.config/yolo-jail/defaults.env"]

[env.secrets]
files = ["~/.config/yolo-jail/secrets.env"]
```

```bash
# ~/.config/yolo-jail/defaults.env     (public)
GH_TOKEN=ghp_…
DEBUG=1
```

```bash
# ~/.config/yolo-jail/secrets.env      (private)
ZAI_API_KEY=zai-…
```

### 4.1 A provider key in the private set, its pack selected, the agent on it

You run `yolo -p claude=zai` (or set `yolo active-provider claude zai` once).

| Name | Reaches | Written to |
| :--- | :--- | :--- |
| `ZAI_API_KEY` | claude only | `agent-env/claude.sh` |
| `GH_TOKEN`, `DEBUG` | every process | `yolo-user-env.sh` |

```text
  ZAI_API_KEY (provider zai): claude only
```

Unchanged from today for all three names. This is row 3 of [§3.4](#34-the-four-outcomes) for the key and
row 1 for the other two.

### 4.2 The same key, the agent on another provider

You run `yolo` with claude on its native login and pi on nothing.

| Name | Reaches | Written to |
| :--- | :--- | :--- |
| `ZAI_API_KEY` | nobody | nowhere |

```text
  ZAI_API_KEY (provider zai): withheld from every process — no agent in this launch selected it
```

Unchanged from today: the pack is selected, so zai claims the name and nobody selected zai.

### 4.3 The pack deselected: the hole this design closes

`packs = ["claude", "pi"]`; `secrets.env` still holds `ZAI_API_KEY`.

| Name | Reaches today (key under `[env]`) | Reaches under this design (key under `[env.secrets]`) | Written to |
| :--- | :--- | :--- | :--- |
| `ZAI_API_KEY` | **every process**, no line | nobody | nowhere |

```text
  ZAI_API_KEY: withheld from every process — nothing in this launch claims it. To use it as zai's
    key: yolo pack add zai.
```

The remedy names the pack because a shipped pack yolo did not select declares a provider claiming the name
([§3.6](#36-the-lines)). The value's reach did not change with the edit to `packs`: it narrowed from one
agent to nobody, and the line says why. The launch runs ([OQ-SE1](#OQ-SE1)).

### 4.4 A provider key left in the public set, its pack selected: the refusal

`defaults.env` also holds `ZAI_API_KEY=zai-…` (the user never made a private file), and `packs` still names
`zai`. Whatever claude and pi select, the launch stops before writing anything:

```text
config.env.files: ZAI_API_KEY is set in your public environment ([env], from
~/.config/yolo-jail/defaults.env), and the zai provider this launch selects claims it as its key. A
public value reaches every process in the jail, so a provider's key may not live there. Move it to the
private set: yolo config secret ZAI_API_KEY --user
```

After `yolo config secret ZAI_API_KEY --user`, the line is gone from `defaults.env` and present in
`secrets.env`, and the next launch is [§4.1](#41-a-provider-key-in-the-private-set-its-pack-selected-the-agent-on-it)
or [§4.2](#42-the-same-key-the-agent-on-another-provider). With no `[env.secrets] files` yet, the verb
refuses first: `no private file to move ZAI_API_KEY into; run yolo config add env.secrets.files
'"~/.config/yolo-jail/secrets.env"' --user, then this command again`. This is row 2 of
[§3.4](#34-the-four-outcomes), and `yolo check` reports the same text.

### 4.5 The same key public, its pack not selected

`packs = ["claude", "pi"]`; `defaults.env` holds `ZAI_API_KEY`; nothing is under `[env.secrets]`.

| Name | Reaches | Written to |
| :--- | :--- | :--- |
| `ZAI_API_KEY` | every process | `yolo-user-env.sh` |

```text
  ZAI_API_KEY reaches every process from your public environment; the zai provider, which this launch
    does not select, claims that name. If it is zai's key: yolo config secret ZAI_API_KEY --user
```

Row 1: nothing selected claims it, so the public declaration stands and the value goes where the user put
it. The line is the shipped set's one job, wording ([SE-D8](#SE-D8)); a release that adds a pack claiming
`FOO_TOKEN` adds this line to a launch holding `FOO_TOKEN` publicly, and changes nothing else.

### 4.6 Plain variables in the public set

The common files, any selection. No provider claims `GH_TOKEN` or `DEBUG`.

| Name | Reaches | Line |
| :--- | :--- | :--- |
| `GH_TOKEN`, `DEBUG` | every process | none |

Unchanged from today, named once, in the place they are today. This is the owner's "the non-secrets are
easy and safe".

### 4.7 A plain variable put in the private set

`secrets.env` also holds `GH_TOKEN=ghp_…`, and `defaults.env` does not. No provider claims it.

| Name | Reaches | Written to |
| :--- | :--- | :--- |
| `GH_TOKEN` | nobody | nowhere |

```text
  GH_TOKEN: withheld from every process — nothing in this launch claims it. If no provider or MCP server
    needs it, it belongs in the public set: yolo config unsecret GH_TOKEN --user
```

The launch starts, and the jail's `gh` fails later with its own message; the line repeats on every
launch until the name is moved ([OQ-SE1](#OQ-SE1)). `gh` is not an MCP server, so nothing can claim the
token: a value every shell should hold is public.

### 4.8 The same name in both sets

`defaults.env` holds `ZAI_API_KEY=old-…` and `secrets.env` holds `ZAI_API_KEY=zai-…`; `packs` names `zai`;
claude is on zai.

| Name | Reaches | Written to |
| :--- | :--- | :--- |
| `ZAI_API_KEY` | **the launch is refused** before anything is written | nowhere |

```text
config.env: ZAI_API_KEY is set in both your public environment ([env], from
~/.config/yolo-jail/defaults.env) and your private one ([env.secrets], from
~/.config/yolo-jail/secrets.env). A name lives in one set. Keep the private copy:
yolo config secret ZAI_API_KEY --drop --user
or keep the public copy: yolo config unsecret ZAI_API_KEY --drop --user
```

The refusal is fatal whatever the name, whether or not anything claims it, and whatever order the entries
merge in, at every entry that runs the gate ([SE-D2](#SE-D2), [SE-D7](#SE-D7)). Each copy is named by the
set and the file or table that set it, in the scope that set it, so a copy in a repository's committed
config names `--project`. The `--drop` form removes the other set's copy instead of moving one *(proposed;
[SE-D9](#SE-D9))*.

### 4.9 A secret written inline

```toml
[env.secrets]
files = ["~/.config/yolo-jail/secrets.env"]
set   = { OPENROUTER_API_KEY = "sk-or-…" }
```

`packs = ["claude", "pi", "zai", "openrouter"]`; pi is on openrouter.

| Name | Reaches | Written to |
| :--- | :--- | :--- |
| `OPENROUTER_API_KEY` | pi only | `agent-env/pi.sh` |

```text
  OPENROUTER_API_KEY (provider openrouter): pi only
```

The same reach as the file form, and no line about the form ([P6](#32-principles)). The config file that
holds the value is the user's to protect, as it is today for an inline `[env] set`.

### 4.10 A plain value written inline

```toml
[env]
files = ["~/.config/yolo-jail/defaults.env"]
set   = { EDITOR_THEME = "dark" }
```

| Name | Reaches | Line |
| :--- | :--- | :--- |
| `EDITOR_THEME` | every process | none |

Unchanged from today. If you write `ZAI_API_KEY = "…"` here while `zai` is selected, the refusal of
[§4.4](#44-a-provider-key-left-in-the-public-set-its-pack-selected-the-refusal) prints with `([env] set)`
as the place, and `yolo config secret ZAI_API_KEY --user` moves the inline value to `[env.secrets] set`.

### 4.11 A workspace-local config with a private file

```toml
# <workspace>/yolo-jail.local.toml (yours, not committed)
[env.secrets]
files = ["dev-secrets.env"]
```

`dev-secrets.env` sits in the workspace and holds `ZAI_API_KEY`; the user config has no private set.

| Name | Reaches | Written to |
| :--- | :--- | :--- |
| `ZAI_API_KEY` | claude only (claude on zai) | `agent-env/claude.sh` |

The same rule in the other scope ([SE-D5](#SE-D5)); the line is
[§4.1](#41-a-provider-key-in-the-private-set-its-pack-selected-the-agent-on-it)'s. If the entry instead
read `files = ["~/.config/yolo-jail/secrets.env"]`, the launch is refused as it is today for `[env] files`:
a workspace config may not list a file outside the workspace, and the refusal spells the entry for your
user config, `[env.secrets] files = ["~/.config/yolo-jail/secrets.env"] (yolo config add env.secrets.files
'"~/.config/yolo-jail/secrets.env"' --user)`.

### 4.12 A repository that ships a private set

```toml
# <workspace>/yolo-jail.toml (committed)
[env]
files = [".env"]

[env.secrets]
files = [".env.secrets"]
```

`.env` holds `DATABASE_URL=postgres://…`; `.env.secrets` is git-ignored and holds `STRIPE_KEY=sk_test_…`.
`packs = ["claude"]`.

| Name | Reaches | Line |
| :--- | :--- | :--- |
| `DATABASE_URL` | every process | none |
| `STRIPE_KEY` | nobody | `STRIPE_KEY: withheld from every process — nothing in this launch claims it. If no provider or MCP server needs it, it belongs in the public set: yolo config unsecret STRIPE_KEY --project` |

A project may declare a name private; it can only narrow by doing so, never widen ([SE-D5](#SE-D5)). If
the repository instead put `ZAI_API_KEY` in `.env` and a contributor selects `zai`, the refusal of
[§4.4](#44-a-provider-key-left-in-the-public-set-its-pack-selected-the-refusal) prints with `--project`,
and the move keeps the file inside the workspace.

### 4.13 Removing a name

```toml
[env]
files = ["~/.config/yolo-jail/defaults.env"]
unset = ["AWS_PROFILE"]
```

| Name | Reaches | Line |
| :--- | :--- | :--- |
| `AWS_PROFILE` | nobody; a pack's `env` value of that name, and a private-set value of that name, are removed too | none |

Unchanged from today for the public set. A removal carries no value, so it belongs to no set; the one
`unset` list removes the name from both ([SE-D4](#SE-D4)).

### 4.14 `AWS_PROFILE` for `aws` and `terraform` while `bedrock` is selected

`packs = ["claude", "bedrock"]`; claude runs on `bedrock` through SSO; the user wants `AWS_PROFILE=dev` for
`aws` and `terraform` in the jail. `bedrock` claims `AWS_PROFILE`
([`packs/bedrock/pack.toml`](../../packs/bedrock/pack.toml)).

| Where the user writes it | Outcome |
| :--- | :--- |
| `defaults.env` (public) | **refused**, row 2: `AWS_PROFILE is set in your public environment … and the bedrock provider this launch selects claims it as its key … yolo config secret AWS_PROFILE --user` |
| `secrets.env` (private) | claude only; `aws` and `terraform` do without, as today; `AWS_PROFILE (provider bedrock): claude only` |
| `secrets.env`, and the user wants every process to hold it | No config spelling does that ([OQ-SE3](#OQ-SE3)): the per-run grant `yolo --with-credentials bedrock -- bash` (or `yolo host --with-credentials bedrock -- terraform`) is the way |

### 4.15 `bedrock` against `bedrock-key`, with the bearer in the private set

`packs = ["claude", "bedrock"]`, where `packs/bedrock` ships both providers
([bedrock-key-provider.md](bedrock-key-provider.md)); `[allow.aws-auth] profile = "my-sso"`; `secrets.env`
holds `AWS_BEARER_TOKEN_BEDROCK=…`.

| You run | `AWS_BEARER_TOKEN_BEDROCK` reaches | The SSO pointer reaches | Line |
| :--- | :--- | :--- | :--- |
| `yolo -p claude=bedrock-key` | claude only | nobody; the aws-auth daemon is not started | `AWS_BEARER_TOKEN_BEDROCK (provider bedrock-key): claude only` |
| `yolo -p claude=bedrock` | nobody | claude only | `AWS_BEARER_TOKEN_BEDROCK (provider bedrock-key): withheld from every process — no agent in this launch selected it` |
| `yolo` with claude on its native login | nobody | nobody | the same withheld line |

The bearer's file is listed under `[env.secrets] files` like every other key; nothing about it is a
special case. That is the answer this design gives [OQ-BK3](bedrock-key-provider.md#OQ-BK3): in the user
config, in the private set. A bearer left under `[env] files` is the refusal of
[§4.4](#44-a-provider-key-left-in-the-public-set-its-pack-selected-the-refusal) the moment `bedrock` is
selected.

### 4.16 Two agents on different providers in one launch

`packs = ["claude", "pi", "zai", "openrouter"]`; `secrets.env` holds `ZAI_API_KEY` and `OPENROUTER_API_KEY`;
`defaults.env` holds `GH_TOKEN`. You run `yolo -p claude=zai -p pi=openrouter`.

| Name | Reaches | Written to |
| :--- | :--- | :--- |
| `ZAI_API_KEY` | claude only | `agent-env/claude.sh` |
| `OPENROUTER_API_KEY` | pi only | `agent-env/pi.sh` |
| `GH_TOKEN` | every process | `yolo-user-env.sh` |

```text
  ZAI_API_KEY (provider zai): claude only
  OPENROUTER_API_KEY (provider openrouter): pi only
```

A bare shell in the jail holds `GH_TOKEN` and neither key. Unchanged from today.

### 4.17 Several providers claiming one secret

The user declares a provider of their own on the Bedrock platform, with no names of its own:

```toml
[providers.my-bedrock]
platform = "aws-bedrock"
endpoints.anthropic = { base_url = "https://bedrock-runtime.us-west-2.amazonaws.com" }
```

`packs = ["claude", "pi", "bedrock"]`; `secrets.env` holds `AWS_ACCESS_KEY_ID` and
`AWS_SECRET_ACCESS_KEY`. You run `yolo -p claude=bedrock -p pi=my-bedrock`.

| Name | Reaches | Written to |
| :--- | :--- | :--- |
| `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY` | claude and pi | `agent-env/claude.sh`, `agent-env/pi.sh` |

```text
  AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY (provider bedrock, my-bedrock): claude, pi only
```

`my-bedrock` inherits `bedrock`'s claims as a same-platform sibling
([PP-D9](providers-and-profiles-redesign.md#PP-D9)), so both providers claim both names and each agent
on either receives them. Unchanged from today; the names are simply in the private set now, where a
public copy would be refused while either provider is selected.

### 4.18 A secret for an MCP server: `TAVILY_API_KEY`

claude's and pi's MCP tables name a server with `env = { TAVILY_API_KEY = "${TAVILY_API_KEY}" }` and
`requires_env = ["TAVILY_API_KEY"]`. No provider claims the name.

| Where the user writes it | Reaches | What happens to the server |
| :--- | :--- | :--- |
| `defaults.env` (public) | every process | starts under claude and pi; no line. Today's state |
| `secrets.env` (private) | claude and pi, and no other process | starts under both; the server's `requires_env` is those two agents' claim ([OQ-SE2](#OQ-SE2)); `TAVILY_API_KEY (mcp tavily): claude, pi only` |
| `secrets.env` (private), and no agent's config names the server | nobody | not started; `TAVILY_API_KEY: withheld from every process — nothing in this launch claims it.` and the row-4 remedy |

The public row is not refused: an MCP server's claim delivers a private value but never makes a public one
a refusal; only a provider's key may not be public ([SE-D15](#SE-D15)).

### 4.19 A legacy JSONC user config

```jsonc
// ~/.config/yolo-jail/config.jsonc
{ "packs": ["claude", "zai"], "env_sources": ["~/.config/yolo-jail/secrets.env"] }
```

`secrets.env` holds `ZAI_API_KEY`. A legacy list is read into the public set, so the launch is refused as in
[§4.4](#44-a-provider-key-left-in-the-public-set-its-pack-selected-the-refusal), and the remedy reads
`run yolo config migrate, then yolo config secret ZAI_API_KEY --user` ([SE-D6](#SE-D6)). A legacy file with
no claimed name launches as it does today.

### 4.20 A fresh launch against an attach

The common files, claude on zai, the jail running from
[§4.1](#41-a-provider-key-in-the-private-set-its-pack-selected-the-agent-on-it). While it runs, the user
adds `ZAI_API_KEY` to `defaults.env` as well, then opens a second terminal with `yolo`.

| Entry | Outcome |
| :--- | :--- |
| the attach | refused with [§3.5](#35-the-refusal)'s text; nothing is rewritten; the running jail's processes keep the environment they hold |
| the next fresh launch, after `yolo config secret` | [§4.1](#41-a-provider-key-in-the-private-set-its-pack-selected-the-agent-on-it) |

Every entry runs the same gate ([SE-D7](#SE-D7)). Moving a name from public to private between entries
takes it out of `yolo-user-env.sh` on the next entry, since that file is truncated and rewritten each time.

### 4.21 Handing a shell a provider's key for one launch: `--with-credentials`

The files of [§4.16](#416-two-agents-on-different-providers-in-one-launch). You run
`yolo --with-credentials zai -- bash`.

| Name | Reaches | Line |
| :--- | :--- | :--- |
| `ZAI_API_KEY` | every process in this jail, for its life | `ZAI_API_KEY (provider zai): every process in this jail, by its --with-credentials grant` |
| `OPENROUTER_API_KEY` | nobody | `… withheld from every process — no agent in this launch selected it` |
| `GH_TOKEN` | every process | none |

Unchanged from today. The grant names a **provider**, so it widens only a claimed private value and only
for one launch; a private value nothing claims has no grant to ride and belongs in the public set if every
process should hold it.

### 4.22 The same cases at `yolo host`

`yolo host -- <cmd>` composes one process from your **user** config only, so
[§4.11](#411-a-workspace-local-config-with-a-private-file) and
[§4.12](#412-a-repository-that-ships-a-private-set) do not apply there: a workspace entry is never read.
Everything else gives the same reach, with these differences in what is printed:

| Case | At the host |
| :--- | :--- |
| a claimed name in the public set | the same refusal, before anything runs, at `yolo host --`, `yolo host env` and `yolo host -p zai -- claude` alike |
| a private name withheld that your shell also exports (`export GH_TOKEN=…` before `yolo host`) | the command holds your shell's value; the line reads `GH_TOKEN: not added by yolo — … the invoking shell's own value passes through` and still names `yolo config unsecret` |
| `yolo host -p zai -- bash` | refused before anything runs, as today; the remedy is `yolo host --with-credentials zai -- bash`, which hands `bash` the key for that run |
| `eval "$(yolo host env --with-credentials zai)"` | exports the key into your current shell; a public `GH_TOKEN` is exported too; a private, unclaimed one is not, and the line on stderr names the verb |

A variable your shell exports is in neither set: it is not config, and the gate treats it as today.

### 4.23 The same cases on macos-user

Every example above reads the same on macos-user. The vehicle differs, not the reach: the public set and
the launched program's own names ride the per-session file, every other agent's own names ride that
agent's env file, and a withheld name is written to neither. The refusal is the same text.

## 5. Migration: the first launch after the change

A user with today's config — `[env] files = ["~/.config/yolo-jail/secrets.env"]`, the file holding
`ZAI_API_KEY`, `GH_TOKEN` and `DEBUG` — launches with `packs = ["claude", "zai"]`. The launch is refused:

```text
config.env.files: ZAI_API_KEY is set in your public environment ([env], from
~/.config/yolo-jail/secrets.env), and the zai provider this launch selects claims it as its key. A
public value reaches every process in the jail, so a provider's key may not live there. Move it to the
private set: yolo config secret ZAI_API_KEY --user
```

The user runs it; with no `[env.secrets] files` yet, the verb first asks for one file
(`yolo config add env.secrets.files '"~/.config/yolo-jail/private.env"' --user`), then moves the one
line. `GH_TOKEN` and `DEBUG` stay where they are and keep reaching every process. The next launch is
[§4.1](#41-a-provider-key-in-the-private-set-its-pack-selected-the-agent-on-it). The same user with
`packs = ["claude"]` is not refused: nothing selected claims the key, so the launch runs and prints
[§4.5](#45-the-same-key-public-its-pack-not-selected)'s line, every time, until the key moves.

Nothing is rewritten in the user's files by a launch; the verb is the user's own act. A disclosure is never
suppressible ([OQ-RO3](../reference/report-tiers.md#why-its-this-way)), and there is no `YOLO_ALLOW_*`
hatch: the fix is one config command, and a hatch exists for broken user config, which this is not
([SE-D10](#SE-D10)).

**State that already exists.** On the first entry after the change the shared file is truncated and
rewritten, so a key that moved to the private set leaves it; a jail already running keeps the environment
its processes hold until it restarts. Per-agent files are rewritten whole on every entry, as today.

## 6. Alternatives considered

| Alternative | Verdict |
| :--- | :--- |
| **A. Claims from every shipped pack, selected or not** | **Rejected as the rule**, kept as one line's wording ([§3.6](#36-the-lines)). It still infers secrecy from a name list: a key for a provider yolo does not ship goes everywhere, and a release that ships a new pack changes reach in launches that never selected it. Reading the shipped set to **decide** also cuts against the selection-scoped rule the pack system keeps ([`AGENTS.md`](../../AGENTS.md#packs-and-what-core-does-not-know)) |
| **B. The key named on the provider: `[providers.zai] credentials = "~/.config/yolo-jail/zai.env"`** | **Rejected.** The common habit is one file for every key, so this is N pointers to one file; a user-declared provider inheriting a platform's claims has nothing to point at; and the `--with-credentials all` grant needs the one store [OQ-ES5](credential-sources-separation.md#10-decision-ledger) ruled |
| **C. A per-entry flag, `{ file = "x.env", secret = true }`, inside `[env] sources`** | **Rejected.** A flag on a list entry is a second spelling of the same fact, invisible to `yolo config add env.files`, and an inline entry would need a third; a table is one spelling for both forms |
| **D. A top-level `[secrets]` table** | **Rejected** for `[env.secrets]`, a narrow call: the private set shares `[env]`'s keys, order and scope rules, and reads as what it is, a second list of environment variables. The owner's "qualified with a namespace in TOML" allows either |
| **E. Refuse a public name an unselected shipped pack claims** | **Rejected as a refusal.** Its trigger is the shipped set, so a release could refuse a working config; the same fact is one line ([SE-D8](#SE-D8)) |
| **F. Today's behavior** | **Rejected** on the owner's words of 2026-11-09 |

## 7. Risks

| Risk | Mitigation |
| :--- | :--- |
| A user puts a plain variable in the private set and loses `gh` or an MCP server for an hour | The line prints on every launch and in `yolo check`, naming `yolo config unsecret`; the launch still starts ([OQ-SE1](#OQ-SE1)) |
| A user's one dotenv file mixes keys and plain variables, and the refusal makes them split it by hand | `yolo config secret` moves one line, and the refusal names it; splitting a file is the one-time cost [§5](#5-migration-the-first-launch-after-the-change) states |
| A workspace config smuggles a file from outside the workspace into either set | Refused by the same check as today ([OQ-AS3](../research/agent-safehouse.md#OQ-AS3)), extended to `env.secrets.files` |
| The three notches drift | The rule is in the gate's one function ([CN-D5](../reference/providers.md#CN-D5)); each notch's tests already pin the gate's result through its vehicle |
| The set bit is wrong for an include or a layer | It is assigned where the entry is read, the same place relative paths are anchored per file (`AnchorEnvSources`, [`envsources.go`](../../internal/config/envsources.go)) |
| A repository's public `.env` and a user's private set name the same variable, and the launch refuses | It does, by ruling ([SE-D2](#SE-D2)); the refusal names both copies and the `--drop` command for each, in the scope that holds it |

## 8. What this does not license

- **No value in any line**, at any notch, in `launch.log` or `boot.log`; names and files only, as every
  existing disclosure ([CN-D16](../reference/providers.md#CN-D16)).
- **No widening by a release.** A new shipped pack may add a line; it never changes who receives a value in
  a launch that does not select it.
- **No judgment on inline against file.** Both sets take both, and no line distinguishes them.
- **No per-launch spelling for a private name nothing claims.** `--with-credentials` stays by provider; a
  value every process should hold is public, or it is not held.
- **No grant from config** ([OQ-ES5](credential-sources-separation.md#10-decision-ledger) stands): the
  private set is a fact about where a variable is declared, not a grant to a process.
- **Not access control within a jail's uid.** Every file a vehicle writes is readable by every process of
  that uid; the gate governs ambient environment, as
  [the separation doc says](credential-sources-separation.md#8-what-this-does-not-license).
- **No change to the credential pre-flight, to removals, to the launch-environment relay, or to the wire
  bridge's key read** ([CN-D11](../reference/providers.md#CN-D11)). Each reads the gate's answer.

## 9. Open Questions

None. [OQ-SE1](#OQ-SE1) to [OQ-SE3](#OQ-SE3) were ruled on 2026-11-09 and are in the [Decision Ledger](#decision-ledger).

## Decision Ledger

The owner's rulings ([OQ-SE1](#OQ-SE1) to [OQ-SE3](#OQ-SE3), [SE-D2](#SE-D2)) and the seat's mechanism decisions.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="SE-D1"></a>SE-D1 | **Hydration keeps which set declared each value**, assigned where the entry is read beside the per-file anchoring of relative paths, and carried through the merge, so the gate never re-reads a config to learn it | 2026-11-09 | [§3.3](#33-the-two-sets) | ✅ `TestResolveEnvKeepsWhichSetDeclaredEachName`; through a recomposition, `TestAPrivateKeyReachesOnlyItsClaimingAgent` (integration) |
| <a id="SE-D2"></a>SE-D2 | **A name in both sets is a fatal error** (owner, 2026-11-09: *"just make this a fatal error."*). The refusal names both copies and the `--drop` command that keeps each, whatever claims the name | 2026-11-09 | [§3.3](#33-the-two-sets), [§4.8](#48-the-same-name-in-both-sets) | ✅ `TestANameInBothSetsIsReportedWithBothPlaces`, `TestSecretEnvRefusalNamesANameInBothSetsAndBothDrops`, `TestANameInBothSetsRefusesBeforeAnythingIsWritten`, `TestHostRefusesANameInBothSets`, `TestThePacksSectionFailsANameInBothSets`, `TestANameInBothSetsRefusesTheLaunch` (integration) |
| <a id="SE-D3"></a>SE-D3 | **`[env.secrets]` has `files` and `set`, no `sources`**: the ordered mixed list exists to lower legacy `env_sources`, and the private set has no legacy spelling | 2026-11-09 | [§3.3](#33-the-two-sets) | ✅ `TestAPrivateEnvTableValidatesAsTheReadableEnvShape` |
| <a id="SE-D4"></a>SE-D4 | **One `unset`, under `[env]`, removes a name from both sets.** A removal carries no value, so it has no set | 2026-11-09 | [§3.3](#33-the-two-sets), [§4.13](#413-removing-a-name) | ✅ `TestOneUnsetRemovesANameFromBothSets`, `TestResolveEnvCarriesRemovalsAFileLaterCancels` |
| <a id="SE-D5"></a>SE-D5 | **Both sets exist in both scopes**, under the same rule. A workspace private file must lie inside the workspace, refused with the user-scope spelling as `[env] files` is today; a project declaring a name private can only narrow its reach | 2026-11-09 | [§3.3](#33-the-two-sets), [§4.12](#412-a-repository-that-ships-a-private-set) | ✅ `TestAWorkspacePrivateFileOutsideTheWorkspaceIsRefused`, `TestTheSharedFileCarriesNoPrivateValue` |
| <a id="SE-D6"></a>SE-D6 | **A legacy JSONC file has no private set.** Its `env_sources` is read into the public set as today, and the refusal names `yolo config migrate` before the move verb | 2026-11-09 | [§3.3](#33-the-two-sets), [§4.19](#419-a-legacy-jsonc-user-config) | ✅ `TestSecretEnvRefusalOnALegacyFileNamesMigrateFirst`, `TestHostRefusesALegacyPublicKeyNamingMigrateFirst`, `TestConfigSecretOnALegacyConfigNamesMigrateFirst` |
| <a id="SE-D7"></a>SE-D7 | **The refusal fires at every entry that runs the gate** — fresh launch, attach, `yolo check`, `yolo host --`, `yolo host env` — and at every notch; nothing is written before it, and a running jail keeps its environment | 2026-11-09 | [§3.4](#34-the-four-outcomes), [§4.20](#420-a-fresh-launch-against-an-attach) | ✅ `TestAPublicProviderKeyRefusesBeforeAnythingIsWritten`, `TestHostRefusesAPublicProviderKey` (`yolo host --` and `yolo host env`), `TestAPublicProviderKeyRefusesTheLaunch` and `TestAnAttachRefusesAPublicProviderKey` (integration); macos-user and Apple Container take the same call above the backend switch, not run on a Mac |
| <a id="SE-D8"></a>SE-D8 | **The shipped set is read to word a line, never to decide a reach**: a public name an unselected shipped pack claims gets one line naming the move; a private name such a pack claims gets `yolo pack add` in its remedy; a release therefore changes text, never delivery | 2026-11-09 | [§3.6](#36-the-lines) | ✅ `TestAnUnselectedShippedProviderWordsAPublicNamesLine`, `TestAPrivateNameAnUnselectedPackClaimsNamesThePack`, `TestUnselectedProviderClaimsReadsTheShippedSetMinusTheSelection` |
| <a id="SE-D9"></a>SE-D9 | **Two named verbs move a name between the sets**, `yolo config secret <NAME>` and `yolo config unsecret <NAME>` in the scope that holds the entry unless a scope flag says otherwise; `--drop` removes the other set's copy of the name instead of moving one, so `secret NAME --drop` keeps the private copy and `unsecret NAME --drop` the public one; a verb earns its place because the move crosses a dotenv file and a config file, which no generic edit can. The names are the seat's and the plan may rename them | 2026-11-09 | [§3.5](#35-the-refusal) | ✅ `TestConfigSecretMovesOneDotenvLineToThePrivateFile`, `TestConfigSecretMovesAnInlineValue`, `TestConfigSecretDropKeepsThePrivateCopy`, `TestConfigUnsecretMovesBack`, `TestConfigSecretMovesANameOutOfAnInlineSourcesTable` |
| <a id="SE-D10"></a>SE-D10 | **No hatch.** No `YOLO_ALLOW_*` variable restores today's delivery; the one remedy is the config verb | 2026-11-09 | [§5](#5-migration-the-first-launch-after-the-change) | ✅ no `YOLO_ALLOW_*` variable reaches the refusal |
| <a id="SE-D11"></a>SE-D11 | **The disclosure gains two line kinds** — a private name withheld for want of a claim, and a public name an unselected shipped pack claims — and keeps its names-only rule; nothing is printed about whether a secret was inline or in a file | 2026-11-09 | [§3.6](#36-the-lines) | ✅ `TestAPrivateNameNothingClaimsReachesNobody`, `TestAnUnselectedShippedProviderWordsAPublicNamesLine`, `TestDescribeEnvSourcesNamesThePrivateEntriesConsulted` |
| <a id="SE-D12"></a><a id="OQ-SE1"></a>SE-D12 ([OQ-SE1](#OQ-SE1)) | **A private name nothing claims starts the launch**, reaches nobody, and prints a line naming `yolo config unsecret` on every launch and in `yolo check` (owner, 2026-11-09: A) | 2026-11-09 | [§3.4](#34-the-four-outcomes), [§3.6](#36-the-lines) | ✅ `TestAPrivateNameNothingClaimsReachesNobody`, `TestSecretEnvGapWarnsOfAPrivateNameNothingClaims`, `TestHostSecretEnvKeepsThePrivateSetFromAnUnclaimingCommand` |
| <a id="SE-D13"></a><a id="OQ-SE2"></a>SE-D13 ([OQ-SE2](#OQ-SE2)) | **An MCP server's `requires_env` claims a name too**, for the agents whose config names that server, and delivers a private value to those agents only (owner, 2026-11-09: B) | 2026-11-09 | [§1](#1-words-this-doc-uses), [§4.18](#418-a-secret-for-an-mcp-server-tavily_api_key) | ✅ `TestAnMCPServersRequiresEnvClaimsAPrivateNameForItsAgents`, `TestMCPEnvClaimsAndAgents` |
| <a id="SE-D14"></a><a id="OQ-SE3"></a>SE-D14 ([OQ-SE3](#OQ-SE3)) | **A claimed name never reaches every process from config**; the per-launch `--with-credentials` grant is the one widening (owner, 2026-11-09: A). This answers [OQ-ES6](credential-sources-separation.md#OQ-ES6) too | 2026-11-09 | [§4.14](#414-aws_profile-for-aws-and-terraform-while-bedrock-is-selected) | ✅ `TestAPublicNameAProviderClaimsIsReportedAndNotWidened`, `TestTheFallbackStillScopesAClaimedNameNobodyHydrated` |
| <a id="SE-D15"></a>SE-D15 | **Only a provider's claim makes a public value a refusal.** An MCP server's claim delivers a private value, but a public name only an MCP server claims stays public and reaches every process, as today; otherwise every MCP key kept public today would refuse | 2026-11-09 | [§3.4](#34-the-four-outcomes) | ✅ `TestAPublicNameOnlyAnMCPServerClaimsStaysShared`, `TestSecretEnvRefusalIsEmptyForAPrivateKeyAndAnUnclaimedPublicName` |

## Appendix: what this doc did not verify

- The `[env.secrets]` table, its `files` and `set` rows in the readable schema, and the verbs
  `yolo config secret`, `yolo config unsecret` and `--drop` are proposed, not built. The generic verb's
  one-key-one-value shape and its refusal of an untyped key were read in
  [`configeditverbs.go`](../../internal/cli/configeditverbs.go); that `yolo config add
  env.secrets.files '"<path>"' --user` works once the schema names the key follows from
  `addMissingKey`, which adds a missing key with its parents, and was not run.
- That the MCP `requires_env` check reads a public value from the boot environment on every backend;
  [CN-D19](../reference/providers.md#CN-D19) says per agent, and the shared file is sourced before the
  tables render, but this was not re-run.
- The macos-user and host rows of [§3.7](#37-what-each-notch-writes) are read from the code's comments and
  [`providers.md`](../reference/providers.md#the-credential-gate), not measured on a Mac.
- That the workspace-scope file check (`validateEnvSourceScope`) extends to a new `env.secrets.files` list
  by adding one `check(...)` call; read, not changed.
