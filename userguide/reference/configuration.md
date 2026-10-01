# Configuration

You describe an agent's environment in JSONC files: JSON that allows `// comments` and trailing
commas. This page covers where those files live, which settings go in which file, and how a change
reaches a jail. `yolo config-ref` is the complete reference for every key.

## The config files

| File | Scope | Create it with |
|---|---|---|
| `~/.config/yolo-jail/config.jsonc` | **User**: your defaults for every project | `yolo init-user-config` |
| `yolo-jail.jsonc` | **Workspace**: one project's settings, committed with it | `yolo init` |
| `yolo-jail.local.jsonc` | Workspace, this machine only; keep it out of git | by hand |

The **workspace** is the folder you run `yolo` in, exactly: there is no search upward for a config
file, and launching from a subfolder makes that subfolder its own workspace with its own jail.
Inside the jail it appears at `/workspace`, as the same live files, not a copy.

The files merge in the order above, later files winning. Lists are combined without duplicates;
single values and objects in a later file replace earlier ones. Any file can also pull in other
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
in `yolo-jail.jsonc`: its `packages`, `mise_tools`, MCP and language servers, `resources`, ports,
devices, and read-only `mounts`. A read-write `mounts` entry goes in your user config.

Some keys work **only in your user config**. The project folder is writable from inside the jail,
so a key that could grant an agent more (new skills, a program on your host, a different model
endpoint, a writable host folder) must not be settable there. `yolo check` refuses these in a
project config and names the file to move them to:

- `packs`
- `profiles`, `profile`, `adapters`, and a provider's address (`endpoints.<protocol>.base_url`)
- `agent_updates` and `programs`
- `cache_relocations`
- `host_management`, `host_wrappers`, `host_apply_on_launch`, `host_floor`, `host_path` and `promotion_target`
- `perf_logging` and `update_check`
- a `host_files` entry that names a `source` file on your host
- a host service's `command`, `env` and `doctor_cmd`, and any loophole setting its pack marks as
  user-only

## A starting point

A typical user config:

```jsonc
// ~/.config/yolo-jail/config.jsonc
{
  "packs": ["claude", "guardrails"],
  "env_sources": ["~/.config/yolo-jail/secrets.env"]   // API keys; see Providers and Models
}
```

A typical project config:

```jsonc
// yolo-jail.jsonc
{
  "packages": ["postgresql", "strace"],          // Nix packages built into the jail
  "mise_tools": { "node": "22", "python": "3.13" },
  "mcp_presets": ["chrome-devtools"],
  "network": {
    "ports": ["3000:3000"],                      // open the dev server from your host
    "forward_host_ports": [5432]                 // reach the host's Postgres from the jail
  },
  "mounts": ["~/code/shared-lib"],               // read-only, under /ctx ($YOLO_CONTEXT_DIR) in the jail
  "resources": { "memory": "8g", "cpus": 4 }
}
```

Not every key works on every setup; `forward_host_ports`, for example, stops a launch on Apple
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
`yolo-jail.jsonc`: you approve every change. A project's first launch with a non-empty config asks
too. The record of what you approved is kept on your host, under `~/.local/share/yolo-jail/approvals/`,
where the jail cannot rewrite it. A project copied or moved to a new folder asks once more.

Edits to your user config do not ask, because only you can make them.

**Scripts, CI and editor tasks cannot answer.** A launch with no terminal to ask on stops instead,
printing the difference and the files involved. Pass `--accept-config-changes` to approve it for
that one launch; it is a flag rather than an environment variable, so an approval never carries
over to a later launch or a child process. yolo asks only when its input is a terminal, so
`yolo | tee log` still asks and `yolo < /dev/null` stops.

## When a change takes effect

**A running jail keeps the config it started with.** Running `yolo` in a project whose jail is
already running joins that jail, and joining neither asks about changes nor applies them. Stop the
jail and launch again:

```bash
yolo stop        # from the project folder
yolo -- claude
```

On Apple Container, `yolo stop` cannot see the jail yet; use `container ls` and
`container stop <name>`. On `macos-user` every launch starts fresh, so there is nothing to stop.

A few things do reach a running jail when you run `yolo` in it again, such as a new API key or a
`-p` profile choice. [Settings per setup](settings-per-setup.md#what-a-running-jail-picks-up-when-you-run-yolo-again)
lists exactly which.

**When an agent edits the config from inside the jail**, the workflow is:

1. The agent edits `yolo-jail.jsonc` and runs `yolo check --no-build`, the fast check, to validate
   it.
2. The agent asks you to restart the jail.
3. You exit, then run `yolo` again, review the difference and approve it.

`yolo config drift`, run inside a jail, tells an agent whether the project config on disk differs
from the one the jail started with: it exits `0` when they match, `3` when they differ (printing the
difference) and `4` when it cannot tell.
