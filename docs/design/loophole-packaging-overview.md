# Loopholes in packs — the system-level picture

**Status:** DESIGN, 2026-08-13. Not built. This is the readable half of
[`loophole-packaging.md`](loophole-packaging.md), written to be commented on.

**What this doc is for.** The detailed design is written for whoever implements it — it cites line
numbers, names functions, and argues from measured code. That is the right shape for building and
the wrong shape for deciding. This doc carries the same design at the level where the choices
actually live: what we are adding, what it costs, what it opens, and the decisions it needs from
you. **Where the two disagree the detailed doc is the authority** — but if a comment here changes
something, the change lands there too.

**The one-line version.** A pack can ship agents, skills, config, files and mounts. It cannot ship a
**loophole** — the one kind of thing that runs a process on your host. This adds that, and
essentially the whole design is about what must be true before a pack you fetched from a git URL may
run code on your machine.

---

## 1. Why this got acute

A loophole is how something on the host reaches into the jail: the audio socket pass-through, the
Claude OAuth broker, the host-process view. It is the only extension point that puts a process on
your real machine — which is what makes it the most useful one and the one that has never been
distributable.

There are three ways a loophole gets onto a machine, and only one was ever open to a third party:

| Channel | Who it is for | State |
|---|---|---|
| bundled in the yolo binary | yolo's own three | fine — **but see §1.1** |
| a hand-placed directory in your home | one local loophole, yours | works; no fetch, no version, no approval, no manifest travelling with the code — **and see §1.1** |
| the `loopholes` block in a config file | **the only third-party path** | **degraded, and now dead** |

The transport unification retired the old `unix-socket` transport outright rather than deprecating
it, on the principle that a value which still validates is a value someone will use. Third-party
daemons were all on that transport, because the surviving one (loopback-TLS) requires the daemon to
publish an authenticated endpoint file, and nothing yolo ships lets a non-Go program do that. So the
only third-party channel now points at a transport that no longer exists.

Meanwhile every host service on the roadmap — the crossing audit log, a git credential proxy,
approval-gated credentials — **is** a loophole. And `pack-capabilities.md` was being written to let
a pack *cancel* a loophole while there was still no way to *ship* one. That asymmetry is what raised
the question in the first place.

### 1.1 Do we need three channels at all? — raised in review, and it is the better question

The table above is the *status quo*, and reviewing it surfaced the structural objection: **once a
pack can carry a loophole, what are the other two channels for?**

This repo has already made exactly this move once, and the result is the headline sentence of
`AGENTS.md`: **"AGENTS ARE PACKS. Core does not know what an agent is."** There is no agent
registry, no `agents` config key, no special case — the six agents yolo ships are ordinary packs
that happen to live in the binary, selected by bare name. A loophole registry (`bundled_loopholes/`)
plus a magic home directory plus a config block is the world *before* that move.

So the honest position is that the first draft argued the wrong thing. It defended `bundled` as
*"the channel for the things yolo itself is accountable for"* — but accountability is a property of
**who wrote it**, and an official pack already carries exactly that property. Two of its three
supporting reasons turn out to be about the specific loopholes rather than about the channel:

| Draft's reason to stay bundled | Holds up? |
|---|---|
| the broker **auto-activates** — bundled + `requires` *is* the default-on mechanism, and opt-in would silently reintroduce the token race | **Partly.** It is a real requirement, but an *official* pack could carry an implicit-selection bit. That mechanism already exists for the conventional local pack. |
| the broker's manifest **is not what runs** — its real spawn is reconstructed in Go, and its per-jail relay has no manifest vocabulary at all | **Yes, and this is the strongest one.** Making it a pack would be ceremony over a thing that does not obey the manifest. |
| `host-processes`' client is a **binary baked into the image** | **No.** An official pack is embedded in the binary too — a baked client is fine for one. |

And the home directory is the weaker of the two. Its stated justification is that *a directory you
placed carries your own authority* — which is **the same sentence** that justifies trusting a
`file://` pack. Two mechanisms, one argument. Worse, it is the one channel that activates a host
daemon with **no selection step at all**: drop a directory in, and it is discovered. That is a
direct contradiction of *"nothing is active by default"* (§5), sitting in the design as a legacy.

There is one non-obvious payoff to retiring it, and it resolves a question this design otherwise has
to answer: `yolo loopholes enable/disable` **only works for user-directory loopholes today** — every
other source is refused with an instruction to go edit a config file. Retire the directory and that
command has no special case left to serve, which forces the enable/disable state into config for all
sources — which the detailed design already calls the better end state and defers as a separate
decision.

**Both became questions in §8, and one is now settled: OQ-LP10** (retire the home directory) is
**ruled yes**, and **OQ-LP11** (bundled loopholes become official packs) is still open. Neither
blocks the 15th kind — the kind is what makes either one *possible*. The rest of this doc describes
the world with all three channels intact, because that is the world the design was written against.

---

## 2. The proposal

**A 15th contribution kind, `loophole`, which points at a directory rather than inlining anything:**

```jsonc
{ "kind": "loophole", "from": "loopholes/acme-proxy" }
```

