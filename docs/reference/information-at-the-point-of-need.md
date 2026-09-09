---
status: current
verified: 2026-09-09
verified_commit: 356bcec8
covers:
  - packs/guardrails/
  - internal/cli/run/backendlimits.go
  - internal/jailcontent/briefing.go
tags: [principle, briefings, packs, ux, diagnostics]
---

# Give information where it is needed, not where it is easy to put

**Status:** PRINCIPLE, current as of 2026-09-09, verified against `356bcec8`.

**Audience:** anyone about to add a paragraph to a briefing, an `AGENTS.md`, or any other text an
agent reads before it starts work. Read this before writing prose *about* a mechanism.

A principle keeps its rationale on purpose. The rule alone ("don't put it in the briefing") reads as
a length budget, which is the misreading this page spends most of its words preventing.

**Sibling principles:** [`gate-placement-principle.md`](gate-placement-principle.md) (where a gate
earns its place), [`extension-point-principle.md`](extension-point-principle.md) (who designs an
extension point), [`happy-path-principle.md`](happy-path-principle.md) (fill the matrix).

**Reads with:** [`agent-briefings.md`](agent-briefings.md) — how a briefing is composed and
delivered, which is the mechanism this principle rations.

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

### Where the test is applied in code

`internal/cli/run`'s `backendlimits.go` is the principle used as a filter rather than quoted as a
maxim, and it is worth reading as the worked case of *both* answers.

What it puts in the briefing: the facts a backend makes false without saying so — that the agent's
config file reflects the user's, that its home is its own, that a skill it edits stays edited. Each
is false on some backend, silently, and **none has a moment of use to attach the correction to**.
The human is told at launch, on stderr, where the agent never reads.

What it deliberately leaves out, by the same test: settings that are read-and-ignored on a backend
and warned about, which condition nothing the agent does — it never asked for a memory cap — and an
absent MCP preset, which shows up as a server simply not in its config. Those stay human-only.

## What about the reasoning?

Reasons are for the person changing the rule, not the agent obeying it. They belong
where that person will look: a `README.md` at the pack root (which is not a briefing
source — only `AGENTS.md` is), a design doc, or a code comment beside the rule.

`packs/guardrails/README.md` is the worked example, and it says so about itself in its second
paragraph: the blocker prints *what* to use instead at the moment it refuses, and the README says
*why the rule exists* and *why `grep` is only half-blocked*. Neither reaches the jail's briefing, and
the agent that hits the block still gets what it needs, when it needs it.

## What this does NOT say

Not "briefings should be short." A briefing should be exactly as long as the things
an agent must know before acting, which is sometimes long. The claim is about the
*criterion*, not the length: fit for the bucket is not a reason to use the bucket.

> [!WARNING]
> **A `README.md` at a pack root is not a briefing source, and that is load-bearing rather than
> incidental.** Only `AGENTS.md` is read into a jail's briefing
> ([`agent-briefings.md`](agent-briefings.md)). So a pack author who wants the reasoning recorded
> without paying for it every session already has the right file — and one who moves that prose into
> `AGENTS.md` "so it is documented" has quietly re-created the failure above.
