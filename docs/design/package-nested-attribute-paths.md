---
title: "Nested nixpkgs attribute paths in `packages`"
date: 2026-08-22
status: draft
tags: [config, nix, packages, flake]
summary: "A `packages` entry like `rocmPackages.clr` fails because yolo reads any dot as an output selection. In Nix both are the same attribute walk, so one path-walking resolver supports nested collections and output selection alike — provided it keeps the base derivation for the /lib symlink farm."
---

# Nested nixpkgs attribute paths in `packages` — and why output selection is the same operation

**Status:** DESIGN SKETCH, 2026-08-22. Nothing built. **Re-verified 2026-08-23: still nothing
built**, and `60376fed` does not invalidate any premise below — see the postscript.

> [!NOTE]
> **Postscript, 2026-08-23 — audit against the tree, and against `60376fed`.**
>
> **"Nothing built" holds — re-verified 2026-09-02.** Every mechanism this doc proposes to change
> is untouched:
> `packageNameRe` is still the single-optional-dot pattern `^[a-zA-Z0-9_-]+(\.[a-zA-Z0-9_-]+)?$`
> (declared at [`internal/config/config.go#L165`](../../internal/config/config.go#L165), enforced at
> [`validate.go#L224`](../../internal/config/validate.go#L224) — both anchors drifted ~30 lines
> under the unrelated `use_profiles` key-census work and are repinned here);
> `parseDottedSpec` still splits on
> one dot at [`flake.nix#L173`](../../flake.nix#L173) under the comment *"Validator (yolo check)
> rejects multi-dot strings, so we only handle one dot here"*; and `flake.nix` contains no
> `attrByPath`, no `hasAttrByPath`, and no `resolvePackagePath`. §5.1's resolver does not exist in
> any form.
>
> **One worked example has died in the pin (found 2026-09-02):** `llvmPackages_16` was removed
> from nixpkgs (*"unmaintained and obsolete"*), so this doc's `llvmPackages_16.libclang.dev`
> example no longer evaluates against the pinned rev (`f13ff45a`). The *shape* it illustrates is
> unchanged — substitute a maintained set (e.g. `llvmPackages_19.libclang.dev`) when implementing
> the test list in §7. The other examples still hold against the pin: `rocmPackages` has exactly
> 114 attributes, `rocmPackages.clr` and `xorg.libX11` are derivations, `gtk4.outputs` is
> `[out dev devdoc debug]`.
>
> **`60376fed` (2026-08-20) does not change this doc's premises — it *is* one of them.** This doc
> was written on 2026-08-22, two days after that commit, and §2.1 already cites it by hash and
> quotes the error it introduced. `requireDerivation` is at
> [`flake.nix#L255`](../../flake.nix#L255), exactly as §2.1 says, and
> [`integration/packagecollection_test.go`](../../integration/packagecollection_test.go) exists
> already — so §7's first bullet list is *extending* a suite, not creating one. Nothing here is
> stale.
>
> **One consequence of `60376fed` the body does not yet record.** §2.1 quotes the refusal
> accurately but *truncated*. The full `nonPackageError` message ends with two more lines
> ([`flake.nix#L248-L254`](../../flake.nix#L248)):
>
> ```text
>   Members include: <sample>, ...
>   A collection member is NOT selectable from `packages`: use the member's own
>   top-level attribute if nixpkgs has one (`nix search nixpkgs <member>` to
>   check — most of xorg.* is top-level today, e.g. libX11), and drop "<entry>".
> ```
>
> That second line is a **normative statement to the user that this design reverses.** It is not a
> diagnostic that happens to fire; it is advice telling people the thing this doc wants to make
> work is not a thing. So P3 ("keep collection diagnostics honest and actionable") acquires a
> second obligation the body does not state: shipping this design means rewriting that advice, not
> merely narrowing when the throw fires. A user who followed the shipped advice and rewrote
> `xorg.libX11` to `libX11` is not wrong afterwards — but the message must stop saying the dotted
> form is unselectable, or the error becomes the lie.
>
> Everything else in §1–§7 is stated as of 2026-08-22 and still reads true.

**The short version.** A `packages` entry like `"rocmPackages.clr"` currently fails because yolo assumes any dot indicates an output selection on a top-level package. But in Nix, derivation outputs *are* attributes on the derivation itself. Unifying dotted strings as a general attribute path walk (`lib.attrByPath`) supports arbitrary nested collections (`rocmPackages.clr`, `llvmPackages_16.libclang.dev`, `darwin.apple_sdk.frameworks.Security`) without new syntax, provided the resolver preserves the base derivation for the `/lib` symlink farm and header propagation.

**Reads with:** [`noncontainer-nix-environment.md`](noncontainer-nix-environment.md) (how `packages:` materializes off-container), [`image-staging-vs-baking.md`](image-staging-vs-baking.md) (the image build path), [`mise-node-dynamic-linking.md`](mise-node-dynamic-linking.md) (the `/lib` symlink farm and dlopen discovery).

---

## 1. Goal & Principles

### 1.1 Goal
Allow `packages` in `yolo-jail.jsonc` to resolve nested nixpkgs package collections (`rocmPackages.clr`, `xorg.libX11`, `llvmPackages_16.libclang`, `darwin.apple_sdk.frameworks.Security`) alongside output selection (`gtk4.dev`, `rocmPackages.clr.dev`), across container image builds and non-container (`macos-user` / `guest`) environments.

### 1.2 Principles
- **P1. One uniform syntax.** A user should not have to learn a separate syntax for a top-level package, a nested collection member, or a package output. Attribute access in Nix is uniform; yolo's package resolution should match.
- **P2. Preserve the `/lib` symlink farm contract.** Adding a library to `packages` (or `.dev` for headers) must continue to link its shared `.so` libraries into `/lib` for runtime `dlopen()` discovery by non-nix tooling.
- **P3. Keep collection diagnostics honest and actionable.** A bare collection with no derivation (`xorg`, `rocmPackages`, `python3Packages`) must continue to be refused by name with member samples, rather than failing downstream inside Nix's string coercion.

---

## 2. Problem Statement & Why Runtime Workarounds Fall Short

### 2.1 The Current Failure
Currently, string entries in `packages` support at most one dot (`packageNameRe = ^[a-zA-Z0-9_-]+(\.[a-zA-Z0-9_-]+)?$`), which [`flake.nix`](../../flake.nix#L173-L179) parses exclusively as `<base-package>.<output>`.

When a user specifies a collection member:
```jsonc
"packages": ["rocmPackages.clr"]
```
yolo attempts to resolve base attribute `imagePkgs.rocmPackages` and output `clr`. Because `rocmPackages` is an attribute set of 114 derivations (not a derivation itself), the `requireDerivation` guard ([`flake.nix:255`](../../flake.nix#L255), added in `60376fed`) refuses the build:

```
error: yolo: `packages` entry "rocmPackages.clr" resolves to nixpkgs.rocmPackages,
which is a package COLLECTION of 114 attributes, not a package — it has no
derivation to install. (from the `packages` entry "rocmPackages.clr" — the part
after the dot selects an OUTPUT, not a collection member)
```

### 2.2 Why Runtime `nix shell` is Insufficient
An interactive `nix shell nixpkgs#rocmPackages.clr` works for temporary build tasks, but fails to replace baked packages:

| Property | Baked via `packages:` | Runtime `nix shell` |
|---|---|---|
| Available to non-nix tooling | **Yes** (in `/bin` / `/lib`) | **No** (subshell only) |
| On `PATH` for all jail processes (MCP servers, hooks, subagents) | **Yes** | **No** (interactive subshell only) |
| Symlinked into the `/lib` farm for `dlopen()` by soname | **Yes** | **No** |
| Startup cost per invocation | **Zero** | Re-resolves and fetches |
| Fully offline / air-gapped jail runs | **Yes** | Fails on cold cache |

The **/lib symlink farm row** is the hard limit: non-nix binaries (Python wheels, node native addons, downloaded binaries) locate shared libraries by soname (`dlopen("libamdhip64.so.6")`) using `LD_LIBRARY_PATH=/lib:/usr/lib`. If a library is not baked into the image and linked into `/lib`, runtime `dlopen()` fails regardless of `nix shell`.

---

## 3. What this Proposal does NOT Propose (Non-Goals)

- **No arbitrary Nix syntax in JSON.** We are not supporting arbitrary Nix expressions, function calls, or uncurried attribute overrides in `yolo-jail.jsonc`.
- **No abolition of the collection guard.** Specifying a bare attribute set (e.g. `"xorg"` or `"rocmPackages"`) remains a hard error.

---

## 4. Architectural Analysis & The Traps

### 4.1 Output Selection *is* Attribute Access
In Nix, a derivation's outputs are exposed as attributes on the derivation attrset:
- `pkgs.gtk4` → derivation with outputs `["out", "dev", ...]`
- `pkgs.gtk4.dev` → derivation corresponding to the `dev` output
- `pkgs.rocmPackages.clr` → derivation corresponding to the `clr` package

Both `gtk4.dev` and `rocmPackages.clr` are reached by walking the same dotted attribute path.

### 4.2 The Base Derivation vs. Output Trap in `/lib` Farm Extraction
In [`flake.nix:489-508`](../../flake.nix#L489-L508), `extraLibPackages` builds the runtime `/lib` symlink farm by running `imagePkgs.lib.getLib` on the **base derivation**:
```nix
# Runtime-library derivations for the /lib farm. getLib is applied
# to the BASE derivation of each spec, never the selected outputs:
# getLib is a no-op on an output-specified entry, so deriving the
# farm from extraPackages made a ".dev" request... contribute no
# runtime .so at all
extraLibPackages = builtins.concatMap (r:
  let
    devRequested = r.outputs != null && builtins.elem "dev" r.outputs;
    propagatedLibs = map (i: imagePkgs.lib.getLib i.pkg)
      (builtins.filter (i: i.key != r.drv.outPath)
        (propagatedClosure r.drv));
  in
    [ (imagePkgs.lib.getLib r.drv) ]
    ++ imagePkgs.lib.optionals devRequested propagatedLibs
) resolvedPackageSpecs;
```

If `"gtk4.dev"` were naively resolved as a leaf derivation `drv = imagePkgs.gtk4.dev`:
- `lib.getLib (imagePkgs.gtk4.dev)` returns `gtk4.dev` itself (which contains only headers and `.pc` files, no `.so` shared libraries).
- `libgtk-4.so` would be **missing from `/lib` and `/usr/lib`**.

Therefore, the resolver must distinguish:
- **`drv` (Base Derivation):** The parent derivation providing the package (`gtk4`, `rocmPackages.clr`), from which `.outPath`, `getLib`, and `propagatedClosure` are derived.
- **`outputs`:** The requested outputs (e.g. `["dev"]`), or `null` for default.

---

## 5. Proposed Solution

### 5.1 Resolution Algorithm (`flake.nix`)
We replace `parseDottedSpec` with a path-walking parser that walks `lib.attrByPath` from head to tail:

```nix
# Walk a dotted string against an attribute root (e.g. imagePkgs).
# Returns { drv = <base-derivation>; outputs = [ "dev" ] | null; }
resolvePackagePath = rootPkgs: entryStr:
  let
    parts = builtins.filter builtins.isString (builtins.split "\\." entryStr);
    isDrv = p: p != null && builtins.isAttrs p && (p ? outPath);

    walk = prefix: remaining:
      let
        curr = pkgs.lib.attrByPath prefix null rootPkgs;
      in
        if curr == null then
          throw "yolo: `packages` entry \"${entryStr}\" does not exist in nixpkgs (failed at ${builtins.concatStringsSep "." prefix})."
        else if isDrv curr then
          if remaining == [] then
            # Reached end of path on a valid derivation
            { drv = curr; outputs = null; }
          else if builtins.length remaining == 1
                  && (builtins.elem (builtins.head remaining) (curr.outputs or ["out"])) then
            # Final component is an output of this derivation (.dev, .lib, .out)
            { drv = curr; outputs = [ (builtins.head remaining) ]; }
          else
            # Continue traversing if curr is also an attrset (passthru / sub-package)
            let nextVal = pkgs.lib.attrByPath (prefix ++ [ (builtins.head remaining) ]) null rootPkgs;
            in if nextVal != null then
              walk (prefix ++ [ (builtins.head remaining) ]) (builtins.tail remaining)
            else
              throw "yolo: `packages` entry \"${entryStr}\": \"${builtins.head remaining}\" is neither a sub-attribute nor a valid output of ${builtins.concatStringsSep "." prefix} (valid outputs: ${builtins.concatStringsSep ", " (curr.outputs or ["out"])})."
        else if remaining == [] then
          # Exhausted path but target is a collection or non-package attrset
          throw (nonPackageError (builtins.concatStringsSep "." prefix) entryStr curr)
        else
          walk (prefix ++ [ (builtins.head remaining) ]) (builtins.tail remaining);
  in
    walk [ (builtins.head parts) ] (builtins.tail parts);
```

### 5.2 Host-Side Validation

The pattern and its enforcement live in two files, and both move (verified 2026-08-23):

1. Update `packageNameRe` — declared at [`internal/config/config.go#L165`](../../internal/config/config.go#L165), not in `validate.go` — to allow multi-segment dotted identifiers:
   ```go
   packageNameRe = regexp.MustCompile(`^[a-zA-Z0-9_-]+(\.[a-zA-Z0-9_-]+)*$`)
   ```
2. Update the error message on regex mismatch, raised at [`internal/config/validate.go#L224`](../../internal/config/validate.go#L224), which today reads *"expected '\<name>' or '\<name>.\<output>' (letters, digits, '_' and '-' only; at most one dot)"*:
   `"expected '<name>', '<collection>.<name>', or '<name>.<output>' (letters, digits, '_' and '-' separated by dots)"`.
3. Rewrite the collection-refusal advice in `nonPackageError` ([`flake.nix#L248`](../../flake.nix#L248)) — see the postscript at the top. It currently tells the user a collection member is not selectable from `packages`, which is the sentence this design falsifies.

### 5.3 Non-Container Backend Resolution (`yoloNoncontainerPackages`)
Update `noncontainerResolved` in `flake.nix` to use `pkgs.lib.hasAttrByPath` and `pkgs.lib.attrByPath` instead of flat `src ? ${attr}` checks, evaluating `lib.meta.availableOn` and `meta.available` on the resolved base derivation.

---

## 6. Alternatives Considered

| Alternative | Verdict | Rationale |
|---|---|---|
| **Explicit `attr` key on object specs** (`{"attr": "rocmPackages.clr"}`) | ❌ **Rejected** | Creates two syntaxes for one idea when uniform attribute path traversal makes standard string entries *just work*. |
| **Flake-ref syntax** (`nixpkgs#rocmPackages.clr`) | ❌ **Rejected for v1** | Unnecessary CLI-style divergence in JSON config; does not align with existing `packages: [...]` conventions. |
| **Naive leaf derivation resolution** (discarding base package) | ❌ **Rejected** | Breaks runtime library `/lib` farm extraction (`getLib gtk4.dev` loses `.so` files). |

---

## 7. Test Plan

1. **Integration Tests ([`integration/packagecollection_test.go`](../../integration/packagecollection_test.go)):**
   - Verify `rocmPackages.clr` resolves as a valid package derivation.
   - Verify `rocmPackages.clr.dev` resolves with `rocmPackages.clr` as base and `dev` as output.
   - Verify `xorg.libX11` resolves.
   - Verify `gtk4.dev` continues to resolve with `gtk4` as base and `dev` as output.
   - Verify bare collections (`xorg`, `rocmPackages`, `python3Packages`) are still caught and refused with member hints.
2. **Nested Jail Verification:**
   - Launch nested jail with `"packages": ["rocmPackages.clr"]` and verify successful container image build and execution.

---

## 8. Open Questions

1. 💬 **OQ-1: Namespace collision between collection members and derivation outputs.** If package
   `foo` has an output named `bar` AND nixpkgs has an attrset `foo.bar` containing package `baz`, how
   should `foo.bar` resolve — to the `bar` *output* of the `foo` derivation, or to the `bar` *member*
   of the `foo` collection?

   **What it decides:** the resolver's central disambiguation rule, and therefore the whole feature.
   §5.1's `walk` already encodes an answer — it tests `builtins.elem (head remaining) (curr.outputs
   or ["out"])` *before* it tries a deeper attribute — so ruling the other way is not a tweak to that
   code, it is a different algorithm. It also decides what §4.2's base-derivation contract means in
   the ambiguous case: an output resolution keeps `foo` as the base and feeds `getLib foo` to the
   `/lib` farm, while a member resolution makes `foo.bar` the base and feeds `getLib foo.bar`. Those
   produce **different image contents**, silently, from the same config string. This is the rule the
   roadmap's 💬 10 row names as gating the item as a whole rather than one corner of it.

   _Leaning:_ **output wins on the leaf; a deeper path wins over both.** Concretely: if the remaining
   path is exactly one component and that component is in `curr.outputs`, resolve it as an output;
   otherwise keep walking. In practice the two namespaces do not overlap — nixpkgs output names are a
   tiny closed set (`out`, `dev`, `lib`, `bin`, `man`, `doc`, `info`, `debug`) and collections are
   named `*Packages`, `xorg`, `gst_all_1` — so the rule almost never fires, which is an argument for
   picking the *predictable* one rather than the clever one. The alternative worth stating, and the
   reason I hold this loosely: **refuse the ambiguity instead of ranking it.** A `throw` naming both
   candidate resolutions and telling the user to disambiguate costs nothing today (no known collision
   exists to break) and cannot silently produce the wrong `/lib` farm tomorrow. If you want the
   resolver to have no surprising cases at all, that is the ruling to make.

   **Answer:**
   > _(empty — fill in when decided)_
