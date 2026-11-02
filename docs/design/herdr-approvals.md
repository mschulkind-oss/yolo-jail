---
title: "Approvals in herdr: the pane that asked lights up, and one key answers it"
date: 2026-11-02
status: in-review
stage: DESIGN
next: "Rule OQ-HA1 (where the herdr front end ships) and OQ-HA2 (whether it raises herdr's toast); then plan, after the v0.16.0 release"
depends-on:
  - ../research/herdr-integration.md
  - boundary-broker.md
tags: [design, herdr, approvals, broker, on_request, terminal, host]
summary: "When a jailed agent's push or gh write needs approval, the herdr pane running that jail lights up: its sidebar row shows the request id and reads 'awaiting approval', a herdr notification fires, and prefix+a opens a picker over the pending requests that answers each with the same yolo approve the toast buttons run. yolo adds three things: a jail-identity token on the panes it already labels, a stock on_request notifier that maps a request to those panes, and an interactive yolo approve --pick. Nothing of herdr's crosses into a jail; every approval is a human keystroke on the host."
---

# Approvals in herdr: the pane that asked lights up, and one key answers it

**Status:** 2026-11-02; design, nothing built. herdr behavior is SOURCED from its docs at tag
`v0.9.5` (its latest release, 2026-10-14); yolo evidence is read at `37773b7ee` plus the in-flight
`on_request` fields (`YOLO_REQUEST_HEADLINE` and friends). No herdr was run. Built after v0.16.0.

> **In short.** A request already names its jail, and the launcher already labels the jail's herdr
> pane. Tag that pane with the jail's id, and every approval surface herdr has, sidebar, toast and
> popup, can point at the pane that asked and answer it with `yolo approve`.

**What you see.** You run `yolo -- claude` in a herdr pane, and the agent pushes to `main`:

| Where | Today | With this design |
| :--- | :--- | :--- |
| herdr sidebar row for the pane | `● claude · yolo-jail` (idle) | `● claude · yolo-jail · awaiting approval r-3f9a` |
| Notification | your `notify-send` toast, if you configured one | the same, plus a herdr toast with the request sound, so it reaches you over SSH too |
| `prefix+a` | nothing | a popup listing pending requests: headline, project, command; `Enter` allows once, `f` for a duration, `d` denies, `g` jumps to the pane that asked |
| The pane's context menu (optional plugin) | nothing | "Approve what this pane's jail asked", the same picker filtered to that jail |
| When the request is answered or expires | the toast closes | the row, the label and the toast clear within two seconds |

