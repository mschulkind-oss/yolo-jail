---
title: "Where the next roadmap items come from — eight candidates, ranked, two of them arguments to build less"
date: 2026-08-23
status: draft
tags: [roadmap, docs, process, candidates]
summary: "The 📦 queue emptied by shipping, so this file says where the next items would come from. Eight candidates found by auditing every doc the roadmap points at, each with an explicit verdict — four worth building, two worth a ruling first, two I would drop. Plus §4: two rows already ON the roadmap that I think are questions we invented."
---

# Where the next roadmap items come from — eight candidates, ranked

**Status:** CANDIDATES, 2026-08-23. **Nothing here is committed, and nothing here is a task.**
Every item is a *proposal with a verdict*; four say build, two say rule first, two say drop.

**The short version.** [`roadmap.md`](roadmap.md)'s 📦 queue is empty because the last item
shipped, not because the work ran out — so the interesting question is where the *next* items come
from. This file is the output of auditing every design doc the roadmap points at (2026-08-23) and
writing down what the audit itself kept tripping over. The strongest three are all the same shape:
**a property this repo already believes in, with no mechanism that would notice it being false** —
an open question with no ID, a jail-blind check that reports the wrong side, a reproduction recipe
that lives in prose instead of a test.

**How to use it.** Nothing here becomes work by being written down. An item earns a 💬 row in the
roadmap when you want it; until then this file is a shelf. **§4 is the part that argues in the other
direction** — two rows already on the roadmap that I think should leave it.

**Reads with:** [`roadmap.md`](roadmap.md) (the live state; this file is deliberately NOT it),
[`BACKLOG.md`](BACKLOG.md) (the one implementable list for the packs cluster — an item lives there
once it is real), [`../design/retired-decisions.md`](../design/retired-decisions.md) (where a *no*
goes, so it stops being re-proposed).

---

## 1. The four I would build

Ordered by what pays for itself soonest.

### I1. A decision-link checker — an open question with no ID is invisible

**The evidence is this audit.** Five docs the roadmap points at held live questions that carried
neither a status emoji nor an explicit ID, so no count of "what is open" could be taken except by
reading all 8,000 lines. Worse, IDs failed to resolve **in both directions**:
[`boundary-broker.md`](../design/boundary-broker.md) §10.6 called a fork in the road *"the
maintainer's call — see the B1b row in `roadmap.md`"*, and the roadmap cited `OQ-B1b` back at the
doc. Neither existed. A decision with two dangling pointers is a decision nobody can make.

**The proposal.** A test over `docs/` — the same shape as
`internal/entrypoint/shippedclients_test.go`, which pins three spellings of a binary list together:

1. every item under an `## Open Questions` heading carries a status emoji (💬 / 💬 🤷 / ✅ / 🔒) and
   a bold stable ID;
2. every `OQ-<ID>` the roadmap cites **resolves** to that ID in the doc it names.

Rule 2 is the valuable half: it is a link checker for decisions rather than for URLs. Rule 1 is what
makes rule 2 cheap.

**Verdict: build it.** It is the only item in this file that would have paid for itself during the
audit that produced the file.

### I2. Report orphaned programs — a jail is the union of every pack ever selected

**Measured** in [`program-delivery.md`](../design/program-delivery.md) and reproducible in this jail:
the config is `"packs": ["claude"]`, and `~/.npm-global/lib/node_modules/` holds `pi`, `copilot`,
`codex` and a stray `fzf` from a pack that no longer exists. Dropping a pack removes its launcher and
**never uninstalls its program**.

**The proposal is the report, not the removal.** `yolo check` (or `yolo pack ls`) names every
installed program with no selecting pack. Uninstalling is **OQ-PD4** and needs a ruling; *saying so*
needs none, and it turns PD4 from a judgement call into a question with data under it.

**Verdict: build the report now.** Cheapest thing in this file, and it makes a live 💬 row easier to
answer.

### I3. One jail-blindness concept in `check`, instead of three per-section rulings

Three items the roadmap holds separately are the same defect:

| Where | What it does in a jail |
|---|---|
| `sectionRunningJails` (`check.go:514`) | reports the **nested** podman's view; prints `[PASS] No jails currently running` while the host has one |
| `sectionGPUNvidia` (`sections_devices.go:38`) | three `[FAIL]`s for host facts read from the wrong side |
| broker-ca **OQ-3** | `[PASS]` on a section that was *skipped* — the shape that hid a daemon that never started |

Each is currently a small question about one section's wording. **They are one question about
`check`'s vocabulary:** a section knows whether its facts are host-authoritative, jail-observable, or
both, and today it has no way to say so — so every section re-decides it by hand and two of the three
above decided it wrong.

**Verdict: worth one ruling, then build.** The ruling is what a jail-observable section should
*print* (a fourth verdict beside PASS/FAIL/SKIP, or a scope suffix). Answering it collapses three 💬
items into one and takes the AMD twin's existing guard as the reference implementation.

### I4. Make the reachability class reproducible in CI, not only in prose

[`AGENTS.md`](../../AGENTS.md) carries an exact bare-`podman run` reproduction of the loopback-TLS
outage — bind a listener on this jail's own loopback, dial it from a container using the stack under
test, watch `FAIL` become `CONNECT` when the flag is added. It is measured, dated, and lives in a
Markdown file. Meanwhile `integration/reachability_test.go` opens with a **warning** that a nested
jail is structurally blind to this class.

