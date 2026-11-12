---
title: "What may a macos-user test assume about its host?"
date: 2026-10-18
status: accepted
stage: BUILT
next: "Done on hardware 2026-10-19 (§12, all conditions met); owed only a macos-user.yml run and the ownership-enabled volume, neither a developer Mac's to give"
tags: [design, macos-user, testing, privilege, seatbelt, measurement]
summary: "The macos-user suite takes no privilege beyond the run-as rule yolo installs; a closed unified log store is a countable policy skip, and mise shims remain ahead of the floor. The answered decisions and implementation are recorded, and native-Mac runtime is verified: on the host of record the suite exits 0 (150 passed, 4 skipped, 0 failed) and the narrow-sudo cell passes, leaving only a macos-user.yml run and an ownership-enabled volume owed."
---

# What may a macos-user test assume about its host?

**Status:** 2026-10-18. All four rulings are compacted into the [Decision Ledger](#decision-ledger). The fixture changes in original commits `98715e67b` and `41dd11e3f`, and subsequent recovery fixes, are in the current source ancestry. Portable guard hardening is in `cab34b522` and `201c4009e`; the `201c4009e` commit itself only changes the test. The earlier `4e7b9b92b` commit changed a documentation heading, not the suite code. Linux and darwin compile/test evidence is recorded in the handoff. **NATIVE-MAC RUNTIME IS NOW
VERIFIED:** 2026-10-19 at `c3a38e634`, on the host of record, `-run '^TestMacosUser'` exits 0 —
150 passed, 4 skipped, 0 failed — and the narrow-sudo cell passes beside it
([§12](#12-what-done-looks-like)). MEASURED: the suite and the narrow-sudo cell on a developer Mac,
2026-10-19. UNMEASURED: a `macos-user.yml` run, and an ownership-enabled volume for the third
relocation case.


> **In short.** The suite may assume only what the backend itself asks a host for. A test that
> needs another account crosses through the one rule `yolo macos-setup` installs, like a launch;
> a fact that is the Mac's own policy, like a closed log store, is a countable skip; and a red
> run on a developer Mac then means a broken change, never an unprivileged host.

**Why it matters.** This suite is the only instrument for a darwin-only, privilege-bearing
backend, and [`integration/macosusergate_test.go`](../../integration/macosusergate_test.go)
opens by saying why it exists: *"A SUITE THAT SKIPS MUST NOT BE INDISTINGUISHABLE FROM A SUITE
THAT PASSES."* The inverse now holds and is unguarded — a suite that FAILS for a privilege it
never declared is indistinguishable from a suite that caught a regression. Twelve tests, every
run, deterministically.

**The shape.** One gate (`requireMacosUser`, probing five host facts) and no new privilege
beside it. Every crossing into the sandbox account goes through one helper built on the launch's
own argv builder, a guard test refuses any other sudo, and one host-fact probe, the log store's,
skips the two tests that read it with its reason in the suite's ledger.

**Cost.** The System-keychain CA test now uses a CA the Mac already trusts and skips where there
is none, the GitHub runner included, so no Mac measures a TLS handshake through the session's
bundle any more. And a cleanup can leave a file the sandbox account wrote where that account
cannot reach it; the test then prints the one command an administrator runs.

**Start at [§3](#3-the-three-classes)** — the classification is the whole argument;
[§5](#5-the-shape) and [§6](#6-two-defects-and-one-design-question-this-run-found) fall out
of it.

**Needs your ruling:** None.

**Reads with:** [`macos-user-privilege-lifecycle.md`](macos-user-privilege-lifecycle.md) (what
privilege the backend itself takes, and when), [`macos-user-build-step-threat-model.md`](macos-user-build-step-threat-model.md)
(Q3, answered on hardware for the first time in [§7](#7-measurements-this-run-produced)),
[`install-capture.md`](../reference/install-capture.md) (hand-off H4, whose landing made this
run possible).

---

## 1. Recommendation and principles

**Remove unneeded privilege and count policy skips. Do not weaken an assertion.** The suite never needs passwordless root `sudo`; the host's unified-log policy is probed once, and tests that need that data skip with a counted reason.

- **P1. A test may assume only what its gate probed.** The gate is the contract. A test that spends an unprobed privilege has no contract, and its failure carries no information.
- **P2. A skip is countable or it is a lie.** The suite ledger records both skips for an unreadable host log store; no privileged skip is added.
- **P3. The suite's privilege floor is the product's.** Every sandbox-account crossing uses the one run-as rule `yolo macos-setup` installs; no test asks for root `sudo`.
- **P4. A developer Mac is a first-class instrument.** It is the only host that can answer questions a GitHub runner structurally cannot ([§7](#7-measurements-this-run-produced) has two), so "it works in CI" is not a sufficient standard for this suite.

## 2. What the gate probes today, and what the suite spends

`requireMacosUser` ([`integration/macosusergate_test.go:344`](../../integration/macosusergate_test.go))
probes five facts through `macosUserHost.gate()`: `goos == darwin`, `euid != 0`,
`sandbox-exec` on PATH, the `_yolojail` account, `/Users/Shared/yolo`, and `runAsQuiet` — the
launch's own readiness probe, `sudo -n --user=_yolojail /usr/bin/env /usr/bin/true`.

That list is deliberate and documented: a launch needs *only* the run-as rule, and the
narrow-sudo CI step takes root `sudo` away on purpose, so probing root would skip the very
tests that measure a launch without it.

| Privilege | Probed by the gate | Spent by the suite | Installed by `yolo macos-setup` |
| :--- | :--- | :--- | :--- |
| `sudo -n --user=_yolojail …` (the run-as rule) | yes | yes | yes |
| Passwordless **root** `sudo` | **no** | **no** (six tests in the baseline run were rewritten) | **no** |
| A readable unified log store (`log show`) | **no** | **yes, 2 tests** | n/a — the host's own policy |

The third row is not a privilege yolo grants or asks for at all: on a managed Mac the unified
log store is closed to a non-admin user, and `log show` answers
`Could not open local log store: Operation not permitted`.

## 3. The three classes

The full suite was run with CI's own command —
`YOLO_TEST_MACOS_USER=1 go test -count=1 -timeout 0 -run '^TestMacosUser' ./integration` —
and every one of the twelve failures reproduced on a re-run at a later commit. **No failure in
classes A or B says anything about yolo's code.**

### 3.1 Class A — six tests spent a privilege `yolo macos-setup` does not install

Each failed with `sudo: a password is required`, in a fixture or a *control* probe rather than
in the behaviour under test. **Only half of them wanted root.** The other three spelled a
crossing off the rule's shape: sudo matches the command's path, and the rule names
`/usr/bin/env`, so `sudo -u _yolojail /bin/ls` is a different command that only a broader grant
admits. Per [OQ-DM1](#decision-ledger), none of the six needs more than setup installs now.

| Test | What it spent | What it does now |
| :--- | :--- | :--- |
| `TestMacosUserCaptureSeatbeltProfileDeniesTheSharedHome` | root `sudo` for the capture plan's prepare steps, and `sudo rm` at cleanup | the prepare steps as the invoking user, exactly as `RunCapturePlan` runs them; cleanup through the sandbox account's own removal |
| `TestMacosUserTrustsSystemKeychainCA` | root `sudo security add-trusted-cert` into the System keychain, and its cleanup | reads the System keychain as the launch does and takes the CAs the launch's own composition keeps; skips, saying so, where there are none; never writes a keychain |
| `TestMacosUserHostFloorIsTheHostUsersAlone` | `sudo -u _yolojail /bin/ls`, off the rule's shape, in the **control** | the control through the rule |
| `TestMacosUserLaunchDeniesReadOfHost0644File` | `sudo --user=_yolojail /bin/bash`, off the rule's shape | through the rule |
| `TestMacosUserMacosLogAsTheSandboxAccountMeasurement` | `sudo --user=_yolojail /bin/sh`, off the rule's shape | through the rule; it records the account's view and asserts nothing about the host's store, so it is not class B |
| `TestMacosUserServiceProbeRefusesAnEndpointTheSandboxCannotRead` | root `sudo` for the launch's endpoint ACL grant | the grant as the invoking user, who owns the endpoint, as the launch applies it |

The same pass removed every root `sudo` left in the suite's cleanups, diagnostic listings and
process kills, each now the invoking user's act or the sandbox account's through the rule.

> [!NOTE]
> **`4d3248ded` was the first commit to attack this class** — *"test(macos-user): unseal the
> staged overlay fixtures before cleanup, so a non-root run removes them"* — and its shape is
> the one the ruling took: remove the need, don't declare it.

### 3.2 Class B — two tests need a readable unified log store

- `TestMacosUserMacosLogBridgeScopesToTheSandbox`: the user-scope `show` returned nothing
  (`SHOW_RC=77`), so the sandbox account's entry could not be looked for.
- `TestMacosUserSeatbeltProfileEnforcesItsRules`: **only its `macos-log-deny` control.** Every
  confinement subtest in it passed — workspace write, workspace read, staging-root read,
  ancestor stat, same-sandbox process info, pty ioctls, declared-device ioctl, and both
  relocation ancestor stats. The profile itself is not in question; the test cannot establish
  that the host could read the log it then proves the sandbox cannot.

The store is the Mac's policy, so there is nothing to remove. Per [OQ-DM2](#decision-ledger)
both skip countably where the invoking user cannot read it: the log-bridge test whole, and of
`SeatbeltProfileEnforcesItsRules` only the `macos_log_store_read_refused` case
([§5](#5-the-shape)).

### 3.3 Class C — one root-`sudo` probe that read as a missing precondition

`TestMacosUserSweepsAKilledSessionsFiles` reported
*"NOT OBSERVED: the killed session left no env file … or profile … so there was nothing for the
next launch to sweep"* ([`integration/macosuserspawnlock_test.go`](../../integration/macosuserspawnlock_test.go)).
**It is class A, and neither stale nor timing.** Its existence probe was root
`sudo -n /bin/test -e`, from when a session's files were root's. On a Mac without passwordless
root `sudo` that probe answered "absent" for every path, so the control reported a kill that
left nothing. The same probe answered the later sweep check, where "absent" is a pass. A
session's files are now the invoking user's, in the staging root it owns, so a plain `lstat` is
the probe, and the sandbox's sleep is stopped by the sandbox account itself, through the rule.

## 4. Why CI cannot see any of this

| Fact about the GitHub runner | Consequence |
| :--- | :--- |
| Passwordless root `sudo` | class A never appears |
| An admin user with an open log store | class B never appears |
| A clean home: no mise tools, no user config, no installed agents | [§6.1](#61-the-nix-floor-loses-to-a-mise-shim) cannot appear |
| Every nix path substituted from `cache.nixos.org` | Q3 is VOID there, every run |

The workflow is honest about the last one and added a `--rebuild` step to force a real build.
**That step has still never produced an answer on a runner**; it did here
([§7](#7-measurements-this-run-produced)).

## 5. The shape

**The suite's privilege floor is the product's** ([OQ-DM1](#decision-ledger)). The 2026-10-18 ruling rejects asking test hosts for root privileges that the product itself does not require. So there is no root-`sudo` probe and no `requireRootSudo`; the gap [P3](#1-recommendation-and-principles) names is closed rather than printed.

- **One way into the sandbox account.** A step that acts as `_yolojail` runs through the
  suite's `sandboxCommand`, which builds its argv with the launch's own
  `macosuser.RunAsSandboxArgv` (`/usr/bin/sudo -n --user=_yolojail /usr/bin/env <argv…>`, the
  one rule setup installs) and starts it in `/`, as the launch's runner starts every crossing.
  A test that runs a production argv, such as the service probe's, runs it as built.
- **Everything else is the invoking user's.** Fixture setup, the capture plan's prepare steps,
  the launch's ACL grants, existence checks in the staging root: the launch and `yolo capture`
  run these as the invoking user, so the tests do too.
- **Cleanup never escalates to root.** A fixture the sandbox account wrote into is removed as
  the invoking user, then by that account's own `chmod -R u+w` and `rm -rf` through the rule
  (the guest pass a capture's cleanup runs), then as the invoking user again. What that leaves,
  where the account cannot reach it, is logged with the one command an administrator runs.
- **The guard.** `TestMacosUserSuiteSpendsNoRootSudo` reads every Go file in `integration/` and fails on an unapproved sudo command literal or a root-sudo command at command position inside a literal passed to `sh`, `bash`, `zsh` or `dash` with `-c`. These source-reading checks run in the Linux short suite, before a Mac is needed. The scanner handles the shell separators and quoting it tests; it is not a general shell interpreter and does not inspect sourced or generated scripts. Root operands include `root` and numeric UID zero (`#0`), whether separated or attached/quoted; command-like diagnostic prose is not treated as execution. The product-built run-as argv remains outside this source scan, and the host-side script that names sudo replaces it with a shell function first.
- **The System-keychain CA test reads, never writes.** It takes the CAs the launch's own
  composition (`ComposeCATrust`) keeps from the System keychain. Where there are none it skips,
  naming that it only reads the keychain; that includes the GitHub runner.

**The unified log store is a countable skip** ([OQ-DM2](#decision-ledger)). The suite probes
it once per run, as the invoking user, with the seatbelt control's own store read (`log show`
returning an `eventMessage`). Where the store is closed, `requireUnifiedLog` skips the log-bridge
test and the one `macos_log_store_read_refused` case of `SeatbeltProfileEnforcesItsRules`; every
other rule in that test still asserts. Each skip enters the suite's ledger with the policy
reason, as a gate skip does, and a test's own reason is never overwritten by the gate's generic
one. An empty read that `log` did not refuse also skips, and its reason says so rather than
blaming the Mac's policy.

Mise shims outrank the floor, as in the jail ([OQ-DM3](#decision-ledger)). The 2026-10-18 ruling preserves `SandboxPath`'s order. The floor test asks two lookups per binary: the agent's, on the whole PATH, and the
floor's, on that PATH with every directory under the sandbox's `$HOME` taken out. The sandbox
home is one account home shared by every workspace and test on the Mac, so a shim another
workspace's mise install left is no fault of this launch's; the second lookup is the floor's own
reach, and only it can fail. A mise shim or another home directory winning the first lookup is
logged as by design, and path_helper and Homebrew are blamed only when the second lookup lost. ⚠
The container jail has the same exposure, through the machine-wide `/mise/shims`
([backlog](../plans/BACKLOG.md#stale-mise-shims)).

**A developer-Mac measurement counts** ([OQ-DM4](#decision-ledger)). A line it clears stays
cleared, recording the host and the date and saying `macos-user.yml` has not confirmed it.

**The vacuity check is unchanged.** A declared macos-user run that executes zero tests, or a gate
nothing got through, still exits non-zero.

**What only a Mac can confirm.** Every rewritten test passing on a Mac that grants only the
run-as rule; the CA test on a Mac whose System keychain holds a TLS CA; the guest-pass cleanup;
and the host's process listing, which was root's `ps` and is now `ps` run by the sandbox account
followed by `ps` run by the invoking user, so each account's processes are listed by their own
uid. The supervisor and doorway watches decide their tests' assertions from it.

## 6. Two defects and one design question this run found

These are the only three failures that are about yolo. [§6.2](#62-another-sessions-doorway-was-read-as-this-sessions-survivor) and
[§6.3](#63-the-housekeeping-tests-slot-was-not-due-because-its-stamp-is-the-machines) are fixed in the tests: in the code,
neither reads as a product defect, and the Mac's re-run is what confirms it.
[§6.1](#61-the-nix-floor-loses-to-a-mise-shim) now has a settled disposition in the [Decision Ledger](#decision-ledger); the rewritten test distinguishes intended mise precedence from a floor lookup failure, pending a Mac run.

### 6.1 The nix floor loses to a mise shim

`TestMacosUserFloorReachesTheSandboxPath` failed because `node`, `npm` and `jq` resolved to
`/Users/_yolojail/.yolo/mise/shims/<name>` instead of into the nix profile
([`integration/macosuserfloor_test.go:68`](../../integration/macosuserfloor_test.go)).

The maintainer ruled that mise shims outrank the floor, as in the jail ([Decision Ledger](#decision-ledger)). `entrypoint.HomePathDirs` is shared between `BootPath` and `macosuser.SandboxPath` so that a mise shim outranks a `~/.local/bin` tool on every backend ([OQ-PD27](program-delivery.md#decision-ledger)). The rewritten test distinguishes the agent's full-PATH result from the floor's reach after removing sandbox-home directories. Mise winning the first lookup is by design; only the second lookup can fail the floor check. The rewritten test still needs its native-Mac run.

### 6.2 Another session's doorway was read as this session's survivor

`TestMacosUserOpensTheAWSDoorwayOutsideTheSandbox` reported *"the doorway survives the session
(pids 81026)"*, on both runs. A *doorway* is
[HS-D15](host-notch-services.md#HS-D15)'s word for the thin host-side adapter an agent's client talks
to, which checks the launch's caller token and forwards to a credential service.

**The teardown is right; the check was not.** On macos-user the workspace's keeper opens the
doorway, stops it when the last session leaves (`endKey`), and the last session's quit waits
for that keeper to be gone before the launch returns (`streamKeeperTeardown`), so the test's 10 s
wait is slack rather than the bound. What failed was the check's selection: it ran
`pgrep -f "internal daemon aws-credential-adapter --listen"` machine-wide, and every AWS doorway
has that argv whichever launch opened it, short of its port. A developer who runs Claude on
Bedrock through `yolo host -- claude`, or in another macos-user workspace, holds one for as long
as that session runs. CI's runner has none, which is why only a developer Mac failed. That
reading is inferred from the code: the run did not record the surviving process's port or start
time.

**Fixed in the test.** The doorway checks now judge only the pids the launch itself named on its
`Opened the … doorway (pack …, pid N)`, `Started the … service (pack …, pid N)` or
`… is back (pid N)` lines. A survivor of this launch's still fails, and another process with the
same argv is printed beside it as "not this launch's, so not judged". The codex doorway and the
two wire-bridge tests share that check. `doorwaysightings_test.go` runs that shape under
`-short` on Linux.

**The Mac re-runs** `-run '^TestMacosUserOpensThe(AWS|Codex)DoorwayOutsideTheSandbox$'`. A pass
closes this item. A failure now prints the survivor's full listing line, with its uid and
`--listen` port, which tells a leak in the teardown apart from another session's doorway.

### 6.3 The housekeeping test's slot was not due, because its stamp is the machine's

`TestMacosUserHousekeepingReapsRetiredLoopholeStateDuringTheSession` found the retired
generations all still present after the session, and no `<ws>/.yolo/housekeeping.log`.

**Shared state between tests is not the cause.** The suite's run store keeps `state/`, and so
the retired loophole-state archive, private to the run. Earlier tests' generations do land
there, which is why the list carried their timestamps. The test dates its seeds in 2099,
though, and the slot keeps by name order, so those generations can only sort below the seeds.

**The cause is shared state with the machine.** The run store links `build/` back to the
machine's store, and that is where every housekeeping class keeps its once-a-day debounce stamp
(`classDebounce`). So when one of the developer's own macos-user launches had run this class in
the last 24 hours, the test's slot was not due, and it reaped nothing and noted nothing. A pass
with every class debounced finishes in milliseconds, which is what the maintainer's own
macos-user launches show for `housekeeping.slot` (0.000–0.002 s). The failing run did not record
its slot's timing, so this reading is inferred from the code. The earlier suite tests did not
stamp the class: they run with the automatic reapers off, and this class checks that before its
stamp. A CI runner starts with no stamp, which is why only a developer Mac failed.

**The product is right and matches containers.** The container slot debounces the same archive
by a stamp in the same directory (`small-classes`), and the macos-user slot reaps and notes
whenever its class is due (`TestMacosUserLaunchRunsItsHousekeepingSlot`).

**Fixed in the test.** It takes the stamp over for its one launch (`ownHousekeepingStamp`) under
the cross-run lock, and puts it back afterwards: the machine's bytes if it had a stamp, nothing
if it had none. It also freshens the shared cache class's stamp for that launch, because the slot
runs that class first and its walk is bounded at a minute, longer than the test's session. The stamp's path is exported from the slot (`run.HousekeepingStampPath`), so the
test cannot drift onto a file the slot does not read. `TestMacosUserSlotReadsTheExportedLoopholeStateStamp`
and `TestOwnHousekeepingStampHandsTheMachinesStampBack` pin both halves under `-short` on Linux.
**Superseded on 2026-10-24.** A launch with the reapers on ran every due class against the machine's shared `build/`, so the test now runs on a private store instead (`isolateReaperStore`, `integration/reaperstore_test.go`): its `build/` holds stamps no other launch reads, every class but loophole-state is stamped as just run, and the harness refuses a reaper launch without such a store. `ownHousekeepingStamp` and its test remain for a test that must own a machine stamp.
Two things the cross-run lock does not keep out, because a developer's own launches do not take
it: a real pass holding the machine-wide pass lock at that moment, which makes this one skip, and
a real macos-user launch stamping the class in between. A failure now names both, and the stamps
it set aside.

**The Mac re-runs** `-run '^TestMacosUserHousekeepingReapsRetiredLoopholeStateDuringTheSession$'`.
A pass closes this item.

## 7. Measurements this run produced

**Five claims the corpus carries as `NOT MEASURED on a Mac` passed**, on the host of record:

Host of record: macOS 26.5, arm64, uid 501, `_yolojail` present, `/Users/Shared/yolo` present.
These local measurements were recorded on 2026-10-17 and 2026-10-18; `macos-user.yml` has not
confirmed them.

| Test | Claim it clears |
| :--- | :--- |
| `TestMacosUserSeatbeltSealedBuildProfileDeniesTheLoopbackAndTheNixDaemon` | [FP-D24](forked-programs-as-packs.md#FP-D24) |
| `TestMacosUserHostFloorBuildsAForkFixtureForTheMac` | [FP-D24](forked-programs-as-packs.md#FP-D24) |
| `TestMacosUserLaunchBuildsAndRunsAPatchedFork` | [`macosuserpatchedfork_test.go`](../../integration/macosuserpatchedfork_test.go), [§9](../reference/patched-forks.md) |
| `TestMacosUserLaunchRefusesAConflictingSeriesAndNamesTheHatches` | as above |
| `TestMacosUserCachedGoodServesTheDarwinGoodBuild` | as above |

**The cache-relocation measurement, with volumes attached** (APFS with ownership *disabled*,
and ExFAT; an ownership-*enabled* APFS volume needs `diskutil enableOwnership`, which needs
root, so that third case is still unmeasured):

- **Both delivered.** The sandbox wrote through the link and the file was at the target on both
  volumes ([CX-D5](../reference/context-mounts.md#CX-D5), [OQ-CX8](../reference/context-mounts.md#oq-cx8)).
- **The grant is per-target, not per-volume.** Reading a *sibling* directory on the same volume
  was refused under the profile on both — including on ExFAT, where the target
  *"was NOT granted (the volume took no access entries)"* and delivery still worked. The
  confinement holds where the filesystem cannot help it.

**Every vendor-install cell passes on darwin — all nine, the whole `install` matrix of
[`macos-user.yml`](../../.github/workflows/macos-user.yml).** The `via: npm` half had never run
on a Mac: `opencode` (29.4 s), `pi` (29.1 s), `omp` (19.1 s), `chrome-devtools` (6.4 s). The
`via: installer` half followed: `claude` (71.7 s), `copilot` (28.7 s), `codex` (30.3 s), `agy`
(22.5 s), `muse-code` (56.6 s). Each installs the vendor's **darwin** build through the generated
launcher into the sandbox account's home and probes `--version` — the platform no other CI job
installs.

> [!NOTE]
> **Three packs declare a `project_dirs` row the installed bundle does not witness**, logged by
> each cell without failing it: `opencode` (`.claude/skills`, `.agents/skills` — named only as
> home dirs), `copilot` 1.0.88 and `codex` 0.157.0 (`.agents/skills`). Informational in this
> test, and worth a look on its own: a declaration no bundle backs is a surface yolo renders for
> nobody.

**Q3, answered for the first time** ([`macos-user-build-step-threat-model.md`](macos-user-build-step-threat-model.md)):
`nixpkgs#jq^*` rebuilt with `--option sandbox true` **built under the sandbox** in 91 s;
nothing was refused by the sandbox policy; the output differs from the substituted one
(`may not be deterministic`), which that doc's own classification calls a reproducibility
difference and not a sandbox refusal. The sandbox setting was **honoured** rather than ignored,
because this host's `trusted-users` includes the invoking user — the condition CI cannot
guarantee. The floor build itself substituted every path and so remains VOID, exactly as on the
runner.

## 8. Open questions

None. All four were ruled on 2026-10-18 and are compacted into the ledger below.

### Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| OQ-DM1 | **The suite never needs passwordless root `sudo`** (option B, remove the need). The 2026-10-18 ruling rejects tests requesting root privileges not required by the product. Every privileged step goes through what yolo itself installs, the one run-as rule, built by `macosuser.RunAsSandboxArgv`, or is the invoking user's own; a capture plan's prepare steps run exactly as production runs them; the CA test uses a CA the System keychain already holds and never adds one; a guard test fails on a sudo the suite spells itself | 2026-10-18 | [§3.1](#31-class-a--six-tests-spent-a-privilege-yolo-macos-setup-does-not-install), [§3.3](#33-class-c--one-root-sudo-probe-that-read-as-a-missing-precondition), [§5](#5-the-shape) | Landed; native-Mac runtime unverified |
| OQ-DM2 | **An honest skip.** The host's unified log store being closed is the Mac's policy: probe it once (`log show` as the invoking user); the log-bridge test skips naming the policy; `SeatbeltProfileEnforcesItsRules` skips only its `macos-log-deny` control; both skips are counted in the suite's ledger, as the gate's are | 2026-10-18 | [§3.2](#32-class-b--two-tests-need-a-readable-unified-log-store), [§5](#5-the-shape) | Landed; native-Mac runtime unverified |
| OQ-DM3 | **Mise shims outrank the floor, for parity with the jail** (option B). The 2026-10-18 ruling preserves `SandboxPath`'s order; the floor test asks for the floor's reach past the sandbox home's shared directories, and blames path_helper or Homebrew only when the second lookup loses | 2026-10-18 | [§5](#5-the-shape), [§6.1](#61-the-nix-floor-loses-to-a-mise-shim) | Landed; native-Mac runtime unverified |
| OQ-DM4 | **A developer Mac clears a `NOT MEASURED on a Mac` line, with provenance** (option A). The 2026-10-18 ruling counts the local measurements with host and date provenance. The lines [§7](#7-measurements-this-run-produced) cleared stay, each recording the host and the date and saying `macos-user.yml` has not confirmed it | 2026-10-18 | [§5](#5-the-shape) | ✅ |

## 9. What this does not cover

- **The narrow-sudo cell.** CI narrows `/etc/sudoers.d` to the enrolled rule and re-runs three
  tests. That edits a machine's privilege configuration and needs root, so it was not run here;
  it is not part of this proposal.
- **Whether any class A or B test is itself correct.** This doc classifies why they could not
  run, not what they assert.
- **`internal/cli`'s runtime**, which belongs to [`test-suite-speed.md`](../plans/test-suite-speed.md).
  ⚠ It is worth naming here anyway, because it blunts [P4](#1-recommendation-and-principles):
  **`just done` cannot go green on this Mac at all.** Measured 2026-10-18 on a docs-only commit —
  the gate ran the full `just check-ci` (no verified ancestor for this toolchain) and
  `internal/cli` panicked at `test timed out after 20m0s`, `1201.046s`, with no `--- FAIL` of its
  own; every other package passed, including `internal/logcap` and `internal/packsrc`, both of
  which were red here two days earlier and have since been fixed. So a developer Mac can run the
  macos-user suite but not the landing gate, and the two problems are independent.

  **Verification status (2026-10-19):** the focused repairs did not change the `internal/cli` suite or its
  20-minute limit and established no localized speedup. The reported Mac run remains 1241.180 s;
  no Mac timing rerun is claimed.

## 10. Alternatives considered

| Alternative | Verdict |
| :--- | :--- |
| Grant the developer Mac passwordless root `sudo` so the suite passes | **Rejected.** It makes the measuring instrument less confined than the thing measured, and [P3](#1-recommendation-and-principles) is the opposite. |
| Delete the six root-`sudo` tests | **Rejected.** They assert real confinement; the privilege is in their fixtures, not their claims. |
| One `requireMacosUserPrivileged(t)` covering both host facts | **Rejected.** The run-as rule is the suite's only required privilege; the unified log is a countable host-policy skip, not an additional grant ([DM1 and DM2 in the Decision Ledger](#decision-ledger)). |
| A `paths:`-filtered PR trigger for `macos-user.yml` | **Out of scope here**, and the workflow already explains why it is not wired: nobody has measured what a first macos-user launch costs. |

## 11. Sequencing

The four rulings are compacted into the [Decision Ledger](#decision-ledger). The original Mac changes and the doorway, housekeeping and floor-test repairs are in the current source ancestry; portable shell-guard hardening followed in `cab34b522` and `201c4009e`. The `4e7b9b92b` commit itself changed only a documentation heading. The remaining work is to run the rewritten suite on a Mac with only the run-as rule and report any failures without granting root `sudo` or bypassing the host's log policy.

## 12. What done looks like

**Every condition below is MET, measured 2026-10-19 at `c3a38e634` on the host of record.**

- ✅ On a Mac with only the run-as grant, `-run '^TestMacosUser'` **exits 0**: 150 passed, 4
  skipped, 0 failed, 626.5 s, ledger `executed=100 skipped=14`. The ledger prints both
  log-policy skips, each naming the policy and quoting `log exited 77`.
- ✅ No test asked for root `sudo` — the privilege is gone from the suite, not declared
  ([OQ-DM1](#decision-ledger) option B holding on hardware). Every one of the twelve 2026-10-17
  failures is gone, and none was replaced by a privileged skip.
- ✅ `TestMacosUserSeatbeltProfileEnforcesItsRules` asserts its other rules on a host with no
  readable log: only the `macos_log_store_read_refused` subtest skips, which is
  [OQ-DM2](#decision-ledger) option A as built.
- ✅ The [§6](#6-two-defects-and-one-design-question-this-run-found) repairs have had their Mac
  re-runs: the doorway, housekeeping and floor tests all pass, so the three re-diagnoses hold at
  runtime and not only in review.
- ✅ **The narrow-sudo cell passes on this host** — `PasswordlessStartAndExit`,
  `TheGuestCannotWriteTheStagingRoot` and `TerminateEndsTheSessionThroughItsTeardown`, with
  `executed=3 skipped=0`. It needed no `sudoers` edit: the cell's own skip message is *"set
  `YOLO_TEST_MACOS_USER_NARROW_SUDO=1` once root sudo needs a password"*, and here it already
  does, so a developer Mac measures for free what CI must narrow `sudoers` to reach.
- ⬜ **Still owed, and neither is this host's to give:** a `macos-user.yml` run (CI has confirmed
  none of it), and an ownership-*enabled* APFS volume for the third relocation case, which needs
  `diskutil enableOwnership` and so root.
