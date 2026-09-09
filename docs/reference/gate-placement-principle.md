---
status: current
verified: 2026-09-09
verified_commit: 356bcec8
covers:
  - internal/packsrc/lock.go
  - internal/packload/hostaccessgates_test.go
  - internal/cli/run/packhostgrants.go
  - internal/config/userlayer.go
  - internal/config/packs.go
  - internal/image/autoload.go
tags: [principle, gates, trust, scope, security]
---

# Principle: put the gate where the authority changes

**Status:** PRINCIPLE, current as of 2026-09-09, verified against `356bcec8`.

**Audience:** anyone adding a confirmation prompt, an approval record, a scope restriction, a
signature check, or any other gate — and anyone deciding that an existing one is missing. Read this
before writing "and then we ask the user to confirm."

A principle keeps its rationale on purpose: a rule stripped to its verdict reads as arbitrary and
gets ignored, and this one is cited to *delete* things, which is the harder direction.

**Sibling principles:** [`extension-point-principle.md`](extension-point-principle.md) (who designs
an extension point), [`happy-path-principle.md`](happy-path-principle.md) (fill the matrix, don't
support every tool). Those two are about what to build; this one is about what *not* to.

---

## The principle

> **A gate earns its place only where the authority changes hands. If performing the guarded act
> already required at least as much authority as the gate protects, the gate is theatre — and worse
> than nothing, because it looks like protection while the real gap stays open.**

Two questions answer almost every case. Ask them in this order.

### Test 1 — the authority test: could this actor already do it?

Before adding a gate, name what the actor must already have in order to reach it. If that is at
least as much as the gate protects, delete the gate.

A confirmation dialog in front of "run this program on your machine", shown to someone who had to
edit a file in their own home to get there, protects nothing: that person could have written
`~/.bashrc` or a crontab and skipped the question entirely. The dialog does not raise the bar. It
raises the *appearance* of a bar, which is worse, because a reviewer reading the code sees a gate and
stops looking.

### Test 2 — the blast-radius test: trusted relative to what?

"Trusted", "user level" and "privileged" are not properties of a path. They are properties of a
**relationship between an actor and what that actor can destroy**.

The same file is a credential boundary in one context and ordinary content in another. `~/.config` on
your laptop is user scope because ruining it ruins your machine. The same path inside a container is
just a file in a box you can throw away — so the container's occupant may own it, and nothing is lost
if they wreck it.

**Scope names are roles, not locations, and they rebind as you nest.** Inside a jail, the jail *is*
the machine: its occupant plays the role the human plays outside, because the jail is the blast
radius. A design that hardcodes "user level = the human's home directory" will be wrong the first
time it runs one level in.

## The corollary that generates the real work

**When a gate turns out to be theatre, the useful move is not to delete it and stop. It is to ask
which actor you were actually worried about** — because there usually is one, and the theatre was
hiding it.

Theatre appears when a gate is aimed at the wrong actor: the one who already had the authority. The
threat you had in mind was someone else, with less. Name that second actor, and the right gate — a
smaller, cheaper, structural one — usually falls straight out.

## Worked examples, all from this repo

