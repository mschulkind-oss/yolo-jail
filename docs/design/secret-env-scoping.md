---
title: "A key in your secret store reaches nobody until a provider claims it or you share it"
date: 2026-11-09
status: in-review
stage: DESIGN
next: "Rule OQ-SE1 and OQ-SE2 — the first launch's behavior and whether sharing overrides a claim decide the migration text"
tags: [credentials, env_sources, providers, credential-gate, security, parity, config]
summary: "Today a dotenv value is withheld from the wrong agents only while some selected pack's provider claims its name; deselect the pack and the same key reaches every process in the jail. This design makes the dotenv files a user config lists a secret store whose values reach the agents whose selected provider claims them, every process when the user shares the name, and otherwise nobody, so a value's reach never changes with an edit the user did not make. One rule for zai, cerebras, kilo, openrouter, bedrock, bedrock-key and any provider a user declares; the same rule at the jail, macos-user and host notches."
vantage:
  status-chip: true
---

# A key in your secret store reaches nobody until a provider claims it or you share it

**Status:** 2026-11-09. Nothing built. Evidence verified at `22d155da5`. The seat's design, pending the
owner's review; every ledger row is the seat's. Breaking changes are permitted by the owner's words of
2026-11-09 (*"I'm not afraid of a breaking change because I don't know that anybody else is using this"*),
and this change is in the trust-boundary class [legacy-config-compat.md](legacy-config-compat.md#OQ-LC3)
keeps outside its keep-working rule.

> **In short.** Whether a dotenv value is a secret is decided by **where it came from** — a file your user
> config lists is a secret store — never by **which packs happen to be selected**. A secret reaches the
> agents whose selected provider claims its name, every process when you list the name as shared, and
> otherwise nobody; the launch names each outcome and the one command that changes it.

**Why it matters.** With `packs = ["claude", "zai"]`, `ZAI_API_KEY` reaches claude on the zai profile and
nothing else. Remove `zai` from `packs` and leave the key in the dotenv: the key is written into the shared
`yolo-user-env.sh` and every shell, agent and MCP server in the jail holds it. The same value's reach flips on
an edit that mentions neither the key nor a credential, and nothing says so.

**The shape.** One classification at hydration (the **origin** of each value: the config scope and entry
kind that produced it); one gate rule with three outcomes (claimed → selecting agents, shared → every
process, else withheld); one new user-scope list, `[env] shared`; one disclosure block that names every
outcome and its remedy; no change to the delivery vehicles.

**Cost.** Every unclaimed variable in a user's dotenv file — `GH_TOKEN`, `TAVILY_API_KEY`, `DEBUG` — stops
reaching the jail until the user names it under `[env] shared`. One command per name, printed by the launch.

**Start at [§3](#3-the-rule)** — the origin classification and the three outcomes. Everything else applies it.

**Needs your ruling:** [OQ-SE1](#OQ-SE1), [OQ-SE2](#OQ-SE2), [OQ-SE3](#OQ-SE3), [OQ-SE4](#OQ-SE4).

**Reads with:** [`providers.md`](../reference/providers.md#the-credential-gate) (the credential gate as
built, whose rule this amends), [`credential-sources-separation.md`](credential-sources-separation.md)
(the `--with-credentials` grant this keeps, and [OQ-ES6](credential-sources-separation.md#OQ-ES6), which
[OQ-SE2](#OQ-SE2) subsumes), [`bedrock-key-provider.md`](bedrock-key-provider.md) (whose
[OQ-BK3](bedrock-key-provider.md#OQ-BK3) this design answers as "the same as every provider"),
[`agent-credentials.md`](../reference/agent-credentials.md#env_sources--the-sanctioned-secret-channel)
(the channel and its vehicles). No companion plan is seeded: the owner asked for the design alone.

---

## 1. Words this doc uses

- **The credential gate** — the one function that decides which composed environment values reach which
  process at a launch; built, named in [`providers.md`](../reference/providers.md#the-credential-gate), and
  located in [`credentialscope.go`](../../internal/packload/credentialscope.go). Not a file writer: where an
  answer lands is a vehicle's business.
- **A claim** — a provider's declaration that a variable name carries its credential, its
  `api_key_env_name` ([`packs/zai/pack.toml`](../../packs/zai/pack.toml) claims `ZAI_API_KEY`), widened to
  same-platform siblings ([PP-D9](providers-and-profiles-redesign.md#PP-D9)). From
  [`providers.md`](../reference/providers.md#the-credential-gate). Not a value: a pack never carries one.
- **A delivery vehicle** — the code that writes a launch's environment for one notch or backend: the
  container's shared `yolo-user-env.sh` plus per-agent `agent-env/<agent>.sh` files, macos-user's per-session
  file, the host notch's one exec'd process. From [`providers.md`](../reference/providers.md#the-credential-gate).
- **The secret store** *(coined here)* — the dotenv files a **user-scope** config lists under `[env] sources`
  or `[env] files` (legacy `env_sources`), and every value hydrated from one. Not an inline `set` table, which
  sits in the config file itself, and not a workspace config's files, which must lie inside the workspace.
- **A shared name** *(coined here)* — a variable name the user lists under `[env] shared` in user scope,
  asking that its value reach every process. Not a grant: a grant names a provider for one launch; a shared
  name is a standing fact about one variable.
- **Reach** *(coined here)* — the set of processes a value's environment delivery includes: one agent's,
  several agents', every process, or none. Not file permissions: every file a vehicle writes is readable by
  every process of the jail's uid ([§8](#8-what-this-does-not-license)).
- **The notches** — the jail (podman, Apple Container), macos-user, and `yolo host --`, the three places yolo
  composes an environment; the term is `notch-convergence.md`'s. The owner's parity rule is that each gives
  the same answer to the same config.

## 2. What exists today, precisely

Verified 2026-11-09 against `22d155da5`.

| Fact | Where |
| :--- | :--- |
| The gate reads the claims off the **composed** provider table — the selected packs' providers plus the user's `providers` entries — and nothing else | [`credentialscope.go:50-55, 676-712`](../../internal/packload/credentialscope.go#L50-L55) |
| A hydrated value whose name no composed provider claims goes to the **shared** set, which every process receives | [`credentialscope.go:241-247`](../../internal/packload/credentialscope.go#L241-L247); ruled as [CN-D3](../reference/providers.md#CN-D3) |
| A claimed value goes only to agents whose active set resolves to a claimant | [`credentialscope.go:266-271`](../../internal/packload/credentialscope.go#L266-L271) |
| The shared set is written to `yolo-user-env.sh` (0600), sourced by every shell and the entrypoint; per-agent values to `agent-env/<agent>.sh` | [`userenv.go:198-251`](../../internal/cli/run/userenv.go#L198-L251); [`providers.md`](../reference/providers.md#the-credential-gate) |
| macos-user's session file and the host notch's exec read the same gate; the host composes from user scope only | [`envfile.go`](../../internal/macosuser/envfile.go); [`host.go:2544-2593`](../../internal/cli/host.go#L2544-L2593); [CN-D5](../reference/providers.md#CN-D5) |
| The disclosure lists only **claimed** names, grouped by claimant and recipients, never values; an unclaimed name is never mentioned | [`credentialscope.go:1047-1160`](../../internal/packload/credentialscope.go#L1047-L1160); [CN-D16](../reference/providers.md#CN-D16) |
| The hydrated map carries **no provenance**: which scope or entry kind produced a value is gone after the merge, and the one check that needs it re-reads the workspace config | [`envsourcescope.go:31-35`](../../internal/config/envsourcescope.go#L31-L35); [`envsources.go:259-276`](../../internal/config/envsources.go#L259-L276) |
| A workspace-scope dotenv entry that resolves outside the workspace is refused, naming the user-scope move | [`envsourcescope.go:16-23`](../../internal/config/envsourcescope.go#L16-L23); the owner's ruling on [OQ-AS3](../research/agent-safehouse.md#OQ-AS3), 2026-10-29 |
| `yolo check` runs the same gate to predict a launch, with no derives | [`envoverrides.go:172-190`](../../internal/cli/check/envoverrides.go#L172-L190) |
| The `[env]` table has `sources`, `files`, `set`, `unset`; `yolo config add env.files …` appends to a list | [`config_ref.txt:734-754`](../../internal/cli/config_ref.txt#L734-L754) |
| The shipped packs a launch did **not** select are already read at one notch, to word what it cannot offer | [`protocolresolution.go:539-556`](../../internal/packload/protocolresolution.go#L539-L556) |

The hole, stated as the gate sees it: **"claimed" is computed from the selection, so "secret" is too.**
The [OQ-PR2](provider-reachability-and-model-names.md#9-decision-ledger) amendment in
[`provider-reachability-and-model-names.md`](provider-reachability-and-model-names.md#9-decision-ledger)
records the consequence — a claim is launch-wide and withholds a name from every other process while its
pack is selected, and the moment the pack is not, the name is nobody's and goes everywhere.
The credential pre-flight cannot catch it: it asks only about **selected** providers
([`providerpreflight.go:22-30`](../../internal/cli/run/providerpreflight.go#L22-L30)).

### 2.1 The measured flip

The owner's case, in the gate's terms. One user config, one dotenv holding `ZAI_API_KEY`.

| `packs` | `ZAI_API_KEY` claimed? | Reach | Written to | Disclosed? |
| :--- | :--- | :--- | :--- | :--- |
| `["claude", "zai"]`, claude on zai | yes | claude only | `agent-env/claude.sh` | `… (provider zai): claude only` |
| `["claude", "zai"]`, claude on native | yes | nobody | nowhere | `… withheld from every process` |
| `["claude"]` | **no** | **every process** | **`yolo-user-env.sh`** | **nothing** |

The third row is reached by removing one word from `packs`. The same table holds for every shipped
provider key and for any user-declared provider the user later deletes from `providers`.

## 3. The rule

### 3.1 Principles

- **P1. Secrecy is a property of origin, not of selection.** What a user keeps in a file outside the
  repository, and lists from the user config, is a secret. Whether yolo ships a pack that names the variable
  has no bearing on that.
- **P2. Nothing leaks** ([OQ-BR4](../reference/providers.md#oq-br4), ruled 2026-09-25). A value's reach
  may narrow or stay when the user edits something unrelated to it; it never widens.
- **P3. The user says "everyone" in as many words, once, per name.** A value that should reach every
  process is named, in user scope, and the launch repeats the fact on every run.
- **P4. Every withheld value names its one command** ([the happy-path
  principle](../reference/happy-path-principle.md#the-principle)). A line that only reports is a defect.
- **P5. One gate, three notches, one answer.** The rule lives in the gate's one function, so the jail,
  macos-user and `yolo host` compose the same reach for the same config.

### 3.2 Origin: what hydration must now keep

Each hydrated value carries its **origin**, two facts the resolver knows while reading and drops today:

| Origin | Produced by |
| :--- | :--- |
| **store** | a dotenv file listed by a user-scope config: the user config, its includes, a `--user-layer`, a per-workspace file under `~/.config/yolo-jail/workspaces/` |
| **plain** | a user-scope inline `set` (top-level or inside `sources`); any workspace-scope entry, file or inline |

An inline value is plain by construction: it is written in the config file itself and, per
[provider-reachability's ledger](provider-reachability-and-model-names.md#9-decision-ledger), lands in the
assembled config at 0644 (that doc's claim; not re-measured here). A workspace-scope file is plain because the
[OQ-AS3](../research/agent-safehouse.md#OQ-AS3) ruling confines it to the workspace, which every process in
the jail can already read; [OQ-SE4](#OQ-SE4) asks whether the owner wants that half reversed.

Later entries still win per name, across kinds and scopes, as today. When a workspace inline entry
re-assigns a name the user's store also sets, the surviving value is the workspace's and its origin is plain:
the user's value is not delivered anywhere, so nothing of the user's leaks through the override.

### 3.3 The three outcomes

For every hydrated value, in this order, the first rule that applies decides:

| # | Condition | Reach | Disclosure line |
| :--- | :--- | :--- | :--- |
| 1 | a **selected** provider claims the name (the composed table, as today) — unless [OQ-SE2](#OQ-SE2) says a shared name wins | the agents whose active set resolves to a claimant; no other process | `ZAI_API_KEY (provider zai): claude only`, or `… withheld from every process — no agent in this launch selected it` |
| 2 | the name is a **shared name** | every process | `GH_TOKEN, META_API_KEY: every process, shared by env.shared` |
| 3 | origin **store**, neither claimed nor shared | **nobody**; written to no file | `CEREBRAS_API_KEY: withheld from every process — no provider in this launch claims it and it is not shared.` then the remedy ([§3.5](#35-the-remedy-line)) |
| 4 | origin **plain** | every process, as today | none, as today |

Rule 1 is today's gate unchanged; rule 4 is today's [CN-D3](../reference/providers.md#CN-D3) narrowed to
plain values. Rules 2 and 3 are the design. A removal (`unset`, or an inline `null`) is unchanged and names
nothing: it carries no value, so it has no reach.

> [!WARNING]
> The intuitive fix — widen the claims to every pack yolo ships, selected or not — keeps the gate guessing
> secrecy from a name list. A key for a provider yolo has not heard of still goes everywhere, and a release
> that ships a new pack changes who receives a value in launches that never selected it
> ([§6](#6-alternatives-considered), A). The shipped set is read only to **word** a remedy, never to decide one.

### 3.4 The `[env] shared` list

```toml
# ~/.config/yolo-jail/config.toml
[env]
files  = ["~/.config/yolo-jail/secrets.env"]
shared = ["GH_TOKEN", "TAVILY_API_KEY", "META_API_KEY"]
```

- **User scope only**, like `allow` and `active_provider`
  ([`config_ref.txt`](../../internal/cli/config_ref.txt#L69-L100)). A workspace config carrying `shared` is
  refused by validation with the user-scope spelling, because a workspace config is agent-editable and a
  `shared` entry there would hand every process the user's store value of that name.
- **Plain names, no patterns.** A name is an environment variable name; anything else is refused by the
  same validator that checks dotenv keys.
- **Degenerate inputs.** An empty list shares nothing. Duplicates collapse. A shared name nothing assigns is
  harmless at launch and a `yolo check` warning ("shared but never set"), not a refusal. A shared name that
  an `unset` removes is removed: the removal wins the name, and no line is printed for it.
- **A shared name that a selected provider also claims** is [OQ-SE2](#OQ-SE2).
- **Not a target list.** A shared name reaches every process; "these two agents only" is
  [OQ-SE3](#OQ-SE3).

Every case, written out as config, selection, reach and printed line, is in
[§4](#4-worked-examples-every-case-as-written).

### 3.5 The remedy line

Rule 3's line names the easiest next step, chosen in this order; the first that applies is printed, and the
verb is spelled for the user config's format as the scope refusal already does
([`envsourcescope.go:100-105`](../../internal/config/envsourcescope.go#L100-L105)):

1. A **shipped pack this launch did not select** declares a provider claiming the name: `To use it as
   cerebras's key: yolo pack add cerebras. To hand it to every process instead: yolo config add env.shared
   '"CEREBRAS_API_KEY"' --user`.
2. Otherwise: `To hand it to every process: yolo config add env.shared '"FOO_TOKEN"' --user`.
3. On a legacy JSONC user config the verb cannot write, the line names `yolo config migrate` first, then the
   verb.

At the host notch the same lines print, with the host's existing additions
([ES-D2, ES-D23](credential-sources-separation.md#10-decision-ledger)): a name the invoking shell also
exports is "not added by yolo", and a claimed name's remedy names `--with-credentials`.

### 3.6 What each notch writes

Nothing about the vehicles changes; only what the gate hands them does.

| Notch | Shared set (rules 2 and 4) | Per-agent (rule 1) | Rule 3 |
| :--- | :--- | :--- | :--- |
| podman, Apple Container | `yolo-user-env.sh`, truncated and rewritten on every entry, so a value that stops being shared vanishes on the next attach | `agent-env/<agent>.sh` | written nowhere |
| macos-user | the per-session file, plus the launched program's own | each other agent's env file | written nowhere |
| `yolo host --` | the one exec'd process | the same process, when the command is an agent on a claiming profile | not added; the shell's own export, if any, passes through |

`yolo check` predicts the same three outcomes from the same gate and prints the same lines, remedies
included, so a user sees the withheld list before launching.

## 4. Worked examples: every case, as written

Each example shows the files as you would write them, what you select, who receives each name, and
the line the launch prints. Values are placeholders; the launch never prints one. Two spellings here are
proposed, not built: the `[env] shared` key, and `yolo config add env.shared '"NAME"' --user` as the
verb that appends to it (whether that verb takes a new `[env]` list, and several names at once, is
unverified). Where an outcome rests on an open question, the example shows the leaning and names it.
`yolo host -- <cmd>` and macos-user give the same answers unless an example says otherwise
([§4.17](#417-the-same-cases-at-yolo-host), [§4.18](#418-the-same-cases-on-macos-user)).

The common files, used unless an example changes them:

```toml
# ~/.config/yolo-jail/config.toml
packs = ["claude", "pi", "zai"]

[env]
files = ["~/.config/yolo-jail/secrets.env"]
```

```bash
# ~/.config/yolo-jail/secrets.env
ZAI_API_KEY=zai-…
GH_TOKEN=ghp_…
```

### 4.1 A provider key, its pack selected, the agent on it

You run `yolo -p claude=zai` (or set `yolo active-provider claude zai` once).

| Name | Reaches | Written to |
| :--- | :--- | :--- |
| `ZAI_API_KEY` | claude only | `agent-env/claude.sh` |
| `GH_TOKEN` | nobody ([§4.4](#44-gh_token-not-shared)) | nowhere |

```text
  ZAI_API_KEY (provider zai): claude only
```

Unchanged from today for the key. This is rule 1 of [§3.3](#33-the-three-outcomes).

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

`packs = ["claude", "pi"]`; the dotenv still holds `ZAI_API_KEY`.

| Name | Reaches today | Reaches under this design | Written to |
| :--- | :--- | :--- | :--- |
| `ZAI_API_KEY` | **every process**, no line | nobody | nowhere |

```text
  ZAI_API_KEY: withheld from every process — no provider in this launch claims it and it is not
    shared. To use it as zai's key: yolo pack add zai. To hand it to every process instead:
    yolo config add env.shared '"ZAI_API_KEY"' --user
```

The first clause of the remedy names the pack because a shipped pack yolo did not select declares a
provider claiming the name ([§3.5](#35-the-remedy-line), step 1).

### 4.4 `GH_TOKEN`, not shared

The common files, any selection. No provider claims `GH_TOKEN`, and the user has not shared it.

| Name | Reaches today | Reaches under this design |
| :--- | :--- | :--- |
| `GH_TOKEN` | every process | nobody |

```text
  GH_TOKEN: withheld from every process — no provider claims it and it is not shared.
    To hand it to every process: yolo config add env.shared '"GH_TOKEN"' --user
```

The jail's `gh` fails later with its own message. [OQ-SE1](#OQ-SE1) asks whether this launch should
instead refuse until the name is shared; under its leaning, it starts.

### 4.5 `GH_TOKEN`, shared

```toml
[env]
files  = ["~/.config/yolo-jail/secrets.env"]
shared = ["GH_TOKEN"]
```

| Name | Reaches | Written to |
| :--- | :--- | :--- |
| `GH_TOKEN` | every process | `yolo-user-env.sh` |

```text
  GH_TOKEN: every process, shared by env.shared
```

The line prints on every launch for as long as the name is shared.

### 4.6 `META_API_KEY` for pi and for the Muse Code CLI

`packs = ["claude", "pi", "muse-code"]`; the dotenv holds `META_API_KEY`; pi's own provider list and
the Muse CLI both read it, and no yolo provider claims it.

```toml
[env]
files  = ["~/.config/yolo-jail/secrets.env"]
shared = ["META_API_KEY"]
```

| Name | Reaches | Line |
| :--- | :--- | :--- |
| `META_API_KEY` | every process: pi, the Muse CLI, every shell | `META_API_KEY: every process, shared by env.shared` |

If a later release ships a `meta` pack whose provider claims `META_API_KEY` and you select it, nothing
changes for you: the shared name still reaches every process, and the line gains the claimant,
`META_API_KEY (provider meta): every process, shared by env.shared` ([OQ-SE2](#OQ-SE2) A). Under
[OQ-SE2](#OQ-SE2) B the claim would win and the Muse CLI would lose the key the day you selected the
pack, which is today's behavior and the flip this design removes.

### 4.7 A secret that is no provider's: `TAVILY_API_KEY` for an MCP server

The dotenv holds `TAVILY_API_KEY`; claude's MCP table names a server with `env = { TAVILY_API_KEY =
"${TAVILY_API_KEY}" }` and `requires_env = ["TAVILY_API_KEY"]`.

| `[env] shared` | Reaches | What happens to the server |
| :--- | :--- | :--- |
| not listed | nobody | dropped by its `requires_env` check for every agent; the launch prints `TAVILY_API_KEY: withheld from every process — … To hand it to every process: yolo config add env.shared '"TAVILY_API_KEY"' --user` |
| `["TAVILY_API_KEY"]` | every process | the server starts under claude and pi alike; the launch prints `TAVILY_API_KEY: every process, shared by env.shared` |

There is no spelling for "claude and pi only" ([OQ-SE3](#OQ-SE3), leaning A). Today the key reaches
every process with no line, so nothing narrows for a user who shares it.

### 4.8 A key a shipped pack claims but you did not select, beside one nothing claims

`packs = ["claude"]`; the dotenv holds `CEREBRAS_API_KEY` and `FOO_TOKEN`.

| Name | Reaches | Line |
| :--- | :--- | :--- |
| `CEREBRAS_API_KEY` | nobody | `CEREBRAS_API_KEY: withheld from every process — no provider in this launch claims it and it is not shared. To use it as cerebras's key: yolo pack add cerebras. To hand it to every process instead: yolo config add env.shared '"CEREBRAS_API_KEY"' --user` |
| `FOO_TOKEN` | nobody | `FOO_TOKEN: withheld from every process — no provider claims it and it is not shared. To hand it to every process: yolo config add env.shared '"FOO_TOKEN"' --user` |

The two lines differ only in the first remedy clause. Reach is the same: the shipped set never decides
who receives a value ([SE-D4](#SE-D4)).

### 4.9 A value written inline under `[env] set`

```toml
[env]
files = ["~/.config/yolo-jail/secrets.env"]
set   = { DEBUG = "1", EDITOR_THEME = "dark" }
```

| Name | Reaches | Line |
| :--- | :--- | :--- |
| `DEBUG`, `EDITOR_THEME` | every process | none |

Unchanged from today. A value in the config file is plain by construction ([§3.2](#32-origin-what-hydration-must-now-keep)).
If you write `ZAI_API_KEY = "…"` inline, which the user guide tells you not to, it is claimed while zai
is selected and plain when it is not: this design protects only values you keep in a file.

### 4.10 A dotenv the workspace config lists, inside the workspace

```toml
# <workspace>/yolo-jail.local.toml (yours, not committed)
[env]
files = ["dev.env"]
```

`dev.env` sits in the workspace and holds `DATABASE_URL=postgres://…`.

| Name | Reaches | Line |
| :--- | :--- | :--- |
| `DATABASE_URL` | every process | none |

A workspace file is plain ([OQ-SE4](#OQ-SE4), leaning A): it must lie inside the workspace, which
every process in the jail can already read. The same file listed from your **user** config would be
a store and `DATABASE_URL` would be withheld until shared.

### 4.11 A dotenv the repository ships

```toml
# <workspace>/yolo-jail.toml (committed)
[env]
files = [".env"]
```

`.env` is in the repository and holds `DATABASE_URL` and `STRIPE_KEY=sk_test_…`.

| Name | Reaches | Line |
| :--- | :--- | :--- |
| `DATABASE_URL`, `STRIPE_KEY` | every process | none |

Same as [§4.10](#410-a-dotenv-the-workspace-config-lists-inside-the-workspace): plain. If the repository
instead writes `files = ["~/.config/yolo-jail/secrets.env"]`, the launch is refused today and stays
refused: a workspace config may not list a file outside the workspace, and the refusal names the move
to your user config. A workspace config carrying `shared = [...]` is refused the same way
([SE-D3](#SE-D3)).

### 4.12 Removing a name

```toml
[env]
files = ["~/.config/yolo-jail/secrets.env"]
unset = ["AWS_PROFILE"]
```

| Name | Reaches | Line |
| :--- | :--- | :--- |
| `AWS_PROFILE` | nobody; a pack's `env` value of that name is removed too | none |

Unchanged from today. A removal carries no value, so it is neither a store value nor a shared one;
listing `AWS_PROFILE` under `shared` as well changes nothing ([SE-D7](#SE-D7)).

### 4.13 `AWS_PROFILE` shared while `bedrock` is selected

`packs = ["claude", "bedrock"]`; the dotenv holds `AWS_PROFILE=dev` for `aws` and `terraform` in the
jail; claude runs on `bedrock` through SSO.

```toml
[env]
files  = ["~/.config/yolo-jail/secrets.env"]
shared = ["AWS_PROFILE"]
```

| Under | `AWS_PROFILE` reaches | Line |
| :--- | :--- | :--- |
| [OQ-SE2](#OQ-SE2) A (leaning) | every process: claude, `aws`, `terraform`, every shell | `AWS_PROFILE (provider bedrock): every process, shared by env.shared` |
| [OQ-SE2](#OQ-SE2) B | claude only; `aws` and `terraform` do without, as today | `AWS_PROFILE (provider bedrock): claude only`, and `yolo check` says the shared entry has no effect while a selected provider claims the name |

Without the `shared` line the outcome is today's: claude only, and the host's remedy is
`yolo host --with-credentials bedrock -- terraform`.

### 4.14 `bedrock` against `bedrock-key`, with the bearer in the store

`packs = ["claude", "bedrock"]`, where `packs/bedrock` ships both providers
([bedrock-key-provider.md](bedrock-key-provider.md)); `[allow.aws-auth] profile = "my-sso"`; the dotenv
holds `AWS_BEARER_TOKEN_BEDROCK=…`.

| You run | `AWS_BEARER_TOKEN_BEDROCK` reaches | The SSO pointer reaches | Line |
| :--- | :--- | :--- | :--- |
| `yolo -p claude=bedrock-key` | claude only | nobody; the aws-auth daemon is not started | `AWS_BEARER_TOKEN_BEDROCK (provider bedrock-key): claude only` |
| `yolo -p claude=bedrock` | nobody | claude only | `AWS_BEARER_TOKEN_BEDROCK (provider bedrock-key): withheld from every process — no agent in this launch selected it` |
| `yolo` with claude on its native login | nobody | nobody | the same withheld line |

The bearer file lives in your user config's `[env]` like every other key; nothing about it is a
special case. That is the answer this design gives [OQ-BK3](bedrock-key-provider.md#OQ-BK3).

### 4.15 Two agents on different providers in one launch

`packs = ["claude", "pi", "zai", "openrouter"]`; the dotenv holds `ZAI_API_KEY`, `OPENROUTER_API_KEY`
and `GH_TOKEN`; `shared = ["GH_TOKEN"]`. You run `yolo -p claude=zai -p pi=openrouter`.

| Name | Reaches | Written to |
| :--- | :--- | :--- |
| `ZAI_API_KEY` | claude only | `agent-env/claude.sh` |
| `OPENROUTER_API_KEY` | pi only | `agent-env/pi.sh` |
| `GH_TOKEN` | every process | `yolo-user-env.sh` |

```text
  ZAI_API_KEY (provider zai): claude only
  OPENROUTER_API_KEY (provider openrouter): pi only
  GH_TOKEN: every process, shared by env.shared
```

A bare shell in the jail holds `GH_TOKEN` and neither key. Unchanged from today for the two keys.

### 4.16 Handing a shell a provider's key for one launch: `--with-credentials`

The files of [§4.15](#415-two-agents-on-different-providers-in-one-launch). You run
`yolo --with-credentials zai -- bash`.

| Name | Reaches | Line |
| :--- | :--- | :--- |
| `ZAI_API_KEY` | every process in this jail, for its life | `ZAI_API_KEY (provider zai): every process in this jail, by its --with-credentials grant` |
| `OPENROUTER_API_KEY` | nobody | `… withheld from every process — no agent in this launch selected it` |
| `GH_TOKEN` | every process | `GH_TOKEN: every process, shared by env.shared` |

Unchanged from today. The grant names a **provider**, so it cannot hand a shell `GH_TOKEN` or
`CEREBRAS_API_KEY` while the cerebras pack is unselected; those take `shared`, or the pack.

### 4.17 The same cases at `yolo host`

`yolo host -- <cmd>` composes one process from your **user** config only, so [§4.10](#410-a-dotenv-the-workspace-config-lists-inside-the-workspace)
and [§4.11](#411-a-dotenv-the-repository-ships) do not apply there: a workspace entry is never read.
Everything else gives the same reach, with two differences in what is printed:

| Case | At the host |
| :--- | :--- |
| a withheld name your shell also exports (`export GH_TOKEN=…` before `yolo host`) | the command holds your shell's value; the line reads `GH_TOKEN: not added by yolo — … the invoking shell's own value passes through` and still names the share verb |
| `yolo host -p zai -- bash` | refused before anything runs, as today; the remedy is `yolo host --with-credentials zai -- bash`, which hands `bash` the key for that run |
| `yolo host -- claude` with claude on zai | `ZAI_API_KEY` reaches that one process; `GH_TOKEN` reaches it only if shared; the same lines as the jail's, on stderr |
| `eval "$(yolo host env --with-credentials zai)"` | exports the key into your current shell; a shared `GH_TOKEN` is exported too; an unshared one is not, and the line on stderr names the verb |

### 4.18 The same cases on macos-user

Every example above reads the same on macos-user. The vehicle differs, not the reach: the shared
names and the launched program's own ride the per-session file, every other agent's own names ride
that agent's env file, and a withheld name is written to neither.

## 5. Migration: the first launch after the change

A user with today's config and a dotenv holding `ZAI_API_KEY`, `GH_TOKEN` and `DEBUG` launches with
`packs = ["claude"]`. Under [OQ-SE1](#OQ-SE1)'s leaning the launch **runs**, and its disclosure reads:

```text
Credential scope: a value from your secret store reaches only the agents whose profile selects the
provider that claims it, or every process when you share it by name.
  ZAI_API_KEY: withheld from every process — no provider in this launch claims it and it is not
    shared. To use it as zai's key: yolo pack add zai. To hand it to every process instead:
    yolo config add env.shared '"ZAI_API_KEY"' --user
  GH_TOKEN, DEBUG: withheld from every process — no provider claims them and they are not shared.
    To hand them to every process: yolo config add env.shared '"GH_TOKEN"' '"DEBUG"' --user
```

Nothing is rewritten in the user's files; `yolo config add` is the user's own act. The lines repeat on
every launch until the names are shared, removed from the file, or claimed by a selected provider. A
disclosure is never suppressible ([OQ-RO3](../reference/report-tiers.md#why-its-this-way)), and there is no
`YOLO_ALLOW_*` hatch: the fix is one config command, and a hatch exists for broken user config, which this
is not.

**State that already exists.** On the first entry after the change the shared file is truncated and
rewritten, so a previously shared key leaves it; a jail already running keeps the environment its processes
hold until it restarts. Per-agent files are rewritten whole on every entry, as today.

## 6. Alternatives considered

| Alternative | Verdict |
| :--- | :--- |
| **A. Claims from every shipped pack, selected or not** | **Rejected as the rule**, kept as the remedy's wording ([§3.5](#35-the-remedy-line)). It still infers secrecy from a name list: a key for a provider yolo does not ship goes everywhere, and a release that ships a new pack changes reach in launches that never selected it. Reading the shipped set to **decide** also cuts against the selection-scoped rule the pack system keeps ([`AGENTS.md`](../../AGENTS.md#packs-and-what-core-does-not-know)) |
| **B. A second key, `credential_sources`, gated, beside a plain `env_sources`** | **Rejected**, as it was in [OQ-ES1](credential-sources-separation.md#10-decision-ledger): every user restates "this is a credential" by moving it, and the gated channel still needs a claim to route. This design is its inverse — the channel the guide already calls the secret one **is** the secret store — with the per-name `shared` list for what should travel |
| **C. The key named on the provider: `[providers.zai] credentials = "~/.config/yolo-jail/zai.env"`** | **Rejected.** The common habit is one file for every key, so this is N pointers to one file; AWS's pair and the `--with-credentials all` grant need the one store [OQ-ES5](credential-sources-separation.md#10-decision-ledger) ruled; and an MCP server's key has no provider to hang on |
| **D. Refuse a dotenv that holds a name some unselected shipped pack claims** | **Rejected as a refusal.** Its trigger is the shipped set, so a release could refuse a working config; the same fact is one clause of the remedy line |
| **E. A per-entry flag, `{ file = "x.env", shared = true }`** | **Rejected**, as the separation doc's alternative D was: a per-file flag in a per-name problem |
| **F. A per-name recipient list for non-provider secrets** | **Deferred** to [OQ-SE3](#OQ-SE3); the gate's per-agent delivery could carry it, and nobody has asked |
| **G. Today's behavior** | **Rejected** on the owner's words of 2026-11-09 |

## 7. Risks

| Risk | Mitigation |
| :--- | :--- |
| A user misses the withheld line and loses `gh` or an MCP server for an hour | The line prints on every launch and in `yolo check`; [OQ-SE1](#OQ-SE1) B makes it a refusal instead |
| `shared` becomes a dumping ground that re-creates today's shared file | Every shared name is disclosed on every launch, with its claimant when one exists; the cost is visible each time |
| A workspace config smuggles `shared` or an out-of-workspace file | Both refused by validation; the second already is ([OQ-AS3](../research/agent-safehouse.md#OQ-AS3)) |
| The three notches drift | The rule is in the gate's one function ([CN-D5](../reference/providers.md#CN-D5)); each notch's tests already pin the gate's result through its vehicle |
| Origin tracking is wrong for an include or a layer | Origin is assigned where the file is read, the same place relative paths are anchored per file ([`envsources.go:43-80`](../../internal/config/envsources.go#L43-L80)) |

## 8. What this does not license

- **No value in any line**, at any notch, in `launch.log` or `boot.log`; names and files only, as every
  existing disclosure ([CN-D16](../reference/providers.md#CN-D16)).
- **No widening by a release.** A new shipped pack may change a remedy's wording; it never changes who
  receives a value in a launch that does not select it.
- **No workspace-scope `shared`, and no workspace-scope key of any kind that reaches a user-scope value.**
- **No per-launch spelling for a shared name.** `--with-credentials` stays by provider; a non-provider
  secret is shared in config or not at all.
- **No grant from config** ([OQ-ES5](credential-sources-separation.md#10-decision-ledger) stands): a shared
  name is a fact about the variable, not a grant to a process.
- **Not access control within a jail's uid.** Every file a vehicle writes is readable by every process of
  that uid; the gate governs ambient environment, as
  [the separation doc says](credential-sources-separation.md#8-what-this-does-not-license).
- **No change to the credential pre-flight, to removals, to the launch-environment relay, or to the wire
  bridge's key read** ([CN-D11](../reference/providers.md#CN-D11)). Each reads the gate's answer.

## 9. Open Questions

1. 💬 **OQ-SE1: On the first launch after the change, does a withheld `GH_TOKEN` stop the launch or only print a line?**

   Priya has `GH_TOKEN=…` and `ZAI_API_KEY=…` in `~/.config/yolo-jail/secrets.env`, listed in her user
   config, and `packs = ["claude"]`. Today both reach every process and nothing is printed. After this
   change she runs `yolo` as usual. Both names are now in her secret store, nothing claims them and she has
   shared neither, so the gate withholds both. The jail either starts without them, with two lines naming
   the command that shares each, or refuses to start until she runs it.

   - **A — Start, and print the lines on every launch.** Her jail is up; `gh` fails later with its own
     message, and the launch output above it names `yolo config add env.shared '"GH_TOKEN"' --user`.
     Cost: a user who does not read the launch output loses time to a tool that quietly lacks its token.
   - **B — Refuse until every store name is claimed, shared or removed.** She sees the two lines at once
     and runs the command before anything else. Cost: a non-secret such as `DEBUG=1` kept in the file
     blocks the jail too, and the one working config she had last week refuses once.

   <!-- vantage: question id=OQ-SE1 leaning="A — withholding protects her already, and a refusal over a variable that is not a secret is heavier than the problem; the line names the command and repeats on every launch." -->

   _Leaning:_ A — withholding protects her already, and a refusal over a variable that is not a secret is
   heavier than the problem; the line names the command and repeats on every launch.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-SE2: When Matt shares a name a selected provider also claims, does every process get it?**

   Matt keeps `AWS_PROFILE=dev` in his secret store because `aws` and `terraform` in the jail read it.
   The `bedrock` provider claims that name, so today a jail with the bedrock pack withholds it from both
   tools and hands it only to an agent on a Bedrock profile; at the host the remedy is
   `yolo host --with-credentials bedrock -- terraform`, one run at a time. He adds
   `shared = ["AWS_PROFILE"]` to his user config. The gate has to say whether his list or the provider's
   claim wins the name.

   - **A — The shared name wins.** `aws`, `terraform` and every agent hold `AWS_PROFILE`; the launch prints
     `AWS_PROFILE (provider bedrock): every process, shared by env.shared` on every run. Cost: a
     deliberate, user-made exception to "nothing leaks" that an agent on another provider now sees; it is
     the user's own file and the line says so each time.
   - **B — The claim wins; `shared` covers only names no selected provider claims.** His list entry
     changes nothing and `yolo check` tells him so; the per-run grant stays the only way. Cost: the
     `AWS_PROFILE` problem stays open, and one list has two meanings depending on which packs are
     selected, which is the flip this design exists to remove.

   <!-- vantage: question id=OQ-SE2 leaning="A — one list, one meaning, disclosed every launch; it is what the sibling question on this exact case already leans to, and B re-creates a selection-dependent meaning." -->

   _Leaning:_ A — one list, one meaning, disclosed every launch; it is what the sibling question on this
   exact case ([OQ-ES6](credential-sources-separation.md#OQ-ES6)) already leans to, and B re-creates a
   selection-dependent meaning. A ruling here answers that question too.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 **OQ-SE3: Can a secret that is not a provider's be sent to two agents instead of everyone?**

   Dana keeps `TAVILY_API_KEY` in her secret store for an MCP server that claude and pi both launch. No
   provider claims it, so after this change the key reaches nobody until she shares it, and sharing it
   hands it to every shell and process in the jail, not just the two agents whose MCP tables name the
   server. Today it reaches everything anyway, so nothing narrows for her; the question is whether the new
   list should let her say "these agents".

   - **A — Every process or nobody.** She writes `shared = ["TAVILY_API_KEY"]`; one list, one meaning. Cost:
     a shell in the jail holds a key only two agents need, which is today's state.
   - **B — A recipient form beside the plain name**, such as `{ name = "TAVILY_API_KEY", to = ["claude",
     "pi"] }`. The key lands in those two agents' env files and no other. Cost: a second shape in the list,
     a validator for agent names, and a per-agent file for a value no profile selected, which the vehicles
     never write today.

   <!-- vantage: question id=OQ-SE3 leaning="A — nobody has asked, every file is readable by the whole uid anyway, and B can be added later without changing A's meaning." -->

   _Leaning:_ A — nobody has asked, every file is readable by the whole uid anyway, and B can be added later
   without changing A's meaning.

   **Answer:**
   > _(empty — fill in when decided)_

4. 💬 **OQ-SE4: Is a dotenv file the repository ships plain environment, or a secret store too?**

   Sam's project keeps `.env` with `DATABASE_URL` and a staging `STRIPE_KEY`, and `yolo-jail.toml` lists it
   under `[env] files`. yolo already refuses a workspace entry that points outside the workspace, so the file
   can hold only what the repository holds, which every process in the jail can read from disk anyway.
   Today every value in it reaches every process. The design has to say whether a file the project ships is
   plain by construction or a store like Sam's own.

   - **A — Plain.** Every value reaches every process, as today, with no line; a name a selected provider
     claims is still that provider's. Cost: a project-shipped AWS pair reaches pi's Bedrock support when no
     Bedrock profile is selected, as it does today.
   - **B — A store.** Each value is withheld until claimed or shared, and `shared` is user-scope only, so
     Sam names `DATABASE_URL` in his personal config for a project fact. Cost: the project's environment
     needs a per-user step, and the thing withheld is a file the agent can already open.

   <!-- vantage: question id=OQ-SE4 leaning="A — the workspace confinement already makes the file the project's; withholding what the jail can read from disk protects nothing and costs a step in the wrong file." -->

   _Leaning:_ A — the workspace confinement already makes the file the project's; withholding what the jail
   can read from disk protects nothing and costs a step in the wrong file.

   **Answer:**
   > _(empty — fill in when decided)_

## Decision Ledger

The seat's decisions, pending the owner's review. Each is a mechanism choice with one defensible answer.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="SE-D1"></a>SE-D1 | **Origin is assigned where a file is read**, beside the per-file anchoring of relative paths, and carried through the merge on each value, so the gate never re-reads a config to learn it | 2026-11-09 | [§3.2](#32-origin-what-hydration-must-now-keep) | — |
| <a id="SE-D2"></a>SE-D2 | **The rule lives in the gate's one function**, and the vehicles are untouched: a rule-3 value is simply absent from every set the gate hands them | 2026-11-09 | [§3.6](#36-what-each-notch-writes) | — |
| <a id="SE-D3"></a>SE-D3 | **`shared` is user scope only**, refused in a workspace config with the user-scope verb named | 2026-11-09 | [§3.4](#34-the-env-shared-list) | — |
| <a id="SE-D4"></a>SE-D4 | **The shipped set is read to word a remedy, never to decide a reach**; a release therefore changes text, never delivery | 2026-11-09 | [§3.5](#35-the-remedy-line) | — |
| <a id="SE-D5"></a>SE-D5 | **The disclosure gains two line kinds** — shared names, and store names withheld for want of a claim or a share — and keeps its names-only rule; a shared name is listed on every launch | 2026-11-09 | [§3.3](#33-the-three-outcomes) | — |
| <a id="SE-D6"></a>SE-D6 | **No hatch.** No `YOLO_ALLOW_*` variable restores today's delivery; the one remedy is the config verb | 2026-11-09 | [§5](#5-migration-the-first-launch-after-the-change) | — |
| <a id="SE-D7"></a>SE-D7 | **A shared name nothing sets is a `yolo check` warning, not a refusal**; a removal of a shared name wins the name silently | 2026-11-09 | [§3.4](#34-the-env-shared-list) | — |

## Appendix: what this doc did not verify

- The exact `yolo config add` spelling for a new list under `[env]`, and whether that verb accepts several
  values in one call as [§5](#5-migration-the-first-launch-after-the-change) shows; the planner confirms it
  against [`config-commands.md`](config-commands.md).
- That the assembled config holding inline values is 0644 — taken from
  [provider-reachability's ledger](provider-reachability-and-model-names.md#9-decision-ledger).
- That the MCP `requires_env` check reads a shared value from the boot environment on every backend;
  [CN-D19](../reference/providers.md#CN-D19) says per agent, and the shared file is sourced before the
  tables render, but this was not re-run.
- The macos-user and host rows of [§3.6](#36-what-each-notch-writes) are read from the code's comments and
  [`providers.md`](../reference/providers.md#the-credential-gate), not measured on a Mac.
