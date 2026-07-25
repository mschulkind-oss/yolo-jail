# Host-file staging — a user-extensible set of host files, one engine

**Status:** **SHIPPED 2026-07-25.** All four phases are implemented, unit-tested,
and verified end-to-end in a nested jail (all four modes render; `once` keeps an
in-jail edit, `copy` discards it, `capture` reverts its `managed` key while keeping
the sibling edit; `config ls`/`diff`/`reset` report and clear exactly the diverged
surface). Four `integration/hostfiles_test.go` tests pass against real containers.

Three deviations from the text below are worth knowing:

1. **Modes.** All four (`readonly`/`once`/`copy`/`capture`) shipped as specified.
   [composed-file-permissions.md §7.4](../design/composed-file-permissions.md)
   argues they should collapse to three (`copy` merges into `readonly`, and
   `readonly` should be a `:ro` mount rather than a bare `0o444` chmod, which is
   *asymmetric* — root ignores it while a non-root agent gets EACCES and the
   surface silently stops re-rendering). That change is **not** made here.
2. **Home-root destinations** (`~/.npmrc`, Example 1) do NOT use the
   writable-subtree staging §"Delivery" describes — that makes the destination a
   *directory*. They use the `GlobalHome` relative-symlink hatch instead; see
   [composed-file-permissions.md §7.5](../design/composed-file-permissions.md) for
   why the alternatives break `mode: once`, one of them permanently.
