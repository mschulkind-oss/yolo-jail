---
title: "Managed MCP sign-in — source and public-document grounding"
status: accepted
tags: [research, mcp, oauth, packs]
summary: "Source-read evidence and public native-bootstrap contracts, with callback, provenance and delivery gaps explicitly unverified."
---

# Research: Managed native MCP sign-in capture, sharing, status, and recovery

**Reads with:** [Managed MCP sign-in](../design/managed-mcp-sign-in.md) (the proposed contract; this grounding is not a build hand-off).

## Summary

The current source already adopts Claude native MCP credentials into host-only canonical state, refreshes them on the host, writes refreshed access tokens back to their native source entry, and delivers shared credentials to pi through a command-backed Authorization header. It does **not** yet render that shared-header route for Claude or Codex: Claude still receives an ordinary `slack` remote entry beside an independently authenticated plugin entry, so a “dynamic client registration unsupported” error on the plain entry does not establish whether the plugin credential is missing, capture failed, or delivery is simply absent.

The right continuation is the existing accepted [sharing design](../design/mcp-auth-sharing.md) and companion plan, amended by [managed MCP sign-in](../design/managed-mcp-sign-in.md) for the managed-native-first-login requirement—not another OAuth login ceremony. Registered client metadata can come from a selected pack's genuinely supported native bundle/agent declaration; yolo cannot invent a Slack client registration, assume a public client ID licenses arbitrary agents to impersonate that product, or initiate authorization under an adopted client identity contrary to MA-D18.

## Evidence boundary and method

**Source/public-document grounding, checked 2026-11-17**, against supplied baseline `a97a0e055f20de4524063b70729595ea8794bef8`. HEAD and cleanliness were supplied for the source-read round, not independently verified by that reader. Named source files, tests, the sharing design/plan, existing store research and official public documentation were read. No Go commands, agents, installers or authenticated requests support this packet; no real credential/token store was opened.

**Labels:** “direct evidence” means observed source/public text; “interpretation” means a consequence of that source, not native behavioral proof; “engineering investigation” means unsettled work, not an owner decision or a chosen implementation. Tests below were inspected, not executed. Native macOS/rootless/provider witnesses remain separate.

## Findings

### 1. Preserve the settled contract and repair status drift first

**Claim:** The accepted design already owns scope, adoption, refresh ownership, delivery, and fallback; source is ahead of several shipped prose claims. **Sources:** `docs/design/mcp-auth-sharing.md`, `docs/design/mcp-auth-sharing-plan.md`, `packs/mcp-auth/README.md`, `internal/cli/mcpauth.go`. **Support:** direct evidence. **Confidence:** high.

Carry these exact settled words into the designer's context:

