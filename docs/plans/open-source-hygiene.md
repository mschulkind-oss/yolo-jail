---
title: "Open-source hygiene: the four calls the 2026-09-26 audit left the maintainer"
date: 2026-11-12
status: in-review
stage: DESIGN
next: "The maintainer rules OQ-OS1 to OQ-OS4; each ruling is a small cleanup an agent can then land"
tags: [plan, open-source, hygiene, release]
summary: "On 2026-09-26 an open-source hygiene audit of this repository, run with the open-source-project skill, left four decisions to the maintainer. Its report is outside this repository, so until this doc the roadmap line was their only record. Each is filed here as a question."
---

# Open-source hygiene: the four calls the 2026-09-26 audit left the maintainer

**Status:** 2026-11-12. Four questions, all open. The audit's four leftovers were recorded on
2026-09-26 in row 67 of that day's roadmap (commit `5b6ff7f26`). Its report is not in this repository, so each question
below says what the tree shows and what only the report could say.

**Needs your ruling:** [OQ-OS1](#OQ-OS1), [OQ-OS2](#OQ-OS2), [OQ-OS3](#OQ-OS3) and
[OQ-OS4](#OQ-OS4).

The audit followed the `open-source-project` skill (a maintainer-wide skill, not part of this
repository), whose pre-release guidance supplies the rules two of the questions turn on.

## Open Questions

1. 💬 <a id="OQ-OS1"></a>**OQ-OS1: Which public research doc did the audit flag, and does it stay public?**

   The audit flagged one document under `docs/research/` to review before it stays public. Its
   report names it; the tree does not, and no commit message says which.

   - **A.** You name the doc from the report, and an agent reviews it for anything private and
     edits or removes it.
   - **B.** An agent sweeps every doc under `docs/research/` for private names and paths, and the
     report's pick is not needed.

   <!-- vantage: question id=OQ-OS1 leaning="A, if the report is at hand, since it names one doc and the reason; B otherwise." -->

   _Leaning:_ A, if the report is at hand, since it names one doc and the reason; B otherwise.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 <a id="OQ-OS2"></a>**OQ-OS2: Do the scratch-directory entries stay in the committed `.gitignore`?**

   The committed `.gitignore` ignores `.research/` and `/scratchpad/`, the second under the comment
   *"audit/test scratch artifacts (reviewer-created; never commit)"*. The skill's pre-release
   guidance puts a maintainer's own scratch directories in a global gitignore or
   `.git/info/exclude`, keeping the committed file to what matters to every contributor.

   - **A.** Move them out: delete both lines, and add them to your global gitignore.
   - **B.** Keep them, because agents working in this repository write there.

   <!-- vantage: question id=OQ-OS2 leaning="A for .research/, which no contributor's tooling writes; B for /scratchpad/ only if a tool in this repository writes it, which nothing found in the tree does." -->

   _Leaning:_ A for `.research/`, which no contributor's tooling writes; B for `/scratchpad/` only if a tool in this repository writes it, which nothing found in the tree does.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 <a id="OQ-OS3"></a>**OQ-OS3: Does this repository take the template's `last-release` recipe?**

   The skill's project template has two standard release recipes, `release` and `last-release`.
   This `Justfile` has `release` and no `last-release`. The release process here is its own
   ([`release-process.md`](../reference/release-process.md)).

   - **A.** Add `last-release`, adapted to this repository's release flow.
   - **B.** Do without it, and record why in the release process doc.

   <!-- vantage: question id=OQ-OS3 leaning="No leaning: what last-release would add here depends on the template's recipe body, which the audit read and this doc has not." -->

   No leaning: what `last-release` would add here depends on the template's recipe body, which the
   audit read and this doc has not.

   **Answer:**
   > _(empty — fill in when decided)_

4. 💬 <a id="OQ-OS4"></a>**OQ-OS4: When does the Python-version migration retire?**

   yolo stopped being a Python package in 0.7 (July 2026). `just install` still runs
   `yolo internal migrate-host`, which removes the old Python console scripts (`internal/hostmigrate`)
   so the Go binary can take their names. The roadmap line does not name this code; it is read here
   as the migration the audit meant.

   - **A.** Retire it at a named release, after which an old Python install gets
     `go install`'s own error.
   - **B.** Keep it while it costs nothing, since it finds nothing on a clean host and prints nothing.

   <!-- vantage: question id=OQ-OS4 leaning="B for now: it is small, silent on a clean host, and its removal saves little; revisit at 1.0." -->

   _Leaning:_ B for now: it is small, silent on a clean host, and its removal saves little; revisit at 1.0.

   **Answer:**
   > _(empty — fill in when decided)_

## Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
