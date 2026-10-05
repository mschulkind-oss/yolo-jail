---
title: "Workspace widening: implementation sketch"
date: 2026-10-05
status: draft
stage: SKETCH
next: "Complete against the tree with the implementation-plan skill before anyone builds from it"
tags: [plan, github, broker, config, approvals]
---

# Workspace widening: implementation sketch

**Status:** 2026-10-05. Incomplete. This collects what the design's research and review turned up.
It is not a hand-off: complete it against the tree with the `implementation-plan` skill before
building.

- [`workspace-widening.md`](workspace-widening.md) is the design, and it wins on behavior wherever
  the two disagree.
- Line numbers are from `aea26d196` and will move.

## Code sites

### Config layer (`internal/config`)

**`brokered.go`**

- **Delete the user-scope reader**: `BrokeredWidening` and `brokeredWidening` (`:70-72`).
- **Replace `validateBrokered`'s workspace-scope error** (`:290-301`) with the per-scope rules of
  the design's [§3.1](workspace-widening.md#31-the-entry). Keep its position in `ValidateConfig`'s
  append order, which is a frozen contract (`validate.go:62-64`).
- **`knownBrokeredSourceKeys`** (`:62-65`) becomes `repos`, with `workspaces` kept as retired.
- **Keep** `brokeredWorkspaceKeyProblem`, `brokeredKeyNames` and `resolvesThrough`, moving them if
  need be. The per-workspace file uses them (`workspacefile.go:268`, `:332`, comment `:48`).

**Validation rules**

- **User-scope refusal.** Copy `brokeredUserSwitchRefusal` (`validate_loopholes.go:440-469`): it
  reads user scope directly and locates the key with `UserScopeSources().AnnotateOne`.
  - **Read each scope on its own.** Validation runs on the MERGED `brokered`, so a leftover user
    `workspaces` and a workspace `repos` would combine under one source.
- **Retired-key message for `workspaces`.**
  - Follow the convention at `validate.go:223-232`: an error on the host, and a warning in a jail
    with the standard suffix.
  - The respelling precedent is `use_profiles` (`validate.go:1866-1867`). The retired-key rules are
    at `config.go:48-52`, `:139` and `:199-200`.
  - **The message is printed into `.yolo/launch.log`**, which the jail reads (`run.go:103-115`).
    So a launch prints the full edit only for its own workspace. Host `yolo check` prints them all.

**Reading the entry**

- **The new reader** takes `LoadWorkspaceConfig`, or `LoadWorkspaceConfigWithSources` for
  provenance (`load.go:341-347`; `sources.go:289` `Locations`). It must never take `LoadConfig`'s
  merged result.
  - The re-read precedent is `workspaceLoopholeEntries` and `WorkspaceLoopholeOrigins`
    (`validate_loopholes.go:524-600`).
- **Containment.** `brokered` is accepted only from files whose resolved path, symlinks followed, is
  inside the workspace.
  - `include_if_found` today refuses only `/` and `~` (`load.go:277-287`), and `readFile` follows
    symlinks (`load.go:50`).
  - The model to copy is `brokerscope/remotes.go:77-81` and `:139-143`.
  - On macOS, resolve both sides: `/var` is a symlink to `/private/var`.

**`scopeapproval.go`**

- **`ScopeSource`** (`:32-40`) carries only a remotes `Read` today. It needs the entry's
  repositories and their sources.
- **`compareScope`** (`:139-163`) compares `s.Read.Repos()` alone. It needs the sources-record
  comparison too.
- **`scopeBlock`** (`:168-208`) builds the header and rows. Its labels need `termsafe.Visible`; the
  model is the `GitConfig` field's doc at `remotes.go:36-38`.
- **`RecordApproval`** (`:223-241`) is the one writer shared by the y path, the flag and the host
  check. It also writes the new sources record.
- **`ApprovedScope`** (`:251-259`) is no longer the scope file's source, because the gate's
  in-memory `next[source]` replaces it. A concurrent macos-user session can write the record
  between the gate (`run.go:449`) and `holdLaunchLock` (`run.go:755`).

**`snapshot.go`**

- **The config part** is `SnapshotJSON(config)` at `:287`. Put the `brokered` projection BEFORE that
  call, in one helper that `scopeapproval.go:227` also uses. Never put it inside `SnapshotJSON`,
  which `drift.go:46`, `assembled.go`, `inherit.go:480` and the rest share.
- **The flag path** (`:379-404`) records silently today. It now prints the block and the count line
  first.
- **`ChangedNonInteractiveError`**: its Headline (`:147-149`) and its Advice file list (`:154-183`)
  assume git.

**Other files in this package**

- **`inherit.go:259-263`**: keep `brokered` in the "neither" class and rewrite its reason. The
  census is exhaustive by test (`:44`, `:91`). Other comments cite this reason: `load.go:381-386`
  and `workspacefile.go:55-58`.
- **`drift.go`**: no change. It keeps reporting the edit.

### Launch (`internal/cli/run`)

**The gate**

