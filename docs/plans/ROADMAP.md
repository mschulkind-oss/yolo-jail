# ROADMAP — sequencing the active plans

**Date:** 2026-07-22. **Purpose:** one ordering for everything under
`docs/plans/`, so "what do I work on next?" has a single answer. Every
dependency below was checked against the plan docs **and** the code they cite
(re-verified 2026-07-22, after the agent-config prism cutover + agy landed and
the cache-relocation host acceptance step was discharged) — where a claim rests
on the tree, the file:line is named.

## Open work at a glance

Everything not marked done below reduces to **four** open items. In priority /
lane order:

| # | Open item | Lane | Blocker |
|---|---|---|---|
| 1 | **config-composition — non-agent surface ports** (mise, standalone MCP/LSP, git identity onto the prism, then delete their bespoke generators) | jail-side | none — the main remaining agent-completable thread |
| 2 | **D4 Cachix** (one Mac download proof) | hardware-gated | substituter enabled + account/cache/CI-push all done (2026-07-22); needs only a real Mac to prove the download path |
| 3 | **composed-file follow-ups** — the deferred tail of `host_files` + two pre-existing prism defects (see below) | jail-side | none; each item is independently shippable |
| 4 | **agent auth — capture the model, fix the asymmetry** (Claude broker rationale, the six un-investigated agents, macos-user parity, 4 verified defects) | jail-side (macos-user half is Mac-gated) | none for the audit/docs half; the macos-user fixes need a Mac to verify |

### Item 3 — composed-file follow-ups

[host-file-staging.md](host-file-staging.md) **shipped** 2026-07-25 and is closed to
new scope; its "Scope: the line" section is the authoritative in/out list. The items
it pushed out land here. Nothing here is a half-working feature — every deferral is
either a validation error or a documented limit in `yolo config-ref` — so these are
improvements, not repairs, **except** the two defects at the end.

| Sub-item | Why it is deferred | Notes |
|---|---|---|
| **Comment preservation on a `json`/`toml` surface** | needs a decision, not just code | Reasoning + ranked options are parked in host-file-staging.md; the three sub-questions (staleness, attachment, in-jail additions) are already answered there, so this starts from decisions. Cheapest useful step is a yolo-authored header pointing at the `:ro` original — no comment parsing at all. `raw` is already lossless today. |
| **Collapse the four `host_files` modes to three** | behavior change on a shipped key | [composed-file-permissions.md §7.4](../design/composed-file-permissions.md): `copy` merges into `readonly`, and `readonly` becomes a real `:ro` mount instead of `0o444` — which is *asymmetric* (root ignores it; a non-root agent gets EACCES and the surface silently stops re-rendering). |
| **Rename the recovered state** | mechanical, wants one pass | Four terms for one concept; "captured edits" is the proposed user-facing umbrella. NOT "managed" — that already means keys *yolo* wins. |
| **`managed`/`defaults` array-append pinning** | no user surface has needed it | Object merge only today; shape-checked at config time so it fails loudly, not silently. |
| **⚠ `copilot/config` can lose an OAuth token** | **a real defect** | Renders statefully with `Defaults: {"yolo": true}` and no host layer, so an absent/corrupt sidecar reduces a token-bearing file to one key. Steady state recovers, which is why it went unnoticed. [§4.2](../design/composed-file-permissions.md). |
| **⚠ Reserved destinations miss symlink targets** | **a real gap** | `~/.config/git/config`, `~/.config/bashrc`, `~/.claude/claude.json` validate while their aliases are rejected. [§4.5](../design/composed-file-permissions.md). |

The two ⚠ rows predate `host_files` and were surfaced by the audit in
[composed-file-permissions.md](../design/composed-file-permissions.md); they are the
only rows here that fix something broken rather than extend something working.

### Item 4 — agent auth: capture the model, fix the asymmetry

**Why now.** Auth is the most incident-scarred subsystem in the repo (~35
incident-driven commits, 2026-02 → 2026-07) and simultaneously the least evenly
covered: Claude has a whole host daemon, an in-jail TLS terminator and two research
writeups; the other six agents have *nothing but a one-way file copy*. That
asymmetry has never been examined, and a 2026-07-25 audit found it is **investigative,
not architectural** — the strongest single piece of evidence was in a live jail, not
the code: **gemini's `~/.gemini/oauth_creds.json` carries a `refresh_token` and
`expiry_date`** (verified), the exact shape whose rotation is the entire reason Claude
needs a broker, and the repo mentions a non-Anthropic refresh token **zero times**.

Three things make this a real item rather than a tidy-up: the broker's reasoning is
scattered across two research docs, a README and ~35 commit messages and is
**actively contradicted by two shipped user-facing docs**; macos-user is expected to
change and has a confirmed auth bug today; and the audit turned up **four verified
defects**, one of them a credential exposure.

#### 4a. Capture the Claude broker rationale (docs; do this first)

The *why* exists but is not collected anywhere, and the parts that are written down
are partly wrong. Consolidate into one design doc (proposed
`docs/design/agent-auth.md`) so a future reader can defend or redesign it:

