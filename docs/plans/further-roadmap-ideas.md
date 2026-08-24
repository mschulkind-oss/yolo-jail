---
title: "Where the next roadmap items come from — seven candidates, and three rows to drop"
date: 2026-08-23
status: proposal
tags: [roadmap, docs, process, candidates]
summary: "The 📦 queue emptied by shipping, so this file says where the next items come from. Seven proposals from auditing every doc the roadmap points at — twice, the second time with five parallel agents, then adversarially re-checked — each with an explicit verdict. Plus three rows that should LEAVE the roadmap: a pointer is not a question and neither is a confirmation."
---

# Where the next roadmap items come from — seven candidates, and three rows to drop

**Status:** CANDIDATES, 2026-08-23 (updated the same evening after a five-agent verification pass
over the whole corpus). **Nothing here is committed, and nothing here is a task.** Every item is a
*proposal with a verdict* — this file deliberately carries **no open questions of its own**: an
entry here either has a verdict or does not belong. **Five say build** (§1), **two say rule first** (§2), and **three say
drop a row the roadmap already carries** (§4, §4a).

**The short version.** [`roadmap.md`](roadmap.md)'s 📦 queue is empty because the last item
shipped, not because the work ran out — so the interesting question is where the *next* items come
from. This file is the output of auditing every design doc the roadmap points at (2026-08-23) and
writing down what the audit itself kept tripping over — first alone, then again with five agents
reading in parallel. The strongest four are all the same shape: **a property this repo already
believes in, with no mechanism that would notice it being false** — an open question with no ID, a
status line that says NOTHING BUILT about code that shipped, a jail-blind check that reports the
wrong side, and a reproduction recipe that lives in prose instead of a test.

**How to use it.** Nothing here becomes work by being written down. An item earns a 💬 row in the
roadmap when you want it; until then this file is a shelf. **§4 and §4a are the parts that argue in
the other direction** — three rows already on the roadmap that I think should leave it.

