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
  replaces wholesale** (list the full set you want, not just additions).
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
