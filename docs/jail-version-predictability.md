# Predictable tool versions in jails — plan

**Date:** 2026-07-03 · **Status:** plan drafted, no decision yet.
Implemented 2026-07-03: weekly `flake.lock` bump CI workflow added
(`.github/workflows/update-flake-lock.yml`); first manual bump done the
same day — nixpkgs 2026-02-08 → 2026-07-02 (mise 2026.2.1 → 2026.6.13,
node 22.23.1, python 3.13.13, go 1.26.4, git 2.54.0). Image rebuild
(`just load && just install`) pending on the host
**Trigger:** in-jail mise is `2026.2.1` while the host runs `2026.6.11`; the
skew produced divergent behavior on shared state (see
[mise-host-jail-path-mismatch.md](mise-host-jail-path-mismatch.md)).

## How the jail's mise got so old

Every tool baked into the jail image (mise, node, python, go, git, rg, …)
comes from `flake.nix`, whose single `nixpkgs` input
(`github:nixos/nixpkgs/nixos-unstable`) is locked by `flake.lock`. That lock:

- pins nixpkgs rev `d6c71932…` dated **2026-02-08**;
- was committed with the initial open-source commit (2026-03-22) and has
  **never been updated since**.

So the image is frozen at nixos-unstable-as-of-Feb-8. mise `2026.2.1` is
simply what that snapshot shipped. The host's mise (`2026.6.11`) is installed
outside yolo's control, so the two drift apart indefinitely.

## What is already deterministic (the gap is not reproducibility)

The build is *already* fully pinned — arguably yolo already has the lockfile
the problem statement asks for:

| Source | Determinism today |
|---|---|
| Base image toolset (`flake.nix` package list) | Locked by `flake.lock` — bit-for-bit predictable |
| Plain `packages: ["postgresql"]` entries | Resolve against the **same** locked nixpkgs (`imagePkgs.${name}`, flake.nix:113/137) — predictable |
| `{"name": …, "nixpkgs": "<commit>"}` entries | Pinned to an explicit nixpkgs commit (flake.nix:118-124) — predictable |
| `{"name": …, "version", "url", "hash"}` entries | Exact source override — predictable |
| Host tools (mise, git, …) | Out of scope — unmanaged by yolo |

The real gaps are **process and visibility**, not mechanism:

1. **Silent staleness.** Nothing bumps `flake.lock`, nothing reports its age,
   and nothing surfaces what versions a jail actually ships. The lock aged
   ~5 months without anyone noticing until a version-skew bug bit.
2. **Monolithic updates.** When the lock *is* bumped, every tool moves at
   once; there's no way to advance one critical tool (mise) independently.
3. **Unmanaged host↔jail skew on shared state.** mise's binary version
   differs across a *shared* `MISE_DATA_DIR`. Different mise versions
   interpreting/rewriting the same state dir is exactly what made the
   dangling-symlink bug fatal on one side and invisible on the other.

## Candidate measures (composable, not either/or)

### V1. Visibility: `yolo versions`

