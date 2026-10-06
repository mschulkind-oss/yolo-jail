---
title: "Plan: GitHub scope widening moves into the workspace config"
date: 2026-10-05
status: accepted
stage: DECIDED
next: "Build step 1, the confined workspace reads in internal/config/load.go, then the rest in one landing"
depends-on:
  - workspace-widening.md
tags: [plan, github, broker, config, approvals]
---

# Plan: GitHub scope widening moves into the workspace config

**Status:** 2026-10-05. Completed against the tree, written against `2a9d3a5a7`.
**Design:** [`workspace-widening.md`](workspace-widening.md), every section of its [§3](workspace-widening.md#3-the-design).
**Precedence:** the design wins on behavior, the tree wins on fact, and this file is advice and
the first thing to be wrong. Never twist the code to match it.

## Map

| Path | Change |
| :--- | :--- |
| `internal/config/load.go`, `sources.go` | A workspace load that records sources reads each file beneath an `os.Root` on the workspace; `srcFile` gains `contained`, `rel` (workspace-relative) and `via` (the include that reached it) |
| `internal/config/brokered.go` | Rewritten: the retired `workspaces` message, the user-scope `repos` refusal, the workspace shape, containment and duplicate checks, the entry reader, the projection. Keep `brokeredKeyNames`, `resolvesThrough` and `brokeredWorkspaceKeyProblem`, which `workspacefile.go:268`, `:332` use |
| `internal/config/gateread.go` | new: `ReadWorkspaceForGate`, `WorkspaceRead.Entry`, `NewScopeCheck` |
| `internal/config/scopeapproval.go` | `ScopeSource.Entry`; the sources record; `compareScope`, `scopeBlock`, the counts; `RecordApproval` writes three parts |
| `internal/config/snapshot.go` | The projection before `SnapshotJSON`; `ApproveConfigAndScope` returns the approved scope; the flag path reports; the refusal's headline, advice and counts |
| `internal/config/inherit.go:259-263` | The `brokered` reason |
| `internal/cli/run/preflight.go:391-421`, `:520-578` | `checkConfigChanges(merged, rt)` does the strict read; the count line; the accepted report; the decline lines |
| `internal/cli/run/run.go:449`, `:1213` | The call sites lose their `LoadWorkspaceConfig` line |
| `internal/cli/run/brokeredscope.go` | The gate's result kept in memory; `writeScopeFiles` loses the re-read, `widened` and the fail-closed exception; one launch line |
| `internal/cli/run/keeperplan.go:63-65`, `keeperspawn.go:228`, `keeper.go:262`, `runcmd.go:241-244` | `ApprovedScopes` becomes `map[string][]config.ScopeRepo` |
| `internal/cli/check/check.go:672-691`, `brokeredscope.go` | The accept path through the shared helper, strict, printing the block; the old-form list; the unbrokered-source warning |
| `internal/cli/capturehost.go:737-745` | Delete the sources record too |
| `internal/brokerscope/file.go`, `remotes.go:342-345` | `ConfigFile`, `LocalFile`; comments |
| `internal/ghbroker/scope.go`, `classify.go:160-186`, `daemon.go:131-137` | The advice from the scope file's names; texts drop *widening entry* |
| `internal/render/configkeys.go:165-170`, `internal/loopholedecl/brokered.go:25-27`, `:43-46` | Reasons and comments |
| `internal/cli/gh.go:20-33`, `config_ref.txt:167`, `:173`, `:196`, `:374-410`, `apply.go:1398`, `:1431` | Help, reference, and the false *gitignored* |
| `packs/github/{pack.json,README.md,briefing/gh.md}`, `loopholes/github-broker/manifest.jsonc` | Scope wording; the new bullet |
| `internal/jailcontent/builtinskills/configuring-the-jail/SKILL.md:42-56` | The contrast line; `:55` |
| Docs and changelog | See [Ships with](#ships-with) |

## Reuse

- **Locating a message:** `Sources.AnnotateOne` and `locatedAt` (`sources.go`). A validator that
  re-reads one scope is `brokeredUserSwitchRefusal` (`validate_loopholes.go:440-469`).
- **The duplicate check:** `srcFile.locate` already builds a `json5.Index` per file, and
  `Index.Locate` returns the *written N times* error (`json5/locate.go:101`).
- **Escaping:** `termsafe.Visible` on every file label, then `richtext.Escape` at a markup printer.
  `printScopeBlock` (`preflight.go:440-456`) already escapes each line.
- **Which file the loader reads:** `config.ResolveWorkspaceConfigPath`, never a joined name.
- **The retired-key shape:** `validateUseProfilesRetired` (`validate.go:1857`): an error on the
  host, a warning with the snapshot suffix in a jail, the user's own value respelled.
- **Fixtures:** `brokeredFixture` and `gbOn` (`run/brokeredscope_test.go`); `approvalOptions`
  (`run/configapproval_test.go`); `hostFloorHome`, `decode`, `write` (`config` tests);
  `newGitHubBrokerFixture`, `recordScope`, `ghScript` (`integration/githubbroker_test.go`).

## Contracts

- `config.ReadWorkspaceForGate(workspace) (*WorkspaceRead, error)`: strict, with sources, and an
  error for a `brokered` that a file outside wrote. Its `Config` is unprojected.
- `config.NewScopeCheck(workspace, []ScopeBroker{Source, Label, RemoteHost}, *WorkspaceRead)`:
  the one helper the launch and `yolo check` share.
- `config.ScopeRepo{Repo, Sources}`: `Sources` are display labels, already `termsafe.Visible`. It
  crosses to the keeper as JSON; the plan is build-stamped (`keeper.go:603`), so its shape may
  change freely.
- The sources record's keys, `remote:<name>` and `file:<workspace-relative path>`, come from one
  function, so [OQ-WW1](workspace-widening.md#OQ-WW1)'s other answer is a one-line change.
- The scope file's `config_file` and `local_file`, omitted when empty; `FileVersion` stays 1.

## Traps

- **A non-strict load never errors** (`handleParseFailure`, `load.go:80-87`, and the include
  checks, `:250-286`). The gate and the check read strict, or the refusal never fires.
- **Containment is the open, not a path.** Compute the relative name, open it with `Root.Open`,
  and on any `Root` error fall back to today's `os.ReadFile`, marked outside. An absolute link
  fails `Root.Open` by design and counts as outside.
- **The two kinds of path differ.** The top-level files are joined unresolved; an include is
  resolved at join time (`helpers.go:78-84`). Take the relative name against both the given and
  the resolved workspace, or macOS's `/var` → `/private/var` reads as outside.
- **Only `record` loads open a root.** The plain loads are the launch's hot path, and nothing
  reads `contained` there.
- **`ValidateConfig` gets the merged map.** The retired check reads `workspaces` from it, which is
  what catches an old `.yolo/config-assembled.json` in a jail. Everything else re-reads its own
  scope, since a merged `brokered` mixes them.
- **The in-jail user scope is the generated inherited file**, which never holds `brokered`
  (`inherit.go`). An in-jail test cannot expect a user-scope refusal.
- **This jail sets `YOLO_VERSION`**, and `inJail()` turns every refusal into a warning. Run tests
  under `env -u YOLO_VERSION -u YOLO_HOST_LAYERS`.
- **16 test call sites pass `checkConfigChanges` a map.** With the read moved inside, those tests
  write `yolo-jail.jsonc` instead.
- **Two AST pins read `runContainer`** (`TestFreshLaunchCallsTheConfigArtifactWriter`,
  `TestFreshLaunchENFORCESTheApprovalGate`). Keep `checkConfigChanges` the condition of an `if`
  that returns, before `writeLaunchConfigArtifacts`.
- **`describeRepos`'s line is the integration oracle** (`recordScope` matches `repository scope
  recorded: <list>`). Keep the flat list; only the empty case's words change.
- **Stage new files before any integration run.** The image sees tracked files only.

## Build order

One landing, by the design's [§8](workspace-widening.md#8-sequencing): each step ends green under its command, and all land as one commit.

1. Confined reads and the `srcFile` fields. → `go test -short ./internal/config`
2. Validation: retired, user scope, workspace shape, containment, duplicates; the warning leaves
   `ValidateConfig`. Rewrite `brokered_test.go`. → the same
3. The gate read, projection, sources record, comparison, block, counts and approved result. →
   the same, `scopeapproval_test.go` included
4. Launch, keeper and scope file; the check's accept path, list section and warning. →
   `go test -short ./internal/cli/run ./internal/cli/check ./internal/cli`
5. Broker texts and the scope file's names. → `go test -short ./internal/ghbroker ./internal/brokerscope`
6. Packs, skill, help, reference docs, changelog. → `just check-ci`
7. Integration. → `go test -count=1 -timeout 0 -run 'TestGitHubBroker' ./integration`

## Ships with

- **Unit, `config`:** a workspace `repos` accepted and located at its file and line; each
  malformed element; `sets` an unknown key; a `brokered` through `../` and through a link swapped
  to point out, refused; two `brokered` objects in one file refused; a user-scope `repos`
  refused, naming the workspace files; the old form naming its own project's edit, into the local
  file, and counting the rest with no other path; a record without `brokered` byte-identical
  through the projection; `"brokered": null` asking once; a source change and a rename asking;
  the upgrade rule, silent for remotes only and asking for an entry; escaped file labels; the
  flag path reporting the block; the check's record writing three parts.
- **Unit, `run`:** a file made unparseable after the strict read refusing at the gate; an edit
  after the `y` never reaching the scope file; a record rewritten between gate and spawn not
  reaching it either; the launch line naming each source, escaped; a launch with no brokering
  pack printing no `brokered` line; the old form's launch log naming no other workspace.
- **Unit, `check`, `cli`, `ghbroker`, `jailcontent`:** the accept path's strict read, block and
  union; the old-form section listing every project; the capture cleanup removing the sources
  record; the refusal naming the scope file's two files, and falling back without them; the
  skill no longer calling the local file gitignored.
- **Integration:** `TestGitHubBrokerAWideningEntryAdmitsARepositoryForOneWorkspace`, rewritten to
  the workspace entry. The check prints and records the union, the flag launch prints the block,
  the launch line names `yolo-jail.jsonc`, and the refusal names the entry and the files. It is
  the end-to-end test the design asks for, so no second one is added.
- **Rewrite, never repair:** the four tests in today's `brokered_test.go`;
  `TestAWideningEntryJoinsTheScopeFileAndIsDisclosed`, `TestAWideningEntryWithNoRemoteAndNoApproval`
  and `writeWidening`; `TestTheBrokerRunsAWidenedRepositoryAndNamesTheEntryForAnother`;
  `TestEveryAccountWideRefusalNamesANextStep`; `hostconfigkeys_test.go:25`; `writeWideningEntry`.
- **Docs describing the old thing:** [`boundary-broker.md`](boundary-broker.md)
  ([§5.6](boundary-broker.md#56-the-repository-scope), [§5.7](boundary-broker.md#57-permission-sets),
  [§9.7](boundary-broker.md#97-the-delivery-copy-and-other-workspaces-widening-entries),
  [§11](boundary-broker.md#11-recommendation-and-the-first-build-slice) step 2, and the ledger rows
  the design's [§1.3](workspace-widening.md#13-what-it-changes-in-standing-records) names); `config-safety.md` (summary, P3, the workspace-scope
  invariant, the repository-scope section, drift, the files table); `loophole-system.md:792-810`;
  `pack-system.md`'s [OQ-K2](../reference/pack-system.md#why-its-this-way) paragraph (~`:2831`); `userguide/guides/github.md`, keeping its anchor;
  `writing-loopholes.md:182-200`; `configuration.md:60`; `settings-per-setup.md:238`;
  `packs/github/README.md:50-56`; `docs/plans/roadmap.md` item 43.
- **Changelog:** one `### Added` line, and one `### Changed` line with the one action.
- **Cheap and yours:** the wording of every line the design marks *the implementer's*, and the
  field names inside new structs.

## Don't

- Don't put the projection inside `SnapshotJSON`. Drift, the delivery copy and the inherited
  files share it, and one byte moves every record.
- Don't feed the scope file from `ApprovedScope` or from the plan's `Config`. Both are reads after
  the gate (design WW-P2).
- Don't add a `yolo` verb that writes the entry, or git-ignore the local file (the design's [§5](workspace-widening.md#5-non-goals)).
- Don't give `ValidateConfig` a caller mode for the old form. The launch form is the only one.

## Blockers

None. [OQ-WW1](workspace-widening.md#OQ-WW1) is open and built on its leaning; its other answer
changes the remote key function alone.
