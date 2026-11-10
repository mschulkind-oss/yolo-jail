---
title: "Mac capture runner: what a macOS approval notification looks like, and what it does when pressed, dismissed or withdrawn"
date: 2026-11-09
status: draft
stage: SKETCH
next: "Landed at `2dd3d8e8f`; the owner's Mac agent runs M-0 to M-13 at main and files a lab report"
tags: [design, approvals, notifications, macos, mac-checks, gallery]
summary: "How the owner's Mac agent renders the shared approval fixtures as real macOS notifications, captures them for the gallery with provenance, and measures what terminal-notifier 3 and the owner's current alerter and dialog baseline print on every press, dismissal and withdrawal."
---

# Mac capture runner: what a macOS approval notification looks like, and what it does when pressed, dismissed or withdrawn

**Status:** 2026-11-09. Nothing captured, nothing measured. Every behavior below marked *expected*
comes from [boundary-broker.md §6.2](../boundary-broker.md#62-macos) (SOURCED there) or from
AppleScript's documented semantics, and is what the checks exist to confirm or break.

> **In short.** macOS approval notifications are settled by measurement on a real logged-in Mac,
> never argued from a Linux facsimile. One set of numbered checks both produces the gallery's macOS
> captures, in the shared fixture format, and measures the five behaviors the macOS design leaves
> open: how three actions collapse, what each press prints, what a body click and a dismissal print,
> how a notification is withdrawn, and when the notifier refuses.

**Why it matters.** The [approval-experience umbrella](README.md#7-proposed-sibling-map) gives this
document the reader question *"How are macOS notifications rendered, authorized and captured, and
how do action collapse and withdrawal behave?"*. Until it is answered, the gallery's macOS column is
`missing` for every fixture, and the toast notifier's macOS path rests on unverified claims about
terminal-notifier 3.

**The shape.** A scratch run directory on the Mac; a fixture loader, a process-group launcher and a
stub `yolo` shared by every check ([§3](#3-shared-tools)); checks [M-0](#m-0-environment-and-inventory)
to [M-13](#m-13-the-baseline-hook-end-to-end-under-yolos-stop-sequence); PNGs plus a manifest in the
gallery, and a lab report.

**Start at [§4](#4-mac-checks)**, the checks. [§2](#2-capture-logistics) is the setup they all assume.

**Needs your ruling:** None. Everything here is measurement.

**Reads with:** [README.md](README.md) (the umbrella: rulings AE-R1 to AE-R6 and the five rules every
sibling keeps), [boundary-broker.md §6](../boundary-broker.md#6-the-doorbell-a-persistent-desktop-notification)
(the authority on how yolo notifies; this document measures it, never changes it),
[`fixture.go`](../../../internal/approvalfixtures/fixture.go) (the fixture format, whose doc comments
are its reference), the `mac-handoff` skill (how Mac checks are written and how results come back).

---

## 1. Scope

### 1.1 What the checks decide

| Open item | Where it is open | Checks |
| --- | --- | --- |
| How three `-action` values collapse into **Options**, and what the user sees before opening it | [boundary-broker.md §6.2](../boundary-broker.md#62-macos) | [M-2](#m-2-options-collapse) |
| Which string a press prints | [§6.2](../boundary-broker.md#62-macos): "the action the child prints is the answer" | [M-3](#m-3-what-each-action-press-prints) |
| What a body click prints (`@CONTENTCLICKED` is unverified for 3.x) | [§6.2](../boundary-broker.md#62-macos) | [M-4](#m-4-body-click) |
| What a dismissal prints (`@CLOSED` is the expected Deny) | [§6.2](../boundary-broker.md#62-macos) | [M-5](#m-5-dismissal) |
| That a notification with no `-timeout` never times out, and keeps its buttons in Notification Center after a banner hides | [§6.2](../boundary-broker.md#62-macos) | [M-6](#m-6-no-timeout-banners-and-alerts) |
| How 3.x withdraws a notification ("the first thing the macOS build measures") | [§6.2](../boundary-broker.md#62-macos) | [M-7](#m-7-withdrawal) |
| Exit 3 (not authorized) and exit 4 (no GUI session) | [§6.2](../boundary-broker.md#62-macos), [§6.5](../boundary-broker.md#65-when-there-is-no-notifier-or-nobody-answers) | [M-8](#m-8-exit-3-and-exit-4) |
| What Focus and the per-app style do to a pending request | [§6.2](../boundary-broker.md#62-macos) | [M-9](#m-9-focus-and-style) |
| How much of a long request is visible | Umbrella [§4.3](README.md#43-representative-gallery-coverage) | [M-10](#m-10-long-text-truncation) |
| What the owner's current baseline looks like and does | Umbrella [§4.1](README.md#41-evidence-first-not-an-html-imitation) | [M-11](#m-11-baseline-alerter) to [M-13](#m-13-the-baseline-hook-end-to-end-under-yolos-stop-sequence) |

Every check is held against the umbrella's rule 3 ([§3.1](README.md#31-five-rules-that-bind-every-sibling)):
**a body click never grants, a dismissal is Deny, expiry is not consent, and unknown output is not
approval.** A keystroke that grants without the user choosing a button is the dialog analogue of a
body click and is held to the same rule.

### 1.2 Non-goals

- **No Linux facsimile counts as Mac proof.** An HTML tile, a Linux-rendered screenshot styled to
  look like macOS, or a capture from a virtual display without a logged-in user is a `preview` in
  the fixture format, never a `native-capture`.
- **No modal dialog as the design.** The `display dialog` path is measured only because it is part
  of the owner's current baseline. [boundary-broker.md §6.2](../boundary-broker.md#62-macos) rules
  it out for yolo, and nothing measured here reopens that.
- **No signed helper app.** Building, signing or notarizing yolo's own `.app` is later work under
  [§6.2](../boundary-broker.md#62-macos). These checks measure terminal-notifier 3 and the baseline only.
- **No yolo change, and no real request.** The checks drive the notifiers directly with fixture
  environment and a stub `yolo`. They file nothing in a real request store and decide nothing.
- **No permanent change to the Mac.** Settings changed for a check (an app's notification
  permission, its style, Focus, appearance) are restored at the end of that check, and the lab
  report says they were. Nothing is installed that the owner did not already have, except
  terminal-notifier 3 if [M-0](#m-0-environment-and-inventory) finds it missing.

### 1.3 Terms

- **Posting tool** *(coined here)*: the program that hands a notification to macOS and prints the
  answer — terminal-notifier, alerter, or `osascript`. It is recorded as `daemon` in a capture's
  provenance, because the fixture format names that key for the Linux notification server; on
  macOS the server is always the system's, so the posting tool is what varies. Not the toast
  notifier, which is yolo's daemon that would drive a posting tool.
- **Notification style**: the per-app setting in System Settings › Notifications that decides
  whether a notification stays on screen until acted on or slides away into Notification Center.
  macOS has labelled it *Alerts* and *Banners*, and later releases *Persistent* and *Temporary*;
  record the label as the Mac shows it.
- **Baseline** *(from the umbrella, [§4.1](README.md#41-evidence-first-not-an-html-imitation))*:
  the notifier behind the toasts the owner sees today, captured before any cosmetic change.

## 2. Capture logistics

### 2.1 The session

| Requirement | Why | How the agent checks it |
| --- | --- | --- |
| A logged-in GUI user at the console, screen unlocked, display awake | A notification posted with no GUI session is refused (exit 4, *expected*) or shown on nobody's screen | `who` lists a `console` line for the user; `pmset -g assertions` shows no display sleep |
| Commands run from a terminal app inside that session, not over SSH alone | An SSH-only shell may lack the GUI bootstrap context; [M-8](#m-8-exit-3-and-exit-4) measures exactly that | The agent's shell is a child of the terminal app (`ps -o ppid= -p $$` walks up to it) |
| Notifications allowed for the posting tool | Otherwise exit 3 (*expected*) | System Settings › Notifications lists the tool with *Allow notifications* on; the first post's authorization prompt is accepted once |
| Focus (Do Not Disturb) off, except in [M-9](#m-9-focus-and-style) | Focus hides or queues notifications | Control Center shows no Focus active; record `focus: off` |
| Accessibility permission for the terminal app | Pressing buttons, opening Options and sending keystrokes is driven through System Events | `osascript -e 'tell application "System Events" to get name of every process'` succeeds |
| Screen Recording permission for the terminal app | `screencapture` of another app's window, and window names, need it | A test `screencapture -x /tmp/t.png` shows other apps' content, not only the desktop |

**Who presses the buttons.** The Mac agent drives every press itself: it finds the element through
System Events (the `NotificationCenter` process's UI elements), then clicks its centre with a real
mouse event (`cliclick c:<x>,<y>` if installed) or, failing that, performs the element's AXPress
action. The witness records which, since an AXPress is not proof a real click behaves the same.
Keystrokes go through `osascript -e 'tell application "System Events" to key code <n>'` (36 is
Return, 53 is Escape), which posts a real key event to the frontmost app.

### 2.2 Appearance, scale and style

- **Appearance:** light unless the check says dark. Read it with
  `defaults read -g AppleInterfaceStyle 2>/dev/null || echo Light` (prints `Dark` or `Light`).
  Switch with `osascript -e 'tell application "System Events" to tell appearance preferences to set dark mode to true'`
  (or `false`), and restore the owner's value at the end.
- **Scale:** the main display's backing scale factor,
  `osascript -l JavaScript -e 'ObjC.import("AppKit"); $.NSScreen.mainScreen.backingScaleFactor'`
  (prints `2` on a Retina display). Captures are taken on the built-in or main display only.
- **Style:** Alerts (Persistent) for gallery captures, since that is the style yolo's first
  notification asks the user to choose ([boundary-broker.md §6.2](../boundary-broker.md#62-macos)).
  [M-6](#m-6-no-timeout-banners-and-alerts) and [M-9](#m-9-focus-and-style) also run under Banners.

### 2.3 Screenshotting only the notification

Notifications are drawn by the system's Notification Center process, not by the posting tool, so
the window to capture belongs to it. In order of preference:

1. **The notification's own frame.** Read the position and size of the notification element from
   System Events, then `screencapture -x -R<x>,<y>,<w>,<h> <file>.png`. This is the gallery image.
2. **The window.** List on-screen windows owned by Notification Center with the snippet below, then
   `screencapture -x -o -l<window id> <file>.png`. On some releases that window is a tall
   transparent panel; if the PNG is much larger than the banner, use method 1.
3. **Full screen, then crop.** `screencapture -x full.png`, then crop to the frame from method 1
   with any tool that keeps pixels unscaled. Last resort; say so in the witness.

```swift
// windows.swift — run with: swift windows.swift
import CoreGraphics
let list = CGWindowListCopyWindowInfo([.optionOnScreenOnly], kCGNullWindowID) as! [[String: Any]]
for w in list {
    let owner = w[kCGWindowOwnerName as String] as? String ?? ""
    guard owner.contains("Notification") else { continue }
    let b = w[kCGWindowBounds as String] as! [String: Any]
    print(w[kCGWindowNumber as String]!, owner, w[kCGWindowLayer as String]!,
          b["X"]!, b["Y"]!, b["Width"]!, b["Height"]!)
}
```

Every capture records which method made it (provenance key `capture_method`). Captures are PNG at
the display's native pixels; never rescale them.

### 2.4 Provenance per capture

A gallery capture becomes an `Evidence` entry ([`fixture.go`](../../../internal/approvalfixtures/fixture.go))
with `platform: "macos"`, `kind: "native-capture"` and these provenance values:

| Key | macOS meaning | Example |
| --- | --- | --- |
| `os` | `sw_vers` product version and build, and `uname -m` | `macOS 15.1 (24B83) arm64` |
| `daemon` | The posting tool | `terminal-notifier` |
| `daemon_version` | Its version as its package manager or bundle states it | `3.1.0` |
| `theme` | Appearance and notification style, as `<light\|dark>, <style label>` | `light, Alerts` |
| `scale` | Backing scale factor | `2` |
| `captured_at` | UTC, RFC 3339 | `2026-11-12T15:04:05Z` |
| `commit` | The main commit the checks ran at | the full SHA |
| `capture_method` *(extra)* | `frame`, `window` or `full-crop` ([§2.3](#23-screenshotting-only-the-notification)) | `frame` |
| `bundle_id` *(extra)* | The posting app's bundle identifier | as read in M-0 |
| `focus` *(extra)* | `off`, or the Focus mode that was on | `off` |

`tested` is `visual` for a capture only, `action` when a press was also driven and its output
recorded, and `lifecycle` when a withdrawal or dismissal was driven and observed.

## 3. Shared tools

Run directory: `/Users/Shared/yolo/mac-capture`, created fresh. A checkout of main at the commit
under test supplies the fixtures. Set once per shell:

```sh
RUN=/Users/Shared/yolo/mac-capture
SRC=<path to the main checkout>
FIX=$SRC/internal/approvalfixtures/fixtures
mkdir -p "$RUN/bin" "$RUN/out" "$RUN/shots"
export PATH="$RUN/bin:$PATH"
```

**Load a fixture's hook environment** (fixtures with `prompt: "request"` only; one with
`prompt: "none"` files no request and is never posted):

```sh
fixture() { eval "$(jq -r '.hook_env | to_entries[] | "export \(.key)=\(.value | @sh)"' "$FIX/$1.json")"; }
```

**Stub `yolo`**, which only logs its arguments, so a hook that "approves" changes nothing:

```sh
cat > "$RUN/bin/yolo" <<EOF
#!/bin/sh
printf '%s\n' "\$*" >> "$RUN/yolo-calls.log"
EOF
chmod +x "$RUN/bin/yolo"
```

**Launch a posting tool the way yolo launches a hook**: in its own process group, with SIGINT and
SIGTERM at their defaults (a shell's `&` would leave SIGINT ignored, which would make every
withdrawal measurement wrong), recording output, exit code and timestamps:

```sh
cat > "$RUN/bin/launch" <<'EOF'
#!/bin/sh
# launch <label> <command...>: run command in its own process group; record to $RUN/out/<label>/
label=$1; shift; out="$RUN/out/$label"; mkdir -p "$out"
now() { perl -MTime::HiRes=time -e 'printf "%.3f\n", time'; }
now > "$out/start"
perl -e 'setpgrp(0,0); $SIG{INT}=$SIG{TERM}="DEFAULT"; exec @ARGV or die "exec: $!"' "$@" \
  > "$out/stdout" 2> "$out/stderr" &
echo $! > "$out/pgid"
wait $!; echo $? > "$out/exit"; now > "$out/end"
EOF
chmod +x "$RUN/bin/launch"
export RUN
```

Run it in the background (`launch <label> … &`) and act on the notification while it waits.
`$RUN/out/<label>/pgid` is the group to signal. Every witness quotes `stdout` byte for byte
(`od -c stdout` when it is not plain text), `exit`, and `end` minus the time of the action.

**Post through terminal-notifier 3** with the three default answers of
[boundary-broker.md §6.4](../boundary-broker.md#64-choosing-a-duration) and no `-timeout`:

```sh
tn() {  # tn <label> [extra flags...]
  label=$1; shift
  launch "$label" terminal-notifier -title "yolo: $YOLO_REQUEST_HEADLINE" \
    -message "$YOLO_REQUEST_DESCRIPTION" -group "$YOLO_REQUEST_ID" \
    -action "Allow 15 min" -action "Add to working set" -action "Deny" "$@" &
}
```

> [!WARNING]
> The 3.x flag spellings (`-action` repeated, `-group`, `-remove`) are taken from
> [boundary-broker.md §6.2](../boundary-broker.md#62-macos) and the 2.x lineage and are not verified
> for 3.x. [M-0](#m-0-environment-and-inventory) saves `terminal-notifier -help`; where a spelling
> differs, use the 3.x one, and record the difference in the lab report — it is itself a finding
> the macOS design needs.

## 4. Mac checks

**Commit:** at main `2dd3d8e8f` or later; record the SHA in
every capture's `commit` and in the lab report. **Build:** none — the checks use the checkout's
fixtures and the posting tools, not a yolo binary. **Order:** M-0 first; the rest in any order,
each restoring what it changed.

Each check is: **Run**, **Observe**, **Witness** (what goes in the lab report), and **Pass**.

### M-0. Environment and inventory

- **Run:** `sw_vers`; `uname -m`; `brew list --versions terminal-notifier alerter`;
  `command -v terminal-notifier alerter cliclick jq`; `terminal-notifier -help > $RUN/out/tn-help.txt 2>&1`;
  the appearance and scale reads of [§2.2](#22-appearance-scale-and-style). Find the posting
  apps' bundle identifiers with `mdls -name kMDItemCFBundleIdentifier` on each `.app` the
  package installed. Record each tool's notification settings in System Settings (allowed, style).
  If terminal-notifier is absent or older than 3.0, `brew install terminal-notifier`.
- **Observe:** which baseline path the owner's hook takes on this Mac: alerter if `command -v alerter`
  finds it, else the dialog.
- **Witness:** every output above, the help text, and the baseline path.
- **Pass:** terminal-notifier 3.0 or later is on `PATH`. A 2.x is a failure of the setup, not of a
  check ([§6.2](../boundary-broker.md#62-macos): "A 2.x is no notifier").

### M-1. Gallery capture

- **Run:** for each fixture whose `prompt` is `request`, in light appearance and Alerts style:
  `fixture <id>; tn M-1-<id>`, wait for the notification, capture it by [§2.3](#23-screenshotting-only-the-notification),
  then press Deny to end it. Repeat once in dark appearance.
- **Observe:** title, body lines and the action affordance as drawn.
- **Witness:** `gallery/captures/macos/<id>.png` (light) and `gallery/captures/macos/<id>.dark.png`,
  each with its provenance in the manifest ([§5](#5-where-the-outputs-land)).
- **Pass:** a capture exists for every request fixture, at native pixels, showing only the
  notification. `tested: visual`.

### M-2. Options collapse

- **Run:** `fixture <shortest fixture>; tn M-2-three`. Capture the notification closed; hover it and
  capture; open the Options menu and capture. Repeat with one `-action` only (`launch M-2-one
  terminal-notifier … -action "Allow 15 min" &`).
- **Observe:** whether any action label is visible before Options is opened; the menu's label
  ("Options" or other); the order of the three entries; whether one action is drawn as a button
  rather than a menu.
- **Witness:** `gallery/captures/macos/checks/M-2-closed.png`, `M-2-hover.png`, `M-2-open.png`,
  `M-2-one.png`, and the answers above.
- **Pass:** it is a measurement; pass is a complete record. Note for the gallery whether a user can
  see what each answer grants without opening a menu.

### M-3. What each action press prints

- **Run:** three times, once per action: `tn M-3-<n>`, open Options, choose action *n*.
- **Observe:** `stdout`, `exit`, latency from the click to `end`; whether the notification leaves
  the screen and Notification Center on its own.
- **Witness:** the three `stdout` files byte for byte, exit codes, latencies.
- **Pass:** each press prints exactly its own label (or a stable key derivable from it), once, and
  exits 0. Anything else is a failure [§6.2](../boundary-broker.md#62-macos) must absorb before the toast notifier relies on it.

### M-4. Body click

- **Run:** `tn M-4`, click the notification body (not Options, not close).
- **Observe:** `stdout`, `exit`; whether an app is activated or a window opened.
- **Witness:** `stdout` byte for byte (is it `@CONTENTCLICKED`?), exit code, what was activated.
- **Pass:** the output is none of the three action labels. If it equals one, rule 3 is broken and
  the report says so first.

### M-5. Dismissal

- **Run:** three variants: hover and click the close control (`tn M-5-close`); swipe it away
  (`tn M-5-swipe`, a drag driven by `cliclick dd:/du:` or recorded as not drivable); in Banners
  style, let it slide into Notification Center and use Clear there (`tn M-5-clear`).
- **Observe:** `stdout` and `exit` for each.
- **Witness:** the three outputs byte for byte and exit codes.
- **Pass:** every dismissal prints something distinct from every action label; *expected*
  `@CLOSED`. A dismissal that prints nothing and leaves the child running is recorded as such: the
  request would then wait with nothing on screen.

### M-6. No timeout, Banners and Alerts

- **Run:** `tn M-6-alerts` under Alerts style; `tn M-6-banners` under Banners. Wait 10 minutes
  each; then, for the banner, open Notification Center and press Allow 15 min from there.
- **Observe:** whether either notification leaves the screen on its own; whether either child
  exits on its own; whether the banner's entry in Notification Center still has its Options.
- **Witness:** screenshots at 0 s and 10 min (`checks/M-6-*.png`), each child's `exit` and `end`,
  the late press's `stdout`.
- **Pass:** neither child exits before a user action; the press from Notification Center prints
  the action as in M-3. A child that prints `@TIMEOUT` or exits is a failure of the "no
  `-timeout` never times out" claim.

### M-7. Withdrawal

Run each variant on a fresh notification, under Alerts style, with a second notification of a
different fixture on screen as a control.

| Variant | Command, while the notification waits |
| --- | --- |
| a. remove by group | `terminal-notifier -remove "$YOLO_REQUEST_ID"` from a second shell |
| b. SIGINT to the group | `kill -INT -- -$(cat $RUN/out/M-7b/pgid)` |
| c. SIGTERM to the group | `kill -TERM -- -$(cat $RUN/out/M-7c/pgid)` |
| d. SIGINT, then SIGKILL after 2 s | as b, then `kill -KILL -- -<pgid>` 2 s later (yolo's hook stop sequence: [`brokerhook.go`](../../../internal/brokerhook/brokerhook.go), `StopGrace`) |

- **Observe:** per variant, whether the notification leaves the screen, whether its entry leaves
  Notification Center (open it and look), the time from the command to its disappearance, the
  child's `stdout` and `exit`, and that the control notification is untouched.
- **Witness:** before and after screenshots of the screen and of Notification Center
  (`checks/M-7<v>-*.png`), timings, outputs, exit codes. Whether `-remove` exists in 3.x at all.
- **Pass:** at least one variant removes the notification from the screen **and** from
  Notification Center without touching the control; that variant is what [§6.2](../boundary-broker.md#62-macos)'s "Withdrawing" step
  uses. For the hook path, variant b must do so within 2 s; a notification that survives d is a
  stale button yolo cannot take back, and the report says so first.

### M-8. Exit 3 and exit 4

- **Run:** (a) turn *Allow notifications* off for terminal-notifier, `tn M-8a`, then restore it.
  (b) If Remote Login is already on (never turn it on for this; otherwise record
  "cannot run"), from `ssh localhost` run the same `terminal-notifier` command with the fixture
  exported.
- **Observe:** exit code and output for each; for (b), whether a notification appeared on the
  console screen anyway.
- **Witness:** exit codes, `stdout`/`stderr`, and a console screenshot for (b).
- **Pass:** (a) exits 3 and (b) exits 4, as [§6.2](../boundary-broker.md#62-macos) states. If (b) posts to the console instead, that
  is the macOS form of the unattended-desktop hazard [§6.1](../boundary-broker.md#61-linux) guards against on Linux, and is reported
  first.

### M-9. Focus and style

- **Run:** turn on Do Not Disturb; `tn M-9`; wait 1 minute; turn Focus off. Then set the style to
  None (notifications allowed, no on-screen style), `tn M-9-none`, and restore.
- **Observe:** whether anything shows while Focus is on; whether it appears when Focus ends;
  whether the child is running throughout; exit codes. Same for style None.
- **Witness:** screenshots, `exit`, whether the child was alive (`ps -p <pid>`).
- **Pass:** a measurement. The finding [§6.5](../boundary-broker.md#65-when-there-is-no-notifier-or-nobody-answers) needs is whether a suppressed notification is
  distinguishable from a shown one (exit code or output); if not, a request waits silently.

### M-10. Long text truncation

- **Run:** pick the request fixture with the longest description:
  `for f in $FIX/*.json; do jq -r 'select(.prompt=="request") | [(.hook_env.YOLO_REQUEST_DESCRIPTION|length), .id] | @tsv' $f; done | sort -n | tail -1`.
  `fixture <id>; tn M-10`. Capture it collapsed; then expand it (click-and-hold or the expand
  chevron, whichever the release offers) and capture again.
- **Observe:** how many title and body lines show collapsed and expanded; where text is cut and
  whether an ellipsis marks the cut; whether the request id survives.
- **Witness:** `checks/M-10-collapsed.png`, `M-10-expanded.png`, the visible line counts.
- **Pass:** a measurement. The gallery needs to know whether the scope a button grants
  ([§6.3](../boundary-broker.md#63-what-the-notification-shows), body item 3) is visible without
  expanding.

### M-11. Baseline: alerter

Run only if M-0 found alerter; otherwise record "not installed; the baseline is the dialog" and do
not install it. This is the path [boundary-broker.md §6.2](../boundary-broker.md#62-macos) rejected
(the deprecated `NSUserNotification`); it is measured as the baseline, not as a candidate.

```sh
al() {  # the baseline's alerter call, verbatim in shape
  launch "$1" alerter -title "yolo: $YOLO_REQUEST_HEADLINE" -message "$YOLO_REQUEST_DESCRIPTION" \
    -actions "Allow once,Allow 1h,Deny" -closeLabel Ignore &
}
```

- **Run:** `al M-11-shot` and capture it for every request fixture (gallery variant `<id>.baseline.png`);
  then one run each: press each action (`M-11-a1` to `a3`), press Ignore (`M-11-ignore`), click the
  body (`M-11-body`), and SIGINT the group (`M-11-int`, then SIGKILL after 2 s if it is still
  running).
- **Observe:** output and exit per run; whether it posts at all on this release; whether SIGINT
  removes it from the screen and from Notification Center.
- **Witness:** captures, outputs byte for byte, exit codes, before and after screenshots for the
  SIGINT run, `alerter -version` or the package version.
- **Pass:** a measurement. Flag first any run whose output is exactly `Allow once`, `Allow 1h` or
  `Deny` without that action having been chosen.

### M-12. Baseline: the modal dialog

```sh
dlg() {  # the baseline's dialog, verbatim in shape
  launch "$1" osascript -e "display dialog \"$YOLO_REQUEST_DESCRIPTION\" with title \"yolo: approval\" \
    buttons {\"Deny\",\"Allow 1h\",\"Allow once\"} default button \"Allow once\" giving up after 3500" &
}
```

(A description containing a double quote breaks this quoting; use a fixture without one, or pass the
text through `on run argv`, and record which.)

- **Run:**
  1. `dlg M-12-shot`, capture the dialog for every request fixture (gallery variant
     `<id>.dialog.png`, `capture_method` `window`).
  2. **The Return-key hazard.** Open TextEdit with a new document and make it frontmost. Start
     `dlg M-12-return`. Without clicking anything, send Return (`key code 36`).
  3. **Focus steal.** Repeat step 2, but start typing into TextEdit (keystrokes every 200 ms) from
     before the dialog appears until after it is visible, then send Return.
  4. Escape (`key code 53`) on a fresh dialog: `M-12-escape`.
  5. Each button by click: `M-12-b1` to `b3`.
  6. SIGINT the group of a fresh dialog: `M-12-int`, then SIGKILL after 2 s if needed.
  7. `giving up after` with a short value (`giving up after 5`): `M-12-giveup`.
- **Observe:** for every run, `stdout` (expected `button returned:<label>, gave up:false`) and
  `exit`; in step 2, whether Return chose *Allow once*; in step 3, whether the dialog took keyboard
  focus and which keystrokes reached it; in step 6, whether the dialog leaves the screen.
- **Witness:** outputs, exit codes, a screenshot of the dialog in front of TextEdit, the TextEdit
  text after step 3.
- **Pass:** *expected to fail rule 3*: by AppleScript's semantics the default button answers
  Return, so step 2 prints `button returned:Allow once` and the baseline hook grants Allow once
  without a button press. Step 3 tells whether a user typing elsewhere can grant by accident. Both
  results go into the gallery's baseline entry as a finding, whatever they are.

### M-13. The baseline hook end to end, under yolo's stop sequence

The owner's hook is a personal script; the check uses this generic stand-in with the same mapping.

```sh
cat > "$RUN/baseline-hook.sh" <<'EOF'
#!/bin/sh
# stand-in for the owner's on_request hook on macOS
if command -v alerter >/dev/null; then
  ans=$(alerter -title "yolo: $YOLO_REQUEST_HEADLINE" -message "$YOLO_REQUEST_DESCRIPTION" \
        -actions "Allow once,Allow 1h,Deny" -closeLabel Ignore)
else
  ans=$(osascript -e "display dialog \"$YOLO_REQUEST_DESCRIPTION\" buttons {\"Deny\",\"Allow 1h\",\"Allow once\"} default button \"Allow once\" giving up after 3500" \
        | sed -n 's/^button returned:\([^,]*\), gave up:false$/\1/p')
fi
case $ans in
  "Allow once") yolo approve "$YOLO_REQUEST_ID" ;;
  "Allow 1h")   yolo approve "$YOLO_REQUEST_ID" --for 1h ;;
  "Deny")       yolo approve "$YOLO_REQUEST_ID" --deny ;;
esac
EOF
chmod +x "$RUN/baseline-hook.sh"
```

- **Run:** with a fixture loaded and `$RUN/yolo-calls.log` empty: `launch M-13-<case> $RUN/baseline-hook.sh &`
  for each case — each of the three answers, dismissal or Ignore, a body click (alerter only),
  give-up (dialog only, with a short `giving up after`), and stop: `kill -INT -- -<pgid>`, then
  `kill -KILL -- -<pgid>` 2 s later if the group is still alive.
- **Observe:** the line appended to `yolo-calls.log` per case; for stop, whether the notification
  or dialog survives the hook's death.
- **Witness:** `yolo-calls.log` after each case, the hook's `exit`, a screenshot after the stop.
- **Pass:** the three answers log `approve <id>`, `approve <id> --for 1h` and `approve <id> --deny`;
  every other case logs nothing; after stop, nothing actionable remains on screen. A surviving
  dialog or alert after SIGKILL is a stale button and is reported first.

## 5. Where the outputs land

| Output | Path | Writer |
| --- | --- | --- |
| Gallery captures | `docs/design/approval-experience/gallery/captures/macos/<fixture-id>.png`, with variants `<fixture-id>.dark.png`, `.baseline.png`, `.dialog.png` | Mac agent |
| Check screenshots | `docs/design/approval-experience/gallery/captures/macos/checks/M-<n>-<what>.png` | Mac agent |
| Manifest | `docs/design/approval-experience/gallery/captures/macos/manifest.json` | Mac agent |
| Measurements | A lab report, `docs/lab-reports/YYYY-MM-DD-mac-capture-runner.md` ([lab reports](../../lab-reports/README.md)) with an Observed row per check | Mac agent |
| Fixture `evidence` entries | `internal/approvalfixtures/fixtures/<fixture-id>.json` | A Linux-side agent, from the manifest, so the fixture files keep one writer and `go test` validates the result |

The manifest has the same shape as the Linux capture's, keyed by fixture id, one entry per capture:

```json
{
  "schema": 1,
  "platform": "macos",
  "captures": {
    "circleci-rerun-workflow-synthetic": [
      {
        "file": "captures/macos/circleci-rerun-workflow-synthetic.png",
        "renderer": "terminal-notifier 3",
        "tested": "visual",
        "provenance": {
          "os": "…", "daemon": "terminal-notifier", "daemon_version": "…",
          "theme": "light, Alerts", "scale": "2", "captured_at": "…", "commit": "…",
          "capture_method": "frame", "bundle_id": "…", "focus": "off"
        }
      }
    ]
  }
}
```

`file` is relative to the gallery directory, as `Evidence.File` requires. Each entry maps one to
one onto an `Evidence` value with `platform: "macos"` and `kind: "native-capture"`; a fixture with
no capture keeps its `missing` entry, with the reason. A terminal-notifier capture's `renderer` is
exactly `terminal-notifier 3`, the string each fixture's macOS `missing` entry carries: folding
replaces an entry by platform and renderer, so another spelling would leave the fixture both
missing and captured, which validation refuses. A baseline entry's `renderer` says which baseline
path drew it (alerter or the dialog), never just "baseline", and is added beside it.

When the report comes back, the `mac-handoff` skill's incoming steps apply: verify each claimed
failure, fix forward, fold the M-2 to M-9 outcomes into
[boundary-broker.md §6.2](../boundary-broker.md#62-macos) (its "first thing the macOS build
measures" and the `@CONTENTCLICKED` caveat), fold M-11 to M-13 into the gallery's baseline entries,
then retire the report.

## 6. Alternatives considered

| Alternative | Verdict |
| --- | --- |
| A hosted macOS CI runner for captures | Rejected for now: a CI runner has no logged-in user to authorize notifications or choose a style, so it measures the refusal paths at best. Revisit for regression captures once the helper app exists. |
| Driving presses only with AXPress | Rejected as the default: an accessibility action can take a path a mouse click does not. AXPress is the fallback, and the witness says when it was used. |
| Capturing the full screen always | Rejected as the default: it leaks the owner's desktop into a public file (AE-R6). Allowed only cropped, as a last resort. |
| Testing the baseline through a real yolo request and the owner's real hook | Rejected: it needs a live request store and approves nothing real by design. The stand-in plus stub `yolo` measures the same mapping without either. |
