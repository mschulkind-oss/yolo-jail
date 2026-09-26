---
title: "Plan sketch: a pack gives pi a whole package"
date: 2026-09-25
status: draft
tags: [pi, packs, files, slots, config-list, plan]
summary: "Parking lot for build-level detail behind pack-pi-resources.md: where `register` and `expects` decode, where core emits the per-landing list contribution at each notch, what to verify on the host retire path and the macOS backends, and the order. Not a hand-off; the design wins on behavior."
---

# Plan sketch: a pack gives pi a whole package

**Status:** SKETCH, 2026-09-25 — incomplete, and unstable while [OQ-PR1](pack-pi-resources.md#OQ-PR1)
is open. Do not build from it.

**Precedence:** [`pack-pi-resources.md`](pack-pi-resources.md) wins on behavior. This file holds
settled detail the design does not need.

---

## Where things go

- **Decode.** `register` and `expects` are fields on a `files` destination in `internal/packdecl`
  (strict and tolerant decoders; refuse both on anything but a destination; `register.surface`
  must name a surface the same manifest owns; `entry` must contain `{landing}` and no other
  token). `validateFilesDestinations` is the neighbour to extend.
- **Emission.** The per-landing list contribution is produced where addressed `files` trees are
  resolved (destination borrowing, `packload.ResolveDestinations` / `SlotLanding`), and handed to
  the same fold `config-list` contributions take (`internal/agentcfg/listcontrib.go`), attributed to
  the contributing pack so the orphan and provenance reports name it. Check whether the list
  contributions are gathered per notch or once; both notches must see the same set
  (`filesslotparity_test.go` is the pattern to copy for a parity pin).
- **Advisory.** `expects` feeds `pack lint`, `pack footprint` and a `yolo check` WARN row; it never
  refuses.
- **packs/pi.** Add the slot with `register` and `expects`. Nothing else in the pack moves.

## Check before relying on it

- The host path for a dropped pack's addressed tree: does `yolo host apply` retire the files it
  wrote for a pack that left `packs`? The design is safe either way (an unregistered tree is
  inert), but the doc's behavior table should say which.
- macos-user and Apple Container delivery of an addressed `files` tree
  ([`settings-per-setup.md`](../../userguide/reference/settings-per-setup.md)).
- What pi does when two packages register the same command, tool or theme name, and whether one
  failing extension stops the others (the design marks both UNVERIFIED).
- That `~` in a user-scope `packages` entry resolves at the host exactly as in the jail
  (`resolvePath` with `homeDir`), on macOS too.

## Tests the build owes

- Decoding: accepted on a destination, refused on a contribution and on other kinds, bad
  pointer, missing or extra token.
- A pack addressing pi yields exactly one `packages` entry beside an overlay's list, at both
  notches; the user's in-jail `pi install` entries survive; dropping the pack removes the entry.
- No pack addressing pi leaves `settings.json` byte-identical.
- `expects` warns on a tree with none of the names, and says nothing on a well-formed one.
- Each call site pinned: delete the emission, the test goes red.

## Order

1. Decode `register` and `expects`.
2. Emission at both notches, with the parity test.
3. `packs/pi` declares the slot.
4. Human: migrate the maintainer's pack ([design §4](pack-pi-resources.md#4-the-maintainers-pack-before-and-after)).
