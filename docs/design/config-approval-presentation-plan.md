---
title: Implement complete config-approval presentation
status: accepted
stage: DECIDED
next: Implement serially from Slice 0
depends-on: [config-approval-presentation.md]
evidence-date: 2026-11-16
evidence-snapshot: HEAD @ 0dbe45c4b0db0148a0af4061c2338fabd0173f13
---

# Plan: Implement complete config-approval presentation

**Status:** Ready to implement against `HEAD 0dbe45c4b0db0148a0af4061c2338fabd0173f13`. The accepted design has no open owner questions. The source checks below settle U1–U6 against this tree. No source, index or documentation file in `/workspace` was changed while preparing this plan.

> **In short.** Keep the config approval identity, its canonical JSON bytes, its record, and the ask/don't-ask predicate unchanged. Add a replay-verified structural change set and render it in both Interpreted and Exact blocks at the existing gate callers. Add a read-only `yolo config approval-diff` inspection command. Report config-only acceptance on `--accept-config-changes`.

**Design dependency:** `docs/design/config-approval-presentation.md` (accepted). **Related design and build plan:** [`docs/design/workspace-git-skills.md`](workspace-git-skills.md) (accepted) and [`docs/design/workspace-git-skills-plan.md`](workspace-git-skills-plan.md) (DECIDED). This plan remains limited to config approval presentation; it does not implement Git skill imports.

## Precedence and traps

The accepted design wins on behavior; the inspected tree wins on source facts; this plan is advice and the first thing to correct if the tree has moved. Never twist code to match a stale symbol/path. Stop and report if a named production seam is gone, a red test does not fail as predicted, a required change crosses the fences below, or a non-cosmetic behavior disagreement appears.

- **Trap:** approval identity is serialized JSON, not display text. Never serialize TOML for storage or mutate the canonical serializer; that would re-prompt existing workspaces.
- **Trap:** `brokered` is projected out, but `requests` and `repos` are not. Dropping the latter merely because they participate in separate permission logic would hide approved config changes.
- **Trap:** the synthetic mount record is not a regular config key. Suppress it only when the current mount disclosure is actually printed; otherwise D8 must fall back to raw JSON.
- **Trap:** `ReadApprovalRecord` is read-only, but `ApproveConfigAndScope` can write even on a no-change branch. `approval-diff` must never reach the approval function.
- **Trap:** `configRunW` resolves a target and prints a disclosure for ordinary verbs. Dispatch the new targetless inspection verb before that path. `configApply` has an inline jail guard, not a reusable helper; do not invoke config apply to simulate a host check.
- **Trap:** the jail's own workspace and a nested workspace have different guard outcomes. Preserve both predicates already asserted by `TestConfigApplyRefusesInAJail` and `TestConfigApplyInAJailServesANestedWorkspace`.
- **Trap:** accepted config-only reporting currently is silent. Widen the accepted-report condition without moving it after record write or changing no-prompt behavior.

## 1. Verified tree facts and resolved U1–U6

The implementation below is grounded in the actual tree at the snapshot above, not just the older design baseline.

