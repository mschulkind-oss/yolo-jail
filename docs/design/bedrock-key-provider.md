---
title: "The Bedrock API key is a provider you select, not a layer you pass"
date: 2026-11-07
status: accepted
stage: BUILT
next: "Rerun W-BK2 on macos-user with a static pair to verify the corrected private-set refusal guidance"
depends-on:
  - bedrock-plumbing.md
  - providers-and-profiles-redesign.md
tags: [packs, providers, bedrock, aws, aws-auth, credentials]
summary: "A Bedrock API key becomes a provider named bedrock-key that an owner selects with -p or yolo active-provider, like every other provider. The credential gate keeps the key to the agents that select it, and a served pointer is withheld from an agent whose provider does not claim it, so SSO stays off where the key is used without a layer file or a flag."
---

# The Bedrock API key is a provider you select, not a layer you pass

**Status:** 2026-11-11. Built and merged at `e946cfc39`; the provider and remedy are in main. Evidence verified at `5a69dd0b4`. MEASURED: W-BK1 to W-BK3 ran on macos-user at main `5a69dd0b4` on darwin/arm64 with installed yolo `0.17.1+197.g5a69dd0b4075`; the placeholder cases passed, and the real static pair exposed the expected aws-auth override refusal recorded below. Follow-up `b35cff4e6` distinguishes TOML `[env.secrets].files` from `.set` in the refusal, gives each only its matching removal guidance, and redacts private inline values in config-edit diffs while preserving ordinary changes and write disclosure. Linux regression tests pass; rerun W-BK2 with a synthetic static pair on macos-user after that commit reaches main. All five questions were ruled by the owner on 2026-11-09 and folded into the body; the key file's home follows [`secret-env-scoping.md`](secret-env-scoping.md), the same pattern as every provider's key.

> **In short.** The key is already a credential a provider can claim; what it lacks is a provider
> of its own and a rule that keeps `aws-auth`'s pointer away from that provider. Both come from
> declarations the packs already make, so the layer file and its flag become unnecessary rather
> than forbidden.

**Why it matters.** Every other provider is one word on the command line. The key is a layer file
plus a flag, and the layer exists only to hold two facts the provider table could state.

