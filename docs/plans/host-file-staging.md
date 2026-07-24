# Host-file staging — a user-extensible set of host files, one engine

**Status:** design, not yet built (2026-07-24).
**Supersedes:** the `## 10` retirement decisions in
[agent-settings-composition.md](agent-settings-composition.md) — specifically
**D4** ("hard-error, as if it never existed"). This plan reopens a user-scope
knob D4 removed, and generalizes it: instead of a bespoke raw-copy, the knob
lowers into the *same* composition engine that already generates `settings.json`.
D1–D3 (commit `a84b11c`) stand — the per-agent *builtin* host files stay
yolo-declared; this plan adds a **user** path beside them, it does not touch them.

## The decision, in one paragraph

A user must be able to bring **any** file need into the jail **without editing
yolo's source** — today every composed surface is a Go literal in
`BuiltinManifest`, and that must stop being the only way. So: one user-facing
config key, **`host_files`**, whose entries are either a **string** (sugar: bring
this host file/dir in, codec auto-detected) or an **object** (rich: pick the
codec, a Lua transform, inline `content`, `managed`/`defaults` layers). Every
entry **de-sugars in-memory into a `manifest.Surface`** appended to the builtin
manifest, and a generic boot loop renders it through the existing pipeline. A
**raw copy is just `codec: "raw"`** — one engine, not two. Composed user files
are **read-write and editable in-jail**, and yolo **manages** them: the §5
capture-diff overlay captures in-jail edits, and any `managed` keys revert on the
next render. The **credential boundary** is enforced **per entry**: an entry that
names a host **`source`** (including bare-string sugar) is **user-scope only** — a
workspace config can never make a host file cross — while a **source-less** entry
(inline `content`, or pure `managed`/`defaults`) crosses nothing and is legal at
any scope.

## Background: where we are, and why this reopens

Commit `a84b11c` retired `host_claude_files` / `host_pi_files` per §10.4. It did
three good things and one wrong thing:

