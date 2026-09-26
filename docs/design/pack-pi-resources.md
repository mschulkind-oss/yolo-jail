---
title: "A pack gives pi a whole package, not a list of files: delivering pi extensions, themes and prompts without naming pi's paths"
date: 2026-09-25
status: draft
tags: [pi, packs, files, slots, extensions, themes, config-list]
summary: "A content pack that ships pi extensions today writes one `files` entry per file, each naming a path inside pi. pi's own loader explains why: a top-level file in ~/.pi/agent/extensions is an extension, but a subdirectory there loads only through a manifest listing exact paths or an index.ts. pi's `packages` setting has the shape the pack wants: a local directory with conventional extensions/, themes/, skills/ and prompts/ folders loads with no list at all. So pi's pack declares a slot outside auto-discovery, and the slot declaration tells core to register every tree that lands there as a local pi package, appended beside the user's own packages. A content pack then writes one entry and no pi path. One ruling owed: adopting this route."
---

# A pack gives pi a whole package, not a list of files

**Status:** DESIGN, 2026-09-25. Nothing built. pi's behavior read from `@earendil-works/pi-coding-agent`
0.87.1 as installed in this jail (`dist/core/package-manager.js`, `dist/core/pi-manifest.js`,
`dist/core/extensions/loader.js`, `docs/packages.md`, `docs/settings.md`, `docs/extensions.md`); yolo
claims read against the working tree on this date. **MEASURED:** nothing. The pi claims come from
reading its source, not from running it.

> **In short.** The maintainer's pack repeats seven entries because pi's extensions folder can't take
> a folder of files, only single files or a folder with a manifest. pi's `packages` setting can: a
> local directory shaped like a pi package loads every extension, theme and prompt in it with no
> list. So pi's pack should expose a slot whose trees are registered as pi packages, and a content
> pack writes one line.

