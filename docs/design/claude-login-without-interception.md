---
title: "Claude login without interception: share the grant, not the file"
date: 2026-09-28
status: in-review
tags: [design, credentials, oauth, claude, broker, interception, notches, network, macos-user]
summary: "Why yolo can stop intercepting platform.claude.com. The race exists only because each jail's Claude redeems the one single-use refresh token itself. If the host broker is the only holder of that token and writes each workspace a credential view with the current access token and no refresh token, Claude never refreshes, so there is nothing to intercept: no hosts entry, no CA, no listener on port 443, at every notch and on every backend. Claude Code has no supported endpoint override, and the options that keep a network hop (a proxy, the vendor's ssh tunnel mode, the wire bridge) each cost more. Two product questions remain: whether the view replaces interception everywhere, and what /login and /logout in a jail mean."
vantage:
  status-chip: true
---

# Claude login without interception: share the grant, not the file

**Status:** DESIGN, 2026-09-28. Nothing here is built. Vendor facts are read from the Claude Code
**2.1.284** binary installed in this jail (`claude --version` prints `2.1.284 (Claude Code)`). No
experiment ran Claude, so every statement about what Claude *does* is INFERRED from its code and
is listed in [§7](#7-what-must-be-measured-before-building) as a measurement owed.

> **In short.** No, the `/etc/hosts` entry is not needed, if [§7](#7-what-must-be-measured-before-building)'s
> measures confirm what Claude's code says. yolo intercepts
> `platform.claude.com` because every jail's Claude refreshes the shared login itself, and each
> refresh spends the one single-use refresh token. Claude refreshes only when its stored
> credential holds a refresh token. So the host broker keeps the refresh token to itself and
> writes each workspace a **credential view**: the current access token, its real expiry, and no
> refresh token. Claude then never contacts the token endpoint. It picks up each new access token
> from the file on its own. With nothing to intercept, there is no hosts entry, no CA, no
> terminator, and no port 443, on a private namespace, a shared one, `macos-user` and Apple
> Container alike. The wire bridge is not needed.

**Why it matters.** Using a Claude Team or Max login with `network.mode: "host"` is ordinary, and
today it breaks. The terminator must hold `127.0.0.1:443`, which on a shared network namespace is
the host's port ([OQ-NC2](../plans/notch-convergence.md#OQ-NC2)). The maintainer, 2026-09-28:
*"I want to fix this for real. I don't love any of these options … do we really need [the
/etc/hosts entry]? … This is just completely normal, like wanting to use your claude teams with
host networking."*

**Start at [§3](#3-the-measured-facts)**, the facts that close most doors, then read
[§4](#4-the-options-that-remove-the-hosts-entry)'s table.

**Rulings:** [OQ-CL1](#OQ-CL1) and [OQ-CL2](#OQ-CL2), both ruled 2026-09-28 as leaned. Nothing here awaits a ruling; the build and its measures are the remaining work.
run) and [OQ-CL2](#OQ-CL2) (what `/login` and `/logout` in a jail mean).

---

## 1. The verdict

1. **Claude Code has no supported way to move its token endpoint.** The one override it reads
   is allowlisted to three Anthropic hosts. Its localhost developer configuration is unreachable
   in the shipped build ([F1](#F1)). So "redirect it somewhere better" is not available.
2. **The interception is needed only because Claude refreshes.** Claude sends a refresh only when
   its stored credential carries a refresh token ([F4](#F4)), and it re-reads the credentials file
   before each refresh check ([F5](#F5)). A file that the broker keeps current and that carries no
   refresh token needs no interception ([§5](#5-the-recommended-shape)).
3. **This is the OpenAI service's design, applied to Claude.** The OpenAI broker already keeps
   the canonical refresh token host-side and hands each agent a view
   ([`openai-auth-broker.md` §2](openai-auth-broker.md#2-one-writer-and-two-views)). Pi's view
   carries a marker where its refresh token would go. Claude's view simply omits the field.
4. **The wire bridge does not make the best option better.** The bridge would help the options
   that keep a network hop ([§4](#4-the-options-that-remove-the-hosts-entry), options B and C). It
   already has a per-launch caller token and a shared-namespace port picker. The recommended
   option has no network hop, so it has nothing for the bridge to carry.

## 2. Terms

- **Credential view** — a per-workspace `.credentials.json` the host broker writes for Claude:
  the current access token and its expiry, with no refresh token. It is not the canonical
  credential. It is not a symlink to a shared file, which is today's mechanism. The word "view"
  is [`openai-auth-broker.md` §2](openai-auth-broker.md#2-one-writer-and-two-views)'s, for the
  same thing on the OpenAI side.
- **Canonical credential** — the one record that holds the refresh token, owned and written by
  the host broker under its refresh lock. It is not what any Claude process reads. Today the
  canonical credential and the file every jail reads are the same file (the machine-scope
  `.claude-shared-credentials/.credentials.json`).
- **Tunnel mode** *(coined here)* — the state Claude Code enters when `ANTHROPIC_UNIX_SOCKET` is
  set and `CLAUDE_CODE_OAUTH_TOKEN` is the literal `ssh-placeholder`. It is the remote half of the
  vendor's `claude ssh` command ([F7](#F7)). It is not the `ANTHROPIC_UNIX_SOCKET` alternative
  [`claude-oauth-interposition.md`](../reference/claude-oauth-interposition.md#the-rejected-alternative-no-credential-in-the-jail-at-all)
  rejected, but it is the same door with the vendor's own name on it.
- **MEASURED** means observed in this jail on 2026-09-28: a command's output, or the text of the
  installed 2.1.284 binary found by the search string given. **SOURCED** means stated in the
  vendor's documentation or in this repository's docs, linked. **INFERRED** means reasoned from
  those, and not observed running.

## 3. The measured facts

Each fact names its search string, so it can be re-found in a newer binary with
`rg -a -o -b '<string>.{0,400}' ~/.local/share/claude/versions/<version>`. Byte offsets are
2.1.284's and will move.

### 3.1 The endpoint cannot be moved

<a id="F1"></a>**F1. No override reaches a URL yolo chooses.** MEASURED. The configuration
function applies `CLAUDE_CODE_CUSTOM_OAUTH_URL` only after this check:

```js
// 2.1.284, offset 197742816
if(!APe.includes(_))throw Error("CLAUDE_CODE_CUSTOM_OAUTH_URL is not an approved endpoint.");
// where APe = ["https://beacon.claude-ai.staging.ant.dev","https://claude.fedstart.com","https://claude-staging.fedstart.com"]
```

It would also move `BASE_API_URL` and the credential file suffix (`-custom-oauth`), so it is not a
token-endpoint override even for an approved host. The `CLAUDE_LOCAL_OAUTH_*` variables feed a
config selected only when the build's environment function returns `"local"`, and in 2.1.284 that
function is `function s(){return"prod"}` (offset 197739566). The official environment-variable
reference lists no token-URL variable either (SOURCED,
[code.claude.com/docs/en/env-vars](https://code.claude.com/docs/en/env-vars)). The network
requirements page says token exchange, refresh and revocation go to `platform.claude.com`
(SOURCED, [network-config](https://code.claude.com/docs/en/network-config)). This agrees with
[`claude-oauth-interposition.md`](../reference/claude-oauth-interposition.md#how-the-handshake-is-trusted)
for 2.1.278, so the conclusion survived a version bump.

<a id="F2"></a>**F2. The token endpoint and the model endpoint are separate constants.** MEASURED.
`TOKEN_URL:"https://platform.claude.com/v1/oauth/token"` and
`BASE_API_URL:"https://api.anthropic.com"` sit in one constants object (offset 197740550).
Refresh posts `{grant_type:"refresh_token",refresh_token,client_id,scope}` to
`ln().TOKEN_URL` (offset 200820937).

### 3.2 When Claude refreshes, and when it does not

<a id="F3"></a>**F3. The access token lives eight hours.** MEASURED from this jail's shared
credentials file: `expiresAt` minus the file's modification time is 8.00 hours. Its fields are
`accessToken`, `refreshToken`, `expiresAt`, `scopes`, `subscriptionType` and `rateLimitTier`
(field names only were read).

<a id="F4"></a>**F4. Claude refreshes only when the stored credential has a refresh token.**
MEASURED text, INFERRED behavior. The refresh check is:

```js
// 2.1.284, offset 200790807 (function Oa)
await RE(e,S);let M=await Wa(S);
if(!g){if(M&&!FL(M.expiresAt))return"not_needed";if(!M?.refreshToken)return"no_refresh_token"}
if(!M?.refreshToken)return"no_refresh_token";
```

`g` is the forced refresh a 401 triggers. Both the scheduled and the forced paths return
`no_refresh_token` before any request when the field is absent. `FL` is the due test,
`n+300000>=e` (offset 200825426), so a token with more than five minutes left is `not_needed`.

<a id="F5"></a>**F5. Claude re-reads the file before each refresh check, and adopts a change.**
MEASURED text, INFERRED behavior. `RE` at the top of `Oa` stats `.credentials.json` and clears the
memoized credential when the modification time differs (offset 200781339). The inference client
calls that check (`let _e=await w6({credentials:T,storageV5:C})`, offset 203895139), and so do
the first-party API helper, the cloud-session calls and the feature-flag client. On a 401 the
handler re-reads the store and, if the access token there differs from the one that failed,
adopts it and retries (`tengu_oauth_401_recovered_from_keychain`, offset 200785469). So a file
rewritten from outside reaches a running Claude with no restart, which is what today's broker
already relies on.

<a id="F6"></a>**F6. Claude counts a subscription login by scope, not by refresh token.**
MEASURED text, INFERRED behavior. The subscriber test is
`function ut(){if(!hl())return!1;return vF(pn()?.scopes)}`, where `vF` requires
`user:inference` in `scopes` (offset 200795819). Nothing on that path requires a refresh token.

> [!WARNING]
> **Claude's own credential save replaces a symlink with a regular file.** MEASURED text,
> INFERRED behavior. The plaintext store writes through `xn(path, json, 0o600)`, which stages
> `<path>.tmp.<hex>` beside the file and `rename`s it onto the path (offset 198489293, with no
> `followSymlinks`). So the first time Claude saves a credential in a jail, today's
> `~/.claude/.credentials.json -> ../.claude-shared-credentials/.credentials.json` becomes a
> private file and the jail stops seeing the broker's writes. In this jail the link's own
> modification time is 2026-09-04 and it is still a link (MEASURED, `stat`), so Claude has not
> saved through it since. The likely reason is that the broker writes the shared file before it
> answers, and Claude's post-refresh compare-and-swap adopts the newer file rather than writing.
> This is a residual of the current design, and one more reason to stop depending on the link.

### 3.3 The channels that carry no refresh token

<a id="F7"></a>**F7. Tunnel mode is the vendor's `claude ssh` remote.** MEASURED. The predicate:

```js
// 2.1.284, offset 201160397 (function A1)
let n=a.ANTHROPIC_UNIX_SOCKET,i=a.CLAUDE_CODE_OAUTH_TOKEN,r=a.ANTHROPIC_API_KEY,
    s=!a.ANTHROPIC_AUTH_TOKEN&&(i===pxe&&!r||r===pxe&&!i);
e.tunnelSocket=n&&s?n:null
// with var pxe="ssh-placeholder" (offset 199351381)
```

In that mode Claude describes its API host as `"local machine (via claude ssh tunnel)"`, counts
itself a subscriber (`if(a.ANTHROPIC_UNIX_SOCKET)return!!a.CLAUDE_CODE_OAUTH_TOKEN`, offset
200753622), and suppresses the `CLAUDE_CODE_OAUTH_TOKEN` 401 veto (`n$o`, below). Only requests
built with `forAnthropicAPI:!0` go over the socket (offset 199334005): the model client and the
Anthropic-profile client. The many `${ln().BASE_API_URL}/api/...` calls, among them
`/api/oauth/profile`, `/api/claude_cli_profile`, `/api/claude_code/settings` and
`/api/claude_code/metrics`, are built from the constant and go direct. Whether tunnel mode gates
them off or sends them with the placeholder is UNMEASURED.

<a id="F8"></a>**F8. `CLAUDE_CODE_OAUTH_TOKEN` still vetoes rotation in 2.1.284.** MEASURED text.
On a 401 with no stored refresh token, `n$o()` is
`Boolean(a.CLAUDE_CODE_OAUTH_TOKEN)&&!xa()&&!a.ANTHROPIC_UNIX_SOCKET`, and when true Claude logs
*"OAuth 401: keeping the user-supplied CLAUDE_CODE_OAUTH_TOKEN instead of adopting the stored
credential. Mint a fresh token with `claude setup-token` and restart with it"* (offset 200783300).
The new wait for a rotated token polls `process.env`, which only an in-process host can change,
and defaults to zero outside a remote session (`RU`). So an env token that expires mid-session
stays expired until restart. SOURCED, the docs agree: *"To replace an expired token, generate a
new one and restart"* ([env-vars](https://code.claude.com/docs/en/env-vars)).

<a id="F9"></a>**F9. `claude setup-token` mints a one-year, inference-only token.** MEASURED:
`startOAuthFlow(…,{loginWithClaudeAi:!0,inferenceOnly:!0,expiresIn:fY})` with `fY=31536000`
(offset 236436565), and the inference-only scope list is `[mk]`, `user:inference`. An
organization policy can refuse it: *"a long-lived Claude.ai subscription token, which this policy
does not permit"* (same offset). SOURCED: *"It can only make model requests, so it can't establish
Remote Control sessions or fetch claude.ai connectors"*
([authentication](https://code.claude.com/docs/en/authentication#generate-a-long-lived-token)).

<a id="F10"></a>**F10. `apiKeyHelper` and `ANTHROPIC_AUTH_TOKEN` take Claude off the subscription
path.** MEASURED text. `hl()`, the "is this a claude.ai login" test, returns false when
`ANTHROPIC_AUTH_TOKEN` or a helper supplies the credential (offset 200753622), so the request is
not sent as a subscription one. SOURCED: under `forceLoginOrgUUID` or
`forceLoginMethod`, *"`ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, or `apiKeyHelper`: blocked at
startup"* ([authentication](https://code.claude.com/docs/en/authentication#restrict-login-to-your-organization)),
which is the managed-settings shape a Team organization uses. This confirms the memory that a
helper cannot carry the subscription, on firmer ground than before: it is excluded by name.

### 3.4 Pointing Claude at the wire bridge

<a id="F11"></a>**F11. A custom `ANTHROPIC_BASE_URL` carries the saved bearer but not the rest of
Claude.** SOURCED: the saved login's bearer follows `ANTHROPIC_BASE_URL`
([`agent-auth-modes.md` §8.1](agent-auth-modes.md#81-measured-2026-09-02-the-subscription-bearer-follows-anthropic_base_url)).
MEASURED text: only the model client reads that variable. The account calls above use the
`BASE_API_URL` constant, and the binary carries these refusals for a custom base URL:
*"not available with a custom ANTHROPIC_BASE_URL"*, *"Artifacts can't check the organization
settings that apply to this session: the session's configuration (such as a custom
ANTHROPIC_BASE_URL) prevents the policy lookup"*, and *"[ToolSearch:optimistic] disabled"*.
SOURCED: *"Remote Control is disabled when this points at a host other than `api.anthropic.com`"*
([env-vars](https://code.claude.com/docs/en/env-vars)). So a placeholder credential with a
far-future `expiresAt` would never be refreshed ([F4](#F4)), but every direct account call would
send the placeholder to `api.anthropic.com`, and a 401 there forces a refresh with the placeholder
refresh token. That is the vendor's dead-token path, the one
[`claude-oauth-interposition.md`](../reference/claude-oauth-interposition.md#what-it-does-not-buy)
warns blanks the credential file.

<a id="F12"></a>**F12. `HTTPS_PROXY` reaches the refresh call.** MEASURED text. When a proxy
variable is set, Claude installs an axios interceptor that sets a proxy agent on every request
not matched by `NO_PROXY`, and a global undici dispatcher (`function rE`, offset 199334756). The
refresh is an axios post ([F2](#F2)). Claude also copies the proxy into `npm_config_*`,
`YARN_HTTP*_PROXY` and `GLOBAL_AGENT_*` for its child tools (offset 199352558). SOURCED: basic
auth in the proxy URL is supported, and SOCKS is not
([network-config](https://code.claude.com/docs/en/network-config)).

### 3.5 What the wire bridge already gives, and where

<a id="F13"></a>**F13. The bridge authenticates callers and moves ports on a shared namespace.**
SOURCED. Every listener demands the launch's 256-bit caller token
([`wire-bridge.md`](../reference/wire-bridge.md#caller-authentication)). On a shared namespace the
launcher picks a free port for every declared address, the bridge's included
([NC-D42](../plans/notch-convergence.md#NC-D42), built). The terminator's `:443` is the one address
that cannot move, and only because of the hosts entry. `macos-user` starts no jail daemon at all.

## 4. The options that remove the hosts entry

Each option below removes `--add-host platform.claude.com:127.0.0.1`. Moving the endpoint is not
among them, because the vendor offers no way ([F1](#F1)).

| | Option | CA and `:443`? | Shared namespace | `macos-user` and Apple Container | Model traffic through yolo | The refresh race | What breaks |
| :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- |
| **A** | **Credential view.** The broker holds the refresh token and writes each workspace a view without one ([§5](#5-the-recommended-shape)) | both gone | works: no network hop | works: a host process writes a file in a directory already bound in | no | gone: only the broker refreshes | Relies on four vendor behaviors, read not run ([§7](#7-what-must-be-measured-before-building)). A jail cannot refresh while the broker is down, as today. `/login` and `/logout` change meaning ([OQ-CL2](#OQ-CL2)) |
| B | **`HTTPS_PROXY` terminator.** The terminator becomes a CONNECT proxy on an ephemeral port with the caller token in the proxy URL. It terminates `platform.claude.com` and splices every other host | CA stays; `:443` gone | works | `macos-user` needs a host-side proxy; Apple Container still has no route | every HTTPS connection is tunneled through yolo, still encrypted | handled as today | Every child that inherits the variable, and Claude's copies into npm and yarn ([F12](#F12)), routes through a yolo process. A dead proxy is a jail with no network. The CA, doc-fetch coupling and `invalid_grant` hazard all stay |
| C | **Tunnel mode through the wire bridge.** `ANTHROPIC_UNIX_SOCKET` names a socket the bridge serves, with `CLAUDE_CODE_OAUTH_TOKEN=ssh-placeholder`. The bridge injects the broker's current access token | both gone | works: a socket path has no port | `macos-user` needs a host-side server and a sandbox rule for the socket; on Apple Container the bridge has no reachable token source, since the host broker is not admitted there | **all of it**, in plaintext on the socket, re-encrypted by yolo | gone: no credential in the jail | yolo owns a man-in-the-middle on inference, which [`claude-oauth-interposition.md`](../reference/claude-oauth-interposition.md#the-rejected-alternative-no-credential-in-the-jail-at-all) rejected. Account calls outside the socket are UNMEASURED ([F7](#F7)) |
| D | **`ANTHROPIC_BASE_URL` at the bridge** with a placeholder credential | both gone | works | as C | all model traffic | gone | Fails outright: account calls send the placeholder and trigger the dead-token clear, and a custom base URL turns off Remote Control, tool search and the artifacts policy lookup ([F11](#F11)) |
| E | **One `claude setup-token` per machine,** handed to every launch as `CLAUDE_CODE_OAUTH_TOKEN` | both gone | works | works | no | gone: no refresh token exists | Inference only: no Remote Control, no claude.ai connectors, no profile scope ([F9](#F9)). An organization policy may forbid it. A one-year bearer in every jail's environment, against eight hours today. Re-enrollment once a year |
| F | **Stop sharing the login** ([OQ-CI1](../reference/claude-oauth-interposition.md#oq-ci1)) | both gone | works | works | no | gone: nothing is shared | One `/login` per workspace, through manual paste, which [`packhooks.go`](../../internal/entrypoint/packhooks.go)'s `linkSharedCredential` comment calls *"wrong behavior, not an inconvenience"* |

**Why A over the others.** It is the only option that removes the CA and the listener without
putting yolo in the model path and without cutting Claude's account features. It uses a mechanism
the broker already depends on: Claude adopting a file rewritten from outside ([F5](#F5)). It
covers the two macOS backends that today get no serialization at all
([`claude-oauth-interposition.md`](../reference/claude-oauth-interposition.md#where-the-interposition-exists-at-all)).
It takes the refresh token out of every jail. It is what the OpenAI service already does. B keeps
every cost of today's design and adds one. C and D trade a fixed-host CA for owning inference. E
and F trade features or ergonomics away.

## 5. The recommended shape

### 5.1 One writer, one view per workspace

1. **The canonical credential moves into the broker's host state directory**, which no launch
   mounts ([CL-D2](#CL-D2)). The broker already reads and writes it under `refresh.lock`, and its
   background refresher already exists. It now refreshes with a lead of thirty minutes rather
   than five ([CL-D5](#CL-D5)), so a view never nears expiry while the broker runs.
2. **Each Claude workspace gets a view** at `~/.claude/.credentials.json`, a regular file and no
   longer a symlink. It carries `accessToken`, `expiresAt`, `scopes`, `subscriptionType` and
   `rateLimitTier` from the canonical credential, and **no `refreshToken` key**
   ([CL-D1](#CL-D1)). With no refresh token, both of Claude's refresh paths stop before a request
   ([F4](#F4)). With `user:inference` in `scopes`, Claude still counts as a subscriber
   ([F6](#F6)).
3. **The broker rewrites every live view on each new access token**, with a temp file and a
   rename inside the view's directory, confined so that a jail cannot aim the write elsewhere
   ([CL-D3](#CL-D3)). Claude stats the file before each refresh check and adopts the change, and
   a 401 on the old token also adopts it ([F5](#F5)).
4. **The interception is deleted**: the `intercepts` entry, `broker_ip`, `ca_cert`,
   `state_files`, the terminator's jail daemon, the CA trio's mounts, and the CA's entry in the
   jail's trust bundle ([CL-D7](#CL-D7)). `platform.claude.com` resolves to the internet in every
   jail. Claude's login, refresh and revocation calls go upstream directly, and the only one it
   still makes is the login exchange.

### 5.2 What each setup gets

| Setup | Today | With the view |
| :--- | :--- | :--- |
| podman, bridge networking | serialized through the terminator | serialized; no hosts entry |
| podman, `network.mode: "host"` | the second jail finds `:443` taken, or binds the host's ([OQ-NC2](../plans/notch-convergence.md#OQ-NC2)) | serialized; nothing listens |
| nested jail | its own broker and CA ([OQ-2](../reference/claude-oauth-interposition.md#oq-2)) | its own broker, relaying its own view ([CL-D6](#CL-D6)) |
| `macos-user` | terminator declined by name; no serialization | serialized, if the host broker runs there (INFERRED) |
| Apple Container | interception dropped whole; no serialization | serialized, if a host process writes the view (INFERRED; nothing dials in) |
| `yolo host -- claude` | host claude keeps its own login ([OQ-NC7](../plans/notch-convergence.md#OQ-NC7)) | unchanged |

### 5.3 What it removes along the way

These are side effects of deleting the interception, and each one is a hazard the reference doc
records today:

- **The unauthenticated proxy branch** that lets a stranger on a shared loopback complete a login
  and have the mirror write it machine-wide
  ([`claude-oauth-interposition.md`](../reference/claude-oauth-interposition.md#two-fragilities-in-the-split-and-one-missing-check)).
  Nothing listens, so there is nothing to reach.
- **The `invalid_grant` hazard**, where a verbatim upstream error at HTTP 400 would make Claude
  blank the machine-wide file
  ([`claude-oauth-interposition.md`](../reference/claude-oauth-interposition.md#what-it-does-not-buy)).
  A view is per workspace and holds no refresh token, so a blanked view is one workspace's
  problem, and the broker rewrites it.
- **The symlink risks**: the symlink-refusing store the vendor already ships, and the save that
  replaces the link ([§3.2](#32-when-claude-refreshes-and-when-it-does-not)). The view is a
  regular file.
- **The doc-fetch coupling**: `platform.claude.com/docs` reads no longer pass through a framed
  broker exchange with a 30-second deadline.
- **The `NODE_EXTRA_CA_CERTS` list-join bug**: nothing ships a `ca_cert`, so it stays unreachable.

## 6. Costs and risks

- **Four vendor behaviors, read and not run.** A view without a refresh token counts as logged
  in, Claude never posts a refresh for it, it adopts an external rewrite, and a 401 on an old
  token recovers from the file ([§7](#7-what-must-be-measured-before-building)). Any of them can
  move in a minor release, as the interception's own facts can. The difference is that a moved
  behavior here fails loud, as a "Login expired" at the next request, rather than as a silent
  race.
- **No self-rescue while the broker is down.** A jail cannot refresh without the broker, so a
  broker dead for longer than the token's remaining life (up to eight hours, [F3](#F3)) leaves
  Claude reporting *"Login expired · Please run /login"*. Today's terminator has the same
  dependency, because it refreshes through the broker. The failed-spawn warning and
  `yolo check` still name the dead daemon.
- **A host process writes into a jail-writable directory.** A jail could replace `~/.claude` or
  the view with a symlink to a host file. The write must be confined: [CL-D3](#CL-D3).
- **The access token still enters the jail**, as today. An eight-hour bearer is what a
  compromised jail can take. The refresh token, which is a standing grant, no longer enters any
  jail, which is an improvement on today.
- **Running jails keep the old path.** A jail launched before the change has the hosts entry in
  its argv and the symlink in its home until it restarts. The first launch after the change
  migrates the canonical credential ([CL-D2](#CL-D2)). A jail still running on the symlink keeps
  working, because the broker keeps writing that file too until no running container holds it.
- **The "login expires in three days" warning moves.** Claude keeps `refreshTokenExpiresAt` in
  the stored credential (offset 200774422), and the warning presumably reads it (INFERRED). A view
  does not carry it. The broker discards that field today
  ([`agent-credentials.md`](../reference/agent-credentials.md#the-claude-oauth-broker)), so no jail
  shows it now either. Surfacing it becomes the broker's job.

## 7. What must be measured before building

Each can be run in a jail today, against a throwaway view, with the terminator's log as the
witness for any request to the token endpoint. None needs a new login.

| # | Measure | Pass |
| :--- | :--- | :--- |
| M1 | Start `claude -p` on a view with no `refreshToken` key and a fresh access token | the model call succeeds, with the OAuth beta header |
| M2 | The same view with `expiresAt` in the past | no `POST /v1/oauth/token` in the terminator log, and Claude reports "Login expired" rather than crashing |
| M3 | Rewrite the view with a new access token while an interactive session is idle, then send a prompt | the next request carries the new token, and there is no restart |
| M4 | Revoke nothing, but put a wrong access token in the view, then correct it | the 401 recovers from the file ([F5](#F5)) |
| M5 | `/login` in a jail whose view has no refresh token | Claude writes a view that does carry one, which is what [CL-D4](#CL-D4) adopts |
| M6 | `/status` and `/usage` on a view | both show the subscription, since `scopes` and `subscriptionType` are present |

## 8. What this does not cover

- **The host notch.** Host claude keeps its own login, by [OQ-NC7](../plans/notch-convergence.md#OQ-NC7).
  A view at the host would make host claude a second refresher of nothing, which is safe, but it
  is that question's to reopen.
- **The OpenAI service.** It already works this way, and this doc copies its shape. Generalizing
  the two brokers into one engine is
  [`openai-auth-broker.md` §2](openai-auth-broker.md#2-one-writer-and-two-views)'s stated
  intent, and not a prerequisite.
- **[OQ-CI1](../reference/claude-oauth-interposition.md#oq-ci1).** This design keeps the login
  shared and answers the reason that question was asked: sharing the grant rather than the file
  removes the costs it listed.

## 9. Open Questions

The mechanism is decided in [§11](#11-decision-ledger). These two change what a user sees.

1. ✅ <a id="OQ-CL1"></a>**[OQ-CL1](#OQ-CL1): Does the credential view replace the interception
   everywhere, or only where interception cannot run?** This decides whether the CA, the hosts
   entry and the terminator are deleted, or kept for bridged podman jails beside a second
   mechanism for shared namespaces, `macos-user` and Apple Container.

      _Leaning:_ **Everywhere, deleted rather than switched.** Two mechanisms for one concern is what
   [notch convergence](../plans/notch-convergence.md#1-the-thesis) exists to end. The view covers
   every setup the interception covers plus three it cannot. A kept terminator keeps the CA, the
   unauthenticated proxy branch and the `invalid_grant` hazard. The order: build the view, pass
   [§7](#7-what-must-be-measured-before-building)'s measures, run it on a real rootless host for a
   day, then delete the interception in the same release. **Cost:** until the view is measured,
   the interception is the only proven path, so the deletion waits on the measures rather than on
   the build.

   **Answer:**
   > **Ruled 2026-09-28, as leaned: everywhere, deleted rather than switched.** The maintainer:
   > *"we can just write the new one in there and it just picks it up. If that's the case, then
   > yes, we should do that."* The view replaces the hosts entry, the CA and the terminator at
   > every notch, in the order [§10](#10-what-i-would-build-in-order) gives: build the view, pass the [§7](#7-what-must-be-measured-before-building) measures (the first proving
   > Claude adopts a rewritten file with no refresh token), a day on a real rootless host, then
   > delete the interception in the same release.

2. ✅ <a id="OQ-CL2"></a>**[OQ-CL2](#OQ-CL2): What do `/login` and `/logout` in a jail mean?**
   Today a jail's `/login` exchange is proxied, and the proxy mirror makes it the machine's login
   ([`claude-oauth-interposition.md`](../reference/claude-oauth-interposition.md#the-login-flow-is-not-terminated--it-is-the-enrollment-path)).
   A jail's `/logout` revokes the stored refresh token (`${TOKEN_URL}/revoke`, which the proxy
   passes upstream) and so signs every workspace out (INFERRED). With no proxy, a jail's `/login`
   writes a credential with a refresh token into that workspace's view. A jail's `/logout` has no
   refresh token to revoke, because Claude revokes only one it holds (`if(m?.refreshToken)await
   JE(…)`, offset 217147433), and it rewrites the view without its `claudeAiOauth` entry. This
   decides what the broker does with each.

      _Leaning:_ **`/login` in any jail still enrolls the machine; `/logout` in a jail signs out that
   workspace; machine-wide logout is a host verb.** The broker adopts a view that carries a
   refresh token, redeems it once under its lock, and rewrites the view without it
   ([CL-D4](#CL-D4)). So the enrollment users know keeps working. A jail's `/logout` stops the
   broker writing that workspace's view until the next launch, and the launch says so.
   `yolo claude-auth logout` signs the machine out, as `yolo openai-auth logout` does, because a
   jail process changing every workspace's login is the trust problem
   [`agent-credentials.md`](../reference/agent-credentials.md#import-and-logout--the-hosts-two-verbs)
   already ruled out for OpenAI. **Cost:** a jail's `/logout` no longer signs other jails out,
   which is a change a user could notice. **The alternative,** a host-only `yolo claude-auth
   login`, would mean yolo driving Claude's OAuth flow itself, with a client id and scopes the
   vendor can change.

   **Answer:**
   > **Ruled 2026-09-28, as leaned** (the maintainer: *"all of that sounds right"*): `/login` in
   > any jail still enrolls the machine — the broker adopts the view's refresh token, redeems it
   > once under its lock and rewrites the view without it; `/logout` in a jail signs out only
   > that workspace until its next launch, which says so; `yolo claude-auth logout` on the host
   > signs the machine out, as `yolo openai-auth logout` does.

## 10. What I would build, in order

1. The measures, M1 to M6 ([§7](#7-what-must-be-measured-before-building)). If any fails, stop:
   this design is wrong and [§4](#4-the-options-that-remove-the-hosts-entry) should be re-read
   with the failure in hand.
2. The canonical move and its migration ([CL-D2](#CL-D2)), with the broker writing both the old
   shared file and the new store while any container that binds the old one runs.
3. The view writer and its registrations ([CL-D1](#CL-D1), [CL-D3](#CL-D3), [CL-D5](#CL-D5)), and
   the pack change that stops linking `.credentials.json`.
4. Enrollment adoption ([CL-D4](#CL-D4)) and the nested relay ([CL-D6](#CL-D6)).
5. After [OQ-CL1](#OQ-CL1), the deletion ([CL-D7](#CL-D7)), which also closes
   [OQ-NC2](../plans/notch-convergence.md#OQ-NC2) and
   [notch convergence item 4](../plans/notch-convergence.md#tier-1--the-loopback-services-p3).

## 11. Decision Ledger

No ruling has been made in this doc. These are implementation decisions under the design.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="CL-D1"></a>[`CL-D1`](#11-decision-ledger) | *Implementation decision.* A view is Claude's own `{"claudeAiOauth": {…}}` shape with `accessToken`, `expiresAt`, `scopes`, `subscriptionType` and `rateLimitTier` copied from the canonical credential, and **no `refreshToken` key**. It carries no marker, unlike Pi's view: Claude posts whatever refresh token it holds to the real token endpoint ([F4](#F4)), so a marker would be spent upstream as an `invalid_grant`. Its `expiresAt` is the real expiry and never a far-future value, so `/status` and the vendor's own expiry messages stay true | 2026-09-28 | [§5.1](#51-one-writer-one-view-per-workspace) | — |
| <a id="CL-D2"></a>[`CL-D2`](#11-decision-ledger) | *Implementation decision.* The canonical credential lives in the broker's state directory (`BrokerDir()`), which no launch mounts. The first broker start after the change adopts the current shared file under `refresh.lock` as the first canonical generation, then keeps writing that file too while any container that binds it runs, found by the same tri-state liveness check the pack trees use. The machine-scope `.claude-shared-credentials` directory and the claude pack's `shared_credentials` hook then go. On `macos-user` the state directory is on the one real filesystem, so "never crosses" there means only that nothing points Claude at it | 2026-09-28 | [§5.1](#51-one-writer-one-view-per-workspace) | — |
| <a id="CL-D3"></a>[`CL-D3`](#11-decision-ledger) | *Implementation decision.* A launch that selects the claude pack registers its workspace's `.claude` directory with the broker, and the broker writes every registered view on each new access token and at registration. The write opens the directory with `os.Root` and refuses when `.claude` or the view is a symlink, writes a temp file and renames it inside that root, and never follows a link a jail could have planted. A registration is dropped only once its container is known gone, never when liveness cannot be asked | 2026-09-28 | [§5.1](#51-one-writer-one-view-per-workspace) | — |
| <a id="CL-D4"></a>[`CL-D4`](#11-decision-ledger) | *Implementation decision, under [OQ-CL2](#OQ-CL2)'s leaning.* A registered view that carries a `refreshToken` is an enrollment. Under `refresh.lock`, the broker checks the same gates the proxy mirror checks today (Claude Code's client id and inference scope), redeems the token once so the jail's copy is spent, installs the result as the next canonical generation, and rewrites every view. A refresh token the broker has already replaced is ignored | 2026-09-28 | [§9](#9-open-questions) | — |
| <a id="CL-D5"></a>[`CL-D5`](#11-decision-ledger) | *Implementation decision.* The background refresher's lead becomes thirty minutes. The old lead matched Claude's five-minute due threshold because Claude refreshed too, and a view's Claude never does, so the lead now only has to beat real expiry with room for a sleeping machine to wake and rewrite. The refresh path's cache floor, which existed to answer the terminator, goes with the terminator | 2026-09-28 | [§5.1](#51-one-writer-one-view-per-workspace) | — |
| <a id="CL-D6"></a>[`CL-D6`](#11-decision-ledger) | *Implementation decision.* A broker whose store holds no refresh token relays: it copies its own view into the views registered with it and refreshes nothing. That is the nested case, since an outer jail's own view carries no refresh token. The condition is the store's content, not nesting, so [OQ-2](../reference/claude-oauth-interposition.md#oq-2)'s "nesting earns affordances, not exemptions" holds: the nested launcher runs its own broker, as today | 2026-09-28 | [§5.2](#52-what-each-setup-gets) | — |
| <a id="CL-D7"></a>[`CL-D7`](#11-decision-ledger) | *Implementation decision, gated on [OQ-CL1](#OQ-CL1).* The deletion removes the manifest's `intercepts`, `broker_ip`, `ca_cert` and `state_files`, the `oauth-terminator` jail daemon, `EnsureCAAndLeaf` and its CA trio, and the CA path from `NODE_EXTRA_CA_CERTS` and the jail trust bundle. No switch keeps the old path: a hatch is for broken user configuration, never for a second yolo mechanism | 2026-09-28 | [§5.1](#51-one-writer-one-view-per-workspace) | — |
| <a id="CL-D8"></a>[`CL-D8`](#11-decision-ledger) | *Implementation decision.* No Claude environment changes: no `CLAUDE_CODE_OAUTH_TOKEN`, which vetoes adopting the file ([F8](#F8)); no `ANTHROPIC_BASE_URL`, which costs account features ([F11](#F11)); no `HTTPS_PROXY`; no `CLAUDE_CONFIG_DIR`. The view sits where Claude already looks | 2026-09-28 | [§4](#4-the-options-that-remove-the-hosts-entry) | — |

## 12. The neighbors

| Doc | Why it reads with this one |
| :--- | :--- |
| [`notch-convergence.md`](../plans/notch-convergence.md#OQ-NC2) | [OQ-NC2](../plans/notch-convergence.md#OQ-NC2), the terminator on a shared namespace, which the view dissolves; [NC-D15](../plans/notch-convergence.md#NC-D15), the refresh-token caller check this deletes along with its listener |
| [`claude-oauth-interposition.md`](../reference/claude-oauth-interposition.md#why-there-is-a-file-on-disk-at-all) | the channel table this doc re-reads for 2.1.284, the interception it retires, and [OQ-CI1](../reference/claude-oauth-interposition.md#oq-ci1) |
| [`openai-auth-broker.md`](openai-auth-broker.md#2-one-writer-and-two-views) | the one-writer, two-views design this copies |
| [`agent-credentials.md`](../reference/agent-credentials.md#the-claude-oauth-broker) | the broker's rulings, its floors and the background refresher [CL-D5](#CL-D5) retunes |
| [`claude-oauth-refresh-mechanics.md`](../research/claude-oauth-refresh-mechanics.md) | the vendor's refresh state machine, including why writing the file is the right hook |
| [`wire-bridge.md`](../reference/wire-bridge.md#caller-authentication) | the caller token and ports that options B and C would reuse |
| [`loopback-tls-reachability.md`](../reference/loopback-tls-reachability.md#a-nested-jail-is-structurally-blind-to-this) | why a nested jail cannot verify a shared-namespace change, which is why [OQ-CL1](#OQ-CL1)'s leaning asks for a real rootless host |
