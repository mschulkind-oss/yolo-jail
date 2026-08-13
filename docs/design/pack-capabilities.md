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

> **PREREQUISITE — read [`loophole-packaging.md`](loophole-packaging.md) first.** §10 below (OQ-CAP2)
> asks whether packs should be able to ship loopholes and recommends deciding that **before**
> building any of §§1–9. That decision is now made, in that doc, as **(B): packs ship loopholes**. It
> supersedes parts of what follows — §2.2's argument in particular — and narrows the rest to the
> **bundled** loophole set. Its §6 is the section-by-section list of what survives here and what
> dies; §§1–9 have NOT yet been rewritten to match, so read them against that list.

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

### 2.1 Supersede is NOT provide — and the difference is which way the demand goes

Raised in review: *"if it would replace the whole thing, this would then be a `provides`
mechanism… fine line between provides and serves."* It is a real fork, and getting it wrong is the
most dangerous mistake this design allows, so it gets its own rule.

**The test — after this pack is selected, does the job still need doing?**

| Answer | Verb | What happened |
|---|---|---|
| **No.** The demand is gone. | `supersedes` | Under Bedrock **no OAuth token is ever refreshed**, so serializing refreshes is not a job being done differently — it is a job that no longer exists. |
| **Yes, and I do it now.** | *provision* — **NOT expressible today** | The demand persists; only the supplier changed. |

**Superseding when you meant providing silently stops the job being done, with nothing taking
over.** That is the failure this section exists to prevent, and it is quiet: the loophole goes
inactive, `loopholes list` correctly reports why, and the work simply never happens. Nothing in the
system can detect it, because "I will do it instead" is exactly the claim `supersedes` does not
make.

So: **`supersedes` is a claim that DEMAND vanished, not that SUPPLY moved.**

### 2.2 Why a pack cannot `serve` — a hard line, not a policy

The follow-on question in review — *"does any pack that `serves` something automatically have
something else with it?"* — has a clean answer: **yes, and that is precisely why packs cannot
serve.**

Serving a capability means **carrying an implementation**. A loophole *is* one: a daemon, a
transport, a lifecycle. A pack is a bundle of declarations across a **closed set of 14 contribution
kinds** (`internal/packdecl/kinds.go`) — program, requires, skills, briefing, files, config,
config-overlay, state, reads-host, mount, env, launch, hook, autonomy — **and none of them is "a
daemon."** Loopholes come from `bundled_loopholes/` or a user loophole dir, never from a pack.

So a pack has nothing to serve *with*. The asymmetry between the two verbs is not a restriction we
imposed; it falls out of where implementations live:

- **`serves`** — for things that ARE an implementation. Loopholes today.
- **`supersedes`** — for things that can only make a claim. Packs today.

That also sharpens §7: pack-to-pack provision is not merely undemanded, it is **unexpressible**,
and making it expressible would mean letting a pack ship a daemon — a much larger change than a
manifest field, and one with its own trust story (a pack that ships executable host-side code is
not the same object as a pack that ships config).

**Granularity, since the review raised "replacing PART":** supersession is always **per job**, never
per component. A loophole serving two capabilities with one superseded stays active for the other
(§4's `every` rule). Superseding *all* of them retires the loophole entirely — but that is an
arithmetic consequence of retiring each job, not a separate "retire this loophole" power. There is
deliberately no way to say "turn that component off" — `enabled: false` already exists for that, and
it is honest about being a blunt instrument where this is a statement about work.

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

`Loophole.Active()` (`internal/loopholes/loopholes.go:232`) gains a third gate beside the two it
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
| **`serves` on a *pack*** (pack-to-pack provision) | **Unexpressible, not merely undemanded** — §2.2. Serving means carrying an implementation, and none of the 14 contribution kinds is a daemon, so a pack has nothing to serve with. Enabling it would mean letting a pack ship host-side executable code, which is a different object with its own trust story. (It is also the A1–A2 case, retired 2026-08-13 once the two auth "modes" turned out not to be peers.) |
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
1b. **`serves` is refused on a PACK manifest** (§2.2) with a message naming the distinction, so an
   author reaching for provision when they mean supersession is told which one they want rather
   than getting an unknown-field error.
2. `serves` + a matching `supersedes` from a selected pack ⇒ inactive, and `loopholes list` prints
   the pack, the capability and the `because`.
3. `supersedes` naming an unserved capability ⇒ **load error** naming the string and listing what is
   served.
4. `supersedes` without `because` ⇒ manifest validation error.
5. A loophole serving two capabilities with only one superseded ⇒ still active.
6. The `Enabled` config knob and `RequirementsMet()` are unchanged and independent — three gates,
   any of which can deactivate.

## 10. The bigger fork this design may be working around — OQ-CAP2

**Raised in review 2026-08-13, and it is the most important open question here:** *"packs can't
ship a loophole? then how are loopholes distributed? I think this is a mistake."*

### How loopholes are distributed today

| Source | Who it is for | State |
|---|---|---|
| `bundled_loopholes/` (embedded in the binary) | yolo's own three | fine |
| a user loophole dir | a local, hand-placed loophole | no packaging, no fetch, no versioning |
| the `loopholes` block in `yolo-jail.jsonc` | **the only third-party path** | **degraded, and getting worse** |

That last row is the finding. `internal/loopholes/discover.go` pins config-defined loopholes to the
**retired `unix-socket` transport**, and its comment says exactly why:

> *"a config entry's daemon is a THIRD-PARTY PROGRAM yolo did not write… nothing yolo ships lets it
> publish a loopback-TLS endpoint file instead (`internal/hostservice` is `internal/`, so it is not
> importable from outside this module)."*

So as the transport unifies (row **T1**), **third-party loopholes are stranded on a transport the
manifest vocabulary no longer accepts** — and they have no sharing story at all: no fetch, no
version, no approval, no manifest travelling with the code.

### Why this bears on the design above

**Supersession exists because loopholes are not SELECTED the way packs are.** The review put it
precisely: *"if you're superseding something, it's because you can't just remove the other pack."*

That is the tell. If the broker shipped inside a pack and were selected by selecting that pack, then
"do not run the broker under Bedrock" is **"do not select the broker pack"** — and §§1–9 above
become unnecessary. No capability namespace, no two verbs, no matching rule.

**So this design may be a workaround for a distribution gap rather than a fix for it.**

### Why it does not dissolve completely

The broker **deliberately auto-activates** (`requires: {command_on_path: "claude"}`) so a user gets
refresh serialization *without knowing they need it*. Make it opt-in and anyone who does not select
it silently gets the single-use-refresh-token race — the exact bug it exists to prevent. A default-on
pack is expressible (the local pack is already `Implicit: true`), but then "not this time" needs a
mechanism again, and that is supersession or an exclude list.

**So the residue is narrow: supersession is only needed for things that AUTO-ACTIVATE.** Its
necessity is proportional to how many of those exist — and if loopholes moved into packs and were
selected explicitly, that count could be zero.

### The cost of the other path, stated plainly

**No pack kind causes host-side code execution today.** Pack hooks run in the entrypoint, which is
in-jail; `program` installs in-jail. A loophole daemon is a **host** process. So "packs ship
loopholes" is not one more kind — it is packs crossing from *"configure the jail"* to *"run code on
your machine"*. There is precedent for gated host **access** (a fetched pack's `host_files` claim is
approved at install and recorded in the lockfile) but none for host **execution**.

