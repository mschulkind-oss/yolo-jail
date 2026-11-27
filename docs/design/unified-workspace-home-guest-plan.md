---
title: "Plan: build slice 2 of the unified workspace home (the guest joins)"
date: 2026-11-27
status: accepted
stage: DECIDED
next: "Build UG1 (the pure package: relative links, laid-link recognition, several owners) and UG2 (the rule and the lock for guest launches) in parallel worktrees; then UG3, UG4, UG5, UG6"
depends-on: []
tags: [plan, setups, home, sessions, macos-user, concurrency]
summary: "The build hand-off for slice 2 of the unified workspace home: a guest (seatbelt-self; seatbelt-yolo-account behind a gate) composes its slice into the unified home with relative links, merges stranded guest homes, is checked by the one-container-plus-one-guest rule, and takes the project lock. Six units, each one commit, tests first; Mac witnesses W-UH4 (amended), W-UH5, W-UH7 to W-UH10 are owed to the owner's Mac agent."
vantage:
  status-chip: true
---

# Plan: slice 2 of the unified workspace home (the guest joins)

**Status:** 2026-11-27. Completed against the tree, written against `27ffeec54` (slice 1 built, U1 to U8).
**Design:** [`unified-workspace-home.md`](unified-workspace-home.md), slice 2 of
[its slices](unified-workspace-home.md#14-slices): links from a guest's slice into the unified home, one container
setup plus one guest allowed together, a second guest refused, stranded guest homes merged, and the yolo-account
gate. **Model:** [`unified-workspace-home-plan.md`](unified-workspace-home-plan.md) (slice 1, built); its
decisions `UHP-D1` to `UHP-D25` still hold and this file amends the ones it names.

**Precedence.** The design wins on behavior. The tree wins on fact, for cosmetic drift only (a moved line, a renamed
helper with the same role): follow the tree and say so in the report and the commit. This file is advice and is the
first thing to be wrong; never twist the code to match it.

**Stop and report, do not commit,** when: a test does not fail the way its task predicts; a file a task names is dirty
with someone else's work; a symbol a task names is gone; the fix needs a file outside the task's fence; a gate fails
outside the task's scope. Other agents commit on `main` concurrently: stage by path with `git add -N <paths>` then
`git commit -- <paths>`, never `git add -A`.

**This environment is Linux.** Nothing here runs Seatbelt. Every unit is verified by unit tests over real
directories (`EvalSymlinks(t.TempDir())`) and by AST pins on call sites; what only a Mac can answer is a
[witness](#mac-checks), run on main after landing, and none of them holds a landing.

## Settled, do not re-open

Quoted from the design so a task never needs it open.

- **The guest's links** ([§5.2](unified-workspace-home.md#52-composition-what-a-launch-does-to-its-own-slice)): the
  slice is `HOME`, dotted (`.claude/`); for each workspace-scope state directory of a SELECTED pack, shared children
  are links into `<workspace>/.yolo/unified-home`, generated and carved-out paths stay real, a link already in the
  slice is never adopted, followed or rewritten, one writer, nothing deleted or overwritten. Links, never mounts.
- **The rule** ([§7.1](unified-workspace-home.md#71-the-rule), [OQ-UH1](unified-workspace-home.md#OQ-UH1),
  [OQ-UH3](unified-workspace-home.md#OQ-UH3)): at most one container setup per workspace; a container and a guest may
  run together; **a second guest is refused** (`seatbelt-self` and `seatbelt-yolo-account` never run at once in one
  workspace); the same setup attaches or opens another session as today; a peer that cannot be asked refuses, unless
  every keeper and session record says not running and only the runtime could not be asked, then one line and go on.
  The project lock is taken by every fresh launch and held until the jail answers. No lock file in `.yolo`, no hatch.
- **The yolo account** ([§7.4](unified-workspace-home.md#74-the-yolo-account), [UH-D13](unified-workspace-home.md#decision-ledger)):
  `seatbelt-yolo-account` joins the unified home only after W-UH4 shows both users can read and write what the other
  made; until then it keeps a private whole state directory and says so on the launch line.
- **Migration** ([§9](unified-workspace-home.md#9-migration-one-merge-for-every-home-on-disk)): the merge reads every
  home on disk, both spellings (`.claude` and `claude`); a guest's slice is an ordinary source; refused while any jail
  of the project runs ("could not ask" counts as running); a source that is not a real directory owned by the user
  (or, for the yolo account, by it) is left with the `mv -n`/`chown` to run. Only a launch merges.
- **Host and jail never share** ([§10](unified-workspace-home.md#10-host-and-jail-never-share)): a guest's `HOME` is
  under `.yolo/setups`, never the invoking user's home, even in run-as-you mode; link targets stay inside the
  workspace's unified home; the host renderer never imports the composition.
- **Out of this slice:** the alias set ([OQ-UH4](unified-workspace-home.md#OQ-UH4), slice 3, gated on W-UH2 and
  W-UH3) and workspace sets (slice 4). Until the alias slice a guest names its sessions by its real path
  (`-Users-Shared-yolo-uh`), the container by `-workspace`: they share the directory and not yet each other's lists.

## Plan decisions

Mechanism choices the design leaves open, decided here. None contradicts a ruling. **Stop and report** if one does.

| ID | Decision | Why |
| :--- | :--- | :--- |
| <a id="UHG-D1"></a>UHG-D1 | `unifiedhome.Merge` skips, silently and uncounted, a source link whose target is exactly what a launch lays at that position (`Options.Laid []LinkForm`, each form's `Target(depth, unifiedPath)`); a link anywhere else still moves as a link. The launch passes both forms, so a composed container slice and a composed guest slice both re-merge as a no-op. | **A defect in the built slice 1, found by reading:** `mergeOtherHomes` merges any stopped other slice, a composed slice holds a link `projects` where the unified home holds a directory, and `PlanLevel` answers `OpKeepBoth`, moving the link into the unified home as `projects.from-<setup>` and stripping the slice. Recognizing a link by the exact string yolo writes is [OQ-UH4](unified-workspace-home.md#OQ-UH4)'s reading of the symlink principle: yolo may interpret a link it laid by a name rule it owns. |
| <a id="UHG-D2"></a>UHG-D2 | **A guest's links are relative** to the link's own directory (`../../../../unified-home/.claude/projects` from `.claude/`), computed from the slice's depth under `.yolo` (`paths.HomeStateRel`) and the link's depth in the slice. Container links stay absolute (`/workspace/...`), as built. `ComposeSpec` carries exactly one of `HomePath` (absolute) and `FromSliceRoot` (relative, the unified home as seen from the slice root). **This refines UH-D4's and [§5.2](unified-workspace-home.md#52-composition-what-a-launch-does-to-its-own-slice)'s "the real path in the guest"**; the design's two phrases are amended in this plan's commit. | Same mechanism (a link, no mount) and the same reason UH-D4 gives. A relative target survives a moved or renamed workspace and needs no resolved real path, which an absolute one would not: `Compose` never rewrites a link, so a stale absolute link would dangle for good. The kernel resolves a relative target from a REAL directory (`~/.claude` is real in the guest, unlike Podman's bind), so the `..` trap of `darwinhomelayout.go` does not apply. |
| <a id="UHG-D3"></a>UHG-D3 | The guest class is `Setup.Kind() == setup.KindGuest` (`setup.go:86`); a setup joins the unified home when its kind is `KindJail` or `KindGuest`; a host setup never does. `refuseContainerPeer` is renamed `refusePeer` and `runsAsContainer` stays. `selfClass` is unchanged. | The method now checks guests too; a name that says "container" misleads the next reader. The AST pin `TestCommitHomeChecksTheExclusionFirst` follows the rename. |
| <a id="UHG-D4"></a>UHG-D4 | Every guest launch takes the project lock in `selectHome` and releases it in `recordKeyedSession` (`keeperspawn.go:1259`), after the record attempt. `Run`'s deferred `releaseHomeLock` stays the backstop. Supersedes [UHP-D4](unified-workspace-home-plan.md#UHP-D4)'s guest carve-out. | `recordKeyedSession` is the ONE function all three guest paths call (a joining launch `:1098`, a keeper start `:1520`, a keeperless session `:1294`), and the record it opens is what `jailStatus` reads (`liveKeyedSessions`). A peer that takes the lock next therefore sees this session. Without the release a keeperless session would hold the lock for its whole life, the concern UHP-D4 named. |
| <a id="UHG-D5"></a>UHG-D5 | The guest's `unifyHome` runs in the macos-user arm of `Run` straight after the existing `commitHome` (`run.go:771`) and before `armMacosUser`. A sealed guest build (`runSealedMacosUser`, `run.go:595`) is checked by the rule (it already calls `commitHome`) and composes nothing; a dry run and `--fresh-home` compose nothing. | The first host write into the slice (the credential view, the env files) follows `commitHome`, and composition must precede every reader of the slice. A sealed build narrows the pack selection to its contributor and is a throwaway home. |
| <a id="UHG-D6"></a>UHG-D6 | For a guest, `unifyHome` makes each missing slice state directory (`Mkdir` mode `0o755` through `own`, the no-link slice root; `EEXIST` is fine) BEFORE composing. Podman and Apple Container need no such step: `prepareWsState` made them. | `Compose` returns quietly when the slice directory is absent. Without this the guest's first launch links nothing and a switch from a container waits one launch. The guest's own bootstrap `MkdirAll`s the same paths later (`DeriveDarwinWorkspaceHome`), which finds them real. |
| <a id="UHG-D7"></a>UHG-D7 | `sliceStateRel(rt, dir)` is the dotted spelling for `rt` `container` and `macos-user` and the dot-stripped one for `podman`; `unifyHome` uses it in place of `wsStateHomeRel(rt, dir)`. `wsStateHomeRel` is untouched. | `wsStateHomeRel` strips the dot for every `rt` but `container` (`prepare.go:889`), so called with `macos-user` it names `claude/`, a directory the guest never reads. |
| <a id="UHG-D8"></a>UHG-D8 | **The yolo-account gate is a package constant in `internal/cli/run`, `unifiedHomeJoinsYoloAccount = false`,** read by a test seam, not config and not an environment variable. While false: a `seatbelt-yolo-account` launch composes and merges nothing and prints the line below once; every other launch leaves the account's slice as a source (`left`, with the line below). The flip is a one-line commit after W-UH4 reports pass. | UH-D13 gates the account on a measurement, and a measurement is a code fact, not a user choice. A hatch is for broken user config, never for a yolo gap. |
| <a id="UHG-D9"></a>UHG-D9 | `unifiedhome.Options.OwnerUIDs []uint32` replaces `OwnerUID *uint32` (nil still means unchecked). `mergeOneHome` passes the invoking user's uid, plus `legacyHomeAccountUID()` when the source setup runs as the yolo account. Amends [UHP-D12](unified-workspace-home-plan.md#UHP-D12). | [§9.2](unified-workspace-home.md#92-the-merge) accepts a source owned by the user or, for the yolo account, by it; one pointer cannot say both. Only reachable once UHG-D8 opens. |
| <a id="UHG-D10"></a>UHG-D10 | The "a guest's home joins later" skips in `mergeOtherHomes` (`unifyhome.go:281`) and `UnifiedHomeWaiting` (`unifyhomewaiting.go:42`) are removed; a guest slice is an ordinary source, waiting while its jail runs (`projectJails` already asks the guest keys). Supersedes [UHP-D3](unified-workspace-home-plan.md#UHP-D3)'s "a guest's slice is LEFT". | The design's migration reads every home on disk. |
| <a id="UHG-D11"></a>UHG-D11 | Composing the launching guest's own slice is allowed while another session of the SAME setup runs. There is no refusal and no new probe. | macos-user has no attach (`AcquireWorkspaceLockFor`'s comment), so a second terminal of one setup always launches. Every composition step is a rename that refuses to replace or an `rmdir` that fails on a non-empty directory; an open file follows its inode and a path through the new link reaches the same place. |
| <a id="UHG-D12"></a>UHG-D12 | The gated account's launch line reads: `This setup keeps a private home: the yolo account's access to this workspace's shared home is not verified yet, so its sessions are not shared with the workspace's other setups. A later release shares them.` The source line reads the same facts with the setup's name: `The seatbelt-yolo-account setup's home stays private until the yolo account's access to the shared home is verified, so its sessions stay where they are.` | Both name who can act (a later release) and no user step, because there is none: [the happy path](../reference/happy-path-principle.md) allows "names who can". |

## Map

| Path | Change |
| :--- | :--- |
| `internal/unifiedhome/compose.go` | `ComposeSpec.FromSliceRoot`; relative targets in `composer.link` (UG1) |
| `internal/unifiedhome/merge.go` | `LinkForm`, `Options.Laid`, `Options.OwnerUIDs` (replaces `OwnerUID`); skip a laid link (UG1) |
| `internal/unifiedhome/*_test.go` | new cases; `OwnerUID` cases rewritten (UG1) |
| `internal/cli/run/containerpeer.go` | `joinsUnifiedHome`; `refusePeer` (renamed); skip only host, dry run, fresh home (UG2) |
| `internal/cli/run/legacyhome.go` | `selectHome` takes the lock for every joining setup; `commitHome` calls `refusePeer` (UG2) |
| `internal/cli/run/keeperspawn.go` | `recordKeyedSession` releases the project lock (UG2) |
| `internal/cli/run/unifyhome.go` | guest: `sliceStateRel`, relative form, make slice dirs, gate, guest sources, `Laid`, `OwnerUIDs` (UG3) |
| `internal/cli/run/unifyhomewaiting.go` | drop the guest skip; honor the gate (UG3) |
| `internal/cli/run/run.go` | one call, `o.unifyHome`, after the macos-user arm's `commitHome` (~`:771`) (UG4) |
| `internal/cli/run/unifiedhomehost_test.go` | rewrite: a guest now composes (UG4) |
| `integration/unifiedhomeguest_test.go` | new, `requireMacosUser`, runs in the macOS nightly (UG5) |
| `docs/design/unified-workspace-home.md`, `docs/plans/roadmap.md`, `docs/reference/jail-home.md`, `docs/reference/storage-and-config.md`, `userguide/guides/storage.md`, `userguide/guides/macos.md` | status, the guest in the prose (UG6) |

## Reuse before you write

- **The slice-1 harness.** `newUnifyFixture(t, setupName)`, `newUnifyFixtureIn(t, ws, setupName)`, `putUnify`,
  `f.unify(t, rt, packs...)`, `f.unified(rel)`, `mustReadlink`, `readFileT`, `walkEntries`, `packsFixture(t, "claude")`
  (`unifyhome_test.go`, `unifiedhomehost_test.go`); `peerOptions(ws, setup, stderr)`, `holdKeeperKey`,
  `makePeerSetup`, `treeListing` (`containerpeer_test.go`). They take any built-in setup, a guest included.
- **The pure package's fixtures.** `tree(t)`, `put`, `open`, `read`, `snapshot`, `stamp` (`merge_test.go`);
  `newCompose`, `linkTarget`, `isReal`, `exists` (`compose_test.go`; its `run` sets `HomePath`, so a relative case
  calls `Compose` directly).
- **The rule is already pure.** `decidePeers(self, facts)` handles guest-beside-guest and guest-beside-container
  (`TestExclusionRule` has both rows); UG2 changes who CALLS it, not it.
- **Liveness for guests.** `jailStatus` already asks the `.macos-user` and `.guest` keys and labels the class
  (`legacyhome.go`, `jailStatus`); `peerFacts` already lists every setup directory.
- **Keeper and session fixtures.** `openKeyedSessionRecord`, `closeKeyedSessionRecord` (`keeperstate.go:681`),
  `liveKeyedSessions` (`:725`), `macosUserKeying{key, record}` (`macosuserkeeper_test.go:458`).
- **Owner uid.** `legacyHomeAccountUID` (`legacyhome.go`, `macosuser.SandboxUID`); the test stands it in.
- **Slice depth.** `paths.HomeStateRel(workspace, home)` returns `setups/<name>/home`; its segment count is the number
  of `..` from the slice root to `.yolo`.
- **macOS gated integration.** `requireMacosUser(t)` (`integration/macosusergate_test.go:500`) and its name rule:
  every test starts `TestMacosUser`. Copy the launch shape of `TestMacosUserTwoWorkspacesRunAtOnceAsTheAccount`
  (`integration/macosuseraccounthome_test.go`): `macosUserWorkspace`, `runMacosUser`, `macosUserHome`.

## Traps

- **`wsStateHomeRel("macos-user", ".claude")` is `claude`.** It strips the dot for every runtime but `container`
  (`prepare.go:889`). A guest composed through it merges and links the wrong directory and every test on a podman
  spelling passes. UHG-D7.
- **A composed slice re-merging is a slice-1 defect today** (UHG-D1): a second container setup launched while the
  first is stopped moves `projects` into the unified home as `projects.from-<setup>`. UG1's first test is red on the
  tree for exactly that. A stray `*.from-*` link in a unified home that points back into it holds no data; it is not
  cleaned automatically.
- **`Compose` returns quietly when the slice directory is absent.** UHG-D6 exists because of it.
- **The guest's bootstrap runs OUTSIDE Seatbelt as the account and refuses a write by path through a link it did not
  lay** (`homeFileThroughLayout`, `darwinhomelayout.go:661`; its message tells the user to `sudo rm` the link). A
  composed link is such a link. Nothing yolo renders is written beneath one (generated paths stay real), so the
  common path never meets it. A user's `host_files` entry whose destination lies beneath a shared child of a state
  directory would be refused with a remedy the next launch undoes. **Do not teach `homeFileThroughLayout` about
  composed links in this slice;** it is a different security surface, and the edge is filed as a backlog item by UG6.
- **The in-guest bootstrap `MkdirAll`s state directories as the account**; a directory the host made first (UHG-D6)
  is owned by the invoking user. In run-as-you mode that is the same user. In account mode the account writes into it
  by the inherited ACL of the shared root, which is the very thing W-UH4 measures; that is why the account is gated.
- **A keeperless guest session holds no keeper lock**, only its record (JL-D42). `jailStatus` reads the record, so a
  test of the rule uses `openKeyedSessionRecord`, and `liveKeyedSessions` deletes a stale record as a side effect:
  never rely on one surviving a probe.
- **APFS is case-insensitive by default.** The plan maps are case-sensitive; what holds is the rename that refuses to
  replace and `move`'s `EEXIST` fallback (`merge.go`), which keeps both. Linux unit tests cannot see it; nothing here
  needs a case-folding fix.
- **`runSealedMacosUser` returns before the call site UG4 adds.** That is intended (UHG-D5); a pin test keeps it so.
- **The jail exports `YOLO_ALLOW_MISSING_PROGRAMS=1`** and fails three `internal/cli` tests; run gates with it unset,
  and `YOLO_NO_AUTO_IMAGE_REAP=1` for any integration run. `git add` before any nested-jail build.

## Tasks

Build order: **UG1** and **UG2** are independent and touch disjoint files, so they run in parallel worktrees and land
serially. **UG3** is after UG1. **UG4** is after UG2 and UG3. **UG5** is after UG4 and is the landing gate. **UG6** is
after UG5. Each unit is one commit and ends green.

### UG1 — the pure package: relative links, laid-link recognition, several owners (independent)

- **Settled:** UHG-D1, UHG-D2, UHG-D9. A guest link names the unified home relative to its own directory; a source
  link yolo laid is neither moved nor counted; nothing is deleted or overwritten.
- **Preconditions:** `git status` clean for `internal/unifiedhome`; `rg -n 'OwnerUID' internal` finds `merge.go` and
  `unifyhome.go` only (plus tests).
- **Tests first** (`internal/unifiedhome/merge_test.go`, `compose_test.go`):

  ```go
  // red today: Merge moves both laid links into dst as projects.from-x.
  func TestMergeSkipsALinkYoloLaidAndMovesAUserLink(t *testing.T) {
  	dst, src := tree(t), tree(t)
  	put(t, dst, ".claude/projects/a.jsonl", "a")
  	for _, d := range []string{"claude", ".claude"} {
  		if err := os.MkdirAll(filepath.Join(src, d), 0o755); err != nil {
  			t.Fatal(err)
  		}
  	}
  	laidAbs := "/workspace/.yolo/unified-home/.claude/projects"
  	laidRel := "../../../../unified-home/.claude/projects"
  	for p, target := range map[string]string{"claude/projects": laidAbs, ".claude/projects": laidRel, "claude/notes": "/elsewhere"} {
  		if err := os.Symlink(target, filepath.Join(src, p)); err != nil {
  			t.Fatal(err)
  		}
  	}
  	rep, err := Merge(open(t, dst), ".claude", Source{Label: "x", Root: open(t, src), Dirs: []string{"claude", ".claude"}},
  		Options{Laid: []LinkForm{{HomePath: "/workspace/.yolo/unified-home"}, {FromSliceRoot: "../../../unified-home"}}})
  	if err != nil {
  		t.Fatal(err)
  	}
  	if exists(dst, ".claude/projects.from-x") || exists(dst, ".claude/projects.from-x.2") {
  		t.Fatal("a link yolo laid was carried into the unified home")
  	}
  	if linkTarget(t, src, "claude/projects") != laidAbs || linkTarget(t, src, ".claude/projects") != laidRel {
  		t.Fatal("a laid link left its slice")
  	}
  	if linkTarget(t, dst, ".claude/notes") != "/elsewhere" || rep.Moved != 1 || rep.KeptBoth != 0 {
  		t.Fatalf("a user's link must still move as a link: moved %d, kept %d", rep.Moved, rep.KeptBoth)
  	}
  }
  ```

  Also: `TestComposeLaysARelativeLinkFromTheSliceRoot` (`FromSliceRoot: "../../../unified-home"`,
  slice dir `.claude`, unified dir `.claude`: `.claude/projects` links to `../../../../unified-home/.claude/projects`
  (one `..` for the link's own directory plus the three of `FromSliceRoot`); a recursed child, pi's
  `.pi/agent/sessions`, links to `../../../../../unified-home/.pi/agent/sessions`, one more `..` for its extra directory; `HomePath` and `FromSliceRoot` both set, or neither, is an error naming both);
  `TestComposeRelativeLinkResolvesToTheUnifiedDirectory` (lay it in a real tree shaped
  `.yolo/setups/s/home` and `.yolo/unified-home`, `filepath.EvalSymlinks` of the link equals the unified directory);
  `TestMergeLeavesASourceOwnedByNeitherUID` (`OwnerUIDs: []uint32{other}` leaves it, the current uid in the list moves
  it; rewrite the existing `OwnerUID` cases to the slice, do not delete them). Red command:
  `go test -short ./internal/unifiedhome -run 'SkipsALinkYoloLaid|RelativeLink|NeitherUID'`.
- **Change:** `merge.go`: `type LinkForm struct{ HomePath, FromSliceRoot string }` with
  `func (f LinkForm) Target(depth int, unifiedPath string) string` (`depth` is the number of directories between the
  slice root and the link, `unifiedPath` is `<unified dir>/<rel>/<name>`; a relative form is
  `strings.Repeat("../", depth) + FromSliceRoot + "/" + unifiedPath`; the one place both spellings are built, used by
  `composer.link` too); `Options.Laid []LinkForm`; in `levelOnly`,
  before `PlanLevel`, drop from `srcE` each `KindLink` whose `Readlink` equals some form's `Target` for that position
  (`m.base`, `rel`, `dstDir`; read with `unix.Readlinkat`, never followed). `compose.go`: `ComposeSpec.FromSliceRoot`;
  `link` builds the target through `LinkForm`. `Options.OwnerUIDs` in `Merge`'s ownership check. Keep the package's
  imports: `imports_test.go` fences it from `internal/paths`, so the relative prefix arrives as data.
- **Gates:** `go test -short ./internal/unifiedhome`, `go vet ./internal/unifiedhome`.
- **Fences:** `internal/cli`, `internal/paths`. `internal/cli/run/unifyhome.go` still names `OwnerUID`: leave it
  compiling by changing that one field use to `OwnerUIDs: []uint32{uid}` in this commit and nothing else there.
- **Commit:** `feat(unifiedhome): relative links, laid-link recognition, several owners`
- **Report:** the red run (it must fail on `projects.from-x`), the green run, and whether `OwnerUID` appeared anywhere
  but the two files named.

### UG2 — the rule and the lock reach guest launches (independent)

- **Settled:** [the rule](#settled-do-not-re-open) and UHG-D3, UHG-D4. A guest launch beside a running guest of another
  setup is refused with the exact text below; beside a running container it proceeds; the same setup is no peer.
  Refusal (from `decidePeers`, unchanged): ``Refusing to launch: `seatbelt-self` is running for this workspace and
  shares its home with `seatbelt-yolo-account`. Stop it with `yolo stop --setup seatbelt-self`, then launch again.``
- **Preconditions:** `git status` clean for `internal/cli/run/{containerpeer,legacyhome,keeperspawn}.go`;
  `rg -n 'refuseContainerPeer' internal` finds `containerpeer.go`, `legacyhome.go` and tests only.
- **Tests first:**
  - `containerpeer_test.go`: `TestCommitHomeRefusesAGuestPeerBeforeAnyWrite` (copy of
    `TestCommitHomeRefusesAContainerPeerBeforeAnyWrite`: `makePeerSetup(ws, "seatbelt-self")`, `holdKeeperKey(t,
    keeperKey(yoloruntime.JailName(ws, "seatbelt-self"), keeperNotchMacosUser))`, a `seatbelt-yolo-account` launch's
    `commitHome()` is false and `treeListing` is unchanged); `TestCommitHomeLetsAGuestLaunchBesideARunningContainer`
    (hold the container's bare key; `commitHome()` is true, nothing printed); `TestAGuestLaunchBesideItsOwnSetupIsNoPeer`
    (hold its own setup's key; true). Rewrite `TestEveryContainerLaunchTakesTheProjectLock` to
    `TestEveryJailLaunchTakesTheProjectLock` with `seatbelt-yolo-account` and `seatbelt-self` wanting `true` and a host
    case wanting `false`. Rewrite `TestSelectHomeDecidesUnderTheLaunchLock` (`setuphome_test.go:330`): the lock is held
    whether or not a legacy home exists. Add `TestRecordingAGuestSessionReleasesTheProjectLock`: `o.selectHome()` on a
    guest, set `o.macosUserKey = &macosUserKeying{key: keeperKey(o.jailName(), keeperNotchMacosUser)}`, call
    `o.recordKeyedSession(o.macosUserKey.key, false)`, then `o.homeLock.isClosed()` is true and `liveKeyedSessions(key)`
    holds one record. Extend `TestCommitHomeChecksTheExclusionFirst` to the new name. Add
    `TestEveryGuestRecordPathReleasesTheLock`: an AST check that the three callers of `recordKeyedSession` are the
    only ones and that the function itself calls `releaseHomeLock` (the call-site shape of `funcCalls`,
    `containerpeer_test.go:264`).
  - Red: `go test -short ./internal/cli/run -run 'GuestPeer|BesideARunningContainer|BesideItsOwnSetup|EveryJailLaunch|SelectHomeDecides|ReleasesTheProjectLock|RecordPathReleases'`.
- **Change:** `containerpeer.go`: `func joinsUnifiedHome(s setup.Setup) bool` (`Kind()` is `KindJail` or `KindGuest`);
  `refusePeer` (renamed) skips `o.DryRun`, `o.FreshHome` and `!joinsUnifiedHome`; rewrite the file header's "a guest
  is not checked" sentences. `legacyhome.go`: `selectHome` takes the lock for `joinsUnifiedHome(o.Setup)` (replacing
  `runsAsContainer`, and deleting the duplicated comment paragraph above it); `commitHome` calls `refusePeer`.
  `keeperspawn.go`: `recordKeyedSession` ends with `o.releaseHomeLock()`, with the comment that this is the point a
  peer's `jailStatus` can see the session.
- **Gates:** `go test -short ./internal/cli/run`, with `YOLO_ALLOW_MISSING_PROGRAMS` unset.
- **Fences:** `unifyhome.go`, `run.go`, `internal/macosuser`. Do not add a probe, a cache of answers or a lock file.
- **Commit:** `feat(run): the one-container-plus-one-guest rule and the project lock reach guest launches`
- **Report:** the red run, the green run, and whether any macos-user test outside the named ones changed result.

### UG3 — the launch's merge and composition understand a guest (after UG1)

- **Settled:** UHG-D6 to UHG-D10, UHG-D12, and [§5.2](unified-workspace-home.md#52-composition-what-a-launch-does-to-its-own-slice) for the guest. A guest slice is composed in its dotted spelling
  with relative links; the spelling it does not read (`claude/`) merges in; a stranded guest slice merges into the
  unified home when no jail of the project runs.
- **Preconditions:** UG1 landed (`rg -n 'FromSliceRoot' internal/unifiedhome` is not empty).
- **Tests first** (`internal/cli/run/unifyhome_test.go`, `unifyhomewaiting_test.go`), each over
  `newUnifyFixture(t, "seatbelt-self")` and `f.unify(t, "macos-user")`:
  - `TestAGuestFirstLaunchLinksTheUnifiedChildren`: a Podman slice's session is already in the unified home
    (`putUnify(t, paths.UnifiedHome(f.ws), ".claude/projects/-workspace/w.jsonl", "s")`); the guest slice has no `.claude`; after `f.unify` the slice's
    `.claude/projects` is a link whose target is exactly `../../../../unified-home/.claude/projects` and
    `filepath.EvalSymlinks` of it is `f.unified(".claude/projects")` (UHG-D6, UHG-D2).
  - `TestAGuestSecondLaunchAdoptsWhatTheAgentWrote`: a real `.claude/projects/-Users-Shared-yolo-uh/g.jsonl` in the slice
    moves to the unified home and a link takes its place; one line `Merged 1 files from 1 homes into this workspace's
    shared home.`; generated `.claude/settings.json` stays real (UHG-D7: a test that fed `rt` `macos-user` and got the
    dot stripped would find `claude/`).
  - `TestAStrandedGuestSliceMergesWhenNothingRuns` (rewrite of `TestAGuestSourceIsLeftAndNamed`, which asserts the
    guest stays: rewrite to the new behavior, do not relax it): launching `podman-self` with a stopped
    `seatbelt-self` slice holding `.claude/projects/g.jsonl` moves it; with `holdKeeperKey` on the guest's
    `.macos-user` key it stays and the line names `seatbelt-self` and `` `yolo stop` ``.
  - `TestAComposedSliceReMergesAsANoOp` (both a container and a guest slice, UHG-D1): compose slice A, launch B, A's
    links and the unified tree are byte-identical and no `.from-` name exists.
  - `TestTheYoloAccountStaysPrivateWhileTheGateIsClosed`: `seatbelt-yolo-account`'s `unifyHome` changes nothing in its
    slice and creates no unified home, and prints the UHG-D12 line once; as a source its slice is left with the
    second line. With the gate seam set true it composes, and `OwnerUIDs` carries the account's uid (stand
    `legacyHomeAccountUID`). A source owned by a third uid is left.
  - `unifyhomewaiting_test.go`: a guest slice with pending files is listed; the gated account's is not.
  - Red: `go test -short ./internal/cli/run -run 'AGuest|StrandedGuest|ComposedSliceReMerges|YoloAccountStays|UnifiedHomeWaiting'`.
- **Change:** `unifyhome.go`: `unifyHome` admits `joinsUnifiedHome` setups; add `sliceStateRel(rt, dir)` (UHG-D7); the
  composition spec is `FromSliceRoot` (`strings.Repeat("../", n)+paths.UnifiedHomeName`, `n` from
  `paths.HomeStateRel`) for a guest and `HomePath: jailUnifiedHome` for a container; a guest `Mkdir`s each missing slice
  state directory first (UHG-D6); pass `Laid: {HomePath jailUnifiedHome, FromSliceRoot relative}` to every `Merge` and
  `Compose`; `mergeOneHome` passes `OwnerUIDs`; delete the guest skip in `mergeOtherHomes` and replace it with the gate
  (UHG-D8): `var unifiedHomeJoinsYoloAccount = false` (a `var` only so the test seam can set it; no code outside the
  test assigns it). `unifyhomewaiting.go`: same two edits. Update both files' header comments, which say a guest joins
  later.
- **Gates:** `go test -short ./internal/cli/run ./internal/cli/check ./internal/unifiedhome`.
- **Fences:** `run.go` (UG4 adds the call), `containerpeer.go`, `internal/macosuser`, `internal/entrypoint`.
- **Commit:** `feat(run): a guest's slice composes into the unified home and stranded guest homes merge`
- **Report:** the red run, the green run, and whether the guest's second-launch line matches the one quoted.

### UG4 — the guest arm calls it, and the guard test follows (after UG2 and UG3)

- **Settled:** UHG-D5, and [§10](unified-workspace-home.md#10-host-and-jail-never-share): a guest's `HOME` is its own
  slice under `.yolo/setups`; every link a guest's slice is given resolves under the workspace's unified home; a host
  setup reaches nothing.
- **Preconditions:** UG2 and UG3 landed; `rg -n 'o.unifyHome\(' internal/cli/run/run.go` finds the container call only.
- **Tests first** (`internal/cli/run/unifiedhomehost_test.go`):
  - Rewrite `TestHostSetupNeverReachesTheUnifiedHome`: its guest branch (`s.Kind() != setup.KindJail`) asserted a guest
    composes nothing. A guest now composes; only `KindHost` yields no composed directory and no path under the unified
    home. For a guest the per-link check resolves the RELATIVE target with `filepath.Join(filepath.Dir(link), target)`
    and requires the result under `paths.UnifiedHome(f.ws)`; keep the "a guest's `HOME` is under `.yolo/setups`, never
    the invoking user's home" assertion, and keep `links == 0` an error for every jail and guest setup EXCEPT
    `seatbelt-yolo-account` while the gate is closed (assert it composes nothing and prints the UHG-D12 line). Choose
    `rt, dir` for a guest as `"macos-user", ".claude"`. This is a rewrite to the new behavior, not a repair.
  - Rewrite `TestLaunchCallsUnifyHome`: callers are exactly `runContainer` once and `Run` once, and the `Run` call
    comes after the `commitHome` call in the macos-user arm and before `armMacosUser` (an AST order check on the
    `if rt == "macos-user"` block). It must fail if the call is deleted.
  - `TestSealedAndDryGuestLaunchesComposeNothing`: `runSealedMacosUser` contains no `unifyHome` call; a dry-run
    `Options` composes nothing.
  - Red: `go test -short ./internal/cli/run -run 'HostSetupNever|CallsUnifyHome|SealedAndDry'`.
- **Change:** `run.go`, in the macos-user arm directly after `if !o.DryRun && !o.commitHome() { return 1 }` (~`:771`):
  `sp = o.Perf.Span("launch.unify_home"); o.unifyHome(cfg, o.homeDir(), staged.packs, rt); sp.End()`. `rt` is
  `"macos-user"` there. Nothing else in `run.go`.
- **Gates:** `go test -short ./internal/cli/run ./internal/unifiedhome`; then this unit is the Linux half of the
  landing gate, which UG5 runs.
- **Fences:** do not move the call above `commitHome` (a refused launch must write nothing) or into
  `runSealedMacosUser`; do not touch the container call at `run.go:2150`.
- **Commit:** `feat(run): the macos-user arm composes the guest's home into the unified home`
- **Report:** red and green, and the line number the call landed at.

### UG5 — the macOS integration test and the landing gate (after UG4)

- **Tests first:** `integration/unifiedhomeguest_test.go` (package `integration`, no `t.Parallel()`, every test named
  `TestMacosUser...` and gated by `requireMacosUser(t)`; it skips off a Mac and runs in the macOS nightly, which
  shards by `go test -list`, never a hand-kept regex):
  - `TestMacosUserGuestSessionsReachTheUnifiedHome`: `packs = ["claude"]`; one launch writes
    `~/.claude/projects/-g/s.jsonl`; a second launch (any command) leaves `.yolo/unified-home/.claude/projects/-g/s.jsonl`
    once, `.yolo/setups/seatbelt-self/home/.claude/projects` a link, and prints `Merged 1 files`.
  - `TestMacosUserAWorkspaceMoveKeepsTheLinksWorking`: after the second launch, rename the workspace directory and
    launch again; the link still resolves and `s.jsonl` reads (the reason for UHG-D2).
  - `TestMacosUserASecondGuestIsRefused`: hold a session of `seatbelt-self` with `holdSession`, launch
    `seatbelt-yolo-account`; the output names ``yolo stop --setup seatbelt-self`` and the exit status is non-zero; the
    flock/session record is the event, no sleep.
  Red command (on a Mac): `go test -count=1 -timeout 0 ./integration -run 'TestMacosUserGuest|TestMacosUserAWorkspaceMove|TestMacosUserASecondGuest'`.
- **Gates (the landing gate, once, on the combined tree):** `python3 scripts/completion-census.py` and commit its
  output; `just check-ci`; `go test -count=1 -timeout 0 ./integration -run UnifiedHome` with
  `YOLO_ALLOW_MISSING_PROGRAMS` unset and `YOLO_NO_AUTO_IMAGE_REAP=1`, which proves the container half of the shared
  code (`commitHome`, the lock, the merge) did not regress; then the full `just test`, because this is a launch-path
  change. Report `podman info --format '{{.Host.Security.Rootless}}'` with the claim: a nested jail cannot verify
  reachability or rootless-only paths ([the blindness section](../reference/loopback-tls-reachability.md#a-nested-jail-is-structurally-blind-to-this)).
- **Fences:** Linux cannot run the three tests above; do not fake a host to make them pass
  (`fakeReadyMacosUserHost` is for gate tests only).
- **Commit:** `test(integration): the guest joins the unified home`
- **Report:** the Linux outcomes, that the three tests skipped off a Mac, the census diff.

### UG6 — docs and status (after UG5)

- **Change:** the rewrites under [Ships with](#ships-with); set the design's `Built` cells for rows that shipped
  (UH-R1, UH-R5, UH-D4, UH-D5, [OQ-UH1](unified-workspace-home.md#OQ-UH1)) and keep `stage: DECIDED` (slices 3 to 5 are unbuilt); review the roadmap
  entry in the same commit.
- **Gates:** `scripts/vantage-check.sh` on each file touched; `just done`.
- **Commit:** `docs(design): record unified home slice 2 as built`

## Ships with

- **Tests by level.** Unit: every case above, in `internal/unifiedhome` and `internal/cli/run`. Integration: the
  three `TestMacosUser...` cases, which are the ones that catch a guest link, a moved workspace or the second-guest
  rule regressing on real Seatbelt. No Linux integration case exists for the guest, because there is no Seatbelt; the
  AST pins (`TestLaunchCallsUnifyHome`, `TestEveryGuestRecordPathReleasesTheLock`, `TestCommitHomeChecksTheExclusionFirst`)
  are what fail on Linux if a call site is deleted.
- **Tests that will break, and should:** `TestAGuestSourceIsLeftAndNamed`, `TestHostSetupNeverReachesTheUnifiedHome`'s
  guest branch, `TestLaunchCallsUnifyHome`, `TestEveryContainerLaunchTakesTheProjectLock`'s guest row,
  `TestSelectHomeDecidesUnderTheLaunchLock`'s legacy-only lock, and the `OwnerUID` cases in `merge_test.go`. Rewrite
  each to the new behavior; never relax an assertion until it passes.
- **Docs describing the old behavior, by path:** `docs/reference/jail-home.md:73` ("on Podman and Apple Container a
  launch also composes"), `docs/reference/storage-and-config.md:374`, `userguide/guides/storage.md` (the table says
  "on every container setup"), and `userguide/guides/macos.md` (add what a guest shares and that the yolo account keeps
  a private home for now). `internal/cli/run/unifyhome.go` and `unifyhomewaiting.go` header comments. The
  design's [§5.2](unified-workspace-home.md#52-composition-what-a-launch-does-to-its-own-slice) table row, `UH-D4` and [§14](unified-workspace-home.md#14-slices) item 2 (the links are relative, UHG-D2) are amended in this plan's own
  commit.
- **Surfaces that are neither:** the two gated-account lines (UHG-D12); the guest's `Merged N files` line (the same
  text as a container's); no config key, no hatch, no `CHANGELOG.md` entry. Backlog item, which UG6 files in the roadmap
  where it keeps unbuilt work: a `host_files` destination beneath a shared state child is refused on macos-user with a
  remedy the next launch undoes.
- **Cheap, and the implementer's:** the shape of `LinkForm`; the name of the gate seam; the order of the new test
  cases; wording of any line not quoted here, provided it names the next step.
- **Stop and ask** (expensive, not the implementer's): any change that lets a launch delete or overwrite a byte; a link
  target outside the workspace's `unified-home`; a lock file in `.yolo`; opening the yolo-account gate (only the Mac
  report of W-UH4 does); any edit to `homeFileThroughLayout` or the Seatbelt profile; any test that sleeps.

## Traceability: the design's verification items to tests

| [§13](unified-workspace-home.md#13-verification) item | Test |
| :--- | :--- |
| The rule: each [§7.1](unified-workspace-home.md#71-the-rule) row through a fake `jailRuns` | `TestExclusionRule` (built), `TestCommitHomeRefusesAGuestPeerBeforeAnyWrite`, `TestCommitHomeLetsAGuestLaunchBesideARunningContainer` (UG2) |
| Composition: shared linked, generated real, a user link neither adopted nor followed | `TestAGuestFirstLaunchLinksTheUnifiedChildren`, `TestAGuestSecondLaunchAdoptsWhatTheAgentWrote` (UG3), `TestComposeLaysARelativeLinkFromTheSliceRoot` (UG1) |
| Links: a source link moves as a link, the target untouched | `TestMergeSkipsALinkYoloLaidAndMovesAUserLink` (UG1), `TestMergeMovesALinkAsALinkAndNeverTouchesItsTarget` (built) |
| The three-home case, guest included | `TestAStrandedGuestSliceMergesWhenNothingRuns` (UG3) |
| Call sites: a merge or exclusion deleted | `TestLaunchCallsUnifyHome`, `TestCommitHomeChecksTheExclusionFirst`, `TestEveryGuestRecordPathReleasesTheLock` |
| The host guard, guest included | `TestHostSetupNeverReachesTheUnifiedHome` (rewritten, UG4), `TestHostRendererDoesNotImportTheComposition` (built) |
| The project lock for every fresh launch; the keeper or record registered before release | `TestEveryJailLaunchTakesTheProjectLock`, `TestRecordingAGuestSessionReleasesTheProjectLock` (UG2) |

## Mac checks

Run by the owner's Mac agent on main at the first commit that contains UG4 ("main `<UG4's landing commit>` or later");
none holds anything off main, and a failure is fixed forward. Setup for all: a fresh workspace under
`/Users/Shared/yolo/uh`, `packs = ["claude"]`, a dev bundle built from main (or the installed `yolo` once it is
that commit). The design's [W-UH4](unified-workspace-home.md#mac-checks) (amended there) and
[W-UH5](unified-workspace-home.md#mac-checks) also run on it. Expected texts are the ones the tests above pin.

- **W-UH7, a guest's sessions reach the unified home.** Settles [UH-R1](unified-workspace-home.md#UH-R1) for the guest.

  ```text
  yolo --setup seatbelt-self -- sh -c 'mkdir -p ~/.claude/projects/-w-uh7 && echo g > ~/.claude/projects/-w-uh7/s.jsonl'
  ```

  ```text
  yolo --setup seatbelt-self -- true
  ```

  Expected on the second: one line `Merged 1 files from 1 homes into this workspace's shared home.`

  ```text
  ls -la .yolo/unified-home/.claude/projects/-w-uh7 .yolo/setups/seatbelt-self/home/.claude
  ```

  Expected: `s.jsonl` once under `unified-home`, and `projects` a link in the slice whose target reads
  `../../../../unified-home/.claude/projects`. Report all three outputs.
- **W-UH8, a container and a guest write at once, and the second guest is refused.** Settles
  [UHG-D11](#UHG-D11) and [OQ-UH1](unified-workspace-home.md#OQ-UH1). Terminal 1:

  ```text
  yolo --setup podman-self -- sh -c 'for i in 1 2 3 4 5; do echo c$i >> ~/.claude/projects/c.jsonl; sleep 2; done'
  ```

  Terminal 2, while it runs, expected to run:

  ```text
  yolo --setup seatbelt-self -- sh -c 'for i in 1 2 3 4 5; do echo g$i >> ~/.claude/projects/g.jsonl; sleep 2; done'
  ```

  Terminal 3, while terminal 2 runs, expected refused naming `yolo stop --setup seatbelt-self`:

  ```text
  yolo --setup seatbelt-yolo-account -- true
  ```

  Then `cat .yolo/unified-home/.claude/projects/c.jsonl .yolo/unified-home/.claude/projects/g.jsonl`. Report the three
  launches' output and the cat (five lines each, no interleaving across files).
- **W-UH9, a moved workspace keeps its links.** Settles [UHG-D2](#UHG-D2). After W-UH7:

  ```text
  mv /Users/Shared/yolo/uh /Users/Shared/yolo/uh2
  ```

  ```text
  cd /Users/Shared/yolo/uh2 && yolo --setup seatbelt-self -- cat ~/.claude/projects/-w-uh7/s.jsonl
  ```

  Expected: `g`. Report the output and `ls -la .yolo/setups/seatbelt-self/home/.claude`.
- **W-UH10, the gated account says so.** Settles [UHG-D8](#UHG-D8) and [UHG-D12](#UHG-D12). In a fresh workspace:

  ```text
  yolo --setup seatbelt-yolo-account -- sh -c 'ls -la ~/.claude'
  ```

  Expected: the first line of [UHG-D12](#UHG-D12) in full; `~/.claude/projects`, if present, a real directory and not a link. Report the output. When W-UH4
  passes, a one-line commit flips `unifiedHomeJoinsYoloAccount`, and W-UH10 is re-run at that commit expecting the
  opposite: a link, and no private-home line.

## Don't

- Do not make the guest's link absolute. A moved workspace would leave every link dangling for good: `Compose` never
  rewrites a link (UHG-D2).
- Do not add `unifyHome` to `runSealedMacosUser` (UHG-D5) or hoist it above `commitHome`.
- Do not open the yolo-account gate in this slice, and do not add an environment variable or a config key to open it
  (UHG-D8): the Mac report of W-UH4 is the only thing that flips it.
- Do not "fix" `homeFileThroughLayout` for composed links here ([Traps](#traps)): it is the guest bootstrap's
  security surface and the edge is rare.
- Do not put a refusal in front of composing a slice whose own setup already runs (UHG-D11).
- Do not cache liveness answers in front of `jailStatus`; a stale "not running" lets a second guest in.
- Do not make `ensure` a slice directory in account mode before the gate opens; the account owns that surface until
  W-UH4 says both users can write.

## Blockers

None. Every behavior the slice reaches is ruled in the design's
[decision ledger](unified-workspace-home.md#decision-ledger) or decided above, and the Mac witnesses gate no landing.
One design finding, handled in this plan's commit rather than filed as a question: the design's
[W-UH4](unified-workspace-home.md#mac-checks) cannot run as written (it writes `~/.claude/projects/...` from a launch
that is, by UH-D13, not yet joined), so it is amended there to use absolute paths in the unified home.
