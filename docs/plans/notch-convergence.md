---
title: "Notch convergence — one code path per concern, and the loopback services that assumed a boundary"
date: 2026-09-28
status: in-review
tags: [plan, notches, host, parity, security, loopback]
summary: "Five audits found that the host notch, the container jail and macos-user reach one declaration through different code for flags, pack selection, providers, env order and render, and that the loopback services serving credentials are safe only while the jail has its own network namespace. The plan: one description, one code path per concern, the notch choosing only the confinement. Caller authentication comes first, because network.mode host and macos-user already share the host's loopback. Then an ordered build list: pure merges that need no ruling, and behavior changes gated on this plan's own questions and on questions other docs own."
---

# Notch convergence — one code path per concern, and the loopback services that assumed a boundary

**Status:** DECIDED, 2026-09-28 — owed: **work.** The thesis and every pure merge in the build list
are ruled by the maintainer's 2026-09-27 words ([§1](#1-the-thesis)). Caller authentication for the
loopback services is ruled the same day and comes first ([§2](#2-security-first-the-boundary-that-is-not-one)).
The wire-bridge part of it is being built elsewhere. The word is `DECIDED` although 💬 questions
are open. Each one gates a single build item, and none of them gates the thesis or blocks the items
before it, so this is [the tie-breaker](README.md#the-vocabulary--seven-words-and-the-word-names-what-is-owed)
applied to per-item gates. That judgement is stated here so it can be checked. Each item's row in
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

**Needs your ruling:** [OQ-NC2](#OQ-NC2), reopened for a fix without hosts-file interception. Also ruled 2026-09-28: [OQ-NC3](#OQ-NC3) (keep autonomy, disclosed) and [OQ-NC10](#OQ-NC10) (retire the unread variable). Ruled 2026-09-28: [OQ-NC1](#OQ-NC1) (run services at every notch), [OQ-NC6](#OQ-NC6) (credential-routing provider fields are user-scope only), [OQ-NC7](#OQ-NC7) (host claude keeps its own login for now), and by parity [OQ-NC4](#OQ-NC4), [OQ-NC5](#OQ-NC5), [OQ-NC8](#OQ-NC8), [OQ-NC9](#OQ-NC9), [OQ-NC11](#OQ-NC11).

**Reads with:** [`wire-bridge.md`](../reference/wire-bridge.md#wb-d4) (WB-D4, the premise
[§2](#2-security-first-the-boundary-that-is-not-one) retires),
[`declaration-parity.md`](../design/declaration-parity.md#1-the-principle-and-what-it-does-not-say)
(the principle this plan executes),
[`credential-sources-separation.md`](../design/credential-sources-separation.md#10-decision-ledger)
(ES-D1 to ES-D27, the host credential decisions several items revisit),
[`host-computed-layer.md`](../design/host-computed-layer.md) (derives at the host,
[`OQ-HC1`](../design/host-computed-layer.md#OQ-HC1) to [`OQ-HC3`](../design/host-computed-layer.md#OQ-HC3)),
[`provider-credential-scope.md`](../design/provider-credential-scope.md#7-decision-ledger)
([`OQ-CN7`](../design/provider-credential-scope.md#OQ-CN7) to
[`OQ-CN9`](../design/provider-credential-scope.md#OQ-CN9)).

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
  [`OQ-WS5`](../design/workspace-skills.md#OQ-WS5)'s, ruled out of v1.
- **It does not revisit [`OQ-DP3`](../design/declaration-parity.md#decision-ledger).** A config value
  of `confinement: host` keeps refusing a launch. Only the `--at host` spellings converge
  ([§4](#4-the-ordered-build-list), item 10).
- **It does not build the guest notch.** Every "one composer" here takes the notch as an input, so
  guest becomes one more value when env-manager Phase 7 lands.
- **It does not re-rule questions other docs own.** Where the right merge depends on one, the item
  is gated on that question by link: [`OQ-CN8`](../design/provider-credential-scope.md#OQ-CN8),
  [`OQ-CN9`](../design/provider-credential-scope.md#OQ-CN9),
  [`OQ-ES5`](../design/credential-sources-separation.md#OQ-ES5)'s jail half,
  [`OQ-AS3`](../research/agent-safehouse.md#OQ-AS3), [`OQ-HC1`](../design/host-computed-layer.md#OQ-HC1),
  [`OQ-HC3`](../design/host-computed-layer.md#OQ-HC3) and
  [`OQ-CO15`](../design/config-ownership-and-promotion.md#oq-co15).

## 2. Security first: the boundary that is not one

### 2.1 What trusts the network namespace today

Every row below is a loopback listener yolo starts. Each one answers whoever reaches it, and each
one's own code or doc says the network namespace is what protects it.

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
| AWS credential adapter | `AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE`, pointing at the `0600` in-jail file the boot writes, `/run/yolo/caller-tokens/YOLO_SERVICE_AWS_AUTH_TOKEN` ([NC-D14](#NC-D14)). The AWS SDKs' container-credentials provider reads it and sends its contents as `Authorization`; which agents' SDKs read the `_FILE` form is UNMEASURED per agent (none may be started in a test) | A constant-time compare of `Authorization`. This is [`OQ-CN7`](../design/provider-credential-scope.md#OQ-CN7)'s option (c), extended from aws-auth to every notch. **Built** (`a5fd280f`) |
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

**Built 2026-09-28** (`8805439f`, [NC-D16](#NC-D16); `660f836d`, [NC-D41](#NC-D41) to
[NC-D44](#NC-D44)). The pointer's contribution names the daemon that serves it (`served_by`),
and the credential gate withholds it and says so wherever that daemon does not run. The address
is no longer a literal in the pack. The daemon declares it once, as `jail_daemon.listen`, and its
argv and the pointer both spell it `{listen}`. On a shared network namespace the launcher picks a
free port for every declared address, the wire bridge's included, and composes it into the argv,
the pointer, the provider table and the via base. A private namespace keeps every declared port.
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
| A1 | `-p` grammar | `parseRunArgs` → `applyProfileValue`: a NAME, or `cli=name,…` | `parseHostExecFlags`, plus a second loop in `hostEnv`: a bare NAME only | as jail | `yolo host -p claude=zai -- claude` refuses a profile named `claude=zai` (MEASURED) | One grammar for every front door | — (**in flight**, ES-D27, [§5](#5-already-merged-and-in-flight)) |
| A2 | Missing, empty and glued values; unknown flags | `-p -- x` reads the injected `run` token as the profile. `--profile=` **executes `--profile=`** in a real jail (MEASURED, rc 127). `-p=` is accepted | "needs a value". `--profile=` is silently empty. `-p=zai` is refused with the full usage | as jail | The same typo gets opposite outcomes | One value-flag reader for `-p`, `--profile`, `--at`, `--network` and `--with-credentials`. A missing or empty value gives "`<flag>` needs a value", rc 2. `-p=` is accepted everywhere. `refuseUnknownFlags`' wording everywhere | — |
| A3 | `--at host` spellings | `parseRunArgs` → `refuseUnbuiltNotch` | `RewriteArgv`/`stripHostNotch` routes `yolo --at host … --` only | as jail | `yolo --at host -- c` runs. `yolo run --at host -- c` and a bare `yolo --at host` refuse. `yolo host --at host` gives rc 2 | The front door decides the notch once, wherever `--at` sits. `yolo host` takes `--at host` as a no-op. A run flag with no host meaning gets a named refusal | — ([OQ-2](../reference/host-agent-environment.md#oq-2) rules the alias; `confinement: host` stays refused by [`OQ-DP3`](../design/declaration-parity.md#decision-ledger)) |
| A4 | Config-change approval | the `--accept-config-changes` flag | `YOLO_ACCEPT_CONFIG_CHANGES` only; the explicit `yolo host [flags] --` refuses the flag | as jail | One approval, two spellings, each refused at the other notch | Accept the flag on the explicit host spelling. The env var stays for the wrapper path only | [OQ-NC10](#OQ-NC10): found while building item 10, no code reads the env var, so the host has nothing for the flag to approve |
| A5 | `--with-credentials` | refused (`refuseHostOnlyFlags`) | `addGrantValue` → `resolveHostGrant` → `ScopeInput.Grants` | refused | Host only | The jail channel passes `Grants`, keyed by the launched basename | [`OQ-ES5`](../design/credential-sources-separation.md#OQ-ES5)'s jail half |
| A6 | Who a profile reaches | `effectiveUseProfiles`: a bare name keys every installed agent CLI and **never** the `--` command. Per-agent env files | `composeHostLaunch` keys the basename of `cmd[0]`, whatever it is (ES-D1) | the jail's table, delivered per launch by basename (`launchEnv`) | `-p zai -- bash` hands bash `ZAI_API_KEY` at the host and nothing in a jail | One table builder. Delivery narrows the table to the launched process | [OQ-NC5](#OQ-NC5) |
| A7 | Profile disclosure; env-override refusal | `noteUseProfiles`, and `checkEnvOverrides` on all three jail arms | neither (no `EnvOverride*` call in `internal/cli/host*.go`) | as jail | MEASURED: the host ran claude with a bearer, a static pair and the aws pointer together, which a jail refuses | The host calls `packload.EnvOverrideRefusal`/`EnvOverrideFindings` over its own lookup and prints the same profile line | — ([OQ-SSO8](../design/sso-backed-bedrock.md#OQ-SSO8) is not scoped to the jail) |
| A8 | Config validation | `ValidateConfig` before every launch | none, apart from `config.UnknownUseProfileKey` | as jail | MEASURED: the host composes a provider written with removed keys and sends claude to first-party with model `m1` | The host runs the provider and profile section of validation over user scope | — |

### 3.2 Packs

| # | Concern | Jail | Host | macos-user | Divergence | Merge | Ruling |
|---|---|---|---|---|---|---|---|
| B1 | Selection closure (`needs`, then `via`) | `stagePacksInto` → `launchSelection` → `Selection.Close`. Also check, validation and promote | none in `loadedHostPacks`, `footerHostPacks`, `configuredPacksForInspection`, host apply, capture, revert or check-deps | as jail | `["claude"]` gives claude, aws-auth, openai-auth and wire-bridge in a jail, and claude alone at the host | One selection function every notch and verb calls. It returns packs, unresolved entries and cause lines | — (but after item 2: ES-D24 measured the aws pointer leaking otherwise) |
| B2 | Precedence order (later wins) | The launcher puts embedded packs first, then configured, then closure. The boot's `LoadJailPacks` reads with `os.ReadDir`, which is alphabetical | config order | as jail | MEASURED: different `PULSE_SERVER` winners at the launcher, the boot and the host | Compute the order once at selection, record it in `_pack-tree.json`, and have every reader use it | [OQ-NC4](#OQ-NC4) |
| B3 | Filters and the no-escape rule | `packstage.Stage` on every configured pack | `loadAsStaged` stages only a fetched or filtered pack, then points `Root` back at the source | as jail | MEASURED: an `exclude`d skill is delivered at the host, and an escaping symlink is rendered by host apply | The host stages every configured pack through `packstage.Stage` for the verb's lifetime | — |
| B4 | Malformed or unresolvable entry | `ValidateConfig` and `stagePacksInto` refuse | `LoadPacks(nil)` drops it silently; unresolvable packs are warned and composed without | as jail | MEASURED: a typo refuses the jail and schedules the pack's host output for retirement | `LoadPacks` returns its problems. Launch-shaped verbs refuse and name the pack; read-only verbs report | — ("host is supposed to act like everywhere else", NC-D5) |
| B5 | A program's `platforms` | `launcherUnpublished` declines, with a line | `DepRequirements` ignores it, so host apply offers a vendor install that does not exist | as jail | MEASURED | One packload predicate for installable programs per goos/goarch | — |
| B6 | Eight resolvers | `stagePacksInto`, check, `resolveSelectedPacks`, `resolveConfiguredPacks`, `UseProfileCLINames` and `LoadJailPacks` | `resolveConfiguredPack`, `footerHostPacks` | jail's | Each answers "which packs, from which tree" slightly differently | One resolver with two modes (stage, or read the declaration only). Filters always apply | — |
| B7 | Embedded packs | `MaterializeEmbedded` into scratch per launch; any problem is fatal | the `Embedded()` lease answers empty on a problem, which reads as "ships no pack by that name" | as jail | A yolo bug refuses a jail and reads as a typo at the host | `stagePacksInto` reads `Embedded()`. Both check `EmbeddedProblems` | — |

### 3.3 Providers, credentials and env

| # | Concern | Jail | Host | macos-user | Divergence | Merge | Ruling |
|---|---|---|---|---|---|---|---|
| C1 | Provider composition | `run.composedProviders`, with every adapter | `composedHostProviders`: `WithoutServiceAdaptations`, then `ViaInert` | `run.composedProviders`: it composes `:8214` to `:8216`, which nothing serves | For one declaration: a container works, the host refuses an adapter but silently drops a `via`, and macos-user launches against dead addresses | One composition helper that takes "this notch runs pack services" as an input. `yolo check` asks it for the configured runtime | — for the helper; [OQ-NC1](#OQ-NC1) for the value at a service-less notch |
| C2 | Literal jail-daemon addresses in pack env | served | delivered, and served by nothing (MEASURED) | delivered; the daemons are declined | Dead pointers, and credential injection by whoever binds the port first | The address becomes a fact of the serving daemon, composed per notch. Unserved means dropped and named | — ([§2.4](#24-the-addresses-those-secrets-protect-are-composed-not-literal)) |
| C3 | Env precedence | `agentEnvFileContent`: `env_sources` in default form is lowest, and the shape is last | `composeHostVarsGranting`: fold, then `env_sources`, then shape, then removals | `launchEnv`: `env_sources` last; tombstones skipped | One key gets three winners | One ordered composition in packload, which each vehicle serializes | [`OQ-CN8`](../design/provider-credential-scope.md#OQ-CN8) picks the order |
| C4 | Delivery vehicle | per-agent files | one exec | per launch, by basename | An agent started from a macos-user shell gets none of its profile | macos-user writes the per-agent files | [`OQ-CN9`](../design/provider-credential-scope.md#OQ-CN9) |
| C5 | Workspace scope of credential inputs | `env_sources` and `providers.*.api_key_env_name` merge in from the workspace | user scope only | as jail | A repo file can make `GH_TOKEN` zai-claimed and send it to z.ai | One scope reader for credential-bearing keys | [`OQ-AS3`](../research/agent-safehouse.md#OQ-AS3), [OQ-NC6](#OQ-NC6) |
| C6 | OpenAI prelaunch | declarative: `YOLO_AUTH_PRELAUNCH_<BIN>_*` read by the `agentAuthPrelaunchShellFn` launcher block, with no login without a TTY | `openaiauthhost.prepare` switches on the names `codex` and `pi`, and logs in regardless of profile or TTY | shim as jail | MEASURED: `yolo host -p zai -- pi </dev/null` reached the broker's browser login | The host reads the same declarative values from its composition. One Go prelaunch, logging in only at a TTY. The managed `CODEX_HOME` is keyed on the declaring pack | — ([OQ-OA3](../design/openai-auth-broker.md#8-decision-ledger) covers codex's managed home only) |
| C7 | Refusal and disclosure renderers | `checkProviderCredentials` has its own verdict line; `Disclosure()` gives no remedies | `credentialScopeLines` → `DisclosureWith(notes)`, and no verdict line | as jail | One refusal, two wordings | Both move next to `ProviderCredentialGaps` in packload. The jail passes `DisclosureNotes` | — |
| C8 | Claude OAuth | brokered through the terminator | the host claude's own login | the singleton is ensured, and no terminator runs | Two login lineages for Claude, one for OpenAI | — | [OQ-NC7](#OQ-NC7) |

### 3.4 Render

| # | Concern | Jail | Host | macos-user | Divergence | Merge | Ruling |
|---|---|---|---|---|---|---|---|
| D1 | Profile selection in render | one table feeds `packoverlay.Collect` and `surfaceSelectionFor` | overlays come from `use_profiles` (`overlayGateProfiles`), derives get no selection (`RenderHostPack`'s "NO profile table"), and the launch gate ignores `-p` | as jail, minus D2 | MEASURED: host apply wrote `CLAUDE_CODE_USE_BEDROCK` while reporting that `profile` does not apply at the host | Resolve the host table once per invocation and feed overlays, derive selection and env from it | [`OQ-HC3`](../design/host-computed-layer.md#OQ-HC3) |
| D2 | macos-user wire tables | all three tables are read at boot | — | `BuildRunPlan` and `PlanInvariants` relay `YOLO_PROVIDERS` and `YOLO_USE_PROFILES` and **drop `YOLO_PROFILES`** | MEASURED by a scratch test: codex's `model_provider` renders nil | One exported list of wire tables, ranged over in both places | — |
| D3 | The render loop | `ConfigurePackSurfaces` → `renderDeclaredSurface`, and `configureOnePack` for check | `RenderHostPack`; `configRender` for previews | as jail | Four loops that agree only by inspection | One loop that asks `Modes().Mechanism` per surface. Per-target facts are inputs | — |
| D4 | Derive content at the host | `deriveComputedLayer` over live tables | `hostTableKeys` over sentinel tables only | as jail | Host pi gets `models.json` `{}`, and no MCP or LSP entry reaches a host agent | Same derive, inputs composed at user scope | [`OQ-HC1`](../design/host-computed-layer.md#OQ-HC1) |
| D5 | `config render --at host` | — | `surfaceManifest()` shows the autonomous posture | — | MEASURED: the preview shows `additionalDirectories ["/"]`, while host apply writes `[]` | `--at host` previews `RenderHostPack` in observe mode | — |
| D6 | Skills | `copySkillSubdirs`: flat, last wins silently | `ComposeHostSkills` + `Collisions`: fatal collisions, `skills_tier` honored | as jail | MEASURED: `/dup` is packb's in a jail, and it is a fatal collision at the host | One composer (the hostskills plan plus `Collisions`) at both notches. Built-ins, the workspace layer and the LSP plugin become explicit per-notch layers | — ([the S1 collision ruling](../reference/pack-system.md#skills-collision), maintainer 2026-08-05; [§6a-2](../reference/pack-system.md#batch-6a)) |
| D7 | Briefing | `BriefingContent` + `agents_md_extra` + `ComposePackBriefings` | `ComposeHostBriefings`: pack prose only. `BriefingContent`'s host branch has no production caller | as jail | MEASURED: `agents_md_extra` never reaches a host agent | One per-destination composer with a notch-aware base body | — ([§6a](../reference/pack-system.md#batch-6a); env-manager Phase 8's done-when) |
| D8 | `files` kind | `packFilesTargets` → a `:ro` bind; addressed files resolved by its own `aliasByAgent` | `RenderHostFiles` over `ResolveDestinations` | **absent, silently** (`preparePackFiles` gated off) | pi's extensions never reach macos-user | The macos overlay carries files; `ResolveDestinations` everywhere | — |
| D9 | Posture launch flags | `InjectLaunchFlags` → `launchFlagClaims(packs, true)`, hardcoded | never called | as jail | A `guarded.launch` entry reaches no notch | `InjectLaunchFlags` takes the target profile's autonomy bit | — ([§6c](../reference/pack-system.md#batch-6c)) |
| D10 | macos-user boot steps | the `boot.go` step list | — | `RunDarwinBootstrap`'s own list: catalog and reconcile skipped on a false premise; git identity through `configureGit` | A new step must be added twice | One step table with declared platform exclusions | — |
| D11 | The `rmw` write rule | every object-valued derive key is regenerated | `in_full` tables only | as jail | No shipped surface hits it yet | One rule at both notches | [`OQ-CO15`](../design/config-ownership-and-promotion.md#oq-co15) |
| D12 | `host_files` and `mise_tools` | `ConfigureHostFiles`, `ConfigureMisePrism` | neither, and nothing says so | as jail | MEASURED: a source-less `host_files` entry is inert at the host with no report line | — | [OQ-NC8](#OQ-NC8) |

### 3.5 Services

| # | Concern | Jail | Host | macos-user | Divergence | Merge | Ruling |
|---|---|---|---|---|---|---|---|
| E1 | Unauthenticated credential services | [§2.1](#21-what-trusts-the-network-namespace-today) | the openai adapter on the host's loopback | the ports are composed, and bound by nobody or anybody | Safe only inside a private namespace | [§2.3](#23-the-fix-every-service-authenticates-its-caller-at-every-notch) | — (ruled 2026-09-27) |
| E2 | Pack services at a service-less notch | `yolo-jaild supervise` | refused, or `via` dropped in silence | declined; the launch proceeds against dead addresses | Three answers | One service-start path | [OQ-NC1](#OQ-NC1) |
| E3 | The terminator's fixed `:443` on a shared namespace | runs | — | declined by name | Collides, or binds the host's `:443` | — | [OQ-NC2](#OQ-NC2) |
| E4 | Autonomy on a shared namespace | autonomous regardless of `network.mode` (`render.ProfileFor` keys on the notch alone) | guarded | autonomous, with no network namespace | "The jail contains the agent" is half true there | — | [OQ-NC3](#OQ-NC3) |

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
| 2 | Served addresses composed from the serving daemon; ephemeral ports where the client takes a composed address; one "served at this notch" predicate feeding one provider-composition helper | C1, C2, the macos-user dead addresses | **pure merge.** The host and macos-user converge on "dropped and named", which is ES-D18's rule generalized. ✅ **Built in part** (`8805439f`, [NC-D16](#NC-D16)): one predicate (`packload.ServedDaemons`, `ServedAtRuntime`), one composition (`ComposeProvidersAt`, `ViaServedAt`) at the jail, macos-user, the host and `yolo check`, and a pack `env` pointer declares its daemon (`served_by`) so the credential gate withholds and names it where that daemon does not run. ✅ **The rest built** `660f836d` ([NC-D41](#NC-D41) to [NC-D44](#NC-D44); fixes `38749610` [NC-D45](#NC-D45), `9304cbb7` [NC-D46](#NC-D46)): a pointer's address is composed from its daemon's declaration (`jail_daemon.listen`, spelled `{listen}`), and a shared-namespace launch serves every jail daemon and pack service on ports it picked, which an attach re-reads. Verified by unit tests over the production compositions and, in the nested jail (a shared namespace), by two concurrent bridged launches that collided on `:8214` before (`TestTwoJailsOnOneLoopbackServeOnTheirOwnPorts`, Linux podman only: a macOS podman machine's host mode is the VM's namespace, [NC-D43](#NC-D43)). The `network.mode: "host"` arm on a real host is in the reachability carve-out: verified only by CI or a real rootless host | 1 | No notch exports an address nothing serves. `yolo check` predicts per runtime |
| 3 | Run the selected packs' services at a service-less notch | E2 | **behavior-changing, gated** on [OQ-NC1](#OQ-NC1) | 1, 2 | `-p cerebras -- claude` works at the host and on macos-user, or refuses identically at both |
| 4 | The terminator on a shared namespace | E3 | **behavior-changing, gated** on [OQ-NC2](#OQ-NC2) | 1 | — |

### Tier 2 — one selection (P1, P2)

| # | Item | Closes | Kind | After | Done when |
|---|---|---|---|---|---|
| 5 | One pack resolver with two modes; the host stages every configured pack through `packstage.Stage`; one embedded materialization | B3, B6, B7 | **pure merge** | — | An `exclude`d skill is absent at both notches. An escaping symlink refuses both. **Partly built** (`c619e688`, corrected by [NC-D11](#NC-D11); [NC-D7](#NC-D7) to [NC-D9](#NC-D9)): `config.ResolvePack` is the one resolver, the host stages a filtered pack into a process pack tree, and an `exclude`d skill is absent at both notches. A fetched or embedded pack's escaping symlink refuses both, and so does a filtered local pack's. **Not met:** an unfiltered local pack's escaping symlink is still followed at the host and refused by the launch. The builder proposes treating that half as gated on [OQ-NC9](#OQ-NC9), because meeting it as written breaks a shipped host behavior. That is a proposal, not this row's original gate, and the row stays partial until [OQ-NC9](#OQ-NC9) is ruled |
| 6 | One selection function, the closure included, at every host verb; `LoadPacks` returns entry problems and launch-shaped verbs refuse | B1, B4 | **pure merge** (NC-D4, NC-D5). ✅ **Built** `a4119e13` ([NC-D31](#NC-D31) to [NC-D33](#NC-D33)): `config.SelectPacks` is the one selection function for the jail launch, `yolo check`, validation, the lazy resolvers and every host verb, and `config.LoadPackEntries` returns the entry problems the launch-shaped host verbs refuse. Measured with a fake agent dumping its environment, over `"packs"` of `["claude"]`, `["codex"]`, `["pi"]` and all three: `yolo host -- claude`, `-p bedrock -- claude` and `yolo host env --agent claude -p bedrock` or `--agent codex` export the same variables before and after, and no dump holds an address nothing at the host serves. aws-auth's pointer, which ES-D24 measured the closure adding, is withheld and named instead. `-- codex` and `-- pi` reached the broker's browser login both before and after this item, which item 15 ends | **2** | `["claude"]` composes four packs at every notch, and host `-p bedrock` exports no unserved pointer |
| 7 | One installable-program predicate that honors `platforms` | B5 | **pure merge** | 5 | Host apply offers no install the vendor does not publish. **✅ Built** (`5b2bd198`; [NC-D10](#NC-D10)): `packdecl.Install.UnpublishedReason`, asked by the jail's launcher generation and the host's dep probe |
| 8 | Pack precedence computed once and recorded | B2 | **behavior-changing, gated** on [OQ-NC4](#OQ-NC4) | 6 | One winner for one key at the launcher, the boot and the host |

### Tier 3 — one front door (P2)

| # | Item | Closes | Kind | After | Done when |
|---|---|---|---|---|---|
| 9 | One value-flag reader (the `-p` grammar is in flight as ES-D27) | A1, A2 | **pure merge.** ✅ Built, `ffb7cd65`, with the fix-forward `f984f389` ([NC-D19](#NC-D19)) | — | `--profile=` refuses with rc 2 at both notches, and `-p=zai` works at both |
| 10 | The notch decided once at the front door; `--accept-config-changes` on the explicit host spelling | A3, A4 | **pure merge.** ✅ A3 built, `434c0920` ([NC-D20](#NC-D20)). A4 is held on [OQ-NC10](#OQ-NC10) | 9 | Every `--at host` spelling routes the same way |
| 11 | One profile table builder; delivery narrows it to the launched process | A6 | **behavior-changing, gated** on [OQ-NC5](#OQ-NC5) | 6, 9 | One recipient rule, named in `--help` at both notches |
| 12 | `--with-credentials` in a jail | A5 | **behavior-changing, gated** on [`OQ-ES5`](../design/credential-sources-separation.md#OQ-ES5)'s jail half | 11 | — |

### Tier 4 — the host runs the jail's checks (P1, P4)

| # | Item | Closes | Kind | After | Done when |
|---|---|---|---|---|---|
| 13 | The [OQ-SSO8](../design/sso-backed-bedrock.md#OQ-SSO8) override check, provider and profile validation, and the profile disclosure line at the host | A7, A8 | **pure merge.** ✅ **Built** `cca31cce` ([NC-D34](#NC-D34) to [NC-D36](#NC-D36)). The done-when is met in the form NC-D16 leaves it: a pointer the host delivers refuses beside its override as in a jail, and the measured aws-auth pointer is now withheld at the host, so a bearer beside it overrides nothing, as on a jail that does not serve aws-auth ([NC-D34](#NC-D34)) | 6 | The measured bearer-plus-pointer host launch refuses, as it does in a jail |
| 14 | One credential refusal and disclosure renderer | C7 | **pure merge.** ✅ Built, `ee62fc31` ([NC-D21](#NC-D21)) | — | Byte-identical refusal bodies at both notches, apart from the named spelling |
| 15 | The declarative OpenAI prelaunch at the host, logging in only at a TTY | C6 | **pure merge.** ✅ **Built** `6ec38ccf` ([NC-D37](#NC-D37)). Measured: before, that launch printed the broker's login URL and hung until killed; after, it runs pi with no broker started | 6 | `yolo host -p zai -- pi </dev/null` never starts a login |

### Tier 5 — one composition order and vehicle

| # | Item | Closes | Kind | After | Done when |
|---|---|---|---|---|---|
| 16 | One ordered env composition, serialized by every vehicle | C3 | **behavior-changing, gated** on [`OQ-CN8`](../design/provider-credential-scope.md#OQ-CN8) | 13 | One key has one winner at every vehicle |
| 17 | Per-agent env files on macos-user | C4 | **behavior-changing, gated** on [`OQ-CN9`](../design/provider-credential-scope.md#OQ-CN9) | 16 | — |
| 18 | One scope reader for credential-bearing keys | C5 | **behavior-changing, gated** on [`OQ-AS3`](../research/agent-safehouse.md#OQ-AS3) and [OQ-NC6](#OQ-NC6) | — | — |

### Tier 6 — render

| # | Item | Closes | Kind | After | Done when |
|---|---|---|---|---|---|
| 19 | Relay `YOLO_PROFILES` on macos-user through one exported wire-table list | D2 | **pure merge** (a live defect; it may land at any time) | — | codex on macos-user renders its `model_provider`. ✅ **Built** `e5f48a5f` ([NC-D22](#NC-D22)) |
| 20 | `InjectLaunchFlags` takes the autonomy bit | D9 | **pure merge** | — | A `guarded.launch` entry reaches a host launch. **Built** `bcb84bac` ([NC-D27](#NC-D27)) |
| 21 | `files` on macos-user; `ResolveDestinations` for addressed files everywhere | D8 | **pure merge** | — | pi's two extensions exist in a macos-user home. ✅ **Built** `4225117a` ([NC-D23](#NC-D23), [NC-D24](#NC-D24)) |
| 22 | One boot step table for Linux and macos-user | D10 | **pure merge** | — | An omission is a declared exclusion, never a missing line. ✅ **Built** `b8db83a8` ([NC-D25](#NC-D25), amended by [NC-D26](#NC-D26)) |
| 23 | `config render --at host` previews what host apply writes | D5 | **pure merge** | — | The preview and the write agree byte for byte. **Built** `8d39ac87` ([NC-D28](#NC-D28)) |
| 24 | One render loop | D3 | **pure merge** (a refactor) | 23 | Four loops become one. **Built** `73cbfff6`: the three writer loops are one, and the fourth's host half is the host apply's render since item 23 ([NC-D29](#NC-D29) keeps the jail preview a reader) |
| 25 | One skills composer | D6 | **behavior-changing, gated** on [OQ-NC11](#OQ-NC11). Filed as ruled (S1, [§6a-2](../reference/pack-system.md#batch-6a)); building it found an open question on the same behavior ([S5](BACKLOG.md#S5)) and two changes S1 does not decide | 24 | The same collision refuses at both notches |
| 26 | One briefing composer with a notch-aware base; `agents_md_extra` at the host | D7 | **behavior-changing, ruled** ([§6a](../reference/pack-system.md#batch-6a), Phase 8) | 24 | A host agent is told it is on the real machine. **Built** `180deba3` ([NC-D30](#NC-D30)) |
| 27 | The host profile table fed into render | D1 | **behavior-changing, gated** on [`OQ-HC3`](../design/host-computed-layer.md#OQ-HC3) | 11, 24 | — |
| 28 | Derives at the host; one `rmw` rule | D4, D11 | **behavior-changing, gated** on [`OQ-HC1`](../design/host-computed-layer.md#OQ-HC1) and [`OQ-CO15`](../design/config-ownership-and-promotion.md#oq-co15) | 24 | — |
| 29 | `host_files` and `mise_tools` at the host | D12 | **behavior-changing, gated** on [OQ-NC8](#OQ-NC8) | 24 | — |
| 30 | Autonomy on a shared namespace | E4 | **behavior-changing, gated** on [OQ-NC3](#OQ-NC3) | 1 | — |
| 31 | Claude OAuth at the host | C8 | **behavior-changing, gated** on [OQ-NC7](#OQ-NC7) | 1 | — |

**Verification, per item.** Every item touches `cmd/` or `internal/`, so it gets the nested-jail run
AGENTS.md requires. **Items 1 to 4 and 30 are in AGENTS.md's reachability carve-out**: a nested jail
is forced onto `--net=host`, and that is one of the configurations they change. So each of those items
is reported verified only against a real rootless host or CI, with
`podman info --format '{{.Host.RootlessNetworkCmd}}'` stated alongside the claim.

## 5. Already merged, and in flight

| What | Where it stands | Rows |
|---|---|---|
| `-p` accepted before the `host` verb (`yolo -p bedrock host -- claude`) | **on main**, `1baf1fd4` | A1 (spelling) |
| The host codex no-op: a profile whose provider the launch does not hold refuses (`packload.MissingProviderError`, ES-D24 to ES-D26), and the host footer leaves out a selection the host refuses | **in flight**, branch `worktree-wf_392bd9fc-1fb-1` (`56d5996d`, `e36b9121`), not on main | B1's symptom, C1 |
| The host reads `-p` in the run path's grammar (`parseProfileValue`, ES-D27). A pair naming another CLI refuses by name, and providers.md's "do not unify" warning is replaced | **in flight**, the same branch (`3830c102`) | A1 |
| Per-launch caller auth for the wire bridge, amending [WB-D4](../reference/wire-bridge.md#wb-d4) | **built**, `ea083e97` ([WB-D18](../reference/wire-bridge.md#wb-d18)); item 1's other four services followed in `a5fd280f` | E1, item 1 |
| [`host-notch-services.md`](../design/host-notch-services.md): the full shape of [OQ-NC1](#OQ-NC1)'s option A at the host. Its HS-D1 runs the closure through item 6's one selection function after item 2, HS-D2 is ES-D27, and its host halves take this plan's caller secret (NC-D2, NC-D3) | **in the tree**, in-review, with two questions of its own under option A ([`OQ-HS3`](../design/host-notch-services.md#OQ-HS3), [`OQ-HS4`](../design/host-notch-services.md#OQ-HS4)) | B1, E2, item 3 |
| [`host-computed-layer.md`](../design/host-computed-layer.md): derives at the host | **on main**, in-review. The HC-D1 to HC-D5 follow-up fixes are in flight on `worktree-wf_ac5e8112-da7-1` | D4, D1 |

> [!WARNING]
> **ES-D24 is the trap for item 6.** The in-flight branch keeps the host off the closure on purpose,
> because running it adds aws-auth's `AWS_CONTAINER_CREDENTIALS_FULL_URI=http://127.0.0.1:1461/…` to
> `yolo host -p bedrock -- claude`: a pointer at an address nothing on the host serves, and one that
> [§2](#2-security-first-the-boundary-that-is-not-one) shows is an injection channel. That measurement
> is right, and it is why item 6 comes after item 2, not a reason to leave the host off the closure.
> Once item 2 drops unserved addresses, ES-D24's reason is gone. Scoping the closure to
> `loadedHostPacks` alone would leave six host verbs on a narrower pack set, which is why
> [HS-D1](../design/host-notch-services.md#HS-D1) runs it through item 6's one selection function.
> **Answered by item 6** ([NC-D31](#NC-D31)): the pointer is withheld and named at every host verb.

## 6. Open questions

1. ✅ <a id="OQ-NC1"></a>**OQ-NC1: Does every notch run the selected packs' services?** The host and
   macos-user run no `jail_daemon` and no `kind: "service"` process. So a `via` or adapter profile
   works in a container, refuses at the host, and on macos-user launches against dead addresses.
   This decides whether item 3 is built at all. It also answers
   [`OQ-OA6`](../design/openai-auth-broker.md#OQ-OA6) for Codex on macos-user. Option A is drawn
   in full, for the host and macos-user, in
   [`host-notch-services.md`](../design/host-notch-services.md), which owns two narrower questions
   under it.

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

   **Answer:**
   > **Ruled in review 2026-09-28, as leaned:** A, run them launch-owned. It is the `openaiauthhost`
   > shape already shipped for host Codex, it makes one declaration mean one thing at every notch,
   > and item 1 makes a host-loopback service no weaker than a jail's.
   > [`OQ-OA6`](../design/openai-auth-broker.md#OQ-OA6) takes route (b). The service lifetime and
   > host-half declaration are [`host-notch-services.md`](../design/host-notch-services.md)'s
   > [`OQ-HS3`](../design/host-notch-services.md#OQ-HS3) and
   > [`OQ-HS4`](../design/host-notch-services.md#OQ-HS4).

2. 💬 <a id="OQ-NC2"></a>**OQ-NC2: What does the Claude OAuth terminator do on a shared network
   namespace?** It must listen on `127.0.0.1:443`, because `--add-host` maps `platform.claude.com`
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

   - **A — Decline it by name, as macos-user does.** Claude in that jail refreshes directly, and can
     race other jails for the single-use refresh token.
   - **B — Refuse the launch** while the terminator cannot be served.
   - **C — Reuse a yolo terminator already listening there**, identified by a handshake, and
     otherwise decline by name. After item 1 it authenticates by refresh-token match and serves the
     same machine-wide credential, so sharing it is correct.

   <!-- vantage: oq id=OQ-NC2 leaning="C: reuse a yolo terminator already on that loopback, since after item 1 it authenticates by refresh-token match and serves the same machine-wide credential; otherwise decline by name and disclose the refresh race." -->

   _Leaning:_ C, falling back to A with the race disclosed. B refuses a configuration that works
   today for a reason the user cannot act on.

   **Answer:**
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

   **Answer:**
   > **Ruled 2026-09-28 by parity, as leaned** (the maintainer, 2026-09-28: *"yes, NC as parity for
   > sure"*; the host acts like every other notch, with the same handling): A. Config order as
   > written, then closure additions, then the local pack last, at every notch.

5. ✅ <a id="OQ-NC5"></a>**OQ-NC5: Who does a bare `-p <name>` reach?** In a jail it reaches every
   installed agent CLI and never the `--` command ([pv-oq-5](../reference/providers.md#pv-oq-5)). At
   the host it reaches whatever command runs, `bash` included (ES-D1, an implementation decision).
   This decides item 11 and the host help's `yolo host -p zai -- curl` example.

   - **A — pv-oq-5 everywhere.** A command no pack installs gets keys only through
     `--with-credentials`, the grant [OQ-ES5](../design/credential-sources-separation.md#OQ-ES5) ruled for exactly that. ES-D1 retires, and the host's
     refusal names the grant.
   - **B — The host meaning everywhere.** A bare name also keys the launched `--` command, so a jail
     `yolo -p zai -- bash` hands bash `ZAI_API_KEY`. That reverses pv-oq-5's never-the-command half.

      _Leaning:_ A. The grant exists now and is ruled, so a second, implicit route for the same thing
   is the duplicate path this plan removes.

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

   **Answer:**
   > **Ruled 2026-09-28 by parity, against the leaning** (the maintainer, 2026-09-28: *"yes, NC as
   > parity for sure"*; the host acts like every other notch, with the same handling): A.
   > Source-less `host_files` entries render at the host through the same surface engine, under
   > `host_management`; source-bearing entries and `mise_tools` are named as inert.

9. ✅ <a id="OQ-NC9"></a>**OQ-NC9: May a local pack carry a symlink that points out of the pack?**
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

   **Answer:**
   > **Ruled 2026-09-28 by parity, as leaned** (the maintainer, 2026-09-28: *"yes, NC as parity for
   > sure"*; the host acts like every other notch, with the same handling): A. A local pack's links
   > are followed at every notch; a fetched pack's escaping link stays refused at every notch.

10. ✅ <a id="OQ-NC10"></a>**OQ-NC10: What does `--accept-config-changes` approve at the host?** Row A4
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

    **Answer:**
    > **Ruled in review 2026-09-28, as leaned:** A. The zero-prompt auto-apply left the host launch
    > nothing to approve: retire the unread `YOLO_ACCEPT_CONFIG_CHANGES`, keep refusing the flag by
    > name, and leave the first-apply MCP loss to `yolo host apply --assert` at a terminal.

11. ✅ <a id="OQ-NC11"></a>**OQ-NC11: Does a skill-name collision refuse a jail launch, and does the
    jail honor `skills_tier`?** Item 25 was filed as ruled by S1. The maintainer's words, 2026-08-05:
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

    This decides item 25.

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

    **Answer:**
    > **Ruled 2026-09-28 by parity, as leaned** (the maintainer, 2026-09-28: *"yes, NC as parity for
    > sure"*; the host acts like every other notch, with the same handling): A. A skill-name
    > collision is fatal at the launch, host-side and before the container exists, and the jail
    > honors `skills_tier`; the jail's fan-out stays as it is until
    > [`OQ-S4`](BACKLOG.md#OQ-S4) is answered.

## 7. Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| OQ-NC1 | **Maintainer ruling**, as leaned: A, every notch runs the selected packs' services, launch-owned; the lifetime and host-half declaration are [`OQ-HS3`](../design/host-notch-services.md#OQ-HS3) and [`OQ-HS4`](../design/host-notch-services.md#OQ-HS4) | 2026-09-28 | [OQ-NC1](#OQ-NC1) | pending |
| [OQ-NC4](#OQ-NC4), [NC5](#OQ-NC5), [NC8](#OQ-NC8), [NC9](#OQ-NC9), [NC11](#OQ-NC11) | **Maintainer ruling by parity:** *"yes, NC as parity for sure"*. Each takes its A: one pack order, `-p` means agent CLIs everywhere, source-less `host_files` render at the host (NC8 against its leaning), local-pack links followed at every notch, a skill collision fatal before the jail starts and the jail honors `skills_tier` | 2026-09-28 | [§6](#6-open-questions) | pending |
| [OQ-NC6](#OQ-NC6) | **Maintainer ruling**, as leaned: A, every provider field that decides where a credential goes is user-scope only | 2026-09-28 | [§6](#6-open-questions) | pending |
| [OQ-NC7](#OQ-NC7) | **Maintainer ruling**, as leaned: A, host claude keeps its own login until [`OQ-CI1`](../reference/claude-oauth-interposition.md#oq-ci1) rules | 2026-09-28 | [§6](#6-open-questions) | ✅ nothing to build |
| [OQ-NC3](#OQ-NC3) | **Maintainer ruling**, as leaned: A, a jail on a shared network keeps autonomy, disclosed from a network primitive in `render.Profile` | 2026-09-28 | [§6](#6-open-questions) | pending |
| [OQ-NC10](#OQ-NC10) | **Maintainer ruling**, as leaned: A, retire `YOLO_ACCEPT_CONFIG_CHANGES` and keep refusing the flag by name | 2026-09-28 | [§6](#6-open-questions) | pending |
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
| <a id="NC-D16"></a>NC-D16 | *Implementation decision.* **Served at this notch** (coined here) means the notch runs the jail daemon: a container runtime runs every daemon its composed payload names, macos-user and the host run none. It is one value, `packload.ServedDaemons`, built by `ServedAtRuntime` for a jail launch (the launch's `servedDaemons`, and `yolo check`'s `predictedServed` over the configured runtime) and `NothingServed` at the host. The one runtime branch lives in `ServedAtRuntime`, so the launch and the prediction cannot disagree. Every provider composition goes through `ComposeProvidersAt` (`WithServed`), which leaves out each adapter address whose service is not served and returns what it left out for the gate; `ViaServedAt` clears an unserved via and names it. `WithoutServiceAdaptations`, `UnservableAdaptations` and `ViaInert`, the host's second path, are deleted. A pack `env` contribution that points at a daemon declares it (`served_by`, packdecl). The credential gate withholds those variables where the daemon is not served, from every vehicle (`ScopeInput.Served`, `CredentialScope.FoldFor`), and every notch names them in one wording (`packload.UnservedLines`). The env-override check skips a contribution the gate withholds, since there is nothing to override. The two shipped pointers declare it: codex's `CODEX_REFRESH_TOKEN_URL_OVERRIDE` (`openai-auth-broker`) and aws-auth's `bedrock` pointer (`aws-auth`). A managed host launch that serves a pointer itself (`yolo host -- codex`, `openaiauthhost`) is not told it is missing (`688ca4f2`). Consequences: on macos-user a profile that needs the wire bridge now refuses as at the host, and aws-auth's pointer is withheld there, so the [OQ-SSO8](../design/sso-backed-bedrock.md#OQ-SSO8) refusals no longer fire over a pointer that could never answer. A container launch that has not enabled a loophole withholds its pointer too | 2026-09-28 | [§2.4](#24-the-addresses-those-secrets-protect-are-composed-not-literal) | ✅ `8805439f` |
| <a id="NC-D17"></a>NC-D17 | *Implementation decision.* **Ephemeral jail ports are not built in this pass, and neither is composing a pointer's address from its daemon's declaration**, which is the same mechanism: today the port is a literal in the daemon's argv and again in the pack's `env`, held together by tests. The host already takes one: the managed Codex adapter binds `127.0.0.1:0` and composes the address it got (`openaiauthhost`). A jail's clients are composed on the host before its daemons bind, so an ephemeral jail port has to be chosen by the launcher on a shared namespace, substituted into the daemon's argv, the pack env value and the provider table, and re-read on an attach exactly as the caller token is. That is a mechanism of its own and is left for a follow-up. Until then two jails sharing one loopback (`network.mode: "host"`, or nested) still contend for `:1460`, `:1461` and `:8214` to `:8216`. Since item 1 the loser **fails closed**: its client reaches the winner's daemon and is refused `401` for the wrong token. Before item 1 the winner answered the loser's client | 2026-09-28 | [§2.4](#24-the-addresses-those-secrets-protect-are-composed-not-literal) | ✅ the follow-up, [NC-D41](#NC-D41) to [NC-D44](#NC-D44) (`660f836d`) |
| <a id="NC-D18"></a>NC-D18 | *Implementation decision.* A managed host Codex launch's caller token belongs to the managed home's **live launches**, not to one launch. Every `yolo host -- codex` shares one managed `CODEX_HOME`, so one `auth.json`, and Codex reloads that file before each refresh, so a token per launch let the newest launch's replace every other session's and those sessions' adapters refused their own Codex `401`: two host sessions at once, which worked before item 1, broke. A launch holds a shared `flock` on the home's `.yolo-live.lock` until its agent exits (`sharedCallerToken`). One that can take it exclusively is alone, and mints a fresh token into the `0600` `.yolo-caller-token`; otherwise it reuses that file's token. A second lock serializes the decision, so no launch rotates the token under another. **What it proves** is unchanged: the per-launch token also lived only in that home's `0600` `auth.json`, so the proof was always that the caller can read the managed Codex home. A per-launch `CODEX_HOME` was not taken, because it would split Codex's sessions and history across launches | 2026-09-28 | [§2.3](#23-the-fix-every-service-authenticates-its-caller-at-every-notch) | ✅ |
| <a id="NC-D19"></a>NC-D19 | *Implementation decision, item 9.* `readValueFlag` (`internal/cli/valueflags.go`) is the one reader for `-p`/`--profile`, `--at`, `--network` and `--with-credentials` at `yolo [run]`, `yolo host --` and `yolo host env`. A value is the next token whatever it looks like (`-p -h` stays a profile named `-h`) or a glued `=value`. The `--` separator is never a value. A trailing flag, a flag followed by `--`, and an empty glued value are refused as "`<flag>` needs a value", exit 2. `--with-credentials` in a jail keeps its host-only refusal, which outranks the missing value. To make `yolo -p -- claude` a missing value rather than a profile named `run`, `RewriteArgv` puts `run` first instead of at the separator, and leaves argv alone when any positional precedes `--` (`f984f389`: for one commit `yolo chekc -- x` launched `chekc` in the jail). `valueTakingFlags` and `runHelpRequested`'s skip derive from `launchValueFlags`, and a source scan pins every `readValueFlag` call against it. A mistyped flag before `yolo host`'s `--`, and at `yolo host env`, is refused in `refuseUnknownFlags`' words | 2026-09-28 | [§4](#4-the-ordered-build-list) item 9 | ✅ `ffb7cd65`, `f984f389` |
| <a id="NC-D20"></a>NC-D20 | *Implementation decision, item 10 row A3.* `routeArgv` (`internal/cli/dispatch.go`) is the front door's one decision, for `Main` and `routeDecision`. A launch, explicit or implicit, whose own tokens carry `--at host` becomes `yolo host`. `parseRunArgs` reads it, so the last `--at` wins and an implicit command start (`yolo run --at host claude --resume`) hands the whole command over. The run token and the notch pair are consumed and every other token is carried over as typed, so the host parser judges it. `stripHostNotch` is deleted. A bare `yolo --at host` is `yolo host`, which prints its usage. `yolo host` takes `--at host` as a no-op and refuses another notch by name, with the jail launcher's words for an unknown one. It refuses by name a run flag with no host meaning, derived from `runFlags` less `--profile` and `--at`, and that includes `--accept-config-changes` until [OQ-NC10](#OQ-NC10). It answers `--help` among its exec flags. `yolo --at host env` is not routed: `env` is a host verb, not a launch, and naming host verbs at the front door would copy `hostMain`'s switch. *Refined after review:* `hostNotchArgv` consumes EVERY `--at` pair, so last-wins holds in both orders (`--at jail --at host` is the host, not a host refusal of the earlier `--at jail`). With no `--`, `hostMain` hands a leading flag other than help to `parseHostExecFlags`, so `yolo --at host --profile=` and `yolo host --profile=` refuse with exit 2 as a `--` line does, not as `unknown verb "-p"`; flags that parse are refused for naming no command, since the host verb has no default command and a host shell would be one the user did not type | 2026-09-28 | [§4](#4-the-ordered-build-list) item 10 | ✅ `434c0920` |
| <a id="NC-D21"></a>NC-D21 | *Implementation decision, item 14.* `packload.ProviderCredentialRefusal`, beside `ProviderCredentialGaps`, is the credential pre-flight's whole message at both notches: the verdict, the facts, the remedy, or the override notice with no hatch re-offered. It also returns whether to stop. The host prints it behind `yolo host: ` and the jail bolds its verdict, which is all that differs. The host names the inherited environment as `packload.FromLaunchEnv`, the jail's words, so the consulted line matches too. `CredentialScope.Disclosure` is deleted and the jail passes the zero `DisclosureNotes` to `DisclosureWith`, so a notch's difference is a named note and never a second function. Each notch's call-site test carries the same literal body | 2026-09-28 | [§4](#4-the-ordered-build-list) item 14 | ✅ `ee62fc31` |
| <a id="NC-D22"></a>NC-D22 | *Implementation decision, item 19.* `entrypoint.WireTables` is the one list of wire tables. The macos-user bootstrap relay, `PlanInvariants`, both channel writers in the run pipeline and `ParseEntryChannel` range over it. `PlanInvariants` now reads the tables from the session env file: it scanned `LaunchArgv`, which has carried no composed value since the channel moved into that file, so it could not fire for any table | 2026-09-28 | [§3.4](#34-render) D2 | ✅ `e5f48a5f` |
| <a id="NC-D23"></a>NC-D23 | *Implementation decision, item 21.* `packFilesTargets` resolves addressed trees through `packload.ResolveDestinations`, and its own slot table is deleted. For the jail's missing-source warning to survive that, destination borrowing routes a `files` contribution whenever it declares a `from`, present or not. So host apply now refuses such a path as a missing source, where it used to drop it without a word | 2026-09-28 | [§3.4](#34-render) D8 | ✅ `4225117a` |
| <a id="NC-D24"></a>NC-D24 | *Implementation decision, item 21.* macos-user delivers `files` trees through the home overlay, beside skills and briefings: copied from the staged pack tree, listed for the overlay install, and write-denied by the Seatbelt profile, which is this backend's `:ro`. A tree whose source is missing is skipped with the jail's own warning | 2026-09-28 | [§3.4](#34-render) D8 | ✅ `4225117a` |
| <a id="NC-D25"></a>NC-D25 | *Implementation decision, item 22.* One table, `internal/entrypoint/bootsteps.go`, run by `Main` and `RunDarwinBootstrap`. An exclusion is a field on the step holding its reason. Each boot runs what it ran before, in the same order and with the same perf and failure labels, except that the orphan catalog and the program reconcile now run on macos-user too, since their stated premise (no staged pack tree there) is false. Left as declared exclusions: the CA bundle, nvim config, the retired-mise cleanup and the stale-client cleanup, each stated as not ported or not established rather than impossible. Not built here: macos-user does not relay `programs.autoprune` (`YOLO_PROGRAMS_AUTOPRUNE`), so autoprune stays off there. Item 1's caller-token files ([NC-D14](#NC-D14)), built beside this table, are its container-only step `write_caller_token_files`, which runs before the supervisor | 2026-09-28 | [§3.4](#34-render) D10 | ✅ `b8db83a8` |
| <a id="NC-D26"></a>NC-D26 | *Implementation decision, item 22, amending NC-D25.* The orphan catalog stays a declared exclusion on macos-user. Its input is there, but its one terminal line sends the reader to three places and none works on that backend: the boot log that holds the names (macos-user keeps none, so every name was discarded), `yolo programs ls` (it answers wrongly inside the sandbox) and `programs.autoprune` (not relayed). The step returns there once all three work on that backend. The program reconcile still runs on macos-user, since its findings are terminal warnings | 2026-09-28 | [§3.4](#34-render) D10 | ✅ `183c3854` |
| <a id="NC-D27"></a>NC-D27 | *Implementation decision.* `InjectLaunchFlags` takes the posture bit as a parameter, the one `LaunchFlagsFor` and `packoverlay.Collect` already take. The jail launcher passes the jail notch's (`render.ProfileFor(KindJail)`), every in-jail carrier passes its render target's, and `yolo host --` passes the host notch's. The rewrite's wording moved to `packload.LaunchInjection.DisclosureLines`, so both notches print the same sentence | 2026-09-28 | [§4](#4-the-ordered-build-list), item 20 | ✅ `bcb84bac` |
| <a id="NC-D28"></a>NC-D28 | *Implementation decision.* `config render --at host` runs `entrypoint.RenderHostPack` in observe over host apply's own inputs. The byte-identical content comes from splitting the rmw writer into a compose half and a write half (`composeRMWSurface`), as the stateful writer already was. A bare `yolo config render` outside any workspace resolves the host notch, so it previews the host apply too. The preview does NOT fetch, because a read-only surface never does (`refreshHostPacks`' rule), so "byte for byte" holds against the pack store as it stands: a branch-following pack whose upstream moved since the last fetch renders at the stored commit, and a git pack never fetched is reported as "not fetched yet", not as a refusal the apply would never make | 2026-09-28 | [§4](#4-the-ordered-build-list), item 23 | ✅ `8d39ac87` |
| <a id="NC-D29"></a>NC-D29 | *Implementation decision.* The one render loop is `planPackSurfaces` (the posture, the contributions and the census's mechanism), `writeSurfaceThrough` (the dispatch) and `renderPackSet` (the jail walk, which both the boot and `yolo check` run with their own failure handling). The jail's `yolo config render` preview stays outside it. It is a reader with no mechanism to ask: its scope leaves out the computed layer and the capture overlay, and it previews core surfaces such as `mise/config` that no pack declares | 2026-09-28 | [§3.4](#34-render), D3 | ✅ `73cbfff6` |
| <a id="NC-D30"></a>NC-D30 | *Implementation decision.* `BriefingContent` at the host notch is its confinement header alone, because every section after it describes a launch. `jailcontent.ComposeBriefingSections` assembles every destination at both notches. The host base is never empty, so yolo owns every briefing destination a selected pack declares, with or without prose, and retires one only once no selected pack declares it. That is [§6a](../reference/pack-system.md#batch-6a)'s "fully generated and controlled". `agents_md_extra` at the host is read from user scope only | 2026-09-28 | [§3.4](#34-render), D7 | ✅ `180deba3` |
| <a id="NC-D31"></a>NC-D31 | *Implementation decision, item 6.* `config.SelectPacks` (`internal/config/packselection.go`) is the one selection function: it resolves the entries in the order given and closes the selection (`packload.Selection.Close`) over what resolved. What differs between callers is an input, never a loop: the resolver (the launch stages each entry into its tree, a host verb resolves for its process, validation and the footer write nothing to the store), the entry order, whether the first failure stops the selection, the closure's profile table, an announcer for the cause lines and a resolver for each addition. The jail launch keeps its embedded-first order as its own input, since which order is right is [OQ-NC4](#OQ-NC4)'s. Every host verb calls it through `cli.selectHostPacks`, with the user-scope table, except `yolo host --` and `yolo host env`, whose table is the one profile the launch selects for the one agent it runs ([HS-D1](../design/host-notch-services.md#HS-D1)). An addition resolves through the verb's own resolver, as the bare-name entry it stands for. Both host front doors print each addition's cause line (WB-D12), and `yolo host apply` prints them in its report. A source scan (`TestEveryHostVerbSelectsThroughTheOneFunction`) fails when a host verb resolves `packs` entries or closes a selection by hand | 2026-09-28 | [§4](#4-the-ordered-build-list), item 6 | ✅ `a4119e13` |
| <a id="NC-D32"></a>NC-D32 | *Implementation decision, item 6, building [NC-D5](#7-decision-ledger).* `config.LoadPackEntries` returns the `packs` entries that did not lower, each named by its place in the list (`config.packs[1]`); `LoadPacks` keeps its warning sink over it. At `yolo host --` and `yolo host env` a malformed entry, an unresolvable pack or a refused closure refuses the launch, every problem listed with its remedy, instead of composing without the pack. The launch hook refuses the same set, which revises [host-apply-staleness](../reference/host-apply-staleness.md#the-dispositions)'s "render nothing, and exec" for this one row. `yolo host apply` reports a malformed entry as an unresolvable pack, so `--assert` refuses the incomplete set: before, a list of only malformed entries read as an empty `packs` and took the branch that retires every pack's output. The read-only verbs report it beside the unresolvable packs. `undeclaredHostProfileError`'s branch naming an unusable pack beside an undeclared profile is deleted, since the launch refuses before any profile is resolved | 2026-09-28 | [§4](#4-the-ordered-build-list), item 6 | ✅ `a4119e13` |
| <a id="NC-D33"></a>NC-D33 | *Implementation decision, item 6, meeting [HS-D1](../design/host-notch-services.md#HS-D1)'s second requirement.* ES-D18's unserved-bridge refusal words a pack the closure joined as joined, quoting its cause line (*"though "wire-bridge" joined this launch (+ wire-bridge (needed by claude))"*), and never says it is in `packs`. On a bare `["claude"]` the `openai-codex` provider is in the table now, so `-p codex -- claude` refuses as that unserved bridge and no longer as a missing provider. A missing provider's pack that a selected pack's `needs` names can now reach the refusal only under a `when_bins` the launch does not meet, and both refusals say so | 2026-09-28 | [§5](#5-already-merged-and-in-flight) | ✅ `a4119e13` |
| <a id="NC-D34"></a>NC-D34 | *Implementation decision, item 13, row A7.* `yolo host --` runs `packload.EnvOverrideFindings` over its selected packs, its one-agent profile table, the host's served set (nothing) and a lookup answering where each variable the agent receives comes from (`hostComposition.envOverrideFindings`). A certain finding refuses, an uncertain one warns, and `yolo host env` discloses both without refusing, as it discloses the credential pre-flight. The one input that differs from a jail is named: the invoking shell is a delivery at the host, since the agent inherits it, so its origin is `packload.FromLaunchEnv`; no jail backend forwards it. No `host_files` destination renders at the host, so a `host_file` override is not evaluated there (a false negative, never a false refusal; [OQ-NC8](#OQ-NC8) owns host files). **The done-when as written cannot be met, and that follows from [NC-D16](#NC-D16), not from this item:** the measured launch's aws-auth pointer names its daemon (`served_by`), which the host does not run, so the gate withholds it and nothing overrides it. That is the answer a jail gives wherever aws-auth is not served. A pointer the host does deliver refuses beside its override exactly as in a jail (`TestHostLaunchRefusesAPointerBesideItsOverride`) | 2026-09-28 | [§4](#4-the-ordered-build-list), item 13 | ✅ `cca31cce` |
| <a id="NC-D35"></a>NC-D35 | *Implementation decision, item 13, row A8.* `config.ValidateProviderSection` is ValidateConfig's provider and profile section over one config with no workspace: the `providers` entries, the retired `agent_profiles` key, `use_profiles`, and the user file's `profiles` and `adapters`, in ValidateConfig's order and words. `validateProviders`, `validateProfiles` and `validateAdapters` each split into that user half and their workspace-scope refusal, which the host does not run, since it reads no workspace config. The host runs it after ES-D5's refusal, which keeps the remedy it adds. This retires ES-D9's typed-`-p` exemption: a `use_profiles` key every jail launch refuses now refuses a typed `-p` at the host too | 2026-09-28 | [§4](#4-the-ordered-build-list), item 13 | ✅ `cca31cce` |
| <a id="NC-D36"></a>NC-D36 | *Implementation decision, item 13, row A7.* `packload.ProfileDisclosures` is the profile line, one per distinct selected name, for the jail's `noteUseProfiles` and for both host front doors, over the host launch's one-agent table and its selected packs | 2026-09-28 | [§4](#4-the-ordered-build-list), item 13 | ✅ `cca31cce` |
| <a id="NC-D37"></a>NC-D37 | *Implementation decision, item 15, row C6.* The host's OpenAI prelaunch reads `YOLO_AUTH_PRELAUNCH_<BIN>_FLAG` and `_LOGIN` from the launch's composed variables (`hostComposition.prelaunch`), spelled by `openaiauthhost.PrelaunchVar` exactly as the jail's launcher spells them; a test pins the two spellings together. `openaiauthhost.Prepare` does nothing when nothing is declared. It serves a declared view the host's way: the pi view as the host credential socket, since a jail's pi auth file is the user's own file at the host; the codex view as a managed `CODEX_HOME` under `host-agents/<declaring pack>` (`EnvFoldEntry.Pack` carries the declarer; the shipped pack's path is unchanged); a login-only prelaunch proves the login and serves nothing; any other view refuses by name. With no login it starts the browser login only when stdin is a terminal (the host gate's own probe, `hostGateCanPrompt`); off one it prints the jail launcher's two lines and runs the command without the credential | 2026-09-28 | [§4](#4-the-ordered-build-list), item 15 | ✅ `6ec38ccf` |
| <a id="NC-D41"></a>NC-D41 | *Implementation decision, NC-D17's follow-up.* A **served address** (coined here, and in `internal/packload/served.go`) is the loopback `host:port` a jail daemon, a service adaptation or a via route answers at in one launch. A loophole's jail daemon declares its address once, as `jail_daemon.listen` (a loopback IP literal and a port), and its argv spells it `{listen}` (`loopholedecl.TokenListen`). The two are refused apart, and `{listen}` is refused in a field the host resolves. A pack `env` value spells it `{listen}` too, legal only beside `served_by`: it is the one interpolation an env value takes, and it resolves to the served address of that daemon. The shipped pointers now read `http://{listen}/oauth/token` (codex) and `http://{listen}/credentials` (aws-auth), so the port is written once. `packload.ServedDaemons` carries where each daemon serves (`WithListen`) and the declared-to-served map for the pack services' adapter and via addresses (`WithRebind`). The payload writer resolves the argv from it (`JailDaemonSpec.ResolvedCmd`), the credential gate resolves the pointer (`servedFold`), and the provider table and via base move with it (`adaptEndpoints`, `ViaServedAt`). A user's `adapters` override is not a declared address and never moves. A pointer whose daemon declares no address is withheld and named. The footprint describes declarations, so it prints `{listen}` where it printed the port; the launch banner names the served address ([NC-D46](#NC-D46)). `yolo check` predicts every daemon at its declared address | 2026-09-28 | [§2.4](#24-the-addresses-those-secrets-protect-are-composed-not-literal) | ✅ `660f836d` |
| <a id="NC-D42"></a>NC-D42 | *Implementation decision.* **Only a shared network namespace moves an address** (`sharesLauncherNetns`, its fourth reader). A bridged jail's served addresses are its declared ones, through the same served set, so its argv, pointers and provider table are byte-identical to before. Ephemeral ports everywhere was not taken: it would change every bridged jail's `ANTHROPIC_BASE_URL` on every launch, and the launcher's loopback is not a bridged jail's, so a port it found free proves nothing there. macos-user shares the host's loopback but runs no jail daemon, so it picks nothing ([OQ-NC1](#OQ-NC1) decides whether it runs them). The terminator's `:443` stays, because `--add-host` maps `platform.claude.com` to it ([OQ-NC2](#OQ-NC2)) | 2026-09-28 | [§2.4](#24-the-addresses-those-secrets-protect-are-composed-not-literal) | ✅ `660f836d` |
| <a id="NC-D43"></a>NC-D43 | *Implementation decision.* **The launcher picks each port** by binding port 0 on the declared loopback host, holding every listener until all are picked so no two collide, and releasing them before the jail starts. On a shared namespace that loopback is normally the jail's, so the kernel's answer is a port free where the daemon will bind. ⚠ Not on a macOS podman machine: there `network.mode: "host"` joins the VM's network namespace, not the Mac's, so the port is picked on a loopback the daemon never binds. The pick still separates two such jails from each other's declared ports, but it proves nothing about the VM's loopback. `sharesLauncherNetns` has the same blind spot, so what the daemons advertise there is unverified too. The container test of this item runs on Linux podman only. It is free when picked, not reserved: a process that binds it first wins it, and that fails closed, since the daemon cannot bind and its clients are refused for the wrong caller token by whatever holds the port. Picks are settled once per process, like the caller tokens, so the two compositions one launch runs agree. A failed pick keeps the declared ports and says so | 2026-09-28 | [§2.4](#24-the-addresses-those-secrets-protect-are-composed-not-literal) | ✅ `660f836d` |
| <a id="NC-D44"></a>NC-D44 | *Implementation decision.* **An attach re-reads the running jail's served addresses and never picks.** The launch writes the declared-to-served map into the `0600` channel section as `YOLO_SERVED_ADDRESSES` (`paths.ServedAddressesEnv`), only when it moved something. An attach adopts that map in `rekeyChannelForAttach`, replacing its own picks, and composes the channel again when it differs, exactly as it adopts the caller tokens. A jail that recorded none serves its declared addresses: it was bridged, or launched by a yolo older than this. The reader keeps only loopback pairs, because the jail can write the file | 2026-09-28 | [§2.4](#24-the-addresses-those-secrets-protect-are-composed-not-literal) | ✅ `660f836d` |
| <a id="NC-D45"></a>NC-D45 | *Implementation decision.* **A pack service takes no `{listen}`.** Only a loophole's jail daemon declares a listen address (`jail_daemon.listen`). A service answers at its adapters' `address` and its `via_address`, which the launcher moves itself, so `{listen}` in a service's `jail_daemon.cmd` or `host_daemon.cmd` would resolve to an empty string. Decode refuses it there, and refuses an `env` value naming `{listen}` whose `served_by` is a service the same manifest declares. A pointer `served_by` another pack's service cannot be decided at decode, so the launch withholds it and names it, saying that only a loophole declares a listen address | 2026-09-28 | [§2.4](#24-the-addresses-those-secrets-protect-are-composed-not-literal) | ✅ `38749610` |
| <a id="NC-D46"></a>NC-D46 | *Implementation decision.* **The launch banner prints a pointer's served address, not `{listen}`.** A launch disclosure describes what is about to run on this machine, which is why it already resolves `{state}`. So the banner's env lines resolve `{listen}` through the served set the launch composed the pointers from (the channel's `served`), and both call sites pass the channel. A bridged launch's banner is byte-identical to before served addresses, and a shared-namespace launch's banner names the picked port. Where the daemon is not served at this notch (macos-user, or the loophole off), the line keeps `{listen}`, and the launch names that pointer as withheld. `yolo pack footprint` stays machine-independent and keeps the token | 2026-09-28 | [§2.4](#24-the-addresses-those-secrets-protect-are-composed-not-literal) | ✅ `9304cbb7` |
