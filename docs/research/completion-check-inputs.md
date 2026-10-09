---
title: "Completion checks read more than Go imports"
status: accepted
stage: CURRENT
tags: [testing, tooling, research]
summary: "Source evidence for a narrowly scoped, read-only completion command; documentation paths can also be runtime, test and publication inputs."
---

# Completion checks read more than Go imports

**Status:** Inspection only; source readers checked 2026-10-09 against the current checkout.
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

## Recommendation and remaining investigation

Start with exact planning-prose paths, preserved anchors and read-only completion. The design's
[initial allowlist](../design/change-aware-completion.md#4-the-first-shortcut-is-an-exact-allowlist)
contains five paths, not all documentation. The reader survey found source citations of the speed
plan and roadmap, but no direct executable body reader for these five paths; that finding is bounded
by this checkout and must be rechecked before implementation. Structural changes still fall back.

The next source investigation is [sketch promotion](../design/change-aware-completion-plan.md#before-promotion):
prove automatic successful-baseline persistence, reader exclusions, renderer anchor comparison and
real-recipe fake-tool fixtures before handing any implementation off. Package-impact selection and
exact command-result reuse come later. Preserve [local landing rules](../../AGENTS.md#workflow),
[CI's whole-tree quality job](../../.github/workflows/ci.yml#L32-L158), native units and both Linux
integration architectures. None of this research changes them.
