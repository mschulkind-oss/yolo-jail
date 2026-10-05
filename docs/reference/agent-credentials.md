---
status: current
stage: CURRENT
verified: 2026-10-01
verified_commit: d4e435a3
covers:
  - internal/cli/run/assemble.go
  - internal/cli/run/assemble_parts.go
  - internal/cli/run/hostfiles.go
  - internal/cli/run/packhostgrants.go
  - internal/cli/run/userenv.go
  - internal/cli/run/agentenvfiles.go
  - internal/cli/run/claudecredentialview.go
  - internal/cli/run/claudesecurestorage.go
  - internal/cli/run/macosctxtree.go
  - internal/cli/claudeauth.go
  - internal/config/envsources.go
  - internal/config/hostfiles.go
  - internal/entrypoint/sharedlink.go
  - internal/entrypoint/identity.go
  - internal/broker/
  - internal/claudeview/
  - internal/oauthbroker/
  - internal/oauthterminator/
  - internal/openaiauth/
  - internal/openaiauthdaemon/
  - internal/openauthclient/
  - internal/openaiauthadapter/
  - internal/openaiauthhost/
  - internal/cli/run/openaiauthmigration.go
  - packs/pi/extensions/yolo-openai-auth.js
  - packs/opencode/plugins/yolo-openai-auth.js
  - internal/macosuser/seatbelt.go
  - internal/macosuser/envfile.go
  - internal/loopholes/guestrun.go
  - internal/awsauth/
  - internal/awsauthdaemon/
  - internal/awscredadapter/
  - internal/packdecl/envoverride.go
  - internal/packload/envoverride.go
  - packs/claude/loopholes/claude-oauth-broker/
  - packs/openai-auth/
  - packs/aws-auth/
tags: [credentials, security, boundary, env_sources, host_files, broker, oauth, aws, bedrock, sso]
---

# Agent credentials — what crosses the jail boundary, and how

