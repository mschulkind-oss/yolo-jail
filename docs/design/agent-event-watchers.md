---
title: "Any background process can ring the agent, and yolo carries the ring to whichever agent is listening"
date: 2026-09-28
status: in-review
stage: DESIGN
next: "Rule OQ-EW11, whether the doorbell is on where no sidecar starts: the build is held until it, OQ-EW12 and OQ-EW13 are ruled"
tags: [design, sidecars, notify, hooks, claude, pi, codex, opencode, copilot, agy, omp, prompt-injection, credentials, enablement, dotfiles]
summary: "yolo runs user-declared background processes (sidecars) for the life of a launch and gives them one agent-agnostic doorbell: a command, `yolo notify`, writes a ping into a ping box; a per-agent deliverer, shipped by that agent's own pack, carries each ping into the session: an asyncRewake hook for Claude, an extension for pi and omp, a plugin for opencode, an extension for copilot, a next-turn hook for codex and agy. The CI watcher is the worked example. Declaring a sidecar never starts it: each machine turns one on with an explicit host command, recorded in machine-local state that a synced dotfile never carries, and a repository's own sidecar is turned on by approving the config-change diff, whose own section shows it; for a sidecar that runs inside a container jail, the jail's own agent may turn it on, bound to that machine and checkout. A ping wakes one agent session per ping box, the master: the first one launched, unless an agent claims it with the in-jail `yolo notify master --claim`. A keeper owns the ping box and the host-side sidecars at every notch, replacing the lock that passed a sidecar from launch to launch. Every declared sidecar waits for an explicit turn-on (OQ-EW10, ruled). Three questions remain: whether the doorbell (the box, the deliverers and the session list) is on in a launch where no sidecar is; which notch a sidecar's pings reach when one workspace runs at two on one machine; and whether a sidecar turned on while a keeper runs starts at the next launch or only the next fresh one."
vantage:
  status-chip: true
---

# Any background process can ring the agent, and yolo carries the ring to whichever agent is listening

