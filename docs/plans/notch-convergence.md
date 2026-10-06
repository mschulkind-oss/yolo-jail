---
title: "Notch convergence — one code path per concern, and the loopback services that assumed a boundary"
date: 2026-09-28
status: in-review
stage: DESIGN
next: "Build OQ-NC12's launch disclosure of a shadowed value (ruled 2026-10-05); the order itself lands with the parity build (B17)"
depends-on:
  - ../design/credential-sources-separation.md#OQ-ES5
  - ../research/agent-safehouse.md#OQ-AS3
  - ../design/config-ownership-and-promotion.md#OQ-CO15
tags: [plan, notches, host, parity, security, loopback]
summary: "Five audits found that the host notch, the container jail and macos-user reach one declaration through different code for flags, pack selection, providers, env order and render, and that the loopback services serving credentials are safe only while the jail has its own network namespace. The plan: one description, one code path per concern, the notch choosing only the confinement. Caller authentication comes first, because network.mode host and macos-user already share the host's loopback. Then an ordered build list: pure merges that need no ruling, and behavior changes gated on this plan's own questions and on questions other docs own."
---

# Notch convergence — one code path per concern, and the loopback services that assumed a boundary

**Status:** 2026-09-28 — owed: **one ruling, then work.** The thesis and every pure merge in the build list
are ruled by the maintainer's 2026-09-27 words ([§1](#1-the-thesis)). Caller authentication for the
loopback services is ruled the same day and comes first ([§2](#2-security-first-the-boundary-that-is-not-one)),
and is built for all five services (item 1). One question is open, [OQ-NC13](#OQ-NC13), filed 2026-09-30 beside
[OQ-NC12](#OQ-NC12), which was decided on its leaning on 2026-10-04 ([NC-D72](#NC-D72)). It gates
only item 16's second half and not the thesis or the items before it, so every other item is work owed now. They are this doc's own questions, not
parked ones, so [the tie-breaker](README.md#the-vocabulary--seven-words-and-the-word-names-what-is-owed)
does not apply: the doc owes a ruling until it is ruled. The earlier questions are all
answered, the last of them,
[OQ-NC2](#OQ-NC2), found answered on 2026-09-30 by another doc's ruling. Items 12, 18 and 28 wait on
questions other docs own, and item 31 on [OQ-NC7](#OQ-NC7)'s ruling. Each item's row in
[§4](#4-the-ordered-build-list) says when it is built. Evidence was verified against `eb0af5ee` on
2026-09-28. The measurements are the five auditors' from 2026-09-27, taken in scratch homes under
`/tmp` against a binary built from that commit.

> **In short.** A notch *(the confinement setting a launch runs at: jail, guest or host; see
> [the dial](../design/yolo-as-environment-manager.md#4-confinement-a-dial-with-three-notches))* should
> choose how the agent is confined and nothing else. Today it also chooses the flag parser, the pack
> set, the provider table, the env order and the render loop, and on some notches it removes the
> security premise a loopback service was built on.

**Why it matters.** The same `"packs": ["claude"]` gives four packs in a jail and one at the host. The
same `-p` means three different things. On a jail with `network.mode: "host"`, on every macos-user
jail, and on the host itself, a credential service that authenticates no caller sits on the host's
real loopback.

**The shape.** Each concern has one composer, and the notch is an **input** to it (it runs pack
services or it does not; its network namespace is private or shared; it is autonomous or guarded).
The notch is never a **branch** that composes a second answer.

**Cost.** Several host behaviors change to match the jail, the
[OQ-SSO8](../design/sso-backed-bedrock.md#OQ-SSO8) refusal among them. One jail behavior changes to
match a ruling the jail never honored: two packs shipping one skill name becomes fatal. WB-D4's
premise is retired.

**Start at [§2](#2-security-first-the-boundary-that-is-not-one)**, the only part that is urgent, then
[§4](#4-the-ordered-build-list).

**Needs your ruling:** none: [OQ-NC12](#OQ-NC12) and [OQ-NC13](#OQ-NC13) were ruled 2026-10-05, filed 2026-09-30 because item 16's gate, [`OQ-CN8`](../reference/providers.md#oq-cn8), is built and decided less than the item needs ([NC-D68](#NC-D68)). [OQ-NC2](#OQ-NC2), reopened for a fix without hosts-file interception, was found answered on 2026-09-30 by [OQ-CL1](../design/claude-login-without-interception.md#OQ-CL1): the credential view deletes the terminator at every notch. Also ruled 2026-09-28: [OQ-NC3](#OQ-NC3) (keep autonomy, disclosed) and [OQ-NC10](#OQ-NC10) (retire the unread variable). Ruled 2026-09-28: [OQ-NC1](#OQ-NC1) (run services at every notch), [OQ-NC6](#OQ-NC6) (credential-routing provider fields are user-scope only), [OQ-NC7](#OQ-NC7) (host claude keeps its own login for now), and by parity [OQ-NC4](#OQ-NC4), [OQ-NC5](#OQ-NC5), [OQ-NC8](#OQ-NC8), [OQ-NC9](#OQ-NC9), [OQ-NC11](#OQ-NC11).

**Reads with:** [`wire-bridge.md`](../reference/wire-bridge.md#wb-d4) (WB-D4, the premise
[§2](#2-security-first-the-boundary-that-is-not-one) retires),
[`declaration-parity.md`](../design/declaration-parity.md#1-the-principle-and-what-it-does-not-say)
(the principle this plan executes),
[`credential-sources-separation.md`](../design/credential-sources-separation.md#10-decision-ledger)
(ES-D1 to ES-D27, the host credential decisions several items revisit),
[`host-agent-environment.md`'s computed layer at the host](../reference/host-agent-environment.md#what-yolo-host-apply-renders-into-a-derived-surface) (derives at the host,
[`OQ-HC1`](../reference/host-agent-environment.md#oq-hc1) to [`OQ-HC3`](../reference/host-agent-environment.md#oq-hc3)),
[`provider-credential-scope.md`](../design/provider-credential-scope.md#7-decision-ledger)
([`OQ-CN7`](../reference/providers.md#oq-cn7) to
[`OQ-CN9`](../reference/providers.md#oq-cn9)).

---

## 1. The thesis

The maintainer, 2026-09-27, verbatim as relayed to this plan's author:

> "Are there like multiple code paths for some of this stuff where there shouldn't be? Like we should
> merge the host and jail notches like as much as possible. We're trying to make them parallel. Seems
> like parsing uh profiles is one of those places that should be the same. Um calling the jail the
> boundary here seems also just as bad for security because jails don't need to be bridge type, they
> can be house type and then um it's identical. So uh if you think this is an issue, we need to solve
> it in both places."

"Bridge type" and "house type" are `network.mode` `bridge` and `host`. A jail on `network.mode: "host"`
shares the host's loopback, so an unauthenticated loopback service inside it is exactly as exposed as
one on the host. Earlier the same day: *"host is supposed to act like everywhere else."*

I read that as four principles. Each build item in [§4](#4-the-ordered-build-list) names the one it
serves:

- **P1. One description.** The declaration (config plus packs) means the same thing at every notch.
  The notch changes how it is enforced, never what was asked for. This is
  [declaration-parity's principle](../design/declaration-parity.md#1-the-principle-and-what-it-does-not-say)
  restated for code rather than for behavior.
- **P2. One code path per concern.** Parsing a flag, closing a pack selection, composing providers,
  ordering env, rendering a surface: each has one function, and every notch calls it. **Differences
  between notches are inputs to that function, and each input is named.** Where the notch is a
  branch inside it, that is the defect this plan exists to remove.
- **P3. No service trusts its network position.** A loopback listener that hands out a credential or
  spends one authenticates its caller at every notch. A private network namespace is defense in
  depth, never the boundary ([§2](#2-security-first-the-boundary-that-is-not-one)).
- **P4. What a notch cannot do, it says.** When a notch genuinely lacks a mechanism (the host has no
  mount namespace; macos-user has no network namespace), the one composer drops that item and names
  it. It never composes around the gap in silence.

**Defined terms, all coined here:**

- A **pure merge** leaves the notches behaving identically afterwards and needs no ruling beyond
  one already made. It may change what one notch does, to match the other or to fix a defect both
  shared.
- A **behavior-changing** item changes what a user sees in a way no existing ruling decides. It is
  either *ruled* (a cited ruling decides it) or *gated* (it waits on a 💬 question).
- A **service-less notch** is one that runs none of the selected packs' `jail_daemon` or
  `kind: "service"` processes. Today those are the host and macos-user.
- A **shared network namespace** means the jail's `127.0.0.1` is the host's. That is the case for
  every macos-user jail, for `network.mode: "host"`, and for a podman-in-podman jail, which is forced
  onto `--net=host`. It is exactly the set `sharesLauncherNetns` (in
  `internal/cli/run/loopholesruntime.go`) returns true for.

### What this plan does not do

- **It does not merge what a notch cannot provide.** Mounts, `writable_home_dirs`, the image and
  `${workspace}` binding stay jail-only. The `${workspace}` pruning at the host is ruled
  ([env-manager OQ-2](environment-manager-plan.md#open-questions-to-resolve-before-their-phase)) and
  becomes an input of the one render loop, not a branch. The workspace skills layer at the host is
  [`OQ-WS5`](../reference/agent-briefings.md#oq-ws5)'s, ruled out of v1, and built after it as one
  link per `yolo host -- <agent>` ([WS-D19](../design/workspace-skills.md#WS-D19)).
- **It does not revisit [`OQ-DP3`](../design/declaration-parity.md#decision-ledger).** A config value
  of `confinement: host` keeps refusing a launch. Only the `--at host` spellings converge
  ([§4](#4-the-ordered-build-list), item 10).
- **It does not build the guest notch.** Every "one composer" here takes the notch as an input, so
  guest becomes one more value when env-manager Phase 7 lands.
- **It does not re-rule questions other docs own.** Where the right merge depends on one, the item
  is gated on that question by link: [`OQ-ES5`](../design/credential-sources-separation.md#OQ-ES5)'s
  jail half, [`OQ-AS3`](../research/agent-safehouse.md#OQ-AS3),
  [`OQ-HC1`](../reference/host-agent-environment.md#oq-hc1),
  [`OQ-HC3`](../reference/host-agent-environment.md#oq-hc3) and
  [`OQ-CO15`](../design/config-ownership-and-promotion.md#oq-co15). Two more,
  [`OQ-CN8`](../reference/providers.md#oq-cn8) and
  [`OQ-CN9`](../reference/providers.md#oq-cn9), were ruled and built on 2026-09-28:
  the second built item 17 ([NC-D67](#NC-D67)), and the first decided part of item 16, whose rest
  is [OQ-NC12](#OQ-NC12) and [OQ-NC13](#OQ-NC13) ([NC-D68](#NC-D68)). Those two ask only what it
  left open, and re-rule nothing. [OQ-NC12](#OQ-NC12) was decided on its leaning on 2026-10-04
  ([NC-D72](#NC-D72)).

## 2. Security first: the boundary that is not one

### 2.1 What trusts the network namespace today

> **Since item 1 was built, this is history.** Each of these listeners now checks the launch's caller
> token: the wire bridge since [WB-D18](../reference/wire-bridge.md#wb-d18), the other four since
> NC-D13 ([§5](#5-already-merged-and-in-flight), the per-launch caller auth row). The table records what
> each answered before that, which is why item 1 exists.

Every row below is a loopback listener yolo starts. Each one answered whoever reached it, and each
one's own code or doc said the network namespace was what protected it.

| Service | Listens on | What an unauthenticated caller gets | The stated premise |
|---|---|---|---|
| Claude OAuth terminator (`yolo-jaild oauth-terminator`, the claude pack's loophole) | `127.0.0.1:443`, fixed: `--add-host` maps `platform.claude.com` to loopback | The machine-wide Claude **access token and refresh token**. A caller can redeem the single-use refresh token and so break every jail's login | [`claude-oauth-interposition.md`](../reference/claude-oauth-interposition.md)'s warning: "The refresh endpoint authenticates nothing", covering only "any process in the jail" |
| OpenAI auth adapter, jail half (`openai-auth-adapter`) | `127.0.0.1:1460`, fixed | The ChatGPT **access and id tokens**. The broker treats any generation mismatch as a stale caller and answers with the current token, with no upstream I/O (`internal/openaiauth/broker.go`, the `DecisionStale` arm) | The adapter "holds no credential state" (code comments) |
| OpenAI auth adapter, host half (`openaiauthhost.prepare`, in-process) | `127.0.0.1:0` on the **host's** loopback, for as long as a managed codex runs | Same as above. MEASURED 2026-09-27: a caller holding no credential posted `refresh_token=yolo-broker:999999` and got HTTP 200 with both tokens | None stated. The ephemeral port was chosen against port contention, not as a secret |
| AWS credential adapter (`aws-credential-adapter`) | `127.0.0.1:1461`, fixed | Narrowed AWS **AccessKeyId, SecretAccessKey and Token** | `internal/awscredadapter/handler.go`'s package doc: "There is no authorization token, and adding one would buy nothing … the boundary is positional and the position is the network namespace" |
| Wire bridge (`yolo-jaild wire-bridge`) | `127.0.0.1:8214`, `:8215` and `:8216`, fixed | Requests spent on the user's provider key, their SigV4-signed Bedrock access, or their ChatGPT subscription (`:8215`) | [WB-D4](../reference/wire-bridge.md#wb-d4): "The jail is the boundary, and a second inbound scheme would protect the jail from itself" |

### 2.2 Why that premise is false in a jail too

A private network namespace exists only for a container jail on `network.mode: "bridge"` that is not
nested. In every other case the jail's `127.0.0.1` is the host's:

- **`network.mode: "host"`** puts the fixed ports `:1460`, `:1461` and `:8214`–`:8216` on the host's
  real loopback. The terminator's `:443` joins them on rootful podman, and on rootless podman the `:443`
  bind should instead fail (both INFERRED from source, not measured). Since
  [NC-D41](#NC-D41) the jail daemons serve there on ports each launch picks; `:443` cannot move.
- **macos-user** has no network namespace at all. The user guide's
  [settings table](../../userguide/reference/settings-per-setup.md) says: "The sandbox is on the Mac's
  own network, so `localhost` is the Mac." Today its jail daemons are declined
  (`noteMacosUserJailDaemonDeclines`). But the codex pack still points Codex at `127.0.0.1:1460`, and
  the provider composition still points claude at `:8215`, so **whatever local process of any user
  binds those ports first receives Codex's refresh request or claude's subscription bearer**.
- **A nested jail** is forced onto `--net=host`, so it joins the outer jail's namespace. There, the
  outer jail's `:443`, `:1460` and `:1461` are already held (MEASURED 2026-09-27 from
  `/proc/net/tcp` in a bridged jail). Since [NC-D41](#NC-D41) a nested launch picks its own ports
  instead (MEASURED 2026-09-28: two concurrent nested bridged jails, each served on its own ports).
- **Every bridged rootless jail reaches the host's loopback** through
  `--network=pasta:--map-host-loopback,…` (`decideHostLoopback`), which forwards every port. That is
  deliberate and documented: it is how a jail reaches a host service at `host.containers.internal`.
  It also means every host-loopback listener above is reachable from every sibling jail, including
  a jail that never selected the pack. [`loophole-transport.md`](../reference/loophole-transport.md)
  already names that adversary: "The adversary is a SIBLING JAIL".

So "the jail is the boundary" holds for one configuration out of five, and even there only for the
jail's own services. **Ruled 2026-09-27: solve it in both places.**

### 2.3 The fix: every service authenticates its caller, at every notch

**Decided (the maintainer's ruling; the mechanisms are implementation decisions, recorded as
NC-D3).** Each launch mints a secret *(a per-launch caller secret, coined here: 256 random bits,
generated once per launch, held in a `0600` file and in the selecting agent's own environment,
never in the shared env file)*. Each service requires that secret on every request. Each client
carries it in a slot it already has, so no agent changes:

| Service | Where the client carries the secret | What the service checks |
|---|---|---|
| Wire bridge | The credential slot each bridged derive already fills: `ANTHROPIC_AUTH_TOKEN`, `COPILOT_PROVIDER_API_KEY`, `apiKey` for pi, oh-omp and opencode, and `env_key` for codex | A constant-time compare of the inbound `Authorization` or `x-api-key`. **Built** (`ea083e97`, [WB-D18](../reference/wire-bridge.md#wb-d18)). It amends [WB-D4](../reference/wire-bridge.md#wb-d4) and also closes claude-on-`codex` sending its subscription bearer to `:8215` with no token set |
| OpenAI auth adapter (both halves) | The refresh marker the auth.json writer already mints, extended from `yolo-broker:<generation>` to `yolo-broker:<generation>.<token>` ([NC-D13](#NC-D13)). Both notches write Codex's auth.json themselves (`openauthclient.WriteCodexAuth`, `prepareCodexHome`), and Codex sends that refresh token back unchanged | `Handler` refuses a marker without this launch's token 401 before calling the broker (at the host, the token the managed Codex home's live launches share, [NC-D18](#NC-D18)), so the stale-caller arm can no longer answer a stranger. **Built** (`a5fd280f`) |
| AWS credential adapter | `AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE`, pointing at the `0600` in-jail file the boot writes, `/run/yolo/caller-tokens/YOLO_SERVICE_AWS_AUTH_TOKEN` ([NC-D14](#NC-D14)). The AWS SDKs' container-credentials provider reads it and sends its contents as `Authorization`; which agents' SDKs read the `_FILE` form is UNMEASURED per agent (none may be started in a test) | A constant-time compare of `Authorization`. This is [`OQ-CN7`](../reference/providers.md#oq-cn7)'s option (c), extended from aws-auth to every notch. **Built** (`a5fd280f`). Since `7d0af878` the token is scoped rather than filed: `AWS_CONTAINER_AUTHORIZATION_TOKEN` in the env file of each agent on `bedrock` only, and the boot writes no file for it ([CN-D23](../design/provider-credential-scope.md#CN-D23)) |
| Claude OAuth terminator | Nothing new. Claude already presents the refresh token it re-reads from `~/.claude/.credentials.json`, which is a symlink to the shared file, just before the POST | The terminator hands the broker that token; the broker answers only when it is the shared file's current refresh token, or one of the last four it replaced (the refresh race), and refuses with `401 caller_unauthenticated` otherwise ([NC-D15](#NC-D15)). **Built** (`a5fd280f`). The proxy branch (Claude's `/login` exchange) carries no secret and stays unauthenticated; see [OQ-NC2](#OQ-NC2) |

Rules that apply to all four:

- **Never logged, never in the shared env file, never in a briefing.** The secret reaches the
  selecting agent only, through the vehicle that notch uses (the per-agent file, the exec env, or
  `launchEnv`).
- **Failure is a refusal, not a fallback.** A service that cannot read its secret file does not
  start. A request with a missing or wrong secret gets a 401 in the protocol's own error shape, with
  a body naming yolo. It never gets a 5xx, which a client would retry.
- **Concurrency.** The secret is per launch and fixed for that launch's lifetime. Two launches never
  share one. An attach reuses the running jail's secret, because an attach never re-stages.
- **Done when:** at every notch and for each row, a request without the secret is refused and a
  request from the selecting agent succeeds. That holds from a host shell next to a
  `network.mode: "host"` jail, and from a sibling bridged jail through `host.containers.internal`.

> [!WARNING]
> **"A token in the environment buys nothing, since everything that can read it can reach the port"**
> (the awscredadapter package doc) is true only inside a private namespace. Once the namespace is
> shared, host processes can reach the port but cannot read the jail agent's environment, and the
> token is the whole difference. Do not re-derive the "no token" argument from that comment. It is
> retired with this item.

### 2.4 The addresses those secrets protect are composed, not literal

The second half of the fix is [§4](#4-the-ordered-build-list) item 2. Two services' addresses are
written as literal pack env:

- `CODEX_REFRESH_TOKEN_URL_OVERRIDE=http://127.0.0.1:1460/oauth/token` in `packs/codex/pack.json`.
- `AWS_CONTAINER_CREDENTIALS_FULL_URI=http://127.0.0.1:1461/credentials` in `packs/aws-auth/pack.json`.

So they are delivered at notches where nothing serves them. MEASURED 2026-09-27:
`yolo host env --agent claude -p bedrock` exported both, with rc 0 and no warning. Item 2 makes each
address a fact of the daemon that serves it. Where the client accepts a composed address, the daemon
listens on an ephemeral port. That ends the `network.mode: "host"` and nested port collisions. An
address nothing serves at this notch is dropped and named (P4). That generalizes ES-D18, which today
covers only `adapter` addresses, to every jail daemon.

**Built 2026-09-28** (`8805439f`, [NC-D16](#NC-D16);
`TestAListenPointerComposesItsDaemonsServedAddress` and
`TestASharedNamespaceLaunchMovesEveryServedAddressTogether`, [NC-D41](#NC-D41) to
[NC-D44](#NC-D44)). The pointer's contribution names the daemon that serves it (`served_by`),
and the credential gate withholds it and says so wherever that daemon does not run. The address
is no longer a literal in the pack. The daemon declares it once, as `jail_daemon.listen`, and its
argv and the pointer both spell it `{listen}`. On a shared network namespace the launcher picks a
free port for every declared address, the wire bridge's included, and composes it into the argv,
the pointer, the provider table and the via base. It holds each port until the process that serves
it has it, so no listener of its own can be given the port first ([NC-D69](#NC-D69)). A private
namespace keeps every declared port.
The Claude OAuth terminator's `:443` is the one address that cannot move, because `--add-host`
maps a hostname to it ([OQ-NC2](#OQ-NC2)).

## 3. The inventory

The five audits of 2026-09-27 (flags, packs, providers, render and services) found overlapping
rows, and this table merges them. **Merge** is the target. **Ruling** says whether the item needs one:
"—" means none, and a link names the gate. Function names were checked against `eb0af5ee`. File and
line numbers are left out on purpose, because they drift.

### 3.1 Flags and the profile table

| # | Concern | Jail | Host | macos-user | Divergence | Merge | Ruling |
|---|---|---|---|---|---|---|---|
| A1 | `-p` grammar | `parseRunArgs` → `applyProfileValue`: a NAME, or `cli=name,…` | `parseHostExecFlags`, plus a second loop in `hostEnv`: a bare NAME only | as jail | `yolo host -p claude=zai -- claude` refuses a profile named `claude=zai` (MEASURED) | One grammar for every front door | — (✅ **built**, ES-D27, [§5](#5-already-merged-and-in-flight)) |
| A2 | Missing, empty and glued values; unknown flags | `-p -- x` reads the injected `run` token as the profile. `--profile=` **executes `--profile=`** in a real jail (MEASURED, rc 127). `-p=` is accepted | "needs a value". `--profile=` is silently empty. `-p=zai` is refused with the full usage | as jail | The same typo gets opposite outcomes | One value-flag reader for `-p`, `--profile`, `--at`, `--network` and `--with-credentials`. A missing or empty value gives "`<flag>` needs a value", rc 2. `-p=` is accepted everywhere. `refuseUnknownFlags`' wording everywhere | — |
| A3 | `--at host` spellings | `parseRunArgs` → `refuseUnbuiltNotch` | `RewriteArgv`/`stripHostNotch` routes `yolo --at host … --` only | as jail | `yolo --at host -- c` runs. `yolo run --at host -- c` and a bare `yolo --at host` refuse. `yolo host --at host` gives rc 2 | The front door decides the notch once, wherever `--at` sits. `yolo host` takes `--at host` as a no-op. A run flag with no host meaning gets a named refusal | — ([OQ-2](../reference/host-agent-environment.md#oq-2) rules the alias; `confinement: host` stays refused by [`OQ-DP3`](../design/declaration-parity.md#decision-ledger)) |
| A4 | Config-change approval | the `--accept-config-changes` flag | `YOLO_ACCEPT_CONFIG_CHANGES` only; the explicit `yolo host [flags] --` refuses the flag | as jail | One approval, two spellings, each refused at the other notch | Accept the flag on the explicit host spelling. The env var stays for the wrapper path only | [OQ-NC10](#OQ-NC10): found while building item 10, no code reads the env var, so the host has nothing for the flag to approve. ✅ Ruled A, which replaces this row's merge: the variable is retired and the flag stays refused by name, built `a1812373` ([NC-D64](#NC-D64)) |
| A5 | `--with-credentials` | refused (`refuseHostOnlyFlags`) | `addGrantValue` → `resolveHostGrant` → `ScopeInput.Grants` | refused | Host only | The jail channel passes `Grants`, keyed by the launched basename | [`OQ-ES5`](../design/credential-sources-separation.md#OQ-ES5)'s jail half, ✅ ruled and built 2026-10-05, though not as this row proposed: the jail holds the grant for its life, every process and later session included, outside the gate's per-agent deliveries, and an attach asking for more is refused ([ES-D31 to ES-D36](../design/credential-sources-separation.md#10-decision-ledger)) |
| A6 | Who a profile reaches | `effectiveUseProfiles`: a bare name keys every installed agent CLI and **never** the `--` command. Per-agent env files | `composeHostLaunch` keys the basename of `cmd[0]`, whatever it is (ES-D1) | the jail's table, delivered per launch by basename (`launchEnv`) | `-p zai -- bash` hands bash `ZAI_API_KEY` at the host and nothing in a jail | One table builder. Delivery narrows the table to the launched process | [OQ-NC5](#OQ-NC5), ✅ built `b2ca409a`: a bare `-p` reaches agent CLIs only at the host too, and `-p zai -- bash` is refused, naming `--with-credentials` ([NC-D62](#NC-D62)) |
| A7 | Profile disclosure; env-override refusal | `noteUseProfiles`, and `checkEnvOverrides` on all three jail arms | neither (no `EnvOverride*` call in `internal/cli/host*.go`) | as jail | MEASURED: the host ran claude with a bearer, a static pair and the aws pointer together, which a jail refuses | The host calls `packload.EnvOverrideRefusal`/`EnvOverrideFindings` over its own lookup and prints the same profile line. Since 2026-09-29 that line says, for each agent keyed to the name, the provider it resolved to and how the agent reaches it at the notch, with a warning where it reaches nothing or delivers no credential ([NC-D36](#NC-D36)) | — ([OQ-SSO8](../design/sso-backed-bedrock.md#OQ-SSO8) is not scoped to the jail) |
| A8 | Config validation | `ValidateConfig` before every launch | none, apart from `config.UnknownUseProfileKey` | as jail | MEASURED: the host composes a provider written with removed keys and sends claude to first-party with model `m1` | The host runs the provider and profile section of validation over user scope | — |

#### Row A4: what the host's approval approves

Moved here verbatim from [OQ-NC10](#OQ-NC10), where its answer is. Row A4
says the host takes the approval as `YOLO_ACCEPT_CONFIG_CHANGES` and refuses the flag, so item 10
was to accept the flag on the explicit `yolo host [flags] --` spelling. Building it found the
premise stale. No code reads `YOLO_ACCEPT_CONFIG_CHANGES`: its last reader went with the
zero-prompt auto-apply (`8629bfd8`, 2026-09-18), which re-renders a stale home without asking.
The one question the host launch gate still asks is the first-apply loss of undeclared MCP
servers, and off a terminal it refuses without consulting the variable. So accepting the flag
today would be a flag that does nothing, and `yolo host --help`, [host-apply-staleness
OQ-HS10](../reference/host-apply-staleness.md#why-its-this-way) and `config-ref` describe a
grant nothing honors. Until this is ruled, the flag keeps the refusal it always had, now worded
as a jail-launch flag ([NC-D20](#NC-D20)). This decides row A4.

#### Row A6: who a bare `-p` reaches

Moved here verbatim from [OQ-NC5](#OQ-NC5), where its answer is. In a jail it reaches every
installed agent CLI and never the `--` command ([pv-oq-5](../reference/providers.md#pv-oq-5)). At
the host it reaches whatever command runs, `bash` included (ES-D1, an implementation decision).
This decides item 11 and the host help's `yolo host -p zai -- curl` example.

### 3.2 Packs

| # | Concern | Jail | Host | macos-user | Divergence | Merge | Ruling |
|---|---|---|---|---|---|---|---|
| B1 | Selection closure (`needs`, then `via`) | `stagePacksInto` → `launchSelection` → `Selection.Close`. Also check, validation and promote | none in `loadedHostPacks`, `footerHostPacks`, `configuredPacksForInspection`, host apply, capture, revert or check-deps | as jail | `["claude"]` gives claude, aws-auth, openai-auth and wire-bridge in a jail, and claude alone at the host | One selection function every notch and verb calls. It returns packs, unresolved entries and cause lines | — (but after item 2: ES-D24 measured the aws pointer leaking otherwise) |
| B2 | Precedence order (later wins) | The launcher puts embedded packs first, then configured, then closure. The boot's `LoadJailPacks` reads with `os.ReadDir`, which is alphabetical | config order | as jail | MEASURED: different `PULSE_SERVER` winners at the launcher, the boot and the host | Compute the order once at selection, record it in `_pack-tree.json`, and have every reader use it | [OQ-NC4](#OQ-NC4), ruled A. ✅ Built `ea5b12b0` ([NC-D57](#NC-D57) to [NC-D59](#NC-D59)) |
| B3 | Filters and the no-escape rule | `packstage.Stage` on every configured pack | `loadAsStaged` stages only a fetched or filtered pack, then points `Root` back at the source | as jail | MEASURED: an `exclude`d skill is delivered at the host, and an escaping symlink is rendered by host apply | The host stages every configured pack through `packstage.Stage` for the verb's lifetime | — |
| B4 | Malformed or unresolvable entry | `ValidateConfig` and `stagePacksInto` refuse | `LoadPacks(nil)` drops it silently; unresolvable packs are warned and composed without | as jail | MEASURED: a typo refuses the jail and schedules the pack's host output for retirement | `LoadPacks` returns its problems. Launch-shaped verbs refuse and name the pack; read-only verbs report | — ("host is supposed to act like everywhere else", NC-D5) |
| B5 | A program's `platforms` | `launcherUnpublished` declines, with a line | `DepRequirements` ignores it, so host apply offers a vendor install that does not exist | as jail | MEASURED | One packload predicate for installable programs per goos/goarch | — |
| B6 | Eight resolvers | `stagePacksInto`, check, `resolveSelectedPacks`, `resolveConfiguredPacks`, `UseProfileCLINames` and `LoadJailPacks` | `resolveConfiguredPack`, `footerHostPacks` | jail's | Each answers "which packs, from which tree" slightly differently | One resolver with two modes (stage, or read the declaration only). Filters always apply | — |
| B7 | Embedded packs | `MaterializeEmbedded` into scratch per launch; any problem is fatal | the `Embedded()` lease answers empty on a problem, which reads as "ships no pack by that name" | as jail | A yolo bug refuses a jail and reads as a typo at the host | `stagePacksInto` reads `Embedded()`. Both check `EmbeddedProblems` | — |

#### Row B3: a local pack's escaping symlinks

Moved here verbatim from [OQ-NC9](#OQ-NC9), where its answer is.
Found while building item 5. The launch stages every configured pack through `packstage.Stage`,
whose no-escape rule refuses such a link whatever the pack's origin. The host read a local pack
in place, so it followed the link and delivered its target as content. A dotfile manager (rcm,
stow, chezmoi) deploys exactly that shape: `packs/mine/skills/x` is a link into the dotfiles
repo. It is the shape [`host-apply-staleness.md`](../reference/host-apply-staleness.md) calls
the one a user's own local pack most often has, and `TestApplyHostConvergesOverASymlinkedPack`
pins it at the host. So "an escaping symlink refuses both" would break a shipped host
behavior. Today the launch refuses such a pack and the host follows it. Item 5 kept both as
they were: `config.ResolvePackSpec.FollowLocalSymlinks` is set by the callers that read an
unfiltered local pack in place before there was one resolver (the host verbs, the footer,
config validation, `UseProfileCLINames` and the lazy loophole resolver). It never reaches a
filtered entry, which every notch staged and refused over such a link, nor a fetched or
embedded pack. This question decides that input.

### 3.3 Providers, credentials and env

| # | Concern | Jail | Host | macos-user | Divergence | Merge | Ruling |
|---|---|---|---|---|---|---|---|
| C1 | Provider composition | `run.composedProviders`, with every adapter | `composedHostProviders`: `WithoutServiceAdaptations`, then `ViaInert` | `run.composedProviders`: it composes `:8214` to `:8216`, which nothing serves | For one declaration: a container works, the host refuses an adapter but silently drops a `via`, and macos-user launches against dead addresses | One composition helper that takes "this notch runs pack services" as an input. `yolo check` asks it for the configured runtime | — for the helper; [OQ-NC1](#OQ-NC1) for the value at a service-less notch. ✅ Both named options are gone (item 2), and a launch-owned service's addresses compose through the same helper (item 3, [NC-D65](#NC-D65)) |
| C2 | Literal jail-daemon addresses in pack env | served | delivered, and served by nothing (MEASURED) | delivered; the daemons are declined | Dead pointers, and credential injection by whoever binds the port first | The address becomes a fact of the serving daemon, composed per notch. Unserved means dropped and named | — ([§2.4](#24-the-addresses-those-secrets-protect-are-composed-not-literal)) |
| C3 | Env precedence | `agentEnvFileContent`: since [`OQ-CN8`](../reference/providers.md#oq-cn8) every line defers to the launcher's incoming environment (CN-D21), so the user's value wins, then the file's first line: `env_sources`, then the gated env, then the shape (re-read 2026-09-30) | `composeHostVarsGranting`: fold, then `env_sources`, then shape, then removals, all applied over the shell | `launchEnv`: `env_sources` last; tombstones skipped. Since item 17, every other agent reads the per-agent file | One key got three winners (MEASURED 2026-09-30 and 2026-10-04, [NC-D72](#NC-D72)) | One ordered composition in packload, which each vehicle serializes. ✅ Built ([NC-D72](#NC-D72)): `CredentialScope.EnvFor`, read by the host exec, both container files, `launchEnv`, `DeliveredTo`, `deliverySource` and `yolo check` | [`OQ-CN8`](../reference/providers.md#oq-cn8), ✅ built `cef51809`, decided the user's value in the per-agent file only. [OQ-NC12](#OQ-NC12) ✅ decided on its leaning A and built ([NC-D72](#NC-D72)). [OQ-NC13](#OQ-NC13) stays open: the host still composes over the shell |
| C4 | Delivery vehicle | per-agent files | one exec | per launch, by basename, and since item 17 the per-agent files too | An agent started from a macos-user shell gets none of its profile. ✅ Closed by item 17 | macos-user writes the per-agent files | [`OQ-CN9`](../reference/providers.md#oq-cn9), ✅ ruled and built `949d9430` (CN-D24; [NC-D67](#NC-D67)) |
| C5 | Workspace scope of credential inputs | `env_sources` and `providers.*.api_key_env_name` merge in from the workspace | user scope only | as jail | A repo file can make `GH_TOKEN` zai-claimed and send it to z.ai | One scope reader for credential-bearing keys | [`OQ-AS3`](../research/agent-safehouse.md#OQ-AS3), [OQ-NC6](#OQ-NC6). ✅ The provider half built `c7a3482b`: every credential-routing provider field is refused at workspace scope ([NC-D63](#NC-D63)). `env_sources` waits on [`OQ-AS3`](../research/agent-safehouse.md#OQ-AS3) |
| C6 | OpenAI prelaunch | declarative: `YOLO_AUTH_PRELAUNCH_<BIN>_*` read by the `agentAuthPrelaunchShellFn` launcher block, with no login without a TTY | `openaiauthhost.prepare` switches on the names `codex` and `pi`, and logs in regardless of profile or TTY | shim as jail | MEASURED: `yolo host -p zai -- pi </dev/null` reached the broker's browser login | The host reads the same declarative values from its composition. One Go prelaunch, logging in only at a TTY. The managed `CODEX_HOME` is keyed on the declaring pack | — ([OQ-OA3](../reference/agent-credentials.md#oq-oa3) covers codex's managed home only) |
| C7 | Refusal and disclosure renderers | `checkProviderCredentials` has its own verdict line; `Disclosure()` gives no remedies | `credentialScopeLines` → `DisclosureWith(notes)`, and no verdict line | as jail | One refusal, two wordings | Both move next to `ProviderCredentialGaps` in packload. The jail passes `DisclosureNotes` | — |
| C8 | Claude OAuth | brokered through the terminator | the host claude's own login; with `YOLO_CLAUDE_CREDENTIAL_VIEW=1`, the machine's login through a view in a store yolo manages ([CL-D27](../design/claude-login-without-interception.md#CL-D27)) | the singleton is ensured, and no terminator runs | Two login lineages for Claude by default, one for OpenAI | — | [OQ-NC7](#OQ-NC7) |

#### Row C3: which of yolo's own sources wins

Moved here verbatim from [OQ-NC12](#OQ-NC12), decided on its leaning A on 2026-10-04 and built ([NC-D72](#NC-D72)); the table below is the 2026-09-30 measurement, kept as the record.
Three sources can set the same name for one agent: the pack env fold, an `env_sources` value
the agent's provider claims, and the agent's shape variables (what its pack's env derive
emits; [the terms](../reference/providers.md#the-credential-gate)). The fold has
one winner inside itself at every vehicle (`packload.EnvFold`, pinned by
`hostfoldparity_test.go`). Between the three, the vehicles disagree. MEASURED 2026-09-30 by a
scratch test over one fixture pack, which gates `K1` on a profile, has its derive emit `K1`
and the claimed `ZAI_API_KEY`, and hydrates `ZAI_API_KEY` from `env_sources`. Each cell names
the one that wins:

| Vehicle | Gated env against the shape var | Claimed `env_sources` against the shape var | The user's value against yolo's |
| :--- | :--- | :--- | :--- |
| Per-agent file (container backends, and macos-user since item 17) | gated env | `env_sources` | the user's ([`OQ-CN8`](../reference/providers.md#oq-cn8)) |
| macos-user session env (the program `yolo -- <cmd>` starts) | shape var | `env_sources` | does not arise: the invoking shell's variables do not cross (`env -i` with a closed list) |
| Host exec (`yolo host --`) | shape var | shape var | yolo's ([OQ-NC13](#OQ-NC13)) |

- **The per-agent file's order is new.** Before CN-D21 its composed lines were plain-form
  (CN-D9), so the shape var won both columns there, as it does at the host. CN-D21 made every
  line defer to a value already set, so the file's first line now wins. CN-D21's ledger row
  records that as a consequence found on 2026-09-30.
- **macos-user gives two answers.** The program a launch starts gets the session env and then
  sources its own file, whose lines keep what the session set, so `yolo -- claude` gets the
  shape var's `K1` and a claude started from the login shell gets the gated env's. INFERRED
  from the two measured vehicles and the file's grammar, not run through a sandbox launcher.
- **Shipped packs do not reach the first two columns alone.** No shipped derive assigns a name
  a shipped provider claims or a shipped pack gates (a grep of `packs/*/derive.lua` for
  literal assignments, 2026-09-30, which misses a name a derive computes). A user's own pack
  or provider does.

No ruling picks this order. [`OQ-CN8`](../reference/providers.md#oq-cn8) decided
the user's value against yolo's, and [pv-oq-8](../reference/providers.md#pv-oq-8) a pack's
gated env against its own static env. This decides the first half of item 16.

- **A — The shape var, then `env_sources`, then the fold.** The most specific source wins: the
  derive composes for this agent's selected profile, `env_sources` is the user's standing file
  for every process, and the fold is each pack's default. It is the host's order, and the
  per-agent file's before CN-D21.
- **B — `env_sources`, then the shape var, then the fold.** A dotenv entry counts as the user's
  own value and beats the profile, as the macos-user session env's comment already argues
  ("a user's own dotenv entry still beats every channel value"). Cost: a dotenv line naming a
  variable the profile sets, such as `ANTHROPIC_BASE_URL`, defeats the profile with no notice.
- **C — The per-agent file's order today:** `env_sources`, then the fold, then the shape var,
  with the other two vehicles moved to it. It is what a jail does, but only as a side effect
  of CN-D21, and it lets a pack's default beat the profile.

Under each option a removal ranks with its source, except an `env_sources` null. That is the
only removal a user writes, and it keeps beating every assignment, as it does at the host.
*Changed when A was built ([NC-D72](#NC-D72)):* a null ranks with `env_sources` too. It removes
a pack's value and, at the host, the shell's, and never a shape var, since a null that removed a
derive's address left the agent the credential paired with it (claude kept zai's token and lost
zai's base URL).

#### Row C3: the host and the user's shell

Moved here verbatim from [OQ-NC13](#OQ-NC13), which is open. [`OQ-CN8`](../reference/providers.md#oq-cn8) was asked
about, and built for, the per-agent file. The host exec applies its composition over the
shell it inherits ([the execution flow](../reference/host-agent-environment.md#execution-flow),
step 3). So for claude on a profile whose derive sets `ANTHROPIC_MODEL`, an
`export ANTHROPIC_MODEL=x` keeps x for claude in a jail and loses it to the profile under
`yolo host -- claude`. `TestComposeHostEnvOrdering` pins a profile's `AWS_REGION` over the
shell's, and the scratch test above measured a shape var over an exported value. The jail tells
a value yolo left from the user's own by comparing it with what yolo wrote or froze into the
container this entry (CN-D21). The host writes no file to compare with, so a value an earlier
`eval "$(yolo host env)"` exported looks exactly like one the user typed. This decides the
second half of item 16.

Since [NC-D72](#NC-D72), the composition this half applies to is one entry per name, and the host
honors the shell through one named input (`hostHonorsIncomingValue`, `false`): B or C is that
input flipped, plus, for B, the record. Flipped, it leaves a value the shell holds in place of
yolo's assignment and of a derive's tombstone, as the per-agent file does for a value yolo did not
set, and keeps an `env_sources` null, the user's own removal. **A cost B and C share, found
building NC-D72:** a value the process already holds splits a derive's ADDRESS from its
CREDENTIAL. For claude on zai, a held `ANTHROPIC_BASE_URL` is kept while the derive still hands
claude zai's key as `ANTHROPIC_AUTH_TOKEN`, so the key goes to that address. The jail already has
this split for a value set inside it (an in-jail `export`, a per-command value, a workspace `.env`
that mise loads), by [`OQ-CN8`](../reference/providers.md#oq-cn8)'s rule: the per-agent file's
lines for the address and the token each defer to a value already set. What B or C adds at the
host is exposure to values the user may not know the shell holds: a dotfile setting the name for
another tool, or an `eval` from a launch on another profile. Under B the record covers only a
value an earlier `eval` exported, never a dotfile's; under C nothing does. NC-D72's null rank
closed the same split from the other side (a null no longer removes the derive's address and
leaves its key). A pairing-aware variant (keep the shell's value unless the derive composed a
credential beside it) would be a fourth option, and would apply to the jail's rule as much as to
the host's.

- **A — Keep composing over the shell, and say so.** A notch difference with its reason
  stated (P4): the host cannot tell a value an earlier `eval` left from one the user typed.
  The providers reference and the user guide name it.
- **B — The shell's value wins, and `yolo host env` records what it exported.** The script
  also exports a record of the names and values it set. A later host launch treats a value
  matching the record as yolo's and replaces it, as a jail replaces its shared file's value.
  That is the jail's rule whole, including its "a stale inherited value cannot win" half.
- **C — The shell's value wins, with no record.** A value from an old `eval` then beats a
  changed profile until the user runs the `eval` again.

### 3.4 Render

| # | Concern | Jail | Host | macos-user | Divergence | Merge | Ruling |
|---|---|---|---|---|---|---|---|
| D1 | Profile selection in render | one table feeds `packoverlay.Collect` and `surfaceSelectionFor` | overlays come from `use_profiles` (`overlayGateProfiles`), derives get no selection (`RenderHostPack`'s "NO profile table"), and the launch gate ignores `-p` | as jail, minus D2 | MEASURED: host apply wrote `CLAUDE_CODE_USE_BEDROCK` while reporting that `profile` does not apply at the host | Resolve the host table once per invocation and feed overlays, derive selection and env from it | [`OQ-HC3`](../reference/host-agent-environment.md#oq-hc3) |
| D2 | macos-user wire tables | all three tables are read at boot | — | `BuildRunPlan` and `PlanInvariants` relay `YOLO_PROVIDERS` and `YOLO_USE_PROFILES` and **drop `YOLO_PROFILES`** | MEASURED by a scratch test: codex's `model_provider` renders nil | One exported list of wire tables, ranged over in both places | — |
| D3 | The render loop | `ConfigurePackSurfaces` → `renderDeclaredSurface`, and `configureOnePack` for check | `RenderHostPack`; `configRender` for previews | as jail | Four loops that agree only by inspection | One loop that asks `Modes().Mechanism` per surface. Per-target facts are inputs | — |
| D4 | Derive content at the host | `deriveComputedLayer` over live tables | `hostTableKeys` over sentinel tables only | as jail | Host pi gets `models.json` `{}`, and no MCP or LSP entry reaches a host agent | Same derive, inputs composed at user scope | [`OQ-HC1`](../reference/host-agent-environment.md#oq-hc1) |
| D5 | `config render --at host` | — | `surfaceManifest()` shows the autonomous posture | — | MEASURED: the preview shows `additionalDirectories ["/"]`, while host apply writes `[]` | `--at host` previews `RenderHostPack` in observe mode | — |
| D6 | Skills | `copySkillSubdirs`: flat, last wins silently | `ComposeHostSkills` + `Collisions`: fatal collisions, `skills_tier` honored | as jail | MEASURED: `/dup` is packb's in a jail, and it is a fatal collision at the host | One composer (the hostskills plan plus `Collisions`) at both notches. Built-ins, the workspace layer and the LSP plugin become explicit per-notch layers | — ([the S1 collision ruling](../reference/pack-system.md#skills-collision), maintainer 2026-08-05; [§6a-2](../reference/pack-system.md#batch-6a)) |
| D7 | Briefing | `BriefingContent` + `agents_md_extra` + `ComposePackBriefings` | `ComposeHostBriefings`: pack prose only. `BriefingContent`'s host branch has no production caller | as jail | MEASURED: `agents_md_extra` never reaches a host agent | One per-destination composer with a notch-aware base body | — ([§6a](../reference/pack-system.md#batch-6a); env-manager Phase 8's done-when) |
| D8 | `files` kind | `packFilesTargets` → a `:ro` bind; addressed files resolved by its own `aliasByAgent` | `RenderHostFiles` over `ResolveDestinations` | **absent, silently** (`preparePackFiles` gated off) | pi's extensions never reach macos-user | The macos overlay carries files; `ResolveDestinations` everywhere | — |
| D9 | Posture launch flags | `InjectLaunchFlags` → `launchFlagClaims(packs, true)`, hardcoded | never called | as jail | A `guarded.launch` entry reaches no notch | `InjectLaunchFlags` takes the target profile's autonomy bit | — ([§6c](../reference/pack-system.md#batch-6c)) |
| D10 | macos-user boot steps | the `boot.go` step list | — | `RunDarwinBootstrap`'s own list: catalog and reconcile skipped on a false premise; git identity through `configureGit` | A new step must be added twice | One step table with declared platform exclusions | — |
| D11 | The `rmw` write rule | every object-valued derive key is regenerated | `in_full` tables only | as jail | No shipped surface hits it yet | One rule at both notches | [`OQ-CO15`](../design/config-ownership-and-promotion.md#oq-co15) |
| D12 | `host_files` and `mise_tools` | `ConfigureHostFiles`, `ConfigureMisePrism` | neither, and nothing says so | as jail | MEASURED: a source-less `host_files` entry is inert at the host with no report line | — | [OQ-NC8](#OQ-NC8) |

#### Row D6: skill-name collisions in a jail

Moved here verbatim from [OQ-NC11](#OQ-NC11), where its answer is. Item 25 was filed as ruled by S1. The maintainer's words, 2026-08-05:
*"I want unnamespaced by default with a fatal collision error if skills collide on name.
Namespacing should be possible by the pack's choice, but it should be a positive choice."*
[pack-system.md](../reference/pack-system.md#skills-collision) records that as "FATAL at apply
time", and [§6a-2](../reference/pack-system.md#batch-6a) rules the composition wholesale at every
notch. Three facts found while building it stop it:

- **An open question already asks this.** [S5](BACKLOG.md#S5) ("a jail resolves a skill-name
  collision SILENTLY") is 💬 with a leaning of a launch *warning*, not a refusal, and says in so
  many words that the jail half "is a decision rather than a port".
- **The jail does not honor `skills_tier`.** It copies every pack's `skills/` flat
  (`jailcontent.copySkillSubdirs`), so a namespaced pack's skill is `/<skill>` in a jail and
  `/<pack>:<skill>` at the host. The host's collision message offers namespacing as the remedy,
  which would be false in a jail until the jail honors the tier. Honoring it renames every
  namespaced pack's skills in every jail, which no ruling asked for. No shipped pack carries
  skills today, so only user packs would move.
- **One composer would also narrow the jail's fan-out**, which is [OQ-S4](BACKLOG.md#OQ-S4),
  open. The jail sends every pack's skills to every destination. The host sends a pack's skills
  only to the destinations it names.

### 3.5 Services

| # | Concern | Jail | Host | macos-user | Divergence | Merge | Ruling |
|---|---|---|---|---|---|---|---|
| E1 | Unauthenticated credential services | [§2.1](#21-what-trusts-the-network-namespace-today) | the openai adapter on the host's loopback | the ports are composed, and bound by nobody or anybody | Safe only inside a private namespace | [§2.3](#23-the-fix-every-service-authenticates-its-caller-at-every-notch) | — (ruled 2026-09-27) |
| E2 | Pack services at a service-less notch | `yolo-jaild supervise` | refused, or `via` dropped in silence | declined; the launch proceeds against dead addresses | Three answers | One service-start path | [OQ-NC1](#OQ-NC1). ✅ Built as item 3: the host and macos-user run the service's host half through one launch-side runner, `internal/launchservice` ([NC-D65](#NC-D65)) |
| E3 | The terminator's fixed `:443` on a shared namespace | runs | — | declined by name | Collides, or binds the host's `:443` | — | [OQ-NC2](#OQ-NC2) |
| E4 | Autonomy on a shared namespace | autonomous regardless of `network.mode` (`render.ProfileFor` keys on the notch alone) | guarded | autonomous, with no network namespace | "The jail contains the agent" is half true there | — | [OQ-NC3](#OQ-NC3) |

#### Row E2: services at a service-less notch

Moved here verbatim from [OQ-NC1](#OQ-NC1), where its answer is. The host and
macos-user run no `jail_daemon` and no `kind: "service"` process. So a `via` or adapter profile
works in a container, refuses at the host, and on macos-user launches against dead addresses.
This decides whether item 3 is built at all. It also answers
[`OQ-OA6`](../reference/agent-credentials.md#oq-oa6) for Codex on macos-user. Option A is drawn
in full, for the host and macos-user, in
[`host-notch-services.md`](../design/host-notch-services.md), which owns two narrower questions
under it.

#### Row E3: the terminator on a shared namespace

Moved here verbatim from [OQ-NC2](#OQ-NC2), where its answer is. It must listen on `127.0.0.1:443`, because `--add-host` maps `platform.claude.com`
to loopback and the port cannot move. On `network.mode: "host"` or a nested jail, a second jail
finds `:443` held, or binds the host's own. This decides whether such a jail's claude refreshes
through the broker.

One more fact bears on it since item 1 was built. The refresh branch now authenticates its
caller by refresh-token match ([NC-D15](#NC-D15)), but the **proxy** branch cannot: Claude's
`/login` exchange carries no secret the jail holds and a stranger lacks. So wherever a stranger
reaches the terminator's port, it can complete an OAuth login of its own through it, and the
proxy mirror (`maybePropagateTokenResponse`) writes that login to the machine-wide shared file,
which every jail's Claude then uses. INFERRED from source, not measured. A shared namespace is
the only place a stranger reaches the port, so the answer here decides this exposure too: A
removes it, B removes it, and C leaves it open until the mirror is gated.

**Checked and already converged** (no row): the credential gate core (`ScopeCredentials`, `AgentEnv`,
`refuseUnspeakableProvider`, `ProviderCredentialGaps`); `packoverlay.Collect`, which gets the target's
autonomy bit at both notches; the shared writers `renderSurfaceRMWSurface` and
`renderSurfaceStatefulDetail`; `Pack.SurfacesForReport`; `packload.ConfigSurfaceCollisions`;
`packload.SlotLanding`; the macos-user surface render, which calls `ConfigurePackSurfaces` unchanged;
`config.LoadAdapterAddresses`; the refusal of a workspace `use_profiles` at both notches
([OQ-CS5](../reference/providers.md#oq-cs5)); and the shared undeclared-profile text
(`packload.UndeclaredProfileMessage`).

## 4. The ordered build list

**The ordering basis:** security first (P3); then the selection and the front door, because every
later composer reads them; then the checks the host skips; then composition and render. Inside a
tier, the smaller item goes first. An item marked *gated* waits for its question and blocks nothing
after it, except where its **After** cell says otherwise.

### Tier 1 — the loopback services (P3)

| # | Item | Closes | Kind | After | Done when |
|---|---|---|---|---|---|
| 1 | Per-launch caller secret for the bridge, both OpenAI adapter halves, the AWS adapter and the terminator ([§2.3](#23-the-fix-every-service-authenticates-its-caller-at-every-notch)) | E1, C2's injection half | **behavior-changing, ruled** (2026-09-27). ✅ **Built**: the bridge `ea083e97`, the other four `a5fd280f` ([NC-D12](#NC-D12) to [NC-D15](#NC-D15)). Verified by unit tests driving each service's real handler and each client's real writer; the shared-namespace configurations it exists for are in the reachability carve-out and are verified only by CI or a real rootless host. The terminator's proxy branch stays unauthenticated ([OQ-NC2](#OQ-NC2)) | — | A caller without the secret is refused at every notch and service; the selecting agent is served |
| 2 | Served addresses composed from the serving daemon; ephemeral ports where the client takes a composed address; one "served at this notch" predicate feeding one provider-composition helper | C1, C2, the macos-user dead addresses | **pure merge.** The host and macos-user converge on "dropped and named", which is ES-D18's rule generalized. ✅ **Built in part** (`8805439f`, [NC-D16](#NC-D16)): one predicate (`packload.ServedDaemons`, `ServedAtRuntime`), one composition (`ComposeProvidersAt`, `ViaServedAt`) at the jail, macos-user, the host and `yolo check`, and a pack `env` pointer declares its daemon (`served_by`) so the credential gate withholds and names it where that daemon does not run. ✅ **The rest built**, pinned by `TestAListenPointerComposesItsDaemonsServedAddress`, `TestASharedNamespaceLaunchMovesEveryServedAddressTogether` and `TestAnAttachComposesForTheRunningJailsServedAddresses` ([NC-D41](#NC-D41) to [NC-D44](#NC-D44); fixes [NC-D45](#NC-D45), `TestAServiceDaemonRefusesTheListenToken`, and [NC-D46](#NC-D46), `TestLaunchBannerNamesThePointersServedAddress`): a pointer's address is composed from its daemon's declaration (`jail_daemon.listen`, spelled `{listen}`), and a shared-namespace launch serves every jail daemon and pack service on ports it picked, which an attach re-reads. Verified by unit tests over the production compositions and, in the nested jail (a shared namespace), by two concurrent bridged launches that collided on `:8214` before (`TestTwoJailsOnOneLoopbackServeOnTheirOwnPorts`, Linux podman only: a macOS podman machine's host mode is the VM's namespace, [NC-D43](#NC-D43)). The `network.mode: "host"` arm on a real host is in the reachability carve-out: verified only by CI or a real rootless host | 1 | No notch exports an address nothing serves. `yolo check` predicts per runtime |
| 3 | Run the selected packs' services at a service-less notch | E2 | **behavior-changing, ruled** ([OQ-NC1](#OQ-NC1) A, [`OQ-HS3`](../design/host-notch-services.md#OQ-HS3), [`OQ-HS4`](../design/host-notch-services.md#OQ-HS4)). ✅ **Built**: the mechanism, `TestStartWaitsForReadinessAndStopEndsTheService` and `TestTheHostHalfServesFromItsInputAndPublishesNoFile`; the host launch, `TestHostCodexClaudeRunsThroughALaunchOwnedBridge`; macos-user, `TestTheMacosUserArmStartsTheServiceAndStopsItAfterTheCommand`; and the disclosure's address list, `launchservice.Plan.PointedAt` ([NC-D65](#NC-D65), [`HS-D24`](../design/host-notch-services.md#HS-D24); HS-D6 to HS-D14 in [`host-notch-services.md`](../design/host-notch-services.md#11-decision-ledger)). Verified by unit tests through `hostMain` and the macos-user arm of `run.Run`. The loopback listener the host half binds is in the reachability carve-out below: its reach from a real `macos-user` sandbox is verified only on a Mac | 1, 2 | `-p cerebras -- claude` works at the host and on macos-user, or refuses identically at both. Met: it works at both, and a service neither can start refuses at both, naming why |
| 4 | The terminator on a shared namespace | E3 | **superseded**, since [OQ-NC2](#OQ-NC2) was answered by [OQ-CL1](../design/claude-login-without-interception.md#OQ-CL1): nothing is built here. The terminator goes at every notch with the interception, [`claude-login-without-interception.md` §10](../design/claude-login-without-interception.md#10-what-i-would-build-in-order) step 5 | 1 | — |

### Tier 2 — one selection (P1, P2)

| # | Item | Closes | Kind | After | Done when |
|---|---|---|---|---|---|
| 5 | One pack resolver with two modes; the host stages every configured pack through `packstage.Stage`; one embedded materialization | B3, B6, B7 | **pure merge** | — | An `exclude`d skill is absent at both notches. An escaping symlink refuses both. **Partly built** (`c619e688`, corrected by [NC-D11](#NC-D11); [NC-D7](#NC-D7) to [NC-D9](#NC-D9)): `config.ResolvePack` is the one resolver, the host stages a filtered pack into a process pack tree, and an `exclude`d skill is absent at both notches. A fetched or embedded pack's escaping symlink refuses both, and so does a filtered local pack's. **Not met:** an unfiltered local pack's escaping symlink is still followed at the host and refused by the launch. The builder proposes treating that half as gated on [OQ-NC9](#OQ-NC9), because meeting it as written breaks a shipped host behavior. That is a proposal, not this row's original gate, and the row stays partial until [OQ-NC9](#OQ-NC9) is ruled. ✅ **The rest built** `81d16986` ([NC-D60](#NC-D60), [NC-D61](#NC-D61)), met in the ruled form: [OQ-NC9](#OQ-NC9) A replaced "an escaping symlink refuses both" with "a local pack's links are followed at both, filtered or not, and a fetched or embedded pack's escaping link refuses both" |
| 6 | One selection function, the closure included, at every host verb; `LoadPacks` returns entry problems and launch-shaped verbs refuse | B1, B4 | **pure merge** (NC-D4, NC-D5). ✅ **Built**, pinned by `TestEveryHostVerbSelectsThroughTheOneFunction` and `TestHostLaunchRefusesAMalformedPacksEntry` ([NC-D31](#NC-D31) to [NC-D33](#NC-D33)): `config.SelectPacks` is the one selection function for the jail launch, `yolo check`, validation, the lazy resolvers and every host verb, and `config.LoadPackEntries` returns the entry problems the launch-shaped host verbs refuse. Measured with a fake agent dumping its environment, over `"packs"` of `["claude"]`, `["codex"]`, `["pi"]` and all three: `yolo host -- claude`, `-p bedrock -- claude` and `yolo host env --agent claude -p bedrock` or `--agent codex` export the same variables before and after, and no dump holds an address nothing at the host serves. aws-auth's pointer, which ES-D24 measured the closure adding, is withheld and named instead. `-- codex` and `-- pi` reached the broker's browser login both before and after this item, which item 15 ends | **2** | `["claude"]` composes four packs at every notch, and host `-p bedrock` exports no unserved pointer |
| 7 | One installable-program predicate that honors `platforms` | B5 | **pure merge** | 5 | Host apply offers no install the vendor does not publish. **✅ Built** (`5b2bd198`; [NC-D10](#NC-D10)): `packdecl.Install.UnpublishedReason`, asked by the jail's launcher generation and the host's dep probe |
| 8 | Pack precedence computed once and recorded | B2 | **behavior-changing, gated** on [OQ-NC4](#OQ-NC4), ruled A. ✅ **Built** `ea5b12b0` ([NC-D57](#NC-D57) to [NC-D59](#NC-D59)): `config.PackSelection.Packs` is the order at the launcher, every host verb and `yolo check`, the launch records it in the pack tree, and the boot and an attach read it back. A duplicated sole-owned claim follows the same order, the later pack holding it (`48deb7bf`, [NC-D59](#NC-D59)). Verified by unit tests over the three call sites and one fixture, not by a nested jail | 6 | One winner for one key at the launcher, the boot and the host |

### Tier 3 — one front door (P2)

| # | Item | Closes | Kind | After | Done when |
|---|---|---|---|---|---|
| 9 | One value-flag reader (the `-p` grammar is ES-D27, built on its own) | A1, A2 | **pure merge.** ✅ Built, `ffb7cd65`, with the fix-forward `f984f389` ([NC-D19](#NC-D19)) | — | `--profile=` refuses with rc 2 at both notches, and `-p=zai` works at both |
| 10 | The notch decided once at the front door; `--accept-config-changes` on the explicit host spelling | A3, A4 | **pure merge.** ✅ A3 built, `434c0920` ([NC-D20](#NC-D20)). ✅ A4 ruled by [OQ-NC10](#OQ-NC10) (no host approval: the variable retired, the flag refused by name) and built, `a1812373` ([NC-D64](#NC-D64)) | 9 | Every `--at host` spelling routes the same way |
| 11 | One profile table builder; delivery narrows it to the launched process | A6 | **behavior-changing.** ✅ Built `b2ca409a` ([NC-D62](#NC-D62)), as [OQ-NC5](#OQ-NC5) ruled. Met: `yolo host --help` and `yolo run --help` both name the rule, and a test reads both | 6, 9 | One recipient rule, named in `--help` at both notches |
| 12 | `--with-credentials` in a jail | A5 | **behavior-changing.** ✅ Built 2026-10-05, as [`OQ-ES5`](../design/credential-sources-separation.md#OQ-ES5)'s jail half ruled ([§5.2](../design/credential-sources-separation.md#52-the-jail-half---with-credentials-at-a-jail-launch-built)) | 11 | `yolo --with-credentials zai -- bash` starts a jail holding zai's key at every notch, and an attach asking for another provider is refused, naming the fresh launch |

### Tier 4 — the host runs the jail's checks (P1, P4)

| # | Item | Closes | Kind | After | Done when |
|---|---|---|---|---|---|
| 13 | The [OQ-SSO8](../design/sso-backed-bedrock.md#OQ-SSO8) override check, provider and profile validation, and the profile disclosure line at the host | A7, A8 | **pure merge.** ✅ **Built**: the override check, `TestHostLaunchRefusesAPointerBesideItsOverride`; the validation, `TestValidateProviderSectionIsValidateConfigsSectionWithoutTheWorkspace`; the profile line, `TestHostLaunchSaysWhereItsProfileLanded` ([NC-D34](#NC-D34) to [NC-D36](#NC-D36)). The done-when is met in the form NC-D16 leaves it: a pointer the host delivers refuses beside its override as in a jail, and the measured aws-auth pointer is now withheld at the host, so a bearer beside it overrides nothing, as on a jail that does not serve aws-auth ([NC-D34](#NC-D34)). **Met as written 2026-09-29**: `yolo host --` now serves aws-auth's pointer for an agent on a Bedrock provider with the loophole on ([`host-notch-services.md` HS-D21](../design/host-notch-services.md#HS-D21)), and the bearer-plus-pointer launch refuses there as in a jail (`TestHostRefusesABearerBesideTheDoorwaysPointer`) | 6 | The measured bearer-plus-pointer host launch refuses, as it does in a jail |
| 14 | One credential refusal and disclosure renderer | C7 | **pure merge.** ✅ Built, `ee62fc31` ([NC-D21](#NC-D21)) | — | Byte-identical refusal bodies at both notches, apart from the named spelling |
| 15 | The declarative OpenAI prelaunch at the host, logging in only at a TTY | C6 | **pure merge.** ✅ **Built**: `openaiauthhost.Prepare`, pinned by `TestPrepareNeverLogsInWithoutATerminal` and `TestHostPiOnZaiDeclaresNoPrelaunch` ([NC-D37](#NC-D37)). Measured: before, that launch printed the broker's login URL and hung until killed; after, it runs pi with no broker started | 6 | `yolo host -p zai -- pi </dev/null` never starts a login |

### Tier 5 — one composition order and vehicle

| # | Item | Closes | Kind | After | Done when |
|---|---|---|---|---|---|
| 16 | One ordered env composition, serialized by every vehicle | C3 | **behavior-changing.** ✅ **First half built** ([NC-D72](#NC-D72)): [OQ-NC12](#OQ-NC12) decided on its leaning A, one ordered composition in packload that every vehicle serializes, and an `env_sources` null ranking with `env_sources`. The second half is **gated** on [OQ-NC13](#OQ-NC13) (whether the host keeps the shell's value), one named input once ruled. Its first gate, [`OQ-CN8`](../reference/providers.md#oq-cn8), is ruled and built (`cef51809`, CN-D21) | 13 | One key has one winner at every vehicle. **Met for yolo's own sources** (the env-winner parity tests, [NC-D72](#NC-D72)); the host still composes over the shell ([OQ-NC13](#OQ-NC13)) |
| 17 | Per-agent env files on macos-user | C4 | **behavior-changing, ruled** ([`OQ-CN9`](../reference/providers.md#oq-cn9), yes). ✅ **Built** `949d9430` (CN-D24), by the credential-scope work and ahead of item 16: it reuses the container vehicle's writer, so it needed nothing from 16, and 16 will move both backends' files at once ([NC-D67](#NC-D67)). Verified by a unit test through `Run` to the backend's handler seam, not on a Mac | 16 (not needed, [NC-D67](#NC-D67)) | An agent started from a bare `yolo`'s login shell on macos-user reads its profile's values from its own file. Met, by `TestMacosUserBareLaunchWritesEveryProfiledAgentsEnvFile` |
| 18 | One scope reader for credential-bearing keys | C5 | **behavior-changing.** ✅ The [OQ-NC6](#OQ-NC6) half built `c7a3482b` ([NC-D63](#NC-D63)): `validateProviderCredentialScope` reads the workspace file for every credential-routing provider field. The `env_sources` half is gated on [`OQ-AS3`](../research/agent-safehouse.md#OQ-AS3) | — | — |

### Tier 6 — render

| # | Item | Closes | Kind | After | Done when |
|---|---|---|---|---|---|
| 19 | Relay `YOLO_PROFILES` on macos-user through one exported wire-table list | D2 | **pure merge** (a live defect; it may land at any time) | — | codex on macos-user renders its `model_provider`. ✅ **Built** `e5f48a5f` ([NC-D22](#NC-D22)) |
| 20 | `InjectLaunchFlags` takes the autonomy bit | D9 | **pure merge** | — | A `guarded.launch` entry reaches a host launch. **Built** `bcb84bac` ([NC-D27](#NC-D27)) |
| 21 | `files` on macos-user; `ResolveDestinations` for addressed files everywhere | D8 | **pure merge** | — | pi's two extensions exist in a macos-user home. ✅ **Built** `4225117a` ([NC-D23](#NC-D23), [NC-D24](#NC-D24)) |
| 22 | One boot step table for Linux and macos-user | D10 | **pure merge** | — | An omission is a declared exclusion, never a missing line. ✅ **Built** `b8db83a8` ([NC-D25](#NC-D25), amended by [NC-D26](#NC-D26)) |
| 23 | `config render --at host` previews what host apply writes | D5 | **pure merge** | — | The preview and the write agree byte for byte. **Built** `8d39ac87` ([NC-D28](#NC-D28)) |
| 24 | One render loop | D3 | **pure merge** (a refactor) | 23 | Four loops become one. **Built** `73cbfff6`: the three writer loops are one, and the fourth's host half is the host apply's render since item 23 ([NC-D29](#NC-D29) keeps the jail preview a reader) |
| 25 | One skills composer | D6 | **behavior-changing**, ruled by [OQ-NC11](#OQ-NC11) (A, by parity), which also answers [S5](BACKLOG.md#S5). Filed as ruled (S1, [§6a-2](../reference/pack-system.md#batch-6a)); building it found an open question on the same behavior and two changes S1 does not decide | 24 | The same collision refuses at both notches. ✅ **Built** `45c53ed1` ([NC-D52](#NC-D52) to [NC-D56](#NC-D56)) |
| 26 | One briefing composer with a notch-aware base; `agents_md_extra` at the host | D7 | **behavior-changing, ruled** ([§6a](../reference/pack-system.md#batch-6a), Phase 8) | 24 | A host agent is told it is on the real machine. **Built** `180deba3` ([NC-D30](#NC-D30)) |
| 27 | The host profile table fed into render | D1 | **behavior-changing, ruled** ([`OQ-HC3`](../reference/host-agent-environment.md#oq-hc3), as leaned). ✅ **Built** `358f877d` ([HC-D17](../design/host-computed-layer.md#HC-D17), [HC-D18](../design/host-computed-layer.md#HC-D18)): host apply composes the `profile` selection once per invocation and hands it to the derives, pinned by `TestHostApplyWritesTheUseProfilesSelectionOnTheEdge` | 11, 24 | — |
| 28 | Derives at the host; one `rmw` rule | D4, D11 | **behavior-changing.** ✅ The [`OQ-HC1`](../reference/host-agent-environment.md#oq-hc1) half, derives at the host, ruled and built `358f877d` ([what each surface gets at the host](../reference/host-agent-environment.md#what-each-surface-gets-at-the-host)), pinned by `TestTheHostRendersEachDerivedSurfaceClass` and `TestYoloHostApplyAssertWritesTheComputedLayer`. The one `rmw` rule is gated on [`OQ-CO15`](../design/config-ownership-and-promotion.md#oq-co15) | 24 | — |
| 29 | `host_files` and `mise_tools` at the host | D12 | **behavior-changing, ruled** ([OQ-NC8](#OQ-NC8) A). ✅ **Built** `7f77a618` ([NC-D48](#NC-D48) to [NC-D51](#NC-D51)) | 24 | A source-less entry renders at the host through the one loop and a later apply removes what it wrote once the entry goes; a source-bearing entry and `mise_tools` are named as inert |
| 30 | Autonomy on a shared namespace | E4 | **behavior-changing, ruled** ([OQ-NC3](#OQ-NC3) A). ✅ **Built** `9a5c5b68` ([NC-D47](#NC-D47)). Verified by unit tests through `Run` and `refreshJailBriefings`; the real `network.mode: "host"` arm is in the reachability carve-out below | 1 | A shared-namespace jail keeps autonomy and says so at launch and in its briefing; a bridged one says nothing |
| 31 | Claude OAuth at the host | C8 | **behavior-changing, ruled** by [`OQ-CI1`](../reference/claude-oauth-interposition.md#oq-ci1) (B, 2026-10-05), which [OQ-NC7](#OQ-NC7)'s ruling (A, 2026-09-28) waited on: `yolo host -- claude` joins the one shared login through a credential view, and a host claude yolo did not start keeps its own. **Held** until a human runs [the view's measures](runbooks/claude-credential-view-measures.md); host claude keeps its own login until then. The opt-in half is built: the jails' switch gives one `yolo host -- claude` launch a view in a store yolo manages, `~/.claude` untouched ([CL-D27](../design/claude-login-without-interception.md#CL-D27), 2026-10-04) | 1 | — |

**Verification, per item.** Every item touches `cmd/` or `internal/`, so it gets the nested-jail run
AGENTS.md requires. **Items 1 to 4 and 30 are in AGENTS.md's reachability carve-out**: a nested jail
is forced onto `--net=host`, and that is one of the configurations they change. So each of those items
is reported verified only against a real rootless host or CI, with
`podman info --format '{{.Host.RootlessNetworkCmd}}'` stated alongside the claim.

## 5. Already merged, and in flight

| What | Where it stands | Rows |
|---|---|---|
| `-p` accepted before the `host` verb (`yolo -p bedrock host -- claude`) | **on main**, `1baf1fd4` | A1 (spelling) |
| The host codex no-op: a profile whose provider the launch does not hold refuses (`packload.MissingProviderError`, ES-D24 to ES-D26), and the host footer leaves out a selection the host refuses | **on main**, pinned by `TestAClaudeCodexSelectionWithoutItsProviderRefuses` and `TestHostFooterTablesLeaveOutAListWhoseFirstEntryTheHostRefuses` | B1's symptom, C1 |
| The host reads `-p` in the run path's grammar (`parseProfileValue`, ES-D27). A pair naming another CLI refuses by name, and providers.md's "do not unify" warning is replaced | **on main**, pinned by `TestHostProfileForReadsTheRunGrammar` and `TestHostPairNamingAnotherCLIRefuses` | A1 |
| Per-launch caller auth for the wire bridge, amending [WB-D4](../reference/wire-bridge.md#wb-d4) | **built**, `ea083e97` ([WB-D18](../reference/wire-bridge.md#wb-d18)); item 1's other four services followed in `a5fd280f` | E1, item 1 |
| [`host-notch-services.md`](../design/host-notch-services.md): the full shape of [OQ-NC1](#OQ-NC1)'s option A at the host. Its HS-D1 runs the closure through item 6's one selection function after item 2, HS-D2 is ES-D27, and its host halves take this plan's caller secret (NC-D2, NC-D3) | **built**, accepted: both its questions ruled and item 3 built ([NC-D65](#NC-D65)) | B1, E2, item 3 |
| [`host-agent-environment.md`'s computed layer at the host](../reference/host-agent-environment.md#what-yolo-host-apply-renders-into-a-derived-surface): derives at the host | **built**, accepted: [OQ-HC1](../reference/host-agent-environment.md#oq-hc1) to [OQ-HC3](../reference/host-agent-environment.md#oq-hc3) ruled and built ([what each surface gets at the host](../reference/host-agent-environment.md#what-each-surface-gets-at-the-host)). The HC-D1 to HC-D5 follow-up fixes are built too: `pi/models`' empty `providers` default (`TestAHostApplyIntoAFreshHomeWritesAModelsFileWithProviders`), `mcpEntryRemedy` (`TestFollowingTheHostMCPRemedyKeepsTheEntry`), `config-ref`'s provider line (rewritten again for [OQ-HC1](../reference/host-agent-environment.md#oq-hc1), `TestConfigRefSaysWhatAHostApplyComputesFromProviders`), `entrypoint.hostSurfaceWouldChange` (`TestAHostApplyCountsAFileItCreatesAsAChange`) and `entrypoint.hostMechanismTableLosses` (`TestAnOwnedHostApplyDoesNotReportAnEntryItKeeps`) | D4, D1 |

> [!WARNING]
> **ES-D24 is the trap for item 6.** It kept the host off the closure on purpose,
> because running it adds aws-auth's `AWS_CONTAINER_CREDENTIALS_FULL_URI=http://127.0.0.1:1461/…` to
> `yolo host -p bedrock -- claude`: a pointer at an address nothing on the host serves, and one that
> [§2](#2-security-first-the-boundary-that-is-not-one) shows is an injection channel. That measurement
> is right, and it is why item 6 comes after item 2, not a reason to leave the host off the closure.
> Once item 2 drops unserved addresses, ES-D24's reason is gone. Scoping the closure to
> `loadedHostPacks` alone would leave six host verbs on a narrower pack set, which is why
> [HS-D1](../design/host-notch-services.md#HS-D1) runs it through item 6's one selection function.
> **Answered by item 6** ([NC-D31](#NC-D31)): the pointer is withheld and named at every host verb.

## 6. Open questions

1. ✅ <a id="OQ-NC1"></a>**OQ-NC1: Does every notch run the selected packs' services?** What it decides, and where
   option A is drawn: [row E2's question](#row-e2-services-at-a-service-less-notch).

   - **A — Run them, launch-owned.** The launch (host exec, macos-user launch) starts each selected
     service as a child for its own lifetime, on an ephemeral loopback port, authenticated by item 1.
     This is the `openaiauthhost` shape, generalized from one daemon to the declaration.
     `WithoutServiceAdaptations`, `ViaInert` and the host's bridge refusal are deleted. Cost: a second
     supervisor beside `yolo-jaild supervise`, unless both call one.
   - **B — Jail only.** Service-less notches keep item 2's "dropped and named", with a refusal where
     the profile needs the service. There are two behaviors for one declaration, but they are
     disclosed.

   _Leaning:_ A. It is the shape already shipped for host Codex, and it is what "host is supposed to
   act like everywhere else" asks for. After item 1, a service on the host's loopback is no weaker
   than one in a jail.

   <!-- vantage: question id=OQ-NC1 -->

   **Answer:**
   > **Ruled in review 2026-09-28, as leaned:** A, run them launch-owned. It is the `openaiauthhost`
   > shape already shipped for host Codex, it makes one declaration mean one thing at every notch,
   > and item 1 makes a host-loopback service no weaker than a jail's.
   > [`OQ-OA6`](../reference/agent-credentials.md#oq-oa6) takes route (b). The service lifetime and
   > host-half declaration are [`host-notch-services.md`](../design/host-notch-services.md)'s
   > [`OQ-HS3`](../design/host-notch-services.md#OQ-HS3) and
   > [`OQ-HS4`](../design/host-notch-services.md#OQ-HS4).

2. ✅ <a id="OQ-NC2"></a>**OQ-NC2: What does the Claude OAuth terminator do on a shared network
   namespace?** Why it must hold `:443`, and the proxy-branch exposure the answer also decides:
   [row E3's question](#row-e3-the-terminator-on-a-shared-namespace).

   - **A — Decline it by name, as macos-user does.** Claude in that jail refreshes directly, and can
     race other jails for the single-use refresh token.
   - **B — Refuse the launch** while the terminator cannot be served.
   - **C — Reuse a yolo terminator already listening there**, identified by a handshake, and
     otherwise decline by name. After item 1 it authenticates by refresh-token match and serves the
     same machine-wide credential, so sharing it is correct.

   _Leaning:_ C, falling back to A with the race disclosed. B refuses a configuration that works
   today for a reason the user cannot act on.

   <!-- vantage: question id=OQ-NC2 -->

   **Answer:**
   > **Answered by [OQ-CL1](../design/claude-login-without-interception.md#OQ-CL1) (2026-09-28):
   > the terminator is deleted at every notch, so a shared network namespace needs no `:443`
   > listener and none of A, B or C is built.** The maintainer: *"we can just write the new one in
   > there and it just picks it up. If that's the case, then yes, we should do that."* The host
   > broker writes each workspace a credential view with no refresh token, so Claude never
   > refreshes and nothing intercepts `platform.claude.com`; the unauthenticated proxy branch goes
   > with the terminator. The deletion lands after that doc's measures pass. Until then the view is
   > the opt-in `YOLO_CLAUDE_CREDENTIAL_VIEW=1`, which drops the terminator from a launch, and a
   > shared-namespace jail without it keeps today's behavior. Recorded 2026-09-30.
   >
   > **Not ruled; reopened 2026-09-28 for a real fix.** The maintainer rejects all three options:
   > *"I want to fix this for real. I don't love any of these options … do we really need [the
   > /etc/hosts entry]? Is there no option or environment we can pass to claude to handle this
   > instead so we can redirect it somewhere better? If we involve the wire bridge here, does that
   > make our options better? … This is just completely normal, like wanting to use your claude
   > teams with host networking."* The question is now how Claude's login refresh reaches yolo's
   > broker with no hosts-file interception at all, so that a shared network namespace needs no
   > port-443 listener. Research is under way.
   >
   > Superseded in substance by
   > [`claude-login-without-interception.md`](../design/claude-login-without-interception.md): if
   > the host broker writes each workspace a credential view with no refresh token, Claude never
   > refreshes, so no terminator listens anywhere and this question dissolves, pending
   > [OQ-CL1](../design/claude-login-without-interception.md#OQ-CL1).

3. ✅ <a id="OQ-NC3"></a>**OQ-NC3: Does a jail on a shared network namespace keep the autonomous
   posture?** `render.ProfileFor` keys autonomy on the notch alone
   ([env-manager's autonomy ruling](../design/yolo-as-environment-manager.md#42-agent-autonomy-is-a-confinement-policy-not-baked-pack-config)).
   A `network.mode: "host"` jail and every macos-user jail reach the host's loopback exactly as a
   host agent does, yet render `--dangerously-skip-permissions`.

   - **A — Keep autonomy, and disclose.** A network primitive in `render.Profile` records whether
     the namespace is shared, and the launch line and briefing read it.
   - **B — Guarded whenever the namespace is shared.** Cost: macos-user becomes guarded as a whole
     backend, which reverses the autonomy ruling for it.

   _Leaning:_ A. Autonomy rests on filesystem confinement, which a shared network does not remove,
   and item 1 closes yolo's own listeners.

   <!-- vantage: question id=OQ-NC3 -->

   **Answer:**
   > **Ruled in review 2026-09-28, as leaned:** A. Keep autonomy, and disclose it from a network
   > primitive in `render.Profile`: autonomy rests on filesystem confinement, which a shared network
   > does not remove, item 1 closes yolo's own listeners, and B would make a whole backend guarded.

4. ✅ <a id="OQ-NC4"></a>**OQ-NC4: Which order does "later wins" follow?** The launcher, the boot and
   the host order packs three ways, so one key has three winners (row B2). This decides item 8.

   - **A — Config order as written, then closure additions, then the local pack last.** That is
     [pack-system.md's config-overlay rule](../reference/pack-system.md) and `localPackEntry`'s
     comment, and it is the host's order today.
   - **B — Embedded packs first, then configured, then the closure.** That is the launcher's order
     today, so a user pack can override a shipped one wherever it is listed.
   - **C — Alphabetical,** which is what the boot does by accident.

   _Leaning:_ A. It is the only order a user can predict from their own config.

   <!-- vantage: question id=OQ-NC4 -->

   **Answer:**
   > **Ruled 2026-09-28 by parity, as leaned** (the maintainer, 2026-09-28: *"yes, NC as parity for
   > sure"*; the host acts like every other notch, with the same handling): A. Config order as
   > written, then closure additions, then the local pack last, at every notch.

5. ✅ <a id="OQ-NC5"></a>**OQ-NC5: Who does a bare `-p <name>` reach?** What it reaches today at each notch, and what it decides:
   [row A6's question](#row-a6-who-a-bare--p-reaches).

   - **A — pv-oq-5 everywhere.** A command no pack installs gets keys only through
     `--with-credentials`, the grant [OQ-ES5](../design/credential-sources-separation.md#OQ-ES5) ruled for exactly that. ES-D1 retires, and the host's
     refusal names the grant.
   - **B — The host meaning everywhere.** A bare name also keys the launched `--` command, so a jail
     `yolo -p zai -- bash` hands bash `ZAI_API_KEY`. That reverses pv-oq-5's never-the-command half.

   _Leaning:_ A. The grant exists now and is ruled, so a second, implicit route for the same thing
   is the duplicate path this plan removes.

   <!-- vantage: question id=OQ-NC5 -->

   **Answer:**
   > **Ruled 2026-09-28 by parity, as leaned** (the maintainer, 2026-09-28: *"yes, NC as parity for
   > sure"*; the host acts like every other notch, with the same handling): A. A bare `-p` reaches
   > agent CLIs only, at every notch; any other command gets keys only through `--with-credentials`.
   > ES-D1 retires.

6. ✅ <a id="OQ-NC6"></a>**OQ-NC6: Is `providers.*.api_key_env_name` user-scope only, as `base_url`
   is?** [`OQ-LM3`](../research/local-model-endpoints.md#oq-lm3) made addresses user-scope only. The
   rest of a provider entry still merges in from the workspace, so a repo file can re-point which
   variable a provider claims and sends upstream. [`OQ-AS3`](../research/agent-safehouse.md#OQ-AS3)
   asks the same question of `env_sources`.

   - **A — User scope only**, refused at workspace scope like `base_url`.
   - **B — Merge, and disclose** a workspace value that changes a claim.

   _Leaning:_ A. [OQ-LM3](../research/local-model-endpoints.md#oq-lm3)'s reason applies unchanged: the blast radius is total.

   <!-- vantage: question id=OQ-NC6 -->

   **Answer:**
   > **Ruled in review 2026-09-28, as leaned:** A. Every provider field that decides where a
   > credential goes is user-scope only, `api_key_env_name` included, refused at workspace scope
   > like `base_url`; [`OQ-LM3`](../research/local-model-endpoints.md#oq-lm3)'s reason, that the
   > blast radius is total, applies unchanged.

7. ✅ <a id="OQ-NC7"></a>**OQ-NC7: Does host claude join the jails' Claude login?** OpenAI has one
   machine-wide login lineage for the host and the jails. Claude has two, because the host notch
   cannot emit the hosts entry that interception needs.

   - **A — No, for now.** Host claude keeps its own login. Revisit alongside
     [`OQ-CI1`](../reference/claude-oauth-interposition.md#oq-ci1).
   - **B — A managed config dir** that shares the machine store. Its refreshes then race the broker
     unless the host routes them through it, and there is no mechanism for that yet.

   _Leaning:_ A. A shared store without interception is worse than two lineages.

   <!-- vantage: question id=OQ-NC7 -->

   **Answer:**
   > **Ruled in review 2026-09-28, as leaned:** A. Host claude keeps its own login until
   > [`OQ-CI1`](../reference/claude-oauth-interposition.md#oq-ci1) decides whether the credential is
   > shared at all; a shared store without interception races the broker.

8. ✅ <a id="OQ-NC8"></a>**OQ-NC8: What do `host_files` and `mise_tools` do at the host?** Both
   render in every jail and are silently inert at the host. That breaks P4 whichever way this is
   answered.

   - **A — Render source-less `host_files` entries at the host** through the same surface engine,
     under `host_management`. Name source-bearing entries and `mise_tools` as inert.
   - **B — Name both as inert** in host apply's notch line now. Revisit `host_files` once
     [`OQ-CO14`](../design/config-ownership-and-promotion.md#oq-co14) settles host ownership. Host
     tools belong to [`OQ-PS1`](../design/provisioner-sets.md#OQ-PS1).

   _Leaning:_ B. It ends the silence today without choosing an ownership model early.

   <!-- vantage: question id=OQ-NC8 -->

   **Answer:**
   > **Ruled 2026-09-28 by parity, against the leaning** (the maintainer, 2026-09-28: *"yes, NC as
   > parity for sure"*; the host acts like every other notch, with the same handling): A.
   > Source-less `host_files` entries render at the host through the same surface engine, under
   > `host_management`; source-bearing entries and `mise_tools` are named as inert.

9. ✅ <a id="OQ-NC9"></a>**OQ-NC9: May a local pack carry a symlink that points out of the pack?**
   Background: [row B3's question](#row-b3-a-local-packs-escaping-symlinks).

   - **A. Follow a local pack's links at every notch.** The launch stages such a pack with
     `FollowSymlinks` too. The no-escape rule's stated threat is someone else's repository
     smuggling a host file into a jail, and a local pack is a directory the user named in their
     own user config, which [OQ-TP9](../design/trust-paths.md#decision-ledger) already treats as
     full trust. A fetched pack's escaping link stays refused at every notch.
   - **B. Refuse at every notch,** as the plan's done-when read. A dotfile-deployed local pack
     then fails `yolo host apply` the way it already fails a jail launch, and the user replaces
     the links with copies.

   _Leaning:_ A. It deletes the input rather than keeping it, which is this plan's rule. It fixes
   the jail refusing a pack the host accepts, and it keeps the no-escape rule for the case that
   motivated it.

   <!-- vantage: question id=OQ-NC9 -->

   **Answer:**
   > **Ruled 2026-09-28 by parity, as leaned** (the maintainer, 2026-09-28: *"yes, NC as parity for
   > sure"*; the host acts like every other notch, with the same handling): A. A local pack's links
   > are followed at every notch; a fetched pack's escaping link stays refused at every notch.

10. ✅ <a id="OQ-NC10"></a>**OQ-NC10: What does `--accept-config-changes` approve at the host?** Why item 10's premise was
    found stale: [row A4's question](#row-a4-what-the-hosts-approval-approves).

    - **A — There is no host approval: retire the variable.** Delete `acceptConfigChangesEnv` and
      its documentation, and keep refusing the flag by name. The first-apply loss stays
      `yolo host apply --assert`'s question, at a terminal.
    - **B — The flag and the variable approve the first-apply loss off a terminal.** The explicit
      spelling takes the flag, the wrapper path the variable, as [OQ-HS10](../reference/host-apply-staleness.md#why-its-this-way) designed. It lets a
      scripted first launch drop undeclared MCP servers, the one-way door host-apply-staleness
      keeps behind a terminal today.

    _Leaning:_ A. A one-way door that drops a user's servers should not open for a flag typed to
    get past a prompt that no longer exists. B re-creates a grant for the one question the gate
    deliberately keeps behind a terminal.

    <!-- vantage: question id=OQ-NC10 -->

    **Answer:**
    > **Ruled in review 2026-09-28, as leaned:** A. The zero-prompt auto-apply left the host launch
    > nothing to approve: retire the unread `YOLO_ACCEPT_CONFIG_CHANGES`, keep refusing the flag by
    > name, and leave the first-apply MCP loss to `yolo host apply --assert` at a terminal.

11. ✅ <a id="OQ-NC11"></a>**OQ-NC11: Does a skill-name collision refuse a jail launch, and does the
    jail honor `skills_tier`?** Background:
    [row D6's question](#row-d6-skill-name-collisions-in-a-jail). This decides item 25.

    - **A — Fatal at the launch, host-side, and the jail honors the tier.** Put a pre-flight beside
      `AgentNameCollisions` that runs before the container exists, and on an attach too. It uses
      `hostskills.Collisions` over the jail's own destinations and prints the host's message. The
      jail delivers through the hostskills layer plan, tiers and wrapped plugins included. Its
      fan-out stays as it is until [OQ-S4](BACKLOG.md#OQ-S4) is answered.
    - **B — A launch warning naming both packs** (S5's option 1). The jail stays flat and
      last-wins, and the host stays fatal.
    - **C — A `yolo check` failure only** (S5's option 2).

    _Leaning:_ A. The refusal would be a launch pre-flight on the host, like the agent-name one, not
    an A12 boot failure inside a running jail, so the stranding cost S5 weighs does not apply. The
    jail has to honor the tier, or the remedy the message offers does nothing there.

    <!-- vantage: question id=OQ-NC11 -->

    **Answer:**
    > **Ruled 2026-09-28 by parity, as leaned** (the maintainer, 2026-09-28: *"yes, NC as parity for
    > sure"*; the host acts like every other notch, with the same handling): A. A skill-name
    > collision is fatal at the launch, host-side and before the container exists, and the jail
    > honors `skills_tier`; the jail's fan-out stays as it is until
    > [`OQ-S4`](BACKLOG.md#OQ-S4) is answered.
    >
    > **Built** `45c53ed1` ([NC-D52](#NC-D52) to [NC-D56](#NC-D56)). [S5](BACKLOG.md#S5) is marked
    > answered by this ruling, and its launch-warning option is superseded.

12. ✅ <a id="OQ-NC12"></a>**OQ-NC12: When two of yolo's own sources set one variable, which wins?**
    The three sources, how each vehicle orders them today (MEASURED), and each option's
    reasoning and cost in full: [row C3's first question](#row-c3-which-of-yolos-own-sources-wins).
    This decides the first half of item 16.

    - **A — The shape var, then `env_sources`, then the fold.**
    - **B — `env_sources`, then the shape var, then the fold.**
    - **C — The per-agent file's order today:** `env_sources`, then the fold, then the shape var,
      with the other two vehicles moved to it.


    _Leaning:_ A. A profile is chosen per agent or per launch, so its derive's value is the more
    specific intent, the reasoning pv-oq-8 applies inside the fold. The case B protects, a value
    meant to beat a profile, already has a spelling that wins: the per-command value
    [`OQ-CN8`](../reference/providers.md#oq-cn8) keeps.

    **Answer:**
    > **Ruled in review 2026-10-05, A, with a disclosure:** *"can we do A and then disclose at every
    > launch if something was shadowed in that way? that's probably best."* Everywhere, the profile wins,
    > then the `env_sources` keys file, then the pack's default; and every launch says which value it
    > shadowed, naming the losing source. Both ways of starting an agent on macos-user give the same
    > answer. The order was built on the leaning by the parity build's B17 ([NC-D72](#NC-D72)), which
    > records one departure from the question's text: its background has an `env_sources` null keep
    > beating every assignment under every option, and the build ranks a null with `env_sources`
    > instead. It removes a pack's value and, at the host, the shell's, and never the profile's
    > value, because a null that removed a derive's address left the agent the credential paired
    > with it. The disclosure is new.

13. ✅ <a id="OQ-NC13"></a>**OQ-NC13: Does the host keep a value the user's shell already has, as a
    jail does?** Background, and each option in full:
    [row C3's second question](#row-c3-the-host-and-the-users-shell). This decides the second half
    of item 16.

    - **A — Keep composing over the shell, and say so.** A notch difference with its reason
      stated (P4): the host cannot tell a value an earlier `eval` left from one the user typed.
    - **B — The shell's value wins, and `yolo host env` records what it exported.** The script
      also exports a record of the names and values it set.
    - **C — The shell's value wins, with no record.** A value from an old `eval` then beats a
      changed profile until the user runs the `eval` again.


    _Leaning:_ B. It is the jail's rule at the host, which "host is supposed to act like everywhere
    else" asks for. The record is the one thing the host lacks to apply the rule's second half. A
    keeps the two notches apart for no gain a user sees, and C drops half of the ruling.

    **Answer:**
    > **Ruled in review 2026-10-05, A, against the leaning.** The maintainer: *"we should have some sort
    > of boundary here. If you're talking about environment variables on the host, they don't get
    > silently passed into the jail, but in the jail config you can list environment sources, you can
    > inject things in. So in the same way at the host, I guess the environment sources should win, and
    > if you really take that parallel, then existing environment variables should have absolutely no
    > impact, which does seem kind of right."* At `yolo host`, what yolo composes (profiles,
    > `env_sources`) wins over a value the user's shell already has, and the shell's value has no say
    > over a name yolo composes. Not ruled here: scrubbing the rest of the shell's environment
    > (`PATH`, `SSH_AUTH_SOCK` and the like) at the host.

## 7. Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| OQ-NC1 | **Maintainer ruling**, as leaned: A, every notch runs the selected packs' services, launch-owned; the lifetime and host-half declaration are [`OQ-HS3`](../design/host-notch-services.md#OQ-HS3) and [`OQ-HS4`](../design/host-notch-services.md#OQ-HS4) | 2026-09-28 | [OQ-NC1](#OQ-NC1) | ✅ item 3 ([NC-D65](#NC-D65)) |
| [OQ-NC4](#OQ-NC4), [NC5](#OQ-NC5), [NC8](#OQ-NC8), [NC9](#OQ-NC9), [NC11](#OQ-NC11) | **Maintainer ruling by parity:** *"yes, NC as parity for sure"*. Each takes its A: one pack order, `-p` means agent CLIs everywhere, source-less `host_files` render at the host (NC8 against its leaning), local-pack links followed at every notch, a skill collision fatal before the jail starts and the jail honors `skills_tier` | 2026-09-28 | [§6](#6-open-questions) | NC4 ✅ `ea5b12b0` ([NC-D57](#NC-D57) to [NC-D59](#NC-D59)); NC5 ✅ `b2ca409a` ([NC-D62](#NC-D62)); NC8 ✅ `7f77a618` ([NC-D48](#NC-D48) to [NC-D51](#NC-D51)); NC9 ✅ `81d16986` ([NC-D60](#NC-D60), [NC-D61](#NC-D61)); NC11 ✅ `45c53ed1` ([NC-D52](#NC-D52) to [NC-D56](#NC-D56)) |
| [OQ-NC11](#OQ-NC11), built | The parity row's NC11 half: a skill-name collision refuses a jail launch host-side, before the container and on an attach too, with the host's message, and the jail composes through the hostskills layer plan, so `skills_tier` and wrapped plugins behave as at the host. Answers [S5](BACKLOG.md#S5) | 2026-09-28 | [OQ-NC11](#OQ-NC11), item 25 | ✅ `45c53ed1` |
| [OQ-NC6](#OQ-NC6) | **Maintainer ruling**, as leaned: A, every provider field that decides where a credential goes is user-scope only | 2026-09-28 | [§6](#6-open-questions) | ✅ `c7a3482b`, the field list [NC-D63](#NC-D63) |
| [OQ-NC7](#OQ-NC7) | **Maintainer ruling**, as leaned: A, host claude keeps its own login until [`OQ-CI1`](../reference/claude-oauth-interposition.md#oq-ci1) rules. *[`OQ-CI1`](../reference/claude-oauth-interposition.md#oq-ci1) was ruled 2026-10-05 (B): host claude joins through a credential view once the view's measures pass, row 31* | 2026-09-28 | [§6](#6-open-questions) | ✅ nothing to build |
| [OQ-NC3](#OQ-NC3) | **Maintainer ruling**, as leaned: A, a jail on a shared network keeps autonomy, disclosed from a network primitive in `render.Profile` | 2026-09-28 | [§6](#6-open-questions) | ✅ `9a5c5b68` ([NC-D47](#NC-D47)) |
| [OQ-NC10](#OQ-NC10) | **Maintainer ruling**, as leaned: A, retire `YOLO_ACCEPT_CONFIG_CHANGES` and keep refusing the flag by name | 2026-09-28 | [§6](#6-open-questions) | ✅ `a1812373` ([NC-D64](#NC-D64)) |
| [OQ-NC5](#OQ-NC5) | *The parity ruling's NC5 half, recorded on its own row for its build:* a bare `-p` reaches agent CLIs only, at every notch | 2026-09-28 | [§6](#6-open-questions) | ✅ `b2ca409a` ([NC-D62](#NC-D62)) |
| [OQ-NC2](#OQ-NC2) | **Answered by [OQ-CL1](../design/claude-login-without-interception.md#OQ-CL1)** (ruled 2026-09-28, recorded here 2026-09-30): the credential view replaces the hosts entry, the CA and the terminator at every notch, *"we can just write the new one in there and it just picks it up"*, so a shared namespace needs no `:443` listener and item 4 is superseded | 2026-09-30 | [§6](#6-open-questions) | — the deletion waits on [`claude-login-without-interception.md`](../design/claude-login-without-interception.md#10-what-i-would-build-in-order)'s measures; the view is built behind `YOLO_CLAUDE_CREDENTIAL_VIEW` |
| NC-D1 | **Maintainer ruling.** Merge the host and jail notches as far as possible: one description, one code path per concern, the notch as an input. "Parsing profiles should be the same", and "host is supposed to act like everywhere else" | 2026-09-27 | [§1](#1-the-thesis) | — |
| NC-D2 | **Maintainer ruling.** A loopback service's safety never rests on the jail's network namespace ("jails … can be house type and then it's identical … solve it in both places"). This retires the premise of [WB-D4](../reference/wire-bridge.md#wb-d4) and the awscredadapter "no token" design | 2026-09-27 | [§2.2](#22-why-that-premise-is-false-in-a-jail-too) | ✅ `ea083e97`, `a5fd280f` |
| NC-D3 | *Implementation decision.* The per-service mechanisms of [§2.3](#23-the-fix-every-service-authenticates-its-caller-at-every-notch): each client carries the secret in a slot it already has, so no agent changes, and the terminator authenticates by refresh-token match | 2026-09-28 | [§2.3](#23-the-fix-every-service-authenticates-its-caller-at-every-notch) | ✅ `ea083e97`, `a5fd280f` |
| NC-D4 | *Implementation decision.* The host's selection closure (item 6) lands after served-address composition (item 2), which removes ES-D24's standing reason for not applying it | 2026-09-28 | [§5](#5-already-merged-and-in-flight) | — |
| NC-D5 | *Implementation decision, on NC-D1.* Launch-shaped verbs (`yolo --`, `yolo host --`, `yolo host env`) refuse a malformed or unresolvable pack entry at every notch, and read-only verbs report it. This overrides NS-D14's per-verb host dispositions | 2026-09-28 | [§3.2](#32-packs) | — |
| NC-D6 | *Implementation decision.* Host-loopback forwarding into bridged jails stays all-port. Reaching a host service is a documented feature, and item 1, not narrower forwarding, is what closes yolo's own listeners | 2026-09-28 | [§2.2](#22-why-that-premise-is-false-in-a-jail-too) | ✅ (unchanged) |
| <a id="NC-D7"></a>NC-D7 | *Implementation decision.* The one pack resolver is `config.ResolvePack`, in `internal/config`, because config validation is one of its callers and `internal/config` cannot import `internal/cli/run`. It has two modes, chosen by whether the caller names a destination. STAGE copies through `packstage.Stage` and loads the copy. DECLARATION loads a filtered entry from a temp copy, and an unfiltered one in place after `packstage.Check`, the same walk without a copy. The pack store's posture (`ReadOnlyStore`) is an input, not a mode, and each caller keeps the one it had. `run.PackRoot` and `cli.loadAsStaged` are deleted | 2026-09-28 | [§4](#4-the-ordered-build-list), item 5 | ✅ `c619e688` |
| <a id="NC-D8"></a>NC-D8 | *Implementation decision.* The host verbs stage into a **process pack tree** (a term coined in `internal/packload/processtree.go`): one leased directory per process under the embedded fallback's name prefix, released by `packload.ReleaseEmbedded`. That exit point already covers every exit that releases the embedded packs, so no new cleanup call is needed. The existing fallback sweep and `yolo prune` reap a tree its owner left behind, by the same lease, without learning a new name. A message naming a staged file maps it back to the source (`packload.Pack.SourcePath`) | 2026-09-28 | [§4](#4-the-ordered-build-list), item 5 | ✅ `c619e688` |
| <a id="NC-D9"></a>NC-D9 | *Implementation decision, on B6's "filters always apply".* An embedded entry's `only` and `exclude` apply at every notch. Config accepted them, and every notch then ignored them. `yolo pack explain` resolves through the same resolver, so it explains shipped and fetched entries as well as local ones. A broken embedded materialization is named as a yolo bug at both notches (`config.embeddedPackNamed` asks `EmbeddedProblems` first) | 2026-09-28 | [§4](#4-the-ordered-build-list), item 5 | ✅ `c619e688` |
| <a id="NC-D10"></a>NC-D10 | *Implementation decision.* The installable-program predicate lives in `packdecl` (`Install.UnpublishedReason`, and `DepRequirement.UnpublishedReason` over the same list), not `packload`, because both projections it answers for are `packdecl` types. At the host, an absent program with no vendor build is neither missing nor a blocker, and gets no install offer. It gets a line and a verdict clause, which is the jail's "no launcher and a line". A present one is present: the probe runs first, the jail's order | 2026-09-28 | [§4](#4-the-ordered-build-list), item 7 | ✅ `5b2bd198` |
| <a id="NC-D11"></a>NC-D11 | *Implementation decision, correcting NC-D8.* A host-side process resolution (`config.ResolvePackForProcess`) copies only a FILTERED entry into the process pack tree, and reads an unfiltered one in place after `packstage.Check`. A copy of an unfiltered pack holds the same files. It cost every host verb a copy of every selected pack, and a loophole module read from it died with the verb while a host-scope daemon spawned from it by `yolo host-daemon start` kept running. `FollowLocalSymlinks` reaches only an unfiltered local entry, and every caller that read one in place before `c619e688` sets it, so those callers agree about which packs resolve again. A filtered local pack's escaping symlink is refused at both notches, as it was before | 2026-09-28 | [§4](#4-the-ordered-build-list), item 5 | ✅ |
| <a id="NC-D12"></a>NC-D12 | *Implementation decision.* Which jail daemons get a caller token is declared, not inferred. A loophole's `jail_daemon` may declare `caller_token: true` (`loopholedecl.JailDaemon.CallerToken`), a pack service's jail daemon always demands one, and the launcher mints one per daemon in the composed payload that demands one (`callerTokenVars` over `jailDaemonsFor`, the payload the argv serializes and macos-user declines). The OpenAI and AWS adapters declare it. The terminator does not, because its client cannot send one. The variable is `paths.ServiceCallerTokenEnv` of the loophole's name, so a loophole token travels, and is re-read by an attach, exactly as the bridge's does. A daemon handed no token, or a malformed one, binds nothing and idles | 2026-09-28 | [§2.3](#23-the-fix-every-service-authenticates-its-caller-at-every-notch) | ✅ `a5fd280f` |
| <a id="NC-D13"></a>NC-D13 | *Implementation decision.* The OpenAI adapter's token rides in the refresh marker as `yolo-broker:<generation>.<token>`. The writer binds it (`openauthclient.BindCallerToken`: the jail's Codex launcher from `$YOLO_SERVICE_OPENAI_AUTH_BROKER_TOKEN`, the host from a token `openaiauthhost.prepare` holds in process and never exports, shared by the managed home's live launches ([NC-D18](#NC-D18))). The adapter refuses anything else `401 invalid_client` in OAuth's shape, then strips the token before the broker and binds the broker's next marker, so the host service's wire contract is unchanged. Pi's marker is not bound, because pi refreshes through the authenticated endpoint, not the adapter | 2026-09-28 | [§2.3](#23-the-fix-every-service-authenticates-its-caller-at-every-notch) | ✅ `a5fd280f` |
| <a id="NC-D14"></a>NC-D14 | *Implementation decision.* The AWS SDKs take the token as `AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE`, not the by-value `AWS_CONTAINER_AUTHORIZATION_TOKEN`, because a pack's `env` contribution cannot carry a secret and a path can sit beside the URI under the same `bedrock` gate and `overridden_by` rules. The entrypoint writes every well-formed caller token it was handed to `/run/yolo/caller-tokens/<VAR>` (`paths.JailCallerTokenFile`), `0600`, no newline, before the supervisor starts. **What it proves:** the caller could read that file. Inside the jail every process can, which is the adapter's intended audience. Outside it, on a shared loopback, no process can, and that is the whole difference the token makes | 2026-09-28 | [§2.3](#23-the-fix-every-service-authenticates-its-caller-at-every-notch) | ✅ `a5fd280f` |
| <a id="NC-D15"></a>NC-D15 | *Implementation decision.* The terminator authenticates at the **broker**, not in the jail, because the broker holds both the shared file and the history of what it replaced. The terminator sends the presented refresh token as `presented_refresh_token` on `action=refresh`. `DoRefreshAsCaller` compares it, under the refresh `flock`, against the file's current token and the SHA-256 of the last four the broker replaced (in memory; a Claude re-reads the file just before it POSTs, so only a token replaced in that window is presented honestly). A mismatch is `401 caller_unauthenticated`, never `invalid_grant`, which would make Claude blank the shared file. The presented token is compared and never spent, so P1 of the refresh-mechanics research holds. A frame **without** the field is a terminator older than this, in a jail still on its boot binaries, and is served as before, so upgrading the host broker logs no running jail out. A stranger cannot choose that arm, because this build's terminator always sends the field | 2026-09-28 | [§2.3](#23-the-fix-every-service-authenticates-its-caller-at-every-notch) | ✅ `a5fd280f` |
| <a id="NC-D16"></a>NC-D16 | *Implementation decision.* **Served at this notch** (coined here) means the notch runs the jail daemon: a container runtime runs every daemon its composed payload names, macos-user and the host run none. It is one value, `packload.ServedDaemons`, built by `ServedAtRuntime` for a jail launch (the launch's `servedDaemons`, and `yolo check`'s `predictedServed` over the configured runtime) and `NothingServed` at the host. The one runtime branch lives in `ServedAtRuntime`, so the launch and the prediction cannot disagree. Every provider composition goes through `ComposeProvidersAt` (`WithServed`), which leaves out each adapter address whose service is not served and returns what it left out for the gate; `ViaServedAt` clears an unserved via and names it. `WithoutServiceAdaptations`, `UnservableAdaptations` and `ViaInert`, the host's second path, are deleted. A pack `env` contribution that points at a daemon declares it (`served_by`, packdecl). The credential gate withholds those variables where the daemon is not served, from every vehicle (`ScopeInput.Served`, `CredentialScope.FoldFor`), and every notch names them in one wording (`packload.UnservedLines`). The env-override check skips a contribution the gate withholds, since there is nothing to override. The two shipped pointers declare it: codex's `CODEX_REFRESH_TOKEN_URL_OVERRIDE` (`openai-auth-broker`) and aws-auth's `bedrock` pointer (`aws-auth`). A managed host launch that serves a pointer itself (`yolo host -- codex`, `openaiauthhost`) is not told it is missing (`688ca4f2`). Consequences: on macos-user a profile that needs the wire bridge now refuses as at the host, and aws-auth's pointer is withheld there, so the [OQ-SSO8](../design/sso-backed-bedrock.md#OQ-SSO8) refusals no longer fire over a pointer that could never answer. A container launch that has not enabled a loophole withholds its pointer too. ⚠ The macos-user half is superseded: that backend now runs its jail daemons ([NC-D66](#NC-D66)); and the host now serves one, aws-auth's doorway for an agent on a Bedrock provider ([`host-notch-services.md` HS-D21](../design/host-notch-services.md#HS-D21)) | 2026-09-28 | [§2.4](#24-the-addresses-those-secrets-protect-are-composed-not-literal) | ✅ `8805439f` |
| <a id="NC-D17"></a>NC-D17 | *Implementation decision.* **Ephemeral jail ports are not built in this pass, and neither is composing a pointer's address from its daemon's declaration**, which is the same mechanism: today the port is a literal in the daemon's argv and again in the pack's `env`, held together by tests. The host already takes one: the managed Codex adapter binds `127.0.0.1:0` and composes the address it got (`openaiauthhost`). A jail's clients are composed on the host before its daemons bind, so an ephemeral jail port has to be chosen by the launcher on a shared namespace, substituted into the daemon's argv, the pack env value and the provider table, and re-read on an attach exactly as the caller token is. That is a mechanism of its own and is left for a follow-up. Until then two jails sharing one loopback (`network.mode: "host"`, or nested) still contend for `:1460`, `:1461` and `:8214` to `:8216`. Since item 1 the loser **fails closed**: its client reaches the winner's daemon and is refused `401` for the wrong token. Before item 1 the winner answered the loser's client | 2026-09-28 | [§2.4](#24-the-addresses-those-secrets-protect-are-composed-not-literal) | ✅ the follow-up, [NC-D41](#NC-D41) to [NC-D44](#NC-D44) (`TestASharedNamespaceLaunchMovesEveryServedAddressTogether`, `TestAnAttachComposesForTheRunningJailsServedAddresses`) |
| <a id="NC-D18"></a>NC-D18 | *Implementation decision.* A managed host Codex launch's caller token belongs to the managed home's **live launches**, not to one launch. Every `yolo host -- codex` shares one managed `CODEX_HOME`, so one `auth.json`, and Codex reloads that file before each refresh, so a token per launch let the newest launch's replace every other session's and those sessions' adapters refused their own Codex `401`: two host sessions at once, which worked before item 1, broke. A launch holds a shared `flock` on the home's `.yolo-live.lock` until its agent exits (`sharedCallerToken`). One that can take it exclusively is alone, and mints a fresh token into the `0600` `.yolo-caller-token`; otherwise it reuses that file's token. A second lock serializes the decision, so no launch rotates the token under another. **What it proves** is unchanged: the per-launch token also lived only in that home's `0600` `auth.json`, so the proof was always that the caller can read the managed Codex home. A per-launch `CODEX_HOME` was not taken, because it would split Codex's sessions and history across launches | 2026-09-28 | [§2.3](#23-the-fix-every-service-authenticates-its-caller-at-every-notch) | ✅ |
| <a id="NC-D19"></a>NC-D19 | *Implementation decision, item 9.* `readValueFlag` (`internal/cli/valueflags.go`) is the one reader for `-p`/`--profile`, `--at`, `--network` and `--with-credentials` at `yolo [run]`, `yolo host --` and `yolo host env`. A value is the next token whatever it looks like (`-p -h` stays a profile named `-h`) or a glued `=value`. The `--` separator is never a value. A trailing flag, a flag followed by `--`, and an empty glued value are refused as "`<flag>` needs a value", exit 2. `--with-credentials` in a jail keeps its host-only refusal, which outranks the missing value. To make `yolo -p -- claude` a missing value rather than a profile named `run`, `RewriteArgv` puts `run` first instead of at the separator, and leaves argv alone when any positional precedes `--` (`f984f389`: for one commit `yolo chekc -- x` launched `chekc` in the jail). `valueTakingFlags` and `runHelpRequested`'s skip derive from `launchValueFlags`, and a source scan pins every `readValueFlag` call against it. A mistyped flag before `yolo host`'s `--`, and at `yolo host env`, is refused in `refuseUnknownFlags`' words | 2026-09-28 | [§4](#4-the-ordered-build-list) item 9 | ✅ `ffb7cd65`, `f984f389` |
| <a id="NC-D20"></a>NC-D20 | *Implementation decision, item 10 row A3.* `routeArgv` (`internal/cli/dispatch.go`) is the front door's one decision, for `Main` and `routeDecision`. A launch, explicit or implicit, whose own tokens carry `--at host` becomes `yolo host`. `parseRunArgs` reads it, so the last `--at` wins and an implicit command start (`yolo run --at host claude --resume`) hands the whole command over. The run token and the notch pair are consumed and every other token is carried over as typed, so the host parser judges it. `stripHostNotch` is deleted. A bare `yolo --at host` is `yolo host`, which prints its usage. `yolo host` takes `--at host` as a no-op and refuses another notch by name, with the jail launcher's words for an unknown one. It refuses by name a run flag with no host meaning, derived from `runFlags` less `--profile` and `--at`, and that includes `--accept-config-changes` until [OQ-NC10](#OQ-NC10). It answers `--help` among its exec flags. `yolo --at host env` is not routed: `env` is a host verb, not a launch, and naming host verbs at the front door would copy `hostMain`'s switch. *Refined after review:* `hostNotchArgv` consumes EVERY `--at` pair, so last-wins holds in both orders (`--at jail --at host` is the host, not a host refusal of the earlier `--at jail`). With no `--`, `hostMain` hands a leading flag other than help to `parseHostExecFlags`, so `yolo --at host --profile=` and `yolo host --profile=` refuse with exit 2 as a `--` line does, not as `unknown verb "-p"`; flags that parse are refused for naming no command, since the host verb has no default command and a host shell would be one the user did not type | 2026-09-28 | [§4](#4-the-ordered-build-list) item 10 | ✅ `434c0920` |
| <a id="NC-D21"></a>NC-D21 | *Implementation decision, item 14.* `packload.ProviderCredentialRefusal`, beside `ProviderCredentialGaps`, is the credential pre-flight's whole message at both notches: the verdict, the facts, the remedy, or the override notice with no hatch re-offered. It also returns whether to stop. The host prints it behind `yolo host: ` and the jail bolds its verdict, which is all that differs. The host names the inherited environment as `packload.FromLaunchEnv`, the jail's words, so the consulted line matches too. `CredentialScope.Disclosure` is deleted and the jail passes the zero `DisclosureNotes` to `DisclosureWith`, so a notch's difference is a named note and never a second function. Each notch's call-site test carries the same literal body | 2026-09-28 | [§4](#4-the-ordered-build-list) item 14 | ✅ `ee62fc31` |
| <a id="NC-D22"></a>NC-D22 | *Implementation decision, item 19.* `entrypoint.WireTables` is the one list of wire tables. The macos-user bootstrap relay, `PlanInvariants`, both channel writers in the run pipeline and `ParseEntryChannel` range over it. `PlanInvariants` now reads the tables from the session env file: it scanned `LaunchArgv`, which has carried no composed value since the channel moved into that file, so it could not fire for any table | 2026-09-28 | [§3.4](#34-render) D2 | ✅ `e5f48a5f` |
| <a id="NC-D23"></a>NC-D23 | *Implementation decision, item 21.* `packFilesTargets` resolves addressed trees through `packload.ResolveDestinations`, and its own slot table is deleted. For the jail's missing-source warning to survive that, destination borrowing routes a `files` contribution whenever it declares a `from`, present or not. So host apply now refuses such a path as a missing source, where it used to drop it without a word | 2026-09-28 | [§3.4](#34-render) D8 | ✅ `4225117a` |
| <a id="NC-D24"></a>NC-D24 | *Implementation decision, item 21.* macos-user delivers `files` trees through the home overlay, beside skills and briefings: copied from the staged pack tree, listed for the overlay install, and write-denied by the Seatbelt profile, which is this backend's `:ro`. A tree whose source is missing is skipped with the jail's own warning | 2026-09-28 | [§3.4](#34-render) D8 | ✅ `4225117a` |
| <a id="NC-D25"></a>NC-D25 | *Implementation decision, item 22.* One table, `internal/entrypoint/bootsteps.go`, run by `Main` and `RunDarwinBootstrap`. An exclusion is a field on the step holding its reason. Each boot runs what it ran before, in the same order and with the same perf and failure labels, except that the orphan catalog and the program reconcile now run on macos-user too, since their stated premise (no staged pack tree there) is false. Left as declared exclusions: the CA bundle, nvim config, the retired-mise cleanup and the stale-client cleanup, each stated as not ported or not established rather than impossible. Not built here: macos-user does not relay `programs.autoprune` (`YOLO_PROGRAMS_AUTOPRUNE`), so autoprune stays off there. Item 1's caller-token files ([NC-D14](#NC-D14)), built beside this table, are its container-only step `write_caller_token_files`, which runs before the supervisor | 2026-09-28 | [§3.4](#34-render) D10 | ✅ `b8db83a8` |
| <a id="NC-D26"></a>NC-D26 | *Implementation decision, item 22, amending NC-D25.* The orphan catalog stays a declared exclusion on macos-user. Its input is there, but its one terminal line sends the reader to three places and none works on that backend: the boot log that holds the names (macos-user keeps none, so every name was discarded), `yolo programs ls` (it answers wrongly inside the sandbox) and `programs.autoprune` (not relayed). The step returns there once all three work on that backend. The program reconcile still runs on macos-user, since its findings are terminal warnings | 2026-09-28 | [§3.4](#34-render) D10 | ✅ `183c3854`, amended by [NC-D71](#NC-D71) |
| <a id="NC-D27"></a>NC-D27 | *Implementation decision.* `InjectLaunchFlags` takes the posture bit as a parameter, the one `LaunchFlagsFor` and `packoverlay.Collect` already take. The jail launcher passes the jail notch's (`render.ProfileFor(KindJail)`), every in-jail carrier passes its render target's, and `yolo host --` passes the host notch's. The rewrite's wording moved to `packload.LaunchInjection.DisclosureLines`, so both notches print the same sentence | 2026-09-28 | [§4](#4-the-ordered-build-list), item 20 | ✅ `bcb84bac` |
| <a id="NC-D28"></a>NC-D28 | *Implementation decision.* `config render --at host` runs `entrypoint.RenderHostPack` in observe over host apply's own inputs. The byte-identical content comes from splitting the rmw writer into a compose half and a write half (`composeRMWSurface`), as the stateful writer already was. A bare `yolo config render` outside any workspace resolves the host notch, so it previews the host apply too. The preview does NOT fetch, because a read-only surface never does (`refreshHostPacks`' rule), so "byte for byte" holds against the pack store as it stands: a branch-following pack whose upstream moved since the last fetch renders at the stored commit, and a git pack never fetched is reported as "not fetched yet", not as a refusal the apply would never make | 2026-09-28 | [§4](#4-the-ordered-build-list), item 23 | ✅ `8d39ac87` |
| <a id="NC-D29"></a>NC-D29 | *Implementation decision.* The one render loop is `planPackSurfaces` (the posture, the contributions and the census's mechanism), `writeSurfaceThrough` (the dispatch) and `renderPackSet` (the jail walk, which both the boot and `yolo check` run with their own failure handling). The jail's `yolo config render` preview stays outside it. It is a reader with no mechanism to ask: its scope leaves out the computed layer and the capture overlay, and it previews core surfaces such as `mise/config` that no pack declares | 2026-09-28 | [§3.4](#34-render), D3 | ✅ `73cbfff6` |
| <a id="NC-D30"></a>NC-D30 | *Implementation decision.* `BriefingContent` at the host notch is its confinement header alone, because every section after it describes a launch. `jailcontent.ComposeBriefingSections` assembles every destination at both notches. The host base is never empty, so yolo owns every briefing destination a selected pack declares, with or without prose, and retires one only once no selected pack declares it. That is [§6a](../reference/pack-system.md#batch-6a)'s "fully generated and controlled". `agents_md_extra` at the host is read from user scope only | 2026-09-28 | [§3.4](#34-render), D7 | ✅ `180deba3` |
| <a id="NC-D31"></a>NC-D31 | *Implementation decision, item 6.* `config.SelectPacks` (`internal/config/packselection.go`) is the one selection function: it resolves the entries in the order given and closes the selection (`packload.Selection.Close`) over what resolved. What differs between callers is an input, never a loop: the resolver (the launch stages each entry into its tree, a host verb resolves for its process, validation and the footer write nothing to the store), the entry order, whether the first failure stops the selection, the closure's profile table, an announcer for the cause lines and a resolver for each addition. The jail launch keeps its embedded-first order as its own input, since which order is right is [OQ-NC4](#OQ-NC4)'s. Every host verb calls it through `cli.selectHostPacks`, with the user-scope table, except `yolo host --` and `yolo host env`, whose table is the one profile the launch selects for the one agent it runs ([HS-D1](../design/host-notch-services.md#HS-D1)). An addition resolves through the verb's own resolver, as the bare-name entry it stands for. Both host front doors print each addition's cause line (WB-D12), and `yolo host apply` prints them in its report. A source scan (`TestEveryHostVerbSelectsThroughTheOneFunction`) fails when a host verb resolves `packs` entries or closes a selection by hand | 2026-09-28 | [§4](#4-the-ordered-build-list), item 6 | ✅ `TestSelectPacksClosesTheSelectionAndJoinsEachAddition`, `TestEveryHostVerbSelectsThroughTheOneFunction`, `TestHostLaunchAnnouncesThePacksTheClosureJoined` |
| <a id="NC-D32"></a>NC-D32 | *Implementation decision, item 6, building [NC-D5](#7-decision-ledger).* `config.LoadPackEntries` returns the `packs` entries that did not lower, each named by its place in the list (`config.packs[1]`); `LoadPacks` keeps its warning sink over it. At `yolo host --` and `yolo host env` a malformed entry, an unresolvable pack or a refused closure refuses the launch, every problem listed with its remedy, instead of composing without the pack. The launch hook refuses the same set, which revises [host-apply-staleness](../reference/host-apply-staleness.md#the-dispositions)'s "render nothing, and exec" for this one row. `yolo host apply` reports a malformed entry as an unresolvable pack, so `--assert` refuses the incomplete set: before, a list of only malformed entries read as an empty `packs` and took the branch that retires every pack's output. The read-only verbs report it beside the unresolvable packs. `undeclaredHostProfileError`'s branch naming an unusable pack beside an undeclared profile is deleted, since the launch refuses before any profile is resolved | 2026-09-28 | [§4](#4-the-ordered-build-list), item 6 | ✅ `TestHostLaunchRefusesAMalformedPacksEntry`, `TestHostApplyRefusesAPacksListOfOnlyMalformedEntries`, `TestConfigInspectionReportsAMalformedPacksEntry` |
| <a id="NC-D33"></a>NC-D33 | *Implementation decision, item 6, meeting [HS-D1](../design/host-notch-services.md#HS-D1)'s second requirement.* ES-D18's unserved-bridge refusal words a pack the closure joined as joined, quoting its cause line (*"though "wire-bridge" joined this launch (+ wire-bridge (needed by claude))"*), and never says it is in `packs`. On a bare `["claude"]` the `openai-codex` provider is in the table now, so `-p codex -- claude` refuses as that unserved bridge and no longer as a missing provider. A missing provider's pack that a selected pack's `needs` names can now reach the refusal only under a `when_bins` the launch does not meet, and both refusals say so | 2026-09-28 | [§5](#5-already-merged-and-in-flight) | ✅ the joined wording in `unservedAdapterRefusal`; the `needs` clause pinned by `TestAClaudeCodexSelectionWithoutItsProviderRefuses` |
| <a id="NC-D34"></a>NC-D34 | *Implementation decision, item 13, row A7.* `yolo host --` runs `packload.EnvOverrideFindings` over its selected packs, its one-agent profile table, the host's served set (nothing) and a lookup answering where each variable the agent receives comes from (`hostComposition.envOverrideFindings`). A certain finding refuses, an uncertain one warns, and `yolo host env` discloses both without refusing, as it discloses the credential pre-flight. The one input that differs from a jail is named: the invoking shell is a delivery at the host, since the agent inherits it, so its origin is `packload.FromLaunchEnv`; no jail backend forwards it. No `host_files` destination renders at the host, so a `host_file` override is not evaluated there (a false negative, never a false refusal; [OQ-NC8](#OQ-NC8) owns host files). **The done-when as written cannot be met, and that follows from [NC-D16](#NC-D16), not from this item:** the measured launch's aws-auth pointer names its daemon (`served_by`), which the host does not run, so the gate withholds it and nothing overrides it. That is the answer a jail gives wherever aws-auth is not served. A pointer the host does deliver refuses beside its override exactly as in a jail (`TestHostLaunchRefusesAPointerBesideItsOverride`) | 2026-09-28 | [§4](#4-the-ordered-build-list), item 13 | ✅ `TestHostLaunchRefusesAPointerBesideItsOverride`, `TestHostBearerBesideAWithheldPointerOverridesNothing` |
| <a id="NC-D35"></a>NC-D35 | *Implementation decision, item 13, row A8.* `config.ValidateProviderSection` is ValidateConfig's provider and profile section over one config with no workspace: the `providers` entries, the retired `agent_profiles` key, `use_profiles`, and the user file's `profiles` and `adapters`, in ValidateConfig's order and words. `validateProviders`, `validateProfiles` and `validateAdapters` each split into that user half and their workspace-scope refusal, which the host does not run, since it reads no workspace config. The host runs it after ES-D5's refusal, which keeps the remedy it adds. This retires ES-D9's typed-`-p` exemption: a `use_profiles` key every jail launch refuses now refuses a typed `-p` at the host too | 2026-09-28 | [§4](#4-the-ordered-build-list), item 13 | ✅ `TestValidateProviderSectionIsValidateConfigsSectionWithoutTheWorkspace`, `TestHostLaunchRefusesAProfileEntryValidationRefuses` |
| <a id="NC-D36"></a>NC-D36 | *Implementation decision, item 13, row A7.* `packload.ProfileDisclosures` is the profile line, one per distinct selected name, for the jail's `noteUseProfiles` and for both host front doors, over the host launch's one-agent table and its selected packs. **Revised 2026-09-29:** the line reads the composition the gate built (`packload.ProfileDisclosureInput`: the table, the packs, the resolved profiles, the composed providers and a per-agent "reaches" lookup) and says, per agent keyed to the name, the provider it resolved to and the route it reaches it by, with a warning, naming why and the fix, where a platform provider no selected pack binds for the agent configures nothing for it, or where no credential variable its no-endpoint provider claims reaches it. The every-pack received list is gone, because it held for every launch and so said nothing: it listed fifteen receivers for a `yolo host -- pi` that started with no model and no key ([`providers.md`](../reference/providers.md#what-the-launch-checks-and-prints)) | 2026-09-28 | [§4](#4-the-ordered-build-list), item 13 | ✅ `TestHostLaunchSaysWhereItsProfileLanded`; the per-agent revision pinned by `TestNoteUseProfilesSaysWhatEachAgentsSelectionReaches` |
| <a id="NC-D37"></a>NC-D37 | *Implementation decision, item 15, row C6.* The host's OpenAI prelaunch reads `YOLO_AUTH_PRELAUNCH_<BIN>_FLAG` and `_LOGIN` from the launch's composed variables (`hostComposition.prelaunch`), spelled by `openaiauthhost.PrelaunchVar` exactly as the jail's launcher spells them; a test pins the two spellings together. `openaiauthhost.Prepare` does nothing when nothing is declared. It serves a declared view the host's way: the pi view as the host credential socket, since a jail's pi auth file is the user's own file at the host; the codex view as a managed `CODEX_HOME` under `host-agents/<declaring pack>` (`EnvFoldEntry.Pack` carries the declarer; the shipped pack's path is unchanged); a login-only prelaunch proves the login and serves nothing; any other view refuses by name. With no login it starts the browser login only when stdin is a terminal (the host gate's own probe, `hostGateCanPrompt`); off one it prints the jail launcher's two lines and runs the command without the credential | 2026-09-28 | [§4](#4-the-ordered-build-list), item 15 | ✅ `TestHostPrelaunchIsWhatThePackDeclares`, `TestPrepareNeverLogsInWithoutATerminal`, `TestManagedCodexHomeIsKeyedOnTheDeclaringPack` |
| <a id="NC-D41"></a>NC-D41 | *Implementation decision, NC-D17's follow-up.* A **served address** (coined here, and in `internal/packload/served.go`) is the loopback `host:port` a jail daemon, a service adaptation or a via route answers at in one launch. A loophole's jail daemon declares its address once, as `jail_daemon.listen` (a loopback IP literal and a port), and its argv spells it `{listen}` (`loopholedecl.TokenListen`). The two are refused apart, and `{listen}` is refused in a field the host resolves. A pack `env` value spells it `{listen}` too, legal only beside `served_by`: it is the one interpolation an env value takes, and it resolves to the served address of that daemon. The shipped pointers now read `http://{listen}/oauth/token` (codex) and `http://{listen}/credentials` (aws-auth), so the port is written once. `packload.ServedDaemons` carries where each daemon serves (`WithListen`) and the declared-to-served map for the pack services' adapter and via addresses (`WithRebind`). The payload writer resolves the argv from it (`JailDaemonSpec.ResolvedCmd`), the credential gate resolves the pointer (`servedFold`), and the provider table and via base move with it (`adaptEndpoints`, `ViaServedAt`). A user's `adapters` override is not a declared address and never moves. A pointer whose daemon declares no address is withheld and named. The footprint describes declarations, so it prints `{listen}` where it printed the port; the launch banner names the served address ([NC-D46](#NC-D46)). `yolo check` predicts every daemon at its declared address | 2026-09-28 | [§2.4](#24-the-addresses-those-secrets-protect-are-composed-not-literal) | ✅ `TestAListenPointerComposesItsDaemonsServedAddress`, `TestThePayloadResolvesTheListenToken`, `TestCheckPredictsEachDaemonAtItsDeclaredListenAddress` |
| <a id="NC-D42"></a>NC-D42 | *Implementation decision.* **Only a shared network namespace moves an address** (`sharesLauncherNetns`, its fourth reader). A bridged jail's served addresses are its declared ones, through the same served set, so its argv, pointers and provider table are byte-identical to before. Ephemeral ports everywhere was not taken: it would change every bridged jail's `ANTHROPIC_BASE_URL` on every launch, and the launcher's loopback is not a bridged jail's, so a port it found free proves nothing there. macos-user shares the host's loopback but runs no jail daemon, so it picks nothing ([OQ-NC1](#OQ-NC1) decides whether it runs them). The terminator's `:443` stays, because `--add-host` maps `platform.claude.com` to it ([OQ-NC2](#OQ-NC2)) | 2026-09-28 | [§2.4](#24-the-addresses-those-secrets-protect-are-composed-not-literal) | ✅ `TestAPrivateNamespaceLaunchKeepsEveryDeclaredAddress`, `TestASharedNamespaceLaunchMovesEveryServedAddressTogether` |
| <a id="NC-D43"></a>NC-D43 | *Implementation decision.* **The launcher picks each port** by binding port 0 on the declared loopback host, holding every socket until all are picked so no two collide, and, since [NC-D69](#NC-D69), until the process that serves the port has it. On a shared namespace that loopback is normally the jail's, so the kernel's answer is a port free where the daemon will bind. ⚠ Not on a macOS podman machine: there `network.mode: "host"` joins the VM's network namespace, not the Mac's, so the port is picked on a loopback the daemon never binds. The pick still separates two such jails from each other's declared ports, but it proves nothing about the VM's loopback. `sharesLauncherNetns` has the same blind spot, so what the daemons advertise there is unverified too. The container test of this item runs on Linux podman only. A process outside the launch that binds the port between its release and the daemon's bind wins it, and that fails closed, since the daemon cannot bind and its clients are refused for the wrong caller token by whatever holds the port. Picks are settled once per process, like the caller tokens, so the two compositions one launch runs agree. A failed pick keeps the declared ports and says so | 2026-09-28 | [§2.4](#24-the-addresses-those-secrets-protect-are-composed-not-literal) | ✅ `TestASharedNamespaceLaunchHoldsEveryPortItPickedUntilItReleasesThem`, `TestOneLaunchComposesOneSetOfServedAddresses` |
| <a id="NC-D44"></a>NC-D44 | *Implementation decision.* **An attach re-reads the running jail's served addresses and never picks.** The launch writes the declared-to-served map into the `0600` channel section as `YOLO_SERVED_ADDRESSES` (`paths.ServedAddressesEnv`), only when it moved something. An attach adopts that map in `rekeyChannelForAttach`, replacing its own picks, and composes the channel again when it differs, exactly as it adopts the caller tokens. A jail that recorded none serves its declared addresses: it was bridged, or launched by a yolo older than this. The reader keeps only loopback pairs, because the jail can write the file | 2026-09-28 | [§2.4](#24-the-addresses-those-secrets-protect-are-composed-not-literal) | ✅ `TestAnAttachComposesForTheRunningJailsServedAddresses`, `TestParseServedAddressesKeepsOnlyLoopbackPairs` |
| <a id="NC-D45"></a>NC-D45 | *Implementation decision.* **A pack service takes no `{listen}`.** Only a loophole's jail daemon declares a listen address (`jail_daemon.listen`). A service answers at its adapters' `address` and its `via_address`, which the launcher moves itself, so `{listen}` in a service's `jail_daemon.cmd` or `host_daemon.cmd` would resolve to an empty string. Decode refuses it there, and refuses an `env` value naming `{listen}` whose `served_by` is a service the same manifest declares. A pointer `served_by` another pack's service cannot be decided at decode, so the launch withholds it and names it, saying that only a loophole declares a listen address | 2026-09-28 | [§2.4](#24-the-addresses-those-secrets-protect-are-composed-not-literal) | ✅ `TestAServiceDaemonRefusesTheListenToken`, `TestAPointerServedByAServiceRefusesTheListenToken`, `TestAListenPointerComposesItsDaemonsServedAddress` |
| <a id="NC-D46"></a>NC-D46 | *Implementation decision.* **The launch banner prints a pointer's served address, not `{listen}`.** A launch disclosure describes what is about to run on this machine, which is why it already resolves `{state}`. So the banner's env lines resolve `{listen}` through the served set the launch composed the pointers from (the channel's `served`), and both call sites pass the channel. A bridged launch's banner is byte-identical to before served addresses, and a shared-namespace launch's banner names the picked port. Where the daemon is not served at this notch (macos-user, or the loophole off), the line keeps `{listen}`, and the launch names that pointer as withheld. `yolo pack footprint` stays machine-independent and keeps the token | 2026-09-28 | [§2.4](#24-the-addresses-those-secrets-protect-are-composed-not-literal) | ✅ `TestLaunchBannerNamesThePointersServedAddress`, `TestEveryBannerCallSitePassesTheLaunchChannel` |
| <a id="NC-D47"></a>NC-D47 | *Implementation decision, item 30, building [OQ-NC3](#OQ-NC3) A.* The **network primitive** (the ruling's term: whether an environment's network namespace is the host's) is a field of `render.Profile` read through `SharesHostNetwork` and set by `WithSharedNetwork`, not a `Primitive`: it enforces nothing, and the `Primitive` list is what `yolo describe` and the briefing print under "Enforced by". The host preset and the Seatbelt preset share by construction; the jail preset is private, and a launch sets it from `sharesLauncherNetns`, the predicate the assembler and the advertised addresses already read (`jailcontent.LaunchProfile`). So `network.mode: "host"`, a nested podman and every macos-user jail share, and Apple Container never does. The Linux guest preset stays private until that backend is built. Autonomy does not move. The launch prints one sentence, `render.SharedNetworkFact`, above the backend dispatch, so the container arm, an attach and macos-user all print it from one call site. The briefing prints the same sentence under the network line, from the applied network mode | 2026-09-28 | [§4](#4-the-ordered-build-list), item 30 | ✅ `9a5c5b68` |
| <a id="NC-D48"></a>NC-D48 | *Implementation decision, item 29, building [OQ-NC8](#OQ-NC8) A.* A source-less `host_files` entry at the host goes through the one render loop, not a fifth one: `planHostFileSurfaces` plans each entry with `planSurface`, and `renderHostPlans`, the body `RenderHostPack` ran, renders it. `RenderHostPack` and `RenderHostUserFiles` are its two callers (`TestEveryPackSurfaceWriterRunsTheOneLoop`). The surface declares `stateful`, so the contract's census picks the mechanism as it does for a pack surface: `rmw` under `assert`, `stateful` under `own`, nothing under `none`. The entries are read from user scope only, as `agents_md_extra` is ([NC-D30](#NC-D30)). Their owner is a pack value with no root and no declaration, so no derive runs, and no host derive inputs are passed. A destination a selected pack also composes refuses the apply before anything is written, which is the jail's two-writers refusal at the host | 2026-09-28 | [§4](#4-the-ordered-build-list), item 29 | ✅ `7f77a618` |
| <a id="NC-D49"></a>NC-D49 | *Implementation decision, item 29.* At the host the entry's `content` is not a host layer, because the file's own content already is one (the `rmw` read, or `own`'s adoption). It is decoded with the entry's codec and folded into one of the entry's own layers, chosen by whether an edit to the file survives the next render in that mode: `once` and `capture` keep edits, so the content fills `defaults`; `copy` and `readonly` do not, so it fills `managed`. The entry's own `defaults` and `managed` win over the content key by key. A keyless codec (`raw`, `lines`) is refused by name, as a keyless pack surface is. File permissions are not set at the host: `readonly`'s 0444 and a source's execute bit are rules for a jail's home | 2026-09-28 | [§4](#4-the-ordered-build-list), item 29 | ✅ `7f77a618` |
| <a id="NC-D50"></a>NC-D50 | *Implementation decision, item 29.* When an entry leaves the config, the next apply finds it by its provenance record, `user-<slug>.provenance`. The destination comes from inverting the slug (`config.HostFilePathFromSlug`; the slug is a reversible escape of the path). The codec is the first object codec the file decodes as, starting from the one its extension names. The keys go by the revert's own walk, factored out as `withdrawHostSurface`: only keys the record attributes to a layer yolo wrote, never one recorded `host`, and an emptied file stays. Each key is named, the dry run says "would remove", and there is no prompt and no archive: the user declared the keys and removed the declaration, and an entry put back writes them again. Under `own`, the entry's capture files are deleted with the record. `yolo host apply --revert` now walks these records too | 2026-09-28 | [§4](#4-the-ordered-build-list), item 29 | ✅ `7f77a618` |
| <a id="NC-D51"></a>NC-D51 | *Implementation decision, item 29.* A source-bearing entry, which is also every directory entry, and a non-empty `mise_tools` are named in host apply's notch line as config that does not apply at the host. Source-bearing entries are listed by destination. This happens in the default view, in `--verbose`, and in the branch with no packs configured, which renders host_files too. `yolo config render user --at host` previews each source-less entry, as item 23 previews pack surfaces | 2026-09-28 | [§4](#4-the-ordered-build-list), item 29 | ✅ `7f77a618` |
| <a id="NC-D52"></a>NC-D52 | *Implementation decision, item 25, row D6.* **One layer writer at both notches.** The jail writes its pack layers with `hostskills.ComposeInto`, the host render's own `writeLayer` over a directory yolo owns wholesale: no ownership record, no adoption, no archive, only the run's claim set, so every host rule that reads a record is inert there. `jailcontent.copySkillSubdirs` and its deref copier are deleted, and `jailcontent` now imports `hostskills`. A pack's layer identity (name, description, tier, wrapped plugins, the map back to its source) has one constructor, `hostskills.PackLayer`, which `ComposeHostSkills` and the jail's records (`run.jailSkillSources`, for the launch and for an attach's adopted packs) both read. The jail's plan is the host's type (`jailcontent.SkillPlan` → `hostskills.Destination`), one layer per pack per destination. `onecomposer_test.go` in both packages pins the callers | 2026-09-28 | [OQ-NC11](#OQ-NC11) | ✅ `45c53ed1` |
| <a id="NC-D53"></a>NC-D53 | *Implementation decision, item 25.* **The collision pre-flight** is `run.checkSkillCollisions`, in `stagePacksInto` directly after the agent-name one: `hostskills.Collisions` over the jail's own plan (its targets and its fan-out, every tier), printing `hostskills.CollisionError`. Every invocation stages before its attach decision, so an attach refuses too. A destination is named `~/<into>`, the path the agent reads, and a claim names the source the user edits (`packload.Pack.SourcePath`). `PrepareSkillsWith` checks the whole plan again before it touches any destination, so an attach adopting a running jail's colliding tree refuses its refresh rather than composing one staging dir and not the next. The jail marks no layer unresolved: a missing `from` stays a warning and the rest composes, as before | 2026-09-28 | [OQ-NC11](#OQ-NC11) | ✅ `45c53ed1` |
| <a id="NC-D54"></a>NC-D54 | *Implementation decision, item 25.* **The order is kept by writing the packs first.** The host's writer composes the pack layers into an empty scratch dir; the built-in suite then fills only the names the packs left, the LSP plugin is written over everything, and the workspace fills what is left last. That is the precedence the jail had (workspace < built-in < packs, the local pack last), reached without handing the host's writer a directory it did not compose, where a built-in would read as an entry it has no claim on. The local pack's override of a shared pack's same-named skill is now a collision, as at the host | 2026-09-28 | [OQ-NC11](#OQ-NC11) | ✅ `45c53ed1` |
| <a id="NC-D55"></a>NC-D55 | *Implementation decision, item 25.* **The reserved child is withheld at write time in a jail.** `ComposeInto` removes a top-level name a destination reserves (packs/claude's `synced`) whichever layer wrote it and returns an `ActionReserved` result naming the pack and the tree. The jail's composition keeps the built-ins and the workspace off that name too. The host keeps its fence at adoption, where its damage is, and its render is unchanged. A reservation names children of the destination that declares it, so a local pack's adopted `synced/` still reaches a destination that reserves nothing ([synced-skill-trees.md §3.3](../design/synced-skill-trees.md#33-why-nothing-caught-it)) | 2026-09-28 | [OQ-NC11](#OQ-NC11) | ✅ `45c53ed1` |
| <a id="NC-D56"></a>NC-D56 | *Implementation decision, item 25.* **Wrapped plugins travel with their source.** A plugin inside a skills source rides that source and its audience; a wrap-in-place plugin (the pack root, which the jail never delivered) rides every source of its pack, once per destination. At the flat default a wrapped plugin's skills arrive and its other components are refused by name, as at the host, where the jail used to copy the plugin's directory whole. What the writer refused or withheld prints on stderr at every invocation as `Skills: …` lines beside the workspace layer's (`WorkspaceSkillsReport.PackNotices`), under the no-quiet-mode rule | 2026-09-28 | [OQ-NC11](#OQ-NC11) | ✅ `45c53ed1` |
| <a id="NC-D57"></a>NC-D57 | *Implementation decision, item 8, building [OQ-NC4](#OQ-NC4) A.* `config.PackSelection.Packs` is the one precedence order: the resolved entries in config order, then the closure's additions in joining order, then the packs an `Implicit` entry resolved to (the conventional local pack) last. The order is the function's, not an input: `SelectPacks` records which configured packs are implicit and `Packs` places them, so a caller handing entries over in another order still gets this one. `Configured` keeps its meaning (the entries' packs in arrival order, the local pack included), so a host verb that names what the config lists is unchanged. An explicit `packs` entry that takes the name `local` is a config line and keeps its place. The launch's embedded-first reorder is deleted, and every other reader already read `Packs`. Pinned at the three call sites over one fixture, whose three old orders all differed from the ruled one (`TestTheLaunchOrdersPacksAsTheConfigListsThem`, `TestTheBootReadsThePackOrderTheLaunchRecorded`, `TestHostVerbsOrderPacksAsTheConfigListsThem`) | 2026-09-28 | [§4](#4-the-ordered-build-list), item 8 | ✅ `ea5b12b0` |
| <a id="NC-D58"></a>NC-D58 | *Implementation decision, item 8.* The pack tree's record (`_pack-tree.json`) moves to `packload` (`WritePackTreeRecord`, `ReadPackTreeRecord`), because the boot reads it and `internal/entrypoint` cannot import the run pipeline. The launch writes it from `Packs`, and the boot (`entrypoint.LoadJailPacks`) and an attach (`run.loadPackTree`) read it through the one reader. **The boot takes only the ORDER from it**: a pack is still named by its staged directory, as the jail always named it (`Pack.StagedSlug`, which the host also keys what it hands the jail on), so a pack whose name and slug differ keeps its in-jail name. A tree with no record, staged before the record existed or built by hand, is read as before, directory by directory. A record that is malformed or names a path outside the tree refuses the boot (A12) | 2026-09-28 | [§4](#4-the-ordered-build-list), item 8 | ✅ `ea5b12b0` |
| <a id="NC-D59"></a>NC-D59 | *Decision, item 8, by the orchestrator on 2026-09-28 as a consequence of [OQ-NC4](#OQ-NC4) (it first shipped as an open point in `ea5b12b0`).* **A sole-owned claim two packs declare is held by the LATER declaration**, the same "later wins" every other key follows: the last in the one pack order (`config.PackSelection.Packs`), then declaration order inside a pack. One rule, `packload.laterWins`, and every composer that keeps one claim per key asks it: `Adaptations` (an adapter pair), `ComposeProviders` (a provider name), `packShippedProfiles` (a profile name), the two provider attributions (`requiredProviders`, `providerShipper`), and `packload.ServiceNamed` (a service name, which the wire bridge's endpoint argument, `serviceEndpointEnvArgs`, took the first hit of), and the jail-daemon payload, `launchservice.ServiceJailDaemons` (the one service composer the launch's `run.serviceJailDaemons` and `yolo check`'s prediction both read; `packload.ServiceJailDaemonNames` is test-only since 2026-10-04), through `packload.HeldServices` (one daemon per service name, the later pack's, so the daemon that runs is the one whose endpoint `serviceEndpointEnvArgs` names; the launch prints a yellow line naming the pack whose declaration it set aside, `noteShadowedServices`; the payload used to keep every declarer, starting two daemons that raced for one endpoint file). The others kept the FIRST before, so "a caller that skipped the pre-flight degrades to a stable table"; the order is now one well-defined order, so later is as stable, and the local pack's adapter beats one a pack pulled in through `needs` declares (wire-bridge's `openai` → `anthropic`). The pre-flights are unchanged: a jail launch refuses a provider name two packs ship and a loophole name two packs ship, and `yolo pack footprint` reports every duplicated sole-owned claim. No launch refuses a duplicated adapter pair, profile name or service name. Checked and left as they are: `briefingDestinations` keeps the first pack's `after` for a briefing path, but briefing is a concat kind, not a sole-owned claim; a manifest's own repeated content source (`governors`) is one pack's; the rest are set de-duplications. Pinned by `internal/packload/laterwins_test.go`, `TestComposeProvidersKeepsTheLaterOnANameClash`, `TestADuplicatedServiceNameStartsOnlyTheLaterPacksDaemon`, and `TestHostStillComposesAnAdapterNoPackServiceServes` restored to its conventional-local-pack fixture; each fails under first wins | 2026-09-28 | [§4](#4-the-ordered-build-list), item 8 | ✅ `ea5b12b0`, the rule `48deb7bf` |
| <a id="NC-D60"></a>NC-D60 | *Implementation decision, item 5, building [OQ-NC9](#OQ-NC9) A.* The pack's ORIGIN decides whether its symlinks are followed, never the caller: `config.followsSymlinks` is true for a local (`file://`) entry, the conventional local pack included, and false for a fetched or embedded one. `ResolvePackSpec.FollowLocalSymlinks` is deleted, and so is the split it kept. A local pack is followed in STAGE and DECLARATION modes and **filtered or not**: the ruling names local packs without exception, and the filters apply to what the links deliver. So the launch, `yolo check`, `yolo pack explain`, the host verbs, the footer, config validation, `UseProfileCLINames` and the lazy resolvers give one answer. The jail's tree holds each link's target as a plain file, since the dotfiles repo is not in the jail. A dangling link and a link loop still refuse. The workspace skills reader (`jailcontent`'s confined reader) is a different tree and rule, and is untouched. Pinned at the launch by an rcm-shaped conventional local pack and a stow-shaped filtered one (`TestStagePacksFollowsADotfileManagersLinksInALocalPack`), a fetched pack's link still refused there (`TestStagePacksRefusesAFetchedPacksEscapingSymlink`), and at `yolo check` and the host resolver | 2026-09-28 | [§4](#4-the-ordered-build-list), item 5 | ✅ `81d16986` |
| <a id="NC-D61"></a>NC-D61 | *Implementation decision, item 5.* `yolo pack lint <dir>` and `yolo pack footprint <dir>` keep the no-escape rule. They read a directory named on the command line, not a pack the user config names, which is the only thing [OQ-NC9](#OQ-NC9) A extends, and lint's rules are the ones a published copy of the pack would meet, where an escaping link is refused. A dotfile-deployed local pack therefore lints with a refusal it does not meet at a launch; `yolo pack explain <name>` resolves the configured entry and follows | 2026-09-28 | [§4](#4-the-ordered-build-list), item 5 | ✅ `81d16986` (no change) |
| <a id="NC-D62"></a>NC-D62 | *Implementation decision, item 11, row A6, as [OQ-NC5](#OQ-NC5) ruled.* "An agent CLI" is a CLI a **selected** pack installs (`selectedPackInstalls`), the set a jail's bare `-p` keys (`effectiveUseProfiles`). A typed `-p` (bare, glued, or the pair naming the command) for any other command is **refused**, not ignored, at `yolo host --` and `yolo host env` alike: the host composes one process, so a `-p` that reaches nothing there would be a flag that silently does nothing. The refusal runs after the declaration check, so an undeclared name keeps its own message, and names the grant spelled for the launch, `yolo host --with-credentials <the profile's provider> -- <cmd>` (the shell's `eval "$(yolo host env --with-credentials <provider>)"` at `yolo host env`), a typed grant kept and widened. ES-D1 retires. The remedies follow: a withheld line for an ad-hoc command names the grant and no profile, so it needs no declared profile; an agent's line keeps ES-D7's `-p` switch; an agent that cannot run on the profile hands the key to an ad-hoc `bash` through the grant; ES-D5's refusal names the grant. A `use_profiles` entry for an unselected shipped pack's CLI (ES-D9) is not a bare `-p` and is unchanged. The jail was already agent-only, so only its help changed | 2026-09-28 | [§4](#4-the-ordered-build-list), item 11 | ✅ `b2ca409a` |
| <a id="NC-D63"></a>NC-D63 | *Implementation decision, item 18, row C5, as [OQ-NC6](#OQ-NC6) ruled: the field list.* Refused at workspace scope, any value, `null` included: **`endpoints` in any form** (a URL, [`OQ-LM3`](../research/local-model-endpoints.md#oq-lm3)'s own; a protocol key with no URL, which an agent pairs with and then sends the provider's key to its own default service, claude's derive treating an entry that names no anthropic address as first-party; a `wire_api`, which rides in the same object; a `null` removing an endpoint or the map); **`api_key_env_name`** (a value re-points the claim, a `null` unclaims the key so every process receives it); and a **`null` provider or `null` `providers`**, which remove claims the same way. Still merged: `models`, `options`, `region` and `capabilities`. `region` is judged not to route a credential: it names a region of the provider the credential was issued for, and this plan did not measure how each agent's SDK builds a host from it. *(Corrected 2026-09-29: measured, it does route one. Claude Code and opencode 1.18.32 build `https://bedrock-runtime.${region}.amazonaws.com` by string template, so a workspace region of `attacker.example/#` sent every prompt and the Bedrock credential to a host the repo chose. `region` still merges, but only as one DNS label, refused otherwise at every scope ([`providers.md`, a region is a host-name part](../reference/providers.md#a-region-is-a-host-name-part)); a label can name a host only inside the domain the agent appends.)* `capabilities` decides features, not where a key goes. Each refusal names the field at the deepest path written and the user config to move it to. The address rule used to let a `null` pass as "removal, never steering"; removing an address or a claim steers a credential, so it no longer does | 2026-09-28 | [§4](#4-the-ordered-build-list), item 18 | ✅ `c7a3482b` |
| <a id="NC-D66"></a>NC-D66 | *Implementation decision, correcting [NC-D16](#NC-D16) for macos-user.* Since [OQ-DP8](../design/declaration-parity.md#OQ-DP8) and [OQ-DP9](../design/declaration-parity.md#OQ-DP9) macos-user RUNS jail daemons, in its Seatbelt guest, so it is no longer a notch that serves none. `packload.ServedAtRuntime` is gone: `loopholes.JailDaemonsRunIn` is the one split of a payload into what a runtime runs, and the launch's `servedDaemons` and `yolo check`'s `predictedServed` both build `packload.ServedInJail` from it. On macos-user it declines an intercepting loophole's daemon, a pack service's whose admitted host half serves its adaptation instead ([NC-D65](#NC-D65), joined through `ServedDaemons.Plus`) or which publishes an endpoint file, a Linux executable in a loophole's folder, and an argv naming a container path the sandbox has no copy of; every other pack service's daemon, and a `{jail_loophole_dir}` program, runs in the guest ([JD-9, JD-10](../design/jail-daemon-on-macos-user-plan.md#JD-9), 2026-10-04). Consequences: codex's `CODEX_REFRESH_TOKEN_URL_OVERRIDE` and aws-auth's `bedrock` pointer are delivered on macos-user, no longer withheld; and macos-user now picks served addresses ([NC-D42](#NC-D42)), because it shares the Mac's loopback, for the daemons it runs only | 2026-09-28 | [§2.4](#24-the-addresses-those-secrets-protect-are-composed-not-literal) | ✅ `068f8fe2` |
| <a id="NC-D65"></a>NC-D65 | *Implementation decision, item 3, as [OQ-NC1](#OQ-NC1) A ruled.* The host launch (`yolo host --`, which the wrappers exec) and the macos-user launch run a selected pack service's `host_daemon` as a launch-owned child, through one runner, `internal/launchservice`, so there is one host-side supervisor and not one per notch; it is not `yolo-jaild supervise`, whose inherited readiness pipe and jail-home log a per-launch child must not have ([`HS-D8`](../design/host-notch-services.md#HS-D8)); since [`HS-D28`](../design/host-notch-services.md#HS-D28) (2026-10-04) it does apply the declaration's restart policy, on the supervisor's backoff, restarting a service that dies mid-session on the address the launch reserved for it. The trigger is the credential gate's own `UnservedAdapterError` ([`HS-D6`](../design/host-notch-services.md#HS-D6)), the address a port the launch picked, the credential this plan's per-launch caller token (NC-D3), and the inputs one `0600` file ([`HS-D7`](../design/host-notch-services.md#HS-D7)). `WithoutServiceAdaptations` and `ViaInert` were already gone (item 2); the host's bridge refusal remains only for a service that cannot run ([`HS-D5`](../design/host-notch-services.md#HS-D5)). `yolo host env` refuses a bridged profile and `yolo host apply` renders no address for it, as [`OQ-HS3`](../design/host-notch-services.md#OQ-HS3) rules. A via stayed inert at both notches until 2026-10-05; since [`HS-D30`](../design/host-notch-services.md#HS-D30) a via or a carrier that routes an agent through a pack service starts that service's host half too, at a port the launch reserved for its via address, at `yolo host --` for an agent whose environment carries the route ([`HS-D31`](../design/host-notch-services.md#HS-D31)) and on macos-user for every profiled agent, while `yolo host env`, `yolo host apply` and the footer stay inert ([`WG-I46`](../design/wire-bridge-gateway.md#WG-I46)) | 2026-09-28 | [§4](#4-the-ordered-build-list), item 3 | ✅ the mechanism, `TestStartWaitsForReadinessAndStopEndsTheService` and `TestTheHostHalfServesFromItsInputAndPublishesNoFile`; the host launch, `TestHostCodexClaudeRunsThroughALaunchOwnedBridge`; macos-user, `TestTheMacosUserArmStartsTheServiceAndStopsItAfterTheCommand` |
| <a id="NC-D64"></a>NC-D64 | *Implementation decision, item 10, row A4, as [OQ-NC10](#OQ-NC10) ruled.* `acceptConfigChangesEnv` and every description of the variable as a grant are deleted: `yolo host --help`, `yolo config-ref`'s disposition table and wrapper paragraph, and host-apply-staleness P4, P5, its flow chart, dispositions, [OQ-HS10](../reference/host-apply-staleness.md#why-its-this-way) row and current values. The refusal of `--accept-config-changes` at the host keeps its jail-launch-flag wording and adds one line: a host launch has nothing to approve, and the first-apply MCP loss is asked at a terminal by `yolo host apply --assert`. A test sets the retired variable and proves the gate still refuses the first-apply loss off a terminal. No CHANGELOG line: nothing read the variable, so no launch behaves differently | 2026-09-28 | [§4](#4-the-ordered-build-list), item 10 | ✅ `a1812373` |
| <a id="NC-D67"></a>NC-D67 | *Decision, reconciling item 17 with [`OQ-CN9`](../reference/providers.md#oq-cn9) on 2026-09-30.* Item 17 is **built**, by the credential-scope work at `949d9430` (CN-D24), and counts here as built although its **After** cell named item 16, which is not. The macos-user arm writes the files through the container vehicle's own writer (`writeMacosUserAgentEnvFiles` over `writeAgentEnvFiles`, called from `run.Run`'s macos-user arm), so it needed nothing item 16 builds, and item 16's order, once built, moves both backends' files at once. Its done-when, left blank while it was gated, is the ruling's own words. `TestMacosUserBareLaunchWritesEveryProfiledAgentsEnvFile` pins it through `Run` to the backend's handler seam: with the arm's call deleted it fails (tried 2026-09-30). Not run on a Mac: CN-D24 names what that leaves unmeasured | 2026-09-30 | [§4](#4-the-ordered-build-list), item 17 | ✅ `949d9430` |
| <a id="NC-D68"></a>NC-D68 | *Decision, reconciling item 16 with [`OQ-CN8`](../reference/providers.md#oq-cn8) on 2026-09-30.* Item 16 **stays gated**. Row C3 named that question as the one that "picks the order", but the ruling decided one relation for one vehicle: the user's explicit value beats yolo's in the per-agent file, the launcher-sourced one. It decided neither the order among yolo's own sources, which the three vehicles answer three ways ([OQ-NC12](#OQ-NC12)'s table, MEASURED by a scratch test), nor whether the host exec, which composes over the shell, honors the user's value at all ([OQ-NC13](#OQ-NC13)). Both are filed here, because C3 is this plan's row and [`providers.md`'s credential gate](../reference/providers.md#the-credential-gate) is built with nothing open. Found while measuring: CN-D21's grammar changed the per-agent file's own order as a side effect. Its lines defer to a value already set, so the first line now wins, and a claimed `env_sources` value and a gated env value beat the shape var there, where before the shape var won, as it still does at the host. Recorded, not changed here; [OQ-NC12](#OQ-NC12) was decided on its leaning on 2026-10-04 ([NC-D72](#NC-D72)). No code moves in this row | 2026-09-30 | [§4](#4-the-ordered-build-list), item 16 | decided by [NC-D72](#NC-D72) |
| <a id="NC-D69"></a>NC-D69 | *Implementation decision, fixing a fault in [NC-D43](#NC-D43).* **A picked port stays held until the process that serves it has it.** The pick bound port 0 and let the port go at once, so the kernel could hand it to whatever bound port 0 next, the same launch included: it fronts its host services, each on a port-0 listener, between the pick and the daemon's start. The daemon's bind then failed with "address already in use", and the launch was refused. Seen as unit-test flakes; the one recorded ([test-suite-speed.md](test-suite-speed.md)) is a macos-user launch under test whose Codex doorway lost its port to the claude broker's front. Each pick is now a **reserved port**, a term coined in `internal/launchservice`'s `reserve.go`: a socket bound to the port and never listened on, so nothing else can bind it, explicitly or as a port-0 pick, and a connection to it is refused as one to a free port is. A doorway the launch opens itself takes its reservation into its plan and is handed it at its start ([HS-D26](../design/host-notch-services.md#HS-D26)). Every other one is released only immediately before the process that starts the jail's daemons: on macos-user just before the sandbox starts, once every listener of the launch's own is bound, and on a container by its keeper, which is handed them ([NC-D70](#NC-D70)). An attach releases what it picked when it adopts the running jail's map. What is left is a process outside the launch binding the port between the release and the daemon's bind, which fails closed as NC-D43 says. For a container jail that window is the container's start and its boot up to the jail-daemon supervisor (`start_jail_daemon_supervisor` is one of the boot's last steps), so what this removes from it is the launch's and the keeper's own listeners, not the boot. Closing that too would mean handing the socket across the container or sandbox boundary to every jail daemon, which is not built | 2026-10-01 | [§2.4](#24-the-addresses-those-secrets-protect-are-composed-not-literal) | ✅ `TestAMacosUserDoorwaysPickedPortIsStillItsOwnWhenAnotherListenerAsksFirst`, `TestASharedNamespaceLaunchHoldsEveryPortItPickedUntilItReleasesThem`, `TestTheMacosUserGuestDaemonsPickedPortIsFreeWhenTheSandboxStarts`, `TestAnAttachComposesForTheRunningJailsServedAddresses` |
| <a id="NC-D70"></a>NC-D70 | *Implementation decision, [NC-D69](#NC-D69) at a container launch.* **The jail's reserved ports are handed to its keeper, which lets them go just before it starts the container.** The fresh launch spawns the keeper before any host service exists, and the keeper then fronts each host service on a port-0 listener before it starts the container ([the keeper design, §9.1](../design/jail-lifetime-last-session-wins.md#91-what-starts-it)). On a shared namespace those fronts bind the loopback the jail's daemons will bind, so a launch that let its reservations go at the spawn would still have one of them handed to a front. So `startKeeper` passes each reservation's socket in `ExtraFiles` after the launch lock, named by one `--reserved-fd` each, and closes its own copies; the keeper marks them close-on-exec at once, as it does its other descriptors ([JL-D29](../design/jail-lifetime-last-session-wins.md#JL-D29)), and closes them after its host services and the credential view and before the container's main process, or at any end before that. Chosen over releasing them when the keeper reports progress, which would race the jail's boot. ⚠ What this cannot change, and a nested jail cannot show: a foreign process binding the port between the keeper's release and the daemon's bind still wins it. The nested jail is a shared namespace, so it exercises the hand-over itself (`TestTwoJailsOnOneLoopbackServeOnTheirOwnPorts`), but `network.mode: "host"` on a real rootless host is verified only there or by CI, and on a macOS podman machine the reservation is on the Mac's loopback, not the VM's ([NC-D43](#NC-D43)) | 2026-10-01 | [§2.4](#24-the-addresses-those-secrets-protect-are-composed-not-literal) | ✅ `TestTheKeeperHoldsTheJailsReservedPortsUntilItStartsTheContainer`, `TestTheKeeperReleasesTheReservedPortsAfterItsHostServicesAndBeforeTheContainer`, `TestTheKeepersArgvNamesEachReservedPortWhereTheSpawnHandsIt` |
| <a id="NC-D71"></a>NC-D71 | *Implementation decision, taken under the maintainer's 2026-10-04 delegation ("make them and build it … adjust later"); reversible. Amends NC-D26.* The orphan catalog runs on macos-user, because the three pointers NC-D26 waited for now work there. The bootstrap keeps the container's `<workspace>/.yolo/boot.log` (`attachDarwinBootLog`), where the names go. The launch relays the user's `programs.autoprune` to the bootstrap only, never to a capture. The session env file names `YOLO_PACK_ROOT` and the workspace, so in-sandbox `yolo programs ls`/`remove` read this jail (`entrypoint.JailEnvFromOS`). The workspace crosses as `YOLO_DARWIN_WORKSPACE`, the bootstrap's own variable, and not as `YOLO_WORKSPACE`: that variable says where a workspace is, while `YOLO_DARWIN_WORKSPACE` says the home was built by the darwin bootstrap, whose Env translation the verbs must repeat. The catalog step's exclusion is deleted; `TestTheMacosUserBootRunsItsStepsInOrder` pins its slot. Because that bootstrap runs outside Seatbelt and each directory the catalog reads is reached through the agent-writable sidecar, the catalog reads and autoprune unlinks only beneath roots opened on those directories, and a symbolic link on the way is named on the terminal and stops that boot removing anything ([program-delivery.md OQ-PD28](../design/program-delivery.md#decision-ledger)); and the bootstrap takes no `YOLO_` name from the session env file, so an `env_sources` value cannot turn autoprune on ([OQ-PD29](../design/program-delivery.md#decision-ledger)). Unmeasured on a Mac | 2026-10-04 | [§3.4](#34-render) D10 | ✅ Built 2026-10-04 |
| <a id="NC-D72"></a>NC-D72 | *Implementation decision, taken under the maintainer's 2026-10-04 delegation ("make them and build it … adjust later"); reversible. [OQ-NC12](#OQ-NC12) decided on its leaning A.* **One ordered env composition, which every vehicle serializes** (item 16's first half). `packload`'s `envcompose.go` composes one entry per name, lowest to highest: the pack env fold (`EnvFold`, unchanged inside), then the `env_sources` the process receives and the `env_sources` removals, then the agent's shape vars (the derive's output, the region fill, tombstones). `CredentialScope.EnvFor(agent)` and `SharedEnv()` are the two answers. The host exec (`hostComposedVars`), the shared file (`writeUserEnvFile`, one line per name: def-form for an `env_sources` winner, plain for a fold winner), the per-agent file (`agentEnvFileContent`: only names whose winner differs from the shared file's, in CN-D21's grammar), the macos-user session env (`launchEnv`: the composition, then the wire tables), `DeliveredTo`, `deliverySource` and `yolo check`'s override prediction all read it. **The null rank:** an `env_sources` null ranks with `env_sources`, so it removes the shell's value (host) and the fold's value (every notch) and never a shape var. That keeps a derive's address paired with its credential (at the host a null of `ANTHROPIC_BASE_URL` used to leave claude zai's token and no zai address), and closes [BR-D26](../design/bedrock-plumbing.md#BR-D26)'s gap (a null removing the filled region). The jail now receives the nulls (`config.HydrateEnvSources`), carried as nil values on the channel's hydration so an attach's recomposition keeps them. The host's relation to the shell is unchanged pending [OQ-NC13](#OQ-NC13), and is one named input, `hostHonorsIncomingValue`: switched on, it leaves a value the shell holds in place of yolo's assignment and of a shape tombstone, and keeps an `env_sources` null, the user's own removal. **The wire tables stay the vehicle's, after the composition at every vehicle**, so no pack `env`, `env_sources` value or null replaces or removes one (the host drops a composed entry under a table's name; the shared file writes no fold line for one; the per-agent file writes no line for one). This amends [FT-D3](../design/agent-footer.md#FT-D3), which put the host's tables before the removals, so a null of `YOLO_USE_PROFILES` handed the agent its parent launch's table or none, against [FT-D2](../design/agent-footer.md#FT-D2)'s every launch setting all three. **In the per-agent file, a name the agent and every process answer alike** (an unclaimed `env_sources` value, a null, a fold winner) gets no line. So an attach, the one delivery that knows the container's frozen environment, leaves a frozen value where the jail shell keeps it instead of giving the agent a second winner (the first build did, found in review: on an attach the agent took `EDITOR=vim` from `env_sources` while the jail shell kept the argv's `EDITOR=cat`). An agent started by another agent keeps that agent's value of such a name, as before. A second review build wrote a line there to override the other agent's value, which put the shared value in the agent's file; the macos-user MCP view reads the shared value back as the `case` guard value no agent's file sets, so with two profiled agents it found none and left a server gated on the name out of the shared table (measured: `K2` set by one agent's derive, `env_sources` `K2` for every other process). Withdrawn: no agent's file sets a name to the shared value. MEASURED at `6dede33a4` over one fixture (a pack gating `K1`, setting `K4`/`K5` statically, a derive setting `K1`, `FXP_KEY`, `K2`, `K6`; `env_sources` assigning claimed `FXP_KEY`, `K2`, `K4` and nulling `K5`, `K6`), the winner per vehicle before the change: <br>per-agent file (podman and Apple Container): K1 gated, FXP_KEY env_sources, K2 shape, K4 fold, K5 fold, K6 shape;<br>bare jail shell: K2 env_sources, K4 fold, K5 fold;<br>macos-user `yolo -- fxa`: K1 shape, FXP_KEY env_sources, K2 env_sources, K4 env_sources, K5 fold, K6 shape;<br>macos-user login shell, then the agent's file: K1 gated, FXP_KEY env_sources, K2 shape, K4 env_sources, K5 fold, K6 shape;<br>host exec: K1 shape, FXP_KEY shape, K2 shape, K4 env_sources, K5 removed, K6 removed.<br>After, at every vehicle: K1, FXP_KEY, K2 and K6 shape; K4 env_sources; K5 removed (a bare jail shell: K2 and K4 env_sources, K5 removed) | 2026-10-04 | [§4](#4-the-ordered-build-list), item 16; row C3 | ✅ Built 2026-10-04 (`CredentialScope.EnvFor`; pinned by `TestTheCompositionRanksShapeOverEnvSourcesOverTheFold` and the env-winner parity tests in `internal/cli` and `internal/cli/run`, each vehicle's old order mutation-checked; the tables by `TestTheHostExecKeepsTheWireTablesOverEnvSources` and `TestTheJailVehiclesKeepTheWireTablesOverEnvSources`; the attach by `TestAnAttachLeavesASharedWinnerWhereTheJailShellHasIt`; the second agent by `TestAnAgentFileWritesNoLineForAWinnerEveryProcessShares` and `TestTheMacosUserAgentFilePairForTheMCPView`) |
| [OQ-NC13](#OQ-NC13) | **Maintainer ruling, A, against the leaning:** at `yolo host` what yolo composes (profiles, `env_sources`) wins over the user's shell, which has no say over a composed name; the host keeps the jail's boundary, where `env_sources` is the way in | 2026-10-05 | [§6](#6-open-questions) | ✅ today's host behavior |
| [OQ-NC12](#OQ-NC12) | **Maintainer ruling, A, plus a disclosure:** the profile wins, then `env_sources`, then the pack default, at every vehicle, and every launch names a value it shadowed and its source | 2026-10-05 | [§6](#6-open-questions) | the order: the parity build (B17); the disclosure: pending |
