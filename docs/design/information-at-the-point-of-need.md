---
title: "Give Information Where It Is Needed, Not Where It Is Easy to Put"
date: 2026-09-04
status: accepted
tags: [briefings, principle, packs, ux]
summary: "The briefing is a bucket everything fits in, which is what makes it the wrong default home for anything. Information that a mechanism can deliver at the moment of use belongs there instead — a refusal explains itself when it refuses, a config error names its own fix — and the briefing keeps only what an agent must know BEFORE it acts."
---

# Give information where it is needed, not where it is easy to put

**Status:** ACCEPTED, 2026-09-04. Written after a pack shipped an `AGENTS.md`
explaining a rule that already explains itself at the moment it fires.

**Sibling principles:** [`gate-placement-principle.md`](gate-placement-principle.md)
(where a gate earns its place), [`extension-point-principle.md`](extension-point-principle.md)
(who designs an extension point), [`happy-path-principle.md`](happy-path-principle.md).

---

## The principle

> **If a mechanism can deliver a fact at the moment the fact is needed, that is where
> it goes. The briefing is for what must be known BEFORE acting — not for everything
> true about the jail.**

## Why the briefing is the tempting wrong answer

It is a bucket, and everything fits in a bucket. Prose about any subject can be
appended to an `AGENTS.md` and it will be *read*, so it always looks like it worked.

Three things go wrong, none of them visible at the moment you add the paragraph:

1. **It is paid on every session, and used on almost none.** A briefing is read in
   full, every launch, by every agent. A paragraph about the search-tool policy costs
   every session and matters only in the sessions that hit the policy.
2. **It competes with what is actually load-bearing.** A briefing people skim is
   worse than a short one they read, and every added paragraph makes skimming more
   rational. Padding it degrades the parts that had to be there.
3. **It goes stale silently.** Prose describing a mechanism drifts from the
   mechanism, and nothing fails when it does. A message emitted BY the mechanism
   cannot drift from it — it is the same code.

## The test

Ask: **at the moment this matters, is something already talking to the user?**

If yes, that is where the information goes. A blocked tool prints its own refusal and
its own alternative. A config error names its own fix. A launch that cannot share a
workspace prints the command that shares it. In each case the mechanism is already
speaking at exactly the right moment, to exactly the person who needs it, with the
specifics in hand — and the briefing would be a worse copy, read earlier, by someone
who does not yet have the problem.

If no — the agent must know it before acting, and nothing will tell it in time — the
briefing is right. "This workspace is a live host mount, so edits are visible
immediately" has no moment of use to attach to; it conditions everything.

## What about the reasoning?

Reasons are for the person changing the rule, not the agent obeying it. They belong
where that person will look: a `README.md` at the pack root (which is not a briefing
source — only `AGENTS.md` is), a design doc, or a code comment beside the rule.

`packs/guardrails/README.md` is the worked example: the refusal message says *what*
to use instead, and the README says *why the rule exists and why grep is only
half-blocked*. Neither is in the jail's briefing, and the agent that hits the block
still gets what it needs, when it needs it.

## What this does NOT say

Not "briefings should be short." A briefing should be exactly as long as the things
an agent must know before acting, which is sometimes long. The claim is about the
*criterion*, not the length: fit for the bucket is not a reason to use the bucket.
