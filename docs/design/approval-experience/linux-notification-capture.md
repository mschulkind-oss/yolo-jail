---
title: "Capturing approval notifications on Linux, headless and reproducibly"
date: 2026-11-09
status: draft
stage: DESIGN
next: "Recapture the integrated production notifier candidate; resolve the swaync virtual-pointer control failure before upgrading its body-click finding"
depends-on:
  - README.md
  - native-notification-gallery.md
tags: [design, approvals, notifications, linux, capture]
summary: "The Linux notification capture sibling of the approval-experience umbrella: a headless sway, swaync and libnotify stack on a private session bus, built from nixpkgs inside the jail, that renders an on_request hook's toast for every fixture, screenshots it with provenance, and measures what the notifier and daemon do on dismissal, action, body click, withdrawal, expiry and Do Not Disturb."
---

# Capturing approval notifications on Linux, headless and reproducibly

**Status:** 2026-11-11. MEASURED: the original 14 swaync baseline captures remain;
14 additional real mako captures use a reconstruction of the **public documented Linux notify
call**, not a copy of the owner's private hook. A real protocol pointer body click on mako
closed the toast with reason 2, no action, empty notifier stdout and exit 0. Swaync input
remains unresolved: both body and button control attempts left the toast visible. Neither
baseline proves the forthcoming production notifier repair.


> **In short.** A real notification daemon, not an HTML imitation, renders each fixture's toast
> in a compositor with no screen. A private D-Bus session bus, a headless sway, swaync and
> `notify-send` all come from nixpkgs, so the stack needs no host desktop and no root. The same
> stack records the D-Bus traffic for every way a toast can end. That is how this doc found that
> dismissing the baseline toast answers nothing, and that a killed notifier leaves a live toast
> behind.

**Why it matters.** The umbrella's AE-R2 says HTML replicas are not native evidence. A Linux
capture is only evidence if a reader can rebuild the stack that made it, so this doc records the
whole stack.

**The shape.**

- One script captures every fixture that has a hook environment.
- A second script measures each lifecycle step against one daemon.
- Both write only into an output directory.
- [`approvalfixtures.Fold`](../../../internal/approvalfixtures/manifest.go) is the only path from
  their manifest into the fixtures.

