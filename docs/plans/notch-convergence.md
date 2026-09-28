---
title: "Notch convergence — one code path per concern, and the loopback services that assumed a boundary"
date: 2026-09-28
status: in-review
tags: [plan, notches, host, parity, security, loopback]
summary: "Five audits found that the host notch, the container jail and macos-user reach one declaration through different code for flags, pack selection, providers, env order and render, and that the loopback services serving credentials are safe only while the jail has its own network namespace. The plan: one description, one code path per concern, the notch choosing only the confinement. Caller authentication comes first, because network.mode host and macos-user already share the host's loopback. Then an ordered build list: pure merges that need no ruling, and behavior changes gated on eight new questions and seven existing ones."
---

# Notch convergence — one code path per concern, and the loopback services that assumed a boundary

**Status:** DECIDED, 2026-09-28 — owed: **work.** The thesis and every pure merge in the build list
are ruled by the maintainer's 2026-09-27 words ([§1](#1-the-thesis)). Caller authentication for the
loopback services is ruled the same day and comes first ([§2](#2-security-first-the-boundary-that-is-not-one)).
The wire-bridge part of it is being built elsewhere. The word is `DECIDED` although eight 💬 questions
are open. Each one gates a single build item, and none of them gates the thesis or blocks the items
before it, so this is [the tie-breaker](README.md#the-vocabulary--seven-words-and-the-word-names-what-is-owed)
applied to per-item gates. That judgement is stated here so it can be checked. Nothing in the list is
built. Evidence was verified against `eb0af5ee` on 2026-09-28. The measurements are the five auditors'
from 2026-09-27, taken in scratch homes under `/tmp` against a binary built from that commit.

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

**Needs your ruling:** [OQ-NC1](#OQ-NC1), [OQ-NC2](#OQ-NC2), [OQ-NC3](#OQ-NC3), [OQ-NC4](#OQ-NC4), [OQ-NC5](#OQ-NC5), [OQ-NC6](#OQ-NC6), [OQ-NC7](#OQ-NC7), [OQ-NC8](#OQ-NC8).

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
  bind should instead fail (both INFERRED from source, not measured).
- **macos-user** has no network namespace at all. The user guide's
  [settings table](../../userguide/reference/settings-per-setup.md) says: "The sandbox is on the Mac's
  own network, so `localhost` is the Mac." Today its jail daemons are declined
  (`noteMacosUserJailDaemonDeclines`). But the codex pack still points Codex at `127.0.0.1:1460`, and
  the provider composition still points claude at `:8215`, so **whatever local process of any user
  binds those ports first receives Codex's refresh request or claude's subscription bearer**.
- **A nested jail** is forced onto `--net=host`, so it joins the outer jail's namespace. There, the
  outer jail's `:443`, `:1460` and `:1461` are already held (MEASURED 2026-09-27 from
  `/proc/net/tcp` in a bridged jail).
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
| Wire bridge | The credential slot each bridged derive already fills: `ANTHROPIC_AUTH_TOKEN`, `COPILOT_PROVIDER_API_KEY`, `apiKey` for pi, oh-omp and opencode, and `env_key` for codex | A constant-time compare of the inbound `Authorization`. **In flight elsewhere.** It amends [WB-D4](../reference/wire-bridge.md#wb-d4) and also closes claude-on-`codex` sending its subscription bearer to `:8215` with no token set |
| OpenAI auth adapter (both halves) | The refresh marker the auth.json writer already mints, extended from `yolo-broker:<generation>` to carry the secret. Both notches already write Codex's auth.json themselves (`openauthclient.WriteCodexAuth`, `prepareCodexHome`), and Codex sends that refresh token back unchanged | `Handler` rejects a marker without the secret before calling the broker, so the stale-caller arm can no longer answer a stranger |
| AWS credential adapter | `AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE`, pointing at the `0600` file (the AWS SDKs' container-credentials provider reads it; UNMEASURED per agent) | A constant-time compare of `Authorization`. This is [`OQ-CN7`](../design/provider-credential-scope.md#OQ-CN7)'s option (c), extended from aws-auth to every notch |
| Claude OAuth terminator | Nothing new. Claude already presents the refresh token from `~/.claude/.credentials.json`, which is a symlink to the shared file | Answer only when the presented refresh token matches the shared file's current one, or the one it just replaced (the refresh race). A caller who cannot read the file gets a 401 |

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
| A4 | Config-change approval | the `--accept-config-changes` flag | `YOLO_ACCEPT_CONFIG_CHANGES` only; the explicit `yolo host [flags] --` refuses the flag | as jail | One approval, two spellings, each refused at the other notch | Accept the flag on the explicit host spelling. The env var stays for the wrapper path only | — (both [OQ-D2](../reference/config-safety.md#oq-d2) and [OQ-HS10](../reference/host-apply-staleness.md#why-its-this-way) hold) |
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
| 1 | Per-launch caller secret for the bridge, both OpenAI adapter halves, the AWS adapter and the terminator ([§2.3](#23-the-fix-every-service-authenticates-its-caller-at-every-notch)) | E1, C2's injection half | **behavior-changing, ruled** (2026-09-27). The bridge part is in flight | — | A caller without the secret is refused at every notch and service; the selecting agent is served |
| 2 | Served addresses composed from the serving daemon; ephemeral ports where the client takes a composed address; one "served at this notch" predicate feeding one provider-composition helper | C1, C2, the macos-user dead addresses | **pure merge.** The host and macos-user converge on "dropped and named", which is ES-D18's rule generalized | 1 | No notch exports an address nothing serves. `yolo check` predicts per runtime |
| 3 | Run the selected packs' services at a service-less notch | E2 | **behavior-changing, gated** on [OQ-NC1](#OQ-NC1) | 1, 2 | `-p cerebras -- claude` works at the host and on macos-user, or refuses identically at both |
| 4 | The terminator on a shared namespace | E3 | **behavior-changing, gated** on [OQ-NC2](#OQ-NC2) | 1 | — |

### Tier 2 — one selection (P1, P2)

| # | Item | Closes | Kind | After | Done when |
|---|---|---|---|---|---|
| 5 | One pack resolver with two modes; the host stages every configured pack through `packstage.Stage`; one embedded materialization | B3, B6, B7 | **pure merge** | — | An `exclude`d skill is absent at both notches. An escaping symlink refuses both |
| 6 | One selection function, the closure included, at every host verb; `LoadPacks` returns entry problems and launch-shaped verbs refuse | B1, B4 | **pure merge** (NC-D4, NC-D5) | **2** | `["claude"]` composes four packs at every notch, and host `-p bedrock` exports no unserved pointer |
| 7 | One installable-program predicate that honors `platforms` | B5 | **pure merge** | 5 | Host apply offers no install the vendor does not publish |
| 8 | Pack precedence computed once and recorded | B2 | **behavior-changing, gated** on [OQ-NC4](#OQ-NC4) | 6 | One winner for one key at the launcher, the boot and the host |

### Tier 3 — one front door (P2)

| # | Item | Closes | Kind | After | Done when |
|---|---|---|---|---|---|
| 9 | One value-flag reader (the `-p` grammar is in flight as ES-D27) | A1, A2 | **pure merge** | — | `--profile=` refuses with rc 2 at both notches, and `-p=zai` works at both |
| 10 | The notch decided once at the front door; `--accept-config-changes` on the explicit host spelling | A3, A4 | **pure merge** | 9 | Every `--at host` spelling routes the same way |
| 11 | One profile table builder; delivery narrows it to the launched process | A6 | **behavior-changing, gated** on [OQ-NC5](#OQ-NC5) | 6, 9 | One recipient rule, named in `--help` at both notches |
| 12 | `--with-credentials` in a jail | A5 | **behavior-changing, gated** on [`OQ-ES5`](../design/credential-sources-separation.md#OQ-ES5)'s jail half | 11 | — |

### Tier 4 — the host runs the jail's checks (P1, P4)

| # | Item | Closes | Kind | After | Done when |
|---|---|---|---|---|---|
| 13 | The [OQ-SSO8](../design/sso-backed-bedrock.md#OQ-SSO8) override check, provider and profile validation, and the profile disclosure line at the host | A7, A8 | **pure merge** | 6 | The measured bearer-plus-pointer host launch refuses, as it does in a jail |
| 14 | One credential refusal and disclosure renderer | C7 | **pure merge** | — | Byte-identical refusal bodies at both notches, apart from the named spelling |
| 15 | The declarative OpenAI prelaunch at the host, logging in only at a TTY | C6 | **pure merge** | 6 | `yolo host -p zai -- pi </dev/null` never starts a login |

### Tier 5 — one composition order and vehicle

| # | Item | Closes | Kind | After | Done when |
|---|---|---|---|---|---|
| 16 | One ordered env composition, serialized by every vehicle | C3 | **behavior-changing, gated** on [`OQ-CN8`](../design/provider-credential-scope.md#OQ-CN8) | 13 | One key has one winner at every vehicle |
| 17 | Per-agent env files on macos-user | C4 | **behavior-changing, gated** on [`OQ-CN9`](../design/provider-credential-scope.md#OQ-CN9) | 16 | — |
| 18 | One scope reader for credential-bearing keys | C5 | **behavior-changing, gated** on [`OQ-AS3`](../research/agent-safehouse.md#OQ-AS3) and [OQ-NC6](#OQ-NC6) | — | — |

### Tier 6 — render

| # | Item | Closes | Kind | After | Done when |
|---|---|---|---|---|---|
| 19 | Relay `YOLO_PROFILES` on macos-user through one exported wire-table list | D2 | **pure merge** (a live defect; it may land at any time) | — | codex on macos-user renders its `model_provider` |
| 20 | `InjectLaunchFlags` takes the autonomy bit | D9 | **pure merge** | — | A `guarded.launch` entry reaches a host launch |
| 21 | `files` on macos-user; `ResolveDestinations` for addressed files everywhere | D8 | **pure merge** | — | pi's two extensions exist in a macos-user home |
| 22 | One boot step table for Linux and macos-user | D10 | **pure merge** | — | An omission is a declared exclusion, never a missing line |
| 23 | `config render --at host` previews what host apply writes | D5 | **pure merge** | — | The preview and the write agree byte for byte |
| 24 | One render loop | D3 | **pure merge** (a refactor) | 23 | Four loops become one |
| 25 | One skills composer | D6 | **behavior-changing, ruled** (S1, [§6a-2](../reference/pack-system.md#batch-6a)): a jail collision becomes fatal | 24 | The same collision refuses at both notches |
| 26 | One briefing composer with a notch-aware base; `agents_md_extra` at the host | D7 | **behavior-changing, ruled** ([§6a](../reference/pack-system.md#batch-6a), Phase 8) | 24 | A host agent is told it is on the real machine |
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
| Per-launch caller auth for the wire bridge, amending [WB-D4](../reference/wire-bridge.md#wb-d4) | **in flight**, built in parallel; not on any branch visible here | E1, item 1 |
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

## 6. Open questions

1. 💬 <a id="OQ-NC1"></a>**OQ-NC1: Does every notch run the selected packs' services?** The host and
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

   <!-- vantage: oq id=OQ-NC1 leaning="A, run them launch-owned: it is the openaiauthhost shape already shipped for host Codex, it makes one declaration mean one thing at every notch, and item 1 makes a host-loopback service no weaker than a jail's; OQ-OA6 then takes route (b)." -->

   _Leaning:_ A. It is the shape already shipped for host Codex, and it is what "host is supposed to
   act like everywhere else" asks for. After item 1, a service on the host's loopback is no weaker
   than one in a jail.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 <a id="OQ-NC2"></a>**OQ-NC2: What does the Claude OAuth terminator do on a shared network
   namespace?** It must listen on `127.0.0.1:443`, because `--add-host` maps `platform.claude.com`
   to loopback and the port cannot move. On `network.mode: "host"` or a nested jail, a second jail
   finds `:443` held, or binds the host's own. This decides whether such a jail's claude refreshes
   through the broker.

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
   > _(empty — fill in when decided)_

3. 💬 <a id="OQ-NC3"></a>**OQ-NC3: Does a jail on a shared network namespace keep the autonomous
   posture?** `render.ProfileFor` keys autonomy on the notch alone
   ([env-manager's autonomy ruling](../design/yolo-as-environment-manager.md#42-agent-autonomy-is-a-confinement-policy-not-baked-pack-config)).
   A `network.mode: "host"` jail and every macos-user jail reach the host's loopback exactly as a
   host agent does, yet render `--dangerously-skip-permissions`.

   - **A — Keep autonomy, and disclose.** A network primitive in `render.Profile` records whether
     the namespace is shared, and the launch line and briefing read it.
   - **B — Guarded whenever the namespace is shared.** Cost: macos-user becomes guarded as a whole
     backend, which reverses the autonomy ruling for it.

   <!-- vantage: oq id=OQ-NC3 leaning="A: keep autonomy and disclose it from a network primitive in render.Profile; autonomy rests on filesystem confinement, item 1 closes yolo's own listeners, and B would make a whole backend guarded." -->

   _Leaning:_ A. Autonomy rests on filesystem confinement, which a shared network does not remove,
   and item 1 closes yolo's own listeners.

   **Answer:**
   > _(empty — fill in when decided)_

4. 💬 <a id="OQ-NC4"></a>**OQ-NC4: Which order does "later wins" follow?** The launcher, the boot and
   the host order packs three ways, so one key has three winners (row B2). This decides item 8.

   - **A — Config order as written, then closure additions, then the local pack last.** That is
     [pack-system.md's config-overlay rule](../reference/pack-system.md) and `localPackEntry`'s
     comment, and it is the host's order today.
   - **B — Embedded packs first, then configured, then the closure.** That is the launcher's order
     today, so a user pack can override a shipped one wherever it is listed.
   - **C — Alphabetical,** which is what the boot does by accident.

   <!-- vantage: oq id=OQ-NC4 leaning="A: config order, closure additions after the list, the local pack last — what the docs and localPackEntry already promise, and the only order a user can see by reading their own config." -->

   _Leaning:_ A. It is the only order a user can predict from their own config.

   **Answer:**
   > _(empty — fill in when decided)_

5. 💬 <a id="OQ-NC5"></a>**OQ-NC5: Who does a bare `-p <name>` reach?** In a jail it reaches every
   installed agent CLI and never the `--` command ([pv-oq-5](../reference/providers.md#pv-oq-5)). At
   the host it reaches whatever command runs, `bash` included (ES-D1, an implementation decision).
   This decides item 11 and the host help's `yolo host -p zai -- curl` example.

   - **A — pv-oq-5 everywhere.** A command no pack installs gets keys only through
     `--with-credentials`, the grant [OQ-ES5](../design/credential-sources-separation.md#OQ-ES5) ruled for exactly that. ES-D1 retires, and the host's
     refusal names the grant.
   - **B — The host meaning everywhere.** A bare name also keys the launched `--` command, so a jail
     `yolo -p zai -- bash` hands bash `ZAI_API_KEY`. That reverses pv-oq-5's never-the-command half.

   <!-- vantage: oq id=OQ-NC5 leaning="A: pv-oq-5 at every notch; the ruled --with-credentials grant is the one way to hand an arbitrary command a key, so ES-D1's any-basename meaning retires and the refusal names the grant." -->

   _Leaning:_ A. The grant exists now and is ruled, so a second, implicit route for the same thing
   is the duplicate path this plan removes.

   **Answer:**
   > _(empty — fill in when decided)_

6. 💬 <a id="OQ-NC6"></a>**OQ-NC6: Is `providers.*.api_key_env_name` user-scope only, as `base_url`
   is?** [`OQ-LM3`](../research/local-model-endpoints.md#oq-lm3) made addresses user-scope only. The
   rest of a provider entry still merges in from the workspace, so a repo file can re-point which
   variable a provider claims and sends upstream. [`OQ-AS3`](../research/agent-safehouse.md#OQ-AS3)
   asks the same question of `env_sources`.

   - **A — User scope only**, refused at workspace scope like `base_url`.
   - **B — Merge, and disclose** a workspace value that changes a claim.

   <!-- vantage: oq id=OQ-NC6 leaning="A: every provider field that decides where a credential goes is user-scope only; OQ-LM3's reason (the blast radius is total) applies unchanged." -->

   _Leaning:_ A. [OQ-LM3](../research/local-model-endpoints.md#oq-lm3)'s reason applies unchanged: the blast radius is total.

   **Answer:**
   > _(empty — fill in when decided)_

7. 💬 <a id="OQ-NC7"></a>**OQ-NC7: Does host claude join the jails' Claude login?** OpenAI has one
   machine-wide login lineage for the host and the jails. Claude has two, because the host notch
   cannot emit the hosts entry that interception needs.

   - **A — No, for now.** Host claude keeps its own login. Revisit alongside
     [`OQ-CI1`](../reference/claude-oauth-interposition.md#oq-ci1).
   - **B — A managed config dir** that shares the machine store. Its refreshes then race the broker
     unless the host routes them through it, and there is no mechanism for that yet.

   <!-- vantage: oq id=OQ-NC7 leaning="A: keep host claude on its own login until OQ-CI1 decides whether the credential is shared at all; a shared store without interception races the broker." -->

   _Leaning:_ A. A shared store without interception is worse than two lineages.

   **Answer:**
   > _(empty — fill in when decided)_

8. 💬 <a id="OQ-NC8"></a>**OQ-NC8: What do `host_files` and `mise_tools` do at the host?** Both
   render in every jail and are silently inert at the host. That breaks P4 whichever way this is
   answered.

   - **A — Render source-less `host_files` entries at the host** through the same surface engine,
     under `host_management`. Name source-bearing entries and `mise_tools` as inert.
   - **B — Name both as inert** in host apply's notch line now. Revisit `host_files` once
     [`OQ-CO14`](../design/config-ownership-and-promotion.md#oq-co14) settles host ownership. Host
     tools belong to [`OQ-PS1`](../design/provisioner-sets.md#OQ-PS1).

   <!-- vantage: oq id=OQ-NC8 leaning="B: name both inert in host apply's notch line now, so nothing is silent; render source-less host_files once OQ-CO14 settles host ownership, and leave host tools to OQ-PS1." -->

   _Leaning:_ B. It ends the silence today without choosing an ownership model early.

   **Answer:**
   > _(empty — fill in when decided)_

## 7. Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| NC-D1 | **Maintainer ruling.** Merge the host and jail notches as far as possible: one description, one code path per concern, the notch as an input. "Parsing profiles should be the same", and "host is supposed to act like everywhere else" | 2026-09-27 | [§1](#1-the-thesis) | — |
| NC-D2 | **Maintainer ruling.** A loopback service's safety never rests on the jail's network namespace ("jails … can be house type and then it's identical … solve it in both places"). This retires the premise of [WB-D4](../reference/wire-bridge.md#wb-d4) and the awscredadapter "no token" design | 2026-09-27 | [§2.2](#22-why-that-premise-is-false-in-a-jail-too) | — (the bridge is in flight) |
| NC-D3 | *Implementation decision.* The per-service mechanisms of [§2.3](#23-the-fix-every-service-authenticates-its-caller-at-every-notch): each client carries the secret in a slot it already has, so no agent changes, and the terminator authenticates by refresh-token match | 2026-09-28 | [§2.3](#23-the-fix-every-service-authenticates-its-caller-at-every-notch) | — |
| NC-D4 | *Implementation decision.* The host's selection closure (item 6) lands after served-address composition (item 2), which removes ES-D24's standing reason for not applying it | 2026-09-28 | [§5](#5-already-merged-and-in-flight) | — |
| NC-D5 | *Implementation decision, on NC-D1.* Launch-shaped verbs (`yolo --`, `yolo host --`, `yolo host env`) refuse a malformed or unresolvable pack entry at every notch, and read-only verbs report it. This overrides NS-D14's per-verb host dispositions | 2026-09-28 | [§3.2](#32-packs) | — |
| NC-D6 | *Implementation decision.* Host-loopback forwarding into bridged jails stays all-port. Reaching a host service is a documented feature, and item 1, not narrower forwarding, is what closes yolo's own listeners | 2026-09-28 | [§2.2](#22-why-that-premise-is-false-in-a-jail-too) | ✅ (unchanged) |