That directory holds a `manifest.jsonc` — the exact on-disk shape a bundled or hand-placed loophole
already has. One loader reads all four sources, and an author can develop a loophole standalone,
prove it works, and drop it into a pack unchanged. Selecting the pack is what activates it;
deselecting is what stops it starting next launch.

Four things fall out of that choice, and they are the substance of the design:

1. **The daemon must not have to be Go — and the framework, not the loophole, owns the wire.** §3.
2. **A loophole must declare where it runs**, because a pack shipping a native binary is shipping
   something that exists for some platforms and backends and not others. §3.3.
3. **Everything the loophole reaches on your host must become something you were asked about.** §4.
4. **Turning it on must stay a decision you made.** §5.

---

## 3. The framework owns the wire

> **This section is rewritten from review.** The first version presented three options and picked
> one. The reviewer's framing is better and is now the design: **the transport is a property of the
> framework, not of the loophole.** A loophole never implements TLS, never opens a port, and never
> publishes a credential. It binds a plain Unix socket and yolo does the rest.

### 3.1 What "the front" is

The jail reaches a host loophole over **loopback TLS** — a TCP listener on `127.0.0.1`,
authenticated by a token minted per jail. That was settled by the transport unification and is not
reopened here; the relevant part is that it is the *only* way across, so it is a framework fact and
every loophole gets it whether or not its author has ever heard of it.

**The front is the small piece of yolo that provides it on a daemon's behalf:** it opens the TLS
listener, checks the token, publishes the endpoint file the jail reads, and then splices each
accepted connection to a plain Unix socket the daemon binds. The daemon sees an ordinary local
socket. It is not new code — it already runs in production for the OAuth broker's per-jail relay,
for the same reason from the other direction.

### 3.2 Why the author cannot be asked to do this themselves

Two tempting alternatives, and the one that looks most generous is the most restrictive:

| Option | What it means | Verdict |
|---|---|---|
| **Export the Go package** | third parties link yolo's server code | ❌ **narrows the author's language to exactly one.** The option that looks like opening it up closes it hardest |
| **Publish the wire spec** | third parties implement the server themselves | ⚠️ necessary to write down, but it cannot be the supported path |
| **The framework provides it** ✅ | the daemon binds a plain Unix socket; yolo owns the listener, the token and the endpoint file | **the only path for a pack-shipped loophole** |

**Why spec-only cannot be the supported path: an enforcement asymmetry.** On the *client* side a
sloppy implementation harms only itself. On the *server* side every security-critical property is
invisible to yolo — whether the endpoint file was written `0600`, whether the private key was
persisted, whether the token compare was constant-time, whether the frame length was capped before
allocating. yolo cannot detect a single violation of any of them.

That is tolerable for a config entry someone hand-wrote on their own machine. It is a different
proposition for a **distributed artifact**: spec-only means shipping other people's TLS-server and
credential-minting code to strangers' machines, where a `0644` publish is undetectable by the
framework and invisible to the user.

**So the design tightened in review: for a pack-shipped loophole, the front is mandatory.** The
manifest's `publishes` field still names which half the daemon does, but a *pack* may only say
`publishes: "socket"` — "yolo, wrap me." Self-publishing stays available for loopholes yolo itself
ships, which are yolo's own code. That converts the enforcement asymmetry from something the design
*documents* into something a third party cannot express: they can't get the TLS properties wrong
because they never write them. The cost is one splice hop for a Go-authored third-party daemon that
could have done it correctly — cheap, and reversible if anyone ever complains.

**What that buys the author:** anything that can bind a Unix socket and read a length prefix —
Python, Node, Rust, Go, a shell script with `socat`. The `nc`-era simplicity the protocol doc mourns
is restored on the server side. **And it is what revives the config block**: a hand-written config
daemon becomes "loopback-TLS, publishes a socket," which is *true of it*, needs no retired
vocabulary, and does not change its argv or its behaviour.

**One real cost — and to answer the review question directly: not implemented, not impossible.** The
front does not propagate the client's end-of-stream upstream, so a daemon that reads its request *to
EOF* works when tested against a bare socket and hangs forever behind the front.

That is a **deliberate constraint imposed by the one upstream that exists today**, not a property of
splicing. The front's own comment records the reasoning: the broker relay tears down *both* of its
sockets on either EOF (frozen parity behaviour), so closing the upstream's write side when the
request direction ends would cut short a response still in flight — which is exactly what a framed
client that writes once and then waits would suffer. So the front deliberately waits only on the
response direction and never signals EOF upstream.

A third-party daemon that reads to EOF has the **opposite** requirement, and the mechanism to serve
it is a few lines: half-close the upstream when the request direction ends. The two behaviours cannot
both be the default, so **it becomes a per-loophole declaration** — the manifest already has to say
`publishes: "socket"`, and it says alongside it how a request ends: framed (the default, today's
behaviour) or terminated by EOF. Presenting it as an inherent limit would have taught authors to
work around something that is one field away.

Until that field exists it is still a real trap, so it stays a named deliverable in the guide — a
behaviour change the author cannot see from the outside.

