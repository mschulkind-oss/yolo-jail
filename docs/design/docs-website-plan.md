---
title: "Docs website — retired implementation sketch"
date: 2026-09-25
status: accepted
tags: [docs, website, userguide, vantage, cloudflare, plan, graduated]
summary: "A pointer. This was the parking lot for build-level detail behind docs-website.md. Its four steps are done, and on 2026-10-01 what it settled (the installer fallback, the unshallowed clone, the gate wiring) moved into docs/reference/docs-website.md. The sketch's file list and order are in git history."
stage: GRADUATED
next: "Nothing is owed here. Delete this pointer once the roadmap's link to it moves"
---

# Docs website — retired implementation sketch

**Status:** 2026-10-01 — every step of this sketch is done, and what it settled is described in
[`../reference/docs-website.md`](../reference/docs-website.md): the build's `uvx` path and its
Python fallback, the unshallowed clone the history export needs, and the gate's three site checks.
MEASURED 2026-09-30, as the sketch recorded: the site served at `docs.yolo-jail.mschulkind.dev`
with HTTP 200, and its commit list named a guide change pushed to `main` the same morning. The one
question the design left open is [OQ-DW3](docs-website.md#OQ-DW3). The sketch's file list and
order described the tree before the build; they are in git history
(`git log --follow -- docs/design/docs-website-plan.md`).
