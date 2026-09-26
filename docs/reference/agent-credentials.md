---
status: current
verified: 2026-09-20
verified_commit: bd0e4142
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
  - packs/claude/loopholes/claude-oauth-broker/
  - packs/openai-auth/
tags: [credentials, security, boundary, env_sources, host_files, broker, oauth]
---

# Agent credentials — what crosses the jail boundary, and how

**Status:** CURRENT as of 2026-09-09, verified against `41dde711`.

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

**yolo splits that pair.** `packs/claude/pack.json` declares `.claude` as `scope: "workspace"` and
`.claude-shared-credentials` as `scope: "machine"`, joined by a relative symlink — so the
credentials file is shared by every jail while the config directory is per-jail. The vendor's lock
is derived from the config directory, not the credentials path, and it disables symlink resolution
outright:

```js
// Claude Code 2.1.278, measured
function Jar(e,n){return{lockfilePath:lE(e,".oauth_refresh.lock"),realpath:!1,stale:60000,update:5000,...}}
```

`realpath:!1` is the load-bearing character. The lock lands at `<configDir>/.oauth_refresh.lock`
— a **per-jail inode** — and is guaranteed never to follow the symlink through to the shared file.
Two jails refreshing at once therefore contend on nothing. **We shared the file and not the lock,**
and the broker is what puts serialization back.

> [!IMPORTANT]
> **Do not "fix" this by machine-scoping the lock directory too.** It would work on podman, where
> both sides are binds of one host inode — and only there. On `container` each jail is its own VM,
> so a lock is guest kernel state over virtiofs and two VMs do not contend (architectural, marked
> UNVERIFIED — nobody has run it). On `macos-user` there are no mounts at all. A shared-file lock
> is therefore **backend-dependent**; a host-side mediator reached over a socket is not, and that
> — not one-ness, and not a credential boundary — is the property the broker is bought for.

It bundles two jobs.