| Item | Finding at this HEAD | Implementation consequence |
| :--- | :--- | :--- |
| Approval identity | `config.SnapshotJSON` still returns `jsonx.DumpsSnapshot`; `approveConfigAndScope` compares the same canonical string to the stored config part. It writes only after acceptance. | Do not modify `SnapshotJSON`, JSON encoding, `approvalConfigPartWith`, gate predicates, record keys/bytes, scope/grant identities, or approval writer semantics. Presentation is additional data derived after the same read. |
| U1 — synthetic mount key | `approvalConfigPartWith` omits only `brokered`; `withMountRecord` adds the resolved-host list under the exact key `"(mount sources, resolved)"` when mounts exist. Existing `WritableMounts`/`MountSources` reports disclose it. | The presenter recognizes this one exact synthetic root. It is never emitted as a config path. Ignore its replay residual only when the existing mount block actually has lines; otherwise fail replay and show the raw comparison. |
| U2 — legacy and readable names | `ReadWorkspaceForGate` selects readable TOML loading when readable workspace files exist; otherwise it loads legacy JSONC/JSON. Existing projections/records may therefore carry readable names or supported legacy names. `ReadApprovalRecord` also normalizes known legacy record spellings via `respellLegacyRecord`. | Diff the actual previous/current trees without renaming paths. Map `packages`, `mise_tools`, `gpu`, `resources`, and `env_sources` only to their design-approved replacement labels; unknown legacy paths retain the generic key label. For example, the Exact path remains `packages` while its area uses the `install` label. Test both formats and actual normalization. |
| U3 — `requests` and `repos` | `approvalConfigPartWith` skips `brokered` only. It retains `requests` and `repos`; `ReadWorkspaceForGate` separately enforces workspace provenance for them. | Keep `requests` and `repos` in the structural change set and label coverage. Do not mistake their separate semantics for removal from the config identity. |
| U4 — label wording | `docs/reference/config/project-config.md` describes `[requests.<source>]` as asking for user-allowed capabilities and `[repos.<name>]` as naming repositories; `config/README.md` defines `requests` as the project's ask. | Use the accepted design's bounded labels `Permission requests` and `Named repositories`. Check every other label against the current config reference at implementation time; labels describe the key only, not a particular change or its effect. |
| U5 — private values | The identity is the merged workspace approval part, not user config or expanded environment contents. The current unified diff prints changed canonical identity values. The Exact view must print only changed values/items and full leaves of added/removed tables, never unchanged siblings or values fetched from files/environment. | No new source or secret-reading path. Test that the renderer receives only the old/current approval trees and does not read user config, env files, scopes or grants. Escaping is not redaction. |
| U6 — host-only refusal | There is no reusable host-only refusal helper. `Options.configApply` has a local guard: it refuses only when `o.inJail() && config.IsJailOwnWorkspace(o.Workspace)`, with fixed actionable text. `TestConfigApplyRefusesInAJail` and `TestConfigApplyInAJailServesANestedWorkspace` pin both sides. | `approval-diff` gets its own read-only guard with the same predicate and a one-line host-only refusal. Do not call `configApply` or any approval path to reuse its guard; that would create effects. Preserve the nested-workspace case. |

The read-only command must use `config.ReadApprovalRecord` and `config.ApprovalRecordPart` through the new `config.ApprovalRecordComparison` result, never `CheckConfigChanges`, `ApproveConfigAndScope`, `WriteApprovalRecord`, or the launch gate's approve step. The approve step can write even when the config is unchanged. `ReadApprovalRecord` reports an absent record without error and returns malformed stored text without repairing it.

Current runtime callers are also verified:

- Fresh launch: `Options.checkConfigChanges` → `runConfigGate` in `internal/cli/run/preflight.go` → `config.ApproveWorkspaceRead` (which delegates to the shared config/scope approval path) → `changePrompter.PromptReport` or `printChangeRefusal`.
- Config apply: `Options.configApply` in `internal/cli/run/liveapply.go` → `runConfigGate` with `gateHooks`; the accepted gate result then feeds the existing apply plan.
- Interactive attach: `attachExisting` in `internal/cli/run/run.go` calls `applyAtAttach`; it enters `liveApply` and the same config gate. A non-interactive attach does not ask.
- Scope and grants remain separate reports and record parts. Writable mounts and resolved mount sources are disclosed before config presentation. Do not merge these into config groups or reorder them.
- `internal/cli/config.go` routes `apply` after its verb/target handling. `config approval-diff` must be dispatched as a special inspection verb before target resolution, so it neither resolves a rendering target nor reaches any config writer.

## 2. Settled behavior contract

Implement the accepted design's D1–D14 as written, with this necessary D15 correction made first in Slice 0:

- The presenter is pure; it cannot decide whether to ask, approve, or record.
- A changed config key path has one numbered group in path-sorted order and exactly one matching number in each block. Exact is the authoritative list; Interpreted adds only the bounded top-level area label and fixed effect phrase. An unlabeled root says `no description for this key`.
- Recursively compare maps, scalars, lists, and arrays of tables. Preserve missing versus explicit JSON `null`; compare duplicate list occurrences, preserve one-based positions, recognize a pure permutation as `reordered`, and do not invent identity for array-table elements. Added/removed tables print every terminal value in sorted key order. No truncation, paging, cap, or `... more` shortcut.
- D8 replay is a hard completeness witness: apply the emitted groups to a copy of the previous tree and compare the canonical result with the current approval identity. The exact synthetic mount root may be accounted for only when the existing mount disclosure is printed. A parse or replay failure discards both structured views and prints the design's notice followed by the existing raw JSON diff; it never presents an empty or partial structured list.
- Presenter text is ASCII. Key segments and string values use TOML-basic-string-compatible escapes: control characters and non-ASCII BMP Unicode scalar values as `\uXXXX`; supplementary Unicode scalar values as `\UXXXXXXXX`. Do not emit surrogate code points or surrogate-pair escapes in display output. Keep canonical JSON identity bytes and JSON rendering unchanged. Existing header, mount, scope and grant markers retain their present glyphs and order.
- Prompt/refusal order remains: existing header; existing mount, scope, and grant blocks; config count line; Interpreted; Exact; existing counts; raw-comparison footer; question. Keep the footer immediately before the question. Non-TTY refusal contains the same two views and footer, and records nothing. Accepted reports keep the existing mount, scope/count and grant/count order, then append the same views; their Interpreted heading says `was approved`, and they have no footer or question. N/refusal writes nothing.
- A scope/grant/mount-only change does not gain fake config groups. When the config identity itself is unchanged, say `Workspace config: no change.` A resolved-mount-only change has no config-key path: the existing mount block is followed by `Workspace config: no config-key paths changed.` The existing blocks and their markers continue to disclose their own changes. Preserve the silent first-launch rule for an empty workspace config.
- `approval-diff` prints the design's three-line header (record path, current is computed/not saved, scope/grants are not compared), then `unifiedDiff`'s three-context-line raw JSON comparison. Equal, changed, absent and malformed-text records exit 0 with the specified notices/current identity. Read/validation failures exit 1; unexpected args/flags exit 2. It does not create the approvals directory, prompt, repair, write, or touch scope/grant state.
- The accepted-report condition includes `ConfigChanged` while keeping the existing accepted report before the approval write. Its config-only header must say workspace config, not imply that mounts changed.

