# Composed-file permissions — what the prism makes read-only, and what it must not

**Status:** design + audit, 2026-07-25. Written to settle a blocking decision in
[host-file-staging.md](../plans/host-file-staging.md) (what to do with a
`~/.npmrc`-style destination) by first answering the general question:
**which files that come out of the prism are read-only, which are read-write, and
which *should* be?**

Audience: maintainers deciding the posture of a composed surface. Everything here
is traced from the live tree or probed inside a running jail; each claim carries a
`file:line` or a probe. Line numbers drift — trust the named symbol.

**Scope.** This doc is about the *permission and writability* posture of files the
composition engine produces. The mount stack itself is documented in
[jail-home.md](jail-home.md) §2 and not restated; the layering engine is
[agent-settings-composition.md](../plans/agent-settings-composition.md).

---

## 1. The one-paragraph answer

Today **every** prism-composed file is `0o644` in a read-write location, and 8 of
them silently accumulate a permanent capture overlay that outranks the host layer
— with no command to see or reset it. The maintainer's instinct ("keep as much
read-only as possible") is right in spirit but cannot be applied uniformly,
because the evidence shows composed files fall into **three genuinely different
kinds**, and the mistake to date has been treating them as one:

| Kind | Example | Who writes it | Correct posture |
|---|---|---|---|
| **Derived** — a pure function of host+config, nothing else writes it | git config, briefings, skills, `copilot/mcp`, `copilot/lsp`, `agy/mcp` | yolo only | **read-only**, kernel-enforced where possible |
| **Shared** — yolo composes it *and* the agent legitimately rewrites it | `claude/settings`, `mise/config`, `copilot/config` | both | **read-write, capture required** |
| **State** — the agent owns it; yolo injects a few keys ([which ones](#11-what-yolo-injects-into-a-state-file-and-why-it-must)) | `~/.claude.json` | agent | **read-write, never composed wholesale** |

The design rule that follows is **one question, not four modes**: *does anything
other than yolo write this file?* If no → read-only. If yes → read-write with
capture, and the capture must be visible. There is no third answer, and
"read-only-ish" (`0o444` DAC) is not one — see §6.

There is a **second, orthogonal axis** — *does a host file cross into this surface?* — and
it sharpens the rule rather than replacing it: see
[§1.0](#10-the-second-axis-is-the-surface-host-linked).

> **⚠ That question is necessary but not sufficient**, and [§8](#8-who-is-writing-program-operation-vs-directed-agent)
> is the correction: "something else writes it" hides **two** writers with opposite
> needs. A *program* writing its own config is unsteerable and is exactly what
> capture exists for. A *human-directed agent* is steerable — and for that writer,
> capture is often the **wrong** outcome, because the durable answer was a config
> key it should have edited instead. Read §1 for the posture, §8 for who to serve.

### 1.0 The second axis: is the surface HOST-LINKED?

Raised in review, and it is a real distinction the taxonomy above was missing.
`~/.claude/settings.json` and `~/.claude.json` are **different kinds of file** on an axis
orthogonal to Derived/Shared/State: one has a **host layer** and the other deliberately
does not.

- **Host-linked** — the host's copy is `:ro`-mounted at `/ctx/host-<agent>/<file>` and
  composed in as the `host` layer, so a host-side edit propagates on the next launch.
- **Jail-only** — no host file crosses at all; the surface is composed purely from
  yolo's own layers.

**Exactly 2 of the 11 rendered surfaces are host-linked** (verified by tracing the
`hostBytes` argument at every render call site): `claude/settings` and `pi/settings`. The
other nine pass `nil`. What decides it is `agents.AgentSpec.HostFiles` — a **hard-coded
per-agent allowlist**, `{Dir: ".claude", Files: ["settings.json"]}` and
`{Dir: ".pi/agent", Files: ["settings.json"]}`, and nothing else. That list is a
**credential boundary no config key can widen**; the retired `host_claude_files` /
`host_pi_files` keys are the counter-example that made it so
([agent-credentials.md](agent-credentials.md)).

Crossed with the kinds above:

| | **Host-linked** (2) | **Jail-only** (9) |
|---|---|---|
| **Derived** | *(empty — invariant)* | `copilot/mcp`, `copilot/lsp`, `agy/mcp` |
| **Shared** | `claude/settings`, `pi/settings` | `mise/config`, `copilot/config`, `codex/config`, `opencode/config`, `agy/settings` |
| **State** | *(empty — forbidden)* | `~/.claude.json` |

**Both empty cells are deliberate, and naming them is the point of the axis:**

- **Derived + host-linked is empty** because a file yolo regenerates from a host source
  with nothing else writing it does not need the prism at all — that is the composed git
  config, which is host-composed and `:ro`-mounted rather than being a surface.
- **State + host-linked is empty because it is forbidden.** Crossing the host's
  *identity* file is exactly what caused the 2026-04-23 `invalid_grant` incident (host and
  jail Claude sharing one single-use refresh-token chain, whichever refreshed first
  burning the other). `cb6e850` separated the identities permanently, and the host's
  `~/.claude.json` is *not* in `HostFiles` — verified: no `/ctx/host-claude/claude.json`
  mount exists.

So `~/.claude/settings.json` (preferences — safe to mirror from the host) crosses, while
`~/.claude.json` (identity + session state) does not. Same directory, same agent,
opposite treatment, for a reason.

**Does host-linkage change the correct posture?** It sharpens one rule rather than adding
a fourth kind: **host-linked ⇒ must stay read-write-with-capture.** Read-only would break
the promise that host edits propagate *and* that in-jail edits survive; both are required
because the host file is a real source of truth someone else maintains. It also makes
capture **more** dangerous there, which is worth stating plainly: on a host-linked surface
a captured overlay outranks the host layer *forever* — the "troublesome and hidden and
sticky" failure — whereas on a jail-only surface there is no host truth to fork from, so a
stale capture is merely stale. That asymmetry is the strongest argument for the
[§5](#5-the-capture-overlay-is-invisible-and-partly-noise) visibility work.

### 1.1 What yolo injects into a State file, and why it must

Fair challenge from review: *what keys are actually injected — doesn't yolo never touch
these? And since this is state rather than config, it lives on the persistent workspace
home, so is there even an issue?*

**Half right, and the half that is wrong is the load-bearing half.** yolo does touch
`~/.claude.json`, and it must. `writeClaudeJSON` (claude.go:54-72) injects exactly three
things, all verified live in this jail:

| Injected | Live value here | Why yolo must |
|---|---|---|
| `mcpServers` — the whole reconciled table | `chrome-devtools`, `sequential-thinking`, `tavily` | this is where claude reads user-scoped MCP from. Without it, `mcp_servers`/`mcp_presets` config does nothing and MCP is simply absent |
| `projects[<workspace>].hasTrustDialogAccepted` | `true` | suppresses the interactive trust prompt — an agent cannot answer a dialog |
| `projects[<workspace>].enableAllProjectMcpServers` | set each boot | lets the workspace's project-scoped MCP servers load without per-server approval |

The reconciliation is the interesting part, and it is why this cannot just be left alone:
the `mcpServers` block must be **re-derived every boot** from live config (a server dropped
from `mcp_servers` has to disappear), yet the file also holds 33 top-level keys of pure
agent state — `numStartups`, `tipsHistory`, `oauthAccount`, onboarding flags. So yolo
prunes only the names its own sidecar records as previously-managed, re-adds the current
set, and touches nothing else.

**On the second half — you are right, and it is exactly why the posture works.** The file
*is* on the persistent per-workspace home (`~/.claude.json` → `.claude/claude.json`, under
the rw `~/.claude` overlay — verified), it survives every restart, and yolo never
regenerates it. That is precisely what makes "State" a *safe* posture rather than a
compromise: no capture sidecar, no overlay precedence, no first-migration hazard. The
issue this row guards against is not the file's persistence — it is the temptation to
**compose** it, which is what §4.2 shows happening to `copilot/config` with a live OAuth
token as the casualty.

So the row is not "yolo has business here"; it is **"yolo must inject a little and compose
nothing"** — and the reason it earns a place in the taxonomy is that one surface
(`copilot/config`) is currently on the wrong side of that line.

---

## 2. Where composed files actually live (and why nothing is enforced today)

> **What "enforced" is for here.** The goal is **not** to stop an agent destroying
> the jail — that is unachievable (the agent runs as UID 0 and can `chmod`), and it is
> not the threat model: the container *is* the boundary, and `/workspace` is
> deliberately writable. The goal is narrower and entirely achievable: **a write to a
> yolo-composed file should fail the first time, as a signal that the agent is
> touching something it does not own.** An agent that hits `EACCES`, reads the error,
> and reconsiders has been told the truth. One that means it can still `chmod +w` and
> proceed — that is fine, because the point was the signal, not the wall.
>
> Read the rest of this section as "how legible is that signal today?" rather than
> "how strong is the sandbox?" — and see [§6](#6-why-0o444-is-not-a-posture), where the
> problem with `0o444` turns out to be that it delivers that signal to *some* agents
> and silence to others.

**The jail home is read-only by DEFAULT.** Writability is an explicit allowlist of
nested binds, not a set of holes punched in a writable home — verified by probe: the
home root itself is `Read-only file system`, while `~/.config`, `~/.cache`,
`~/.local`, `~/.npm-global`, `~/go`, `~/.ssh` and each **selected** agent's overlay
dir are read-write. Anything not on that list is read-only, including any new
top-level directory. That default is the right posture and this doc does not propose
changing it; the question is only what happens *inside* the writable islands, since
every one of the 11 rendered surfaces lands in one — so **nothing about the prism's
output is currently enforced, or even signalled**, by the kernel:

- `~/.claude`, `~/.pi`, `~/.codex`, `~/.copilot`, `~/.gemini`,
  `~/.gemini/antigravity-cli` — per-agent overlay binds, emitted **only for
  selected agents** (assemble.go:162-164).
- `~/.config` — one rw bind covering `~/.config/mise/config.toml` and
  `~/.config/opencode/opencode.json` (assemble_parts.go:74).

Two consequences that are easy to miss:

1. **An unselected agent's dir is read-only** — a consequence of the RO-by-default
   home above, since the overlay bind is emitted per *selected* agent. With
   `agents: [claude, pi, codex, agy]`, `touch ~/.gemini/x` and `touch ~/.copilot/x`
   both fail EROFS (probed). `~/.gemini/antigravity-cli` is writable while its own
   parent `~/.gemini` is not, because agy's overlay dir is a *nested* path
   (agents.go:193) — so the writable set is not even closed under "parent of a
   writable dir".
2. **Mode is create-only.** Every prism write is `writeInPlaceString` →
   `WriteStringInPlace(path, content, 0o644)` → `os.WriteFile`
   (helpers.go:71-73), whose perm argument is **ignored for an existing inode**.
   So the prism can set a mode on creation but can never restore a drifted one.
   All surfaces are in fact `0o644` in this jail (probed) — the drift hazard is
   real but currently unrealized.

> **Correction to a load-bearing comment.** helpers.go:58-60 claims the create
> mode is "0o644 (umask-independent)". That is false: `os.WriteFile`'s perm goes
> to `open(2)` and is masked by the process umask — with `umask 077` a "0o644"
> create yields `0o600`, and `writeExecutable` then yields `0o700`, not the
> documented `0o744`. The jail's umask happens to be `0022`, so the claim is
> accidentally true today and nothing pins it. **Fix: set the mode explicitly
> (`writeBytesMode`) or `chmod` after write, and correct the comment.**

## 3. The 12 declared surfaces, as they actually behave

`BuiltinManifest()` declares 12 surfaces (builtin.go:422-441). **11 are rendered
at boot; one is dead.** 8 of the 11 write capture sidecars.

| Surface | Path | Render path | Sidecars | Host layer | Anything else writes it? |
|---|---|---|---|---|---|
| `claude/settings` | `~/.claude/settings.json` | stateful | ✅ | `/ctx/host-claude` | **YES** — proven |
| `pi/settings` | `~/.pi/agent/settings.json` | stateful | ✅ | `/ctx/host-pi` | no evidence |
| ~~`gemini/settings`~~ | `~/.gemini/settings.json` | stateful | ✅ | — | **YES** — proven, but **being removed**: see [ROADMAP item 0](../plans/ROADMAP.md) (Google is deprecating Gemini CLI). Out of design consideration. |
| `copilot/config` | `~/.copilot/config.json` | stateful | ✅ | — | **YES** — credentials |
| `opencode/config` | `~/.config/opencode/opencode.json` | stateful | ✅ | — | no evidence |
| `codex/config` | `~/.codex/config.toml` | stateful | ✅ | — | no evidence |
| `agy/settings` | `~/.gemini/antigravity-cli/settings.json` | stateful | ✅ | — | no evidence |
| `mise/config` | `~/.config/mise/config.toml` | stateful | ✅ | — | **YES** — `mise use -g` |
| `copilot/mcp` | `~/.copilot/mcp-config.json` | stateless | — | — | no (pure overwrite) |
| `copilot/lsp` | `~/.copilot/lsp-config.json` | stateless | — | — | no (pure overwrite) |
| `agy/mcp` | `~/.gemini/antigravity-cli/mcp_config.json` | stateless | — | — | no (pure overwrite) |
| `claude/config` | `~/.claude.json` | **NEVER RENDERED** | — | — | **YES** — pure state |

Only **two** surfaces have a host layer wired at all (claude + pi), because
`agents.AgentSpec.HostFiles` has exactly two entries (agents.go:80,146). The
other six stateful surfaces pass `hostBytes=nil` — they are yolo-owned outright.

### 3.0.1 Should yolo manage mise tools at all?

Raised in review, and the sharper form of the question: is `mise_tools` **ever** the
right answer, or does it just encourage version pins in the wrong place? Three tiers
exist and it is worth separating them precisely, because they are not
interchangeable:

| Tier | Lives in | Who else sees it | Survives outside the jail | To change |
|---|---|---|---|---|
| nix `packages` | `yolo-jail.jsonc` | the repo, if committed there | no — image-only | **rebuild** |
| `mise_tools` | `yolo-jail.jsonc` **or the user config** | depends on scope | no — jail-only | restart |
| `/workspace/mise.toml` | the workspace | **everyone who clones the repo** | **yes** — host users too | nothing (`mise install`) |

**The workspace file is the right place for a project tool, and it is strictly better
than the other two.** It is committed, shared with non-jail users, and applies with no
restart at all (it is not a composed surface — mise reads it natively, `prism_mise.go:343`;
`mise install` is enough). Any pin that describes *what the project needs* belongs
there, and `mise_tools` for that purpose is a genuine anti-pattern: it hides a project
dependency in jail config where no host user or CI will ever see it.

**But there is one case the workspace file structurally cannot express, and the
maintainer's own configuration is the evidence.** The live user config
(`~/.config/yolo-jail/config.jsonc`) pins:

```jsonc
"mise_tools": { "neovim": "nightly", "pipx:swarf": "latest" }
```

Those are **personal tools wanted in every jail on this machine** — an editor and the
maintainer's own utility. Put them in a workspace `mise.toml` and you commit your editor
choice into every repo you touch and impose it on everyone who clones; put them nowhere
and you reinstall them per jail. No workspace-scoped file can say "mine, everywhere",
because *workspace* is the wrong axis. Note the shape of the evidence: the repo's own
`yolo-jail.jsonc` has `mise_tools` **commented out** (`:79`) while the *user* config uses
it for real. In practice the key is already being used at exactly the scope that
justifies it.

**The alternatives do not cover that case.** `MISE_ENV=jail` is already set
(`assemble.go:397`), so mise reads a `mise.jail.toml` — but that is still a *workspace*
file: per-repo, and committed unless separately gitignored. `mise.local.toml` is
gitignorable but still per-repo. A nix `package` would work but costs a rebuild and
cannot express `pipx:` or `nightly`. So the user-scope case survives every substitute.

**Recommendation: keep `mise_tools`, but retarget the guidance.** The key is not wrong;
its *documentation* is scope-blind. Concretely:

- **user config** (`~/.config/yolo-jail/config.jsonc`) — the legitimate home for
  `mise_tools`. Personal tooling, every jail, deliberately not in any project.
- **workspace config** (`yolo-jail.jsonc`) — should be **discouraged for `mise_tools`**,
  with a hint pointing at `mise.toml`: if the project needs it, the project should
  declare it where CI and host users see it. This is worth a `yolo check` *warning*
  rather than an error — it is a smell, not a bug, and there are edge cases (a tool
  needed before the workspace is trusted).
- **`mise use -g`** — the in-session escape hatch, and the thing capture exists to
  preserve. Steer at `mise.toml` + `mise install` first; `mise use -g` is right only for
  the same "global, not project" case as the user config.

#### Where in the stack the injection happens, and why the host layer is not available

The review's framing is the useful one: *you could put it in user mise (host, and
inherited into the jail if we inherited), or in the user yolo config (jails only, not the
host) — and that jail-only property is arguably the point.* Mapping it out:

| Layer | Scope | On the host? | In the jail? |
|---|---|---|---|
| host `~/.config/mise/config.toml` | user, all projects | ✅ | **❌ not inherited** |
| yolo `mise_tools` (user config) | user, all **jails** | ❌ | ✅ (composed into the jail's global config) |
| yolo `mise_tools` (workspace config) | one project's jails | ❌ | ✅ |
| `/workspace/mise.toml` | one project, **everyone** | ✅ | ✅ (native, no restart) |
| `mise.jail.toml` (`MISE_ENV=jail`) | one project, jails only | ❌ | ✅ (native) |

**So `mise_tools` at user scope fills a real hole: "mine, in every jail, but not on my
host."** The jail's global mise config is *yolo-composed* rather than inherited — verified
live: `~/.config/mise/config.toml` in this jail contains exactly the two `mise_tools`
entries and nothing from the host.

**And I have to retract my own counter-argument.** I previously suggested the key could be
deleted by mounting the host's `~/.config/mise/config.toml` and letting mise layer it.
That does not work, and the reason is already documented: **host and jail mise state are
deliberately separated** — the host store is never mounted, jails use a neutral `/mise`
with `RUSTUP_HOME`/`CARGO_HOME` redirected, because a shared store with mismatched
workspace paths *corrupted `mise install`*
([mise-host-jail-path-mismatch.md](../research/mise-host-jail-path-mismatch.md),
[jail-state-separation-design.md](jail-state-separation-design.md)). Inheriting the host's
tool *declarations* would immediately re-pose "which store are these installed into?" So
the composed-config route is not incidental; it is the separation working as designed.

**The deeper tension the review names, and it is the right frame.** These tiers are
serving **two different questions** that happen to share one file format:

- *What does this application need to build?* → belongs to the **repo**, wanted by CI and
  host users alike → `mise.toml`. Non-negotiable.
- *What does this developer want their environment to contain?* → belongs to the
  **person**, varies by machine, must not be imposed on collaborators → host mise config,
  or `mise_tools` at user scope for the jail-only half.

Conflating them is precisely the anti-pattern: an editor pinned in `mise.toml` inflicts a
preference; a Go version pinned in `mise_tools` hides a build requirement. **The guidance
should therefore be phrased by question, not by key** — "is this the app's requirement or
your preference?" answers *where* it goes, and the restart/rebuild axis is secondary.

#### Is "agent support tooling in every jail" a legitimate use case — or should it be nix?

The review's sharpened version: `mise_tools` is really for **support tools for the agent
or MCP that are not available or convenient in nix**, they never need changing at runtime,
so a restart is fine — could nix bake them instead, maybe even "mise at nix time"?

**Yes — and the question resolved a default that should not have existed.** `mise_tools`
used to carry a built-in `{"neovim": "stable"}`, which made it look like load-bearing
yolo machinery. It was not: a tool yolo wants in *every* jail belongs in the image, not in
a per-workspace mise store that re-installs it. **Removed 2026-07-26** — the default is now
`{}` and neovim is a baked nix package (`flake.nix`, beside `imagePkgs.go`), verified in a
nested jail: `/bin/nvim -> /nix/store/…-neovim-0.12.4/bin/nvim`, working with **no**
`mise_tools` entry at all.

That change also closed a latent trap: the run env sets `VISUAL=nvim` unconditionally
(assemble.go:414, for human ctrl-g editing), so before the bake a user with no
`mise_tools` had a `VISUAL` pointing at a binary that did not exist. Nested-jail check
confirms both halves still work: with a user-config `neovim: nightly` pin, mise's copy
shadows the baked one; without it, the baked one is used.

**So `mise_tools` is now purely a user knob — and it is still worth keeping**, because the
"mine, in every jail, but not on my host" case remains inexpressible anywhere else (the
jail's global mise config is yolo-composed, not inherited from the host — see the stack
table above).

**But the repo has already moved one tool the other way, and the reason is the whole
answer.** `flake.nix:659` bakes Go with this comment:

> `imagePkgs.go  # baked so the default go is RPATH-self-contained like node/python — no LD_LIBRARY_PATH dependency, no mise download on first use`

That names both nix advantages precisely: a nix-built binary is **RPATH-self-contained**
(no dependency on the `LD_LIBRARY_PATH` scaffolding that FHS/mise binaries need — see
[mise-node-dynamic-linking.md](mise-node-dynamic-linking.md)) and it is **present at boot**
rather than downloaded on first use. So "bake it in nix" is the right default whenever it
is available.

**When it is not available, and why the two live examples both qualify:**

| Tool | Nix has it? | Why mise anyway |
|---|---|---|
| `neovim = "nightly"` | yes, but **not nightly** | nixpkgs tracks releases; the *point* is the nightly channel. A nix override would mean pinning a source+hash and rebuilding to move it — for a tool whose whole appeal is that it moves. |
| `pipx:swarf = "latest"` | **no** | a PyPI package via mise's `pipx:` backend. Packaging it for nix is real work for a personal utility. |

So the honest rule is a **three-way test**, not a preference:

1. **Is there a clean nix package at the version you want?** → `packages`. Costs a
   rebuild, buys self-containment and boot-time presence. This is the default.
2. **Is it a channel/ecosystem nix does not track well** (nightly builds, PyPI/npm-only
   tools, anything you want to float)? → `mise_tools`. Costs a per-workspace-store install,
   buys no rebuild and easy version movement.
3. **Is it the project's requirement rather than yours?** → `mise.toml`, regardless of the
   above.

**"mise at nix time" — can we prebake mise tools into the image?** Not usefully, and the
reason is structural rather than effort: the nix image build is **hermetic and offline**
(`-mod=vendor`, no network — see AGENTS.md), while `mise install` is a network fetch of an
un-pinned "latest"/"nightly". Resolving those at build time would mean pinning each tool
to a hash, which converts it into option 1 with extra steps *and* discards the floating
version that motivated mise in the first place. The genuine middle ground already exists
and is worth naming instead: **when a tool stabilizes, promote it from `mise_tools` to a
nix `package`** — exactly the trip Go already made. `mise_tools` is then the *staging
area* for tooling that is not yet worth baking, which is a coherent role rather than a
loophole.

**Restart is fine here, and that is a point in mise's favour.** Support tooling is
installed once and not edited at runtime, so the restart cost the key carries is
irrelevant for this use case — the friction argument that justifies `mise use -g`
(§3.0.1) does not even apply. Which sharpens the guidance: `mise_tools` for *support
tooling*, `mise use -g` only for a genuine mid-session need, `mise.toml` for anything the
project depends on.

### 3.0.2 mise-specific plumbing that should fold into the prism

Yes — and this is item 1 on the ROADMAP, but with a correction worth recording:
**mise is already a prism surface and the roadmap says it is not.**
`ConfigureMisePrism` renders it statefully (`prism_mise.go:82`) and
`yolo config render mise` works — verified. Both
[ROADMAP.md](../plans/ROADMAP.md) and
[agent-settings-composition.md](../plans/agent-settings-composition.md) still claim
`config render mise` reports "no surfaces"; that is stale. The genuinely unported
non-agent surfaces are **MCP, LSP and identity** (`config render mcp|lsp|identity` →
"no surfaces", verified).

What remains mise-specific and *not* yet generic:

- **`YOLO_MISE_TOOLS` as a bespoke transport.** `mise_tools` is merged host-side
  (`config.MergeMiseTools`), serialized into its own env var, and re-decoded in the
  entrypoint — the pattern the composition plan explicitly wants to retire ("the
  config key *is* the `workspace` layer… no per-key merge function, no dedicated env
  var"). Touch points: `assemble.go`, `runplan.go`, `derived.go`, `env.go`,
  `check/entrypoint.go`.
- **`MISE_DISABLE_TOOLS`** — derived from the resolved user env at assembly time
  (`assemble.go`), unrelated to the surface.
- **Two bespoke side effects the prism deliberately does not own** and which should
  *stay* bespoke: the `/workspace/mise.toml` retire surgery (a **workspace**-file
  mutation, which yolo must never own) and the `mise uninstall` subprocess in
  `boot.go`. Both are correctly out of scope; only the transport is the cleanup.

### 3.1 The evidence that agents do write these files

This is the crux, so it is worth stating with proof rather than intuition. The
design docs already treat it as settled: capture exists *because* "the agent's
session can also write that same file — via a `/config` command, a permission
approval, **or a plain file edit** (every agent has a shell and file tools)"
(agent-settings-composition.md:266-269).

Probed in this live jail:

- **`~/.claude/settings.json`** — mtime 12.5 h *after* container start, and it
  carries `model: "us.anthropic.claude-opus-5[1m]"` while the host layer says
  `...opus-5` and the boot render emits no `model` at all. Its overlay holds 13
  captured keys. Claude Code demonstrably rewrites it.
- **`~/.claude.json`** — `numStartups: 48`, `promptQueueUseCount: 307`,
  `tipsHistory`, `skillUsage`, `machineID`. Pure state.
- **`~/.copilot/config.json`** — holds `copilot_tokens`, `logged_in_users`,
  `last_logged_in_user`, `firstLaunchAt`, `model`, `reasoning_effort`. yolo
  authors exactly one key of that file: `{"yolo": true}`.
- **`~/.config/mise/config.toml`** — its overlay has captured a real
  `mise use -g` (`{"tools":{"neovim":"nightly","pipx:swarf":"latest"}}`).

For **pi, codex, agy, opencode** the honest answer is **no evidence either way**:
none of those agents has ever been launched in this jail (no installed binary, no
launcher stamp), so their empty overlays prove nothing. Do not read "empty
overlay" as "safe to make read-only".

## 4. Defect register — verified bugs, each with its fix

**This section IS the work list for defects**; §9 sequences it rather than restating
it. Every entry below was reproduced by probe, not inferred, and each carries the fix
inline so the section is actionable on its own. All five are independent of any
redesign — they are broken today.

| # | Defect | Severity | Fix |
|---|---|---|---|
| [4.1](#41-gitconfig-is-unwritable-and-git-config---global-fails) | `~/.gitconfig` unwritable; `git config --global` fails "Device or resource busy" | confusing, arguably correct behavior | make the intent legible (drop the decoy symlink, or surface the header) |
| [4.2](#42-copilotconfig-can-wipe-a-live-oauth-token) | `copilot/config` can **wipe a live OAuth token** | ⚠ **data loss** | two steps, both specified in [§5.2](#52-how-to-actually-de-compose-a-credential-surface): **(1)** adopt-on-first-migration (one branch in `staterender.go`, fixes every surface at once, proved by probe); **(2)** then de-compose the credential surfaces so tokens leave the capture path |
| [4.3](#43-claudeconfig-is-a-dead-surface-with-two-live-side-effects) | `claude/config` declared but never rendered; `config render` renders it anyway | misleading output, one `writeInPlaceString` from data loss | mark it explicitly non-rendered so both the CLI and any future generic loop skip it |
| [4.4](#44-yolo-config-render-does-not-show-what-the-jail-gets) | `config render` omits the overlay **and** computed layers, and reads "host" from its own destination | the debugging command lies | feed it the real layers, or relabel it a defaults+host preview |
| [4.5](#45-host_files-reserves-symlink-aliases-but-not-their-targets) | reserved destinations miss symlink **targets** | gap, not yet exploited | reserve targets alongside aliases |
| [§2](#2-where-composed-files-actually-live-and-why-nothing-is-enforced-today) | `writeInPlaceString`'s "umask-independent 0o644" comment is false | latent | set modes explicitly (`writeBytesMode`) and correct the comment |

### 4.1 `~/.gitconfig` is unwritable, and `git config --global` fails

The `:ro`-mounted git config collides with the `GlobalHome` symlink escape hatch.
`GlobalHome/.gitconfig → .config/git/config` (ensure.go:98) is meant to resolve
into a writable overlay, but `.config/git/config` is itself shadowed by the
`:ro` composed-gitconfig bind (assemble_parts.go:255). Probed:

```
/home/agent/.gitconfig          NOT writable (Read-only file system)
/home/agent/.config/git/config  NOT writable (Read-only file system)
$ git config --global --add zz.probe 1
error: could not write config file /home/agent/.gitconfig: Device or resource busy
```

For comparison, the other two symlinks work as designed (`~/.claude.json` and
`~/.config/bashrc` are both writable). This is arguably *correct* — the git config
is Derived, so it should be read-only — but the failure is undiagnosable and the
symlink is now a decoy. **Fix: make the intent explicit** (drop the `.gitconfig`
symlink when the composed config is mounted, or emit a header comment; the file
already says "Regenerated read-only every run — edits here do not persist", which
nobody sees because they can't open it).

### 4.2 `copilot/config` can wipe a live OAuth token

`copilot/config` is rendered **statefully** with `Defaults: {"yolo": true}` and
no host layer. On a **first-migration** boot — absent, empty, or undecodable
`last_render` sidecar (staterender.go:129) — the render uses an empty overlay, so
the composed output is exactly `{"yolo": true}` and every agent-written key,
**including `copilot_tokens`**, is dropped. Steady state recovers (the next boot
captures the re-login), which is why it has gone unnoticed; but a new workspace,
a deleted `.yolo/prism/`, or a corrupt sidecar all trigger it. `seedAgentDir`
copies the token file in from `GlobalHome`, so a fresh workspace inherits the
token *and then wipes it on the first render*.

This is the clearest possible demonstration of the §1 taxonomy: `copilot/config`
was classified as a "write-once `{"yolo": true}` bootstrap file"
(config-migration-to-prism.md:120, stale-risk rated NONE) when it is really a
Shared credential file. **Fix: it must not be a wholesale-composed surface** —
either inject the single key read-modify-write (as `~/.claude.json` already
does), or give the surface a "preserve unknown keys" posture.

### 4.3 `claude/config` is a dead surface with two live side effects

Nothing at boot renders `~/.claude.json` — it is written bespoke by
`writeClaudeJSON` precisely because "it must NEVER be wiped"
(prism_claude.go:15-19). But the manifest entry is not inert:

1. It reserves the path against `host_files` (a good side effect).
2. **`yolo config render claude` renders it anyway**, because `configRender`
   iterates `m.ForAgent(agent)` (config.go:126) with no dead-surface filter — so
   the one debugging command prints a composition the jail never performs,
   dumping `machineID`, the full `mcpServers` table and onboarding state, with
   yolo's `projects` block layered on. It is display-only today, but it is one
   `writeInPlaceString` away from being the wipe the comment warns about.

**Fix: mark the surface non-rendered explicitly** (a manifest flag, or remove it
and reserve the path directly) so both the CLI and any future generic render loop
skip it by construction.

### 4.4 `yolo config render` does not show what the jail gets

`render` is documented as the offline twin of the boot render ("what render
prints is what the jail gets", §6). It is not: `renderSurface` builds
`Inputs{Surface, HostBytes, Script, VM}` (config.go:165-170) with **no Overlay,
no Workspace, and no Computed layer**, and it reads the "host" layer from the
surface's own destination path. In-jail that makes the rendered file its own
input. Probed consequences:

- `config render mise --explain` attributes `tools` to layer **`host`**, though
  mise has no host layer at boot at all (the tools ride `computed`).
- `config render claude --explain` prints `model: "...opus-5[1m]"` attributed to
  `host`, a value present in **no** boot layer — it is reading the in-jail edited
  destination.

For gemini/codex/opencode/copilot-mcp/agy-mcp/mise, `computed` is where
essentially all yolo-owned content lives, so `render` output for those surfaces is
structurally unlike the real render. **Fix: feed `render` the overlay sidecar and
the computed layer, or label plainly that it is a defaults+host preview.**

### 4.5 `host_files` reserves symlink aliases but not their targets

`reservedHomeFiles` blocks `~/.gitconfig`, `~/.bashrc`, `~/.claude.json`, but not
the paths they point at. Verified with a built binary against a temp workspace:
`~/.config/git/config`, `~/.config/git/ignore`, `~/.claude/claude.json` and
`~/.config/bashrc` all **pass** validation. Two of those are `:ro` mounts (the
render would EROFS-warn and skip); `~/.claude/claude.json` is the real
`~/.claude.json` inode, i.e. Claude's state file reachable under its alias.
**Fix: reserve symlink targets alongside their aliases.**

## 5. The capture overlay is invisible, and partly noise

Capture is load-bearing (§3.1) but three things are wrong with it today.

**It has no UI.** `yolo config` implements exactly one subcommand, `render`
(config.go:66-72). The sidecars live in `<workspace>/.yolo/prism/`, which is
gitignored. In this jail they hold 13 captured claude keys and 1 mise key that
no command can show, diff, or reset. Since **overlay outranks host**, a captured
value beats the host file *forever* — the exact "troublesome *and* hidden *and*
sticky" failure host-file-staging.md warns about, already shipping for the
builtin surfaces.

**Most of it is redundant.** Of the 13 keys in `claude-settings.overlay.json`, 11
are byte-identical to the host layer. They are migration-boot noise, not in-jail
edits. Only `model` and `permissions` actually diverge. Capture has no
auto-retire: a key whose captured value equals the layer beneath it stays in the
sidecar forever.

**One thing it is *not*:** a null tombstone is **not** permanent. `mergeAccumulate`
replaces a stored `null` the moment the key reappears on disk (engine.go:111-125),
verified by probe. So `"model": null` in the live overlay is a stale artifact
about to be overwritten, not a forever-suppression. The "captured deletion is
permanent" framing applies only to a key that is never rewritten.

### 5.1 Where should the sidecars live? (open — needs a decision)

Raised in review: the captured diff is **real state**, arguably valuable enough to
commit, and it currently hides under `<workspace>/.yolo/`, which `.gitignore:6` excludes
wholesale. **Only one of the two sidecars is state**, and getting that right changes the
shape of the answer:

| Sidecar | Kind | Why |
|---|---|---|
| `<surface>.overlay.json` | **STATE** — the durable record | the captured diff: a user's/agent's intent, small, human-readable, irrecoverable if deleted |
| `<surface>.last_render` | **NEITHER — a *pending-edit baseline*** | regenerable *going forward*, but **destroying it destroys information that has not been captured yet** (proved below) |

**`last_render` is not a cache, and the distinction is decidable rather than a matter
of naming.** A cache can be deleted with no loss of information. This cannot. The
review framing is exactly right: *it will regen, but the next start is when the diff is
captured, and that produces state* — so between boot N and boot N+1 the file is
**carrying the only baseline** against which an uncaptured edit can be recognized.

Proved with the real engine (temp copy of the tree, `ComposeStateful` directly). Same
inputs, `last_render` present vs absent:

```
last_render PRESENT  -> render {"mine":true,"theme":"dark"}   overlay {"mine":true,"theme":"dark"}
last_render DELETED  -> render {"theme":"light"}              overlay {}   firstMigration=true
```

Deleting it does not degrade gracefully — the agent's edit is **silently discarded** and
the surface reverts to `defaults`. The mechanism is `firstMigration := !in.LastRenderPresent
|| !lastOK` (staterender.go:129) → `overlay = emptyOverlay(kind)` (:140), which skips
capture entirely for that boot.

So the honest characterization is a **three-way**, not a two-way, split:

- **overlay** = state, permanently.
- **last_render** = state *for exactly one boot cycle* — a write-ahead baseline. Durable
  obligation between boots; worthless after the next capture.
- nothing here is a pure cache.

**Practical consequences**, all following from that:

- **Gitignoring it: fine.** The obligation is only until the next boot; nothing durable
  is lost by not committing it.
- **Moving it to `GlobalStorage`: fine**, and arguably better (it is per-workspace but
  not workspace *content*).
- **`yolo prune` is not a hazard here** — checked, and an earlier draft of this doc was
  wrong to imply otherwise. Prune never touches `.yolo/prism` (no reference anywhere in
  `internal/prune`), its dedup is **hardlinking** rather than deletion (content-preserving
  by construction), and what it does delete is regenerable: agent staging, shadowed
  files, stale out-links, image roots. So no guard is needed. The hazard in the bullet
  above is *any* deletion of `last_render` while an uncaptured edit exists — today
  nothing in yolo does that except `config reset`, where it is the intent.
- **`yolo config reset` deleting it: correct** — and this is the asymmetry that makes the
  characterization useful. Reset is an *intentional* discard of exactly those pending
  edits, so removing the baseline is the point (and it forces a clean reseed). Every
  *unintentional* loss of the same file is a silent data loss. Same deletion, opposite
  meanings.

That also relocates an earlier finding in this audit. It reported "a sidecar contains a
secret" (`codex-config.last_render` holds a `tvly-` API key, because `last_render`
copies a rendered file after `${VAR}` interpolation) and treated it as disqualifying for
the whole directory. **It is narrower than that** — the secret is in the *baseline*
file, which is short-lived and never a candidate for anywhere visible. Splitting the two
therefore removes the secret objection from the file you might actually want to move.

**But secrets will reach the capture diff too, by a different route** — and this is the
real constraint, verified rather than hypothesized. An in-jail `/login` writes
credentials into a file that *is* a composed surface, and the next boot captures them:
`~/.copilot/config.json` holds `copilot_tokens`, `logged_in_users` and
`last_logged_in_user` (probed live) **and** renders through `renderSurfaceStateful`
(prism.go:376). So on any workspace with copilot selected, a login puts an OAuth token
into `copilot-config.overlay.json`. Not "could" — will, by construction.

Which points at the reviewer's own suggestion as the right structural fix: **credential
state should not be a composed surface at all.** It should live in an *unmanaged* file
the prism never renders and never captures — which is exactly the posture
[§1](#1-the-one-paragraph-answer) calls **State** (`~/.claude.json` already has it:
declared in the manifest but deliberately never rendered, written bespoke because it
"must NEVER be wiped"). Doing that for `copilot/config` would:

- remove the token from the capture path entirely, so the overlay becomes genuinely
  shareable-in-principle;
- **also fix the ⚠ token-wipe defect in [§4.2](#42-copilotconfig-can-wipe-a-live-oauth-token)** — the
  same root cause, one fix;
- and require a home to persist in, which is the "explicitly allow for" part: a
  credential file needs a writable, *non-composed* location. `~/.copilot/` is already a
  writable overlay (§2), so the file can simply stay where it is once the prism stops
  owning it — no new mount, no new allowlist entry. The requirement is to stop
  composing it, not to relocate it.

So the ordering inverts: **de-compose credential surfaces first**, and the sidecar
location question gets much easier afterwards.

**Options, and what each costs:**

1. **Leave both under `.yolo/`, improve discoverability only** — `yolo config ls`
   already surfaces overlay contents (shipped), and could print the path. Zero risk,
   solves the "invisible" complaint without touching the secret question. This is the
   cheapest correct step and does not foreclose anything.
2. **Separate the durable record from the pending-edit baseline; keep both gitignored.**
   Prerequisite for anything below.
3. **Overlay to a committable path** (e.g. `.yolo-config/` or a `yolo/` dir, ignored by
   default only if the user says so). Needs: a scope decision (an overlay is
   per-workspace *and* per-machine — a captured `theme: dark` committed to a shared
   repo is at best noise, at worst a fight), and a story for the case the reviewer
   raises directly: **some people will not want it under source control**, so the path
   cannot be committable-by-construction.
4. **Two paths, user-selected** (the reviewer's own suggestion) — a config key naming
   where overlays live, defaulting to the current gitignored location. Most flexible,
   but it splits the sidecar layout into a variable, which every reader of
   `prismSidecarDir` then has to know about, and `yolo config reset` has to search both.

**My recommendation:** ① now, then **de-compose the credential surfaces** (above), then
② — because after de-composing, ② is what makes "the overlay is safe to look at" a true
statement rather than an aspiration. ③/④ stay blocked, but on **one** remaining question
rather than two: *is a captured edit per-workspace or per-machine?* A captured
`theme: dark` committed to a shared repo is at best noise; if the answer is
"per-machine", the whole idea of committing it dissolves and ④'s config key is the only
sensible form. (Committing *intentional* shared config is a different feature —
ROADMAP item 5, config packs — and probably wants that mechanism, not this one.)

### 5.2 How to actually de-compose a credential surface

§5.1 concluded "de-compose credential surfaces first" without showing a mechanism —
a fair objection. Here it is, with the options tested rather than asserted.

**The precedent already works and is worth reading first.** `~/.claude.json` is declared
in the manifest and **never rendered**; `writeClaudeJSON` (claude.go:54-72) does a
read-modify-write instead:

```go
claudeJSON := loadObject(claudeJSONPath)          // start from what is THERE
mcpServers := setDefaultMap(claudeJSON, "mcpServers")
for _, name := range loadManagedSet(...) { mcpServers.Delete(name) }  // prune only OURS
updateFrom(mcpServers, configured)                // assert only our keys
projects := setDefaultMap(claudeJSON, "projects") // touch only our subtree
...
writeInPlaceString(claudeJSONPath, dumpJSONIndent2(claudeJSON))
```

Nothing is composed from layers; nothing the agent wrote is at risk, because the file is
only ever *edited*, never *regenerated*. That is the shape a credential surface wants.

**Four options, and one is far smaller than the rest.**

**(a) Full de-composition** — move `copilot/config` to the `writeClaudeJSON` pattern.
Correct, and precedented. What it gives up, honestly: no host layer (copilot has none —
verified, `hostBytes=nil` at prism.go:376), no Lua transform, no capture, and no
`yolo config ls`/`diff` visibility. For a credential file, losing capture is a *feature*
(we do not want tokens in the overlay) and losing the host layer costs nothing. Losing
`config ls` visibility is the only real regret. Cost: one bespoke writer per surface, and
`claude/config` shows that is ~20 lines.

**(c) Adopt-on-first-migration** — the smallest possible change, and it **works**.

*How it identifies "our part" — there is no marker, and no recursion.* Worth stating
plainly because the mechanism sounds more exotic than it is. yolo never labels its own
keys. What it has is the ability to **compute what the layers alone would produce**, and
"ours" is defined as exactly that:

```
declared layers      Defaults{"yolo": true}                      <- all yolo asserts
pure render          {"yolo": true}                              <- layers, nothing else
file on disk         {"yolo": true, "copilot_tokens": …, "model": "x"}

mergeDiff(pure, disk) = {"copilot_tokens": …, "model": "x"}       <- the residue = theirs
```

One subtraction, no loop: **ours = what the layers generate; theirs = whatever is left
over.** `mergeDiff` is purely two-document (engine.go:163) — it knows nothing about
ownership, only about difference — so the whole trick is *choosing the right left-hand
side*. Today the first-migration branch passes an empty overlay
(`overlay = emptyOverlay(kind)`, staterender.go:140), which is equivalent to subtracting
the disk file from nothing and keeping nothing. Pass the pure render instead and the
residue is preserved.

The "recursion" intuition is understandable but is the *steady state*, not this fix: each
boot's render becomes next boot's baseline, so the residue is re-derived every boot rather
than accumulated once. That is a fixed point, not a growing structure — and it is exactly
why the baseline file is load-bearing for one cycle (§5.1). The rationale for discarding
on first migration was that capturing would "pin stale bespoke output" — a concern about
the *historical* migration away from bespoke writers, which is now complete.

Proved with the real engine on the actual `copilot/config` shape:

```
TODAY  (first migration):            { "yolo": true }                       <- token gone
ADOPT  (baseline = pure render):     { "copilot_tokens": {...}, "model": "x", "yolo": true }
```

So option (c) fixes [§4.2](#42-copilotconfig-can-wipe-a-live-oauth-token) for **every**
surface at once, with no per-surface code — and it is a strictly better first-boot
behavior in general, not just for credentials: adopting what is on disk is what a
migration *should* do. Its cost is that the token then lives in the overlay sidecar,
which is exactly what §5.1 wants to avoid. **So (c) is the right bug fix and the wrong
end state.**

**(b) Capture allow/denylist** — capture everything except credential-shaped keys.
Rejected: the predicate is the problem. `copilot_tokens` is guessable, but
`logged_in_users`, `last_logged_in_user`, an `oauthAccount`, a bare `token` — a
heuristic that is wrong either way is worse than no heuristic, because it fails silently.

**(d) Split the file** — yolo owns a separate config file, credentials stay in theirs.
Cleanest in principle, but it depends on the *agent* supporting a second config path, and
there is **no evidence** copilot does. Not available without upstream cooperation.

**Recommendation: (c) then (a), in that order, and they are complementary.**

1. **Ship (c) now** — it is a small change in one place (`staterender.go`'s
   first-migration branch: seed the overlay from `mergeDiff(pureRender, current)` rather
   than empty), it fixes a live data-loss bug across all surfaces, and it needs no
   per-surface decisions. Verification is straightforward: the probe above becomes a unit
   test, plus a nested-jail check that a copilot token survives a deleted sidecar.
2. **Then (a) for the surfaces that hold credentials** — so tokens leave the capture path
   entirely and the overlay becomes safe to expose. Audit target: `copilot/config`
   (confirmed: `copilot_tokens`, `logged_in_users`, `last_logged_in_user`) and any other
   surface whose live file carries auth-shaped keys. `~/.claude.json` is already done.

The honest gap: (a) removes those surfaces from `yolo config ls`, which is a regression in
exactly the legibility this doc has been arguing for. Worth fixing by having `ls` list
*non-rendered but yolo-touched* files too — which it would need anyway, since
`claude/config` is already in that category and is currently mislabeled rather than
absent.

### 5.3 Capture timing — deferred, not lost

Raised in review: deferring capture to the next boot looks like a hack. It is a
deferral, but **the earlier claim in this doc that "the last session's edits are lost by
construction" was wrong**, and the correction matters because it changes the priority.

**Nothing is stored in the container.** Every composed surface lives under a
host-backed read-write bind, so `--rm` destroys no content — verified by resolving each
surface against its covering mount:

| Surface | Covering mount | Persists past `--rm`? |
|---|---|---|
| `~/.claude/settings.json` | `~/.claude` (rw overlay) | ✅ |
| `~/.config/mise/config.toml` | `~/.config` (rw overlay) | ✅ |
| `~/.codex/config.toml` | `~/.codex` (rw overlay) | ✅ |

The baseline persists too — `.yolo/prism/` is in the workspace. So at exit the edited
file **and** its baseline both survive on the host, and the next boot's
`mergeDiff(last_render, current)` sees the edit and captures it normally. Observable
right now in this jail: the surface is newer (19:07) than its baseline (15:43) — an
uncaptured edit sitting safely on disk, awaiting the next boot.

**So the real consequence is narrow:** between the last entrypoint run and the next one,
the captured state is *stale* — the overlay does not yet reflect the edit. What that
actually costs:

- `yolo config diff` run host-side in that window under-reports; it shows the last
  captured overlay, not what is on disk.
- A `yolo config reset` in that window discards the baseline *and* leaves the edited file
  in place, so the edit is then adopted as if it were original — surprising, but not
  destructive.
- The one genuine loss case remains §5.1's: something deletes `last_render` while an
  uncaptured edit exists. Nothing in yolo does that today except `config reset`, where it
  is the intent — see §5.1; it is not a `prune` hazard and not a timing fix.

**Which reprioritizes the options.** Since nothing is lost, an inotify watcher is
solving a staleness problem, not a data-loss problem, and its costs (debounce against
agents that rewrite config repeatedly, plus a race with the entrypoint's own renders on a
flock-free sidecar path) are no longer justified by urgency.

| Option | Buys | Cost | Verdict |
|---|---|---|---|
| **`yolo config capture`** subcommand | an explicit checkpoint; makes the mechanism nameable (§8's real problem) | small — the capture half of `ComposeStateful` without re-rendering | **do this** |
| capture in the existing `onTerminate` hook | closes the window automatically | best-effort only (SIGKILL skips it); host-side, so it reads surfaces through the workspace mount | worth it after the above |
| inotify on `yolo-jaild` | eliminates the deferral | debounce + a real race story | **not now** — no data loss to justify it |
| shrink the problem via [§8](#8-who-is-writing-program-operation-vs-directed-agent)'s class split | fewer surfaces need capture at all | design work | do anyway, for its own reasons |

**Honest statement for the doc:** capture is **eventually consistent**, the window is
"since the last `yolo` invocation" (50 invocations over 3 days in this jail, so usually
short), and nothing is lost by waiting — only *observability* lags.

## 6. Why `0o444` is not a posture

`host_files` `mode: readonly` chmods the destination `0o444`. The plan is already
honest that this is "DAC, not kernel enforcement… a strong signal and a speed
bump, not a sandbox" (host-file-staging.md:158-167), because Claude YOLO runs
**UID 0** and root ignores mode bits. This audit adds that it is worse than
"weak" — it is **asymmetric**, which is the one thing a predictability-first
design cannot tolerate:

- as **root**: the write succeeds. `0o444` accomplishes nothing.
- as a **non-root** agent: `writeInPlaceString` opens `O_TRUNC` on a `0o444`
  file, gets `EACCES`, and the surface warns and never updates — every boot.

So the same declaration means "no protection" for one agent and "silently stops
re-rendering" for another. Compare the two honest options:

| Mechanism | Enforced? | Root bypass | Composition available | Backends |
|---|---|---|---|---|
| `:ro` bind mount | **kernel** | no | **none** (can't compose into `:ro`) | podman only¹ |
| `0o444` chmod | DAC only | **yes** | full | all |
| rw + capture | no | n/a | full | all |

¹ Apple Container ignores `:ro` entirely (apple/container#889) and cannot do
single-file binds (#1089) — every `:ro` surface degrades to a writable
materialized copy. macos-user has no bind mounts at all, so `:ro` is structurally
absent and the git identity there falls back to imperative
`git config --global` replay, staleness bug included.

**And even `:ro` is only enforced at the mount path.** The composed git config is
staged at `<wsState>/yolo-gitconfig` = `/workspace/.yolo/home/yolo-gitconfig`,
inside the rw workspace — same inode (verified: 306553017 at both paths). An
in-jail agent can edit the jail's global git config through the workspace path and
the change appears instantly at the `:ro` path. What `:ro` really buys is not
immutability but **non-persistence**: the next run unconditionally rewrites the
staged file. Surfaces staged outside the workspace (briefings, skills — under
`AGENTS_DIR`) are genuinely out of reach.

## 7. The proposed model

**One question decides the posture:** *does anything other than yolo write this
file?*

### 7.1 Derived → read-only, and say so

A file that is a pure function of host + config gets:

- **`:ro` bind mount** where the content can be composed **host-side** and the
  backend supports it (the existing git-config/briefing/skills pattern). Stage
  outside the workspace so the inode is genuinely unreachable.
- a **header comment** in the file itself where the codec allows
  (`# Regenerated every run — edits do not persist`), because a user who cannot
  write the file deserves to know why. The git config already does this.
- **no sidecars.** Nothing to capture.

Candidates today: `copilot/mcp`, `copilot/lsp`, `agy/mcp` (already stateless
pure-overwrites), plus git config/gitignore/briefings/skills (already `:ro`).
These are the surfaces where the maintainer's read-only instinct applies cleanly.

### 7.2 Shared → read-write with *visible* capture

`claude/settings`, `mise/config`, `copilot/config`, and — until evidence says
otherwise — `pi/settings`, `codex/config`, `opencode/config`, `agy/settings`.
(`gemini/settings` belonged here too and is being removed — [ROADMAP item
0](../plans/ROADMAP.md).) These stay `0o644` in a rw overlay with capture, plus the three
things capture is missing:

1. **`yolo config ls`** — one row per surface: path, codec, posture, contributing
   layers, overlay-key count with a ⚠ when non-empty.
2. **A boot notice** when a surface renders with a non-empty overlay.
3. **`yolo config diff` / `yolo config reset`** to inspect and discard.

Plus an **auto-retire**: drop an overlay key whose captured value equals the value
the layers beneath it would produce. That alone would empty 11 of claude's 13
keys and make the ⚠ signal meaningful.

### 7.3 State → never composed wholesale

`~/.claude.json` is the model here and it is already right: yolo does a
read-modify-write that injects only `mcpServers` and the workspace `projects`
entry, preserving everything else. `copilot/config` must move to this posture
(§4.2). The rule: **if a file holds credentials or session state, yolo may inject
keys but must never render it from layers**, because a first-migration boot
renders from defaults alone.

### 7.4 What this means for `host_files`' four modes

The four modes (`readonly`/`once`/`copy`/`capture`) collapse to **two postures
plus one seed flag**, which is the same taxonomy as above:

| Today | Becomes | Why |
|---|---|---|
| `readonly` | **`readonly`**, but implemented as a `:ro` mount where possible, else `copy` + a header comment — never a bare `0o444` | `0o444` is asymmetric (§6). A source-bearing file is Derived: the host file is the truth, so non-persistence is the actual goal, not unwriteability. |
| `copy` | merged into `readonly` | They differ only in the chmod, which is the part that does not work. |
| `once` | **`once`** (kept) | The genuine "seed then leave alone" case; no sidecar, no precedence puzzle. Cheapest correct posture for a source-less file. |
| `capture` | **`capture`** (kept, explicit) | The Shared case. Only mode that writes sidecars; must be visible per §7.2. |

Net: **`readonly` (derived, non-persistent), `once` (seeded), `capture`
(shared)** — three, down from four, and each maps to exactly one row of the §1
table.

### 7.5 The blocking decision: home-root files and new top-level dirs

The immediate question was what to do about `~/.npmrc` (Example 1 in the plan).
Established facts:

- `checkHostFileDest` **allows** both `~/.npmrc` (home-root dotfile) and
  `~/foo/bar.json` (new top-level dir) — neither is reserved.
- Both **fail today**: the home root is `:ro`, so `os.WriteFile("~/.npmrc")` gets
  EROFS and `os.MkdirAll("~/foo")` gets EROFS (probed). `ConfigureHostFiles`
  fails open, so the user sees a warning and no file.
- The `writable_home_dirs` pattern **cannot** serve a home-root *file*: it makes
  the destination a **directory**, so the composed write fails "is a directory".
- Pre-creating an **empty backing file** and bind-mounting it **breaks `once`**:
  `os.Stat` on a bind-mounted empty file succeeds, so the seed-if-absent guard
  returns early on the first boot and the file stays empty forever.

**Decision: use the third, already-proven mechanism — the `GlobalHome` relative
symlink** (`storage.EnsureSymlink`), exactly what `.bashrc`, `.claude.json` and
`.gitconfig` do. For a home-root destination `~/.npmrc`, materialize
`GlobalHome/.npmrc → .config/yolo-home/.npmrc` and let it resolve through the
mount table into the already-writable `~/.config` overlay. This:

- needs **no new mount** and no `GlobalHome` mountpoint dance;
- keeps `once` correct — a **dangling** symlink makes `os.Stat` return ENOENT on
  boot 1 (seed) and succeed on boot 2 (leave alone), verified end-to-end;
- reuses a mechanism with three existing precedents.

For a **new top-level dir** (`~/foo/bar.json`), reuse `writable_home_dirs`
staging for the destination's parent (backing dir + `GlobalHome` mountpoint + rw
bind) — that is the case the mechanism was built for.

> **Caveat on the mountpoint step.** `prepare.go:145-155` says the OCI runtime
> does not auto-create a mountpoint inside a `:ro` bind (crun `mkdirat` → EROFS,
> surfacing as `conmon bytes "": readObjectStart`). Seven live podman 5.8.4
> experiments in this jail **could not reproduce** that: the nested mountpoint
> auto-created in every realistic variant, including the exact yolo-shaped argv
> and with `--read-only`. The only reproduction was when the `:ro` bind's **host
> source** was itself on a read-only filesystem. Physical corroboration: the
> live `GlobalHome/.pi-lens` and `.foo` are `drwxr-xr-t` (1755 — podman's
> signature) with mtimes ~7 h *before* the fix commit landed, whereas
> `MkdirAll`'d dirs are `drwxr-xr-x` (755). **Keep the pre-create** — it is
> cheap, idempotent, and makes ownership/mode deterministic — but the comment's
> stated mechanism is version-dependent and should be softened rather than
> trusted as a cross-runtime guarantee. jail-home.md:167 (which says podman
> auto-creates) may in fact be the accurate description.

## 8. Who is writing: program operation vs directed agent

**This is the split §1 was missing**, and it changes what "serve the writer" means.
"Something other than yolo writes this file" hides two writers with opposite needs:

| | **Class 1 — program operation** | **Class 2 — human-directed agent** |
|---|---|---|
| Who | the agent *binary*, as part of functioning | the agent *reasoning*, because a human asked |
| Example | `/settings` in Claude writes `settings.json`; `npm config set`; a CLI's first-run write | "add tool X", "raise the memory limit", "add this MCP server" |
| Steerable? | **No.** You cannot ask a program to write elsewhere. | **Yes.** A skill, an error message, or a header can point it at the right path. |
| Right outcome | **preserve it** — this is precisely why capture exists | **redirect it** — the durable answer is usually a config key, not this file |
| Wrong outcome | reverting the user's `/settings` choice on next boot | capturing an edit that should have been a `yolo-jail.jsonc` change |

Two consequences follow, and they pull in opposite directions:

1. **Capture is *correct* for class 1 and is the whole motivation.** A human types
   `/settings`, picks a theme, and Claude rewrites its own JSON. Nothing else can
   preserve that across a regeneration. Removing capture to "simplify" would break
   the one case it was built for.
2. **Capture is a *consolation prize* for class 2.** If an agent was told "add a
   global tool" and it edits the composed file, capture makes that survive — but the
   change is now invisible to `yolo-jail.jsonc`, absent from the repo, and lost on
   `yolo config reset`. The edit *worked*, which is exactly why nobody notices it went
   to the wrong place. **Silent success in the wrong file is worse than a loud
   failure**, because it never gets corrected.

So the design goal is not "capture more" or "capture less" — it is **route by class**:
make class 1 work silently, and make class 2 *fail loudly with the alternative named*.
That reframes §6's `0o444` finding: the mode bit is not a security measure, it is the
**class-2 routing signal**, and its asymmetry (root bypasses, non-root gets EACCES) is
bad precisely because it routes some agents and not others.

### 8.1 The three-axis map an agent actually needs

For any "change X" request the answer has three parts, and today an agent can discover
at most one of them:

| Axis | Question | Where the answer lives today |
|---|---|---|
| **Source of truth** | which file should hold this durably? | `yolo config-ref` (jail keys only); **nothing** for agent-surface keys |
| **Application** | does it apply live, need a restart, or need a rebuild? | `configuring-the-jail` skill (jail keys only) |
| **Escape hatch** | is there a live invocation that avoids the restart? | one sentence in that skill (`npm i -g`, `mise use`) |

Worked through for the common requests:

| Request | Source of truth | Live? | Escape hatch | Steering today |
|---|---|---|---|---|
| add a CLI/runtime | `mise_tools` | needs restart | **`mise use -g`** (live, and captured so it persists) | ✅ skill covers it |
| add a nix package | `packages` | needs **rebuild** | `npm i -g` / `pip install` for the non-nix case | ✅ skill covers it |
| raise memory/cpus | `resources` | needs restart | none — mounts/env are frozen at create | ✅ skill covers it |
| add an MCP server | `mcp_servers` | needs restart | none | ⚠ skill does not name the key |
| open a port / add a mount | `network.ports` / `mounts` | needs restart | none | ✅ skill covers it |
| set Claude's model/theme/effort | **none exists** | n/a | edit the composed file → captured | ❌ **no steering at all** |
| add a Claude hook | **none exists** | n/a | edit the composed file → captured | ❌ none |
| bring a host dotfile in | `host_files` | needs restart | none | ⚠ config-ref only |
| reshape a composed key | `yolo-jail.config.lua` (Lua transform) | needs restart | none | ❌ undocumented for agents |

The two ❌ rows are the interesting ones: **for agent-surface keys there is no config
source of truth at all**, so "edit the composed file and let capture hold it" is not
the agent doing the wrong thing — it is *the only thing available*. That makes capture
load-bearing for class 2 as well, until a knob exists.

### 8.2 What steering exists today (and the gaps)

Verified inventory. The good news is that the best-in-class example is already in the
tree, so there is a pattern to copy rather than invent.

**Works well:**

- **Blocked-tool shims** — the model to imitate. The generated shim prints the reason
  *and* a `Suggestion:` line, then exits 127: `grep`'s says *"Use ripgrep (rg) for
  recursive searches"* → `Suggestion: Try: rg <pattern> [path]`. Reason + alternative +
  a documented bypass (`YOLO_BYPASS_SHIMS`).
- **The `configuring-the-jail` skill** — genuinely good on all three axes for jail
  keys: the three-layer merge, rebuild-vs-restart (only `packages` and `gpu.vaapi`
  rebuild), and it *explicitly* disclaims the ephemeral case (*"Do NOT use this for
  one-off installs… you can `npm i -g`, `pip install`, `uv pip install`, or `mise use`
  a tool right now with no config change and no restart"*). That sentence is the only
  place in shipped agent-facing text naming a live path.
- **`host_files` scope error** — the strongest single message in the codebase: it names
  the exact file to move the entry to *and why*.

**Gaps, in priority order:**

1. **No built-in skill or briefing line mentions composed surfaces at all.** Zero of
   the four embedded skills (`jail-startup`, `configuring-the-jail`,
   `diagnosing-the-jail`, `developing-yolo-jail`) mention the prism, a composed
   surface, `settings.json`, capture, or `yolo config ls`. The briefing names
   `yolo-jail.jsonc` exactly once, scoped to `packages`/`resources`. So the machinery
   built to make composed state legible is invisible through every channel an agent
   reads.
2. **Three agents get no skills at all.** `opencode`, `pi` and `codex` have
   `Skills: ""`, so the loop `continue`s and they receive no skills dir — *including
   yolo's own built-in suite*. Their briefing still says "read **configuring-the-jail**",
   a dangling reference. (Already a known ROADMAP item; it is also a steering bug.)
3. **No composed file says it is generated.** Exactly two generated files carry a
   header — the git config and `yolo-user-env.sh` — and **neither is a prism surface**.
   All 11 rendered surfaces are header-free (byte-probed). Worse, the git config's
   header is the one nobody can read: the file is EROFS in-jail (§4.1). A structured
   `json` surface *cannot* carry a comment header at all, which is the connection to
   the comment work in ROADMAP item 3.
4. **`yolo config ls|diff|reset` is undiscoverable.** It appears only inside the
   `host_files` entry of config-ref; USER_GUIDE never mentions it. (`yolo --help`'s
   blurb was also stale, advertising only `render` — fixed 2026-07-25.)

### 8.3 The restart axis, precisely

Class-2 guidance depends on this and the boundary is sharper than the docs suggest:

- **Mounts and the whole `-e` env block are frozen at container create.** The attach
  path emits only `<rt> exec -i [-t] <cname> yolo-entrypoint <cmd>` — **no `-e`, no
  `-v`** — so nothing carried by env or mounts can change without a new container.
- **The entrypoint re-runs every generator on each attach**, but reads only that frozen
  env. So composed surfaces *are* re-rendered live — from **stale config**. This is
  idempotent recomputation, not a config re-read, and it is the subtlety most likely to
  mislead: "the file regenerated" does not mean "my config change took".
- **One thing genuinely is live:** `refreshJailBriefings` runs on every invocation
  *before* the attach branch, so briefings and skills update in a running jail
  (inode-preserving writes, and the skills dir is cleared in place rather than
  recreated, precisely so the `:ro` bind keeps working). Consequence worth naming:
  the keys it re-reads (`network`, `security.blocked_tools`, `mounts`, `loopholes`,
  `resources`, `agents`, `agents_md_extra`) have **live briefing text but frozen
  enforcement** — the jail will *describe* a limit it is not applying.
- **In-jail, `config.LoadConfig` returns `<workspace>/.yolo/config-snapshot.json`
  verbatim** and never reads `yolo-jail.jsonc` (gated on `YOLO_VERSION`). But
  **`yolo check` bypasses `LoadConfig`** and validates the raw files — which is what
  makes the documented "edit → `yolo check --no-build`" loop work at all. That
  split-brain is load-bearing and undocumented.
- **The approval prompt only fires on fresh launch**, after the attach branch has
  returned — so attaching never re-checks config, and in a non-TTY it **auto-accepts**
  and rewrites the snapshot.

### 8.4 What to do about it

Ordered by value per unit of work; none of this is large.

1. **Extend `configuring-the-jail` to cover composed surfaces** (docs only, no code).
   Add: composed files are regenerated every boot; an edit to one is either captured
   or reverted depending on mode; `yolo config ls` shows which; and for keys with no
   config knob (Claude's model/theme/hooks) editing the composed file *is* the
   sanctioned path — say so explicitly, so it stops looking like a mistake. This turns
   the biggest ❌ into a documented decision.
2. **Give `pi`/`codex`/`opencode` a skills dir** so the steering reaches all seven
   agents. Two registry lines; the mount loop already handles it.
3. **A yolo-authored header where the codec allows** (`toml`, `lines`, `raw`): *"Generated
   by yolo — regenerated every boot; see `yolo config ls`."* Names the mechanism at the
   point of contact. Blocked for `json` surfaces, which is the argument for the
   header-only comment work in item 3.
4. **Make the class-2 signal symmetric.** `0o444` routes non-root agents and is silent
   to root; if the signal matters, it needs a mechanism that reaches both (a header, or
   `yolo config ls` in the briefing) rather than relying on the mode bit.
5. **Then, and only then, consider config knobs for the ❌ rows.** A
   `agent_config.<agent>` key is already "decided but unwired" in the composition plan;
   wiring it would convert "edit the composed file" from the only option into a
   fallback. Worth doing *after* the steering, because a knob nobody is told about is
   not an improvement.

## 9. Work items

Ordered by "fixes a real defect" before "improves the model".

**Defects:** see the [§4 register](#4-defect-register--verified-bugs-each-with-its-fix)
— five verified bugs, each with its fix stated inline. Sequence them **4.2 first** (it is
the only data-loss one, and de-composing that surface also removes credentials from the
capture diff, so it unblocks [§5.1](#51-where-should-the-sidecars-live-open--needs-a-decision)),
then 4.3/4.5 (cheap correctness), then 4.4 and the §2 umask fix.

**The model:**

1. ~~`yolo config ls` + boot divergence notice + `config diff` / `config reset`~~ —
   **✅ SHIPPED 2026-07-25** (`e138c55`, `91d2c2a`). This was the prerequisite for
   `capture` being defensible at all. Remaining gap is not the machinery but its
   *discoverability* — see item 5.
2. Overlay auto-retire: drop a captured key equal to the layer beneath it (§5).
3. Separate the overlay (durable) from `last_render` (a one-boot pending-edit
   baseline, NOT a cache — deleting it silently discards uncaptured edits, proved in
   [§5.1](#51-where-should-the-sidecars-live-open--needs-a-decision)).
4. Collapse `host_files`' four modes to three; implement `readonly` as `:ro`
   where possible instead of `0o444` (§7.4).
5. Steer directed agents at composed surfaces — the docs-only gap in
   [§8.4](#84-what-to-do-about-it), which is the highest value-per-effort item here.
6. Home-root destinations via `EnsureSymlink`; new top-level dirs via
   `writable_home_dirs` staging (§7.5).

**Evidence still missing:** whether pi, codex, opencode, and agy rewrite their own
settings files. Nobody has launched them in a jail long enough to know, and their
empty overlays prove nothing. Until someone does, they stay in the Shared bucket
(rw + capture) — the conservative choice, since demoting a Shared file to Derived
is what caused §4.2.

## 10. Open questions

- **Does `:ro` for a Derived surface need host-side composition?** Yes — you
  cannot compose into a `:ro` mount. That means a `:ro` posture gives up
  `managed`/`defaults`/`transform`/overlay for that surface, and moves its
  rendering to the host CLI. For `copilot/mcp`-style pure overwrites that is a
  clean trade; for anything with a Lua transform it is not. Worth deciding
  per-surface rather than as a blanket rule.
- **Should `macos-user` and Apple Container get a documented degradation table?**
  Both silently lose `:ro`. §6's footnote is the summary; a per-surface table may
  be warranted when `macos-user` is next worked on.
- **Is per-workspace the right scope for overlay sidecars?** They live under
  `<workspace>/.yolo/prism/`, so the same host file composed in two workspaces
  can diverge invisibly in different directions. Not obviously wrong, but it is
  unexamined.
