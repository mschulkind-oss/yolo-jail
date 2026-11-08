---
status: current
stage: CURRENT
verified: 2026-10-16
verified_commit: 8e65f344
covers:
  - internal/entrypoint/readiness.go
  - internal/entrypoint/shell.go
  - internal/entrypoint/shims.go
  - internal/entrypoint/forklauncher.go
  - internal/cli/run/programreadiness.go
  - internal/cli/apply.go
  - internal/cli/capturehost.go
  - internal/macosuser/orchestrator.go
tags: [notches, provisioning, readiness, apply, launchers, program-delivery]
summary: "How a jail launch leaves the environment ready: the provisioning stage installs every program a selected pack declares, through that program's own launcher in install-only mode, before the command runs. A program it cannot install stops the launch, offline included, unless YOLO_ALLOW_MISSING_PROGRAMS=1. Currency stays lazy, at invocation. macos-user runs the same act when its host cannot prove a declared program present."
---

# Program readiness — a launch installs every declared program before the command

**Status:** Verified 2026-10-16 against `8e65f344`. Graduated from the design doc
`docs/reference/program-readiness.md`, which git history keeps. MEASURED on container backends: unit
tests against a fake npm, an integration test in a real container, and a nested jail installing a
real npm package, online and in an empty network namespace. UNMEASURED: native macos-user
execution. Its fixture-based integration tests are in the macOS CI workflow, but no recorded native
run has passed them, and no Linux test proves Seatbelt execution
([macos-user](#on-macos-user)).

At the jail notch, a launch is a **readiness act**: before your command runs, the provisioning stage
installs every program a selected pack declares that is not already installed. `yolo -- true` is
therefore enough to leave a fresh jail with its agent CLIs in place, and `yolo apply --at jail`
runs exactly that launch ([JR-D1](#JR-D1)). A program the stage cannot install **stops the launch**,
naming the pack, the program and the installer's error, offline included ([OQ-JR1](#OQ-JR1)).
Keeping an installed program up to date stays where it was: the launcher refreshes it at invocation.

| Component | Lives in |
| :--- | :--- |
| Which programs, and the rendered calls | `internal/entrypoint` (`readyProgramsOf`, `readinessChecks`, `allowMissingPrograms`), in `readiness.go` |
| The act's place in the bootstrap, and its refusal | the bootstrap script `GenerateBootstrapScript` emits (`internal/entrypoint/shell.go`); `provision.RefusedStatus` |
| Each program's install-only mode | `npmLauncherTemplate`, `nativeLauncherTemplate` (`shims.go`), `sourceLauncherTemplate` (`forklauncher.go`) |
| The host's forwarding of the dials | `programReadinessArgs` (`internal/cli/run`) |
| `yolo apply --at jail` | `applyJailLaunch` (`internal/cli/apply.go`) |
| Capture and build jails' exemption | `run.Options.NoProgramReadiness`, set by `runCaptureJail` (`internal/cli/capturehost.go`) |
| macos-user's stage admission | `MissingProgramReadiness` (`internal/entrypoint`), `programReadinessStageFor` and `buildPlan` (`internal/macosuser`) |

**Reads with:** [`program-delivery.md`](../design/program-delivery.md) (owns the launcher and
[`OQ-PD12a`](../design/program-delivery.md#decision-ledger), the lazy currency ruling this must not
contradict); [`agent-program-runtimes.md`](agent-program-runtimes.md) (the same split, ruled for an
interpreter); [`host-apply-staleness.md`](host-apply-staleness.md) (the host's launch gate, and what
it deliberately does not answer).

---

## Readiness and currency are different questions

This is the distinction the whole system rests on.

| | Question | Answered | Ruled by |
| :--- | :--- | :--- | :--- |
| **Readiness** | Is the program installed at all? | Before the command, by the launch's readiness act | [OQ-JR1](#OQ-JR1), [OQ-JR2](#OQ-JR2), [JR-D2](#JR-D2) |
| **Currency** | Is the installed program up to date? | At invocation, by the launcher, at most once per update interval | [`OQ-PD12a`](../design/program-delivery.md#decision-ledger) |

[`OQ-PD12a`](../design/program-delivery.md#decision-ledger) is about **currency**: what it deleted was a network round-trip per selected pack on
every launch. Nothing in it decides when a program is first installed. Readiness came along with it
only because, for a launcher-installed program, one mechanism does both jobs: the launcher's cold
branch installs, and its warm branch refreshes.

> [!IMPORTANT]
> **Install eagerly, refresh lazily.** Splitting the two is what lets a launch install without
> reopening the currency ruling. The same split governs an interpreter
> ([`OQ-AR2`](agent-program-runtimes.md#oq-ar2)): the bootstrap installs a declared Node floor
> nothing satisfies, and refuses a launch it cannot satisfy
> ([`OQ-AR3`](agent-program-runtimes.md#oq-ar3)).

**A failed update stops the command, not the launch.** The readiness act installs only what is
absent and never refreshes, so the update refusal of
[PD-NF1](../design/program-delivery.md#PD-NF1) never fires inside it, and `YOLO_USE_INSTALLED`
does not apply to it. That refusal belongs to the invocation: the first run of a program after a
launch may stop with it when a newer version failed to install, naming
`YOLO_USE_INSTALLED=<bin> <bin>` to run the installed version
([§3.6](../design/program-delivery.md#36-no-silent-fallback-to-an-older-version-pd-nf1)). Under
`--update` the explicit refresh still fails loudly, and neither `YOLO_ALLOW_MISSING_PROGRAMS` nor
`YOLO_USE_INSTALLED` bypasses it.

## What "ready" means

**Ready = every program a selected pack declares is installed and runnable, before the target
command runs.** Three properties follow:

- **Scoped to the declared set, not to an agent.** Core has no agent registry and cannot tell
  `yolo -- claude` from `yolo -- bash`, so the trigger is the selected pack set, which is statically
  knowable. Every selected pack's programs, once per home, never narrowed to what the command might
  run ([OQ-JR2](#OQ-JR2)).
- **Install-only, never refresh.** A program that is present is already ready; touching it would
  re-incur exactly what [`OQ-PD12a`](../design/program-delivery.md#decision-ledger) deleted.
- **A miss is work, a hit is nothing.** The rule "never rebuild on a timer or on every launch"
  survives, because a hit does no work: a second `yolo -- true` on the same home installs nothing
  and says nothing.

**Exactly the programs the boot wrote a launcher for** ([JR-D5](#JR-D5)), by the same predicates as
`GenerateAgentLaunchers` and in its order. A name the image or a declared mise tool already
provides is ready already. A program whose vendor publishes no build for this platform has no
launcher and keeps the generator's own line rather than refusing the launch: no install can fix
it, and the pack is more than its program. `TestReadinessAsksForExactlyTheLaunchersTheBootWrote`
holds the two lists together. The same selection (`SelectedProgramPreparations`) also feeds
`yolo pack update`'s explicit refresh, and its host-side form (`SelectedHostProgramPreparations`)
prepares programs before a jail exists, judging image collisions and vendor platforms against the
jail rather than the host.

## How the act runs

**Where: the provisioning stage, last in the bootstrap**, after `mise install` and the Node floors.
It is the one phase that runs after the launchers exist and after the CA bundle (`GenerateCABundle`),
and the one place a launch already installs over the network.

> [!WARNING]
> **The act must not go in launcher generation.** `yolo check` runs the generators host-side as a
> dry run, so a generator that installed would install on an observe verb. `yolo check` installs
> nothing, on a cold home, however often it runs.

**How: each program's own launcher, in install-only mode** ([JR-D4](#JR-D4)). The bootstrap runs
each launcher in `~/.yolo/bin/launch/` with `YOLO_INSTALL_ONLY=1`. In all three launcher kinds —
npm, installer and source — that mode means one thing: install the program if it is absent, refresh
nothing that is present, never run it, and exit 0 only when there is a program to run. So each
install keeps one implementation, the launcher's, and the act is a caller of it, as
`yolo pack update` and `yolo capture` are.

**The launcher's cold branch stays.** It is the fallback for an install the act skipped or that
failed under the bypass, and for a program added to a running jail: delete the installed binary,
invoke the name, and it installs again.

**Reporting.** A launch that installs says so: a launch has no quiet mode, and this reports
something yolo did. A jail whose packs declare no program renders no call and prints nothing for the
act.

## When an install cannot happen

**The launch stops** ([OQ-JR1](#OQ-JR1)). A launch that cannot install a declared program refuses
before your command runs, naming the pack, the program and the installer's error, offline included.
The refusal is `provision.RefusedStatus`, the one status the provisioning stage passes through
without asking, so a refused launch ends with that status and the command never runs. The refusal
quotes the lines of the install's output that say what failed and why (npm's
*"request to … failed, reason: getaddrinfo EAI_AGAIN"*, an installer's *"download failed: <url>"*);
the full output is above it and in `.yolo/logs/startup.log`.

**The bypass is `YOLO_ALLOW_MISSING_PROGRAMS=1`** ([JR-D3](#JR-D3)). Any non-empty value lets the
launch continue, loudly, listing each program it could not install with its pack and error; each
then installs on first use, as before. The refusal names the variable. The launcher forwards it from
the host environment into the jail, because the act runs inside it, and the boot bakes it into the
bootstrap, so the stage reads the launch's decision rather than its own environment. No other
`YOLO_ALLOW_*` implies it, and the Node floor's refusal is unchanged. For a patch series that does
not apply, the variable waives the failure only when nothing of the program would run
([PF-D83](patched-forks.md#PF-D83)).

**A fork with no host build stops a launch too** ([JR-D6](#JR-D6)). A fork's program is a program the
act installs; the source launcher's install-only run materializes the host's build, and with no build
it exits naming why, which is the error the refusal quotes. A program a pack builds from its own tree
(`via: "pack"`) joins the act the same way, its quoted error naming the build's log and
`yolo pack build <pack>` ([`pack-buildable-programs.md` §8](../design/pack-buildable-programs.md#8-failure-and-the-next-step)).
A program a dev override (`programs.overrides`) builds from a checkout joins it as the same
pack-tree build, its refusal naming the checkout as the source
([§5](../design/pack-buildable-programs.md#5-the-dev-override)).

> [!NOTE]
> **The launch's own install is the only one that refuses.** [`OQ-PD12`](../design/program-delivery.md#decision-ledger)'s *"No jail refuses to boot
> over this"* still governs the launcher's install at first use, which is unchanged: an absent
> program whose first-use install fails makes that command fail, loudly, naming the network
> ([`program-delivery.md`](../design/program-delivery.md#what-evergreen-means-precisely)).

> [!WARNING]
> **An offline npm install can be slow before it succeeds.** Offline, an npm program whose packages
> are in the machine's npm cache from another workspace is not refused quickly: the launcher's
> `npm install --prefer-online` retries the registry for each package, about 70 s each, before it
> takes the cached copy, so the act can take many minutes and then succeed. With nothing cached it
> fails after one such wait and the launch is refused. A bound on the install, or `--prefer-offline`
> when the registry does not answer, is the launcher's to decide
> ([`program-delivery.md` §3.5](../design/program-delivery.md#35-the-second-axis-who-the-dependency-serves-amendment-2026-09-03)).

## Turning the act off

- **`YOLO_NO_PROGRAM_READINESS=1`** turns the act off for one launch, and the integration suite sets
  it for every launch unless `YOLO_TEST_REAL_PACK_INSTALLS` is set ([JR-D7](#JR-D7)). Without it
  every suite launch selecting a shipped agent pack would install that vendor's current release
  before its command, and a vendor's bad release would refuse those launches
  ([`agent-install-in-ci.md`](agent-install-in-ci.md#three-triggers-matched-to-three-causes)). It is
  not a second spelling of the bypass: it installs nothing ahead of time, and the stage says so,
  naming each program it left. The act's own integration tests turn it back on, over a fixture
  whose installer is a file in its pack.
- **A capture or build jail runs no act** ([JR-D8](#JR-D8)). The launcher sets
  `YOLO_NO_PROGRAM_READINESS` to `capture-jail` for it, whatever the host holds, and its stage gives
  that jail's reason instead of naming the variable. That jail's command is itself an install:
  `yolo capture` records what its installer adds to the home, and a fork's or patched extension's
  build jail produces the program the act would have asked for.

## `yolo apply --at jail`

The jail arm of `yolo apply` runs the launch's readiness act with no target command
([JR-D1](#JR-D1)): `run --at jail -- true`, after one line saying what that launch does. It is the
same launch path `yolo -- true` takes, never a second provisioner, so each notch's `apply` performs
that notch's readiness act, as `yolo host apply --assert` does at the host. On a jail that is
already running, `yolo -- true` attaches and stages nothing, so the verb says so rather than claiming
a provision. `--dry-run` prints the description and launches nothing.

## The host notch's act

`yolo host apply --assert` is the host's readiness act, and its gate states the principle:

> *"an `--assert`'s promise is a ready environment, so a posture that returns 0 having left it
> unready has stated a result it did not achieve."*
> — the header of [`applyhostdepgate.go`](../../internal/cli/applyhostdepgate.go)

It runs a pre-render dependency probe, offers to run each pack's declared install command, and a
decline is fatal at the prompt with nothing written. An `--assert` with any configured pack it
cannot resolve refuses the whole apply and writes nothing.

> [!WARNING]
> **`host_apply_on_launch` is not the host's readiness act and must not be cited as one.** It is a
> staleness net: it exists because `yolo host apply` writes once and nothing looks again, and it
> *"does not answer 'does this host have the tools'"*
> ([`host-apply-staleness.md`](host-apply-staleness.md)). Readiness is `apply`'s job at that notch;
> drift is the wrapper gate's.

## On macos-user

macos-user's provisioning stage is not the container's: it is a confined stage between bootstrap
and agent, under `env -i` ([the stage](macos-user-provisioning.md#the-stage)). The host admits that
stage if a selected launcher-backed program is absent or its presence is unknown; a proven hit needs
no stage ([JR-D2](#JR-D2)). `MissingProgramReadiness` checks the selected staged programs against
their real install prefixes. Once admitted, the generated bootstrap runs the same readiness act,
with the same refusal and bypass. The pre-existing stage triggers — `mise_tools`, and an unmet Node
floor — remain.

Pinned on Linux by `TestMacosUserReadinessAdmissionFailsTowardTheConfinedStage` and
`TestBuildPlanStartsStageForAnAbsentSelectedProgram`; the fixture integration tests
(`integration/macosuserprogramreadiness_test.go`: an absent program installed before the target,
the refusal and the bypass) run in `.github/workflows/macos-user.yml`. **Still owed:** a recorded
green native run of those tests.

## What this does not license

- **No refresh on the readiness path.** Currency stays at invocation; the act touches nothing that
  is present.
- **No agent knowledge in core.** The predicate is *a selected pack declares a program*, never *is
  this an agent*.
- **No new eager population.** MCP and LSP servers are installed at boot as before; `pnpm`'s
  launcher stays lazy, because it serves the project rather than the environment yolo promised.

> [!WARNING]
> **Do not re-adopt eager-at-boot wholesale, and do not answer the gap by blocking the launch at
> first invocation.** The deleted eager shape updated every agent on every launch, with a jail-level
> fatal, a hatch and three ordering constraints; the install-only act keeps none of those except one
> ordering constraint. Blocking at first invocation pays the cost at the least observable moment and
> still leaves a jail that started unready.

## Testing

- **Unit, against a fake npm** (`internal/entrypoint/readiness_test.go`): an install proceeds before
  the command (`TestReadinessInstallsADeclaredProgramBeforeTheCommandRuns`); a warm home installs
  nothing and says nothing; a jail with no programs has no act; offline, the launch is refused, and
  the bypass starts it and lists what it could not install; each launcher kind's install-only mode;
  the act asks for exactly the launchers the boot wrote; the source launcher's install-only mode
  materializes and runs nothing.
- **In a real container** (`integration/readiness_test.go`): an installer program installed before
  the command, checked without running its name, and a second launch that installs nothing; an
  unreachable installer refused, and the bypass.
- **The host side**: `TestAssembleRunCmdForwardsTheReadinessDials` (`internal/cli/run`);
  `TestApplyAtJailRunsTheLaunchsReadinessAct` (`internal/cli`); the capture-jail exemption in
  `capturehost_test.go`, `forkbuild_test.go` and `patchedtree_test.go`.

## Why it's this way

Rulings and decisions a maintainer would otherwise undo, keeping their original ids, which code
comments and sibling docs cite.

| ID | Ruling |
| :--- | :--- |
| <a id="OQ-JR1"></a>[`OQ-JR1`](#OQ-JR1) | **Ruled in review 2026-10-05: always refuse, with a bypass.** A launch that cannot install a program a selected pack declares stops before your command runs, naming the pack, the program and the installer's error, offline included. The maintainer: *"we can have a bypass var or whatever, but by default, no."* Chosen over splitting by cause (refuse online, degrade offline) and over always degrading. It overrides [`OQ-PD12`](../design/program-delivery.md#decision-ledger)'s *"No jail refuses to boot over this"* for the launch's own install only. A missing patched build was ruled the same way the same day ([OQ-PPX3](pi-extensions.md#OQ-PPX3), [PPX-D40](pi-extensions.md#PPX-D40)) |
| <a id="OQ-JR2"></a>[`OQ-JR2`](#OQ-JR2) | **Answered by [HP-DIR2](host-agent-environment.md#HP-DIR2): every program a selected pack declares, once per home.** *"we construct an environment. We do not sniff the command line."* Narrowing readiness to what a launch might run needs exactly that signal, or the agent registry this project deleted |
| <a id="OQ-JR3"></a>[`OQ-JR3`](#OQ-JR3) | **What `yolo apply --at jail` does**: decided as an implementation choice, [JR-D1](#JR-D1) |
| <a id="JR-D1"></a>JR-D1 | *Implementation decision; reversible.* **`yolo apply --at jail` runs the launch's readiness act with no target command**, the same path `yolo -- true` takes. Delegating to the launch adds no second path to the same work, and makes `yolo apply` perform each notch's readiness act ([NC-D1](../plans/notch-convergence.md#7-decision-ledger): *"host is supposed to act like everywhere else"*), the meaning [`yolo-as-environment-manager.md` §3.1](what-yolo-is.md) gave the verb (`applyjailmessage_test.go`) |
| <a id="JR-D2"></a>JR-D2 | *Implementation decision; reversible.* **On macos-user, the host admits the confined stage if a selected launcher-backed program is absent or its presence is unknown; a proven hit needs no stage.** Once admitted, the bootstrap runs the same act with the same refusal and bypass. Native Mac result pending |
| <a id="JR-D3"></a>JR-D3 | *Implementation decision; reversible.* **The bypass is `YOLO_ALLOW_MISSING_PROGRAMS`: any non-empty value lets the launch continue, loudly, listing each program it could not install.** Its spelling is one `internal/paths` constant (`paths.AllowMissingProgramsEnv`) beside `YOLO_ALLOW_MISSING_PROVIDERS`, whose shape it follows; the launcher forwards it, as it forwards `YOLO_ALLOW_UNREACHABLE_SERVICES`. "Programs", not "agents": readiness keys on a declared program. Amended by [PF-D83](patched-forks.md#PF-D83) for a patch series that does not apply |
| <a id="JR-D4"></a>JR-D4 | *Implementation decision; reversible.* **The act installs each program by running that program's own launcher in install-only mode, never a second installer.** The installer launcher's install-only run used to fall through to its due update; it skips it, so the act is install-only |
| <a id="JR-D5"></a>JR-D5 | *Implementation decision; reversible.* **The act asks for exactly the programs the boot wrote a launcher for**; a program with no build for this platform keeps the generator's line rather than refusing the launch |
| <a id="JR-D6"></a>JR-D6 | *Implementation decision; reversible.* **A fork the host built nothing for stops a jail launch, naming the host's reason, and the bypass starts it.** This reverses, at the jail notch, [`forked-programs-as-packs.md` §9](../design/forked-programs-as-packs.md#9-failure-modes)'s *"the launch is not refused — a broken fork is one missing tool"* for its "Build fails" and "Source address unresolvable" rows |
| <a id="JR-D7"></a>JR-D7 | *Implementation decision; reversible.* **`YOLO_NO_PROGRAM_READINESS=1` turns the act off for a launch, and the integration suite sets it unless `YOLO_TEST_REAL_PACK_INSTALLS` is set**, for the same reason `YOLO_NO_AUTO_CAPTURE` exists beside it (`readinessEnvForSuite` in `integration/harness_test.go`) |
| <a id="JR-D8"></a>JR-D8 | *Implementation decision; reversible.* **A capture or build jail runs no readiness act**, because its command is itself the install the act would have done first |

## Current values

Verified at `8e65f344`. The prose above explains what each of these is for; this table is the only
place the exact values are stated.

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| Bypass | `YOLO_ALLOW_MISSING_PROGRAMS` (any non-empty value) | `paths.AllowMissingProgramsEnv` |
| Turn the act off | `YOLO_NO_PROGRAM_READINESS` | `paths.NoProgramReadinessEnv` |
| Capture-jail value | `capture-jail` | `paths.NoProgramReadinessCaptureJail` |
| Install-only launcher mode | `YOLO_INSTALL_ONLY=1` | `entrypoint.InstallOnlyEnv` |
| Explicit refresh launcher mode (`yolo pack update`) | `YOLO_PACK_UPDATE` | `entrypoint.FreshRefreshEnv` |
| Refusal status | 78 | `provision.RefusedStatus` |
| Integration suite opt-in to real installs | `YOLO_TEST_REAL_PACK_INSTALLS` | `integration/harness_test.go` |
| Install log | `.yolo/logs/startup.log` | the provisioning stage |
