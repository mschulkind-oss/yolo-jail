---
title: "Forked programs as packs — implementation sketch"
date: 2026-09-21
status: draft
tags: [plan, sketch, packs, programs, capture, forks]
summary: "Parking lot for the forked-program delivery route: the declaration surface, what capture already gives for free, the relocation unknowns, and test ideas. No design decision lives here — the design is forked-programs-as-packs.md and it wins on behavior."
---

# Forked programs as packs — implementation sketch

**Status:** SKETCH, 2026-09-21 — incomplete, and unstable while questions are open.

**Design:** [`forked-programs-as-packs.md`](forked-programs-as-packs.md). **Precedence:** the
design wins on behavior; the tree wins on fact; this file is advice and is the first thing here
to be wrong.

**Reads with:** [`program-delivery.md`](program-delivery.md) (the `via` routes),
[`install-capture.md`](../plans/install-capture.md) (capture as built).

---

## What this file is

A parking lot for material that surfaced while writing the design and is worth keeping but
needs no ruling. **No design decision is made here.** An entry resting on an unruled question
carries the link and waits.

## What capture already gives, unchanged

Worth listing because it is most of the feature, and the temptation will be to rebuild it:

- the store layout, admission, and the `.yolo-capture-complete` marker that makes a half-written
  entry detectable ([`capture/store.go`](../../internal/capture/store.go));
- the manifest, including `AbsoluteRef` recording, which is the input to any relocation
  decision ([`capture/manifest.go`](../../internal/capture/manifest.go));
- the reflink → hardlink → copy ladder and the `FICLONE` primitive
  ([`capture/materialize.go`](../../internal/capture/materialize.go),
  [`clone_linux.go`](../../internal/capture/clone_linux.go));
- GC (`PruneSupersededCaptures`) and the selection rule it complements;
- the throwaway capture jail, and its deliberate **absence** of a capture-store mount
  ([`capturehost.go:313`](../../internal/cli/capturehost.go)) — the circularity note there
  applies verbatim to a build.

## Declaration surface, sketched

Shape only; the field names are the implementer's and the `via` spelling is
[OQ-FP3](forked-programs-as-packs.md#14-decision-ledger)'s.

```jsonc
{
  "kind": "program",
  "bin": "pi",
  "via": "source",            // the third route; spelling is OQ-FP3
  "source": { /* a packsrc.Addr — git remote + ref */ },
  "build": "npm ci && npm run build",
  "produces": ["bin/pi"]      // what a successful build must have written
}
```

- `produces` exists so a build that exits 0 and writes nothing is a *failed* build rather than
  an empty capture. The design names that failure mode; this is the mechanism.
- `install_hints`, `protocols` and `flags` are unchanged — a forked program is still a program.
- Blocked on [OQ-FP5](forked-programs-as-packs.md#14-decision-ledger) for whether `bin` may collide with a
  shipped pack's.

## Pinning

Reuse `packsrc`'s vocabulary rather than inventing a second one: `Addr` for the address, a lock
entry for the resolved revision. Open: whether fork revisions live in `packs.lock.json` beside
pack sources or in a sibling lock. Both are defensible; it is below the design's altitude and
the implementer should pick after reading [`internal/packsrc/lock.go`](../../internal/packsrc/lock.go).

## Relocation unknowns to measure before building step 3

The design's [§5](forked-programs-as-packs.md#5-relocation-is-the-design-not-the-build) decides
strategy; these are the facts nobody has:

- Which absolute refs a real source build actually embeds — `RUNPATH`, interpreter lines,
  `__FILE__`-style debug paths, embedded prefixes. The manifest's `AbsoluteRef` scan will say,
  and running it against one real fork is cheap.
- Whether `relocate.go`'s text/binary classification declines the binary cases (expected) and
  what fraction of a real artifact that is.
- Whether a Node-based fork (the motivating case) embeds anything at all — a JS tree may be
  entirely relocatable, which would make [OQ-FP1](forked-programs-as-packs.md#14-decision-ledger) much
  cheaper than the general case suggests.

⚠ That last one is worth measuring **first**: if the motivating fork is relocatable, the design's
leaning may be over-cautious for the case that prompted it.

## Test ideas

- A fixture fork whose build writes a known absolute path, so the `AbsoluteRef` scan has
  something to find.
- A build that exits 0 and produces nothing → must be a failed build, not an empty entry.
- Two concurrent builds of one key → one entry, the loser adopts.
- An entry built for one notch, asked for by another → refused by name, per the design's
  failure table.
- The capture jail receives no `env_sources`, no `host_files`, no loophole. This is a property
  worth pinning rather than assuming, because the capture jail is constructed by the same
  pipeline as an ordinary launch.

## Things to check before relying on them

- `packsrc.Addr` currently addresses *packs*. Whether it accepts a bare git remote for a
  non-pack source without contortion is unverified.
- The `via` enum is a closed two-element table
  ([`contributes.go:496-497`](../../internal/packdecl/contributes.go)); adding a third touches
  the validator, the install-kind mapping, and whatever enumerates routes for the disclosure
  banner. Find all three before editing one.
- Auto-capture's trigger enumerates `via: "installer"`
  ([`run/autocapture.go`](../../internal/cli/run/autocapture.go)). Blocked on
  [OQ-FP4](forked-programs-as-packs.md#14-decision-ledger) for whether a source route joins it.
