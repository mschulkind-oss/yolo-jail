---
status: current
verified: 2026-09-03
verified_commit: fb7b566d
covers:
  - internal/packdecl/contributes.go
  - internal/packload/providers.go
  - internal/packload/profiles.go
  - internal/packload/deriveenv.go
  - internal/agentcfg/selection.go
  - internal/agentcfg/luahook/derive.go
  - internal/entrypoint/packsurfaces.go
  - internal/entrypoint/prism.go
  - internal/config/profiles.go
  - packs/claude/derive.lua
  - packs/codex/derive.lua
  - packs/pi/derive.lua
  - packs/opencode/derive.lua
  - packs/zai/pack.json
  - packs/claude/pack.json
tags: [providers, profiles, packs, derives, selection, zai]
---

# The provider system — catalog, composition, and selection

**Status:** CURRENT as of 2026-09-03, verified against `fb7b566d`.

A **provider** is a declaration of a service's facts — where its endpoints are, which wire
protocol each speaks, which model aliases it offers, which environment variable holds its
credential, and which knobs ("options") a profile may tune. Providers compose into ONE table
on the host at launch, cross into the jail, and reach four agents through their own packs'
derives — each in that agent's own vocabulary. A **profile** is user-declared intent: a named
selection over a provider. **Catalog** (an agent's directory of providers it *could* use) and
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
| User config: `providers`, `profiles`, `use_profiles` | `internal/config` (`profiles.go`) |
| The four derives + the two provider packs | `packs/{claude,codex,pi,opencode,zai}` |

**Reads with:** [`pack-system.md`](../design/pack-system.md) (what a pack is, how derives are
loaded), [`local-model-endpoints.md`](../research/local-model-endpoints.md) (the
source-verified per-agent vocabularies the dialect maps translate into).

---

## Principles

**P1. A closed enum is only closed at the end that enforces it.** A value yolo validates
against its own list and then writes verbatim into a third party's config file is type-safe
against a type nobody else holds. The canonical `wire_api` vocabulary is either the union of
the consumers' vocabularies **and** each consumer translates, or it is a free string. It is the
former: three canonical names, and each derive translates canonical → its agent's spelling,
emitting **nothing** for a protocol that agent cannot speak.

**P2. A rule enforced on one layer of a merge must hold on the merge's output.** The config
validator refuses a `base_url` + `endpoints` pair in one user-written entry; the composer
refuses to *manufacture* that pair from two legal inputs. A rule the composer can manufacture
a violation of is a lint, not an invariant.

**A profile is user-declared intent over a provider, and the provider owns the schema of what
a profile for it may carry.** Pack-shipped profiles are defaults the user overrides, exactly as
pack-shipped providers are. Core composes facts and resolves names; it never learns what an
option *means* — the derive decides where each one lands.

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

Two refusals guard the output:

- A composed entry carrying both `base_url` and `endpoints` is refused, naming both sources
  (`ProviderAddressConflictMessage`, shared with the config validator so the two layers cannot
  word it differently). Overriding a pack that ships `endpoints` is spelled
  `endpoints.<protocol>.base_url`.
- A provider NAME claimed by two packs is refused by the launch preflight (sole ownership by
  name).

The credential preflight follows **catalog membership**: a composed entry carrying at least one
endpoint means the launch demands that entry's `api_key_env_name` be deliverable — *"in the
dictionary means you need the key."* An entry with no endpoints (Bedrock: ambient AWS chain,
no pointer) demands nothing, and a `null`-dropped provider leaves the table and stops being
required. `YOLO_ALLOW_MISSING_PROVIDERS=1` is the escape hatch a refusal names.

## What crosses to the jail

Three environment variables, all emitted on every launch:

- `YOLO_PROVIDERS` — the composed table, **secret-free** (`api_key_env_name` carries the NAME
  of a variable, never a value).
- `YOLO_USE_PROFILES` — the effective selection: CLI name → profile name.
- `YOLO_PROFILES` — the RESOLVED profile table: name → `{provider, <options>}`, the output of
  the one lowering (below). In-jail derives and the host notch read the same resolved shape;
  no user-config parsing happens in-jail.

The three are a launcher↔jail contract: a change to any of them must move both halves in one
commit. The source-skew gate cannot see env-var contracts.

## The canonical wire_api vocabulary

Three names — `anthropic`, `openai-chat-completions`, `openai-responses` — deliberately
**nobody's dialect**, so a pass-through cannot work by accident (OQ-PT1). Pack authors write
canonical names; `KnownWireAPI` in `internal/packdecl` is the single closed set, enforced at
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
| claude | no config dialect; the env derive reads endpoints directly | composes nothing |

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
- `yolo.env(agent, fn)` — the env half. Runs **host-side only**: the container's env is fixed
  at `podman run`, `yolo host` has no jail at all, and the macos-user backend fixes its plan
  env before the bootstrap runs. One runner (`AgentEnv`) serves both notches — that shared
  implementation is what keeps `yolo -- claude` and `yolo host -- claude` composing the same
  environment. An in-jail env derive has no consumer and is never run.

