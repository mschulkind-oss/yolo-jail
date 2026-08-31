# A reproducible tool environment for the NON-CONTAINER notches, via nix

**Status:** EXPLORATORY analysis, 2026-08-02 — **re-verified against the tree 2026-08-23**, and
partly overtaken by it. §8 **Option 1 has largely SHIPPED** (the rename and the GC root, carried
as the roadmap's **N2** `11f8bb72` and **N1** `23cee7a6`, both 2026-08-05). Options 2 and 3 are
still sketches for their *packages* half; the launch verb Option 2 depends on **shipped
2026-08-30** as `yolo host -- <cmd>` (with `yolo --at host -- <cmd>` as its alias), so "nothing
launches at the `host` notch" is no longer true, and **OQ-1 — the question every other one here
is subordinate to — is still open.** Seven questions live, two settled; see the Decision Ledger.

> **Postscript, 2026-08-23 (item 4 added 2026-08-30) — what moved underneath this doc.** §1–§8
> keep their original 2026-08-02 tense: read them as the analysis that *motivated* the work, not
> as a description of today's tree. Four things changed, each annotated in place:
>
> 1. **The mechanism was renamed and generalized** (N2, `11f8bb72`). `yoloDarwinPackages` →
>    **`yoloNoncontainerPackages`**, `darwinUnavailablePackages` → **`yoloUnavailablePackages`**
>    (`flake.nix:1204`, `flake.nix:1210`), and the hardcoded `DarwinSystem = "aarch64-darwin"` →
>    `darwinpkg.NativeSystem()`, derived from GOOS/GOARCH
>    (`internal/darwinpkg/darwinpkg.go:29-32`, `:46-76`). This doc proposed `yoloHostPackages`;
>    the shipped name is `noncontainer`, and the commit is explicit that it rejected the doc's
>    suggestion for §7's reason — "`host` is one notch and `guest` needs the identical
>    mechanism, so naming it after either one would be the same lie in a new spelling"
>    (`flake.nix:1196-1203`). The **Go package is still called `darwinpkg`**; that rename is
>    mechanical and deliberately left for the consumer that needs it
>    (`internal/darwinpkg/darwinpkg.go:8-14`).
> 2. **The realized profile is GC-rooted** (N1, `23cee7a6`) — OQ-2, answered *and* built. §5.4's
>    "one shipped gap" is closed; the reasoning that made it safe is preserved there as a warning.
> 3. **`x86_64-darwin` is no longer dead for this flake.** §5.1's first caveat is **retracted**
>    (see `### ⚠ Retracted` there): the flake grew a second nixpkgs input pinned to 26.05 for
>    that system alone (`flake.nix:22-42`), after the 26.11 throw took the macOS nightly red for
>    29 consecutive nights. Re-measured today: an Intel Mac gets **5 of 6** agent CLIs, not zero.
> 4. **The `host` notch grew an exec half, and the apply command was renamed** (2026-08-30;
>    [`host-agent-environment.md`](host-agent-environment.md) §5.2, §6 and its OQ-2/OQ-7
>    rulings). `yolo host -- <cmd>` ships, with `yolo --at host -- <cmd>` as its systematic
>    alias — so §3's premise that yolo never launches a process at `host`, and every conclusion
>    this doc derives from it (§3 row 2, §4.2, §7), is scoped to the **apply command**, not to
>    the notch. `yolo apply --host` was REMOVED rather than deprecated: the command is now
>    `yolo host apply` (or `yolo apply --at host`), and this doc's prose says so throughout.

> **Renamed 2026-08-04, from `host-nix-environment.md`.** The old name was wrong in the same
> way `yoloDarwinPackages` is wrong — it named the mechanism after the first notch that needed
> it. This is about **every notch without a container image**: `guest` needs the identical
> mechanism for the identical reason (a real home, no image, so no baked tool closure), and
> §7 below already concluded exactly that. Keeping "host" in the title invited building
> Phase 7's package layer twice, which is the one mistake this doc exists to prevent.
>
> The rename is not cosmetic for the STRUCTURE: §5 is still organized as "macOS vs Linux" and
> §3 still enumerates host consumers. Both axes are secondary to the notch axis. §3 now opens
> with the notch framing; §5's platform split is legitimately about platform (nix-ld has no
> darwin analogue) and is left alone.

**The thesis, verbatim from the maintainer:**

> "how we can use nix-shell in the mac and the linux cases when running on the host. I think
> this is orthogonal to confinement methods, so could be interesting. thinking about installing
> copilot got me here. perhaps we want to mimic our in jail envs even more somehow."

**The short version.** One of the three claims survives cleanly, one survives in a narrowed
form, and one does not.

- **(b) `nix-shell` is the right shape — NO.** The same reason `flake.nix` already records for
  rejecting a devShell on `macos-user` applies at the `host` notch *with more force*, and the
  measurement is in §4.1: a devShell puts **22 PATH entries and 121 environment variables**
  (clang/gcc, GNU coreutils/sed/grep, `CC`, `AR`, `NIX_CFLAGS_COMPILE`) in front of the user's
  own userland. The right shape is the **`buildEnv` + PATH-prepend** yolo already ships — and,
  for the imperative case, `nix profile`.
- **(c) "mimic the in-jail env more" — YES, but only for about half of what the jail does**, and
  the split is this doc's most useful output (§6). Roughly: the *tool closure* ports, the
  *isolation* does not, and one whole mechanism (`/lib` + `LD_LIBRARY_PATH` + nix-ld) is
  Linux-container-specific and has **no darwin analogue worth building** (§5.3).
- **(a) orthogonal to confinement — NO, and this is the finding that matters most.** A host nix
  environment is not a peer of confinement; it is the **missing provisioning primitive below the
  `jail` notch**, and `guest` (env-manager Phase 7, unbuilt) needs the identical mechanism.
  Building it as "a host feature" would build Phase 7's package layer twice. §7.

**The biggest single finding, and it is a surprise:** the mechanism is **already built, already
cross-platform, and misnamed**. `packages.yoloDarwinPackages` is defined inside
`flake-utils.lib.eachDefaultSystem`, so it exists **per system** — including
`x86_64-linux`. Verified live from inside this Linux jail:

```
$ YOLO_EXTRA_PACKAGES='["fzf","hello"]' nix build --impure --no-link --print-out-paths \
    '/workspace#packages.x86_64-linux.yoloDarwinPackages'
/nix/store/miclw504w9z0m2c68c68jjgqcxdjghd5-yolo-darwin-packages
$ ls /nix/store/miclw…-yolo-darwin-packages/bin
fzf  fzf-share  fzf-tmux  hello
```

So "the Linux case" is not a gap in the *mechanism*; it is a gap in the *name*, the
*filtering* (`darwinResolved` resolves against `pkgs` and gates on
`lib.meta.availableOn { system = <the flake's own system>; }`, which is correct for any system),
and the *caller* (`internal/darwinpkg` hardcodes `.#packages.aarch64-darwin.…` and only the
macos-user orchestrator invokes it). That reframes the work from "design a host nix
environment" to "**generalize a `darwin`-named package to a `host`-named one and give the
non-macos-user notches a caller**."

