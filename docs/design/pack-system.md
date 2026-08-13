# The pack system

A **pack** is a directory of jail configuration — skills, briefing prose, composed config
files, and optionally a tool to install — that yolo delivers into every jail you launch.
This is the whole of how a jail is populated: with no packs configured a jail has nothing
but the built-in shell and skills, and no coding agent. **Agents are packs.** Core has no
notion of "an agent"; the six that ship with yolo (`claude`, `copilot`, `opencode`, `pi`,
`codex`, `agy`) are packs like any other, selected by bare name.

This document describes the pack system as it works: what a pack declares, how the
declaration is validated, how packs are selected and fetched, and how they are rendered
into a jail. It is the authority for **authoring, debugging, or changing a pack**. For the
CLI surface see `yolo pack --help`; for the `packs` config key see `yolo config-ref`.

---

## 0. Three principles

Everything below follows from three rules. Read these first; the rest is their mechanism.

1. **Every file on disk has exactly one writer.** A pack either OWNS a file outright, or it
   does not write that file — it contributes typed INPUTS to a neutral core-owned assembler
   that writes it. Two packs claiming one owned path is an error, caught before anything
   runs. Two packs feeding one assembled file is the feature. There is no third case.

2. **Core knows the DOMAIN, not the TOOL.** Core names domain nouns from a closed,
   tool-independent set — `program`, `skills`, `briefing`, `config`, `state`, and so on. It
   never names `claude` or `copilot`. A pack maps its tool onto those nouns. The boot path
   renders every pack in one loop with no switch on any tool name; the *selection of which
   packs stage* is the only filter (§8).

3. **The manifest is static data.** Every claim a pack makes is readable without executing
   anything — that is what keeps linting, the origin gate, and content hashing honest. Lua
   has exactly one job in a pack (`derive`, §7): it computes config *values*, and never
   declares *effects*.

---

## 1. What a pack is, on disk

A pack is a directory. Nothing in it is mandatory.

```
my-pack/
├── pack.json      # optional — the manifest (contributes[])
├── AGENTS.md      # optional — prose concatenated into every briefing (CLAUDE.md also accepted)
├── derive.lua     # optional — Lua producers for config dynamic layers (§7)
└── skills/        # optional — one dir per skill, each with a SKILL.md
    └── rust-review/
        └── SKILL.md
```

The zero-ceremony pack — an `AGENTS.md` plus a `skills/` tree, no `pack.json` — is a
complete, useful pack: house rules and a skill corpus applied in every jail. `yolo pack
init` scaffolds exactly this.

Content rules are enforced at staging by `internal/packstage`, and a violation is **fatal,
not skipped** (a pack that half-stages is worse than one that fails loudly):

| Rule | Enforcement |
|---|---|
| An executable file is refused unless its entry sets `allow_exec` | staging error |
| A symlink whose target escapes the pack root is refused | staging error |
| Staging clears a destination dir's *contents*, never the dir itself | avoids clobbering a mountpoint |

`yolo pack lint` runs the real staging executor against a directory and reports what it
would stage and what it would drop, so these rules are checkable before a pack is
configured.

---

## 2. The manifest: `contributes[]`

`pack.json` has two top-level keys: `name` and `contributes`. Every field is optional; a
pack with no `pack.json` behaves as an empty `contributes`.

```jsonc
{
  "name": "claude",
  "contributes": [
    { "kind": "program", "bin": "claude", "via": "installer", "url": "https://claude.ai/install.sh" },
    { "kind": "skills",  "from": "skills", "into": ".claude/skills" }
    // ...
  ]
}
```

`contributes` is a single list of typed **contributions**. Each has a required `kind` drawn
from a closed set core owns (§3), plus the fields that kind uses. The struct is a flat
superset — a contribution carries only the fields its kind reads. Decoding is strict
(`DisallowUnknownFields`) and reports *every* problem, not the first, so a typo in one
contribution does not mask a second. An unknown `kind` is a loud load error.

**Every path is relative and points into `$HOME` or the pack.** Absolute paths, `..`
segments, and `:` are rejected as a security property, not a style rule: a pack — especially
a fetched one — naming `/etc/shadow` or `../../` must never validate. This check runs on
every path-bearing field of every kind.

---

## 3. The kinds, their footprints, and conflict rules

The closed kind set, what each *claims* on the environment, and how two claims on the same
target combine:

| Kind | Claims | Combine rule |
|---|---|---|
| `program` | a name on `PATH` + a lazy launcher in `~/.yolo-launchers/` | **Exclusive** — two packs, one `bin` → error |
| `requires` | a binary that must *already* be on `PATH` (asserted, never installed) | **Shared** — many packs may require one binary |
| `skills` | a merge-target skills dir | **Merge** — many packs into one dir is the feature |
| `briefing` | a concat slot at a path | **Concat** — ordered |
| `files` | exclusive ownership of a path | **Exclusive** |
| `config` | a config surface (identity + file) | **Exclusive** — a second writer must be `config-overlay` |
| `config-overlay` | a contribution to a surface another pack owns | **Overlay** — later-wins, per-key provenance |
| `state` | a writable home subtree at a scope | **Scoped** — same subtree at two scopes → error |
| `reads-host` | a host-home file, read-only | **Shared** — many readers are fine |
| `mount` | a host-home dir/file, read-only, at a `/ctx` path | **Shared** — many readers are fine |
| `env` | static environment variables in the jail | **Merge** — a key claimed twice collides |
| `launch` | flags for a binary | **Exclusive** — by `bin` |
| `hook` | a named imperative capability from core's closed set | **PerHook** |
| `autonomy` | the agent's autonomous/guarded permission postures, notch-selected | **Exclusive** — one per pack |

The **footprint** is this table applied to a concrete set of packs: the union of every
claim, plus the collisions where an Exclusive/Scoped target is claimed twice. `yolo pack
footprint` prints it, and `yolo check` folds it in. Some claims are flagged `⚠ review`
because they widen the trust surface — machine-scope state (it leaks across workspaces), a
`reads-host` grant, a `mount` of a host directory, an installer URL, a briefing that
prepends a host file. The review flag is an invitation to look, not a refusal.

The per-kind fields:

### `program`
Installs a tool and puts it on `PATH` via a launcher that installs on first invocation.

