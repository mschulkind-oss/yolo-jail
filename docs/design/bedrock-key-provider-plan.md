---
title: "Bedrock-key provider: implementation plan"
date: 2026-11-10
status: accepted
stage: DECIDED
next: "Build Task 1 (the served-pointer claim rule in the gate), then Tasks 2 to 5 in order"
depends-on:
  - bedrock-key-provider.md
  - secret-env-scoping.md
tags: [packs, providers, bedrock, aws, aws-auth, credentials, plan]
---

# Bedrock-key provider: implementation plan

**Status:** 2026-11-10. Completed against the tree, written against `bc3d3d8e4`.
**Design:** [`bedrock-key-provider.md`](bedrock-key-provider.md), `status: accepted`, no question open
(rulings OQ-BK1 to OQ-BK5 and BK-D1 to BK-D6 in its [Decision Ledger](bedrock-key-provider.md#decision-ledger)).
The key file rides [`secret-env-scoping.md`](secret-env-scoping.md)'s `[env.secrets]`, which is built
(`stage: BUILT`), so nothing here waits on it.

> **Precedence.** The design wins on behavior. The tree wins on fact, for cosmetic drift only (a moved
> line, a renamed helper with the same role): follow the tree and say so in the report and the commit.
> This plan is advice and is the first thing to be wrong; never twist the code to match it.
>
> **Stop and report, and do not commit,** when: a task's first test does not fail the way the task
> predicts; a file the task names is dirty with someone else's work; a symbol the task names is gone; the
> fix needs a file outside the task's fence; a gate fails outside the task's scope.

## Words this plan uses

- **The pointer** — `packs/aws-auth`'s platform-gated, `served_by = "aws-auth"` env contribution, which sets
  `AWS_CONTAINER_CREDENTIALS_FULL_URI` and `AWS_CONTAINER_AUTHORIZATION_TOKEN`.
- **The bearer** — `AWS_BEARER_TOKEN_BEDROCK`, a Bedrock API key.
- **A claim**, **the credential gate**, **the private set** — [secret-env-scoping.md §1](secret-env-scoping.md#1-words-this-doc-uses).
- **The served-pointer rule** *(coined by the design, [How G2 holds](bedrock-key-provider.md#how-g2-holds-a-served-pointer-reaches-only-a-provider-that-claims-it))* — quoted under Task 1.

## What was confirmed against the tree

- **BK-D1's help string is half done.** `internal/cli/activeprovider.go` already says "a selected pack or your
  config's [providers] table declares" (commit `2d2ecbf7b`); `internal/cli/config_ref.txt` (the
  `active_provider` field, ~line 103) still says "The name must be one a selected pack declares."
- **The design's unverified region question is moot**: the shipped `bedrock-key` copies `bedrock`'s whole
  region block (`region_env_name` and `region_file.*`), so the region pre-flight and fill read it exactly as
  they read `bedrock`.
- **`yolo config add env.secrets.files '"~/…"' --user`** is the spelling: `internal/config/envsets.go`
  names the key `env.secrets.files`, and `config_ref.txt` already documents `config add env.files` the same way.
- **Every reader of the pointer gate goes through `gateFiresFor`** (`internal/packload/packload.go` ~929):
  the env fold (`envFold`), the override pre-flight (`EnvOverrideFindings` via `gateDelivered`), and the
  daemon-start decision (`UnselectedProfileServedDaemons` via `gateDelivered`, `profileserved.go`). The host
  notch's doorway plan copies `GateSelection` field by field (`internal/cli/run/hostdoorways.go` ~112 and
  ~277).

## Map

| Path | Change |
| :--- | :--- |
| `internal/packload/packload.go` | `GateSelection.Claims` (agent → names its set's providers claim), filled by `SelectionOf`/`SelectionOfSets`; `gateFiresFor` takes the contribution's `ServedBy` and `Vars` and applies the served-pointer rule |
| `internal/packload/envoverride.go` | pass the new gate args; the mixed-launch remedy |
| `internal/packload/profileserved.go` | `gatedServer` carries the contribution's vars; pass them |
| `internal/cli/run/hostdoorways.go` | both `GateSelection` copies carry `Claims` |
| `packs/bedrock/pack.toml` | `bedrock` narrowed to five names; new `providers.bedrock-key` with the region block and a `bridge` variant |
| `internal/packload/bedrockkey_test.go` | new — the gate rule, the daemon decision, the override evaluation, the remedy |
| `internal/cli/run/bedrockkey_test.go` | new — the launch's own call sites over the shipped packs |
| `packs/aws_auth_pointer_test.go` (or a new `packs/bedrockkey_test.go`) | the shipped claims |
| `integration/awsauth_test.go` | new `yolo host` test: `-p bedrock-key` gets the bearer, no pointer, no doorway |
| `internal/cli/config_ref.txt` | BK-D1's help string |
| docs (see [Ships with](#ships-with)) | the provider, the rule, the recipe |

## Reuse before you write

- `credentialClaims(providers)` (`credentialscope.go` ~739) is the ONE claim computation, siblings included
  (PP-D9). Invert it per agent in `SelectionOf`; never re-derive claims from `api_key_env_name` by hand.
- `ProviderFor(resolved, profile)` and `providerEntry` — already used by `SelectionOf` to read a platform.
- Run-level fixtures (`internal/cli/run`): `officialPack(t, name)`, `awsAuthSelected`, `awsAuthServedConfig`
  (envoverrides_test.go), `goldenOptions`/`retireOptions`, `config.NewHydratedEnv()` + `SetPrivate(...)`
  then `o.composePackChannel(cfg, packs, h)` (secretenv_test.go), `o.checkEnvOverrides`, `channel.launchEnv`,
  `channel.served.Serves`, `channel.scope.EnvSourcesFor`. `secretEnvRefusalRun` drives `Run` on a stub
  macos-user backend from a TOML user config — the end-to-end shape for the mixed refusal.
- Integration: `newAWSAuthFixtureWith` + `awsAuthHostAgentScript` + `TestAWSAuthServesAYoloHostAgentThroughItsDoorway`
  is the shape to mirror; the fixture seeds `config.jsonc`, so a TOML config (needed for `[env.secrets]`)
  replaces it by removing `$HOME/.config/yolo-jail/config.jsonc` and writing `config.toml` (what
  `writeUserTOML` does — but do not call it, it re-isolates HOME and loses the fixture's fake `aws`).

## Traps

- **A `GateSelection` copy that drops `Claims` withholds the pointer from every agent** at that reader — the
  host doorway would stay closed with no error. `hostdoorways.go` builds two literals; both must copy it.
  `TestAWSAuthServesAYoloHostAgentThroughItsDoorway` (integration) is the witness that catches it.
- **`ProfilesOnly` and literal test selections carry no `Claims`.** A platform gate never fires for
  `ProfilesOnly` (no platforms), so it is safe. Test literals that set `Platforms` for an aws-auth pointer
  fixture must add `Claims` — rewrite those to the new rule, do not make the rule lenient for a nil map.
  (Constraint: a lenient nil is a silent second path, the class AGENTS.md's escape-hatch rule forbids.)
- **Siblings co-claim.** A user provider with `platform = "aws-bedrock"` and no list inherits every name a
  same-platform provider lists — now including `bedrock-key`'s bearer. That is the design's ("both claim
  the pointer variable"), not a bug.
- **The bearer in the public `[env]` is refused (secret-env-scoping row 2) whenever `bedrock` is selected**,
  because `bedrock-key` is composed beside it and claims the name. Every test fixture puts the bearer in the
  private set.
- **Variant names are a flat profile namespace**: `bedrock`'s variant is named `bedrock-bridge`, so
  `bedrock-key`'s must be `bedrock-key-bridge`.

## Tasks

### Task 1 — the served-pointer rule in the gate (independent)

- **Settled, do not re-open** ([OQ-BK2](bedrock-key-provider.md#decision-ledger), BK-D3): *"A served env
  contribution whose platform gate fires for an agent is delivered to that agent only when the agent's
  provider claims at least one variable the contribution sets. Otherwise it is withheld for that agent."*
  It lives in `gateFiresFor`, the per-agent gate every notch shares. "The agent's provider" is any entry of
  its active set (the gate is already set-wide, AP-P1). No new provider key.
- **Tests first** (`internal/packload/bedrockkey_test.go`, over the shipped packs through `packs.FS`/the
  existing official-pack loader used by `credentialscope_test.go`, composed providers from
  `ComposeProviders`): with the provider table of Task 2 not yet shipped, build the table inline — a
  `bedrock` claiming the five, a `bedrock-key` claiming the bearer, both `aws-bedrock`:
  - `EnvFold(packs, SelectionOf({"claude": "bedrock-key"}, …), "claude")` holds no
    `AWS_CONTAINER_CREDENTIALS_FULL_URI`; on `bedrock` it does.
  - `UnselectedProfileServedDaemons` names `aws-auth` when the only agent is on `bedrock-key`, and not on `bedrock`.
  - `EnvOverrideFindings` with the bearer delivered and the only agent on `bedrock-key` returns nil.
  - a set `[bedrock-key, bedrock]` receives the pointer (an entry claims it).
  Red: `go test ./internal/packload -run BedrockKey` fails on the first assertion (pointer delivered).
- **Change:** `GateSelection.Claims map[string][]string`; `SelectionOf` and `SelectionOfSets` fill it from
  `credentialClaims(providers)` per agent over its set. `gateFiresFor(packs, p, gate, sel, agent)` where the
  gate carries profile, platform, servedBy and the vars it sets (a small unexported struct; signature is
  internal). Rule applies only when `servedBy != "" && platform != ""`. Update `envFold`, `gateDelivered`,
  `profileServedDaemons`/`gatedServer`, `EnvOverrideFindings`. Copy `Claims` in both `hostdoorways.go` literals.
- **Gates:** `go test ./internal/packload ./internal/cli/... ./packs/...`; rewrite (not relax) any test whose
  literal selection fires the pointer without `Claims`.
- **Fences:** no change to `credentialClaims` or to `ScopeCredentials`' env_sources delivery.
- **Commit:** `feat(packload): deliver a served pointer only to a provider that claims it`.

### Task 2 — ship `bedrock-key` and narrow `bedrock` (after 1)

- **Settled, do not re-open** (OQ-BK1, OQ-BK4, BK-D2): `bedrock-key` ships in `packs/bedrock`, platform
  `aws-bedrock`, `api_key_env_name = ["AWS_BEARER_TOKEN_BEDROCK"]`, the region block as `bedrock`'s, and a
  `bridge` variant via `wire-bridge`. `bedrock` claims exactly `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`,
  `AWS_SESSION_TOKEN`, `AWS_PROFILE`, `AWS_CONTAINER_CREDENTIALS_FULL_URI`. No alias, no migration line.
- **Tests first:** a packs-level test reading the shipped manifest: `bedrock`'s list is the five,
  `bedrock-key`'s is the bearer, both `aws-bedrock`, `bedrock-key-bridge` resolves with via `wire-bridge`.
  Run-level (`internal/cli/run/bedrockkey_test.go`, `awsAuthSelected` + `awsAuthServedConfig` + a private
  bearer through `SetPrivate`):
  - `-p bedrock-key`: `checkEnvOverrides` empty; claude's `launchEnv` has the bearer and no pointer;
    `aws-auth` not in the jail daemons (the `Not started:` line, or `UnselectedProfileServedDaemons`).
  - `-p bedrock` with the same private bearer: the bearer reaches no agent; the pointer reaches claude.
  - `-p bedrock` with a bearer and no SSO profile: `checkProviderCredentials` refuses naming bedrock's names.
- **Gates:** `go test ./packs/... ./internal/cli/run -run 'Bedrock|AWSAuth|EnvOverride'`, then `just test-fast`.
  `packs/releasedecode_test.go` and `packs/shippedmanifests_test.go` may list provider/variant names: update
  them to the new entry.
- **Commit:** `feat(bedrock): ship the bedrock-key provider and narrow bedrock to SSO`.

### Task 3 — the mixed-launch remedy (after 2)

- **Settled, do not re-open** (OQ-BK5): `-p claude=bedrock-key,codex=bedrock` stays refused by the
  launch-wide override check; the remedy says one launch cannot mix an agent on `bedrock` with one on
  `bedrock-key`, and names a launch on either alone. It must not offer the empty-profile step there.
- **Detection (planner's, cheap):** the override is "mixed" when every overriding var is claimed by some
  agent of the selection (`sel.Claims`) and the agents receiving them are disjoint from the agents the
  contribution's gate fires for. Name each side by its agents' primary profiles.
- **Tests first:** packload unit (fold + finding text) and one run-level end-to-end through
  `secretEnvRefusalRun` with a TOML config `packs = ["claude","codex","bedrock"]`, `[allow.aws-auth]
  profile`, `[env.secrets] set = { AWS_BEARER_TOKEN_BEDROCK = "bk-probe" }`, `[active_provider] claude =
  "bedrock-key"`, `codex = "bedrock"`: refused, says `cannot mix`, names `yolo -p bedrock-key` and `yolo -p
  bedrock`, no `profile = ""` step, never `bk-probe`.
- **Commit:** `feat(packload): name the one-provider-per-launch remedy for a mixed Bedrock launch`.

### Task 4 — the host-notch integration test (after 2)

- `TestAWSAuthWithholdsItsPointerFromABedrockKeyHostAgent` in `integration/awsauth_test.go`: the
  fixture as in `TestAWSAuthServesAYoloHostAgentThroughItsDoorway`, config rewritten as TOML with
  `active_provider.pi = "bedrock-key"`, `[allow.aws-auth] profile`, `[env.secrets] set = { AWS_BEARER_TOKEN_BEDROCK = "…probe…" }`;
  a stand-in pi that prints `POINTER=` and `BEARER=present|absent` (never the value). Expect `POINTER=` empty,
  `BEARER=present`, and no `opened the "aws-auth" doorway` line.
- **Commit:** `test(integration): a bedrock-key host agent gets the key and no aws-auth pointer`.

### Task 5 — help and docs (after 3)

- See [Ships with](#ships-with). **Commit:** `docs: the bedrock-key provider and the served-pointer rule`.

## Ships with

- Unit: the four Task 1 cases; Task 2's manifest and three run-level cases; Task 3's remedy.
  Integration: Task 4 at the host notch (no container), plus the existing
  `TestAWSAuth.*` and `TestBedrock.*`/`TestPlainBedrock.*`/`TestABedrock.*` as the regression witnesses
  for the SSO path.
- `internal/cli/config_ref.txt` — the `active_provider` sentence (BK-D1).
- `packs/bedrock/README.md` — two providers; the five names; the bearer is `bedrock-key`'s.
- `packs/aws-auth/README.md` — the pointer reaches only a provider that claims it; the "never in the same
  jail" warning becomes "never in one launch".
- `userguide/guides/providers-and-models.md` — the Bedrock API key section: `yolo config add
  env.secrets.files … --user`, then `yolo -p bedrock-key` / `yolo active-provider claude bedrock-key`;
  drop the statement that native `bedrock` claims the bearer.
- `docs/reference/providers.md` (the credential gate) and `docs/reference/agent-credentials.md` (what
  refuses and what warns) — the served-pointer rule; `bedrock` no longer claims the bearer.
- `docs/design/bedrock-plumbing.md` §6.3 — the hand-written provider is now shipped; link the design.
- `docs/design/bedrock-key-provider.md` — status line and the `Built` column.
- No key value in any message: every new line names variables only (BK-D4).

## Don't

- Don't add a provider key (`login`, `aws_auth`) — rejected, OQ-BK2.
- Don't alias `bedrock` to `bedrock-key` or add a migration line — rejected, OQ-BK4.
- Don't add a per-agent override pre-flight — rejected, OQ-BK5.
- Don't touch or warn about the `--user-layer` recipe — BK-D5.
- Don't promise an attach with the other provider — the design leaves it unpromised.

## Mac checks

Written at landing, below, when the build knows what only a Mac can show.