**The shape.** Three parts, all on the host: the launcher puts a **jail token** on the pane
([§2](#2-the-jails-pane-identity)); a **stock notifier**, run by `on_request`, lights every pane
carrying that token and clears it when yolo stops the hook ([§3](#3-the-request-lights-its-panes));
a **picker**, `yolo approve --pick`, is the popup ([§4](#4-the-picker)).

**Cost.** Two tokens on an existing herdr call, one new verb group, one new `yolo approve` flag, one
JSON field. No new channel, no new daemon. The herdr plugin manifest, if shipped, is a few lines.

**Start at [§2](#2-the-jails-pane-identity):** the identity match is what the rest hangs on.

**Needs your ruling:** [OQ-HA1](#OQ-HA1), [OQ-HA2](#OQ-HA2).

**Reads with:** [`herdr-integration.md`](../research/herdr-integration.md) (how the launcher
reports a jailed agent to herdr; its rulings HR-D2 and HR-D4 bind here),
[`approvals.md`](../../userguide/guides/approvals.md) (the approval surface this fronts),
[`boundary-broker.md`](boundary-broker.md) (the request store and `on_request`).

---

## 1. Terms

- **Request.** One pending approval the host broker filed for a jail's `git push` or `gh` write, with
  an id `r-…`, answered by `yolo approve` ([approvals guide](../../userguide/guides/approvals.md)).
- **`on_request` hook.** The command the user's config names, run once per request, as the user,
  until the request stops waiting; yolo then sends it SIGINT, and SIGKILL two seconds later.
- **Jail id.** The 8-hex key a request carries as `YOLO_REQUEST_JAIL` and `yolo approve` prints as
  "jail". It is `paths.JailShortHash` of the container name, which is derived from the resolved
  workspace path (`runtime.FromWorkspace`), on every backend including macos-user.
- **Pane token** and **state label** *(herdr's)*. Display-only values a script sets on a pane with
  `herdr pane report-metadata`: a token `--token NAME=VALUE` renders in the sidebar as `$NAME`; a
  state label `--state-label STATUS=TEXT` replaces herdr's state word (`idle`, `working`, ...).
- **Picker** *(coined here)*. The interactive `yolo approve --pick`: a host-only terminal UI over
  the pending requests that runs `yolo approve` for the row you choose. It is not a daemon and not
  a herdr pane; herdr shows it in a popup.

## 2. The jail's pane identity

**The match is exact, and it needs no new state.** A request's jail field is
`paths.JailShortHash(sf.Container)` ([`approvalauthority.go`](../../internal/gitbroker/approvalauthority.go)),
and `sf.Container` is the `cname` the launcher computes above the backend dispatch
([`run.go`](../../internal/cli/run/run.go), `runtime.FromWorkspace(o.Workspace)`) and writes into
the scope file ([`brokeredscope.go`](../../internal/cli/run/brokeredscope.go)). An attach computes
the same name from the same workspace, and its requests go through the fresh launch's broker, so
one jail id covers every session of a jail.

**The launcher adds two tokens to the label call it already makes** in
[`herdragent.go`](../../internal/cli/run/herdragent.go), under the same signal arm:

```text
herdr pane report-metadata <pane> --source yolo-jail --agent claude --applies-to-source yolo-jail \
  --title "🔒 JAIL yolo-jail" --token yolo_jail=<jail id> --token yolo_project=yolo-jail
```

- `yolo_jail` is the request's jail id, byte for byte. `yolo_project` is the label's project name.
- Tokens are not guarded by `--agent`, so the release clears them explicitly (`--clear-token`), on
  the same path that clears the title. No TTL: the launcher is long-lived and a token left by a
  SIGKILLed launcher is display-only and dies with the pane ([HA-D1](#HA-D1)).
- Finding the panes of a jail is `herdr pane list` (JSON) filtered on `tokens.yolo_jail` by the
  caller; the CLI documents no token filter, so the filter is client-side. Zero panes is normal: the
  jail was launched outside herdr.

## 3. The request lights its panes

**The stock notifier is `yolo herdr on-request`**, a host-only verb a user names in `on_request`,
alone or beside their own notifier:

```toml
# ~/.config/yolo-jail/config.toml
on_request = "yolo herdr on-request"                       # herdr is the whole front end
on_request = "yolo herdr on-request & exec ~/.local/bin/yolo-approve-toast"   # beside a desktop toast
```

What it does, in order, reading only its environment and `yolo approve --json`:

1. **Find the panes**: `herdr pane list`, keep those whose `tokens.yolo_jail` equals
   `$YOLO_REQUEST_JAIL`. None: print one line and exit 0 ([§6](#6-every-stop-names-its-next-step)).
2. **Light each pane**: one `report-metadata` with `--source yolo-approvals`,
   `--applies-to-source yolo-jail`, `--token yolo_req=<value>`, `--state-label idle=<text>` and
   `--state-label unknown=<text>`, `--seq <unix ms>`. The value is the jail's oldest pending id,
   `r-3f9a`, or `r-3f9a +2` when more are pending; the label is `awaiting approval r-3f9a`.
3. **Notify** ([OQ-HA2](#OQ-HA2)): `herdr notification show "yolo: approval needed" --body "<headline> · <project>" --sound request`.
4. **Wait** for SIGINT, which yolo sends when the request leaves pending.
5. **Clear or recompute**: re-read `yolo approve --json`; with no request left for the jail, send
   `--clear-token yolo_req --clear-state-labels`; otherwise rewrite the value for those left.

- **Several requests, one jail.** Each request has its own hook; all write the same token under one
  source. Each write is computed from the store, so two concurrent writers agree on the value, and
  `--seq` orders them ([HA-D3](#HA-D3)). The value shows the oldest id because it is the one the
  agent is most likely blocked on.
- **The state label rides on `idle` and `unknown`**, the states a waiting agent shows (herdr's
  `blocked` fires only on an approval UI it recognizes), and `--applies-to-source yolo-jail` retires
  it with the launcher's report even if the clear is lost.
- **Text is display-only.** The title is yolo's words; the body is `YOLO_REQUEST_HEADLINE` (built by
  yolo, validated), else `YOLO_REQUEST_SUMMARY`. Nothing from the request goes anywhere but a herdr
  text field ([§5](#5-security)).
- **Which herdr.** The `herdr` on `PATH`, as the launcher's arm does; the socket is herdr's own
  resolution (`HERDR_SOCKET_PATH`, then `HERDR_SESSION`, then the default session). The in-flight
  hook-env strip must drop pane identity (`HERDR_PANE_ID`, `HERDR_TAB_ID`, `HERDR_WORKSPACE_ID`,
  `TMUX_PANE`) and keep session identity, or a named herdr session is unreachable ([HA-D4](#HA-D4)).
- **Timing.** Each herdr call is bounded by 2 s, like the launcher's. Steps 1 to 3 run once;
  step 5 runs once on SIGINT and must finish inside yolo's 2 s grace, so it is one call.

## 4. The picker

`yolo approve --pick [--jail <id> | --pane <herdr pane id>]` is a full-screen terminal UI over the
same store `yolo approve` lists, refreshed live from `yolo approve --watch --json`. Host-only, and
usable in any terminal; herdr is one way to open it.

| Key | Does | Runs |
| :--- | :--- | :--- |
| `↑` `↓` | move between pending requests; the detail pane shows headline, project, jail, repository, command, refs needing approval, expiry, and a failed hook's line | — |
| `Enter` or `o` | allow once | `yolo approve r-…` |
| `f` | allow for a duration, prompted, default `15m` | `yolo approve r-… --for <d>` |
| `a`, `r` | add to the working set; remember in the project's config | `--add`, `--remember` |
| `d` | deny | `yolo approve r-… --deny` |
| `g` | jump to the pane that asked (first pane carrying the jail's token) | `herdr agent focus <pane>` |
| `q`, `Esc` | close | — |

- **Keys come from the request, not the picker.** `yolo approve --json` grows an `answers` list per
  request (`once`, `for`, `add`, `remember`, `deny`), the set the request's source accepts today;
  a key whose answer is absent is not offered. So when a source retires a flag, as the GitHub
  design does for `--add` ([OQ-GW2](github-write-scopes.md#decision-ledger)), the picker follows
  with no change ([HA-D5](#HA-D5)).
- **Each answer is one `yolo approve`**, with its checks, refusals and audit; the picker shows the
  verb's output verbatim and never retries. One key answers one request, never several.
- **Filtering.** `--jail` keeps one jail's requests. `--pane` reads the pane's `yolo_jail` token
  with `herdr pane get` and filters on it; a pane with no token says so and shows every request.
- **In herdr**, two lines of the user's config open it; no plugin is needed for the popup:

  ```toml
  [[keys.command]]
  key = "prefix+a"
  type = "popup"
  command = "yolo approve --pick --pane \"$HERDR_ACTIVE_PANE_ID\""
  width = "80%"
  height = "60%"
  ```

  A popup is not a herdr pane: it gets no `HERDR_PANE_ID`, so the underlying pane comes from
  `HERDR_ACTIVE_PANE_ID`. A `herdr-plugin.toml` adds only an `[[actions]]` entry for the pane's
  context menu and a marketplace listing ([OQ-HA1](#OQ-HA1)).

## 5. Security

- **herdr's socket is full control of the host's terminals** ([HR-D2](../research/herdr-integration.md#HR-D2)).
  It is never mounted into a jail, no `HERDR_*` variable crosses, and no herdr network mode is
  opened to a jail. Everything here runs on the host, in the user's own processes.
- **Approvals come only from the human's keystrokes**, in the picker or on a toast button, each one
  a `yolo approve` run. A jail reaches no surface that could trigger one: not the picker, not the
  hook, not herdr's socket. The picker binds to no port.
- **Request text is display-only.** Headline, summary, command and refs go into herdr text fields
  (title, body, token, label), which herdr strips of control characters and caps. They are never
  passed to `herdr agent prompt`, `send-text`, `pane run`, a shell, or a resume command
  ([HR-D4](../research/herdr-integration.md#HR-D4), [HR-D7](../research/herdr-integration.md#HR-D7)).
- **The hook inherits the broker's environment**, so pane identity is stripped from it: a bare
  `herdr pane …` in a hook can never act on the pane that started the broker.
- **Tokens are claims, not authority.** A pane's `yolo_jail` token only decides which panes light
  and which requests the picker shows first; `yolo approve` checks the request against the store
  and the live scope file as it always does.

## 6. Every stop names its next step

| The stop | The line, and the step it ends with |
| :--- | :--- |
| `herdr` not on `PATH` (notifier) | `yolo herdr: herdr is not on PATH, so no pane was lit; install herdr or remove "yolo herdr on-request" from on_request` — exit 1, recorded on the request as a failed hook |
| No herdr server | `yolo herdr: no herdr server at <socket>; start herdr, or set HERDR_SESSION where yolo is launched` — exit 1 |
| No pane carries the jail | `yolo herdr: no herdr pane runs jail <id> (launched outside herdr?); nothing to light` — exit 0 |
| Notification not shown (`disabled`, `rate_limited`, `busy`, `no_foreground_client`) | one line naming herdr's reason; for `disabled`: `set [ui.toast] delivery in herdr's config` — the pane is still lit, exit 0 |
| Token set but invisible | `yolo herdr check` prints the sidebar row to add: `rows = [["state_icon", "agent", "$yolo_req"]]` under `[ui.sidebar.agents]` |
| `--pick` outside a terminal | `yolo approve --pick needs a terminal; use yolo approve r-<id>` — exit 2 |
| `--pane` names a pane with no token | the picker opens unfiltered, first line: `pane <id> runs no jail yolo knows; showing every request` |
| `yolo approve` refuses inside the picker | its own refusal, verbatim, in the detail pane; the row stays |
| An agent runs `yolo herdr` in the jail | `host-only: yolo herdr runs on your machine` — exit 64 |

## 7. What core adds, and what lives outside

| Where | What |
| :--- | :--- |
| `internal/cli/run/herdragent.go` | the two tokens, and their clear at release |
| `internal/cli` (new) | `yolo herdr on-request`, `yolo herdr panes [--jail]` (the pane list, for debugging), `yolo herdr check` (herdr found, server reachable, sidebar row present) |
| `yolo approve` | `--pick`, `--jail`, `--pane`; `answers` in `--json` |
| `internal/brokerhook` | the env strip keeps `HERDR_SOCKET_PATH` and `HERDR_SESSION` |
| User's herdr config | the `prefix+a` popup line; the `$yolo_req` sidebar row |
| User's yolo config | `on_request` naming the notifier |
| A `herdr-plugin.toml` ([OQ-HA1](#OQ-HA1)) | an `[[actions]]` entry for the pane menu; a `[[panes]]` popup entry |

## 8. Testing without a real herdr

- **A stand-in `herdr`** on the test's `PATH`, as the launcher's arm is already tested with: a script
  that logs every argv and answers `pane list`, `pane get` and `notification show` from a fixture.
  Tests pin the token argv at registration and its clear at release; the notifier's pane selection
  by jail id, its token and label text, the single clear-or-rewrite on SIGINT; each stop's line.
- **The picker** runs over a store fixture with scripted keystrokes and a stand-in `herdr` for
  `g`; tests assert the store's decisions and that an absent answer offers no key.
- **The identity match** is one test: a request filed through the broker fixture carries the jail
  id the launcher would put on the pane, from the same workspace path, symlinked and not.
- **No CI runs herdr**, so one human check remains, listed in the plan: a `yolo -- claude` in a
  split herdr pane with the sidebar row configured; a push to `main`; the row, label, toast and
  `prefix+a` picker appear; `Enter` lets the push through; the row clears.

## 9. Non-goals

- No herdr handle crosses into a jail, and no agent drives herdr ([OQ-HR4](../research/herdr-integration.md#OQ-HR4) stands).
- No automatic focus steal: a request never moves your cursor; `g` does, on your key.
- No retry injection: after approval, `yolo wait` wakes the agent; nothing is typed into its pane.
- Not a general event feed: the sidebar is lit by `on_request` alone; grants, revocations and audit
  stay in `yolo approve` and `yolo audit`.
- No change to what a request may ask or how `yolo approve` decides; this is a front end.

## Open Questions

1. 💬 **<a id="OQ-HA1"></a>[OQ-HA1](#OQ-HA1): Where does the herdr approval front end live: in yolo, in a yolo pack, or in your dotfiles?**

   Matt runs four jails in herdr; when one asks to push, his own script pops a desktop toast. He
   wants the pane to light up and a `prefix+a` picker, and someone maintains that code.

   - **A — In yolo core.** `yolo herdr on-request` and `yolo approve --pick` ship with yolo; you add
     one `on_request` line and one herdr key. A plugin manifest for the pane menu can follow.
   - **B — A `herdr` pack** that also renders and links the plugin on the host, for one user.
   - **C — Dotfiles only.** Shell over `yolo approve --json`; yolo adds the tokens and nothing else.

   <!-- vantage: question id=OQ-HA1 leaning="A: the hook and picker must call yolo approve and parse its JSON, which Go does and tests; herdr's own popup key runs any command, so no plugin is needed for the owner's use, and a manifest adds only a menu entry." -->

   _Leaning:_ **A.** The notifier and the picker are front ends over yolo's own store and verbs,
   which is where the parsing and the tests belong; herdr's `type = "popup"` key runs any command,
   so a plugin adds only a context-menu entry and a marketplace card.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 🤷 **<a id="OQ-HA2"></a>[OQ-HA2](#OQ-HA2): Should the stock notifier raise herdr's own toast as well as lighting the pane?**

   Matt's script already pops a desktop toast with Allow and Deny buttons. If
   `yolo herdr on-request` also calls `herdr notification show --sound request`, a request at
   his desk shows two notifications; over SSH, only herdr's reaches him, since `notify-send`
   fires on the unattended desktop.

   - **A — Always toast.** One herdr toast with the request sound, suppressed by herdr for the
     active tab; you drop `notify-send` from your script if two is too many.
   - **B — Light only.** The row and label change; notifications stay with whatever else the hook
     runs.
   - **C — `--toast` flag**, off by default; one more thing to know.

   <!-- vantage: question id=OQ-HA2 leaning="A: herdr's toast is the one that works over SSH and is already suppressed for the pane you are looking at; a second desktop toast is the owner's script to trim." -->

   _Leaning:_ **A.** herdr's toast is the one that works over SSH, and herdr suppresses it for the
   active tab; trimming a second desktop toast is one line in the owner's script.

   **Answer:**
   > _(empty — fill in when decided)_

## Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="HA-D1"></a>HA-D1 | The pane token `yolo_jail` is the request's jail id exactly, `paths.JailShortHash(runtime.FromWorkspace(workspace))`, set with the existing label call under the signal arm and cleared at release with `--clear-token`; no TTL; `yolo_project` beside it; fresh, attach and macos-user alike | 2026-11-02 | [§2](#2-the-jails-pane-identity) | no |
| <a id="HA-D2"></a>HA-D2 | The stock notifier is a host-only verb, `yolo herdr on-request`, run by `on_request`; it finds panes client-side from `herdr pane list`, writes token and state labels under source `yolo-approvals` guarded by `--applies-to-source yolo-jail`, waits for SIGINT, and clears or rewrites in one call | 2026-11-02 | [§3](#3-the-request-lights-its-panes) | no |
| <a id="HA-D3"></a>HA-D3 | One `yolo_req` token per pane: the jail's oldest pending id plus ` +N`, recomputed from `yolo approve --json` by every writer, ordered by `--seq` in unix milliseconds; labels on `idle` and `unknown` only | 2026-11-02 | [§3](#3-the-request-lights-its-panes) | no |
| <a id="HA-D4"></a>HA-D4 | The hook-env strip drops pane identity (`HERDR_PANE_ID`, `HERDR_TAB_ID`, `HERDR_WORKSPACE_ID`, `TMUX_PANE`) and keeps session identity (`HERDR_SOCKET_PATH`, `HERDR_SESSION`); herdr is the one on `PATH` | 2026-11-02 | [§3](#3-the-request-lights-its-panes) | no |
| <a id="HA-D5"></a>HA-D5 | `yolo approve --json` carries `answers` per request; the picker and any front end offer only those, hardcoding no flag | 2026-11-02 | [§4](#4-the-picker) | no |
| <a id="HA-D6"></a>HA-D6 | `yolo approve --pick [--jail|--pane]` is a terminal UI over `--watch --json`; one key answers one request through one `yolo approve`, output shown verbatim, never retried; `g` focuses the asking pane | 2026-11-02 | [§4](#4-the-picker) | no |
| <a id="HA-D7"></a>HA-D7 | Request text reaches only herdr text fields; never `agent prompt`, `send-text`, `pane run`, a shell or a resume command; the body is the validated headline, summary as fallback | 2026-11-02 | [§5](#5-security) | no |
| <a id="HA-D8"></a>HA-D8 | No herdr handle crosses into a jail and no network door opens for one; approvals are host keystrokes only; the picker binds no port | 2026-11-02 | [§5](#5-security) | no |
| <a id="HA-D9"></a>HA-D9 | Missing `herdr` or no server is a failed hook (exit 1, recorded on the request); no pane for the jail is a no-op (exit 0, one line); a toast not shown never fails the hook | 2026-11-02 | [§6](#6-every-stop-names-its-next-step) | no |
| <a id="HA-D10"></a>HA-D10 | No focus steal and no retry injection: a request moves no cursor and types nothing; `yolo wait` wakes the agent | 2026-11-02 | [§9](#9-non-goals) | no |
| <a id="HA-D11"></a>HA-D11 | Verified without herdr: a stand-in `herdr` on `PATH` for the arm and the verbs, a store fixture with scripted keys for the picker, one identity-match test; one human check on a real herdr, named in the plan | 2026-11-02 | [§8](#8-testing-without-a-real-herdr) | no |
