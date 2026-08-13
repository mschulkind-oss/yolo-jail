# Loopholes in packs — the system-level picture

**Status:** DESIGN, 2026-08-13. Not built. This is the readable half of
[`loophole-packaging.md`](loophole-packaging.md), written to be commented on.

**What this doc is for.** The detailed design is written for whoever implements it — it cites line
numbers, names functions, and argues from measured code. That is the right shape for building and
the wrong shape for deciding. This doc carries the same design at the level where the choices
actually live: what we are adding, what it costs, what it opens, and the five rulings it needs from
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

**Two new questions, both in §8: OQ-LP10** (retire the home directory) and **OQ-LP11** (bundled
loopholes become official packs). Neither blocks the 15th kind — the kind is what makes either one
*possible* — so this is a direction to rule on, not a prerequisite. The rest of this doc describes
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

**One real cost, stated as a requirement rather than a footnote.** The front does not propagate the
client's end-of-stream upstream, so a daemon that reads its request *to EOF* works when tested
against a bare socket and hangs forever behind the front. That is a behaviour change the author
cannot see from the outside, so it is a named deliverable in the guide.

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
| **G1** | the config block's host-execution surface becomes **user-scope-only** — so an agent-editable file cannot reach it |
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

### 4.3 Things the design names rather than solves

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
threat is a workspace config turning it off silently. G1's scoping, plus a launch-time line naming
*who* turned something off, are what actually keep it.

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

Seven rulings — five from the design, two raised by your own review. The other four open questions
are recorded in the detailed doc (§9) and do not need you: one is a technical placement choice, one
is a one-way door I am flagging rather than opening, one resolves itself when a real pack wants it,
and one belongs to the `guest` notch work.

**LP10 and LP11 are a direction, not a prerequisite.** Neither blocks the 15th kind — the kind is
what makes either one *possible*. But they change what the end state looks like, so ruling on them
early avoids building toward a shape you do not want.

| | Question | My read |
|---|---|---|
| **OQ-LP10** 🆕 | **Retire the hand-placed loophole directory in your home?** A `file://` pack does the same job with the same authority, and the directory is the one channel that starts a host daemon with no selection step at all. | **Yes, retire it** — after the kind ships, so there is somewhere to go. It also forces `loopholes enable/disable` off its one special case and into config state for every source, which the design already wants. |
| **OQ-LP11** 🆕 | **Do bundled loopholes become official packs?** `AGENTS.md` says *"AGENTS ARE PACKS. Core does not know what an agent is."* A loophole registry plus a magic directory plus a config block is the world before that move. | **Yes in principle, and not yet.** One real blocker: the broker's manifest is not what runs, so packaging it is ceremony over a thing that ignores the package. Do `audio` first as the proof, keep the broker where it is until its spawn and relay have manifest vocabulary. |
| **OQ-LP2** | **Do the config block's host-execution keys become user-scope-only now?** Covers the command, the environment overrides in both shapes, the doctor command, and `enabled` for anything with a daemon. | **Yes.** Independent of everything else and the biggest single risk reduction here. It is a breaking change for everyone who followed the shipped guide — which literally calls the block "the workspace-scoped entry point" — and a scope error refuses the *whole launch*, so it needs one release of warn-then-error. |
| **OQ-LP3** | **A `file://` pack runs a host daemon with no prompt, ever.** Leave the origin model coherent, or special-case this one kind? | **Leave it** — the origin model's coherence is worth more than a special case. But a host daemon sharpens it, and a one-time confirmation is defensible. Either way it must be *documented*. |
| **OQ-LP6** | **Is the capability system still worth building for one auto-activating bundled loophole?** | **Genuinely open.** The extension-point argument cuts both ways. |
| **OQ-LP8** | **How does an execution approval survive a moving pin without re-prompting forever?** Commit-anchoring, or a digest of the loophole's own files? | **Commit-anchor now, digest later if the friction is real.** The friction is visible and recoverable; content-blind approval is neither. |
| **OQ-LP9** | **Does the scope error downgrade in-jail**, the way the `agents` key hard-errors on the host and warns in-jail? | **Yes, downgrade.** `/workspace` is live-mounted, so in-jail `yolo` and nested jails break identically otherwise. |

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
