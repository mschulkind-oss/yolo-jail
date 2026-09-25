---
title: "Docs website — implementation sketch"
date: 2026-09-25
status: draft
tags: [docs, website, userguide, vantage, cloudflare, plan]
summary: "Parking lot for build-level detail behind docs-website.md: file list, the installer question for the Cloudflare build image, the gate wiring, and the order. Not a hand-off; the design wins on behavior."
---

# Docs website — implementation sketch

**Status:** The repository build described here is in place, 2026-09-25. The Cloudflare dashboard connection is still a human step. [OQ-DW1](docs-website.md#OQ-DW1) is ruled; [OQ-DW2](docs-website.md#OQ-DW2)'s leaning was implemented provisionally. The [design](docs-website.md) wins on behavior.

**Precedence:** [`docs-website.md`](docs-website.md) wins on behavior. This file holds settled detail
the design doesn't need.

---

## Files

- `userguide/`: the pages in [the design's §2.1](docs-website.md#21-layout). Use `git mv` for the three
  whole-file moves so `git log --follow` keeps their history. Whether the site's history view follows
  renames is unchecked; read `vantage build`'s git export before promising readers any pre-move
  history.
- `scripts/build-site.sh`: carries the header comment copied from Vantage's. Uses `set -euo pipefail`,
  deletes `dist/docs`, runs `vantage build userguide/ -o dist/docs -n "YOLO Jail User Guide"`, and
  pins the version in one variable.
- `docs-wrangler.toml`, `docs-worker.js`: copied from Vantage at `4b2ccbc`, with `name` changed.
- `AGENTS.md`: gains a line in the style of Vantage's AGENTS.md line on `Workers Builds: vantage`,
  plus a **Where things live** row for the guide.
- `README.md`: its guide links (lines naming `docs/guides/…`) are repointed. Once
  [OQ-DW1](docs-website.md#OQ-DW1) is ruled, add a site link.

## The installer in the build image

Find out what Cloudflare's Workers Builds image carries before choosing. `uvx --from vantage-md==0.7.0
vantage build …` works in this jail (verified 2026-09-25). If the image has no `uv`, then
`pip install vantage-md==0.7.0` works if it has Python, and a release tarball works with neither.
Check Cloudflare's current build-image docs; don't assume.

Also check the clone depth. `vantage build` pre-renders history, and a shallow clone would show one
commit per file. If the depth is shallow and not configurable, `git fetch --unshallow` at the top of
the script.

## Gate wiring

- `lint-ci` gains the site checks. `vantage-check` needs `uv` on CI's runners, so check `ci.yml`'s
  setup before relying on it.
- The closed-tree check can be a short script that walks `userguide/**/*.md` links; a Go test is
  also fine. Either way it must fail on a `../` that climbs out of the tree.
- The output-directory agreement check reads both files. Keep it dumb.

## Order

1. Create `userguide/` by moving and splitting, repoint every inbound mention, and add the
   closed-tree check. That is one commit, so the gate is never red between them.
2. Add the build script, the Worker files and the `AGENTS.md` line.
3. Human: create the Worker in the Cloudflare dashboard and connect the repo, with the commands from
   [the design's §3.3](docs-website.md#33-cloudflare-workers-builds-the-one-deployer).
4. Human: attach `docs.yolo-jail.mschulkind.dev` in the Cloudflare dashboard. Runtime messages still name repository paths until the domain is serving.