1. **One shared credentials file per host.** On containers that is a shared-credentials bind plus
   an in-jail *relative* symlink from the agent's own credentials path into it
   (`linkThroughShared`, applied through the pack's `shared_credentials` hook). One OAuth
   identity, every jail.
2. **Serialize the refresh HTTP call.** A host-side daemon holds a flock
   (`oauthbroker.RefreshLockPath`) around every refresh, so concurrent jails cannot burn the
   token. What the job needs is that the lock be taken **host-side**, where every backend agrees
   on the inode; it does not need the daemon to be a singleton. Because Claude Code refreshes *itself* and will never voluntarily take yolo's lock, an
   in-jail TLS terminator intercepts the refresh host (via an `--add-host` mapping to loopback)
   and routes the call through a loopback-TLS front — a goroutine in the launching `yolo run`,
   splicing to the singleton's socket — to the daemon under its flock.

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

Managed host launches share the service too. `yolo host -- pi` gives Pi's provider extension the
private host Unix socket. `yolo host -- codex` writes the native credential view under
`<global storage>/host-agents/codex`, sets `CODEX_HOME` to that directory, and starts a dynamic
loopback refresh adapter. Yolo supervises Codex and closes the adapter when Codex exits. The
generated Codex wrapper delegates to the same command. The managed home links `config.toml`,
`AGENTS.md`, and `skills` from the ordinary host Codex home when present; its `auth.json`, session
state, and cache stay separate. A direct `codex` launch and `~/.codex/auth.json` are untouched.

> [!WARNING]
> The OpenAI service is a host-service loophole, and **neither macOS backend carries it end to
> end** — for two different reasons, which is why one sentence cannot cover both. On Apple
> Container it is allow-listed out of an otherwise total loophole skip: the daemon starts and the
> jail cannot reach it (measured on `container` 1.1.0). On `macos-user` the HOST half is no longer
> special at all — that arm starts every loophole's host daemon through the ordinary spawn
> boundary — and what is missing there is the JAIL half: its refresh adapter is a `jail_daemon`,
> and this backend runs none, so `CODEX_REFRESH_TOKEN_URL_OVERRIDE` points at a port nothing
> binds. ⚠ This warning said the service was "the **one** loophole the two macOS backends carry"
> and that the `macos-user` arm "starts it by hand" until 2026-09-18; both described the arm as it
> was before its lifecycle was generalised. Starting a service is still not the same as the jail
> reaching it, and the agent pack dependency alone creates no second credential path. See
> [the backend table](#per-backend-differences) for what each backend actually delivers.

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
sanctioned way to give a jail scoped access to a credential without handing it over. The wire
format and the reachability requirements are [`loophole-transport.md`](loophole-transport.md)'s.

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

There is no `gemini` pack and there never was, but the **gemini-shaped paths are real**: `agy`
occupies a subdirectory of the gemini tree, `Env.GeminiDir` is a live exported method whose
comment says as much, `Env.AgyDir` is built on top of it, and `yolo prune` sweeps gemini log dirs.
A path under `~/.gemini` is agy's, not a leftover.

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
| OpenAI subscription credentials | canonical host-service state; Codex and Pi get workspace views | the **one** service this backend starts, endpoint file mounted — and measured unreachable from the guest, so the agent sees "OpenAI login is required" ([G6](../plans/setup-support-gaps.md#2-ranked-gap-backlog)). The launch **names the cause** as of 2026-09-18: this was the one pack the inert report was withheld for, so the single service this backend starts was the single one it said nothing about. The endpoint variable and the mount are still emitted — the measurement is per BACKEND, so withholding one service's pointer would patch a per-service hole in a per-backend fact | host daemon started like every other, and a launch that cannot start it is the one that is **refused**; the endpoint path rides the sandbox env instead of a mount. Its jail-side refresh adapter does **not** run, so a session works until its first token refresh |
| Host-service loopholes | endpoint file + `YOLO_SERVICE_*_ENDPOINT` | only the OpenAI credential service starts; its endpoint file crosses in the host-services dir bind, gated on the loophole being active and its pack cleared to run host code. Every other pack host daemon is skipped and each one is reported inert | **every host daemon starts**, through the same spawn boundary and the same exec disclosure the container path uses; each endpoint's path rides the sandbox env with a per-file ACL grant instead of a mount. ⚠ "the same one service and nothing else" is retracted (2026-09-18) — it described the arm before the generalisation. The inert report here is the PLATFORM axis only. The `jail_daemon` half runs for nothing and is declined by name |
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
  configure, and serves it to the jail's AWS SDKs over a loopback URL. The SSO session and
  `~/.aws` stay on the host. For anything else, such as another cloud or a static key, a
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
| Background refresher | lead **300s**, tick **60s**, fast retry **5s** × **12** | `internal/oauthbroker/oauthbroker.go` (`BackgroundRefresh*`) |
| Vendor refresh lock | `<configDir>/.oauth_refresh.lock`, `realpath:false`, stale `60000`, update `5000` — **per-jail**, never follows the shared-creds symlink | Claude Code bundle (`acquireOAuthRefreshLock`), measured |
| Broker credentials file | the shared-credentials dir under the global home | `internal/storage` (`ensure.go`), `internal/entrypoint/claude.go` |
| Broker daemon | `yolo internal daemon claude-oauth-broker`, `scope: "host"` | `internal/broker`; `packs/claude/loopholes/claude-oauth-broker/manifest.jsonc` |
| OpenAI canonical state | `<loophole state>/credentials.json`, mode `0600`; parent and lock are private | `internal/openaiauth`; `packs/openai-auth/loopholes/openai-auth-broker/manifest.jsonc` |
| OpenAI credential daemon | `yolo internal daemon openai-auth-broker`, `scope: "host"` | `internal/openaiauthdaemon`; `packs/openai-auth/loopholes/openai-auth-broker/manifest.jsonc` |
| OpenAI jail endpoint | `YOLO_SERVICE_OPENAI_AUTH_BROKER_ENDPOINT` | `internal/openauthclient` |
| AWS credential daemon | `yolo internal daemon aws-auth`, `scope: "host"` | `internal/awsauthdaemon`; `packs/aws-auth/loopholes/aws-auth/manifest.jsonc` |
| AWS canonical state | `<loophole state>/credentials.json`, mode `0600` in a `0700` directory | `internal/awsauth` (`state.go`) |
| AWS jail endpoint | `YOLO_SERVICE_AWS_AUTH_ENDPOINT`, read by the in-jail adapter on `127.0.0.1:1461`. Emitted by any launch where the loophole is active and its pack may run host code, like every other `scope: "host"` loophole's — `hostServicesMountArgs` derives the set from the manifests rather than naming services one by one, which it did until 2026-09-20 (two names, and this one was the third, so the adapter answered `ServiceUnreachable` for every request while the launch reported a healthy jail). ⚠ Not on Apple Container, which starts no host service but the OpenAI one | `internal/awscredadapter` (`EndpointEnv`); `internal/cli/run/assemble_parts.go` |
| Codex refresh adapter | `http://127.0.0.1:1460/oauth/token` | `internal/openaiauthadapter`; `packs/codex/pack.json` |
| Git identity keys carried | `user.name`, `user.email`, plus an in-jail `core.excludesFile` | `internal/cli/run` (`composeGitconfig`), `internal/entrypoint/identity.go` |
| `macos-user` identity replay vars | `YOLO_GIT_NAME`, `YOLO_GIT_EMAIL` only — `YOLO_GLOBAL_GITIGNORE` is read by the entrypoint and **set by nothing**, so the global gitignore does not replay on this backend | `internal/macosuser` (`MacosSandboxEnv`), `internal/entrypoint/identity.go` |
| Config keys in this story | `env_sources`, `host_files`, `host_services`, `loopholes` | `yolo config-ref` |
