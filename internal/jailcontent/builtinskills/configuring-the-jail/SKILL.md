---
name: configuring-the-jail
description: Bake a change into this jail's config (yolo-jail.jsonc): add a package/tool that survives restart, raise CPU/memory limits, open ports/mounts, wire MCP/LSP, set env, or enable a loophole. NOT for ephemeral npm/pip installs, which just work.
---

# Configuring the Jail

Use this when a change must be **baked into how the jail is built** so it
survives a restart: a package/runtime the project always needs, a resource
limit, a published port or mount, an MCP/LSP server, an env source, or enabling
a host-capability loophole.

**Do NOT use this for one-off installs.** The jail has internet and you can
`npm i -g`, `pip install`, `uv pip install`, or `mise use` a tool right now with
no config change and no restart. Only reach for `yolo-jail.jsonc` when the tool
must persist across restarts or be present for every future session.

## You already know the loop — here's what it leaves out

The always-on briefing already states the rule: **edit → `yolo check --no-build`
→ fix any `[FAIL]` → then STOP and ask the human to restart** (you cannot
restart this jail from inside it). Assume that. This skill covers the parts that
rule leaves out.

### Rebuild vs. restart — which changes are slow

Most changes take effect on the **next restart** (config is re-read at launch).
Only changes to the **image package set** force a slower **image rebuild**
first, and there are exactly two ways to change that set:

- `packages` — nix packages baked into the image.
- `gpu.vaapi: true` — but only when `gpu.enabled: true` and `gpu.vendor: "amd"`,
  which pulls `mesa` + `libva-utils` into the package set.

Everything else — `mise_tools`, `resources`, `network`, `mounts`,
`mcp_servers`, `lsp_servers`, `env_sources`, `loopholes`, `agents` — is
restart-only, no rebuild. Prefer `mise_tools` over `packages` for CLIs and
runtimes: it avoids the rebuild entirely. When you touch `packages` (or trigger
vaapi), say so in your handoff — on the yolo-jail dev repo a rebuild also needs
a host `just load`.

### Which file — and how the layers merge

Three layers merge, later wins:

- `~/.config/yolo-jail/config.jsonc` — user/machine defaults.
- `<workspace>/yolo-jail.jsonc` — the committed per-project config. **Edit this
  one** unless told otherwise.
- `<workspace>/yolo-jail.local.jsonc` — gitignored per-machine tweaks
  (auto-merged when present).

Merge edge cases that surprise people:

- Objects deep-merge; lists **union and de-dupe** — **except `agents`, which
  replaces wholesale** (list the full set you want, not just additions). A
  workspace `agents` may not **add** an agent that reads host files (`claude`,
  `pi`) unless `~/.config/yolo-jail/config.jsonc` already lists it — selecting one
  mounts that agent's host `settings.json` into the jail, and this file is committed
  and agent-editable. Agents that read no host files are freely selectable, and
  narrowing the set is always allowed.
- A scalar or `null` in a later layer **overrides**. Use this to disable an
  inherited entry: `"mcp_servers": { "foo": null }` removes an inherited server;
  the same trick disables an inherited preset.

## Two representative edits (then run `yolo check --no-build`)

Add a CLI tool or runtime — the preferred, no-rebuild path:

```jsonc
"mise_tools": { "neovim": "stable", "kubectl": "latest" }
```

Add a nix package — only when no mise tool exists (triggers a rebuild):

```jsonc
"packages": ["ffmpeg", "postgresql"]
```

## Some files in your home are GENERATED — don't hand-edit them

Your own agent config — `~/.claude/settings.json`, `~/.codex/config.toml`,
`~/.copilot/config.json`, and friends — is **not a file you own**. yolo composes
each one at every boot from an ordered stack of layers, so a change you make by
hand may be silently reverted, preserved, or discarded depending on the file. Edit
the *inputs*, not the output.

Find out what is generated and how, before editing anything in `~`:

```
yolo config ls                      # every composed file, its mode, and its source
yolo config render <agent> --explain   # which layer each key came from
yolo config diff <agent>            # your in-jail edits vs what yolo generated
```

