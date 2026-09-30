---
status: current
verified: 2026-09-23
verified_commit: 7ad8358c
covers:
  - internal/packdecl/contributes.go
  - internal/packdecl/envnames.go
  - internal/packdecl/platform.go
  - internal/packdecl/region.go
  - internal/packdecl/platformregion.go
  - internal/packdecl/regionfile.go
  - internal/packload/regionpreflight.go
  - internal/packload/regionfill.go
  - internal/packload/platformswitch.go
  - internal/packload/credentialscope.go
  - internal/packload/profileset.go
  - internal/cli/run/agentenvfiles.go
  - internal/entrypoint/agentenv.go
  - internal/packload/providers.go
  - internal/packload/profiles.go
  - internal/packload/deriveenv.go
  - internal/agentcfg/selection.go
  - internal/agentcfg/staterender.go
  - internal/agentcfg/luahook/derive.go
  - internal/entrypoint/packsurfaces.go
  - internal/entrypoint/prism.go
  - internal/config/profiles.go
  - internal/packoverlay/packoverlay.go
  - internal/cli/run/profilechannel.go
  - internal/cli/run/providerpreflight.go
  - packs/claude/derive.lua
  - packs/codex/derive.lua
  - packs/pi/derive.lua
  - packs/opencode/derive.lua
  - packs/copilot/derive.lua
  - packs/omp/derive.lua
  - packs/zai/pack.json
  - packs/cerebras/pack.json
  - packs/claude/pack.json
  - packs/openrouter/pack.json
  - packs/kilo/pack.json
  - packs/llamacpp/pack.json
  - packs/aws-auth/pack.json
  - packs/openai-auth/pack.json
  - packs/codex/pack.json
tags: [providers, profiles, packs, derives, selection, deselection, zai, cerebras, openrouter, kilo]
---

# The provider system — catalog, composition, and selection

