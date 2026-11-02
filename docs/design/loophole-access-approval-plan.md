---
title: "Plan: loophole grants, built from the accepted design"
date: 2026-11-02
status: accepted
stage: DECIDED
next: "Start wave 1: T1 (grant declarations) and T2 (the loopholegrant package) in parallel"
depends-on: [loophole-access-approval.md, readable-config.md]
tags: [loopholes, packs, config, grants, approval, plan]
summary: "Build hand-off for loophole-access-approval.md: the current tree, the design's build order cut into landable slices, the fan-out with one owner per file, and per task its tests, gates, refusal text and commit message."
---

# Plan: loophole grants, built from the accepted design

**Status:** 2026-11-02. Completed against the tree, written against `e63a87597`.
**Design:** [`loophole-access-approval.md`](loophole-access-approval.md), accepted, with every
question ruled ([LG-D12](loophole-access-approval.md#LG-D12) to
[LG-D14](loophole-access-approval.md#LG-D14)). This plan opens no design question.

## Precedence, and when to stop

1. **The design wins on behavior.** If this plan implies other behavior, this plan is wrong.
2. **The tree wins on fact, for cosmetic drift only.** A moved helper or a renamed symbol with the
   same role: follow the tree and say so in the commit and the report.
3. **This plan is advice and is the first thing to be wrong.** Never twist code to match it.

**Stop and report, and do not commit,** when:

- the first test does not fail the way the task predicts;
- a file the task names is dirty with someone else's work;
- a symbol the task names is gone;
- the change needs a file outside the task's fence (the [ownership tables](#fan-out-and-file-ownership));
- a gate fails outside the task's scope.

## Words used here

The design's four words (selected, running, granted, configured) and its **standing**, **grant
kind**, **valued kind**, **ceiling**, **request**, **requestable by default**, **mediated** and
**unmediated** are defined in the design's [§1](loophole-access-approval.md#1-the-verdict-in-four-words),
[§4.2](loophole-access-approval.md#42-the-approval-in-two-layers),
[§4.4](loophole-access-approval.md#44-mediated-and-unmediated-crossings) and
[§4.6](loophole-access-approval.md#46-how-a-grant-reaches-the-machinery). Terms coined in this plan:

- **Grant file** *(coined in the design, [§4.6](loophole-access-approval.md#46-how-a-grant-reaches-the-machinery))*: here, concretely, the per-launch private settings
  snapshot that `loopholes.WritePrivateSettingsSnapshot` already writes, with a reserved `grants`
  key added ([PD-2](#PD-2)).
- **Grant-declaring loophole** *(coined here)*: a loophole whose manifest has at least one
  `[[grants]]` entry ([PD-1](#PD-1)).
- **Flip** *(coined here)*: the slice that gives one shipped loophole its `[[grants]]` and deletes
  its daemon's old, ungranted code path. Before its flip a loophole behaves exactly as today.
- **Awaiting set** *(coined here)*: a temporary list in a test naming the shipped loopholes not yet
  flipped; each flip removes its name and T13 deletes the empty list.
- **Legacy projection** *(coined here)*: the read-time function that turns a legacy JSONC
  `loopholes.<name>` block into the ceiling, request and record lines the migration would write
  ([PD-9](#PD-9)).

## Plan decisions

One-answer mechanism choices under rulings already made. They are this plan's, not the design's;
a builder does not re-open them.

| Id | Decision | Rests on |
| :--- | :--- | :--- |
| <a id="PD-1"></a>PD-1 | **Manifest spelling.** A top-level `[[grants]]` array in `loophole.toml`, entries `kind` (matches `loopholedecl.ValidGrantKind`), `text` (the gate's line, e.g. `"read your user journal"`), `valued` (bool, default false), `requestable_by_default` (bool, default false; refused on a valued kind), `value_noun` (valued kinds only, e.g. `"process names"`, used in refusals), and the migration keys `legacy_enable` (bool: the kind a legacy `enabled: true` becomes; at most one per manifest), `legacy_setting` (the legacy setting whose `true` becomes this kind, `"full"` → `read_all`) and `legacy_values` (the legacy setting whose list becomes this kind's value, `"visible"`, `"allowed_devices"`). Plus one top-level string `access` (e.g. `"read the host's journal"`), which every refusal and the audio launch line print. Kinds unique per manifest; a manifest with `[brokered]` and `[[grants]]` is refused; a declared setting named `grants` is refused. | [LG-D3](loophole-access-approval.md#LG-D3), [LG-D4](loophole-access-approval.md#LG-D4), [LG-D14](loophole-access-approval.md#LG-D14); the design left the spelling to the plan ([§5](loophole-access-approval.md#5-every-stop-and-its-next-step)'s `default_enabled` text) |
| <a id="PD-2"></a>PD-2 | **The grant file is the private settings snapshot.** Its flat settings keys stay as today; one reserved key `grants` carries `{"pack", "workspace" (host path), "container", "launch_id", "pid", "kinds": {"<kind>": {"standing", "values"?, "text"}}}`. Every grant-declaring loophole gets a snapshot even with zero settings, so the `{settings}` token is legal on a manifest that declares `[[grants]]` and no settings. Live apply finds files by scanning the loophole's state dir and matching workspace, container and a live pid, as `brokerscope.LiveFiles` does. | [LG-D7](loophole-access-approval.md#LG-D7) |
| <a id="PD-3"></a>PD-3 | **One new package, `internal/loopholegrant`**, owns the standing type, the file's read, write and live-replace, and the refusal renderer. Daemons import it; nothing else parses the `grants` key. | LG-D7 |
| <a id="PD-4"></a>PD-4 | **The approval record gains one part**, `<cname>.loophole-grants.json`, listing per pack the recorded grants as `kind` or `kind=value`, added to `config.ApprovalRecordFiles`. Loophole grant sources ride on `config.ScopeCheck` as a new field beside `Sources`, so there is one gate and one record writer. | [LG-D12](loophole-access-approval.md#LG-D12), [BB-D107](boundary-broker.md#BB-D107) |
| <a id="PD-5"></a>PD-5 | **Gate lines** use `grantBlock`'s existing spelling. A skipped request is a gate line only when the gate asks anyway; otherwise it prints in the launch's pack disclosure block. A line cites `file:line` only where provenance carries a line. | design [§4.2](loophole-access-approval.md#42-the-approval-in-two-layers) item 4 |
| <a id="PD-6"></a>PD-6 | **Schema:** one generic `requests.*.*` row of type Any; the resolver types it from the declarations, as `allow.*.*` already works. | readable-config schema |
| <a id="PD-7"></a>PD-7 | **A grant refusal exits 64**, the broker and `gh` precedent. | brokers |
| <a id="PD-8"></a>PD-8 | **Inert reaches the client by environment.** For each selected loophole inert on this setup, the launch sets `YOLO_LOOPHOLE_INERT_<NAME>` (upper snake case) to the inert note's reason, and the client prints [§5](loophole-access-approval.md#5-every-stop-and-its-next-step)'s inert line from it before trying the endpoint. | [LG-D10](loophole-access-approval.md#LG-D10) |
| <a id="PD-9"></a>PD-9 | **Legacy JSONC in the window** is read through the legacy projection, the same function T12's migration rows call: user `enabled: true` → `always` for the `legacy_enable` kind (`on_request` for a valued one); user `settings.full: true` → `read_all` joins it; workspace `enabled: true` → a request; switch file `enabled: true` → a record-equivalent grant; `visible` and `allowed_devices` → request values. | [LG-D11](loophole-access-approval.md#LG-D11), design [§6](loophole-access-approval.md#6-migration) |
| <a id="PD-10"></a>PD-10 | **Pending** is computed where the request is read: requested, in the ceiling, not in the record. A declined gate rewrites standings only. A request written in the jail after launch shows as `requestable` until the next launch or `yolo config apply`; `yolo request` already prints the host step, so the client adds no file read. | design [§4.6](loophole-access-approval.md#46-how-a-grant-reaches-the-machinery) |
| <a id="PD-11"></a>PD-11 | **Journal and macos-log modes:** `read_all` granted → full mode; else `read` granted → user mode; else refuse naming `read`. | design [§4.3](loophole-access-approval.md#43-each-loopholes-access-and-its-approval) |
| <a id="PD-12"></a>PD-12 | **Transitional daemon path.** Until its flip, each daemon enforces the `grants` key when present and keeps today's behavior when absent. Each flip deletes the absent path, which then fails closed with the `not_allowed` text. | sequencing |
| <a id="PD-13"></a>PD-13 | **Default selection of `cgroup-delegate`** is a `pack.toml` key `selected_by_default = true`, honored for embedded packs only, appended in `config.LoadPacks` beside the implicit local pack entry; `warnIfNoPacks` does not count it. Its per-workspace `off` half lands when L4's `off` lands ([LG-D9](loophole-access-approval.md#LG-D9)); T15 pins the selection half. | [LG-D13](loophole-access-approval.md#LG-D13) |
| <a id="PD-14"></a>PD-14 | **The end-to-end journal tests skip** with a reason when the test host has no readable user journal (`journalctl --user -n 1` fails), which is true in a nested jail; CI's Linux runner has one. | design [§11](loophole-access-approval.md#11-tests-that-pin-it) |

## Map

| Path | Change | Task |
| :--- | :--- | :--- |
| `internal/loopholedecl/grants.go` | new: `Grant` decl, `parseGrants`, `access` | T1 |
| `internal/loopholedecl/loopholedecl.go`, `keys.go`, `tokens.go`, `settings.go` | `Manifest.Grants`, `Manifest.Access`; key census; `{settings}` legal with grants; `grants` setting name refused | T1 |
| `internal/loopholedecl/defaultenabled.go` or the decoder | `default_enabled` refused; `scope = "workspace"` refused | T13 |
| `internal/loopholegrant/` | new package: standing, file, live, refusal text | T2 |
| `internal/config/loopholegrants.go` | new: ceiling and request reader for grant-declaring packs, standing per kind | T3 |
| `internal/config/readableallow.go` | accept `always`/`on_request` and request kinds for grant-declaring packs | T3 |
| `internal/config/schema.go` | `requests.*.*` row | T3 |
| `internal/config/loopholegrantlegacy.go` | new: legacy projection | T10 |
| `internal/config/validate_loopholes.go`, `renames.go` | legacy validator accepts what the projection reads | T10 |
| `internal/config/scopeapproval.go`, `snapshot.go`, `gateread.go` | grant sources on `ScopeCheck`, gate rows, record part | T4 |
| `internal/cli/run/brokeredscope.go`, `internal/cli/check/brokeredscope.go`, `internal/cli/run/preflight.go` | one shared declaration collector feeding both | T4 |
| `internal/cli/allow.go`, `internal/cli/request.go` | loophole kinds | T5 |
| `internal/loopholes/load.go`, `discover.go`, `runtime.go`, `settings.go` | grant-declaring loopholes run on selection; `Set.WithGrants`; unmediated argv gate | T6 |
| `internal/cli/run/loopholesruntime.go`, `loopholesettings.go`, `assemble_parts.go`, `prepare.go` | grant file written; disclosure lines; inert env; briefing standing | T6 |
| `internal/jailcontent/briefing.go` | `Loophole` gains a standing line | T6 |
| `internal/journald/`, `cmd/yolo-journalctl/`, `cmd/yolo-log/` | grant enforcement, refusal | T7, then F1 and F2 |
| `internal/hostprocesses/`, `cmd/yolo-ps/` | same | T8, then F3 |
| `internal/serialdaemon/`, `cmd/yolo-serial/` | same | T9, then F4 |
| `packs/{journal,macos-log,host-processes,serial,audio}/loopholes/*/loophole.toml` | `[[grants]]`, `access`; settings removed or moved | F1 to F5 |
| `internal/cli/run/liveapply.go`, `liveapplyplan.go`, `internal/changetiming/` | grant file rewrite; restart outcome for audio | T11 |
| `internal/configmigrate/loweruser.go`, `plan.go`, new `loopholegrants.go` | migration rows and the standing equality layer | T12 |
| `internal/loopholes/loopholescmd.go` | `enable`/`disable` refuse; `list`/`status` show standing | T14 |
| `packs/cgroup-delegate/pack.toml`, `internal/config/packs.go`, `internal/cli/run/run.go` | default selection | T15 |
| `internal/cli/config_ref.txt`, `userguide/guides/loopholes.md`, `docs/reference/loophole-system.md`, `docs/reference/pack-system.md`, `packs/*/README.md`, `AGENTS.md` | docs | T16 (T3 may add one config-ref line) |

## Reuse before you write

- **The broker grant path is the template for everything here.** `loopholedecl.parseBrokered` and
  `Brokered.Grants` for the decl shape; `config.ScopeSource` (`Grants`, `UserGrants`) and
  `grantBlock` for gate lines; `config.RecordApproval` and `writeScopeRecords` for the record;
  `internal/brokerscope` `File`, `NewLaunchID`, `Write`, `ReadFile`, `Sweep`, `LiveFiles` and
  `ReplaceScopeContext` for the file and its live rewrite. Mirror them; do not invent a second shape.
- **`loopholes.WritePrivateSettingsSnapshot`** already writes a private, launch-scoped file in the
  loophole's state dir, and the external-service start already substitutes it for `{settings}`
  (`o.settingsSnapshots`). The grant file is that file with one more key ([PD-2](#PD-2)).
- **`config.ResolveReadableAllowance`** already parses `allow.<pack>` and `requests.<pack>` for
  non-broker packs and errors on undeclared keys; extend it, do not add a parallel reader.
  `ProjectReadableLegacyPack` and `ReadableLegacyPackState` are unused today and are the starting
  point for the legacy projection.
- **`loopholes.InertNote.Line` and `PlatformInertNotes`** carry the inert wording [§5](loophole-access-approval.md#5-every-stop-and-its-next-step) requires.
- **`loopholes.admitsJailSideEffects` and `gateAdmitsCrossing`** are where the unmediated gate goes;
  `Set.WithCredentialView` is the shape for `Set.WithGrants`.
- **Fixtures:** `writeModule(t, name, files)` and `probeTOML` in
  `internal/loopholedecl/tomlform_test.go`; `readableMap` in `internal/config/readableallow_test.go`;
  `githubScope` in `internal/config/scopeapproval_test.go`; `gbSwitched` in
  `internal/cli/check/brokeredscope_test.go`; `integration/serialfixture_test.go` for serial.
- **Hint check:** `TestEveryHintedYoloCommandExists` in `internal/cli/hintcommands_test.go` already
  resolves every `yolo …` in message text; new refusal strings are covered by adding them where it
  collects.

## House style that is not obvious

- Errors from manifest decoding go through `loopholedecl.Errorf` and start with the manifest path.
- Every refusal ends with the next step ([`happy-path-principle.md`](../reference/happy-path-principle.md));
  copy [§5](loophole-access-approval.md#5-every-stop-and-its-next-step)'s text verbatim, filling only the noun, the kind and `<dir>`.
- `internal/cli/run` methods hang off `*Options`; output goes through the launch stream, which is
  teed to `.yolo/launch.log`. There is no quiet path for a disclosure line.
- Commits use conventional messages; no `--no-verify`; `just format` before committing.

## Traps

- **Two scope collectors.** `run.(*Options).brokeredScopeCheck` and
  `check.brokeredScopeForCheck` build the same `ScopeCheck` separately. A grant source added to one
  only makes `yolo check` and the launch disagree. T4 extracts one collector both call.
- **`prepareLoopholeSettings` skips a loophole with no settings** (`len(lp.Settings) == 0`). After
  F1 removes `full`, the journal has none; without the T6 change it gets no grant file and fails
  closed forever.
- **`{settings}` without settings is refused** by `refuseSettingsTokenWithoutDeclaration`. T1 must
  count grants as a declaration, or F1's manifest stops loading.
- **The settings file never crosses into the jail** (`refuseSettingsFileCrossingIntoTheJail`); keep
  the grant file in the same state dir. The client learns the standing from the daemon's stderr,
  never from a file.
- **`loopholes.load.resolve` seeds `Enabled`** from `DefaultEnabled || Brokered != nil`, and
  `SwitchedByConfig` lets config turn it off. Grant-declaring loopholes must join the broker branch
  in both, or a TOML user's journal stays off after F1 ([the in-flight seam](#in-flight-work-and-its-seams)).
- **The argv is golden-tested** (`loopholesRuntimeArgs`). An unmediated gate change rewrites goldens;
  regenerate only for the audio cases, and say so in the commit.
- **Apple Container admits only `HostServiceAdmittedOn`.** A grant source for a loophole not
  admitted there is inert and must stay out of the gate ([LG-D10](loophole-access-approval.md#LG-D10)).
- **The jail exports `YOLO_ALLOW_MISSING_PROGRAMS=1`**, which fails some `internal/cli` tests.
  Run gates with it unset.
- **A bare `rg` from `/workspace` hangs** over the bind-mounted home and store. Always pass paths.
- **Integration tests that read legacy JSON** (`integration/cgroup_test.go`, the `macosuser*`
  tests) write `"loopholes": {X: {"enabled": true, ...}}`. Each flip rewrites the ones naming its
  loophole to TOML `requests` plus an `allow` and an accepted gate; T15 rewrites the cgroup one.

## In-flight work and its seams

Three changes are being built now. Treat each as landed before the task that names it starts; if
it has not, that is a stop-and-report precondition failure.

- **The aws-auth fix** makes a TOML-selected aws-auth run on selection, delivers TOML `allow.<pack>`
  settings to `suppliedSettings`, and adds a "which loopholes run, with which settings" layer to the
  `configmigrate` equality proof. It leaves journal, host-processes, serial, audio, macos-log and
  hello-daemon off. **Seams:** T6 builds on its settings delivery; T12 extends its equality layer
  with standings rather than adding a second one.
- **`yolo config migrate FILE`** (`internal/configmigrate`, `internal/cli`). **Seam:** T12 adds rows
  to the same planner.
- **Provenance carrying the source format** (`internal/config` sources, assembled config, launch
  origins). **Seam:** T3 and T4 cite `file:line` only through it ([PD-5](#PD-5)).

## Fan-out and file ownership

Each task is one worktree and one commit, landed serially onto `main`, each green on
`just check-ci` alone. Builders merge `main` into their worktree before starting, since agent
worktrees branch from the session's start commit.

```text
wave 1   T1 decl ─────────┐          T2 loopholegrant ─────────────┐
wave 2   T3 ceiling/requests (after T1)   T7 journal+macos-log, T8 host-processes, T9 serial (after T2)
wave 3   T4 gate + record (after T3)      T5 allow/request verbs (after T3)
wave 4   T6 launch core (after T2, T4)    T10 legacy projection (after T4)
wave 5   F1 journal → F2 macos-log → F3 host-processes → F4 serial → F5 audio   (serial chain,
         F1 after T6, T7, T10; each Fn after Fn-1 and its daemon task)
         T11 live apply (after T6), parallel with the chain
wave 6   T12 migration rows (after F5 and in-flight migrate)   T13 retire default_enabled (after F5)
         T14 retire enable/disable (after F5)
wave 7   T15 cgroup default selection (after T13)   T16 docs (after T12–T15)
```

**What unblocks the owner's journal:** T1, T2, T3, T4, T5, T6, T7, T10 and F1. That is the critical
path; everything else can land after the journal works. **Release blocker:** no release between F1
and T12, or a legacy user's switch-file access is lost at the release; T10 covers the window only
for files that are still read.

### Wave 1 ownership

| Task | Owns (only it edits) |
| :--- | :--- |
| T1 | `internal/loopholedecl/**` (non-test and test), `internal/loopholes/shipped_ungated_test.go` (new), the one `Grants` copy line in `internal/loopholes/load.go`'s `resolve` and the `Loophole.Grants` field in `internal/loopholes/loopholes.go` |
| T2 | `internal/loopholegrant/**` (new) |

### Wave 2 ownership

| Task | Owns |
| :--- | :--- |
| T3 | `internal/config/loopholegrants.go` (new) and its test, `internal/config/readableallow.go`, `internal/config/readableallow_test.go`, `internal/config/schema.go`, `internal/config/validate.go` hunks for the valued-under-always refusal, `internal/cli/config_ref.txt` (one row, if a test demands it) |
| T7 | `internal/journald/**`, `cmd/yolo-journalctl/**`, `cmd/yolo-log/**` |
| T8 | `internal/hostprocesses/**`, `cmd/yolo-ps/**` |
| T9 | `internal/serialdaemon/**`, `cmd/yolo-serial/**` |

### Wave 3 ownership

| Task | Owns |
| :--- | :--- |
| T4 | `internal/config/scopeapproval.go`, `snapshot.go`, `gateread.go` and their tests; `internal/cli/run/brokeredscope.go`, `preflight.go`; `internal/cli/check/brokeredscope*.go`; new `internal/cli/scopecollect/` or equivalent shared collector |
| T5 | `internal/cli/allow.go`, `internal/cli/request.go` and their tests |

### Wave 4 ownership

| Task | Owns |
| :--- | :--- |
| T6 | `internal/loopholes/load.go`, `discover.go`, `runtime.go`, `settings.go` and their tests; `internal/cli/run/loopholesruntime.go`, `loopholesettings.go`, `assemble_parts.go`, `prepare.go` and their tests and goldens; `internal/jailcontent/briefing.go` |
| T10 | `internal/config/loopholegrantlegacy.go` (new) and test, `internal/config/validate_loopholes.go`, `validate_loopholesettings.go`, `renames.go`, `workspacefile.go` (read-only use; edit only if the reader needs an export) |

### Wave 5 ownership

The chain is serial, so a flip may edit any shared test that names its loophole, plus its manifest,
its README, its daemon package's fallback and its integration tests. T11 owns
`internal/cli/run/liveapply*.go` and `internal/changetiming/**`; a flip that breaks a test in those
files stops and reports rather than editing it.

### Wave 6 ownership

| Task | Owns |
| :--- | :--- |
| T12 | `internal/configmigrate/**`, the `yolo check` record-grant report in `internal/cli/check/` (a new file) |
| T13 | `internal/loopholedecl/**`, every `packs/*/loopholes/*/loophole.toml` (removing `default_enabled` only), `internal/loopholes/load.go`, `discover.go`, `inertreport.go`, and their tests |
| T14 | `internal/loopholes/loopholescmd.go`, `internal/cli/loopholesenable_test.go`, the `loopholes` CLI help text |

### Wave 7 ownership

| Task | Owns |
| :--- | :--- |
| T15 | `packs/cgroup-delegate/pack.toml`, `internal/config/packs.go`, `packselection.go`, `internal/cli/run/run.go` (`warnIfNoPacks` only), `integration/cgroup_test.go` |
| T16 | every doc in the map's last row |

## Tasks

Gates for every task, run with `YOLO_ALLOW_MISSING_PROGRAMS` unset: the targeted `go test` named
in the task while working, then `just format` and `just check-ci` before committing. Integration
tests: `rg -l '<symbol>' integration/` for every symbol, flag or message the task changes, then
`go test -count=1 -run '<those tests>' ./integration`. A task that changes the launch path (T6,
each flip, T15) runs the full `go test -count=1 -timeout 0 ./integration` once before landing.

### T1. Grant declarations in the manifest (independent)

- **Settled, do not re-open:** a manifest declares its grant kinds, each valued or not, with one
  line of text; a valued kind is never requestable by default; a loophole declaring none is
  ungated; the shipped ungated set is `claude-oauth-broker`, `openai-auth-broker`, `aws-auth`,
  `cgroup-delegate`, and `hello-daemon` crosses nothing. Spelling per [PD-1](#PD-1).
- **Tests first** (`internal/loopholedecl/grants_test.go`), each through `Decode` on a
  `writeModule` fixture:
  - `TestGrantsDeclareKindsTextAndValued`: a manifest with
    ```toml
    access = "read the host's journal"
    [[grants]]
    kind = "read"
    text = "read your user journal"
    requestable_by_default = true
    legacy_enable = true
    [[grants]]
    kind = "read_all"
    text = "read the whole host journal"
    legacy_setting = "full"
    ```
    decodes to two `Grant`s with those fields and `Access` set.
  - `TestAValuedKindRequestableByDefaultIsRefused`: `kind = "view"`, `valued = true`,
    `requestable_by_default = true` → error naming `requestable_by_default` and `valued`.
  - `TestGrantsBesideBrokeredIsRefused`, `TestDuplicateGrantKindIsRefused`,
    `TestTwoLegacyEnableKindsAreRefused`, `TestASettingNamedGrantsIsRefused`.
  - `TestSettingsTokenIsLegalWithGrantsAndNoSettings`: `host_daemon.cmd` naming `{settings}` with
    `[[grants]]` and no `[settings]` decodes.
  - `TestNoShippedLoopholeIsUngatedWithoutAReason` (new file
    `internal/loopholes/shipped_ungated_test.go`, so it reads the embedded packs): every shipped
    manifest declares grants, or is in a reasons map of the four ungated names, or declares no host
    daemon, bind, device or intercept; plus a temporary awaiting set `{journal, macos-log,
    host-processes, serial, audio}` whose comment says each flip removes its name.
  - Red: `go test ./internal/loopholedecl -run 'Grant|SettingsTokenIsLegal' ; go test ./internal/loopholes -run Ungated`.
- **Change:** `internal/loopholedecl/grants.go` (new) with `type Grant struct` and `parseGrants`,
  mirroring `parseBrokered`; `Manifest.Grants []Grant` and `Manifest.Access string`;
  `keys.go` census; `refuseSettingsTokenWithoutDeclaration` counts grants; the setting-name refusal
  in `settings.go`. Expose `Manifest.Grants` through `loopholes.Loophole` only if T6 needs it; it
  does, so add `Loophole.Grants` and its copy in `resolve` (T1 owns those two lines; T6 owns the
  files after).
- **Fences:** no shipped manifest changes; no behavior change.
- **Commit:** `feat(loopholedecl): declare loophole grant kinds under [[grants]]`
- **Report:** the red output, the green gates.

### T2. The `loopholegrant` package (independent)

- **Settled:** four standings, `granted`, `requestable`, `pending`, `not_allowed`; one writer; the
  daemon reads only the file; refusal text is [§5](loophole-access-approval.md#5-every-stop-and-its-next-step)'s.
- **Contract other tasks match** (signatures only):
  ```go
  type Standing string // "granted" | "requestable" | "pending" | "not_allowed"
  type Kind struct { Standing Standing; Values []string; Text string }
  type Block struct { Pack, Workspace, Container, LaunchID string; PID int; Kinds map[string]Kind }
  func Read(path string) (settings map[string]any, grants *Block, err error) // grants nil when the key is absent
  func Merge(settings []byte, b Block) ([]byte, error)                       // adds the reserved key
  func LiveFiles(ctx context.Context, stateDir, container, workspace string) ([]LiveFile, error)
  func Replace(ctx context.Context, path string, expect Block, next Block) error
  type Refusal struct { Client, Pack, Access, Kind, ValueNoun, Dir string; Values []string; Standing Standing; Inert string }
  func (r Refusal) Text() string
  ```
- **Tests first** (`internal/loopholegrant/*_test.go`):
  - `TestReadReturnsSettingsAndGrants`, `TestReadWithoutGrantsKeyReturnsNilBlock`,
    `TestReadRefusesAMalformedStanding`.
  - `TestLiveFilesMatchesWorkspaceContainerAndLivePid` (copy `brokerscope`'s live test shape).
  - `TestRefusalTextRequestable`: `Refusal{Client: "yolo-journalctl", Pack: "journal", Access:
    "read the host's journal", Kind: "read", Standing: "requestable"}.Text()` equals exactly
    ```text
    yolo-journalctl: this project holds no grant to read the host's journal, so nothing ran.
      The journal pack is selected and its service is running; it is waiting on a grant.
      Ask for this project:   yolo request journal read
      then the human runs, on the host, in this workspace:  yolo config apply   and answers y.
    ```
  - `TestRefusalTextNotAllowed`: `Client "yolo-ps"`, `Pack "host-processes"`, `Access "see host
    processes"`, `Kind "view"`, `Values {"postgres","redis-server"}`, `Dir "/home/u/proj"` →
    [§5](loophole-access-approval.md#5-every-stop-and-its-next-step)'s `not_allowed` block verbatim, with `<dir>` filled.
  - `TestRefusalTextPending`: `yolo-serial`, `devices`, values `{"/dev/ttyUSB*"}` → [§5](loophole-access-approval.md#5-every-stop-and-its-next-step)'s pending
    block verbatim.
  - `TestRefusalTextInert`: `Inert: "macOS has no systemd journal"` → [§5](loophole-access-approval.md#5-every-stop-and-its-next-step)'s inert line verbatim.
  - Red: `go test ./internal/loopholegrant`.
- **Change:** new package only. A valued kind with no values in a `not_allowed` refusal prints
  `<value_noun>` as the placeholder (`yolo request serial devices <device globs>`).
- **Fences:** imports nothing from `internal/cli`.
- **Commit:** `feat(loopholegrant): the grant file, its standings and the refusal text`

### T3. Ceiling and request readers for grant-declaring packs (after T1)

- **Settled:** `[allow.<pack>]` `always` lists kinds every project gets, `on_request` the kinds a
  project may ask for, any other key a declared setting; a valued kind only in `on_request`; absent
  list means nothing, except a kind requestable by default, which sits in `on_request` unless the
  user wrote an `on_request` list (an empty list withdraws it). `[requests.<pack>]`: `<kind> = true`
  or `<kind> = [values]`. A request outside the ceiling is skipped and named.
- **Tests first:**
  - `TestARequestOutsideTheCeilingIsSkippedAndNamed` (`internal/cli/check/`, through the check
    runner): project `requests.journal.read_all = true`, user config with no `allow.journal`.
    Expect the output to contain the shape of [§5](loophole-access-approval.md#5-every-stop-and-its-next-step)'s `yolo check` text with `read_all` and the line
    `yolo allow journal on_request read_all`.
  - `TestARequestableByDefaultKindNeedsNoCeilingLine` (`internal/config/loopholegrants_test.go`):
    `requests.journal.read = true`, no `allow.journal` → standing `requestable` resolved to a gate
    candidate; with `allow.journal.on_request = []` → skipped.
  - `TestAValuedKindUnderAlwaysIsRefused`, through the TOML validator: `allow.serial.always =
    ["devices"]` → [§5](loophole-access-approval.md#5-every-stop-and-its-next-step)'s text exactly:
    ```text
    ~/.config/yolo-jail/config.toml:9: allow.serial.always lists devices, which carries a value only a project can give.
      Move it:  yolo config unset allow.serial.always devices   then  yolo allow serial on_request devices
    ```
    (line number from the fixture's provenance).
  - Use a fixture manifest with `[[grants]]` (T1's `writeModule`), not a shipped pack: no shipped
    pack declares grants until F1.
  - Red: `go test ./internal/config -run 'Requestable|ValuedKind' ; go test ./internal/cli/check -run OutsideTheCeiling`.
- **Change:** `internal/config/loopholegrants.go` (new): from the selected packs' `Grants`, the
  ceiling and the request, compute per kind its standing *before* the record (granted is T4's,
  once the record is read); `ResolveReadableAllowance` accepts `always`/`on_request` for a
  grant-declaring pack; `schema.go` gains `requests.*.*`.
- **Fences:** no gate or record change (T4); no CLI verb change (T5).
- **Commit:** `feat(config): read the ceiling and requests for loophole grant kinds`

### T4. Gate rows and the record's grant part (after T3)

- **Settled:** at every fresh launch and at `yolo config apply`, the gate shows one line per grant
  it would hold and one per request it skips, in the broker's spelling, e.g.
  `+ journal: read your user journal (from your user config, for every project)`; a y records the
  grants; N records nothing; an `always` line shows once per project and again only when the
  ceiling changes; a withdrawn grant shows removed once ([LG-D12](loophole-access-approval.md#LG-D12));
  an inert loophole's lines stay out ([LG-D10](loophole-access-approval.md#LG-D10)).
- **Tests first:**
  - `TestAnAlwaysGrantShowsOnceAtEachProjectsGate` (`internal/config/`, through
    `CheckConfigAndScopeChanges`): user `allow.<fixture>.always = ["read"]`; first call reports the
    line; after `ApproveConfigAndScope` a second call reports nothing; removing the ceiling line
    reports it removed once, then nothing.
  - `TestTheGrantPartJoinsTheApprovalRecord`: `ApprovalRecordFiles(cname)` includes
    `<cname>.loophole-grants.json`, and an approval writes `{"<pack>": ["read"]}`.
  - `TestAnInertLoopholesRequestStaysOutOfTheGate`: a `container` runtime fixture with
    `requests.<fixture>.read = true` → no gate line; `yolo check` names it inert.
  - `TestCheckAndLaunchCollectTheSameGrantSources`: both callers go through the shared collector
    and produce the same `ScopeCheck` for one fixture (fails if either stops calling it).
  - Red: `go test ./internal/config -run 'AlwaysGrant|GrantPart' ; go test ./internal/cli/check ./internal/cli/run -run 'Inert|SameGrantSources'`.
- **Change:** `ScopeCheck` gains loophole grant sources; `inPlay` counts them; `grantBlock`
  renders them; `writeScopeRecords` writes the new part; the run and check collectors call one
  function. `preflight` hands the approved set on so T6 can write the file.
- **Fences:** no file writing for daemons (T6); no live apply (T11).
- **Commit:** `feat(config): show loophole grants at the config-change gate and record them`

### T5. `yolo allow` and `yolo request` for loophole kinds (after T3, parallel with T4)

- **Settled:** `yolo allow <pack> [always|on_request] <kind>` writes the ceiling and offers the
  approval; `yolo request <pack> <kind> [value...]` writes the project file from inside the jail
  and prints the host step; a valued kind under `always` is refused as T3's text says.
- **Tests first** (`internal/cli/`): `TestAllowWritesALoopholeKind` (`yolo allow host-processes
  on_request view` writes `[allow.host-processes] on_request = ["view"]` on a fixture pack);
  `TestRequestWritesALoopholeKindWithValues` (`yolo request host-processes view postgres
  redis-server` writes `view = ["postgres", "redis-server"]` and prints `yolo config apply`);
  `TestRequestRefusesAnUndeclaredKind`. Extend the collection in
  `TestEveryHintedYoloCommandExists` with T2's refusal strings. Red: `go test ./internal/cli -run 'AllowWrites|RequestWrites|RequestRefuses'`.
- **Change:** `findDeclaredBroker` gains a sibling that finds a grant-declaring pack; `allowUsage`
  and `requestUsage` name loophole kinds.
- **Commit:** `feat(cli): yolo allow and yolo request for loophole grant kinds`

### T6. The launch writes the grant file and gates unmediated crossings (after T2 and T4)

- **Preconditions:** the aws-auth fix has landed (`suppliedSettings` reads TOML `allow.<pack>`
  settings). If not, stop and report.
- **Settled:** a grant-declaring loophole runs whenever its pack is selected and the setup can run
  it ([LG-D2](loophole-access-approval.md#LG-D2)); the launch writes the grant file after the y and
  before the spawn; an unmediated crossing (binds, devices, sockets) is added only under a grant
  ([LG-D6](loophole-access-approval.md#LG-D6)); a selected loophole not granted gets the launch
  disclosure line and a briefing line; inert loopholes get [PD-8](#PD-8)'s env.
- **Tests first:**
  - `TestASelectedLoopholePackRunsItsServiceAndGrantsNothing` (`internal/cli/run/`): a fixture
    grant-declaring pack selected with no `allow`; `startLoopholesMatching` spawns it, and the
    grant file it was handed reads (through `loopholegrant.Read`) with every kind `requestable` or
    `not_allowed`, none `granted`.
  - `TestAnUnmediatedCrossingIsAddedOnlyUnderAGrant` (`internal/loopholes/`): a fixture shaped like
    audio (`host_devices`, `host_bind_mounts`, one kind `use`); `RuntimeArgsFor` through a
    `Set.WithGrants` with no grant yields no `/dev/snd` and no bind; with `use` granted yields all.
  - `TestTheLaunchDisclosesAnUngrantedUnmediatedLoophole`: the disclosure block contains
    ```text
      audio: selected, grants nothing here (not allowed on request by your config).
        To let this project ask:  yolo allow audio on_request use   then  yolo request audio use
    ```
    for the fixture named `audio`.
  - `TestAGrantFileIsWrittenForAGrantDeclaringLoopholeWithNoSettings`.
  - `TestTheBriefingStatesEachLoopholesStanding` (through `briefingLoopholes`).
  - Red: `go test ./internal/cli/run ./internal/loopholes -run 'SelectedLoopholePack|UnmediatedCrossing|Discloses|NoSettings|BriefingStates'`.
- **Change:** `resolve` seeds `Enabled` true for a grant-declaring loophole and `SwitchedByConfig`
  excludes it; `prepareLoopholeSettings` no longer skips one with no settings; the snapshot gets
  `loopholegrant.Merge`; `admitsJailSideEffects` consults `Set.WithGrants`; the disclosure and
  `jailcontent.Loophole` gain the standing; [PD-8](#PD-8)'s env.
- **Integration:** `go test -count=1 -timeout 0 ./integration` once (launch path). No shipped pack
  changes behavior yet; any change in a shipped golden is a stop-and-report.
- **Commit:** `feat(run): run grant-declaring loopholes on selection and hand them a grant file`

### T7, T8, T9. Daemons and clients enforce grants (after T2; parallel with each other and with T3 to T6)

One task per row; each is its own worktree.

| Task | Daemon | Client | Kinds | Rule |
| :--- | :--- | :--- | :--- | :--- |
| T7 | `internal/journald` (`LoadSettings`, journald and macos-log commands) | `cmd/yolo-journalctl`, `cmd/yolo-log` | `read`, `read_all` | [PD-11](#PD-11) |
| T8 | `internal/hostprocesses` (`LoadSettings`, `BuildHandler`) | `cmd/yolo-ps` | `view` (values = names) | a name not in the granted values is invisible; `fields` read from settings |
| T9 | `internal/serialdaemon` (`LoadSettings`, `BuildHandler`, `handleList`) | `cmd/yolo-serial` | `devices` (values = globs) | `list` and `open` see only granted globs; no defaults |

- **Settled:** the daemon reads only the grant file and refuses with T2's text over its own
  transport (stderr, exit 64); absent `grants` key keeps today's behavior until the flip
  ([PD-12](#PD-12)); the client prints the daemon's text and exits with its code, and prints the
  inert line when [PD-8](#PD-8)'s env is set.
- **Tests first:** `TestEachShippedDaemonReadsOnlyItsGrantFile` split per package as
  `Test<Daemon>ReadsOnlyItsGrantFile`: `HOME` and `XDG_CONFIG_HOME` pointed at empty temp dirs, a
  grant file present with the kind `requestable`; a request through the handler returns exit 64 and
  T2's requestable text; with the kind `granted` it is served. Plus
  `Test<Client>PrintsTheInertLine`. Red: `go test ./internal/<daemon> ./cmd/<client>`.
- **Change:** load through `loopholegrant.Read`; replace the clients' `"loopholes": {"journal":
  {"enabled": true}}` recipes with T2's text (the daemon is the source when reachable; the
  client's own fallback when unreachable names `yolo request <pack> <kind>`).
- **Commit:** `feat(<daemon>): refuse without a grant, naming the next step` (one per task).

### T10. Legacy projection (after T4, parallel with T6)

- **Settled:** no access is lost or gained silently ([LG-D11](loophole-access-approval.md#LG-D11));
  the mapping is [PD-9](#PD-9), which is design [§6](loophole-access-approval.md#6-migration)'s table.
- **Tests first** (`internal/config/loopholegrantlegacy_test.go`), one per [§6](loophole-access-approval.md#6-migration) row on a fixture
  manifest with the `legacy_*` keys: user `enabled: true` → ceiling `always ["read"]`; with
  `settings.full: true` → `["read", "read_all"]`; a valued kind → `on_request`; workspace
  `enabled: true` → request; switch file `enabled: true` → record-equivalent grant; `visible`
  list → request values. Red: `go test ./internal/config -run Legacy`.
- **Change:** start from `ProjectReadableLegacyPack`; the gate reads the projection for legacy
  files; the legacy validator stops warning on what the projection consumes.
- **Commit:** `feat(config): read legacy loophole switches as grants during the JSONC window`

### F1 to F5. Flip each shipped loophole (serial chain)

Each flip, for its loophole:

1. Add `access` and `[[grants]]` to the manifest (values below); remove or move settings.
2. Delete its daemon's absent-`grants` path; it fails closed.
3. Remove its name from the awaiting set.
4. Rewrite every test that asserted the old behavior to the new behavior, not until green:
   `internal/loopholedecl/shipped_test.go`'s `wantDefaultEnabled` and settings assertions, `internal/loopholes/shipped_test.go`,
   `internal/loopholedecl/defaultenabled_test.go`, and every `rg -l '<loophole>' internal/config
   internal/cli internal/loopholes` test that switches it on.
5. Rewrite its integration tests to TOML `requests` plus `allow` plus an accepted gate.
6. Update `packs/<pack>/README.md`.

| Flip | After | Manifest | Settings | Integration |
| :--- | :--- | :--- | :--- | :--- |
| F1 journal | T6, T7, T10 | `access = "read the host's journal"`; `read` (requestable by default, `legacy_enable`), `read_all` (`legacy_setting = "full"`) | remove `full` | new `integration/journalgrant_test.go`: `TestJournalWithNoGrantRefusesInTheJail`, `TestJournalReadGrantServesTheUserJournal` ([PD-14](#PD-14)) |
| F2 macos-log | F1 | `access = "read the Mac's unified log"`; same two kinds | remove `full` | `integration/macosuserlogbridge_test.go`, `macosuserparity_test.go` (darwin only; say so) |
| F3 host-processes | F2, T8 | `access = "see host processes"`; `view` (valued, `value_noun = "process names"`, `legacy_enable`, `legacy_values = "visible"`) | remove `visible`; `fields` rescoped to `user` | `integration/macosuserhostprocesses_test.go` |
| F4 serial | F3, T9 | `access = "open host serial devices"`; `devices` (valued, `value_noun = "device globs"`, `legacy_enable`, `legacy_values = "allowed_devices"`) | remove `allowed_devices` and its five defaults; `default_baud` rescoped to `user` | `integration/serialfixture_test.go`, `macosuserserial_test.go` |
| F5 audio | F4 | `access = "use the host's microphone and speakers"`; `use` (`legacy_enable`) | none | full suite |

- **F1's integration tests**, in full shape: `TestJournalWithNoGrantRefusesInTheJail` launches a
  jail with `packs = ["journal"]` and no `allow`, runs `yolo-journalctl -n 1`, expects exit 64 and
  stderr containing `yolo request journal read`. `TestJournalReadGrantServesTheUserJournal` adds
  `[requests.journal] read = true`, launches with `--accept-config-changes`, runs
  `yolo-journalctl -n 1`, expects exit 0.
- **Commit:** `feat(<pack>): grant access through [[grants]] instead of the switch`

### T11. Live apply (after T6; parallel with the flips)

- **Settled:** for a mediated loophole, `yolo config apply` rewrites the grant file and the daemon
  takes it at its next request; for an unmediated one the apply records the grant and says it lands
  at the next restart ([LG-D7](loophole-access-approval.md#LG-D7)).
- **Tests first:** `TestConfigApplyRewritesTheGrantFileAndSaysRestartForAudio`, through
  `ConfigApply` on fixtures: after apply, `loopholegrant.Read` on the live file shows `granted`; the
  output names a restart for the audio-shaped fixture. Red: `go test ./internal/cli/run -run ConfigApplyRewritesTheGrantFile`.
- **Change:** a `liveGrantFilesSeam` beside `liveScopeFilesSeam`; `changetiming.Classify` maps a
  mediated grant to live and an unmediated one to restart.
- **Commit:** `feat(run): yolo config apply rewrites a running loophole's grants`

### T12. Migration rows and the standing equality layer (after F5 and the in-flight `config migrate FILE`)

- **Settled:** design [§6](loophole-access-approval.md#6-migration)'s table, every row; the canonical form compares a standing per loophole
  per kind per workspace; a grant that would vanish is a printed difference and nothing is
  written; a record grant the project file does not state is reported by `yolo check` with the
  `yolo request` line that writes it ([LG-D11](loophole-access-approval.md#LG-D11),
  [LG-D14](loophole-access-approval.md#LG-D14)).
- **Tests first:** `TestLegacyLoopholeEnabledMigratesToAllowAndRequests` and
  `TestTheMigrationCarriesASwitchFileIntoTheRecord`, through the migration planner, one fixture row
  per [§6](loophole-access-approval.md#6-migration) row, asserting the equality proof passes and the standing is unchanged;
  `TestCheckReportsARecordGrantTheProjectFileDoesNotState`.
- **Change:** `lowerLoopholes` calls T10's projection instead of writing nothing; the aws-auth
  fix's "runs" layer gains standings; the switch file is deleted after the record is written.
- **Commit:** `feat(configmigrate): carry every loophole switch into grants`

### T13. Retire `default_enabled` and workspace-scope settings (after F5)

- **Settled:** `default_enabled` is refused at load with [§5](loophole-access-approval.md#5-every-stop-and-its-next-step)'s text, naming `[[grants]]`;
  `scope = "workspace"` on a setting is refused at load.
- **Tests first:** `TestDefaultEnabledIsRefusedOnEveryManifest` (decoder refusal plus a walk of
  every shipped manifest); `TestAWorkspaceScopeSettingIsRefusedAtLoad`. Expected text:
  ```text
  packs/journal/loopholes/journal/loophole.toml: default_enabled is gone. A loophole runs whenever its pack
    is selected and grants nothing until a grant covers the project; declare what can be granted under
    [[grants]] instead, and remove the key.
  ```
- **Change:** strip the key from every manifest; `Enabled` seeds true; delete `SwitchedByConfig`,
  `ApplyConfigEnabled`'s switch half and `ConfigEnabledOverride`; delete the empty awaiting set.
- **Integration:** full suite once.
- **Commit:** `feat(loopholedecl)!: refuse default_enabled; a loophole runs when its pack is selected`

### T14. Retire `yolo loopholes enable|disable` (after F5, parallel with T13)

- **Settled:** both refuse for every loophole with [§5](loophole-access-approval.md#5-every-stop-and-its-next-step)'s text; `disable` first removes a switch an
  older yolo wrote and exits 0; `list` and `status` show the standing per kind
  ([LG-D8](loophole-access-approval.md#LG-D8)).
- **Tests first:** `TestLoopholesEnableRefusesNamingRequestAndAllow` (extends
  `TestLoopholesEnableRefusesABroker`), expecting exactly:
  ```text
  yolo loopholes enable is retired: a loophole runs whenever its pack is selected, and a project gets access by a
    grant, not a switch.
    For this project:    yolo request journal read     (in the workspace), then `yolo config apply` here and y
    For every project:   yolo allow journal read
  ```
- **Commit:** `feat(loopholes): retire enable and disable in favor of request and allow`

### T15. `cgroup-delegate` selected by default (after T13)

- **Settled:** ungated, its pack selected by default; not reported at launch when inert, only by
  `yolo loopholes status` ([LG-D13](loophole-access-approval.md#LG-D13)).
- **Tests first:** `TestTheCgroupDelegateRunsWithAnEmptyConfig`: a launch with no `packs` key
  resolves `cgroup-delegate` as selected and `startCgroupDelegate` publishes its socket. The
  `off` half is added when L4's `off` lands ([PD-13](#PD-13)).
- **Change:** [PD-13](#PD-13); `integration/cgroup_test.go` drops its legacy enable.
- **Commit:** `feat(packs): select cgroup-delegate by default`

### T16. Docs (after T12 to T15)

Rewrite to the shipped behavior: `internal/cli/config_ref.txt` (allow/requests for loophole kinds),
`userguide/guides/loopholes.md`, `docs/reference/loophole-system.md` (the amendments the design's
[§8](loophole-access-approval.md#8-what-this-does-not-license) lists), `docs/reference/pack-system.md`
(`[[grants]]`, `selected_by_default`), `packs/*/README.md` not already done by a flip, and
`AGENTS.md`'s "nothing is active by default" (now with the `cgroup-delegate` exception). Then the
design graduates per the `design-doc` skill. Commit: `docs: loophole grants replace the switch`.

## Ships with

- **Observable surfaces:** the gate lines (T4), the launch disclosure line and briefing standing
  (T6), `yolo loopholes list|status` standing (T14), every refusal in [§5](loophole-access-approval.md#5-every-stop-and-its-next-step) (T2 renders, T7 to T9 and
  T13 to T14 emit).
- **Tests that will break, and should, rewritten to the new behavior:**
  `TestDefaultEnabledIsHonoredBothWays`, `shipped_test.go`'s `wantDefaultEnabled` and the
  `visible`/`fields`/`full` assertions, `cmd/yolo-journalctl/main_test.go`'s enable recipe,
  `TestRetiredEnabledKeyIsRefusedInTOMLToo` if its wording names the switch, and the legacy-JSON
  integration fixtures listed under [Traps](#traps).
- **Error text:** every new `yolo …` hint is covered by `TestEveryHintedYoloCommandExists`.

## What the owner sees when it's done

The owner's config is TOML, so today journal, host-processes and serial do not run at all.

- **Until F1:** nothing changes for these three.
- **After F1:** `packs = ["journal"]` runs the bridge in every jail. `yolo-journalctl` refuses with
  the `requestable` text. The agent runs `yolo request journal read`; at the next launch the gate
  shows `+ journal: read your user journal (requested by yolo-jail.toml:N)` and a y serves the
  user journal from then on in that project. Before T11 it takes effect at the next launch; after
  T11, `yolo config apply` on the host makes it live in the running jail. `read_all` needs
  `yolo allow journal on_request read_all` first.
- **After F3:** `yolo-ps` refuses with the `not_allowed` text naming
  `yolo allow host-processes on_request view`; then `yolo request host-processes view postgres`
  and a y show only those processes.
- **After F4:** `yolo-serial` lists nothing and refuses the same way; a request names the device
  globs, and there is no default list.
- **After F5 and T15:** audio says at launch how to ask; `yolo-cglimit` works with an empty config.
- **After T12:** `yolo config migrate` carries any old switch, setting or switch file into grants
  with a passing equality proof.

## Don't

- Do not add a second gate, record writer or scope collector; [PD-4](#PD-4) and T4 make one.
- Do not let a daemon read config, the project file or the approval record; the grant file only.
- Do not put the grant file where any mount reaches it.
- Do not give a valued kind a default value, and do not restore `serial`'s globs.
- Do not switch on a tool name anywhere in core; read the manifest's shape.
- Do not add an escape hatch environment variable for a missing grant; the refusal names the step.

## Blockers

None design-level. Ordering blockers only: T6 waits for the aws-auth fix; T12 waits for
`yolo config migrate FILE`; no release ships between F1 and T12.
