---
title: "Plan: passwordless macos-user start and stop, through one run-as-_yolojail rule"
date: 2026-10-15
status: accepted
stage: BUILT
next: "Run `gh workflow run macos-user.yml` for task 9's narrow-sudo step on macOS CI; then the owner's Mac check, which needs its own authorization"
depends-on:
  - macos-user-privilege-lifecycle.md#OQ-MP10
tags: [plan, macos-user, privileges, sudo, lifecycle]
summary: "Code-grounded hand-off for OQ-MP10's reduced grant: every root sudo on the macos-user launch, exit, capture and fork-build path becomes a host-user act in a host-owned staging root under /Users/Shared, every crossing into the guest goes through the one rule `(_yolojail) NOPASSWD: /usr/bin/env`, setup installs that rule, and a shared preflight probes it. Ten tasks, partitioned by file, with the macOS CI and owner's-Mac verification each step owes."
---

# Plan: passwordless macos-user start and stop

**Status:** 2026-10-15. Completed against the tree, written against `4d0214a14`.
**2026-10-16: tasks 1 to 10 are built and in main** (commits `5072b0941` and `a4ef8073b`). The decisions
the build took beyond this plan are [PL-10 onward](#implementation-decisions-taken-here).
MEASURED: the narrow-sudo cell passed on a developer Mac on 2026-10-19
([macos-user-suite-on-a-developer-mac.md §12](macos-user-suite-on-a-developer-mac.md#12-what-done-looks-like)).
UNMEASURED: [task 9](#task-9--integration-and-the-narrow-sudo-ci-step)'s step on macOS CI, and the
[owner's check](#the-owners-mac) on a real Mac.

**Design:** [`macos-user-privilege-lifecycle.md`](macos-user-privilege-lifecycle.md), the ruling
in [OQ-MP10](macos-user-privilege-lifecycle.md#decision-ledger) and
[walkthrough points 1 to 4](macos-user-privilege-lifecycle.md#points-settled-in-the-walkthrough-2026-10-15).
Its helper sections, [the authority boundary](macos-user-privilege-lifecycle.md#3-the-authority-boundary) through [exit and restart](macos-user-privilege-lifecycle.md#6-normal-switch-exit-stop-and-restart), are the **fallback**, not this plan: build none of them.

**Precedence.** The design wins on behavior. The tree wins on fact, for cosmetic drift only (a
moved line, a renamed helper with the same role): follow the tree and say so in the report and
the commit. This plan is advice and is the first thing to be wrong; never twist code to match it.
**Stop and report, do not commit** when a task's red test does not fail as predicted, a file it
names is dirty with someone else's work, a named symbol is gone, the fix needs a file outside the
task's fence, or a gate fails outside its scope.

## The settled behavior (do not re-open)

Quoted from the ruling so no task has to read the design:

- **One sudoers rule**, written by setup after `visudo -c -f`:
  `<host user> ALL=(_yolojail) NOPASSWD: /usr/bin/env`, with setup's own `Defaults secure_path`
  in the same file. Every command yolo runs through sudo is named by absolute path. **No root
  helper.**
- **Readiness probe G2:** `sudo -n -u _yolojail /usr/bin/env /usr/bin/true`. A bare
  `/usr/bin/true` would not match the rule.
- **G3 to G10 dropped.** Every per-launch file (Seatbelt profile, env files, CA files, staged
  guest binaries, pack, overlay and context trees, capture and fork-build files) is written by
  the **host user**, not root: mode `0444` (binaries and per-launch directories `0555`);
  credential files `0600` with a per-file read ACL naming `_yolojail` alone, in a `0700`
  directory with a search ACL. The host user deletes its own files at exit. No receipts.
- **The staging root lives outside the host home**, which `_yolojail` cannot traverse: under
  `/Users/Shared`, created once by setup and owned by the host user. **A launch refuses a staging
  root it does not own** (squatting), naming the setup command.
- **One enrolled host user per Mac.** Setup refuses a second, naming the remedy: remove the
  existing enrollment first.
- **The Seatbelt profile must allow the guest to read the new staging root.**
- Existing state follows the design's
  [administrator setup and existing state](macos-user-privilege-lifecycle.md#administrator-setup-and-existing-state): running
  sessions continue under their old launcher; setup never kills them or adopts their root files
  or legacy guest trees; no broad passwordless root removal survives the upgrade.

## Implementation decisions taken here

Mechanism choices the ruling leaves open, decided for the build. Each is reversible.

| ID | Decision | Reason |
| :--- | :--- | :--- |
| PL-1 | The staging root is `/Users/Shared/yolo-staged`. Its layout is today's `/var/yolo-jail` layout unchanged: `bin/`, `packs/`, `home-overlay/`, `ctx/`, `captures/`, `env/`, `profile-<key>.sb`. | Every `Staged*` helper already takes the state dir as a parameter, so only the default moves. Not under `/Users/Shared/yolo`, which is the `_yolojail`-writable workspace root. |
| PL-2 | The staging root and its category directories (`bin/`, `packs/`, `home-overlay/`, `ctx/`, `captures/` and its `entries/`, `staging/`) are host-owned `0755` with no ACL. Each per-launch tree is sealed `0555` directories and `0444` files (`0555` binaries). `env/` is `0700` plus the search ACE. | `0755` and `0555` give the guest the same rights: it is neither owner nor group. A category directory sealed `0555` would need a `u+w` flip on every launch, which races two concurrent launches of different workspaces writing `bin/` or `packs/`. Mac CI asserts the guest cannot create in each (task 9). |
| PL-3 | The enrolled user is whoever owns the staging root. Setup reads `/etc/sudoers.d/yolo-jail` with `sudo` only to refuse a rule naming someone else. | The sudoers file is `0440 root`, so a launch cannot read it; ownership of the root is readable by anyone. |
| PL-4 | The sudoers file is `/etc/sudoers.d/yolo-jail`, staged as `/etc/sudoers.d/.yolo-jail.new` and renamed. Its `Defaults` line is scoped to the run-as account: `Defaults>_yolojail secure_path=…`. | sudo ignores a `sudoers.d` name containing `.`, so the staged name is inert until the rename. `Defaults>` changes nothing for the admin's other sudo. |
| PL-5 | `yolo macos-teardown` also removes the rule and the staging root. It is the "remove the existing enrollment" remedy. | The rule without the account is meaningless, and teardown already needs the admin password. |
| PL-6 | Setup creates the staging root **as the host user** (`/Users/Shared` is `1777`), then refuses any root it finds owned by another account. It uses `sudo` only for `/Users/Shared/yolo-captures`, which today's captures left root-owned, and for the rule. | Same result as "created by setup and chowned", with fewer root acts. |
| PL-7 | Capture scratch cleanup runs as the host user. On failure it runs `/bin/chmod -R u+w` and `/bin/rm -rf` **as `_yolojail` through the rule**, then the host's `rm -rf` again. A failure stays a warning. | The design's open [capture-scratch gap](macos-user-privilege-lifecycle.md#automatic-capture-has-the-same-boundary-not-a-root-scratch-walker): a vendor installer may strip the inherited ACE from files it owns. Running as the files' owner needs no root. |
| PL-8 | Legacy session files: a launch's sweep that claims a gone record whose legacy profile `/var/yolo-jail/profile-<key>.sb` exists keeps the record and warns once per launch, naming `yolo macos-setup`. Setup (the admin act) removes those exact five legacy paths with `sudo` for claimable records. Legacy trees under `/var/yolo-jail` are never removed by yolo; `yolo stores` lists them with the manual command. | The design's [existing-state rules](macos-user-privilege-lifecycle.md#administrator-setup-and-existing-state): no routine root removal, administrator repair of exact targets only after the old sessions end, and guest trees retained. |
| PL-9 | `sudo` itself is invoked as `/usr/bin/sudo`. | Point 4's absolute-path rule; a PATH-shadowed `sudo` would make the run-as audit in task 9 meaningless. |
| PL-10 | `staging-root-owner` also requires `not-root`. | Under `sudo`, the user's own staging root reads as another account's, and that row's remedy would delete it. |
| PL-11 | Setup runs its enrollment checks, the check of the installed rule included, before the account steps. | A refusal then comes before any account, root or rule is made. The staging-root and capture-root refusals come before any `sudo`; the rule check is setup's first. |
| PL-12 | Setup reads a rule check's exit 1 as another user's rule only after `/usr/bin/sudo -n /usr/bin/true` succeeds. | `sudo` also exits 1 when it cannot authenticate, which `grep`'s "no match" cannot be told apart from. |
| PL-13 | Setup repairs a staging or capture root the host user owns but that is unsafe (wrong mode or an access list) with its two `chmod`s, rather than refusing. A link or a non-directory is still refused. | The repair is the one `staging-root-safe`'s remedy names, and it needs no root. |
| PL-14 | Setup reads an owner's name and the host user's primary group with `/usr/bin/id`, not `os/user`. | Go's pure `os/user` reads `/etc/passwd`, which a Mac's accounts are not in. |
| PL-15 | A staged capture entry is sealed with its top left owner-writable, renamed into `entries/`, then its top sealed. Context links are made after the recursive seal, in parents opened one at a time, which are then sealed again. | A rename into another parent needs write on the moved directory itself, and a link cannot be made in a `0555` directory. Opening only the parents keeps a recursive `chmod` from meeting a link. |
| PL-16 | The integration Seatbelt registry claims the `staging-root-read-allow` marker, with two runtime cases: a read of the staging root allowed, and a write refused by the write deny. | The registry must name every marker the profile carries, with a runtime case or a written reason why none can run (a Linux test enforces it); the write case shows the read allow grants nothing more. |
| PL-17 | Integration fixture cleanup removes a workspace as `_yolojail` through the rule before falling back to root `sudo`. | The narrow-sudo step takes root `sudo` away, and `_yolojail` owns what it made. |
| PL-18 | The narrow-sudo CI step, in one `sudo` invocation, moves aside every `/etc/sudoers.d` file granting `NOPASSWD: ALL` except yolo's own, and in `/etc/sudoers` comments out only the `NOPASSWD: ALL` lines for `%admin` or the runner user, adding the stock macOS `%admin ALL = (ALL) ALL` after each `%admin` one. The edited `/etc/sudoers` is checked with `visudo -c -f` before it is installed by one rename, and everything is restored if the final `visudo -c` fails. | After the first change, no further `sudo` works. The hosted runner keeps the `%admin` grant in `/etc/sudoers` itself (run 38913640055), and the maintainer approved editing it on this ephemeral runner (2026-10-16) ([task 9](#task-9--integration-and-the-narrow-sudo-ci-step)). |

## Map

| Path | Change |
| :--- | :--- |
| `internal/macosuser/privilege.go` | **new**: the contract. Staging-root and legacy constants, `RunAsSandboxArgv`, the G2 argv, `RunAsArgvProblems`, `SudoersRule`, `StagingRootFacts` and `JudgeStagingRoot`. |
| `internal/macosuser/statedir.go` | `StateDir()` returns the staging root; add `LegacyStateDir()`. |
| `internal/macosuser/macosuser.go` | `stateDir` default moves (PL-1); staging builders seal per PL-2; `LaunchArgv` uses the run-as prefix; collision text drops `sudo`; setup argvs get absolute paths. |
| `internal/macosuser/jaildaemon.go` | `StageGuestBinaryCommands` seals `0555`; `JailDaemonArgv` uses the run-as prefix. |
| `internal/macosuser/ctxlinks.go` | Context-tree staging as host; `StateDirs` adds the new root's spellings; both preflights and the write probe go through `/usr/bin/env`; the invariant mirror at `~:999` matches. |
| `internal/macosuser/provision.go`, `serviceprobe.go` | `ProvisionArgv`, `ProbeServicesArgv` use the run-as prefix. |
| `internal/macosuser/runplan.go` | `DarwinBootstrapArgv` uses the run-as prefix; `PlanInvariants` wording and `captureStoreInvariants` rewritten; a new rule-shape invariant. |
| `internal/macosuser/seatbelt.go`, `seatbeltcapture.go` | Read allow for the staging root in the session and capture profiles. |
| `internal/macosuser/preconditions.go` | Four new preconditions and three `LaunchProbes` fields; `PrivilegeUnmet`. |
| `internal/macosuser/orchestrator.go` | New `Deps` fields; `InstallRootFile` becomes `WriteHostFile`; stage loop, `stageCaptures`, `finishPackTree` run as host; `teardownDeps` deleted; `PrintPlan` and messages. |
| `internal/macosuser/real.go` | `installRootFileReal` replaced by `writeHostFileReal`; new `stagingRootFactsReal`, `runAsGrantReal`. |
| `internal/macosuser/envfile.go`, `cabundle.go` | Installers run as host with `WriteHostFile`. |
| `internal/macosuser/sessionfiles.go` | Teardown and sweep remove as host; legacy-record handling (PL-8). |
| `internal/macosuser/capture.go` | Prepare, stage, profile, cleanup as host (PL-7); gates add `PrivilegeUnmet` and the capture-root check; driver argvs use the run-as prefix; `PrintCapturePlan`. |
| `internal/macosuser/commands.go` | `MacosSetup` G0 (rule, staging root, capture root, legacy sweep, probe, verdict); `MacosTeardown` removes the enrollment. |
| `internal/cli/check/sections_macos.go` | New `LaunchProbes` fields wired to the check's seams. |
| `internal/cli/hostfloor.go`, `internal/hostfloor/built.go` | `macActBlocked` asks `PrivilegeUnmet`, not the terminal and `sudo -n true`; text. |
| `internal/cli/commands.go` | `macos-setup` and `macos-teardown` help text. |
| `internal/cli/stores/inventory.go`, `stores.go` | Rows for the staging root (host-owned, measurable) and the legacy dir. |
| `internal/setupcensus/kinds.go` | The `/var/yolo-jail` sentence. |
| `integration/macosusergate_test.go` | Gate fact `sudoQuiet` becomes the G2 probe. |
| `integration/macosuserpasswordless_test.go` | **new**: start and stop with no password, under narrowed sudo. |
| `integration/macosuserterminate_test.go` | Lists `env/` and tests files without `sudo`. |
| `.github/workflows/macos-user.yml` | A final step narrowing the runner's sudo, then the passwordless tests. |
| Docs | [Ships with](#ships-with). |

## Root `sudo` call sites today

Counted with `rg -n 'sudo' internal/macosuser internal/cli` at `4d0214a14`, test files excluded.
Display-only text (dry-run renderers, remedy strings) is listed under each task, not here.

**Routine path, executes root sudo: 16 sites in 7 files.** Every one becomes a host-user act.

| # | Site | Today | Replacement |
| ---: | :--- | :--- | :--- |
| 1 to 3 | `real.go` `installRootFileReal` (`~:176`, `:179`, `:186`) | `sudo mkdir -p`, `sudo tee`, `sudo chmod` | `writeHostFileReal`: exclusive no-follow create at the final mode, as host (task 5) |
| 4 | `orchestrator.go` stage loop (`~:1209`) | `sudo` + every `StageCommands` argv | the argv itself, as host (task 5) |
| 5 | `orchestrator.go` `finishPackTree` (`~:1419`) | `sudo rm -rf -- <pack root>` | `/bin/chmod -R u+w`, then `/bin/rm -rf`, as host (task 5) |
| 6 | `orchestrator.go` `teardownDeps` (`~:1439`) | rewrites `sudo` to `sudo -n` on a signal | deleted: no root sudo remains to rewrite (task 5) |
| 7 | `orchestrator.go` `stageCaptures` (`~:1480`) | `sudo` + capture-store scripts | as host (task 5) |
| 8, 9 | `envfile.go` `installSandboxEnvFile` (`~:339`, `:352`) | `sudo` dir and grant argvs | as host (task 5) |
| 10, 11 | `cabundle.go` `installCATrustFiles` (`~:338`, `:350`) | the same | as host (task 5) |
| 12 | `sessionfiles.go` `sessionTeardown.remove` (`~:206`) | `sudo rm -f` | `/bin/rm -f`, as host (task 5) |
| 13 | `sessionfiles.go` `sweepGoneSessions` (`~:318`) | `sudo rm -f` | as host; legacy per PL-8 (task 5) |
| 14 | `capture.go` `runCaptureSteps` (`~:732`) | `sudo` + prepare and stage argvs | as host (task 6) |
| 15 | `capture.go` `RunCaptureCleanup` (`~:817`) | `sudo rm -rf` | PL-7 (task 6) |
| 16 | `cli/hostfloor.go` probe (`~:286`) | `sudo -n true` as root | `PrivilegeUnmet` (task 4) |

**Run-as `_yolojail`, already non-root: 11 sites in 7 files.** Each must match the rule, so each
becomes `RunAsSandboxArgv`. Two of them do **not** match today.

| Site | Today | Matches the rule? |
| :--- | :--- | :--- |
| `runplan.go` `DarwinBootstrapArgv` (`~:370`) | `sudo --user= /usr/bin/env -i …` | shape yes, no `-n` |
| `macosuser.go` `LaunchArgv` (`~:1171`) | `sudo --set-home --user= /usr/bin/env -i …` | shape yes, no `-n` |
| `provision.go` `ProvisionArgv` (`~:156`) | `sudo --user= /usr/bin/env -i …` | shape yes, no `-n` |
| `jaildaemon.go` `JailDaemonArgv` (`~:368`) | `sudo -n --set-home --user= /usr/bin/env -i …` | yes |
| `serviceprobe.go` `ProbeServicesArgv` (`~:66`) | `sudo --user= /usr/bin/env -i …` | shape yes, no `-n` |
| `capture.go` `CaptureDriverArgv` (`~:435`), `ForkBuildDriverArgv` (`~:1145`) | `sudo --user= /usr/bin/env -i …` | shape yes, no `-n` |
| `ctxlinks.go` `CacheRelocationPreflight` (`~:626`) | `sudo --user= /bin/test -r <path>` | **no** |
| `ctxlinks.go` `CacheRelocationWriteProbe` (`~:653`) | `sudo --user= /usr/bin/env -i …` | shape yes, no `-n` |
| `ctxlinks.go` `ContextPreflight` (`~:841`) and its invariant mirror (`~:999`) | `sudo --user= /bin/test …` | **no** |

**Setup and teardown, the administrator act G0, stay root: 5 sites in 2 files.** `commands.go`
`~:46`, `~:60`, `~:81`, `~:210`, and `real.go` `setRandomPasswordReal` (`~:231`). Each gets
absolute paths (task 7).

## Reuse before you write

- **The `sd` parameter on every staging and path helper** (`StagedPackTreeRoot`,
  `StagedHomeOverlay`, `StagedCtxRoot`, `SessionProfilePath`, `SandboxEnvFile`, `GuestBinDir`,
  `StagedCapturesRoot`): `""` means the default. Moving the default moves the launch; tests keep
  passing `t.TempDir()`.
- **`staging_darwin_test.go`'s `runStaged`** executes the plan's argvs against a temp `sd`. With
  `sudo` gone it needs no prefix stripping; extend it for the sealed modes.
- **`fixtureStateDeps` in `orchestrator_test.go`** runs the staging argvs for real on Linux,
  skipping Darwin ACL commands. Its `sudo` stripping becomes dead code to delete, not to keep.
- **`mockDeps(&rec)` in `orchestrator_test.go`** records `run:<argv>` and `install:<path>`.
  The rule-shape and no-root-sudo assertions read `rec`.
- **`failOne` in `preconditions_test.go`** plus `TestTheLaunchRefusesOnEachPreconditionWithItsOwnMessage`
  is the launch's call-site test for every precondition: an entry in `failOne` is all a new
  precondition needs to be covered at the call site.
- **`setUpMacProbes` in `internal/cli/run/autocapture_test.go`** and
  `TestAMacosUserLaunchTheBackendWillRefuseAutoCapturesNothing`: the auto-capture call-site test.
  Add a case per new precondition.
- **`macSetup` in `internal/cli/capturelandlock_test.go`**: the floor's Mac probe fixture.
- **`newMacMachine` in `internal/cli/check/sections_macos_test.go`**: the check's Mac fixture;
  its `Exec` stub returns `RC: 0`, so routing G2 through `o.Exec` makes it pass by default.
- **`sandboxFileReadAce`, `SandboxEnvDirCommands`, `SandboxEnvGrantCommands`** stay the spelling
  of every ACE; only who runs them changes.
- **`runWithTimeout`, `outputWithTimeout`** in `real.go` for the G2 probe and `ls -lde`.
- **`sessionRecord` and `claimGoneSessionRecord`** keep the liveness logic untouched.

## House style that is not obvious

- **Refusals are rich markup and name the next step** ([happy-path principle](../reference/happy-path-principle.md)):
  the command, who runs it and whether it asks for a password. Every new text is quoted under its
  task; copy them.
- **Long all-caps comment leads** (`// THE CONTEXT DIR, …`) state why, and cite the design by
  path. Rewrite a comment that now says "root-owned"; leave the history out.
- **Plan argvs stay pure data.** A plan never reads the filesystem; the launch probes. Keep that:
  `JudgeStagingRoot` is pure, `stagingRootFactsReal` is the impure half.

## Traps

- **The two `/bin/test` preflights do not match the rule.** `sudo --user=_yolojail /bin/test -r`
  needs a rule for `/bin/test`; under the ruled rule it fails, and the launch refuses every context
  mount with the wrong remedy. Route through `/usr/bin/env /bin/test`.
- **sudo's cached credentials mask a missing rule.** An admin who typed a password in the last
  five minutes passes `sudo -n -u _yolojail …` through `%admin ALL=(ALL) ALL`. Setup must run
  `/usr/bin/sudo -k` before its own G2 probe, and every verification step does the same.
- **A `0555` directory blocks its owner too.** `rm -rf` of a sealed tree fails with
  `Permission denied` for the host user who made it. Every host removal or replacement of a sealed
  tree runs `/bin/chmod -R -- u+w <path>` first (BSD `chmod -R` does not follow links).
- **`captureStoreInvariants` refuses any store under `/Users`** (`underUsersRoot`). With the root
  under `/Users/Shared` every launch with a capture fails its invariants. Rewrite that rule to
  "under `plan.StagedDir`"; the Seatbelt read allow is what makes `/Users/Shared/yolo-staged`
  readable.
- **The session profile denies reads under `/Users`**, and the guest execs the staged yolo, sources
  the env file and reads the trees from inside it. Without task 3, every launch dies at its first
  exec with `Operation not permitted`, after a passwordless start that looks fine.
- **sudo ignores files in `sudoers.d` whose name contains `.` or ends in `~`.** A rule named
  `yolo-jail.sudoers` is silently never read; the G2 probe then fails with no visudo error.
- **`/etc/sudoers` must include `sudoers.d`.** Stock macOS has `#includedir /private/etc/sudoers.d`;
  a managed Mac may not. Setup's post-install probe is what detects it; its refusal names the check.
- **Old sessions keep writing `/var/yolo-jail`.** A launcher started before the upgrade is the old
  binary. Never delete or adopt that tree from a routine launch.
- **`chown user:_yolojail` as a non-root user** works only because the host user is in `_yolojail`
  (`SharedRootProvisionCommands` adds them). Spell it `/usr/bin/chgrp _yolojail` in the capture
  prepare argvs; it says what it does.
- **The integration gate's `sudo -n true`** is the root probe. After the CI step narrows sudo it
  fails, and the gate would skip the very tests that measure the change. Task 9 replaces it.
- **Tests that strip a leading `"sudo"`** (`fixtureStateDeps`, `runStaged`, assertions like
  `recAt(rec, "run:sudo "+rmBin)`) assert the old behavior. **Rewrite them to the new behavior; do
  not relax them until green.**
- **`chmod +a` exists only on macOS.** Linux unit tests assert the argv; only `ci.yml`'s
  `check-macos` job and the Mac jobs run it.

## Tasks

Build order: **1**, then **2, 3, 4** in parallel, then **5**, then **6, 7, 8** in parallel, then
**9**, then **10**. Task 5 is what unblocks the rest; landing it late keeps every downstream test
file in flux. Every task runs `just format` before its commit and stages only its own paths
(`git add -N <new>` then `git commit -- <paths>`).

### Task 1 — the contract

**Independent.**

- **Settled:** the rule, the G2 argv, the staging root's ownership rule and one-user-per-Mac. Do
  not re-open.
- **Preconditions:** `git status --short internal/macosuser` is clean; `rg -n 'func StagingRoot'
  internal/macosuser` finds nothing.
- **Tests first:** `internal/macosuser/privilege_test.go` (new):

  ```go
  package macosuser

  import (
  	"os"
  	"strings"
  	"testing"
  )

  func TestTheRunAsPrefixIsTheRuledRuleShape(t *testing.T) {
  	want := "/usr/bin/sudo -n --user=_yolojail /usr/bin/env /usr/bin/true"
  	if got := strings.Join(RunAsGrantProbeArgv(), " "); got != want {
  		t.Errorf("G2 probe = %q, want %q", got, want)
  	}
  	if got := strings.Join(RunAsSandboxArgv(nil, "/usr/bin/true"), " "); got != want {
  		t.Errorf("RunAsSandboxArgv = %q, want %q", got, want)
  	}
  	got := strings.Join(RunAsSandboxArgv([]string{"--set-home"}, "-i", "HOME=/h"), " ")
  	if got != "/usr/bin/sudo -n --set-home --user=_yolojail /usr/bin/env -i HOME=/h" {
  		t.Errorf("with --set-home: %q", got)
  	}
  }

  func TestRunAsArgvProblemsAcceptsOnlyTheRule(t *testing.T) {
  	ok := [][]string{
  		RunAsGrantProbeArgv(),
  		{"/usr/bin/sudo", "-n", "--set-home", "--user=_yolojail", "/usr/bin/env", "-i", "A=1", "/bin/zsh"},
  		{"/bin/rm", "-f", "/x"}, // not a sudo argv: not this check's business
  	}
  	for _, a := range ok {
  		if p := RunAsArgvProblems("t", a); len(p) > 0 {
  			t.Errorf("%q: %v", a, p)
  		}
  	}
  	bad := [][]string{
  		{"sudo", "-n", "--user=_yolojail", "/usr/bin/env", "/usr/bin/true"},      // sudo off PATH
  		{"/usr/bin/sudo", "--user=_yolojail", "/usr/bin/env", "/usr/bin/true"},   // may prompt
  		{"/usr/bin/sudo", "-n", "--user=_yolojail", "/bin/test", "-r", "/x"},     // not /usr/bin/env
  		{"/usr/bin/sudo", "-n", "/bin/rm", "-f", "/x"},                           // root
  		{"/usr/bin/sudo", "-n", "--user=root", "/usr/bin/env", "/usr/bin/true"},  // root
  		{"/usr/bin/sudo", "-n", "-E", "--user=_yolojail", "/usr/bin/env", "true"}, // SETENV
  		{"/usr/bin/sudo", "-n", "--login", "--user=_yolojail", "/usr/bin/env", "x"},
  	}
  	for _, a := range bad {
  		if p := RunAsArgvProblems("t", a); len(p) == 0 {
  			t.Errorf("%q passed", a)
  		}
  	}
  }

  func TestTheSudoersRuleIsExactlyTheRuledOne(t *testing.T) {
  	got, err := SudoersRule("matt")
  	if err != nil {
  		t.Fatal(err)
  	}
  	want := "# Written by `yolo macos-setup` for matt. Lets matt run commands as _yolojail,\n" +
  		"# never as root, without a password. `yolo macos-teardown` removes it; setup rewrites it.\n" +
  		"Defaults>_yolojail secure_path=\"/usr/bin:/bin:/usr/sbin:/sbin\"\n" +
  		"matt ALL=(_yolojail) NOPASSWD: /usr/bin/env\n"
  	if got != want {
  		t.Errorf("rule =\n%s\nwant\n%s", got, want)
  	}
  	if SudoersFile != "/etc/sudoers.d/yolo-jail" || strings.Contains(SudoersFile[len("/etc/sudoers.d/"):], ".") {
  		t.Errorf("SudoersFile %q: sudo ignores a sudoers.d name with a dot", SudoersFile)
  	}
  	for _, u := range []string{"", "root", "ALL", "%admin", "a b", "a,b", "m\natt", "_yolojail", "1abc"} {
  		if _, err := SudoersRule(u); err == nil {
  			t.Errorf("SudoersRule(%q) accepted", u)
  		}
  	}
  }

  func TestJudgeStagingRoot(t *testing.T) {
  	const me = 501
  	for _, c := range []struct {
  		name string
  		f    StagingRootFacts
  		want StagingRootVerdict
  	}{
  		{"absent", StagingRootFacts{}, StagingRootAbsent},
  		{"unreadable", StagingRootFacts{Err: os.ErrPermission}, StagingRootUnknown},
  		{"mine", StagingRootFacts{Exists: true, IsDir: true, UID: me, Mode: 0o755}, StagingRootOK},
  		{"squatted", StagingRootFacts{Exists: true, IsDir: true, UID: 602, Mode: 0o755}, StagingRootForeign},
  		{"link", StagingRootFacts{Exists: true, IsSymlink: true, UID: me}, StagingRootUnsafe},
  		{"file", StagingRootFacts{Exists: true, UID: me, Mode: 0o644}, StagingRootUnsafe},
  		{"group-writable", StagingRootFacts{Exists: true, IsDir: true, UID: me, Mode: 0o775}, StagingRootUnsafe},
  		{"acl", StagingRootFacts{Exists: true, IsDir: true, UID: me, Mode: 0o755, HasACL: true}, StagingRootUnsafe},
  	} {
  		if got := JudgeStagingRoot(c.f, me); got != c.want {
  			t.Errorf("%s: %v, want %v", c.name, got, c.want)
  		}
  	}
  	if StagingRoot() != "/Users/Shared/yolo-staged" || LegacyStateDir() != "/var/yolo-jail" {
  		t.Errorf("roots: %q, %q", StagingRoot(), LegacyStateDir())
  	}
  }
  ```

  Red: `go test -short -run 'RunAs|Sudoers|JudgeStagingRoot' ./internal/macosuser` fails to
  compile (undefined symbols).
- **Change:** `internal/macosuser/privilege.go` (new). Contract signatures, which tasks 2 to 8
  build against:

  ```go
  const SudoersFile = "/etc/sudoers.d/yolo-jail"
  func StagingRoot() string                          // "/Users/Shared/yolo-staged"
  func LegacyStateDir() string                       // "/var/yolo-jail"
  func RunAsSandboxArgv(sudoFlags []string, argv ...string) []string
  func RunAsGrantProbeArgv() []string
  func RunAsArgvProblems(label string, argv []string) []string
  func SudoersRule(hostUser string) (string, error)
  type StagingRootFacts struct {
  	Exists, IsDir, IsSymlink, HasACL bool
  	UID  uint32
  	Mode os.FileMode // permission bits only
  	Err  error       // a failure other than not-exist
  }
  type StagingRootVerdict int // StagingRootOK, StagingRootAbsent, StagingRootForeign, StagingRootUnsafe, StagingRootUnknown
  func JudgeStagingRoot(f StagingRootFacts, euid int) StagingRootVerdict
  ```

  `sudoFlags` admits `--set-home` only; anything else panics (a programming error, never input).
  The user-name check: `^[A-Za-z_][A-Za-z0-9_.-]*$`, and not `root`, `ALL` or `SandboxUser`
  (advice: visudo validates again at setup; this check exists so a hostile name never reaches a
  root-owned file). Leave `stateDir` in `macosuser.go` alone here; task 2 moves it.
- **Gates:** `go test -short ./internal/macosuser`; `GOOS=darwin go vet ./internal/macosuser`.
- **Fences:** nothing outside `privilege.go` and `privilege_test.go`.
- **Commit:** `feat(macos-user): the one-rule contract for passwordless launches`
- **Report:** the red compile error, the green run.

### Task 2 — host-owned staging and the run-as argvs

**After 1.** Mechanical but wide.

- **Settled:** per-launch files host-owned `0444`, binaries and per-launch directories `0555`;
  the staging root under `/Users/Shared`; every crossing is the rule shape. Do not re-open.
- **Preconditions:** task 1 landed. The files below are clean.
- **Tests first:** add to `internal/macosuser/macosuser_test.go`, beside the `PlanInvariants`
  cases (`sessionPlan` is in `sessionfiles_test.go`):

  ```go
  func TestEveryPlanArgvIsTheRuleOrRunsAsTheHost(t *testing.T) {
  	plan := sessionPlan(t, "0123456789abcdef")
  	links := []ContextLink{{Source: "/Users/Shared/data", Dest: "/ctx/data", Dir: true}}
  	plan.ContextPreflight = ContextPreflight(links, "")
  	var argvs [][]string
  	argvs = append(argvs, plan.BootstrapArgv, plan.ProvisionArgv, plan.LaunchArgv, plan.JailDaemonArgv)
  	for _, p := range plan.ContextPreflight {
  		argvs = append(argvs, p.Argv)
  	}
  	for _, a := range argvs {
  		if len(a) == 0 {
  			continue
  		}
  		if a[0] != "/usr/bin/sudo" {
  			t.Errorf("crossing does not start with /usr/bin/sudo: %q", a)
  		}
  		if p := RunAsArgvProblems("t", a); len(p) > 0 {
  			t.Errorf("%v", p)
  		}
  	}
  	for _, c := range append(append([][]string{}, plan.StageCommands...), plan.CaptureStageCommands...) {
  		if len(c) > 0 && (c[0] == "sudo" || c[0] == "/usr/bin/sudo") {
  			t.Errorf("a staging argv runs sudo: %q", c)
  		}
  	}
  	if plan.StagedDir != "/Users/Shared/yolo-staged" {
  		t.Errorf("StagedDir = %q", plan.StagedDir)
  	}
  }
  ```

  Red: `go test -short -run TestEveryPlanArgvIsTheRuleOrRunsAsTheHost ./internal/macosuser`
  (fails on `sudo --user=…` and `/var/yolo-jail`).
- **Change:**
  - `macosuser.go` `~:39` `stateDir` becomes `StagingRoot()`'s value; delete the
    "root-owned, 0444" comment. `StateDir()` in `statedir.go` keeps returning `stateDir`; add
    `LegacyStateDir` there or rely on task 1's.
  - Sealing (PL-2): `StageBinaryCommands` and `StageGuestBinaryCommands` end each file with
    `chmod 0555` before the `mv`. `StagePackCommands`, `StageHomeOverlayCommands`,
    `StageCtxCommands`, `StageEmptyCtxCommands`, `contextLinkCommands`: `chmod -R a-w,a+rX` on the
    `.new` tree (the ctx root then `0500` plus its `list,search` ACE, not `0700`: the owner needs
    no write after sealing). Every `rm -rf <dst>` of a sealed tree is preceded by
    `chmod -R u+w <dst>`; build it as one helper (`unsealCommands(path)`) so no site forgets.
    `isPackTreeWriterCommand` must still recognize the sealing chmod as a writer.
  - Category directories: each `mkdir -p <category>` is followed by `chmod 0755 <category>`.
  - Capture store scripts (`stageCaptureScript`, `pruneCapturesScript` in `macosuser.go`): the
    prune deletes sealed entries, so it unseals first; the stage seals the copied entry
    `a-w,a+rX`. Their comments about "Copied by root, every byte here is root's" become "copied by
    the host user".
  - `LaunchArgv`, `JailDaemonArgv` (keep `--set-home`), `DarwinBootstrapArgv`, `ProvisionArgv`,
    `ProbeServicesArgv`, `CacheRelocationWriteProbe`: build the head with `RunAsSandboxArgv`.
  - `ContextPreflight`, `CacheRelocationPreflight`: `RunAsSandboxArgv(nil, testBin, flag, src)`,
    and the invariant mirror at `ctxlinks.go` `~:999` builds the same argv (it compares strings).
  - `ctxlinks.go` `DarwinContextSiting().StateDirs`: the staging root, its
    `/System/Volumes/Data` spelling, and both legacy spellings.
  - `runplan.go` `PlanInvariants`: replace "root-owned state dir" wording with "host-owned staging
    root"; add the rule-shape check over every crossing argv (call `RunAsArgvProblems`);
    `captureStoreInvariants` drops the `underUsersRoot` clause and keeps "under `plan.StagedDir`";
    add "the workspace is not inside the staging root or `CaptureRootDefault()`".
  - `printPackTreeReservationFailure` (`macosuser.go` `~:309`): `ls -la -- %s`, no `sudo`.
  - `CreateUserCommands`, `ProvisionHomeCommands`, `DeleteUserCommands`,
    `SharedRootProvisionCommands`: absolute paths (`/usr/bin/dscl`, `/usr/sbin/dseditgroup`,
    `/usr/sbin/createhomedir`, `/usr/sbin/chown`, `/bin/chmod`, `/bin/mkdir`, `/bin/rm`).
    `SharedRootProvisionCommands` is also run as host by capture (task 6), so keep its argv
    host-runnable.
- **Gates:** `go test -short ./internal/macosuser`; `GOOS=darwin go vet ./internal/macosuser`.
- **Fences:** not `orchestrator.go`, `real.go`, `envfile.go`, `cabundle.go`, `sessionfiles.go`,
  `capture.go`, `preconditions.go`, `seatbelt*.go`. Tests in those files' `_test.go` that only
  change because of the new root literal are yours; behavior tests are not.
- **Tests that break and must be rewritten** (literal `/var/yolo-jail` or `sudo` prefixes):
  `macosuser_test.go`, `packroot_test.go`, `stagedyolopath_test.go`, `staging_darwin_test.go`,
  `ctxlinks_test.go`, `ctxtree_test.go`, `capturestore_test.go`, `relocations_test.go`,
  `contextsiting_darwin_test.go`, `provision_test.go`, `serviceprobe_test.go`, `jaildaemon_test.go`
  (argv cases only), `hostlayers_test.go`, `loginpath_test.go`, `mcppresets_test.go`,
  `misedatadir_test.go`, `supervisorlog_test.go`, and in other packages
  `internal/cli/run/macosctxcopies_test.go`, `internal/entrypoint/hostfiles_test.go`,
  `internal/jailcontent/contextcopies_test.go`, `internal/loopholes/guestrun_test.go` (each only if
  it fails). `rg -l '/var/yolo-jail' internal --glob '*_test.go'` is the list.
- **Commit:** `feat(macos-user): stage into a host-owned root and cross only through the run-as rule`
- **Report:** the red line, the green gates, and which `_test.go` files you rewrote.

### Task 3 — the Seatbelt read allow

**After 1.** Parallel with 2 and 4.

- **Settled:** "The Seatbelt profile must allow the guest to read the new staging root." Writes
  stay denied. Do not re-open.
- **Tests first:** in `seatbeltdenies_test.go`:

  ```go
  func TestBothProfilesLetTheGuestReadTheStagingRootAndNotWriteIt(t *testing.T) {
  	allow := "(subpath \"/Users/Shared/yolo-staged\")"
  	for name, p := range map[string]string{
  		"session": SeatbeltProfile("/Users/Shared/yolo/proj", SandboxHome(), nil, HomeReadonly{}),
  		"capture": SeatbeltCaptureProfile("/Users/Shared/yolo-captures/jq"),
  	} {
  		i := strings.Index(p, "(deny file-read* (subpath \"/Users\"))")
  		j := strings.LastIndex(p, allow)
  		if i < 0 || j < i {
  			t.Errorf("%s profile: no read allow for the staging root after the /Users deny", name)
  		}
  		if w := strings.Index(p, "(allow file-write*"); w >= 0 {
  			end := strings.Index(p[w:], "\n\n")
  			if end > 0 && strings.Contains(p[w:w+end], allow) {
  				t.Errorf("%s profile: the staging root is writable", name)
  			}
  		}
  	}
  }
  ```

  Red: `go test -short -run TestBothProfilesLetTheGuestReadTheStagingRoot ./internal/macosuser`.
- **Change:** `seatbelt.go` `seatbeltProfile` adds `(subpath <staging root>)` inside the
  `workspace-read-allow` block with a `;; #seatbelt-test-id:staging-root-read-allow#` marker;
  `seatbeltcapture.go` `SeatbeltCaptureProfile` adds the same clause to its `/Users` read allow,
  which the sealed fork-build profile inherits. Advice: take the root from `StagingRoot()`, not a
  parameter, unless a test needs a temp root (the integration Seatbelt gate stages under the real
  root; see task 9).
- **Gates:** `go test -short ./internal/macosuser`.
- **Fences:** `seatbelt.go`, `seatbeltcapture.go`, `seatbelt*_test.go` only. Byte-pinned profile
  tests (`seatbelt_readonly_test.go` and the dry-run goldens) are rewrites, not repairs.
- **Commit:** `feat(macos-user): let the guest read the host-owned staging root under Seatbelt`

### Task 4 — the shared preflight (G2 and the staging root)

**After 1.** Parallel with 2 and 3.

- **Settled:** the probe joins the shared readiness predicates before the native build,
  auto-capture, the context and cache probes and staging; launch, `yolo check --no-build`, auto
  capture and the fork executor consume the same result; unknown is a refusal. Do not re-open.
- **Tests first:** in `preconditions_test.go`, add the four `failOne` entries (the existing
  `TestTheLaunchRefusesOnEachPreconditionWithItsOwnMessage` then proves the launch call site), and:

  ```go
  func TestTheLaunchAsksForTheStagingRootAndTheRunAsGrant(t *testing.T) {
  	var ids []string
  	for _, c := range LaunchPreconditions() {
  		ids = append(ids, c.ID)
  	}
  	want := "macos,not-root,sandbox-exec,sandbox-user,sandbox-home,staging-root," +
  		"staging-root-owner,staging-root-safe,run-as-grant,workspace-location,workspace-shared"
  	if got := strings.Join(ids, ","); got != want {
  		t.Errorf("preconditions = %s\nwant %s", got, want)
  	}
  }
  ```

  `failOne` cases: `staging-root` sets `d.StagingRootFacts` to return `StagingRootFacts{}`;
  `staging-root-owner` to an owned-by-602 directory; `staging-root-safe` to a `0775` directory of
  uid 501; `run-as-grant` sets `d.RunAsGrant = func() bool { return false }`.
  Red: `go test -short -run 'TestTheLaunchAsksFor|TestEveryPrecondition' ./internal/macosuser`.
- **Change:**
  - `preconditions.go`: `LaunchProbes` gains `StagingRoot func(path string) StagingRootFacts` and
    `RunAsGrant func() bool`; `launchProbes()` maps them from `Deps`. Four preconditions after
    `sandbox-home`: `staging-root` (Requires macos), `staging-root-owner` (Requires staging-root),
    `staging-root-safe` (Requires staging-root-owner), `run-as-grant` (Requires sandbox-user).
    Export `PrivilegeUnmet(p LaunchProbes) (Precondition, bool)`: the first of those four, or of
    their requirements, that does not hold.
  - `orchestrator.go`: `Deps` gains `StagingRootFacts func(string) StagingRootFacts` and
    `RunAsGrant func() bool`; `RealDeps` wires `stagingRootFactsReal` and `runAsGrantReal`.
    Touch nothing else in this file.
  - `real.go`: `stagingRootFactsReal` (`os.Lstat`, owner from `Sys().(*syscall.Stat_t)`, ACL from
    `/bin/ls -lde <path>`: any line after the first is an ACE) and `runAsGrantReal` (run
    `RunAsGrantProbeArgv()` with stdin nil and a 10 s `runWithTimeout`; anything but exit 0 is
    false).
  - `orchestrator_test.go` `mockDeps`: both new fields answer "ready" (owner 501, `0755`, no ACL;
    grant true). Touch only that hunk.
  - `internal/cli/check/sections_macos.go` `macosLaunchProbes`: `StagingRoot` from a new
    `Options.MacosStagingRoot` seam (default `macosuser` real facts, set in `fillDefaults`), and
    `RunAsGrant` through `o.Exec(macosuser.RunAsGrantProbeArgv(), "", nil, 10*time.Second)`.
    `newMacMachine` sets a passing `MacosStagingRoot`.
  - `internal/cli/hostfloor.go`: `macCaptureProbes` drops `StdinIsTerminal` and
    `SudoWithoutPassword` for `Privilege func() (Precondition, bool)`
    (`macosuser.PrivilegeUnmet(macosuser.RealLaunchProbes())`); `macActBlocked`'s fourth case
    becomes the privilege refusal below. `CaptureHow` and `internal/hostfloor/built.go` `~:231` lose
    "sudo may ask for your password".
  - `internal/cli/run/autocapture_test.go` `setUpMacProbes`: the new fields, ready; add a case per
    new precondition to `TestAMacosUserLaunchTheBackendWillRefuseAutoCapturesNothing`.
  - `internal/cli/capturelandlock_test.go` `macSetup`: replace `terminal`/`sudo` with a
    `privilege` fact; tests built on "no terminal, no sudo" are rewrites.
- **Refusal texts** (rich markup; `%s` is the root path):

  | ID | Unmet | Fix | Refusal |
  | :--- | :--- | :--- | :--- |
  | `staging-root` | `The staging directory /Users/Shared/yolo-staged does not exist or cannot be read` | `Run the one-time setup as your normal admin user (not with sudo): yolo macos-setup` | `[bold red]The macos-user staging directory /Users/Shared/yolo-staged is missing.[/bold red]\nThis Mac was set up by an older yolo, or not at all. Run the one-time setup as your normal admin user, not with sudo; it asks for your password once:\n  [bold]yolo macos-setup[/bold]` |
  | `staging-root-owner` | `The staging directory /Users/Shared/yolo-staged belongs to another account` | `One macOS user per Mac runs the macos-user backend. If another user set it up, they run yolo macos-teardown; otherwise an administrator inspects it (ls -le /Users/Shared/yolo-staged) and removes it (sudo rm -rf /Users/Shared/yolo-staged). Then run yolo macos-setup.` | `[bold red]Refusing the macos-user launch: /Users/Shared/yolo-staged belongs to another account.[/bold red]\nA launch stages what the sandbox reads there, so it must be yours.\nIf another macOS user set this Mac up, they run [bold]yolo macos-teardown[/bold] first. Otherwise an administrator inspects it with `ls -le /Users/Shared/yolo-staged` and removes it with `sudo rm -rf /Users/Shared/yolo-staged`.\nThen run [bold]yolo macos-setup[/bold] as your normal admin user.` |
  | `staging-root-safe` | `The staging directory /Users/Shared/yolo-staged is a link, writable by others, or carries an access list` | `Run yolo macos-setup as your normal admin user; it resets the directory's mode and access list.` | `[bold red]Refusing the macos-user launch: /Users/Shared/yolo-staged is not safe to stage into[/bold red] (a symbolic link, writable by another account, or carrying an access list).\nRepair it as your normal admin user:\n  [bold]yolo macos-setup[/bold]` |
  | `run-as-grant` | `sudo cannot run commands as _yolojail without a password` | `Run the one-time setup as your normal admin user: yolo macos-setup. It installs one rule, /etc/sudoers.d/yolo-jail, which runs commands as _yolojail, never as root. On a managed Mac that refuses it, ask your administrator to allow that rule.` | `[bold red]This launch would stop at a password prompt:[/bold red] sudo cannot run commands as _yolojail without one (`/usr/bin/sudo -n --user=_yolojail /usr/bin/env /usr/bin/true` failed).\nRun the one-time setup as your normal admin user, not with sudo; it asks for your password once:\n  [bold]yolo macos-setup[/bold]\nOn a managed Mac that refuses it, ask your administrator to allow the rule it prints. `yolo check --no-build` asks the same question.` |

  The floor's text (`macActBlocked`): `<act> on a Mac runs as the sandbox account through the
  one-time setup's sudo rule, which this Mac does not have (<Unmet>) — <Fix>, and the next `yolo
  host` launch <does>`.
- **Gates:** `go test -short ./internal/macosuser ./internal/cli/check ./internal/cli/run ./internal/cli`.
- **Fences:** in `orchestrator.go` the `Deps` struct and `RealDeps` only; in `real.go` the two new
  functions only. Nothing in `capture.go` (task 6 calls `PrivilegeUnmet`).
- **Commit:** `feat(macos-user): probe the run-as rule and the staging root before every launch`
- **Report:** the red line, the green gates, and the check's new rows as `yolo check` renders them
  in `sections_macos_test.go`.

### Task 5 — the launch path writes and removes as the host user

**After 2 and 4.**

- **Settled:** per-launch files written by the host user, credentials `0600` plus the read ACE in
  the `0700` env dir; the host deletes its own files at exit; cleanup never prompts; a failed
  removal keeps the liveness record and warns, naming the next step. Do not re-open.
- **Tests first:** in `terminate_test.go`, replace `TestAnUnsignaledTeardownKeepsPlainSudo` and
  rewrite `TestASignalDuringTheBootstrapEndsTheLaunchThroughItsTeardown`'s `sudo -n` assertion; add:

  ```go
  func TestALaunchRunsNoRootSudoAndEveryCrossingIsTheRule(t *testing.T) {
  	var rec []string
  	d := mockDeps(&rec)
  	if rc := RunMacosUser(d, newOpts("/Users/Shared/yolo/proj")); rc != 42 {
  		t.Fatalf("rc = %d, want the session's 42", rc)
  	}
  	for _, r := range rec {
  		argv, ok := strings.CutPrefix(r, "run:")
  		if !ok {
  			argv, ok = strings.CutPrefix(r, "proxy:")
  		}
  		if !ok {
  			continue
  		}
  		words := strings.Fields(argv)
  		if len(words) > 0 && strings.HasSuffix(words[0], "sudo") {
  			if p := RunAsArgvProblems("t", words); len(p) > 0 {
  				t.Errorf("%s: %v", r, p)
  			}
  		}
  	}
  	if recAt(rec, "write:"+StagingRoot()+"/profile-") < 0 {
  		t.Errorf("the profile was not written as the host:\n%s", strings.Join(rec, "\n"))
  	}
  	if recAt(rec, "run:"+rmBin+" -f "+StagingRoot()+"/env/") < 0 {
  		t.Errorf("the teardown did not remove the env files as the host:\n%s", strings.Join(rec, "\n"))
  	}
  }
  ```

  (`mockDeps` records `write:<path>` from the new `WriteHostFile` seam; rename its `install:`
  entry.) Red: `go test -short -run TestALaunchRunsNoRootSudo ./internal/macosuser`.
  Add to `envfile_test.go`, Linux-runnable:

  ```go
  func TestWriteHostFileCreatesAtItsFinalModeAndNeverFollowsOrReplaces(t *testing.T) {
  	old := syscall.Umask(0)
  	defer syscall.Umask(old)
  	dir := t.TempDir()
  	p := filepath.Join(dir, "k.env")
  	if !writeHostFileReal(p, "export A='1'\n", 0o600) {
  		t.Fatal("write failed")
  	}
  	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
  		t.Errorf("mode %v", fi.Mode().Perm())
  	}
  	if writeHostFileReal(p, "x", 0o600) {
  		t.Error("replaced an existing file")
  	}
  	link := filepath.Join(dir, "l")
  	_ = os.Symlink(filepath.Join(dir, "target"), link)
  	if writeHostFileReal(link, "x", 0o444) {
  		t.Error("wrote through a symlink")
  	}
  	if _, err := os.Lstat(filepath.Join(dir, "target")); err == nil {
  		t.Error("created the link's target")
  	}
  }
  ```
- **Change:**
  - `orchestrator.go`: `Deps.InstallRootFile` becomes `WriteHostFile func(path, content string,
    mode os.FileMode) bool`; `RealDeps` wires `writeHostFileReal`. The stage loop (`~:1209`) and
    `stageCaptures` (`~:1480`) run `deps.Run(cmd)`. `finishPackTree` (`~:1419`) runs the unseal
    then `rmBin -rf --`; its texts drop `sudo`. Delete `teardownDeps` and its uses (`~:1153`,
    `~:1418`); every routine sudo already carries `-n`. Remove "sudo may prompt" from
    `~:1138` and `~:1614`. `PrintPlan` (`~:1913` to `~:1980`): one section "as you (no sudo)" for
    staging, writes (`write <path> (0444|0600, content never on argv)`) and grants, and the
    crossings printed as the full argv; drop "sudo may prompt".
  - `real.go`: `writeHostFileReal(path, content, mode)` creates the parent with `0755`
    (`env/` is created by its own dir commands), opens with
    `O_WRONLY|O_CREATE|O_EXCL|syscall.O_NOFOLLOW` at `mode`, `Fchmod`s to `mode` (umask-proof),
    writes, `Sync`s, closes; any error removes the partial file and returns false. Delete
    `installRootFileReal`.
  - `envfile.go` `installSandboxEnvFile` and `cabundle.go` `installCATrustFiles`: `deps.Run(cmd)`
    for dir and grant argvs; `WriteHostFile(path, content, 0o600)`. Comments about `sudo tee` and
    its window go.
  - `sessionfiles.go`: `sessionTeardown.remove` runs the argv as is; `finish`'s warning names
    `/bin/rm -f <paths>` with no `sudo`. `sweepGoneSessions` removes as host; PL-8: if
    `<LegacyStateDir()>/profile-<key>.sb` exists, keep the record (close the claimed file without
    removing it) and print once per launch:
    `[yellow]Warning: a macos-user session from an older yolo left root-owned files in /var/yolo-jail[/yellow] (session <key>). Launches no longer run root commands, so they cannot remove them. Run [bold]yolo macos-setup[/bold] (it asks for your password once), or remove them yourself: `sudo rm -f <the five legacy paths>`.`
    `openSessionRecordOrWarn`'s text drops `sudo`. The package comment's "sudo rm -f" lines become
    host removals.
- **Gates:** `go test -short ./internal/macosuser`; `GOOS=darwin go vet ./internal/macosuser`.
- **Fences:** not `capture.go`, `commands.go`, `preconditions.go`, `seatbelt*.go`.
- **Tests that break and must be rewritten:** `orchestrator_test.go` (dry-run and `install:`
  cases), `terminate_test.go`, `sessionfiles_test.go` (`TestATeardownWhoseRemovalFailsKeepsTheRecordForTheNextSweep`
  keeps its shape with a host `rm`), `envfile_test.go`, `cabundle_test.go`, `dryrunwords_test.go`,
  `jaildaemon_test.go` `TestADaemonsEnvFileWhoseInstallFailsIsRemovedThroughTheTeardown`,
  `runtiming_test.go`, `launchwriter_test.go`. `fixtureStateDeps`' `sudo` stripping is deleted.
- **Commit:** `feat(macos-user): write and remove session files as the host user`
- **Report:** the red line; green gates; every test rewritten and the one-line reason.

### Task 6 — capture and fork build

**After 5.** Parallel with 7 and 8.

- **Settled:** capture and fork-build files follow the session files; scratch creation and
  deletion run as host or guest, never root; a failed scratch cleanup never fails a successful
  capture; the fork executor consumes the same readiness result. Do not re-open.
- **Tests first:** in `capture_test.go`, mirror task 5's audit over `RunCaptureAct` and
  `RunForkBuildAct` with `mockDeps`, plus:

  ```go
  func TestACaptureRefusesBeforeAnyWorkWhenTheRuleIsMissing(t *testing.T) {
  	for name, act := range map[string]func(Deps) int{
  		"capture": func(d Deps) int { return RunCaptureAct(d, testCaptureOptions(), t.TempDir(), false) },
  		"fork":    func(d Deps) int { return RunForkBuildAct(d, testForkBuildOptions(), t.TempDir(), t.TempDir(), false) },
  	} {
  		var rec []string
  		var buf bytes.Buffer
  		d := mockDeps(&rec)
  		d.Out = &buf
  		d.RunAsGrant = func() bool { return false }
  		if rc := act(d); rc != 1 {
  			t.Errorf("%s: rc = %d", name, rc)
  		}
  		if !strings.Contains(buf.String(), "yolo macos-setup") ||
  			recAt(rec, "sudo") >= 0 || recAt(rec, "write:") >= 0 {
  			t.Errorf("%s: printed %q and ran %v", name, buf.String(), rec)
  		}
  	}
  }
  ```

  (`testCaptureOptions` and `testForkBuildOptions` are `capture_test.go`'s fixtures.) Red: `go test -short -run TestACaptureRefusesBeforeAnyWork ./internal/macosuser`.
- **Change:** `RunCaptureAct` asks `captureGatesPass` before `ComposeCATrust`, as
  `RunForkBuildAct` already does before its toolchain build (advice: the design wants the refusal
  before any costly or privileged work). `captureGatesPass` asks `PrivilegeUnmet(deps.launchProbes())` after the account
  check and prints that precondition's `Refusal("")`; then the capture root: `JudgeStagingRoot`
  over `deps.StagingRootFacts(CaptureRootDefault())`, where absent is fine (the host creates it,
  `0755`), and foreign or unsafe refuses with
  `[bold red]Refusing the capture: /Users/Shared/yolo-captures belongs to another account or is not safe.[/bold red] Run [bold]yolo macos-setup[/bold] as your normal admin user; it gives the directory to you.`
  `runCaptureSteps` runs prepare and stage argvs as host; `CaptureStagingCommands` and
  `ForkBuildStagingCommands` use `/usr/bin/chgrp` instead of `chown`; `WriteHostFile` for the
  profile; `RunCaptureCleanup` per PL-7 (the guest pass is
  `RunAsSandboxArgv(nil, chmodBin, "-R", "--", "u+w", root)` then `RunAsSandboxArgv(nil, rmBin,
  "-rf", "--", root)`); `CaptureDriverArgv`, `ForkBuildDriverArgv` use the run-as prefix;
  `capturePlanProblems` gets the rule-shape check; the `SourceCommands` "copied under sudo" check
  keeps working against both spellings. `PrintCapturePlan` and the "sudo may prompt once" line
  follow `PrintPlan`'s new shape.
- **Gates:** `go test -short ./internal/macosuser ./internal/cli`.
- **Fences:** `capture.go`, `capture_test.go`, `forkbuilds_test.go`, `forkbuildact_test.go`,
  `capturestore_test.go` (behavior only), `internal/cli/capturehost.go` only if a text names sudo.
- **Commit:** `feat(macos-user): run capture and fork builds without root`

### Task 7 — setup installs the rule; teardown removes it

**After 5.** Parallel with 6 and 8.

- **Settled:** G0 writes the rule after `visudo -c -f`, with its own `Defaults secure_path`;
  one enrolled user per Mac, a second refused naming the remedy; setup declares ready only after
  the exact G2 probe passes as the setup principal; setup reports the standing authority; it never
  kills old sessions or adopts legacy trees. Do not re-open.
- **Tests first:** `internal/macosuser/setupgrant_test.go` (new), with `mockDeps` plus a fake
  `StagingRootFacts` and a `Run` that answers `sudo /bin/cat` of the rule:

  ```go
  func TestSetupInstallsTheRuleThenProvesItWithAnEmptyCache(t *testing.T) {
  	var rec []string
  	d := mockDeps(&rec)
  	d.StagingRootFacts = func(string) StagingRootFacts { return StagingRootFacts{} } // fresh Mac
  	d.HostUser = func() string { return "matt" }
  	if rc := MacosSetup(d); rc != 0 {
  		t.Fatalf("rc = %d", rc)
  	}
  	order := []string{
  		"run:/usr/bin/sudo /usr/sbin/visudo -c -f /etc/sudoers.d/.yolo-jail.new",
  		"run:/usr/bin/sudo /bin/mv -f /etc/sudoers.d/.yolo-jail.new /etc/sudoers.d/yolo-jail",
  		"run:/usr/bin/sudo /usr/sbin/visudo -c",
  		"run:/usr/bin/sudo -k",
  		"run:/usr/bin/sudo -n --user=_yolojail /usr/bin/env /usr/bin/true",
  	}
  	last := -1
  	for _, w := range order {
  		i := slices.Index(rec, w)
  		if i <= last {
  			t.Fatalf("%q missing or out of order:\n%s", w, strings.Join(rec, "\n"))
  		}
  		last = i
  	}
  }

  func TestSetupRefusesASecondEnrolledUserBeforeAnySudo(t *testing.T) {
  	var rec []string
  	var buf bytes.Buffer
  	d := mockDeps(&rec)
  	d.Out = &buf
  	d.StagingRootFacts = func(string) StagingRootFacts {
  		return StagingRootFacts{Exists: true, IsDir: true, UID: 502, Mode: 0o755}
  	}
  	if rc := MacosSetup(d); rc != 1 {
  		t.Fatalf("rc = %d", rc)
  	}
  	if recAt(rec, "sudo") >= 0 {
  		t.Errorf("ran sudo before refusing: %v", rec)
  	}
  	if !strings.Contains(buf.String(), "yolo macos-teardown") {
  		t.Errorf("the refusal names no remedy:\n%s", buf.String())
  	}
  }
  ```

  Red: `go test -short -run 'TestSetup' ./internal/macosuser`. Also a teardown test: the rule and
  the staging root are removed, the legacy dir is not.
- **Change:** `commands.go` `MacosSetup`, after the account and shared-root steps:
  1. Enrollment: judge the staging root. Foreign with a human uid (≥ 501, not `_yolojail`) →
     second-enrollment refusal, naming the owner (`os/user.LookupId`); foreign otherwise → squat
     refusal. Both before any `sudo`. Then ask the existing rule through `deps.Run`:
     `/usr/bin/sudo /usr/bin/grep -s -qxF -e '<user> ALL=(_yolojail) NOPASSWD: /usr/bin/env'
     /etc/sudoers.d/yolo-jail` (`-s`: a first run has no file, and grep says nothing about it). Exit 0 is ours, 2 is absent (both continue), 1 is another
     user's rule: the second-enrollment refusal, naming "another macOS user".
  2. Disclose: `• Granting <user> one standing right: run commands as _yolojail, never as root,
     without a password (/etc/sudoers.d/yolo-jail). Launches then start and stop with no prompt.`
  3. Staging root (PL-6), every act through `deps.Run` so a test records it: absent →
     `/bin/mkdir -m 0755 <root>` as host; then `/bin/chmod 0755 <root>` and `/bin/chmod -N <root>`
     as host. Capture root: absent → the same as host; root-owned →
     `/usr/bin/sudo /usr/sbin/chown <user>:<primary group> <root>` (the group from `id -gn`), then
     the host's two `chmod`s.
  4. The rule: `SudoersRule(user)` into a host temp file; `sudo /bin/cp` to the `.new` name,
     `sudo /usr/sbin/chown root:wheel`, `sudo /bin/chmod 0440`, `sudo /usr/sbin/visudo -c -f`,
     `sudo /bin/mv -f`, then `sudo /usr/sbin/visudo -c`; if that last check fails, the rule is
     parked under the `.new` name (`sudo /bin/mv -f`) and `sudo /usr/sbin/visudo -c` runs again
     without it. When the second check fails with exactly the complaints of the first (the rule
     adds none), the rule goes back in and setup warns; otherwise the parked rule is removed and
     setup refuses. Neither check takes `-q`, which would hide visudo's reason. sudo itself reads a
     `sudoers.d` file that root owns and that no one else can write, whatever its other mode bits,
     and skips with a warning one it cannot trust (sudo's `open_sudoers` and the lexer's
     `includedir` loop); only `visudo -c` requires mode 0440, so a pre-existing mode or owner
     complaint does not stop sudo honoring the rule, and step 6's probe proves it does.
  5. Legacy sweep (PL-8): for each claimable record whose legacy profile exists, `sudo /bin/rm -f`
     the five legacy paths, then remove the record; print the count and `yolo stores` for the
     legacy trees.
  6. `/usr/bin/sudo -k`, then the G2 probe as `deps.Run(RunAsGrantProbeArgv())` (not
     `deps.RunAsGrant`, so the test sees its order); fail → the verdict below; pass → the ready
     verdict, replacing "sudo will prompt per run" with "Launches start and stop with
     no password from now on."
  `MacosTeardown`: after the account steps, `sudo /bin/rm -f /etc/sudoers.d/yolo-jail`, then the
  host unseals and removes the staging root if it owns it; print that `/var/yolo-jail`, if
  present, is left for `yolo stores`.
- **Texts:**
  - Second user: `[bold red]This Mac's macos-user backend is set up for another macOS user (<name>).[/bold red] One user per Mac. That user runs [bold]yolo macos-teardown[/bold], then you run [bold]yolo macos-setup[/bold]. If that account is gone, an administrator runs `sudo rm -f /etc/sudoers.d/yolo-jail && sudo rm -rf /Users/Shared/yolo-staged`, then you run [bold]yolo macos-setup[/bold].`
  - Squat: `[bold red]/Users/Shared/yolo-staged already exists and belongs to <owner>, not to you.[/bold red] Setup does not adopt it: whoever owns it controls what the sandbox reads. Inspect it with `ls -leR /Users/Shared/yolo-staged`, remove it with `sudo rm -rf /Users/Shared/yolo-staged`, then run [bold]yolo macos-setup[/bold] again.`
  - Every visudo refusal: one red `✗` headline, a blank line, visudo's captured output (a passing
    check prints nothing), a blank line, then the commands, one per line, with no placeholders.
  - The rule file alone fails: `✗ visudo rejected yolo's sudo rule. Nothing was installed.`, then
    `This is a yolo bug. Report it with the output above and this rule:`, the rule, the issues URL.
  - Before the first sudo of setup or teardown, when `sudo -n /usr/bin/true` (captured) fails,
    so a prompt is coming: `• sudo needs your password to <set up the sandbox account and its
    rule | remove the sandbox account and its rule | remove yolo's sudo rule>
    (/etc/sudoers.d/yolo-jail).` Cached credentials print nothing.
  - The whole configuration fails the same with and without the rule: a yellow
    `⚠ Installed /etc/sudoers.d/yolo-jail. visudo -c also fails without it:`, a blank line,
    visudo's complaints, a blank line, then the fixes at the paths visudo printed: one
    `sudo chmod 0440 <files>` for the bad modes, one `sudo chown root:wheel <files>` for the bad
    owners, `sudo visudo -f <path>` for each other file named, and `sudo visudo -c`.
  - Moving the checked rule into place fails, first or after the comparison accepted it:
    `✗ yolo's sudo rule is not installed: <the mv> failed.`, the pre-existing complaints when
    there are any, their fixes, then `yolo macos-setup`.
  - Both fail with nothing captured to compare:
    `✗ Your sudo configuration is invalid without yolo's rule too. Nothing was installed.`, then
    the same fixes, `sudo visudo -c`, `yolo macos-setup`.
  - It fails only with the rule, or the rule adds a complaint: `✗ visudo rejected your sudo
    configuration with yolo's rule in place. The rule was removed again.`, the added complaints
    (all of visudo's output when the configuration passes without the rule), then the yolo-bug
    lines.
  - The rule cannot be removed after that: `sudo rm -f /etc/sudoers.d/yolo-jail`, a no-sudo
    fallback (`osascript -e 'do shell script "/bin/rm -f /etc/sudoers.d/yolo-jail" with
    administrator privileges'`), then `yolo macos-setup`.
  - Probe fails after install: `[bold red]✗ The rule is installed, but sudo still asks for a password to run as _yolojail.[/bold red] Check that /etc/sudoers reads /private/etc/sudoers.d (`sudo grep -n includedir /etc/sudoers`); a managed Mac may block it, and then your administrator has to allow the rule. Run [bold]yolo macos-setup[/bold] again afterwards.`
  - Unsafe user name: `[bold red]Your user name <name> cannot be written into a sudo rule safely.[/bold red] The macos-user backend supports names of letters, digits, '_', '.' and '-'.` (that is the whole remedy: there is no workaround to offer).
- `internal/cli/commands.go`: `macosSetupUsage` names the rule and the staging root and says
  setup asks for the admin password once; `macosTeardownUsage` says it removes them.
- **Gates:** `go test -short ./internal/macosuser ./internal/cli`.
- **Fences:** `commands.go`, `setupgrant_test.go`, `setupnix_test.go` and the `MacosSetup` and
  `MacosTeardown` tests in `orchestrator_test.go` (texts only), `internal/cli/commands.go`.
- **Commit:** `feat(macos-user): setup installs the one run-as rule; teardown removes it`

### Task 8 — `yolo stores` and the census sentence

**After 2.** Parallel with 6 and 7.

- **Change:** `internal/cli/stores/inventory.go`: the per-workspace leaves list the staging root,
  host-owned and measurable, with removal commands that unseal then remove, no `sudo`; a second
  section for `LegacyStateDir()` when present, root-owned, with today's `sudo` commands and a
  sentence that a launch no longer writes there. `stores.go` `~:85` help text and `~:371` return
  both roots. `internal/setupcensus/kinds.go` `~:159`: "copied into a host-owned read-only tree
  under /Users/Shared/yolo-staged".
- **Tests:** `internal/cli/stores/macosuserrows_test.go`: rewrite to the new root; add a legacy
  row case. Red: the new legacy case.
- **Gates:** `go test -short ./internal/cli/stores ./internal/setupcensus`.
- **Commit:** `feat(stores): list the macos-user staging root and the legacy /var/yolo-jail`

### Task 9 — integration and the narrow-sudo CI step

**After 5, 6, 7.**

- **Settled:** startup and exit without a password, with an invalidated cache, with and without a
  terminal; a cached broad rule must not hide a missing runtime rule. Do not re-open.
- **Change:**
  - `integration/macosusergate_test.go`: `sudoQuiet` becomes `runAsQuiet`, probed with
    `macosuser.RunAsGrantProbeArgv()`; its `gate()` reason names `yolo macos-setup`. Tests that use
    root `sudo` for their own fixtures keep it and therefore run only in the main step.
  - `integration/macosuserterminate_test.go`: `macosUserSessionEnvFiles` uses `os.ReadDir`, the
    survivors check `os.Lstat`; `env/` is the host's now.
  - Literal `/var/yolo-jail` in `macosusercontextmounts_test.go`, `macosusergitignore_test.go`,
    `macosuserjaildaemon_test.go`, `macosuserjaildaemonprobe_test.go`,
    `macosuseroverlaystate_test.go`, `macosuserseatbelt_test.go`, `macosuserstores_test.go`:
    rewrite to `macosuser.StateDir()`.
  - `integration/macosuserpasswordless_test.go` (new):

    ```go
    // TestMacosUserPasswordlessStartAndExit is the measurement OQ-MP10 left UNMEASURED: with root
    // sudo out of reach, a launch with no terminal starts, runs and tears down. Declared by
    // YOLO_TEST_MACOS_USER_NARROW_SUDO=1, which only the narrow-sudo CI step and the owner's
    // runbook set; elsewhere it skips, because a broad rule would make it pass vacuously.
    func TestMacosUserPasswordlessStartAndExit(t *testing.T) {
    	requireMacosUser(t)
    	if os.Getenv("YOLO_TEST_MACOS_USER_NARROW_SUDO") != "1" {
    		t.Skip("set YOLO_TEST_MACOS_USER_NARROW_SUDO=1 once root sudo needs a password")
    	}
    	_ = exec.Command("/usr/bin/sudo", "-k").Run()
    	if runQuiet(10*time.Second, "/usr/bin/sudo", "-n", "/usr/bin/true") {
    		t.Fatal("root sudo still needs no password, so this run proves nothing; narrow it first")
    	}
    	if !runQuiet(10*time.Second, macosuser.RunAsGrantProbeArgv()...) {
    		t.Fatal("the G2 probe fails: run `yolo macos-setup` before this test")
    	}
    	ws := macosUserWorkspace(t, `{}`)
    	before, _ := os.ReadDir(filepath.Join(macosuser.StateDir(), "env"))
    	r := runMacosUser(t, ws, `echo "=== AGENT $(id -un) ==="`)
    	if r.rc != 0 || !strings.Contains(r.stdout, "=== AGENT _yolojail ===") {
    		t.Fatalf("the launch did not reach the agent as _yolojail (rc %d):\n%s", r.rc, r.combined())
    	}
    	if strings.Contains(r.combined(), "assword") {
    		t.Errorf("the launch mentioned a password:\n%s", r.combined())
    	}
    	after, _ := os.ReadDir(filepath.Join(macosuser.StateDir(), "env"))
    	if len(after) > len(before) {
    		t.Errorf("the session left %d env file(s) behind", len(after)-len(before))
    	}
    	fi, err := os.Lstat(macosuser.StateDir())
    	if err != nil || fi.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
    		t.Errorf("the staging root is not this user's: %v", err)
    	}
    }
    ```

    Add `TestMacosUserTheGuestCannotWriteTheStagingRoot`: as `_yolojail` through the rule, under no
    profile, `touch` in the root and in `bin/`, `packs/`, `env/` and a staged `.sb` fails; a `cat`
    of a profile and `--version` of the staged yolo succeed (PL-2's measurement, the ruling's
    table redone at the real root).
  - `.github/workflows/macos-user.yml`: after the existing integration step, a step "Narrow sudo
    to the enrolled rule" that prints `sudo grep -rn NOPASSWD /etc/sudoers /etc/sudoers.d`, moves
    the runner's blanket rule aside with `sudo` (validate with `visudo -c` after), and asserts
    `sudo -k; ! sudo -n true`; then a step running
    `go test -count=1 -timeout 0 -v -run '^TestMacosUser(PasswordlessStartAndExit|TheGuestCannotWriteTheStagingRoot|TerminateEndsTheSessionThroughItsTeardown)$' ./integration`
    with `YOLO_TEST_MACOS_USER=1` and `YOLO_TEST_MACOS_USER_NARROW_SUDO=1`. Both steps are last:
    nothing after them may need root. The runner's `%admin` rule turned out to live in
    `/etc/sudoers` itself, so the step also comments out that line, as PL-18 records.
- **Gates:** `go test -short ./integration` on Linux (compile and the gate's table tests); then
  `gh workflow run macos-user.yml` on the pushed branch is the measurement, which the owner
  triggers (pushing is the human's).
- **Commit:** `test(macos-user): measure passwordless start and exit under narrowed sudo`
- **Report:** the gate table test result, and the CI run URL once the owner runs it.

### Task 10 — documentation

**After 9.**

- `userguide/guides/macos.md` `~:48`, `~:49`, `~:194`, `~:213`, `~:364`: setup asks for the
  password once and grants one rule; launches start and stop with no prompt; the staging root
  path; `/var/yolo-jail` from older versions and `yolo stores`.
- `userguide/guides/confinement.md` `~:87`: drop "sudo may ask for your password once".
- `docs/reference/macos-user-provisioning.md` `~:227` to `~:267` and the paths table `~:764` to
  `~:766`: the run-as argv with `-n` and `/usr/bin/sudo`, the host-owned files and modes, the
  teardown and sweep without root, the legacy sweep.
- `docs/plans/runbooks/macos-user-manual-checks.md`: the [owner's check](#the-owners-mac) as a section.
- `CHANGELOG.md` `[Unreleased]`: under `### Changed`, one bold-lead entry and the one action:
  "**macos-user launches no longer ask for a password.** Run `yolo macos-setup` once after
  upgrading; it asks for your admin password and installs one rule that runs commands as the
  sandbox account, never as root. See the macOS guide." (In `CHANGELOG.md` the link is `userguide/guides/macos.md`.)
- `docs/design/macos-user-privilege-lifecycle.md`: record what the measurements found, and move
  `stage` to `BUILT` once tasks 1 to 9 land.
- **Gate:** `UV_OFFLINE=1 scripts/vantage-check.sh check <each file>`.
- **Commit:** `docs(macos-user): passwordless launches after one setup`

## Verification: what runs where

| Claim | Linux (`just check-ci`) | macOS CI without sudo (`ci.yml` `check-macos`) | macOS CI with sudo (`macos-user.yml`) | The owner's Mac |
| :--- | :---: | :---: | :---: | :---: |
| Every crossing argv matches the rule; no routine root `sudo` | yes (tasks 2, 5, 6) | yes | — | — |
| Preconditions and their call sites (launch, check, auto-capture, capture, fork, floor) | yes (task 4) | yes | — | — |
| `writeHostFileReal` modes, no-follow, no replace | yes | yes | — | — |
| Staging argvs execute and seal; `chmod +a` ACEs apply | partly (no ACLs) | yes (`staging_darwin_test.go`) | — | — |
| The Seatbelt profile admits reads of the staging root and refuses writes | text only | — | yes (Seatbelt gate) | — |
| Setup installs a rule `visudo` accepts, and G2 passes after `sudo -k` | argv order only | — | yes (setup step) | yes |
| Start and exit with root sudo out of reach, no terminal | — | — | yes (task 9's narrow step) | yes |
| A password-requiring admin, an expired cache, a terminal and no terminal; capture; `yolo stop`; second terminal | — | — | — | yes |
| Upgrade from an install with `/var/yolo-jail` and an old session running | — | — | — | yes |

### The owner's Mac

Needs the owner's explicit authorization ([what may happen next](macos-user-privilege-lifecycle.md#what-may-happen-next)):
it installs the rule on a real machine. On a Mac set up by an older yolo, with one old session
left running in another terminal. The
[runbook](../plans/runbooks/macos-user-manual-checks.md#11-passwordless-start-and-stop-the-owners-check--new-2026-10-16-never-run)
carries this check as run, with each row's exact text.

1. `yolo macos-setup`: one password prompt; it reports installing the rule, and its ready
   verdict ends "Launches start and stop with no password from now on."; the old session keeps
   running.
2. `sudo -k; yolo check --no-build`: the four new rows pass.
3. `sudo -k; yolo -- /bin/sh -c 'id -un' </dev/null`: prints `_yolojail`, no prompt.
4. In a project with a pack whose program installs by capture: `sudo -k; yolo capture <bin>`:
   no prompt.
5. `sudo -k; yolo -- sleep 600 &`, then from a second terminal `sudo -k; yolo stop`: the session
   ends, no prompt, `ls /Users/Shared/yolo-staged/env` shows none of its files.
6. End the old session; `yolo` once more: the legacy warning names `yolo macos-setup`; running it
   removes the legacy files.
7. Record each result in the design doc's status line, with the macOS version.

## Ships with

- **Unit tests** named in tasks 1 to 8; the call-site tests are `failOne` (launch), the
  auto-capture table, `sections_macos_test.go`, `capturelandlock_test.go`, and task 6's
  capture/fork refusal.
- **Integration:** task 9's two new tests and the rewritten terminate test, plus every
  `TestMacosUser*` in the main step, which now exercises host-owned staging end to end.
- **CLI help:** `macos-setup`, `macos-teardown` (task 7).
- **Dry-run output:** `yolo run --dry-run` and the capture dry run (tasks 5, 6); their byte-pinned
  tests are rewrites.
- **Error and warning texts:** quoted under tasks 4, 5, 6, 7.
- **Docs:** task 10's list. `docs/reference/macos-no-vm-direction.md`,
  `docs/reference/macos-user-home-tiers.md` and `docs/reference/macos-user-nix-and-features.md`
  name `/var/yolo-jail`; check each claim, not only the path.
- **Not changed:** `flake.nix` and the ship set (no new binary), `vendor/`, pack binary pins.

## Don't

- **Don't build any part of the helper** ([the authority boundary](macos-user-privilege-lifecycle.md#3-the-authority-boundary) through [exit and restart](macos-user-privilege-lifecycle.md#6-normal-switch-exit-stop-and-restart)): no protocol, receipts, peer
  checks or `/Library/PrivilegedHelperTools`. They are the fallback.
- **Don't keep a root path "just in case".** No `sudo` fallback when a host write fails: a failed
  write refuses with its remedy, as today.
- **Don't add a terminal check.** The rule makes the terminal irrelevant; the floor's old
  "no terminal to ask on" case is deleted, not kept beside the new one.
- **Don't refresh sudo's timestamp** (`sudo -v`) anywhere; the design rejected it.
- **Don't touch `/var/yolo-jail` from a routine launch**, not even to read `env/` (it is
  `0700 root`).
- **Don't widen the rule** to `/bin/test`, `/bin/rm` or a second line for the probe; everything
  goes through `/usr/bin/env`.
- **Don't give the staging root a group or an inheritable ACL** "so the guest can read it": `0755`
  already does, and either would let a later file inherit write.

## Blockers

None for building. The owner's Mac run needs the owner's authorization; task 9's CI run needs the
owner to push and dispatch it.
