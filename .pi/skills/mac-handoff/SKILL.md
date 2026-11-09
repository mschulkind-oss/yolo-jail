---
name: mac-handoff
description: Use when a change needs verification only a Mac can give (macos-user, Seatbelt, Apple Container, darwin-only paths), when writing Mac checks for the owner's Mac agent, or when picking up a Mac lab report.
---

# Hand work to the Mac, and take it back

The owner runs a Mac agent. Work reaches it through **documents on main**, and its results come
back as **lab reports**. Nothing else carries a Mac handoff.

## The rules

- **No branches.** Never push a branch, a tag or a bundle for the Mac, and never hold code off
  main waiting for a Mac check. The owner, 2026-11-03: *"why are you pushing branches? just
  update the design doc like normal"*; 2026-11-09: *"we don't use branches for this."*
- **Land first, on the Linux gate.** The code lands on main after the usual landing gate
  ([AGENTS.md, Workflow](../../../AGENTS.md#workflow)) and its review. The Mac agent tests
  main. If a Mac check fails, fix it forward with a new commit, as with any other defect.
- **Never ask a person to run Mac checks.** Not a colleague, not the bug's reporter. The
  owner's Mac agent runs them.
- **A plan never says "do not land before the Mac check".** Where a plan says so, the plan is
  wrong: rewrite the gate as "the Mac check runs on main after landing".

## Write the Mac checks (outgoing)

Put them in the design doc that owns the change, or in its companion implementation plan, under a
`## Mac checks` section (examples: [`named-setups-plan.md`](../../../docs/design/named-setups-plan.md#mac-checks),
[`mcp-auth-sharing.md`](../../../docs/design/mcp-auth-sharing.md#mac-checks)). Each check is a
**witness**: a named, numbered step list another agent can run cold.

- **An id** in the doc's own series (`W-SU4`), and the slice or decision it settles.
- **The commit:** "at main `<sha>` or later", the first main commit that contains the change.
  Never a branch name.
- **Setup:** where to run it (a fresh workspace under `/Users/Shared/yolo/<x>`), what must
  exist first, and which build (the installed `yolo`, or a dev bundle built from main).
- **Steps:** exact commands, each alone on its own line, with the **expected output** of each,
  quoted where the text matters. Include the negative cases the decision implies.
- **What to report back:** the outputs to paste and the measurements to take.
- **What it decides:** the ledger row or open question the result settles.

Write them by [the plain-language principle](../../../docs/reference/plain-language-principle.md).
Check every command and expected line against the code on main before writing it: a witness
that expects text the code never prints fails a correct build. Then tell the owner the doc,
the section and the commit, in one short message. Do not restate the steps in chat.

## Take the results back (incoming)

The Mac agent files a [lab report](../../../docs/lab-reports/README.md),
`docs/lab-reports/YYYY-MM-DD-mac-<slug>.md`: the machine (OS, architecture, runtime versions),
the main commit it ran at, an Observed table with one row per witness (passes, fails, or cannot
run yet, and why), and the evidence.

When you pick one up:

1. **Verify before believing.** Re-read each claimed failure against the code at that commit.
2. **Fix forward.** Each real failure gets a failing test first where one can be written on Linux,
   then the fix, on main.
3. **Fold the results in.** Record each witness's outcome in the owning doc: the ledger row's
   Built column or note, the plan's status line, the open question it settles. A witness that
   must be re-run after a fix gets its new "at main `<sha>` or later".
4. **Retire the report.** Once everything in it has a home, `git rm` it (git history is the
   archive), and repoint any link to it.
