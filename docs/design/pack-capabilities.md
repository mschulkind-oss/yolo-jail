# Capabilities — naming the job, so a pack can say a loophole is unnecessary

**Status:** DESIGN, 2026-08-13. Not built. Queue row **A6**.

**Why this is a design doc and not three lines in a queue row.** The immediate need is one pack
turning off one loophole ([`../plans/outstanding-work.md`](../plans/outstanding-work.md) A6:
selecting Bedrock makes the OAuth broker pointless, and on macOS it leaves a known-broken TLS stack
starting for nothing). That could be solved with a hardcoded name in an afternoon. It is being
designed instead because **loopholes and packs are extension points other people build on**, and
the first outside author to want this must not have to invent it — see
[`extension-point-principle.md`](extension-point-principle.md), which this doc is the worked
example for.

**Reads with:** [`pack-system.md`](pack-system.md) (the manifest and its kinds),
[`loophole-protocol.md`](loophole-protocol.md) and [`../guides/loopholes.md`](../guides/loopholes.md)
(what a loophole is), [`extension-point-principle.md`](extension-point-principle.md) (why design it
now).

---

## 1. The concept

A **capability is a named job** — not a name for the thing that does the job.

`claude-oauth-refresh` is *"serializing OAuth token refreshes so concurrent consumers do not burn a
single-use refresh token."* The `claude-oauth-broker` loophole is one implementation of it. A
different implementation would serve the same capability.

That distinction is the whole design, and §3 is why.

## 2. The vocabulary — two verbs

```jsonc
// bundled_loopholes/claude-oauth-broker/manifest.jsonc — a statement about ITSELF
"serves": ["claude-oauth-refresh"]

// a claude-bedrock pack — a claim about SOMETHING ELSE, so it costs more to make
"supersedes": [
  { "capability": "claude-oauth-refresh",
    "because": "Bedrock overrides the OAuth path entirely; no token is ever refreshed" }
]
```

**`serves` is a bare string list; `supersedes` requires an object with `because`.** The asymmetry is
deliberate and is a principle in its own right: **a claim about yourself is cheap; a claim about
another component is not.** Saying "this is my job" needs no justification. Saying "your job does
not need doing" is an assertion about code you did not write, and the person who later finds their
loophole silently absent deserves a sentence explaining why.

## 3. Why a capability and not the loophole's name

`"supersedes": ["claude-oauth-broker"]` would work today and be wrong tomorrow:

- it couples the pack to **one implementation**, so a replacement loophole doing the identical job
  is not superseded and starts running again;
- **renaming the loophole silently breaks every pack** that named it;
- it says *"turn that thing off"* where the true statement is *"that job does not need doing"* —
  and only the second survives the implementation changing underneath it.

The capability is the **invariant**; the loophole is the **implementation**. A pack should only ever
have to know the invariant.

## 4. The rule

`Loophole.Active()` (`internal/loopholes/loopholes.go:219`) gains a third gate beside the two it
already has:

```
Active()     = Enabled && RequirementsMet() && !Superseded()
Superseded() = serves is NON-EMPTY  AND  every served capability is superseded by some selected pack
```

Two choices worth stating, because the opposite of each is a plausible bug:

- **`every`, not `any`.** A loophole serving two jobs, one of them superseded, still has a job.
- **Non-empty `serves` is required.** A loophole that declares nothing can never be superseded.
  **Silence means "not participating", never a default claim** — so adding this mechanism cannot
  change the behavior of any manifest that does not opt in, including every third-party loophole
  that exists today.

## 5. The namespace — and it INVERTS the skills rule

**Two declarations naming the same capability string is the mechanism working, not a collision.**
A capability name is a **rendezvous point**: `serves` and `supersedes` find each other precisely by
matching on it.

This is worth saying loudly because the repo just spent a batch making name collisions **fatal** for
skills (S1: two packs claiming one skill name refuses the whole apply). Someone will reach for that
rule here by analogy and be wrong. The difference: a skill name is an **identity** — two things
claiming it means one is lost. A capability name is an **interface** — two things naming it is how
they connect.

Bare strings, no namespacing prefix. A prefix would only help if capability names collided by
accident across unrelated authors, and §6.1 makes an unmatched name a load error, so an accidental
collision surfaces immediately rather than silently.

## 6. Failure modes, designed out

### 6.1 A typo supersedes nothing, silently

`"capability": "claude-oauth-refersh"` matches no `serves`, so nothing is superseded and the pack
author believes it worked.

**A supersession matching no served capability is REFUSED AT LOAD**, naming the unmatched string and
listing the capabilities that *are* served by the active loophole set. The namespace is closed by
the loopholes present, so this is decidable rather than a guess.

