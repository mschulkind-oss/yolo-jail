---
status: current
verified: 2026-09-09
verified_commit: 41dde711
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
  - internal/macosuser/seatbelt.go
  - packs/claude/loopholes/claude-oauth-broker/
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

- **A second copy of the OAuth broker daemon *is* the race it exists to prevent.** One daemon per
  machine, ensured under a host-wide flock, never spawned per jail — see
  [the broker](#the-claude-oauth-broker).

- **`host_files` cannot reach a yolo-owned destination.** No entry may target a path yolo owns as
  a single file or symlink, nor any yolo-composed agent surface (`hostFileReservedDests`), so the
  channel cannot be used to overwrite `~/.claude/settings.json` and strip its managed block.

- **Only Claude has a write-back path to the global home.** Every other agent's overlay dir is
  seeded one-way from the global home and never written back.

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
the single source of truth and the entrypoint never re-reads config. `macos-user` carries only the
source-less entries: with no bind mounts there is no `/ctx/host-user` to carry a source into, and
a source-bearing entry is **skipped** rather than silently rendering without its host layer.

### The Claude OAuth broker

The broker exists because **Anthropic mints single-use refresh tokens**: if two jails share one
credentials file and both refresh in the same window, one loses the race and gets logged out. It
bundles two jobs.

1. **One shared credentials file per host.** On containers that is a shared-credentials bind plus
   an in-jail *relative* symlink from the agent's own credentials path into it
   (`linkThroughShared`, applied through the pack's `shared_credentials` hook). One OAuth
   identity, every jail.
2. **Serialize the refresh HTTP call.** A host-side **singleton** daemon holds a flock
   (`oauthbroker.RefreshLockPath`) around every refresh, so concurrent jails cannot burn the
   token. Because Claude Code refreshes *itself* and will never voluntarily take yolo's lock, an
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

Each agent authenticates **itself inside the jail** — yolo wires config, not auth. The agent packs
use OAuth through their own login flow; several also accept a provider API key through
`env_sources`. Each pack's manifest pins that agent's overlay dirs (its `state` contributions) and
its config surfaces; `packs/*/pack.json` is the enumeration, and
[`pack-system.md`](pack-system.md) is how to read one.

Two asymmetries are the load-bearing part, and neither is visible from a per-agent table:

- **Overlay dirs are per-workspace and seeded once.** For each selected agent,
  `<workspace>/.yolo/home/<subdir>` is bound over the corresponding dir in the jail home, and the
  prepare step seeds it by copying **top-level regular files only** — the auth tokens — from the
  global home, never overwriting (`seedAgentDir`). An agent pack that declares no `state` dir
  rides the per-workspace `.config` overlay instead.
- **Claude is the one host-shared credential.** Only Claude gets a separate read-write
  shared-credentials mount plus the relative symlink, so a single OAuth identity is shared across
  every jail on a host, and only Claude has a write-back path that propagates login state to the
  shared seed. History stays isolated per host workspace even when the home is shared, because the
  history file is keyed on a hash of the host directory.

> [!WARNING]
> **Cross-jail credential propagation is a Claude-only property in the code.** Nothing in the run
> or boot path writes agent tokens *into* the global home except Claude's shared-credentials
> mechanism, so a login in one jail does not reach another for any other agent. Do not generalize
> the Claude behavior when reasoning about, or documenting, another agent.

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
| User `host_files` | source-bearing: `/ctx/host-user/<slug>` `:ro`; source-less: composed | source-less composes; single-file `:ro` for `/ctx/host-user` unhandled upstream | **source-less only** |
| Claude shared credentials | shared bind + relative symlink | not mounted — one whole-home bind, so creds live in that per-workspace home | free — one real credentials file in the shared home |
| claude-oauth-broker | active when the `claude` pack is selected | **skipped** — it declares `intercepts`, which need `--add-host` | skipped by default; the shared home is already one creds file |
| Host-service loopholes | endpoint file + `YOLO_SERVICE_*_ENDPOINT` | how the endpoint file crosses into an AC guest is an unmade mount decision | not wired — the loophole runtime lives in the container launch path |
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

> [!WARNING]
> **The launch-time config-change approval is not reached on `macos-user`.** `CheckConfigChanges`
> has exactly one caller, in the container launch pre-flight, so the y/N prompt that would flag a
> poisoned `packages:` edit does not run on the backend where the unconfined host-side build
> happens — the worst place to lose it. Anything relying on that prompt as a control must not
> assume it on that backend.

## What this does not license

- **Not a config knob over the per-agent host-file set.** That is the credential boundary; a
  workspace config widening it is exactly the hole the retired per-agent keys were.
- **Not a deny-list.** Nothing is stripped, because nothing is added. Adding a filter would imply
  the host home is a mount source, which is the property being preserved.
- **Not AWS, cloud-profile or SSO integration.** yolo contains no cloud-provider code: it does not
  mount a cloud credentials dir and does not forward session tokens or profile names. A jail-local
  key arriving through `env_sources` is the whole mechanism, and its blast radius is whatever that
  key is scoped to.
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

Verified at `41dde711`. The prose above explains what each of these is for; this table is the only
place the values themselves are stated.

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| Per-agent host grant mount | `/ctx/host-<pack>/<file>`, `:ro` | `internal/cli/run` (`hostFileArgs`) |
| User host-file mount | `/ctx/host-user/<slug>`, `:ro` | `internal/cli/run/hostfiles.go` (`hostUserFileArgs`) |
| Resolved `env_sources` file | `~/.config/yolo-user-env.sh`, `export K=${K:-'v'}` lines | `internal/cli/run/userenv.go` |
| Jail service dir | `/run/yolo-services/` | `internal/svcendpoint`, `internal/loopholes` |
| Endpoint file | `<name>.endpoint`, mode `0600`, named by `YOLO_SERVICE_<NAME>_ENDPOINT` | `internal/svcendpoint` |
| Declared-service socket | `<name>.sock`, named by `YOLO_SERVICE_<NAME>_SOCKET` | `internal/loopholes` |
| Broker refresh lock | `oauthbroker.RefreshLockPath` | `internal/oauthbroker/refresh.go` |
| Broker credentials file | the shared-credentials dir under the global home | `internal/storage` (`ensure.go`), `internal/entrypoint/claude.go` |
| Broker daemon | `yolo internal daemon claude-oauth-broker`, `scope: "host"` | `internal/broker`; `packs/claude/loopholes/claude-oauth-broker/manifest.jsonc` |
| Git identity keys carried | `user.name`, `user.email`, plus an in-jail `core.excludesFile` | `internal/cli/run` (`composeGitconfig`), `internal/entrypoint/identity.go` |
| `macos-user` identity replay vars | `YOLO_GIT_NAME`, `YOLO_GIT_EMAIL` only — `YOLO_GLOBAL_GITIGNORE` is read by the entrypoint and **set by nothing**, so the global gitignore does not replay on this backend | `internal/macosuser` (`MacosSandboxEnv`), `internal/entrypoint/identity.go` |
| Config keys in this story | `env_sources`, `host_files`, `host_services`, `loopholes` | `yolo config-ref` |
