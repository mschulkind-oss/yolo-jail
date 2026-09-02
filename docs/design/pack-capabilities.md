# Capabilities — naming the job, so a pack can say a bundled loophole is unnecessary

**Status:** **LANDED 2026-08-15**, re-checked 2026-08-23; **no open questions since 2026-09-02** —
OQ-CAP (§10) was a confirmation rather than a fork, and it is now recorded as settled by the
implementation that had already shipped its leaning (see §10). Designed 2026-08-13, rewritten the
same day against [`loophole-packaging.md`](loophole-packaging.md), which is its prerequisite.

*(This line used to cite "queue row **A6**"; the roadmap's lettered queue was retired on 2026-08-17,
and the ruling that letter pointed at is [`loophole-activation.md`](loophole-activation.md)
**OQ-A6**. Two more places below still say "roadmap A6" and "three bundled loopholes exist" — both
were true when written; there are **no bundled loopholes left** as of 2026-08-19, which narrows
supersession's remaining scope to loopholes a pack's own selection cannot remove.)*

> ### LANDED — what shipped, and where this document is now wrong
>
> `serves` is `internal/loopholedecl` (capabilities.go); `supersedes` is `internal/packdecl`
> (supersedes.go, a top-level key per OQ-CAP); the gate and the report are
> `internal/loopholes/supersede.go`. `bundled_loopholes/claude-oauth-broker` declares
> `serves: ["claude-oauth-refresh"]`, which on its own changes nothing. No `claude-bedrock`
> pack was built (OQ-6 is a maintainer call).
>
> **Four corrections, in the order they matter:**
>
> 1. **§4 calls supersession "a third gate" — it is the FOURTH.** `Active()` was
>    `Enabled && SupportedHere() && RequirementsMet()` before this change, not
>    `Enabled && RequirementsMet()`; the `platforms` declaration had already landed. The
>    shipped order is `Enabled && !Superseded() && SupportedHere() && RequirementsMet()`,
>    with supersession SECOND rather than last: it is a field read (cheapest), and it belongs
>    beside `Enabled` because both are decisions a user's configuration made, where the two
>    below are facts about the machine. `InactiveReason()` branches in the same order, which
>    is what stops the gate and the explanation from disagreeing.
> 2. **§5's "REFUSED AT LOAD" holds for the STRUCTURE and not for the MATCH.** An empty
>    `capability`, a missing `because`, a duplicate, a control character: all refused at load,
>    in `packdecl`, on both the strict and tolerant paths, because every one is
>    version-invariant. A capability matching no `serves` is REPORTED, loudly, with the
>    did-you-mean and the served list — not refused. Three reasons: the claim is decodable
>    long before the loopholes are (`pack lint` and the in-jail entrypoint have no loophole
>    set, and cannot get one — the `loopholes → config → packload` cycle); a pack superseding
>    a capability served only by a newer bundled manifest would brick every jail on a
>    pre-`just load` image, which is the `tier` incident a fourth time; and the failure
>    direction of a warning is safe — an unmatched claim leaves the loophole running, while a
>    refusal would take down `yolo loopholes list`, the command a user runs to find out what
>    happened.
> 3. **The footprint claim is DISPLAY-ONLY and deliberately NOT in `HostAccessClaims`.** §5
>    says `pack footprint` "carries the same line", and it does — as a `Claim` whose Kind is
>    the display label `supersedes`, deliberately absent from `packdecl`'s closed kind
>    registry, so two packs superseding one capability cannot be reported as a collision (§5's
>    own rule) and no per-kind exhaustiveness test acquires a non-contribution. It is not an
>    approval key: every string in that set GRANTS the pack something, while a supersession
>    relinquishes; keying an approval on `capability` alone would be content-blind, and
>    keying it on the `because` too would re-prompt on every reword — which, since
>    `promptYesNo` fails closed on a non-TTY, permanently refuses the pack.
>    `internal/packload/supersede.go` records the trigger that would reopen it.
> 4. **One wire is left for the run lane, and it is the only inert part.** `internal/loopholes`
>    takes the claims as data (`SetPackSupersessions` / `SetPackSupersessionResolver`,
>    mirroring `SetPackModules`) because it cannot import `packload`. The two calls that fill
>    the record live in `internal/cli/run/packs.go`, which this change did not own. Until they
>    land the record is empty — and empty is the SAFE direction: nothing is superseded and
>    every loophole behaves exactly as it does today.