| Landed | Verdict |
|---|---|
| **D1/D2** — moved the per-agent *builtin* host files to a fixed, yolo-declared registry (`internal/agents.AgentSpec.HostFiles`: claude ⇒ `.claude/settings.json`, pi ⇒ `.pi/agent/settings.json`) | **Keep.** These are yolo's own defaults; they stay Go-declared. |
| **D3** — deleted the bespoke per-agent pathways (`appendSettingsScripts`, the claude/pi `syncHost*Files` twins) | **Keep.** The special-casing is gone for good. This plan does not bring per-agent code back — it adds one *generic* user path. |
| **D4** — dropped both keys from `knownTopLevelConfigKeys` so any occurrence **hard-errors** | **Reverse, and generalize.** D4 removed the *legitimate* user ability to bring extra host files (pi's `models.json`, `themes/*.json`) into the jail. The replacement is not a narrow raw-copy — it is the general `host_files` mechanism below. |

The error in D4 was treating *"the set of host files that cross into the jail is a
credential boundary"* as *"no config may ever widen it."* The real boundary is
narrower and **per entry**: a config may not make a **host file cross** unless it
is the **user's own** config. Bringing a *source-less* managed file into being
crosses nothing, so even a workspace config may do that. That distinction is the
spine of this design.

## What was already true (facts this builds on)

Verified against the implementation (2026-07-24), because the first draft of this
doc got two of them backwards:

- **Composed files are read-WRITE.** `renderSurfaceStateful` writes the composed
  output `0o644` via `writeInPlaceString` into the agent overlay dir
  (`~/.claude`, `~/.pi`), which is a **writable** bind (`assemble.go` ~162, no
  `:ro`). The only `:ro` mount is the *host source* input at `/ctx/host-<agent>/`.
- **In-jail edits are captured.** The §5 capture-diff overlay is fully wired
  (`internal/agentcfg/staterender.go` `ComposeStateful` + `prism.go`
  `renderSurfaceStateful`): each boot it diffs the on-disk file against the
  `last_render` sidecar, folds the delta into the durable `overlay` sidecar, and
  re-renders with the overlay outranking host/computed.
- **`managed` reverts by re-Enforce, not a file mode.** `compose.go` re-applies
  the managed layer *after* the overlay and the Lua hook. §9 explicitly rejected
  the read-only-file approach ("that file is `rw` in the jail… managed stays a
  layer, never an OS file").
- **A surface owner need not be a real agent.** `BuiltinManifest` already carries
  non-agent owners (`mise`, `agy`); `renderSurfaceStateful`/`renderSurfaceComputed`
  are **surface-agnostic** — they take `(agent, name)` as data and `Lookup` the
  manifest. A source-less user surface is exactly the shape of `ConfigureAgyPrism`
  (nil host bytes, nil computed).
- **`raw` is a real codec.** `codec.registry` maps `raw` → byte-exact
  passthrough. But **the compose engine is object-only today**: `compose.go`
  asserts the decoded host layer is `map[string]any` and errors otherwise, and
  `luahook.Ctx.Config` is typed `map[string]any`. So `raw` (string) and `lines`
  (`[]any`) are registered and unit-tested but flow through **zero** surfaces and
  cannot pass `Compose` unchanged. Unifying them is a real (small) code change —
  see [Making `raw` a first-class codec](#making-raw-a-first-class-codec).
- **`yaml` is a phantom.** It is named in `manifest.knownCodecs` (5 names) but
  absent from `codec.registry` (4 names) — no `yaml.go`, no vendored YAML lib,
  and `codec.go` forbids new deps. A surface declaring `codec: "yaml"` passes
  config validation and then **dies at render**. This validation split must be
  reconciled before users can name codecs.

## The model: one key, string-or-object, lowered to a Surface

`host_files` is a **list**; each item is a **string** or an **object**.

### The object form

| Field | Required | Meaning |
|---|---|---|
| `path` | ✅ | `~`-relative **jail destination** (e.g. `~/.config/mytool/config.json`). The surface's `Path`. |
| `source` | — | host path to seed the `host` layer from. **Its presence makes the entry user-scope only** (a host file crosses). Mutually exclusive with `content`. |
| `content` | — | inline literal seed (a string). Crosses nothing → legal at any scope. Mutually exclusive with `source`. |
| `codec` | — | `json` \| `toml` \| `lines` \| `raw`. Overrides auto-detect. (`yaml` is not yet real — see must-avoid.) |
| `managed` | — | object of yolo-asserted keys that **revert on edit** (re-Enforced each render). Structured codecs only. |
| `defaults` | — | user-overridable base layer. Structured codecs only. |
| `transform` | — | path to a Lua hook (structured codecs only; a transform on a raw surface is unsupported — identity). |
| `mode` | — | `managed` (default: §5 capture, editable, managed) \| `copy` (pure overwrite each boot, no edit capture) \| `once` (seed only if absent). |

### The string (sugar) form

A bare string `"~/.foo/bar"` means: **`source` == that host path**, `path` ==
same `~`-relative destination, codec **auto-detected** from the extension, `mode:
managed`. It is therefore always **source-bearing** → user-scope only. A
directory string (or one ending `/`) routes to the recursive-copy path
(directories are not a codec — see below). This is intent #3: the 90% case stays
a flat list.

### De-sugaring

Every entry lowers to a `manifest.Surface` with a synthetic owner
**`Agent: "user"`** and `Name` = a slug derived from `path` (so the `(Agent,
Name)` key space never collides with builtin agent surfaces — `manifest.New`
already validates + dedups on that key). The user surfaces are appended to
`BuiltinManifest` via `manifest.New(...)`, and a **generic boot loop** renders
each one. No per-file Go, no new lifecycle machinery — `renderSurfaceStateful`
already does the work.

## Making `raw` a first-class codec

This is what makes "a raw copy is just a codec" literally true (intent #2),
without rewriting the engine (which the judge panel scored as overkill and
high-risk). Add **one non-object branch** in `Compose` at the object assertion:
when the decoded value is not a `map[string]any` (raw `string`, lines `[]any`),
**skip deep-merge + the Lua transform + object-Enforce** and do **whole-value
replacement** in ascending layer order (`defaults < host < overlay < managed`,
each simply replaces), then `Encode`.

This keeps the §5 sidecars working unchanged: the `overlay` sidecar is always
JSON, and a raw string stores fine as a JSON string, so `ComposeStateful`'s
capture/re-incorporate loop gives raw files the **same read-write, editable,
managed lifecycle** as structured ones (intent #4). What you do **not** do:
generalize `luahook.Ctx.Config` or the whole `map[string]any` value model — a
transform on a raw surface is simply unsupported.

## Codec auto-detection

A small extension→codec map beside `codec.registry`:

| Extension | Codec |
|---|---|
| `.json` | `json` |
| `.toml` | `toml` |
| everything else (incl. no ext, `.sh`, `.yaml`, `.yml`, `.jsonc`) | `raw` |

`.yaml`/`.yml` → `raw` **for now** (no yaml codec exists; building one needs a
vendored dep + `go mod vendor` + a `goSrc` fileset update — out of scope).
`.jsonc` → `raw` on purpose: routing it through the `json` codec would sort keys
and **drop comments**. The object form's explicit `codec` always wins. Structured
codecs (`json`/`toml`) give key-by-key overlay capture; `raw` gives whole-file
capture. Both are managed.

**Prerequisite fix:** reconcile the codec validation split — make
`manifest.knownCodecs` derive from `codec.CodecNames()` (the 4 real codecs, no
`yaml`) so a user can never declare a codec that validates but fails at render.

## The credential boundary — per entry

*Which host files cross into the jail* is a **credential boundary**: an entry that
names a `source` can forward `~/.ssh/id_ed25519`, `~/.aws/credentials`, or any
secret. A **workspace** `yolo-jail.jsonc` travels with the repo and is
agent-editable, so it must never make a host file cross. But a **source-less**
entry (`content`, or pure `managed`/`defaults`) copies *nothing from the host* —
it just brings a yolo-managed file into being — so it is safe at any scope.

Hence the rule is **per entry**, not per key:

| Entry kind | Crosses a host file? | Allowed scope |
|---|---|---|
| source-bearing (bare string, or object with `source`) | **yes** | **user config only** |
| source-less (object with `content`, or only `managed`/`defaults`) | no | user **or** workspace |

Enforcement is the exact `cache_relocations` precedent
(`internal/config/relocations.go`, `validateCacheRelocations`):

- **Source-bearing entries are read only from `paths.UserConfigPath()` directly**
  (+ its `include_if_found`), never from the merged/workspace/snapshot config — so
  workspace scope is *inexpressible by construction*.
- `validateHostFiles` re-reads `LoadWorkspaceConfig` and **hard-errors** on any
  source-bearing entry found there — defense-in-depth against a silent no-op, not
  the boundary itself.
- The host-source filesystem probe is **`inJail()`-gated** (host paths aren't in
  the jail's mount namespace; probing them in-jail would turn a valid host config
  into a fatal error on every nested run — the bug `cache_relocations` already hit).

> **User scope = the human is trusted.** Nothing blocks a user from listing
> `~/.ssh/…` in their *own* `host_files` — their machine, their call, and a
> blocklist is unenforceable (symlinks). The boundary is that the **repo** cannot
> make that choice on their behalf.

## Delivery, directories, and refresh

- **Composed output** lands in the jail home via the existing overlay-dir
  mechanism. A user surface whose `path` is under a **new** directory (e.g.
  `~/.config/foo/`) needs that subtree made **writable**: the jail home is `:ro`
  and writable subtrees derive from `AgentSpec.OverlayDirs` + `writable_home_dirs`.
  So the loader must **register each user destination's home-relative parent as a
  writable subtree** (reusing the `writable_home_dirs` staging), or the composed
  write EROFS-fails on podman. This is the one required plumbing addition beyond
  the config + render loop.
- **Host `source`** crosses `:ro` at `/ctx/host-user/<slug>`, mounted by a
  `hostFileArgs` sibling (the `hostclaude.go` pattern), and read fail-open (absent
  mount → nil → the surface falls back to `defaults`, exactly like
  `ConfigurePiPrism`).
- **Directories** are **not** a codec (codec is strictly per-file: `os.ReadFile`).
  A directory entry (string ending `/`, or object with a dir `source`) routes to a
  **recursive copy/stage** step (reuse the skills/`writable_home_dirs` copy +
  reserved-segment guard), *not* the compose engine. `mode: copy` is implied for
  directories.
- **Refresh:** structured/managed surfaces regenerate every boot in the entrypoint
  (same as `settings.json`). Host `source` bytes are re-read from the `:ro` mount
  each boot, so editing the host file propagates on the next launch.

## Collision safety

`manifest.New` dedups only `(Agent, Name)` — **not** `Path`. So two user entries
writing the same destination, or a user entry clobbering `~/.claude/settings.json`
(and thus stripping yolo's managed block), would pass. Guards:

1. Run every de-sugared destination through the **reserved-home-segment guard**
   (`writablehome.go`) so a user file can't clobber a yolo-managed mount/overlay or
   a builtin agent surface path.
2. Add a **destination-`Path` uniqueness check** across the merged manifest.

## macos-user — accepted deficiencies, not design constraints

Composition runs wherever it must (host-side on `macos-user`, which has no
container/entrypoint and no bind mounts). Per the maintainer's direction, the
design is **not** bent to fit `macos-user`; instead the gaps are recorded and
revisited when `macos-user` is shaped up:

- **Composed user files are not read-only there** (the native `/Users/_yolojail`
  home is writable) — accepted.
- **No workspace/jail isolation of the §5 sidecars** on the shared native home —
  accepted; noted for the `macos-user` pass.
- **Host `source` entries can't bind-mount** (no `/ctx`), so they **fail-open to
  `defaults`** — a source-less managed entry still works via the pure generator; a
  source-bearing one degrades. Accepted.

## Worked examples

### Example 1 — the common case (intent #3): flat sugar list

User scope only (each bare string is source-bearing). `.gitignore_global` and
`.npmrc` have no useful extension → `raw` (whole-file capture); `config.json` →
`json` (key-by-key capture, so an in-jail edit to one key survives regeneration).

```jsonc
// ~/.config/yolo-jail/config.jsonc  — user scope
{
  "host_files": [
    "~/.gitignore_global",
    "~/.npmrc",
    "~/.config/mytool/config.json"
  ]
}
```

### Example 2 — source-less managed files (intent #4), legal at workspace scope

Nothing crosses the host boundary, so a **repo** may ship these in its
`yolo-jail.jsonc`. Both are read-write and editable in-jail; `managed` keys revert.

```jsonc
// /workspace/yolo-jail.jsonc  — OK even at workspace scope (no `source`)
{
  "host_files": [
    {
      "path": "~/.config/ripgrep/config",
      "content": "--max-columns=200\n--smart-case\n"   // codec auto → raw
    },
    {
      "path": "~/.config/mytool/settings.json",
      "defaults": { "telemetry": false, "theme": "dark" },  // user may retheme…
      "managed":  { "telemetry": false }                    // …but telemetry stays off
    }
  ]
}
```

### Example 3 — rich: seed from host, explicit codec, transform, one managed key

User scope only (`source` present). The dir entry is copied wholesale.

```jsonc
// ~/.config/yolo-jail/config.jsonc  — user scope
{
  "host_files": [
    {
      "path": "~/.config/starship.toml",
      "source": "~/.config/starship.toml",
      "codec": "toml",
      "transform": "~/.config/yolo-jail/starship.lua",
      "managed": { "add_newline": true }
    },
    { "path": "~/.config/nvim/", "source": "~/dotfiles/nvim/", "mode": "copy" }
  ]
}
```

### Example 4 — the motivating pi case, in the new model

Replaces the retired `host_pi_files`. `models.json` composes as JSON (managed if
yolo ever needs to assert a provider key; plain today); `themes/` is a dir copy.

```jsonc
// ~/.config/yolo-jail/config.jsonc  — user scope
{
  "agents": ["claude", "pi"],
  "host_files": [
    "~/.pi/agent/models.json",   // json codec, key-by-key capture
    "~/.pi/agent/themes/"        // directory → recursive copy
  ]
}
```

`~/.pi/agent/settings.json` is still the yolo **builtin** surface (composed with
its managed block); these entries sit beside it. A user entry may **not** name
`settings.json` as its `path` — the collision guard rejects clobbering a builtin
surface.

### Example 5 — what a workspace config canNOT do (hard error)

```jsonc
// /workspace/yolo-jail.jsonc  — travels with the repo
{
  "host_files": ["~/.ssh/id_ed25519", "~/.aws/credentials"]  // source-bearing
}
```

```
Invalid jail config:
  config.host_files[0]: an entry that names a host source is user-scope only —
  move it to ~/.config/yolo-jail/config.jsonc (a workspace config travels with the
  repo and is agent-editable, so it cannot decide which host files cross into the
  jail). A source-less entry (inline `content`, or only `managed`/`defaults`) is
  allowed here.
```

## Work items

Phased; each phase is one atomic commit. The loader + validator must land
together (a half-migration is a silent no-op).

### Phase 0 — engine prerequisites

1. **Reconcile the codec validation split** (`manifest.knownCodecs` →
   `codec.CodecNames()`), so a declared codec can never validate then fail at
   render.
2. **Non-object branch in `Compose`** (`compose.go` object assertion): raw/lines
   do whole-value replacement through the same pipeline + §5 sidecars. Add the
   Compose-level tests raw/lines currently lack.

### Phase 1 — config schema + loader + validator

3. **`internal/config/hostfiles.go` (new)** — mirror `relocations.go`:
   - `const hostFilesKey = "host_files"`.
   - `type HostFileEntry` capturing `Path`, `Source`, `Content`, `Codec`,
     `Managed`, `Defaults`, `Transform`, `Mode`, and `IsDir`.
   - `LoadHostFiles(warn)` — reads **source-bearing** entries only from
     `paths.UserConfigPath()` (+ includes); source-less entries from the merged
     config; `inJail()` early-return for the host-source side; malformed → skip+warn.
   - `checkHostFiles(v, probeFS)` — shared shape/polymorphism validation
     (string|object), codec auto-detect + explicit-codec check, `source`⊕`content`
     exclusivity, `path` under `$HOME`, `..`/`:` rejection, per-entry scope
     classification.
4. **`internal/config/validate.go`** — `validateHostFiles`: re-read
   `LoadWorkspaceConfig`, hard-error on any **source-bearing** entry at workspace
   scope; `probeFS = !inJail()`.
5. **`internal/config/config.go`** — add `"host_files"` to
   `knownTopLevelConfigKeys`.

### Phase 2 — de-sugar + render

6. **De-sugar to `manifest.Surface`** (owner `user`, slug name), appended to the
   builtin manifest; codec auto-detect applied here.
7. **Generic boot render loop** — after the hardcoded agent switch in `boot.go`,
   iterate the user surfaces calling `renderSurfaceStateful` (managed) or
   `renderSurfaceComputed` (`mode: copy`), and the recursive-copy path for
   directories.
8. **Register writable subtrees** for each destination parent (reuse
   `writable_home_dirs` staging) so composed writes don't EROFS on podman.
9. **Host `source` mounts** — a `hostFileArgs` sibling binds `:ro` at
   `/ctx/host-user/<slug>`; fail-open read.
10. **Collision guards** — reserved-home-segment guard + destination-`Path`
    uniqueness across the merged manifest.

### Phase 3 — docs + config-ref

11. **`internal/cli/config_ref.txt`** — a `host_files` block: the string|object
    union, codec auto-detect table, per-entry scope rule, `mode`.
12. **`docs/design/agent-credentials.md`** — add `host_files` to the credential
    matrix; the per-entry source-bearing = user-scope boundary.
13. **`docs/design/jail-home.md`** — user surfaces in the home overlay; writable
    subtree registration; the composed-wins ordering vs. a dir copy.
14. **`agent-settings-composition.md`** — annotate D4 (reversed + generalized),
    and fix the §4 layer table's `agent_config.<agent>` claim (decided-but-unwired).

## Test plan

- **Unit (codec/compose)**: raw + lines round-trip through `Compose` (whole-value
  replacement) and through `ComposeStateful` (overlay capture of a raw edit);
  `knownCodecs` == `CodecNames()`.
- **Unit (config)**: string and object entries validate; auto-detect maps
  `.json`/`.toml`/else correctly; explicit codec overrides; `source`⊕`content`
  enforced; `path` outside `$HOME`/`..`/`:` rejected; dir entry flagged `IsDir`.
- **Unit (scope)**: a **source-bearing** entry at workspace scope → hard error; a
  **source-less** entry at workspace scope → OK; `LoadHostFiles` reads
  source-bearing only from user config; `inJail()` skips the host-source side.
- **Unit (collision)**: two entries with the same `path` → error; an entry whose
  `path` is a builtin surface (`~/.claude/settings.json`) or a reserved segment →
  error.
- **Nested-jail (mandatory)**: fresh temp workspace + temp `$HOME` with a user
  config carrying (a) a raw sugar file, (b) a `json` sugar file, (c) a source-less
  managed object, (d) a dir; run `./dist-go/linux-$(go env GOARCH)/yolo -- bash`;
  confirm each lands writable in the home, the managed key reverts after an in-jail
  edit + re-render, a raw in-jail edit is captured, and a workspace-scope
  source-bearing entry hard-errors.

## Non-goals

- **No full engine generalization.** The `map[string]any` value model stays;
  raw/lines get a thin non-object bypass in `Compose`, not a rewrite of
  `luahook.Ctx` / deep-merge / provenance.
- **No `yaml` codec** until one is deliberately built (vendored dep + `go mod
  vendor` + `goSrc` fileset). `.yaml`/`.yml` → `raw` meanwhile.
- **No transform on a raw surface** — identity only; a transform requires a
  structured codec.
- **No arbitrary host→container mapping.** `host_files` destinations are
  `$HOME`-relative; arbitrary paths into `/ctx` remain `mounts` (`:ro`).
- **No tree-staging glob executor** (the §3.3 `ctx.stage` vaporware — its only
  consumer is a `config render` display line). A flat list + per-entry codec +
  recursive dir copy covers the need.

## Decisions (settled) and remaining forks

**Settled** (maintainer direction, 2026-07-24):

- **One key, string|object union** (not two keys) — unify the sugar and rich
  forms; internally everything is a Surface.
- **Raw is a codec** — a copy is `codec: "raw"`, one engine (intent #2).
- **Per-entry credential gating** — source-bearing = user-scope-only; source-less
  = any scope. (Chosen over wholesale key-level gating: a repo may legitimately
  ship a source-less managed file.)
- **Directories in v1** — separate recursive-copy path (dirs aren't a codec).
- **Bare string == source-bearing** — a source-less file uses the object form
  with `content`/`defaults`.
- **`.jsonc`/`.yaml`/`.yml` → `raw`** — preserve bytes; no lossy re-encode, no
  yaml codec yet.
- **macos-user is not a design constraint** — composition runs there; the gaps
  (not read-only, no sidecar isolation, source fail-open) are recorded
  deficiencies to revisit during the `macos-user` pass, not schema limits.

**Still open (worth a look before implementation):**

- **`managed`/`defaults` merge semantics for a user surface** mirror the builtin
  surfaces (RFC-7386 object merge, `append` pins). Confirm no user-surface needs
  array-append pinning in v1 (defer if not).
- **Slug scheme for `Name`** — a path-derived slug must be stable and
  collision-free across entries; pin the exact derivation (e.g. cleaned
  home-relative path with separators mapped) when implementing.