**And one thing the front cannot do, corrected from review.** The first version called the front
*"the natural home for the crossing audit log."* That overclaims: the front sees a byte stream, and
a loophole's protocol can be anything — frames, a raw stream, audio, video. **Generic per-request
logging is not possible there and the design should not imply it is.** What the front can honestly
record is *connection*-level: which loophole, when, by which jail, how much data. Per-request
logging remains a property of daemons that speak yolo's own framed protocol, and any richer audit
log is a per-loophole concern rather than a framework one.

### 3.3 A loophole must declare where it can run — raised in review

The framework owning the wire means the *transport* is portable. **What the daemon is made of is
not.** A pack shipping a compiled Linux binary, or one that talks to a Linux-only kernel interface,
must be able to say so — otherwise it is discovered on macOS and fails as a confusing runtime error
rather than an honest "not supported here."

Today's manifest can express *"the thing I need is present"* (a command on `PATH`, a file that
exists) but not *"I only exist for this platform."* Those are different statements and the second
one is missing. **Requirement: a loophole declares its supported platforms** — OS, and architecture
where it matters — and yolo reports by name when a selected pack's loophole is unsupported on this
machine, rather than skipping it silently or letting it fail obscurely.

**This is the same reporting gap the design already has on a second axis.** Two shipped backends
make a loophole inert regardless of platform: Apple Container skips host daemons entirely, and the
macOS no-VM backend never reaches loophole startup at all (§6). Platform and backend are two
questions with one answer shape — *this loophole does nothing here, and here is why* — so they
should be one mechanism and one message, not two.

**And it marks the extension point without designing it.** The reviewer's note: a genuinely native,
platform-specific transport — one that cannot work across all runtimes — is foreseeable but is not
being designed now. Declaring platform support is the field that makes such a loophole *expressible*
later without a schema break, which is precisely the "design the extension point, not the
implementation" rule from [`extension-point-principle.md`](extension-point-principle.md).

---

## 4. Trust: this is a widening, and the first draft got that wrong

The first draft argued the kind was a step **downward** — that the existing hole is wider than the
one we are opening. Review refuted it and **it is withdrawn**, for two reasons worth keeping visible
because the argument is tempting:

- It compares against a baseline **this same batch removes** (see G1 below, which ships first). By
  the time the kind lands there is no ungated host execution left to be downward from.
- Writing a config file requires an attacker to *already* be able to write on your machine. A
  fetched pack is a **distribution channel** that ships and re-ships the code itself. The repo
  already encodes that distinction; the draft argued as if it did not.

So: **it is a widening and it should be justified as one.** The justification is that the approval
machinery is exactly what this boundary was built for, that the enumeration of what crosses is now
total, and that the alternative — third-party loopholes stranded on a transport that no longer
validates — is not a safer world, only a poorer one.

### 4.1 The pre-existing hole, which is real and worth fixing regardless

**The `loopholes` block in a config file already runs an arbitrary host command, gated by nothing —
and a *workspace* config can write it.** No prompt, no lockfile, no origin check, no launch-time
notice. A successful spawn is silent and so is a failed one.

Three keys are user-scope-only today (`packs`, `host_files`, `cache_relocations`) for the stated
reason that *a workspace config is agent-editable*. `loopholes` is not one of them, and workspace
wins over user. Which means, concretely:

- An agent that can commit one line to a repo's `yolo-jail.jsonc` can run a host command at your
  next launch.
- It can also set environment variables on a **first-party** daemon's spawn — `LD_PRELOAD` into the
  broker — without declaring a command at all.
- And it can set `enabled: false` on the Claude OAuth broker, which drops it silently while `yolo
  check` still renders a green pass. That quietly reintroduces the single-use-refresh-token race the
  broker exists to prevent.

**This is finding-independent of the whole packaging question, and it should ship first.** It is
**OQ-LP2** below.

### 4.2 The four gates

| | What it does |
|---|---|
| **G1** | the config block's host-execution surface becomes **user-scope-only** — so an agent-editable file cannot reach it. **This costs per-workspace loopholes; see §4.3** |
| **G2** | everything a pack-shipped loophole touches on your host becomes an **approval claim** you saw at install time |
| **G3** | **origin still bounds it** — a pack you fetched needs approval; one in a directory you control does not; a missing or corrupt lockfile approves nothing |
| **G4** | the **per-launch disclosure** names the host access in effect, every launch, not just in a lockfile you have to go read |

All four are shipped machinery. Two of them had defects that changed the shape of the work:

**The claim enumeration was not total, and the gate short-circuits on an empty one.** This was the
worst defect in the draft. Claims were defined for the daemon and for TLS interception — and *not*
for bind mounts or device passthrough, which were listed under "everything else is allowed." Since
the origin gate returns "fine" when a pack claims nothing, a fetched pack shipping **only** bind
mounts produced zero claims, asked nothing, and got an arbitrary absolute host path into a jail
whose agent runs as root. `~/.ssh`, or `/`. There was no prompt at which you could have stopped it,
and the draft's own dogfood example was exactly that shape.

> **The load-bearing rule in the whole design: a claim-free loophole must be unrepresentable.**
> Every declaration that crosses the boundary emits its own approvable string — the daemon it runs,
> each host it intercepts, each path it mounts, each socket it connects you to, each device it
> passes through.

