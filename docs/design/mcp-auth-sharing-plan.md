---
title: "Plan: the remote mcp_servers form, then one MCP sign-in per user-level server for Codex (slice 1)"
date: 2026-11-04
status: accepted
stage: DECIDED
next: "Task 1: withhold remote MCP servers at the derive boundary from every pack that declares no remote form, tests first"
depends-on: [mcp-auth-sharing.md]
tags: [plan, mcp, oauth, credentials, broker, packs, loopholes, sharing]
summary: "Build hand-off for mcp-auth-sharing.md. Phase A ships the remote mcp_servers form and scope attribution; phase B ships the mcp-auth pack, its host daemon on the host-broker framework, the canonical store, and Codex end to end on podman and Linux yolo host. Later slices are outlined and are re-planned against the tree when phase B lands."
---

# Plan: the remote `mcp_servers` form, then one MCP sign-in per user-level server for Codex (slice 1)

**Status:** 2026-11-04. Completed against the tree, written against `22a68f53c`. Phases A and B
are ready to build. Phases C to E are outlined only: phase B rewrites the files they touch, so
each is re-planned at the commit phase B lands on, before anyone builds from it.

**Design:** [`mcp-auth-sharing.md`](mcp-auth-sharing.md), every question ruled
([decision ledger](mcp-auth-sharing.md#decision-ledger)). The broker sits on the host-broker
framework of [`pack-brokers.md` §5.1](pack-brokers.md#51-the-host-broker-framework). Its readout
feeds [`access-introspection.md` §6](access-introspection.md#6-the-access-readout). Both of
those are open designs; [Coherence with the pack-broker design](#coherence-with-the-pack-broker-design)
lists what each side owes the other.

**Precedence.** The design wins on behavior. The tree wins on fact, for cosmetic drift only (a
moved line, a renamed helper with the same role): follow the tree and say so in the report. This
plan is advice and the first thing to be wrong; never twist the code to match it. **Stop and
report, do not commit,** when:

- a test does not fail the way its task predicts;
- a file the task names is dirty with someone else's work;
- a symbol the task names is gone;
- the fix needs a file outside the task's fence;
- a gate fails outside the task's scope.

**Terms used here.** Every term below is the design's
([§2](mcp-auth-sharing.md#2-vocabulary)) unless marked:

- *Sign-in*, *agent store*, *entry*, *canonical store*, *replica*, *user-level server*,
  *workspace server*, *unknown server*, *sign-in identity*: as defined there.
- *Host-broker framework*: as [`pack-brokers.md` §2](pack-brokers.md#2-terms) defines it.
- **Remote form** *(coined here)*: an `mcp_servers` entry with `url` instead of `command`.
- **Share file** *(coined here)*: the per-launch file the launch writes for the broker, naming
  each replica of this jail and the shared servers in it ([ID-4](#id-4)).
- **Identity digest** *(coined here)*: the hex SHA-256 of a server's sign-in identity in
  canonical JSON ([ID-3](#id-3)).
- **Liveness lock** *(coined here)*: the `flock` a running broker holds on its own share file's
  lock, which tells the other brokers its replicas are live ([ID-5](#id-5)).
- **Canonical JSON** *(coined here)*: object keys sorted, no whitespace, `<`, `>` and `&`
  unescaped, non-ASCII written as UTF-8. This is what `serde_json` writes for a `BTreeMap`, and
  so what Codex hashes into its store key ([ID-2](#id-2)).

**Gates every task uses.**

- `just format`, then the task's targeted `go test` lines.
- Before landing a commit that touches `cmd/` or `internal/`: `just check-ci`, plus the
  integration tests the task names. For a launch-path change, also run the nested-jail check
  from [AGENTS.md workflow step 2](../../AGENTS.md#workflow): `just build-dev-bundle`, then run
  the emitted launcher by absolute path from a throwaway workspace.
- In this jail, run each integration test with `YOLO_NO_AUTO_IMAGE_REAP=1` set and
  `YOLO_ALLOW_MISSING_PROGRAMS` unset.
- `git add` every new file before an image build: nix sees tracked files only.
- End of task: `just done`, green.

## Map

| Path | Change |
| :--- | :--- |
| `internal/agentcfg/luahook/derive.go` | `DeriveCtx.RemoteMCPTransports`; the remote filter at the ctx boundary; `WithheldRemoteMCPServers`; `DeriveCtx.MCPSignInStore` (task 15) |
| `internal/agentcfg/luahook/mcpauth.go` | new: `yolo.sha256`, `yolo.canonical_json`, `yolo.mcp_auth`; `MCPAuthKeys`, `MCPAuthMatch` |
| `internal/agentcfg/luahook/shippedpacks_test.go` | runs every shipped `mcp_auth` key function too |
| `internal/packdecl/packdecl.go`, `toml.go`, `kinds.go`, `mcpauth.go` (new) | the `mcp_remote_transports` pack key; the `mcp-auth` kind; `mcpEntryKeys` grows the remote keys |
| `internal/packdecl/parity_test.go`, `toml_test.go` | root-key list; grouped-kind coverage |
| `internal/setupcensus/`, `userguide/reference/settings-per-setup.md` | the `mcp-auth` kind's cells |
| `packs/releasedecode_test.go` | a `knownReleaseBreaks` row only if the release test asks for one |
| `internal/config/config.go`, `validate.go`, `schema.go` | the remote form's keys and checks |
| `internal/cli/config_ref.txt` | the `mcp_servers` section rewritten |
| `internal/config/brokerfence.go`, `mountsrequestbind.go` | the fence covers every selected host-daemon loophole's state directory |
| `internal/packload/mcpcompose.go`, `mcpscope.go` (new) | `MCPServerSources` treats `off` as removal; `MCPServerScopes` |
| `internal/cli/configwhy.go` | names a server's scope |
| `internal/entrypoint/prism.go`, `surfaceloop.go`, `mcp.go` | collect and print the remote-form notice once per server |
| `internal/entrypoint/packsurfaces.go`, `hostrender.go`, `packload` env composition | set `RemoteMCPTransports` (and later `MCPSignInStore`) on every `DeriveCtx` they build |
| `packs/codex/derive.lua`, `packs/codex/pack.toml` | the remote form; `[[mcp_auth]]`; `needs` the `mcp-auth` pack; the file-store pin |
| `packs/claude/derive.lua`, `packs/claude/pack.toml` | the remote form |
| `packs/opencode/derive.lua`, `packs/opencode/pack.toml` | the remote form |
| `packs/mcp-auth/` | new pack: `pack.toml`, `loopholes/mcp-auth-broker/loophole.toml`, `README.md` |
| `packs/embed.go` | embed `mcp-auth/` if the embed list is explicit |
| `internal/mcpauth/` | new: records, canonical store, lock protocols, the seed and adopt planners, store adapters |
| `internal/mcpauthdaemon/` | new: the daemon (`Main`, self-check, watch, propagate, refresh, the status handler) |
| `internal/internaldaemon/internaldaemon.go` | `case "mcp-auth-broker"`; the usage line |
| `internal/loopholedecl/` | the `launch_input` host-daemon key and its `{launch_input}` token |
| `internal/cli/run/brokeredscope.go`, `loopholesruntime.go`, `mcpauthshare.go` (new) | the share-file producer; token resolution; removal with the daemon; the launch block |
| `internal/cli/run/unknownlaunchenv.go` | `YOLO_SERVICE_MCP_AUTH_BROKER_ENDPOINT` |
| `internal/brokeraudit/brokeraudit.go`, `internal/cli/audit.go` | `Event.Server`, `Event.Generation`; how `yolo audit` renders an event with no argv |
| `internal/cli/mcpauth.go` (new), `dispatch.go`, `cli.go`, `help.go`, `subhelp.go` | `yolo mcp-auth status` |
| `integration/mcp_test.go` | the remote form reaches Codex; copilot is told |
| `integration/mcpauth_test.go` | new: seed and harvest through a real Codex jail |
| `docs/reference/mcp-configuration.md`, `userguide/` MCP pages, `docs/reference/loophole-system.md` | the new form, scope, the broker |

## Reuse before you write

- **The ctx boundary filter.** `eligibleMCPServers`, `withheldMCPServers` and
  `WithheldMCPServers` in
  [`internal/agentcfg/luahook/derive.go`](../../internal/agentcfg/luahook/derive.go) are the
  exact shape the remote filter takes: a pure rule over the table and one `DeriveCtx` fact,
  applied in `buildDeriveCtxTable`, with an exported reporter that the boot's drop notice reads.
  Put the remote rule beside them and run it in the same place, before `withoutProvidesKey`.
  Read that function's comment before moving anything: `requires_env` and `provides` are
  stripped at different layers on purpose.
- **The pack-wide key precedent.** `needs` ([`internal/packdecl/needs.go`](../../internal/packdecl/needs.go))
  and `session_homes` (`sessionhome.go`) are pack-wide keys with their own decode, their own
  validation and an entry in `isGroupedKind`'s exclusion list. `mcp_remote_transports` copies that shape.
- **The one-named-entry kind.** `KindMCP` in
  [`internal/packdecl/kinds.go`](../../internal/packdecl/kinds.go) is the closest sibling to
  `mcp-auth`: copy its `footprints` row, its `[[mcp]]` decode and its tests.
- **Beneath-root file access into jail-writable state.** `paths.OpenWorkspaceStateSubdir`,
  `paths.OpenStateSubdirRoot`, `paths.ReadRegularFileBeneath` and
  `paths.WriteRegularFileBeneath` in
  [`internal/paths/statefile.go`](../../internal/paths/statefile.go). The write truncates in
  place and keeps the inode, which is also how Codex writes its store. Use these for every
  replica read and write; never `os.WriteFile`.
- **The read-modify-write over a shared agent file.** `claudeview.UpdateSharedFile` in
  [`internal/claudeview/claudeview.go`](../../internal/claudeview/claudeview.go) is the shape
  (open the root, lock, read, mutate, write beneath). Do not reuse its lock: see
  [Traps](#traps).
- **The daemon's shape.** Mirror
  [`internal/openaiauthdaemon/main.go`](../../internal/openaiauthdaemon/main.go):
  - flags parsed in `Main`, with `--self-check` the only mode that needs no `--socket`;
  - `hostservice.ServeFrontedUnix` for the jail-facing socket;
  - `hostservice.ExitStateDirRetired` when the state directory is retired;
  - the graded `OK:`/`NOTE:`/`FAIL:` self-check lines that `yolo check` reads.

  `Session.JailID` from the preamble is the host-asserted jail; `Session.JSON` and
  `Session.Exit` answer.
- **The loophole manifest.** Copy
  [`packs/claude-auth/loopholes/claude-oauth-broker/loophole.toml`](../../packs/claude-auth/loopholes/claude-oauth-broker/loophole.toml):
  `lifecycle = "spawned"`, `transport = "loopback-tls"`, a `doctor_cmd`, and a `[host_daemon]`
  with `publishes = "socket"` and `detach = true`. The `{state}` token
  (`loopholedecl.TokenState`) already expands to `loopholes.StateDirFor(name)`, which is
  `~/.local/share/yolo-jail/state/<name>`: the design's canonical-store directory.
- **The per-launch file a daemon is handed.** `writeScopeFiles`, `scopeTokenArg` and
  `removeScopeFile` in
  [`internal/cli/run/brokeredscope.go`](../../internal/cli/run/brokeredscope.go), and the spawn
  loop in `startLoopholesMatching` that removes the file when the daemon stops. The share file
  takes the same lifecycle through the same map ([ID-4](#id-4)).
- **Workspace-home path mapping per backend.** `wsStateHomeRel` in
  [`internal/cli/run/prepare.go`](../../internal/cli/run/prepare.go) maps a home-relative path
  to its place under `<ws>/.yolo/home`: the dot is stripped on podman, kept on Apple Container.
- **The integration fixture for a host broker.** `TestOpenAIAuthBrokerRoundTripsAnImportedToken`
  in [`integration/openaiauth_test.go`](../../integration/openaiauth_test.go):
  - `packHome` writes the user config;
  - `macArchivePrivateState` gives the test a private state directory;
  - `loopholes.StateDirFor` names the state path;
  - the in-jail probe compares tokens by hash, so the log never holds one;
  - `kvLine` reads the probe's output.

  The new broker test is that test's twin.
- **Audit.** `brokeraudit.Open(path, warn)` and the `Event` struct in
  [`internal/brokeraudit/brokeraudit.go`](../../internal/brokeraudit/brokeraudit.go); the log
  path is the one `yolo audit` reads (`paths.BrokerDir()` plus `audit.jsonl`).

## House style that isn't obvious

- **Every stop names its next step**
  ([happy path](../reference/happy-path-principle.md)): every launch line, refusal and
  self-check `NOTE:`/`FAIL:` in this plan ends with the command or the person that fixes it.
  `internal/cli/hintcommands_test.go` fails any message string naming a `yolo …` command that
  does not exist, so a line naming `yolo mcp-auth status` cannot land before that verb does.
- **Config errors are prefixed `config.mcp_servers.<name>`**, as `validateMCPServers` already
  writes them.
- **A derive cannot load another file** (the Lua sandbox has no `require` and no `io`), so a
  helper two derives need is a core `yolo.*` function, never a shared Lua file.
- **Comments in this repository state the why**, with the design id. Match the surrounding
  density; do not add a header block to a file that has none.

## Traps

- **An array-valued top-level manifest key is read as a contribution kind** unless
  `isGroupedKind` in [`internal/packdecl/toml.go`](../../internal/packdecl/toml.go) lists it.
  `mcp_remote_transports = ["http"]` would silently become an unknown kind
  `mcp-remote-transports`. Add it to the exclusion list in the same commit.
- **`[[mcp_auth]]` becomes kind `mcp-auth`** through `groupKind`'s underscore-to-hyphen rewrite,
  but only once `footprints` knows `mcp-auth`. Until then it decodes as an unknown kind and is
  skipped with a warning, not refused.
- **`TestEveryPackKindHasACellOnEverySetup`** (`internal/setupcensus`) and the user-guide table
  in [`settings-per-setup.md`](../../userguide/reference/settings-per-setup.md) fail on a new
  kind until it has a cell for every backend and notch. That is intended: write the cells, do
  not exempt the kind.
- **`TestTheMCPKindCarriesEveryMCPServerKey`** fails when `knownMCPServerKeys` gains a key that
  `packdecl.mcpEntryKeys` lacks. The pack `[[mcp]]` kind must take the remote keys too: design
  [§5](mcp-auth-sharing.md#5-which-servers-are-user-level) makes a selected pack's server user-level.
- **Go's JSON encoder escapes `<`, `>` and `&`, and `json.Encoder` appends a newline.**
  `serde_json` does neither. A canonical-JSON helper built on `json.Marshal` produces a
  different Codex key for any URL containing `&`. Use an `Encoder` with `SetEscapeHTML(false)`
  and trim the newline. Task 7's second test vector catches this.
- **Codex hashes `headers: {}` always**, whatever headers the server has. Its key is
  `<name>|<first 16 hex of sha256(canonical JSON of {"type":"http","url":U,"headers":{}})>`, and a
  `local:` prefix is stripped from the name first. Headers are part of yolo's sign-in identity
  ([design §2](mcp-auth-sharing.md#2-vocabulary)) but not part of Codex's key. The share file
  carries both.
- **Codex removes `.credentials.json` when its last entry goes, and an empty file fails its
  parse.** A missing store reads as empty. The broker creates the file only when it has an entry
  to write, and deletes it when its write would leave `{}` (`empty_store = "delete"`).
- **Codex's `expires_at` is milliseconds since the epoch**, `Option<u64>`, and its
  `refresh_token` is `#[serde(default)]`: an entry with no `refresh_token` key parses. The entry
  calls its URL `server_url`, not `url`. All of this is from `codex-rs/rmcp-client/src/oauth.rs`
  at tag `rust-v0.166.0`.
- **Do not reuse `claudeview.acquireStorageLock`.** When its wait runs out, it writes without
  the lock. Design [§12](mcp-auth-sharing.md#12-forbidden-behavior) forbids that: a replica
  whose lock cannot be taken is retried and never written.
- **The jail can plant a symlink anywhere under `<ws>/.yolo/home`**, including at the store, at
  the lock file, or at `mcp-oauth-locks/` itself. Open the lock file beneath the root too, with
  no-follow semantics. `os.Root.MkdirAll` (Go 1.25+) creates `mcp-oauth-locks/` beneath the root.
- **No fsnotify is vendored.** A Linux watch uses `unix.InotifyInit1` directly, as
  [`internal/nixroots/watch_linux.go`](../../internal/nixroots/watch_linux.go) does. Watch the
  store's **directory**: Codex deletes and recreates the file. Every platform keeps the 30 s
  poll as the floor. A new dependency needs `go mod vendor` committed, or the hermetic image
  build breaks while `go test` passes.
- **A new `YOLO_SERVICE_<SLUG>_ENDPOINT` must join the hand-kept list** in
  [`internal/cli/run/unknownlaunchenv.go`](../../internal/cli/run/unknownlaunchenv.go), or a
  nested launch warns about an unknown variable its user cannot unset.
- **Apple Container admits host daemons by name.** `HostServiceAdmittedOn` in
  [`loopholesruntime.go`](../../internal/cli/run/loopholesruntime.go) admits only
  `openai-auth-broker` (and the Claude broker under the credential view). Phase B does not admit
  `mcp-auth-broker`: the inert line from `backendInertReason` (`loopholeinert.go`) is what that
  backend shows, beside the launch block's own line.
- **The spawn loop names one loophole already** (`openaiauth.LoopholeName`, for a migration). Do
  not add a second name switch for this broker. The share file's producer is chosen by the
  manifest's `launch_input` key ([ID-4](#id-4)).
- **Integration tests in this jail run the image reaper through the host's nix daemon** unless
  `YOLO_NO_AUTO_IMAGE_REAP=1` is set. The jail also exports `YOLO_ALLOW_MISSING_PROGRAMS=1`,
  which fails three `internal/cli` tests and leaks into launches: unset it for the gate.
- **An official pack declares no `binaries`**
  ([OQ-BP8](../reference/pack-binaries.md#OQ-BP8)). The daemon is `yolo internal daemon
  mcp-auth-broker`, and the in-jail client is the `yolo mcp-auth` verb.

## Implementation decisions

Each decision is one-answer within the design's rulings. Each says why it holds and how to
reverse it.

- <a id="id-1"></a>**ID-1. A pack declares its remote form with the pack-wide key `mcp_remote_transports`.**
  Its value is the list of `transport` values its derive renders: `["http"]` for Codex (Codex has
  no SSE transport), `["http", "sse"]` for Claude and opencode. Core withholds, at the ctx
  boundary, every remote server whose transport is not in the list, so a derive never sees a
  server it would render wrong.
  - *Why:* the drop notice must know what was withheld without running the script
    (`WithheldMCPServers` takes only the ctx), and four shipped derives copy entries verbatim, so
    a server handed to them would land in the wrong dialect.
  - *Reverse by:* deleting the key; every derive then sees every server.
- <a id="id-2"></a>**ID-2. Lua gets three core functions.** `yolo.sha256(s)` returns lowercase
  hex. `yolo.canonical_json(v)` returns canonical JSON (see *Terms*). `yolo.mcp_auth(agent,
  { key = fn, match = fn })` registers the design's `mcp_auth_key` and `mcp_auth_match`.
  `canonical_json` writes a Lua table whose keys are exactly 1..n (n ≥ 1) as an array, and every
  other table, the empty one included, as an object: an empty JSON object is what Codex's key
  hashes, and no shipped key function needs an empty array.
  - *Why:* the functions are registered, not bare globals, because every other derive entry
    point is a `yolo.*` registration and `guardUnknownAPI` already stubs unknown members.
  - *Reverse by:* none needed; they are additive.
- <a id="id-3"></a>**ID-3. The identity digest** is `yolo.sha256` over
  `canonical_json({url, transport, headers, client_id})`, after defaults: `transport` is `"http"`,
  `headers` is `{}`, and `client_id` is `""` when there is no `oauth.client_id`. It is the second
  half of the canonical record's key
  ([design §7](mcp-auth-sharing.md#7-the-canonical-store)).
  - *Reverse by:* a store `version` bump that rekeys records.
- <a id="id-4"></a>**ID-4. The launch writes one share file per jail**, at
  `<state>/replicas/<container name>.json`, and hands it to the daemon through
  `launch_input = "mcp-auth-share"` in `[host_daemon]` and the `{launch_input}` token in its
  `cmd`. The set of producers is closed and core-owned; `mcp-auth-share` is its only member. The
  share files in `replicas/` are the design's registry
  ([§8](mcp-auth-sharing.md#8-the-broker)). The share file is written before the spawn and
  removed after the daemon exits, through the map `writeScopeFiles` fills.
  - *Why:* the launch, not the daemon, holds the composition, the backend's path mapping, and
    the Lua state that computes agent keys.
  - *Reverse by:* folding the producer into the framework's generic scope token once
    [`pack-brokers.md`](pack-brokers.md) has one (see
    [coherence](#coherence-with-the-pack-broker-design)).
- <a id="id-5"></a>**ID-5. A replica is live while its daemon holds the liveness lock.** Each
  daemon takes a `flock` on `replicas/<container name>.lock` before it seeds and holds it for life.
  - A propagating daemon writes only the replicas whose lock it cannot take, and that test is a
    positive proof: the lock is held, so the daemon is running.
  - A share file whose lock is free and whose `written_at` is more than 10 minutes old is stale,
    and the daemon that finds it removes it.
  - No daemon asks the container runtime anything.
- <a id="id-6"></a>**ID-6. Each per-jail daemon refreshes the records its own share file lists**,
  under the record's refresh lock, re-reading the record after taking it. N jails sharing one
  record therefore make one refresh per expiry window: the first daemon refreshes it, and the
  others see the new expiry and skip. This is [HD-R1](host-daemon-ownership.md#HD-R1)'s argument
  (a lock, not a singleton).
- <a id="id-7"></a>**ID-7. The daemon seeds before it binds its socket.** The spawn's readiness
  wait is the endpoint becoming connectable, so the agent cannot start before its seed is
  written. The seed is bounded by the 10 s lock wait per replica. A replica it cannot lock gets no
  seed, and the launch block says so.
- <a id="id-8"></a>**ID-8. `yolo mcp-auth status` has two readers.** On the host it reads the
  canonical store and `replicas/` directly, under the canonical lock taken shared, with no daemon.
  In a jail it asks this jail's daemon with the request `{"op":"status"}` over
  `YOLO_SERVICE_MCP_AUTH_BROKER_ENDPOINT`. Both render one object, `yolo.mcp-auth.status/1`
  ([task 14](#task-14)). That object is also the readout line
  [`access-introspection.md`](access-introspection.md#61-what-it-holds) asks for.
- <a id="id-9"></a>**ID-9. The mount fence covers every selected host-daemon loophole's state
  directory**, not only `brokered` ones. A `mounts` source that is, contains or lies inside
  `loopholes.StateDirFor(name)` is refused from workspace config and disclosed from user config,
  through `brokerMountFences`.
  - *Why:* a read-only `mounts` of `~/.local/share/yolo-jail/state` is refused by nothing today
    (the refusal set covers read-write sources only). That leaves the Claude and OpenAI brokers'
    canonical logins exposed as well, and will leave this design's canonical store exposed.
  - *Reverse by:* narrowing the fence to loopholes that declare `credential_paths`, once the
    framework has that key.
- <a id="id-10"></a>**ID-10. Audit events** carry `service = "mcp-auth"`, `event` set to one of
  `adopt`, `refresh`, `sign-out`, `dead`, `seed`, `harvest` (and `header`, in phase D), plus the
  new `server` and `generation` fields, the host-asserted jail and the workspace. They never
  carry a token, a fingerprint of a refresh token, or an entry body. `seed` and `harvest` are
  logged only when they write.
- <a id="id-11"></a>**ID-11. `transport` defaults to `"http"`; `url` must be `https://`, or
  `http://` with a loopback host** (`localhost`, `127.0.0.1`, `[::1]`). The MCP authorization
  specification requires this.

## Tasks

Phase A is design [§16](mcp-auth-sharing.md#16-what-i-would-build-in-order) item 1, and phase B
is item 2. Within each phase, a task marked *independent* can run in its own worktree beside the
others; a task marked *after N* lands after task N.

### Phase A: the remote form

<a id="task-1"></a>**Task 1: withhold remote servers from a derive with no remote form.** *Independent.*

- **Settled (do not re-open):** *"a derive that has no remote form yet leaves the server out and
  the boot names it per agent (`linear: Muse has no remote MCP form in yolo yet; it is configured
  for claude, codex, pi`)."* ([§4](mcp-auth-sharing.md#4-prerequisite-a-remote-mcp_servers-form))
- **Preconditions:** `rg -n 'func withheldMCPServers|func WithheldMCPServers' internal/agentcfg/luahook/derive.go`
  finds both functions. If not, stop and report.
- **Tests first:** add to `internal/agentcfg/luahook/derive_test.go`:

  ```go
  // A remote server reaches a derive only when its pack declares that transport (ID-1);
  // a stdio server is untouched; the reporter names exactly what was withheld, by transport.
  func TestDerive_RemoteServersReachOnlyADeclaredTransport(t *testing.T) {
  	script := `
  yolo.derive("codex", "config", function(ctx)
    local names = {}
    for name in pairs(ctx.mcp_servers or {}) do names[name] = true end
    return { seen = names }
  end)`
  	servers := map[string]any{
  		"fs":     map[string]any{"command": "mcp-fs"},
  		"linear": map[string]any{"url": "https://mcp.linear.app/mcp"},
  		"legacy": map[string]any{"url": "https://sse.example.test/sse", "transport": "sse"},
  	}
  	for _, tc := range []struct {
  		name       string
  		transports []string
  		seen       map[string]any
  		withheld   map[string]string
  	}{
  		{"no remote form", nil, map[string]any{"fs": true},
  			map[string]string{"linear": "http", "legacy": "sse"}},
  		{"http only", []string{"http"}, map[string]any{"fs": true, "linear": true},
  			map[string]string{"legacy": "sse"}},
  		{"both", []string{"http", "sse"}, map[string]any{"fs": true, "linear": true, "legacy": true}, nil},
  	} {
  		t.Run(tc.name, func(t *testing.T) {
  			ctx := &DeriveCtx{Agent: "codex", Surface: "config", RemoteMCPTransports: tc.transports,
  				Tables: map[string]map[string]any{"mcp_servers": servers}}
  			out, err := GopherLuaVM{}.Derive(script, ctx)
  			if err != nil {
  				t.Fatal(err)
  			}
  			if got := out["seen"]; !reflect.DeepEqual(got, tc.seen) {
  				t.Errorf("derive saw %v, want %v", got, tc.seen)
  			}
  			if got := WithheldRemoteMCPServers(ctx); !reflect.DeepEqual(got, tc.withheld) {
  				t.Errorf("WithheldRemoteMCPServers = %v, want %v", got, tc.withheld)
  			}
  		})
  	}
  }
  ```

  Then add the notice's pure rule to `internal/entrypoint/prism_siblings_test.go` (or a new
  `remotemcpnotice_test.go`):

  ```go
  func TestRemoteMCPNoticeNamesEachAgentWithoutAForm(t *testing.T) {
  	// agent -> server -> transport withheld; rendered: server -> agents it reached.
  	withheld := map[string]map[string]string{
  		"muse-code": {"linear": "http"}, "codex": {"legacy": "sse"},
  		"copilot": {"linear": "http", "legacy": "sse"},
  	}
  	rendered := map[string][]string{"linear": {"claude", "codex", "pi"}, "legacy": {"claude"}}
  	got := remoteMCPNotices(withheld, rendered)
  	want := []string{
  		`legacy: codex, copilot have no "sse" MCP form in yolo yet; it is configured for claude`,
  		`linear: copilot, muse-code have no remote MCP form in yolo yet; it is configured for claude, codex, pi`,
  	}
  	if !reflect.DeepEqual(got, want) {
  		t.Errorf("notices:\n got %q\nwant %q", got, want)
  	}
  	if got := remoteMCPNotices(map[string]map[string]string{"copilot": {"linear": "http"}}, nil); !reflect.DeepEqual(got,
  		[]string{`linear: copilot has no remote MCP form in yolo yet, so no agent in this jail is configured for it`}) {
  		t.Errorf("no-agent notice: %q", got)
  	}
  }
  ```

  Red: `go test ./internal/agentcfg/luahook ./internal/entrypoint -run 'RemoteServersReach|RemoteMCPNotice'`
  fails to compile (`RemoteMCPTransports`, `WithheldRemoteMCPServers`, `remoteMCPNotices`).
- **Change:**
  - In `derive.go`, add the field `RemoteMCPTransports []string` to `DeriveCtx` and the pure rule
    `withheldRemoteMCPServers`. A server is remote when it has a string `url`; its transport is
    `transport`, defaulting to `"http"`. Apply the rule in `buildDeriveCtxTable` beside
    `eligibleMCPServers` and export the reporter.
  - Wording rules: the `"sse"` wording applies when a transport other than `http` is the one
    missing. A line names the agents alphabetically; servers are sorted by name.
  - In `internal/entrypoint`, the surface loop collects per agent what
    `luahook.WithheldRemoteMCPServers` reports, and which servers each agent's rendered MCP
    tables hold. After the loop it prints `remoteMCPNotices` once, through the same writer
    `noteDroppedManagedEntries` uses. The withheld server is declared, so it must not also
    trigger the "not in config" drop notice: extend the check that skips capability-withheld
    names.
  - Nothing sets `RemoteMCPTransports` yet. That is task 2, once the key exists.
- **Gates:** `go test ./internal/agentcfg/luahook ./internal/entrypoint`; `just check-ci`.
- **Fences:** no pack file, no `internal/config`.
- **Commit:** `feat(mcp): withhold remote MCP servers from a derive with no remote form`
- **Report:** the red compile failure, the green run, and where in the surface loop the notice
  is printed.

<a id="task-2"></a>**Task 2: the remote form in config, and the pack key that admits it.** *After 1.*

- **Settled (do not re-open):** [§4](mcp-auth-sharing.md#4-prerequisite-a-remote-mcp_servers-form):
  - *"Exactly one of `command` and `url`. Both, or neither, is refused naming the key to remove.
    `args` and `env` with `url` are refused the same way; `requires_env` and `provides` apply to
    both."*
  - *"`headers` … written verbatim, `${VAR}` not interpolated."*
  - *"A client secret is never rendered into an agent's config."*

  `sign_in` and `oauth.client_secret_command` are not part of this task: they arrive with
  phase D, user-scope only ([MA-D11](mcp-auth-sharing.md#decision-ledger),
  [MA-D26](mcp-auth-sharing.md#decision-ledger)). Until then they are unknown keys.
- **Preconditions:** `rg -n 'knownMCPServerKeys *=' internal/config/config.go` shows
  `command, args, env, requires_env, provides`.
- **Tests first:** a table test in `internal/config/validate_test.go` (keep the file's existing
  test style), against `ValidateConfig` or the narrowest exported entry the file's MCP tests
  already use. Each case is a `mcp_servers` table and the exact error it must produce:

  ```go
  var remoteMCPCases = []struct{ name, toml, wantErr string }{
  	{"remote ok", `[mcp_servers.linear]
  url = "https://mcp.linear.app/mcp"
  headers = { X-Org = "acme" }
  oauth = { client_id = "c1", scopes = ["read"], callback_port = 3118 }`, ""},
  	{"loopback http ok", `[mcp_servers.dev]
  url = "http://127.0.0.1:8808/mcp"`, ""},
  	{"both", `[mcp_servers.linear]
  url = "https://mcp.linear.app/mcp"
  command = "npx"`, "config.mcp_servers.linear: has both command and url; a server is local (command) or remote (url), so remove one"},
  	{"neither", `[mcp_servers.linear]
  requires_env = ["X"]`, "config.mcp_servers.linear: needs command (a local server) or url (a remote one)"},
  	{"args with url", `[mcp_servers.linear]
  url = "https://mcp.linear.app/mcp"
  args = ["-y"]`, "config.mcp_servers.linear.args: applies only to a local server (command); remove it, or replace url with command"},
  	{"bad transport", `[mcp_servers.linear]
  url = "https://mcp.linear.app/mcp"
  transport = "ws"`, `config.mcp_servers.linear.transport: must be "http" or "sse", got "ws"`},
  	{"plain http", `[mcp_servers.linear]
  url = "http://mcp.linear.app/mcp"`, `config.mcp_servers.linear.url: must be an https:// URL (http:// only for localhost), got "http://mcp.linear.app/mcp"`},
  	{"port range", `[mcp_servers.linear]
  url = "https://mcp.linear.app/mcp"
  oauth = { callback_port = 70000 }`, "config.mcp_servers.linear.oauth.callback_port: expected a port from 1 to 65535"},
  	{"headers not strings", `[mcp_servers.linear]
  url = "https://mcp.linear.app/mcp"
  headers = { X-Org = 3 }`, "config.mcp_servers.linear.headers.X-Org: expected a string"},
  }
  ```

  Add a pack-side case to `internal/packdecl/toml_test.go`: a `[[mcp]]` entry with `url` decodes,
  and `mcp_remote_transports = ["http", "sse"]` decodes onto the manifest. A value outside the
  pair is refused: `mcp_remote_transports: "ws" is not a transport yolo renders ("http", "sse")`.

  Red: `go test ./internal/config ./internal/packdecl -run 'Remote|MCP'`.
- **Change:**
  - [`internal/config/config.go`](../../internal/config/config.go): `knownMCPServerKeys` gains
    `url`, `transport`, `headers`, `oauth`. A `knownMCPOAuthKeys` set holds `client_id`,
    `scopes`, `callback_port`.
  - [`validate.go`](../../internal/config/validate.go) `validateMCPServers`: replace the
    unconditional `command` check with the rules above.
  - [`schema.go`](../../internal/config/schema.go) `ReadableSchemaKeys`: `url` (string),
    `transport` (string), `headers` (table of strings), `oauth` (table) and its three members.
    Follow how `env` is declared.
  - `mcp_servers.*` already takes `off` as its removal spelling in the schema
    (`RemovalOffString`, accepted by `readableTypeMatches` in `readablevalidate.go`). Add a case
    proving a workspace `off` reaches `packload.ComposeMCPServers` and `MCPServerSources` as a
    removal, exactly as `null` does. If it arrives as the string `"off"` instead, fix it there in
    this task; the scope rule of task 6 depends on it.
  - [`internal/packdecl/packdecl.go`](../../internal/packdecl/packdecl.go) `mcpEntryKeys` gains
    the same keys.
  - The pack-wide key `mcp_remote_transports` gets its own decode and validation, beside
    `needs.go`, and joins `isGroupedKind`'s exclusion list and `parity_test.go`'s root-key list.
  - Set `DeriveCtx.RemoteMCPTransports` from the agent pack's declaration everywhere a
    `DeriveCtx` is built: `rg -n 'DeriveCtx\{' internal --glob '!*_test.go'` lists the sites.
    Each one must set it, or that path renders nothing remote.
  - Rewrite the `mcp_servers` section of
    [`internal/cli/config_ref.txt`](../../internal/cli/config_ref.txt). It is stale today:
    it still says `null` only, calls `command` a string, says pi is skipped, and omits agy.
- **Integration:** add to `integration/mcp_test.go`:

  ```go
  // TestRemoteMcpServerIsWithheldFromAnAgentWithNoRemoteForm: a url server never lands in an
  // agent config whose pack declares no remote form, and the boot says so by name (task 1).
  func TestRemoteMcpServerIsWithheldFromAnAgentWithNoRemoteForm(t *testing.T) {
  	requireJail(t)
  	dir := writeProjectWithPacks(t, mcpConfigWithAgents(
  		`"mcp_servers": {"linear": {"url": "https://mcp.linear.app/mcp"}}`), "copilot")
  	r := runYolo(t, dir, `grep -c mcp.linear.app /home/agent/.copilot/mcp-config.json || true`)
  	if r.rc != 0 {
  		t.Fatalf("rc %d\n%s", r.rc, r.stderr)
  	}
  	if strings.TrimSpace(r.stdout) != "0" {
  		t.Fatalf("copilot's config holds the remote server: %q", r.stdout)
  	}
  	want := "linear: copilot has no remote MCP form in yolo yet, so no agent in this jail is configured for it"
  	if !strings.Contains(r.stderr+r.stdout, want) {
  		t.Fatalf("missing the remote-form notice %q\n%s", want, r.stderr)
  	}
  }
  ```

  Copilot gets no remote form in this plan, so the test stays true through phase B.
- **Gates:** `go test ./internal/config ./internal/packdecl ./internal/packload ./internal/entrypoint`;
  `go test ./packs -run Release`; `just check-ci`;
  `go test -count=1 -run 'TestRemoteMcpServerIsWithheld|TestCustomMcpServerConfigPropagates|TestWorkspaceMcpConfigsAreIsolated' ./integration`;
  the nested-jail check.
- **Fences:** no derive.lua.
- **Commit:** `feat(config): a remote mcp_servers form (url, transport, headers, oauth)`
- **Report:** the red and green runs, the `DeriveCtx` construction sites set, and whether the
  release test asked for a `knownReleaseBreaks` row.

<a id="task-3"></a>**Task 3: Codex renders the remote form.** *After 2.*

- **Settled (do not re-open):** *"`oauth.client_id` and `callback_port` are rendered into every
  agent that has a static-client setting (… Codex `oauth.client_id` …)"*; *"A client secret is
  never rendered."* ([§4](mcp-auth-sharing.md#4-prerequisite-a-remote-mcp_servers-form)).
- **Codex's dialect** (`codex-rs/config/src/mcp_types.rs`, `rust-v0.166.0`): a streamable-HTTP
  server is `url`, plus optional `http_headers` (a string map), top-level `scopes`, and
  `oauth = { client_id, callback_port }`. There is no SSE variant.
- **Tests first:** in `internal/entrypoint/prism_codex_test.go`, a user table with
  `linear = {url, headers = {X-Org = "acme"}, oauth = {client_id = "c1", scopes = ["read"],
  callback_port = 3118}}` renders this TOML table, and `legacy` (sse) renders nothing:

  ```toml
  [mcp_servers.linear]
  url = "https://mcp.linear.app/mcp"
  http_headers = { X-Org = "acme" }
  scopes = ["read"]
  oauth = { client_id = "c1", callback_port = 3118 }
  ```

  Assert the parsed values, not the bytes, following the file's existing assertions. Red:
  `go test ./internal/entrypoint -run Codex`.
- **Change:** in [`packs/codex/pack.toml`](../../packs/codex/pack.toml), add
  `mcp_remote_transports = ["http"]`. In the `config` derive in
  [`packs/codex/derive.lua`](../../packs/codex/derive.lua), branch on `s.url` where it rebuilds
  each entry, and render the table above. `TestEveryShippedPackDeriveStillRuns` must stay green.
- **Integration:** extend task 2's test file with `TestRemoteMcpServerReachesCodex`: packs
  `codex`, the same server; in the jail, `python -c` with `tomllib` prints
  `mcp_servers.linear.url` from `~/.codex/config.toml`; assert it.
- **Gates:** `go test ./internal/entrypoint ./internal/agentcfg/luahook`; `just check-ci`;
  `go test -count=1 -run 'TestRemoteMcpServer|TestCustomMcpServerConfigPropagates' ./integration`.
- **Fences:** only Codex's two pack files and its test.
- **Commit:** `feat(codex): render remote MCP servers`
- **Report:** red, green, integration output.

<a id="task-4"></a>**Task 4: Claude renders the remote form.** *After 2; independent of 3 and 5.*

- **Settled:** as task 3. Claude's dialect in `~/.claude.json`'s `mcpServers` is
  `{ "type": "http" | "sse", "url", "headers", "oauth": { "clientId", "callbackPort" } }`
  ([§9.1](mcp-auth-sharing.md#91-what-the-protocol-allows) table). There is no scopes key; scopes
  are not rendered.
- **Tests first:** `internal/entrypoint/prism_claude_test.go`: the same `linear` renders
  `{"type":"http","url":"https://mcp.linear.app/mcp","headers":{"X-Org":"acme"},"oauth":{"clientId":"c1","callbackPort":3118}}`,
  and `legacy` renders with `"type":"sse"`. There is no `transport` key and no `scopes` key in
  the output. Red: `go test ./internal/entrypoint -run Claude`.
- **Change:** in [`packs/claude/pack.toml`](../../packs/claude/pack.toml), set
  `mcp_remote_transports = ["http", "sse"]`. The claude config derive copies entries verbatim
  today. For a `url` entry it builds the dialect above; a stdio entry keeps the verbatim copy.
- **Gates, fences, report:** as task 3, scoped to Claude's files. Integration: one case in the
  same file reading `~/.claude.json`.
- **Commit:** `feat(claude): render remote MCP servers`

<a id="task-5"></a>**Task 5: opencode renders the remote form.** *After 2; independent of 3 and 4.*

- **Settled:** as task 3. opencode's dialect is
  `{ "type": "remote", "url", "headers", "oauth": { "clientId" } }`, and opencode connects to
  SSE and streamable HTTP alike.
- **Tests first:** `internal/entrypoint/prism_opencode_test.go`, asserting that dialect for
  `linear` and for `legacy`. Red: `go test ./internal/entrypoint -run Opencode`.
- **Change:** in [`packs/opencode/pack.toml`](../../packs/opencode/pack.toml), set
  `mcp_remote_transports = ["http", "sse"]`; [`packs/opencode/derive.lua`](../../packs/opencode/derive.lua)
  branches on `s.url`.
- **Gates, fences, report:** as task 3, scoped to opencode.
- **Commit:** `feat(opencode): render remote MCP servers`

pi, Copilot and agy keep the withheld-with-notice behavior in this plan. Their remote dialects
are not read yet ([Blockers](#blockers)).

<a id="task-6"></a>**Task 6: scope attribution and `config why`.** *After 2.*

- **Settled (do not re-open):** the scope table in
  [§5](mcp-auth-sharing.md#5-which-servers-are-user-level), and
  [MA-D25](mcp-auth-sharing.md#decision-ledger):
  - a pack added with `-p` belongs to no config layer and gives workspace scope;
  - a workspace `null` or `off` removes the server here ("absent here");
  - `packs` is a user-only key today, so the row "from a pack only the workspace config selects"
    cannot occur yet.

  Scope is recomputed on every launch.
- **Tests first:** `internal/packload/mcpscope_test.go`, a table test of
  `MCPServerScopes(effective, userOnly *jsonx.OrderedMap) map[string]Scope`, where
  `Scope{Level: "user"|"workspace", Reason string}`. Cases, one per [§5](mcp-auth-sharing.md#5-which-servers-are-user-level) row that can occur:
  - user only → user;
  - workspace only → workspace;
  - both, workspace changes only `requires_env` → user;
  - both, workspace changes `url` → workspace, reason `its url differs from your user-level server`;
  - both, workspace changes `headers` → workspace;
  - workspace `null` → absent from the result;
  - workspace server with a user server's name and identity → user;
  - a different name with the same URL → workspace;
  - a stdio server → user or workspace by the same rule, with no identity comparison beyond equality of `command` and `args`.

  And `internal/cli/configwhy_test.go`: `yolo config why mcp_servers.linear` for the
  identity-differs case prints a line containing
  `scope: workspace — yolo-jail.toml changes its url, so it signs in separately from your user-level linear`.
  Red: `go test ./internal/packload ./internal/cli -run 'Scope|ConfigWhy'`.
- **Change:**
  - New `internal/packload/mcpscope.go`, pure, beside `ComposeMCPServers`.
  - The user-only table is `ComposeMCPServers(UserScopeConfig, user-selected packs, home)`; the
    pack list leaves out every pack a `-p` added.
  - The launch keeps both tables; phase B's share-file producer reads the scopes from there.
  - `configWhy` gains the scope line for an `mcp_servers.<name>` path. It calls the user-scope
    loader the launch uses, and never writes.
- **Gates:** `go test ./internal/packload ./internal/cli`; `just check-ci`.
- **Fences:** no `internal/cli/run` beyond keeping the tables.
- **Commit:** `feat(mcp): scope attribution for MCP servers, named by config why`
- **Report:** red, green, the `config why` output for one case.

### Phase B: slice 1, Codex on podman and Linux `yolo host`

<a id="task-7"></a>**Task 7: the Lua helpers and the `mcp_auth` registration.** *Independent.*

- **Settled:** [§6](mcp-auth-sharing.md#6-what-an-agent-pack-declares): *"`mcp_auth_key(server)` →
  the agent's key … `mcp_auth_match(key, entry)` → the server name and URL an existing entry
  belongs to, or nil … Core hands Lua `sha256` and a canonical JSON serializer."*
- **Tests first:** `internal/agentcfg/luahook/mcpauth_test.go`:

  ```go
  // The vectors were computed with Python's json.dumps(sort_keys=True, separators=(",", ":"),
  // ensure_ascii=False) and hashlib.sha256, the bytes serde_json writes for Codex's BTreeMap.
  func TestCodexStoreKeyVectors(t *testing.T) {
  	script := `
  yolo.mcp_auth("codex", {
    key = function(s)
      local j = yolo.canonical_json({ type = "http", url = s.url, headers = {} })
      return s.name .. "|" .. string.sub(yolo.sha256(j), 1, 16)
    end,
    match = function(key, entry)
      local name = string.match(key, "^(.*)|%x+$")
      if name == nil or type(entry.server_url) ~= "string" then return nil end
      return { name = name, url = entry.server_url }
    end,
  })`
  	for _, tc := range []struct{ name, url, want string }{
  		{"linear", "https://mcp.linear.app/mcp", "linear|9436f96375b1259b"},
  		{"odd", "https://mcp.example.test/mcp?a=1&b=<2>", "odd|8ac55be545c4b620"},
  	} {
  		got, err := MCPAuthKeys(script, "codex", []MCPServerRef{{Name: tc.name, URL: tc.url}})
  		if err != nil {
  			t.Fatal(err)
  		}
  		if got[tc.name] != tc.want {
  			t.Errorf("key(%s) = %q, want %q", tc.name, got[tc.name], tc.want)
  		}
  	}
  	m, err := MCPAuthMatch(script, "codex", "linear|9436f96375b1259b",
  		map[string]any{"server_url": "https://mcp.linear.app/mcp"})
  	if err != nil || m == nil || m.Name != "linear" || m.URL != "https://mcp.linear.app/mcp" {
  		t.Fatalf("match = %+v, %v", m, err)
  	}
  }
  ```

  An empty Lua table encodes as `{}` ([ID-2](#id-2)), which is what makes `headers = {}` hash
  as Codex's empty map. The first vector is the real Codex key for Linear's URL; the second
  catches HTML escaping. Add `yolo.sha256("")` →
  `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855`, and
  `yolo.canonical_json({b=1,a={2,3}})` → `{"a":[2,3],"b":1}`.
  Red: `go test ./internal/agentcfg/luahook -run 'CodexStoreKey'`.
- **Change:**
  - New `internal/agentcfg/luahook/mcpauth.go`: install the three functions before
    `guardUnknownAPI` reads the known members, as `installModelFor` is.
  - `MCPAuthKeys(script, agent string, servers []MCPServerRef) (map[string]string, error)` and
    `MCPAuthMatch(script, agent, key string, entry map[string]any) (*MCPServerRef, error)` run a
    pack's script in a fresh VM with no ctx sources, the same sandbox `Derive` uses.
  - `MCPServerRef{Name, URL, Transport string; Headers map[string]string; ClientID string}`.
  - Extend `TestEveryShippedPackDeriveStillRuns` so that, for every shipped pack registering
    `mcp_auth`, it runs key and match on one fixture server and checks the round trip
    `match(key(s), entry_for(s)).name == s.name`.
  - `deriveapiskew_test.go` lists known `yolo.*` members: add the three.
- **Gates:** `go test ./internal/agentcfg/luahook`; `just check-ci`.
- **Fences:** `internal/agentcfg/luahook` only.
- **Commit:** `feat(luahook): sha256, canonical_json and the mcp_auth registration`
- **Report:** red, green, the empty-object rule chosen.

<a id="task-8"></a>**Task 8: the `[[mcp_auth]]` declaration.** *Independent.*

- **Settled:** [§6](mcp-auth-sharing.md#6-what-an-agent-pack-declares)'s block, verbatim:
  - `store` (home-relative);
  - `format`: `json` | `dir` | `unsupported`;
  - `entries_at`, `companions`;
  - `empty_store`: `delete` | `{}`;
  - `lock = { protocol, path, stale_seconds }`, where `protocol` is `flock` |
    `proper-lockfile` | `mkdir-heartbeat` | `none`;
  - `fields`, with `expires_at = { path, unit }` and `unit` `ms` | `s`;
  - `rereads_before_refresh`, `login_hint` (may contain `{name}`), `unsupported_reason`.

  One declaration per pack ([MA-D4](mcp-auth-sharing.md#decision-ledger)).
- **Tests first:** `internal/packdecl/mcpauth_test.go`:
  - Codex's block decodes into `Manifest.MCPAuth`.
  - Each of these is refused with a message naming the key:
    - `store` absolute or holding `..`;
    - `format = "sqlite"`;
    - `lock.protocol = "fcntl"`;
    - `format = "unsupported"` with no `unsupported_reason`;
    - two `[[mcp_auth]]` blocks in one pack.
  - Extend `TestGroupedKindNamesCoverAllLiveKinds`.

  Red: `go test ./internal/packdecl -run 'MCPAuth|GroupedKind'`.
- **Change:**
  - New `internal/packdecl/mcpauth.go` (struct, decode, validation).
  - `kinds.go`: `KindMCPAuth Kind = "mcp-auth"`, plus a `footprints` row with `CombineExclusive`,
    claims `"where this agent keeps MCP sign-ins, for yolo's MCP sign-in broker"`, not
    review-worthy.
  - Setupcensus cells: `yes` on podman and on Linux `yolo host`; `planned (witness)` on Apple
    Container and macos-user. Use the census's own vocabulary.
  - The user-guide row.
  - Run `go test ./packs -run Release`. The release test's header says an unknown kind is
    tolerated, so expect no break. Add a `knownReleaseBreaks` row
    (`guardNoRestageOnAttach`, `pinAttachLeavesAnOlderJailsTree`) only if it reports one.
- **Gates:** `go test ./internal/packdecl ./internal/setupcensus ./packs`; `just check-ci`.
- **Fences:** no pack file yet (task 12 declares Codex's).
- **Commit:** `feat(packdecl): the mcp-auth kind, a pack's MCP sign-in store declaration`

<a id="task-9"></a>**Task 9: fence every host-daemon loophole's state directory from `mounts`.** *Independent.*

- **Settled:** [§12](mcp-auth-sharing.md#12-forbidden-behavior): *"Never mount the canonical store
  into a jail."* That is [ID-9](#id-9) and [MA-D24](mcp-auth-sharing.md#decision-ledger), and
  the framework fence of [`pack-brokers.md` §5.1](pack-brokers.md#51-the-host-broker-framework).
- **Tests first:** extend `internal/config/brokerfence_test.go` (or the file holding
  `validateBrokerMountFence`'s tests) with a fixture pack whose loophole has a `[host_daemon]`
  and no `brokered` block:
  - A workspace `mounts = ["~/.local/share/yolo-jail/state/fixture-daemon:/ctx/x"]` is refused,
    naming `fixture-daemon` and `the state of the fixture-daemon host daemon, which holds
    credentials the jail must not read`.
  - The same entry in user config is disclosed, not refused.
  - A mount of `~/.local/share/yolo-jail/state` (containing it) is refused.
  - A loophole with no `[host_daemon]` arms nothing.

  Red: `go test ./internal/config -run Fence`.
- **Change:** `brokerMountFences` in
  [`internal/config/mountsrequestbind.go`](../../internal/config/mountsrequestbind.go) adds one
  fence per known loophole with a host daemon, at `loopholes.StateDirFor(name)`. It keeps the
  `paths.BrokerDir()` fence, armed by a brokered loophole as today. Update `brokerfence.go`'s
  header comment, which says the fence is armed by `brokered` alone.
- **Gates:** `go test ./internal/config`; `just check-ci`;
  `go test -count=1 -run 'Mount' ./integration`.
- **Fences:** `internal/config` only.
- **Commit:** `fix(config): fence every host daemon's state directory from mounts`
- **Report:** red, green; and whether any existing test asserted that such a mount was allowed
  (rewrite it to the new behavior; do not relax it).

<a id="task-10"></a>**Task 10: audit events without an argv.** *Independent.*

- **Settled:** [ID-10](#id-10), [MA-D22](mcp-auth-sharing.md#decision-ledger).
- **Tests first:** `internal/brokeraudit/brokeraudit_test.go`: an `Event{Service: "mcp-auth",
  Event: "refresh", Server: "linear", Generation: 4, Outcome: "ok"}` round-trips. And
  `internal/cli/audit_test.go`: `yolo audit --service mcp-auth` renders it as one line containing
  `mcp-auth refresh linear (generation 4) ok`, with no empty argv rendering.
  Red: `go test ./internal/brokeraudit ./internal/cli -run Audit`.
- **Change:** `Event.Server string \`json:"server,omitempty"\`` and
  `Event.Generation int \`json:"generation,omitempty"\``. The audit renderer shows `server` and
  `generation` when `Argv` is empty. `--service` help names `mcp-auth` beside `github`.
- **Gates:** `go test ./internal/brokeraudit ./internal/cli`; `just check-ci`.
- **Commit:** `feat(audit): events that name a server instead of an argv`

<a id="task-11"></a>**Task 11: the canonical store, the Codex adapter, and the pure planners.** *After 8.*

- **Settled (do not re-open):**
  - [§7](mcp-auth-sharing.md#7-the-canonical-store): the store's path, mode `0600`, its key, its
    record fields, its states, and *"one `flock` on `<pack>.lock` beside it for every
    read-modify-write, and one refresh lock per record."*
  - [§8.1](mcp-auth-sharing.md#81-seed): *"An entry already there with a lower generation, or
    none, is replaced; every non-shared entry is left byte-for-byte."*
  - [§8.2](mcp-auth-sharing.md#82-adopt): adopt a differing entry with generation + 1; a
    disappeared entry in a cleanly parsed store signs out everywhere; an unparseable store is
    never "every entry gone".
  - [§8.4](mcp-auth-sharing.md#84-refresh-the-broker-is-the-single-refresher): *"A copy never
    carries a refresh token."*
  - [§15](mcp-auth-sharing.md#15-risks)'s last row (a missing file is an empty store).
- **The record, as a contract.** The file is `<state>/codex.json`:

  ```json
  {
    "version": 1,
    "records": [
      {
        "server": "linear",
        "identity": "<64-hex identity digest, ID-3>",
        "url": "https://mcp.linear.app/mcp",
        "generation": 2,
        "state": "live",
        "adopted_from": "/home/u/work/a",
        "entry": { "server_name": "linear", "server_url": "https://mcp.linear.app/mcp",
                   "client_id": "c1", "access_token": "…", "expires_at": 1767225600000,
                   "refresh_token": "…", "scopes": [] },
        "companions": {},
        "access_token": "…", "refresh_token": "…", "expires_at_ms": 1767225600000, "client_id": "c1",
        "token_endpoint": "",
        "refreshed_at": ""
      }
    ]
  }
  ```

  `entry` is the agent's native entry verbatim, refresh token included: only the canonical copy
  holds it. The flat fields are what `fields` extracted.
- **How a replica copy is identified.** A replica's copy is the record's entry with the
  `fields.refresh_token` path deleted, and nothing else changed. The replica holds no generation
  number, so the broker recognizes a replica entry as "its own write" when it equals that copy
  byte for byte under canonical JSON. Anything else under a shared key is the agent's own write.
- **Tests first:** `internal/mcpauth/plan_test.go`, pure, no files. The two planners are:
  - `PlanSeed(records []Record, replica Store, shared []SharedServer) (Store, []Event)`;
  - `PlanAdopt(records []Record, replica Store, parsedOK bool, shared []SharedServer, workspace string) (records []Record, replica Store, events []Event)`.

  Cases:
  1. **Seed into a missing store:** creates `{linear|9436f96375b1259b: <copy>}`; the copy has no
     `refresh_token` key.
  2. **Seed leaves an unshared entry** (`notion|aaaa…`) byte-for-byte, including key order.
  3. **Seed over the broker's own older copy:** replaced by the newer copy.
  4. **Adopt a new sign-in:**
     - The replica entry differs from the copy: the record takes it with generation + 1 and
       `adopted_from` set.
     - The replica's entry loses its refresh token.
     - Events: `adopt`.
  5. **Adopt a first sign-in** (no record yet): generation 1.
  6. **Sign-out:** a shared key absent from a store with `parsedOK` true removes the record and
     emits `sign-out`.
  7. **Unparseable store:** with `parsedOK` false, a missing key changes nothing and emits nothing.
  8. **Workspace server:** an entry not in `shared` is never read past its key. Feed it an entry
     whose value is not even an object; nothing fails.
  9. **Empty after sign-out:** a store whose last entry the plan removes is planned as deleted
     when `empty_store = "delete"`, and as `{}` when `"{}"`.
  10. **Dead record:** a record in state `dead` seeds nothing and removes its copy.

  And `internal/mcpauth/store_test.go`, which touches files:
  - The canonical file is created `0600`; a second writer blocks on `codex.lock`.
  - A replica write through a planted symlink at `.credentials.json` replaces the link and never
    touches its target.
  - A planted symlink at `mcp-oauth-locks` is refused, and nothing is written.
  - With the lock held by another file descriptor, a write waits 10 s, gives up, and has written
    nothing. Inject the wait duration; do not sleep 10 s in the test.

  Red: `go test ./internal/mcpauth` (the package does not exist).
- **Change:**
  - `internal/mcpauth/{record,plan,store,lock,adapter}.go`.
  - The `flock` protocol opens its lock file beneath the replica root. Add
    `paths.OpenLockFileBeneath(r, name)` beside `OpenRegularFileBeneath`: `O_RDWR|O_CREATE`, no
    truncate, the same link refusal.
  - The JSON adapter reads `entries_at` and `fields` by path.
  - `proper-lockfile` and `mkdir-heartbeat` are not implemented in this task. A store declaring
    either refuses at load with `mcp-auth: lock protocol "proper-lockfile" arrives with slice 2`,
    which nothing ships yet.
- **Gates:** `go test ./internal/mcpauth ./internal/paths`;
  `go test -race ./internal/mcpauth`; `just check-ci`.
- **Fences:** no daemon, no launch code.
- **Commit:** `feat(mcpauth): the canonical store, the json adapter, and the seed and adopt planners`
- **Report:** red, green, the race run.

<a id="task-12"></a>**Task 12: the `mcp-auth` pack, its daemon, seed at start and harvest at stop, for Codex on podman.** *After 6, 7, 9, 10, 11.*

- **Settled (do not re-open):**
  - [§8](mcp-auth-sharing.md#8-the-broker): *"Shipped as the `mcp-auth-broker` loophole of a new
    pack, `mcp-auth`, which every agent pack that declares `[[mcp_auth]]` `needs` … one per
    jail, started and stopped with the launch."*
  - [§8.1](mcp-auth-sharing.md#81-seed): the seed runs *"before the agent starts"*.
  - [§8.5](mcp-auth-sharing.md#85-launch-boundary-only): the adopt at stop.
  - [§8.6](mcp-auth-sharing.md#86-what-the-launch-prints): the launch block, verbatim.
  - [§10](mcp-auth-sharing.md#10-parity-every-backend-and-notch): *"Slice 1 ships podman and
    Linux `yolo host`; the other columns wait on their witnesses."*
  - The derive pins `mcp_oauth_credentials_store = "file"` in a container jail
    ([§6](mcp-auth-sharing.md#6-what-an-agent-pack-declares) table).
- **What the user sees on the other backends in this task:**
  - Apple Container prints `MCP sign-ins for codex are not shared on Apple Container in this
    yolo yet; each workspace signs in once (codex mcp login <name>)`.
  - macos-user prints the same line with `macos-user`.
  - The producer writes no replica for either, and the daemon there has nothing to do.
  - This is the parity exception, and the reason is the design's own: those columns wait on
    their witnesses.
- **The share file, as a contract** (`<state>/replicas/<container name>.json`, `0600`):

  ```json
  {
    "version": 1,
    "jail": "yolo-work-a-3f2c",
    "workspace": "/home/u/work/a",
    "written_at": "2026-11-04T12:00:00Z",
    "replicas": [
      {
        "pack": "codex",
        "root": { "workspace_state_subdirs": ["home", "codex"] },
        "store": ".credentials.json",
        "lock": { "protocol": "flock", "path": "mcp-oauth-locks/file-store.lock" },
        "decl": { "format": "json", "entries_at": [], "companions": [], "empty_store": "delete",
                  "fields": { "access_token": "access_token", "refresh_token": "refresh_token",
                              "expires_at": { "path": "expires_at", "unit": "ms" }, "client_id": "client_id" },
                  "login_hint": "codex mcp login {name}" },
        "shared": [ { "name": "linear", "identity": "<hex>", "url": "https://mcp.linear.app/mcp",
                      "key": "linear|9436f96375b1259b" } ]
      }
    ]
  }
  ```

  - `root` is opened from the workspace through `paths.OpenWorkspaceStateSubdir` and
    `OpenStateSubdirRoot`, one component at a time. At `yolo host` (task 15) it is `{ "dir": "<home>" }`.
  - `store` and `lock` are relative to `root`, mapped through `wsStateHomeRel` from the
    declaration's home-relative paths.
  - `match` stays in the pack's script. The daemon loads it from the staged pack tree, the
    path that `loopholes` resolution already hands a spawned loophole.
- **Tests first:**
  - Unit, `internal/cli/run/mcpauthshare_test.go`: the producer, on a fake launch with Codex
    selected and the user-level `linear`, writes the file above. Cases:
    - on Apple Container it writes `"replicas": []` and returns the not-shared line;
    - a workspace-scope server is absent from `shared` and appears in the launch block's kept
      lines.
  - Unit, `internal/mcpauthdaemon/main_test.go`: `Main` with a share file and a forged canonical
    record seeds the replica before the socket is connectable. On SIGTERM it harvests a changed
    entry into canonical with generation + 1, and writes audit events `seed`, `adopt`.
  - Integration, new `integration/mcpauth_test.go`:

    ```go
    // TestMcpAuthBrokerSeedsAndHarvestsCodex forges a canonical record for a user-level remote
    // server in a private state dir, launches a codex jail, and checks in the jail, by hash,
    // that the seeded entry carries the access token and no refresh token. The jail then
    // writes a new sign-in under Codex's own flock; after the launch returns, the canonical
    // record holds it as generation 2 and the workspace store has lost its refresh token.
    func TestMcpAuthBrokerSeedsAndHarvestsCodex(t *testing.T) {
    	requireJailExclusive(t, "TestMcpAuthBrokerSeedsAndHarvestsCodex reads its HOME's host daemons")
    	if rt := detectRuntime(); rt != "podman" || goruntime.GOOS != "linux" {
    		t.Skipf("slice 1 shares on podman on Linux; this is %q on %s", rt, goruntime.GOOS)
    	}
    	dir := writeProject(t, `{}`)
    	packHome(t, `{"packs": ["codex", "codex-auth"],
    	  "mcp_servers": {"linear": {"url": "https://mcp.linear.app/mcp"}}}`)
    	macArchivePrivateState(t)
    	state := loopholes.StateDirFor("mcp-auth-broker")
    	forgeMCPRecord(t, filepath.Join(state, "codex.json"), "linear", "https://mcp.linear.app/mcp",
    		"forged-access-1", "forged-refresh-1", 1)
    	script := strings.Join([]string{
    		`python3 - <<'PY'`,
    		`import json, hashlib, fcntl, os`,
    		`p = os.path.expanduser('~/.codex/.credentials.json')`,
    		`e = json.load(open(p))['linear|9436f96375b1259b']`,
    		`print('SEED_ACCESS=' + hashlib.sha256(e['access_token'].encode()).hexdigest())`,
    		`print('SEED_HAS_REFRESH=' + str('refresh_token' in e and bool(e['refresh_token'])))`,
    		`os.makedirs(os.path.expanduser('~/.codex/mcp-oauth-locks'), exist_ok=True)`,
    		`with open(os.path.expanduser('~/.codex/mcp-oauth-locks/file-store.lock'), 'a') as l:`,
    		`    fcntl.flock(l, fcntl.LOCK_EX)`,
    		`    d = json.load(open(p))`,
    		`    d['linear|9436f96375b1259b'].update(access_token='jail-access-2', refresh_token='jail-refresh-2')`,
    		`    open(p, 'w').write(json.dumps(d))`,
    		`PY`,
    	}, "\n")
    	r := runYolo(t, dir, script, withEnv("YOLO_NO_AUTO_IMAGE_REAP=1"))
    	if r.rc != 0 {
    		t.Fatalf("rc %d\n%s\n%s", r.rc, r.stdout, r.stderr)
    	}
    	if got, want := kvLine(r.stdout, "SEED_ACCESS"), sha256Hex("forged-access-1"); got != want {
    		t.Fatalf("seeded access hash %s, want %s", got, want)
    	}
    	if kvLine(r.stdout, "SEED_HAS_REFRESH") != "False" {
    		t.Fatalf("the seeded copy carries a refresh token")
    	}
    	if !strings.Contains(r.stderr, "MCP sign-ins (codex): linear shared from your user-level servers") {
    		t.Fatalf("missing the launch block\n%s", r.stderr)
    	}
    	rec := readMCPRecord(t, filepath.Join(state, "codex.json"), "linear")
    	if rec.Generation != 2 || rec.RefreshToken != "jail-refresh-2" || rec.AccessToken != "jail-access-2" {
    		t.Fatalf("harvest: generation %d, access %s, refresh %s", rec.Generation,
    			fingerprint(rec.AccessToken), fingerprint(rec.RefreshToken))
    	}
    	ws := readJSON(t, filepath.Join(dir, ".yolo/home/codex/.credentials.json"))
    	if _, has := ws["linear|9436f96375b1259b"].(map[string]any)["refresh_token"]; has {
    		t.Fatalf("the workspace store kept the jail's refresh token after harvest")
    	}
    	if left := awaitHomeHostDaemons(os.Getenv("HOME"), homeDaemonBound); len(left) > 0 {
    		t.Fatalf("daemons outlived the launch: %s", strings.Join(left, ", "))
    	}
    }
    ```

    `forgeMCPRecord`, `readMCPRecord`, `sha256Hex`, `fingerprint` and `readJSON` are new helpers
    in the same file, built on `internal/mcpauth`'s types. The forged record's `entry` has Codex's
    field names.
  - Red: `go test ./internal/cli/run ./internal/mcpauthdaemon -run 'MCPAuth|Share'` fails to
    compile, and the integration test fails at the launch block.
- **Change:**
  - **The pack:** `packs/mcp-auth/pack.toml` (a description, and `[[loophole]] from =
    "loopholes/mcp-auth-broker"`) and `loophole.toml`:

    ```toml
    name = "mcp-auth-broker"
    version = 1
    serves = ["status"]
    transport = "loopback-tls"
    lifecycle = "spawned"
    doctor_cmd = ["yolo", "internal", "daemon", "mcp-auth-broker", "--self-check", "--state-dir", "{state}"]

    [host_daemon]
    cmd = ["yolo", "internal", "daemon", "mcp-auth-broker", "--socket", "{socket}",
           "--state-dir", "{state}", "--share", "{launch_input}"]
    publishes = "socket"
    detach = true
    launch_input = "mcp-auth-share"
    ```

    Match `serves` to whatever the Claude broker's manifest declares. Embed the pack if
    `packs/embed.go` lists packs explicitly.
  - **Codex:** [`packs/codex/pack.toml`](../../packs/codex/pack.toml) gets `needs = [{ pack =
    "codex-auth" }, { pack = "mcp-auth" }]` and Codex's `[[mcp_auth]]` block from design [§6](mcp-auth-sharing.md#6-what-an-agent-pack-declares).
    `derive.lua` gets the `yolo.mcp_auth("codex", …)` registration from task 7's test. The
    config derive renders `mcp_oauth_credentials_store = "file"` in a container jail
    ([task 15](#task-15) adds the host branch).
  - **The token:** `internal/loopholedecl` validates `launch_input` against the closed producer
    set. `{launch_input}` is legal only in a `cmd` whose `[host_daemon]` declares
    `launch_input`. `scopeTokenArg` resolves it from the same map `writeScopeFiles` fills, keyed
    by loophole name.
  - **The producer:** `internal/cli/run/mcpauthshare.go`. It is called where `writeScopeFiles`
    is, and:
    - computes the shared set from task 6's scopes;
    - computes keys with `luahook.MCPAuthKeys` over the pack's `derive.lua`;
    - writes the file;
    - prints the launch block.

    The spawn loop removes the file after the daemon exits, as it does a scope file.
  - **The daemon:** `internal/mcpauthdaemon`, dispatched by `case "mcp-auth-broker"` in
    `internaldaemon.Run`, with the name added to the usage line. Order:
    1. take the liveness lock ([ID-5](#id-5));
    2. seed every replica ([ID-7](#id-7));
    3. bind and serve `{"op":"status"}` on the fronted socket;
    4. on SIGTERM or the end of the jail, harvest (`PlanAdopt`) once, write audit, release, exit.

    The self-check prints `OK:` with the record count, `NOTE:` for no records, and `FAIL:` for a
    canonical file that does not parse, naming `yolo mcp-auth status`. That verb lands in
    task 14, so until then the line names the file path instead. `hintcommands_test` enforces
    this.
  - **Environment:** add `YOLO_SERVICE_MCP_AUTH_BROKER_ENDPOINT` to `unknownlaunchenv.go`.
- **Gates:** `go test ./internal/... ./packs`; `just check-ci`;
  `go test -count=1 -run 'TestMcpAuthBroker|TestOpenAIAuthBroker|TestRemoteMcpServer|HostDaemon' ./integration`;
  the nested-jail check, reporting `podman info --format '{{.Host.Security.Rootless}}'`.
- **Fences:** no change to the Claude or OpenAI brokers. `HostServiceAdmittedOn` is unchanged.
- **Commit:** `feat(mcp-auth): share Codex's user-level MCP sign-ins at launch and exit`
- **Report:** red, green, the integration output (hashes only), and the nested-jail line.

<a id="task-13"></a>**Task 13: live sync: watch, adopt, propagate.** *After 12.*

- **Settled:** [§8.2](mcp-auth-sharing.md#82-adopt) (*"an `inotify`/FSEvents watch, with a 30 s
  poll as the floor"*), and [§8.3](mcp-auth-sharing.md#83-propagate) (*"A replica whose lock
  cannot be taken within 10 s is retried at 1, 2, 4 … 60 s and logged once; it is never written
  without its lock."*). Two adoptions at once are serialized by the canonical lock, and the
  later one wins.
- **Tests first:** `internal/mcpauthdaemon/sync_test.go`. Two daemons on two share files over
  one temporary state directory, with a fake clock for the poll and the backoff:
  - A write in replica A reaches replica B within one poll. With inotify on Linux, assert it
    arrives before the poll fires.
  - B's lock held by a test descriptor: B is written after release, never before. The backoff
    sequence is 1, 2, 4, 8, 16, 32, 60, 60 s, and it is logged once.
  - A stale share file (lock free, `written_at` 11 minutes old) is skipped and removed.
  - A share file whose lock is free and 1 minute old is skipped and kept.
  - A sign-out in A removes the entry from B.
  - Event order for one adoption: `adopt`, then `propagate` per replica.

  Red: `go test ./internal/mcpauthdaemon -run Sync`.
- **Change:** a watch per replica root directory (the root is opened beneath; watch its path),
  and a poll ticker. Propagation reads `replicas/` under the canonical lock, filters by liveness
  lock and pack, and writes through task 11's store code.
- **Integration:** extend `integration/mcpauth_test.go` with
  `TestMcpAuthBrokerPropagatesBetweenRunningJails`. Two workspaces with the same user config:
  launch jail A detached, with a script that waits for a marker file; launch jail B, which
  writes a new sign-in. Assert that A's store holds B's access hash within 10 s, and that A's
  copy has no refresh token. Follow the harness's existing detached-launch pattern
  (`rg -n 'detach' integration/harness_test.go`). If there is none, stop and report: the test
  needs a harness change outside this fence.
- **Gates:** `go test -race ./internal/mcpauthdaemon`; `just check-ci`;
  `go test -count=1 -run TestMcpAuthBroker ./integration`.
- **Commit:** `feat(mcp-auth): live sync between running Codex jails`

<a id="task-14"></a>**Task 14: refresh, record states, and `yolo mcp-auth status`.** *After 13.*

- **Settled:**
  - [§8.4](mcp-auth-sharing.md#84-refresh-the-broker-is-the-single-refresher):
    - refresh *"when a quarter of its lifetime or 10 minutes remain, whichever is later"*;
    - discovery by *"RFC 9728 protected resource metadata, then RFC 8414"*;
    - `invalid_grant` makes the record `dead` and removes it from every replica, with the line
      naming `login_hint`;
    - transient failures retry at 30 s, 1, 2, 5 min until expiry.
  - [§8.6](mcp-auth-sharing.md#86-what-the-launch-prints): `yolo mcp-auth status` lists each
    record's generation, expiry and last refresh, and never a token.
  - [ID-6](#id-6), [ID-8](#id-8).
- **The status object, as a contract** (`yolo.mcp-auth.status/1`; `--json` prints it, and the
  plain form renders it):

  ```json
  {
    "schema": "yolo.mcp-auth.status/1",
    "agents": [
      {
        "pack": "codex",
        "sharing": "live",
        "servers": [
          { "name": "linear", "scope": "user", "state": "live", "generation": 3,
            "expires_at": "2026-11-04T13:00:00Z", "refreshed_at": "2026-11-04T12:00:00Z",
            "refreshed_by": "yolo-work-a-3f2c", "next_step": "" },
          { "name": "notion", "scope": "workspace", "state": "kept",
            "next_step": "yolo config set mcp_servers.notion.url https://mcp.notion.com/mcp --user" }
        ]
      }
    ]
  }
  ```

  - `sharing` is `live`, `launch-boundary`, or `not-shared` with a `reason`.
  - In a jail, `servers` lists only what this launch composed. On the host it lists every record
    and every live replica.
- **Discovery, in order:**
  1. `GET <origin>/.well-known/oauth-protected-resource<path>`, then `<origin>/.well-known/oauth-protected-resource`;
  2. from its `authorization_servers[0]`: `/.well-known/oauth-authorization-server<path>`, then
     `/.well-known/openid-configuration`;
  3. cache `token_endpoint` in the record.

  The refresh is `POST token_endpoint` with `grant_type=refresh_token`, `refresh_token`,
  `client_id` and `resource=<server url>`. A response with no new `refresh_token` keeps the old
  one. A `token_type` of `DPoP` is not handled here; phase D handles it.
- **Tests first:** `internal/mcpauthdaemon/refresh_test.go`, with an `httptest.NewTLSServer`
  serving both metadata documents and the token endpoint, and the client injected (no environment
  hatch):
  - the schedule for a 1-hour token (refresh at 15 min left) and a 20-minute one (10 min left);
  - two daemons, one refresh (count the token-endpoint hits);
  - rotation keeps the new refresh token in canonical and none in any replica;
  - `invalid_grant` makes the record `dead` and the replicas empty, writes the audit event `dead`,
    and the launch line reads
    `linear: its sign-in was revoked; sign in again inside any jail with codex mcp login linear`;
  - a 503 retries at 30 s, then 1 min;
  - discovery failure leaves the record unrefreshed, and status names it.

  Then `internal/cli/mcpauth_test.go`: `yolo mcp-auth status` over a fixture state prints the
  plain form; `--json` matches the object above; a token never appears in either (grep the
  output for the fixture's token strings).
  Red: `go test ./internal/mcpauthdaemon ./internal/cli -run 'Refresh|MCPAuth'`.
- **Change:**
  - A refresh scheduler in the daemon, under the per-record refresh lock
    `<state>/codex.<identity>.refresh.lock`.
  - The `status` handler.
  - The verb `yolo mcp-auth` (`status`, with `--json`), added to `registry` in
    [`dispatch.go`](../../internal/cli/dispatch.go), to `commandHelp` and to `subcommandUsage`.
  - Route it early in `cli.Main` when in a jail, as `gh` and `git-remote` are, because it reads
    a service endpoint.
  - The tests that fail on a new verb (`TestEveryRegisteredCommandIsListedInHelp`,
    `TestUsageListedCommandsAreRegistered`, `TestEveryRegisteredCommandAnswersHelp`,
    `TestSubcommandUsageHasNoStaleEntries`, `TestUsageListsEveryParsedFlag`,
    `TestEveryVerbDispatcherIsListed`) are its checklist.
  - Task 12's self-check line now names `yolo mcp-auth status`.
- **Gates:** `go test -race ./internal/mcpauthdaemon`; `go test ./internal/cli`; `just check-ci`;
  `go test -count=1 -run TestMcpAuthBroker ./integration`.
- **Commit:** `feat(mcp-auth): the broker refreshes shared sign-ins; yolo mcp-auth status`

<a id="task-15"></a>**Task 15: Linux `yolo host`, and `mcp_sign_in_store`.** *After 12; independent of 13 and 14.*

- **Settled:**
  - [MA-D19](mcp-auth-sharing.md#decision-ledger): `mcp_sign_in_store` is user-scope, `"agent"`
    (the default) or `"file"`, and is refused in project config naming the `--user` command.
  - [MA-D20](mcp-auth-sharing.md#decision-ledger): at `yolo host`, on every OS, the default
    leaves Codex on its own store, so its MCP sign-ins are not shared. The launch says so:
    `codex keeps MCP sign-ins in its own credential store here, so they are not shared. To share
    them: yolo config set mcp_sign_in_store file --user`.
  - In a container jail the derive pins the file store whatever the key says
    ([§10](mcp-auth-sharing.md#10-parity-every-backend-and-notch)).
- **Tests first:**
  - Config: `mcp_sign_in_store = "file"` in workspace config is refused with
    `config.mcp_sign_in_store: is user config only; run yolo config set mcp_sign_in_store file --user`.
  - Derive: `DeriveCtx.MCPSignInStore` `"file"` renders `mcp_oauth_credentials_store = "file"`;
    `"agent"` renders no such key.
  - The host producer writes a replica `{ "root": { "dir": <home> } }` only when the key is
    `"file"`, and otherwise returns the line above.
  - Integration is not possible for `yolo host` in this suite. Run the unit test of `yolo host
    apply` in [`internal/entrypoint/hostrender.go`](../../internal/entrypoint/hostrender.go)'s
    tests for the rendered key, and say in the commit what a human checks on a Linux host: two
    `yolo host -- codex` sessions in two workspaces, one `codex mcp login linear`, and
    `yolo mcp-auth status` showing one record.

  Red: `go test ./internal/config ./internal/entrypoint ./internal/cli/run -run 'SignInStore|HostMCP'`.
- **Change:**
  - Add the key to `config.go`, `schema.go` and the user-only key list.
  - `DeriveCtx.MCPSignInStore` is set to `"file"` by the container-jail callers and to the key's
    value by host and macos-user callers.
  - The Codex derive's branch.
  - The host launch path writes a share file and spawns the daemon for the agent that
    `yolo host --` runs ([§10](mcp-auth-sharing.md#10-parity-every-backend-and-notch) table,
    *Daemon admitted*); `rg -n 'startLoopholes' internal/cli` shows whether `yolo host` already
    spawns host daemons. If it does not, stop and report: that is a framework change outside
    this plan.
- **Gates:** `go test ./internal/...`; `just check-ci`.
- **Commit:** `feat(mcp-auth): share Codex's sign-ins at yolo host when mcp_sign_in_store is file`

### Phases C to E: outline, re-planned when phase B lands

Each of these needs a planning pass at the commit phase B lands on. Their settled behavior is
the design's; what is recorded here is what that pass must start from.

- **Phase C, slice 2** ([§16](mcp-auth-sharing.md#16-what-i-would-build-in-order) item 3):
  - the `proper-lockfile` and `mkdir-heartbeat` protocols;
  - the opencode and Claude adapters (Claude in view mode,
    [OQ-MA3](mcp-auth-sharing.md#decision-ledger));
  - pi, after its lock fix ([§11](mcp-auth-sharing.md#11-existing-state-on-the-day-it-ships));
  - the Apple Container launch-boundary mode and admission (`HostServiceAdmittedOn`), and the
    macos-user columns, each behind its witness in a Mac design doc.

  The pi lock fix is its own task first: `packs/pi/pack.toml`'s `session_homes` link to
  `mcp-auth.json`, and pi's lock beside the link.
- **Phase D, one sign-in** (item 4):
  - `sign_in` and `mcp_sign_in` (user-scope only, refused in workspace config);
  - `oauth.client_secret_command`, also user-scope only
    ([MA-D26](mcp-auth-sharing.md#decision-ledger));
  - the Claude adoption and write-back under `.storage-write.lock`;
  - `yolo mcp-auth header`, which writes the `header` audit event and refuses a server this
    launch did not compose;
  - `yolo mcp-auth logout` and `login`;
  - the header-route renders for Codex (`http_headers_helper`) and pi.

  It is gated on the measurements in
  [§9.3](mcp-auth-sharing.md#93-adopting-a-sign-in-an-agent-already-has) and item 4.
- **Phase E, slice 3** (item 5): the store route, then Copilot, agy, Muse and omp as their
  facts arrive.
- **The brokercore and framework migration.** When
  [`pack-brokers.md`](pack-brokers.md) lands its framework pieces (the readout request, a generic
  per-launch input token, a framework credential fence), move this broker onto them. Do not
  re-implement them in parallel. See
  [Coherence with the pack-broker design](#coherence-with-the-pack-broker-design).

## Ships with

- **Tests by level.**
  - Unit:
    - the ctx-boundary filter (task 1);
    - config validation (task 2);
    - the dialects (tasks 3 to 5);
    - the scope table (task 6);
    - the key vectors (task 7);
    - the declaration (task 8);
    - the fence (task 9);
    - audit (task 10);
    - the planners and store (task 11);
    - the producer and daemon (task 12);
    - sync (task 13);
    - refresh and status (task 14);
    - the host key (task 15).
  - Integration, in `integration/mcp_test.go`: `TestRemoteMcpServerIsWithheldFromAnAgentWithNoRemoteForm`
    and `TestRemoteMcpServerReachesCodex`.
  - Integration, in `integration/mcpauth_test.go`: `TestMcpAuthBrokerSeedsAndHarvestsCodex` and
    `TestMcpAuthBrokerPropagatesBetweenRunningJails`. These two catch a broken end-to-end
    share, which no unit test does.
- **Tests that will break, and should.** Any test asserting that a read-only `mounts` of a
  host-daemon state directory is allowed (task 9). Any golden render of Codex's `config.toml` in
  a jail, which gains `mcp_oauth_credentials_store = "file"` (task 12). Rewrite them to the new
  behavior.
- **Docs that now describe the old thing:**
  - [`mcp-configuration.md`](../reference/mcp-configuration.md): the composition, the remote form,
    scope, and the broker.
  - The user guide's MCP pages and [`settings-per-setup.md`](../../userguide/reference/settings-per-setup.md).
  - [`loophole-system.md`](../reference/loophole-system.md): a new pack-shipped loophole and its
    Apple Container standing.
  - [`context-mounts.md`](../reference/context-mounts.md): the broker mount fence now covers host
    daemons' state.
  - [`pack-system.md`](../reference/pack-system.md): the kinds table and the pack-wide keys.
  - `config_ref.txt` (task 2).
- **Surfaces:**
  - the launch block (task 12);
  - `yolo mcp-auth status` (task 14);
  - the self-check lines;
  - the audit events (`yolo audit --service mcp-auth`);
  - `YOLO_SERVICE_MCP_AUTH_BROKER_ENDPOINT`.
- **Cheap, and the implementer's:** the Go types inside `internal/mcpauth`; how the poll and the
  watch share a loop; the error wording of internal failures that no user line quotes.
- **Stop and ask:**
  - a measured Codex that rewrites a seeded entry it did not sign in to (the planners would then
    adopt their own copy);
  - a Codex release that changes its key function (task 7's vectors go red);
  - any need to write a replica without its lock.

## Don't

- **Don't build the broker on `brokercore.Daemon`.** It requires a repository scope file and
  starts the request store and `on_request` hook, none of which this broker has: it decides no
  call and files no request ([`pack-brokers.md` §13](pack-brokers.md#13-coherence-with-the-mcp-sign-in-broker)).
- **Don't add a `[brokered]` block** to the loophole. That block means "decides calls under
  grants", and `yolo approve` would start listing it.
- **Don't write a generic OAuth refresh client and reuse it for the Claude or OpenAI brokers.**
  Theirs are provider-specific on purpose.
- **Don't put a cache of agent keys in the canonical store.** Records are keyed by name and
  identity digest so that a change to a key function does not orphan them
  ([§7](mcp-auth-sharing.md#7-the-canonical-store)). The key lives in the share file, recomputed
  every launch.
- **Don't add a `YOLO_*` switch to turn sharing off** ([§12](mcp-auth-sharing.md#12-forbidden-behavior)).
- **Don't admit the daemon on Apple Container** in phase B. It needs the launch-boundary mode
  and its witness (phase C).

## Blockers

- **B1, measured before task 13 claims "within seconds":** does a running Codex re-read
  `.credentials.json` when it changes, or only at process start? Run it with a real sign-in. If
  it reads only at start, a propagated sign-in reaches the next Codex process, not the running
  one. That bounds the first line of design
  [§16](mcp-auth-sharing.md#16-what-i-would-build-in-order)'s *what done looks like*, and the
  designer must restate it before task 13 lands.
- **B2, before pi, Copilot or agy get a remote form:** each one's remote MCP dialect, read from
  its source or documentation at the version yolo installs. This plan has no witness for them,
  so they keep the withheld-with-notice line.
- **No design question is open.** The decisions this plan recorded in the design's ledger
  (MA-D20 to MA-D26) change no ruling.

## Coherence with the pack-broker design

This section was checked against [`pack-brokers.md`](pack-brokers.md) and
[`access-introspection.md`](access-introspection.md) as they stand uncommitted on 2026-11-04,
both `status: draft`. The four points
[`pack-brokers.md` §13](pack-brokers.md#13-coherence-with-the-mcp-sign-in-broker) asks for, and
where they land:

| Its ask | Answer | Lands in |
| :--- | :--- | :--- |
| **Audit:** adopt, refresh, sign-out, dead, each header, `service = "mcp-auth"`, never a token | Adopted, plus `seed` and `harvest` when they write ([ID-10](#id-10), [MA-D22](mcp-auth-sharing.md#decision-ledger)) | Task 10 (the fields), tasks 12 to 14 (the events), phase D (`header`) |
| **Readout:** the same answer as `yolo mcp-auth status`, over the broker's endpoint | Adopted: one object, `yolo.mcp-auth.status/1`, served for `{"op":"status"}` ([ID-8](#id-8), [MA-D23](mcp-auth-sharing.md#decision-ledger)) | Task 12 (the op), task 14 (the object) |
| **Fence:** the canonical store fenced from `mounts` | **Not covered today:** a read-only mount of yolo's state directory is refused by nothing unless a `brokered` loophole is selected, and even then only `broker/` and declared credential paths are fenced. This plan fences every selected host-daemon loophole's state directory, which also closes the same gap for the Claude and OpenAI brokers ([ID-9](#id-9), [MA-D24](mcp-auth-sharing.md#decision-ledger)) | Task 9 |
| **Shared code:** the framework's lifecycle, supervision and declarations | Adopted for what exists: the spawned-loophole lifecycle (`startExternalService`, `daemonSupervisor`, `stopService`/`detachService`), the loopback-TLS front, `needs`, and the manifest. Not `brokercore`, which is the call layer ([Don't](#dont)) | Task 12 |

What [`pack-brokers.md`](pack-brokers.md) and its plan must adopt in turn, so there is one
framework and not two:

1. **The framework fence is ID-9's rule.** Every selected host-daemon loophole's state directory
   is fenced; a `credential_paths` key is the addition for credentials held elsewhere. If the
   framework names that key outside `[brokered]`, the MCP pack adopts it at the migration step.
2. **A per-launch input token.** This plan adds `launch_input` and `{launch_input}` for a
   core-owned producer. The framework should either adopt it as its generic per-launch input
   (with `{repository_scope}` as one producer) or rename both in one change. It must not add a
   third mechanism.
3. **The readout request.** The framework's readout request should be, or alias, `{"op":"status"}`
   returning a schema-tagged object. Then `yolo access` renders this broker's object as its
   readout line without a second protocol.
4. **Audit's shape.** Task 10 adds `server` and `generation` to `brokeraudit.Event` and teaches
   `yolo audit` to render an event with no argv. The framework's audit section should take these
   as framework fields, not MCP-only ones.
5. **Apple Container reach.** The framework's "unreachable on this backend" readout line is what
   this broker shows there until phase C admits it.
6. **Naming.**
   - The pack is `mcp-auth`, the loophole `mcp-auth-broker`, the audit service `mcp-auth`, and
     the verb `yolo mcp-auth`.
   - `yolo access` ([OQ-AX2](access-introspection.md#OQ-AX2) is still open) shows this broker
     under its loophole name.
   - `yolo approve`, `yolo wait` and `yolo history` never list it.
