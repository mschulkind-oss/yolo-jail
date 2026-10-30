---
title: "Building the GitHub work kinds: the plan's seed"
date: 2026-10-30
status: draft
stage: SKETCH
next: "Wait for github-write-scopes.md to be accepted; then a planner completes this against the tree"
depends-on:
  - github-write-scopes.md#OQ-GW1
  - github-write-scopes.md#OQ-GW2
  - github-write-scopes.md#OQ-GW3
  - github-write-scopes.md#OQ-GW4
  - github-write-scopes.md#OQ-GW5
  - github-write-scopes.md#OQ-GW6
tags: [plan, github, broker, grants, config]
summary: "What the design of the GitHub work kinds happened to surface about the build. Not a hand-off: nothing may be built from this while it is a sketch."
---

# Building the GitHub work kinds: the plan's seed

**Status:** 2026-10-30 — seeded during design; not a hand-off, and incomplete until
[`github-write-scopes.md`](github-write-scopes.md) is accepted. The design wins on behavior.

What the design work surfaced, and nothing it went looking for. Each entry is settled-but-boring
or a note for the planner to check; no behavior is decided here.

- **Classifier.** `targetOf` in `internal/ghbroker/writetarget.go` already names a write's kind
  and resource from the canonical argv; the work kinds split its `pr-write` and `issue-write`
  into `comment`/`edit`/`approve` by the `pr review` flag (`p.has("approve")`,
  `p.has("request-changes")`) and, for `gh api`, by the `event` field (`p.values("field")`,
  `p.values("raw-field")`; `--input` or an `@` value is unreadable). `prSubResource` and
  `issueSubResource` stay the fence; the kind is assigned inside them.
- **The state read.** `readIssue` reads `issues/{n}` for PR-ness and `merged_at`. The
  auto-merge rule needs `pulls/{n}`'s `auto_merge` (and `draft`) for an `approve` or a ready;
  check whether one read of `pulls/{n}` can replace the `issues/{n}` read for a known PR, or
  whether two reads are needed when the number's kind is not yet known (`issue-or-pr`).
- **Config.** `requests.github.<kind>` becomes a mixed type (`true` or `"ask"`) in
  `internal/config/schema.go`; `create_pr` and `create_issue` take `"ask"` too. The
  `kind_capabilities` map in `packs/github/loopholes/github-broker/loophole.toml` declares the
  new capabilities; `pending_capabilities` is empty and stays so. Check how `readableallow.go`
  turns an `always` capability into a per-repository grant (`legacyKind`) and whether a work kind
  should be a grant on the repository in the scope file or a workspace-level fact the broker
  reads directly — the design needs only the resolution in [§5](github-write-scopes.md#5-two-layers-ceiling-asks-and-the-narrowing),
  evaluated under the store lock.
- **Verbs.** `yolo request` takes `<capability> [resource]`; `--ask` and `--local` are new flags.
  `yolo allow`'s pairing offer needs the interactive check `allowInteractive` already has.
  `yolo approve --add` output in `internal/cli/approvegithub.go` names the kinds the workspace
  holds, which means the CLI reads the effective allowance for the request's workspace.
- **Store.** `WorkingEntry.By` already distinguishes `create-pr`/`create-issue`, `human`, and a
  grant id; the merge-of-added rule reads it. A timed entry already has a kind
  (`brokerstore.WorkingMerge` and friends); check whether the kinds there need `comment`,
  `edit`, `approve` as distinct working kinds or whether a timed entry can carry the capability
  name.
- **Tests that pin today's line and will move:** `TestWorkingSetMergeAsksAndEndsTheEntry`,
  `TestCreatePRUnderAGrantRunsUnaskedAndJoinsTheWorkingSet`, `TestApproveGitHubForAndAddCLI`,
  and the `pr review --approve` row in `workingset_test.go`'s target table.
- **Docs that restate today's line:** the `description` in the github pack's `loophole.toml`,
  [BB-D111](boundary-broker.md#BB-D111)'s prose, and the pack's agent briefing.
