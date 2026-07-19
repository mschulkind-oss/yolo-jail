# Go port — post-transition backlog (non-critical-path)

**Date:** 2026-07-19. **Status:** queued — do NOT start until the Go-only cutover
lands (see [go-port-remaining-work.md](go-port-remaining-work.md) for the
critical path to that point).

This is the parking lot for work that is real but **not on the critical path to
running Go-only**. The maintainer's directive: don't build Python *and* Go
versions of everything — queue non-critical items here and do them after the
transition, in a single Go world. Grouped by theme; each item notes what it
depends on.

---

## 1. nix-ld — kill the `LD_LIBRARY_PATH` whack-a-mole (image change)

**Decision landed** (`de66f62`, `e6d7734`): replace the `/lib64` interpreter
symlink (currently the raw nix glibc `ld.so`) with **nix-ld** as the FHS
interpreter, so the mise node resolves `libstdc++` env-free and the per-call-site
wrapper hacks disappear. Full blueprint + empirical validation in
[docs/design/mise-node-dynamic-linking.md](../design/mise-node-dynamic-linking.md)
§Resolution.

**Why post-transition, not now:** it's a `flake.nix` + entrypoint image change
requiring a host `nix build` + `just load && just install` (can't be built or
validated from inside the jail), and it's orthogonal to the Python→Go cutover.
Do it as its own sequenced change with nested-jail validation at each gate.

**The work (from the blueprint):**
- [ ] flake.nix: retarget `/lib64` + `/lib` interpreter symlink → nix-ld
  (variant A: custom derivation with `DEFAULT_NIX_LD` baked to the real loader;
  variant B: stock `pkgs.nix-ld` + `NIX_LD` env + `/run` wiring). Delivery rides
  the same CI/Cachix/on-demand-builder paths as the rest of the image.
- [ ] flake.nix: bake the minimal fallback lib dir at a non-store path
  (`/usr/share/nix-ld/lib/` — ld.so + core farm trio symlinks).
- [ ] entrypoint (Python+Go twins, or **Go-only after cutover**): idempotent
  `/run/current-system/sw/share/nix-ld/lib` symlink at startup, on reuse-`exec`
  paths too. **Post-transition this is Go-only** — one implementation, not two,
  which is exactly why it's parked here.
- [ ] cli: explicit mode on `--tmpfs /run` (docker `-u` EACCES guard).
- [ ] KEEP the baked `LD_LIBRARY_PATH` (`flake.nix:718`) — it's the only
  dlopen-by-soname discovery for nix-built processes (user-packages contract).
- [ ] Staged DELETE (separate commits, after nested-jail validation): the
  `LD_LIBRARY_PATH` export lines in the MCP wrappers + evaluate the cli `-e`
  re-export. **The custom-`mcp_servers` wrapper gap closes for free.**
- [ ] Validation: `env -i` smoke suite in a nested jail (mise node, claude
  binary, copilot addons, an MCP spawn, a ctypes dlopen) + one aarch64 run.

**Deferred in full** (2026-07-19 directive: pull nothing forward — transition
first). Even the independent sliver (a `yolo check` loader-drift probe) waits; it
would only add a Python+Go twin to maintain through the cutover, which is exactly
what we're avoiding. Do the whole of §1 after the Go-only world exists, as a
single Go implementation.

---

## 2. Distribution — ship the Go binary the standard way

**The target:** once Python is gone, yolo-jail becomes a **Go CLI + container**
(no longer "Python CLI + container"), so its channel set shifts to the org's
Go-CLI standard. The canonical reference is **swarf** (Go CLI + daemon), whose
setup we copy near-verbatim. Standard: the OSS skill's
`references/distribution.md` — every consumer project ships through **≥3
channels: Homebrew (primary) + language-native (`go install`) + source**, plus
PyPI-via-go-to-wheel for the `pipx`/`uvx` audience.

**This replaces the current PyPI→`homebrew-pypi-poet`→formula chain** (which only
works while there's a Python package). Depends on: Python removed (or at least
the Go binary being the shipped `yolo`). This is critical-path blocker **F.1** in
the other doc — *authoring* the config is safe now, but *cutting over* to it is
post-transition.

