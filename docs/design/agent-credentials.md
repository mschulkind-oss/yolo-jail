# Agent credentials: what crosses the jail boundary, and how

**Status:** DRAFT (2026-07-23) — describes the shipped code as of this date;
every claim carries a `file:line` (line numbers drift — trust the named
function). Records both the model and the one prose/code discrepancy found
(§4, §6).
**Scope:** how yolo-jail delivers (and deliberately withholds) credentials and
identity for all six supported agents (`claude`, `copilot`, `gemini`,
`opencode`, `pi`, `codex` — plus `agy`), across the three backends (`podman`,
`container` = Apple Container, `macos-user`) and both OSes.
**Reads with:**
[jail-home.md](jail-home.md) (how `/home/agent` is composed, where creds land),
[identity-prism-decision.md](identity-prism-decision.md) (the git-identity
allowlist + mechanism),
[macos-user-nix-and-features.md](macos-user-nix-and-features.md) (the no-VM
backend and its shared-home consequence),
[macos-user-build-step-threat-model.md](macos-user-build-step-threat-model.md)
(the one place a prior session's writes flow back host-side),
[../guides/loopholes.md](../guides/loopholes.md) +
[loophole-protocol.md](loophole-protocol.md) (the oauth broker and the
host-service pattern),
[storage-and-config.md](storage-and-config.md) (state separation).

## The one thing to internalize

yolo-jail's credential story is a **structural** one, not a policy one: host
credentials are **physically absent** from the jail, and the only credentials an
agent can reach are ones a human deliberately provisioned *into* the jail
through a small set of explicit channels. There is no `denyRead` list to get
right — `~/.ssh`, `~/.gitconfig`, `~/.aws`, and cloud/gh tokens are simply not
mounted (or, on macos-user, actively denied by the Seatbelt profile). Compare
[../research/sandbox-comparison.md](../research/sandbox-comparison.md): "Not
mounted; no host credentials by default" vs. the syscall-filter model where an
un-set `denyRead` leaks `~/.aws`.

---

## 1. The credential boundary

The canonical statement lives in the auto-generated per-jail briefing and is
mirrored into `AGENTS.md`/`CLAUDE.md` verbatim
(`internal/agents/agentsmd.go:186-195`):

> Host credentials are not propagated into the jail: the host's `~/.ssh`,
> `~/.gitconfig`, and cloud/gh tokens are invisible here. This is a **credential
> boundary, not a network block** — outbound SSH and HTTPS work normally, so git
> push/pull and API calls succeed whenever the jail has its own credentials
> (e.g. a workspace-specific deploy key or a token in `.env`). Only without such
> jail-local credentials do authenticated operations fail.

Two consequences worth stating plainly:

- **The network is open; the wallet is empty.** Bridge networking is the default
  (`docs/research/sandbox-comparison.md`), so the jail can reach the internet and
  SSH out. What it lacks is *identity* — a private key, a PAT, an OAuth token. A
  `git push` fails not because the packets are blocked but because the jail holds
  no credential the remote will accept.
- **Nothing is stripped; things are never added.** The design is an *allowlist*
  everywhere it can be (git identity is the clearest example, §2.6): rather than
  mount the host's `~/.gitconfig` and scrub credentials out, yolo composes a
  fresh file containing only the two keys it names. See
  [identity-prism-decision.md §2](identity-prism-decision.md).

The boundary is enforced by **omission** at the mount-assembly layer
(`internal/cli/run/assemble.go`, `assemble_parts.go`): the host home is never a
mount source; only the yolo-managed `GLOBAL_HOME`
(`~/.local/share/yolo-jail/home`) and the per-workspace `.yolo/home` overlay are.
On macos-user there is no mount layer at all, so the boundary is enforced by the
Seatbelt profile's read denies instead (§5).

---

## 2. Delivery mechanisms — the toolbox

Seven mechanisms carry (or withhold) credential-shaped data. Each is
**backend-conditional**; the per-backend split is summarized in §5.

### 2.1 `:ro` bind mounts (container backends only)

A host file mounted read-only into the jail. Kernel-enforced against even the
jail's root. Used for:

- **Composed git config** (podman): `<wsState>/yolo-gitconfig` →
  `/home/agent/.config/git/config:ro` (`assemble_parts.go:253-255`).
- **Global gitignore** (podman): host `core.excludesFile` →
  `/home/agent/.config/git/ignore:ro` (`assemble_parts.go:231-237`).
- **Host `~/.claude` / `~/.pi/agent` settings**: the yolo-declared per-agent
  host file (`agents.AgentSpec.HostFiles` — claude/pi each declare just
  `settings.json`) mounted `:ro` at `/ctx/host-claude/settings.json` and
  `/ctx/host-pi/settings.json`, then *composed* into the jail `settings.json` at
  boot (§2.2). Wired at `assemble.go:335`; built by `hostFileArgs`
  (`internal/cli/run/hostclaude.go`). Which host files cross **for an agent
  surface** is fixed in yolo-shipped code — a **credential boundary, not a config
  knob** (retiring `host_claude_files`/`host_pi_files` is what bought that; §2.2).
- **User-declared host files** (`host_files`) cross at `/ctx/host-user/<slug>:ro`,
  built by `hostUserFileArgs` (`internal/cli/run/hostfiles.go`). This is a
  *separate* channel from the per-agent set above and it **is** user-widenable —
  under a per-entry scope rule that is the whole point. See §2.4.

Apple Container cannot do a nested single-file `:ro` bind, so it **materializes**
(copies) these into `<wsState>` instead, relying on the whole-`wsState` →
`/home/agent` bind (`acMaterialize`; e.g. `assemble_parts.go:233`,
`assemble_parts.go:247-251`). The `:ro` guarantee is lost there — the copy is
writable — but the file is regenerated every run regardless.

### 2.2 Generated / copied files at boot

The entrypoint (`internal/entrypoint`) re-runs pure generators on every boot and
writes into the **writable per-workspace overlays** (`jail-home.md §3`). Relevant
to credentials:

- **Host agent settings are composed in.** claude/pi each read one host file —
  `settings.json`, the yolo-declared `agents.AgentSpec.HostFiles` set — from the
  `/ctx/host-<agent>/` `:ro` mount, **fail-open** (a missing mount yields the
  defaults). The prism composes it into the jail's `~/.claude/settings.json` /
  `~/.pi/agent/settings.json` as `defaults<host<overlay<computed<transform<managed`,
  so host changes propagate while jail-local edits survive (captured in the §5
  overlay sidecar) and yolo-required keys win
  (`internal/entrypoint/prism_claude.go`, `prism.go`). Which host files cross **for
  an agent surface** is not a config knob — the retired
  `host_claude_files`/`host_pi_files` keys used to let a workspace config widen it,
  and that was the credential-boundary hole §10.4 of the settings-composition plan
  closed. A user may still bring extra host files in via `host_files`, but under a
  per-entry scope rule and never onto an agent surface path (§2.4). **This is the
  delivery path for API-key-in-settings credentials** — see the Bedrock worked
  example (§3).
- **Composed git identity replay** (macos-user only): `configureGit`
  (`internal/entrypoint/identity.go:12-28`) runs `git config --global user.name/
  user.email/core.excludesFile` from `YOLO_GIT_*` env (§2.6).

### 2.3 Launch env + `env_sources` (the sanctioned secret channel)

`env_sources` is the **primary way jail-local credentials enter** (deploy-key
passphrases, provider API keys, AWS keys). It is an ordered list; each entry is
either an inline `{"KEY":"VALUE"}` map or a path to a `KEY=VALUE` dotenv file
(`#` comments, quotes, `export` prefix all tolerated). Later entries win;
missing files warn-and-skip rather than failing the run (config-ref
`env_sources`; `internal/config/envsources.go:58-99`, `ParseDotenv:16-42`). Path
resolution supports `~`, absolute, and workspace-relative
(`ResolveEnvSourcePath:44-56`).

Delivery differs by backend but the *resolver* is shared
(`config.ResolveEnvSources`):

- **Container backends:** resolved host-side (`run.go:187`), written to
  `<wsState>/yolo-user-env.sh` as `export K=${K:-'v'}` lines
  (`internal/cli/run/userenv.go:19-34`), mounted/materialized at
  `~/.config/yolo-user-env.sh` (`assemble.go:180-185`), and *sourced* by
  `.bashrc` and the entrypoint (`internal/entrypoint/boot.go:113-119`,
  `shell.go:100`). It is a **file the jail sources**, not a `docker -e` block, so
  the values are re-derivable from that one file.

  Because the file OUTLIVES the config that produced it, the writer's empty case
  truncates rather than no-ops: an empty resolve means "nothing should be
  exported", and leaving the previous render in place instead made removing
  `env_sources` unable to revoke a credential — the stale file kept exporting it
  through every subsequent rebuild. The file itself must survive (it is a
  bind-mount source; podman refuses to start on a missing one), so revocation is
  truncate-in-place, not delete.
- **macos-user:** resolved host-side in `buildPlan`
  (`internal/macosuser/orchestrator.go:120-131`), then baked onto the sandbox
  launch/bootstrap argv via `env -i K=V …`
  (`internal/macosuser/runplan.go:56-86`).

**The `${VAR}` placeholder convention** ties `env_sources` to MCP — but as of
2026-08-03 **yolo does not do the substituting**. An MCP server's `env` value
written as `"${TAVILY_API_KEY}"` is passed through VERBATIM, and the agent that
launches the server resolves it from its own environment, where `env_sources`
values already are (`hydrateEnvFromUserEnvFile` exports them before any generator
runs). A `requires_env` gate still drops the server entirely if the var is empty.
See [mcp-configuration.md](mcp-configuration.md) for why yolo interpolating was
removed: the expanded value had no provenance layer, and it sourced config content
from process env at render time. A secret still lives in one unsynced dotenv file
and is still scoped to exactly one MCP server — the resolution just happens one
step later, at launch, by the consumer.

### 2.4 User-declared host files (`host_files`) — per-entry scope

`host_files` lets a user bring **any** host file into the jail as a composed
surface (`docs/plans/host-file-staging.md`). It reopens, deliberately and
narrowly, the ability the retired `host_claude_files`/`host_pi_files` keys had —
so it is a credential-relevant mechanism and its boundary is **per entry**, not
per key:

| Entry kind | Crosses a host file? | Allowed scope |
|---|---|---|
| **source-bearing** — a bare string, or an object with `source` | **yes** | **user config only** |
| **source-less** — object with `content`, or only `managed`/`defaults` | no | user **or** workspace |

The split is enforced **by construction**, not by a check: `config.LoadHostFiles`
reads source-bearing entries **only** from `paths.UserConfigPath()` (plus its
`include_if_found` files), so workspace scope is inexpressible. Of the places a
config key can come from, two are jail-writable — the workspace
`yolo-jail{,.local}.jsonc` and `<workspace>/.yolo/config-snapshot.json`, both on
the read-write `/workspace` bind — and only the host user config is not.
`validateHostFiles` additionally hard-errors on a source-bearing entry found at
workspace scope; that is defense-in-depth against a silent no-op, not the
boundary itself.

A source-less entry copies nothing from the host — it just brings a yolo-managed
file into being — so a repo may legitimately ship one.

**Within the user's own config nothing is blocked.** Listing `~/.ssh/id_ed25519`
there is the human's call: their machine, their decision, and a blocklist would be
unenforceable anyway (symlinks). The boundary is that the **repo** cannot make
that choice on their behalf.

Delivery: each source-bearing entry's host path is bound `:ro` at
`/ctx/host-user/<slug>` (`hostUserFileArgs`, `internal/cli/run/hostfiles.go`), and
the resolved entry list travels to the entrypoint in `YOLO_HOST_FILES` — the host
CLI is the single source of truth, so the entrypoint never re-reads config. A
destination may not be any path yolo owns as a single file/symlink, nor any
yolo-composed agent surface (`hostFileReservedDests`), so this channel cannot be
used to overwrite `~/.claude/settings.json` and strip its managed block.

**macos-user carries only the source-less entries** (`SourceLessHostFiles`): with
no bind mounts there is no `/ctx/host-user` to carry a source into, and a
source-bearing entry is skipped rather than silently rendering without its host
layer.

### 2.5 The `claude-oauth-broker` loophole (Claude OAuth refresh)

The broker exists because **Anthropic mints single-use refresh tokens**: if two
jails share one `.credentials.json` and both refresh in the same window, one
loses the race and gets logged out
([../guides/USER_GUIDE.md:180-189](../guides/USER_GUIDE.md)). It bundles two jobs
([macos-user-nix-and-features.md §3.5](macos-user-nix-and-features.md);
`bundled_loopholes/claude-oauth-broker/README.md`):

1. **One shared credentials file per host.** On containers this is the
   `.claude-shared-credentials` bind (`assemble.go:166-168`) plus an in-jail
   relative symlink `~/.claude/.credentials.json →
   ../.claude-shared-credentials/.credentials.json`
   (`internal/entrypoint/claude.go:161-187`; `jail-home.md §4.2`). One OAuth
   identity, every jail.
2. **Serialize the refresh HTTP call.** A host-side **singleton** daemon (`yolo
   internal daemon claude-oauth-broker`, socket
   `/tmp/yolo-claude-oauth-broker.sock`, `internal/broker/brokerlifecycle.go:36-62`)
   holds a `flock` (`RefreshLockPath`, `internal/oauthbroker/refresh.go:14-39`)
   around every refresh, so concurrent jails can't burn the token. Because Claude
   Code refreshes *itself* and will never voluntarily take our lock, an in-jail
   TLS terminator (`yolo-jaild oauth-terminator`) intercepts
   `platform.claude.com` (via `--add-host …:127.0.0.1`) and routes the refresh
   through a per-jail host relay to the singleton. The broker operates on **one
   file only** —
   `~/.local/share/yolo-jail/home/.claude-shared-credentials/.credentials.json` —
   and never touches host Claude's `~/.claude/.credentials.json`
   (`README.md:61`). Host and in-jail Claude keep **independent** OAuth
   identities (separate `/login` flows; Anthropic allows multiple concurrent
   refresh tokens) — this is the fix for the 2026-04-23 `invalid_grant` incident
   (`README.md:63-65`).

Activation is gated by `requires.command_on_path: claude`
(`manifest.jsonc:13-15`) — present-but-inactive when host Claude isn't
installed. Apple Container **cannot** run it: it skips `tls-intercept` loopholes
entirely because `--add-host` is unsupported
(`internal/loopholes/runtime.go:37`; [loopholes.md](../guides/loopholes.md)
step 3). macos-user **skips it by default** — see §5.

### 2.6 Git-identity composition (host-composed, never a wallet)

Git identity is a **two-key allowlist** — `user.name` + `user.email`, plus an
in-jail `core.excludesFile` — never a mount of `~/.gitconfig`
([identity-prism-decision.md](identity-prism-decision.md), IMPLEMENTED
2026-07-23, commit `c250c72`). The rationale is precisely credential hygiene:
inheriting the host gitconfig would drag in `credential.helper`,
`user.signingkey`, `url.*.insteadOf` rewrites (credential leaks) and
`commit.gpgsign` (which *breaks every in-jail commit* with no key present — proven
in §2 of that doc). So none of those are named, hence none cross.

- **Container backends** compose a fresh minimal INI each run from `git config
  --get user.name/user.email` (repo-local value for the host CWD wins) and mount
  it (podman `:ro`; AC materialize) at `~/.config/git/config`
  (`internal/cli/run/assemble_parts.go:210-258`, `composeGitconfig:274-292`).
  Fresh composition each run is what fixes the old add-only staleness bug (a
  cleared host key now vanishes from the jail).
- **macos-user** has no mount namespace, so it forwards the same two keys as
  `YOLO_GIT_NAME`/`YOLO_GIT_EMAIL` (`MacosSandboxEnv`,
  `internal/macosuser/orchestrator.go:99-115`) and replays them imperatively via
  `configureGit` (`internal/entrypoint/identity.go`). A plan invariant asserts
  the identity actually reached the bootstrap env (`runplan.go:238-244`).

### 2.7 Host-service loopholes (secrets that never enter the jail)

The general pattern (`host_services` config; broker is one instance): a host-side
daemon holds the secret and exposes a Unix socket bind-mounted at
`/run/yolo-services/<name>.sock`, with `YOLO_SERVICE_<NAME>_SOCKET` in the launch
env. The agent calls the service but **never sees the raw credential**
([../guides/USER_GUIDE.md:797-921](../guides/USER_GUIDE.md); config-ref
`host_services`). This is the sanctioned way to give a jail scoped access to a
credential without handing it over. **Unavailable on Apple Container** (no
Unix-socket bind through virtiofs — USER_GUIDE:928-930) and **not wired on
macos-user** (the loophole runtime lives in the container launch path;
[macos-user-nix-and-features.md §3.5](macos-user-nix-and-features.md)), though
that doc argues the framework ports cleanly to macos-user in principle.

---

## 3. Worked example — AWS Bedrock (a jail-local credential done right)

Claude-on-Bedrock is the canonical illustration of "jail-local credential, no
host path in". The long-term IAM keys of a tightly-scoped user (`matt-bedrock`,
`bedrock:InvokeModel` only, no session token, **not** SSO) reach the jail through
**`env_sources`**, resolved into `~/.config/yolo-user-env.sh`, which the jail
shell sources — so `CLAUDE_CODE_USE_BEDROCK`, `AWS_REGION`, and the key pair land
in the agent's process env and are inherited by every Bash tool call.

