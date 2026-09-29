---
name: ruling-walkthrough
description: Use when the maintainer wants to go through open design questions (OQs) quickly in chat instead of reading the docs — pre-draft them, present two at a time in plain words, record each ruling in its doc, and keep builds moving in parallel.
---

# Ruling walkthrough

Settle a backlog of open design questions (OQs, the `💬 <a id="OQ-…">` items in `docs/`) in a
live conversation, with low latency: the maintainer answers in chat, the agent records and builds.

## Before the first pair

1. **Pre-draft every pair in one delegated pass.** One agent reads each question in its doc
   (the question block, its options, its `_Leaning:_`, and any section it needs to state it
   accurately) and writes a personal, git-ignored file, `swarf/walkthrough-<date>.md`: an index,
   then each question in 8–14 lines — the question in one plain sentence, why it matters (what
   the user notices, what it unblocks), the real options with their costs, the leaning with its
   reason, and the trap (the thing easy to get wrong). It verifies every anchor exists and says
   which questions are already ruled.
2. **Fix missing anchors first.** A question with a `vantage: oq` directive but no
   `<a id="OQ-…">` cannot be linked or ruled cleanly; sweep for them (a script over `docs/`) and
   add them in each doc's existing format before starting.
3. **Order:** live defects first, then questions that release a build, then questions that only
   close a doc. Pair questions that decide one thing together.

## Presenting a pair

- **Lead each question with its setup**: the concrete situation that raises it, in two or three
  sentences a newcomer can picture ("a repo's `mise.toml` pins node 22.4; you have 20 installed;
  you run `yolo host -- pi` there…"), BEFORE the options. A question stated only in the doc's own
  terms ("the stale-shim verdict", "one consent covers the refresh") gets "I don't get this".
- **Two questions at a time**, in plain words, each with the leaning and what choosing it
  changes for the user. No doc jargon without a one-phrase definition. Say "rule both as leaned,
  or adjust?"
- **The pair is the LAST thing in the turn.** Status updates, landings and notifications come
  first; the two questions close the message, so they are what the maintainer sees when the
  turn ends. Never let a pair scroll by above later output.
- **When the maintainer asks "what is this / why", answer the premise, not the options.** A
  "why X at all?" usually targets the assumption under the options table; say what problem it
  arises from, then re-present the pair.
- **If the maintainer's answer is none of the options**, record his rule in his words as the
  answer ("ruled, narrower than the leaning" / "none of the options as written"), not the
  nearest option.

## Recording a ruling

For each ruled question, in its doc:
- flip `💬` to `✅`, remove its `<!-- vantage: oq … -->` directive, and fill `**Answer:**` with
  `> **Ruled in review <date>, as leaned:** …` (or "against the leaning", quoting him);
- add a Decision Ledger row (`pending` in Built until the build lands);
- update the doc's `Needs your ruling`/`Rulings` line and Status line;
- move its roadmap row (`docs/plans/roadmap.md`) from 💬 Needs you to 📦 Ready once nothing in
  that doc awaits a ruling;
- `uvx vantage-check@0.7.0 <doc> docs/plans/roadmap.md`, fix, then commit on main with a
  `docs(design): rule OQ-… …` message.

A comment arriving through Vantage review (`## Review Comments for …`) is the same ruling:
record it as above, then deliver one `.vantage/inbox/*.jsonl` line per comment acted on
(write to `.writing`, then `mv`), each with a fresh nonce, AFTER the doc is saved.

## Docs that arrive mid-walkthrough

A design doc another agent writes (pushed from the host, or landed by a teammate) joins the queue:
review it before presenting it. Run a workflow over it — independent lenses (evidence against
current main, incident logs such as `/ctx/host-yolo-logs`, design against the standing rulings,
coverage gaps, decidability) → one editor applying the confirmed findings and restating each
question walkthrough-ready → skeptics refuting the edited claims → a fix pass. Its questions then
enter the queue in plain words like the rest.

## A ruling that changes a doc's model

When a ruling replaces a doc's framework (not just picks an option), the doc's BODY still
describes the old one. Reconcile the body in the same pass (a delegated editor), and restate any
still-open sibling question whose options the ruling changed, before presenting it.

## Keep builds moving

- A ruling that releases a build: brief a builder agent immediately (worktree isolation,
  Opus), in parallel with the walkthrough. Partition concurrent builders by file area.
- A ruling that raises a new design question: fan out a design-doc agent; do not stall the
  walkthrough on it.
- Land each finished build yourself from the durable landing worktree
  (`/workspace/.claude/worktrees/land`, never `/tmp`): cherry-pick, restamp cited SHAs,
  `just check-ci` and the full integration suite once, ending the gate with `exit $rc` and
  reading its output before `git merge --ff-only`.

## Improving this skill

The process is adjusted as it runs. When the maintainer changes how the walkthrough should go,
edit this file in the same turn and commit it. Recorded adjustments:
- 2026-09-29: two questions at a time; the pair is always the last thing in the turn.
- 2026-09-29: a ruling that is none of the options is recorded in his words.
- 2026-09-29: a ruling that raises a follow-on question files it as a new OQ with a leaning
  (BB1 → BB6) and queues it, rather than leaving the gap in prose.
- 2026-09-29: every question opens with its concrete setup (the maintainer could not answer
  HP5/HP6 as first stated).
- 2026-09-29: with ultracode on, review/improve passes on arriving docs and doc reconciliation
  run as workflows, not single agents.