Two consequences worth knowing. **A host socket bind is its own claim class**, because a read-only
bind of a Unix socket is fully connectable and bidirectional — measured, twice, this is the
well-known `docker.sock:ro` result. So `:ro` is *not* a boundary for a socket, and the axis the
draft audited (read-only versus read-write) was the wrong one; the axis that matters is **path
scope**, and a pack-shipped loophole is now confined to the same home-relative namespace the `mount`
kind uses. And **host execution must read differently from a host read** in the prompt — the
existing model has one severity, and "runs code as you" cannot share a line with "reads a file."

**The approval is content-blind, and permanent.** The draft celebrated that a changed daemon
argument would re-prompt. True — but an attacker never needs to change the argument. Nothing ever
compares the commit; approval is a string match, and the field that records *what commit you
approved* is written and never read. So: approve `RUNS python3 acme-daemon.py` once; the author, or
whoever compromises that repo, rewrites the script entirely; the next install prints one yellow
"moved from abc → def" line and **no prompt**; the next launch runs the new code as you.

For a host **read** the claim string (a path) *is* the risk-bearing fact, which is why the existing
model works. For host **execution** the risk-bearing fact is file content, which no claim string can
carry. So an approval carrying an execution claim is anchored to the **commit**, not the string.
That has a real cost — a pack pinned to a moving branch re-prompts on every commit — which is
**OQ-LP8**.

### 4.3 What G1 costs: per-workspace loopholes stop being expressible — raised in review

The reviewer's question — *"meaning you can't declare a loophole in a workspace? then how do you
provision some workspaces with a priv and not others?"* — is the one the design should have answered
and did not. Taking it in two halves, because they are already different today:

**A pack-shipped loophole is already not per-workspace, and G1 has nothing to do with it.** The
`packs` key is user-scope-only *now*: the loader's own comment says workspace scope is
"inexpressible," and its file header gives the reason this design keeps repeating — a workspace
config travels with the repo and is agent-editable. So selecting a loophole-bearing pack for one repo
and not another is not possible before this design and not possible after it.

**A config-block loophole IS per-workspace today, and G1 removes exactly that.** A workspace
`yolo-jail.jsonc` can declare a `command` and get a host daemon for that repo alone. That is the
mechanism a reader would reach for, the shipped guide teaches it, and it works **because it is
ungated** — the same ungatedness §4.1 calls the pre-existing hole. So:

> **After G1 there is no per-workspace loophole mechanism at all.** That is not a migration cost, it
> is a capability removal with no replacement, and the design was calling it a migration.

Two shapes could give the capability back without giving it back to the agent. Both keep the rule
that *the human decides* and differ in where the decision is written:

| | How it works | Trade |
|---|---|---|
| **(a) User-scope declaration, workspace-selected** | the user config declares the loophole once and names which workspace paths get it | no new trust machinery; but new vocabulary, and the user config has to enumerate paths it otherwise knows nothing about |
| **(b) Workspace requests, human approves** ✅ | the workspace *asks*; approval is recorded host-side, keyed by (workspace, claims), and re-prompts when the ask widens | **the machinery already exists and is proven** — this is `yolo pack install`'s y/N-plus-lockfile with a different requester |

**My read is (b), and the objection to it needs answering rather than inheriting.**
`three-decisions.md` deleted a request/grant split for packs, and it would be easy to cite that as
settled. But read *why* it was deleted: *"a repo that wants to configure its agents already has a git
repo and can lay out whatever it likes in the workspace — it does not need a distribution mechanism
to reach files it already owns."* That reasoning is sound and **does not transfer**: a workspace
cannot "already lay out" host execution. The deletion covered a case where the request bought
nothing; here it buys the only thing that makes the capability safe. Recorded as **OQ-LP12**.

**And it changes G1's shipping story.** G1 is still right — an agent-editable file must not reach
host execution — but "warn for one release, then error" assumes users have somewhere to go. Under
(b) they do, and the warning can name it. Without a decision here, G1's migration is telling people
their working setup is now a config error with no supported replacement, which is a different and
worse message.

### 4.4 The gap all four gates miss: what runs is not what was approved — raised in review

Your question — *"the agent could change the loophole host binary if it lived in the workspace and
that certainly would not be noticed"* — is correct, is **not** introduced by this design, and is not
closed by any of the four gates. It is the most serious thing in this document.

**Every gate above governs a DECLARATION. None governs the FILE the declaration names.** G1 decides
who may write `command: ["python3", "/workspace/tool.py"]`. G2 records that string in a lockfile.
G3 asks who the pack came from. G4 prints it at launch. **Nothing looks at `tool.py`.** And
`/workspace` is bind-mounted live and agent-writable, so the one thing that actually executes on your
host is the one thing no gate reads.

The `file://` escape is worse than the doc said, and my earlier "leave it" read is **withdrawn**.
Local origin is a bare `file://` prefix check with **no constraint on the path whatsoever** — a
`file:///workspace/…` pack is "local", so it is trusted with no prompt, ever. The justification in
the code is explicitly an argument about *reading*: local content is *"the user's own files, which
they can already read."* That is sound for a host **read** and unsound for host **execution**, and
it collapses entirely when the "local" directory is one an agent writes.