## 3. Files, reuse, and ownership

| Path | Planned change | Reuse / fence |
| :--- | :--- | :--- |
| `docs/design/config-approval-presentation.md` | Slice 0: correct D15, the width/character-set wording, and the literal examples; refresh evidence metadata only for U1–U6 checked at this HEAD. At the final slice, mark the accepted design's built/status/next metadata accurately. | This design is the behavior contract. Do not expand its owner rulings or rewrite unrelated sections. |
| `internal/config/approvaldiff.go` (new) | Pure recursive structural diff, stable groups/operations, and replay verification. Keep the synthetic mount rule here; do not add key labels to the config identity package. | Reuse `jsonx.OrderedMap`, `jsonx.DeepCopyMap`, and `SnapshotJSON`; no generic diff framework. |
| `internal/config/approvaldiff_test.go` (new) | Source-named unit/property-style examples for traversal, list alignment, null/absence, table flattening, synthetic key, replay and legacy/readable roots. | Existing `config_test.go`, snapshot, scope and mount tests remain the identity/effect regression suite. |
| `internal/config/snapshot.go` | Carry the computed change set on `ChangeReport` and `ChangedNonInteractiveError` (or an equivalent single report object); populate it from the exact previous/current canonical approval strings on the existing changed branch. Widen accepted-report eligibility only for config identity change. | Keep the compare/prompt/write branches and order intact. If decode/replay fails, preserve `DiffLines` for fallback. |
| `internal/cli/run/approvalpresentation.go` (new) | ASCII TOML-display escaping, Interpreted/Exact formatting, width-aware wrapping, block labels/effects, and the safe raw-fallback notice. Pure formatter boundary takes already-verified data plus terminal width/color. | Reuse `tty.Width`, `o.pr`, existing color conventions, `printScopeBlock`, and current headers. Do not create a theme or risk vocabulary. |
| `internal/cli/run/preflight.go` | Wire the same presentation into prompt, refusal, and accepted reporting. Keep existing mount/scope/grant order, counts, prompt predicate, footer position and refusal advice. | Replace the production config-diff display at the consumer call sites; keep any narrow raw-diff rendering needed for D8 fallback. `printConfigDiff` may remain for raw comparison only. |
| `internal/cli/run/configapproval_test.go`, `configapprovaldispatch_test.go`, `writablemountprompt_test.go`, `liveapply_test.go`, `integration/attachskewrestart_linux_test.go`, `integration/mechanics_test.go`, and focused new source-named tests | Update assertions that require unified JSON hunks in the gate; add real gate/caller tests and preserve block order/markers/record identity. | Existing helpers `approvalOptions`, `approvalConfig`, `approveWorkspaceConfigThroughGate`, `applyFixture`, and runtime seams are test-only fixtures; never use a real workspace, terminal, approval, or credential. |
| `internal/cli/run/approvalpresentation_test.go` (new) | Formatter escaping/wrapping/completeness and coverage of all workspace-settable labels. | Run alongside, never instead of, the actual gate/caller tests. |
| `integration/attachskewrestart_linux_test.go`, `integration/mechanics_test.go` | Assert rendered output from the real launch PTY and `config apply` binary paths; preserve non-interactive attach behavior. | Update the existing fixtures rather than adding a new approval source; all approvals are synthetic and in temporary workspaces. |
| `internal/config/approvalcomparison.go` (new) | `ApprovalRecordComparison(workspace)` reads `ReadApprovalRecord` and `ApprovalRecordPart`; distinguishes missing, valid, and malformed old record; returns existing unified diff without repairing bytes. The result holds record path/presence, current canonical text, equality, notice state and diff lines for the CLI. | Keep side-effect boundary explicit; no call to `CheckConfigChanges` or approve/write functions. |
| `internal/cli/run/approvaldiff.go` (new) | Host/nested-workspace guard, arg validation, read-only command result and error exit/output policy. | Mirror the exact conditional of `configApply`, but keep an independent one-line refusal and do not route through apply. Test both the jail's own workspace and a nested workspace. |
| `internal/cli/config.go`, `internal/cli/configapprovaldiff_test.go` (new), and `internal/cli/run/approvaldiff_test.go` (new) | Register the `approval-diff` command and help token; route before target resolution, with no flags except help. Test through `configRunW` and the run command boundary. | Reuse the established config dispatch/output seams. Do not add config values or change render/diff compatibility refusals. |
| `docs/reference/config-safety.md` | Re-verify and update the stale 2026-09-09 description, verified stamp, call-site table, ordering and test references to the new structured view and read-only verb. | Preserve approval/record/security rulings; do not imply a changed identity or prompt predicate. |
| `docs/reference/config/editing.md` | Add `yolo config approval-diff` to the inspection table, with its read-only/no-record boundary. | It is not an editing verb and must not be added as a config key-writing suggestion or to the `config-ref` editing verbs list. |

