---
title: "Completion checks read more than Go imports"
status: accepted
stage: CURRENT
tags: [testing, tooling, research]
summary: "Source evidence for a narrowly scoped, read-only completion command; documentation paths can also be runtime, test and publication inputs."
---

# Completion checks read more than Go imports

**Status:** Source inspection and bounded offline diagnostics, 2026-10-09; no project gate run.
No selector, baseline recorder or evidence-reuse mechanism is implemented by this research.

> **Finding.** A small documentation shortcut is defensible; a Markdown-extension shortcut is not.
> Completion also needs a successful baseline covering earlier commits, not just an empty working diff.

**Reads with:** [the behavior design](../design/change-aware-completion.md),
[its implementation sketch](../design/change-aware-completion-plan.md), and
[the suite-speed investigation](../plans/test-suite-speed.md) (measurements, not selection).

## The recipe names describe different coverage

The complete [Justfile](../../Justfile) was read, including setup, installation, publication and cleanup.
The relevant dependency graph is [the gate block](../../Justfile#L409-L498):

| Entry | What it actually does | What it does not establish |
| :--- | :--- | :--- |
| `done` | `check`, then Git cleanliness | Full container integration or native macOS execution |
| `check` | Mutating formatting; Linux and Darwin vet/staticcheck; all short units; official binary pins | Guide/site/changelog checks added by `lint-ci` |
| `check-ci` | Read-only formatting check; the same lint, short units and pins; changelog, guide and site-contract checks | A rendered site build or full integration |
| `test` | Short units, then uncached full integration with real pack installs requested | Lint or official digest-check coverage |
| `check-all` | Formatting, lint and `test` | The explicit `check-pack-binaries` dependency |

The Darwin staticcheck pass excludes SA4023; the Linux pass does not. Architectures remain the
host's. [The cross-language lint pin](../../internal/capture/lintgate_pin_test.go) checks actual
recipe calls and build-constraint coverage for both shipping architectures. It is not native proof.

## Toolchains and call sites are inputs too

- [The development tool declaration](../../mise.toml) names Go 1.26, Node 24, latest just, uv and
  staticcheck. [The module](../../go.mod) requires Go 1.26.0. These are not exact installed versions.
- [The toolchain pin test](../../internal/capture/gatetoolchain_pin_test.go) ties gate commands to
  declared providers; Python and sh are system prerequisites, not mise tools.
- [The official binary tool](../../tools/pack-binaries/main.go#L127-L231) rebuilds and compares
  declared official builds. [Its exact toolchain](../../tools/pack-binaries/toolchain.go#L15-L26)
  is Go 1.26.7, distinct from the module's minimum.
- [Recipe tests](../../tools/pack-binaries/recipe_test.go) pin vendor mode, environment scrubbing,
  architecture levels and deterministic build arguments. A changed dependency can move program
  bytes even if its command directory did not change.
- [Call-site tests](../../tools/pack-binaries/callsites_test.go) pin quality, install, integration
  and release wiring. A selector test alone would not protect the real completion recipe.

## Non-Go readers rule out a broad prose category

| Input | Actual consumer | Conservative consequence |
| :--- | :--- | :--- |
| Pack manifests, Lua, JavaScript, briefing and skill prose | [Official pack embed](../../packs/embed.go#L88-L89), [embed drift test](../../internal/packload/embeddrift_test.go) | Pack Markdown is shipped content; full route |
| Built-in skill Markdown | [Skill embed](../../internal/jailcontent/builtinskills/embed.go#L16-L17) | Runtime instructions; full route |
| CLI reference/templates/config | [Reference embed](../../internal/cli/configref.go#L16), [initialization embeds](../../internal/cli/init.go#L21-L30) | Extension is immaterial; full route |
| Example pack directories, including their prose | [Real pack lint test](../../internal/cli/packexamples_test.go#L32-L69) | All example directories are inputs; full route |
| Root README | [Wheel builder](../../tools/build-wheels/main.go#L329-L335), [real README link test](../../tools/build-wheels/readmelinks_test.go#L65-L89) | Packaging input; full route |
| README and getting-started guide | [Tap workflow tests](../../tools/tap-install-check/workflow_test.go#L58-L95) | Install command is tested; full route |
| Setup reference and every guide page | [Guide census](../../internal/setupcensus/guide_test.go#L32), [whole-guide claim scan](../../internal/setupcensus/guide_test.go#L477-L510) | A guide edit can fail Go tests; full route |
| Source comments and messages citing docs | [Source citation test](../../internal/paths/doccitations_test.go) | Removed paths/anchors can break non-Markdown inputs |
| Flake, shipping lists and source bundle | [Go source fileset](../../flake.nix#L186-L220), [shipped-client pin](../../internal/entrypoint/shippedclients_test.go) | Cross-language contract; full route |
| Workflows, scripts, fixtures, golden files, agent instructions and config | Lint/toolchain/pin wiring above; declared embeds and direct reads | Not ordinary prose; unknown ownership selects full |

The citation test scans source directories and root files, not just Go. Its pattern and explicit
historical/fixture exceptions are visible, and it checks fragments as well as paths. It does not
check semantic numbered-section references. [The corpus-honesty guide](../plans/README.md#keeping-this-corpus-honest--the-checks-so-they-are-re-runnable)
requires source-first claim review; link validity is not truth. [The SHA sweep](../../scripts/check-doc-shas.sh)
also distinguishes reachability from merely having a dangling commit object locally.

## Documentation checks have their own boundaries

[The Vantage wrapper](../../scripts/vantage-check.sh) isolates checker output on a separate pipe and
preserves its failure. It requests latest; existing offline checker evidence must name its release.
Strict checks apply to actual changed Markdown, including new files, not to a missing assumed folder.

[The guide closed-tree checker](../../scripts/check-userguide-closed-tree.py) checks that published
links stay within the guide. [The site contract checker](../../scripts/check-site-output-dir.py)
and [base-href tests](../../scripts/test-site-base-href.py) check output location, bindings and HTML
rewriting. They do not build the guide. [The site builder](../../scripts/build-site.sh#L12-L34)
can fetch history, install a renderer, erase output and write a new export; completion must not call
that deployment recipe as if it were read-only. Rendered-guide validation remains separate work.

## Measurements justify relevance, not a speed prediction

Read from the current primary [speed plan](../plans/test-suite-speed.md), its 2026-10-08 retake,
not remeasured here: at `b2eeffc12b631d5cda108238e77a6c4cb5bb777c`, the second warm unit run took
**123.287 s** and the three full-integration runs had median **631.301 s**. All five commands
passed. Neither target was met; host-wide idleness was not proved. The unit recipe used `-count=1`;
ordinary quality recipes may reuse Go's test cache. There is no projected or measured selector saving.

## Verification context is not just Go version

Source rechecked 2026-10-09. The installed Go 1.26.7 source's `cmd/go/internal/cfg.EnvFile`
selects explicit `GOENV` or the platform user-config directory's `go/env`; `initEnvCache` also
reads `GOROOT/go.env`. Environment overrides those defaults. Implicit cgo additionally consults
C-compiler PATH availability. The shipped defaults enable automatic toolchain selection. See
[upstream configuration source](https://github.com/golang/go/blob/go1.26.7/src/cmd/go/internal/cfg/cfg.go).
Thus identical executable/version and environment strings can still hide changed settings or tools.

[Official builds](../../tools/pack-binaries/recipe.go) scrub most GO/CGO values, retain selected
cache paths and disable Go env/workspace/automatic toolchain selection for the build itself.
[Their toolchain resolver](../../tools/pack-binaries/toolchain.go) still consults proxy environment
and user Go env settings. That build's closed environment does not close the entire quality gate.

Short-unit inputs are broader: [PATH-sensitive host tests](../../internal/cli/hostblockers_test.go),
[ambient launch context assertions](../../internal/cli/capturelandlock_test.go),
[native CI-dependent behavior](../../internal/capture/clone_darwin_test.go), and
[optional network tests](../../internal/packload/packproperties_test.go#L302) are examples.
A grep of literal environment names cannot cover dynamic names or called production file readers.
The default supported profile therefore needs a source audit, not an all-environment hash or a
manual caller promise. Clearing variables or replacing HOME would check a different context.

The intended two-phase protocol records effective metadata during full verification and compares
resolved file/tool identities on prose without any Go/gofmt/staticcheck argv. Direct tools and
declared mise resolution need offline fixtures; opaque shims and custom inputs select full.
No default profile is yet proven. That is an explicit [promotion gate](../design/change-aware-completion-plan.md#verification-context),
not permission to ship a permanently disabled shortcut.

## Renderer, checker and source citations are different mechanisms

Source inspected in the readable bundled modules of the cached released Vantage 0.9.2 checker:
[renderer pipeline](https://github.com/mschulkind-oss/vantage/blob/v0.9.2/packages/vantage-md/src/pipeline.ts),
[question anchors](https://github.com/mschulkind-oss/vantage/blob/v0.9.2/packages/vantage-md/src/rehypeVantageAnchors.ts),
[checker target index](https://github.com/mschulkind-oss/vantage/blob/v0.9.2/packages/vantage-check/src/core/slugs.ts),
and [link collection](https://github.com/mschulkind-oss/vantage/blob/v0.9.2/packages/vantage-check/src/rules/links.ts).
External URLs were not fetched; the module names and functions were read from the released bundle.

The renderer processes raw HTML, directives and sanitization before question IDs and `rehype-slug`.
The CLI target index instead builds mdast heading slugs and adds HTML `id`/`name` and question IDs.
Links include images and definitions. The CLI exposes no AST/anchor-export command. A strict PASS
therefore cannot supply the ordered rendered-ID comparison the shortcut needs.

[The source-citation matcher](../../internal/paths/doccitations_test.go#L215-L264) is a third
interpretation: explicit double-quoted IDs are scanned across the whole text, headings are ATX,
and fence state uses a limited pattern. Its interpretation is not full GFM or question directives.
A prose route must preserve its targets too; unchanged Vantage IDs alone are insufficient.
The proposed adapter dependency remains unresolved. Do not port a regex and call it equivalent.

## Bounded diagnostics establish syntax, not a shipped shortcut

Synthetic local-only Git history probes confirmed that net diffs can hide an intermediate source
change/revert, while comparing each intervening commit against every parent retains it. A two-parent
merge exposed different paths on each parent edge. `--raw -z --no-renames` conservatively exposed
both deleted and added rename names. Ordinary and linked worktrees resolved distinct Git
administration directories; the common directory is not the right baseline storage location.

A Linux file-inode event probe observed deliberate mutate/restore despite equal final bytes.
It does not prove recursive registration, atomic replacement, external-context coverage, overflow
handling or native macOS observation. The design now requires automatic bounded observation,
not caller certification; unsupported observation runs full without reusable coverage.

The actual copied `just done` recipe, with real Git/just and fake quality tools, passed its six
full-control lint/unit/pin assertions, then failed the no-Go prose assertion as expected. It invoked
`gofmt -w` and the Go/lint/pin commands after an ordinary allowlisted prose commit. Deleting the
copied recipe's `check` dependency made the coverage assertion fail with an empty argv log even
though the recipe printed success. Restoring the recipe recovered the full control, not a feature
green. These are diagnostic reds, not landed tests, runtime execution or performance evidence.
The [complete diagnostic listing](../design/change-aware-completion-plan.md#diagnostic-test-listing)
is reproducible; its fake context is deliberately not accepted as a production baseline proof.

## Recommendation and remaining investigation

Start with exact planning-prose paths, preserved anchors and read-only completion. The design's
[initial allowlist](../design/change-aware-completion.md#4-the-first-shortcut-is-an-exact-allowlist)
contains five paths, not all documentation. The reader survey found source citations of the speed
plan and roadmap, but no direct executable body reader for these five paths; that finding is bounded
by this checkout and must be rechecked before implementation. Structural changes still fall back.

The next source investigation is [sketch promotion](../design/change-aware-completion-plan.md#before-promotion):
close a reachable supported context profile, provisioned renderer adapter and complete input
observer, then finish persistence/history/error cases in the real-front-door fixture. Baseline
commands, record ordering and deterministic caller/prose reds are prepared, not implemented.
Package-impact selection and exact command-result reuse come later. Preserve [local landing rules](../../AGENTS.md#workflow),
[CI's whole-tree quality job](../../.github/workflows/ci.yml#L32-L158), native units and both Linux
integration architectures. None of this research changes them.
