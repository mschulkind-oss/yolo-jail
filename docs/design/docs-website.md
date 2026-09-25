---
title: "The user guide becomes a website, deployed exactly the way Vantage deploys its own"
date: 2026-09-25
status: draft
tags: [docs, website, userguide, vantage, cloudflare, deploy]
summary: "A learner-facing user guide, published as a static site built by Vantage and served by a Cloudflare static-assets Worker that Workers Builds redeploys on every push to main. The setup, the directory layout and the page organization are copied from the Vantage repo, file for file. Two decisions are yolo-jail's own: the guide stops being docs/guides/ and becomes userguide/, a closed tree whose relative links never leave it, and Vantage comes from a pinned PyPI release instead of being built from source. Two questions are open: the address, and which reference pages cross into the guide. Nothing is built."
---

# The user guide becomes a website, deployed exactly the way Vantage deploys its own

**Status:** DESIGN, 2026-09-25. Nothing built. The Vantage claims were checked against
`mschulkind-oss/vantage` at `4b2ccbc` and against the live sites on this date. The yolo-jail claims
were checked against `7e529260`.

> **In short.** The user guide becomes a published site by copying Vantage's setup whole: a
> `userguide/` tree, one build script, one static-assets Worker and a dashboard-configured Cloudflare
> build. This repo adds exactly one rule of its own: the guide is a **closed tree**, so what readers
> see on the site is what the gate checked.