### 2a. `.goreleaser.yaml` (copy swarf's shape)
- [ ] `version: 2`; `before.hooks`: `go mod tidy`, `go vet ./...`.
- [ ] `builds:` — **one entry per SHIPPED `cmd/` binary.** Decide the set first:
  the host-facing binaries are `yolo` (+ the host-side daemons that run *outside*
  the jail: `yolo-claude-oauth-broker-host`, `yolo-ps`, `yolo-host-processes` —
  matching today's 4 PyPI console scripts). **The in-jail daemons
  (`yolo-entrypoint`, `yolo-cgd`, `yolo-journald`, `yolo-broker-relay`,
  `yolo-jail-supervisor`, `yolo-oauth-terminator`) ship in the NIX IMAGE, not the
  host release — do NOT put them in goreleaser.** `goprobe`/`yolo-parity` are
  dev-only; exclude. (If module consolidation (§3) folds daemons into one
  multi-call binary, revisit.)
- [ ] `CGO_ENABLED=0`; matrix linux+darwin × amd64+arm64.
- [ ] ldflags `-s -w` + `-X …/internal/version.Version={{.Version}}` +
  `.GitCommit={{.ShortCommit}}` — see §2d.
- [ ] `archives:` tar.gz `name_template`; `checksum:` checksums.txt;
  `changelog:` sort asc, exclude `^docs:`/`^chore:`/`^test:`.
- [ ] **No `brews:` section** (swarf does the tap by hand — §2b). Native
  `brews:` is the simpler alternative if we don't need the source-build formula;
  decide.

### 2b. `.github/workflows/release.yml` (tag-triggered)
- [ ] Trigger `push: tags: v*`; `permissions: contents: write`.
- [ ] `goreleaser` job: checkout@v6 `fetch-depth: 0`, setup-go@v6
  `go-version-file: go.mod`, goreleaser-action@v7 `version: "~> v2"`,
  `args: release --clean`, `GITHUB_TOKEN`.
- [ ] `update-homebrew` job (`needs: goreleaser`): regenerate the formula
  (source-build style: `url` = the GitHub source tarball, `depends_on "go"`,
  `go build` with the version ldflags), push to `mschulkind-oss/homebrew-tap`
  `Formula/yolo-jail.rb` using a **`HOMEBREW_TAP_TOKEN`** secret (cross-repo PAT
  — 🔒 maintainer creates it). ⚠ **yolo-jail wrinkle vs swarf:** swarf's formula
  is a pure `go build`; yolo-jail's `yolo` needs the **nix image** to function
  (the formula/brew install gives you the host CLI, but `yolo run` still builds/
  pulls the container image on first use). Confirm the brew formula's `test do`
  is just `yolo --version`, and that first-run image-build UX is documented.

### 2c. `.github/workflows/publish.yml` (release-published → PyPI via go-to-wheel)
- [ ] Trigger `release: [published]`; `permissions: id-token: write`;
  `environment: pypi`.
- [ ] `uvx go-to-wheel .` (or the right cmd path) with `--name yolo-jail`,
  metadata flags, `--set-version-var …/internal/version.Version --version
  "${GITHUB_REF_NAME#v}"` → wheels for linux(glibc+musl)/macOS/Windows, each
  wrapping the Go binary behind a console entry point. `uv publish` via **PyPI
  Trusted Publishing (OIDC, no token)** — 🔒 maintainer configures the trusted
  publisher.
- **DECIDED (2026-07-19): keep PyPI.** yolo-jail stays a 4-channel project
  (Homebrew + `go install` + PyPI-via-go-to-wheel + source). The go-to-wheel
  path makes it near-free and preserves the existing `pipx install yolo-jail` /
  `uvx` audience through the cutover. License: **Apache-2.0 everywhere** (resolve
  swarf's Apache/MIT inconsistency in our copy).

### 2d. `internal/version` — the injection target
- [ ] Confirm `internal/version` exposes `Version`/`GitCommit`(/`Dirty`) vars
  settable by `-X` (swarf's exact pattern). Today's Go version resolution is
  git-describe/`YOLO_REPO_ROOT` based — reconcile so a tagged goreleaser build
  reports the tag, and a bare `go install …@latest` reports a sane default
  (swarf accepts `dev` there). **Deferred with the rest of §2** (2026-07-19: pull
  nothing forward) — done as part of the distribution cutover, not before.

### 2e. Justfile + README + go.mod
- [ ] Justfile: local `build`/`install`/`deploy` with git-derived ldflags (incl.
  `Dirty`); **no `release` recipe** — releasing = pushing a `v*` tag (swarf model).
  Reconcile with the current `just deploy` (which builds the Python CLI + nix
  image + refresher timer).
- [ ] README `## Install`: the standard 4-channel block (brew / go install /
  pipx / source), install section BEFORE usage.
- [ ] go.mod module path already `github.com/mschulkind-oss/yolo-jail` → `go
  install …@latest` works once tagged.

### 2f. Known copy-time gotchas (from the swarf recon)
- swarf declares Apache-2.0 in goreleaser+brew but MIT in publish.yml — **pick
  one** (yolo-jail is Apache-2.0 per the OSS playbook; use that everywhere).
- goreleaser `main: .` (swarf is a single root-package binary); yolo-jail uses
  `cmd/<name>` — set `main:` accordingly per binary.
- `go install` builds carry no ldflags → report the default version; accepted.

---

## 3. Module consolidation + "always-Go" cosmetics (already in the endgame)

Tracked as §G in [go-port-remaining-work.md](go-port-remaining-work.md); repeated
here as the post-transition anchor:
- [ ] Consolidate the ~60 `internal/*` packages + `cmd/*` binaries (which mirror
  the Python module boundaries) into a structure that reads as native Go. Decide
  whether the `cmd/yolo-*` daemons fold into one multi-call binary (also affects
  the goreleaser `builds:` set in §2a — do these together).
- [ ] Drop the parity/divergence machinery (oracles, drift dump, `divergences.md`);
  keep the regression tests, drop the live-Python comparisons.
- [ ] Strip "ports X" docstrings, rename `*_parity_test.go` that are now plain
  unit tests, archive/delete the `go-port-*` docs.
- [ ] Lift the color-aware rich→ANSI renderer into one shared helper as part of
  this consolidation — see **§5** (the CLI color audit): the same strip-always
  printer is duplicated across 4 command packages; consolidating it here fixes
  the lost-colors bug everywhere at once.

---

## 4. OSS-hygiene sweep against the playbook (low priority, post-stabilize)

Once the dust settles, run the `open-source-project` skill audit against the
Go-only repo (it converges a repo toward the org standard). Likely findings to
pre-stage:
- [ ] README `## Install` matches the standard format (covered by §2e).
- [ ] `.github/workflows/` has the 3-workflow set (ci/release/publish) — ci.yml
  already runs `just check-ci`; release/publish come from §2.
- [ ] dependabot.yml covers the ecosystems (add `gomod`; drop `pip` once Python
  is gone).
- [ ] Versioning: git tags as source of truth (Go standard) — no more
  `setuptools-scm` once Python's removed.
- [ ] Confirm yolo-jail's dossier in the maintainer's backplane
  (`references/projects/yolo-jail.md`) is updated from "Python CLI + container"
  to "Go CLI + container" and the distribution.md row + channel matrix updated.
- [ ] No README claims a channel without a publish workflow (the standard's
  self-audit rule).

---

## 5. CLI color audit — every command's output, not just the config diff

**Confirmed bug class.** The config-change approval diff shipped with no colors
because its `printer` **threaded a `color` flag but always stripped** rich markup
(`richTagRe.ReplaceAllString(s, "")`) instead of rendering it to ANSI. Fixed for
`internal/runcmd` this session (`bdfd522` — added the color-aware `richToANSI`
path). But the same strip-always pattern lives in **other command packages** that
were never audited — **`prune` is confirmed to have lost its colors the same
way** (`internal/prunecmd/prunecmd.go:437` — `line()` strips unconditionally
despite carrying `p.color`).

**Do a systematic pass over EVERY native command's output on a TTY** and confirm
color renders (or is intentionally plain), not silently stripped:

- [ ] **`prune`** (`internal/prunecmd`) — CONFIRMED broken; `printer.line`
  ignores `p.color`. Port `runcmd`'s `richToANSI` (or extract it to a shared
  helper — see below).
- [ ] **`builder`** (`internal/buildercmd`) — has the same `richTagRe`
  strip-always printer; audit.
- [ ] **`macos-*`** (`internal/macosuser/orchestrator.go`) — same pattern; audit
  (dry-run plan + macos-setup/teardown output).
- [ ] **`check`/`doctor`** (`internal/checkcmd`) — has its OWN ansi path
  (`ansiRed`/`style()`); verify it actually colors on a TTY and isn't a
  strip-always in disguise.
- [ ] **`config-ref`** (`internal/configref`) — already has a `tagReplacer`
  ANSI renderer gated on a `color` bool; confirm it's wired to a real TTY check.
- [ ] **`loopholes`, `broker`, `init`, `init-user-config`, `ps`** — audit each;
  some are plain `typer.echo`-equivalent (byte-parity, intentionally no color),
  some should color. Classify each: "intentionally plain" vs "lost its color."
- [ ] **Consolidate the renderer.** Four packages now carry near-duplicate
  `richTagRe` printers (runcmd fixed, three not). During §3 consolidation, lift
  the color-aware rich→ANSI renderer into ONE shared helper (mirror
  `runcmd/console.go`'s `richToANSI` + `isStyleTag` + the `Color && IsTTY` gate)
  and route every command through it. That both fixes the bug everywhere and
  removes the duplication.
- [ ] Gate rule (same as runcmd): render ANSI only when `Color && IsTTYStdout()`
  — never emit escapes to a pipe/redirect (so captured/greppable output stays
  clean, and `NO_COLOR` is honored).

Note: this is cosmetic (the byte-parity contract was always on the ANSI-*stripped*
text, so decisions/numbers were never affected — only the on-screen color). Safe
to defer to post-transition; do it as part of §3's consolidation so the shared
renderer lands once.

---

## Dependencies at a glance

```
cutover (wipe Python)  ──┬─→ §2 distribution cutover (needs Go binary = shipped yolo)
                         ├─→ §3 module consolidation (also feeds §2a builds: set)
                         └─→ §4 OSS-hygiene sweep

nix-ld (§1)  ── independent image change; post-cutover, host-gated

2026-07-19 directive: PULL NOTHING FORWARD. Everything above is post-cutover.
The version-ldflags reconcile (§2d) and the check probe (§1 sliver) COULD be done
now but are deliberately deferred — pulling them forward means maintaining a
Python+Go twin through the cutover, which defeats "transition ASAP".
```