- P3: **“Core knows stores, never agents.”** A pack declares the store; core implements named lock/file mechanisms.
- MA-D18: **“yolo never starts a sign-in under an adopted client id, nor renders one into an agent's config.”**
- [OQ-MA1](../design/mcp-auth-sharing.md#decision-ledger) owner: **“don't carry a refresh token if that's possible.”** Copies are access-token-only unless an actual measured agent incompatibility establishes the exception.
- [OQ-MA4](../design/mcp-auth-sharing.md#decision-ledger) owner: **“Sign out everywhere, but only from a store that parsed cleanly.”**
- [OQ-MA8](../design/mcp-auth-sharing.md#decision-ledger) owner: **“It signs in itself, with the line: she asked for the server in every agent, and one extra sign-in is a smaller loss than a missing server.”** This is a capability fallback, not proof every agent can bootstrap Slack.
- [OQ-MA9](../design/mcp-auth-sharing.md#OQ-MA9) / MA-D27: source agents continue receiving the configured server beside their native entry; **“the header route (phase D) replaces the plain entry; no agent is withheld the server.”** Owner chose B, against the document's leaning.
- [OQ-MA10](../design/mcp-auth-sharing.md#OQ-MA10) / MA-D28: Linux `yolo host` adopts/writes back by default, without another opt-in; Mac host Keychain support remains a separate design. Owner: **“C. and put a design doc on the roadmap for mac keychain stuff.”**
- [OQ-MA7](../design/mcp-auth-sharing.md#decision-ledger): no published yolo client metadata document for now; a public client is a lasting commitment.
- [OQ-MA2](../design/mcp-auth-sharing.md#decision-ledger) covers MCP-bearing bundles yolo delivers through selected packs, not arbitrary externally installed bundle servers. A declared `sign_in=once` remote URL may nevertheless adopt a matching plugin-native entry through [§9.3](../design/mcp-auth-sharing.md#93-adopting-a-sign-in-an-agent-already-has); distinguish the configured server's authority from wholesale promotion of unknown plugin servers.

The design introduction still says **“The header route is not built.”** The plan's ID-22–24 says it is built for pi, matching observed code. `packs/mcp-auth/README.md` still calls the implementation detached/not integrated, says it does no OAuth refresh, says **“No `yolo mcp-auth` command exists yet,”** and excludes host/macOS modes now represented in source. These are stale factual statements, not rulings to reimplement. Tasks 13–15 chiefly describe the broader per-agent/Codex sharing tier; do not claim they are completed merely because Claude's one-sign-in tier has a poll/refresher.

### 2. Scope identity is proven from composition, not inferred from a store key

**Claim:** Shared-server authority comes from frozen effective versus user-only composition and same-name sign-in identity. **Sources:** `internal/packload/mcpscope.go`; `internal/cli/run/mcpauthshare.go`; plan Phase A boundary and ID-3. **Support:** direct evidence. **Confidence:** high.

`MCPServerScopes` compares remote URL, transport (default `http`), headers (default empty map), and OAuth client ID (default empty string). Same name and identity gives user scope; differing URL/headers/client ID or a server absent from user-only composition gives workspace scope. `requires_env`/`provides` are not the identity, although they affect eligibility. Removals have no scope.

`produceMCPAuthShare` returns an empty replica list outside Linux podman or when `MCPProven` is false. It loads the selected staged pack tree, not a global agent registry. `mcpShareFromPacks` admits JSON/flock replica declarations only, skips conditional `requires_env`/`provides` entries because the frozen input cannot prove per-agent eligibility, filters transport by the pack declaration, and calculates keys with pack Lua. Canonical identity is SHA-256 of sorted JSON `{url,transport,headers,client_id}` with HTML escaping disabled and no trailing newline.

`onceServersOf` additionally requires user scope, a remote URL, and effective `sign_in == config.MCPSignInOnce`. It builds `{Name, Identity, URL}`. Header-only launches can carry this list even without an adoption source, so a participating consumer can use a record adopted elsewhere. That is not permission to adopt without a source or to serve a workspace-redefined identity.

**Investigation:** the one-sign-in `OnceServer` data model carries neither expected issuer/account nor explicit client ID/header fields; the digest fences composition but adoption candidate matching first uses URL alone. The later issuer check is real, but current source does not demonstrate an explicit configured-client-ID-over-adoption fallback or account selection policy. Do not promise those from the richer design prose.

### 3. Pack declarations, native stores, and actual delivery surfaces

**Claim:** Current verified store support is much narrower than the design's agent survey. **Sources:** `packs/claude/pack.toml`, `packs/codex/pack.toml`, `packs/pi/pack.toml`, `packs/opencode/pack.toml`, `packs/copilot/pack.toml`; `internal/packdecl/mcpauth.go`; `internal/agentcfg/luahook/mcpauth.go`. **Support:** direct evidence for the inspected declarations; interpretation for capability limits. **Confidence:** high.

- **Claude:** needs `claude-auth`, `wire-bridge`, and `mcp-auth`. Its `[[mcp_auth]]` declares `.claude/.credentials.json`, JSON entries at `["mcpOAuth"]`, companion `["mcpOAuthClientConfig"]`, empty-store `{}`, proper-lockfile `.claude/.storage-write.lock`, stale 15 seconds, fields `accessToken`, `refreshToken`, `expiresAt` milliseconds, `clientId`, and login hint **“run /mcp in claude and authenticate {name}”**. It declares HTTP/SSE remote rendering and machine-scope `.claude-shared-credentials` plus `shared_credentials` hook.
- **Codex:** needs `codex-auth` and `mcp-auth`. MCP-only `.codex/.credentials.json`, root entry map, delete-empty behavior, flock `.codex/mcp-oauth-locks/file-store.lock`, snake_case token fields and millisecond expiry, `rereads_before_refresh=true`, hint `codex mcp login {name}`, HTTP only. Key Lua hashes `{type="http",url,headers={}}` and strips `local:` from the name. Native match returns name and `server_url`, **not issuer or header provenance**.
- **pi:** inspected full manifest declares HTTP-only remote rendering and session-home `mcp-auth.json` sharing within its agent/session home. Its current shared-header consumer needs no native MCP-store adapter: it writes no OAuth state for the one-sign-in server. Its pack does not independently need `mcp-auth`; a pi-only jail needs the service selected through another pack or explicitly.
- **opencode:** declares HTTP/SSE remote rendering; no corresponding cross-agent store delivery is demonstrated in this packet. Its full surveyed manifest and derive require follow-up before calling the design's proposed store route shipped.
- **Copilot:** the inspected complete short manifest has neither remote-transport declaration nor MCP-auth declaration; needs only its model-auth pack. A generic remote form is withheld from derives lacking transport support. Do not confuse the model-login Keychain declaration with MCP OAuth capture.

The existing research `docs/research/mcp-auth-stores.md` surveys older versions of pi, opencode, Copilot, agy, Muse, and omp, including unverified formats/locks. Those are investigation pointers, not verified current adapters. In particular the plan explicitly preserves unknown Codex native entries without adopting them: empty native headers in a key cannot prove the user-approved identity. Fixing managed first-login must not reopen that provenance hole.

`MCPAuth` schema currently has store/format/entries/companions/lock/token fields, reread capability, login hint, and unsupported reason. It does **not** yet have a native bootstrap client/bundle declaration, supported refresh-auth method, account identity, or delivery-route declaration. Lua `MCPAuthMatch` returns string name/URL and optional issuer. Lua derive is a pure renderer, not a network/browser side-effect executor.

### 4. Managed first-login writes and capture/adoption call chain

**Claim:** There is a real passive native capture path; it adopts only after successfully redeeming the source refresh token. **Sources:** `internal/cli/run/claudesecurestorage.go`, `mcpauthshare.go`, `mcpauthboundary.go`; `internal/cli/host.go`, `hostmcponce.go`; `internal/mcpauthdaemon/main.go`, `once.go`; `internal/mcpauth/once.go`, `source.go`. **Support:** direct evidence. **Confidence:** high for source behavior; native agent behavior remains unmeasured here.

For a bridged jail, `claudeSharedCredentialDir` resolves the selected pack's machine-scope hook. `claudeSecureStorageDir` returns the backend's absolute shared directory unless the credential view is active. Container argv receives `CLAUDE_SECURESTORAGE_CONFIG_DIR`, so native Claude writes directly to a regular credential file rather than through the hook symlink. `onceShare` qualifies a source only when the bridged directory exactly matches the selected pack's declared store hook; `onceShareFrom` currently accepts JSON plus proper-lockfile with store/lock in the same directory.

Linux podman daemon `Main` loads pinned selected-pack scripts, completes replica adoption/seed before binding readiness, then `serve` starts `runOnce` alongside the framed endpoint. `runOnce` makes an immediate pass, then passes every 30 seconds, after readiness so OAuth latency does not hold startup. Shutdown cancels and waits for the poll before final harvest.

Linux host `host.go` calls `hostOnceSignIn` after credential-view environment composition. `hostmcponce.go` rejects in-jail use, chooses the launched program's owning pack, refuses the alternate secure-storage environment with a next step, and builds `HostOnceSession` over **that program's own** store. It stays resident instead of exec'ing, starts the poll around the child program, and defers its stop/last pass. Launching pi does not silently adopt from an unrelated host Claude store through this session path.

`OnceBroker.Pass → passServer → candidate → adopt` reads source entries, considers matching URLs with a nonempty unspent/unrefused refresh token, and chooses the latest expiry (key tie-break). Adoption takes the per-record refresh lock, rereads source/canonical state, discovers metadata from the configured URL, requires a nonempty matching issuer, refreshes as the native entry's `clientId`, then commits generation 1 or increments an existing generation. Only after canonical commit does `writeBack` remove the native refresh token and replace access token/expiry at the original `SourceKey`.

**Important limits:** source `Read` is an unlocked atomic-rename snapshot, not a read under the agent lock; writes take that lock. Adoption's network refresh is serialized among brokers but does not hold the source lock across native-agent token redemption. This packet establishes broker-vs-broker exclusion, **not** proof a still-running native agent cannot use an in-memory refresh token during takeover. Failed canonical persistence after successful OAuth rotation requires a new native sign-in; the code's error explicitly says so.

### 5. Refresh, concurrency, cache, sign-out, and revocation

**Claim:** The one-sign-in tier has host refresh and conservative store mutation, but several recovery states collapse and some broader design lifecycle behavior is still unverified. **Sources:** `internal/mcpauth/once.go`, `refresh.go`, `store.go`, `source.go`; `internal/mcpauthdaemon/header.go`. **Support:** direct evidence; interpretation for gaps. **Confidence:** high.

Refresh threshold is `max(lifetime/4, 10 minutes)`, with one-hour default lifetime when response expiry is absent. The daemon poll interval is 30 seconds. Transient failures use 30 seconds, 1 minute, 2 minutes, then 5 minutes repeatedly. Definitively refused source tokens are remembered by SHA-256 in memory. Each canonical record has a separate refresh flock; the record is reread after acquiring it so another broker's successful refresh is not repeated. Discovery's issuer/token endpoint are cached in the record and reused for subsequent refresh.

`TokenClient` uses a 30-second HTTP timeout and never follows redirects. Metadata discovery tries path-aware protected-resource metadata, then origin metadata, takes the first advertised authorization server or server origin fallback, then authorization-server/OpenID metadata with issuer equality. URLs must be HTTPS or loopback HTTP. Refresh POST contains `grant_type`, refresh token, optional client ID, and resource URL. This implementation is public-client refresh: it supplies no client secret, no companion-secret extraction, and no confidential-client auth method. DPoP is not accepted as a bearer delivery route.

`SourceStore.Update` opens beneath a safe root, waits at most its configured lock budget, never writes without the agent's proper-lockfile directory, preserves unrelated document keys, writes an exclusive 0600 temporary file, fsyncs, and renames. Absent/malformed/unreadable source is never rewritten. `passServer` currently ignores an unparsed source and returns nil, losing the diagnostic distinction between no native write and unreadable/torn state.

A successful refresh writes access/expiry back only when the source entry still appears managed (no refresh token, or the spent token adoption took). Failure to write back logs a line and is retried later rather than failing canonical commit. Definitive refresh failure marks canonical dead and clears top-level tokens, deleting the source entry only if its access token still matches. Clean disappearance signs out everywhere only from the recorded origin directory; another store that never held the entry cannot sign the user out.

**Investigations:** account/team/grant scoping is not in the matching API; “latest expiry” is not an explicit account choice. Check old record `Entry` retention on dead state, durable refusal/backoff semantics across process restart, and turning off `once`/remote revocation endpoints: the observed CLI only builds `header`, and this packet found no shipped logout/revoke call path. Do not claim design [§9.2](../design/mcp-auth-sharing.md#92-the-opt-in)'s automatic server revocation is implemented. Header requests construct fresh `OnceBroker` instances, so their transient backoff/refusal maps do not persist across requests even though record locks do.

### 6. Delivery failure is distinct from capture failure—especially Claude

**Claim:** Claude's plain rendered server does not consume the shared header route, while pi's does. **Sources:** `packs/claude/derive.lua` (MCP key/match and config derive around lines 733–775), `packs/pi/derive.lua` (`pi_mcp_servers`, around 1920–1980), `packs/codex/derive.lua` (around 390–450), `internal/agentcfg/luahook/derive.go`. **Support:** direct evidence. **Confidence:** high.

The common derive boundary filters eligible transports/capabilities, constructs `ctx.mcp_one_sign_in` **before** stripping `sign_in` from `ctx.mcp_servers`, and passes `{header_command = "yolo mcp-auth header <quoted name>"}` when the route is available. It does not rewrite any vendor syntax itself.

Claude config currently builds `{type,url,headers?,oauth?}`. Only explicit `oauth.client_id`/`callback_port` become `clientId`/`callbackPort`; no one-sign-in branch, no headersHelper. Its store key contains the native server name plus hash, so `plugin:slack:slack` and plain `slack` have different entries. **Interpretation:** an adopted, working plugin entry can coexist with an unauthenticated plain entry trying unsupported dynamic registration. Advice to obtain a new static ID or sign in again is premature until source/canonical/delivery state is distinguishable.

Pi uses `ctx.mcp_one_sign_in`, removes every case variant of existing Authorization, preserves other headers, sets `Authorization = "!" .. header_command`, and omits OAuth. Native pi then need not sign in or persist this server's credential. Codex still emits URL/http_headers/scopes/static oauth without a helper; the plan cites an older installed version lacking helper support as its blocker. Source proves non-rendering, not what the owner's currently installed Codex supports.

`yolo mcp-auth header` is intentionally credential-bearing: stdout is only `Bearer <access token>`. Jail calls use this launch's loopback-TLS endpoint and framed `op=header`; host calls compose user scope and read canonical state themselves. Only listed one-sign-in identities are served. Under five minutes remaining it attempts host refresh; valid tokens survive transient refresh failure, expired/dead/missing tokens are refused. Each issuance is audited without token content. **Never use this verb as a diagnostic status probe.**

### 7. Slack registration and native bootstrap metadata

**Claim:** Slack requires an existing registered app identity; its documented native Claude plugin provides one, but generic add-URL examples are insufficient evidence that arbitrary agents can register. **Sources:** [Slack overview](https://docs.slack.dev/ai/slack-mcp-server), [harness guide](https://docs.slack.dev/ai/slack-mcp-server/connect-to-harnesses/), [native plugin manifest](https://raw.githubusercontent.com/slackapi/slack-skills-plugin/main/.mcp.json). **Support:** direct evidence. **Confidence:** high.

Official Slack wording: **“We do not support SSE-based connections or Dynamic Client Registration at this time.”** Also: **“MCP clients must be backed by a registered Slack app with a fixed app ID and hardcode that app ID.”** Marketplace/internal app approval, scopes, and IP restrictions continue to apply. Its plugin manifest directly supplies HTTP URL `https://mcp.slack.com/mcp`, public OAuth client ID `1601185624273.8899143856786`, and callback port 3118. This public identifier is metadata, not a secret.

Slack's overview discusses confidential OAuth/client secret, but [PKCE documentation](https://docs.slack.dev/authentication/using-pkce) explicitly exempts qualifying desktop public-client refresh from client-secret requirements and shows refresh using client ID alone. It also documents 30-day PKCE refresh-token expiry. [Rotation documentation](https://docs.slack.dev/authentication/using-token-rotation) says access tokens last 12 hours, refresh tokens are single-use with a short grace period, and repeated refresh can revoke older access tokens beyond the two-active-token limit. Do not generalize public-client refresh to confidential clients or promise indefinite Slack login.

**Interpretation/proposed direction, not shipped behavior:** add supported native bootstrap metadata/capability at pack/bundle boundaries: registered identity provenance, native bundle/config delivery, callback requirements, known refresh authentication method, and supported shared-delivery route. Prefer delivering/declaring the actual supported native Slack integration within the managed agent, using its ordinary `/mcp` browser sign-in, over synthesizing a client ID in core. Core may validate the declaration, classify metadata/route support, and render via pack Lua. It should not have `if server==slack` or `if agent==claude` OAuth bootstrap switches. Reusing a native supported bundle is different from placing its partner client ID into unrelated agents or starting yolo's own flow under it; the latter conflicts with MA-D18 and requires genuine provider/client support, not inference from a public string.

### 8. Browser callback and backend boundaries are not yet settled by this evidence

**Claim:** Host refresh reachability does not prove browser-to-native callback reachability. **Sources:** native plugin manifest; `internal/cli/run/mcpauthboundary.go`, `network.go`, `hostloopback.go`; the sharing design's [§9.5](../design/mcp-auth-sharing.md#95-signing-in-once), [§10](../design/mcp-auth-sharing.md#10-parity-every-backend-and-notch) and [Mac checks](../design/mcp-auth-sharing.md#mac-checks). **Support:** direct evidence plus explicit missing evidence. **Confidence:** high for the distinction, incomplete for native routing.

The existing design explicitly leaves native `/mcp`/`mcp login` inside the jail. Its host-loopback browser listener belongs to the proposed **fallback broker login**, not to the current native first-login path. I did not locate a production MCP-native callback-port publication/browser-open call chain with available named-file reads. `network.go` inspected here forwards jail traffic to host ports with socat; `hostloopback.go` solves jail-to-host loopback service access. Neither is evidence that a browser redirect to host localhost:3118 reaches a native listener bound inside the jail. This is a required engineering investigation before claiming managed first-login completes.

Apple Container and podman-on-Mac run adoption/refresh only at start and confirmed-gone exit boundaries; native cross-VM lock behavior is unmeasured. macos-user runs the poll for the session, but whether native Claude writes the declared file rather than Keychain and whether the host can update the account home need Mac witnesses. Mac host adoption deliberately remains unsupported. A manually invoked host header can still serve an existing canonical record without a Mac source; source supports that narrower statement, not automatic Mac host capture/render parity.

Real rootless-host tests must observe the actual networking backend/rootless fact. Nested podman forces host networking/user namespaces and cannot establish browser callback publication, jail-to-host reachability, or rootless-only behavior. Native provider tests must also establish source-agent behavior after refresh-token removal and helper reconnect semantics. No such probes were performed here.

### 9. Token-free status exists as an internal endpoint, not a human CLI

**Claim:** A safe inspection path can reuse parts of status, but must avoid existing effectful constructors and expose absent delivery independently. **Sources:** `internal/cli/mcpauth.go`, `mcpauth_test.go`; daemon `main.go` status handler; `once.go` `onceStatus`; `internal/mcpauth/store.go`. **Support:** direct evidence and proposed requirements. **Confidence:** high.

`mcpAuthMain` accepts exactly `header SERVER`; `TestMCPAuthHeaderThroughMainPrintsOnlyTheHeaderValue` explicitly expects `status` to fail with exit 2. The daemon supports `op=status` for the host-asserted matching jail and returns `schema=yolo.mcp-auth.status/1`, agent rows, and once rows, with no tokens. Replica rows are `launch-boundary`. Once rows include state, generation, expiry, adopted_from, and login hint; **they are empty when this share has no source**, even if it serves shared headers from a canonical record. That omission makes a header-only pi launch hard to explain.

`CanonicalStore.ReadStatus` is strict read-only: requires an existing safe 0600 lock, never creates/repairs it, reads under the lock, and refuses unsafe canonical mode. But `NewCanonicalStore` does `MkdirAll`, and `onceStatus` calls `onceStore → NewOnceStore → NewCanonicalStore` before `ReadStatus`. Therefore “status never creates state” is not established for the full production handler even though the strict reader itself is side-effect-free. Do not reuse `HostHeader`, normal `Read`, adoption, or self-check constructor paths for a no-network/no-refresh/no-write inspection command.

**Proposed observable state contract:** separate configured scope/identity, native bootstrap readiness, callback route support, source capability/store observation, adoption result/refusal, canonical freshness/dead state, selected refresh owner, rendered delivery route, service reachability evidence, and source writeback pending. Distinguish no source, unreadable source, native entry not captured, issuer mismatch, confidential refresh unsupported, canonical adopted but renderer plain OAuth, shared token expired, and endpoint unavailable. Preserve token-free stdout/JSON/log/audit and next-step wording. No implicit OAuth discovery, token refresh, browser opening, agent execution, or permission repair during inspection.

Recovery should name the ordinary managed agent sign-in and **which native server** to authenticate—plugin versus duplicate plain entry matters. A hint “authenticate slack” without bootstrap metadata can send the user straight back into unsupported registration. Existing audit (`adopt`, `adopt-refused`, `refresh`, `dead`, `sign-out`, `header`) helps, but does not by itself prove native browser completion or correct rendering.

## Acceptance scenarios and production-callsite seams

These are proposed acceptance requirements, not executed checks or authorization for live sign-in:

1. **Native first login, no ceremony:** a managed agent with selected supported native integration completes browser OAuth, atomically writes a synthetic native record, and the production poll adopts it; a second participating consumer receives access-only credentials without its own OAuth. Drive `Main/serve → runOnce → Pass` or real launcher callsites, not only `OnceBroker.Pass`. Existing `TestOnceLoopAdoptsClaudesSignInFromTheShare` uses shipped pack declaration/derive and injected ticks.
2. **Metadata and identity:** drive composed selected-pack/bootstrap declarations through production surface loop to renderer; assert exact native client/callback metadata, no client-ID leakage into unsupported agents, user overrides precedence, and no promotion of workspace redefinitions. `TestMCPAuthShareUsesFrozenUserScopeAndSelectedPack`, `TestMCPAuthNativeKeyCannotProveHeaderIdentity`, and conditional-withhold test are observed seams.
3. **Claude delivery regression:** the actual managed plain `slack` render becomes JSON-producing headersHelper on supported routes, retains nonauth headers, omits native OAuth, and preserves plugin/source adoption. Test through `buildDeriveCtxTable` and production entrypoint/host apply caller so deleting MCPHeaderRoute wiring fails. Official Claude docs require helper stdout JSON, not existing bare Bearer output; select explicit output representation without shell interpolation of credentials.
4. **Status has zero effects:** through `cli.Main` and daemon status, synthetic missing/unsafe state never creates a directory/lock, changes modes, calls a fake HTTP transport, starts a poll, or prints fixture token values. Cover header-only consumer with no local source and healthy canonical record. Existing CLI header test protects early dispatch/banner purity, not this new status contract.
5. **Rotation/concurrency:** two brokers, one due record and single-use synthetic refresh server produce one refresh under the record lock; test canonical failure after redemption, native refresh competition, writeback lock held/released, no refresh-token replica, and source update after another native sign-in. Existing `TestOnceAdoptsTheSignInAndTakesItsRefreshToken`, issuer-refusal test, source lock tests, and daemon poll seams are starting points, not all native guarantees.
6. **Source failure/sign-out:** clean origin-map deletion signs out; absent/malformed/unreadable file never does; unrelated source missing entry does not; dead state/reconnect supplies actionable distinct reason. Use `SourceDir` origin fencing and fake clock, not sleep-pass conditions.
7. **Host/backend callsites:** `TestHostLaunchOnLinuxKeepsTheOneSignInForTheSession` pins resident launch and stop; Mac unsupported/view tests pin refusals. `TestAppleContainerLaunchRunsTheOneSignInPassAtStart`, `TestKeeperRunsTheOneSignInPassOnceTheJailIsGone`, and `TestMacosUserSessionKeepsTheOneSignInForItsLength` exercise launch/keeper/session callsites using `onceBoundaryPass`/`onceServe` recorders. Preserve these, then add callback argument/forwarding seams and real-native witnesses separately.
8. **No-agent automation:** all fixtures synthetic, loopback only where effects are deliberately tested, no actual agent/installer/API. Native human witnesses explicitly cover registered-app approval, rootless callback/browser routing, refreshing source plugin after token removal, reconnect/401 helper rerun, Mac file/Keychain choice, account permissions, provider rejection, and revocation recovery.

## Contradictions

- Design introduction/MA-D27 Built text says header route unbuilt; plan ID-22–24 and pi/CLI source show it built for pi only. README says no CLI/no refresh/not integrated; source contradicts it.
- Slack harness guide supplies generic Codex/pi add-URL examples without a registered identity, while the same official overview prohibits dynamic registration and requires a registered app. Treat examples as incomplete bootstrap instructions, not proof the screenshot is wrong.
- Slack confidential-client guidance requires a secret; its PKCE desktop public-client guidance permits secretless refresh. These are different client classes, not permission to omit a secret universally.
- Design [§9.3](../design/mcp-auth-sharing.md#93-adopting-a-sign-in-an-agent-already-has) says read through the source under its lock and adoption when all refresh requirements are present; implementation uses unlocked rename snapshots and public-client-only redemption. Richer companion-secret/account/fallback claims need reconciliation.
- Prior research's remote-schema absence and pi-adapter setup describe historical versions. Current source has remote rendering and native pi MCP config; do not repeat historical prerequisites as current defects.

## Missing evidence and remaining engineering investigations

No real runtime token state was examined: **missing token versus delivery failure remains unresolved intentionally**. Current source establishes a plausible delivery mismatch, not an actual credential-state diagnosis.

Highest-value investigations:

1. Native first-login callback/browser-open/publication call chain on real rootless and Mac backends.
2. Pack-delivered supported native bundle/bootstrap identity with provenance and compatibility-version capabilities.
3. Actual Claude JSON-header helper contract/version plus generic route wiring.
4. Read-only status acquisition without constructors/network and state distinctions.
5. Takeover race with source-agent memory, account/team identity, confidential refresh support, turn-off/revocation lifecycle, and durable recovery after rotation/persistence failure.

 Codex helper/native-origin support remains separately blocked until measured/verified; adding client metadata does not supply missing header-origin proof.

## Sources

| Source anchor | Why kept |
| :--- | :--- |
| [Sharing design](../design/mcp-auth-sharing.md), [plan](../design/mcp-auth-sharing-plan.md) | Settled rulings and partial-shipping boundary |
| [One-sign-in broker](../../internal/mcpauth/once.go), [source store](../../internal/mcpauth/source.go), [canonical store](../../internal/mcpauth/store.go) | Adoption, refresh ownership and strict read/write boundaries |
| [Daemon](../../internal/mcpauthdaemon/), [CLI](../../internal/cli/mcpauth.go) | Production poll, header and status callsites |
| [Launch producer](../../internal/cli/run/mcpauthshare.go), [boundary passes](../../internal/cli/run/mcpauthboundary.go), [host session](../../internal/cli/hostmcponce.go) | Backend/session wiring |
| [Claude declaration](../../packs/claude/pack.toml), [derive](../../packs/claude/derive.lua), [pi derive](../../packs/pi/derive.lua), [Codex derive](../../packs/codex/derive.lua) | Declared stores and actual delivery forms |
| [Derive boundary](../../internal/agentcfg/luahook/derive.go), [scope proof](../../internal/packload/mcpscope.go) | Composition identity and generic route context |
| [Native-store research](mcp-auth-stores.md) | Historical measurements, not current capability proof |

[Sign-in point](../glossary.md#sign-in-point), [callback route](../glossary.md#callback-route) and
[source agent](../glossary.md#source-agent) use the managed sign-in proposal's definitions.

### Fast-moving — verify before building

The public plugin manifest URL follows upstream `main`, not a redistribution pin. Client metadata,
callback port, helper-version support, licence and provider refresh limits above were read on
2026-11-17; they require revalidation before packaging or native verification. This publication
performs no new fetch and makes no licence or bundle-delivery conclusion.

**Kept:** accepted sharing design/plan—settled rulings and partial-shipping boundary; source files and tests listed above—current production contracts; store research—historical native-store measurements with explicit stale/unverified fences; official Slack overview/plugin manifest/PKCE/rotation—registered identity and refresh rules; [Claude MCP documentation](https://code.claude.com/docs/en/mcp)—helper runs fresh at connection/reconnect, retries once after 401/403, JSON string-map stdout, ten-second shell timeout, configured Authorization disables OAuth fallback; [MCP authorization specification](https://modelcontextprotocol.io/specification/2025-11-25/basic/authorization)—resource audience, registration methods and OAuth flow constraints.

**Rejected/deprioritized:** generic search summaries as final evidence; third-party tutorials/forum reports; older survey claims as current capability proof; direct plugin GitHub HTML fetch (incomplete extraction, replaced with original raw public manifest). No authenticated provider probes. `source_check` retrieved official passages confirming no DCR/fixed Slack app, but its automated semantic verdict was unavailable (`unclear`); I manually inspected fetched original text and raw manifest. This is manual validation, not an automated source-check pass.

## Next steps

Revise the existing design/plan in place around managed native bootstrap/capture/delivery/status, retaining the quoted rulings and source/proposed distinctions. Before implementation, close callback routing and registration-provenance engineering gaps; implement token-free status and Claude delivery with production-callsite regression tests, then obtain explicitly separate native/rootless/Mac witnesses without claiming synthetic tests prove them.
