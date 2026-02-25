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
- freetype 2.14.1 was released September 11, 2025 (bugfix for 2.14.0 released Sep 6).
  It exists upstream at freetype.org but hasn't landed in nixpkgs yet (unstable still
  has 2.13.3 as of 2026-02).
- The pinned nixpkgs mechanism works for any package at any historical version that
  exists in nixpkgs. For versions not yet in nixpkgs, use version overrides (see below).
- `builtins.fetchTarball` is cached by nix, so repeated builds don't re-download.
- The fetched nixpkgs set is fully independent — it won't interfere with the main
  flake's pkgs. However, the pinned package's runtime dependencies (glibc, etc.)
  come from the pinned nixpkgs, which may differ from the base image. For pure
  library packages like freetype this is fine (statically linked or loaded via
  LD_LIBRARY_PATH). For complex packages with many deps, ABI compatibility should
  be considered.

## Version Overrides (overrideAttrs)
For versions that exist upstream but haven't been packaged in nixpkgs, we use
nix's `overrideAttrs` to swap the source tarball while keeping the existing build
recipe (configure flags, patches, dependencies):
```jsonc
"packages": [
  {"name": "freetype", "version": "2.14.1",
   "url": "mirror://savannah/freetype/freetype-2.14.1.tar.xz",
   "hash": "sha256-MkJ+jEcawJWFMhKjeu+BbGC0IFLU2eSCMLqzvfKTbMw="}
]
```

### How it works in nix
```nix
pkgs.freetype.overrideAttrs (old: {
  version = "2.14.1";
  src = pkgs.fetchurl {
    url = "mirror://savannah/freetype/freetype-2.14.1.tar.xz";
    hash = "sha256-MkJ+jEcawJWFMhKjeu+BbGC0IFLU2eSCMLqzvfKTbMw=";
  };
})
```

### Caching
Nix derivations are content-addressed by ALL inputs. If two jails use the same
version override (same name, version, url, hash), nix computes the same derivation
hash → instant cache hit from the local nix store. No rebuild needed.

### Limitations
- Only works for minor version bumps where the build recipe is compatible.
  Major version changes may need different configure flags or patches.
- The existing nixpkgs patches for the package are still applied. If a patch
  doesn't apply to the new source, the build will fail with a clear error.
- `url` supports nix mirror:// syntax (mirror://savannah/, mirror://sourceforge/).

## Proposed Config Format
Support strings (existing), pinned nixpkgs commits, and version overrides:
```jsonc
"packages": [
  "strace",                                    // latest from flake's nixpkgs
  {"name": "freetype", "nixpkgs": "e6f23dc0"}, // pinned to specific nixpkgs commit
  {"name": "freetype", "version": "2.14.1",    // version override (build from source)
   "url": "mirror://savannah/freetype/freetype-2.14.1.tar.xz",
   "hash": "sha256-MkJ+jEcawJWFMhKjeu+BbGC0IFLU2eSCMLqzvfKTbMw="}
]
```

## Implementation Scope
1. **flake.nix**: Parse mixed list, handle objects with `builtins.fetchTarball`
2. **cli.py**: Already passes packages as JSON — supports mixed types. Update type
   hints, help text, config-ref docs.
3. **Generated AGENTS.md**: Document that agents can pin versions
4. **docs/config-safety.md**: Add pinned package examples
5. **Tests**: Config snapshot tests handle mixed types (already JSON-based)
