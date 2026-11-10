---
title: "What approval toasts look like today, and the fixture set that lets us prove it"
date: 2026-11-09
status: draft
stage: DESIGN
next: "Run the Mac checks in mac-capture-runner.md, then land P-1 and P-2 in the toast notifier before any wording change"
depends-on:
  - README.md
  - ../boundary-broker.md
tags: [design, approvals, notifications, gallery, fixtures]
summary: "The native notification gallery sibling of the approval-experience umbrella: a shared, validated fixture format with provenance labels, real fixtures captured from yolo approve over real brokerstores, a generated gallery page that labels every rendering native, preview or missing, and a baseline of the owner's current toasts measured against the umbrella's five rules."
---

# What approval toasts look like today, and the fixture set that lets us prove it

**Status:** 2026-11-09. Fixture format, capture test and gallery generator built in
[`internal/approvalfixtures`](../../../internal/approvalfixtures/fixture.go); the baseline is
SOURCED from the owner's dotfiles and MEASURED where [the Linux capture runner](linux-notification-capture.md)
drove it. Every fixture that prompts has a native Linux capture, and no fixture has Mac
evidence.

> **In short.** The owner's toasts are not yolo's: a personal `on_request` script posts a
> three-button critical notification, and two of its behaviors break the umbrella's rules
> (dismissing it answers nothing, and its macOS fallback grants on Return). Fix the baseline's
> rule breaks first, then improve wording against captured evidence, using one fixture set
> whose every rendering says whether it is native, preview or missing.

**Why it matters.** Without a measured baseline, "the toasts are poor" cannot be located in
wording, truncation, the daemon, or the notifier's lifecycle, and an HTML mock-up would be
mistaken for proof.

**The shape.** A fixture set (one JSON file per representative request, labeled), a capture test
that grounds real fixtures in `yolo approve --json`, a generated gallery page, and two capture
runners (Linux headless, Mac by checklist) that add native evidence to fixtures.

