---
status: current
verified: 2026-09-23
verified_commit: 7ad8358c
covers:
  - internal/packdecl/contributes.go
  - internal/packdecl/envnames.go
  - internal/packload/credentialscope.go
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
(`provider-switching.md`, graduated 2026-09-26), and they were verified against `38814ba4` on 2026-09-26.
The rest of the doc keeps its `7ad8358c` stamp. MEASURED: the clear, its boot-log record and
the host-layer override are pinned through the boot render by unit tests in `internal/entrypoint`.
The adopting-boot failure was reproduced through the same render at `38814ba4`, and the fix that
[the clear on an adopting boot](#a-clear-holds-on-an-adopting-boot-too) describes is pinned the same
way, newer than that stamp. UNMEASURED: no live agent session has been watched across a deselect.

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
| User config: `providers`, `profiles`, `use_profiles` | `internal/config` (`profiles.go`, `UseProfileCLINames`) |
| The `profile` modifier's two gates | `internal/packload` (`EnvFold`) for `env`; `internal/packoverlay` (`Collect`) for `config-overlay` |
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
[the credential preflight](#the-credential-preflight)'s question.

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

## What crosses to the jail

Three environment variables, all delivered on every **entry** — a fresh launch and an
attach alike — through the **channel section** of `yolo-user-env.sh` (0600, live-mounted,
rewritten whole by each entry; `writeUserEnvFile` in `internal/cli/run`):

- `YOLO_PROVIDERS` — the composed table, **secret-free** (`api_key_env_name` carries the NAME
  of a variable, never a value).
- `YOLO_USE_PROFILES` — the effective selection: CLI name → profile name.
- `YOLO_PROFILES` — the RESOLVED profile table: name → `{provider, <options>}`, the output of
  the one lowering (below). In-jail derives and the host notch read the same resolved shape;
  no user-config parsing happens in-jail.

The same section carries the SHARED pack env fold (every pack's unconditional `kind: "env"`)
as plain-form `export K='v'` lines, which the boot's hydrate applies OVER the environment —
the def-form `export K=${K:-'v'}` lines above them (the env_sources no provider claims) keep
the opposite precedence. What the credential gate scopes to ONE agent — its provider's claimed
env_sources, the profile-gated env its selection satisfies, and its env derive's shape vars
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
- An attach to a jail launched BEFORE the file crossing carries the tables in its
  frozen environment, which its older entrypoint lets beat the file — so the attach
  compares its own selection table to the jail's FROZEN one and splits by
  explicitness: a TYPED `-p` naming a different profile refuses (the delivery cannot
  take, and the remedy names the two-command restart series — `yolo stop`, then an
  ordinary launch); a config-side drift warns and proceeds; a matching or empty
  selection is a plain re-entry and stays silent.
- An attach to a jail launched after that but BEFORE [the credential gate](#the-credential-gate)
  reads the shared file on every entry but has no per-agent env directory, and its launchers
  source none. A current launch freezes `YOLO_AGENT_ENV_FILES=1` into the container, so an
  attach that inspects an environment without it knows the per-agent half cannot arrive. When
  this entry scopes nothing to any agent, the delivery runs as usual. When it does, a TYPED
  `-p` refuses, naming the agents and the restart, and a config-only selection warns by name
  and delivers nothing, so the jail keeps what its last entry gave it. Neither prints the
  gate's "`… only`" disclosure, which would describe a delivery that jail cannot receive.
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
| An `env_sources` value whose name a composed provider **claims** (lists in its `api_key_env_name`) | each agent whose selected profile resolves to a claiming provider; no other process, a bare shell included |
| An `env_sources` value no provider claims (`GH_TOKEN`, anything else) | every process, as before |
| A `profile`-gated `kind: "env"` contribution | the pack's own agent when it selected that profile; for a pack that installs no CLI (`aws-auth`, `llamacpp`), every agent that selected it |
| An env derive's output (the shape vars) | its own agent, and the derive's copy of the table carries the `api_key` of that agent's provider only |

`packs/claude`'s `bedrock` provider claims `AWS_BEARER_TOKEN_BEDROCK`, `AWS_ACCESS_KEY_ID`,
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
- **macos-user.** One command per invocation, so the delivery is **per launch**: the session
  carries the shared values plus the launched program's own, and says so when another agent
  had values it cannot carry. `macosuser.buildPlan` no longer hydrates `env_sources` itself.
- **The host notch.** `yolo host -- <agent>` composes one process: the shared values plus that
  agent's. The shell it inherits is the user's and passes through untouched.

Every arm discloses what it scoped or withheld, by name and never by value
(`CredentialScope.Disclosure`). The files are readable by every process of the jail's uid, as
the shared file is: the gate decides what each agent's **environment** carries, and an agent
started by another agent inherits that agent's environment, as any child does. The menu half of
[`OQ-CN4`](../design/provider-credential-scope.md#OQ-CN4) is each agent's own key:
opencode's derive writes `enabled_providers: [<selected provider>]` beside its selected model;
claude's single `ANTHROPIC_BASE_URL` already reaches one provider per launch; pi's
`enabledModels` is a soft shortlist and restricts nothing.

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
table, string, math libraries only; no `os`, no `io`). Two registrations:

- `yolo.derive(agent, surface, fn)` — the file half. Runs in-jail at boot for each declared
  surface, returning that surface's computed layer.
- `yolo.env(agent, fn)` — the env half. Runs **host-side only**: its output crosses
  per-entry through the agent's own env file on the container backends
  ([the credential gate](#the-credential-gate)),
  `yolo host` has no jail at all, and the macos-user backend fixes its plan env before
  the bootstrap runs. One runner (`AgentEnv`) serves both notches — that shared
  implementation is what keeps `yolo -- claude` and `yolo host -- claude` composing the same
  environment. An in-jail env derive has no consumer and is never run.

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
`providers`, `use_profiles`), `agent` and `surface`, `profile_name` (the profile active at this
agent's CLI name), `selected_provider` (the provider it resolves to), `profile` (that profile's
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

The failure this closes was reproduced through the boot render at `38814ba4` (pi, a selection
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
> for the same reason.

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
  and a derive default when the profile names none or names `default` (see
  [Current values](#current-values)).
- **A switch from a third-party provider clears the stale `model_provider`.** The selection stops
  naming that key, so the per-key rule above clears the value yolo wrote for the previous
  provider. Nothing special-cases it.

codex's pack declares `openai-responses` among the protocols its program speaks, which is the
protocol the `openai-codex` entry serves, so pointing codex at it resolves like any other pairing
([`protocol-resolution.md`](protocol-resolution.md)).

## Per-agent delivery

What each agent actually receives, from one composed table and one selection:

| Agent | Catalog | Selection |
| :--- | :--- | :--- |
| codex | `~/.codex/config.toml` `[model_providers.<id>]` (TOML); never a row for `openai-codex` | top-level `model_provider` + `model`; `model` alone for `openai-codex` ([above](#selecting-openai-codex-for-codex)) |
| pi | `~/.pi/agent/models.json` `providers.<id>` (JSON; credential as `apiKey: "${VAR}"` config-value syntax) | `~/.pi/agent/settings.json` `defaultProvider` + `defaultModel` (a pair of bare ids), and `enabledModels` (the scoped list, default first) |
| opencode | `~/.config/opencode/opencode.json` `provider.<id>` — `baseURL`/`apiKey` live UNDER `options` | top-level `model = "<provider>/<model>"` |
| omp | `~/.oh-omp/agent/models.yml` `providers.<id>` (YAML; credential as the provider's env-var NAME, which oh-omp resolves before treating it as a literal) | **none** — the derive writes a catalog and no selection key, so a selected profile makes the provider *available* and the user chooses it inside the agent |
| copilot | no catalog (BYOK is env-var-only; no copilot config file has provider keys) | process env from the copilot pack's env derive: `COPILOT_PROVIDER_BASE_URL` (the sole activation gate), `COPILOT_PROVIDER_TYPE`, `COPILOT_PROVIDER_WIRE_API` (openai type only), `COPILOT_MODEL` (required — a provider with no resolvable alias composes nothing at all), `COPILOT_PROVIDER_API_KEY` (a placeholder for a keyless loopback endpoint), `COPILOT_PROVIDER_MAX_PROMPT_TOKENS` ← the provider's `context_window` option |
| claude | no catalog (claude has no provider directory) | process env from the claude pack's env derive: the address and credential for the provider's `anthropic` endpoint (`ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN` — a dummy token on a routed launch that has no key, so claude never falls back to the user's own subscription login), `AWS_REGION` from the provider's `region`, one model id per claude tier resolved from the provider's aliases (the selected one from the profile's `model` option), and knobs composed from provider options (the context window, request and stream timeouts). Claude's `[1m]` suffix is appended to every model id when the `context_window` option is at least one million — it is Claude Code's client syntax for the context-1m beta, stripped before the wire — and non-essential traffic is disabled on any routed launch. The exact variable set is the derive's, in `packs/claude/derive.lua` |

The spellings are facts about each agent, source-verified and carried as provenance comments
in the derives (pi 0.84.4's settings-manager keys and its ten-id api registry; opencode's
first-slash model format and options nesting; codex's binary-verified `responses`-only). The
model a selection names is resolved IN THE DERIVE — alias = the profile's `model` option or
`default`, then the provider's `models` map; core resolves no model.
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
`id` (required — the wire id, which is usually not the alias), plus optional `name`,
`reasoning`, `input`, `cost`, `context_window`, and `max_tokens`. The field set is **closed**
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
NAME selects that profile for every selected pack (uniformly, whether or not a command
follows `--`), and `cli=name` (comma-separated, repeatable) selects for the named CLI only.
The persistent form is `use_profiles` in user config. Profile names refuse `=` at
declaration so the two grammars cannot be ambiguous, and neither flag means startup
timing — that is `--timing`. (The former third spelling `--pack-profile` is deleted —
never in a release, and redundant once `-p` carried both grammars.)

> [!WARNING]
> **The flag is parsed per notch, and only the run path takes the pair grammar.**
> `applyProfileValue` is where `cli=name` is understood; `yolo host` and `yolo host env` parse
> the flag in their own bodies and accept a bare profile NAME only — one notch runs one agent,
> so there is nothing for a pair to key against. Neither host parser ever carried the timing
> meaning, so [OQ-PT5](#oq-pt5)'s split touched the run path alone: do not "unify"
> them, the grammars differ because the notches do. The run path's help scan mirrors its parse
> flag for flag, which is the other half of the split — `-p` consumes the next token there
> too, so `yolo -p -h` reads a profile named `-h` rather than answering help, deliberately.

## Profiles and options

A **profile** is a named selection over one provider, and the name is what the user types. It is
also the whole of the `profile` kind: whatever a pack does differently while a profile is active
lives on other contributions, gated by name ([the `profile` modifier](#the-profile-modifier)).
Whether a gate should key on the profile name or the provider, and what `-p <name>` should name
at all, is open in [`providers-and-profiles-redesign.md`](../design/providers-and-profiles-redesign.md).

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
name used to be a silent no-op; it is a diagnosable error instead.

The selection itself is a table keyed by **CLI name** — the bin a pack installs — mapping each
CLI to the profile it runs: `use_profiles` in user config, then `-p` on the command line
([the flag grammar](#per-agent-delivery)). CLI names are the right key because the namespace is
already exclusively owned — `program` is sole-owned by bin, so a CLI name resolves to at most
one pack — and because a pack slug is not what a derive knows itself by. Both `profiles` and
`use_profiles` are **user-scope-only** ([OQ-CS5](#oq-cs5)): a workspace file travels with the
repo and is agent-editable, and a profile steers which endpoint and which model an agent talks
to. The selector's pre-rename spelling, `agent_profiles`, is refused by name with the
replacement in the message. Every derive receives the **whole** table, so a pack that installs
no CLI — a provider pack — still reads any CLI's selected name.

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
"use_profiles": { "pi": "pi-zai" }
```

What it does, in order:

1. **The launch adds the pack.** Selecting the profile brings `wire-bridge` into the jail the way
   `needs` does, and says so (`+ wire-bridge (via of profile pi-zai, active for pi)`). A `via`
   naming a pack yolo does not ship, or one that declares no `via_address`, refuses the launch.
   Only an agent a selected pack installs counts: a `use_profiles` entry for an agent the launch
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
`use_profiles` selection; a `-p` is an argument to a launch that has not happened, so `check`
cannot see it.

| What the via does for the agent | Launch | `yolo check` |
| :--- | :--- | :--- |
| **Nothing**: the agent's config does not point at its via URL, for example pi with a via over `openai-codex` | warns on stderr that the via has no effect, and starts ([WG-I15](../design/wire-bridge-gateway.md#WG-I15)) | WARN |
| **Points the agent at a prefix the bridge serves no route for**: the provider declares neither wire, is not in the composed table, or is the ChatGPT subscription (opencode, whose derive re-points any selected provider) | refuses, naming the profile, the agent, the reason and the via URL ([WG-I13](../design/wire-bridge-gateway.md#WG-I13)) | FAIL |
| **Points the agent at a route with only the wire it does not prefer**, for example pi on a Responses-only provider such as `openrouter` | warns on stderr, naming the endpoint the provider lacks, and starts ([WG-I14](../design/wire-bridge-gateway.md#WG-I14)) | WARN |

The agent's **preferred wire** *(coined in [WG-I14](../design/wire-bridge-gateway.md#WG-I14))* is
the one its pack's first declared protocol names: chat-completions for `openai`, Responses for
`openai-responses`. For every shipped agent that is the wire its derive speaks on the via route, so
the third row's requests will fail. It is a warning, not a refusal, because the launcher reads the
wire off the declared `protocols`, not off what the derive writes, and a pack yolo does not ship
can write either. An agent whose first protocol is neither, such as claude or copilot, or that
declares none, such as agy, is not checked: none of their derives read the via URL.

`via` is a field, not an option: the provider's option census does not apply to it, and a user's
`via` replaces a pack-shipped one for the same profile name. It works for agents that take a base
URL and keep its path: pi, oh-omp and opencode, which speak chat-completions there, and codex,
which speaks Responses. claude and copilot already reach the bridge through its adapter routes,
which a via profile does not change. At the host notch (`yolo host`) there is no bridge daemon,
so the agent uses its own client. That holds even when `wire-bridge` is listed in `packs`: the
host clears every via address before any derive reads one
([WG-I12](../design/wire-bridge-gateway.md#WG-I12)).

### The `profile` modifier

Two kinds take `profile: "<name>"`, and each asks a different holder of the name whether it is
active. An inactive gate is a **clean skip** — no error, no orphan report — because selection is
the optionality.

| Kind | Active when | Why that key |
| :--- | :--- | :--- |
| `config-overlay` | the name is the profile active for the **target surface's owning agent** (the `agent` half of `agent/name`) | the surface names an agent, so the surface is what the gate asks |
| `env` | per AGENT: the name is the profile that agent selected, and the pack is either the one installing that agent's CLI or a pack installing no CLI at all | an env has no surface to name an agent, so the delivery names one: the agent's own env file ([the credential gate](#the-credential-gate)). The CLI-less arm is what keeps a CLI-less pack's gated env reachable (`packs/aws-auth` and `packs/llamacpp` ship this case), for each agent that selected it |

Every other kind **refuses** the field, because a modifier nothing reads is an
accepted-and-ignored declaration. That includes the kinds that cross the boundary — `mount`,
`state`, `loophole`, and every host read — which are reviewed when a pack is approved: a launch
flag that switched one on would be a claim the reviewer never saw. A profile stays inside the
claims its pack already made.

The env half folds **per agent and per pack, in delivery order**: the pack's unconditional
`env` keys, then its gated ones satisfied for that agent, so a pack's variant overrides its own
default without a load error ([OQ-8](#pv-oq-8)), while a *later* pack's unconditional value
still beats an *earlier* pack's gated one. `packload.EnvFold(packs, profiles, agent)` is the one
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
`use_profiles` alone, because a gated overlay rewrites the user's real config files — and can
rewrite where an agent sends the credentials the user already holds (`ANTHROPIC_BASE_URL`).

### Two channels, split by payload type

A profile's payload goes where its type can survive (the payload-type principle, under
[Principles](#principles)). **Configuration** — endpoints, model aliases, `wire_api`,
permissions, flags an agent reads from its settings, and the *name* of a credential variable —
rides the agent's config surface, reached by the agent derive or by a gated `config-overlay`.
**Environment** — credential values, process flags, and unsets — rides the process env,
through the env derive or a gated `env`, and reaches only a process yolo launches: `yolo --`,
`yolo host --`, or the host wrapper.

`packs/claude`'s `bedrock` is the worked example, and it uses both channels ([D8](#pv-d8)). The
profile names the `bedrock` provider, which the pack ships as a bare name — no endpoint, so it
is never a credential requirement — and whose region and model ids come from the user's
`providers.bedrock` entry. A gated `config-overlay` puts `CLAUDE_CODE_USE_BEDROCK` into the
`env` block of `claude/settings`, so a bare `claude` outside yolo still runs in Bedrock mode; a
gated `env` sets the same variable for a yolo-launched process; the claude env derive composes
`AWS_REGION` and the model ids from the provider entry; and `packs/aws-auth` contributes its
credentials pointer under the same gate. Claude Code honors the settings file's `env` block
before its first API call ([OQ-4](#pv-oq-4)).

> [!NOTE]
> **UNMEASURED: Bedrock mode itself.** [OQ-4](#pv-oq-4) was measured with `ANTHROPIC_BASE_URL`
> as the witness variable — a controlled listener run showed a settings-only value producing
> traffic identical to the process-env control. `CLAUDE_CODE_USE_BEDROCK` rides the same mechanism, but
> the re-test with real AWS credentials that was to confirm it before the Go path was deleted
> has no record of having run, and nothing has yet run Bedrock against a live `aws sso login`.

### What the launch checks and prints

A selector that silently selects nothing looks exactly like one that works, so each spelling
that can be mistyped is checked against the right set, and each check is fatal:

| Spelling | Checked against | Where |
| :--- | :--- | :--- |
| a `use_profiles` **key** | the CLI names every **resolvable** pack installs — selected or not | config validation (`yolo check` and every launch) |
| `-p <cli>=<name>` | the same namespace | launch preflight (`checkProfileTargets`) — a flag never reaches config validation |
| a selected profile **name** | the declared set: selected packs' profiles plus the user's `profiles` | launch preflight, both notches |

The key check answers against the **universe**, not the selection: whether a string names a real
CLI is a fact about the packs this machine can resolve, while selection only decides whether a
contribution renders. When the universe cannot be enumerated — a configured pack that does not
resolve — the key check steps aside; that pack is refused on its own terms, first and louder. A
**bare** `-p <name>` is not checked against anything but the declared set: it keys the name onto
every CLI the selected packs install, never onto the command after `--`, so there is no CLI name
in it to mistype.

When anything is selected, the launch prints one line per distinct profile name: **DECLARED** —
the selected packs shipping a profile of that name — and **RECEIVED** — every selected pack,
because every derive gets the whole table. It never says *honored* ([OQ-10](#pv-oq-10)): what a
derive does with the string is unobservable from the launcher, and a transparency line that
overclaims is the silent-skip failure wearing a badge. An attach that delivers a profile prints
the same line.

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
| <a id="oq-cs5"></a>[OQ-CS5](#oq-cs5) — `profiles` and `use_profiles` are user-scope-only | A workspace config is agent-editable and travels with the repo; it cannot steer endpoints. |
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

Verified at `7ad8358c`, except the four deselection rows (the clear's log line, the boot log,
the id-writing surfaces with a host layer, and codex's `openai-codex` default), which were
verified at `38814ba4`. The prose above explains what each is for; this table is the only
place the exact spellings are stated.

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| Canonical `wire_api` set | `anthropic`, `openai-chat-completions`, `openai-responses` | `packdecl.knownWireAPIs` |
| Provider table env var | `YOLO_PROVIDERS` | `internal/cli/run` env block |
| Selection table env var | `YOLO_USE_PROFILES` | same |
| Resolved-profiles env var | `YOLO_PROFILES` | same |
| Selection namespace key | `selection` | `agentcfg.SelectionKey` |
| Selection record path | `<workspace>/.yolo/prism/<agent>-<name>.selection.json` in a jail; the state dir's host-capture store at the host notch under `host_management: own` | `render.Target.SelectionPath` |
| Deselection clear's log line | `selection: cleared <agent>/<surface> <key> (was <value as JSON>): the profile that set it is no longer selected`, one per cleared key whose value left the file, the value cut at 200 bytes with a trailing `…` | `entrypoint.noteSelectionClears` |
| Where that line goes | `<workspace>/.yolo/boot.log` (the previous boot's is `boot.log.prev`); never the terminal | `entrypoint.bootLogName`, `Env.note` |
| Id-writing surfaces with a host layer | pi's `settings` (`~/.pi/agent/settings.json`) only; codex's `config.toml` and opencode's `opencode.json` declare no `readsHost` | `packs/{pi,codex,opencode}/pack.json` |
| codex's model for `openai-codex` | the profile's `model` option; `gpt-6-sol` when the profile names none or names `default` | `packs/codex/derive.lua` |
| User config keys | `providers` (merged-scope — **except the ADDRESS**), `profiles` / `use_profiles` (user-scope-only); `agent_profiles` refused by name as the old spelling of `use_profiles` | `internal/config` |
| Provider address scope | `endpoints.<protocol>.base_url` is **USER-SCOPE ONLY** since 2026-09-17: a workspace `yolo-jail.jsonc` or `yolo-jail.local.jsonc` carrying one is a fatal config error. The rest of a `providers` entry still merges from either scope. The reason is the workspace file is AGENT-EDITABLE, and the address decides where inference goes — [`OQ-LM3`](../research/local-model-endpoints.md#oq-lm3) calls it the one answer that cannot be revised later without a breaking config change. The entry-level `base_url` shorthand is refused at any scope | `internal/config/validate.go` |
| Missing-provider hatch | `YOLO_ALLOW_MISSING_PROVIDERS=1` | `internal/paths` |
| Kinds that take the `profile` modifier | `env`, `config-overlay` — refused on every other kind | `packdecl` `validateContribution` |
| Profile flag grammar | `-p` / `--profile`: a bare name, or `cli=name` (comma-separated, repeatable) on the run path; a bare name only on `yolo host` / `yolo host env` | `internal/cli` (`applyProfileValue`) |
| Profile disclosure line | `Profile <name>: declared: <packs or none>; received: <every selected pack>` | `run.noteUseProfiles` |
| zai model IDs | `glm-4.6`, `glm-5.3`, `glm-5.3-flash`; the default is `glm-5.3`. These are wire-true IDs; Claude alone appends `[1m]` when `context_window` ≥ 1000000. | `packs/zai/pack.json` |
| zai Coding Plan OpenAI endpoint | `https://api.z.ai/api/coding/paas/v4` (`openai-chat-completions`) | `packs/zai/pack.json` |
| zai provider options | `model: glm-5.3`, `context_window: 1000000`, `api_timeout_ms: 3000000` | `packs/zai/pack.json` |
| zai credential variable | `ZAI_API_KEY` | `packs/zai/pack.json` |
| llamacpp endpoints | `anthropic`: `http://localhost:8080`; `openai`: `http://localhost:8080/v1` (`openai-chat-completions`) — a LOCAL inference server (llama.cpp `llama-server`), reached through `network.forward_host_ports` | `packs/llamacpp/pack.json` |
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