- **The root problem.** Anthropic mints **single-use refresh tokens** — each
  successful refresh rotates the token and invalidates the one presented. Two clients
  sharing one `.credentials.json` means whichever refreshes first burns the other.
  `internal/oauthbroker/refresh.go:31-36` names the invariant: *"The flock is the
  load-bearing single-use-refresh-token serialization contract. We must NOT silently
  proceed unlocked (that would let concurrent jails burn the token)."*
- **Why it is fatal rather than annoying.** Claude Code keeps an in-process set
  (`T86`) of refresh tokens that ever returned `invalid_grant`. It is **never
  cleaned, not persisted, and invisible to every log yolo can see**, so *one* lost
  race permanently disables refresh for that process's lifetime — the user sees
  `/login` and the broker log explains nothing
  ([claude-oauth-refresh-mechanics.md](../research/claude-oauth-refresh-mechanics.md) §2, §3.4).
- **The three failure paths that look identical** (§4 of the same doc): **A** Claude
  is idle and has *no proactive refresh at all* for Pro/Max tokens; **B** a transient
  broker error (Cloudflare 1010 read as `invalid_grant`, a socket desync) poisons
  `T86` forever; **C** the cross-jail single-use rotation race. One fix collapses all
  three: a host-side loop keeping the **disk file** ahead of expiry, because Claude's
  401-recovery re-reads disk and adopts a differing access token *without consulting
  `T86` and without any HTTP refresh*. That is why a 60-second proactive refresher is
  architectural and not a band-aid.
- **Why host and jail identities are deliberately SPLIT.** The 2026-04-23
  `invalid_grant` incident was host Claude and jail Claude sharing one token chain.
  `cb6e850` removed the mirror; divergence is now *the design*. Re-converging
  reintroduces Path C with a racer the flock can never reach, because host Claude
  refreshes natively and will never take the lock. Instructive tail: that fix was
  **half a fix for nine days** — the entrypoint's reverse HOST→SHARED copy survived
  and re-converged identities at every boot (`8ce6f47` → `927723d`).
- **Why the architecture has three processes.** The terminator exists because Claude
  refreshes *itself* and will never take our lock, and binds `:443`, which is only
  unprivileged inside the container netns. The per-jail relay exists because
  bind-mounting the singleton's *socket file* pinned a dead inode across broker
  restarts; the relay dials per connection and also stamps `jail_id` host-side so log
  attribution is not an in-jail self-report.
- **The on-disk shape constraint.** Claude ≥ 2.1.200 treats a creds file carrying only
  the token trio as *not logged in*; `scopes`/`subscriptionType`/`rateLimitTier` are
  load-bearing, so no writer may strip them. Both writers (broker `NormalizeOAuth`,
  entrypoint `harvestCredentialsFile`) preserve metadata deliberately.
- **The honest cost.** 3 process kinds (N+2 for N jails), 3 sockets per hop, 5 PID/lock
  paths, 4 log sinks, a frozen-contract list (timings, UA, atomic-write recipe,
  `Accept-Encoding` stripping both ways), an `openssl`-exec'd 10-year CA, and a
  teardown guard stack whose order may not change. A redesign should weigh this
  honestly — and know that **a complete non-MITM alternative exists**: an
  `apiKeyHelper`-based credential broker (−4406/+129 lines) was built and live-tested
  on a fork (`4b84ea8`, never merged; `git merge-base --is-ancestor` confirms it is
  not in `main`). **No evidence found** in `main` of a rationale for rejecting it —
  the two lines developed in parallel and the fork simply ended.

#### 4b. The six un-investigated agents

| Agent | Creds land | Scope | What yolo does |
|---|---|---|---|
| **claude** | `~/.claude/.credentials.json` → relative symlink into `~/.claude-shared-credentials` | **host-shared** | broker + terminator + harvest + `claude.json` back-propagation |
| copilot | `~/.copilot/config.json` (holds a live `gho_` token) | per-workspace | ⚠ *composed by the prism* — can be wiped (item 3) |
| gemini | `~/.gemini/oauth_creds.json` (**has a `refresh_token`**) | per-workspace | one-way seed only |
| pi | `~/.pi/agent/` | per-workspace | one-way seed only |
| codex | `~/.codex/` | per-workspace | one-way seed only |
| agy | `~/.gemini/antigravity-cli/` | per-workspace | one-way seed only |
| opencode | `~/.config/opencode/` | per-workspace, **no inheritance at all** | nothing — it has no `OverlayDirs` entry, so it rides the `.config` overlay, which is created but never seeded. An accident, not a decision. |

`seedAgentDir` copies top-level regular files **from** the `:ro` GlobalHome base into
the workspace overlay, never overwriting and never writing back
(`internal/cli/run/storagehelpers.go:37-64`). So a `/login` in one workspace is
invisible to every other workspace for all six. **The work:** determine for each
whether its refresh token rotates (gemini/Google first — highest-value unknown),
whether `codex login` even writes a file (none exists in a jail where codex is
selected), and then decide per agent between "leave it per-workspace", "seed
bidirectionally like claude", or "needs serialization".

#### 4c. macos-user auth — one confirmed bug, then parity

macos-user is one shared native home (`/Users/_yolojail`) for **every concurrent
session and every workspace**, no container, no bind mounts. Consequences:

- **⚠ Confirmed bug: Claude's credentials symlink is DANGLING there.**
  `ensureCredentialsSymlink` runs unconditionally in `RunDarwinBootstrap`, but the
  target dir is provisioned only by `storage.EnsureGlobalStorage` + the container bind
  mount — neither of which runs on macos-user (verified: no `claude-shared-credentials`
  reference on any macos-user path). Claude's own `open(O_CREAT)` on it then fails
  ENOENT. The M1 runbook's login step was optional and never run, so this never
  surfaced.
- **The broker is unwired, by decision.** `BrokerSocketGrantCommands`
  (`internal/macosuser/macosuser.go:319`) exists with **zero call sites**. The recorded
  reasoning — the shared home already gives one creds file — is sound for *sharing*
  but explicitly does **not** address serialization, and macos-user gets neither the
  flock nor the proactive refresher. Since nothing prevents two concurrent `yolo`
  runs there, the "defer the concurrent case" decision may be deferring the common
  case. `git 84d0365` already sketches the port: no relay, no mount — just
  chgrp/chmod on the singleton socket + parent so the sandbox uid connects directly
  and `getpeereid` attests a real uid.
- **Bedrock creds do not reach a macos-user jail.** The worked example in
  agent-credentials.md §3 rides the `/ctx/host-claude` mount, which does not exist
  there, so the surface fails open to defaults. The doc notes the fail-open in its §5
  table but never connects it to §3.
- **env_sources secrets are on the process argv** (`env -i K=V…`), i.e. visible in
  `ps` to every user on the Mac — and they reach the *launch* argv but not the
  *bootstrap* argv, so MCP `${VAR}` interpolation and `requires_env` gating silently
  drop every secret-gated MCP server.
- The `/Library/Keychains` deny is effectively free: all seven agents are file-based,
  and nothing in the repo calls any Keychain API.

#### 4d. Verified defects found by the audit

| Defect | Evidence |
|---|---|
| **⚠ Broker CA *private key* is readable in-jail** | The whole loophole state dir crosses `:ro`, so `/var/lib/yolo-jail/loopholes/claude-oauth-broker/ca.key` is readable by the UID-0 agent (verified: `-----BEGIN PRIVATE KEY-----`). Only `server.crt`/`server.key` are needed in-jail — `ca.key` is used solely host-side by `cert.go`. Combined with `NODE_EXTRA_CA_CERTS` trusting that CA, a jail process can mint a trusted leaf for **any** host. Fix is narrow: mount only the two server files. |
| **⚠ Claude creds symlink dangles on macos-user** | see 4c |
| **⚠ Config-approval snapshot is agent-writable** | `.yolo/config-snapshot.json` is mode 664 inside the workspace and writable in-jail (verified). An agent that edits `yolo-jail.jsonc` *and* matches the snapshot makes the launch-time diff prompt disappear — the exact bypass [config-safety.md](../design/config-safety.md) exists to prevent, and it is undiscussed. |
| **Two shipped docs contradict the code** | `bundled_loopholes/claude-oauth-broker/README.md:59` and `docs/guides/USER_GUIDE.md:182` both say *"no background timer / no proactive refresh"*, but `oauthbrokercmd.go:88` starts `RunBackgroundRefresher` by default — and that refresher **is** the architectural fix for all three logout paths. USER_GUIDE:186 additionally still describes the host-creds mirror that `cb6e850` deleted (`--host-creds-file` is gone from the Go code entirely), and README:3 links a plan doc deleted by `5eb1643`. |

#### 4e. Open questions the maintainer must decide

- **Does Google rotate gemini's refresh token?** The single highest-value unknown; it
  decides whether the broker's problem class is Claude-specific or general.
- **Keep the TLS-MITM architecture, or revisit `apiKeyHelper`?** A working
  alternative exists on an abandoned fork and `main` records no rejection rationale.
- **Is per-workspace credential isolation a feature or an accident?** It is currently
  both — deliberate for claude (shared) and incidental for the rest (per-workspace),
  with opencode getting neither by oversight.
- **Should env_sources secrets be redactable?** They land cleartext at 0644 in five
  agent config files, a prism `last_render` sidecar, and Claude session transcripts.
  There is no redaction concept anywhere in the design docs.
- **Who is the adversary?** The code reasons consistently about a config-writing agent
  (confused or prompt-injected) and never about a malicious npm/MCP dependency reading
  `~/.config/yolo-user-env.sh`. Naming this would settle several of the above.
- **Are the reverse-engineered mechanics still true?** Pinned to Claude 2.1.143/2.1.201;
  today is later. §7 supplies a re-verification recipe that has not been re-run.

#### 4f. Testability

