---
title: "A `packages` entry is a nixpkgs attribute path — and installs what `nix build` would"
date: 2026-08-22
status: accepted
tags: [config, nix, packages, flake]
summary: "A `packages` entry is a nixpkgs attribute path, resolved by the attribute walk Nix itself does, so `rocmPackages.clr` installs a collection member and `gtk4.dev` an output, exactly what `nix build nixpkgs#<entry>` builds. yolo adds one rule of its own: an output keeps its parent as the base derivation, because the /lib farm and the header closure read the base."
stage: BUILT
next: "Graduate into a system doc (system-doc): the resolver, its refusals and the /lib-farm base rule, from §5 and the ledger. Before that, measure the non-container half on a Mac: a macos-user launch with a dotted entry, which no CI job runs"
---

# A `packages` entry is a nixpkgs attribute path — and installs what `nix build` would

**Status:** built 2026-10-06 on `d775dde6`, after [OQ-1](#8-decision-ledger) was ruled on
2026-10-05. MEASURED: in a nested podman jail launched from a throwaway workspace with the
freshly built `yolo`, one entry of each shape in [§5.1](#51-the-resolver) was installed as
designed (headers and `.pc` files on `PKG_CONFIG_PATH`, the base's `.so` in `/lib`, the
texsource files linking Nix's derivation and not the rejected one), and a bare
`rocmPackages` stopped the launch with the member advice of [§5.4](#54-the-refusals). Every
store path the integration tests compare is Nix's own answer, read from `nix build --dry-run`
against the flake's pin. UNMEASURED: the non-container resolution on a Mac. Its Linux
evaluation is tested, and no macos-user launch with a dotted entry has run.

> **In short.** A dotted `packages` entry is not yolo syntax. It is the attribute path
> `nix build nixpkgs#<entry>` takes, walked the way Nix walks it, so every entry means what it
> means on the command line. yolo's own contribution is one rule: an output keeps its parent
> as the base derivation.

**Why it matters.** `"rocmPackages.clr"`, the ROCm runtime the AMD GPU passthrough needs, was
refused: the one-dot pattern read every dot as an output selection. Collection members had no
spelling at all.

**The shape.** One walk ([§5.1](#51-the-resolver)), one split into base and outputs, and every
consumer reading that split: the image contents, the `/lib` farm and the non-container
`buildEnv`.

**Start at [§4.3](#43-the-collision-and-why-nix-decides-it)**, the one place two readings of a
path existed. The rest falls out of following Nix there.

**Needs your ruling:** None.

**Reads with:** [`provisioner-evidence.md`](provisioner-evidence.md#3-the-nix-resolver-in-depth)
(how `packages:` materializes off-container),
[`image-staging-vs-baking.md`](../reference/image-staging-vs-baking.md) (the image build path),
[`mise-node-dynamic-linking.md`](../reference/mise-node-dynamic-linking.md) (the `/lib` symlink
farm and dlopen discovery).

---

## 1. Goal and principles

### 1.1 Goal

Let `packages` in `yolo-jail.jsonc` name anything nixpkgs builds by attribute path: a
top-level package (`strace`), an output (`gtk4.dev`), a member of a package collection
(`rocmPackages.clr`, `gst_all_1.gstreamer`) and a member's output
(`gst_all_1.gstreamer.dev`). This holds on every backend: the container image, and the
non-container `buildEnv` that `macos-user` builds.

### 1.2 Principles

- **P1. One syntax, and it is Nix's.** An entry installs what `nix build nixpkgs#<entry>`
  builds, against the flake's pinned nixpkgs. A user who can spell it for `nix build` can spell
  it here, quoting a name with punctuation as the Nix language does, and yolo never reinterprets
  a name.
- **P2. Preserve the `/lib` symlink farm contract.** Adding a library to `packages`, or its
  `.dev` output for headers, links its shared libraries into `/lib` for `dlopen()` by soname.
- **P3. Keep collection diagnostics honest and actionable.** A bare collection (`xorg`,
  `rocmPackages`, `python3Packages`) is refused by name, with members sampled and one written
  out as an entry the user could use instead.

---

## 2. The failure this fixes

Until this design, a string entry allowed at most one dot (`packageNameRe` was
`^[a-zA-Z0-9_-]+(\.[a-zA-Z0-9_-]+)?$`), and the flake read that dot as `<package>.<output>`.
So `"rocmPackages.clr"` resolved `rocmPackages` as the package and `clr` as its output, and the
collection guard refused it:

```text
error: yolo: `packages` entry "rocmPackages.clr" resolves to nixpkgs.rocmPackages,
which is a package COLLECTION of 114 attributes, not a package — it has no
derivation to install. (from the `packages` entry "rocmPackages.clr" — the part
after the dot selects an OUTPUT, not a collection member)
```

The same message went on to tell the user that *"a collection member is NOT selectable from
`packages`"*. That sentence is gone ([§5.4](#54-the-refusals)), because it is no longer true.

### 2.1 Why runtime `nix shell` is not the answer

`nix shell nixpkgs#rocmPackages.clr` works for a temporary task, but it does not replace a
baked package:

| Property | Baked via `packages:` | Runtime `nix shell` |
| :--- | :--- | :--- |
| Available to non-nix tooling | **Yes** (in `/bin` / `/lib`) | **No** (subshell only) |
| On `PATH` for all jail processes (MCP servers, hooks, subagents) | **Yes** | **No** |
| Symlinked into the `/lib` farm for `dlopen()` by soname | **Yes** | **No** |
| Startup cost per invocation | **Zero** | Re-resolves and fetches |
| Fully offline jail runs | **Yes** | Fails on a cold cache |

The `/lib` farm row is the hard limit. Non-nix binaries (Python wheels, node native addons,
downloaded binaries) find a shared library by soname (`dlopen("libamdhip64.so.7")`) through
`LD_LIBRARY_PATH=/lib:/usr/lib`, and a library that is not linked into `/lib` is not found,
whatever `nix shell` provides.

---

## 3. What this does NOT do

- **No arbitrary Nix in JSON.** No expressions, function calls or overrides in
  `yolo-jail.jsonc`. The attribute-path grammar is all of Nix this accepts, and only a subset of
  it ([§5.2](#52-the-grammar-yolo-check-enforces)).
- **No abolition of the collection guard.** A bare set (`"xorg"`, `"rocmPackages"`) is still a
  hard error.
- **No renaming of what Nix names.** yolo never maps an entry to a different attribute, not
  even where nixpkgs deprecates one (`xorg.libX11` warns in this pin and still resolves).

---

## 4. How Nix reaches a value, and the traps

### 4.1 Output selection is attribute access

In Nix a derivation's outputs are attributes on the derivation:

- `pkgs.gtk4` is a derivation with outputs `[out dev devdoc debug]`.
- `pkgs.gtk4.dev` is the same derivation, with `dev` selected.
- `pkgs.rocmPackages.clr` is a derivation reached through a collection.

`gtk4.dev` and `rocmPackages.clr` are the same operation, one attribute access per name, and
`nix build` performs exactly that walk. When the value it reaches is one output of a derivation,
it carries Nix's own marker, `outputSpecified = true` with `outputName` naming the output, and
that marker is how `nix build` knows to build only that output.

### 4.2 The base-derivation trap in the `/lib` farm

The `/lib` farm applies `lib.getLib` to each entry's **base derivation**, and a `.dev` request
walks the base's propagated inputs for the header closure. `getLib` is a no-op on a value that
already selects an output, so `getLib gtk4.dev` is `gtk4.dev` itself: headers and `.pc` files,
no `.so`. A resolver that kept only the walked value would leave `libgtk-4.so` out of `/lib`,
and every binary built against the headers would fail to start.

So the resolver yields two things, not one:

- **The base derivation.** For an output, the derivation one step up (`gtk4`); for anything
  else, the value itself (`rocmPackages.clr`).
- **The selected outputs.** `["dev"]` for `gtk4.dev`, or none (the default) otherwise.

### 4.3 The collision, and why Nix decides it

A path's last name can be both one of its parent's outputs and an attribute that is not that
output. Then the path has two readings: the output, with the parent as base, or the attribute,
as its own base. The two give **different image contents**, even though yolo's contents step
reads the attribute under both: the `/lib` farm takes `getLib` of whichever is the base.

**Measured 2026-10-01, over this flake's nixpkgs pin** (`e158d9ed`), by the probe in
[the appendix](#appendix-re-running-the-collision-probe):

- **Top level: none**, among 24,786 derivations.
- **One level down: 1,961**, among the 79,811 derivations of the 292 package sets marked
  `recurseForDerivations`. All but one are `texlivePackages.<pkg>.texsource`; the other is
  `cygwin.newlib-cygwin-nobin.bin`, a cross-compilation set.

**[OQ-1](#8-decision-ledger), ruled (C) on 2026-10-05: follow Nix exactly.** Of the three
readings put to the ruling, the other two were rejected:

| Reading | What `texlivePackages.abc.texsource` installs | Verdict |
| :--- | :--- | :--- |
| **(A)** The output wins on the last name | The `texsource` output of `abc-2.0b.drv` | Rejected |
| **(B)** Refuse the ambiguous path | Nothing; a `throw` names both readings | Rejected |
| **(C)** Follow Nix | What `nix build nixpkgs#texlivePackages.abc.texsource` builds | **Ruled** |

**(A) and (C) do not coincide.** Measured 2026-10-05 with `nix build --dry-run --json` against
the pin, with nothing built:

| Installable | Derivation | Output path |
| :--- | :--- | :--- |
| `texlivePackages.abc.texsource` (C) | `v7cj0riq…-abc-2.0b-texsource.drv`, a fixed-output derivation | `out`: `/nix/store/xvfaa5f5…-abc-2.0b-texsource` |
| `texlivePackages.abc^texsource` (A) | `ybx390i2…-abc-2.0b.drv` | `texsource`: `/nix/store/synsxp3g…-abc-2.0b-texsource` |

The attribute is the separate derivation that `abc` takes as an input. It carries
`outputSpecified = true` with `outputName = "out"`, so Nix builds its `out`, and that is what
the image holds. Classified by (A) instead, the entry would make `abc` the base, and the `/lib`
farm would link `getLib abc`, which is `abc`'s default output `fdfdnz1a…-abc-2.0b-tex`, in place
of the texsource derivation. That is the difference the integration test catches.

> [!WARNING]
> **The last name being in the parent's `outputs` is not evidence that the path names an
> output.** `texsource` is in `abc`'s `outputs` (`[tex texdoc texsource]`), and the attribute
> is another derivation. The test is Nix's marker on the value reached: `outputSpecified`, with
> `outputName` equal to the last name, under a parent derivation that lists that output
> ([§5.1](#51-the-resolver)).

---

## 5. The design as built

### 5.1 The resolver

Every entry, string or object `name`, goes through one resolver in [`flake.nix`](../../flake.nix):

1. **Parse** the entry into names on its dots; a quoted name is one name, its dots included
   ([§5.2](#52-the-grammar-yolo-check-enforces)).
2. **Walk** from the nixpkgs root one name at a time, testing `?` before each read. A missing
   attribute is an abort that `tryEval` cannot catch, so the test is what lets the non-container
   path skip an unknown entry ([§5.3](#53-the-non-container-path)). The walk never forces the
   value it ends on.
3. **Classify** the value reached:
   - not found: refused on the image path, skipped on the non-container path;
   - found and not a derivation: the collection or non-package refusal ([§5.4](#54-the-refusals));
   - found and an output of its parent, by the test in [§4.3](#43-the-collision-and-why-nix-decides-it):
     the base is the parent and the output is the last name;
   - any other derivation: its own base, with no output selected.
4. **Consume** the split. The image contents take the selected outputs of the base (a `.dev`
   output brings its propagated closure's `.dev` outputs, as before). The `/lib` farm takes
   `getLib` of the base and, for a `.dev` request, of its propagated closure.

The image therefore always receives the value the path names: for an output, the contents read
`base.<output>`, which is that value. Only the `/lib` farm and the header closure read the base.

| Entry | Base | Outputs | Image gets | `/lib` farm gets |
| :--- | :--- | :--- | :--- | :--- |
| `strace` | `strace` | default | `strace` | `getLib strace` |
| `gtk4.dev` | `gtk4` | `dev` | `gtk4.dev` and its propagated `.dev` outputs | `getLib gtk4` and its propagated libraries |
| `rocmPackages.clr` | `rocmPackages.clr` | default | `clr` | `getLib clr` |
| `rocmPackages.clr.icd` | `rocmPackages.clr` | `icd` | `clr.icd` | `getLib clr` |
| `texlivePackages.abc.texsource` | the texsource derivation | default | that derivation's `out` | `getLib` of it |

### 5.2 The grammar `yolo check` enforces

A `packages` string, and an object's `name`, must match `packageNameRe`
([`internal/config/config.go`](../../internal/config/config.go)):

- names separated by single dots;
- each name is letters, digits, `_` and `-`, or quoted as Nix quotes it: `nerd-fonts."m+"`,
  `rubyPackages."http_parser.rb"`;
- no empty name, no leading or trailing dot, no unclosed quote and no empty quotes.

This is a subset of what `nix build` parses (it also takes `nerd-fonts.m+` unquoted), and
every entry it accepts means what Nix means. **Measured 2026-10-06** over the
pin: 11 of the 84,949 member names in the 296 recursed package sets need the quotes, and among
top-level names only `makeScopeWithSplicing'`, a function. The refusal names the three shapes,
the `nix build` spelling it mirrors and `nix search nixpkgs <name>` as the way to find a path.

**The object form takes the same path** ([NP-D2](#NP-D2)). `{"name": "rocmPackages.clr",
"platforms": ["linux"]}` is valid, because a macos-user launch with a package that has no Mac
build tells the user to write exactly `{"name": "<pkg>", "platforms": ["linux"]}`. Pinned
(`nixpkgs`) and version-override (`version`, `url`, `hash`) objects resolve their `name` the
same way, against their own nixpkgs.

### 5.3 The non-container path

`yoloNoncontainerPackages`, the native `buildEnv` a macos-user launch builds, resolves with the
same walk and split against the flake's own `system`:

- **Unknown is skipped, not fatal, in the flake.** An entry the walk cannot find is skipped with
  a warning naming the missing step, for instance `nixpkgs.rocmPackages has no attribute
  "nosuch"`. The macos-user launch then refuses host-side, as it does for any skipped package.
- **A collection, a non-package or an object naming an output twice is fatal**, outside the
  `tryEval` around platform availability, which would otherwise relabel a config error
  `no <system> build`.
- **A skip names the entry as written**, `cudaPackages.libcublas.dev` and not its base, so the
  macos-user refusal's `{"name": "<pkg>", "platforms": ["linux"]}` can be filled in from it
  verbatim ([NP-D6](#NP-D6)). The non-container floor drops its own copy of the **base** path
  a user declared (`gtk4` for `gtk4.dev`); a dotted base never matches a floor name, which are
  all top-level.

### 5.4 The refusals

Each names the entry as written and says what to do ([NP-D4](#NP-D4)):

| Case | Path | Says |
| :--- | :--- | :--- |
| A name is missing | image: abort; non-container: skip | which step failed (`nixpkgs.gtk4 has no attribute "nosuch"`), the outputs when that step is a derivation, and `nix search nixpkgs <name>` |
| A bare collection | both: abort | three sampled members, one written as an entry (`"rocmPackages.amdsmi"`) with the `nix build` it mirrors, and the `nix eval … --apply builtins.attrNames` that lists them all |
| A large set with no package among the first 500 names scanned | both: abort | what was seen, and the same listing command; never "holds no packages" |
| A set holding no packages at all (`lib`) | both: abort | remove the entry |
| An object whose `name` selects an output and which lists `outputs` | both: abort | the one spelling that says it once: `{"name": "gtk4", "outputs": ["dev","out"]}` |

The member sample scans names until it finds three packages, at most 500
([NP-D5](#NP-D5)). A fixed window was not enough: **measured 2026-10-06**,
`python3Packages`' first 49 names are removed aliases that throw, so the old 40-name window
found no member in a set of 12,184 packages and said it held none.

---

## 6. Alternatives considered

| Alternative | Verdict | Rationale |
| :--- | :--- | :--- |
| **(A) The output wins on the last name** | ❌ Rejected by [OQ-1](#8-decision-ledger) | Installs a different store path from `nix build` on 1,960 texlive paths ([§4.3](#43-the-collision-and-why-nix-decides-it)) |
| **(B) Refuse an ambiguous path** | ❌ Rejected by [OQ-1](#8-decision-ledger) | Refuses paths Nix builds without complaint |
| **An explicit `attr` key on object specs** (`{"attr": "rocmPackages.clr"}`) | ❌ Rejected | Two syntaxes for one idea, when the attribute walk makes the string form work |
| **Flake-ref syntax** (`nixpkgs#rocmPackages.clr`) | ❌ Rejected | A command-line spelling in JSON, and the `nixpkgs#` prefix would carry nothing |
| **Keep only the walked value** (no base) | ❌ Rejected | `getLib gtk4.dev` loses the `.so` files ([§4.2](#42-the-base-derivation-trap-in-the-lib-farm)) |
| **The design's first resolver sketch** (`lib.attrByPath` with a `null` default) | ❌ Rejected | `null` cannot be told from an attribute whose value is `null`, and its output test was the leaf-in-`outputs` test (A) |

---

## 7. What done looks like

Observable, and each checked on 2026-10-06:

- `yolo check` accepts every shape in [§5.1](#51-the-resolver)'s table, the quoted names and the
  object form naming a member, and refuses an empty name, a stray quote or a space, naming the
  shapes it takes.
- A nested jail with `gtk4.dev`, `rocmPackages.clr`, `gst_all_1.gstreamer.dev`,
  `texlivePackages.abc.texsource`, `nerd-fonts."m+"` and
  `{"name": "rocmPackages.rocminfo", "platforms": ["linux"]}` boots with `gtk4.pc` and
  `gstreamer-1.0.pc` on `PKG_CONFIG_PATH`, `libgtk-4.so`, `libamdhip64.so` and
  `libgstreamer-1.0.so` in `/lib` from their base derivations, `rocminfo` on `PATH`, the M+
  fonts under `/share/fonts`, and `/source/latex/abc/*` linking `xvfaa5f5…` and not `synsxp3g…`.
- A launch with a bare `rocmPackages` stops at the nix build, printing the member advice.

The tests, each revert-checked on 2026-10-06:

- **Unit** ([`packageattrpath_test.go`](../../internal/config/packageattrpath_test.go)),
  through `ValidateConfig`: the accepted shapes and the refusals in one table. Removing the
  `validatePackages` call fails every refusal row.
- **Integration** ([`packagecollection_test.go`](../../integration/packagecollection_test.go)),
  evaluation only: what each shape installs, compared with `nix build --dry-run` and
  `lib.getLib` against the pin; the collection advice for `xorg`, `rocmPackages` and
  `python3Packages`; the missing-name refusal and skip; the double-output refusal on both
  paths; a skip named as written. The first four fail against the previous `flake.nix`, the
  texsource case alone fails when the output test is reduced to reading (A), and the skip-name
  test fails when the skip is named by its base again.

---

## 8. Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="OQ-1"></a>[`OQ-1`](#8-decision-ledger) | (C), under delegation: follow Nix exactly. A `packages` entry with dots installs what `nix build nixpkgs#<path>` builds, so the collision resolves to the attribute, never by ranking output against member | 2026-10-05 | [§4.3](#43-the-collision-and-why-nix-decides-it) | ✅ `flake.nix` `splitPackageAttr` |
| <a id="NP-D1"></a>[`NP-D1`](#8-decision-ledger) | *Implementation decision.* "Is an output of its parent" is Nix's marker on the value reached (`outputSpecified`, `outputName` equal to the last name, a parent derivation listing it), not the last name's membership in the parent's `outputs` | 2026-10-06 | [§5.1](#51-the-resolver) | ✅ `flake.nix` `splitPackageAttr` |
| <a id="NP-D2"></a>[`NP-D2`](#8-decision-ledger) | *Implementation decision.* An object's `name` is the same attribute path, in every object form, so the macos-user refusal's advice works for every string entry; a `name` that selects an output plus an `outputs` list is refused | 2026-10-06 | [§5.2](#52-the-grammar-yolo-check-enforces) | ✅ `validatePackages`, `objectOutputs` |
| <a id="NP-D3"></a>[`NP-D3`](#8-decision-ledger) | *Implementation decision.* Quoted names are accepted as Nix writes them, and the unquoted name stays letters, digits, `_` and `-`: a subset of Nix's grammar that spells every package measured | 2026-10-06 | [§5.2](#52-the-grammar-yolo-check-enforces) | ✅ `packageNameRe`, `packageAttrPath` |
| <a id="NP-D4"></a>[`NP-D4`](#8-decision-ledger) | *Implementation decision.* A missing name is a yolo refusal naming the failed step on the image path (it was Nix's raw missing-attribute error) and a skip naming it on the non-container path | 2026-10-06 | [§5.4](#54-the-refusals) | ✅ `missingPackageError` |
| <a id="NP-D5"></a>[`NP-D5`](#8-decision-ledger) | *Implementation decision.* The collection member sample is an early-exit scan of at most 500 names, and "holds no packages" is said only of a set scanned whole | 2026-10-06 | [§5.4](#54-the-refusals) | ✅ `nonPackageError` |
| <a id="NP-D6"></a>[`NP-D6`](#8-decision-ledger) | *Implementation decision.* A non-container skip names the entry as written (it named the base, `gtk4` for `gtk4.dev`); the floor's de-duplication keeps the base | 2026-10-06 | [§5.3](#53-the-non-container-path) | ✅ `noncontainerResolved` |

---

## Appendix: re-running the collision probe

The 2026-10-01 measurement in [§4.3](#43-the-collision-and-why-nix-decides-it) as one
expression. Save it as `collide.nix` and run `nix eval --impure --json --file collide.nix`;
pin `rev` and `narHash` to the `nixpkgs` node of `flake.lock`. It finished in a few minutes in a
jail.

```nix
let
  src = builtins.fetchTree {
    type = "github"; owner = "nixos"; repo = "nixpkgs";
    rev = "e158d9ed9b51c98974c5e66e1ba1c9e0255fecaa";
    narHash = "sha256-hKlVl12B1dF0Q5vd9dY3lIJM5mFGWYSlXwSLAqHZ1+s=";
  };
  pkgs = import src { system = "x86_64-linux"; overlays = [ ];
    config = { allowAliases = false; allowUnfree = true; }; };
  lib = pkgs.lib;
  try = e: let r = builtins.tryEval e; in if r.success then r.value else null;
  # Output names whose attribute is not that output.
  shadowed = p: builtins.filter
    (o: try ((p.${o}.outputName or null) == o) == false)
    (let os = try (p.outputs or [ "out" ]); in if builtins.isList os then os else [ ]);
  isDrv = v: try (lib.isDerivation v) == true;
  isSet = v: try (builtins.isAttrs v && !(lib.isDerivation v)
    && (v.recurseForDerivations or false)) == true;
  row = name: v: { inherit name; outputs = shadowed v; };
  top = builtins.concatMap (n: let v = try pkgs.${n}; in
    if isDrv v then [ (row n v) ] else [ ]) (builtins.attrNames pkgs);
  nested = builtins.concatMap (s: let set = pkgs.${s}; in
    builtins.concatMap (m: let v = try set.${m}; in
      if isDrv v then [ (row "${s}.${m}" v) ] else [ ])
    (let ns = try (builtins.attrNames set); in if builtins.isList ns then ns else [ ]))
    (builtins.filter (n: isSet (try pkgs.${n})) (builtins.attrNames pkgs));
  hits = rows: builtins.filter (r: r.outputs != [ ]) rows;
in { top = hits top; nested = hits nested; }
```

Nix's own answer for one colliding path, the 2026-10-05 measurement, from the repository root:

```console
$ nix build --dry-run --json --no-link --option substitute false --inputs-from . \
    'nixpkgs#texlivePackages.abc.texsource' 'nixpkgs#texlivePackages.abc^texsource'
```
