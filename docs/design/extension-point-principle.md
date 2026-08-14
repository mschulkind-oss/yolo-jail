# Principle: the framework author designs the extension point, not the first extender

**Audience:** anyone adding a mechanism that someone outside this repo will build on — a pack
manifest field, a loophole manifest field, a contribution kind, a config key, a hook name. Read this
before deciding that one use case is too few to design for.

**Sibling principles:** [`happy-path-principle.md`](happy-path-principle.md) (fill the matrix, don't
support every tool) and [`gate-placement-principle.md`](gate-placement-principle.md) (put the gate
where the authority changes). The first is about *breadth*, the second about *what not to build*;
this one is about *who does the designing*.

---

## The principle

> **When a mechanism will be extended by people outside this repo, design the extension point when
> you build the first instance — even though there is only one. The first outside author must not
> have to invent it.**

Not "build every field anyone might want." Design **the concept and its semantics** — the namespace,
the matching rule, the failure modes — and wire the one edge you need. Later edges are then
additive.

## Why

**A missing extension point does not stay missing.** The first outside author who needs it either
cannot do the thing, or invents a workaround. Workarounds become de facto API: harder to remove than
a designed mechanism, and they encode one person's special case as everyone's shape. You will
support it either way — the only choice is whether you designed it.

**One use case designs a mechanism that fits one use case.** The framework author has the whole
picture: every other kind, every other notch, the failure modes that bit last month. The first
outside author has one problem and no context. Whoever designs it is choosing which of those two
views the mechanism encodes.

**The costs are asymmetric.** Deferring an internal abstraction costs *us* a refactor. Deferring an
extension point costs *a user* a workaround — and costs us compatibility with that workaround
forever.

## The distinction from YAGNI — which is still right

"Do not abstract until the second consumer appears" is a good rule and this does not repeal it. It
applies to **implementation**, not to **extension points**:

| | Internal abstraction | Public extension point |
|---|---|---|
| Who are the callers? | Us. All of them. | Strangers, mostly unwritten. |
| Cost of getting it wrong | A refactor we can do | A compatibility surface we inherit |
| Does the second consumer teach you the shape? | **Yes** — wait for it | **No** — they are gone by the time you see their workaround |
| Rule | **Wait** | **Design now** |

**The test: who pays if I am wrong?** If the answer is *us*, wait for the second consumer. If it is
*someone we have never met*, design it now.

### A worked contrast, both from this repo, one week apart

- **[`loophole-transport.md`](loophole-transport.md) §6 — waiting was right.** The generic
  `loopback-tls` transport was deliberately *not* generalized before a second consumer. Correct,
  because a transport is **internal**: we own both consumers (the broker relay and
  `host-processes`), nobody outside writes one, and the second consumer taught us the shape for
  free.
- **[`pack-capabilities.md`](pack-capabilities.md) — waiting was wrong.** The same instinct said
  "one loophole, one pack, hardcode the name and move on." But a loophole manifest is a **public
  surface** — the whole point of the loophole framework is that people write their own. A
  hardcoded `supersedes: "claude-oauth-broker"` would have been the workaround we then supported
  forever, and it would have baked in the error §3 of that doc exists to prevent: naming the
  implementation instead of the job.

Same repo, same fortnight, opposite answers — and the discriminator is not "how many consumers" but
**"do I own all of them."**

## How to apply — six rules that fell out of the first instance

Each of these is a specific decision in [`pack-capabilities.md`](pack-capabilities.md); they
generalize.

1. **Name the job, not the thing that does it.** An extension point that references an
   implementation name breaks when the implementation is renamed or replaced, and it lets no
   alternative implementation participate. Reference the invariant.
2. **Silence means "not participating", never a default claim.** Adding the mechanism must not
   change the behavior of anything that has not opted in — including every third-party manifest
   already in the wild. Make the empty declaration inert by construction.
3. **A claim about yourself is cheap; a claim about another component is not.** Require
   justification for the second kind. A mandatory `because` string is not decoration if it is
   printed where the consequence lands.
4. **Anything that turns something off must name who did it and why.** A silent deactivation is
   indistinguishable from a bug, and the person debugging it is not the person who caused it.
5. **Refuse an unmatched reference at load, with a message that fixes it.** A closed namespace makes
   typos decidable. "Superseded nothing" is useless; "no loophole serves X — served: [...]" is a
   fix. This is the S1 lesson: **the message is most of the value.**
6. **Ship one edge, design the namespace.** The general thing is the concept and its semantics, not
   every possible relation. Later edges are additive — same namespace, same matching rule, no
   migration — which is what makes shipping one edge honest rather than premature.

## What this does not license

**It is not a mandate to generalize everything.** The question is only ever "is this surface
extended by people I do not control?" Most of yolo is not: the run pipeline, the render targets, the
prism's layer model, the transport. Those follow the ordinary rule — wait for the second consumer,
because we are the second consumer.

**It is not a licence to add fields nobody asked for.** [`pack-capabilities.md`](pack-capabilities.md)
§7 lists three things it deliberately does *not* build (pack-to-pack `serves`, `needs`, a central
registry) and gives a reason for each. Designing the extension point means settling the *semantics*
so later additions fit — not shipping the additions.

**It does not make the first instance special.** If designing the general mechanism cannot be
justified without the one use case, the use case is doing the arguing and the design will fit it too
closely. The test in [`pack-capabilities.md`](pack-capabilities.md) §3 — *would this survive the
implementation being replaced?* — is the one that catches that.