**Why it matters.** Today the guide is a
[1,790-line single file](../guides/USER_GUIDE.md) readable only on GitHub, and a learner meets it
beside 143 design, plan and research docs. Vantage already solved this for itself, and
its solution has run long enough to have failed once in a way worth copying the fix for
([§3.4](#34-the-one-failure-vantage-already-paid-for)).

**The shape.** [`userguide/`](#2-the-guide-a-closed-tree-in-vantages-organization) (the content) →
[`scripts/build-site.sh`](#31-the-build-script-the-one-entry-point) (runs `vantage build`) →
`dist/docs/` → [a static-assets Worker](#32-the-worker) (serves it), with
[Cloudflare Workers Builds](#33-cloudflare-workers-builds-the-one-deployer) as the only thing that
deploys.

**Cost.** `docs/guides/` is deleted. Its 101 mentions across 60 files, some in runtime messages,
are repointed, and about 50 distinct link targets that leave the guide tree are rewritten
([§2.2](#22-the-closed-tree-rule)).

**Start at [§2.2](#22-the-closed-tree-rule).** That rule is the only design decision that isn't a
copy of Vantage's.

**Needs your ruling:** [OQ-DW1](#OQ-DW1) (the address), [OQ-DW2](#OQ-DW2) (which reference pages
cross in).

**Reads with:** [`docs-website-plan.md`](docs-website-plan.md) (the implementation sketch: incomplete,
and not to be built from), and Vantage's
[`static-sites.md`](https://github.com/mschulkind-oss/vantage/blob/main/userguide/guides/static-sites.md)
(what `vantage build` emits).

---

## Defined terms

- **Closed tree** (coined here): a directory of Markdown whose relative links all resolve *inside*
  it. Anything outside is reached by an absolute URL. [§2.2](#22-the-closed-tree-rule) says why the
  site needs one.
- **Workers Builds**: Cloudflare's CI for Workers. You connect a GitHub repo in the Cloudflare
  dashboard and store a build command and a deploy command there. Cloudflare runs them on push and
  reports the result as a GitHub check named `Workers Builds: <worker name>`.
- **Static-assets Worker**: a Cloudflare Worker whose `wrangler` config names an `[assets]`
  directory. Cloudflare serves the files and the Worker's own code does nothing else.

## 1. What Vantage does, verified

Every row was read from the Vantage tree at `4b2ccbc` or probed live on 2026-09-25.

| Piece | Vantage's | What it is |
| :--- | :--- | :--- |
| Content | `userguide/` | `README.md` (index), `getting-started.md`, `features.md`, `guides/` (5 task-shaped pages), `reference/` (4 look-up pages) |
| Build | `scripts/build-site.sh` | builds Vantage from its own source, then `./vantage build userguide/ -o dist/docs -n "Vantage User Guide"` |
| Serve | `docs-wrangler.toml` + `docs-worker.js` | Worker `vantage`: `main = "docs-worker.js"`, `[assets] directory = "dist/docs"`, `not_found_handling = "single-page-application"`, `workers_dev = true`, `preview_urls = false`. The Worker is one line, `return env.ASSETS.fetch(request)` |
| Deploy | Cloudflare dashboard | build command `bash scripts/build-site.sh`, deploy command `npx wrangler deploy --config docs-wrangler.toml`. **Neither is in the repo.** Vantage's `.github/workflows/` has no deploy step |
| Address | custom domain | `docs.vantageapp.dev` and `vantage.mschulkind.workers.dev` serve the same build |
| Gate | `just _self-check` | runs the compiled `vantage-check` over `docs userguide README.md AGENTS.md …`. Vantage's comment calls it "the only thing keeping the docs' links and anchors honest" |

**`vantageapp.dev` itself is a different site.** It is an Astro landing page that links to
`docs.vantageapp.dev`. Its source is not in the Vantage repo, and it is not in any
`mschulkind-oss` repo this jail's token can list. It is out of scope here ([§5](#5-non-goals)).

`vantage build` pre-renders every file, directory listing, commit and diff under the root into JSON,
alongside the same React UI the live server uses. The result is a static snapshot with no search, no
live reload and no review mode (Vantage's own
[Limitations](https://github.com/mschulkind-oss/vantage/blob/main/userguide/guides/static-sites.md#limitations)).
A trial build of today's `docs/guides/` with `vantage-md` 0.7.0 produced 189 files and 8.6 MB.

## 2. The guide: a closed tree in Vantage's organization

### 2.1 Layout

`userguide/` at the repo root, with Vantage's four-part organization. The section-to-page mapping
below is the proposal. **Page boundaries are the implementer's call; the four parts are not.**

| Page | Takes | From |
| :--- | :--- | :--- |
| `README.md` | Tagline, a four-line quick start, and the "What's in This Guide" tables (Start here / Guides / Reference / Links), shaped like Vantage's | new |
| `getting-started.md` | Installation, First Run, Authentication | `USER_GUIDE.md` |
| `features.md` | A capability tour: isolation, packs and agents, packages, network, MCP/LSP, loopholes, storage. Each gets a paragraph linking to its guide | new, from `USER_GUIDE.md`'s section intros |
| `guides/macos.md` | as is | `docs/guides/macos.md` |
| `guides/loopholes.md` | as is | `docs/guides/loopholes.md` |
| `guides/migrating-to-packs.md` | as is | `docs/guides/migrating-to-packs-and-host-management.md` |
| `guides/packages-and-tools.md` | Package Management, Blocked Tools | `USER_GUIDE.md` |
| `guides/networking.md` | Network & Ports | `USER_GUIDE.md` |
| `guides/mcp-and-lsp.md` | MCP Presets, LSP Servers | `USER_GUIDE.md` |
| `guides/devices-and-gpus.md` | Device Passthrough, NVIDIA, AMD ROCm | `USER_GUIDE.md` |
| `guides/storage.md` | Storage & Persistence, Container Reuse | `USER_GUIDE.md` |
| `guides/troubleshooting.md` | Troubleshooting | `USER_GUIDE.md` |
| `reference/cli-reference.md` | CLI Commands | `USER_GUIDE.md` |
| `reference/configuration.md` | Configuration, Config Safety | `USER_GUIDE.md` |
| `reference/settings-per-setup.md` | What works in each setup | `USER_GUIDE.md`, and see [OQ-DW2](#OQ-DW2) |

**The guide moves; it is not copied.** `docs/guides/` is deleted in the same change that creates
`userguide/`, and every mention of it is repointed. Two copies of a user guide is the drift this repo
spends its sweeps undoing. Runtime messages that name a guide path (`internal/cli/check`,
`internal/cli/run/jailprefix.go`, `internal/loopholes/retired.go`, `config_ref.txt`) name the new
repo path. Whether they should name a URL waits on [OQ-DW1](#OQ-DW1).

The voice doesn't change: capabilities first, plain *works / doesn't / planned*, and per-key detail
in `docs/reference/`.

### 2.2 The closed-tree rule

`vantage build userguide/` publishes **only** `userguide/`. A relative link to `../docs/reference/…`
passes every local check, because the file exists in the checkout, but on the site it points at
nothing. Today's guide tree links about 50 distinct targets that way: 7 from `USER_GUIDE.md`, 27
from `loopholes.md`, 13 from `macos.md`, 3 from `migrating-…`. Vantage's own `userguide/` has none,
which is why its setup never had to say this.

So:

- **Every relative link in `userguide/` resolves inside `userguide/`.** A link to anything outside is
  an absolute `https://github.com/mschulkind-oss/yolo-jail/blob/main/<path>` URL. It is readable on
  the site and on GitHub, and it is honest that the reader is leaving the guide.
- **The gate enforces it.** `vantage-check` checks that a link resolves, not where it resolves. So
  the gate gets one more check, which refuses any relative link from `userguide/` whose target is
  outside it. How it is written is the implementer's choice.
- **The rule runs one way.** The rest of the repo may link *into* `userguide/` with relative paths.

### 2.3 What else is published

The site renders each file's git history and diffs, so every commit message that touched a guide
page is public on the site. This repo is already public on GitHub, so nothing new is exposed. Still,
a commit message on a guide page is now read by learners.

## 3. Build, serve and deploy, copied

### 3.1 The build script, the one entry point

`scripts/build-site.sh` does exactly one thing: it produces `dist/docs/` from `userguide/` with the
display name `YOLO Jail User Guide`. Its header comment says the same thing Vantage's does: the
Cloudflare dashboard runs this file by name, and renaming it breaks the deploy invisibly.

**One deliberate difference: Vantage is installed, not built.** Vantage builds its own binary because
the site is also a test of that binary. Here it is a tool, so the script runs the **`vantage-md`**
wheel from PyPI at a **pinned** version (0.7.0 today; `vantage-md` is the PyPI project for the
executable server). A bump is a commit, so a Vantage release can't change the site without a commit
in this repo. `dist/` is already gitignored here.

The script must never need yolo-jail's own toolchain: no Go, no nix, no `just`. Workers Builds has
none of them. How it installs the wheel (`uvx`, `pip`, or a release tarball) depends on what the
build image carries, and that is a [sketch](docs-website-plan.md) item.

### 3.2 The Worker

`docs-wrangler.toml` and `docs-worker.js` are Vantage's, byte for byte, except
`name = "yolo-jail"`. `preview_urls = false` stays: a branch build is a check, not a published
preview.

### 3.3 Cloudflare Workers Builds, the one deployer

The Worker is connected to `mschulkind-oss/yolo-jail` in the Cloudflare dashboard. The build
command is `bash scripts/build-site.sh`, the deploy command is
`npx wrangler deploy --config docs-wrangler.toml`, and the production branch is `main`. This is a
human action in the dashboard. Nothing in this repo can do it.

- **Trigger.** Every push to `main` builds and deploys. A push to any other branch builds, and
  reports as the `Workers Builds: yolo-jail` check without deploying.
- **One writer.** Workers Builds is the only thing that deploys. There is no `just deploy-site` and
  no laptop deploy: a second deployer could publish from a dirty tree.
- **Failure.** A failed build deploys nothing. The previous deployment keeps serving, and the red
  check is the only signal, with no alert. A broken link can't get this far, because the local gate
  refuses it first ([§3.5](#35-the-gate)).
- **No GitHub Actions deploy.** Like Vantage, `ci.yml` gains no deploy step.

### 3.4 The one failure Vantage already paid for

Vantage deleted `build-docs.sh` on 2026-05-30. Its dashboard kept running it until 2026-09-01, so
every PR carried a red check that nothing in the tree could explain. The fix Vantage landed was two
pieces of text: the build script's header comment, and an `AGENTS.md` line saying the dashboard
holds the command and must change in the same breath as a rename. **Both are copied here.**

### 3.5 The gate

`just check-ci` gains the site checks, so CI and the pre-commit hook run them:

- `vantage-check` over `userguide/`, which checks links and anchors;
- the closed-tree check from [§2.2](#22-the-closed-tree-rule);
- a check that `scripts/build-site.sh`'s output directory is `docs-wrangler.toml`'s
  `[assets] directory`, so the two files can't drift apart.

Running `vantage-check` over the rest of `docs/` is already the
[doc convention](../plans/README.md#keeping-this-corpus-honest--the-five-checks-so-they-are-re-runnable)
and stays outside the gate. This design doesn't widen it.

A local preview runs the live server over the tree, `uvx --from vantage-md vantage userguide/`. It
needs no build.

## 4. Done looks like

- A push to `main` that edits a guide page changes the live site, and nobody runs anything.
- The site opens on `README.md`'s tables, and every page in [§2.1](#21-layout) is reachable from them.
- A commit adding `[x](../docs/reference/foo.md)` to a guide page fails `just check-ci`.
- Renaming `scripts/build-site.sh` without updating the dashboard produces a red
  `Workers Builds: yolo-jail` check on the PR, not a silent stale site.
- `rg -n 'docs/guides/' docs internal cmd packs README.md AGENTS.md` prints nothing.

## 5. Non-goals

- **A landing page.** Vantage's lives outside its repo. If yolo-jail wants one, it is a separate
  site and a separate doc, and [OQ-DW1](#OQ-DW1) only decides whether the docs address leaves room
  for it.
- **Publishing `docs/`.** Design, plans, research and most reference material stay GitHub-only.
- **Search, versioned docs, per-release sites.** The static export has no search, and one site
  tracks `main`.
- **Generated reference pages.** `yolo config-ref` and `yolo --help` stay the authorities for keys
  and flags. `reference/` pages link to them and don't transcribe them.

## 6. Alternatives, with verdicts

| Alternative | Verdict |
| :--- | :--- |
| Keep `docs/guides/` and build the site from it | **No.** The directory holds more than learner material, and its outbound links are the norm there. The closed-tree rule would fight the whole corpus |
| Copy `docs/guides/` into `userguide/` at build time | **No.** Two paths to one page, and the gate would check one while readers read the other |
| GitHub Pages via an Actions deploy | **No.** Not Vantage's setup, which was the request. It also adds a deploy token to CI |
| Build Vantage from source, as Vantage does | **No.** It would need Go and Node in the Cloudflare build to produce a tool this repo doesn't develop |
| Unpinned `vantage-md` | **No.** A Vantage release could change the published site with no commit here |

## Open Questions

1. 💬 <a id="OQ-DW1"></a>**[OQ-DW1](#OQ-DW1): What address does the site live at?**
   Vantage serves `docs.vantageapp.dev`, a custom domain on the Worker, and keeps the apex for a
   separate landing page. Choices: **(a)** `yolo-jail.<account>.workers.dev` only; **(b)** a
   `docs.` subdomain of a domain you register, with the `workers.dev` address kept as Vantage keeps
   its own; **(c)** an apex domain, which uses up the slot a landing page would want. Runtime
   messages can name a URL instead of a repo path only once this is ruled.

   _Leaning:_ **(b)**, and launch on the `workers.dev` address meanwhile. Attaching the domain later
   is a dashboard action, and no file in the repo changes.

   <!-- vantage: oq id=OQ-DW1 leaning="(b): a docs. subdomain of a registered domain, launching on the workers.dev address meanwhile; attaching the domain later changes no file in the repo." -->

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 <a id="OQ-DW2"></a>**[OQ-DW2](#OQ-DW2): Which `docs/reference/` pages cross into the guide?**
   The closed-tree rule turns every outbound link into a GitHub URL, which is right for contributor
   material and wrong for a page a learner needs. `settings-per-setup.md` is linked six times from
   the guide and is a per-setup table of what works. The rest (`loophole-protocol.md`,
   `mcp-configuration.md`, `config-safety.md`, …) explain the machinery. Choices: **(a)** move
   `settings-per-setup.md` into `userguide/reference/` and leave the rest; **(b)** move nothing and
   accept GitHub hops; **(c)** move every reference page the guide links.

   _Leaning:_ **(a)**. It is the only one written for someone choosing a setup rather than changing
   the code. The `AGENTS.md` authority table follows it to its new path.

   <!-- vantage: oq id=OQ-DW2 leaning="(a): move settings-per-setup.md into userguide/reference/ and leave the rest on GitHub; it is the only one written for someone choosing a setup rather than changing the code." -->

   **Answer:**
   > _(empty — fill in when decided)_

## Decision Ledger

| ID | Ruling | Date |
| :--- | :--- | :--- |
| — | None yet | — |
