---
title: "The Bedrock API key is a provider you select, not a layer you pass"
date: 2026-11-07
status: in-review
stage: DESIGN
next: "Rule OQ-BK1 and OQ-BK2 — the shipped entry and the pointer rule decide everything else"
depends-on:
  - bedrock-plumbing.md
  - providers-and-profiles-redesign.md
tags: [packs, providers, bedrock, aws, aws-auth, credentials]
summary: "A Bedrock API key becomes a provider named bedrock-key that an owner selects with -p or yolo active-provider, like every other provider. The credential gate keeps the key to the agents that select it, and a served pointer is withheld from an agent whose provider does not claim it, so SSO stays off where the key is used without a layer file or a flag."
---

# The Bedrock API key is a provider you select, not a layer you pass

**Status:** 2026-11-07. Nothing built. Evidence verified at `2bbdd801f`. The seat's design, pending
the owner's review; every ledger row is the seat's.

> **In short.** The key is already a credential a provider can claim; what it lacks is a provider
> of its own and a rule that keeps `aws-auth`'s pointer away from that provider. Both come from
> declarations the packs already make, so the layer file and its flag become unnecessary rather
> than forbidden.

**Why it matters.** Every other provider is one word on the command line. The key is a layer file
plus a flag, and the layer exists only to hold two facts the provider table could state.

**The shape.** A `bedrock-key` provider on platform `aws-bedrock` that claims exactly
`AWS_BEARER_TOKEN_BEDROCK`; the shipped `bedrock` narrowed to the five chain variables; and one
gate rule — a served env contribution reaches an agent only when its provider claims a variable
the contribution sets.

**Cost.** A user who selects `bedrock` with a bearer and no SSO profile today must select
`bedrock-key` instead; the launch tells them so. A launch mixing an SSO agent and a key agent stays
refused, as it is today.

