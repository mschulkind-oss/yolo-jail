---
title: "`yolo -- true` now installs each selected pack's declared program before it exits"
date: 2026-09-22
status: accepted
stage: BUILT
next: "Record the native macOS CI result for the fixture-based macos-user readiness tests"
tags: [notches, provisioning, readiness, apply, launchers, program-delivery]
summary: "Each notch needs an ahead-of-time act that leaves the environment ready. `yolo -- true` now installs each selected pack's declared program through its own install-only launcher before the command, refuses a failed install by default, and keeps currency lazy. The same act runs on macos-user when the host cannot prove a declared program is present; its native fixture tests are in macOS CI, with an actual native run still pending."
vantage:
  status-chip: true
---

# `yolo -- true` now installs each selected pack's declared program before it exits

**Status:** 2026-09-22; re-checked against the tree 2026-09-24; questions triaged
2026-09-30; [OQ-JR1](#OQ-JR1) ruled 2026-10-05. **The readiness act is built on container and
macos-user backends**: the confined provisioning stage installs every selected pack's declared
program before the command, using its own install-only launcher. An install failure refuses,
offline included, unless `YOLO_ALLOW_MISSING_PROGRAMS=1` is set. The macos-user stage is admitted
when the host cannot prove a selected program already present; its fixture-based native tests are
in macOS CI. **Native Mac execution remains pending.** [OQ-JR1](#OQ-JR1), [JR-D2](#JR-D2), and
[JR-D3](#JR-D3) remain the governing decisions. [OQ-JR2](#OQ-JR2) is answered by
[HP-DIR2](host-tool-provisioning.md#HP-DIR2): every declared program, once per home. [OQ-JR3](#OQ-JR3)
is decided as an implementation choice, [JR-D1](#JR-D1), built with the act: `yolo apply --at jail`
runs the launch with no target. The [Decision Ledger](#9-decision-ledger)'s Built column says what
each row's code is. Alternative B, the message fix ([§5](#5-alternatives-with-verdicts)), shipped
first, 2026-09-24 (`a323fd9a`).

> **In short.** The container jail notch had no act that made the environment ready until
> 2026-10-05 — it had a launch that made it *startable*, and deferred the rest to whoever
> happened to type the program's name. The launch is that act now; JR-D2 carries it into
> macos-user when the host cannot prove the program already present.

**Historical gap, before 2026-10-05.** Until 2026-09-24, `yolo apply` at the jail notch printed, in as many words:

> *"At the jail notch, provisioning happens as part of launch. Run `yolo -- <cmd>` to provision and
> enter, or `yolo -- true` to provision and exit."*

Run that command and every `via: npm` program a selected pack declares is still absent. The
launcher installs it on **first invocation** (the cold-home branch of `npmLauncherTemplate` in
[`shims.go`](../../internal/entrypoint/shims.go) — *"Cold home: the FIRST install is not a poll …
without this branch a fresh jail would simply have no agent CLI at all"*). So the provisioning
command provisions, exits 0, and leaves the thing you selected the pack for uninstalled.

That message was corrected first. The container readiness act closed the described gap on
2026-10-05; JR-D2 carries the same act into macos-user on 2026-10-06. The historical failure is
kept here to explain why the readiness act exists.

**The shape.** Give the jail notch the readiness act the host already has, and let the existing
provisioning stage own it.

**Cost.** A jail that starts nothing pays an install it may not need — which is the objection that
deleted the 2026-09-03 eager shape, and the reason [§3](#3-what-ready-has-to-mean-here) scopes
readiness to the *declared set* rather than to a refresh.

**Start at [§2](#2-the-host-notch-already-has-this-and-says-why)** — the host's act is the model, and
it is already written down.

**Needs your ruling:** none. [OQ-JR1](#OQ-JR1) was ruled in review on 2026-10-05 ((C), with a
bypass), [OQ-JR2](#OQ-JR2) is answered by a standing ruling, and [OQ-JR3](#OQ-JR3) was decided as
an implementation choice ([JR-D1](#JR-D1)). One consequence of building the ruling reaches another
doc and is flagged for its owner: a fork the host built nothing for now stops a jail launch
([JR-D6](#JR-D6)).

**Reads with:** [`program-delivery.md`](program-delivery.md) (owns the launcher and
[`OQ-PD12a`](program-delivery.md#decision-ledger), the lazy ruling this must not contradict),
[`../reference/agent-program-runtimes.md`](../reference/agent-program-runtimes.md) (the same split, ruled for an interpreter),
[`../reference/host-apply-staleness.md`](../reference/host-apply-staleness.md) (the host's launch
gate, and what it deliberately does *not* answer).

---

## 1. Readiness and currency are different questions

This is the distinction the whole doc rests on, and conflating them is why the subject looks settled
when it is not.

| | Question | Answer today | Ruled by |
| :--- | :--- | :--- | :--- |
| **Readiness** | Is the thing installed at all? | before the command, by the launch's readiness act on container backends; on macos-user when the host cannot prove a selected program present | [OQ-JR1](#OQ-JR1), [OQ-JR2](#OQ-JR2), [JR-D2](#JR-D2) |
| **Currency** | Is the installed thing up to date? | at invocation, bounded by `UPDATE_INTERVAL` | [`OQ-PD12a`](program-delivery.md#decision-ledger) |

[`OQ-PD12a`](program-delivery.md#decision-ledger) is about **currency**. Its operative sentence is
*"are you saying every launch updates every agent?"*, and what it deleted was a network round-trip
per selected pack on every launch, forever. Nothing in it decides when a program is first installed —
readiness came along for the ride only because, for a `via: npm` program, **one mechanism does both
jobs**: the launcher's cold branch installs, and its warm branch refreshes.

> [!IMPORTANT]
> **Splitting the two is what makes this buildable without reopening a shipped ruling.** Install
> eagerly, refresh lazily. The same split was already ruled for an interpreter
> ([`../reference/agent-program-runtimes.md`](../reference/agent-program-runtimes.md),
> [`OQ-AR2`](../reference/agent-program-runtimes.md#oq-ar2)), and the tree already draws
> the line in a comment: *"Agent CLIs … are NOT installed here. Lazy-install launchers … install
> them on first use, keeping boot fast. … Only MCP/LSP tools that agents depend on are installed
> here"* (the bootstrap script `GenerateBootstrapScript` emits, in
> [`shell.go`](../../internal/entrypoint/shell.go)). ⚠ That comment's middle sentence — the
> launchers *"no longer update themselves on a timer"* — is stale against
> [`OQ-PD12`](program-delivery.md#decision-ledger): the launchers do refresh, at most once per
> `UPDATE_INTERVAL`. The install/refresh line it draws is still accurate.

## 2. The host notch already has this, and says why

`yolo host apply --assert` is a readiness act, and its gate states the principle this doc is
applying one notch down:

> *"an `--assert`'s promise is a ready environment, so a posture that returns 0 having left it
> unready has stated a result it did not achieve."*
> — the header of [`applyhostdepgate.go`](../../internal/cli/applyhostdepgate.go)

It runs a **pre-render dependency probe**, offers to run each pack's declared install command, and a
decline is **fatal at the prompt with nothing written** — no hatch flag. That is the standard the
jail notch fails: `yolo -- true` returns 0 having left the environment unready, which is exactly the
sentence above. The host verb has since taken the same rule one step further: since 2026-09-23 an
`--assert` with any configured pack it cannot resolve refuses the whole apply and writes nothing,
rather than rendering the packs it could (`01101ed1`).

> [!WARNING]
> **`host_apply_on_launch` is not the host's readiness act and must not be cited as one.** It is a
> **staleness** net — it exists because `yolo host apply` writes once and nothing looks again, and it
> says so itself: it *"does not answer 'does this host have the tools'"*
> ([`host-apply-staleness.md`](../reference/host-apply-staleness.md)). Readiness is `apply`'s job at
> that notch; drift is the wrapper gate's. Reading the wrapper gate as the readiness act is what
> makes the jail's gap look like a deliberate symmetry.

## 3. What "ready" has to mean here

**Ready = every program a SELECTED pack declares is installed and runnable, before the target
command runs.** Three properties follow, and each is chosen against a failure the 2026-09-03
reversal named:

- **Scoped to the declared set, not to an agent.** Core has no agent registry and cannot tell
  `yolo -- claude` from `yolo -- bash`, so the trigger is the selected pack set — which is
  statically knowable, and is the shape `installerBins` already implements for auto-capture
  ([`autocapture.go`](../../internal/cli/run/autocapture.go)).
- **Install-only, never refresh.** A warm program is already ready; touching it would re-incur
  exactly what [`OQ-PD12a`](program-delivery.md#decision-ledger) deleted.
- **A miss is work, a hit is nothing.** The existing forbidden-behaviour rule *"never rebuild on a
  timer or on every launch"* survives untouched, because a hit does no work.

**Where it goes: the provisioning stage.** `mise install --quiet` already runs there, before the
target command (`StepMiseInstall` in [`provision.go`](../../internal/provision/provision.go)), and it
is the only phase that is both after the launchers exist and after `GenerateCABundle` — the TLS
constraint that sank the 2026-09-03 boot step. ⚠ It may **not** go in launcher generation: that
runs host-side as a dry run under `yolo check` (the generator list in
[`check/entrypoint.go`](../../internal/cli/check/entrypoint.go)), so a generator that installed
would install on an observe verb.

**There is now a built precedent in this stage.** [`OQ-AR2`](../reference/agent-program-runtimes.md#oq-ar2)'s
eager interpreter install shipped 2026-09-22 in the bootstrap script, after `mise install`: it
installs `node@<floor>` for a declared floor nothing satisfies. Its refusal
([`OQ-AR3`](../reference/agent-program-runtimes.md#oq-ar3)) did NOT ship as a refusal that day: the
bootstrap printed the message and exited 1, and `provision.Script` degrades every non-vetoed failure
to status 0, so the target ran anyway. Since 2026-09-25 the bootstrap exits `provision.RefusedStatus`,
the one status `provision.Script` passes through without asking
([`provision.go`](../../internal/provision/provision.go)), and the launch stops before the target on
both backends. That is the same split this doc proposes — install eagerly, refresh lazily — applied
to a program's interpreter rather than to the program itself, so it is the nearest template for
where a declared-program install would sit. It had two holes, recorded as questions, decided
2026-09-30 and built the same day: a failed `mise install` skipped the bootstrap, so no floor was
checked ([`OQ-AR6`](../reference/agent-program-runtimes.md#oq-ar6), built as
[AR-L4](../reference/agent-program-runtimes.md#ar-l4): the bootstrap now runs whatever the steps before it
did), and macos-user ran no stage at all unless `mise_tools` asked for one
([`OQ-AR5`](../reference/agent-program-runtimes.md#oq-ar5), built as [AR-L3](../reference/agent-program-runtimes.md#ar-l3):
a declared floor the host cannot show met starts it too).

### When an install cannot happen

This is the background to [OQ-JR1](#OQ-JR1): why the question arises, the two standing rulings it
sits between, and its options in full.

Every neighbour in the provisioning stage degrades — `mise install` failing prints
`PROVISIONING FAILED` and continues unless a human at a TTY says no; the bootstrap records that
*"an offline boot fails here routinely and simply retries next launch"*. But a jail that started
without the program its pack declared is the unready environment this doc exists to stop.

*Restated 2026-09-30.* Two standing rulings pull opposite ways.
[`OQ-PD12`](program-delivery.md#decision-ledger) scoped an absent agent's failed install to the
command: *"Offline with the agent **absent** → that command fails, loudly, naming the network.
**No jail refuses to boot over this**"*
([`program-delivery.md`](program-delivery.md#what-evergreen-means-precisely)). That was ruled
for the launcher's install at first use, which is all there was. And
[`OQ-AR3`](../reference/agent-program-runtimes.md#oq-ar3) refuses a launch whose declared Node
floor nothing satisfies, an offline one included, because *"if a pack is selected, the jail must
be able to run what it declares."* The leaning was written two ways, and one of them cannot
hold: readiness installs only a program that is absent ([§3](#3-what-ready-has-to-mean-here)),
so every failed install leaves that pack with *"no runnable program"*, and a test on that alone
refuses every failure, offline ones included. The leaning's other wording splits by the
failure's cause instead, and that is the one restated as (A).

The options in full:

- **(A)** Split by cause. Offline, the launch starts, names each program it could not install,
  and the launcher's cold branch retries when the name is run. A package or installer that
  fails while the network is up refuses the launch, naming the pack, the program and the
  installer's error, with no escape hatch. yolo has to tell the two apart, by whether the
  registry or installer URL answered at all.
- **(B)** Always degrade. The launch starts and names each program it could not install,
  whatever the cause. It is [`OQ-PD12`](program-delivery.md#decision-ledger)'s rule carried to
  the launch.
- **(C)** Always refuse, as [`OQ-AR3`](../reference/agent-program-runtimes.md#oq-ar3) does for a
  floor. An offline cold home cannot start a jail while a program-declaring pack is selected.

**Ruled 2026-10-05: (C), with a bypass** ([OQ-JR1](#OQ-JR1)). A launch that cannot install a
declared program stops before your command runs, naming the pack, the program and the error,
offline included. `YOLO_ALLOW_MISSING_PROGRAMS=1` ([JR-D3](#JR-D3)) lets the jail start and list
what it could not install. [`OQ-PD12`](program-delivery.md#decision-ledger)'s rule still governs
the launcher's install at first use, which stays the fallback (R3 in [§6](#6-risks)).

*As built, 2026-10-05:* the act is the bootstrap's last step, after the Node floors
([`shell.go`](../../internal/entrypoint/shell.go)); its calls are rendered by
[`readiness.go`](../../internal/entrypoint/readiness.go), one per program, and each runs that
program's own launcher in install-only mode ([JR-D4](#JR-D4)). The refusal is the floor's exit
status, `provision.RefusedStatus`, which the stage passes through without asking, so a refused
launch ends with that status and the command never runs. The refusal quotes the lines of the
install's output that say what failed and why (npm's *"request to … failed, reason: getaddrinfo
EAI_AGAIN"*, an installer's *"download failed: <url>"*), and the full output is above it and in
`.yolo/startup.log`.

## 4. What this does not license

- **No refresh on the readiness path.** Currency stays at invocation. This doc installs what is
  missing and touches nothing that is present.
- **No agent knowledge in core.** The predicate is *a selected pack declares a program*, never *is
  this an agent*.
- **No new eager population.** MCP/LSP servers are already installed at boot and stay there;
  `pnpm`'s launcher stays lazy, because it serves the project rather than the environment yolo
  promised.
- **No silent cost.** A launch that installs says so, per the report tiers — a launch has no quiet
  mode, and this reports something yolo did.
- **Not a fix for `yolo apply --at jail` being a stub.** This doc is about what a launch leaves
  behind. The verb follows it: [OQ-JR3](#OQ-JR3) was decided as [JR-D1](#JR-D1), so once the
  launch is a readiness act, `yolo apply --at jail` runs that act with no target instead of
  pointing at it.

## 5. Alternatives, with verdicts

| Alternative | Verdict |
| :--- | :--- |
| **A. Leave it; the launcher installs on first use and that works** | **Rejected** — it works for a human who types the name, and fails every other consumer: a script, an IDE pointing at an absolute path, a `yolo -- true` that was told it provisions. The promise is the defect, not the laziness. |
| **B. Fix the message instead of the behaviour** — stop claiming `yolo -- true` provisions | **Rejected as the answer; SHIPPED 2026-09-24 as the cheap half** (`a323fd9a` — the message, the `apply` help line and the migration guide). It removed the false statement without giving the notch a readiness act, so `yolo apply --at jail` still has nothing to offer but a pointer. |
| **C. Re-adopt eager-at-boot wholesale** | **Rejected** — that is the 2026-09-03 shape, and its four costs (a jail-level fatal, a hatch, three ordering constraints, an update of every agent on every launch) were deleted for reasons that still hold. Install-only in the provisioning stage keeps none of them except one ordering constraint. |
| **D. A dedicated `yolo provision` verb** | **Runner-up.** The env-manager plan already calls a no-exec jail provision a follow-up, and a verb would give `apply --at jail` something real to delegate to. Rejected as the *primary* fix because it leaves an ordinary launch still unready — the gap is in the launch, and a new verb does not close it. Fed [OQ-JR3](#OQ-JR3), decided as [JR-D1](#JR-D1) with no new verb: `yolo apply --at jail` runs the launch's own act. |
| **E. Install at first invocation but block the launch until it finishes** | **Rejected as the worst of both** — it pays the cost at the least observable moment and still leaves a jail that started unready. |

## 6. Risks

| Risk | Mitigation |
| :--- | :--- |
| **R1.** A jail that never runs an agent pays its install — the objection that killed the eager shape. | Install-only and once per home, not per launch: the cost is paid on a cold home and never again, where the deleted shape paid a network round-trip every launch. [OQ-JR2](#OQ-JR2) asked whether that is still too much, and is answered: every declared program, because narrowing would mean reading the command line ([HP-DIR2](host-tool-provisioning.md#HP-DIR2)). |
| **R2.** An offline cold home cannot install, and the launch has to decide. | The provisioning stage's five neighbours all degrade rather than refuse ([§3](#3-what-ready-has-to-mean-here)'s home). [OQ-JR1](#OQ-JR1), ruled 2026-10-05, decides it the other way: the launch stops, naming the pack, the program and the error, and `YOLO_ALLOW_MISSING_PROGRAMS=1` ([JR-D3](#JR-D3)) starts it anyway. |
| **R3.** The change is made and the launcher's cold branch is left in place, so nothing proves the eager path ran. | The cold branch **must stay** — it is the fallback for an install that failed and for a program added to a running jail. So the done-condition cannot be "the branch is gone"; it is [§7](#7-what-done-looks-like)'s observable state of a fresh jail. |
| **R5.** *Found by the nested-jail check, 2026-10-05.* Offline, an npm program whose packages are in the machine's npm cache, from an install in another workspace, is not refused quickly: the launcher's `npm install --prefer-online` retries the registry for each package, about 70 s each, before it takes the cached copy, so the act can take many minutes and then succeed. With nothing cached it fails after one such wait and the launch is refused. | Not changed here. It is the launcher's install, and its first-use install has always done the same; a bound on the install, or `--prefer-offline` when the registry does not answer, is the launcher's to decide ([`program-delivery.md` §3.5](program-delivery.md#35-the-second-axis-who-the-dependency-serves-amendment-2026-09-03)). |
| **R4.** `macos-user`'s provisioning stage is not the container's. | Since 2026-09-12 it has a confined stage between bootstrap and agent ([the stage](../reference/macos-user-provisioning.md#the-stage)), under `env -i`. JR-D2 now runs the same generated readiness/bootstrap step there when the host cannot prove a declared program present. Fixture integration cases cover admission, refusal, bypass and the warm path in `.github/workflows/macos-user.yml`; native execution is pending the macOS CI run. |

## 7. What done looks like

1. In a fresh workspace with a pack declaring a `via: npm` program, `yolo -- true` exits 0 and the
   program **is installed** — `command -v <bin>` resolves inside a subsequent jail with no further
   install. ✅ **Met 2026-10-05**: against a fake npm
   (`TestReadinessInstallsADeclaredProgramBeforeTheCommandRuns`), for an installer program in a
   real container (`TestALaunchInstallsTheDeclaredProgramsBeforeItsCommand`, which checks the
   program is installed without running its name), and in a nested jail for `cowsay@1.6.0` from
   the npm registry, through `yolo apply` as well as `yolo -- true`.
2. A second `yolo -- true` on the same home installs nothing and says nothing. ✅ **Met
   2026-10-05** (`TestAWarmHomeInstallsNothingAndSaysNothing`, offline; the second launch of the
   container test; the nested jail's second launch).
3. A jail whose packs declare no program is unchanged — byte-identical launch output. ✅ **Met
   2026-10-05**: no call is rendered and the stage prints nothing for the act
   (`TestAJailWithNoProgramsHasNoReadinessAct`). The bootstrap script's own bytes grow by the
   act's functions, which print nothing when nothing calls them.
4. `yolo apply --at jail` no longer claims a provisioning that did not happen. ✅ **Met
   2026-09-24** by alternative B (`a323fd9a`), ahead of the rest, and **kept true 2026-10-05**: the
   verb now runs the launch ([JR-D1](#JR-D1)) and says what that launch does, an attach included.
5. The launcher's cold branch still works: delete the installed binary, invoke the name, and it
   returns. ✅ Unchanged: install-only is a branch of its own, taken only under
   `YOLO_INSTALL_ONLY=1`; the cold branch's tests pass, and in the nested jail a deleted `cowsay`
   came back on its next run.
6. `yolo check` installs nothing, on a cold home, twice. ✅ The act runs in the provisioning
   stage, which `yolo check` never runs; the generators it does run render the calls and install
   nothing.
7. Offline, on a cold home with a program-declaring pack selected, `yolo -- true` stops before
   `true` runs, naming the pack, the program, the error and `YOLO_ALLOW_MISSING_PROGRAMS=1`; with
   that variable set it starts, lists each program it could not install, and exits 0
   ([OQ-JR1](#OQ-JR1)). ✅ **Met 2026-10-05**, against a fake npm offline
   (`TestReadinessRefusesAProgramItCannotInstallOfflineIncluded`,
   `TestTheHatchStartsTheJailAndListsWhatItCouldNotInstall`), against an unreachable installer in
   a real container (`integration/readiness_test.go`), and in a nested jail launched inside an
   empty network namespace (`unshare -n`, loopback only), where npm's `getaddrinfo EAI_AGAIN`
   refused the launch with status 78 and the variable started it.
8. On macos-user, a stage starts when a selected launcher-backed program is absent, installs it
   through the same bootstrap readiness call before the target, and skips the stage when the host
   proves it present. ✅ Source and fixture behavior are pinned by
   `TestMacosUserReadinessAdmissionFailsTowardTheConfinedStage`,
   `TestBuildPlanStartsStageForAnAbsentSelectedProgram`, and the native fixture integration tests
   in `integration/macosuserprogramreadiness_test.go`. **Native execution is pending the macOS CI
   run; no Linux test proves Seatbelt execution.**

## 8. Open Questions

1. ✅ <a id="OQ-JR1"></a>**[OQ-JR1](#OQ-JR1): does an install that cannot happen refuse the launch, or degrade?**
   Every other step in the provisioning stage degrades, and two standing rulings pull opposite
   ways; the evidence and each option in full are in
   [When an install cannot happen](#when-an-install-cannot-happen). Stakes: whether "ready" is a
   promise or a best effort at this notch.

   - **(A)** Split by cause: offline, the launch starts and names what it could not install; a
     failure with the network up refuses it.
   - **(B)** Always degrade, whatever the cause.
   - **(C)** Always refuse, as [`OQ-AR3`](../reference/agent-program-runtimes.md#oq-ar3) does for a
     floor.

   _Leaning:_ **(A).** (C) makes an offline cold boot unusable. (B) re-creates the false success
   this doc opens with for a package that will never install.

   **Answer:**
   > **Ruled in review 2026-10-05: (C) with a bypass, against the leaning** (the maintainer's
   > answer: *"171 no. we can have a bypass var or whatever, but by default, no"*). When a launch
   > cannot install a program a selected pack declares before your command runs, the launch
   > stops, naming the pack, the program and the installer's error. Offline is no exception: an
   > offline cold home starts no jail while a pack that declares a program is selected. One
   > opt-in variable, `YOLO_ALLOW_MISSING_PROGRAMS=1`, forwarded from the host environment, lets
   > the jail start anyway and list each program it could not install; the launcher's cold branch
   > then retries when the name is run. The refusal names the variable, and the variable is
   > documented where the refusal is enforced, as every `YOLO_ALLOW_*` hatch is. Its name says
   > "programs", not "agents", because readiness keys on a declared program, never on whether it
   > is an agent ([§4](#4-what-this-does-not-license)); it follows the existing
   > `YOLO_ALLOW_MISSING_PROVIDERS` ([JR-D3](#JR-D3)). It is a hatch the standing rule allows:
   > an offline machine or a broken registry is the user's situation, not a yolo bug. Built
   > 2026-10-05 with the readiness act it governs
   > ([When an install cannot happen](#when-an-install-cannot-happen), *As built*).
   >
   > **The consistency question this raised, answered the same day.** A patched fork or patched
   > extension whose first build fails leaves nothing serving, the same unready state as a program
   > that could not be installed, and that launch was never stopped
   > ([PF §6.7](patched-forks.md#67-what-the-mode-never-does): *"Never refuses a launch over a
   > check, a replay or a build"*; [PPX-D12](patched-extensions.md#PPX-D12): *"the jail launch is
   > never refused"*). [OQ-PPX3](patched-extensions.md#OQ-PPX3), ruled in review the same day,
   > agrees with this ruling: a fresh launch refuses before booting when a patched build a
   > selected pack needs has none to deliver ([PPX-D40](patched-extensions.md#PPX-D40),
   > [PF-D77](patched-forks.md#PF-D77)). Whether that refusal honors this one's bypass is left to
   > its builder.

2. ✅ <a id="OQ-JR2"></a>**[OQ-JR2](#OQ-JR2): is readiness scoped to every declared program, or only to what the launch might run?**
   The selected pack set is statically knowable, but a config selecting seven program-declaring packs
   makes a cold home install seven CLIs to run one — the precise cost that deleted the 2026-09-03
   shape, minus the per-launch repetition. Narrowing it needs a signal core does not have, since
   there is no agent registry and argv is not sniffed. Stakes: the cold-home cost, and whether
   `macos-user` ships with the gap named rather than closed.

   _Leaning:_ **Every declared program, once per home.** Narrowing needs core to guess what the jail
   will run, which is the registry this project deleted — and the cost is one-time rather than
   per-launch, which is what made the earlier shape intolerable.

   <!-- vantage: question id=OQ-JR2 -->

   **Answer:**
   > Answered by [HP-DIR2](host-tool-provisioning.md#HP-DIR2) (2026-09-29): *"we construct an
   > environment. We do not sniff the command line"*, so readiness covers every program a selected
   > pack declares, once per home, and nothing narrows it to what the launch might run. Two rulings
   > already apply the same scope: the host floor holds the programs of the selected packs
   > ([OQ-HP1](host-tool-provisioning.md#OQ-HP1), 2026-09-29: *"if you include the claude pack, it
   > should also have the floor include claude on the host"*), and a fork's build is triggered by
   > the selected pack set ([OQ-FP4](forked-programs-as-packs.md#14-decision-ledger), 2026-09-22).
   > The stake's `macos-user` half is sequencing, decided as [JR-D2](#JR-D2).

3. ✅ <a id="OQ-JR3"></a>**[OQ-JR3](#OQ-JR3): does `yolo apply --at jail` become real, or keep pointing at the launch?**
   Today it is a stub that points at the launch. Its message was the false claim this doc opens
   with until 2026-09-24, when alternative B corrected it to say declared programs install on first
   use. Once the launch is a readiness act, the stub could say *provisioning happens as part of
   launch*, now accurately. Or the notch could grow the no-exec provision the env-manager plan
   already defers. Stakes: whether every notch ends up with the same verb, or the jail stays the one
   whose readiness act is spelled `yolo -- true`.

   _Leaning:_ **Keep it a pointer, with the message corrected.** Once the launch genuinely
   provisions, that sentence is true, and a second path to the same work is a second thing to keep
   correct. Revisit if a consumer appears that cannot afford to start a container just to provision.

   <!-- vantage: question id=OQ-JR3 -->

   **Answer:**
   > Decided as an implementation choice ([JR-D1](#JR-D1)), reversible: `yolo apply --at jail`
   > runs the launch's own readiness act with no target command, the same code path as
   > `yolo -- true`, so there is no second path to keep correct, and the verb means at the jail
   > what it means at the host.

## 9. Decision Ledger

Rows land here as [§8](#8-open-questions)'s questions are answered or decided, and the ruling moves
into the body section it governs. All three are settled; [OQ-JR1](#OQ-JR1) was ruled in review
on 2026-10-05. The rows from [JR-D4](#JR-D4) on are the implementation decisions building it made.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| OQ-JR2 | **Answered by [HP-DIR2](host-tool-provisioning.md#HP-DIR2), not ruled here: every program a selected pack declares, once per home.** The maintainer's words there: *"we construct an environment. We do not sniff the command line."* Narrowing readiness to what a launch might run needs exactly that signal, or the agent registry this project deleted. The host floor already takes the selected packs' programs ([OQ-HP1](host-tool-provisioning.md#OQ-HP1)), and a fork's build already keys on the selected pack set ([OQ-FP4](forked-programs-as-packs.md#14-decision-ledger)) | 2026-09-29, recorded 2026-09-30 | [§3](#3-what-ready-has-to-mean-here), [§6](#6-risks) R1 | 2026-10-05: the act's set is every selected pack's programs (`readyProgramsOf`, `internal/entrypoint/readiness.go`) |
| <a id="JR-D1"></a>JR-D1 | *Implementation decision, [OQ-JR3](#OQ-JR3).* **`yolo apply --at jail` runs the launch's readiness act with no target command.** It is the launch path, the same one `yolo -- true` takes, never a second provisioner. The leaning kept a pointer to avoid *"a second path to the same work"*, and delegating to the launch gives no second path. What it adds is the notch as an input: [NC-D1](../plans/notch-convergence.md#7-decision-ledger) (*"host is supposed to act like everywhere else"*) has `yolo apply` perform each notch's readiness act, as `yolo host apply --assert` does at the host. It is also the jail meaning [`yolo-as-environment-manager.md` §3.1](yolo-as-environment-manager.md#31-apply-is-the-verb-the-current-design-is-missing) gave the verb, *"builds the image, stages packs, renders config, and exits"*. It is built with the readiness act. Until then the corrected pointer (`a323fd9a`) stays, since a launch today leaves the declared programs uninstalled. On a jail that is already running, `yolo -- true` attaches and stages nothing, so the verb says so rather than claiming a provision. Reversible: the verb goes back to printing the pointer | 2026-09-30 | [§4](#4-what-this-does-not-license), [§5](#5-alternatives-with-verdicts) D | 2026-10-05: the jail arm of `applyMain` runs `applyJailLaunch`, which is `runRun` over `run -- true`, after one line saying what that launch does, an attach included; `--dry-run` prints the description and launches nothing (`internal/cli/apply.go`). Tests: `internal/cli/applyjailmessage_test.go` |
| <a id="JR-D2"></a>JR-D2 | *Implementation decision, [§6](#6-risks) R4 and [OQ-JR2](#OQ-JR2)'s `macos-user` stake.* **The host admits the confined stage if a selected launcher-backed program is absent or its presence is unknown; a proven hit needs no stage.** Once admitted, the generated bootstrap runs the same readiness act under the stage's `env -i`, using each program's own install-only launcher and the existing refusal plus `YOLO_ALLOW_MISSING_PROGRAMS=1` bypass. The pre-existing `mise_tools` and unmet Node-floor start rules remain. Reversible: the backend can ship with the container instead of after it | 2026-09-30 | [§6](#6-risks) R4 | 2026-10-06: `MissingProgramReadiness` (`internal/entrypoint/readiness.go`) checks the selected staged programs against their real install prefixes; `programReadinessStageFor` and `buildPlan` admit the existing stage (`internal/macosuser/orchestrator.go`); `DarwinEnvFrom` renders readiness calls into the bootstrap. Tests: `TestMacosUserReadinessAdmissionFailsTowardTheConfinedStage`, `TestBuildPlanStartsStageForAnAbsentSelectedProgram`, and fixture-only `TestMacosUserInstallsAnAbsentDeclaredProgramBeforeTheTarget` / `TestMacosUserProgramReadinessRefusesAndHonorsTheExistingBypass` (`integration/macosuserprogramreadiness_test.go`). Native Mac result pending. |
| OQ-JR1 | **Ruled in review, (C) with a bypass, against the leaning (A): a launch that cannot install a program a selected pack declares stops before your command runs, naming the pack, the program and the installer's error, offline included.** The maintainer's words: *"171 no. we can have a bypass var or whatever, but by default, no."* An opt-in variable ([JR-D3](#JR-D3)) lets the jail start and list what it could not install. This overrides, for the launch's own install only, [OQ-PD12](program-delivery.md#decision-ledger)'s *"No jail refuses to boot over this"*, which was ruled for the launcher's install at first use; that install is unchanged. [JR-D2](#JR-D2) stands: until macos-user's stage carries the step, that backend attempts no install, so it names each declared program it did not install. The patched mode, whose failed first build never stopped a launch ([PF §6.7](patched-forks.md#67-what-the-mode-never-does), [PPX-D12](patched-extensions.md#PPX-D12)), was ruled the same way the same day: a missing patched build refuses a fresh launch ([OQ-PPX3](patched-extensions.md#OQ-PPX3), [PPX-D40](patched-extensions.md#PPX-D40)) | 2026-10-05 | [When an install cannot happen](#when-an-install-cannot-happen), [§6](#6-risks) R2 | 2026-10-05, container backends: the bootstrap's readiness block and its refusal (`internal/entrypoint/shell.go`), its calls (`readinessChecks`, `internal/entrypoint/readiness.go`). Tests: `internal/entrypoint/readiness_test.go` (offline refused, the hatch starts and lists, an install proceeds, a warm home is silent), `integration/readiness_test.go`; a nested jail with no network |
| <a id="JR-D3"></a>JR-D3 | *Implementation decision, [OQ-JR1](#OQ-JR1)'s bypass, reversible.* **The variable is `YOLO_ALLOW_MISSING_PROGRAMS=1`: any non-empty value lets the launch continue, loudly, listing each program it could not install with its pack and error.** The refusal names it and it is documented where the refusal is enforced, as every `YOLO_ALLOW_*` hatch is, with its spelling in one `internal/paths` constant beside `YOLO_ALLOW_MISSING_PROVIDERS`, whose shape it follows. The readiness act runs in the provisioning stage, inside the jail, so the launcher forwards it from the host environment, as it forwards `YOLO_ALLOW_UNREACHABLE_SERVICES`. "Programs", not "agents": readiness keys on a declared program, never on whether it is an agent ([§4](#4-what-this-does-not-license)). No other `YOLO_ALLOW_*` implies it, and the Node floor's refusal ([OQ-AR3](../reference/agent-program-runtimes.md#oq-ar3)) is unchanged. Whether [PPX-D40](patched-extensions.md#PPX-D40)'s refusal of a missing patched build honors it too is that build's call, as PPX-D40 records | 2026-10-05 | [§8](#8-open-questions) | 2026-10-05: `paths.AllowMissingProgramsEnv`, forwarded by `programReadinessArgs` (`internal/cli/run/programreadiness.go`) only when set, and BAKED into the bootstrap by the boot (`allowMissingPrograms`, `readiness.go`) for the Node floor checks' reason, so the stage reads the launch's decision rather than its own environment. Tests: `TestTheHatchStartsTheJailAndListsWhatItCouldNotInstall`, `TestAssembleRunCmdForwardsTheReadinessDials`, `TestTheMissingProgramsHatchStartsTheJail` |
| <a id="JR-D4"></a>JR-D4 | *Implementation decision, [OQ-JR1](#OQ-JR1).* **The act installs each program by running that program's OWN LAUNCHER in install-only mode (`YOLO_INSTALL_ONLY=1`), never a second installer.** The mode existed for the installer launcher, for `yolo capture`; the npm and source launchers honor it too now, and in all three it means one thing: install the program if it is absent, refresh nothing that is present, never run it, and exit 0 only when there is a program to run. The installer launcher's install-only run used to fall through to its due update, which a capture's always-cold home never reached; it now skips the update, so the act is install-only as [§3](#3-what-ready-has-to-mean-here) requires. So each install keeps one implementation, as `yolo pack update` and `yolo capture` already call the launcher rather than reimplementing it. Reversible: the act can call a Go installer instead | 2026-10-05 | [§3](#3-what-ready-has-to-mean-here) | 2026-10-05: `npmLauncherTemplate`, `nativeLauncherTemplate` (`internal/entrypoint/shims.go`), `sourceLauncherTemplate` (`forklauncher.go`). Tests: the three `…InstallOnlyMode…` cases in `readiness_test.go` |
| <a id="JR-D5"></a>JR-D5 | *Implementation decision, [OQ-JR1](#OQ-JR1) and [OQ-JR2](#OQ-JR2).* **The act asks for exactly the programs the boot wrote a launcher for**, by the same predicates as `GenerateAgentLaunchers`, in its order. A name the image or a declared mise tool already provides is ready already. A program whose vendor publishes no build for this platform has no launcher and keeps the generator's line rather than refusing the launch, as `launchercollision.go` reasons for that fault class: no install can fix it, and the pack is more than its program. Reversible: the platform case can join the refusal | 2026-10-05 | [§3](#3-what-ready-has-to-mean-here) | 2026-10-05: `readyProgramsOf` (`internal/entrypoint/readiness.go`). Test: `TestReadinessAsksForExactlyTheLaunchersTheBootWrote` |
| <a id="JR-D6"></a>JR-D6 | *Implementation decision, [OQ-JR1](#OQ-JR1), with a consequence in another doc.* **A fork's program is a program the act installs, so a fork the host built nothing for stops a jail launch, naming the host's reason, and `YOLO_ALLOW_MISSING_PROGRAMS=1` starts it.** The source launcher's install-only run materializes the host's build, and with no build it exits naming why, which is the error the refusal quotes. ⚠ This reverses, at the jail notch, [`forked-programs-as-packs.md` §9](forked-programs-as-packs.md#9-failure-modes)'s *"the launch is not refused — a broken fork is one missing tool"* for its "Build fails" and "Source address unresolvable" rows; that doc's rows are its owner's to update. It agrees with [OQ-PPX3](patched-extensions.md#OQ-PPX3), ruled the same day for a patched build ([PPX-D40](patched-extensions.md#PPX-D40)): that refusal is the host's, before the boot, and is not built, while this one is the jail's, and is the one a patched fork with no build meets until it is. Reversible: a source program with no build can be left out of the refusal | 2026-10-05 | [When an install cannot happen](#when-an-install-cannot-happen) | 2026-10-05: `sourceLauncherTemplate`'s install-only branch. Test: `TestTheSourceLaunchersInstallOnlyModeMaterializesAndRunsNothing` |
| <a id="JR-D7"></a>JR-D7 | *Implementation decision.* **`YOLO_NO_PROGRAM_READINESS=1` turns the act off for a launch, and the integration suite sets it for every launch unless `YOLO_TEST_REAL_PACK_INSTALLS` is set.** Without it every suite launch selecting a shipped agent pack would install that vendor's current release before its command, the question [`agent-install-in-ci.md`](../reference/agent-install-in-ci.md#three-triggers-matched-to-three-causes) moved off the every-push gate, and a vendor's bad release would refuse those launches. The same reason `YOLO_NO_AUTO_CAPTURE` exists beside it. Not a second spelling of the hatch: it installs nothing ahead of time, and the stage says so, naming each program it left. The act's own integration tests turn it back on, over a fixture whose installer is a file in its pack. Reversible: the suite can drop it once its agent selections stop needing a vendor | 2026-10-05 | [§6](#6-risks) R1 | 2026-10-05: `paths.NoProgramReadinessEnv`, `readinessChecks`; `readinessEnvForSuite` and `withReadiness` (`integration/harness_test.go`). Test: `TestNoProgramReadinessInstallsNothingAndSaysWhatItLeft` |
| <a id="JR-D8"></a>JR-D8 | *Implementation decision.* **A capture or build jail runs no readiness act: the launcher sets `YOLO_NO_PROGRAM_READINESS` to `capture-jail` for it, whatever the host holds, and its stage says that jail's reason instead of naming the variable.** That jail's command is itself an install. `yolo capture` records what its installer adds to the home, so an act that installed the program first left nothing to record and the capture was refused; a fork's build jail runs the build that produces the program, so an act that asked for that program first refused the jail before the build ran. Patched-extension build jails take the same path. Reversible: the exemption can narrow if a capture ever needs the other programs ready | 2026-10-06 | [§3](#3-what-ready-has-to-mean-here) | 2026-10-06: `run.Options.NoProgramReadiness`, set by `runCaptureJail` (`internal/cli/capturehost.go`); `paths.NoProgramReadinessCaptureJail`. Tests: `TestACaptureJailTurnsTheReadinessActOff`, `TestACaptureJailsReadinessIsOffAndSaysWhy`, and the call-site checks in `capturehost_test.go`, `forkbuild_test.go` and `patchedtree_test.go` |
