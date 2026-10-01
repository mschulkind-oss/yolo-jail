---
title: "herdr: a terminal multiplexer for coding agents, and how yolo fits inside its panes"
date: 2026-09-29
status: in-review
tags: [research, herdr, terminal, multiplexer, sessions, lifecycle, worktrees, host, notches, integration]
summary: "herdr is a terminal multiplexer built for coding agents. A background server owns every pane's terminal, so agents keep running when the window closes, and herdr shows which agent is working, blocked or done. It sits beside yolo rather than competing with it: herdr is where the terminals live, and yolo is the environment each agent runs in. `yolo host -- <agent>` in a herdr pane already gets everything herdr offers. `yolo -- <agent>` in a container jail shows up as a plain terminal, because herdr looks for the agent among host processes and finds only podman. The best first step is small. When the launcher runs in a herdr pane, it tells herdr which program is inside by setting a variable on the host-side runtime process, and, for an agent herdr recognizes, it labels the pane as a jail, the way yolo already marks tmux panes and kitty tabs. Three things break and need rulings or other designs. Closing a herdr pane signals the launcher and, through podman's signal forwarding, the jail's own processes, and probably kills the launcher during its teardown (inferred, not measured). herdr's worktrees break git inside container jails. A herdr restart brings jail panes back as empty shells. herdr's socket must never cross into a jail, because it gives full control of the user's terminals. Four rulings are owed."
stage: DESIGN
next: "Rule OQ-HR1 to OQ-HR4. OQ-HR3 now carries the measured commit half of §6 item 8 (§4.5, 2026-10-01: a read-write .git bind commits, a read-only one refuses every write, and the jail sees its own checkout as prunable); what §6 still owes needs a real host, a herdr pane, a rootless podman or a Mac"
vantage:
  status-chip: true
---

# herdr: a terminal multiplexer for coding agents, and how yolo fits inside its panes

