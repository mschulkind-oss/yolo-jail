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
| bundled in the yolo binary | yolo's own three | fine |
| a hand-placed directory in your home | one local loophole, yours | works; no fetch, no version, no approval, no manifest travelling with the code |
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

Three things fall out of that choice, and they are the substance of the design:

1. **The daemon must not have to be Go.** §3.
2. **Everything the loophole reaches on your host must become something you were asked about.** §4.
3. **Turning it on must stay a decision you made.** §5.

---

## 3. The interesting decision: yolo runs the TLS front

A loopback-TLS loophole needs a *server*: something that listens, authenticates a per-jail token,
and publishes an endpoint file with the right permissions. Today only yolo can be that server. Three
ways out, and the one that looks most generous is the most restrictive:

| Option | What it means | Verdict |
|---|---|---|
| **Export the Go package** | third parties link yolo's server code | ❌ **narrows the author's language to exactly one.** The option that looks like opening it up closes it hardest |
| **Publish the wire spec** | third parties implement the server themselves | ⚠️ necessary, insufficient — see below |
| **yolo runs the front** ✅ | the daemon binds a plain Unix socket; yolo puts an authenticated TLS listener in front of it and publishes the endpoint file | **the supported path** |

**Why spec-only cannot be the only path: an enforcement asymmetry.** On the *client* side a sloppy
implementation harms only itself. On the *server* side every security-critical property is
invisible to yolo — whether the endpoint file was written `0600`, whether the private key was
persisted, whether the token compare was constant-time, whether the frame length was capped before
allocating. yolo cannot detect a single violation of any of them.

That is tolerable for a config entry someone hand-wrote on their own machine. It is a different
proposition for a **distributed artifact**: spec-only means shipping other people's TLS-server and
credential-minting code to strangers' machines, where a `0644` publish is undetectable by the
framework and invisible to the user. The front distributes the *capability* and keeps **one**
implementation of the obligation; spec-only distributes the *obligation* and keeps no ability to
check it.

**What the front buys the author:** anything that can bind a Unix socket and read a length prefix —
Python, Node, Rust, Go, a shell script with `socat`. The `nc`-era simplicity the protocol doc mourns
is restored on the server side.

**What it costs, and both are stated as requirements rather than footnotes.** The front never
propagates the client's end-of-stream upstream, so a daemon that reads its request *to EOF* works on
a bare socket and hangs forever behind the front — a behaviour change the author cannot see. And
the front sees bytes rather than requests, so a spliced third-party daemon's traffic is not in
yolo's access log. That second one is a known gap and also the natural home for the crossing audit
log later, since every third-party crossing would pass through one yolo-owned process.

The manifest says which half the author picked (`publishes: "endpoint"` — I publish it myself — or
`publishes: "socket"` — you wrap me). The jail dials the same thing either way. **And this is what
revives the config block**: a hand-written config daemon becomes "loopback-TLS, publishes a socket,"
which is *true of it*, needs no retired vocabulary, and does not change its argv or its behaviour.

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

**So the broker stays bundled — and `bundled` is not a legacy channel to migrate away from.** It is
the channel for *the things yolo itself is accountable for*, exactly the way pack staging already
separates official packs from the rest. Two channels, one loader, and the difference is who is
accountable.

| Loophole | Verdict | Why |
|---|---|---|
| `claude-oauth-broker` | stays bundled | it auto-activates by design; its real spawn bypasses its own manifest; its per-jail relay has no manifest vocabulary at all |
| `host-processes` | stays bundled | its client is a binary baked into the image — a pack cannot ship it |
| `audio` | stays bundled, **becomes the worked example** | no daemon, no host execution: the one bundled loophole a pack could carry with zero new vocabulary |

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
  looking provisioned and configuring nothing.
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

Five rulings. The other four open questions are recorded in the detailed doc (§9) and do not need
you: one is a technical placement choice, one is a one-way door I am flagging rather than opening,
one resolves itself when a real pack wants it, and one belongs to the `guest` notch work.

| | Question | My read |
|---|---|---|
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
4. **The server-side spec**, labelled plainly as the *unsupervised* path.
5. **The kind itself**, whose gating item is the **total claim enumeration** — nothing crosses
   without a string you saw.
6. **The approval invariants**: commit-anchored execution claims, and the per-launch disclosure
   moved to print *before* the daemon starts rather than a phase after it. For a read, after is
   cosmetic. For an execution, after is a notification that something already happened.
7. **The `audio` example pack**, as the end-to-end proof.
