// Package jailcontent holds the jail CONTENT an agent reads once it is inside: the
// composed briefing, the staged skills tree, the loophole descriptions that go into the
// briefing, and the yolo-source-tree probe that gates a couple of them.
//
// THE NAME IS THE POINT, and it took two goes to get right. This package was
// `internal/agents`, and it was accurately named while it held the AGENT REGISTRY — a
// []AgentSpec of six entries (install spec, briefing paths, skills dir, overlay dirs, host
// files, yolo flags, mise-retire tokens) plus the derived unions every subsystem read. That
// registry is GONE: it is pack DATA now (internal/packdecl, packs/*/pack.json), read through
// internal/packload, and core does not know what an "agent" is. What was left behind is the
// stuff that was never per-agent in the first place, and it kept a name that told the next
// reader to look here for agent knowledge that is not here — the one thing a package name
// costs nothing to get right and everything to get wrong (pack-code-separation.md §6).
//
// So the name now says the OUTPUT, not the audience: content destined for the jail. Its
// host-notch counterpart is internal/hostskills, which composes the same kind of thing into
// the user's real home; the two are deliberately separate packages because a jail's
// destination is a staging dir yolo owns outright and a host's is the user's own tree.
//
// Nothing in here switches on which agent is present. Every destination arrives as a pack
// declaration (SetPackSkillTargets, the briefing contributions the CLI walks), so a jail with
// no packs stages nothing rather than inventing a target.
package jailcontent