**Start at [The design](#the-design)** — the provider, the claims, and the two guarantees.

**Needs your ruling:** [OQ-BK1](#OQ-BK1), [OQ-BK2](#OQ-BK2), [OQ-BK3](#OQ-BK3), [OQ-BK4](#OQ-BK4),
[OQ-BK5](#OQ-BK5).

**Reads with:** [`bedrock-plumbing.md`](bedrock-plumbing.md) (the three supported credentials, and
the hand-written API-key provider this replaces),
[`providers-and-profiles-redesign.md`](providers-and-profiles-redesign.md) (the claims rule, PP-D9,
and the one-selection-answers-every-gate rule, PP-D5),
[`agent-credentials.md`](../reference/agent-credentials.md) (what refuses a launch and what only
warns).

---

## Summary: what the owner types

| | Before | After |
| :--- | :--- | :--- |
| Once | write `~/.config/yolo-jail/bedrock-key.toml` by hand | `yolo config add env.sources ~/.config/yolo-jail/bedrock-key.env` |
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
([`internal/cli/run/envoverrides.go:34-37`](../../internal/cli/run/envoverrides.go)), and the
refusal's remedy is exactly the layer's second line: set the profile to `""` in the layer that
should not use it (`LoginLoophole.EmptyProfileStep`,
[`internal/packload/served.go:201`](../../internal/packload/served.go)). The existing test of this
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
requirement on the platform). Its own `api_key_env_name` so that it claims the bearer and nothing
else ([P2](#premises)). Whether it ships in `packs/bedrock` or is authored by the user is
[OQ-BK1](#OQ-BK1); the shape is the same either way, and a shipped entry also carries `bedrock`'s
`bridge` variant so claude and copilot reach non-Anthropic models through the wire bridge with the
key as `x-api-key` ([WG-I31](wire-bridge-gateway.md#WG-I31)).

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
| `bedrock-key` | `AWS_BEARER_TOKEN_BEDROCK` |

A user who authors `bedrock-key` without a shipped narrowing must also override
`providers.bedrock.api_key_env_name` to the five, which a user override of the shipped list does
([`credentialscope.go:672-675`](../../internal/packload/credentialscope.go)). That is the strongest
argument for shipping both halves together ([OQ-BK1](#OQ-BK1)). What the narrowing costs an
existing user is [OQ-BK4](#OQ-BK4).

### How G1 holds: the credential gate

With the key file named in the user config's `[env] sources`, the file is read on every launch,
and the bearer is **delivered to the agents whose selection resolves to a claiming provider and to
no other process** — the gate's rule, which PP-D9 was built to keep
([`credentialscope.go:665-668`](../../internal/packload/credentialscope.go)). An SSO launch reads
the file and delivers the key nowhere; the launch's withheld-credential disclosure says so by
name, never by value. That is G1 restated at the agent, not the launch: a stronger guarantee in
one direction (two agents in one launch can differ) and a weaker one in another (the file is
opened by launches that do not use it). Whether the owner wants the file in the user config at all,
or keeps it in a layer that now carries only the `[env]` line, is [OQ-BK3](#OQ-BK3).

### How G2 holds: a served pointer reaches only a provider that claims it

The aws-auth contribution *sets* `AWS_CONTAINER_CREDENTIALS_FULL_URI` and
`AWS_CONTAINER_AUTHORIZATION_TOKEN` ([`packs/aws-auth/pack.toml:16-17`](../../packs/aws-auth/pack.toml)).
The shipped `bedrock` *claims* `AWS_CONTAINER_CREDENTIALS_FULL_URI`; `bedrock-key` does not. The
rule this design adds to the platform gate:

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

Where this rule lives, and whether a new key should express it instead, is [OQ-BK2](#OQ-BK2).

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
| key file loaded, `-p bedrock` (SSO) | the launch runs on SSO; the bearer is withheld from every process and the disclosure names it |
| `-p claude=bedrock-key,codex=bedrock` in one launch | **refused**, as today, by the launch-wide override check ([P4](#premises)); [OQ-BK5](#OQ-BK5) |
| an existing config selecting `bedrock` with a bearer and no SSO profile | after the narrowing, the credential pre-flight refuses and names `bedrock-key`; [OQ-BK4](#OQ-BK4) |

## Non-goals

- No change to how SSO credentials are minted, narrowed or refreshed.
- No general redesign of providers, profiles or the claims model; the one rule added is to the
  served-contribution gate.
- No minted Bedrock API key ([OQ-SSO9](../reference/agent-credentials.md#sso-decision-ledger)
  retired it).
- No per-agent override pre-flight, unless [OQ-BK5](#OQ-BK5) rules it in.
- No workspace-scope provider declaration; `platform` and `api_key_env_name` stay user-scope
  ([`internal/config/schema.go:385`](../../internal/config/schema.go)).

## Alternatives considered

| Alternative | Verdict |
| :--- | :--- |
| **A distinct platform**, `platform = "aws-bedrock-key"`, so aws-auth's gate never fires | **Rejected.** Every Bedrock fact keys on the platform: the agents' Bedrock mode, the region pre-flight, the claims. A second platform value duplicates all of it to turn off one pointer |
| **A new provider key**, say `login = "none"` or `aws_auth = false` | **Not rejected; it is [OQ-BK2](#OQ-BK2) B.** It states intent plainly, but it is a second way to say what the claim list already says, and the brief's rule is to prefer an existing mechanism |
| **Keep the layer as the only recipe**, document it | **Rejected.** It answers the owner's question with "no" |
| **A variant**, `bedrock/key` | **Rejected.** A variant carries a name, a via and model options; claims are a property of the provider entry, so a variant cannot claim a different list |
| **Dynamic claims**: `bedrock` drops the bearer from its list when the pointer is served | **Rejected.** A claim list that depends on another pack's state is undebuggable from the config, and `yolo check` could not predict it |

## Open Questions

1. 💬 **OQ-BK1: Does yolo ship the `bedrock-key` provider, or does each user write it?**

   Matt has a Bedrock API key in `~/.config/yolo-jail/bedrock-key.env` and an SSO profile under
   `[allow.aws-auth]`. He wants `yolo -p bedrock-key`. Today no provider of that name exists, so
   the launch refuses the name with the declared list. He can add the four-line provider to his
   user config, but the bearer stays co-claimed by the shipped `bedrock`, so his SSO launches are
   refused the moment the key file is in `[env] sources`; he must also override `bedrock`'s claim
   list by hand, a detail nothing tells him. Two users have this shape today (the owner's dotfiles
   and the user guide's hand-written example); how many others is unsurveyed.

   - **A — Ship it in `packs/bedrock`.** `bedrock-key` and the narrowed `bedrock` land together;
     `yolo -p bedrock-key` works on any machine with the pack selected, and `yolo check` lists
     it. Cost: a second shipped Bedrock provider to explain, and one user override of
     `bedrock`'s list (if anyone made one) to re-check.
   - **B — Document the user-authored shape.** Nothing ships; the guide shows both the provider
     and the `bedrock` override. Cost: every user repeats a two-part edit, and getting half of it
     right is a refused launch whose remedy does not name the other half.

   <!-- vantage: question id=OQ-BK1 leaning="A — the narrowing is the half nobody would guess, and shipping both halves is the only way it is always present together." -->

   _Leaning:_ A — the narrowing is the half nobody would guess, and shipping both halves is the
   only way it is always present together.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-BK2: How does selecting the key provider keep the SSO pointer away?**

   Matt runs `yolo -p bedrock-key` with his SSO profile still configured. Today any provider on
   platform `aws-bedrock` receives aws-auth's pointer and starts its daemon, so the launch holds
   the bearer beside the pointer and is refused: `Refusing to launch … AWS_BEARER_TOKEN_BEDROCK
   is delivered by …; set aws-auth's profile to "" …`. Something has to withhold the pointer for
   that agent without emptying the profile he needs for SSO launches.

   - **A — A served pointer reaches only a provider that claims it.** `bedrock-key` claims the
     bearer and not `AWS_CONTAINER_CREDENTIALS_FULL_URI`, so the pointer is withheld, the daemon
     is not started, and the launch's existing "withheld" and "Not started" lines say so. Nothing
     new in the config. Cost: a rule to document ("a pointer is a credential a provider claims");
     a user provider that lists only the bearer silently loses SSO, which is what it asked for.
   - **B — A new provider key**, `login = "none"` (or `aws_auth = false`), read by the gate.
     Explicit on the page. Cost: a second spelling of a fact the claim list already carries, and
     a provider could say `login = "none"` while still claiming the pointer variable, a
     contradiction the validator must then refuse.
   - **C — A per-launch aws-auth switch**, such as `--allow aws-auth.profile=` on the command
     line. Cost: it is the layer again with a shorter spelling, and `active_provider` still
     cannot express it.
   - **D — A distinct platform value.** Rejected above; listed so the choice is complete.

   <!-- vantage: question id=OQ-BK2 leaning="A — the pointer variable is already in bedrock's claim list and absent from bedrock-key's; the gate only has to read what is declared." -->

   _Leaning:_ A — the pointer variable is already in `bedrock`'s claim list and absent from
   `bedrock-key`'s; the gate only has to read what is declared.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 **OQ-BK3: Does the key file move into the user config, or stay in a layer?**

   Matt keeps `bedrock-key.env` loaded only by his layer, so an SSO launch never opens it. With
   `bedrock-key` a provider, he can name the file once with
   `yolo config add env.sources ~/.config/yolo-jail/bedrock-key.env` and never pass the flag
   again; every launch then reads the file, and the gate delivers the key only to an agent on
   `bedrock-key`. The guarantee moves from "which launches read the file" to "which processes
   receive the value".

   - **A — In the user config.** `yolo -p bedrock-key` and `yolo active-provider` are the whole
     recipe. An SSO launch's disclosure lists the bearer as withheld by name. Cost: the file is
     opened by launches that do not use it, and a launch that cannot read it says so every time.
   - **B — Stay in a layer holding only the `[env]` line.** Launches that do not pass the layer
     never open the file. Cost: the flag stays, and `active_provider` cannot select the key
     without it, which is half of what the owner asked for.

   <!-- vantage: question id=OQ-BK3 leaning="A — the gate is the guarantee the rest of yolo relies on for every other provider's key, and the layer's remaining job is one line." -->

   _Leaning:_ A — the gate is the guarantee the rest of yolo relies on for every other provider's
   key, and the layer's remaining job is one line.

   **Answer:**
   > _(empty — fill in when decided)_

4. 💬 **OQ-BK4: What happens to a user who selects `bedrock` with a bearer today?**

   A user with no SSO profile and a bearer in `[env] sources` runs `yolo -p bedrock` today and it
   works: `bedrock` claims the bearer, nothing serves a pointer, claude sends the bearer. After
   the narrowing, `bedrock` no longer claims the bearer, the gate withholds it, and the credential
   pre-flight refuses the launch for a provider none of whose names arrived. The user guide's
   example and the "no-daemon fallback" in the credentials reference describe exactly this user;
   how many exist is unsurveyed.

   - **A — Narrow, and let the refusal name `bedrock-key`.** The pre-flight's refusal already
     lists the names a provider claims and where it looked; it gains one line: "a Bedrock API key
     is the `bedrock-key` provider: `yolo -p bedrock-key`". The user changes one word. Cost: a
     config that launched last release refuses once.
   - **B — Narrow, and alias.** When the only delivered Bedrock credential is the bearer and the
     selection is `bedrock`, resolve it to `bedrock-key` and say so. Cost: a selection that means
     a different provider than it names, and a rule the one-namespace design was built to avoid.

   <!-- vantage: question id=OQ-BK4 leaning="A — one refusal that names the one-word fix, against a silent rewrite of what the user selected." -->

   _Leaning:_ A — one refusal that names the one-word fix, against a silent rewrite of what the
   user selected.

   **Answer:**
   > _(empty — fill in when decided)_

5. 💬 **OQ-BK5: May one launch run an SSO agent and a key agent side by side?**

   Matt runs `yolo -p claude=bedrock-key,codex=bedrock`. Under this design codex gets the pointer
   and claude the bearer, and neither agent holds both. The override pre-flight still refuses,
   because it asks whether the launch delivers the bearer anywhere and the pointer anywhere, not
   whether one agent holds both. Today the same launch is refused for the same reason, so nothing
   regresses; the question is whether the design owes the per-agent check.

   - **A — Out of scope; refused as today.** The refusal is unchanged and its remedy (empty the
     profile) is wrong for this launch, so it gains one sentence: "one launch cannot mix an agent
     on `bedrock` with one on `bedrock-key`". Cost: a legitimate launch stays refused.
   - **B — Make the override pre-flight per agent.** The check asks, for each agent, whether that
     agent receives both. The mixed launch runs. Cost: `yolo check` predicts the same refusal and
     must change in step; the macos-user and host arms compose one environment per invocation and
     need the same split.

   <!-- vantage: question id=OQ-BK5 leaning="A — nobody has asked for the mixed launch, and B is a pre-flight redesign this doc should not carry; file it when someone does." -->

   _Leaning:_ A — nobody has asked for the mixed launch, and B is a pre-flight redesign this doc
   should not carry; file it when someone does.

   **Answer:**
   > _(empty — fill in when decided)_

## Decision Ledger

The seat's decisions, pending the owner's review. Each has one defensible answer.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="BK-D1"></a>BK-D1 | **User-declared providers are selectable, and the help says so.** The launch, `yolo active-provider` and `yolo check` already resolve a user's `providers` entry by name ([P1](#premises)). The two strings that say "one a selected pack declares" are corrected to "one a selected pack or your `providers` table declares"; no selection code changes | 2026-11-07 | [Selection](#selection) | — |
| <a id="BK-D2"></a>BK-D2 | **`bedrock-key` is on platform `aws-bedrock`.** Every Bedrock fact keys on the platform; a second value would duplicate them to switch off one pointer | 2026-11-07 | [Alternatives](#alternatives-considered) | — |
| <a id="BK-D3"></a>BK-D3 | **The pointer rule, whichever form [OQ-BK2](#OQ-BK2) takes, lives in the per-agent gate every notch shares** (`gateFiresFor`), never in a notch's composition, so jail, macos-user and host give one answer | 2026-11-07 | [How G2 holds](#how-g2-holds-a-served-pointer-reaches-only-a-provider-that-claims-it) | — |
| <a id="BK-D4"></a>BK-D4 | **No message this design adds prints a credential value.** Refusals and disclosures name variables and files, as the existing ones do | 2026-11-07 | [Failure paths](#failure-paths-stated) | — |
| <a id="BK-D5"></a>BK-D5 | **The layer recipe is not removed or warned about.** It stays a working spelling; the owner retires it by deleting a file | 2026-11-07 | [The layer](#what-happens-to-the---user-layer-recipe) | — |

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
| the refusal's remedy for a login loophole: profile `""` in the layer | [`internal/packload/served.go:195-207`](../../internal/packload/served.go), [`envoverride.go:174-180`](../../internal/packload/envoverride.go) |
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