Add a `yolo versions` command that prints the locked nixpkgs rev + date and
the resolved versions of the headline tools (mise, node, python, go, git).
Diagnostic only — **no staleness nagging in `yolo check`**: jail users are
the wrong audience for "the lock is old" (decided 2026-07-03; that's a
maintainer concern, handled by V2's CI).

- \+ Cheap; makes the whole topic observable; no behavior change.
- ‒ Doesn't by itself fix anything.

### V2. Update policy: weekly CI lock bumps (dependabot-style)

A scheduled CI workflow — the standard tool is the
`DeterminateSystems/update-flake-lock` GitHub Action — runs weekly, bumps
`flake.lock`, and opens a PR; the existing CI (`.github/workflows/ci.yml`
already runs a nix build) validates it, and the PR body can carry a
before/after table of headline tool versions. Staleness is thereby handled
where dependency updates belong: maintainer-facing PRs, not user-facing
warnings.

- \+ Uses nix's native mechanism; one reviewed PR = one predictable
  version set for **all** jails; zero yolo-runtime surface.
- ‒ Monolithic bump; occasionally a nixpkgs update breaks an unrelated tool
  (that's what the PR's CI run + a nested-jail test is for).

### V3. Per-tool pins for shared-state-critical tools (mise first)

Pin mise in `flake.nix` independently of the main lock, using the same
override mechanisms user packages already have (nixpkgs-commit pin or
version/url/hash). Record it somewhere greppable (a `TOOLCHAIN` block in
flake.nix or a small `toolchain.jsonc` the flake reads). Then mise can be
bumped to match the host (or a chosen target) without moving the world.

- \+ Decouples the one tool whose version demonstrably matters; makes the
  mise version an explicit, reviewed choice rather than a lock side-effect.
- ‒ Pin rot: an explicitly pinned tool is even easier to forget than a lock
  (mitigate via V1 reporting + V4 skew check).

### V4. Host↔jail mise skew guard

The `yolo` CLI runs on the host, so at `yolo run`/`yolo check` time it can
compare host `mise --version` against the image's mise and warn on mismatch
(major/minor, not patch). Rationale: any two mise versions sharing
`MISE_DATA_DIR` is a latent hazard regardless of how versions are managed.

- \+ Directly targets the observed failure mode; catches drift from either
  side, including host upgrades yolo can't control.
- ‒ Warn-only (yolo can't fix the host); noisy if host auto-updates mise
  often.

### V4a. How the host gets to a specific mise version (if V4 blocks)

If skew blocks instead of warns, the jail pin becomes the source of truth
and the host must be able to converge on an exact version. The options, by
how the host's mise was installed:

1. **Standalone install (`~/.local/bin/mise` via `curl https://mise.run`)**
   — `mise self-update 2026.6.11` takes an exact version argument
   (verified via `--help`), or re-run the installer pinned:
   `curl https://mise.run | MISE_VERSION=v2026.6.11 sh`. Clean and exact.
2. **Distro package (pacman on Arch)** — `self-update` is disabled for
   package-manager installs. Pinning means downgrading via the Arch
   archive (`pacman -U https://archive.archlinux.org/packages/m/mise/…`)
   plus `IgnorePkg = mise`, and every `pacman -Syu` thereafter fights the
   pin. Effectively: blocking on skew forces the host off the distro
   package.
3. **From the same nixpkgs pin the jail uses (recommended if blocking)** —
   the host already has nix (it builds the jail images). Expose mise as a
   flake output of this repo and install it into the host profile:
   `nix profile install /path/to/yolo-jail#mise`. Host and jail then run
   the **byte-identical binary from the same `flake.lock`**, and a lock
   bump updates both sides through one reviewed commit. A `just mise-sync`
   target (host-only, like `agent-deploy`) makes it one command, and the
   block message can print exactly that command.

Caveat for (3): nixpkgs lags mise releases by days-to-weeks, so the
matched version is "whatever the lock has", not "latest". If the pin ever
uses the flake's version/url/hash override to get ahead of nixpkgs, the
same output still works — the flake builds it, both sides share it.

Cost of blocking, regardless of mechanism: a host that updates mise as part
of routine system updates (pacman, self-update cron) will re-skew after
every update and be unable to enter any jail until re-synced. That is the
strongest argument for warn-at-run/block-only-in-check, or for adopting (3)
so the sync is trivial.

### V5. Share the host's mise binary into the jail

mise ships as a static musl binary, so the host's `mise` would likely run
unmodified inside the NixOS container. Bind-mounting it makes skew
*impossible by construction* for the one tool that shares state.

- \+ Eliminates the class rather than detecting it.
- ‒ Inverts the goal: jail version now tracks host state, so jails are no
  longer predictable from the repo alone; breaks if a host ever has a
  non-static/foreign-arch mise; macOS/Apple Container host binary is
  Mach-O and can't be shared at all. Keep as a fallback idea, not the plan.

### V6. Per-workspace image locks

Let each workspace pin its own nixpkgs for the whole image (not just per
package). Rejected for now: per-package pinning already exists for the rare
case, and per-workspace base images fragment the image cache and multiply
build times for little gain.

### V7. Remove the shared state instead of managing the skew

The skew only *matters* because host and jails share `MISE_DATA_DIR`.
Option F in
[mise-host-jail-path-mismatch.md](mise-host-jail-path-mismatch.md) splits
that: jails share a jail-land-only store (mounted at the same path string,
so nothing in-jail changes), the host keeps its own. macOS already works
this way (named volume `yolo-mise-data`, run_cmd.py:1070); Linux is the
only branch with a true host bind. With F in place, the host's mise
version becomes irrelevant to jails — V4/V4a (skew guard, host version
sync) are no longer needed, and V3's pin matters only for cross-image
consistency within jail-land. The full bundle (split store + neutral path
+ per-side venvs) is written up as the likely path in
[jail-state-separation-design.md](jail-state-separation-design.md).

## Recommended plan (updated 2026-07-03 — V7/F changes the shape)

Phased; each step is independently shippable:

1. **V7 (= option F) first** — split the host↔jail mise store; jails keep
   sharing among themselves. One-line mount change with macOS precedent.
   This *dissolves* the host-skew problem instead of policing it.
2. **V2** — one manual `flake.lock` bump now (first update since March,
   closes the 5-month gap), then the weekly `update-flake-lock` CI
   workflow so it never silently ages again. Maintainer-facing PRs, not
   `yolo check` warnings.
3. **V1** — `yolo versions` as a diagnostic command (no nagging).
4. **V3 resolved as "no separate pin"** — the repo's `flake.lock` *is* the
   mise pin (yolo-jail version governs mise version); manual bumps on CI
   notification. See the Open Question answer below.
5. **V4/V4a dropped** (obsoleted by V7). Skip V5/V6 unless evidence changes.

## Open Questions

### What cadence for flake.lock updates?

Monthly is a reasonable default; too-frequent bumps churn the image cache
(each bump = full image rebuild on every machine), too-rare recreates this
staleness.

_Leaning:_ Monthly nag via `yolo check` lock-age warning (threshold ~45
days), bump on demand when a specific version matters. No CI automation
until the manual loop proves annoying.

**Answer:**
> Weekly, via CI automation (dependabot-style PR from a scheduled
> `update-flake-lock` workflow) — decided 2026-07-03. Staleness handling
> must NOT surface in `yolo check`: jail users are the wrong target;
> version updates are a maintainer/CI concern.

### Should the mise skew check warn or block?

_(Likely moot if V7/option-F ships — with the store split, host mise
version no longer touches jail state. Kept for the record.)_

`MISE_DATA_DIR` sharing means any skew is a latent hazard, but blocking
`yolo run` on a host-side condition yolo can't fix would be hostile —
unless the fix is one command (see V4a; the nix-profile route makes it
`nix profile install .#mise` / `just mise-sync`).

_Leaning:_ Warn at `yolo run` (one line), full detail in `yolo check`. If
we adopt V4a-(3) and host mise comes from this repo's flake, upgrading to
block becomes reasonable because the block message can print the exact
one-command fix.

**Answer:**
> _(empty — fill in when decided)_

### Where does the mise pin live?

Options: inline in `flake.nix` (simplest, but buried), a `toolchain.jsonc`
read by the flake (greppable, but new plumbing), or a documented convention
in this doc + V1 reporting.

_Leaning:_ Inline in `flake.nix` with a loud comment, surfaced by
`yolo versions` — no new config surface until a second tool needs pinning.

**Answer:**
> Decided 2026-07-03: **no separate pin.** The yolo-jail version governs
> the mise version via `flake.lock` — a given repo commit ⇒ a given mise,
> deterministically. Bumps are manual, prompted by CI (the weekly
> update-flake-lock PR and/or its failure notifications). With the
> host↔jail store split, the pin's original host-matching purpose is gone;
> cross-image homogeneity in jail-land is maintained by keeping images
> current with the repo, not by a second pinning mechanism.

### Does the headline-version report need to work without a build?

`yolo versions` could read versions from the built image (accurate but needs
an image) or evaluate the flake (`nix eval`, no build but slower/cold-cache).

_Leaning:_ Read from the built image when present, fall back to `nix eval`;
`yolo check` already builds anyway.

**Answer:**
> Deferred 2026-07-03 — `yolo versions` is a nice-to-have diagnostic, not
> part of the accepted work; decide at implementation time per the leaning
> if/when it gets built.