**So this is one defect with three faces**, and seeing them as one is what makes it fixable:

| Where | What changes silently |
|---|---|
| a fetched pack at a moving ref | the daemon's file content; the argv never has to change (§4.2) |
| a `file://` pack under a workspace | the same, with **no approval at any point** to be stale |
| a config-block `command` naming a workspace path | the same, and **G1 does not touch it** — it gates the declaration, not the target |

**Your conclusion is the right one: everything a loophole runs on the host is promoted under manual
confirmation, anchored to CONTENT, regardless of origin.** Not "fetched packs re-prompt on a commit
bump" (§4.2's fix, which only covers the first row) but: yolo hashes what it is about to execute —
the loophole's module directory, plus any file named in the argv that lives outside it — records
that digest with the confirmation, and re-confirms when it changes. Origin then decides how loud the
first confirmation is, not whether there is one.

**Two things to be honest about rather than discover later.** The digest is not a security boundary
in the strong sense — a Python daemon can `import` anything and a binary can `dlopen` anything, so
what is hashed is *what yolo can name*, not *everything that will run*. Stating that scope is the
difference between a useful tripwire and a false promise. And the friction is real and worse than
§4.2's: a loophole under active development changes on every edit, so it re-confirms on every
launch. That wants a deliberate "I am developing this one" escape recorded in the **user** config —
a file the agent cannot write — rather than a prompt people learn to hit `y` on.

**This is OQ-LP13, and it outranks the rest.** It also changes what "ship G1 first" means: G1 remains
the biggest reduction in *who can declare* host execution, and it should still ship first, but it
should stop being described as closing the hole. It closes half of one.

### 4.5 The model in two verbs — RULED in review, and it resolves most of this document

> **Ruling:** *"loopholes can only be installed at the user level, and enabled at the workspace level
> or user level."*

That sentence settles more open questions than anything else here, so this section states the whole
model in one place — which is what the review asked for.

| | **INSTALL** | **ENABLE** |
|---|---|---|
| What it decides | that this code may run on your machine **at all** | whether an installed loophole is **active for this jail** |
| Scope | **user only** | **user or workspace** |
| Who can perform it | a human editing a file no agent can write | anyone who can edit the workspace config — including an agent |
| The gate | one confirmation, every origin — **what it checks is LP13**, still open (§4.4) | none needed |
| Cost of getting it wrong | arbitrary host execution | a vetted daemon runs for a repo you did not intend |

**Why the split is the right line.** The risk was never "a daemon runs" — it is "code you never
vetted runs". Install is where vetting happens, once, against content. Enable is a routing decision
*within an already-vetted set*, so it can safely live in a file an agent can write: the worst an
agent can do is switch on something you already looked at and approved.

**What "install" means concretely, and what has to change.** Today a loophole can arrive five ways
and only one of them asks you anything — `yolo pack install` prompts for **fetched** packs, while a
`file://` pack, the local pack, the home directory and the config block are all silent. Under
§1.1's consolidation those five collapse toward one (a pack named in the user config), and that act
*is* install. **So the change is: install becomes an explicit, confirmed act for every origin.**
Origin then decides how loud the first confirmation is, never whether there is one.

**What it does to the open questions:**

- **OQ-LP12 dissolves.** No request/grant machinery, and my "the workspace asks, you approve" proposal
  is withdrawn as more mechanism than the problem needs. You install once; each workspace enables what
  it wants from the set you vetted.
- **OQ-LP2 sharpens into the same split.** The config block's `command`, `env` and `doctor_cmd` are
  install-shaped, so user-only. `enabled` is enable-shaped, so both scopes.
- **OQ-LP3 dissolves.** `file://` stops being a trusted origin that skips the prompt; it becomes an
  origin whose first confirmation is quieter.
- **OQ-LP13 gets its home** — still open, but no longer a question about *where*: install is the
  confirmation point, so all that is left to decide is what it checks.

**One thing the design had decided the other way, stated plainly.** §4.2's G1 required `enabled` to be
user-scope-only for any daemon-bearing loophole, in **both** directions. This ruling overrides that:
`enabled` is available at workspace scope for everything. Two consequences, and the second is the one
to watch:

1. **ON** — a workspace can switch on any *installed* loophole. Bounded, because install already
   vetted the content, and that is the whole point of the split.
2. **OFF** — a workspace can still disable the broker silently, which is the failure §4.2 named. Scope
   no longer protects it, so **disclosure has to**: a launch-time line naming the loophole *and the
   file that disabled it*, and `yolo check` warning rather than rendering a green pass. Those were
   already requirements; under this ruling they stop being belt-and-braces and become the only
   protection.

**And the development escape hatch is DELETED.** I proposed a per-path "I am developing this one"
opt-out to blunt re-confirmation friction, and the review answer is better: **develop a loophole
inside a jail.** Checked it — the loophole runtime has exactly **one** jail-aware branch, the device
passthrough skip; host daemons, bind mounts, endpoint publication and intercepts all behave
identically when the launcher is itself in a jail. So a nested jail is a real development
environment, the daemon's "host" is a container you can throw away, and the re-confirmation friction
stays exactly where it should be: on the machine that matters. An escape hatch would have punched a
hole in the one gate that reads content, to serve a workflow that already had a better answer.

