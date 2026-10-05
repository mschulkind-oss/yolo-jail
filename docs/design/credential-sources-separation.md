---
title: "Should credentials leave env_sources?"
date: 2026-09-27
status: in-review
stage: DESIGN
next: "Rule OQ-ES6 — whether a user may share a claimed generic name such as AWS_PROFILE with every process"
tags: [providers, profiles, credentials, env-sources, notches, cli]
summary: "A key listed in env_sources is withheld from `yolo host -- bash`, and the filing proposed a separate credential_sources key. Measured against the built credential gate: the surprise happens only for a claimed name that env_sources supplies, the host remedy (`yolo host -p <profile> -- <cmd>`) is already built but undocumented, and the split would revisit OQ-CN1. Five implementation decisions, now built, make the host remedy findable. OQ-ES5's host half is ruled (2026-09-27): an explicit `yolo host --with-credentials <provider…|all>` grant, keys only. Its jail half is ruled (2026-10-05) and built: the same flag at a jail launch, at every notch, the jail holding exactly the set it was launched with (ES-D31 to ES-D36). One question stays open: shared generic names such as AWS_PROFILE. The split itself (OQ-ES1) is answered no by OQ-ES5's own ruling, and aws-auth's pointer under a typed -p (OQ-ES7) is moot, since -p reaches agent CLIs only."
vantage:
  status-chip: true
---

# Should credentials leave `env_sources`?

