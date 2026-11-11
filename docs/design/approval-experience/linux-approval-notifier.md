---
status: accepted
stage: BUILT
next: "Review and integrate the bounded notifier, then capture the integrated candidate"
depends-on:
  - ../boundary-broker.md
  - native-notification-gallery.md
tags: [design, approvals, notifications, linux]
---

# A Linux doorbell that only calls the host approval CLI

**Status:** 2026-11-09. Grounded at `62b8bbda375acda7caa3f675a4844aa020e8b060`.
The opt-in per-request renderer is implemented in
[`internal/approvalnotify`](../../../internal/approvalnotify/), with consumer-loop, godbus-adapter,
signal and existing-hook lifecycle tests.
[`internalapprovalnotify_test.go`](../../../internal/cli/internalapprovalnotify_test.go) drives
its subprocess decision runner through the current CLI/store boundary. This is bounded
protocol evidence, not a native capture of the integrated candidate or completion of the per-user singleton.
The supervisor approved optional delivery through the existing `on_request`, not automatic activation.

> **In short.** A first-party Linux hook can own its D-Bus notification and forward
> deliberate answers through `yolo approve`, without acquiring any approval authority.

**Needs your ruling:** None for this bounded slice.
**Reads with:** [the implementation plan](linux-approval-notifier-plan.md),
[boundary-broker §6](../boundary-broker.md#6-the-doorbell-a-persistent-desktop-notification),
[the gallery](native-notification-gallery.md#4-presentation-changes-the-evidence-justifies).

## Contract and staged scope

BB-D11 supplies the transport; BB-D75 supplies the existing optional hook and CLI-only
answer boundary; BB-D98 / [OQ-BB10](../boundary-broker.md#OQ-BB10) supply the unchanged no-notifier behavior.
The brokerstore remains the sole decision authority. The hook's stdout never means consent.
The posted text is cut to 4096 bytes before it is escaped, so an escaped body is at most about 6×4096 bytes (about 24 KiB) plus the fixed next-step wording; the bound is 6×, not 5×.

The user may explicitly configure `on_request = 'exec yolo internal approval-notify'`.
This document does not change user config, the personal hook, grants, source adapters,
CLI authorization, or launch behavior. The hidden command consumes the existing schema-1
request-filed JSON from stdin. It is host-only and Linux-only. It keeps one private
connection for that request's life, using only the supplied `DBUS_SESSION_BUS_ADDRESS`;
no address guessing, session discovery, `dbus-launch`, or server activation is allowed.

The connection checks the current `org.freedesktop.Notifications` owner and the `actions`
capability before posting. The request notification is critical, resident, timeout zero, with no
`default` action. Explicit keys are `once`, `15m`, `deny`; labels are Allow once, Allow for
15 minutes, Deny. They invoke the absolute hook-provided `YOLO_BIN` with respectively
`approve <id>`, `approve <id> --for 15m`, `approve <id> --deny`, without a shell.
The current CLI may refuse `--for`; the notifier must report that refusal, not substitute
Allow once. Removed Add/Remember are not restored. This fixed first-slice list is not an
implementation of the future `approvals.buttons` configuration in [§6.4](../boundary-broker.md#64-choosing-a-duration).

Every signal is checked for interface, path, correctly typed payload, issued notification
id, and current server owner. The owner must also be the owner that issued the id:
a replacement daemon must not inherit an old daemon's numeric id. Only offered action
keys count. Only `NotificationClosed` reason 2 invokes Deny; reasons 1, 3 and 4 do not
answer. A body click never grants; if the daemon reports it as dismissal reason 2, it denies.
Server loss, connection loss and unrecognized signals never decide anything.

Handled SIGINT/SIGTERM, the request expiry, and the existing hook runner's stop when a
request is decided elsewhere all withdraw the issued id while the same connection is
still open. A stop that lands during the post still withdraws the id the post returns, and
the post itself is bounded. A stop that reaches an in-flight `yolo approve` interrupts it
(SIGINT, with a bounded wait) rather than killing it. Withdrawal does not invoke Deny. An accepted button invokes the CLI once;
its success is authoritative only through the CLI/store. On exits 3, 5 or 6, a short
non-actionable follow-up carries the CLI's refusal and next step, escaped for the desktop
markup dialect. The follow-up is normal urgency, not resident, and expires after ten seconds. The original actionable notification is withdrawn. The refusal's status
and text remain available through the existing hook failure mechanism. No feedback toast
is interpreted as an answer, and its closure cannot deny the original request.

Without an address, server owner or actions capability, nothing is posted or decided.
The command returns a diagnostic failure to the existing background hook-failure path,
not to the launch terminal. No terminal fallback, startup notice, notice file, dialog,
HTTP listener or new authorization surface is introduced.

## What this can and cannot prove

- **P-1:** typed dismissal reason 2 is Deny through the CLI, never inferred from empty output.
- **P-2, partial:** handled SIGINT/SIGTERM and decided-elsewhere withdrawal close the issued id while the connection is open. The time bounds are not one number. Under the broker hook runner, a stop is SIGINT to the whole process group, then a group SIGKILL after `StopGrace` (2 s), so the notifier's 5 s post bound and the CLI's 5 s `WaitDelay` are both capped at 2 s there. A direct SIGINT or SIGTERM with no follow-up kill may get the full 5 s. A post still stalled at the group kill has an unlearned id, so a toast it later shows may be orphaned and cannot be withdrawn. No every-handled-stop guarantee is claimed. SIGKILL, OOM and an uncatchable process death run no cleanup; a surviving broker-owned notification registry or the BB-D71 singleton remains future work. The D-Bus connect handshake is unbounded and outside every bound above. It takes no context on purpose: a context that ends closes the connection before the withdraw can run. A bounded connection would be a separate mechanism, and this limit does not block the present slice.
- **P-4:** CLI refusal feedback is non-actionable and cannot widen a grant.
- **P-3:** no default grant action on Linux; no claim about the private Mac hook or renderer.

BB-D71's auto-started per-user singleton, cross-source watch, lock, broker-restart survival,
and reshow/replacement remain unbuilt. This hook holds its own connection, but does not
meet that singleton lifecycle. It is deliberately optional, not a rewrite of [§6](../boundary-broker.md#6-the-doorbell-a-persistent-desktop-notification).

NG-D5 still requires native re-capture of the integrated candidate. The captured candidate
in [§4.2 of the Linux capture](linux-notification-capture.md#42-the-notifier-candidate) is
`ff92fabc4`; the stop-boundary repair (the 5 s `WaitDelay` and post bound) landed later, at
`9d5bcd413`, and has no native capture. Fake transport and
CLI/store tests demonstrate bounded protocol behavior, not desktop appearance or full
P-2. P-5 through P-9 wording changes stay fenced until rule fixes are independently proven.
The existing gallery images remain evidence of the owner's baseline, not this notifier.
The capture lead owns native Linux evidence; macOS remains missing.
