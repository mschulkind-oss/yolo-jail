---
title: "Pack brokers: a third-party pack brokers its own service through core's grants, approvals and dry run"
date: 2026-11-04
status: draft
stage: DESIGN
next: "Rule OQ-BX1 to OQ-BX6, starting with OQ-BX1 (who executes an allowed call) and OQ-BX3 (where the CircleCI pack lives)"
depends-on:
  - boundary-broker.md
  - github-write-scopes.md
  - access-introspection.md
  - mcp-auth-sharing.md
tags: [design, broker, packs, extension-point, approvals, grants, working-set, circleci, linear, graphql, mcp]
summary: "The git and GitHub brokers are built into core, so a pack that wants the same grants, working set, requests, yolo approve, yolo wait, yolo audit and on_request machinery for another service has to change yolo. This design splits a broker into what a pack knows (how to read a call: its kind, its resource, which lookups it needs) and what core owns (the jail-facing front, the decision, the store, the approvals, the audit, execution with the host's login, and the dry run). A pack declares cells and resource types and ships a classifier; core does everything else. Two proofs: CircleCI through its own CLI, and Linear's GraphQL API as the hard HTTP case. The MCP sign-in broker shares the lower layer (lifecycle, declarations, fences, audit, a readout line) and stays outside the call layer, because it brokers no calls."
---

# Pack brokers: a third-party pack brokers its own service through core's grants, approvals and dry run

**Status:** 2026-11-04. Nothing built. Evidence read at `3ccc28def` on 2026-11-04; CircleCI's CLI
and API facts are from its published documentation and changelog of that date, not run. No `yolo`
or agent CLI was run.

> **In short.** A pack knows how to read a call; only core may decide one. So the extension point
> is a **classifier** the pack ships, and everything that grants, files, approves, executes or
> predicts stays in core, where a jail and a buggy pack alike cannot reach it.

**Why it matters.** A downstream user built their own broker for several internal services
because yolo's approvals only exist for git and GitHub. Each such broker re-implements the store,
the menu, the wait and the audit, and none of them is covered by
[`yolo access`](access-introspection.md).

**The shape.** Two layers. The **host-broker framework** (lifecycle, declarations, fences, audit,
a readout line) is shared by every host daemon that holds a credential for a jail, the MCP sign-in
broker included. The **call layer** adds, for a pack that brokers calls, core's front, decision,
store, approvals and executors around the pack's classifier.

**Cost.** `yolo approve`, `yolo wait` and the config validator stop hard-listing the git and
GitHub sources. One new manifest block, one classifier protocol, two core executors, an in-jail
HTTP front, and a conformance kit.