**Start at [§2](#2-the-owners-current-toasts-the-baseline)** — the baseline and its findings.

**Needs your ruling:** None.

**Reads with:** [the umbrella](README.md) (rules AE-R1..R6 and the five rules this doc measures
against), [`linux-notification-capture.md`](linux-notification-capture.md) (how Linux captures are
made), [`mac-capture-runner.md`](mac-capture-runner.md) (the Mac checks),
[`boundary-broker.md` §6](../boundary-broker.md#6-the-doorbell-a-persistent-desktop-notification)
(the toast design this gallery measures toward).

---

## 1. The fixture set

**Fixture** — one sanitized representative approval request with its provenance labels and the
evidence of how it renders; the umbrella's *fixture set* is all of them. The format's reference is
the doc comment of [`fixture.go`](../../../internal/approvalfixtures/fixture.go), and `Validate`
enforces every rule below; [`fixture_test.go`](../../../internal/approvalfixtures/fixture_test.go)
runs it over every committed file, so a fixture that breaks a rule fails `go test`.

### 1.1 Where it lives and who reads it

| What | Where |
| :--- | :--- |
| Fixtures, one per file, named after the id | [`internal/approvalfixtures/fixtures/`](../../../internal/approvalfixtures/fixtures) |
| Format, `Load`, `Validate` | [`internal/approvalfixtures/fixture.go`](../../../internal/approvalfixtures/fixture.go) |
| Capture test (real fixtures) | [`internal/cli/approvalcapture_test.go`](../../../internal/cli/approvalcapture_test.go) |
| Gallery page and native captures | [`gallery/`](gallery/index.html) beside this doc |

The files are plain JSON so a consumer outside Go (the browser queue, a Mac runner) reads them
directly. A Go consumer calls `approvalfixtures.Load()`, which embeds the directory.

### 1.2 The labels

| Label | Values | Rule `Validate` enforces |
| :--- | :--- | :--- |
| `support` | `real`, `synthetic` | Real means yolo files this request today; synthetic is a future service or verb |
| `origin` | `captured`, `hand-written` | A real request is always captured, a synthetic one never; a captured one names the command it came from |
| `prompt` | `request`, `none` | `none` (policy runs it without asking) carries no request at all, so nothing can invent a prompt |
| `state` | `pending`, `allowed`, `denied`, `expired`, `refused`, `stale`, `decided-elsewhere`, `no-prompt` | The store's states plus the three outcomes a surface must handle |
| evidence `kind` | `native-capture`, `preview`, `missing` | Every fixture lists Linux and macOS evidence; a native capture names its file, what it tested and seven provenance keys; a browser rendering is never native; missing has no file and says why |
| evidence `tested` | `visual`, `action`, `lifecycle` | A preview tests only `visual` |

**Sanitization is positive, not a denylist.** Every workspace must be `/home/dev/code/<name>` and
every repository `example-org/<name>`; anything else fails validation. A denylist would have to
name what it hides.

### 1.3 Real fixtures stay real

The capture test files each real request through the brokers' own request construction into a
real brokerstore under a temporary home, runs the real `yolo approve` entry point, and writes the
request object it printed (and `brokerhook.Env`'s hook environment, and any decision's exit and
message) into the fixture, with ids and times normalized. Run without the update variable it
regenerates in memory and **fails when yolo's output has drifted from the committed fixture**, so
the gallery can never show a request shape yolo no longer prints:

```console
$ YOLO_UPDATE_APPROVAL_FIXTURES=1 go test -run TestApprovalCapture ./internal/cli/
$ YOLO_UPDATE_APPROVAL_FIXTURES=1 go test ./internal/approvalfixtures/   # regenerates the page
```

### 1.4 Coverage

Sixteen fixtures, against the umbrella's
[§4.3](README.md#43-representative-gallery-coverage) table. A real fixture's decision column is the
exit code `yolo approve` returned when the capture test decided it.

| Family | Fixture | Support | What it varies |
| :--- | :--- | :--- | :--- |
| Git | `git-push-working-set-miss` | real | Two refs pushed, one covered by the working set: the request lists only the uncovered ref |
| Git | `git-push-new-branch` | real | A ref that does not exist on the remote yet |
| Git | `git-push-force` | real | A non-fast-forward update |
| Git | `git-push-long-names` | real | Two refs with very long names, for wrapping and truncation |
| GitHub | `github-pr-comment` | real | A write whose content (the comment body) is long |
| GitHub | `github-workflow-run` | real | A workflow run on a branch other than the default |
| GitHub | `github-approve-run` | real | Approving a held workflow run, filed as a raw API call |
| CircleCI | `circleci-run-list` | real | The project-scoped read: **no prompt**, since policy runs it without asking |
| CircleCI | `circleci-rerun-workflow-synthetic` | synthetic | A rerun of failed jobs, which yolo does not mediate yet |
| S3 | `s3-delete-objects-synthetic` | synthetic | A destructive operation over several objects |
| Cross-cutting | `state-allowed-once` | real | Allowed once (exit 0) |
| Cross-cutting | `state-denied` | real | Denied (exit 0) |
| Cross-cutting | `state-expired` | real | Expired with no decision |
| Cross-cutting | `state-decided-elsewhere` | real | A second answer after the first (exit 3, already decided) |
| Cross-cutting | `state-stale` | real | Scope gone before the answer (exit 6, not confirmable) |
| Cross-cutting | `state-hook-failure` | real | The hook could not reach a session bus, so nothing was shown |

**Not covered yet.** The umbrella asks for several cases that no fixture has yet:

- push options;
- partially covered scope beyond the working-set miss;
- exact versus broader available actions on GitHub;
- CircleCI run-approval and pipeline-creation stories;
- unavailable actions;
- sensitive-data redaction.

Each needs either a source that files it or a synthetic fixture. The fixture format already
accepts all of them.

**Evidence today.**

- **Browser:** every fixture has a preview.
- **Linux:** the 15 fixtures that prompt have a native capture
  ([`linux-notification-capture.md`](linux-notification-capture.md)).
- **macOS:** every fixture lists its capture as missing.

## 2. The owner's current toasts: the baseline

### 2.1 What renders them

All SOURCED from the owner's dotfiles at commit `44bb526` unless marked MEASURED.

1. **The hook.** The owner's shared user config (not a work-tagged one) sets `on_request` to a
   personal shell script in `~/.local/bin`. yolo's host broker runs it once per filed request
   with the `YOLO_REQUEST_*` fields
   ([`boundary-broker.md` §6.6](../boundary-broker.md#66-one-store-several-front-ends-and-a-stable-surface-for-the-ones-yolo-does-not-ship)).
2. **The notify call (Linux).** `notify-send -u critical -a yolo -A once="Allow once" -A hour="Allow 1h" -A deny=Deny "yolo: <headline>" "<description markup>"`.
   The body is yolo's `DESCRIPTION`: the headline, then the agent's title and refs, one per line.
   The script deliberately omits the `SUMMARY` (the command, set, repository, digests).
3. **The daemon.** **swaync** (SwayNotificationCenter), a systemd user unit wanted by the sway
   session target; its config sets only `timeout: 3`, with the default stylesheet. **mako is not
   live**: its config file's only line says notifications moved to swaync.
4. **The answer.** `notify-send` blocks and prints the clicked action's key; the script maps
   `once`, `hour` and `deny` to `yolo approve <id>`, `--for 1h` and `--deny` through the `yolo`
   link the broker puts first on the hook's `PATH`. Anything else, including empty output,
   answers nothing.
5. **Decision elsewhere.** yolo stops the hook's process group with SIGINT, then SIGKILL after a
   grace ([`brokerhook.go`](../../../internal/brokerhook/brokerhook.go), `StopGrace`).
6. **macOS.** The same script uses `alerter` if installed, else an `osascript` `display dialog`
   with buttons Deny / Allow 1h / Allow once, **default button "Allow once"**, giving up after
   3500 seconds.

**What happens to a toast once it is posted.** These results are Linux only: swaync 0.12.6 and
libnotify 0.8.8, from [`linux-notification-capture.md` §4](linux-notification-capture.md#4-what-the-stack-measured),
whose rows are cited by letter.

| Event | What the toast does | What the hook answers |
| :--- | :--- | :--- |
| Button pressed | Closes | `once`, `hour` or `deny`, mapped to `yolo approve` (c) |
| User dismisses it (close, swipe, middle or right click) | Closes, reason 2 | **Nothing**; the request stays pending (b) |
| Body click | Closes, reason 2; no action is invoked | **Nothing** (d, SOURCED) |
| Decided elsewhere, expired, or broker stopped: yolo sends SIGINT to the hook | Withdrawn, reason 3 | Nothing, and the run is not recorded as a failure (e1) |
| Notifier killed (SIGKILL; SIGTERM by source) | **Stays on screen with live buttons** whose presses reach no one (e2) | Nothing |
| Time passes | A critical toast never leaves the screen by itself; the request's own expiry withdraws it through SIGINT (f) | — |
| Do Not Disturb on | A critical toast still shows (g) | — |

### 2.2 Findings against the five rules

The rules are the umbrella's [§3.1](README.md#31-five-rules-that-bind-every-sibling). Each finding
gives its evidence label. Most of these belong to the owner's personal script or to the daemon,
not to yolo. They are reported here so that the toast design in
[`boundary-broker.md` §6](../boundary-broker.md#6-the-doorbell-a-persistent-desktop-notification)
does not repeat them.

**Lifecycle**

- **B-1. Dismissal answers nothing.** Rule 3 says dismissal is Deny. MEASURED (b).
  - `notify-send` reports a dismissal exactly as it reports a toast it was never shown: empty
    output and exit 0. A script cannot tell the two apart, so it answers nothing.
  - The request then stays pending until it expires. Meanwhile, `yolo approve` in a terminal
    still offers it.
  - A body click ends the same way (d, SOURCED). It grants nothing, so it complies with rule 3
    on that count.
- **B-2. A killed notifier leaves an orphaned toast.** Rule 3 says a decision made anywhere
  withdraws stale actionable notifications. MEASURED for SIGKILL, SOURCED for SIGTERM (e2).
  - yolo's own stop sequence works: SIGINT to the process group withdraws the toast (e1).
  - Any other end of `notify-send` leaves the toast up with buttons that do nothing:
    - the SIGKILL after `StopGrace`, when SIGINT did not end the group;
    - a SIGTERM;
    - an out-of-memory kill.
  - A press on such a toast grants nothing, but it looks as though it answered.
- **B-3. A critical toast outlives its own attention span.** This is not a rule break: expiry is
  not consent, and yolo withdraws the toast when the request expires. MEASURED (f).
  - The toast stays on screen until a decision or the request's expiry.
  - Do Not Disturb does not hold it back (g).
  - The toast design should choose this behavior deliberately rather than inherit it.
- **B-4. The macOS fallback grants on Return.** This is a rule 3 hazard. SOURCED only; no Mac
  evidence exists.
  - The script's `osascript` dialog sets `default button "Allow once"`.
  - A keypress that was meant for another window can therefore grant.
  - The umbrella's [§4.2](README.md#42-native-constraints-are-part-of-the-design) also notes that a modal dialog is not a toast.
- **B-5. A refused click has no feedback.** This breaks rule 4. SOURCED from the script; the exit
  codes are MEASURED in the fixtures.
  - The script runs `yolo approve` and stops. On a refusal the user sees the toast disappear and
    nothing else:
    - exit 3, already decided (`state-decided-elsewhere`);
    - exit 5, policy;
    - exit 6, not confirmable (`state-stale`).
  - Only the store's `hook_failure` field records it.
- **B-6. Without a session bus, nothing is shown.** Rule 4 is honored: `yolo approve` shows the
  failure (`state-hook-failure`).
  - `notify-send` cannot reach a bus, the hook exits 1, and no toast appears.
  - Nothing falls back to another surface. That matches the umbrella's no-fallback rule, but the
    owner learns of it only from the CLI.

**Content.** All MEASURED from the Linux captures and the real fixtures.

- **C-1. A GitHub write omits its content.** `github-pr-comment` shows only "write to PR
  example-org/app#42". The comment body the agent would post appears only in the `SUMMARY`, which
  the script leaves out.
- **C-2. The headline is printed twice.** It is the title, and it is again the first line of
  `DESCRIPTION`. That uses one of a toast's few lines.
- **C-3. The toast does not say what each button grants or for how long the request lasts.**
  - "Allow 1h" does not say what it covers.
  - No request id, expiry or workspace is shown.
  - So a toast cannot be matched to a `yolo approve` line.
- **C-4. A force push looks like any other push.** `git-push-force`, `git-push-new-branch` and a
  fast-forward all read "push 1 ref to …" plus the ref. The old and new objects that tell them
  apart appear only in the command.
- **C-5. A partly covered push shows only the ref that asks.** In `git-push-working-set-miss` the
  toast lists `main`. The other ref, which goes through with the same push, appears only in the
  command.
- **C-6. Approving a held workflow run reads as a generic API call.** `github-approve-run` says
  "call the GitHub API on example-org/app", with the API path as its title. Nothing shows whose
  run it is, or whether its head is a fork.
- **C-7. A refusal message names an internal path.** `state-stale`'s refusal names the broker's
  scope file under the user's data directory, and a person has to read past it to reach the next
  step.
- **C-8. swaync wraps rather than truncates.** `git-push-long-names` shows both refs in full, at
  the cost of a toast about 280 pixels tall. Another daemon may cut the text off; that is
  unmeasured.

## 3. The gallery page

[`gallery/index.html`](gallery/index.html) is generated from the fixtures by
[`gallery.go`](../../../internal/approvalfixtures/gallery.go) and kept current by a test. Each tile
shows the request detail yolo printed, the decision and its exit code where the fixture has one,
and one slot per evidence entry:

- **native capture** — the image and its provenance table;
- **preview** — an HTML imitation of the owner's toast (title, body, the three buttons), stamped
  `PREVIEW (HTML, not native)`;
- **missing** — the words "No capture" and the reason, never an image.

Request text is untrusted (umbrella rule 2): the page is built with contextual escaping and carries
no script, which a test proves with markup in a headline.

## 4. Presentation changes the evidence justifies

Each change cites the finding that justifies it. **Rule fixes come before wording**, because a
better-worded toast that still leaves a dismissed request pending is still broken.

| # | Change | Where it lands | Justified by |
| :--- | :--- | :--- | :--- |
| P-1 | Treat a closed toast without an action as Deny. With `notify-send` this needs a notifier that sees the close reason (for example `gdbus` monitoring `NotificationClosed`), because `notify-send`'s output cannot tell a dismissal from a toast that was never shown | The toast notifier of [boundary-broker §6](../boundary-broker.md#6-the-doorbell-a-persistent-desktop-notification); the owner's script meanwhile | B-1 |
| P-2 | Withdraw on every stop: a notifier that holds the notification id and closes it on SIGTERM as well, or a broker that closes by id rather than relying on the notifier's signal handler | The toast notifier | B-2 |
| P-3 | No default button that grants; on macOS, a notification rather than a modal dialog | The owner's script; the Mac renderer | B-4 |
| P-4 | Report a refused click: a short follow-up notification carrying `yolo approve`'s message on exit 3, 5 or 6 | The toast notifier | B-5 |
| P-5 | Show the content being approved: the comment body (truncated, with a route to the full text), the workflow and ref, the run's actor and whether its head is a fork | Request descriptions in the GitHub broker; the notifier body | C-1, C-6 |
| P-6 | Say what each button grants: "Allow 1h" names its reach, and the body shows the request id, workspace and expiry | The notifier body | C-3 |
| P-7 | Mark force pushes and new branches in the description, and say when a push carries refs that need no approval | The git broker's request description | C-4, C-5 |
| P-8 | Drop the headline from the body's first line when the title already shows it | The notifier, or a `DESCRIPTION` variant without the headline | C-2 |
| P-9 | State the next step without internal paths in a not-confirmable refusal | `yolo approve`'s refusal text | C-7 |

**Proof.** Each change is proven by re-running the capture and lifecycle scripts against the
changed hook or notifier. Both before and after are then in the gallery. P-5, P-7 and P-9 change
yolo's own output, so the capture test regenerates the real fixtures and the change shows up as a
fixture diff.

## 5. What this does not cover

- **No renderer is built here.** The toast notifier of
  [`boundary-broker.md` §6](../boundary-broker.md#6-the-doorbell-a-persistent-desktop-notification)
  stays the design; this doc supplies its evidence.
- **No change to the owner's script.** Findings about it are reported to the owner; the dotfiles
  are theirs.
- **No Mac evidence.** [`mac-capture-runner.md`](mac-capture-runner.md) owns it.
- **No history or queue semantics.** The browser queue sibling reads the same fixtures.

## Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| NG-D1 | Fixtures live in a Go package directory and are embedded, so Go consumers need no path; the files stay plain JSON for everyone else | 2026-11-09 | [§1.1](#11-where-it-lives-and-who-reads-it) | ✅ |
| NG-D2 | Sanitization is an allowlist of spellings, not a denylist of secrets | 2026-11-09 | [§1.2](#12-the-labels) | ✅ |
| NG-D3 | Real fixtures are regenerated by a test that fails on drift, never edited by hand | 2026-11-09 | [§1.3](#13-real-fixtures-stay-real) | ✅ |
| NG-D4 | The gallery is a generated static page kept current by a test, not a front-end toolchain | 2026-11-09 | [§3](#3-the-gallery-page) | ✅ |
| NG-D5 | Rule breaks (P-1 to P-4) are fixed before any wording change, and every change is proven by a re-capture | 2026-11-09 | [§4](#4-presentation-changes-the-evidence-justifies) | ❌ |
