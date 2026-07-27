// Package agents holds the jail CONTENT that agents read: the briefing, the staged skills
// tree, the loophole descriptions, and the yolo-source-tree probe that gates a couple of
// them.
//
// It used to be "the single source of truth for the coding agents yolo-jail can install" —
// a registry of six AgentSpecs. That registry is GONE; see below.
package agents

// The AGENT REGISTRY that used to live here is GONE.
//
// It was a []AgentSpec of six entries — install spec, briefing paths, skills dir, overlay
// dirs, host files, yolo flags, mise-retire tokens — plus the derived unions and lookups
// every subsystem read. All of it is now pack DATA (internal/packdecl, packs/*/pack.json),
// read through internal/packload. Core does not know what an "agent" is.
//
// What remains in this package is the stuff that was never per-agent in the first place:
// skills staging, briefing composition, loophole descriptions, and the source-tree probe.
// Those are named for agents because they produce content agents read, not because they
// switch on which agent is present.