This is S1's lesson applied where it does fit: **the message is most of the value.** "superseded
nothing" is useless; "no loophole serves `claude-oauth-refersh` — did you mean
`claude-oauth-refresh`? served: [claude-oauth-refresh]" is a fix.

### 6.2 An over-broad claim

A pack supersedes a capability it does not actually replace, and the user loses a loophole they
needed. Mitigated by the mandatory `because` (§2), which is not decoration: it is **printed wherever
the supersession takes effect**, so the justification travels with the consequence.

### 6.3 Something is off and nobody can tell why

**Anything that turns something off must name who did it and why.** `yolo loopholes list` already
prints active/inactive per loophole; an inactive-by-supersession loophole must read:

```
inactive  claude-oauth-broker  (bundled/loopback-tls/spawned)
    superseded by pack `claude-bedrock` — claude-oauth-refresh
    "Bedrock overrides the OAuth path entirely; no token is ever refreshed"
```

`yolo doctor` and `yolo pack footprint` should carry the same line — footprint especially, since
"what does selecting this pack do to me?" is exactly the question it answers.

### 6.4 Two packs disagree

Pack A supersedes `X`; pack B implicitly relies on the loophole serving `X`.

**v1 is one-directional: any supersession wins, and there is deliberately no `needs`.** Reliance on
a loophole is already implicit today — nothing declares it — so adding a counter-claim would invent
a conflict-resolution problem before anybody has the conflict. The mitigation is §6.3: the
supersession is *visible*, with a reason, so the collision is diagnosable in one command.

**Recorded as a known limit**, not an oversight. If it ever bites, `needs` is additive (§7).

## 7. What is deliberately NOT built — and why the namespace still generalizes

| Not built | Why |
|---|---|
| **`serves` on a *pack*** (pack-to-pack provision) | This is the A1–A2 case, retired 2026-08-13 once the two auth "modes" turned out not to be peers. No demonstrated need. |
| **`needs: [<capability>]`** | §6.4 — invents conflict resolution before the conflict exists. |
| **A yolo-owned registry of capability names** | Core deliberately does not know what an agent is (`internal/packdecl`'s opening premise). A central registry would rebuild the agent registry the pack system exists to avoid. Capabilities are declared by whoever holds the fact. |

**The general thing being shipped is the NAMESPACE AND ITS SEMANTICS, not every edge.** That is what
makes shipping one edge honest rather than premature: a later `serves` on packs, or a `needs`, is
**purely additive** — same namespace, same matching rule, same load-time check, no migration for
anything already written. An outside author who names a capability today is naming it in the system
that will still be there.

## 8. The first-party instance

```jsonc
// bundled_loopholes/claude-oauth-broker/manifest.jsonc
{
  "name": "claude-oauth-broker",
  "requires": { "command_on_path": "claude" },
  "serves": ["claude-oauth-refresh"],
  ...
}
```

```jsonc
// the claude-bedrock pack
{
  "name": "claude-bedrock",
  "supersedes": [
    { "capability": "claude-oauth-refresh",
      "because": "Bedrock overrides the OAuth path entirely; no token is ever refreshed" }
  ],
  "contributes": [ /* config-overlay: CLAUDE_CODE_USE_BEDROCK, AWS_REGION, model IDs */ ]
}
```

**What this buys beyond turning off a daemon:** under Bedrock the in-jail terminator currently still
binds `127.0.0.1:443` and sets up TLS interception — and on macOS + podman **that is the stack
[#31](https://github.com/mschulkind-oss/yolo-jail/issues/31) breaks**. Superseding removes a
known-broken failure surface, not three idle processes.

## 9. Acceptance

1. A loophole with no `serves` behaves exactly as today — byte-identical, including every
   third-party manifest. Pinned by a test.
2. `serves` + a matching `supersedes` from a selected pack ⇒ inactive, and `loopholes list` prints
   the pack, the capability and the `because`.
3. `supersedes` naming an unserved capability ⇒ **load error** naming the string and listing what is
   served.
4. `supersedes` without `because` ⇒ manifest validation error.
5. A loophole serving two capabilities with only one superseded ⇒ still active.
6. The `Enabled` config knob and `RequirementsMet()` are unchanged and independent — three gates,
   any of which can deactivate.

## 10. Open question

**OQ-CAP.** `supersedes` currently lives at the **manifest top level**, beside `name`, rather than
as a `contributes[]` entry. That matches `skills_tier` (a per-pack fact, not a contribution) and
reads correctly — superseding is a property *of the pack*, not a thing it delivers. The alternative
is a `kind: "supersedes"` contribution, which would put it in the same list as everything else a
pack does and make `yolo pack footprint` pick it up for free. **My read: top-level**, with footprint
taught to print it explicitly — a contribution that contributes nothing is a category error, and
`footprint` already reads the manifest.
