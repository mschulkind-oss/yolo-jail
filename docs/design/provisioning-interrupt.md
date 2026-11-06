---
title: "A Ctrl-C during provisioning is reported as an unwritable log"
date: 2026-11-06
status: in-review
stage: DESIGN
next: "Rule OQ-PLW1; the repair in internal/provision and internal/macosuser waits on it"
tags: [provisioning, diagnostics, happy-path, interrupts, logging]
summary: "When the provisioning stage is interrupted, the log writer is signalled with it, and the stage reports that the log could not be written and names permissions and disk space, neither of which was true when measured. One ruling decides how the stage tells an interrupted writer from a failed one."
---

# A Ctrl-C during provisioning is reported as an unwritable log

**Status:** 2026-11-06. Nothing built. Measured on a Mac on 2026-10-19
([the lab report's §2](../lab-reports/2026-10-19-launch-diagnostics-on-a-mac.md#2-an-interrupted-provisioning-stage-blames-permissions-and-disk));
the code read at this document's date still behaves as measured.

> **In short.** A log writer that stopped because the user interrupted the launch has not shown that
> the log is unwritable, so the stage should not say it is, and should not send the user to fix
> permissions or free disk space.

**Why it matters.** Each stop must name the easiest next step that keeps the user's trust
([`happy-path-principle.md`](../reference/happy-path-principle.md)). This one names two wrong steps,
and the same run then wrote its failure line into the log it had just called unwritable.

**Needs your ruling:** [OQ-PLW1](#OQ-PLW1).

**Reads with:** [the 2026-10-19 lab report](../lab-reports/2026-10-19-launch-diagnostics-on-a-mac.md)
(the measurement and the evidence), [`storage-and-config.md`](../reference/storage-and-config.md)
(where the provisioning log lives).

---

## 1. How the stage decides the log failed

The **provisioning stage** is the shell script `provision.Script` (in `internal/provision`)
generates: it runs the setup steps with their output piped through `yolo internal log-append --tee`
into `<workspace>/.yolo/startup.log`, then reads the exit status of both sides of that pipe. The
**log writer** is that `log-append` process. When the writer's status is non-zero, the script prints
*"Provisioning log could not be written: … — repair workspace .yolo permissions or free disk space,
then retry yolo."* and exits with `provision.StageLogFailedStatus`. The macos-user backend reads that
status and prints its own line with the same two causes (`runProvisionStage` in
`internal/macosuser`).

A `Ctrl-C` signals the whole foreground process group, so the writer is killed too and its status is
non-zero. In the measured run the setup's status was 141 (SIGPIPE, because the writer had gone), the
script's next append of its failure line into the same log succeeded, and then the writer's non-zero
status sent it to the unwritable-log message anyway.

## 2. What does not change

A writer that really cannot write, such as a full disk or a `.yolo` the user cannot write, still
stops the launch, because without the log the stage cannot prove whether provisioning refused the
launch (`provision.StageLogFailedStatus`'s comment says why). Only the report of an interrupt changes.

## Open Questions

1. 💬 **OQ-PLW1: When the user presses Ctrl-C during provisioning, what should the launch say?**

   Matt launches a jail and presses Ctrl-C while `mise install` runs. The launch prints
   *"Provisioning log could not be written … repair workspace .yolo permissions or free disk
   space, then retry yolo."* His log is complete, he owns it, and the disk has 67 GiB free. He
   spends time checking permissions and disk before realizing his own Ctrl-C caused it. The stage
   reads only the writer's exit status, and a signalled writer and a failed one both exit non-zero.

   - **A — Read the status as an interrupt.** A writer that exited from SIGINT or SIGPIPE (status
     130 or 141) prints *"Provisioning interrupted — run yolo again to continue."* A writer killed
     by any other signal still gets the permissions-and-disk message.
   - **B — Check the log before blaming it.** When the writer exits non-zero, the stage appends one
     more line to the log. If that append succeeds, the log is writable, and the stage says the
     provisioning was interrupted and to run `yolo` again. Only a failed append gets the
     permissions-and-disk message. No signal numbers are involved, and it covers any way the writer
     can die.
   - **C — Stop naming causes.** Keep one message, but drop the guesses: *"Provisioning stopped
     before its log was complete (writer exit 130); run yolo again, and if this repeats, check that
     .yolo is writable and the disk has space."* Nothing new is decided, and a real write failure
     reads the same as an interrupt.

   <!-- vantage: question id=OQ-PLW1 leaning="B — check the log before blaming it. It tests the claim the message makes, so it is right for every way the writer can stop, and a failed setup already makes such an append today." -->

   _Leaning:_ B — check the log before blaming it. It tests the claim the message makes, so it is
   right for every way the writer can stop, and a failed setup already makes such an append today.

   **Answer:**
   > _(empty — fill in when decided)_
