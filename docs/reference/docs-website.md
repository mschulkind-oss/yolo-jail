---
status: current
stage: CURRENT
verified: 2026-10-01
verified_commit: d4e435a3
covers:
  - userguide/
  - scripts/build-site.sh
  - scripts/check-userguide-closed-tree.py
  - scripts/check-site-output-dir.py
  - docs-wrangler.toml
  - docs-worker.js
  - Justfile
  - .github/workflows/ci.yml
tags: [docs, website, userguide, vantage, cloudflare, deploy]
summary: "How the user guide is published: userguide/ is a closed tree of Markdown, scripts/build-site.sh turns it into a static Vantage export with a pinned vantage-md, a static-assets Cloudflare Worker serves that export, and Cloudflare Workers Builds is the one thing that deploys it, on every push to main. The local gate refuses a guide link that leaves the tree and a build script whose output the Worker does not serve."
---

# The docs website — how the user guide is built, served and deployed

**Status:** verified 2026-10-01 against `d4e435a3`. It graduated that day from the design
`docs-website.md`, which keeps one open question about runtime messages
([`OQ-DW3`](../design/docs-website.md#OQ-DW3)). MEASURED 2026-09-30, as the design recorded: the
site served at its address with HTTP 200, and its recent-commits list named a guide change pushed
to `main` that morning, so a push reaches the live site with nobody running anything. MEASURED
2026-10-01: the index still returns 200 and its recent-commits list names a guide change of that
morning, but a path-shaped deep link (`/guides/macos`, `/README.md`) returns HTTP 500 with
Cloudflare's error 1101, a Worker exception, and so does a deep link on `docs.vantageapp.dev`,
the setup this one copies. UNMEASURED: whether a browser reaches a page by its own address, and
whether the site's history view follows the `git mv` renames that created the tree.

The user guide is published as a website by copying [Vantage](https://github.com/mschulkind-oss/vantage)'s
own setup whole. `userguide/` holds the content. One build script turns it into a static export of
the Vantage viewer. A Cloudflare Worker serves that export, and Cloudflare Workers Builds builds
and deploys it on every push to `main`. The one rule this repository adds is that the guide is a
**closed tree**, so what readers see on the site is what the gate checked.

| Component | Lives in |
| :--- | :--- |
| The content, in Vantage's four parts: an index, getting started, a feature tour, `guides/`, `reference/` | `userguide/` |
| The build, the one entry point Cloudflare runs | `scripts/build-site.sh` |
| The Worker that serves the export | `docs-wrangler.toml`, `docs-worker.js` |
| The closed-tree check, and its tests | `scripts/check-userguide-closed-tree.py`, `scripts/test-check-userguide-closed-tree.py` |
| The check that the build writes what the Worker serves, and its tests | `scripts/check-site-output-dir.py`, `scripts/test-check-site-output-dir.py` |
| The gate that runs them | `lint-ci` in the `Justfile`, run by `just check-ci` locally and in `ci.yml` |
| The deploy | the Cloudflare dashboard, not this repository |

**Reads with:** Vantage's
[`static-sites.md`](https://github.com/mschulkind-oss/vantage/blob/main/userguide/guides/static-sites.md)
(what `vantage build` emits, and its limits), and [`../design/docs-website.md`](../design/docs-website.md)
(the open question on runtime messages).

---

## Terms

- **Closed tree** (coined in the design this graduated from): a directory of Markdown whose
  relative links all resolve inside it. Anything outside it is reached by an absolute URL.
- **Workers Builds**: Cloudflare's CI for Workers. A GitHub repository is connected in the
  Cloudflare dashboard, which stores a build command and a deploy command. Cloudflare runs them on
  push and reports the result as a GitHub check named `Workers Builds: <worker name>`.
- **Static-assets Worker**: a Cloudflare Worker whose `wrangler` config names an `[assets]`
  directory. Cloudflare serves the files, and the Worker's own code does nothing else.

## The closed-tree rule

`vantage build userguide/` publishes only `userguide/`. A relative link from a guide page to
`../docs/reference/…` passes every local check, because the file exists in the checkout, and on
the site it points at nothing. So:

- **Every relative link in `userguide/` resolves inside `userguide/`.** A link to anything else is
  an absolute `https://github.com/mschulkind-oss/yolo-jail/blob/main/<path>` URL, readable on the
  site and on GitHub, and honest that the reader is leaving the guide.
- **The gate enforces it.** `vantage-check` proves that a link resolves, not where it resolves, so
  `check-userguide-closed-tree.py` refuses any relative link from `userguide/` whose target is
  outside it. It reads inline and reference-style links and skips fenced code.
- **The rule runs one way.** The rest of the repository links into `userguide/` with relative
  paths.

The guide **moved** into `userguide/`; it is not a copy of anything. Two copies of a user guide
is the drift this repository's doc sweeps exist to undo.

One `docs/reference/` page crossed into the guide, as `userguide/reference/settings-per-setup.md`:
the per-setup table of what works, the one reference page written for someone choosing a setup
rather than changing the code ([DW-D1](#dw-d1)). Every other reference page stays on GitHub behind
an absolute link.

> [!WARNING]
> **Do not build the site from another directory, or copy pages into `userguide/` at build
> time.** The old guide directory held contributor material whose outbound links are the norm,
> so the closed-tree rule would have fought the whole corpus; and a copy gives one page two paths,
> with the gate checking one while readers read the other.

## The build

`scripts/build-site.sh` produces `dist/docs/` from `userguide/`, under the display name
`YOLO Jail User Guide`, and does nothing else:

- **Vantage is installed, not built.** Vantage builds its own binary because its site is also a
  test of that binary; here it is a tool, so the script runs the `vantage-md` package from PyPI at
  a **pinned** version. A bump is a commit, so a Vantage release cannot change the site without a
  commit in this repository. It runs through `uvx` when the build image has it, and otherwise
  through a throwaway Python virtual environment kept outside the published directory, because
  Workers Builds images are not guaranteed to carry `uv`.
- **No yolo toolchain.** The script needs no Go, no nix and no `just`; the build image has none.
- **Full history.** The export includes each page's git history, so a shallow clone is unshallowed
  first rather than silently showing one commit per file.
- **A clean output.** `dist/docs` is deleted before each build, and `dist/` is gitignored.

What the export holds: every file, directory listing, commit and diff under the root,
pre-rendered as JSON beside the same React UI the live Vantage server uses. It has no search, no
live reload and no review mode. Because history is rendered, every commit message that touched a
guide page is public on the site, so a guide commit's message is read by learners.

A local preview needs no build: `uvx --from vantage-md vantage userguide/` serves the tree live.

## Serving

`docs-wrangler.toml` and `docs-worker.js` are Vantage's, with the Worker's name changed and the
`[assets]` table's `binding = "ASSETS"` added. The Worker is one line that hands each request to its
static assets through that binding. An unmatched path is meant to be served
as the app, with a 200 (`not_found_handling = "single-page-application"`), so the viewer can
route it. The `workers.dev` address stays enabled beside the custom domain, as Vantage keeps its
own. Branch preview URLs are off: a branch build is a check, not a published preview.

**The binding is a fix Vantage's copy also needs.** Without it `env.ASSETS` is undefined, and the
Worker threw on each request that reached it. A browser never saw that: its navigation request
carries `Sec-Fetch-Mode: navigate`, which the single-page-application fallback answers before the
Worker runs. Every other request for a page that is not a file got Cloudflare error 1101, a 500:
curl, a link checker, a crawler, a link preview. MEASURED 2026-10-01: `/guides/macos` returned 500 to
a plain GET and 200 with the navigate header, here and on `docs.vantageapp.dev`; `wrangler dev`
showed `TypeError: Cannot read properties of undefined (reading 'fetch')` at `docs-worker.js:3`, and
200 for both once the binding was added. `scripts/check-site-output-dir.py` now refuses a Worker
that reads a binding its config does not declare.

## Deploying

The Worker is connected to this repository in the Cloudflare dashboard, which holds the build
command, the deploy command and the production branch. **Nothing in this repository can change
them.**

- **Trigger.** Every push to `main` builds and deploys. A push to any other branch builds and
  reports as the `Workers Builds: yolo-jail` check, without deploying.
- **One writer.** Workers Builds is the only thing that deploys. There is no `just` recipe and no
  laptop deploy, because a second deployer could publish from a dirty tree, and `ci.yml` has no
  deploy step.
- **Failure.** A failed build deploys nothing: the previous deployment keeps serving, and the red
  check is the only signal. A broken link does not get that far, because the local gate refuses it
  first.

> [!WARNING]
> **Renaming or moving `scripts/build-site.sh` breaks the deploy invisibly** unless the
> dashboard's build command changes in the same breath. Vantage paid for this once: it deleted its
> build script, the dashboard kept running the old name for three months, and every pull request
> carried a red check nothing in its tree could explain. The two defenses are copied from its fix:
> the script's header comment, and the `AGENTS.md` line on the `Workers Builds: yolo-jail` check.

## The gate

`lint-ci`, and so `just check-ci` locally and in CI, runs three site checks on every landing:

- `vantage-check` over `userguide/`, for links and anchors;
- the closed-tree check, after its own tests;
- the output-directory check, after its own tests: every `vantage build` invocation in the build
  script writes the directory `docs-wrangler.toml` serves, so the two files cannot drift apart.

Running `vantage-check` over the rest of `docs/` is the corpus convention
([`../plans/README.md`](../plans/README.md#keeping-this-corpus-honest--the-five-checks-so-they-are-re-runnable))
and stays outside the gate.

## What this does not do

- **No landing page.** Vantage's lives outside its repository. If yolo-jail wants one, it is a
  separate site; the docs address is a subdomain, which leaves the apex free for it.
- **No publishing of `docs/`.** Design, plans, research and most reference material stay
  GitHub-only.
- **No search, versioned docs or per-release sites.** The static export has no search, and the one
  site tracks `main`.
- **No generated reference pages.** `yolo config-ref` and `yolo --help` stay the authorities for
  keys and flags; the guide's reference pages link to them rather than transcribing them.

## Why it's this way

Each row is a ruling a maintainer could undo on purpose, kept under its original id.

| Ruling | Why |
| :--- | :--- |
| <a id="oq-dw1"></a>[`OQ-DW1`](#oq-dw1): the address is `docs.yolo-jail.mschulkind.dev`, a `docs.` subdomain attached in the dashboard, with the `workers.dev` address kept (maintainer, 2026-09-25) | A subdomain leaves the apex for a landing page, as Vantage does, and attaching it is a dashboard action no repository file has to follow. |
| <a id="oq-dw2"></a><a id="dw-d1"></a>[`OQ-DW2`](#oq-dw2), decided as [`DW-D1`](#dw-d1): **`settings-per-setup.md` crosses into the guide, and no other reference page does** (2026-09-30) | It is the one page the guide links that is written for a reader choosing a setup: the maintainer's review of it said *"this is for a user learning about yolo, not me as a developer"*. Moving nothing would send a learner to GitHub for the table of what works in their setup. Moving another page later is a `git mv` and its links, and the closed-tree check refuses any link left pointing out. |
| <a id="dw-d2"></a>[`DW-D2`](#dw-d2): the move's "no file names the deleted guide directory" check prints only path-shaped words, drops URLs, and skips the files that record the move (2026-09-30) | A bare search over the same paths could never print nothing: it matched its own line, and text that names the deleted directory on purpose. |
| **No deploy from GitHub Actions** | It is not Vantage's setup, it would be a second deployer, and it would put a deploy token in CI. |
| **A pinned `vantage-md`** | Unpinned, a Vantage release could change the published site with no commit here. |

## Current values

Verified at `d4e435a3`. The prose above explains what each of these is for; this table is the only
place the values themselves are stated.

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| The address | `https://docs.yolo-jail.mschulkind.dev` | the Cloudflare dashboard ([`OQ-DW1`](#oq-dw1)) |
| The Worker's name, and so the check's | `yolo-jail`, `Workers Builds: yolo-jail` | `docs-wrangler.toml` |
| The build command | `bash scripts/build-site.sh` | the Cloudflare dashboard |
| The deploy command | `npx wrangler deploy --config docs-wrangler.toml` | the Cloudflare dashboard |
| The production branch | `main` | the Cloudflare dashboard |
| The pinned Vantage | `vantage-md` `0.7.0` | `VANTAGE_MD_VERSION`, `scripts/build-site.sh` |
| The export's display name | `YOLO Jail User Guide` | `scripts/build-site.sh` |
| The export directory | `dist/docs` | `scripts/build-site.sh`; `[assets] directory`, `docs-wrangler.toml` |
| Unmatched paths, `workers.dev`, previews | `single-page-application`; on; off | `docs-wrangler.toml` |
| The gate's `vantage-check` | `vantage-check@0.7.0` | `lint-ci`, `Justfile` |
