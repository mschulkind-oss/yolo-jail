---
title: "Installed pile declaration contract"
date: 2026-10-21
status: open
tags: [lab-report, pi, work-control, acceptance]
summary: "Records what the installed work-control API consumes, its diagnostic gaps, and the acceptance boundary left unresolved."
---

# Installed pile declaration contract, 2026-10-21

A [lab report](README.md), not a plan or acceptance verdict. It inspects installed Pi APIs, not the loaded runtime or its projection. No software, runtime, configuration, hook, profile, cache, or tool changes were made.

## Fixed and committed

This report commits the verified installed API contract and source locators. No software or acceptance-channel fix was made.

## Observed contract

`manage_todo_list` declares `id`, `title`, `description`, `status`, and optional `criterion`, `rank`, and `after`. Its state manager clones whole item objects, so arbitrary keys can survive storage/readback; that is not proof a downstream consumer supports them. The pile normalizer reconstructs only its declared fields and drops `deliverable`, `question`, and `dispatch`. The supported checkable artifact is the one `criterion` string; `description` carries exact-line declarations, `rank` orders work, and `after` names todo IDs that gate on their terminal completion.

> [!WARNING]
> Do not add arbitrary fields to a todo row expecting a consumer. Readback proves persistence only. Do not rewrite a frozen criterion, remove and re-add an item, make up an owner answer, or invent a dependency to clear a diagnostic.

The owner-question consumer trims each description line and matches `^question:\s*(.+)$`. This declares an owner question; it is not a generic wait for CI, a runtime, or a schedule. For dispatch, the pile reads live or capacity-queued work-board run rows. No static `todo.dispatch` or CI/runtime-wait field exists in the inspected pile surface. Direct tool calls put native run association `item` in `options.item`; flat workflow child/run parameters carry `item` directly, with the primary first. This does not authorize launching or rebinding work.

A genuine `after` edge blocks while the named pile item exists and is not completed. The diagnostic index instead derives unmet edges from description `blockedBy:` relations. In production, the projection reads `after` to mark a row blocked but does not bridge those edges into the diagnostic index's `blockedBy` input. The installed projection test manually performs that bridge; it is not evidence the production caller does. If both gating and diagnostic visibility are needed, retain a real `after` dependency and its matching `blockedBy:` line. Never invent the relationship.

The criterion ledger freezes the first declared criterion, or the title when there is no criterion. Successful writes may append criterion text after the frozen text. However, the illegal-state branch still classifies any frozen `source: title` as “criterion is a topic” even when the current row now has a criterion and the ledger records an append. This is a classification defect in the inspected consumer; appending the deliverable sentence cannot repair that branch while preserving the frozen identity.

Codemode is not a bypass: installed codemode calls `ctx.executeTool`, and Pi's nested runner invokes the same before/after hooks as direct calls. Work-control snapshots successful nested todo writes before transcript persistence. Shared hooks do not make unsupported fields consumable or prove the running build.

## Payload shapes — examples only, not submitted

The write replaces the whole list. Preserve every row and its fields; the snippets transform one row inside the complete `rows` array. The first illustrates the still-open [OQ-MCW2](../design/macos-user-model.md#decision-ledger), not an answer:

```js
const updated = rows.map(row => row.id === 46
  ? { ...row, description: `${row.description}\nquestion: OQ-MCW2: Is protection against arbitrary same-UID processes outside a workspace's Seatbelt profile part of the security claim?` }
  : row);
const payload = { operation: "write", todoList: updated };
```

The second is schematic: use it only after independently verifying the referenced row is the real, existing, unfinished prerequisite. No dependency between particular items is established here.

```js
const prerequisite = String(verifiedExistingUnfinishedRow.id);
const updated = rows.map(row => row.id === verifiedDependentRow.id
  ? {
      ...row,
      after: [...new Set([...(row.after ?? []), prerequisite])],
      description: `${row.description}\nblockedBy: ${prerequisite}`
    }
  : row);
const payload = { operation: "write", todoList: updated };
```

## Open acceptance boundary

Item 23 remains `not-done` at inspected baseline `9f6ab06821bdc55a5c1f340433b3a6ded976e39d`; this report does not re-adjudicate it. The [existing evidence report](2026-10-21-macos-repair-evidence.md#native-evidence-and-acceptance-boundary) records hosted acceptance pending without a row-specific successful receipt. The exact non-secret frozen packet-contract excerpt needed to identify the acceptance channel remains unavailable here. **Channel 23 is unknown.** A push or green GitHub CI is not established as sufficient. Do not convert pending to acceptance by cosmetic deletion, start a duplicate review/implementation, or dispatch a new child under the completed-item guard.

The task handoff reports that nested `question:` lines did not clear warnings and that a later direct write tried the same criterion/question, but whether that write was consumed is unverified. The currently loaded build and projection are **not verified**; no claim is made about current runtime bytes or cleared warnings. The canonical [OQ-MCW2](../design/macos-user-model.md#decision-ledger) and [OQ-MCW3](../design/macos-user-model.md#decision-ledger) remain open with empty answers.

## Evidence and locators

Short excerpts were checked against local installed sources, not an upstream release. These locators identify consumers, not just API names:

```text
/home/agent/.pi/agent/yolo-patched/pi-manage-todo-list/src/tool.ts:24-51
  Schema: criterion, rank and after are optional; deliverable/question/dispatch are absent.
/home/agent/.pi/agent/yolo-patched/pi-manage-todo-list/src/state-manager.ts:19-20,45-76
  read() copies the array; write() clones each entire row.
/home/agent/.pi/agent/extensions/work-control.ts:4968-4999
  pile normalizer builds only id/title/status/description/criterion/rank/after/origin/predicatedOn.
/home/agent/.pi/agent/extensions/work-control.ts:6093-6101,6138-6146,6158-6185
  Exact question parsing; diagnostic reads blockedBy; title-source still yields illegal after criterion exists.
/home/agent/.pi/agent/extensions/work-control.ts:6048-6053,6328-6373,16623-16624
  No dispatch-spec journal on this branch; production board projects after but feeds rows to a blockedBy-only index.
/home/agent/.pi/agent/extensions/work-control.ts:14519-14531,14543-14570,18275-18298
  Freeze source and append ledger; successful nested/direct writes pass through shared hooks and snapshotting.
/home/agent/.pi/agent/yolo-patched/pi-subagents/src/extension/schemas.ts:230-238,293-309
  item is a string or ordered string list on flat run parameters; the model tool has options.
/home/agent/.pi/agent/yolo-patched/pi-subagents/src/shared/types.ts:1774-1785
  ItemRef documents the primary-first association on a run.
/home/agent/.local/share/pi-fork/node_modules/@earendil-works/pi-coding-agent/dist/core/agent-session.js:474-502
  Nested calls invoke the same before/after tool hooks.
/home/agent/.pi/agent/yolo-patched/pi-subagents/docs/tool-reference.md:70,76
  Direct model-call fields outside the listed top-level properties go in options.
/ctx/packs/matt/tests/work-control-pile-states.test.mjs:25-27,87-98
  Test-only helper manually maps after to blockedBy before calling the index.
```

Evidence commands: `uvx vantage-check style-guide` (Vantage 0.10.0), direct `sed` inspection of the locators, `git diff --check`, and `uvx vantage-check` on this report. No tests, build, CLI runner, API/auth, GitHub query, nested run, or runtime reproduction was performed.
