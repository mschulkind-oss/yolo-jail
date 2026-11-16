---
title: Config approval presentation
status: in-review
stage: DESIGN
next: Owner rules on [OQ-CAP1](#OQ-CAP1), [OQ-CAP2](#OQ-CAP2) and [OQ-CAP3](#OQ-CAP3); until then the recommended format and raw-view command remain proposals.
date: 2026-11-16
baseline: HEAD @ 20d40e71d2283172ebbd75a4d99b8679b3cd8ada
owner-request: "Now that we've switched to Toml for the config file format, it's a little weird that we show JSON diffs in that config approval prompt."
---

# Config approval presentation

**In short.** The gate compares and stores a canonical JSON identity, then shows its JSON line diff. Keep that identity unchanged and recommend a complete, deterministic key-path change list in TOML-shaped notation as the primary view; the raw JSON view remains secondary, and its exact user command is unresolved.

**Why it matters.** People author TOML but approve a normalized workspace value tree; line hunks hide path context, removals and ordered-list changes. Resolved mount destinations can change without a config text edit.

**Shape.** A pure presenter compares the stored previous tree with the current approval tree and emits a complete, sorted group per changed config path. Existing mount, repository-scope and grant blocks retain their order and disclosure. Identity, record and prompt predicate do not change.

**Cost.** One structural tree walk, deterministic sequence alignment and TOML-literal formatting, plus prompt/refusal tests and re-verification of [`config-safety.md`](../reference/config-safety.md). No record migration or ask/don’t-ask change.

**Start at [§4](#4-proposed-shape)** for the recommendation, then [§6](#6-rules-behind-the-mocks) for its completeness rules.

**Needs your ruling:** [OQ-CAP1](#OQ-CAP1) (primary view), [OQ-CAP2](#OQ-CAP2) (raw-view command/surface) and [OQ-CAP3](#OQ-CAP3) (flag-path config-only reporting).

**Reads with:** [`config-safety.md`](../reference/config-safety.md) (stale; [§1](#1-current-behavior-verified-baseline) records its limit), [`config/README.md`](../reference/config/README.md), [`live-config-apply.md`](live-config-apply.md), [`report-tiers.md`](../reference/report-tiers.md), [`cli-color.md`](../reference/cli-color.md) and [`config/rulings.md`](../reference/config/rulings.md) for existing accepted-report decisions.

This document is exploration with a recommendation. It does not authorize implementation and does not record an owner choice of format.

## 1. Current behavior (verified baseline)

This baseline was checked against the named source at `20d40e71d2283172ebbd75a4d99b8679b3cd8ada`; no config or approval record was read and no approval flow was run. [`config-safety.md`](../reference/config-safety.md) is the closest reference, but its 2026-09-09 stamp predates TOML and current grant handling.

| Fact | Code path |
| :--- | :--- |
| The gate does a fresh, strict, workspace-only read; project/local layers and includes merge, while user config is excluded. | [`gateread.go`](../../internal/config/gateread.go), [`load.go`](../../internal/config/load.go) |
| TOML and legacy JSONC/JSON normalize to one structured value. | [`newreadableconfig.go`](../../internal/config/newreadableconfig.go) |
| `brokered` is projected out; writable mounts carry their *resolved* host destinations. | [`brokered.go`](../../internal/config/brokered.go) (`approvalConfigPart`) |
| The approval identity is sorted-key, two-space, ASCII-escaped JSON compared as a string with the host-side record. | [`snapshot.go`](../../internal/config/snapshot.go), [`jsonx.go`](../../internal/jsonx/jsonx.go) |
| The display is a line-based unified diff with three lines of context over those JSON strings. | [`difflib.go`](../../internal/config/difflib.go) |
| Block order is header, mounts, repository scope, loophole grants, config diff, counts, one y/N defaulting to N. | [`preflight.go`](../../internal/cli/run/preflight.go) |
| Launch prompt requires stdin and stderr TTYs; otherwise it refuses with the same diff and records nothing. | [`preflight.go`](../../internal/cli/run/preflight.go), [`configapproval_test.go`](../../internal/cli/run/configapproval_test.go) |
| `--accept-config-changes` records without prompting. A config-only change is currently silent; the accepted reporter runs only for scope, grants or mount changes. | [`snapshot.go`](../../internal/config/snapshot.go), [`preflight.go`](../../internal/cli/run/preflight.go) |
| Repository scope and loophole grants have separate records and blocks. | [`scopeapproval.go`](../../internal/config/scopeapproval.go), [`loopholegrantgate.go`](../../internal/config/loopholegrantgate.go) |

`config-safety.md` carries a 2026-09-09 verification stamp and predates TOML and current grant handling; use code and current config references for these details.

## 2. Three things the current prompt conflates

These terms are used throughout.

- **File-text diff** is the difference between bytes in selected workspace config files (TOML or, for legacy workspaces, JSONC/JSON). The gate never computes it. Comments, key order and formatting live only here.
- **Semantic change** is a difference in the gate’s approval input: merged workspace values after the `brokered` projection, together with resolved mount destinations included in that approval part. It excludes user config and runtime defaults. This is what the person is asked to approve.
- **Approval identity** is the canonical JSON string whose byte equality decides whether the gate asks and which is stored on acceptance. Repository-scope and loophole-grant identities remain separate.

Today one JSON string serves as both identity and picture. The owner’s request is about presentation; the recommendation leaves identity, stored records, trust boundaries and gate timing unchanged.

Neither the snapshot nor this proposal describes the fully effective runtime config. User config and built-in defaults are outside both.

## 3. The gap

1. **Wrong notation.** The person wrote `nix = ["jq", "ripgrep"]` under `[install]` and is shown braces, quoted keys and indentation depth.
2. **Line-diff noise.** Adding a list element to canonical JSON changes the previous item line too: `"jq"` becomes `"jq",`, followed by `"ripgrep"`. The unified diff therefore shows punctuation churn as well as the new item (derived from the encoder/diff contract; no approval flow was run).
3. **Lost location.** Three context lines inside a deep object can leave a changed leaf with no visible parent key.
4. **Unexplained removals.** A `-` line does not say whether the key is now unset or was replaced by a lower workspace layer.
5. **Changes with no file edit.** A symlink retarget changes a resolved mount destination, and the person's TOML is untouched.

## 4. Proposed shape

**Change list** means a complete enumeration of changed workspace key paths in the approval input. The synthetic resolved-mount record is represented by the existing mount disclosure block, not a fabricated config path; repository-scope and grant blocks are separate approval parts. Those blocks stay in the prompt and are outside the config-path count. Groups derive from previous/current values, not source-file text.

### 4.1 Decisions (design judgement)

| # | Decision | Reason |
| :--- | :--- | :--- |
| D1 | The approval identity, its record format and the ask/don't-ask rule are unchanged. | A TOML identity would re-prompt or strand every existing record and buys nothing for the reader. |
| D2 | The presenter is a pure function of (previous tree, current tree, existing mount/scope/grant reports, width, color) to text; it never decides whether a change exists. | This keeps presentation apart from effectful approval ([§8](#8-pure-presentation-versus-effectful-approval)). |
| D3 | Paths follow TOML key quoting and dotted-key conventions, with explicit bracketed tokens for array positions; values are TOML literals. The output is *TOML-shaped*, not a TOML document. | The merged tree is not any one file, and a document would suggest a file the person could go and find. |
| D4 | Verbs are words (`added`, `removed`, `changed`, `reordered`), never bare `+`/`-` glyphs or color alone. | They survive `NO_COLOR`, pipes and screen readers. |
| D5 | Every changed config path and every value needed to show its full change is printed, with no elision or silent omission. The header counts groups and, where a table is expanded, its terminal values. | Approval must not cover a path the prompt left out ([§6.6](#66-width-color-accessibility-and-bounds)). |
| D6 | The mount, scope and grant blocks keep their current order, content and textual markers. | That ordering exists so a widening cannot scroll past unnoticed, and this design does not reopen it. |
| D7 | The change list adds no redaction and no user-config/default values. For a changed value it prints the old/new values needed to explain that change, not unchanged sibling values merely as context. | Redaction hides an approved value; showing unrelated values widens exposure ([§6.5](#65-secrets)). |
| D8 | If the identity differs but no config path is found, first account for resolved-mount changes already fully shown by the existing mount block. If any identity difference remains unexplained, or the stored record cannot be decoded, say so and fall back to the raw JSON diff. | The gate must never ask with an empty/unexplained report or hide an identity difference. |
| D9 | One diff walk and one TOML-literal formatter serve the interactive prompt and non-interactive refusal. `--accept-config-changes` uses them only if [OQ-CAP3](#OQ-CAP3) is ruled that way. | The first two surfaces share a report; the accepted-report policy is a separate owner choice. |

### 4.2 Alternatives and verdicts

| Option | What it is | Verdict |
| :--- | :--- | :--- |
| A. TOML-document diff | Serialize both trees as TOML and show a unified line diff. | **Rejected as primary.** It keeps the line-diff problems (context loss, list noise), needs a table-layout policy, and looks like a file diff when it is not one. |
| B. Plain-language categorized summary only | "Packages: adds ripgrep", driven by a per-key vocabulary. | **Rejected as sole view.** A paraphrase can misstate what changed, the vocabulary drifts as the schema grows, and unknown keys need a fallback anyway. Categories are kept only where they already exist: mounts, scope and grants. |
| C. Change list plus secondary raw diff | A precise per-path list in TOML notation, with canonical JSON available on request. | **Recommended.** The list is itself exact, so nothing needs expanding for completeness. The raw view remains for debugging identity questions. |
| D. Keep JSON, add a caption | Explain that JSON is the normalized form. | **Rejected.** It is the cheapest option but fixes none of gaps 2–5. |
| E. Per-file TOML text diff | Diff each source file. | **Rejected.** Comment and order edits would appear although they change nothing, resolved-destination changes would not appear, and no previous file text is stored. |

## 5. Proposed prompts

All blocks are **proposed**. Keys other than `install.nix` are fictional, and the surrounding header and question wording is illustrative. Existing mount, scope and grant block text is abbreviated as `[... existing block ...]`.

### 5.1 Scalar edit

```text
Workspace config changed since last approval: 1 change

  changed   example.log_level
              was  "info"
              now  "debug"

Approve? [y/N]
```

### 5.2 Removal, and removal that reveals a lower workspace layer

```text
Workspace config changed since last approval: 2 changes

  removed   example.timeout
              was  30
              now  (not set in workspace config; your user config or the
                    built-in default applies and is not part of this approval)

  changed   example.retries
              was  5
              now  2
```

The second entry is what dropping a local-file override looks like. The merged value changed, so it is reported as `changed`, which is true. The first entry does not guess the fallback value (D7).

### 5.3 Lists: element change and pure reorder

```text
  changed   install.nix   (1 item added)
              added    "ripgrep"   (current item 2 of 2)

  reordered example.search_path   (same 3 items; all positions shown)
              was  ["a", "b", "c"]
              now  ["c", "a", "b"]
```

Indexes in detail lines are one-based and refer to the full previous or current list as labeled. Duplicates remain separate occurrences. Order is part of approval identity; a permutation is explicitly `reordered`, never treated as no change.

### 5.4 Nested named entities

```text
  added     example.servers.alpha   (table; 2 terminal values)
              example.servers.alpha.args     = ["--stdio"]
              example.servers.alpha.command  = "alpha-server"

  changed   example.servers.beta.args   (1 item removed)
              removed  "--verbose"   (previous item 2 of 2)

  removed   example.rules[3]   (array item 3 of 3; table; 2 terminal values)
              example.rules[3].action  = "skip"
              example.rules[3].name    = "tmp"
```

A table keyed by name gets its own path. Added/removed table groups expand every terminal value under its full path in sorted key order; the stated count is the number of terminal values, with an empty table/list represented explicitly and counted as one empty value. For arrays of tables there is no guessed identity key: a changed element is a remove plus an add at its old and new one-based positions. Equal duplicate elements remain distinct.

### 5.5 Scope-only change, with what the list covers

```text
Approval needed: repository scope changed (workspace config unchanged)

[... existing repository-scope block, unchanged by this design ...]

Workspace config: no change.

Approve? [y/N]
```

When config changes, the header gives the scope of this view, not a per-key source attribution:

```text
Workspace config changed since last approval: 3 paths
  merged workspace config (project/local files and their includes;
  user config is not part of this approval)
```

The current loader tracks per-value origins, but the prior approval snapshot contains only JSON values. This proposal does not promise source-file provenance for a changed or removed path.

### 5.6 Security-sensitive change with no file edit

```text
Approval needed: workspace config changed (resolved writable mount changed)

[... existing writable-mount block, with its existing textual marker ...]

Workspace config change list: no config-key paths changed.

Approve? [y/N]
```

The approval identity includes a synthetic record of resolved mount sources. A symlink retarget can change that record without a config text edit; the existing marked mount block fully discloses it and is not duplicated as a fabricated TOML key path or counted as a config-key change.

## 6. Rules behind the mocks

### 6.1 Values TOML cannot spell

| Case | Rendering | Note |
| :--- | :--- | :--- |
| Absent key | `(not set in workspace config ...)` | Never rendered as an empty string or `null`. |
| JSON `null` in the tree | `(null)` in parentheses | TOML has no null. The JSONC decoder, normalized values and JSON snapshot encoder can retain/emit null; the presenter handles it without confusing it with an absent key. |
| Mixed-type list | Inline TOML array | TOML-shaped literal; array order and repeated values are preserved. |
| Empty table or list | `{}` / `[]` | A key going from absent to empty is `added`, not silent. |
| Key needing quotes | Quoted dotted segment, for example `example."a.b".c` | Each map key is one TOML key segment; quotes/escapes preserve punctuation within the original key. Array positions use an explicit one-based `[n]` path token. |
| Multi-line or very long string | Printed in full, wrapped, as a basic string with escapes | Never truncated (D5). |

Anything in parentheses is presenter vocabulary, not TOML. Dotted paths use bare key segments only when TOML permits them, otherwise quoted/escaped segments; list indexes are explicit one-based tokens. The complete display is TOML-shaped, not pasteable source.

### 6.2 Comments and order

Comment, whitespace and key-order edits do not reach the tree. They do not change the identity and never appear. Object keys in the presentation sort by their escaped path, independent of file order. List order and duplicate occurrences are part of identity and are preserved.

### 6.3 Removals

A removed path always prints its old value in full; the `now` line states only that the workspace no longer sets it and does not guess a lower workspace value, user value or default. An added or removed table is one path group expanded to all terminal child paths in sorted order; the table heading states the number of terminal values. Empty tables/lists are explicit terminal values and count as one. This rule is symmetric for additions and removals.

### 6.4 Surfaces

| Surface | Behavior |
| :--- | :--- |
| Interactive prompt (stderr) | Change list after the existing blocks, then the single y/N. The input grammar is unchanged. |
| Non-interactive refusal | The identical change list plus the existing advice. Nothing is recorded, as today. |
| `yolo config apply` and attach | Same presenter. These already share the gate. |
| `--accept-config-changes` | See [OQ-CAP3](#OQ-CAP3). |

### 6.5 Secrets

The proposal prints the values the JSON diff prints today, in a different notation. It adds no redaction, because a redacted value is one the person approved without seeing. It adds no user-config or default values, because that would newly expose data outside the approval. Adding redaction later would be an owner decision about disclosure, not a presentation tweak.

### 6.6 Width, color, accessibility and bounds

- **Alignment.** A list that is a pure permutation with the same multiplicities is one `reordered` group showing both complete orders. Otherwise align exact items with a longest-common-subsequence walk; ties select the earliest previous index, then earliest current index. Each unmatched occurrence is printed as an add/remove with its original one-based index. Duplicates are never set-deduplicated; edits to array-table items are remove-plus-add, never guessed as identity-preserving edits.
- **Counts and completeness.** The header counts changed path groups; each added/removed table group separately states its terminal-value count. Empty containers count as one terminal value. Every added/removed occurrence is printed, including duplicate occurrences; no cap, `… and N more`, pager, or silent omission. A 200-path first launch reports all 200.
- **Width.** Wrapping may break long values only at valid escape/character boundaries; continuation indentation preserves which `was`/`now` value it belongs to. Nothing depends on column alignment. Non-TTY output is not hard-wrapped.
- **Color.** Color follows the existing contract in [`cli-color.md`](../reference/cli-color.md). It only reinforces verb words; additions green, removals red, paths dim. Existing writable-mount markers remain text.
- **Accessibility.** Verb words lead each group; `was` and `now` are literal labels. No meaning is carried only by glyph, position or color.

## 7. Non-goals

- Changing the approval identity, the record format or location, or what triggers a prompt.
- Changing the mount, scope or grant blocks, their order, or their non-suppressibility.
- The host-configuration-apply gate ([`host-apply-staleness.md`](../reference/host-apply-staleness.md)). Its change list and privacy ruling are a different gate with different data.
- Per-key source-file attribution. The stored record holds no provenance for the previous side.
- A general renderer or theme framework, or an implementation plan.

## 8. Pure presentation versus effectful approval

| Concern | Kind | Changed here? |
| :--- | :--- | :--- |
| Read, merge, project, serialize, compare | Decides whether to ask | No |
| Write record on `y` or on the flag | Effect | No |
| Turn two trees into text | Pure | **Yes, the whole proposal** |

**Invariant.** The gate still decides whether to ask by comparing the same approval identities. When the config identity changes, the config presenter lists all changed config-key paths and must print at least one line. On a scope-only gate it says `Workspace config: no change.` The existing mount block counts as complete presentation of resolved-mount-record changes; if a config identity difference has no config-key path and no mount block explains it, D8 prints a notice and raw JSON diff rather than an empty list or bare question.

## 9. Costs, risks, migration and observability

| Item | Assessment |
| :--- | :--- |
| Build cost | One structural diff, one TOML-literal formatter, and a rewrite of the diff-text assertions in the prompt tests. |
| Risk: the presenter drops a path | Mitigated by D8 and by a property worth testing: applying the printed changes to the previous tree yields the current tree. |
| Risk: list alignment misleads | Element-equality alignment can show an edit as remove plus add. That is verbose but never wrong. |
| Risk: long first-launch output | Accepted. It is the truthful size, and the header count warns the reader. |
| Risk: readers assume the output is pasteable TOML | Mitigated by the parenthesized vocabulary and the provenance line. |
| Migration | None for records. Legacy JSONC workspaces get the same list, since notation follows the tree and not the file format. `config-safety.md` needs re-verification regardless of this design. |
| Stderr text consumers | None are known. The packet identified no machine consumer of the diff text, and none is assumed here. |
| Observability | The raw canonical diff is the fallback and, if [OQ-CAP2](#OQ-CAP2) selects it, an explicit read-only diagnostic view; its command syntax and comparison semantics remain open. |

## 10. Verified checks and remaining uncertainty

The evidence packet supplied the first-pass claims. The following bounded source check resolves A1–A4 against the current tree; it does not claim a universal search for historical owner decisions.

| ID | Check | Finding |
| :--- | :--- | :--- |
| A1 | Can the gate name contributing files in the header? | The loader records a per-value source tree with file origins and writer order ([`sources.go`](../../internal/config/sources.go)). The current gate result keeps that tree private, and the saved approval snapshot contains only JSON values ([`gateread.go`](../../internal/config/gateread.go), [`snapshot.go`](../../internal/config/snapshot.go)). Therefore exact source-file lists are not assumed; the mock uses a generic merged-workspace line, and per-key attribution is a non-goal. |
| A2 | Can an approval value be JSON `null`? | The JSON value model and snapshot encoder accept `nil` and emit `null`; legacy JSONC input can represent it, and `SchemaTypeAny` fields preserve unconstrained values ([`jsonx.go`](../../internal/jsonx/jsonx.go), [`readablevalidate.go`](../../internal/config/readablevalidate.go), [`schema.go`](../../internal/config/schema.go)). The presenter renders explicit `(null)` distinct from absent; it does not claim every schema path permits null. |
| A3 | Does list append create JSON comma churn? | Yes, mechanically: the snapshot encoder writes the array separator after the previous item and before the next line ([`jsonx.go`](../../internal/jsonx/jsonx.go)); the unified diff compares those lines ([`difflib.go`](../../internal/config/difflib.go)). Appending `"ripgrep"` after `"jq"` changes the previous line from `"jq"` to `"jq",` and adds the new item line. No approval flow was run. |
| A4 | Is config-only accepted reporting already ruled? | The inspected [`WW-D20`](workspace-widening.md#WW-D20) covers accepted repository-scope blocks/counts, and [`RC-D24`](../reference/config/rulings.md#RC-D24) covers writable-mount disclosure. Neither inspected ruling covers config-only acceptance reporting. Current code calls `ReportAccepted` only for scope, grants or mount-source changes, so config-only acceptance is silent ([`snapshot.go`](../../internal/config/snapshot.go), [`preflight.go`](../../internal/cli/run/preflight.go)). This is bounded evidence, not a claim that no such ruling exists anywhere; keep [OQ-CAP3](#OQ-CAP3) open. |

No approval record or real workspace config was opened. The only remaining choices are the owner questions below; A1–A4 are no longer unresolved factual assumptions.

## 11. Open questions

1. 💬 <a id="OQ-CAP1"></a>**[OQ-CAP1](#OQ-CAP1): Which view should lead an approval of a changed workspace config?**

   You author TOML; today the prompt displays a unified diff of the canonical JSON identity. The choice is whether to replace that primary view, and with what.

   - **(a) TOML-document diff.** Render both trees as TOML and diff those documents. Familiar, but still subject to line-context loss and array punctuation churn, and it resembles a file diff that the gate did not compute.
   - **(b) Categorized prose only.** Summarize by known config concepts. Shorter for common keys, but it can misstate semantics and needs schema-specific coverage and an unknown-key fallback.
   - **(c) Complete TOML-shaped path list.** Every changed path and value is described structurally, with deterministic array handling; it is explicit but can be long.
   - **(d) Keep JSON with a caption.** Smallest change, but it leaves the current readability and context problems.

   <!-- vantage: question id=OQ-CAP1 leaning="(c) complete TOML-shaped path list, with the canonical JSON identity unchanged" -->

   _Leaning:_ (c), as recommended in [§4.2](#42-alternatives-and-verdicts). This is a design recommendation, not an owner selection.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 <a id="OQ-CAP2"></a>**[OQ-CAP2](#OQ-CAP2): If the raw JSON view remains secondary, how does a user invoke it and what does it compare?**

   Today the only display is the prompt/refusal diff, and it is the canonical JSON diff. A recommendation that says “on request” is incomplete without a user-visible path, especially for a non-interactive refusal or later diagnosis. The command spelling, flags, inputs and whether it reads or updates approval state are all unsettled; no syntax is selected here.

   - **(a) No raw view beyond the D8 exceptional fallback.** No new command surface; simplest, but limits ordinary diagnostics.
   - **(b) Read-only identity view.** A command displays the current canonical approval JSON only; it does not compare with the stored approval. The command syntax remains an owner choice.
   - **(c) Read-only raw comparison.** A command compares the current approval input with the stored identity and prints the canonical JSON diff; the exact verb, flag and stale/invalid-record behavior remain owner choices.
   - **(d) Interactive expansion.** A prompt affordance expands the raw diff; it does not serve pipes or non-interactive refusals and changes prompt interaction design.

   <!-- vantage: question id=OQ-CAP2 leaning="Retain a read-only raw comparison, but leave command syntax and inputs to owner choice" -->

   _Leaning:_ Keep a read-only raw view available, but do not choose its command syntax or exact input semantics in this design. This is a real CLI-surface gap, not a plan-level naming detail.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 <a id="OQ-CAP3"></a>**[OQ-CAP3](#OQ-CAP3): Should `--accept-config-changes` disclose a config-only change list before recording it?**

   Today this flag accepts without an interactive prompt, and a config-only change produces no report. The existing accepted-report hook covers repository scope, grants and mounts; its current scope is not a ruling about config-only reports. The question is specifically whether the new list should be printed for a flag-path config-only acceptance; existing sensitive blocks remain unchanged.

   - **(a) Keep config-only acceptance silent.** The flag is explicit opt-in and automation logs stay short.
   - **(b) Print the same complete list as an “accepted” report before recording.** This leaves a log of which config values the flag consented to, but exposes the config values in the launch stream.

   The bounded source check in [§10](#10-verified-checks-and-remaining-uncertainty) found WW-D20 for accepted scope blocks/counts and RC-D24 for writable-mount disclosure, but no inspected ruling on config-only acceptance reporting. This remains an owner choice; do not infer an answer from those adjacent rulings.

   <!-- vantage: question id=OQ-CAP3 leaning="Print the same list as an accepted report before recording, subject to owner choice" -->

   _Leaning:_ (b), as a presentation recommendation only. This would change disclosure behavior and is not implemented or authorized by this design.

   **Answer:**
   > _(empty — fill in when decided)_

## 12. Acceptance examples

The design is met when all of these hold:

1. Appending `"ripgrep"` to `install.nix` yields one changed-list path and one added item; the presenter has no JSON braces or trailing-comma churn.
2. Editing only comments or key order in a TOML file produces no prompt, as today.
3. Reordering a list produces a `reordered` group showing both orders.
4. Removing a key shows its full old value and the "not set in workspace config" line, with no guessed fallback value.
5. A symlink retarget under a writable mount shows the existing marked mount block; if no config key changed, that block is the complete mount disclosure and the config-path list says no config-key paths changed.
6. A scope-only change shows the scope block and "Workspace config: no change."
7. With `NO_COLOR` or a piped stderr, every verb and the mount marker are still present as text.
8. A first launch with 200 changed leaf values prints each leaf in deterministic groups; the header group count and each table’s terminal-value count agree with the output.
9. An identity difference with no unexplained structural path prints the relevant mount block; an unexplained empty structural diff prints the D8 notice and raw diff, never a bare question.
10. Records written before the change compare as equal after it, with no re-prompt.

## 13. Decision ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| CAP-R1 | The TOML-era approval prompt showing JSON diffs is an issue to explore; this authoring request does not select a format or authorize implementation. | 2026-11-16 | [§1](#1-current-behavior-verified-baseline) | No |

The open questions remain in [§11](#11-open-questions), not in the ledger. The design recommendation is complete as a proposal, but the presentation and raw-view surface remain owner-choice-dependent.