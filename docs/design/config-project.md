---
title: "A project's file: what it installs, what it asks for, and what it turns off"
date: 2026-10-25
status: accepted
stage: DECIDED
next: "Convert this repository's own yolo-jail.jsonc with yolo config migrate once the owner has installed a yolo that reads TOML"
tags: [config, workspace, install, grants, mounts, design]
summary: "The project config as annotated files, today and proposed: install, needs, mounts, ports, environment, and the local file. Every section is ruled. Part of the readable-config redesign."
---

# A project's file: what it installs, what it asks for, and what it turns off

**Status:** accepted 2026-10-25 (the maintainer, after reading every document: *"it all sounds
good and I want to move forward"*). Built by 2026-10-30; this repository's own file is still the legacy `yolo-jail.jsonc`, whose
conversion waits on the owner. Legacy JSONC is still read for a few releases: a launch offers
`yolo config migrate`, and `"convert_later": true` in the old user file stops the offer. The
"today" files are this repository's and a composite
team project's.

**How to read this.** [§1](#1-the-file) is the file; [§2](#2-no-file-at-all) is no file;
[§3](#3-the-local-file-and-turning-something-off) is the local file and the word "off". Every
section is approved; the rulings are in the [ledger](readable-config.md#decision-ledger).

> **In short.** A project file says what the project needs, inside what your own config allows,
> and most projects need no file at all.

**Reads with:** [`readable-config.md`](readable-config.md) (front door and ledger),
[`config-user.md`](config-user.md) (the ceiling a project's asks fit inside),
[`config-commands.md`](config-commands.md) (the commands an agent runs instead of editing this file).

---

## 1. The file

### This repository's

Today, `yolo-jail.jsonc`: 133 lines, of which 121 are the template's comments and 12 are live.

```jsonc
{
  "packages": ["just"],
  "network": { "mode": "bridge" },                     // the default, restated
  "mise_tools": { "lua": "5.4.9" },
  "brokered": { "git": { "grants": [
    { "kind": "branch", "repo": "github.com/mschulkind-oss/yolo-jail", "branch": "main" }
  ] } }
}
```

Proposed, `yolo-jail.toml`:

```toml
[install]
nix = ["just"]                    # runs this repo's Justfile recipes
mise = { lua = "5.4.9" }          # syntax-checks the packs' derive.lua files

[requests.git]                    # what this project requests, from your [allow.git] on_request list
push = ["main"]
```

### A team project's

Today:

```jsonc
{
  "packages": ["postgresql", "just"],
  "mise_tools": { "node": "22", "python": "3.12" },
  "mounts": ["~/code/acme-shared", "~/x:/ctx/x:ro"],  // the second is one host path named "~/x:/ctx/x:ro", skipped with a warning
  "network": { "ports": ["8000:8000"], "forward_host_ports": [5432] },   // HOST:JAIL, then JAIL:HOST
  "gpu": { "enabled": true },
  "resources": { "memory": "16g", "cpus": 8 },
  "env_sources": ["~/.config/yolo-jail/secrets.env", { "AWS_PROFILE": null, "PAGER": "cat" }],
  "brokered": { "github": { "repos": ["acme/shared"] } }
}
```

Proposed:

```toml
mounts = [                                # read-only unless `write = true`, which your config must allow first (RC-D24)
  "~/code/acme-shared",                   # a bare path lands at /ctx/acme-shared, as today
  { host = "~/x", at = "/ctx/x" },        # one list; an entry is a path or a table
  { host = "~/scratch", at = "/ctx/scratch", write = true },  # refused unless your config allows it; then approved at launch, in red
]

[install]
nix = ["postgresql", "just"]
mise = { node = "22", python = "3.12" }   # "3.12" stays a string

[machine]
gpu = true                                # warns and starts without one on a machine that has none
resources = { memory = "16g", cpus = 8 }
ports = [{ host = 8000, jail = 8000 }]           # the host reaches the jail's :8000
forward = [{ jail = 5432, host = 5432 }]         # the jail reaches the host's postgres at localhost:5432

[env]
files = ["~/.config/yolo-jail/secrets.env"]
set = { PAGER = "cat" }
unset = ["AWS_PROFILE"]

[requests.github]
repos = ["acme/shared"]                   # beyond this repository's own remotes

[requests.git]
new_branches = true                       # branches of its own, never main; each is approved at launch
```

A direction is a key, not a position in a string: `{ host = 8000, jail = 8000 }` where today's
`"8000:8000"` and `forward_host_ports` run opposite ways. `mounts` is one top-level list and sits
before the first section, because in TOML a bare key after `[install]` belongs to `install`; TOML
also spells an array of tables as `[[mounts]]` blocks, the same key, but a file may use only one
spelling for it, so the table entry above is written inline. A project requests; your config says
what may be requested. A request outside that is not an error: two people on one repository may
allow different things, so the one who does not sees it reported and skipped, and the jail runs
without it:

```console
$ yolo check
yolo-jail.toml:22: requests.git.new_branches asks to create branches; your config does not list
  new_branches under allow.git.on_request, so this project will not. To allow it: yolo allow git on_request new_branches
```

Nothing that decides where inference goes or what a jail may do with your logins is accepted
here, because this file is the agent's to edit; `yolo check` refuses such a key naming the file
it belongs in.

Approved 2026-10-25 ([OQ-RC17](readable-config.md#decision-ledger)): *"each section names what
the project decides, and the two string traps become keys."* The mount lines were corrected in the
same round, from a bare `mounts` that had landed under `[install]` and a second spelling of the
same key, to the one list above.

## 2. No file at all

Approved 2026-10-25 as convention C5 ([`config-user.md` §8](config-user.md#8-lines-you-never-write)).
Most projects need nothing. `yolo` in a folder with no `yolo-jail.toml` runs with defaults;
`yolo init` writes the three-line pointer file only when asked. It writes no git ignore file:
`.yolo/` keeps itself out of commits with its own `.gitignore`, and neither the project's
`.gitignore` nor the clone's `.git/info/exclude` is touched (the owner, 2026-11-07: *"it's VERY
surprising when your own settings get changed for you"*).

```text
# before                                   # after
acme/                                      acme/
  yolo-jail.toml   (the 133-line template)
  .gitignore       (+ ".yolo/")
```

## 3. The local file, and turning something off

`yolo-jail.local.toml` is this machine's and uncommitted, merged after the project file as today.
It is where a project turns off something your own config added:

```toml
# This machine only; not committed.
[mcp_servers.postgres]
command = ["npx", "-y", "@modelcontextprotocol/server-postgres", "postgresql://localhost/acme"]

mcp_servers.tavily = "off"        # your config adds it; not wanted here

[machine]
resources = { memory = "32g" }
```

Config lists merge by union, so a lower layer can add to a list and never remove from it; "off"
has to be a word on the thing. Today one `null` does every removal; TOML has none, so each kind of
thing gets its own spelling, and most are settled ([RC-D23](readable-config.md#RC-D23)):

```toml
[env]
unset = ["AWS_PROFILE"]           # a variable: a string is a legal value, so "off" can never be one
                                  # (reviewed: "now you can't set that var to 'off' literally")
[requests.github]
repos = []                        # a list: empty clears it
[active_provider]
claude = "native"                 # an agent left to itself: the one reserved word, ruled under OQ-RC20
```

A reserved string is allowed only where a string is never a legal value. That leaves the
table-valued things your config declares and a project does not want, an MCP server today and a
provider tomorrow, settled below; the per-workspace pack switch is a file yolo writes, not this
one.

Ruled 2026-10-25 ([OQ-RC7](readable-config.md#decision-ledger)): a string in the table's place.
`mcp_servers.tavily = "off"` is legal because a server is never a string; the one rule is that a
string where a table is expected is refused unless it is exactly `"off"`. The same spelling
serves any table-valued thing your config declares, a provider or a variant included, and
`yolo config unset mcp_servers.tavily --local` writes that one line.

## 4. Principles

1. **A project requests; your config says what may be requested.** Every request is a line in the
   launch approval prompt, granted or skipped; a request outside your ceiling is never an error.
2. **A project file is the agent's to edit**, so nothing user-scope is accepted here, and the
   refusal names the file it belongs in.
3. **A direction is a key, not a position in a string.**
4. **Off is a word at the place**, never a `null`, and never a string where a string could be a
   value.
5. **No file is the common case.**

## Appendix: evidence

- **The string traps are documented traps.** `config-ref`'s `mounts` section says a `~/x:/ctx/x:ro`
  string is parsed as one literal host path and skipped with a warning; its `ports` section says
  "Note the reversed order" of `forward_host_ports`. The table form of a mount already exists:
  this repository's `yolo-jail.local.jsonc` writes `{ "host": …, "at": …, "mode": "ro",
  "optional": true }`.
- **`null` has more than four meanings today.** `yolo config-ref` mentions it on 27 lines: an
  agent's own login, an MCP server removed, an environment variable unset, an include disabling a
  lower layer, a model-fact tombstone, a `repos` list cleared, a provider or adapter override
  dropped. The shipped prism meets the same problem on TOML surfaces, where a managed `null`
  deletes the key ([`pack-system.md`](../reference/pack-system.md)).
- **Scope is enforced per key today**, each validator re-reading the workspace config and
  refusing a user-only key there (`internal/config/validate.go` and its siblings); there is no
  central list. The new shape states scope per section, so the refusal can name it.
- **Config lists merge by union** (`internal/config/load.go`), so a lower layer can add to a list
  and never remove from it; that is why `"off"` is a word on the thing and not a list edit.