**Start at [§5](#5-two-layers)**, then [§6](#6-the-decision-is-cores).

**Needs your ruling:** [OQ-BX1](#OQ-BX1), [OQ-BX2](#OQ-BX2), [OQ-BX3](#OQ-BX3),
[OQ-BX4](#OQ-BX4), [OQ-BX5](#OQ-BX5), [OQ-BX6](#OQ-BX6).

**Reads with:** [`access-introspection.md`](access-introspection.md) (the readout and the dry run;
its decision function is the one every call broker uses),
[`pack-brokers-plan.md`](pack-brokers-plan.md) (the implementation plan's seed; not a hand-off),
[`mcp-auth-sharing.md`](mcp-auth-sharing.md) (the MCP sign-in broker; see
[§13](#13-coherence-with-the-mcp-sign-in-broker)).

---

## 1. The problem

The owner asked for a full extension point: a pack that is not yolo's, with no change to yolo
core, uses the grant, working-set, request, `yolo approve`, `yolo wait`, `yolo audit` and
`on_request` machinery as the git and GitHub brokers do, and the dry run covers it. The proof is
CircleCI, and on its form the owner said:

> *"If CCI has a CLI tool, I'd rather use that and build that, and then find another service for
> the HTTP example."*

### 1.1 Requirements

| # | Requirement | From |
| :--- | :--- | :--- |
| R1 | A third-party pack brokers a service with no change to yolo's code | the owner |
| R2 | It uses the same grants, cells, working set, requests, `yolo approve`, `yolo wait`, `yolo audit`, `on_request` and gate as git and GitHub | the owner |
| R3 | `yolo access` reads and predicts its calls with the same guarantee as git's and GitHub's | the owner; [access-introspection R6](access-introspection.md#11-requirements) |
| R4 | One extension point serves a CLI-wrapping broker and an HTTP-wrapping broker | the owner's two proofs |
| R5 | The CircleCI proof wraps CircleCI's CLI; where the CLI falls short the design says so, and never silently falls back to the HTTP API | the owner |
| R6 | The HTTP proof is a widely used public service, harder than CircleCI | the owner |
| R7 | One host-broker framework, not two: the MCP sign-in broker either fits it or this doc says exactly why not | the coordinator |
| R8 | Every refusal names its next step | [`happy-path-principle.md`](../reference/happy-path-principle.md) |

### 1.2 What a downstream user's broker asks for

A downstream user's broker, built outside yolo for internal HTTP and CLI services, is evidence of
what a pack would want to build. Each of its features, generalized, and where it lands:

| Feature | Lands | Why |
| :--- | :--- | :--- |
| Brokered HTTP and CLI calls to services yolo does not know | **Extension point** | This design |
| Per-call human approval for any such service | **Core** | Approvals are core's ([§6](#6-the-decision-is-cores)) |
| Time-windowed approvals | **Core**, `yolo approve --for`, capped by a per-cell maximum the pack declares | One store, one clock |
| Approvals that follow a conversation | **Out of scope** | No grant is scoped to a session ([BB-D83](boundary-broker.md#BB-D83)) |
| Previewing a batch of writes and approving them together | **Core, partly**: [OQ-AX1](access-introspection.md#decision-ledger) lets an agent file its requests before acting; a "request changes" answer is [OQ-BX5](#OQ-BX5) | The preview is the dry run |
| Draft-only writes and author attribution | **Out of scope** here | A source's own policy; GitHub's lives in [`github-write-scopes.md`](github-write-scopes.md) |
| Desktop and chat notifications | **Core's `on_request` hook**; anything richer out of tree | The hook already serves any source |
| A web console of requests | **Out of tree**, on the CLI's stable surface | [`boundary-broker.md` §1.5](boundary-broker.md#15-the-maintainers-path-2026-10-23) |
| Work streams and a review inbox | **Out of scope** | Not an access question |
| Labelling calls with the session that made them | **Core**: the host-asserted jail identity is already on every call | The jail cannot label itself |
| MCP servers behind OAuth | **The MCP sign-in broker** ([§13](#13-coherence-with-the-mcp-sign-in-broker)) | Already designed |
| Object-store commands approved one by one | **Extension point**, a pack executor ([§8.3](#83-the-pack-executor)) | Its signing is not a header |
| Endpoint files kept outside per-launch storage; a keeper that restarts a dead daemon | **Not this design**; a backlog item for the host-daemon owners | Daemon plumbing, not access |

## 2. Terms

Every term is *(coined here)* unless it links elsewhere.

| Term | Meaning | What it is not |
| :--- | :--- | :--- |
| **host-broker framework** | What yolo gives every host daemon that holds a credential for a jail: lifecycle, pack declarations, fences, audit, the readout line ([§5.1](#51-the-host-broker-framework)) | Not a daemon; not the call layer |
| **call broker** | A framework daemon that forwards the jail's calls to a service with the host's credential and decides each one: git, GitHub, and every pack broker of this design | Not the MCP sign-in broker, which hands out a token and forwards no call |
| **pack broker** | A call broker a pack declares through this design's `call_broker` block | Not the git or GitHub broker, which core implements today |
| **front** | The jail-side end of a call broker: an intercepted CLI (**CLI front**) or a loopback HTTP endpoint (**HTTP front**) | Not the loopback-TLS front of [`loophole-transport.md`](../reference/loophole-transport.md), which carries the front's frames to the host |
| **classifier** | The pack's program that turns one call into a **classification**: its cell, its resource, its canonical form, and the lookups it needs ([§7.2](#72-the-classifier)) | Not the decision; it never sees standing or a credential |
| **canonical form** | The exact argv or request bytes core executes, produced by the classifier | Not the jail's bytes, which are never sent |
| **lookup** | A read the classifier asks core to run with the host's credential to learn a fact, such as which project a workflow belongs to | The same thing [`access-introspection.md`](access-introspection.md#2-terms) calls a **fact read**, seen from the classifier's side |
| **resource type** | A kind of thing a cell acts on (a pipeline, a workflow, an issue), with its parent type, declared by the pack | Not a cell |
| **scope root** | A resource the human approved at the gate as in scope for a workspace: a repository for a source rooted in git remotes, or a declared root such as a team | Not a grant: it says where calls may go, not which run |
| **executor** | What runs an allowed call with the host's credential: a **core executor** (CLI or HTTP), or the **pack executor** | Not the classifier |

Existing terms used as defined elsewhere: **cell**, **ceiling**, **ask**, **working set**
([`github-write-scopes.md`](github-write-scopes.md#1-the-cells)); **request**, **grant**, **timed
grant**, **Allow once** ([`boundary-broker.md` §1.3](boundary-broker.md#13-terms)); **prediction**,
**act mode**, **evaluate mode**, **decision function** ([`access-introspection.md`](access-introspection.md#2-terms)).

## 3. What exists today

| Fact | Where |
| :--- | :--- |
| A pack loophole's host daemon runs one per jail, started and ended with it, behind the loopback-TLS front, and learns the caller's jail from the connection preamble | [HD-R1](host-daemon-ownership.md#HD-R1), [`loophole-transport.md`](../reference/loophole-transport.md) |
| A third-party pack may ship a host program by URL and checksum; an official pack may not, its programs being yolo subcommands | [OQ-BP8](../reference/pack-binaries.md#OQ-BP8) |
| The `brokered` block declares a source, its forge host, its credential paths (fenced from `mounts`) and its grant kinds; core puts the scope in front of the human at the config-change gate and writes the approved scope to a per-launch file | `internal/loopholedecl` (`brokered.go`) |
| Generic loophole grant kinds, the user's ceiling `[allow.<pack>]` and the project's `[requests.<pack>]` already work for any pack loophole, but grant only per kind, not per call | [`loophole-grants.md`](../reference/loophole-grants.md) |
| The store, working set, limits and audit are written without a service name baked in: the source is a field | `internal/brokerstore`, `internal/brokeraudit` (`Service`) |
| `yolo approve` names the GitHub and git stores in its code and revalidates through each source's own package | `internal/cli/approve.go` (`ApprovalStore()`) |
| The `on_request` hook is notify-only and source-neutral | `internal/brokerhook` |
| A pack's `intercept` contribution puts a forwarder on the jail's PATH ahead of the real program, as the github pack does for `gh` | `packs/github/pack.toml` |
| [`loophole-grants.md` §8](../reference/loophole-grants.md#8-what-this-does-not-license) states the trust model: yolo does not enforce anything on a pack it does not ship | — |

**The gap.** Everything a pack needs exists except the seam: the decision, approvals and execution
are inside `internal/ghbroker` and `internal/gitbroker`, and `yolo approve` knows those two by name.

## 4. Principles

- <a id="BX-P1"></a>**BX-P1. The pack reads; core decides.** No protocol message lets a pack's
  program grant, file, answer, spend or widen anything.
- <a id="BX-P2"></a>**BX-P2. What was classified is what is sent.** Core executes the canonical
  form the classifier returned, never the jail's bytes, so a parser difference between the
  classifier and the service cannot smuggle a second call.
- <a id="BX-P3"></a>**BX-P3. The classifier holds no credential.** Its lookups go through core's
  executor under the read grant, audited like any read.
- <a id="BX-P4"></a>**BX-P4. Allowlist.** An argv word, flag, route or operation the classifier
  does not know is refused ([BB-P2](boundary-broker.md#BB-P2)).
- <a id="BX-P5"></a>**BX-P5. What a pack declares, the gate shows.** Cells, their shipped defaults,
  scope roots and any read-over-a-write-method exception appear in the config-change gate before a
  human's `y`.
- <a id="BX-P6"></a>**BX-P6. One framework.** Every host daemon holding a credential for a jail
  shares one lifecycle, one declaration shape, one fence, one audit log and one readout line,
  whether or not it brokers calls.

## 5. Two layers

```mermaid
flowchart TB
  subgraph L1[host-broker framework]
    LC[lifecycle: one daemon per jail, HD-R1]
    DE[declarations: needs, one fence, launch_input, scope]
    AU[audit: one log, service field]
    RO[readout: op status]
  end
  subgraph L2[call layer]
    FR[front: CLI or HTTP]
    CL[pack classifier]
    DF[decision function]
    ST[store, approve, wait, hook, history]
    EX[executor]
  end
  MCP[MCP sign-in broker] --> L1
  GH[git and GitHub brokers] --> L1
  GH --> L2
  PB[pack brokers: CircleCI, Linear] --> L1
  PB --> L2
```

### 5.1 The host-broker framework

Every host daemon that holds a host credential for a jail gets, from core:

- **Lifecycle.** Spawned per jail by the launch, detached at the jail's end
  ([HD-R1](host-daemon-ownership.md#HD-R1)), its endpoint published through the loophole transport.
- **Declarations.** The pack `needs` it; its scope, if it has one, goes through the gate.
- **One fence.** Every selected host-daemon loophole's state directory is fenced from `mounts`,
  read-only sources included: a source that is, contains or lies inside it is refused from
  workspace config and disclosed from user config. A loophole's `credential_paths` are added on top
  of that fence, never instead of it. Today nothing refuses a read-only `mounts` entry under
  `~/.local/share/yolo-jail/state/` unless a brokered loophole is selected, which leaves the Claude
  and OpenAI brokers' stored logins exposed too; the MCP sign-in plan's
  [ID-9](mcp-auth-sharing-plan.md#id-9) builds this fence, and it is the framework's.
- **One per-launch input.** What a launch computes for a daemon and hands it as a file reaches it
  through the `launch_input` key of `[host_daemon]` and its `{launch_input}` token, whose producers
  are a closed, core-owned set ([ID-4](mcp-auth-sharing-plan.md#id-4)). A call broker's approved
  scope roots are one more producer of that set, `call-broker-scope`; the `brokered` block's
  `{repository_scope}` stays as it is, and folding it into `launch_input` is one change that renames
  both, never a third mechanism.
- **Audit.** It writes the one host audit log with its `service`, read by `yolo audit --service`.
  `server` and `generation` are framework audit fields beside `service`, `event`, the
  host-asserted jail and the workspace: optional, set by the daemons whose records have them (the
  MCP sign-in broker), and never a token.
- **A readout line.** It answers the framework's readout request, `{"op":"status"}`, over its own
  endpoint, with whatever it holds for this workspace and never a secret; that answer is the
  daemon's line in [`access-introspection.md` §6](access-introspection.md#6-the-access-readout).
  A call broker answers the same request, and its answer is its whole readout.
- **Backend reach.** On a backend whose jail cannot reach host daemons (Apple Container today,
  except the daemons it admits by name), its readout line says unreachable and names the reason.
  The MCP sign-in broker shows that line on Apple Container until the MCP plan's phase C admits its
  daemon there.

### 5.2 The call layer

A call broker adds, all in core:

- The **front** in the jail, which forwards a call's bytes and nothing else.
- The **pipeline** of [`access-introspection.md` §5](access-introspection.md#5-one-pipeline-two-modes)
  in act and evaluate mode, with the pack's classifier as its classify step.
- The **store**, `yolo approve`, `yolo wait`, the `on_request` hook, `yolo history`, limits.
- The **executor** that runs the canonical form with the host's credential.

A pack broker's daemon *is* core's: the `call_broker` block replaces `host_daemon` for that
loophole, and the launch runs `yolo internal daemon call-broker` for it with the pack's
declaration. The pack ships only its classifier, and optionally a pack executor.

## 6. The decision is core's

The decision function is [`access-introspection.md` §5.2](access-introspection.md#52-the-decision-function),
the same ordered rules git and GitHub use. For a pack broker its inputs come from:

| Input | From |
| :--- | :--- |
| Refused classes | The classifier's `refused` answer, and core's own (an unknown cell, an undeclared resource type, an out-of-scope root) |
| Read grant | The pack's `read` standing, as for any grant-declaring loophole |
| Standing read | A classification whose cell is `read` |
| Cell values | `[requests.<pack>.working_set]` and `[requests.<pack>.all]`, merged as GitHub's are, under the ceiling `[allow.<pack>]`, with the invariant `working_set` ≥ `all` ([`github-write-scopes.md` §2](github-write-scopes.md#2-the-ceiling-and-the-ask)) |
| Working set | Entries of this source, matched against the resource **or any of its parents** in the classification's chain |
| Timed grants, Allow once | Core's store, bound to the command digest of the canonical form |
| Limits | Core's store |

A call that carries several writes, such as a GraphQL document with two mutations, is decided per
write, as a git push is per ref: it runs when every write runs, is refused when any is refused, and
otherwise asks once for the whole call.

**Revalidation in `yolo approve`** is source-neutral: core re-checks the stored classification's
scope root and the workspace's standing, as GitHub's `ApprovalAuthority` does today, and the act
that spends the approval classifies the call again from scratch. A classification that changed
between filing and spending no longer matches the stored digest, so it asks again.

## 7. What a pack declares

### 7.1 The block

```toml
# packs/circleci/loopholes/circleci-broker/loophole.toml (sketch; spellings are the plan's)
[call_broker]
source = "circleci"
front = "cli"                      # or "http"
program = "circleci"               # CLI front: the host program; intercepted in the jail
classifier = "bin/circleci-classify"   # from the pack's `binaries`
executor = "core"                  # or "pack" (OQ-BX1)
credential_paths = ["~/.config/circleci"]
env_from_host = ["CIRCLE_TOKEN", "DBUS_SESSION_BUS_ADDRESS"]

[call_broker.scope]
roots = "remotes"                  # or "declared"
remote_host = "github.com"
root_template = "gh/{owner}/{repo}"

[[call_broker.resource]]
type = "pipeline"
parent = "project"
[[call_broker.resource]]
type = "workflow"
parent = "pipeline"

[[call_broker.cell]]
name = "rerun_workflows"
tables = ["working_set", "all"]
default = { working_set = true }   # shown at the gate (OQ-BX6)
max_for = "8h"

[[call_broker.joins_working_set]]
cell = "trigger_pipelines"         # a pipeline the agent triggered joins
[[call_broker.joins_working_set]]
follows = { source = "git", fact = "branch" }   # so does one built from a branch in git's working set

[[call_broker.lookup]]
name = "workflow-project"
cli = ["workflow", "get", "{id}", "--json"]
```

Core validates the block at load, as it validates `brokered`: an unknown key is refused, a cell
name must not be `read`, every resource's parent must be declared, and a `joins_working_set` rule
naming another source names one the workspace selects.

**Scope roots** come in two forms:

- `remotes`: the workspace's git remotes on `remote_host`, mapped through `root_template`, exactly
  as the `brokered` block's scope is read today, plus `brokered.<source>.repos` widening.
- `declared`: roots the workspace lists in `brokered.<source>.roots`, such as `team:ENG`, shown in
  the gate's scope block like a widened repository.

### 7.2 The classifier

A program the pack ships, run by core as a subprocess per call with an empty environment beyond
`PATH`, an empty working directory, no TTY, a 2 s deadline and a 64 KiB answer cap. It is given
JSON on stdin and answers JSON on stdout:

| Given | Answers one of |
| :--- | :--- |
| The front, the call (argv and a stdin digest; or method, path, query, an allowlisted set of header names and values, body), the scope roots, and the results of any lookups it asked for earlier in this call | `lookups`: named lookups to run, with arguments; at most three rounds per call |
| | `classification`: the canonical form, and for each write in it: the cell, the resource with its parent chain up to a scope root, and a one-line description in the user's terms |
| | `refused`: a class, the reason, and the next step the pack knows |
| | `local`: the call is not the service's and runs in the jail as is (a CLI front's `--help`, `version`) |

It must be deterministic: the same input gives the same answer. Core enforces the deadline, the
cap, the round limit and the schema; the conformance kit checks determinism ([§19](#19-what-done-looks-like)).
A crash, a timeout or an answer that fails the schema refuses the call, naming the pack and its
version, and is audited as `classifier-failed`.

### 7.3 Lookups

A lookup is declared by the pack as a template, CLI argv or HTTP request, and run by core's
executor, never by the classifier. It must classify as a standing read under the pack's own
classifier; core checks this when the block loads, against the conformance fixtures, and again at
run time. A lookup that fails is a fact the classifier is given, and it usually answers `refused`
or classifies the write as asking.

### 7.4 Working-set rules

A resource joins the source's working set when:

1. a call in a `joins_working_set` cell creates it; the executor's output names the created id
   through a pack-declared extraction, given to the classifier as the last lookup round; or
2. a lookup's fact names a resource of another source's working set, as a `follows` rule declares:
   a CircleCI pipeline built from a branch the git broker's working set holds; or
3. a human hands it over with `yolo request <pack> working-set <resource>`, as GitHub's hand-over
   works.

Entries leave the set as GitHub's do. The cross-source rule reads only the other source's working
set for this workspace, never its grants.

## 8. What core runs

### 8.1 The CLI executor

Runs the host program the block names, as the host user, with the canonical argv, stdin as the
jail sent it (its digest is in the classification), an empty working directory, no TTY, and an
environment of `PATH`, `HOME` and the block's `env_from_host` taken from the host daemon's own
environment, never the jail's. The host program uses the host's own login, as `gh` does. Stdout,
stderr and the exit status cross back, through core's redactor when the pack declares a readable
credential value (an env var or a file key); a keyring-only login has no redaction backstop, and
the readout says so.

### 8.2 The HTTP executor

Sends the canonical request to the one host the block pins, with the credential header built from
the declared source (an env var or a 0600 file under a fenced path), and:

- follows no redirect: a 3xx crosses back as is, so a credential never reaches another host;
- sends no cookie and keeps none;
- caps the response at 32 MiB and streams it;
- refuses a credential found in a URL or query string in either direction;
- redacts the credential from the response.

**The method floor.** A non-safe method (anything but GET, HEAD, OPTIONS) can be classified `read`
only on a path the block declares `read_over_post`, which the gate shows. GraphQL needs it; a REST
API never should.

### 8.3 The pack executor

For a credential core cannot apply as a header: a request signature over the body (an object
store's), a token minted per call, or a CLI that must run in a prepared directory. Core spawns the
pack's executor only after the decision says the call runs, gives it the canonical form on stdin,
and relays what it prints. The pack's executor holds the credential; core still owns everything
before it and audits the result. Whether core ships both executor kinds is
[OQ-BX1](#OQ-BX1).

## 9. The trust boundary

| Against | Holds because |
| :--- | :--- |
| **The jail forging an approval** | The store is host-only; the jail-readable approval records only mislead the agent that forges them |
| **The jail widening its reach** | Scope roots and cells reach a decision only through the gate; the jail sends call bytes and nothing else |
| **The jail smuggling a second call** | Core sends the canonical form ([BX-P2](#BX-P2)) |
| **The jail redirecting the credential** | The executors take no host, server, token or config argument from the jail; the classifier refuses any flag or header that names one |
| **A buggy classifier** | Schema, deadline, cap, round limit, the method floor, and lookups that must classify as reads; an unknown cell or resource type is refused by core |
| **A pack lying in its declarations** | The gate shows every cell, default, root form and floor exception before a `y` |

**Not defended: a malicious pack.** A pack's programs run as the host user, so one that wants to
can read the host's login or run `yolo approve` itself. That is the trust model
[`loophole-grants.md` §8](../reference/loophole-grants.md#8-what-this-does-not-license) already
states, and the pack exec banner at launch is where the human accepts it. Two cheap tripwires
catch a careless pack rather than a hostile one: no protocol verb decides, and `yolo approve`
refuses when its own process ancestry includes a call-broker daemon or a classifier, naming the
pack.

## 10. CLI and HTTP: what they share and where they differ

| | CLI front | HTTP front |
| :--- | :--- | :--- |
| In the jail | `intercept` puts a forwarder named for the program ahead of it on PATH | A loopback HTTP listener run by `yolo-jaild`; the pack's `env` names its URL in the variable the client reads, such as a base-URL setting |
| Identity, scope, decision, store, approvals, audit, dry run | Shared | Shared |
| What the classifier reads | argv and the stdin digest | method, path, query, allowlisted headers, body |
| Canonical form | argv | request bytes |
| Lookups | argv templates | request templates |
| Credential | The host program's own login | A header core builds from a declared source |
| Executor hazards | Host file writes by flag, a server or token flag, spawned helper programs, git pushes | Redirects, cookies, credentials in URLs, response size, non-safe methods that read |
| A refusal reaches the agent as | Exit 64 and the refusal text on stderr, as `gh`'s | Status 403 with headers `Yolo-Verdict` and `Yolo-Request`, and a body in the error shape the block declares (GraphQL's `errors` array for Linear), whose message is the refusal text |
| An ask reaches the agent as | Exit 75 and the menu | Status 403, `Yolo-Verdict: asks`, the menu in the message |

## 11. Proof one: CircleCI, through its CLI

### 11.1 Why the CLI works here

CircleCI's current CLI (v1, a Go rewrite on API v3, generally available August 2026) has a verb
for everything an agent needs: listing and reading runs, workflows, jobs, test results and
pipelines; triggering, cancelling, rerunning and approving; reading and setting project
environment variables and contexts. The host's `circleci auth login` stores the token in the
host's keyring, and the host CLI reads it itself, so the jail never sees a token and core injects
nothing, as with `gh`.

### 11.2 Classification

| Command | Cell | Resource | Default |
| :--- | :--- | :--- | :--- |
| `run list/get/watch`, `workflow list/get`, `job get/output`, `job artifact` (listing), `testresult list/get`, `pipeline list`, `envvar list` (values masked), `context list/get`, `context secret list` (names only) | `read` | project, or the item's project by lookup | runs |
| `run trigger`, `pipeline run` | `trigger_pipelines` (joins the working set) | project | asks |
| `workflow rerun` (with or without `--from-failed`) | `rerun_workflows` | workflow → pipeline → project | working set: runs ([OQ-BX6](#OQ-BX6)); otherwise asks |
| `run cancel`, `workflow cancel` | `cancel` | pipeline or workflow | working set: runs ([OQ-BX6](#OQ-BX6)); otherwise asks |
| `run approve` (a hold job) | `approve_holds` | job → workflow → pipeline → project | asks, even in the working set: a hold is often a deploy gate |
| `envvar set/delete` | `project_envvars` | project | asks |
| `context create/delete`, `context secret set/delete` | `contexts` | the organization, by lookup; `all` table only | asks |
| `pipeline create`, `project` and `policy` writes | `project_settings` | project | asks |

A workflow, job or pipeline id is resolved to its project by a lookup (`workflow get`, `job get`)
before any write; the target must root in a scope root. A project named `circleci/<org>/<project>`
rather than by its VCS slug is resolved to its repository by lookup; one that has none is refused,
naming `brokered.circleci.repos`.

### 11.3 Refused, with the next step

| Command | Why | Next step the refusal names |
| :--- | :--- | :--- |
| `api <path>` | A raw passthrough to every API version: it would bypass every cell | the named verb, if v1 has one; otherwise none through the broker |
| `auth …`, `setting …`, a server flag, `CIRCLE_HOST` | Prints, moves or redirects the credential | the human's host login |
| `env subst` | Can print an injected token | — |
| `extension …`, `mcp …`, `completion install`, `update` | Runs or installs host programs, or serves on the host | run it on the host |
| `preflight run` | Pushes a branch | `git push`, through the git broker |
| `runner token create`, `runner config` | Prints a runner token | the host |
| `orb publish`, `namespace delete` | Irreversible and organization-wide | the host |
| `config generate`, `migrate actions`, `project link`, any `-o`/`--out` flag | Writes host files | the same command without the flag, where output can stream |

### 11.4 Where the CLI falls short

Said plainly, with no HTTP fallback behind any of them:

1. **Cloud only.** v1 speaks API v3, which CircleCI Server does not serve. A Server user's v0 CLI
   has a different grammar this pack does not classify; the daemon refuses to start against a host
   other than CircleCI's cloud, and the readout says so.
2. **No passthrough.** Anything only `circleci api` reaches is unavailable through the broker
   until v1 gains a verb for it.
3. **Files.** A command whose only output is a host file is refused; artifact download is out
   until the plan confirms it can stream to stdout.
4. **A local file input.** `config validate` reads a path; the broker never reads a jail-named
   path on the host, so it is refused until the plan confirms the CLI reads stdin.
5. **The keyring.** A host daemon started without a session bus cannot read a keyring login; the
   read fails, and the refusal names `circleci auth login --insecure-storage` or a host
   `CIRCLE_TOKEN` as the human's choice.
6. **No redaction backstop** for a keyring login, since core cannot read the token to look for it.
7. **A new CLI release's new flags** are refused until the classifier learns them
   ([BX-P4](#BX-P4)); yolo pins no CLI version.

## 12. Proof two: Linear, over HTTP

### 12.1 Why Linear

Linear's public API is GraphQL only: every call is a POST to one path, so the method and path say
nothing. Reading a call means parsing the document, choosing the operation, and naming each
mutation field; the resources sit in variables, often as identifiers like `ENG-123` that need a
lookup; one request can carry several mutations under aliases; there is no official CLI. That
defeats every declarative route table, which is why it proves the classifier.

**The close alternative is Slack's Web API** ([OQ-BX4](#OQ-BX4)): its method is in the path and
classifies by table, but its targets are in form bodies, some methods return tokens, and its
workspace and token types are many. It would test the HTTP executor harder and the classifier
less.

### 12.2 How it classifies

- **Operation.** A `query` is `read`; a `mutation` is decided per top-level field; a
  `subscription` is refused. A document with more than one operation and no `operationName` is
  refused; with one, the canonical form is that operation alone, re-printed.
- **Cells.** `issueCreate` is `create_issues` (joins the working set); `issueUpdate`,
  `issueArchive` are `edit_issues`; `commentCreate` is `comment_issues`; `issueDelete` and anything
  named `*Delete` asks in every table; `fileUpload` is `attach_files`; an unknown mutation is
  refused ([BX-P4](#BX-P4)).
- **Resources.** An issue id or identifier resolves by lookup to its team; the team must be a
  declared scope root (`team:ENG`).
- **The method floor.** The block declares `read_over_post = ["/graphql"]`; the gate line reads
  *Linear's reads arrive as POST; the pack's classifier decides which are reads.*
- **Uploads.** `fileUpload` returns a signed upload URL on another host. The URL crosses to the
  jail, which uploads directly; no credential is involved, and the storage host is not brokered.
- **Rate limits** are by query complexity and are Linear's: a prediction says yolo's verdict only.
- **The credential.** A Linear API key in a 0600 file under a fenced path, sent as the
  `Authorization` header. A missing file refuses every call, naming the file to create.

## 13. Coherence with the MCP sign-in broker

[`mcp-auth-sharing.md`](mcp-auth-sharing.md) is accepted and unbuilt. Its broker uses the
host-broker framework ([§5.1](#51-the-host-broker-framework)) and sits outside the call layer, for
three reasons: it forwards no call, so there is nothing to classify or decide; it files no request,
so approvals do not apply; and it deliberately hands an access token to the jail, the opposite of a
call broker, whose credential never crosses. Every touch point:

| Touch point | The MCP design | This design |
| :--- | :--- | :--- |
| Names | The pack `mcp-auth`, its loophole `mcp-auth-broker`, the verb `yolo mcp-auth` | The same names, everywhere this design and the readout use them |
| Lifecycle | `yolo internal daemon mcp-auth-broker`, one per jail, [HD-R1](host-daemon-ownership.md#HD-R1) | The framework's lifecycle, unchanged |
| Declarations | The `mcp-auth` pack, `needs`ed by every agent pack declaring `[[mcp_auth]]` | The framework's `needs`; no `call_broker` block |
| Per-launch input | The share file, through `launch_input = "mcp-auth-share"` and `{launch_input}` ([ID-4](mcp-auth-sharing-plan.md#id-4)) | The framework's one per-launch input mechanism; a call broker's scope is another producer of it |
| Fence | A host-only canonical store under the loophole's state directory ([ID-9](mcp-auth-sharing-plan.md#id-9)) | The framework's fence is every selected host daemon's state directory, with `credential_paths` on top |
| Audit | `service = "mcp-auth"`, events `adopt`, `refresh`, `sign-out`, `dead`, `seed`, `harvest`, `header`, with `server` and `generation` ([ID-10](mcp-auth-sharing-plan.md#id-10)) | `server` and `generation` are framework audit fields; never a token |
| Readout line | `{"op":"status"}` over its endpoint, rendering `yolo.mcp-auth.status/1`, the same object `yolo mcp-auth status` prints ([ID-8](mcp-auth-sharing-plan.md#id-8)) | `{"op":"status"}` is the framework's readout request; that object is the daemon's line in `yolo access` |
| Identity | `yolo mcp-auth header` answers by host-asserted identity and refuses a server the launch did not compose | The framework's identity; the refusal names its next step |
| Apple Container | The jail cannot reach the broker until phase C admits it; store entries are written at launch instead | The readout shows the framework's unreachable line there until phase C |
| Dry run | — | None: it decides no call ([access-introspection non-goals](access-introspection.md#10-non-goals)); the readout lists what it holds |
| Approvals, `yolo approve`, `yolo wait`, `yolo history` | Files no request | Never lists its records: they are sign-in records, not requests |

**A future MCP call broker**, one that keeps a server's token on the host and forwards each tool
call for approval, would be a pack broker with an HTTP front: a JSON-RPC `tools/call` classified by
its tool name, as Slack's methods would be. Nothing here builds it.

## 14. Failure paths

| Failure | What happens |
| :--- | :--- |
| The classifier is missing, fails its checksum, crashes or times out | Refused, naming the pack and its version; `yolo check` reports the pack; audited |
| A lookup fails | The classifier is told; the call is refused or asks, as it would in evaluate mode |
| The host program is missing | Refused, naming its install as the human's host step |
| The host login is missing or expired | The executor's failure crosses back, plus the pack-declared login hint as the next step |
| Two calls at once | Each runs its own pipeline; the store's lock serializes filing and spending, as for GitHub |
| A pack is removed while requests are pending | They expire on the store's clock; `yolo approve` lists them with the pack shown as removed and refuses to approve them |
| A pack upgrade changes a classification | A pending request's digest no longer matches; it asks again |
| The jail sends an `Authorization` header to the HTTP front | It is dropped, and the response says so in a header |

## 15. Non-goals

- **No change to the git and GitHub brokers' rules.** They move onto the shared decision function
  through [`access-introspection.md`](access-introspection.md); they keep their own front and
  classifier.
- **No declarative classifier language.** See [OQ-BX2](#OQ-BX2).
- **No host login verb.** A pack's credential is the host program's login or a declared file.
- **No defense against a malicious pack** ([§9](#9-the-trust-boundary)).
- **No CircleCI Server.**

## 16. Alternatives considered

| Alternative | Verdict |
| :--- | :--- |
| **The pack ships the whole daemon** and calls core's store as a library | Rejected: the pack would decide, and a bug would file or spend wrongly; [BX-P1](#BX-P1) |
| **A plugin loaded into yolo's process** | Rejected: Go plugins are fragile across builds, and it runs pack code with yolo's own state open |
| **CircleCI over API v2 instead of its CLI** | Rejected by the owner's direction; the CLI covers the agent's needs |
| **One generic HTTP broker with a route table and no classifier** | Rejected as the only form: GraphQL defeats it. A route table may be a classifier the pack writes |
| **The classifier gets the credential and does its own lookups** | Rejected: [BX-P3](#BX-P3); lookups would not be audited reads |

## 17. Risks

| Risk | Mitigation |
| :--- | :--- |
| A classifier misreads a write as a read | The method floor; lookups must classify as reads; the conformance kit's fixtures; the audit shows every read |
| The classifier's latency on every call | One subprocess per call with a 2 s deadline; the plan measures it, and a long-lived classifier is a later change if it bites |
| A pack's shipped working-set defaults are too generous | The gate shows them ([BX-P5](#BX-P5)), and [OQ-BX6](#OQ-BX6) rules whether they may exist |
| CircleCI's CLI moves fast | Unknown words are refused, not passed |

## 18. What I would build, in order

1. **Source-neutral approvals.** `yolo approve`, `yolo wait` and the config validator read sources
   from the selected packs' declarations instead of naming git and GitHub. No behavior change.
2. **The framework's `{"op":"status"}` readout request** and its audit fields for every host
   daemon. The fence and `launch_input` arrive with the MCP sign-in plan; this design reuses them.
3. **The `call_broker` block, the classifier protocol and the CLI executor**, with the CircleCI
   pack as the first user.
4. **The HTTP front and executor**, with the Linear pack.
5. **The pack executor**, if [OQ-BX1](#OQ-BX1) rules for it.
6. **The conformance kit.**

## 19. What done looks like

- With the CircleCI pack selected and `circleci auth login` done on the host, `circleci run list`
  in the jail lists runs; `circleci workflow rerun <id>` on a workflow of a pipeline built from the
  agent's pushed branch runs unasked; the same on another branch's workflow asks, with a menu that
  `yolo approve r-<id>` answers.
- `yolo access -- circleci run approve <job>` predicts `asks`, naming `approve_holds`.
- `circleci api /me` is refused, naming the named verb.
- A Linear `issueUpdate` on an issue of an out-of-scope team is refused, naming
  `brokered.linear.roots`.
- `yolo audit --service circleci` lists every call, read or write, refused included.
- **The conformance kit** *(coined here)*: a command a pack author runs against their pack that
  feeds its fixtures through the classifier twice (determinism), through act and evaluate mode
  (same verdict), checks each lookup classifies as a read, and checks the block loads. A pack
  broker is done when the kit passes.

## Open Questions

1. 💬 **OQ-BX1: Who runs an allowed call: core, the pack, or either?**

   A pack author wraps a service whose CLI uses the host login (CircleCI) and another whose API
   needs each request signed (an object store). Core can run the first and send a plain header for
   a token API; it cannot sign.

   - **A — Core executors only.** The object store cannot be brokered.
   - **B — The pack's executor only.** Every pack writes its own execution and redaction.
   - **C — Both.** Core executors by default; a pack executor only where core's cannot apply.

   <!-- vantage: question id=OQ-BX1 leaning="C — core's executors carry the redirect and redaction rules most packs would get wrong, and the pack executor covers what core cannot sign." -->

   _Leaning:_ C — core's executors carry the redirect and redaction rules most packs would get wrong, and the pack executor covers what core cannot sign.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-BX2: Is the classifier a program, or a declaration core interprets?**

   A pack author writes the CircleCI classifier. As a program, they write Go (or anything) and
   ship it with a checksum. As a declaration, they write a table of argv patterns in the
   manifest, and core interprets it; GraphQL would still need a program.

   - **A — A program.** One shape for every service.
   - **B — A declaration**, with a program allowed when the declaration cannot express it.

   <!-- vantage: question id=OQ-BX2 leaning="A — GraphQL forces the program anyway, and two shapes double what the kit has to check; a pack can still generate its program from a table." -->

   _Leaning:_ A — GraphQL forces the program anyway, and two shapes double what the kit has to check; a pack can still generate its program from a table.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 **OQ-BX3: Where does the CircleCI pack live?**

   The proof must show a pack that is not yolo's. In `packs/`, it would be official, and official
   packs ship no binaries ([OQ-BP8](../reference/pack-binaries.md#OQ-BP8)), so its classifier would
   be a yolo subcommand: a change to yolo. In its own repository, it ships its classifier by URL
   and checksum, as any third-party pack does.

   - **A — `packs/circleci`**, its classifier a yolo subcommand.
   - **B — Its own repository**, added with `yolo pack add <url>`.

   <!-- vantage: question id=OQ-BX3 leaning="B — only a pack outside the tree proves the extension point needs no change to yolo." -->

   _Leaning:_ B — only a pack outside the tree proves the extension point needs no change to yolo.

   **Answer:**
   > _(empty — fill in when decided)_

4. 💬 **OQ-BX4: Which service is the HTTP proof?**

   The HTTP proof should be the hardest common case. Linear's GraphQL hides every write inside one
   POST, so classifying it needs a parser and lookups. Slack's Web API names its method in the path,
   which a table reads, but carries targets in form bodies and returns tokens from some methods.

   - **A — Linear.** Proves the classifier.
   - **B — Slack.** Proves the HTTP executor's guards.

   <!-- vantage: question id=OQ-BX4 leaning="A — a design whose classifier handles GraphQL handles a method table too; the reverse is not true." -->

   _Leaning:_ A — a design whose classifier handles GraphQL handles a method table too; the reverse is not true.

   **Answer:**
   > _(empty — fill in when decided)_

5. 💬 **OQ-BX5: May a human deny with a note the agent reads?**

   An agent asks to trigger a pipeline on the wrong branch. Today the human can only deny, and the
   agent learns nothing but "denied". With a note, `yolo approve r-<id> --deny --note "use the
   release branch"` and `yolo wait` prints the note.

   - **A — Deny only**, as today.
   - **B — An optional note on deny**, shown by `yolo wait` and `yolo history`, for every source.

   <!-- vantage: question id=OQ-BX5 leaning="B — it turns a dead end into a next step, for git and GitHub too." -->

   _Leaning:_ B — it turns a dead end into a next step, for git and GitHub too.

   **Answer:**
   > _(empty — fill in when decided)_

6. 💬 **OQ-BX6: May a third-party pack ship working-set cells that run unasked by default?**

   GitHub ships `working_set.comment_*` and `edit_*` as `true`, so an agent comments on its own PR
   without asking. The CircleCI pack would like `rerun_workflows` and `cancel` on the agent's own
   pipelines to run the same way. The human would see them in the gate before their first `y`.

   - **A — No.** A third-party pack's cells all start at ask; the human adds `true` lines.
   - **B — Yes, shown at the gate** with a line per default cell, and capped by the user's ceiling.

   <!-- vantage: question id=OQ-BX6 leaning="B — the gate shows the defaults and the ceiling still bounds them, which is the rule official packs already follow." -->

   _Leaning:_ B — the gate shows the defaults and the ceiling still bounds them, which is the rule official packs already follow.

   **Answer:**
   > _(empty — fill in when decided)_

## Decision Ledger

Designer decisions taken while drafting; none is a ruling of the owner's.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| BX-D1 | Two layers: the host-broker framework for every credential daemon, and the call layer for brokers of calls | 2026-11-04 | [§5](#5-two-layers) | — |
| BX-D2 | A pack broker's daemon is core's; the pack ships a classifier and optionally an executor | 2026-11-04 | [§5.2](#52-the-call-layer) | — |
| BX-D3 | Core executes the classifier's canonical form, never the jail's bytes | 2026-11-04 | [§4](#4-principles) | — |
| BX-D4 | Lookups run through core's executor and must classify as reads | 2026-11-04 | [§7.3](#73-lookups) | — |
| BX-D5 | Scope roots are the workspace's remotes or roots declared in the workspace config, both through the gate | 2026-11-04 | [§7.1](#71-the-block) | — |
| BX-D6 | A resource may join a source's working set by following another source's working set, as declared | 2026-11-04 | [§7.4](#74-working-set-rules) | — |
| BX-D7 | A call with several writes is decided per write; it asks once for the whole call | 2026-11-04 | [§6](#6-the-decision-is-cores) | — |
| BX-D8 | The HTTP executor follows no redirect, and a non-safe method reads only on a declared, gate-shown path | 2026-11-04 | [§8.2](#82-the-http-executor) | — |
| BX-D9 | The MCP sign-in broker uses the framework and not the call layer | 2026-11-04 | [§13](#13-coherence-with-the-mcp-sign-in-broker) | — |
| BX-D10 | CircleCI's `api` passthrough is refused; the CLI's shortfalls are stated and not filled from the HTTP API | 2026-11-04 | [§11.4](#114-where-the-cli-falls-short) | — |
| BX-D11 | The framework's fence is every selected host-daemon loophole's state directory, read-only `mounts` included; `credential_paths` adds to it. Adopted from the MCP sign-in plan's ID-9 | 2026-11-04 | [§5.1](#51-the-host-broker-framework) | — |
| BX-D12 | One per-launch input mechanism: `launch_input` and `{launch_input}`, with a closed core-owned producer set; a call broker's scope is a producer of it. Renaming either is one change that renames both | 2026-11-04 | [§5.1](#51-the-host-broker-framework) | — |
| BX-D13 | `{"op":"status"}` is the framework's readout request, for credential daemons and call brokers alike | 2026-11-04 | [§5.1](#51-the-host-broker-framework) | — |
| BX-D14 | `server` and `generation` are framework audit fields, optional per daemon | 2026-11-04 | [§5.1](#51-the-host-broker-framework) | — |
| BX-D15 | On Apple Container the MCP sign-in broker's readout line is the framework's unreachable line until the MCP plan's phase C admits it | 2026-11-04 | [§13](#13-coherence-with-the-mcp-sign-in-broker) | — |
| BX-D16 | Names are the MCP design's (`mcp-auth`, `mcp-auth-broker`, `yolo mcp-auth`); its records are never listed by `yolo approve`, `yolo wait` or `yolo history` | 2026-11-04 | [§13](#13-coherence-with-the-mcp-sign-in-broker) | — |