The `MODE` column in `yolo config ls` is the part that decides whether your edit
survives:

- **capture** — your edit is recorded in a sidecar under `<workspace>/.yolo/prism/`
  and re-applied on later boots. Editing works, but it is invisible to anyone
  reading the config, so prefer changing the real input.
- **copy** — regenerated from scratch every boot. **Your edit is silently gone
  after a restart.** Never hand-edit these.
- **unrendered** — yolo does not compose this file; the agent owns it.

To make a change that *persists and is legible*, change the input instead: an MCP
server belongs in `mcp_servers`, an LSP in `lsp_servers`, and an arbitrary host
file you want composed into the jail belongs in `host_files` (user config only —
`yolo config-ref` has the shape). If you edited a composed file and want to undo
it, `yolo config reset <agent>` discards the captured edits.

## Four things agents reliably get wrong

Every one of these was a real wrong belief that reached a config edit. They are
all *confidently* wrong — the shape of the mistake is being sure enough not to
check, so check these even when you are sure.

### 1. A `mounts` entry lands at `/ctx/<basename>` — of the RESOLVED path

`"mounts": ["~/code/sysadmin"]` mounts at `/ctx/sysadmin`. But the basename is
taken **after symlink resolution**, so if `~/code/sysadmin` is a link to
`~/src/sysadmin-config` it lands at `/ctx/sysadmin-config` instead. Write the
explicit `"~/code/sysadmin:/ctx/sysadmin"` form whenever you care about the name
— which is any time you are about to tell someone the path.

### 2. `mounts` is read-only, and `:ro` is NOT a suffix you write

Read-only is the only mode there is; every entry gets `:ro` appended for you.
There is no writable form. And a docker-style third field **silently breaks the
mount**: `"~/x:/ctx/x:ro"` parses as one host path literally named
`~/x:/ctx/x:ro`, which does not exist, so the entry is skipped with a warning.
`yolo check` only *warns* about it too — so the config looks accepted and mounts
nothing. Two fields maximum.

### 3. An in-jail `yolo check` CANNOT judge a `mounts` path

`mounts` paths are resolved on the **host**, at launch. From inside a jail, `~`
is the jail's home, so an in-jail `yolo check` warns

```
config.mounts[0]: host path does not exist and will be skipped: /home/agent/code/sysadmin
```

for every entry you add. **That warning is expected in here and is not a
failure** — do not "fix" it by rewriting the path, and do not conclude the mount
won't work. Only a host `yolo check` can judge these. (Same reason `mounts` is
stripped from the in-jail user-config snapshot and is not inherited by a nested
jail.)

### 4. The host IS reachable in the default bridge mode

Bridge mode gives the jail its own netns; it does not cut it off from the host.
Three distinct things, and picking the wrong one is what makes people add config
they don't need:

- **The host** → `host.containers.internal`. Always there. No config.
- **A host service bound to the host's `127.0.0.1`** → also
  `host.containers.internal:<port>`. yolo asks the rootless network stack to
  forward the host's loopback into the jail; this is how yolo's own host daemons
  are reached. **This was not true before 2026-08-17** — if you "know" the host's
  localhost is unreachable from a jail, that memory is stale. Check
  `$YOLO_HOST_LOOPBACK` (`requested`/`shared` = fine) before believing otherwise.
- **`localhost:<port>` inside the jail, literally** → `network.forward_host_ports`.
  Only for a client you cannot point at another host.

`localhost` inside the jail is still the *jail's* loopback. That is the part that
is true, and probably the seed of the whole confusion.

## Don't guess at keys — the schema lives in the CLI

This skill shows two shapes on purpose. For **every** other key — `resources`,
`network`/`ports`/`forward_host_ports`, `mounts`, `mcp_servers`/`mcp_presets`,
`lsp_servers`, `env_sources`, `loopholes`, and their exact fields, allowed
values, and defaults — run the authoritative, always-current reference:

```
yolo config-ref
```

Read it before inventing a key name. `yolo check` will reject unknown keys, but
reading first saves a slow, human-gated round-trip.
