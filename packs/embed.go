// Package packs embeds the OFFICIAL packs into every yolo binary, so an installed
// binary carries them without a repo checkout.
//
// These are "official" in exactly one sense that matters: their content ships with
// yolo and is reviewed with the release, so a declaration from one carries yolo's own
// authority (see config.PackEntry.MayGrantHostFiles — an embedded pack may name a host
// file, a fetched pack may not). Structurally they are ordinary packs, read through
// the same loader as a user's.
//
// The embed list is EXPLICIT so editor droppings or a stray __pycache__ never get baked
// into a release binary. The cost is that a NEW pack directory must be added below;
// TestEmbedMatchesTree fails the build the moment the tree and this directive drift, so
// the sync is test-enforced rather than convention-enforced — the same trade
// bundled_loopholes makes.
//
// FLAKE TRAP: packs/ must also be listed in the goSrc fileset in flake.nix. The image
// build is hermetic and only sees the paths that fileset names, so a pack dir missing
// from it VANISHES from the image while `go build` stays green.
//
// NOT EVERY OFFICIAL PACK IS AN AGENT. `audio`, `host-processes` and `journal` ship
// LOOPHOLES (the 15th contribution kind) and install no CLI at all — they are the dogfood
// for docs/design/loophole-packaging.md §7 / OQ-LP11, whose prize is that "AGENTS ARE
// PACKS" becomes true of loopholes too. Anything here that reasons about "the six agent
// packs" (a comment, a test's name list) is describing the agent SUBSET, not this list.
//
// TWO DIFFERENT ARRIVALS, and the difference is the thing to hold on to. `host-processes`
// and `audio` MOVED here from bundled_loopholes/ (2026-08-18): a name that leaves that
// directory also leaves the reserved set, because the reservation is read off that
// embed.FS, so `git mv` retired it for free. `journal` (2026-08-18) was never bundled at
// all — it was a BUILTIN SERVICE with a hardcoded name in paths.BuiltinLoopholeNames and a
// top-level config key of its own, so its reservation had to be deleted BY HAND in the
// same commit. A reservation left standing over a pack-shipped name is not a warning: the
// name pre-flight is fatal, so every launch that selects the pack fails.
package packs

import "embed"

//go:embed all:claude all:copilot all:opencode all:pi all:codex all:agy all:audio all:host-processes all:journal
var FS embed.FS