**The shape.** A `bedrock-key` provider on platform `aws-bedrock` that claims exactly
`AWS_BEARER_TOKEN_BEDROCK` and the static key pair ([BK-D5](#BK-D5)); the shipped `bedrock` narrowed to the five chain variables; and one
gate rule — a served env contribution reaches an agent only when its provider claims a variable
the contribution sets.

**Cost.** `bedrock` does not claim the bearer; it retains the static AWS pair and SSO pointer
claims. A delivered pair and active aws-auth pointer cannot share a launch, because the pair would
silently take precedence over narrowed SSO credentials. One launch may not mix an agent on
`bedrock` with one on `bedrock-key`; separate launches can choose different providers when their
credential inputs do not conflict.

**Start at [The design](#the-design)** — the provider, the claims, and the two guarantees.

**Needs your ruling:** None.

**Reads with:** [`bedrock-plumbing.md`](bedrock-plumbing.md) (the three supported credentials, and
the hand-written API-key provider this replaces),
[`providers-and-profiles-redesign.md`](providers-and-profiles-redesign.md) (the claims rule, PP-D9,
and the one-selection-answers-every-gate rule, PP-D5),
[`agent-credentials.md`](../reference/agent-credentials.md) (what refuses a launch and what only
warns), [`secret-env-scoping.md`](secret-env-scoping.md) (how every provider's key in `[env.secrets]`
reaches its agents; the key file follows it ([OQ-BK3](#OQ-BK3)), and it may supersede the narrowing's refusal story).

---

## Summary: what the owner types

| | Before | After |
| :--- | :--- | :--- |
| Once | write `~/.config/yolo-jail/bedrock-key.toml` by hand | `yolo config add env.secrets.files '"~/.config/yolo-jail/bedrock-key.env"' --user` |
| Per launch | `yolo --user-layer ~/.config/yolo-jail/bedrock-key.toml` | `yolo -p bedrock-key` |
| Per machine or workspace | not expressible | `yolo active-provider claude bedrock-key` |

`[allow.aws-auth] profile = "<sso-profile>"` stays in the user config. Nothing in the user's
files is edited by yolo; the layer file keeps working for as long as the owner keeps it.

## Current state: the layer, and why it works

The owner's layer does two things, and both are load-bearing:

- **It loads `bedrock-key.env`**, so `AWS_BEARER_TOKEN_BEDROCK` is in the launch's environment.
  The shipped `bedrock` provider claims that name
  ([`packs/bedrock/pack.toml:6-13`](../../packs/bedrock/pack.toml)), so the credential gate
  delivers it to the agents on `bedrock` and to nothing else.
- **It sets `[allow.aws-auth] profile = ""`**, so the aws-auth service serves nothing and its
  pointer is withheld (`packload.UnconfiguredLogins`,
  [`internal/packload/served.go:157`](../../internal/packload/served.go)).

The second is not a courtesy. **A bearer delivered beside the pointer refuses the launch, with no
hatch.** `packs/aws-auth` declares the bearer a certain override of its pointer
([`packs/aws-auth/pack.toml:9`](../../packs/aws-auth/pack.toml)); the env-override pre-flight
evaluates every certain override as a refusal
([`internal/cli/run/envoverrides.go:34-37`](../../internal/cli/run/envoverrides.go)). To keep the
static credential while withholding the pointer, set only the user-scope profile with
`yolo config set allow.aws-auth.profile "" --user`; the refusal no longer suggests editing a
legacy JSONC layer or removing unrelated credentials (`LoginLoophole.EmptyProfileStep`,
[`internal/packload/served.go:195-207`](../../internal/packload/served.go)). The existing test of this
layer launches only with the profile emptied or the loophole disabled
([`internal/cli/run/bedrockkeylayer_test.go:21-29`](../../internal/cli/run/bedrockkeylayer_test.go)).

So the two guarantees the owner named are, today, one mechanism each:

| Guarantee | What holds it today |
| :--- | :--- |
| **G1** — the key reaches only launches that ask for it | the layer is the only thing that loads the env file |
| **G2** — SSO is off where the key is used | the layer's `profile = ""`, which the override pre-flight would otherwise demand |

The cost is that both live in a file that is neither the user config nor a provider: the one
place yolo's selection verbs cannot reach.

## Premises

**P1. A user-declared provider is selectable at launch, by `-p` and by `active_provider`.** The
launch resolves a selection against `config.LoadSelectableProfiles`
([`internal/cli/run/profilechannel.go:176`](../../internal/cli/run/profilechannel.go)) and refuses
an undeclared name there ([`profilechannel.go:187`](../../internal/cli/run/profilechannel.go)).
That namespace is the user's `providers` table composed with the packs': "cfg's own `providers`
table joins the namespace whole when cfg selects through `active_provider`"
([`internal/config/providersemantic_selection.go:163`](../../internal/config/providersemantic_selection.go)),
and a TOML config that declares a starting `model` or `variants` under a provider joins without
the key ([`providersemantic_selection.go:107-113`](../../internal/config/providersemantic_selection.go)).
`yolo active-provider` reads the same namespace
([`internal/cli/activeprovider.go:41`](../../internal/cli/activeprovider.go)). The help text that
says "the name must be one a selected pack declares"
([`activeprovider.go:27`](../../internal/cli/activeprovider.go),
[`config_ref.txt:115`](../../internal/cli/config_ref.txt)) is stale, not the rule
([BK-D1](#decision-ledger)).

**P2. The claims follow the platform, and a provider's own list is exact.** A provider with a
`platform` and no `api_key_env_name` co-claims every name a same-platform provider lists; one that
lists its own claims exactly that list, and "a declaration is never widened"
([`internal/packload/credentialscope.go:676`](../../internal/packload/credentialscope.go);
[PP-D9](providers-and-profiles-redesign.md#PP-D9)).

**P3. The pointer and its daemon follow the selection's platform.** aws-auth's env contribution
is gated on platform `aws-bedrock` ([`packs/aws-auth/pack.toml:13`](../../packs/aws-auth/pack.toml));
the gate fires per agent (`gateFiresFor`,
[`internal/packload/packload.go:907`](../../internal/packload/packload.go)); the jail daemon starts
only when the gate fires for some agent ([CN-D22](../reference/providers.md#CN-D22)); and the host
notch plans its worker from the same gate ([HS-D29](host-notch-services.md#HS-D29)). One rule,
every notch.

**P4. The override pre-flight reads launch-wide delivery.** A contribution counts as delivered
when its gate fires for any agent, and the overriding variable when the launch delivers it
anywhere ([`internal/packload/envoverride.go:144-162`](../../internal/packload/envoverride.go)).
It does not ask whether the two reach the same agent.

## The design

### The provider

```toml
[providers.bedrock-key]
platform = "aws-bedrock"
api_key_env_name = ["AWS_BEARER_TOKEN_BEDROCK"]
region_env_name = ["AWS_REGION", "AWS_DEFAULT_REGION"]   # as bedrock's; the region pre-flight is by platform
```

A provider on platform `aws-bedrock` so that every agent starts in Bedrock mode and the region
pre-flight applies ([BR-D1](bedrock-plumbing.md#BR-D1);
[`internal/packload/regionpreflight.go:74`](../../internal/packload/regionpreflight.go) keys the
requirement on the platform). Its own `api_key_env_name` so that it claims the bearer, the static
key pair and its session token, and nothing else ([P2](#premises), [BK-D5](#BK-D5)). **It ships in `packs/bedrock`, beside the narrowed `bedrock`**
([OQ-BK1](#decision-ledger)): the narrowing is the half nobody would guess, and shipping both halves
together is the only way they are always present together. A user-authored entry of the same shape
works too, and the shipped entry also carries `bedrock`'s `bridge` variant so claude and copilot reach
non-Anthropic models through the wire bridge with the key as `x-api-key`
([WG-I31](wire-bridge-gateway.md#WG-I31)).

### The claims, and why `bedrock` must narrow

Today `bedrock` claims six names, the bearer among them. If `bedrock-key` claims the bearer and
`bedrock` still does, the gate delivers the bearer to every agent on either provider: an SSO
launch with the key file loaded then carries the bearer beside the pointer, and **the launch is
refused** ([P4](#premises); the refusal in
[`agent-credentials.md`](../reference/agent-credentials.md#what-refuses-the-launch-and-what-only-warns)).
So the narrowing is not a tidy-up; the design does not work without it:

| Provider | Claims |
| :--- | :--- |
| `bedrock` | `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN`, `AWS_PROFILE`, `AWS_CONTAINER_CREDENTIALS_FULL_URI` |
| `bedrock-key` | `AWS_BEARER_TOKEN_BEDROCK`, `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN` |

Shipping the narrowing with the provider is what spares a user the second edit: without it, anyone
authoring `bedrock-key` would also have to override `providers.bedrock.api_key_env_name` to the five
([`credentialscope.go:672-675`](../../internal/packload/credentialscope.go)), a detail nothing would
tell them.

**Selecting `bedrock` does not select the bearer** ([OQ-BK4](#decision-ledger)). A bearer in
`[env] sources` is not used for an agent on `bedrock`: it is not in that provider's claim list,
so the gate withholds it and the disclosure names it. `bedrock` does still claim the static AWS
credential pair, the session token, `AWS_PROFILE` and the container-credentials pointer. When
`aws-auth` is configured to serve that pointer, a static pair delivered in the same launch is a
certain override: the launch refuses before the session rather than silently using the long-lived
pair instead of the narrowed, refreshed SSO credentials ([OQ-SSO8](../reference/agent-credentials.md#sso-decision-ledger)). This is the existing credential-isolation rule, not a failure of the `bedrock-key` gate. With no served pointer, the static pair remains an accepted credential under `bedrock`'s claim; `bedrock-key` is the route that accepts either the bearer or pair without claiming the SSO pointer. There is no alias from `bedrock` to `bedrock-key`, no special bearer refusal sentence and no migration path: no configs of that shape exist to migrate.

### How G1 holds: the credential gate

**The key file lives in the user config's `[env.secrets]`**, its reach decided by the claim — the same
pattern as every provider's key in [`secret-env-scoping.md`](secret-env-scoping.md)
([OQ-BK3](#OQ-BK3)). Named there, the file is read on every launch,
and the bearer is **delivered to the agents whose selection resolves to a claiming provider and to
no other process** — the gate's rule, which PP-D9 was built to keep
([`credentialscope.go:665-668`](../../internal/packload/credentialscope.go)). An SSO launch reads
the file and delivers the key nowhere; the launch's withheld-credential disclosure says so by
name, never by value. That is G1 restated at the agent, not the launch: a stronger guarantee in
one direction (two agents in one launch can differ) and a weaker one in another (the file is
opened by launches that do not use it). A launch that cannot read the file says so every time;
that is the price of dropping the flag, which is what lets `yolo active-provider` select the key.

### How G2 holds: a served pointer reaches only a provider that claims it

The aws-auth contribution *sets* `AWS_CONTAINER_CREDENTIALS_FULL_URI` and
`AWS_CONTAINER_AUTHORIZATION_TOKEN` ([`packs/aws-auth/pack.toml:16-17`](../../packs/aws-auth/pack.toml)).
The shipped `bedrock` *claims* `AWS_CONTAINER_CREDENTIALS_FULL_URI`; `bedrock-key` does not. The
rule this design adds to the platform gate, read off the declared claims and nothing else
([OQ-BK2](#decision-ledger)):

> A served env contribution whose platform gate fires for an agent is delivered to that agent
> only when the agent's provider claims at least one variable the contribution sets. Otherwise it
> is withheld for that agent, and the launch says so in the line that already reports a withheld
> pointer.

Everything else follows from mechanisms that exist:

- **No pointer** reaches an agent on `bedrock-key`, so the override pre-flight has nothing to be
  overridden and the launch proceeds ([P4](#premises) — an undelivered contribution is not
  evaluated).
- **No daemon** starts when every Bedrock agent is on `bedrock-key`, because CN-D22 starts it only
  for a gate that delivers ([P3](#premises)); the existing `Not started: the aws-auth jail daemon…`
  line reports it.
- **Every notch** gets the same answer, since the gate is the one function the jail, macos-user
  and host compositions share ([P3](#premises)).
- **The user's SSO profile stays configured.** `[allow.aws-auth] profile` is a user-scope setting
  for a machine, not for a launch, and it is never emptied.
- **`bedrock` is unchanged**, and so is a user provider that declares a platform and no list: both
  claim the pointer variable, so both still receive the pointer.

No new provider key expresses this. A `login = "none"` or `aws_auth = false` would be a second
spelling of a fact the claim list already carries, and a provider could say it while still claiming
the pointer variable, a contradiction a validator would then have to refuse. The gate reads what is
declared.

### Two providers in one workspace, never in one launch

**One launch may not mix an agent on `bedrock` with one on `bedrock-key`**
([OQ-BK5](#decision-ledger)). `-p claude=bedrock-key,codex=bedrock` is refused, as it is today, by the
launch-wide override check ([P4](#premises)): the launch delivers the bearer somewhere and the pointer
somewhere, and the check does not ask whether one agent holds both. The refusal's remedy sentence
changes, because emptying the SSO profile is the wrong advice for this launch: it says that one launch
cannot mix an agent on `bedrock` with one on `bedrock-key`, and names a launch on either alone.

**Separate launches in the same workspace may each use a different one when their credential
inputs do not conflict.** Every fresh launch composes its own environment: the gate runs anew, the
shared and per-agent env files are rewritten whole on every entry ([CN-D7](../reference/providers.md#CN-D7)),
and the aws-auth daemon is started only when some agent's selection delivers its pointer ([P3](#premises)).
A shared bearer file works for this: `bedrock-key` claims it and `bedrock` does not. A shared static
pair is different: both providers claim it, so a `bedrock` launch with an active aws-auth pointer
refuses under [BK-D5](#BK-D5) and [OQ-SSO8](../reference/agent-credentials.md#sso-decision-ledger).
That refusal preserves the isolation rule; yolo does not conditionally hide the static pair from one
provider under the current claims model. To use the SSO route in that situation, keep the pair out
of that launch's private set; to use the pair without aws-auth's pointer, `bedrock-key` provides the
route that does not claim the pointer. The current design does not scope one private file to only
one of these providers.

What this doc does **not** promise is an attach into a running jail with the other provider. An attach
composes against the daemons the running jail started at boot and never starts one
([`servedaddresses.go:41-42`](../../internal/cli/run/servedaddresses.go)), so `-p bedrock` attached to a
jail launched on `bedrock-key` alone has no pointer source and the credential pre-flight refuses the
attach for want of a credential; and `-p bedrock-key` attached to an SSO jail puts the bearer beside the
launch's pointer, which the launch-wide check judges. Whether that check runs at the attach's delivery
was not verified here; until it is, the sequence that works is the stop-and-relaunch above, and the
refusal, where one fires, should name it.

### Selection

By [P1](#premises), `-p bedrock-key`, `-p claude=bedrock-key,codex=bedrock` and
`yolo active-provider claude bedrock-key` resolve today for a declared `bedrock-key`, in every
notch. A misspelling is refused with the declared list. The only change is the two help strings
([BK-D1](#decision-ledger)).

### What happens to the `--user-layer` recipe

Nothing is taken away. A layer carrying `[env] sources` and `profile = ""` keeps launching
exactly as the existing test pins. Once `bedrock-key` exists, the layer's two lines are each
redundant: the gate carries G1 and the pointer rule carries G2. The migration is the owner
deleting a file, or not; yolo never edits it.

### Failure paths, stated

| Situation | What the person sees |
| :--- | :--- |
| `-p bedrock-key` with no key file loaded | the credential pre-flight's refusal naming `AWS_BEARER_TOKEN_BEDROCK` and the `[env] sources` it consulted ([`internal/cli/run/providerpreflight.go:56`](../../internal/cli/run/providerpreflight.go)); the value is never printed |
| key file loaded, `-p bedrock` (SSO) | with no static pair delivered, the launch runs on SSO; the bearer is withheld from every process and the disclosure names it |
| static pair and an active aws-auth pointer in the same launch | **refused** by the certain override rule ([OQ-SSO8](../reference/agent-credentials.md#sso-decision-ledger)); the static pair would be used before the narrowed pointer credentials |
| `-p claude=bedrock-key,codex=bedrock` in one launch | **refused**, as today, by the launch-wide override check ([P4](#premises)); the remedy says one launch cannot mix the two and names a launch on either alone ([two providers in one workspace](#two-providers-in-one-workspace-never-in-one-launch)) |
| `-p bedrock` with a bearer and no SSO credential | the ordinary credential pre-flight refusal, naming the five names `bedrock` claims and where it looked; the bearer is not used for `bedrock` ([the claims](#the-claims-and-why-bedrock-must-narrow)) |

## Non-goals

- No change to how SSO credentials are minted, narrowed or refreshed.
- No general redesign of providers, profiles or the claims model; the one rule added is to the
  served-contribution gate.
- No minted Bedrock API key ([OQ-SSO9](../reference/agent-credentials.md#sso-decision-ledger)
  retired it).
- No per-agent override pre-flight ([OQ-BK5](#decision-ledger)): one launch runs one Bedrock
  credential route.
- No alias and no migration path from `bedrock` with a bearer to `bedrock-key`
  ([OQ-BK4](#decision-ledger)).
- No workspace-scope provider declaration; `platform` and `api_key_env_name` stay user-scope
  ([`internal/config/schema.go:385`](../../internal/config/schema.go)).

## Alternatives considered

| Alternative | Verdict |
| :--- | :--- |
| **A distinct platform**, `platform = "aws-bedrock-key"`, so aws-auth's gate never fires | **Rejected.** Every Bedrock fact keys on the platform: the agents' Bedrock mode, the region pre-flight, the claims. A second platform value duplicates all of it to turn off one pointer |
| **A new provider key**, say `login = "none"` or `aws_auth = false` | **Rejected** ([OQ-BK2](#decision-ledger)). It states intent plainly, but it is a second way to say what the claim list already says, and could contradict it |
| **An alias**: resolve `bedrock` to `bedrock-key` when the only delivered Bedrock credential is the bearer | **Rejected** ([OQ-BK4](#decision-ledger)). A selection that means a different provider than it names; selecting `bedrock` means SSO |
| **A per-agent override pre-flight**, so one launch mixes an SSO agent and a key agent | **Rejected** ([OQ-BK5](#decision-ledger)). Separate launches in one workspace already serve the need |
| **Keep the layer as the only recipe**, document it | **Rejected.** It answers the owner's question with "no" |
| **A variant**, `bedrock/key` | **Rejected.** A variant carries a name, a via and model options; claims are a property of the provider entry, so a variant cannot claim a different list |
| **Dynamic claims**: `bedrock` drops the bearer from its list when the pointer is served | **Rejected.** A claim list that depends on another pack's state is undebuggable from the config, and `yolo check` could not predict it |

## Decision Ledger

The owner's rulings of 2026-11-09 first (BK-D6 carries [OQ-BK3](#OQ-BK3)'s), then the seat's decisions, which each have one defensible answer.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="OQ-BK1"></a>[OQ-BK1](#OQ-BK1) | **`bedrock-key` ships in `packs/bedrock`, beside the narrowed `bedrock`.** The owner: *"the narrowing is the half nobody would guess, and shipping both halves is the only way it is always present together."* | 2026-11-09 | [The provider](#the-provider) | `d95b06451` |
| <a id="OQ-BK2"></a>[OQ-BK2](#OQ-BK2) | **A served pointer reaches only an agent whose provider claims a variable it sets; the gate reads the declared claims, and no new provider key expresses it.** The owner: *"the pointer variable is already in bedrock's claim list and absent from bedrock-key's; the gate only has to read what is declared."* | 2026-11-09 | [How G2 holds](#how-g2-holds-a-served-pointer-reaches-only-a-provider-that-claims-it) | `d95b06451` |
| <a id="BK-D6"></a><a id="OQ-BK3"></a>BK-D6 ([OQ-BK3](#OQ-BK3)) | **The key file goes in the user config's `[env.secrets]`, its reach decided by the claim**, the same pattern as every provider in [`secret-env-scoping.md`](secret-env-scoping.md); `yolo -p bedrock-key` and `yolo active-provider` are the whole recipe, and no layer or flag is needed. The owner: *"obviously A. that's the point of the design, no?"* | 2026-11-09 | [How G1 holds](#how-g1-holds-the-credential-gate) | user guide recipe, with this status line |
| <a id="OQ-BK4"></a>[OQ-BK4](#OQ-BK4) | **Narrow, with no migration path from the bearer.** Selecting `bedrock` never uses `AWS_BEARER_TOKEN_BEDROCK`; the ordinary credential pre-flight stands when its claimed credentials are absent. The retained static AWS pair claim is stated separately in [BK-D5](#BK-D5), and a pair delivered beside the aws-auth pointer is refused under [OQ-SSO8](../reference/agent-credentials.md#sso-decision-ledger). No alias or special bearer refusal line is added. The owner: *"We don't need any happy path here … for migrating configs, because nobody has any configs."* May be superseded by [`secret-env-scoping.md`](secret-env-scoping.md), which the owner named as "the other design going on" | 2026-11-09 | [The claims](#the-claims-and-why-bedrock-must-narrow) | `d95b06451` |
| <a id="OQ-BK5"></a>[OQ-BK5](#OQ-BK5) | **Not in one launch; yes in one jail or workspace.** One launch may not mix an agent on `bedrock` with one on `bedrock-key` (refused as today, with the corrected remedy sentence); separate launches in the same workspace may each use a different one, by stop and relaunch. An attach with the other provider is not promised until its pre-flight placement is verified. The owner: *"in the same jail/workspace? yes, in the same launch? no."* | 2026-11-09 | [Two providers in one workspace](#two-providers-in-one-workspace-never-in-one-launch) | `d95b06451` |
| <a id="BK-D1"></a>BK-D1 | **User-declared providers are selectable, and the help says so.** The launch, `yolo active-provider` and `yolo check` already resolve a user's `providers` entry by name ([P1](#premises)). The two strings that say "one a selected pack declares" are corrected to "one a selected pack or your `providers` table declares"; no selection code changes | 2026-11-07 | [Selection](#selection) | `config_ref.txt`, with this status line |
| <a id="BK-D2"></a>BK-D2 | **`bedrock-key` is on platform `aws-bedrock`.** Every Bedrock fact keys on the platform; a second value would duplicate them to switch off one pointer | 2026-11-07 | [Alternatives](#alternatives-considered) | `d95b06451` |
| <a id="BK-D3"></a>BK-D3 | **The pointer rule ([OQ-BK2](#decision-ledger)'s form) lives in the per-agent gate every notch shares** (`gateFiresFor`), never in a notch's composition, so jail, macos-user and host give one answer | 2026-11-07 | [How G2 holds](#how-g2-holds-a-served-pointer-reaches-only-a-provider-that-claims-it) | `d95b06451` |
| <a id="BK-D4"></a>BK-D4 | **No message this design adds prints a credential value.** Refusals and disclosures name variables and files, as the existing ones do | 2026-11-07 | [Failure paths](#failure-paths-stated) | `d95b06451` |
| <a id="BK-D5"></a>BK-D5 | **`bedrock-key` also takes a static key pair.** It claims `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY` and `AWS_SESSION_TOKEN` beside the bearer, so a key file holding an IAM key pair works with `-p bedrock-key`; it still claims neither `AWS_PROFILE` nor the pointer, so aws-auth stays out of a `bedrock-key` launch and the pair does not override anything there. `bedrock` retains its claim on the pair; where that pair is delivered beside aws-auth's pointer, the existing certain-override refusal applies ([OQ-SSO8](../reference/agent-credentials.md#sso-decision-ledger)). The owner: *"why don't swe suport access/secret? I want to"* | 2026-11-10 | [The claims](#the-claims-and-why-bedrock-must-narrow) | ✅ `TestBedrockKeyReceivesAStaticKeyPair`, `TestABedrockKeyLaunchWithAKeyPairTripsNoOverride` |
| <a id="BK-D7"></a>BK-D7 | **The layer recipe is not removed or warned about.** It stays a working spelling; the owner retires it by deleting a file | 2026-11-07 | [The layer](#what-happens-to-the---user-layer-recipe) | nothing to build |

## Appendix: evidence

Verified 2026-11-07 at `2bbdd801f`.

| Claim | Where |
| :--- | :--- |
| `bedrock` claims six names, platform `aws-bedrock`, region from env or `~/.aws/config`, needs `aws-auth` | [`packs/bedrock/pack.toml:3-21`](../../packs/bedrock/pack.toml) |
| aws-auth's env contribution: platform-gated, `served_by = "aws-auth"`, sets the pointer and its token; the bearer is a certain override, the key pair unless `AWS_PROFILE`, `~/.aws` uncertain | [`packs/aws-auth/pack.toml:7-17`](../../packs/aws-auth/pack.toml) |
| the profile setting: absent, the service serves nothing | [`packs/aws-auth/loopholes/aws-auth/loophole.toml:55-59`](../../packs/aws-auth/loopholes/aws-auth/loophole.toml) |
| claims follow the platform; an own list is exact and never widened; siblings read off the composed table so a user override moves a co-claim | [`internal/packload/credentialscope.go:658-676`](../../internal/packload/credentialscope.go) |
| the platform gate fires per agent; a CLI-less pack's gated env serves whichever agent's selection satisfies it | [`internal/packload/packload.go:898-925`](../../internal/packload/packload.go) |
| a certain override refuses with no hatch; an uncertain one warns | [`internal/cli/run/envoverrides.go:22-37`](../../internal/cli/run/envoverrides.go) |
| the override check evaluates a contribution delivered to any agent against launch-wide delivery | [`internal/packload/envoverride.go:126-162`](../../internal/packload/envoverride.go) |
| the refusal's remedy for a login loophole: set only its user-scope profile with `yolo config set allow.<pack>.<setting> "" --user` | [`internal/packload/served.go:195-207`](../../internal/packload/served.go), [`envoverride.go:174-180`](../../internal/packload/envoverride.go) |
| the layer launches only with the profile emptied or the loophole disabled; the refusal never prints the value | [`internal/cli/run/bedrockkeylayer_test.go:13-29,68-80`](../../internal/cli/run/bedrockkeylayer_test.go) |
| the launch resolves `-p` and `active_provider` against the user's providers composed with the packs' | [`internal/cli/run/profilechannel.go:173-189`](../../internal/cli/run/profilechannel.go), [`internal/config/providersemantic_selection.go:148-171`](../../internal/config/providersemantic_selection.go) |
| when a TOML config joins the namespace without the `active_provider` key | [`internal/config/providersemantic_selection.go:107-145`](../../internal/config/providersemantic_selection.go) |
| `yolo active-provider` reads the same namespace; its help text says packs only | [`internal/cli/activeprovider.go:27,41`](../../internal/cli/activeprovider.go), [`internal/cli/config_ref.txt:115`](../../internal/cli/config_ref.txt) |
| the credential pre-flight consults `[env] sources` and refuses a selected provider whose key never arrived | [`internal/cli/run/providerpreflight.go:28-58`](../../internal/cli/run/providerpreflight.go) |
| the three supported credentials; "never two at once"; the hand-written API-key provider | [`bedrock-plumbing.md` §6.3–6.4](bedrock-plumbing.md#63-a-hand-written-api-key-provider-documentation-only) |
| the bearer in a dotenv is "kept as the no-daemon fallback"; the bearer beside the pointer is refused | [`agent-credentials.md`](../reference/agent-credentials.md#what-refuses-the-launch-and-what-only-warns) |

**Unverified, delegated to the planner:** whether a user-authored `aws-bedrock` provider with no
`region_env_name` of its own is read by the region pre-flight through the platform alone, or needs
`region = "<region>"`; and the exact `yolo config add` spelling for a list under `[env]`.