> **Corrected 2026-08-12 (queue row B4).** This paragraph previously said the keys
> live under the `"env"` block of `~/.claude/settings.json` on the host, riding the
> `/ctx/host-claude/` `:ro` mount. **Measured in a live jail: that block is `{}`,
> and the delivery path is — and was — `env_sources`.** Everything after this note
> was accurate; only the mechanism was wrong.
>
> The `settings.json` `env` block is nonetheless **the right target design**: it is
> the one channel that renders at *both* the jail and host notches, which is why
> [`agent-auth-modes.md`](agent-auth-modes.md) §11.2 has a Bedrock auth pack deliver
> its non-secret half there via `config-overlay`. The old sentence described the
> correct mechanism before anything used it.
>
> **Secrets-hygiene cost, tracked as open in `ROADMAP.md` §4e:** `env_sources` values
> land cleartext at 0644 in several agent config files and a prism `last_render`
> sidecar, and on macos-user they ride the process argv (`env -i K=V…`), visible in
> `ps`. Choosing it as the secret channel is a decision, not a default.

yolo itself contains **zero**
AWS code — it does not mount `~/.aws` and does not forward
`AWS_SESSION_TOKEN`/`AWS_PROFILE`. The blast radius of the key leaking inside the
jail is bounded to Bedrock invoke cost on the allowed model ARNs. (Source:
project memory `project_bedrock_creds.md`; the delivery mechanism is verifiable
in `hostclaude.go` + `prism_claude.go`, the IAM scoping is not in-repo.) The
same shape works via `env_sources` for a plain
`ANTHROPIC_API_KEY`/`OPENAI_API_KEY`.

