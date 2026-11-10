---
title: "Bedrock-key provider: implementation plan"
date: 2026-11-10
status: accepted
stage: BUILT
next: "Landed at `e946cfc39`; the owner's Mac agent runs W-BK1 to W-BK3 at main and files a lab report"
depends-on:
  - bedrock-key-provider.md
  - secret-env-scoping.md
tags: [packs, providers, bedrock, aws, aws-auth, credentials, plan]
---

# Bedrock-key provider: implementation plan

**Status:** 2026-11-10. Completed against the tree, written against `bc3d3d8e4`; built as `d95b06451` (Tasks 1 to 3) and `e2eacec79` (Task 4), with Task 5 in the docs commit after them. The Traps section records where the build overrode this plan.
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

- **A `GateSelection` copy that drops `SetClaims` lets the pointer through** at that reader, since an agent
  with no entry is not narrowed. `hostdoorways.go` builds two literals and both copy it. They matter only
  when a launch holds an agent with no client of its own for the platform beside a `bedrock-key` agent,
  where a dropped copy would open the aws-auth doorway for nobody. *Backlog (review finding, minor):* no
  test covers those two copies; the witness is a `PlanHostDoorwaysFor` unit test with such a launch.
- **The `yolo host` worker-gate wording** (`internal/cli/host.go`, `workerGateClause`) still says only "is
  not on platform …". No shipped pack reaches it with a served pointer today (aws-auth is a doorway at that
  notch). *Backlog (review finding, minor):* give it the jail's "and claims a variable its pointer sets".
- **`ProfilesOnly` and literal test selections carry no claims.** A platform gate never fires for
  `ProfilesOnly` (no platforms), so it is safe. *As built (overrides the line this replaced):* an agent with
  no `SetClaims` entry, and a set entry whose provider claims nothing at all, are NOT narrowed. The second
  is the design's own outcome — "a user provider with `platform = "aws-bedrock"` and no list still
  receives the pointer" — which the literal rule would break whenever no same-platform provider lists
  names for it to inherit (the existing `fetchedmodels_test` fixtures are that case). Both are the design's
  behavior, not a hatch; `TestAClaimlessBedrockProviderStillReceivesThePointer` pins the second.
