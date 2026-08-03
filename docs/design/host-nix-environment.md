# A reproducible tool environment at the `host` notch, via nix

**Status:** EXPLORATORY analysis, 2026-08-02. **No code changed, no plan proposed.** Written to
test a thesis, not to sequence work. Where the answer is "already built" or "not worth doing,"
it says so.

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

**Reads with:** [`macos-user-nix-and-features.md`](macos-user-nix-and-features.md) §1 (the
shipped mechanism, in detail — this doc does not restate it),
[`yolo-as-environment-manager.md`](yolo-as-environment-manager.md) §3.5 + §4 (the dial and the
dep-handoff design), [`host-render-target.md`](host-render-target.md) §2.1 (the kind census),
[`../plans/environment-manager-plan.md`](../plans/environment-manager-plan.md) Phase 4.3/6/7
(what is deferred), [`../plans/pack-host-management-plan.md`](../plans/pack-host-management-plan.md)
§8.3/§8.4 (the `install_hints` matrix and its two live defects).

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
"entered." This is what `packages.yoloDarwinPackages` is.

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
confinement, your machine, your credentials. `yolo apply --host` renders config into the real
`$HOME` and **launches nothing** — which is exactly why the `launch` kind is refused there
(`render.HostUnimplemented`: *"launch flags need a launcher — apply --host configures your tools
but never runs them, so there is nowhere to inject them"*). Hold onto that: it is the crux of
§4.2.

---

## 1. What is already solved, stated precisely

The most common way to waste effort here is to design something that exists. So, exactly:

| Capability | State | Where |
|---|---|---|
| A nix expression that materializes `packages:` as a **pure, toolchain-free profile** | **SHIPPED** | `flake.nix` `packages.yoloDarwinPackages` |
| …and it works for **every** `eachDefaultSystem` system, Linux included | **SHIPPED, unadvertised** | verified above on `x86_64-linux` |
| Realizing it and putting `<out>/bin` on an agent's PATH, no container | **SHIPPED** | `internal/darwinpkg` → `internal/macosuser/orchestrator.go` |
| Per-package "no build on this platform" filtering, warn-and-skip | **SHIPPED** (a hard error is decided but unbuilt: revival plan **A2**) | `darwinSkippedNames` / `darwinUnavailablePackages` |
| Pinning to yolo's `flake.lock` rather than the user's channel | **SHIPPED** (structural — it *is* the flake) | `flake.lock` rev `241313f4` |
| `yolo check` verifying nix + `/nix` + trusted-user on macOS | **SHIPPED** | `cli/check/section_nix_probe.go`, `sections_macos_platform.go` |
| The same, on **Linux** | **NOT WIRED** — `nixDaemonStoreCheck` and the platform block are `IsMacOS`-gated | same files |
| A **caller** for the profile at the `host` notch | **DOES NOT EXIST** | `apply --host` never touches nix (`cli/apply.go`) |
| `packages:` reported at all by `apply --host` / `check --at host` | **DOES NOT EXIST** — `packages` is not a pack *kind*, so the `FieldSet` census never sees it; it is a top-level config key with no host handler | `render/fieldset.go`, `cli/apply.go` |
| A **gcroot** on the realized profile | **DOES NOT EXIST** — `darwinpkg` passes `--no-link`, and no `--add-root`; contrast `internal/image/gcroot.go`, which does this for the OCI image | `darwinpkg/darwinpkg.go:81` |

**So the honest framing is not "should yolo build a host nix environment."** It is: *yolo already
has one, for one notch on one platform, called by one backend, under a platform-specific name,
with no GC root and no non-macOS `check` coverage.* Everything below is about whether to
generalize that and what it buys.

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

---

## 2. What `install_hints` is for, and what a nix env would and would not replace

`install_hints` (`packdecl.DepRequirement` → `internal/depcheck`) maps a *pack's* declared
binary to a package name per host package manager, so `check-deps` / `apply --host` can print
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

  So the `tryEval` wrapper around `availableOn` (`flake.nix:301-303`) **absorbs the unfree
  assertion during eval** — the package is reported *available*, the skip path never runs, and
  the abort surfaces later inside `buildEnv`. The guard is not missing a check so much as
  succeeding at the wrong time: `availableOn` reads `meta.platforms`/`badPlatforms` and unfree
  is not a platform fact, so no amount of platform probing will catch it.

  The consequence for the user is what the earlier framing got right: putting an agent CLI in
  `packages:` yields a nix `check-meta` trace rather than the `darwinUnavailablePackages`
  warn-and-skip that the mechanism promises.

  **FIXED (2026-08-02).** `darwinResolved` now tests `drv.meta.available` — nixpkgs' own
  verdict, which reads without throwing — alongside `availableOn`, and routes a failure into
  the existing skip-and-warn path. `availableOn`'s use is unchanged. `meta.available` rather
  than a bare `meta.unfree` test because it flips back to true under `NIXPKGS_ALLOW_UNFREE=1`
  / `allowUnfreePredicate`, so a user who deliberately opted in still gets the package
  instead of a silent skip; yolo does **not** set that variable for them. The skip warning
  rides on `darwinPackages` (the build path, whose stderr is streamed) rather than on the skip
  list alone (read by a separate eval whose stderr is discarded). Reason precedence puts the
  platform case first, since `meta.available` folds `unsupported` in with the licence checks.
  Verified: `YOLO_EXTRA_PACKAGES='["hello","claude-code","iptables","nosuchpkg-xyz"]'` builds,
  keeps `hello`, and warns three distinct reasons (unfree / no darwin build / no such package).
- The brew-cask Brewfile bug (§8.4) is unaffected by any nix work. **FIXED (2026-08-02)** on
  its own: `install_hints` grew a `brew-cask` flavor key; see `internal/depcheck`.

---

## 3. Where a host nix env would *actually* be consumed

Before comparing mechanisms, be concrete about the consumer, because the answer differs per row
and this is where claim (b) breaks.

| Consumer | Does yolo control the process? | Can a PATH-prepend reach it? |
|---|---|---|
| `yolo --at guest -- claude` | **yes** (yolo execs it) | **yes** — this is exactly what macos-user does today |
| `yolo --at host -- claude` (design §4.1) | **yes**, if this verb exists (it does not; `--at` is `apply`-only today) | **yes** |
| `yolo apply --host` then the user runs `claude` themselves, later, in their own shell | **no** | **only via a shell rc edit** |
| A pack's `program` install at the host notch (Phase 4.3) | **yes**, behind a confirm | n/a — this *installs*, it does not PATH-scope |

**Row 3 is the hard one, and it is the row the maintainer's "installing copilot" question sits
in.** `apply --host` configures and exits. Its own refusal text for the `env` kind already
states the constraint: *"the only place to set these off-container is your shell profile, which
apply --host does not write."* An environment that has to be *entered*, or a PATH that has to be
*prepended*, cannot serve a consumer yolo never launches — unless yolo writes the user's shell
rc, which is a much bigger claim than a pack's env contribution asks for and is already
refused-by-name.

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
squarely on the wrong side of the constraint `render.HostUnimplemented` already records for the
`launch` kind: *`apply --host` configures your tools but never runs them.* So `nix shell` is a
good fit for the `yolo --at host -- claude` / `yolo --at guest -- claude` rows of §3 and **no fit
at all** for the `apply --host`-then-user-runs-it row.

It also loses the two things the buildEnv gives: a **single stable path** (a `buildEnv` out-path
is one dir you can symlink, report in `describe`, and gcroot; `nix shell` re-resolves per
invocation) and **`flake.lock` pinning** (`nixpkgs#x` resolves through the *registry* — the
user's channel — not yolo's lock, unless you pass the locked URL explicitly, at which point you
are back to needing yolo's flake anyway).

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

Three caveats, all verified:

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
  today (OQ-6).

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

**One shipped gap that a host feature makes worse:** `darwinpkg` builds with `--no-link` and
registers **no GC root**, unlike `internal/image/gcroot.go` which carefully roots the OCI image.
A user's next `nix-collect-garbage` therefore deletes the realized profile and the next launch
re-downloads it. Tolerable for a per-launch materialization; **actively bad** for a host env the
user is expected to keep on their PATH between sessions, where GC would silently break their
`claude` command. Any host-notch use needs a gcroot. (OQ-2.)

---

## 6. "Mimic our in-jail envs more" — the isolation/environment split

The maintainer's third instinct is the one that holds up best, and making it precise is this
doc's main contribution. Everything the jail gives its agent, sorted by whether a nix closure
plus a launch env could supply it off-container:

| What the jail provides | Mechanism | Class | Off-container? |
|---|---|---|---|
| `corePackages` / `fullPackages` (the ~60-package baked set) | OCI image layers | **environment** | ✅ a `buildEnv` of the same attrs (minus Linux-only ones, §5.2) |
| `packages:` (user's extras) | `extraPackages` → image | **environment** | ✅ **already shipped** as `yoloDarwinPackages` |
| `mise_tools` | mise, PATH-ordered shims | **environment** | ✅ already runs natively on macos-user (`ConfigureMisePrism`) |
| Env hygiene (`PAGER`/`GIT_PAGER=cat`, `BAT_PAGER=""`, `EDITOR=cat`, `VISUAL=nvim`, `OVERMIND_SOCKET`) | `-e` flags + generated `.bashrc` | **environment** | ⚠️ **only for a process yolo launches.** In a shell yolo does not start, this is a shell-rc edit — refused by name today (`KindEnv`). And `EDITOR=cat` in a *human's* shell is hostile: it exists because an agent cannot drive an editor |
| `PATH` order (`.yolo-shims:.local/bin:$NPM_CONFIG_PREFIX/bin:<mise>:$GOPATH/bin:/bin:/usr/bin:.yolo-launchers`) | generated `.bashrc` / launch env | **environment** | ⚠️ same: yolo-launched yes, user's shell no. macos-user already needs a **login-rc re-prepend** (`.zprofile`/`.zshrc`/`.bash_profile`) to survive macOS `path_helper` — evidence of how far you must reach to own a PATH you did not start |
| Blocked-tool shims (`grep -r`, `find`) | generated scripts, first on PATH | **hybrid — see below** | ⚠️ mechanically yes; the design flags it `!` ("shims would land on your real PATH — opt in explicitly") |
| `/lib` farm + `LD_LIBRARY_PATH` + nix-ld | image + baked `Env` | **environment, but Linux-container-only** | ❌ no darwin analogue (§5.3); on a Linux host it would be actively wrong to set `LD_LIBRARY_PATH=/lib:/usr/lib` |
| Composed agent config (settings, MCP, LSP, skills, briefing) | the prism / `render` | **environment** | ✅ **already shipped** — `apply --host` |
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
pack's `program` is `via: npm` or `via: installer`). So the *jail* does not get its agent CLIs
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
launches the process, so a prepend works. At `host`, `apply --host` launches nothing — hence the
`launch` and `env` kinds' refusals. **The mechanism's *viability* is a function of the notch**,
which is the definition of not-orthogonal. §3 is this argument in table form.

**3. The primitive model already says so.** `render.confinement.go` lists **`PrimBakedImage`**
as a Primitive, with the comment: *"A provisioning primitive, not a confinement one, but it
travels with the jail notch and is absent below it."* The code has already recorded that
provisioning is entangled with the notch and put the entanglement in the same struct. A host nix
env is precisely the *replacement* for `PrimBakedImage` below `jail` — which makes it a
**candidate seventh Primitive** (`PrimNixProfile`?), not a feature living beside the dial.

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

### Option 0 — Do nothing; fix the two `install_hints` defects instead

Fix the brew-cask Brewfile bug (§8.4) and the unfree-hint insufficiency (§8.3), and leave
provisioning at the host notch as "print the remedy, the user runs it."

**For:** zero new mechanism; both fixes are needed regardless; the design's "manifest is the
floor" rule is satisfied. **Against:** leaves `install_hints` covering **0–1 of six** agent CLIs
on a non-Arch Linux host (§2), which is the concrete weakness that started this. Does not touch
the pre-existing `packages: ["claude-code"]` eval abort (OQ-6), which is a real bug either way.

### Option 1 — Rename and generalize the shipped mechanism; add no new consumer

Rename `yoloDarwinPackages` → something system-neutral (`yoloHostPackages`), stop hardcoding
`aarch64-darwin` in `darwinpkg`, add a gcroot, and make `describe` / `check --at host` **report**
the resolved profile path. `apply --host` gains one line: *"packages: <n> resolved at
/nix/store/… — add its bin to your PATH, or use `yolo --at host --` (unbuilt)."*

**For:** small; honest; fixes the GC-root gap; removes a platform-specific name from a
platform-neutral output; and it is a **prerequisite for Phase 7.2** regardless of what happens
at `host`. Does not touch the user's machine or their PATH. **Against:** solves row 3 of §3 only
by handing the user a path and asking them to wire it up. That is a real product gap, though it
is exactly consistent with the "manifest is the floor" posture.

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

**What would talk me out of all of it:** if the answer to OQ-1 is "the host notch is
configure-only, forever" *and* the maintainer is content with `install_hints` coverage, then
Option 1 shrinks to a Phase 7.2 prerequisite with no host-notch story at all, and this doc's
conclusion is *"already solved for macos-user; the real gap is Linux `guest`, which is Phase
7.2's problem."* I think that is a defensible outcome and would not be a wasted read.

---

## 9. Open questions for the maintainer

Ordered by how much else they block. Each says what would resolve it.

- **OQ-1 (blocks everything). Is the `host` notch a place where agents *run*, or only a place
  where they are *configured*?** Today it is configure-only, and `launch`/`env` are refused for
  that reason. Option 2 says "run"; Option 3 says "install"; Option 1 says "neither, just report."
  **Resolved by:** a product decision, not research. Worth answering before any nix work,
  because §3's table has a different winner per answer.

- **OQ-2. Should the realized profile be gcrooted, and where?** It is not today
  (`darwinpkg` passes `--no-link`, no `--add-root`), so a user's `nix-collect-garbage` deletes
  it. Harmless for a per-launch materialization; breaks a host env the user keeps on PATH.
  Note `internal/image/gcroot.go` already solves this for the image and records that the
  registration **must run host-side** (in-jail, `/nix/var/nix/gcroots` is unmounted).
  **Resolved by:** deciding whether the profile outlives a launch. If yes, this is a real bug
  today on macos-user too.

- **OQ-3. Is `nix profile --profile <yolo-dir>` (§4.3) attractive or a trap?** It gives a stable
  path *plus* generations, rollback, and a self-managing gcroot — but it is mutable state, which
  adds a row to the §3.3 closure table and interacts with `--sealed`. **Resolved by:** deciding
  how much the reproducibility/sealing story must cover host provisioning. I genuinely do not
  know which way this should go and would not want to guess in code.

- **OQ-4. Does the environment need to carry *variables*, not just PATH?** A `buildEnv` cannot;
  a devShell can (and that is the *only* real argument for one). Today Go carries a whitelist of
  exactly one (`PKG_CONFIG_PATH`). The jail's baked `Env` carries `SSL_CERT_FILE`,
  `LD_LIBRARY_PATH`, `PKG_CONFIG_PATH`, `FONTCONFIG_*`, `TZDIR`. **Resolved by:** enumerating
  which of those a host/guest process actually needs. If it stays ≤3, keep the Go whitelist; if
  it grows, the devShell rejection deserves a second look on *this* axis only (never on PATH).

- **OQ-5. Is "no PATH pollution" the right claim for a `buildEnv`, or should it be "no
  *undeclared* pollution"?** A `buildEnv` containing `gnugrep` still shadows `/usr/bin/grep`
  when prepended (§5.2) — the difference from a devShell is legibility, not effect. On a Mac
  host that is the BSD-vs-GNU hazard arriving by the front door. **Resolved by:** deciding
  whether a host-notch profile should *warn* when a declared package shadows a system binary, or
  trust the declaration.

- **OQ-6 — RESOLVED AND FIXED (2026-08-02). `packages:` containing an unfree attr aborted the
  `yoloDarwinPackages` BUILD** (not the eval — see §2's re-measurement) instead of being
  skipped, because `availableOn` reads `meta.platforms` and never sees the licence.
  **Resolved:** yolo catches the case and reports it through the existing
  `darwinUnavailablePackages` warn-and-skip, and does **not** set `allowUnfree` on the user's
  behalf — unfree is a licence decision the user makes once, machine-wide, and slipping the
  override in would make it for them silently (the same consumer-grants-power invariant
  `allow_exec` follows). A user who *has* opted in (`NIXPKGS_ALLOW_UNFREE=1` or
  `allowUnfreePredicate`) still gets the package, because the check reads `meta.available`,
  which honors those. Fires for **any** unfree attr, not just an agent CLI (`["vscode"]`,
  `["terraform"]` measured identically).

- **OQ-7. Should the *jail* get its agent CLIs from nix too?** All six are in nixpkgs for all
  three live platforms (§5.1); the jail currently installs them lazily via `npm -g` and
  curl-to-shell. Nix would make the jail more reproducible and drop the curl-to-shell
  (a supply-chain improvement); npm/installer gives same-day upstream versions, which for
  agent CLIs shipping daily may matter more (§5.1's freshness column: two of six already lag).
  **Resolved by:** a call on pin-vs-freshness for agent CLIs specifically. Note this is a
  *bigger* change than anything else in this doc and would touch every pack — flagging it
  because the research surfaced it, not because I recommend it.

- **OQ-8. Should the `packages:` key report at all below `jail`?** It is not a pack kind, so the
  `FieldSet` census never sees it and `apply --host` prints nothing about it — while macos-user
  honors it natively. The env-manager design meanwhile promises `check --at host` will print
  *"packages: yolo does not manage packages here."* Those cannot both be right (§1.1).
  **Resolved by:** OQ-1, mostly — but the *reporting* inconsistency is worth fixing even under
  Option 0, since "silently absent" is the failure mode `render.HostUnimplemented` exists to
  prevent.

- **OQ-9. Do Linux `yolo check` runs need the macOS nix probes?** `nixDaemonStoreCheck` and the
  `/nix`-exists check are `IsMacOS`-gated. If a nix env becomes a Linux host feature, a Linux
  user with a broken daemon gets no diagnosis. **Resolved by:** whichever option is chosen —
  under Option 0 it does not matter.

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
| `x86_64-darwin` throws for all six, for `hello`, **and for the flake's own `yoloDarwinPackages`** (even with an empty package list) | same eval, plus `nix eval --impure '/workspace#packages.x86_64-darwin.yoloDarwinPackages.drvPath'` → `error: Nixpkgs 26.11 has dropped support for x86_64-darwin.` |
| An unfree attr in `packages:` aborts the eval; `NIXPKGS_ALLOW_UNFREE=1` fixes it | `YOLO_EXTRA_PACKAGES='["claude-code"]' nix eval …yoloDarwinPackages.drvPath`, with and without the var |
| `nix profile` records a locked flake URL per entry | `nix profile add --profile <tmp> nixpkgs#hello; nix profile list --profile <tmp>` |
| npm-vs-nixpkgs versions in §5.1 | `npm view <pkg> version` for the four npm-distributed packs; `claude --version` in-jail for claude |
| `iptables`/`glibc`/`nix-ld` have no darwin build; the GNU userland core does | `nix eval nixpkgs#legacyPackages.aarch64-darwin.<pkg>.meta.platforms` |
| No `DYLD_*` handling exists anywhere in the repo | `rg DYLD` → 2 hits, both vendored `x/sys` constants |
| `darwinpkg` registers no gcroot | `rg 'gcroot\|add-root' internal/darwinpkg internal/macosuser` → no matches; contrast `internal/image/gcroot.go` |

**Not verified, and flagged as such:** everything about macOS *runtime* behavior — SIP stripping
`DYLD_*`, `path_helper` reordering, whether a prepended nix `clang` actually breaks an Xcode
build. Those are asserted from documented macOS behavior and from what the repo already
compensates for (`WriteLoginRC` exists precisely because `path_helper` reorders PATH), not from a
run on a Mac. A Mac session would settle them; none of the doc's conclusions turn on the details.
