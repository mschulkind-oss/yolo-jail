---
title: "Plan: build slice 1 of the unified workspace home (container setups)"
date: 2026-11-24
status: accepted
stage: DECIDED
next: "U1 to U5 have landed (U5 wires the launch: compose, merge, adopt); build U6 (host readers and yolo check), then U7 and U8"
depends-on: []
tags: [plan, setups, home, sessions, concurrency, migration]
summary: "The build hand-off for slice 1 of the unified workspace home: a new package internal/unifiedhome holds the pure merge and the composition; paths names the unified home; the launch composes each container setup's slice, merges stranded homes at the commit point, takes the project lock on every container launch and refuses a second container setup. Eight units, each one commit, tests first; Mac witnesses W-UH1, W-UH5 and W-UH6 are owed to the owner's Mac agent."
vantage:
  status-chip: true
---

# Plan: slice 1 of the unified workspace home

**Status:** 2026-11-24. Completed against the tree, written against `dfaca2a56`.
**Design:** [`unified-workspace-home.md`](unified-workspace-home.md), slice 1 of
[its slices](unified-workspace-home.md#14-slices): the pure merge, the unified home, composition for Podman and Apple
Container, the stranded-home migration, the project lock for every fresh container launch, the two-container refusal
with the keeper-evidence exception, and the host guard test.

**Precedence.** The design wins on behavior. The tree wins on fact, for cosmetic drift only (a moved line, a renamed
helper with the same role): follow the tree and say so in the report and the commit. This file is advice and is the
first thing to be wrong; never twist the code to match it.

**Stop and report, do not commit,** when: a test does not fail the way its task predicts; a file a task names is dirty
with someone else's work; a symbol a task names is gone; the fix needs a file outside the task's fence; a gate fails
outside the task's scope. Other agents commit on `main` concurrently: stage by path with `git add -N <paths>` then
`git commit -- <paths>`, never `git add -A`.

## Settled, do not re-open

Quoted from the design so a task never needs it open. Each is a ruling or a design decision.

- **Layout.** The unified home is `<workspace>/.yolo/unified-home`, dotted, mirroring the in-jail path. A slice stays at
  `.yolo/setups/<name>/home` and keeps its backend's layout: Podman `claude/` (dot-stripped), Apple Container
  `.claude/`. Only links cross: a link is named for the child (`projects`) and targets the absolute
  `/workspace/.yolo/unified-home/.claude/projects` ([UH-D1, UH-D3, UH-D4](unified-workspace-home.md#decision-ledger)).
- **Default is shared.** For each workspace-scope `[[state]]` directory of a SELECTED pack, every child is shared
  except generated paths and per-pack carve-outs ([§6.2](unified-workspace-home.md#62-the-default-for-an-undeclared-path)).
  No table of shipped directory names; `packload.Embedded*` is never read to reserve a name
  ([UH-D9](unified-workspace-home.md#decision-ledger)).
- **The merge** ([§9.2](unified-workspace-home.md#92-the-merge)): absent at destination is a rename that refuses to
  replace; directory on both sides recurses; identical bytes skip; different bytes keep both, the newer by mtime keeps
  the name, the other becomes `<name>.from-<setup>` (`.2`, `.3` on a further clash); a link or a file-versus-directory
  clash moves as a link under its own name, never followed; sockets, fifos, lock and pid files are left and counted;
  `settings.json`, `.claude.json`, `history.jsonl` and databases are never merged by content. Nothing is deleted or
  overwritten. It walks beneath `O_NOFOLLOW` roots and is crash-convergent.
- **Composition** ([§5.2](unified-workspace-home.md#52-composition-what-a-launch-does-to-its-own-slice)): a rename that
  refuses to replace moves each real, non-generated, non-carved, non-link child of the slice's directory into the
  unified home (merging when the name exists) and a link takes its place; each unified child absent from the slice gets
  a link; a child that is a prefix of a generated or carved path is composed recursively; a link already in the slice
  is never adopted, followed or rewritten. A slice that cannot hold links, or `workspace_readonly` covering `.yolo`:
  composition declines with one line and never refuses the launch. One writer: a launch writes only its own slice and
  adds to the unified home.
- **The rule** ([§7.1](unified-workspace-home.md#71-the-rule), [OQ-UH1](unified-workspace-home.md#OQ-UH1),
  [OQ-UH3](unified-workspace-home.md#OQ-UH3)): at most one container setup per workspace; container plus guest allowed;
  a second guest refused; the same setup attaches as today; a peer that cannot be asked refuses, unless every keeper
  lock and session record says not running and only its runtime could not be asked, then the launch proceeds with one
  line naming that setup. Refusal texts (exact, below in U4). No lock file in `.yolo`, no new hatch.
- **Migration** ([§9](unified-workspace-home.md#9-migration-one-merge-for-every-home-on-disk),
  [OQ-UH2](unified-workspace-home.md#OQ-UH2)): only a launch merges; no `yolo home merge` verb; silent through the
  plan and commit split; one informational line, also in `launch.log`; refused while any jail runs for the legacy home
  and every other setup's slice ("could not ask" counts as running, naming `yolo stop`); also left, with the `mv -n` to
  run: a link at `.yolo` or on the `setups` chain, a source not a real directory owned by the user (or, for the yolo
  account, by it), a source that is its own mount.
- **Legacy home, two steps** ([§9.3](unified-workspace-home.md#93-the-legacy-home-in-two-steps)): shared children merge
  into the unified home unconditionally (no attribution refusal); the remainder (installs, generated files) moves to the
  attributed setup's slice.
- **Host and jail never share** ([§10](unified-workspace-home.md#10-host-and-jail-never-share)): one path function
  `paths.UnifiedHome(workspace)`; link targets confined to `<workspace>/.yolo/unified-home`; the host renderer does not
  import the composition package.

## Plan decisions

Mechanism choices the design leaves open, decided here at the plan's altitude. None contradicts a ruling. **Stop and
report** if one turns out to.

| ID | Decision | Why |
| :--- | :--- | :--- |
| <a id="UHP-D1"></a>UHP-D1 | One new package, `internal/unifiedhome`, holds a pure plan layer (listings in, steps out) and a thin apply layer over `*os.Root` and `x/sys/unix` descriptors. The rename is a parameter (`Rename func(oldDir int, old string, newDir int, new string) error`), not a global; `run` passes `legacyHomeRename`. | `renameNoReplaceBetween` lives in package `run` behind `renamenoreplace_linux.go` and `renamenoreplace_darwin.go`; moving it enlarges the diff and the test seam `legacyHomeRename` already exists. |
| <a id="UHP-D2"></a>UHP-D2 | `jailRuns` keeps its `(answer, how)` signature as a wrapper over a new `jailStatus(cname) jailAnswer` carrying `Class` (container or guest) and `RuntimeOnly` (every keeper and record answered "not running", only a runtime was unaskable). A key `cname` or a runtime answer is class container; keys `cname.macos-user` and `cname.guest` are class guest. | The design requires the class to come from the witness that answered ([§7.2](unified-workspace-home.md#72-where-it-runs)); three callers use the two-value form (`legacyjail.go:29`, `freshhome.go:46`, `legacyhome.go:445`), so a wrapper leaves them untouched. |
| <a id="UHP-D3"></a>UHP-D3 | Slice 1 touches container setups only. The exclusion check and composition run when `o.Setup.Confinement` is Podman or Apple Container. A guest launch's behavior is unchanged (slice 2). A source slice whose setup resolves (`setup.Lookup(name, user)`, user map from `config.SelectedSetupFromConfig()`) to Seatbelt or Landlock is a LEFT source in the merge, with the line saying the guest joins in a later release; a name that does not resolve is treated as a container source. | A guest slice is `HOME` and is not composed in slice 1; merging its sessions out would strand them, which is the bug this design ends. |
| <a id="UHP-D4"></a>UHP-D4 | The project lock (`holdHomeLock`) is taken by `selectHome` for every non-dry, non-fresh-home launch of a container setup, whether or not a legacy home exists. It still rides on the launch lock, so an attach that releases its launch lock (`attachExisting`'s `o.releaseLaunchLock`) releases it too. Macos-user launches keep today's condition (legacy home present). | A macos-user keeperless session (JL-D42) takes no keeper and would hold the lock for the whole session; slice 2 revisits it with the guest. |
| <a id="UHP-D5"></a>UHP-D5 | The exclusion check is the first statement of `commitHome`, and `commitHome` returns false when it refuses. It is skipped for fresh-home launches (`o.FreshHome`: that home shares nothing) and for guest setups (UHP-D3). | `commitHome` is already the commit point every arm calls and `TestEveryArmCommitsTheHomeBeforeItWritesIt` pins; riding on it keeps one call site. |
| <a id="UHP-D6"></a>UHP-D6 | Merge and composition run in one new method `o.unifyHome(wsState, loadedPacks, rt) bool`, called in `runContainer` straight after `prepareWsState` and before `deliverChannel`. | `preparePodmanBindSources` makes the slice's `claude/` real only inside `prepareWsState`, and the selected packs are known there, which `commitHome` is not given. |
| <a id="UHP-D7"></a>UHP-D7 | A deferral (a jail of another setup runs, or the runtime cannot be asked) no longer binds the legacy home (SUP-D21 is superseded by [§12](unified-workspace-home.md#12-rulings-this-amends)): the launch binds its setup's slice. The remainder (step 2 of the legacy home) is adopted by `adoptLegacyHome`'s rename when the slice is absent or an empty directory, and otherwise by the same merge into the populated slice, slice names winning ties by keeping both. The legacy `prism` and mountpoint record move only when the slice has none. | A populated slice would otherwise make `destTaken` leave the installs stranded for good, the failure SUP-D21 was written to prevent. The merge is already the union primitive. |
| <a id="UHP-D8"></a>UHP-D8 | The launch's one line reads `Merged N files from M homes into this workspace's shared home.` plus ` K kept twice (<name>.from-<setup>).` when K > 0. | The design's "such as" example counts sessions, which core cannot recognize; a file count is exact and agent-neutral. |
| <a id="UHP-D9"></a>UHP-D9 | The generated-path set is built from the selected packs' contributions: `Path` of `config`, `config-overlay` and `config-list` (`internal/packdecl/contributes.go:714`, `:1896`), `Into` of `skills` and `briefing` (`:369`), `To` of `files` (`:2278`), plus `paths.InstalledProgramSurfaces()` and core's fixed list (`~/.yolo/bin`, bootstrap and venv scripts, `.ssh`, `config`, `bash_history`, `venv-shadows`, `writable-home`, `.config`). Core has no per-agent knowledge. | Design: "yolo knows every destination a selected pack renders" ([§6.1](unified-workspace-home.md#61-what-scope--workspace-now-means)). |
| <a id="UHP-D10"></a>UHP-D10 | `unifiedhome.Report.Moved` counts every file and link that arrived (a renamed directory counts the leaves beneath it, kept-both arrivals included) and `KeptBoth` is the subset that arrived as `<name>.from-<label>`. When the source file is the newer of two, the destination's file is the one renamed to `<name>.from-<label>` (the label names the merge that displaced it, not the file's origin) and the source takes the name; a failed second rename leaves the name absent and a rerun finishes it. | UHP-D8's line needs one exact file count; the design says only that the newer keeps the name. |
| <a id="UHP-D11"></a>UHP-D11 | A directory absent at the destination is renamed whole only when nothing beneath it is a lock, pid or socket file, a fifo or a device; otherwise the merge creates it and recurses so those are left in the source. A child whose rename fails is left and counted, the rest continue, and `Merge` returns the failures joined. A source directory that is a link is left unread. | [§9.2](unified-workspace-home.md#92-the-merge) says such files are left, which a whole-directory rename would break; one bad child must not strand the others. |
| <a id="UHP-D12"></a>UHP-D12 | `Options.OwnerUID` (nil means unchecked) leaves a source state directory another user owns. The "source is its own mount" refusal needs mountinfo and stays with the caller (U5), which already knows the source root. | Ownership is one `fstat`; a device-number test cannot see a same-filesystem bind mount, so the pure package does not pretend to. |
| <a id="UHP-D13"></a>UHP-D13 | Regular files with identical bytes are compared byte for byte after a size check, not hashed. A process file is a regular file named `lock`, `pid`, `SingletonLock`, `SingletonSocket`, `SingletonCookie` or ending `.lock`, `.pid`, `.sock`. | Exact and cheaper than a hash for two files; the name list is the minimal one the design's "lock and pid files" needs. |
| <a id="UHP-D14"></a>UHP-D14 | `unifiedhome.Compose` takes the composed directory per backend (`sliceDir`, `unifiedDir`), paths relative to it, and a `HomePath` it joins with `unifiedDir` and the child's path to build every link target, so no target can leave the unified home. The one thing it removes from a slice is an EMPTY directory the merge emptied (`rmdir`, which fails on a non-empty one), so the link can take its place; a directory still holding anything is left real and counted. A `per_setup_state` path is relative to its state directory, so omp's `agent.db` is `agent/agent.db` under `.oh-omp`. | A slice child cannot become a link while a husk of it remains; `rmdir` loses no byte. The plan's bare omp names would not match `agent/`. |
| <a id="UHP-D15"></a>UHP-D15 | Composition declines (`ComposeReport.Declined` plus its one line, no error, no refusal) when laying a link fails with `EROFS`, `EPERM`, `EACCES`, `ENOTSUP`, `EOPNOTSUPP` or `ENOSYS`; any other failure is a returned error. What was adopted before the decline stays in the unified home and a later launch finishes the links. `Options.Symlink` is the test seam. | The plan requires "one line and never refuses the launch" for a slice that cannot hold links, and a half-composed slice must converge on rerun. |
| <a id="UHP-D16"></a>UHP-D16 | `per_setup_state` is spelled as the design's array of `{dir, paths}` tables and listed in `packs/releasedecode_test.go` as a known break of v0.20.0 for pi, copilot and omp (that reader takes any array-valued root key for an unknown kind and skips it, guarded by `guardNoRestageOnAttach`); `toml.go` lists the key beside `session_homes` so this build does not. | The release-decode check measured the skip; a table-valued spelling would contradict the design's [§6.3](unified-workspace-home.md#63-the-carve-out-key), and the skip only leaves an old jail sharing nothing, as before. |
| <a id="UHP-D17"></a>UHP-D17 | `jailAnswer` carries `Answer`, `How`, `Class`, `KeeperUnknown`, `Unasked` (runtimes on PATH that could not be asked, in order) and `RuntimeOnly` (unknown, no keeper unreadable, only runtimes unasked). Class is guest for the `cname.macos-user` and `cname.guest` keys and container for the bare key and any runtime answer. `decidePeers` is pure over `[]peerFact` and returns a refusal or notes; `refuseContainerPeer` prints them. | The plan fixes the shape in prose only; a pure verdict keeps the [§7.1](unified-workspace-home.md#71-the-rule) table testable without a fake runtime. |
| <a id="UHP-D18"></a>UHP-D18 | An unaskable peer's refusal names the first unasked runtime and its listing command as `runningListCommand(rt)` plus ` -a` (`podman ps -a`, `container ls -a`), matching the design's quoted text. When a keeper lock could not be read (no runtime to name), the refusal says it could not tell and names `yolo stop --setup <peer>`. A running peer of the same class outranks an unknown one; with several unknown peers the first by name is named. | The design quotes only the runtime case; a keeper that cannot be read has no runtime to list and must still name a next step. |
| <a id="UHP-D19"></a>UHP-D19 | `refuseContainerPeer` is also skipped for `o.DryRun` (besides `o.FreshHome` and guest setups, UHP-D5): a dry run writes nothing and `commitHome` is reachable from one. The [OQ-UH3](unified-workspace-home.md#OQ-UH3) proceed line is dim, printed once per peer, and the launch continues. | A dry run that refused would report a launch that no launch would make. |
| <a id="UHP-D20"></a>UHP-D20 | The race pin is two AST orderings, not a spawned keeper: `keeper.run` calls `awaitLivenessLock` before `beforeReady`, and `runContainer` calls `releaseHomeLock` only inside the keeper's `relay` (its `started` event, and again at ready), never before it. The lock is not held to ready: only the launch process relays ready, and a stopped one would hold every other launch of the workspace. The tree satisfies both, so the design's race inference holds. | A real keeper spawn needs a container; the two orderings are the whole inference, and each fails if moved. |

Decided while building U5:

| ID | Decision | Why |
| :--- | :--- | :--- |
| <a id="UHP-D21"></a>UHP-D21 | `unifyHome(cfg, wsState, packs, rt)` takes the launch's config and returns nothing: it never refuses, so a bool would be always true. It declines (one line, nothing merged or composed) when a `workspace_readonly` entry equals or contains the unified home's path (`.yolo`, `.`, or `.yolo/unified-home`). | The readonly check needs `cfg`; UHP-D6 spelled the call without it. A readonly slice of `.yolo` alone is the case §5.2 names; an entry on the unified home itself has the same effect. |
| <a id="UHP-D22"></a>UHP-D22 | `unifiedhome.Source` gains `Stay []string`: paths beneath each source directory (`/`-separated) that the merge never moves out, a name equal to one stays whole and a directory that is a proper prefix is merged child by child. `.` is a valid destination and source directory, for a whole-home merge. | §9.3 merges only the SHARED children of other homes; without a stay list the merge would carry another setup's generated files and installs (ELF against Mach-O) into the unified home. The legacy remainder (UHP-D7) is a whole-home merge. |
| <a id="UHP-D23"></a>UHP-D23 | The stay set per state directory is `basehome.DeclsFromPacks(selected)`'s config surfaces, skills, briefing and file destinations and credential links, plus the targets of `paths.HomeFileRedirects`, `paths.InstalledProgramSurfaces` and each selected pack's `per_setup_state`, each cut to the paths beneath that directory. Core's fixed list (`.config`, `.ssh`, `bash_history`, ...) names no path beneath a pack's state directory, so it is not consulted. | UHP-D9 named JSON-pointer `Path` fields, which are positions inside a surface, not home paths; `DeclsFromPacks` is the one place that already reads every destination of the selected packs as a home-relative path. |
| <a id="UHP-D24"></a>UHP-D24 | A launch always binds its own setup's slice (`currentHome`), including when the legacy home is deferred, left, or laid by another backend. A legacy home whose slice is populated takes `mergeLegacyRemainder` (a whole-home merge, label `legacy`); the home's empty directory skeleton stays and later launches merge nothing and say nothing. | One answer for every outcome of `planLegacyHome`; the legacy home's shared children reach the unified home through `unifyHome` regardless. |
| <a id="UHP-D25"></a>UHP-D25 | The line's N is `Report.Moved` summed over the merges and the composition; M counts the homes that moved at least one file, the launching slice once however many of its directories moved. A source that is a guest's, another user's, its own mount, or a link, or that waits behind a running or unaskable jail, prints one dim line naming the setup and the step. | UHP-D8 fixes the text, not how M is counted. |

## Map

| Path | Change |
| :--- | :--- |
| `internal/paths/unifiedhome.go` | new: `UnifiedHome`, `EnsureUnifiedHome`, `OpenUnifiedHomeRoot` (U1) |
| `internal/paths/unifiedhome_test.go` | new (U1) |
| `internal/unifiedhome/` (`plan.go`, `merge.go`, `compose.go`, tests) | new package: pure plan, apply, composition (U2, U3) |
| `internal/packdecl/packdecl.go`, new `internal/packdecl/persetupstate.go` | top-level `per_setup_state` key and validator, beside `session_homes` (`packdecl.go:196`, `sessionhome.go`) (U3) |
| `packs/pi/pack.toml`, `packs/copilot/pack.toml`, `packs/omp/pack.toml` | declare `per_setup_state` carve-outs (U3) |
| `internal/cli/run/legacyhome.go` | `jailStatus`, class, peer enumeration; `selectHome` lock; `commitHome` exclusion; legacy two-step (U4, U5) |
| `internal/cli/run/flock.go` | no change expected; `holdHomeLock` reused as is (U4) |
| `internal/cli/run/unifyhome.go` | new: `unifyHome`, the generated-path set, the launch line (U5) |
| `internal/cli/run/run.go` | one call, `o.unifyHome`, after `prepareWsState` (`run.go:2127`) (U5) |
| `internal/prune/agentlogs.go` | also sweep the unified home's log directories (U6) |
| `internal/cli/check/unifiedhome.go` | new: read-only plan of what waits, mirroring `basehomedisclosure.go` (U6) |
| `internal/cli/run/legacyhome_test.go`, `setuphome_test.go`, `legacyhomelate_test.go` | rewrites of tests that assert SUP-D21 (U5) |
| `integration/unifiedhome_test.go` | new (U7) |
| `docs/design/unified-workspace-home.md`, `docs/plans/roadmap.md` | status and links (final task) |

## Reuse before you write

- **Plan and commit split**: `selectHome`, `commitHome`, `currentHome`, `legacyHomeRequest`, `adoptLegacyHomeStep`
  (`internal/cli/run/legacyhome.go`). Mirror the shape: a pure `plan*` that mutates nothing, one `commit*` after the
  last refusal.
- **Liveness**: `Options.projectJails`, `projectJailNames`, `jailRuns` (`legacyhome.go:442`, `:460`, `:491`);
  `probeKeeper`, `liveKeyedSessions`, `keeperKey`, `keeperNotchMacosUser`, `keeperNotchGuest`
  (`keeperstate.go:177`, `:725`, `:617`, `:607`); `probeExistingContainer`; `projectContainerRuntimes`.
- **Locks**: `holdHomeLock`, `releaseHomeLock`, `workspaceLock.ride` (`flock.go:365`, `:393`, `:152`).
- **Stop remedy**: `stopRemedy(rt, cname)` (`attachskew.go:67`), `runningListCommand(rt)` (`lifecycle.go:129`); the
  refusal shape of `refuseUnkeptJail` (`lifecycle.go:368`) and `refuseLegacyJail` (`legacyjail.go`). `yolo stop
  --setup <name>` is the real flag (`internal/cli/stopsetups_test.go`).
- **No-link descriptor chains**: `paths.OpenWorkspaceStateRel(workspace, "unified-home")` already opens any directory
  under `.yolo` refusing a link at every component (`paths/setupstate.go`, `OpenWorkspaceStateRel`);
  `paths.EnsureSetupStateDir` (`setupstate_unix.go:27`) is the creation shape to copy for `EnsureUnifiedHome`;
  `paths.OpenWorkspaceHomeRoot` and `paths.WorkspaceHomeStates` list and open the sources.
- **Rename that refuses to replace**: `renameNoReplaceBetween` and the `legacyHomeRename` seam
  (`legacyhome.go:117`); tests swap the seam like `TestLegacyHomeLeavesWhatItCannotMove` does.
- **Pack state declarations**: `packload.WritableDirs(loadedPacks)` is the workspace-scope list;
  `wsStateHomeRel(rt, ".claude")` (`prepare.go:889`) spells a slice directory for a backend.
- **Validator**: `packdecl.SessionHomeNameProblem` (`sessionhome.go:97`) validates carve-out names.
- **Tests**: `packHome(t)`, `seedLegacyHome(t, ws)`, `dispatchOptions(...)`, `answerContainerOf(...)` in
  `internal/cli/run/*_test.go`; the keeper fixtures `openKeyedSessionRecord` and `closeKeyedSessionRecord`
  (`setuphome_test.go:237` shows both); `testtmp.ShortDir()` for socket fixtures. The link shape of
  `TestALinkedConfigIsMovedAndItsTargetIsNeverTouched` (`internal/configmigrate/linkedconfig_test.go:44`).

## House style that isn't obvious

- **Every refusal names the next step** ([happy path](../reference/happy-path-principle.md)); a line that only reports is
  a defect. Commands are built with `shquote.JoinDisplay`, never string-concatenated.
- **Tests are event-driven**: wait on a held flock, a channel or an injected rename, never a sleep or elapsed time.
  Fixtures are `EvalSymlinks(t.TempDir())`; a test leaves nothing in `TMPDIR` (`tmp-leak-gate.py` fails the run).
- **Rich markup**: lines go through `o.pr(o.Stderr).print(richtext.Escape(line))`, as `selectHome` does.

## Traps

- **Podman binds `claude/` at `~/.claude` as a directory.** A link inside it resolves in the jail only through
  `/workspace/.yolo/unified-home/...`; a link relative to the host path dangles. Constraint: link targets are the
  absolute container path ([§5.2](unified-workspace-home.md#52-composition-what-a-launch-does-to-its-own-slice)). Host
  readers following that absolute link find nothing: U6 reads the unified home directly.
- **`prepareWsState` copies old overlays into `claude/projects` (`prepare.go:758`).** Composition must run after it or
  the copy lands in a directory about to be replaced by a link. Hence UHP-D6.
- **`podmanLaidHome` and `appleContainerLaidHome` read layout markers beneath a home** (`legacyhome.go:543`, `:563`). A
  composed slice still carries them; never delete a marker directory.
- **`TestSelectHomeDecidesUnderTheLaunchLock` uses the `macos-user` setup** and asserts the lock is held only while a
  legacy home exists. It stays true (UHP-D4); do not "fix" it for container setups, add a container case beside it.
- **`liveKeyedSessions` removes stale records as a side effect** (`keeperstate.go:725`). A fake `jailRuns` test must
  not depend on a record surviving a probe.
- **The in-jail flock fixtures lie about Apple Container.** A flock does not cross the VM boundary; the exclusion rule
  rests on the host-side keeper lock and runtime query only. Do not add a lock file in `.yolo` to "help".
- **`git add` before any nested-jail image or bundle build**: nix sees tracked files only.
- **The jail exports `YOLO_ALLOW_MISSING_PROGRAMS=1`** and fails three `internal/cli` tests; run gates with it unset
  and `YOLO_NO_AUTO_IMAGE_REAP=1` for any integration run.

## Tasks

Build order: **U1** and **U2** are independent and touch disjoint files. **U3** is after U2. **U4** is independent of
U1 to U3 but shares `legacyhome.go` with U5, so it lands first. **U5** is after U1, U2, U3 and U4. **U6** is after U5.
**U7** is last and is the landing gate. Each unit is one commit and ends green. U1, U2 and U4 may run in parallel
worktrees; land them serially.

### U1 — name the unified home

- **Settled:** `paths.UnifiedHome(workspace)` is `<workspace>/.yolo/unified-home` and is the only function that names
  it. The directory is created through a no-link descriptor chain and refused when a link sits at `.yolo` or at it.
- **Preconditions:** `git status` clean for `internal/paths`; `rg -n 'func EnsureSetupStateDir' internal/paths` finds
  `setupstate_unix.go`.
- **Tests first** (`internal/paths/unifiedhome_test.go`): red command
  `go test -short ./internal/paths -run 'TestUnifiedHome|TestEnsureUnifiedHome|TestOpenUnifiedHome'`.
  - `TestUnifiedHomeIsUnderTheWorkspaceStateDir`: `UnifiedHome("/w") == "/w/.yolo/unified-home"`.
  - `TestEnsureUnifiedHomeMakesItThroughNoLinks`: creates it; with a link at `.yolo/unified-home` pointing at a
    sentinel directory, returns `*LinkedStateDirError` naming the link and writes nothing in the sentinel.
  - `TestOpenUnifiedHomeRootRefusesALinkAtYolo`: link at `.yolo`, returns `*LinkedStateDirError`.
  - `TestUnifiedHomeIsNotAWorkspaceHome`: `HomeStateRel(ws, UnifiedHome(ws))` is `ok == false` and
    `WorkspaceHomeStates` does not list it (it is not a home any reaper walks as a home).
- **Change:** `paths/unifiedhome.go` (new): `UnifiedHome`, `EnsureUnifiedHome(workspace) (*os.File, error)` modeled
  on `EnsureSetupStateDir`, `OpenUnifiedHomeRoot(workspace) (*os.Root, error)` returning
  `OpenWorkspaceStateRel(workspace, "unified-home")`. It creates `.yolo` only through `EnsureWorkspaceStateDir`.
- **Gates:** `go test -short ./internal/paths`; `just format`.
- **Fences:** no edit to `HomeStateRel`, `WorkspaceHomeStates` or `ValidHomeStateRel`: the unified home must NOT be
  one of the workspace's homes.
- **Commit:** `feat(paths): name the unified workspace home`
- **Report:** the red run and the green run.

### U2 — the pure merge

- **Settled:** the merge, quoted above, including every refusal that leaves a source. "Never loses a file", "union,
  order independent up to which colliding file keeps the name" and "crash-convergent" are the properties.
- **Preconditions:** U1 not required (tests build their own trees under `t.TempDir()` roots).
- **Tests first** (`internal/unifiedhome/merge_test.go`, package `unifiedhome`): red command
  `go test -short ./internal/unifiedhome`. It fails to compile first; that is the red.
  Contract the tests drive (signatures are the contract; bodies are the implementer's):

  ```go
  // Source is one home to read from, labeled for .from- names.
  type Source struct {
      Label string    // "podman-self", "legacy"
      Root  *os.Root  // the home, opened beneath a no-link chain
      Dirs  []string  // home-relative state directories to read: both spellings, ".claude" and "claude"
  }
  type Options struct {
      Rename func(oldDir int, old string, newDir int, new string) error
  }
  type Report struct{ Moved, Skipped, KeptBoth int; Left []LeftEntry }
  func Merge(dst *os.Root, dstDir string, src Source, o Options) (Report, error)
  // The pure layer: one directory level, listings in, steps out, nothing touched.
  func PlanLevel(src, dst []Entry, sameBytes func(name string) bool, label string) []Step
  ```

  Cases, each a named test with exact literals:
  - `TestMergeConservesEveryInputByteString`: seeded `rand` (seed `7`), 40 files over three sources with name
    collisions; afterwards every input byte string exists in the unified tree, a `.from-` name or a left source.
  - `TestMergeTheThreeStrandedHomes`: `podman-self/home/claude/projects/b.jsonl` = `b`,
    `apple-container-self/home/.claude/projects/c.jsonl` = `c` and a Podman-laid
    `apple-container-self/home/claude/projects/a.jsonl` = `a`, merging both spellings of `.claude` yields
    `unified-home/.claude/projects/{a,b,c}.jsonl` and no `.from-` name.
  - `TestMergeCollisionKinds`: identical bytes skip and count; different bytes keep both, newer mtime keeps the name
    (set with `os.Chtimes`); a file against a directory keeps both.
  - `TestMergeSecondFromNameGetsANumericSuffix`: three differing `x.jsonl` give `x.jsonl`, `x.jsonl.from-a`,
    `x.jsonl.from-a.2`.
  - `TestMergeMovesALinkAsALinkAndNeverTouchesItsTarget`: a link in a source pointing at a sentinel outside `.yolo`
    arrives as a link; the sentinel's bytes are byte-identical; the link's target is never read.
  - `TestMergeIsIdempotentAfterAnyFailedRename`: loop `n` over every rename; inject an error on the `n`th through
    `Options.Rename`, rerun with the real one, assert the same final union.
  - `TestMergeLeavesSocketsAndNeverMergesSettings`: a fifo is left and counted; `settings.json` and `history.jsonl`
    present on both sides are kept both, never concatenated.
- **Change:** `internal/unifiedhome/plan.go` (pure: `PlanLevel`, `Entry`, `Step`), `merge.go` (apply: walks with
  `os.Root` and `unix.Openat(O_NOFOLLOW)` descriptors, calls `Options.Rename`). Imports only the standard library and
  `golang.org/x/sys/unix` (no `internal/paths`, no `internal/setup`).
- **Gates:** `go test -short ./internal/unifiedhome`; `go vet ./internal/unifiedhome`; `just format`.
- **Fences:** nothing under `internal/cli`; do not move `renameNoReplaceBetween` (UHP-D1).
- **Commit:** `feat(unifiedhome): pure merge of every home into one`
- **Report:** the red compile failure, the green run, and whether `os.Root` alone was enough or a descriptor walk was needed.

### U3 — composition and the carve-out key (after U2)

- **Settled:** composition and the `per_setup_state` key quoted above. Names are `dir`-relative and exact, `/` between
  segments, no `..`, no absolute path, no glob. A pack with nothing to carve out says nothing. A top-level key, as
  `session_homes` is, so an older yolo's decoder ignores it ([§6.3](unified-workspace-home.md#63-the-carve-out-key)).
  Databases are carved out ([UH-D7](unified-workspace-home.md#decision-ledger)).
- **Tests first:**
  - `internal/unifiedhome/compose_test.go`: `TestComposeLinksSharedChildrenAndKeepsGeneratedReal` (slice
    `.claude/{projects,settings.json}` with `settings.json` generated: `projects` moves up and becomes a link to
    `/workspace/.yolo/unified-home/.claude/projects`, `settings.json` stays real);
    `TestComposePrefixCarveOutIsComposedRecursively` (pi `.pi/agent/{sessions,npm}` with `agent/npm` carved: `agent`
    is a real directory, `agent/sessions` a link, `agent/npm` real); `TestComposeNeverAdoptsAUserLink`;
    `TestComposeLinksUnifiedChildrenAbsentFromTheSlice`; `TestComposeDeclinesWhenTheSliceCannotHoldLinks` (injected
    `symlinkat` error: no refusal, a `Declined` result with the line); `TestComposeIsIdempotent` (a second run changes
    nothing).
  - `internal/packdecl/persetupstate_test.go`: `TestPerSetupStateDecodesAndRefusesBadNames` (a `..`, an absolute name,
    a glob `*.db` and a `.lock` name each name their field), `TestAnOlderDecoderIgnoresPerSetupState` (the key
    next to an unknown contribution field, as `packs/releasedecode_test.go` does for `session_homes`).
  - `packs/` shipped-set test beside `releasedecode_test.go`: `TestShippedPacksCarveOutTheirInstalls`: pi carves
    `agent/npm`, `agent/git`, `agent/bin`, `agent/tools`; copilot carves `session-store.db`, `session-store.db-wal`,
    `session-store.db-shm`; omp carves `agent.db`, `history.db`, `models.db`, `tool-results.db`, `concept-graph.db`
    (the names its own `session_homes` already lists at `packs/omp/pack.toml:9`).
  Red command: `go test -short ./internal/unifiedhome ./internal/packdecl ./packs -run 'Compose|PerSetupState|CarveOut'`.
- **Change:** `unifiedhome/compose.go` (pure `PlanCompose(sliceDir, unifiedDir []Entry, generated, carved []string)`
  plus apply with the same `Options`); `packdecl/persetupstate.go` and the `per_setup_state` field beside
  `SessionHomeDecls` (`packdecl.go:196`) with an accessor `Manifest.PerSetupState()`; the three `pack.toml` edits.
- **Gates:** `go test -short ./internal/unifiedhome ./internal/packdecl ./packs`; `just format`;
  `go run ./cmd/yolo pack lint packs/pi packs/copilot packs/omp` is not required, but the shipped-pack load test must
  pass.
- **Fences:** no edit to `session_homes`; `packs/codex-auth`, `packs/claude` and `packs/agy` declare no carve-out here
  (codex's standalone payload is already `paths.InstalledProgramSurfaces`, UHP-D9).
- **Commit:** `feat(packdecl): per_setup_state carve-outs and the composition plan`
- **Report:** red, green, and the list of pack files changed.

### U4 — the witness class, the project lock and the exclusion rule

- **Settled:** the rule table of [§7.1](unified-workspace-home.md#71-the-rule). The refusal texts, verbatim:

  > Refusing to launch: could not ask Podman whether `podman-self` is running for this workspace. Run `podman ps -a`
  > to see, then launch again.

  > Refusing to launch: `podman-self` is running for this workspace and shares its home with `apple-container-self`.
  > Stop it with `yolo stop --setup podman-self`, then launch again.

  The two names, runtime and command come from the facts (the peer's setup name, the launching setup's name, the
  runtime that could not be asked, `runningListCommand(rt)`). "Race: launch X passes under the project lock and starts
  its keeper before releasing it; launch Y then sees X's keeper flock" is an inference the builder pins.
- **Preconditions:** `rg -n 'func \(o \*Options\) jailRuns' internal/cli/run/legacyhome.go` finds it.
- **Tests first** (`internal/cli/run/containerpeer_test.go`): red command
  `go test -short ./internal/cli/run -run 'ContainerPeer|ExclusionRule|ProjectLockIsTaken|KeeperIsRegisteredBefore|CommitHomeChecks'`.
  - `TestPeerClassComesFromTheWitness`: a held guest key gives class guest even when the peer's setup definition is
    absent from config; a held container keeper gives class container; a runtime answer gives container.
  - `TestExclusionRule`: a table over the [§7.1](unified-workspace-home.md#71-the-rule) rows through a fake
    `jailStatus`: container peer refuses; guest peer allows; unknown refuses; the same setup is not a peer; unknown
    with `RuntimeOnly` proceeds and prints one line naming the setup it could not ask; unknown with a keeper
    `keeperUnknown` still refuses. Assert the refusal text equals the two quoted lines with the names filled.
  - `TestEveryContainerLaunchTakesTheProjectLock`: `selectHome` on a Podman setup with no legacy home holds the lock
    file of `yoloruntime.FromWorkspace(ws)` (`o.homeLock.path == launchLockPath(...)`); a `macos-user` setup with none
    does not; a fresh-home launch does not.
  - `TestTheKeeperIsRegisteredBeforeTheProjectLockIsReleased`: using the harness of
    `TestTheProjectLockGoesWithTheLaunchLock` (`homelock_test.go:16`) assert the order of the events
    `keeper flock held` then `releaseHomeLock` at the point `run.go:2846` releases it. If the keeper is registered
    after the release in the tree, **stop and report**: the design's race inference is false.
  - `TestCommitHomeChecksTheExclusionFirst` (AST, shape of `legacyhomelate_test.go:55`): `commitHome`'s first
    statement calls `refuseContainerPeer`; fails if it is deleted or moved below the `homeMovePending` early return.
  - `TestCommitHomeRefusesAContainerPeerBeforeAnyWrite`: with a held `apple-container-self` keeper flock, a
    `podman-self` `commitHome` returns false and the workspace tree is byte-identical (no `.yolo/setups/podman-self`).
- **Change:** `legacyhome.go`: `jailStatus` and `jailAnswer`, `jailRuns` as a wrapper (UHP-D2); `peerFacts()` returns
  one fact per `paths.SetupStateNames` entry other than `o.Setup.Name` (name, cname, `jailStatus`) with no legacy
  name; a pure `decidePeers(self setup.Setup, facts []peerFact) peerVerdict`; `refuseContainerPeer()` prints and
  returns true on refusal; `selectHome` takes the lock per UHP-D4; `commitHome` calls the check first (UHP-D5).
- **Gates:** `go test -short ./internal/cli/run`; `just format`; `just test-fast` once at the end.
- **Fences:** `flock.go`, `keeperstate.go`, `lifecycle.go` unchanged; no new `YOLO_ALLOW_*`; no lock file.
- **Commit:** `feat(run): one container setup per workspace, decided under the project lock`
- **Report:** red, green, the race test's event order, and any existing test that failed and why.

### U5 — wire the launch: compose, merge, adopt (after U1, U2, U3, U4)

- **Settled:** composition and merge as above; the two-step legacy home ([UHP-D7](#UHP-D7)); the line ([UHP-D8](#UHP-D8));
  "A launch is the only thing that merges old homes."
- **Preconditions:** U1 to U4 landed; `git status` clean for `internal/cli/run`.
- **Tests first** (`internal/cli/run/unifyhome_test.go`), each driving `runContainer`'s caller seam the way
  `TestLaunchMovesTheLegacyHomeAndBindsTheSetupsHome` does (`setuphome_test.go:83`):
  - `TestALaunchComposesItsSliceAndASwitchKeepsTheSessions` (W-UH1's Linux shadow): a Podman launch with a session
    file in `claude/projects/-workspace/w.jsonl` leaves `unified-home/.claude/projects/-workspace/w.jsonl` once and
    `claude/projects` a link; an Apple Container launch of the same workspace sees `.claude/projects` linked to the
    same file.
  - `TestStrandedHomesMergeOnTheFirstQuiescedLaunch` (W-UH6's shadow): the three-home workspace of
    `TestMergeTheThreeStrandedHomes` plus one launch with no jail running leaves a, b, c under `unified-home`, none in
    the old homes, and prints exactly one line matching `^Merged 3 files from 2 homes into this workspace's shared home\.$`.
  - `TestAJailRunningDefersTheMergeOfOtherHomesButNotTheOwnSlice`: a held guest keeper defers the other homes (the
    line names `yolo stop`), the launching slice still composes.
  - `TestAGuestSourceIsLeftAndNamed` (UHP-D3).
  - `TestADeferredLegacyHomeBindsTheSlice` and `TestLegacyRemainderMergesIntoAPopulatedSlice` (UHP-D7).
  - `TestUnifyHomeRunsAfterPrepareWsStateAndBeforeDeliverChannel` (AST over `runContainer`, shape of
    `TestEveryArmCommitsTheHomeBeforeItWritesIt`): fails if the call is deleted or hoisted above `prepareWsState`.
  - `TestUnifyHomeDeclinesOnAReadonlyYolo`: `workspace_readonly: [".yolo"]` composes nothing, prints one line, and the
    launch proceeds.
  - **Rewrites, not repairs:** `TestALaunchWhileAJailRunsKeepsTheLegacyHome`, the deferral cases of
    `TestLegacyHomeDefersOnlyWhatItWouldMove` and `TestLegacyHomeWaitsWhileAJailRuns` (`legacyhome_test.go:133`,
    `:155`), `TestLaunchMovesTheLegacyHomeAndBindsTheSetupsHome` (`setuphome_test.go:83`) and the `currentHome` cases
    assert SUP-D21's "keep using the legacy home". Rewrite each to the new behavior (the deferred launch binds the
    slice; the remainder is adopted later). Do not relax an assertion until it passes.
  Red command: `go test -short ./internal/cli/run -run 'Unify|Stranded|Composes|Deferred|LegacyRemainder'`.
- **Change:** `run/unifyhome.go` (new): `generatedPaths(loadedPacks)` (UHP-D9), `unifyHome` (UHP-D6): ensure the
  unified home (U1), merge every other source on `paths.WorkspaceHomeStates` (read both spellings of each selected
  pack's `WritableDirs`; skip guest-owned sources and refuse as [§9.2](unified-workspace-home.md#92-the-merge) lists), then compose `o.homeDir()` with
  `unifiedhome.Compose`, printing one line. `legacyhome.go`: `currentHome` returns the slice (UHP-D7);
  `adoptLegacyHomeStep` falls to the merge when the slice is populated. `run.go:2127`: the single call.
  Link target prefix: `/workspace/.yolo/unified-home` on both container backends.
- **Gates:** `go test -short ./internal/cli/run ./internal/unifiedhome ./internal/paths`; `just format`;
  `just build-dev-bundle` then, from a throwaway workspace, the emitted launcher by absolute path with
  `--setup podman-self -- sh -c 'ls -l ~/.claude/projects'` (nested; proves composition, never reachability).
- **Fences:** `internal/render` and the host-apply path never import `unifiedhome`; `internal/cli/run/macos*`,
  `keeper*`, the sealed arm and `macosuser` unchanged; `SetupHomeState` unchanged.
- **Commit:** `feat(run): compose each container setup's home and merge stranded homes`
- **Report:** red, green, the nested launcher's `ls -l`, and every rewritten test named.

### U6 — host readers and `yolo check` (after U5)

- **Settled:** "Host-side readers ... would follow an absolute `/workspace/...` link to nothing; they read agent state
  from the unified home and install state from the slice" ([§5.2](unified-workspace-home.md#52-composition-what-a-launch-does-to-its-own-slice));
  "`yolo check` shows what waits, a read-only plan" ([§9.4](unified-workspace-home.md#94-stranded-users-and-what-they-see)).
- **Tests first:**
  - `internal/prune/agentlogs_unified_test.go`, `TestPurgeAgentLogsSweepsTheUnifiedHome`: an old file in
    `unified-home/.copilot/logs/x.log` is purged (dry run reports it, apply removes it); a link in the slice to it is
    not followed; `claude/projects` transcripts are untouched. Red command
    `go test -short ./internal/prune -run UnifiedHome`.
  - `internal/cli/check/unifiedhome_test.go`: `TestCheckReportsWhatWaitsToMerge` (a workspace with a stranded home and
    a running peer prints one line naming the source and `yolo stop`, and writes nothing) and
    `TestCheckCallsTheUnifiedHomeReport` (AST, shape of `TestCheckCallsTheBaseHomeDisclosure`,
    `basehomedisclosure_test.go:33`).
- **Change:** `prune/agentlogs.go`: after the per-home loop, open `paths.OpenUnifiedHomeRoot(ws)` and sweep the dotted
  spelling of `agentLogWorkspaceSubdirs` (`.copilot/logs`, `.gemini/tmp`, `.pi/agent/logs`, `.codex/log`), through the
  same `purgeOldFilesUnder`. `check/unifiedhome.go`: `o.noteUnifiedHomeWaiting()` calling the pure
  `unifiedhome` listing (no mutation), registered where `noteLegacyBaseHome` is called in `Check`.
- **Gates:** `go test -short ./internal/prune ./internal/cli/check`; `just format`.
- **Fences:** `prune/prune.go` dedup unchanged (installs live in the slice); `claudeview`, `configtarget.go`,
  `buildjaillogs.go` are untouched: they read slice-resident files (credentials view, `yolo-perf.log`, config).
- **Commit:** `feat(prune): sweep agent logs in the unified home; check reports a waiting merge`
- **Report:** red, green.

### U7 — the host guard, the integration test, and the landing gate (after U6)

- **Settled:** the [§10](unified-workspace-home.md#10-host-and-jail-never-share) test: over every built-in setup, through `Run`'s commit point and not only the callee.
- **Tests first:**
  - `internal/cli/run/unifiedhomehost_test.go`: `TestHostSetupNeverReachesTheUnifiedHome` (over `setup.BuiltIns()`:
    a host setup yields no composed directory and no path under `unified-home`; each jail setup's every link target
    resolves under `paths.UnifiedHome`; a guest's `HOME` is under `.yolo/setups`, never the invoking user's home);
    `TestLaunchCallsUnifyHome` (deleting the `runContainer` call fails it).
  - `internal/unifiedhome/imports_test.go`: `TestHostRendererDoesNotImportTheComposition` (parses
    `internal/render` and the host-apply package imports; fails if either reaches `internal/unifiedhome`).
  - `integration/unifiedhome_test.go` (package `integration`, `requireJail(t)`, no `t.Parallel()`): a `podman-self`
    launch composes its slice and the next launch finds the session through the link; with a held keeper flock for a
    fake `apple-container-self` jail, a `podman-self` launch is refused naming `yolo stop --setup apple-container-self`;
    with a held `.guest` key it proceeds; a prepared stranded-homes workspace migrates. The flock is the event.
  Red command: `go test -short ./internal/cli/run ./internal/unifiedhome -run 'HostSetup|CallsUnifyHome|DoesNotImport'`;
  integration: `go test -count=1 -timeout 0 ./integration -run UnifiedHome` with `YOLO_ALLOW_MISSING_PROGRAMS` unset and
  `YOLO_NO_AUTO_IMAGE_REAP=1`.
- **Gates (the landing gate, once, on the combined tree):** `python3 scripts/completion-census.py` and commit its
  output (these tests read files outside their package); `just check-ci`; the integration run above; then the full
  `just test` because this is a launch-path change. Reachability and rootless-only paths are not verified by a nested
  jail: report `podman info --format '{{.Host.Security.Rootless}}'` with the claim
  ([the blindness section](../reference/loopback-tls-reachability.md#a-nested-jail-is-structurally-blind-to-this)).
- **Fences:** the nested jail verifies nothing about `host.containers.internal`; do not claim it does.
- **Commit:** `test(run): unified home host guard and integration`
- **Report:** the red runs, `just check-ci` and `just test` outcomes, the census diff, the Rootless report.

### U8 — docs and status (after U7)

- **Change:** the rewrites listed under [Ships with](#ships-with); set the design's `Built` cells for rows that shipped
  and its `stage` to remain `DECIDED` (slices 2 to 5 are unbuilt); review `docs/plans/roadmap.md` item 3 in the same
  commit: its "buildable once its plan is written" becomes "slice 1 built; guest slice next".
- **Gates:** `uvx vantage-check` on each file touched; `just done`.
- **Commit:** `docs(design): record unified home slice 1 as built`

## Ships with

- **Tests by level.** Unit: every case above, in `internal/unifiedhome`, `internal/paths`, `internal/packdecl`,
  `internal/prune`, `internal/cli/check` and `internal/cli/run`. Integration: `integration/unifiedhome_test.go`, the
  test that catches the exclusion rule or the link target regressing end to end.
- **Tests that will break, and should:** the SUP-D21 cases named in U5, plus any `legacyhome_test.go` case asserting
  `legacyHomeDeferred` binds `.yolo/home`. Rewrite to the new behavior.
- **Docs describing the old behavior, by path:** `docs/design/named-setups.md` section 9 and SU-D6 (gets a pointer; the
  line at `named-setups.md:388` already proposes it); `docs/design/named-setups-plan.md` SUP-D21 and SUP-D34 (status
  note); `docs/reference/jail-home.md`, `docs/reference/storage-and-config.md`,
  `docs/reference/macos-user-home-tiers.md` and `userguide/reference/settings-per-setup.md` (claims that a per-setup
  home is THE per-workspace state, and that installs and logins are kept with the project): these rewrites are
  slice 5's ([§12](unified-workspace-home.md#12-rulings-this-amends)); slice 1 adds only a one-line pointer in
  `jail-home.md` so the shipped behavior is not described falsely.
- **Surfaces that are neither:** the new pack key `per_setup_state` (documented by its doc comment in
  `internal/packdecl/persetupstate.go`, which is the reference; `yolo pack --help` if it lists keys); the three
  `pack.toml` edits; the launch line in `launch.log`; `yolo check`'s waiting line. No config key, no hatch, no
  `CHANGELOG.md` entry (it is written at release time).
- **Cheap, and the implementer's:** the shape of `Step`, `Entry` and `Report`; hash function for "identical bytes"
  (any content hash, read once); the order of sources in the merge (the result is order independent up to which
  colliding file keeps the name); the wording of any line not quoted here, provided it names the next step.
- **Stop and ask** (expensive, not the implementer's): any change that lets a launch delete or overwrite a byte; any
  link target outside `<workspace>/.yolo/unified-home`; any lock file in `.yolo`; any extension of the rule to guest
  launches (slice 2); any test that sleeps.

## Traceability: the design's verification items to tests

| [§13](unified-workspace-home.md#13-verification) item | Test |
| :--- | :--- |
| Conservation (randomized collisions) | `TestMergeConservesEveryInputByteString` (U2) |
| The three-home case; identical, different, type-clash collisions; two `.from-` names | `TestMergeTheThreeStrandedHomes`, `TestMergeCollisionKinds`, `TestMergeSecondFromNameGetsANumericSuffix` (U2) |
| Links: source link and `setups` chain link; sentinel byte-identical | `TestMergeMovesALinkAndNeverTouchesItsTarget` (U2), `TestOpenUnifiedHomeRootRefusesALinkAtYolo` (U1) |
| Idempotence: fail the Nth rename, rerun | `TestMergeIsIdempotentAfterAnyFailedRename` (U2), `TestComposeIsIdempotent` (U3) |
| Composition: shared linked, generated and carved real, prefix carve-out, user link | the five `TestCompose*` cases (U3), `TestShippedPacksCarveOutTheirInstalls` (U3) |
| The rule: each [§7.1](unified-workspace-home.md#71-the-rule) row through a fake `jailRuns`, unknown included | `TestExclusionRule`, `TestPeerClassComesFromTheWitness` (U4) |
| Call sites: arm merge not deleted or hoisted above the config prompt; exclusion not deleted | `TestUnifyHomeRunsAfterPrepareWsStateAndBeforeDeliverChannel` (U5), `TestEveryArmCommitsTheHomeBeforeItWritesIt` (existing, kept), `TestCommitHomeChecksTheExclusionFirst` (U4), `TestLaunchCallsUnifyHome` (U7) |
| Refusals: running, unknown, linked chain, wrong owner, EXDEV each move nothing and print a runnable command | `TestAJailRunningDefersTheMergeOfOtherHomesButNotTheOwnSlice` (U5); the linked, owner and EXDEV cases are the existing `TestLegacyHomeLeavesWhatItCannotMove` and `TestLegacyHomeRefusesLinkedState` extended to the merge in U5 |
| The host guard of [§10](unified-workspace-home.md#10-host-and-jail-never-share) | `TestHostSetupNeverReachesTheUnifiedHome`, `TestHostRendererDoesNotImportTheComposition` (U7) |
| The project lock for every fresh launch; keeper registered before release | `TestEveryContainerLaunchTakesTheProjectLock`, `TestTheKeeperIsRegisteredBeforeTheProjectLockIsReleased` (U4) |
| [§13.2](unified-workspace-home.md#132-integration-nested-jail-podman) integration: compose then find, refused with held flock, proceeds with `.guest`, stranded workspace migrates | `integration/unifiedhome_test.go` (U7) |

## Mac witnesses owed

Run by the owner's Mac agent on main after slice 1 lands; none holds anything off main, and a failure is fixed
forward. Their commands and expected outputs are in [the design's Mac checks](unified-workspace-home.md#mac-checks).

- **W-UH1** (sessions follow a switch, Apple Container to Podman): **owed.** Linux shadow: `TestALaunchComposesItsSliceAndASwitchKeepsTheSessions`.
- **W-UH5** (the pairing rule, including the stopped Podman machine): **owed.** Linux shadow: `TestExclusionRule`.
- **W-UH6** (the stranded home): **owed.** Linux shadow: `TestStrandedHomesMergeOnTheFirstQuiescedLaunch`.

W-UH2, W-UH3 and W-UH4 belong to slices 2 and 3 and do not gate this slice.

## Don't

- Do not move or rename `renameNoReplaceBetween` or `legacyHomeRename`; pass the rename in (UHP-D1).
- Do not read `packload.Embedded*` to decide which directory names are shared; the selected packs' `WritableDirs` is
  the only input ([UH-D9](unified-workspace-home.md#decision-ledger)).
- Do not add a `yolo home merge` verb ([OQ-UH2](unified-workspace-home.md#OQ-UH2)).
- Do not compose a guest's slice or merge a guest source in this slice (UHP-D3); `seatbelt-yolo-account` stays on its
  private whole directory until W-UH4.
- Do not put a cache of liveness answers in front of `jailStatus`: a stale "not running" lets a second container in.
- Do not add `/workspace/.yolo/unified-home` as a mount. Links, never mounts, on both container backends.

## Blockers

None. Every question the slice reaches is ruled in the design's
[decision ledger](unified-workspace-home.md#decision-ledger). The Mac witnesses above gate no landing.
