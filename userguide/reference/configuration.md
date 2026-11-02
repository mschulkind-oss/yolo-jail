# Configuration

You describe an agent's environment in TOML files. This page covers where those files live, which
settings go in which file, the commands that edit them, and how a change reaches a jail.
`yolo config-ref` is the complete reference for every key.

## The config files

| File | Scope | Create it with |
|---|---|---|
| `~/.config/yolo-jail/config.toml` | **User**: your defaults for every project | `yolo init-user-config` writes the commented JSONC form; `yolo config migrate` converts it |
| `yolo-jail.toml` | **Workspace**: one project's settings, committed with it | `yolo init` writes a three-line pointer; a project needs no settings to run |
| `yolo-jail.local.toml` | Workspace, this machine only; keep it out of git | `yolo config set <key> <value> --local` |

The older JSONC format (`config.jsonc`, `yolo-jail.jsonc`, `yolo-jail.local.jsonc`, and the `.json`
spellings) is still read for a few releases, so a project you have not converted keeps working.
Keep one format per file: a TOML file beside a legacy file for the same role is refused rather
than merged. `yolo init-user-config` still writes the commented JSONC user file.

**To convert, run `yolo config migrate`.** It rewrites the old files as TOML, proves the result
means exactly what the old files did, and writes nothing otherwise. Each original is kept as
`<name>.pre-migrate`. On the host it converts your user config and this project; in a jail only
the project's files. Convert only once every machine that shares these files runs a yolo that
reads TOML: an older yolo finds no `config.jsonc` and starts with no packs. A launch that finds an
old file offers the conversion too, and `"convert_later": true` in your old user file stops the
offer. `yolo pack migrate <path>` does the same for a pack you wrote (`pack.json` to `pack.toml`).

A file you pass with `--user-layer` is not part of your config, so the plain command leaves it
alone. Name it to convert it: `yolo config migrate ~/work.jsonc` writes `~/work.toml` beside it,
proved over your user config the way a launch layers it, and touches nothing else. Then launch with
`--user-layer ~/work.toml`; a command that still names `~/work.jsonc` is told the new name.

## Change it with a command

You rarely need to edit a file by hand. Each command writes the right file, keeps your comments,
and runs the same checks as `yolo check`:

| To | Run | Where |
|---|---|---|
| select or drop an agent or other pack | `yolo pack add claude`, `yolo pack remove claude` | host |
| let every project's jail use a login | `yolo allow git fetch`, `yolo allow github always read` | host |
| let a project ask for more than that | `yolo allow git on_request push` | host |
| ask for it from a project | `yolo request git push main`, `yolo request github repos acme/lib` | host or jail |
| choose which provider an agent runs on | `yolo active-provider claude bedrock` | host |
| set, add to or remove any other key | `yolo config set machine.resources.cpus 4 --project`, `yolo config add`, `yolo config unset` | host (user scope), either (project) |
| find which file set a key | `yolo config why <key>` | either |

A project's `yolo request` is a request, not a grant: it takes effect only within what your user
config allows, and you approve it on the host with `yolo config apply`.

A project's TOML file reads like this:

```toml
# yolo-jail.toml
mounts = ["~/code/shared-lib"]                   # read-only, under /ctx in the jail

[install]
nix = ["postgresql", "strace"]                   # Nix packages built into the jail
mise = { node = "22", python = "3.13" }

[machine]
resources = { memory = "8g", cpus = 4 }
ports = [{ host = 3000, jail = 3000 }]           # open the dev server from your host

[env]
set = { LOG_LEVEL = "debug" }
unset = ["AWS_PROFILE"]

[requests.git]
push = ["main"]                                  # asks; your [allow.git] on_request decides
```

The settings in the rest of this page and the guides are written in the older JSONC spelling,
which `yolo config migrate` converts: `packages` is `[install] nix`, `mise_tools` is `[install]
mise`, `resources` is `[machine] resources`, `network.ports` is `machine.ports`, and a broker's
`brokered` key is `[allow.<source>]` in your config and `[requests.<source>]` in a project's.
`yolo config-ref` has the full table.

The **workspace** is the folder you run `yolo` in, exactly: there is no search upward for a config
file, and launching from a subfolder makes that subfolder its own workspace with its own jail.
Inside the jail it appears at `/workspace`, as the same live files, not a copy.

The files merge in the order above, later files winning. Legacy JSONC/JSON lists are combined without duplicates; TOML lists follow that behavior unless a schema-defined removable list is explicitly reset with `[]`. That reset is TOML-only: an empty list in JSONC/JSON keeps its legacy merge behavior. Single values and objects in a later file replace earlier ones. Any file can also pull in other
files with `include_if_found`, which skips a file that is missing, handy for a gitignored overrides
file.

After **every** edit to any of these files, run:

```bash
yolo check
```

When `yolo check` or a launch refuses a setting, the message starts with the file and line the
setting is written on, and the column its value starts at, so you can open that file at that spot:

```text
~/.config/yolo-jail/profiles.jsonc:3:19: config.use_profiles: RENAMED — this key is now `profile` …
```

If more than one file writes the same setting, the message ends by naming the others, so you can
fix every copy. The first file named is the one whose value takes effect.

## Which settings go where

Most keys work in either file. Put personal defaults in your user config and what the project needs
in `yolo-jail.toml`: its `[install]` packages and tools, MCP and language servers, `[machine]` resources, ports,
devices, and read-only `mounts`. A read-write `mounts` entry goes in your user config.

