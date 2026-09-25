---
title: "`yolo -- true` says it provisions, and leaves every agent CLI uninstalled"
date: 2026-09-22
status: draft
tags: [notches, provisioning, readiness, apply, launchers, program-delivery]
summary: "Each notch is supposed to have an ahead-of-time act that leaves the environment ready. The host has one — `yolo host apply --assert` probes dependencies, offers the install, and refuses rather than returning 0 on an unready home. The jail's equivalent is the launch, and the launch does not install the programs its selected packs declare: they arrive on first invocation. Until 2026-09-24 yolo's own message called `yolo -- true` the provisioning act; the message is corrected, and the readiness act it promised is still unbuilt."
vantage:
  status-chip: true
---

# `yolo -- true` says it provisions, and leaves every agent CLI uninstalled

**Status:** DESIGN, 2026-09-22; re-checked against the tree 2026-09-24. **The readiness act is not
built**, and all three questions are open. One piece shipped: alternative B, the message fix
([§5](#5-alternatives-with-verdicts)), landed 2026-09-24 (`a323fd9a`), so `yolo apply --at jail` no
longer says a launch provisions declared programs.

> **In short.** The jail notch has no act that makes the environment ready — it has a launch that
> makes it *startable*, and defers the rest to whoever happens to type the program's name.

**Why it matters.** Until 2026-09-24, `yolo apply` at the jail notch printed, in as many words:

> *"At the jail notch, provisioning happens as part of launch. Run `yolo -- <cmd>` to provision and
> enter, or `yolo -- true` to provision and exit."*

Run that command and every `via: npm` program a selected pack declares is still absent. The
launcher installs it on **first invocation** (the cold-home branch of `npmLauncherTemplate` in
[`shims.go`](../../internal/entrypoint/shims.go) — *"Cold home: the FIRST install is not a poll …
without this branch a fresh jail would simply have no agent CLI at all"*). So the provisioning
command provisions, exits 0, and leaves the thing you selected the pack for uninstalled.

The message now tells the truth — the jail arm of `applyMain` in
[`apply.go`](../../internal/cli/apply.go) says the declared programs are *"NOT installed by the
launch"* and that `yolo -- true` leaves them uninstalled — **but the gap it described is unchanged.**
The notch still has no act that leaves the environment ready; it now says so instead of claiming one.

**The shape.** Give the jail notch the readiness act the host already has, and let the existing
provisioning stage own it.

**Cost.** A jail that starts nothing pays an install it may not need — which is the objection that
deleted the 2026-09-03 eager shape, and the reason [§3](#3-what-ready-has-to-mean-here) scopes
readiness to the *declared set* rather than to a refresh.

**Start at [§2](#2-the-host-notch-already-has-this-and-says-why)** — the host's act is the model, and
it is already written down.

**Needs your ruling:** [OQ-JR1](#OQ-JR1), [OQ-JR2](#OQ-JR2), [OQ-JR3](#OQ-JR3).

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
| **Readiness** | Is the thing installed at all? | on first invocation | **nothing** |
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
where a declared-program install would sit. ⚠ It is a template with two holes, both recorded as open
questions rather than fixed: a failed `mise install` skips the bootstrap, so no floor is checked
([`OQ-AR6`](agent-program-runtimes.md#OQ-AR6)), and macos-user runs no stage at all unless
`mise_tools` asks for one ([`OQ-AR5`](agent-program-runtimes.md#OQ-AR5)).

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
- **Not a fix for `yolo apply --at jail` being a stub.** Whether that verb grows a real
  implementation is [OQ-JR3](#OQ-JR3); this doc is about what a launch leaves behind.

## 5. Alternatives, with verdicts

| Alternative | Verdict |
| :--- | :--- |
| **A. Leave it; the launcher installs on first use and that works** | **Rejected** — it works for a human who types the name, and fails every other consumer: a script, an IDE pointing at an absolute path, a `yolo -- true` that was told it provisions. The promise is the defect, not the laziness. |
| **B. Fix the message instead of the behaviour** — stop claiming `yolo -- true` provisions | **Rejected as the answer; SHIPPED 2026-09-24 as the cheap half** (`a323fd9a` — the message, the `apply` help line and the migration guide). It removed the false statement without giving the notch a readiness act, so `yolo apply --at jail` still has nothing to offer but a pointer. |
| **C. Re-adopt eager-at-boot wholesale** | **Rejected** — that is the 2026-09-03 shape, and its four costs (a jail-level fatal, a hatch, three ordering constraints, an update of every agent on every launch) were deleted for reasons that still hold. Install-only in the provisioning stage keeps none of them except one ordering constraint. |
| **D. A dedicated `yolo provision` verb** | **Runner-up.** The env-manager plan already calls a no-exec jail provision a follow-up, and a verb would give `apply --at jail` something real to delegate to. Rejected as the *primary* fix because it leaves an ordinary launch still unready — the gap is in the launch, and a new verb does not close it. Feeds [OQ-JR3](#OQ-JR3). |
| **E. Install at first invocation but block the launch until it finishes** | **Rejected as the worst of both** — it pays the cost at the least observable moment and still leaves a jail that started unready. |

## 6. Risks

| Risk | Mitigation |
| :--- | :--- |
| **R1.** A jail that never runs an agent pays its install — the objection that killed the eager shape. | Install-only and once per home, not per launch: the cost is paid on a cold home and never again, where the deleted shape paid a network round-trip every launch. [OQ-JR2](#OQ-JR2) is whether that is still too much. |
| **R2.** An offline cold home cannot install, and the launch has to decide. | The provisioning stage's five neighbours all degrade rather than refuse ([§3](#3-what-ready-has-to-mean-here)'s home). [OQ-JR1](#OQ-JR1) rules it; whichever way, it is stated rather than inherited. |
| **R3.** The change is made and the launcher's cold branch is left in place, so nothing proves the eager path ran. | The cold branch **must stay** — it is the fallback for an install that failed and for a program added to a running jail. So the done-condition cannot be "the branch is gone"; it is [§7](#7-what-done-looks-like)'s observable state of a fresh jail. |
| **R4.** `macos-user`'s provisioning stage is not the container's. | Since 2026-09-12 that backend has a confined stage of its own (`runProvisionStage`, between the darwin bootstrap and the agent — [the stage](../reference/macos-user-provisioning.md#the-stage)), so there is a place for readiness there; it runs a subset of the container's steps, under `env -i`. Named, not assumed away: [OQ-JR2](#OQ-JR2) decides whether readiness ships container-first with the gap reported on that backend. |

## 7. What done looks like

1. In a fresh workspace with a pack declaring a `via: npm` program, `yolo -- true` exits 0 and the
   program **is installed** — `command -v <bin>` resolves inside a subsequent jail with no further
   install.
2. A second `yolo -- true` on the same home installs nothing and says nothing.
3. A jail whose packs declare no program is unchanged — byte-identical launch output.
4. `yolo apply --at jail` no longer claims a provisioning that did not happen. ✅ **Met
   2026-09-24** by alternative B (`a323fd9a`), ahead of the rest; it must stay true once the launch
   does install, which means the message changes again when this ships.
5. The launcher's cold branch still works: delete the installed binary, invoke the name, and it
   returns.
6. `yolo check` installs nothing, on a cold home, twice.

## 8. Open Questions

1. 💬 <a id="OQ-JR1"></a>**[OQ-JR1](#OQ-JR1): does an install that cannot happen refuse the launch, or degrade?**
   Every neighbour in the provisioning stage degrades — `mise install` failing prints
   `PROVISIONING FAILED` and continues unless a human at a TTY says no; the bootstrap records that
   *"an offline boot fails here routinely and simply retries next launch"*. But a jail that started
   without the program its pack declared is the unready environment this doc exists to stop. Stakes:
   whether "ready" is a promise or a best effort at this notch — and note
   [`../reference/agent-program-runtimes.md`](../reference/agent-program-runtimes.md)'s
   [`OQ-AR3`](../reference/agent-program-runtimes.md#oq-ar3) already ruled **refuse** for
   the adjacent case of an unsatisfiable interpreter floor.

   <!-- vantage: oq id=OQ-JR1 leaning="Degrade on a network failure, refuse on a declared-program failure that leaves nothing runnable — the distinction being whether the jail can still do the job it was selected for. A blanket refusal makes an offline cold boot unusable; a blanket degrade re-creates the exact false success this doc opens with." -->

   _Leaning:_ **Degrade when the install merely failed, refuse when the jail has no runnable program
   for a selected pack.** A blanket refusal makes an offline cold boot unusable; a blanket degrade
   re-creates the false success this doc opens with.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 <a id="OQ-JR2"></a>**[OQ-JR2](#OQ-JR2): is readiness scoped to every declared program, or only to what the launch might run?**
   The selected pack set is statically knowable, but a config selecting seven program-declaring packs
   makes a cold home install seven CLIs to run one — the precise cost that deleted the 2026-09-03
   shape, minus the per-launch repetition. Narrowing it needs a signal core does not have, since
   there is no agent registry and argv is not sniffed. Stakes: the cold-home cost, and whether
   `macos-user` ships with the gap named rather than closed.

   <!-- vantage: oq id=OQ-JR2 leaning="Every declared program, once per home. Narrowing needs core to guess what the jail will run, which is the registry this project deleted; and the cost is bounded and one-time rather than per-launch, which is what made the earlier shape intolerable." -->

   _Leaning:_ **Every declared program, once per home.** Narrowing needs core to guess what the jail
   will run, which is the registry this project deleted — and the cost is one-time rather than
   per-launch, which is what made the earlier shape intolerable.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 <a id="OQ-JR3"></a>**[OQ-JR3](#OQ-JR3): does `yolo apply --at jail` become real, or keep pointing at the launch?**
   Today it is a stub that points at the launch. Its message was the false claim this doc opens
   with until 2026-09-24, when alternative B corrected it to say declared programs install on first
   use. Once the launch is a readiness act, the stub could say *provisioning happens as part of
   launch*, now accurately. Or the notch could grow the no-exec provision the env-manager plan
   already defers. Stakes: whether every notch ends up with the same verb, or the jail stays the one
   whose readiness act is spelled `yolo -- true`.

   <!-- vantage: oq id=OQ-JR3 leaning="Keep it a pointer, with the message corrected. Once the launch genuinely provisions, the stub's sentence is true and a second path to the same work is a second thing to keep correct — but revisit if a consumer appears that cannot spawn a container just to provision one." -->

   _Leaning:_ **Keep it a pointer, with the message corrected.** Once the launch genuinely
   provisions, that sentence is true, and a second path to the same work is a second thing to keep
   correct. Revisit if a consumer appears that cannot afford to start a container just to provision.

   **Answer:**
   > _(empty — fill in when decided)_

## 9. Decision Ledger

No rulings yet. Rows land here as [§8](#8-open-questions)'s questions are answered, and the ruling
moves into the body section it governs.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| — | — | — | — | — |
