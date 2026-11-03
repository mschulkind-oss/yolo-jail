---
title: "Where each coding agent keeps its remote-MCP sign-ins"
date: 2026-11-03
status: accepted
stage: CURRENT
next: "Confirm the unverified rows with one real sign-in each on a Mac and in a jail (agy, muse-code, Copilot and Codex on macOS)"
tags: [research, mcp, oauth, credentials, agents]
summary: "Five source-read reports, one per agent family, on where each agent stores the OAuth sign-ins for remote MCP servers, how the store is keyed and written, and whether one server can be shared alone. None can: every agent keeps one store for all its servers. The evidence behind the MCP sign-in sharing design."
---

# Where each coding agent keeps its remote-MCP sign-ins

Read from each agent's source or binary text on 2026-11-03; no agent was run and no credential was opened. The design that rests on this is [`mcp-auth-sharing.md`](../design/mcp-auth-sharing.md).

## Claude Code

Researched 2026-11-03 against Claude Code **2.1.317** (`~/.local/share/claude/versions/2.1.317`,
a Bun single-file binary; snippets found with `rg -a -o`), and against yolo at `63b28519e`. No agent run
beyond `--version`. No credential values were read: only key names, with hashes redacted.

### 1. Where the tokens live, and whether yolo already shares them

- **The tokens sit in the same file as the subscription login.** The store is
  `<storageDir>/.credentials.json`. Remote-MCP tokens are under the top-level key `mcpOAuth`, and
  dynamic-client secrets are under `mcpOAuthClientConfig`. The login itself is under `claudeAiOauth`.
  - In the binary: `function S(){let e=F$(),r=".credentials.json";return{storageDir:e,storagePath:j(e,".credentials.json")}}`
    and `async function dB(e){...return(await jn().readAsync())?.mcpOAuth...}`.
  - `n?.mcpOAuthClientConfig?.[s]?.clientSecret` uses the same key `s` (see section 2).
  - yolo's own code already says so: `internal/oauthbroker/views.go:182`, `:188-196` (`accountKeys`,
    which keeps `mcpOAuth`, `pluginSecrets` and `coworkRemoteDevice`), `internal/oauthbroker/store.go:262-270`,
    and `internal/claudeview/claudeview.go:358` (which cites claude-code#45551).
- **On Linux the store is always a plaintext file.** The binary has `function lke(){return"file"}`
  (`lke` is a minified function name). On macOS Claude can keep the store in the Keychain, and the
  Keychain service name is derived from the store directory
  (`docs/reference/claude-oauth-interposition.md:698`). UNVERIFIED for MCP entries on a Mac.
- **`CLAUDE_SECURESTORAGE_CONFIG_DIR` moves the whole store directory**, not single entries. It is in
  the binary's env list, and `docs/design/claude-login-without-interception.md` CL-D22 (line 535) says
  the same.
- **CONFIRMED: yolo already shares the whole store, MCP tokens included, machine-wide, by default.**
  - `packs/claude/pack.toml` declares a `[[state]] at=".claude-shared-credentials" scope="machine"`
    directory, plus a `[[hook]] hook="shared_credentials" from=".claude/.credentials.json"` that links
    the file into it.
  - Under the CL-D22 bridge, the launcher also sets `CLAUDE_SECURESTORAGE_CONFIG_DIR` to that
    directory (`internal/cli/run/claudesecurestorage.go:1-80`, and CL-D23 at design-doc line 536).
    Claude therefore reads and writes the shared file directly, with no symlink in its path.
  - Live check in this jail:
    - `CLAUDE_SECURESTORAGE_CONFIG_DIR=/home/agent/.claude-shared-credentials`.
    - `~/.claude-shared-credentials/.credentials.json` is a regular file with mode 0600. Its top-level
      keys are `claudeAiOauth` and `mcpOAuth`.
    - `mcpOAuth` holds one key, `plugin:slack:slack|<hex>`, which comes from a Claude plugin, not from
      yolo config.
    - `~/.claude/.credentials.json` is the hook's symlink to that file.
- **Exception: the credential-view mode** (`YOLO_CLAUDE_CREDENTIAL_VIEW=1`). It is off by default
  (`claudeview.DefaultOn` returns false, `internal/claudeview/claudeview.go:77`). A view launch turns
  the bridge off (`claudesecurestorage.go`, "MUTUALLY EXCLUSIVE WITH THE VIEW"). Its store is then
  the workspace's own `~/.claude/.credentials.json` (`claudeview.ViewRel`, line 104).
  - In that mode MCP logins are **per-workspace**. CL-D27 point (3) says MCP OAuth is "not seeded"
    into a host view.
  - ⚠ **CL-D7's planned deletion of the bridge in favor of the view would silently turn today's
    machine-wide MCP sharing into per-workspace sharing.** Any design here has to account for that.

### 2. Format

- **One file holds every server, and the model login too.** `mcpOAuth` is an object keyed by
  **`<serverName>|<sha256 of the server config, first 16 hex chars>`**:
  ```
  function Qi(e,r){let o=_({type:r.type,url:r.url,headers:r.headers||{}}),
    n=G("sha256").update(o).digest("hex").substring(0,16);return`${e}|${n}`}
  ```
  The key is the name as written in config plus a hash of `{type, url, headers}`. `_` is presumably
  a stable JSON serializer (UNVERIFIED). So:
  - A workspace server with the **same name, type, URL and headers** as a user-level server shares
    its entry by necessity.
  - The same URL under a different name gets its own entry and its own login.
  - Headers that differ per workspace give a different key.
- Each entry holds `accessToken`, `refreshToken`, `clientSecret` and `discoveryState` (all seen in
  the binary).
- **Writes are read-modify-write of the whole file, under a lock.**
  - Claude calls `jn().mutate(o => ({...o, mcpOAuth: {...o.mcpOAuth, [u]: ...}}))` under
    proper-lockfile's `lock(join(storeDir,".storage-write"), {realpath:false, stale:15000})`. The
    binary shows this, and `claudeview.go:350-361` records the same.
  - The write itself is a temp file plus rename. That rename replaced the old symlink with a regular
    file: the measured loop that motivated CL-D22 (design-doc line 535).
  - Reads open with `O_NOFOLLOW` (`p(e,o.O_RDONLY|o.O_NOFOLLOW|N)`). **A symlink at the store path is
    refused**, so any per-server or per-file trick must not rely on a symlink there.
- The broker already does read-modify-write under that same lock and preserves `mcpOAuth`
  (CL-D13, CL-D25, CL-D26).

### 3. Can only the user-level servers' entries be shared?

**Not natively.** Claude offers no per-server token path, no per-server store, and no knob that
separates `mcpOAuth` from the login. The env variable moves the whole store, and `.credentials.json`
is the only file in it. A split therefore needs yolo to own a merge. Two things make that merge
cheap:

- **yolo can compute every key itself.** The key is a pure function of the server name and the
  `{type,url,headers}` that yolo renders into `~/.claude.json` `mcpServers` (`packs/claude/derive.lua:714-735`).
  At render time yolo knows each server's scope, so it can list the user-level keys.
- **The broker already performs locked, key-preserving read-modify-writes of exactly this file**,
  both for views and for the shared file.

Caveats:

- **Some MCP servers never pass through yolo's composed table.** Claude plugin servers
  (`plugin:<plugin>:<name>`) and project `.mcp.json` servers are among them; the claude pack sets
  `enableAllProjectMcpServers = true` (`packs/claude/pack.toml`, `[[config]]` for `~/.claude.json`).
  yolo cannot place these by scope. The default should treat them as workspace-scoped, or treat
  plugin servers as user-scoped if plugins are user-installed. Owner's call.
- The hash serializer `_` is UNVERIFIED as key-order-stable. An implementation should match keys by
  the name prefix plus a recomputed hash, and test against a real entry.

### 4. Recommendation

There are two parts.

**Now: (a), the status quo.** The whole store is shared machine-wide through the CL-D22 bridge. That
gives every user-level server a shared login today. The cost is that workspace servers' tokens also
sit in the one file every jail reads.

- Because entries are keyed by name and config hash, a workspace server is never *used* by another
  workspace unless that workspace declares it identically.
- So the leak is about who can read the token, not cross-use.
- Isolating it is option (c).

**If the owner wants workspace servers kept private, or once CL-D7 moves Claude to the view: (c), a
merge that yolo owns.** Claude cannot do this split itself, so the cheapest honest mechanism is:

1. Each workspace uses its own store, as the view mode already does.
2. The host broker, which already rewrites each view under `.storage-write.lock` keeping the other
   keys, copies the `mcpOAuth` and `mcpOAuthClientConfig` entries whose keys are in the launch's
   user-level set (computed by yolo with `Qi`'s formula) **between a machine-scope MCP store and
   each view**, in both directions.
3. It resolves conflicts by newest-wins on the token's expiry.

This extends CL-D13 and CL-D25 and needs no new vendor surface. One open problem: two jails holding
the same MCP refresh token can race a single-use refresh. That is the same class of problem the
claude-oauth-broker exists for, and it is UNVERIFIED whether MCP providers rotate refresh tokens.

## Codex

Source read: openai/codex tag `rust-v0.166.0` (the version the jail's launcher installed today,
`~/.codex/packages/standalone/releases/0.166.0-x86_64-unknown-linux-musl`). Copies of the files
read are under `codex-src/` beside this note. `R` = `codex-rs/` at that tag,
https://github.com/openai/codex/tree/rust-v0.166.0/codex-rs. No codex was run except
`--version`; no credential file exists in this jail (`~/.codex` has no `.credentials.json`).

### 0. Precondition yolo does not meet today (cross-cutting, verified)

yolo cannot declare a remote MCP server for codex at all. `mcp_servers.<name>` accepts only
`command`/`args`/`env`/`provides`/`requires_env` (`internal/cli/config_ref.txt:2920-2945`), and
`packs/codex/derive.lua:395-406` copies only `command`, `args`, `env`. A remote (`url`) server
reaches codex only through `codex mcp add --url` writing `~/.codex/config.toml`, a file yolo
regenerates ("hand edits may be reverted", the file's own header). So "user-level vs workspace"
scope is not knowable for codex remote servers until yolo's MCP schema grows `url` and the codex
derive renders it.

### 1. Where tokens live

- Setting: top-level `mcp_oauth_credentials_store = "auto" | "file" | "keyring"`, default
  `auto` (`R/config/src/config_toml.rs:306-313`; enum `R/config/src/types.rs:150-165`). Not
  per-server: `McpServerConfig` has no store field (`R/config/src/mcp_types.rs:234-313`).
  Distinct from `cli_auth_credentials_store` (model login, `auth.json`), config_toml.rs:289-295.
- `auto`: keyring first; on a keyring save error, falls back to the file
  (`R/rmcp-client/src/oauth.rs:600-634`); on load, keyring `Ok(None)` falls through to the file
  (`R/rmcp-client/src/oauth/resolved_store.rs:343-372`). The chosen store is pinned per client
  lifecycle (resolved_store.rs:36-41).
- Keyring backend kind defaults to `Direct` on Linux/macOS (`types.rs:177-184`): service
  `"Codex MCP Credentials"`, account = store key (oauth.rs:100, 530-541).
- File: `$CODEX_HOME/.credentials.json` (oauth.rs:857, 1101-1103); `CODEX_HOME` moves it (and
  everything else codex keeps). The file holds ONLY MCP OAuth entries; the model login is
  `~/.codex/auth.json`, separate.
- **Linux jail (no DBus)**: Linux build uses keyring `linux-native-async-persistent`
  (`R/keyring-store/Cargo.toml`, keyring 3.6.3). Its save writes keyutils, then Secret Service;
  if Secret Service fails it reverts keyutils and returns the error
  (keyring-rs v3.6.3 `src/keyutils_persistent.rs:63-79`). The jail has no
  `DBUS_SESSION_BUS_ADDRESS` and no `/run/dbus` (checked), so `auto` lands in the FILE.
  Unverified by measurement (no login was run).
- **macOS host notch (`yolo host`)**: `auto` writes the login Keychain (apple-native). The key
  does not include CODEX_HOME (except `ema-idp:` servers, oauth.rs:1066-1076, "The OS keyring is
  shared across homes"), so every workspace on that user already shares by name+URL.
- **macos-user backend**: UNVERIFIED whether the Seatbelt sandbox reaches the Keychain
  (`internal/macosuser/seatbelt.go:227-234` denies reads of `/Library/Keychains` and
  `/System/Library/Keychains`; I found no securityd mach-lookup deny). If it does, the store is
  the sandbox account's Keychain (shared across that account's workspaces); if not, `auto`
  falls to the file. A Mac witness is needed.
- **yolo today**: `packs/codex/pack.toml` declares `[[state]] at = ".codex"` (workspace scope)
  and NO `shared_credentials` hook, so the file is per-workspace in a jail. Model login sharing
  is the openai-auth-broker's job, not a hook.

### 2. File format and write discipline

- One JSON object for all servers: `BTreeMap<key, entry>` (oauth.rs:860-880). Entry fields:
  `server_name, server_url, issuer?, client_id, access_token, expires_at, refresh_token,
  scopes, executor_owned?`.
- Key = `"<server_name>|<sha256(json{type:"http",url,headers:{}})[..16]>"`
  (oauth.rs:1056-1084). Lookup matches on `server_url == url` AND server name
  (oauth.rs:896-916). So it is keyed by **config name + URL**: a workspace server with the
  same name and URL as a user one shares its entry by necessity; same URL, different name does
  not.
- Read-modify-write, edited IN PLACE: open with `O_NOFOLLOW`, `set_len(0)`, write
  (oauth.rs:1127-1172); a test pins that a hard link keeps seeing updates
  (oauth.rs:1433-1457). When the map becomes empty the file is `remove_file`d (oauth.rs:1154-1159).
- **A symlink at `.credentials.json` makes every WRITE fail** (`O_NOFOLLOW`, pinned by test
  `fallback_file_write_does_not_follow_symlinks`, oauth.rs:1461-1489); reads follow it. So
  yolo's `shared_credentials` hook (a relative symlink, `internal/claudeview/claudeview.go:109`)
  would break codex sign-in and refresh.
- Locked: `flock` on `$CODEX_HOME/mcp-oauth-locks/file-store.lock` for the whole RMW
  (`oauth/store_lock.rs:19,34,111-128`), and a per-server refresh lock
  `$CODEX_HOME/mcp-oauth-locks/<sha>.lock` (`oauth/refresh_lock.rs:22,52-65`; plain open, no
  NOFOLLOW, so a symlinked dir is fine). Sharing the file without sharing this directory leaves
  cross-jail refreshes unserialized.

### 3. Per-server split?

Not natively: one file per CODEX_HOME, one global store setting, no per-server path. A split
needs yolo to own a merge: compose the jail's file from machine-store entries for user-level
servers plus the workspace's own, and write back after every change. Refresh rewrites the file
(oauth.rs:794 `persist_if_needed`), and a server that rotates refresh tokens would have two
copies burn each other, the same problem openai-auth-broker solves; so the merge would need a
live sync, not a boot-time copy.

Mitigating fact: because entries are keyed by name+URL, a workspace-only server's entry in a
shared file is only USED by a jail that declares that same name and URL. Sharing the whole
file isolates function, not confidentiality (every jail can read every token).

### 4. Recommendation

(a) Share the whole store machine-wide, with these mechanics (not the existing hook):
1. Pin `mcp_oauth_credentials_store = "file"` in the jail's rendered config, so a jail that
   ever gains a Secret Service does not silently pin to a per-process keyring.
2. Containers: bind-mount ONE machine-scope file over `~/.codex/.credentials.json`, seeded with
   `{}` (an empty file fails to parse, oauth.rs:1121-1125), plus a machine-scope
   `mcp-oauth-locks/` dir (bind or `shared_directory` symlink). Known gap, UNVERIFIED: deleting
   the last server's entry calls `remove_file` on a bind-mount target (expect `EBUSY`), so
   logout of the only server errors.
3. macos-user / no bind mounts: a symlink is out; a hard link works only on one filesystem and
   breaks when the empty-store `remove_file` runs, so yolo would relink at launch. Or point
   `CODEX_HOME` elsewhere, which moves all codex state; not recommended.
4. Host notch: leave codex alone; on macOS the Keychain already shares across workspaces.

A real user-level-only split is (c): it needs a yolo-owned live merge/sync daemon. Its only
gain over (a) is keeping workspace servers' tokens out of other jails' reach.

## GitHub Copilot CLI

Agent: GitHub Copilot CLI (`packs/copilot`, `packs/copilot-auth`). Source read: the unpacked
release under `~/.cache/copilot/pkg/linux-x64/1.0.105/` (`cli-main.js`, and strings in
`prebuilds/linux-x64/runtime.node`), plus the 1.0.92 and 1.0.98 copies beside it, which have the
same code. No agent run except `copilot --version`. No credential read: this jail has no
`~/.copilot/mcp-oauth-config/` at all. The search ran over a strings dump of `runtime.node` (binary strings only, no credentials), kept outside the repository.

### 1. Where the tokens are stored, and whether yolo shares them

- **Keychain first, then a file directory.** The OAuth store is native Rust, reached from JS as
  `S.McpOauthStoreHandle.create(join(Es(t,"config"), "mcp-oauth-config"), sessionId, process.env.COPILOT_AGENT_SESSION_ID)`
  (`cli-main.js`, the `ofr="mcp-oauth-config"` / class `Pye` block). The native code has a
  keyring backend (strings in `runtime.node`: `vendor__keyring-core-1.0.1`,
  `vendor__zbus-secret-service-keyring-store-1.0.1`, and `src/runtime/src/mcp_base/oauth_store_files.rs`).
  GitHub's docs say `mcp-oauth-config/` "Contains MCP OAuth token, registration, and PKCE
  fallback files when keychain-backed storage is unavailable"
  (https://docs.github.com/en/copilot/reference/copilot-cli-reference/cli-config-dir-reference).
  - **In a container jail the store is the file directory.** No Secret Service is reachable:
    `DBUS_SESSION_BUS_ADDRESS` is unset here. Inferred, not observed: no MCP sign-in has
    happened in this jail. Issue #4695 also reports the file store being used on Windows even
    with Credential Manager available (https://github.com/github/copilot-cli/issues/4695).
  - **On macOS (the macos-user backend) it is probably the login Keychain**, as items named
    `copilot-mcp-oauth`, one per sign-in (#4695, a macOS commenter: "94 copilot-mcp-oauth
    Keychain items"). Not checked on a Mac: whether a macos-user agent account's keychain is
    shared across workspaces.
- **Path:** `$COPILOT_HOME/mcp-oauth-config/`, or `~/.copilot/mcp-oauth-config/` by default.
  `Es()` → `S.resolveCopilotHome(configDir ?? null, process.env.COPILOT_HOME ?? null, homedir)`
  (`cli-main.js`, `function gX`), and the legacy `--config-dir` flag outranks it. The docs say
  "`COPILOT_HOME` replaces the entire `~/.copilot` path". **No setting moves only the OAuth
  store.** #3838 also mentions a "compatibility mirror" at `~/.copilot/config/mcp-oauth-config/`
  (unverified).
- **Not shared today.** `packs/copilot/pack.toml:64-65` declares only `[[state]] at = ".copilot"`,
  with no `scope`. That is workspace scope, the same as pi's `.pi`. The pack has no
  `.copilot-shared-credentials` machine state dir and no `shared_credentials` or
  `shared_directory` hook, unlike claude (`packs/claude/pack.toml:112-120`), pi
  (`packs/pi/pack.toml:213-221`) and agy (`packs/agy/pack.toml:44-52`). So every workspace
  signs in to each MCP server separately.

### 2. Format

- **One file set per sign-in, all in one directory, kept apart from the model login.** The file
  names are `<hash>.tokens.json` (`accessToken`, `expiresAt`, `scope`, and sometimes
  `refreshToken`/`requestedScope`), `<hash>.json` (the dynamic client registration) and
  `<hash>.verifier` (the PKCE code verifier, the secret value from the login flow's
  proof-key step). The suffixes `.tokens.json` / `.verifier` / `.secret` sit together in
  `runtime.node`'s strings, and #4695 names all three files. `.secret` is probably a static
  client secret (`saveStaticClientSecret`); I inferred that and did not confirm the file name.
- **The key is the server URL plus the OAuth client id, hashed. It is not the server's name.**
  Every JS call site passes `S.mcpOauthStoreKey(server.url, server.oauthClientId)`
  (`cli-main.js`: `mOn(ut.url, c, ut.oauthClientId)` in the `/mcp` UI, and `gOn(ut.url, Qe, pt, c)`
  for a static secret). The native strings include a domain-separation label,
  `copilot-mcp-oauth-store-key-v1`, so the hash is probably SHA-256 over that label, the URL
  and the client id. I did not confirm the algorithm. I did not load the native module to
  compute one.
- **The key is not reliably stable.** #4695 shows the same server collecting many `<hash>`
  file sets ("one token, two keys"; it suspects the loopback redirect port is folded in). New
  hash names can therefore appear at any time.
- **The model login lives elsewhere:** the OS keychain under the service name `copilot-cli`,
  or else plain text in `~/.copilot/config.json`
  (https://docs.github.com/en/copilot/how-tos/copilot-cli/set-up-copilot-cli/authenticate-copilot-cli).
  yolo read-modify-writes that `config.json` (`packs/copilot/pack.toml`, `[[config]] name="config"`),
  which is why it is not a symlink candidate. It is outside this question, but note that
  copilot's GitHub login is not machine-shared either.
- **How writes happen is unverified.** #4695 saw one save path update an existing hash file in
  place while another created a new file about 6 ms later. I found no lock for the OAuth files.
  The native code has file leases only for `mcp-installations.json` and `mcp-activations.json`
  ("MCP installation storage is busy"). Either way the writes happen inside the directory, so a
  symlink on the DIRECTORY survives both in-place writes and a temp-file-and-rename save. A
  symlink on one FILE might not survive a rename.

### 3. Can yolo share only the user-level servers' sign-ins?

- **No per-server setting exists.** The store location is one directory per process, derived
  from `COPILOT_HOME`. The only per-server key inputs are `url` and `oauthClientId`, and
  changing the client id changes what the authorization server sees, so that is not a usable
  lever.
- **Linking individual files is fragile.** yolo would have to recompute the native hash (an
  undocumented algorithm), create links for files that do not exist yet, and catch the new hash
  names #4695 shows appearing. A rename-style write would also replace a file symlink with a
  real file.
- **Because the key is the URL, sharing by URL is forced.** A workspace server with the same URL
  and client id as a user-level one gets the same hash, so the two necessarily share a sign-in,
  whatever the split.
- **Copilot reads workspace MCP files that yolo never renders.** It also loads `.mcp.json` and
  `.github/mcp.json` from the project (config-dir reference page). Those servers never appear in
  yolo's composed table, so yolo cannot even list them to exclude them.
- **What an honest per-server split would take** is a yolo-owned sync: keep the workspace
  directory private, and at boot (and on exit, or with a watcher) copy in and out only the file
  sets whose `<hash>` matches a user-scope server. That requires reproducing
  `mcpOauthStoreKey`, and it still misses the unstable extra keys.

### 4. Recommendation

**(a) Share the whole `~/.copilot/mcp-oauth-config/` directory machine-wide.** The cheapest
honest mechanism already exists:

```toml
[[state]]
at = ".copilot-shared-credentials"
because = "identity/credential state shared across workspaces"
scope = "machine"

[[hook]]
at = ".copilot-shared-credentials"
from = ".copilot/mcp-oauth-config"
hook = "shared_directory"
```

- `shared_directory` symlinks a whole directory into the machine tier, using the same
  copy-then-link order as `shared_credentials` (`internal/entrypoint/packhooks.go:9-10`,
  `internal/entrypoint/sharedlink.go:438`). Linking at the directory level is robust to how
  copilot writes and to new hash names.
- **The bargain to state:** a workspace-only remote server's sign-in is shared too. The store is
  keyed by URL and has no per-server knob. The per-server alternative, (c), is a yolo-owned sync
  that depends on an undocumented native hash, which I do not recommend.
- **Open items:**
  - Possibly also share `mcp-secrets/`. It holds secret placeholders, not sign-ins.
  - Refresh races between jails are unverified. Rotating refresh tokens plus no locking could
    cost a re-sign-in, as with Claude's broker.
  - On macos-user the tokens are probably in the Keychain, so the hook changes nothing there.
    Verify on a Mac.

## pi and omp

Researched 2026-11-03 by reading source only. No agent was run, nothing made a network call, and no credential file was opened (none exists in this jail: `~/.pi/agent/mcp-auth.json` is absent, and oh-omp is not installed).

Sources:
- **pi**: stock pi 1.0.1 at `~/.npm-global/lib/node_modules/@earendil-works/pi-coding-agent` (written `$PI` below).
- **omp**: `@oh-labs/oh-omp-linux-x64@0.15.3`, the version `packs/omp/pack.toml` pins. I unpacked it from the npm cache. It is a Bun-compiled single binary, so the research ran `strings` over it; every `S:<n>` below is a line number in that strings output (reproducible with `strings` over the same package), not a source line.

---

### pi

#### 1. Where the tokens live
- The file is `<agentDir>/mcp-auth.json`. It is built as `new FileAuthStorageBackend(join(getAgentDir(), "mcp-auth.json"))` (`$PI/dist/extensions/mcp/oauth.js:10,85`) and documented as `~/.pi/agent/mcp-auth.json` (`$PI/docs/mcp.md:129`).
- It is a plain file, not the OS keychain.
- **The only thing that moves it is `PI_CODING_AGENT_DIR`**, which moves the whole agent directory. There is no per-store setting, and the per-server `oauth` object does not name a store path (`$PI/dist/core/mcp-servers.js:29-80` validates only `clientId`, `clientSecret`, `callbackPort`, `callbackUrl`, `scope`, `clientName`, `clientRegistration` and `authServerMetadataUrl`).
- The store is created with its default backend at both call sites (`$PI/dist/extensions/mcp/index.js:253`, `cli.js:140`). No extension API can inject a different one.
- **What yolo does today**:
  - The file is per-workspace. `[[state]] at = ".pi"` has no scope, so it is workspace scope (`packs/pi/pack.toml`, the `[[state]]` block).
  - `session_homes.shared` lists `mcp-auth.json`, which only links each session home back to `~/.pi/agent/mcp-auth.json` inside one workspace.
  - The `shared_credentials` hook covers `auth.json` only. MCP sign-ins are **not** shared across workspaces.

#### 2. Format
- **One JSON object for all servers.** Each value is a server's whole OAuth state: client registration, tokens, `tokensExpireAt` and the PKCE/state values (`oauth.js:79`).
- **Keyed by name plus URL**: the key is `mcp__<name with - turned into _>|<normalized URL>` (`oauth.js:75-78`; `mcpNamespace` is at `$PI/dist/core/mcp-servers.js:20-22`).
  - Older versions keyed by URL alone. The first server with that URL to load takes the old entry over (`oauth.js:92-99`).
  - Per the docs, two servers with the same URL and different names sign in separately, and the same name and URL in different `mcp.json` files share one sign-in (`docs/mcp.md:131`).
- **Not the same file as model logins**, which are in `auth.json`. It uses the same backend class.
- **Written in place, so a symlink holds.** `withLock` does `readFileSync`, then the update, then `writeFileSync(authPath, …, mode 0600)`. There is no temp-file-and-rename (`$PI/dist/core/auth-storage.js:57-75`, `:15`). `writeFileSync` follows a symlink.
- **Locking**:
  - **File writes.** `proper-lockfile.lockSync(authPath, {realpath:false})` makes `<path>.lock` *beside the link*, not beside its target (`auth-storage.js:39`). Two jails writing through two links therefore take different locks, and their read-modify-write is **not serialized**: one jail's write can drop another jail's entry.
    - SP-D10 already records this for `auth.json` across jails and across sessions in one jail (`docs/design/per-session-profiles.md:389`).
  - **Refreshes.** Each server has a lock directory, `<getAgentDir()>/mcp-auth-refresh-<sha256(key)[:16]>`, held from reading the tokens until the new ones are saved (`oauth.js:86,110-133`).
    - **Gap (my reading, unmeasured):** `getAgentDir()` is the *session* home, because yolo exports `PI_CODING_AGENT_DIR` per session. So refresh locks are per session even today: two pi sessions in one workspace can refresh the same rotating refresh token at once, and one of them loses the grant.
    - Machine-wide sharing widens this race to every jail.

#### 3. Can the store be split per server?
**Not with stock pi.**
- There is one file path, set by the agent directory. There is no per-server store setting, and the backend cannot be injected (see the first section of this report).
- Name-based tricks do not help: renaming a workspace server at render time changes its key but not its file.
- The key includes the server name, so a workspace server shares a sign-in with a user server only when both its name **and** its URL match one in another workspace. The URL alone is not enough.
- Related facts:
  - **The project's own file**: pi also reads `<project>/.pi/mcp.json` for trusted projects, and a project entry replaces a global one of the same name (`$PI/dist/extensions/mcp/config.js:1-30`; `docs/mcp.md:32`). Those servers' tokens land in the same `mcp-auth.json`.
  - **Composition**: yolo renders its composed table, all scopes merged, into `~/.pi/agent/mcp.json` (`packs/pi/derive.lua:1903-1916`). The scope is known only at yolo's render time; pi never sees it.
  - **Servers that need no OAuth state**: `"auth": {"provider": …}` on a server sends a `/login` provider's token from `auth.json`. That file is already shared machine-wide, and a project file cannot set it (`config.js:24-26`).

#### 4. Recommendation for pi
**(a) Share the whole `mcp-auth.json` machine-wide.** That is a second `[[hook]] hook = "shared_credentials"`:
- `at = ".pi-shared-credentials"`
- `from = ".pi/agent/mcp-auth.json"`

It is the same bargain the maintainer approved for `auth.json`. The base name differs, so it does not collide in that directory.

- **Honest cost, against the owner's "only user-level"**:
  - Entries for workspace-only servers are also *stored* machine-wide, so every jail can read them.
  - They only *take effect* in a jail whose config has a server with the same name and URL.
- **The alternative is (c), a yolo-owned split**, with two pieces:
  - yolo keeps a machine store holding only the entries for user-level `(name, URL)` keys, which it knows at render time.
  - A sync runs both ways between that store and each workspace's `mcp-auth.json`.

  I would not build it:
  - pi rewrites the whole file under a lock that sits beside the link.
  - Copying a *rotating* refresh token into two places means whichever copy refreshes second loses the grant.
  - A sync can only avoid that by being the single writer, which pi's design does not allow.
- **Whichever path is chosen, file the lock gap above (unverified, worth a measurement)**:
  - Refresh locks are per session home, so sessions do not serialize refreshes today.
  - A shared file widens the race to every jail.
  - The cheapest fix is upstream: put the lock beside the file's `realpath`, or honor an env var for the lock directory.

---

### omp (oh-omp 0.15.3, a fork of oh-my-pi)

#### 1. Where the tokens live
- **In SQLite: `<agentDir>/agent.db`**, through `AuthCredentialStore.open(getAgentDbPath())` (S:504414, S:519627, S:259962). This is the **same database and table, `auth_credentials`, as omp's model logins** (S:504478).
- The agent directory is `~/.oh-omp/agent`, unless one of these moves it:
  - **`PI_CODING_AGENT_DIR`**: omp reads pi's variable name (`new DirResolver(process.env.PI_CODING_AGENT_DIR)`, S:260013). An omp started from inside a yolo pi session's shell would therefore pick up *pi's* session directory (unverified side hazard).
  - **`$XDG_DATA_HOME/oh-omp`**: if that directory already exists *and* the agent directory is the default, the data files (`agent.db` and others) go there (S:259871-259900).
- It is not the OS keychain.
- The database also holds settings, model usage, cache and the memory/thread tables. `AgentStorage` (S:569778) and `openMemoryDb(getAgentDbPath())` (S:1423200) open the same file. **So `agent.db` is not a credential-only file.**
- **What yolo does today**: there is only `[[state]] at = ".oh-omp"`, which is workspace scope, with no `shared_credentials` hook (`packs/omp/pack.toml`). Neither MCP nor model sign-ins are shared across workspaces.
- **yolo renders no MCP config for omp at all**: the pack's `[[config]]` block declares only `models.yml` and `config.yml`.

#### 2. Format
- **Each MCP sign-in is one row** whose `provider` is a random id, `mcp_oauth_<ms>_<rand>`. Its data is `{type:"oauth", access, refresh, expires, …}` (S:1429376-1429382).
- **The link from server to token is held in the server's MCP config entry**: omp itself writes `auth: {type:"oauth", credentialId, tokenUrl, clientId, clientSecret}` into the file the server came from (S:1429274-1429284 for add, S:1429876-1429885 for reauth), via `updateMCPServer(found.filePath, …)`. So the keying is server → credentialId, *written into config*, and it is not derived from the name or the URL.
- **Which config files omp edits**:
  - Only the user file `<agentDir>/mcp.json` and the project file `<cwd>/.oh-omp/mcp.json` (S:259996-259999, `#findConfiguredServer` S:1429400-1429412).
  - Per its bundled docs, omp also *discovers* servers in other tools' files (`.claude/`, `.cursor/`, `.vscode/`, `mcp.json`, `.mcp.json`) (S:562709). I did not verify which user-level files. `/mcp reauth` on such a server reports "not found".
  - The config writes are atomic, temp file then rename (S:562881, bundled doc).
- **Database concurrency**: WAL mode with `busy_timeout=5000` (S:504426-504428). A refresh-lease table exists (`auth_refresh_leases`, S:504494, S:504597-504610), but **the MCP refresh path does not use it**: `#resolveAuthConfig` refreshes and then calls `authStorage.set` with no lease (S:1420322-1420340). Concurrent refreshes of a rotating token can therefore race even inside one database.

#### 3. Can the store be split per server?
**Not by any knob.** The token is a row in one database, and the binding is a `credentialId` that omp writes into a config file.
- **For user-level servers**:
  - yolo would first have to render omp's `~/.oh-omp/agent/mcp.json`, which it does not do today. omp writes `auth.credentialId` into that file, so the surface must keep keys omp adds, as pi's "not declared in full" mcp surface does.
  - The row would also have to live in a machine-shared database.
- **The only way to move `agent.db` is to move the agent directory or `$XDG_DATA_HOME/oh-omp`**, which also moves settings, memory and history.
- **Workspace servers** in `.oh-omp/mcp.json` get their own `credentialId`, so they never share by URL. Separation is natural here, but storage is not.

#### 4. Recommendation for omp
**(c) Not possible without a yolo-owned mechanism. Do not share `agent.db` machine-wide.**
- Sharing it would carry memory, settings and history along with the credentials.
- A WAL database through a symlink needs SQLite to resolve the link for its `-wal` and `-shm` files. I believe its unix VFS does, but I have not checked that for Bun's bundled SQLite.
- A WAL database across a VM file-share boundary (a macOS backend) is unsafe.

The cheapest honest options, in order:
1. **Defer.** omp has no yolo MCP rendering at all, so there are no yolo-defined user-level servers in omp to share yet.
2. **If omp gets MCP rendering, use a yolo-held token for user-level servers.** omp resolves a header value that starts with `!` by running it as a shell command (S:562881, bundled doc). For user-level servers yolo could render `headers.Authorization = "!<yolo token helper> <server>"` against a machine store that some other agent's shared sign-in fills.
   - That is a cross-agent design, worth weighing against the other agents' findings.
   - Unverified: whether a static header suppresses omp's own OAuth prompt.
3. **Upstream**: ask omp to put MCP credentials in a separate database or file, or to honor a path variable for one.

---

### Summary table

| | pi | omp |
|---|---|---|
| Store | `~/.pi/agent/mcp-auth.json` (JSON), separate from `auth.json` | `~/.oh-omp/agent/agent.db` (SQLite), same table as model logins, plus memory and settings |
| Moved by | `PI_CODING_AGENT_DIR` (whole directory) | `PI_CODING_AGENT_DIR` (whole directory), `$XDG_DATA_HOME/oh-omp` if it exists |
| Keyed by | `mcp__<name>\|<url>` | random `credentialId`, written into the server's `mcp.json` entry |
| Write style | read, modify, then `writeFileSync` in place, so a link holds; the lock sits beside the link and does not serialize across links | SQLite WAL transactions; the MCP refresh takes no lease |
| Shared by yolo today | only among one workspace's sessions | no (and no machine-scope login sharing either) |
| Per-server split | no | no (needs a yolo-owned mechanism) |
| Recommendation | (a) a `shared_credentials` hook for `mcp-auth.json`; file the refresh-lock gap | (c) defer; or render user-level servers with a `!helper` header |

## opencode, agy and muse-code

Research only. No agent was run (not even `--version`); no credential file was read (none exist in
this jail for these three). Evidence is static: strings of the installed binaries, the repo, and
vendor docs. Each claim is marked **verified** (read in code/strings) or **inferred/unverified**.

Scratch evidence (kept under this directory):
- `oc/oc.strings`: `strings` of `~/.npm-global/lib/node_modules/opencode-ai/bin/opencode.exe`
  (opencode-ai **1.19.0**; a Bun single-file binary, so the JS source is readable in the strings).
- `agy/agy.strings`: `strings` of `~/.local/bin/agy` (a Google-internal Go build, stripped, with no
  `.symtab`; `go tool objdump` refuses it, so only strings are available).
- `muse-dl/muse-bin`: Muse Code **1.4.14-R7309.1** x86_64-linux, downloaded unauthenticated from
  Meta's public channel manifest (`api.meta.ai/muse-code/channels/muse-stable` → lookaside URL).
  Its sha256 matched the manifest's checksum. It was **never executed** (chmod -x). Strings are in
  `muse-dl/muse.strings`. The binary is about 415 MB; delete it when this research closes.

### Cross-cutting finding (applies to all three, and probably to every agent)

**yolo cannot declare a remote MCP server at any scope today.** `mcp_servers.<name>` accepts only
`command`, `args`, `env`, `requires_env`, `provides` (`internal/config/config.go:186`
`knownMCPServerKeys`). There is no `url`, `headers` or `transport` key. The renders agree:
opencode's derive emits only `type = "local"` entries (`packs/opencode/derive.lua:603-615`). agy's
derive passes the composed (stdio) table straight through into a `computed` surface
(`packs/agy/derive.lua:23-28`, `packs/agy/pack.toml:27`). muse-code declares no MCP surface at all.

So every remote, OAuth-capable server an agent signs in to today was added by the user directly in
the agent's own config. yolo has no scope information about it. "Share only user-level servers"
needs `mcp_servers` to grow a remote shape first; without that, yolo has nothing to partition by.
I did not check pi/claude/codex for the same gap; the other researchers' files should confirm it.

---

### 1. opencode

#### Where it is stored (verified)
- `mcp-auth.json` is at `Global.Path.data`/`mcp-auth.json`
  (`e6=s7.join(R4.Path.data,"mcp-auth.json")` in the McpAuth module, `oc.strings` line ~245458).
- `Global.Path.data` is `$XDG_DATA_HOME/opencode`, defaulting to `~/.local/share/opencode`
  (`var W="opencode",U=H.join(V,W)… data:U` in the Global module). **The only thing that moves it
  is `XDG_DATA_HOME`.** That env var also moves `auth.json` (model logins), the session DB, `log/`
  and `repos/`. opencode has no per-file override: its `OPENCODE_*` env list has
  `OPENCODE_AUTH_CONTENT` for `auth.json` only, and nothing for mcp-auth.
- It is a plain file, not a keychain.
- **It is not shared across workspaces.** `packs/opencode/pack.toml` declares no `[[state]]` and no
  `[[hook]]` at all (confirmed by reading the whole manifest). `~/.local/share/opencode` sits under
  the per-workspace core `.local` overlay (`docs/design/agent-directory-map.md:185-186,715-731`).
  The model logins (`auth.json`) are **not** machine-shared either, unlike pi, claude, agy and muse.

#### Format (verified)
- It is one file for all servers. Schema: `Record<string, Entry>`, where `Entry =
  {tokens?:{accessToken, refreshToken?, expiresAt?, scope?}, clientInfo?:{clientId, clientSecret?,
  clientIdIssuedAt?, clientSecretExpiresAt?}, codeVerifier?, oauthState?, serverUrl?}`. These are
  key names only; I read no values.
- It is **keyed by the server NAME as written in config** (`mcpName`). Each entry also carries
  `serverUrl`, and `McpAuth.getForUrl(name, url)` returns nothing when the stored `serverUrl`
  differs from the configured URL. A same-name server with a different URL therefore reads as "not
  authenticated" instead of using the wrong token. **But it collides on write:** a sign-in to it
  overwrites the entry (`set`/`updateTokens` replace `[name]` and reset `serverUrl`). Two different
  URLs under one name take turns clobbering each other.
- It is **separate from the model credentials** (`auth.json`, keyed by provider id).
- **Write path:** every mutation runs read → modify → `FileSystem.writeJson(path, obj, 0o600)`.
  That is `writeFileString` (an in-place open+truncate+write) followed by `chmod`, with **no
  temp+rename**, so a symlink at the path survives and both calls follow it.
- **Locking:** every read and every mutate runs under `EffectFlock.withLock("mcp-auth:<path>")`.
  This is a cross-process **mkdir lock with a heartbeat and stale-breaking**, kept in
  `Global.Path.state/locks/<hash>.lock`, i.e. `~/.local/state/opencode/locks`. That directory is
  also per-workspace in a jail. **Sharing only the json therefore loses mutual exclusion between
  jails.** The lock key hashes the logical path, which is the same in every jail, so sharing
  `~/.local/state/opencode/locks` too would restore it. (inferred from the code, not exercised)

#### Can it be split per server?
- No. opencode has no per-server store, no per-server path knob, and no store directory. The
  remote config's `oauth` field (`McpOAuthConfig`) holds only clientId/clientSecret/scope/redirect,
  not a token path. Moving the file means moving the whole data root (`XDG_DATA_HOME`), which drags
  sessions and the DB along with it.
- Scope inside opencode: user-level servers live in `~/.config/opencode/opencode.json[c]`, and
  project servers in the repo's `opencode.json`. **Both write the same file under the same
  name-keyed table.** A per-server split would need a yolo-owned merge: two files, with yolo
  syncing the user-scope entries out of a per-jail file into a machine store and back. opencode
  rewrites the whole file on every refresh, so the sync would have to happen at boot and exit, or
  under a watcher, while racing opencode's own lock.

#### Recommendation: (a) share the whole store, plus the lock dir
Add a machine-scope `[[state]]` (for example `.opencode-shared-credentials`) and a
`shared_credentials` hook `from = ".local/share/opencode/mcp-auth.json"`. Add `auth.json`
alongside it, since opencode is the one agent pack whose model login is not shared yet. This is
symlink-safe (verified: in-place write). To keep opencode's own cross-process lock meaningful, also
share `.local/state/opencode/locks` through the `shared_directory` hook (it holds other opencode
locks too, which are harmless to share). Without that, the cost is a rare lost update when two
jails refresh at the same moment.

A per-server split is (c): impossible without a yolo-owned merge, and the merge is not cheap. A
name collision between a workspace server and a user server already shares or clobbers by name
today, within one workspace.

One more caveat: a `.local/share/opencode/mcp-auth.json` hook nests under the `.local` overlay.
The muse-code pack (`packs/muse-code/pack.toml:199-211`) documents that a nested `state` under a
wholesale-linked parent breaks macos-user. A file-level *hook* is what muse uses, so the same shape
should work here, but **unverified for `.local` on macos-user**.

---

### 2. agy (Google Antigravity CLI)

#### Where it is stored (partly verified)
- The token file name is **`mcp_oauth_tokens.json`** (verified: the string is present in the
  binary). The token store is a `tokenStore` type in `third_party/jetski/cortex/utils/mcp`, with
  methods `loadCredentials`, `saveCredentials`, `writeCredentials`, `findCredentials` and
  `removeToken`, plus a `persistingTokenSource` that writes refreshed tokens back (verified: symbol
  names in strings).
- The directory is **unverified**. Antigravity's docs say *"Access tokens are stored in
  `~/.gemini/antigravity/mcp_oauth_tokens.json`. Expired tokens are refreshed automatically, and
  invalid tokens are removed."* (https://antigravity.google/docs/mcp; the IDE and CLI share one
  language server per https://github.com/google-antigravity/antigravity-cli/issues/25). The binary
  also references `.gemini/antigravity/` paths. A CLI build could instead use
  `~/.gemini/antigravity-cli/`. Either way, the file is under `~/.gemini`.
- I found no env var that moves it. The strings show only `$HOME`-relative `/.gemini/...` literals.
  (unverified that none exists)
- It is a file. The docs and strings show no keychain for MCP tokens. The agy pack's own note about
  a host keyring concerns the model login.
- **It is not shared across workspaces.** `packs/agy/pack.toml:41-52` declares `.gemini` as
  workspace-scope state, and the machine-scope `.gemini-shared-credentials` hook links only
  `.gemini/antigravity-cli/antigravity-oauth-token`.

#### Format (mostly unverified)
- It is one file for all servers. The in-memory type is `map[string]*mcp.clientCredentials`
  (verified type string). The key is most likely the **server name** from `mcp_config.json`, going
  by the log strings `Failed to refresh token for %s`, `Clearing stored token for %s after failure`
  and `failed to get Google credentials for server %s`. That reading is inferred; the `%s` could be
  a URL.
- It is separate from the model login (`antigravity-oauth-token`).
- Whole rewrite versus in place, and locking: **unknown.** `writeCredentials` exists, and
  `saveCredentials`/`removeToken`/`findCredentials` each carry a `deferwrap1` (a deferred
  unlock), which suggests an **in-process mutex only**. I found no file-lock symbol. Whether the
  write is a temp+rename, which would replace a symlink with a regular file and silently un-share
  it, cannot be told from strings.

#### Can it be split per server?
- No per-server path, knob or directory was found. `mcp_config.json` supports `serverUrl` and
  `headers`, but no token-path field (vendor doc above).
- In a jail, yolo **owns `mcp_config.json` as a `computed` surface** (`packs/agy/pack.toml:27`), so
  a user cannot persist a remote server there. agy also reads workspace `.gemini/` config (the
  binary has `/path/to/workspace/.gemini/antigravity/...` strings), so workspace-defined servers
  probably exist (unverified).

#### Recommendation: (a) whole-file share, gated on two measurements
Add `shared_credentials` `from = ".gemini/antigravity/mcp_oauth_tokens.json"` (or the
`antigravity-cli` path, once measured) into the existing `.gemini-shared-credentials` dir. Before
shipping it, measure the real directory and confirm the write is not a rename. That takes one host
or jail sign-in by the owner, then `stat` and `readlink` on the path; nothing automated can do it.
A per-server split is (c), the same as opencode. It is also moot until yolo can render a remote
server into agy's computed `mcp_config.json`.

---

### 3. muse-code (Meta Muse Code)

#### Where it is stored (strongly inferred, close to verified)
- On Linux, Muse's credential backend is **`file`**. Its telemetry note reads *"off-macOS stamps
  `file`"*; on macOS it is the Keychain, with a `keychain_fallback_file` fallback, and the override
  is `TBH_CREDENTIAL_BACKEND` (verified strings).
- The file is the config root's **`auth.json`**: `$XDG_CONFIG_HOME/muse/auth.json`, defaulting to
  `~/.config/muse/auth.json` (the embedded docs name `config_dir`/`auth_path` and "settings.json,
  auth.json, and trust.json" under it; verified). `XDG_CONFIG_HOME` moves it, together with
  settings and trust.
- MCP OAuth records live **in that same `auth.json`**. The evidence: the file is
  `AuthFile{schema_version, providers}` (2 fields; shape confirmed in
  `docs/design/universal-login-sharing.md:174-176`), and the MCP record struct `McpOAuthRecordV1`
  (14 fields) starts with `mechanism, record_version, server_name, resource_url_sha256, issuer,
  client_id, registered_redirect_path, access_token, refresh_token, expires_at_unix_s, token_type,
  scopes, login_nonce…`. That is the same `mechanism`-tagged shape as the `providers.meta` login.
  The key prefixes `mcp_oauth:` and `mcp_oauth_client:` (plus `mcp-oauth-v1`) sit together in the
  strings, and no other credential file name appears in the binary. **Inferred:** entries are
  `providers["mcp_oauth:<…>"]` and `providers["mcp_oauth_client:<…>"]`. The exact key composition
  is not readable from strings.
- **So Muse's MCP sign-ins are probably ALREADY machine-shared today**, as a side effect: the pack
  links all of `~/.config/muse/auth.json` into `.muse-shared-credentials` with a
  `shared_credentials` hook (`packs/muse-code/pack.toml:212-220`).

#### Format and keying
- It is one file shared with the Meta model login. Each record carries **`server_name`** (the
  `mcpServers` entry name; *"<server> is a streamable-HTTP entry under mcpServers in
  settings.json"*) **and `resource_url_sha256`**. That points to keying or validating by both name
  and URL hash, so a workspace server with the same name and a different URL would not reuse the
  token. Inferred, not verified.
- Locking: verified by the messages `credential store lock failed:`, `credential store read
  failed:` and `credential store write failed:`. The docs say *"A sign-in done in another terminal
  is picked up without a restart"* (https://dev.meta.ai/docs/muse-code/extending), so it re-reads
  the file. Whether the write is a rename is **unknown**. The existing login hook has evidently
  survived in use, which weakly suggests it is not a rename (unverified).

#### Can it be split per server?
- No. Every record shares one file with the model login, there is no per-server path, and
  `mcpServers` is read from user `settings.json` and project config. Any split is a yolo-owned
  merge over a file that also holds the subscription login, which is the riskiest file to rewrite.
- yolo renders no MCP servers into Muse at all (the pack has no MCP surface).

#### Recommendation: (a), which is the status quo; record it and do not undo it
The whole store is already machine-wide through the login hook. There is nothing to add. What
remains is to state it in the pack comment and in the design, and to confirm it with one measured
`muse mcp login` by the owner (a `jq 'keys'` of `providers` shows the `mcp_oauth:` keys). A
per-server split is (c), and not worth it.

---

### Summary table

| Agent | Store | Keyed by | Shared machine-wide today? | Symlink-safe write | Lock | Per-server split | Recommendation |
|---|---|---|---|---|---|---|---|
| opencode | `$XDG_DATA_HOME/opencode/mcp-auth.json` (own file) | server name, URL-checked on read | **No** (no state or hook at all; `auth.json` is not shared either) | yes (in-place write + chmod; verified) | cross-process mkdir lock in `~/.local/state/opencode/locks` (per-jail) | no; needs a yolo merge | (a) hook the json (and `auth.json`), and share the locks dir |
| agy | `~/.gemini/antigravity[-cli]?/mcp_oauth_tokens.json` (dir unverified) | probably server name | No (`.gemini` is workspace state) | unknown | probably in-process only | no | (a) after measuring the path and the write mode |
| muse-code | `~/.config/muse/auth.json`, the same file as the Meta login (inferred) | server name + URL sha256 (inferred) | **Probably yes, already** (via the login hook) | unknown (the login hook works in practice) | yes ("credential store lock") | no | (a) status quo; document it and measure once |

The common blocker for "share only user-level servers": yolo's `mcp_servers` has no remote form, so
yolo neither renders nor knows the scope of any OAuth-capable server. Every remote server is
agent-config-native, and none of these three agents has a per-server token store.

