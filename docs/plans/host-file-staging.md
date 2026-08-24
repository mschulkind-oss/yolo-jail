# Host-file staging — a user-extensible set of host files, one engine

**Status: ✅ SHIPPED 2026-07-25.** All four phases are implemented, unit-tested,
and verified end-to-end in a nested jail. **Read [What is built, and what is
not](#what-is-built-and-what-is-not) first** — the body of this doc is the original
DESIGN and still reads in the future tense; that section is the authority on what
the code actually does and what remains open.

**Supersedes:** the `## 10` retirement decisions in
[agent-settings-composition.md](agent-settings-composition.md) — specifically
**D4** ("hard-error, as if it never existed"). This plan reopens a user-scope
knob D4 removed, and generalizes it: instead of a bespoke raw-copy, the knob
lowers into the *same* composition engine that already generates `settings.json`.
D1–D3 (commit `a84b11c`) stand — the per-agent *builtin* host files stay
yolo-declared; this plan adds a **user** path beside them, it does not touch them.

## What is built, and what is not

> **How to read the rest of this doc.** Everything below this section is the
> original design, written before implementation, in the future tense ("the loader
> must…", "add one non-object branch…"). It was *followed*, so it is still an
> accurate account of the reasoning — but where it and the code disagree, **the code
> wins and this section says so.** Individual sections carry a `> **Built:**` or
> `> **⚠ Deviation:**` note where they need one.

### Shipped

| Phase | What landed | Where |
|---|---|---|
| **0** — engine prerequisites | phantom `yaml` removed (`knownCodecs` derives from `codec.Names()`); non-object branch in `Compose`/`Enforce`; transform interface widened to `any`, kind-checked per codec | `internal/agentcfg/{compose.go,manifest,codec,luahook}` |
| **1** — config schema | `host_files` key, `HostFileEntry`, `LoadHostFiles`, `checkHostFiles`, `validateHostFiles`, the per-entry scope rule | `internal/config/hostfiles.go` |
| **2** — de-sugar + render | the four modes, directory copy, `/ctx/host-user/<slug>` reads, `YOLO_HOST_FILES` wire form | `internal/entrypoint/hostfiles.go` |
| **2** — host-side wiring | `YOLO_HOST_FILES` emission, `:ro` source mounts, destination staging | `internal/cli/run/hostfiles.go` |
| **2** — macos-user | source-less entries only (`SourceLessHostFilesFrom`) | `internal/macosuser/runplan.go` |
| **3** — visibility | `yolo config ls` / `diff` / `reset` + a boot-time divergence notice | `internal/cli/config{ls,diff}.go`, `internal/entrypoint/prism.go` |
| **4** — docs | `host_files` block in `config-ref`; `agent-credentials.md` §2.4; `jail-home.md` §2.8; D4 annotated | — |
| **tests** | unit coverage per phase, plus 4 real-container tests | `integration/hostfiles_test.go` |

Verified in a nested jail: all four modes render; `once` keeps an in-jail edit,
`copy` discards it, `capture` reverts its `managed` key while keeping the sibling
edit; a destination under a brand-new top-level dir renders (the case that
EROFS-failed before staging existed); `ls`/`diff`/`reset` report and clear exactly
the diverged surface.

### Deviations from the design below

1. **`path` is OPTIONAL when `source` is a `~/…` path** — it defaults to the same
   home-relative destination, so a mirrored file names its path once. Required only
   for a source-less entry, or a `source` outside `$HOME`. (Added after review; the
   field table and Examples 1/3 below are updated to match.)
2. **Home-root destinations** (`~/.npmrc` — Example 1) do **not** use the
   writable-subtree staging [Delivery](#delivery-directories-and-refresh)
   describes; that would make the destination a *directory*. They use the
   `GlobalHome` relative-symlink hatch. See
   [composed-file-permissions.md §7.5](../design/composed-file-permissions.md) for
   why each alternative breaks `mode: once`, one of them permanently.
3. **No surfaces are appended to `BuiltinManifest`.** Each user surface renders
   standalone through the extracted surface-taking render cores, so the
   [Collision safety](#collision-safety) destination-uniqueness check is enforced
   at the config layer rather than "across the merged manifest".
4. **`yolo config ls` defaults to surfaces whose file exists** (`--all` for the
   whole manifest), which the [§ sketch](#yolo-config-ls--the-whole-picture-one-screen)
   does not mention. Presence is only knowable in-jail, so host-side the column is
   suppressed rather than guessed.

## Scope: the line

**This is the authoritative in/out list.** The rule it enforces: *nothing may look
like it works and then not.* Anything in the **Out** column below is either rejected
outright with a validation error, or documented as a known limit in
`yolo config-ref` — never silently half-working. If you find something that
validates and then does not behave, that is a bug in this table, not a feature gap.

Out-of-scope items are tracked as **ROADMAP open item #3, "composed-file follow-ups"**
([sequencing-2026-07.md](sequencing-2026-07.md)), which is where they get sequenced. This doc stays the
design of record for the `host_files` key itself and is **closed** to new scope.

### In — shipped and supported

| Capability | Guarantee |
|---|---|
| string sugar + object form, one `host_files` key | validated by `yolo check`; malformed entries are errors, not skips |
| codecs `json` / `toml` / `lines` / `raw`, auto-detected by extension | a declared codec is accepted iff something can decode it (`knownCodecs` derives from the registry) |
| modes `readonly` / `once` / `copy` / `capture` | per-kind defaults; `capture` is never implicit |
| `defaults` / `managed` / `transform` layers | RFC-7386 object merge; transform works on every codec incl. `raw` |
| directory entries (recursive copy) | `mode: copy` implied; composition keys rejected for a dir |
| per-entry credential boundary | source-bearing entries are **unreadable** from workspace scope by construction, plus a hard `yolo check` error |
| destination staging (`.config/*`, home-root files, new top-level dirs) | all three cases verified in a nested jail |
| `yolo config ls` / `diff` / `reset` + boot notice | captured edits cannot be invisible |
| comments on a `raw` surface | **byte-exact** round trip, verified |

### Out — deliberately not covered, and how that is enforced

| Not covered | How a user finds out | Tracked |
|---|---|---|
| **`yaml` codec** | `codec: "yaml"` is a `yolo check` **error** naming the four real codecs and saying `.yaml` is handled as `raw`. The phantom name was deleted so it cannot validate-then-die. | won't do (needs a vendored dep) |
| **Comments preserved on a `json`/`toml` surface** | The only item here that cannot announce itself with an error, so it is **documented in `config-ref` under `codec`** — naming a structured codec on a commented file discards them; `raw` keeps them byte-exact. See [below](#the-one-gap-that-needed-closing-to-hold-the-line). | #3 |
| **In-jail-added comments captured back** | Never captured, never reverted; documented as one-way host→jail. | #3 |
| **`managed`/`defaults` array-append pinning** | Object merge only; an array in `managed` replaces rather than appends. Shape-checked at config time, so no surprise at render. | #3 |
| **`readonly` as a kernel-enforced `:ro` mount** | It is `0o444` DAC. Documented in config-ref as "a strong signal and a speed bump, not a sandbox", with the root-bypass called out. | #3 |
| **Source-bearing entries on `macos-user`** | Filtered out (`SourceLessHostFilesFrom`) rather than rendered without their host layer. | accepted deficiency |
| **Single-file `:ro` on Apple Container** | apple/container#1089; source-less entries compose fine. | accepted deficiency |
| **Arbitrary host→container paths** | Destinations are `$HOME`-relative; an absolute path is a `yolo check` error pointing at `mounts`. | won't do (by design) |

### The one gap that needed closing to hold the line

Everything in the Out table announces itself with a validation error except **comment
loss on a `json`/`toml` surface**. A user points `host_files` at a commented `.jsonc`
and gets `codec: raw` by auto-detect, so comments survive — but the moment they write
`"codec": "json"` to get key-level merging, the comments vanish with no warning. That
is exactly the "looks like it works and then doesn't" case, and it cannot be an error
because both halves of the trade are legitimate.

**✅ Closed, without building any preservation machinery:** `yolo config-ref` now
states the trade under `codec` — a structured codec parses into keys and re-emits,
discarding every comment; `raw` keeps the bytes exactly, at the cost of key-level
merging and the `defaults`/`managed` layers. "Pick per file: raw to preserve a
hand-written config, json/toml to compose one." The user meets the limit before they
hit it, so the line holds without deciding anything about preservation.

Deciding *how* to preserve them is #3's business, not this doc's; the reasoning and
ranked options are parked in
[Comments, and why `.jsonc`/`.yaml` fall back to `raw`](#comments-and-why-jsoncyaml-fall-back-to-raw)
below, with the sub-questions (staleness, attachment, in-jail additions) answered
there so #3 starts from decisions rather than a blank page.

### Resolved, for the record

- **Slug scheme** — settled: reversible percent-escape with `_` as the sole
  sentinel (`HostFileEntry.Slug`), injective by construction, unit-tested.
- **Naming for the recovered state** — proposed vocabulary in
  [Naming](#naming-what-to-call-the-recovered-state) (*overlay* = the layer,
  *overlay sidecar* = the file, **"captured edits"** = the user-facing term; not
  "managed", which is taken). Renaming the Go identifiers is a mechanical pass
  under #3.

### Not this feature's bugs, but found while building it

Both are pre-existing prism defects, tracked under #3 so they do not get lost:

- **`copilot/config` can lose an OAuth token** on a first-migration boot — it
  renders statefully with `Defaults: {"yolo": true}` and no host layer, so an
  absent/corrupt sidecar reduces a token-bearing file to one key.
  [composed-file-permissions.md §4.2](../design/composed-file-permissions.md).
- **Reserved destinations miss symlink *targets*** — `~/.config/git/config`,
  `~/.config/bashrc` and `~/.claude/claude.json` validate while their aliases
  (`~/.gitconfig`, `~/.bashrc`, `~/.claude.json`) are rejected. [§4.5
  there](../design/composed-file-permissions.md).

## The decision

**The problem.** Every file yolo composes is a Go literal in `BuiltinManifest`.
That means a user who wants one more file in the jail — pi's `models.json`, their
own `~/.npmrc` — has to edit yolo's source. That must stop being the only way.

**The mechanism.** One config key, **`host_files`**, taking a list whose entries
are either:

- a **string** — `"~/.npmrc"`: bring this host file (or `dir/`) in at the same
  path, codec auto-detected. The 90% case stays a flat list.
- an **object** — pick the codec, add a Lua transform, supply inline `content`,
  layer `managed`/`defaults` on top.

Each entry lowers into a `manifest.Surface` and renders through the pipeline that
already generates `settings.json`. **A raw copy is just `codec: "raw"`** — so this
adds a config key, not a second engine.

**The default is the mode that accumulates no hidden state.** It turns on one
question — *is there a host source of truth?*

| Entry | Default | Why |
|---|---|---|
| source-bearing | `readonly` | the host file stays authoritative; host edits keep propagating |
| source-less | `once` | seed it, then leave it alone — in-jail edits just persist |

The §5 capture-diff overlay engages **only** when a user writes
`"mode": "capture"` explicitly, because a captured edit outranks `host` *forever*
— see [Overlay capture is the exception](#overlay-capture-is-the-exception-never-a-default).
And because "how was this file built?" is now a real question, **`yolo config ls`**
answers it: every managed file, its codec, mode, contributing layers, and whether
an overlay is winning.

**The credential boundary is per entry, not per key.** An entry naming a host
`source` (bare-string sugar included) is **user-scope only** — a workspace config,
which travels with the repo and is agent-editable, can never make a host file
cross. A source-less entry copies nothing from the host, so it is legal anywhere.

### Why this reopens something we just closed

Commit `a84b11c` retired `host_claude_files` / `host_pi_files`. Three of its four
decisions were right and stand untouched: the per-agent *builtin* host files became
a fixed yolo-declared registry (**D1/D2**, `agents.AgentSpec.HostFiles`), and the
bespoke per-agent pathways were deleted for good (**D3** — this plan brings back no
per-agent code, only one generic path).

**D4 is the one to reverse.** It dropped both keys so any occurrence hard-errors,
and in doing so removed a legitimate ability: bringing *extra* host files into the
jail at all. The mistake was reading *"the set of host files that cross is a
credential boundary"* as *"no config may ever widen it."* The boundary is narrower
than that — a config may not make a host file cross **unless it is the user's own**.
Bringing a source-less managed file into being crosses nothing, so even a workspace
config may do it. That distinction is the spine of everything below.

## What was already true (facts this builds on)

Before designing anything, it was worth checking what the engine could already do
— the first draft of this doc guessed twice and got both wrong, so what follows was
read out of the tree (2026-07-24) rather than assumed.

The good news was that most of the machinery already existed. **Composed files are
read-write**, not read-only as the draft had it: `renderSurfaceStateful` writes
`0o644` into the agent overlay dir, which is a writable bind — the only `:ro` mount
in the picture is the host *input* at `/ctx/host-<agent>/`. **In-jail edits are
already captured** too: the §5 overlay loop is fully wired, diffing the on-disk file
against the `last_render` sidecar each boot, folding the delta into the durable
`overlay` sidecar, and re-rendering with the overlay outranking host and computed.
So the "surviving edits" half of this feature needed no new mechanism at all.

Worth being precise about how `managed` actually enforces, since the name suggests
a file mode: it doesn't touch permissions. `compose.go` simply re-applies the
managed layer *after* the overlay and the Lua hook, so a managed key wins the merge
in the generated file. §9 considered and rejected the read-only-file approach in as
many words — "that file is `rw` in the jail… managed stays a layer, never an OS
file."

The most encouraging discovery was that **a surface owner need not be a real
agent**. `BuiltinManifest` already carries `mise` and `agy`, and the render helpers
take `(agent, name)` as plain data. A source-less user surface is structurally
identical to `ConfigureAgyPrism` — nil host bytes, nil computed — which meant the
user path could reuse the boot renderer wholesale.

Two things did need fixing first, and both are now done (Phase 0), so the two
bullets below describe the *old* tree and are kept only for the reasoning:

- **`raw` was registered but unreachable.** `codec.registry` mapped `raw` to a
  byte-exact passthrough, but the engine was object-only — `compose.go` asserted
  `map[string]any` and `luahook.Ctx.Config` was typed the same way. So `raw` and
  `lines` were unit-tested and flowed through *zero* surfaces. Making "a copy is
  just a codec" literally true meant unifying them; see
  [Making `raw` a first-class codec](#making-raw-a-first-class-codec).
- **`yaml` was a phantom.** `manifest.knownCodecs` accepted it while
  `codec.registry` had no implementation, so a surface declaring `codec: "yaml"`
  passed `yolo check` and then died at render — validated, then fatal. The fix was
  to delete the name and derive the accepted set from the registry, which is what
  now happens.

## The model: one key, string-or-object, lowered to a Surface

`host_files` is a **list**; each item is a **string** or an **object**.

### The object form

| Field | Required | Meaning |
|---|---|---|
| `path` | ⚠ conditional | `~`-relative **jail destination** (e.g. `~/.config/mytool/config.json`). The surface's `Path`. **Optional when `source` is a `~/…` path** — it then defaults to the same home-relative destination, so mirroring a host file needs the path only once. Required for a source-less entry, and for a `source` outside `$HOME` (`/etc/foo.conf` has no unambiguous home-relative counterpart). |
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

### Naming: what to call the recovered state

**Open, and worth settling** — the thing an agent naturally reaches for "managed
state" is already three different concepts, and the codebase currently uses four
names for one of them. Counted across Go and Markdown: *overlay sidecar* (35),
*captured in-jail edits* (15), *capture-diff overlay* (13), *capture overlay* (10).

Do **not** call it "managed" anything. That word is taken twice over:

| Term | Means | Where |
|---|---|---|
| `managed` (the field/layer) | keys **yolo asserts** and re-applies after the Lua hook, so they revert on edit | manifest `Managed`, `host_files.managed` |
| `mode: capture` | the **posture** that turns edit-recovery on | `host_files.mode` |
| *(this)* | the **recovered content** — what an in-jail edit left behind, which now outranks `host` | `<workspace>/.yolo/prism/<surface>.overlay.json` |

Naming them all "managed" would collapse the exact distinction the design rests on:
`managed` keys are what yolo **wins**, and this is what the **agent** wins.

The four current terms are not really synonyms — they name different aspects, which
is why they all survive. That suggests keeping a small deliberate vocabulary rather
than picking one winner:

- **overlay** — the *layer* in the fold (`defaults < host < workspace < overlay <
  computed`). Correct in engine/layer contexts; already the code's identifier.
- **overlay sidecar** — the *file* on disk. Use when the subject is a path.
- **captured edits** — the *content*, in user-facing prose. This is the one to
  standardize on for CLI output and docs, because it says whose they are and where
  they came from without any yolo jargon. (`yolo config ls` and the boot notice
  already use it.)
- ~~capture-diff overlay~~ — the *mechanism* (diff vs `last_render`). Accurate but
  it puts implementation in the name; keep it to §5 where the mechanism is the
  subject, and prefer "overlay" elsewhere.

If a single umbrella term is wanted, **"captured edits"** is the candidate: it is
already the most user-legible, it is what the two shipped commands print, and it
cannot be confused with `managed`. Renaming the code identifiers is a mechanical
follow-up, not part of this plan.

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

> **✅ Built** (`internal/cli/configls.go`, `configdiff.go`). The table below is a
> sketch: real output also carries an `(absent)` marker in-jail, lists only
> existing surfaces unless `--all` is passed, and names host_files surfaces by
> `user/<slug>`. `diff` additionally flags a captured value equal to yolo's last
> render as a *redundant* capture — which is what most captured keys turn out to be.

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

> **✅ Built** exactly as described (`compose.go` branches on `codec.Kind`;
> `Ctx.Enforce`/`enforceValue` gained the same whole-value branch).

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

> **✅ Built** as described: `Ctx.Config` is `any`, and `vm.go` checks the returned
> value against the surface's `codec.Kind` (fail-closed) rather than "must be an
> object".

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

### Comments, and why `.jsonc`/`.yaml` fall back to `raw`

> **Status: not built, and the conclusion is that it is smaller than it looks.**
> Raised in review — could we handle `.jsonc`/`.yaml` properly, "even if it's a
> subset of comments"? — then narrowed by three follow-up ideas: hoist comments
> into a header block, ask whether the jail wants comments at all, or just link to
> a verbatim original. The second reframes the problem and the third turns out to be
> nearly free, so they are taken first below.

The `raw` fallback costs something real. A `.jsonc` surface gets whole-file capture
instead of key-level, no `managed`/`defaults` (those need keys), and a Lua transform
that must do string surgery rather than table edits. For a hand-written config full
of explanatory comments — exactly the kind a user brings in via `host_files` — that
is the difference between composable and merely copied.

**Reading these formats is already solved.** `internal/json5` parses comments and
trailing commas today (it is how yolo reads its own `yolo-jail.jsonc`), and it is
in-repo — no vendoring, no `goSrc` change. `BurntSushi/toml` is already vendored and
its lexer emits comment tokens.

**Writing them back is the wall.** Both *discard* comments on the way in:
`json5.skipWS` treats `//` and `/* */` as whitespace, and BurntSushi's encoder has
no comment support whatsoever. The `Codec` interface is
`Decode([]byte) (any, error)` / `Encode(any) ([]byte, error)`, where `any` is the
generic value model — a `map[string]any` has nowhere to put "there was a comment
above this key." So a comment-preserving codec is not a parser swap; it needs
somewhere for the trivia to *live* across the round trip. Three ways to give it one,
in ascending order of cost:

#### First: does the jail even want comments?

**Ask this before building anything, because it mostly dissolves the problem.**
A comment exists to tell a *human editing the file* why it is the way it is. Inside
the jail, who is that human?

- For a **`readonly`** or **`copy`** surface: nobody. The file is regenerated from
  layers every boot and the jail-side copy is not the editing venue — you edit the
  *host* file, where the comments live untouched. A comment in the rendered output
  is decoration.
- For a **`once`** surface: the agent might edit it, and then a comment saying
  "yolo seeded this, host edits do not propagate" is genuinely useful — but that is
  a comment *yolo* should write, not one carried from a source.
- For a **`capture`** surface: an in-jail edit round-trips through the overlay
  sidecar, which today is JSON and carries values only — so a comment would not
  survive capture as currently built. **That is a decision, not a constraint**: the
  sidecar is engine-internal and `staterender.go` says so outright ("its on-disk
  format is yolo's choice, not the surface's"). It was chosen as JSON because JSON
  is the one codec that round-trips `null` tombstones. Widening it to carry trivia
  is work, but nothing stops it.

So the mode axis says: **the more read-only a surface is, the less its comments
matter** for *editing* purposes, because a read-only file is not somewhere you were
going to explain yourself. That much holds.

#### But there is a second reader, and it changes the answer

The framing above asks "who *edits* this file?" and gets "mostly nobody, so mostly
nobody needs the comments." That question is too narrow. **The agent reads config it
never edits**, precisely to work out *why* something is the way it is — and a
comment is often the only place that intent exists at all:

```jsonc
// pinned: 2.14 regressed on the arm64 builder, see PR #412
"toolVersion": "2.13.1",
// upstream default is 30s; our CI runner is slow
"timeout": 90
```

Strip those and the value survives while the *reason* is destroyed. An agent asked
to change that timeout has no way to know it was deliberate — and the trail is
exactly what a coding agent should be reading before it edits anything. This is a
better argument for preservation than the editing one, and it applies to the modes
where the editing argument said comments do not matter (`readonly`, `copy`), because
being unwritable does not make a file unread.

Two consequences for the ranking further down:

1. **"Nobody needs these comments" is wrong as a blanket claim.** The right claim is
   narrower: nobody needs them *in order to edit the jail-side copy*. Reading is a
   distinct need that survives the mode analysis.
2. **Losing them on the way IN is the thing to avoid.** Once the composed file drops
   a comment, the reason is gone from everything downstream — the render, the
   capture sidecar, the agent's view. That argues for the cheap options below
   (which keep the trail reachable) over doing nothing, even if full in-place
   fidelity stays out of scope.

Worth noting what already works: a `raw` surface preserves comments **byte-exact**
(verified), so today the lossless path exists — it just costs key-level merging. The
gap is specifically a **structured** surface (`json`/`toml`) whose comments are
dropped at `Decode` and are then unrecoverable from the rendered file.

#### Idea: extract every comment into a header/footer block

Rather than keep comments *in place*, hoist them: strip all of them on decode,
concatenate, and re-emit as one verbatim block at the top (or bottom) of the
rendered file. Nothing is lost, and the body stays clean structured data the engine
can merge key-by-key.

Attractive, and it has one hard blocker plus one soft one:

- **Strict JSON has no comment syntax at all.** `encoding/json` rejects `//`, so a
  `json`-codec surface has nowhere to put the block — it would have to become
  `.jsonc`-with-a-`json`-codec, i.e. output that its own consumer may refuse to
  parse. `toml` (`#`) and `lines`/`raw` are fine. So this is a TOML-and-friends
  feature, not a JSON one, which is awkward given JSON is the widest surface.
- **Detached comments are close to noise.** `// only on macOS` is meaningful two
  lines above the key it qualifies and nearly useless in a pile at the top of the
  file. Preserving the *bytes* while destroying the *association* satisfies a
  checklist, not a reader.

Verdict: not worth it as stated. The salvageable half is the narrow version —
preserve only the **leading** comment block (the "what is this file" preamble),
which is the one comment whose meaning does not depend on position. Cheap (~40
lines, no interface change), and it composes with the "yolo writes the header
itself" answer above.

#### Idea: link to a verbatim original instead

Do not preserve comments in the rendered file; point at the untouched source. Two
ways, and the first is nearly free:

**For a source-bearing entry, the pristine original is already in the jail.** It is
bind-mounted `:ro` at `/ctx/host-user/<slug>` — verified present and readable. So the
whole feature could be one line in the yolo-authored header:

```jsonc
// Generated by yolo from ~/.config/mytool/config.json — original: /ctx/host-user/<slug>
```

That is strictly better than hoisting comments: the reader gets the *entire* original
with its comments in their original positions, and the rendered file stays clean.
It also composes with `yolo config ls`/`diff`, which already answer "how was this
built?" — the mount path just closes the last gap ("…and what did it look like
before?"). Cost is a header line and a doc sentence.

The gap is **source-less** entries, where there is no original to point at — the
composed file *is* the only artifact. The closest equivalent is pointing at the
config that declared it (`~/.config/yolo-jail/config.jsonc`, itself `:ro`-mounted
and readable in-jail), which is where the user wrote whatever comments they had.

#### The real preservation options

**Trivia sidecar** — decode to `(value, trivia)`, where trivia maps a key path to
its attached comments, and re-attach on encode. This is the option that actually
serves the reading-the-trail case, because it keeps a comment *next to the key it
explains* rather than hoisted or hyperlinked. Full fidelity for keys that survive a
merge, and it composes with the layer model because the *value* stays the plain
generic model — `deepMerge`, provenance and the whole engine are untouched.

What it costs, honestly:

- **`Codec` grows a second, optional interface.** `TriviaCodec` alongside `Codec`, so
  the four existing codecs are unaffected and only `json`/`toml` opt in.
- **The overlay sidecar must widen** from "the generic value model" to
  "value + trivia". This is the part I previously called impossible and should not
  have — the sidecar is engine-internal and its format is explicitly yolo's own
  choice. The reason it is JSON is `null` tombstones, and a `{"value":…,"trivia":…}`
  envelope keeps that property. It is a migration (existing sidecars must still
  load, which the §3.3 first-migration path already handles by re-seeding), not a
  blocker.
- **Merging raises a question the value model never had.** If `managed` overwrites a
  key, does the user's comment above it stay? Defensible answer: **trivia follows the
  key, not the value** — the comment explains *why this key is here*, which usually
  survives a value change. But it is a genuinely new rule, and the wrong answer is
  actively misleading (a stale comment justifying a value that is no longer there is
  worse than no comment).
- **Comment→key association is heuristic.** "The comment block immediately above,
  plus a trailing same-line comment" is the convention every formatter uses; it is a
  convention, not a fact, and a comment floating between two keys has to pick one.

##### The three hard sub-questions, and cheap answers to each

**① Staleness — what if the value changes under the comment?** The failure mode is
worse than losing the comment: a comment justifying a value that is no longer there
actively misleads. Three candidate rules, and only the last is safe:

| Rule | Behavior | Verdict |
|---|---|---|
| trivia follows the key | comment stays even when a higher layer overwrites the value | **unsafe** — this is exactly how you get `// pinned to 2.13` sitting above `"2.15"` |
| trivia follows the value | comment travels with whichever layer won | wrong shape; layers other than `host` have no comments to travel |
| **trivia is dropped when its value is overridden** | the comment survives iff the `host` layer's value survived | **safe by construction** |

The third is the one to take, and it is *cheaper* than the others: a comment is
emitted only when the rendered value for that key came from the layer the comment
came from. Provenance already records exactly that (`Result.Provenance` maps key →
winning layer), so the check is one map lookup per key and needs no new bookkeeping.
The cost is under-preservation — a `managed` override silently drops the user's
comment — which is the right way to be wrong here. Better a missing explanation than
a lying one.

**② What if the agent ADDS a comment in the jail?** Best answer: **don't support it.**
Not because it is hard, but because it has no destination. The comment would live in
the capture sidecar, outrank the host file forever, and be visible to nobody on the
host — the same "hidden, sticky, troublesome" trap that made capture opt-in in the
first place. An agent that wants to record a reason has better venues: the workspace
(committed, reviewable), or the host file, which it can *read* but should not be
editing through a composed surface anyway.

Concretely: **comment capture is one-way — host → jail, never back.** On capture, the
diff considers values only; a comment the agent added is neither preserved nor
reverted, it is simply not tracked, and the next render drops it. That is a *documented
loss*, not silent: `yolo config diff` would be the place to say "in-jail comments are
not captured". Cheap to implement (it is the current behavior) and it keeps trivia
strictly a projection of the host source, which also makes ① trivially correct.

**③ Which surface actually needs this?** Worth checking before building anything —
and the answer for the highest-value surface is: **none of it.**

Verified in this jail: `~/.claude/settings.json` is **pure JSON with zero comments**,
in both the host source (`/ctx/host-claude/settings.json` — no `//` at all) and the
render. Claude Code ships no `$schema` on it and the file is plain `json` codec. So
the widest, most-managed, highest-stakes surface has **no comments to lose**, and
strict JSON means it could not carry them anyway (`encoding/json` rejects `//`).

That is the single most useful fact in this whole section: **the surface where getting
it right matters most does not need it.** The comment problem belongs to
user-declared `host_files` entries pointing at hand-written configs — `.jsonc`,
`starship.toml`, a tool's `config.toml` with a "why" above a key. Real, but a
narrower and lower-stakes population than the agent settings.

**Format-preserving CST** — parse to a concrete syntax tree, mutate in place,
re-serialize byte-identically except where changed (`toml-edit`-style). The only
approach that survives arbitrary formatting, at the price of a dependency per
format, a second value model in the engine, and `Encode(any)` no longer being the
whole contract. Still out of proportion — but note it is the only option that
preserves a comment through a *capture* round trip without the trivia envelope,
since it never lowers to the generic model at all.

#### The escape hatch: default to `readonly` when undecided

There is a way to duck all of the above, and it is worth stating as a policy rather
than leaving implicit. **A surface that has no captured state has no staleness
problem, no attach problem, and no in-jail-comment problem** — because it is rendered
fresh from its layers every boot and nothing is recovered from the previous one.

So the rule for anything whose semantics are not fully decided: **ship it `readonly`
(or `once`), never `capture`.** That is already the default (source-bearing →
`readonly`, source-less → `once`; `capture` is always explicit), which means the
policy is *already enforced* — this section just names why that default is load-
bearing rather than merely conservative. Every hard question in this section is a
question about **captured** state; decline capture and they do not arise.

The corollary is a decision rule for the comment work: **do the reading-the-trail
options first** (point at the `:ro` original, yolo-authored header) because they work
identically for every mode and add no recovered state. Only reach for trivia in the
sidecar if someone specifically needs comments preserved *through capture* — which,
given ② says in-jail comments are not captured anyway, is a narrow case.

#### Where this lands

The goal is now explicitly **"do not lose the reasoning on the way in"**, not "match
the source byte-for-byte". Those want different things, and the cheap options serve
the first.

**Ranked by value per unit of work:**

1. **Point at the original** (`/ctx/host-user/<slug>` in a yolo-authored header) —
   near-free, and for the *reading-the-trail* case it delivers the most: the whole
   untouched file, comments in position, one path away. It does not put the comment
   next to the key, so it is a pointer to the trail rather than the trail itself.
2. **Yolo writes its own header** — a `Generated by yolo, edits do not persist`
   line. No comment-parsing at all. Blocked on the same strict-JSON problem, so it
   applies to `toml`/`lines`/`raw` only.
3. **Trivia sidecar** — the only option that keeps a comment *beside the key it
   explains*, which is what the agent-reading-for-intent case actually wants.
   Promoted from "if fidelity ever matters" once that reader was accounted for.
   Real work (an optional codec interface, a widened overlay envelope, and a
   trivia-follows-the-key merge rule), so it wants a decision, not a drive-by.
4. **Leading-comment preservation** — the cheap subset of ①/③, if a user's own
   preamble is worth keeping on its own.
5. **CST** — still out of proportion, unless byte-exact round-tripping of a
   structured surface becomes a requirement in itself.

Two facts to keep in view. **`raw` already round-trips `.jsonc` byte-exact, comments
and all** (verified) — so the lossless path exists today at the cost of key-level
merging, and a user who values comments over merging can have them right now. And
the **specific** gap is narrower than "comments": it is a `json`/`toml` surface,
where comments die at `Decode` and are unrecoverable from the render. Nothing here is
on the critical path, but ① is small enough that shipping it while ③ is undecided
would already close most of the reading-the-trail complaint.

**YAML stays `raw` regardless.** No in-repo reader (so even the cheap options need a
vendored dep first), and its whitespace-significant, multi-document, anchor-bearing
syntax is where a naive round trip does the most damage. `.yaml` → `raw` is the right
default regardless of what happens above.

#### What shipped: option 3, for `rmw` only, and the reason it was small there

**Done 2026-08-12** (`internal/entrypoint/tomltrivia.go`). Option 3 — the trivia option,
the only one that keeps a comment *beside the key it explains* — turned out to cost almost
none of what is priced above, **for one mode**. Three of its four listed costs are costs of
CAPTURED STATE: widening the overlay envelope, the sidecar migration, and the staleness rule
needing somewhere to live. An `rmw` surface has no captured state at all, and its source and
destination are the same file, read and rewritten in one operation. So the option collapses
to "scan the comments on the way in, put them back on the way out" — no sidecar, no
migration, no `TriviaCodec` on the engine's interface, and nothing touching the shared
emitter that `stateful` and the render-fingerprint gate depend on.

**The mode is not an implementation detail here — it is the argument.** `rmw`'s contract
already says "preserve everything yolo does not declare"; comments are part of everything,
so the mode was violating its own promise. The other two modes are different problems, not
smaller versions of this one:

| mode | ruling |
|---|---|
| `rmw` | **preserve** — contract-mandated, and cheap for the reason above |
| `computed` | **do not preserve, and this is correct.** yolo is the sole author; there is no user comment in the file. A comment in that output would be one yolo *wrote*, which is option 2, a different feature |
| `stateful` | **still open.** The file is composed, so a comment can only come from the `host` layer and preserving it is a PROJECTION from one file into another — the case this whole section was about. Still wants the optional codec interface, trivia surviving the Lua transform boundary, and ① keyed on `Result.Provenance` |

Four of the sub-questions above resolved as written, one corrected:

- **① staleness** — taken as ruled, translated to the mode where the file IS the layer the
  comment came from: a comment survives iff the render did not change the value under it.
- **② in-jail additions** — untouched; `rmw` keeps no sidecar, so there was never a channel
  for a comment to travel back through.
- **③ which surface needs it** — confirmed by construction rather than by inspection. Every
  shipped `json` surface has *nothing to lose*: strict JSON has no comment syntax, so a
  commented file does not decode and the RMW path REFUSES it, byte-untouched. That is now
  pinned by a test rather than left as an observation about today's `settings.json`.
- **the concession that ① "silently drops the user's comment"** — corrected. Every drop is
  reported by key through `HostRenderResult.Formatting`, in observe as well as assert, so the
  user sees which comment goes before the write happens.

**What is deliberately still lost, and reported rather than fixed:** key ORDER (the emitter
is canonical, so a re-emitted file is sorted), a comment block detached from what follows by
a blank line anywhere but the top or bottom of the file, a comment inside a multi-line value,
and anything under an `[[array of tables]]`. Hoisting those somewhere they did not come from
is the "detached comments are close to noise" failure this section already rejected, so they
are counted and named instead.

## The credential boundary — per entry

> **✅ Built** as described, including the `inJail()`-gated probe. One addition:
> `LoadHostFiles` does not early-return in-jail (source-bearing entries are still
> needed there to re-emit the wire form); only the filesystem *probe* is gated.

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

> **⚠ Deviation in the first bullet.** Registering the destination's parent as a
> writable subtree is what a NEW TOP-LEVEL DIR gets. A **home-root file**
> (`~/.npmrc`) cannot use it — a directory bind makes the destination a directory,
> and a pre-created empty backing file breaks `mode: once` permanently (`os.Stat` on
> a bind-mounted empty file succeeds, so the seed never happens). Those go through a
> dangling `GlobalHome` relative symlink instead. A destination already under a rw
> bind (`~/.config/…`, the common case) needs nothing.
> See [composed-file-permissions.md §7.5](../design/composed-file-permissions.md).

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

> **✅ Built**, with one deviation: there is no merged manifest to check across
> (user surfaces render standalone), so destination uniqueness is enforced in the
> config layer — `checkHostFiles` rejects duplicates within one config value and
> `dedupeHostFilesByPath` resolves the cross-scope case. The reserved-destination
> guard matches EXACT paths rather than reusing `checkWritableHomeDir`'s
> first-segment rule, which would reject `~/.config/mytool/x.json` — the central
> use case.

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

`path` is omitted: with a `~/…` source it defaults to the same home-relative
destination, so a mirrored file names its path once rather than twice.

```jsonc
{
  "host_files": [
    "~/.gitignore_global",
    { "source": "~/.config/mytool/config.json",  // path defaults to the same place
      "mode": "capture" }   // opt in: capture in-jail edits (overlay outranks host)
  ]
}
```

Spelling `path` out is still legal, and required when the destination differs from
the source or the source lives outside `$HOME`:

```jsonc
{ "path": "~/.config/mytool/config.json", "source": "/etc/mytool/defaults.json" }
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

### Example 3 — rich: seed from host, transform, one managed key

User scope only (`source` present). The dir entry is copied wholesale.

No `codec` and no `path`: `.toml` auto-detects to the `toml` codec, and the
destination defaults to the source's home-relative path. Name a `codec` only to
*override* the extension — e.g. `"codec": "raw"` on a `.json` file whose comments
or key order must survive byte-for-byte.

```jsonc
// ~/.config/yolo-jail/config.jsonc  — user scope
{
  "host_files": [
    {
      "source": "~/.config/starship.toml",   // → toml codec, same dest path
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

> **✅ All phases below are DONE** (see
> [What is built](#what-is-built-and-what-is-not) for the per-phase landing map).
> Kept as the record of how the work was sequenced. Two items came out differently:
> the `readonly` mode ships as a `0o444` chmod rather than a `:ro` mount (item 7 —
> still an open question), and item 8's writable-subtree registration does not cover
> home-root files, which use the symlink hatch instead.

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

> **✅ Implemented.** Unit coverage landed per phase; the nested-jail bullet became
> `integration/hostfiles_test.go` (4 tests, real containers) — narrowed in one
> respect: it drives the SOURCE-LESS half, because a source-bearing entry is read
> only from `~/.config/yolo-jail/config.jsonc`, which in this repo's own jail is a
> `:ro` bind of the maintainer's dotfiles and cannot be written by a test. The
> source-bearing render path is covered in `internal/entrypoint` against a fake
> `/ctx/host-user` mount, and its mount emission in `internal/cli/run`.

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

**Resolved during implementation** (both were the "still open" forks):

- **`managed`/`defaults` merge semantics** — RFC-7386 object merge only, mirroring
  the builtin surfaces. Array-append pinning was **deferred**: no user surface has
  needed it. Still open if one does.
- **Slug scheme for `Name`** — **settled**: a reversible percent-escape with `_` as
  the sole sentinel (`[A-Za-z0-9.-]` pass through, every other byte becomes `_hh`).
  Injective by construction, so two surfaces can never share the sidecars or the
  `/ctx/host-user` mount point. `HostFileEntry.Slug`, unit-tested for injectivity.

**Still open:** see [Scope: the line](#scope-the-line) for the authoritative in/out
list, and ~~ROADMAP item #3~~ *(that numbering was retired 2026-08-17; the deferred tail is now
[`BACKLOG.md`](BACKLOG.md) §Stage E)* for where the deferred tail is sequenced — chiefly whether
the four modes should collapse to three and `readonly` become a real `:ro` mount.