- **`checkConfigChanges`** (`preflight.go:399-405`) is the one method both backends call:
  macos-user at `run.go:449-450`, and the container fresh path at `run.go:1212-1216`.
  - `wsCfg` there is a second read, separate from the strict `cfg` at `run.go:175` and
    `preflight.go:27`.
  - Its error is dropped today (`wsCfg, _ :=`). Make it refuse instead ([WW-D18](workspace-widening.md#WW-D18)).

**`brokeredscope.go`**

- **`brokeredScopeCheck`** (`:32-51`) reads remotes only.
- **`recordApprovedScopes`** (`:54-62`) re-reads the record. Replace that with the gate's result.
- **`writeScopeFiles`** (`:66-120`): delete the `BrokeredWidening` re-read at `:89` and the
  fail-closed exception at `:72-79`, and rewrite the disclosure lines at `:106-119`. The comment at
  `:106-107` says no label carries markup; with sources, that is no longer true.

**The keeper**

- **On the container arm, the keeper writes the scope file and prints the launch line.** Approved
  scopes cross in the plan (`keeper.go:262`, `keeperplan.go:63-65`, `keeperspawn.go:224-231`).
- **The labeled result must cross there too**: each repository and its sources, beside
  `ApprovedScopes`.
- **The plan's `Config`** is the pre-gate merged config. Never read the entry or its sources from
  it.

**Printing (`preflight.go`)**

- **The scope block printer** (`:440-456`) applies only `richtext.Escape`. Add `termsafe.Visible`
  for labels.
- **The header and question** (`:520-529`). The count line goes directly above the question.
- **The decline lines** (`:559-578`) name files only when the config changed. A changed entry row
  needs them too.

### Check (`internal/cli/check`)

- **`brokeredscope.go:21-34`** is a second copy of the scope read. Share one helper with the
  launch.
- **`check.go:672-691`**, the `--accept-config-changes` path:
  - It prints the block before recording.
  - Its *"recorded"* line uses `describeRepos(s.Read.Repos())`, the remotes alone; it should list
    the union.

### Broker (`internal/ghbroker`, `internal/brokerscope`)

**`scope.go`**

- **`widenAdvice`** (`:72-94`): rewrite per the design's
  [§3.4](workspace-widening.md#34-what-the-agent-is-told).
- **`describe`** (`:67`) holds the empty-scope text. `gh auth status` prints it too
  (`daemon.go:430`).
- **`ForWorkspace`** and the key JSON-quoting become dead code.
- **`paths.UserConfigPath()`** is no longer used here.

**Elsewhere in the broker**

- **`classify.go:162-171`**: both account-wide reasons say *"widening entry"*.
- **`daemon.go:137`** unions `Repos` and `Widened`, and can stay. Keep `sf.Workspace` for the audit
  log (`:138`) and for `placementRefusal` (`:146`).
- **`brokerscope/file.go:41-48`**: rewrite the `Widened` doc comment and stop writing the field.
  Keep it decodable, and leave `FileVersion` at 1.

### Approval-record deletion

- **`capturehost.go:741-744`** deletes `<cname>.json` and `<cname>.scope.json`. It must delete the
  sources record too.
- **Find every other path** that deletes the approval record: EW-D33's rule is that every such path
  deletes each part.

### Other

- **`internal/render/configkeys.go:165-170`**: `brokered` stays not applicable at `yolo host`.
  Rewrite the reason, which cites `BrokeredWidening`.
- **`internal/loopholedecl/brokered.go:25-27` and `:43-46`**: the comments say "user config".
- **`internal/cli/gh.go:32-33`**: the `--help` text.
- **`internal/cli/config_ref.txt:374-410`**: the whole `brokered` entry.
- **`packs/github/loopholes/github-broker/manifest.jsonc:31-38`**: the comment.
- **`packs/github/briefing/gh.md`**: the new bullet goes after the exit-64 bullet.
- **`internal/jailcontent/builtinskills/configuring-the-jail/SKILL.md:42-46`**: the contrast line.

## Tests that pin today's behavior

### Rewrite

Each rewritten test should go through the call site, so it fails if the production caller is
deleted.

- **`internal/config/brokered_test.go`**:
  - `TestBrokeredWideningIsTheUserEntryForThisWorkspaceAlone` (`:22`)
  - `TestAWideningKeyThatRunsThroughTheWorkspaceNeverMatchesIt` (`:84`)
  - `TestValidateBrokeredRefusesEachBadShapeAndAWorkspaceValue` (`:129`)
  - `TestValidateBrokeredWarnsOfASourceNoSelectedPackBrokers` (`:186`)
- **`internal/cli/run/brokeredscope_test.go`**:
  - `writeWidening` (`:369`)
  - `TestAWideningEntryJoinsTheScopeFileAndIsDisclosed` (`:388`), which asserts the entry is *not*
    in the diff and *not* approved
  - `TestAWideningEntryWithNoRemoteAndNoApproval` (`:427`), which flips
- **`internal/ghbroker/daemon_test.go`**:
  `TestTheBrokerRunsAWidenedRepositoryAndNamesTheEntryForAnother` (`:476`).
- **`internal/ghbroker/api_test.go`**: `TestEveryAccountWideRefusalNamesANextStep` (`:158`).
- **`internal/cli/hostconfigkeys_test.go:25`**: the sample writes the old shape into a user config.
- **`integration/githubbroker_test.go`**:
  - `writeWideningEntry` (`:541-548`)
  - `TestGitHubBrokerAWideningEntryAdmitsARepositoryForOneWorkspace` (`:564`)
  - `recordScope` (`:129-134`), whose *"recorded"* line every broker integration test depends on

### May move

These move if the wording or the rows change:

- `scopeapproval_test.go`: `TestScopeChangeWithNoTerminalRefusesNamingTheScope`,
  `TestBothChangedNamesBoth`, `TestAPlainPrompterSeesTheBlockFirst` and
  `TestRecordApprovalWritesBothParts`
- `brokeredscope_test.go:158`
- `brokeredscope_macosuser_test.go:105`
- `daemon_test.go:147`, `:422` and `:440`
- `classify_test.go:348`
- `render/configkeys_test.go:103`

### Add

One test for each of these behaviors:

- **The briefing pin**, in `internal/cli/run/githubbriefing_test.go`.
- **Config-part bytes:** they do not move for a record without `brokered`.
- **No late edits:** an edit after the y never reaches the scope file.
- **No re-read:** a record rewritten by a concurrent session between the gate and the spawn does not
  reach this launch's broker.
- **User-scope refusal:** a user-scope `repos` is refused.
- **Source changes:** a source change asks, and so does a second source for an approved repository.
- **Upgrade:** no sources record and an unchanged set records sources without asking.
- **Escaping:** an include whose name holds an escape sequence renders escaped in the block, the
  decline lines and the launch line.
- **Containment:** a `brokered` key reached through `../` or a symlink outside the workspace is
  refused.
- **The flag path** prints the block.
- **The launch log:** the old-form refusal names no other workspace in `.yolo/launch.log`.

## Docs to rewrite in the same landing

**[`boundary-broker.md`](boundary-broker.md)**, wherever the body describes the user-scope form:

- the frontmatter `next` and `summary`;
- the Status and Cost paragraphs;
- [§1.2](boundary-broker.md#12-what-it-rules)'s [OQ-BB6](boundary-broker.md#OQ-BB6) line;
- the glossary rows for widening entry, repository scope and out of scope;
- BB-P9, including its headline;
- [§5.6](boundary-broker.md#56-the-repository-scope): the trap bullet, how the broker gets it, the
  mermaid, *Widening one workspace* and *Out of scope*;
- [§5.7](boundary-broker.md#57-permission-sets)'s scope sentence;
- [§9.2](boundary-broker.md#92-prompt-fatigue)'s *"never asks at all"*;
- [§9.7](boundary-broker.md#97-the-delivery-copy-and-other-workspaces-widening-entries), now
  historical;
- [§11](boundary-broker.md#11-recommendation-and-the-first-build-slice) step 2;
- BB-D30, BB-D31 and BB-D32, extended;
- the ledger rows already annotated *superseded*.

**`docs/reference/config-safety.md`:**

- the summary;
- P3's exception (`:68-75`);
- the gate-input invariant (`:85-90`);
- the user-config line (`:154-158`);
- the drift invariant (`:220-222`);
- the [OQ-S3](../reference/config-safety.md#oq-s3) exception.

**Other references:**

- `docs/reference/loophole-system.md:801-804`: the scope is the remotes plus the entry.
- `docs/reference/pack-system.md` [OQ-K2](../reference/pack-system.md#why-its-this-way) (around `:2831`): add this channel to the values the
  conditional protects.

**User guide:**

- `userguide/guides/github.md:61-110`: the prompt sample and the *Adding a repository* section.
  Keep its anchor, which `configuration.md:60` and `settings-per-setup.md:238` link to.
- `userguide/guides/writing-loopholes.md:186-196`: `source` also names the workspace key.
- `userguide/reference/configuration.md:60`: take `brokered` out of the user-only list.
- `userguide/reference/settings-per-setup.md:238`.

**Pack and changelog:**

- `packs/github/README.md:50-56` and `:64`.
- `CHANGELOG.md` `[Unreleased]`: a `### Changed` line (the one action) and an `### Added` line, in
  the house style.

## Traps

- **`git add` before a nested-jail check.** The image sees tracked files only.
- **A workspace `"brokered": null` passes today's validator**, so one record can hold it. Stripping
  it re-prompts that workspace once. Accepted.
- **In-jail, `LoadConfig` returns `.yolo/config-assembled.json` verbatim** (`load.go:454-457`). A
  copy an older launcher wrote still carries the user `workspaces` key, which is why the in-jail
  refusal is a warning.
- **macos-user writes neither `config-boot.json` nor `config-assembled.json`.** So
  `yolo config drift` has no baseline there.
- **Names the reviews flagged as easy to get wrong:**
  - the `.json` fallback, where creating a `.jsonc` beside it shadows the whole file;
  - the local file, which yolo does not git-ignore;
  - the attach path, which validates config, so a malformed entry refuses a new terminal.