Auth is the worst-covered subsystem at the highest-risk moment: the Go port kept the
broker's *logging* contract byte-faithfully (logs are what made incidents
diagnosable) while dropping ~46 of 47 behavioral tests, so **refresh semantics have
essentially zero coverage**. AGENTS.md forbids tests that start an agent
interactively or make API calls, but that leaves plenty testable with a fake upstream:
flock serialization under N concurrent callers, the 90 s cache-headroom decision, the
metadata-preservation invariant, `NormalizeOAuth` key-order stability, the
transient-vs-permanent retry classification, and the relay's `jail_id` stamping and
drain-before-close semantics. Untestable without hardware: the real OAuth flow, and
**how an interactive login actually completes** — there is *zero* repo evidence on
that for any backend (no `BROWSER`, no `xdg-open`, chromium is headless-MCP-only, and
the runbook's login step was optional and never recorded as run).

Not on this list because they are **done or held**: J1–J3, D1/D2/D3, Track M
M0–M2, module-consolidation, the agent-config prism cutover, agy, **the VM-builder
removal** (`internal/builder` + the `yolo builder {setup,start,stop,status}`
commands deleted and `yolo check` rewired onto the container builder, 2026-07-23 —
revival plan Open Decision #3, RESOLVED; the container builder is now the sole
builder, see [linux-builder-lifecycle.md](../design/linux-builder-lifecycle.md)),
**and nix-ld**
are all **done** (nix-ld shipped Variant A on 2026-07-22 — `e05666a`/`1d614e1`/
`d38463a`/`d6d2e65`/`c434f35`; only a host-gated `env -i` acceptance matrix
remains before `just load`); `cache-relocation` work items 1–10 are **done**
(host acceptance step discharged 2026-07-22) and item 11 (`yolo cache relocate`)
is **held** pending a level-of-abstraction design question, not scheduled.

This is a **meta-doc**: it sequences the plans, it does not restate them. Each
plan remains the source of truth for its own work items. The
[macos-revival-and-distribution-plan.md](macos-revival-and-distribution-plan.md)
is the roadmap of record for the macOS/distribution effort (Tracks J/D/M); this
ROADMAP reconciles with its internal "Sequencing at a glance" and folds the
post-Go-port backlog (nix-ld, color audit, consolidation) into the same picture.

## The plans

| Plan | One-liner | Lane / status |
|---|---|---|
| [macos-revival-and-distribution-plan.md](macos-revival-and-distribution-plan.md) | Tracks J (Linux-jail fixes), D (distribution/source-access), M (Mac hardware). Roadmap of record. | **J1–J3 + D1/D2/D3 done, Track M M0/M1/M2 PROVEN on HW 2026-07-21; only D4 Mac-download proof remains.** |
| [handoff-cachix-cache.md](handoff-cachix-cache.md) | The revival plan's **D4**: publish the OCI image to a Cachix cache. | human-gated — substituter ENABLED (flake.nix:13-16, 730c258); Cachix account + cache exist and CI has pushed data; only the Mac download proof remains |
| [nix-ld-dynamic-linking.md](nix-ld-dynamic-linking.md) | Replace the `LD_LIBRARY_PATH` whack-a-mole with nix-ld; closes the custom-`mcp_servers` startup gap. | jail-side — **DONE 2026-07-22** (Variant A: custom `nix-ld.overrideAttrs`, `DEFAULT_NIX_LD` baked; MCP-wrapper exports removed; `yolo check` tripwire added). Only a host-gated `env -i` acceptance matrix remains before `just load`. |
| [agent-settings-composition.md](agent-settings-composition.md) | Layered regeneration of any generated config (agent settings, MCP, LSP, mise, identity) + a Lua transform. **Design FINALIZED 2026-07-20.** | jail-side — **agent-config surfaces DONE 2026-07-22: prism is the sole boot config path (gate retired, bespoke writers deleted); non-agent surfaces (mise/MCP/LSP/identity) still to port** |
| [cache-relocation.md](cache-relocation.md) | User-scope-only `cache_relocations` so a huge cold cache subdir can live on other storage; unblinds `prune`/`purge`. Podman behavior proven 2026-07-21; host acceptance discharged 2026-07-22. | **DONE (items 1–10); item 11 held on a design question** |
| [../design/linux-builder-lifecycle.md](../design/linux-builder-lifecycle.md) | Decision record: the two builder mechanisms (VM vs container), why the **VM builder was removed** and the container builder is the sole builder, plus the KEYS-bug diagnosis kept as evidence + a manual unblock. | jail-side (macOS-runtime-gated) — **DONE 2026-07-23**: `internal/builder` + the `yolo builder` commands deleted; `yolo check` Image Build + the run-path failure remedy rewired onto the container builder |
| [cli-color-audit.md](cli-color-audit.md) | Shared rich→ANSI renderer + TTY gate across commands. | jail-side — **DONE 2026-07-22** (renderer consolidated, TTY probe unified, check/doctor leak fixed, all commands classified) |
| [antigravity-agy-support.md](antigravity-agy-support.md) | Support Google Antigravity CLI (`agy`) as a native agent inside `yolo-jail`. | jail-side — **DONE 2026-07-22** (born on the prism; all eight touchpoints landed) |
| [module-consolidation-and-cleanup.md](module-consolidation-and-cleanup.md) | Collapse the parity-era `internal/*` split; drop parity machinery; §4 OSS-hygiene remnants. | **DONE 2026-07-21** (package-merge declined) |

| [integration-parallelism.md](integration-parallelism.md) | Bounded `t.Parallel()` for the container suite (needs per-test GlobalStorage first). | parked (test speed) |
| [runbooks/](runbooks/) | Track M verification procedures (see [Runbooks](#runbooks) below). | hardware-gated |

## Lanes — not everything is one linear sequence

Three lanes run in parallel; only the jail-side lane is a sequence. The other
two are gated on a resource an in-jail agent doesn't have.

- **Jail-side (agent-completable).** Developable and testable from inside a
  jail; `internal/` changes still get a nested-jail sanity run per AGENTS.md.
  With the Jul-21/22 wave landed, the jail-side work left is a single thread —
  the **non-agent config-composition surfaces** (porting mise/MCP/LSP/identity
  onto the prism — the agent-config surfaces and `agy` are done). J2, J3, D2,
  cli-color-audit (now fully DONE — renderer consolidated, TTY probe unified,
  check/doctor leak fixed, all commands classified), module-consolidation, the
  agent-config prism cutover, agy, and **nix-ld** (shipped Variant A 2026-07-22)
  have all landed.
- **Host-gated (needs a human at a host with nix) — for SHIPPING, not
  validating.** A nested `yolo -- bash` rebuilds the flake and runs the new
  image, so a `flake.nix` / image change is fully validated in-jail, runtime
  behavior included (verified 2026-07-22; AGENTS.md "Build & deploy"). What still
  needs a host session is loading the proven image into the maintainer's OWN
  day-to-day jails (`just load`) — that's shipping, and it never blocks jail-side
  development or verification. **nix-ld** is the live example: it is fully
  implemented and in-jail-validated, and its only remaining step is the
  host-gated `env -i` acceptance matrix before `just load`. The one genuinely
  hardware-gated remnant is **D4's Mac download proof** (see the next bullet),
  which needs real Mac hardware, not just a host with nix.
- **Hardware-gated (needs a real Mac).** No in-jail agent can complete these.
  Members: **D4 Cachix** — as of 2026-07-22 the account + cache + CI push are
  done, so only the one Mac download proof remains. Track M's M0/M1/M2 are
  already PROVEN on real Apple Silicon (2026-07-21), so the hardware gate is
  discharged for the current scope.

## Current state — what's already done

Marked here so the "start here" arrow points at the real next item.

- ✅ **J1.1–J1.4** (2026-07-20) — runtime unification, darwinpkg stderr drain,
  builder VM reaping, `yolo --help`. Each RED-then-GREEN; J1.1 nested-jail
  verified.
- ✅ **D1** (2026-07-20) — `just deploy` records `repo_path`; `check` honors it.
  Verified: `internal/repopath/` exists, wired into the install recipe.
- ✅ **D2 — graceful launch degradation** (2026-07-21) — repo-root resolution is
  no longer a hard gate. When it fails the launch proceeds degraded on a
  cached/loaded image (`image.AutoLoadOptions.SkipBuild`), and `Run` prints a
  soft notice instead of exiting 1 (commit 8f1d612). Nested-jail verified both
  paths: normal launch + rebuilds; degraded runs the cached image.
  *(The intermediate `repoBound`-gated `/opt/yolo-jail:ro` bind + `YOLO_REPO_ROOT`
  env described in the original commit were later removed entirely by the
  prebuilt-bundle cutover — 2026-07-23: `/opt/yolo-jail` is now a baked install
  prefix and no `YOLO_REPO_ROOT` is injected into the jail.)*
- ✅ **D3** (2026-07-20) — Go-era source bundle ships so checkout-less installs
  build the image. Verified the staged tree evaluates. *(Superseded 2026-07-23:
  the source-tree/`git archive` bundle + `stageInstalledWheel` staging were
  replaced by the prebuilt "two files and a binary" bundle — `flake.nix` +
  `flake.lock` + prebuilt `bin/linux-<arch>/`, consumed by the flake's prebuilt
  short-circuit with no staging. See `docs/research/repo-root-and-distribution.md`.)*
- ✅ **CI green** (2026-07-20) — the `TestShimPersistence` failure (shim
  mount-anchor / `ClearContents`) is fixed and the four test-merges landed; the
  full CI run (both arches, integration incl.) passed.
- ✅ **cli-color-audit — DONE** (2026-07-20/22) — the shared `internal/richtext`
  renderer landed and `prune`/`builder`/`macosuser`/`broker` (plus the top-level
  `cli`/`config`/`ps` commands) route through it with a TTY gate. The tail closed
  2026-07-22: `internal/cli/run/console.go` migrated onto `internal/richtext`
  (`67454a8`), the two TTY-probe conventions unified onto `internal/tty`
  (`b76b2ba`), a `check`/`doctor` ANSI-leak-to-a-pipe fixed (`c9ea5e8`), and the
  last commands (`loopholes`/`init`/`init-user-config`) classified.
- ✅ **go baked into the image** (2026-07-20) — `imagePkgs.go` in corePackages,
  `miseBaseTools` now empty (all default runtimes baked). Built + evaluated
  in-jail AND green on both CI `build-image` arches.
- ✅ **Cachix D4 substituter enabled** (2026-07-20) — `nixConfig` substituter +
  key live at `flake.nix:13-16` (730c258); the CI push job self-enables once the
  `CACHIX_AUTH_TOKEN` secret exists. Account + first push + Mac download proof
  remain (human-gated).
- ✅ **config-composition Phase A + B** (2026-07-20/21) — engine (`internal/
  agentcfg`) + codecs + real gopher-lua sandbox VM + manifest landed; the
  exported `Compose` orchestrator + `yolo config render <agent> [--surface|
  --explain]` CLI cover every agent surface (pi/claude/gemini/copilot/opencode/
  codex) plus MCP/LSP/mise; `mergeAccumulate` tombstone fix.
- ✅ **config-composition — agent-config surfaces wired + cut over** (2026-07-22) —
  boot (`internal/entrypoint`) and `yolo check` now render the agent-config
  surfaces through `agentcfg` via the `Configure*Prism` writers; the
  `YOLO_PRISM_SURFACES` cutover gate is retired (prism is unconditional), and the
  six bespoke `Configure*` writers plus their dead helpers are deleted. `agy` was
  born directly on the prism. Obsolete snapshot/managed-MCP sidecars self-clean on
  each surface's first-migration boot. **Remaining:** the non-agent surfaces
  (mise/MCP/LSP/identity) still use bespoke generators; `host_*_files` keys stay
  (the prism host layer reads through them).
- ✅ **J2 — native-Go macos-user bootstrap re-port** (2026-07-21) — all four
  items (12d27cb/731dbe5/1e68e24/544a806/e65993a): platform literals threaded
  through `*Env`; darwin-native generation entry + Go writers; `yolo internal
  darwin-bootstrap` self-exec + launch-path swap (fresh-inode staging, Python
  machinery deleted, `RepoSrc`→`RepoRoot`); finding-6 password via stdin. Mac
  runtime behavior verified in **M1** on real hardware.
- ✅ **J3 — container-builder rewiring** (2026-07-21) — resurrected
  `internal/containerbuilder` (8abb67c) and wired the offload into
  `AutoLoadImage` (c2f0b94): a failed macOS from-source build retries over a
  container builder via ssh-ng before falling back. Behavioral e2e is the
  mac-ac-container-builder runbook (PASSED on HW).
- ✅ **module-consolidation-and-cleanup** (2026-07-21) — parity-era comments and
  machinery removed (743e053/d2b2db7/84b3e09); package-merge deliberately
  declined. `Status: DONE`.
- ✅ **Track M M0/M1/M2 — PROVEN on real Apple Silicon** (2026-07-21) —
  macos-user runs the agent under Seatbelt with native aarch64-darwin `packages:`
  (9933e7b/8763fd5/43bd846); OQ-1 (path_helper) and finding-6 (password apply)
  observed and passing. See `docs/research/macos-support-matrix.md`.
- ✅ **mise migration fix** (2026-07-20) — stale unpinned baked-runtime lines
  stripped on upgrade, workspace/injected pins preserved (nested-jail verified).
- ✅ **nix-ld — DONE** (2026-07-22) — Variant A: a custom `nix-ld.overrideAttrs`
  with `DEFAULT_NIX_LD` baked to the real glibc loader is the `/lib64` + `/lib`
  FHS interpreter; the fallback lib dir is the baked non-store
  `/usr/share/nix-ld/lib`; the three MCP-wrapper `LD_LIBRARY_PATH` exports are
  removed (`1d614e1`) and the custom-`mcp_servers` gap closed for free; the baked
  Env + cli `-e` `LD_LIBRARY_PATH` are deliberately kept (`d38463a`) as the
  nix-process dlopen-by-soname path; a `yolo check` FHS-loader tripwire
  (`d6d2e65`) guards regressions. Only the broader host-gated `env -i` acceptance
  matrix remains before `just load`.

Everything else below is **open**: config-composition non-agent surfaces
(mise/MCP/LSP/identity ports) and the D4-download human step. (cli-color-audit
and nix-ld are now fully DONE — see above.)

## Recommended order (jail-side thread)

With J1/D1/D2/D3/J2/J3/consolidation, the agent-config prism cutover, and agy all
done, the jail-side lane has collapsed to two independent items — there is no
longer a critical-path chain:

1. **config-composition — non-agent surface ports** — *the main remaining jail
   work; its own self-contained thread (see below).* The engine drives boot for
   the agent-config surfaces already; what remains is folding the non-agent
   surfaces (mise, standalone MCP/LSP, git identity) onto the prism and then
   deleting their bespoke generators. This is where the real design
   nuance lives — see the [config-composition build](#config-composition-build-own-self-contained-thread)
   section and `agent-settings-composition.md`.

2. ~~**cache-relocation**~~ — **DONE 2026-07-21; host acceptance discharged
   2026-07-22.** Work items 1–10 landed (user-scope-only `cache_relocations`,
   nested rw bind mount, prune/purge accounting, docs) and were verified end to
   end in a nested jail: a write to `~/.cache/<subdir>` inside the jail lands on
   the relocated target and the host-side stub stays empty. The host-gated
   acceptance step is now done too — the maintainer moved a HuggingFace cache to
   cold storage on another machine successfully (2026-07-22). `yolo cache
   relocate` (item 11) is **held**, not merely deferred: the maintainer is not
   sure `cache_relocations` sits at the right level of abstraction and does not
   want a command locked around it until that resolves (see the plan's "Is
   `cache_relocations` the right level?" open question). Note for whoever revisits
   **module-consolidation**: this touched `internal/prune/prunecmd.go` and
   `report.go`, so rebase before starting there.

3. **J2 — native-Go macos-user bootstrap re-port (J2.1 → J2.4) + D2.** *The
   critical-path Mac-backend item; now unblocked (the CI fix cleared
   `internal/entrypoint`).* The dead piece is real: `internal/cli/commands.go:375`
   still sets `RepoSrc = repoRoot/src` and `internal/macosuser/runplan.go:152,175`
   still stage/require a `python3` interpreter — and the tracked `src/` tree no
   longer exists (`git ls-files src/` → empty; the untracked `src/` +
   `yolo_jail.egg-info/` in the tree are stale Python build artifacts, not the
   shipped source). J2.1 threads container literals through `*entrypoint.Env`
   and J2.2 adds a darwin-native generation entry — both touch
   `internal/entrypoint` (which the landed CI fix left green). D2 (graceful
   repo-root degradation) pairs naturally with J2 step 3 — both touch the run front door
   and the `RepoSrc` contract; land them together. J2's Mac-side behavior
   (password apply, path_helper OQ-1, fresh-inode re-exec) is verified in **M1**,
   not the jail.

4. **J3 — container-builder rewiring.** After J2 (macos-user needs no builder at
   all). Resurrect `internal/containerbuilder` from git history (verified GONE —
   deleted with zero importers) and wire it into `internal/image/autoload.go`.
   Jail-developable; its verification runbook
   ([runbooks/mac-ac-container-builder.md](runbooks/mac-ac-container-builder.md))
   is zero-sudo and agent-runnable, so Track M can confirm it from inside a
   sandbox — and that cell already **PASSED** on real HW (2026-07-17).

5. **module-consolidation-and-cleanup** — *last, by its own admission.* Collapse
   the Python-mirroring `internal/*` split and drop the parity machinery only
   **after** J2/J3 land, so it consolidates a settled tree rather than a moving
   one. This is where the shared rich→ANSI renderer belongs if cli-color-audit
   didn't already lift it.

### Coupling: cli-color-audit ↔ module-consolidation (resolved)

Verified overlap — both plans called for the *same* shared color-aware rich→ANSI
renderer to replace the four+ near-duplicate `richTagRe` printers. cli-color-audit
ran first and landed the shared helper (`internal/richtext`), so if
module-consolidation is ever revisited it simply inherits it — don't build it
twice.

### cli-color-audit tail — DONE (2026-07-22)

The tail closed: `internal/cli/run/console.go` migrated off its private
`richTagRe`/`richToANSI`/`stripRich` onto `internal/richtext` (`67454a8`), the
two TTY-probe conventions unified onto `internal/tty` (`b76b2ba`), a genuine
`check`/`doctor` ANSI-leak-to-a-pipe fixed (`c9ea5e8`), and the remaining
commands (`loopholes`/`init`/`init-user-config`) classified. Nothing left.

## Config-composition build (own self-contained thread)

[agent-settings-composition.md](agent-settings-composition.md) is **finalized**
and jail-side, independent of the macOS J/D/M tracks. Its shape is **serial
foundation, then parallel fan-out, then deletion** — and Phases A and B have
**landed**:

**Phase A — the engine (DONE).** Built as a leaf library: pure `decode`/
`deepMerge`/`enforce`/`render` per codec (json/toml), the locked-down gopher-lua
VM + `ctx` bridge, the manifest schema + loader, and the fixture corpus that is
the spec. `yolo config render` is the thin read-only CLI over it.

**Phase B — surface migrations (DONE for agent configs).** Every agent surface
(pi/claude/gemini/copilot/opencode/codex, plus `agy` born on the prism) renders
through `agentcfg` at boot via its `Configure*Prism` writer, each verified at
parity in a nested jail. MCP/LSP/mise/identity are **not** yet manifest-modeled
and are **not** reachable via `yolo config render` (`config render mise` →
"no surfaces"); they still run through bespoke generators — their ports remain.

**Phase C — deletion + boot-wiring (DONE for agent configs, 2026-07-22).** Boot
and `yolo check` render the agent-config surfaces through `Compose`; the
`YOLO_PRISM_SURFACES` cutover gate is retired (prism unconditional), and the six
bespoke `Configure*` writers plus their dead helpers (the three-way merge, codex
TOML dumper, numeric-equality cluster) are deleted. `host_*_files` keys stay (the
prism host layer reads through them). **Remaining:** wire + cut over the non-agent
surfaces (mise/MCP/LSP/identity), then delete their bespoke generators — a serial
cleanup pass with nested-jail parity verification per surface.

## What unblocks the gated lanes

- **nix-ld — DONE (2026-07-22); only the host `env -i` acceptance matrix +
  `just load` remain.** Shipped as an image-layer change: the `flake.nix`
  interpreter retarget (`nixLd = imagePkgs.nix-ld.overrideAttrs`, `DEFAULT_NIX_LD`
  baked; `/lib` + `/lib64` `$LINKER_BASENAME` → `${nixLd}/libexec/nix-ld`) plus
  the baked non-store fallback dir `/usr/share/nix-ld/lib`. The entrypoint `/run`
  symlink turned out **unnecessary** under this variant (the built binary has
  zero `/run/current-system` references). The three MCP-wrapper
  `LD_LIBRARY_PATH` exports are gone (`mcp_wrappers.go`, `1d614e1`); the baked
  Env (`flake.nix:732`) + cli `-e` re-export (`assemble.go:405`) are deliberately
  kept as the nix-process dlopen-by-soname path (`d38463a`). Built + validated
  in a nested `yolo -- bash` (AGENTS.md "Build & deploy") and guarded by a
  `yolo check` FHS-loader tripwire (`d6d2e65`). The only host steps left are the
  broader `env -i` acceptance matrix (Claude native binary, copilot, MCP spawn,
  ctypes `dlopen`, aarch64) and `just load` to ship it to the maintainer's own
  jails. **User-visible payoff realized:** *custom* `mcp_servers` now start
  without the wrapper `LD_LIBRARY_PATH` hack — the gap where an MCP server that
  bypassed the node wrapper silently failed to load `libstdc++` under a scrubbed
  env is **closed**.
- **D4 Cachix (hardware-gated now).** The `flake.nix` `nixConfig` substituter is
  enabled (flake.nix:13-16, 730c258) and the CI push job self-enables with the
  `CACHIX_AUTH_TOKEN` secret. **As of 2026-07-22 the Cachix account and cache
  exist and already hold image data pushed from CI runs** — so account/token and
  first push are DONE. The ONLY remaining gate is hardware: prove one real Mac
  *downloads* the prebuilt image (substituter hit) instead of building from
  source. Composes with D3 (done) to give checkout-less Mac installs the image by
  download. See [handoff-cachix-cache.md](handoff-cachix-cache.md).

## The whole picture

```
 DONE ─────────────────────────────────────────────────►│ now │──────────────►

 jail    J1.1–J1.4 ✓  D1 ✓  D2 ✓  D3 ✓  CI ✓  cli-color-audit (FULLY DONE) ✓
 (agent)  J2.1–J2.4 ✓  J3 ✓  module-consolidation ✓  agy ✓  nix-ld ✓ (Variant A)
          config Phase A ✓  B ✓  agent-config cutover ✓ ─► non-agent surface ports

 (hw)    D4 Cachix ── substituter enabled ✓  account + cache + CI push ✓; needs only a Mac download proof ──►

 mac     M0 ✓  M1 ✓  M2 ✓  ── PROVEN on real Apple Silicon 2026-07-21 ────────────────────
 (hw)          (OQ-1 path_helper + finding-6 password observed and passing)
```

## Parallelization — what can run concurrently right now

The lanes have thinned out to essentially one open jail-side thread:

- **jail:** the config-composition **non-agent surface ports** thread is the only
  open agent-completable work — a wire-then-delete cleanup pass per surface,
  verifying each in a nested jail. (nix-ld, previously the parallel jail-side
  item, landed 2026-07-22.)
- **hardware (D4 Cachix Mac-download proof):** on its own clock; does not block
  the jail lane.

**Today's slice:** config-composition non-agent ports — jail-side, nested-jail
validatable per surface. There is no longer a hard cross-lane dependency — M1's
dependency on J2 is discharged (both landed), and nix-ld is done.

## Parked

- **integration-parallelism** — bounded `t.Parallel()` for the container suite.
  Parked on purpose: CI is free (wall time is only a convenience) and the fast
  local loop (`just test-fast`, `-short`) skips every container test, so this only
  pays off for a full local `just test`. It also needs real work first — per-test
  `GlobalStorage` isolation to unstick the shared `last-load` sentinel race
  (`autoload.go:143`) — before `t.Parallel()` is safe; N is bound by memory (each
  jail is a VM/container), not the 32 cores. The 2026-07-20 launch-merges
  (zbar/cli/isolation/cgroup → single launches, landed in c4ae68a) already
  recovered ~120s/arch with zero parallelism risk. See
  [integration-parallelism.md](integration-parallelism.md).

## Runbooks

The Mac verification procedures moved here from `docs/guides/runbooks/` — they
are Track M **verification gates**, not user-facing reference (the maintainer's
"mostly plans in disguise" call). They now live under
[`docs/plans/runbooks/`](runbooks/):

- [runbooks/mac-macos-user-e2e.md](runbooks/mac-macos-user-e2e.md) — Track M
  gate. The you-drive/agent-advise macos-user acceptance-bar test
  (§5 `which jq` → `/nix/store/…`). **M1 PASSED on real HW (2026-07-21)** —
  kept as the repeatable procedure.
- [runbooks/mac-ac-container-builder.md](runbooks/mac-ac-container-builder.md) —
  a **PASSED** gate (real HW, 2026-07-17) kept as the repeatable zero-sudo
  procedure; Track-M / J3-adjacent, agent-runnable.
- [runbooks/mac-go-port-verification.md](runbooks/mac-go-port-verification.md) —
  **STALE, recommended for `git rm` (maintainer call).** Its method is "diff
  each Go command against `uv run python -m src.cli …` and bail back to Python" —
  dead post-wipe (the Python tree is gone; the doc's own footer admits it). It
  carries a prominent STALE banner and is kept only until the maintainer
  confirms deletion; the live gates are the two runbooks above. **Recommendation:
  delete it** — the diff-against-Python method cannot be revived.

New Track-M runbooks (e.g. the M0 `mac-sandvault-session.md` deliverable) land
here too.
