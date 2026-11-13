---
title: "yolo's own config: the commands that write it, and what a write does"
status: current
stage: CURRENT
verified: 2026-11-12
verified_commit: 8dc1bab44
covers:
  - internal/configedit/edit.go
  - internal/configedit/apply.go
  - internal/configedit/effective.go
  - internal/configedit/targeted.go
  - internal/configdoc/document.go
  - internal/config/editvalidation_target.go
  - internal/cli/config.go
  - internal/cli/configeditverbs.go
  - internal/cli/allow.go
  - internal/cli/request.go
  - internal/cli/requestrepos.go
  - internal/cli/activeprovider.go
  - internal/cli/configwhy.go
  - internal/cli/surfacecmd.go
tags: [config, cli, editing, splice, toml, jsonc, reference]
summary: "Which command writes which part of yolo's config, which file it writes, and what the write does to the bytes: one hunk, every other byte untouched, verified after the write and checked by yolo config check. Also the inspection verbs, and why agent surfaces are yolo surface, not yolo config."
---

# yolo's own config: the commands that write it, and what a write does

**Status:** Verified 2026-11-12 against `8dc1bab44`.

Every message that once said "edit your config" prints a command instead, and the command changes
the bytes of one value in the file it names and nothing else. That is what makes it safe for yolo
to write a file a person also writes. A command earns its place by doing more than a text edit:
resolving a pack's source, chaining a grant into its approval, validating a provider name against
the declared ones, writing a removal into the right layer, or reaching the user config from inside
a jail. An agent or a person may still edit a project file by hand; `yolo config check` names the
fix when a line is wrong ([validation.md](validation.md)).

## The commands

| Command | Writes | What it does beyond the text |
| :--- | :--- | :--- |
| `yolo pack add <entry>`, `yolo pack remove <entry>` | user `packs` | resolves the source, refuses a duplicate, says when it is read |
| `yolo allow <source> [always\|on_request] <capability>` | user `[allow.<source>]` | then offers this workspace's approval through the gate `yolo config apply` runs |
| `yolo allow repos <alias> <host>/<path>`, `yolo allow repos.<alias>.<source> …` | user `[repos.<alias>]` and its ceiling | |
| `yolo request <source> <capability> [resource]` | project `[requests.<source>]` | runs `yolo check` and names the host step that approves it |
| `yolo request repos <alias> <host>/<path> [--local]`, `yolo request repos.<alias>.<source> …` | project `[repos.<alias>]` | |
| `yolo active-provider <agent\|default> <name\|provider/variant\|native>` | user `[active_provider]` | validates the name against what the selected packs and the file declare |
| `yolo config set\|add\|unset <key> [value]` | any key | schema-typed; `unset` writes the key's removal spelling |
| `yolo config secret\|unsecret <NAME>` | `[env]` and `[env.secrets]` | moves a name between the two without printing its value |
| `yolo config migrate` | every legacy file, once | the one whole-file rewrite ([`config-migration.md`](../../design/config-migration.md)) |
| `yolo pack migrate <path>` | a pack's manifest | converts `pack.json`, or a legacy spelling in `pack.toml`, in place |

Every named verb and `config set|add|unset` take `--dry-run`, which prints the hunk and writes
nothing. A named verb writes into the file that already holds its section, so a user whose
`[allow]` lives in `config.d/work.toml` has it edited there. The named verbs are sugar over the
generic one: a message prints the named verb where one covers its key and `yolo config set` where
none does, which is the case for a pack's own `allow.<pack>` settings, since a pack cannot ship a
verb. Advice that names a verb is pinned against the verbs themselves
(`TestVerbAdviceCommandsAreAcceptedByTheirVerbs`, and config-ref's list by
`TestConfigRefTOMLSectionNamesTheVerbsTheCLIHas`).

**Inside a jail.** A command that writes the user config (`yolo allow`, `yolo active-provider`,
`yolo pack add`, a user-scope `config set`) is refused with the line for the human to run on the
host. `yolo request` and project-scope edits run, and land at the next `yolo config apply` or launch
through the config-change prompt.

**After the write.** A project edit reaches the jail through the same y/N gate as any project
change. A user-config edit is trusted, except a grant, which `yolo allow` offers to approve for this
workspace. A `yolo config set|add|unset` that writes runs `yolo config check` right after it
(`runConfigEditWithPostWriteCheck`); a dry run and a no-op write nothing and check nothing. A hunk
whose values cannot be safely told apart from a secret is not printed, and the output says so.

## Which file a generic edit writes

`yolo config set|add|unset` picks one existing file consumed by the normal config graph
(`config.ResolveEditTarget`):