**Status:** CURRENT as of 2026-09-23, verified against `7ad8358c`. It is also the as-built home
of the profile-variant design (`profiles-as-pack-variants.md`, retired): its surviving rulings
are [the profile-variant rows](#the-profile-variant-rulings) of the appendix. MEASURED: each
section was re-read against the code at `7ad8358c` — the profile and preflight sections line by
line, the per-agent spellings by spot check against the derives. UNMEASURED: Bedrock mode end to end — see
[the Bedrock example](#two-channels-split-by-payload-type).

**The deselection rule is newer than that stamp.** [Deselection](#deselection-clear-what-yolo-wrote-keep-what-the-user-wrote),
[the host-layer override](#a-selection-outranks-a-host-layer-value) and
[codex's `openai-codex` selection](#selecting-openai-codex-for-codex) came from the provider-switching design
(`provider-switching.md`, graduated 2026-09-26), and they were verified against `ca86d945` on 2026-09-26.
The rest of the doc keeps its `7ad8358c` stamp. MEASURED: the clear, its boot-log record and
the host-layer override are pinned through the boot render by unit tests in `internal/entrypoint`.
The adopting-boot failure was reproduced through the same render at `ca86d945`, and the fix that
[the clear on an adopting boot](#a-clear-holds-on-an-adopting-boot-too) describes is pinned the same
way, newer than that stamp. UNMEASURED: no live agent session has been watched across a deselect.

**The host notch's grant is newer too** (2026-09-27): the disclosure wording and the
refusal of a selection key naming a command no pack installs at `yolo host` (a `use_profiles`
key then, a `profile` key since [PP-D10](../design/providers-and-profiles-redesign.md#PP-D10)), described under
[the credential gate](#the-credential-gate), come from
[`credential-sources-separation.md`](../design/credential-sources-separation.md) ES-D2 to ES-D5, and
the remedy's corrections from ES-D10 to ES-D12. The `--with-credentials` grant is from the same
day's ruling of that doc's [OQ-ES5](../design/credential-sources-separation.md#OQ-ES5) host
half, built as ES-D13 to ES-D17. Newer still (2026-09-28), a bare `-p` reaches agent CLIs only at
the host as in a jail, `--with-credentials` being an ad-hoc command's one grant
([OQ-NC5](../plans/notch-convergence.md#OQ-NC5), which retired ES-D1).
MEASURED: pinned through `hostMain` by unit tests in `internal/cli`. UNMEASURED: no real host has
run it.

**The platform and the provider-keyed gates are newest** (2026-09-29): a provider says what
service it is ([the platform](#the-platform-what-service-a-provider-is),
[`OQ-BR2`](../design/providers-and-profiles-redesign.md#OQ-BR2)), every shipped provider fact keys
on the provider rather than on a profile's name
([`OQ-BR8`](../design/providers-and-profiles-redesign.md#OQ-BR8)), the region preflight keys on
the platform and asks each agent ([the region preflight](#the-region-preflight)), and a Bedrock
switch in the user's own Claude settings that no Bedrock provider serves is named at launch
([a switch in the agent's own config](#a-switch-in-the-agents-own-config)). MEASURED BY TESTS
ONLY: each is pinned through the credential gate and the shipped derives, and each launch arm's
call site through the code a launch runs. UNMEASURED: no launch was run and no agent started.

**One Bedrock provider for every agent is newer still** (2026-09-29, later that day):
[the shipped Bedrock provider](#the-shipped-bedrock-provider) moved into its own pack with a
model list of every maker, and codex, opencode and pi are bound to their own Bedrock clients
([`OQ-BR9`](../design/bedrock-plumbing.md#OQ-BR9), [`OQ-BR1`](../design/bedrock-plumbing.md#OQ-BR1)).
MEASURED BY TESTS ONLY: each binding through the boot render, and claude's through the assembled
launch channel. The agents' Bedrock clients were read from their installed builds and never
run. UNMEASURED: no request has reached Bedrock.

**Active sets came the same day** (2026-09-29): an agent may run on an ordered list of profiles
([an active set](#an-active-set-several-profiles-for-one-agent),
[`active-provider-sets.md`](../design/active-provider-sets.md),
[`OQ-AP1`](../design/active-provider-sets.md#OQ-AP1) to
[`OQ-AP3`](../design/active-provider-sets.md#OQ-AP3)). MEASURED: the
grammar, every refusal, the gate's delivery and pi's render are pinned by unit tests, every call
site the review cut to the set's first entry now fails one, and two integration launches in a
real jail rendered pi's files and environment for `-p pi=zai,openrouter` and
`-p pi=zai,bedrock`. The build first spelled the config list under `use_profiles`, and was
re-expressed on the `profile` key when it landed after that rename
([PP-D10](../design/providers-and-profiles-redesign.md#PP-D10)). UNMEASURED: no pi session was run,
so none switched providers. opencode took a set on 2026-09-30
([AP-D15](../design/active-provider-sets.md#AP-D15)), MEASURED the same way: unit tests over its
render, the jail's channel and `yolo host`, and two integration launches rendering its file for
`-p opencode=zai,openrouter` and `-p opencode=zai,bedrock`; no opencode session was run.

A **provider** is a declaration of a service's facts — where its endpoints are, which wire
protocol each speaks, which model aliases it offers, which environment variable holds its
credential, and which knobs ("options") a profile may tune. Providers compose into ONE table
on the host at launch, cross into the jail, and reach each agent through that agent's own
pack's derives — in that agent's own vocabulary. A **profile** is user-declared intent: a named
selection over a provider. Whatever a pack does *differently* under a profile is not part of the
profile: it is an ordinary contribution carrying the **`profile` modifier**, which gates it on
that name being active. **Catalog** (an agent's directory of providers it *could* use) and
**selection** (which one it *does* use) are two features with different triggers: catalog rides
presence, selection is an explicit act.

| Component | Lives in |
| :--- | :--- |
| Manifest schema: provider, profile, options contributions | `internal/packdecl` (`ProviderContribution`, `ProfileContribution`) |
| Table composition + credential preflight | `internal/packload` (`ComposeProviders`, `ProviderCredentialGaps`) |
| Profile lowering: declarations → resolved table | `internal/packload` (`ResolveProfiles`, `ProviderFor`) |
| Env-derive runner (both notches) | `internal/packload` (`AgentEnv`, `deriveenv.go`) |
| Lua sandbox: `yolo.derive` / `yolo.env` registrations, derive ctx | `internal/agentcfg/luahook` (`DeriveCtx`, `Derive`) |
| Selection namespace: edge-triggered apply | `internal/agentcfg` (`SelectionKey`, `ApplySelection`) |
| Surface render + selection lift | `internal/entrypoint` (`ConfigurePackSurfaces`, prism stateful render) |
| User config: `providers`, `profiles`, `profile` | `internal/config` (`profiles.go`, `profileselection.go`, `UseProfileCLINames`) |
| The `profile` modifier's two gates, and `env`'s `platform` gate | `internal/packload` (`EnvFold` over a `GateSelection`) for `env`; `internal/packoverlay` (`Collect`) for `config-overlay` |
| A provider's platform, and the platform switch an agent pack declares | `internal/packdecl` (`Contribution.Platform`, `PlatformSwitch`); `internal/packload` (`SelectionOf`, `PlatformSwitchConflicts`) |
| Launch-side profile checks, the disclosure line, the credential preflight | `internal/cli/run` (`checkProfileTargets`, `checkProfileDeclarations`, `noteUseProfiles`, `checkProviderCredentials`) |
| Host-notch composition of the same | `internal/cli` (`composeHostLaunch`, `overlayGateProfiles`) |
| The agent derives that consume the table, and the provider packs that fill it | `packs/*/derive.lua`; every pack declaring a `provider` contribution |

**Reads with:** [`pack-system.md`](pack-system.md) (what a pack is, how derives are
loaded), [`protocol-resolution.md`](protocol-resolution.md) (how an agent's declared wire
protocols and a provider's endpoints are paired, and the `adapter` contribution that supplies an
address neither side declared), [`local-model-endpoints.md`](../research/local-model-endpoints.md) (the
source-verified per-agent vocabularies the dialect maps translate into),
[`cerebras-pack-and-copilot-delivery.md`](cerebras-pack-and-copilot-delivery.md) (which agents a
provider can reach *at all*, one level below the delivery channels below),
[`host-agent-environment.md`](host-agent-environment.md) (the host notch's own delivery: `yolo
host --` and its wrapper directory), [`config-migration-to-prism.md`](config-migration-to-prism.md)
(the stateful render a selection lifts into: the captured overlay, `last_render`, and the adopting
boot, on which [deselection](#deselection-clear-what-yolo-wrote-keep-what-the-user-wrote) clears too).

---

## Principles

**P1. A closed enum is only closed at the end that enforces it.** A value yolo validates
against its own list and then writes verbatim into a third party's config file is type-safe
against a type nobody else holds. The canonical `wire_api` vocabulary is either the union of
the consumers' vocabularies **and** each consumer translates, or it is a free string. It is the
former: three canonical names, and each derive translates canonical → its agent's spelling,
emitting **nothing** for a protocol that agent cannot speak.

**P2. A rule enforced on one layer of a merge must hold on the merge's output.** An entry-level
`base_url` beside `endpoints` is refused whichever layer spells it: the config validator
refuses the key outright on the host, and the composer refuses to *manufacture* the pair from a
pack's `endpoints` and a user's shorthand — the case that still reaches it in a jail, where the
validator only warns. A rule the composer can manufacture a violation of is a lint, not an
invariant.

**A profile is user-declared intent over a provider, and the provider owns the schema of what
a profile for it may carry.** Pack-shipped profiles are defaults the user overrides, exactly as
pack-shipped providers are. Core composes facts and resolves names; it never learns what an
option *means* — the derive decides where each one lands.

**Route by payload type: configuration goes in the agent's config file, secrets and process
flags go in the process environment.** The two channels fail on different axes, so neither is a
fallback for the other. A config-surface patch survives every invocation, including the ones
yolo is not part of (an IDE, cron, an absolute path), but it cannot *deliver* a credential: every
agent's file names the variable and reads the value from its own environment. Process env
carries the value and the unsets, but only reaches a process yolo (or its host wrapper)
launches — and `yolo host apply`, which never runs a process, refuses a pack's `env`
contribution. A payload that has a config spelling rides the file; the rest rides the env; a pack
that needs both declares both.

**A pack never carries a credential value, and the schema offers no slot for one.** A pack is a
distribution artifact, fetched and approved at a commit. The only credential-shaped field is
`api_key_env_name`, which holds a variable NAME by contract, or a list of names for a provider
whose credential arrives in several variables (Bedrock's bearer, key pair and SSO pointer); the
value travels `env_sources` and is hydrated at launch, and reaches only an agent that selected
the provider ([the credential gate](#the-credential-gate)); a `base_url` carrying userinfo is
refused. This is a recommendation
backed by the schema, not a scanner — content scanning is a product category of its own, and
yolo does not ship one.

**Catalog from presence; selection by explicit act.** A provider entry that reaches an agent
lands in that agent's directory — no gate. Telling the agent to *use* it is a separate,
deliberate step, and its absence writes nothing.

## How the table composes

Composition happens ONCE per launch, host-side (`ComposeProviders`): every selected pack's
`kind: "provider"` facts laid UNDER the user's `providers` entries, merged per field — objects
merge recursively, every other value replaces, and a **null drops** at the top level and at
every depth below it, with one carve-out: a null *inside an options map* is "declared, no
default", not a deletion, because dropping a default must not un-declare the option a profile
may name.

A provider's service facts are a pack's to ship and a user's to override: endpoints and wire
protocols are the same for every user of a service, so a shareable pack carries them, while the
credential pointer is a fact about one machine ([OQ-12](#pv-oq-12)). A personal endpoint is
therefore a user `providers` entry that overrides nothing. The table stays flat — one entry per
provider — and where one key serves several wire shapes the shape axis lives inside the entry
as `endpoints.<protocol>` ([OQ-2](#pv-oq-2)).

Between the packs' providers and the user's entries, the **models pass** applies every selected
pack's `kind: "models"` contribution: each `add` appends model entries to the list of the
provider it names, in pack order, and then each `only` keeps just the ids it names, the `only`s
intersecting. A narrowed entry carries `models_only: true`, the composed fact each agent's derive
renders an exact menu from ([Model lists shaped by packs](#model-lists-shaped-by-packs)). The
user's own `providers.<name>.models` then composes over the result per alias, so your config has
the last word ([MM-D11](../design/model-lists-and-pickers.md#MM-D11)).

Last, over the finished table, the **adapter pass** fills in an endpoint for a protocol a
provider does not serve but a selected adapter converts to — below the user layer, so an address
the user wrote always wins ([`protocol-resolution.md`](protocol-resolution.md)).

Two refusals guard the output:

- A composed entry carrying both `base_url` and `endpoints` is refused, naming both sources
  (`ProviderAddressConflictMessage`, shared with the config validator so the two layers cannot
  word it differently). The entry-level `base_url` shorthand itself is retired: the config
  validator refuses it naming the explicit spelling, `endpoints.<protocol>.base_url`
  ([`protocol-resolution.md`](protocol-resolution.md#the-single-protocol-base_url-shorthand-is-removed)).
- A provider NAME claimed by two packs is refused by the launch preflight (sole ownership by
  name).

What a composed entry is required to bring — its credential — is
[the credential preflight](#the-credential-preflight)'s question, and, for a provider reached
through a region, its region is [the region preflight](#the-region-preflight)'s.

### The platform: what service a provider is

A provider declares what service it is in `platform`, an open vocabulary: `"aws-bedrock"` for
Amazon Bedrock, the one value anything reads today
([`OQ-BR2`](../design/providers-and-profiles-redesign.md#OQ-BR2), ruled 2026-09-29). A pack sets
it on its `kind: "provider"` contribution, and a user sets it on a `providers.<name>` entry,
their own or a pack's, from user scope only: the platform decides which agents receive a pack's
credential pointer, so a workspace file carrying it is refused. Only its shape is checked (one
token, no whitespace), and a value nothing reads changes nothing.

It is how a derive recognizes a service without matching a provider's NAME, so a provider you
declare gets the behavior the shipped one does. It composes into the entry as a field like any
other, and a derive reads the selected provider's as `ctx.selected_platform`. These readers key
on `aws-bedrock` today:

- **claude's derive** turns on Claude Code's own Bedrock client (`CLAUDE_CODE_USE_BEDROCK=1`, in
  its env and in `claude/settings`) when claude's selected provider declares it and its profile
  routes through no via service ([the worked example](#two-channels-split-by-payload-type));
- **codex's, opencode's and pi's derives** bind their agents' own Bedrock clients on the same
  two conditions, and give a via profile over such a provider their via rows instead
  ([the shipped Bedrock provider](#the-shipped-bedrock-provider));
- **aws-auth's credentials pointer** is an `env` contribution with a `platform` gate
  ([the `profile` modifier](#the-profile-modifier)), so it reaches each agent on a Bedrock
  provider, whatever its profile is named;
- **the region preflight** requires a region of every provider of a platform some pack declares
  region variables for ([the region preflight](#the-region-preflight)).

So `"providers": {"bedrock-eu": {"platform": "aws-bedrock", "region": "eu-west-1"}}` with a profile
over it, or a profile `bedrock-sso` over the shipped `bedrock`, gets all of them, as `-p bedrock`
does. **The credential claims follow the platform too**
([PP-D9](../design/providers-and-profiles-redesign.md#PP-D9)): a provider that declares a
`platform` and no `api_key_env_name` of its own claims every variable a provider of the same
platform in the composed table lists. So `bedrock-eu` claims the six AWS variables the shipped
`bedrock` lists, and the [credential gate](#the-credential-gate) delivers an `env_sources` AWS key
to the agents on either provider and to no other process. Before, `bedrock-eu` claimed nothing
while `bedrock` still claimed all six, so the gate withheld them from every process, the agent on
`bedrock-eu` included. A provider that lists its own `api_key_env_name` claims exactly that list
and inherits nothing. ⚠ `platform` is not `platforms` (on `program` and `service`), which lists
the host OS/arch pairs a build exists for.

### The shipped Bedrock provider

One provider, `bedrock`, is Amazon Bedrock's `bedrock-runtime` endpoint for every agent, and it
lives in its own pack, [`packs/bedrock`](../../packs/bedrock/README.md), which installs no program
([`OQ-BR9`](../design/bedrock-plumbing.md#OQ-BR9), ruled 2026-09-29). Every agent pack whose
derive binds Bedrock `needs` that pack, so `"packs": ["codex"]` alone carries `-p bedrock`:
claude, codex, opencode and pi. The pack in turn needs `aws-auth`. copilot, oh-omp and agy need
neither, having no Bedrock client of their own, so a `-p bedrock` in a jail of them alone is
refused as a profile nothing declares.

- **The provider** declares `"platform": "aws-bedrock"`, `region_env_name` `AWS_REGION` and
  `AWS_DEFAULT_REGION`, a `region_file` naming `~/.aws/config`, the six AWS credential names
  under `api_key_env_name`, no endpoint, no region and no `options`. A region is the user's to
  set, on the provider, in the environment or in the profile's section of `~/.aws/config`
  ([the region preflight](#the-region-preflight), [the region file](#the-region-file)).
- **The model list** is one list of every maker's models, each entry keyed by its runtime id and
  naming its maker as `vendor` in `model_options`, beside `order`, `name`, `context_window`,
  `max_tokens` and `input`. *Vendor* is the model's maker, a term coined in
  [`bedrock-plumbing.md`](../design/bedrock-plumbing.md#61-the-provider-shape-one-bedrock-provider-or-two):
  one lowercase token, read by derives and interpreted by no core code, never parsed from the id.
  A user's object-form entry takes `vendor` too
  (`"kimi": {"id": "global.moonshotai.kimi-k3", "vendor": "moonshotai"}`), and an entry with no
  vendor is offered to every agent. The list names no `default` alias.
- **Which entries an agent picks among** is decided by its own derive, from the makers, as each
  entry declares them, that its client is known to serve: claude's Bedrock client Anthropic's
  (Messages serves Claude only), codex's OpenAI's (it drives Responses), opencode's and pi's
  every maker's (Converse). The filter is by declared maker, not by what the client could call:
  codex skips another maker's model whose AWS page lists Responses, until a turn measures one.
  pi and opencode list the entries in their model menus; claude's and codex's menus are not
  shaped yet ([OQ-BR13](../design/model-lists-and-pickers.md#OQ-BR13)).
- **Which model an agent starts on**: the profile's `model` when it names an entry that agent can
  call, as an alias or an id, or an id the provider does not list, which is passed through; else
  the provider's `default` alias when that agent can call it; else the first entry it can call,
  in `order`. A listed entry the agent cannot call is skipped, never sent. claude's own client is
  the exception: with nothing named, yolo pins no model, because Claude Code starts on an
  Anthropic model of its own, a valid session yolo does not steer
  ([`OQ-ML2`](../design/model-lists-and-pickers.md#OQ-ML2)).

Each agent's binding, written only for the selected Bedrock provider and only on the agent's own
transport. A Bedrock provider never gets an agent's generic catalog row, whose one credential is
a key while Bedrock's is the AWS chain ([BR-D10](../design/bedrock-plumbing.md#BR-D10)):

| Agent | Client | What the derive writes |
| :--- | :--- | :--- |
| claude | `CLAUDE_CODE_USE_BEDROCK` | the switch, `AWS_REGION` from the provider's `region`, and a model only as above |
| codex | built-in `amazon-bedrock-runtime` | `model_provider = "amazon-bedrock-runtime"`, `model`, and `[model_providers.amazon-bedrock-runtime.aws] region` only for a provider-declared region; no other field, since codex refuses them |
| opencode | built-in `amazon-bedrock` | `provider["amazon-bedrock"]` with the list's models and `options.region` only for a provider-declared region, no `npm` and no endpoint; `model` and `small_model` `amazon-bedrock/<id>`, `enabled_providers` naming it; as a later entry of an [active set](#an-active-set-several-profiles-for-one-agent), the same row, and `amazon-bedrock` in `enabled_providers` after the entries before it |
| pi | built-in `amazon-bedrock` on Converse | `providers["amazon-bedrock"].models` in models.json with each entry's facts and no `baseUrl`, `api` or `apiKey`; `defaultProvider`/`defaultModel`, `enabledModels` and pi-subagents' policy over the list; `AWS_REGION` from the provider's `region` in pi's environment |

opencode reads `AWS_REGION` and not `AWS_DEFAULT_REGION`, and falls back to `us-east-1`, so its
pack says so under `platform_regions` and [the region preflight](#the-region-preflight) counts
`AWS_REGION` alone for it: with only `AWS_DEFAULT_REGION` delivered and no `region` on the
provider, an opencode launch is refused ([BR-D18](../design/bedrock-plumbing.md#BR-D18)). ⚠ A pi row replaces
pi's own catalog entry of the same id, so pi takes the list's facts for it and loses its own cost
and thinking levels.

**`bedrock-bridge`** is the pack's second profile, `{provider: bedrock, via: wire-bridge}`, the
one shipped way to force the wire bridge ([`OQ-BR1`](../design/bedrock-plumbing.md#OQ-BR1)).
Under it no agent runs its own Bedrock client: codex, opencode, pi and oh-omp get their via rows
([routing a profile through the bridge](#routing-a-profile-through-the-bridge-via)), and claude
and copilot are routed at the bridge's adapter address, which composition gives `bedrock` for
this profile alone ([BR-D16](../design/bedrock-plumbing.md#BR-D16),
[WG-I26](../design/wire-bridge-gateway.md#WG-I26)). The provider names no address, so the bridge
reaches `bedrock-runtime`'s own `/openai/v1` in the region, the provider's `region` or else the
served agent's `AWS_REGION` then `AWS_DEFAULT_REGION`, and signs every request itself
([WG-I37](../design/wire-bridge-gateway.md#WG-I37) to
[WG-I39](../design/wire-bridge-gateway.md#WG-I39)). The address is marked `for_via` in the
composed table: it is no endpoint for a profile without the via, so `-p bedrock` reaches each agent
exactly as before, and codex, opencode and pi are not refused over an address they cannot speak.
copilot, which has no Bedrock client, starts on the list's first model.

## The credential preflight

A launch refuses when a provider some agent's profile **selects**, and its table **catalogs**,
has no deliverable credential. A composed entry carrying at least one endpoint
([OQ-PT4](#oq-pt4)) and selected by an agent demands that the ONE variable its
`api_key_env_name` points at be set in what the launch would deliver. Three consequences follow:

- The scope is the **selected provider** ([`OQ-CN3`](../design/provider-credential-scope.md#OQ-CN3),
  ruled 2026-09-26, narrowing the selected-pack scope of [OQ-13](#pv-oq-13) deliberately). The
  credential gate delivers a provider's key only to an agent that selected it, so a key for a
  provider nobody selected is a key nobody will deliver — and a key nobody will deliver is not a
  missing credential. A selected pack whose provider no agent selects still catalogs its entry;
  what an agent does with a catalog row it has no key for is its own (pi hides it, and opencode
  is narrowed to its selected provider, [below](#the-credential-gate)).
- An entry with no endpoint (Bedrock: the ambient AWS chain) demands nothing, and a
  `null`-dropped provider leaves the table and stops being required. A cataloged entry that
  points at no single variable — none, or a list of several — demands nothing either.
- A profile's `provider` creates no requirement of its own: a provider the table does not hold
  reaches no derive, so there is no delivery to demand a key for.

Every entry of an [active set](#an-active-set-several-profiles-for-one-agent) is selected, so
each entry's key is demanded, and a missing one refuses the whole launch: yolo never starts an
agent on the entries that have keys. The fact names the entry's position ("profile openrouter is
entry 2 of pi's profiles (zai, openrouter)"; `packload.ProviderCredentialGapsIn`, which both
notches call). The region pre-flight asks each entry too, and the region fill reads the host's
`~/.aws/config` for the first entry that needs a region, so a Bedrock entry after the first is
given its profile's region as a primary one is (`AgentDelivery.RegionFileFor`).

**In a jail the key must reach each agent on the provider**, through what that agent receives:
`env_sources`, a selected pack's `env`, the profile's provider environment, or, on a container,
the argv's `-e` pairs. The shell yolo was launched from reaches no process of a jail
([BR-D2](../design/bedrock-plumbing.md#BR-D2)), so a key left only there counts for an agent
whose env derive copies its value into the agent's own environment (claude's
`ANTHROPIC_AUTH_TOKEN`, copilot's `COPILOT_PROVIDER_API_KEY`) and for no other. opencode, pi and
codex read the variable itself, so for them the refusal names the variable as left in that shell
and offers `env_sources` (`packload.ProviderCredentialGapsTo`,
[CN-D25](../design/provider-credential-scope.md#7-decision-ledger)); until 2026-09-30 the check
counted that shell for every agent, and those three started with no key. At `yolo host` the agent
inherits that shell, so it counts there.

The refusal names the provider, the pack that shipped it (or the user config, when only the
user's entry put it there), the variable, and **every channel consulted** — the `env_sources`
entries and the invoking environment. That last line exists because `env_sources` fails open (a
missing file warns and skips) while the configuration side fails closed; without it a user is
told only that a key never arrived, not which channel was meant to bring it.

`YOLO_ALLOW_MISSING_PROVIDERS=1`, forwarded from the host environment, is the escape hatch the
refusal names. It turns the refusal into a **loud continuation** — the notice says nothing was
repaired — never into silence. Both notches run the check, on every entry that delivers an
environment: the jail launcher on a fresh launch, on an attach that rewrites the per-entry
channel (below), and on every macos-user invocation; `yolo host --` on the environment it is
about to exec, before it resolves the target. The composition is `ProviderCredentialGaps` in
`internal/packload`; each notch supplies its own lookup and its own voice. A second check runs
just before it on the jail notch, at the same three entries: a launch that delivers a selected
pack's `kind: "env"` contribution beside something that pack declares under `overridden_by` is
refused, with no hatch, or warned about and let through when the pack declares that override
`certain: false` (`EnvOverrideFindings`, also in `internal/packload`). Core names no variable
there. `packs/aws-auth` declares the three for its credentials pointer: a Bedrock bearer and a
static key pair refuse, and a `~/.aws` grant warns
([`sso-backed-bedrock.md` OQ-SSO8](../design/sso-backed-bedrock.md#OQ-SSO8)).

## The region preflight

A launch refuses when an agent's profile **selects** a provider that is reached through a region
and the launch can see no region reaching that agent
([`OQ-BR6`](../design/bedrock-plumbing.md#OQ-BR6), ruled 2026-09-25). Without it codex fails at
its first Bedrock request, and claude, opencode and pi silently use `us-east-1`.

- **Which providers.** Every provider whose composed entry declares a `platform` that some
  selected pack says is reached through a region
  ([the platform](#the-platform-what-service-a-provider-is)). A pack says so by declaring
  `region_env_name` beside `platform` on a provider it ships: the variables an agent on that
  platform reads its region from. packdecl refuses the field without a `platform`. The bedrock
  pack's `bedrock` declares `AWS_REGION` and `AWS_DEFAULT_REGION` for `aws-bedrock`, so a
  provider you declare yourself with `"platform": "aws-bedrock"` is required a region from the
  same two variables, with nothing restated ([BR-D1](../design/bedrock-plumbing.md#BR-D1)). A
  provider with no platform, or one whose platform no selected pack declares variables for,
  requires nothing. Core names no provider, platform or variable.
- **What counts as a region.** The composed entry's `region`, which a user's `providers` entry
  sets from either scope (`"providers": {"bedrock": {"region": "eu-west-1"}}`), or one of the
  declared variables, non-empty, in what the launch delivers to **that agent**. In a jail that is
  the `env_sources` the credential gate delivers to it, a selected pack's shared `kind: "env"`,
  its own gated env and provider environment, and, on a container, the argv's `-e` pairs, which
  every process inherits. A value only another agent receives does not count
  ([BR-D4](../design/bedrock-plumbing.md#BR-D4)). Nor does a variable the agent does not read:
  an agent's own pack may list, under its program's `platform_regions`, the variables that
  agent reads on a platform, and the preflight then counts only those for it. packs/opencode
  lists `AWS_REGION` for `aws-bedrock`, so `AWS_DEFAULT_REGION` alone is no region for opencode,
  and the refusal says it reached opencode unread
  ([BR-D18](../design/bedrock-plumbing.md#BR-D18)). Under a profile that routes the agent
  through the wire bridge (`bedrock-bridge`), the bridge reads the region in the agent's place,
  from every variable the provider lists, so there `AWS_DEFAULT_REGION` counts for opencode too
  ([WG-I38](../design/wire-bridge-gateway.md#WG-I38)). It is never the shell yolo was launched from,
  which no backend forwards; a region found only there is named in the refusal as not
  delivered. At `yolo host --` the exec'd environment includes that shell, so it counts
  ([BR-D2](../design/bedrock-plumbing.md#BR-D2)).
- **The region file.** An agent that none of the above gives a region is given the one its
  platform's region file holds, before the preflight asks, in a jail and at `yolo host` alike
  ([BR-DIR1](../design/bedrock-plumbing.md#BR-DIR1); see [the region file](#the-region-file)).
  The preflight then counts it as a delivered variable. When the file gives none, the refusal
  names the file, the profile and why, and offers the file as a third way to set a region.
- **Scope.** As the credential preflight's: a provider nobody selects, and an entry a `null`
  dropped, demand nothing.

### The region file

A provider pack may declare, beside `region_env_name`, where its platform's agents keep a
region the environment does not carry: `region_file`, one key in one profile's section of a
file under the home directory of the machine yolo launches on. The bedrock pack declares AWS's
shared config for `aws-bedrock`: the `region` key of `[profile NAME]` in `~/.aws/config`, which
`AWS_CONFIG_FILE` relocates. The profile named `default` has two spellings, and `[profile
default]` takes priority: `[default]` is read only in a file with no `[profile default]`, as the
AWS SDKs claude and codex use read it. Core names no AWS file, section or variable
([BR-D21](../design/bedrock-plumbing.md#BR-D21)).

- **When.** Only for an agent on a provider of that platform whose composed entry sets no
  `region` and to which none of the platform's region variables reaches (at `yolo host`,
  counting the invoking shell it inherits). One the agent does not read counts here too: opencode
  receiving only `AWS_DEFAULT_REGION` is not given the file's region in its place, which may
  differ, and is refused as the preflight says
  ([BR-D23](../design/bedrock-plumbing.md#BR-D23)). The region is delivered in the first
  variable the agent reads: `AWS_REGION` for every shipped agent.
- **Which profile.** The one the agent's credential comes from
  ([BR-D21](../design/bedrock-plumbing.md#BR-D21)): when an `env` contribution declaring
  `region_profile_setting` reaches the agent, the setting it names of the loophole it is
  `served_by` (aws-auth's pointer names `loopholes.aws-auth.settings.profile`, the profile
  aws-auth mints for); otherwise the pack's `profile_env_name` (`AWS_PROFILE`) as the agent
  receives it; otherwise the `default_profile`. Only that profile's own section is read, so an
  `[sso-session]` block's `sso_region`, the SSO portal's region, never is.
- **Left in the launching shell, in a jail.** The shell yolo was launched from reaches no
  jail's agent ([BR-D2](../design/bedrock-plumbing.md#BR-D2)), so a region variable or an
  `AWS_PROFILE` other than `default` set only there is a choice this launch does not carry. The
  file is not read in its place, since the default profile's region may not be the one meant, and
  the launch is refused, naming the variable and, for a profile, offering to deliver it through
  `env_sources`. A profile aws-auth serves decides before the shell's `AWS_PROFILE` does. At
  `yolo host` the shell is the agent's, so both count there.
- **Where the file is.** `path_env_name` (`AWS_CONFIG_FILE`) in the environment yolo was
  launched from, with a leading `~` expanded, else `path` under that environment's `HOME`, which
  is where the host's own config is
  ([BR-D24](../design/bedrock-plumbing.md#BR-D24)). It is read in Go, in AWS's documented INI
  format, with no `aws` process on the launch path
  ([BR-D22](../design/bedrock-plumbing.md#BR-D22)).
- **The value.** One DNS label, as a provider's `region` is ([below](#a-region-is-a-host-name-part));
  anything else is not delivered, and the refusal says so.
- **A disclosed host read.** yolo reads the file on the host on the pack's word, so the
  declaration is a review-worthy `reads-host` claim: `yolo pack footprint` lists it, and every
  launch that selects the pack names it under "Pack environment this launch", saying the file is
  read and not mounted, and that only the region reaches the agent. The last selected pack that
  declares a `region_file` for a platform is the one read for it.
- **Delivery and disclosure.** The credential gate appends it to the agent's own environment,
  so it rides the agent's env file in a jail, the session on `macos-user` and the exec'd
  environment at `yolo host` ([BR-D20](../design/bedrock-plumbing.md#BR-D20)); in a jail's env
  file it is a default, so a value exported at the agent's own launch wins, as does one the
  container's own environment carries, on an attach as on the fresh launch. Every notch prints
  one line: `Region: AWS_REGION=<region> for <agents> on provider "<name>", read from
  <file> [<section>] (profile "<profile>", <what chose it>): the provider sets no region, and no
  region variable reaches <them>` ([BR-D25](../design/bedrock-plumbing.md#BR-D25)). At
  `yolo host` the agent could read the file itself, and is given it anyway, so the refusal and
  the line are the same at every notch ([BR-D26](../design/bedrock-plumbing.md#BR-D26)).

### A region is a host-name part

A provider's `region` is **one DNS label**: lowercase letters and digits with single hyphens
between them, at most 63 characters (`us-east-1`, `us-gov-west-1`). Anything else is refused at
every scope: a pack manifest, the user config, and a workspace config, whose file is checked on
its own as well as in the merged map. The reason is where the value goes. Claude Code and
opencode 1.18.32 build their Bedrock address as `https://bedrock-runtime.${region}.amazonaws.com`
with no check of their own (read from their shipped binaries on 2026-09-29, never run), so a
workspace `region` of `attacker.example/#` would have sent every prompt, every file the agent
read and the Bedrock credential to `bedrock-runtime.attacker.example`. A label can name a host
only inside the domain the agent appends. yolo keeps no list of regions: which exist, and which
serve which model, is AWS's to change. The rule is `packdecl.RegionProblem`, read by
`config.validateProviderRegion` and the manifest validator.

The refusal names the pack, the provider and its platform, the agents on it that receive no
region, the region file and profile it read and why they gave none, every way to set a region
and every channel consulted.
It honors the credential preflight's hatch, `YOLO_ALLOW_MISSING_PROVIDERS=1`
([BR-D3](../design/bedrock-plumbing.md#BR-D3)), and runs wherever that preflight runs: the jail
launcher's `checkProviderCredentials` asks both, so the fresh launch, the attach and every
macos-user invocation do, and `yolo host --` asks it before resolving the target. The facts are
`ProviderRegionGaps` in `internal/packload`, and the wording `ProviderRegionRefusal`.

## What crosses to the jail

Three environment variables, all delivered on every **entry** — a fresh launch and an
attach alike — through the **channel section** of `yolo-user-env.sh` (0600, live-mounted,
rewritten whole by each entry; `writeUserEnvFile` in `internal/cli/run`):

- `YOLO_PROVIDERS` — the composed table, **secret-free** (`api_key_env_name` carries the NAME
  of a variable, never a value).
- `YOLO_USE_PROFILES` — the effective selection: CLI name → profile name, or a list of them for
  an agent holding an [active set](#an-active-set-several-profiles-for-one-agent) of more than
  one (a list of one crosses as the plain name), the `profile` key and `-p` already folded, so
  no `"*"` crosses.
- `YOLO_PROFILES` — the RESOLVED profile table: name → `{provider, <options>}`, the output of
  the one lowering (below). In-jail derives and the host notch read the same resolved shape;
  no user-config parsing happens in-jail.

The same section carries the SHARED pack env fold (every pack's unconditional `kind: "env"`)
as plain-form `export K='v'` lines, which the boot's hydrate applies OVER the environment —
the def-form `export K=${K:-'v'}` lines above them (the env_sources no provider claims) keep
the opposite precedence. What the credential gate scopes to ONE agent — its provider's claimed
env_sources, the gated env its selection satisfies, and its env derive's shape vars
(the `ANTHROPIC_*` / `COPILOT_*` blocks, credential included) — is not in this file at all;
it crosses in that agent's own env file ([the credential gate](#the-credential-gate)). These
files, not the `podman run` argv, are the channel's only container-side crossing: an argv `-e`
would freeze one launch's providers into the container's
environment, where every later `podman exec` inherits them as stale state — the failure
per-entry delivery exists to prevent (`yolo -p <name> -- claude` against a running jail
must deliver THIS entry's profile; the attach rewrites the file and the exec'd boot
re-hydrates it). Consequences worth knowing:

- A provider credential no longer rides a `ps`-visible argv line; it lands in a 0600
  file — the selecting agent's own. The argv-exposure trade-off this reference
  used to record is retired by the same move.
- **An attach composes its channel over the packs the running jail booted with**, not the
  configured ones, since a running jail keeps its pack tree
  ([`OQ-PK2`](pack-system.md#oq-pk2)). A selection only a newly configured pack can satisfy —
  a profile only it declares — refuses the attach before anything is written, naming the
  restart.
- **An attach first asks whether the running jail can receive this delivery**, by comparing
  the contract tags the container froze at launch (`YOLO_CONTRACT_TAGS`) with the tags this
  entry needs ([`attach-skew-and-contract-guardrails.md`](../design/attach-skew-and-contract-guardrails.md#what-was-built-2026-09-26)).
  When one is missing it never proceeds on its own. At a terminal it asks
  `Restart jail now? [Y/n]`, naming the sessions the restart ends, and a restart continues
  as a fresh launch. Elsewhere, or when the question is declined, it refuses and names the
  two-command series, `yolo stop` and then an ordinary launch. `YOLO_ALLOW_ATTACH_SKEW=1`
  proceeds but delivers nothing, so the jail keeps what its last entry gave it, and says
  so on stderr and in the briefing it refreshes for the session
  ([SK-D15](../design/attach-skew-and-contract-guardrails.md#decision-ledger)). Typed `-p` and config-side selections are treated alike. Two older jails
  lack a tag:
  - A jail launched BEFORE the file crossing carries the tables in its frozen environment,
    which its older entrypoint lets beat the file. It lacks `entry-channel`, so an entry
    selecting a DIFFERENT table than the frozen one takes the disposition. A matching or
    empty selection is a plain re-entry: nothing is needed, nothing is written, and nothing
    is said.
  - A jail launched after that but BEFORE [the credential gate](#the-credential-gate)
    reads the shared file on every entry but has no per-agent env directory, and its
    launchers source none. It lacks `agent-env-files`, which a jail the gate's first build
    launched shows by its legacy `YOLO_AGENT_ENV_FILES=1` instead. An entry that scopes
    nothing to any agent needs no such tag and delivers as usual. One that does takes the
    disposition, and no path prints the gate's "`… only`" disclosure for a delivery the jail
    cannot receive.
- The macos-user backend has no attach and no frozen copy; it still layers the same
  channel into its per-invocation plan env, narrowed to the one program it launches.

The three are a launcher↔jail contract: a change to any of them must move both halves in one
commit. The source-skew gate cannot see env-var contracts — and the FILE contract is the
same hazard one layer down.

## The credential gate

A profile's credentials and gated env reach **only the agent that selected it**
([`OQ-BR4`](../design/provider-credential-scope.md#OQ-BR4), ruled 2026-09-25;
[`OQ-CN1`–`OQ-CN6`](../design/provider-credential-scope.md#6-open-questions), ruled
2026-09-26). One function decides it — `packload.ScopeCredentials`, called by the jail notch's
`composePackChannel` and by the host notch's `composeHostVars` — over three kinds of value:

| Value | Who receives it |
| :--- | :--- |
| An `env_sources` value whose name a composed provider **claims** (lists in its `api_key_env_name`, or, for a provider that lists none and declares a `platform`, a same-platform provider lists: [the platform](#the-platform-what-service-a-provider-is)) | each agent whose selected profile — any entry of its [active set](#an-active-set-several-profiles-for-one-agent) — resolves to a claiming provider; no other process, a bare shell included. The disclosure names a set's keys in set order |
| An `env_sources` value no provider claims (`GH_TOKEN`, anything else) | every process, as before |
| A gated `kind: "env"` contribution | the pack's own agent when its selection satisfies the gate; for a pack that installs no CLI (`aws-auth`), every agent whose selection does. A `platform` gate is satisfied by the selected provider's platform, a `profile` gate by the profile's name ([the `profile` modifier](#the-profile-modifier)), either one by any entry of an active set |
| An env derive's output (the shape vars) | its own agent, and the derive's copy of the table carries the `api_key` of that agent's provider only, or of each provider in its active set |

`packs/bedrock`'s `bedrock` provider claims `AWS_BEARER_TOKEN_BEDROCK`, `AWS_ACCESS_KEY_ID`,
`AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN`, `AWS_PROFILE` and
`AWS_CONTAINER_CREDENTIALS_FULL_URI`, so an AWS pair hydrated for one agent's Bedrock profile no
longer reaches pi's Bedrock support (or any shell) unless pi selected Bedrock. A provider with
ONE variable keeps the string spelling; a derive sees `api_key_env_name` only when it names
exactly one variable (`packload.ProvidersForDerive`), so a multi-route provider points an agent
at none of them.

Where each answer lands is the vehicle's:

- **Container backends.** `yolo-user-env.sh` carries the shared values; each profiled agent's
  own values go to `<workspace>/.yolo/home/agent-env/<agent>.sh` (0600, in a 0700
  directory), bound `:ro` at `~/.config/yolo-agent-env/` on podman. Apple Container binds
  `<workspace>/.yolo/home` itself as the home, so there the files are written straight to
  `<workspace>/.yolo/home/.config/yolo-agent-env/`, with the same modes, on every entry. The agent's
  launcher in `~/.yolo/bin/launch` sources its own file immediately before the pre-launch
  authentication step and the exec (`agentEnvShellFn` in `internal/entrypoint`). An agent the
  image, the store package farm or a declared mise tool provides gets no launcher, so it gets
  the transparent wrapper instead, with its launch flags if it has any and with none if not,
  whenever the entry wrote it a file; the wrapper sources the file too. An attach rewrites the directory whole, so an agent that
  lost its profile loses its file. The wire bridge reads a served agent's key from that agent's
  file (`resolveKey` in `internal/wirebridged`).
- **macos-user.** One command per invocation, so the session env is **per launch**: it
  carries the shared values plus the launched program's own. `macosuser.buildPlan` no longer
  hydrates `env_sources` itself. Every other profiled agent gets its own values from its own
  env file, which the launch writes with the container writer into
  `<workspace>/.yolo/home/config/yolo-agent-env/` (0600, in a 0700 directory), where the
  bootstrap's home layout links the sandbox's `~/.config`. So an agent started from a bare
  `yolo`'s login zsh sources its profile's values, as its container twin does
  ([`OQ-CN9`](../design/provider-credential-scope.md#OQ-CN9), built). No file is written on a
  dry run.
- **The host notch.** `yolo host -- <cmd>` composes one process: the shared values plus that
  command's. The shell it inherits is the user's and passes through untouched. `yolo host env`
  prints the same one-command slice for a shell to eval (`--agent`, default `claude`), and its
  disclosure goes to stderr.
  - **A profile reaches agent CLIs only, as in a jail.** A bare `-p <name>` selects for the
    launched command only when a selected pack installs it, so `yolo host -p zai -- pi` is pi
    on zai, and `yolo host -p zai -- bash` is refused before anything runs, naming the grant
    spelled for that command: `yolo host --with-credentials zai -- bash`. That grant, keys
    only, is the one way an ad-hoc command receives a provider's claimed `env_sources` values,
    disclosed as `ZAI_API_KEY (provider zai): bash only`, and
    `eval "$(yolo host env --with-credentials zai)"` puts them in the current shell. No pack's
    env derive runs for a name no pack installs, and a CLI-less pack's gated env (`aws-auth`'s
    pointer) does not fire for it
    ([`OQ-ES7`](../design/credential-sources-separation.md#OQ-ES7), open). A `profile` key
    naming a command no resolvable pack installs is refused here with the validator's message,
    as `yolo check` and every jail launch refuse it
    ([OQ-NC5](../plans/notch-convergence.md#OQ-NC5), which retired ES-D1;
    [ES-D2 to ES-D5](../design/credential-sources-separation.md#10-decision-ledger)).
  - **Its disclosure says what this notch can do about it.** For an ad-hoc command a withheld
    line names the grant, `yolo host --with-credentials <provider> -- <cmd>`. For an agent it
    names a typed `-p` that would deliver it, with a declared profile that resolves to the
    claiming provider and that the agent can run on, or says to declare one under `profiles`
    when none does. The line is checked the way the launch it names would be, so it never names
    a launch that refuses. With `"packs": ["claude", "cerebras"]`, claude cannot run on the
    openai-only cerebras profile here: wire-bridge joins through claude's `needs`, as it does in
    a jail, but the host composes none of the bridge's addresses (next bullet), and listing it
    in `packs` changes nothing. The line names
    `yolo host --with-credentials cerebras -- bash` instead, and says why. On an agent, the named
    `-p` replaces the agent's own profile, and the line says so ("run claude on the zai profile
    for one launch, replacing its bedrock profile"). At `yolo host env` the shell spelling is
    always `eval "$(yolo host env --with-credentials <provider>)"`, never the verb's own agent
    with a `-p`, whose slice would export that agent's whole provider shape into the shell. A withheld name the
    invoking shell also exports is disclosed as not added by yolo, the shell's own value
    passing through. A withheld name yolo sets from another source, such as a pack's `env`,
    is disclosed as not delivered from `env_sources`, the process holding that source's value.
    Neither line calls the name withheld, because the command holds it anyway
    ([ES-D10 to ES-D12](../design/credential-sources-separation.md#10-decision-ledger)).
  - **A profile the wire bridge serves starts the bridge for its one command.** `yolo host
    -p cerebras -- claude`, and `-p codex -- claude` on a bare `"packs": ["claude"]`, start
    the bridge's host half as a child of that launch, on a loopback port it picked, and point
    claude at it with the launch's caller token as `ANTHROPIC_AUTH_TOKEN`; the bridge stops when
    claude exits ([`wire-bridge.md`](wire-bridge.md#at-the-host-notch),
    [`host-notch-services.md`](../design/host-notch-services.md)). The launch-chosen port
    overrides the manifest's and a user's `adapters` override. `yolo host env` refuses such a
    profile, naming the `yolo host --` spelling, because an environment script owns no process
    for the bridge to live beside ([OQ-HS3](../design/host-notch-services.md#OQ-HS3)). A
    bridge this launch cannot start refuses, naming why and the container jail where the profile
    works ([HS-D5](../design/host-notch-services.md#HS-D5)). Copilot, which also speaks openai,
    still runs on cerebras's own endpoint, and starts no bridge.
  - **`--with-credentials` grants keys by provider, for one run.**
    `yolo host --with-credentials zai,cerebras -- <cmd>` hands the command those providers'
    claimed `env_sources` values, and `all` names every composed provider that claims a value
    there. `eval "$(yolo host env --with-credentials all)"` exports them into the current shell;
    with neither `--agent` nor `-p` that script is an ad-hoc command's slice, so no agent's
    shape rides along. With `-p` it is the slice `-p` composes, plus the keys
    ([ES-D21](../design/credential-sources-separation.md#10-decision-ledger)).
    The grant is keys only: it selects no profile, runs no derive, re-points no base URL, and
    the credential pre-flight asks nothing of a granted provider. The gate delivers it
    (`ScopeInput.Grants`), so its lines name the command as the recipient, and a
    `Credential grant` block follows on every run given the flag. The block names each
    provider's delivered names, or says a named provider delivered nothing. The scope block's
    rule line then names the grant beside the profile. A withheld line on such a run names the
    same run with the claimant added to the grant (`--with-credentials zai,cerebras`, keeping a
    typed `-p`), never a `-p` that would drop the grant
    ([ES-D22 and ES-D23](../design/credential-sources-separation.md#10-decision-ledger)). An unknown provider
    refuses, naming the composed ones. It combines with `-p`: an agent keeps its profile and
    also receives the granted keys. Only the typed flag grants. `-p`, the `profile` key, a
    `YOLO_ALLOW_*` variable and config cannot, and a jail launch given the flag refuses as
    host-only ([OQ-ES5](../design/credential-sources-separation.md#OQ-ES5), ruled for the host;
    [ES-D13 to ES-D17](../design/credential-sources-separation.md#10-decision-ledger)).

Every arm discloses what it scoped or withheld, by name and never by value
(`CredentialScope.Disclosure`; the host notch adds its remedy and its shell note through
`CredentialScope.DisclosureWith`). The files are readable by every process of the jail's uid, as
the shared file is: the gate decides what each agent's **environment** carries, and an agent
started by another agent inherits that agent's environment, as any child does.

Two consequences to know:

- **The loopback credential services follow the selection** ([`OQ-CN7`](../design/provider-credential-scope.md#OQ-CN7),
  built). `aws-auth`'s adapter (`127.0.0.1:1461`, or a port the launch picked on a jail
  sharing its launcher's network namespace) starts only when some agent's selected provider
  declares the platform `aws-bedrock` (`-p bedrock`, a profile of your own over it, or a
  Bedrock provider of your own); a fresh launch that leaves it out says so, naming a profile
  that would start it. Its caller token is scoped: the only
  exported copy is `AWS_CONTAINER_AUTHORIZATION_TOKEN` in each selecting agent's env file,
  which the AWS SDK sends as `Authorization`, and the adapter refuses a request without it, so
  a bare shell or another agent is refused. The token is still a same-uid file read away:
  the agent's file and an unexported record in the shared file hold it. The wire bridge
  publishes a route only for a provider some agent's profile selects, but its caller token is
  shared, so any jail process can still use a published route.
- **Your own value beats a profile's composed one** ([`OQ-CN8`](../design/provider-credential-scope.md#OQ-CN8),
  built). The agent's launcher sources its file after the shell you typed the command in, so
  each composed value is written against that incoming environment: `ANTHROPIC_MODEL=x
  claude`, or `export ANTHROPIC_BASE_URL=…` before it, keeps your value. The profile's value
  still replaces an empty one and one yolo itself set elsewhere (the shared file, the
  container's frozen environment, another agent's file), so a stale inherited value does not
  win. A derive's tombstone removes only such a value, too. This is the per-agent file's rule.
  The host notch applies its composition over the shell it inherits, so there a profile's
  composed value replaces one your shell exports; whether the host should keep yours is
  [OQ-NC13](../plans/notch-convergence.md#OQ-NC13), and which of yolo's own sources wins when two
  set one variable, which the vehicles answer differently today, is
  [OQ-NC12](../plans/notch-convergence.md#OQ-NC12). The menu half of
[`OQ-CN4`](../design/provider-credential-scope.md#OQ-CN4) is each agent's own key:
opencode's derive writes `enabled_providers: [<selected provider>]` beside its selected model,
or every provider of its [active set](#an-active-set-several-profiles-for-one-agent), the primary
first;
claude's single `ANTHROPIC_BASE_URL` already reaches one provider per launch; pi's
`enabledModels` is a soft shortlist and restricts nothing. For `openai-codex` pi gets no
`enabledModels` at all: its extension registers exactly
[the declared list](#the-openai-codex-model-list), so pi's view of that provider is the list
([ML-D2](../design/model-lists-and-pickers.md#ML-D2)).

## The canonical wire_api vocabulary

Three names — `anthropic`, `openai-chat-completions`, `openai-responses` — deliberately
**nobody's dialect**, so a pass-through cannot work by accident ([OQ-PT1](#oq-pt1)). Pack authors write
canonical names; `KnownWireAPIs` in `internal/packdecl` is the single closed set, enforced at
manifest and config layers; a value outside it is refused on the authoring path and
dropped-and-reported across a version boundary (the tolerant skew path).

Each derive owns a **dialect map** translating canonical → its agent's spelling, every entry
carrying its provenance (source, version, date) — a dialect map with no provenance is the same
unverified assertion in a new location:

| Agent | canonical → dialect | Emits for unspeakable protocols |
| :--- | :--- | :--- |
| codex | `openai-responses` → `responses` (the only value codex accepts) | **no entry at all** |
| pi | `anthropic` → `anthropic-messages`; `openai-chat-completions` → `openai-completions`; `openai-responses` → `openai-responses` | no entry |
| opencode | consumes no protocol field (URL only) | — |
| omp | the same three spellings as pi | no entry |
| claude | no config dialect; the env derive reads endpoints directly | composes nothing |
| copilot | `anthropic` → `COPILOT_PROVIDER_TYPE=anthropic`; `openai-chat-completions` → `TYPE=openai` + `COPILOT_PROVIDER_WIRE_API=completions`; `openai-responses` → `TYPE=openai` + `WIRE_API=responses` (provenance in the derive: copilot 1.0.48 help topic) | composes nothing — the one agent speaking both families; nothing only when the provider names no endpoint |

An endpoint that declares **no** `wire_api` gets the derive's own default — codex `responses`
(its only accepted value), pi `openai-completions` (a legal registry value; pi itself has no
default — an absent `api` is a composition error that deletes the provider from its model
list).

> [!WARNING]
> **Codex cannot reach z.ai's OpenAI route — record it, do not fix it.** z.ai's openai route
> serves chat completions only (measured: `/v4/responses` is 404 on both routes); codex speaks
> `responses` only. No `wire_api` value makes the pairing work, so the codex derive emits no
> zai entry rather than one that fails at first request. Do not "fix" this by restoring a chat
> spelling on the codex side — `chat` was removed from the product, and the canonical
> vocabulary exists precisely so this mistake is unrepresentable.

## Derives: the delivery mechanism

A **derive** is the one place a pack runs Lua — a sandboxed producer of config values (base,
table, string, math libraries only; no `os`, no `io`). Two registrations and one helper:

- `yolo.derive(agent, surface, fn)` — the file half. Runs in-jail at boot for each declared
  surface, returning that surface's computed layer.
- `yolo.env(agent, fn)` — the env half. Runs **host-side only**: its output crosses
  per-entry through the agent's own env file on the container backends
  ([the credential gate](#the-credential-gate)),
  `yolo host` has no jail at all, and the macos-user backend fixes its plan env before
  the bootstrap runs. One runner (`AgentEnv`) serves both notches — that shared
  implementation is what keeps `yolo -- claude` and `yolo host -- claude` composing the same
  environment. An in-jail env derive has no consumer and is never run.
- `yolo.model_for(alias)` — not a registration but the one helper: it resolves a model alias
  for the SELECTED provider and returns `"<provider>/<id>", "<id>"`, or `nil`. It never reads
  another provider's aliases. A missing [tier alias](#tier-aliases) is a warning at boot, never
  a refusal; any other missing name is `nil`, silently
  ([OQ-XM1](../research/extension-model-defaults.md#OQ-XM1),
  [XM-D1](../research/extension-model-defaults.md#XM-D1)).

> [!WARNING]
> **The env half is not a surface, and the host render's key probe is not its seam.** A
> `yolo.env` producer is recorded under the agent name alone, in storage of its own, so a pack
> may declare a real surface named `env` without colliding with the environment composition —
> keying the producer as `(agent, "env")` makes that collision representable, which is the
> whole reason it is not keyed that way. The host notch needs a REAL invocation for the same
> reason: `hostTableKeys` probes each derive against a sentinel table for key NAMES only and
> deliberately passes no content (a jail's derived tables embed jail-absolute paths), so an env
> path built into the boot loop alone composes nothing for `yolo host`, which has no jail to
> boot.

The derive context (`DeriveCtx`) carries: the live tables (`mcp_servers`, `lsp_servers`,
`providers`, and `use_profiles`, the folded CLI-keyed selection, which kept its name when the
config key became `profile`), `agent` and `surface`, `profile_name` (the profile active at this
agent's CLI name), `selected_provider` (the provider it resolves to), `selected_platform` (that
provider's [`platform`](#the-platform-what-service-a-provider-is), read off its row in the table,
"" when it declares none), `profile` (that profile's
resolved options — always a table, empty when no profile is active), and `tombstone`, the one
spelling of a removal (a Lua `nil` omits a key; it does not remove one). `mcp_servers` is the
one table filtered before a derive sees it: a server whose job the active authentication source
already performs is dropped. **Selection resolution is one rule**: the resolved `YOLO_PROFILES`
table (`ProviderFor`) feeds both the surface path and the env path —
never the pack manifests, never a Lua re-derivation. The env runner additionally builds a
**hydrated copy** of the providers table for the derive invocation only, `api_key` resolved
from the hydrated `env_sources` then the invoking environment. The hydrated copy is
per-invocation and never serialized; `YOLO_PROVIDERS` stays secret-free.

Errors are fatal at the boot step (`genStep` → the jail refuses to start) and refuse the
launch host-side. There is deliberately no second reporting channel.

> [!WARNING]
> **The `yolo.*` table is a host↔jail version boundary; a new function must be tolerated
> before it is called.** The host stages the `derive.lua` the in-jail entrypoint executes and
> the two halves deploy on different cadences, so a newer host stages a script calling a
> `yolo.*` member the running image never registered. The whole script runs in order to
> REGISTER its producers, which makes the blast radius the script rather than the call: adding
> `yolo.env` took down BOTH claude surfaces at boot on every pre-existing image, over a
> producer the entrypoint does not even invoke. So the two paths that render a surface — the
> jail's boot loop and `yolo check`'s dry run, sharing `deriveComputedLayer` — read an unknown
> `yolo.<name>` tolerantly and report it (`DeriveCtx.UnknownAPI`), while `AgentEnv` on the host
> refuses it: strict is the zero value, and tolerance is asked for only at the boundary that
> can name why.

> [!NOTE]
> **Retired 2026-09-05 — the credential no longer rides the argv.** The env derive's output
> used to cross as `-e ANTHROPIC_AUTH_TOKEN=<secret>`, visible in `ps` to anything on the
> host that could see the launcher's process; per-entry file delivery moved it into a 0600
> file, which since the credential gate is the selecting agent's own. The recorded trade-off — "an
> env var must reach the container somehow" — was true of the argv crossing and is not the
> only crossing: the live-mounted file reaches a running jail at least as well, and an
> attach reaches it ONLY that way.

<a id="selection-write-on-activation-never-on-absence"></a>

## Selection: write on activation, clear only what yolo wrote

An interactive in-agent model choice (pi's `/model`, opencode's picker) writes the SAME keys a
selection would. A render that re-asserts those keys every boot would silently revert the
user's choice on the next launch — the exact hazard the selection semantics refuse. So
selection keys ride a **reserved namespace** of the computed layer — `selection`, a flat map
of surface keys whose values are scalars or arrays of scalars — and the stateful render lifts it
onto the surface root with an edge-triggered apply (`ApplySelection`):

| Situation | What the render does |
| :--- | :--- |
| Key absent in the file, selection names it | **writes it** (activation) |
| File value equals what yolo last wrote, selection moved | **writes the new value** |
| File value differs from what yolo last wrote | **keeps the user's value** — yolo claims nothing |
| File value is the host layer's (equal to what your host config supplies, or unchanged since a render that took it from the host) | **writes the selection over it**: a host value is not an in-jail edit ([below](#a-selection-outranks-a-host-layer-value), [`OQ-SW1`](#oq-sw1)) |
| Nothing recorded for the key, file value equals the selection | **adopts it** as yolo's write — the upgrade path for a key that moved into the selection, such as pi's `enabledModels` |
| Selection stops naming the key, file still holds what yolo wrote | **clears it**, so the agent's default or the host layer applies ([below](#deselection-clear-what-yolo-wrote-keep-what-the-user-wrote), [`OQ-PSW2`](#oq-psw2)) |
| Selection stops naming the key, file value differs from what yolo wrote | **keeps the user's value** |

The baseline "what yolo last wrote" is the **selection record**: a per-surface sidecar, beside
the last-render state, holding the value yolo's selection mechanism last wrote for each key. It
is NOT the last render itself (a user edit is captured into the overlay and re-asserted by it,
so last-render converges to the file and would revert the user one boot later). Only the
selection mechanism writes it. A lost or corrupt record claims nothing — every key then reads as
the user's — which is the safe direction, and the re-arm path if a selection ever seems inert:
delete the key from the file once. Non-stateful surfaces drop the namespace with a warning; the
host render's key-probe never claims it.

An array compares by value and in order, so a reordered list is a user edit: pi starts a
session on the first entry of `enabledModels`.

> [!WARNING]
> **Selection values are scalars or arrays of scalars, never objects.** The host render
> discovers dynamic *table* keys by probing each derive with an empty selection, and claims
> every object-valued key as a table it writes wholesale. An object under the selection would
> read as such a table; a derive that gated a whole table on the selection would register no
> keys there and make the host deep-merge where the boot replaces. An array is a leaf to every
> reader, replaced whole and never merged into, so it carries neither risk. Catalog tables come
> from presence; selection values are scalars or arrays.

### Deselection: clear what yolo wrote, keep what the user wrote

**Deselection is a state, not the absence of one.** Without a clear, "no profile" would mean
"whatever the last profile left behind": run `yolo -p bedrock -- codex`, then `yolo -- codex`,
and codex would keep asking its default endpoint for the Bedrock model yolo wrote. That state is
reached by doing nothing, which is why it is the one that bites.

A key the selection stops naming is decided on its own, against the selection record:

| The key, no longer selected | What the render does |
| :--- | :--- |
| File value equals the record (**yolo's own value**, unedited) | **clears it** — lifts nothing for it — and **drops the key from the record** |
| File value differs from the record (the user changed it) | keeps it, lifting the current value |
| No record for the key (yolo never wrote it) | keeps it — yolo claims nothing |
| Key absent from the file | nothing to clear; the record entry is dropped |

- **Each key clears on its own.** A provider-and-model pair nobody touched clears as a pair,
  but if the user edited one half in the jail, that half stays and the untouched half clears.
- **Dropping the record entry is what makes the clear safe.** If yolo cleared the key and kept
  remembering the value, a user who later typed the same id by hand would lose it on the next
  deselect. After a clear yolo has no claim, the same rule as "a lost record claims nothing".
- **An emptied record removes its sidecar**, so a surface whose every key was cleared keeps no
  selection state at all.

**The clear works by omission, so what comes back is whatever sits underneath.** The render
rewrites the file from its layers every boot, so a key no layer asserts simply stops existing, or
falls through to a lower layer that still asserts it. A selected key never lingers in the
[captured overlay](config-migration-to-prism.md#vocabulary): the overlay is narrowed every boot
against the computed layer, which asserts the key while it is selected (`narrowOverlay`). So the
value a clear returns to is pi's host layer where one is set, and otherwise the agent's own
default. **Only pi has a host layer to return to.** Of the three surfaces that carry selection
keys, only pi's `settings` declares `readsHost`, so for codex and opencode a clear always means
the agent's own default.

> [!WARNING]
> **Do not clear with a tombstone.** A tombstone in the computed layer deletes the key through
> every layer below it, the user's host file included, so "clear" would mean the agent's built-in
> default and never the host value. Because the next boot has no record left, the host value
> then comes back — the key flaps between two answers across two launches. Omission returns to the
> host value and holds it.

<a id="a-clear-holds-on-an-adopting-boot-too"></a>

**A clear holds on an adopting boot too.** An **adopting boot** is one whose `last_render`
sidecar is absent or does not decode, so the render seeds the captured overlay from the file on
disk ([adoption](config-migration-to-prism.md#adoption-what-the-first-migration-keeps)). That file
holds the very value being cleared, so adoption alone would re-capture yolo's own id as the user's,
the file would keep it, and with the record entry gone no later deselect could clear it. So the
render is handed the cleared keys (`StatefulInputs.SelectionCleared`) and removes them from the
captured overlay before narrowing it, on every boot (`dropSelectionCleared`):

- **On an adopting boot**, the cleared key leaves the file exactly as on any other boot.
- **A key the user changed** is not a clear, so it is kept as on any boot, and every other key the
  file holds is adopted as usual.
- **In steady state** the overlay never holds a selected key, so the step changes nothing unless a
  capture between boots left a stale value for it. The key then falls through to the host layer or
  the agent's default, never to that capture.

The failure this closes was reproduced through the boot render at `ca86d945` (pi, a selection
written, `last_render` deleted, then two deselects: both keys survived both). Both ways a boot
adopts, sidecar absent and sidecar undecodable, are now pinned through the same render
(`selectionadoptclear_test.go`).

<a id="where-a-clear-is-recorded"></a>

**Where a clear is recorded** ([`OQ-PSW4`](#oq-psw4)). Each cleared key whose value left the file
is one line in the jail's `boot.log` and nothing on the terminal. The line names the agent, surface and key, and the
value it held, which is safe to print because a selection key carries a provider or model choice
and never a credential. A kept user edit records nothing. The clear is decided once
(`ApplySelectionReport`, the one implementation of the arm, which `ApplySelection` and
`ApplySelectionOver` wrap) and handed to the render. The line is printed after the render writes
the file, from the render's own report of which cleared values left it
(`StatefulOutput.SelectionCleared`), so the log and the file cannot disagree. A clear the file
still shows, because the host layer supplies the very value yolo wrote, records nothing. The host notch
runs the same apply, but it has no boot log, so a clear there is recorded nowhere. When the jail
gets a verbosity ([`OQ-DB1`](../design/diagnostics-past-the-boundary.md#oq-db1), unruled),
promoting the line to the terminal is a change at `noteSelectionClears` alone.

> [!IMPORTANT]
> **Claude needs no clear, so do not add one.** Claude resolves a tier name such as `opus` to a
> provider's id itself, so yolo never writes claude a model id through the selection namespace.
> The claude env derive emits `ANTHROPIC_MODEL` and the per-tier variables only while a provider
> is selected, and they are process environment rebuilt on every launch, so a deselect simply
> stops emitting them. The codex profile's picker keys in `claude/settings` (`availableModels`,
> `modelPicker`) are ordinary computed keys, re-asserted every boot. They leave with the profile
> for the same reason. At `yolo host apply`, whose `rmw` write re-reads your real file, a computed
> key the derive stops asserting leaves by the computed-leaf clear
> ([HC-D25](../design/host-computed-layer.md#HC-D25)) rather than by omission: claude's Bedrock
> switch is the case that matters, since the host leaves claude's `codex` profile out.

### A selection outranks a host-layer value

pi's `settings.json` is a stateful surface with a host layer, so after one launch the value the
user's **host** `~/.pi/agent/settings.json` supplies sits in the jail's own file. Read naively,
that is a value with no record, which the rule above treats as an in-jail edit to keep. That
reading made `-p` inert for pi for every user whose host config names a model. The layer order
was never the problem: a selection lifts onto the computed layer, which outranks the host layer.
The edge trigger in front of it had the provenance wrong.

So a file value is **the host's** (`HostOwnedKeys`), and a selection writes over it
(`ApplySelectionOver`), when it either:

- equals what the host layer supplies now, or
- is unchanged since the previous render **and** that render's provenance record names the host
  layer as the key's winner — a host value an earlier launch rendered, whose source has since
  moved on the host.

An in-jail edit matches neither: it differs from the host value and from the previous render.
An edit that happens to equal the host value is indistinguishable from it, and treating it as
the host's is harmless. The check runs only when a selection exists, because it decides
activation and never a clear. Together with omission it gives pi a full cycle. A selection
writes over the host value, a deselect clears yolo's write so the host value comes back, and
the next selection writes over it again.

### Selecting `openai-codex` for codex

`openai-codex` is the subscription provider the `openai-auth` pack ships — codex's own
first-party ChatGPT login, backed by the host's `openai-auth` broker — and codex's derive treats
it differently from every other provider:

- **No catalog row.** The derive excludes `openai-codex` from `model_providers` by name, because
  codex implements it natively; a third-party row for it would point codex at the endpoint as if
  it were someone else's API.
- **The selection is `model` alone, never `model_provider`**, so codex runs on its own login. The
  value is the profile's `model` option as written, not an alias looked up in a `models` map,
  and the first id of [the declared list](#the-openai-codex-model-list) when the profile names
  none or names `default`. With that list emptied it writes no `model`, and codex starts on its
  own default.
- **A switch from a third-party provider clears the stale `model_provider`.** The selection stops
  naming that key, so the per-key rule above clears the value yolo wrote for the previous
  provider. Nothing special-cases it.

codex's pack declares `openai-responses` among the protocols its program speaks, which is the
protocol the `openai-codex` entry serves, so pointing codex at it resolves like any other pairing
([`protocol-resolution.md`](protocol-resolution.md)).

### The `openai-codex` model list

The subscription's models are declared once, on the `openai-codex` provider the `openai-auth`
pack ships, and every agent that can use the provider renders that one list
([ML-D1](../design/model-lists-and-pickers.md#ML-D1)):

- **claude** offers exactly the list in its picker and, while the profile's `enforce_models` is
  on (the default), allows nothing else. Every tier (opus, sonnet, haiku, fable) is pinned to the
  profile's `model` or the first id, so claude's Default row and its background requests use a
  listed model. The start model is pinned only when the profile sets `pin_model: "true"`, since
  claude returns to a pinned start at every launch, over a model chosen with `/model`
  ([MM-D3](../design/model-lists-and-pickers.md#MM-D3));
- **codex** starts on the profile's `model` or the first id, and its `/model` menu is exactly the
  list: the launcher writes codex's own catalog entries for the listed ids, in the list's order,
  to `~/.codex/yolo-model-menu.json` before it execs codex, and hands codex the file with
  `-c model_catalog_json=…` ([MM-D9](../design/model-lists-and-pickers.md#MM-D9),
  [MM-D22](../design/model-lists-and-pickers.md#MM-D22)). An id codex's catalog lacks is left
  out with a warning, and with none left codex keeps its own menu. At the host codex keeps its
  own menu: the step is not built there yet
  ([MM-D22](../design/model-lists-and-pickers.md#MM-D22));
- **pi**'s extension registers exactly the list for `openai-codex`, read from a file yolo writes
  at every jail boot, with the cost, thinking and image facts taken from pi's own catalog. pi gets
  no model scope for it, and its sub-agents may use only the listed ids. A listed model pi's
  catalog does not know registers text-only, with no thinking levels, and pi warns once when
  that happens ([ML-D7](../design/model-lists-and-pickers.md#ML-D7)). While the `enforce_models`
  of the profile that governs `openai-codex` is on, as it is by default and with no profile
  selected, a model outside the list, typed with `--model` or resumed from a session, ends its
  turn with an error naming the list and the switch
  ([MM-D23](../design/model-lists-and-pickers.md#MM-D23)). On the host,
  `yolo host apply` writes the same list into that file, from the provider table it composes at
  user scope ([OQ-HC1](../design/host-computed-layer.md#OQ-HC1), which superseded
  [ML-D8](../design/model-lists-and-pickers.md#ML-D8)).

A declared model that has a 1M-context variant lists it right after itself, as `<id>[1m]`. The
suffix is the clients' spelling for the long-context request, and each strips it before the
model id reaches the service.

To change the list, override the provider's `models` in your config, the same per-field merge
every shipped provider takes. A `null` removes a model, and a new alias adds one after the
declared ones, with no 1M variant:

```jsonc
{
  "providers": {
    "openai-codex": { "models": { "gpt-5.6-sol": "gpt-5.6-sol", "gpt-6-luna": null } }
  }
}
```

Removing the first model moves every agent's default to the next one.

A second alias for a model already in the list, such as `"default": "gpt-6.1-sol"`, adds no row
and changes nothing: each model is listed once, with the facts of the alias spelled the same as
its id. Another alias naming it only fills in a fact that one lacks, such as a `name` for a model
you added.

## Per-agent delivery

What each agent actually receives, from one composed table and one selection:

| Agent | Catalog | Selection |
| :--- | :--- | :--- |
| codex | `~/.codex/config.toml` `[model_providers.<id>]` (TOML); never a row for `openai-codex` | top-level `model_provider` + `model`; `model` alone for `openai-codex` ([above](#selecting-openai-codex-for-codex)) |
| pi | `~/.pi/agent/models.json` `providers.<id>` (JSON; credential as `apiKey: "${VAR}"` config-value syntax); never a row for `openai-codex`, whose models the extension registers from [the declared list](#the-openai-codex-model-list) | `~/.pi/agent/settings.json` `defaultProvider` + `defaultModel` (a pair of bare ids), and `enabledModels` (the scoped list, default first), which is not written for `openai-codex`. Also, for every provider, pi-subagents' `subagents` block: `defaultModel` as `<provider>/<id>` (the same model), and `modelScope` `{enforce, strict, allow}` over the provider's configured ids, or `<provider>/*` when it configures none, so a child agent never crosses providers ([XM-D3](../research/extension-model-defaults.md#XM-D3), [XM-D4](../research/extension-model-defaults.md#XM-D4)). For an [active set](#an-active-set-several-profiles-for-one-agent) the pair stays the primary's, `enabledModels` is each entry's run in set order, each led by its own default (an `openai-codex` entry adds its declared base ids, never a `[1m]` variant, since `enabledModels` are minimatch patterns), and `modelScope.allow` is the union, so a child may use any listed provider and none other; each entry's profile options reach its own catalog row, and the OpenAI login pre-launches when any entry is `openai-codex` |
| opencode | `~/.config/opencode/opencode.json` `provider.<id>` — `baseURL`/`apiKey` live UNDER `options` | top-level `model = "<provider>/<model>"` and `small_model`, written only when a model resolves, and `enabled_providers` naming the selected provider whether or not one does, so opencode on a provider that declares no models chooses among that provider's own ([AP-D17](../design/active-provider-sets.md#AP-D17)). For an [active set](#an-active-set-several-profiles-for-one-agent) `model` and `small_model` stay the primary's and `enabled_providers` names every entry in set order, a Bedrock entry as `amazon-bedrock` wherever it sits and an entry whose provider names no endpoint by that provider's name, which must be opencode's own id for it (`anthropic`), since yolo writes such an entry no row; opencode reads that key as a filter ("When set, ONLY these providers will be enabled", its 1.18.32 schema), so the order states the set and does not order opencode's menu. Each entry's own `enforce_models` decides the `whitelist` on its provider's row. A model picked in opencode lasts for that run of it: the `model` yolo writes outranks opencode's saved recent picks at its next start ([AP-D15](../design/active-provider-sets.md#AP-D15)) |
| omp | `~/.oh-omp/agent/models.yml` `providers.<id>` (YAML; credential as the provider's env-var NAME, which oh-omp resolves before treating it as a literal) | **none** — the derive writes a catalog and no selection key, so a selected profile makes the provider *available* and the user chooses it inside the agent |
| copilot | no catalog (BYOK is env-var-only; no copilot config file has provider keys) | process env from the copilot pack's env derive: `COPILOT_PROVIDER_BASE_URL` (the sole activation gate), `COPILOT_PROVIDER_TYPE`, `COPILOT_PROVIDER_WIRE_API` (openai type only), `COPILOT_MODEL` (required — a provider with no resolvable alias composes nothing at all), `COPILOT_PROVIDER_API_KEY` (a placeholder for a keyless loopback endpoint), `COPILOT_PROVIDER_MAX_PROMPT_TOKENS` ← the provider's `context_window` option |
| claude | no catalog (claude has no provider directory) | process env from the claude pack's env derive: the address and credential for the provider's `anthropic` endpoint (`ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN` — a dummy token on a routed launch that has no key, so claude never falls back to the user's own subscription login), `AWS_REGION` from the provider's `region`, one model id per claude tier resolved from the provider's aliases (the selected one from the profile's `model` option; on a Bedrock provider only from its Anthropic entries, and none unless named, [the shipped Bedrock provider](#the-shipped-bedrock-provider)), and knobs composed from provider options (the context window, request and stream timeouts). Claude's `[1m]` suffix is appended to every model id when the `context_window` option is at least one million — it is Claude Code's client syntax for the context-1m beta, stripped before the wire — and non-essential traffic is disabled on any routed launch. The exact variable set is the derive's, in `packs/claude/derive.lua` |

A Bedrock provider is the exception for codex, opencode and pi: it gets no row of this table's
shape, but the agent's built-in Bedrock provider ([the shipped Bedrock provider](#the-shipped-bedrock-provider)).

The spellings are facts about each agent, source-verified and carried as provenance comments
in the derives (pi 0.84.4's settings-manager keys and its ten-id api registry; opencode's
first-slash model format and options nesting; codex's binary-verified `responses`-only). The
model a selection names is resolved IN THE DERIVE — alias = the profile's `model` option or
`default`, then the provider's `models` map. Core decides no model: `yolo.model_for` only looks
an alias up for the provider the derive was handed.

### Tier aliases

A provider's `models` map is open vocabulary, but four names are **conventional tier aliases**
that every provider is expected to declare, so a pack can ask for a capability instead of a
vendor's model name ([model-lists-and-pickers §6](../design/model-lists-and-pickers.md#6-tier-aliases-default-fast-balanced)):

| Alias | Means |
| :--- | :--- |
| `default` | what you get when nothing is said, and every derive's fallback |
| `fast` | cheap and quick |
| `balanced` | the middle tier, where one exists |
| `frontier` | the most capable tier ([OQ-XM2](../research/extension-model-defaults.md#OQ-XM2)) |

It is a convention with a warning, not an enum: a derive asking `yolo.model_for` for one the
selected provider lacks gets `nil`, and the launch prints one warning naming the provider and
the alias and proceeds. The list is `luahook.ConventionalModelAliases`.

**claude reads three of them for its tiers** ([MM-D17](../design/model-lists-and-pickers.md#MM-D17)):
`balanced` pins its Sonnet tier (`ANTHROPIC_DEFAULT_SONNET_MODEL`), `fast` its Haiku tier, and
`frontier` its Opus tier wherever a tier pin reads an alias, which is the `openai-codex` list and a
list an `only` narrowed; on a routed provider the Opus tier stays the selected model. claude's own
names, `sonnet`, `haiku` and `opus`, stay synonyms and win where a provider declares both, and a
name whose model claude's client cannot call gives way to the tier's other name
([MM-D18](../design/model-lists-and-pickers.md#MM-D18)). The Fable tier reads `fable` alone.

Which ids yolo ships where an agent cannot default, a `models` kind for company packs, and how
each picker renders the list are designed in [`model-lists-and-pickers.md`](../design/model-lists-and-pickers.md).

> [!WARNING]
> **Codex reads a provider's credential variable from `env_key`, and a plausible `api_key_env`
> is silently ignored.** Codex's `ModelProviderInfo` has an `env_key` field and no
> `api_key_env`, and it does not refuse unknown fields: an unknown key is reported only under
> codex's opt-in `--strict-config`. So a catalog row carrying `api_key_env` — the spelling
> yolo's own `api_key_env_name` invites — loads without complaint and binds no provider
> credential. **The failure is a credential leak, not an anonymous request:** codex never reads
> the provider's key and falls back to its own first-party OpenAI login (a ChatGPT sign-in or an
> API key), sending that bearer — and, for a ChatGPT sign-in, its `ChatGPT-Account-ID` header —
> to the provider's `base_url`. Only with no login does the request go out unauthenticated.
> (Checked against the codex 0.145.0 source, and read rather than captured from a live request.)
> The codex derive writes `env_key` from the provider's `api_key_env_name`.
> `TestCodexDeriveUsesCodexCredentialField` in `internal/entrypoint` pins both halves: `env_key`
> is written, and `api_key_env` is absent.

A `models.<alias>` value is either the bare wire-id string (the shorthand) or an **object**:
`id` (required — the wire id, which is usually not the alias), plus optional `name`, `vendor`
(the model's maker, [above](#the-shipped-bedrock-provider)), `reasoning`, `input`, `cost`,
`context_window`, and `max_tokens`. The field set is **closed**
(an unknown key is refused, not accepted-and-ignored), and the facts are canonical snake —
`cost.cache_read`/`cache_write`, translated to pi's `cacheRead`/`cacheWrite` by the derive.
`id` is required rather than inferred from the alias because the merge replaces: the moment a
value is an object, a pack's id string under that alias is gone, so an inferred `id = alias`
would silently rewrite the wire id (cerebras' `default` → `qwen-3.8-27b`) the first time
someone added one fact. `packload` lowers every object back to the string alias→id contract the
derives read, moving the facts to an internal per-alias map — so no consumer learns two shapes.
A pack manifest keeps its `models` alias→id map in the string form and may ship the same
per-alias facts in `model_options`: a flat map of string-valued facts keyed by a declared
model alias. For example, the Z.ai pack ships `"glm-5.3-flash": {"input": "text,image"}`
there; this marks only Flash as image-capable. When a user supplies object-form model facts,
they override individual shipped facts without erasing other aliases' facts:

```jsonc
"providers": {
  "kilo": {
    "models": {
      "default": {
        "id": "deepseek-v4.1-flash",
        "reasoning": true,
        "input": ["text", "image"],
        "cost": { "input": 0.3, "output": 1.2, "cache_read": 0.006, "cache_write": 0 },
        "context_window": 1048576, "max_tokens": 384000
      },
      "pro": "deepseek-v4.1-pro"
    }
  }
}
```

> [!NOTE]
> **Provenance, and the one value that is an assumption rather than a fact.** The rates and
> capabilities above are Kilo's own catalog row for `deepseek/deepseek-v4.1-flash` (checked
> 2026-09-20 against `https://api.kilo.ai/api/gateway/models`): `prompt` 0.3, `completion` 1.2,
> `input_cache_read` 0.006, `input_modalities` `["text", "image"]`, `context_length` 1048576,
> `max_completion_tokens` 384000, and `reasoning` among the supported parameters. The catalog
> publishes **no `input_cache_write` for this model** — though it does for 71 of its 380 rows —
> so `cache_write: 0` is a declared assumption, not a catalog fact. It cannot be omitted: pi's
> cost schema needs all four rates and this config's own validator requires them too, so the only
> reachable value is an explicit zero. If Kilo bills cache writes at a rate it does not publish,
> that zero understates cost silently; re-check the row before trusting the figure.

<a id="a-re-pointed-alias-loses-the-packs-vendor"></a>

**One shipped fact does not follow an alias you re-point: `vendor`.** A pack may declare a
model's maker in `model_options.<alias>.vendor`, and the wire bridge routes on it: on a Bedrock
upstream, an id declared `anthropic` goes untranslated to Bedrock's Anthropic Messages route
([the Messages pass-through](wire-bridge.md#the-messages-pass-through-on-a-bedrock-upstream)).
The vendor describes the id the pack put under that alias, so when your `models.<alias>` names a
different id, as a string or as an object's `id`, the composed entry drops the pack's `vendor`
for that alias and keeps its other facts. Your object-form entry names your id's maker in its own
`vendor`, and that one is kept: the drop removes only the pack's. A string-form alias names no
maker, so your id then carries no vendor: the bridge translates it, and every agent is offered
it. Restating the pack's own id keeps the pack's vendor. `packload.dropRepointedVendors` is the
rule; `TestARepointedAliasDropsTheShippedVendor` and
`TestARepointedAliasKeepsTheVendorTheUserDeclares` pin it.

The provider's `options` is the **fallback**: a fact common to every model is declared once
there, and a per-model value overrides it for that alias. The facts are **additive** — an alias
that declares none renders the same `models.json` row it did before, leaving pi's own defaults
(`reasoning: false`, `input: ["text"]`, zero cost) in place. pi's schema accepts all of these
(`dist/core/model-config.js`, `ModelDefinitionSchema`), but its `modelFromJson` fills those
defaults for an absent field — so before this the derive was the only path from config to the
file, and a user-set capability read as inert. All four cost rates are required (pi's
`ModelCostSchema` discards the whole file on one missing), and `context_window`/`max_tokens` read
from the same object override the provider-level option, so models of one provider may differ.
The provider entry needs no pre-declaration (that gate is profiles-only).

The user-facing spellings: on the run path `-p <sel>` / `--profile <sel>` take BOTH grammars — a bare
NAME selects that profile for every CLI the selected packs install that no pair names
(uniformly, whether or not a command follows `--`), and `cli=name` (comma-separated,
repeatable) selects for the named CLI only, beside a bare name in either order. The persistent
form is the `profile` key in user config, which mirrors the flag form for form
([the selection](#declaring-and-selecting-a-profile)). A comma continues a list in either
grammar, a bare `-p zai,openrouter` or a pair's `-p pi=zai,openrouter`
([an active set](#an-active-set-several-profiles-for-one-agent)). Profile names refuse `=` and `,`
at declaration so the grammars cannot be ambiguous, and neither flag means startup
timing — that is `--timing`. (The former third spelling `--pack-profile` is deleted —
never in a release, and redundant once `-p` carried both grammars.)

`yolo host` and `yolo host env` read the same grammar (`parseProfileValue`,
[ES-D27](../design/credential-sources-separation.md#10-decision-ledger)), for the ONE command they
compose: a bare name selects that profile for it, and a `cli=name` pair naming that command (the
command after `--`, or `yolo host env`'s `--agent`) means the bare name, so
`yolo host -p claude=zai -- claude` is `yolo host -p zai -- claude`. A pair naming any other CLI is
refused by name, since there is no second process for it to select for; handing the command
another provider's key stays `--with-credentials`' alone. Before, both host parsers took a bare name
only and refused `claude=codex` as an undeclared profile of that name. The run path's help scan
mirrors its parse flag for flag, so `-p` consumes the next token there too, and `yolo -p -h` reads a
profile named `-h` rather than answering help, deliberately.

## Profiles and options

A **profile** is a named selection over one provider, and the name is what the user types. It is
also the whole of the `profile` kind. A fact of the PROVIDER keys on the provider, in the agent's
own derive or on a `platform` gate, never on the profile's name
([`OQ-BR8`](../design/providers-and-profiles-redesign.md#OQ-BR8), ruled 2026-09-29), so a second
profile over one provider gets what the first gets; a variant that really is a name may still be
gated by name ([the `profile` modifier](#the-profile-modifier)). What `-p <name>` should name at
all is open in [`providers-and-profiles-redesign.md`](../design/providers-and-profiles-redesign.md).

### Declaring and selecting a profile

Two sources declare profiles. A pack ships `kind: "profile"` — `{name, provider}`, both
required ([OQ-PT8](#oq-pt8)), plus an optional [`via`](#routing-a-profile-through-the-bridge-via); the user's `profiles` key declares more, or customizes a pack's by
name. A pack-shipped profile is a **default the user overrides**, never a second schema: for a
name both sides declare, the user's values win per option and the pack's provider stands unless
the user names another. Profiles **point at** a provider; there is no `extends`
([OQ-CS9](#oq-cs9)), because provider-declared option defaults already remove the duplication
inheritance would.

The option NAMES a profile may carry are the provider's declared `options` — a flat
name→default map in which a null means *declared, no default* ([OQ-CS4](#oq-cs4)). A profile
naming an option the provider does not declare is refused, naming what it does accept; a
provider that declares no options imposes no census at all. **Core validates no option value**:
what an option means, and where it lands, is the derive's business, and a derive's error
refuses the launch. `packload.ResolveProfiles` lowers both sources into one resolved table —
provider defaults under the profile's own values — once per launch, host-side; it crosses as
`YOLO_PROFILES` and nothing in the jail re-derives it.

A profile name is sole-owned **within** a pack — a second declaration of one name is a load
error — and deliberately not across packs: `bedrock` in two packs is two unrelated declarations
that share a selector value. A name may not contain `=`, because the `-p` grammar dispatches on
it.

**Declaration is mandatory** ([OQ-CS6](#oq-cs6)): a selected name that neither a selected pack
nor the user's `profiles` declares refuses the launch, naming what is declared. An undeclared
name used to be a silent no-op; it is a diagnosable error instead. At `yolo host --`, a pack
whose manifest has problems refuses the launch before the selected profile's declaration is
checked, naming that pack and its problems, as a jail launch refuses the same config
([NC-D32](../plans/notch-convergence.md#NC-D32), which revised
[NS-D18](../design/notch-scoped-config-contributions.md#10-decision-ledger)).

The selection itself is a table keyed by **CLI name** — the bin a pack installs — mapping each
CLI to the profile it runs. Two sources fill it: the `profile` key in user config, then `-p` on
the command line ([the flag grammar](#per-agent-delivery)), and every `-p` form beats every key
form for each CLI it reaches. The key mirrors the flag form for form
([PP-D10](../design/providers-and-profiles-redesign.md#PP-D10)):

```jsonc
// ~/.config/yolo-jail/config.jsonc — one of:
"profile": "bedrock"                                // -p bedrock: every agent
"profile": { "pi": "codex", "claude": "bedrock" }   // -p pi=codex,claude=bedrock
"profile": { "*": "bedrock", "pi": "codex" }        // -p bedrock -p pi=codex
"profile": { "pi": ["zai", "openrouter"] }          // -p pi=zai,openrouter
"profile": ["zai", "openrouter"]                    // -p zai,openrouter: every agent
```

`"*"` is every agent the object does not name. Within one source a named CLI keeps its own
entry, and the default (a bare `-p`, the string or list form, or `"*"`) reaches every other CLI
the selected packs install, never the command after `--`. A list is an
[active set](#an-active-set-several-profiles-for-one-agent). A null entry selects no profile for its
CLI, so `"*"` does not reach it. Both spellings lower to one shape and one fold
([PP-D11](../design/providers-and-profiles-redesign.md#PP-D11)), so a form cannot mean one
thing in config and another on the command line. CLI names are the right key because the
namespace is already exclusively owned — `program` is sole-owned by bin, so a CLI name resolves
to at most one pack — and because a pack slug is not what a derive knows itself by; `"*"` cannot
collide with one, since no program is called `*`. Both `profiles` and `profile` are
**user-scope-only** ([OQ-CS5](#oq-cs5)): a workspace file travels with the repo and is
agent-editable, and a profile steers which endpoint and which model an agent talks to. The
selector's earlier spellings, `use_profiles` (in every release from v0.9.0 through v0.11.0) and
`agent_profiles`, are refused by name with the replacement in the message, and the
`use_profiles` refusal respells the user's own entries under the new key; in a jail, where the
config is a generated snapshot, each is a warning instead. Every derive receives the **whole**
table, so a pack that installs no CLI — a provider pack — still reads any CLI's selected name.

### An active set: several profiles for one agent

An agent's value in the `profile` key may be a **list** of profile names, its **active set** (a term
[`active-provider-sets.md`](../design/active-provider-sets.md) coins: the ordered list of profiles
one agent runs on for one launch; [OQ-AP1](../design/active-provider-sets.md#OQ-AP1), ruled
2026-09-29). Every listed provider is live for that agent in one session, and the **first
entry**, the set's primary, is where a fresh session starts when yolo has to pick, and where
opencode starts every time yolo writes its `model` ([AP-D15](../design/active-provider-sets.md#AP-D15)).
A list of one is the plain name, byte for byte, so no existing config moves.

- **Spelling.** In config, `"profile": {"pi": ["zai", "openrouter"]}`, a JSON array and never a
  comma string (`"zai,openrouter"` is refused, respelled as the array). On the command line a
  comma continues the list of the CLI named before it: `-p pi=zai,openrouter,claude=codex`. A
  later pair for a CLI replaces its whole list, and a typed pair replaces the key's list for
  the launch ([AP-D4](../design/active-provider-sets.md#AP-D4)). The key and the flag lower to one
  `config.ProfileSelection`, whose two fields are lists, and fold through one
  `config.FoldProfiles`, so a list means one thing in both spellings
  ([PP-D11](../design/providers-and-profiles-redesign.md#PP-D11)). A bare element before any pair
  (`-p zai,pi=openrouter`), an empty entry (`-p pi=zai,`) and a name after an empty pair
  (`-p pi=,openrouter`) are refused as misuse, exit 2; until this build the first was dropped in
  silence, so `-p pi=zai,openrouter` started pi on zai alone. An empty pair is not an empty
  entry: `-p pi=,claude=zai` selects nothing for pi and zai for claude, as it always did. A
  profile name may not contain `,`, refused in both schemas where `=` is.
- **Who may hold one.** An agent whose pack declares `provider_sets` on the program that installs
  it (`packdecl.Contribution.ProviderSets`): pi and opencode today
  ([AP-D15](../design/active-provider-sets.md#AP-D15) for opencode). A list named at any other
  agent (claude, codex, copilot, oh-omp) is refused before anything starts, naming the
  one-profile spelling
  ([OQ-AP2](../design/active-provider-sets.md#OQ-AP2)), by config validation for a list named in
  the `profile` key (`validateProfile`), by `checkProfileTargets` for a typed pair, and again
  after resolution by `packload.ProfileSetProblems`. A **bare** list, one naming no agent — a bare
  `-p zai,openrouter`, the key's list form `"profile": ["zai", "openrouter"]`, or a list under
  `"*"` — goes whole to every set-capable agent and its first entry to every other, and the launch
  prints one line naming those agents, the entries they ignore and where the list was written
  ([OQ-AP3](../design/active-provider-sets.md#OQ-AP3); `config.FoldProfiles`,
  `packload.BareListNote`). The entries an agent ignores must still be declared: `-p zai,typo`
  or `"profile": ["zai", "typo"]` refuses naming `typo` whichever agents are selected
  (`checkProfileDeclarations` in a jail, `hostBareListUndeclared` and `composeHostVarsWith` at
  `yolo host` and `yolo host env`).
- **What a set refuses** (`packload.ProfileSetProblems`, at every notch and predicted by
  `yolo check`): a name listed twice; two entries resolving to one provider; two entries on one
  regional platform, such as two Bedrock providers, since the agent's process holds one
  `AWS_REGION` ([AP-D12](../design/active-provider-sets.md#AP-D12)); a via profile
  anywhere but first ([AP-D9](../design/active-provider-sets.md#AP-D9)), since an agent has one
  via route and its upstream is the primary's provider; and, at the protocol gate, any entry the
  agent cannot be paired with, named by position. Every entry must be declared.
- **What the derive sees.** `ctx.selected_provider` and `ctx.profile` are the primary, as ever, so
  a derive written before sets reads a set of one unchanged. `ctx.active_set` lists every entry in
  order, each with `profile_name`, `provider`, `platform`, `profile` and its own profile's
  `enforce_models` ([AP-D16](../design/active-provider-sets.md#AP-D16)), and
  `yolo.model_for(alias, provider)` answers for any entry of the set and nil outside it.
- **Every notch.** The jail's channel carries the list in `YOLO_USE_PROFILES`, and an attach
  delivering one needs the `profile-sets` contract tag, so a jail an older yolo launched takes the
  restart-or-refuse disposition ([AP-D8](../design/active-provider-sets.md#AP-D8)). macos-user
  composes the same channel. `yolo host -- <agent>` and `yolo host env --agent <agent>` take the
  same `-p` and `profile` key, open aws-auth's doorway for a Bedrock entry anywhere in the set,
  and fill a Bedrock entry's region from `~/.aws/config` as a primary's is; `yolo host apply`
  renders the key's set into the agent's own files, leaving out a whole set it cannot render and
  saying what the key's bare list narrowed, as a launch does.

The launch names each set of more than one in order ("Active set for pi: zai, openrouter"), beside
the per-name profile lines, which answer for each entry what it reaches for the agent holding it.
What pi and opencode render from a set is in [per-agent delivery](#per-agent-delivery). The
config-overlay `profile` modifier still gates on the primary alone, and oh-omp does not yet
declare `provider_sets`.

> [!WARNING]
> **`autonomy` and `profile` are two kinds on purpose; do not merge them** ([OQ-1](#pv-oq-1)).
> Their bodies once looked alike, but their selectors have different authorities. The
> confinement notch selects an autonomy posture, which no config or CLI can reach; a profile
> arrives through channels a user — and, before the scope rule, an agent — can write. Merging
> them puts a notch-owned permission bypass behind a user-owned selector, and the dangerous
> direction is `-p autonomous` **at the host notch**, which hands a real host the agent's
> permission bypass.

### Routing a profile through the bridge: `via`

`via` is an optional profile field, on a pack's `kind: "profile"` and on a user `profiles`
entry alike. Its value names a **service pack**, and today the one that serves it is
`wire-bridge`. A profile with `via` sends its agent's model traffic through that service instead
of straight to the provider ([OQ-WG6](../design/wire-bridge-gateway.md#OQ-WG6)):

```jsonc
// ~/.config/yolo-jail/config.jsonc
"profiles": {
  "pi-zai": { "provider": "zai", "via": "wire-bridge", "model": "glm-5.3" }
},
"profile": { "pi": "pi-zai" }
```

What it does, in order:

1. **The launch adds the pack.** Selecting the profile brings `wire-bridge` into the jail the way
   `needs` does, and says so (`+ wire-bridge (via of profile pi-zai, active for pi)`). A `via`
   naming a pack yolo does not ship, or one that declares no `via_address`, refuses the launch.
   Only an agent a selected pack installs counts: a `profile` entry for an agent the launch
   does not carry adds nothing ([WG-I10](../design/wire-bridge-gateway.md#WG-I10)). `yolo check`,
   config validation and `yolo config promote` resolve the same pack set
   ([WG-I11](../design/wire-bridge-gateway.md#WG-I11)).
2. **The agent gets its own URL.** Its derive receives `ctx.via_url`,
   `http://127.0.0.1:8216/agent/pi` here, and writes it as the selected provider's base URL. Only
   the agent whose active profile has `via` gets one. Every other agent, and every other provider
   row of the same agent, keeps its own URL.
3. **The bridge forwards to the provider unchanged**, adding the provider's own credential: its
   `api_key_env_name`, or a SigV4 signature for a `bedrock-runtime` upstream
   ([the via route](wire-bridge.md#the-via-route--one-route-per-agent-under-agentname)). It
   passes two wires through, OpenAI chat-completions and OpenAI Responses, and sends each
   request to the provider endpoint for the wire its path names
   ([WG-I20](../design/wire-bridge-gateway.md#WG-I20)).

The launch checks that the bridge can serve the agent before it starts anything. First it runs
the agent's own derives with `ctx.via_url` set, to see whether the agent's config points at the
URL at all. A derive decides which provider rows ride it: pi, oh-omp and codex keep
`openai-codex` on their own subscription client, and codex writes no row for a provider it
cannot reach. A via there changes nothing the agent sends
([WG-I15](../design/wire-bridge-gateway.md#WG-I15)). `yolo check` predicts every answer for the
`profile` key's selection; a `-p` is an argument to a launch that has not happened, so `check`
cannot see it.

| What the via does for the agent | Launch | `yolo check` |
| :--- | :--- | :--- |
| **Nothing**: the agent's config does not point at its via URL, for example pi with a via over `openai-codex` | warns on stderr that the via has no effect, and starts ([WG-I15](../design/wire-bridge-gateway.md#WG-I15)) | WARN |
| **Points the agent at a prefix the bridge serves no route for**: the provider declares neither wire, is not in the composed table, or is the ChatGPT subscription (opencode, whose derive re-points any selected provider) | refuses, naming the profile, the agent, the reason and the via URL ([WG-I13](../design/wire-bridge-gateway.md#WG-I13)) | FAIL |
| **Points the agent at a route with only the wire it does not prefer**, for example pi on a Responses-only provider such as `openrouter` | warns on stderr, naming the endpoint the provider lacks, and starts ([WG-I14](../design/wire-bridge-gateway.md#WG-I14)) | WARN |
| **Carries none of the agent's requests**: the agent's first protocol is neither via wire, as claude's and copilot's `anthropic` is, and its config does not point at its via URL | warns on stderr that the via sends none of its requests through the bridge, naming the agent's own switch for the provider's platform when its pack declares one (claude's `CLAUDE_CODE_USE_BEDROCK` for `aws-bedrock`), and starts | WARN |

The agent's **preferred wire** *(coined in [WG-I14](../design/wire-bridge-gateway.md#WG-I14))* is
the one its pack's first declared protocol names: chat-completions for `openai`, Responses for
`openai-responses`. For every shipped agent that is the wire its derive speaks on the via route, so
the third row's requests will fail. It is a warning, not a refusal, because the launcher reads the
wire off the declared `protocols`, not off what the derive writes, and a pack yolo does not ship
can write either. An agent whose first protocol is neither, such as claude or copilot, is never
refused: it gets the fourth row's warning when its config does not point at the via URL. That
matters for claude on a Bedrock provider, because a via turns claude's own Bedrock client off
([PP-D4](../design/providers-and-profiles-redesign.md#PP-D4)) while no via route carries claude,
so claude starts on its own login. An agent that declares no protocols, such as agy, is not
checked: nothing in its pack reads the via URL.

`via` is a field, not an option: the provider's option census does not apply to it, and a user's
`via` replaces a pack-shipped one for the same profile name. It works for agents that take a base
URL and keep its path: pi, oh-omp and opencode, which speak chat-completions there, and codex,
which speaks Responses. claude and copilot already reach the bridge through its adapter routes,
which a via profile does not change, and no via route carries either of them. At the host notch
(`yolo host`) there is no bridge daemon, so the agent uses its own client. That holds even when `wire-bridge` is listed in `packs`: the
host clears every via address before any derive reads one
([WG-I12](../design/wire-bridge-gateway.md#WG-I12)).

### Model lists shaped by packs

A pack can shape the model list of any provider, one another pack ships included, with a
`kind: "models"` contribution (`yolo config-ref` has the fields;
[OQ-BR12](../design/model-lists-and-pickers.md#OQ-BR12)). It takes one verb: `add` appends model
entries, each naming its maker as `vendor`, or `only` keeps just the ids it names. A company
ships its model policy this way once, instead of every engineer copying a list into their own
config:

```jsonc
{ "kind": "models", "provider": "bedrock", "add": [
    { "id": "global.anthropic.claude-opus-5-5", "vendor": "anthropic", "name": "Claude Opus 5.5" },
    { "id": "global.moonshot.kimi-k3", "vendor": "moonshot", "name": "Kimi K3" } ] },
{ "kind": "models", "provider": "bedrock", "only": [
    "global.anthropic.claude-opus-5-5", "global.moonshot.kimi-k3" ] }
```

A list an `only` narrowed is each agent's exact menu for that provider wherever the agent allows
one ([§14.1](../design/model-lists-and-pickers.md#141-the-table)): claude's `modelPicker` (on its
own Bedrock client, the list's Anthropic models), an extension registration in pi, a `whitelist`
in opencode (its own Bedrock client's row included) and an `enabledModels` scope in oh-omp. The
model yolo names for an agent to start on is the list's default entry, even when the `only`
dropped the model the profile names: the profile's `model` when the list holds it, else the
`default` alias, else the first entry. codex gets that start, and on `openai-codex` its menu is
the narrowed list too; copilot gets that start and nothing else. The
one exception is claude with `enforce_models` off, which keeps the start pin it had before the
`only` (what should replace it is [OQ-MM3](../design/model-lists-and-pickers.md#OQ-MM3)'s). A
list a pack only ADDS to keeps today's rendering beside the agent's own catalog; what it
should show instead is an open question ([OQ-MM1](../design/model-lists-and-pickers.md#OQ-MM1)).

**`enforce_models`** is a profile field, like `via`, that decides whether a narrowed list also
REFUSES other models. It defaults to on: claude's allowlist (`availableModels` with
`enforceAvailableModels`), opencode's whitelist and pi's extension then turn away a model outside
the list. On `openai-codex` claude's allowlist and pi's refusal hold whether or not an `only`
narrowed the list, since there the list is each one's whole menu for the provider, and pi's
refusal lives in the registration that also carries the subscription login
([MM-D23](../design/model-lists-and-pickers.md#MM-D23)). Off (`"enforce_models": false`), the list only shapes the menus, and opencode's menu is not narrowed at
all, since its whitelist cannot hide without refusing. With the switch off claude's start pin
returns, on `openai-codex` and under an `only` alike, wherever the profile's `model` or the
provider's default names a model: no allowlist then keeps an off-list saved model from starting,
so a `/model` choice lasts one session. On `openai-codex` the switch governs
claude's allowlist too. On claude's own Bedrock client the refusal is claude's alone, client side,
with [the four gaps](../design/model-lists-and-pickers.md#142-what-each-row-rests-on) that come
with it, so a repository's `.claude/settings.json` can widen or switch it off. pi's refusal is
its extension's: a model outside the list, typed with `--model` or resumed from a session, ends
its turn with an error naming the list and the switch, before any request leaves
([MM-D21](../design/model-lists-and-pickers.md#MM-D21)). It does not hold under
`pi --no-extensions`, and should the extension not find pi's own stream for the list's models,
pi shows the menu and says once that it cannot refuse there. oh-omp does not refuse yet:
`--model` still runs a model outside the scope.

**`pin_model`** is a profile option (`"pin_model": "true"`) asking claude to START every session
on the profile's model. Where claude's allowlist renders it is off by default, since the
allowlist already replaces an off-list saved model with Default, and a pinned start overrides a
model chosen with `/model` at every launch
([MM-D3](../design/model-lists-and-pickers.md#MM-D3)). Elsewhere today's start pin stays until
[OQ-MM3](../design/model-lists-and-pickers.md#OQ-MM3) is ruled. The shipped providers that declare
options declare `pin_model` too, so a profile over them may set it.

**`yolo check` checks the lists against the agents' own catalogs**
([MM-D16](../design/model-lists-and-pickers.md#MM-D16)). Every model id a composed provider lists,
the shipped lists included, is looked up in the **model catalog** of each installed agent: the
model ids the agent knows with no network, read from the files its pack names in `model_catalog`
([pack-system.md](pack-system.md#model_catalog)). An id no catalog knows is one warning per
provider, never a refusal; when no catalog could be read, one skip says the check could not ask
and why. Nothing is run and no network is reached: in a jail the check reads the jail's npm
install, at the host yolo's floor copy when the floor holds the one `yolo host --` runs, so an
agent not installed yet, or one the floor holds no entry for, is one it could not ask. A
provider every endpoint of which is on this machine (`localhost`, a loopback address,
`host.containers.internal`) is left out, since its ids are the ones your own server serves
([MM-D20](../design/model-lists-and-pickers.md#MM-D20)). Today only pi's pack declares a catalog:
no other shipped agent's package was found to ship one as a file
([MM-D19](../design/model-lists-and-pickers.md#MM-D19)). No launch makes a model-list network call.

### The `profile` modifier

Two kinds take `profile: "<name>"`, and each asks a different holder of the name whether it is
active; `env` also takes the provider-keyed gate, `platform: "<platform>"`. An inactive gate is a
**clean skip** — no error, no orphan report — because selection is the optionality.

| Gate | Active when | Why that key |
| :--- | :--- | :--- |
| `profile` on `config-overlay` | the name is the profile active for the **target surface's owning agent** (the `agent` half of `agent/name`) | the surface names an agent, so the surface is what the gate asks |
| `profile` on `env` | per AGENT: the name is the profile that agent selected, and the pack is either the one installing that agent's CLI or a pack installing no CLI at all | an env has no surface to name an agent, so the delivery names one: the agent's own env file ([the credential gate](#the-credential-gate)) |
| `platform` on `env` | per AGENT: the provider that agent's profile resolves to declares that [`platform`](#the-platform-what-service-a-provider-is), and the pack is the agent's own or installs no CLI | a provider fact must reach every profile over the provider and every provider of the service, which a name gate misses (trap [D5](../design/providers-and-profiles-redesign.md#D5), [`OQ-BR8`](../design/providers-and-profiles-redesign.md#OQ-BR8)). `packs/aws-auth` ships the CLI-less case: its pointer gates on `aws-bedrock` |

A contribution carries one gate at most. **No shipped pack uses the `profile` gate** since
2026-09-29: every use was a provider fact a second profile over the same provider lost, and each
moved onto the provider ([PP-D2](../design/providers-and-profiles-redesign.md#PP-D2)). It stays
for a pack of your own whose variant really is a name, and
`TestNoShippedPackKeysAFactOnAProfileName` keeps the shipped packs off it.

Every other kind **refuses** the field, because a modifier nothing reads is an
accepted-and-ignored declaration. That includes the kinds that cross the boundary — `mount`,
`state`, `loophole`, and every host read — which are reviewed when a pack is approved: a launch
flag that switched one on would be a claim the reviewer never saw. A profile stays inside the
claims its pack already made.

The env half folds **per agent and per pack, in delivery order**: the pack's unconditional
`env` keys, then its gated ones satisfied for that agent, so a pack's variant overrides its own
default without a load error ([OQ-8](#pv-oq-8)), while a *later* pack's unconditional value
still beats an *earlier* pack's gated one. `packload.EnvFold(packs, selection, agent)` is the one
definition of that order, agent `""` being the shared fold every process receives, and both
notches reduce the same sequence. Provider variables from the agent's env derive are layered
after the fold, as the more specific intent. Env values are literal strings; a removal has no
spelling in a pack's env map (only a derive's `ctx.tombstone` removes, which the per-agent env
file spells as `unset`).

> [!NOTE]
> **The launch-wide "wide pass" is gone (trap D2, closed).** It matched a gated env against any
> bin the launch installed, so `-p codex=bedrock` fired `packs/claude`'s
> `CLAUDE_CODE_USE_BEDROCK` jail-wide. Ruled out by
> [`OQ-BR4`](../design/provider-credential-scope.md#OQ-BR4) and built with the credential
> gate: a satisfied gate delivers to each agent whose selected profile satisfies it and to no
> other, and the CLI-less case stays reachable.

The config-overlay half composes the provider's facts rather than restating them
([OQ-PT3](#oq-pt3)): a pack that routes an agent through a provider ships the provider entry
and the profile, and the agent's own derive writes the address — `packs/zai` carries no
overlay literal. `config-overlay` plus `profile` is also **the** cross-pack mechanism: a
contribution to another pack's surface, with collision detection, per-key provenance
(`config-overlay:<pack>` in `yolo config diff`), a footprint row and a fixed layer. There is no
second, fragment-shaped kind for it ([OQ-16](#pv-oq-16)).

Which table the overlay gate reads depends on the notch ([OQ-17](#pv-oq-17)). In a jail it is
the table the launcher emitted, `YOLO_USE_PROFILES`, which is the render that actually
happened. At the host notch — `yolo host apply` and `yolo config diff` — it is the **user-scope**
`profile` key alone, because a gated overlay rewrites the user's real config files — and can
rewrite where an agent sends the credentials the user already holds (`ANTHROPIC_BASE_URL`).

### Two channels, split by payload type

A profile's payload goes where its type can survive (the payload-type principle, under
[Principles](#principles)). **Configuration** — endpoints, model aliases, `wire_api`,
permissions, flags an agent reads from its settings, and the *name* of a credential variable —
rides the agent's config surface, reached by the agent derive or by a gated `config-overlay`.
**Environment** — credential values, process flags, and unsets — rides the process env,
through the env derive or a gated `env`, and reaches only a process yolo launches: `yolo --`,
`yolo host --`, or the host wrapper.

claude on `bedrock` is the worked example, and it uses both channels ([D8](#pv-d8)). The profile
names the `bedrock` provider, which `packs/bedrock` ships with no endpoint, so it is never a
credential requirement, with `"platform": "aws-bedrock"`, and with a `region_env_name`, so a
region is ([the region preflight](#the-region-preflight)). Its region comes from the user's
`providers.bedrock` entry, the environment or the host's `~/.aws/config`
([the region file](#the-region-file)), and its model list is the pack's
([the shipped Bedrock provider](#the-shipped-bedrock-provider)). claude's settings derive puts
`CLAUDE_CODE_USE_BEDROCK` into the `env` block of `claude/settings`, so a bare `claude` outside
yolo still runs in Bedrock mode; claude's env derive sets the same variable for a yolo-launched
process, beside `AWS_REGION` and any model id the profile names among the Anthropic entries; and
`packs/aws-auth` contributes its
credentials pointer on a `platform` gate. All three key on the provider's platform, not on the
profile's name, so a profile of your own over `bedrock`, or a Bedrock provider of your own, gets
the same ([`OQ-BR8`](../design/providers-and-profiles-redesign.md#OQ-BR8)). The switch also needs
claude's own transport: a profile over the same provider that routes through the wire bridge
(`via`) gets the pointer and not the switch
([PP-D4](../design/providers-and-profiles-redesign.md#PP-D4)). claude speaks `anthropic`, which
the via route does not pass, so on the shipped `bedrock` such a profile routes claude at the
bridge's adapter address, composed for the via, and the bridge reaches Bedrock by region
([`wire-bridge-gateway.md` WG-I39](../design/wire-bridge-gateway.md#WG-I39)). On a Bedrock provider
whose anthropic address is its own, the bridge carries none of claude's requests, and the launch
says so ([what the via does](#routing-a-profile-through-the-bridge-via)). Until 2026-09-29 the switch rode a
`profile: "bedrock"` gated `env` and `config-overlay` pair. Claude Code honors the settings file's
`env` block before its first API call ([OQ-4](#pv-oq-4)).

> [!NOTE]
> **MEASURED 2026-09-29: Bedrock mode on real credentials. UNMEASURED: the settings-only
> path.** In the live-login try-out a jail's claude ran in Bedrock mode on credentials `aws-auth`
> served from a live `aws sso login`
> ([`sso-backed-bedrock.md` §11](../design/sso-backed-bedrock.md#11-evidence-and-how-to-re-check-it)).
> That claude, like every yolo-launched one, had `CLAUDE_CODE_USE_BEDROCK` in its process env as
> well as in the settings file. [OQ-4](#pv-oq-4) was measured with `ANTHROPIC_BASE_URL` as the
> witness variable — a controlled listener run showed a settings-only value producing traffic
> identical to the process-env control — and `CLAUDE_CODE_USE_BEDROCK` rides the same mechanism,
> but a settings-only Bedrock run on real credentials, a bare `claude` outside yolo, has no record.

### What the launch checks and prints

A selector that silently selects nothing looks exactly like one that works, so each spelling
that can be mistyped is checked against the right set, and each check is fatal:

| Spelling | Checked against | Where |
| :--- | :--- | :--- |
| a `profile` **key** other than `"*"` | the CLI names every **resolvable** pack installs — selected or not | config validation (`yolo check` and every launch); at the host notch (`yolo host --`, `yolo host env`, `yolo host apply` and its automatic run at a wrapped launch, `yolo config render --at host`) the provider and profile section of the same validation over user scope (`config.ValidateProviderSection`), with the launched command's own key refused first, adding the `-p` spelling that is legal (`config.UnknownProfileKey`) |
| `-p <cli>=<name>` | the same namespace | launch preflight (`checkProfileTargets`) — a flag never reaches config validation |
| a selected profile **name** | the declared set: selected packs' profiles plus the user's `profiles` | launch preflight, both notches |
| a `-p`/`--profile` with **no value** (trailing, followed by `--`, or `--profile=`) | nothing: it is refused as "`-p` needs a value", exit 2 | the front door, both notches, through one value-flag reader that `--at`, `--network` and `--with-credentials` share |
| a **list** (`-p pi=a,b`, a `profile` array) | the grammar (no element before a pair, no empty entry: exit 2), then whether the CLI holds a set and [the set's own rules](#an-active-set-several-profiles-for-one-agent) | the front door's parser; config validation and `checkProfileTargets` for the CLI; `packload.ProfileSetProblems` after resolution, at both notches and in `yolo check` |

The key check answers against the **universe**, not the selection: whether a string names a real
CLI is a fact about the packs this machine can resolve, while selection only decides whether a
contribution renders. When the universe cannot be enumerated — a configured pack that does not
resolve — the key check steps aside; that pack is refused on its own terms, first and louder. A
**bare** `-p <name>`, like the `profile` key's `"*"` or string form, is not checked against
anything but the declared set: on the run path it keys the name onto every CLI the selected packs
install that no pair beside it names (for the key, no named entry beside `"*"`), never onto the
command after `--`, so there is no CLI name in it to mistype. At the host notch both reach the one
command after `--` only when a selected pack installs it. For any other command a bare `-p` is
refused, naming the grant that is legal there, `--with-credentials`
([the host notch](#the-credential-gate)); the key's `"*"` or string form simply does not reach it,
so that command runs with no profile and nothing is refused.

When anything is selected, the launch prints one line per distinct profile name. It names the
selected packs that **declare** the name (or says your config's `profiles` does), and then, for
each agent the profile table keys to it, the provider its selection resolved to and how that
agent reaches it at this notch:

```text
Profile bedrock: declared by bedrock; claude → provider "bedrock", through claude's own "aws-bedrock" client; pi → provider "bedrock", through pi's own "aws-bedrock" client
```

The route is what the launch composed, read from declarations: the endpoint protocol resolution
paired (`on its "openai" endpoint`), a via that serves the agent here (`through pack
"wire-bridge"'s via route`), the agent's own client for a provider named only by its platform, or
its own client for a provider that re-points nothing. **A platform is bound by the agent's pack**:
it ships a provider of that platform, needs a selected pack that does, or declares its program's
switch or region variables for it; every shipped agent pack that binds Bedrock needs
`packs/bedrock` ([the shipped Bedrock provider](#the-shipped-bedrock-provider)). A **warning** follows
the line, naming why and the fix, in two cases:

- the provider names no endpoint, only a platform, and no selected pack binds that platform for
  the agent, so the selection configures nothing for it (copilot under `-p bedrock`, which has no
  Bedrock client of its own). `yolo host --` then opens no credential doorway for that agent
  either ([HS-D23](../design/host-notch-services.md#HS-D23));
- the provider names no endpoint, so [the credential preflight](#the-credential-preflight) asks
  nothing of it, and none of the credential variables it claims reaches the agent at this notch.
  The warning names them, and names the withheld pointer that would carry one when there is one
  (aws-auth's, with its loophole off). "Reaches" is what that agent receives: in a jail its own
  delivery and a container's `-e` pairs, at `yolo host` the environment the exec hands it, the
  shell included.

It never says *honored* ([OQ-10](#pv-oq-10)): a binding is a declaration, and what a derive does
with the string is unobservable from the launcher. The every-pack **received** list the line used
to carry is gone: the table does reach every pack's derive whole, which is true of every launch
and so told no one anything, and on 2026-09-29 it listed fifteen receivers for a `yolo host -- pi`
that started with no model and no key. An attach that delivers a profile prints the same line, and
so do `yolo host --` and `yolo host env`, over the one profile their launch selects
(`packload.ProfileDisclosures`, the one function every notch calls). It is a disclosure, so no
flag hides it ([OQ-RO3](report-tiers.md#why-its-this-way)).

The host notch also runs the [OQ-SSO8](../design/sso-backed-bedrock.md#OQ-SSO8) override check
a jail launch runs: a pack's env contribution delivered beside a variable the pack declares
overrides it refuses `yolo host --`, and `yolo host env` prints the same refusal without
refusing. The one input that differs is the invoking shell, which the agent `yolo host` execs
inherits, so a variable exported there counts as delivered at the host and never in a jail. A
pointer the host withholds because nothing there serves it has nothing to override. The one it
serves does: since `yolo host --` opens aws-auth's adapter for an agent on a Bedrock provider
([`host-notch-services.md` §4.8](../design/host-notch-services.md#48-yolo-host)), a Bedrock
bearer or a static key pair exported beside that pointer refuses the launch as in a jail, and
`yolo host env`, which opens nothing, withholds the pointer and refuses nothing.

### A switch in the agent's own config

An agent can be switched onto a provider platform by its own settings, whatever yolo selects:
Claude Code reads `CLAUDE_CODE_USE_BEDROCK` from the `env` block of `~/.claude/settings.json`,
which reaches a jail as `claude/settings`' host layer and is claude's own file at `yolo host`.
yolo obeys a switch you wrote and deletes nothing it did not write; one it wrote itself it removes
when it stops asserting it (below). When the switch is on (JSON `true`, `1`, or
`1`/`true`/`yes`/`on` in any case) and claude's selected provider is not of that
platform, the credential gate sends claude none of that platform's credentials, so the launch
prints one line naming the conflict and both fixes
([PP-D1](../design/providers-and-profiles-redesign.md#PP-D1), ruled 2026-09-29):

```text
claude: ~/.claude/settings.json sets CLAUDE_CODE_USE_BEDROCK, which puts claude on its own "aws-bedrock" client, but no "aws-bedrock" provider is selected for claude, so yolo delivers it none of that platform's credentials: select one (-p bedrock), or remove CLAUDE_CODE_USE_BEDROCK from ~/.claude/settings.json (yolo leaves it alone).
```

**A switch yolo wrote is named as yolo's.** `yolo host apply` writes claude's switch into your real
`~/.claude/settings.json` while claude's host selection (the `profile` key) is on a Bedrock provider
([D8](#pv-d8)), and records the value it wrote at `/env/CLAUDE_CODE_USE_BEDROCK` in the host's
computed-leaf record, beside the provenance record
([HC-D25](../design/host-computed-layer.md#HC-D25)). When the file holds that recorded value, the
line says `yolo host apply` wrote it for claude's host selection, and that applying with claude on
a provider of another platform removes it, instead of telling you to remove a key of yours:

```text
claude: ~/.claude/settings.json sets CLAUDE_CODE_USE_BEDROCK, which `yolo host apply` wrote there for claude's host selection, and it puts claude on its own "aws-bedrock" client, but no "aws-bedrock" provider is selected for claude, so yolo delivers it none of that platform's credentials: select one (-p bedrock), or run `yolo host apply` with claude on a provider of another platform, which removes it.
```

That apply does remove it: a leaf the settings derive stops asserting is cleared when the file
still holds the value yolo wrote. A switch you wrote before yolo asserted it, or changed after, is
never recorded and never removed.

The `-p` it offers is a declared profile over a provider of that platform that routes through no
via service; with none declared it says to select a provider of that platform. It is a disclosure,
never a refusal: every jail arm prints it beside the provider preflight (the fresh container
launch, the attach and every macos-user invocation) and `yolo host --` before its preflights, so a
launch they then refuse still says it. At `yolo host` your own credentials may still serve claude,
which is why the line says what yolo delivers rather than that the launch will fail. `yolo host
env` and `yolo host apply` launch nothing and print nothing.

Core knows no agent's file or variable: the switch is the agent pack's declaration, a `program`
contribution's `platform_switches` (`{platform, surface, pointer}`; packs/claude declares
`aws-bedrock` at `/env/CLAUDE_CODE_USE_BEDROCK` in `claude/settings`, a `readsHost` surface), and
`packload.PlatformSwitchConflicts` reads it from your own copy of that file. Not read: a switch
captured from an in-jail edit of the jail's settings file, and one exported in the process
environment.

## What this does not license

- **No reopening the `wire_api` enum** as a free string — that restores the
  pass-through-verbatim failure the closed vocabulary closed.
- **No agent names in core.** The protocols an agent speaks are declared by that agent's pack
  and spelled by its derive, never held in a Go table; `internal/agentenv`'s agent→protocol map
  was deleted with the placeholder vocabulary it existed to serve, and `internal/agentenv` is now
  only the environ overlay both notches share.
- **No cross-pack fragment kind.** A pack reaching another pack's surface does it through
  `config-overlay`, gated by `profile` when it is conditional. A kind that targets "a pack"
  rather than a surface cannot say which file it lands in, which layer, who wins a shared key,
  or what `config diff` shows — the four questions `config-overlay` already answers.
- **No provider written anywhere but a `provider` contribution or the `providers` key.** A pack
  names a provider; it never inlines one in an untyped payload, where every check the typed
  schema buys (the URL rule, the `wire_api` set, the name-only credential pointer) would be one
  nesting level away from bypassed.
- **No profile-conditional boundary claims.** The modifier gates `env` and `config-overlay`
  only; making a mount, a host read, state or a loophole conditional on a launch flag would let
  an approved pack claim something its reviewer never saw.
- **No profile on launch flags.** The `launch` kind is retired; a pack's launch flags live only
  in its autonomy postures, where the confinement notch can withhold them, and a
  profile-switched flag would be one no notch could take away.
- **No stacking.** One profile per CLI per launch; two profiles active for one CLI would need
  a precedence rule between two variants on one key, and no use case has asked for one.
- **No provider registry or discovery.** Providers are shipped by packs or written by hand.
- **No secrets scanner.** The schema is the mechanism; a user who wants a content tripwire runs
  one in CI over the same files.
- **No gating the catalog on selection.** The directory is the feature; pi and opencode have
  interactive pickers that browse it.
- **No clearing a value yolo did not write.** The selection record is the whole authority for a
  [deselection clear](#deselection-clear-what-yolo-wrote-keep-what-the-user-wrote): no record, or
  a corrupt one, means no claim, and a value the user changed is never cleared.
- <a id="no-launch-time-model-id-refusal"></a>**No refusing a launch because a config holds a
  model id outside the selected provider's `models`.** It would catch deselection residue loudly,
  but it also refuses every legitimate hand-picked model, and a launch-time check cannot see a
  mid-session `/model` switch at all. An enforced model allowlist is the wire bridge's job, which
  sees every request ([`OQ-WG3`](../design/wire-bridge-gateway.md#OQ-WG3), ruled, not built).
- **No value schema for options** — a typechecker in core is `wire_api`'s enum one layer up.
- **No credential VALUE in any composed or wire table.** The name crosses; the value is
  hydrated per derive invocation and in the 0600 env files (`yolo-user-env.sh` for an
  unclaimed value, the selecting agent's own file for a claimed one) only.

> [!WARNING]
> **Three derives currently breach that last line.** The pi, opencode and copilot derives fall
> back to a literal `options.api_key` when a provider names no `api_key_env_name`, and core
> validates no option value, so a key written there is accepted, crosses in `YOLO_PROVIDERS`,
> and lands in the agent's file or environment. The invariant stands and the fallback is the
> drift: do not document it as a supported spelling.

## Why it's this way

Rulings a future change would otherwise undo, kept with their original IDs (cited from code
comments and sibling docs). Every row has its own anchor; `#why-its-this-way` still resolves for
older links.

| Ruling | Why it holds |
| :--- | :--- |
| <a id="oq-pt1"></a>[OQ-PT1](#oq-pt1) — three canonical names, nobody's dialect | A pass-through cannot work by accident; borrowed spellings cannot be translated because none is canonical. |
| <a id="oq-pt2"></a>[OQ-PT2](#oq-pt2) — refuse the composed `base_url`+`endpoints` pair | The shorthand-as-override is the ambiguous spelling once more than one protocol exists; per-field override is spelled `endpoints.<protocol>.base_url`. Retiring the shorthand outright finished this ruling rather than reversing it. |
| <a id="oq-pt3"></a>[OQ-PT3](#oq-pt3) — a profile-gated `config-overlay` composes the provider's fact rather than restating it | A URL literal in an overlay is a second copy of a provider fact, and the two drift; `packs/zai` ships the provider and the profile and no overlay. |
| <a id="oq-pt4"></a>[OQ-PT4](#oq-pt4) — the credential requirement follows catalog membership | Pack presence means in the dictionary; a `null`-dropped provider leaves the dictionary and the requirement together. |
| <a id="oq-pt5"></a>[OQ-PT5](#oq-pt5) — `--timing` takes the timing meaning; `-p`/`--profile` are name-only | The overloaded parse cost two fix commits; the heuristic is deleted, not made careful. |
| <a id="oq-pt8"></a>[OQ-PT8](#oq-pt8) — `kind: "profile"` is `{name, provider}` and nothing else | A config patch, launch flags and an env map were never a profile; as contributions gated on a profile name they live in the kinds that own those channels, and the CLI-less reachability defect goes with the body rather than being guarded. A `config` on a profile is refused with the migration named. |
| <a id="oq-pt9"></a>[OQ-PT9](#oq-pt9) — everything goes to the derive, credential included | The sandbox never was the boundary: a derive already controls `mcp_servers` commands, and a fetched pack's `env` is granted in-jail exec knowingly. |
| <a id="oq-cs1"></a>[OQ-CS1](#oq-cs1) — selection written into each agent's own key | "Activating a profile should work for all." |
| <a id="oq-cs2"></a>[OQ-CS2](#oq-cs2) — never write the selection key when no profile is active; narrowed by [OQ-PSW2](#oq-psw2) | An interactive in-agent choice must survive the next launch. It still binds the derive, which emits nothing selection-shaped with no profile active: no default, and no tombstone. What [OQ-PSW2](#oq-psw2) narrowed is the render's old never-clear. A value yolo wrote and nobody edited is now cleared on deselect, and a value the user wrote is still kept. |
| <a id="oq-psw2"></a>[OQ-PSW2](#oq-psw2) — deselecting clears what yolo wrote, by omission, and forgets it | A value only yolo's stale record protects is residue: the next launch asks a new endpoint for the old provider's model. Omission rather than a tombstone, so the host layer's value returns and holds ([the tombstone warning](#deselection-clear-what-yolo-wrote-keep-what-the-user-wrote)); forgetting the record entry means a later hand-typed identical id is the user's. |
| <a id="oq-psw4"></a>[OQ-PSW4](#oq-psw4) — the clear is silent on the terminal and recorded in `boot.log` | Ruled *"no print, except in verbose mode"*: a deselect is a routine path, and a line on every one is noise; the log makes a changed file explainable afterward. The verbose half waits on the jail getting a verbosity ([`OQ-DB1`](../design/diagnostics-past-the-boundary.md#oq-db1)) and is then one call site ([where a clear is recorded](#where-a-clear-is-recorded)). |
| <a id="oq-sw1"></a>[OQ-SW1](#oq-sw1) — a selection outranks a host-layer value | *"Just because they're the host files doesn't mean you want them that way in the jail."* An explicit `-p` is a more specific choice than a standing host default, and the hazard [OQ-CS2](#oq-cs2) protects, an in-jail edit, still wins because it differs from the host value ([the override](#a-selection-outranks-a-host-layer-value)). |
| <a id="oq-cs3"></a>[OQ-CS3](#oq-cs3) — core resolves no model | The derive gets the active profile and the provider entry and picks its own agent's model and fallback; `default` is an ordinary alias, not a core concept. |
| <a id="oq-cs4"></a>[OQ-CS4](#oq-cs4)/CS7 — provider-declared flat options; core checks the key census only | "Model can't be the only config we'll want"; a validated value set is the enum mistake one layer up. |
| <a id="oq-cs5"></a>[OQ-CS5](#oq-cs5) — `profiles` and `use_profiles` are user-scope-only | A workspace config is agent-editable and travels with the repo; it cannot steer endpoints. `use_profiles` is the `profile` key since [PP-D10](../design/providers-and-profiles-redesign.md#PP-D10), and the rule holds for it. |
| <a id="oq-cs6"></a>[OQ-CS6](#oq-cs6) — declaration is mandatory | An undeclared name is diagnosable instead of silently inert; reverses the old free-form ruling deliberately. |
| <a id="oq-cs8"></a>[OQ-CS8](#oq-cs8) — the agent pack composes the binding in its own derive | Core stops holding an agent→protocol table; each agent declares how a selection reaches it. |
| <a id="oq-cs9"></a>[OQ-CS9](#oq-cs9) — profiles point at a provider; no `extends` | Provider-declared option defaults already remove the duplication inheritance would fix. |
| <a id="oq-cs10"></a>[OQ-CS10](#oq-cs10) (withdrawn → constraint) — the host notch runs the env derive | `yolo host -- claude` composes the same environment; the composition is host-launch-time, so the derive is too. |

### The profile-variant rulings

These rows carry the **bare** `OQ-N` ids of the retired profile-variant design
(`profiles-as-pack-variants.md`), and `D8` from that design's diff table. A bare id is ambiguous
across this repository — a dozen other docs carry a first question of their own under the same number — so these resolve here only
when a citation names this doc or the retired design beside them. The anchors carry a `pv-`
prefix for that reason. Rows marked SUPERSEDED are tombstones: the id is still cited, and the
row says what replaced it.

| Ruling | Why it holds |
| :--- | :--- |
| <a id="pv-oq-1"></a>[OQ-1](#pv-oq-1) — `autonomy` and `profile` stay two kinds | The confinement-conditional keys live in `autonomy` and nowhere else, and the selectors are asymmetric: the notch chooses a posture and nothing a config or CLI writes can reach it, while a profile name arrives through channels a user writes. Merging them puts a permission bypass behind a user-owned selector. |
| <a id="pv-oq-2"></a>[OQ-2](#pv-oq-2) — `providers` stays flat | A profile selects a provider by name and never reshapes the map; where one credential serves several endpoint shapes, the shape axis lives inside the entry as `endpoints.<protocol>`. |
| <a id="pv-oq-3"></a>[OQ-3](#pv-oq-3) — SUPERSEDED by [OQ-CS6](#oq-cs6) | Profile names were free-form and never checked. Declaration is now mandatory and an undeclared name refuses the launch; a code comment still reading "profile VALUES are free-form" is stale. What survives is that a gated contribution whose name is not selected is a clean skip. |
| <a id="pv-oq-4"></a>[OQ-4](#pv-oq-4) — Claude Code honors `settings.json`'s `env` block before the first API call | MEASURED (claude 2.1.252, scratch config dir, inherited `ANTHROPIC_*` scrubbed): a settings-only `ANTHROPIC_BASE_URL` produced traffic identical to the process-env control. It is what lets a flag like `CLAUDE_CODE_USE_BEDROCK` ride the config file — UNMEASURED for that variable itself. |
| <a id="pv-oq-5"></a>[OQ-5](#pv-oq-5) — SUPERSEDED in part by [OQ-CS6](#oq-cs6) | A bare `-p <name>` still reaches every selected pack — that half stands. "Declared or not" is dead: the name must be declared. |
| <a id="pv-oq-6"></a>[OQ-6](#pv-oq-6) — `api_key_env` is renamed `api_key_env_name` | The value is the NAME of a variable, and the spelling says so before the regex has to. The old key is refused by name with the replacement in the message. |
| <a id="pv-oq-7"></a>[OQ-7](#pv-oq-7) — SUPERSEDED by [OQ-PT8](#oq-pt8) | A `null` in a profile's env map once meant *unset*; the map died with the profile body. Pack env values are literal strings, and a derive's `ctx.tombstone` is the only removal. |
| <a id="pv-oq-8"></a>[OQ-8](#pv-oq-8) — a pack's gated env later-wins over its own static env | The variant is the more specific intent and overrides its own default; it is not a load error. The fold stays per pack, so a later pack's static value still beats an earlier pack's gated one. |
| <a id="pv-oq-10"></a>[OQ-10](#pv-oq-10) — the launch line says DECLARED and RECEIVED, never "honored" | What a derive does with the string is unobservable from the launcher; a print that overclaims is the silent-skip failure wearing a badge. |
| <a id="pv-oq-12"></a>[OQ-12](#pv-oq-12) — `kind: "provider"` exists; pack defaults < user overrides | Endpoints are facts about the service and belong in a shareable pack; only the credential pointer is machine-local. It reversed the design's own "providers stay a config key". |
| <a id="pv-oq-13"></a>[OQ-13](#pv-oq-13) — the credential preflight is scoped to the SELECTED set | Selecting a provider pack is the intent. "Configured but unselected stays inert" was withdrawn: an unkeyed provider in the catalog is a failure at the agent's first request. |
| <a id="pv-oq-14"></a>[OQ-14](#pv-oq-14) — SUPERSEDED by [OQ-CS8](#oq-cs8) and [OQ-PT9](#oq-pt9) | The provider-declared `env_shape` is gone with its validators; the agent's own pack composes the variables in its env derive, credential included. What survives is "no agent is special-cased" — a code comment citing it as the live binding is stale. |
| <a id="pv-oq-16"></a>[OQ-16](#pv-oq-16) — `config-overlay` takes a `profile` gate, and it is the cross-pack mechanism | A cross-pack contribution already has collision detection, provenance, a footprint and a layer here; a second, fragment-shaped kind would answer none of those. What the overlay carries was re-ruled by [OQ-PT3](#oq-pt3). |
| <a id="pv-oq-17"></a>[OQ-17](#pv-oq-17) — the host notch's overlay gate reads USER-SCOPE config only | A gated overlay rewrites real-home keys, and a workspace config is agent-editable. The jail notch reads the table the launcher emitted, whose blast radius is the disposable home. |
| <a id="pv-d8"></a>[D8](#pv-d8) — Bedrock's non-secret half is a managed patch to `claude/settings` | `yolo host apply` refuses a pack's `env`, and a bare invocation outside yolo gets no process env; the settings file is the channel both survive. The process env still carries the credentials and the region. |

## Current values

Verified at `7ad8358c`, except the deselection rows for the boot log and the id-writing
surfaces with a host layer, verified at `ca86d945`, and the rows the `openai-codex` model list
touched (the clear's log line, codex's `openai-codex` default, the list and pi's copy of it),
verified at `2a34a176`, except the host half of pi's copy, verified at `f3da48dc`, and the
tier-alias and pi-subagents rows, verified at `58fc65ce`, and the rows the provider-keyed gates added (the platform, the shipped Bedrock provider, the region requirement, both gates, the platform switches and llamacpp's attribution header), verified 2026-09-29 against the tree that shipped the platform switch, and the Bedrock rows the one-provider build rewrote or added (the shipped Bedrock provider, its model list, the vendor, the makers, the built-in ids, `bedrock-bridge`, the region requirement, pi-subagents'), verified 2026-09-29 against the tree that shipped `packs/bedrock`'s model list. The prose
above explains what each is for; this table is the only place the exact spellings are stated.

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| Canonical `wire_api` set | `anthropic`, `openai-chat-completions`, `openai-responses` | `packdecl.knownWireAPIs` |
| Provider table env var | `YOLO_PROVIDERS` | `internal/cli/run` env block |
| Selection table env var | `YOLO_USE_PROFILES` | same |
| Resolved-profiles env var | `YOLO_PROFILES` | same |
| Selection namespace key | `selection` | `agentcfg.SelectionKey` |
| Selection record path | `<workspace>/.yolo/prism/<agent>-<name>.selection.json` in a jail; the state dir's host-capture store at the host notch under `host_management: own` | `render.Target.SelectionPath` |
| Deselection clear's log line | `selection: cleared <agent>/<surface> <key> (was <value as JSON>): yolo's selection no longer sets it`, one per cleared key whose value left the file, the value cut at 200 bytes with a trailing `…`. A key is cleared when its profile is deselected, or when a derive stops naming it while the profile stays active | `entrypoint.noteSelectionClears` |
| Where that line goes | `<workspace>/.yolo/boot.log` (the previous boot's is `boot.log.prev`); never the terminal | `entrypoint.bootLogName`, `Env.note` |
| Id-writing surfaces with a host layer | pi's `settings` (`~/.pi/agent/settings.json`) only; codex's `config.toml` and opencode's `opencode.json` declare no `readsHost` | `packs/{pi,codex,opencode}/pack.json` |
| codex's model for `openai-codex` | the profile's `model` option; the first declared `openai-codex` id when the profile names none or names `default` (`gpt-6.1-sol` as shipped); no `model` when the list is empty | `packs/codex/derive.lua` |
| The `openai-codex` model list | ids `gpt-6.1-sol`, `gpt-6-astra`, `gpt-6-luna` in that order, each with a `[1m]` variant at 1,000,000 tokens after it; declared as `models` (alias = id) plus `model_options` facts `order`, `name`, `description`, `context_window`, `long_context_window` | `packs/openai-auth/pack.json` |
| pi's copy of that list | `~/.pi/agent/yolo-openai-codex-models.json`, the computed surface `pi/codex-models`: `{"models": [{"id", "base", "name", "contextWindow"}, …]}`, read by the openai-auth extension at load. At the host notch it is `{}` under `host_management: assert` and refused under `own` | `packs/pi/pack.json`, `packs/pi/extensions/yolo-openai-auth.js` |
| User config keys | `providers` (merged-scope — **except the ADDRESS**), `profiles` / `profile` (user-scope-only); `use_profiles` and `agent_profiles` refused by name as old spellings of `profile` | `internal/config` |
| Provider credential-routing scope | Every provider field that decides where a credential goes is **USER-SCOPE ONLY**: a workspace `yolo-jail.jsonc` or `yolo-jail.local.jsonc` carrying one is a fatal config error naming the field and the user config. The address, `endpoints.<protocol>.base_url`, since 2026-09-17 ([`OQ-LM3`](../research/local-model-endpoints.md#oq-lm3)). Since 2026-09-28 ([OQ-NC6](../plans/notch-convergence.md#OQ-NC6), the field list [NC-D63](../plans/notch-convergence.md#NC-D63)) also the rest of `endpoints` in any form (a protocol with no URL, a `wire_api`, a null removing an endpoint or the map), `api_key_env_name` (a value re-points the claim, a null unclaims the key so every process receives it), and a null provider or null `providers`, which remove claims. Since 2026-09-29 also `platform`, which decides which agents a pack's credential pointer reaches ([PP-D7](../design/providers-and-profiles-redesign.md#PP-D7)). `models`, `options`, `region` and `capabilities` still merge from either scope, a `region` only as one DNS label ([a region is a host-name part](#a-region-is-a-host-name-part)). The reason is the workspace file is AGENT-EDITABLE, and these fields decide where a credential and the inference behind it go. The entry-level `base_url` shorthand is refused at any scope | `internal/config/validate.go` (`validateProviderCredentialScope`, `validateProviderRegion`) |
| Missing-provider hatch | `YOLO_ALLOW_MISSING_PROVIDERS=1`, for the credential and the region preflights | `internal/paths` |
| Provider platform | `platform`, one token, open vocabulary; `aws-bedrock` is the one value read today (claude's derive, aws-auth's gate, the region preflight); a derive reads the selected provider's as `ctx.selected_platform` | `packdecl.PlatformProblem`, `luahook` (`selectedPlatform`) |
| The shipped Bedrock provider | `bedrock` in the bedrock pack: `"platform": "aws-bedrock"`, no endpoints, no region, no options, the six AWS credential names under `api_key_env_name`; needed by claude, codex, opencode and pi, and needing aws-auth | `packs/bedrock/pack.json`, each agent pack's `needs` |
| The Bedrock model list | `global.anthropic.claude-opus-5-5` (vendor `anthropic`, order 1), `us.openai.gpt-6.1-sol` (`openai`, 2), `global.openai.gpt-6-astra` (`openai`, 3); each keyed by its id, with `name`, `context_window`, `max_tokens` and `input` (and `reasoning` for Opus) in `model_options`; no `default` alias; no Region detection, so codex starts on GPT-6.1 Sol in every Region and opencode and pi on Claude Opus 5.5, and GPT-6 Sol is not shipped ([BR-D19](../design/bedrock-plumbing.md#BR-D19), superseding [BR-D17](../design/bedrock-plumbing.md#BR-D17)'s global-first pick). Read from each AWS model card on 2026-09-29 | `packs/bedrock/pack.json`, `packs/bedrock/README.md` |
| Model vendor | `vendor`, one lowercase token (`[a-z0-9][a-z0-9._-]*`), in a pack's `model_options.<alias>` or a user's object-form `models.<alias>`; an entry with none is offered to every agent | `packdecl.ValidModelVendor`, `config.validateModelEntry`, `packload.flattenModelFacts` |
| Makers each Bedrock client calls | claude `anthropic`; codex `openai`; opencode and pi every maker | `packs/{claude,codex,opencode,pi}/derive.lua` (`callableModels`) |
| Bedrock built-in provider ids | codex `amazon-bedrock-runtime`; opencode `amazon-bedrock`; pi `amazon-bedrock` | `packs/{codex,opencode,pi}/derive.lua` |
| The bridge-forcing Bedrock profile | `bedrock-bridge` = `{provider: bedrock, via: wire-bridge}` | `packs/bedrock/pack.json` |
| Region requirement | a provider's `region_env_name` beside its `platform` (pack manifests only), a requirement of every provider of that platform; the bedrock pack's `bedrock` declares `AWS_REGION`, `AWS_DEFAULT_REGION` for `aws-bedrock`; a program's `platform_regions` narrows the list for that agent alone, and packs/opencode's lists `AWS_REGION` | `packs/bedrock/pack.json`, `packload.ProviderRegionGaps` |
| Region file | a provider's `region_file` `{path, path_env_name, profile_env_name, default_profile, profile_section, key}` beside `region_env_name` (pack manifests only); the bedrock pack's: `.aws/config`, `AWS_CONFIG_FILE`, `AWS_PROFILE`, `default`, `profile {profile}`, `region`; an `env` contribution's `region_profile_setting` beside `served_by` and a `platform` gate, and aws-auth's pointer's is `profile`; delivered in the agent's first region variable, disclosed as `Region: …` | `packs/bedrock/pack.json`, `packs/aws-auth/pack.json`, `packload` (`regionfill.go`) |
| Kinds that take the `profile` modifier | `env`, `config-overlay` — refused on every other kind; no shipped pack uses it | `packdecl` `validateContribution` |
| Kinds that take the `platform` gate | `env` — one gate per contribution, `profile` or `platform`; `platform` on `provider` is the declaration | `packdecl` `validateContribution` |
| Platform switches | a `program`'s `platform_switches` `[{platform, surface, pointer}]`; claude's: `aws-bedrock`, `claude/settings`, `/env/CLAUDE_CODE_USE_BEDROCK`; on when `true`, `1`, `yes` or `on` | `packs/claude/pack.json`, `packload.PlatformSwitchConflicts` |
| Profile flag grammar | `-p` / `--profile`: a bare name or comma list, or `cli=name` pairs (comma-separated, a bare name after a pair continuing that CLI's list, repeatable), on every notch; at `yolo host` / `yolo host env` a pair may name only the one command composed | `config.ParseProfileFlag`, through `parseProfileValue` (`applyProfileValue` on the run path, `hostProfileFor` at the host) |
| Profile disclosure line | `Profile <name>: declared by <packs, or your config's profiles>; <agent> → provider "<p>", <route>; …`, then one `Warning: profile "<name>" …` line per agent the selection reaches nothing for or delivers no credential to | `packload.ProfileDisclosures`; printed by `run.noteUseProfiles` and `hostComposition.profileLines` |
| Conventional tier aliases | `default`, `fast`, `balanced`, `frontier`; a missing one warns at boot as `pack derive for <agent>: provider "<name>" declares no "<alias>" model alias …`, and only when a derive asks `yolo.model_for` for it | `luahook.ConventionalModelAliases`, `luahook.MissingTierAliasNote` |
| pi-subagents' block | `subagents.defaultProvider`, `subagents.defaultModel` (`<provider>/<id>`, deleted when no default resolves), `subagents.modelScope` `{enforce: true, strict: true, allow}` (the configured ids, or `<provider>/*`), in `~/.pi/agent/settings.json` whenever a pi profile selects `openai-codex`, a provider pi can reach, or a Bedrock provider on pi's own client (then `amazon-bedrock`) | `packs/pi/derive.lua` (`piSubagents`) |
| zai model IDs | `glm-4.6`, `glm-5.3`, `glm-5.3-flash`; the default is `glm-5.3`. These are wire-true IDs; Claude alone appends `[1m]` when `context_window` ≥ 1000000. | `packs/zai/pack.json` |
| zai Coding Plan OpenAI endpoint | `https://api.z.ai/api/coding/paas/v4` (`openai-chat-completions`) | `packs/zai/pack.json` |
| zai provider options | `model: glm-5.3`, `context_window: 1000000`, `api_timeout_ms: 3000000` | `packs/zai/pack.json` |
| zai credential variable | `ZAI_API_KEY` | `packs/zai/pack.json` |
| llamacpp endpoints | `anthropic`: `http://localhost:8080`; `openai`: `http://localhost:8080/v1` (`openai-chat-completions`) — a LOCAL inference server (llama.cpp `llama-server`), reached through `network.forward_host_ports` | `packs/llamacpp/pack.json` |
| llamacpp attribution header | the provider option `attribution_header: "false"`, which claude's derive turns into `CLAUDE_CODE_ATTRIBUTION_HEADER=0` (a profile may set `"true"`); only claude receives it | `packs/llamacpp/pack.json`, `packs/claude/derive.lua` |
| llamacpp credential | **none.** The provider declares no `api_key_env_name`, so the credential pre-flight requires nothing; each agent's derive supplies its own dummy, which every agent here accepts against a loopback server | `packs/llamacpp/pack.json` |
| llamacpp model id | `llama` — the `--alias` the recipe tells the server to publish, so one id is true across all agents | `packs/llamacpp/pack.json`, `packs/llamacpp/README.md` |
| `needs` vocabulary | top-level manifest key — `needs: [{pack, when_bins}]`, conditional pack dependency resolved as a transitive closure at selection (the added pack prints its cause line on the banner); manifests only, never user config | `internal/packdecl/needs.go`, `internal/packload/needs.go` |
| cerebras model aliases | `default: qwen-3.8-27b` — the one alias; `gpt-oss-120b` deliberately absent (hallucinated tool calls have no tier) | `packs/cerebras/pack.json` |
| cerebras endpoints | `openai`: `https://api.cerebras.ai/v1` (`openai-chat-completions`) — the only endpoint the manifest declares. It hand-wrote an `anthropic` endpoint at the wire bridge's loopback address until 2026-09-18; the bridge's `adapter` contribution declares that address itself now and core composes it into this entry, so the manifest states only Cerebras's own upstream ([`protocol-resolution.md`](protocol-resolution.md)) | `packs/cerebras/pack.json` |
| cerebras provider options | `model: default`, `context_window: "65536"` (the free-tier window; claude's auto-compact triggers at it, and a paid-tier user overrides to `131072` in their own profile) | `packs/cerebras/pack.json` |
| cerebras needs | `wire-bridge` when `claude` or `copilot` is selected — the two agents whose derives read an anthropic endpoint (claude directly; copilot by its D-3 preference), so the launch that composes the adapted anthropic address is the one that stages its listener | `packs/cerebras/pack.json` |
| cerebras credential variable | `CEREBRAS_API_KEY` | `packs/cerebras/pack.json` |
| OpenRouter endpoints | `openai`: `https://openrouter.ai/api/v1` (`openai-responses`); `anthropic`: `https://openrouter.ai/api` | `packs/openrouter/pack.json` |
| OpenRouter credential variable | `OPENROUTER_API_KEY` | `packs/openrouter/pack.json` |
| Kilo endpoints | `openai`: `https://api.kilo.ai/api/gateway` (`openai-chat-completions`) — the only endpoint the manifest declares. Its hand-written `anthropic` loopback endpoint was **eliminated rather than moved** on 2026-09-18: Kilo and cerebras share the bridge's single `openai → anthropic` adaptation now, so the second loopback port Kilo used to name is declared nowhere ([`protocol-resolution.md`](protocol-resolution.md)) | `packs/kilo/pack.json` |
| Kilo reachability | Claude and Copilot use the wire bridge — through the adapter's composed `anthropic` address, not a Kilo-declared one; Pi and OpenCode use the direct OpenAI route; Codex has no documented Responses route and receives no entry | `packs/kilo/pack.json`, `packs/wire-bridge/pack.json`, `packs/{claude,codex,pi,opencode,copilot}/derive.lua` |
| Kilo credential variable | `KILO_API_KEY` | `packs/kilo/pack.json` |
| Gateway model catalogs | Neither gateway pack declares a model or model default. The user supplies `providers.<gateway>.models`; profiles name an alias with their `model` option. | `packs/{openrouter,kilo}/pack.json` |
