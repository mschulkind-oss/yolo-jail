---
title: "yolo's own config: the files, the two scopes, and how they merge"
status: current
stage: CURRENT
verified: 2026-11-12
verified_commit: 8dc1bab44
covers:
  - internal/paths/paths.go
  - internal/config/load.go
  - internal/config/newreadableconfig.go
  - internal/config/userlayer.go
  - internal/config/confignamestate.go
  - internal/config/readablemerge.go
  - internal/config/removals.go
  - internal/config/renames.go
  - internal/config/schema.go
  - internal/config/readablevalidate.go
  - internal/config/workspacefile.go
tags: [config, toml, scopes, merge, reference]
summary: "The front of the config reference: which files yolo reads for its own config, in what order, which of two scopes each key belongs to, how layers merge and how a layer turns something off, the TOML format and the legacy JSONC window, and which page answers which question."
---

# yolo's own config: the files, the two scopes, and how they merge

**Status:** Verified 2026-11-12 against `8dc1bab44`.

yolo's config is a small set of TOML files a person writes: one for the user, read by every
launch, and one or two for a project, read by launches in that workspace. Each section names a
decision in a word a person would use (`packs`, `allow`, `requests`, `active_provider`), says each
fact once, and leaves out whatever is the default. yolo can edit any of these files one value at a
time without disturbing anything else in them ([editing.md](editing.md)), and a fast read-only
command says whether an edit is valid ([validation.md](validation.md)). Every key, with its type,
is listed by `yolo config-ref`; these pages describe the shape and the rules, not each key.

## Vocabulary

- **User config.** The files under `~/.config/yolo-jail/` that hold what every project gets:
  the pack selection, logins and their ceilings, providers. Only the user, on the host, edits it.
- **Project config** (also *workspace config*). `yolo-jail.toml` at the workspace root, committed,
  and `yolo-jail.local.toml` beside it, this machine's and uncommitted. An agent in the jail may
  edit it; a change reaches a launch only through the config-change prompt
  ([`config-safety.md`](../config-safety.md)).
- **Scope.** Which of those two a key may be written in. A key is user-only, project-only, or
  both (`config.SchemaScope`). Nothing that decides where inference goes or what a jail may do
  with the user's logins is accepted in a project file, because that file is the agent's to edit.