3. **No surfaces are appended to `BuiltinManifest`.** Each user surface renders
   standalone through the extracted surface-taking cores, so the §"Collision
   safety" destination-uniqueness check is enforced at the config layer instead of
   "across the merged manifest".
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
**raw copy is just `codec: "raw"`** — one engine, not two. **Overlay capture is the
exception, not the default:** a file mirroring a host source defaults to `readonly`
(the host file stays the source of truth), a source-less file defaults to `once`
(seed it, then leave it alone — in-jail edits just persist), and the §5
capture-diff overlay engages only when a user explicitly writes `"mode":
"capture"`, because a captured edit outranks `host` forever. See
[Overlay capture is the exception](#overlay-capture-is-the-exception-never-a-default).
Because the construction of any given file is now a real question,
**`yolo config ls`** shows every managed file, its codec, mode, contributing
layers, and whether an overlay is winning. The **credential boundary** is enforced
**per entry**: an entry that
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
- **`yaml` is a phantom — remove it.** It is named in `manifest.knownCodecs`
  (5 names) but absent from `codec.registry` (4 names) — no `yaml.go`, no vendored
  YAML lib, and `codec.go` forbids new deps. A surface declaring `codec: "yaml"`
  passes config validation and then **dies at render**. There is no yaml codec and
  this design does not add one; the fix is to **delete the phantom name** so
  `manifest.knownCodecs` == `codec.CodecNames()` (the 4 real codecs). A user can
  then never name `yaml`, and a `.yaml`/`.yml` file is handled as `raw` bytes.

## The model: one key, string-or-object, lowered to a Surface

`host_files` is a **list**; each item is a **string** or an **object**.

### The object form

| Field | Required | Meaning |
|---|---|---|
| `path` | ✅ | `~`-relative **jail destination** (e.g. `~/.config/mytool/config.json`). The surface's `Path`. |
| `source` | — | host path to seed the `host` layer from. **Its presence makes the entry user-scope only** (a host file crosses). Mutually exclusive with `content`. |
| `content` | — | inline literal seed (a string). Crosses nothing → legal at any scope. Mutually exclusive with `source`. |
| `codec` | — | `json` \| `toml` \| `lines` \| `raw` — the four real codecs. Overrides auto-detect. There is no `yaml` codec (the phantom name is removed — see below); a `.yaml` file is handled as `raw`. |
| `managed` | — | object of yolo-asserted keys that **revert on edit** (re-Enforced each render). Structured codecs only. |
| `defaults` | — | user-overridable base layer. Structured codecs only. |
| `transform` | — | path to a Lua hook. Works on **every** codec, raw included — for a raw surface `ctx.config` is a Lua **string** (see [Transforms on non-object surfaces](#transforms-on-non-object-surfaces)). |
| `mode` | — | `readonly` (`0o444`, re-rendered each boot, no sidecar — edits fail loudly) \| `once` (seed if absent, then never touched — edits just persist, no sidecar) \| `copy` (`0o644`, overwritten each boot, no sidecar — edits silently lost) \| `capture` (`0o644`, **the overlay exception** — re-rendered each boot *and* in-jail edits captured into a sidecar that outranks `host`). **Default:** `readonly` for source-bearing, `once` for source-less — never `capture`; it is always explicit. See [below](#overlay-capture-is-the-exception-never-a-default). |

> **Naming:** the `managed` **field** (keys yolo re-asserts every render) and
> `mode: capture` (persisting in-jail edits) are unrelated. An earlier draft called
> the mode `managed` too; it is `capture` to keep the two apart, since a surface
> commonly has `managed` keys *without* wanting capture.

### Overlay capture is the exception, never a default

An earlier draft of this doc made capture (rw + edit capture) the blanket default
and argued for it. That was wrong. **No `mode` ever gets overlay capture
implicitly** — a user must ask for it by writing `"mode": "capture"`. The reason
falls out of the layer order:

**`overlay` outranks `host`.** `Compose` folds
`defaults < host < workspace < overlay < computed`. So once an in-jail edit is
captured into the overlay sidecar, it **wins over the host file forever** — the
sidecar never ages out. For a file mirroring a host dotfile that silently breaks
the promise made in [Delivery](#delivery-directories-and-refresh) ("editing the
host file propagates on the next launch"): the user edits `~/.npmrc` on the host,
relaunches, and the jail keeps serving the captured version, with the divergence
recorded only in a sidecar they have never heard of. Troublesome *and* hidden *and*
sticky — worse than any failure the alternatives cause.

This is not a hypothetical agent-typo scenario. Tools rewrite their own config as
normal operation: `npm config set` rewrites `~/.npmrc`, `git config --global`
rewrites `~/.gitconfig`, plenty of CLIs rewrite theirs on first run. A captured
surface forks from its source on the first such call — and never rejoins.

Capture also buys the least where it is most tempting. For a *host-mirrored* file
the user already has the natural way to make a change stick: **edit it on the
host**, where it is version-controlled, visible, and shared by every jail.

So the default is the mode that **does not accumulate hidden state**, and it turns
on one question — *does a host source of truth exist?*

| Entry kind | Default `mode` | Why |
|---|---|---|
| **source-bearing** (bare string, or object with `source`) | **`readonly`** | The host file is the source of truth; host edits keep propagating. An in-jail edit fails *at the moment of the edit* rather than being silently reverted or silently made permanent. |
| **source-less** (`content`, or only `managed`/`defaults`) | **`once`** | The jail's copy is the only copy, so yolo seeds it and then leaves it alone. In-jail edits simply persist as ordinary file writes — **no sidecar, no capture, no precedence puzzle**. Re-seeding needs `yolo config reset`. |

Note the source-less default is `once`, not `capture`: for a file with no host
counterpart, "write it if absent, then don't touch it" gives edits-survive-reboot
*without* any overlay machinery. Capture only earns its complexity when a file must
be **both** continuously re-rendered from upstream layers **and** in-jail editable —
e.g. a source-less file whose `managed` keys yolo must keep asserting while letting
the agent tune the rest. That is the exception, and it is spelled
`"mode": "capture"`.

**Honest limit on `readonly`.** It renders the file `0o444`, which stops ordinary
writers and makes an accidental edit fail loudly (`EACCES`) — but it is **DAC, not
kernel enforcement**: an agent running as **root** (Claude YOLO runs UID 0 with
`IS_SANDBOX=1`) bypasses the mode bits, and any agent can `chmod +w`. Truly
unwriteable would require a `:ro` bind mount at the destination, the mechanism
briefings use — but you cannot compose *into* a `:ro` mount, so that trades away
`managed`/`defaults`/`transform` entirely. `readonly` is therefore "the file yolo
renders is not meant to be edited, and casual edits fail" — a strong signal and a
speed bump, not a sandbox. A user who wants kernel-enforced read-only should mount
the file with `mounts` instead of composing it.

### `yolo config ls` — the whole picture, one screen

With four modes, five layers, per-entry codecs, and an optional overlay, the honest
answer to "why does this file look like that?" must be one command, not doc
archaeology. **`yolo config ls`** lists every managed file — builtin and
user-declared — and how it is constructed:

```
$ yolo config ls
SURFACE              PATH                          CODEC  MODE      LAYERS                    OVERLAY
claude/settings      ~/.claude/settings.json       json   managed   defaults host managed      2 keys ⚠
pi/settings          ~/.pi/agent/settings.json     json   managed   defaults managed           –
user/npmrc           ~/.npmrc                      raw    readonly  host                       –
user/ripgreprc       ~/.config/ripgrep/config      raw    once      content                    –
user/mytool          ~/.config/mytool/config.json  json   managed   defaults host managed      1 key ⚠
mise/config          ~/.config/mise/config.toml    toml   copy      computed                   –

⚠ 2 surfaces have captured in-jail edits that outrank their host layer.
  Inspect: yolo config diff claude --surface settings
  Discard: yolo config reset claude --surface settings
```

The value is that **an overlay can never be invisible**: a file whose content
diverges from what its layers would produce is flagged on the listing, in the boot
output, and in `--explain`. This is a gap in what ships **today** — the builtin
surfaces already carry live capture overlays with no user-facing view of them — so
these commands are not new-feature polish, they are the missing half of a mechanism
already in production:

- **`yolo config ls`** — the table above. `--explain` per surface for per-key
  provenance (extends `render --explain`, which already names the winning layer).
- **`yolo config diff <agent> [--surface s]`** — captured overlay vs. a
  freshly-composed host/defaults render. Raw surfaces get a whole-file diff.
- **`yolo config reset <agent> [--surface s]`** — discard the overlay sidecar (and
  re-seed a `once` file), replacing "know to delete a file in `<workspace>/.yolo/`
  by hand".
- **Boot-time notice** — any surface rendering with a non-empty overlay prints
  `~/.npmrc: 2 keys from captured in-jail edits (yolo config diff)`.

### The string (sugar) form

A bare string `"~/.foo/bar"` means: **`source` == that host path**, `path` ==
same `~`-relative destination, codec **auto-detected** from the extension, and
`mode: readonly` (the source-bearing default — the host file stays the source of
truth). It is therefore always **source-bearing** → user-scope only. A
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
managed lifecycle** as structured ones (intent #4).

## Transforms on non-object surfaces

A Lua transform works on a raw surface too — `ctx.config` is simply a **string**
instead of a table. Lua is good at strings (`string.gsub`, `string.format`,
`..`), and the plumbing is *already* mostly there:

- **The marshaller handles it today.** `luahook/marshal.go` `goToLua` has a
  `case string: return lua.LString(val)`, and `luaToGo` has the mirror
  `case lua.LString: return string(v)`. Scalars round-trip losslessly right now.
- **The sandbox already opens Lua's `string` library** (`vm.go` `openSandboxLibs`
  opens base/string/table/math), so `string.gsub` et al. are in scope for a hook.

Only two things actually block it, and both are narrow type declarations, not
missing capability:

| Blocker | Location | Change |
|---|---|---|
| `Ctx.Config` is typed `map[string]any` | `luahook/luahook.go` | widen to `any` |
| `Run` asserts the hook left an object: ``cfgMap, ok := cfg.(map[string]any)`` → error "want an object/table" | `vm.go` ~115 | accept a non-object when the surface's codec is non-object |

So the design **widens the interface** rather than declaring raw un-transformable:

```lua
-- config.lua — rewrite a host path that means nothing inside the jail
yolo.transform("user", function(ctx)
  if ctx.surface == "npmrc" then
    ctx.config = ctx.config:gsub("/Users/matt/", "/home/agent/")
  end
end)
```

Type discipline, so a hook can't silently corrupt a surface:

- `ctx.config`'s Lua type is **determined by the surface codec** — table for
  `json`/`toml`, string for `raw`, table (array) for `lines`. A hook can rely on it.
- A hook that returns the **wrong kind** for the codec is a **loud fail-closed
  error** (the existing `wrapLuaErr` path), not a coerced write — the current
  object assertion generalizes to "must match the codec's kind" instead of
  "must be an object".
- `ctx.managed` on a raw surface is the whole-file string (or absent), consistent
  with the `managed` semantics below.

`Enforce()` also needs the same non-object branch as `Compose` (whole-value
replace instead of key merge) — `enforceValue` already replaces on a type
mismatch, so this is a small extension, not a redesign. What this design still
does **not** do is generalize the *whole* `map[string]any` value model
(`deepMerge`, provenance, `mergeDiff`): non-object surfaces bypass those by
construction (whole-value replacement), which is why the change stays small.

**Who applies the captured edit when there is no merge?** The *same*
`ComposeStateful` loop — "capture" and "re-incorporate" are codec-agnostic; only
the *combine* step differs. For a structured codec, combine = RFC-7386 deep-merge
(the overlay is a key-level patch). For raw, combine = **whole-value replacement**,
so the pipeline degenerates cleanly:

1. **Capture:** each boot, `ComposeStateful` diffs the on-disk file against the
   `last_render` sidecar. For raw the "diff" is just inequality — if the bytes
   changed, the **entire edited string becomes the new `overlay`** (stored as a
   JSON string in the same sidecar).
2. **Re-incorporate:** on the next render the layer order
   `defaults < host < overlay < managed` replaces rather than merges, so a
   non-empty `overlay` (the user's edited content) **wins over `host`/`defaults`**.
   That is exactly the read-write, edit-survives-reboot behavior — no merge engine
   needed, because for a single opaque blob "the merge" and "the last write" are
   the same thing.

The one real consequence: **`managed` on a raw surface is whole-file** — a raw
`managed` value replaces the entire rendered file, so it means "pin these exact
bytes," not "pin this key." That is coarse and rarely what you want; `managed`
(and `defaults`) are really structured-codec features. A raw surface that needs
byte-for-byte pinning is better expressed as `mode: copy` or `mode: readonly`.

## Codec auto-detection

A small extension→codec map beside `codec.registry`:

| Extension | Codec |
|---|---|
| `.json` | `json` |
| `.toml` | `toml` |
| everything else (incl. no ext, `.sh`, `.yaml`, `.yml`, `.jsonc`) | `raw` |

`.yaml`/`.yml` → `raw`: there is no yaml codec and this design does not add one
(a real one would need a vendored dep + `go mod vendor` + a `goSrc` fileset
update). `.jsonc` → `raw` on purpose: routing it through the `json` codec would
sort keys and **drop comments**. The object form's explicit `codec` always wins.
Structured codecs (`json`/`toml`) give key-by-key overlay capture; `raw` gives
whole-file capture. Both are managed.

**Prerequisite fix:** remove the phantom `yaml` from the codec validation split —
make `manifest.knownCodecs` derive from `codec.CodecNames()` (the 4 real codecs)
so a user can never declare a codec that validates but fails at render.

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
- **Refresh** depends on the mode. `readonly`/`copy`/`capture` re-render every boot
  in the entrypoint (same as `settings.json`), re-reading host `source` bytes from
  the `:ro` mount — so a host-side edit propagates on the next launch. `once` does
  not: it is seeded when absent and then left alone, so later host edits do *not*
  propagate (that is the trade for "in-jail edits persist without a sidecar", and
  `yolo config reset` forces a re-seed). Only `capture` writes overlay sidecars.

## Collision safety

`manifest.New` dedups only `(Agent, Name)` — **not** `Path`. So two user entries
writing the same destination, or a user entry clobbering `~/.claude/settings.json`
(and thus stripping yolo's managed block), would pass. Guards:

1. Run every de-sugared destination through the **reserved-home-segment guard**
   (`internal/config/writablehome.go`'s `checkWritableHomeDir`) so a user file
   can't clobber a yolo-managed mount/overlay or
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

User scope only (each bare string is source-bearing). All three therefore default
to **`mode: readonly`**: the host file stays the source of truth, a host-side edit
propagates on the next launch, and an in-jail edit fails loudly rather than
silently forking. `.gitignore_global`/`.npmrc` have no useful extension → `raw`;
`config.json` → `json`.

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

The entry below opts *into* the capture exception, which is the only way to get it:
`mytool` rewrites its own config as it runs, and those in-jail changes should
persist across boots even though a host copy exists. The cost is explicit and now
visible — `yolo config ls` flags the surface, and `yolo config reset` undoes it.

```jsonc
{
  "host_files": [
    "~/.gitignore_global",
    { "path": "~/.config/mytool/config.json",
      "source": "~/.config/mytool/config.json",
      "mode": "capture" }   // opt in: capture in-jail edits (overlay outranks host)
  ]
}
```

### Example 2 — source-less files (intent #4), legal at workspace scope

Nothing crosses the host boundary, so a **repo** may ship these in its
`yolo-jail.jsonc`. The first is source-less with no `managed` keys → default
**`mode: once`**: yolo writes it if absent, then never touches it, so an in-jail
edit simply persists with no sidecar involved.

The second needs continuous enforcement (yolo must keep asserting `telemetry:
false`) *and* in-jail editability of everything else — that is precisely the case
that earns the capture exception, so it says `"mode": "capture"` explicitly.

```jsonc
// /workspace/yolo-jail.jsonc  — OK even at workspace scope (no `source`)
{
  "host_files": [
    {
      "path": "~/.config/ripgrep/config",
      "content": "--max-columns=200\n--smart-case\n"   // codec auto → raw; mode once
    },
    {
      "path": "~/.config/mytool/settings.json",
      "mode": "capture",                                    // opt in: re-render + capture
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
    "~/.pi/agent/models.json",   // json codec, readonly (host is source of truth)
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

1. **Remove the phantom `yaml` codec** — make `manifest.knownCodecs` derive from
   `codec.CodecNames()` (the 4 real codecs) so a declared codec can never validate
   then fail at render.
2. **Non-object branch in `Compose`** (`compose.go` object assertion): raw/lines
   do whole-value replacement through the same pipeline + §5 sidecars. Same branch
   in `Ctx.Enforce`/`enforceValue`. Add the Compose-level tests raw/lines lack.
3. **Widen the transform interface to non-object surfaces** — `Ctx.Config`
   `map[string]any` → `any`; `vm.go`'s post-hook assertion becomes "the value's
   kind matches the surface codec" (fail-closed otherwise) instead of "must be an
   object". `goToLua`/`luaToGo` already handle strings, and the sandbox already
   opens Lua's `string` lib, so no marshaller or sandbox change. Test: a raw
   surface transformed with `string.gsub`; a hook returning a table for a raw
   surface errors loudly.

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
   iterate the user surfaces calling `renderSurfaceStateful` (**only** `mode:
   capture` — the sole sidecar-writing path) or `renderSurfaceComputed`
   (`readonly`/`copy`/`once` — no sidecars; `readonly` chmods `0o444`, `once`
   skips an existing file), and the recursive-copy path for directories. Apply the
   per-kind mode default (source-bearing → `readonly`, source-less → `once`) at
   de-sugar time, not here.
8. **Register writable subtrees** for each destination parent (reuse
   `writable_home_dirs` staging) so composed writes don't EROFS on podman.
9. **Host `source` mounts** — a `hostFileArgs` sibling binds `:ro` at
   `/ctx/host-user/<slug>`; fail-open read.
10. **Collision guards** — reserved-home-segment guard + destination-`Path`
    uniqueness across the merged manifest.

### Phase 3 — `yolo config ls` + capture visibility (see [above](#yolo-config-ls--the-whole-picture-one-screen))

Not optional polish. With four modes and five layers, "how is this file
constructed?" must be answerable by a command; and `mode: capture` is only
defensible if divergence is visible and reversible. These apply to the **builtin**
surfaces too, which have carried silent capture overlays since the prism cutover.

11. **`yolo config ls`** — one row per surface (builtin + user): surface, path,
    codec, mode, contributing layers, overlay-key count with a ⚠ when non-empty,
    plus the footer pointing at `diff`/`reset`. Data all exists —
    `manifest.Surfaces()` plus `Result.Provenance`.
12. **Boot-time divergence notice** — when a surface renders with a non-empty
    overlay, print `<path>: N keys from captured in-jail edits (yolo config diff)`
    in the startup output.
13. **`yolo config diff <agent> [--surface s]`** — captured overlay vs. a
    freshly-composed host/defaults render. Extends `render --explain`'s existing
    per-key provenance (which already names `overlay` as a winning layer); raw
    surfaces get a whole-file diff.
14. **`yolo config reset <agent> [--surface s]`** — discard the overlay sidecar
    (and re-seed a `once` file), replacing "know to delete a file in
    `<workspace>/.yolo/` by hand".

### Phase 4 — docs + config-ref

15. **`internal/cli/config_ref.txt`** — a `host_files` block: the string|object
    union, codec auto-detect table, per-entry scope rule, the four modes + their
    per-kind defaults, and that capture is opt-in.
16. **`docs/design/agent-credentials.md`** — add `host_files` to the credential
    matrix; the per-entry source-bearing = user-scope boundary.
17. **`docs/design/jail-home.md`** — user surfaces in the home overlay; writable
    subtree registration; the composed-wins ordering vs. a dir copy.
18. **`agent-settings-composition.md`** — annotate D4 (reversed + generalized),
    fix the §4 layer table's `agent_config.<agent>` claim (decided-but-unwired), and
    record that `ctx.config` is no longer always an object.

## Test plan

- **Unit (codec/compose)**: raw + lines round-trip through `Compose` (whole-value
  replacement) and through `ComposeStateful` (overlay capture of a raw edit);
  `knownCodecs` == `CodecNames()`.
- **Unit (transform on non-object)**: a raw surface transformed via `string.gsub`
  round-trips; a hook that leaves a table on a raw surface (or a string on a `json`
  surface) fails loudly; `ctx.managed` on a raw surface reads as the whole-file
  value.
- **Unit (config)**: string and object entries validate; auto-detect maps
  `.json`/`.toml`/else correctly; explicit codec overrides; `source`⊕`content`
  enforced; `path` outside `$HOME`/`..`/`:` rejected; dir entry flagged `IsDir`.
- **Unit (mode defaults)**: a source-bearing entry with no `mode` resolves to
  `readonly`; a source-less one to `once`; an explicit `mode` always wins; **no
  default ever resolves to `capture`** (the capture-is-opt-in invariant).
- **Unit (sidecars)**: only `mode: capture` produces `last_render`/`overlay`
  sidecars; `readonly`/`copy`/`once` write none.
- **Unit (scope)**: a **source-bearing** entry at workspace scope → hard error; a
  **source-less** entry at workspace scope → OK; `LoadHostFiles` reads
  source-bearing only from user config; `inJail()` skips the host-source side.
- **Unit (collision)**: two entries with the same `path` → error; an entry whose
  `path` is a builtin surface (`~/.claude/settings.json`) or a reserved segment →
  error.
- **Nested-jail (mandatory)**: fresh temp workspace + temp `$HOME` with a user
  config carrying (a) a raw sugar file, (b) a `json` sugar file, (c) a source-less
  `content` entry, (d) an explicit `mode: capture` entry, (e) a dir; run
  `./dist-go/linux-$(go env GOARCH)/yolo -- bash`; confirm the sugar files land
  `0o444` and an edit fails `EACCES`, the `content` file lands writable and survives
  a relaunch with no sidecar, the `managed` entry's managed key reverts after an
  in-jail edit + re-render while its other edits are captured, `yolo config ls`
  flags exactly that one surface, `yolo config reset` clears it, and a
  workspace-scope source-bearing entry hard-errors.

## Non-goals

- **No `yaml` codec**, and the phantom `yaml` name is deleted from
  `manifest.knownCodecs`. `.yaml`/`.yml` files are handled as `raw` bytes. (A real
  yaml codec would need a vendored dep + `go mod vendor` + a `goSrc` fileset
  update — deliberately out of scope.)
- **No generalization of the object value model.** `deepMerge`, per-key
  provenance, and `mergeDiff` stay object-only; non-object surfaces bypass them via
  whole-value replacement. (Transforms *do* work on raw — see
  [Transforms on non-object surfaces](#transforms-on-non-object-surfaces) — but a
  raw surface gets no per-key provenance or key-level diff.)
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
- **`.jsonc`/`.yaml`/`.yml` → `raw`** — preserve bytes; no lossy re-encode. The
  phantom `yaml` codec name is **removed**, not deferred.
- **Overlay capture is the exception, never a default** — `readonly` for
  source-bearing, `once` for source-less; `capture` only when explicitly written.
  A captured edit outranks `host` forever, so implicit capture would silently and
  permanently fork a host-mirrored file.
- **Transforms work on every codec** — the interface widens (`Ctx.Config` → `any`,
  kind-checked against the surface codec) rather than declaring raw
  un-transformable; Lua handles strings natively and the marshaller already does.
- **`yolo config ls` is in scope** — with four modes and five layers, file
  construction must be inspectable by command. It plus `diff`/`reset` also close a
  gap that exists for the builtin surfaces today.
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
