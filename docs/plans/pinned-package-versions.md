# RFC: Pinned Package Versions

## Summary
Allow `yolo-jail.jsonc` `packages` array to contain objects that pin a nix package
to a specific nixpkgs commit, enabling version-specific package installation without
editing the flake.

## Design

### Config Format
```jsonc
"packages": [
  "strace",
  {"name": "freetype", "nixpkgs": "e6f23dc08d3624daab7094b701aa3954923c6bbb"}
]
```

### Nix Implementation
```nix
extraPackages = map (spec:
  if builtins.isString spec then
    pkgs.${spec}
  else
    let pinnedPkgs = import (builtins.fetchTarball {
      url = "https://github.com/NixOS/nixpkgs/archive/${spec.nixpkgs}.tar.gz";
    }) { inherit system; };
    in pinnedPkgs.${spec.name}
) extraPackageSpecs;
```

### CLI Changes
- `auto_load_image` type: `List[str]` → `List[Union[str, dict]]`
- `config-ref` command: document object format and how to find commits
- Generated AGENTS.md: document pinned packages for in-jail agents
- Help text example: show mixed format

### Safety
- The config change detection (y/N diff) already covers this — adding a pinned
  package triggers the diff prompt.
- `builtins.fetchTarball` is cached by nix; no re-download on subsequent builds.
- `--impure` is already required and used.

## Files to Change
1. `flake.nix` — extraPackages parsing logic
2. `src/cli.py` — type hints, help text, config-ref docs
3. `src/entrypoint.py` — generated AGENTS.md packages section (if present)
4. `docs/config-safety.md` — add pinned package example

## Non-Goals
- Custom overlays or patches (out of scope; can be added later)
- Version resolution from version strings (requires a registry; users supply commit)
