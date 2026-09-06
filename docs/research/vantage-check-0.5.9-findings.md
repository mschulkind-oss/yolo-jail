---
title: "vantage-check 0.5.9 — defects found adopting the new reference rules"
summary: "Eight defects in the ref/* and vantage/oq-* rules, each with a minimal reproduction, found while linking 5139 findings across a 143-file corpus. Two are correctness gaps that make the rules accept a wrong link or miss a real one; one is a deadlock between two rules."
tags: [tooling, vantage-check, documentation]
---

# vantage-check 0.5.9 — defects found adopting the new reference rules

**Status:** REPORT, 2026-09-06. Written for upstream. Every defect below has a minimal
reproduction that was run; where a claim is inferred rather than measured it says so.

> [!NOTE]
> **This document reports 46 errors under the rules it documents, and that is D12 rather than an
> oversight.** Every one is a specimen — `§4.3b`, `OQ-PD12a` and friends quoted as examples of what
> the rules do to them. They are not references and must not be linked; linking a specimen would
> destroy the thing being shown. See D12.

**How this was found.** `vantage-check` 0.5.9 added `ref/unlinked-section`, `ref/unlinked-oq`,
`ref/unlinked-file` and `vantage/oq-missing`. On this repo that took a corpus that reported
**"nothing to fix"** under 0.5.8 to **5139 errors across 143 files** overnight. We adopted the
rules and worked the corpus down to **165**. The defects below are what four independent passes
hit on the way, deduplicated.

The rules are a good idea and most of what they found was real — including **21 genuinely stale
cross-references** that had been invisible for months, three OQ ids that exist in no document, and
a whole reference doc still carrying two deleted documents' section numbers. Nothing here argues
against the rules. It argues that four of them are unsafe to apply mechanically as shipped.

---

## Severity 1 — the rules accept a wrong link and reject the right one

### D1. `ref/unlinked-section` truncates a trailing letter, then validates against the wrong section

The tokenizer matches `§\d+(\.\d+)*` and drops a trailing letter. Lettered subsections are a
first-class convention in this corpus (20+ headings: `2.1a`, `3.3a`, `4.3a/b`, `5.2a`, `9a–9d`,
`4a–4f`, `6a–6c`).

**Reproduction** — a document containing both `## 4.3 Four gates` and `## 4.3b The scope model`:

| Source | 0.5.9 |
| :--- | :--- |
| `§4.3b` (bare) | error — **and the message says `§4.3`** |
| `[§4.3b](#43b-the-scope-model)` — **correct** | **ERROR:** *"`§4.3` links to `#43b-…`, but §4.3 in that document is `#43-four-gates`"* |
| `[§4.3](#43-four-gates)b` — **wrong** | **PASSES** |
| `§[4.3b](#43b-the-scope-model)` — workaround | passes |

So the rule demands a link, refuses the correct one, and accepts one that lands the reader on a
different section. In one case (`broker-as-a-pack.md`) `§5.2a` *retracts* `§5.2`, so obeying the
rule points readers at the retracted claim.

**It also breaks links that were already correct** before anyone touched them, e.g.
`pack-config-keys.md:65`'s `[§3b](#3b-workspace-scope-and-what-makes-it-safe-now)`.

**Quiet failure mode.** `[§6a](other.md#6a-…)` *passes* when the target has no plain `## 6` for the
truncated token to compare against. So the rule fires only where a sibling plain-numbered section
exists — exactly where the mistake it induces is most damaging.