> ### PREREQUISITE — read [`loophole-packaging.md`](loophole-packaging.md) first
>
> §9 below (OQ-CAP2) asked whether packs should be able to ship loopholes and recommended deciding
> that **before** building any of this. **The decision is made: (B), packs ship loopholes**, designed
> in that doc. **Selection is now the primary mechanism for "do not run that loophole" — you
> deselect the pack.**
>
> **This document was cut on that assumption**, and what remains is the residue that doc predicted:
> **supersession exists only for loopholes SELECTION CANNOT REMOVE — i.e. bundled ones, and in
> practice the one that auto-activates.** Concretely: the sections defending a public extension
> surface against hypothetical third-party collisions went from ~90 lines to ~15 (the
> "a pack cannot serve" argument, the namespace-inverts-the-skills-rule section, the over-broad-claim
> failure mode, and the two-packs-disagree section); the OQ-CAP2 fork went from a 96-line
> three-option decision to a resolution. What GREW is the statement of what is left, because that is
> the thing a reader now needs. §6 of `loophole-packaging.md` is the section-by-section record; this
> file is the live document.

**Why this is still a design doc and not three lines in a queue row.** The immediate need is one pack
turning off one loophole ([`../plans/roadmap.md`](../plans/roadmap.md) A6:
selecting Bedrock makes the OAuth broker pointless, and on macOS it leaves a known-broken TLS stack
starting for nothing). That could be solved with a hardcoded name in an afternoon. It is designed
instead because **a loophole manifest is a public surface** — see
[`extension-point-principle.md`](extension-point-principle.md), which this doc is the worked example
for. That argument survives the narrowing: `serves` is a field third parties will write even if only
bundled loopholes are ever superseded.

**Reads with:** [`loophole-packaging.md`](loophole-packaging.md) (**prerequisite**),
[`pack-system.md`](pack-system.md) (the manifest and its kinds),
[`loophole-protocol.md`](loophole-protocol.md) and [`../guides/loopholes.md`](../guides/loopholes.md)
(what a loophole is).

---

## 1. The concept, and its now-narrow scope

A **capability is a named job** — not a name for the thing that does the job.

`claude-oauth-refresh` is *"serializing OAuth token refreshes so concurrent consumers do not burn a
single-use refresh token."* The `claude-oauth-broker` loophole is one implementation of it. A
different implementation would serve the same capability.

**What this is now the answer to.** Not *"turn off a loophole"* — for a pack-shipped loophole that is
"deselect the pack". It is the answer to *"turn off a loophole that runs whether or not I asked for
it."* Three bundled loopholes exist and one of them auto-activates by design
(`claude-oauth-broker`, `requires: {command_on_path: "claude"}`), so a user with Claude Code
installed gets refresh serialization without knowing they need it. That default is deliberate and
worth keeping ([`loophole-packaging.md`](loophole-packaging.md) §5.4) — which is exactly why it needs
a way to say "not this time".

**What it is NOT the answer to, and this is the correction that matters.** Supersession does not
protect the broker's default from being *removed*; a workspace `yolo-jail.jsonc` can already set
`loopholes.claude-oauth-broker.enabled: false` and the broker vanishes with no message and a green
`yolo check` ([`loophole-packaging.md`](loophole-packaging.md) §4.3 G1, §5.4). That hole is closed by
**scoping `enabled`**, not by anything here. Supersession is the *considered* off switch; it is not a
guard against the blunt one.

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