- **Siblings co-claim.** A user provider with `platform = "aws-bedrock"` and no list inherits every name a
  same-platform provider lists — now including `bedrock-key`'s bearer. That is the design's ("both claim
  the pointer variable"), not a bug.
- **The bearer in the public `[env]` is refused (secret-env-scoping row 2) whenever `bedrock` is selected**,
  because `bedrock-key` is composed beside it and claims the name. Every test fixture puts the bearer in the
  private set.
- **Variant names are a flat profile namespace**: `bedrock`'s variant is named `bedrock-bridge`, so
  `bedrock-key`'s must be `bedrock-key-bridge`.
- **A shipped provider must be in `shippedProviderNamedProfiles`** (`internal/packload/shippedmanifest_test.go`)
  or the packload fixtures never resolve its profile, and a rule test passes with the rule deleted.
- **The not-started line must name a profile that claims the pointer.** Before this build it offered every
  profile on the platform, so a user provider sorting before `bedrock` was offered. As built,
  `packload.ProfilesServing` filters them (`packservices.go`), and the line reads "no agent's selected
  provider is on platform … and claims a variable its pointer sets" (`macosuserservices.go` says the same).
- **The golden provider table changes on purpose**: `testdata/provider-table-equivalence.golden` is rewritten
  (`YOLO_UPDATE_PROVIDER_GOLDEN=1`) for the all-shipped and pack-bedrock cases only, which are exactly the
  cases that compose `packs/bedrock`.

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

What only a Mac shows: the gate rule at the macos-user notch with a real Seatbelt session, where the
aws-auth adapter is a doorway opened outside the sandbox (`macosuserdoorways.go`) rather than a jail daemon.
The Linux suite drives that arm only through a stub backend (`TestAMixedBedrockLaunchIsRefusedNamingALaunchOnEither`,
`TestClaudeAtTheBridgeOnMacosUserHoldsNoBedrockAPIKey`). None of these holds the landing; they run on main
after it.

**Shared setup for every witness.** At main `e946cfc39` or later. The
installed `yolo` built from that main (`just install` on the Mac). A fresh workspace,
`/Users/Shared/yolo/bedrock-key`, holding an empty `yolo-jail.toml`. A host AWS profile with a live SSO
session (`aws sso login --profile <p>` done). The user config `~/.config/yolo-jail/config.toml` holds:

```toml
packs = ["claude", "codex", "bedrock"]

[providers.bedrock]
region = "us-east-1"

[providers.bedrock-key]
region = "us-east-1"

[allow.aws-auth]
profile = "<p>"
unnarrowed = true

[env.secrets]
set = { AWS_BEARER_TOKEN_BEDROCK = "not-a-real-key" }
```

The key value is a placeholder: no witness sends a request, and no line may print it.

### W-BK1 — `bedrock-key` opens no aws-auth doorway (OQ-BK2, CN-D22)

1. In the workspace:

   ```
   yolo --setup seatbelt-self -p claude=bedrock-key -- true
   ```

   Expected: exit 0. Stderr has a line starting `Not started: the aws-auth jail daemon, because no agent's
   selected provider is on platform "aws-bedrock" and claims a variable its pointer sets` and ending
   `` select one (`-p <agent>=bedrock`) to start it (providers.md OQ-CN7).``. Stderr has NO line starting
   `Opened the "aws-auth" doorway`, and no line contains `not-a-real-key`.

Report: the exit code and every stderr line naming `aws-auth`. Settles: OQ-BK2's Built column at the
macos-user notch.

*Run 2026-11-10 at `5a69dd0b4`* ([lab report](../lab-reports/2026-11-10-mac-pinned-helpers-and-bedrock-key.md#3-a-static-key-pair-in-the-private-set-blocks-every-bedrock-launch)): **passes**, with the placeholder and with the owner's real static key pair.

### W-BK2 — `bedrock` still opens it (OQ-BK4, the control)

1. In the same workspace:

   ```
   yolo --setup seatbelt-self -p claude=bedrock -- true
   ```

   Expected: exit 0, a line starting `Opened the "aws-auth" doorway (pack "aws-auth"`, and no
   `Not started: the aws-auth jail daemon` line.

Report: the exit code and the `aws-auth` lines. Settles: that the narrowing left the SSO path whole on
macos-user. If W-BK1 passes and this fails, the rule is withholding from a claiming provider: file it as a
defect against `d95b06451`.

*Run 2026-11-10 at `5a69dd0b4`* ([lab report](../lab-reports/2026-11-10-mac-pinned-helpers-and-bedrock-key.md#3-a-static-key-pair-in-the-private-set-blocks-every-bedrock-launch)): **passes with the placeholder. With the owner's real static key pair in the private set, refused** by the aws-auth override check, because `bedrock` claims the pair too ([BK-D5](bedrock-key-provider.md#BK-D5)). Owner decision.

### W-BK3 — one launch cannot mix them (OQ-BK5)

1. In the same workspace:

   ```
   yolo --setup seatbelt-self -p claude=bedrock-key,codex=bedrock -- true
   ```

   Expected: a nonzero exit before the sandbox starts, with stderr containing `Refusing to launch`,
   `One launch cannot mix them`, and `` `yolo -p bedrock-key` or `yolo -p bedrock` ``; no line contains
   `profile = ""` or `not-a-real-key`.

Report: the exit code and the full refusal. Settles: OQ-BK5's remedy at the macos-user notch.

*Run 2026-11-10 at `5a69dd0b4`* ([lab report](../lab-reports/2026-11-10-mac-pinned-helpers-and-bedrock-key.md#3-a-static-key-pair-in-the-private-set-blocks-every-bedrock-launch)): **passes with the placeholder.** With the real pair, the override refusal fires instead and the "cannot mix" remedy never shows.