---

## 4. Per-agent matrix — where each agent's creds live and how they get there

Each agent authenticates **itself inside the jail** — yolo wires config, not
auth. Per config-ref `agents`: claude/gemini/copilot/codex use OAuth via their
own `/login` (or `codex login`); opencode/pi/codex also accept a provider API key
via `env_sources`. The agent registry (`internal/agents/agents.go`) pins each
agent's overlay dir(s) and config surfaces; the config surfaces and their
generators are enumerated in
[config-migration-to-prism.md §"Eight persistent surfaces"](config-migration-to-prism.md).

| Agent | Overlay dir(s) (`agents.go`) | Creds/token location in jail home | How creds arrive | Managed config surface | Shared vs per-workspace |
|---|---|---|---|---|---|
| **claude** | `.claude` | `~/.claude/.credentials.json` (symlink → shared dir) | in-jail `/login`; **host-shared** via broker/shared-creds dir | `~/.claude/settings.json` (prism-composed) + `~/.claude.json` | **host-shared** creds (see below) |
| **copilot** | `.copilot` | under `~/.copilot/` | in-jail OAuth `/login` | `~/.copilot/{mcp-config,lsp-config,config}.json` | per-workspace overlay (see §4 note) |
| **gemini** | `.gemini` | under `~/.gemini/` (logs under `~/.cache/gemini-cli/`) | in-jail `gemini login` | `~/.gemini/settings.json` (+ MCP sidecar) | per-workspace overlay |
| **opencode** | *(none)* | `~/.config/opencode/` (+ XDG data) | in-jail login / API key via `env_sources` | `~/.config/opencode/opencode.json` | per-workspace via the `.config` overlay |
| **pi** | `.pi` | under `~/.pi/agent/` | provider API key via `env_sources`; host `settings.json` (`AgentSpec.HostFiles`) | `~/.pi/agent/settings.json` (prism-composed) | per-workspace overlay |
| **codex** | `.codex` | under `~/.codex/` | `codex login` or `OPENAI_API_KEY` via `env_sources` | `~/.codex/config.toml` (+ MCP sidecar) | per-workspace overlay |
| *(agy)* | `.gemini/antigravity-cli` | under `~/.gemini/antigravity-cli/` | shares `~/.gemini` tree, own subdir | `~/.gemini/antigravity-cli/{settings,mcp_config}.json` | per-workspace overlay |