> [!NOTE]
> **Two of those three gaps are closed (N2, `11f8bb72`, 2026-08-05).** The name is now
> `yoloNoncontainerPackages` / `noncontainerResolved` (`flake.nix:368`, `:1204`) and the caller
> resolves `darwinpkg.NativeSystem()` instead of a frozen `aarch64-darwin`
> (`internal/darwinpkg/darwinpkg.go:46-76`). The filtering never needed fixing — it was correct
> for any system all along, which is what the paragraph above claims and what the commit
> re-verified. **The third gap is the live one:** the only caller is still the macos-user
> orchestrator, so `host` and Linux `guest` still have no consumer. Re-checked 2026-08-23.
>
> Note also that the hardcoded `aarch64-darwin` was not the only frozen-arch bug of its class:
> grepping the literal (rather than trusting BACKLOG E8's entry) turned up a third instance in
> `yolo check`'s extra-platforms remedy, which told an Intel Mac user to delete a line they did
> not have. It survived because nothing tested the remedy string.

**Reads with:** [`macos-user-nix-and-features.md`](macos-user-nix-and-features.md) §1 (the
shipped mechanism, in detail — this doc does not restate it),
[`yolo-as-environment-manager.md`](yolo-as-environment-manager.md) §3.5 + §4 (the dial and the
dep-handoff design), [`host-render-target.md`](host-render-target.md) §2.1 (the kind census),
[`../plans/environment-manager-plan.md`](../plans/environment-manager-plan.md) Phase 4.3/6/7
(what is deferred), [`../plans/pack-host-management-plan.md`](../plans/pack-host-management-plan.md)
§8.3/§8.4 (the `install_hints` matrix and the two defects it had, both since fixed),
[`package-nested-attribute-paths.md`](package-nested-attribute-paths.md) (what a `packages:`
entry may *say* — a sketch, and it resolves through the same `noncontainerResolved` block §2
dissects).

**Two docs are blocked on this one's OQ-1** and spell it `N3/OQ-1`:
[`boundary-broker.md`](boundary-broker.md) §10 (its approval tier's shape differs by the answer)
and [`agent-auth-modes.md`](agent-auth-modes.md) (which notes it *escaped* the dependency by
making auth-as-packs host-complete without a launcher). The roadmap tracks it as
[row 4, "Non-container nix"](../plans/roadmap.md).

---

## 0. Vocabulary — five terms this doc leans on

Pinned because three of them are routinely conflated in conversation about nix.

**A `devShell`** — a `mkShell` derivation, entered with `nix develop` / `nix-shell`, or dumped
with `nix print-dev-env`. It is a **build environment**: it carries the whole `stdenv` (a C
compiler, GNU coreutils, `make`) plus ~100 build variables, because its job is to let you
*compile* the thing. `nix develop` starts a subshell.

**A `buildEnv`** (a.k.a. a *profile*) — `pkgs.buildEnv { paths = [ … ]; }`, a derivation whose
output is a single symlink tree (`<out>/bin`, `<out>/lib`, …) union-ing exactly the packages
you named. It carries **no** toolchain and **no** environment variables. You realize it with
`nix build --print-out-paths` and consume it by prepending `<out>/bin` to `PATH`. Nothing is
"entered." This is what `packages.yoloNoncontainerPackages` is (`yoloDarwinPackages` when this
doc was written; renamed 2026-08-05).

**`nix profile`** — an imperative, mutable, per-user generation-tracked profile at
`~/.local/state/nix/profiles/profile`, whose `bin` is on the user's PATH via their nix install.
`nix profile add nixpkgs#hello` mutates it. It *does* record a locked flake URL per entry
(verified: `Locked flake URL: …nixpkgs-26.11pre1045908.a5cbcfe95479…narHash=…`), so it is not
*unpinned* — but the pin is **whatever nixpkgs the registry resolved at install time**, per
entry, drifting independently. It is not yolo's `flake.lock`.

**`nix shell`** (not `nix develop`) — the forgotten fourth mechanism, and it behaves like a
`buildEnv`, not a devShell. Verified:

```
$ nix shell nixpkgs#hello --command bash -c 'echo $PATH'
/nix/store/lxra5…-hello-2.12.3/bin:/home/agent/.yolo-shims:…   ← ONE entry prepended
```

**The `host` notch** — `confinement: host` from the dial (§4 of the env-manager design): no
confinement, your machine, your credentials. `yolo host apply` renders config into the real
`$HOME` and **launches nothing** — which is exactly why the `launch` kind is honored-but-unbuilt
*there*, at that command (`render.HostUnimplemented`, `internal/render/fieldset.go:96-98`,
re-read 2026-08-30: *"launch flags apply to a process yolo starts, and `yolo host apply` only
configures your tools — it never runs them, so there is no argv to inject them into.
`yolo host -- <program>` is the notch that does the launching"*). Hold onto that: it is the crux
of §4.2 — and note the sentence's tail now names a verb that SHIPPED (postscript 4).

> [!NOTE]
> **That sentence was re-worded after this doc was written, and the reword is an argument in
> this doc's favor.** The original text ("launch flags need a launcher"; for `env`, "the only
> place to set these off-container is your shell profile") read as a fact about being
> *off-container*, which it is not — at `guest`, macos-user already execs the agent. The
> comment above the map now says so and names `yolo --at host -- <cmd>` (this doc's §8 Option 2)
> as what would make both kinds renderable at `host` (`internal/render/fieldset.go:83-96`). So
> the refusal is scoped to the **command**, not the notch, and the codebase now points at
> Option 2 from inside the refusal itself.

---

## 1. What is already solved, stated precisely

The most common way to waste effort here is to design something that exists. So, exactly —
**state column re-verified 2026-08-23**, with the 2026-08-02 reading kept where it moved:

| Capability | State | Where |
|---|---|---|
| A nix expression that materializes `packages:` as a **pure, toolchain-free profile** | **SHIPPED** | `flake.nix:1204` `packages.yoloNoncontainerPackages` (was `yoloDarwinPackages`) |
| …and it works for **every** `eachDefaultSystem` system, Linux included | **SHIPPED, and now advertised by its name** | `flake.nix:1196-1210`; verified on `x86_64-linux` |
| Realizing it and putting `<out>/bin` on an agent's PATH, no container | **SHIPPED** | `internal/darwinpkg` → `internal/macosuser/orchestrator.go` |
| Per-package "no build on this platform" filtering, warn-and-skip | **SHIPPED** (a hard error is decided but unbuilt: revival plan **A2**) | `noncontainerSkippedNames` / `yoloUnavailablePackages` (`flake.nix:472-474`, `:1210`) |
| Pinning to yolo's `flake.lock` rather than the user's channel | **SHIPPED** (structural — it *is* the flake) | `flake.lock`; rev `241313f4` at first writing, `f13ff45a` today, **plus a second `nixpkgs-x86-darwin` input** (§5.1) |
| A target system that follows the machine instead of a constant | **SHIPPED 2026-08-05** (N2) — was `DarwinSystem = "aarch64-darwin"` | `darwinpkg.NativeSystem()`, `internal/darwinpkg/darwinpkg.go:46-76` |
| A **gcroot** on the realized profile | **SHIPPED 2026-08-05** (N1) — the root *is* the build's `--out-link`, so it cannot be skipped | `internal/darwinpkg/gcroot.go`, `darwinpkg.go:117-141` |
| The resolved profile **reported** to a human | **SHIPPED 2026-08-05** for `describe` (gated on `PrimBakedImage` being absent) and for `check`'s macos-user section | `internal/cli/describe.go:161-190`, `internal/cli/check/sections_macos.go:103-134` |
| `yolo check` verifying nix + `/nix` + trusted-user on macOS | **SHIPPED** | `cli/check/section_nix_probe.go`, `sections_macos_platform.go` |
| The same, on **Linux** | **STILL NOT WIRED** — `nixDaemonStoreCheck` and the platform block are `IsMacOS`-gated | `section_nix_probe.go:28-31`, `check.go:77-78` (OQ-9) |
| The profile report, on **Linux** | **STILL NOT WIRED** — `checkPackageProfile` is called only from the macos-user backend section, which returns early off macOS | `sections_macos.go:100`; the gate is `sections_macos.go:46-60` (OQ-9) |
| A **caller** for the profile at the `host` notch | **STILL DOES NOT EXIST** | `yolo host apply` never touches nix (`cli/apply.go`: no `packages` handling at all) |
| `packages:` reported by `yolo host apply` / `check --at host` | **STILL DOES NOT EXIST** — `packages` is not a pack *kind*, so the `FieldSet` census never sees it; it is a top-level config key with no host handler | `render/fieldset.go`, `cli/apply.go` (OQ-8) |

**So the honest framing was never "should yolo build a host nix environment."** It was: *yolo
already has one, for one notch on one platform, called by one backend, under a platform-specific
name, with no GC root and no non-macOS `check` coverage.* Two of those five qualifiers are now
gone (the name, the GC root). What remains is the shape of the whole doc: **one notch, one
backend, no non-macOS coverage.** Everything below is about whether to generalize that and what
it buys.

### 1.1 The one thing genuinely absent: `packages:` has no meaning below `jail`

This is worth isolating, because it is the actual hole. The env-manager design says (§3.4) that
`check --at host` should print:

```
✗  packages   yolo does not manage packages here (no image to bake)
```

That is the *shipped intent* — packages are a jail concept, and below `jail` the design hands
off to `install_hints`. But it is not what `macos-user` does: that backend **does** manage
packages below the jail notch, natively, via the buildEnv. The design's own table
([`host-render-target.md`](host-render-target.md) §2.2) records this as
`macos-user | … | program: ✅ (native nix)`.

So the design contains a latent inconsistency that this exploration surfaces: **`packages:` is
already honored at a sub-jail notch on one platform and declared unmanageable at a sub-jail
notch in another section.** Resolving that inconsistency *is* the decision this doc exists to
tee up (OQ-1).

**Update 2026-08-23 — half of the reporting half shipped, and it picked a side.** `yolo describe`
now prints the resolved profile for any notch **without** `PrimBakedImage`, reading the GC-root
symlink rather than invoking nix so the command stays instant
(`internal/cli/describe.go:161-190`). That is the *opposite* of the env-manager's
`✗ packages   yolo does not manage packages here`: the shipped line says yolo does manage them,
and names the store path. `yolo host apply` still says nothing at all, so the inconsistency is now
**between two yolo commands** rather than between a doc and a backend — which is a sharper
version of the same question, not a resolution of it (OQ-8).

---

## 2. What `install_hints` is for, and what a nix env would and would not replace

`install_hints` (`packdecl.DepRequirement` → `internal/depcheck`) maps a *pack's* declared
binary to a package name per host package manager, so `check-deps` / `yolo host apply` can print
`brew install claude-code` or `nix profile install nixpkgs#claude-code`. It **never installs**
(`depcheck.Check` docstring: *"It never installs anything — it reports"*).

**They are complementary, not competitors, and the reason is a boundary, not a preference.**

| | `install_hints` | a host nix env (buildEnv on PATH) |
|---|---|---|
| Whose machine changes | **the user's**, permanently, in their manager's namespace | nothing outside `/nix/store` |
| Reproducibility | **none** — `brew install claude-code` is "whatever brew has today" | yolo's `flake.lock`, byte-identical per platform |
| Who runs it | the user (or Phase 4.3's confirm-gated offer) | yolo, silently, as a build |
| Scope | machine-global | **process-scoped** if yolo launches; otherwise nothing |
| Works with no nix | **yes** — the entire point | no |
| Coverage across the six agent packs | **weak** (see below) | **6/6 on both arm64 Linux and arm64 macOS** (§5.1) |

The coverage asymmetry is stark and is most of the motivation. From §8.3's verified matrix:

| manager | agent packs covered (of 6) |
|---|---|
| `apt` | **0** — no Debian/Ubuntu suite packages any of them, in any release |
| `dnf` | **1** (`pi-coding-agent`, and only in Rawhide) |
| `pacman` | **2** (`openai-codex`, `opencode`; the other four are AUR-only, which `pacman -S` cannot install) |
| `brew` | 6 — but **4 are casks** (`claude-code`, `copilot-cli`, `codex`, `antigravity-cli`), which `depcheck.Manifest` used to emit as `brew "<x>"` → a Brewfile that fails on those four (defect §8.4, **fixed 2026-08-02** by the `brew-cask` hint key) |
| `nix` | **6** — but 3 (`claude-code`, `github-copilot-cli`, `antigravity-cli`) are `unfree`, so bare `nix profile install` refuses (defect §8.3) |

**Read the bottom two rows together and the case makes itself.** On a Linux host that is not
Arch, `install_hints` covers **zero to one** of six agent CLIs. `nix` is the only manager that
covers all six — which means *the reproducible path and the only-path-that-works path are the
same path on Linux*. That is a much stronger argument for a nix route than "reproducibility is
nice."

**But it does not make `install_hints` unnecessary, for three reasons.**

1. **A user with no `/nix` gets nothing from a nix env** (§5.4), and telling a brew user to
   install nix to get `copilot` is a worse experience than `brew install copilot-cli`.
2. **`install_hints` answers a different question.** It covers a pack's *host dependencies*
   generally — the motivating case in the pack-host plan is `fzf` and `fd` for a
   file-suggestion pack, not agent CLIs. `fzf` is in nixpkgs, in brew, in apt, in pacman. For
   *that* class, `install_hints` is fine and a nix env is overkill.
3. **The "print a remedy" path is the floor the design deliberately guarantees** (env-manager
   §3.5: *"the composed manifest is always the floor"*). A nix env is an *additional* offer, not
   a replacement for the floor.

**A concrete, cheap consequence worth noting even if nothing else here is built.** Both §8.3
defects are one-line-ish fixes that a nix route makes *more* important, not less:

- The `unfree` refusal is real, and it also hits **`packages:` today** — but the failure is at
  **BUILD, not eval**, and the distinction matters for the fix. Re-measured:

  ```console
  $ YOLO_EXTRA_PACKAGES='["claude-code"]' nix eval  --impure …#yoloDarwinPackages.name
  yolo-darwin-packages                        # eval SUCCEEDS

  $ YOLO_EXTRA_PACKAGES='["claude-code"]' nix build --impure …#yoloDarwinPackages
  error: Refusing to evaluate package 'claude-code-2.1.214' … because it has an
         unfree license (‘unfree’)
         at …/pkgs/build-support/buildenv/default.nix:113:9
  ```

  So the `tryEval` wrapper around `availableOn` (now `flake.nix:410-415`) **absorbs the unfree
  assertion during eval** — the package is reported *available*, the skip path never runs, and
  the abort surfaces later inside `buildEnv`. The guard is not missing a check so much as
  succeeding at the wrong time: `availableOn` reads `meta.platforms`/`badPlatforms` and unfree
  is not a platform fact, so no amount of platform probing will catch it.

  The consequence for the user is what the earlier framing got right: putting an agent CLI in
  `packages:` yields a nix `check-meta` trace rather than the warn-and-skip that the mechanism
  promises.

  **FIXED (2026-08-02); the guard is `flake.nix:416-444` today.** The resolver tests
  `drv.meta.available` — nixpkgs' own verdict, which reads without throwing — alongside
  `availableOn`, and routes a failure into the existing skip-and-warn path.

> [!WARNING]
> **Three traps in that fix, each of which cost a measurement to find. Do not re-derive them.**
>
> - **`availableOn` alone can never catch unfree, and adding more platform probing will not
>   help.** It reads `meta.platforms`/`badPlatforms`; a licence is not a platform fact. The
>   `tryEval` around it *absorbs* the unfree assertion, so the package is reported available and
>   the abort lands later, inside `buildEnv` — an eval that succeeds is not evidence the build
>   will (`flake.nix:416-425`).
> - **Test `meta.available`, not `meta.unfree`** (`flake.nix:425-434`). `meta.available` flips back to true under
>   `NIXPKGS_ALLOW_UNFREE=1` / `allowUnfreePredicate`, so a user who deliberately opted in still
>   gets the package instead of a silent skip. **yolo does not set that variable on the user's
>   behalf** — unfree is a licence decision the user makes once, machine-wide, and slipping the
>   override in would make it for them silently. (This used to cite `allow_exec` as the
>   sibling invariant; that key is gone — see `pack-system.md` §1 — and the argument here
>   never depended on it: a licence decision is the user's whether or not anything else
>   works the same way.)
> - **The warning has to ride on the BUILD path** (`flake.nix:475-484`). It is emitted from
>   `noncontainerPackages`, whose stderr is streamed — not from the skip list alone, which a
>   separate eval reads with its stderr discarded. And reason precedence puts the **platform**
>   case first, because `meta.available` folds `unsupported` in with the licence checks, so
>   testing it first mislabels a plain platform miss (`iptables` on darwin) as "broken or
>   blocklisted" (`flake.nix:441-455`).
> - **A collection or a non-package is fatal, not skipped**, and that test sits deliberately
>   OUTSIDE the `tryEval` — which would otherwise swallow the throw and relabel a typo'd
>   `packages` entry as "no `<system>` build" (`flake.nix:456-464`).
>
> **Re-measured 2026-08-23** against the current lock, via
> `nix eval --impure '/workspace#yoloUnavailablePackages.<sys>'` with all six agent attrs in
> `YOLO_EXTRA_PACKAGES`: the skip list is exactly
> `["claude-code","github-copilot-cli","antigravity-cli"]` on `x86_64-linux`, `aarch64-darwin`
> **and** `x86_64-darwin`; with `NIXPKGS_ALLOW_UNFREE=1` it is `[]` on `aarch64-darwin`. The
> warn-and-skip and the opt-in escape hatch both still work.
- The brew-cask Brewfile bug (§8.4) is unaffected by any nix work. **FIXED (2026-08-02)** on
  its own: `install_hints` grew a `brew-cask` flavor key; see `internal/depcheck`.

---

## 3. Where a non-container nix env would *actually* be consumed

Before comparing mechanisms, be concrete about the consumer, because the answer differs per row
and this is where claim (b) breaks.

**Note the notch axis, which the old title obscured.** The first row is `guest`, not `host` —
and it is the row where the mechanism is *already proven*, because macos-user does exactly this
today. So `guest` is not a future consumer of a host feature; it is the CLOSEST thing to a
working example, and `host` is the harder case (row 3, where yolo never launches the process).
Reading the table in that order is the point: build for the notch that already works, then see
how far it reaches.

| Consumer | Does yolo control the process? | Can a PATH-prepend reach it? |
|---|---|---|
| `yolo --at guest -- claude` | **yes** (yolo execs it) | **yes** — this is exactly what macos-user does today |
| `yolo host -- claude` (aliased `yolo --at host -- claude`) | **yes** — the verb SHIPPED 2026-08-30 (postscript 4); it was hypothetical when this table was written | **yes** |
| `yolo host apply` then the user runs `claude` themselves, later, in their own shell | **no** | **only via a shell rc edit** |
| A pack's `program` install at the host notch (Phase 4.3) | **yes**, behind a confirm | n/a — this *installs*, it does not PATH-scope |

**Row 3 is the hard one, and it is the row the maintainer's "installing copilot" question sits
in.** `yolo host apply` configures and exits. Its own honored-but-unbuilt text for the `env`
kind states the constraint (re-read 2026-08-30, `internal/render/fieldset.go:99-103`): *"env vars
apply to a process yolo starts, and `yolo host apply` only configures your tools — it never runs
them. Setting them for your whole session would mean editing your shell rc, a much larger claim
than a pack's env contribution asks for. `yolo host -- <program>` delivers them at launch
instead, to that process only."* An environment that has to be *entered*, or a PATH that has to
be *prepended*, cannot serve a consumer yolo never launches — unless yolo writes the
user's shell rc, which is already refused by name. **Row 2 stopped being hypothetical on
2026-08-30**: `yolo host -- <cmd>` shipped, `yolo --at host -- <cmd>` is its systematic alias,
and the refusal's own text now points at it — so there IS a launch verb below `jail`, and row 3
is the only row left with no consumer yolo controls.

Two exits from that, and they are the real fork (§8, Options):

- **Do launch.** Make `yolo --at host -- <cmd>` real, and the PATH-prepend serves it for free.
  The design already wants this verb (§4.1: *"the second line is the case that started this"*).
  Note that this also unblocks the `launch` kind at the host notch, which is currently
  honored-but-unbuilt for exactly this reason. This is the *coherent* answer.
- **Do not launch; hand off a stable path.** Materialize the profile, symlink it to a fixed
  location, and *tell* the user (`export PATH=~/.local/state/yolo/host-env/bin:$PATH`, or
  `direnv`, or their own rc). yolo owns the closure; the user owns their PATH. Honest, and
  consistent with "the manifest is the floor, running it is an offer."

---

## 4. The mechanisms, compared honestly

Four candidates, not three: `nix shell` deserves its own row because it is routinely lumped in
with `nix develop` and behaves nothing like it.

| | reproducible? | PATH pollution | must be *entered*? | non-bash shells | mutates the user's machine |
|---|---|---|---|---|---|
| **devShell** (`nix develop` / `nix-shell` / `print-dev-env`) | yes (flake-pinned) | **severe — see §4.1** | **yes** (subshell) or source a 70 KB script | `print-dev-env` emits **bash** syntax; `nix develop` spawns bash | no (but see §4.3) |
| **`nix shell nixpkgs#x`** | yes | **none** (one dir prepended) | **yes** (subshell / `--command`) | wraps any command; no shell syntax involved | no |
| **`buildEnv` + PATH prepend** (shipped) | yes (flake-pinned) | **none** | **no** — the caller sets PATH for the process it launches | shell-agnostic (it is an env var, set by the launcher) | no |
| **`nix profile add`** | per-entry, drifting (§0) | user's whole profile | **no** — always on their PATH | shell-agnostic (their nix install did it) | **yes** — this is the point |

### 4.1 The flake's rejection of a devShell holds at the host notch, with more force

The comment at `flake.nix:934-941` is the load-bearing prior art:

> *"A devShell's `print-dev-env` would dump the whole stdenv toolchain (clang, GNU
> coreutils/sed/grep, make, …) onto the agent PATH ahead of the macOS BSD userland; a buildEnv
> contains only the declared packages."*

**Measured, not assumed.** Against this repo's own nearly-empty `devShells.default` (its
`buildInputs` is literally `[ pkgs.just ]`):

```
$ nix print-dev-env --impure --json | jq -r '.variables.PATH.value' | tr : '\n' | wc -l
22
$ … | jq -r '.variables | keys | length'
121
```

The 22 PATH entries for a one-package shell: `patchelf`, `gcc-wrapper`, `gcc`, `glibc-bin`,
`coreutils`, `binutils-wrapper`, `binutils`, **`just`**, then `stdenv.initialPath`'s
`coreutils findutils diffutils gnused gnugrep gawk gnutar gzip bzip2 gnumake bash patch xz file`.
One of those 22 is what was asked for. The 121 variables include `CC`, `CXX`, `AR`, `LD`,
`NIX_CFLAGS_COMPILE`, `NIX_HARDENING_ENABLE`, `SOURCE_DATE_EPOCH`, `IN_NIX_SHELL`, and — note
this one — **`TZ`** and **`SHELL`**.

**Why this is *worse* at the host notch than it was on macos-user.** On macos-user, yolo builds
the launch environment from scratch (`env -i`-style) and the pollution would land on a sandboxed
agent's PATH. At the `host` notch the pollution would land in **the human's own interactive
shell**, alongside their own dotfiles, for the rest of the session. Concretely, on macOS:

- `stdenv.initialPath` puts **GNU** `sed`, `grep`, `awk`, `tar`, `find`, `coreutils` **ahead of
  `/usr/bin`**. On darwin those are BSD. A user's own scripts, their `~/.zshrc` functions, and
  any `Makefile` they run in that shell silently change behavior — `sed -i` alone differs (BSD
  requires an argument to `-i`, GNU does not).
- `stdenv.cc` on `aarch64-darwin` is `clang-wrapper-21.1.8` (verified by eval from this Linux
  jail). Prepending a nix clang wrapper ahead of Xcode's `clang` breaks anything that expects
  Apple's SDK defaults.
- `CC`/`CXX`/`NIX_CFLAGS_COMPILE` exported into a human's shell will be picked up by unrelated
  builds in that terminal.

On Linux the userland collision is milder (GNU-on-GNU), but `CC`, `LD`, `NIX_LDFLAGS`, and a
`gcc-wrapper` ahead of the distro's are still real hazards for an interactive session.

**Conclusion on claim (b): `nix-shell`/`nix develop` is the wrong shape, and it is wrong for the
already-recorded reason plus a new one (the polluted shell is the human's own, not an agent's).**
The lesson generalizes rather than being macOS-specific.

**Two caveats, so this is not overstated.** (1) `nix develop` accepts
`--ignore-environment`/`-k`, and a hand-authored `mkShell` with `stdenv = pkgs.stdenvNoCC` and
`buildInputs` only would reduce the dump — but at that point you have hand-built a `buildEnv`
with extra steps and kept `print-dev-env`'s bash-only output. (2) A devShell *does* carry one
thing a `buildEnv` cannot: **environment variables and `shellHook`s** as part of the derivation.
If a future need is "the environment must also set `SSL_CERT_FILE` / `PKG_CONFIG_PATH` /
`FONTCONFIG_FILE`," a devShell is the nix-native way to express that. Today yolo carries those
as a small explicit whitelist in Go (`darwinpkg.ProfilePaths` exposes exactly
`PKG_CONFIG_PATH`), which is more legible and more auditable than a 121-variable dump. **If the
env-var list grows past a handful, revisit.** (OQ-4.)

### 4.2 `nix shell` is the interesting dark horse — and it dies on the `launch` refusal

`nix shell nixpkgs#a nixpkgs#b --command <cmd>` prepends exactly the requested store `bin`
dirs and nothing else (verified in §0). It needs no flake output at all, no `buildEnv`
definition, no `--print-out-paths` parsing. It is the *cheapest* correct mechanism for
"run this command with these tools available."

**But it is a launcher.** It only exists as a wrapper around a process yolo starts. That puts it
squarely on the wrong side of the constraint `render.HostUnimplemented` records for the `launch`
kind: *`yolo host apply` only configures your tools — it never runs them.* So `nix shell` is a
good fit for the `yolo host -- claude` / `yolo --at guest -- claude` rows of §3 — the first of
which now exists (postscript 4) — and **no fit at all** for the
`yolo host apply`-then-user-runs-it row.

It also loses the two things the buildEnv gives, and **both of them have since become load-bearing
rather than theoretical**: a **single stable path** — the out-path is one dir you can symlink,
report in `describe` (shipped: `internal/cli/describe.go:161-190`), and GC-root (shipped:
`internal/darwinpkg/gcroot.go`), where `nix shell` re-resolves per invocation — and **`flake.lock`
pinning** (`nixpkgs#x` resolves through the *registry*, i.e. the user's channel, not yolo's lock,
unless you pass the locked URL explicitly, at which point you are back to needing yolo's flake
anyway). N1's GC root in particular has no `nix shell` analogue at all.

### 4.3 `nix profile` is the one that actually answers the maintainer's copilot question

Worth stating plainly because the mechanism yolo would *like* (a scoped profile) does not solve
row 3 of §3, and this one does. `nix profile add` puts the binary on the user's PATH **forever,
in every shell, with no cooperation from yolo** — which is precisely what a user asking "how do
I install copilot" wants. It is what `depcheck.installCmd` already prints for the `nix` manager:
`nix profile install nixpkgs#github-copilot-cli`.

Its costs are real and should not be soft-pedaled: it **mutates the user's machine** (the same
category `install_hints` is in, so it belongs behind Phase 4.3's confirm, not in a silent
apply); its pin is per-entry and drifts from yolo's `flake.lock`; `nix profile upgrade` only
works for unlocked references; and for the three unfree packages a bare `nix profile install`
refuses (§8.3's defect).

**A `--profile <dir>` variant is a genuinely interesting middle ground** and I have not seen it
considered anywhere in the docs. `nix profile add --profile ~/.local/state/yolo/host-profile
nixpkgs#…` builds a **yolo-owned** profile the user's PATH does not see by default. Verified
working here into a temp dir. That is a `buildEnv`-like stable path *with* generations, rollback,
and `nix profile list` provenance — and it gcroots itself, which the current buildEnv path does
not. It costs the imperative/declarative purity that env-manager §3.3's sealing story rests on:
a profile is *mutable state*, and the closure table would gain a row. (OQ-3.)

### 4.4 Recommendation on mechanism

**Generalize the existing `buildEnv` for the environment yolo launches; use `nix profile` (behind
Phase 4.3's confirm) for the environment yolo does not.** They are not competing — they serve
the two different consumers of §3, and neither serves the other's.

Reject the devShell in all forms (§4.1). Keep `nix shell` in mind as a possible *simplification*
of the launcher path if the `buildEnv` output ever proves more machinery than it earns — but note
it forfeits `flake.lock` pinning and the stable path.

---

## 5. macOS vs Linux

### 5.1 Platform coverage of the six agent CLIs — verified, from this Linux jail

The nixpkgs eval *does* resolve `aarch64-darwin` attributes from Linux (evaluation is
platform-independent; only *building* needs the platform). So this table is measured, not
guessed — against `flake.lock` rev `241313f4`:

| pack (`bin`) | nixpkgs attr | version | `aarch64-darwin` | `aarch64-linux` | `x86_64-linux` | unfree |
|---|---|---|---|---|---|---|
| claude (`claude`) | `claude-code` | 2.1.220 | ✅ | ✅ | ✅ | **yes** |
| copilot (`copilot`) | `github-copilot-cli` | 1.0.61 | ✅ | ✅ | ✅ | **yes** |
| codex (`codex`) | `codex` | 0.146.0 | ✅ | ✅ | ✅ | no |
| opencode (`opencode`) | `opencode` | 1.18.9 | ✅ | ✅ | ✅ | no |
| pi (`pi`) | `pi-coding-agent` | 0.83.0 | ✅ | ✅ | ✅ | no |
| agy (`agy`) | `antigravity-cli` | 1.1.8 | ✅ | ✅ | ✅ | **yes** |

**6/6 on all three live platforms.** That is much better coverage than any other manager and it
is the strongest single fact in favor of the nix route.

Three caveats, all verified at the time — **the first is now retracted, see below**:

- **`x86_64-darwin` is dead.** Every one of the six (and `hello`) throws
  `error: Nixpkgs 26.11 has dropped support for x86_64-darwin.` on eval. So an Intel Mac gets
  **nothing** from this route — not "fewer packages," *nothing at all*, including from
  `nix build` of the existing `yoloDarwinPackages`. Since `macos-user` is arm64-only anyway
  this is not a regression, but a host-notch feature aimed at "macOS users" must say so.
- **Freshness is close but not equal.** nixpkgs `codex` 0.146.0 and `pi-coding-agent` 0.83.0
  match npm exactly today; `claude-code` 2.1.220 matches the version running in this jail;
  `opencode` 1.18.9 vs npm 1.18.11 and `github-copilot-cli` 1.0.61 vs npm 1.0.77 lag. So a nix
  route means "pinned, and a few days-to-weeks behind" — which for an agent CLI that ships
  daily is a **real** tradeoff, not a footnote, and it interacts with the packs' auto-updater-off
  managed keys (`claude`'s `preferences.autoUpdaterStatus: "disabled"`, copilot's
  `--no-auto-update`). Pin *and* disable updates and the user is on nixpkgs' cadence.
- **3 of 6 are `unfree`** (§2). This is the sharpest practical blocker, and it hits `packages:`
  today (OQ-6 — since **fixed**: they are now named and skipped rather than aborting the build,
  and an opted-in user still gets them; re-measured 2026-08-23, §2).

### ⚠ Retracted: "`x86_64-darwin` is dead, an Intel Mac gets nothing" (2026-08-23)

The **observation** was right and mattered more than this doc knew; the **consequence** is now
wrong, because the flake was fixed on 2026-08-18 (`927fb9f5`).

What actually happened: nixpkgs 26.11's throw did not merely deny an Intel Mac its packages — it
took out **every host-side nix call on that system**, `nix eval .#installPrefix` included, which
is what the integration suite's staleness oracle runs. And the only hosted runner exercising
yolo's macOS code paths is `macos-26-intel` (GitHub's Apple Silicon runners cannot nest a VM for
Podman Machine). The macOS nightly went red for **29 consecutive nights**, last green
2026-07-21 — the morning of the `flake.lock` bump that crossed 26.11 — and the roadmap recorded
it as "nix is broken on that runner, not in our tree," the opposite of true
(`flake.nix:22-35`).

The fix is a **second nixpkgs input used for `x86_64-darwin` alone**:
`nixpkgs-x86-darwin.url = "github:nixos/nixpkgs/nixpkgs-26.05-darwin"`, selected by
`hostNixpkgs = if system == "x86_64-darwin" then nixpkgs-x86-darwin else nixpkgs`
(`flake.nix:42`, `:50`). Deliberately **not** used for `aarch64-darwin` — real Mac users stay on
26.11.

**Re-measured today** against the current lock:

```console
$ nix eval --impure --raw '/workspace#packages.x86_64-darwin.yoloNoncontainerPackages.name'
evaluation warning: Nixpkgs 26.05 will be the last release to support x86_64-darwin
yolo-noncontainer-packages                  # evaluates; no throw

$ NIXPKGS_ALLOW_UNFREE=1 YOLO_EXTRA_PACKAGES='[…the six agent attrs…]' \
    nix eval --impure --json '/workspace#yoloUnavailablePackages.x86_64-darwin'
["antigravity-cli"]                         # 5 of 6 resolve, not 0 of 6
```

So the corrected statement is: **an Intel Mac gets 5 of the 6 agent CLIs, off a frozen 26.05
line.** `antigravity-cli` is the one the mechanism skips there — where on `aarch64-darwin` with
the same opt-in the skip list is empty. *Which* reason it skips for (absent from 26.05, or
refused on that platform) this measurement does not distinguish; `yoloUnavailablePackages`
carries names, and the reason is only printed on the build path (§2). The *deadline* framing in the
roadmap survives intact and is the part to keep: 26.05 is the last release supporting
`x86_64-darwin` and is security-fixed only to the end of 2026, after which the choice is a
self-hosted arm64 Mac runner or macOS tests on `macos-user` only.

> [!WARNING]
> **Do not "simplify" the flake back to one nixpkgs input.** `pkgs` is evaluated for every
> system `flake-utils` enumerates, so a throw on any one system is a throw on every attribute —
> which is why a single dead platform could take CI down for a month while looking like an
> infrastructure problem. Any future platform drop wants the same shape: a per-system input
> override, not a `packages`-level filter.

### 5.2 What the *jail's own* package set looks like on darwin

Spot-checked `corePackages` members by eval:

| package | `aarch64-darwin` |
|---|---|
| `coreutils-full`, `gnugrep`, `gnused`, `gawk`, `findutils`, `procps`, `socat`, `sox` | ✅ |
| `iptables` | ❌ (Linux netfilter) |
| `glibc`, `nix-ld` | ❌ (structurally Linux) |

So "mimic the in-jail env" at the tool level is *mostly* achievable on darwin — with the
important twist that **you probably do not want the GNU userland ones on a Mac host**, which is
the §4.1 BSD hazard arriving through the front door instead of the devShell's back door. A
`buildEnv` containing `gnugrep` still shadows `/usr/bin/grep` when its `bin` is prepended. The
difference from a devShell is only that here it is a **declared** choice the user made in
`packages:`, not an incidental dump. That is a big difference in *legibility* and none at all in
*effect* — worth saying out loud, because it means the buildEnv's "no pollution" property is
"no *undeclared* pollution," which is the honest version of the claim. (OQ-5.)

### 5.3 The `/lib` + `LD_LIBRARY_PATH` + nix-ld mechanism has no darwin analogue worth building

This is the clearest "environment vs isolation" boundary case, so it gets its own subsection.

Inside the jail, non-nix binaries (mise-installed node, pip/npm native modules) find shared
libraries through a three-part Linux-only contraption: the `binPathLinks` **`/lib` symlink
farm**, the baked `LD_LIBRARY_PATH=/lib:/usr/lib:/usr/lib/<multilib>` in the image `Env`, and
**nix-ld** as the FHS interpreter at `/lib64` (`docs/plans/nix-ld-dynamic-linking.md`,
`docs/design/mise-node-dynamic-linking.md`).

The darwin analogue would be `DYLD_LIBRARY_PATH` / `DYLD_FALLBACK_LIBRARY_PATH`, and it does not
work, for reasons that are not yolo's to fix:

- **System Integrity Protection strips `DYLD_*` from the environment** of any protected
  binary and across `exec` of platform binaries. There is no `dyld` equivalent of nix-ld.
- macOS has no `/lib64` FHS interpreter to replace: Mach-O binaries carry absolute
  `LC_LOAD_DYLIB` paths, and nix's darwin binaries already reference store paths directly.
- **There is nothing in the repo that tries.** `rg DYLD` over the whole tree finds only two
  hits, both in vendored `golang.org/x/sys` constants. That silence is itself evidence: the
  problem the Linux farm solves — *FHS-expecting foreign binaries on a nix system* — barely
  exists on macOS, where foreign binaries expect `/usr/lib` and macOS **has** `/usr/lib`.

**So this whole mechanism is jail-and-Linux-specific and should be explicitly out of scope for
any host-notch nix work.** Naming it as out of scope is more useful than leaving it as an
unexamined "and the rest."

### 5.4 What if the user has no `/nix`?

Three sub-cases, and only one of them is interesting:

1. **No nix at all** (the common macOS/Linux user). A host nix env is unavailable. `yolo check`
   already `fail`s on `nix not found` — but note that on macOS it *additionally* runs
   `nix store info` and the `/nix`-exists probe, while on **Linux those probes are `IsMacOS`-gated
   and never run**. If a host nix env becomes a Linux feature, that gate needs revisiting.
   Telling a brew user to install nix to get `copilot` is worse than `brew install copilot-cli`,
   so `install_hints` stays the floor (§2).
2. **nix present, user not trusted.** Already handled as a warning, not a failure, for
   macos-user: a non-trusted user can still substitute from `cache.nixos.org`; being trusted is
   what makes `--accept-flake-config` consult yolo's cachix. Same story would hold here.
3. **nix present, but the closure must be *built* rather than substituted.** This is the one
   that bites. All six agent CLIs are prebuilt in `cache.nixos.org` for the three live systems,
   so in practice it is a download — but the failure mode (a from-source darwin build with
   `--print-build-logs` streaming for minutes) is already documented for macos-user and would be
   identical here. `nixdiag.ParseDryRunWillBuild` already exists to classify this
   (build/substitutable/**inconclusive**, where inconclusive must never be read as a miss).

**One shipped gap that a host feature makes worse — CLOSED 2026-08-05 (OQ-2, shipped as N1,
`23cee7a6`).** As written, `darwinpkg` built with `--no-link` and registered **no GC root**,
unlike `internal/image/gcroot.go` which carefully roots the OCI image; a user's next
`nix-collect-garbage` deleted the realized profile out from under a launch that had just
resolved it — or out from under a long-running session still executing binaries from it. On a
notch with no baked image that closure *is* the agent's toolset; there is no fallback. It is now
rooted at `<home>/.local/share/yolo-jail/build/package-roots/packages`
(`internal/darwinpkg/gcroot.go:52-60`), and `yolo check`'s macos-user section reports a dangling
root as its own FAIL (`internal/cli/check/sections_macos.go:118-134`).

> [!WARNING]
> **Four things about that GC root are deliberate. Changing any of them re-opens the defect.**
>
> - **The root IS the build's `--out-link`**, not a follow-up `nix-store --add-root` like
>   `image.RegisterImageRoot` uses. The two-step leaves a window in which a concurrent GC can
>   collect a just-built closure; nix creating the root as part of the build it is already
>   running has no such window. It also makes rooting non-optional — failing to create the root
>   fails the build, which is the right polarity when the alternative is an agent executing from
>   an unrooted closure (`internal/darwinpkg/darwinpkg.go:117-141`).
> - **The leaf name is FIXED (`packages`), not keyed by `sha256(storePath)`.** `--out-link`
>   *replaces* the link in place, so a changed `packages:` retargets the one root and the old
>   closure becomes collectable — verified empirically, two package lists, still exactly one
>   entry. A content-keyed leaf would accumulate one permanent root per package set ever
>   configured, with no reaper: right for images (several are legitimately live at once, and
>   prune has a liveness protocol), a slow disk leak here (`internal/darwinpkg/gcroot.go:34-50`).
> - **It lives in `build/package-roots/`, a SIBLING of `build/roots/`, on purpose.**
>   `prune.PruneOrphanImageRoots` enumerates every symlink under `build/roots` and reaps the ones
>   no recently-loaded image needs — a package root parked there would be swept by a routine
>   `yolo prune --apply`, unrooting the very closure it exists to pin
>   (`internal/paths/paths.go:362-372`).
> - **Registering it from inside a jail does not work, and that is fine.** Verified 2026-08-05:
>   `nix build --out-link` does register the indirect root, and the host daemon then prunes it as
>   stale, because the link's path is the jail's spelling of a directory the host mounts
>   elsewhere — the same caveat `image.RegisterImageRoot` documents. Harmless today because every
>   caller is a non-container notch provisioning a real host home. Worth knowing before someone
>   reuses this from in-jail code and wonders why the root evaporates.

---

## 6. "Mimic our in-jail envs more" — the isolation/environment split

The maintainer's third instinct is the one that holds up best, and making it precise is this
doc's main contribution. Everything the jail gives its agent, sorted by whether a nix closure
plus a launch env could supply it off-container:

| What the jail provides | Mechanism | Class | Off-container? |
|---|---|---|---|
| `corePackages` / `fullPackages` (the ~60-package baked set) | OCI image layers | **environment** | ✅ a `buildEnv` of the same attrs (minus Linux-only ones, §5.2) |
| `packages:` (user's extras) | `extraPackages` → image | **environment** | ✅ **already shipped** as `yoloNoncontainerPackages`, GC-rooted since 2026-08-05 |
| `mise_tools` | mise, PATH-ordered shims | **environment** | ✅ already runs natively on macos-user (`ConfigureMisePrism`) |
| Env hygiene (`PAGER`/`GIT_PAGER=cat`, `BAT_PAGER=""`, `EDITOR=cat`, `VISUAL=nvim`, `OVERMIND_SOCKET`) | `-e` flags + generated `.bashrc` | **environment** | ⚠️ **only for a process yolo launches.** In a shell yolo does not start, this is a shell-rc edit — refused by name today (`KindEnv`). And `EDITOR=cat` in a *human's* shell is hostile: it exists because an agent cannot drive an editor |
| `PATH` order (`.yolo-shims:.local/bin:$NPM_CONFIG_PREFIX/bin:<mise>:$GOPATH/bin:/bin:/usr/bin:.yolo-launchers`) | generated `.bashrc` / launch env | **environment** | ⚠️ same: yolo-launched yes, user's shell no. macos-user already needs a **login-rc re-prepend** (`.zprofile`/`.zshrc`/`.bash_profile`) to survive macOS `path_helper` — evidence of how far you must reach to own a PATH you did not start |
| Blocked-tool shims (`grep -r`, `find`) | generated scripts, first on PATH | **hybrid — see below** | ⚠️ mechanically yes; the design flags it `!` ("shims would land on your real PATH — opt in explicitly") |
| `/lib` farm + `LD_LIBRARY_PATH` + nix-ld | image + baked `Env` | **environment, but Linux-container-only** | ❌ no darwin analogue (§5.3); on a Linux host it would be actively wrong to set `LD_LIBRARY_PATH=/lib:/usr/lib` |
| Composed agent config (settings, MCP, LSP, skills, briefing) | the prism / `render` | **environment** | ✅ **already shipped** — `yolo host apply` |
| Disposable home / overlay | bind mounts | **isolation** | ❌ |
| Credential omission (no `~/.ssh`, no `~/.gitconfig`) | absence of a mount | **isolation** | ❌ — and inverted at `host`: your creds are *the point* |
| `resources` (cpu/mem/pids), `yolo-cglimit` | cgroups | **isolation** | ❌ |
| `network` modes, `ports` | netns | **isolation** | ❌ |
| Loopholes (audio, host-processes, oauth-broker) | socket mounts + `--add-host` | **isolation-boundary plumbing** | mostly **moot** off-container (a native process reaches the resource directly) |
| `devices` / `gpu` | cgroup device rules | **isolation** | ❌ |
| Agent autonomy (`--dangerously-skip-permissions` etc.) | pack `autonomy` kind | **policy, decided by confinement** | ✅ already correct — `host` renders the *guarded* posture (Phase 9) |

**The line, in one sentence: a nix closure plus a launch env can supply everything in the
"environment" class for a process yolo starts, and nothing in the "isolation" class ever.**

**The blocked-tool shims are the one genuine hybrid, and they are worth dwelling on** because
they look like environment and behave like policy. `grep -r` is blocked because a recursive grep
wastes an agent's context, not because it is dangerous — that is an *environment* property, and
`GenerateShims` is already a pure generator that runs natively on macos-user. But at the `host`
notch the shims would land on **the human's** PATH, and a human typing `grep -r` and being told
to use `rg` is a different product than an agent being nudged. The env-manager design already
marks this `!` / opt-in rather than ✅, and that is right. **If yolo launches the host agent
(§3 row 2), the shims can be scoped to that process and the dilemma dissolves** — another point
for the launcher answer over the rc-editing answer.

**The most valuable "mimic" target is not any of the above.** It is the fact that **all six agent
CLIs are in nixpkgs for all three live platforms** (§5.1) while the jail installs them
**lazily, at first use, via npm and curl-to-shell** (`~/.yolo-launchers/` launchers; every shipped
pack's `program` is `via: npm` or `via: installer` — re-counted 2026-08-23: four npm
(`opencode`, `pi`, `copilot`, `codex`), two installer (`claude`, `agy`), zero nix). So the *jail* does not get its agent CLIs
from nix either. A host nix env would be **more reproducible than the jail** on exactly the axis
the maintainer's copilot question is about. That is a genuinely interesting inversion, and it
raises a question this doc cannot answer alone: *should the jail's agent CLIs move to nix too?*
(OQ-7.) Arguments both ways: nix gives one reproducible mechanism and drops the curl-to-shell
(also a supply-chain win); npm/installer gives same-day upstream versions, which for
fast-moving agent CLIs may matter more than the pin. The unfree three complicate it.

---

## 7. Is it orthogonal to confinement? No — and this is the load-bearing finding

The maintainer's framing is that a nix env supplies *tools* while confinement supplies
*isolation*, so the two vary independently. That is true as a **statement about the two
concepts** and false as a **statement about the work**, for three reasons that compound.

**1. `guest` needs the identical mechanism, and it is unbuilt.** Phase 7 is *"a real home on the
real filesystem, no image, an LSM boundary"* — macOS Seatbelt, Linux bwrap+Landlock. **No image
means no baked package closure.** So `confinement: guest` has, by construction, exactly the same
"where do the tools come from" hole as `host`, and the answer at macOS `guest` is already
written: `render.GuestProfileMacOS()` = separate user + Seatbelt, and macos-user's *existing*
`buildEnv` materialization is its package layer. **Linux `guest` (7.2) has no package layer at
all yet.** A host-notch nix env designed in isolation would be Phase 7.2's package layer, built
under a different name. That is not orthogonality; that is a shared dependency.

**2. The notch decides whether a PATH-prepend has a consumer.** At `jail` and `guest`, yolo
launches the process, so a prepend works. At `host`, `yolo host apply` launches nothing — hence
the `launch` and `env` kinds' refusals — though its sibling verb `yolo host -- <cmd>` does launch
(postscript 4). **The mechanism's *viability* is a function of the notch**, which is the
definition of not-orthogonal. §3 is this argument in table form.

**3. The primitive model already says so.** `internal/render/confinement.go:39-42` lists
**`PrimBakedImage`** as a Primitive, with the comment: *"A provisioning primitive, not a
confinement one, but it travels with the jail notch and is absent below it."* The code has
already recorded that provisioning is entangled with the notch and put the entanglement in the
same struct. A host nix env is precisely the *replacement* for `PrimBakedImage` below `jail` —
which makes it a **candidate seventh Primitive** (`PrimNixProfile`?), not a feature living
beside the dial.

**Since 2026-08-05 the code acts on this, without minting the primitive.** `describe` prints the
resolved profile line **iff `PrimBakedImage` is absent** from the notch's vector
(`internal/cli/describe.go:172-177`): "where do my tools come from" gets a nix-profile answer
only below the jail notch, and printing one for a jail would name a closure the launch does not
use. So the *absence* of `PrimBakedImage` is already the live switch for the whole mechanism —
which is the argument for the seventh primitive, made in the negative. Whether to spell it
positively as `PrimNixProfile` is still open, and it is downstream of OQ-1.

**Where the maintainer's instinct *is* right, and it is not a small consolation.** The nix
env is orthogonal to the *enforcement* primitives — namespaces, Seatbelt, Landlock,
separate-user. You can compose "Landlock + nix profile," "Seatbelt + nix profile," or "nothing +
nix profile" freely. So the correct statement is:

> A nix tool environment is orthogonal to the **enforcement** primitives and **load-bearing for
> the provisioning** primitive. It is not a peer of the dial; it is what fills the
> `PrimBakedImage`-shaped hole at the two notches that have no image.

**The practical consequence, which is the reason to care:** designing this as "a host feature"
risks a Linux `guest` package layer being built twice. Designing it as "the sub-jail
provisioning primitive" gets `host` and both `guest` variants from one mechanism — and one of
the three is already shipped, which is the strongest possible evidence the abstraction is right.

---

## 8. Options, with a recommendation

Ordered from least to most work. These are alternatives to *choose among*, not phases.

### Option 0 — Do nothing; fix the two `install_hints` defects instead  *(DONE 2026-08-02)*

Fix the brew-cask Brewfile bug (§8.4) and the unfree-hint insufficiency (§8.3), and leave
provisioning at the host notch as "print the remedy, the user runs it."

**Both shipped** (`e40df9f1`): `install_hints` grew a `brew-cask` installer-flavor key, which
wins over `brew` when a pack declares both (`internal/depcheck/depcheck.go:53-54`,
`internal/packdecl/contributes.go:45-51`), and the unfree skip landed in the flake (§2). The
rest of this option — *leave provisioning at the host notch as "print the remedy"* — is the
status quo, and remains the answer if OQ-1 comes back "configure-only."

**For:** zero new mechanism; both fixes are needed regardless; the design's "manifest is the
floor" rule is satisfied. **Against:** leaves `install_hints` covering **0–1 of six** agent CLIs
on a non-Arch Linux host (§2), which is the concrete weakness that started this. Does not touch
the pre-existing `packages: ["claude-code"]` abort (OQ-6 — since fixed, and it was a **build**
abort, not an eval one), which was a real bug either way.

### Option 1 — Rename and generalize the shipped mechanism; add no new consumer  *(MOSTLY SHIPPED 2026-08-05)*

Rename `yoloDarwinPackages` → something system-neutral (`yoloHostPackages`), stop hardcoding
`aarch64-darwin` in `darwinpkg`, add a gcroot, and make `describe` / `check --at host` **report**
the resolved profile path. `yolo host apply` gains one line: *"packages: <n> resolved at
/nix/store/… — add its bin to your PATH, or use `yolo --at host --` (unbuilt)."*

**For:** small; honest; fixes the GC-root gap; removes a platform-specific name from a
platform-neutral output; and it is a **prerequisite for Phase 7.2** regardless of what happens
at `host`. Does not touch the user's machine or their PATH. **Against:** solves row 3 of §3 only
by handing the user a path and asking them to wire it up. That is a real product gap, though it
is exactly consistent with the "manifest is the floor" posture.

**Shipped as N1 + N2 (`23cee7a6`, `11f8bb72`), with two deliberate divergences and two
leftovers** — status re-checked 2026-08-23:

| Sub-item | State |
|---|---|
| System-neutral name | **Shipped**, but as `yoloNoncontainerPackages`, **not** the `yoloHostPackages` this doc proposed — the axis is "no baked image," and `host` is only one of the notches that has it (§7) |
| Stop hardcoding `aarch64-darwin` | **Shipped** — `darwinpkg.NativeSystem()` from GOOS/GOARCH; an unrecognized GOARCH passes through verbatim so nix rejects it loudly rather than resolving the wrong machine's package set |
| GC root | **Shipped** — see §5.4 |
| `describe` reports the profile | **Shipped** — `internal/cli/describe.go:161-190` |
| `check` reports the profile | **Shipped for macos-user only** — the report lives inside the macOS backend section (OQ-9) |
| `check --at host` reports it | **Not shipped** — `check` has no `--at` at all; its only flags are `--build`/`--no-build` (`internal/cli/commands.go:653-680`) |
| The `yolo host apply` line | **Not shipped** — `yolo host apply` still says nothing about `packages:` (OQ-8) |
| Rename the **Go package** `darwinpkg` | **Not done, on purpose** — mechanical, left for the consumer that needs it (`internal/darwinpkg/darwinpkg.go:8-14`) |

**And the finding that this option was never able to deliver on its own is now measured:** the
mechanism is generalized but still has exactly **one caller**, the macos-user orchestrator. A
rename does not give `host` or Linux `guest` a consumer; only Options 2/3 (or Phase 7.2) do.

### Option 2 — Option 1 plus `yolo --at host -- <cmd>` (the launcher)

Make the design's own §4.1 escape valve real. yolo builds the launch env: profile `bin`
prepended, the pack's *guarded* autonomy posture (Phase 9, already correct), optionally the
scoped shims, and execs the agent. The user's own shell is untouched.

**For:** this is the shape everything else in the codebase is already built for — the same
`render.Target`/`Profile` machinery, the same `buildEnv` realization macos-user does, and it
unblocks `launch` and `env` at the host notch for a *scoped* process instead of by rc-editing.
It makes the shim dilemma (§6) disappear. **Against:** a new verb (`--at` is `apply`-only
today); "yolo launches your host agent" is a bigger product claim than "yolo configures it"; and
the env-manager design's own §8 warns `host` will be over-used, which a convenient launcher
accelerates. Note this option does **not** answer "how do I install copilot" for a user who
wants `copilot` in their own terminal.

### Option 3 — Option 1 plus a yolo-owned `nix profile` (the installer)

Add `nix profile add --profile <yolo-owned-dir>` as a Phase 4.3 remedy class, so the confirm-gated
install has a **reproducible** option beside `brew install`. Optionally offer the user's *default*
profile (fully machine-global) as a separate, louder confirm.

**For:** this is the only option that answers the literal copilot question — the binary ends up
on the user's PATH permanently, and `nix` is the only manager covering all six agents. Generations
and rollback come free, as does a GC root. **Against:** it *mutates the user's machine*, so it
belongs behind Phase 4.3's confirm and inherits all of that increment's unresolved UX; a mutable
profile adds a row to the §3.3 closure table and complicates `--sealed`; the unfree three need
`allowUnfree` handling; and the per-entry registry pin drifts from `flake.lock`, weakening the
"yolo's exact closure" pitch that motivates the whole idea.

### Recommendation

**Option 1 now, on Phase 7's account rather than on the host notch's** — because it is small, it
is a strict improvement to already-shipped code (gcroot, name, non-macOS `check`), and Phase 7.2
needs it whatever is decided about `host`. Then **Option 2 if the maintainer wants the host
notch to be a place agents *run*, or Option 3 if they want it to be a place tools get
*installed*.** Those two are genuinely different products and the doc cannot pick for them —
which is OQ-1.

**And fix Option 0's two defects regardless.** They are independent of everything above.

> [!NOTE]
> **Status of the recommendation, 2026-08-23.** Option 0 is done; Option 1 is done except for
> the two reporting leftovers and the Go-package rename. **The fork is now the whole of what is
> left**, and it is untouched: Option 2 and Option 3 are both unbuilt, and choosing between them
> is OQ-1. The roadmap's "no longer urgent — the auth thread routed around the `env` refusal
> that motivated it" is accurate about urgency and says nothing about the question, which is
> still the one everything else here is subordinate to.

**What would talk me out of all of it:** if the answer to OQ-1 is "the host notch is
configure-only, forever" *and* the maintainer is content with `install_hints` coverage, then
Option 1 shrinks to a Phase 7.2 prerequisite with no host-notch story at all, and this doc's
conclusion is *"already solved for macos-user; the real gap is Linux `guest`, which is Phase
7.2's problem."* I think that is a defensible outcome and would not be a wasted read.

---

## 9. Open Questions

Seven live, ordered by how much else they block. Two more are settled — see the Decision Ledger
below. **IDs are cited outside this doc:** `OQ-1` is spelled **`N3/OQ-1`** in
[`boundary-broker.md`](boundary-broker.md) §10 and [`agent-auth-modes.md`](agent-auth-modes.md),
where it is named as a blocker; the roadmap calls it "nix OQ-1". Both spellings mean this
question and neither may be renumbered.

> [!NOTE]
> The ✅/❌/⚠️ marks in §5 and §6 are **platform-availability and portability** marks inside
> tables — they are not answered-question markers, and there is no ✅-flavored OQ anywhere in
> this doc. Live questions are the 💬 items below; settled ones are ledger rows.

1. 💬 **OQ-1 (also cited as N3) — Is the `host` notch a place where agents *run*, or only a
   place where they are *configured*?** Today it is configure-only: `--at` is `apply`-only
   (`internal/cli/apply.go:54-64`) and `launch`/`env` are honored-but-unbuilt for exactly that
   reason (`internal/render/fieldset.go:83-103`). Option 2 says "run"; Option 3 says "install";
   Option 1 says "neither, just report."

   **What it decides:** everything left in this doc — §3's consumer table has a different winner
   per answer, and Options 2 and 3 are different products. It also gates the boundary broker's
   approval tier (`boundary-broker.md` §10: *"a boundary approval service is much more
   compelling in a world where yolo launches processes at multiple notches, and its shape
   differs"*), and it is the difference between the blocked-tool shims being an agent nudge and
   a change to a human's own terminal (§6).

   _Leaning:_ **"a place agents run"** — Option 2. It is the shape the codebase is already built
   for, it dissolves the shim dilemma instead of arguing about it, and `render.HostUnimplemented`
   now says in its own comment that the refusal is a limit of `yolo host apply` rather than of the
   notch. But this is a product call, not a research finding, and the counter-argument is real:
   the env-manager design's own §8 warns `host` will be over-used, and a convenient launcher
   accelerates that.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-3 — Is `nix profile --profile <yolo-dir>` (§4.3) attractive or a trap?** It gives a
   stable path *plus* generations, rollback, and a self-managing GC root — but it is mutable
   state, which adds a row to the env-manager §3.3 closure table and interacts with `--sealed`.

   **What it decides:** whether Option 3 is buildable as specified, and how much of the
   reproducibility/sealing story has to cover host provisioning. Note N1 has since taken one
   argument off the table: the declarative `buildEnv` now GC-roots itself, so "it gcroots itself"
   is no longer a `nix profile` advantage.

   _Leaning:_ I genuinely do not know, and would not want to guess in code. If sealing is meant
   to be a hard guarantee, a mutable profile is a trap; if it is a best-effort description, it is
   the cheapest way to get generations and rollback.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 **OQ-4 — Does the environment need to carry *variables*, not just PATH?** A `buildEnv`
   cannot; a devShell can, and that is the *only* real argument for one (§4.1). Re-verified
   2026-08-23: the Go whitelist is still exactly one variable, `PKG_CONFIG_PATH`, and only when
   `<out>/lib/pkgconfig` exists (`internal/darwinpkg/darwinpkg.go:169-192`). The jail's baked
   `Env` carries `SSL_CERT_FILE`, `LD_LIBRARY_PATH`, `PKG_CONFIG_PATH`, `FONTCONFIG_*`, `TZDIR`.

   **What it decides:** whether the devShell rejection gets re-opened on this one axis (never on
   PATH — §4.1 is not in question). It also bounds how far "mimic the in-jail env" can go for a
   non-container notch.

   _Leaning:_ keep the Go whitelist. One variable is not a case for 121, and an explicit list in
   Go is more auditable than a derivation's dump. Revisit only if the enumeration comes back
   with more than about three.

   **Answer:**
   > _(empty — fill in when decided)_

4. 💬 **OQ-5 — Is "no PATH pollution" the right claim for a `buildEnv`, or should it be "no
   *undeclared* pollution"?** A `buildEnv` containing `gnugrep` still shadows `/usr/bin/grep`
   when prepended (§5.2) — the difference from a devShell is legibility, not effect. On a Mac
   host that is the BSD-vs-GNU hazard arriving by the front door instead of the back.

   **What it decides:** whether a non-container profile should *warn* when a declared package
   shadows a system binary, or trust the declaration. Verified 2026-08-23: nothing warns today,
   on any path.

   _Leaning:_ restate the claim honestly ("no undeclared pollution") and do not build a warner
   yet — a shadow is what `packages: ["gnugrep"]` means. Revisit if the `host` notch ever puts
   this on a human's interactive PATH, where the declaration was made once and the surprise
   arrives months later.

   **Answer:**
   > _(empty — fill in when decided)_

5. 💬 **OQ-8 — Should the `packages:` key report at all below `jail`, and which command says
   so?** `packages` is not a pack kind, so the `FieldSet` census never sees it and `yolo host apply`
   prints nothing about it, while `macos-user` honors it natively. The env-manager design
   promises `check --at host` will print *"packages: yolo does not manage packages here."*

   **What it decides:** whether "silently absent" — the exact failure mode
   `render.HostUnimplemented` exists to prevent — is allowed to persist for the one config key
   that has a real off-container implementation.

   _Leaning:_ **the question narrowed since it was written, and the narrow half should just be
   fixed.** `describe` now reports the resolved profile whenever `PrimBakedImage` is absent
   (`internal/cli/describe.go:172-177`), which contradicts the env-manager's promised `✗
   packages` line — so two yolo commands now disagree. Make `yolo host apply` say what `describe`
   says, and retire the `✗ packages` sentence from the env-manager design. That is worth doing
   even under Option 0. The *policy* half ("should `host` manage packages at all") stays with
   OQ-1.

   **Answer:**
   > _(empty — fill in when decided)_

6. 💬 **OQ-9 — Do non-macOS `yolo check` runs need the nix probes and the profile report?**
   Re-verified 2026-08-23: `nixDaemonStoreCheck` and the extra-platforms/builder block are still
   `IsMacOS`-gated (`internal/cli/check/section_nix_probe.go:28-31`), and so is the whole
   platform section (`check.go:77-78`). The profile report added by N2 is **also** macOS-only —
   it lives inside `checkMacosUserBackend` (`sections_macos.go:100`), which returns early both
   in a jail and off macOS (`sections_macos.go:46-60`).

   **What it decides:** whether a Linux user of a non-container notch gets any diagnosis at all
   when their daemon is broken or their profile root is dangling. Sharper now than when written:
   N2 made the *mechanism* per-system, so the platform gate on its *diagnostics* is no longer
   symmetric with the thing it diagnoses.

   _Leaning:_ split the profile report out of the macos-user section and run it wherever
   `PrimBakedImage` is absent — same predicate `describe` already uses. The daemon probes are a
   larger question and can wait for OQ-1.

   **Answer:**
   > _(empty — fill in when decided)_

7. 💬 **OQ-7 — Should the *jail* get its agent CLIs from nix too?** All six are in nixpkgs for
   all three live platforms (§5.1); the jail installs them lazily via `npm -g` and
   curl-to-shell — re-counted 2026-08-23, four `via: npm` and two `via: installer`, zero nix.

   **What it decides:** nothing else in this doc — it is listed last for that reason. But it is
   the largest change surfaced here (it would touch every pack), and it is the one that would
   make the jail as reproducible as the non-container notches, which is a genuinely odd
   inversion to leave standing.

   _Leaning:_ **no, not now.** Same-day upstream versions matter more for a CLI that ships daily
   than the pin does, two of six already lag in nixpkgs, and three are unfree — which would put
   the §2 warn-and-skip on the jail's critical path. Flagged because the research surfaced it,
   not because I recommend it.

   **Answer:**
   > _(empty — fill in when decided)_

---

## Decision Ledger

Settled here; the ruling itself lives in the body section named in the last column, and the
traps that made each ruling safe are preserved there as `> [!WARNING]` blocks.

| ID | Ruling / Decision | Date | Settled in |
| :--- | :--- | :--- | :--- |
| OQ-2 | **Yes, GC-root it** — and the root IS the build's `--out-link`, at `build/package-roots/packages`, a sibling of the image roots so `prune` cannot sweep it. Shipped as **N1** (`23cee7a6`) | 2026-08-05 | §5.4 (+ its warning block) |
| OQ-6 | **Warn-and-skip, via `meta.available`** — an unfree attr in `packages:` is skipped with a named reason instead of aborting the build; yolo never sets `allowUnfree` for the user, and an opted-in user still gets the package. Shipped `e40df9f1` | 2026-08-02 | §2 (+ its warning block) |
| — | Option 0's two `install_hints` defects (brew-cask Brewfile verb; unfree hint) — **both fixed** | 2026-08-02 | §8 Option 0 |
| N2 | **The mechanism is per-system and its name says so**: `yoloNoncontainerPackages` / `yoloUnavailablePackages` / `NativeSystem()`. Rejected this doc's `yoloHostPackages` — the axis is "no baked image," not "macOS," and not "`host`" either. Shipped `11f8bb72` | 2026-08-05 | §7, §8 Option 1 |
| N1 | The roadmap's ID for OQ-2's fix. Same ruling, same commit — recorded separately because `internal/darwinpkg` cites it by this spelling | 2026-08-05 | §5.4 |

---

## 10. Facts verified for this doc

Recorded so a later reader can tell measurement from inference. All run from inside a Linux
(`x86_64-linux`) yolo jail against `flake.lock` rev `241313f4`, 2026-08-02.

| Claim | How |
|---|---|
| `yoloDarwinPackages` builds on `x86_64-linux` | `nix build --impure --print-out-paths '/workspace#packages.x86_64-linux.yoloDarwinPackages'` → a profile whose `bin` holds exactly `fzf`, `hello` (+ fzf's own scripts); `hello` ran from it |
| The darwin buildEnv **evaluates** from Linux | `nix eval --impure --raw '.#packages.aarch64-darwin.yoloDarwinPackages.drvPath'` → a `.drv` path |
| devShell PATH/env pollution: 22 entries, 121 vars | `nix print-dev-env --impure --json \| jq` on this repo's own one-package devShell |
| `aarch64-darwin` `stdenv.cc` is `clang-wrapper-21.1.8`; `initialPath` is the GNU set | `nix eval '/workspace#devShells.aarch64-darwin.default.stdenv.{cc.name,initialPath}'` |
| `nix shell` prepends exactly one dir | `nix shell nixpkgs#hello --command bash -c 'echo $PATH'` |
| All six agent attrs exist for `aarch64-darwin` / both Linuxes, with the versions and unfree flags in §5.1 | `nix eval nixpkgs#legacyPackages.<sys>.<attr>.{meta.platforms,meta.unfree,version}` per attr |
| `x86_64-darwin` throws for all six, for `hello`, **and for the flake's own `yoloDarwinPackages`** (even with an empty package list) — **no longer true of the flake; see the §5.1 retraction** | same eval, plus `nix eval --impure '/workspace#packages.x86_64-darwin.yoloDarwinPackages.drvPath'` → `error: Nixpkgs 26.11 has dropped support for x86_64-darwin.` |
| An unfree attr in `packages:` aborts the eval; `NIXPKGS_ALLOW_UNFREE=1` fixes it — **superseded by §2's re-measurement: the abort is at BUILD, not eval** | `YOLO_EXTRA_PACKAGES='["claude-code"]' nix eval …yoloDarwinPackages.drvPath`, with and without the var |
| `nix profile` records a locked flake URL per entry | `nix profile add --profile <tmp> nixpkgs#hello; nix profile list --profile <tmp>` |
| npm-vs-nixpkgs versions in §5.1 | `npm view <pkg> version` for the four npm-distributed packs; `claude --version` in-jail for claude |
| `iptables`/`glibc`/`nix-ld` have no darwin build; the GNU userland core does | `nix eval nixpkgs#legacyPackages.aarch64-darwin.<pkg>.meta.platforms` |
| No `DYLD_*` handling exists anywhere in the repo | `rg DYLD` → 2 hits, both vendored `x/sys` constants |
| `darwinpkg` registers no gcroot — **fixed 2026-08-05, see §5.4** | `rg 'gcroot\|add-root' internal/darwinpkg internal/macosuser` → no matches; contrast `internal/image/gcroot.go` |

### 10.1 Re-verification pass, 2026-08-23

Same jail, current tree (`flake.lock` nixpkgs `f13ff45a`, plus the `nixpkgs-x86-darwin` 26.05
input). What changed is stated where it belongs; this is the audit trail.

| Claim re-checked | Verdict | How |
|---|---|---|
| The flake attrs are `yoloNoncontainerPackages` / `yoloUnavailablePackages` | **confirmed** | `flake.nix:1204`, `:1210`; the Go side pins the pair in `internal/darwinpkg/darwinpkg.go:29-32`, with a drift test that fails if the flake stops binding them (`flakeattr_test.go`) |
| The target system follows the machine, not a constant | **confirmed** | `darwinpkg.NativeSystem()`, `darwinpkg.go:46-76` |
| The profile build is GC-rooted, and rooting is not optional | **confirmed** | `--out-link` in `BuildProfileArgv` (`darwinpkg.go:117-141`); `materialize_test.go` asserts on the argv actually run |
| `x86_64-darwin` evaluates rather than throwing | **CHANGED — old claim retracted** | `nix eval --impure --raw '/workspace#packages.x86_64-darwin.yoloNoncontainerPackages.name'` → `yolo-noncontainer-packages`, with `evaluation warning: Nixpkgs 26.05 will be the last release to support x86_64-darwin` |
| Unfree warn-and-skip still fires, and `NIXPKGS_ALLOW_UNFREE=1` still overrides it | **confirmed** | `YOLO_EXTRA_PACKAGES='[…6 agent attrs…]' nix eval --impure --json '/workspace#yoloUnavailablePackages.<sys>'` → `["claude-code","github-copilot-cli","antigravity-cli"]` on all three systems; `[]` on `aarch64-darwin` with the var; `["antigravity-cli"]` on `x86_64-darwin` with the var |
| `--at` is still `apply`-only; no launch verb at `host` — **no longer true: `yolo host -- <cmd>` and its `yolo --at host -- <cmd>` alias shipped 2026-08-30, postscript 4** | **confirmed on 2026-08-23** | `internal/cli/apply.go:54-64`, `internal/cli/help.go:21` |
| `apply --host` still reports nothing about `packages:` | **confirmed** | no `packages` handling anywhere in `internal/cli/apply.go` |
| The nix daemon probes and the profile report are still macOS-gated | **confirmed** | `section_nix_probe.go:28-31`, `check.go:77-78`, `sections_macos.go:100` |
| The Go env whitelist is still one variable | **confirmed** | `PKG_CONFIG_PATH` only, `darwinpkg.go:169-192` |
| Still no `DYLD_*` handling | **confirmed** | `rg DYLD` → the same 2 vendored `x/sys` hits |
| Every shipped pack's agent CLI still comes from npm or an installer | **confirmed** | `rg '"via"' packs/*/pack.json` → 4 × `npm`, 2 × `installer` |
| Nothing warns when a declared package shadows a system binary (OQ-5) | **confirmed absent** | no shadow check in `internal/darwinpkg` or `internal/macosuser` |

**Not re-verified in this pass:** the §5.1 **version** column (nixpkgs and npm versions have both
moved since 2026-08-02 — the freshness *argument* stands, the specific numbers are stale), and
the exact package versions on the 26.05 `x86_64-darwin` line: a `builtins.getFlake`-based
comparison eval was killed at 600s in-jail and is not worth the wall-clock. `antigravity-cli`'s
absence there was measured through the mechanism's own skip list, which is the fact that matters.

**Not verified, and flagged as such:** everything about macOS *runtime* behavior — SIP stripping
`DYLD_*`, `path_helper` reordering, whether a prepended nix `clang` actually breaks an Xcode
build. Those are asserted from documented macOS behavior and from what the repo already
compensates for (`WriteLoginRC` exists precisely because `path_helper` reorders PATH), not from a
run on a Mac. A Mac session would settle them; none of the doc's conclusions turn on the details.