**A confirmation that was theatre, and the real gap behind it.** A proposal to digest every file a
loophole would execute, and re-confirm when the digest changed, dies to Test 1: installing a loophole
means editing your own user config, which already requires host access as you. But the *finding*
underneath was real — an **agent** can rewrite a daemon that lives in a live-mounted workspace, and
an agent has none of that authority. Two actors, one of whom the gate was never about. The rule that
survives is structural and costs a path comparison: installed content may not live where an agent
writes ([`loophole-system.md`](loophole-system.md#the-placement-rule)). The theatre is gone and the
hole is closed, which is the opposite of the usual trade.

**A "you can already read this" argument used to justify execution.** yolo trusts `file://` pack
content because it is the user's own files, which they can already read. Sound for a gate about
**reading**. The same sentence was carrying a gate about **running**, where it does not hold — an
agent that can write those files has escalated, not merely read something it already could. Test 1
must be applied to the verb the gate actually guards.

**A restriction that is NOT theatre, for a reason worth internalising.** `packs`, `host_files` and
`cache_relocations` are user-scope-only. That looks superficially similar to the case above — both
are "only a trusted file may say this" — but here the actor genuinely changes: a workspace config
travels with a repo and is agent-editable, so allowing the key would hand an agent something it could
not otherwise get (`internal/config` — `userlayer.go`'s header comment states the test, and
`packs.go` states the widening rule it produces). Same shape, opposite verdict, and the difference is
entirely Test 1.

**A gate placed one step too late.** Loopholes split into *install* (this code may run here at all)
and *enable* (this jail uses it). The interesting result is that enable needs **no** gate, even
though enable is what starts the daemon: by then the content was already vetted at install, so an
agent flipping it on gains nothing it was not already given. One gate, at the step where the
authority changed — not one per scary-sounding verb.

**A prompt any pipe defeats — and the stronger objection it hid.** `yes | yolo pack install` answered
the fetched-pack approval prompt. A gate that a shell pipeline dismisses is theatre against anything
automated; if the answer must come from a person, it has to require a terminal and fail closed
without one. A gate that cannot tell a human from a pipe is not asking a human.

> [!IMPORTANT]
> **That criticism was true and too small, and the lesson is about reading rather than about packs.**
> The prompt was deleted outright
> ([`OQ-TP9`](../design/trust-paths.md#decision-ledger) in
> [`../design/trust-paths.md`](../design/trust-paths.md)), because it fails **Test 1** at the top of this document: selecting a pack means writing
> `packs` in the user config as the host user, which already exceeds everything the prompt withheld.
> The *pipe* objection is a narrow ground, and a gate criticized on a narrow ground looks
> **examined** — which stops the next reader from asking the authority question at all. When a gate
> appears in a worked-example list, run Test 1 on it explicitly, even if some other objection already
> applies. What replaced the prompt is disclosure, pinned by
> `packload.TestTheDisclosureThatReplacedTheGateIsStillWired`.

**Scope, rebinding one level in.** A nested jail's "user level" is the outer jail, because the outer
jail is what an inner-jail loophole can damage. Its agent may legitimately own that scope — the same
agent must not touch the human's. This is Test 2 in its purest form, and it is why the scope model
recurses instead of pointing at a fixed path.

## The artifact form: a name that states a guarantee

A gate is not the only thing that can be theatre. **A persisted field, a file name, or a config key
can assert a property nothing enforces**, and it fails the same way — a reader sees it and stops
looking.

The worked example is the pack lockfile's approval record. `ApprovedAt` was written on every install
and read by nothing; the approval check compared claim strings and never consulted it. But it lived
in a *trust* file and its name said the approval was anchored to a commit, so anyone reading that
lockfile — or building the next gate against it — would reasonably conclude the anchoring existed.
`ApprovedAt` went first, for exactly that reason; `ApprovedHostAccess` and `HostAccessApproved` went
with the prompt when Test 1 deleted it. The lockfile now carries **no** approval record at all, and
that absence is a ruling: `packsrc.LockEntry`'s header says so, and
`packsrc.TestTheLockfileHoldsNoApprovalRecord` goes red if any of it comes back.

**The test is the same one, applied to the artifact instead of the code path:** does the name assert
something the system enforces? If not, the honest options are to enforce it or to delete it. There is
no third option where it stays as documentation, because a field is not read as documentation — it is
read as a fact about the system.

> [!WARNING]
> **Pin the gap with an assertion, not a placeholder.** Where a hole is known and deliberately not
> closed, write a test that fails if the behaviour changes: it records the gap *and* stays true. A
> half-built field records it and lies. Do not reintroduce a persisted approval field to "document"
> an approval nothing enforces — decoding tolerates the stray key from an old lockfile precisely so
> that removal costs no compatibility.

**This is where YAGNI and the extension-point principle divide cleanly.**
[`extension-point-principle.md`](extension-point-principle.md) protects designed extension
*surfaces* from YAGNI — a manifest field a third party will write, whose semantics we must own
before someone else invents them. It does not protect a half-finished implementation with no reader,
no third-party contract, and no caller waiting on it. When in doubt, ask who would be stuck if it
were absent: a stranger who cannot express something, or nobody.

## What this principle does NOT say

**It is not an argument against defence in depth.** Two gates against the *same* actor can both be
worth having when they fail differently. The principle is about gates aimed at an actor who is
already past them.

**It is not "an attacker could do it anyway, so why bother."** That reasoning is how real boundaries
get removed. The question is never "could *someone* do this" but "could **this actor**, at this
moment, without the gate." If the actor differs, the gate stands.

**It is not licence to skip a gate because the mechanism looks similar to one that was theatre.**
Two of the examples above look identically like "only a trusted file may declare this." One is
theatre and one is load-bearing. Only Test 1, applied to the specific actor and the specific verb,
tells them apart.

**Visibility is not a gate, and is not subject to this test at all.** Telling someone what is
happening on their machine has value even when they already authorised it — the disclosure is not
pretending to stop anything. Do not delete a message because the act behind it was permitted.

**It does not say a gate must identify the human — sometimes the right actor test is a
DECLARATION.** A sibling formulation gets quoted a lot — *"a gate that cannot tell a human from a
pipe is not asking a human"* — and it is right about **prompts**. It is wrong wherever the thing that
makes an act safe is not *who* is present but that somebody **SAID** the dangerous precondition
holds.

The worked case is the stale-image launch
([`image-staging-vs-baking.md`](image-staging-vs-baking.md#a-failed-build-is-fatal),
[`OQ-2`](image-staging-vs-baking.md#why-its-this-way)). The design's own leaning was to prompt an
interactive human and refuse a pipe; the shipped code refuses **both** and takes an env var as the
way past. The reason generalises: what makes running on a stale image safe is knowing the image *is*
stale — **precisely the knowledge whose absence caused the bug** — and a TTY test proves presence,
not knowledge. The asymmetry decides it: refusing costs a rerun with one env var; continuing costs an
investigation two layers from the cause.

> [!WARNING]
> **Do not "fix" `internal/image` to consult a TTY on the strength of this principle.** The
> divergence is deliberate and argued at the `currentPath == ""` branch in `autoload.go`. The test to
> apply is the one above: is the missing thing *presence* or *knowledge*?
