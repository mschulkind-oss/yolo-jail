---
status: current
stage: CURRENT
next: "Re-verify against the tree (system-doc), starting at the per-backend table's macos-user column: the stamp is d8bf06a0, and the launch-owned doorways and Claude's store bridge landed after it"
verified: 2026-09-20
verified_commit: d8bf06a0
covers:
  - internal/cli/run/assemble.go
  - internal/cli/run/assemble_parts.go
  - internal/cli/run/hostfiles.go
  - internal/cli/run/packhostgrants.go
  - internal/cli/run/userenv.go
  - internal/config/envsources.go
  - internal/config/hostfiles.go
  - internal/entrypoint/claude.go
  - internal/entrypoint/identity.go
  - internal/broker/
  - internal/oauthbroker/
  - internal/oauthterminator/
  - internal/openaiauth/
  - internal/openaiauthdaemon/
  - internal/openauthclient/
  - internal/openaiauthadapter/
  - internal/macosuser/seatbelt.go
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

**Status:** verified 2026-09-20 against `d8bf06a0`. The
[gemini-paths paragraph](#agys-paths-under-gemini) alone was re-checked against `f937d0fd` on
2026-09-27. The [SSO-backed Bedrock section](#sso-backed-bedrock-credentials-aws-auth) and the
`aws-auth` rows in the tables below were written against `fe24347c` on 2026-09-29, when
[`sso-backed-bedrock.md`](../design/sso-backed-bedrock.md) graduated into them; the launch
warning in that section, and its rows, were added the same day with the change that built it
([SSO-D1](../design/sso-backed-bedrock.md#SSO-D1)). Their `yolo host` sentences were rewritten
when that notch started opening the adapter
([HS-D21](../design/host-notch-services.md#HS-D21)). The OpenAI service's `macos-user` warning and
its two `macos-user` cells in [the backend table](#per-backend-differences) were corrected on
2026-09-30 against `4ac4b8fa`, for the launch-owned doorway (`fea3b6c7`). Nothing else in the doc
was re-checked.

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
| The secret channel (`env_sources`) | `internal/config` (`ResolveEnvSources`, `ParseDotenv`); `internal/cli/run` (`userenv.go`) |
| Composed agent surfaces, and the boot-time compose | `internal/entrypoint` (`prism.go`), `internal/agentcfg` |
| Git identity — the two-key allowlist | `internal/cli/run` (`composeGitconfig`), `internal/entrypoint` (`identity.go`) |
| The Claude OAuth broker daemon and its flock | `internal/broker`, `internal/oauthbroker` (`RefreshLockPath`) |
| The OpenAI credential service and agent views | `internal/openaiauth`, `internal/openaiauthdaemon`, `internal/openauthclient`, `internal/openaiauthadapter` |
| The AWS credential service and its jail adapter | `internal/awsauth` (mint, cache, lock, narrowing), `internal/awsauthdaemon`, `internal/awscredadapter`; the pack is `packs/aws-auth` |
| The pack-declared override rule the AWS pointer uses | `internal/packdecl` (`EnvOverride`), `internal/packload` (`EnvOverrideFindings`) |
| The shared-credentials symlink | `internal/entrypoint` (`linkThroughShared`, applied through the `shared_credentials` hook) |
| The jail-facing hop for a host daemon | `internal/svcendpoint` (`ServeFrontWithOptions`) |
| The `macos-user` Seatbelt profile | `internal/macosuser` (`seatbelt.go`) |
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
  OpenAI credential service shares Codex and Pi authentication through separate canonical state
  and generated agent views; it never makes either workspace overlay authoritative.

## The delivery channels

### `:ro` bind mounts

A host file mounted read-only into the jail, kernel-enforced against even the jail's root.
Container backends only. It carries the composed git config and global gitignore, the per-agent
host settings grant at `/ctx/host-<pack>/`, and user-declared host files at
`/ctx/host-user/<slug>`.

Apple Container cannot do a nested single-file `:ro` bind, so it **materializes** (copies) these
into the workspace state dir instead, relying on the whole-directory bind over the jail home. The
`:ro` guarantee is lost there — the copy is writable — but the file is regenerated every run
regardless.

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
- **`macos-user`** resolves host-side and bakes the pairs onto the sandbox launch argv via
  `env -i K=V …`.

> [!WARNING]
> **An empty resolve TRUNCATES the exported file; it must never be a no-op.** The file outlives
> the config that produced it and is a bind-mount source, so it cannot simply be deleted (podman
> refuses to start on a missing one). Leaving the previous render in place instead made removing
> `env_sources` unable to *revoke* a credential — the stale file kept exporting it through every
> subsequent rebuild. Revocation is truncate-in-place.

> [!WARNING]
> **Choosing `env_sources` as the secret channel is a decision, not a default.** Resolved values
> land cleartext at `0644` in several agent config files and in a render sidecar, and on
> `macos-user` they ride the process argv, visible in `ps`. That is the cost of the channel; a
> reader weighing where to put a key should know it before picking this one.

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
`YOLO_CTX_ROOT`. There is still no bind mount anywhere on that backend; the bytes arrive by copy.

> **⚠ Changed 2026-09-13.** Until then that backend carried **only** the source-less entries: with
> no bind mounts there was no `/ctx/host-user` to carry a source into, so a source-bearing entry
> was **skipped** rather than silently rendered without its host layer. One shape is still
> skipped — a `source` naming a **directory**, which a copy does not scale to — and it is now
> named in a warning instead of dropped in silence.

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
`packs/claude`, not a pack of its own, because the dependency is structural. It is the only
shipped loophole manifest with `default_enabled: true`, which is what makes selecting the pack
*sufficient*: the others are host access you ask for, and this one keeps a credential you already
asked for from being burnt.

The broker operates on **one file only** — the shared credentials file under the global home — and
never touches host Claude's own credentials. Host and in-jail Claude therefore keep **independent**
OAuth identities, through separate `/login` flows, which Anthropic permits.

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

#### ⚠ The 90/300 threshold mismatch — a live defect

**The broker answers most refreshes with the token the caller already has, and Claude counts that
as a failed refresh.** All four measurements below are against Claude Code 2.1.278 and the tree.

Claude considers a token due for refresh at **300 seconds** of remaining life:

```js
function eO(e,n=Date.now()){if(e===null)return!1;return n+300000>=e}
```

yolo's broker refuses to act until **90 seconds** (`oauthbroker.go:162`):

```go
if expiresAtMS-nowMS() < 90_000 { return nil }   // else: serve the on-disk record
```

Between those two numbers is a 210-second window in which Claude asks for a refresh and
`AsOAuthResponse(cached)` echoes the **same `access_token`** back with HTTP 200. Claude's
401-recovery path reads that as exhaustion:

```js
if(Zt()?.accessToken===De){ if(bre()!==null||!$1(w)&&++_e>=Rmo) throw m("api_request","api_request_oauth_refresh_exhausted"), new Fd(w,g) } else _e=0
```

`Rmo` is **2**. On a plain host this branch is unreachable, because a real refresh always mints a
new access token — which is the whole of why a plain Claude never loses its login and a jail's
does. Three aggravating facts:

- **`force` is dropped.** `IsRefreshGrant` inspects only `grant_type`, and `DoRefresh(credsPath)`
  has no force parameter, so Claude's force-refresh cannot compel an upstream call from inside a
  jail.
- **yolo already disagrees with itself.** `BackgroundRefreshLeadSeconds` is **300** — the same
  number Claude uses. Only the on-demand cache floor dissents, and Claude always wins the race to
  act on the threshold, because it checks before every API call while the refresher ticks at 60s.
- **Measured in one jail's terminator log:** of 380 successful refresh replies, **364 (96%)**
  carried 90–299 seconds of life — i.e. were the caller's own token handed back.

**FIXED 2026-09-20.** The floor was one constant answering two different questions, and the fix
splits them: `LiveTokenFloorMS` (90s) still answers the `cached` action's *is there a usable
token*, while `RefreshCacheFloorMS` — **derived** as `ConsumerRefreshDueMS + 60_000`, so the
inequality is in the source rather than in two numbers that happen to differ — answers the refresh
path's *should I mint*. `DoRefresh` reads the second. The derivation is pinned by a test that fails
if the floor ever sits below the consumer's threshold again, and a behavioural test asserts the two
paths disagree inside the old window.

⚠ **`force` is still dropped**, and closing this did not close that. It is a separate and optional
improvement: with the floor above Claude's threshold there is nothing for a force to override in
normal operation, so it now only matters if the consumer's threshold moves and yolo's constant does
not follow.

#### What the broker does *not* buy

Three beliefs about it were measured false, and each one had been load-bearing somewhere:

- **It is not what stops a jail spending a stale token.** That is the terminator, independently:
  `Refresh` sends `AskHostBroker(endpointPath, singleton("action","refresh"))` and nothing else,
  so the refresh token Claude presents is discarded at the jail edge and never reaches upstream.
  `DoRefresh` takes only a path.
- **It does not need to be a singleton.** See the flock note above — host-side is the requirement.
- **It does not trigger the vendor's dead-token disk clear, and cannot surface a real one
  either.** The vendor classifies a dead token by the **top-level `error` string**, not the status
  code (`jd(e.response.data).code==="invalid_grant"`). yolo's five broker codes — `creds_unreadable`,
  `no_refresh_token`, `upstream_http`, `upstream_bad_response`, `upstream_unreachable` — are never
  that string, so the catastrophic shared-file blanking path is closed. The mirror is the cost: a
  **genuine** upstream `invalid_grant` is wrapped as `{"error":"upstream_http","body":"…"}`, so
  Claude cannot see a truly dead token either and retries instead of prompting a clean re-login.

> [!WARNING]
> **The broker discards `refresh_token_expires_in`, the one field that predicts a logout.**
> Anthropic returns it and Claude 2.1.278 persists it as `refreshTokenExpiresAt`; it appears
> **nowhere** in `internal/`, `packs/` or `docs/`. `NormalizeOAuth` drops it, so neither the
> broker's log nor `describeCreds` can say how long the refresh token itself has left — which is
> why a refresh-token expiry is undiagnosable after the fact rather than merely unpredicted.

### The OpenAI subscription credential service

Codex and Pi use one machine-wide OpenAI subscription grant without sharing either agent's whole
home. The `openai-auth` pack owns a host singleton named `openai-auth-broker`; both agent packs
depend on that pack, so selecting either agent selects the same service rather than declaring two
refresh owners.

The canonical file contains the access, identity and refresh tokens, their expiry, the OpenAI
account id, and a monotonically increasing generation. It lives under the loophole's host state
directory and never crosses into a jail. Login replacement, logout and refresh all take one
machine-wide file lock. Refresh reloads the canonical file while holding that lock, returns the
current generation to a caller holding an older refresh token, and contacts OpenAI at most once
for the current generation. Updates use an atomic rename of a mode-`0600` file in a mode-`0700`
directory.

One prerelease build passed the literal relative path `{state}/credentials.json` to the daemon.
The next launch checks only two bounded locations for that file: the current workspace and, on
Linux, the recorded singleton process's working directory under `/proc`. While holding the same
singleton lock used for startup, it validates the file, moves it into the canonical private state
directory, removes the empty literal `{state}` directory, and replaces the daemon so later writes
use the canonical path. It never searches other workspaces. If both a legacy and canonical file
exist, or more than one bounded candidate exists, yolo refuses to choose and prints the paths;
preserving both for a deliberate manual choice is safer than overwriting a refresh authority.

The two agents receive different views:

- Codex gets its native `auth.json` shape in the workspace home. Its native refresh URL points to
  a jail-local HTTP adapter, which forwards the presented `yolo-broker:<generation>` marker
  through the authenticated host-service endpoint. Only the host service holds or may redeem the
  canonical refresh token.
- Pi's `openai-codex` provider extension asks the service for an access token and expiry. Its
  workspace record carries the nonsecret marker `yolo-broker` where Pi's schema requires a
  refresh string. Pi never receives the canonical refresh token, so Pi's per-workspace file lock
  is no longer responsible for cross-workspace serialization.

The host service checks expiry proactively once per minute and refreshes within five minutes of
expiry. A permanent upstream refusal preserves the last state for diagnosis, marks login as
required, and suppresses further unattended redemption attempts. Status output contains token
fingerprints: eight hexadecimal characters derived from a token's SHA-256 digest. A fingerprint
helps correlate generations without revealing the token.

Browser login also runs in the host service. It binds host loopback port 1455, falling back to
1457, prints the authorization URL through the client, validates the OAuth state on the exact
callback path, exchanges the code with PKCE, and atomically replaces canonical state. Directly
launched host Codex and its credential file remain outside this service.

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
`YOLO_AUTH_PRELAUNCH_<BIN>_*` values the launched command's pack declares in its `env`, as the
launch composes them ([notch convergence item 15](../plans/notch-convergence.md#tier-4--the-host-runs-the-jails-checks-p1-p4)).
So `yolo host -p codex -- pi` gives Pi's provider extension the private host Unix socket, and a
`yolo host -- pi` or `yolo host -p zai -- pi` does nothing of the kind, since pi's pack declares
its view on the `codex` profile only. `yolo host -- codex` writes the native credential view under
`<global storage>/host-agents/<the declaring pack>` (`codex` for the shipped pack), sets
`CODEX_HOME` to that directory, and starts a dynamic loopback refresh adapter. With no login, the
browser login starts only at a terminal; off one the launch says a login is required and runs
without the credential, as a jail's launcher does. Yolo supervises Codex and closes the adapter when Codex exits. The
generated Codex wrapper delegates to the same command. The managed home's `config.toml` is a
COPY of the ordinary host one, rebuilt at every launch with this launch's workspace trusted and
the daemon key below, so the ordinary `config.toml` is read and never written; `AGENTS.md` and
`skills` are links to the ordinary host Codex home when present; the managed home's own
`auth.json`, session state, and cache stay separate. A direct `codex` launch and `~/.codex/auth.json` are untouched.
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
> since 2026-09-29 (`fea3b6c7`) the JAIL half arrives by another route: the refresh adapter is a
> `jail_daemon`, which this backend does not run, so the launch opens it outside the Seatbelt
> sandbox as a listener it owns on the Mac's loopback, and `CODEX_REFRESH_TOKEN_URL_OVERRIDE`
> names that port ([`host-notch-services.md` HS-D15](../design/host-notch-services.md#HS-D15)).
> MEASURED on a hosted Mac on 2026-09-30 (`TestMacosUserOpensTheCodexDoorwayOutsideTheSandbox`,
> `macos-user.yml` run 36719581090), with no Codex run through it. Until that change this
> warning said the adapter's port was bound by nothing. ⚠ It also said the service was "the **one** loophole the two macOS backends carry"
> and that the `macos-user` arm "starts it by hand" until 2026-09-18; both described the arm as it
> was before its lifecycle was generalised. Starting a service is still not the same as the jail
> reaching it, and the agent pack dependency alone creates no second credential path. See
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

Then an agent selects the `bedrock` profile: `yolo -p bedrock -- claude`, or
`-p <agent>=bedrock` for another agent. claude, codex, opencode and pi each `need`
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
  selected profile is `bedrock`, and in no other process's environment, a bare shell's included
  ([`OQ-CN7`](../design/provider-credential-scope.md#OQ-CN7)). The **caller token** is a secret
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

The adapter starts only on a launch where some agent's profile is `bedrock`, and the pointer
crosses only where the adapter runs. A launch that does not run it leaves the pointer out and
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

- The declaration is evaluated only while the pointer is delivered, which takes an agent on the
  `bedrock` profile.
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

**The launch warns first, and proceeds.** A launch where some agent's profile is `bedrock` asks
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
asks the same way, before the sandboxed command runs. `yolo host` starts no aws-auth service
and asks nothing ([`host-notch-services.md` HS-D20](../design/host-notch-services.md#HS-D20)).
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
starts no host service but the OpenAI one, so none of this runs there.
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
`git config --global`. Full account: [`git-identity.md`](git-identity.md).

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

Most agents authenticate **inside the jail**. Codex and Pi can instead use yolo's OpenAI
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
  every jail on a host. Codex and Pi receive generated views from canonical service state and
  cannot write that state directly. Claude history stays isolated per host workspace even when
  the home is shared, because the history file is keyed on a hash of the host directory.

> [!WARNING]
> **Do not generalize either sharing mechanism.** Claude shares one agent-native file. OpenAI
> subscription authentication keeps one canonical service file and materializes narrower Codex
> and Pi views. Other agent overlays are still seeded one-way per workspace.

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

`macos-user`'s defining property for credentials is its **single shared home**: every session runs
as one hidden Unix user, and there are **no mounts** — so every credential surface that is a mount
on the container backends is either an imperative replay or "just the real home." The security
consequence is that **all concurrent `macos-user` sessions share one credentials file per agent**;
the shared home *is* the sharing mechanism, with no per-workspace and no per-session separation.

Its boundary is the Seatbelt profile: `(allow default)`, then deny reads under the users root
(re-allowing only the sandbox home and the neutral workspace) and deny the system keychain
directory. So other host users' homes and the login keychain are unreadable, while the network is
fully open.

| Credential mechanism | podman | container (Apple Container) | macos-user |
| :--- | :--- | :--- | :--- |
| Host cred propagation | none — not mounted | none — not mounted | none — Seatbelt read-denies the users root and the keychain dir |
| git identity, global gitignore | composed file, `:ro` bind | composed file, **materialized** (no nested `:ro`) | forwarded as env, replayed with `git config --global` |
| `env_sources` | file mounted, sourced | file **materialized**, sourced | baked onto the launch argv via `env -i` |
| Per-agent host settings grant | `/ctx/host-<pack>/` `:ro` mount, then boot compose | materialized copy, then boot compose | boot compose, fail-open — no `/ctx`, same pure generators |
| User `host_files` | source-bearing: `/ctx/host-user/<slug>` `:ro`; source-less: composed | source-less composes; a **file** `source` is materialized (copied into the workspace home, since Apple Container cannot bind a single file there); a **directory** `source` binds `:ro` from Apple Container 1.1.0 and is skipped with a named reason below it or when the version is unreadable | source-less composes; a **file** `source` is copied into a root-owned `/ctx` tree (2026-09-13); a **directory** `source` is skipped and warned |
| Claude shared credentials | shared bind + relative symlink | shared bind **nested inside** the whole-home bind, then the same relative symlink — one mount per declared shared dir (2026-08-24; before that the single bind put the creds in the per-workspace home) | free — one real credentials file in the shared home |
| claude-oauth-broker | active when the `claude` pack is selected | **skipped whole** — no singleton is ensured on this backend, the host-service start admits only the OpenAI service, and the container args drop the loophole for its `intercepts` (which need `--add-host`) | **host half runs, jail half does not.** ⚠ This cell said "the arm returns before any broker ensure", which stopped being true when the arm's lifecycle was generalised: the singleton is ensured and a per-jail front publishes `claude-oauth-broker.endpoint` (measured, unit, 2026-09-18). Nothing uses it — the TLS terminator that would route a refresh through it is a `jail_daemon`, this backend runs none, and the interception would need an `--add-host` it cannot emit either. So refreshes are still not serialized, and the launch now declines the terminator by name |
| OpenAI subscription credentials | canonical host-service state; Codex and Pi get workspace views | the **one** service this backend starts, endpoint file mounted — and measured unreachable from the guest, so the agent sees "OpenAI login is required" ([G6](../plans/setup-support-gaps.md#2-ranked-gap-backlog)). The launch **names the cause** as of 2026-09-18: this was the one pack the inert report was withheld for, so the single service this backend starts was the single one it said nothing about. The endpoint variable and the mount are still emitted — the measurement is per BACKEND, so withholding one service's pointer would patch a per-service hole in a per-backend fact | host daemon started like every other, and a launch that cannot start it is the one that is **refused**; the endpoint path rides the sandbox env instead of a mount. Its refresh adapter does not run as a jail daemon: the launch opens it outside the sandbox as a listener it owns, and Codex's refresh URL names that port (2026-09-29, HS-D15; MEASURED on a hosted Mac 2026-09-30, with no Codex run). Until then a session worked only until its first token refresh |
| AWS SSO credentials (`aws-auth`) | host singleton; adapter in the jail; pointer and caller token in the env file of the agent on `bedrock`. MEASURED, in daily use (2026-09-29) | not started: this backend starts no host service but the OpenAI one | host singleton; the adapter opens outside the Seatbelt sandbox as a listener the launch owns, on the Mac's loopback the agent shares, and the pointer names its port. UNMEASURED: its hardware test ran on a hosted Mac on 2026-09-30 and was refused at the region pre-flight before its probe, so nothing past the launch was observed. `yolo host`, which is no backend, does the same for the one agent it runs when that agent is on a Bedrock provider ([`host-notch-services.md` §4.8](../design/host-notch-services.md#48-yolo-host)), MEASURED by unit tests with a fake agent and a fake `aws` |
| Host-service loopholes | endpoint file + `YOLO_SERVICE_*_ENDPOINT` | only the OpenAI credential service starts; its endpoint file crosses in the host-services dir bind, gated on the loophole being active and its pack cleared to run host code. Every other pack host daemon is skipped and each one is reported inert | **every host daemon starts**, through the same spawn boundary and the same exec disclosure the container path uses; each endpoint's path rides the sandbox env with a per-file ACL grant instead of a mount. ⚠ "the same one service and nothing else" is retracted (2026-09-18) — it described the arm before the generalisation. The inert report here is the PLATFORM axis only. The `jail_daemon` half runs for nothing and is declined by name, except a doorway, one whose `jail_daemon` declares `host_cmd`, which the launch opens outside the sandbox instead (HS-D15) |
| Per-workspace cred isolation | per-workspace `.yolo/home` overlay | one whole-home bind per workspace, but the claude dir is shared across workspaces there | **one shared home for all sessions** |
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
- **Not per-session isolation on `macos-user`.** One home, all sessions, by construction.

## Why it's this way

Rulings a future change would otherwise undo, kept with their original IDs.

| Ruling | Why it holds |
| :--- | :--- |
| **[OQ-A10](loophole-system.md#oq-a10)** — the broker is a *contribution* of `packs/claude`, not a pack of its own | The dependency is structural: there is no jail that wants the broker and not claude. Selecting the pack is the declaration, which is why the host-side activation probe could be deleted rather than replaced. |
| **[OQ-A1](loophole-system.md#oq-a1)** — the broker stays on by default through `default_enabled: true` on its own manifest | Every other shipped loophole is host access you ask for; this one keeps a credential you already asked for from being burnt, so the two need opposite defaults. Off by default means silently reintroducing the race. |
| **P1 (broker)** — concurrent consumers must be serialized by a host-wide flock | Anthropic mints single-use refresh tokens. This is a property of the upstream service, not of yolo's architecture, so no refactor retires it. |
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
| Resolved `env_sources` file | `~/.config/yolo-user-env.sh`, `export K=${K:-'v'}` lines — the values no provider claims | `internal/cli/run/userenv.go` |
| Per-agent env file | `~/.config/yolo-agent-env/<agent>.sh`, `0600`, a `:ro` bind on podman and written in place on Apple Container — the credentials and gated env the credential gate scopes to that agent, sourced by its launcher ([`providers.md`](providers.md#the-credential-gate)) | `internal/cli/run/agentenvfiles.go`, `internal/entrypoint/agentenv.go` |
| Jail service dir | `/run/yolo-services/` | `internal/svcendpoint`, `internal/loopholes` |
| Endpoint file | `<name>.endpoint`, mode `0600`, named by `YOLO_SERVICE_<NAME>_ENDPOINT` | `internal/svcendpoint` |
| Declared-service socket | `<name>.sock`, named by `YOLO_SERVICE_<NAME>_SOCKET` | `internal/loopholes` |
| Broker refresh lock | `oauthbroker.RefreshLockPath` | `internal/oauthbroker/refresh.go` |
| Broker refresh floor | **`ConsumerRefreshDueMS + 60_000`** = 360s — `DoRefresh` serves the on-disk token above this and mints below it. Derived, not written, so it cannot drift under the consumer's threshold ([why](#-the-90300-threshold-mismatch--a-live-defect)) | `internal/oauthbroker/oauthbroker.go` (`RefreshCacheFloorMS`, `cachedForRefresh`) |
| Broker liveness floor | **90s** — the `cached` action's floor, a different question and deliberately lower | `internal/oauthbroker/oauthbroker.go` (`LiveTokenFloorMS`, `CachedTokens`) |
| Consumer due-threshold | **300s** — Claude Code's own `Date.now()+300000>=expiresAt`. A fact about the client, recorded so the floor above derives from it | `internal/oauthbroker/oauthbroker.go` (`ConsumerRefreshDueMS`); measured, 2.1.278 |
| Background refresher | lead **1800s** (thirty minutes, since 2026-09-29; it was 300s, the value the threshold section above describes), refreshing with a cache floor equal to the lead; tick **60s**, fast retry **5s** × **12** ([CL-D5](../design/claude-login-without-interception.md#CL-D5), [CL-D18](../design/claude-login-without-interception.md#CL-D18)) | `internal/oauthbroker/oauthbroker.go` (`BackgroundRefresh*`), `internal/oauthbroker/refresh.go` (`doBackgroundRefresh`) |
| Vendor refresh lock | `<storage dir>/.oauth_refresh.lock`, the storage dir being `CLAUDE_SECURESTORAGE_CONFIG_DIR ?? CLAUDE_CONFIG_DIR ?? ~/.claude`; `realpath:false`, stale `60000`, update `5000`. **Machine-scoped** since [CL-D22](../design/claude-login-without-interception.md#CL-D22), on every jail launch but a credential-view launch, whose lock stays in the workspace's `~/.claude`; one lock across Apple Container VMs is unmeasured ([above](#the-claude-oauth-broker)) | Claude Code bundle (`acquireOAuthRefreshLock`), measured; `internal/cli/run/claudesecurestorage.go` |
| Broker credentials file | the shared-credentials dir under the global home | `internal/storage` (`ensure.go`), `internal/entrypoint/claude.go` |
| Broker daemon | `yolo internal daemon claude-oauth-broker`, `scope: "host"` | `internal/broker`; `packs/claude/loopholes/claude-oauth-broker/manifest.jsonc` |
| OpenAI canonical state | `<loophole state>/credentials.json`, mode `0600`; parent and lock are private | `internal/openaiauth`; `packs/openai-auth/loopholes/openai-auth-broker/manifest.jsonc` |
| OpenAI credential daemon | `yolo internal daemon openai-auth-broker`, `scope: "host"` | `internal/openaiauthdaemon`; `packs/openai-auth/loopholes/openai-auth-broker/manifest.jsonc` |
| OpenAI jail endpoint | `YOLO_SERVICE_OPENAI_AUTH_BROKER_ENDPOINT` | `internal/openauthclient` |
| AWS credential daemon | `yolo internal daemon aws-auth`, `scope: "host"` | `internal/awsauthdaemon`; `packs/aws-auth/loopholes/aws-auth/manifest.jsonc` |
| AWS settings keys | `loopholes.aws-auth.settings.profile`, `.role_arn`, `.session_policy` (strings) and `.unnarrowed` (bool, default `false`), every one `scope: "user"`; the loophole ships `default_enabled: false` | `packs/aws-auth/loopholes/aws-auth/manifest.jsonc`; `internal/awsauth` (`settings.go`) |
| AWS pointer | `AWS_CONTAINER_CREDENTIALS_FULL_URI=http://{listen}/credentials` and `AWS_CONTAINER_AUTHORIZATION_TOKEN={caller_token}`, gated on the `bedrock` profile, `served_by: "aws-auth"` | `packs/aws-auth/pack.json` |
| AWS mint timing | re-mint below **10 min** of remaining life (twice the SDK's five-minute window), a pre-mint tick at half that; `AssumeRole` asks **3600 s**, the role-chaining ceiling, as session name `yolo-jail` | `internal/awsauth` (`RemintLead`, `Broker.TickInterval`, `MintDuration`, `SessionName`) |
| AWS lapse answer | a `4xx` with `Code: ExpiredToken` and a message naming `aws sso login --profile <profile>`; a profile missing from `~/.aws/config` answers `ProfileNotFound` instead | `internal/awsauth` (`loginRequired`, `mint.go`) |
| AWS caller refusal | `401`, `Code: CallerUnauthenticated` | `internal/awscredadapter` (`handler.go`) |
| AWS service log | `~/.local/share/yolo-jail/logs/host-service-aws-auth.log` on the host: the profile, the narrowing and the SSO form at start, then each failed mint the pre-mint ticker or a launch check ran | `internal/awsauthdaemon` (`reportStartup`, `mintTracker`) |
| AWS launch check | declared as `host_daemon.launch_check: true`; asked by a launch that serves the adapter; answered from the cache, or from the mint a cold cache needs, within a **2 s** budget the daemon clamps to at most **5 s**, the launch reading **1 s** past it; a failure prints `loophole aws-auth: cannot mint a Bedrock credential for this launch: …` | `packs/aws-auth/loopholes/aws-auth/manifest.jsonc`; `internal/hostservice` (`LaunchCheckBudget`, `LaunchCheckBudgetCap`), `internal/awsauthdaemon` (`launchcheck.go`), `internal/cli/run` (`launchcheck.go`, `launchCheckMargin`) |
| AWS canonical state | `<loophole state>/credentials.json`, mode `0600` in a `0700` directory | `internal/awsauth` (`state.go`) |
| AWS jail endpoint | `YOLO_SERVICE_AWS_AUTH_ENDPOINT`, read by the in-jail adapter, which listens on `127.0.0.1:1461` (its `jail_daemon.listen`), or on a port the launch picked when the jail shares its launcher's network namespace. Emitted by any launch where the loophole is active and its pack may run host code, like every other `scope: "host"` loophole's — `hostServicesMountArgs` derives the set from the manifests rather than naming services one by one, which it did until 2026-09-20 (two names, and this one was the third, so the adapter answered `ServiceUnreachable` for every request while the launch reported a healthy jail). On `macos-user` the launch runs the adapter outside the sandbox instead, through `jail_daemon.host_cmd`, and `yolo host --` runs it the same way for an agent on a Bedrock provider, handing the adapter the endpoint of a front of its own in its input file (`internal/cli/run/hostdoorways.go`). ⚠ Not on Apple Container, which starts no host service but the OpenAI one | `internal/awscredadapter` (`EndpointEnv`); `internal/cli/run/assemble_parts.go` |
| Codex refresh adapter | `http://{listen}/oauth/token`: `127.0.0.1:1460` on a jail with its own network namespace, a port the launch picked on one sharing its launcher's (`network.mode: "host"`, or nested) | `internal/openaiauthadapter`; the pointer in `packs/codex/pack.json`, the port as `jail_daemon.listen` in `packs/openai-auth/loopholes/openai-auth-broker/manifest.jsonc` |
| Git identity keys carried | `user.name`, `user.email`, plus an in-jail `core.excludesFile` | `internal/cli/run` (`composeGitconfig`), `internal/entrypoint/identity.go` |
| `macos-user` identity replay vars | `YOLO_GIT_NAME`, `YOLO_GIT_EMAIL` only — `YOLO_GLOBAL_GITIGNORE` is read by the entrypoint and **set by nothing**, so the global gitignore does not replay on this backend | `internal/macosuser` (`MacosSandboxEnv`), `internal/entrypoint/identity.go` |
| Config keys in this story | `env_sources`, `host_files`, `host_services`, `loopholes` | `yolo config-ref` |
