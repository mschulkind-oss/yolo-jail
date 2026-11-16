---
title: Config approvals show both what changed and what it means
status: accepted
stage: DECIDED
next: Review this design, then write the implementation plan; nothing is built.
date: 2026-11-16
baseline: HEAD @ 20d40e71d2283172ebbd75a4d99b8679b3cd8ada
owner-request: "Now that we've switched to Toml for the config file format, it's a little weird that we show JSON diffs in that config approval prompt."
---

# Config approvals show both what changed and what it means

**Status:** 2026-11-16 (`c3ac9c649`). The owner rulings [OQ-CAP1](#OQ-CAP1)–[OQ-CAP3](#OQ-CAP3) are recorded and the designer has closed the display mapping, the raw-comparison command and the accepted report. No owner question is open. Nothing is built.

> **In short.** Keep the approval identity and gate unchanged. Show two labeled blocks for every config change: an **Interpreted** block (areas and effects) and an **Exact** block (dotted paths and values), cross-referenced by number. `yolo config approval-diff` prints the raw JSON comparison, read-only. `--accept-config-changes` now reports config-only changes.

**Why it matters.** People author TOML but approve a normalized workspace value tree; line hunks hide path context, removals and ordered-list changes. Resolved mount destinations can change without a config text edit.

**The shape.** A pure presenter compares the stored previous tree with the current approval tree and emits one numbered group per changed config path. Each group appears once in the Exact block (verb, dotted path, values) and once in the Interpreted block (area, effect), so neither view can omit a change the other shows. Existing mount, repository-scope and grant blocks retain their order and disclosure. Identity, record and prompt predicate do not change.

**Cost.** One structural diff, two block formatters, one flat root-to-label table, one read-only `yolo config` verb, a wider condition for the accepted report, plus prompt/refusal/accepted tests and re-verification of [`config-safety.md`](../reference/config-safety.md). No record migration or ask/don’t-ask change.

**Start at [§5.1](#51-anatomy-and-order)** for what the prompt looks like, then [§6.7](#67-interpreted-view) for what the interpretation may and may not say, and [§6.8](#68-raw-comparison-on-request) for the raw-comparison command.

**Needs your ruling:** None. The owner chose both initial views, the on-request raw comparison, and config-only reporting with `--accept-config-changes`. The remaining mechanics are decided in [§4.1](#41-decisions-design-judgement) as design judgement.

**Reads with:** [`config-safety.md`](../reference/config-safety.md) (stale; [§1](#1-current-behavior-verified-baseline) records its limit), [`config/README.md`](../reference/config/README.md), [`live-config-apply.md`](live-config-apply.md), [`report-tiers.md`](../reference/report-tiers.md), [`cli-color.md`](../reference/cli-color.md) and [`config/rulings.md`](../reference/config/rulings.md) for existing accepted-report decisions.

---

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
| The prompt header is `<parts> changed since last run:` and the question is `Accept these <parts> changes? [y/N] `; the accepted-report header is `<what>; --accept-config-changes records it as approved:`. Scope and grant rows are marked, aligned rows under a labeled header, with a separate counts line. (Checked 2026-11-16 at `c3ac9c649`.) | [`preflight.go`](../../internal/cli/run/preflight.go), [`scopeapproval.go`](../../internal/config/scopeapproval.go), [`snapshot.go`](../../internal/config/snapshot.go) |
| No `yolo config` verb prints or compares the stored approval record. `ReadApprovalRecord` returns the stored text (absent: no record, no error); `ApprovalRecordPart` computes the current gate projection. (Checked 2026-11-16 at `c3ac9c649`.) | [`config.go`](../../internal/cli/config.go), [`migraterecord.go`](../../internal/config/migraterecord.go) |

`config-safety.md` carries a 2026-09-09 verification stamp and predates TOML and current grant handling; use code and current config references for these details.

## 2. Three things the current prompt conflates

These terms are used throughout.

- **File-text diff** is the difference between bytes in selected workspace config files (TOML or, for legacy workspaces, JSONC/JSON). The gate never computes it. Comments, key order and formatting live only here.
- **Semantic change** is a difference in the gate’s approval input: merged workspace values after the `brokered` projection, together with resolved mount destinations included in that approval part. It excludes user config and runtime defaults. This is what the person is asked to approve.
- **Approval identity** is the canonical JSON string whose byte equality decides whether the gate asks and which is stored on acceptance. Repository-scope and loophole-grant identities remain separate.

Today one JSON string serves as both identity and picture. The owner’s direction changes the picture only: keep identity, stored records, trust boundaries and gate timing unchanged.

Neither the snapshot nor this proposal describes the fully effective runtime config. User config and built-in defaults are outside both.

## 3. The gap

1. **Wrong notation.** The person wrote `nix = ["jq", "ripgrep"]` under `[install]` and is shown braces, quoted keys and indentation depth.
2. **Line-diff noise.** Adding a list element to canonical JSON changes the previous item line too: `"jq"` becomes `"jq",`, followed by `"ripgrep"`. The unified diff therefore shows punctuation churn as well as the new item (derived from the encoder/diff contract; no approval flow was run).
3. **Lost location.** Three context lines inside a deep object can leave a changed leaf with no visible parent key.
4. **Unexplained removals.** A `-` line does not say whether the key is now unset or was replaced by a lower workspace layer.
5. **Changes with no file edit.** A symlink retarget changes a resolved mount destination, and the person's TOML is untouched.

## 4. Proposed shape

**Change list** means a complete enumeration of changed workspace key paths in the approval input, as numbered **groups**. The owner has ruled that the initial display contains both an exact dotted-path view and a structured interpreted view ([OQ-CAP1](#OQ-CAP1)). Both are labeled blocks in the style of the gate’s repository-scope block: a header line, then short marked rows. Neither is a prose paragraph, and neither is a pipe table, because full values must wrap on a narrow terminal ([§6.6](#66-width-color-accessibility-and-bounds)).

The two views are the same group list rendered twice. Every group has one number, one Exact entry and one Interpreted row. The interpretation adds grouping, a label for the area and a fixed effect phrase; it never adds a judgement ([§6.7](#67-interpreted-view)).

The synthetic resolved-mount record is represented by the existing mount disclosure block, not a fabricated config path; repository-scope and grant blocks are separate approval parts. Those blocks stay in the prompt and are outside the config-path count. Groups derive from previous/current values, not source-file text.

### 4.1 Decisions (design judgement)

| # | Decision | Reason |
| :--- | :--- | :--- |
| D1 | The approval identity, its record format and the ask/don't-ask rule are unchanged. | A TOML identity would re-prompt or strand every existing record and buys nothing for the reader. |
| D2 | The presenter is a pure function of (previous tree, current tree, existing mount/scope/grant reports, width, color) to the exact view plus a structured interpreted view; it never decides whether a change exists. | This keeps presentation apart from effectful approval ([§8](#8-pure-presentation-versus-effectful-approval)). |
| D3 | The exact view uses dotted TOML key notation, explicit bracketed array positions, and TOML literals. The output is *TOML-shaped*, not a TOML document. | The merged tree is not any one file, and a document would suggest a file the person could go and find. |
| D4 | Verbs are words (`added`, `removed`, `changed`, `reordered`), never bare `+`/`-` glyphs or color alone. | They survive `NO_COLOR`, pipes and screen readers. |
| D5 | Every changed config path and every value needed to show its full change is printed, with no elision or silent omission. The header counts groups and, where a table is expanded, its terminal values. | Approval must not cover a path the prompt left out ([§6.6](#66-width-color-accessibility-and-bounds)). |
| D6 | The mount, scope and grant blocks keep their current order, content and textual markers. | That ordering exists so a widening cannot scroll past unnoticed, and this design does not reopen it. |
| D7 | The change list adds no redaction and no user-config/default values. For a changed value it prints the old/new values needed to explain that change, not unchanged sibling values merely as context. | Redaction hides an approved value; showing unrelated values widens exposure ([§6.5](#65-secrets)). |
| D8 | Before printing, the presenter replays its own group list onto the previous tree and compares the canonical result with the current identity. Differences confined to the synthetic resolved-mount record count as explained only when the mount block is printed. If the replay does not match, or the stored record cannot be decoded, both views are dropped, one notice line says so, and the raw JSON diff is printed instead. | The gate must never ask with an empty/unexplained report or hide an identity difference. The replay turns "unexplained" into a check rather than a judgement. |
| D9 | One diff walk and shared presentation logic serve the interactive prompt, non-interactive refusal, and `--accept-config-changes` accepted report. | The owner ruled that config-only changes are reported on the flag path ([OQ-CAP3](#OQ-CAP3)); the existing gate and identity remain unchanged. |
| D10 | Groups are numbered `[1]`…`[n]` in sorted-path order. The Exact block lists them in that order. The Interpreted block has exactly one row per group, carrying the same number. | A reader, or a test, can confirm that no change is missing from either view by counting. It also gives the owner like-for-like examples when pruning a view later. |
| D11 | Order on every surface: existing header; existing mount, scope and grant blocks; config count line; **Interpreted**; **Exact**; existing counts; raw-comparison footer; question. | Sensitive blocks keep first position (D6). The short orienting view comes first, and the authoritative view sits directly above the question. |
| D12 | The interpretation vocabulary is bounded: one flat table of labels keyed by top-level config key, a structural rule for the area, and a fixed set of effect phrases derived from the Exact verb. An unlabeled key gets a generic row, never a guess. | No per-key vocabulary exists in the schema today (`SchemaKey` has no description or group field, [§10.1](#101-checks-added-2026-11-16)), and one that paraphrased each setting would drift and could misstate a change. |
| D13 | The Interpreted block carries no risk, severity or safety wording and no severity color. Writable-mount, scope and grant disclosure stays solely in the existing blocks with their existing markers. | A label such as "low risk" printed by the tool would read as the tool’s authority. The presenter does not know enough to say it. |
| D14 | The raw comparison is a new read-only verb, `yolo config approval-diff`, with no flags. It is named in a one-line footer on the prompt and the refusal. The y/N input grammar is unchanged. | No existing verb does this ([§1](#1-current-behavior-verified-baseline)). `config diff` is already taken by a compatibility refusal. A key inside the prompt would change an approval grammar that this design does not reopen. |
| D15 | Presenter text is ASCII. Values and keys print with every control and non-ASCII character as a `\uXXXX` escape, as the canonical identity already does. | An approval prompt must not be reshaped by bidirectional or control characters inside a value. Existing blocks keep their existing glyphs (D6). |

### 4.2 Alternatives and verdicts

| Option | What it is | Verdict |
| :--- | :--- | :--- |
| A. TOML-document diff | Serialize both trees as TOML and show a unified line diff. | **Rejected as primary.** It keeps the line-diff problems (context loss, list noise), needs a table-layout policy, and looks like a file diff when it is not one. |
| B. Plain-language categorized summary only | "Packages: adds ripgrep", driven by a per-key vocabulary. | **Rejected as sole view.** A paraphrase can misstate what changed, the vocabulary drifts as the schema grows, and unknown keys need a fallback anyway. The Interpreted block in C keeps the useful part (an area label and an effect phrase) but bounds it to one label per top-level key and a generic fallback (D12). |
| C. Exact and interpreted structured views | Complete dotted-path details plus a structured interpretation, initially shown together; raw JSON comparison remains available read-only on request. | **Ruled.** Both views appear initially; the owner may later prune one. |
| D. Keep JSON, add a caption | Explain that JSON is the normalized form. | **Rejected.** It is the cheapest option but fixes none of gaps 2–5. |
| E. Per-file TOML text diff | Diff each source file. | **Rejected.** Comment and order edits would appear although they change nothing, resolved-destination changes would not appear, and no previous file text is stored. |

## 5. Initial two-view output

The initial prompt shows both views ([OQ-CAP1](#OQ-CAP1)). The Exact block is what is approved; the Interpreted block groups and labels those same numbered changes. Both are labeled blocks. The rules are in [§6](#6-rules-behind-the-mocks); this section is examples.

In the examples, `install`, `mcp_servers` and `workspace_readonly` are real top-level keys; field names and value shapes under them are illustrative. `example.*` is fictional and shows the unlabeled fallback. Header, question and existing-block text follow the current strings ([§1](#1-current-behavior-verified-baseline)); existing mount, scope and grant blocks are abbreviated in square brackets.

### 5.1 Anatomy and order

A scalar edit, shown as the whole prompt:

```text
⚠  Workspace config changed since last run:

Workspace config: 1 change in 1 area
  (merged project/local files and their includes; user config is not
  part of this approval)

Interpreted  (grouping and labels only; the Exact block is what you approve)
  workspace_readonly: Read-only workspace
    [1] changes from false to true

Exact
  [1] changed   workspace_readonly
        was  false
        now  true

Raw JSON comparison (read-only, records nothing): yolo config approval-diff
Accept these workspace config changes? [y/N]
```

| Part | Source | Rule |
| :--- | :--- | :--- |
| Header line, question | Existing | Unchanged text and logic. |
| Mount, scope, grant blocks | Existing | Unchanged, printed before any config view (D6). |
| Count line | New | `Workspace config: <n> change(s) in <m> area(s)`, then the fixed coverage note. `n` is the number of groups. |
| Interpreted | New | One header per area, one row per group ([§6.7](#67-interpreted-view)). |
| Exact | New | One entry per group, sorted by path ([§6.1](#61-values-toml-cannot-spell)–[§6.3](#63-removals)). |
| Footer | New | One line naming the raw-comparison command ([§6.8](#68-raw-comparison-on-request)). |

### 5.2 Removal, and removal that reveals a lower workspace layer

```text
Workspace config: 2 changes in 1 area
  (merged project/local files and their includes; user config is not
  part of this approval)

Interpreted  (grouping and labels only; the Exact block is what you approve)
  example  (no description for this key)
    [1] retries: changes from 5 to 2
    [2] timeout: stops setting it (was 30); user config or the built-in
        default applies and is not part of this approval

Exact
  [1] changed   example.retries
        was  5
        now  2
  [2] removed   example.timeout
        was  30
        now  (not set in workspace config; your user config or the
              built-in default applies and is not part of this approval)
```

Group `[1]` is what dropping a local-file override looks like: a lower workspace layer still sets the key. The merged value changed, so both views say `changed`, which is true. Neither view claims the value was inherited, because the stored record holds no provenance. Group `[2]` does not guess the fallback value (D7).

### 5.3 Lists: append, duplicates and pure reorder

```text
Workspace config: 3 changes in 2 areas
  (merged project/local files and their includes; user config is not
  part of this approval)

Interpreted  (grouping and labels only; the Exact block is what you approve)
  example  (no description for this key)
    [1] flags: removes 1 item: "-v"
    [2] search_path: reorders the same 3 items; order is part of the approval
  install: Nix packages and mise tools
    [3] nix: adds 1 item: "ripgrep"

Exact
  [1] changed   example.flags   (1 item removed)
        removed  "-v"   (previous item 2 of 3)
  [2] reordered example.search_path   (same 3 items; all positions shown)
        was  ["a", "b", "c"]
        now  ["c", "a", "b"]
  [3] changed   install.nix   (1 item added)
        added    "ripgrep"   (current item 2 of 2)
```

In `[1]` the previous list was `["-v", "-v", "-q"]`. The two equal items stay separate occurrences, and the Exact entry names which position went. Indexes are one-based and refer to the full previous or current list as labeled. A permutation is explicitly `reordered`, never treated as no change.

### 5.4 Tables: named entries, arrays of tables, and a type change

```text
Workspace config: 4 changes in 4 areas
  (merged project/local files and their includes; user config is not
  part of this approval)

Interpreted  (grouping and labels only; the Exact block is what you approve)
  example.limit  (no description for this key)
    [1] replaces this table (2 values) with 15
  example  (no description for this key)
    [2] rules: removes item 3 of 3 (table; 2 values)
  mcp_servers.alpha: MCP servers
    [3] adds this table (2 values)
  mcp_servers.beta: MCP servers
    [4] args: removes 1 item: "--verbose"

Exact
  [1] changed   example.limit   (table replaced by a value)
        was  example.limit.hard  = 20
             example.limit.soft  = 10
        now  15
  [2] removed   example.rules[3]   (previous item 3 of 3; table; 2 terminal values)
        example.rules[3].action  = "skip"
        example.rules[3].name    = "tmp"
  [3] added     mcp_servers.alpha   (table; 2 terminal values)
        mcp_servers.alpha.args     = ["--stdio"]
        mcp_servers.alpha.command  = "alpha-server"
  [4] changed   mcp_servers.beta.args   (1 item removed)
        removed  "--verbose"   (previous item 2 of 2)
```

Added and removed tables expand every terminal value under its full path in sorted key order. The Interpreted row gives only the count, so table contents are not printed twice. For arrays of tables there is no guessed identity key: a changed element is a remove plus an add at its old and new one-based positions.

### 5.5 Unlabeled key and a long value

```text
Interpreted  (grouping and labels only; the Exact block is what you approve)
  example  (no description for this key)
    [1] banner: changes value (see [1] below)

Exact
  [1] changed   example.banner
        was  "short"
        now  "a string long enough that repeating it in the interpreted
              row would only add a second copy of the same text ..."
```

An unlabeled key is still fully reported. The fallback row uses the key itself as the area and the same effect phrases. A value over the inline limit is printed once, in full, in the Exact block. The `...` here abbreviates the mock; real output never elides (D5).

### 5.6 Security scope together with a config change

```text
⚠  Workspace config and repository scope changed since last run:

[... existing repository-scope block: labeled header, marked repository
 rows with sources, grant rows; unchanged by this design ...]

Workspace config: 1 change in 1 area
  (merged project/local files and their includes; user config is not
  part of this approval)

Interpreted  (grouping and labels only; the Exact block is what you approve)
  install: Nix packages and mise tools
    [1] nix: adds 1 item: "ripgrep"

Exact
  [1] changed   install.nix   (1 item added)
        added    "ripgrep"   (current item 2 of 2)

[... existing scope counts line ...]
Raw JSON comparison (read-only, records nothing): yolo config approval-diff
Accept these workspace config and repository scope changes? [y/N]
```

The scope block is not restated, numbered or summarized in either config view. Repository scope and grants have their own records and are outside the config count.

### 5.7 Scope-only change

```text
⚠  Repository scope changed since last run:

[... existing repository-scope block ...]

Workspace config: no change.

[... existing scope counts line ...]
Accept these repository scope changes? [y/N]
```

No config views and no raw-comparison footer are printed, because the config identity is equal.

### 5.8 Resolved mount change with no file edit

```text
⚠  Workspace config changed since last run:

[... existing writable-mount block, with its existing textual marker ...]

Workspace config: no config-key paths changed
  (the resolved mount change is shown in full above)

Raw JSON comparison (read-only, records nothing): yolo config approval-diff
Accept these workspace config changes? [y/N]
```

The approval identity includes a synthetic record of resolved mount sources. A symlink retarget can change that record without a config text edit. The existing marked mount block fully discloses it; it is not duplicated as a fabricated key path, not counted, and not given an Interpreted row.

### 5.9 Accepted report on `--accept-config-changes`

```text
⚠  Workspace config changed since last run; --accept-config-changes records it as approved:

Workspace config: 1 change in 1 area
  (merged project/local files and their includes; user config is not
  part of this approval)

Interpreted  (grouping and labels only; the Exact block is what was approved)
  install: Nix packages and mise tools
    [1] nix: adds 1 item: "ripgrep"

Exact
  [1] changed   install.nix   (1 item added)
        added    "ripgrep"   (current item 2 of 2)
```

No question and no raw-comparison footer: once the record is written, that command would report no difference. This report is the only place the accepted change is shown ([§6.4](#64-surfaces)).

### 5.10 Narrow terminal, `NO_COLOR`

The same change as [§5.2](#52-removal-and-removal-that-reveals-a-lower-workspace-layer) group `[2]` at 40 columns without color:

```text
Interpreted  (grouping and labels only;
the Exact block is what you approve)
  example  (no description for this
  key)
    [2] timeout: stops setting it (was
        30); user config or the
        built-in default applies and
        is not part of this approval

Exact
  [2] removed   example.timeout
        was  30
        now  (not set in workspace
              config; your user config
              or the built-in default
              applies and is not part
              of this approval)
```

Every word, number and value is still present. Nothing depends on a column lining up with another row.

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
| Control or non-ASCII character in a key or value | `\uXXXX` escape inside a basic string or quoted key segment | Same escaping as the canonical identity (D15). A value cannot move the cursor, change direction or imitate presenter text. |
| Table replaced by a value, or the reverse | One `changed` group; the table side expands to its terminal values | The group heading says which shape replaced which. |

Anything in parentheses is presenter vocabulary, not TOML. Dotted paths use bare key segments only when TOML permits them, otherwise quoted/escaped segments; list indexes are explicit one-based tokens. The complete display is TOML-shaped, not pasteable source.

### 6.2 Comments and order

Comment, whitespace and key-order edits do not reach the tree. They do not change the identity and never appear. Object keys in the presentation sort by their escaped path, independent of file order. List order and duplicate occurrences are part of identity and are preserved.

### 6.3 Removals

A removed path always prints its old value in full; the `now` line states only that the workspace no longer sets it and does not guess a lower workspace value, user value or default. An added or removed table is one path group expanded to all terminal child paths in sorted order; the table heading states the number of terminal values. Empty tables/lists are explicit terminal values and count as one. This rule is symmetric for additions and removals.

### 6.4 Surfaces

| Surface | Config views | Raw-comparison footer | Behavior otherwise |
| :--- | :--- | :--- | :--- |
| Interactive prompt (stderr) | Count line, Interpreted, Exact, after the existing blocks | Yes, directly above the question | Single y/N; input grammar unchanged. To read the raw comparison, answer N (the default, which records nothing) or use a second terminal. |
| Non-interactive refusal | Identical | Yes, after the views and before the existing advice | Existing headline and advice text unchanged. Nothing is recorded, as today. |
| `yolo config apply` and attach | Identical | Yes | Same presenter; these already share the gate. |
| `--accept-config-changes` accepted report | Identical, with "was approved" in the Interpreted header | No | See below. |
| `yolo config approval-diff` | None; raw JSON only | Not applicable | [§6.8](#68-raw-comparison-on-request). |

**Accepted report ([OQ-CAP3](#OQ-CAP3)).** Today the accepted reporter runs only when scope, grants or mounts changed. It now also runs when the config identity changed. Everything else is as today:

- It prints before the record is written, on stderr, without a prompt.
- The existing header wording and the existing order (mounts, scope block and counts, grant block and counts) are kept. The config views are appended after them.
- The record written is the same canonical identity as answering `y`.
- A first launch with an empty workspace config is still recorded silently. That is the existing rule for an empty first config, with or without the flag.
- A first launch with a non-empty config and the flag reports every path as `added`.
- A mount-only change prints the existing mount block and the "no config-key paths changed" line of [§5.8](#58-resolved-mount-change-with-no-file-edit).

The earlier decision to stay silent on expected configuration does not apply here: this report appears only when something changed and was accepted unattended, which is the case the owner asked to see.

### 6.5 Secrets

The Exact block prints the values the JSON diff prints today, in dotted notation. The Interpreted block repeats only short scalar values and short list items from those same groups ([§6.7](#67-interpreted-view)); it never prints a value the Exact block does not. Neither adds redaction, because a redacted value is one the person approved without seeing. Neither adds user-config or default values, because that would newly expose data outside the approval. Adding redaction later would be an owner decision about disclosure, not a presentation tweak.

Three surfaces are new, and none widens what is shown:

| Surface | What it prints | Why it is not a new exposure |
| :--- | :--- | :--- |
| Accepted report | The changed values of an accepted config-only change, on stderr | The same values the prompt would have shown. An unattended run’s stderr may be captured in a log; that is the cost of the ruling and is the same class of output as the existing accepted scope report. |
| `yolo config approval-diff` | The two identity strings’ differences, on stdout | Both sides are the workspace approval input: one already on disk in the host-side record, one computed from workspace config. It reads no user config and no defaults. |
| Interpreted block | Labels and effect phrases | Labels are fixed text; values come only from the group being described. |

Nothing in this design writes a config value anywhere new. No log, cache, temp file or record is added.

### 6.6 Width, color, accessibility and bounds

- **Grouping.** A changed scalar is one group. An added or removed table is one group ([§6.3](#63-removals)). Two tables at the same path are compared key by key and never form a group themselves. A list with no table element on either side is one group with item lines. A list with a table element on either side yields one group per unmatched element, at `path[n]`, unless it is a pure permutation.
- **Alignment.** A list that is a pure permutation with the same multiplicities is one `reordered` group showing both complete orders. Otherwise align exact items with a longest-common-subsequence walk; ties select the earliest previous index, then earliest current index. Each unmatched occurrence is printed as an add/remove with its original one-based index. Duplicates are never set-deduplicated; edits to array-table items are remove-plus-add, never guessed as identity-preserving edits.
- **Counts and completeness.** The count line gives the number of groups and the number of areas. Group numbers run `[1]` to `[n]` with no gaps, and each appears exactly once in each block. Each added/removed table group separately states its terminal-value count; empty containers count as one terminal value. Every added/removed occurrence is printed, including duplicate occurrences; no cap, `… and N more`, pager, or silent omission. A 200-path first launch reports all 200, in both blocks.
- **Width.** On a terminal, lines wrap at the terminal width, or at 40 columns if the terminal is narrower or its width is unknown. Wrapping may break long values only at valid escape/character boundaries; continuation indentation preserves which row, or which `was`/`now` value, a line belongs to. Nothing depends on column alignment, and there are no table borders. Non-TTY output is not hard-wrapped.
- **Character set.** All presenter text (titles, verbs, labels, effect phrases, brackets, the footer) is ASCII. Keys and values are escaped to ASCII (D15). The existing header and mount markers keep their current characters; this design does not restyle them.
- **Color.** Color follows the existing contract in [`cli-color.md`](../reference/cli-color.md), including `NO_COLOR`. It only reinforces words that are already there: block titles bold, additions green, removals red, paths dim. The Interpreted block uses no color that could read as severity (D13). Existing writable-mount markers remain text.
- **Accessibility.** Verb words lead each Exact entry; `was` and `now` are literal labels; block titles are words. No meaning is carried only by glyph, position or color.

### 6.7 Interpreted view

The Interpreted block is derived from the group list by three bounded rules. It has no access to anything the Exact block lacks.

**Area.** The area of a group is its top-level key. If the path has a second segment and the value at those first two segments is a table on either side, the area is those two segments instead: `mcp_servers.alpha`, `machine.resources`. This is decided from the two trees, not from a list of known keys. Areas are printed in order of their lowest group number.

**Label.** The area header is `<area>: <label>`, where the label comes from one flat table keyed by top-level key. The label is a noun phrase naming what the key is about. A key with no row prints `<area>  (no description for this key)`.

| Top-level key | Label |
| :--- | :--- |
| `agents_md_extra` | Extra briefing text |
| `briefing_provenance` | Briefing origin labels |
| `devices` | Device access requests |
| `env` | Environment variables |
| `ephemeral_storage` | Temporary storage |
| `host_files` | Files provisioned into the environment |
| `include_if_found` | Optional included config files |
| `install` | Nix packages and mise tools |
| `kvm` | KVM device access |
| `lsp_servers` | Language servers |
| `machine` | GPU, resources, ports and forwarding |
| `mcp_servers` | MCP servers |
| `model_facts` | Model metadata |
| `mounts` | Paths outside the workspace |
| `network` | Network mode |
| `per_side_paths` | Per-side workspace subpaths |
| `providers` | Provider routes |
| `provisioners` | Program installation order |
| `prune` | Disk-usage warning |
| `repos` | Named repositories |
| `requests` | Permission requests |
| `required_capabilities` | Required capabilities |
| `security` | Blocked-tool shims |
| `workspace_readonly` | Read-only workspace |
| `writable_home_dirs` | Writable home directories |

These are the top-level keys of the current readable vocabulary that a workspace file may set ([§10.1](#101-checks-added-2026-11-16)). The table is new presentation metadata: the schema carries no descriptions today. Three rules keep it small and honest:

- A label describes the key, never the change and never its consequence.
- Legacy spellings that can still appear in an approval tree (`packages`, `mise_tools`, `gpu`, `resources`, `env_sources`) take the label of their replacement. Anything else falls back.
- A test lists every workspace-settable top-level key and fails when one has neither a label nor a deliberate fallback entry. Adding a config key therefore cannot silently produce a wrong label, only a missing one.

**Effect.** Each row is `[n] <leaf>: <phrase>`, where `<leaf>` is the group’s path relative to the area (omitted when the group is the area itself). The phrase is chosen by the Exact verb and the value shapes:

| Exact group | Effect phrase |
| :--- | :--- |
| `added` scalar | `sets to <value>` |
| `added` table or empty container | `adds this table (<k> values)` / `sets to an empty list` / `sets to an empty table` |
| `removed` scalar | `stops setting it (was <value>); user config or the built-in default applies and is not part of this approval` |
| `removed` table | `removes this table (<k> values)` |
| `changed` scalar | `changes from <old> to <new>` |
| `changed` with a shape change | `replaces this <shape> (<k> values) with <value>`, or the reverse |
| `changed` list | `adds <a> item(s): <items>` and/or `removes <r> item(s): <items>` |
| `reordered` list | `reorders the same <k> items; order is part of the approval` |
| Array-of-tables element | `adds item <i> of <k> (table; <v> values)` / `removes item <i> of <k> (table; <v> values)` |

A value or item is repeated inline only when its rendered literal is at most 40 characters, and at most five items are listed; otherwise the phrase ends `(see [n] below)`. This is a pointer, not an omission: the Exact entry `[n]` prints everything.

**What the block never says.** It gives no risk, severity, safety or recommendation wording, and no claim about where a value came from or what the runtime will do with it (D13). It makes no statement about mounts, repository scope or grants beyond one fixed pointer: when the area is `mounts` and a mount block is printed, the area header is followed by `(resolved host paths: see the mount block above)`. The block header always states that the Exact block is what is approved.

**Pruning later.** Because rows and entries share numbers, the owner can compare the two views change by change on real prompts. Removing either block later is a deletion of one formatter and its title; the group list, counts, D8 replay and footer do not depend on which blocks are printed.

### 6.8 Raw comparison on request

`yolo config approval-diff` prints the raw JSON comparison ([OQ-CAP2](#OQ-CAP2)). It takes no flags other than help.

| Aspect | Decision |
| :--- | :--- |
| What it compares | The stored config approval record for this workspace against the current approval input. These are the same two strings the gate compares, obtained through the gate’s own read path, with the same normalization of the stored text. |
| What it does not compare | Repository scope and loophole grants. They have separate records; the output says so in one line. It reads no user config. |
| Output | Stdout: a three-line header (record path; "current: computed now, not saved"; the not-compared note), then the existing unified JSON diff with three context lines. Errors go to stderr. |
| Where it runs | On the host, where the records live. Inside a jail it refuses the same way `yolo config apply` does. |
| Effects | None. It records no approval, creates no directory, writes no scope file, cache or temp file, never prompts and uses no network. It must not call the gate’s approve step, which can write even when nothing changed. |

| Situation | Result | Exit |
| :--- | :--- | :--- |
| Record and current input are equal | Header, then `No difference: no config approval is pending.` | 0 |
| They differ | Header, then the diff | 0 |
| No record for this workspace | Header, then `No approval is recorded for this workspace yet.`, then the current approval input in full as canonical JSON | 0 |
| Stored record is not valid JSON | Header, then `The stored record is not valid JSON; comparing it as text.`, then the diff. The record is not repaired or rewritten. | 0 |
| Stored record cannot be read (for example, permissions) | Error naming the record path; no comparison | 1 |
| Workspace config is invalid, or a mount cannot be resolved | The same validation error the gate reports; no comparison | 1 |
| Any argument or flag, including `--accept-config-changes` | Usage error | 2 |

The exit code does not signal "differs", because this is an inspection command, not a check; `yolo check` and the gate remain the checks.

The D8 fallback prints this same diff inline, preceded by `The change list could not account for every difference; showing the raw comparison instead.`

## 7. Non-goals

- Changing the approval identity, the record format or location, or what triggers a prompt.
- Changing the mount, scope or grant blocks, their order, or their non-suppressibility.
- The host-configuration-apply gate ([`host-apply-staleness.md`](../reference/host-apply-staleness.md)). Its change list and privacy ruling are a different gate with different data.
- Per-key source-file attribution. The stored record holds no provenance for the previous side.
- A general renderer or theme framework, or an implementation plan.
- A per-setting vocabulary. Labels stop at the top-level key ([§6.7](#67-interpreted-view)).
- Any risk or severity classification of config changes.
- A key inside the prompt for showing raw details, or any other change to the y/N grammar.
- A preview command for the two-view report, or a raw comparison of scope or grant records. `yolo config approval-diff` covers the config identity only.
- Redaction of approval values on any surface ([§6.5](#65-secrets)).

## 8. Pure presentation versus effectful approval

| Concern | Kind | Changed here? |
| :--- | :--- | :--- |
| Read, merge, project, serialize, compare | Decides whether to ask | No |
| Write record on `y` or on the flag | Effect | No |
| Turn two trees into text | Pure | **Yes: the two views** |
| Call the accepted reporter | Existing call, before the record is written | **Condition widened** to include a config identity change; what is recorded is unchanged |
| Read the record and current input, print their diff | Read-only | **New verb**, outside the gate; it never approves or records |

**Invariant.** The gate still decides whether to ask by comparing the same approval identities. When the config identity changes, the config presenter lists all changed config-key paths in both blocks and must print at least one line. On a scope-only gate it says `Workspace config: no change.` The existing mount block counts as complete presentation of resolved-mount-record changes. If replaying the group list does not reproduce the current identity and no printed mount block explains the remainder, D8 prints a notice and the raw JSON comparison rather than an empty list, a partial list or a bare question.

## 9. Costs, risks, migration and observability

| Item | Assessment |
| :--- | :--- |
| Build cost | One structural diff, one TOML-literal formatter, two block formatters, one label table with its coverage test, one read-only verb, a widened accepted-report condition, and a rewrite of the diff-text assertions in the prompt tests. |
| Risk: a label misdescribes a key | Bounded: labels name the key’s subject only, stop at the top level, and the Exact path is printed beside every row. A missing label degrades to the key name. |
| Risk: the Interpreted block is read as the tool’s assessment | Mitigated by its fixed header line and by D13. The two blocks carry the same numbers, so a reader can always check a row against its entry. |
| Risk: two blocks double the output | Accepted for the initial period by ruling. Table contents and long values print once; the Interpreted block is one line per change. |
| Risk: accepted changes appear in unattended logs | Accepted by ruling ([§6.5](#65-secrets)). |
| Risk: the presenter drops a path | Mitigated by D8 and by a property worth testing: applying the printed changes to the previous tree yields the current tree. |
| Risk: list alignment misleads | Element-equality alignment can show an edit as remove plus add. That is verbose but never wrong. |
| Risk: long first-launch output | Accepted. It is the truthful size, and the header count warns the reader. |
| Risk: readers assume the output is pasteable TOML | Mitigated by the parenthesized vocabulary and the provenance line. |
| Migration | None for records. Legacy JSONC workspaces get the same list, since notation follows the tree and not the file format. `config-safety.md` needs re-verification regardless of this design. |
| Stderr text consumers | None are known. The packet identified no machine consumer of the diff text, and none is assumed here. |
| Observability | `yolo config approval-diff` prints the raw JSON comparison of the current approval input with the stored identity, read-only ([§6.8](#68-raw-comparison-on-request)). The prompt and the refusal name it in a one-line footer. |

## 10. Verified checks and remaining uncertainty

The evidence packet supplied the first-pass claims. The following bounded source check resolves A1–A4 against the current tree; it does not claim a universal search for historical owner decisions.

| ID | Check | Finding |
| :--- | :--- | :--- |
| A1 | Can the gate name contributing files in the header? | The loader records a per-value source tree with file origins and writer order ([`sources.go`](../../internal/config/sources.go)). The current gate result keeps that tree private, and the saved approval snapshot contains only JSON values ([`gateread.go`](../../internal/config/gateread.go), [`snapshot.go`](../../internal/config/snapshot.go)). Therefore exact source-file lists are not assumed; the mock uses a generic merged-workspace line, and per-key attribution is a non-goal. |
| A2 | Can an approval value be JSON `null`? | The JSON value model and snapshot encoder accept `nil` and emit `null`; legacy JSONC input can represent it, and `SchemaTypeAny` fields preserve unconstrained values ([`jsonx.go`](../../internal/jsonx/jsonx.go), [`readablevalidate.go`](../../internal/config/readablevalidate.go), [`schema.go`](../../internal/config/schema.go)). The presenter renders explicit `(null)` distinct from absent; it does not claim every schema path permits null. |
| A3 | Does list append create JSON comma churn? | Yes, mechanically: the snapshot encoder writes the array separator after the previous item and before the next line ([`jsonx.go`](../../internal/jsonx/jsonx.go)); the unified diff compares those lines ([`difflib.go`](../../internal/config/difflib.go)). Appending `"ripgrep"` after `"jq"` changes the previous line from `"jq"` to `"jq",` and adds the new item line. No approval flow was run. |
| A4 | Is config-only accepted reporting already ruled? | The inspected [`WW-D20`](workspace-widening.md#WW-D20) covers accepted repository-scope blocks/counts, and [`RC-D24`](../reference/config/rulings.md#RC-D24) covers writable-mount disclosure. Current code calls `ReportAccepted` only for scope, grants or mount-source changes, so config-only acceptance is silent ([`snapshot.go`](../../internal/config/snapshot.go), [`preflight.go`](../../internal/cli/run/preflight.go)). The owner has now ruled config-only accepted-change reporting with `--accept-config-changes`; this update records that direction without changing code or live config. |

No approval record or real workspace config was opened. A1–A3 remain bounded verified design inputs; A4’s earlier behavior is superseded by the owner ruling recorded in the ledger below.

### 10.1 Checks added 2026-11-16

Read from the tree at `c3ac9c649` by source inspection; no command was run.

| ID | Check | Finding |
| :--- | :--- | :--- |
| A5 | Does an existing command already print or compare the stored approval record? | No. `yolo config` has `check`, `set`, `add`, `unset`, `secret`, `unsecret`, `why`, `migrate`, `dump`, `drift` and `apply`. `dump` prints current config, `drift` compares against the jail’s boot baseline, `why` explains one key. `diff` is intercepted only to redirect to `yolo surface diff`. ([`config.go`](../../internal/cli/config.go) `configRunW`) |
| A6 | What can a read-only command reuse? | `ReadApprovalRecord` returns the stored text, reports absence without error, and does not validate JSON. `ApprovalRecordPart` computes the current projection. The gate’s approve step can write even on no-change paths, so an inspection command must not go through it. ([`migraterecord.go`](../../internal/config/migraterecord.go), [`snapshot.go`](../../internal/config/snapshot.go) `approveConfigAndScope`) |
| A7 | What layout do existing disclosures use? | The gate’s scope block is a labeled header, marked rows (`+`, `-`, `~`, blank) with a status word, and a separate counts line. The prompt header, question, accepted-report header and refusal advice are fixed strings quoted in [§1](#1-current-behavior-verified-baseline). The every-launch broker line is a different, compact layout and is not the model here. ([`scopeapproval.go`](../../internal/config/scopeapproval.go), [`preflight.go`](../../internal/cli/run/preflight.go)) |
| A8 | Is there an existing grouping or description per config key? | No. `SchemaKey` has `Path`, `Scope`, `Type` and `Removal` only. Descriptions live in reference prose. The readable vocabulary has 50 top-level keys, 25 of them user-only. ([`schema.go`](../../internal/config/schema.go), [`workspacescope.go`](../../internal/config/workspacescope.go)) |

### 10.2 Unverified assumptions

These do not block the design, because each has a stated safe behavior, but the plan must check them.

| ID | Assumption | If it is wrong |
| :--- | :--- | :--- |
| U1 | The synthetic resolved-mount record sits under a distinguishable key in the approval tree, so the D8 replay can set it aside when a mount block is printed. | The replay treats the difference as unexplained and falls back to the raw diff, which is safe but noisy; the plan then adds the distinguishing rule. |
| U2 | Approval trees from legacy JSONC workspaces may use either legacy or readable top-level names. | No effect on correctness. Unlabeled names use the fallback row. |
| U3 | `requests` and `repos` values may be projected out of the config part, like legacy `brokered`. | Their label rows are simply never used. |
| U4 | The label wording matches the project’s reference prose. | Wording is adjusted at plan time against the CLI reference; the rules in [§6.7](#67-interpreted-view) do not change. |
| U5 | The approval identity holds no private environment values beyond what today’s prompt diff already prints. | Out of scope either way: this design prints nothing the existing diff does not ([§6.5](#65-secrets)). |
| U6 | `yolo config apply` has a reusable host-only refusal. | `approval-diff` states its own one-line refusal inside a jail. |

## 11. Decision ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| CAP-R1 | The TOML-era approval prompt showing JSON diffs is an issue to explore; this authoring request did not select a format or authorize implementation. | 2026-11-16 | [§1](#1-current-behavior-verified-baseline) | No |
| <a id="OQ-CAP1"></a>[OQ-CAP1](#OQ-CAP1) | Initially show both the exact dotted-notation view and structured interpreted explanations, using tables or labeled blocks rather than prose paragraphs; the owner may later prune one. | 2026-11-16 | [§5](#5-initial-two-view-output), [§6.7](#67-interpreted-view) | No |
| <a id="OQ-CAP2"></a>[OQ-CAP2](#OQ-CAP2) | Retain a raw JSON comparison of current approval input with the stored identity, read-only and available on request. | 2026-11-16 | [§6.8](#68-raw-comparison-on-request) | No |
| <a id="OQ-CAP3"></a>[OQ-CAP3](#OQ-CAP3) | Report config-only accepted changes on the `--accept-config-changes` path; preserve the existing approval identity and no-prompt behavior. | 2026-11-16 | [§6.4](#64-surfaces), [§5.9](#59-accepted-report-on---accept-config-changes) | No |

The three rows above are owner rulings. Everything the designer chose in order to carry them out is design judgement, recorded as D1–D15 in [§4.1](#41-decisions-design-judgement): the block layout and order, group numbering, the label table and effect phrases, the command name `yolo config approval-diff` and its cases, and the accepted-report details. None of these is an owner ruling, and the owner may change any of them without reopening [OQ-CAP1](#OQ-CAP1)–[OQ-CAP3](#OQ-CAP3).

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
11. `--accept-config-changes` records config-only acceptance and reports the accepted changes in the two-view format without prompting; the record written is byte-identical to the one `y` writes.
12. For any change, group numbers run `[1]` to `[n]` without gaps, and each number appears exactly once in the Interpreted block and once in the Exact block; the count line’s `n` agrees.
13. A change under a top-level key with no label prints the fallback area header and the same effect phrases; nothing is omitted and no label is invented.
14. The Interpreted block contains no value absent from the Exact block, and no risk, severity or safety wording, for any input.
15. A list that loses one of two equal items reports one removed occurrence with its previous position; it is not reported as unchanged or deduplicated.
16. A table replaced by a scalar is one `changed` group whose `was` side lists every terminal value.
17. At 40 columns with `NO_COLOR`, the output has the same words, numbers and values as at 120 columns with color; presenter text and printed values are ASCII.
18. A value containing a control or bidirectional character prints as `\uXXXX` escapes in both blocks.
19. `yolo config approval-diff` prints the documented result for each of: equal, different, no record, undecodable record, unreadable record, invalid workspace config; in every case the approvals directory and every record are unchanged afterwards.
20. The prompt and the non-interactive refusal print the raw-comparison footer when the config identity changed; the scope-only prompt and the accepted report do not.
21. Replaying the printed groups onto the previous tree reproduces the current identity; when it does not, both views are replaced by the D8 notice and the raw diff.
