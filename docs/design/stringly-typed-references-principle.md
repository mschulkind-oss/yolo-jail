---
title: "Principle: Stringly-Typed References Fail Closed — at a Point That Can Decide and an Actor Who Can Act"
date: 2026-08-29
status: accepted
tags: [principles, validation, packs, config, architecture]
summary: "Every reference that names another component by string must fail closed. Amended 2026-08-30: the rule is not only about severity but about PLACEMENT — fail closed at the point that can decide the reference and whose actor can act on the failure. Where no such point exists, that is plumbing to fix, not a licence to warn. The census now records what the code does today, which for three of five mechanisms is not this."
---

# Principle: stringly-typed references fail closed — at a point that can decide, and an actor who can act

**Status:** PRINCIPLE, authored 2026-08-29, **substantially amended 2026-08-30**. Amended rather
than rewritten, per the convention its siblings follow.

> [!IMPORTANT]
> **What the amendment changed, and why.** The first version stated the severity rule and stopped.
> Three problems surfaced within a day of writing it. **(1)** The repo's own flagship
> implementation — capability supersession — contains a written, reasoned *departure* from it
> ([`supersede.go:208-237`](../../internal/loopholes/supersede.go#L208-L237)), and this doc's census
> reported that case backwards. **(2)** The census was wrong about three of its five rows, in a doc
> whose entire subject is that silence must not mask a mismatch. **(3)** `optional: true` as first
> written disabled the typo check along with the selection check — the exact failure §2 names.
>
> The severity rule survives intact: **fail closed**. What it gained is **R5, placement** — and the
> discriminator it needed was already written down twice in this repo, in
> [`gate-placement-principle.md`](gate-placement-principle.md) and in `kinds.go`'s
> structure-versus-skew rule. §5 is the worked contrast that was missing.

**Audience:** anyone designing a manifest field, configuration key, or cross-component reference
where one component names another by string — pack slugs, profile tags, capability identifiers,
service names, surface slots, provider names.

**Sibling principles:** [`gate-placement-principle.md`](gate-placement-principle.md) (put the gate
where the authority changes — **R5 is that principle applied to references**),
[`extension-point-principle.md`](extension-point-principle.md) (who designs an extension point), and
[`happy-path-principle.md`](happy-path-principle.md) (fill the matrix with one blessed path).

**Reads with:** [`reference-mismatch-diagnostics.md`](reference-mismatch-diagnostics.md) — the
design doc that closes the gap between §6's two columns, written from the user's side.

---

## 1. The principle

> **Every reference that names another component by string must fail closed. A name that matches
> nothing is a fatal error, never a silent skip — and the refusal belongs at the first point that
> can DECIDE the reference and whose actor can ACT on the failure. Where no such point exists, that
> is plumbing to fix, not a licence to downgrade to a warning.**

Two halves, and the second is the one this doc got wrong first. Severity says *what* happens.
Placement says *where* — and placement is what makes the severity affordable.

---

## 2. Why

1. **Typos in strings are undecidable without closed verification.** When component A references
   component B by string (`target: "claude"` vs `target: "cloude"`), a permissive system silently
   drops the payload. The author believes it worked; the system boots unconfigured.
2. **The silent-skip debugging nightmare.** An adapter or profile that silently fails to apply
   presents downstream as mysterious auth failures, absent environment variables, or a loophole that
   should have been superseded and is still running. The distance between cause and symptom is the
   whole cost.
3. **Explicit intent beats implicit guessing.** An author who genuinely means "apply this only if
   that component is present" can say so. An author who typed a name wrong cannot say anything,
   which is why the two cases must not share a disposition (§3).
4. **The repo is already moving this way, twice, for measured reasons.** A failed nix build used to
   fall back to the cached image; *"a broken build then looked like a working jail running **stale**
   code, reported two layers from its cause"* — fatal since 2026-08-15. The in-jail reachability
   witness became FATAL on 2026-08-18 after a total loopback-TLS outage went unnoticed for four
   days. Both are the same lesson: **a warning nobody reads is more expensive than a refusal
   everybody sees** (see [AGENTS.md](../../AGENTS.md)).

---

## 3. A reference asks two questions, and only one of them is optional

The first version of this doc collapsed them, and `optional: true` inherited the collapse — it
skipped an unmatched reference without asking *why* it was unmatched.

| Question | Checked against | Disposition |
| :--- | :--- | :--- |
| **Does this string name a real thing?** | the resolvable **universe** — every pack, capability or provider this build could know | **Fatal, always.** `optional` does not excuse a typo. |
| **Is that thing active here?** | the **selected** set for this launch | Fatal when required; a clean, reported skip when `optional: true`. |

`optional` answers the second question only. An opportunistic adapter that names four agents and
finds one selected is working as designed; an opportunistic adapter that names `cloude` is broken,
and marking it optional must not make it look fine.

---

## 4. Where the gate goes (R5)

A refusal is only worth what the person receiving it can do about it. Two conditions:

- **Decidability.** The check must run somewhere the full candidate set is in hand. A validator that
  can decode a claim but cannot resolve the registry cannot refuse it — and that is a fact about
  *that point*, not about the rule.
- **Actionability.** The actor receiving the refusal must be able to fix it. A jail that cannot run
  `just load` cannot act on "your image is older than your tree."

**When a point fails either test, move the gate — do not lower the severity.** In practice the
upstream point almost always exists: the host launch path holds the complete resolved set and the
user standing at it has every remedy available.

And where an actor genuinely cannot act — the in-jail read across a version boundary —
skip-and-report is not a weaker fail-closed. It is a **different gate**, and the fail-closed one
belongs upstream, where the remedy lives.

---

## 5. The worked contrast: two answers to skew, both in this tree, both right

This is the section the first version lacked, and the case that produced it is the doc's own
flagship.

**Skew** here means: the pack tree and the binary that reads it are on independent clocks.
`/workspace` is bind-mounted live; the baked binaries are frozen until a host `just load`
([AGENTS.md](../../AGENTS.md)). So the usual skew is not an exotic third-party-pack-from-the-future
case — it is *your own working tree being newer than your own image*, which is the normal state
between loads.

The tree already holds two dispositions for it, and the discriminator is **not** severity:

| Site | On skew | Why that is right there |
| :--- | :--- | :--- |
| `DecodeTolerant` ([`packdecl.go:266-272`](../../internal/packdecl/packdecl.go#L266-L272)) | skip + report: *"this build does not know it, so the contribution is not rendered (version skew; a build that knows the kind will render it)"* | The in-jail reader **cannot** `just load`. Refusing failed the boot for a manifest yolo ships. |
| `ensureJailImage` (`integration/harness_test.go:324`) | **abort, naming the fix command** | The host-side actor **can** rebuild. |

Same failure class, opposite dispositions, and the discriminator is *who can act* — which is
[`gate-placement-principle.md`](gate-placement-principle.md) exactly.

### 5.1 The departure that produced R5

[`supersede.go:208-237`](../../internal/loopholes/supersede.go#L208-L237) documents its own
divergence from [`pack-capabilities.md`](pack-capabilities.md) §5:

> **REPORTED, NOT REFUSED, and this is a deliberate departure** … §5's premise is that "the namespace
> is closed by the loopholes present, so this is decidable" — true of the SET, but the set is a fact
> about one machine at one moment, and a refusal keyed on it is refusable by circumstance.

It gives three reasons. **One survives as stated, and it argues for relocation rather than
downgrade:**

1. **"The claim is decodable long before the loopholes are"** — `pack.json` is validated by
   `yolo pack lint` and by the in-jail entrypoint, neither of which holds the
   bundled+pack+user+config loophole set. **True, and it is R5's decidability test failing.** The
   answer is to check where the set exists — `NewHostSet`, the host launch path — not to warn.
2. **"Would brick every jail on a pre-`just load` image."** *Brick* is overstated: the recovery is
   `just load`, run on the host, which by definition is not inside the jail that refused. That is a
   breaking change with a one-command fix. What the reason gets right is that the **message** must
   name skew as skew — *"your image predates your working tree; run `just load`"* — rather than
   reporting a mismatch the author cannot otherwise explain.
3. **"A refusal would take down `yolo loopholes list` — the very command a user runs to find out
   what happened."** **This does not survive.** It is an argument about placement wearing severity's
   clothes. Refuse at launch; report at `list`. Fail-closed never required one disposition at every
   surface.

> [!WARNING]
> **Do not read this section as licensing a warning wherever skew is possible.** The half of that
> comment worth keeping is its distinction — structural validity (decidable from the declaration
> alone, version-invariant) versus matching against a runtime-assembled set. The remedy it drew from
> the distinction was weaker than the one this repo already had. Its own conclusion states the good
> half: *"the structural half IS refused at load, in packdecl, where it is version-invariant … and
> the match half is reported here."* R5 keeps the split and moves the match half to a gate that can
> hold it.

### 5.2 What R5 does not excuse

`unmatchedSupersessions`' **message** is the best diagnostic of its kind in the tree and R4 is
modelled on it. It names the offending string, suggests the nearest served capability at a
length-scaled edit distance, lists what *is* served, and states the consequence — *"Nothing was
superseded, so every loophole keeps running."* Relocating that check must **keep the sentence**.
The defect is where it lands and how loud it is, never what it says.

---

## 6. The rules

IDs are stable — R1–R4 keep their numbering because sibling docs cite them.

### R1: Fail closed by default
A reference whose target does not exist in the resolvable universe aborts. This is not conditional
on `optional` (§3).

### R2: Explicit opt-in for permissive *selection*, never for existence
`"optional": true` means *"apply only if the named target is selected here."* It never means *"skip
if the name is wrong."* An optional reference to a nonexistent name is still fatal, and the message
should say which of the two rules it tripped.

### R3: Rich diagnostics — the message is most of the value
Never a generic `invalid target`. Name **the offending string**, **the declaring component**, **the
active candidate set**, a **did-you-mean** at a length-scaled edit distance, and **the explicit
remedy** ("add `claude` to `packs`, or mark this fragment `optional: true`"). When the mismatch is
attributable to skew, say *that*, with the rebuild command.

### R4: Closed enums where fixed, live-registry validation where open
- **Fixed syntactic slots** — a name the schema owns — must be closed Go enums checked at parse
  time. Shipped examples: contribution `kind` ([`kinds.go`](../../internal/packdecl/kinds.go)),
  surface `mode` ([`load.go:38`](../../internal/agentcfg/manifest/load.go#L38)), surface `codec`
  ([`manifest.go:215`](../../internal/agentcfg/manifest/manifest.go#L215)). **`providers.*.wire_api`
  is a slot of this shape that is NOT closed today** — free-form string,
  [`validate.go:867-869`](../../internal/config/validate.go#L867-L869). *(`notch` is not an example
  of this rule at all: it is a Go type a constructor sets, never a manifest string.)*
- **Open semantic slots** — pack slugs, profile names, capability identifiers, provider names — are
  validated against the live resolved registry, at the point R5 selects.

### R5: Place the gate where the reference is decidable and the failure is actionable
§4. If the natural validation point cannot resolve the registry, or its actor cannot act on the
refusal, move the gate upstream. Lowering severity to fit the wrong location is the anti-pattern
this rule exists to name.

---

## 7. Census — what the code does today, and what R1–R5 ask for

> [!WARNING]
> **This table is now two columns for a reason.** The first version of this doc conflated them and
> asserted the target state as the live state in a section headed *"Live Census in YOLO Jail"*.
> Three of five rows were wrong. Every claim below was re-measured on 2026-08-30; the gap between
> the columns is queued as roadmap 💬 **17** and designed in
> [`reference-mismatch-diagnostics.md`](reference-mismatch-diagnostics.md).

| Mechanism | String field | **Today (measured 2026-08-30)** | **Target under R1–R5** |
| :--- | :--- | :--- | :--- |
| `agent_profiles` (→ `pack_profiles`) | the `<pack>` key | **Unchecked.** [`validate.go:902-922`](../../internal/config/validate.go#L902-L922) validates the *value* is a string; the key is compared to nothing. `{"cloude": "bedrock"}` returns `[PASS] Merged config is semantically valid`. | Fatal at `check` and at launch, against the pack universe. |
| `pack-capabilities` | `supersedes.capability` | **A stderr warning** at discovery ([`discover.go:717-729`](../../internal/loopholes/discover.go#L717-L729)). `SupersessionProblems()` is a value-shaped seam whose "obvious next reader" does not exist yet. Structural validity (empty `capability`, missing `because`, duplicates) *is* refused at load in `packdecl`. | Fatal on the **launch** path, where `NewHostSet` holds the full set. Reported, not fatal, at `loopholes list`. Message unchanged (§5.2). |
| `providers.*` | `wire_api` | **Unchecked beyond "is a string"** ([`validate.go:867-869`](../../internal/config/validate.go#L867-L869)). `"totally-not-a-wire-api"` passes. | Closed enum at parse time (R4). |
| `providers.*` | `base_url` | **Unchecked beyond "is a string".** `https://user:sk-secret@x.com/v1` passes validation — a plaintext credential in a git-tracked file. | Parse as `http`/`https`; refuse userinfo. |
| `env_sources` | file paths | **Warn + skip** ([`envsources.go:85-87`](../../internal/config/envsources.go#L85-L87)) — a stderr `warning:` line, not a trace log. | **Unchanged — this is correct.** A host path absent on this machine is portability, not a typo, and there is no registry to check it against. But an *active* profile whose `api_key_env` was never hydrated by any source is a launch refusal. |
| Config **keys** (all) | key names | **Fatal.** `reportUnknownKeys` ([`validate.go:116-124`](../../internal/config/validate.go#L116-L124)) — `[FAIL] config.agent_profilez: unknown key`. | Unchanged — this row is the model. |

> [!NOTE]
> **The shape of the gap, in one observation.** In the same file, one line apart: mistype a **key**
> and you get `[FAIL] config.providers.bedrock.wire_apid: unknown key`; mistype a **value that names
> a component** and you get `[PASS]`. Field names live in a closed namespace and are enforced.
> References to components do not, and are not. That asymmetry is the entire subject of this doc.

**`pack-fragment` has been removed from this census.** It is a proposal in an `in-review` doc
([`pack-profiles.md`](pack-profiles.md)), not a mechanism; the first version listed it as a live test
case while that doc cited this one as its authority. Two same-day docs each grounding the other in an
unbuilt thing is not evidence. See [`profiles-as-pack-variants.md`](profiles-as-pack-variants.md) §8
for the counter-design's treatment.

---

## 8. What this does not license

**It is not a mandate to refuse everything unresolvable.** `env_sources` is permissive and should
stay permissive: a host path is not a reference into a namespace, there is no candidate set to
suggest from, and absence on one machine is the portability case the field exists for. The rule
covers references into a **closed universe of named components**, not environment probes. By the
same token `loopholedecl.Requires` (`command_on_path`, `file_exists`) is not an instance of this
principle — it asserts facts about a host, and the first version of this census listed it in error.

**It is not a licence to refuse across a version boundary from a place that cannot recover.**
That is R5's second test, and `DecodeTolerant` is the shipped case where skip-and-report is right.

**It does not make a warning acceptable because a refusal is inconvenient to place.** Inconvenient
placement is the finding, not the exemption (§5.1, reason 3).

**It says nothing about severity for values that are not references.** A malformed URL, a bad enum,
a missing required field are schema validation and were already fatal. This principle is only about
one component naming another.