### 4.6 Things the design names rather than solves

- **A `file://` pack is trusted unconditionally and forever** — no approval, no re-approval. Not
  changing it: the origin model's whole claim is that a directory you control carries your own
  authority. It is the largest residual risk here, and it is **OQ-LP3**.
- **Selection controls activation, not revocation.** Deselecting a pack stops the *next* launch from
  starting its daemon. It does not stop a daemon that already ran — once arbitrary host execution
  has happened once, persistence is available by means yolo has no view of. No packaging design
  changes that, and this one does not claim to.
- **`yes | yolo pack install` grants approval.** A one-line hardening, independent of this design,
  worth doing in the same batch now that a `y` means "run this code."
- **Nothing reaps a departed loophole's state.** For a hand-placed loophole that is untidy. For a
  pack-shipped *intercepting* loophole it is a **CA private key left behind by a pack you
  deselected**. The design names the three artifacts needed; the awkward part is that the property
  making a pack-shipped CA possible (state keyed by loophole name, so it survives restaging) is the
  same property that makes it unattributable.

---

## 5. Defaults, and what stays bundled

**Nothing pack-shipped is ever on by default.** "Nothing is active by default" is the pack system's
headline property, and for this kind a default-on third-party pack would mean yolo running a daemon
on your machine that you did not ask for.

**So the broker keeps a default-on channel.** The draft called that channel `bundled` and defended
it as *the things yolo itself is accountable for* — which §1.1 now questions, because an official
pack carries the same accountability. What survives the questioning is narrower and worth separating
from the packaging: **the broker must be on for a user with Claude Code installed, whether or not
they know they need it**, because the alternative is silently reintroducing the single-use-refresh-
token race. Where that default *lives* is OQ-LP11; that it exists is not in question.

| Loophole | Verdict | Why |
|---|---|---|
| `claude-oauth-broker` | stays where it is | it must auto-activate; and its manifest **is not what runs** — the real spawn is reconstructed in Go and its per-jail relay has no manifest vocabulary at all. That second reason is the one that would survive becoming a pack |
| `host-processes` | stays where it is | its client is a binary baked into the image. Note this rules out a *third-party* pack, not an official one (§1.1) |
| `audio` | stays, **becomes the worked example** | no daemon, no host execution: the one a pack could carry with zero new vocabulary |

**But "it stays bundled" does not keep the default safe** — that was the draft's third refuted
claim. Staying bundled protects the broker from being *deselected*, which was never the threat. The
threat is a workspace config turning it off silently. **And under §4.5's ruling, scoping no longer
protects it either**: `enabled` is writable at workspace scope by design. So the launch-time line
naming the loophole *and the file that disabled it*, plus `yolo check` warning instead of passing
green, are not belt-and-braces any more — they are the only thing keeping this default.

**The `audio` example is dogfood, not a migration.** Under the corrected claim enumeration a fetched
copy of it emits four review-worthy claims and would prompt — which makes it *better* dogfood than
the draft's version, since it now exercises the approval path too. One honest limitation: device
passthrough is skipped whenever the launcher is itself in a jail, and nested-jail verification is
this repo's mandated loop, so the device half of that example is only observable off a jail host.

---

## 6. What can go wrong

- **It bricks jails on a stale image, and this is the `tier` incident for the third time.** A pack
  declaring a kind the *baked entrypoint* does not know about fails the boot — unknown *fields* are
  tolerated across a version skew, unknown *kinds* are not. So "tolerate an unknown kind" ships
  **before** the kind exists, or the first pack to declare it bricks any jail running a pre-`just
  load` image. The alternative ("require a host reload first") is not a mechanism, it is a hope, and
  it cannot be stated to a third-party author at all.
- **It is a silent no-op on two shipped backends.** Apple Container skips every host daemon; the
  macOS no-VM backend never reaches loophole startup at all. Both must say so by name rather than
  looking provisioned and configuring nothing — and this is the same message as §3.3's
  platform-support declaration, so they should be one mechanism.
