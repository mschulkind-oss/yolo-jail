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

---

## 2. Where composed files actually live (and why nothing is enforced today)

The jail home is a `:ro` bind of the shared `GlobalHome` with read-write binds
nested inside it (jail-home.md §2.2). Every one of the 11 rendered surfaces lands
in a **read-write** nested bind, so nothing about the prism's output is enforced
by the kernel:

- `~/.claude`, `~/.pi`, `~/.codex`, `~/.copilot`, `~/.gemini`,
  `~/.gemini/antigravity-cli` — per-agent overlay binds, emitted **only for
  selected agents** (assemble.go:162-164).
- `~/.config` — one rw bind covering `~/.config/mise/config.toml` and
  `~/.config/opencode/opencode.json` (assemble_parts.go:74).

Two consequences that are easy to miss:

1. **An unselected agent's dir is read-only.** With `agents: [claude, pi, codex,
   agy]`, `touch ~/.gemini/x` and `touch ~/.copilot/x` both fail EROFS (probed).
   `~/.gemini/antigravity-cli` is writable while its own parent `~/.gemini` is
   not, because agy's overlay dir is a *nested* path (agents.go:193).
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
| `gemini/settings` | `~/.gemini/settings.json` | stateful | ✅ | — | **YES** — proven |
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

`claude/settings`, `mise/config`, `copilot/config`, `gemini/settings`, and — until
evidence says otherwise — `pi/settings`, `codex/config`, `opencode/config`,
`agy/settings`. These stay `0o644` in a rw overlay with capture, plus the three
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

## 8. Work items

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

## 9. Open questions

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