- **Ceiling and request.** The two words for the two scopes. The user config's `allow` says what
  every project's jail gets and what a project may ask for; a project file's `requests` is the
  ask. A request outside the ceiling is skipped and reported, never an error
  ([user-config.md](user-config.md#what-a-jail-may-do-with-your-logins)).
- **Layer.** One file in the merge order below. A higher layer adds to or overrides a lower one.
- **Legacy config.** The JSONC (or JSON) files yolo read before TOML. They are still read for a
  few releases, and `yolo config migrate` converts them
  ([`config-migration.md`](../../design/config-migration.md)).

## The files

| Scope | TOML name | Legacy names, still read |
| :--- | :--- | :--- |
| user | `~/.config/yolo-jail/config.toml`, then `config.d/*.toml` | `config.jsonc`, `config.json` |
| project | `<workspace>/yolo-jail.toml` | `yolo-jail.jsonc`, `yolo-jail.json` |
| project, this machine | `<workspace>/yolo-jail.local.toml` | `yolo-jail.local.jsonc`, `yolo-jail.local.json` |

The names are constants in `internal/paths` (`UserConfigTOMLName`, `WorkspaceConfigTOMLName` and
their siblings).

- **The file name is the format.** A `.toml` file is TOML; a `.jsonc` or `.json` file is legacy.
  A TOML file may carry `format = 2`; absent means 2, a higher number is refused naming
  `yolo update`, and a lower one is refused as unsupported (`consumeReadableFormat`). No migrated
  file writes the key.
- **One format in one place.** A TOML file beside a legacy one of the same scope is refused
  (`readableSiblingCollisionError`). The refusal names each file and the step: when the legacy
  file is the one `yolo config migrate` already converted (its `<name>.pre-migrate` holds the same
  thing), it says the file came back and gives the one `rm`; otherwise it offers each file to
  keep. Two legacy names without a TOML one are read as before, `.jsonc` first.
- **A name that exists but cannot be read is a refusal, never "no file".** A dangling symbolic
  link, a link loop, a directory or an unreadable file at a config name is refused in every scope,
  naming what it is and the next step (`configNameState`, presence by `os.Lstat`). For a dangling
  link the message gives the link's own text and says to restore the target or `rm` the link.
- **`config.d/`.** Beside a TOML `config.toml`, every `config.d/*.toml` is read after it, in name
  order. A machine-specific file (a work or a personal one a dotfiles manager links in on the
  right machines) goes there and needs no line in `config.toml`.
- **`include_if_found`.** A list of paths, relative to the including file, read after it. An entry
  that names no file, or one that exists but cannot be read, is skipped with one notice saying how
  to include it: the key means "include if we can".
- **The per-workspace file.** yolo keeps its own file for one workspace in
  `~/.config/yolo-jail/workspaces/`, for state it writes on its own; it is not the user's to edit
  and is described in `workspacefile.go`'s header. A command the user types writes the user's
  file; a side effect never does.

On the maintainer's machines the user config is four files, `config.toml`, `config.d/overrides.toml`,
and a work and a personal file a dotfiles manager places in `config.d/` on the right machines. A
migration converts each where it is and never merges them.

## Merge order

```text
config.toml ── its include_if_found ── config.d/*.toml ── --user-layer      (user scope)
      │
yolo-jail.toml ── its includes ── yolo-jail.local.toml ── its includes      (project scope)
      │
the per-workspace file yolo writes                                          (one workspace)
```

`LoadReadableConfig` composes the user scope (`loadReadableUserScopeWithInputUse`), then the
project files over it, then the per-workspace file (`applyWorkspaceFile`). `--user-layer` is a
launch flag naming one more user-scope file. Inside a jail the config is a generated snapshot of
the host's merge, so a check there cannot judge host paths ([validation.md](validation.md)).

Every value keeps a record of the file and line that wrote it, which is what `yolo config why`
prints and what every refusal names.

## How layers merge, and how a layer turns something off

- **Tables merge key by key**, recursively; a scalar in a higher layer replaces the lower one.
- **Lists merge by union**, in order: the lower layer's items, then each higher item not already
  present (`mergeLists`). So a layer can add to a list and never remove from it by listing less.
- **A removal is a word at the place**, one per kind of thing, because TOML has no `null` and
  because a reserved string may never stand where a string is a legal value:

| To remove | Spelling | Example |
| :--- | :--- | :--- |
| a table-valued thing a lower layer declared (an MCP or LSP server, a provider, a variant, a mise tool, everything a project requests of one pack) | the string `"off"` in the table's place | `mcp_servers.tavily = "off"` |
| an environment variable | its name in `env.unset` | `unset = ["AWS_PROFILE"]` |
| every item of certain lists | an empty list, which replaces rather than unions | `security.blocked_tools = []` |
| an agent's provider | `"native"`: the agent is left to its own login | `claude = "native"` |

The paths that take each spelling are `config.ReadableRemovalPolicies`. A string in a table's
place is refused unless it is exactly `"off"`, which is legal only because a server or a
provider is never a string. A legacy file's `null` (one value did every removal) is read through
the same policies.

## Format

TOML, for every file a person writes; files only yolo reads stay JSON. TOML keeps a one-line
table one line when a tool rewrites it, reads a version such as `3.10` as text when it is quoted,
and was already a dependency (`internal/tomlx`). A key in the legacy vocabulary written in a TOML
file is refused naming its replacement, from the conversion's own rename table
(`config.RenameDescriptors`); there are no aliases. A few renames, for orientation:

| Legacy | TOML |
| :--- | :--- |
| `packages`, `mise_tools` | `install.nix`, `install.mise` |
| `gpu`, `resources`, `network.ports`, `network.forward_host_ports` | `machine.gpu`, `machine.resources`, `machine.ports`, `machine.forward` |
| `env_sources` | `env.sources` (and `env.files`, `env.set`, `env.unset`) |
| `brokered` | `allow.<source>` (user) and `requests.<source>` (project) |
| `profile`, `profiles` | `active_provider`, `providers.<id>` and its `variants` |
| `loopholes.<p>.settings` | `allow.<p>` |

The legacy reader stays for a few releases. A launch over a legacy file offers
`yolo config migrate`; `"convert_later": true` written by hand in the legacy user file stops the
offer. What the migration converts and proves is [`config-migration.md`](../../design/config-migration.md).

## Principles

1. **Name the decision, not the mechanism.** `push = ["main"]`, not a `{"kind": "branch", …}`
   grant object under a section called `brokered`.
2. **Say each fact once.** A key is the value it names; facts several entries share sit in one
   place (`every_model`).
3. **Write nothing that is the default**, and a convention is a line never written at all
   ([user-config.md](user-config.md#lines-you-never-write)).
4. **One switch, one place.** `packs` is the switch; a heading or a declaration never selects or
   grants anything.
5. **No value is a reserved string**, with one word excepted: `"native"`, for an agent left to
   its own login (`"off"` stands only where a string is never a legal value).
6. **Two words for two scopes:** `allow` and `requests`.
7. **Plural for a collection you fill, singular for one thing:** `packs`, `providers.<id>`,
   `mcp_servers.<name>`; `active_provider`, `allow.git`.
8. **An edit yolo makes changes only the bytes it was asked to** ([editing.md](editing.md)).

## What the config is not

- **Not a custom format, and not code.** TOML carries the shape; a manifest is data
  ([`pack-manifests.md`](../../design/pack-manifests.md)).
- **Not a second approval path.** Which scope may hold which key, and what the config-change
  prompt approves, are the gate's ([`config-safety.md`](../config-safety.md)).
- **Not migrated automatically.** A launch offers the conversion; a person answers.
- **Not where a generated agent surface is edited.** `yolo config` is yolo's own config; an
  agent's rendered settings files are `yolo surface …` ([editing.md](editing.md#yolo-config-and-yolo-surface)).

## Which page answers which question

| Page | Question |
| :--- | :--- |
| [user-config.md](user-config.md) | What does the user config say: packs and their sources, conventions, providers and `active_provider`, the `allow` ceilings, a login pack's settings? |
| [project-config.md](project-config.md) | What does a project file say: install, mounts, machine, env, `requests`, named repositories, the local file? |
| [editing.md](editing.md) | Which command writes which key, and what does a write do to the file? |
| [validation.md](validation.md) | How is a config edit checked, and how does that differ from `yolo check`? |
| [rulings.md](rulings.md) | Why is it this way: every ruling and decision behind these pages, by id. |
| [`config-migration.md`](../../design/config-migration.md) | How legacy files become TOML (a design doc until the old reader is removed). |

## Where the code lives

- File names: `internal/paths/paths.go`.
- Selection and loading: `internal/config/load.go` (`LoadReadableConfig`, `resolveWorkspaceConfigPath`,
  `mergeLists`), `newreadableconfig.go` (`resolveUserConfigPath`, `config.d`, `format`, the
  sibling refusal), `userlayer.go`, `confignamestate.go`.
- The readable merge and removal spellings: `internal/config/readablemerge.go`, `removals.go`.
- The schema, its scopes and the rename table: `internal/config/schema.go`, `renames.go`,
  `readablevalidate.go`.
- The per-workspace file: `internal/config/workspacefile.go`.