**Start at [§4](#4-what-the-stack-measured).**

**Needs your ruling:** None.

**Reads with:**

- [the umbrella](README.md) [§4.2](README.md#42-native-constraints-are-part-of-the-design), for the native constraints this stack must honor.
- [`native-notification-gallery.md`](native-notification-gallery.md), which consumes the captures and the findings.
- [`boundary-broker.md` §6.1](../boundary-broker.md#61-linux), the toast design these measurements test against.

---

## 1. The stack

| Part | What | Version captured with |
| :--- | :--- | :--- |
| Session bus | `dbus-run-session` with the nix dbus package's own `session.conf` | dbus 1.16.2 |
| Compositor | sway, `WLR_BACKENDS=headless`, `WLR_RENDERER=pixman`, one output 1280x800 at scale 1, solid black background, no bar | sway 1.12 (wlroots 0.20.2) |
| Daemon | swaync with config `{"timeout": 3}` and the default stylesheet | SwayNotificationCenter 0.12.6, GTK 4.22.4 |
| Notifier | the hook under test, which calls `notify-send` | libnotify 0.8.8 |
| Screenshot | `grim` on the whole output, then `magick -trim` to the toast plus a 10-pixel border | grim 1.5.0 |
| Second daemon (visual and pointer) | mako, default theme, test `default-timeout=0`; earlier expiry measurements used `3000` | mako 1.11.0 |
| Pointer client | Generated persistent `zwlr_virtual_pointer_v1` client: absolute motion and primary button press/release; compositor roundtrip and daemon method-reply barriers | wayland 1.26.0; wlr-protocols 2022-09-05 |
| Font | DejaVu Sans, which swaync's closure brings in | — |

All of it comes from `nixpkgs` through `nix shell`, resolved at run time and not pinned. The
script records the store path of every tool it ran in
[`tools.txt`](gallery/captures/linux/tools.txt) and
[`linux/mako/tools.txt`](gallery/captures/linux/mako/tools.txt) next to the captures.
The pointer client needs `cc`, `wayland-scanner`, the Wayland development output and
`wlr-protocols`; its Nix output links and generated client stay in the durable run directory.

**Why sway and swaync.** The owner's desktop runs swaync under sway. sway, swaync, grim and
notify-send all worked on the first try, so neither cage nor weston was needed. A plain sway draws
no notification by itself. The umbrella's [§4.2](README.md#42-native-constraints-are-part-of-the-design) says so, and the daemon is what makes the toast.

**Two jail quirks the script handles.**

- `dbus-run-session` needs `--config-file` naming the nix package's `session.conf`, because the
  jail has no system dbus config.
- The `os` provenance is deliberately generic (`Linux userland in a container`): the kernel and
  distribution describe the capturing machine, while the daemon, toolkit and font versions are
  what determine the rendering.

## 2. Capturing the gallery

```console
$ scripts/approval-gallery/capture-linux.sh \
    --hook <the on_request script under test> \
    --fixtures internal/approvalfixtures/fixtures \
    --out docs/design/approval-experience/gallery/captures/linux \
    --run-dir <a short directory for sockets>
$ YOLO_UPDATE_APPROVAL_FIXTURES=1 go test ./internal/approvalfixtures/
```

The reproducible public-reference invocation for the second daemon is:

```console
$ scripts/approval-gallery/capture-linux.sh --reference-baseline --daemon mako \
    --fixtures internal/approvalfixtures/fixtures \
    --out docs/design/approval-experience/gallery/captures/linux \
    --run-dir <short durable directory>
```

`--reference-baseline` generates only the documented `notify-send` call in the run directory.
It does not reproduce the owner's answer mapping or stop traps. For a later candidate use
`--hook <candidate entry point> --hook-source <source/commit identity> --renderer <candidate label>`
instead, with a **distinct renderer label** so a repair cannot silently overwrite baseline evidence.
The runner replaces only that renderer's entries and preserves other baselines in the platform
manifest; mako PNGs and tool provenance are stored under `linux/mako/`. Use a new output tree
when capturing a different renderer on the same daemon, to keep its image paths distinct.
The hook receives fixture environment and the stub; it must reach the private bus without a real
approval store. Run only after the notifier lead's candidate is integrated. These scripts do not
build or prove that candidate themselves.

For each fixture that has a `hook_env` and whose request records no hook failure, the script:

1. Builds an environment holding only the fixture's `YOLO_REQUEST_*` values (one key at a time,
   because values can hold newlines), a throwaway `HOME`, and the bus, display and runtime
   variables the daemon needs, and runs the hook in it with `env -i`.
2. Puts a **stub `yolo` first on that `PATH`**. The stub only logs its arguments, and the run
   fails if the log is not empty, since nothing presses a button during a visual capture. The public reference never invokes it.
3. Takes screenshots until two consecutive frames are identical and not all black. It crops the
   frame to the toast.
4. Dismisses the toast the way a user would (`swaync-client --close-latest` or `makoctl dismiss`). It waits for the
   hook to exit, and sends it SIGINT if it is still running.
5. Writes one manifest entry. Each entry has the file, the renderer, `tested: visual`, a note that
   this is the default theme and not the owner's desktop (and, for a fixture whose state is not
   pending, that the outcome is not rendered), and the provenance below.

A fixture whose toast does not appear fails the run, and its image and entry are left out rather
than kept from an earlier run.

Two fixtures are skipped. `circleci-run-list` has `prompt: none` and no hook environment, and
`state-hook-failure` records a hook that could not reach a session bus, so neither showed a toast.
The fold test refuses a manifest that names a `prompt: none` fixture, and refuses a fixture whose
native capture no committed manifest lists, so a capture cannot appear where no toast was shown or
outlive the run that made it.

### 2.1 Provenance

The seven keys
[`Validate`](../../../internal/approvalfixtures/fixture.go) requires of every native capture:

| Key | Linux value | Why it matters |
| :--- | :--- | :--- |
| `os` | `Linux userland in a container` | Kept generic; the toolkit and font rows pin the rendering |
| `daemon`, `daemon_version` | `swaync`, `SwayNotificationCenter-0.12.6` | Button layout, wrapping and lifecycle differ by daemon and version |
| `theme` | swaync default CSS, GTK default, no gsettings theme | The owner's pixels can differ from these |
| `scale` | `1` | Wrapping and truncation depend on it |
| `captured_at` | UTC time of the run | — |
| `commit` | the yolo commit whose fixtures were rendered | It ties the picture to the request text |

The mako entries in the Linux manifest also record `hook_source`: a public-reference reconstruction identified by
the documented call and fixture commit `62b8bbda375acda7caa3f675a4844aa020e8b060`. It is not
the private owner hook or a candidate source identity. Its renderer is explicitly
`public reference baseline: documented notify-send call to mako`, with `tested: visual`.
All 14 entries are native pixels, not previews; macOS remains missing, and the two skipped
fixtures acquire no capture.

The Linux manifest adds `compositor`, `compositor_version`, `compositor_mode`, `libnotify_version`,
`resolution`, `fonts`, `crop` and `daemon_config`. A manifest is
[`ParseManifest`](../../../internal/approvalfixtures/manifest.go)'s format; the Mac runner writes
the same shape ([`mac-capture-runner.md`](mac-capture-runner.md)).

### 2.2 What a capture proves, and what it does not

A capture proves what a real daemon draws for this exact request text at this size. It does
**not** prove three things:

- **The owner's exact pixels.** Their theme, fonts, scale and dconf can differ.
- **What a button does.** Every capture is `tested: visual`.
- **Anything for a fixture whose hook text matches another's.** The `state-*` fixtures share
  request text with a pending fixture, so they render the same toast. A toast is posted once,
  when the request is filed, and what happens to it afterward is lifecycle, recorded in [§4](#4-what-the-stack-measured), not
  pixels.

## 3. Measuring the lifecycle

```console
$ capture-linux.sh … --lifecycle     # or, inside the stack's environment:
$ scripts/approval-gallery/measure-lifecycle.sh <swaync|mako> <out-dir> <run-dir>
```

The script posts the owner's exact `notify-send` call: critical urgency and three actions. It runs
`dbus-monitor` on `org.freedesktop.Notifications` and takes a screenshot before and after each
step:

| Step | What it does |
| :--- | :--- |
| s1 | Post the toast, and record the `Notify` call |
| s2 | Dismiss the toast as a user would |
| s3, s3b | Invoke "Allow once", then "Deny" |
| s4 | Try a body click |
| s5 | SIGINT the notifier |
| s6 | SIGKILL the notifier, then press a button on the toast that is left |
| s7, s7b | Wait past the timeout with a critical toast and with a normal one |
| s8 | Do Not Disturb (swaync only) |

## 4. What the stack measured

**Evidence labels.** **MEASURED** means observed in this stack's D-Bus log and screenshots, and
reproduced by re-running [§3](#3-measuring-the-lifecycle). **SOURCED** means read in the daemon's or libnotify's source at the
versions in [§1](#1-the-stack), with the file and line given.

Unless a row says otherwise, it is swaync 0.12.6 with libnotify 0.8.8.

| | Question | Answer | Label |
| :--- | :--- | :--- | :--- |
| a | What does the owner's call send? | Three actions (`once`, `hour`, `deny`), urgency 2 (critical), expire timeout -1. It sends no `default` action and no `resident` or `transient` hint. | MEASURED |
| b | What does a user's dismissal do? | The daemon emits `NotificationClosed` with reason 2 (dismissed). `notify-send` **prints nothing and exits 0**, so the hook answers nothing and the request stays pending until it expires. The close button, a swipe, and a middle or right click are all reason 2 (`notification.vala:177,218,235`). mako gives the same result. | MEASURED; the gesture list SOURCED |
| c | What does pressing a button do? | `ActionInvoked` with the key, then `NotificationClosed(2)`. `notify-send` prints the key and exits 0, then sends a redundant `CloseNotification`, which produces a second close with reason 3. | MEASURED |
| d | What does a swaync body click do? | A primary click runs `click_default_action`. This toast has no `default` action, so nothing is invoked. It is not resident, so it is dismissed with reason 2 and the hook answers nothing (`notification.vala:173-177,426,461-485`). The original run delivered no pointer. The expansion witnesses a persistent virtual pointer and an on-toast cursor, but neither a body click nor an action-button control produced a close/action. That failed control prevents a semantic claim; see [§4.1](#41-second-daemon-visuals-and-actual-pointer-evidence). | SOURCED only |
| e1 | What does SIGINT to the notifier do? (This is how yolo stops a hook.) | `notify-send` sends `CloseNotification`, the daemon emits reason 3, and the toast disappears. stdout is empty and the exit is 0. | MEASURED |
| e2 | What do SIGKILL or SIGTERM do? | **The toast stays on screen with live buttons.** Pressing "Allow once" on it emits `ActionInvoked` with no one listening. libnotify installs its SIGINT handler only after the notification is shown, and it has no SIGTERM handler (`notify-send.c:154-164,561-562`). | SIGKILL MEASURED; SIGTERM SOURCED |
| f | Does a critical toast expire? | **No.** With `timeout: 3` it is still on screen after 6 seconds, because `timeout-critical` defaults to 0 (`config.json.in:21`, `notification.vala:797-803`). A normal-urgency toast leaves the screen at 3 seconds but stays in the notification center with its buttons, and no close is signaled (`notiDaemon.vala:33-38`). mako, with the test timeout, expired even a critical toast with reason 1. | MEASURED |
| g | Does Do Not Disturb hide it? | Not a critical toast. It still shows and joins the center, while a normal toast is held in the center (`notiDaemon.vala:224-225`). DND engaged only with `GSETTINGS_BACKEND=keyfile`; the jail's default GSettings backend ignores the toggle. | MEASURED under keyfile; the bypass rule SOURCED |

The gallery doc weighs these against the umbrella's five rules
([`native-notification-gallery.md` §2.2](native-notification-gallery.md#22-findings-against-the-five-rules)).

### 4.1 Second-daemon visuals and actual pointer evidence

The [mako manifest](gallery/captures/linux/manifest.json) contains 14 independent **visual**
captures. This is no longer lifecycle-only evidence: for example the
[force-push toast](gallery/captures/linux/mako/git-push-force.png) is an actual mako-rendered PNG.
Mako's default appearance wraps the headline and body in a narrow blue toast; these are test
pixels, not the owner's theme. The actions are not drawn as swaync-style buttons in this theme.
No button behavior follows from that visual evidence.

Under the same private bus/headless compositor, the pointer measurement uses a persistent
protocol device, not a daemon CLI action. It positions in body whitespace at `(1240,55)` on
the 1280×800 output, then sends primary press/release. The mako test disables expiry so a close
cannot be mistaken for a three-second timeout. The generated client is compiled **before**
posting; the monitor waits for its `NameAcquired` signal, and success waits for a close signal,
not an elapsed-time budget. Polling delays only pace observation, and generous timeouts fail hangs.

| Daemon | Observed outcome | Evidence status |
| :--- | :--- | :--- |
| mako 1.11.0 | `NotificationClosed(id=1, reason=2)`, no `ActionInvoked`; notifier stdout empty, exit 0; toast gone | MEASURED: [before](gallery/captures/linux/mako/lifecycle/pointer_before.png), [after](gallery/captures/linux/mako/lifecycle/pointer_after.png) |
| swaync 0.12.6 | Persistent virtual pointer registered on `seat0` with pointer capability 1; cursor witnessed in toast body. Both body and first-action-button control remained visible without close/action before the failure guard | Input blocker, **not** measured dismissal semantics; row d remains SOURCED |

Raw logs are retained under the workspace durable directory at `approval62/pointer/` and
`approval62/control/`; the mako `pointer-result.txt`, `pointer.dbus`, `pointer.cmd` and PNGs
record the successful run. The swaync logs and `pointer-delivered.png` record failed input
controls. Next step for swaync is to verify toolkit input delivery with a working button control
(or use a different private compositor backend), then repeat the body test. Do not infer an
unchanged/no-op semantic outcome from a failed positive control.

Inside the stack's private bus and tool environment, repeat the bounded pointer check with:

```console
$ scripts/approval-gallery/measure-lifecycle.sh mako <durable evidence dir> <short durable socket dir> --pointer-only
$ scripts/approval-gallery/measure-lifecycle.sh swaync <durable control dir> <short durable socket dir> --pointer-action
```

The mako screenshots inherit the version/theme/scale/source identity above, but measure the
public lifecycle test request (`rerun failed jobs`), not a fixture's final state. They are not
folded as additional fixture visuals. All observations are Linux container/private-GUI evidence;
none is Mac or rootless-host reachability proof.

## 5. Limits

- **Two daemon test themes captured.** swaync is the owner's daemon, while mako visuals and
  pointer measurements use a test config rather than the owner's config. dunst, GNOME Shell and KDE Plasma are
  unmeasured, and the umbrella's [§4.2](README.md#42-native-constraints-are-part-of-the-design) table about them remains source-backed context.
- **Swaync pointer control unresolved.** Mako has a measured real body click, but row d for
  swaync still rests on source because its button control failed. A persistent virtual device
  fixes the absent capability, not necessarily GTK input delivery.
- **Not the owner's pixels.** Every capture carries that note. A capture on the owner's own
  desktop would need a host-side run, which this work does not perform (AE-R6).
- **Unpinned versions.** Each run records its versions. A later run may draw differently, and
  its provenance will say so.

## 6. What this does not cover

- **macOS.** That is [`mac-capture-runner.md`](mac-capture-runner.md). A Linux rendering never
  counts as Mac proof.
- **A renderer.** This stack captures whatever the hook posts. It ships no notifier.
- **CI.** The scripts need nix and a few hundred megabytes of closure. They are run by hand when
  fixtures or the hook change, and the fold test keeps the fixtures in step with whatever
  manifest is committed.

## Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| LC-D1 | Headless sway plus swaync on a private session bus, from nixpkgs, with no host desktop | 2026-11-09 | [§1](#1-the-stack) | ✅ |
| LC-D2 | The hook runs against a stub `yolo` that only logs, so a capture can never decide a request | 2026-11-09 | [§2](#2-capturing-the-gallery) | ✅ |
| LC-D3 | Runners write a manifest and never a fixture; `Fold` is the single writer, and a test fails while a manifest is unfolded | 2026-11-09 | [§2.1](#21-provenance) | ✅ |
| LC-D4 | Each lifecycle row is labeled MEASURED or SOURCED; a source reading is never presented as a measurement | 2026-11-09 | [§4](#4-what-the-stack-measured) | ✅ |
