---
title: "Installed-launcher provenance — implementation sketch"
stage: BUILT
status: superseded
depends-on: [installed-launcher-provenance.md#OQ-IP1]
next: "Delete when the design graduates into self-update.md"
tags: [install, self-update, builds]
summary: "Parking lot for the mechanics of the install-provenance design: where the two builds are made, where an assertion would go, and what has to be re-read against the tree."
---

# Installed-launcher provenance — implementation sketch

**Status:** 2026-10-23, built. Every question is ruled, and the build is recorded in [the design's decision ledger](installed-launcher-provenance.md#9-decision-ledger). The anchors below name the tree before the build, and many no longer exist.

> [!IMPORTANT]
> **This is a sketch, not a build hand-off.** An agent must not build from it. It exists so implementation-level material has somewhere to go while the design is argued; the design wins on behavior, and [`installed-launcher-provenance.md`](installed-launcher-provenance.md) is the design. No decision is made here — anything that chooses behavior is an `OQ-IP` question over there.

Anchors below were read at `d4d3ff0d2`. Re-read each before relying on it.

## Where the two builds are made

| Step | Where | What it does |
| :--- | :--- | :--- |
| Launcher build (gets provenance) | [`matched-bundle.py:206-208`](../../scripts/matched-bundle.py) | sets `SOURCE_DIR`/`SOURCE_BRANCH` for `build_kind == "installed"` only |
| Bundle build (loses it) | [`matched-bundle.py:219-220`](../../scripts/matched-bundle.py) | pops both from `stage_env` before `stage-source-bundle.sh` |
| Dispatch to `$GOBIN` | [`matched-bundle.py:472`](../../scripts/matched-bundle.py) | `_atomic_install_launcher(output/bin/yolo, gobin)` |
| Dispatch to the generation | [`matched-bundle.py:464`](../../scripts/matched-bundle.py) | `publish_install_generation(output/bin/yolo, output/share/yolo-jail, …)` |
| The ldflags themselves | [`build-go.sh:82-107`](../../scripts/build-go.sh) | four optional `-X` values; `SOURCE_DIR`/`SOURCE_BRANCH` are the last two |
| Install guard | [`matched-bundle.py:404-406`](../../scripts/matched-bundle.py) | `install` refuses inside a jail when `YOLO_VERSION` is set — **the install path cannot be exercised in-jail** |

## The invariant, and where an assertion would go (waits on [OQ-IP2](installed-launcher-provenance.md#OQ-IP2))

- The comparison to make is `sha256(output/bin/yolo)` against `sha256(output/share/yolo-jail/bin/linux-<host-arch>/yolo)`.
- Natural home: [`validate_pair`](../../scripts/matched-bundle.py) ([`matched-bundle.py:238-268`](../../scripts/matched-bundle.py)), which both install and development builds already call through [`validate_bundle`](../../scripts/matched-bundle.py).
- It compares identity only today: `_stamp_lines` ([`matched-bundle.py:230-235`](../../scripts/matched-bundle.py)) builds a set of two lines, and `validate_pair` checks those plus the JSON stamp. **No byte comparison exists anywhere.**
- Development bundles pass such an assertion today and can serve as its control case.

## The provenance record (waits on [OQ-IP1](installed-launcher-provenance.md#OQ-IP1))

Constraints that hold whichever option is ruled:

- It must not enter `share/yolo-jail`, which is both the shipping tree and what a jail mounts: the published generation's root **is** that tree's contents, so a file written there is readable inside the jail.
- It must be findable from a running binary, which knows only its own baked `SnapshotIdentity` and its own path — so the lookup key is the identity, not the path.
- A missing or mismatched record must degrade to the message that already exists for it ([`update.go:420-422`](../../internal/cli/update.go)).

Candidate locations, none chosen: host state beside the flake bundle (`paths.FlakeBundleDir`, [`paths.go`](../../internal/paths/paths.go)), a file beside `$GOBIN/yolo` written by the same atomic install, or a record in the existing install state. Weigh them by whether the jail can read them, and by what happens when a binary is copied out of its installation.

Read side to change: [`channel.go:232-237`](../../internal/selfupdate/channel.go) feeds `Detect`, whose precedence is set at [`channel.go:177-183`](../../internal/selfupdate/channel.go); [`update.go:229`](../../internal/cli/update.go) prints the checkout it resolved. Every consumer of the two stamps is in `internal/selfupdate` — verified by grep, nothing else reads them.

## Migration (waits on [OQ-IP3](installed-launcher-provenance.md#OQ-IP3))

Installs in the field carry baked provenance. Whatever the reader does with a value it can no longer see needs a stated answer before the writer stops setting it.

## Tests that touch this

| Test | What it pins |
| :--- | :--- |
| [`versionstamp_test.go:31-95`](../../integration/versionstamp_test.go) | the jail's version, commit, snapshot identity and build kind all match the selected bundle's stamp |
| [`versionstamp_test.go:63-71`](../../integration/versionstamp_test.go) | the host's reported digest equals its own executable's digest |
| [`versionstamp_test.go:72-75`](../../integration/versionstamp_test.go) and [`versionstamp_test.go:179-186`](../../integration/versionstamp_test.go) | the jail's reported digest equals the selected bundle's `bin/linux-<arch>/yolo` |
| [`channel_test.go:163-175`](../../internal/selfupdate/channel_test.go) | classification from `SourceDir`/`SourceBranch`/`GitCommit` |
| [`matched_bundle_test.go:85-92`](../../internal/reporoot/matched_bundle_test.go) | the launch-time identity check against a bundle stamp |
| [`identity_test.go`](../../internal/version/identity_test.go) | the `binary sha256:` line and its unavailable-variants |

## Documents to update when this lands

- [`self-update.md`](../reference/self-update.md) — the install-channel table and the source-check section, which state that a source install's apply runs in the checkout.
- [`version.go:31-42`](../../internal/version/version.go) — the `SourceDir`/`SourceBranch` comments claim the stamp is what lets a source install find its checkout, and that comment is the contract a reader trusts.
- [`matched-development-bundles.md`](../reference/matched-development-bundles.md) — its matched-artifact contract gains the one-build property if [OQ-IP2](installed-launcher-provenance.md#OQ-IP2) rules it checked.
- [`installed-launcher-provenance.md`](installed-launcher-provenance.md) — graduates to `docs/reference/` once built, per the usual hand-off.
- [`AGENTS.md`](../../AGENTS.md) — checked: it does not mention provenance stamps, so it needs nothing.

## Check before relying on any of this

- Whether `publish_install_generation` writes anything besides the copied `share/yolo-jail` contents into the generation root — the jail-visible set is what constrains the record's location.
- Whether any pack or capture path reads `yolo-build-stamp.json` and would need to tolerate a new sibling.
- That `validate_pair`'s failure text still names a next step for a byte mismatch; the Happy Path rule requires it ([`happy-path-principle.md`](../reference/happy-path-principle.md)).

## Gates when it is built

- `just format`, then `just check-ci`.
- `uvx vantage-check docs/` for the documents above.
- `go test -run TestTheJailsYoloCarriesItsMatchedBundleStamp ./integration` — the test that reads both sides of this.
- The install path needs a **host** run (`matched-bundle.py install` refuses in-jail), so the assertion's own verification cannot come from a nested jail.