- **Seven different surfaces answer "what loopholes do I have,"** and two of them *execute host code*
  (`yolo check` and `yolo loopholes status` both run a loophole's doctor command). Neither has the
  origin gate anywhere in reach. So either they learn about packs and two read-only-looking preflight
  commands run an unapproved pack's code, or they do not and three of this design's claims are
  unimplementable. The requirement is that the pack-aware, lock-gated loophole set becomes **one
  constructed value** produced once and passed to every consumer.
- **A daemon that starts and never becomes reachable is completely silent today** — the printer was
  plumbed in and the message was never written — and each one costs a full five seconds. That is the
  failure a third-party daemon will actually hit, so it is a named deliverable.
- **The prompt gets longer, and a long prompt is a skimmed prompt.** Total enumeration means an
  audio-shaped pack emits four claims. That is honest and it is also the shape people click through.
  Nothing here solves it; grouping by loophole in the display while keeping per-claim strings in the
  lockfile is the obvious mitigation.
- **Nothing here has ever run.** No pack-shipped loophole exists. This is design over verified code
  paths, not over a working instance.

---

## 7. What it does to `pack-capabilities.md`

That doc is already rewritten to assume this one. The short version: **its central argument for why
a pack cannot serve a capability was that none of the contribution kinds is a daemon — which is
exactly what the 15th kind falsifies.** Its conclusion survives for a different and better reason.

The bigger consequence is that **supersession shrinks to the bundled set**. If selection is how a
pack-shipped loophole turns on and off, then "supersede a loophole" is only ever needed for
loopholes selection *cannot* remove — the auto-activating bundled ones, of which there is
effectively one. Whether a whole capability system is worth building for that is **OQ-LP6**. The
counter-argument: a loophole manifest is a public surface either way, so `serves` is a field third
parties will write even if only bundled loopholes are ever superseded.

---

## 8. What I need from you

**Two are RULED and are recorded here as answers, not questions:** the install/enable scope model
(§4.5) and retiring the home directory (**LP10**). The scope model dissolved **LP12** outright, and
folded **LP3** and probably **LP8** into LP13 — so five decisions are left, and **LP13 is the one I
would take next**: it is a live hole rather than a design choice, and it is what decides whether G1
closes the hole or half of it.

The other four open questions live in the detailed doc (§9) and do not need you: one is a technical
placement choice, one is a one-way door I am flagging rather than opening, one resolves itself when a
real pack wants it, and one belongs to the `guest` notch work.

### OQ-LP13 🆕 — is everything a loophole runs on the host confirmed against CONTENT?

**The question was:** every gate in this design governs a *declaration*; none governs the *file* the
declaration names. A `file://` pack under a live-mounted workspace, or a config-block `command`
naming a workspace path, is rewritable by the agent between launches with nothing to notice it. Do
we hash what is about to execute and re-confirm when it changes — regardless of origin?

**My read: yes, and before the kind ships.** This is not a risk the 15th kind introduces; it exists
today and G1 does not touch it, so it is worth fixing whether or not any of the rest happens. The shape is in
§4.4: hash the loophole's module directory plus any argv-named file outside it, record the digest
with the confirmation, re-confirm on change. Origin decides how loud the first confirmation is, not
whether there is one.

**Where it would live:** at **install**, which §4.5's ruling already makes a user-scope act. So the
scope half is settled and this question is only about *what install checks* — one confirmation
point, one scope, every origin.

**One limit to state rather than discover.** The digest covers what yolo can *name*, not everything
that will *run* — a script can import, a binary can `dlopen`. So it is a tripwire against silent
substitution, not a boundary, and it should be described that way rather than sold as one.

**The escape hatch is deleted.** I previously wanted a per-path "I am developing this one" opt-out to
blunt re-confirmation friction. Withdrawn on your answer: **develop the loophole in a jail.** The
loophole runtime has exactly one jail-aware branch (device passthrough), so a nested jail runs host
daemons, bind mounts, endpoint publication and intercepts identically — a real development
environment whose "host" is a container you can throw away. Re-confirmation friction then only
applies on the machine where it should. An opt-out would have been a hole in the one gate that reads
content, serving a workflow that already had a better answer.

### OQ-LP2 — do the config block's host-execution keys become user-scope-only now?

**Context first, since the one-line version assumed the config schema.** The `loopholes` block in a
config file takes entries in two shapes, and the validator already distinguishes them:

- an **inline** entry *declares a loophole* — it must carry a `command`, which is the argv yolo runs
  on your host. It may also carry `env` (that command's environment) and `doctor_cmd` (a second
  command, run by `yolo check` and `yolo loopholes status`).
- an **override** entry *adjusts a loophole that already exists* — no `command`, just `enabled`,
  `env`, and `jail_env`.

Applying §4.5's two verbs to those keys gives the answer directly, and it is not "everything moves":

| Key | Why it lands where it does | Scope |
|---|---|---|
| `command` | it **is** the host execution | **user** |
| `doctor_cmd` | a second host execution, run by two commands users treat as read-only preflight | **user** |
| `env` — *either shape* | changes **what runs**, not whether: an override entry can set `LD_PRELOAD` on a daemon **yolo itself** spawns, injecting code into a first-party process | **user** |
| `enabled` | changes **whether** an already-installed loophole is active | **either** |
| `jail_env` | container-side environment; touches nothing on the host | **either** |

**My read: yes** — for the install-shaped keys above. It is the biggest single reduction in *who may
declare* host execution and is independent of everything else here.

**Note what changed:** the design previously wanted `enabled` user-only too, for daemon-bearing
loopholes. §4.5's ruling overrides that, and the protection for the case that motivated it — a
workspace silently disabling the broker — moves from scoping to disclosure (§4.5, consequence 2).

**Two caveats, both grown since the first draft.** It is a breaking change for everyone who followed
the shipped guide, which literally calls the block "the workspace-scoped entry point". And a scope
error refuses the **whole launch**, not just the loophole — so it needs one release of
warn-then-error, and the warning has to name the fix.

### OQ-LP12 — how does a workspace get a loophole another workspace does not? ✅ DISSOLVED

**Answered by §4.5's ruling, and no new machinery is needed.** You install at user scope; each
workspace enables what it wants from the set you already vetted. **My earlier proposal — the
workspace *asks* and you approve, keyed by (workspace, claims) — is withdrawn as more mechanism than
the problem needed.** The request/grant split existed to answer "how does a repo get something it
cannot grant itself"; the two-verb model answers it by making the thing a repo can do (enable)
harmless, rather than by building an approval path for it.

Residual worth knowing: a workspace can enable **any** installed loophole, not only the ones you had
that repo in mind for. That is bounded by install-time vetting, and the per-launch disclosure naming
what is active is what makes it visible.

### OQ-LP10 — retire the hand-placed loophole directory in your home? ✅ RULED YES

**Answering your question directly: yes, the local pack is exactly what it becomes**, and that makes
this cheaper than I implied. The conventional local pack (`~/.config/yolo-jail/local/`) is
implicitly selected, so a loophole dropped into it is discovered with no config edit at all — the
"drop a directory in and it works" ergonomics survive the retirement intact. The migration is moving
a directory, not writing a manifest and editing config.

**But it does not fix the objection I raised, and I should not have implied it would.** The local
pack is *implicitly* selected too, so a loophole in it also activates with no selection step. What
actually improves is different and worth having: a pack-shipped loophole emits claims and appears in
the per-launch disclosure, where the home directory prints nothing at all today. Visibility, not
gating.

**Ruled: yes, retire it — after the kind ships**, so there is somewhere to go. The second payoff is
unchanged and is a real simplification: it leaves
`loopholes enable/disable` with no special case to serve, forcing enable/disable state into config
for every source, which the design already wants and currently defers.

### OQ-LP11 🆕 — do bundled loopholes become official packs?

`AGENTS.md` opens with *"AGENTS ARE PACKS. Core does not know what an agent is."* A loophole
registry plus a magic directory plus a config block is the world before that move.

**My read: yes in principle, not yet.** One blocker is real: the broker's manifest is not what runs —
its spawn is reconstructed in Go and its per-jail relay has no manifest vocabulary — so packaging it
would be ceremony over a thing that ignores the package. Do `audio` first as the proof (§7 already
builds it as an example, so this is mostly a relabel), and leave the broker until those two have
manifest vocabulary.

### OQ-LP3 — a `file://` pack runs a host daemon with no prompt, ever ✅ DISSOLVED

**Absorbed by LP13.** My original read was "leave it — the origin model's coherence is worth more
than a special case"; that is withdrawn, because local origin is a bare `file://` prefix check with
no path constraint, so a directory an agent writes counts as yours (§4.4). Content-anchored
confirmation covers this row with no special case at all — which is a better outcome than the
special case I was arguing against.

**What survives, and it is small:** whether `IsLocal()` should *additionally* constrain the path
(refusing, say, a `file://` under a live-mounted workspace). Worth documenting either way, and not
urgent once confirmation is content-anchored.

### OQ-LP6 — is the capability system still worth building for one auto-activating bundled loophole?

**Genuinely open**, and the extension-point argument cuts both ways: a loophole manifest is a public
surface regardless, so `serves` is a field third parties will write even if only bundled loopholes
are ever superseded.

### OQ-LP8 — how does an execution approval survive a moving pin without re-prompting forever?

**My read: commit-anchor now, digest later if the friction is real** — the friction is visible and
recoverable where content-blind approval is neither. **LP13 probably collapses this question**: if confirmation
becomes content-anchored for every origin, the digest is built anyway and the commit anchor is
redundant. Decide LP13 and this one most likely disappears into it.

### OQ-LP9 — does the scope error downgrade in-jail?

The `agents` key hard-errors on the host and warns in-jail, because the in-jail config is a generated
snapshot. **My read: yes, downgrade** — `/workspace` is live-mounted, so in-jail `yolo` and nested
jails break identically otherwise.

---

## 9. The order the work has to land in

Not a task list — the *dependencies*, since three of these exist to make the rest safe.

1. **Tolerate an unknown kind** (§6, first bullet). Before the kind exists, or packs brick jails.
2. **The config block goes user-scope-only** (§4.1), with the warn-then-error migration and the
   guide fixed in the same commit. **Independent of everything else; ship it regardless of whether
   the rest ever happens.**
3. **The front**, so a daemon can be written in any language, plus the loud-failure and
   stale-socket fixes it needs to be trustworthy — then the config block's daemons migrate onto the
   surviving transport for free.
4. **The server-side spec**, labelled plainly as the *unsupervised* path — and reachable only by a
   loophole yolo itself ships, since a pack-shipped one must take the front (§3.2).
5. **The kind itself**, whose gating item is the **total claim enumeration** — nothing crosses
   without a string you saw. Its **platform-support declaration** (§3.3) lands here too, sharing one
   mechanism and one message with the inert-backend report.
6. **The approval invariants**: commit-anchored execution claims, and the per-launch disclosure
   moved to print *before* the daemon starts rather than a phase after it. For a read, after is
   cosmetic. For an execution, after is a notification that something already happened.
7. **The `audio` example pack**, as the end-to-end proof.