The launcher goes in `~/.yolo-launchers/`, which is **last** on PATH — after `/bin`. That
is deliberate and it is the whole reason the dir exists separately from `~/.yolo-shims`
(the blocked-tool shims, which are first): an installer only needs to run when nothing
else provides the name. Ordering it before `/bin` made a pack declaring `program fzf`
shadow the image's working `/bin/fzf` and then fail, because the launcher execs an
absolute install path and never consults PATH — declaring the dependency honestly broke
it. The consequence to know: a name the **image** bakes now wins over the pack's declared
version.
- `bin` (required) — the command name.
- `via` (required) — `npm` or `installer`.
- `package` (required for `npm`) — the npm package.
- `url` (required for `installer`) — the install-script URL (origin-gated, §9).
- `flags` — optional flags baked into the launcher.
A pack may declare **several** `program` contributions and each gets its own launcher —
exclusivity is per `bin`, not per pack, so `shellcheck` + `shfmt` in one pack is ordinary.
(Until 2026-08-03 only the *first* installed in a jail, while the host path reported all of
them; `InstallContributions()` returned inside its loop.)

- `install_hints` — optional map of host package manager (`brew`/`brew-cask`/`apt`/`dnf`/
  `pacman`/`nix`) → the package that provides `bin` there. Used below the `jail` notch
  (where yolo bakes no image) by `yolo check-deps` / `apply` to probe for the binary and, if
  missing, emit a runnable manifest. Declared by the pack that *introduces* the dependency.
  **For a `program`, the pack's OWN installer is the preferred remedy** and a hint is only
  the secondary line — see the note under `requires`. So hints matter most on `requires`,
  where yolo installs nothing at all.
  One key names an installer *flavor* instead of a manager: **`brew-cask`**, for a Homebrew
  cask (`brew install --cask`, and `cask "<token>"` in the generated Brewfile) rather than a
  formula. Use it whenever `https://formulae.brew.sh/api/cask/<token>.json` exists and
  `.../api/formula/<token>.json` 404s — a Brewfile `brew` line naming a cask token fails,
  and bare `brew install <token>` silently prefers a same-named *formula* (brew's `copilot`
  formula is AWS's deprecated ECS CLI, not the `copilot-cli` cask). `brew-cask` wins over
  `brew` when a pack declares both; the detected manager stays plain `brew`.

```json
{ "kind": "program", "bin": "opencode", "via": "npm", "package": "opencode-ai" }
{ "kind": "program", "bin": "claude", "via": "installer", "url": "https://claude.ai/install.sh" }
{ "kind": "program", "bin": "psql", "via": "npm", "package": "x",
  "install_hints": { "brew": "postgresql@16", "apt": "postgresql-16", "nix": "postgresql_16" } }
{ "kind": "program", "bin": "claude", "via": "installer", "url": "https://claude.ai/install.sh",
  "install_hints": { "brew-cask": "claude-code" } }
```

### `requires`
A binary that must **already exist**. Asserts presence and installs nothing.

`program` and `requires` are *install* vs *presence*, and conflating them was a real defect:
a pack needing a tool the image already bakes (`fd`, `fzf`) or the user already has (`jq`,
`psql`) had only `program`, so it either lied — declaring an npm install for a baked binary,
which then shadowed it — or declared nothing and lost `install_hints` entirely. Both
happened; the second is what `docs/examples/claude-fzf-pack/` did until this kind landed.

- `bin` (required) — the command that must be on `PATH`.
- `install_hints` — as `program`'s, and this is where they matter most: yolo will never
  install this binary, so the hints are the *only* remedy it can offer.
- `via`/`package`/`url` are **refused by name** — those belong to `program`, and a
  `requires` carrying one is the author reaching for the other kind. Silent otherwise: the
  fields are simply never read, so the tool never installs and nothing says why.

What it does at each notch:

| notch | effect |
|---|---|
| jail / guest | asserts presence at boot; a missing bin is a **warning naming the bin** (never a boot failure — the pack's other contributions are fine, and a fatal here would stop the jail you need in order to fix the pack) |
| host | feeds `yolo check-deps` / `apply --host` **exactly as `program`'s hints do**; that is the whole host-side point, and what lets a content-only pack carry a remedy |

It generates **nothing** — no launcher, no file, nothing on `PATH` — so unlike `program` it
cannot shadow the very binary it asserts. Not `Exclusive` either: it owns no path, so many
packs requiring one binary is the normal case rather than a collision.

```json
{ "kind": "requires", "bin": "fzf",
  "install_hints": { "brew": "fzf", "apt": "fzf", "nix": "fzf" } }
```

> **Where `nix` hints belong.** On genuine third-party dependencies like this one — where the
> user's own package manager is the right answer and nixpkgs is not meaningfully stale. The
> six shipped agent packs dropped their `nix` hints (2026-08-03): each agent CLI ships a
> first-party installer *and* updater, so the pack's own `via` is the remedy, and routing a
> user through nixpkgs handed them whatever that repo had — measured 2026-08-02,
> `github-copilot-cli` was 16 releases behind (1.0.61 vs 1.0.77) with nothing in the output
> to say so. `detectManager` also reaches `nix` only by *elimination* (after apt/dnf/pacman/
> brew all fail), so a user cannot even select it deliberately.
>
> A printed `curl … | sh` is a **suggestion the user runs**, never something yolo runs. yolo
> already flags an installer URL `⚠ review` in the footprint; actually running one is
> env-manager plan Phase 4.3's confirm-gated territory.

### `skills`
A skills tree merged into an agent's skills dir. Precedence is built-in < pack < the user's
own tree, so a local skill always wins.
- `from` (OPTIONAL since 2026-08-04, defaults to `skills/`) — pack-relative source dir.
  **Honored** at both notches (the jail's skills staging and `apply --host`), and by
  wrapped-plugin discovery, which scans it rather than a fixed `skills/`. It became optional
  because every shipped pack declared the same literal while the resolver already defaulted it —
  the validator was the only half of the code that thought the field mattered (§6a-3 of
  `../plans/shipped-2026-08-pack-batch.md` §6a-3). A pack with no manifest at all still merges its `skills/` dir — the
  zero-ceremony case.
- `into` (required) — home-relative destination.

```json
{ "kind": "skills", "from": "skills", "into": ".claude/skills" }
```

> **A source that is not there.** A `from` naming a directory the pack does not contain
> delivers nothing and is REPORTED by name — a warning at launch, a `refused` line and a
> non-zero exit at `apply --host` — rather than silently falling back to `skills/`. The
> CONVENTIONAL `skills/` being absent is the one exemption, and it is not an oversight: all
> six packs yolo ships declare `from: "skills"` and carry no skills of their own (their
> contribution exists to NAME the destination other packs merge into), so complaining there
> would fire on every launch of a stock config.

> **Shipped-behavior caveat.** `into` is the mount destination: two loaded packs declaring a
> `skills` contribution with the *same* `into` currently produce two bind mounts at one path
> and the jail fails to start ("duplicate mount destination"), even though the footprint model
> treats skills as safely mergeable. A zero-ceremony pack (a bare `skills/` dir, no
> contribution) merges cleanly into whichever agent pack owns that destination; declaring an
> explicit `skills` contribution that duplicates another pack's `into` is the case to avoid.
> See §14.

### `briefing`
Prose concatenated into a briefing file, attributed to its pack. **The destination is
GENERATED WHOLESALE at every notch** (ruling 2026-08-04 — see the ownership note below).
- `from` (OPTIONAL since 2026-08-04) — pack-relative source. Absent, the candidates are
  `AGENTS.md` then `CLAUDE.md` (`packdecl.Contribution.BriefingCandidates`). **Honored at both
  notches** since 2026-08-04: both readers go through `packload.BriefingProseFor`. The precedence
  is a FALLBACK CHAIN rather than a single choice — a declared `from` that is not in the pack's
  content falls back to the convention (as the host notch always did) and WARNS, naming the file
  that was not read. `skills` refuses in the same situation; the difference is deliberate,
  because narrowing the briefing chain would break packs that relied on the host behavior.
- `into` (required — and deliberately NOT conventionalized: a source has one right answer per
  KIND, a destination one per AGENT, so inferring it would mean inferring the agent set).
- `after` — `"host:<path>"` prepends the user's own briefing at that host path ahead of the
  composed content, so a personal `AGENTS.md` still outranks the pack's. Origin-gated, and
  **JAIL-ONLY**: at the host notch the path it names IS the generated destination, so there is no
  user-maintained file left to prepend and the host render ignores it. It is still a host-access
  claim at both notches, since declaring it means reading the host home.

```json
{ "kind": "briefing", "from": "AGENTS.md", "into": ".claude/CLAUDE.md", "after": "host:.claude/CLAUDE.md" }
```

> **Ownership.** yolo owns the destination file outright and regenerates it from the pack set
> on every apply, so a hand edit does not survive. The first apply into a destination the user
> wrote is CONFIRMED, and their prose is MOVED into
> `~/.config/yolo-jail/local/AGENTS.md` (the conventional local pack), from which yolo composes
> it back into every destination — so their instructions keep reaching their agents. To add
> personal prose, edit the local pack, not the destination.

### `files`
An opaque tree the pack owns outright, bind-mounted `:ro` at `into` in the jail.
- `from` (required) — pack-relative source. Unlike `skills`/`briefing`, `from` is
  **honored**: there is no conventional location for an opaque tree, so the declaration is
  the only thing that can name it. A file works as well as a directory.
- `into` (required) — home-relative destination.

```json
{ "kind": "files", "from": "files", "into": ".claude/fkdir" }
```

The source bound is the pack's **staged** tree, so `packstage`'s exec-bit and
escaping-symlink refusals have already run on it — `files` is not a channel around them.
Sole ownership is enforced before the container starts: two contributions claiming one
`into` are refused at launch, naming both packs, rather than reaching podman as a
"duplicate mount destination" error that names neither.

> **Backend note.** Apple Container cannot bind-mount a single file, so a `files`
> contribution naming one FILE is copied into the jail home there instead of mounted (the
> same route `briefing` takes). A directory is mounted on both backends.

Rendering `files` at the HOST is still refused by name (`yolo apply --host`): a bind mount
means nothing off-container, and writing the tree into a real `$HOME` is a different
posture with its own overwrite rules. See §14.

### `config`
A composed config surface the pack owns. `config` is a JSON array of surface definitions
(§5) carried verbatim; the surface schema is validated by the config engine.
- `config` (required) — the surface array.

```json
{ "kind": "config", "config": [
  { "agent": "opencode", "name": "config", "codec": "json",
    "path": "~/.config/opencode/opencode.json",
    "defaults": { "$schema": "https://opencode.ai/config.json" },
    "managed": { "permission": "allow" } } ] }
```

### `config-overlay`
A contribution to a surface **another pack owns** — the mechanism a "house-rules" pack uses
to assert keys on an agent's config without owning the file. Folds in after the owner's
layers, later-wins, with per-key provenance recorded.
- `surface` (required) — the target `"agent/name"`.
- `config` (required) — the object to overlay.

The body may carry **only `managed`** — the keys to contribute. Every field that would
redefine the *surface* (`agent`, `name`, `path`, `codec`, `mode`, `transform`, `defaults`,
`retireOnFirstRender`) is refused by name at decode: a contributor contributes keys, and the
owner decides where the file lands, in what format, and how it is maintained across boots.
That refusal is what stops an overlay reproducing the silent `mode`-flip hazard a second
`config` declaration can cause (`pack-config-collaboration.md` R1).

**If the target surface has no owner** among the selected packs, the overlay is **inert and
reported by name** — it neither creates the file nor fails the launch:

```
config-overlay  no effect — claude/settings has no owner (the `claude` pack is not selected)
```

It also fails in the useful direction: add the owning pack later and the overlay starts
working with no further edit. Applied overlays are announced too (which pack contributed to
which surface), and `yolo config diff <agent>` reads out per-key provenance — which pack set
which key, and where the owner's `managed` layer beat a contribution.

### `state`
A writable home subtree that persists.
- `at` (required) — home-relative dir.
- `scope` — `workspace` (default; backed per-workspace under `<ws>/.yolo/home/`) or
  `machine` (backed by the global home, **leaks across workspaces by design**).
- `because` — **required** for `scope: machine`, so a cross-workspace leak is a conscious,
  documented decision.

```json
{ "kind": "state", "at": ".claude", "scope": "workspace" }
{ "kind": "state", "at": ".claude-shared-credentials", "scope": "machine",
  "because": "identity/credential state shared across workspaces" }
```

### `reads-host`
A host-home file mounted read-only into the jail — the credential boundary. Origin-gated:
only an embedded or local pack may read a host file.
- `host` (required) — host-home-relative source.
- `into` — `/ctx` destination (defaults to `host-<pack>/<basename>`).

```json
{ "kind": "reads-host", "host": ".claude/settings.json", "into": "host-claude/settings.json" }
```

### `mount`
A host-home directory (or file) mounted read-only into the jail at a `/ctx` path. Like
`reads-host`, but the source may be a whole **directory** and the destination is an
arbitrary `/ctx` path the pack chooses (rather than a config-surface feed matched by
basename). Use it to make a reference tree — a dataset, a shared prompt library, a config
directory — visible in the jail. Origin-gated exactly like `reads-host`: a fetched pack is
refused, because a whole-home read is precisely what the credential boundary governs.
- `host` (required) — host-home-relative source dir or file.
- `into` (required) — the `/ctx` destination (mounted at `/ctx/<into>`).

```json
{ "kind": "mount", "host": "datasets/acme", "into": "acme-data" }
```
An absent source is skipped with a warning rather than failing the jail.

### `env`
Static environment variables set in the jail. Values are **literal strings only** — no
interpolation, no secrets, no host references — so `env` never reads the host and is
honored regardless of origin (a fetched pack may set env). A key two packs both set
collides in the footprint.
- `vars` (required) — a non-empty `map[string]string`.

```json
{ "kind": "env", "vars": { "ACME_MODE": "fast", "ACME_TELEMETRY": "0" } }
```
For values that must reference a secret or a host path, use the user config's
`env_sources` instead — that is the channel for host-derived and sensitive values, kept
out of a distributable pack on purpose.

### `launch`
Flags injected right after a binary on the command line, with alias suppression.
- `bin` (required), `flags`, `aliases` (`map[string][]string` — e.g. `-y` implies `--yolo`,
  so a user who typed `-y` does not also get `--yolo`).

```json
{ "kind": "launch", "bin": "claude", "flags": ["--dangerously-skip-permissions"] }
```

### `hook`
A named request for a core-provided imperative behavior. The set is **closed**:
`shared_credentials`, `per_jail_history`, `claude_plugins`. A pack requests a hook by name
and supplies its parameters; it cannot ship the hook's logic (that would put arbitrary
effect code in a fetched pack). New behavior means a new named hook in core.
- `hook` (required, from the closed set), plus `from`/`at` as that hook's parameters.

```json
{ "kind": "hook", "hook": "shared_credentials", "from": ".claude/.credentials.json", "at": ".claude-shared-credentials" }
```

---

## 4. The one-writer rule and the neutral assembler

Principle 1 restated concretely. There are two ways a file reaches the jail:

- **Sole ownership.** `files`, `program`, `launch`, and a pack's own `skills`/`briefing`
  tree — the pack owns the target, and two packs claiming one path is a lint error before
  anything runs.

- **Shared production via a neutral owner.** When two packs affect one file, no pack writes
  it. Each emits typed contributions and a **core-owned assembler** consumes all of them and
  writes the file into a staging tree it wholly owns; the staging tree is mounted read-only
  into the jail. The compose engine (§5) is that owner for config; the stage-then-`:ro`-mount
  pattern is it for skills and briefings.

```
   packs' typed contributions                 core assembler                 the jail
  ┌────────────────────────────┐          (the ONLY writer)            ┌──────────────────┐
  │ claude:  config inputs ─────┼──┐                                   │                  │
  │ house-rules: config overlay─┼──┼──►  compose ──► staging/ ──:ro──► │ ~/.claude/       │
  │ claude:  skills tree ───────┼──┼──►  merge   ──► staging/ ──:ro──► │   settings.json  │
  │ house-rules: skills tree ───┼──┘                  │                │   skills/        │
  │ claude:  briefing prose ────┼─────►  concat  ─────┘  (collision-   │   CLAUDE.md      │
  │ house-rules: briefing ──────┼─────►             checked, tracked)  │                  │
  └────────────────────────────┘                                      └──────────────────┘
        declare inputs                  one module writes every file      never written in place
```

Three properties fall out: no file is ever written twice (a "collision" is detected among
inputs, never raced on disk); provenance is free (one module writing from declared inputs
can say which contribution produced which key); and the staging layer is a safety boundary
(a bad contribution corrupts a throwaway tree, never the live home).

---

## 5. Config surfaces and the compose engine

A `config` contribution declares one or more **surfaces**. A surface is one generated file
plus its layer data:

| Field | Meaning |
|---|---|
| `agent` | the owning surface id — an agent (`claude`) or a non-agent owner (`mcp`, `lsp`, `mise`, `identity`) |
| `name` | the per-owner surface name; `(agent, name)` is the unique key |
| `path` | the file yolo writes in the jail, e.g. `~/.claude/settings.json` |
| `codec` | `json` \| `toml` \| `lines` \| `raw` — the decode/encode round-trip |
| `mode` | how the file is maintained (below); defaults to `stateful` |
| `defaults` | yolo's base layer, lowest precedence, user-overridable |
| `managed` | yolo's asserted keys, applied last so they win the merge |
| `retireOnFirstRender` | stale sidecar files to clean up on first render |

The engine composes a surface by folding layers with RFC-7386 merge semantics, lowest to
highest precedence:

```
defaults  <  host  <  workspace  <  config-overlay  <  capture-overlay  <  computed(derive)  <  [lua transform]  <  managed
```

- **`host`** is derived, not declared: a `reads-host` grant whose basename matches the
  surface path becomes this layer, read from its `/ctx` mount. This is how claude's own
  `~/.claude/settings.json` composes into the jail with no second declaration.
- **`config-overlay`** carries the keys OTHER packs contribute to a surface this one owns
  (§3), in `packs`-list order (later wins). Below `managed`, so the owner still wins a
  genuine conflict.
- **`capture-overlay`** carries a user's in-jail edits across regeneration (for `stateful`
  surfaces).
- **`computed`** is the per-boot dynamic layer produced by `derive` (§7); a null value there
  is an RFC-7386 tombstone that deletes the key.
- **`managed`** is the floor yolo always wins.

An **`rmw` surface has no layer fold** — it merges keys into a file the agent owns — so it
expresses the same precedence by write order instead: overlays are asserted first, then the
derived tables, then `managed`, then `defaults` fill only where absent. Same outcome, one
mechanism short.

`${workspace}` in a map key is substituted with the container workspace path.

The four **modes** (a closed set):

| Mode | Behavior |
|---|---|
| `stateful` | compose from layers *and* capture in-jail edits back into the overlay (the default) |
| `computed` | compose from layers and overwrite every boot, discarding in-jail edits |
| `rmw` | read-modify-write an agent-owned file: merge yolo's managed keys into whatever the agent wrote, no sidecars |
| `unrendered` | declared but never written |

`rmw` is the mode for a file the agent itself owns and mutates at runtime (e.g. claude's
`~/.claude.json`): yolo regenerates only the keys it manages and leaves the rest alone. yolo
owns the top-level `mcpServers` key in that file and regenerates it wholesale each boot — a
server a UI adds at the user scope is overwritten with a boot-time drop notice, while
local-scope and project `.mcp.json` servers are untouched. yolo does not reconcile; it
regenerates.

**Comments survive an `rmw` render, and the mode is why.** "Preserve everything yolo does
not declare" covers the prose as well as the keys, so on a `toml` surface a comment is put
back beside the key it explains — with one exception, which is a rule rather than a
limitation: a comment above a key the render CHANGES is dropped, because a `# pinned to 2.13`
sitting above `"2.15"` misleads worse than no comment at all. Every such drop is named in
`apply --host`'s output, in observe as well as assert. A `json` surface has nothing to
preserve: strict JSON has no comment syntax, so a commented file never decodes and `rmw`
refuses it untouched.

The other two composing modes are deliberately unchanged and for different reasons.
`computed` is a file yolo solely authors, so there is no user comment in it to keep.
`stateful` composes a file from many layers, which makes preserving a comment a PROJECTION
out of the `host` layer rather than an in-place edit — still open, and the remaining cost is
in [`../plans/host-file-staging.md`](../plans/host-file-staging.md).

---

## 6. Composed-file posture: what "writable" means

Every composed file has a read/write posture, from a three-way taxonomy:

- **Derived** — yolo is the sole author; the file is a pure function of layers. Made
  effectively read-only in the jail (`computed` mode); an in-jail edit is discarded next
  boot.
- **Shared** — yolo and the agent both write, on disjoint keys (`rmw`); yolo asserts its
  managed keys and preserves the agent's.
- **State** — the agent owns it; yolo only seeds and persists it (`stateful` with capture,
  or a `state` dir).

A second axis is whether a surface is **host-linked** (has a `host` layer from a
`reads-host` grant). The posture and the host-link together decide whether an in-jail edit
survives, and whether the file is safe to regenerate.

---

## 7. The `derive` slot

A surface whose content depends on live configuration — which MCP servers are set, which LSP
servers are enabled — cannot be static data. That dynamic layer is the one place a pack runs
Lua, and it is tightly bounded.

A pack ships `derive.lua` at its root and registers per-surface producers:

```lua
yolo.derive("opencode", "config", function(ctx)
  -- ctx.mcp_servers and ctx.lsp_servers are the live, read-only source tables.
  -- return the computed layer for opencode's `config` surface.
end)
```

The contract:

- A `derive` function is a **producer**: it returns the computed layer (a config value),
  which the engine folds in at the `computed` slot. It runs *before* the merge — it does not
  mutate the composed file.
- Its inputs are the **live source tables** `ctx.mcp_servers` and `ctx.lsp_servers`,
  read-only. The source set is closed and core-owned: a pack *projects* from these into its
  tool's dialect; it never invents a new source.
- `ctx.tombstone` is a sentinel that round-trips to Go `nil` so a key can be deleted (bare
  Lua `nil` in a table just drops the entry, which cannot express "delete this key");
  `ctx.empty_array` is the sentinel for an intentional empty JSON array (Lua cannot
  distinguish an empty array from an empty object).
- It runs in the sandboxed Lua VM, and it must be deterministic.

This is the whole of pack-supplied logic. There is no reshape op DSL and no pack-supplied
effect code; a projection that a fixed combine rule cannot express is a `derive` function,
and nothing else in a pack executes.

The **canonical MCP-server type** lives in core: `name → {command, args, env}`, open and
additively versioned (a new transport is a new optional field that never breaks an existing
projection). Each agent pack's `derive` projects that canonical table into its tool's shape
— opencode folds `command`+`args` into one array and renames `env`→`environment`; claude
tombstones `mcpServers` out of `settings.json` because MCP belongs in `.claude.json`.

---

## 8. Selection and the load path

### The `packs` key

Packs are selected by the `packs` config key, **user scope only** — a workspace config
naming a pack is a hard error, because a workspace is agent-editable and a pack can grant
host access. Nothing is active by default; an empty `packs` yields a jail with no agent, and
says so at launch.

Three source forms:

```jsonc
"packs": [
  "claude",                                              // a pack yolo ships, by bare name
  "file:///home/me/code/my-pack",                        // a local directory
  "git+ssh://git@github.com/org/repo//subdir?ref=main"   // a fetched pack (ref mandatory)
]
```

The object form adds `name`, `only`/`exclude` globs (per-project narrowing of a shared
corpus), and `allow_exec`.

### Fetch, lock, offline launch

Fetching happens in exactly one place: `yolo pack install` (and its alias `update`).
Everything else is offline.

- A git source is cloned host-side into a content-addressed store: a bare mirror per repo,
  a checkout per commit. Fetches run with fsck-on-transfer so malformed third-party content
  is rejected at the boundary, and with terminal prompts disabled so a missing credential
  errors instead of hanging. **The jail has no git credentials by design**, so fetch is
  host-only.
- The lockfile (`~/.config/yolo-jail/packs.lock.json`, beside the user config) records the
  asked-for `source`, the resolved `commit`, the `ref`, and — for a fetched pack the user
  granted host access — the approved host-access claims and the commit they were approved at
  (§9). Because trees are keyed by commit, a moving ref never corrupts an existing checkout.
- Launch resolves pins from the store and never fetches; a missing pin errors and points at
  `yolo pack install`. `yolo pack status` flags drift between the config address and the
  lock.

### Host-side staging, then jail-side render

- The host stages only the **selected** packs into the mounted pack tree (`YOLO_PACK_ROOT`),
  clearing it first. **The mount is the filter**: the entrypoint renders every pack it finds
  under the root, so staging only the selected ones — and clearing the tree — is what makes a
  dropped pack stop rendering. Staging all packs and filtering later would render packs
  nobody asked for.
- A declared pack that cannot be staged is a **fatal** error — a jail must not come up
  silently missing a pack it was told to load.
- In the jail, the entrypoint renders every staged pack in **one loop with no switch on any
  tool name**: for each contribution it dispatches on `kind`. This loop is the concrete proof
  of principle 2.
- Certain core reservation lists (writable-home roots, global-home subdirs, host-file
  reserved destinations) cover **every pack yolo ships**, not just the selected ones — a
  reservation that only knew the selected set could let one pack claim a path another pack
  needs.

---

## 9. The credential boundary: origin + approval

A pack has an **origin**: embedded (ships with yolo), local (a `file://` directory the user
controls), or fetched (cloned from a git ref). Four contributions constitute **host
access**, checked through a single predicate so a caller cannot honor some but miss another:

- `reads-host` — a host file read.
- `mount` — a host-home directory or file read.
- `program` via `installer` — a curl-to-shell install URL. (`npm` is *not* gated: a package
  name is not a host read.)
- `briefing` with `after: "host:…"` — prepending the user's own briefing file.

Static `env` is *not* host access — its values are literal strings, so it is honored for
any origin.

Host access is granted by **origin plus approval**:

- An **embedded or local** pack may access the host unconditionally. Its origin already
  carries the user's own authority — yolo ships the embedded packs, and a `file://` pack is
  a directory the user controls.
- A **fetched** pack may access the host **only for the specific claims the user approved at
  `yolo pack install`.** Installing a third-party pack no longer means trusting it with your
  host home by default, but it is no longer a hard refusal either: at install time the pack's
  host-access claims are shown and the user answers y/N once. The approval — the exact set of
  claims, and the commit it was granted at — is recorded per-pack in the lockfile.

```
$ yolo pack install
acme  v2 → a1b2c3d
  ⚠ pack acme reads your host:
      mount acme-refs -> /ctx/acme-refs
      reads-host .config/acme/key
  Approve host access for acme? [y/N] y
```

**A moving pin re-prompts only when it gains access.** `yolo pack update` pulls a new commit
and recomputes the pack's host-access claims. If they are a subset of what was approved
(unchanged, or narrowed), the approval carries forward silently. If the new commit declares a
claim the user has *not* approved — a benign pack turning to read `~/.ssh` across a ref bump —
install re-prompts with the full current claim set before recording it.

At **launch**, a fetched pack's host access is gated on this lockfile approval, not on origin:
its `mount`/`reads-host`/`installer`/host-`briefing` claims are honored only if the lock
approves the currently-resolved commit's claims. An unapproved fetched pack still loads — its
`env`, `skills`, `briefing`, `config`, and other non-host contributions all work — but its
host claims are refused with a printed notice pointing at `yolo pack install`. The gate
**fails closed**: no lockfile, a corrupt lockfile, or a non-interactive install all mean "not
approved". Inside the jail every pack loads as host-permitted, because the host already
applied the gate by deciding which `/ctx` mounts exist.

**Transparency at every launch.** Because a fetched pack *can* now read the host, the startup
banner lists what each loaded pack reads this launch — its mounts, host-file reads, and env —
so the effective environment is visible on screen, not just recorded in a lockfile. Only
honored claims appear (a refused, unapproved claim is not in the footprint), so the banner is
an accurate picture of what actually crossed into the jail.

---

## 10. Command surface

| Verb | What it does |
|---|---|
| `yolo pack init [dir]` | scaffold a valid skeleton (`AGENTS.md`, an example skill, `README.md`); never a `pack.json` |
| `yolo pack lint [dir]` | run the real staging executor (no-stageable-files, missing skills/briefing, a skill dir with no `SKILL.md`) **and validate the `pack.json` manifest** (unknown kind, missing required field, unknown key — every problem, not the first), then print the pack's footprint |
| `yolo pack ls` | list configured packs and what each stages |
| `yolo pack explain <name>` | stage one pack and show what it stages and what it dropped (`file://` local only) |
| `yolo pack footprint [ref]` | print claims + cross-pack collisions + review summary; `[ref]` is an embedded pack name **or a local path / `file://` source** so you can inspect a pack you are authoring |
| `yolo pack install` / `update` | fetch configured packs, write the lockfile, report moved pins, prune dropped packs (the only network step); **prompt to approve a fetched pack's host access**, re-prompting only when a moved pin gains a claim (§9) |
| `yolo pack status` | show locked commits and flag config/lock drift |

---

## 11. Worked examples

### The `claude` pack

Selected as `"claude"`. It declares, in one `contributes[]`:

- `program` — installs `claude` from its installer URL (origin-gated).
- `briefing` — its `AGENTS.md` composed into `~/.claude/CLAUDE.md`, prepending the user's own
  `~/.claude/CLAUDE.md` if present.
- `skills` — its `skills/` tree merged into `~/.claude/skills`.
- two `config` surfaces — `~/.claude.json` in `rmw` mode (yolo owns `mcpServers`, claude owns
  the rest) and `~/.claude/settings.json` in the default `stateful` mode.
- two `state` dirs — `.claude` (per-workspace) and `.claude-shared-credentials` (machine,
  with a `because`).
- `reads-host` — `~/.claude/settings.json`, which becomes the `host` layer of the `settings`
  surface by basename match.
- `launch` — `--dangerously-skip-permissions`.
- three `hook`s — `shared_credentials`, `per_jail_history`, `claude_plugins`.

Its `derive.lua` produces two computed layers: for `config`, the `mcpServers` table from the
canonical source; for `settings`, tombstoning `mcpServers` (MCP belongs in `.claude.json`)
and toggling LSP plugins by which LSP servers are configured.

### The `opencode` pack

A leaner pack: `program` (npm), `briefing`, and one `config` surface. Its `derive.lua` is the
worked projection — it folds the canonical MCP `command`+`args` into opencode's single
`command` array, renames `env`→`environment`, injects `type: "local"` and `enabled: true`,
and omits the whole `mcp` key when no servers are configured.

### A minimal house-rules pack (no manifest)

```
acme-conventions/
├── AGENTS.md                      # house conventions, appended to every briefing
└── skills/
    └── acme-rust-review/
        └── SKILL.md
```

Configured as `"file:///home/me/acme-conventions"`. It declares no host access, so nothing
is origin-gated; it works identically as an embedded, local, or fetched pack. `yolo pack
lint` validates it, `yolo pack install` records it in the lockfile, and every jail gets the
prose appended to its briefing (attributed to the pack) and the skill merged into the skills
dir.

---

## 12. Invariants

- The manifest stays static data — every claim readable without executing anything.
- The credential boundary holds — a fetched pack reads the host only for claims the user
  approved at install, and a pin that gains access re-prompts; a fetched pack never reads the
  host silently.
- Packs stay user scope — a workspace config cannot name one.
- The source set for `derive` stays closed and core-owned — a pack projects, never invents.
- `derive` is deterministic.

---

## 13. Open questions

These are unsettled and do not affect a pack author today; they are recorded here rather
than scattered.

1. **Footprint in the content hash.** Whether a pack's declared footprint should be part of
   what its lockfile pin hashes, so a pack cannot silently widen its claims across a moving
   ref.
2. **Skills reshape.** Skills merge as plain trees today. Whether a second producer will ever
   need to reshape another pack's skills (as `derive` reshapes config) is open; no case has
   needed it.
3. **Machine-scope gating.** Whether `scope: machine` state should require more than a
   `because` string — e.g. an explicit user opt-in per pack.
4. **Collision severity.** Reservation lists cover embedded packs only. A configured pack's
   `state` dir is not reserved, so a `host_files` entry can collide with it and surface as an
   opaque mount error. The direction: compute embedded/configured/user claims in one pass at
   `yolo check`, refuse pack-vs-pack, report pack-vs-user. The footprint union is the
   mechanism; the severity wiring is unbuilt.
5. **Per-confinement field applicability.** Which kinds mean anything when a pack is rendered
   somewhere other than a container (see §14) — `config`/`skills`/`briefing` port cleanly;
   `files`/`install` are refused; `reads-host` and `state` at the host are genuinely unclear.
   This is `yolo-as-environment-manager.md`'s concern.

---

## 14. Gaps between the schema and the shipped tooling

The `contributes[]` schema above is the intended design; some of it is not yet honored by
the shipped code. A pack author should know these before treating a manifest field as
load-bearing. `yolo pack lint` now validates the manifest and `yolo pack footprint <path>`
inspects a local pack, so most of what follows is catchable before boot.

Not yet wired:

- **`files` at the HOST.** `files` now delivers in a jail (bind-mounted `:ro` at `into`),
  but `yolo apply --host` still refuses it by name: the refusal is true of a *bind mount*
  and false of the *intent*, and writing the tree into a real `$HOME` needs the
  never-clobber and file-mode policy the host renders are building
  (`docs/plans/pack-host-management-plan.md` Phase 7). Refused by name, never silently
  skipped.

- ~~**`from` on `briefing`, in a JAIL.**~~ **FIXED 2026-08-04** (shipped-2026-08-pack-batch.md §6a-4).
  `run.readPackBriefing` took a DIRECTORY and read a root `AGENTS.md`/`CLAUDE.md` regardless of
  `from`, so a pack whose prose lived elsewhere briefed at the host notch and not in a jail.
  Both readers now go through `packload.BriefingProseFor`, over
  `packdecl.Contribution.BriefingCandidates()` — the same one-resolver convergence `skills`
  needed. One deliberate difference from `skills`: `briefing`'s precedence is a **fallback
  chain**, so a declared `from` that is absent still resolves to the convention (as the host
  notch always did) — but the fallback now WARNS, naming the file that was not read.

- **Typed inter-pack exports.** The design allows a pack to `export` a canonical type (e.g.
  MCP servers) that other packs `import`, so a shared dependency lives in one pack. Only the
  *projection* half shipped (`derive`); the export/import graph did not. MCP server instances
  come from the `mcp_servers` config table, not from an exporting pack.

- ~~**Rendering a pack OUT to the host.**~~ **SHIPPED 2026-08-02**
  (`docs/plans/pack-host-management-plan.md`). `yolo apply --host` now renders `config`,
  `skills`, `briefing`, and `files` into the real `$HOME`, reports resolved `program` dep
  state, and accounts for every other kind BY NAME (rendered, refused, or unbuilt — never
  silently absent). Three things a reader should know before relying on it:
  - **Ownership does not carry over from the jail.** Every jail path is disposable and
    `:ro`; the host equivalents are the user's own files. So `files` refuses any path it cannot
    prove yolo wrote, and retires its own output by ARCHIVING it under the state dir rather than
    deleting (reclaimed by `yolo prune`). `skills` used to work the same way and no longer does —
    see the next bullet.
  - **`skills` is COMPOSED WHOLESALE**, at every notch (maintainer ruling 2026-08-04,
    shipped-2026-08-pack-batch.md §6a-2), which makes it the `briefing` story applied to a directory:
    - **The user's own skills MOVE into the local pack** (`~/.config/yolo-jail/local/skills/`),
      where yolo composes them back into EVERY destination. That is the point of the ruling
      rather than a side effect: a personal skill used to live in each agent's dir
      independently and drift per agent — `claude` on v2, `codex` on v1, `pi` without it — with
      no command reporting the divergence. One copy cannot diverge. The first apply that adopts
      a destination is CONFIRMED and fails closed on a non-interactive stdin; archiving is the
      fallback for anything that cannot be moved.
    - **Collisions are resolved by CONTENT, not by name.** Byte-identical copies of one name
      union silently (measured: every name shared across four real agent dirs was
      byte-identical). DIFFERING content is a real conflict — both survive as `<name>` and
      `<name>-from-<agent>`, warned about ONCE, at the migration, naming both sources. Losing
      one of two hand-written skills silently is the failure the ruling exists to prevent.
    - **Precedence is the LAYER ORDER**, so the local pack — appended last — outranks every
      shared pack. It did not before: the per-entry rule asked "did THIS PACK write it?", which
      refused any pack overwriting another's recorded name whatever the order, and the local
      pack lost a flat-tier collision (§6a-5). Composition asks only "is this yolo's?", so the
      refusal is unrepresentable rather than handled. **Superseded in part by S1 below:** layer
      order still decides which layer may write a name, but two packs *contending* for one
      unnamespaced name no longer reach that rule at all — it is refused.
    - **The tier survived, narrowed** — and then narrowed again. It decides how the AGENT invokes
      a skill (a namespaced pack gets one subtree, invoked `<pack>:<skill>`; the default writes
      bare names) and therefore what shape yolo writes, never what yolo may overwrite.
      **Superseded by S1/S2 (maintainer ruling 2026-08-05):** it is now a MANIFEST-level
      `"skills_tier"` — one positive choice per pack, honored at every destination — not a
      per-contribution `"tier"`, and unnamespaced is the default:
      - A per-contribution tier declared a GLOBAL property (what a skill is called) at a
        PER-DESTINATION site, so it could not express a consistent name. Worse, a zero-ceremony
        pack borrows its destinations and INHERITED each one's tier, so the user's own local pack
        was namespaced in Claude and flat everywhere else without ever choosing either — one
        skill, two invocation names. The inheritance is gone; a borrowed destination is a
        destination, not a naming policy.
      - **A NAME COLLISION BETWEEN TWO PACKS IS FATAL** at apply time, naming both packs, both
        source paths, and both remedies (rename, or opt one pack into namespacing). Measured
        before the ruling: at flat tier one pack's skill silently won and the loser produced no
        output line at all; at namespaced tier both survived under two names. The error costs the
        deliberate flat-tier override, and that is the ruling's own trade — an intentional
        override and an accidental clash are the same declaration, so yolo cannot tell them apart
        and the user should.
      - The MIGRATION's suffix path is untouched: adopting a user's pre-existing tree still keeps
        both copies (`mine`, `mine-from-codex`). **Adoption preserves, declaration refuses** —
        two different situations, deliberately.
      - A pack still carrying `"tier"` on a contribution is refused BY NAME with the migration in
        the message, rather than failing on the strict decoder's bare `unknown field "tier"`.
  - **`briefing` is GENERATED WHOLESALE**, at every notch (maintainer ruling 2026-08-04,
    shipped-2026-08-pack-batch.md §6a). It was a delimited managed block inside the user's file; that
    mechanism existed to keep an append from growing without bound when source and destination
    are the same file, which accepted a premise the ruling rejects — that a briefing file is
    jointly owned. Three consequences a reader should know:
    - **Pre-existing prose MOVES into the local pack** (`~/.config/yolo-jail/local/AGENTS.md`),
      where yolo composes it back into every destination. So the migration preserves behavior
      rather than merely avoiding deletion. Archiving is the fallback for prose that cannot be
      moved; nothing is ever deleted.
    - **The first apply that adopts a destination is CONFIRMED**, once, and fails closed on a
      non-interactive stdin. Taking wholesale ownership of a file the user wrote is a one-way
      door.
    - **Dropping the last contributing pack archives the destination** rather than leaving a
      generated file with no owner. `after: "host:<path>"` is jail-only as a result: at the host
      the path it names IS the generated destination, so there is nothing left to prepend.

Resolved sharp edges (kept because the reasoning is the interesting part):

- ~~**Two `skills` contributions with the same `into` fail the jail at boot.**~~ **FIXED
  2026-08-02** by deduplicating mounts per destination. The assembler emitted one bind per
  contribution, so podman rejected the second with "duplicate mount destination" — even
  though the footprint model calls skills a safe merge, and `PrepareSkills` had *already*
  merged every pack's skills into each staging dir, making the second mount the same content
  again.

  The documented workaround was "do not declare a `skills` contribution whose `into`
  duplicates another pack's" — and that advice was unfollowable in the configuration it most
  matters for: an agent pack naming `~/.claude/skills` plus a user pack sharing a skills
  corpus is the entire point of the kind. `briefing` had the identical bug for the identical
  reason (`CombineConcat`, prose already merged) and is fixed the same way.

  **`files` is deliberately NOT fixed this way.** A second `files` claimant on one path is a
  genuine sole-ownership violation, so it stays a **pre-flight refusal naming both packs**
  rather than a silent dedup — deduping there would let one pack's content shadow another's.
  Same podman symptom, opposite correct response.

- ~~**A `files` tree can shadow a config surface.**~~ **CAUGHT IN PRE-FLIGHT 2026-08-02.** A
  `files` claim is a `:ro` mount over its whole destination, so a surface the entrypoint must
  write beneath that path hit a read-only filesystem and refused the boot with an error
  naming the *surface*, not the claim that shadowed it — and usually cross-pack, so neither
  author could see it. Now reported before the container exists, with the remedy (narrow the
  `into`).

- ~~**`config` exclusivity was documented and unenforced.**~~ **ENFORCED 2026-08-02**
  (`pack-config-collaboration.md` Option 1 / R1). The table above has always called `config`
  Exclusive — *"a second writer must be `config-overlay`"* — while `manifest.Merge` resolved
  two declarations of one identity last-writer-wins, WHOLE: the survivor brought its own
  `mode`, `path`, `codec` and `defaults`, so a pack could flip another pack's surface from
  `stateful` to `rmw` and silently disable in-jail edit capture for a file it did not own.

  Now a **loud collision**: named in `yolo pack footprint` and `yolo check`, refused at launch
  and by `apply --host`, naming both packs, the fields that already disagree, and the
  `config-overlay` shape to convert to. It could only land after `config-overlay` was wired —
  refusing the incorrect expression before the correct one existed would have been a pure
  regression.

  Two things it is deliberately NOT. An `autonomy` posture patching a surface the same pack
  owns is not a second declaration (it merges into the base surface's managed layer), which is
  what keeps all five agent packs launchable. And a `config-overlay` alongside a `config` on
  one identity is the supported shape, not a clash. Also fixed with it: `yolo pack footprint`
  now reports `config-overlay` claims, which it skipped entirely while the kind was inert.

---

## Where the rest lives

| Topic | Authority |
|---|---|
| Pack manifest schema, field by field | `internal/packdecl/packdecl.go`, `contributes.go`, `kinds.go` (the doc comments are the reference) |
| The `packs` config key, precedence, entry schema | `yolo config-ref` |
| The composition engine internals | `internal/agentcfg` |
| Bringing host files INTO a jail as a user (the `host_files` key) | `docs/plans/host-file-staging.md` |
| Rendering a pack OUT to the host (the invert-the-flow design) | `docs/design/host-render-target.md` |
| The credential/identity boundary a pack respects | `docs/design/agent-credentials.md`, `docs/design/identity-prism-decision.md` |
| Composed-file read/write posture, in depth | `docs/design/composed-file-permissions.md` |
| yolo as a confinement dial (jail / guest / host) | `docs/design/yolo-as-environment-manager.md` |
