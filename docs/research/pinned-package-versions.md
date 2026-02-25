# Research: Pinned Package Versions in Yolo Jail

## Problem Statement
Users need specific versions of nix packages (e.g., freetype 2.14.1) inside jails.
The current `packages` array only supports names resolved against the flake's nixpkgs
input — always the latest version on that pin. There's no way to get a different version
of a specific package without editing the flake itself.

## Current Flow
1. **Config**: `yolo-jail.jsonc` has `"packages": ["strace", "htop"]` (strings only)
2. **CLI** (`src/cli.py`): reads `config["packages"]`, passes as JSON via
   `YOLO_EXTRA_PACKAGES` env var to `nix build --impure`
3. **Nix** (`flake.nix`): reads env var, `builtins.fromJSON`, maps strings to
   `pkgs.${name}` against the flake's nixpkgs input, appends to image `contents`

## Nix Version Pinning Mechanisms
- **`builtins.fetchTarball`**: fetches a specific nixpkgs archive by commit hash.
  Works with `--impure`. Returns a path that can be `import`-ed to get a pkgs set.
  ```nix
  let pinnedPkgs = import (builtins.fetchTarball {
    url = "https://github.com/NixOS/nixpkgs/archive/<commit>.tar.gz";
  }) { inherit system; };
  in pinnedPkgs.freetype
  ```
- **Nix version lookup**: https://lazamar.co.uk/nix-versions/ maps package names
  to nixpkgs commits per version per channel.

## Key Findings
- freetype 2.14.1 does **not** appear in any nixpkgs channel as of 2025-06. The
  latest available is 2.13.3 (commit `e6f23dc0`). Freetype 2.14.x may not have
  been released upstream yet, or may need a custom overlay.
- The mechanism is still valuable: any package at any historical version can be
  pulled in via its nixpkgs commit hash.
- `builtins.fetchTarball` is cached by nix, so repeated builds don't re-download.
- The fetched nixpkgs set is fully independent — it won't interfere with the main
  flake's pkgs. However, the pinned package's runtime dependencies (glibc, etc.)
  come from the pinned nixpkgs, which may differ from the base image. For pure
  library packages like freetype this is fine (statically linked or loaded via
  LD_LIBRARY_PATH). For complex packages with many deps, ABI compatibility should
  be considered.

## Proposed Config Format
Support both strings (existing) and objects (pinned) in the `packages` array:
```jsonc
"packages": [
  "strace",                                    // latest from flake's nixpkgs
  {"name": "freetype", "nixpkgs": "e6f23dc0"}  // pinned to specific nixpkgs commit
]
```
The `nixpkgs` field is a commit hash (short or full) from github.com/NixOS/nixpkgs.
Users find the right commit via https://lazamar.co.uk/nix-versions/.

## Implementation Scope
1. **flake.nix**: Parse mixed list, handle objects with `builtins.fetchTarball`
2. **cli.py**: Already passes packages as JSON — supports mixed types. Update type
   hints, help text, config-ref docs.
3. **Generated AGENTS.md**: Document that agents can pin versions
4. **docs/config-safety.md**: Add pinned package examples
5. **Tests**: Config snapshot tests handle mixed types (already JSON-based)