### Cross-feature serial fence: workspace Git skill imports

The accepted skill-import design now has the DECIDED implementation plan [`workspace-git-skills-plan.md`](workspace-git-skills-plan.md). Do not implement its feature from this approval plan or guess its files; its plan owns that work. The roadmap routes it under [Buildable now](../plans/roadmap.md#buildable-now-by-an-agent). The approval-display tasks and gates below remain unchanged. The two features are separate and must land/rebase serially wherever they share a display key or repository surface:

- `internal/cli/config.go` belongs to config approval presentation's `yolo config approval-diff`; skill imports registers the top-level `yolo skills` command in `internal/cli/dispatch.go`. Keep these surfaces distinct; recheck common registry/help tests after both land.
- The approval label-coverage test is tied to workspace-settable readable keys. If skill imports adds `skill_imports` before that test's slice lands, include its accurate noun-phrase label and update coverage in the same skill-import slice. If the approval slice lands first, rebase skill imports and keep its coverage green. Never omit the key or weaken the assertion.
- This approval plan changes `internal/cli/run/preflight.go` and `internal/config/snapshot.go`; skill imports must resolve after the existing gate, and its plan explicitly avoids these files unless its implementer finds a necessary source change. If that becomes necessary, stop and coordinate/rebase rather than taking a concurrent hunk or changing approval identity/order.
- The approval plan's gate, display, approval-record, integration-test and command fences remain unchanged. It does not transfer permission to edit Git broker, scope, mount, import-store or jail-staging files. Resolve any new overlap by serial landing and retesting both caller suites.

## 4. Tests-first implementation slices and exact gates

Land the following slices serially in one worktree. Do not stage or commit files outside the named slice. Tests must call the production consumer boundary where one exists; pure formatter tests alone are not sufficient evidence.

### Slice 0 — Correct the display-escape contract

**Files:** `docs/design/config-approval-presentation.md` only.

**Tests first / red:** No runtime test applies. Before editing, verify the current tree cannot meet the accepted D15 literally for supplementary characters by applying its proposed `\uXXXX` form: a valid TOML basic-string escape for U+1F600 is `\U0001F600`, not UTF-16 surrogate escapes. Preserve that distinction in the doc diff.

**Change:** Correct the D15 decision, the character-set/width rule, and the rendering examples with the exact contract from [§2](#2-settled-behavior-contract): controls and non-ASCII BMP scalars `\uXXXX`; supplementary scalars `\UXXXXXXXX`; never emit surrogate code points/pairs. In addition to D15 and the character-set bullet, update the TOML-literal table’s control/non-ASCII example into separate control/BMP and supplementary-scalar rows. State explicitly that display escaping is separate from canonical JSON identity. Recheck the D15 reason (prevent control/bidi reshaping) and do not change the decision or original JSON encoder contract. Record U1–U6 current-tree findings and update the design evidence snapshot only to the reviewed commit.

**Green:** `uvx vantage-check docs/design/config-approval-presentation.md` from `/workspace` passes. Review the diff to confirm no other accepted design behavior moved. Replace the unverified U1–U6 assumptions in the design with the bounded results from section 1 here; do not rewrite the original baseline stamp or broaden the verification claim. Commit: `docs(config): specify TOML-valid approval display escapes`.

### Slice 1 — Pure structural change set, complete replay (after Slice 0)

**Files:** `internal/config/approvaldiff.go`, `internal/config/approvaldiff_test.go`.

**Test first (red):** Add a primary case that uses actual ordered maps and fails until the structural diff/replay exists. Example contract (adapt constructor names only to existing `jsonx` API):

```go
func TestBuildApprovalChangeSetReplaysAndExcludesDisclosedMountRecord(t *testing.T) {
    previous := decode(t, `{
      "install": {"nix": ["jq"]},
      "(mount sources, resolved)": ["/srv/old → /ctx/data"]
    }`)
    current := decode(t, `{
      "install": {"nix": ["jq", "ripgrep"]},
      "(mount sources, resolved)": ["/srv/new → /ctx/data"]
    }`)

    got := BuildApprovalChangeSet(previous, current, true) // existing mount block is printed
    if !got.ReplayVerified {
        t.Fatalf("groups did not account for the approval identity: %+v", got)
    }
    if len(got.Groups) != 1 || got.Groups[0].Path != "install.nix" {
        t.Fatalf("groups = %+v; want only install.nix, never the synthetic mount key", got.Groups)
    }
    if got.Groups[0].Verb != "changed" || !reflect.DeepEqual(got.Groups[0].AddedItems, []any{"ripgrep"}) {
        t.Fatalf("install.nix change = %+v; want one added occurrence", got.Groups[0])
    }
}
```

**Red command:** `go test ./internal/config -run '^TestBuildApprovalChangeSetReplaysAndExcludesDisclosedMountRecord$' -count=1`; before implementation this must fail because the structural API/behavior is absent.

**Green behavior matrix:** add source-named cases for:

1. Ordinary `changed`, `added`, and `removed` nested keys; absent versus explicit `null`; and empty lists/tables.
2. Duplicate list occurrences with exact one-based indexes; a pure reorder with equal multiplicities; the LCS tie-break (earliest previous, then earliest current index); and list add/remove without inventing a scalar edit.
3. Arrays of tables changed as remove+add at positions, plus every leaf of added/removed tables in sorted order.
4. Escaped map-key/dotted path segments and sorted group numbering.
5. The actual `mountRecordKey` excluded only when mount lines exist; without mount disclosure, the synthetic-only residual makes replay false.
6. No mutation of either input; readable TOML `install.nix` and legacy JSON `packages` path without renaming either tree; retained `requests`/`repos` paths; and no elision of tables/paths at any size.

The replay test compares the replayed tree's `SnapshotJSON` bytes to the exact current identity, with only the one U1 mount exception when a mount report is printed. A mismatch produces `ReplayVerified == false`; the caller, not the pure helper, selects the raw fallback. This makes the correctness property independently assertable.

**Gate:**

```sh
gofmt -w internal/config/approvaldiff.go internal/config/approvaldiff_test.go
go test ./internal/config -run '^TestBuildApprovalChangeSet' -count=1
```

First command result must show the named tests passing; the pre-implementation red is missing behavior/API, not a timeout or fixture failure. Commit: `feat(config): compute replay-verified approval changes`.

### Slice 2 — Wire all gate consumers and exact safe display (after Slice 1)

**Files:** `internal/config/snapshot.go`, `internal/cli/run/approvalpresentation.go` (new), `internal/cli/run/preflight.go`, focused tests in `internal/cli/run/configapproval_test.go`, `writablemountprompt_test.go`, and `liveapply_test.go` (or narrow new `configapprovalpresentation_test.go`).

**Test first (red), at the production gate:** extend `TestNonInteractiveConfigChangeRefusalIsActionable` or add a source-named sibling. Seed the old value through the existing test gate helper, change the workspace file, call `o.checkConfigChanges` with no TTY, and assert that refusal contains both numbered views, the changed path and full value, and the footer; assert the stored approval bytes remain byte-identical. Do not test only the pure formatter. The red behavior is that the current gate prints JSON hunks rather than the two blocks.

```go
func TestConfigApprovalRefusalRendersBothViewsAndRecordsNothing(t *testing.T) {
    ws := t.TempDir()
    o, out := approvalOptions(t, ws, false)
    approveWorkspaceConfigThroughGate(t, o, ws, `{"packages":["jq"]}`, out)
    before, err := os.ReadFile(config.ApprovalSnapshotPath(ws))
    if err != nil { t.Fatal(err) }

    writeWorkspaceConfig(t, ws, `{"packages":["jq","ripgrep"]}`)
    if o.checkConfigChanges(nil, "podman") {
        t.Fatal("changed config without a terminal must refuse")
    }
    for _, want := range []string{"Interpreted", "Exact", "[1]", "packages", "ripgrep",
        "yolo config approval-diff", "Accept these workspace config changes?"} {
        if !strings.Contains(out.String(), want) { t.Errorf("missing %q:\n%s", want, out) }
    }
    after, err := os.ReadFile(config.ApprovalSnapshotPath(ws))
    if err != nil { t.Fatal(err) }
    if !bytes.Equal(before, after) { t.Fatal("refusal rewrote the approval identity") }
}
```

**Red command:** `go test ./internal/cli/run -run '^TestConfigApprovalRefusalRendersBothViewsAndRecordsNothing$' -count=1`; against the baseline this fails because the production gate has JSON hunks instead of both views.

Use the package's current fixtures/imports; the test's function call must remain `o.checkConfigChanges`, not a direct formatter invocation.

**Green behavior matrix and call-site evidence:**

- Interactive fresh-launch gate: same fixture, `IsTTYStdin` and `IsTTYStderr` true, `Stdin = strings.NewReader("n\n")`; both blocks and footer precede the question; N refuses and does not mutate the record. `y` in a scratch fixture exercises acceptance, not a real operator approval.
- Config apply: extend `liveapply_test.go` through its existing `configApply`/`applyFixture` path. A config-only changed record prints the views before apply outcome; preserve `configApply`'s `ChangedNonInteractiveError` retry/action line and its approval/apply sequence. Keep assertions for accepted config identity bytes.
- Interactive attach: exercise `applyAtAttach`/`liveApply` with the test seam so the actual attach gate prints the same views. Keep the non-interactive attach's current one-line behavior; it must not start asking. The existing integration mechanics test pins the non-interactive attach caller, and `attachExisting → applyAtAttach` remains the source call-site to preserve.
- `--accept-config-changes`: call the real check path with a synthetic temp workspace and explicit test flag; config-only acceptance must print the views and correct config-only header before record write. Verify the resulting bytes equal the bytes the same gate would accept interactively. Assert the accepted view has `was approved` and no question/footer. No live workspace, credential or human approval is used.
- Mixed scope/grant/read-write mount case: update the existing consumer tests so mount marker, scope block and grant block are still before config count/Interpreted/Exact; footer stays immediately before y/N. A scope-only, grants-only, or mount-only case has no fabricated config path and keeps the current block order/markers. Continue testing in `scopeapproval_test.go`, grant-gate tests and `writablemountprompt_test.go` at the real production report caller.
- TOML display escaping: assert ASCII-only output for `"emoji 😀"`, a control such as U+001B, a bidi override U+202E, and a non-ASCII key segment. Expect `\U0001F600` for the supplementary scalar, `\uXXXX` for BMP/control values, and assert there is no raw bidi/control character and no `\uD83D\uDE00` surrogate-pair rendering. Test escaping in Exact paths and values. Canonical `SnapshotJSON` bytes before/after display remain identical.
- Full display completeness: keep one group per changed path in each view; added/removed tables expose every leaf; long values wrap only at escape boundaries on a terminal and do not hard-wrap on non-TTY output; labels match the key, generic fallback is literal, and no severity/risk wording appears.
- Invalid old identity or replay mismatch: through the real gate consumer, both structured views are absent, the design notice and old unified JSON comparison are present, and the gate still follows its original ask/refuse/accept branch. Never test fallback only as a helper.
- Runtime proof: update `integration/attachskewrestart_linux_test.go:TestAttachRestartsAnOlderJailAtATerminal` to assert both views and footer are visible before its existing y/N event; update `integration/mechanics_test.go:TestNestedMechanicsTracksAppliedLaunchAndLiveMounts` to assert config-apply accepted views and confirm the pending non-interactive attach retains its one-line notice without asking.

**Gate:**

```sh
gofmt -w internal/config/snapshot.go internal/cli/run/approvalpresentation.go internal/cli/run/preflight.go internal/cli/run/configapproval_test.go internal/cli/run/writablemountprompt_test.go internal/cli/run/liveapply_test.go
go test ./internal/config -run '^(TestCheckConfigChanges|TestApproval|TestBuildApprovalChangeSet)' -count=1
go test ./internal/cli/run -run '^(TestConfigApproval|TestNonInteractiveConfigChangeRefusalIsActionable|TestConfigChangePromptRequiresTerminalStdinAndStderr|TestWritable|TestConfigApply)' -count=1
```

Run focused tests first; if an existing test has a stale unified-diff assertion, update it to the accepted visible contract rather than preserving JSON hunks in the prompt. Commit: `feat(run): show complete config approval views`.

### Slice 3 — Read-only inspection verb through CLI dispatch (after Slice 2)

**Files:** `internal/config/approvalcomparison.go` (new), `internal/cli/run/approvaldiff.go` (new), `internal/cli/run/approvaldiff_test.go` (new), `internal/cli/config.go`, `internal/cli/configapprovaldiff_test.go` (new).

**Test first (red), through the command router:** create a temporary HOME/workspace, write a real config fixture and a synthetic old approval-record file directly as test setup; snapshot its exact bytes; invoke `configRunW([]string{"approval-diff"}, &out, &errw)`. Assert the command emits the record/current/not-compared header plus the existing raw unified diff and that record bytes do not change. This writes fixture bytes only; it does not call approval code or touch a user's approval.

```go
func TestConfigApprovalDiffIsReadOnlyThroughDispatch(t *testing.T) {
    home, workspace := withHomeAndCwd(t)
    _ = home
    writeFile(t, filepath.Join(workspace, config.WorkspaceTOMLConfigName),
        "[install]\nnix = [\"jq\", \"ripgrep\"]\n")
    recordPath := config.ApprovalSnapshotPath(workspace)
    old := `{"install":{"nix":["jq"]}}`
    writeFile(t, recordPath, old+"\n") // fixture only; no gate/approval API
    before, err := os.ReadFile(recordPath)
    if err != nil { t.Fatal(err) }

    var out, errw bytes.Buffer
    if rc := configRunW([]string{"approval-diff"}, &out, &errw); rc != 0 {
        t.Fatalf("rc=%d stderr=%s", rc, errw.String())
    }
    for _, want := range []string{recordPath, "current: computed now, not saved",
        "scope and loophole grants are not compared", "--- previous config", "+++ current config",
        "ripgrep"} {
        if !strings.Contains(out.String(), want) { t.Errorf("missing %q:\n%s", want, out) }
    }
    after, err := os.ReadFile(recordPath)
    if err != nil { t.Fatal(err) }
    if !bytes.Equal(before, after) { t.Fatal("approval-diff changed the stored record") }
}
```

**Red command:** `go test ./internal/cli -run '^TestConfigApprovalDiffIsReadOnlyThroughDispatch$' -count=1`; against the baseline the router rejects `approval-diff` with exit 2.

The current `unifiedDiff` headings are `--- previous config` and `+++ current config`; keep those exact literals in the dispatch assertion. Exercise these additional production dispatch cases: equal record; no record (current canonical JSON printed, no approvals directory created); malformed stored record (raw text comparison and no repair); unreadable record (use an `approvals` regular file as an `ENOTDIR` fixture, not mode bits that root can bypass); invalid workspace config or unresolved mount; extra positional args and `--accept-config-changes` return usage exit 2; help; and output/error stream separation.

At the run-package command boundary, test `YOLO_VERSION` plus the jail's own workspace refuses before reading/creating approval state, names the host action, and returns 1. Also test an in-jail nested workspace is not refused, matching the existing `config apply` predicate and its tests. Do not introduce a generic host-only guard refactor for this command.

**Green gate:**

```sh
gofmt -w internal/config/approvalcomparison.go internal/cli/run/approvaldiff.go internal/cli/config.go internal/cli/configapprovaldiff_test.go
go test ./internal/config -run '^TestApprovalRecord' -count=1
go test ./internal/cli/run -run '^TestConfigApprovalDiff' -count=1
go test ./internal/cli -run '^TestConfigApprovalDiff' -count=1
```

Commit: `feat(cli): add read-only approval diff command`.

### Slice 4 — Reference updates, accurate built status, final focused gates (after Slice 3)

**Files:** `docs/reference/config-safety.md`, `docs/reference/config/editing.md`, and status/ledger metadata in `docs/design/config-approval-presentation.md`.

**Tests first (red):** Add/update the doc/source checks where they exist so `config-safety.md` asserts the current approval formatter/command behavior, and `config/editing.md` lists the read-only verb without calling it an editor. Keep the doc's verified-commit stamp honest. Do not add the read-only command to the config-writing verbs or alter `config-ref`'s actual command registry.

**Change:** Re-verify the whole config-safety explanation against current readers/callers. Replace the old unified-diff-as-prompt claim with Interpreted/Exact behavior, D8 fallback, ASCII TOML display escapes, and `approval-diff` read-only semantics. Preserve accurate host-only, nested-workspace, no-prompt, no-write and identity constraints. Mark the three owner design rulings built only after the implementation slices are green; update `next`/status metadata to describe the landed code without claiming a commit that does not exist.

**Gate:**

```sh
uvx vantage-check docs/design/config-approval-presentation.md docs/reference/config-safety.md docs/reference/config/editing.md
go test ./internal/config ./internal/cli/run ./internal/cli -count=1
git diff --check
git status --short
git diff --cached --exit-code
```

The Vantage checker must pass; `git diff --cached --exit-code` must report no staged changes. Commit: `docs(config): document approval presentation and inspection`.

## 5. Final landing and nested-jail verification

These are guidance for the eventual implementation/landing owner, not commands run while authoring this plan.

1. For each serial slice, run its targeted red/green gates above before committing. Keep commit slices in order; do not combine them with workspace Git skill imports or land those overlapping edits concurrently.
2. For the complete candidate, run `just format`, the focused package gates above, and `just build-dev-bundle`. The changed `internal/cli` paths make the project-mandated nested-jail check mandatory. Invoke the emitted launcher by its **absolute path** from a throwaway workspace (`just dev -- <args>` follows the repository workflow). Exercise `config approval-diff` with fixture/empty host state and confirm no writes. For gate smoke, use a throwaway workspace and a no/absent-TTY refusal or N; never approve a real workspace. No APIs, user credentials, or actual user approval are needed.
3. Nested-jail limitation: this change is not a reachability-shaped or rootless-only change, so a nested jail is useful. If implementation expands into either class, the project guide's caveat applies: podman-in-podman forces `--net=host` and `--userns=host` and proves neither rootless reachability nor rootless-only paths; report `podman info --format '{{.Host.RootlessNetworkCmd}}'` or `{{.Host.Security.Rootless}}` as applicable, and leave that claim to a real rootless host/CI.
4. At landing, run `just check-ci` once on the combined tree and `just test` once: this changes launch-prompt output, so the full integration suite is mandatory for the landing owner, not every worker. Also run the focused changed-path integration tests: `go test -run '^(TestAttachRestartsAnOlderJailAtATerminal|TestNestedMechanicsTracksAppliedLaunchAndLiveMounts)$' ./integration`. The former exercises the real fresh-launch prompt through a PTY; the latter exercises the actual binary config-apply accepted report and non-interactive attach flows. Assert both views before the PTY question and its footer immediately above the question; assert accepted views have no footer/question; assert the pending attach retains its one-line notice without the views or a prompt. No test calls an API or uses a real workspace approval.
5. `just done` is only the post-commit change-aware check; it is not a substitute for `just check-ci`. This plan-authoring task itself must not run those gates, approve records, or modify the checkout.

## 6. Fences, stops, and “Ships with”

**Fences:** no change to approval identity or JSON bytes; no change to gate predicate, TTY grammar, record location/key/schema, mount/scope/grant records, authorization semantics, or launch/attach timing; no user-config/default values; no redaction-policy redesign; no `config check`/`config apply` behavior change outside the specified rendering/reporting; no general-purpose diff or theme framework; no work on skill imports, broker access, workspace keys, or staging; no credentials, API calls, actual human approvals, source/index edits while this plan is being authored; no full test suite/census by a worker.

**Stop rather than guess if:** the actual approval identity starts using a different serialization/projection; the synthetic mount key ceases to be distinguishable; replay cannot explain a change and raw fallback cannot preserve the old identity; rendering would expose a value not already represented by the changed approval identity; interactive attach or config apply no longer reaches the shared gate; host-only refusal semantics changed; a new config-root label cannot be grounded in the reference; or the actual workspace Git skill implementation plan requires incompatible changes in a shared file. Escalate that evidence to the owner/coordinator before widening scope.

**Ships with:** complete, replay-verified structural groups; stable path/array/list handling with null-versus-absent and duplicate preservation; ASCII-safe TOML display escaping including non-BMP scalars; one shared Interpreted/Exact presentation at fresh launch, config apply, interactive attach, refusal and accepted-report call sites; unchanged mount/scope/grant blocks/order and approval bytes; a safe raw fallback; read-only `yolo config approval-diff` with complete absence/equality/malformed/error/host-boundary behavior; tests at pure, gate-consumer and CLI-dispatch boundaries; updated config-safety and editing references; reconciled accepted-design build metadata.
