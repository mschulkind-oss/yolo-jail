---
title: "The user guide becomes a website — the one question left"
date: 2026-09-25
status: in-review
tags: [docs, website, userguide, vantage, cloudflare, deploy, graduated]
summary: "A stub. The built site graduated on 2026-10-01 to docs/reference/docs-website.md: the closed userguide/ tree, the pinned Vantage build, the static-assets Worker, Workers Builds as the one deployer, the gate, and the rulings OQ-DW1, OQ-DW2 (DW-D1) and DW-D2. What stays here is OQ-DW3, filed 2026-10-01 from the question the design's layout section left waiting on OQ-DW1: whether runtime messages name the site's URL rather than a userguide/ repository path."
stage: GRADUATED
next: "Rule OQ-DW3: whether yolo's runtime messages name the guide's URL on the site instead of a userguide/ repository path"
vantage:
  status-chip: true
---

# The user guide becomes a website — the one question left

**Status:** 2026-10-01 — the settled body of this design graduated to
[`../reference/docs-website.md`](../reference/docs-website.md), which is the authority for how the
guide is built, served and deployed, and where [`OQ-DW1`](../reference/docs-website.md#oq-dw1),
[`OQ-DW2`](../reference/docs-website.md#oq-dw2), [`DW-D1`](../reference/docs-website.md#dw-d1) and
[`DW-D2`](../reference/docs-website.md#dw-d2) now resolve. The companion implementation sketch,
[`docs-website-plan.md`](docs-website-plan.md), is retired to a pointer: its four steps are done. This file holds the one
question the design left open, filed with an id on 2026-10-01. The design's argument, its
alternatives and its measurement of Vantage's setup are in git history
(`git log --follow -- docs/design/docs-website.md`).

**Needs your ruling:** [OQ-DW3](#OQ-DW3) (whether runtime messages name the guide's URL).

## Open Questions

1. 💬 <a id="OQ-DW3"></a>**[OQ-DW3](#OQ-DW3): Do yolo's runtime messages name the guide's URL on
   the site, instead of a `userguide/` repository path?** The design's layout section repointed
   every message that named the old guide directory at the new repository path and left whether
   they should name a URL waiting on [`OQ-DW1`](../reference/docs-website.md#oq-dw1), which named
   the address on 2026-09-25. Six messages name a guide page by repository path at `d4e435a3`:
   `internal/loopholes/retired.go:118`, `internal/runtime/machineshares.go:353`,
   `internal/cli/check/sections_macos_platform.go:213` and `:262`,
   `internal/cli/run/jailprefix.go:361`, and `internal/cli/config_ref.txt:565`. This decides what
   a user is told to read when yolo sends them to the guide, and most installs (Homebrew, the
   release archive) have no checkout for a repository path to name.

   Options: **(a)** name the page's URL on the site; **(b)** keep the repository path; **(c)**
   name both, the URL first.

   Two facts bear on it. The site tracks `main`, so a URL printed by an older release can show a
   page describing newer behavior than the installed build; a repository path has the same
   problem for anyone reading it on GitHub, and names nothing at all without a checkout. And a
   page's own address does not load today: MEASURED 2026-10-01, a path-shaped deep link
   (`/guides/macos`, `/README.md`) on `docs.yolo-jail.mschulkind.dev` returns HTTP 500 with
   Cloudflare's error 1101, a Worker exception, while `/` returns 200; a deep link on
   `docs.vantageapp.dev` fails the same way. So (a) and (c) need a page address shown to load
   first, and a check that each named page exists under `userguide/`, since the closed-tree check
   reads links, not message strings.

   <!-- vantage: oq id=OQ-DW3 leaning="(a), once a page's own address is shown to load; until then (b) ships, since a URL that returns an error is worse than a path." -->

   _Leaning (agent-drafted, 2026-10-01):_ **(a)**, once a page's own address is shown to load.
   The message is read by whoever ran yolo, and for most of them a repository path names nothing.
   Until a deep link loads, (b) is what ships: a URL that returns an error is worse than a path.