**Status:** 2026-09-28. Nothing built (checked 2026-09-30: no `notify` or `sidecar` verb and no ping box in `internal/` or `cmd/`). Evidence verified at `d4ac39db`, against Claude
Code 2.1.284, pi 0.87.1, codex 0.158.0, copilot 1.0.48, opencode 1.18.32 and agy 1.2.9 as
installed in this jail ([Appendix A](#appendix-a--the-evidence-per-agent)). No agent session was
started for this doc, so every delivery path is UNMEASURED end to end. **Revised 2026-09-29:**
declaring a sidecar no longer starts it; enabling one is a per-machine act
([§12](#12-enabling-a-sidecar-redesign-2026-09-29)), and the sections before it are updated to
match. **Revised again 2026-09-29:** a ping wakes one master session per box
([§3.6](#36-which-session-a-ping-wakes-the-master), [EW-DIR3](#EW-DIR3)); under
[OQ-JL5](jail-lifetime-last-session-wins.md#OQ-JL5)'s ruling a keeper owns the box and the
host-side sidecars at every notch ([EW-D24](#EW-D24), [EW-D25](#EW-D25), replacing
[EW-D19](#EW-D19)); and what is still on by default is revisited
([§12.8](#128-what-is-on-by-default-revisited-2026-09-29)). Its citations were verified against
`30b65282`. **Reviewed the same day:** the master is the earliest session that can take a ping
([EW-D28](#EW-D28)), a claim is told to the session it displaces ([EW-D32](#EW-D32)), a host-side
sidecar's log and state leave the workspace ([EW-D31](#EW-D31)), and [OQ-EW12](#OQ-EW12) and
[OQ-EW13](#OQ-EW13) are asked; those citations were verified against `303e0367`. **Carried out
2026-09-30:** [OQ-EW7](#OQ-EW7)'s B, under which a repository's sidecars are acknowledged and
turned on in the config-change prompt ([§12.4](#124-a-sidecar-a-repository-declares),
[EW-D33](#EW-D33), [EW-D34](#EW-D34)), and [OQ-EW10](#OQ-EW10)'s in-jail turn-on of an agent-side
sidecar ([§12.9](#129-turning-one-on-from-inside-the-jail),
[EW-D35](#EW-D35) to [EW-D39](#EW-D39)); their citations were verified against `95f90be2`.

> **In short.** The watcher is not the product; the doorbell is. yolo should run any process a
> user declares beside the agent, give it one command, `yolo notify "<text>"`, that works the
> same whichever agent is in front, and leave it to each agent's own pack to turn a ping into
> whatever that agent can hear.

**Why it matters.** The CI watcher works only in Claude, only when the agent remembers to start
it as its own background task, and only because it writes Claude's private inbox frames itself.
pi, codex and the rest get nothing, and every new watcher would have to learn every agent.

**The shape.** A **sidecar** runs for the launch. It calls **`yolo notify`**, which drops a
**ping** in the launch's **ping box**. Each agent's **deliverer** takes pings from the box into
its session ([§1.2](#12-terms)).

**Turning one on.** Declaring a sidecar, in config, a pack or a repository, never starts it. It
runs on a machine only after `yolo sidecar enable <name>` on that machine, which writes a record
into yolo's machine-local state, where no synced dotfile reaches. For an agent-side sidecar in a
container jail, the jail's own agent may run the same command, which records a turn-on bound to
this machine and checkout ([§12.9](#129-turning-one-on-from-inside-the-jail)). A
repository's own sidecar is turned on instead by approving the config-change diff, which shows it
in a section of its own and binds it to that declaration, so a changed one is asked about again
([§12.4](#124-a-sidecar-a-repository-declares)).

**Cost.** One new contribution kind and one config key, two new `yolo` verbs (`notify` and
`sidecar`), and a deliverer per agent pack. `yolo host -- <agent>` has to stay resident whenever
a sidecar is enabled, and a keeper holds the sidecars and the box at every notch
([`jail-lifetime-last-session-wins.md` §9.9](jail-lifetime-last-session-wins.md#99-the-keeper-at-yolo-host-and-macos-user)).

**Start at [§3](#3-the-shape)**, the shape. The per-agent answer is [§2.2](#22-what-each-agent-can-hear).

**Needs your ruling:**

- [OQ-EW11](#OQ-EW11): whether the doorbell is on where no sidecar is
  ([§12.8](#128-what-is-on-by-default-revisited-2026-09-29));
- [OQ-EW12](#OQ-EW12): when one workspace runs at two notches on one machine, which of them a
  sidecar's pings reach, and whether a claim can take them across;
- [OQ-EW13](#OQ-EW13): whether a sidecar turned on while a keeper runs starts at the next launch,
  or only at the next fresh one.

[OQ-EW10](#OQ-EW10) was ruled 2026-09-29 (A, and the jail's own agent may turn on an agent-side
sidecar) and is carried out in [§12.9](#129-turning-one-on-from-inside-the-jail);
[OQ-EW9](#OQ-EW9) was directed 2026-09-29 ([EW-DIR3](#EW-DIR3)) and is designed in
[§3.6](#36-which-session-a-ping-wakes-the-master); [OQ-EW5](#OQ-EW5) to [OQ-EW8](#OQ-EW8) were
ruled 2026-09-29, and [OQ-EW7](#OQ-EW7)'s B is carried out in
[§12.4](#124-a-sidecar-a-repository-declares). The feature stays held until the open ones are ruled
([EW-DIR1](#EW-DIR1)). [OQ-EW1](#OQ-EW1), [OQ-EW3](#OQ-EW3) and [OQ-EW4](#OQ-EW4) were ruled
2026-09-29, and [OQ-EW2](#OQ-EW2) is superseded by [OQ-EW8](#OQ-EW8) and [OQ-EW9](#OQ-EW9).

**Reads with:** [`host-notch-services.md`](host-notch-services.md#44-lifetime) (the resident
host launch a host-side sidecar rides on), [`providers.md`'s credential gate](../reference/providers.md#the-credential-gate)
(the "deliver as specifically as possible" rule the secrets section follows),
[`config-safety.md`](../reference/config-safety.md) (the host-side approval record the
enablement record sits beside), and [§15](#15-the-neighbors) for the rest. No implementation
sketch is open yet.

---

## 1. The verdict, and the words it uses

**Build the doorbell into core, the deliverers into the agent packs, and keep the CI watcher in
the maintainer's own pack.** yolo guarantees two things and nothing else: the sidecar lives as
long as the launch, and a ping reaches the foreground agent by the best route that agent offers.
What to watch, when to ring and what to say are the sidecar's business.

Four principles carry the design, numbered so later sections and questions can cite them:

- <a id="EW-P1"></a>**EW-P1. The sidecar decides; yolo delivers.** Core never parses a ping,
  never schedules one and never knows what a CI run is. The whole contract between a sidecar
  and yolo is one command.
- <a id="EW-P2"></a>**EW-P2. Core does not know what an agent is.** Every per-agent delivery
  route ships in that agent's pack, in the pack kinds that already exist, as
  [`AGENTS.md`](../../AGENTS.md) requires of everything else. Core's part is the ping box and
  the reader commands every deliverer shares.
- <a id="EW-P3"></a>**EW-P3. A ping is a doorbell's ring, not an instruction.** Pinged text enters the
  model's context, so it is a prompt-injection channel. yolo bounds it, frames it as an
  automated notice that carries no user authority, and routes it through the agent's
  non-user channel wherever one exists ([§5](#5-security)).
- <a id="EW-P4"></a>**EW-P4. A secret the agent must not hold never runs beside the agent.** A
  process in the jail runs as the agent's user, so the agent can read its environment and its
  files. A sidecar holding such a secret runs at the host, and only its pings cross.

### 1.1 Why now

The maintainer, 2026-09-28: *"I want anybody to be able to run any generic background process
that can decide however it wants to ping the foreground agent session through whatever means we
can make happen. That's the goal. And I want you to use the CI watcher as the example here."*
Earlier the same day he asked for it to work in pi as well as Claude, *"so that we could just
configure in our yolo config some sort of watcher like this that just starts up, and the agent
doesn't even have to participate in this management."*

### 1.2 Terms

Every term here is coined in this doc unless it says otherwise.

| Term | Meaning | What it is not |
| :--- | :--- | :--- |
| **sidecar** | A background process declared in yolo config or by a pack, which yolo starts with a launch once it is enabled on that machine ([§12](#12-enabling-a-sidecar-redesign-2026-09-29)), and stops when the launch ends | Not a `kind: "service"` ([§3.1](#31-declaring-a-sidecar)), which serves the jail's own loopback and holds no grant; and not agy's own `sidecars` directory ([Appendix A](#a6-agy-129)) |
| **side** | Where a sidecar runs. `agent`: beside the agent, inside its confinement. `host`: on the host, outside it. At the host notch the two are the same place | Not the notch. The notch is where the agent runs; the side is where the sidecar runs relative to it |
| **ping** | One short text notice a process hands to yolo for the foreground agent, with a sender label, a time and an id | Not a user turn, and not a command |
| **doorbell** | This doc's word for the whole path a ping takes: the one command that rings it, `yolo notify`, the ping box, and the deliverers that carry a ping into a session. A ping is one ring of it | Not `yolo notify` alone, which is only its command, and not any vendor's notification feature |
| **ping box** | The store that holds pings until the master's deliverer takes them, and the box's session list. One per keeper: one per container jail, and at macos-user and at `yolo host` one per workspace on the machine at that notch ([EW-D24](#EW-D24)). Its format is private to core | Not the Claude inbox socket, and not a queue any agent reads directly |
| **keeper** | Not coined here: [`jail-lifetime-last-session-wins.md`](jail-lifetime-last-session-wins.md#11-terms) coins it. The one host process that holds a jail's host services, or at macos-user and `yolo host` a workspace's, from its first session to its last | Not a supervisor, and not machine-wide |
| **agent session** | One running agent process whose deliverer has called one of `yolo notify`'s reader forms: one claude, one pi ([§3.6](#36-which-session-a-ping-wakes-the-master)) | Not a yolo session: a bare `yolo` shell is one yolo session in which several agent sessions can come and go, and a shell with no deliverer is none |
| **master** | The maintainer's word ([EW-DIR3](#EW-DIR3)): the one agent session per box that a ping reaches. By default the first one launched; an agent can claim it ([§3.6](#36-which-session-a-ping-wakes-the-master)) | Not a session that owns anything: it is only where pings go |
| **session list** | The box's record of its agent sessions, in the order they first registered, with any claim in force ([§3.6](#36-which-session-a-ping-wakes-the-master)) | Not the keeper's session count, which is one shared lock and knows no session by name |
| **deliverer** | The per-agent piece that moves pings from the box into one agent's session: a hook, an extension, a plugin or a sidecar of its own | Not a wire-bridge `adapter`, which is a protocol conversion at an address ([`kinds.go`](../../internal/packdecl/kinds.go)) |
| **tier** | How well a deliverer can reach its agent: **wake** (it starts a turn while the agent is idle), **next turn** (the ping is attached to the next turn the user starts), or **held** (the ping waits in the box and the launch says so) | Not a quality score. A held ping is not lost |
| **replicator** | Whatever copies config between machines: a git-synced dotfiles repository, GNU Stow, Syncthing, chezmoi. It writes `~/.config` exactly as the user does, and it cannot know which machine the user meant | Not the agent. It is the actor the enablement gate stands against ([§12.2](#122-where-the-record-lives-and-why-there)) |
| **enablement record** | A host-side file in yolo's machine-local state saying that sidecar N, from source S, may run (or, from `disable`, may not) for workspace W, or for every workspace, on this machine. Written only by `yolo sidecar enable` and `disable` at the host, as [OQ-EW5](#OQ-EW5) ruled ([§12.3](#123-the-command)) | Not config, and not synced by yolo: it sits beside the config-change gate's approval record ([EW-D14](#EW-D14)). Not a jail turn-on, which the jail writes and the host only reads |
| **gated set** | The maintainer's phrase, from the [OQ-EW3](#OQ-EW3) and [OQ-EW4](#OQ-EW4) rulings: the declared sidecars that wait for an explicit act before they run. Since [OQ-EW10](#OQ-EW10)'s ruling it is every declared sidecar ([EW-D13](#EW-D13)) | Not a list anyone writes. The config declares the set; each machine's records decide which members run there |
| **acknowledgement** | The record that binds a sidecar someone other than the user wrote to the declaration that was shown, holding a hash of it, so a changed declaration is not started ([§12.4](#124-a-sidecar-a-repository-declares)). For a sidecar the workspace's own config declares it is the approval record's sidecar part, written by the config-change prompt's `y` ([OQ-EW7](#OQ-EW7), B; [EW-D33](#EW-D33)). For a fetched pack's it is the sidecar's enablement record, which also holds the pack's locked commit ([EW-D23](#EW-D23)) | Not a separate command for a repository's sidecar, and not the config-change prompt for a fetched pack's, which no diff shows |
| **sidecar section** | The labeled block of the config-change prompt that lists in full, command lines included, each sidecar the workspace's own config declares that a `y` would newly acknowledge. What it showed is recorded as the approval record's third part, the **sidecar part**, `<container-name>.sidecars.json` beside the snapshot and the scope part ([§12.4](#124-a-sidecar-a-repository-declares), [EW-D33](#EW-D33)) | Not the config diff, which shows the same keys as JSON lines, and not shown for a sidecar from user config or a pack |
| **jail turn-on** | An agent-side sidecar turned on from inside a container jail by the jail's own agent, with `yolo sidecar enable` run there for the jail's own workspace ([OQ-EW10](#OQ-EW10)). It is an entry in a file in the jail's own per-workspace home, bound to this machine and workspace by the turn-on tag, which the host reads at the next launch that creates the container ([§12.9](#129-turning-one-on-from-inside-the-jail)) | Not an enablement record: the host never copies it into its store, and every host record outranks it ([EW-D37](#EW-D37)) |
| **turn-on tag** | A keyed hash of this machine's id ([EW-D15](#EW-D15)) together with the workspace's container name, which each fresh launch hands the jail in the sidecar view and each jail turn-on carries, so the entry means this machine and this checkout ([EW-D35](#EW-D35)) | Not a secret from the agent, and not the machine id itself |
| **sidecar view** | A file each fresh launch writes beside `config-assembled.json` for the jail to read: each declared sidecar's source, side and state as that launch resolved them, the hashes a jail turn-on binds to, and the turn-on tag ([§12.9](#129-turning-one-on-from-inside-the-jail)) | Not load-bearing: a jail that edits it only makes entries the host will not honor |
| **authorship filter** | A watcher reporting only CI runs whose commit this checkout pushed, read from git's local record of its own pushes ([§12.6](#126-two-machines-one-project)) | Not a lock. It narrows what one watcher reports and promises nothing about another |

## 2. What exists today

### 2.1 The CI watcher, and what it depends on

The maintainer's personal `ci_watch.py` (in the git-ignored `swarf/tools/`, about 170 lines of
Python standard library) polls `GET /repos/<repo>/actions/runs` with an ETag, so an unchanged
poll is a free 304. It reports each run that newly reached `completed`, and in `--post` mode it
writes two JSON frames to `CLAUDE_CODE_MESSAGING_SOCKET`: an `auth` frame carrying
`CLAUDE_CODE_MESSAGING_TOKEN`, then a `user` frame. Its companion how-to (in the git-ignored
`scratch/`) states the rule that makes it work: *"Start the watcher with the Bash tool's
`run_in_background: true`. Never use `nohup`, `setsid`, `disown` or a trailing `&`."* That gives
it four dependencies, each of which this design removes:

1. **The agent has to start it,** and start it again in every new session.
2. **Only Claude can hear it.** The socket and its frames are Claude's.
3. **It must be the session's descendant, holding the session's variables.** Claude exports
   those two variables only into its Bash tool's commands. A yolo-supervised process is a PID
   descendant of claude in a fresh launch, since the entrypoint starts `yolo-jaild supervise`
   and then execs into bash and claude (MEASURED, `ps` in this jail: PID 2 is `claude`, PID 53
   `yolo-jaild supervise` has parent 2; [`runtime.go`](../../internal/entrypoint/runtime.go)
   `startJailDaemonSupervisor`, [`boot.go`](../../internal/entrypoint/boot.go) `execBash`).
   But the supervisor started before claude made its socket, so none of the `CLAUDE_CODE_*`
   variables is in its environment (MEASURED, `/proc/53/environ`). On an attach the supervisor
   is reused and is no relative of the new agent at all.
4. **Its token is the agent's.** It reads `GH_TOKEN` from the workspace `.env`, which the agent
   can read too.

What it gets right, and what this design keeps: ETag polling, a baseline so the first poll
reports nothing old, and **facts-only messages** (short SHA, workflow, event, conclusion, URL),
never a commit message, branch name or log line.

### 2.2 What each agent can hear

The question for each agent is whether a process that is not the agent can put text in front of
the model, and whether that works while the agent is idle. The evidence behind every row is in
[Appendix A](#appendix-a--the-evidence-per-agent).

| Agent | Best route | What it takes | Tier this design uses | Evidence |
| :--- | :--- | :--- | :--- | :--- |
| **claude** | A hook with `asyncRewake: true`: *"runs in background and wakes the model on exit code 2"*, with the hook's output shown to the model as a system reminder | One `hooks` entry in `~/.claude/settings.json`, which the claude pack already renders | **wake** | MEASURED in the binary's settings schema; SOURCED ([hooks docs](https://code.claude.com/docs/en/hooks)) |
| claude, alternative | The inbox socket `/tmp/cc-socks/<pid>.sock` | Same UID; the token is optional on Linux. A sender that does not attest its permission mode is **held for the user's approval when the session bypasses prompts**, which every yolo claude does, unless `crossSessionInbound` is `"accept"` | not used ([§7](#7-alternatives-considered)) | MEASURED in the binary |
| claude, alternative | MCP channels (`claude/channel`) | `channelsEnabled`: *"Teams/Enterprise: default off"* | not used | MEASURED in the binary |
| **pi** | An extension calling `pi.sendMessage(…, {triggerTurn: true, deliverAs: "followUp"})` | One file under `~/.pi/agent/extensions/`, which the pi pack already ships two of. pi ships a `file-trigger.ts` example doing exactly this | **wake** | MEASURED in pi's types, session code and examples |
| **omp** | The same extension, if omp's API is pi's, as the omp pack's own `yolo-footer.js` comment claims | One file under `~/.oh-omp/agent/extensions/` | **wake**, UNMEASURED | INFERRED; omp is not installed here |
| **opencode** | A plugin calling `client.session.prompt` on the active session, or the HTTP API (`/session/{id}/prompt_async`, `/tui/submit-prompt`) when the TUI runs with `--port` | One plugin file under `~/.config/opencode/plugins/` | **wake**, UNMEASURED | SOURCED ([plugins](https://opencode.ai/docs/plugins), [server](https://opencode.ai/docs/server)); the routes are MEASURED in the binary |
| **copilot** | An extension that calls `joinSession()` for *"the user's current foreground session"* and then `session.send()` | One extension directory in copilot's user extensions dir. Extensions load by default | **wake**, UNMEASURED | MEASURED in the package's bundled SDK docs |
| **codex** | A `UserPromptSubmit` hook returning `additionalContext`. For waking: `codex queue --thread <id> --message <text>` through the shared app-server daemon | The hook is one config entry. `queue` needs the TUI to be on the shared daemon, not its embedded fallback, and whether a queued message starts a turn on an idle thread is open upstream ([openai/codex#49081](https://github.com/openai/codex/issues/49081)) | **next turn**; the wake route is closed while [OQ-CDX1](../research/codex-background-service.md#OQ-CDX1) keeps the daemon off wherever yolo launches Codex | hooks SOURCED ([hooks](https://learn.chatgpt.com/docs/hooks)); `queue` MEASURED in `--help` |
| **agy** | A `PreInvocation` hook returning `injectSteps` with a `userMessage`. For waking: `agy agentapi send-message`, which appears to need the running server's address and CSRF token | The hook is one `hooks.json` entry | **next turn** first | MEASURED in agy's embedded docs; the wake route is INFERRED |
| a plain shell | Nothing | — | **held** | — |

**The shape of the answer:** four of the seven agents (claude, pi, opencode, copilot) have a
route that wakes an idle session from a file or a process the agent's own pack can ship, and
omp probably shares pi's. codex and agy have one that reaches the next turn. agy has one that
might wake, which needs measuring; codex's is closed while its background server is off
([OQ-CDX1](../research/codex-background-service.md#OQ-CDX1)). None of the wake routes needs the agent to take part, and none
needs yolo to learn a vendor's socket protocol.

## 3. The shape

```mermaid
flowchart LR
  subgraph launch["one launch"]
    S1["sidecar<br/>side: agent"] -- "yolo notify" --> BOX[("ping box")]
    S2["sidecar<br/>side: host"] -- "yolo notify" --> BOX
    AG["the agent's own shell<br/>or background task"] -- "yolo notify" --> BOX
    BOX -- "yolo notify --wait / --follow / --drain" --> D1["claude deliverer<br/>asyncRewake hook"]
    BOX --> D2["pi deliverer<br/>extension"]
    BOX --> D3["codex deliverer<br/>UserPromptSubmit hook"]
    D1 --> C["claude session"]
    D2 --> P["pi session"]
    D3 --> X["codex session"]
  end
```

Core owns five things: the `sidecar` declaration, its enablement on each machine
([§12](#12-enabling-a-sidecar-redesign-2026-09-29)), its lifecycle, `yolo notify`, and the ping
box. The agent packs own the deliverers. Only enabled sidecars appear in the picture above; a
declared one that is not enabled on this machine starts nothing.

### 3.1 Declaring a sidecar

A sidecar is declared as a pack contribution of a new kind, `sidecar`, or inline in the config
key `sidecars`. It is its own kind rather than a `kind: "service"`, for two reasons. A service is
*"THE ANTI-LOOPHOLE … a service holds NO grant and crosses NO boundary"*
([`kinds.go`](../../internal/packdecl/kinds.go)), while a host-side sidecar runs user code on the
host with host credentials. And a service carries an endpoint file, a reachability witness and a
caller token, none of which a sidecar has. It reuses the service's machinery, not its kind:
agent-side sidecars run under the existing `yolo-jaild supervise`.

As a pack contribution:

```jsonc
{
  "kind": "sidecar",
  "name": "ci-watch",                       // required; unique among the launch's sidecars
  "cmd": ["python3", "{pack_dir}/sidecars/ci_watch.py"],
  "side": "agent",                          // "agent" (default) | "host"
  "restart": "on-failure",                  // "always" | "on-failure" (default) | "no"
  "host_env": ["GH_TOKEN"]                  // side "host" only: host variables this process gets
}
```

In config, where a user turns a pack's sidecar off or declares one outright:

```jsonc
"sidecars": {
  "ci-watch": { "enabled": false },                        // off in this workspace
  "flaky-log": { "cmd": ["sh", "-c", "tail -F log | …"] }  // declared inline, side "agent"
}
```

**A declaration starts nothing by itself.** Either form above distributes wherever the config or
pack goes, and a sidecar runs on a machine only once it is turned on there: by `yolo sidecar
enable` at the host, by the jail's own agent for an agent-side one
([§12.9](#129-turning-one-on-from-inside-the-jail)), or, for one the workspace's own config
declares, by approving the config-change diff that shows it
([§12.4](#124-a-sidecar-a-repository-declares)). `enabled` can only veto: `false` at any scope
keeps a sidecar off, and `true` at any scope starts nothing ([EW-D13](#EW-D13)).

The rules:

- **`{pack_dir}`** in `cmd` is the only substituted token, and it resolves to the pack's staged
  tree wherever the sidecar runs. A pack runs a script through its interpreter, as above,
  because an embedded pack's files carry no execute bit
  ([`packs/hello-daemon/README.md`](../../packs/hello-daemon/README.md)).
- **A name collision** between two packs resolves as `service` does: the later pack in
  selection order wins. User config beats every pack, and only by writing a whole declaration of
  its own. A workspace config never takes a name that user config, the local pack or a selected
  pack declares; it may only veto it (the table below). The launch names the loser. An
  enablement record names the source it was made for, a pack by the source address its entry
  is written with rather than by its name, so neither the winner of a collision nor a renamed
  pack entry inherits another source's record ([§12.3](#123-the-command)).
- **Zero sidecars**, or none enabled, means nothing starts. The ping box still exists, because
  the agent's own background tasks can ring it too ([§3.3](#33-yolo-notify-and-the-ping-box)).
- **Who may declare which side** is [OQ-EW1](#OQ-EW1) (ruled: host side from the user's own word
  only) and [OQ-EW3](#OQ-EW3) (ruled: a repository may declare an agent-side one, which runs only
  once acknowledged). **Whether a declared sidecar runs** is its enablement on the machine
  ([§12](#12-enabling-a-sidecar-redesign-2026-09-29)), which carries out [OQ-EW4](#OQ-EW4).
- **A host-side `cmd` obeys the loophole placement rule.** It is refused, by name, when
  `{pack_dir}` or its program resolves inside the workspace this launch mounts or inside the jail
  home yolo manages ([EW-D20](#EW-D20)).

**Which scope may set which key.** Config is merged field by field, recursively, and the merge
keeps no record of which file a field came from (`MergeConfig` in
[`load.go`](../../internal/config/load.go)). Without a rule per key, a workspace entry
`"ci-watch": {"cmd": [...]}` would merge into a user-declared host-side `ci-watch`, keep its
`side: "host"` and `host_env`, and run the repository's command on the host looking
user-declared. So each key's scope is checked against each config file as written, before the
merge, as the loophole scope pass does ([`validate_loopholes.go`](../../internal/config/validate_loopholes.go)
re-reads the workspace files for the same reason) ([EW-D22](#EW-D22)):

| Key | User config | Workspace config (`yolo-jail.jsonc`, `yolo-jail.local.jsonc`) |
| :--- | :--- | :--- |
| `cmd`, which makes the entry a whole declaration | Allowed. Under a pack's name it replaces that pack's declaration whole, and the declaration's source becomes user config | Allowed, agent side only, and only under a name that user config, the local pack and every selected pack leave undeclared |
| `side: "host"`, `host_env` | Only in an entry that has its own `cmd` ([OQ-EW1](#OQ-EW1)) | Refused ([OQ-EW1](#OQ-EW1)) |
| `restart` | Only in an entry that has its own `cmd` | Only in an entry that has its own `cmd` |
| `enabled` | Allowed: `false` vetoes, `true` starts nothing | Allowed: `false` vetoes, `true` starts nothing |

An entry without `cmd` may carry only `enabled`. Changing any other field of a sidecar someone
else declared means writing a whole declaration, which then belongs to the scope that wrote it.
A violation is refused by key name: an error on the host and a warning in-jail, as loopholes'
scope violations are, and either way the refused entry never reaches a spawn. A veto is read
per file too, so a workspace `"enabled": true` cannot undo a user-config `false`.

### 3.2 Lifecycle, per side and per notch

Everything in this table applies only to a sidecar that is enabled on this machine and not
vetoed ([§12.1](#121-declaring-is-not-enabling)). An **attach** is a second `yolo` launch that
joins a container already running for the workspace.

| | side `agent` | side `host` |
| :--- | :--- | :--- |
| **Container jail** (podman, Apple Container) | A daemon under `yolo-jaild supervise`, started before the agent, stopped when the container stops. One per container, so an attach adds none. Not started when another keeper on this machine already runs it for this workspace ([EW-D25](#EW-D25)) | A child of the jail's keeper, started after the pre-flights and before the container, and stopped when the jail ends. An attach starts none of its own and says so. The same one-instance rule ([EW-D25](#EW-D25)) |
| **`yolo host -- <agent>`** | Same as `host`. A sidecar a workspace declares does not run here ([EW-D18](#EW-D18)) | A child of the workspace's `yolo host` keeper ([`jail-lifetime-last-session-wins.md` §9.9](jail-lifetime-last-session-wins.md#99-the-keeper-at-yolo-host-and-macos-user)), spawned by the first `yolo host` launch in the workspace that has a sidecar to start and ended with the last `yolo host` session there. Sidecars start before the agent. A second `yolo host` in the same workspace joins that keeper: it starts none and shares the box, and nothing passes from launch to launch ([EW-D25](#EW-D25)). A host launch that has a keeper stays resident as its agent's parent instead of exec'ing (`hostExec` in [`host.go`](../../internal/cli/host.go) execs today; [JL-D36](jail-lifetime-last-session-wins.md#JL-D36)) |
| **`yolo host env`, `yolo host apply`, an agent started without yolo** | Runs no sidecar. The same rule [OQ-HS3](host-notch-services.md#OQ-HS3) ruled for host services: an agent launched outside yolo lacks the feature, and that is accepted | Same |
| **macos-user** | **Not started**, and the launch line says it does not run on macos-user yet ([§12.5](#125-what-the-launch-says)); the launch is not refused. The guest's supervisor could run it: [OQ-DP8](declaration-parity.md#OQ-DP8) is ruled and built for loophole jail daemons. What holds it back is [EW-D25](#EW-D25)'s one-instance rule, which nothing there can keep. That supervisor is each session's, runs as the sandbox account and is started by the session's own `sudo`, so two sandboxes in one workspace would each run the sidecar and both would ping, the duplicate [EW-DIR1](#EW-DIR1) and [EW-D25](#EW-D25) exist to prevent; a keeper cannot start what runs as the sandbox account, and only keepers take EW-D25's lock. That is a named limit, not a carve-out ([`jail-lifetime-last-session-wins.md` §9.9.10](jail-lifetime-last-session-wins.md#9910-every-feasibility-problem-found), row 9). The same sidecar declared host side runs there | A child of the workspace's macos-user keeper, as at the host, with the same one-instance rule ([EW-D25](#EW-D25)). Two sandboxes in one workspace are two sessions of that keeper |

The container row changes with
[`jail-lifetime-last-session-wins.md`](jail-lifetime-last-session-wins.md), whose
[OQ-JL1](jail-lifetime-last-session-wins.md#OQ-JL1) the maintainer directed on 2026-09-29. A
shared jail's host services move out of the first launcher into a **keeper**: one small
background process per running container jail, spawned by the fresh launch before any host
service or the container exists, that ends itself with the jail
([its §9](jail-lifetime-last-session-wins.md#9-the-keeper-design-2026-09-29)). Host-side sidecars
move with those services and become the keeper's
([its §9.2](jail-lifetime-last-session-wins.md#92-what-it-owns)), so a host-side sidecar at a
container backend is stopped when the jail ends, not when the launch that created the container
does. That is [HD-R1](host-daemon-ownership.md#HD-R1)'s unit: a host daemon *"ends with that
jail"*. Neither is built.

[OQ-JL5](jail-lifetime-last-session-wins.md#OQ-JL5) was ruled A on 2026-09-29: a keeper at every
notch that starts a long-lived host service or sidecar. So `yolo host` and macos-user get one too,
one per workspace at each notch, designed in
[`jail-lifetime-last-session-wins.md` §9.9](jail-lifetime-last-session-wins.md#99-the-keeper-at-yolo-host-and-macos-user).
That retires the trap the question named: [EW-D19](#EW-D19)'s lock, which handed one sidecar from
launch to launch at those notches, is the handoff
[JL-D14](jail-lifetime-last-session-wins.md#JL-D14) rejected for container jails. Within one
notch the keeper now owns each sidecar from the workspace's first session there to its last, and
no lock passes it on. Across notches, where one workspace can have two keepers on one machine, a
lock between keepers keeps it to one instance ([EW-D25](#EW-D25),
[§12.7](#127-one-machine-several-launches-or-sessions)).

Common to both sides:

- **Environment.** A sidecar gets `YOLO_NOTIFY_BOX` (where the box is), `YOLO_NOTIFY_FROM` (its
  own name), `YOLO_SIDECAR_STATE` (a per-workspace directory that survives relaunches, for
  state files such as a watcher's seen list: inside the confinement for an agent-side sidecar,
  and in host state no jail mounts for a host-side one, [EW-D31](#EW-D31)), and `YOLO_WORKSPACE` (the workspace path as that
  side sees it). An agent-side sidecar also has the container's environment. A host-side one
  has only these, `PATH`, `HOME` and the variables its `host_env` names.
- **Restart.** `on-failure` restarts a non-zero exit with the supervisor's backoff, 1 second
  doubling to 30 ([`supervisor.go`](../../internal/supervisor/supervisor.go)). An exit 0 is
  "nothing to do here", and is not restarted. `always` restarts both; `no` restarts neither.
- **Logs.** Agent side: `~/.local/state/yolo-jail-daemons/<name>.log`, as every supervised
  daemon. Host side: in host state no jail mounts, keyed by the workspace's container name,
  beside the keeper's own log ([EW-D31](#EW-D31)). Never under the workspace's `.yolo/`, which the
  same workspace's container jail can write: a link planted there would make the keeper write the
  log, or a host-side sidecar write its state, into any file the host user can write, and state a
  jail can edit would shape the pings a host-side sidecar sends to an unconfined `yolo host`
  agent. Each log is rotated once at 5 MB, as the supervisor does, and its lines reach
  `launch.log` only through the opener that refuses a planted link, as the keeper's own do
  ([JL-D19](jail-lifetime-last-session-wins.md#JL-D19)).
- **Stop.** `SIGTERM`, then `SIGKILL` after 5 seconds on the agent side (the supervisor's grace)
  and 2 seconds on the host side ([`host-notch-services.md` §4.4](host-notch-services.md#44-lifetime)'s). A host-side sidecar whose launch died without cleanup
  exits within 5 seconds of noticing; how it notices is the implementer's choice.
- **Config changes** take effect at the next launch that starts a keeper: one that creates a
  container, like every other key the jail freezes at boot, or the first launch of a workspace at
  `yolo host` or macos-user while no keeper runs for it there. So do `yolo sidecar enable` and
  `disable`, which say so ([§12.3](#123-the-command)), and a turn-on made from inside a container
  jail, which takes effect only when the container is next created ([EW-D38](#EW-D38)). Whether
  a launch that joins a running keeper starts a sidecar enabled since is [OQ-EW13](#OQ-EW13).

### 3.3 `yolo notify` and the ping box

`yolo notify` is a subcommand of the one binary that already runs on both sides, so a sidecar
needs nothing installed. It has one writer form and three reader forms, and the reader forms
are what every deliverer calls. The box format stays private to core.

| Form | Who calls it | Behavior |
| :--- | :--- | :--- |
| `yolo notify [--from NAME] [--] TEXT` (or TEXT on stdin) | a sidecar, a script, the agent itself | Writes one ping. Exits 0 on success, 2 on empty or oversized input it could not truncate, 3 when `YOLO_NOTIFY_BOX` is unset or missing (*"not running under a yolo launch"*), so a sidecar can fall back to printing. `--from` defaults to `YOLO_NOTIFY_FROM`, then to `shell` |
| `yolo notify --wait --reader ID` | a hook that blocks, such as Claude's | Holds its agent session's reader lock, which arms it, and blocks until its session is the master and a ping is untaken; takes those pings, prints them as one framed message and exits 2. Exits 0 at once, printing nothing, if another `--wait` of the same agent session holds that lock, or if there is no box |
| `yolo notify --follow --reader ID` | an extension or plugin that stays running, such as pi's | Holds its agent session's reader lock while it runs, and whenever its session is the master, takes each batch of untaken pings and streams it as one JSON line, until its stdin closes |
| `yolo notify --drain --reader ID [--format F]` | a next-turn hook, such as codex's | If its session is the master, takes and prints every untaken ping; then exits 0 immediately, printing nothing when there is none. `--format` renders the agent's hook output shape, and each shape is named by the pack that needs it |
| `yolo notify --pending` | a human, or the agent | Lists the pings no master has taken yet, and marks nothing |
| `yolo notify master [--claim \| --release] [--reader ID]` | an agent, usually because the user asked it to, or a human | Shows the box's agent sessions, oldest first, and which is master and why; `--claim` makes the agent session it runs under the master, and `--release` gives that claim back ([§3.6](#36-which-session-a-ping-wakes-the-master)) |

Every reader form also takes `--agent-pid PID`, the process of the agent its deliverer serves,
and its first call registers that agent session in the box's session list. A reader form only
takes pings for the master; any other waits ([§3.6](#36-which-session-a-ping-wakes-the-master)).

The box:

- **Where it lives.** Every keeper has one, a host directory the keeper creates when its key
  starts and removes, session list included, when its key ends ([EW-D24](#EW-D24)). At a
  container backend it is bind mounted read-write into the jail at one fixed path, so both sides
  write the same place. At `yolo host` it lives in host state that no jail mounts, never under
  the workspace's `.yolo/`, which the same workspace's container jail can write: a box there
  would let that jail put text in front of an unconfined agent. At macos-user the sandbox's `yolo notify` and
  deliverers write it too, so it lives in a dir of the two-account shape setup already
  provisions (host-user-owned, group `_yolojail`, setgid, inheriting ACEs), outside every
  workspace, so that no container jail of the same workspace, which mounts the workspace, can
  write it. That use is unmeasured and unbuilt ([`jail-lifetime-last-session-wins.md` §9.9.10](jail-lifetime-last-session-wins.md#9910-every-feasibility-problem-found),
  item 4). Within those rules its exact path is the implementer's.
- **A ping** is one file, written to a temporary name and renamed into place, so a reader never
  sees half of one. It carries an id, the time, the `from` label and the text.
- **Order** is by the time of the write, then by id. Pings that arrive within **2 seconds** of
  each other are delivered as one message.
- **Readers.** A reader id is the deliverer's choice, and names an agent and a session
  (`claude:<session id>`, for example). Each ping is taken once, by the master's reader
  ([§3.6](#36-which-session-a-ping-wakes-the-master)). A master that arms takes the pings no one
  has taken, including those rung before any agent started, capped at the newest **20** with one
  line saying how many older ones were left in the box.
- **Retention.** The box keeps the newest **200** pings. A box ends with its keeper.
- **Concurrency.** Writers never coordinate, because each ping is its own file. Which reader
  takes a ping is the master rule, and the take is an atomic create in the box, so no ping is
  taken twice ([EW-D30](#EW-D30)). Two readers of one agent session are serialized by that
  session's reader lock, which is keyed on the agent session (its pid and start time), never on
  the reader id alone: two claudes resuming one conversation in two tabs (`claude --continue`)
  can carry one session id, and a lock on the id would let one's waiter shut out the other's
  ([EW-D27](#EW-D27)).
- **Forbidden.** The host side never reads the box, so nothing the jail writes there can reach
  the host. The host side's writer never follows a link it finds in the box, since the jail can
  plant one. It creates each file relative to the box directory, exclusively and without
  following links, which is the [`workspaceskills.go`](../../internal/jailcontent/workspaceskills.go)
  rule for reading a jail-editable tree applied to writing into one.

### 3.4 Deliverers, per agent

Each deliverer ships in its agent's pack, in kinds that exist today: `config-overlay` for a
settings entry and `files` for a script, exactly as the maintainer's pack already ships Claude
`Stop` hooks and eight pi extensions (`/ctx/packs/matt/pack.json`). Each pack also declares its
tier, so the launch can say what a sidecar's pings will do ([§3.5](#35-failure-paths)).

- **claude, wake.** The claude pack adds one command hook, `yolo notify --wait --reader
  claude:<session> --agent-pid $PPID`, with `asyncRewake: true` and an explicit `timeout` of **86400** seconds,
  registered on **`SessionStart` and on `Stop`**. `SessionStart` arms it when the session
  opens. Each time the agent finishes a turn, `Stop` re-arms it, and the lock makes the second
  waiter exit at once while the first is still waiting. When a ping lands, the waiter exits 2,
  its output reaches the model as a system reminder, and the model takes a turn, after which
  `Stop` arms the next waiter. The session id comes from the hook's stdin JSON, and `$PPID` is
  the claude process that runs the hook's shell, INFERRED, to be measured with the deliverer
  ([§10](#10-what-i-would-build-in-order) step 3). A waiter that is not the master's keeps
  waiting, and wakes nothing ([§3.6](#36-which-session-a-ping-wakes-the-master)).
- **pi, wake.** The pi pack ships `yolo-notify.js` into `~/.pi/agent/extensions/`. On
  `session_start` it spawns `yolo notify --follow --reader pi:<session> --agent-pid <pi's
  process.pid>`. For each line it calls
  `pi.sendMessage({customType: "yolo-notify", content, display: true}, {triggerTurn: true,
  deliverAs: "followUp"})`, which starts a turn when pi is idle and waits for the current turn
  to end when pi is busy. It uses `sendMessage` rather than `sendUserMessage`, so the ping
  arrives as a custom message rather than a user turn. On `session_shutdown`, and on reload, it
  kills the child. The docs require exactly that pairing of start and cleanup.
- **omp, wake, unmeasured.** The omp pack ships the same file at its own extension path. This
  rests on omp's API being pi's, which is the omp pack's claim and is unchecked.
- **opencode, wake.** A plugin file that spawns `yolo notify --follow --reader opencode:<session>
  --agent-pid <pid>`, with opencode's own `process.pid`, since a plugin runs inside opencode, and
  calls `client.session.prompt` on the session it last saw active.
- **copilot, wake.** An extension that spawns `yolo notify --follow --reader copilot:<session>
  --agent-pid <pid>`, with its own `process.ppid`, since copilot runs an extension as its child,
  then joins the foreground session and calls `session.send`. copilot offers no non-user role for this, so the framing line in
  [§5](#5-security) is the whole marker.
- **codex, next turn.** A `UserPromptSubmit` hook running `yolo notify --drain --reader
  codex:<session> --agent-pid $PPID --format codex`, the session id from the hook's input as in
  claude's. A wake route, a codex-pack sidecar piping `--follow` into `codex queue`, is closed for
  now: `queue` reaches a running Codex only through Codex's background server, and
  [OQ-CDX1](../research/codex-background-service.md#OQ-CDX1) turned that server off wherever yolo
  launches Codex.
- **agy, next turn.** A `PreInvocation` hook running `yolo notify --drain --reader agy:<session>
  --agent-pid $PPID --format agy`.
- **Every deliverer passes `--reader` and `--agent-pid`**, so its agent registers and can be
  master ([EW-D27](#EW-D27)). How each finds its session id and pid is its pack's, and INFERRED
  until measured with that deliverer.
- **A plain shell, held.** Nothing is delivered. `yolo notify --pending` shows what waits.

### 3.5 Failure paths

| Step | What fails | What happens, and who finds out |
| :--- | :--- | :--- |
| Declaration | Unknown `side`, empty `cmd`, `host_env` on side `agent`, a side a scope may not declare, a key a scope may not set ([§3.1](#31-declaring-a-sidecar)'s table) | The launch refuses, naming the sidecar and the field (in-jail, a scope violation warns instead, and the entry still never reaches a spawn). `yolo check` reports the same |
| Enablement | Declared but not enabled on this machine; a record made on another machine; a turn-on made from inside a jail on another machine or for another checkout, outranked by a host record, or read at `yolo host` ([§12.9](#129-turning-one-on-from-inside-the-jail)); a fetched pack's declaration or locked commit changed since it was enabled; a workspace-declared sidecar at the host notch; an agent-side sidecar on macos-user | Nothing starts. The launch never refuses over it and, as [OQ-EW5](#OQ-EW5) ruled, never asks about it: one line per sidecar names its state and the command that would start it ([§12.5](#125-what-the-launch-says), [EW-D16](#EW-D16)) |
| Acknowledgement | A sidecar the workspace's own config declares is new, changed, or not yet acknowledged on this machine | The config-change prompt shows it in its sidecar section. A `y` starts it; an `N`, or a launch with no terminal and no `--accept-config-changes`, launches nothing, as a changed config always did ([§12.4](#124-a-sidecar-a-repository-declares), [EW-D33](#EW-D33)) |
| Start | The command cannot be executed | Treated as an exit under `restart`, as the supervisor already treats a failed spawn: retried with its backoff, each failure logged with its error. The launch does not wait for a sidecar and does not refuse over one: a watcher is never worth a jail |
| Running | The sidecar crashes | Restarted per `restart`, with the backoff capped at 30 seconds, for as long as the launch lives. Each exit is logged. Under `no`, the supervisor logs a giving-up line and the sidecar stays down |
| Ringing | `yolo notify` finds no box | Exit 3; the sidecar decides what to do. The CI watcher prints instead ([§4](#4-the-worked-example-the-ci-watcher)) |
| Ringing | The sidecar floods | Past **6 pings per 60 seconds** from one `from` label, later pings stay in the box, undelivered, and the next delivered message says how many were held and names `yolo notify --pending` |
| Delivery | The agent's pack ships no deliverer, or its tier is **held**, or another session is the master | At launch, one disclosure line per enabled sidecar naming whom its pings reach and how. At a launch whose box has no master yet it is this launch's agent, for example *"ci-watch: pings reach claude immediately"*, *"ci-watch: pings reach codex at your next prompt"*, *"ci-watch: pings wait in the box (bash has no deliverer); `yolo notify --pending` lists them"*. At an attach, or a launch that joins a keeper, it names the current master instead: *"ci-watch: pings reach the master session, claude:ab12 (first launched; wake), not this one; `yolo notify master` shows why"* ([EW-D32](#EW-D32)) |
| Delivery | A wake deliverer dies without cleanup, or times out (the Claude waiter killed or past its 86400 seconds, the pi child gone) | Its session is passed over until it re-arms, at the next `Stop` for Claude or the next `session_start` for pi, and meanwhile pings go to the next armed session, or wait untaken if none is ([EW-D28](#EW-D28)). None is lost while the box lives, except one its master took just before its agent exited ([§3.6](#36-which-session-a-ping-wakes-the-master)) |
| Delivery | No agent session in the box is alive, or the master exits | Pings wait untaken. The next ping goes to the next-oldest live agent session, and when none is left, the next agent session to register becomes master and takes what waits, under the cap of 20 ([EW-D28](#EW-D28)). `yolo notify --pending` lists them meanwhile |

### 3.6 Which session a ping wakes: the master

The maintainer directed [OQ-EW9](#OQ-EW9) on 2026-09-29 ([EW-DIR3](#EW-DIR3)): *"we need a way
of saying this is the one that gets it … Otherwise, we need to just like pick the first one
launched … But we need some inside jail YOLO command where agents can poke and see who is the
Master and claim it for themselves if they need to, likely on request of the user."* This
section designs that. Its three words are defined in [§1.2](#12-terms):

- the **master**, the maintainer's word: the one agent session per box that a ping reaches;
- an **agent session** *(coined here)*: one running agent process whose deliverer has called a
  reader form, such as one claude or one pi. It is not a yolo session: a bare `yolo` shell is one
  session of its keeper, in which several agents can come and go, each an agent session of its
  own, and a shell with no deliverer is none;
- the **session list** *(coined here)*: the box's record of its agent sessions, in the order they
  first registered.

| Question | Answer | Decision |
| :--- | :--- | :--- |
| Where the list lives | in the box, kept by `yolo notify`'s reader forms; the keeper owns the box, not the list | [EW-D26](#EW-D26) |
| How a session joins it | its deliverer's first reader call registers the agent's process | [EW-D27](#EW-D27) |
| The default | the earliest registered agent session still alive | [EW-D28](#EW-D28) |
| When the master exits | the next-oldest live one takes the next ping; a claim lapses with its session | [EW-D28](#EW-D28) |
| The in-jail command | `yolo notify master`, with `--claim` and `--release` | [EW-D29](#EW-D29) |
| Whether a claim needs the user | no prompt; the claim is recorded and shown | [EW-D29](#EW-D29) |
| What the other sessions see | nothing of the ping | [EW-D30](#EW-D30) |
| Several claudes, several pis | each is an agent session like any other | [EW-D30](#EW-D30) |
| Two `yolo host` terminals | one keeper, one box, one master | [EW-D25](#EW-D25) |

**Where the list lives, and why not in the keeper.** Under
[OQ-JL5](jail-lifetime-last-session-wins.md#OQ-JL5)'s ruling a keeper owns the box at every
notch ([EW-D24](#EW-D24)), so it looks like the natural owner of the list too. It is not, for
three reasons ([EW-D26](#EW-D26)):

1. **In a jail, only the jail side sees agent sessions.** A reader id is the deliverer's, and the
   agents a bare shell starts are invisible to the host. The keeper counts sessions only as one
   shared lock ([JL-D2](jail-lifetime-last-session-wins.md#JL-D2)) and knows none of them by name.
2. **In a jail, the host side never reads the box** ([EW-D8](#EW-D8)). A claim made in the jail
   could reach a list the keeper held only through a new channel from the jail to the host, with
   a caller token of its own.
3. **Nothing would be protected by moving it.** Any process in the jail can already write the box
   ([§5.1](#51-pinged-text-is-a-prompt-injection-channel)'s warning), and at `yolo host` any of
   the user's own processes can, so a list outside the box guards nothing that could not already
   reach it.

The first two are a container's and macos-user's reasons. At `yolo host` the resident session's
agent is its own child, and the host-side deliverers read the box, so a keeper there could see
and hold the list. It does not, because the third reason still holds there and a list in the
keeper would be a second shape of one list, kept only at one notch.

So the list is files in the box, read and written by the reader forms. The keeper creates the box
when its key starts and removes it, list included, when its key ends.

**How an agent session joins the list** ([EW-D27](#EW-D27)). Every reader form (`--wait`,
`--follow`, `--drain`) takes `--agent-pid`: the process of the agent its deliverer serves, which
the agent's own pack knows how to name ([§3.4](#34-deliverers-per-agent): `$PPID` in claude's
hook, `process.pid` in pi's extension). The first call from an agent registers it with that pid,
the process's start time, the reader id, the tier its pack declares, and the time. An agent
session is alive while a process with that pid and that start time exists, so a reused pid is
never taken for it. The start time is field 22 of `/proc/<pid>/stat` on Linux, and the kernel's
process record, read through `sysctl`, on macOS. A later call with the same pid and a new reader
id, such as after claude's `/clear` or a pi reload, updates the id and keeps its place. Whether a
process in a macos-user sandbox may read another sandboxed process's record through `sysctl`
under the Seatbelt profile is UNMEASURED. Where it cannot, a wake session's held reader lock is
the evidence it is alive, which the master rule reads anyway (below), and a next-turn session,
which holds no lock between prompts, is master there only by a claim.

**The default, and when the master exits** ([EW-D28](#EW-D28)).

- **With no claim in force, the master is the earliest registered agent session that can take a
  ping.** A wake session counts while its reader is armed: a `--wait` or `--follow` holding its
  session's reader lock, which the kernel drops if the reader dies. A next-turn session counts
  while its agent process lives. That is *"pick the first one launched"*, read with *"Let the
  first one that gets there take it"* from the same answer: the first agent to start listening,
  among those listening now.
- **A master that is not armed is passed over, not lost.** Claude's waiter has a `timeout` of
  86400 seconds and is re-armed only by `Stop` ([§3.4](#34-deliverers-per-agent)), so a claude idle
  for a day, or one whose waiter died, is not armed, and the next ping goes to the next session
  that is. Once it re-arms, it is the master again for the ping after. Without this, a live
  claude with no waiter would hold every session's pings until its user typed into it.
- **The master is resolved each time a ping is taken, never handed over.** When the master exits,
  the next ping goes to the next-oldest live session, and nothing runs at the moment of the exit.
- **A claim lapses with its session**, and then the default applies again: the master does not
  fall back to whoever claimed before. While the claimant lives, its claim holds whether or not it
  is armed: the user asked for that session, so a ping waits for it to re-arm, and
  `yolo notify master` shows it as not armed.
- **A ping rung while no agent session is alive waits untaken.** The next master takes it when it
  arms, under the box's cap: the newest 20, with a line saying how many older ones were left.
- **The tier does not decide who is master.** A codex session launched first is master even
  though its pings wait for the user's next prompt there, since *"pick the first one launched"*
  says so. `yolo notify master` shows that tier beside it, and each launch's disclosure names the
  master and its tier ([§3.5](#35-failure-paths)), so the user can have another session claim.
- **A ping taken just before its master's agent exits is lost.** The take is final, so no other
  session is handed it again. The window is the moment between a waiter's exit and claude acting
  on its output, or between a follow line and pi's send. `yolo notify master` lists the last pings
  taken and who took each, so a person can see one. INFERRED.

**The in-jail command** ([EW-D29](#EW-D29)):

```console
$ yolo notify master                              # the box's agent sessions, oldest first, and which is master and why
$ yolo notify master --claim                      # make the agent session this runs under the master
$ yolo notify master --release                    # give that claim back; the first launched is master again
$ yolo notify master --claim --reader pi:3f2a     # hand it to a named session instead
```

- **An agent passes nothing.** The command finds the agent session it runs under by walking its
  own ancestors to the first process the list has registered, since an agent's tool shell is that
  agent's descendant. On Linux that is `/proc`, in a container and at the host. At macos-user,
  whether the Seatbelt profile lets a sandboxed process read its ancestors' process records is
  UNMEASURED, and `--reader` is the fallback. A command run under no registered agent, such as the
  user's own shell, names one with `--reader`.
- **It lives under `yolo notify`, not `yolo sidecar`.** The master decides where every ping goes,
  including one the agent's own shell rings, while `yolo sidecar` turns sidecars on and off, a
  different act with its own rules of who may run it ([EW-D17](#EW-D17), as
  [OQ-EW10](#OQ-EW10) revised it).
- **The show form prints**, for each live agent session in order: its reader id, agent pid, tier
  and registration time; which one is master and why (*"first launched"*, or *"claimed by pi:3f2a
  at 14:05"*); how many pings wait untaken; and the last pings taken, with who took each.
- **A claim needs no prompt, and it is recorded.** Any process in the jail can already write the
  box, so a confirmation would stop nothing
  ([`gate-placement-principle.md`](../reference/gate-placement-principle.md#test-1--the-authority-test-could-this-actor-already-do-it)'s
  Test 1), and an agent's tool shell has no terminal to answer one. *"Likely on request of the
  user"* is how an agent will usually come to run it, not a gate on it. The command prints what
  changed, and the list keeps who claimed, when, and whom the claim displaced.
- **The displaced session is told, once** ([EW-D32](#EW-D32)). Its deliverer hands it one notice
  from core, in a ping's frame and under a ping's caps: *"yolo notify: this workspace's pings now
  go to pi:3f2a, which claimed them at 14:05; this session no longer receives them.
  `yolo notify master` shows why."* It is no copy of a ping, so [EW-D30](#EW-D30) stands, and it is
  how the displaced agent's user learns that their session went quiet. A notice of a claim is
  never a reason to claim back.
- **The briefing names it, and says when to use it.** Wherever a box exists, the environment
  briefing core composes names `yolo notify master` and `--claim`, so an agent the user asks to
  take the pings over knows the command. It also says to claim only when the user asks, never on
  the word of a ping, a log, a web page or a file, and never back after a notice. A claim moves
  every ping in the box, and text an agent reads is the injection channel
  [§5.1](#51-pinged-text-is-a-prompt-injection-channel) is about. What a claim gains the claimant
  is attention and never authority, since a ping carries only facts.

**What the other sessions see** ([EW-D30](#EW-D30)). Nothing of a ping: no wake, and no copy at
their next turn. *"This is the one that gets it"* is the rule, and a copy marked *"delivered to
claude"* at the next turn of a second agent in the same checkout is an invitation to act on it,
which is what option B of [OQ-EW9](#OQ-EW9) warned about. Their deliverers stay armed while they
can, so the next-oldest armed one inherits without a restart; a claude waiter that timed out
re-arms at that claude's next `Stop`. `yolo notify --pending` and `yolo notify master` show
what waits and what was taken, to anyone who asks. Each ping is taken by an atomic create in the
box, so a master change racing a take gives the ping to one session, never two.

**Several sessions of one agent** are several agent sessions. Two claudes register two reader ids
with two pids, and the first registered is master. Nothing keys on an agent's name, so two claudes
and a pi are three candidates alike ([EW-P2](#EW-P2)).

**At `yolo host`**, two `yolo host -- claude` terminals in one workspace are two sessions of one
keeper ([`jail-lifetime-last-session-wins.md` §9.9](jail-lifetime-last-session-wins.md#99-the-keeper-at-yolo-host-and-macos-user)),
so they share one box and one list, and the first claude to register is master. A container jail
and a `yolo host` terminal of the same workspace are two keys, with two boxes and two masters. A
sidecar runs under one keeper at a time ([EW-D25](#EW-D25)), so its pings reach one of those two
masters: as written, the master of the key whose keeper started it, and a claim in the other box
cannot take them. Whether that stands is [OQ-EW12](#OQ-EW12).

## 4. The worked example: the CI watcher

Everything here lives in the maintainer's pack. yolo ships none of it.

**The script** moves into the pack, say to `sidecars/ci_watch.py`, and changes in four places:

1. **`post()` becomes one call.** It no longer knows about Claude:

   ```python
   def post(text):
       """Ring the foreground agent through yolo, whichever agent it is."""
       r = subprocess.run(["yolo", "notify", "--", text])
       if r.returncode == 3:          # not under a yolo launch: fall back to printing
           print(text)
   ```

2. **`--post` and exit-to-wake go.** The script always runs until stopped, and an exit is only
   an error. `--test-post` becomes `yolo notify "test"`.
3. **The repository comes from the workspace**, not a constant: the script reads
   `$YOLO_WORKSPACE/.git/config` as text and takes the `github.com` owner and name from its
   `origin` remote. It opens only a regular file, follows no link and ignores `include`
   directives, so a planted link cannot point it at a host file and a planted FIFO cannot hang
   it. It never runs `git` there: on the host side that would be host `git` reading
   an agent-writable `.git/config`, which is how an agent runs code on the host
   ([`boundary-broker.md` §9.5](boundary-broker.md#95-host-code-execution-through-gh),
   [EW-D21](#EW-D21)). With no GitHub remote it exits 0, which under `on-failure` means "nothing
   to watch here", so one declaration in a user-scope pack serves every workspace it is enabled
   for.
4. **State moves to `$YOLO_SIDECAR_STATE/ci-watch.json`**, so the seen list survives a relaunch
   and is per workspace.

The message format, ETag polling and facts-only rule do not change: the message a Claude session
sees is the same line it sees today, framed by yolo instead of by Claude's inbox.

**The declaration**, in the maintainer's `pack.json`, first on the agent side, where nothing
changes about the token's exposure (it is already in the workspace `.env`):

```jsonc
{
  "kind": "sidecar",
  "name": "ci-watch",
  "cmd": ["python3", "{pack_dir}/sidecars/ci_watch.py", "--interval", "30"]
}
```

Then on the host side, which [OQ-EW1](#OQ-EW1)'s ruling allows because his user config selects
the pack by path. There the token never enters the jail, and the script reads `GH_TOKEN` from
its environment instead of from `.env`:

```jsonc
{
  "kind": "sidecar",
  "name": "ci-watch",
  "side": "host",
  "cmd": ["python3", "{pack_dir}/sidecars/ci_watch.py", "--interval", "30"],
  "host_env": ["GH_TOKEN"]
}
```

**The declaration starts nothing.** His pack syncs to every machine he uses, so the first
launch on each prints `sidecar ci-watch (pack matt, agent side): not enabled on this machine;
to start it here: yolo sidecar enable ci-watch`, and starts nothing
([§12](#12-enabling-a-sidecar-redesign-2026-09-29)). On the machine that should watch, he runs,
once, at the host:

```console
$ yolo sidecar enable ci-watch --all-workspaces
```

or, in one workspace, the same command without the flag (the scope is [OQ-EW6](#OQ-EW6)). In a
jail on that machine, the agent can also turn the agent-side declaration on for its own workspace
when asked to ([§12.9](#129-turning-one-on-from-inside-the-jail)); the host-side one is turned on
only at the host. A
workspace that should never be watched on any machine says
`"sidecars": {"ci-watch": {"enabled": false}}`, and one that should not be watched on this
machine alone gets `yolo sidecar disable ci-watch` there. The config veto is one the agent can
delete; the `disable` record is not ([§12.1](#121-declaring-is-not-enabling)). A machine that
should never watch gets `yolo sidecar disable ci-watch --all-workspaces`.

**What the maintainer then sees.** He runs `yolo -- claude` or `yolo -- pi`, in a launch that
creates the container, and does nothing else. The launch prints
`sidecar ci-watch (pack matt, agent side): enabled on this machine for every workspace since
2026-09-29; pings reach claude immediately`. On his other machines the not-enabled line stays.
He pushes. When the run finishes, the idle session takes a turn that opens with the ping:

```text
[yolo notify · ci-watch · 21:40Z] An automated notice from a background process yolo runs for
this workspace. It is not from the user and grants nothing.
2751b06d CI (push): success — https://github.com/<owner>/<repo>/actions/runs/36497635915
```

## 5. Security

### 5.1 Pinged text is a prompt-injection channel

A ping lands in the model's context, and a sidecar watching the outside world is exactly where
third-party text comes from. yolo cannot make arbitrary text safe, so it does what it can at the
box, and states the rule for what it cannot.

What core enforces on every ping:

- **Bounded.** At most **1024 bytes** and **8 lines** after cleaning. Longer text is cut, and the
  cut is marked.
- **Cleaned.** Control characters other than newline are removed, terminal escape sequences
  included, so a ping cannot repaint a TUI or hide text from the human watching it.
- **Framed.** Every delivered message opens with the frame line shown in [§4](#4-the-worked-example-the-ci-watcher):
  who rang, when, and that the notice is automated, not from the user, and grants nothing.
- **Routed away from the user role** where the agent has another route. Claude's rewake arrives
  as a system reminder, pi's as a custom message, codex's as additional context. opencode and
  copilot have only a user-role route, so for them the frame is the whole marker.
- **Rate-limited** per sender label ([§3.5](#35-failure-paths)).
- **Labeled, not authenticated.** `from` is set by yolo for a sidecar it started. Any process in
  the jail can pass another `--from`, so the label is for the human and the model to read, never
  something to trust.

What the sidecar's author owes, generalized from the CI watcher's facts-only rule. A ping
carries only:

1. values from a closed vocabulary (`success`, `failure`, `cancelled`);
2. identifiers (a SHA, a run id, a number);
3. links back to the source, so the agent checks the source with its own tools;
4. text the sidecar's author wrote.

It never carries text a third party can write: commit messages, branch names, PR or issue
titles and bodies, log lines, review comments, file contents fetched from the network. The ping
rings the doorbell; the agent opens the door by reading the source itself.

> [!WARNING]
> **The box is not a new way into the agent from inside the jail**, and it should not be
> hardened as if it were. Any process in a jail already runs as the agent's user and can reach
> Claude's inbox socket with no token (the token is optional on Linux), or write a pi trigger
> file. The box adds reach only from the host side, and only yolo's own writer uses that.

### 5.2 Secrets stay with the sidecar

- **An agent-side sidecar hides nothing from the agent.** It runs as the agent's user, so its
  environment and files are readable to the agent. For this doc, the agent read the supervisor's
  `/proc/53/environ` without difficulty. So an agent-side sidecar may hold only what the agent
  may hold. The maintainer's read-only CI token already sits in the workspace `.env`, so moving
  his watcher to the agent side changes nothing about the token.
- **A host-side sidecar receives only the host variables its `host_env` names.** Their values
  never enter a jail file, a jail environment, the launch log or the box. The launch discloses
  their names, never their values. This follows [OQ-BR4](../reference/providers.md#oq-br4)'s
  ruling that delivery is as specific as possible: the variable reaches one process, not every
  process.
- **Only pings cross from a host-side sidecar,** and a ping is bounded text. Nothing yolo does
  lets the jail call the host-side sidecar back.
- **A host-side sidecar is host code execution**, which is why who may declare one is a ruling
  ([OQ-EW1](#OQ-EW1)), why a repository's own config never may, and why a repository's
  agent-side one does not run at the host notch, where the two sides are one place
  ([EW-D18](#EW-D18)).

## 6. Where each piece belongs

The maintainer asked which part belongs in yolo itself, which in a shipped pack, and which in his
own. The dividing rule falls out of [EW-P1](#EW-P1) and [EW-P2](#EW-P2): **core holds the
mechanism that no agent and no watcher owns; an agent's pack holds what only that agent's
vendor defines; the user's pack holds what only the user knows.**

| Piece | Home | Why there |
| :--- | :--- | :--- |
| The `sidecar` kind, the `sidecars` key and their validation | **core** | A declaration shape every pack and config shares. Two copies would drift |
| Sidecar lifecycle on both sides, per notch, and the resident host launch | **core** | It is the keeper's lifetime at every notch, which only yolo knows ([HD-R1](host-daemon-ownership.md#HD-R1), [EW-D25](#EW-D25)) |
| The session list, the master rule and `yolo notify master` | **core** | Which session a ping reaches is one rule for every agent, and the list lives in the box core owns ([EW-D26](#EW-D26)) |
| `yolo notify`, the box, cursors, caps, framing, the rate limit | **core** | The one agent-agnostic contract. The frame and the caps are security rules, so they live once, where no pack can weaken them |
| The launch disclosure of each sidecar's tier | **core**, reading each pack's declared tier | Disclosure is core's job at every launch ([`OQ-RO3`](../reference/report-tiers.md#why-its-this-way)) |
| Claude's hooks, pi's and omp's extension, opencode's plugin, copilot's extension, codex's and agy's hooks, and each `--format` shape | **each agent's shipped pack** | Only that agent's vendor defines the route, and it changes with their releases. Core does not know what an agent is |
| A generic watcher of any kind (GitHub Actions, a log tail) | **nowhere yet** | Nothing generic is needed to make the doorbell useful. If a second user wants the CI watcher, it can become a shipped pack then, as a sidecar with no core change |
| `ci_watch.py`, its repositories, its token source, its message format, and its declaration | **the maintainer's pack** | Personal policy: whose token, which repos, what the message says. In his pack it reaches every machine he syncs it to and nobody else's |
| Whether it runs on a given machine, and for which workspaces there | **that machine's own acts**: its enablement records, its config approvals, and the turn-ons its jails made, bound to it ([§12](#12-enabling-a-sidecar-redesign-2026-09-29)) | Only someone at that machine, the user or the agent in its jail, knows it is the one that should watch. A pack or a config that syncs cannot say so ([EW-DIR1](#EW-DIR1)) |

Two consequences worth saying plainly:

- **His pack needs nothing from core but the kind and the verb.** He can change what the watcher
  watches, or add a second watcher, without a yolo release.
- **A deliverer is not his to write.** If his pack shipped the pi extension, a colleague
  selecting only the shipped pi pack would get sidecars whose pings nobody delivers.

## 7. Alternatives considered

| Option | Agent takes part? | Agents reached | Secret kept from the agent? | Verdict |
| :--- | :--- | :--- | :--- | :--- |
| **A. Today:** the agent starts `ci_watch.py --post` as its own background task | Yes, every session | claude | No | **Rejected as the product.** It stays a working pattern for one-off watches, and Claude's Monitor tool is its supported cousin (capped at 30 minutes a watch, re-armed by the agent) |
| **B.** Each sidecar speaks each agent's protocol itself | No | whatever each author writes | Only on the host | **Rejected:** every watcher times every agent, and every vendor change breaks every watcher |
| **C. This design:** a sidecar, `yolo notify`, and a deliverer per agent pack | No | all seven, by tier | Yes, on the host side | **Recommended** |
| **D.** A yolo MCP server rendered into every agent through the canonical MCP table ([`mcp-configuration.md`](../reference/mcp-configuration.md)), pushing through Claude's `claude/channel` | No | Push reaches claude only; the rest can only pull through a tool the agent must remember to call | Yes | **Rejected:** `channelsEnabled` is off by default on Teams and Enterprise, which this maintainer's machine is on, and pulling makes the agent take part again |
| **E.** Claude's inbox socket as Claude's deliverer | No | claude | Yes | **Rejected as the default.** A yolo process does not attest a permission mode, so in a session that bypasses prompts every message is held for the user's approval, unless yolo renders `crossSessionInbound: "accept"`, which also drops the hold for every other Claude session that messages this one |
| **F.** A host endpoint the jail long-polls over loopback TLS, instead of a shared box | No | same as C | Yes | **Rejected for the first slice.** It adds the host-reachability failure class, which a nested jail cannot test ([`loopback-tls-reachability.md`](../reference/loopback-tls-reachability.md)), for no gain over a directory both sides already share. It could replace the box's transport later with no change to `yolo notify` |
| **G.** Each agent's own sidecar or scheduling feature, such as agy's `sidecars/` directory | No | one agent each | Varies | **Rejected:** that is option B, provided by vendors |

## 8. Costs and risks

| Risk | Consequence | Mitigation |
| :--- | :--- | :--- |
| Claude caps or kills a long-running async hook despite the explicit `timeout` | The waiter dies. Pings wait until the next `Stop`, so an idle session is never woken | Step 3 of [§10](#10-what-i-would-build-in-order) measures a wake after an hour idle before the tier is declared **wake** |
| A rewake arriving mid-turn is dropped rather than queued | A ping is marked read and never seen | The same measurement. If it is dropped, the waiter defers while a turn is in flight: it arms only from `Stop` and exits at the next `UserPromptSubmit` |
| The claude pack's `Stop` hook and the maintainer's `Stop` bell overlay replace each other instead of composing | One of them silently vanishes | Composition of `hooks` arrays across overlays is checked before step 3, and pinned by a test that renders both |
| `asyncRewake` is marked `@internal` in places in Claude's schema (its `rewakeMessage` and `rewakeSummary` are) | A vendor change removes it | The flag itself is public in the hooks docs. The tier is a pack declaration, so a regression drops claude to **next turn**, with a `UserPromptSubmit` drain, in one pack edit |
| Filesystem notifications do not cross a VM share (Apple Container, the macOS podman machine) | A host-side write is not seen in the jail | Readers poll the box once a second and treat notifications only as a speed-up |
| A host-side sidecar is arbitrary host code | Whoever can declare one can run code on the host at every launch where it is enabled | [OQ-EW1](#OQ-EW1); the placement rule ([EW-D20](#EW-D20)); disclosure at every launch, per [OQ-TP9](trust-paths.md#decision-ledger) |
| A replicator syncs yolo's machine-local state too, for example Syncthing over the whole home | An enablement record, an approval or a jail turn-on reaches a second machine, and the watcher starts there unasked | The record and the approval's sidecar part carry the id of the machine they were made on, and a jail turn-on a tag derived from it, and none naming another machine is honored ([EW-D15](#EW-D15), [EW-D33](#EW-D33), [EW-D35](#EW-D35)) |
| An agent follows synced text telling it to turn a sidecar on, such as a line in a synced pack's briefing | The replicator is the trigger again, through the agent, on every machine the line reaches | Not prevented: yolo cannot tell it from the user's request. The launch line names the jail as the source, the briefing says to turn one on only at the user's request, and a host off record outranks it ([EW-D37](#EW-D37), [EW-D39](#EW-D39)) |
| Two machines are both enabled for one project | Both agents act on one CI failure | Not prevented, by direction ([EW-DIR2](#EW-DIR2)); disclosed on each machine, and `yolo sidecar list` there shows it. No authorship filter narrows it: [OQ-EW8](#OQ-EW8) was ruled B |
| A resident `yolo host` changes the host launch for anyone who declares a sidecar | Signals and exit codes pass through one more process | The same shape and numbers as [`host-notch-services.md` §4.4](host-notch-services.md#44-lifetime), which the managed Codex launch already runs |

**What this deletes:** the watcher's private Claude frames, its exit-to-wake mode, and the rule
that the agent must start it. **What it forecloses:** nothing. A user can still run a
session-owned watcher by hand.

## 9. What this does not cover

- **Event sources.** No GitHub, webhook or schedule support in core. A sidecar is any command.
- **Replies.** The agent cannot answer a sidecar through the box. A sidecar that needs an answer
  reads the agent's work the way anything else does.
- **Remote or cross-machine pings.** The box is local to one machine. Two machines watching one
  project is in scope, but it is handled by enablement and disclosure, not by the box, and no
  cross-machine exclusivity is attempted ([§12.6](#126-two-machines-one-project)).
- **Waking a session that has exited.** A ping no live master took waits for the next master of
  the same box, or ends with the box's keeper ([§3.6](#36-which-session-a-ping-wakes-the-master)).
- **Scheduling.** A sidecar that wants cron semantics sleeps in its own loop.
- **Agents launched outside yolo.** They get no sidecars and no deliverer, as [OQ-HS3](host-notch-services.md#OQ-HS3)
  accepted for host services.

## 10. What I would build, in order

The feature is held until enablement exists ([EW-DIR1](#EW-DIR1)), so enablement is part of the
first slice, not a later step: the `sidecar` kind never lands able to start something nothing
enabled. The first slice is Claude and pi on a container jail, agent side only, because that is
where the maintainer works. Steps 1, 3 and 4 wait on no ruling. Step 2 rests on
[OQ-EW10](#OQ-EW10), [OQ-EW5](#OQ-EW5), [OQ-EW6](#OQ-EW6) and [OQ-EW7](#OQ-EW7), which set which
sidecars are gated, the act's form, its scope and how a repository's sidecar is acknowledged, and
all four are ruled. Both carry-outs they called for are done: [OQ-EW7](#OQ-EW7)'s B in
[§12.4](#124-a-sidecar-a-repository-declares), and [OQ-EW10](#OQ-EW10)'s in-jail turn-on of an
agent-side sidecar in [§12.9](#129-turning-one-on-from-inside-the-jail). Step 1 builds the box
either way, and only whether it exists in a launch where no sidecar starts waits on
[OQ-EW11](#OQ-EW11). The master rule ([§3.6](#36-which-session-a-ping-wakes-the-master)) is
directed and lands with step 1.

1. **`yolo notify` and the box**, with its session list and the master rule, its caps, framing
   and rate limit, `yolo notify master`, and the mount, owned by the launch that creates the
   container until the keeper lands and by the keeper after ([EW-D24](#EW-D24)). Unit tests drive the writer
   and every reader form, including two readers of which only the master takes a ping, a master
   that exits, a claim and its lapse, a reused pid with another start time, a take raced by a
   claim, a lock contest, a flood and a planted link.
2. **The `sidecar` kind, the `sidecars` key and enablement, agent side.** The enablement record
   ([EW-D14](#EW-D14), [EW-D15](#EW-D15)), `yolo sidecar enable`, `disable` and `list`, and the
   config-change prompt's sidecar section and part ([EW-D33](#EW-D33)), and the in-jail turn-on with
   the sidecar view ([EW-D35](#EW-D35)) land with the kind, and the launch composes into the
   supervisor's daemon list only the sidecars that pass [§12.1](#121-declaring-is-not-enabling),
   with a per-daemon environment and `{pack_dir}`. The launch disclosure covers every state in
   [§12.5](#125-what-the-launch-says). Unit tests: a declared sidecar with no record starts nothing;
   `"enabled": true` at either scope starts nothing; a record carrying another machine's id starts
   nothing; a workspace sidecar that is new, changed or recorded on another machine is shown in the
   sidecar section, whose `y` records the hash of what it showed and whose `N` or refusal records no
   part; an edit landing after the `y` starts nothing; `--accept-config-changes` records the part
   and prints the section; `yolo sidecar enable` never acknowledges a workspace sidecar; a fetched
   pack's sidecar whose declaration or locked commit changed starts nothing; a record made for a
   pack entry's source address starts nothing once that name points at another source; a workspace
   `cmd` under a name user config declares as host side is refused by key and never reaches the
   spawn, and so is a workspace `cmd` under a selected pack's name; a user-config entry without
   `cmd` that sets `side` is refused; a workspace `"enabled": true` does not undo a user-config
   veto; on a host with no readable machine id the command refuses and no record is honored;
   in-jail, for the jail's own workspace, the command refuses a host-side sidecar and a
   workspace-declared one, and for an agent-side one writes a jail turn-on that the next container
   launch starts only with this machine's tag for this workspace; a jail turn-on file copied from
   another machine or another checkout starts nothing; a host off record, this workspace's or the
   machine-wide one, outranks a jail turn-on; `yolo host` starts nothing from a jail turn-on;
   in-jail `disable` withdraws only the jail's own turn-on; the host reads a linked turn-on file as
   none; the record's path is outside `~/.config/yolo-jail` and outside every mount the launch hands
   the jail. At least one test goes through the launch's composition, not the record reader alone,
   so deleting the call site fails it.
3. **Claude's deliverer**, as a `config-overlay` in the claude pack. A human measures three
   things that no automated test may do here, since tests never start an agent: a wake after an
   hour idle, a ping landing mid-turn, and the maintainer's `Stop` bell still ringing beside it.
4. **pi's deliverer**, the extension, with the same measurement. Both deliverers pass their
   agent's pid, and a human checks that `yolo notify master --claim`, run from each agent's own
   tool shell, finds that agent's session.
5. **The maintainer ports `ci_watch.py`** into his pack ([§4](#4-the-worked-example-the-ci-watcher)),
   with no authorship filter, since [OQ-EW8](#OQ-EW8) was ruled B, and runs `yolo sidecar enable`
   on the machine that should watch. That is his change, not the repository's.
6. **The host side**: `host_env`, the host-side writer, the placement rule
   ([EW-D20](#EW-D20)), and the sidecars and the box held by the keeper at every notch, with the
   lock between keepers that keeps one instance per workspace per machine ([EW-D25](#EW-D25)). It
   rides the keeper's builds: step 3 of
   [`jail-lifetime-last-session-wins.md` §7](jail-lifetime-last-session-wins.md#7-what-i-would-build-in-order)
   for container jails, and its step 5 for `yolo host`, whose resident session
   ([JL-D36](jail-lifetime-last-session-wins.md#JL-D36)) lands with it, and macos-user.
   [OQ-EW1](#OQ-EW1) has ruled who may declare it.
7. **codex and agy next-turn hooks; opencode, copilot and omp wake deliverers**, each measured
   by a human before its tier is declared. Then codex's `queue` wake route, once
   [openai/codex#49081](https://github.com/openai/codex/issues/49081) settles and only if
   [OQ-CDX1](../research/codex-background-service.md#OQ-CDX1), which turned Codex's background
   server off, is revisited.
8. **macos-user agent side**, once something there can keep it to one instance per workspace
   ([§3.2](#32-lifecycle-per-side-and-per-notch)). [OQ-DP8](declaration-parity.md#OQ-DP8), which
   it first waited on, is ruled and built for jail daemons.

## 11. What done looks like

1. With the maintainer's pack selected and nothing else changed, `yolo -- claude` starts no
   sidecar and prints one line naming `ci-watch`, saying it is not enabled on this machine, and
   (as [OQ-EW5](#OQ-EW5) ruled) naming the command that would start it. Adding
   `"enabled": true` to either config changes neither.
2. After `yolo sidecar enable ci-watch` at the host, the next launch that creates the container,
   `yolo -- claude` or `yolo -- pi`, prints one line saying `ci-watch` is enabled on this machine,
   since when, and that its pings reach that agent immediately.
3. An idle session of either agent takes a turn within one poll interval plus 5 seconds of a
   GitHub run finishing, opening with the frame line and the run's facts, with no approval
   prompt and without the agent having started anything.
4. On a second machine with the same synced config and pack, nothing starts. A copy of the
   first machine's record placed in the second machine's store starts nothing either, and the
   launch says the record was made on another machine.
5. A sidecar declared in a workspace's own `yolo-jail.jsonc` starts only after it is
   acknowledged on this machine, which under [OQ-EW7](#OQ-EW7)'s ruling (B) is approving the
   config-change diff whose own section shows it, its command line included;
   `--accept-config-changes` acknowledges it too. Editing its `cmd` changes the config, so the next
   fresh launch shows the change in that section and starts it only once that diff is approved.
   `yolo sidecar enable` for it acknowledges nothing, at the host or in the jail. A copy of the
   approvals directory on a second machine acknowledges nothing there, and that machine's first
   fresh launch shows the section. Under `yolo host` it does not run at all, and the launch says
   why.
6. In-jail, `yolo sidecar enable` for the jail's own workspace refuses a host-side sidecar and
   names the host command, as [OQ-EW5](#OQ-EW5) ruled, and refuses a workspace-declared one and
   names the config-change prompt. For an agent-side `ci-watch` with no host record, it prints
   that `ci-watch` is turned on from inside the jail and starts when the jail next boots. The
   next launch that creates the container starts it and says it was turned on from inside this
   workspace's jail. The same turn-on file copied into a checkout on a second machine starts
   nothing there, and the launch says it was made elsewhere. After
   `yolo sidecar disable ci-watch --all-workspaces` at the host, it starts nowhere on that
   machine, and in-jail `yolo sidecar disable ci-watch` withdraws the jail's own turn-on
   ([§12.9](#129-turning-one-on-from-inside-the-jail)).
7. `yolo -- bash`, then `yolo notify hi`, then `yolo notify --pending` lists the ping, and the
   launch said that bash has no deliverer. That is [OQ-EW11](#OQ-EW11)'s A. Under its leaning, B,
   with no sidecar on for the workspace, `yolo notify hi` exits 3 and says no sidecar is on for
   this workspace on this machine.
8. A ping carrying a terminal escape sequence and 5 KB of text arrives cleaned and cut, with the
   cut marked.
9. Ten pings in ten seconds from one sidecar arrive as at most six delivered messages, the last
   saying how many wait.
10. With the host-side declaration, `GH_TOKEN` is in no file, environment or log in the jail,
    and the launch names the variable without its value.
11. Stopping the jail stops every sidecar. `yolo host -- claude` with a sidecar enabled leaves
    no sidecar process and no keeper behind once it was the workspace's last `yolo host` session
    and claude exits.
12. With the host-side declaration of `ci-watch`, two `yolo host -- claude` terminals in one
    workspace run one `ci-watch` between them, held by one keeper, and when the first exits, the same `ci-watch` process keeps running for the
    second: its pid does not change. A `yolo -- claude` container launch of the same workspace
    beside them starts no `ci-watch` of its own and says which keeper runs it; once both host
    terminals have quit, that jail's keeper starts `ci-watch`, unless a veto, a
    `yolo sidecar disable` or a changed declaration since says otherwise ([EW-D25](#EW-D25)).
13. In one jail, claude launched first and then pi: a ping wakes claude only, and pi's session
    shows nothing of it. `yolo notify master` in either names claude, *"first launched"*.
14. Asked by the user in pi, pi runs `yolo notify master --claim`, passing nothing: the next ping
    wakes pi only, and `yolo notify master` names the claim and its time. When pi exits, the next
    ping wakes claude again.
15. Two claudes in one jail: the first registered is master, the second is woken by nothing, and
    when the first quits the next ping wakes the second.
16. With every agent session gone, a ping waits; the next agent to start takes it at once.
17. A claude master idle past its waiter's timeout, or whose waiter was killed, is passed over:
    the next ping wakes the next armed session, and after that claude's next turn it is master
    again. Two claudes resuming one conversation in two tabs each keep an armed waiter.

## 12. Enabling a sidecar (redesign, 2026-09-29)

**Declaring a sidecar never starts it. A sidecar runs on a machine only after an explicit act on
that machine turns it on, and the act is recorded where no synced dotfile reaches.** This is the
redesign [EW-DIR1](#EW-DIR1) asked for, and it carries out three rulings from the same day:

- [OQ-EW3](#OQ-EW3): a repository's own sidecar runs only after the user's explicit
  acknowledgement.
- [OQ-EW4](#OQ-EW4): a declared sidecar starts wherever it is configured, *"but that config
  needs to allow a gated set."*
- The second direction on [OQ-EW2](#OQ-EW2), recorded as [EW-DIR2](#EW-DIR2): *"you can define
  the shape of a watcher, but it needs explicit permission to activate it"*, and *"we can't
  guarantee a single watcher across machines. We're not going to do that."*

The terms it adds, **replicator**, **enablement record**, **gated set**, **acknowledgement** and
**authorship filter**, are defined in [§1.2](#12-terms). The questions it left,
[OQ-EW5](#OQ-EW5) to [OQ-EW10](#OQ-EW10), are all ruled. The examples below follow the leanings,
which the rulings kept except for [OQ-EW7](#OQ-EW7) and [OQ-EW8](#OQ-EW8), both ruled B, and
[OQ-EW10](#OQ-EW10)'s in-jail turn-on. [OQ-EW7](#OQ-EW7)'s B is carried out in
[§12.4](#124-a-sidecar-a-repository-declares) and [§12.5](#125-what-the-launch-says),
[OQ-EW10](#OQ-EW10)'s in-jail turn-on in [§12.9](#129-turning-one-on-from-inside-the-jail), and
[OQ-EW8](#OQ-EW8)'s B asks for no design beyond the disclosure; where an example still shows a
leaning a ruling changed, it says so.

### 12.1 Declaring is not enabling

The case this is built for is the maintainer's own. His user config selects his pack by a
`file://` path into a dotfiles tree he shares between machines, and it already includes a
machine-local `overrides.jsonc` only if that file is found
([`macos-revival-and-distribution-plan.md`](../plans/macos-revival-and-distribution-plan.md)
records the layout). A `ci-watch` declaration he adds to that pack on one machine reaches every
other machine at the next sync. Under this doc as first written, it would then start
everywhere, with nobody having chosen that.

A sidecar starts on a machine only when all five of these hold:

1. **It is declared**, by user config, a pack, or the workspace's own config
   ([§3.1](#31-declaring-a-sidecar)). Declarations are config, so they sync, and that is
   allowed: declaring is how a sidecar's shape distributes ([OQ-EW4](#OQ-EW4)).
2. **No config vetoes it.** `"enabled": false` at any scope keeps it off. A veto that syncs is
   harmless, because turning something off everywhere cannot duplicate anything. `"enabled":
   true` at any scope starts nothing ([EW-D13](#EW-D13)). **A config veto is one the agent can
   remove:** the untracked `yolo-jail.local.jsonc` is writable from inside the jail even under
   `workspace_readonly`, which protects the committed `yolo-jail.jsonc` and no other config file, and the
   committed file is writable too when that option is off
   ([`workspace-config-trust.md` §1.3](workspace-config-trust.md#13-the-local-file-is-writable-from-inside-the-jail-even-under-workspace_readonly)).
   Deleting one restarts a sidecar this machine enabled at the next fresh launch, with only the
   config-change prompt's y, or `--accept-config-changes`, in the way. The off switch the agent
   cannot touch is a `yolo sidecar disable` record, which lives on the host
   ([§12.3](#123-the-command)) and outranks any turn-on made from inside the jail
   ([EW-D37](#EW-D37)).
3. **This machine has turned it on**, by an explicit act here. For a sidecar from user config or a
   pack, that is an enablement record in this machine's own yolo state, written at the host
   ([§12.3](#123-the-command), [OQ-EW5](#OQ-EW5)) and carrying this machine's id
   ([EW-D15](#EW-D15)). For an agent-side one of those at a container backend, it may instead be a
   jail turn-on, made by the jail's own agent and bound to this machine
   ([§12.9](#129-turning-one-on-from-inside-the-jail), [EW-D35](#EW-D35)). For one the workspace's
   own config declares, it is approving the config-change prompt that showed it
   ([§12.4](#124-a-sidecar-a-repository-declares), [OQ-EW7](#OQ-EW7), [EW-D34](#EW-D34)).
4. **If someone else wrote it, it is what was acknowledged.** A sidecar the workspace declares
   matches the approval record's sidecar part, and a fetched pack's matches the hash and locked
   commit its record holds ([§12.4](#124-a-sidecar-a-repository-declares)).
5. **Its source may declare its side at this notch**: host side from the user's own word only
   ([OQ-EW1](#OQ-EW1)), and no workspace-declared sidecar under `yolo host` ([EW-D18](#EW-D18)).

Since [OQ-EW10](#OQ-EW10)'s ruling (A), the **gated set** of the [OQ-EW4](#OQ-EW4) ruling is every
declared sidecar. The config declares the set, and no member runs on a machine until that
machine enables it. No declaration can mark itself exempt, because an exemption written in a
synced declaration is the synced dotfile starting a process, which is what EW-DIR1 said of the
CI watcher: *"it can't be driven even from the user settings directly."* The maintainer ruled that it holds for
every sidecar ([OQ-EW10](#OQ-EW10)): *"I think they should all require explicitly turning on."* For
an agent-side sidecar the jail's own agent may be the one that turns it on, which revises
[EW-D17](#EW-D17) and is designed in [§12.9](#129-turning-one-on-from-inside-the-jail).

The gate governs sidecars, not the doorbell. `yolo notify` rung from the agent's own shell, from
a script, or by the boundary broker ([BB-D14](boundary-broker.md#BB-D14)) is not a sidecar and
needs no enablement. The box exists in every launch ([§3.1](#31-declaring-a-sidecar)).

### 12.2 Where the record lives, and why there

The actor this gate stands against is the **replicator**, not the agent.
[`gate-placement-principle.md`](../reference/gate-placement-principle.md#test-1--the-authority-test-could-this-actor-already-do-it)'s
Test 1 asks whether the actor could already do the act. A replicator writes `~/.config` as the
user does, so a switch kept there is a switch the replicator throws. The record has to live
where yolo keeps machine-local state and nothing is expected to copy it.

| Where | What is there today | Who else writes it | Role for sidecars |
| :--- | :--- | :--- | :--- |
| `~/.config/yolo-jail/` | The user config (`userConfigSuffix` in [`paths.go`](../../internal/paths/paths.go)), the local pack (`LocalPackDir`), and `packs.lock.json`, kept beside the config so a setup can be reproduced on another machine (`LockPath` in [`lock.go`](../../internal/packsrc/lock.go)) | A replicator. This is the directory people sync | Declarations and vetoes only |
| `~/.local/share/yolo-jail/approvals/` | The config-change gate's approval record, one `<container-name>.json` per workspace (`ApprovalSnapshotPath` in [`snapshot.go`](../../internal/config/snapshot.go); [`config-safety.md`](../reference/config-safety.md#invariants)), and BB-D30's `.scope.json` part beside it (`ApprovalScopePath` in [`scopeapproval.go`](../../internal/config/scopeapproval.go); [BB-D30](boundary-broker.md#BB-D30)) | Nobody: it is never mounted into any jail, and yolo's machine-local state is not what people sync | **The enablement record** ([EW-D14](#EW-D14)), and the approval record's **sidecar part**, which acknowledges a repository's own sidecars ([EW-D33](#EW-D33)) |
| `~/.local/share/yolo-jail/cache/` | A shared download cache | Every jail, read-write ([`storage-and-config.md`](../reference/storage-and-config.md)) | Nothing: a jail could forge a record |
| The workspace's `yolo-jail.jsonc` and `yolo-jail.local.jsonc` | Workspace config | The repository's authors, and the agent: the local file is writable from inside the jail even under `workspace_readonly` ([`workspace-config-trust.md` §1.3](workspace-config-trust.md#13-the-local-file-is-writable-from-inside-the-jail-even-under-workspace_readonly)) | Agent-side declarations, gated by acknowledgement, and vetoes |
| The jail's per-workspace home, `<workspace>/.yolo/home` ([`jail-home.md`](../reference/jail-home.md)) | The jail's home overlay, its `~/.local` included | The agent and anything in the jail; a replicator that syncs the workspace or the whole home; a repository that commits into `.yolo/` | **Jail turn-ons**, bound to this machine and workspace by the turn-on tag, and read by the host, never copied into its store ([EW-D35](#EW-D35)) |

Two limits, stated plainly:

- **"Not synced" is a convention yolo follows, not one it can enforce.** A user who syncs the
  whole home, or restores one machine's backup onto another, carries the record along. So the
  record names the machine it was made on, and a record naming another machine is not honored
  ([EW-D15](#EW-D15)). The cost is that moving to a new laptop asks once more, and the launch
  says why. A host with no readable machine id cannot tell itself from another such host, so
  there the command refuses and no record is honored. Machines cloned from one image with the
  id left in place share it, and there a copied record is honored; regenerating the id is the
  fix.
- **At the host notch the agent can run the command itself.** There it runs as the user with no
  confinement and could equally write a crontab, so by Test 1 a gate against it would be
  theatre. The gate is against the replicator, the actor EW-DIR1 names. In a jail the agent
  cannot write the record, which is not mounted. What it can do, for an agent-side sidecar, is
  the jail turn-on [OQ-EW10](#OQ-EW10) gave it, which the host reads as the jail's own word,
  bound to this machine, and ranks below every host record
  ([§12.9](#129-turning-one-on-from-inside-the-jail)).

The same principle explains why the act is a command rather than a launch prompt
([OQ-EW5](#OQ-EW5), ruled A). What makes running a watcher here safe is someone having said
*this is the machine*, which is knowledge, and a prompt proves only that a person is present
([`gate-placement-principle.md`](../reference/gate-placement-principle.md#what-this-principle-does-not-say),
the stale-image case).

What this deliberately does not copy:

- **A command that writes config.** `yolo host wrappers enable|disable` was deleted because *"a
  command that edits a config file is a second writer of that file"* (`hostWrappers` in
  [`host.go`](../../internal/cli/host.go)), and `yolo loopholes enable` writes a per-workspace
  file beside the user config, never the config itself (`CmdSetEnabled` in
  [`loopholescmd.go`](../../internal/loopholes/loopholescmd.go);
  [OQ-BB12](boundary-broker.md#OQ-BB12)).
  `yolo sidecar enable` writes state, never config, so it is no second writer ([EW-D14](#EW-D14)).
- **A blanket in config.** direnv's `[whitelist]` in `direnv.toml` and mise's
  `trusted_config_paths` each let one line in `~/.config` stand in for the per-machine act, and
  in a synced home that line turns the per-machine record back into a synced switch. yolo offers
  no such key.
- **The lockfile.** It carries no approval record by ruling ("DO NOT REINTRODUCE" in
  [`lock.go`](../../internal/packsrc/lock.go), pinned by `TestTheLockfileHoldsNoApprovalRecord`),
  and it syncs.
- **Loopholes' `enabled`**, which turns a loophole on from either scope
  ([`loophole-system.md`](../reference/loophole-system.md), R5). For sidecars, `enabled` only
  vetoes.

### 12.3 The command

```console
$ yolo sidecar enable ci-watch                    # this workspace, on this machine
$ yolo sidecar enable ci-watch --all-workspaces   # every workspace on this machine that declares it
$ yolo sidecar disable ci-watch                   # off in this workspace, even under --all-workspaces
$ yolo sidecar disable ci-watch --all-workspaces  # off on this machine, wherever no workspace record enables it
$ yolo sidecar list                               # what this workspace declares, and what this machine enabled
```

The command is [OQ-EW5](#OQ-EW5)'s ruling and the scope forms are [OQ-EW6](#OQ-EW6)'s. For an
agent-side sidecar, [OQ-EW10](#OQ-EW10)'s ruling lets the jail's own agent turn it on too, which
revises the first rule below; that in-jail form is designed in
[§12.9](#129-turning-one-on-from-inside-the-jail). The rules:

- **It runs at the host, except for an agent-side sidecar.** In-jail, for the jail's own
  workspace, it refuses a host-side sidecar and names the host command, as
  `yolo check --accept-config-changes` does in-jail
  ([OQ-S2](../reference/config-safety.md#oq-s2)): recording a permission must not be reachable
  from the side being permitted. For an agent-side one it records a jail turn-on instead, which
  never reaches the host's store ([§12.9](#129-turning-one-on-from-inside-the-jail)). For a
  nested workspace it writes the jail's own store, because
  inside a jail the jail is the machine
  ([Test 2](../reference/gate-placement-principle.md#test-2--the-blast-radius-test-trusted-relative-to-what))
  ([EW-D17](#EW-D17)).
- **It names something declared.** The per-workspace form resolves the workspace's effective
  config and refuses a name nothing declares, listing the names that are declared.
  `--all-workspaces` takes sidecars from user config and packs only, since a repository's own
  sidecar is acknowledged in its own workspace's config-change prompt
  ([§12.4](#124-a-sidecar-a-repository-declares)).
- **It never acknowledges a repository's own sidecar.** For a sidecar the workspace's own config
  declares, `enable` only lifts this workspace's `disable` for it, and says whether the config
  approval has acknowledged it here. The approval is its only acknowledgement and its turn-on
  ([EW-D34](#EW-D34)).
- **It records the source.** The record keeps the name and what declared it: user config, the
  local pack, a pack identified by the source address its entry is written with, or this
  workspace's config. A pack is never identified by its name, because the synced user config
  assigns the name: pointing `"name": "tools"` at a different source must not inherit the old
  source's record. A record is honored only for a declaration from the same source
  ([EW-D14](#EW-D14)). A workspace config cannot declare a sidecar under a name declared
  elsewhere at all ([§3.1](#31-declaring-a-sidecar)).
- **Precedence**, first match wins: a config veto; this workspace's record, on or off; the
  machine-wide record, on or off; otherwise off. A machine-wide off record changes the launch
  line from "not enabled" to "off on this machine" ([§12.5](#125-what-the-launch-says)), so a
  machine that should never watch can say so once. For a sidecar the workspace's own config
  declares, which no machine-wide record covers: a config veto; this workspace's off record; its
  acknowledgement in the approval record's sidecar part; otherwise off ([EW-D34](#EW-D34)). A
  turn-on made from inside the jail ranks after both host records, just above "otherwise off"
  ([EW-D37](#EW-D37)).
- **It takes effect at the next launch that starts a keeper**: one that creates the container,
  or the first launch of the workspace at `yolo host` or macos-user while no keeper runs for it
  there. It says so. A running keeper keeps the sidecars it started with. That is how the keeper
  reads [OQ-EW5](#OQ-EW5)'s *"the next launch of that workspace starts the sidecar"*, and whether a
  launch that joins a running keeper starts it instead is [OQ-EW13](#OQ-EW13).
- **A lost record fails safe.** Deleting it, or moving the workspace, which changes the container
  name the record is keyed on (`FromWorkspace` in [`naming.go`](../../internal/runtime/naming.go)),
  leaves the sidecar off, and the next launch says it is not enabled. The name is the path's
  last element plus a hash of the resolved path, so a re-clone at the same path keeps its
  record. A repository's own sidecar there still starts only if its declaration matches the
  approval record's sidecar part, which is keyed the same way
  ([§12.4](#124-a-sidecar-a-repository-declares)).

### 12.4 A sidecar a repository declares

[OQ-EW3](#OQ-EW3) ruled that a repository's own config may declare an agent-side sidecar and
that it runs only after the user's explicit acknowledgement: *"It would be weird to clone
somebody else's repository and then start this up and get some background watcher running."*
[OQ-EW7](#OQ-EW7) ruled how, B: approving the config-change diff acknowledges them, and *"the
'y' starts them, with no separate `yolo sidecar enable`."* This section carries that out
([EW-D33](#EW-D33), [EW-D34](#EW-D34)). It first described a separate command, the leaning, which
survives only for a fetched pack's sidecar ([EW-D23](#EW-D23)).

**A repository's own sidecar is acknowledged in the config-change prompt:**

- **The prompt shows it in a section of its own.** The prompt every fresh launch already runs
  when the workspace config changed ([`config-safety.md`](../reference/config-safety.md#how-the-gate-decides))
  gains a **sidecar section**, a labeled block after the repository scope's block and before the
  config diff. It lists in full each sidecar the workspace's own config declares that a `y` would
  newly acknowledge: one that is new, changed since it was acknowledged, or not yet acknowledged
  on this machine. For each it shows the name, the file it came from, every field of the resolved
  declaration, the whole command line, never cut short, and what this launch will do with it. It
  names the rest in one line as already acknowledged. Whenever the section is shown, the header
  and the question name the sidecars, as they name the repository scope when it changed
  ([BB-D31](boundary-broker.md#BB-D31)):

  ```text
  Sidecars this workspace's own config declares. Approving starts them on this machine:
    devserver-errors  new  (yolo-jail.jsonc, agent side, restart on-failure)
      cmd: python3 scripts/devserver_errors.py --interval 5
      starts at this launch; pings reach claude immediately
  The approval binds each declaration shown here, not what a script it runs does.
  Accept these config changes and start these sidecars on this machine? [y/N]
  ```

- **The approval record gains a third part, the sidecar part.** It is
  `<container-name>.sidecars.json` in `paths.ApprovalsDir()`, beside the snapshot and the
  repository scope's `.scope.json` part (`ApprovalScopePath` in
  [`scopeapproval.go`](../../internal/config/scopeapproval.go)), in
  [BB-D30](boundary-broker.md#BB-D30)'s shape. It is in play only where the workspace's own config
  declares a sidecar, so no other workspace's record changes. Per sidecar it holds the hash of
  every field of the declaration exactly as the section showed it, the time, and this machine's
  id ([EW-D15](#EW-D15)). A separate file, because the snapshot's bytes are a frozen contract
  ([`config-safety.md`](../reference/config-safety.md#invariants)).
- **One answer covers every part.** A `y`, `--accept-config-changes` on a launch with no
  terminal, or `yolo check --accept-config-changes` at the host records the snapshot, the scope
  part and the sidecar part together. An `N` or a refusal records none of them, and nothing
  launches. The flag acknowledges sidecars too, as the ruling said, so a scripted launch that
  passes it starts the workspace's sidecars. With no prompt to show the section on, that launch
  prints it anyway, command lines in full, so what the flag started is disclosed and reaches
  `launch.log`.
- **What is acknowledged is what was shown**, by construction. The section is rendered from the
  same loaded workspace config that the gate records, in one process, so no edit lands between
  what the user read and what the part holds. That is why the separate command's print-and-ask
  is not needed here. The launch's composition compares each declaration it would start with the
  part, so an edit that lands after the `y` starts nothing, and the next fresh launch shows it.
- **A changed declaration is asked about again.** An edit to any field is a change to the
  workspace config, so the next fresh launch shows it in the diff and marks it changed in the
  section. Its `y` acknowledges the new declaration, and its `N` launches nothing.
- **A new machine asks once.** A sidecar missing from the part, hashed differently, or recorded
  on another machine counts as a change. So a synced or restored approvals directory acknowledges
  nothing, and the first fresh launch on the new machine shows the section alone when nothing
  else changed. On a host with no readable machine id, the `y` approves the config, acknowledges
  no sidecar, and the section says why. A launcher inside a jail records one with no id, as
  EW-D15 allows there.
- **There is no third answer.** The prompt does not offer "the config but not its sidecars". The
  ruling joined into one `y` the two answers that option A kept apart. A user who wants the
  config without one of its sidecars keeps that sidecar off with a veto in
  `yolo-jail.local.jsonc`, or with `yolo sidecar disable <name>` at the host, and the section
  shows it as acknowledged and staying off.
- **The `y` is its turn-on too, and nothing else turns it on** ([EW-D34](#EW-D34)).
  [OQ-EW10](#OQ-EW10) requires every sidecar to be turned on by an explicit act on the machine, and
  a `y` at the host is one. `yolo sidecar enable` at the host only lifts that workspace's `disable`
  for it, and acknowledges nothing. `--all-workspaces` never covers one ([§12.3](#123-the-command)).
  In-jail, the command refuses one and names the prompt, although the sidecar runs inside the jail
  ([§12.9](#129-turning-one-on-from-inside-the-jail)): a repository can instruct its agent through
  its `AGENTS.md`, so an agent's turn-on standing in for the `y` would be the repository starting
  its own sidecar, the case [OQ-EW3](#OQ-EW3) named.
- **A path that deletes the approval record deletes the sidecar part too**, as it deletes the
  scope part (`cleanupCaptureWorkspace` in [`capturehost.go`](../../internal/cli/capturehost.go)
  removes both today). Deleting the part alone fails safe: the next fresh launch asks.

**What holds for every hash-bound sidecar:**

- **It binds the declaration, not the script the declaration runs.** A `cmd` of
  `["npm", "run", "watch"]` or `["sh", "scripts/x.sh"]` pins nothing about what actually runs,
  so a pull that changes `package.json` or the script keeps the acknowledged sidecar running.
  That is accepted on the agent side, where the process has no power the agent's own shell
  lacks, which is the premise [OQ-EW3](#OQ-EW3) was asked on. The sidecar section says so, and so
  does the command for a fetched pack's sidecar.
- **A repository's sidecar never runs at the host notch.** There the agent side and the host
  side are one place ([§1.2](#12-terms)), so a repository's sidecar would be host code a workspace
  declared, which [OQ-EW1](#OQ-EW1) refuses. `yolo host` does not start it and says why
  ([EW-D18](#EW-D18)).

**A fetched pack's sidecar is acknowledged at the command, since no diff shows it.** The gate
reads the workspace config only, and user config, the only scope that selects packs
([`packs.go`](../../internal/config/packs.go)), never prompts
([`config-safety.md`](../reference/config-safety.md#principles), P1). A fetched pack is not the
user's own word ([OQ-EW1](#OQ-EW1)), and an upstream edit reaches every machine that enabled it
with no act on that machine: `yolo pack update` on the desk, the synced `packs.lock.json` on the
laptop. So its record holds the hash of its resolved declaration and the pack's locked commit
(`Commit` in [`lock.go`](../../internal/packsrc/lock.go)), which also covers the pack's own
scripts. `yolo sidecar enable` prints the whole resolved declaration and that commit, asks
`Run this on this machine? [y/N]`, and records the hash of exactly what it printed; with no
terminal it refuses. This is a question inside a command the user chose to run, not a question
at launch, so it is not [OQ-EW5](#OQ-EW5)'s option B ([EW-D23](#EW-D23)). When the declaration or
the commit changes, the sidecar stops and the launch line names the change. The jail's own agent
can also turn on a fetched pack's agent-side sidecar, bound to the same hash and commit
([§12.9](#129-turning-one-on-from-inside-the-jail)).

**The user's own sidecars are recorded by name, not by hash.** That is [OQ-EW1](#OQ-EW1)'s set:
user config, the local pack, and packs the user config selects by path. The user editing their
own config or pack needs no gate (Test 1), and asking again at every edit would be the prompt
fatigue that [`config-safety.md`](../reference/config-safety.md#principles)'s P3 rules out.

### 12.5 What the launch says

Every fresh launch, meaning one that creates the container or starts a keeper at `yolo host` or
macos-user, and every `yolo host` launch with no keeper, prints one line per declared sidecar,
whatever its state. The line is a disclosure, so no flag hides it
([OQ-RO3](../reference/report-tiers.md#why-its-this-way)). A sidecar that is not enabled never
refuses a launch, and, as [OQ-EW5](#OQ-EW5) ruled, the launch never asks about one
([EW-D16](#EW-D16)). The one question is the config-change prompt the launch already runs, whose
sidecar section covers the workspace's own sidecars ([EW-D33](#EW-D33)).
`yolo check` and `yolo sidecar list` report the same states. An attach, or a launch that joins a
keeper, starts none and names the ones running.

| State | What the launch prints |
| :--- | :--- |
| Declared, not enabled here | `sidecar ci-watch (pack matt, agent side): not enabled on this machine; to start it here: yolo sidecar enable ci-watch` |
| Enabled for this workspace | `sidecar ci-watch (pack matt, agent side): enabled on this machine for this workspace since 2026-09-29; pings reach claude immediately` |
| Enabled for every workspace | `sidecar ci-watch (pack matt, agent side): enabled on this machine for every workspace since 2026-09-29; pings reach claude immediately` |
| Record made on another machine | `sidecar ci-watch (pack matt, agent side): the record enabling it was made on another machine; to start it here: yolo sidecar enable ci-watch` |
| Turned on from inside the jail | `sidecar ci-watch (pack matt, agent side): turned on from inside this workspace's jail on 2026-09-30; pings reach claude immediately` ([§12.9](#129-turning-one-on-from-inside-the-jail)) |
| Turned on from inside the jail, not yet running | At an attach or a launch that joins a keeper: `sidecar ci-watch (pack matt, agent side): turned on from inside this jail at 14:05; starts when this jail next boots` |
| A jail turn-on made elsewhere | `sidecar ci-watch (pack matt, agent side): turned on from inside a jail on another machine or for another checkout; not started; to start it here: yolo sidecar enable ci-watch` |
| A jail turn-on a host record outranks | The host record's own line, then `(a turn-on from inside the jail on 2026-09-30 does not override it)` |
| A jail turn-on at `yolo host` | `sidecar ci-watch (pack matt, agent side): turned on only from inside the jail, which does not reach yolo host, where it would run unconfined; to start it here: yolo sidecar enable ci-watch` |
| Declared by the workspace, acknowledged | `sidecar devserver-errors (this workspace's yolo-jail.jsonc, agent side): acknowledged on this machine when you approved its config on 2026-09-29; pings reach claude immediately`. At a launch that passed `--accept-config-changes` it says *"acknowledged by --accept-config-changes at this launch"* |
| Declared by the workspace, on a host with no machine id | `sidecar devserver-errors (this workspace's yolo-jail.jsonc, agent side): not started; this machine has no readable machine id, so approving the config cannot acknowledge it here` |
| A fetched pack's, changed since it was enabled | `sidecar watcher (pack tools, fetched, agent side): its locked commit changed since you enabled it on 2026-09-29; not started; to start the new one: yolo sidecar enable watcher`. A changed field reads the same, naming the field |
| Off in this workspace on this machine | `sidecar ci-watch (pack matt): off in this workspace on this machine (yolo sidecar disable, 2026-09-29)` |
| Off on this machine | `sidecar ci-watch (pack matt): off on this machine (yolo sidecar disable --all-workspaces, 2026-09-29)` |
| Vetoed | `sidecar ci-watch (pack matt): off in this workspace ("enabled": false in yolo-jail.jsonc)` |
| Not at this notch | `sidecar devserver-errors (this workspace's yolo-jail.jsonc): does not run under yolo host, where it would be unconfined` |
| Not on this backend yet | `sidecar ci-watch (pack matt, agent side): does not run on macos-user yet, where each sandbox would run its own; not started` |
| Running under another keeper here | `sidecar ci-watch (pack matt, host side): already running for this workspace under another keeper on this machine (the yolo host keeper, pid 48121), whose master gets its pings; this launch starts none, and this launch's keeper starts it once that one ends` |

The *"pings reach …"* clause names the box's master ([EW-D32](#EW-D32)). At a launch whose box has
no master yet, it is this launch's agent and its tier, as the rows show. At an attach or a launch
that joins a keeper it names the current master, for example *"pings reach the master session,
claude:ab12 (first launched; wake), not this one"*. A sidecar the workspace's own config declares
has no "not acknowledged" line: a fresh launch that finds one new, changed, or not yet
acknowledged here asks about it in the config-change prompt before it gets this far, and an `N`
or a refusal launches nothing ([§12.4](#124-a-sidecar-a-repository-declares),
[EW-D33](#EW-D33)). Where a line names `yolo sidecar enable` for an agent-side sidecar at a
container backend, the jail's own agent may run it too
([§12.9](#129-turning-one-on-from-inside-the-jail)).

### 12.6 Two machines, one project

**yolo does not try to guarantee one watcher per project across machines** ([EW-DIR2](#EW-DIR2)).
What it guarantees is narrower: **a replicator never adds a watcher.** Two machines watch one
project only if someone enabled it on both, and every launch on each machine says so.

Why a cross-machine lock is not on offer:

- yolo's state is per machine by design ([§12.2](#122-where-the-record-lives-and-why-there)), so
  neither machine can see the other's record.
- Synced dotfiles are eventually consistent, so they cannot serve as a lock, and a lock written
  there would make the replicator part of the gate.
- GitHub is the one store both machines see consistently. The one atomic primitive there that a
  watcher could use is creating a ref, where a second create of the same name is refused. That needs a write token beside
  the watcher, which today holds a read-only one in the agent-readable `.env`
  ([§2.1](#21-the-ci-watcher-and-what-it-depends-on)), and it leaves refs in the repository.
  Commit statuses have no compare-and-swap, and check runs are *"only available to GitHub
  Apps"* ([GitHub docs](https://docs.github.com/rest/checks/runs)). This is the guarantee
  EW-DIR2 declined.

What happens when both machines are enabled anyway:

- **A ping is a fact**, so a duplicate costs attention, not correctness.
- **The agents' reactions are what collide.** An unforced push is already a compare-and-swap on
  the branch, so the second agent's fix is refused as non-fast-forward, although both did the
  work. Comments and issues do duplicate. The harm is duplicated work and noise, not corrupted
  shared state, as long as agents do not force-push.
- **A watcher can narrow itself without shared state**, with an authorship filter: it reports
  only runs whose commit this checkout pushed. Git records that locally. In this repository's
  `git reflog show refs/remotes/origin/main`, this checkout's own pushes read `update by push`,
  and commits pushed from anywhere else arrive as `fetch … fast-forward` or `pull: fast-forward`
  (MEASURED, 2026-09-29). Runs of commits that no checkout pushed, such as the merge button's,
  bots' and other people's, then reach no machine unless one opts in. The maintainer ruled that
  his watcher does not ([OQ-EW8](#OQ-EW8), B); such a filter would live in his pack, because core does not know
  what a CI run is ([EW-P1](#EW-P1)).
- **A host-side watcher reads git's files as text.** It reads `.git/config` and the reflog under
  `.git/logs/` and never runs host `git` in the workspace. It opens only regular files, follows
  no link and ignores `include` directives, so the agent cannot point it at a host file of its
  choosing or hang it on a planted FIFO ([EW-D21](#EW-D21)).

### 12.7 One machine, several launches or sessions

Per-machine enablement settles which machine runs a watcher. The same duplicate can still arise
inside one machine, and there a lock is cheap, because everything is on one filesystem.

- **Several launches of one workspace at one notch.** They share one keeper
  ([`jail-lifetime-last-session-wins.md` §9.9.3](jail-lifetime-last-session-wins.md#993-one-keeper-per-workspace-per-notch)),
  and the keeper owns each sidecar it starts from the first session to the last, so nothing is
  passed on when a session ends ([EW-D25](#EW-D25)). A container jail already ran one set per
  container, and an attach starts none ([EW-D10](#EW-D10)). At `yolo host` and macos-user a second
  terminal now joins the workspace's keeper there and shares its sidecars and its box, where
  before this each terminal would have started its own set and box, and where [EW-D19](#EW-D19)
  then passed each sidecar from launch to launch by a kernel lock, which is the handoff
  [JL-D14](jail-lifetime-last-session-wins.md#JL-D14) rejected.
- **Several notches of one workspace on one machine.** A container jail and a `yolo host`
  terminal of one workspace, or macos-user and `yolo host` on a Mac, are two keys with two
  keepers, and nothing else stops each from running `ci-watch`. So one instance per workspace per
  machine is still kept by a lock, now taken only by keepers: a keeper holds a kernel lock per
  sidecar it starts, under `~/.local/share/yolo-jail/locks/`, beside the workspace launch lock
  (`launchLockPath` in [`flock.go`](../../internal/cli/run/flock.go)). A keeper that finds one
  held starts none, and its launch names the keeper holding it
  ([§12.5](#125-what-the-launch-says)).
  - **A keeper that could start the sidecar later waits on that lock**, and starts its own
    instance once the other key ends: every sidecar at `yolo host`, and every host-side one
    elsewhere. An agent-side sidecar in a container is not one of them, since its supervisor's
    set is fixed at boot. This is a start by a second owner after the first has gone, not a
    handoff: nothing running moves, and each keeper holds what it started for its own key's life.
  - **A takeover checks the gate again**: it re-reads the config vetoes and this machine's records,
    and, where the record holds a hash, compares the declaration it would start with it, so a
    `yolo sidecar disable`, a new veto or a changed declaration since the keeper began starts
    nothing, and the keeper's log and its sessions' next lines say why.
  - The two keys keep separate boxes, so the pings reach the master of whichever key runs the
    sidecar. A box is never shared across notches
    ([JL-D37](jail-lifetime-last-session-wins.md#JL-D37)), so a claim cannot take them across;
    whether it should is [OQ-EW12](#OQ-EW12).
- **Several agent sessions in one box.** Two agents attached to one jail, or two terminals of one
  keeper at `yolo host`, share one box. A ping wakes one of them, the master
  ([§3.6](#36-which-session-a-ping-wakes-the-master)), as [EW-DIR3](#EW-DIR3) directed on
  [OQ-EW9](#OQ-EW9). The old leaning of [OQ-EW2](#OQ-EW2), *"a session that does not care ignores
  one line"*, answered a smaller worry than two sessions that both care and share one checkout.

### 12.8 What is on by default (revisited 2026-09-29)

Directing [OQ-EW9](#OQ-EW9), the maintainer said: *"this makes me question entirely. Even trying
to enable this by default, because of exactly this"*, "this" being several agent sessions in one
jail with no way to say which one a ping is for. After the rulings of 2026-09-29, "by default"
means this:

| What | What turns it on today | Ruled by |
| :--- | :--- | :--- |
| A sidecar the user's config or a pack declares | `yolo sidecar enable <name>` at the host, in that workspace, on that machine, or `--all-workspaces` there; for an agent-side one, the same command run by the jail's own agent at a container backend ([§12.9](#129-turning-one-on-from-inside-the-jail)); nothing else | [OQ-EW5](#OQ-EW5), [OQ-EW6](#OQ-EW6); every such sidecar, by [OQ-EW10](#OQ-EW10), whose ruling also lets the jail's own agent turn on an agent-side one |
| A sidecar the workspace's own config declares | approving the config-change diff whose sidecar section shows it, on that machine, or `--accept-config-changes`; nothing else ([§12.4](#124-a-sidecar-a-repository-declares)) | [OQ-EW7](#OQ-EW7) (B) |
| A sidecar the user's own word marks ungated | nothing: no declaration can mark itself ungated | [OQ-EW10](#OQ-EW10) (A) |
| The ping box, the deliverers, `yolo notify` and the session list | nothing: the box exists in every launch ([§3.1](#31-declaring-a-sidecar)), each agent pack's deliverer is rendered into every session ([§3.4](#34-deliverers-per-agent)), and every agent session registers ([§3.6](#36-which-session-a-ping-wakes-the-master)) | not ruled |

What follows from it:

- **No sidecar is on by default.** The nearest is a repository's own, which one y turns on: the
  one a fresh workspace with a non-empty config is asked at its first launch
  ([OQ-S3](../reference/config-safety.md#oq-s3)). That is [OQ-EW7](#OQ-EW7)'s ruling, B, carried
  out in [§12.4](#124-a-sidecar-a-repository-declares): a sidecar section in the diff, whose
  approval records the hash of what it showed ([EW-D33](#EW-D33)).
- **What is still on by default is the doorbell itself**, and that is what the maintainer's worry
  was about. The master answers the worry for every source of pings at once: a ping from a
  sidecar, a script, an agent's own background task or another session's agent reaches one
  session per box ([§3.6](#36-which-session-a-ping-wakes-the-master)).
- **What the master does not change.** Under the design as written, every agent session registers
  and arms a deliverer in every launch, even where nothing will ever ring, and any process in a
  jail can wake its master. At `yolo host` the box alone would also give every launch a keeper
  and keep it resident ([`jail-lifetime-last-session-wins.md` §9.9.8](jail-lifetime-last-session-wins.md#998-a-launch-with-nothing-long-lived)).
- **So one choice is left, and it is the maintainer's**: whether the doorbell is on where no
  sidecar is. That is [OQ-EW11](#OQ-EW11). Nothing else still on by default is something the
  master requirement bears on.

### 12.9 Turning one on from inside the jail

[OQ-EW10](#OQ-EW10) was ruled A, with one change to who may turn a sidecar on: *"I think they
should all require explicitly turning on. But if we're talking about something running inside
the jail, then the agent inside the jail should be able to be the one that turns it on."* So the
jail's own agent may turn on an agent-side sidecar, which revises [EW-D17](#EW-D17)'s in-jail
refusal, and a host-side sidecar keeps [OQ-EW5](#OQ-EW5)'s host-only act. This section designs it
([EW-D35](#EW-D35) to [EW-D39](#EW-D39)). It uses three terms coined here and defined in
[§1.2](#12-terms): a **jail turn-on**, the in-jail act and its record; the **turn-on tag**, which
binds that record to this machine and this workspace; and the **sidecar view**, the file a launch
hands the jail so it can see what is declared.

| Question | Answer | Decision |
| :--- | :--- | :--- |
| What the jail may turn on | an agent-side sidecar from user config, the local pack or a selected pack, for the jail's own workspace; never a host-side one, and never one the workspace's own config declares | [EW-D36](#EW-D36) |
| Where it is recorded | an entry in a file in the jail's own per-workspace home, which the host reads and never copies into its own store | [EW-D35](#EW-D35) |
| How it still means this machine | the entry carries a turn-on tag, derived from the machine id and the workspace, which only a launch on this machine can hand the jail | [EW-D35](#EW-D35) |
| A copied or cloned file | a copy on another machine, at another path or in a clone starts nothing, and the launch says why | [EW-D35](#EW-D35) |
| How it ranks against the host's records | below every one of them; the jail can withdraw only its own | [EW-D37](#EW-D37) |
| When it takes effect | at the next launch that creates the container, whatever the answer to [OQ-EW13](#OQ-EW13) | [EW-D38](#EW-D38) |
| Where it counts | at a container backend only, never at `yolo host` | [EW-D38](#EW-D38) |
| What the launch, `list` and the briefing say | the launch names it as the jail's, `list` works in-jail, and the briefing says to use it only at the user's request | [EW-D39](#EW-D39) |

**The in-jail command** ([EW-D36](#EW-D36)) has the host's verbs ([§12.3](#123-the-command)), run
inside the jail for the jail's own workspace:

```console
$ yolo sidecar enable ci-watch     # turn on an agent-side sidecar for this workspace, on this machine
$ yolo sidecar disable ci-watch    # withdraw this jail's own turn-on
$ yolo sidecar list                # what this jail booted with, and what it has turned on since
```

- **It resolves the name through the sidecar view**, and refuses one the view does not list,
  listing those it does. The jail cannot see the host's records, so the view is how it knows each
  sidecar's source, side and state.
- **It refuses a host-side sidecar** and names the host command, as it always did
  ([OQ-EW5](#OQ-EW5)). The ruling's words are *"something running inside the jail"*.
- **It refuses one the workspace's own config declares** and names the config-change prompt,
  whose `y` is that sidecar's only turn-on ([EW-D34](#EW-D34)).
- **Where a host record governs, it records nothing.** A sidecar the host turned on, for this
  workspace or every workspace, is on already, and one it turned off stays off, since every host
  record outranks a jail turn-on ([EW-D37](#EW-D37)). The command names the record that governs,
  as the sidecar view shows it at this jail's boot, and for an off record names the host command
  that lifts it.
- **It asks nothing.** An agent's tool shell has no terminal, and a confirmation would stop
  nothing the agent cannot already do: it can run the same command as its own background task
  ([Test 1](../reference/gate-placement-principle.md#test-1--the-authority-test-could-this-actor-already-do-it)),
  as [EW-D29](#EW-D29) argued for a claim. What a jail turn-on adds is supervision, restarts,
  and a start at each later boot of the jail.
- **It prints what it recorded and when it starts**, for example *"ci-watch (pack matt, agent
  side): turned on from inside this jail, for this workspace on this machine; it starts when this
  jail next boots"*.
- **`--all-workspaces` keeps [EW-D17](#EW-D17)'s nested meaning.** In-jail it writes the jail's
  own store, which covers the workspaces nested in this jail and never this jail's own, which
  only the host's machine-wide record covers. The command says so.

**Where the record lives, and why there** ([EW-D35](#EW-D35)). A jail turn-on is an entry in one
file under the jail's `~/.local/state/`. That directory is per workspace on both container
backends: podman binds `<workspace>/.yolo/home/local` at `~/.local`, and Apple Container binds the
whole overlay as the home (`WorkspaceHomeState` in [`paths.go`](../../internal/paths/paths.go);
[`jail-home.md`](../reference/jail-home.md)). An entry holds the sidecar's name and source as the
sidecar view names them, the time, the turn-on tag, and, for a fetched pack's sidecar, the
declaration's hash and the pack's locked commit as the view lists them. The file's name is the
implementer's. The places it could have gone and did not:

| Where | Why not |
| :--- | :--- |
| The host's record store, `~/.local/share/yolo-jail/approvals/` | No jail mounts it, and it must stay that way ([EW-D14](#EW-D14)): a jail that could write there could write the approval record beside it |
| A host record, written from the jail's request at the next launch | The host would be adopting an unbound request, so a replicator that carried the file to a second machine before the first machine's next launch would have it adopted there. It would also give the host's store a second writer beside `yolo sidecar enable` and `disable` |
| `~/.local/share/yolo-jail/cache/` | Every jail writes it ([`storage-and-config.md`](../reference/storage-and-config.md)), so one jail could turn on another workspace's sidecar |
| The ping box | It is removed when its keeper ends ([EW-D24](#EW-D24)) |
| The jail's own yolo store, `~/.local/share/yolo-jail/` inside the jail | That is the in-jail launcher's record store for the workspaces nested in this jail ([EW-D17](#EW-D17)), a different reader that trusts it as a host's |

**How it still means this machine** ([EW-D35](#EW-D35)). Nothing the jail writes by itself can say
which machine it is on, because the jail can read no machine id (MEASURED: this jail has no
`/etc/machine-id`; [EW-D15](#EW-D15)). So each fresh launch hands the jail a turn-on tag in the
sidecar view, and the in-jail command copies it into the entry. The tag is a keyed hash, under a
fixed application-specific key, of [EW-D15](#EW-D15)'s machine id together with the workspace's
container name, and never the id itself. That is the shape
[machine-id(5)](https://man7.org/linux/man-pages/man5/machine-id.5.html) asks for: the id *"must
not be exposed in untrusted environments"*, and an application that needs a stable value tied to
the machine should hash it *"with a cryptographic, keyed hash function, using a fixed,
application-specific key"*. The exact construction is the implementer's. At the next launch the
host computes the tag again and honors an entry only when the two match. The tag is no secret
from the agent, which may make the turn-on anyway: its one job is to make the file mean this
machine and this checkout wherever the file is carried. It travels in a file rather than an
environment variable, because an agent may run its tool shells with a scrubbed environment, which
is why the image bakes `LD_LIBRARY_PATH` into its own environment ([`AGENTS.md`](../../AGENTS.md)).

**A copied or cloned file.** An entry that reaches somewhere it was not made:

| How it got there | What the host does |
| :--- | :--- |
| A replicator synced the workspace, or the whole home, to another machine | That machine's tag differs, so nothing starts, and the launch says the turn-on was made on another machine or for another checkout |
| The workspace was copied to another path on this machine | The copy's container name differs (`FromWorkspace` in [`naming.go`](../../internal/runtime/naming.go)), so its tag does too, with the same result |
| The repository was cloned | A clone carries none, since `.yolo/` ignores itself (`EnsureWorkspaceStateDir` in [`paths.go`](../../internal/paths/paths.go) writes a `.gitignore` there whose one rule is a bare `*`). A repository that commits one anyway cannot compute a machine's tag |
| This machine's backup was restored at the same path | It is honored, as a restored host record is |
| The workspace was moved | Its container name changes, so the entry is not honored, as a host record is not ([§12.3](#123-the-command)) |

On a host with no readable machine id there is no tag. The launch hands the jail none, the
in-jail command refuses and says why, and no jail turn-on is honored there, which is
[EW-D15](#EW-D15)'s rule for records. A launcher that itself runs inside a jail, for a nested jail,
has no id either, and as EW-D15 allows there it keys the tag with none, so the tag binds the
workspace alone, the outer jail being the blast radius.

**How the host reads it.** The file is jail-writable state, so the host reads it by
[`jail-home.md`](../reference/jail-home.md#host-code-in-jail-writable-state)'s rule: beneath an
`os.Root` on `.yolo/home` (`paths.OpenWorkspaceStateSubdir`), with `paths.ReadRegularFileBeneath`,
which reads no link and cannot hang on a FIFO, capped in size and parsed as data. An unreadable or
malformed file reads as no jail turn-on, which is off. The host reads it at the launch that creates
the container, which composes the supervisor's daemon list, and for `yolo sidecar list` and
`yolo check` at the host. A `yolo host` or macos-user launch reads it only to say that it does not
count there. No host command writes it.

**How it ranks** ([EW-D37](#EW-D37)). For a sidecar from user config or a pack, first match wins:

1. a config veto;
2. this workspace's host record, on or off;
3. the machine-wide host record, on or off;
4. a jail turn-on honored at this launch;
5. otherwise off.

So a jail turn-on counts only where the host has said nothing about that sidecar. That keeps two
properties this design already relies on: a `yolo sidecar disable` record is the off switch the
agent cannot touch ([§12.1](#121-declaring-is-not-enabling)), and a machine-wide off still means a
machine that never watches, which a turn-on from any jail on it would otherwise undo. The ruling
lets the agent turn a sidecar on; it does not say the agent overrules what the user said at the
host. A fixed order also needs no clock to settle which of two writers spoke last.

In-jail `yolo sidecar disable` removes the jail's own entry. Where a host record, the config
approval or a veto governs, it writes nothing, exits non-zero, and names what governs, the veto
the agent may write itself in `yolo-jail.local.jsonc` (which the next fresh launch's config diff
shows), and the host command.

**When it takes effect, and where** ([EW-D38](#EW-D38)). At the next launch that creates the
container. The supervisor's whole input is `YOLO_JAIL_DAEMONS`, read once when it starts (`Main`
in [`supervisorcmd.go`](../../internal/supervisor/supervisorcmd.go)), so a running jail cannot
gain an agent-side sidecar. Each option of [OQ-EW13](#OQ-EW13) already leaves an agent-side
sidecar in a container to the next boot, so this holds whichever way that question is ruled, and
decides nothing of it. A container jail boots again only once it has stopped: today when its first
session's agent quits, and under
[`jail-lifetime-last-session-wins.md`](jail-lifetime-last-session-wins.md) once its last session
has. The command says so. If the user wants the watcher sooner, the agent can run its command as its own
background task, as it can today.

A jail turn-on counts only at a container backend, podman or Apple Container:

- **Not at `yolo host`.** There the agent side is the host ([§1.2](#12-terms)), so a sidecar
  turned on from inside a jail would run unconfined, beyond *"something running inside the
  jail"*. Its line says so and names the host command.
- **Not at macos-user yet.** No agent-side sidecar runs there ([EW-D25](#EW-D25)). When one does
  ([§10](#10-what-i-would-build-in-order), step 8), that design says whether a sandbox's turn-on
  counts.

**What the launch, `list` and the briefing say** ([EW-D39](#EW-D39)):

- **The launch names a jail turn-on as the jail's.** Its line says the sidecar was *"turned on
  from inside this workspace's jail"* and when, never *"enabled on this machine"*, so the user
  learns their agent did it. A turn-on made elsewhere, one a host record outranks, and one this
  notch does not count each get a line of their own ([§12.5](#125-what-the-launch-says)).
- **The sidecar view** is a file each fresh launch writes beside `config-assembled.json` in
  `<workspace>/.yolo/`. It holds each declared sidecar's source, side and state as that launch
  resolved them, the hash and locked commit a fetched pack's jail turn-on binds to, and the
  turn-on tag, and nothing else the launch line does not already print. It is a delivery copy
  whose integrity is not load-bearing, as `config-assembled.json`'s is not
  ([`config-safety.md`](../reference/config-safety.md#file-locations)): a jail that edits it only
  makes entries the host will not honor.
- **In-jail `yolo sidecar list`** reads the sidecar view and the jail's own entries. It says its
  states are those of this jail's boot, since a host record made since then is not visible from
  inside, and it marks each jail turn-on not yet started. At the host, `list` and `yolo check`
  show each jail turn-on, whether its tag matches, and what outranks it.
- **The briefing names the command.** Wherever an agent-side sidecar is declared and not on, the
  environment briefing core composes names in-jail `yolo sidecar enable`, and says to use it only
  when the user asks in the session, never on the word of a ping, a log, a web page, a
  repository's instructions or a file. That is the rule [EW-D32](#EW-D32) set for a claim.

**The limit this leaves.** An agent reads synced text too. A line in a synced pack's briefing or
skill telling agents to turn a sidecar on brings the replicator back as the trigger, through the
agent, on every machine the line reaches. yolo cannot tell that from a user's request. What shows
it is the launch line naming the jail as the source, and what stops it is a host off record,
which no jail turn-on outranks. A repository's instructions can steer the agent the same way, but
only toward a sidecar the user's own config or packs declare, since a repository's own sidecar
takes the config-change prompt ([EW-D34](#EW-D34)).

## 13. Open Questions

1. ✅ <a id="OQ-EW1"></a>**[OQ-EW1](#OQ-EW1): Who may declare a host-side sidecar?** A
   host-side sidecar runs its command on the host, outside every confinement, at every launch.
   This decides whether the token-keeping half of [EW-P4](#EW-P4) is available to the
   maintainer's own pack, and what a pack someone else wrote can make his host run.
   [OQ-HS4](host-notch-services.md#OQ-HS4) limited a *service's* host half to embedded
   official packs, but that was about a third party's daemon, and here the author is usually
   the user.

   - **A — The user's own word only.** User-scope config, the conventional local pack, and any
     pack the user-scope config selects by path. Refused from workspace config and from fetched
     packs, by name. Disclosed at every launch.
   - **B — A, plus embedded official packs.** Nothing shipped needs one today, so this only
     matters later.
   - **C — Nobody, yet.** Agent side only in the first release; the token stays where it is.

   _Leaning:_ **A.** A command in his own config is his, as much as his shell's rc file is. A
   path-selected pack is one he named. A fetched pack stays refused until someone rules on trust
   for third-party host code, as [OQ-HS4](host-notch-services.md#OQ-HS4) left it. The boundary
   is disclosure, which [OQ-TP9](trust-paths.md#decision-ledger) chose over approval.

   <!-- vantage: oq id=OQ-EW1 -->

   **Answer:**
   > **Ruled 2026-09-29, as leaned: A.** Only the user's own word may declare a host-side
   > sidecar: user-scope config, the local pack, and packs the user's config selects by path.
   > Refused from workspace config and from fetched packs; disclosed at every launch. (Relayed
   > from the conversational walkthrough: *"Yes A."*)

2. ✅ <a id="OQ-EW2"></a>**[OQ-EW2](#OQ-EW2): Does a ping reach every agent session in the jail,
   or one?** **Superseded 2026-09-29 by [OQ-EW8](#OQ-EW8) (two machines) and [OQ-EW9](#OQ-EW9)
   (sessions in one jail).** The text below is kept as asked. An attach can put claude and pi in
   one container, both with deliverers. This decides whether a CI result interrupts both, and
   whether a sidecar ever has to name a target.

   - **A — Every session.** Each reader gets every ping. Simple, and a session that does not care
     ignores one line.
   - **B — The first reader to take it.** One session handles each event. Which one is a race.
   - **C — A target the sidecar names**, by agent. Precise, but a sidecar has to know which
     agents run, which [EW-P1](#EW-P1) set out to avoid.

   _Leaning:_ **A.** The box cannot know which session cares, and the other two either race or
   make the sidecar know about agents. Two agents in one jail is rare, and C can be added later as
   an optional `--to` without changing A's default.

   **Directed 2026-09-29, back to the drawing board.** The maintainer, answering this question:
   *"this is a much deeper question i think and i don't think we can actually release this feature
   until we really nail this down ... normally i put these settings in my dot files i share them
   across computers ... i may have two agents across different computers ... waiting ... on the same
   project ... a distributed set of ci watchers and agents that are both going to try to act and
   that's not going to be good for anyone ... this really just needs to be a very intentional act
   to enable this ci watcher somewhere ... it can't be driven even from the user settings directly,
   because then when my.file syncing setup, I'm going to get it in two places without even thinking
   about it."* The feature is not released until enablement is an intentional, per-machine act that
   a synced dotfile cannot trigger, and the cross-machine duplicate is designed for. A redesign is
   in progress; this question is restated with it.

   **Directed again 2026-09-29.** The maintainer: *"I understand that we can't guarantee a single
   watcher across machines. We're not going to do that. But I do like your clarification that you
   can define the shape of a watcher, but it needs explicit permission to activate it. That is
   definitely the right route to go."* Recorded as [EW-DIR2](#EW-DIR2).

   <!-- vantage: oq id=OQ-EW2 -->

   **Answer:**
   > **Superseded 2026-09-29, not ruled.** The redesign is
   > [§12](#12-enabling-a-sidecar-redesign-2026-09-29). What this question asked is restated as
   > [OQ-EW8](#OQ-EW8) (what, if anything, stops two machines acting on one event) and
   > [OQ-EW9](#OQ-EW9) (which sessions in one jail a ping wakes).

3. ✅ <a id="OQ-EW3"></a>**[OQ-EW3](#OQ-EW3): May a repository's own `yolo-jail.jsonc` declare
   an agent-side sidecar?** A cloned repo's config could then start a process at launch, and ring
   the agent with whatever it likes. This decides whether a project can ship its own watcher (a
   dev server's error tail, say) to everyone who opens it.

   - **A — Yes, where the agent is confined; refused at the host notch.** In a jail the process has
     no power the agent's own shell lacks, and the repo can already instruct the agent through its
     `AGENTS.md`. At the host there is no confinement, so a repo would gain host code execution
     at launch.
   - **B — Never.** Sidecars are user-scope only, like the host-reaching keys
     [OQ-AS3](../research/agent-safehouse.md#OQ-AS3) asks about.

   _Leaning:_ **A**, with a host-side declaration refused from workspace scope under every answer
   to [OQ-EW1](#OQ-EW1).

   <!-- vantage: oq id=OQ-EW3 -->

   **Answer:**
   > **Ruled 2026-09-29, none of the options as written: yes, but gated.** The maintainer: *"i
   > think the answer is yes but again i'm not sure it should even be default on ... It would be
   > weird to clone somebody else's repository and then start this up and get some background
   > watcher running. I think we need an explicit acknowledgement step before it's default on
   > anywhere. Yes, it starts it everywhere or whatever, it distributes it everywhere when you
   > put it in the config, but that config needs to allow a gated set."* A repository's own
   > config may declare an agent-side sidecar, but it does not run until the user has explicitly
   > acknowledged it; the gated-configuration model is being designed with [OQ-EW2](#OQ-EW2)'s
   > redesign.

   Designed in [§12.4](#124-a-sidecar-a-repository-declares); how the acknowledgement is given is
   [OQ-EW7](#OQ-EW7).

4. ✅ <a id="OQ-EW4"></a>**[OQ-EW4](#OQ-EW4): Is a pack's sidecar on when its pack is
   selected?** **Ruled 2026-09-29, and its mechanics superseded by [OQ-EW5](#OQ-EW5),
   [OQ-EW6](#OQ-EW6) and [OQ-EW7](#OQ-EW7).** The text below is kept as asked. Loopholes that
   ship in packs are off until enabled
   ([`packs/hello-daemon`](../../packs/hello-daemon/README.md) states the precedent). This
   decides whether the maintainer's watcher starts in every workspace the moment his pack is
   selected, or only where he turns it on.

   - **A — On when selected.** `"sidecars": {"<name>": {"enabled": false}}` turns one off.
     Selecting a pack whose point is a sidecar is the act of wanting it.
   - **B — Off until enabled**, as loopholes are. One more line per workspace, and a declared
     sidecar that silently does nothing until someone finds the switch.

   _Leaning:_ **A.** A loophole is off because it crosses the boundary. An agent-side sidecar
   crosses nothing, and a host-side one is already gated by [OQ-EW1](#OQ-EW1). The launch
   discloses every sidecar it starts, so an unwanted one is visible at once.

   <!-- vantage: oq id=OQ-EW4 -->

   **Answer:**
   > **Ruled 2026-09-29: A, inside a gate.** The maintainer: *"The answer is yes. That was when I
   > mentioned, yes, it starts everywhere you specify the config needs to allow a gated set."*

   _Carried out as_ (the doc's reading, not his words): a declared sidecar needs no second config
   switch per workspace, and it starts wherever it is configured once the gate passes. The
   per-machine enablement record that gate became came later, from [EW-DIR1](#EW-DIR1), and the
   acknowledgement from [OQ-EW3](#OQ-EW3). Designed in [§12.1](#121-declaring-is-not-enabling)
   and [EW-D13](#EW-D13). Which sidecars the gate covers was ruled in [OQ-EW10](#OQ-EW10) (every one),
   and its other parts in [OQ-EW5](#OQ-EW5), [OQ-EW6](#OQ-EW6) and [OQ-EW7](#OQ-EW7).

5. ✅ <a id="OQ-EW5"></a>**[OQ-EW5](#OQ-EW5): What act turns a sidecar on for a machine: a
   command, or a question at launch?** This decides what the maintainer does on each machine,
   and whether a synced declaration can ever reach a running watcher through a reflexive answer.

   _Setup:_ Matt keeps his yolo user config and his pack in a dotfiles tree that syncs between
   his desk and his laptop. On the desk he adds `ci-watch` to the pack. The laptop pulls it
   overnight. The next morning he runs `yolo -- claude` in yolo-jail on the laptop. Under
   [§12](#12-enabling-a-sidecar-redesign-2026-09-29) the watcher does not start there, because
   nothing on the laptop has turned it on, and the launch says so. The question is what that act
   on a machine is: a command he types, or a question the launch asks him.

   - **A — A command at the host.** `yolo sidecar enable ci-watch` writes the enablement record.
     Until then each launch prints `sidecar ci-watch (pack matt, agent side): not enabled on this
     machine; to start it here: yolo sidecar enable ci-watch` and starts nothing. No launch ever
     asks.
   - **B — The launch asks, once per machine.** The first launch that finds `ci-watch` declared
     and not enabled asks `Start ci-watch on this machine for this workspace? [y/N]` and records
     either answer. A launch with no terminal starts nothing and asks next time. One step fewer,
     but the question appears on every machine the dotfiles reach, at the first launch after a
     sync, which is where a reflexive y lands.
   - **C — A config file that stays on the machine**, such as the `overrides.jsonc` his config
     includes only if found. Nothing new to build, but yolo cannot tell a file that syncs from
     one that does not, and it is user settings, which [EW-DIR1](#EW-DIR1)'s words rule out.

   _Leaning:_ **A.** It is the shape of direnv's `direnv allow` and launchd's `launchctl
   disable`: a verb that writes state outside the synced tree, and a launch that names the verb
   instead of asking. What makes a watcher safe to run here is knowing this is the machine that
   should watch, and a prompt proves only that someone is at a terminal
   ([§12.2](#122-where-the-record-lives-and-why-there)). B is the fallback if a command is too
   much friction. The ledger rows that assume A, [EW-D14](#EW-D14)'s writer,
   [EW-D16](#EW-D16)'s no-prompt rule and [EW-D17](#EW-D17), change under B.

   <!-- vantage: oq id=OQ-EW5 -->

   **Answer:**
   > **Ruled 2026-09-29: A, with the command's scope narrowed.** The maintainer: *"yes, I think
   > this should be a command you run on the host. I guess you can, in the special case, you can
   > name the jail path that you're talking about, but otherwise this is a command that must be
   > run on the host, but also must be run in a workspace … you would have to do this on a per
   > repository basis on that machine, and then when you start up, it's going to fire it up."*
   > `yolo sidecar enable <name>` runs at the host, in the workspace it enables (its cwd), or
   > names that workspace's path explicitly as the one exception; it writes the machine-local
   > record, and the next launch of that workspace starts the sidecar. The launch never asks.

6. ✅ <a id="OQ-EW6"></a>**[OQ-EW6](#OQ-EW6): Does turning a sidecar on cover one workspace, or
   every workspace on the machine?** This decides how many commands each machine takes, and how
   much one mistaken command duplicates.

   _Setup:_ Matt wants his desk to watch CI for all 12 of his GitHub repositories. On the laptop
   he wants only his dotfiles repository watched, although he sometimes works on yolo-jail there
   too. Both machines have the same pack, so both see `ci-watch` declared in every workspace.

   **How each option sits with [OQ-EW4](#OQ-EW4)'s ruling.** He ruled there that a selected
   sidecar needs no second switch per workspace, and rejected *"one more line per workspace"*.
   B keeps that ruling, and so does A's `--all-workspaces`. A's default and C bring a
   per-workspace act back, and D a per-launch one, so choosing any of them narrows what he
   ruled.

   - **A — One workspace by default, with a machine-wide form.** `yolo sidecar enable ci-watch`
     in a workspace covers that workspace on this machine. `--all-workspaces` covers every
     workspace here that declares it, and `yolo sidecar disable ci-watch` in one workspace turns
     it off there even under the machine-wide form. The desk takes one `--all-workspaces`, and
     the laptop one command in the dotfiles checkout. A moved workspace loses its own record and
     says so; a re-clone at the same path keeps it, since the record is keyed on the path (a
     repository's own sidecar is still held back by its declaration hash).
   - **B — The whole machine only.** One command covers every workspace on the machine. The
     laptop case then needs an `"enabled": false` veto in each workspace that should not be
     watched there, and the only veto that stays on the laptop is each checkout's untracked
     `yolo-jail.local.jsonc`, since the committed file travels with the repository to the desk.
     That file is one the agent can edit or delete from inside the jail
     ([§12.1](#121-declaring-is-not-enabling)), so under B every laptop-only off switch is one
     the agent can remove, with only the config-change prompt's y in the way.
   - **C — One workspace only.** Twelve commands on the desk, and one more for every repository
     he clones.
   - **D — One launch.** `yolo --sidecar ci-watch -- claude` starts it for that launch and records
     nothing. Nothing to forget to turn off, but a launch without the flag has no watcher, which
     loses the *"just starts up"* of [§1.1](#11-why-now).

   _Leaning:_ **A**, and it narrows [OQ-EW4](#OQ-EW4)'s ruling on purpose rather than fitting
   it. The duplicate [EW-DIR1](#EW-DIR1) raised is two machines acting on one project, so the
   default act should be no wider than one project, even though that brings back one act per
   workspace, which [OQ-EW4](#OQ-EW4) ruled out for a config switch. "The desk is my watch
   machine" is also a real wish, and `--all-workspaces` says it and keeps that ruling whole:
   after it, the sidecar starts wherever it is configured, with no further step per workspace.
   Both off switches A adds, `disable` for one workspace and `disable --all-workspaces` for the
   machine, are host records the agent cannot touch.

   <!-- vantage: oq id=OQ-EW6 -->

   **Answer:**
   > **Ruled 2026-09-29, as leaned: A.** One workspace by default; a whole-machine option
   > (`--all-workspaces`) is kept for users who want it, and a per-workspace disable overrides
   > it. The maintainer: *"Let's default to just one workspace but give whole machine as an
   > option. Who knows, maybe somebody will want it."*

7. ✅ <a id="OQ-EW7"></a>**[OQ-EW7](#OQ-EW7): For a repository's own sidecar, is acknowledging it
   the same command, or part of the config-change prompt?** This decides whether "launch with
   this config" and "run this background process here" are one answer or two.

   _Setup:_ Matt clones a colleague's repository whose committed `yolo-jail.jsonc` declares
   `devserver-errors`, an agent-side sidecar that tails the dev server's log and pings on each
   error. Its first launch shows the config diff and asks y/N, as every fresh workspace with a
   non-empty config does ([OQ-S3](../reference/config-safety.md#oq-s3)). The question is whether
   that y also acknowledges the sidecar.

   - **A — The same command as every other sidecar.** Answering y launches the jail without the
     sidecar, and a line shows its `cmd` and names `yolo sidecar enable devserver-errors`. The
     command prints the whole declaration and the file it came from, asks y/N, and records the
     hash of exactly what it printed, so a later change to any field stops it being started,
     with a line saying why. The hash covers the declaration, not the script it runs: a pull
     that edits `scripts/x.sh` under an unchanged `cmd` keeps it running
     ([§12.4](#124-a-sidecar-a-repository-declares)). `--accept-config-changes` never starts a
     sidecar.
   - **B — The config prompt is the acknowledgement.** The diff shows sidecars in a section of
     their own, recorded as another part of the approval record in [BB-D30](boundary-broker.md#BB-D30)'s
     shape, and a y starts them. One prompt instead of a prompt and a command, but a script or a
     CI launch passing `--accept-config-changes` then starts a stranger's background process that
     nobody read.

   _Leaning:_ **A.** One act covers every source, and the two answers stay separate. It is the
   leaning [`workspace-config-trust.md`](workspace-config-trust.md#OQ-WT5) took for a similar
   grant: refuse and name the verb, and `--accept-config-changes` never grants it.

   <!-- vantage: oq id=OQ-EW7 -->

   **Answer:**
   > **Ruled 2026-09-29: B.** (Relayed: *"71 B."*) A repository's own sidecars get their own
   > labeled section in the config-change diff, and approving that diff acknowledges them: the
   > "y" starts them, with no separate `yolo sidecar enable`. The acknowledgement is host-side
   > and per machine, like every config approval, so a synced dotfile still cannot start one. As
   > the option stated, `--accept-config-changes` (the scripted form of that "y") acknowledges
   > them too; the section names each sidecar's command line so the prompt shows what is being
   > started.

   **Carried out 2026-09-30** in [§12.4](#124-a-sidecar-a-repository-declares) and
   [§12.5](#125-what-the-launch-says) ([EW-D33](#EW-D33), [EW-D34](#EW-D34)): the sidecar
   section, the approval record's sidecar part, and the `y` as a repository sidecar's only
   turn-on. A fetched pack's sidecar, which no diff shows, keeps the command
   ([EW-D23](#EW-D23)).

8. ✅ <a id="OQ-EW8"></a>**[OQ-EW8](#OQ-EW8): If two machines are both enabled for one project,
   does anything stop both agents acting on one event?** This decides whether the maintainer's
   watcher narrows what it reports, given that yolo will not guarantee one watcher.

   _Setup:_ Matt enables `ci-watch` for yolo-jail on the desk, and weeks later on the laptop,
   having forgotten. He pushes from the laptop and CI fails. Both watchers see it, both agents
   start fixing it, and neither can see the other. The maintainer ruled that yolo will not try to
   guarantee one watcher across machines ([EW-DIR2](#EW-DIR2)), so the question is only whether
   anything short of that is worth having.

   - **A — Disclosure, plus an authorship filter in his own watcher.** Each launch on each
     machine says `ci-watch … enabled on this machine … since <date>`, and `yolo sidecar list`
     shows that machine's records. The watcher also reports only runs of commits this checkout
     pushed, which git records locally ([§12.6](#126-two-machines-one-project)). The machine that
     pushed hears, and the other stays silent. Runs of commits that no checkout pushed (the merge
     button, bots, other people's pull requests) reach no machine unless one opts in with a flag.
     No core change, no shared state, no write token.
   - **B — Disclosure only.** The same launch lines and list, and both agents act. The worst case
     is duplicated work: the second unforced push is refused as non-fast-forward, so nothing
     shared is corrupted unless an agent force-pushes.

   A lease on GitHub, such as creating one ref per run so that the second create fails, is not
   offered: it is the guarantee EW-DIR2 declined, and it needs a write token the watcher does not
   hold.

   _Leaning:_ **A.** It removes the common case, where the machine that pushed is the one whose
   agent should react, with no credential and no shared state, and core stays blind to CI
   ([EW-P1](#EW-P1)). B is the fallback if a merge-button run that no machine hears is worse
   than a duplicate.

   <!-- vantage: oq id=OQ-EW8 -->

   **Answer:**
   > **Ruled 2026-09-29: B.** (Relayed: *"72 B."*) No filter keeps two machines from acting on
   > the same result: each launch's disclosure line and `yolo sidecar list` on each machine are
   > the whole answer, and a duplicate is at worst duplicate work (the second push is rejected
   > because the branch moved). Consistent with [EW-DIR2](#EW-DIR2): nothing guarantees one
   > watcher across machines.

9. ✅ <a id="OQ-EW9"></a>**[OQ-EW9](#OQ-EW9): In one jail with several agent sessions, which
   sessions does a ping wake?** This restates [OQ-EW2](#OQ-EW2) after the redesign: enabling a
   sidecar on a machine settles which machine hears, not which session. **Directed 2026-09-29
   ([EW-DIR3](#EW-DIR3)), and designed in [§3.6](#36-which-session-a-ping-wakes-the-master).
   The options below are kept as asked; the direction supersedes all three.**

   _Setup:_ On the desk, claude and pi are attached to one yolo-jail jail in two terminals,
   working in the same checkout. `ci-watch` reports a failure on main. Both agents have
   deliverers that can wake an idle session.

   - **A — One session is woken, and the rest are told.** The woken one is the session the user
     last typed into, among those whose agent can be woken. The others see the ping at their
     next turn, marked `delivered to claude (session …)`. The box hands each ping to one waker
     atomically. The cost is one more hook per agent pack, recording when the user last prompted
     that session.
   - **B — Every session is woken.** Both agents take a turn on the failure. In one checkout
     their fixes can edit the same files at once, which is worse than two machines, where at
     least the working trees are separate.
   - **C — The enable command names the agent.** `yolo sidecar enable ci-watch --to claude` wakes
     every claude session and never pings pi.

   <!-- vantage: oq id=OQ-EW9 -->

   _Leaning:_ **A.** [OQ-EW2](#OQ-EW2)'s old leaning, *"a session that does not care ignores
   one line"*, answered a smaller worry than two sessions that both care and share one checkout.
   A answers it where a lock is cheap, on one filesystem, and C can be added later as a filter.
   Two `yolo host` terminals share one box and one watcher under [EW-D19](#EW-D19), so the same
   answer covers them.

   **Answer:** directed, not ruled ([EW-DIR3](#EW-DIR3)), 2026-09-29. None of A to C as written:
   > "I don't really know what to do. The best thing I can come up with is pick one at random. Let the first one that gets there take it. We could have multiple clawed agents in one. We could have multiple pies. So it's not even clear what you would do here. Yeah, I mean, this makes me question entirely. Even trying to enable this by default, because of exactly this, I mean, like, we need to be able to somehow identify a message. Master here. We need a way of saying this is the one that gets it. So I guess we need to be able to have agents elect to be the Master. Otherwise, we need to just like pick the first one launched to have rules. I think that's pretty good. probably the way to go. But we need some inside jail YOLO command where agents can poke and see who is the Master and claim it for themselves if they need to, likely on request of the user."

   Read as: one session per jail is the **master** (the maintainer's word) and is the one a ping
   wakes; by default it is the first session launched; an in-jail `yolo` command shows which session
   is master and lets an agent claim it, usually because the user asked it to; and whether sidecars
   should be on by default is to be reconsidered in that light. The design comes back as new
   questions.

   **Designed 2026-09-29** in [§3.6](#36-which-session-a-ping-wakes-the-master)
   ([EW-D26](#EW-D26) to [EW-D30](#EW-D30)). The master is an agent session, not a yolo session;
   the session list lives in the box, kept by the reader forms, because only the jail side sees
   agent sessions and the host never reads the box; the first registered live one is master, the
   next-oldest inherits, and `yolo notify master --claim` needs nothing passed and no prompt. The
   other sessions see nothing of a ping. The default is revisited in
   [§12.8](#128-what-is-on-by-default-revisited-2026-09-29): no sidecar is on by default, and
   whether the doorbell is on where no sidecar is, is [OQ-EW11](#OQ-EW11).

10. ✅ <a id="OQ-EW10"></a>**[OQ-EW10](#OQ-EW10): Does every declared sidecar wait for a
    machine's explicit act, or may the config exempt some?** This decides whether a sidecar that
    is harmless to run twice can start on every machine the dotfiles reach, as
    [OQ-EW4](#OQ-EW4)'s answer, *"it starts everywhere you specify"*, reads on its own, or
    whether every sidecar needs the per-machine act the CI watcher does. His words on
    [OQ-EW4](#OQ-EW4), *"the config needs to allow a gated set"*, can be read either way, so
    [EW-D13](#EW-D13) and [§12.1](#121-declaring-is-not-enabling) follow the leaning below until
    this is ruled.

    _Setup:_ Matt's synced pack declares two sidecars: `ci-watch`, which he wants on one machine
    only, and `flaky-log`, which tails a local test log and pings when a test flakes. Running
    `flaky-log` on both his desk and his laptop duplicates nothing, because each machine's log is
    its own. He adds both to the pack on the desk, and the laptop pulls it overnight. The question
    is whether `flaky-log` also waits for `yolo sidecar enable flaky-log` on each machine.

    - **A — Every sidecar is gated.** Neither starts on either machine until that machine enables
      it. `flaky-log` costs one command per machine (one `--all-workspaces` under
      [OQ-EW6](#OQ-EW6)'s leaning), and no declaration can exempt itself.
    - **B — The config marks which are gated.** Taken literally, only marked sidecars wait, and
      an unmarked one starts on every machine the dotfiles reach, which is the duplicate
      [EW-DIR1](#EW-DIR1) was about whenever the author forgets the mark. So B is only safe with
      gated as the default and an explicit `"gated": false` that only the user's own word
      ([OQ-EW1](#OQ-EW1)'s set) may write. `flaky-log` then starts everywhere with no command.
      That one line is a synced setting starting a process, which EW-DIR1 ruled out for the CI
      watcher; a repository's own sidecar and a fetched pack's stay gated under every answer
      ([OQ-EW3](#OQ-EW3)).

    <!-- vantage: oq id=OQ-EW10 -->

    _Leaning:_ **A.** [EW-DIR2](#EW-DIR2) says it for a watcher in general: *"you can define the
    shape of a watcher, but it needs explicit permission to activate it."* An exemption written in
    a synced declaration is the synced file starting a process, and the harm of a sidecar that
    should have been gated is one the author learns about on the second machine, after it ran.
    The cost is one command per machine for a sidecar that could safely run everywhere.

    **Answer:** **A, with one change to who may turn it on** (maintainer, 2026-09-29):
    > "I think they should all require explicitly turning on. But if we're talking about something running inside the jail, then the agent inside the jail should be able to be the one that turns it on."

    Every declared sidecar waits for an explicit turn-on, and no declaration can exempt itself. For
    an **agent-side** sidecar (one that runs inside the jail), the agent in that jail may be the one
    that turns it on. That revises [EW-D17](#EW-D17), under which the command refuses in-jail for
    the jail's own workspace. A **host-side** sidecar keeps the host-only act ([OQ-EW5](#OQ-EW5)).
    This fits the gate's stated purpose: it is against the replicator, the synced dotfile
    ([EW-DIR1](#EW-DIR1)), and an agent-side sidecar can hold nothing the agent could not already
    run itself.

    **Carried out 2026-09-30** in [§12.9](#129-turning-one-on-from-inside-the-jail)
    ([EW-D35](#EW-D35) to [EW-D39](#EW-D39)). In-jail, `yolo sidecar enable` records a jail
    turn-on in the jail's own per-workspace home, bound to this machine and checkout by a tag
    each launch hands the jail. The host reads it at the next launch that creates the container,
    ranks it below every host record, and names it at launch as the jail's. A repository's own
    sidecar is not one the jail can turn on: the config approval is its turn-on
    ([EW-D34](#EW-D34)).

11. 💬 <a id="OQ-EW11"></a>**[OQ-EW11](#OQ-EW11): Is the doorbell on in every launch, or only
    in a launch where a sidecar is on?** This decides whether the part of this feature that is
    still on by default ([§12.8](#128-what-is-on-by-default-revisited-2026-09-29)) stays on: the
    doorbell ([§1.2](#12-terms)), meaning the ping box, the deliverers and the session list; the
    `yolo notify` command itself ships either way. No sidecar is on by default under any answer.

    _Setup:_ Matt has not enabled `ci-watch` on his laptop. In yolo-jail there he runs
    `yolo -- claude` in one terminal and `yolo -- pi` in another, and nothing on the laptop is set
    to ring. As designed, both sessions still register in the jail's box, claude's deliverer waits
    after every turn, and when pi runs `yolo notify "tests done"` from a background task, claude,
    the master, is woken by it. On his Mac, every `yolo host -- claude` there also gets a keeper
    for its box, and stays resident as claude's parent.

    - **A — On in every launch, as designed.** `yolo notify` works from any shell, script or
      background task in any launch, and the master takes each ping. Matt sees no change from
      [§3.3](#33-yolo-notify-and-the-ping-box). The cost: the deliverer runs in every Claude
      session whether anything rings or not, before its behavior over a long idle is measured
      ([§8](#8-costs-and-risks)); any agent session in a jail can wake another; and every
      `yolo host` launch gets a keeper and a resident `yolo`.
    - **B — On only where a sidecar will start.** The box exists in a launch only when at least one
      sidecar passes its gate for that workspace on that machine, enabled or acknowledged.
      Elsewhere there is no box: a deliverer finds none and returns at once, as
      [§3.3](#33-yolo-notify-and-the-ping-box) already specifies; no agent session registers;
      `yolo notify` exits 3 saying no sidecar is on for this workspace on this machine; and a
      `yolo host` launch with nothing else to hold execs as today. The cost: an agent's own
      background task cannot ring its session unless some sidecar is on there. For Claude its own
      Monitor tool already covers that case ([Appendix A.1](#a1-claude-21284)); for pi nothing does.

    <!-- vantage: oq id=OQ-EW11 leaning="B: turn the doorbell on only where a sidecar will start, since nothing in yolo is active by default, A runs an unmeasured hook in every Claude session and a resident yolo host launch for a box nothing writes; the use B gives up, any background process ringing, stays open by declaring that process a sidecar." -->

    _Leaning:_ **B.** Nothing in yolo is active by default: an empty config gives a jail with no
    agent, and the launch says so (`run.warnIfNoPacks`, [`AGENTS.md`](../../AGENTS.md)). A pays
    for a deliverer in every Claude session, before its long-idle behavior is measured, and for a
    keeper and a resident `yolo host` launch, all for a box nothing is set to write. What B gives
    up is part of what the maintainer asked for: *"any generic background process that can decide
    however it wants to ping the foreground agent session"* ([§1.1](#11-why-now)). Under B such a
    process reaches a session only where some sidecar is on, or once it is declared as a sidecar
    and turned on itself, which keeps the use at the price of one declaration and one turn-on per
    process, and matches his later doubt about *"trying to enable this by default"*. **The trap:** if an agent ringing its own
    or another session is a use the maintainer wants in every launch, a long build or a deploy
    announcing itself, A is right, and the master is what makes A safe with several sessions in
    one box.

    **Answer:**
    > _(empty — fill in when decided)_

12. 💬 <a id="OQ-EW12"></a>**[OQ-EW12](#OQ-EW12): When one workspace runs at two notches on one
    machine, which of them does a sidecar's ping reach?** This decides whether an agent can take
    the pings over across notches, which [EW-DIR3](#EW-DIR3)'s *"claim it for themselves if they
    need to"* asks of it within one box.

    _Setup:_ On his Mac, Matt has `ci-watch` enabled host side for yolo-jail. He runs
    `yolo -- codex` there in one tab, which is a macos-user sandbox, and later
    `yolo host -- claude` in another tab of the same checkout. The two notches are two keepers
    with two ping boxes, because a box never crosses notches
    ([JL-D37](jail-lifetime-last-session-wins.md#JL-D37)). `ci-watch` runs once per workspace per
    machine ([EW-D25](#EW-D25)), under the keeper that started it, the macos-user one. So every CI
    ping wakes codex, that box's master, and claude in the host tab hears none. When he asks
    claude to take the pings over, `yolo notify master --claim` works only inside claude's own
    box, which no sidecar rings. The only way to move them is to quit every macos-user session,
    after which the `yolo host` keeper starts its own `ci-watch`.

    - **A — The notch whose keeper started the sidecar keeps its pings.** The host tab's launch
      line says `ci-watch` runs under the macos-user keeper and that its pings reach that box's
      master ([§12.5](#125-what-the-launch-says)), and `yolo notify master` in the host tab says
      the same and names the way to move it: quit that notch's sessions. The cost: a claim does
      not reach across notches.
    - **B — One watcher per notch.** Each keeper starts its own `ci-watch`, so both boxes' masters
      are pinged, and codex and claude both wake on one CI failure in one checkout. The cost: the
      duplicate [EW-DIR3](#EW-DIR3) set out to prevent, now inside one machine, and two pollers of
      one repository.
    - **C — A claim made at `yolo host` pulls a host-side sidecar's pings across.** A host-side
      sidecar's writer runs on the host, and may read the `yolo host` box, which only the user's
      own processes write. Before each ping it checks there for a claim, and rings the claiming
      box instead of its own, so claude's `--claim` in the host tab takes `ci-watch`'s pings from
      the macos-user keeper. The cost: it works in one direction only, since the host side never
      reads a box a jail or a sandbox writes ([EW-D8](#EW-D8)), so codex could not pull them back
      by a claim; an agent-side sidecar's pings never move; and two keys are coupled through the
      writer.

    <!-- vantage: oq id=OQ-EW12 leaning="A: one workspace at two notches at once is the uncommon case, the launch line and yolo notify master say where the pings go and how to move them, B brings back the duplicate the master exists to prevent, and C couples two keys for a claim that works in one direction only." -->

    _Leaning:_ **A.** One workspace open at two notches at once is the uncommon case, and A tells
    the user where the pings go and how to move them, in the launch line and in
    `yolo notify master`. B brings back two agents acting on one event in one checkout, which the
    master exists to prevent. C couples two keys through the writer for a claim that works in one
    direction only. **The trap:** if Matt routinely keeps a host agent beside a sandboxed one in
    one checkout on the Mac and wants the host one to hear CI, A strands the pings in whichever
    notch launched first, and C is worth its coupling.

    **Answer:**
    > _(empty — fill in when decided)_

13. 💬 <a id="OQ-EW13"></a>**[OQ-EW13](#OQ-EW13): When a sidecar is turned on while a keeper
    already runs for the workspace, does it start at the next launch, or only at the next fresh
    one?** This decides how soon `yolo sidecar enable` takes effect wherever a session stays open
    all day.

    _Setup:_ On his Mac, Matt keeps `yolo host -- claude` open in yolo-jail all day. At noon he runs
    `yolo sidecar enable ci-watch` there, then opens a second tab with `yolo host -- pi`. That tab
    joins the running keeper
    ([`jail-lifetime-last-session-wins.md` §9.9.5](jail-lifetime-last-session-wins.md#995-how-a-second-launch-joins)),
    which runs what it started with at 9 a.m., and that had no `ci-watch`. As designed, `ci-watch`
    does not start: the second tab's line says it runs from the workspace's next fresh launch at
    `yolo host`, which comes only once every `yolo host` session there has quit. With one tab open
    all day, that is tomorrow. A container jail behaves the same for a host-side sidecar, since an
    attach starts none ([EW-D10](#EW-D10)). [OQ-EW5](#OQ-EW5)'s answer said *"the next launch of
    that workspace starts the sidecar"* and *"then when you start up, it's going to fire it up"*,
    which the keeper turns into "the next fresh launch".

    - **A — The next fresh launch starts it, as designed.** `yolo sidecar enable` says so, and so
      does each joining launch's line: *"enabled since 12:04; starts at this workspace's next fresh
      launch here, once its 1 other session has quit"*. Nothing starts beside a running keeper.
      The cost: an always-open tab delays it indefinitely.
    - **B — Any next launch starts it, through the running keeper.** The joining launch prints the
      sidecar's line as a fresh launch would, then hands the keeper that one sidecar. The keeper
      re-checks the gate, as it does before starting a sidecar another key gave up
      ([EW-D25](#EW-D25)), starts it, and holds it to the key's end like the rest. Matt's second tab
      starts `ci-watch` at noon. The cost: the keeper runs more than its first plan, a second path
      from a disclosure to a start, for sidecars only ([JL-D39](jail-lifetime-last-session-wins.md#JL-D39)
      refuses it for a joiner's services); and in a container jail it reaches host-side sidecars
      only, since an agent-side one's supervisor is fixed at boot.
    - **C — `yolo sidecar enable` starts it in every running keeper of the workspace on this
      machine.** The command prints the disclosure in its own terminal and asks each keeper to
      start it, so nothing waits for a launch. The cost: the same second path, driven by a command
      rather than a launch; the same agent-side limit; and the running sessions learn of it only at
      their next quit or arrival
      ([JL-D19](jail-lifetime-last-session-wins.md#JL-D19)).

    <!-- vantage: oq id=OQ-EW13 leaning="B: it keeps OQ-EW5's words, the next launch starts it, with the disclosure printed by a launch before the start; the keeper already starts a sidecar late after re-checking the gate when another key gives one up, so B reuses that path rather than adding one; A can delay an enable for as long as any tab stays open, and C starts code with no launch to say so." -->

    _Leaning:_ **B.** It keeps [OQ-EW5](#OQ-EW5)'s words, *"the next launch of that workspace
    starts the sidecar"*, with the disclosure printed by a launch before the start. The keeper
    already starts a sidecar after its first plan, re-checking the gate, when another key gives
    one up ([EW-D25](#EW-D25)), so B reuses that path rather than adding a new one. A delays an
    enable for as long as any tab of the workspace stays open, and C starts code with no launch to
    say so to the sessions it serves. **The trap:** if a keeper that grows during its life is
    worse than a late start, because `ps` and the keeper line should say at launch everything a
    keeper will ever run, A is the answer, and `yolo sidecar enable` says to quit the other
    sessions first.

    **Answer:**
    > _(empty — fill in when decided)_

## 14. Decision Ledger

Rulings (2026-09-29) and implementation decisions, taken where a question had one right answer,
recorded so an implementer does not reopen them.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="EW-D1"></a>[`EW-D1`](#14-decision-ledger) | *Implementation decision.* A sidecar is its own contribution kind, `sidecar`, not a `kind: "service"`: a service holds no grant and has an endpoint, a witness and a caller token; a host-side sidecar holds a credential and has none of those. Agent-side sidecars reuse the supervisor | 2026-09-28 | [§3.1](#31-declaring-a-sidecar) | — |
| <a id="EW-D2"></a>[`EW-D2`](#14-decision-ledger) | *Implementation decision.* The doorbell's one command, `yolo notify`, is a subcommand of the one binary on both sides, not a new `cmd/` binary, so it adds nothing to `shippedBinaries` or the bundle | 2026-09-28 | [§3.3](#33-yolo-notify-and-the-ping-box) | — |
| <a id="EW-D3"></a>[`EW-D3`](#14-decision-ledger) | *Implementation decision.* The box is a launch-owned host directory mounted into the jail, one file per ping, renamed into place. Readers poll once a second and use filesystem notifications only as a speed-up. *Revised 2026-09-29:* at the host notch and on macos-user the box is per workspace on the machine, not per launch ([EW-D19](#EW-D19)). *Revised again 2026-09-29:* the box is the keeper's at every notch, and it holds the session list ([EW-D24](#EW-D24), [EW-D26](#EW-D26)) | 2026-09-28 · revised 2026-09-29 | [§3.3](#33-yolo-notify-and-the-ping-box) | — |
| <a id="EW-D4"></a>[`EW-D4`](#14-decision-ledger) | *Implementation decision.* Deliverers never read the box. They call `yolo notify --wait`, `--follow` or `--drain`, so the format stays private to core and one reader implementation serves every agent | 2026-09-28 | [§3.3](#33-yolo-notify-and-the-ping-box) | — |
| <a id="EW-D5"></a>[`EW-D5`](#14-decision-ledger) | *Implementation decision.* Claude's deliverer is an `asyncRewake` command hook on `SessionStart` and `Stop`, one waiter per session by lock, with an explicit 86400-second timeout. Not the inbox socket ([§7](#7-alternatives-considered) option E) and not MCP channels (option D) | 2026-09-28 | [§3.4](#34-deliverers-per-agent) | — |
| <a id="EW-D6"></a>[`EW-D6`](#14-decision-ledger) | *Implementation decision.* pi's deliverer uses `sendMessage` with `triggerTurn: true` and `deliverAs: "followUp"`, never `sendUserMessage`, so a ping neither interrupts a turn nor arrives as the user | 2026-09-28 | [§3.4](#34-deliverers-per-agent) | — |
| <a id="EW-D7"></a>[`EW-D7`](#14-decision-ledger) | *Implementation decision.* Every ping is cleaned of control characters, cut to 1024 bytes and 8 lines, framed as an automated notice with no user authority, batched within 2 seconds, and limited to 6 delivered per 60 seconds per sender label, with the excess held and counted | 2026-09-28 | [§5.1](#51-pinged-text-is-a-prompt-injection-channel) | — |
| <a id="EW-D8"></a>[`EW-D8`](#14-decision-ledger) | *Implementation decision.* The host side never reads the box, and its writer creates each file relative to the box directory, exclusively, following no link | 2026-09-28 | [§3.3](#33-yolo-notify-and-the-ping-box) | — |
| <a id="EW-D9"></a>[`EW-D9`](#14-decision-ledger) | *Implementation decision.* A host-side sidecar gets only `PATH`, `HOME`, the four `YOLO_*` variables and the host variables its `host_env` names. None of their values is written anywhere the jail can read. Names are disclosed, values never | 2026-09-28 | [§5.2](#52-secrets-stay-with-the-sidecar) | — |
| <a id="EW-D10"></a>[`EW-D10`](#14-decision-ledger) | *Implementation decision.* Host-side sidecars belong to the launch that creates the container; an attach starts none. A sidecar enabled at the host notch makes `yolo host` resident, in [`host-notch-services.md` §4.4](host-notch-services.md#44-lifetime)'s shape. *Revised 2026-09-29:* "declared" became "enabled" ([EW-D13](#EW-D13)), and at the host notch and on macos-user one instance per workspace per machine is shared across launches ([EW-D19](#EW-D19)). *Revised again 2026-09-29:* host-side sidecars belong to the keeper, not to the launch that creates the container, and at `yolo host` and macos-user to that workspace's keeper there ([EW-D25](#EW-D25)) | 2026-09-28 · revised 2026-09-29 | [§3.2](#32-lifecycle-per-side-and-per-notch) | — |
| <a id="EW-D11"></a>[`EW-D11`](#14-decision-ledger) | *Implementation decision.* A sidecar never delays or refuses a launch by failing to start. It is retried with the supervisor's backoff and logged. A malformed declaration does refuse | 2026-09-28 | [§3.5](#35-failure-paths) | — |
| <a id="EW-D12"></a>[`EW-D12`](#14-decision-ledger) | *Implementation decision.* Each agent pack declares its deliverer's tier, and the launch discloses per sidecar what its pings will do with the agent being launched. *Revised 2026-09-29:* where the box already has a master, the line names that master instead ([EW-D32](#EW-D32)). A tier is declared **wake** only after a human has measured it | 2026-09-28 | [§3.4](#34-deliverers-per-agent) | — |
| [OQ-EW1](#OQ-EW1) | **Maintainer ruling:** A; only the user's own word declares a host-side sidecar | 2026-09-29 | [§13](#13-open-questions) | pending |
| [OQ-EW3](#OQ-EW3) | **Maintainer ruling:** a repository may declare an agent-side sidecar, and it runs only after the user's explicit acknowledgement | 2026-09-29 | [§13](#13-open-questions) | pending |
| [OQ-EW5](#OQ-EW5) | **Maintainer ruling:** A, narrowed; `yolo sidecar enable` runs at the host in the workspace it enables (or names that workspace's path), per repository per machine; the next launch starts it | 2026-09-29 | [§12](#12-enabling-a-sidecar-redesign-2026-09-29) | pending |
| [OQ-EW6](#OQ-EW6) | **Maintainer ruling:** A; one workspace by default, `--all-workspaces` kept as an option | 2026-09-29 | [§12](#12-enabling-a-sidecar-redesign-2026-09-29) | pending |
| [OQ-EW7](#OQ-EW7) | **Maintainer ruling:** B; approving a repository's config diff acknowledges its sidecars, shown in their own section. Carried out 2026-09-30 in [§12.4](#124-a-sidecar-a-repository-declares) ([EW-D33](#EW-D33), [EW-D34](#EW-D34)) | 2026-09-29 | [§12](#12-enabling-a-sidecar-redesign-2026-09-29) | pending |
| [OQ-EW8](#OQ-EW8) | **Maintainer ruling:** B; no cross-machine filter, disclosure and `yolo sidecar list` only | 2026-09-29 | [§12](#12-enabling-a-sidecar-redesign-2026-09-29) | pending |
| [OQ-EW10](#OQ-EW10) | **Maintainer ruling:** A; every declared sidecar waits for an explicit turn-on and none is exempt. For an agent-side sidecar the jail's own agent may turn it on, which revises [EW-D17](#EW-D17); a host-side sidecar keeps the host-only act. Carried out 2026-09-30 in [§12.9](#129-turning-one-on-from-inside-the-jail) ([EW-D35](#EW-D35) to [EW-D39](#EW-D39)) | 2026-09-29 | [§13](#13-open-questions) | pending |
| <a id="EW-DIR1"></a>EW-DIR1 | **Maintainer direction:** the feature is held until enabling a sidecar is an intentional per-machine act that a synced dotfile cannot trigger, and two machines watching one project is designed for ([OQ-EW2](#OQ-EW2)) | 2026-09-29 | [§12](#12-enabling-a-sidecar-redesign-2026-09-29) | — |
| <a id="EW-DIR2"></a>EW-DIR2 | **Maintainer direction,** the second on [OQ-EW2](#OQ-EW2): declaring a watcher's shape is separate from the explicit permission that activates it, and yolo does not try to guarantee a single watcher across machines | 2026-09-29 | [§12.6](#126-two-machines-one-project) | — |
| <a id="EW-DIR3"></a>EW-DIR3 | **Maintainer direction** on [OQ-EW9](#OQ-EW9): a ping wakes one session per jail, the **master** (the maintainer's word); by default the first session launched; an in-jail `yolo` command shows the master and lets an agent claim it, likely at the user's request; and whether sidecars are on by default is to be reconsidered given this. Designed in [§3.6](#36-which-session-a-ping-wakes-the-master) ([EW-D26](#EW-D26) to [EW-D30](#EW-D30)) and [§12.8](#128-what-is-on-by-default-revisited-2026-09-29), whose question is [OQ-EW11](#OQ-EW11); the review of the design added [OQ-EW12](#OQ-EW12) and [OQ-EW13](#OQ-EW13) | 2026-09-29 | [§13](#13-open-questions) | designed, not built |
| [OQ-EW4](#OQ-EW4) | **Maintainer ruling:** A, inside a gate: *"it starts everywhere you specify the config needs to allow a gated set."* Read as: no second config switch per workspace. Which sidecars the gate covers was ruled in [OQ-EW10](#OQ-EW10) (every one), and its mechanics in [OQ-EW5](#OQ-EW5) to [OQ-EW7](#OQ-EW7) | 2026-09-29 | [§12.1](#121-declaring-is-not-enabling) | pending |
| <a id="EW-D13"></a>[`EW-D13`](#14-decision-ledger) | *Implementation decision, as [OQ-EW10](#OQ-EW10) ruled (A),* carrying out [OQ-EW4](#OQ-EW4), [EW-DIR1](#EW-DIR1) and [EW-DIR2](#EW-DIR2). That ruling also lets the jail's own agent turn on an agent-side sidecar, which revises [EW-D17](#EW-D17) and is carried out in [§12.9](#129-turning-one-on-from-inside-the-jail). The gated set is every declared sidecar, from every source: none runs on a machine without that machine's own turn-on, which is its enablement record, its config approval for a sidecar the workspace declares ([EW-D34](#EW-D34)), or a jail turn-on bound to it ([EW-D35](#EW-D35)), and no declaration can exempt itself. Config's `enabled` can only veto: `false` at any scope keeps a sidecar off, and `true` at any scope starts nothing, since otherwise the replicator is the trigger. This deliberately differs from loopholes, whose `enabled` switches on from either scope | 2026-09-29 | [§12.1](#121-declaring-is-not-enabling) | — |
| <a id="EW-D14"></a>[`EW-D14`](#14-decision-ledger) | *Implementation decision.* The enablement record lives under `paths.ApprovalsDir()`, as a part beside the approval record for one workspace and as one machine-wide file whose name no container name can take (every container name begins `yolo-`, `FromResolved` in [`naming.go`](../../internal/runtime/naming.go)). Never under `~/.config/yolo-jail`, never under `cache/`, never mounted into a jail. As [OQ-EW5](#OQ-EW5) ruled (A), only `yolo sidecar enable` and `disable` write it, and nothing that writes it writes config. It holds, per sidecar, on or off, the name, the declaring source (user config, the local pack, a pack identified by the source address its entry is written with and never by its name, or the workspace), the time, and for a sidecar the workspace or a fetched pack declares a hash of every field of the resolved declaration, plus, for a fetched pack, its locked commit. A record is honored only for a declaration from the same source and, where it holds a hash or a commit, the same ones. A missing record fails safe, and a path that deletes the approval record deletes this part too. *Revised 2026-09-29:* a pack is keyed by source address rather than name, fetched packs are hash-bound, the hash covers every field rather than `cmd`, `side` and `restart`, and a record can say off. *Revised 2026-09-30:* a sidecar the workspace declares is acknowledged in the approval record's sidecar part ([EW-D33](#EW-D33)), not here, so this record holds a hash only for a fetched pack's, and for the workspace's own it can only say off ([EW-D34](#EW-D34)). A jail turn-on is not this record: the jail writes it in its own home and the host only reads it ([EW-D35](#EW-D35)) | 2026-09-29 · revised 2026-09-29 · revised 2026-09-30 | [§12.2](#122-where-the-record-lives-and-why-there) | — |
| <a id="EW-D15"></a>[`EW-D15`](#14-decision-ledger) | *Implementation decision.* The record carries the id of the machine it was made on (`/etc/machine-id` on Linux, `IOPlatformUUID` on macOS). A record naming another machine is not honored, and the launch says so and names the command, so a synced home or a restored backup carries no enablement. *Revised 2026-09-29:* on a host with no readable id the command refuses and says why, and no record is honored there, because every id-less host would otherwise count as one machine. In-jail, where no id is readable either (MEASURED: this jail has no `/etc/machine-id`), an id-less record is allowed, since that store is the jail's own and per workspace, and the jail is the blast radius ([EW-D17](#EW-D17)). Machines cloned from one image with the id left in place share it, a stated limit | 2026-09-29 · revised 2026-09-29 | [§12.2](#122-where-the-record-lives-and-why-there) | — |
| <a id="EW-D16"></a>[`EW-D16`](#14-decision-ledger) | *Implementation decision.* A sidecar that is not enabled, not acknowledged, vetoed, off, not allowed at this notch, not yet available on this backend, or running under another launch never refuses a launch. As [OQ-EW5](#OQ-EW5) ruled (A), the launch also never asks about one; a sidecar the workspace declares is shown in the config-change prompt the launch already runs ([EW-D33](#EW-D33)). Every fresh launch prints one line per declared sidecar naming its state and, where one exists, the command that would start it; `yolo check` and `yolo sidecar list` report the same. The line is a disclosure and no flag hides it | 2026-09-29 | [§12.5](#125-what-the-launch-says) | — |
| <a id="EW-D17"></a>[`EW-D17`](#14-decision-ledger) | *Implementation decision, as [OQ-EW5](#OQ-EW5) ruled (A).* In-jail, `yolo sidecar enable` and `disable` for the jail's own workspace refuse and name the host command, the [OQ-S2](../reference/config-safety.md#oq-s2) precedent. For a nested workspace they write the jail's own store, since inside a jail the jail is the machine. **Revised by [OQ-EW10](#OQ-EW10) (2026-09-29):** for an agent-side sidecar the jail's own agent may turn it on in-jail; the refusal stands for a host-side one. *Carried out 2026-09-30* in [§12.9](#129-turning-one-on-from-inside-the-jail) ([EW-D35](#EW-D35) to [EW-D39](#EW-D39)): in-jail, `enable` for an agent-side sidecar records a jail turn-on, and the refusal stands for a host-side sidecar and, by [EW-D34](#EW-D34), for one the workspace's own config declares | 2026-09-29 · revised 2026-09-30 | [§12.3](#123-the-command) | — |
| <a id="EW-D18"></a>[`EW-D18`](#14-decision-ledger) | *Implementation decision,* following from [OQ-EW1](#OQ-EW1). A sidecar the workspace's own config declares does not run under `yolo host`, where the agent side and the host side are one place and it would be host code a workspace declared. It is not started and the launch says why; the launch is not refused | 2026-09-29 | [§12.4](#124-a-sidecar-a-repository-declares) | — |
| <a id="EW-D19"></a>[`EW-D19`](#14-decision-ledger) | ***Superseded 2026-09-29 by [EW-D25](#EW-D25)***, under [OQ-JL5](jail-lifetime-last-session-wins.md#OQ-JL5)'s ruling: within one notch the keeper owns each sidecar, so no lock passes one from launch to launch; the lock between keepers keeps its cross-notch half. Kept as written: *Implementation decision.* At most one instance of each sidecar runs per workspace per machine, across every notch. Every launch that starts sidecars, the launch that creates a container included, holds a kernel lock per sidecar it starts, on either side, under `~/.local/share/yolo-jail/locks/`, beside the launch lock. A launch that finds one held starts none and names the launch holding it. At the host notch and on macos-user a second launch of the workspace shares the first one's box and waits on the lock, so the kernel hands the sidecar to it when the first exits; a container launch does not wait, since its supervisor's set is fixed at boot. A takeover re-reads the vetoes and the records and checks the declaration's hash before it starts anything. The box at the host notch and on macos-user is per workspace, and it is cleared only once no launch is known to hold it. *Revised 2026-09-29:* the lock first covered only the host notch and macos-user, which left a container launch and a `yolo host` launch each running the sidecar | 2026-09-29 · revised 2026-09-29 | [§12.7](#127-one-machine-several-launches-or-sessions) | — |
| <a id="EW-D20"></a>[`EW-D20`](#14-decision-ledger) | *Implementation decision.* A host-side sidecar obeys the loophole placement rule ([`loophole-system.md`](../reference/loophole-system.md#the-placement-rule)): refused by name, at the spawn, when `{pack_dir}` or its program resolves inside the workspace the launch mounts or the jail home yolo manages, so an agent cannot edit the host code a host-side sidecar runs | 2026-09-29 | [§3.1](#31-declaring-a-sidecar) | — |
| <a id="EW-D21"></a>[`EW-D21`](#14-decision-ledger) | *Implementation decision,* stated for sidecar authors in the kind's documentation, since yolo cannot see what a script does. A host-side sidecar reads the workspace's git state (`.git/config`, the reflog under `.git/logs/`) as text and never runs host `git` there. It opens only regular files, follows no link and ignores `include` directives, so the agent can neither point it at a host file nor hang it on a FIFO. These are the rules [BB-D19](boundary-broker.md#BB-D19) set for the broker. The worked example follows them | 2026-09-29 | [§4](#4-the-worked-example-the-ci-watcher) | — |
| <a id="EW-D22"></a>[`EW-D22`](#14-decision-ledger) | *Implementation decision.* `sidecars` has a key-by-key scope table ([§3.1](#31-declaring-a-sidecar)), checked against each config file as written, before the field-by-field merge. A workspace entry under a name that user config, the local pack or a selected pack declares may carry only `enabled`. At any scope, an entry without `cmd` may carry only `enabled`, so changing another field means writing a whole declaration, which then belongs to that scope. Vetoes are read per file, so a workspace `true` never undoes a user `false`. A violation is refused by key name, an error on the host and a warning in-jail, as loopholes' scope violations are, and never reaches a spawn | 2026-09-29 | [§3.1](#31-declaring-a-sidecar) | — |
| <a id="EW-D23"></a>[`EW-D23`](#14-decision-ledger) | ***Superseded 2026-09-30 by [EW-D33](#EW-D33) for a sidecar the workspace declares, and kept for a fetched pack's.*** [OQ-EW7](#OQ-EW7)'s ruling (B) moved a repository's own acknowledgement into the config-change prompt's sidecar section. A fetched pack's sidecar is in no diff, since user config alone selects packs and never prompts, so the command below still acknowledges it at the host, printing the pack's locked commit with the declaration. Kept as written: *Implementation decision, under [OQ-EW7](#OQ-EW7)'s leaning (A).* For a hash-bound sidecar, the acknowledging command prints the whole resolved declaration and the file it came from, asks y/N, and records the hash of exactly what it printed; with no terminal it refuses. So a pull or an agent edit landing between the launch line and the command is never acknowledged unseen. The hash covers the declaration, not a script its `cmd` names, and the command says so | 2026-09-29 · revised 2026-09-30 | [§12.4](#124-a-sidecar-a-repository-declares) | — |
| <a id="EW-D24"></a>[`EW-D24`](#14-decision-ledger) | *Implementation decision, under [OQ-JL5](jail-lifetime-last-session-wins.md#OQ-JL5)'s ruling.* **The ping box is the keeper's at every notch**: one per keeper, which is one per container jail and one per workspace at each of macos-user and `yolo host`, created when the key starts and removed, session list included, when it ends. At a container backend it is bind mounted into the jail; at `yolo host` it lives in host state no jail mounts, never under the workspace's `.yolo/`, which the same workspace's container jail can write; at macos-user it lives in a two-account dir of the shape setup provisions, outside every workspace, so no container jail of the workspace can write it (unmeasured and unbuilt). A box never spans two notches ([JL-D37](jail-lifetime-last-session-wins.md#JL-D37)), since a box a more confined notch writes and a less confined agent reads would reverse [EW-D8](#EW-D8). Until the keeper lands at containers, the launch that creates the container owns it, as [EW-D3](#EW-D3) said | 2026-09-29 | [§3.3](#33-yolo-notify-and-the-ping-box) | — |
| <a id="EW-D25"></a>[`EW-D25`](#14-decision-ledger) | *Implementation decision, replacing [EW-D19](#EW-D19).* **Within one notch, the workspace's keeper owns each sidecar it starts from its key's first session to its last**, so a second launch there joins the keeper, starts none, shares the box, and nothing passes from launch to launch. **Across notches, one instance per workspace per machine is kept by a per-sidecar kernel lock that only keepers take**: a keeper that finds it held starts none and its launch names the holder; one that could start the sidecar later (every sidecar at `yolo host`, every host-side one elsewhere, never an agent-side one in a container, whose supervisor is fixed at boot) waits on the lock and starts its own instance when the other key ends, re-checking the vetoes, the records and the declaration's hash first. At macos-user an agent-side sidecar cannot take part, since each session's guest supervisor would start its own and only keepers take the lock, so it is not started there ([§3.2](#32-lifecycle-per-side-and-per-notch)). Forced by [JL-D14](jail-lifetime-last-session-wins.md#JL-D14): a lock that hands a running jail's sidecar from launch to launch is the handoff rejected for container jails, while a second keeper starting its own instance after the first has gone moves nothing running | 2026-09-29 | [§12.7](#127-one-machine-several-launches-or-sessions) | — |
| <a id="EW-D26"></a>[`EW-D26`](#14-decision-ledger) | *Implementation decision, carrying out [EW-DIR3](#EW-DIR3).* **The master is an agent session, and the session list lives in the box, kept by `yolo notify`'s reader forms, not by the keeper.** Forced three ways in a jail: only the jail side sees agent sessions (a reader id is the deliverer's, and a bare shell's agents are invisible to the host), while the keeper counts sessions only as one shared lock ([JL-D2](jail-lifetime-last-session-wins.md#JL-D2)); the host side never reads the box ([EW-D8](#EW-D8)), so a keeper-held list would need a new jail-to-host channel for a claim; and any jail process can already write the box, so a list outside it protects nothing (Test 1 of [`gate-placement-principle.md`](../reference/gate-placement-principle.md#test-1--the-authority-test-could-this-actor-already-do-it)). At `yolo host` the first two do not hold, since the agent is the resident session's child and the host-side deliverers read the box; the list stays in the box there because the third still holds, and one list shape serves every notch. The keeper owns the box's lifetime ([EW-D24](#EW-D24)) | 2026-09-29 | [§3.6](#36-which-session-a-ping-wakes-the-master) | — |
| <a id="EW-D27"></a>[`EW-D27`](#14-decision-ledger) | *Implementation decision.* **An agent session registers through its deliverer's first reader call, which passes `--agent-pid`**, and is recorded with that pid, the process's start time (`/proc/<pid>/stat` field 22 on Linux, the kernel's process record through `sysctl` on macOS), its reader id, its tier and the time. It is alive while a process with that pid and start time exists, so a reused pid never passes for it. A later call from the same pid with a new reader id (claude's `/clear`, a pi reload) updates the id and keeps its place. Each agent session's reader lock is keyed on that session, its pid and start time, never on the reader id alone, since two claudes resuming one conversation can carry one session id. Where a macos-user sandbox cannot read another process's record (UNMEASURED), a wake session's held reader lock is its liveness, and a next-turn session is master there only by a claim. Each agent's pack names how its deliverer finds the pid, since core does not know what an agent is ([EW-P2](#EW-P2)); claude's `$PPID` is INFERRED and measured with its deliverer | 2026-09-29 | [§3.6](#36-which-session-a-ping-wakes-the-master) | — |
| <a id="EW-D28"></a>[`EW-D28`](#14-decision-ledger) | *Implementation decision, reading [EW-DIR3](#EW-DIR3)'s "pick the first one launched".* **With no claim in force the master is the earliest registered agent session that can take a ping, resolved at each take, never handed over.** A wake session can while its reader holds its session's reader lock, and a next-turn session while its agent lives; one whose waiter timed out or died is passed over until it re-arms, since *"Let the first one that gets there take it"* is in the same answer. When it exits, the next ping goes to the next-oldest one that can take it; a claim holds while its claimant lives, armed or not; a claim lapses with its session and the default resumes, never an earlier claimant; a ping rung with no live agent session waits and the next master takes it under the cap of 20. The tier does not decide: a next-turn agent launched first is master, and `yolo notify master` and each launch's disclosure show its tier so the user can have another claim. A ping taken just before its master's agent exits is lost, and the last takes are listed | 2026-09-29 | [§3.6](#36-which-session-a-ping-wakes-the-master) | — |
| <a id="EW-D29"></a>[`EW-D29`](#14-decision-ledger) | *Implementation decision, carrying out [EW-DIR3](#EW-DIR3)'s in-jail command.* **`yolo notify master` shows the list; `--claim` makes the calling agent session master; `--release` gives the claim back; `--reader ID` names another session.** It lives under `yolo notify`, since the master governs every ping, while `yolo sidecar` turns sidecars on and off, a different act with its own rules of who may run it ([EW-D17](#EW-D17), as [OQ-EW10](#OQ-EW10) revised it). The caller passes nothing: the command walks its own ancestors to the first registered agent process, with `--reader` as the fallback where that cannot be read (macos-user's Seatbelt profile, UNMEASURED). **A claim needs no prompt**: a confirmation would stop nothing any jail process cannot already do to the box, and an agent's tool shell has no terminal to answer one; *"likely on request of the user"* is how an agent comes to run it, not a gate. The claim is printed and recorded (who, when, whom it displaced), and the environment briefing names the command wherever a box exists | 2026-09-29 | [§3.6](#36-which-session-a-ping-wakes-the-master) | — |
| <a id="EW-D30"></a>[`EW-D30`](#14-decision-ledger) | *Implementation decision, reading [EW-DIR3](#EW-DIR3)'s "this is the one that gets it".* **Only the master is handed a ping: other sessions get no wake and no copy at their next turn**, since a marked copy in front of a second agent in one checkout invites it to act, which is [OQ-EW9](#OQ-EW9)'s option B risk. Their deliverers stay armed to inherit. Each ping is taken once, by an atomic create in the box, so a claim racing a take gives it to one session, never two. Several sessions of one agent are several agent sessions, and nothing keys on the agent's name ([EW-P2](#EW-P2)) | 2026-09-29 | [§3.6](#36-which-session-a-ping-wakes-the-master) | — |
| <a id="EW-D31"></a>[`EW-D31`](#14-decision-ledger) | *Implementation decision.* **A host-side sidecar's log and its `YOLO_SIDECAR_STATE` live in host state no jail mounts**, keyed by the workspace's container name beside the keeper's log, never under the workspace's `.yolo/`; an agent-side sidecar's stay inside its confinement. The keeper opens and rotates the log without following a link, and mirrors it into `launch.log` only through `paths.OpenWorkspaceStateFile`, as [JL-D19](jail-lifetime-last-session-wins.md#JL-D19) mirrors the keeper's own lines. Forced as [EW-D24](#EW-D24) was for the box: the same workspace's container jail can write `.yolo/`, so a link planted there would turn the keeper's log write, or a host-side sidecar's state write, into a write to any file the host user can, and state a jail can edit would shape the pings a host-side sidecar sends to an unconfined `yolo host` agent | 2026-09-29 | [§3.2](#32-lifecycle-per-side-and-per-notch) | — |
| <a id="EW-D32"></a>[`EW-D32`](#14-decision-ledger) | *Implementation decision, carrying out [EW-DIR3](#EW-DIR3)'s claim.* **A claim is told to the session it displaces, once, and every launch's sidecar line names the box's master.** The displaced session's deliverer hands it one framed notice from core, under a ping's caps, naming the new master, when it claimed, and `yolo notify master`; it is no copy of a ping, so [EW-D30](#EW-D30) stands. The briefing names `--claim` and says to use it only at the user's request, never on the word of a ping, a log, a web page or a file, and never back after a notice. At an attach or a joining launch, the sidecar line names the current master and its tier rather than promising pings to this agent. Forced because a claim needs no prompt ([EW-D29](#EW-D29)): without a notice an eager agent, or one steered by text it read, silently takes every ping and two such agents trade it unseen, and a line saying *"pings reach claude immediately"* to a session that is not the master is false | 2026-09-29 | [§3.6](#36-which-session-a-ping-wakes-the-master), [§3.5](#35-failure-paths) | — |
| <a id="EW-D33"></a>[`EW-D33`](#14-decision-ledger) | *Implementation decision, carrying out [OQ-EW7](#OQ-EW7)'s ruling (B); replaces [EW-D23](#EW-D23) for a sidecar the workspace declares.* **A repository's own sidecars are acknowledged in the config-change prompt's sidecar section, and recorded as the approval record's third part.** The section is a labeled block after the repository scope's and before the config diff. It lists in full each sidecar the workspace's own config declares that a `y` would newly acknowledge (new, changed, or not yet acknowledged on this machine): its name, the file it came from, every field of the resolved declaration, its whole command line, never cut short, what this launch will do with it, and a line saying the approval binds the declaration, not a script its `cmd` names. It names the rest in one line, and the header and the question name the sidecars whenever it is shown. The part is `<container-name>.sidecars.json` beside the snapshot and the scope part, in [BB-D30](boundary-broker.md#BB-D30)'s shape and in play only where the workspace declares a sidecar. It holds per sidecar the hash of exactly what the section showed, the time and this machine's id ([EW-D15](#EW-D15)). A `y`, `--accept-config-changes` or host `yolo check --accept-config-changes` records every part together, and an `N` or a refusal records none. A launch that the flag approves prints the section anyway. A sidecar missing from the part, hashed differently or recorded on another machine is a change, so a new machine asks once, with the section alone when nothing else changed. The composition compares each declaration it starts with the part, so an edit after the `y` starts nothing. Every path that deletes the approval record deletes the part (`cleanupCaptureWorkspace` removes the other two today). On a host with no readable machine id the `y` acknowledges no sidecar and the section says why; a launcher inside a jail records one with no id, as EW-D15 allows there. There is no third answer, "the config but not its sidecars", since the ruling joined into one `y` the two answers option A kept apart; a veto in `yolo-jail.local.jsonc` or `yolo sidecar disable` keeps one off. Forced because rendering the section and recording the part from one loaded config in one process is what makes "what is acknowledged is what was shown" hold without EW-D23's separate print-and-ask. A separate file, because the snapshot's bytes are a frozen contract, and a part in play only where its subject is leaves every other workspace's record as it was, as the scope part does | 2026-09-30 | [§12.4](#124-a-sidecar-a-repository-declares) | — |
| <a id="EW-D34"></a>[`EW-D34`](#14-decision-ledger) | *Implementation decision, reconciling [OQ-EW7](#OQ-EW7) (B) with [OQ-EW10](#OQ-EW10).* **For a sidecar the workspace's own config declares, the config approval's `y` is its turn-on as well as its acknowledgement, and nothing else turns it on.** `yolo sidecar enable` at the host only lifts that workspace's `disable` for it and acknowledges nothing, `--all-workspaces` never covers it, and in-jail the command refuses it and names the config-change prompt. Precedence for it, first match wins: a config veto; this workspace's off record; its acknowledgement in the sidecar part; otherwise off. Forced three ways: B's answer says *"the 'y' starts them, with no separate `yolo sidecar enable`"*; the `y` is an explicit act at the host, on this machine, recorded where no synced dotfile reaches, which is all [OQ-EW10](#OQ-EW10) asks of a turn-on; and [OQ-EW3](#OQ-EW3) ruled the user's own acknowledgement, while a repository can instruct its agent through its `AGENTS.md`, so an in-jail turn-on standing in for the `y` would be the repository starting its own sidecar, the case [OQ-EW3](#OQ-EW3) named | 2026-09-30 | [§12.4](#124-a-sidecar-a-repository-declares) | — |
| <a id="EW-D35"></a>[`EW-D35`](#14-decision-ledger) | *Implementation decision, carrying out [OQ-EW10](#OQ-EW10)'s in-jail turn-on.* **A jail turn-on is an entry in a file in the jail's own per-workspace home, bound to this machine and this workspace by the turn-on tag, and the host only reads it.** The file is under the jail's `~/.local/state/`, so on the host it is beneath `<workspace>/.yolo/home`, wherever the backend's layout puts `~/.local`. An entry holds the sidecar's name and source as the sidecar view names them, the time, the tag, and for a fetched pack's sidecar the declaration's hash and the pack's locked commit as the view lists them. The tag is a keyed hash, under a fixed application-specific key, of [EW-D15](#EW-D15)'s machine id together with the workspace's container name, never the id itself, as [machine-id(5)](https://man7.org/linux/man-pages/man5/machine-id.5.html) advises. Each fresh launch writes it into the sidecar view, a file rather than an environment variable, which an agent's tool shell may scrub. The host reads the file beneath an `os.Root` on `.yolo/home` with `paths.ReadRegularFileBeneath`, capped and as data; an unreadable or malformed file is no turn-on. It honors an entry only when the tag matches the one it computes now, so a file synced to another machine, copied to another path or committed into a repository starts nothing, and the launch says so; `.yolo/` ignores itself, so a clone carries none. On a host with no readable machine id there is no tag: the in-jail command refuses and no jail turn-on is honored. A launcher inside a jail keys the tag with no id, as EW-D15 allows there. The host never copies an entry into its own store. Forced because the jail cannot see the host's store, which no jail mounts ([EW-D14](#EW-D14)), and can read no machine id, so only a value a launch hands it can make its word mean this machine. Binding at write time, rather than having the host adopt an unbound request at the next launch, is what stops a replicator carrying the file first, and it keeps EW-D14's one writer. Not `cache/`, which every jail writes; not the box, which ends with its keeper; not the jail's own yolo store, which the in-jail launcher trusts as a host's for nested workspaces ([EW-D17](#EW-D17)) | 2026-09-30 | [§12.9](#129-turning-one-on-from-inside-the-jail) | — |
| <a id="EW-D36"></a>[`EW-D36`](#14-decision-ledger) | *Implementation decision.* **In-jail, `yolo sidecar enable` turns on only an agent-side sidecar that user config, the local pack or a selected pack declares, for the jail's own workspace.** It resolves the name through the sidecar view and refuses one the view does not list. It refuses a host-side sidecar and names the host command ([OQ-EW5](#OQ-EW5)), and refuses one the workspace's own config declares and names the config-change prompt ([EW-D34](#EW-D34)). `--all-workspaces` keeps EW-D17's nested meaning: the jail's own store, for workspaces nested in this jail, never this jail's own. It asks nothing: an agent's tool shell has no terminal, and a confirmation would stop nothing the agent could not do by running the same command as its own background task (Test 1), as [EW-D29](#EW-D29) argued for a claim. A fetched pack's sidecar is included, bound to its hash and locked commit, so a pack update stops it as it stops a host record; `packs` is user scope only ([`packs.go`](../../internal/config/packs.go)), so every pack a jail turn-on reaches is one the user's config selected. Forced by the ruling's words, *"something running inside the jail"*, which cover every agent-side sidecar the user's own config brings in and no host-side one | 2026-09-30 | [§12.9](#129-turning-one-on-from-inside-the-jail) | — |
| <a id="EW-D37"></a>[`EW-D37`](#14-decision-ledger) | *Implementation decision.* **A jail turn-on ranks below every host record, and the jail can withdraw only its own.** For a sidecar from user config or a pack, first match wins: a config veto; this workspace's host record, on or off; the machine-wide host record, on or off; a jail turn-on honored at this launch; otherwise off. In-jail `yolo sidecar disable` removes the jail's own entry; where a host record, the config approval or a veto governs, it writes nothing, exits non-zero, and names what governs, the veto the agent may write itself, and the host command. In-jail `enable` where a host record governs records nothing and names that record. No host command writes the jail's file. Forced because a `disable` record must stay the off switch the agent cannot touch ([§12.1](#121-declaring-is-not-enabling)), and a machine-wide off must still mean a machine that never watches, which a turn-on from any jail on it would otherwise undo. The ruling lets the agent turn a sidecar on, not overrule the user's own word at the host, and a fixed order needs no clock to settle which of two writers spoke last | 2026-09-30 | [§12.9](#129-turning-one-on-from-inside-the-jail) | — |
| <a id="EW-D38"></a>[`EW-D38`](#14-decision-ledger) | *Implementation decision.* **A jail turn-on takes effect at the next launch that creates the container, and counts only at a container backend.** The supervisor's whole input is `YOLO_JAIL_DAEMONS`, read once when it starts (`Main` in [`supervisorcmd.go`](../../internal/supervisor/supervisorcmd.go)), so a running jail gains no agent-side sidecar. Every option of [OQ-EW13](#OQ-EW13) already leaves an agent-side sidecar in a container to the next boot, so this holds whichever way it is ruled and decides nothing of it. The command says when it will start, and the agent can meanwhile run the command as its own background task. At `yolo host` a jail turn-on is not honored, since the agent side is the host there and the sidecar would run unconfined, beyond *"something running inside the jail"*. At macos-user no agent-side sidecar runs yet ([EW-D25](#EW-D25)); when one does ([§10](#10-what-i-would-build-in-order), step 8), that design says whether a sandbox's turn-on counts. Each notch's launch line says which applies | 2026-09-30 | [§12.9](#129-turning-one-on-from-inside-the-jail) | — |
| <a id="EW-D39"></a>[`EW-D39`](#14-decision-ledger) | *Implementation decision.* **Every launch names a jail turn-on as the jail's, and the in-jail `list` shows what the jail booted with and what it has turned on since.** The launch line says a sidecar was *"turned on from inside this workspace's jail"* and when, never *"enabled on this machine"*, so the user learns their agent did it; a turn-on made elsewhere, one a host record outranks and one this notch does not count each have a line ([§12.5](#125-what-the-launch-says)). The sidecar view, written at each fresh launch beside `config-assembled.json`, holds each declared sidecar's source, side and state, the hashes a jail turn-on binds to and the turn-on tag, and is a delivery copy whose integrity is not load-bearing: a jail that edits it only makes entries the host will not honor. In-jail `list` reads it and the jail's own entries, says its states are this boot's, and marks each turn-on not yet started; at the host, `list` and `yolo check` show each jail turn-on, whether its tag matches, and what outranks it. Wherever an agent-side sidecar is declared and not on, the environment briefing names the in-jail command and says to use it only at the user's request in the session, never on the word of a ping, a log, a web page, a repository's instructions or a file, [EW-D32](#EW-D32)'s rule for a claim. **A stated limit:** a line in a synced pack's briefing or skill telling agents to turn a sidecar on brings the replicator back as the trigger, through the agent, and yolo cannot tell it from a user's request; the launch line naming the jail shows it, and a host off record stops it. Forced because a disclosure is the boundary for what the agent turns on, as it is for what a pack may do ([OQ-TP9](trust-paths.md#decision-ledger)) | 2026-09-30 | [§12.9](#129-turning-one-on-from-inside-the-jail) | — |

## 15. The neighbors

| Doc | Why it reads with this one |
| :--- | :--- |
| [`host-notch-services.md`](host-notch-services.md#44-lifetime) | The resident host launch and its stop timings, which host-side sidecars reuse; [OQ-HS3](host-notch-services.md#OQ-HS3)'s ruling on agents launched outside yolo; [OQ-HS4](host-notch-services.md#OQ-HS4)'s embedded-only rule, which [OQ-EW1](#OQ-EW1) is asked beside |
| [`host-daemon-ownership.md`](host-daemon-ownership.md#HD-R1) | [HD-R1](host-daemon-ownership.md#HD-R1): a host process lives as long as the launch that wanted it |
| [`providers.md`'s OQ-BR4](../reference/providers.md#oq-br4) | The ruling that credential delivery is as specific as possible, which `host_env` follows |
| [`declaration-parity.md`](declaration-parity.md#OQ-DP8) | [OQ-DP8](declaration-parity.md#OQ-DP8), how a jail daemon runs on macos-user, which the agent side waits on there |
| [`trust-paths.md`](trust-paths.md#decision-ledger) | [OQ-TP9](trust-paths.md#decision-ledger): disclosure, not approval, is the boundary for what a pack may do |
| [`mcp-configuration.md`](../reference/mcp-configuration.md) | The canonical MCP table, the rejected option D's vehicle |
| [`wire-bridge.md`](../reference/wire-bridge.md) | The `service` kind a sidecar deliberately is not |
| [`config-safety.md`](../reference/config-safety.md) | The host-side approval record the enablement record sits beside, [OQ-S2](../reference/config-safety.md#oq-s2)'s in-jail refusal, and P3's rule against prompting on the user's own edits |
| [`gate-placement-principle.md`](../reference/gate-placement-principle.md) | Test 1 applied to the replicator, and why a declaration beats a prompt here |
| [`workspace-config-trust.md`](workspace-config-trust.md) | The iced `yolo trust` design: a host-side record keyed on workspace and content, and a refusal that names the verb, which the acknowledgement follows |
| [`boundary-broker.md`](boundary-broker.md#BB-D30) | [BB-D30](boundary-broker.md#BB-D30)'s second part of the approval record, and [BB-D19](boundary-broker.md#BB-D19)'s rule that the host reads a workspace's git state as text |
| [`loophole-system.md`](../reference/loophole-system.md#the-placement-rule) | The placement rule host-side sidecars obey, and R5, which sidecars' `enabled` deliberately does not follow |
| [`jail-lifetime-last-session-wins.md`](jail-lifetime-last-session-wins.md) | Several sessions in one jail, which [OQ-EW9](#OQ-EW9) is about; the keeper that [OQ-JL1](jail-lifetime-last-session-wins.md#OQ-JL1)'s direction made the owner of a shared jail's host services, which host-side sidecars and the box follow ([§3.2](#32-lifecycle-per-side-and-per-notch), [EW-D24](#EW-D24)); and [OQ-JL5](jail-lifetime-last-session-wins.md#OQ-JL5)'s ruling, whose keeper at `yolo host` and macos-user ([its §9.9](jail-lifetime-last-session-wins.md#99-the-keeper-at-yolo-host-and-macos-user)) replaces [EW-D19](#EW-D19) with [EW-D25](#EW-D25) |

## Appendix A — the evidence per agent

Gathered 2026-09-28 in this jail, read-only: binaries' strings, packages' docs and types,
`--help` output, and public docs. No agent session was started and no API was called. One probe
had a side effect: `codex --version`, run through yolo's lazy launcher, let the launcher update
codex from 0.156.1 to 0.158.0 under `~/.codex/packages/`, as that launcher does on any call.

### A.1 claude (2.1.284)

- **`asyncRewake`**, MEASURED in the settings schema embedded in
  `~/.local/share/claude/versions/2.1.284`: `asyncRewake: … "If true, hook runs in background and
  wakes the model on exit code 2 (blocking error). Implies async."`, beside `async`, `once`,
  `timeout` (*"Timeout in seconds for this specific command"*) and the `@internal`
  `rewakeMessage` and `rewakeSummary` (the latter *'Defaults to "Stop hook feedback"'*).
- **The inbox**, MEASURED: sockets at `/tmp/cc-socks/<n>.sock`, mode `srw-------`, one per live
  session. The auth line is required only on Windows by default (the log prints
  `auth line … "optional"` otherwise). The sender's PID is read from the kernel as provenance,
  not as authentication. The hold reasons, verbatim: *"The sending session's permission mode
  class doesn't match this session's. Review it below, or set "crossSessionInbound" to
  "accept"."* and *"The sender did not attest its permission mode and this session bypasses
  prompts."* `crossSessionInbound` takes `accept`, `hold` or `refuse`; a repository or managed
  setting may only tighten it. No sender allowlist exists.
- **Channels**, MEASURED: `channelsEnabled`, *"Managed-org opt-in for channel notifications (MCP
  servers with the claude/channel capability pushing inbound messages). Teams/Enterprise: default
  off; Console: default on."*
- **Monitor**, MEASURED from this session's tool schema: each stdout line of a watched command
  becomes a notification; every monitor expires after at most 30 minutes and is re-armed by the
  agent.
- **yolo's claude pack** renders no Claude Code hook and no messaging setting today (MEASURED,
  [`packs/claude/pack.json`](../../packs/claude/pack.json)). The maintainer's pack renders
  `Stop`, `StopFailure` and `Notification` hooks through a `config-overlay` on `claude/settings`
  (MEASURED, `/ctx/packs/matt/pack.json`).

### A.2 pi (0.87.1)

All MEASURED under `@earendil-works/pi-coding-agent` in the npm prefix.

- `sendMessage(message, options?: {triggerTurn?: boolean; deliverAs?: "steer" | "followUp" |
  "nextTurn"})` and `sendUserMessage(content, options?: {deliverAs?: "steer" | "followUp"})`,
  the latter documented *"Always triggers a turn"* (`dist/core/extensions/types.d.ts:1045-1058`).
- In `dist/core/agent-session.js` (around lines 1481-1518): on an idle agent `triggerTurn: true`
  starts a turn and `false` appends without one; on a streaming agent `followUp` waits for the
  turn to end and the default steers. `sendUserMessage` while streaming with no `deliverAs`
  throws.
- `examples/extensions/file-trigger.ts`: *"Watches a trigger file and injects its contents into
  the conversation. Useful for external systems to send messages to the agent."* It calls
  `fs.watch` in `session_start` and `sendMessage(…, {triggerTurn: true})`.
- `docs/extensions.md` requires processes, sockets, watchers and timers to start in
  `session_start` and close in an idempotent `session_shutdown`, and says a reload replaces the
  extension runtime.
- No built-in listener: nothing in `dist` calls `createServer` or `.listen`. `--mode rpc` drives
  a pi the caller spawned; it cannot attach to a running TUI.
- yolo's pi pack ships two extensions today through `files` entries into
  `.pi/agent/extensions/` ([`packs/pi/pack.json`](../../packs/pi/pack.json)).

### A.3 omp

Not installed here. Its pack installs `@oh-labs/oh-omp` and ships `yolo-footer.js`, whose comment
says omp runs extensions *"with the same extension API pi has"*
([`packs/omp/extensions/yolo-footer.js`](../../packs/omp/extensions/yolo-footer.js)). The npm
package carries no docs. INFERRED only.

### A.4 codex (0.158.0)

- `codex queue --thread <uuid|name> --message <text>`, *"Queue a message for an existing
  session"* (MEASURED, `--help`; SOURCED, [openai/codex#39092](https://github.com/openai/codex/pull/39092)),
  sent as `thread/queue/add` to the shared app-server daemon on `app-server-control.sock`. The
  TUI uses that daemon by default and falls back to an embedded server that `queue` cannot reach
  (MEASURED, strings). The daemon also offers `turn/start`, `turn/steer`, `thread/inject_items`
  and `thread/loaded/list` (MEASURED, a schema generated from the binary). Since
  [OQ-CDX1](../research/codex-background-service.md#OQ-CDX1) was ruled on 2026-09-29, yolo turns
  that daemon off wherever it launches Codex, so `queue` has nothing to reach in a yolo launch.
- Whether a queued message starts a turn on an idle thread is unsettled:
  [openai/codex#49081](https://github.com/openai/codex/issues/49081), filed 2026-09-28, reports
  queued messages not sent after a turn ends.
- Hooks: only `SessionStart`, `UserPromptSubmit` and `Stop` inject context, all on the agent's own
  lifecycle (SOURCED, [learn.chatgpt.com/docs/hooks](https://learn.chatgpt.com/docs/hooks)).
  `notify` fires on `agent-turn-complete` and is outbound only (MEASURED).

### A.5 copilot (1.0.48), opencode (1.18.32)

- **copilot**, MEASURED in the package's bundled SDK docs: extensions under the user extensions
  directory or `.github/extensions/<name>/extension.mjs` run as long-lived children of the CLI,
  load by default, call `joinSession()` for *"the user's current foreground session"*, and
  `session.send()` *"programmatically inject[s] a new user message"*. Config-file hooks inject
  context only on `sessionStart` (SOURCED,
  [hooks configuration](https://docs.github.com/en/copilot/reference/hooks-configuration)). A
  hidden `--ui-server` embeds a JSON-RPC server in the TUI (MEASURED).
- **opencode**: the TUI opens a reachable HTTP port only with `--port`, `--hostname` or mDNS;
  otherwise it talks to an in-process worker (MEASURED in the binary). With a port, `POST
  /session/{id}/prompt_async`, `/tui/append-prompt` and `/tui/submit-prompt` exist (MEASURED;
  SOURCED, [server](https://opencode.ai/docs/server)). Plugins receive `client`, and
  `client.session.prompt` sends to a session (SOURCED, [plugins](https://opencode.ai/docs/plugins),
  [SDK](https://opencode.ai/docs/sdk)).

### A.6 agy (1.2.9)

MEASURED from `--help` and agy's embedded docs: `agy agentapi send-message <recipient_id>
<content>` exists, and its strings suggest it needs `ANTIGRAVITY_LS_ADDRESS` and
`ANTIGRAVITY_CSRF_TOKEN`. Hooks in `hooks.json` include `PreInvocation`, which may return
`injectSteps` with a `userMessage`; hooks are synchronous with a 30-second default timeout. agy
has its own `sidecars/<id>/sidecar.json` with a restart policy and a cron schedule; whether the
CLI rather than the desktop app runs them is INFERRED.