The derive context (`DeriveCtx`) carries: the live tables (`mcp_servers`, `lsp_servers`,
`providers`, `use_profiles`), `selected_provider` (the active profile's provider), and
`profile` (that profile's resolved options). **Selection resolution is one rule**: the
resolved `YOLO_PROFILES` table (`ProviderFor`) feeds both the surface path and the env path —
never the pack manifests, never a Lua re-derivation. The env runner additionally builds a
**hydrated copy** of the providers table for the derive invocation only, `api_key` resolved
from the hydrated `env_sources` then the invoking environment. The hydrated copy is
per-invocation and never serialized; `YOLO_PROVIDERS` stays secret-free.

Errors are fatal at the boot step (`genStep` → the jail refuses to start) and refuse the
launch host-side. There is deliberately no second reporting channel.

> [!WARNING]
> **The credential does ride the `podman run` argv.** The env derive's output crosses as
> `-e ANTHROPIC_AUTH_TOKEN=<secret>`, visible in `ps` to anything on the host that can see the
> launcher's process. This is structural — an env var must reach the container somehow, and the
> alternatives trade one exposure for another — and it is recorded here because it is the kind
> of fact a reader should not have to rediscover. The *file* half is handled: `yolo-user-env.sh`
> is written 0600.

## Selection: write on activation, never on absence

An interactive in-agent model choice (pi's `/model`, opencode's picker) writes the SAME keys a
selection would. A render that re-asserts those keys every boot would silently revert the
user's choice on the next launch — the exact hazard the selection semantics refuse. So
selection keys ride a **reserved namespace** of the computed layer — `selection`, a flat map
of scalar surface keys — and the stateful render lifts it onto the surface root with an
edge-triggered apply (`ApplySelection`):

| Situation | What the render does |
| :--- | :--- |
| Key absent in the file, selection names it | **writes it** (activation) |
| File value equals what yolo last wrote, selection moved | **writes the new value** |
| File value differs from what yolo last wrote | **keeps the user's value** — yolo claims nothing |
| Selection stops naming the key | **leaves the file untouched** — never clears |

The baseline "what yolo last wrote" is a per-surface record beside the last-render state —
NOT the last render itself (a user edit is captured into the overlay and re-asserted by it,
so last-render converges to the file and would revert the user one boot later). A lost or
corrupt record claims nothing — every key then reads as the user's — which is the safe
direction, and the re-arm path if a selection ever seems inert: delete the key from the file
once. Non-stateful surfaces drop the namespace with a warning; the host render's key-probe
never claims it.

> [!WARNING]
> **Selection values must stay scalars.** The host render discovers dynamic *table* keys by
> probing each derive with an empty selection; a derive that gated a whole table on the
> selection would register no keys there and make the host deep-merge where the boot replaces.
> Catalog tables come from presence; selection keys are scalars.

## Per-agent delivery

What each agent actually receives, from one composed table and one selection:

| Agent | Catalog | Selection |
| :--- | :--- | :--- |
| codex | `~/.codex/config.toml` `[model_providers.<id>]` (TOML) | top-level `model_provider` + `model` |
| pi | `~/.pi/agent/models.json` `providers.<id>` (JSON; credential as `apiKey: "${VAR}"` config-value syntax) | `~/.pi/agent/settings.json` `defaultProvider` + `defaultModel` (a pair of bare ids) |
| opencode | `~/.config/opencode/opencode.json` `provider.<id>` — `baseURL`/`apiKey` live UNDER `options` | top-level `model = "<provider>/<model>"` |
| claude | no catalog (claude has no provider directory) | process env from the claude pack's env derive: `ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN`, `AWS_REGION`, `ANTHROPIC_DEFAULT_{OPUS,SONNET,HAIKU}_MODEL` |

The spellings are facts about each agent, source-verified and carried as provenance comments
in the derives (pi 0.84.4's settings-manager keys and its ten-id api registry; opencode's
first-slash model format and options nesting; codex's binary-verified `responses`-only). The
model a selection names is resolved IN THE DERIVE — alias = the profile's `model` option or
`default`, then the provider's `models` map; core resolves no model.

The user-facing spellings: `--pack-profile <cli>=<name>` selects for one CLI per-launch;
`-p <name>` / `--profile <name>` take a bare NAME (with a command, keyed to that command's
binary; without one, applied to every selected pack); the persistent form is `use_profiles` in
user config. `-p` never takes `cli=name` and never means startup timing — that flag is
`--timing`.

## Profiles and options

Declaration is **mandatory**: a selected name neither a selected pack nor the user `profiles`
key declares refuses the launch, naming what IS declared. User `profiles` entries are
user-scope-only, as is `use_profiles` — a workspace spelling of either is refused (a workspace
file travels with the repo and is agent-editable; it cannot steer which endpoint an agent
talks to). A profile entry carries `provider` (required) plus option values; the option NAMES
a profile may use are the provider's declared `options` — a flat name→default map where null
means *declared, no default*. A profile naming an option the provider does not declare is
refused, naming what it does accept; **no value validation happens in core** — the derive
validates, and its errors refuse the launch. There is no `extends`; profiles point at a
provider, full stop.

`kind: "profile"` is a selection and nothing else: `{name, provider}`. Everything a profile
used to carry as a body is a contribution gated by the `profile:` modifier — `kind: "env"` and
`kind: "config-overlay"` today — and that gate keys on the profile being active for a bin the
pack installs, else active for any bin (the two-pass rule that keeps a CLI-less pack's gated
contributions reachable). packs/claude's `bedrock` is the worked example: the profile names
the provider; a gated env contribution sets `CLAUDE_CODE_USE_BEDROCK`; a gated overlay patches
claude/settings.

## What this does not license

- **No reopening the `wire_api` enum** as a free string — that restores the
  pass-through-verbatim failure the closed vocabulary closed.
- **No agent names in core.** The protocol an agent speaks lives in that agent's derive, not
  in a Go table; `internal/agentenv`'s agent→protocol map was deleted with the placeholder
  vocabulary it existed to serve.
- **No gating the catalog on selection.** The directory is the feature; pi and opencode have
  interactive pickers that browse it.
- **No value schema for options** — a typechecker in core is `wire_api`'s enum one layer up.
- **No credential VALUE in any composed or wire table.** The name crosses; the value is
  hydrated per derive invocation and in `yolo-user-env.sh` (0600) only.

## Why it's this way

Rulings a future change would otherwise undo, kept with their original IDs (cited from code
comments and sibling docs):

| Ruling | Why it holds |
| :--- | :--- |
| OQ-PT1 — three canonical names, nobody's dialect | A pass-through cannot work by accident; borrowed spellings cannot be translated because none is canonical. |
| OQ-PT2 — refuse the composed `base_url`+`endpoints` pair | The shorthand-as-override is the ambiguous spelling once more than one protocol exists; per-field override is spelled `endpoints.<protocol>.base_url`. |
| OQ-PT4 — the credential requirement follows catalog membership | Pack presence means in the dictionary; a `null`-dropped provider leaves the dictionary and the requirement together. |
| OQ-PT5 — `--timing` takes the timing meaning; `-p`/`--profile` are name-only | The overloaded parse cost two fix commits; the heuristic is deleted, not made careful. |
| OQ-PT9 — everything goes to the derive, credential included | The sandbox never was the boundary: a derive already controls `mcp_servers` commands, and a fetched pack's `env` is granted in-jail exec knowingly. |
| OQ-CS1 — selection written into each agent's own key | "Activating a profile should work for all." |
| OQ-CS2 — never write the selection key when no profile is active | An interactive in-agent choice must survive the next launch. |
| OQ-CS4/CS7 — provider-declared flat options; core checks the key census only | "Model can't be the only config we'll want"; a validated value set is the enum mistake one layer up. |
| OQ-CS5 — `profiles` and `use_profiles` are user-scope-only | A workspace config is agent-editable and travels with the repo; it cannot steer endpoints. |
| OQ-CS6 — declaration is mandatory | An undeclared name is diagnosable instead of silently inert; reverses the old free-form ruling deliberately. |
| OQ-CS8 — the agent pack composes the binding in its own derive | Core stops holding an agent→protocol table; each agent declares how a selection reaches it. |
| OQ-CS10 (withdrawn → constraint) — the host notch runs the env derive | `yolo host -- claude` composes the same environment; the composition is host-launch-time, so the derive is too. |

## Current values

Verified at `fb7b566d`. The prose above explains what each is for; this table is the only
place the exact spellings are stated.

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| Canonical `wire_api` set | `anthropic`, `openai-chat-completions`, `openai-responses` | `packdecl.knownWireAPIs` |
| Provider table env var | `YOLO_PROVIDERS` | `internal/cli/run` env block |
| Selection table env var | `YOLO_USE_PROFILES` | same |
| Resolved-profiles env var | `YOLO_PROFILES` | same |
| Selection namespace key | `selection` | `agentcfg.SelectionKey` |
| Selection record path | `<workspace>/.yolo/prism/<agent>-<name>.selection.json` | entrypoint stateful render |
| User config keys | `providers` (merged-scope), `profiles` / `use_profiles` (user-scope-only) | `internal/config` |
| Missing-provider hatch | `YOLO_ALLOW_MISSING_PROVIDERS=1` | `internal/paths` |
| zai model aliases | `default: glm-5.3[1m]`, `fast: glm-5.3-flash[1m]` | `packs/zai/pack.json` |
| zai credential variable | `ZAI_API_KEY` | `packs/zai/pack.json` |