Some keys work **only in your user config**. The project folder is writable from inside the jail,
so a key that could grant an agent more (new skills, a program on your host, a different model
endpoint, a writable host folder) must not be settable there. `yolo check` refuses these in a
project config and names the file to move them to:

- `packs`
- `profiles`, `profile`, `adapters`, and a provider's address (`endpoints.<protocol>.base_url`)
- `agent_updates` and `programs`
- `cache_relocations`
- `host_management`, `host_wrappers`, `host_apply_on_launch`, `host_floor`, `host_path`, `provisioners` and `promotion_target`
- `perf_logging` and `update_check`
- `shipped_skill_prefix`, a word every skill yolo ships starts with: with
  `shipped_skill_prefix = "yolo"`, `configuring-the-jail` is delivered as
  `yolo-configuring-the-jail`, and so is every shipped pack's skill. Unset, shipped skills carry
  no marker
- `local_clones`, the clones of yours a published patch branch can be built from (see
  [your clone of a published branch](../guides/patch-series.md#your-clone-of-a-published-branch)); an
  entry that is not an absolute path to a git clone with a remote stops `yolo check`, the launch,
  `yolo host` and the `yolo pack` commands
- a `host_files` entry that names a `source` file on your host
- a host service's `command`, `env` and `doctor_cmd`, and any loophole setting its pack marks as
  user-only

One key works **only in a project config**: `brokered`, which adds repositories to that project's
[GitHub access](../guides/github.md#adding-a-repository-the-project-has-no-remote-for). The next
fresh launch asks you to approve each one, and `yolo check` refuses it in your user config.

Grants under `brokered` are split between the two: your user config lists the kinds of grant any
project may hold, `brokered.<source>.grant_kinds`, and a project config names each grant and its
repository, `brokered.<source>.grants`. A grant joins at the config-change prompt, and only if
your user config lists its kind; `yolo check` refuses either key in the other config. The git
source defines three kinds beside `read`: `create-branch`
([Git](../guides/git.md#letting-the-agent-push-its-own-branches)); `branch`, a branch in the
working set, declared in config, whose grant also names a `branch`, such as `"main"` or the
pattern `"patch/*"` ([Git](../guides/git.md#declaring-a-working-set-branch-in-config)); and `tag`,
whose grant names a `tag`, such as `"v*"`, the agent may push unasked
([Git](../guides/git.md#letting-the-agent-push-tags)). `brokered.git.repos` is refused: a git
remote joins with `git remote add`.

## A starting point

A typical user config:

```toml
# ~/.config/yolo-jail/config.toml
packs = ["claude", "guardrails"]                 # yolo pack add claude; yolo pack add guardrails

[env]
files = ["~/.config/yolo-jail/secrets.env"]      # API keys; see Providers and Models

[allow.git]
always = ["fetch"]                               # yolo allow git fetch
```

The project config above is the other half. Add `machine.forward = [5432]` to reach the host's
Postgres from the jail.

Not every key works on every setup; `machine.forward`, for example, stops a launch on Apple
Container. [Settings per setup](settings-per-setup.md) has the per-key detail.

To see the result of the merge, run `yolo describe --json`.

## Approving config changes

When a project's config changes, the next launch that starts a jail shows you the difference and
waits for your answer:

```text
Config has changed since last confirmed session.
Diff:
  + "packages": ["postgresql"]

Accept this config? [y/N]:
```

This is what stops an agent from quietly adding packages, mounts or devices by editing
`yolo-jail.toml` (or the legacy `yolo-jail.jsonc`): you approve every change. A project's first launch with a non-empty config asks
too. The record of what you approved is kept on your host, under `~/.local/share/yolo-jail/approvals/`,
where the jail cannot rewrite it. A project copied or moved to a new folder asks once more.

Edits to your user config do not ask, because only you can make them.

**Scripts, CI and editor tasks cannot answer.** A launch with no terminal to ask on stops instead,
printing the difference and the files involved. Pass `--accept-config-changes` to approve it for
that one launch; it is a flag rather than an environment variable, so an approval never carries
over to a later launch or a child process. yolo asks only when its input is a terminal, so
`yolo | tee log` still asks and `yolo < /dev/null` stops.

## When a change takes effect

**A running jail keeps most of the config it started with.** Running `yolo` in a project whose jail is
already running joins that jail; joining alone does not apply most config changes. On a host, run
`yolo config apply` to review and approve the same config-change gate. It applies eligible mount
changes under `/ctx` and supported GitHub repository-scope changes to the running jail; changes it
cannot apply remain pending for a fresh launch. Live mount injection currently supports Linux
`podman` jails; Podman Machine and Apple Container mount changes wait for a restart. The command
reports which changes took effect and which wait for restart. `yolo config drift` shows what remains
different from the jail's effective config. For a fresh jail, run:

```bash
yolo stop        # from the project folder
yolo -- claude
```

On `macos-user` every launch starts fresh, so there is no running jail to update between invocations.

**When an agent edits the config from inside the jail**, the workflow is:

1. The agent makes the change with `yolo request` or `yolo config set --project` (or edits the file) and runs `yolo check --no-build`, the fast check, to validate
   it.
2. The agent asks you to run `yolo config apply` on the host. Eligible mount and GitHub repository-
   scope changes can reach a supported running jail; the command names anything that still needs a
   restart.
3. If a restart-only change is needed, exit the jail, then run `yolo` again and review its prompt.

`yolo config drift`, run inside a jail, tells an agent whether the project config on disk differs
from the config the jail has actually applied: it exits `0` when they match, `3` when they differ
(printing the difference) and `4` when it cannot tell.