**Why it matters.** Every content pack that ships pi extensions has to know pi's internal layout
and list each file, and the one slot mechanism built for this ([`files` slots](../reference/pack-system.md#files))
is unusable for pi today: pi's pack declares no slot, and a tree landed under
`~/.pi/agent/extensions/<pack>/` would load nothing without a hand-written manifest.

**The shape.** Three parts:
- a **slot** pi's pack declares at `.pi/agent/yolo-packs`, carrying a new `register` field;
- **core registration**: for every tree that lands in that slot, core appends the tree's path to
  pi's `packages` list, as a `config-list` entry;
- the **content pack's tree**, laid out like a pi package.

**Cost.** One new optional field on a `files` slot, plus one advisory one (`expects`). The
maintainer's pack migrates once: its tree moves and eight entries become one. No shipped behavior
changes until a pack addresses the slot.

**Start at [§1](#1-what-pi-loads-from-where-and-in-what-form)**, which is how pi decides what to load.
The rest follows from it.

**Needs your ruling:** [OQ-PR1](#OQ-PR1).

**Reads with:**
- [`pack-pi-resources-plan.md`](pack-pi-resources-plan.md): the implementation sketch. It is
  incomplete, and nobody builds from it.
- [`pi-pack-extensions.md`](pi-pack-extensions.md): the slot architecture (D) this builds on, and
  whose "pi does nothing (auto-discovery)" this doc corrects ([§2.2](#22-what-the-slot-design-got-wrong-about-pi)).
- [`slots-and-contributions.md`](slots-and-contributions.md): the destination redesign; how this
  relates is [§6](#6-how-this-relates-to-slots-and-contributions).

---

## Defined terms

- **Slot**: a `files` declaration with `agent` + `into` and no `from`. It names where content
  addressed to that agent lands, and ships nothing ([pack-system.md, `files`](../reference/pack-system.md#files)).
- **Addressed tree**: a `files` contribution with `agents` + `from` and no `into`. It lands at
  `<slot>/<contributing pack>` (`packload.SlotLanding`), read-only, at both notches.
- **Landing**: that `<slot>/<pack>` path, home-relative.
- **Local pi package**: an entry in pi's `packages` setting that is a path rather than `npm:` or
  `git:`. pi loads it where it is, never installs or updates it.
- **Registration** *(coined here)*: the `packages` entry core appends for one landed tree.

## 1. What pi loads, from where, and in what form

Everything here is pi 0.87.1, read from source. The function names are pi's.

| Where | What pi does with it | Globs? | Source |
| :--- | :--- | :--- | :--- |
| A top-level `.ts`/`.js` file in `~/.pi/agent/extensions/` | Loads it as **one extension**. Automatic | — | `collectAutoExtensionEntries` |
| A **subdirectory** of `~/.pi/agent/extensions/` | Loads only what the subdirectory declares: the `pi.extensions` list in its `package.json`, resolved as **exact paths** (a missing path is skipped), or else its `index.ts`/`index.js`. With neither, it loads **nothing**. It never looks deeper | **No.** A `./*.ts` entry here names a file literally called `*.ts` | `resolveExtensionEntries` |
| `~/.pi/agent/themes/*.json`, `~/.pi/agent/prompts/*.md` | Loads each top-level file. Automatic | — | `collectAutoThemeEntries`, `collectAutoPromptEntries` |
| Settings arrays `extensions`, `themes`, `skills`, `prompts` | Each plain entry is a file or directory; a directory is expanded with the same rules as the automatic folders. Pattern entries (`!x`, `+x`, `-x`, or anything containing `*`/`?`) only **filter** what the plain entries found | Filters only: a glob with no plain entry beside it adds **nothing** | `resolveLocalEntries`, `splitPatterns` |
| `packages` entry `npm:…` / `git:…` | Installed into pi's store, then loaded as a package; updated by `pi update --extensions` | — | `resolvePackageSources`, `updateConfiguredSources` |
| `packages` entry that is a **local path** (e.g. `~/some/dir`) | Loaded in place, **never installed or updated**. A path that does not exist is **skipped silently**. A file is loaded as one extension; a directory is loaded as a **package** (next rows) | — | `resolveLocalExtensionSource` |
| A package whose `package.json` has a **`pi` key** | Loads **only** the resource arrays that key lists, for **every** type. Arrays expand globs and accept `!` exclusions. A type the manifest does not list loads nothing, **even if its conventional folder exists** | **Yes** | `collectPackageResources`, `collectFilesFromManifestEntries`, `readPiManifest` |
| A package **without** a `pi` key (no `package.json`, or one without `pi`) | Loads each **conventional folder** that exists: `extensions/` (every top-level `.ts`/`.js`, plus subdirectories by the row-2 rule), `themes/`, `skills/`, `prompts/`. No list needed | Not needed | `collectPackageResources`, `collectResourceFiles` |
| A local package directory with **neither** a `pi` key nor any conventional folder | Loads **the directory itself** as one extension, which fails unless it has an index | — | `resolveLocalExtensionSource` |

Four more facts decide the design:

- **Duplicates.** Packages are deduplicated by identity: the npm name, the git host and path, or a
  local package's resolved absolute path. Project scope wins over user scope (`getPackageIdentity`,
  `dedupePackages`). Individual resources are deduplicated by absolute file path, first one wins
  (`addResource`).
- **Scope and trust.** User settings (`~/.pi/agent/settings.json`) resolve relative paths from
  `~/.pi/agent` and expand `~`. Project settings and `.pi/extensions` load only once the project is
  trusted (`getBaseDirForScope`, `addAutoDiscoveredResources`).
- **Loading.** Extensions load through jiti with pi's own modules aliased (`loader.js`,
  `loadExtensionModule`). So an extension that imports only `@earendil-works/*` and `node:*`
  needs no `node_modules`, which is true of all seven of the maintainer's. They load from
  read-only files, which is already proven: today's seven are `:ro` mounts.
- **Update.** `pi update --extensions` touches only `npm:` and `git:` sources, so a local package
  never fetches and never contends for the refresh lock ([`pi-git-extension-caching.md`](pi-git-extension-caching.md)).

**The maintainer's pack, read against this table.** Each of its seven files works because each is
mounted as a *top-level* file in `~/.pi/agent/extensions/`. Moving them into a subdirectory stops
them loading unless that subdirectory gets a manifest that lists all seven, by exact path. That is
why the pack has seven entries.

## 2. What yolo has today

### 2.1 Slots, as built

A slot is `{"kind": "files", "agent": "pi", "into": "<dir>"}`. An addressed tree is
`{"kind": "files", "agents": ["pi"], "from": "<dir>"}` and lands at `<dir>/<pack>` at both notches,
through one resolver (`packload.SlotLanding`), pinned by `filesslotparity_test.go`. The rules:
- one slot per agent;
- one addressed tree per agent per pack (`validateFilesDestinations`, `validateAddressedFiles`);
- an addressed tree is a read-only mount in the jail, and written file by file at the host.

**pi's pack declares no slot.** Its only `files` entries are its own two
`.pi/agent/extensions/yolo-*.js` files. So `{"agents": ["pi"]}` has nowhere to land today, and
[`pi-pack-extensions.md` §8](pi-pack-extensions.md#8-invariants-and-failure-modes) says as much:
"no manifest in the corpus declares a files slot at all".

### 2.2 What the slot design got wrong about pi

[`pi-pack-extensions.md`](pi-pack-extensions.md) Architecture D places the slot at
`.pi/agent/extensions` and says of activation: *"Pi does nothing (auto-discovery)"*. Row 2 of
[§1](#1-what-pi-loads-from-where-and-in-what-form) makes that false for any tree with more than one
extension file. A tree lands at `extensions/<pack>/`, a subdirectory, and pi loads nothing from it
without a manifest or an index. D's slot location is the problem; D's architecture (a slot the
agent pack declares, content addressed by agent name) stands and is what this design uses.

### 2.3 How pi's `packages` list is composed

`pi/settings` is a stateful, host-reading surface of `packs/pi`. Arrays in a `config-overlay`
**replace**, and the maintainer's pack sets `packages` that way (its overlay's `managed` block, which
folds at the overlay slot, not the managed floor). A [`config-list`](../reference/pack-system.md#adding-entries-to-an-array-config-list)
appends after every overlay, first occurrence wins, with per-entry capture in the jail and an
inserted-entries record at the host. So an entry added that way sits **beside** the user's own
`packages`, and vanishes when its contributor does: the jail stops re-contributing it, and the
host removes "an inserted entry no longer contributed" (`agentcfg.ReconcileInsertedList`). A
derive's `computed` layer would **replace** the array instead, so registration must not come from
a derive.

## 3. The design

### 3.1 pi's pack declares a registering slot

```jsonc
// packs/pi/pack.json
{
  "kind": "files",
  "agent": "pi",
  "into": ".pi/agent/yolo-packs",
  "register": { "surface": "pi/settings", "path": "/packages", "entry": "~/{landing}" },
  "expects": ["extensions", "themes", "skills", "prompts", "package.json"]
}
```

- **`into` is outside every folder pi scans on its own**, so a landed tree is loaded only because
  it is registered, never by accident, and a stale tree is inert.
- **`register`** is new and optional on a slot. It is refused on anything but a slot.
  - `surface` must be a surface the same pack owns.
  - `path` is a JSON Pointer to an array, with the same rules as `config-list`'s `path`.
  - `entry` is a string template whose one token, `{landing}`, is the tree's home-relative landing
    path. An unknown token, or no `{landing}` at all, is refused.
- **`expects`** is new, optional and advisory: the top-level names a well-formed tree contains. A
  tree with none of them is warned about ([§3.4](#34-behavior-in-every-case)).

### 3.2 A content pack addresses pi once

```jsonc
{ "kind": "files", "agents": ["pi"], "from": "files/pi" }
```

The tree is a pi package with no `pi` manifest:

```
files/pi/
  extensions/*.ts      each top-level file is one extension
  themes/*.json
  prompts/*.md         optional
  skills/…             optional; see §3.4
```

If the author does want a `package.json` (for npm dependencies, say), it must either have **no**
`pi` key or list **every** type it ships, because a `pi` key switches the conventional folders off
([§1](#1-what-pi-loads-from-where-and-in-what-form), row 7).

### 3.3 What core does

For every addressed tree that lands in a slot declaring `register`, core emits one list
contribution **on behalf of the contributing pack**:
- target: the declared `surface` and `path`;
- add: `entry`, with `{landing}` substituted, e.g. `~/.pi/agent/yolo-packs/matt`.

It then folds exactly as a hand-written `config-list` does: pack order, first occurrence wins,
per-entry capture, and the host's inserted-entries record. Core knows a slot, a surface, a pointer
and a template, and nothing about pi.

Both notches do the same thing:
- **In the jail**, the tree is mounted `:ro` at the landing and `settings.json` composes with the
  entry.
- **At the host** (`yolo host apply`), destination borrowing writes the tree at the real home's
  landing, and the entry is inserted into the real `~/.pi/agent/settings.json` under the rmw record.

### 3.4 Behavior in every case

| Case | Behavior |
| :--- | :--- |
| No pack addresses pi | Nothing is mounted and nothing is registered. `settings.json` is byte-identical to today's |
| A tree lands and registers | pi loads every extension in `extensions/`, every theme and prompt, as a user-scope package. No project trust involved |
| Two packs address pi | Two landings and two entries, in pack order. Same-named files in different packs are different paths, so both load. A clash in what they *register* (a command or tool name, a theme name) is pi's to resolve. **UNVERIFIED** how pi resolves it |
| A tree with none of `expects` | `pack lint`, `pack footprint` and `yolo check` **warn**, naming the tree and the expected names. It still lands and registers. pi would load the directory as one extension and fail, since that is pi's rule |
| One extension fails to load | pi's startup error. yolo does nothing extra. **UNVERIFIED** that pi loads the rest |
| A pack leaves `packs` | Jail: the tree is unstaged and its entry is not re-contributed, so it is gone next boot. Host: the inserted entry is removed. A host tree left behind is inert, because nothing registers it; whether the host retires it is the plan's to check |
| The user runs `pi install` in the jail | Their entries are captured per entry, beside the registrations, and survive |
| The user deletes a registration in the jail | Captured as a removal and kept removed, which is `config-list`'s rule, not a new one |
| The addressed agent's pack is not selected | The existing orphan report for an addressed `files` contribution. Nothing is registered, because no pack owns the surface |
| The tree ships `skills/` | pi loads them for pi only. `pack lint` notes that the `skills` kind reaches every agent, the recommended route |
| Refresh | A new or removed registration changes `settings.json`, so the [`due_on_change`](pi-git-extension-caching.md) refresh runs once. Local packages are never fetched |
| macos-user and Apple Container | Whatever those backends do for an addressed `files` tree today. **UNVERIFIED** per backend; the plan checks [`settings-per-setup.md`](../../userguide/reference/settings-per-setup.md) |

**Forbidden.** Registration never writes through `computed` and never replaces the array. Core
never names pi, and never registers a tree landed in a slot without `register`. pi's own two files
at `.pi/agent/extensions/yolo-*.js` are unchanged by this design.

### 3.5 Done looks like

- The maintainer's pack, migrated ([§4](#4-the-maintainers-pack-before-and-after)), loads all seven
  extensions and all six themes in a jail, and `~/.pi/agent/settings.json`'s `packages` holds its
  six sources plus `~/.pi/agent/yolo-packs/matt`.
- Dropping the pack from `packs` removes both the mount and the entry on the next launch, and the
  entry from the host file on the next `yolo host apply`.
- A pack whose tree has no conventional folder gets a warning from `pack lint`.
- No shipped pack's rendered files change until a pack addresses pi.

## 4. The maintainer's pack, before and after

Before: seven entries naming `.pi/agent/extensions/<file>`, plus one naming `.pi/agent/themes`.

After:

```jsonc
// /ctx/packs/matt/pack.json: the eight files entries become
{ "kind": "files", "agents": ["pi"], "from": "files/pi" }
```

```
files/pi/extensions/{compact-tools,thinking-preview,usage-capture,thinking-summary,tok-rate,or-route,slash-aliases}.ts
files/pi/themes/{catppuccin-mocha,dark-readable,dracula,gruvbox-dark,nord,tokyo-night}.json
```

Nothing else in the pack changes. The overlay's `packages`, `theme` and the rest stay as they are,
because the theme is chosen by name wherever pi loaded it from.

## 5. Alternatives, with verdicts

| | Shape | Verdict |
| :--- | :--- | :--- |
| **A** | Today's per-file entries | **Rejected as the answer**; it stays working. It fails both complaints, and themes and prompts each need their own path |
| **B** | A slot at `.pi/agent/extensions`, with the tree carrying a `package.json` listing every extension | **Rejected.** The listing is exact paths with no globs (row 2), so the pack still names every file, and it covers extensions only |
| **C** | A slot outside auto-discovery, registered as a local pi package | **Chosen** ([§3](#3-the-design)). One entry, no pi path, covers every pi resource type, a stale tree is inert, and both notches share one rule |
| **D** | Register through the settings arrays (`extensions: [<landing>/extensions]`, `themes: [...]`) | **Rejected.** It needs one entry per resource type, so the registration template has to know pi's four types, and it gives up a package's manifest option |
| **E** | Wait for [`slots-and-contributions.md`](slots-and-contributions.md) | **Rejected.** C fits today's slot shape, so it waits on nothing ([§6](#6-how-this-relates-to-slots-and-contributions)) |
| **F** | pi's derive reads the delivered set and writes `packages` | **Rejected.** A derive writes the `computed` layer, which replaces the array and would erase the user's packages ([§2.3](#23-how-pis-packages-list-is-composed)) |

## 6. How this relates to slots-and-contributions

[`slots-and-contributions.md`](slots-and-contributions.md) replaces the `agent`/`agents` spelling
with an `exposes` axis, and its build is blocked on [OQ-D6](slots-and-contributions.md#OQ-D6).
This design uses today's spelling, and `register` and `expects` are fields of a destination. When
`exposes` lands, both fields move with the destination unchanged. Adding a slot in today's shape
is not the D6 migration window, which concerns shipped packs *changing* shape. A tolerant decoder
that does not know `register` ignores it, which only leaves a landed tree unregistered, the same
as today.

It also needs no second slot: pi's resource types all ride one package tree, so the
one-slot-per-agent rule does not bind.

## 7. Non-goals

- Registering trees for other agents. `register` is generic, but no other shipped agent's config
  takes a list of package paths. Claude's `enabledPlugins` is an object map, for example.
- Changing pi's own two built-in extension files.
- Policing clashes between extensions. pi owns what it loads.

## Open questions

1. 💬 <a id="OQ-PR1"></a>**[OQ-PR1](#OQ-PR1): Is C the route every content pack uses to reach
   pi?** It changes what a content pack writes for pi (one tree, shaped as a pi package) and
   migrates the maintainer's pack. The per-file entries keep working either way. Stakes: whether
   yolo's answer to "ship pi extensions from a pack" is a pi package or a list of paths.

   _Leaning:_ **Yes.** It is the only option with no pi path and no file list, and it covers
   themes and prompts in the same line.

   <!-- vantage: oq id=OQ-PR1 leaning="Yes: C, a slot outside pi's auto-discovery whose landed trees core registers as local pi packages. The only option with no pi path and no file list, covering themes and prompts in the same line." -->

   **Answer:**
   > _(empty — fill in when decided)_

## Decision Ledger

Implementation decisions, recorded as decisions rather than asked, because each has one workable
answer:

| ID | Decision | Why it holds |
| :--- | :--- | :--- |
| PR-D1 | The slot is `.pi/agent/yolo-packs` | Outside every folder pi scans on its own ([§1](#1-what-pi-loads-from-where-and-in-what-form)), so only a registration loads a tree |
| PR-D2 | Registration is a core-emitted `config-list` entry, attributed to the contributing pack | The only layer that appends beside the user's list and forgets a dropped pack at both notches ([§2.3](#23-how-pis-packages-list-is-composed)) |
| PR-D3 | The entry is `~/{landing}` | pi expands `~` in user settings, at both notches; an absolute path would differ between the jail and the host |
| PR-D4 | `expects` warns and never refuses | A tree pi cannot load is pi's failure to report; yolo adds the early hint, not a second gate |

## Appendix A: evidence

- pi 0.87.1, `dist/core/package-manager.js`:
  - `resolveExtensionEntries`: a subdirectory's manifest entries are `resolve` plus `existsSync`,
    with no glob expansion;
  - `collectAutoExtensionEntries`, `collectPackageResources`, `collectFilesFromManifestEntries`
    (`expandPackageGlob`), `resolveLocalEntries` / `splitPatterns` (patterns filter plain entries);
  - `resolveLocalExtensionSource`: a missing path is skipped, and a directory with no resources
    becomes one extension;
  - `updateConfiguredSources` (npm and git only), `getPackageIdentity`, `dedupePackages`,
    `addResource`.
- pi 0.87.1, `dist/core/pi-manifest.js`: `readPiManifest` returns null without a `pi` object.
- pi 0.87.1, `dist/core/extensions/loader.js`: jiti with `moduleCache: false` and pi's aliases.
- yolo:
  - `packload.SlotLanding` (`internal/packload/mergedest.go`);
  - `validateFilesDestinations` and `validateAddressedFiles` (`internal/packdecl/contributes.go`);
  - `agentcfg.ReconcileInsertedList` (`internal/agentcfg/listcontrib.go`);
  - `packs/pi/pack.json`: `settings` is `readsHost` with no `mode`, so it is `stateful` (the default,
    [pack-system.md](../reference/pack-system.md)), and there is no files slot.