**Supersede is NOT provide, and the difference is which way the demand goes.** The test: *after this
pack is selected, does the job still need doing?*

| Answer | Verb | What happened |
|---|---|---|
| **No.** The demand is gone. | `supersedes` | Under Bedrock **no OAuth token is ever refreshed**, so serializing refreshes is not a job being done differently — it is a job that no longer exists. |
| **Yes, and I do it now.** | *provision* — **not expressible today** | The demand persists; only the supplier changed. |

**Superseding when you meant providing silently stops the job being done, with nothing taking over.**
Nothing in the system can detect it, because "I will do it instead" is exactly the claim `supersedes`
does not make. So: **`supersedes` is a claim that DEMAND vanished, not that SUPPLY moved.**

**Granularity: always per job, never per component.** A loophole serving two capabilities with one
superseded stays active for the other (§3's `every` rule). Superseding *all* of them retires the
loophole — an arithmetic consequence of retiring each job, not a separate "retire this loophole"
power. There is deliberately no way to say "turn that component off": `enabled: false` already exists
for that and is honest about being a blunt instrument where this is a statement about work.

> **`serves` stays on the loophole manifest, and the reason CHANGED.** The old argument was that a
> pack is a bundle across a closed set of kinds *"and none of them is a daemon"*, so a pack has
> nothing to serve *with*. **The 15th kind falsifies that premise** — a pack can now ship an
> implementation. The conclusion survives for a better reason: **the implementation a pack ships has
> a manifest of its own, and a statement about an implementation belongs there.** So `serves` lives
> on the loophole manifest and travels *inside* the pack. Its old corollary — that pack-to-pack
> provision is "unexpressible" — is now false and is deleted (§6).

## 3. Why a capability and not the loophole's name

**This is the strongest argument in the doc and it survives the narrowing intact.**
`"supersedes": ["claude-oauth-broker"]` would work today and be wrong tomorrow:

- it couples the pack to **one implementation**, so a replacement loophole doing the identical job
  is not superseded and starts running again;
- **renaming the loophole silently breaks every pack** that named it;
- it says *"turn that thing off"* where the true statement is *"that job does not need doing"* —
  and only the second survives the implementation changing underneath it.

The capability is the **invariant**; the loophole is the **implementation**. A pack should only ever
have to know the invariant.

## 4. The rule

`Loophole.Active()` (`internal/loopholes/loopholes.go:232`) gains a third gate beside the two it
already has:

```
Active()     = Enabled && RequirementsMet() && !Superseded()
Superseded() = serves is NON-EMPTY  AND  every served capability is superseded by some selected pack
```

- **`every`, not `any`.** A loophole serving two jobs, one of them superseded, still has a job.
- **Non-empty `serves` is required.** **Silence means "not participating", never a default claim** —
  so adding this mechanism cannot change the behavior of any manifest that does not opt in.
- **`Superseded()` is only ever REACHABLE for a loophole selection cannot remove.** A pack-shipped
  loophole is deselected by deselecting its pack, so the gate is dead code for it. In practice that
  means: bundled loopholes, and a hand-placed user-dir one.

**On the namespace, briefly.** Two declarations naming the same capability string is the mechanism
working, not a collision — a capability name is an **interface** (a rendezvous point), where a skill
name is an **identity**. Bare strings, no prefix; §5 makes an unmatched name a load error, so an
accidental collision surfaces immediately. *(This was a full section arguing against reaching for the
skills-collision rule by analogy. At three bundled manifests that hazard is hypothetical, so it is
two sentences.)*

## 5. The two failure modes that are designed out

**A typo supersedes nothing, silently.** `"capability": "claude-oauth-refersh"` matches no `serves`,
so nothing is superseded and the author believes it worked. **A supersession matching no served
capability is REFUSED AT LOAD**, naming the unmatched string and listing what *is* served. The
namespace is closed by the loopholes present, so this is decidable rather than a guess. This is S1's
lesson applied where it fits: **the message is most of the value.** "Superseded nothing" is useless;
*"no loophole serves `claude-oauth-refersh` — did you mean `claude-oauth-refresh`? served:
[claude-oauth-refresh]"* is a fix.

**Something is off and nobody can tell why.** **Anything that turns something off must name who did
it and why.** `yolo loopholes list` already prints active/inactive per loophole; an
inactive-by-supersession loophole must read:

```
inactive  claude-oauth-broker  (bundled/loopback-tls/spawned)
    superseded by pack `claude-bedrock` — claude-oauth-refresh
    "Bedrock overrides the OAuth path entirely; no token is ever refreshed"
```

The mandatory `because` is not decoration: it is **printed wherever the supersession takes effect**,
so the justification travels with the consequence. `yolo doctor` and `yolo pack footprint` carry the
same line.

**This rule has gained two more authors, and both belong in the same code path:**

1. **"No pack ships it"** is now a reason a loophole is absent, so `loopholes list` must distinguish
   *superseded* from *not shipped* from *requirements unmet* — and per
   [`loophole-packaging.md`](loophole-packaging.md) §5.1 that command is **census site 5**, which
   does not see pack loopholes today. The provenance this section promises is blocked on that
   convergence.
2. **"A workspace config disabled it"** is the other, and it is the more dangerous one, because it is
   agent-editable and prints nothing at all today
   ([`loophole-packaging.md`](loophole-packaging.md) §4.3 G1). Applying this section's own rule to
   `enabled` is a one-line launch notice and a `yolo check` warn instead of an `ok`.

**Two packs disagreeing** (A supersedes X; B implicitly relies on X) is recorded as a known limit,
not designed for: any supersession wins, there is deliberately no `needs`, and the mitigation is the
visibility above. At three bundled loopholes and one superseding pack, the conflict is unreachable.

## 6. What is deliberately NOT built

| Not built | Why |
|---|---|
| **`needs: [<capability>]`** | Invents conflict resolution before the conflict exists (§5). Additive if it ever bites. |
| **A yolo-owned registry of capability names** | Core deliberately does not know what an agent is. A central registry would rebuild the agent registry the pack system exists to avoid. Capabilities are declared by whoever holds the fact. |

**Deleted 2026-08-13: "`serves` on a *pack* — unexpressible."** It is expressible now; it lives on
the loophole manifest inside the pack (§2). The old row's reasoning — *"none of the kinds is a
daemon, so a pack has nothing to serve with"* — was the premise
[`loophole-packaging.md`](loophole-packaging.md) falsified.

**The general thing being shipped is the NAMESPACE AND ITS SEMANTICS, not every edge.** A later
`needs` is purely additive — same namespace, same matching rule, same load-time check, no migration.

## 7. The first-party instance

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

## 8. Acceptance

1. A loophole with no `serves` behaves exactly as today — byte-identical, including every
   third-party manifest. Pinned by a test.
2. **`serves` is refused on a PACK manifest**, with a message naming the distinction. The message
   changed with the premise: not *"a pack has nothing to serve with"* (false since the 15th kind) but
   *"put it on your loophole's `manifest.jsonc` — a statement about an implementation belongs with
   the implementation."* A fix rather than a wall.
3. `serves` + a matching `supersedes` from a selected pack ⇒ inactive, and `loopholes list` prints
   the pack, the capability and the `because`.
4. `supersedes` naming an unserved capability ⇒ **load error** naming the string and listing what is
   served.
5. `supersedes` without `because` ⇒ manifest validation error.
6. A loophole serving two capabilities with only one superseded ⇒ still active.
7. The `Enabled` config knob and `RequirementsMet()` are unchanged and independent — three gates,
   any of which can deactivate.

## 9. OQ-CAP2 — RESOLVED 2026-08-13 with **(B)**

**The question, raised in review:** *"packs can't ship a loophole? then how are loopholes
distributed? I think this is a mistake."*

**The tell it exposed:** supersession exists because loopholes are not SELECTED the way packs are —
*"if you're superseding something, it's because you can't just remove the other pack."* If a loophole
shipped inside a pack, "do not run the broker under Bedrock" would be "do not select the broker
pack", and most of this document would be unnecessary.

**Resolved (B): packs ship loopholes**, designed in
[`loophole-packaging.md`](loophole-packaging.md) — a 15th contribution kind pointing at a module
directory in the pack; a yolo-run TLS front so an external author needs neither Go nor a TLS
implementation; host execution as an approvable claim in the machinery `yolo pack install` already
has.

**What that leaves here, precisely:**

- **The residue is the bundled set, and no smaller.** All three bundled loopholes stay bundled — the
  broker because it auto-activates by design and because neither its host singleton argv nor its
  per-jail relay is expressible in a manifest; `host-processes` because its client is a baked image
  binary; `audio` because there is no reason to move it. So every mechanism above is scoped to
  **three first-party manifests**, of which **one** auto-activates in a way a pack can reasonably
  want to cancel.
- **This document was cut to match**, rather than left standing with a caveat at the top. The
  namespace argument (§3), the two verbs (§2) and the two failure modes (§5) are what survived; the
  sections that existed to defend a public extension surface against hypothetical third-party
  collisions were compressed to their conclusions.
- **The size question is now genuinely open.** Whether a capability namespace is the right shape for
  one auto-activating loophole, or whether something blunter is, is
  [`loophole-packaging.md`](loophole-packaging.md) **OQ-LP6** — a maintainer call, and it is now a
  decision about three first-party manifests rather than about a public surface. The counter-argument
  is in this doc's opening: a loophole manifest remains a public surface, so `serves` is a field
  third parties will write regardless.

**The cost of (B), recorded because it was the reason to hesitate.** No pack kind caused host-side
code execution before it: pack hooks run in the entrypoint (in-jail), `program` installs in-jail. A
loophole daemon is a **host** process, so this is packs crossing from *"configure the jail"* to
*"run code on your machine"*. There is precedent for gated host **access** (a fetched pack's
`host_files` claim is approved at install and recorded in the lockfile) but none for host
**execution**. That is a real trust step and
[`loophole-packaging.md`](loophole-packaging.md) §4 is where it is paid for — including the finding
that **an approval anchored to a claim STRING is content-blind**, which is a new invariant host reads
never needed.

---

## 10. Open question (design detail)

~~💬~~ **OQ-CAP — SETTLED 2026-09-02, by the tree rather than by a fresh ruling.** It was a
*confirmation*, not a fork: the leaning was uncontested since 2026-08-13, and the implementation
had already built it — `supersedes` is a field on the **manifest top level**
(`internal/packdecl/packdecl.go:86`, beside `skills_tier`), and
`TestSupersedesNotAContributionKind` (`internal/packdecl/supersedes_test.go:117-131`) **refuses**
the alternative outright: a `contributes[]` entry with `kind: "supersedes"` is a validation error.
A question whose losing option is pinned rejected by a test is not open; recording that is what
[`../plans/further-roadmap-ideas.md`](../plans/further-roadmap-ideas.md) §4b asked for. Reopen only
with a migration case nobody has.

The reasoning, kept for the record: top-level matches `skills_tier` (a per-pack fact, not a
contribution) and reads correctly — superseding is a property *of the pack*, not a thing it
delivers; a contribution that contributes nothing is a category error. **(B) strengthens this:**
the thing that *is* a contribution is the loophole; `supersedes` staying top-level keeps the two
visibly different.

**The one-line residue is work, not a question:** the leaning's second half — `yolo pack footprint`
taught to print `supersedes` — is still unbuilt (verified 2026-09-02: `internal/cli/pack.go`
mentions supersession nowhere). It is queued as a small repair on the roadmap.

**Answer:**
> _(empty — fill in when decided)_
