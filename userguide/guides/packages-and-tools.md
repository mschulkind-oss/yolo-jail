# Packages and Tools

## Package Management

### Nix Packages (Image-Level)

Add system packages via the `packages` config array. These are baked into the container image:

```jsonc
{
  "packages": ["postgresql", "strace", "htop"]
}
```

Package names must match [nixpkgs attributes](https://search.nixos.org/packages). The image only rebuilds when this list changes.

**Non-default outputs (`.dev` for headers + `pkg-config`):** Nixpkgs splits many libraries into outputs — the default output ships only the runtime `.so`, while `.dev` carries headers and `.pc` files. For cgo / FFI builds, request the `.dev` output with a dotted shorthand or an explicit `outputs` array:

```jsonc
{
  "packages": [
    "gtk4.dev",                                      // dotted shorthand
    {"name": "gtk4", "outputs": ["out", "dev"]}      // explicit form
  ]
}
```

When a `.dev` output is selected, the image also pulls in the `.dev` outputs of every transitively propagated build input — so `pkg-config --cflags gtk4` resolves `pango → harfbuzz → fontconfig → …` without listing each by hand. `PKG_CONFIG_PATH` is preset so the `.pc` files are found out of the box. The package's runtime libraries (and those of its propagated closure) are linked into `/lib` as well, so binaries built against a `.dev` request also run — a bare `"gtk4.dev"` covers both compile and runtime.

Common outputs: `out` (default), `dev` (headers + pkg-config), `bin`, `lib`, `man`, `doc`.

**Pinned versions:** Pin to a specific nixpkgs commit for reproducibility. `outputs` works alongside pinning:

```jsonc
{
  "packages": [
    "postgresql",
    {"name": "freetype", "nixpkgs": "e6f23dc0..."},
    {"name": "gtk4", "nixpkgs": "e6f23dc0...", "outputs": ["out", "dev"]}
  ]
}
```

Find nixpkgs commits for specific versions at [lazamar.co.uk/nix-versions](https://lazamar.co.uk/nix-versions/).

### Mise Tools (Runtime-Level)

Add tools to your workspace's `mise.toml` for workspace-specific runtimes:

```toml
# mise.toml
[tools]
typst = "latest"
rust = "1.80"
```

On jail startup, `mise install` fetches declared tools. They persist across restarts in the jail-land mise store mounted at `/mise` — shared by every jail, fully independent of the host's own mise installation.

To inject tools into all jails globally, use `mise_tools` in your config:

```jsonc
{
  "mise_tools": {"neovim": "stable", "typst": "latest"}
}
```

---

## Blocked Tools

**Nothing is blocked by default.** yolo's default blocked list is empty. Blocking is opt-in, two ways:

- **The `guardrails` pack** — add `"packs": ["guardrails"]` and `grep` (with recursive flags) and `find` refuse, pointing at `rg` and `fd`.
- **Your own `security.blocked_tools`** — any tool you name, with your own message.

An entry of yours naming the same tool as a pack's **replaces** the pack's wholesale. A block is only
generated when the replacement binary is actually on the agent's PATH, so a block can never leave the
jail with neither the tool nor its alternative.

What `guardrails` blocks, and what it suggests instead:

| Blocked | Suggestion |
|---------|-----------|
| `grep` (recursive flags) | Use `rg` (ripgrep) |
| `find` | Use `fd` |

### Customize Blocked Tools

```jsonc
{
  "security": {
    "blocked_tools": [
      {"name": "grep", "message": "Use rg", "suggestion": "rg <pattern>"},
      {"name": "curl", "message": "Network access blocked"}
    ]
  }
}
```

### Bypass

Set `YOLO_BYPASS_SHIMS=1` in scripts that need blocked tools:

```bash
YOLO_BYPASS_SHIMS=1 grep -r "pattern" .
```

---