**Status:** 2026-09-27, filed at `9ebbb659` and corrected the same day against the built
gate (`b8759598`). Evidence verified at `8da7840d`, by scratch cells driving `hostMain` as
`internal/cli`'s `TestHostGate*` cells do. **ES-D1 to ES-D5 are BUILT** (2026-09-27; the cells
are `internal/cli/hostcredentialgrant_test.go`), with the mechanism
choices recorded as ES-D6 to ES-D12 in the [ledger](#10-decision-ledger). A review the same day
found the built remedy naming `-p` launches the named agent refuses, and a `yolo host env` shell
spelling that exported the agent's whole provider shape; ES-D7 and ES-D8 are amended, and ES-D10
to ES-D12 record the fixes (`hostComposition.credentialRemedy`'s shell spelling,
`hostComposition.runsOn`, `hostComposition.remedyAction` and `DisclosureNotes.Composed`, each
with its cell in that file). **[OQ-ES5](#OQ-ES5)'s host half was ruled
by the maintainer later the same day**: an explicit `yolo host --with-credentials
<provider[,provider...]|all> -- <cmd>` grant, keys only, host only. It is BUILT
([§5.1](#51-the-explicit-grant---with-credentials-built), ES-D13 to ES-D17). Its "host only" is
superseded: **the jail half was ruled 2026-10-05 and is BUILT**
([§5.2](#52-the-jail-half---with-credentials-at-a-jail-launch-built), ES-D31 to ES-D36), so a jail
launch takes the flag at every notch and ES-D17's refusal is retired. The same day,
[ES-D18](#10-decision-ledger) stopped `yolo host` composing the in-jail wire bridge's adapter
address, which ES-D10 had found it pointing claude at. A review the same day found gaps in that
work, fixed as the ledger's [ES-D19](#10-decision-ledger) onward. [OQ-ES6](#OQ-ES6) is still
open. Triaged 2026-09-30: [OQ-ES1](#OQ-ES1) is answered no by
[OQ-ES5](#OQ-ES5)'s own ruling, which keeps `env_sources` the one store, so
[OQ-ES2](#OQ-ES2) and [OQ-ES4](#OQ-ES4) are moot, as each said they would be. [OQ-ES7](#OQ-ES7)
is moot since [OQ-NC5](../plans/notch-convergence.md#OQ-NC5). **ES-D1 is retired**
(2026-09-28): [notch-convergence OQ-NC5](../plans/notch-convergence.md#OQ-NC5) ruled that a bare
`-p` reaches agent CLIs only at every notch, so `yolo host -p <profile> -- <cmd>` for a command no
selected pack installs is refused, naming `--with-credentials`, which is now an ad-hoc command's
one grant. The remedies ES-D2, ES-D7 and ES-D10 name for an ad-hoc command, and the help ES-D3
wrote, are amended to match ([NC-D62](../plans/notch-convergence.md#NC-D62)). The `-p` spellings
below, for an ad-hoc command, are the design as it was built on 2026-09-27.

> **In short.** The gate withholds a claimed name only when `env_sources` supplies it, and the
> host has had a per-command remedy since the gate shipped: `yolo host -p <profile> -- <cmd>`. A
> separate credentials key would still route by each provider's claim on the name, so it revisits
> [OQ-CN1](../reference/providers.md#oq-cn1) instead of replacing it.

**Why it matters.** Nobody could find the remedy: the warning named none, and `--help` said `-p`
was for agents (fixed by ES-D2 and ES-D3). And the same rule takes `AWS_PROFILE` from `terraform` whenever the claude pack is
selected ([§3.4](#34-the-case-the-filing-missed-claimed-generic-names)).

**The shape.** One `env_sources` channel, classified by name as built
([CN-D6](provider-credential-scope.md#CN-D6)); five host decisions that pin and
surface the existing grant; three new questions the filing's cases lead to.

**Cost.** None on the recommended path. The split adds a key, a migration, and a refusal that
fires on `AWS_PROFILE` for every claude-pack user.

**Start at [§3](#3-where-the-surprise-is-by-spelling-and-by-notch)**; for the host-only answer,
[§5](#5-the-host-half-is-built-what-shipping-it-takes), and for the ruled multi-provider grant,
[§5.1](#51-the-explicit-grant---with-credentials-built).

**Needs your ruling:** [OQ-ES6](#OQ-ES6), which asks for an exception to
[OQ-BR4](../reference/providers.md#oq-br4). [OQ-ES5](#OQ-ES5) is ruled at both halves, and both are
built ([§5.1](#51-the-explicit-grant---with-credentials-built) and
[§5.2](#52-the-jail-half---with-credentials-at-a-jail-launch-built)).

**Reads with:** [`credential-sources-separation-plan.md`](credential-sources-separation-plan.md)
(the sketch; its host section is built), [`providers.md`'s credential gate](../reference/providers.md#the-credential-gate)
(the gate's design, rulings and implementation decisions),
[`providers.md`](../reference/providers.md#the-credential-gate) (the gate as built),
[`host-agent-environment.md`](../reference/host-agent-environment.md) (the host's composition).

---

## Words this doc uses

- **`env_sources`**. The config key that hydrates host-side values from dotenv files and inline
  maps into a launch's environment. A dotenv file is one kind of entry
  (`config.ResolveEnvSourcesFull`). `yolo host` reads only the user-scope entries
  (`config.UserScopeConfigOrEmpty`). A workspace config never reaches it.
- **Claimed name.** A variable that some **composed** provider lists in its `api_key_env_name`
  (`credentialClaims`). A composed provider is either a selected pack's `kind: "provider"` or one
  of the user's own `providers` entries (`packload.ComposeProviders`). A claim does not need any
  profile to select the provider. That is why the incident lists four providers, none of them
  selected.
- **The credential gate.** `packload.ScopeCredentials`. It decides which process receives which
  composed value, and it writes nothing (its header says so). Where each answer lands depends on
  the vehicle:
  - container backends: per-agent files, `~/.config/yolo-agent-env/<agent>.sh`, which
    `deliverChannel` writes;
  - macos-user: the one launched program's session
    ([CN-D12](provider-credential-scope.md#CN-D12));
  - the host notch: the one exec'd process (`composeHostVars`).
- **The shared file.** `~/.config/yolo-user-env.sh` in a container jail. It carries only the
  unclaimed `env_sources` values (`SharedEnvSources`). Its readers are:
  - the `.bashrc` line (`internal/entrypoint/shell.go`);
  - the container command's `miseActivate` (`internal/cli/run/command.go`);
  - the entrypoint (`hydrateEnvFromUserEnvFile`, `execBash`);
  - the wire bridge (`resolveKey`).
- **Ad-hoc command** *(coined here)*. A command that no selected pack installs, such as `bash`,
  `curl` or `terraform`. It is not an agent, so no pack's env derive runs for it.
- **Grant** *(coined here)*. A per-invocation selection that hands a provider's claimed values to
  what that invocation starts: at the host, the one command (a recipient of the gate); at a jail
  launch, every process of the jail it starts, for the jail's life (ES-D31). It is not a config
  setting, and it does not turn off the gate.
- **Notch**. A place where yolo renders an agent's environment: the jail, `yolo host`, and
  `guest`, which is not built yet
  ([`providers.md`'s credential gate](../reference/providers.md#the-credential-gate)).

## 1. What happened, precisely

On 2026-09-27:

```console
$ yolo host -- bash -c 'echo $ZAI_API_KEY'
yolo-jail 0.10.0+653.gd6f875df | linux/x86_64 | host
yolo host: Credential scope: a provider's credential reaches only the agents whose profile selects it.
  DEEPSEEK_API_KEY (provider deepseek): withheld from every process — no agent in this launch selected it
  OPENROUTER_API_KEY (provider openrouter): withheld from every process — no agent in this launch selected it
  ZAI_API_KEY (provider zai): withheld from every process — no agent in this launch selected it
  CEREBRAS_API_KEY (provider cerebras): withheld from every process — no agent in this launch selected it
```

The command printed an empty line. The transcript supports four conclusions. The run itself cannot
be reproduced from the repository, but the probes reproduce its shape.

1. **`env_sources` was the key's only source.** Had the invoking shell exported `ZAI_API_KEY`,
   `bash` would have printed it. The shell `yolo host` inherits passes through untouched
   ([CN-D13](provider-credential-scope.md#CN-D13), `hostComposition.environ`), and the
   probe printed the same "withheld" line over it
   ([§3.5](#35-what-the-disclosure-says-and-what-it-gets-wrong)).
2. **Nothing keyed `bash`.** There was no `-p` and no `use_profiles` entry for `bash`, so `bash`
   received only the unclaimed values (`EnvSourcesFor`).
3. **The user declared a deepseek provider.** No `packs/*/pack.json` declares `deepseek`, so the
   user's own `providers` entry claimed `DEEPSEEK_API_KEY`. zai, openrouter and cerebras are
   shipped packs.
4. **The disclosure is five lines.** It is the rule line plus one line per group of names that
   share a claimant and recipients (`CredentialScope.Disclosure`,
   [CN-D16](provider-credential-scope.md#CN-D16)).

What the maintainer said:

> *"I find it confusing that you can add something to the environment sources of a jail
> and then start up the jail and then it's not there because it's been filtered out.
> I think instead we should move credentials into their own files that are very clearly
> marked as credentials, basically the same thing as environment sources, but it'll be
> clear that they're credentials and not expected to all make it into the environment."*

## 2. Principles

- **P1. What a user lists under a key named for the environment should reach the
  environment.** This is the filing's premise and the argument for the split. It collides with
  [OQ-CN1](../reference/providers.md#oq-cn1)'s ruling, and [OQ-ES1](#OQ-ES1) is where the two
  are weighed.
- **P2. Nothing leaks** ([OQ-BR4](../reference/providers.md#oq-br4), ruled 2026-09-25):
  *"certainly not Claude Code gets Bedrock"* because another agent selected it. Any change here
  keeps a claimed name out of every process whose profile did not select its provider, unless the
  user explicitly asks for it.
- **P3. A narrowing is said, and an acknowledgment is explicit.** Two rules bind here:
  - The gate discloses on every arm
    ([CN-D16](provider-credential-scope.md#CN-D16), and "No silent narrowing" in
    [what the credential gate does not do](../reference/providers.md#what-the-credential-gate-does-not-do)),
    and a disclosure is never suppressible
    ([`OQ-RO3`](../reference/report-tiers.md#why-its-this-way)).
  - An override has to be acknowledged in as many words, and it *"shouldn't just silently ride
    along"* ([OQ-SK1](attach-skew-and-contract-guardrails.md#OQ-SK1), 2026-09-26).
- **P4. A value no provider claims reaches every process**
  ([CN-D3](provider-credential-scope.md#CN-D3)). The catch is that "claimed" is not
  the same as "secret". bedrock's claim list includes `AWS_PROFILE`, which is a profile name
  ([CN-D2](provider-credential-scope.md#CN-D2),
  [§3.4](#34-the-case-the-filing-missed-claimed-generic-names)).

## 3. Where the surprise is, by spelling and by notch

The surprise depends on **where the key came from** and **which notch runs the command**. The
filing treated it as one case. It is several cases, and only one of them matches the incident.

### 3.1 At the host

Every cell is measured, with packs `claude`, `pi` and `zai` and an unclaimed `PORT` beside the
key. In every cell, `PORT` reached `bash`.

| Where `ZAI_API_KEY` is | `yolo host -- bash` | `yolo host -p zai -- bash` |
| :--- | :--- | :--- |
| `env_sources` only (the incident) | absent. Disclosed `withheld from every process` | **delivered**. Disclosed `ZAI_API_KEY (provider zai): bash only` |
| exported in the invoking shell only | **present**, from the shell. No disclosure line | present, from the shell. No disclosure line |
| both | **present**, from the shell. Disclosed `withheld from every process` at `8da7840d`, which was wrong; since ES-D4, `not added by yolo …`, the shell's value passing through | delivered: the `env_sources` value beats the shell's. Disclosed `bash only` |

The middle column is the part the filing missed. `-p` is not agent-only at the host.
`effectiveHostProfiles` keys the one-agent table by the launched command's basename, whatever that
command is. `ScopeCredentials` then builds an `AgentDelivery` for every key in that table, with no
check that a pack installs the name. `AgentEnv` returns nothing for a binary no pack owns
(`binOwner`), so no pack code runs. The `gateFiresFor` comment already states this reliance.

The host also has two narrower facts:

- **`-p` takes a profile name, not a provider name** (`packload.DeclaredProfileNames`). Every
  shipped provider ships a same-named profile, so `-p zai`, `-p openrouter`, `-p cerebras`,
  `-p kilo`, `-p llamacpp` and `-p bedrock` all work. A provider that the user declares under
  `providers` also needs a one-line `profiles` entry before `-p` can name it. Without one, the
  launch refuses with `no profile named … is declared`.
- **The grant is narrower than a selecting agent's delivery.** It carries the claimed
  `env_sources` values only. There are no shape variables, because no derive runs for `bash`. A
  pack that installs no CLI (**CLI-less**) contributes no gated env either, because
  `gateFiresFor` fires only for a basename that a selected pack installs
  ([CN-D4](provider-credential-scope.md#CN-D4)). So `yolo host -p bedrock -- bash`
  receives the static AWS pair, but not aws-auth's `AWS_CONTAINER_CREDENTIALS_FULL_URI`
  (measured). [OQ-ES7](#OQ-ES7) asked whether that should change. It is moot: since
  [OQ-NC5](../plans/notch-convergence.md#OQ-NC5), a typed `-p` for such a command is refused.

To put the key in the current shell instead, `eval "$(yolo host env --agent bash -p zai)"` prints
`export ZAI_API_KEY=…`, with the disclosure on stderr (`hostEnv`, `hostEnvDelta`). `--agent`
defaults to `claude`, so the plain `yolo host env` withholds the zai key.

### 3.2 In the jail

This section is read from the code and from
[`providers.md`](../reference/providers.md#the-credential-gate). It was not measured here.

| Where `ZAI_API_KEY` is | bare shell (`yolo -- bash`) | an agent whose profile selects zai | a child of that agent |
| :--- | :--- | :--- | :--- |
| `env_sources` | absent. The shared file carries unclaimed values only (`deliverChannel` writes `SharedEnvSources`) | present, from its per-agent file | present, by inheritance ([CN-D8](provider-credential-scope.md#CN-D8)) |
| the host shell that ran `yolo` | absent. It never crosses as a raw variable | only what its env derive composes from it (`ScopeInput.Fallback`, [CN-D6](provider-credential-scope.md#CN-D6)) | as its parent |

**The jail has no grant for a shell.** `-p bash=zai` is refused (`(*Options).checkProfileTargets`:
`no pack installs a CLI named "bash"`). A bare `-p` never keys the `--` command. The 2026-09-03
ruling, restated at that function on 2026-09-19, is that a profile selects providers "for THE
AGENTS IN THE JAIL". The only in-jail route is to run `. ~/.config/yolo-agent-env/<agent>.sh`
by hand, and that works only when some agent selected the provider in this entry. The files are
readable by every process of the jail's uid, so scoping here governs a process's **ambient
environment** and is not access control. [OQ-ES5](#OQ-ES5) asked whether the jail should get a
grant. *Since 2026-10-05 it has one:* `yolo --with-credentials zai -- bash` launches a jail whose
every process holds zai's claimed values
([§5.2](#52-the-jail-half---with-credentials-at-a-jail-launch-built)).

### 3.3 On macos-user

Delivery is per launch, keyed by the basename of argv[0]
([CN-D12](provider-credential-scope.md#CN-D12), `(*packChannel).launchEnv`). So
`yolo -- zsh` gets the shared values only. An agent started from that shell used to get none of its
profile's values either; that was [OQ-CN9](../reference/providers.md#oq-cn9), ruled and built
2026-09-28 (`949d9430`): macos-user now writes each profiled agent's env file, which that agent's
launcher sources. There was no grant for an ad-hoc command here either until 2026-10-05; since
then `--with-credentials` hands one to the sandbox session
([§5.2](#52-the-jail-half---with-credentials-at-a-jail-launch-built)).

### 3.4 The case the filing missed: claimed generic names

The `bedrock` provider claims `AWS_BEARER_TOKEN_BEDROCK`, `AWS_ACCESS_KEY_ID`,
`AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN`, `AWS_PROFILE` and
`AWS_CONTAINER_CREDENTIALS_FULL_URI` ([CN-D2](provider-credential-scope.md#CN-D2)).
It was declared in `packs/claude` when this was measured; since 2026-09-29 it is `packs/bedrock`'s
([`bedrock-plumbing.md` BR-D15](bedrock-plumbing.md#BR-D15)), which `packs/claude` names in its
`needs` unconditionally, so the claude pack alone still selects it. With only `"packs": ["claude"]` and `AWS_PROFILE` plus an access key in `env_sources`,
`yolo host -- terraform` receives neither, and the launch discloses
`AWS_PROFILE, AWS_ACCESS_KEY_ID (provider bedrock): withheld from every process` (measured). The
same rule keeps them out of the jail's shared file (`SharedEnvSources`, read from the code), so
`aws` and `terraform` in a jail shell lose them too.

The gate cannot tell "AWS keys for Bedrock" from "AWS keys for terraform", because both use the
same variable names. **This is a sharper surprise than the incident**, because the user declared
no AWS provider and selected no Bedrock profile. Selecting the claude pack is enough.
`yolo host -p bedrock -- terraform` is a remedy for one command. [OQ-ES6](#OQ-ES6) asks whether a
standing one is wanted.

### 3.5 What the disclosure says, and what it gets wrong

The disclosure is not silent. It is printed at `yolo host --` and on stderr at `yolo host env`,
and on every jail entry ([CN-D16](provider-credential-scope.md#CN-D16)). At
`8da7840d` it had two defects, both in the built gate rather than in the design. At the host both
are fixed, by ES-D2 and ES-D4. The jail's line changes only for a jail's own grant, whose names
it reports as every process's ([ES-D31](#10-decision-ledger)):

- **It named no remedy.** "no agent in this launch selected it" gave no way to get the value back,
  even at the host, where `-p` is one flag away. `hostUsage` made this worse: it described `-p`
  as a preset "for the wrapped agent". At the host the line now names the typed `-p`
  ([ES-D2](#10-decision-ledger)), and the help describes it ([ES-D3](#10-decision-ledger)).
- **It misreported what the host notch does when the shell holds the same name.**
  `CredentialScope.Disclosure` read only the hydrated `env_sources`, never the inherited
  environment. When the invoking shell also exported a withheld name, the line said
  "withheld from every process" while the exec'd process held the shell's value (the table in
  [§3.1](#31-at-the-host)). The host now says the name was not added by yolo
  ([ES-D4](#10-decision-ledger)), and a name yolo set from another source, such as a pack's
  `env`, is said to be held from that source ([ES-D12](#10-decision-ledger)). The jail has no
  shell case, because a host shell's value never crosses raw.

## 4. The filed proposal: a key of its own for credentials

### 4.1 What it proposes

The filing proposes three changes:

1. **Split the key.** `env_sources` stays for general variables and is delivered to every
   process. A new `credential_sources` key, which accepts the same entries, is delivered only to
   agents whose profile selects the claiming provider.
2. **Refuse claimed names in `env_sources`.** A claimed name found there is refused, not withheld
   ([OQ-ES2](#OQ-ES2)).
3. **Add a host grant** so that `bash` can ask for credentials. That grant exists already:
   [§5](#5-the-host-half-is-built-what-shipping-it-takes).

### 4.2 What it would revisit

The split asks you to revisit gate rulings, which you should know before ruling
[OQ-ES1](#OQ-ES1):

- **[OQ-CN1](../reference/providers.md#oq-cn1), ruled 2026-09-26.** *"The key name is a fact
  about the provider … a per-profile allowlist in user config … puts a fact about zai in every
  user's file."* Under the split, every user restates "this is a credential" by moving it to a
  file, which is a coarser form of the rejected alternative. The split also still needs the claim
  to route. The filed design delivers `credential_sources` "only to agents whose active profile
  selects the claiming provider", which is the [OQ-CN1](../reference/providers.md#oq-cn1) claim.
- **[CN-D6](provider-credential-scope.md#CN-D6).** "The gate classifies NAMES, not
  sources." The split classifies by source. It is also silent on the channel CN-D6 exists for:
  the launch environment, `ScopeInput.Fallback`, can be classified only by name.
- **[CN-D3](provider-credential-scope.md#CN-D3).** The split's `env_sources` would
  behave as CN-D3's unclaimed values already do. The only change in behavior is
  [OQ-ES2](#OQ-ES2)'s refusal.

**A name no provider claims cannot be routed.** The filed example
`{ "CUSTOM_KEY": "secret-value" }` has no claimant, so the gate cannot tell which agent should
receive it. The split would have to invent a rule for it: every agent, which is CN-D3; none; or a
per-name recipient list, which is [OQ-CN1](../reference/providers.md#oq-cn1)'s rejected allowlist.

### 4.3 What it costs that the filing did not count

- **The refusal would hit ordinary AWS configuration.** Under [OQ-ES2](#OQ-ES2)'s filed leaning,
  a claimed name in `env_sources` is refused. That makes `AWS_PROFILE=dev` a refusal for every user of the
  claude pack ([§3.4](#34-the-case-the-filing-missed-claimed-generic-names)).
- **The refusal would depend on which packs are selected.** Claims come from the composed
  providers, so with no zai pack, `ZAI_API_KEY` is unclaimed and passes (measured). Adding `zai`
  to `packs` would turn a working launch into a refusal.
- **The filed examples are wrong.** `ANTHROPIC_API_KEY` and `OPENAI_API_KEY` are claimed by no
  shipped provider (`api_key_env_name` across `packs/*/pack.json`: zai, openrouter, cerebras,
  kilo, and bedrock's six). The refusal would never fire on them, and today they reach every
  process ([CN-D3](provider-credential-scope.md#CN-D3)).
- **The host needs its own check.** `yolo host` never runs `ValidateConfig` (`composeHostVars`
  reads `config.UserScopeConfigOrEmpty`). A refusal therefore needs a separate pre-flight at the
  host, and it can never see a workspace entry.
- **Other readers depend on the claimed name.** The wire bridge reads a served agent's key from
  that agent's file, then from the shared file
  ([CN-D11](provider-credential-scope.md#CN-D11)). MCP `requires_env` is asked per
  agent ([CN-D19](provider-credential-scope.md#CN-D19)). Any new channel has to feed
  both, and the refusal would refuse an MCP server's claimed key.
- **macos-user gets a third vehicle.** The filing designs only the host and container paths.
  [OQ-CN5](../reference/providers.md#oq-cn5) ruled that all three vehicles ship together.

## 5. The host half is built: what shipping it takes

**The host-only answer:** `yolo host -p zai -- bash -c 'echo $ZAI_API_KEY'` prints the key at
`8da7840d`, and `eval "$(yolo host env --agent bash -p zai)"` puts it in the current shell.
Nothing needs a ruling. What was missing was a test, a help line, two disclosure wordings and
one refusal. All five are recorded as implementation decisions, and all five are built:

| | What | Why it has one answer |
| :--- | :--- | :--- |
| **ES-D1** | `yolo host -p <profile> -- <cmd>` **is** the host's grant, for any command. Pin it through `hostMain` with a non-agent basename. The cells assert three things: the claimed values arrive; `PORT` still arrives; the line reads `… : bash only`. They must fail if `ScopeCredentials`' agent loop gains an "a pack installs this name" check, or if `effectiveHostProfiles` stops keying the launched basename | It is already the behavior. `hostcredentialgate_test.go` pins pi, codex and claude and no non-agent, so nothing stops it being lost |
| **ES-D2** | At the host (`yolo host --` and `yolo host env`), each "withheld" line names the remedy: `yolo host -p <profile> -- <cmd>`. The profile named is a declared one that resolves to the claiming provider, and one the named command can run on ([ES-D10](#10-decision-ledger)); if there is none, the line says to declare one. The wording is the implementer's. The jail's line is unchanged until [OQ-ES5](#OQ-ES5) decides whether a shell has a remedy there | "No silent narrowing" already requires the disclosure; naming the one existing remedy is the only way to make it actionable |
| **ES-D3** | `hostUsage` describes `-p` as applying to the wrapped **command**, and says an ad-hoc command then receives that profile's claimed `env_sources` values. The host-notch bullet in [`providers.md`](../reference/providers.md#the-credential-gate) and [`host-agent-environment.md`](../reference/host-agent-environment.md) say the same | The help text describes the behavior wrongly today |
| **ES-D4** | At the host, a withheld name that the invoking shell also holds is disclosed as not added by yolo, with the shell's own value passing through. It is never disclosed as "withheld". The wording is the implementer's | The line is false today ([§3.5](#35-what-the-disclosure-says-and-what-it-gets-wrong)); [CN-D13](provider-credential-scope.md#CN-D13) fixes the behavior, and the disclosure has to match it |
| **ES-D5** | Only a typed `-p` keys a command that no resolvable pack installs. A `use_profiles` entry for such a name is refused at `yolo host --` too, with `validateProfile`'s message plus the `-p` spelling. The rule is the validator's own, so a key for an unselected shipped pack's CLI passes at both, as it always has ([ES-D9](#10-decision-ledger)). `yolo check` and every jail launch reading the same user file already refuse it (`unknownProfileCLIMessage`) | Today the host accepts it only because it skips validation (measured: `use_profiles: {"bash": "zai"}` delivers at the host). Two notches disagreeing about one user file is a defect, and the validator's rule stands |

**The notches still differed, and ES-D1 said so.** At the host, `-p` keyed the `--` command. In the
jail it never does ([§3.2](#32-in-the-jail)). The host runs exactly one process, so keying that
process's basename was the only meaning a host `-p` could have. The jail's 2026-09-03 ruling is about
agents sharing one container, and it stands. *Superseded 2026-09-28:* once `--with-credentials`
existed, a second, implicit route for the same grant was a duplicate, and
[OQ-NC5](../plans/notch-convergence.md#OQ-NC5) gave the host the jail's meaning. A host `-p` for a
command no selected pack installs is now refused, naming the grant.

**What done looks like** (all seven are met, each by a cell in
`internal/cli/hostcredentialgrant_test.go`, and each cell was checked to fail when its production
call site is removed):

1. `yolo host -p zai -- bash -c 'echo $ZAI_API_KEY'` prints the key, and stderr reads
   `ZAI_API_KEY (provider zai): bash only`.
2. With the key only in `env_sources`, `yolo host -- bash -c 'echo $ZAI_API_KEY'` prints nothing,
   and the disclosure names `yolo host -p zai -- …`.
3. With the key also exported in the invoking shell, no line claims it was withheld.
4. `yolo host --help` describes `-p` as applying to any wrapped command.
5. `use_profiles: {"bash": "zai"}` refuses `yolo host -- bash` with the validator's message and
   the `-p` spelling.
6. Deleting the host's `ScopeCredentials` call, or its `effectiveHostProfiles` keying, fails a
   cell.
7. Every command a withheld line names runs. With `"packs": ["claude", "cerebras"]`,
   `yolo host -- claude` names `yolo host -p cerebras -- bash`, never a claude launch that
   refuses on the protocol pairing ([ES-D10](#10-decision-ledger)).

### 5.1 The explicit grant: `--with-credentials`, built

[OQ-ES5](#OQ-ES5)'s host half is ruled, and it is built as ruled. The case is a command that
needs many providers' keys at once, such as a usage bar that pings every subscription. `-p`
names one profile, so it cannot serve that case.

With packs `claude`, `zai` and `cerebras` and both keys in `env_sources` (measured through
`hostMain`, as the cells run it):

```console
$ yolo host --with-credentials all -- usage-bar
yolo host: Credential scope: a provider's credential reaches only the processes whose profile selects it or whose --with-credentials grant names it.
  ZAI_API_KEY (provider zai): usage-bar only
  CEREBRAS_API_KEY (provider cerebras): usage-bar only
yolo host: Credential grant (--with-credentials all): usage-bar receives the granted providers' claimed env_sources values, keys only — the grant selects no profile and re-points nothing, and every process usage-bar starts inherits them
  cerebras: CEREBRAS_API_KEY
  zai: ZAI_API_KEY
```

The grant is a **recipient of the gate**, not a second path around it
([ES-D13](#10-decision-ledger)). The flag's grammar is
[ES-D14](#10-decision-ledger), its disclosure [ES-D15](#10-decision-ledger), `yolo host env`'s
default slice under it [ES-D16](#10-decision-ledger), and the jail's refusal
[ES-D17](#10-decision-ledger), retired on 2026-10-05 by the jail half
([§5.2](#52-the-jail-half---with-credentials-at-a-jail-launch-built)).

**What done looks like.** All nine are met, each by a cell in
`internal/cli/hostwithcredentials_test.go`, `internal/cli/hostonlyflags_test.go` or
`internal/packload/credentialgrant_test.go`. Each cell was checked to fail when its production
call site is removed.

1. `yolo host --with-credentials zai,cerebras -- bash` hands `bash` both keys and the unclaimed
   values, and nothing of a provider it did not name.
2. Granted to `claude`, zai's key arrives, and none of the zai profile's shape does: no
   `ANTHROPIC_BASE_URL`, and no `ANTHROPIC_AUTH_TOKEN`.
3. `all` grants every provider that claims a value env_sources holds, and no other.
4. The grant is disclosed on every run, by name, including a grant that delivers nothing. No
   value is ever printed.
5. A named provider env_sources holds no value for is reported with the names it claims.
6. An unknown provider refuses before the exec, naming every known provider.
7. `-p bedrock --with-credentials zai -- claude` keeps claude's bedrock shape and keys and adds
   zai's key.
8. Without the typed flag, nothing grants: `-p`, `use_profiles`, and every `YOLO_ALLOW_*` variable
   set together leave an unnamed provider's key withheld and print no grant line.
9. `eval "$(yolo host env --with-credentials all)"` exports the keys and none of an agent's shape.
   ~~A jail launch given the flag exits 2, naming that it is host-only and the host spelling.~~
   *Superseded 2026-10-05:* a jail launch takes the flag
   ([§5.2](#52-the-jail-half---with-credentials-at-a-jail-launch-built)).

### 5.2 The jail half: `--with-credentials` at a jail launch, built

[OQ-ES5](#OQ-ES5)'s jail half was ruled in review on 2026-10-05, and it is built as ruled. The
maintainer: *"The jail shouldn't be able to discover credentials from outside that it wasn't
launched with. But you should be able to run a jail with whatever set of credentials you want.
Like that should be the same."* So the host's flag works at a jail launch, read by the host's own
resolver ([ES-D34](#10-decision-ledger)), and the set is fixed when the jail is launched: the jail
holds exactly the credentials it was launched with and fetches no others later.

With packs `zai` and `cerebras` and both keys in `env_sources`, a podman launch prints (the text
`TestAPodmanJailGrantNamesKeysOnTheArgvAndHandsValuesToTheClient` asserts, driving a whole launch to
its keeper's plan):

```console
$ yolo --with-credentials zai -- bash
Credential scope: a provider's credential reaches only the processes whose profile selects it or whose --with-credentials grant names it.
  ZAI_API_KEY (provider zai): every process in this jail, by its --with-credentials grant
  CEREBRAS_API_KEY (provider cerebras): withheld from every process — no agent in this launch selected it
Credential grant (--with-credentials zai): this jail holds the granted providers' claimed env_sources values for its whole life, keys only — the grant selects no profile and re-points nothing — and every process in it inherits them: this session, every session attached to it later, and everything each one starts
  zai: ZAI_API_KEY
```

**What each notch does now.** The grant is held by the jail, not by one process
([ES-D31](#10-decision-ledger)), and each notch carries it in a vehicle no workspace file is
([ES-D32](#10-decision-ledger)):

| Notch | Who holds the grant | How the values cross | A later entry |
| :--- | :--- | :--- | :--- |
| podman, on Linux or a Mac's machine | every process in the jail | each name as a bare `-e NAME` on the container argv, the value in the environment of the one runtime client the keeper starts the container with | holds the jail's set; one asking for more is refused ([ES-D33](#10-decision-ledger)) |
| Apple Container | the same | the same argv | the same |
| macos-user | every process of the one sandbox session | the root-owned per-session env file, never the per-agent files under `<workspace>/.yolo/home` | none: every invocation is its own session, with its own grant |
| `yolo host` | the one command ([§5.1](#51-the-explicit-grant---with-credentials-built)) | its exec environment | — |

**What done looks like.** All nine are met, each by a cell in
`internal/cli/run/jailgrant_test.go` or `internal/cli/jailwithcredentials_test.go`, and each cell
was checked to fail when its production call site is removed.

1. `yolo run --with-credentials zai,cerebras -- bash` reaches the launch with the names, in the
   host's grammar; an empty element refuses, exit 2; nothing says host-only any more
   (`TestJailLaunchTakesWithCredentials`, `TestJailLaunchRefusesAnEmptyGrantValue`).
2. An unknown provider refuses before either backend starts, naming the known ones; a named provider
   with no value is reported; `all` is every provider claiming a value
   (`TestAJailGrantResolvesAsTheHostsDoes`).
3. On podman the argv carries `-e ZAI_API_KEY` and no value anywhere, the value reaches the
   runtime client alone, and no workspace file holds it
   (`TestAPodmanJailGrantNamesKeysOnTheArgvAndHandsValuesToTheClient`).
4. Apple Container's argv names the key the same way, before every `-e` yolo writes
   (`TestAnAppleContainerJailGrantNamesKeysOnTheArgv`).
5. The keeper hands the value to the container's client and records the names, never a value, in
   its start record (`TestTheKeeperHandsTheGrantToTheContainerClientAndRecordsNames`).
6. On macos-user the granted key rides the session and no file under `<workspace>/.yolo/home`
   holds it, while an agent's own profile still reaches its own env file
   (`TestMacosUserGrantRidesTheSessionEnvFileOnly`).
7. An attach naming the jail's set, part of it, or nothing enters and is told what it holds; one
   naming a provider the jail lacks, or a jail launched with none, is refused before anything is
   written, naming `yolo stop` and the fresh launch (`TestAnAttachHoldsTheJailsGrantAndAsksForNoOther`).
8. Every entry discloses the grant by name, never by value, and says everything started inherits it
   (cells 3, 6 and 7).
9. Nothing else implies it, and config cannot express it: a profile, a `-p`, every `YOLO_ALLOW_*`
   hatch and an environment spelling grant nothing, and a config key spelling it is refused as
   unknown (`TestAJailGrantIsImpliedByNothingElse`, `TestJailLaunchGrantIsImpliedByNothingElse`).

UNMEASURED: no cell runs a real container. That a bare `-e NAME` takes its value from the runtime
client's environment is podman's documented `--env` behavior, and Apple Container's is READ FROM
its source (`Parser.env` on apple/container's main branch, 2026-10-05: a name with no `=` inherits
from the client's environment). That every `exec` session inherits the container's frozen
environment is what the jail's main-process hold already relies on (`JailMainEnv`). A Mac, on
either container backend or macos-user, has not run it.

## 6. Trade-offs

| Dimension | Today: one channel, classified by name | The split: `credential_sources` |
| :--- | :--- | :--- |
| Files a user keeps | One | Two, and every existing `env_sources` credential moves |
| A tool that reads `.env` itself (Vite, Next.js, Django) | Unaffected. The gate governs what yolo adds to an environment, not files a tool opens | Unaffected, unless the user moves keys out of `.env` to satisfy the split |
| A withheld key | Disclosed, and at the host the line names the remedy (ES-D2) | Disclosed, and scoped by the key's own name |
| Leak prevention | By name, for every composed provider's claim | Still by name. The split cannot route a name no provider claims ([§4.2](#42-what-it-would-revisit)) |
| `AWS_PROFILE` under the claude pack | Withheld from shells ([OQ-ES6](#OQ-ES6)) | Refused outright under [OQ-ES2](#OQ-ES2)'s filed leaning |
| An ad-hoc host command | `yolo host -p <profile> -- <cmd>`, built | The same |
| Configuration effort | Add the key, select a profile | Classify every variable as one or the other |

**Refusal, leak or disclosure?** The filing offered three options for a claimed name in
`env_sources`: pass it through, withhold it silently, or refuse it. That list misses how today
works:

- **Pass it through.** This leaks, and breaks [OQ-BR4](../reference/providers.md#oq-br4).
- **Withhold it silently.** This is not what happens today. Today it is withheld and disclosed.
- **Refuse it.** This refuses the channel the code names as "the SECRET channel"
  (`composeHostLaunch`'s comment), for reasons that depend on which packs are selected
  ([§4.3](#43-what-it-costs-that-the-filing-did-not-count)).
- **Withhold it, disclose it, and name the remedy.** This is today's behavior at the host since
  ES-D2, and it is the one I recommend.

## 7. Alternatives considered

| Alternative | Verdict |
| :--- | :--- |
| **A. `credential_sources`, with a refusal in `env_sources`** (the filing's proposal) | **Rejected** ([OQ-ES1](#OQ-ES1), answered 2026-09-30 by [OQ-ES5](#OQ-ES5)'s ruling, which keeps `env_sources` the one store). It would also have revisited [OQ-CN1](../reference/providers.md#oq-cn1) and [CN-D6](provider-credential-scope.md#CN-D6) and still needed their claim |
| **B. `yolo host -p <profile> -- <cmd>` for any command** | **Built.** ES-D1 to ES-D5 pin it and make it findable |
| **C. `--all-credentials` for one invocation** | Rejected as a flag of its own. At the host, the maintainer's [OQ-ES5](#OQ-ES5) ruling makes the same grant a VALUE of the one explicit flag, `--with-credentials all`, for a command that needs every provider's key: the usage-bar case |
| **C′. `--with-credentials <provider…>`, an explicit grant naming providers** | **Ruled for the host, 2026-09-27, and for the jail and macos-user, 2026-10-05** ([OQ-ES5](#OQ-ES5)). Both built ([§5.1](#51-the-explicit-grant---with-credentials-built), [§5.2](#52-the-jail-half---with-credentials-at-a-jail-launch-built)) |
| **D. `unscoped: true` inside an `env_sources` inline map** | Rejected. It is a per-file flag in a per-name problem, and it would sit in a workspace-editable file |
| **E. A user-scope acknowledgment that shares one claimed name with every process** | Open, as [OQ-ES6](#OQ-ES6). It is the `AWS_PROFILE` case |

## 8. What this does not license

- **No suppressed disclosure.** Every grant is disclosed. The existing `-p` route already says
  "bash only" ([CN-D16](provider-credential-scope.md#CN-D16),
  [`OQ-RO3`](../reference/report-tiers.md#why-its-this-way)), and `--with-credentials` prints
  its own block on every run ([ES-D15](#10-decision-ledger)).
- **No grant written into a file.** An entry rewrites the per-agent directory whole
  ([CN-D7](provider-credential-scope.md#CN-D7)), so a grant that lived in a file would
  be changed by the next attach, and one launch's result would depend on another's. At the host a
  grant rides only its own entry's exec environment. At a jail launch it rides the jail's own
  launch vehicle and nothing later rewrites it: the container's frozen environment (the names on
  the argv, the values in the runtime client's environment), or on macos-user the root-owned
  per-session env file. Never a file under the workspace ([ES-D32](#10-decision-ledger)).
- **No workspace-scope credential key.** A workspace config can be edited by an agent, which is
  why `yolo host` never reads one (`UserScopeConfig`).
  [`OQ-AS3`](../research/agent-safehouse.md#OQ-AS3) asks whether `env_sources` itself should go
  user-scope-only.
- **Not access control within a jail's uid.** A per-agent file is readable by every process of the
  jail's uid ([§3.2](#32-in-the-jail)). The gate governs ambient environment.
- **Not the `autonomy` kind, and not the post-merge script slot.** `packdecl.AutonomyPosture`
  carries only config patches and launch flags per posture, so it cannot express environment
  delivery.
  [`OQ-LT2`](../reference/pack-system.md#oq-lt2) (no post-merge script slot) is not involved.

## 9. Open Questions

1. ✅ <a id="OQ-ES1"></a>**OQ-ES1: Should credentials move out of `env_sources` into a key of their own?**

   <!-- vantage: question id=OQ-ES1 -->

   This was filed as question 1, and ruling it **revisits a ruling you made.**
   [OQ-CN1](../reference/providers.md#oq-cn1) put the key-to-provider fact on the provider
   declaration and rejected a per-user list on 2026-09-26, and CN-D6 made the gate classify
   names, not sources ([ledger](provider-credential-scope.md#7-decision-ledger)). What has
   changed since is only the surprise in [§1](#1-what-happened-precisely).
   [§3](#3-where-the-surprise-is-by-spelling-and-by-notch) shows that surprise is narrower than
   filed, and [§5](#5-the-host-half-is-built-what-shipping-it-takes) shows its host remedy is
   built. The stakes: a new config key and a migration for every user, or a disclosure fix.

   _Leaning:_ No; [OQ-CN1](../reference/providers.md#oq-cn1) and CN-D6 hold. The split still
   needs the provider's claim to route ([§4.2](#42-what-it-would-revisit)). It has no answer for an unclaimed name, and it would turn
   `AWS_PROFILE` into a refusal ([§4.3](#43-what-it-costs-that-the-filing-did-not-count)). The
   filing's own leaning was also to keep `env_sources` unified.

   **Answer:**
   > **Answered by [OQ-ES5](#OQ-ES5)'s host ruling (2026-09-27): no. Credentials stay in
   > `env_sources`.** The solution the maintainer approved there (*"I like the rest of the
   > solution a lot"*) keeps `env_sources` the one store, with no split. Their reason calls it the
   > credential store in so many words: *"this is the credential store and it needs to be able to
   > be shared because I don't want to put it in multiple places."* The surprise that filed this
   > question is met by the disclosure and its remedies instead. Each withheld line names a remedy
   > (ES-D2, and for an ad-hoc command the grant, [NC-D62](../plans/notch-convergence.md#NC-D62)),
   > and `--with-credentials` is the explicit grant. [OQ-ES2](#OQ-ES2) and [OQ-ES4](#OQ-ES4) are
   > moot, as each said it would be on a no.

2. ✅ <a id="OQ-ES2"></a>**OQ-ES2: Under a split, what happens to a claimed name found in `env_sources`?**

   <!-- vantage: question id=OQ-ES2 -->

   **Moot (2026-09-30):** [OQ-ES1](#OQ-ES1) is answered no, so there is no split. This was filed
   as question 2, blocked on [OQ-ES1](#OQ-ES1) and moot if that ruled no.
   The filed leaning was a hard refusal, and it is the worst of the options:
   - it refuses the channel the gate was built to serve;
   - whether it fires depends on which packs are selected;
   - it refuses `AWS_PROFILE` for every user of the claude pack;
   - it can never see a workspace entry at the host
     ([§4.3](#43-what-it-costs-that-the-filing-did-not-count)).

   _Leaning, should it open:_ Keep today's withhold-and-disclose, with ES-D2's remedy. That
   answer is also the reason the split buys nothing.

3. The filed question 3 (how an ad-hoc host command asks for credentials) is **answered by the
   tree, not ruled.** `yolo host -p <profile> -- <cmd>` already delivers. It is recorded as
   [OQ-ES3 and ES-D1 to ES-D5](#10-decision-ledger). What it leaves open is
   [OQ-ES5](#OQ-ES5) and [OQ-ES7](#OQ-ES7).

4. ✅ <a id="OQ-ES4"></a>**OQ-ES4: Under a split, at which config scope may `credential_sources` appear?**

   <!-- vantage: question id=OQ-ES4 -->

   **Moot (2026-09-30):** [OQ-ES1](#OQ-ES1) is answered no, so there is no `credential_sources`.
   This was filed as question 4, blocked on [OQ-ES1](#OQ-ES1) and moot if that ruled no. The
   filed leaning, "both scopes, matching `env_sources`", misreads the host. `yolo host` reads user
   scope only, whatever the key (`composeHostLaunch`), so a workspace `credential_sources` would
   feed jails only. A credential in a workspace file is also readable by every jail process,
   whichever key lists it. That is outside the gate by
   [the credential gate](../reference/providers.md#the-credential-gate).

   _Leaning, should it open:_ User scope only, for the reason in
   [§8](#8-what-this-does-not-license).

5. ✅ <a id="OQ-ES5"></a>**OQ-ES5: An explicit grant for a command `-p` cannot reach?** — **the host half RULED
   2026-09-27; the jail half RULED 2026-10-05**


   The filed cases and options, (i) and (ii), are in the [background](#background-to-oq-es5); the
   note under the Answer restates the options as (A) and (B).

   _Leaning:_ (ii). The flag needs four properties, following the 2026-09-26 acknowledgment rule
   ([OQ-SK1](attach-skew-and-contract-guardrails.md#OQ-SK1)):
   - it is its own flag, and no other override implies it;
   - it is carried only in that entry's exec environment, never in a file
     ([§8](#8-what-this-does-not-license));
   - it is disclosed on every entry, including that everything the command starts inherits it
     ([CN-D8](provider-credential-scope.md#CN-D8));
   - it can never be expressed in config.

   `--all-credentials` is rejected ([§7](#7-alternatives-considered)).

   **Answer:**
   > **The HOST half ruled by the maintainer, 2026-09-27.** The case that decided it: an AI usage
   > bar run under `yolo host` pings every subscription and provider, so it needs ALL provider
   > keys, from the one credential store the user keeps in `env_sources` — *"this is the
   > credential store and it needs to be able to be shared because I don't want to put it in
   > multiple places"* — and after the credential gate it received none, because no profile
   > selected them. The approved solution, in the maintainer's reply *"I like the rest of the
   > solution a lot"*:
   >
   > - **`env_sources` stays the one store.** No split; this is [OQ-ES1](#OQ-ES1)'s leaning, and
   >   [OQ-ES1](#OQ-ES1) itself is not otherwise ruled here.
   > - **An explicit grant flag,** `yolo host --with-credentials <provider[,provider...]|all> --
   >   <cmd>`. It hands that one command the named providers' CLAIMED `env_sources` values: keys
   >   only, with no profile routing and no shape variables, so nothing re-points a base URL.
   >   `all` is every provider in the composed table that claims a value.
   > - **The same flag on `yolo host env`:** `eval "$(yolo host env --with-credentials all)"`
   >   exports them into the current shell.
   > - **Disclosed on every run,** names only and never values.
   > - **An unknown provider name refuses,** naming the known ones. **A named provider with no
   >   value is reported,** not silently skipped.
   > - **It combines with `-p`:** an agent keeps its profile and additionally receives the granted
   >   keys.
   > - **Nothing else implies it:** not `-p`, not `use_profiles`, not any `YOLO_ALLOW_*`
   >   ([OQ-SK1](attach-skew-and-contract-guardrails.md#OQ-SK1)'s acknowledgment rule).
   > - **HOST ONLY.** A jail launch does not accept it, and a jail launch given the flag refuses,
   >   naming that it is host-only.
   >
   > **The JAIL half ruled in review 2026-10-05, none of the options as written.** The maintainer:
   > *"The jail shouldn't be able to discover credentials from outside that it wasn't launched with.
   > But you should be able to run a jail with whatever set of credentials you want. Like that should
   > be the same."* So `--with-credentials` works at a jail launch as it does at `yolo host`, at every
   > notch, and the set is fixed when the jail is launched: the jail holds exactly the credentials it
   > was launched with and can fetch no others later. The "HOST ONLY" bullet above is superseded.
   > *Read from the ruling, not separately ruled:* the granted set is the jail's, so a later session
   > attached to that jail has it too, and an attach that asks for credentials the running jail was
   > not launched with is refused, naming a fresh launch (stop the jail, relaunch with the flag). The `--all-credentials` rejection above is overtaken
   > for the host by `all` as a value of the one explicit flag ([§7](#7-alternatives-considered)).

   > [!NOTE]
   > **The jail half, restated 2026-09-30 in letters (the options above were numbered).** The case
   > left is a shell in a jail, or in a macos-user jail, that needs a provider's key no agent in
   > that launch selected. `-p` cannot carry it: since
   > [OQ-NC5](../plans/notch-convergence.md#OQ-NC5) a `-p` reaches agent CLIs only, at every
   > notch, and a jail never keyed the `--` command anyway. So this flag is the only candidate
   > grant, and the choice is whether it works in a jail at all.
   > [Notch-convergence item 12](../plans/notch-convergence.md#4-the-ordered-build-list) is gated
   > on it.
   >
   > - **(A) The same flag at every notch** (was (ii)). `yolo --with-credentials zai -- bash`
   >   hands that one launched command the named providers' claimed `env_sources` values, keys
   >   only, as at the host. It rides only that entry's exec environment, it is disclosed on every
   >   entry (including that what the command starts inherits it), and it is never expressible in
   >   config: the four properties listed above. The jail keys the grant by the launched basename,
   >   as notch-convergence's row A5 proposes. It is that plan's thesis, in the maintainer's words
   >   *"host is supposed to act like everywhere else"*
   >   ([§1](../plans/notch-convergence.md#1-the-thesis)). **Cost:** a key no agent asked for
   >   enters the jail when the user types the flag, and an agent started from that shell
   >   inherits it.
   > - **(B) Host only, as built** (was (i)). A jail launch keeps refusing the flag, naming that it
   >   is host-only, and the documented route is `. ~/.config/yolo-agent-env/<agent>.sh`. That
   >   works only when some agent in the launch selected the provider. **Cost:** no jail shell can
   >   get a key its agents did not select, and one flag works at one notch and is refused at the
   >   others.
   >
   > _Leaning, restated:_ (A), because it takes the ruled host flag into the jail unchanged.

6. 💬 <a id="OQ-ES6"></a>**OQ-ES6: May a user share a claimed generic name, such as `AWS_PROFILE`, with every process?**

   <!-- vantage: question id=OQ-ES6 leaning="Yes, by a user-scope, per-name acknowledgment that shares that one claimed name with every process and is disclosed on every launch: a deliberate user exception to OQ-BR4 that only the user file can express, since a workspace config is agent-editable." -->

   bedrock's claim list makes the claude pack alone enough to take `AWS_PROFILE` and the static
   AWS pair from `aws` and `terraform`, at the host and in every jail shell
   ([§3.4](#34-the-case-the-filing-missed-claimed-generic-names)). This asks for **a user-made
   exception to [OQ-BR4](../reference/providers.md#oq-br4)**, which is yours to make or refuse.

   The options:
   - (a) the per-invocation grant stays the only remedy: `yolo host -p bedrock -- terraform`, or
     [OQ-ES5](#OQ-ES5)'s flag in a jail;
   - (b) a user-scope, per-name acknowledgment that shares one claimed name with every process,
     disclosed on every launch;
   - (c) narrow [CN-D2](provider-credential-scope.md#CN-D2)'s list ([caveat](#background-to-oq-es6)).

   The stakes: whether selecting the claude pack silently changes ordinary AWS tooling.

   _Leaning:_ (b), in user scope only, because a workspace config can be edited by an agent. It is
   explicit, it names one variable, and it is disclosed on every launch, so it does not silently
   ride along.

   **Answer:**
   > _(empty — fill in when decided)_

7. ✅ <a id="OQ-ES7"></a>**OQ-ES7: Does a typed host `-p` hand an ad-hoc command a CLI-less pack's gated env?**

   <!-- vantage: question id=OQ-ES7 -->

   Today `yolo host -p bedrock -- bash` receives the static AWS pair but not aws-auth's
   `AWS_CONTAINER_CREDENTIALS_FULL_URI`. `gateFiresFor` does not fire for a basename that no
   selected pack installs ([§3.1](#31-at-the-host)). This revisits
   [CN-D4](provider-credential-scope.md#CN-D4), an implementation decision, whose
   rule "a table key naming no CLI any selected pack installs activates nothing" the host relies
   on by name. The stakes: SSO is the primary Bedrock route
   ([`OQ-SSO7`](sso-backed-bedrock.md#13-decision-ledger)), so for most Bedrock users a grant
   without the pointer delivers nothing usable.

   _Leaning:_ Yes, for a typed `-p` only. The user named the profile for this one process, and no
   other process receives it, so the grant stays as specific as
   [OQ-BR4](../reference/providers.md#oq-br4) requires.

   **Answer:**
   > **Moot since [OQ-NC5](../plans/notch-convergence.md#OQ-NC5) (ruled 2026-09-28).** A typed
   > `-p` now reaches agent CLIs only, at every notch. So `yolo host -p bedrock -- bash` is
   > refused, naming `yolo host --with-credentials bedrock -- bash`
   > ([NC-D62](../plans/notch-convergence.md#NC-D62)), and the case this question asked about can
   > no longer be written. The grant that replaced it carries keys only, by
   > [OQ-ES5](#OQ-ES5)'s ruling (*"keys only, with no profile routing and no shape variables"*).
   > A grant-only process runs no derive and fires no gated env (ES-D13), so it never carried a
   > CLI-less pack's pointer either. Whether it should would be a new question about the grant.
   > It is not this one.

### 9.1 Background to the open questions

#### Background to [OQ-ES5](#OQ-ES5)

This case had no mechanism when it was filed:
- a jail shell (`-p bash=zai` is refused);
- a macos-user shell (delivery is keyed by argv[0]);
- a host command that needs two providers at once. `-p` names one profile, and the last one
  given wins (`parseHostExecFlags`).

The options are (i) document `. ~/.config/yolo-agent-env/<agent>.sh`, which works only when
some agent selected the provider, and stop there; or (ii) add a flag that names providers.
Option (ii) **revisits a constraint [OQ-CN6](../reference/providers.md#oq-cn6) stated**:
*"a bare `yolo -- bash` shell selects no provider, so it gets no provider value."* It would
break that constraint only when the user asks, for one invocation. Reusing `-p` in the jail would instead revisit the 2026-09-03
ruling that a profile never keys the `--` command. The stakes: whether a user can debug a
provider from a jail shell without starting an agent.

#### Background to [OQ-ES6](#OQ-ES6)

On option (c), narrowing [CN-D2](provider-credential-scope.md#CN-D2)'s list: at the host,
though, `AWS_PROFILE` selects the user's own SSO credentials, so it is not a harmless name.

## 10. Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| OQ-ES5 | **The host half, ruled by the maintainer.** `yolo host --with-credentials <provider[,provider...]\|all> -- <cmd>`, and the same flag on `yolo host env`, hands one command the named providers' claimed `env_sources` values: keys only, no profile routing and no shape variables. `all` is every composed provider that claims a value. Disclosed on every run, names only. An unknown provider refuses, naming the known ones; a named provider with no value is reported. It combines with `-p`. Nothing else implies it. HOST ONLY: a jail launch given it refuses, naming that it is host-only. `env_sources` stays the one store. The jail half is open. *Superseded 2026-10-05, the HOST ONLY clause, by the jail half's ruling (next row)* | 2026-09-27 | [OQ-ES5](#OQ-ES5) | ✅ 2026-09-27, as ES-D13 to ES-D17; ES-D17 retired 2026-10-05 |
| [OQ-ES5](#OQ-ES5) (jail half) | **Ruled in review, none of the options as written:** `--with-credentials` works at a jail launch at every notch, and a jail holds exactly the credentials it was launched with; an attach asking for others is refused, naming a fresh launch (read from the ruling) | 2026-10-05 | [OQ-ES5](#OQ-ES5) | ✅ 2026-10-05, as ES-D31 to ES-D36 ([§5.2](#52-the-jail-half---with-credentials-at-a-jail-launch-built)) |
| OQ-ES3 | *Answered by the tree, not ruled.* Filed as question 3: "how should an arbitrary command request provider credentials?" At the host, `yolo host -p <profile> -- <cmd>` already delivers that profile's claimed `env_sources` values to any command and discloses it as `<cmd> only`. ES-D1 to ES-D5 finish it. The jail and multi-provider half is [OQ-ES5](#OQ-ES5); the CLI-less half is [OQ-ES7](#OQ-ES7) | 2026-09-27 | [§5](#5-the-host-half-is-built-what-shipping-it-takes) | ✅ behavior at `8da7840d`; pinned `595f9022` |
| OQ-ES1 | **Answered by [OQ-ES5](#OQ-ES5)'s host ruling:** no split. `env_sources` stays the one credential store. The maintainer: *"this is the credential store and it needs to be able to be shared because I don't want to put it in multiple places"*, approving a solution that kept the one store (*"I like the rest of the solution a lot"*). The filed surprise is met by the disclosure's remedies and the explicit grant | 2026-09-30 | [OQ-ES1](#OQ-ES1) | ✅ nothing to build: `env_sources` is already the one store |
| [OQ-ES2](#OQ-ES2), [OQ-ES4](#OQ-ES4) | **Moot** by [OQ-ES1](#OQ-ES1)'s no, as each said on filing: with no split, there is no claimed-name refusal to design and no `credential_sources` scope | 2026-09-30 | [OQ-ES2](#OQ-ES2), [OQ-ES4](#OQ-ES4) | — |
| OQ-ES7 | **Moot since [OQ-NC5](../plans/notch-convergence.md#OQ-NC5)** (2026-09-28): a typed host `-p` for a command no selected pack installs is refused, naming `--with-credentials` ([NC-D62](../plans/notch-convergence.md#NC-D62)), so it can no longer hand an ad-hoc command anything. The grant is keys only by [OQ-ES5](#OQ-ES5)'s ruling and fires no gated env (ES-D13) | 2026-09-30 | [OQ-ES7](#OQ-ES7) | — |
| ES-D1 | *Implementation decision.* The typed host `-p` is the grant for any command, pinned through `hostMain` with a non-agent basename. The cells fail if the agent loop checks installation or the basename keying goes. The notches differ on purpose: the jail's `-p` never keys the `--` command. *Retired 2026-09-28 by [OQ-NC5](../plans/notch-convergence.md#OQ-NC5):* a bare `-p` reaches agent CLIs only at every notch, and a host `-p` for a command no selected pack installs is refused, naming `--with-credentials` ([NC-D62](../plans/notch-convergence.md#NC-D62)) | 2026-09-27 | [§5](#5-the-host-half-is-built-what-shipping-it-takes) | ✅ `595f9022`; retired as NC-D62 |
| ES-D2 | *Implementation decision.* At the host, a "withheld" line names `yolo host -p <profile> -- <cmd>`, using a declared profile that resolves to the claimant, or says to declare one. The jail's line is unchanged until [OQ-ES5](#OQ-ES5). *Amended 2026-10-05 by ES-D31:* the jail's line names a name the jail's own grant holds as every process's, and still names no remedy, since a running jail takes no grant later (ES-D33). *Amended 2026-09-27 by ES-D23:* on a run given `--with-credentials`, the line names that run with its grant widened instead. *Amended 2026-09-28 by [NC-D62](../plans/notch-convergence.md#NC-D62):* for an ad-hoc command the line names the grant, `yolo host --with-credentials <provider> -- <cmd>`, and the `-p` arm is an agent's only | 2026-09-27 | [§5](#5-the-host-half-is-built-what-shipping-it-takes) | ✅ `hostComposition.credentialRemedy`, pinned by `TestHostGrantWithheldLineNamesTheGrantRemedy` and `TestHostEnvWithheldLineNamesTheGrantRemedy` |
| ES-D3 | *Implementation decision.* `hostUsage`, providers.md's host-notch bullet and host-agent-environment.md describe `-p` as applying to any wrapped command. *Amended 2026-09-28 by [NC-D62](../plans/notch-convergence.md#NC-D62):* they describe `-p` as reaching agent CLIs only, and `--with-credentials` as an ad-hoc command's grant | 2026-09-27 | [§5](#5-the-host-half-is-built-what-shipping-it-takes) | ✅ `hostUsage`, pinned by `TestHostHelpDescribesProfileAsReachingAgentCLIsOnly` (help), and `internal/cli/config_ref.txt` (config-ref); providers.md and host-agent-environment.md 2026-09-27 |
| ES-D4 | *Implementation decision.* At the host, a withheld name the invoking shell holds is disclosed as not added by yolo, never as "withheld" ([CN-D13](provider-credential-scope.md#CN-D13)) | 2026-09-27 | [§3.5](#35-what-the-disclosure-says-and-what-it-gets-wrong) | ✅ `hostComposition.processHolds`, pinned by `TestHostGrantShellHeldNameIsDisclosedAsNotAddedNeverWithheld` and `TestHostEnvShellHeldNameIsDisclosedAsNotAdded` |
| ES-D5 | *Implementation decision.* Only a typed `-p` keys a command no resolvable pack installs. A `use_profiles` key naming one is refused at `yolo host --` as `validateProfile` refuses it everywhere else | 2026-09-27 | [§5](#5-the-host-half-is-built-what-shipping-it-takes) | ✅ `config.UnknownProfileKey`, pinned by `TestHostGrantRefusesAUseProfilesKeyNoPackInstalls` and `TestUnknownProfileKeyIsTheValidatorsRefusal` |
| ES-D6 | *Implementation decision.* ES-D2 and ES-D4 are one input to the gate's disclosure, `packload.DisclosureNotes`, read by `CredentialScope.DisclosureWith`: a `Remedy` sentence appended to each withheld group's line, given its claimants, and an `Inherited` test that rewords a withheld name the launched process already holds. `Disclosure()` is `DisclosureWith` with no notes, which is the jail's wording, so the jail's lines cannot change by accident. *Amended 2026-10-05 by ES-D31:* the jail's one note is its own grant (`DisclosureNotes.Granted`), passed only when its processes hold one. Chosen over post-processing the host's lines because the grouping is the gate's: a shell-held name has to leave its group, and only the grouping knows the groups | 2026-09-27 | [§3.5](#35-what-the-disclosure-says-and-what-it-gets-wrong) | ✅ `CredentialScope.DisclosureWith`, pinned by `TestCredentialScopeDisclosureRemedyReachesWithheldLinesOnly`; `DisclosureNotes.Inherited`, pinned by `TestCredentialScopeDisclosureInheritedRewordsWithheldOnly` |
| ES-D7 | *Implementation decision.* The profile ES-D2 names is a claimant's same-named profile when it resolves to that claimant (every shipped provider ships one), else the first declared profile, by name, that resolves to any claimant. With none, the line says to declare one under `profiles`, showing the entry (`"deepseek": {"provider": "deepseek"}`) and the `-p` it enables. ES-D10 narrows every candidate to one the named command can run on. `yolo host --` spells the remedy for the command as typed; `yolo host env`, which runs nothing, names `eval "$(yolo host env --with-credentials <provider>)"` for the shell and `yolo host -p <profile> -- <agent>` for one launch. *Amended 2026-09-27:* the shell spelling first named the verb's own `--agent`, default `claude`, whose slice carries the agent's whole provider shape: `ANTHROPIC_BASE_URL`, and the key again under `ANTHROPIC_AUTH_TOKEN`. An eval'ing shell then hands those to every process it starts, disclosed only by the key's own name ([CN-D13](provider-credential-scope.md#CN-D13)). `--agent bash` is [§3.1](#31-at-the-host)'s and the help's spelling, and any name no selected pack installs composes the same slice. **Revised 2026-09-28 by [NC-D62](../plans/notch-convergence.md#NC-D62):** the shell spelling was `eval "$(yolo host env --agent bash -p <profile>)"` until a typed `-p` was refused for every command no selected pack installs, bash among them. It is now the grant, `--with-credentials`, which composes the same ad-hoc slice with the keys only ([ES-D16](#10-decision-ledger)). An ad-hoc command's `yolo host --` remedy is the grant too, `yolo host --with-credentials <provider> -- <cmd>`, and names no profile; an agent's keeps the `-p` this row picks | 2026-09-27 | [§5](#5-the-host-half-is-built-what-shipping-it-takes) | ✅ `remedyProfiles`, pinned by `TestRemedyProfilePrefersTheProvidersOwnName` and `TestHostGrantWithheldLineSaysToDeclareAProfileWhenNoneSelectsTheProvider`; shell spelling, `TestHostEnvWithheldLineNamesTheGrantRemedy` |
| ES-D8 | *Implementation decision.* ES-D4's "the shell holds it" means the composed environment, the one the exec hands over or an eval'ing shell ends with, holds the invoking shell's own value intact. A removal keeps the "withheld" line. Only a withheld group is reworded, because a delivered name carries the `env_sources` value, which beats the shell's; the reworded line names no remedy, because the command already holds a value. *Amended 2026-09-27:* this row first kept the "withheld" line for a value yolo composes over the shell's too, which is false for the same reason ES-D4 is: the process holds the name. That case is ES-D12's | 2026-09-27 | [§3.5](#35-what-the-disclosure-says-and-what-it-gets-wrong) | ✅ `TestCredentialScopeDisclosureInheritedRewordsWithheldOnly` and `TestHostGrantBeatsTheShellsValue`; amended: `hostComposition.processHolds`, pinned by `TestHostEnvShellValueComposedOverIsNotDisclosedAsPassingThrough` |
| ES-D9 | *Implementation decision.* ES-D5 asks the validator's own namespace through `config.UnknownProfileKey`: `config.UseProfileCLINames`, every resolvable pack's CLI, selected or not, with the same message `validateProfile` adds and the same step-aside when a configured pack cannot resolve. The plan's "`binOwner` over the selected packs … resolves the same selection" was wrong, since the validator's namespace is the whole universe; refusing on the selected packs alone would refuse what `yolo check` accepts, the disagreement ES-D5 exists to end. So `use_profiles: {"codex": "zai"}` with codex unselected is accepted at both notches. The refusal sits in the host composition, so `yolo host env --agent <name>` refuses the same entry, and a typed `-p` is exempt. **Revised 2026-09-28 by [notch-convergence item 13](../plans/notch-convergence.md#tier-4--the-host-runs-the-jails-checks-p1-p4):** the host runs the provider and profile section of validation over user scope, so a `use_profiles` key every jail launch refuses refuses every host launch too, a typed `-p` included; ES-D5's check still runs first for the remedy it adds | 2026-09-27 | [§5](#5-the-host-half-is-built-what-shipping-it-takes) | ✅ `config.UnknownProfileKey`, pinned by `TestHostGrantAcceptsAUseProfilesKeyTheValidatorAccepts` and `TestUnknownProfileKeyStepsAsideWhenTheNamespaceIsUnknowable` |
| ES-D10 | *Implementation decision.* A remedy is a command the user will run, so the profile it names must be one the named command can run on, asked the way that launch asks. `runsOn` calls `ScopeCredentials` over the launch's own inputs with the candidate selected for the command, which runs `AgentEnv`'s protocol pairing gate and the pack's env derive. ES-D7's order gives the candidates, and the first that composes is named. When none does, the line says the agent cannot run on that profile and names the ad-hoc spelling instead: `yolo host -p <profile> -- bash`, or only the shell spelling at `yolo host env`. The declare-a-profile arm asks the same of the entry it shows, resolved to its provider alone. A command no selected pack installs is never asked, since no pack code runs for it. The credential pre-flight is not re-asked, because the name the line is about is the credential it looks for. Chosen over `PairingRefusals`, the pairing gate alone, which would miss a derive's refusal. The pairing refusal's own fix, adding wire-bridge to `packs`, is not named: at the host that composes claude onto the adapter's address, `http://127.0.0.1:8214` (measured), and the bridge exists only in-jail ([`wire-bridge.md`](../reference/wire-bridge.md#what-this-does-not-license)). *Amended 2026-09-27 by ES-D18:* the host composes that address no longer, so with wire-bridge listed the claude launch refuses naming why, and `runsOn` answers no for it | 2026-09-27 | [§5](#5-the-host-half-is-built-what-shipping-it-takes) | ✅ `hostComposition.runsOn`, pinned by `TestHostGrantRemedyNeverNamesAProfileTheAgentRefuses` and `TestHostGrantRemedyPrefersAProfileTheAgentRunsOn` |
| ES-D11 | *Implementation decision.* A `-p` names one profile, so the one a remedy names replaces the launch's own. A withheld name belongs to a provider the command's profile did not select, so on a command a selected pack installs, the named `-p` re-points the agent's backend and adds no key. That line is worded as the switch it is ("To run claude on the zai profile for one launch, replacing its bedrock profile"). An ad-hoc command's line stays a grant ("To hand it to bash for one launch"), and says which typed profile it replaces when there is one. Chosen over also offering the ad-hoc spelling on an agent's exec line, which would lengthen every such line for a case `yolo host env`'s shell spelling already covers. *Amended 2026-10-04 by [AP-D19](active-provider-sets.md#AP-D19), an implementation decision taken under the maintainer's 2026-10-04 delegation ("make them and build it … adjust later"); reversible:* on an agent that holds an [active set](active-provider-sets.md) (its pack declares `provider_sets` and the launch selected a profile), the remedy ADDS the claiming profile to the set instead of switching to it: "To add the cerebras profile to pi's active set for one launch, keeping zai, openrouter: `yolo host -p pi=zai,openrouter,cerebras -- pi`", the pair form that replaces the set for one launch ([AP-D4](active-provider-sets.md#AP-D4)), named only when that widened set composes (`runsOnSet`: the set's rules, then the gate). Otherwise the switch is named as before, and on an agent holding more than one profile it says "replacing its active set (zai, bedrock)". The first wording named `yolo host -p cerebras -- pi` there, whose run handed pi cerebras's key and empty `ZAI_API_KEY` and `OPENROUTER_API_KEY` (MEASURED against 6dede33a4 by `TestHostSetRemedyAddsTheProfileToTheSet`) | 2026-09-27 | [§5](#5-the-host-half-is-built-what-shipping-it-takes) | ✅ `hostComposition.remedyAction`, pinned by `TestHostGrantAgentRemedyIsWordedAsAProfileSwitch` and `TestHostEnvAgentRemedyIsWordedAsAProfileSwitch`; the amendment `hostComposition.addAction`, pinned by `TestHostSetRemedyAddsTheProfileToTheSet` and `TestHostSetRemedyFallsBackToTheSwitchForASecondRegionalEntry` |
| ES-D12 | *Implementation decision.* A withheld name the process holds from a value yolo composed from another source than `env_sources`, such as a pack's `env` of the same name, is disclosed as not delivered from `env_sources`, the process holding that source's value. It is never disclosed as "withheld from every process". The mechanism is `DisclosureNotes.Composed`, beside `Inherited`, which is asked first. At the host it answers for a name whose composed value is not the shell's own, with or without a shell value. The line keeps its remedy, because a delivered `env_sources` value beats the pack's. The jail passes no notes (ES-D6), so its line is unchanged. The jail has the same case: a pack's static value of a claimed name reaches every process through the shared file whenever no agent receives the `env_sources` value. *Corrected 2026-10-04:* this row said that case was "read from the code and not measured", and its reason for keeping the remedy, "a delivered `env_sources` value beats the pack's", held at the host alone. In a jail, the agent's file wrote the `env_sources` value def-form after the shared file's plain-form pack line, so the agent kept the pack's value (MEASURED at 6dede33a4 for an unclaimed name, which a jail shell held as the pack's; a claimed name takes the same two lines). Since the one ordered composition ([OQ-NC12](../plans/notch-convergence.md#OQ-NC12), decided on its leaning A), a delivered `env_sources` value beats a pack's `env` at every vehicle, pinned by the env-winner parity tests in `internal/cli/run` | 2026-09-27 | [§3.5](#35-what-the-disclosure-says-and-what-it-gets-wrong) | ✅ `DisclosureNotes.Composed`, pinned by `TestCredentialScopeDisclosureComposedRewordsWithheldOnly` and `TestHostGrantComposedNameIsNotDisclosedAsWithheld` |
| ES-D13 | *Implementation decision.* [OQ-ES5](#OQ-ES5)'s grant is a RECIPIENT of the gate: `packload.ScopeInput.Grants` maps a process to the providers it is granted, and `ScopeCredentials` gives that process a delivery carrying every `env_sources` name a granted provider claims (`credentialClaims`, the one claim model). A grant-only process has no profile and no provider, so no derive runs, no gated env fires and nothing is re-pointed. `SelectedProviders` never lists a granted provider, so the pre-flight asks nothing of it. `LookupFor` is not widened, so no derive sees a granted key. The disclosure counts the grantee as a recipient (`ZAI_API_KEY (provider zai): bash only`). Chosen over appending the values at the host after the gate, which would re-derive the claim model and leave the gate's own lines calling a delivered name withheld. A jail passes no grant, so its answer is unchanged. *Amended 2026-10-05 by ES-D31:* a jail's grant is still never a recipient of the gate, by design: it is every process's, carried outside the per-agent deliveries | 2026-09-27 | [§5.1](#51-the-explicit-grant---with-credentials-built) | ✅ 2026-09-27 |
| ES-D14 | *Implementation decision.* The flag's grammar. It is repeatable, and each value is a comma list; the occurrences merge. An empty element refuses at the parse, exit 2, because a grant of nothing is a mistake. `all` is a keyword: every composed provider that claims a name `env_sources` holds (`packload.ClaimingProviders`), and it may stand beside names. A provider literally named `all` cannot be granted by name. Any other name the composed table does not hold refuses the launch, exit 1, before the exec, naming every composed provider. The same refusal is `yolo host env`'s error | 2026-09-27 | [§5.1](#51-the-explicit-grant---with-credentials-built) | ✅ 2026-09-27 |
| ES-D15 | *Implementation decision.* The grant's disclosure is its own block after the gate's lines, `Credential grant (--with-credentials <as typed>)`, printed on every run given the flag, including one that grants nothing. It says the values are keys only, that the grant selects no profile and re-points nothing, and that everything the command starts inherits them ([CN-D8](provider-credential-scope.md#CN-D8)). With a profile it says the command keeps it. Then there is one line per granted provider: the names it delivered, or `nothing granted`, with the names it claims or that it claims none. It prints names only. Unconditional in `hostExec` and in `hostEnvDelta`'s result ([`OQ-RO3`](../reference/report-tiers.md#why-its-this-way)) | 2026-09-27 | [§5.1](#51-the-explicit-grant---with-credentials-built) | ✅ 2026-09-27 |
| ES-D16 | *Implementation decision.* `yolo host env --with-credentials …` with no `--agent` composes the ad-hoc slice (`bash`, ES-D7's stand-in for a name no selected pack installs), not the verb's default `claude`. The ruled spelling `eval "$(yolo host env --with-credentials all)"` asks for keys, and claude's slice would export its profile's whole shape beside them, `ANTHROPIC_BASE_URL` included, re-pointing every claude that shell starts. That is ES-D7's defect, and a grant re-points nothing. `--agent <name>` still composes that agent's slice, profile included, with the keys added. *Amended 2026-09-27 by ES-D21:* the flip applies only when `-p` is not given either | 2026-09-27 | [§5.1](#51-the-explicit-grant---with-credentials-built) | ✅ 2026-09-27 |
| ES-D17 | *Implementation decision.* A jail launch given `--with-credentials` exits 2 through `refuseHostOnlyFlags`, ahead of the unknown-flag refusal. The message names the flag as host-only, says the jail half is open, and gives the host spelling with the value typed. Only yolo's own tokens are asked (`parseRunArgs`' boundary), so a wrapped program's flag of the same name is its own. The front door skips the flag's value (`valueTakingFlags`). Without that skip, `yolo --with-credentials zai -- bash` answered `unknown command "zai"`, which never said the flag is host-only. `yolo --at host --with-credentials … -- <cmd>` is the host verb's spelling and carries it there. **Retired 2026-10-05 by [OQ-ES5](#OQ-ES5)'s jail half:** `refuseHostOnlyFlags` is deleted, and a jail launch takes the flag (ES-D34). The front door's value skip stays, so `yolo --with-credentials zai -- bash` still reaches the launch | 2026-09-27 | [§5.1](#51-the-explicit-grant---with-credentials-built) | ✅ 2026-09-27; retired 2026-10-05 |
| ES-D18 | *Implementation decision.* The host composes no adapter address that a pack's own `service` serves. `composedHostProviders` passes `packload.WithoutServiceAdaptations`, and `Adaptation.Service` records the sibling service, which is [the one fact](../reference/protocol-resolution.md#the-three-declarations) that tells that shape from a remote gateway or a user-run proxy. `yolo host` starts no pack service, so `packs/wire-bridge`'s `http://127.0.0.1:8214` is a dead address there ([`wire-bridge.md`](../reference/wire-bridge.md#at-the-host-notch)). A pairing only such an adaptation would resolve refuses at the protocol gate as `*packload.UnservedAdapterError`, handed the left-out list through `ScopeInput.UnservedAdaptations`. The host words it: the profile, the address, the service that serves it only inside a jail, and `yolo -p <agent>=<profile> -- <agent>`, where the profile works. Refused at `yolo host --` and `yolo host env` alike, before the exec. Chosen over refusing on the composed address, which would also refuse an agent that speaks the provider's own wire: copilot on cerebras now runs on cerebras's openai endpoint, as it does with wire-bridge unlisted. This is [WG-I12](wire-bridge-gateway.md#WG-I12)'s rule, which clears the via address, carried to the adapter address. Found by ES-D10: the remedy could not name the pairing refusal's own fix, since at the host that composed claude onto the bridge's address. *Amended 2026-09-27 by ES-D19 and ES-D20:* the same refusal now covers wire-bridge unlisted, and it names only a container jail as where the profile works. *Narrowed 2026-09-28 by [HS-D5](host-notch-services.md#HS-D5):* the host and macos-user start the service's host half for the launch, so this refusal remains only for a service with no host half, one whose pack yolo does not ship, a host half that fails to start, and `yolo host env` | 2026-09-27 | [`wire-bridge.md`](../reference/wire-bridge.md#at-the-host-notch) | ✅ 2026-09-27 |
| ES-D19 | *Implementation decision.* At the host, an UNSELECTED shipped pack that serves its adaptation with its own `service` is no outcome 3 remedy. The ordinary pairing refusal told `{"packs": ["claude", "cerebras"]}` to add wire-bridge to `packs`, and the pairing would resolve. Doing that then produced ES-D18's refusal, so the user met two refusals in a row (measured through `hostMain`, and the same for `-p codex -- claude` through the `openai-responses` adapter at `:8215`). The host hands the gate `packload.UnservableAdaptations`: the selected packs' service adaptations, then the unselected shipped packs', each at the user's `adapters` override. The gate takes those out of outcome 3's candidates. A pairing one of them would resolve refuses once, as `UnservedAdapterError`, whether the pack is listed or not; `Selected` says which. The order is a selected pack's first, then an outcome 3 this notch can serve, then an unselected pack's. The host's wording says the listing changes nothing here, and it says "would point" rather than "points", since the host composes the address in neither case. Chosen over filtering outcome 3 alone, which would have ended at outcome 4's "nothing declares an adapter": false, since a shipped pack declares one. The jail notch passes no list, so outcome 3 there still names the pack to add | 2026-09-27 | [`wire-bridge.md`](../reference/wire-bridge.md#at-the-host-notch) | ✅ 2026-09-27 |
| ES-D20 | *Implementation decision.* ES-D18's refusal said the bridge is "a daemon yolo runs only inside a jail" and "The profile works inside a jail". Both are false on the macos-user backend, which starts no jail daemons, the bridge included ([`wire-bridge.md`](../reference/wire-bridge.md#no-macos-user-bridge); `run.noteMacosUserJailDaemonDeclines`). A user following it there would start claude against the same dead address. The refusal now says the service runs only in a container jail, and names the jail spelling for podman or Apple Container. It says macos-user does not run the service either, and names `YOLO_RUNTIME=podman` or `YOLO_RUNTIME=container` as the dial that picks a container backend for one launch. That env var outranks the `runtime` key. The sentence is the same on every host OS. Chosen over a darwin-only clause: on Linux macos-user cannot be selected, so the clause is true there too and costs one sentence, and a branch on the OS would need a seam to test from Linux. The packload `Error()` says "container jail" too | 2026-09-27 | [`wire-bridge.md`](../reference/wire-bridge.md#at-the-host-notch) | ✅ 2026-09-27 |
| ES-D21 | *Implementation decision.* `yolo host env`'s default slice flips to `bash` under `--with-credentials` only when neither `--agent` nor `-p` is given. ES-D16 flipped it whenever a grant was given, so `yolo host env -p zai --with-credentials cerebras` exported only `ZAI_API_KEY` and `CEREBRAS_API_KEY`. `yolo host env -p zai` alone exports claude's whole zai slice, `ANTHROPIC_BASE_URL` among it (measured through `hostMain`). Meanwhile the disclosure said "bash's slice keeps its zai profile, and the grant only adds keys beside it". That contradicted the ruling's "it combines with -p". A typed `-p` asks for that profile's slice, and the grant only adds keys to it. Chosen over recording the flip for the `-p` case, which would make the grant change what `-p` composes. The help says which slice each spelling gets | 2026-09-27 | [§5.1](#51-the-explicit-grant---with-credentials-built) | ✅ 2026-09-27 |
| ES-D22 | *Implementation decision.* Under a grant, the gate's disclosure heads with "a provider's credential reaches only the processes whose profile selects it or whose --with-credentials grant names it". Before, its fixed head line read "reaches only the agents whose profile selects it". ES-D13 made a grantee a recipient, so the next line contradicted it: `ZAI_API_KEY (provider zai): usage-bar only`, for a `usage-bar` no profile selects (measured, and in [§5.1](#51-the-explicit-grant---with-credentials-built)'s own console block). `CredentialScope.disclosureRule` picks the line from whether any delivery carries `Granted`. With no grant, as at every jail launch, the line is unchanged. It names the flag because the host's `--with-credentials` is the grant's only source (`ScopeInput.Grants`). *Amended 2026-10-05 by ES-D31:* a jail launched with a grant takes the same head line, chosen from its `DisclosureNotes.Granted` lines, and the flag is the only source there too | 2026-09-27 | [§5.1](#51-the-explicit-grant---with-credentials-built) | ✅ 2026-09-27 |
| ES-D23 | *Implementation decision.* On a run given `--with-credentials`, a withheld line's remedy is the same run with a claimant added to the grant: `yolo host [-p <typed>] --with-credentials <as typed>,<claimant> -- <cmd>`, or `eval "$(yolo host env … --with-credentials …)"` at the shell. The shell spelling keeps the typed `-p`, and `--agent` where it is not the verb's default for that spelling (`hostEnvDefaultAgent`, shared with the verb). Before, the line named `yolo host -p cerebras -- usage-bar`, which drops the grant: run, it lost `ZAI_API_KEY`, and unlike ES-D11's "replacing its X profile" the line did not say so (measured). A grant is keys only and runs no derive, so the widened run composes whenever the original did, and `runsOn` need not be asked. The first claimant is named, since any one delivers the group. Chosen over naming the `-p` with a warning that it drops the grant: the user already chose the grant, and the additive command exists. The `-p` help line and `credentialRemedy`'s comment no longer call `-p` the only way | 2026-09-27 | [§5.1](#51-the-explicit-grant---with-credentials-built) | ✅ 2026-09-27 |
| ES-D24 | *Implementation decision.* `yolo host` still applies no pack's `needs`. The dig behind ES-D25 proposed running the jail's selection closure in `loadedHostPacks`, so that openai-auth and wire-bridge join a claude launch and it reaches ES-D18's refusal. That was measured first, with a fake `claude` and `yolo host env --format json` printing every composed variable, over `yolo host -- claude`, `yolo host -p bedrock -- claude`, a `use_profiles` of bedrock, and `yolo host env --agent codex\|pi` with and without `-p codex`, each over `"packs": ["claude"]`, `["codex"]`, `["pi"]` and all three. The plain claude dumps and every codex and pi dump matched. Every claude bedrock dump gained `AWS_CONTAINER_CREDENTIALS_FULL_URI=http://127.0.0.1:1461/credentials`, aws-auth's bedrock-gated pointer at an address only the jail half of the aws-auth loophole serves. So the closure is not applied, and a profile whose provider only a needed pack declares refuses instead (ES-D25). The host services `yolo host --` starts do not depend on the pack set: the managed OpenAI launch is keyed on the command's name (`openaiauthhost.Prepare`). **Revised 2026-09-28 by [notch-convergence item 6](../plans/notch-convergence.md#tier-2--one-selection-p1-p2)** ([NC-D4](../plans/notch-convergence.md#7-decision-ledger), [HS-D1](host-notch-services.md#HS-D1)): the host now runs the closure through the one selection function, because served-address composition withholds and names the aws-auth pointer this row measured (NC-D16). **Revised 2026-09-28 by [notch-convergence item 15](../plans/notch-convergence.md#tier-4--the-host-runs-the-jails-checks-p1-p4)** ([NC-D37](../plans/notch-convergence.md#NC-D37)): the managed OpenAI launch is no longer keyed on the command's name; it reads the prelaunch the selected packs declare in the composed variables, so it too now depends on the pack set | 2026-09-27 | [`wire-bridge.md`](../reference/wire-bridge.md#at-the-host-notch) | ✅ 2026-09-27 |
| ES-D25 | *Implementation decision.* A selected profile whose provider the composed table does not hold refuses at the protocol gate, naming why ([declaration-parity P1](declaration-parity.md#1-the-principle-and-what-it-does-not-say)). Before, `refuseUnspeakableProvider` returned nil there and the derive ran over nothing. With `"packs": ["claude"]`, `yolo host -p codex -- claude`, `yolo -p codex host -- claude`, `yolo --at host -p codex -- claude` and a `use_profiles` of codex each exited 0 and handed claude three context-window constants and no address, so it ran on its own Claude login (measured). v0.10.0 composed `ANTHROPIC_BASE_URL=http://127.0.0.1:8215` there instead, with no token beside it (measured on a `v0.10.0` build). The refusal is `packload.MissingProviderError`, in three shapes. If a selected pack ships the provider, the user's null `providers` entry removes it, and the refusal names the null. If no pack yolo ships declares it, the refusal says to declare it. If an unselected shipped pack declares it, the gate asks the pairing again with that pack's declaration composed as this notch composes, and answers from that. An `*UnservedAdapterError` is returned itself, carrying the pack (`ProviderPack`, `NeededBy`), and the host's wording adds that the provider's pack is missing too and that listing it changes nothing. Any other refusal is carried as the reason adding the pack alone would not help. A resolving pairing refuses naming the pack to add, with one exception. When the agent's pack registers no `yolo.env` producer for it (`luahook.EnvRegistrations`), the row has no reader in the agent's environment, and nothing is refused. That is codex and pi on their codex profile at the host, which reach the subscription through the managed launch, so `yolo host -p codex -- pi` still runs. At a jail a config-surface derive for the agent counts as a reader too ([ES-D30](#10-decision-ledger)). Claude's `openai-codex` derive branch also composes nothing when the row carries no address. `yolo check` reports the same refusal through `PairingRefusals`. The credential pre-flight is unchanged ([OQ-PT4](../reference/providers.md#oq-pt4)): a dropped provider still demands no key, and the refusal is the selection's, never a missing credential | 2026-09-27 | [`protocol-resolution.md`](../reference/protocol-resolution.md#the-four-outcomes) | ✅ 2026-09-27 |
| ES-D26 | *Implementation decision.* ES-D18's refusal names the jail spelling as a jail launch: "`yolo -p claude=codex -- claude`, which is a jail launch, not a `yolo host` one". Read as a host spelling, it sent the user to `yolo host -p claude=codex -- claude`, which refused as an undeclared profile named `claude=codex`. Once the host parses that pair (ES-D27), the same spelling refuses with this refusal, so adding `host` to it can never look like the fix | 2026-09-27 | [`wire-bridge.md`](../reference/wire-bridge.md#at-the-host-notch) | ✅ 2026-09-27 |
| ES-D27 | *Implementation decision.* `yolo host -p` and `yolo host env -p` read the run path's grammar, one parser for both notches (`parseProfileValue`, which `applyProfileValue` now calls). A `cli=name` pair naming the command composed (the command after `--`, or `--agent`) means the bare name. A pair naming any other CLI refuses by name, with the launch that would compose it, because the host composes one command's environment and there is no second process for the pair to select for. An empty name refuses. Before, both host parsers took the value as a bare name, so `yolo host -p claude=codex -- claude` refused as an undeclared profile named `claude=codex` (measured on 1baf1fd4). The only sources for keeping the host name-only were an implementer's note in 582ae850 and the "do not unify" warning in `providers.md` that grew from it, with no maintainer ruling, and that warning is deleted. A pair is never a credential grant: handing a process another provider's key stays `--with-credentials`' alone. Chosen over reading a pair naming another CLI as selecting for it anyway, which would compose a profile for a process that is not running | 2026-09-28 | [`providers.md`](../reference/providers.md#per-agent-delivery) | ✅ 2026-09-28 |
| ES-D28 | *Implementation decision.* A claude jail on its `codex` profile ensures the machine's OpenAI login before claude starts. Before, only the codex and pi packs declared the launcher's authentication step (`YOLO_AUTH_PRELAUNCH_<BIN>_FLAG`), so `yolo -p codex -- claude` never started the login: on a machine never logged in to OpenAI, claude started and its first request failed in the bridge, whose Codex route asks the credential service for an access view on every request (reproduced in a unit test: claude's env on that profile carried no prelaunch variable, and its launcher made no credential call). The claude pack now gates `YOLO_AUTH_PRELAUNCH_CLAUDE_LOGIN=1` on `codex`, and the shared launcher step reads `_LOGIN` as the login alone: the same token request with no view flag, whose access view is discarded, and the same login at a terminal when it fails. No file is written, since claude reads no OpenAI auth file. Core names no agent: the step keys on the launcher's own binary name, as before. The gate keys on the profile NAME, as pi's does, so a user-declared profile over `openai-codex` does not reach it; which a gate should key on is open in [`providers-and-profiles-redesign.md`](providers-and-profiles-redesign.md) | 2026-09-28 | [`wire-bridge.md`](../reference/wire-bridge.md#lifecycle-and-failure-behavior) | ✅ 2026-09-28 |
| ES-D29 | *Implementation decision.* The bridge's Codex route dials the composed `openai-codex` entry's `openai-responses` base URL, with `wirebridged.CodexResponsesBaseURL` as the default for an entry that names none. It dialed the constant outright, a second writer of the URL `packs/openai-auth` declares, which left no way to drive the route end to end without the real subscription: the integration test the build asked for, against a stub upstream, could not be written. This is the listen address's rule applied to the upstream, whose constant already became a default the same way. That makes the bridge the first consumer to send the subscription's access view to a URL read from the composed table: pi, omp and codex exclude `openai-codex` from their catalogs by name and reach the subscription through their own clients at a fixed address. The whole safety case is that only a user-scope `providers` entry can move the URL, since a workspace-scope provider address is refused (`config.validateProviderAddressScope`). `TestWireBridgeTranslatesClaudeCodexToResponses` drives the route with a forged login against a stub; with the constant restored it answers HTTP 401 | 2026-09-28 | [`wire-bridge.md`](../reference/wire-bridge.md#current-values) | ✅ 2026-09-28 |
| ES-D30 | *Implementation decision.* At a jail, a `yolo.derive` for the agent in any selected pack is a reader of the provider table, beside its pack's `yolo.env` producer, so ES-D25's exception applies there only to an agent nothing derives for at all. The host keeps the env-producer test alone, since it renders no config surface. Before, the exception asked only for a `yolo.env` producer at both notches, and ES-D25 claimed a jail never reached it for shipped packs. That held for shipped profiles and failed for a user-declared one. With `"profiles": {"myz": {"provider": "zai"}}` selected for pi, codex or opencode over `"packs": ["<agent>"]`, `yolo check` passed and the jail started the agent on its own default, because each agent's settings derive writes nothing for a provider the table lacks (measured, the silent no-op of [declaration-parity P1](declaration-parity.md#1-the-principle-and-what-it-does-not-say)). Claude and copilot already refused. It now refuses naming `zai` to add, at the jail and in `yolo check`. `yolo host -p codex -- pi` still runs. `TestAJailRefusesAUserProfileWhoseProviderItLacks` and `TestAJailCountsASurfaceDeriveAsAReader` pin it | 2026-09-28 | [`protocol-resolution.md`](../reference/protocol-resolution.md#the-four-outcomes) | ✅ 2026-09-28 |
| ES-D31 | *Implementation decision, reversible, inside [OQ-ES5](#OQ-ES5)'s jail half:* **a jail's grant is held by the JAIL, not by one process, and it is not a recipient of the gate.** "The granted set is the jail's", read with "a later session attached to that jail has the jail's set", leaves no single process to key it by: every process in the jail holds it (on macos-user, every process of the one sandbox session), the session the launch starts, every session attached later and everything each starts. So the launch resolves it once (`resolveJailGrant`, `internal/cli/run/jailgrant.go`), from the gate's own claims (`CredentialScope.GrantFor`), and carries it outside the gate's per-agent deliveries. The host's `ScopeInput.Grants` route would write a grantee's delivery into the per-agent env files every entry rewrites under `<workspace>/.yolo/home`, which is both a file the next attach replaces and a copy of the credential in the workspace (the leak the audit measured on macos-user). The gate's disclosure counts the grant through one note, `DisclosureNotes.Granted` with its `GrantHolder` phrase, so a granted name reads `every process in this jail, by its --with-credentials grant` and never `withheld`, and the head line takes ES-D22's grant rule. Chosen over keying the grant by the launched basename (notch-convergence row A5's proposal), which an attach could not hold | 2026-10-05 | [§5.2](#52-the-jail-half---with-credentials-at-a-jail-launch-built) | ✅ `resolveJailGrant` and `Options.grantDisclosureNotes`; pinned by `TestAPodmanJailGrantNamesKeysOnTheArgvAndHandsValuesToTheClient` and `TestMacosUserGrantRidesTheSessionEnvFileOnly`, each call site mutation-checked |
| ES-D32 | *Implementation decision, reversible:* **the vehicles.** On podman and Apple Container, which share the argv (`assembleRunCmd`), each granted NAME is a bare `-e NAME`, never its value. It is placed before every `-e` yolo writes, so a provider claiming a name yolo sets cannot replace yolo's value (on both runtimes a later `-e` wins). The values travel to the keeper in its plan (`keeperPlan.GrantEnv`: 0600, in a 0700 directory of its own, read once and removed; the merged config's inline `env_sources` already ride the same file). They reach only the environment of the one runtime client the keeper starts the container with (`startJailMainWithEnv`), never the keeper's own, so no host service it starts inherits them. The container's frozen environment then holds the set for the jail's life, and every `exec` session inherits it, as the main-process hold already relies on (`JailMainEnv`). On macos-user the grant is set on the launch env (`jailGrant.applyTo`), which the backend writes into the root-owned per-session env file (`macosuser.SandboxEnvFile`), never through the channel the arm writes per-agent files from. Chosen over `-e NAME=VALUE`, which puts a credential on a `ps`-visible argv (`TestNoTokenInLaunchArgv`'s rule), and over `--env-file`, which writes one into a file. UNMEASURED on a Mac and against a real container: podman's bare `--env NAME` is its documented behavior, and Apple Container's is READ FROM its source (`Parser.env`, apple/container main, 2026-10-05) | 2026-10-05 | [§5.2](#52-the-jail-half---with-credentials-at-a-jail-launch-built) | ✅ `jailGrant.envArgs` in `assembleRunCmd`, `keeperPlanFor`, `startJailMainWithEnv` in `keeper.run`, `applyTo` in the macos-user arm; pinned by `TestAPodmanJailGrantNamesKeysOnTheArgvAndHandsValuesToTheClient`, `TestAnAppleContainerJailGrantNamesKeysOnTheArgv`, `TestTheKeeperHandsTheGrantToTheContainerClientAndRecordsNames` and `TestMacosUserGrantRidesTheSessionEnvFileOnly` |
| ES-D33 | *Implementation decision, reversible, the attach rule read from the ruling:* **the keeper's start record carries the grant, names only, and an attach is compared against it by provider and by name.** `keeperRecord.Grant` is the launch's `jailGrant`, whose encoded fields are the spelling as typed, the providers and, per provider, the names it delivered and claims; the values are an unexported field no encoding reaches. An attach resolves its own request over its own composition. It enters when every provider it names is in the jail's grant and every name it would deliver the jail holds: the same set, a subset, or no request. Otherwise it is refused before anything is written, exit 1, naming what the jail lacks, `'yolo stop' from this workspace`, and `yolo --with-credentials <the jail's set and this entry's> -- <cmd>`. A record that cannot be read refuses any request, since nothing shows the jail holds it. Values are never compared, so a rotated key needs the fresh launch too, which the attach's line says ("as they were at its launch"). Every attach to a jail launched with a grant prints it, whether or not it typed the flag or delivers its channel. macos-user has no attach: each invocation is a session with its own grant | 2026-10-05 | [§5.2](#52-the-jail-half---with-credentials-at-a-jail-launch-built) | ✅ `refuseGrantTheJailLacks` and `noteHeldGrant` in `attachExisting`; pinned by `TestAnAttachHoldsTheJailsGrantAndAsksForNoOther` |
| ES-D34 | *Implementation decision, reversible:* **one flag, one resolver.** The jail's parser reads `--with-credentials` in the host's grammar (`addGrantValue`: comma lists, repeatable, an empty element refused at the parse, exit 2) into `run.Options.WithCredentials`, the launch's only source of a grant; `refuseHostOnlyFlags` is deleted. Both notches resolve through `packload.ResolveGrant` (the host's `resolveHostGrant` now calls it) and report each provider through `packload.GrantProviderLines`, so an unknown provider refuses in one sentence, naming the known ones, and a provider with no value is reported in one. The jail resolves above the backend dispatch, so the refusal comes before either arm starts anything, exit 1, as at the host. The flag joins `runFlags` and `yolo run --help`; `jailOnlyRunFlags` leaves it out, since the host shares it. No config key or environment variable sets it, and a config key spelling it is the ordinary unknown-key refusal | 2026-10-05 | [§5.2](#52-the-jail-half---with-credentials-at-a-jail-launch-built) | ✅ `parseRunArgs`, `packload.ResolveGrant`, `GrantProviderLines`; pinned by `TestJailLaunchTakesWithCredentials`, `TestJailLaunchRefusesAnEmptyGrantValue`, `TestAJailGrantResolvesAsTheHostsDoes`, `TestAJailGrantIsImpliedByNothingElse` and the host's existing `TestWithCredentials*` cells |
| ES-D35 | *Implementation decision, reversible:* **the jail grant's disclosure.** Its own block after the gate's lines, on every entry that holds one, a grant that delivered nothing included, in three headers: a fresh container launch (`this jail holds … for its whole life … every process in it inherits them: this session, every session attached to it later, and everything each one starts`), an attach (`this jail was launched with --with-credentials <set>: it holds … as they were at its launch … this session and everything it starts inherit them`, and, when the entry typed the flag, that it asks for nothing more), and a macos-user session (`this session receives … <program> and everything it starts inherit them`). Each says keys only, that the grant selects no profile and re-points nothing, and, when an agent keeps a profile, that it does and the grant only adds keys beside it. Then ES-D15's per-provider lines. Names only. No quiet switch ([`OQ-RO3`](../reference/report-tiers.md#why-its-this-way)) | 2026-10-05 | [§5.2](#52-the-jail-half---with-credentials-at-a-jail-launch-built) | ✅ `noteHeldGrant`; pinned by the cells ES-D31 and ES-D33 name |
| ES-D36 | *Implementation decision, reversible: what the grant does not change.* A granted name is in every process's environment, so the boot's MCP `requires_env` gate and `${NAME}` interpolation see it as they see any value the jail holds, on both backends (READ FROM CODE: the container boot reads its own environment, and macos-user's `scopedMCPView` keeps a session key no agent file names), and a server configured to use it is rendered with it. That is the inheritance [CN-D8](provider-credential-scope.md#CN-D8) discloses, not a new delivery. The per-agent files keep exactly the gate's answer, so an agent whose profile selects a granted provider receives its value twice, from the jail and from its file, and the two agree. One edge, READ FROM CODE and left as built: when that key was rotated in `env_sources` after the launch, an attach's file for that agent overrides the jail's launch-time value with the current one through a `case` guard that lists the launch-time value (`inheritedValues` reads the container's frozen environment), so the old value appears in that agent's file under `<workspace>/.yolo/home` on podman and Apple Container. Dropping it from the guard would leave the agent on the stale granted value instead of its profile's. `yolo check` resolves no grant: no typed flag reaches it | 2026-10-05 | [§5.2](#52-the-jail-half---with-credentials-at-a-jail-launch-built) | — nothing to build |
