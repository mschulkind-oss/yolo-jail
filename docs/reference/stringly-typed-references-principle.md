---
status: current
verified: 2026-09-09
verified_commit: 356bcec8
covers:
  - internal/config/validate.go
  - internal/config/envsources.go
  - internal/packdecl/packdecl.go
  - internal/packdecl/kinds.go
  - internal/agentcfg/manifest/
  - internal/loopholes/supersede.go
tags: [principle, validation, packs, config, diagnostics]
---

# Principle: stringly-typed references fail closed — at a point that can decide, and an actor who can act

**Status:** PRINCIPLE, current as of 2026-09-09, verified against `356bcec8`.

**Audience:** anyone designing a manifest field, configuration key, or cross-component reference
where one component names another by string — pack slugs, profile tags, capability identifiers,
service names, surface slots, provider names.

A principle keeps its rationale on purpose. This one has two halves that look like one rule, and the
half people drop is placement: severity says *what* happens, placement says *where*, and placement
is what makes the severity affordable.

**Sibling principles:** [`gate-placement-principle.md`](gate-placement-principle.md) (put the gate
where the authority changes — **[R5](#r5-place-the-gate-where-the-reference-is-decidable-and-the-failure-is-actionable)
is that principle applied to references**),
[`extension-point-principle.md`](extension-point-principle.md) (who designs an extension point), and
[`happy-path-principle.md`](happy-path-principle.md) (fill the matrix with one blessed path).

**Reads with:** [`../design/reference-mismatch-diagnostics.md`](../design/reference-mismatch-diagnostics.md) —
the in-flight design that closes the one remaining gap named below, written from the user's side.

---

## The principle

> **Every reference that names another component by string must fail closed. A name that matches
> nothing is a fatal error, never a silent skip — and the refusal belongs at the first point that
> can DECIDE the reference and whose actor can ACT on the failure. Where no such point exists, that
> is plumbing to fix, not a licence to downgrade to a warning.**

## Why

1. **Typos in strings are undecidable without closed verification.** When component A references
   component B by string (`target: "claude"` vs `target: "cloude"`), a permissive system silently
   drops the payload. The author believes it worked; the system boots unconfigured.
2. **The silent-skip debugging nightmare.** An adapter or profile that silently fails to apply
   presents downstream as mysterious auth failures, absent environment variables, or a loophole that
   should have been superseded and is still running. The distance between cause and symptom is the
   whole cost.
3. **Explicit intent beats implicit guessing.** An author who genuinely means "apply this only if
   that component is present" can say so. An author who typed a name wrong cannot say anything,
   which is why the two cases must not share a disposition
   ([below](#a-reference-asks-two-questions-and-only-one-of-them-is-optional)).
4. **The repo already moved this way twice, for measured reasons.** A failed nix build used to fall
   back to the cached image, and a broken build then looked like a working jail running **stale**
   code, reported two layers from its cause. The in-jail reachability witness became fatal after a
   total loopback-TLS outage went unnoticed for days. Both are the same lesson: **a warning nobody
   reads is more expensive than a refusal everybody sees.**

## A reference asks two questions, and only one of them is optional

Collapsing them is the mistake this section exists to prevent — an `optional` flag that skips an
unmatched reference *without asking why it was unmatched* re-creates the exact failure the principle
names.

| Question | Checked against | Disposition |
| :--- | :--- | :--- |
| **Does this string name a real thing?** | the resolvable **universe** — every pack, capability or provider this build could know | **Fatal, always.** `optional` does not excuse a typo. |
| **Is that thing active here?** | the **selected** set for this launch | Fatal when required; a clean, reported skip when `optional: true`. |

`optional` answers the second question only. An opportunistic adapter that names four agents and
finds one selected is working as designed; an opportunistic adapter that names `cloude` is broken,
and marking it optional must not make it look fine.

## Where the gate goes

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

## The worked contrast: two answers to skew, both right

**Skew** *(coined in this repo)* — the pack tree and the binary that reads it are on independent
clocks. `/workspace` is bind-mounted live; the jail's binaries are chosen at launch and frozen for
the session. So the usual skew is not an exotic third-party-pack-from-the-future case — it is *your
own working tree being newer than your own image*, which is the normal state between loads. Not the
same thing as a typo, and not the same thing as an unselected target: the name is right, the reader
is old.

The tree holds two dispositions for it, and the discriminator is **not** severity:

| Site | On skew | Why that is right there |
| :--- | :--- | :--- |
| `packdecl.DecodeTolerant` | skip + report: *"this build does not know it, so the contribution is not rendered (version skew; a build that knows the kind will render it)"* | The in-jail reader **cannot** `just load`. Refusing failed the boot for a manifest yolo ships. |
| `ensureJailImage` in `integration/` | **abort, naming the fix command** | The host-side actor **can** rebuild. |

Same failure class, opposite dispositions, and the discriminator is *who can act* — which is
[`gate-placement-principle.md`](gate-placement-principle.md) exactly.

### The departure that produced R5

`loopholes.unmatchedSupersessions` documents its own divergence from the capability design's
"refused at load" wording, and the divergence is where R5 came from. Its premise — *"the namespace
is closed by the loopholes present, so this is decidable"* — is true of the SET, but the set is a
fact about one machine at one moment, and a refusal keyed on it is refusable by circumstance.

Of the three reasons the comment gives, **one survives as stated, and it argues for relocation
rather than downgrade:**

1. **"The claim is decodable long before the loopholes are"** — `pack.json` is validated by
   `yolo pack lint` and by the in-jail entrypoint, neither of which holds the
   bundled+pack+user+config loophole set. **True, and it is R5's decidability test failing.** The
   answer is to check where the set exists — the host launch path — not to warn.
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
> **Do not read the departure as licensing a warning wherever skew is possible.** The half of that
> comment worth keeping is its distinction — structural validity (decidable from the declaration
> alone, version-invariant) versus matching against a runtime-assembled set. The structural half
> **is** refused at load, in `packdecl`, where it is version-invariant; the match half is reported.
> R5 keeps that split and moves the match half to a gate that can hold it. The remedy the comment
> drew from the distinction was weaker than the one this repo already had.

> [!WARNING]
> **Relocating a check must KEEP ITS SENTENCE.** `unmatchedSupersessions`' message is the best
> diagnostic of its kind in the tree and [R3](#r3-rich-diagnostics--the-message-is-most-of-the-value)
> is modelled on it: it names the offending string, suggests the nearest served capability at a
> length-scaled edit distance, lists what *is* served, and states the consequence — *"Nothing was
> superseded, so every loophole keeps running."* The defect is where it lands and how loud it is,
> never what it says.

## The rules

IDs are stable — R1–R4 keep their numbering because sibling docs and code comments cite them, and
R5 was added without renumbering for the same reason.

### R1: Fail closed by default

A reference whose target does not exist in the resolvable universe aborts. This is not conditional
on `optional`.

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

- **Fixed syntactic slots** — a name the schema owns — are closed Go enums checked at parse time.
  The shipped instances are contribution `kind` (`packdecl.KnownKind`, over the `footprints` map),
  surface `mode` and `codec` (`internal/agentcfg/manifest`), and `providers.*.wire_api`
  (`config.validateWireAPI`, over `packdecl.KnownWireAPIs`). *(`notch` is not an instance of this
  rule at all: it is a Go type a constructor sets, never a manifest string.)*
- **Open semantic slots** — pack slugs, profile names, capability identifiers, provider names — are
  validated against the live resolved registry, at the point R5 selects.

### R5: Place the gate where the reference is decidable and the failure is actionable

If the natural validation point cannot resolve the registry, or its actor cannot act on the
refusal, move the gate upstream. Lowering severity to fit the wrong location is the anti-pattern
this rule exists to name.

## The shape of the enforcement, mechanism by mechanism

Each string-valued mechanism in the tree has exactly one disposition, and the disposition — not a
count of how many have reached the target — is the durable fact. Every entry names the symbol that
decides it, so a reader can re-measure rather than trust a tally.

| Mechanism | The string | Disposition today |
| :--- | :--- | :--- |
| Config **key** names | the key itself | **Fatal.** `config.reportUnknownKeys` — this row is the model the rest are measured against. |
| `use_profiles` | the `<cli>` key | **Fatal** at `check` and at launch, against the installed-CLI universe. `config.validateUseProfiles`; retired spellings refuse by name. |
| `providers.*.wire_api` | the value | **Fatal**, closed enum (R4). `config.validateWireAPI`. |
| `providers.*.base_url` | the value | **Fatal** where the URL carries userinfo — a plaintext credential in a git-tracked file. `config.providerURLProblem`. |
| Contribution `kind` | the value | **Fatal** at load for a known build; **skip + report** across the version boundary. `packdecl.KnownKind` / `packdecl.DecodeTolerant`. |
| `supersedes.capability` | the capability name | **Structurally** fatal at load in `packdecl`; the **match** is reported, not refused (`loopholes.unmatchedSupersessions`, surfaced by `SupersessionProblems`). |
| `env_sources` | file paths | **Warn + skip**, and correctly so. `config.ResolveEnvSourcesFull`. |

> [!IMPORTANT]
> **The supersession match is the one mechanism that departs from R1–R5, and it is a known
> departure, not an oversight.** Where the refusal should land is unruled — it is
> [`../design/reference-mismatch-diagnostics.md`](../design/reference-mismatch-diagnostics.md)'s
> [`OQ-RM2`](../design/reference-mismatch-diagnostics.md#OQ-RM2) (refuse the launch, or refuse the
> pack?), and that doc is the design of record. Do not
> "fix" it by relocating the check before that question is answered, and do not read the table above
> as a to-do list.

The asymmetry worth internalising is what the table shows in one glance: **field names live in a
closed namespace and were enforced from the start; references to *components* were not.** Mistype a
key and validation names it; mistype a value that names a component and, historically, nothing
happened. That asymmetry is the entire subject of this principle.

## What this does not license

**It is not a mandate to refuse everything unresolvable.** `env_sources` is permissive and should
stay permissive: a host path is not a reference into a namespace, there is no candidate set to
suggest from, and absence on one machine is the portability case the field exists for. The rule
covers references into a **closed universe of named components**, not environment probes. By the
same token `loopholedecl.Requires` (`command_on_path`, `file_exists`) is not an instance of this
principle — it asserts facts about a host.

**It is not a licence to refuse across a version boundary from a place that cannot recover.**
That is R5's second test, and `DecodeTolerant` is the shipped case where skip-and-report is right.

**It does not make a warning acceptable because a refusal is inconvenient to place.** Inconvenient
placement is the finding, not the exemption.

**It says nothing about severity for values that are not references.** A malformed URL, a bad enum,
a missing required field are schema validation and were already fatal. This principle is only about
one component naming another.
