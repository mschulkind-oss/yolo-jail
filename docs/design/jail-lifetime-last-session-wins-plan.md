---
title: "Last session wins: the keeper at the container backends — retired hand-off"
date: 2026-09-30
status: accepted
stage: GRADUATED
next: "Nothing is owed here. Delete this pointer once the roadmap's link to it moves to the design"
tags: [plan, lifecycle, attach, sessions, keeper, graduated]
summary: "A pointer. This was the build hand-off for step 3 of jail-lifetime-last-session-wins.md, the keeper at podman and Apple Container. Step 3 landed at e343542d; the design's section 7 names its code and tests, and the traps this plan recorded moved there on 2026-10-01. The plan's map, reuse list and build order described the tree before the build and are in git history."
vantage:
  status-chip: true
---

# Last session wins: the keeper at the container backends — retired hand-off

**Status:** 2026-10-01 — the work this plan handed off is in the tree, so the plan is retired.
Read [`jail-lifetime-last-session-wins.md`](jail-lifetime-last-session-wins.md), which owns the
keeper:

- what was built, the code it lives in and the tests that pin it:
  [§7](jail-lifetime-last-session-wins.md#7-what-i-would-build-in-order), step 3;
- the traps the build met, moved from here: the bullet after that step's **Built** entry;
- what is still owed (the Mac runs of step 4, the keeper at `yolo host` and macos-user of
  step 5): the same section.

MEASURED in nested jails on Linux podman, by the integration tests that step names. UNMEASURED:
either Mac backend and a real rootless systemd host. The plan's map, its reuse list and its build
order described the tree as it was before the build; they are in git history
(`git log --follow -- docs/design/jail-lifetime-last-session-wins-plan.md`).