**Status:** verified 2026-10-01 against `d4e435a3`, the whole doc. A runtime claim says whether
anyone has watched it run: `MEASURED` where someone has, `UNMEASURED` where no one has, which
covers most of the `macos-user` column and opencode on the OpenAI service. The
[OpenAI service section](#the-openai-subscription-credential-service), its `OQ-OA` and `OA-D1`
rows in [Why it's this way](#why-its-this-way) and its current values were rewritten against
`d4e435a3` the same day, when the design `openai-auth-broker.md` graduated into them.

yolo-jail's credential story is **structural, not a policy one**: host credentials are
*physically absent* from the jail, and the only credentials an agent can reach are ones a human
deliberately provisioned *into* the jail through a small set of explicit channels. There is no
deny-read list to get right — `~/.ssh`, `~/.gitconfig`, `~/.aws` and cloud/gh tokens are simply
not mounted, or on `macos-user` are actively denied by the Seatbelt profile. This doc is the
enumeration of those channels and of what each one does and does not carry.

| Component | Lives in |
| :--- | :--- |
| Mount assembly — where omission is the boundary | `internal/cli/run` (`assemble.go`, `assemble_parts.go`) |
| Per-agent host-file grants (`reads-host`) | `internal/cli/run` (`hostFileArgs`); `packload.Pack.HonoredHostFiles` |
| User-declared host files (`host_files`) | `internal/config` (`LoadHostFiles`, `validateHostFiles`, `hostFileReservedDests`); `internal/cli/run` (`hostUserFileArgs`) |
| The secret channel (`env_sources`) | `internal/config` (`ResolveEnvSources`, `ParseDotenv`); `internal/cli/run` (`userenv.go`, `agentenvfiles.go`); `internal/macosuser` (`envfile.go`) |
| Composed agent surfaces, and the boot-time compose | `internal/entrypoint` (`prism.go`), `internal/agentcfg` |
| Git identity — the two-key allowlist | `internal/cli/run` (`composeGitconfig`), `internal/entrypoint` (`identity.go`) |
| The Claude OAuth broker daemon, its flock and its canonical login | `internal/broker`, `internal/oauthbroker` (`RefreshLockPath`, `ConfigureStore`) |
| The opt-in Claude credential view | `internal/claudeview` (`SwitchEnv`), `internal/oauthbroker` (`views.go`), `internal/cli/run` (`claudecredentialview.go`) |
| The OpenAI credential service and agent views | `internal/openaiauth`, `internal/openaiauthdaemon`, `internal/openauthclient`, `internal/openaiauthadapter`, `internal/openaiauthhost` |
| The AWS credential service and its jail adapter | `internal/awsauth` (mint, cache, lock, narrowing), `internal/awsauthdaemon`, `internal/awscredadapter`; the pack is `packs/aws-auth` |
| The pack-declared override rule the AWS pointer uses | `internal/packdecl` (`EnvOverride`), `internal/packload` (`EnvOverrideFindings`) |
| The shared-credentials symlink | `internal/entrypoint` (`linkThroughShared`, applied through the `shared_credentials` hook) |
| The jail-facing hop for a host daemon | `internal/svcendpoint` (`ServeFrontWithOptions`) |
| The `macos-user` Seatbelt profile | `internal/macosuser` (`seatbelt.go`) |
| Which jail daemons the `macos-user` guest runs | `internal/loopholes` (`JailDaemonsRunIn`) |
| The `macos-user` copy of host files into `/ctx` | `internal/cli/run` (`macosctxtree.go`) |
| The canonical boundary prose every jail is told | `internal/jailcontent` (`BriefingContent`) |

**Reads with:** [`jail-home.md`](jail-home.md) (how the jail home is composed and where creds
land), [`git-identity.md`](git-identity.md) (the identity allowlist and its mechanism),
[`loophole-system.md`](loophole-system.md) and
[`loophole-transport.md`](loophole-transport.md) (the endpoint file, the front, and why
loopback-TLS is the only hop), [`storage-and-config.md`](storage-and-config.md) (state
separation), [`../design/macos-user-build-step-threat-model.md`](../design/macos-user-build-step-threat-model.md)
(the one place a prior session's bytes flow back to a more privileged context).

For `env_sources`, `host_files` and `host_services` as config keys — schema, precedence,
scope rules — run `yolo config-ref`.

---

## The credential boundary

The canonical statement lives in the auto-generated per-jail briefing and is mirrored into each
agent's briefing file verbatim:

> Host credentials are not propagated into the jail: the host's `~/.ssh`, `~/.gitconfig`, and
> cloud/gh tokens are invisible here. This is a **credential boundary, not a network block** —
> outbound SSH and HTTPS work normally, so git push/pull and API calls succeed whenever the jail
> has its own credentials (e.g. a workspace-specific deploy key or a token in `.env`). Only
> without such jail-local credentials do authenticated operations fail.

Two consequences worth stating plainly:

- **The network is open; the wallet is empty.** Bridge networking is the default, so the jail can
  reach the internet and SSH out. What it lacks is *identity* — a private key, a PAT, an OAuth
  token. A `git push` fails not because the packets are blocked but because the jail holds no
  credential the remote will accept.
- **Nothing is stripped; things are never added.** The design is an allowlist everywhere it can
  be. Git identity is the clearest case: rather than mount the host's `~/.gitconfig` and scrub
  credentials out of it, yolo composes a fresh file containing only the keys it names.

On the container backends the boundary is enforced by **omission at the mount-assembly layer**:
the host home is never a mount source. Only the yolo-managed global home and the per-workspace
`.yolo/home` overlay are. On `macos-user` there is no mount layer at all, so the boundary is
enforced by the Seatbelt profile's read denies instead.

## Invariants

- **A new credential channel is a change to the mount-assembly layer or to the compose path, and
  nowhere else.** The channels enumerated below are the complete set; anything that reaches the
  jail reaches it through one of them. That is what makes the enumeration worth writing down at
  all — a crossing outside it would appear in no report and no audit.

- **Which host files cross *for an agent surface* is yolo-shipped code, not a config knob.** It
  is each pack's `reads-host` contributions **plus the `readsHost` field of its own config
  surfaces** (the declaration a surface's host layer moved onto on 2026-09-12), both read
  through the one accessor `packload.Pack.HonoredHostFiles`. A
  pack's declaration is data yolo ships; the retired `host_claude_files`/`host_pi_files` keys let
  a *workspace* config widen this, and that was the hole closing them bought.

- **A source-bearing `host_files` entry is expressible only at user scope, by construction.** Of
  the places a config key can come from, two are jail-writable — the workspace
  `yolo-jail{,.local}.jsonc` and the assembled workspace config, both on the read-write
  `/workspace` bind. `config.LoadHostFiles` reads source-bearing entries only from the user config
  path, so workspace scope is *inexpressible* rather than merely refused. `validateHostFiles`'s
  hard error on a source-bearing workspace entry is defense-in-depth against a silent no-op, not
  the boundary itself.

- **A second refresh owner *is* the race each OAuth service exists to prevent.** The Claude and
  OpenAI services each use one host-wide lock and reload their canonical file while holding it;
  per-jail processes only adapt the agent protocol. See [the Claude broker](#the-claude-oauth-broker)
  and [the OpenAI service](#the-openai-subscription-credential-service).

- **`host_files` cannot reach a yolo-owned destination.** No entry may target a path yolo owns as
  a single file or symlink, nor any yolo-composed agent surface (`hostFileReservedDests`), so the
  channel cannot be used to overwrite `~/.claude/settings.json` and strip its managed block.

- **Only Claude writes authentication back through an agent overlay into the global home.** The
  OpenAI credential service shares Codex, Pi and opencode authentication through separate
  canonical state and generated agent views; it never makes any workspace overlay authoritative.

## The delivery channels

### `:ro` bind mounts

A host file mounted read-only into the jail, kernel-enforced against even the jail's root.
Container backends only. It carries the composed git config and global gitignore, the per-agent
host settings grant at `/ctx/host-<pack>/`, and user-declared host files at
`/ctx/host-user/<slug>`.

Apple Container **materializes** (copies) these into the workspace state dir instead, relying on
the whole-directory bind over the jail home. That is a choice, not a limit: `container` 1.1.0
binds a single file and honors `:ro` (measured 2026-09-14), and the copy is kept because it needs
no version gate (`acMaterialize` in `internal/cli/run` states the trade). The `:ro` guarantee is
lost there — the copy is writable — but the file is regenerated every run regardless.

### Composed surfaces at boot

The entrypoint re-runs pure generators on every boot and writes into the writable per-workspace
overlays. For credentials the relevant case is that **host agent settings are composed in**: the
surfaces that declare `readsHost` read their one host file from the `/ctx/host-<pack>/`
mount, **fail-closed** — a file the launcher reports as delivered that the jail cannot read
refuses the boot, while a file the user simply does not have yields the defaults — and the
composed result lands on the
jail's own surface through the ordinary layer fold, so host changes propagate, jail-local edits
survive, and yolo-required keys win. This is also the delivery path for an API key written into an
agent's settings.

### `env_sources` — the sanctioned secret channel

`env_sources` is the **primary way jail-local credentials enter**: deploy-key passphrases,
provider API keys, cloud access keys. It is an ordered list; each entry is either an inline
key/value map or a path to a `KEY=VALUE` dotenv file, with `#` comments, quotes and an `export`
prefix all tolerated. Later entries win; a missing file warns and is skipped rather than failing
the run. Path resolution accepts `~`, absolute and workspace-relative forms.

The *resolver* is shared across backends; delivery is not.

- **Container backends** resolve host-side and write `export K=${K:-'v'}` lines into a file that
  is mounted (or materialized) into the jail and *sourced* by `.bashrc` and by the entrypoint. It
  is a file the jail sources, not a `-e` block on the container argv, so the values are
  re-derivable from that one file.
- **`macos-user`** resolves host-side and writes the same values into one per-session file,
  root-owned `0600` with a single `user:` ACL entry letting the sandbox account read it. The
  sandboxed argv names the file and carries no value: `env -i` there passes only the identity
  variables (`HOME`, `USER`, `SHELL`, `PATH` and the two that travel with `PATH`), and a plan
  check refuses any other pair (`envfile.go` in `internal/macosuser`). Until 2026-09-13 the
  values rode the argv.

On every backend, a value a selected provider claims as its credential is held back from the
shared delivery and goes to the env file of the agent whose profile selected that provider
([the credential gate](providers.md#the-credential-gate)). On `macos-user` the launched agent's
own values also ride its session file, since that backend runs one command per invocation.

> [!WARNING]
> **An empty resolve TRUNCATES the exported file; it must never be a no-op.** The file outlives
> the config that produced it and is a bind-mount source, so it cannot simply be deleted (podman
> refuses to start on a missing one). Leaving the previous render in place instead made removing
> `env_sources` unable to *revoke* a credential — the stale file kept exporting it through every
> subsequent rebuild. Revocation is truncate-in-place.

> [!WARNING]
> **Choosing `env_sources` as the secret channel is a decision, not a default.** Resolved values
> are written cleartext into the files that deliver them, every one `0600`, and are exported into
> the environment of every process that sources them — on the container backends, every shell in
> the jail. Any process running as the jail's user can read them. That is the cost of the
> channel; a reader weighing where to put a key should know it before picking this one.

**The `${VAR}` placeholder convention** ties `env_sources` to MCP, and **yolo does not do the
substituting.** An MCP server's `env` value written as `"${TAVILY_API_KEY}"` is passed through
verbatim, and the agent that launches the server resolves it from its own environment, where the
`env_sources` values already are — the entrypoint exports them before any generator runs. A
`requires_env` gate still drops the server entirely when the variable is empty. Interpolating in
yolo was removed deliberately: the expanded value had no provenance layer, and it sourced config
content from process env at render time. A secret still lives in one unsynced dotenv file and is
still scoped to exactly one MCP server; the resolution just happens one step later, at launch, by
the consumer. See [`mcp-configuration.md`](mcp-configuration.md).

### User-declared host files (`host_files`)

`host_files` lets a user bring **any** host file into the jail as a composed surface. It reopens,
deliberately and narrowly, what the retired per-agent keys had — so its boundary is **per entry**,
not per key:

| Entry kind | Crosses a host file? | Allowed scope |
| :--- | :--- | :--- |
| **source-bearing** — a bare string, or an object with `source` | yes | **user config only** |
| **source-less** — an object with `content`, or only `managed`/`defaults` | no | user **or** workspace |

A source-less entry copies nothing from the host — it just brings a yolo-managed file into being —
so a repo may legitimately ship one.

**Within the user's own config nothing is blocked.** Listing a private key there is the human's
call: their machine, their decision, and a blocklist would be unenforceable anyway through
symlinks. The boundary is that the *repo* cannot make that choice on their behalf.

The resolved entry list travels to the entrypoint in an environment variable, so the host CLI is
the single source of truth and the entrypoint never re-reads config. `macos-user` carries the same
list, in two halves that come from different places: the source-less entries are read out of the
merged config by the pure plan builder, and the source-bearing ones are resolved by the host CLI,
which also **copies each file source** into a root-owned tree the sandbox reads and names it with
`YOLO_CTX_ROOT`. There is still no bind mount anywhere on that backend; the bytes arrive by copy,
and the same root-owned tree carries each pack's `reads-host` grant. One shape is skipped there —
a `source` naming a **directory**, which a copy does not scale to — and the launch names it in a
warning rather than dropping it in silence.

### The Claude OAuth broker

**What is on the wire — which single hostname is intercepted, which single grant is terminated, why
a credential *file* is involved at all, and the open question of whether to share the credential —
is [`claude-oauth-interposition.md`](claude-oauth-interposition.md)'s.** This section owns the
broker's place among the credential channels, its rulings, and the threshold defect below.

The broker exists because **yolo moved the credentials file out from under the vendor's own
cross-process lock.** Anthropic mints single-use refresh tokens, so two Claudes refreshing in the
same window burn one another's token — and Claude Code already solves that for itself. It takes a
real cross-process lock before every refresh (`acquireOAuthRefreshLock`), and on a plain host that
lock and the file it protects sit in one directory, which is why a host Claude never loses its
login to a race no matter how many sessions run.

**Until [CL-D22](../design/claude-login-without-interception.md#CL-D22), yolo split that pair.**
`packs/claude/pack.json` declares `.claude` as `scope: "workspace"` and `.claude-shared-credentials`
as `scope: "machine"`, joined by a relative symlink, so the credentials file was shared by every
jail while `~/.claude` stayed per-jail. The vendor's lock is derived from a directory, not from the
credentials path:

```js
// Claude Code 2.1.278, measured
function Jar(e,n){return{lockfilePath:lE(e,".oauth_refresh.lock"),realpath:!1,stale:60000,update:5000,...}}
```

With nothing else set, that directory is `~/.claude`, so the lock landed at
`~/.claude/.oauth_refresh.lock`, a **per-jail inode**, and two jails refreshing at once contended
on nothing. **We shared the file and not the lock,** and the broker is what put serialization back.
(The vendor takes a second lock, and `realpath:!1` is not what keeps either one off the shared
file: [`claude-oauth-interposition.md`](claude-oauth-interposition.md#why-a-broker-exists) has
both corrections.)

**Since CL-D22 the pair is whole again, in the machine-scope directory.** The one directory Claude
keeps its credential file, its refresh lock and its write lock in is
`CLAUDE_SECURESTORAGE_CONFIG_DIR ?? CLAUDE_CONFIG_DIR ?? ~/.claude` (MEASURED, 2.1.284), and every
jail launch but a credential-view launch now sets the first to `.claude-shared-credentials` under
the jail home ([CL-D23](../design/claude-login-without-interception.md#CL-D23)). So the file, the
refresh lock `.oauth_refresh.lock` and the write lock `.storage-write.lock` sit in one directory
that every jail on the machine opens, and the broker takes that write lock for its own writes of
the shared file ([CL-D25](../design/claude-login-without-interception.md#CL-D25)). Whether
Claude's own lock then keeps two jails apart depends on how each backend reaches that directory:

| Backend | How a jail reaches the machine-scope directory | Claude's own lock across jails |
| :--- | :--- | :--- |
| podman | one host directory, bound read-write into every jail | contends (INFERRED): the lock is a `mkdir`, answered by the one host kernel for every jail. The broker's interception serializes the refresh as well |
| `macos-user` | one real directory in the sandbox account's home, no mount | contends (INFERRED): one kernel, one directory |
| Apple Container | one host directory, reached from each jail's own VM over virtiofs | **unmeasured**: whether a `mkdir` in one VM is seen at once by another, and whether the lock's modification time, which its stale check reads, is current across VMs |

> [!IMPORTANT]
> **Rejoining the pair did not retire the case for a host-side lock.** CL-D22 machine-scoped the
> lock directory knowingly, as a bridge until the credential view replaces the interception. A
> lock in a shared directory is still **backend-dependent**: it contends on podman and
> `macos-user` by inference, and nobody has run it across two Apple Container VMs, where each
> jail's kernel is its own. A host-side mediator reached over a socket is not backend-dependent,
> and that, not one-ness and not a credential boundary, is the property the broker is bought for
> and the view keeps
> ([`claude-login-without-interception.md`](../design/claude-login-without-interception.md)).

It bundles two jobs.

1. **One shared credentials file per host.** On containers that is a shared-credentials bind plus
   an in-jail *relative* symlink from the agent's own credentials path into it
   (`linkThroughShared`, applied through the pack's `shared_credentials` hook), and since
   [CL-D22](../design/claude-login-without-interception.md#CL-D22) Claude's credential store
   pointed at that directory itself, so Claude opens the real file and reads nothing through the
   link. One OAuth identity, every jail.
2. **Serialize the refresh HTTP call.** A host-side daemon holds a flock
   (`oauthbroker.RefreshLockPath`) around every refresh, so concurrent jails cannot burn the
   token. What the job needs is that the lock be taken **host-side**, where every backend agrees
   on the inode; it does not need the daemon to be a singleton. Because Claude Code refreshes *itself* and will never voluntarily take yolo's lock, an
   in-jail TLS terminator intercepts the refresh host (via an `--add-host` mapping to loopback)
   and routes the call through a loopback-TLS front — a goroutine in the jail's keeper, the process
   the launching `yolo run` spawns to hold a container jail's host services for its life
   ([`jail-home.md`](jail-home.md)), splicing to the singleton's socket — to the daemon under its
   flock.

**Selecting the `claude` pack is the dependency.** The broker is a *contribution* of
`packs/claude`, not a pack of its own, because the dependency is structural. Its manifest ships
`default_enabled: true`, which is what makes selecting the pack *sufficient*. One other shipped
manifest does the same, `openai-auth-broker`, for the same reason: every other loophole is host
access you ask for, and these two keep a credential you already asked for from being burnt.

The broker keeps **one canonical login** for the machine, `claude-credentials.json` in its own
host-only state directory, which no launch mounts. It keeps two kinds of file in step with it,
each written under the refresh lock: the **shared credentials file** under the global home, which
an interception jail's Claude reads as its own store file, with the refresh token in it; and each
registered workspace's **credential view** (below). On its first locked operation a broker with no
canonical login yet adopts the shared file's login as the first one. The broker never writes host
Claude's own credentials. It reads the host user's `~/.claude/.credentials.json` only to relay a
file there that carries **no** refresh token, which is a nested jail's own view, and never relays
a real login. Host and in-jail Claude therefore keep **independent** OAuth identities, through
separate `/login` flows, which Anthropic permits.

**The credential view is the opt-in replacement for the interception.** A view is a per-workspace
`.credentials.json` the broker writes into the workspace's Claude directory: the current access
token and its real expiry, and **no refresh token**. Claude refreshes only when its stored
credential holds one, so a jail reading a view never contacts the token endpoint and the broker
is the machine's one refresher. A launch selects it with `YOLO_CLAUDE_CREDENTIAL_VIEW=1` in the
host environment; it is off by default on every backend (`claudeview.DefaultOn`). A view launch
drops the terminator, the intercepted hostname and the shared-file link, and registers the
workspace with the broker, which writes the view from the canonical login. A `/login` in that
jail leaves a view carrying a refresh token, which the broker adopts as the machine's login; a
`/logout` there signs out only that workspace, until its next launch. `yolo claude-auth logout`
signs the whole machine out, and `yolo claude-auth status` describes the canonical login, the
shared file and every view by fingerprint, never by token. UNMEASURED: no real Claude has run on
a view yet; the measures it waits on, and the deletion of the interception after them, are
[`claude-login-without-interception.md`](../design/claude-login-without-interception.md)'s.

> [!WARNING]
> **Do not reintroduce a host-side `command_on_path: claude` activation probe.** It stood in for
> *"is there a claude to refresh for"* and read **false for exactly the user yolo exists for**:
> someone who installs `claude` inside the jail via the lazy launcher and never on the host. Their
> loophole vanished from every surface with no reason given, silently taking the refresh
> serialization with it. Nothing replaces the probe — a loophole whose program is missing must
> fail **loudly at spawn**, not disappear from `yolo loopholes list`.

> [!WARNING]
> **`scope: "host"` requires `publishes: "socket"`, refused otherwise at load.** An endpoint file
> carries **one jail's** bearer token, so a host-wide publisher would hand every jail the same
> credential. And do not express "one daemon per machine" by testing the loophole's *name*, which
> is how the run pipeline used to do it — `scope` is the declaration.

> [!WARNING]
> **There is deliberately NO token environment variable for this hop.** An env var is inherited by
> every child process the in-jail terminator spawns. The jail's credential is the endpoint file
> alone, mode `0600`, named by its own `YOLO_SERVICE_*_ENDPOINT` variable. Pinned by
> `TestNoBrokerTokenEnvEmitted`, which asserts *"no token environment variable exists, at all."*

The broker's `{state}` directory is keyed by the loophole **name**, so an upgrading host keeps its
existing CA and every already-running jail keeps trusting it. Its `state_files` crosses only the
CA cert and the server cert/key, leaving the CA *key* host-side. Nothing protects the name by
reservation any more: what protects it is `packs/claude` **occupying** it, since loophole names are
sole-owned across packs, fatally, plus the origin gate.

There is a second off switch, and it is the right one for a user on a different auth route: the
manifest declares `serves: ["claude-oauth-refresh"]`, so a selected pack may `supersedes` that
capability with a reason. Under an auth route where no OAuth token is ever refreshed, the job does
not *exist* rather than being done differently — and this is the only off switch that does not
require editing a `loopholes:` block.

<a id="-the-90300-threshold-mismatch--a-live-defect"></a>

#### The refresh floor sits above Claude's own due threshold

**A refresh the broker answers with the caller's own token is, to Claude, a failed refresh**, so
the floor below which the broker mints must sit above the point at which Claude asks. The
measurements below are against Claude Code 2.1.278.

Claude considers a token due for refresh at **300 seconds** of remaining life:

```js
function eO(e,n=Date.now()){if(e===null)return!1;return n+300000>=e}
```

and its 401-recovery path reads an unchanged access token as exhaustion:

```js
if(Zt()?.accessToken===De){ if(bre()!==null||!$1(w)&&++_e>=Rmo) throw m("api_request","api_request_oauth_refresh_exhausted"), new Fd(w,g) } else _e=0
```

`Rmo` is **2**. On a plain host this branch is unreachable, because a real refresh always mints a
new access token. Until 2026-09-20 the broker's one floor was **90 seconds**, below Claude's 300,
so for 210 seconds of every token's life Claude asked for a refresh and got its own token back
with HTTP 200. Of 380 successful refresh replies in one jail's terminator log, **364 (96%)**
carried 90–299 seconds of life, and that window was ending sessions.

The floor answered two questions, and now there are two constants. `LiveTokenFloorMS` answers the
`cached` action's *is there a usable token*. `RefreshCacheFloorMS` answers the refresh path's
*should I mint*, and is **derived** from `ConsumerRefreshDueMS`, Claude's threshold recorded as a
fact about the client, so the inequality lives in the source rather than in two numbers that
happen to differ ([current values](#current-values)). A test fails if the derived floor ever sits
below the consumer's threshold, and a behavioural test asserts the two paths disagree inside the
old window. Claude still checks before every API call while the background refresher ticks once a
minute, so Claude is the one that acts first on its threshold; the floor is what makes that
harmless.

> [!WARNING]
> **`force` is dropped.** `IsRefreshGrant` inspects only `grant_type`, and the refresh path has
> no force parameter, so Claude's force-refresh cannot compel an upstream call from inside a
> jail. With the floor above Claude's threshold there is nothing for a force to override in normal
> operation; it matters only if Claude's threshold moves and `ConsumerRefreshDueMS` does not
> follow.

#### What the broker does *not* buy

Three beliefs about it were measured false, and each one had been load-bearing somewhere:

- **It is not what stops a jail spending a stale token.** That is the terminator, independently.
  It hands the broker the refresh token Claude presented, and the broker uses it only as proof of
  the caller: the token must be the shared file's current one, or one of the few the broker just
  replaced, or the answer is `caller_unauthenticated`, which the terminator returns as HTTP 401
  (`DoRefreshAsCaller`). The presented token is compared and never spent; the refresh is always
  made from the canonical login under the flock. The check exists because the terminator's port
  is a loopback port, which anything sharing that loopback can reach.
- **It does not need to be a singleton.** See the flock note above — host-side is the requirement.
- **It does not trigger the vendor's dead-token disk clear, and cannot surface a real one
  either.** The vendor classifies a dead token by the **top-level `error` string**, not the status
  code (`jd(e.response.data).code==="invalid_grant"`). None of the refresh path's codes —
  `creds_unreadable`, `caller_unauthenticated`, `no_refresh_token`, `upstream_http`,
  `upstream_bad_response`, `upstream_unreachable` — is that string, so the catastrophic
  shared-file blanking path is closed. The mirror is the cost: a **genuine** upstream
  `invalid_grant` is wrapped as `{"error":"upstream_http","body":"…"}`, so Claude cannot see a
  truly dead token either and retries instead of prompting a clean re-login.

<a id="login-fields"></a>

#### Two fields that belong to the login, not to the token

Claude's stored login holds two fields that describe the login rather than the access token in
hand. `refreshTokenExpiresAt` is the refresh token's deadline in epoch milliseconds, from which
Claude warns during a login's last three days that it is about to expire. `scopes` is the list of
OAuth scopes the login was granted, which Claude checks for `user:inference`.

Every time the broker writes new tokens, `NormalizeOAuth` sets both by one rule. Its own refreshes
count, and so does a jail's `/login`, whose code exchange passes through the broker's proxy.

- **The token response carries the field:** the broker takes it, `refresh_token_expires_in`
  when it is a number and `scope` when it names at least one scope.
- **The response carries none, and it rotated the previous record's own refresh token:** that is
  a refresh within one login, so the broker keeps the previous record's value.
- **Otherwise:** the field is left out, and `scopes` is never written as an empty list.

A `/login` redeems no refresh token, so the previous login's deadline and scopes never reach the
new one. Claude 2.1.288 also takes both from the response, and carries a stored deadline forward
only after checking that the stored record holds the refresh token it just redeemed (MEASURED in
its binary).

> [!WARNING]
> **Nothing in yolo shows the refresh-token deadline yet.** The broker keeps the field by the rule
> above, but neither its log nor `yolo claude-auth status` prints the value; `status` lists the
> field's name among the others. So a refresh-token expiry is undiagnosable after the fact unless
> someone reads the credentials file itself.

### The OpenAI subscription credential service

Codex, Pi and opencode use one machine-wide OpenAI subscription grant without sharing any agent's
whole home. The `openai-auth` pack owns a host singleton named `openai-auth-broker`, and every
agent pack that can use the subscription `needs` that pack, so selecting any of them selects the
same service rather than declaring a second refresh owner ([`OQ-OA1`](#oq-oa1)). Sharing the
credential files alone would race on a single-use refresh token; a login per workspace and agent
would avoid the race by making the user repeat the browser login every time.

<a id="openai-one-writer"></a>

#### One writer, and a view per agent

The host service is the **only writer** of the canonical credential: the access, identity and
refresh tokens, their expiry, the OpenAI account id, and a monotonically increasing
**generation**. The file lives under the loophole's host state directory and never crosses into a
jail. Login, refresh, replacement and logout all take one machine-wide file lock, and every update
is an atomic rename of a mode-`0600` file in a mode-`0700` directory.

A refresh takes the lock, reloads the canonical file, and then decides:

- a caller presenting an older generation than the current one is **stale**: it gets the current
  generation back, and OpenAI is not contacted, even when that generation is due;
- a current access token with more than the refresh lead left is returned **cached**;
- otherwise the service redeems the refresh token **exactly once**, persists the rotation, and
  answers.

Once a redemption may have started, the caller's cancellation no longer reaches it
(`context.WithoutCancel`): OpenAI may already have consumed the refresh token, so the service
finishes and persists the rotation before it releases the lock.

One prerelease build passed the literal relative path `{state}/credentials.json` to the daemon.
The next launch checks only two bounded locations for that file: the current workspace and, on
Linux, the recorded singleton process's working directory under `/proc`. While holding the same
singleton lock used for startup, it validates the file, moves it into the canonical private state
directory, removes the empty literal `{state}` directory, and replaces the daemon so later writes
use the canonical path (`prepareLegacyOpenAIAuthState`, `internal/cli/run`). It never searches
other workspaces. If both a legacy and canonical file exist, or more than one bounded candidate
exists, yolo refuses to choose and prints the paths: preserving both for a deliberate manual
choice is safer than overwriting a refresh authority.

No agent ever receives the canonical refresh token ([`OQ-OA2`](#oq-oa2)). Each gets a **view** of
the canonical state, shaped for its own client:

- **Codex** gets its native `auth.json` in the workspace home, with
  `yolo-broker:<generation>` where its schema requires a refresh token. Its supported refresh-URL
  override (`CODEX_REFRESH_TOKEN_URL_OVERRIDE`) points at a small HTTP adapter, which forwards the
  marker to the service over the authenticated host-service endpoint. A stale marker gets the
  current generation, so a stale Codex view repairs itself on its next refresh. The adapter's
  loopback port is reachable by every process on that loopback, so the launch also binds its
  per-launch caller token into the marker (`yolo-broker:<generation>.<token>`), and the adapter
  refuses a marker without it, with HTTP 401, before the service is asked anything. The service
  never sees the suffix.
- **Pi**'s `openai-codex` provider extension asks the service for an access token and its expiry,
  and writes them to the workspace provider record with `yolo-broker:<generation>` in Pi's
  required `refresh` field. Pi's normal refresh window calls the extension, which asks the service
  again, so concurrent workspaces each take their own Pi file lock and the service performs at
  most one upstream refresh among them. Pi's other provider credentials stay in its workspace
  `auth.json`.
- **opencode** gets Pi's view, merged under its own `openai` key in its own `auth.json`
  ([`OA-D1`](#oa-d1)). opencode files its own ChatGPT login and an OpenAI API key under that same
  key, so the view replaces either one, and the launch names what it replaced. opencode's own
  request `fetch` refreshes against a hard-coded `auth.openai.com` and keeps the refresh token it
  gets back, and no config key or variable moves it, so yolo's opencode plugin replaces that
  fetch, which a later plugin may do. The plugin asks the service for the access token within the
  refresh lead of its expiry, and opencode never sends a refresh. If the plugin fails to load,
  opencode's own fetch sends the marker, which is no credential, once the token expires.
  UNMEASURED: no opencode session has sent a request on the subscription.

Pi's and opencode's views are written only when `openai-codex` is an entry of that agent's active
provider set, keyed on the provider rather than on a profile's name, so a user's own profile over
`openai-codex` gets the view too. Codex's pack declares its view unconditionally.

> [!WARNING]
> **Pi's view refreshes before expiry and at no other time** ([`OQ-OA7`](#oq-oa7)). Pi calls a
> provider's refresh only from expiry-checked call sites, an unauthorized (HTTP 401) response is in
> none of its retry classifiers, and its extension API shows no response status, so an "ask once
> more after a 401" step cannot be built (measured against pi 0.87.0, re-read in 0.99.1). The
> service's proactive refresh covers what that step was for. The one gap is a token OpenAI rejects
> before its recorded expiry: pi reports that request as failed. Re-open this only if pi gives an
> extension a way to see a response's status.

The service also refreshes **proactively**, checking on a short tick and refreshing once the
canonical access token is within the refresh lead of expiry, so an idle agent or a machine waking
from sleep resumes with a current token. That replicates the Claude broker's semantics (a
machine-wide flock, reload under lock, stale-caller detection, cached return, one redemption,
atomic persistence, proactive refresh, fingerprint-only diagnostics) without its hostname
interception: Codex exposes a refresh-URL override and Pi a provider extension API, so no
OpenAI-signing CA is installed and no unrelated traffic is intercepted ([`OQ-OA5`](#oq-oa5)).

> [!NOTE]
> **Two flock transactions, not one shared engine.** The design once said the Claude and OpenAI
> brokers' engine would be generalized once, with provider-specific adapters. What shipped is
> `internal/openaiauth` beside `internal/oauthbroker`, each with its own lock transaction, and
> nothing depends on merging them.

<a id="openai-login"></a>

#### Login, and what a user sees

The first Codex, Pi or opencode launch without a credential prints one browser URL, and opens it
on the host when a browser opener exists. **The login runs in the host service**, not in the jail
([`OQ-OA4`](#oq-oa4)): the service binds a host loopback callback port, falling back to a second
one when another process holds the first, names the port it got in the redirect URI, accepts only
the exact callback path, compares the OAuth `state` before it takes the code, refuses a second
callback, exchanges the code with PKCE, and atomically replaces the canonical state. The jail's
login action only streams the URL back. So no callback is relayed into a jail and no per-jail host
port is reserved. ⚠ A third concurrent login finds no free port.

One login serves every agent and every workspace on the machine, and a new login replaces the old
grant atomically. Logout is explicit, machine-wide, and says so before it deletes anything
([Import and logout](#import-and-logout--the-hosts-two-verbs)).

<a id="openai-failure-and-recovery"></a>

#### Failure and recovery

- A **transient** upstream error leaves the canonical state unchanged and is returned as
  retryable. There is no automatic second redemption.
- A **permanent** refusal keeps the last state for diagnosis, marks the grant as needing a login,
  and stops further unattended redemption: the proactive refresh skips a grant marked that way.
- A **client disconnect** after a redemption may have started does not cancel it (above).
- A **service restart** reloads the persisted state; a pending login is lost, and a retry prints a
  fresh URL.

Status and logs carry token fingerprints, expiry, generation decisions, latency, caller identity
and upstream error metadata. A fingerprint is eight hexadecimal characters of a token's SHA-256
digest, enough to correlate generations without revealing the token. Token bodies, authorization
codes, PKCE verifiers and callback query strings are never logged or printed.

<a id="openai-backends"></a>

#### How each backend reaches it

The refresh algorithm is the same on every backend, and none intercepts `auth.openai.com`
([`OQ-OA5`](#oq-oa5)); only the route from an agent's adapter to the host service differs.

- **Container jails** use the per-jail authenticated loopback-TLS front and its endpoint file.
  The Codex adapter is the loophole's `jail_daemon`, inside the jail. Pi's extension and the
  opencode plugin call the same front.
- **`macos-user`** starts the same host singleton, and the sandboxed account reaches its front on
  the Mac's loopback, reading the endpoint credential from the sandbox-visible state the launch
  prepared. The Codex adapter is a **doorway** (the thin adapter that checks the launch's caller
  token and forwards to the host service): the launch opens it outside the sandbox as a listener
  it owns, on a port it picked, and closes it when the command exits ([`OQ-OA6`](#oq-oa6)). See
  the warning under [Import and logout](#import-and-logout--the-hosts-two-verbs).
- **`yolo host`** reaches the service through a private mode-`0600` Unix socket ([below](#import-and-logout--the-hosts-two-verbs)).

<a id="openai-measured"></a>

#### What has been watched running

MEASURED, as recorded on 2026-09-30: a brokered refresh through a jail's published endpoint on
rootless podman, on both architectures (`TestOpenAIAuthBrokerRoundTripsAnImportedToken`, `ci.yml`
run 36662086103), and the `macos-user` doorway on a hosted Mac, reachable from inside the sandbox,
refusing a refresh without the launch's caller token, admitting one bound to it, and gone when the
session ends (`TestMacosUserOpensTheCodexDoorwayOutsideTheSandbox`, `macos-user.yml` run
36719581090; it runs no Codex). opencode's view is MEASURED by unit tests only.

UNMEASURED, because each needs a ChatGPT account and wall-clock time, and so a person
([`../design/openai-auth-broker-plan.md`](../design/openai-auth-broker-plan.md) owns the checks):

- two Codex and two Pi processes crossing one expiry concurrently with exactly one upstream
  refresh;
- Codex and Pi working from one browser login in different workspaces;
- an opted-in host Codex and a jail Codex crossing one expiry without a reused-token failure;
- a browser login from a bridged-network jail with two logins pending at once;
- the same login and expiry crossing under `macos-user`, with no DNS or trust-store change.

### Import and logout — the host's two verbs

Import and machine-wide logout are absent from the **jail-facing** action protocol: either would
let any process in a selected jail change host-wide authentication. Since 2026-09-18 they exist
on the **host** one, and since 2026-09-20 they are a public verb: `yolo openai-auth import --from
<auth.json>` and `yolo openai-auth logout`, alongside `yolo openai-auth status`. The verb resolves
the daemon's private socket (starting the daemon if it is not running) and states that the
operation's scope is the whole machine before it acts.

**`yolo internal openai-auth` is retained as an alias** into the same handler — not a second copy
of it — and prints where the verb moved to before doing the work. The address was hidden until the
promotion, so nothing in this tree ever invoked it; what kept the old spelling is the asymmetry of
the failure, since a verb that manages credentials whose scripted spelling is deleted becomes a
credential operation that silently stops running.

**The socket is the authorization, and it has to be**, because a handler cannot see which socket
carried its bytes and `Session.JailID` falls back to the client's *self-asserted* value on the
private one. So the daemon builds **two handlers** — the jail-facing socket gets the one that
refuses these two actions, the private mode-`0600` socket gets the one that serves them — and the
difference is expressed where the sockets are bound rather than inside a handler that would have
to ask the untrusted side which side it is.

Import reads a Codex `auth.json` a human already has and installs it as the next generation. It
refuses a relative path, a symlink, a non-regular file, a file missing any of the three tokens,
and — the one worth knowing — **a yolo broker view**: the `auth.json` yolo writes for an agent
carries `yolo-broker:<generation>` where the refresh token goes, so importing one would replace
the canonical grant with a marker nothing can redeem. It does **not** refuse a lapsed access
token: the thing being imported is the refresh token, and the broker refreshes a generation that
is already due. Logout deletes the canonical state under the same lock and is idempotent.

Managed host launches share the service too, on the same trigger a jail's launcher reads: the
`YOLO_AUTH_PRELAUNCH_<BIN>_*` values the launched command's pack declares, in its `env`
contribution or its env derive, as the launch composes them
([notch convergence item 15](../plans/notch-convergence.md#tier-4--the-host-runs-the-jails-checks-p1-p4)).
So `yolo host -p codex -- pi` gives Pi's provider extension the private host Unix socket, and
`yolo host -p zai -- pi` does nothing of the kind, since pi's derive declares the prelaunch only
when `openai-codex` is in pi's active provider set; `yolo host -p codex -- opencode` hands
opencode's plugin the same socket on the same rule. At the host, opencode's `auth.json` is the
user's own and yolo does not write it, so the plugin offers the shared login in opencode's
`/connect`, and each such launch says so until that file holds the view
(`opencodeHostLoginNotice`, `internal/openaiauthhost`). `yolo host -- codex` writes the native credential view under
`<global storage>/host-agents/<the declaring pack>` (`codex` for the shipped pack), sets
`CODEX_HOME` to that directory, and starts a dynamic loopback refresh adapter. With no login, the
browser login starts only at a terminal; off one the launch says a login is required and runs
without the credential, as a jail's launcher does. Yolo supervises Codex and closes the adapter when Codex exits. The
generated Codex wrapper delegates to the same command. The managed home's `config.toml` is a
COPY of the ordinary host one, rebuilt at every launch with this launch's workspace trusted and
the daemon key below, so the ordinary `config.toml` is read and never written; `AGENTS.md` and
`skills` are links to the ordinary host Codex home when present; the managed home's own
`auth.json`, session state, and cache stay separate. A direct `codex` launch and `~/.codex/auth.json` are untouched
([`OQ-OA3`](#oq-oa3)): a host Codex shares the login only when yolo launches it, and one launched
directly keeps its own home and, if logged in there, a grant of its own. An explicit import can
seed the service from that file once; after it the two files are independent.
The managed launch also keeps Codex's background server off, since one would outlive the launch and
keep posting refreshes to its closed adapter: the managed `config.toml` sets
`features.daemon_auto_start = false`, the argv gains `--no-daemon` (disclosed; not beside `queue`
or `--remote`, which Codex refuses it with and which start no server, and deliberately beside
`agents`, which would start one and which Codex then refuses), the home's daemon settings turn
Codex's own updater off from the first launch, and a server or updater an earlier launch left
in that home is stopped once, when no other launch of it is live
([OQ-CDX1](../research/codex-background-service.md#OQ-CDX1), [CDX-D2](../research/codex-background-service.md#CDX-D2),
[CDX-D3](../research/codex-background-service.md#CDX-D3)).

> [!WARNING]
> The OpenAI service is a host-service loophole, and **Apple Container does not carry it end to
> end**: it is allow-listed out of an otherwise total loophole skip, so the daemon starts and the
> jail cannot reach it (measured on `container` 1.1.0). On `macos-user` the HOST half is not
> special — that arm starts every loophole's host daemon through the ordinary spawn boundary — and
> the JAIL half arrives by another route. That backend runs jail daemons in its Seatbelt guest,
> but the refresh adapter declares a host argv (`jail_daemon.host_cmd`), so since 2026-09-29
> (`fea3b6c7`) the launch opens it outside the sandbox instead, as a listener it owns on the Mac's
> loopback, and `CODEX_REFRESH_TOKEN_URL_OVERRIDE` names that port
> ([`host-notch-services.md` HS-D15](../design/host-notch-services.md#HS-D15),
> [the sandbox's decline list](macos-user-nix-and-features.md#the-jail-daemons-run-in-the-sandbox)). MEASURED on a
> hosted Mac on 2026-09-30 (`TestMacosUserOpensTheCodexDoorwayOutsideTheSandbox`, `macos-user.yml`
> run 36719581090), with no Codex run through it. Starting a service is still not the same as the
> jail reaching it, and the agent pack dependency alone creates no second credential path. See
> [the backend table](#per-backend-differences) for what each backend actually delivers.

### SSO-backed Bedrock credentials (`aws-auth`)

The `aws-auth` pack turns a human's `aws sso login` on the host into Bedrock access inside a
jail, without the jail holding the SSO session or anything else the login can reach. A host
service, one per machine, mints a short-lived AWS credential from the live session and
**narrows** it, which here means making it smaller than what the login can do, before it leaves
the host. An adapter inside the jail hands that credential to the agent's AWS SDK when the SDK
asks. What crosses the boundary is a **pointer**, a URL naming the adapter, and the credential
is fetched behind it at use time. That is what lets a re-login on the host reach a jail that is
already running: nothing in the jail was fixed at launch, so nothing needs a relaunch. This
section states the behavior; the reasoning behind each rule is in
[the design's decision ledger](../design/sso-backed-bedrock.md#13-decision-ledger).

The pointer speaks the **container-credentials protocol**, the AWS SDKs' own way for a
container to get credentials, built for ECS task roles. The SDK sends one HTTP `GET` to the URL
in `AWS_CONTAINER_CREDENTIALS_FULL_URI`, with the value of `AWS_CONTAINER_AUTHORIZATION_TOKEN`
as its `Authorization` header, and reads back a JSON body of four strings: an access key id, a
secret, a session token and an expiry. It accepts plain `http` only to a loopback address, or to
ECS's own link-local ones. Every AWS SDK the shipped agents carry implements it, so no agent
needs code of its own, and the wire bridge's SigV4 signer reads the same pointer
([`wire-bridge-gateway.md` OQ-BR10](../design/wire-bridge-gateway.md#OQ-BR10)).

#### What a user configures

Everything goes in the **user** config. Each setting is declared `scope: "user"` and a
workspace value is refused, because a workspace `yolo-jail.jsonc` is a file the jail's own agent
can rewrite. A user enables the loophole, which ships off, and gives it a profile and a
narrowing:

- `profile` is the host AWS profile the service resolves: the one `aws sso login --profile`
  logs in.
- `role_arn` is a role the service assumes before serving, optionally with `session_policy`,
  an inline IAM policy attached to that `AssumeRole`.
- `unnarrowed: true` serves the profile's permission set as it is. It is the one setting that
  widens, and it is a bool so that no misspelling can grant.

Then an agent selects a Bedrock provider, a provider whose `platform` is `aws-bedrock`: the
shipped `bedrock` profile (`yolo -p bedrock -- claude`, or `-p <agent>=bedrock` for another
agent), or a user's own profile over the same provider. The gate keys on the provider's
platform, never on a profile's name. claude, codex, opencode and pi each `need`
`packs/bedrock`, which `needs` `aws-auth`, so selecting any of them selects this pack, and
selected but unconfigured it changes nothing. The worked config block is
in [the pack README](../../packs/aws-auth/README.md#enabling-it).

The service **refuses to start**, naming the key to write, in six cases:

- no `profile` is set;
- neither `role_arn` nor `unnarrowed` is set, because absence never means un-narrowed;
- `session_policy` is set without `role_arn`: an inline policy is an argument to `AssumeRole`,
  so there is nothing to attach it to;
- `unnarrowed` is set beside `role_arn`: choosing either would silently discard the other;
- `session_policy` is not a JSON policy document;
- the host cannot run the `aws` CLI. The manifest has no `requires.command_on_path` probe for
  it, for the reason [the Claude broker's section](#the-claude-oauth-broker) gives: a loophole
  whose program is missing fails loudly at spawn rather than disappearing.

#### What crosses into the jail

- **The pointer and the caller token**, exported only in the env file of each agent whose
  selected provider is a Bedrock one, and in no other process's environment, a bare shell's
  included ([`OQ-CN7`](providers.md#oq-cn7)). The **caller token** is a secret
  the launcher mints for each launch, and the adapter answers `401` to a request that does not
  carry it. It exists because the loopback is not always the jail's own: a jail on
  `network.mode: "host"` puts the adapter's port on the host's loopback, and a nested jail
  shares its parent's
  ([notch convergence §2.3](../plans/notch-convergence.md#23-the-fix-every-service-authenticates-its-caller-at-every-notch)).
- **The region**, as for any Bedrock profile: from the Bedrock provider entry or the
  environment, else the `region` of the profile this service mints for (its `profile` setting),
  which the launch reads from the host's `~/.aws/config`
  ([the region file](providers.md#the-region-file)). The credential service itself never
  supplies one, and the protocol has no field for it.
- **The endpoint file** for the adapter's own hop to the host, mode `0600`, as for
  [every host-service loophole](#host-service-loopholes).

Nothing else crosses: no access key, secret or session token in any file or variable, no SSO
access or refresh token, no `~/.aws`, and not the service's minted-credential cache, whose state
directory crosses only an inert marker file.

The adapter starts only on a launch where some agent's selected provider is a Bedrock one, and
the pointer crosses only where the adapter runs. A launch that does not run it leaves the pointer out and
says why: a jail whose launch has not enabled the loophole, and `yolo host env`, which runs no
process. `yolo host -- <agent>` runs it for that one agent: with the loophole enabled and the
agent on a Bedrock provider, the launch opens the adapter itself as a listener on the host's
loopback, on a port it picked and behind a caller token it minted, forwarding through a front of
its own to the host service, and closes it when the agent exits
([`host-notch-services.md` §4.8](../design/host-notch-services.md#48-yolo-host)). A profile of
the user's own still wins there: every shipped agent's AWS client asks the profile it resolves in
the user's `~/.aws` before the pointer, which is `AWS_PROFILE`'s, or `default` when that is unset,
so a `[default]` holding credentials is used instead, and the launch cannot tell. The adapter
holds nothing across a request: it checks the caller token,
forwards through the authenticated front to the host service, and passes the answer back. The
host service answers from its cache. It mints ahead of need and re-mints well before expiry,
because the SDK gives each fetch about a second, and the SDK re-fetches shortly before expiry on
its own, so the credential's short life is invisible to the agent.

> [!WARNING]
> **Inside the jail the caller token is not a boundary.** It sits in the selecting agent's env
> file, which any process running as the jail's user can read, and anything that reads it can
> `GET` the same credential. So the narrowing is the only defense inside the jail, which is why
> the service will not start without one.

#### The narrowing

The service serves one of three arms, chosen by the settings:

| Settings | What the service does | What the jail's credential can do |
| :--- | :--- | :--- |
| `role_arn` | `AssumeRole` from the SSO session, with no additional session policy | what that role's own policy allows |
| `role_arn` and `session_policy` | the same `AssumeRole`, with the inline policy attached | the intersection of the role's policy and the session policy, which can narrow inside Bedrock to named actions |
| `unnarrowed: true` | no `AssumeRole`: the profile's own credentials, as the `aws` CLI resolves them | whatever the profile's permission set grants |

An SSO session is already a role session, so both `AssumeRole` arms are **role chaining**, and
STS caps a chained session at one hour whatever the role's own maximum says. The re-minting
above makes that cap cost nothing. The cache is keyed by profile, so one service serves several
AWS identities. Each entry records the arm that minted it, so an entry minted under a different
narrowing is a miss rather than a wider credential served.

**An un-narrowed service is disclosed at every launch.** The `unnarrowed` setting declares a
`disclose` sentence, and the launch prints it on stderr as `loophole aws-auth: …` whenever the
resolved value is true. The launch prints any bool setting's `disclose` sentence the same way,
and knows no loophole's name. The service also prints its own disclosure when it starts, and its
self-check, which `yolo check` runs, reports it as a `NOTE` rather than a pass. No flag hides the
launch line ([`OQ-RO3`](report-tiers.md#why-its-this-way)).

#### What refuses the launch, and what only warns

Three things delivered beside the pointer make an AWS SDK ignore it while every turn still
succeeds, because the SDK's credential chain reads them first. The pack declares all three under
`overridden_by` on the pointer's `env` contribution, and core evaluates the declaration for any
pack without naming an AWS variable (`packload.EnvOverrideFindings`; the schema is
`internal/packdecl`'s `EnvOverride`).

| Delivered beside the pointer | Result | Why |
| :--- | :--- | :--- |
| `AWS_BEARER_TOKEN_BEDROCK` | **refused** | every client measured prefers the bearer to the credential chain |
| both `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY`, unless `AWS_PROFILE` is also delivered | **refused** | the chain's environment provider answers before the container-credentials provider in claude, codex, opencode and pi |
| a `host_files` entry that renders anything under `~/.aws` | **warning** | it overrides the pointer only when it holds credentials for the profile the SDK resolves, and a path cannot say whether it does |

A refusal stops a fresh launch before the jail starts, and an attach before it rewrites the
running jail's environment file. It names both sides and says to drop one. There is no escape
hatch, because proceeding would be proceeding into the wrong credential. The warning prints on
every such launch, cannot be switched off, and lets the launch continue. `yolo check` predicts
all three, as two FAILs and a WARN.

The rule counts only what reaches the jail:

- The declaration is evaluated only while the pointer is delivered, which takes an agent on a
  Bedrock provider.
- A variable counts when it is delivered into the jail, through `env_sources` or a pack. One
  exported only in the shell `yolo` was run from does not count, since no backend forwards that
  shell.
- A directory grant counts only on a backend that delivers directories.
- One half of the key pair alone is not refused, since it answers nothing. The pair beside
  `AWS_PROFILE` is let through because the JavaScript SDKs then skip the environment provider.
  codex's does not, so for codex that combination is a false negative, which the ruling accepts
  rather than risk a false positive.

#### When the SSO session lapses

The service never runs a login, because starting a session is a browser flow and belongs to the
human. Inside a live session it keeps the SSO access token fresh the way every AWS client on the
machine does, by shelling out to `aws configure export-credentials`, and it re-reads the host's
SSO cache on every mint. So a lapse runs like this:

1. The portal session ends. A credential already served keeps working until its own expiry, so
   the lapse is felt up to an hour late.
2. The next mint fails, and the agent's next fetch gets a `4xx` whose message names
   `aws sso login --profile <profile>` verbatim. Every failure the adapter returns is a `4xx`,
   a transport fault included, because a `4xx` is the one class whose message the SDK puts on
   the error it raises. That turn fails.
3. The human runs that login on the host. The next fetch succeeds, in the jail that is already
   running, with no relaunch.

**The launch warns first, and proceeds.** A launch where some agent's provider is a Bedrock one asks
the service, once it has started or found it, whether that agent's first fetch would be served.
The question is the **launch check**, a term coined for this feature: one request a launch sends
to a host daemon whose manifest declares `host_daemon.launch_check`, through the front the launch
just published for it ([the protocol](loophole-protocol.md#the-launch-check)). The service
answers from its cache when that is warm, which is every launch but the first after a spawn, a
settings change or a lapse, and runs no `aws` for it. When the cache is cold it mints now, or
waits for the mint it already has running. That is the mint the agent's first fetch would
otherwise make inside the SDK's budget of about a second, so a success also warms the cache for
that fetch. The launch waits for the answer only as long as a short budget
([current values](#current-values)). A failed mint prints one line, `loophole aws-auth: cannot
mint a Bedrock credential for this launch: …`, in the same words as the `4xx` and `yolo check`:
`aws sso login --profile <profile>` for a session that lapsed or was never established, and how
to add the profile or point the setting at another for a profile the host's `~/.aws/config` does
not have. The line ends by saying when the fix reaches the running jail. A fix outside yolo does
with no relaunch, since every mint runs `aws` afresh; that is all a lapsed session or a missing
CLI can need. The service reads `loopholes.aws-auth.settings` only when it starts, and a launch
of a new jail is what restarts it on a change, so a line whose fix can be a setting says both.
The jail then starts as it would have. A mint still running when the budget runs out is
reported by the previous attempt's failure and its age, or, when there is none, by a dim line
saying the launch could not tell. A service that cannot be asked gets a dim line too, but for one
started by an older yolo: nothing restarts the host-wide service when yolo is upgraded, and the
one still running does not know the question, so the launch warns that it predates this yolo and
names `yolo host-daemon restart aws-auth`. The warning is a disclosure, so no flag hides it
([`OQ-RO3`](report-tiers.md#why-its-this-way)).

An attach asks too: a new agent entering a jail that is already running, such as
`yolo -p bedrock -- claude` in a second terminal, gets the same question and the same warning,
through the front the jail's own launch published. It starts nothing, so a jail whose launch did
not start the service is not asked. The check cannot see a session that ended after the cached
credential was minted: that credential still works, and the lapse shows at the next mint, within
its hour. `macos-user`
asks the same way, before the sandboxed command runs. `yolo host` ensures the service for a
Bedrock agent's doorway ([`host-notch-services.md` HS-D21](../design/host-notch-services.md#HS-D21))
but asks it nothing: no launch check runs at that notch.
The decision is [`SSO-D1`](../design/sso-backed-bedrock.md#SSO-D1).

The SSO config form sets how often a human acts, not how long a jail lasts. A profile in the
`sso-session` token-provider form refreshes its own access token, so a login is needed only when
the portal session ends. The legacy profile-only form has no refresh token, so it needs a login
each time its fixed session ends. Both are served, and the service names the form it resolved
when it starts and in its self-check.

#### Where it runs

On podman, as described above. On `macos-user`, whose sandboxed agent shares the Mac's
loopback, the launch opens the adapter outside the Seatbelt sandbox, as a listener it owns on a
port it picks, which the pointer names
([`host-notch-services.md` HS-D15](../design/host-notch-services.md#HS-D15)). Apple Container
starts no host service but the OpenAI one, and the Claude broker for a launch that opted into the
credential view, so none of this runs there.
[The backend table](#per-backend-differences) has the row.

#### What has been watched running

**MEASURED on 2026-09-29**, on a Linux host with rootless podman, where the launcher reported
`YOLO_HOST_LOOPBACK=requested`. The service is in daily use across many of the maintainer's
jails, serving the maintainer's own SSO profile, narrowed to a Bedrock-only role. Its log shows
the `sso-session` form and every credential minted with `AssumeRole` and no additional session
policy, which is the first arm above. The host's crossings log shows hundreds of accepted
crossings from about a dozen jails. Inside one of those jails, claude ran in Bedrock mode. Its
environment carried the pointer, the caller token and the region, and no AWS key, session token
or bearer, and `~/.aws` did not exist. A `GET` with the caller token returned the four keys,
expiring within the hour, in a few milliseconds, which is a cache read. Without the token it
returned `401`. From the same jail, with the served credential, S3 `ListBuckets` was refused
`AccessDenied` and EC2 `DescribeInstances` returned HTTP 403 `UnauthorizedOperation`, both naming
the assumed Bedrock-only role, while Bedrock's `ListFoundationModels` succeeded. CI also runs the transport over a real loopback hop, on rootless podman on both
architectures, with a fake `aws` (`integration/awsauth_test.go`).

The maintainer's host logs the SSO profile out and back in every four hours, and a jail up about
ten hours, with the service never restarting, kept working across at least two of those cycles:
a running jail picks up a new login with no relaunch.

**UNMEASURED:** a turn during a real lapse (the logout-to-login gap there is short), and the lapse
message a real expiry produces, and the launch warning printing it (CI checks both with a fake
`aws`); the legacy SSO form; the session-policy and un-narrowed arms
against a live login; codex, opencode and pi on this channel; and `macos-user`, which has not run
on a Mac.

### Git-identity composition

Git identity is a **two-key allowlist** — `user.name` and `user.email`, plus an in-jail
`core.excludesFile` — never a mount of `~/.gitconfig`.

> [!WARNING]
> **Do not inherit the host gitconfig, not even filtered.** It drags in `credential.helper`,
> `user.signingkey` and `url.*.insteadOf` rewrites — all credential leaks — and `commit.gpgsign`,
> which *breaks every in-jail commit* with no key present. None of those are named, so none cross.
> Composing fresh each run is also what makes a *cleared* host key vanish from the jail; an
> add-only merge left it behind.

Container backends compose a fresh minimal INI each run from the host's effective
`user.name`/`user.email` (a repo-local value for the host working directory wins) and mount it —
`:ro` on podman, materialized on Apple Container. `macos-user` has no mount namespace, so it
forwards the same two keys as environment variables and replays them imperatively with
`git config --global`, adding the workspace as a git `safe.directory` there too, since the sandbox
account does not own it. Its global gitignore is copied into the root-owned context tree the
launch stages, and the bootstrap points `core.excludesFile` at the copy. Full account:
[`git-identity.md`](git-identity.md).

### Host-service loopholes

The general pattern, of which the broker is one instance: a host-side daemon holds the secret and
the jail reaches it through a per-jail directory bound at `/run/yolo-services/`. A loophole with a
manifest publishes an **endpoint file** — host:port, base64 cert, token, written `0600` — named by
`YOLO_SERVICE_<NAME>_ENDPOINT`, and the client dials a cert-pinned, token-authenticated loopback
port. A service declared directly in the workspace config still gets a `<name>.sock` and a
`YOLO_SERVICE_<NAME>_SOCKET`, because its daemon is a program yolo did not write.

Either way **the agent calls the service and never sees the raw credential.** That is the
sanctioned way to give a jail scoped access to a credential without handing it over. The one
deliberate exception is [`aws-auth`](#sso-backed-bedrock-credentials-aws-auth): its agent does
hold an AWS credential, but only a short-lived, narrowed one fetched on demand, while the SSO
session it is minted from stays on the host. The wire format and the reachability requirements
are [`loophole-transport.md`](loophole-transport.md)'s.

## Where each agent's credentials live

Most agents authenticate **inside the jail**. Codex, Pi and opencode can instead use yolo's OpenAI
credential service, while several agents also accept a provider API key through
`env_sources`. Each pack's manifest pins that agent's overlay dirs (its `state` contributions) and
its config surfaces; `packs/*/pack.json` is the enumeration, and
[`pack-system.md`](pack-system.md) is how to read one.

Two asymmetries are the load-bearing part, and neither is visible from a per-agent table:

- **Overlay dirs are per-workspace, and nothing seeds them but the Claude login.** For each
  selected agent, `<workspace>/.yolo/home/<subdir>` is bound over the corresponding dir in the
  jail home. The one channel from the machine store into a new workspace is the Claude login
  seed, and it forwards only the login and onboarding keys; `seedAgentDir`, which copied every
  top-level file of the machine store's copy of the dir, is deleted
  ([`base-home-legacy-state.md`](../design/base-home-legacy-state.md#27-the-seed)). An agent
  pack that declares no `state` dir rides the per-workspace `.config` overlay instead.
- **Claude and OpenAI subscription authentication have different sharing paths.** Claude gets a separate read-write
  shared-credentials mount plus the relative symlink, so a single OAuth identity is shared across
  every jail on a host. Codex, Pi and opencode receive generated views from canonical service
  state and cannot write that state directly. Claude history stays isolated per host workspace even when
  the home is shared, because the history file is keyed on a hash of the host directory.

> [!WARNING]
> **Do not generalize either sharing mechanism.** Claude shares one agent-native file. OpenAI
> subscription authentication keeps one canonical service file and materializes narrower Codex,
> Pi and opencode views. Other agent overlays are still seeded one-way per workspace.

<a id="agys-paths-under-gemini"></a>

There is no `gemini` pack and there never was, but the **gemini-shaped paths are real, and they
are agy's.** Two different directories matter, and they are not the same one:

- **agy's config dir** is `~/.gemini/antigravity-cli` (`Env.AgyDir`, built on `Env.GeminiDir`),
  where `packs/agy` renders `settings.json` and `mcp_config.json` and where agy keeps its OAuth
  token, the file the shared-credentials hook links.
- **agy's workspace state** is the **whole** `~/.gemini`: the `state` contribution in
  `packs/agy/pack.json` names `.gemini`, not the config dir.

`yolo prune` also sweeps gemini log dirs. A path under `~/.gemini` is agy's, not a leftover.

> [!WARNING]
> **Do not narrow agy's `state` to `.gemini/antigravity-cli`.** agy writes its own project state
> to `~/.gemini/config/projects`, a sibling of the config dir rather than a child of it. With the
> state narrowed, `~/.gemini/config` is read-only in the jail, and agy fails at boot with
> `mkdir /home/agent/.gemini/config: read-only file system` (fixed in `01e01808`, which widened
> the state to `.gemini`).

## Per-backend differences

`macos-user`'s defining property for credentials is its **single account home**: every session
runs as one hidden Unix user, and there are **no mounts** — so every credential surface that is a
mount on the container backends is a copy, an imperative replay, or "just the real home." Since
2026-09-12 that home has a **workspace tier**: each agent state dir the podman argv would bind from
`<workspace>/.yolo/home` is a symlink from the account home into that same directory, and a mirror
there points each machine-scope shared dir back at the account home's real one, so the relative
`shared_credentials` link resolves as it does on every backend. Everything the tier does not link
is the machine tier, credentials included, so **all concurrent `macos-user` sessions share one
credentials file per agent**, with no per-session separation.

Its boundary is the Seatbelt profile: `(allow default)`, then deny reads under the users root
(re-allowing only the sandbox home and the neutral workspace) and deny both system keychain
directories. So other host users' homes and the login keychain are unreadable, while the network is
fully open.

| Credential mechanism | podman | container (Apple Container) | macos-user |
| :--- | :--- | :--- | :--- |
| Host cred propagation | none — not mounted | none — not mounted | none — Seatbelt read-denies the users root and both system keychain directories |
| git identity, global gitignore | composed file, `:ro` bind | composed file, **materialized** (a copy by choice, needing no version gate) | the two keys forwarded as env, replayed with `git config --global`; the global gitignore copied into the root-owned `/ctx` tree the launch stages, with `core.excludesFile` pointed at the copy (2026-10-05) |
| `env_sources` | file mounted, sourced | file **materialized**, sourced | a per-session root-owned `0600` file the sandbox account may read, named on the argv; no value on the argv since 2026-09-13 |
| Per-agent host settings grant | `/ctx/host-<pack>/` `:ro` mount, then boot compose | materialized copy, then boot compose | copied into the root-owned `/ctx` tree the launch stages (`YOLO_CTX_ROOT`, 2026-09-13), then boot compose; a copy that fails ends the launch |
| User `host_files` | source-bearing: `/ctx/host-user/<slug>` `:ro`; source-less: composed | source-less composes; a **file** `source` is materialized (copied into the workspace home, since Apple Container cannot bind a single file there); a **directory** `source` binds `:ro` from Apple Container 1.1.0 and is skipped with a named reason below it or when the version is unreadable | source-less composes; a **file** `source` is copied into a root-owned `/ctx` tree (2026-09-13), and a **directory** `source` into the same tree, confined to its source and capped (2026-10-05); one over the cap, or that cannot be copied whole, ends the launch naming the entry |
| Claude shared credentials | shared bind + relative symlink | shared bind **nested inside** the whole-home bind, then the same relative symlink — one mount per declared shared dir (2026-08-24; before that the single bind put the creds in the per-workspace home) | the account home's own `.claude-shared-credentials`, the machine tier, reached by the same relative symlink through the workspace tier's mirror |
| claude-oauth-broker | active when the `claude` pack is selected | **skipped whole**: the host-service start admits only the OpenAI service, and the container args drop the loophole for its `intercepts` (which need `--add-host`). A launch that opts into the credential view also starts the singleton, which writes the view into the home the guest binds; no endpoint is published to the jail. UNMEASURED on this backend | **host half runs, jail half does not.** The singleton is ensured and a per-jail front publishes `claude-oauth-broker.endpoint` (measured, unit, 2026-09-18). Nothing uses it: the guest runs jail daemons, but it declines the TLS terminator because the interception needs an `--add-host` and a bind to port 443, and the sandbox has neither. So refreshes are not serialized here, and the launch names the declined terminator. The credential view can be opted into on this backend too; it is UNMEASURED there |
| OpenAI subscription credentials | canonical host-service state; Codex, Pi and opencode get workspace views | the **one** service this backend starts for every launch, endpoint file mounted — and measured unreachable from the guest, so the agent sees "OpenAI login is required" ([G6](../plans/setup-support-gaps.md#2-ranked-gap-backlog)). The launch **names the cause** in its inert report. The endpoint variable and the mount are still emitted — the measurement is per BACKEND, so withholding one service's pointer would patch a per-service hole in a per-backend fact | host daemon started like every other, and a launch that cannot start it is the one that is **refused**; the endpoint path rides the sandbox env instead of a mount. Its refresh adapter is a doorway: it declares a host argv, so the launch opens it outside the sandbox as a listener it owns rather than in the guest, and Codex's refresh URL names that port (2026-09-29, HS-D15; MEASURED on a hosted Mac 2026-09-30, with no Codex run) |
| AWS SSO credentials (`aws-auth`) | host singleton; adapter in the jail; pointer and caller token in the env file of the agent on a Bedrock provider. MEASURED, in daily use (2026-09-29) | not started: this backend starts no host service but the OpenAI one and, for an opted-in credential view, the Claude broker | host singleton; the adapter opens outside the Seatbelt sandbox as a listener the launch owns, on the Mac's loopback the agent shares, and the pointer names its port. UNMEASURED: its hardware test ran on a hosted Mac on 2026-09-30 and was refused at the region pre-flight before its probe, so nothing past the launch was observed. `yolo host`, which is no backend, does the same for the one agent it runs when that agent is on a Bedrock provider ([`host-notch-services.md` §4.8](../design/host-notch-services.md#48-yolo-host)), MEASURED by unit tests with a fake agent and a fake `aws` |
| Host-service loopholes | endpoint file + `YOLO_SERVICE_*_ENDPOINT` | only the OpenAI credential service starts, and the Claude broker for an opted-in credential view; the OpenAI endpoint file crosses in the host-services dir bind, gated on the loophole being active and its pack cleared to run host code. Every other pack host daemon is skipped and each one is reported inert | **every host daemon starts**, through the same spawn boundary and the same exec disclosure the container path uses; each endpoint's path rides the sandbox env with a per-file ACL grant instead of a mount. The inert report here is the PLATFORM axis only. The `jail_daemon` half runs in the guest, as the sandbox account under the session's Seatbelt profile (`yolo-jaild supervise`, since 2026-09-28; MEASURED with a local pack on a hosted Mac, 2026-09-30). The guest declines four shapes by name: an intercepting daemon (the Claude terminator), a pack service's (its host half runs instead), a doorway declaring `host_cmd` (opened outside the sandbox, HS-D15), and one whose argv names a container-only path |
| Per-workspace cred isolation | per-workspace `.yolo/home` overlay | one whole-home bind per workspace, with the machine-scope shared dirs (`.claude-shared-credentials` among them) bound inside it and shared across workspaces | a per-workspace tier: each agent state dir podman would bind is a symlink from the account home into `<workspace>/.yolo/home`; credentials stay in the one account home, shared by every session |
| Isolation boundary | userns (Linux) / VM (macOS) + read-only root | VM + read-only root | Unix user + Seatbelt — weaker, deliberately |

Linux versus macOS on the container backends is a runtime-flag story, not a credential story: the
mechanisms above are identical for podman on either. The real axis is **container versus
`macos-user`**, because the mount layer is what most credential delivery rides on.

## What a compromised prior session can tamper with

The sharper question than "what can a live session reach."

- **Shared, mutable surfaces.** The shared credentials file, the shared cache and the shared
  toolchain store are read-write and host-shared, so a prior session can poison a shared cache or
  the shared Claude credentials file. That shared-home render fight is the reason agent-config
  writes are convergent and single-writer.
- **The `macos-user` host-side nix build — the trust-boundary inversion.** Provisioning runs a
  host-side `nix build` **as the invoking user, unconfined**, with inputs a prior sandbox session
  could have written. This is the one place a prior session's bytes flow back to a *more*
  privileged context. Container backends never trigger a host-user build and so do not have the
  inversion. Full analysis in
  [`../design/macos-user-build-step-threat-model.md`](../design/macos-user-build-step-threat-model.md).

> [!NOTE]
> **The launch-time config-change approval does reach `macos-user`** — since 2026-08-18 (`bb825486`),
> and the gap is worth remembering because of where it was: the y/N prompt that flags a poisoned
> `packages:` edit was missing on exactly the backend whose host-side build is unconfined. The
> approval now has a call site on each arm — the container one gates the FRESH-LAUNCH path only
> (attaching to a running jail deliberately skips it), and the `macos-user` arm has no attach, so
> every invocation of that backend is gated. `--dry-run` is the one exemption on it: nothing
> launches, so there is no change to approve.

## What this does not license

- **Not a config knob over the per-agent host-file set.** That is the credential boundary; a
  workspace config widening it is exactly the hole the retired per-agent keys were.
- **Not a deny-list.** Nothing is stripped, because nothing is added. Adding a filter would imply
  the host home is a mount source, which is the property being preserved.
- **Not general cloud-credential forwarding.** yolo never mounts a cloud credentials directory
  such as `~/.aws`, and never forwards an SSO session, refresh token or session token into the
  jail. The one cloud integration it ships is the opt-in [`aws-auth`](../../packs/aws-auth/README.md)
  pack, off until you enable it in your user config: a host-side service turns your host
  `aws sso login` into a short-lived AWS credential, narrowed to a role or session policy you
  configure, and serves it to the jail's AWS SDKs over a loopback URL
  ([how](#sso-backed-bedrock-credentials-aws-auth)). The SSO session and `~/.aws` stay on the
  host. For anything else, such as another cloud or a static key, a
  jail-local key arriving through `env_sources` is the whole mechanism, and its blast radius is
  whatever that key is scoped to.
- **Not a promise that a raw secret stays out of the agent's process.** A host-service loophole
  keeps the secret host-side; `env_sources` deliberately does the opposite. The channel chosen is
  the decision.
- **Not per-session isolation on `macos-user`.** One account home, whose machine tier holds the
  credentials, for every session, by construction.

## Why it's this way

Rulings a future change would otherwise undo, kept with their original IDs.

| Ruling | Why it holds |
| :--- | :--- |
| **[OQ-A10](loophole-system.md#oq-a10)** — the broker is a *contribution* of `packs/claude`, not a pack of its own | The dependency is structural: there is no jail that wants the broker and not claude. Selecting the pack is the declaration, which is why the host-side activation probe could be deleted rather than replaced. |
| **[OQ-A1](loophole-system.md#oq-a1)** — the broker stays on by default through `default_enabled: true` on its own manifest | A shipped loophole is host access you ask for, except where it keeps a credential you already asked for from being burnt, as this one and `openai-auth-broker` do, so the two kinds need opposite defaults. Off by default means silently reintroducing the race. |
| **P1 (broker)** — concurrent consumers must be serialized by a host-wide flock | Anthropic mints single-use refresh tokens. This is a property of the upstream service, not of yolo's architecture, so no refactor retires it. |
| <a id="oq-oa1"></a>[`OQ-OA1`](#oq-oa1) (OpenAI): **one machine-wide host service is the sole writer** of the subscription's refresh token, and every agent pack that can use the subscription `needs` the one pack that owns it | OpenAI's refresh token is single-use, so two writers race on it, and a login per agent or workspace would make the user repeat the browser flow every time. |
| <a id="oq-oa2"></a>[`OQ-OA2`](#oq-oa2) (OpenAI): **no agent receives the canonical refresh token.** Codex uses its native refresh override with an opaque generation marker; Pi gets an access-token view | A view an agent can redeem would make that agent a second writer. The marker lets the service tell a stale view from a current one without trusting the caller with anything redeemable. |
| <a id="oq-oa3"></a>[`OQ-OA3`](#oq-oa3) (OpenAI): **`yolo host -- codex` shares the login through a managed Codex home; a directly launched host Codex is untouched** | Rewriting the user's ordinary `~/.codex/auth.json` would silently change a program yolo did not launch. |
| <a id="oq-oa4"></a>[`OQ-OA4`](#oq-oa4) (OpenAI): **no browser callback depends on a per-jail host port** | Ruled as a temporary state-routed relay for container callbacks. As built, the host service runs the whole login and the jail only streams the URL back, which meets the ruling with no relay at all; do not add one. |
| <a id="oq-oa5"></a>[`OQ-OA5`](#oq-oa5) (OpenAI): **every backend uses authenticated loopback TLS and the same refresh algorithm, and none intercepts `auth.openai.com`** | Codex's refresh-URL override and Pi's provider API are supported seams; interception would need an OpenAI-signing CA and would catch unrelated host traffic. |
| <a id="oq-oa6"></a>[`OQ-OA6`](#oq-oa6) (OpenAI), route (b), maintainer ruling 2026-09-29: **on `macos-user` the Codex refresh adapter is a launch-owned doorway outside the sandbox**, under [HS-D15](../design/host-notch-services.md#HS-D15)'s rule for every credential service | The doorway opens on whichever loopback the agent sees: a container's own loopback holds it as a jail daemon, and `macos-user` and `yolo host` share the Mac's. The maintainer, on why an in-jail placement was never a principle: *"the host doesn't have to run [in the jail] anyway, so the host can access it."* |
| <a id="oq-oa7"></a>[`OQ-OA7`](#oq-oa7) (OpenAI), decided 2026-09-29: **Pi's view refreshes before expiry only, with no ask-once-more after an unauthorized response** | Pi gives an extension no way to see a response's status ([the warning](#openai-one-writer)), and the service's proactive refresh covers the expiry case. Re-open only if pi adds a status hook. |
| <a id="oa-d1"></a>[`OA-D1`](#oa-d1) (OpenAI), decided 2026-10-01: **opencode gets Pi's view, and yolo's plugin replaces opencode's request `fetch` on that entry** | opencode's refresh address is hard-coded and no config or variable moves it (read from opencode 1.18.34's source, not run), so the only way to keep it from redeeming is to own the fetch. MEASURED by unit tests only; no opencode session has sent a request on the subscription. |
| **`env_sources` over the settings `env` block, as shipped** | The `env` block is the right long-term target — it is the one channel that renders at *both* the jail and host notches — but nothing shipped uses it for a secret today, and a doc that said otherwise was measured wrong against a live jail. State the mechanism that runs. |
| **MCP `${VAR}` is passed through verbatim** | An interpolated secret entered the file without passing through any provenance layer, and sourced config content from process env at render time. Resolution one step later, by the consumer, loses nothing. |
| **[OQ-SSO1](../design/sso-backed-bedrock.md#13-decision-ledger), [OQ-SSO10](../design/sso-backed-bedrock.md#OQ-SSO10)**: `aws-auth` requires a narrowing, serves un-narrowed only when asked by name, and discloses that at every launch through a declared `disclose` sentence | Any process in the jail can read the served credential, so the narrowing is the only defense there. A default that widened could not be tightened later without breaking working setups. The service is a singleton, so its own spawn line prints once and then serves every later launch in silence; and a launch that tested the loophole's name would be a switch on a tool name in the one loop that renders every pack. |
| **[§8](../design/sso-backed-bedrock.md#8-behaviour-this-design-specifies), [SSO-D1](../design/sso-backed-bedrock.md#SSO-D1)**: a session that is missing, lapsed or for a profile the host lacks is a launch WARNING, never a refusal, asked through a declared `launch_check` | The human may be about to log in, and a jail that will not start is worse than a first request that fails clearly. The launch asks the daemon rather than probing AWS itself so the words are the one classifier's, and it asks by declaration because a test of the loophole's name would be a switch on a tool name in the loop that renders every pack. |
| **[OQ-SSO2](../design/sso-backed-bedrock.md#13-decision-ledger)**: one `aws-auth` service per machine, its cache keyed by profile | The mint is the slow step and a fetch has about a second, so a warm shared cache is what keeps fetches inside the budget. Keying by profile keeps a distinct AWS identity per jail without a second daemon. |
| **[OQ-SSO4](../design/sso-backed-bedrock.md#13-decision-ledger)**: the profile, role and session policy are user-scope settings only | The workspace config is writable from inside the jail, so a workspace value would let the agent choose its own profile or swap the narrowing for one of its own. |
| **[OQ-SSO3](../design/sso-backed-bedrock.md#13-decision-ledger), [OQ-SSO6](../design/sso-backed-bedrock.md#13-decision-ledger)**: the service refreshes inside a live session and never runs a login; a lapsed session is a message, not a request | Refreshing is what every AWS client on the machine already does against the same cache. Starting a session is a browser flow and the human's. A jail able to trigger a host login would be half an approval mechanism, which belongs to [`boundary-broker.md`](../design/boundary-broker.md) rather than to a credential pack. |
| **[OQ-SSO8](../design/sso-backed-bedrock.md#OQ-SSO8)**: the override rule is declared by the pack, fatal only when the override is certain, and never a false positive | Core names no AWS variable, so the next pack with the same shape needs no core change. A certain override is a silent wrong answer, so it refuses with no hatch. A possible one is only a warning, because refusing a working jail is worse than a false negative. |
| **[OQ-SSO9](../design/sso-backed-bedrock.md#OQ-SSO9)**: yolo mints no Bedrock API key | A bearer minted from the narrowed, role-chained session would die within an hour of launch, and every shipped agent reaches Bedrock with a chain credential, natively or through the signing wire bridge. A user who wants a bearer mints it on the host and delivers it through `env_sources`. |

## Current values

The prose above explains what each of these is for; this table is the only place the values
themselves are stated. Every row names where its value is defined, so a row is checkable against
that file rather than against a commit stamp. The host singletons are the rows that grow: they are
whatever this prints, and nothing else in the tree enumerates them —

```console
$ rg -n '"scope": "host"' packs/*/loopholes/*/manifest.jsonc
```

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| Per-agent host grant mount | `/ctx/host-<pack>/<file>`, `:ro` | `internal/cli/run` (`hostFileArgs`) |
| User host-file mount | `/ctx/host-user/<slug>`, `:ro` | `internal/cli/run/hostfiles.go` (`hostUserFileArgs`) |
| Resolved `env_sources` file | `~/.config/yolo-user-env.sh`, `0600`: `export K=${K:-'v'}` lines for the values no provider claims, then the channel section's plain `export K='v'` lines. On `macos-user` a per-session root-owned `0600` file with one `user:` ACL entry for the sandbox account carries them instead | `internal/cli/run/userenv.go` (`userEnvFileMode`); `internal/macosuser/envfile.go` |
| Per-agent env file | `~/.config/yolo-agent-env/<agent>.sh`, `0600` in a `0700` directory, a `:ro` bind on podman, written in place on Apple Container and in the workspace tier's `config/yolo-agent-env` on `macos-user` — the credentials and gated env the credential gate scopes to that agent, sourced by its launcher ([`providers.md`](providers.md#the-credential-gate)) | `internal/cli/run/agentenvfiles.go`, `internal/entrypoint/agentenv.go` |
| Jail service dir | `/run/yolo-services/` | `internal/paths` (`JailHostServicesDir`) |
| Endpoint file | `<name>.endpoint`, mode `0600`, named by `YOLO_SERVICE_<NAME>_ENDPOINT` | `internal/svcendpoint` |
| Declared-service socket | `<name>.sock`, named by `YOLO_SERVICE_<NAME>_SOCKET`, for a service still on a unix socket | `internal/cli/run` (`hostServiceSocketEnvVar`) |
| Broker refresh lock | `<broker state>/refresh.lock`, held around every refresh and every write of the canonical login, the shared file and the views | `internal/oauthbroker` (`RefreshLockPath`, set by `ConfigureStore`) |
| Broker refresh floor | **`ConsumerRefreshDueMS + 60_000`** = 360s — `DoRefresh` serves the on-disk token above this and mints below it. Derived, not written, so it cannot drift under the consumer's threshold ([why](#the-refresh-floor-sits-above-claudes-own-due-threshold)) | `internal/oauthbroker/oauthbroker.go` (`RefreshCacheFloorMS`, `cachedForRefresh`) |
| Broker liveness floor | **90s** — the `cached` action's floor, a different question and deliberately lower | `internal/oauthbroker/oauthbroker.go` (`LiveTokenFloorMS`, `CachedTokens`) |
| Consumer due-threshold | **300s** — Claude Code's own `Date.now()+300000>=expiresAt`. A fact about the client, recorded so the floor above derives from it | `internal/oauthbroker/oauthbroker.go` (`ConsumerRefreshDueMS`); measured, 2.1.278 |
| Background refresher | lead **1800s** (thirty minutes since 2026-09-29; it was 300s), refreshing with a cache floor equal to the lead; tick **60s**, fast retry **5s** × **12** ([CL-D5](../design/claude-login-without-interception.md#CL-D5), [CL-D18](../design/claude-login-without-interception.md#CL-D18)) | `internal/oauthbroker/oauthbroker.go` (`BackgroundRefresh*`), `internal/oauthbroker/refresh.go` (`doBackgroundRefresh`) |
| Vendor refresh lock | `<storage dir>/.oauth_refresh.lock`, the storage dir being `CLAUDE_SECURESTORAGE_CONFIG_DIR ?? CLAUDE_CONFIG_DIR ?? ~/.claude`; `realpath:false`, stale `60000`, update `5000`. **Machine-scoped** since [CL-D22](../design/claude-login-without-interception.md#CL-D22), on every jail launch but a credential-view launch, whose lock stays in the workspace's `~/.claude`; one lock across Apple Container VMs is unmeasured ([above](#the-claude-oauth-broker)) | Claude Code bundle (`acquireOAuthRefreshLock`), measured; `internal/cli/run/claudesecurestorage.go` |
| Broker canonical login | `<broker state>/claude-credentials.json`, host-only: no launch mounts it | `internal/oauthbroker/store.go` (`CanonicalPath`) |
| Shared credentials file | `<global home>/.claude-shared-credentials/.credentials.json`, what an interception jail's Claude reads | `internal/storage` (`ensure.go`), `internal/oauthbroker` (`LegacyCredsPath`) |
| Claude credential view | opted in by `YOLO_CLAUDE_CREDENTIAL_VIEW=1` in the host environment, off by default on every backend; the view is `<workspace>/.yolo/home/<claude dir>/.credentials.json`, one registration per workspace under `<broker state>/claude-views` | `internal/claudeview` (`SwitchEnv`, `DefaultOn`, `ViewRel`); `internal/oauthbroker/store.go` (`ViewRegistryDir`) |
| Claude host verbs | `yolo claude-auth status`, `inspect <file>`, `refresh`, `logout`; `refresh` and `logout` refuse inside a jail | `internal/cli/claudeauth.go` |
| Broker daemon | `yolo internal daemon claude-oauth-broker`, `scope: "host"` | `internal/broker`; `packs/claude/loopholes/claude-oauth-broker/manifest.jsonc` |
| OpenAI canonical state | `<loophole state>/credentials.json`, mode `0600`; parent and lock are private | `internal/openaiauth`; `packs/openai-auth/loopholes/openai-auth-broker/manifest.jsonc` |
| OpenAI credential daemon | `yolo internal daemon openai-auth-broker`, `scope: "host"` | `internal/openaiauthdaemon`; `packs/openai-auth/loopholes/openai-auth-broker/manifest.jsonc` |
| OpenAI jail endpoint | `YOLO_SERVICE_OPENAI_AUTH_BROKER_ENDPOINT` | `internal/openauthclient` |
| OpenAI refresh lead, and the proactive tick (verified at `d4e435a3`) | 5 min; checked every minute | `openaiauth.DefaultRefreshLead`; the daemon's `-refresh-interval` (`internal/openaiauthdaemon`) |
| OpenAI login callback (verified at `d4e435a3`) | `http://localhost:<port>/auth/callback`, port `1455`, else `1457`; abandoned after 15 min | `internal/openaiauthdaemon` (`loginPorts`, the daemon's `-login-timeout`) |
| OpenAI upstream request timeout (verified at `d4e435a3`) | 30 s | `internal/openaiauthdaemon` (`upstream.go`) |
| OpenAI view markers (verified at `d4e435a3`) | `yolo-broker:<generation>` in Pi's and opencode's views; `yolo-broker:<generation>.<caller token>` in Codex's, the adapter stripping the suffix | `internal/openauthclient` (`pi.go`, `opencode.go`, `callermarker.go`) |
| opencode's view (verified at `d4e435a3`) | the `openai` key of opencode's `auth.json`, merged by `yolo internal openai-auth-client token --opencode-auth`; at the host, `$XDG_DATA_HOME/opencode/auth.json`, else `~/.local/share/opencode/auth.json`, which yolo never writes | `internal/openauthclient/opencode.go`; `internal/openaiauthhost` (`opencodeHostAuthPath`) |
| AWS credential daemon | `yolo internal daemon aws-auth`, `scope: "host"` | `internal/awsauthdaemon`; `packs/aws-auth/loopholes/aws-auth/manifest.jsonc` |
| AWS settings keys | `loopholes.aws-auth.settings.profile`, `.role_arn`, `.session_policy` (strings) and `.unnarrowed` (bool, default `false`), every one `scope: "user"`; the loophole ships `default_enabled: false` | `packs/aws-auth/loopholes/aws-auth/manifest.jsonc`; `internal/awsauth` (`settings.go`) |
| AWS pointer | `AWS_CONTAINER_CREDENTIALS_FULL_URI=http://{listen}/credentials` and `AWS_CONTAINER_AUTHORIZATION_TOKEN={caller_token}`, gated on the agent's provider declaring `"platform": "aws-bedrock"`, `served_by: "aws-auth"` | `packs/aws-auth/pack.json` |
| AWS mint timing | re-mint below **10 min** of remaining life (twice the SDK's five-minute window), a pre-mint tick at half that; `AssumeRole` asks **3600 s**, the role-chaining ceiling, as session name `yolo-jail` | `internal/awsauth` (`RemintLead`, `Broker.TickInterval`, `MintDuration`, `SessionName`) |
| AWS lapse answer | a `4xx` with `Code: ExpiredToken` and a message naming `aws sso login --profile <profile>`; a profile missing from `~/.aws/config` answers `ProfileNotFound` instead | `internal/awsauth` (`loginRequired`, `mint.go`) |
| AWS caller refusal | `401`, `Code: CallerUnauthenticated` | `internal/awscredadapter` (`handler.go`) |
| AWS service log | `~/.local/share/yolo-jail/logs/host-service-aws-auth.log` on the host: the profile, the narrowing and the SSO form at start, then each failed mint the pre-mint ticker or a launch check ran | `internal/awsauthdaemon` (`reportStartup`, `mintTracker`) |
| AWS launch check | declared as `host_daemon.launch_check: true`; asked by a launch that serves the adapter; answered from the cache, or from the mint a cold cache needs, within a **2 s** budget the daemon clamps to at most **5 s**, the launch reading **1 s** past it; a failure prints `loophole aws-auth: cannot mint a Bedrock credential for this launch: …` | `packs/aws-auth/loopholes/aws-auth/manifest.jsonc`; `internal/hostservice` (`LaunchCheckBudget`, `LaunchCheckBudgetCap`), `internal/awsauthdaemon` (`launchcheck.go`), `internal/cli/run` (`launchcheck.go`, `launchCheckMargin`) |
| AWS canonical state | `<loophole state>/credentials.json`, mode `0600` in a `0700` directory | `internal/awsauth` (`state.go`) |
| AWS jail endpoint | `YOLO_SERVICE_AWS_AUTH_ENDPOINT`, read by the in-jail adapter, which listens on `127.0.0.1:1461` (its `jail_daemon.listen`), or on a port the launch picked when the jail shares its launcher's network namespace. Emitted by any launch where the loophole is active and its pack may run host code, like every other `scope: "host"` loophole's — `hostServicesMountArgs` derives the set from the manifests rather than naming services one by one, which it did until 2026-09-20 (two names, and this one was the third, so the adapter answered `ServiceUnreachable` for every request while the launch reported a healthy jail). On `macos-user` the launch runs the adapter outside the sandbox instead, through `jail_daemon.host_cmd`, and `yolo host --` runs it the same way for an agent on a Bedrock provider, handing the adapter the endpoint of a front of its own in its input file (`internal/cli/run/hostdoorways.go`). ⚠ Not on Apple Container, which starts no aws-auth service | `internal/awscredadapter` (`EndpointEnv`); `internal/cli/run/assemble_parts.go` |
| Codex refresh adapter | `http://{listen}/oauth/token`: `127.0.0.1:1460` on a jail with its own network namespace, a port the launch picked on one sharing its launcher's (`network.mode: "host"`, or nested) | `internal/openaiauthadapter`; the pointer in `packs/codex/pack.json`, the port as `jail_daemon.listen` in `packs/openai-auth/loopholes/openai-auth-broker/manifest.jsonc` |
| Git identity keys carried | `user.name`, `user.email`, plus an in-jail `core.excludesFile` | `internal/cli/run` (`composeGitconfig`), `internal/entrypoint/identity.go` |
| `macos-user` identity replay vars | `YOLO_GIT_NAME`, `YOLO_GIT_EMAIL`, from the launch env; `YOLO_GLOBAL_GITIGNORE`, the staged copy of the global gitignore, set on the bootstrap by the plan | `internal/macosuser` (`MacosSandboxEnv`, `buildBootstrapEnv`), `internal/entrypoint/identity.go` |
| Config keys in this story | `env_sources`, `host_files`, `host_services`, `loopholes` | `yolo config-ref` |