**Same root cause in `ref/unlinked-oq`, and worse there:** `OQ-PD12a` is reported as `OQ-PD12`.
Applying the fix at the reported column yields `` [`OQ-PD12`](#decision-ledger)a ``. `OQ-PD12` and
`OQ-PD12a` are **distinct rulings where PD12a supersedes half of PD12**, so the suggested fix
misdirects. This corrupted four sites before it was caught.

**Fix:** match `§\d+(\.\d+)*[a-z]?` and `OQ-[A-Z]*\d+[a-z]?`. Roughly 65 links in this corpus use
the `§[4.3b](…)` workaround purely to route around it and should be normalised back afterwards.

### D2. The number check is skipped when the target has no heading with that number

**Reproduction** — a document with `## 8. What I would actually do` and `## 9. Something`:

```markdown
[§9.5](#8-what-i-would-actually-do)      → ✓ nothing to fix
```

A link labelled §9.5 pointing at §8 passes clean. The comparison only runs when the target has a
heading numbered with the truncated token; where it does not — which is precisely where
sub-numbered items live in body text — **any anchor is accepted**.

Combined with D1: the rule refuses the right link for `§4.3b` and accepts one that misdirects.

### D3. Cross-document references validate the number, never the document

`[§2](#2-the-inventory)` and `[§2](./unrelated.md#2-something-else)` both pass. The rule verifies
number↔anchor *within whatever document the link names*, so a reference belonging to document A but
linked to document B's own §N is undetectable.

This is not hypothetical: a mechanical pass here produced two such links
(`loophole-transport.md` §2.1b and §4d, both aimed at the host document instead of
`loophole-packaging.md` / `sequencing-2026-07.md`) and nothing flagged either. Across the corpus,
**44 references needed hand-adjudication** because the intended document was named only in prose
("the plan", "the design doc", "recorded in zai-plumbing §3") while the host document *also* had a
section of that number.

Given the rule's stated purpose — *"nothing notices when the section is renumbered"* — this is the
case it most needs to notice. A heuristic (warn when a `.md` filename appears within N characters
before an intra-document `§` link) would have caught both instances above.

---

## Severity 2 — false positives with no correct fix

### D4. `ref/unlinked-file` tests existence against `/` and writes the link relative to the document

**Reproduction**, from a document in `docs/plans/`:

| Source | 0.5.9 |
| :--- | :--- |
| `` `/workspace/flake.nix` `` | **ERROR**, suggests `` [`/workspace/flake.nix`](.//workspace/flake.nix) `` |
| `` `/zzz/nope/absent.conf` `` | no error (does not exist) |
| `` `/etc/hostname` `` | no error (no extension) |
| `` `roadmap.md` `` (real sibling) | error — **correct** |

Existence is tested against the real filesystem root; the suggestion is written against the
document's directory. `.//workspace/flake.nix` resolves to nothing. **The message is also factually
false** — *"names a file that exists beside this document"* — there is no `workspace/` beside
`docs/plans/`.

These paths name filesystem locations inside a container, not repo files. There is no correct link
to write. **69 instances** across this corpus were skipped for that reason.

**Fix:** never emit a document-relative suggestion for a path beginning `/`; either ignore absolute
paths or offer an allowlist.

### D5. `ref/unlinked-file` matches a same-named sibling that is not the file meant

`AGENTS.md` and `README.md` exist in nearly every directory here. Matching on basename against the
document's own directory produces a confidently wrong link. Three measured instances, all reverted
after being applied: two in `examples/claude-fzf-pack/README.md` citing the **repo-root**
`AGENTS.md` (its Limitations section, its `stagePacks` invariant — neither is in the pack's own
`AGENTS.md`), and one enumerating a *pack's* root-level filenames that matched `docs/plans/README.md`.

### D6. The suggested fix does not render when the code span carries more than the reference

Inline code spans are a deliberate target — the suggestion keeps the backticks inside the link,
`` [`OQ-PD19`](#OQ-PD19) `` — and that is right when the span *is* the reference. When it carries
other text the suggestion produces dead markup:

```markdown
source:      the roadmap cited it as `auth OQ-9`
suggested:   the roadmap cited it as `auth [OQ-9](#OQ-9)`     ← renders literally
only fix:    the roadmap cited it as [`auth OQ-9`](#OQ-9)
```

Measured on `` `auth OQ-9` ``, `` `💬 OQ-B1b` ``, `` `§1 row 1` ``, `` `N3/OQ-1` ``. Not a rule
error — the *message* should say "wrap the whole code span".

### D7. A range has no expressible fix

`` `OQ-T1..T4` `` reports `OQ-T1`, so the mechanical fix links the whole span to T1's section and
tells a reader looking for T4 to read T1. Three instances; all hand-pointed at the ledger that
actually covers T1–T9. The rule has no notion of a range.

### D8. The rule fires on a question's own defining heading

`### 💬 OQ-TP10 — …` is where OQ-TP10 is *defined*; `ref/unlinked-oq` demands it be a link, and the
only satisfying edit is a self-link. **17 instances in one partition alone.**

The tool already has the machinery to avoid this: an `<a id="oq-lm1"></a>` before the id suppresses
the finding, and a ledger cell containing *only* `**OQ-TP1**` is exempt. But a ledger cell carrying
more than the bare id is **not** exempt, and adding bold does not help (measured on
`reference/providers.md:307–318`) — so the same self-link workaround is forced there too.

**Fix:** treat a heading or ledger row that *defines* an id as a definition, not a reference.

---

## Severity 3 — false negatives

### D9. `vantage/oq-missing` does not detect a heading-form open question

**This is the most consequential defect in the set**, because the rule exists so a reviewer gets a
one-click answer in review mode, and it is silent on exactly the questions that have one to give.

Heading-form (`### 💬 OQ-X — …` with a `_Leaning:_` and an empty `**Answer:**`) is this corpus's
convention for its *live* questions. Only the list form is reported.

**Measured:** removing the `oq` directive from `docs/design/trust-paths.md`'s `### 💬 OQ-TP10`
produces **no error at all**. The two sole live questions of this project's two most-worked
documents — `OQ-TP10` and `OQ-PD19` — were invisible to the rule, and were only given directives
because a human noticed.

The documented directive form *does* silence the rule and *does* create the anchor. Guide and
linter agree. Only the detection is missing.

### D10. Repo-root-relative paths are silently ignored

From a document in `docs/plans/`: `internal/cli/pack.go`, `docs/design/program-delivery.md`,
`packs/claude/pack.json` and `flake.nix` produce **no finding**, while `roadmap.md` and
`../design/pack-system.md` correctly do. The rule resolves only against the document's directory,
so repo-root-relative citations — very common here — are real, followable references it ignores.

The corpus-wide count of 318 `ref/unlinked-file` findings therefore *understates* the work.

---

## Severity 4 — a deadlock between two rules

### D11. `vantage/oq-missing` and `vantage/oq-id-format` cannot both be satisfied

`docs/design/macos-user-home-tiers.md:173`, measured both directions:

- **without** the `oq` directive → `vantage/oq-missing` demands one
- **with** it → `vantage/oq-id-format` refuses: *"`OQ-HT-2` is not a usable open-question id"*

The grammar is `OQ-` + optional short uppercase prefix + digits, which rejects a hyphenated series
prefix. The only escape is renaming the `OQ-HT-1/2/3` series — 13 mentions across three files.

**Fix:** either admit `OQ-[A-Z]+-\d+` in the grammar, or have `oq-missing` stand down when
`oq-id-format` would reject the id it is asking to be declared.

---

### D12. A document *about* the reference rules cannot satisfy them

Measured on this file: **46 errors** — 24 `ref/unlinked-section`, 21 `ref/unlinked-oq`,
1 `ref/unlinked-file` — every one a specimen rather than a reference.

The existing exemptions do not reach the case. Fenced and indented blocks are exempt, but a
specimen belongs in the comparison table that shows source beside verdict, and a table cell cannot
be fenced. An `<a id="…"></a>` suppresses a *definition*, not a quotation. So the rules are
unsatisfiable for any document whose subject is references — a style guide, a migration note, or a
bug report like this one.

**Fix:** a file-level or region-level opt-out (`<!-- vantage: ignore ref/* -->`), or an inline
specimen escape. This is the same shape as D6 and D8: the rules model every occurrence of an id as
a reference, when a corpus contains at least four kinds — reference, definition, specimen, and
quotation.

## One non-defect worth recording

`vantage-check` does not strip HTML-looking text inside a heading's code span:
`` ### 5.2 The CLI shape: `yolo host <verb>` `` slugs to `#52-the-cli-shape-yolo-host-verb-…` —
`<verb>` becomes `verb`, not nothing. Any tool computing anchors with a github-slugger clone that
strips `<…>` as a tag emits a dead anchor here. `link/dead-section-anchor` catches it and its
"Did you mean" suggestion is correct, so this is a trap for *consumers*, not a bug.

---

## What the rules found that was real

Recorded so the report is not read as a complaint:

- **21 stale cross-references**, including a `§10` in a document ending at §9; a `§6.5 of the
  implementation spec` where that phrase occurs once in the whole corpus and names no document; and
  a `§11.2` about a Bedrock auth pack that the target section says nothing about.
- **Three OQ ids that exist in no document** — `OQ-PT3`, `OQ-PT8`, `OQ-CS9` — all carried forward
  from two design docs deleted on 2026-09-02.
- **`docs/reference/providers.md` inherited two deleted documents' section numbers.** It has
  entirely unnumbered headings, yet ~49 references across the corpus still cite `providers.md §N.M`.
- **`zai-plumbing.md` had six references to a `§4.1` that never existed** — checked against its
  first commit.
- **Two archived documents** still cited by section: `go-port-post-transition.md` (6 references) and
  an uncommitted `.research/REPORT.md` (2).
