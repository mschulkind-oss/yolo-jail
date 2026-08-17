# Loopholes in packs — the system-level picture

**Status:** DESIGN 2026-08-13; **the `loophole` kind LANDED 2026-08-14, and so did the rest of the
plan** — every item in the landing order, OQ-LP9 included. What is built: the kind itself
(`packdecl.KnownKinds()` now returns **fifteen**), its total claim enumeration, the manifest schema as
a leaf package (`internal/loopholedecl`, which resolves OQ-LP1 by extraction), the `platforms`
declaration, the one-value inert report on both axes, the fourth launch pre-flight for name
exclusivity, the seven-surface convergence as one constructed value, retirement-on-deselect with a
`prune` sweeper, the disclosure fix (every crossing kind, execution printed **before** the spawn), the
placement rule's config *and* manifest faces, the pack-shipped **subset wired at three seams**, **G2a**,
**`audio` as an official pack**, **OQ-LP9's three parts** (the inner-scope census, the two generated
per-consumer files, and the global `--user-layer` flag), and the earlier batch's front / `publishes` /
`request_end` / `{loophole_dir}` tokens / unknown-kind skew tolerance / config-block scope model.
**What is NOT built: one thing, deliberately** — **G2b**, the content-anchored exec approval, which is
a maintainer decision under OQ-LP8 rather than pending work.

**And the last two batches produced two findings that are about the DESIGN rather than the
implementation**, which is why they are stated here rather than filed as bugs. (1) **The pack-shipped
subset cannot express `audio`'s reason to exist** — see §5, which is the one place this doc says the
design is incomplete. (2) **The placement rule must exempt yolo's own bundled loopholes**, or yolo's
own development jail refuses all three of them on every launch (§4.4). Section-by-section *Landed*
markers are in [`loophole-packaging.md`](loophole-packaging.md); this is its readable half, written to
be commented on.

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

There were three ways a loophole got onto a machine, and only one was ever open to a third party.
*(This is the state the section was written against; a fourth channel landed 2026-08-14 — see the note
at the end of §1.1.)*

| Channel | Who it is for | State |
|---|---|---|
| bundled in the yolo binary | yolo's own three | fine — **but see §1.1** |
| a hand-placed directory in your home | one local loophole, yours | works; no fetch, no version, no approval, no manifest travelling with the code — **and see §1.1** |
| the `loopholes` block in a config file | **the only third-party path** | **was degraded and dead; the front revived it** (§3.1) |

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

**Both became questions in §8, and one is now settled and SHIPPED: OQ-LP10** (retire the home
directory) is **ruled yes and carried out**, and **OQ-LP11** (bundled loopholes become official packs)
is still open. Neither blocks the 15th kind — the kind is what makes either one *possible*. The rest of
this doc describes the world with all three channels intact, because that is the world the design was
written against; read every mention of the home directory below as historical.

**Landed 2026-08-14: there are now FOUR channels, not three — and the fourth has its first
inhabitant.** The pack-shipped channel exists and slots between bundled and the home directory in
precedence: a hand-placed user directory still overrides a pack's loophole (it carries your own
authority), and a pack claiming a *reserved* name never reaches an ordering at all — the launch
pre-flight refuses it, fatally. The official `audio` pack (§5, OQ-LP11) is the first loophole to
arrive that way. *(Superseded below: the home directory has since been retired, so the count is three
again and the precedence sentence about it no longer applies.)*

**But the consolidation this section argues for got HARDER to finish, not easier, and that is the
honest reading.** Shipping `audio` as a pack established that a pack **cannot express** the sockets the
bundled `audio` exists for (§5), so the pack sits *beside* the bundled copy rather than replacing it —
one more channel populated, none retired. **OQ-LP11**'s remaining half — bundled loopholes *becoming*
packs — now depends on **OQ-LP14**, the missing vocabulary, because you cannot delete a bundled
loophole whose capability no pack can declare.

**Landed: OQ-LP10 is CARRIED OUT — the home directory is retired, and the count is back to three.**
`~/.local/share/yolo-jail/loopholes/` is no longer read by discovery, by `yolo check`'s walker, or by
anything else; the `user` source label is deleted along with it, so the ordering is bundled < pack <
config. A directory still sitting there is **reported, never silently dropped** — discovery warns once
per process and `yolo check` renders a graded row, both naming every stranded module and the exact
commands to move it into the conventional local pack (`~/.config/yolo-jail/local/`, implicitly
selected). One migration caveat is stated in the notice itself: a pack's loophole is held to the
pack-shipped subset, so a manifest using `jail_env`, an absolute or writable bind host, or
`publishes: "endpoint"` is refused with the reason at load.

**What did NOT come with it, deliberately: the enable/disable rework.** Retiring the directory left
`yolo loopholes enable|disable` with no manifest to write, which is the second payoff this section
promised — but the replacement is a read-modify-write of a hand-commented `~/.config/yolo-jail/config.jsonc`,
and that drops every comment in the file through the json5 → re-serialize round trip. That is a
decision, not typing. The interim is a command that PRINTS the exact key, the exact file and the exact
value and exits non-zero, rather than one that silently does nothing.

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

**Landed 2026-08-14.** The kind is in `packdecl`'s closed set — `{"kind": "loophole", "from": …}`,
`from` required, Exclusive by loophole NAME — and all four of the above shipped with it, including
G2a; the only unbuilt piece of (3) is **G2b** (§4.2), and that is a decision rather than pending work.
One thing the proposal did not say and the implementation had to: **the name is knowable without
decoding the manifest**, because `name` must equal the module dir's basename. That is what lets the
exclusivity pre-flight run before any loophole is loaded, and what lets a claim be keyed even when the
manifest is unreadable.

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

