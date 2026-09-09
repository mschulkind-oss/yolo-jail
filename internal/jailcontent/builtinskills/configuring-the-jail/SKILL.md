---
name: configuring-the-jail
description: "Bake a change into this jail's config (yolo-jail.jsonc): add a package/tool that survives restart, raise CPU/memory limits, open ports/mounts, wire MCP/LSP, set env, or enable a loophole. NOT for ephemeral npm/pip installs, which just work."
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
`mcp_servers`, `lsp_servers`, `env_sources`, `loopholes`, `packs` — is
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

- Objects deep-merge; lists **union and de-dupe**.
- **`packs` is not in the merge at all — it is USER-SCOPE ONLY.** Which coding
  agent a jail gets is decided by `packs`, and that key is read from
  `~/.config/yolo-jail/config.jsonc` **directly**, never from the merged config. A
  `packs` entry in `yolo-jail.jsonc` does nothing, and no amount of layering makes
  it work: workspace scope is inexpressible by construction rather than
  validated-against. The reason is the credential boundary — a pack can carry
  skills and briefing prose an agent then follows, and a workspace config travels
  with the repo and is agent-editable, so it must not be able to name content that
  enters the jail. If a project needs its own agent configuration it already has a
  git repo and can lay out whatever it likes in the workspace.
- The `agents` key is **retired**, and an `agents` entry is a hard `yolo check`
  error on the host rather than something ignored. An agent arrives as a pack:
  `"packs": ["claude"]` is the spelling, and `yolo pack --help` is the tooling.
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
survives. There are four values and every one of them means something different
for your edit:

- **capture** — your edit is recorded in a sidecar under `<workspace>/.yolo/prism/`
  and re-applied on later boots. Editing works, but it is invisible to anyone
  reading the config, so prefer changing the real input.
- **copy** — regenerated from scratch every boot. **Your edit is silently gone
  after a restart.** Never hand-edit these.
- **rmw** — the file is the AGENT'S, and yolo only asserts its own managed keys
  into it at boot, filling defaults where they are absent and preserving
  everything else. So your edit survives *unless* it touches a key yolo manages.
  This is the mode `~/.claude.json` is in, which is why `yolo config render` has
  nothing to show for it: there is no layer fold, so there is no preview.
- **unrendered** — yolo does not compose this file at all; the agent owns it.

To make a change that *persists and is legible*, change the input instead: an MCP
server belongs in `mcp_servers`, an LSP in `lsp_servers`, and an arbitrary host
file you want composed into the jail belongs in `host_files` (user config only —
`yolo config-ref` has the shape). If you edited a composed file and want to undo
it, `yolo config reset <agent>` discards the captured edits.

## Five things agents reliably get wrong

Every one of these was a real wrong belief that reached a config edit. They are
all *confidently* wrong — the shape of the mistake is being sure enough not to
check, so check these even when you are sure.

### 1. The directory you launch from IS `/workspace`. Don't mount it.

`yolo` takes the **current working directory** as the workspace and bind-mounts
it **read-write and live** at `/workspace`. Configuring a jail for
`~/code/sysadmin` means `cd ~/code/sysadmin && yolo`, and that directory then
*is* `/workspace` — the same files, not a copy, no sync step either way.

It does **not** appear at `/ctx/sysadmin`, and putting it in `mounts` is wrong
twice over: redundant, and `mounts` is read-only, so you would be asking for a
crippled second view of a tree you can already write. **`mounts` is for paths
outside the workspace.**

Two consequences that follow from "it is literally the cwd":

- There is no `--workspace` flag and no walk up the tree looking for
  `yolo-jail.jsonc`. Launch from a subdirectory and *that subdirectory* is the
  whole workspace, with its own config and its own jail.
- `yolo-jail.jsonc` sits at the workspace root, so from inside a jail it is
  always `/workspace/yolo-jail.jsonc` — whatever it is called on the host.

### 2. A `mounts` entry lands at `/ctx/<basename>` — of the RESOLVED path

`"mounts": ["~/code/sysadmin"]` mounts at `/ctx/sysadmin`. But the basename is
taken **after symlink resolution**, so if `~/code/sysadmin` is a link to
`~/src/sysadmin-config` it lands at `/ctx/sysadmin-config` instead. Write the
explicit `"~/code/sysadmin:/ctx/sysadmin"` form whenever you care about the name
— which is any time you are about to tell someone the path.

### 3. `mounts` is read-only, and `:ro` is NOT a suffix you write

Read-only is the only mode there is; every entry gets `:ro` appended for you.
There is no writable form. And a docker-style third field **silently breaks the
mount**: `"~/x:/ctx/x:ro"` parses as one host path literally named
`~/x:/ctx/x:ro`, which does not exist, so the entry is skipped with a warning.
`yolo check` only *warns* about it too — so the config looks accepted and mounts
nothing. Two fields maximum.

### 4. An in-jail `yolo check` CANNOT judge a `mounts` path

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

### 5. Networking: name the DIRECTION before you touch a port key

Bridge mode gives the jail its own netns; it does not cut it off from the host.
Two directions, one key each, and **the two keys put the ports in opposite
orders**. Decide which row you are in before writing anything:

| Who connects | Who listens | Key | Entry order |
|---|---|---|---|
| the host | the jail | `network.ports` | `"HOST:JAIL"` |
| the jail | the host | *nothing* — dial `host.containers.internal:<port>` | — |
| the jail | the host | `network.forward_host_ports` | `"JAIL:HOST"` |

`network.ports` goes to podman's `-p` verbatim, and podman is host-first.
`forward_host_ports` is yolo's own and is jail-first — the port you write is the
one you will dial from in here. A transposed entry usually does not error; it
forwards the wrong port and presents as a broken service, so **check the order
before you debug the service.**

**There are two loopbacks.** `localhost` / `127.0.0.1` inside the jail is the
*jail's* loopback, always. The host's is a different interface reached by name.
That single fact is true and is the seed of most of the confusion here.

Two corollaries worth having:

- **You usually need no config to reach the host.**
  `host.containers.internal:<port>` already reaches host services *including*
  ones bound only to the host's `127.0.0.1` — yolo has the rootless network stack
  forward the host's loopback in, which is how yolo's own host daemons are
  reached. **Not true before 2026-08-17**: if you "know" the host's localhost is
  unreachable from a jail, that memory is stale. `$YOLO_HOST_LOOPBACK`
  (`requested`/`shared` = forwarding is in place) settles it. Reach for
  `forward_host_ports` only when something must literally see `localhost:<port>`.
- **To publish a server *out* of the jail, bind `0.0.0.0` in here**, not
  `127.0.0.1` — `network.ports` cannot publish a jail-loopback-only listener.
  Binding wide inside the jail is safe: the published port is the only way in.

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