**The proposal.** Promote the recipe to an env-gated integration test. The jail is the "host" for a
container it starts, so the class reproduces from inside — which is precisely the property the
warning says a *nested jail* lacks and the bare-podman path has.

**Verdict: build it.** A total loopback-TLS outage shipped and went unnoticed for four days for want
of exactly this test, and the reproduction is already written down.

---

## 2. The two that need a ruling before they are anything

### I5. Release notes that have never announced a release

[`../RELEASE-NOTES.md`](../RELEASE-NOTES.md) holds **seventeen** entries, every one of them under
`## Unreleased`, several carrying upgrade instructions that only make sense at a boundary
(*"restart the broker singleton after upgrading, or every OAuth refresh on that host fails"*). There
is no version heading in the file and no release process doc in the repo — distribution today is
`just install` plus a nix image.

**The question is what a yolo release even is**, and it is genuinely yours: a tag, a
`just load` on your own machines, or nothing at all — in which case the file should say it is a
running upgrade log rather than release notes, and the upgrade instructions should be dated instead
of versioned.

**Verdict: needs a ruling; I have no strong leaning.** Worth noting the failure mode is quiet — a
file this good is worth having read by someone.

### I6. The `mise` lockfile, which is the cheapest win and still the wrong first move

`mise` supports a lockfile; yolo never enables it and there is no mise lockfile in the tree. The
roadmap calls this *"the cheapest single win, if you want one before ruling"*, and it is.

**Verdict: hold it anyway.** **OQ-PD6** asks precisely whether the declaration carries the pin or the
receipt does, and enabling mise's own lockfile is an answer to that question, entered without
answering it. If PD6 rules *receipt*, the mise lockfile is in the wrong place and will have to move —
so the cheap win costs its own rework. Do it the moment PD1/PD2/PD6 land, in whichever shape they
say.

---

## 3. What this file does NOT propose

- **No new subsystems, no new config keys, no new pack kinds.** Every item above is a report, a
  test, or a ruling on wording.
- **Not a queue, and not a commitment.** [`roadmap.md`](roadmap.md) is the only file that says what
  is being built. Nothing here has a position in it.
- **No re-litigation of what shipped.** The broker's move, the npm ruling, the refused-contribution
  fatal and the darwin warmup skip are all done; where one of them left a residue, the residue is a
  row in the roadmap, not an entry here.
- **No archaeology-driven work.** A doc being old is not a reason to change it. §5's archiving idea
  is deliberately the weakest thing in this file for exactly that reason.

---

## 4. Two rows already on the roadmap that I would drop

The roadmap's 📦 note points here for this argument. Both of these are rows I believe we **invented**
rather than found — they read as decisions but nothing turns on either answer.

**a. `sectionRunningJails` has no in-jail guard** (💬 8, third bullet). The row's own text concedes
the output is *"true of the runtime it can see"*, and the orphan-cleanup path underneath acts on that
same runtime. So the ruling being requested is about a **label**, on a line nobody has been misled by
in a recorded instance. **Drop it as a row and let I3 absorb it** — as one input to `check`'s
vocabulary question it is useful evidence; as a standalone decision it is a wording preference
wearing a decision's clothes.

**b. `pack-capabilities` OQ-CAP** (💬 9). The roadmap says it is *"a one-line deliverable that is
decided in all but name."* If that is true then it is not a question — it is either 📦 or it is
nothing, and asking for a ruling to confirm what is already decided spends the scarcest thing in this
project, which is your attention. **Drop it from 💬**: either queue the one line or retire the idea
to [`../design/retired-decisions.md`](../design/retired-decisions.md).

> [!NOTE]
> **The general shape, since it will recur.** A row belongs in 💬 when two answers lead to
> *materially different work*. Both rows above have one plausible answer and a small edit behind it.
> The cost of getting this wrong is not clutter — it is that eleven rows of "needs you" read as an
> eleven-decision backlog when the real number is nine, and the two cheap ones are the ones a tired
> reader answers first.

---

## 5. The weakest idea in the file, kept because it is nearly free

**Archive a plan when its sprint ends, not when someone notices.** At least eight of the thirty-three
files in `docs/plans/` announce their own completion in the first six lines —
`pack-host-management-plan.md` (*"ALL PHASES SHIPPED 2026-08-02"*), `host-pack-drop-cleanup.md`
(*"ALL FOUR RULINGS SHIPPED 2026-08-03"*), `host-file-staging.md` (*"✅ SHIPPED 2026-07-25"*),
`feedback-real-pack-adoption.md` (*"ALL SEVEN FINDINGS CLOSED"*), `cache-relocation.md`
(*"Implemented 2026-07-21"*), `module-consolidation-and-cleanup.md`, `agent-settings-composition.md`
and `handoff-fzf-pack-adoption.md`. The triage that produced
this layout ([`doc-triage.md`](doc-triage.md)) ran once, in July, and was executed properly; nothing
has swept since.

**Verdict: do the cheap half only, and only when passing through.** A `docs/plans/archive/` move is
mechanical, but a shipped plan is still the best account of *why* something is shaped the way it is,
and the repo has already been bitten by treating "old" as "safe to delete". If a sweep happens, the
rule that matters is the one [`retired-decisions.md`](../design/retired-decisions.md) already states:
a decision *not* to build never leaves, whatever happens to the plan around it.
