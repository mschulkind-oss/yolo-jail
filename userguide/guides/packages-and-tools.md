# Packages and Tools

Choose the tools your agent finds on its `PATH`: system packages built with Nix, versioned
development tools through mise, and any tools you block along with what to use instead. None of
this installs anything on your own machine.

Every jail already has a working set of tools, including `git`, `rg`, `fd`, `jq`, `node`, `python3`,
`uv`, `go`, `gh` and `mise`, so add only what your project needs beyond that.

## Package Management

### Nix Packages (Image-Level)

Add system packages with the `packages` key, usually in the project's `yolo-jail.jsonc`. yolo builds
them into the jail image with [Nix](https://nixos.org), a reproducible package builder:

```jsonc
{
  "packages": ["postgresql", "strace", "htop"]
}
```

Package names are [nixpkgs](https://search.nixos.org/packages) names. The image is rebuilt only when
this list changes, and a rebuilt image reaches a jail at its next fresh start: `yolo check`, then
`yolo stop` and launch again. A package that fails to build stops the launch with Nix's own error.

**On a Mac** each different package list builds its own Linux image, so the first launch after a
change is slower; a package missing from every binary cache is built in a temporary container on
your runtime. On `macos-user`, packages are built as native Mac programs instead, and a package with
no Mac build stops the launch. Mark a Linux-only package so other setups skip it:

```jsonc
{ "packages": [ { "name": "strace", "platforms": ["linux"] } ] }
```

**Packages inside a collection:** an entry is a nixpkgs attribute path, and it installs what `nix build nixpkgs#<entry>` would build. So a dot reaches into a package collection the way it does on the command line:

```jsonc
{
  "packages": [
    "rocmPackages.clr",                 // a member of the rocmPackages collection
    "gst_all_1.gstreamer.dev",          // a member's dev output
    {"name": "rocmPackages.clr", "platforms": ["linux"]}  // the object form takes the same path
  ]
}
```

A bare collection such as `"rocmPackages"` is not a package, so `yolo` stops the launch and names members you could write instead. A name that is not letters, digits, `_` and `-` is quoted the way Nix quotes it: `"nerd-fonts.\"m+\""`.

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
An entry can also rebuild a package from a different upstream version with `version`, `url` and
`hash`; `yolo config-ref` shows the form.

### Mise Tools (Runtime-Level)

[mise](https://mise.jdx.dev) is a tool-version manager: use it for a particular Node, Python, Rust
or other tool version, or a tool nixpkgs does not have. A project's own `mise.toml` works as it does
outside the jail:

```toml
# mise.toml
[tools]
typst = "latest"
rust = "1.80"
```

When the jail starts, `mise install` fetches the declared tools. They are kept in yolo's own mise
store at `/mise`, separate from any mise on your host. With podman the store is shared by every
jail, so a version is downloaded once; on Apple Container each project has a store of its own, so
each project downloads its versions once.

To add tools without a `mise.toml`, use `mise_tools`, in the project config for one project or in
your user config for every jail:

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