**Status:** 2026-09-29; research. Nothing is built, and nothing here changes the tree (re-checked 2026-09-30: nothing under `internal/`, `cmd/` or `packs/` names herdr). herdr evidence
was read at tag `v0.9.3` (commit `7b116c05`). yolo evidence was read at `e766fb23`. No agent CLI was
run. One research lens ran the herdr 0.9.3 release binary as a headless named herdr session in a
scratch directory, with `sh` and `sleep` probes in its panes. Those results are marked MEASURED.
Everything else about herdr comes from its source, docs and issue tracker. On 2026-10-01 a
second lens measured [§6](#6-what-is-unmeasured) item 8, git in a container through a bind of a
scratch repository's `.git`, on a rootful podman nested in a jail
([§4.5](#45-option-4-git-in-herdrs-worktrees)). Four rulings are owed.

**Update, 2026-09-30.** Option 2 is built in core, ahead of [OQ-HR1](#OQ-HR1)'s ruling, which
leans the same way ([built note](#built-2026-09-30)). A second design written in parallel,
`terminal-multiplexer-integration.md`, is folded into [§4.9](#49-folded-in-the-terminal-multiplexer-design)
rather than kept beside this doc.

> **In short.** herdr is tmux rebuilt for coding agents. herdr and yolo are two layers of one
> setup: herdr owns the terminals, and yolo owns what runs in them. `yolo host` already works in
> herdr. A jailed agent shows up as a plain terminal, because herdr looks for it among host
> processes. The fix is for the launcher to tell herdr what is inside, and that is small. Never
> give a jail herdr's socket.

**Why it matters.** The maintainer asked, on 2026-09-29, how yolo could integrate with herdr.
herdr went from its first release to about 41,000 GitHub stars in six months. Its users run
several agents at once in one repository, which is the several-sessions-in-one-jail case
[`jail-lifetime-last-session-wins.md`](../design/jail-lifetime-last-session-wins.md) is designing
for.

**The shape.** Six options, smallest first
([§4](#4-integration-options-smallest-first)):

- do nothing;
- a shell recipe, with no yolo code;
- the launcher tells herdr what is inside (the recommended first slice);
- resume after a herdr restart;
- git in herdr-made worktrees;
- only if wanted, a narrow herdr door for jailed agents.

**Cost of the first slice.** Small: a herdr arm beside yolo's tmux and kitty jail indicators, and
one variable on the runtime client, which needs an environment seam in the two TTY proxies
([§4.3](#43-option-2-the-launcher-tells-herdr-what-is-inside)). The slice passes nothing of
herdr's into the jail.

**Start at [§3](#3-where-it-meets-yolo-today-notch-by-notch)**, which says what works and what breaks
today.

**Needs your ruling:**

- [OQ-HR1](#OQ-HR1): where yolo's herdr knowledge lives;
- [OQ-HR2](#OQ-HR2): what a jail pane does after a herdr restart;
- [OQ-HR3](#OQ-HR3): git in a worktree whose repository is outside the jail;
- [OQ-HR4](#OQ-HR4): whether a jailed agent may drive herdr.

**Reads with:**

- [`jail-lifetime-last-session-wins.md`](../design/jail-lifetime-last-session-wins.md): several
  sessions in one jail. This doc adds evidence to its [OQ-JL1](../design/jail-lifetime-last-session-wins.md#OQ-JL1).
- [`agent-event-watchers.md`](../design/agent-event-watchers.md): pinging a running agent. herdr is
  not a delivery route for it.
- [`durable-scratch-space.md`](../design/durable-scratch-space.md): worktrees and the absolute paths
  git writes.
- [`host-agent-environment.md`'s launch PATH](../reference/host-agent-environment.md#the-launch-path-and-which-copy-of-a-program-runs): which variables `yolo host`
  passes through.
- [`host-notch-services.md`](../design/host-notch-services.md): services a host launch owns.
- [`central-yolo-watcher.md`](central-yolo-watcher.md): herdr's server is the kind of resident
  process that doc declines to build.

---

## 1. The short answer

### 1.1 What herdr is, in plain words

herdr is a terminal multiplexer: a program in the tmux family that splits one terminal window
into many terminals. It is built for people who run several coding agents at once. Three things
set it apart:

- **Agents outlive the window.** A background server owns every terminal. Closing the window, or
  detaching with `ctrl+b q`, leaves every agent running, and running `herdr` again reattaches.
- **It knows what each agent is doing.** It recognizes about two dozen agent CLIs (Claude Code,
  Codex, pi, opencode, Copilot and others). It reads each one's screen and marks the pane working,
  blocked, done or idle, and it can pop a notification when one needs you.
- **It is scriptable.** A JSON socket and a CLI let scripts, plugins and agents split panes, start
  agents, type prompts and read output.

It is one person's project, Apache-2.0, written in Rust. It reached v0.9.3 today and is in Y
Combinator's Fall 2026 batch ([§2.1](#21-the-project)).

### 1.2 How it fits yolo

herdr decides where terminals live and what the human sees about them. yolo decides the
environment an agent works in: its tools, config, skills and credentials, and how confined it is
([`what-yolo-is.md`](../reference/what-yolo-is.md)). So the fit is herdr on the host, with `yolo`
running in its panes.

| You run in a herdr pane | What you get today |
| :--- | :--- |
| `yolo host -- claude` | Everything herdr offers: status, notifications, herdr's own integration hook, and restore after a herdr restart ([§3.3](#33-yolo-host)) |
| `yolo -- claude` (a container jail) | The agent runs, but herdr sees only `yolo` and `podman`, so the pane is a plain terminal: no status and no notification ([§3.1](#31-a-container-jail)) |
| `HERDR_AGENT=claude yolo -- claude` | herdr reads Claude's screen and shows its status. The pane shows idle while the jail builds and boots |

### 1.3 The best integration, and its cost

**When the launcher runs inside a herdr pane, it tells herdr what program is in the jail, and,
when herdr recognizes that program as an agent, it labels the pane as a jail**
([§4.3](#43-option-2-the-launcher-tells-herdr-what-is-inside), [HR-D8](#HR-D8)).

- **The first half** puts `HERDR_AGENT=<the command's name>` on the host-side runtime process
  that the launcher starts (`podman run`, `podman exec` or `container run`). That variable is
  herdr's own documented fix for a sandbox that hides the agent.
- **The second half** is `herdr pane report-metadata`, a display-only label. It is the herdr
  version of the marker yolo already puts on a tmux pane border or a kitty tab.

**Cost: small.** A herdr arm beside yolo's tmux and kitty indicators, one variable on the runtime
client threaded through an environment seam in the TTY proxies, and tests. It needs no new
channel, and the slice passes nothing of herdr's into the jail.

**What it does not fix.** Each of these belongs somewhere else:

- Closing a herdr pane, or restarting herdr, SIGKILLs the launcher half a second after hanging
  it up, and podman forwards the same hangup into the jail. The launcher is probably still in its
  teardown when the kill lands. That is INFERRED, not measured
  ([§3.4](#34-closing-a-pane-is-a-kill)). It was evidence for
  [OQ-JL1](../design/jail-lifetime-last-session-wins.md#OQ-JL1), now directed to a keeper: one
  background process per running container jail, owning its host services, that no pane close
  reaches
  ([its §9.7](../design/jail-lifetime-last-session-wins.md#97-signal-handling-sig-proxy-and-a-pane-close)),
  and not something a herdr integration can fix.
- After a herdr restart, jail panes come back as empty shells ([OQ-HR2](#OQ-HR2)).
- herdr's worktrees break git inside a container jail ([OQ-HR3](#OQ-HR3)).

> [!CAUTION]
> **Never hand a jail herdr's socket.** Its only lock is the socket file's `0600` mode. Anything
> that can connect can open a new host shell pane and type into it, so a jail with the socket is a
> jail with a host shell ([§3.8](#38-the-socket-is-full-control-of-the-host)).

### 1.4 Terms

- **herdr server.** The background process that owns every pane's terminal in one herdr session.
  It is not the TUI you look at, which herdr calls a client.
- **pane.** herdr's word for one terminal in its layout. herdr nests session, herdr workspace,
  tab and pane.
  - This doc always writes **herdr session** and **herdr workspace** with the product name,
    because both words mean something else in yolo.
  - A *workspace* on its own is yolo's: the directory a jail mounts.
  - A *jail session* is [`jail-lifetime-last-session-wins.md`](../design/jail-lifetime-last-session-wins.md#11-terms)'s:
    one `yolo` invocation running a command in a jail that others may share.
- **Foreground process group.** A standard POSIX term: the process group that a terminal
  delivers keyboard signals to, as returned by [tcgetpgrp(3)](https://man7.org/linux/man-pages/man3/tcgetpgrp.3.html).
  herdr looks for agents only among its members. It is not the whole process tree under the
  pane.
- **Agent hint** *(coined here)*. The variable `HERDR_AGENT=<agent>`, set on some process in a
  pane's foreground process group, that tells herdr which agent's screen rules to apply. herdr
  documents the variable; the phrase is ours.
- **Screen reading** *(coined here)*. herdr's default way of telling an agent's state. It matches
  per-agent rules, in a file herdr calls a *screen manifest*, against the bottom of the pane and
  the terminal title. It is not a report from the agent.
- **Resume command.** herdr's term: a command line herdr stores for a pane and types into that
  pane's shell after a herdr server restart.
- **Launcher.** The host-side `yolo` process of one invocation. The term comes from
  [`jail-lifetime-last-session-wins.md`](../design/jail-lifetime-last-session-wins.md#2-what-ties-a-jail-to-its-first-terminal-today).
- **Runtime client** *(coined here)*. The `podman run`, `podman exec` or `container run` process
  that the launcher starts on the host to run or enter the container. It is not the container,
  and not conmon, podman's per-container monitor, which runs in a session of its own.
- **Teardown.** The launcher's end-of-jail chain, which stops the container, then the port
  forwards and loopholes, then clears tracking files and captures config. It is described in
  [`jail-lifetime-last-session-wins.md` §2](../design/jail-lifetime-last-session-wins.md#2-what-ties-a-jail-to-its-first-terminal-today),
  row 6.
- **Jail indicator.** yolo's existing marker on the terminal around a jail: a red
  "🔒 JAIL &lt;project&gt;" tmux pane border or kitty tab title. yolo sets it at launch and restores
  it at exit ([`terminal.go`](../../internal/cli/terminal.go#L29-L45)).
- **Notch.** A position on yolo's confinement dial: a container jail, macos-user, or `yolo host`.
  Defined in [the launch-PATH ruling](../reference/host-agent-environment.md#he-dir1).

Evidence is marked **MEASURED** (run and observed), **SOURCED** (read in source, docs or an issue)
or **INFERRED** (reasoned from those, not observed).

---

## 2. What herdr is and how it works

herdr source links below point at tag `v0.9.3` unless they say otherwise.

### 2.1 The project

| Fact | Value | Mark |
| :--- | :--- | :--- |
| Repository | [herdrdev/herdr](https://github.com/herdrdev/herdr). The old `ogulcancelik/herdr` redirects there | MEASURED (`gh api`) |
| First release | v0.1.0 on 2026-03-27, the day the repository was created | MEASURED (`gh release list`) |
| Latest | v0.9.3 on 2026-09-29, a same-day hotfix for v0.9.2. There are 59 stable releases | MEASURED |
| License | Apache-2.0 since 2026-07-22. AGPL before that, with a dual-licensing note from 2026-05-26 | MEASURED (the `LICENSE` commit history) |
| Team | One maintainer, Can Celik. Y Combinator Fall 2026, team size 1 | SOURCED ([blog post](https://herdr.dev/blog/herdr-is-joining-y-combinator), [YC page](https://www.ycombinator.com/companies/herdr)) |
| Stack | Rust, terminal emulation from vendored libghostty-vt, a ratatui/crossterm TUI, tokio | SOURCED ([`Cargo.toml`](https://github.com/herdrdev/herdr/blob/v0.9.3/Cargo.toml)) |
| Size | 41,451 stars, 3,188 forks and 383 open issues and pull requests at 2026-09-29T20:44Z. GitHub's `open_issues_count` counts both; at 22:00Z it was 381, which split into 361 open issues and 20 open pull requests (`gh api` search queries) | MEASURED |

**Where it is going.** The maintainer's guide describes *"migrating toward a server-owned runtime
protocol with the TUI as one client"*. The YC post names *"a sandbox for risky code and ephemeral
agents"* among the machines that *"should be connected"*. The source already mentions three
connection kinds, *"Local, SSH, and Cloud"*, and the third has no public docs
([`endpoint.rs`](https://github.com/herdrdev/herdr/blob/v0.9.3/src/protocol/endpoint.rs#L5)).
SOURCED.

### 2.2 One server per herdr session owns every terminal

- **Start, detach and reattach.** Running `herdr` starts the server for the herdr session, or
  attaches to it if it is already running. `ctrl+b q` detaches the client while every pane keeps
  running
  ([`concepts.mdx`](https://github.com/herdrdev/herdr/blob/v0.9.3/docs/next/website/src/content/docs/concepts.mdx)).
  SOURCED.
- **State lives in the config directory.** The default herdr session keeps `herdr.sock`,
  `herdr-client.sock`, `session.json` and its logs in `~/.config/herdr/`. A named herdr session
  keeps its own under `~/.config/herdr/sessions/<name>/`, and herdr honors `XDG_CONFIG_HOME`
  ([`session.rs`](https://github.com/herdrdev/herdr/blob/v0.9.3/src/session.rs#L157-L171)).
  SOURCED. The research lens's `herdr status server` printed the socket path. MEASURED.
- **The server inherits the environment of the terminal that started it.** It inherits the whole
  environment of the first client, with no clearing
  ([`autodetect.rs`](https://github.com/herdrdev/herdr/blob/v0.9.3/src/server/autodetect.rs#L194-L240)).
  So every pane's environment comes from whichever terminal started the server. SOURCED.
- **What a pane's environment holds.** MEASURED in a probe pane, and SOURCED
  ([`pane.rs`](https://github.com/herdrdev/herdr/blob/v0.9.3/src/pane.rs#L92-L117),
  [`pane.rs`](https://github.com/herdrdev/herdr/blob/v0.9.3/src/pane.rs#L162-L205)):
  - it sets `TERM=xterm-256color`, `COLORTERM`, `TERM_PROGRAM=herdr` and `TERM_PROGRAM_VERSION`;
  - it removes the outer terminal's handles: `TMUX`, `TMUX_PANE`, `KITTY_WINDOW_ID`,
    `WEZTERM_PANE`, `ITERM_SESSION_ID`, `WT_SESSION`, `STY`, `ZELLIJ*` and `LC_TERMINAL*`;
  - it removes outer agent identity: `CLAUDECODE`, `CLAUDE_CODE_SESSION_ID`,
    `CLAUDE_CODE_MESSAGING_TOKEN`, `CODEX_THREAD_ID` and `OMPCODE`;
  - it adds `HERDR_ENV=1`, `HERDR_SOCKET_PATH`, `HERDR_BIN_PATH`, `HERDR_SESSION`,
    `HERDR_WORKSPACE_ID`, `HERDR_TAB_ID` and `HERDR_PANE_ID`.
- **herdr refuses to nest.** Inside a pane, where `HERDR_ENV=1`, herdr will not start unless
  `[experimental] allow_nested = true` is set
  ([`main.rs`](https://github.com/herdrdev/herdr/blob/v0.9.3/src/main.rs#L452)). SOURCED.

### 2.3 How herdr finds an agent, and reads its state

- **Five states:** working, blocked, done (finished but not yet looked at), idle and unknown.
- **Identity comes from the foreground process group.** herdr reads the pane terminal's
  foreground process group and matches its members' names and argv against 24 known agent kinds,
  22 of which have screen manifests
  ([`detect/mod.rs`](https://github.com/herdrdev/herdr/blob/v0.9.3/src/detect/mod.rs#L43-L121)).
  SOURCED.
- **State comes from screen reading.** Rules match the live bottom of the pane plus the terminal
  title and progress strings, and output activity counts as working. **Blocked is strict:** it is
  set only when a known approval or question UI is on screen. Otherwise a known agent falls back
  to idle, and Codex falls back to unknown
  ([`agents.mdx`](https://github.com/herdrdev/herdr/blob/v0.9.3/docs/next/website/src/content/docs/agents.mdx#L71-L77)).
  SOURCED.
- **Manifests update without a binary update.** herdr downloads them from `herdr.dev`
  (`[update] manifest_check = false` stops that), and local overrides live in
  `~/.config/herdr/agent-detection/<agent>.toml`
  ([`manifest_update.rs`](https://github.com/herdrdev/herdr/blob/v0.9.3/src/detect/manifest_update.rs#L16)).
  SOURCED.
- **The agent hint.** herdr's docs name wrappers and sandboxes as the thing that hides an agent.
  They tell the user to put `HERDR_AGENT=<agent>` on the host-visible wrapper, and they warn:
  *"Herdr cannot see it if you set it only inside a VM or container"*
  ([`agents.mdx`](https://github.com/herdrdev/herdr/blob/v0.9.3/docs/next/website/src/content/docs/agents.mdx#L65-L69)).
  On Linux herdr reads the variable from `/proc/<pid>/environ` of **any** member of the
  foreground group
  ([`linux.rs`](https://github.com/herdrdev/herdr/blob/v0.9.3/src/platform/linux.rs#L714-L724),
  [`app/agents.rs`](https://github.com/herdrdev/herdr/blob/v0.9.3/src/app/agents.rs#L438-L447)).
  On macOS it reads it through `KERN_PROCARGS2`
  ([`macos.rs`](https://github.com/herdrdev/herdr/blob/v0.9.3/src/platform/macos.rs#L948-L954)).
  SOURCED.
- **The hint works on a child of the wrapper.** The research lens ran three probe panes:
  - a plain `sh -c 'sleep 300; true'` was not an agent;
  - with `HERDR_AGENT=claude` on the parent wrapper, the pane was claude;
  - with the hint only on a child in the same process group, and none on the parent, the pane
    was also claude.

  With the hint set and no Claude UI on screen, the pane read `idle`. MEASURED.
- **Upstream will not guess across a sandbox.** An issue about an agent hidden behind
  `docker exec` was closed as not planned, with the hint as the answer
  ([herdr#2999](https://github.com/herdrdev/herdr/issues/2999)). A gVisor report
  ([herdr#1982](https://github.com/herdrdev/herdr/issues/1982)) led to a server opt-in,
  `HERDR_PROCESS_DETECTION=child-groups`, for runtimes with no terminal foreground group. SOURCED.

### 2.4 Hooks, self-reports and resume commands

- **`herdr integration install <agent>` edits the agent's own config.** It honors
  `CLAUDE_CONFIG_DIR`, `CODEX_HOME`, `COPILOT_HOME` and `PI_CODING_AGENT_DIR`, and needs the
  directory to exist already
  ([`integrations.mdx`](https://github.com/herdrdev/herdr/blob/v0.9.3/docs/next/website/src/content/docs/integrations.mdx#L104-L142)).
  SOURCED.
  - **claude** gets `~/.claude/hooks/herdr-agent-state.sh` and a `SessionStart` hook in
    `settings.json`. The hook reports only which session is running. It exits unless
    `HERDR_ENV=1`, `HERDR_SOCKET_PATH` and `HERDR_PANE_ID` are all set
    ([`herdr-agent-state.sh`](https://github.com/herdrdev/herdr/blob/v0.9.3/src/integration/assets/claude/herdr-agent-state.sh),
    [`claude_settings.rs`](https://github.com/herdrdev/herdr/blob/v0.9.3/src/integration/claude_settings.rs#L19)).
  - **codex** gets `hooks.json` and `[features] hooks = true` in `config.toml`. **pi** gets an
    extension file. **copilot** gets an edit to its `settings.json`.
- **Only six sources may override the screen.** herdr grants full authority over state to six
  of its own sources (pi, omp, mastracode, opencode, kilo and kimi). Claude, Codex and Copilot
  state is always read from the screen
  ([`detect/mod.rs`](https://github.com/herdrdev/herdr/blob/v0.9.3/src/detect/mod.rs#L323-L343)).
  SOURCED.
- **Agents can report for themselves.** An agent calls
  `"$HERDR_BIN_PATH" pane report-agent` with a state and a sequence number,
  `report-agent-session`, and `release-agent` on exit
  ([`add-herdr-support.mdx`](https://github.com/herdrdev/herdr/blob/v0.9.3/docs/next/website/src/content/docs/add-herdr-support.mdx#L36-L87)).
  Self-reported state existed by 0.9.0 at the latest: herdr's `CHANGELOG.md` under 0.9.0 changes
  how `pane report-agent` and `pane report-agent-session` parse their arguments (#2926). What
  0.9.2 added is a self-reported *resume command*
  ([herdr#4687](https://github.com/herdrdev/herdr/pull/4687), *"feat: let agents report their own
  resume command"*). SOURCED.
- **Rules for a resume command.** The first word must be a plain command name, made of
  `[A-Za-z0-9_.-]`, with no path and no leading `-`. The command may have at most 64 arguments and
  8 KiB, and no apostrophes or control characters
  ([`agent_resume.rs`](https://github.com/herdrdev/herdr/blob/v0.9.3/src/agent_resume.rs#L56-L89)).
  SOURCED.
- **Who may set a resume command is looser than the docs say.** When no hook holds authority over
  the pane, herdr records a resume command from **any** source whose reported agent matches the
  pane's detected agent
  ([`state.rs`](https://github.com/herdrdev/herdr/blob/v0.9.3/src/terminal/state.rs#L1933-L1942)).
  The socket docs say a custom agent must first claim the pane. SOURCED.
- **Measured end to end.** The research lens sent
  `pane report-agent-session <pane> --source yolo --agent claude --seq N -- yolo -- claude --resume <id>`
  to a pane identified through the hint, and herdr accepted it.
  - The same call on an unidentified pane failed with `resume_not_accepted`.
  - A first word that was a path failed with `invalid_resume_argv`.
  - After the herdr session was stopped, restarted and reattached, herdr typed each stored command
    into its restored pane's shell and ran it. One of them, an `sh -c "echo … > file"`, created
    the file.

  MEASURED.

> [!NOTE]
> herdr's own page says resume commands need herdr 0.10.0
> ([`add-herdr-support.mdx`](https://github.com/herdrdev/herdr/blob/v0.9.3/docs/next/website/src/content/docs/add-herdr-support.mdx#L93)).
> They shipped in 0.9.2 and ran in 0.9.3, so that line is stale. It is recorded here only: this
> investigation does not comment upstream.

### 2.5 The socket, and its one lock

- **The API.** Newline-delimited JSON over a Unix socket. The socket is chosen by `--session`, then
  `HERDR_SOCKET_PATH`, then `HERDR_SESSION`, then the default
  ([`socket-api.mdx`](https://github.com/herdrdev/herdr/blob/v0.9.3/docs/next/website/src/content/docs/socket-api.mdx#L641-L646)),
  and `herdr api schema --json` prints the whole schema. SOURCED.
- **The number 22 is not the JSON API's.** `PROTOCOL_VERSION = 22` in
  [`wire.rs`](https://github.com/herdrdev/herdr/blob/v0.9.3/src/protocol/wire.rs#L20) versions
  herdr's private server-and-client protocol, *"the binary protocol over local sockets"*, and
  [`endpoint.rs`](https://github.com/herdrdev/herdr/blob/v0.9.3/src/protocol/endpoint.rs#L1-L9)
  calls the endpoint contract *"independent from the private binary protocol"*. The JSON API
  reports that number in its `ping` reply, `session.snapshot` and the schema document, but it
  counts incompatible changes to the binary wire format. It is not a measure of how fast the JSON
  API changes. SOURCED.
- **The JSON API does change.** Four public methods, `pane.graphics.info`, `.set`, `.clear` and
  `.stream`, were removed: they *"are no longer available. There is no replacement socket image
  API"*
  ([`socket-api.mdx`](https://github.com/herdrdev/herdr/blob/v0.9.3/docs/next/website/src/content/docs/socket-api.mdx#L182-L185)).
  SOURCED.
- **The methods reach everything.** They cover servers, notifications, herdr workspaces,
  worktrees, tabs, panes (split, type text and keys, read, report), layouts, agents (start,
  prompt, wait, read), event streams and plugins.
- **The file mode is the only lock.** The socket is created `0600`
  ([`socket_paths.rs`](https://github.com/herdrdev/herdr/blob/v0.9.3/src/server/socket_paths.rs#L11-L12),
  [`socket_paths.rs`](https://github.com/herdrdev/herdr/blob/v0.9.3/src/server/socket_paths.rs#L72-L75)).
  The API has no peer-credential check, no token and no per-pane scoping. SOURCED, and MEASURED by
  the lens with an `rg` for peer-credential and authorization code that found none.
- **Nothing upstream is scheduled.** A user reported that a read-only bind of `~/.config/herdr`
  into a bubblewrap sandbox gave the sandbox full RPC, and asked for caller tiers based on peer
  credentials ([herdr#3727](https://github.com/herdrdev/herdr/issues/3727)). It was closed as not
  planned, but as a duplicate: herdr's triage bot folded it into the still-open Ideas discussion
  [herdr#514](https://github.com/herdrdev/herdr/discussions/514) (*"I'm consolidating it
  there"*). Two related PRs were closed without merging:
  - [herdr#2434](https://github.com/herdrdev/herdr/pull/2434), a report-only listener for SSH
    panes that accepts only the five `pane.report_*` methods plus `ping`, and rejects every other
    method with `method_not_allowed`;
  - [herdr#3026](https://github.com/herdrdev/herdr/pull/3026), which reads the peer's process id
    from the kernel (`SO_PEERCRED`, `LOCAL_PEERPID`) and stamps it onto agent reports.

  So the proposal is open and unscheduled, not refused. SOURCED (`gh issue view`, `gh pr view`,
  2026-09-29).

### 2.6 Restore, worktrees, automation, notifications, plugins and remotes

**What survives what**
([`session-state.mdx`](https://github.com/herdrdev/herdr/blob/v0.9.3/docs/next/website/src/content/docs/session-state.mdx#L8-L15)),
SOURCED:

| Event | Processes | Layout | Agent conversation |
| :--- | :--- | :--- | :--- |
| Detach and reattach | keep running | kept | kept, because nothing stopped |
| herdr server restart | end | restored, with each pane's directory | only through a resume command; every other pane comes back as a fresh shell in its saved directory |
| `herdr update --handoff` | best effort, experimental, and not for brew, mise or nix installs | kept | kept if the handoff succeeds |

- **Restore details.** Restored agents start 100 ms apart (`[session] startup_per_agent_delay_ms`).
  herdr's built-in resume for Claude is `claude --resume <id>`, from a session id that herdr's own
  Claude hook reported
  ([`session-state.mdx`](https://github.com/herdrdev/herdr/blob/v0.9.3/docs/next/website/src/content/docs/session-state.mdx#L73-L115)).
  Screen history is opt-in, because it stores whatever was on screen, secrets included. SOURCED.
- **Worktrees.** `herdr worktree create` runs `git worktree add` into
  `<worktrees.directory>/<repo>/<branch-slug>`, where the directory defaults to
  `~/.herdr/worktrees`. It opens the checkout as a new herdr workspace grouped under the source,
  and `--path` puts the checkout somewhere else. `herdr worktree remove` runs `git worktree remove`
  and never deletes the branch
  ([`cli-reference.mdx`](https://github.com/herdrdev/herdr/blob/v0.9.3/docs/next/website/src/content/docs/cli-reference.mdx#L173-L186)).
  SOURCED.
- **Automation.**
  - `herdr agent start <name> --kind claude --pane <id> -- <args>` **types** the kind's canonical
    executable name, `claude`, and the arguments into an idle shell pane. It then waits up to
    30 s until herdr detects that kind in the same terminal
    ([`app/agents.rs`](https://github.com/herdrdev/herdr/blob/v0.9.3/src/app/agents.rs#L150-L230)).
  - `herdr agent prompt <pane> <text>` types text and Enter into an agent, and refuses one that is
    blocked
    ([`cli-reference.mdx`](https://github.com/herdrdev/herdr/blob/v0.9.3/docs/next/website/src/content/docs/cli-reference.mdx#L343-L359)).
  - An open issue reports that Codex's model picker reads as idle, so a prompt can answer the
    picker ([herdr#4647](https://github.com/herdrdev/herdr/issues/4647)).

  SOURCED.
- **Notifications.** They are off by default (`[ui.toast] delivery = "off"`). The choices are an
  in-app toast, an OSC 9 escape to the outer terminal, or a macOS system notification. Any socket
  client can raise one with `herdr notification show`, and `events.subscribe` streams status
  changes. SOURCED.
- **Plugins.** A plugin is a `herdr-plugin.toml` manifest of commands, event hooks and panes.
  Plugins run as the user, inherit the environment, and are neither sandboxed nor reviewed. The
  marketplace is the GitHub topic `herdr-plugin`
  ([`plugins.mdx`](https://github.com/herdrdev/herdr/blob/v0.9.3/docs/next/website/src/content/docs/plugins.mdx)).
  SOURCED.
- **Remote machines.** These go only over OpenSSH (`herdr --remote`, `herdr machine add`). With
  consent, herdr installs itself on the remote at `~/.local/bin/herdr`. SOURCED.
- **Closing a pane.** herdr sends SIGHUP to every process in the pane's terminal session, then
  SIGTERM 250 ms later, then SIGKILL 250 ms after that. It takes the list of processes before the
  first signal, and a process that called `setsid` is in another session and is never signalled
  ([`pane.rs`](https://github.com/herdrdev/herdr/blob/v0.9.3/src/pane.rs#L1616-L1660)). SOURCED.
  The lens stopped a herdr session over a script that trapped HUP and TERM. The script logged HUP
  at *t* and TERM at *t* + 262 ms, and then died with no further line. MEASURED.

### 2.7 How it is installed

- **The routes.** `curl https://herdr.dev/install.sh | sh`, `brew install herdr`,
  `mise use -g herdr`, the upstream nix flake, or nixpkgs, whose `pkgs/by-name/he/herdr` is at
  0.9.1. `herdr update` is disabled for brew, mise and nix installs. SOURCED
  ([`install.mdx`](https://github.com/herdrdev/herdr/blob/v0.9.3/docs/next/website/src/content/docs/install.mdx)),
  and MEASURED for the nixpkgs version.
- **yolo's pinned nixpkgs carries it.** Revision `e158d9ed` in [`flake.lock`](../../flake.lock)
  has herdr 0.9.1 (`nix eval`). MEASURED.
- **herdr ships an agent skill.** Its first step is to stop unless `HERDR_ENV=1`
  ([`SKILL.md`](https://github.com/herdrdev/herdr/blob/v0.9.3/skills/herdr/SKILL.md#L13)).
  SOURCED.

### 2.8 Other people's container plugins

- **[mrshll/herdr-container-agents](https://github.com/mrshll/herdr-container-agents)** (MIT,
  2026-08-21) finds a `docker exec` client in a pane's foreground job. It reads the container
  session's foreground process group from host `/proc`, claims the pane, and relays herdr's own
  verdict about that screen. It is Linux-only.
  - Its README says that once it reports, herdr stops applying screen rules. herdr's source gives
    that authority only to the six official sources, so for this plugin a visible blocker on
    screen can still override its report
    ([`state.rs`](https://github.com/herdrdev/herdr/blob/v0.9.3/src/terminal/state.rs#L2078-L2099)).
  - SOURCED.
- **[gambtho/herdr-devcontainer](https://github.com/gambtho/herdr-devcontainer)** (MIT,
  2026-08-12) opens panes that `docker exec` into a repository's Dev Container. SOURCED.

Neither is needed for yolo, because the launcher can set the hint itself
([§4.3](#43-option-2-the-launcher-tells-herdr-what-is-inside)).

---

## 3. Where it meets yolo today, notch by notch

### 3.1 A container jail

This is what happens today when you type `yolo -- claude` in a herdr pane on podman on Linux:

1. **The shell starts `yolo` as a job** in the pane's foreground process group.
2. **podman stays in yolo's process group.** yolo's TTY proxy starts the runtime client with no
   `setsid`, because *"setsid broke `podman -it`"*. So *"podman stays in this process's group ON
   THE HOST TTY"* ([`ttyproxy.go`](../../internal/ttyproxy/ttyproxy.go#L18),
   [`ttyproxy.go`](../../internal/ttyproxy/ttyproxy.go#L76-L80)). SOURCED.
3. **Claude runs outside that group.** It runs inside the container, under conmon. herdr's
   foreground job is `{yolo, podman}`, and neither is an agent. INFERRED.
4. **No herdr handle reaches the jail.** From the host terminal's environment the launcher
   forwards only `TERM`, `COLORTERM` and `NO_COLOR`
   ([`assemble.go`](../../internal/cli/run/assemble.go#L932-L938)). So the jail has no `HERDR_*`
   variable, no herdr binary and no socket. herdr's skill stops at its first check, and herdr's
   hooks would do nothing even if they were present. SOURCED. **One herdr artifact does cross
   today:** the `SessionStart` hook entry that `herdr integration install claude` writes into the
   host's `~/.claude/settings.json` reaches jail Claude through the settings layer
   ([§3.6](#36-two-writers-on-one-agent-config-file), [HR-D9](#HR-D9)).

| In a herdr pane | Today | With an agent hint on the runtime client |
| :--- | :--- | :--- |
| Typing, colour, resize | work: the proxy passes the bytes through | the same |
| herdr's status for the pane | a plain terminal | Claude's screen rules apply. It shows blocked on a visible question, and idle while the jail builds and boots (the idle fallback was MEASURED) |
| Notifications and "done" | none | from that status, when notifications are enabled |
| `herdr agent prompt <pane>` from a host script | refused: there is no agent in the pane | types into Claude. During boot it would type into yolo's output |
| `herdr agent start --kind claude` | types `claude` into the host shell, which runs a host claude, or the shell function of [§4.2](#42-option-1-a-recipe-with-no-yolo-code) | the same |
| Detaching, or closing the terminal window | the launcher keeps running: herdr owns the pane | the same |
| Closing the pane, or stopping or restarting herdr | herdr signals the launcher and the runtime client, podman forwards the hangup into the jail, and the launcher is probably SIGKILLed during its teardown (INFERRED, [§3.4](#34-closing-a-pane-is-a-kill)) | the same |
| After a herdr restart | a plain shell in the saved directory | the same, unless yolo reports a resume command ([OQ-HR2](#OQ-HR2)) |
| A herdr worktree | git is broken inside the jail ([§3.5](#35-herdrs-worktrees)) | the same |

**podman on macOS and Apple Container.** The runtime client is an ordinary process of the user's,
so herdr can read a hint on it through `KERN_PROCARGS2`. INFERRED. On those backends the launcher
has no signal handler at all ([`proxy_other.go`](../../internal/cli/run/proxy_other.go)), so a pane
close skips the teardown entirely. That is the same as a window close there today
([`jail-lifetime-last-session-wins.md` §2.2](../design/jail-lifetime-last-session-wins.md#22-which-backends-have-the-problem)).
SOURCED.

> [!WARNING]
> **yolo's kitty indicator probably misfires inside a herdr pane that runs in kitty.** herdr
> removes `KITTY_WINDOW_ID` but not `KITTY_PID`. yolo's kitty arm needs only `KITTY_PID`, and when
> the window id is missing it falls back to `--match recent:0`
> ([`terminal.go`](../../internal/cli/terminal.go#L70-L80)). So one jail pane would retitle the
> kitty tab holding all of herdr "🔒 JAIL &lt;project&gt;", and two jail panes would fight over it.
> That only happens where kitty's remote control reaches it, for example through an inherited
> `KITTY_LISTEN_ON`. INFERRED and unmeasured. The tmux arm is safe, because herdr removes `TMUX`.
> [HR-D5](#HR-D5) handles it.

### 3.2 macos-user

- **No herdr variable crosses.** The launch runs
  `sudo --user=<sandbox> /usr/bin/env -i <a closed list> sandbox-exec -f <profile> -- …`
  ([`macosuser.go`](../../internal/macosuser/macosuser.go#L770-L815)). `env -i` and the closed list
  mean no `HERDR_*` variable reaches the agent. SOURCED.
- **herdr probably cannot see the agent.** The agent runs as the sandbox account. macOS normally
  refuses to show another account's process arguments and environment without root, and `sudo`
  itself runs as root. So herdr probably cannot identify the agent, or read a hint set on it. A
  hint on the launcher, which is the human's own process, is the one herdr can read. INFERRED, and
  not measured on a Mac.
- **Whether the agent is even in the pane's foreground group is unknown.** It depends on whether
  macOS's `sudo` gives the command a terminal of its own. Unmeasured.
- **herdr worktrees break git here too.** The Seatbelt profile denies reads under `/Users` outside
  the workspace and the sandbox home
  ([`seatbelt.go`](../../internal/macosuser/seatbelt.go#L131)). INFERRED.
- **Several sessions do not share a sandbox.** macos-user has no attach, so each pane is its own
  sandbox, and the several-sessions problem of [§3.4](#34-closing-a-pane-is-a-kill) does not
  arise. SOURCED
  ([`jail-lifetime-last-session-wins.md` §2.2](../design/jail-lifetime-last-session-wins.md#22-which-backends-have-the-problem)).

### 3.3 `yolo host`

**herdr sees the agent.** `yolo host -- claude` either execs the agent
([`host.go`](../../internal/cli/host.go#L457)), or, when the launch needs a service it owns, runs
the agent as a child in the launch's foreground process group
([`agent.go`](../../internal/launchservice/agent.go#L20)). Either way herdr finds `claude` by name.
SOURCED for yolo, and INFERRED for herdr's side.

**The `HERDR_*` variables arrive, by ruling.** The agent receives the whole ambient environment.
`HERDR_*` are *carried variables* under
[the child's PATH](../reference/host-agent-environment.md#which-copy-runs):
yolo only hands them to the child and decides nothing with them. That doc's governing ruling
(2026-09-25) is that `yolo host` must not depend on its caller's environment for its own
*decisions*, and it carries session variables through on purpose. It was revised for PATH on
2026-09-29 ([HE-DIR1](../reference/host-agent-environment.md#he-dir1)): yolo's checks read the
caller's PATH. Two 2026-09-29 rulings in
[`host-tool-provisioning.md`](../design/host-tool-provisioning.md) refine that:

- **[HP-DIR2](../design/host-tool-provisioning.md#HP-DIR2), the maintainer's direction on two
  environment layers.** (1) `yolo host -- <cmd>` composes an environment on top of the user's
  PATH, project tools and mise shims included, with yolo's floor as a fallback. (2) An agent yolo
  delivers from the floor starts in a set, predictable environment: the floor's own node by
  absolute path, mise stripped.
- **[OQ-HP7](../design/host-tool-provisioning.md#OQ-HP7), ruled (a) under principle
  [HP-DIR3](../design/host-tool-provisioning.md#HP-DIR3).** Only the delivered agent's own startup
  is fixed. The commands the agent runs see the user's own shell environment.

Neither layer, as written, drops a carried variable such as `HERDR_*`: layer 2 fixes the agent's
node and strips mise, and
[the child's PATH](../reference/host-agent-environment.md#which-copy-runs)
passes carried variables through unchanged. So herdr's official hook, its skill and its native
restore all work. INFERRED; nothing was run.

**What a host launch in herdr looks like, end to end:**

- **Restore comes back through yolo, when the wrappers are on.** After a herdr restart, herdr
  types `claude --resume <id>` into a host shell. With `host_wrappers` on and the wrap directory
  first on `PATH`, `claude` is `exec yolo host -- claude`, so both restore and
  `herdr agent start` come back through yolo. `host_wrappers` is on by default under
  `host_management: "own"` (`yolo config-ref`). Without the wrappers, a bare host claude runs
  without yolo's composed environment. [OQ-HS3](../design/host-notch-services.md#OQ-HS3)'s ruling
  accepted exactly that. The maintainer: *"I'm totally fine with agents either being broken or not
  having features (same as broken) if not launched correctly once host management is on."*
  INFERRED.
- **A pane close ends the host launch cleanly.** A service the launch owns is in its own process
  group but in the same terminal session, so herdr signals it too. Its lifetime rules already
  cover a parent that dies
  ([`host-notch-services.md` §4.4](../design/host-notch-services.md#44-lifetime), step 4).
  INFERRED.

### 3.4 Closing a pane is a kill

**What yolo does on a hangup.** The launcher's handler runs the teardown in this order
([`run.go`](../../internal/cli/run/run.go#L1634-L1662)), SOURCED:

1. `stopJail`, which is `podman stop -t 5`, allowed up to 10 s
   ([`lifecycle.go`](../../internal/cli/run/lifecycle.go#L18),
   [`lifecycle.go`](../../internal/cli/run/lifecycle.go#L159-L167));
2. port-forward cleanup;
3. `stopLoopholes`;
4. `forgetGoneContainer`;
5. scratch-volume removal;
6. the config capture at quit (called E3 in the tree).

**What herdr does.** herdr signals every process in the pane's session: SIGHUP, SIGTERM about
250 ms later, and SIGKILL about 250 ms after that
([§2.6](#26-restore-worktrees-automation-notifications-plugins-and-remotes)). That list holds the
runtime client as well as the launcher, because the client stays in the launcher's process group
([§3.1](#31-a-container-jail)).

**podman forwards those signals into the container.** yolo starts the jail with
`podman run --rm -i --init … -t` ([`assemble.go`](../../internal/cli/run/assemble.go#L350-L364))
and never passes `--sig-proxy` (`rg sig-proxy internal/` finds nothing). podman's default is
*"Proxy received signals to the process (default true)"* (`podman run --help`, podman 5.8.7).
SOURCED. MEASURED here, on this jail's nested rootful podman 5.8.7: a `podman run --rm -it --init`
client, run under a Python pty over `localhost/yolo-jail:latest` with a `bash` that traps HUP, was
sent SIGHUP. The container's `bash` logged HUP and exited, and the client exited 0. So a pane close
signals the container's main process at the same moment as the launcher, and the first agent is
probably hung up directly rather than stopped by the launcher (INFERRED for yolo's real entrypoint
chain). Outside herdr, an ordinary window close hangs up the same process group, so it forwards
the same HUP today. INFERRED.

What that probably leaves:

- **The container probably stops, for that reason.** Its main process gets the forwarded HUP and
  TERM and, if it exits, the container ends with it. The `podman stop` the handler started came
  after herdr took its list of processes, so herdr does not signal it, and it probably finishes
  on its own. INFERRED.
- **The launcher is probably SIGKILLed in its teardown.** If the forwarded hangup ends the
  container quickly, step 1's `podman stop` can return early, and the teardown can get past
  step 1 before the SIGKILL at about 500 ms. Whatever has not run by then is left for the next
  launch's reaper or `yolo prune`: the sockets directory, the tracking file, the home skeleton,
  the pack tree, the scratch volumes and the config capture. The loophole daemons, which were
  started with `setsid`, are orphaned. INFERRED from the order; how far it gets is unmeasured
  ([§6](#6-what-is-unmeasured) item 3).
- **An attached pane forwards nothing.** Its runtime client is `podman exec`, which has no
  sig-proxy option (`podman exec --help`, 5.8.7). MEASURED.
- **An attached pane's agent keeps running.** An attached pane's launcher passes no teardown. Its
  agent keeps running headless in the jail, which is defect 1 of
  [`jail-lifetime-last-session-wins.md` §2.3](../design/jail-lifetime-last-session-wins.md#23-four-defects-found-on-the-way).
  SOURCED there.
- **A herdr server restart does this to every pane at once.**

**What herdr removes** is the window-close case. Closing the terminal window only detaches a herdr
client, so the launcher keeps running. SOURCED
([`session-state.mdx`](https://github.com/herdrdev/herdr/blob/v0.9.3/docs/next/website/src/content/docs/session-state.mdx#L8-L15)).

> [!IMPORTANT]
> **This was evidence for
> [OQ-JL1](../design/jail-lifetime-last-session-wins.md#OQ-JL1), which the
> maintainer directed on 2026-09-29.** That doc carries it as its
> [§3.1](../design/jail-lifetime-last-session-wins.md#31-what-a-pane-close-does-measured)
> ([HR-D6](#HR-D6)), and its answer is a **keeper**
> ([§9](../design/jail-lifetime-last-session-wins.md#9-the-keeper-design-2026-09-29)):
> one small background process per running container jail, spawned by the fresh launch before any
> host service or the container exists, that owns the jail's host services while any session is
> open and ends itself. Nothing of it is built. What this section's evidence says about it:
>
> - **It told against a launcher that holds the jail.**
>   [OQ-JL1](../design/jail-lifetime-last-session-wins.md#OQ-JL1) leaned to D:
>   after its own agent quits, the first launcher holds the jail for the others, and survives a
>   window-close hangup the way `nohup` does. Inside herdr the hangup is followed by SIGKILL within
>   0.5 s, and a launcher
>   started from a shell leads its own process group, so it cannot leave the pane's session:
>   [setsid(2)](https://man7.org/linux/man-pages/man2/setsid.2.html) fails with `EPERM` for a
>   group leader. The maintainer rejected D in his own terms, *"I don't want a solution where the
>   first terminal waits"*, and under the keeper no launcher has to outlive its pane.
> - **What survives a pane close is a process outside herdr's list.** herdr takes that list once,
>   before the first signal (`shutdown_pane_processes` calls `session_processes` once,
>   [`pane.rs`](https://github.com/herdrdev/herdr/blob/v0.9.3/src/pane.rs#L1616-L1660)). The
>   keeper is spawned with `setsid`, which a child may call because the `EPERM` applies only to a
>   group leader, so it is in a session of its own and outside the list. That doc's
>   [§3.1](../design/jail-lifetime-last-session-wins.md#31-what-a-pane-close-does-measured)
>   measured a setsid'd child surviving the pane-close sequence.
> - **No runtime client sits in a pane, so no hangup is forwarded to pid 1.** Today's attached
>   `podman run` client forwards a pane close's HUP and TERM into the container's main process
>   (the HUP was MEASURED above), and an ordinary window close does the same. Under the keeper,
>   pid 1 is a hold, a process that only keeps the container running. The keeper keeps pid 1's
>   runtime client itself, attached with no tty and `--sig-proxy=false`, and the hold ignores
>   SIGHUP and SIGINT and ends only on SIGTERM
>   ([JL-D15](../design/jail-lifetime-last-session-wins.md#JL-D15)). That doc's
>   [§3.1](../design/jail-lifetime-last-session-wins.md#31-what-a-pane-close-does-measured)
>   measured pid 1 untouched by a pane close both with the container started outside the pane
>   and with an attached client under `--sig-proxy=false`.
> - **A pane close ends only that pane's session.** Each session's launcher still dies with its
>   pane, and its signal arm never stops the jail
>   ([JL-D4](../design/jail-lifetime-last-session-wins.md#JL-D4)). Whether it also
>   hangs up its own agent in the jail, which otherwise runs on headless, is
>   [OQ-JL8](../design/jail-lifetime-last-session-wins.md#OQ-JL8), which leans to
>   ending the agent with its pane. The arm has about 500 ms before the SIGKILL, and one exec round
>   trip took 56 to 71 ms in that doc's measurement, so the hangup fits. INFERRED.
> - **The timing budget told against a successor.** B's successor, spawned only when the first
>   launcher quits, would have had to finish its handoff inside that 0.5 s with no launcher left
>   to wait for it. That is one of the reasons
>   [JL-D14](../design/jail-lifetime-last-session-wins.md#JL-D14) chose a keeper
>   started with the jail.
> - **A herdr server restart ends the jail.** Every pane's launcher is signalled at once, so no
>   session is left, and the keeper tears the jail down
>   ([§4.4](../design/jail-lifetime-last-session-wins.md#44-failure-paths)). What a
>   jail pane does when herdr brings it back is [OQ-HR2](#OQ-HR2).

### 3.5 herdr's worktrees

- **Each herdr worktree becomes its own jail.** herdr's worktree flow checks out
  `~/.herdr/worktrees/<repo>/<slug>` and opens it as a new herdr workspace, whose panes start in
  that directory. yolo takes the current directory as the workspace
  ([`runcmd.go`](../../internal/cli/run/runcmd.go#L752-L757)). So `yolo -- claude` there is a
  separate jail, with its own `.yolo/`, its own per-workspace home and its own durable directory.
  SOURCED.
- **git fails inside it.** The checkout's `.git` file names the main repository's admin
  directory by absolute path, and a container jail does not mount that path. With the main
  repository's path absent, `git status` in the worktree fails with
  `fatal: not a git repository: (null)`. MEASURED here, with git 2.55.0, in a scratch repository
  under the research scratch directory. [`durable-scratch-space.md` §2.6](../design/durable-scratch-space.md#26-a-worktree-records-absolute-paths-measured)
  measured the mirror case, a worktree made in a jail and read on the host.
- **This is not new; herdr makes it common.** Any `git worktree add` to a place outside the
  repository has the same fault. herdr makes it the default flow.
- **The other notches.** macos-user fails the same way ([§3.2](#32-macos-user)). `yolo host` is
  unaffected, because it sees real paths.
- **Removing a herdr worktree deletes its jail state.** `herdr worktree remove` runs
  `git worktree remove`, and git removed a worktree whose only extra content was an ignored
  `.yolo/` without `--force`, taking `.yolo/home` with it. MEASURED here, with git 2.55.0. So
  removing a herdr worktree also removes that jail's per-workspace home, which holds the agents'
  transcripts, and its durable directory. That matches the durable-dir design's *"deleting a
  workspace deletes its durable dir with it"*
  ([`durable-scratch-space.md` §5.2](../design/durable-scratch-space.md#52-the-durable-dir)), but
  a herdr user may not expect it. INFERRED.

[OQ-HR3](#OQ-HR3) asks what the launch should do.

### 3.6 Two writers on one agent config file

**herdr's hook leaks into jails.**

- `herdr integration install claude` adds a `SessionStart` hook to the host's
  `~/.claude/settings.json`. Its command runs `bash` on the hook script at the host's absolute
  home path
  ([`command.rs`](https://github.com/herdrdev/herdr/blob/v0.9.3/src/integration/command.rs#L27)).
  SOURCED.
- The claude pack's settings surface reads the host file as a layer (`readsHost: true`,
  [`pack.json`](../../packs/claude/pack.json#L66-L82)) and deep-merges it with no key filter
  ([`compose.go`](../../internal/agentcfg/compose.go#L389-L416)). SOURCED.
- So jail Claude runs herdr's hook at every session start, at a host path that does not exist in
  the jail. It fails, and Claude treats a failing `SessionStart` hook as a non-blocking error.
  Even with the file present, the hook would exit early for lack of `HERDR_*`. INFERRED: what the
  user sees has not been measured.
- This is the one herdr artifact that crosses into a jail today. [HR-D9](#HR-D9) owns it.

**At the host, the two writers coexist today.**

- Under `host_management: "assert"`, yolo rewrites only the keys its packs declare, and today the
  claude pack declares no `hooks`, so herdr's entry is left alone.
- Under `"own"`, herdr's edit counts as the user's: yolo captures it and re-applies it on top of
  every render.
- SOURCED from `yolo config-ref`, and INFERRED for herdr.

**What would collide.** The unbuilt design in
[`agent-event-watchers.md`](../design/agent-event-watchers.md) has the claude pack render `hooks`
entries. If that pack comes to own the `hooks` key under `assert`, herdr's entry has to survive
the rewrite. That is the same composition check the design already owes for its `Stop` hooks
([`agent-event-watchers.md` §8](../design/agent-event-watchers.md#8-costs-and-risks)).

### 3.7 herdr inside the jail instead

- **It works.** `packages: ["herdr"]` would bake herdr into the image, since the pinned nixpkgs
  carries 0.9.1 ([§2.7](#27-how-it-is-installed)). Inside the jail, detection is native, and
  herdr's skill is safe to use, because the socket reaches only jail processes. INFERRED.
- **It loses herdr's headline feature.** Detaching ends the yolo session. The herdr client is the
  jail's command, so when it exits the container stops and takes the herdr server with it
  ([`jail-lifetime-last-session-wins.md` §2](../design/jail-lifetime-last-session-wins.md#2-what-ties-a-jail-to-its-first-terminal-today),
  rows 1 and 2). The fix would be a jail that outlives every session, and
  [JL-P2](../design/jail-lifetime-last-session-wins.md#JL-P2) rules that out: *"Nothing inside the
  jail can hold the jail open."* So agents that survive the window are exactly what this shape
  gives up. INFERRED.
- **So it is a use, not a design target.** It is a way to run many agents in one confined room,
  with native detection, and not the shape to build for ([HR-D1](#HR-D1)).

### 3.8 The socket is full control of the host

A process that can reach herdr's socket can run host commands in at least four ways:

1. **`pane.split`** makes a new host shell pane, and `pane.send_text` types into it.
2. **`layout.apply`** takes a command line for each pane.
3. **`plugin.link` and `plugin.action.invoke`** run a plugin manifest's command as the user.
4. **A resume command such as `sh -c "…"`** is typed into a host shell at the next herdr restart.
   That one was MEASURED ([§2.4](#24-hooks-self-reports-and-resume-commands)).

SOURCED
([`socket-api.mdx`](https://github.com/herdrdev/herdr/blob/v0.9.3/docs/next/website/src/content/docs/socket-api.mdx)).

So no herdr handle may cross into a jail: not the socket, not `~/.config/herdr` even read-only
([herdr#3727](https://github.com/herdrdev/herdr/issues/3727)), and not `HERDR_SOCKET_PATH` or
`HERDR_BIN_PATH` ([HR-D2](#HR-D2)). If yolo ever reports a resume command, the host builds it and
the jail never supplies one ([HR-D7](#HR-D7)).

---

## 4. Integration options, smallest first

### 4.1 Option 0: nothing

- **You type:** `yolo -- claude` in a herdr pane.
- **You see:** the agent works. herdr shows a plain terminal, sends no notification, and cannot
  prompt or start the agent. Closing the pane and restarting herdr behave as in
  [§3.4](#34-closing-a-pane-is-a-kill).
- **Cost:** none.

### 4.2 Option 1: a recipe, with no yolo code

A shell function, active only inside herdr panes. This is a specimen, not run:

```bash
# ~/.bashrc or ~/.zshrc
if [ "${HERDR_ENV:-}" = 1 ]; then
  claude() { HERDR_AGENT=claude command yolo -- claude "$@"; }
  codex()  { HERDR_AGENT=codex  command yolo -- codex  "$@"; }
fi
```

- **You type:** `claude` in a pane. herdr's `agent start --kind claude` types the same word, so it
  also lands in a jail.
- **You see:** the pane shows Claude's status. It also shows idle while the jail builds and
  boots, so `agent start` reports ready early, and a following `agent prompt` could type into
  yolo's output (INFERRED from the measured idle fallback). You get no jail label. Pane close and
  restore behave as in Option 0.
- **Cost:** none to yolo. A user-guide paragraph at most.
- **A trap.** Never put `HERDR_AGENT` on the pane itself, for example through a `layout.apply`
  pane environment or `pane split --env`. The idle shell would then read as Claude too. herdr's
  docs warn against setting it globally
  ([`agents.mdx`](https://github.com/herdrdev/herdr/blob/v0.9.3/docs/next/website/src/content/docs/agents.mdx#L67)).
- **A second trap: the name.** A shell function outranks every `PATH` entry, so inside herdr panes
  the recipe's `claude` shadows the `host_wrappers` `claude` of [§3.3](#33-yolo-host). That is the
  "`claude` means two notches" problem [§4.7](#47-rejected)'s last row rejects for a yolo-built
  version. With the recipe's names, `herdr agent start --kind claude` and herdr's native restore
  both go to the jail. That breaks the native restore of a `yolo host -- claude` pane: herdr types
  `claude --resume <id>` with a host session id, and the jail has no such conversation. INFERRED.
  A distinct name, such as `jclaude`, or typing `yolo -- claude` explicitly, avoids both.

### 4.3 Option 2: the launcher tells herdr what is inside

**Recommended as the first slice.** When the launcher's environment has `HERDR_ENV=1` and
`HERDR_BIN_PATH`, it does two things.

**1. It puts the agent hint on the runtime client.** It adds
`HERDR_AGENT=<the base name of the first word after -->` to the environment of `podman run`, of
`podman exec` on an attach, and of `container run`.

- **Not into the jail.** It is never passed with `-e`.
- **Not on the launcher itself.** The runtime client starts after the pre-flights, the build, the
  staging and the config-diff prompt. The idle window then shrinks to the jail's boot.
- **Core still decides nothing about agents.** herdr ignores a name it does not know, so
  `yolo -- bash` changes nothing. herdr decides what counts as an agent.
- **The research lens measured that a hint on a child in the group is enough**
  ([§2.3](#23-how-herdr-finds-an-agent-and-reads-its-state)).

**2. It labels the pane.** A specimen:

```bash
"$HERDR_BIN_PATH" pane report-metadata "$HERDR_PANE_ID" \
  --source yolo --agent claude --title "🔒 JAIL yolo-jail"
```

- At exit it clears the label with `--clear-title`, the way the tmux arm restores its border.
- The `--agent` guard makes the title stop applying once the pane's agent changes, which should
  cover a launcher killed before it could clear it. INFERRED from
  [`socket-api.mdx`](https://github.com/herdrdev/herdr/blob/v0.9.3/docs/next/website/src/content/docs/socket-api.mdx#L751).
- **The guard limits the label to agents herdr recognizes.** herdr checks it at display time
  against the pane's current agent (`metadata_guards_match`, called from `agent_metadata_is_valid`
  in [`metadata.rs`](https://github.com/herdrdev/herdr/blob/v0.9.3/src/terminal/metadata.rs#L129-L142)).
  A label such as `bash` is accepted (`normalize_reported_agent_label` in
  [`api_helpers.rs`](https://github.com/herdrdev/herdr/blob/v0.9.3/src/app/api_helpers.rs#L191-L200)),
  but it never matches. So a pane running bare `yolo`, `yolo -- bash` or any command herdr does
  not recognize shows no jail label, and with [HR-D5](#HR-D5) standing the kitty arm down it gets
  no jail marker at all. Today's tmux and kitty indicators mark every jail. SOURCED.
  [HR-D8](#HR-D8) states that scope and names the non-agent case as a follow-up.
- The option set is SOURCED
  ([`cli-reference.mdx`](https://github.com/herdrdev/herdr/blob/v0.9.3/docs/next/website/src/content/docs/cli-reference.mdx#L309-L332)).

**What you type and see:**

- **You type:** `yolo -- claude` in a herdr pane, and nothing else.
- **You see:**
  - herdr's sidebar lists the pane as claude: working, blocked on a question, or done;
  - the pane's border reads "🔒 JAIL yolo-jail", on a split pane, or on any pane with
    `pane_borders = "always"` (see the [first real run](#built-2026-09-30));
  - herdr's notifications fire, if you turned them on;
  - `herdr agent prompt <pane> "…"` from a host script reaches the jailed agent.
- **After you quit:** the label goes, and the pane is a shell again.

**Cost: small.** The files the slice touches:

- **The label:** a herdr arm beside the tmux and kitty indicators in
  [`terminal.go`](../../internal/cli/terminal.go).
- **The hint:** one variable on the runtime client's environment. The client is spawned through
  `runWithProxy`, which is [`proxy_linux.go`](../../internal/cli/run/proxy_linux.go) over
  [`ttyproxy`](../../internal/ttyproxy/ttyproxy.go) on Linux and
  [`proxy_other.go`](../../internal/cli/run/proxy_other.go) elsewhere. Both take argv only and
  inherit the launcher's environment, and no caller sets a child environment today. So the hint
  needs an environment seam in both proxies, carried on `run.Options`
  ([`runcmd.go`](../../internal/cli/run/runcmd.go)) and set by the fresh arm and the attach arm
  in [`run.go`](../../internal/cli/run/run.go). The cheaper alternative is to set the variable in
  the launcher's own environment with `os.Setenv` just before the spawn. That still leaves the
  launcher's starting environment, the one herdr reads, untouched ([HR-D3](#HR-D3)), but every
  later subprocess of the launcher would inherit it.
- **Tests:** that `HERDR_ENV=1` puts the hint on the runtime client, that no `HERDR_*` appears in
  the container's argv, and that the label's argv goes through a seam like `tmuxCmd`.
- The kitty fix of [HR-D5](#HR-D5) rides along.

**No ruling decides whether a jail launch may do this, and [OQ-HR1](#OQ-HR1) carries it.** The
predictability ruling, in [`host-agent-environment.md`'s launch PATH](../reference/host-agent-environment.md#the-launch-path-and-which-copy-of-a-program-runs),
governs `yolo host`. [OQ-HE7](../reference/host-agent-environment.md#oq-he7) asked whether to extend
it to a jail launch's host-side PATH lookups: podman, nix and the macOS tools. It was retired on
2026-09-29 by [HE-DIR1](../reference/host-agent-environment.md#he-dir1), so a jail launch keeps
finding those on the PATH it was started with. That settles PATH lookups and nothing else. Nobody
has asked whether a jail launch may decide from its launcher's session variables, or run a program
one of them names. So ruling [OQ-HR1](#OQ-HR1) (A) or (B) also rules it, for the herdr arm. The
change adds nothing the agent sees.

- **This is not output styling.** The exemption in
  [the child's PATH](../reference/host-agent-environment.md#which-copy-runs)
  covers *"Output styling (`NO_COLOR`, color detection)"*. Option 2 reads `HERDR_ENV` to decide
  whether to run a host program, and the program is the one the ambient `HERDR_BIN_PATH` names.
  That is a decision input: which program to run.
- **The precedent is exact, and it is built, not ruled.** The tmux arm already reads `TMUX` to
  decide to act, and runs `tmux` from the ambient `PATH` (`tmuxCmd` in
  [`terminal.go`](../../internal/cli/terminal.go)). The kitty arm does the same with `KITTY_PID`
  and `kitten`. No ruling covers those arms either. The herdr arm differs in one respect: under
  Option 2 it runs the program the ambient `HERDR_BIN_PATH` names, where the other two look a
  name up on `PATH`. The narrower variant below removes that difference.
- **A narrower variant** resolves `herdr` by name on `PATH`, rather than executing
  `$HERDR_BIN_PATH` blindly. That narrows the decision input to whether and where to label,
  from `HERDR_ENV` and `HERDR_PANE_ID`.

**Not at `yolo host`.** No jail label is applied there. The label reads "🔒 JAIL &lt;project&gt;",
and `yolo host` is the unconfined notch, so it would be a false safety signal. Today
`SetupJailIndicator` runs only on the jail path, in `runRun`
([`commands.go`](../../internal/cli/commands.go#L1179)), never at `yolo host`. The hint is not
needed there either, because the agent is in the foreground group itself. Reading `HERDR_ENV` at
`yolo host` would also make `HERDR_*` a decision input there, against [§3.3](#33-yolo-host)'s
"yolo only hands them to the child". If a host label is ever wanted, it must name the notch
accurately and be classified under
[the child's PATH](../reference/host-agent-environment.md#which-copy-runs)
as a decision input.

**How to verify it.** A nested jail can show the hint on rootful nested podman: run herdr inside
this jail, and the freshly built launcher in one of its panes. One part a nested jail cannot show:
whether herdr can read the environment of a *rootless* podman process that has re-entered its user
namespace. That is the rootless carve-out in [`AGENTS.md`](../../AGENTS.md#testing), and only a
real rootless host settles it. INFERRED.

**macos-user.** herdr can read only the launcher. A process cannot add a variable to the
environment it started with, and that starting environment is what herdr reads. So the launcher
would have to re-exec itself with the hint. Until a Mac measures it, macos-user is left out.
INFERRED.

**What it fixes:** status and the label. **What it does not fix:** pane close, restore and
worktrees.

#### Built, 2026-09-30

[`herdragent.go`](../../internal/cli/run/herdragent.go), called from `run.Run` above the backend
dispatch. It does what this section describes, with three differences:

- **A self-report as well as the hint.** It also runs `herdr pane report-agent <pane> --source
  yolo-jail --agent <bin> --state unknown`, and `release-agent` at exit. That identifies the pane
  from the start of the launch, and on macos-user, where no runtime client carries the hint. The
  `yolo-jail` source matches a hand-written wrapper some users already run, so a machine running
  both ends up with one registration. Whether herdr accepts `report-agent-session` on a pane identified
  this way, without the hint, is unmeasured ([§2.4](#24-hooks-self-reports-and-resume-commands)
  measured only the hint), so Option 3 should rely on the hint.
- **Which panes it acts in.** Only a launch whose command is a program a selected pack installs
  (`Pack.InstallBins`) gets the hint, the report or the label. `yolo -- bash` and bare `yolo` get
  none of them, as [HR-D8](#HR-D8) scopes the label. Core still names no agent.
- **The narrower variant.** It runs the `herdr` found on `PATH`, never the ambient
  `HERDR_BIN_PATH`.

Also built: the hint goes on `runtimeClientEnv`, which only `runArmedSession` reads, so it reaches
each session's runtime client and nothing else ([HR-D3](#HR-D3)). The kitty arm stands down in a
herdr pane ([HR-D5](#HR-D5)), detected by `HERDR_ENV=1` as well as `TERM_PROGRAM=herdr`, because
herdr 0.7.5 leaves the outer terminal's `TERM_PROGRAM` set and does not set `HERDR_BIN_PATH`.
MEASURED in a herdr 0.7.5 pane on macOS. `YOLO_NO_HERDR=1` turns all of it off. A failed report
prints one line, and the launch continues with only the hint.

**First real run, 2026-09-30, herdr 0.7.5, podman on macOS, iTerm2.** A `yolo -- claude` built
from this slice made herdr's sidebar show Claude's status, the same result as the hand-written
wrapper. MEASURED.

**The label shows only on a pane border, and by default only a split pane has one.** In 0.9.3
herdr draws the reported title in exactly one place: the pane's border label, where it takes
priority over a manual pane name (`border_label` in `src/terminal/state.rs`, called only from
`src/ui/panes.rs`). The sidebar never shows it. With the default `[ui] pane_borders = "auto"`,
herdr draws borders only around split panes, so a pane alone in its tab has nowhere to show the
label. `pane_borders = "always"` also frames a lone pane. Both SOURCED at `v0.9.3`. A
`report-metadata --title "🔒 JAIL probe"` probe, sent with this slice's `--source` and `--agent`
guard, did not appear while its pane was alone in the tab, on 0.7.5 or 0.9.3. Once the tab was
split, it appeared on the pane's border on 0.9.3. MEASURED. So the label in [§4.3](#43-option-2-the-launcher-tells-herdr-what-is-inside) shows
only on a split pane, unless the user turns borders on for every pane.

Not yet tested: the label and status clearing after a normal quit and after Ctrl-C, and the hint
on the podman client (`ps eww`). So the run does not tell whether the status came from the hint
or from the self-report.

### 4.4 Option 3: resume after a herdr restart

- **The idea.** The launcher reports a resume command it builds itself:
  `yolo <its own flags> -- <program> <that pack's resume arguments> <session id>`. It sends the
  report through `"$HERDR_BIN_PATH" pane report-agent-session --source yolo`. herdr accepts it on
  a pane it has identified through the hint, which was MEASURED
  ([§2.4](#24-hooks-self-reports-and-resume-commands)). So this depends on Option 2.
- **What it needs** (INFERRED; a sketch, not a design):
  - **A jail-to-host channel for the session id.** For example, each agent pack's own session
    hook writes the id to a per-session file that the host reads without following links.
  - **Validation on the host** against a strict character set.
  - **A resume template in each agent pack**, such as claude's `--resume <id>`, codex's
    `resume <id>` and pi's `--session <id>`, so core still knows no agent.
  - **A per-session key.** Several panes share one jail, so `--continue`, which resumes the most
    recent conversation in the workspace, is wrong.
- **You see:** after a reboot, each jail pane starts again in its own conversation, 100 ms apart.
  The first pane launches the jail fresh and the rest attach.
- **Cost: medium.** [OQ-HR2](#OQ-HR2) asks whether to build it.

### 4.5 Option 4: git in herdr's worktrees

- **The idea.** When the workspace is a linked worktree whose main repository is outside it, the
  launch mounts that repository's git directory into the jail, at its host path, read-write, and
  says so at launch.
- **The rest of the mechanism:**
  - the macos-user profile gets a matching read-write allowance;
  - a path inside the credential boundary is refused by the same check that guards the workspace
    ([`workspacescope.go`](../../internal/paths/workspacescope.go)).
- **You see:** git works in a herdr worktree jail, and the launch prints one line naming the extra
  mount.
- **Cost: small to medium.** [OQ-HR3](#OQ-HR3) asks whether to build it.

**What a bind of the repository's `.git` does to git.** MEASURED on 2026-10-01
([§6](#6-what-is-unmeasured) item 8), with the rootful podman 5.8.7 nested in a jail, git 2.55.0
and the jail image `localhost/yolo-jail` (image ID `b617a1ad0710`). Every container was started
with `--read-only` and ran as uid 0. A scratch repository under `/tmp` was laid out the way
herdr leaves one: the linked worktree's `.git` file names `/home/u/code/repo/.git/worktrees/slug`,
and that directory's `gitdir` file names the checkout's host path,
`/home/u/.herdr/worktrees/repo/slug/.git`. The checkout was bound at `/workspace`, and the
repository's `.git` at its host path, in four ways. "Outside" below is the outer jail, standing
in for the host.

| The `.git` bind | `git status` | `git add` and `git commit` | `git worktree add` |
| :--- | :--- | :--- | :--- |
| read-write, as Option 4 has it | exit 0 | exit 0, and the commits are on the `slug` branch outside | exit 0 |
| read-only | exit 0, and silent with a stat-dirty index | exit 128, `--allow-empty` included: `fatal: Unable to create '/home/u/code/repo/.git/worktrees/slug/index.lock': Read-only file system` | exit 128 with `--detach`: `fatal: could not create directory of '/home/u/code/repo/.git/worktrees/wt3': Read-only file system`. Exit 255 with `-b`: `fatal: cannot lock ref 'refs/heads/wt2'` |
| read-only, with `worktrees/slug` read-write over it | exit 0 | `git add` exit 128: `error: unable to create temporary file: Read-only file system`, then `fatal: adding files failed`, so nothing is staged to commit. `git commit --allow-empty` exit 128: `fatal: failed to write commit object` | as read-only |
| read-write, with `config` and `hooks` read-only over it | exit 0 | exit 0 | exit 0 |

- **Only a writable `.git` commits.** The objects and refs live in the repository's `.git`
  itself, not in the worktree's own directory under it, `worktrees/slug`. So a read-only bind
  keeps `status` and `log` working and refuses every write. `git hash-object -w` failed the same
  way, exit 128 with `error: unable to create temporary file: Read-only file system`. MEASURED.
- **The jail sees its own checkout as prunable.** The `gitdir` file names the checkout's host
  path, and the jail mounts the checkout at `/workspace` instead. So `git worktree list --porcelain`
  marks it `prunable gitdir file points to non-existent location`. With the bind read-write:
  - `git worktree prune -v` printed
    `Removing worktrees/slug: gitdir file points to non-existent location`, exited 0, and the
    directory was gone outside the container too;
  - every git command after it failed with `fatal: not a git repository: (null)`, exit 128, and the
    `slug` branch survived;
  - `git gc` did the same once the worktree's `index` file was four months old, and failed mid-run
    with `fatal: not a git repository: '/home/u/code/repo/.git/worktrees/slug'` and
    `fatal: failed to run rerere`, exit 128. It kept the directory while the `index` was fresh,
    even with only `gitdir` aged.

  MEASURED. git's default grace period for that is three months
  ([`gc.adoc`](https://github.com/git/git/blob/v2.55.0/Documentation/config/gc.adoc#L107-L113),
  git 2.55.0). SOURCED.
- **This is not new with Option 4.** A jail started in the main checkout also lists the outside
  worktree as prunable, and `git worktree prune -n -v` names it for removal. MEASURED. So a jail in
  the main checkout can already delete `.git/worktrees/<name>`, the directory git keeps for each
  linked worktree, for every herdr worktree of that repository. Option 4 adds the same reach from
  a worktree jail, its own checkout included. INFERRED from the same listing.
- **Two things kept the checkout.** Binding it a second time, at its host path beside
  `/workspace`, cleared `prunable`, and `git worktree prune -v` then removed nothing. Locking it
  worked by the path git recorded, and not by the path the jail sees:
  `git worktree lock --reason probe /home/u/.herdr/worktrees/repo/slug` exited 0, and
  `prune -n -v` then named nothing. `git worktree lock slug` worked too. `git worktree lock /workspace`
  exited 128 with `fatal: '/workspace' is not a working tree`, and `git worktree lock .` the same.
  MEASURED. git accepts a unique trailing part of a worktree's recorded path as its name
  ([`git-worktree.adoc`](https://github.com/git/git/blob/v2.55.0/Documentation/git-worktree.adoc#L289-L295),
  git 2.55.0). herdr does not lock the worktrees it makes: its two `git worktree add` commands pass
  no `--lock`
  ([`worktree.rs`](https://github.com/herdrdev/herdr/blob/v0.9.3/src/worktree.rs#L239-L278)).
  SOURCED.
- **A worktree made in the jail stays registered outside it.** `git worktree add -b wt2 /tmp/wt2`
  registered `worktrees/wt2` with the `gitdir` `/tmp/wt2/.git`, a path in the container's own
  `/tmp`, which ended with the container. Outside, `git worktree list` marks it prunable and names
  its `wt2` branch. MEASURED.
- **A read-only `config` over a read-write `.git` lasts until the next write outside.** In the
  jail, `git config probe.key value` exited 4 with
  `error: could not write config file /home/u/code/repo/.git/config: Device or resource busy`, and
  writing `hooks/post-commit` failed with `Read-only file system`. Then one `git config` outside the
  container, which replaces the file by renaming a new one over it, removed the jail's `config`
  mount, and the jail's `/proc/self/mountinfo` no longer listed it. The jail's next
  `git config jail.key 1` exited 0, and the line was in the outside file. The `hooks` mount
  stayed. MEASURED, on Linux 7.2.7. The kernel refuses a rename onto a mountpoint of the caller's
  own mount namespace with `EBUSY`, and after a rename from any other namespace detaches every
  mount on the replaced file
  (`vfs_rename`'s [refusal](https://github.com/torvalds/linux/blob/v7.2/fs/namei.c#L6040-L6042)
  and [detach](https://github.com/torvalds/linux/blob/v7.2/fs/namei.c#L6079-L6086), Linux 7.2).
  SOURCED.
- **Ownership.** Everything git wrote was owned by uid 0 outside, the uid the container ran as and
  the uid that owned the scratch repository, so these runs could not exercise git's ownership
  check (`safe.directory`). git left the `gitdir` file as it was, read back after the first read-write and read-only
  runs. MEASURED. A rootless host maps uids differently and is unmeasured.

### 4.6 Option 5: a narrow herdr door for jailed agents

- **The idea.** A loophole (a host capability deliberately opened into a jail through a mediated
  channel, as [`loophole-system.md`](../reference/loophole-system.md) defines it). It would take
  the jail's requests on the host and forward only an allowlist to herdr for the jail's own pane,
  such as a toast or a label. It would refuse every method that starts or types into anything.
- **You see:** a jailed agent can raise a herdr toast, for example "done, look at pane 2". It
  cannot open panes.
- **Cost: medium, and it keeps growing.** The allowlist is the whole boundary, over an API that
  changes: it has already removed four public `pane.graphics.*` methods with no replacement
  ([§2.5](#25-the-socket-and-its-one-lock)).
- **Prior art that would make it cheap.** herdr's unmerged
  [herdr#2434](https://github.com/herdrdev/herdr/pull/2434) is a report-only listener: the five
  `pane.report_*` methods plus `ping`, everything else refused. If upstream ships a listener of
  that shape, the label half of this door, and self-reported agent state, would need no filter
  written by yolo. It does not include `notification.show`, so a toast would still need yolo's
  own filter. INFERRED.
- [OQ-HR4](#OQ-HR4) asks whether to build it.

### 4.7 Rejected

| Idea | Why not |
| :--- | :--- |
| Bind-mount herdr's socket, or `~/.config/herdr`, into the jail | Host code execution ([§3.8](#38-the-socket-is-full-control-of-the-host)) |
| Forward herdr's official hooks and socket into the jail so herdr sees a jailed Claude natively | Decisively, the socket crossing into the jail is host code execution ([§3.8](#38-the-socket-is-full-control-of-the-host)). Restore is wrong too: herdr would store a jailed session id under its own Claude source, and at the next restart type `claude --resume <id>` into a host shell. That resumes at the host notch whether or not wrappers are on, since with them `claude` is `yolo host -- claude`. It would most likely find no such conversation, because a jailed Claude's transcripts live in the workspace overlay (`<workspace>/.yolo/home/claude/…`, [`AGENTS.md`](../../AGENTS.md)), not in the host's `~/.claude/projects`. INFERRED from [`session-state.mdx`](https://github.com/herdrdev/herdr/blob/v0.9.3/docs/next/website/src/content/docs/session-state.mdx#L73-L115) |
| Deliver `yolo notify` pings through `herdr agent prompt` | The text arrives as if the user typed it, which [EW-P3](../design/agent-event-watchers.md#EW-P3) rules out, and [herdr#4647](https://github.com/herdrdev/herdr/issues/4647) shows a prompt can answer a picker ([HR-D4](#HR-D4)) |
| herdr inside the jail as the supported shape | Detaching ends the jail ([§3.7](#37-herdr-inside-the-jail-instead)) |
| A herdr plugin shipped by yolo, in the style of herdr-container-agents | It would rebuild the agent hint by reading host `/proc`, as unsandboxed plugin code, when the launcher can simply set the hint |
| yolo generating a host `claude` that launches the jail | `host_wrappers` already own the bare name on the host for `yolo host`. A second meaning would make `claude` mean two notches. The recipe of [§4.2](#42-option-1-a-recipe-with-no-yolo-code) stays the user's |

### 4.8 Implementation decisions

Each of these has one sensible answer, so none is asked.

| ID | Decision | Rests on |
| :--- | :--- | :--- |
| <a id="HR-D1"></a>HR-D1 | *Implementation decision.* **herdr runs on the host, and yolo runs in its panes.** herdr inside a jail works with `packages: ["herdr"]` and is documented, not built for | [§3.7](#37-herdr-inside-the-jail-instead), [JL-P2](../design/jail-lifetime-last-session-wins.md#JL-P2) |
| <a id="HR-D2"></a>HR-D2 | *Implementation decision.* **No herdr handle crosses into a jail**: no socket, no `HERDR_SOCKET_PATH`, `HERDR_BIN_PATH` or `HERDR_PANE_ID`, and no `~/.config/herdr`. True today. The one herdr artifact that does cross today is not a handle: the host `SessionStart` hook entry of [§3.6](#36-two-writers-on-one-agent-config-file), owned by [HR-D9](#HR-D9). Once Option 2 reads `HERDR_*`, a test pins that none reaches the container's argv. The user guide says that mounting `~/.config/herdr` exposes the socket | [§3.8](#38-the-socket-is-full-control-of-the-host) |
| <a id="HR-D3"></a>HR-D3 | *Implementation decision.* **The agent hint goes on the runtime client only.** It is never passed with `-e`, and never set on the launcher's own starting environment. Its value is the command's base name, and herdr decides whether that is an agent | [§4.3](#43-option-2-the-launcher-tells-herdr-what-is-inside) |
| <a id="HR-D4"></a>HR-D4 | *Implementation decision.* **`yolo notify` never delivers through `herdr agent prompt`.** herdr's notifications remain the human's signal, beside `yolo notify` | [EW-P3](../design/agent-event-watchers.md#EW-P3) |
| <a id="HR-D5"></a>HR-D5 | *Implementation decision.* **The kitty indicator stands down when `TERM_PROGRAM=herdr`,** and the herdr label takes its place | [§3.1](#31-a-container-jail) |
| <a id="HR-D6"></a>HR-D6 | *Implementation decision.* **The pane-close kill is [OQ-JL1](../design/jail-lifetime-last-session-wins.md#OQ-JL1)'s input, not a herdr-specific fix.** The evidence of [§3.4](#34-closing-a-pane-is-a-kill) is carried into that doc, and no herdr-only teardown path is built. What is carried: (1) podman forwards a pane close's HUP (MEASURED) and TERM (its default proxies every received signal) into the container, so under D the hold at pid 1 needs `--sig-proxy=false`, a detached start, or to ignore forwarded signals, and this holds for an ordinary window close too; (2) what survives a pane close is any process outside herdr's one-time process list: A's keeper, or a detached process the launcher spawns on the hangup, within about 500 ms (250 ms if SIGTERM ends the launcher). So B's successor, or a D-plus-successor hybrid, survives too. *Carried 2026-09-29:* that doc's [§3.1](../design/jail-lifetime-last-session-wins.md#31-what-a-pane-close-does-measured) holds the evidence, and [OQ-JL1](../design/jail-lifetime-last-session-wins.md#OQ-JL1) was directed to A's keeper, so the conditionals on D and B no longer apply. The keeper design takes both halves: pid 1's runtime client runs with `--sig-proxy=false` and the hold ignores SIGHUP ([JL-D15](../design/jail-lifetime-last-session-wins.md#JL-D15)), and the keeper is spawned with `setsid` ([§9.1](../design/jail-lifetime-last-session-wins.md#91-what-starts-it)) | [§3.4](#34-closing-a-pane-is-a-kill) |
| <a id="HR-D7"></a>HR-D7 | *Implementation decision.* **If yolo reports a resume command, the host builds it.** The jail supplies at most a session id, checked against a strict character set. herdr types whatever it stores into a host shell | [§2.4](#24-hooks-self-reports-and-resume-commands), MEASURED |
| <a id="HR-D8"></a>HR-D8 | *Implementation decision.* **The pane label carries the `--agent` guard and is cleared at exit,** so a killed launcher does not leave a stale jail title. **Scope:** herdr shows a guarded label only while the pane's detected agent matches it, so the label appears only for agents herdr recognizes. A jail running bare `yolo`, `yolo -- bash` or an unrecognized command gets no herdr marker. The non-agent case is a follow-up, with one candidate: an unguarded label with `--ttl-ms`, refreshed by the launcher, so a killed launcher's label expires | [§4.3](#43-option-2-the-launcher-tells-herdr-what-is-inside) |
| <a id="HR-D9"></a>HR-D9 | *Implementation decision.* **herdr's host `SessionStart` hook reaching jail Claude is a follow-up for the claude pack's host-layer composition, not part of the herdr slice.** It crosses today through the `readsHost` settings layer, which folds host keys with no filter ([§3.6](#36-two-writers-on-one-agent-config-file)). First measure what the user sees ([§6](#6-what-is-unmeasured) item 5). Then either drop, and disclose, a host-layer hook command whose absolute host path does not exist in the jail, or accept the failing hook and document it. The choice covers every host hook, not only herdr's, so it belongs with the pack's composition rules | [§3.6](#36-two-writers-on-one-agent-config-file) |

### 4.9 Folded in: the terminal-multiplexer design

A second design, `terminal-multiplexer-integration.md` (its questions [OQ-TM1](#OQ-TM1) to [OQ-TM4](#OQ-TM4), anchored in the table below), was written on
2026-09-30 on a branch that started before this doc landed. It shipped with the first version of
the registration above. Its useful parts are recorded here, and the doc itself is not carried, so
there is one place for herdr questions.

**Other multiplexers.** Both of these are read from code and docs, and neither was run:

- **mato** decides a tab is active from output recency alone and has no API.
- **cmux** reads terminal notification escapes (OSC 9, 99 and 777). Its socket can also send input
  and read screens, so it must never be relayed, for the reason [§3.8](#38-the-socket-is-full-control-of-the-host)
  gives for herdr's.

On Linux the TTY proxy copies output bytes unchanged
([`ttyproxy.go`](../../internal/ttyproxy/ttyproxy.go)), and elsewhere the runtime writes to the
launcher's own stdout. So both should work with a jail as they are. herdr is the only one with a
registration API, so no general multiplexer abstraction is built.

**Its jail-side pack, and where each part belongs here.** It proposed an opt-in `herdr` pack with
four parts: hook entries in each agent's generated config, a `yolo internal herdr-hook` client
inside the jail, a loophole forwarding herdr's report calls for the launching pane, and resume.

| Its question | What it asked | Where it belongs here |
| :--- | :--- | :--- |
| <a id="OQ-TM1"></a>[OQ-TM1](#OQ-TM1) | Build the jail side now, or wait | [OQ-HR2](#OQ-HR2) and [OQ-HR4](#OQ-HR4). Its leaning, wait, matches theirs |
| <a id="OQ-TM2"></a>[OQ-TM2](#OQ-TM2) | What a resume command reported from a jail becomes | [OQ-HR2](#OQ-HR2) C, **under [HR-D7](#HR-D7)**. See the correction below |
| <a id="OQ-TM3"></a>[OQ-TM3](#OQ-TM3) | What the loophole forwards | [OQ-HR4](#OQ-HR4) B. Its report-only allowlist is the same as herdr#2434's listener |
| <a id="OQ-TM4"></a>[OQ-TM4](#OQ-TM4) | Where per-agent hook entries live | [HR-D9](#HR-D9), and the deliverer question in [`agent-event-watchers.md`](../design/agent-event-watchers.md). Its leaning, entries in each agent's pack, agrees with that doc |

> [!WARNING]
> **Its resume design broke [HR-D7](#HR-D7), and it is corrected here.** It had the in-jail
> `herdr-hook` client rewrite the resume command to `yolo -- <agent> --resume <id>` *"before it
> leaves the jail"*, then forward it through the loophole. herdr types a stored resume command into
> a host shell at its next restart. That was MEASURED with an `sh -c` command
> ([§2.4](#24-hooks-self-reports-and-resume-commands)). So a resume command built inside the jail
> is a way for the jail to run host commands, whatever rewrite the jail claims to have done. Under
> HR-D7 the jail supplies a session id at most. The host checks it against a strict character set
> and builds the command from the agent pack's resume template
> ([§4.4](#44-option-3-resume-after-a-herdr-restart)). A loophole that forwards
> `report_agent_session` from a jail must refuse any resume command the jail supplies.

Two of its claims are corrected by this doc's evidence:

- *"herdr's own hook integrations cannot close the gap: `herdr integration install claude` writes
  the HOST's `~/.claude`, which the jail never reads."* Jail Claude does read host keys: the
  `SessionStart` entry crosses through the unfiltered `readsHost` settings layer
  ([§3.6](#36-two-writers-on-one-agent-config-file), [HR-D9](#HR-D9)). The conclusion still holds:
  the hook exits without `HERDR_*` and reports nothing.
- *"herdr's full command set was not checked."* It was. [§3.8](#38-the-socket-is-full-control-of-the-host)
  lists four ways to run host commands through the socket.

---

## 5. Open questions

1. 💬 <a id="OQ-HR1"></a>**[OQ-HR1](#OQ-HR1): Where does yolo's herdr knowledge live?**

   **Setup.** Matt runs herdr on his laptop with four panes: two `yolo -- claude` in yolo-jail, one
   `yolo host -- codex`, and a shell. herdr's sidebar shows the codex pane's status. The two jail
   panes are plain terminals, so he misses that one of them has been waiting on a question for
   twenty minutes. The fix is two small behaviors in the launcher
   ([§4.3](#43-option-2-the-launcher-tells-herdr-what-is-inside)). It is a question because
   [`AGENTS.md`](../../AGENTS.md#packs-and-what-core-does-not-know) says core does not know what an
   agent is, and herdr is a named third-party program. A or B also decides a point no other ruling
   covers: that a jail launch decides from its launcher's `HERDR_ENV` and runs a host program, as
   the tmux and kitty arms already do from `TMUX` and `KITTY_PID`
   ([§4.3](#43-option-2-the-launcher-tells-herdr-what-is-inside)). C does not.

   **A was built on 2026-09-30, ahead of this ruling** ([built note](#built-2026-09-30)). Ruling B
   or C means removing it.

   - **A. In core, beside the tmux and kitty indicators.** *You see:* every herdr pane running
     `yolo -- <agent>` shows that agent's status and "🔒 JAIL &lt;project&gt;", with no config.
   - **B. In a shipped `herdr` pack.** *You see:* the same, after you add `"herdr"` to `packs`. It
     needs a new contribution kind that sets environment on the host-side runtime client and runs
     a host command at launch and exit. That kind's only user would be herdr, and it would put host
     execution in a pack's hands.
   - **C. Documentation only,** the recipe of [§4.2](#42-option-1-a-recipe-with-no-yolo-code).
     *You see:* status only after you add a shell function. There is no label, and
     `agent start` reports ready early.

   <!-- vantage: oq id=OQ-HR1 leaning="A, core beside the tmux and kitty indicators: the precedent is exact, the hint must sit on a host process yolo spawns that no pack kind reaches, and the value is the command's own name, so core still decides nothing about agents." -->

   _Leaning:_ **A.** The precedent is exact: yolo already marks tmux panes and kitty tabs from core
   ([`terminal.go`](../../internal/cli/terminal.go#L29-L45)), and those are named third-party
   programs too. The hint has to sit on a host process yolo spawns, which no pack kind reaches.
   Its value is the command's own name, so herdr, not core, decides what is an agent. B adds a
   host-execution kind for nothing a user would notice.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 <a id="OQ-HR2"></a>**[OQ-HR2](#OQ-HR2): What does a jail pane do after a herdr restart?**

   **Setup.** Matt has four herdr panes, each running `yolo -- claude` in the same repository: one
   jail, four sessions. He reboots. herdr restores the layout: four panes, each a plain shell in
   the repository, with nothing running. Today he types `yolo -- claude --resume` four times and
   picks each conversation from Claude's list. It is a question because herdr would retype a
   command for him if yolo reported one, but the conversation id lives inside the jail, and each
   cheap variant has a cost.

   - **A. Report nothing,** as today. *You see:* four shells, and you relaunch by hand.
   - **B. Report the launch's own command,** `yolo -- claude`, with no id. *You see:* four agents
     start by themselves, 100 ms apart, in one jail. The panes look restored, but every
     conversation is new, and the old ones are reachable only through Claude's own resume picker.
   - **C. Report `yolo -- claude --resume <id>`, built on the host**
     ([§4.4](#44-option-3-resume-after-a-herdr-restart)). *You see:* each pane comes back in its
     own conversation. The cost is a jail-to-host channel for the id, a resume template in each
     agent pack, and a per-session key. It depends on Option 2.

   <!-- vantage: oq id=OQ-HR2 leaning="C, resume the right conversation, built as a second slice after the launcher sets the hint; A until then; never B, because a pane that looks restored but lost its conversation is worse than an honest empty shell." -->

   _Leaning:_ **C, as a second slice after Option 2, and A until then. Not B.** A pane that looks
   restored but has lost its conversation is worse than an honest empty shell. C is the only
   variant that gives back what the reboot took.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 <a id="OQ-HR3"></a>**[OQ-HR3](#OQ-HR3): What does a container jail do when its workspace is
   a worktree of a repository outside it?**

   **Setup.** Matt presses herdr's worktree key in yolo-jail. herdr runs
   `git worktree add ~/.herdr/worktrees/yolo-jail/<slug>` and opens a new herdr workspace there.
   In its pane he runs `yolo -- claude`. The jail starts with that directory as `/workspace`, and
   every git command inside fails with `fatal: not a git repository: (null)`
   ([§3.5](#35-herdrs-worktrees)). The checkout's `.git` file points at the main repository's
   `.git` under `~/code`, which the jail never mounted. Any `git worktree add ../x` does the same;
   herdr only makes it the everyday flow. It is a question because the fix widens what a jail can
   write.

   - **A. Mount the main repository's git directory at its host path, read-write, and disclose it**
     ([§4.5](#45-option-4-git-in-herdrs-worktrees)). *You see:* git works, and the launch prints
     one line naming the extra mount. The jail can then write that repository's refs, config and
     hooks. A jail started in the main checkout can already do exactly that, but this jail was
     started somewhere else. A path inside the credential boundary is refused.
   - **B. Refuse, or warn, at launch.** *You see:* a message naming the fault and the ways round
     it (`yolo host`, or a worktree the agent makes inside the jail under `$YOLO_DURABLE_DIR`),
     instead of a git that fails later with `(null)`.
   - **C. Leave it as it is.** *You see:* git fails inside the jail with
     `fatal: not a git repository: (null)`.

   <!-- vantage: oq id=OQ-HR3 leaning="A, mount the main repository's git directory at its host path read-write and disclose it: git needs it to work at all, it grants what a jail in the main checkout already has, and B leaves herdr's headline flow broken." -->

   _Leaning:_ **A.** git cannot work in a linked worktree without its main repository's git
   directory, read-write, because commits land there. That directory is what a jail in the main
   checkout already writes, so A grants no new kind of power, and the disclosure says where. B is
   honest but leaves herdr's main worktree flow broken in every container jail. If A is ruled, B's
   message is still the fallback where the path is refused.

   _Measured since the leaning_ (2026-10-01, rootful podman,
   [§4.5](#45-option-4-git-in-herdrs-worktrees)). A's premise holds: with the `.git` bound
   read-write at its host path, the worktree commits and the commits land where the host reads
   them. A read-only bind is no middle way: `git status` works and every commit fails. Two things
   A's text does not say yet:

   - Inside the jail its own checkout shows as `prunable`. So a `git worktree prune`, or a `git gc`
     once the checkout's index is three months old, deletes `.git/worktrees/<name>`, the directory
     git keeps for the checkout, for the host too. A jail in the main checkout can already do that
     to every outside worktree.
   - A narrower A, with a read-only `config` file bound over the read-write `.git`, holds only
     until the host's next `git config`.

   [§4.5](#45-option-4-git-in-herdrs-worktrees) measured two guards against the first. Whether A
   takes one is part of the ruling.

   **Answer:**
   > _(empty — fill in when decided)_

4. 💬 <a id="OQ-HR4"></a>**[OQ-HR4](#OQ-HR4): May a jailed agent drive herdr?**

   **Setup.** herdr ships an agent skill that teaches an agent to split panes, start sibling agents
   and read their output. Matt's jailed Claude is asked to "start three reviewers in new panes".
   Today it cannot: the jail has no herdr binary, no socket and no `HERDR_*` variables, and herdr's
   skill stops at its first line. It is a question because herdr's socket has one lock, its file
   mode. Whoever connects can split a host pane and type into it, so any door is a filter that
   yolo would have to write and keep current
   ([§3.8](#38-the-socket-is-full-control-of-the-host)).

   - **A. No.** No herdr handle crosses into a jail. *You see:* the skill refuses inside a jail.
     You drive herdr yourself, or run the orchestrating agent at `yolo host`.
   - **B. A notify-and-label door** ([§4.6](#46-option-5-a-narrow-herdr-door-for-jailed-agents)).
     *You see:* a jailed agent can raise a herdr toast and label its own pane. It cannot open or
     type into panes. herdr's unmerged report-only listener
     ([herdr#2434](https://github.com/herdrdev/herdr/pull/2434)) is prior art: if upstream ships
     one, the label half would need no yolo filter.
   - **C. An orchestration door** that can open new panes only to run `yolo -- <agent>` in the
     same workspace. *You see:* the skill's "start reviewers" flow works, and every reviewer is
     jailed. The cost is that yolo re-implements a filtered slice of an API that has already
     removed public methods with no replacement ([§2.5](#25-the-socket-and-its-one-lock)), and
     that filter is the whole security boundary.

   <!-- vantage: oq id=OQ-HR4 leaning="A, no herdr handle crosses into a jail for now: B overlaps yolo notify and herdr's own status notifications, and C is a real feature whose boundary is a filter over an API that is still changing; revisit if in-jail orchestration is wanted." -->

   _Leaning:_ **A, for now.** B mostly duplicates `yolo notify` and herdr's own status
   notifications, which already tell the human when an agent is done or blocked. C is a real
   feature, but its whole boundary would be a filter over an API that is still changing. It is worth
   revisiting if you want jailed agents to orchestrate other jailed agents.

   **Answer:**
   > _(empty — fill in when decided)_

---

## 6. What is unmeasured

1. **A real `yolo -- claude` in a host herdr pane.** One run on herdr 0.7.5 showed the status.
   A label probe on 0.9.3 showed on a split pane's border, which is the only place herdr draws it
   ([first real run](#built-2026-09-30)). Still owed on a real host:
   - whether the hint on the runtime client alone makes herdr pick Claude's manifest (that run
     also self-reported, so it cannot tell);
   - how long the idle window lasts during boot;
   - what the screen rules make of Claude under `--dangerously-skip-permissions`;
   - the label from a real jail launch, rather than a probe, and that it clears at exit.
2. **Rootless podman.** Whether herdr can read `HERDR_AGENT` from a rootless podman process that
   has re-entered its user namespace, and whether host `/proc` shows a jailed agent's process
   group at all. A nested jail runs rootful and cannot show either. Only a real rootless host can.
3. **The launcher under herdr's pane-close sequence.** Which teardown steps ran, whether the
   container stopped, and whether podman's forwarded hangup or the launcher's `podman stop` ended
   it, measured on a real host with a real jail. The forwarding itself was MEASURED on a probe
   container; the rest of [§3.4](#34-closing-a-pane-is-a-kill) is inferred from the order.
4. **macOS.** On macos-user: whether herdr can identify an agent that runs as another account,
   whether it can read a hint on the launcher, and whether macOS's `sudo` moves the agent to a
   terminal of its own. On the podman machine and Apple Container: whether a hint on the runtime
   client works.
5. **herdr's host hook inside jail Claude.** What the failing `SessionStart` hook of
   [§3.6](#36-two-writers-on-one-agent-config-file) looks like to the user.
6. **The kitty misfire.** Whether [§3.1](#31-a-container-jail)'s kitty indicator really retitles
   the kitty tab that holds herdr.
7. **Resume through yolo, end to end.** The measured resume ran `sh` and `sleep`, not `yolo`. A
   restore that runs `yolo -- claude --resume <id>` in several panes of one jail, 100 ms apart, has
   not been tried.
8. **Option 4's mount, on a rootless host.** Both halves were MEASURED on 2026-10-01 with the
   rootful podman 5.8.7 nested in a jail (`podman info` reports `rootless: false`). The mount
   half: a scratch repository's `.git` was bound read-write at `/home/u/code/repo/.git` into a
   `--read-only` container of the jail image, beside its linked worktree bound at `/workspace`,
   whose `.git` file named `/home/u/code/repo/.git/worktrees/slug`. Podman created `/home/u` and
   the path below it in the read-only root, and the bound directory listed in full. The commit
   half, and what a read-only bind does instead, is
   [§4.5](#45-option-4-git-in-herdrs-worktrees)'s table and the list below it. A rootless host is
   still open. Its uid mapping decides who owns what git writes into that directory, and whether
   git's ownership check then accepts the directory at all. INFERRED.
9. **herdr's own docs lag its code.** The resume page says 0.10.0. At the v0.9.3 tag,
   `docs/versions/manifest.json` still says the current version is 0.9.1. So the live `herdr.dev`
   pages may lag the `docs/next` tree read here.
10. **Third-party claims.** Claims in READMEs and news posts, such as marketplace size and "native
    support" lists, were not checked beyond what herdr's source shows.
