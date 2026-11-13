---
title: "yolo's own config: a project's file"
status: current
stage: CURRENT
verified: 2026-11-12
verified_commit: 8dc1bab44
covers:
  - internal/config/schema.go
  - internal/config/readablevalidate.go
  - internal/config/removals.go
  - internal/config/mounts.go
  - internal/config/mountsrequest.go
  - internal/config/portforwards.go
  - internal/config/envsets.go
  - internal/config/readableallow.go
  - internal/config/readablenamed.go
  - internal/config/brokeredskipped.go
  - internal/cli/requestrepos.go
  - internal/cli/run/offswitch.go
tags: [config, workspace, install, mounts, requests, repos, reference]
summary: "A project's yolo-jail.toml and yolo-jail.local.toml: what a project installs, mounts, sets on the machine and in the environment, what it requests of the user's logins, which other repositories it names, how the local file turns something off, and what a project file may not say."
---

# yolo's own config: a project's file

**Status:** Verified 2026-11-12 against `8dc1bab44`.

A project's file says what the project needs, inside what the user's config allows. It is
`yolo-jail.toml` at the workspace root, committed, with `yolo-jail.local.toml` beside it for this
machine ([README](README.md#the-files)). An agent in the jail may edit it; a change reaches the
jail through the config-change prompt at the next launch or `yolo config apply`. Most projects need
no file at all.

```toml
mounts = [                                # read-only unless write = true
  "~/code/acme-shared",                   # a bare path lands at /ctx/acme-shared
  { host = "~/x", at = "/ctx/x" },
  { host = "~/scratch", at = "/ctx/scratch", write = true },  # needs the user's [allow.mounts]
]

[install]
nix = ["postgresql", "just"]
mise = { node = "22", python = "3.12" }   # a version stays a string

[machine]
gpu = true                                # warns and starts without one where there is none
resources = { memory = "16g", cpus = 8 }
ports = [{ host = 8000, jail = 8000 }]    # the host reaches the jail's :8000
forward = [{ jail = 5432, host = 5432 }]  # the jail reaches the host's :5432 at localhost

[env]
files = ["~/.config/yolo-jail/secrets.env"]
set = { PAGER = "cat" }
unset = ["AWS_PROFILE"]

[requests.git]                            # from the user's [allow.git] on_request list
push = ["main"]
new_branches = true

[requests.github]
create_pr = true                          # on origin

[repos.shared]                            # a repository beyond this one's own remotes
repo = "github.com/acme/shared"
```

## The sections

- **`mounts`** is one top-level list, so it sits before the first `[section]` (in TOML a bare key
  after a heading belongs to that table). An entry is a path, or a table with `host`, `at`,
  `write`, `optional` and `mode`. How a mount reaches each backend is
  [`context-mounts.md`](../context-mounts.md).
- **`[install]`**: `nix` packages (a name, or a table pinning `nixpkgs`, `version`, `url`,
  `outputs` or `platforms`) and `mise` tools by version. A change rebuilds the image.
- **`[machine]`**: `gpu` (`true`, or a table with `vendor`, `devices`, `capabilities`, `mode` and
  the vendor's own options), `resources`, `ports` and `forward`. A direction is a key, never a
  position in a string: `ports` entries are `{ host, jail }`, with optional `address` (the host
  interface) and `protocol` (`"tcp"` or `"udp"`), and `forward` entries are `{ jail, host }`.
- **`[env]`**: `files` (dotenv paths), `set`, `unset`, and `sources`, an ordered list whose entries
  are a dotenv path or `{ set = {…}, unset = […] }`, for when order across files and inline values
  matters. Values meant to stay out of reports go in `[env.secrets]`, which `yolo config secret`
  writes.
- **`[mcp_servers.<name>]`** and `[lsp_servers.<name>]` add servers for this project.
- **`[requests.<source>]`** asks for what the user's `allow.<source>.on_request` lists
  ([user-config.md](user-config.md#what-a-jail-may-do-with-your-logins)). `git` takes `push` (branch
  names or patterns), `push_tags` and `new_branches`; `github` takes `create_pr`, `create_issue`,
  `runs` (workflow files), `dispatch_workflows` and `approve_runs` (actors). A loophole pack with
  grant kinds takes its own: `[requests.journal]`. A request outside the user's ceiling is reported
  and skipped, naming the `yolo allow` line that would allow it; the jail runs without it.

Every key and its type is `yolo config-ref`.

## Named repositories

A project's unqualified `[requests.github]` lines mean one repository: the one `origin` resolves
to. Any other repository joins by a named table:

```toml
[repos.infra]                             # yolo request repos infra github.com/acme/infra
repo = "github.com/acme/infra"

[repos.infra.requests.github]             # writes there come only from its own lines
runs = ["deploy.yml"]
```

A `[repos.<name>]` table puts the repository in scope and requests read, for the GitHub broker and
for git's read scope alike ([user-config.md](user-config.md#what-a-jail-may-do-with-your-logins)).
Writes on it come only from lines under its own `requests.github`. Git write lines under a name
are not built and are refused. `[repos.origin.requests.github]` is refused naming the unqualified
`[requests.github]`, which already says it. The old list, `requests.<source>.repos`, is refused at
load naming the table and `yolo config migrate`, which converts it. The rules and the approval
prompt's lines are [`github-write-scopes.md`](../../design/github-write-scopes.md#8-which-repository-a-line-is-for).

## No file at all

`yolo` in a folder with no project file runs with the user's config and the defaults.
`yolo init` writes a three-line pointer file only when asked, and writes no ignore file: `.yolo/`
keeps itself out of commits with its own `.gitignore`, and neither the project's `.gitignore` nor
the clone's `.git/info/exclude` is touched (`TestInitWritesThePointerFileOnly`).

## The local file, and turning something off

`yolo-jail.local.toml` is this machine's, uncommitted, merged after the project file. It is where a
project turns off something the user's config added. Because lists merge by union, "off" is a word
on the thing, never a shorter list ([README](README.md#how-layers-merge-and-how-a-layer-turns-something-off)):

```toml
mcp_servers.tavily = "off"        # the user's config adds it; not wanted here

[requests.journal]
off = true                        # keep the journal pack's loophole off in this workspace

[machine]
resources = { memory = "32g" }
```

`yolo config unset mcp_servers.tavily --local` writes the first line ([editing.md](editing.md)).

## What a project file may not say

A project file is the agent's to edit, so nothing that decides where inference goes or what a jail
may do with the user's logins is accepted in it. A user-only key there is refused naming the user
config as its home ("user-scope only; move this key to the user config", or the verb that writes
it; `readablevalidate.go`). The user-only keys are the top-level keys
`config.WorkspaceUserScopeOnlyKey` lists (among them `packs`, `programs`, `setup`,
`cache_relocations`, `perf_logging` and the `host_*` keys), the readable schema's `pack_sources`,
`allow` and `active_provider`, and a provider's selection and routing keys (`name`, `alias`,
`model`, `via`, `variants`, `endpoints` and their kin; `readableProviderScope`). A provider's model
facts may be written in either scope.

Some keys a project might want stay refused for a structural reason a consent cannot answer:

| Keys | Why a project file may not hold them |
| :--- | :--- |
| `host_wrappers`, `host_apply_on_launch`, `host_management`, `promotion_target` | machine-wide |
| `perf_logging` | read before the workspace config loads |
| a loophole's install, `env`, `doctor_cmd` | reach a host daemon |
| `cache_relocations`, source-bearing `host_files`, a provider's `base_url` | agent-editable, and `base_url` steers inference and credentials |

The one widening a project may request is a writable mount, under the user's `[allow.mounts]`
ceiling. It is read from the file that declares it, never from an included file, and never from a
legacy JSONC workspace file; the config-change prompt shows it in red. The ruling and the
alternatives it replaced are [rulings.md](rulings.md#a-writable-mount-from-a-workspace).

## Where the code lives

- Scope of each key and the refusal: `internal/config/schema.go` (`ReadableSchemaKeys`,
  `readableProviderScope`), `workspacescope.go`, `readablevalidate.go`.
- Mounts and the writable request: `internal/config/mounts.go`, `mountsrequest.go`,
  `mountsrequestbind.go`. Ports: `portforwards.go`, and `readablemerge.go`'s `PublishPortSpec`. Env: `envsets.go`,
  `envsources.go`.
- Requests, skips and named repositories: `internal/config/readableallow.go`, `brokeredskipped.go`,
  `readablenamed.go`; the verb: `internal/cli/requestrepos.go`.
- The off switches: `internal/cli/run/offswitch.go`.
