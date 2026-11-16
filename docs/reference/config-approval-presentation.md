---
title: Workspace config approval presentation
status: accepted
stage: CURRENT
verified: 2026-11-16
verified_commit: 6b8879611
covers:
  - internal/config/approvaldiff.go
  - internal/config/approvalcomparison.go
  - internal/config/snapshot.go
  - internal/cli/run/approvalpresentation.go
  - internal/cli/run/approvaldiff.go
  - internal/cli/run/preflight.go
  - internal/cli/config.go
summary: Complete structured explanations of the workspace approval identity, with read-only raw inspection.
---

# Workspace config approval presentation

**Status:** Verified 2026-11-16 against `6b8879611`. MEASURED: focused config,
CLI-dispatch and approval-presentation callers exercised the combined implementation.

Yolo describes a workspace's coding environment and renders it where the agent runs;
the approval gate protects the host when agent-editable workspace settings change.
The presentation explains that gate's input without changing its identity or decision.
[Config safety](config-safety.md) owns approval, records and the terminal predicate.
This page owns the explanation and the inspection command, not another approval gate.

## Identity and explanation

The **approval identity** is the canonical JSON string compared with the host-side
record. It contains merged project/local config and their includes, with `brokered`
projected out and resolved mount sources included. It excludes user config, runtime
defaults and expanded environment contents. Repository scope and loophole grants have
separate identities and disclosures.

A **change group** is one numbered structural change in the previous/current config
value trees. It is not a source-file hunk: formatting, comments and map-key order do
not change the identity. List order and repeated occurrences do.
`ApprovalChangeSet` in `internal/config` carries the groups and their replay result.

Two blocks share this same group list:

- **Interpreted:** areas, noun-phrase labels and fixed effect phrases. It gives no
  risk, safety, severity, recommendation, provenance or effective-runtime judgement.
- **Exact:** changed dotted paths, verb words and the full values needed to explain
  each change. This is the authoritative detail of what is approved.

Each group number occurs once in each block. The count line reports groups and areas;
expanded tables separately report their terminal-value count. Unknown roots retain
complete Exact output and a generic Interpreted label rather than a guessed meaning.
`approvalAreaLabels` names roots only; a permanent schema-root test requires an
accurate label or deliberate fallback when the readable vocabulary grows.

## Structural changes

`BuildApprovalChangeSet` walks the trees in deterministic path order:

| Input shape | Explanation |
| :--- | :--- |
| Scalar addition, removal or replacement | One group, with the necessary old/new literals |
| Two tables at the same path | Compare their children, not one opaque table replacement |
| Added or removed table | One group expanded to every terminal path and value |
| Table replaced by another value, or the reverse | One changed group; retain the complete non-table side and expanded table side |
| Empty table/list | Explicit terminal value, never confused with absence |
| Scalar list edit | One group with unmatched item occurrences and original positions |
| List containing tables | One group per unmatched element; no guessed identity key |
| Pure permutation with equal multiplicities | One reordered group showing both complete orders |

List matching uses exact equality and longest-common-subsequence alignment; ties
prefer earlier previous positions, then earlier current positions. Duplicate values
remain separate occurrences. A changed array-table element is removal plus addition,
not an inferred in-place edit. Mixed arrays label each occurrence by its own shape.

A removed key prints its complete previous value and says that it is no longer set
in workspace config. It never invents the user value or default that might apply.
Dropping an override that reveals a lower workspace-layer value is an ordinary
changed value: the saved identity has no previous-side provenance.

## Replay and fallback

The builder replays its groups onto the previous tree and compares the canonical
result with the current tree. Resolved mount sources are explained by the existing
mount block, not by a fabricated config path; that remainder is allowed only when
its disclosure is actually printed.

The presenter drops **both** structured views when replay cannot explain the identity.
It prints a notice and the raw unified JSON comparison instead. An undecodable record,
a noncanonical present identity or an identity-only difference must not become an
empty explanation followed by a bare approval question. Record presence matters:
a missing empty first-run record is not an existing zero-byte record.

The identity encoder and stored bytes remain unchanged. Presentation neither
re-serializes the approval record nor decides whether the gate should ask.

## Disclosure surfaces

The existing header and sensitive disclosures lead the explanation: mount, repository
scope and grant blocks retain their content, markers and relative ordering. The config
count, Interpreted and Exact blocks follow. The interactive path then prints existing
counts, the raw-comparison footer and the single default-no question.