- With no flag, the key's scope supplies the default: a user-only key goes to the user config, any
  other key to the project file. The project file is chosen even when the user or local file is
  the one whose value currently wins.
- `--user`, `--project`, `--local` select that scope's existing file; `--file <path>` selects one
  consumed file. A user-only key written to a project target, or a project-only key to the user
  target, is refused.
- A target must exist, except the standard project or local TOML file, which may be created; a
  `--dry-run` never creates it.
- A value's type comes from the schema, never from whether the text looks like a number: a mise
  version stays a string, and a TOML literal (`'"NEW"'`) is accepted where the schema needs one.
- `unset` of an ordinary scalar removes it from the named file, so a lower layer's value or the
  default may reappear. A table, a list, an agent's provider and a legacy null each take their
  removal spelling ([README](README.md#how-layers-merge-and-how-a-layer-turns-something-off)):
  `yolo config unset mcp_servers.tavily --local` writes `mcp_servers.tavily = "off"`.

## The splice

A **splice** (a term the readable-config design coined) parses the file only to find the byte
range of one value, rewrites that range, and leaves every other byte as it was. It is not a round
trip through a parser and a printer, which is what reformats a file. It works on TOML and on
legacy JSONC (`configedit.PlanEdit`, over `configdoc`'s positioned index).

1. **Placement.** A new key goes at the end of its table; a new table at the end of the file after
   one blank line; a new list item after the last, with the list's own separator and line breaks
   copied from its neighbors. A removed key takes its own line and its separator, and nothing else.
2. **Comments are never read as belonging to anything.** No command moves or removes one; the
   comment above a removed item stays where it was.
3. **Indentation** is copied from the nearest sibling.
4. **One lock, no lock file.** Every writer of a config file (an edit, a file creation,
   `yolo config migrate`) takes a lock on the target's parent directory, then re-reads the file
   under it. No lock file is made beside the config, where it would litter the work tree.
5. **Atomic replace.** The new bytes are written to a temporary file and renamed over the named
   physical file; a symbolic link at the name is resolved, never replaced.
6. **Verified after the write.** The result is re-read, re-parsed and compared with the effective
   config the edit meant to produce, through the standard layered reader. A mismatch restores the
   original bytes and refuses, printing both (`configedit.Apply`, `ApplyEffective`).
7. **A no-op says so** and writes nothing: an item already present, a key already absent.
8. **It never reorders, never reformats a line it did not write, never writes a key it cannot
   validate, and never touches a file it did not name in its output.** A problem the config already
   had before the edit is printed as a warning naming where it is, and does not refuse the edit.

## Inspecting the config

| Command | Answers |
| :--- | :--- |
| `yolo config check` | is the config valid? ([validation.md](validation.md)) |
| `yolo config why <key>` | the effective value, each file that set it (and that the files merge, for a list), and what applies without it |
| `yolo config dump` | the whole computed config, as canonical JSON |
| `yolo config drift` | whether the workspace config on disk differs from the one this jail has |
| `yolo config apply` | host only: approve this workspace's config changes and apply what the running jail can take |
| `yolo config-ref` | every key, its type and its scope |

## `yolo config` and `yolo surface`

`yolo config` is yolo's own config. The composed agent surfaces, the settings, MCP and LSP files
yolo renders for each agent from packs, are `yolo surface` (`ls`, `render`, `diff`, `reset`,
`promote`, `capture`). Each old `yolo config <verb>` spelling of those six refuses with exit 2,
naming its replacement (`config.go`'s refusal, pinned by `surfacecmd_test.go`).

## What editing is not

- **Not a side effect.** yolo writes the user's file only when the user typed a command that says
  so. State yolo keeps on its own goes in its per-workspace file, never the user's.
- **Not an ignore-file edit.** No config command, `yolo init` and `yolo config migrate` included,
  writes `.gitignore` or `.git/info/exclude`; when a file needs ignoring, the output names the line.
- **Not a reformatter.** A file is never rewritten whole, except once, by `yolo config migrate`.

## Where the code lives

- The splice: `internal/configedit/edit.go` (`PlanEdit`), `apply.go` (`Apply`, the directory lock,
  `restoreFailure`), `effective.go` (`ApplyEffective`), `targeted.go`; positions:
  `internal/configdoc`.
- Target selection: `internal/config/editvalidation_target.go` (`ResolveEditTarget`).
- Verbs: `internal/cli/config.go` (`configRunW`), `configeditverbs.go`, `allow.go`, `allowrepos.go`,
  `request.go`, `requestrepos.go`, `activeprovider.go`, `packadd.go`, `packmigrate.go`,
  `configwhy.go`, `surfacecmd.go`.