**Landed 2026-08-14 as `platforms`, and the "one mechanism" is literal.** An entry is `<goos>` or
`<goos>/<goarch>` spelled as **Go** spells them, both halves against a CLOSED list — because
`["darwins"]` under an open list is a loophole supported nowhere, on every machine, forever, with no
message, which is the silent-nothing shape the field exists to end. Omitting the key means every
platform (so every pre-existing manifest keeps its meaning); an explicitly *empty* list is refused,
since it declares support for nothing. Two implementation facts the requirement did not state.
**(1)** The declaration is static and its *evaluation* is a pure function of `(GOOS, GOARCH)`, so
the schema package never reads `runtime.GOOS` — a leaf that reads the world grows an import. **(2)**
The platform and backend halves share one mechanism *in fact*, not just in intent: the platform
producer shipped as a VALUE (`loopholes.InertNote`, carrying an `Axis` and exactly one `Line()`
rendering) with **zero callers**, built expecting the backend half to plug in — which it then did,
as `AxisBackend`. Nothing in the run pipeline formats its own inert sentence, so the two
half-messages §3.3 forbids are unrepresentable rather than merely avoided. And **backend beats
platform** when both apply: the actionable line is "switch backends", not "get a different machine".
The skew direction is worth noting for whoever widens the lists — an unknown *key* is tolerated (so
an older build treats every loophole as supported everywhere, i.e. today's behaviour), but a
GOOS/GOARCH **value** is not, so a value only a newer Go knows is a refusal on an older build.

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

**Landed 2026-08-14 — the enumeration is total, and one class came out coarser than specified.**
G2's producer, G3's gate and G4's disclosure all read the same set now, through one merged helper
(the drift they used to be able to have is a source-level test failure). Two things the design got
slightly wrong, both worth correcting here rather than annotating:

- **The socket class is discriminated by the MANIFEST, not by the inode**, and it could not have
  been otherwise. Nothing in the claim producer may stat the host path: the value is raw
  (`{loophole_dir}/x`, `${XDG_RUNTIME_DIR}/pulse/native`), and resolving it is exactly what makes an
  approval string machine-specific and therefore permanently re-prompting; and a stat is a fact
  about *this machine at this moment*, so the class would change when the socket happened to be
  absent. The test is `readonly: false` — the manifest itself saying the bind is bidirectional,
  which is what every socket bind in-tree declares — **or** a `.sock`/`.socket` basename. So a `:ro`
  socket bind with a non-obvious name lands in the MOUNT class, which is why that class's text
  carries the socket caveat verbatim instead of claiming "read-only" and stopping. Nothing is
  understated; only the discriminator is coarse, and the named fix is a **declared socket bit** in
  `internal/loopholedecl`, making the class a fact the author states rather than one yolo infers.
- **An UNREADABLE declaration is a claim, not the absence of one.** The design's rule was about a
  crossing that claims nothing; the same short-circuit applies when yolo cannot parse the manifest
  at all, so that case emits one fail-closed claim ("declaration UNREADABLE at &lt;from&gt;") and is
  treated as host execution — a manifest this build cannot read may well declare a daemon.

And **`jail_daemon` gets no claim**, deliberately: it is a process inside the container, which is
the one place a pack's code was always allowed to run.

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

**The fix is NOT hashing — RULED in a later round, and the argument is better than mine.** I proposed
digesting everything a loophole would execute and re-confirming on change. Your objection:

> *"Not sure there's even any confirmation needed. If you can edit user-level files, you have all the
> perms already."*

That is right, and it dissolves most of what I was building. Writing the user config already requires
host access as you — at which point `~/.bashrc`, a systemd unit or a crontab are all available. A
confirmation dialog guarding an act that already required the authority it protects is theatre.

**But it does not cover the case that started this**, and the difference is worth being precise
about. Your argument is about the **declaration**, which only a human can write. My finding is about
the **file the declaration names**, which an agent can rewrite. They are different actors: the human
who wrote `command: ["python3", "/workspace/tool.py"]` had all the permissions; the agent that
rewrites `tool.py` afterwards has none of them.

**So the fix becomes a placement rule instead, which is cheaper and reads better.** Not *"confirm the
content"* but *"installed content may not live where an agent writes."* User-level install is then a
sound trust boundary, exactly as your argument says, because nothing reachable from it is
agent-writable. Refuse — at install, by name — a loophole whose module directory or argv target
resolves inside the workspace being mounted or inside yolo's own jail-home tree.

**One honest limit** (and the general form of this reasoning is now
[`gate-placement-principle.md`](gate-placement-principle.md)). The rule cannot be complete: yolo knows the workspace it is launching and the
home trees it manages, but not that `~/code/other-project` is somewhere an agent writes in a
*different* jail. So it catches the case that actually happens — content sitting in the repo you are
working in — and your permission argument covers the rest. A tripwire on the common shape, not a
boundary, and it should be described that way.

**A SECOND LIMIT, discovered by shipping it: the rule must NOT judge yolo's own bundled content —
fixed 2026-08-14.** As first landed it refused all three bundled loopholes on **every launch of yolo's
own development jail**, telling the user to *"install the loophole outside that tree"* about content
they did not install — and taking out the OAuth broker, the audio pass-through and `host-processes`
together. The cause is the self-hosting case: yolo running from its own source tree prefers the repo's
`bundled_loopholes/`, which is inside the very workspace the launch mounts `:rw`.

The exemption is the rule's own reasoning rather than a carve-out. The rule exists because installed
content in an agent-writable tree can be swapped by an actor with **none of the authority that
installed it**; a bundled loophole is the yolo binary's own content — *the same artifact that performs
this check* — so an agent that can rewrite it has already rewritten the checker. The gate would protect
nothing it does not already presuppose, which is
[`gate-placement-principle.md`](gate-placement-principle.md)'s **Test 1** exactly. Pack and user
loopholes stay judged, and the regression pins that the *same path* is still refused for both.

**The reusable part is about verification, not loopholes:** no unit test could have caught this,
because every placement test builds its module dir under `t.TempDir()` — so the one configuration where
the two real paths coincide is the one nobody constructs. **A rule about how two real paths relate
cannot be verified by a test that invents both of them.** The `-short` suite was green throughout; the
mandated nested-jail smoke is what found it.

**This is OQ-LP13, now much smaller than when it was filed.** It also changes what "ship G1 first"
means: G1 remains the biggest reduction in *who can declare* host execution, but it should stop being
described as closing the hole on its own.

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
| The gate | the user-scope edit **is** the gate — plus a placement rule so what it names is not agent-writable (§4.4, LP13) | none needed |
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

- **A `file://` pack is trusted unconditionally and forever** — no approval, no re-approval. The
  origin model's claim is that a directory you control carries your own authority; the detailed half
  **withdrew** "not changing it" (§4.3a), because the check constrains the path in no way, so a
  directory an *agent* writes is equally "local". The ruled answer is a placement rule rather than
  content confirmation — installed content may not live where an agent writes — and it now covers the
  two trees a launch hands over (**done 2026-08-14** for loophole argv and daemon spawns, and for a
  manifest's own **module dir**, `host_daemon.cmd` and `doctor_cmd` — a refused module dir suppresses
  the argv refusals under it, since `{loophole_dir}` resolves to that dir and so a module dir in an
  agent-writable tree means *every* host-side field names an agent-writable target, including the ones
  no rule can see). Still **OQ-LP3** for the trees yolo cannot know about.
- **Selection controls activation, not revocation.** Deselecting a pack stops the *next* launch from
  starting its daemon (and, since 2026-08-14, retires the state it left behind). It does not stop a
  daemon that already ran — once arbitrary host execution has happened once, persistence is available
  by means yolo has no view of. **Narrower than it was, and named precisely:** teardown now kills the
  whole process *group*, so what survives is only what the daemon deliberately placed outside its own
  group — a `~/.bashrc` line, a crontab entry, a double-forked reparented process. No packaging design
  changes that, and this one does not claim to.
- **`yes | yolo pack install` grants approval.** A one-line hardening, independent of this design,
  worth doing in the same batch now that a `y` means "run this code." **Done 2026-08-13:** approval
  needs a terminal, and a pipe is refused before the prompt is shown rather than after it answers.
- **Nothing reaps a departed loophole's state.** For a hand-placed loophole that is untidy. For a
  pack-shipped *intercepting* loophole it is a **CA private key left behind by a pack you
  deselected**. The design names the three artifacts needed; the awkward part is that the property
  making a pack-shipped CA possible (state keyed by loophole name, so it survives restaging) is the
  same property that makes it unattributable.

  **Fixed 2026-08-14, and the unattributability was resolved by writing the attribution down**: a
  pack→loophole-state ownership record at staging, a detector on the launch path (where deselection
  is actually observed), and a `prune` sweeper. The state dir *and* its `host-service-<name>.log` are
  **archived, not deleted** — dated generations, keep-newest-three, carrying a marker naming the pack
  that owned them, because *"whose key is this?"* is the first question anyone asks of an archived
  directory. Three refusals the design did not anticipate, each protecting a private key rather than
  tidiness: **retire before record** (one config edit can drop a pack and select a different one
  shipping the same loophole name — recording first would hand the new pack the old pack's CA), an
  **unknown** configured-pack set retires nothing, and a **corrupt** record is neither acted on nor
  overwritten. **One honest gap:** a pack still in `packs` that has *stopped declaring* a loophole is
  not detected — that evidence is indistinguishable from a momentarily-unreadable pack tree, so
  retirement keys only on the signal the user typed.

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
| `audio` | stays — and an **additive official pack ships beside it** | no daemon, no host execution: the one a pack could carry with zero new vocabulary. **Half true, measured:** the claim classes needed no new vocabulary, the host PATHS did — see the finding below |

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

**The count holds against the shipped producer, and its composition was wrong.** Four claims, yes —
but *two* IPC (the two binds declaring `readonly: false`), *one* MOUNT
(`{loophole_dir}/asound.conf`, a `readonly: true` regular file), and one device. The draft called them
*"three socket/dir binds and `/dev/snd`"*; only two of the three binds are socket-class. The
distinction earns its keep: the MOUNT class's text carries the socket caveat verbatim precisely
*because* the classes are separate, and collapsing them would make that caveat say nothing.

### 5.1 THE FINDING: the pack-shipped subset is TOO TIGHT — and this is the design being incomplete, not the code

**Landed 2026-08-14 as `packs/audio`, an official embedded pack** (one `loophole` contribution, one
`env` contribution). Writing it produced the only finding in this batch that says **the design is
unfinished** rather than that an implementation was:

> **`audio`'s reason to exist is inexpressible for a pack.** The loophole exists to pass through two
> host sockets, `${XDG_RUNTIME_DIR}/pulse/native` and `${XDG_RUNTIME_DIR}/pipewire-0`. A pack-shipped
> loophole's bind hosts must be inside its own module dir or relative to `$HOME`. The `$VAR` spelling
> is refused; the literal `/run/user/<uid>/pulse/native` is refused as absolute; and the path is not
> under `$HOME`, so home-relative cannot reach it. **There is no third spelling.** The socket half of
> the very loophole this design nominated as its own dogfood cannot be declared by a pack at all.

**The rule is right and the vocabulary is missing** — those are different things, and reading this as
"loosen the rule" would be the wrong lesson. Admitting `${XDG_RUNTIME_DIR}` admits `${HOME}/.ssh` and
`${XDG_RUNTIME_DIR}/../../etc` with it; the refusal is doing real work. What the finding says is that
a **runtime-dir socket** — the ordinary shape of host IPC on Linux, and something a third-party
loophole will certainly want — has no way to be said.

**And the design's own suggested workaround does not survive contact with the case.** §4's subset
table offers *"or a `host_daemon` that mediates the access"*. Spelled out here, that means: to bind the
PipeWire socket **the user's own desktop session already exposes**, write an audio proxy — a host
daemon speaking the PipeWire protocol, shipped in a pack, forwarding frames — and thereby swap one
`:ro` bind for **arbitrary host execution plus a claim that says so**. That is not a mitigation; it is
a strictly larger grant reached by a much longer road, and it would be the *only* route to working
audio. A rule whose escape hatch is "run code on the host instead" is pushing authors toward the
sharpest capability in the system in order to obtain the mildest one. The proportionate answer is a
**declared, enumerated runtime-socket vocabulary** (yolo resolves the runtime dir; the manifest names
only the socket; the claim is emitted in the host-IPC class that already exists) — recorded as
**OQ-LP14**, named and not designed.

**So the shipped pack is ADDITIVE, and two of its shapes were forced rather than chosen.** The bundled
`audio` is kept and untouched — do nothing and nothing changes. The pack's loophole is named
`audio-alsa`, because `audio` is a **reserved** name and a pack claiming one refuses the launch
*fatally*; and it binds `/etc/alsa/conf.d/50-yolo-audio-alsa.conf` rather than `/etc/asound.conf`,
because podman refuses two binds on one destination — a jail with both the bundled loophole and this
pack would **refuse to start**. alsa-lib reads `conf.d` first, so the fragment routes identically;
verified with `sox` in all three cases (unrouted, fragment-only, both).

**The second cost was named in advance and is now paid by a shipped pack:** `jail_env` is refused for a
pack, so `PULSE_SERVER`/`PIPEWIRE_REMOTE` are declared through the `env` kind and become
**unconditional** — set on every launch that selects the pack, even where no socket crossed. That is
LP5's cost, and it moved from a prediction to a behaviour someone can observe.

**What the pack proved, and what it did not.** It closes §6's *"nothing here has ever run"* at the
level that mattered: a real instance goes through discovery, selection, the subset loader, the claim
enumeration, the name pre-flight, the inert report, the container argv and teardown — and building it
surfaced two live defects nothing else had (the lazy loophole resolver skipped **every embedded pack**,
so a selected `audio` was absent from `yolo loopholes list` and warned *"no loophole named 'audio-alsa'
is installed"* at every launch — the same sentence a genuine staging failure prints, fired on the one
case where nothing was wrong). It does **not** exercise the approval prompt or a host-daemon spawn: the
pack declares no daemon, and an embedded pack carries yolo's own authority, so its approval is true by
construction and never reaches a prompt.

---

## 6. What can go wrong

Five of these six were **closed** by the batch that landed the kind, and the sixth by the batch that
shipped the `audio` pack; the markers say which. Only the prompt-length one is still open.

- ~~**It bricks jails on a stale image, and this is the `tier` incident for the third time.**~~ A pack
  declaring a kind the *baked entrypoint* does not know about fails the boot — unknown *fields* are
  tolerated across a version skew, unknown *kinds* were not. So "tolerate an unknown kind" had to ship
  **before** the kind exists, or the first pack to declare it bricks any jail running a pre-`just
  load` image. The alternative ("require a host reload first") is not a mechanism, it is a hope, and
  it cannot be stated to a third-party author at all. **Closed 2026-08-13** — the tolerant decoder
  skips an unknown kind and reports it by name, the boot warns each one so the degradation is
  audible, and a regression test pins that such a manifest **boots** (2026-08-14). The kind then
  landed on top of that, in the right order.
- ~~**It is a silent no-op on two shipped backends.**~~ Apple Container starts no loophole host
  services at all; the macOS no-VM backend never reaches loophole startup. **Closed 2026-08-14** —
  one line per inert loophole, by name, on both backends, through the *same* value type as §3.3's
  platform report rather than a second sentence.
- ~~**Seven different surfaces answer "what loopholes do I have,"** and two of them execute host
  code.~~ **Closed 2026-08-14** — the pack-aware, lock-gated set is now ONE constructed value
  (`loopholes.NewHostSet`), and a consumer cannot assemble a different view, forget to include the
  bundled set, or bypass the origin gate. The requirement was the value; the implementation added the
  second half it needed, which is that the door is **nailed shut in the callee**: `RunDoctorChecks`
  refuses to execute a pack-sourced record unless the caller recorded that its pack's host access was
  approved, and a set assembled by hand carries no gate and so can execute nothing a pack shipped. A
  rule the two read-only-looking preflight commands were merely *asked* to follow is a rule the next
  call site does not know about.
- ~~**A daemon that starts and never becomes reachable is completely silent today**~~ — the printer
  was plumbed in and the message never written, and each one cost a full five seconds. **Closed
  2026-08-13**: a yellow warning naming the loophole, the awaited path and the log, plus a real
  `cmd.Wait()` replacing a dead `ProcessState` read so a crashed daemon is reported at once.
- **The prompt gets longer, and a long prompt is a skimmed prompt.** Total enumeration means an
  audio-shaped pack emits four claims. That is honest and it is also the shape people click through.
  Nothing here solves it; grouping by loophole in the display while keeping per-claim strings in the
  lockfile is the obvious mitigation. **Still open** — the footprint does distinguish the severities
  now (`⚠ RUNS CODE ON YOUR MACHINE` versus `⚠ review`, and the review tail counts executions first
  and separately rather than saying "1 loophole"), which is a legibility improvement and not the
  length fix.
- ~~**Nothing here has ever run.**~~ **Closed 2026-08-14 at the level that mattered:** a pack-shipped
  loophole now EXISTS — the official `audio` pack (§5.1) — so a real instance goes through discovery,
  selection, the subset loader, the claim enumeration, the name pre-flight, the inert report, the
  container argv and teardown. Building it found two defects nothing else had (a lazy resolver that
  skipped every embedded pack, and the subset finding), which is the whole argument for dogfood.
  **The residual, stated precisely:** the pack declares no host daemon, and an embedded pack carries
  yolo's own authority — so the **approval prompt** and a **host-daemon spawn** are still unexercised
  end to end. That needs a *fetched* pack with a daemon, which is a fixture rather than a design.

---

## 7. What it does to `pack-capabilities.md`

That doc is already rewritten to assume this one. The short version: **its central argument for why
a pack cannot serve a capability was that none of the contribution kinds is a daemon — which is
exactly what the 15th kind falsifies**, and as of 2026-08-14 it falsifies it in fact rather than in
prospect. Its conclusion survives for a different and better reason.

The bigger consequence is that **supersession shrinks to the bundled set**. If selection is how a
pack-shipped loophole turns on and off, then "supersede a loophole" is only ever needed for
loopholes selection *cannot* remove — the auto-activating bundled ones, of which there is
effectively one. Whether a whole capability system is worth building for that is **OQ-LP6**. The
counter-argument: a loophole manifest is a public surface either way, so `serves` is a field third
parties will write even if only bundled loopholes are ever superseded.

---

## 8. What I need from you

**Everything here is now ruled, and everything ruled is now BUILT.** The install/enable scope model
(§4.5), **LP2**, **LP6**, **LP10**, **LP11** and **LP13** all have answers; **LP12** dissolved and
**LP3** and **LP8** folded into LP13. LP2 and LP13 have both **shipped**, and **LP9 — the last thing
that needed you — is now BUILT too** (2026-08-14), as the inner-scope census, the two generated
per-consumer files and the global `--user-layer` flag. Its entry below records the four corrections the
implementation forced.

**So there is exactly ONE new thing that needs you, and it came from shipping rather than from
reviewing: OQ-LP14** — the pack-shipped subset has no vocabulary for a runtime-dir socket, which is why
the `audio` pack ships only half of `audio` (§5.1). It needs a ruling; nothing shipped is blocked on it.

The remaining open questions live in the detailed doc (§9) and do not need you: **LP1 is resolved by
extraction** — the schema now lives in `internal/loopholedecl` as a leaf, which was the recommended
of the two options — one is a one-way door I am flagging rather than opening (LP4), one is now a cost a
shipped pack actually PAYS rather than a prediction (LP5 — `audio`'s env is unconditional), and one
belongs to the `guest` notch work (LP7).

**LP11's first step is DONE and its second is now blocked on LP14.** `audio` ships as an official pack
carrying a loophole. But it is **additive** — the bundled copy is kept, because a pack cannot express
the sockets — so no channel has been retired and the consolidation LP11 is really about cannot finish
until LP14 is answered. **LP10** (retire the hand-placed loopholes directory) is unblocked, unaffected,
and still not carried out.

### OQ-LP13 — what stops an agent swapping the file a loophole runs? ✅ RULED — and the answer is not hashing

**The question was:** every gate governs a *declaration*; none governs the *file* the declaration
names, and `/workspace` is agent-writable. Do we hash what is about to execute?

**Ruled: no hashing, and no new confirmation.** *"If you can edit user-level files, you have all the
perms already."* Correct — writing the user config requires host access as you, and at that point
`~/.bashrc` and cron are equally available, so a dialog guarding it protects nothing. The install
step **is** the confirmation: an edit to the user config to install, a workspace config to use it.

**What survives is one placement rule, not a mechanism.** The permission argument covers the human
who writes the declaration; it does not cover the agent who rewrites the file that declaration names.
So: **installed content may not live where an agent writes** — refuse, at install and by name, a
loophole whose module directory or argv target resolves inside the workspace being mounted or inside
yolo's own jail-home tree. That makes user-level install a sound boundary rather than one with a
hole in it, and it costs a path check instead of a digest, a lockfile field and a re-prompt loop.

**Limit, stated rather than discovered:** the rule cannot be complete. yolo knows the workspace it is
launching and the homes it manages, not that `~/code/other-project` is agent-writable in some other
jail. It catches the shape that actually occurs — a daemon sitting in the repo being worked on — and
the permission argument covers the rest.

**Landed 2026-08-14, both faces.** The config faces (an inline entry's `command`/`doctor_cmd`, plus
the spawn) and the manifest faces (the module dir, `host_daemon.cmd`, `doctor_cmd`) go through one
tree comparison, because two comparisons is how the two faces would come to disagree about what
"inside the workspace" means. Two things the ruling did not specify and the implementation decided:
the **module dir is the face that subsumes the others** (`{loophole_dir}` resolves to it, so a module
dir in an agent-writable tree means every host-side field names an agent-writable target — including
the ones no rule can see, like a Python daemon's imports), which is why a refused module dir
*suppresses* the argv refusals under it: one mistake, one message. And the check is deliberately
conservative about what counts as a path (no whitespace, no shell metacharacters), because a false
positive refuses a working loophole at *every* launch — so §4.4's "cannot be complete" limit now has
a second, narrower edge to name.

### OQ-LP2 — do the config block's host-execution keys become user-scope-only? ✅ RULED YES

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

**Ruled: yes** — for the install-shaped keys above. It is the biggest single reduction in *who may
declare* host execution and is independent of everything else here.

**Note what changed:** the design previously wanted `enabled` user-only too, for daemon-bearing
loopholes. §4.5's ruling overrides that, and the protection for the case that motivated it — a
workspace silently disabling the broker — moves from scoping to disclosure (§4.5, consequence 2).

**The migration is ruled too, and it is better than the warn-then-error I proposed.**

> *"We just need obvious messages and probably some fatal error. If a workspace tries to enable a
> loophole that is not installed, it should fatal error, with instructions to install, or perhaps
> this is the confirmation. We could allow automatically adding it to yolo at the user level."*

**"Perhaps this is the confirmation" is the good part**, and it replaces the separate confirmation
step LP13 just deleted. The sequence becomes:

1. a workspace config enables `acme`; `acme` is not installed;
2. yolo **fails the launch** with a message naming the loophole, the file that asked for it, and what
   installing it would grant;
3. and offers to add it to the user config for you.

That is the human-in-the-loop moment, in the right place, arrived at from the direction people
actually hit it — rather than a prompt bolted onto a command nobody runs twice. The offer must
require a real human: a TTY, and fail-closed without one, or a workspace could drive its own
promotion.

**It also fixes a shipped behaviour that is wrong today.** An unknown loophole name in config
currently produces a *warning at every launch* saying the entry is being treated as an override and
is probably a no-op. That is the least useful of the three options — it neither works nor stops.
Under this ruling it becomes fatal with instructions.

**Two migration facts stay true.** It is a breaking change for everyone who followed the shipped
guide, which literally calls the block "the workspace-scoped entry point". And a scope error refuses
the **whole launch**, not just the loophole — which is precisely why the message has to carry the
fix.

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

### OQ-LP10 — retire the hand-placed loophole directory in your home? ✅ RULED YES; CARRIED OUT

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

**DONE.** Discovery, `yolo check`'s walker and the `user` source label are all gone; a populated
directory produces a migration notice (discovery stderr, once per process, plus a graded `yolo check`
row) naming every stranded module and the `mv` + `pack.json` to write. The `SourceUser` constant is
deleted, so the ordering is bundled < pack < config, and `resolve()`'s default label moved to the
FAIL-SAFE end (`SourcePack`: refused host code without a recorded gate, and judged rather than
exempted by the placement rule) — it used to be the permissive `user`.

**The second payoff is NOT yet collected, and that is deliberate.** `enable`/`disable` now print the
config key rather than writing it. Writing it means yolo performing a read-modify-write on the user's
hand-commented `config.jsonc`, which loses every comment through the json5 → `jsonx.DumpsIndent` round
trip — a degradation that was acceptable for a yolo-generated manifest and is not for a file the human
wrote. The obvious dodge, a conventionally-named auto-merged state file beside it, is already
**withdrawn with cause** in this codebase (`internal/config/userlayer.go`'s header: it activates
because a file exists, invisibly at the call site). So the config-write is a separate decision and a
separate change.

### OQ-LP11 — do bundled loopholes become official packs? ✅ RULED YES; FIRST STEP SHIPPED 2026-08-14

`AGENTS.md` opens with *"AGENTS ARE PACKS. Core does not know what an agent is."* A loophole
registry plus a magic directory plus a config block is the world before that move.

**Ruled: yes, and it ships in this batch rather than after it** — which upgrades §5's `audio`
example from "a doc example someone might build later" to a deliverable. That is a good trade: it
was already the plan to build `audio` as the end-to-end proof, so shipping it as a real official pack
costs a relabel and buys the consolidation immediately.

**Shipped — and "costs a relabel" was wrong.** `packs/audio` exists and carries a loophole, which is
the first half. What it does *not* do is consolidate anything: it is **additive**, sitting beside a
bundled copy that is deliberately KEPT. Three measurements forced that (§5.1): the plain name `audio`
is **reserved** and claiming it refuses the launch fatally; podman refuses two binds on one
destination, so binding `/etc/asound.conf` like the bundled one would make a jail with both **refuse to
start**; and — the load-bearing one — **a pack cannot express the two sockets `audio` exists for at
all**. So the honest pack ships the ALSA half under the name `audio-alsa`.

**That reorders this question's remaining half.** Retiring a bundled loophole means a pack can do what
it did; for `audio` that is now known to be false, and the fix is **OQ-LP14**'s runtime-socket
vocabulary. LP11's consolidation therefore *depends on LP14*, where before it looked independent of
everything except the kind.

**The broker still waits, on a stated blocker rather than caution:** its manifest is not what runs —
the spawn is reconstructed in Go and the per-jail relay has no manifest vocabulary at all — so
packaging it would be ceremony over a thing that ignores the package. It moves when those two have
vocabulary, and `host-processes` can move whenever, since an official pack is embedded in the same
binary as its client.

### OQ-LP14 — the subset cannot say "a socket in this session's runtime dir" ❓ NEW, and it needs you

**Raised by building the thing, not by reviewing it.** A pack-shipped loophole's bind hosts must be
inside its own module dir or relative to `$HOME`. `${XDG_RUNTIME_DIR}/pulse/native` is neither, in every
spelling: the variable form is refused (it names an absolute path one indirection later), the literal
`/run/user/<uid>/…` is refused as absolute, and the path is not under `$HOME`. So the socket half of
`audio` — the loophole this design chose as its own dogfood — **cannot be declared by a pack** (§5.1).

**The rule is right; the vocabulary is missing.** Widening the path rule would admit `${HOME}/.ssh` and
`${XDG_RUNTIME_DIR}/../../etc` with it. And the design's own named fallback — *"a `host_daemon` that
mediates"* — means writing an **audio proxy** to reach a socket the user's session already exposes,
i.e. reaching for arbitrary host execution in order to obtain a read-only bind. That inverts the risk
ordering the whole design rests on, so it cannot be the answer.

**My read: a declared, enumerated runtime-socket vocabulary.** yolo resolves the session runtime dir;
the manifest names only the socket inside it; the claim is emitted in the **host IPC** class the
producer already has, so the approval string stays machine-independent (what is approved is the
declaration, not the resolved path). Say the word if you would rather leave the subset as it is and
accept that runtime-dir IPC is bundled-only — that is a coherent answer too, and it is what ships
today. **Nothing is blocked on this**; it decides whether LP11's consolidation can ever finish.

### OQ-LP3 — a `file://` pack runs a host daemon with no prompt, ever ✅ DISSOLVED

**Absorbed by LP13.** My original read was "leave it — the origin model's coherence is worth more
than a special case"; that is withdrawn, because local origin is a bare `file://` prefix check with
no path constraint, so a directory an agent writes counts as yours (§4.4). Content-anchored
confirmation covers this row with no special case at all — which is a better outcome than the
special case I was arguing against.

**What survives, and it is small:** whether `IsLocal()` should *additionally* constrain the path
(refusing, say, a `file://` under a live-mounted workspace). Worth documenting either way, and not
urgent once confirmation is content-anchored.

### OQ-LP6 — is the capability system still worth building for one auto-activating bundled loophole? ✅ RULED YES

**Ruled: build it.** The extension-point argument is what carries it: a loophole manifest is a public
surface regardless, so `serves` is a field third parties will write even if only bundled loopholes
are ever superseded — and designing that field once, now, is cheaper than retrofitting it around
whatever the first third-party use turns out to be. A6 proceeds at its narrowed scope.

### OQ-LP8 — how does an execution approval survive a moving pin? ✅ MOSTLY COVERED — one thing to confirm

**Yes, your LP13 argument covers most of it.** With no content confirmation there is no re-prompt
loop to design, so the question of how an approval survives a pin move largely stops existing.

**One residual, because it is the one case your argument does not reach.** "You already had the
perms" is true of *your* edit; it is not true of *someone else's future commits*. A pack pinned at
`?ref=main` re-fetches content nobody has seen, from an author who can change it at will — that is
not a permission you exercised, it is trust you extended continuously.

**My read: accept it and say so, rather than build re-prompting.** It follows from your model —
choosing to follow a branch *is* the trust decision — and it keeps the friction at zero. What it
requires is that the docs say it in one plain sentence, and that **tag pins are the documented shape
for a pack carrying host execution**. Say the word if you would rather it re-prompt when the commit
moves; that is the only variant left in this question.

### OQ-LP9 — nested jails: the outer jail IS the user level for the inner one ✅ BUILT 2026-08-14

**Design settled by your three-part split; all three parts have now shipped, with four corrections the
implementation forced — they are at the end of this entry, and one of them says this doc's own named
example was the wrong one.**

**Your comment changed this question entirely, and for the better.** I had asked something small —
does the scope error downgrade to a warning in-jail, the way the `agents` key does. You reframed it:

> *"For jail in jail, the outer jail is essentially 'user level' for the inner jail. We need to
> support this somehow."*

**That is the correct model, and it is the same one §4.5 just ruled — applied recursively.** "User
level" is not a fixed path; it is *the scope that owns the machine the daemon runs on*. On your
laptop that is your config. Inside jail A, the machine a loophole runs on **is jail A**, so jail A's
own config is the user level, and jail A's agent legitimately owns it — because the blast radius is a
container you can throw away. Nothing about that reaches your real host.

**This is now written up as a principle**, since it generalises well past loopholes:
[`gate-placement-principle.md`](gate-placement-principle.md) — put the gate where the *authority*
changes, and remember that "trusted" is a relationship between an actor and what it can destroy, not
a property of a path.

### Why that config file is there — and your follow-up is right that the method is wrong

**What it serves, corrected: not only nesting.** The mount comment says "for nested jails", but the
readers say more — `yolo check --no-build` (which the jail briefing tells every agent to run),
`yolo loopholes list`, and `yolo pack` all read user scope *inside* the jail. So the file matters on
every setup, including ones that cannot nest; nesting is just the loudest consumer. Your "this only
serves jail-in-jail-capable setups" concern lands on the *raw-bind method*, not on the file existing.

**And the raw bind is the wrong method, for exactly the reason you gave.** It is not generated — it
is a single-file `:ro` bind of your real config — which means it inherits **keys whose meaning does
not survive the boundary**:

- **Paths that do not exist in the container.** A cache relocation target on a big host disk, a
  loophole config naming a host socket — in-jail `yolo check` evaluates these against a world where
  their referents are simply absent, and warns about problems you do not have.
- **Grants whose referent silently changes.** `host_files` means *your real home* when read on the
  host. Read inside jail A, "the host home" is jail A's disposable home — same words, different
  object.
- **And what crosses is not even the whole config.** Only `config.jsonc` and `config.lua` are
  mounted; `include_if_found` files are not. Your own config's first line proves it: the
  `overrides.jsonc` it includes stays host-side, so the in-jail "user scope" is neither the full
  effective config nor a designed subset — it is whatever happened to be in the top file. The
  raw bind is already doing filtering; it is just doing it **by accident**.

**So the answer to your original question flips: yes, it should be generated.** One thing to keep
from the current arrangement, deliberately: single-file delivery into a jail-owned directory is what
makes writing *beside* the file jail-local, and that property must survive the change.

### What replaces it — your three-part split, which is simpler than what I proposed

> *"Write the file 'yolo check and loopholes' needs, and put a comment saying that's the case; set
> whatever runtime is needed for jail-in-jail and mark that as why; and the yolo CLI has a CLI arg
> that allows a layered-in file like it was user-level so that privileged things can be futzed
> with."*

**Adopted, and it beats my one-big-snapshot design because it splits by CONSUMER instead of by key
class.** I was building one file that had to be right for every reader at once, gated by a census
answering the abstract question "does this key's meaning survive the boundary." Your split asks
three concrete questions instead — *what does preflight read*, *what does a nested launch need*,
*how does a human grant more* — and each has a checkable answer.

**1. The preflight file.** Generated for exactly the in-jail readers that exist — `yolo check`,
`yolo loopholes list`, `yolo pack` — containing only the keys they evaluate meaningfully in-jail,
with a header comment naming its purpose, its generator, and its launch time. This is what stops the
false errors: a key like `gpu` simply is not in the file, so in-jail `yolo check` never evaluates a
host driver against a container that does not have it. *(This paragraph originally named
`cache_relocations`, which turned out to be the one example that was already fixed by hand — see the
Landed note's correction 1.)*

**2. The nested-launch input.** Provided only where nesting is possible, marked in its header as
existing for that reason — the keys an inner launcher composes a jail from (`packages`, `packs`,
mise tools). On a backend that cannot nest it is simply not written, which answers the "excludes
some setups" concern by construction: nothing is mounted that serves a capability the setup lacks.

**Both are renders of one computation, which is what keeps the split honest.** The launcher already
composes the effective config and `yolo config dump` already renders it canonically. Each file is
that computation through a per-consumer filter — so they cannot drift from the *source*, and the
census I proposed does not disappear so much as shrink into two named manifests, each testable
against its actual readers instead of against an abstraction.

**3. The layer arg.** `yolo --user-layer <file>` (name yours to pick): layer this file in at
user-level precedence, explicitly, at the invocation that wants it. This replaces my
`config.local.jsonc` proposal, and I am dropping that one with cause — **a conventionally-named
auto-merged file is the same mechanism as the `include_if_found`/`overrides.jsonc` accident I argued
against**, one notch more designed. It activates because a file exists, invisibly at the call site.
The arg is the opposite: visible in the command line, testable, inert unless passed.

**Why the arg is safe everywhere, in one sentence from the principle doc:** passing an argument to
`yolo` requires the ability to run commands, which already exceeds anything the argument grants —
[`gate-placement-principle.md`](gate-placement-principle.md) Test 1, so no gate belongs on it, on
the host or in a jail. And it is how the nested-development story actually works now: the in-jail
agent writes a layer file in its own home, passes `--user-layer`, and installs a loophole whose
blast radius is the container — Test 2.

**Recursion stays by composition:** jail A composes inherited + any layer it was passed, renders
the two files above for jail B. Every level sees the same shape at any depth.

**On "tricky to test completely, but worth it" — the testable core.** Each generated file gets a
golden-render test against its consumer list (a new config key fails the build until it is assigned
to files or explicitly to neither, which is the census surviving as a drift test); plus one
integration case per false-error class this exists to kill — in-jail `yolo check` over a config
carrying a host-referent key must be silent about it. What stays genuinely hard is the full nested
matrix, and that is the part worth doing by hand once per release rather than pretending a unit
test covers it.

### LANDED 2026-08-14 — and four things the split got wrong or left open

**1. `cache_relocations` was the WRONG example for the false-error class, and this doc used it twice.**
It is the key the design kept naming, and measured with a real in-jail `yolo check --no-build` it was
**already silent** — it and `host_files` had each been hand-patched with an in-jail guard, one at a
time, over the feature's life. What was *actually* still producing false errors, and what nobody had
noticed:

| key | what an in-jail check actually printed |
|---|---|
| `gpu` | **four fails** — nvidia-smi / nvidia-ctk / runc "not found", "No CDI spec" — about a host GPU you configured correctly |
| `mounts` | one warning per entry: *"host path does not exist and will be skipped"* |
| `env_sources` | *"env_sources file not found, skipping"* per host dotenv |

**This strengthens the ruling rather than weakening it**, which is why the sentences above were
corrected instead of annotated: a hand-added guard fixes the incident someone complained about, and a
census fixes the pattern. Two keys guarded, three louder classes unguarded, is exactly the shape of
maintenance the generated file replaces. The two pre-guarded keys are now covered by the same test as
**regressions**, so a future refactor that reasonably deletes a redundant guard cannot revive the false
error — and each live class has a control asserting the check *does* report it when the key is present,
so the silence is provably the filter's doing.

**2. Five keys earn BOTH memberships, measured rather than judged.** The first pass classified
`security`, `mise_tools`, `mcp_servers`, `mcp_presets` and `lsp_servers` as preflight-only from the
shape of the key. Measured against the code instead: `yolo check`'s entrypoint dry-run feeds exactly
these into a temp home and runs the **real generators** over them, while the run pipeline feeds the same
five to a **real container**. One consumer validates, the other composes — both memberships are earned,
and a nested launch that dropped them would silently lose your MCP servers and your blocked-tool shims.
Same for `journal` and `host_processes`. The lesson for the split: "in one file" must not be allowed to
collapse into "in both" by default, so the distinctness assertion moved to a key that is genuinely
preflight-only.

**3. A defect only the real nested run caught: the host wrote the nested-launch file and NOTHING READ
IT.** Part 2's file was generated correctly and consumed nowhere, so `packages`, `env_sources`,
`resources` and `network` reached a jail and stopped there. Measured across two real nesting levels: at
**depth 2 the file had lost `packages` and `env_sources`**, because depth 1's effective config never
contained them — precisely the "a rule changes with nesting" failure the recursion-by-composition
property forbids, and it made the whole file decoration. After the fix (the inherited file is folded
into the user-scope read, *under* the jail's own config) both files are byte-identical at depth 1 and
depth 2. **Two unit tests passed while this was broken.** It is the second time in this batch that a
thing was built and not wired — the first was the origin gate computed and ignored at the spawn — and
the shared shape is worth naming: *the value was computed correctly and then not consumed.*

**4. The layer arg is GLOBAL.** Not a `run` flag: **four** commands read user scope in-jail (`run`,
`check`, `loopholes`, `pack`), so a flag reaching only `run` would change a launch but not the command
you verify it with — which sends an agent hunting for a bug in the feature instead of noticing the flag
it forgot to pass. It is consumed in `Main` before argv rewriting, and it stops at `--` so an inner
command's own `--user-layer` is left alone.

**And the nested-development path (part 3's whole purpose) is VERIFIED end to end in a real nested
container:** an in-jail agent wrote a pack shipping a loophole, wrote a layer naming it, and
`pack install`, `loopholes list`, `check` and a nested launch all saw it. **One constraint you need:**
the jail's home ROOT is `:ro`, so `mkdir ~/mypack` fails — the pack has to live under
`~/.local/share/…` or in the workspace. The layer file itself lands fine because `~/.config` is
writable, which is the single-file-delivery property doing its job. `yolo config-ref` carries the
copy-pasteable sequence.

**One thing that could not be built as asked: `config drift` cannot be TAUGHT user-half drift.** There
is no host file in a jail to diff against — the user scope in here is a generated render, so the
comparison has no second side. So the command **names the scope it cannot compare**, in-jail only, on
both the in-sync and the drifted path (the limit is a property of the command, not of the result), with
exit codes 0/3/4 untouched so an agent keying on them keeps working. Silence there would invite a
reader to take "In sync" as "nothing changed anywhere" and then debug a stale `packs` list as a code
problem.

**And one gain the split did not promise:** `include_if_found` **content** now crosses. The old raw bind
mounted exactly two filenames, so an included file stayed host-side — the accident this doc complained
about. Rendering from the effective config fixes it for free, since the includes are merged before the
filter runs. The directive itself is deliberately *not* emitted: re-resolving a host-relative sibling
path inside a jail would hunt for a file that is not there.

---

## 9. The order the work has to land in

Not a task list — the *dependencies*. Rewritten after the rulings, which removed work as often as
they added it. **ALL EIGHT ITEMS HAVE LANDED** (2026-08-14). One residual sits inside item 6 — **G2b**,
and it is a decision rather than pending work. The two things this list does *not* contain, because
neither is an item, are the batch's design findings: **OQ-LP14** (the subset cannot express a
runtime-dir socket, §5.1) and the placement rule's **bundled exemption** (§4.4).

1. ✅ **Tolerate an unknown kind** (§6, first bullet). Before the kind exists, or packs brick jails.
   — **done 2026-08-13**, plus the boot regression test 2026-08-14.
2. ✅ **The config block's install-shaped keys go user-scope-only** (§4.1, LP2), with the
   **fatal-error-plus-offer** migration: a workspace enabling an uninstalled loophole fails the
   launch, names the file that asked, and offers to install it at user level. Independent of
   everything else; ship it regardless of whether the rest happens. — **done 2026-08-13.**
   - ✅ **2a. The placement rule** (LP13): refuse installed content that resolves inside the mounted
     workspace or a jail-home tree. A path check, not a digest — this is what makes item 2's
     user-scope boundary sound instead of half-sound. — **config faces done 2026-08-14**, manifest
     faces (module dir, `host_daemon.cmd`, `doctor_cmd`) **also 2026-08-14**, and the **bundled
     exemption** the same day: as first shipped the rule refused all three bundled loopholes on every
     launch of yolo's own development jail, because a self-hosting yolo reads them out of the `:rw`
     workspace. Exempting them is the rule's own Test-1 reasoning, not a carve-out — and no unit test
     could have caught it (§4.4).
3. ✅ **The front**, so a daemon can be written in any language, plus the loud-failure and
   stale-socket fixes it needs to be trustworthy — then the config block's daemons migrate onto the
   surviving transport for free. — **done 2026-08-13**, plus `request_end`, which this item did not
   anticipate and hazard 2 needed.
4. ✅ **The server-side spec**, labelled plainly as the *unsupervised* path — and reachable only by a
   loophole yolo itself ships, since a pack-shipped one must take the front (§3.2). — **done
   2026-08-13**, corrected 2026-08-14.
5. ✅ **The kind itself**, whose gating item is the **total claim enumeration** — nothing crosses
   without a string you saw. Its **platform-support declaration** (§3.3) lands here too, sharing one
   mechanism and one message with the inert-backend report. — **done 2026-08-14**, together with the
   fourth name-exclusivity pre-flight, the reserved-name set composed once, the seven-surface
   convergence, retirement-on-deselect and its `prune` sweeper, and the schema extracted as a leaf
   (`internal/loopholedecl`, which is OQ-LP1 resolved by extraction). **The pack-shipped SUBSET is
   now WIRED at three seams** (discovery, `pack lint`, `yolo check`'s walker) — it had a window where
   it was implemented, tested and reachable from nothing, during which a manifest with all four
   violations was discovered, Active, and produced `-v /:/ctx/hostroot` plus
   `-e LD_PRELOAD=/ctx/evil.so` while `pack lint` printed "pack ok". Wiring it then exposed the
   rules' own limit: they have no spelling for a runtime-dir socket (**OQ-LP14**, §5.1).
6. ✅ **The per-launch disclosure** moved to print *before* the daemon starts rather than a phase after
   it. For a read, after is cosmetic; for an execution, after is a notification that something
   already happened. *(Commit-anchored claims are gone from this item — LP13 removed the mechanism
   they belonged to.)* — **done 2026-08-14**, and generalized past what the item asked for: the
   covered set is DATA, exhaustive over the kind set by test, because the hardcoded set was already
   wrong for **two shipped kinds** (`program via installer` and `briefing after host:` are host reads
   that appeared at no launch). The read/exec split is per **claim**, not per kind. **G2a landed too**
   — the claim string is the raw, unelided, placeholder-preserving argv, pinned by two tests, because
   an elided argv collapses two different daemons onto one approval and an expanded one makes the
   approval machine-specific. **One residual, and it is a DECISION: G2b** — `ApprovedAt` is written and
   read by nothing, so a fetched pack at a mutable ref whose daemon *file* changes under an unchanged
   argv re-installs with no prompt. Whether to close it is OQ-LP8; not implemented on purpose.
7. ✅ **`audio` as a real official pack** (LP11), which is both the end-to-end proof and the first step
   of the bundled-to-packs consolidation. — **done 2026-08-14** as `packs/audio`, and it makes §6's
   last bullet false: a pack-shipped loophole now exists and runs. **Two framings this item had were
   wrong.** It is not a "first step of consolidation" in the sense of *replacing* anything — the pack
   is **additive** and the bundled copy is deliberately kept, because the name is reserved, podman
   refuses a duplicate bind destination, and above all **a pack cannot express `audio`'s sockets at
   all**. And it is not the full end-to-end proof: the pack ships no daemon, and an embedded pack's
   approval is true by construction, so the prompt and the spawn remain unexercised. What it did prove
   is the value of dogfood — it surfaced two live defects and the design finding now filed as
   **OQ-LP14** (§5.1).
8. ✅ **Nested-jail user scope** (LP9), per your three-part split: the generated preflight file (kills
   the false `yolo check` errors), the nesting-only launch input (written only where nesting
   exists), and the `--user-layer` CLI arg (explicit privilege futzing; safe everywhere by
   gate-placement Test 1). The "develop loopholes in a jail" ruling depends on it. — **done
   2026-08-14**, with four corrections in §8's OQ-LP9 Landed note: the false-error example this doc
   named was already fixed while three louder classes were not, five keys earn both memberships,
   the nested-launch file was written and never read (found by two real nesting levels, missed by two
   unit tests), and the arg had to be GLOBAL rather than a `run` flag. The development path it exists
   to enable is verified in a real nested container.