| Surface | Behavior |
| :--- | :--- |
| Interactive launch, attach or `yolo config apply` | Same complete views and raw-comparison footer; unchanged y/N grammar |
| Noninteractive refusal | Same explanation and footer, then actionable existing advice; records nothing |
| `--accept-config-changes` | Reports changed config even when no scope/grant/mount changed; says “was approved”; no question or footer |
| Scope-only change | Existing scope block and “Workspace config: no change.”; no config views/footer |
| Resolved-mount-only change | Existing mount disclosure and no config-key paths changed; no synthetic path group |
| Empty first config | Existing silent record behavior |

The accepted report is emitted **before** writing the same identity that answering yes
would write. Its changed values can therefore appear in unattended stderr logs. There
is no new redaction, log, record, cache or secret-reading path. Only changed workspace
values are exposed; unchanged siblings are not added merely as context.

## Display notation

Dotted paths use individually quoted segments where needed and one-based bracketed
list positions. Literals are TOML-shaped, **not a pasteable TOML document**. Explicit
JSON null is `(null)`; absence has a separate not-set explanation.

Display-only string escaping is ASCII and uses TOML basic-string escapes: named
control escapes, Unicode scalar escapes for other controls/non-ASCII, and no surrogate
code points or JSON surrogate pairs. The canonical JSON encoder is independent.

Terminal output wraps without elision or splitting escape tokens; nonterminal output
is not hard-wrapped. No pager, truncation or “and more” cap hides a changed value.
Verb words, `was`/`now` and group numbers retain meaning without color or alignment.
Color follows [the CLI color contract](cli-color.md), including `NO_COLOR`.

## Raw inspection

Run `yolo config approval-diff` on the host in the workspace. It takes only help,
prints to stdout, and compares the stored config identity with the current strict
workspace approval input. It does not compare scope or grant records.

| Situation | Output / status |
| :--- | :--- |
| Equal | Header and no-pending-difference message; success |
| Different | Header and unified JSON diff; success |
| No record | Header, no-record message and complete current canonical input; success |
| Invalid stored JSON | Header, text-comparison notice and diff; record untouched; success |
| Unreadable record or invalid workspace input/mount | Error on stderr; failure |
| Unexpected argument/flag | Usage error |

The header names the record, says the current input is computed but not saved, and
names the excluded scope/grant parts. Exit success does not mean “equal”.
The jail's own-workspace guard refuses inspection there and names the host command;
a nested workspace keeps the existing host-command guard convention.
Initialization does not read user config, including for performance defaults.
No gate approval step, prompt, network, temporary output or persistent write occurs.

## Boundaries

This does not alter the identity format/location, approval predicate, approval writes,
mount/scope/grant trust disclosures, user-config apply gate, y/N grammar or redaction
policy. It supplies no source-file attribution, per-setting semantic catalogue, risk
classification, two-view preview command or raw scope/grant comparison.

## Where the code lives

| Responsibility | Anchor |
| :--- | :--- |
| Structural diff, replay and record comparison | `ApprovalChangeSet`, `CompareApprovalRecord` in `internal/config` |
| Complete view formatting and root labels | `approvalPresentationLines`, `approvalAreaLabels` in `internal/cli/run` |
| Prompt/refusal/accepted-report caller | `changePrompter` and the preflight gate in `internal/cli/run` |
| Read-only public dispatch | `ConfigApprovalDiff` in `internal/cli/run`, config registry in `internal/cli` |

## Current values

| Value | Setting | Owner |
| :--- | :--- | :--- |
| Minimum terminal wrap width | 40 columns | `approvalPresentationLines` caller in `internal/cli/run` |
| Interpreted inline limits | 40 rendered characters; at most 5 items | `interpretedEffect` helpers in `internal/cli/run` |
| Raw diff context | 3 lines | `unifiedDiff` in `internal/config` |

## Why it's this way

| ID | Constraint |
| :--- | :--- |
| OQ-CAP1 | Keep both labeled views, linked by group number; neither is a substitute for complete Exact values. |
| OQ-CAP2 | Raw JSON comparison remains read-only and on request, outside approval. |
| OQ-CAP3 | Config-only unattended acceptance is disclosed; approval identity and no-prompt semantics stay unchanged. |
