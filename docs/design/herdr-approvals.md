---
title: "Approvals in herdr for an owner who is not at the host: herdr rings the bell where the owner's terminal is, and one key answers it"
date: 2026-11-26
status: in-review
stage: DESIGN
next: "Rule OQ-HA1 to OQ-HA4; slice 0 in section 8 needs no ruling and no code and works for the remote owner today"
depends-on:
  - ../research/herdr-integration.md
  - boundary-broker.md
tags: [design, herdr, approvals, broker, on_request, terminal, host, remote, notifications]
summary: "yolo's approval hook runs on the host, so its desktop toast is never seen by an owner who reaches the host over ssh. herdr already forwards what it raises itself to the terminal its client is attached to, and drops what a pane prints (MEASURED), so the design drives herdr's own surfaces from the host: a herdr notification is the alert, a pane token and state label are the marker, and a herdr popup key running yolo approve on the host is the answer. Every approval stays a human keystroke on the host; nothing of herdr's crosses into a jail and nothing a jail writes can raise the alert."
---

# Approvals in herdr for an owner who is not at the host: herdr rings the bell where the owner's terminal is, and one key answers it

**Status:** 2026-11-26; design, nothing built but the hook-environment strip of
[§3.4](#34-the-hook-reaches-the-right-herdr). herdr is SOURCED at tag `v0.9.5` (commit `2ecf19a92`, its
latest release) and MEASURED by running that release's Linux binary headless
([§2.3](#23-what-herdr-does-with-a-notification-measured)). yolo is read at `95f02a27a`.

> **In short.** The owner is on the far end of an ssh, where a toast raised on the host is never
> seen. herdr knows where the owner's terminal is and carries to it what herdr itself raises, never
> what a pane prints, so yolo asks herdr to raise the alert and mark the pane, and a herdr key runs
> `yolo approve` on the host to answer.

**Why it matters.** The hook's desktop notification "pops up on the desktop of the machine the broker
runs on, which may be unattended" ([`approvals.md:219`](../../userguide/guides/approvals.md)). A remote
owner's agent sits blocked on a `git push` until the request expires, and nothing in the owner's
terminal says so.

**The shape.** Three parts, all on the host: the **alert**, one herdr notification delivered by herdr's
client to the owner's own terminal or desktop ([§4](#4-the-alert)); the **marker**, a token and label
on the pane that asked ([§3](#3-the-jails-pane-identity-and-the-marker)); the **answer**, a herdr key
that runs `yolo approve` on the host ([§5](#5-answering)).

**Cost.** One verb group (`yolo herdr`), one `yolo approve` flag, two tokens on a herdr call the launcher
already makes, one JSON field. No new daemon or channel into a jail, and no change to herdr.

**Start at [§2.3](#23-what-herdr-does-with-a-notification-measured):** what herdr forwards decides which
channels exist at all.

**Needs your ruling:** [OQ-HA1](#OQ-HA1), [OQ-HA2](#OQ-HA2), [OQ-HA3](#OQ-HA3), [OQ-HA4](#OQ-HA4).

**Reads with:** [`herdr-integration.md`](../research/herdr-integration.md) (the launcher's herdr report;
its HR-D2, HR-D4 and the HR-D10 ruling, that herdr integration works with host management on and needs
no herdr change, bind here), [`approvals.md`](../../userguide/guides/approvals.md) (the surface this
fronts), [`boundary-broker.md`](boundary-broker.md) (the store and `on_request`),
[`linux-approval-notifier.md`](approval-experience/linux-approval-notifier.md) (the desktop doorbell this
chains with).

---

## 1. Terms

- **Request.** One pending approval the host broker filed for a jail's `git push` or `gh` write (or
  any brokered source), with an id `r-…`, answered by `yolo approve`. A jail may have at most three
  pending at once ([`limits.go:40`](../../internal/brokerstore/limits.go)).
- **`on_request` hook.** The command the user's config names, run once per request, as the user, on the
  host. It has no terminal (its output goes to a buffer,
  [`brokerhook.go:386`](../../internal/brokerhook/brokerhook.go)) and its stdin is the request JSON. yolo
  sends it SIGINT when the request stops waiting and SIGKILL two seconds later; the kernel SIGKILLs it
  outright if the broker dies ([`procattr_linux.go:9`](../../internal/brokerhook/procattr_linux.go)).
- **Jail id.** The 8-hex key a request carries as `YOLO_REQUEST_JAIL`: `paths.JailShortHash` of the
  container name, which is derived from the resolved workspace path on every backend, macos-user
  included.
- **Outer terminal** *(coined here)*. The terminal emulator the owner is actually looking at. Under
  herdr it is the one herdr's attached client draws into, which is not the pane's pty.
- **herdr client** and **delivery mode** *(herdr's)*. The client is the process that draws herdr into an
  outer terminal. It runs on the host when the owner runs `herdr` in an ssh session, and on the owner's
  own machine for `herdr --remote` and saved machines. The delivery mode is the client's
  `[ui.toast] delivery` setting: `off`, `herdr` (an in-app toast), `terminal` (an escape sequence to the
  outer terminal) or `system` (the client machine's desktop notification service).
- **Pane token** and **state label** *(herdr's)*. Display-only values a script sets on a pane with
  `herdr pane report-metadata`: a token `--token NAME=VALUE` renders in the sidebar as `$NAME`; a state
  label `--state-label STATUS=TEXT` replaces herdr's state word.
- **Alert** *(coined here)*. The one message per request that tells the owner a request is waiting. It is
  a doorbell: it names the request and the command that answers it, and it is never a button.
- **Marker** *(coined here)*. The token and label on the pane that asked, which stay visible until the
  request is answered. The alert is momentary; the marker is what the owner finds later.
- **Picker** *(coined here)*. The interactive `yolo approve --pick`: a host-only terminal UI over the
  pending requests that runs `yolo approve` for the row you choose. It is not a daemon and not a herdr
  pane.

## 2. What the new fact changes

### 2.1 The path today

`on_request` names one command, in user config only
([`onrequest.go:52`](../../internal/config/onrequest.go)). The shipped notifier for it,
`yolo internal approval-notify`, posts a D-Bus notification on the session bus named by the broker's
own `DBUS_SESSION_BUS_ADDRESS` and refuses to guess another
([`notifier.go:96`](../../internal/approvalnotify/notifier.go)). Two cases follow for a remote owner:

| The broker was started from | What the owner gets |
| :--- | :--- |
| An ssh session, with no session bus | Nothing posted; the request records `your on_request failed, so you may not have been notified` ([`approve.go:872`](../../internal/cli/approve.go)) |
| The host's own desktop session | A toast on the host's screen, with buttons nobody is there to press |

The second case fails silently, which is the worse one: nothing in yolo can tell a toast on an
unattended desktop from one on an attended desktop.

### 2.2 The four channels, and which one this design is

| Channel | Reaches a remote owner | Verdict |
| :--- | :--- | :--- |
| Desktop notification on the host | No | Stays as the fallback ([OQ-HA2](#OQ-HA2)) |
| Write an escape sequence into the asking pane's tty | No under herdr: herdr drops it ([§2.3](#23-what-herdr-does-with-a-notification-measured)). A bare terminal would show it, but it lands inside a TUI's own drawing | Rejected |
| Ask herdr to raise a notification | Yes: herdr's client emits it to its own outer terminal or desktop | **This design** |
| Write an escape sequence to the owner's outer terminal directly | Only where yolo can name that tty: a tmux client, not a herdr client | Deferred ([OQ-HA4](#OQ-HA4)) |

### 2.3 What herdr does with a notification (MEASURED)

herdr is a server holding the panes and a client drawing them. The server decides *that* a
notification is due and sends it to every attached client; **the client decides how to deliver it**,
from the client's own config ([`notification_policy.rs:207-246`](https://github.com/herdrdev/herdr/blob/v0.9.5/src/client/shell/notification_policy.rs#L207-L246)).
herdr's own docs: `terminal` is "an outer-terminal notification that also works over SSH"
([`configuration.mdx:495`](https://github.com/herdrdev/herdr/blob/v0.9.5/docs/next/website/src/content/docs/configuration.mdx#L495)).

MEASURED with the v0.9.5 release's Linux binary (sha256 `14d9e8bc…`, equal to the release's published
digest), headless in a scratch home: a client in a pty (`[ui.toast] delivery = "terminal"`), the
pty master read for every byte the client wrote.

| Probe | Result |
| :--- | :--- |
| A pane's process prints `ESC ] 9 ;`, `ESC ] 777 ; notify`, `ESC ] 99 ;` and BEL | **None reaches the client's output**, under `TERM` `xterm-ghostty`, `xterm-kitty`, `xterm-256color`, and `TERM_PROGRAM=iTerm.app`. The text never appears. |
| `herdr notification show "t" --body b` with `TERM=xterm-ghostty`, or `TERM_PROGRAM=iTerm.app` | The client writes `ESC ] 9 ; t: b ESC \` |
| The same with `TERM=xterm-kitty` | The client writes `ESC ] 99 ; i=1:d=0 ; t` and `ESC ] 99 ; i=1:p=body ; b` |
| The same with `TERM=xterm-256color` and no `TERM_PROGRAM` | **Nothing is written, and the API still answers `shown: true`** |
| The same with the client's `delivery = "off"` | Nothing is written; the API still answers `shown: true` |
| `notification show` with no client attached | `no_foreground_client`, or `disabled` when the server's own delivery is `off` |
| `pane report-agent … --state blocked` on a pane in a background tab, source `yolo-jail` | Pane state reads `blocked`; the client writes `ESC ] 9 ; claude needs attention: <workspace> · 1 · 1` |
| `--state unknown` afterwards | State returns to `unknown` |
| `report-metadata` with a token and `--state-label working=…` and `unknown=…`, `--ttl-ms 2500` | Both appear on the pane, and **both are gone after the TTL** |

What that means, with the source that explains each:

1. **A pane cannot ring the owner's terminal under herdr.** The pane's bytes go through herdr's terminal
   emulator, which sets callbacks for size, bell, working directory, clipboard write, colour scheme and
   pty writes, and none for the desktop-notification option its library offers
   ([`lib.rs:925-961`](https://github.com/herdrdev/herdr/blob/v0.9.5/crates/ghostty-vt/src/lib.rs#L925-L961),
   [`:1151`](https://github.com/herdrdev/herdr/blob/v0.9.5/crates/ghostty-vt/src/lib.rs#L1151)).
   It does not matter which pane or whether the agent is busy drawing: the answer is the same for every
   pane, so yolo never writes into one.
2. **herdr emits two dialects**, chosen by the client's environment: OSC 9 for Ghostty, iTerm2 and
   WezTerm, OSC 99 for kitty, nothing for any other terminal
   ([`terminal_notify.rs:11-56`](https://github.com/herdrdev/herdr/blob/v0.9.5/src/terminal_notify.rs#L11-L56)).
   The client reads `TERM_PROGRAM`, `KITTY_WINDOW_ID` and `TERM`. ssh sends `TERM` by default and
   `TERM_PROGRAM` only when both ends are configured to (INFERRED from OpenSSH's `SendEnv`/`AcceptEnv`,
   not measured), so a Ghostty, iTerm2 or WezTerm user whose `TERM` is `xterm-256color` gets nothing
   through a plain ssh. `herdr --remote` runs the client on the owner's machine, where all three are
   detected.
3. **`shown: true` means herdr handed the message to a client, not that a human saw it.** yolo cannot
   learn that from herdr, so the alert is never the only evidence of a request: the marker and
   `yolo approve` are ([§3](#3-the-jails-pane-identity-and-the-marker)).
4. **A herdr notification raised through the API is never suppressed for the active tab.** The
   suppression applies to a notification that names a pane's tab or workspace; a `notification show`
   names neither ([`notification_policy.rs:196`](https://github.com/herdrdev/herdr/blob/v0.9.5/src/client/shell/notification_policy.rs#L196),
   [`:295-312`](https://github.com/herdrdev/herdr/blob/v0.9.5/src/client/shell/notification_policy.rs#L295-L312); SOURCED).
   An agent blocked on a broker is not showing an approval screen, so the owner who is looking at that
   very pane still needs the alert.
5. **herdr's in-app toast lasts five seconds**
   ([`notification_policy.rs:15-22`](https://github.com/herdrdev/herdr/blob/v0.9.5/src/client/shell/notification_policy.rs#L15-L22))
   and the API raises at most one a second
   ([`api.rs:19`](https://github.com/herdrdev/herdr/blob/v0.9.5/src/app/api.rs#L19)). A request waits
   minutes, so the marker, not the toast, is what stays.

### 2.4 Which attach mode the owner is in

herdr has three ways to reach a server, and they differ in exactly the two things this design
touches:

| The owner attaches by | The client runs on | Set `[ui.toast] delivery` in | `prefix+a` must be configured in |
| :--- | :--- | :--- | :--- |
| ssh to the host, then `herdr` | the host | the host's herdr config: `terminal` if the terminal is detected ([§2.3](#23-what-herdr-does-with-a-notification-measured) item 2), else `herdr` | the host's config |
| `herdr --remote host` | the owner's machine | the owner's local config: `system` or `terminal` | the **host's** config, with `--remote-keybindings server`: "local custom command keybindings are not sent, because those commands would run on the remote host" ([`persistence-remote.mdx:59`](https://github.com/herdrdev/herdr/blob/v0.9.5/docs/next/website/src/content/docs/persistence-remote.mdx#L59)) |
| a saved machine in a local herdr | the owner's machine | the owner's local config | the host's config; the server's custom commands "still run there" ([`connecting-machines.mdx:112`](https://github.com/herdrdev/herdr/blob/v0.9.5/docs/next/website/src/content/docs/connecting-machines.mdx#L112), SOURCED, not measured) |

herdr's config is the user's, not yolo's: yolo never writes it
([`yolo herdr check`](#44-yolo-herdr-check) prints the lines to add).

## 3. The jail's pane identity, and the marker

**The match is exact, and it needs no new state.** A request's jail field is
`paths.JailShortHash(sf.Container)` ([`approvalauthority.go:111`](../../internal/gitbroker/approvalauthority.go)),
and `sf.Container` is the name the launcher computes above the backend dispatch
(`runtime.FromWorkspace`, [`run.go:156`](../../internal/cli/run/run.go)) and writes into the scope file
([`brokeredscope.go`](../../internal/cli/run/brokeredscope.go)). An attach computes the same name from the
same workspace, so one jail id covers every session of a jail.

### 3.1 The launcher adds two tokens

The launcher already labels the pane under its signal arm
([`herdragent.go:125`](../../internal/cli/run/herdragent.go)); every herdr call it makes carries
`--source yolo-jail --agent <agent>` ([`herdragent.go:152`](../../internal/cli/run/herdragent.go)). It
adds two tokens to that call:

```text
herdr pane report-metadata <pane> --source yolo-jail --agent claude --applies-to-source yolo-jail \
  --title "🔒 JAIL yolo-jail" --token yolo_jail=<jail id> --token yolo_project=yolo-jail
```

- `yolo_jail` is the request's jail id, byte for byte. `yolo_project` is the label's project name.
- herdr documents that tokens are not guarded by `--agent`, so the release clears them explicitly
  (`--clear-token`), on the path that clears the title. A SIGKILLed launcher leaves display-only tokens
  that die with the pane ([HA-D1](#HA-D1)).
- Finding a jail's panes is `herdr pane list` (JSON, MEASURED to carry each pane's `tokens`) filtered on
  `tokens.yolo_jail` by the caller. Zero panes is normal: the jail was launched outside herdr.

### 3.2 The marker

While a jail has a request pending, each of its panes carries one more token and a label, written under a
second source so the launcher's own source stays untouched:

```text
herdr pane report-metadata <pane> --source yolo-approvals --applies-to-source yolo-jail \
  --token yolo_req=r-3f9a --state-label idle=… --state-label working=… --state-label blocked=… \
  --state-label done=… --state-label unknown=…  --ttl-ms <ms until expiry> --seq <unix ms>
```

- **The label is `awaiting approval r-3f9a`, or `awaiting approval r-3f9a +2` when two more are pending
  for the jail.** The id shown is the oldest, the one the agent is most likely blocked on.
- **The label rides on all five states.** A jail agent waiting on the broker is blocked inside a tool
  call, and what its screen reads as to herdr's rules (`working`, `idle` or `unknown`) is not known
  without running the agent (INFERRED). The label is display-only and leaves with the marker, so labelling
  every state costs nothing.
- **`--ttl-ms` is the request's remaining life**, at most herdr's 86 400 000 ms. MEASURED: a TTL
  removes the token and every label. A TTL is the cleanup that survives a SIGKILLed hook (the broker
  dying kills it), so a stuck marker cannot outlive its request.
- `--applies-to-source yolo-jail` retires the label with the launcher's report even if a clear is lost;
  MEASURED: a label written with a different guard source is not stored.
- The pane's row needs `$yolo_req` in `[ui.sidebar.agents] rows`; `yolo herdr check` prints the line.

### 3.3 Several requests, one jail

Each request has its own hook and all write the same token under one source. Every write is computed
from the store (`yolo approve --json`), so concurrent writers agree on the value, and `--seq` (unix
milliseconds) orders them ([HA-D3](#HA-D3)). herdr accepts sequenced token reports from at most 32
distinct sources per pane for its lifetime; this design uses two.

### 3.4 The hook reaches the right herdr

The hook inherits the broker's environment, and the broker outlives the pane it was started from, so the
runner strips pane identity (`HERDR_PANE_ID`, `HERDR_TAB_ID`, `HERDR_WORKSPACE_ID`, `TMUX_PANE`,
`ZELLIJ_PANE_ID`, `WEZTERM_PANE`, `KITTY_WINDOW_ID`) and keeps session identity (`HERDR_SOCKET_PATH`,
`HERDR_SESSION`)
([`brokerhook.go:225`](../../internal/brokerhook/brokerhook.go), BUILT, documented in
[`approvals.md`](../../userguide/guides/approvals.md)). The herdr used is the one on the hook's `PATH`,
as the launcher's arm does. **A broker started outside herdr has no session variable**, so the hook
reaches herdr's default session; an owner with a named session must start the broker from inside it, and the
"no herdr server" line of [§6](#6-every-stop-names-its-next-step) says so ([HA-D4](#HA-D4)).

## 4. The alert

### 4.1 The notifier

**The stock notifier is `yolo herdr on-request`**, a host-only verb a user names in `on_request`. It reads
its environment and `yolo approve --json`, never stdin, so a command chained after it still receives the
request JSON:

1. **Raise the alert**: `herdr notification show "yolo: <headline>" --body "run: yolo approve <id>"
   --sound request`. The text is `YOLO_REQUEST_HEADLINE`, which yolo builds and validates, and the id; no
   other field of the request is used ([§7](#7-security)).
2. **Read herdr's answer.** `shown: true`: go to step 3. `rate_limited`: wait 1.1 s and try once more
   (two requests filed together). Any other reason, or no herdr: print one line naming it and **exit 3
   at once**, so a chained fallback runs ([§4.2](#42-one-approval-one-alert)).
3. **Mark the panes** ([§3.2](#32-the-marker)) that carry the jail's token. None is normal, and then the
   verb exits 0 here: there is nothing to clear.
4. **Wait** for SIGINT, which yolo sends when the request leaves pending.
5. **Clear or recompute** with one herdr call inside yolo's two-second grace: no request left for the jail,
   `--clear-token yolo_req --clear-state-labels`; otherwise rewrite the value for those left.

`--sound request` plays on the herdr client's machine ([`configuration.mdx:503`](https://github.com/herdrdev/herdr/blob/v0.9.5/docs/next/website/src/content/docs/configuration.mdx#L503));
for an ssh-attached client that is the host, which makes no difference to anyone and no harm.

Each herdr call is bounded by 2 s, as the launcher's are. Steps 1 to 3 run once per request.

### 4.2 One approval, one alert

Three mechanisms, each of which exists today or is built here:

1. **One hook per request.** A broker lifetime runs the hook once per request it filed
   ([`brokerhook.go:300`](../../internal/brokerhook/brokerhook.go): a request already running is skipped),
   and every request has exactly one runner however many jails are up.
2. **One alert producer per request**, by chaining in a fixed order. `yolo herdr on-request` exits 3 the
   moment herdr accepted nothing (no client attached, delivery disabled, herdr missing), and the desktop
   notifier runs only then:

   ```toml
   on_request = 'yolo herdr on-request || exec yolo internal approval-notify'
   ```

   An owner who wants no fallback writes the verb alone; one who wants both
   ([OQ-HA2](#OQ-HA2)) writes `yolo herdr on-request & exec yolo internal approval-notify`.
3. **One alert kind per request.** The verb raises a notification and does not also report a `blocked`
   state, because herdr would then raise its own "needs attention" as well
   ([OQ-HA3](#OQ-HA3)).

Nothing withdraws an alert: an OSC 9 notification cannot be recalled and herdr's toast expires on its own.
An owner who answers elsewhere and later sees the alert runs `yolo approve`, which exits 3 ("already
decided"). The alert says so by naming the id.

### 4.3 Remote detection: none, by design

The alert's channel is not chosen by guessing where the owner is. The signals available all fail:

| Signal | Why it cannot route |
| :--- | :--- |
| `SSH_CONNECTION` in the hook's environment | The hook inherits the broker's, which belongs to whichever launch started the broker, possibly long before and possibly from a different session than the owner's present one |
| `DISPLAY`, `WAYLAND_DISPLAY`, `DBUS_SESSION_BUS_ADDRESS` | Present on an unattended desktop too, and they say nothing about who is looking |
| Whether herdr's client is remote | herdr's API names no client count or location (searched `src/api` and `src/cli` at v0.9.5); the one client fact it returns is the `no_foreground_client` reason |

What *is* observable is the outcome: herdr says whether any client received the alert
(`shown`, `no_foreground_client`, `disabled`), and that is what the chain in [§4.2](#42-one-approval-one-alert) keys on. Whether the
owner's *terminal* then displays it is the client's delivery setting and terminal detection
([§2.3](#23-what-herdr-does-with-a-notification-measured)), which yolo cannot see from the host and
[§4.4](#44-yolo-herdr-check) proves by ringing once.

### 4.4 `yolo herdr check`

A host-only verb that makes no persistent write and never edits herdr's config. It reports, in order:
herdr found on `PATH`; a server reachable at the socket herdr would resolve; the sidebar row for
`$yolo_req` present in the host's herdr config; the `prefix+a` popup present in it; and the **terminal
facts** (`TERM`, `TERM_PROGRAM`, `KITTY_WINDOW_ID`) with whether herdr names a notification backend for
them. Those three are read from the process's own environment, so the owner runs it in the terminal that
starts the herdr client, not in a pane (a pane's environment is the server's, not the client's).
`yolo herdr check --ring` raises one test alert, `yolo: test alert, no request pending`, so the owner can
see whether it arrives; it is the only proof a display happened.

## 5. Answering

**Every approval is a keystroke on the host.** The alert and marker only say what to type; nothing is
decided by a click on a notification, and no approval surface is reachable from a jail.

### 5.1 Without a new picker

The alert carries the command (`run: yolo approve r-3f9a`). herdr's popup runs on the host and takes any
command ([`configuration.mdx:204`](https://github.com/herdrdev/herdr/blob/v0.9.5/docs/next/website/src/content/docs/configuration.mdx#L204)),
so the owner's host herdr config can hold:

```toml
[[keys.command]]
key = "prefix+a"
type = "popup"
command = "yolo approve; exec \"${SHELL:-sh}\""
```

The popup lists every pending request (`yolo approve`) and leaves a shell on the host in which the owner
types `yolo approve r-3f9a`, adding `--for 15m` or `--deny` as wanted. It needs no yolo change.

### 5.2 The picker

`yolo approve --pick [--jail <id> | --pane <herdr pane id>]` is a full-screen terminal UI over the same
store `yolo approve` lists, refreshed live from `yolo approve --watch --json`. It is host-only and works
in any terminal; the popup above is one way to open it.

| Key | Does | Runs |
| :--- | :--- | :--- |
| `↑` `↓` | move between pending requests; the detail pane shows headline, project, jail, repository, command, refs needing approval, expiry, and a failed hook's line | none |
| `Enter` or `o` | allow once | `yolo approve r-…` |
| `f` | allow for a duration, prompted, default `15m` | `yolo approve r-… --for <d>` |
| `a`, `r` | add to the working set; remember in the project's config | `--add`, `--remember` |
| `d` | deny | `yolo approve r-… --deny` |
| `g` | jump to the pane that asked (the first pane carrying the jail's token) | `herdr agent focus <pane>` |
| `q`, `Esc` | close | none |

- **Keys come from the request, not the picker.** `yolo approve --json` grows an `answers` list per request
  (`once`, `for`, `add`, `remember`, `deny`), the set the request's source accepts today (no such field
  exists yet); a key whose answer is absent is not offered. When a source retires a flag, as the GitHub
  design does for `--add` ([OQ-GW2](github-write-scopes.md#decision-ledger)), the picker follows with no
  change ([HA-D5](#HA-D5)).
- **Each answer is one `yolo approve`**, with its checks, refusals and audit; the picker shows the verb's
  output verbatim and never retries. One key answers one request, never several.
- **Filtering.** `--jail` keeps one jail's requests. `--pane` reads the pane's `yolo_jail` token with
  `herdr pane get` and filters on it; a pane with no token says so and shows every request.
- **In herdr**, the key becomes `command = "yolo approve --pick --pane \"$HERDR_ACTIVE_PANE_ID\""`. A
  popup is not a herdr pane and gets no `HERDR_PANE_ID`, so the underlying pane comes from
  `HERDR_ACTIVE_PANE_ID`.

### 5.3 Where the key lives

The popup runs on whichever machine owns the config that defines the key, so for an owner on
`herdr --remote` or a saved machine it must be in the host's config and the client must take the server's
keybindings ([§2.4](#24-which-attach-mode-the-owner-is-in)). `yolo herdr check` names the attach mode's
requirement; a key defined only in the owner's local config would run on their laptop, where `yolo
approve` finds no broker and says so.

## 6. Every stop names its next step

| The stop | The line, and the step it ends with |
| :--- | :--- |
| `herdr` not on `PATH` (notifier) | `yolo herdr: herdr is not on PATH, so no alert was raised; install herdr, or remove "yolo herdr on-request" from on_request` — exit 3 |
| No herdr server | `yolo herdr: no herdr server at <socket>; start herdr, or set HERDR_SESSION where yolo is launched` — exit 3 |
| `no_foreground_client` | `yolo herdr: no herdr client is attached, so the alert had no one to reach; attach with herdr, or chain a fallback: on_request = 'yolo herdr on-request \|\| exec yolo internal approval-notify'` — exit 3 |
| `disabled` | `yolo herdr: herdr's toast delivery is off; set [ui.toast] delivery in the config of the machine that runs your herdr client` — exit 3 |
| `rate_limited` twice | `yolo herdr: herdr limits alerts to one a second; this request has no alert, see yolo approve` — exit 3 |
| `shown` but nothing displayed | not detectable by the hook. Run `yolo herdr check --ring` in the terminal that starts herdr; when herdr names no backend for its `TERM` and `TERM_PROGRAM`, it says `herdr cannot raise a terminal notification here; set [ui.toast] delivery = "herdr", or start herdr with TERM_PROGRAM set` |
| No pane carries the jail | `yolo herdr: no herdr pane runs jail <id> (launched outside herdr?); alert raised, nothing to mark` — exit 0 |
| Token set but invisible | `yolo herdr check` prints the sidebar row to add: `rows = [["state_icon", "agent", "$yolo_req"]]` under `[ui.sidebar.agents]` |
| `--pick` outside a terminal | `yolo approve --pick needs a terminal; use yolo approve r-<id>` — exit 2 |
| `--pane` names a pane with no token | the picker opens unfiltered, first line: `pane <id> runs no jail yolo knows; showing every request` |
| `yolo approve` refuses inside the picker | its own refusal, verbatim, in the detail pane; the row stays |
| An agent runs `yolo herdr` in the jail | `host-only: yolo herdr runs on your machine` — exit 64 |

Exit 3 is `on-request`'s own code for "no alert was accepted"; a chain keys on any non-zero. A hook that
exits non-zero is recorded on the request, so a chain with no fallback leaves the owner's `yolo approve`
listing saying `your on_request failed, so you may not have been notified`.

## 7. Security

- **herdr's socket is full control of the host's terminals** ([HR-D2](../research/herdr-integration.md#HR-D2)).
  It is never mounted into a jail, no `HERDR_*` variable crosses, and no herdr network mode is opened to a
  jail. Everything here runs on the host, in the user's own processes.
- **Approvals come only from the human's keystrokes**, in the popup shell, the picker or a toast button
  from the desktop notifier, each one a `yolo approve` run. A jail reaches no surface that could trigger
  one: not the picker, not the hook, not herdr's socket. The picker binds no port.
- **A jail cannot raise this alert, and under herdr it cannot raise any notification.** The only bytes yolo causes to
  reach the owner's terminal are inside a herdr notification yolo requested from the host. A jail's own
  escape sequences are dropped by herdr (MEASURED, [§2.3](#23-what-herdr-does-with-a-notification-measured))
  and by tmux unless `allow-passthrough` is on, which defaults to off (MEASURED, tmux 3.8). **Not
  covered:** in a bare terminal over ssh, with no multiplexer, a jail's output reaches the terminal
  unchanged; yolo's tty proxy filters no escape sequence (no OSC handling exists in `internal/ttyproxy`
  or `internal/tty`; searched 2026-11-26). So a jail agent can already print a lookalike desktop
  notification there. This design does not change that, and it is why the alert is never an authority:
  `yolo approve` re-reads the store and the scope file every time. Filtering a jail's output would also
  drop the agent's own legitimate notifications and is not proposed here.
- **The alert's words come from the safe fields.** Title and body are a fixed prefix,
  `YOLO_REQUEST_HEADLINE` and the id. The headline is the field the store documents as "safe in any
  renderer" ([`watch.go:218`](../../internal/brokerstore/watch.go)): yolo's words around parts it validated,
  such as a repository name. The agent's free text (`TITLE`, `REFS`, `DESCRIPTION`, `SUMMARY`) never enters
  an alert. Every value passes `termsafe.Visible` when the hook environment is built
  ([`brokerhook.go:337`](../../internal/brokerhook/brokerhook.go)), herdr strips control characters and caps
  the title at 80 and the body at 240, and the client strips ESC, BEL and the C1 string terminator again
  before writing ([`terminal_notify.rs:65-93`](https://github.com/herdrdev/herdr/blob/v0.9.5/src/terminal_notify.rs#L65-L93)).
  A test pins that no byte below 0x20 or in 0x7f to 0x9f reaches the argv yolo hands herdr.
- **Spam is bounded.** A jail has at most three pending requests and a same-command denial is quiet for ten
  minutes ([`limits.go`](../../internal/brokerstore/limits.go)), so at most three alerts per jail are
  outstanding.
- **Request text is display-only in herdr.** Never passed to `herdr agent prompt`, `send-text`, `pane run`,
  a shell, or a resume command ([HR-D4](../research/herdr-integration.md#HR-D4),
  [HR-D7](../research/herdr-integration.md#HR-D7)).
- **Tokens are claims, not authority.** A pane's `yolo_jail` token decides which panes get the marker and
  which requests the picker shows first; `yolo approve` checks the request against the store and the live
  scope file as it always does.

## 8. What I would build, in order

| Slice | What | Needs a ruling | Code |
| :--- | :--- | :--- | :--- |
| **0** | The owner's two config edits and one command: the `on_request` line below, `[ui.toast] delivery` on the machine that runs the herdr client, and the `prefix+a` popup of [§5.1](#51-without-a-new-picker). Works today for a remote owner whose terminal herdr detects (the herdr half is MEASURED; the hook half is the documented `on_request` environment) | none | none |
| **1** | `yolo herdr on-request` (alert only, exit-3 contract, one retry on `rate_limited`) and `yolo herdr check` with `--ring` | [OQ-HA1](#OQ-HA1) | Go, stand-in `herdr` tests |
| **2** | The two launcher tokens and the marker with its TTL | none beyond slice 1 | Go |
| **3** | `yolo approve --pick`, `answers` in `--json` | none beyond slice 1 | Go |
| later | Whichever of [OQ-HA3](#OQ-HA3) B and [OQ-HA4](#OQ-HA4) B the owner picks | [OQ-HA3](#OQ-HA3), [OQ-HA4](#OQ-HA4) | Go |

Slice 0's `on_request`, before any yolo code exists:

```toml
on_request = 'herdr notification show "yolo: $YOLO_REQUEST_HEADLINE" --body "run: yolo approve $YOLO_REQUEST_ID" --sound request'
```

It differs from the verb in one way that is why slice 1 exists: herdr exits 0 when it answers
`shown: false` (MEASURED, `no_foreground_client`), so this line fails silently where the verb exits 3.

## 9. Testing without a real herdr

- **A stand-in `herdr`** on the test's `PATH`, as the launcher's arm is already tested with: a script that
  logs every argv and answers `pane list`, `pane get` and `notification show` from a fixture, including each
  `reason`. Tests pin the token argv at registration and its clear at release; the notifier's pane
  selection by jail id, its token and label text and TTL, the single clear-or-rewrite on SIGINT; the exit-3
  contract for every reason; the argv bytes of the alert ([§7](#7-security)); each stop's line.
- **A chained hook** is one test: a fixture `on_request` of `yolo herdr on-request || …` with a stand-in
  herdr answering `no_foreground_client` runs the fallback exactly once and with the request JSON still on
  its stdin.
- **The picker** runs over a store fixture with scripted keystrokes and a stand-in `herdr` for `g`; tests
  assert the store's decisions and that an absent answer offers no key.
- **The identity match** is one test: a request filed through the broker fixture carries the jail id the
  launcher would put on the pane, from the same workspace path, symlinked and not.
- **No CI runs herdr**, so one human check remains, listed in the plan: from a second machine, `ssh` to the
  host, run `herdr` there with `[ui.toast] delivery = "terminal"` in a Ghostty, iTerm2, WezTerm or kitty
  window; in a pane run `yolo -- claude` and have it push to `main`; the desktop notification appears on the
  second machine, the sidebar row reads `awaiting approval r-…`, `prefix+a` opens the popup, `yolo approve
  r-…` lets the push through, and the row clears. Repeat once with `herdr --remote host` and
  `--remote-keybindings server`.

## 10. Non-goals

- No herdr handle crosses into a jail, and no agent drives herdr ([OQ-HR4](../research/herdr-integration.md#OQ-HR4) stands).
- No automatic focus steal: a request never moves your cursor; `g` does, on your key.
- No retry injection: after approval, `yolo wait` wakes the agent; nothing is typed into its pane.
- Not a general event feed: the marker is lit by `on_request` alone; grants, revocations and audit stay in
  `yolo approve` and `yolo audit`.
- No change to what a request may ask or how `yolo approve` decides; this is a front end.
- No change to herdr, and no write to herdr's config. yolo prints the lines the owner adds.
- No push or chat transport (ntfy, Slack, email) for an owner with no herdr client attached and no desktop.
  The chain in [§4.2](#42-one-approval-one-alert) is where the owner's own command goes.

## Open Questions

1. 💬 **<a id="OQ-HA1"></a>[OQ-HA1](#OQ-HA1): Where does the herdr approval front end live: in yolo, in a yolo pack, or in your dotfiles?**

   Matt's yolo host is a remote machine he reaches over ssh, with four jails running in herdr there.
   He wants an alert on his laptop when one asks to push, the pane marked, and one key to answer, and
   someone has to maintain that code.

   - **A — In yolo core.** `yolo herdr on-request` and `yolo approve --pick` ship with yolo; Matt adds one
     `on_request` line and one herdr key, and runs `yolo herdr check` when an alert does not arrive. A herdr
     plugin manifest for the pane menu can follow.
   - **B — A `herdr` pack** that also renders and links a plugin on the host, for one user. The same verbs,
     behind a pack he must select and that adds a manifest to maintain.
   - **C — Dotfiles only.** Shell over `yolo approve --json` and `herdr notification show`; yolo adds the
     tokens and nothing else. He gets the alert tonight and no exit-3 contract, so a chain cannot fall back
     and a silent miss stays silent.

   <!-- vantage: question id=OQ-HA1 leaning="A: the notifier and the picker are front ends over yolo's own store and verbs, which is where the parsing, the exit-3 contract and the tests belong; herdr's own popup key runs any command, so a plugin adds only a menu entry." -->

   _Leaning:_ **A.** The exit-3 contract that lets a chain fall back, the JSON parsing and the stand-in
   tests belong in Go beside the store; herdr's `type = "popup"` key runs any command, so a plugin adds only
   a context-menu entry.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **<a id="OQ-HA2"></a>[OQ-HA2](#OQ-HA2): When a herdr client is attached, should the desktop toast also fire?**

   Matt is on his laptop in an ssh session to the host, with herdr attached, when a jail asks to push. His
   `on_request` can ring herdr, which reaches his laptop, and can also post the host desktop's D-Bus toast,
   which reaches an empty room unless he is at that desk. Today only the toast exists.

   - **A — herdr first, desktop only when no herdr client took the alert.**
     `yolo herdr on-request || exec yolo internal approval-notify`. On the laptop he gets one alert. At the
     host's desk with herdr attached he gets herdr's alert and no Allow buttons; with no herdr client
     attached he gets the desktop toast with buttons, as today.
   - **B — Both, always.** At the desk he gets two notifications for one request; on the laptop he gets one,
     plus a toast on the empty desktop whose buttons work if anyone is there.
   - **C — herdr only.** One alert everywhere herdr is attached; with no client attached he hears nothing, and
     the host desktop never shows the buttons.

   <!-- vantage: question id=OQ-HA2 leaning="A: one doorbell per request, and the desktop's Allow buttons return exactly when no herdr client is attached to take the alert; the owner who wants both writes one & instead of ||." -->

   _Leaning:_ **A.** One alert per request is the rule that keeps a doorbell a doorbell, and the desktop
   buttons come back exactly when herdr has no one to tell. Preference-shaped for the desk case, so the
   weakest part of the leaning is the lost buttons at the desk, which the picker replaces.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 **<a id="OQ-HA3"></a>[OQ-HA3](#OQ-HA3): Should the alert be a herdr notification, or herdr's own "needs attention" from a blocked state?**

   Matt's Claude pane is in a background tab and asks to push. herdr has two ways to ring his terminal for it,
   and both worked in the measurement. A notification the hook raises says `yolo: git push to main` and
   `run: yolo approve r-3f9a`, is never suppressed for the tab he is looking at, and lasts five seconds in
   herdr's own toast. Reporting the pane `blocked` makes herdr say `claude needs attention: <workspace> · 1 ·
   1` with no request id, lights the tab and workspace as blocked in the sidebar, and herdr stays quiet when
   that tab is the one he is on. If the broker dies, the kernel kills the hook before it can set the state
   back, and the pane stays `blocked` until its next report.

   - **A — A notification only.** The text carries the id and the command; the marker is a label that expires
     by itself. No state is changed, so a dead hook leaves nothing wrong behind.
   - **B — `blocked` only.** herdr's own alert and rollups, no request id in the text, nothing for the tab he
     is on, and the stuck-state risk.
   - **C — Both.** Two alerts for one request, since herdr raises its own as well; the rollups light too.

   <!-- vantage: question id=OQ-HA3 leaning="A: it carries the request id and the answering command, is never suppressed for the active tab, and changes no state a dead hook could leave wrong; B's rollup is the only thing lost." -->

   _Leaning:_ **A.** The alert's job is to name the request and the command that answers it, and a
   notification does that without lying about the agent's state if the hook dies. The cost is the sidebar
   rollup that `blocked` would give a collapsed workspace.

   **Answer:**
   > _(empty — fill in when decided)_

4. 💬 **<a id="OQ-HA4"></a>[OQ-HA4](#OQ-HA4): Should yolo write the alert escape itself for terminals that are not herdr?**

   A colleague reaches the same host over ssh in Ghostty, runs tmux, and has no herdr. His desktop toast also
   lands in an empty room. yolo could write the alert escape to his outer terminal itself: MEASURED, a write to
   the tmux client's tty (`tmux list-clients -F '#{client_tty}'`) arrives at the outer terminal untouched, while a
   pane's own escape is dropped by default and an escape wrapped for tmux passes only with `allow-passthrough
   on`. The hazard (INFERRED, not measured) is that tmux redraws that tty with its own writes, so a write could land
   inside a half-sent sequence; and a bare ssh with no multiplexer has no such tty to name.

   - **A — No.** yolo ships nothing for him; the doc gives a three-line `on_request` recipe over
     `tmux list-clients` that he owns. He gets the alert tonight and the interleave hazard is his.
   - **B — A yolo verb for tmux clients.** `yolo herdr`'s sibling writes one escape, built from the same
     escaped headline, to each tmux client tty. Tested, and still subject to the interleave hazard.
   - **C — B plus the launcher's own terminal.** The tty proxy that already sits on the owner's terminal
     injects the escape between output chunks, which also covers a bare ssh. To avoid landing inside a
     half-sent sequence the proxy would have to track escape state in the jail's output.

   <!-- vantage: question id=OQ-HA4 leaning="A: the owner uses herdr, which carries the alert through its own client; the tmux recipe is three lines the colleague can own, and B/C carry an escape-interleave hazard for a user nobody has asked for." -->

   _Leaning:_ **A.** Nobody has asked for the non-herdr path, B and C each add a writer with an interleave
   hazard to make a measured-possible alert, and the recipe costs the colleague one line.

   **Answer:**
   > _(empty — fill in when decided)_

## Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="HA-D1"></a>HA-D1 | The pane token `yolo_jail` is the request's jail id exactly, `paths.JailShortHash(runtime.FromWorkspace(workspace))`, set with the existing label call under the signal arm and cleared at release with `--clear-token`; no TTL; `yolo_project` beside it; fresh, attach and macos-user alike | 2026-11-02 | [§3.1](#31-the-launcher-adds-two-tokens) | no |
| <a id="HA-D2"></a>HA-D2 | The stock notifier is a host-only verb, `yolo herdr on-request`, run by `on_request`; it raises the alert first, marks panes found client-side from `herdr pane list` under source `yolo-approvals` guarded by `--applies-to-source`, waits for SIGINT, and clears or rewrites in one call. It reads no stdin, so a chained command still gets the request JSON | 2026-11-26 | [§4.1](#41-the-notifier) | no |
| <a id="HA-D3"></a>HA-D3 | One `yolo_req` token per pane: the jail's oldest pending id plus ` +N`, recomputed from `yolo approve --json` by every writer, ordered by `--seq` in unix milliseconds. The state label is set on **all five** states, and the write carries `--ttl-ms` equal to the request's remaining life (MEASURED: a TTL removes token and labels), so a hook killed with its broker leaves nothing behind | 2026-11-26 | [§3.2](#32-the-marker) | no |
| <a id="HA-D4"></a>HA-D4 | The hook-env strip drops pane identity (`HERDR_PANE_ID`, `HERDR_TAB_ID`, `HERDR_WORKSPACE_ID`, `TMUX_PANE`, `ZELLIJ_PANE_ID`, `WEZTERM_PANE`, `KITTY_WINDOW_ID`) and keeps session identity (`HERDR_SOCKET_PATH`, `HERDR_SESSION`); herdr is the one on `PATH` | 2026-11-02 | [§3.4](#34-the-hook-reaches-the-right-herdr) | strip: yes (`brokerhook.paneIdentity`); verb: no |
| <a id="HA-D5"></a>HA-D5 | `yolo approve --json` carries `answers` per request; the picker and any front end offer only those, hardcoding no flag | 2026-11-02 | [§5.2](#52-the-picker) | no |
| <a id="HA-D6"></a>HA-D6 | `yolo approve --pick [--jail\|--pane]` is a terminal UI over `--watch --json`; one key answers one request through one `yolo approve`, output shown verbatim, never retried; `g` focuses the asking pane | 2026-11-02 | [§5.2](#52-the-picker) | no |
| <a id="HA-D7"></a>HA-D7 | Request text reaches only herdr text fields; never `agent prompt`, `send-text`, `pane run`, a shell or a resume command. The alert's text is a fixed prefix, the headline (the store's safe-in-any-renderer field) and the id, never an agent free-text field | 2026-11-26 | [§7](#7-security) | no |
| <a id="HA-D8"></a>HA-D8 | No herdr handle crosses into a jail and no network door opens for one; approvals are host keystrokes only; the picker binds no port | 2026-11-02 | [§7](#7-security) | no |
| <a id="HA-D9"></a>HA-D9 | A missing `herdr`, no server, or an alert herdr did not accept exits 3 at once with one line naming the next step, so a chain falls back and a lone hook is recorded failed; no pane for the jail is a no-op after the alert (exit 0) | 2026-11-26 | [§6](#6-every-stop-names-its-next-step) | no |
| <a id="HA-D10"></a>HA-D10 | No focus steal and no retry injection: a request moves no cursor and types nothing; `yolo wait` wakes the agent | 2026-11-02 | [§10](#10-non-goals) | no |
| <a id="HA-D11"></a>HA-D11 | Verified without herdr: a stand-in `herdr` on `PATH` for the arm and the verbs, a store fixture with scripted keys for the picker, one identity-match test; one human check on a real herdr, named in the plan | 2026-11-26 | [§9](#9-testing-without-a-real-herdr) | no |
| <a id="HA-D12"></a>HA-D12 | The alert is a herdr notification requested from the host, never bytes written into a pane's tty: herdr drops a pane's OSC 9, 777 and 99 (MEASURED), and in a bare terminal the write would land inside the agent's own drawing | 2026-11-26 | [§2.3](#23-what-herdr-does-with-a-notification-measured) | no |
| <a id="HA-D13"></a>HA-D13 | The remote owner is not detected. The channel is chosen by outcome: herdr's `shown` result decides whether a chained fallback runs; `SSH_CONNECTION`, `DISPLAY` and the bus address are not read, because the hook inherits the broker's environment | 2026-11-26 | [§4.3](#43-remote-detection-none-by-design) | no |
| <a id="HA-D14"></a>HA-D14 | One alert per request: one hook per request (BUILT), one producer by the `\|\|` chain, one alert kind (a notification, not also a `blocked` report). Nothing withdraws an alert | 2026-11-26 | [§4.2](#42-one-approval-one-alert) | hook: yes; rest: no |
| <a id="HA-D15"></a>HA-D15 | yolo never writes herdr's config. `yolo herdr check` prints the lines to add for delivery, the sidebar row and the popup key, names the attach mode's requirement (host config, `--remote-keybindings server`), and `--ring` raises one test alert | 2026-11-26 | [§2.4](#24-which-attach-mode-the-owner-is-in) | no |
| <a id="HA-D16"></a>HA-D16 | A jail's own escape output is not filtered by this design; the alert is a hint, never an authority, and `yolo approve` re-reads the store every time | 2026-11-26 | [§7](#7-security) | n/a |