**Reads with:** [`roadmap.md`](roadmap.md) (the live state; this file is deliberately NOT it),
[`BACKLOG.md`](BACKLOG.md) (the one implementable list for the packs cluster — an item lives there
once it is real), [`../design/retired-decisions.md`](../design/retired-decisions.md) (where a *no*
goes, so it stops being re-proposed), [`README.md`](README.md#keeping-this-corpus-honest--the-five-checks-so-they-are-re-runnable)
(the five sweeps §I1 proposes automating — run them by hand until it is built).

---

## 1. The five I would build

Ordered by what pays for itself soonest.

### I1. A decision-link checker — an open question with no ID is invisible

**The evidence is this audit, and the second pass made it much worse than the first.** Five docs the
roadmap points at held live questions carrying neither a status emoji nor an explicit ID, so no count
of "what is open" could be taken except by reading ~9,000 lines. Worse, IDs failed to resolve **in
both directions**: [`boundary-broker.md`](../design/boundary-broker.md) §10.6 called a fork in the
road *"the maintainer's call — see the B1b row in `roadmap.md`"*, and the roadmap cited `OQ-B1b` back
at the doc. Neither existed.

**Then the deeper pass found it is a whole vocabulary, not a few typos.** The roadmap was
restructured on 2026-08-17 from a lettered queue into states, and **five separate docs still cite
names it no longer holds** — `rows B1/B1b/B2/B3/B4`, `threads A–C`, `N3`, `ROADMAP open item 5`,
`ROADMAP item N`. Every one of those resolved to nothing for six days, in docs whose whole job is to
tell you where a decision lives. Alongside them, one audit pass corrected **13 stale file:line
anchors** in a single doc — no claim wrong, every pointer off.

**The proposal.** A test over `docs/` — the same shape as
`internal/entrypoint/shippedclients_test.go`, which pins three spellings of a binary list together:

1. every item under an `## Open Questions` heading carries a status emoji (💬 / 💬 🤷 / ✅ / 🔒) and
   a stable ID;
2. every `OQ-<ID>` the roadmap cites **resolves** to that ID in the doc it names;
3. every `roadmap.md` reference by *name* (`row X`, `thread Y`, `item N`) is refused outright —
   the roadmap holds states and IDs, and nothing else is citable;
4. every backticked **code path** (`internal/…`, `packs/…`, `cmd/…`) exists. A corpus sweep on
   2026-08-23 found **15 distinct nonexistent paths**, and the split is the interesting part: most
   are docs correctly *recording a deletion* (`internal/brokerrelay`, `internal/builder`,
   `internal/agents/*`), two are **another project's** source quoted in research
   (`internal/cdi/cdi.go` is ROCm's container-toolkit, with its URL two lines away), one is a
   deliberate placeholder (`internal/foo.go`), and one was **real rot** —
   `internal/entrypoint/prism_claude.go`, a writer deleted when the prism became the unconditional
   path. So the rule needs the same allowlist shape as rule 5, and the payoff is one real find per
   sweep rather than a wall;
5. every backticked SHA-shaped token resolves with `git rev-parse`. **Three phantom SHAs were in the
   corpus today** (`533ccc1`, `8e77580`, `7fad359c`) and each sat in a sentence offering itself as
   evidence. ⚠ **This rule needs an allowlist, and finding that out is half its value.** A final sweep
   returns **9 unresolved tokens out of 173, and 0 of them are false evidence**: six are upstream
   `flake.lock` nixpkgs revs (`241313f4`, `f13ff45a`) or other projects' commits quoted in research,
   and **three are the corrected phantoms being *named as* phantoms** in the prose that fixes them.
   A checker that cannot tell "cited as evidence" from "cited as a known-bad value" reports 9 where
   the answer is 0, and gets switched off in a week.

Rule 2 is the valuable half: it is a link checker for decisions rather than URLs. Rule 3 is the one
this session would have needed. **Rules 4 and 5 were both run by hand on 2026-08-23 before being
proposed** — together they found one dead writer path and three phantom SHAs, and taught the design
that both need an allowlist. A rule you have not run is a guess.

**Verdict: build it.** The only item in this file that would have paid for itself twice during the
audit that produced it.

### I1b. And the harder half — a doc that says NOTHING BUILT while its subject shipped

**Dangling links are the cheap failure. This is the expensive one**, because it does not look broken:

| Doc | Said | Actually |
| :--- | :--- | :--- |
| `pack-code-separation.md` | "NOTHING BUILT. 2026-08-15" | all four rulings in the tree |
| `loophole-activation.md` | broker jail wiring "🛑 blocked" | shipped 2026-08-19 |
| `image-staging-vs-baking.md` | "Nothing built" | C1 shipped, `--accept-flake-config` live |
| `rocm-passthrough-design.md` | "Draft / implementation-ready" | shipped in June |
| `program-kind-defects.md` | "No code changed" | all three defects fixed |
| `macos-revival…plan.md` | "nothing engineering-side fully open" | false; D1 retired, D2 reverted, D3 superseded |
| `BACKLOG.md` / `roadmap.md` | E3 "open, not urgent" | shipped 2026-08-15 |

Seven docs, one week's drift, every one of them a doc a reader consults precisely to learn what is
done. **A status line is a claim about the tree and nothing checks it.**

> [!NOTE]
> **A detector for this needs to accept where status actually lives.** Sweeping for a line-start
> `**Status:**` on 2026-08-23 left eight false positives: a status stated **mid-line** after a
> `**Date:**` (three runbooks and plans do this), a `> ## ✅ FIXED` callout instead of a status line
> (two handoffs), and two files that are pack *content* rather than docs. All eight carry their
> state perfectly well. **Do not "fix" the docs to suit the checker** — that is the tail wagging the
> dog, and it is how a corpus ends up uniform and unread.

**The proposal, and it is deliberately weaker than I1:** no mechanism can verify "is this built" in
general. What *can* be checked mechanically is **staleness of the claim itself** — a `**Status:**`
line whose ISO date is older than the newest commit touching the code paths the doc cites gets
flagged for re-verification, not for correctness. That converts an unbounded question into a queue.

**And the third wave found the shape that makes rule 4 pay for itself.**
`host-render-target.md` §1–§7 has ~20 anchors off by 90–440 lines, one naming a package that no
longer exists, and a quoted string that appears nowhere in `internal/`. Nobody would ever repair
that by hand — but a checker that *reports* it lets the doc carry an honest "treat these as where to
look, not as citations" warning, which is what it now does. **The output of these checks is often a
warning, not a fix**, and that is still worth having.

**Verdict: build the flag, not the verdict.** And in the meantime the cheap discipline is the one
this session used: when a sprint closes, re-read the status line of every doc it touched — the drift
clusters there rather than spreading evenly.

### I2. Report orphaned programs — a jail is the union of every pack ever selected

**Measured** in [`program-delivery.md`](../design/program-delivery.md) and reproducible in this jail:
`@github/copilot` and npm `fzf 0.5.2` are installed with **no selecting pack and no launcher** —
copilot was deselected, `fzf` came from a test pack that no longer exists. Dropping a pack removes
its launcher and **never uninstalls its program**. *(The first draft of this entry cited pi and codex
too; they are legitimately selected now, and re-measuring on 2026-08-23 is what caught it. The
finding is narrower and cleaner than it looked.)*

**The proposal is the report, not the removal.** `yolo check` (or `yolo pack ls`) names every
installed program with no selecting pack. Uninstalling is **OQ-PD4** and needs a ruling; *saying so*
needs none, and it turns PD4 from a judgement call into a question with data under it.

**Verdict: build the report now.** Cheapest thing in this file, and it makes a live 💬 row easier to
answer.

### I3. One jail-blindness concept in `check`, instead of three per-section rulings

> [!NOTE]
> **Half of this landed the day it was written.** The roadmap no longer holds these as three separate
> items — 💬 **10** is the single vocabulary question, and the recount that produced it found **ten**
> sections already stepping aside, not four. What is left of I3 is the ruling and the build.

Three items the roadmap used to hold separately are the same defect:

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
*print* — and note `check`'s reporter has exactly three verdict tokens and **no `[SKIP]`**, so the
ruling is really "does a fourth token exist". The AMD device section is the reference
implementation: it guards both of its checks, and its NVIDIA twin guards neither.

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

### I5. Release notes that have accumulated since the last release, and no cut in sight

[`../RELEASE-NOTES.md`](../RELEASE-NOTES.md) holds **eighteen** entries, every one of them under
`## Unreleased`, several carrying upgrade instructions that only make sense at a boundary
(*"restart the broker singleton after upgrading, or every OAuth refresh on that host fails"*).

> [!NOTE]
> **Corrected 2026-08-23 — my first draft of this entry said yolo "has never announced a release",
> and that is wrong.** There are **14 tags**, the newest being **v0.8.0 on 2026-08-13**, and
> `release.yml`/`publish.yml` are wired to the `v*` tag push. What is actually true is narrower and
> still worth deciding: the notes **file was created on 2026-08-18**, *after* that tag, so every
> entry in it has accumulated post-release and nothing has cut a version since. The check that
> disproved the stronger claim is `git tag --sort=-creatordate | head`, which cost nothing — a
> reminder that "there is no X" is the easiest kind of claim to get wrong.

**The question is what a cut should mean now.** Options: tag when the notes justify it (the file
becomes the trigger); tag on a cadence and let the notes fall where they may; or accept a rolling
upgrade log and rename the `## Unreleased` heading so the entries stop looking queued. **This is
yours** — I have no strong leaning, and the failure mode is quiet: a file this good is worth having
read by someone.

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

*(§4a generalises them into a class, found on the second pass.)*

The roadmap's 📦 note points here for this argument. Both of these are rows I believe we **invented**
rather than found — they read as decisions but nothing turns on either answer.

**a. `sectionRunningJails` has no in-jail guard** (now a bullet of 💬 **10**, after that row was
rebuilt around the vocabulary question). The row's own text concedes
the output is *"true of the runtime it can see"*, and the orphan-cleanup path underneath acts on that
same runtime. So the ruling being requested is about a **label**, on a line nobody has been misled by
in a recorded instance. **Drop it as a row and let I3 absorb it** — as one input to `check`'s
vocabulary question it is useful evidence; as a standalone decision it is a wording preference
wearing a decision's clothes.

**b. `pack-capabilities` OQ-CAP** (💬 **12**). The roadmap says it is *"a one-line deliverable that is
decided in all but name."* If that is true then it is not a question — it is either 📦 or it is
nothing, and asking for a ruling to confirm what is already decided spends the scarcest thing in this
project, which is your attention. **Drop it from 💬**: either queue the one line or retire the idea
to [`../design/retired-decisions.md`](../design/retired-decisions.md).

> [!NOTE]
> **The general shape, since it will recur.** A row belongs in 💬 when two answers lead to
> *materially different work*. Both rows above have one plausible answer and a small edit behind it.
> The cost of getting this wrong is not clutter — it is that **14** rows of "needs you" read as a
> 14-decision backlog when **three of them cannot change the work** (§4 a, §4 b, and the pointer in
> §4a). Twelve is the honest figure, and the cheap three are the ones a tired reader answers first —
> which is the worst possible order to spend attention in.

---

## 4a. A third row I would drop, added after the deeper pass

**§4 b is one instance; this is the class.** Any question whose doc says it is *"decided in all but
name"*, and any "question" that is really a **pointer** to a question living somewhere else.

- **The confirmation:** `pack-capabilities` OQ-CAP, uncontested since 2026-08-13. Still on the
  roadmap as 💬 **12**.
- **The pointer:** `boundary-broker` **OQ-D** — not a question at all but a redirect to
  `agent-auth-modes` OQ-1, and its own text says so. *(This one is already fixed: today's compaction
  moved it into that doc's Decision Ledger, so it no longer reads as open. It is kept here as the
  worked example of the shape.)*

The general rule, since it will recur: **a pointer is not a question, and neither is a confirmation.**
Both inflate the "needs you" count with items that cannot change the work, which is how a 14-row list
starts reading like a 14-decision backlog when the real number is smaller.

---

## 4b. What the verification pass changed about this file

*(Added 2026-08-23, last thing.)* Every idea above was written during an audit and then
**adversarially re-checked by agents told to disprove it**. That pass found: two commit SHAs in the
roadmap that do not exist, a count of "ten sections" that was ten *call sites* in nine sections, six
open questions invisible to the very command this file proposes standardising, a "sole caller" claim
with a second caller, and one of my own entries (I5) resting on "yolo has never cut a release" when
there are fourteen tags.

**That is the argument for I1 and I1b, made against this file rather than by it.** None of those
errors were reasoning failures; every one was a fact that had drifted or a check nobody ran. The
ideas above are worth what they are worth; the *method* — write it down, then pay someone to
disprove it — is what actually found things.

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
