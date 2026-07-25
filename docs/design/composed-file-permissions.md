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
| **State** — the agent owns it; yolo only injects a few keys | `~/.claude.json` | agent | **read-write, never composed wholesale** |

The design rule that follows is **one question, not four modes**: *does anything
other than yolo write this file?* If no → read-only. If yes → read-write with
capture, and the capture must be visible. There is no third answer, and
"read-only-ish" (`0o444` DAC) is not one — see §6.

> **⚠ That question is necessary but not sufficient**, and [§8](#8-who-is-writing-program-operation-vs-directed-agent)
> is the correction: "something else writes it" hides **two** writers with opposite
> needs. A *program* writing its own config is unsteerable and is exactly what
> capture exists for. A *human-directed agent* is steerable — and for that writer,
> capture is often the **wrong** outcome, because the durable answer was a config
> key it should have edited instead. Read §1 for the posture, §8 for who to serve.

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

### 3.0.1 Should `mise use -g` be prevented rather than captured?

Raised in review, and worth answering because it is the one row where the "something
else writes it" writer is a *tool yolo itself installed*, not an agent going
off-script. Three findings shape the answer.

**There is a real use case, and it is the lack of a restart.** The declarative routes
both work: `mise_tools` in `yolo-jail.jsonc` (injected as the computed layer via
`YOLO_MISE_TOOLS`) and a workspace `/workspace/mise.toml` (a live mise layer, re-read
every boot). But both are *config* changes, and a config change means `yolo check` plus
a jail restart. `mise use -g <tool>` is the in-session escape hatch: an agent mid-task
discovers it needs `jq` at a specific version and installs it without tearing down the
jail it is working in. Blocking that would make the only path "stop, edit config,
restart, re-establish context" — the exact friction the writable-home islands exist to
avoid. So **capture is right here**, and the design already reasons this way
deliberately: the computed layer folds *above* the overlay, so an injected pin still
beats a stale in-jail `mise use -g`, while a genuinely user-added tool survives
(`prism_mise.go:44-46`).

**Preventing it is also not really available.** `mise` is on `PATH` and the agent is
UID 0; `mise use -g` writes an ordinary file in a writable overlay. The strongest
honest measure is the §7.1 signal — make the file `0o444` so the first `mise use -g`
*errors*, telling the agent this is yolo-managed config and the declarative route
exists. That is a legibility improvement, not a block, and it fits the "warn, not
prevent" framing of §2.

**The nuance that makes capture cheap here.** mise is the one surface with **no
`defaults` and no `managed`** at all (`builtin.go` `miseConfig` declares only
`Agent`/`Name`/`Path`/`Codec`) — every yolo-owned tool arrives via the computed layer.
So there is no managed-key-reverts-on-edit surprise to explain: the only precedence
rule is "an injected pin wins its own key, everything else you added is yours."

**Recommendation:** keep capture, add the `0o444` signal, and document the two
declarative routes in the error path so the signal is actionable.

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

## 4. Bugs and inconsistencies this audit found

These are independent of any redesign and worth fixing regardless.

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

Raised in review: this is **real state**, arguably valuable enough to commit, and it
currently hides under `<workspace>/.yolo/`, which `.gitignore:6` excludes wholesale.
Three findings constrain the answer, and the first is disqualifying for the simple
version.

**⚠ A sidecar already contains a secret.** `rg -l "tvly-" .yolo/prism/` matches
`codex-config.last_render` in this very workspace — an API key, in cleartext, because
`last_render` is a byte copy of a rendered file that had `${VAR}` interpolated into it.
So **"just move them somewhere committable" cannot be the default.** Any scheme that
makes the directory a candidate for `git add` needs the secret question answered first
— and there is no redaction concept anywhere in the design today.

**The two sidecars are not the same kind of thing**, which is what makes a single
answer awkward:

| Sidecar | What it is | Shareable? |
|---|---|---|
| `<surface>.overlay.json` | the **captured edits** — a user's/agent's intent, small, human-readable, the thing you might want to keep or review | plausibly yes |
| `<surface>.last_render` | a byte copy of yolo's own output, purely a **diff baseline**, regenerable, and the one that can hold interpolated secrets | **no** — it is cache, not state |

That split suggests the honest move is not "relocate the directory" but **separate the
two by kind**: the overlay is durable state, `last_render` is a cache. Once split, the
cache can stay gitignored under `.yolo/` (or move to `GlobalStorage` entirely, since it
is per-workspace but not workspace-*content*), and only the overlay is a candidate for
anywhere visible.

**Options, and what each costs:**

1. **Leave both under `.yolo/`, improve discoverability only** — `yolo config ls`
   already surfaces overlay contents (shipped), and could print the path. Zero risk,
   solves the "invisible" complaint without touching the secret question. This is the
   cheapest correct step and does not foreclose anything.
2. **Split cache from state; keep both gitignored.** Prerequisite for anything below,
   and independently worth doing because it stops calling regenerable cache "state".
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

**My recommendation:** ① now, ② next, and treat ③/④ as blocked on two prior questions
that are worth answering on their own merits anyway — *should env_sources secrets be
redactable in composed output?* (already an open question in ROADMAP item 4) and *is a
captured edit per-workspace or per-machine?* Committing machine-specific captured edits
to a shared repo is a different feature (shared config packs, ROADMAP item 5) and
probably wants that mechanism rather than this one.

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

**Defects (independent of the redesign):**

1. `copilot/config` must stop being a wholesale-composed surface — it can wipe a
   live OAuth token on any first-migration boot (§4.2).
2. Reserve symlink *targets* (`~/.config/git/config`, `~/.config/bashrc`,
   `~/.claude/claude.json`) alongside their reserved aliases (§4.5).
3. Mark `claude/config` explicitly non-rendered so `config render` stops printing
   a composition the jail never performs (§4.3).
4. Fix the `writeInPlaceString` umask claim — set modes explicitly and correct
   the comment (§2).
5. Make `~/.gitconfig`'s unwritability legible rather than a "Device or resource
   busy" mystery (§4.1).

**The model:**

6. `yolo config ls` + boot divergence notice + `config diff` / `config reset`
   (§7.2) — already Phase 3 of host-file-staging.md, and the prerequisite for
   `capture` being defensible at all.
7. Overlay auto-retire: drop a captured key equal to the layer beneath it (§5).
8. Feed `config render` the overlay + computed layers, or relabel it (§4.4).
9. Collapse `host_files`' four modes to three; implement `readonly` as `:ro`
   where possible instead of `0o444` (§7.4).
10. Home-root destinations via `EnsureSymlink`; new top-level dirs via
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
