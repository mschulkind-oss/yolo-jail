// Package packs embeds the OFFICIAL packs into every yolo binary, so an installed
// binary carries them without a repo checkout.
//
// These are "official" in exactly one sense that matters: their content ships with
// yolo and is reviewed with the release. That USED TO decide what they could declare —
// an embedded pack could name a host file and a fetched one could not — until OQ-TP9
// deleted the origin gate (docs/design/trust-paths.md, 2026-09-04). What remains is a
// provenance claim and a delivery route: nothing to fetch, no pin to record.
// Structurally they are ordinary packs, read through the same loader as a user's.
//
// The embed list is EXPLICIT so editor droppings or a stray __pycache__ never get baked
// into a release binary. The cost is that a NEW pack directory must be added below;
// TestEmbedMatchesTree fails the build the moment the tree and this directive drift, so
// the sync is test-enforced rather than convention-enforced — the same trade
// `bundled_loopholes/embed.go` made until that channel was retired.
//
// FLAKE TRAP: packs/ must also be listed in the goSrc fileset in flake.nix. The image
// build is hermetic and only sees the paths that fileset names, so a pack dir missing
// from it VANISHES from the image while `go build` stays green.
//
// NOT EVERY OFFICIAL PACK IS AN AGENT. Fifteen packs are embedded here (counted against
// `ls packs/` 2026-09-04): six install a CLI and nine do not, in four kinds. Five of
// those nine — `audio`, `host-processes`, `journal`, `cgroup-delegate` and `serial` —
// ship a LOOPHOLE (one of
// nineteen contribution kinds, a count pinned by `internal/packdecl/kinds_test.go`) —
// `audio` also contributes an `env` block, the only one of the five that ships anything
// beside its loophole — and they are the dogfood for docs/design/loophole-packaging.md
// §7 / OQ-LP11, whose prize is that "AGENTS ARE PACKS" becomes true of loopholes too.
// `zai` and `cerebras` ship neither CLI nor loophole: a provider and a profile —
// zai was the first pack whose whole content is declarative facts, cerebras the second
// (and the first to carry a `needs` entry — the wire-bridge it joins when claude or
// copilot is selected). `guardrails` ships neither either: blocked-tool refusals and install
// requirements (9caba669 moved the blocked tools out of core — core blocks nothing by
// default), the third kind of CLI-less pack. `wire-bridge` is the fourth kind and the
// first of it: a `kind: "service"` pack, one in-jail daemon and its endpoint file,
// no grants (docs/design/wire-bridge.md §2.1).
// Anything here that reasons about "the six agent packs" (a comment, a test's name
// list) is describing the agent SUBSET, not this list.
//
// AND THE CONVERSE: `claude` is an agent pack that ALSO ships a loophole
// (`loopholes/claude-oauth-broker`). So "agent pack" and "loophole pack" are not a
// partition of this list, and a test that assumed they were would miss the one pack in
// both sets.
//
// THIS EMBED IS THE ONLY CHANNEL LEFT. There were two — `bundled_loopholes/embed.go`
// carried the loophole manifests yolo shipped — and emptying it was the sprint's goal
// rather than a side effect (docs/design/broker-as-a-pack.md OQ-BP4). Five loopholes made
// the trip and they arrived by three different routes, which is worth remembering only
// because each route had a different way of going wrong:
//
//	host-processes, audio (2026-08-18)   reserved ONLY as bundled directory names, read
//	                                     off that embed.FS, so `git mv` retired the
//	                                     reservation for free.
//	journal, cgroup-delegate (2026-08-18) never bundled — BUILTIN SERVICES with hardcoded
//	                                     names in paths.BuiltinLoopholeNames, so each
//	                                     reservation had to be deleted BY HAND.
//	claude-oauth-broker (2026-08-19)     reserved BOTH ways at once, and it is a
//	                                     CONTRIBUTION OF `packs/claude` rather than a pack
//	                                     of its own (loophole-activation.md OQ-A10) —
//	                                     because the dependency is structural and a
//	                                     separate pack would reinstate a second selection
//	                                     step. Its move deleted the reserved namespace
//	                                     entirely, since it was the last name in it.
//
// A reservation left standing over a pack-shipped name is not a warning: the name
// pre-flight is fatal, so every launch that selects the pack fails.
package packs

import "embed"

//go:embed all:claude all:copilot all:opencode all:pi all:codex all:agy all:zai all:cerebras all:audio all:host-processes all:journal all:cgroup-delegate all:serial all:guardrails all:wire-bridge
var FS embed.FS