Notes and mechanics:

- **Overlay dirs are per-workspace and seeded once.** For each selected agent,
  `<workspace>/.yolo/home/<subdir>` is bind-mounted over `/home/agent/.<subdir>`
  (`assemble.go:162-164`); `prepareWsState` seeds it by copying **top-level
  regular files only** (the auth tokens) from `GLOBAL_HOME/.<subdir>`, never
  overwriting (`seedAgentDir`, `internal/cli/run/storagehelpers.go:37-64`;
  `prepare.go:168-171`). opencode has no overlay dir, so its state rides the
  per-workspace `.config` overlay (`assemble_parts.go:74`) — there is no bespoke
  opencode credential handling in-repo (`env.go:215-216` only names the dir).
- **Claude is the one host-shared credential.** Only Claude gets the separate
  `.claude-shared-credentials` rw mount (`assemble.go:166-168`) + relative
  symlink (`claude.go:161-187`) so a single OAuth identity is shared across all
  jails on a host, and `claudejson.go` back-propagates the `oauthAccount` login
  state to the shared seed (`jail-home.md §4.3`). No other agent has a
  write-back-to-`GLOBAL_HOME` path in the code.
- **⚠ Discrepancy to resolve (not invented — flagged):** the USER_GUIDE
  (`:176`) states "on podman a `/login` in any jail propagates to every other
  jail automatically," and config-ref (`:667`) says "GitHub CLI (gh) is
  pre-authenticated via the shared home." I could **not** confirm a
  cross-jail-propagation path for **non-Claude** agents (copilot/gemini/gh) in
  the current per-workspace-overlay code: overlay dirs and `.config` are
  per-workspace, seeded one-way from `GLOBAL_HOME` and never written back
  (`seedAgentDir` copies FROM `GLOBAL_HOME`; nothing in the run/boot path writes
  agent tokens INTO `GLOBAL_HOME` except Claude's shared-creds mechanism). Either
  the prose predates the per-workspace-overlay layout (v2) and is now stale for
  non-Claude agents, or there is a seeding/promotion step I did not locate. Worth
  a maintainer check before relying on it.
- **History is isolated per host-workspace even when the home is shared:** Claude
  `history.jsonl` is keyed on `sha256(YOLO_HOST_DIR)` (`jail-home.md §4.4`).

---

## 5. Per-backend / per-OS differences

macos-user's defining property for credentials is its **single shared home**:
every session runs as the one hidden user `_yolojail` with home
`/Users/_yolojail` (`internal/macosuser/macosuser.go:48-49`). There are **no
mounts** (`macos-user-nix-and-features.md §"the one thing"`), so every
credential surface that is a mount on containers is either an imperative replay
or "just the real home". The security consequence: **all concurrent macos-user
sessions share one credentials file per agent** — the shared home *is* the
sharing mechanism, with no per-workspace isolation and no per-session
separation. The boundary is enforced not by absent mounts but by the Seatbelt
profile, which is `(allow default)` then denies reads under `/Users` (re-allowing
only the sandbox home + the neutral workspace) and denies `/Library/Keychains`
(`internal/macosuser/seatbelt.go:41-54`) — so host users' homes and the login
keychain are unreadable, but the network is fully open (`(allow default)`).

| Credential mechanism | podman (Linux/macOS) | container (Apple Container, macOS) | macos-user (macOS) |
|---|---|---|---|
| Host cred propagation | none (not mounted) | none (not mounted) | none (Seatbelt read-denies `/Users/*`, `/Library/Keychains`) |
| git identity | composed file, `:ro` bind at `~/.config/git/config` (`assemble_parts.go:253-255`) | composed file, **materialized** (no nested `:ro`) (`:247-251`) | `YOLO_GIT_*` env → `git config --global` replay (`identity.go`) |
| global gitignore | `:ro` bind (`:231-237`) | materialized (`acMaterialize`) | `YOLO_GLOBAL_GITIGNORE` env → replay |
| `env_sources` / `${VAR}` | `yolo-user-env.sh` mounted, sourced | `yolo-user-env.sh` **materialized**, sourced | baked onto launch argv via `env -i` (`runplan.go`) |
| host `~/.claude`/`~/.pi` settings | `/ctx/host-*/settings.json` `:ro` mount + boot compose (`hostclaude.go`) | materialized copy + boot compose | boot compose, fail-open (no `/ctx`; same pure generators; `macos-user-nix-and-features.md Part 2`) |
| user `host_files` (§2.4) | source-bearing: `/ctx/host-user/<slug>` `:ro` mount; source-less: composed from `content`/`defaults` | ⚠ single-file `:ro` unhandled for `/ctx/host-user` (apple/container#1089); source-less entries compose fine | **source-less ONLY** (`SourceLessHostFiles`) — no `/ctx` to carry a source into, so a source-bearing entry is skipped rather than silently rendering without its host layer |
| Claude shared credentials | `.claude-shared-credentials` rw bind + symlink (`assemble.go:166-168`) | **not mounted** — AC uses one whole-home bind; creds live in that per-workspace home | **free** — one real `~/.claude/.credentials.json` in the shared home |
| claude-oauth-broker | ✅ active when host `claude` present | ❌ **skipped** — it declares `intercepts`, which need `--add-host` (`runtime.go`) | **skip by default** — shared home already = one creds file; refresh serialization only bites with *concurrent* Claude sessions and needs hard-to-port host redirection (`macos-user-nix-and-features.md §3.5`; `EndpointGrantCommands` exists but is uncalled) |
| host-service loopholes (secret brokers) | ✅ loopback-TLS endpoint file + `YOLO_SERVICE_*_ENDPOINT` | ⛔ **deferred, not blocked** — the transport no longer needs a Unix-socket bind, but how the endpoint file crosses into an AC guest is an unmade mount decision (`loophole-transport.md` §6.5) | not wired (container-path only); framework ports in principle |
| per-workspace cred isolation | ✅ (`.yolo/home` overlay per workspace) | ⚠ single whole-`wsState` home bind, still per workspace, but `.claude` shared across workspaces there → history keyed by host dir (`jail-home.md §4.4`) | ❌ **one shared home for ALL sessions** |
| isolation boundary | VM (macOS) / userns (Linux) + read-only root | VM + read-only root | Unix user + Seatbelt — **weaker, documented** (`macos-user-nix-and-features.md Part 4`) |

Linux vs macOS on the **container** backends is mostly a runtime-flag story, not
a credential story: the credential mechanisms above are identical for podman on
Linux and podman on macOS (Podman Machine). The real axis is **container vs
macos-user**, because the mount layer is what most credential delivery rides on.

---

## 6. Security model & residual gaps

**What a live agent session CAN reach:** the workspace; its own writable home
overlay; whatever `env_sources`/host-`settings.json` creds a human put in;
Claude's shared OAuth token (Claude only); the open network; and, through
loopholes, host-mediated services (never their raw secrets). **What it CANNOT
reach:** the host's `~/.ssh`, `~/.gitconfig`, `~/.aws`, gh/cloud tokens, other
host users' homes, the login keychain — none are mounted (containers) or all are
Seatbelt-denied (macos-user).

**What a *compromised prior* session could tamper with** (the sharper question):

- **Shared, mutable surfaces.** `~/.claude-shared-credentials`, `~/.cache`,
  `/mise` are rw and host-shared (`jail-home.md §4`, gotcha 5). A prior session
  can poison a shared cache or the shared Claude creds file. The shared-home
  render fight is the reason agent-config writes are convergent/single-writer.
- **The macos-user host-side nix build — the trust-boundary inversion.** On
  macos-user, provisioning runs a host-side `nix build` **as the invoking user,
  unconfined**, with inputs (`packages:`, the resolved `repoRoot` flake) a prior
  `_yolojail` session could have written. This is the one place a prior session's
  bytes flow back to a *more* privileged context. Full analysis in
  [macos-user-build-step-threat-model.md](macos-user-build-step-threat-model.md):
  reachable outcomes are `_nixbld`-level code exec, host-env exfil via `--impure`,
  and substituter poisoning via `--accept-flake-config` (trusted-user gated); the
  sharpest vector is a `flake.nix`+`go.mod` pair planted at the workspace root
  hijacking `repoRoot` with **no config-diff prompt** to flag it. Container
  backends never trigger a host-user build, so they don't have this inversion.
- **Config-diff prompt gap on macos-user.** The y/N config-change approval that
  guards a poisoned `packages:` edit is called only in the container launch path,
  so it is **not currently reached on macos-user** — the worst place to lose it,
  since that is where the unconfined build runs. Decided-to-fix (roadmap A1;
  `macos-user-nix-and-features.md §3.6`).
- **Residual documentation gap (§4):** the "creds shared across all jails" prose
  is confirmed only for Claude in code; treat non-Claude cross-jail propagation
  as unverified.

The overarching model is [security-shim.md](security-shim.md)'s "unprivileged
UID, no host credentials" on containers, weakened deliberately to "Unix user +
Seatbelt, shared home" on macos-user in exchange for no-VM speed
([macos-no-vm-direction.md](macos-no-vm-direction.md)).

---

## Where things live

| Topic | Authority |
|---|---|
| The credential-boundary statement (canonical prose) | `internal/agents/agentsmd.go:186-195`; every jail's `AGENTS.md`/`CLAUDE.md` |
| git-identity policy + mechanism | [identity-prism-decision.md](identity-prism-decision.md); `internal/cli/run/assemble_parts.go` (`gitIdentityMountArgs`), `internal/entrypoint/identity.go` |
| `env_sources` semantics + `${VAR}` | config-ref `env_sources`; `internal/config/envsources.go`; [mcp-configuration.md §2](mcp-configuration.md) |
| Host agent-file set (credential boundary) | `internal/agents/agents.go` (`AgentSpec.HostFiles`); `internal/cli/run/hostclaude.go` (`hostFileArgs`); `internal/entrypoint/{prism_claude,prism}.go` |
| Claude OAuth broker | [../guides/loopholes.md](../guides/loopholes.md), [loophole-protocol.md](loophole-protocol.md); `bundled_loopholes/claude-oauth-broker/`; `internal/broker`, `internal/oauthbroker` |
| Shared Claude credentials + symlink/harvest | `jail-home.md §4.2`; `internal/entrypoint/claude.go:161-209`; `internal/storage/ensure.go:69-80` |
| Home composition, overlays, per-agent dirs | [jail-home.md](jail-home.md); `internal/agents/agents.go`; `internal/cli/run/{assemble,assemble_parts,prepare,storagehelpers}.go` |
| macos-user shared home + Seatbelt | [macos-user-nix-and-features.md](macos-user-nix-and-features.md), [../guides/macos.md](../guides/macos.md); `internal/macosuser/{macosuser,seatbelt,orchestrator,runplan}.go` |
| macos-user build-step threat model | [macos-user-build-step-threat-model.md](macos-user-build-step-threat-model.md) |
| Host-service secret brokers | [../guides/USER_GUIDE.md](../guides/USER_GUIDE.md) "Host services"; config-ref `host_services` |
| State separation / persistence | [storage-and-config.md](storage-and-config.md); `jail-home.md §4` |