That is a real trust step, not a blocker: the approval machinery exists and the boundary is exactly
the kind of thing it was built to gate. But it should be decided deliberately, not arrived at.

### OQ-CAP2 — the fork

- **(A) Ship capabilities as designed (§§1–9).** Works with loopholes as they are, small, touches no
  trust boundary. Leaves third-party loophole distribution unfixed and stranded on a retired
  transport.
- **(B) Let packs ship loopholes.** Fixes distribution properly, makes *selection* the mechanism,
  and probably makes most of this doc unnecessary. Needs the host-execution trust story and a way to
  keep good defaults on.
- **(C) Both**, in that order — (A) now because it is small and (B) is not, (B) when the trust story
  is worked out, retiring whatever of (A) selection makes redundant.

**My read: the review is right that (B) is the real fix**, and I do not think (A) should be built
first on the strength of it — building a workaround for a gap you have already decided to close is
how the workaround becomes permanent, which is the exact failure
[`extension-point-principle.md`](extension-point-principle.md) warns about. **Recommendation: decide
(B) first.** If (B) is going to happen, (A) shrinks to whatever auto-activating loopholes remain,
and that is a much smaller thing to design.

### RESOLVED 2026-08-13 — **(B)**, designed in [`loophole-packaging.md`](loophole-packaging.md)

The recommendation was taken. A 15th contribution kind, `loophole`, points at a module directory in
the pack; the framework runs a TLS front in front of a plain-socket daemon so an external author
needs neither Go nor a TLS implementation; host execution is an approvable claim in the machinery
`yolo pack install` already has. Three findings there change how §§1–9 should be read:

1. **§2.2's argument is dead.** Its premise — *"none of the 14 kinds is 'a daemon'"* — is what the
   15th kind falsifies. Its conclusion (`serves` does not go on `pack.json`) survives for a different
   reason: the loophole a pack ships has a manifest of its own, and that is where a statement about
   an implementation belongs. So `serves` stays exactly where it is and travels *inside* the pack.
2. **The residue is what this doc predicted, and no smaller.** The three bundled loopholes stay
   bundled — the broker because it auto-activates by design and because neither its host singleton
   argv nor its per-jail relay is expressible in a manifest; `host-processes` because its client is a
   baked image binary. So supersession survives for the **bundled set only**, and every §§1–9
   mechanism is now scoped to three first-party manifests rather than to a public surface.
3. **Which reopens the size question.** Whether a capability namespace is still the right shape for
   three bundled loopholes, or whether something blunter is, is recorded there as **OQ-LP6**. The
   extension-point argument cuts both ways: a loophole manifest is still a public surface, so
   `serves` is a field third parties will write even if only bundled loopholes are ever superseded.

---

## 11. Open question (design detail)

**OQ-CAP.** `supersedes` currently lives at the **manifest top level**, beside `name`, rather than
as a `contributes[]` entry. That matches `skills_tier` (a per-pack fact, not a contribution) and
reads correctly — superseding is a property *of the pack*, not a thing it delivers. The alternative
is a `kind: "supersedes"` contribution, which would put it in the same list as everything else a
pack does and make `yolo pack footprint` pick it up for free. **My read: top-level**, with footprint
taught to print it explicitly — a contribution that contributes nothing is a category error, and
`footprint` already reads the manifest.
