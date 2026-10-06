---
title: "Plan: a pack gives pi a whole package"
date: 2026-09-25
status: accepted
stage: BUILT
next: "A human check, since no test starts pi: select a pack whose pi folder holds an extension and a theme, start pi in a jail, and see both load; then migrate the maintainer's pack (pack-pi-resources.md §4)"
depends-on:
  - pack-pi-resources.md#OQ-PR1
tags: [pi, packs, files, slots, config-list, plan]
summary: "Built 2026-10-05: `register` and `expects` decode on a files slot, packs/pi declares the slot at .pi/agent/yolo-packs, packload computes one entry per landed tree through the matcher delivery uses, and packoverlay.Collect places it as a config-list entry of the contributing pack, so both notches list it and forget it with its pack. Verified by unit tests whose call sites were deleted to watch them fail, and by a nested jail. Left for a human: pi loading the folder."
---

# Plan: a pack gives pi a whole package

**Status:** built 2026-10-05, from [`pack-pi-resources.md`](pack-pi-resources.md) as ruled
([OQ-PR1](pack-pi-resources.md#OQ-PR1)). This file records where each part landed, what was
checked, and the one check left for a human. The design wins on behavior, and its
[Decision Ledger](pack-pi-resources.md#decision-ledger) holds the implementation decisions
(PR-D5 to PR-D9).

---

## Where each part landed

- **Decode.** `register` (`packdecl.FilesRegister`: `surface`, `path`, optional `entry`) and
  `expects` are fields of a `files` contribution, refused on anything but a slot, with
  config-list's pointer rules and one template token
  ([`filesregister.go`](../../internal/packdecl/filesregister.go)). The surface-ownership rule needs
  the pack's surfaces, so `packoverlay.Collect` checks it ([PR-D7](pack-pi-resources.md#decision-ledger)).
- **Emission.** `packload.Registrations` turns each tree landing in a registering slot into one
  entry, through `matchedDestinations`, the matcher `borrowedDestinations` now shares
  ([`registration.go`](../../internal/packload/registration.go),
  [`mergedest.go`](../../internal/packload/mergedest.go)). `packoverlay.Collect` places each entry
  as a config-list entry of the contributing pack, ahead of that pack's own lists, so the jail
  boot, `yolo host apply`, `yolo config render` and `yolo config ls` see the same set with no call
  of their own ([PR-D6](pack-pi-resources.md#decision-ledger)).
- **Advisory.** `packload.ExpectsNotes` feeds a warning in `yolo pack lint`, `yolo pack footprint`
  and `yolo check` (`printExpectsNotes` in `internal/cli/pack.go`, and `sectionPacks`).
- **packs/pi.** The slot at `.pi/agent/yolo-packs`, registering into `pi/settings` `/packages`, with
  `expects`. Nothing else in the pack moved. Its footprint line names the list.
- **`yolo features`** lists `registered-files-slots` ([PR-D9](pack-pi-resources.md#decision-ledger)).
- **Patched extensions in the slot.** [`pack-conventions.md`](pack-conventions.md)'s
  [OQ-PC3](pack-conventions.md#OQ-PC3) may add patched extensions written directly into the slot;
  the emission stays one loop over landings with one predicate so that can join it.

## What was checked before relying on it

- **The host path for a dropped pack's tree.** `yolo host apply --assert` retires it with the rest
  of the pack's `files` output, archived, behind the one dropped-pack confirmation
  (`pruneDroppedPackOutput`, which reads the ownership record the files render writes). `--revert`
  removes the entry and leaves the tree, which nothing lists. Read from the code; the entry half is
  under test.
- **macos-user and Apple Container.** The tree takes the existing `files` path on each, and the
  entry the one surface loop. Read from the code (`packFilesMountArgs`, `buildMacosHomeOverlay`);
  neither backend was run.
- **`~` at both notches.** The entry the tests read is `~/.pi/agent/yolo-packs/<pack>` at both. That
  pi expands it is pi's `resolvePath`, read from source, not run.
- **What pi does with two packs registering one command, tool or theme name, or with one failing
  extension.** Not checked: both need pi to run, and stay UNVERIFIED in the design.

## The tests

The jail and host render tests were run failing before the emission existed. Each call site was
then deleted in turn to watch its tests go red: the placement loop in `Collect`, the slot in
`packs/pi/pack.json`, the `filesSlotProblems` call in `validateContribution`, and the
`ExpectsNotes` calls in `pack lint`, `pack footprint` and `yolo check`.

| What | Where |
| :--- | :--- |
| Decoding: accepted on a slot, refused elsewhere, pointer, missing and unknown token | `internal/packdecl/filesregister_test.go` |
| One entry per landed tree, in pack order, over a raw and a resolved set, at the landing delivery uses | `internal/packload/registration_test.go` |
| Placement as the contributing pack's config-list entry; a slot naming another pack's surface is a problem | `internal/packoverlay/registration_test.go` |
| The jail render lists the folder; a `pi install` survives; dropping the pack removes the entry; no addressing pack leaves `settings.json` byte-identical | `internal/entrypoint/pifolderregistration_test.go` |
| `yolo host apply` lists the folder beside your own entry under both ownerships, a drop removes it, `--revert` removes it | same file |
| The shipped pi pack binds the folder `:ro` at the landing it lists, and the host writes it there | `internal/cli/run/pifolderslot_test.go` |
| `expects`: `pack lint`, `pack footprint` and `yolo check` warn on a misshapen folder and are silent on a good one | `internal/cli/packexpects_test.go`, `internal/cli/check/packs_test.go` |
| A pack named `my_pack` is listed where the launch mounts it, through the real stager and the boot's loader; a folder a filter dropped is not listed | `internal/cli/run/pifolderslot_test.go` |
| A tree with no source registers nothing; a jail-loaded pack lands under the launch's name | `internal/packload/registration_test.go` |
| `pack lint` notes a folder's `skills/`, and is silent on a folder without one | `internal/cli/packexpects_test.go` |
| A slot's bad `register` is refused at `yolo host apply` as `files`, and the verdict says so | `internal/cli/hostapplystagefailure_test.go` |

## Fixed in review

Review of the build on 2026-10-05 found four defects, each fixed with a test that failed first:

- **A pack whose name a staged directory escapes was listed where nothing was mounted.** The jail
  named the landing after the staged directory (`my_5fpack`) while the launch mounted the folder
  under the name in `packs` (`my_pack`). The jail now reads the launch's name from the staged
  tree's record ([PR-D10](pack-pi-resources.md#decision-ledger)).
- **A folder that was not delivered was still listed**, for a `from` naming nothing or a folder an
  only/exclude filter dropped ([PR-D11](pack-pi-resources.md#decision-ledger)).
- **`pack lint` did not note a folder's `skills/`**, which [§3.4](pack-pi-resources.md#34-behavior-in-every-case)
  rules ([PR-D12](pack-pi-resources.md#decision-ledger)).
- **`yolo host apply` refused a slot's bad `register` as `config-overlay`**, a declaration the
  author never wrote. It now leads with `files`, in the line and in the verdict.

## The nested jail

Run 2026-10-05 from a throwaway workspace with a scratch home under `$YOLO_DURABLE_DIR`, the
freshly built `yolo` by path, and `YOLO_REPO_ROOT` at the build's worktree; `packs` was `pi` and a
pack whose one entry addresses pi with `files/pi` (an extension and a theme). The launch printed
`pi/settings: config-list entries from <pack>`, and in the jail:

- `~/.pi/agent/settings.json` held `"packages": ["~/.pi/agent/yolo-packs/<pack>"]`;
- `~/.pi/agent/yolo-packs/<pack>/` held `extensions/` and `themes/`, mounted `ro`, and a write
  there failed with `Read-only file system`.

A second launch with the pack dropped from `packs` left `packages` out of `settings.json` and
mounted nothing at the slot. pi was never started.

## Left for a human

1. Start pi in such a jail and see the folder's extension and theme load.
2. Migrate the maintainer's pack ([design §4](pack-pi-resources.md#4-the-maintainers-pack-before-and-after)).
