# Emptying `bundled_loopholes/` — the broker, the identity rule, and the proving ground

**Status:** DESIGN SETTLED, 2026-08-15. **Two of the three conversions shipped 2026-08-18** — `host-processes` (§12, the proving ground) and `audio`; `bundled_loopholes/` now holds only this doc's own subject. **§10 step 4 shipped 2026-08-19**: §6.1's mechanism gap is closed by `host_daemon.scope` (see §6.1), the broker declares `publishes: "socket"` + `scope: "host"`, and `internal/brokerrelay` is deleted. **What is left is step 5 alone** — moving the manifest into `packs/claude`, with the reservation trap §6.1's warning names. Code claims verified against the tree on 2026-08-15 unless dated otherwise.

**Four review rounds, same day.** Round 0: §3.1 grew from a deferral into a design — pack-shipped binaries are wanted as a general capability. Round 1 **retracted this doc's central claim** (§5.2a): the front is *not* a component that never parses. Rounds 2 and 3 settled identity (§5.5) by rejecting, in turn, my payload stamp, my mandatory version of it, and my `framed`/`raw` compromise — arriving somewhere none of the three reached: **yolo never parses a daemon's payload at all.** The title has changed twice and the scope grew from one loophole to the whole channel.

**Three rulings, each of which overrules a leaning of mine:**

> 1. **The broker move and the pack-shipped binary capability ship TOGETHER**, not one then the other (OQ-BP1).
> 2. **`bundled_loopholes/` has no inhabitants at the end of this work sprint** (OQ-BP4) — the goal is the channel's retirement, not one fewer entry in it.
> 3. **Every connection stays raw and yolo prepends its own connection preamble** — default on, `preamble: false` for a dumb pipe (OQ-BP2, §5.5). yolo never parses a daemon's payload. Breaking changes are in scope.

**All open questions that gate implementation are now answered.** What remains open (OQ-BP5, OQ-BP6) belongs to the binary capability and blocks nothing in §12.

**Scope note.** That second ruling makes this doc one of three conversions rather than a self-contained change, and the other two are not designed here: `host-processes` needs the same `publishes` change with none of the relay complexity, and `audio` cannot become a pack at all until **OQ-LP14** is answered. §11 states what each needs and what is genuinely blocking; the work belongs to the sprint, not to this document.

**The short version.** [`pack-code-separation.md`](pack-code-separation.md) §4 named two things that must be true before the `claude-oauth-broker` can ship as a pack: a **jail-side daemon shippable as a binary**, and the **per-jail relay** becoming expressible. The first is essentially already built — the container mount, the manifest token, the loader and the pack-shipped subset all already permit it, and nobody noticed because nothing has tried. The second reduces to three of four jobs the framework-owned front *already does*, plus one it does not: the relay decodes each request to stamp a host-asserted `jail_id` into it. **That job is now deleted rather than moved.** yolo prepends its own **connection preamble** to every authenticated connection and never inspects a payload byte again — which removes the relay's reason to exist, removes the framework's only obligation to re-serialize someone else's JSON byte-identically, and gives every daemon, in any language, one uniform thing to read. `internal/brokerrelay` goes; nothing replaces it.

**The most important section is §5.5** — everything else is inventory and consequence.

**Reads with:** [`pack-code-separation.md`](pack-code-separation.md) (why this is being done at all; its §4 is what this doc corrects), [`loophole-packaging-overview.md`](loophole-packaging-overview.md) (the `loophole` kind, the front, and the pack-shipped subset — §3 is the contract this leans on), [`loophole-packaging.md`](loophole-packaging.md) (the implementation authority, and OQ-LP11/OQ-LP14), [`agent-credentials.md`](agent-credentials.md) (§2.5, why a broker exists at all), [`loophole-transport.md`](loophole-transport.md) (why loopback-TLS is the only hop).

---

## 1. Verdict up front

**Ship it as a pack, and expect the work to be smaller than §4 estimated — but concentrated somewhere §4 did not look.**

Three claims, each argued below:

- **P1. Jail-daemon-as-binary is not a missing mechanism.** `{jail_loophole_dir}` already resolves to a container path, the module dir is already bind-mounted there `:ro` **without `noexec`**, `nix-ld` already runs non-nix dynamically-linked binaries, and the pack-shipped subset does not restrict `jail_daemon` at all. What is missing is *selection, delivery and trust* for the binary — designed in §3.1 on review request, and **not on the broker's critical path**, because an official pack may keep a baked daemon. (§3)
- **P2. The relay is 3/4 redundant with the front.** Per-connection upstream dial, TLS termination, endpoint publication, and layer-attributable failure are all in `svcendpoint` already. (§4)
- **P3. The relay's last job is replaced by something smaller than itself.** Ruled in §5.5: yolo never parses a daemon's payload, and instead prepends one **connection preamble** to every authenticated connection — so `readFirstMessage`, `stampJailID` and their fallbacks are deleted rather than relocated, and the framework stops re-serializing someone else's JSON to keep a frozen wire contract. (§5)

**What I would not do:** treat this as a prerequisite for the rest of `pack-code-separation.md`. Its steps 1–3 are independent and should land first regardless of how this goes.

---

## 2. What the broker is today

Five hops, four processes, two of which are baked subcommands rather than binaries:

```
  IN THE JAIL                      │        ON THE HOST
                                   │
  claude ──(1)──► 127.0.0.1:443    │
                  yolo-jaild       │
                  oauth-terminator │
                       │           │
                       └──(2) loopback-TLS, per-jail bearer token ──►│
                                   │        yolo internal daemon broker-relay
                                   │        ├─ front: TLS, token check, publish
                                   │        │   endpoint file (0600, per jail)
                                   │        ├─ splice ──► its own host-only
                                   │        │   AF_UNIX socket in /tmp
                                   │        └─ stamp jail_id into frame #1 ──(3)──┐
                                   │                                              │
                                   │        yolo internal daemon claude-oauth-broker
                                   │        ├─ flock (serializes refreshes) ◄──(4)─┘
                                   │        ├─ upstream refresh + background tick
                                   │        └─ writes the SHARED creds file ──(5)──►
                                   │            ~/.claude-shared-credentials/
```

| Piece | Package | Runs as | Per |
|---|---|---|---|
| in-jail TLS terminator | `internal/oauthterminator` | `yolo-jaild oauth-terminator` | jail |
| per-jail relay (front + stamp + dial) | `internal/brokerrelay` | `yolo internal daemon broker-relay` | jail |
| host singleton | `internal/oauthbroker` | `yolo internal daemon claude-oauth-broker` | host |
| lifecycle + `yolo broker` CLI | `internal/broker` | in-process | host |

**What the manifest already declares** (`bundled_loopholes/claude-oauth-broker/manifest.jsonc`, verified 2026-08-15): `serves`, `transport: loopback-tls`, `intercepts`, `broker_ip`, `ca_cert: {state}/ca.crt`, `state_files`, `requires.command_on_path`, `host_daemon.cmd`, `jail_daemon.cmd`, `doctor_cmd`. That is nearly the whole surface. **What it does not declare is the relay** — `relayEnsure` is called from the run pipeline (`internal/cli/run/loopholesruntime.go:790-797`), keyed per jail by a hash of the container name, with its own pid file, lock and socket. Nothing in any manifest describes it.

**P1 of the existing design still holds and is not reopened:** the broker exists because Anthropic mints single-use refresh tokens, so concurrent consumers must be serialized by a host-wide flock ([`agent-credentials.md`](agent-credentials.md) §2.5).

---

## 3. Blocker 1 re-examined — the jail-side binary is already expressible

[`pack-code-separation.md`](pack-code-separation.md) §4 says: *"A pack-shipped binary would have to be mounted `:ro` from the loophole module dir and be executable with a runtime the image bakes — which is the 'native binary' question `loophole-packaging-overview.md` §3.3 names and does not design."*

Every clause of that is already satisfied. Evidence, verified 2026-08-15:

| What §4 says is needed | State in the tree |
|---|---|
| the module dir reachable from inside the container | **Built.** `-v <module>:/etc/yolo-jail/loopholes/<name>:ro` (`internal/loopholes/runtime.go:144-156`) |
| a way to *name* that path in the manifest | **Built.** `{jail_loophole_dir}` is legal in `jail_daemon.cmd` and refused in host fields, with the mount point as one constant (`internal/loopholedecl/tokens.go:5-37`) |
| the mount to permit execution | **Satisfied by omission.** The `-v` is `:ro` with **no `noexec`** |
| a runtime for a non-nix binary | **Shipped 2026-07-22.** `nix-ld` is the FHS interpreter; that is precisely its job ([`../plans/nix-ld-dynamic-linking.md`](../plans/nix-ld-dynamic-linking.md), IMPLEMENTED) |
| the pack-shipped subset to permit a `jail_daemon` | **Never restricted it.** The subset constrains jail *env*, bind mounts, `ca_cert`, `requires` and `publishes` (`internal/loopholedecl/packshipped.go`) — there is no `jail_daemon` rule |
| a way to say "Linux/amd64 only" | **Landed 2026-08-14** as `platforms`, closed-list `<goos>[/<goarch>]` |
| the daemon to be spawned generically | **Built.** `jail_daemon` payloads become `YOLO_JAIL_DAEMONS` for `yolo-jaild supervise` with no per-loophole code (`internal/loopholes/runtime.go:248-251`) |

So the honest statement is: **a pack can already ship a jail-side daemon binary; nothing has ever tried.** That is an unexercised path, not a missing one — the same shape of finding as agy's never-run credential hook in [`pack-code-separation.md`](pack-code-separation.md) §3.2.

### 3.1 What is actually unresolved here

Not mechanism — **distribution**. *(Section rewritten after review: the maintainer wants pack-shipped binaries supported as a general capability, with per-arch selection and ideally dynamic download, so this is now a design rather than a deferral.)*

Three sub-problems that are easy to run together and should not be: **selection** (which file runs on this machine), **delivery** (how the file got there), and **trust** (what approving it means). Only the third is hard.

#### Selection — a convention, not a mechanism

`platforms` already declares *support*; it does not say which file to execute. The convention should be the one the repo already uses for its own cross-builds — `dist-go/<goos>-<goarch>/` from `just build-go`, and `bin/linux-<arch>` for a shipped bundle's prebuilt short-circuit:

```
loophole/broker/bin/<goos>-<goarch>/terminator      # e.g. bin/linux-amd64/terminator
```

`{jail_loophole_dir}` then resolves as it does today and the runtime substitutes the pair, so a manifest writes one path and gets the right file. A missing build for this machine is an **honest inert report** through the mechanism `platforms` already established (`loopholes.InertNote`), not an exec failure at daemon start. This part is a naming rule plus a lookup; it carries no trust weight.

#### Delivery — three options, and the digest is what separates them

| | Option | Verdict |
|---|---|---|
| **A** | **Checked into the pack tree.** `bin/<goos>-<goarch>/…` committed alongside the manifest | ✅ **Works today with zero new mechanism, and it is already pinned.** The lockfile records the resolved commit SHA (`packsrc.LockEntry.Commit`), so a committed binary is pinned byte-for-byte by machinery that exists. Cost is repo weight: every platform in every clone, forever |
| **B** | **Declared download.** the manifest names a URL per platform — a GitHub release asset — plus a **mandatory `sha256`**; yolo fetches at `pack install`, verifies, and caches by digest | ✅ **The right general mechanism**, and the one the comment asks for — with the digest as a hard requirement, for the reason below |
| **C** | **Declared build step.** the pack names a command that produces the binary at install time | ⚠️ **Not now, and possibly never on the host.** See below — it is a different risk class *and* it destroys the property B exists to preserve |

**Why the digest is not optional, stated as a requirement:**

> **P4. A pinned pack must pin everything that runs.** Today the lockfile's commit SHA pins the pack's whole tree — that is what "pinned" means here. A URL is not in the tree. A GitHub release asset can be deleted and re-uploaded, and a tag can be moved, both without changing any commit. So a manifest that names a URL *without* a digest silently converts "pinned pack" into "pinned manifest, unpinned payload", and the thing left unpinned is the executable. A `sha256` written **in the manifest** restores the property transitively: the commit pins the manifest, the manifest pins the bytes.

That also answers *when* the fetch happens: at **`pack install`**, never at launch. A launch-time fetch would mean no network is no jail, and would move the moment-of-trust from "when you approved this pack" to "every time you start one". Cache the verified artifact under yolo's state tree keyed by digest — not in the module dir, which is the git tree and is mounted `:ro`.

**Why the build step is a different question.** A build is arbitrary code execution, so it lands in the sharpest existing category rather than a new one, and the precedent is already in the schema: `packdecl.Install.InstallerURL` is *"a curl-piped installer … the sharpest thing a manifest can name: a URL whose contents run as a shell script"*, honored only under the origin rule — **a fetched pack cannot introduce one** (`packdecl.go:111-122`). A build step is that, plus the loss of P4: builds are not bit-reproducible in general, so there is no digest to pin and no way to say what will run. My read is that B covers the real need and C should wait for a case B cannot serve. It is OQ-BP5 because the comment explicitly asks for it and because "both" is a coherent answer.

#### Trust — the split that matters, and it is not jail-vs-fetched

The comment's framing treats "shipping a binary" as one thing. It is two, and they sit on opposite sides of the boundary this whole design exists to defend:

| | Runs where | Risk if hostile | Existing category |
|---|---|---|---|
| **`jail_daemon` binary** | inside the sandbox | bounded by the jail — the same blast radius as any `npm install` the agent already does | comparable to `InstallerURL`, which is already origin-gated |
| **`host_daemon` binary** | on the real machine, as a daemon | unbounded — this is *the* thing the four gates exist for | must go through host-execution approval: enumerated claim, y/N at install, recorded in the lockfile |

So the mechanism should be one schema and **two gates**: shipping a jail-side binary is roughly as sharp as what a pack can already do, while shipping a host-side one is a host-execution grant and must be disclosed as such. That asymmetry is worth building in from the start, because a single "packs may ship binaries" switch would quietly grant the second while the reader is thinking about the first.

#### And the interim answer for *this* broker

None of the above blocks the broker move. `jail_daemon.cmd` may keep naming a baked subcommand for an **official** pack, because [`loophole-packaging-overview.md`](loophole-packaging-overview.md) §1.1 already rules that a baked client is fine for one. So the broker can become a pack **now**, on baked daemons, and adopt the binary mechanism when it exists — which is the sequencing OQ-BP1 asks about, restated as "does the broker wait for the general capability?" rather than "does the capability exist?"

---

## 4. Blocker 2 — the relay, and how little of it is irreducible

The relay does four things (`internal/brokerrelay/brokerrelay.go:1-45`, `handle` at :159):

1. **Terminate loopback-TLS and publish the endpoint file** the jail reads (0600, per jail, carrying that jail's bearer token).
2. **Dial the broker's socket per connection**, so a restarted broker (new socket inode) is picked up on the next request rather than 502-ing the jail.
3. **Stamp `jail_id`** into the single framed JSON request, overriding any client-supplied value.
4. **Attribute failures to a layer** — endpoint missing / dial refused = relay layer; accepted-then-zero-frames = broker layer — including a bounded drain so a dial failure surfaces as clean EOF rather than `ECONNRESET`.

Three of those four are already the framework's, for every `publishes: "socket"` daemon:

| Relay job | Front equivalent | Evidence |
|---|---|---|
| TLS + token + publish endpoint | `listenWith(publishPath, advertiseHost, CrossingViaFront)` | `svcendpoint/front.go:42-48` |
| per-connection upstream dial | `splice` dials inside the accept-loop goroutine | `svcendpoint/front.go:56-85` |
| layer-attributable failure | `CrossingUnreachable` + `CrossingReasonUpstreamDial` on the audit record | `svcendpoint/crossing.go:61-92`, `front.go:85-97` |
| **stamp `jail_id`** | **none today** — but see §5.2a, this is a gap in the code, not a principle | `front.go:44-46`: *"splice does not parse the stream"*; `crossing.go:194`: parsing the request "is both unavailable at the front" |

So the folding is not "reimplement the relay in the front". It is: **switch the broker's `host_daemon` to `publishes: "socket"`, let the existing front serve it, and delete `internal/brokerrelay` — except for job 3.**

---

## 5. Jail identity — the actual design decision

### 5.1 Why `jail_id` exists

It is an **audit attribution field**, not a routing or authorization field. `internal/hostservice` reads `Request["jail_id"]` (defaulting to `"unknown"`) and emits it as `jail=<id>` in the per-request record (`hostservice.go:38,73`). The security property is stated at `svcendpoint/crossing.go:105`: the per-jail token is what *makes the relay's host-side stamp trustworthy*. Compare `hostservice`'s tier-2 behaviour, where a client-supplied `jail_id` is recorded verbatim and is explicitly untrusted (`tiers_test.go:88-90`).

So the invariant to preserve is narrow and worth naming:

> **I1.** A `jail_id` in an audit record was asserted by the **host**, never by the jail — a jail cannot forge another jail's identity into the log.

### 5.2 What is actually on the wire — the concrete version

*Added after review: the sections above assume the vocabulary. This one does not.*

**Not startup metadata, and not argv.** That is the first thing to get out of the way, because it is the intuitive reading and it cannot work. The broker is **one host-wide process serving every jail on the machine** — that is its entire reason to exist, since the flock it holds is what stops two jails burning the same single-use refresh token. So there is no moment at which you could tell it "you are serving jail X": by the time it starts, it does not yet know which jails will connect, and while it runs, the answer changes from one connection to the next. The identity is a property of the **connection**, not of the process.

**What a request looks like.** The loophole protocol is one request per connection, client-first: a 4-byte big-endian length, then that many bytes of UTF-8 JSON.

```
  what the jail sends                what the broker should see
  ┌────┬──────────────────────┐      ┌────┬───────────────────────────────────────┐
  │ 00 │ {"action":"refresh"} │  ──► │ 00 │ {"action":"refresh",                  │
  │ 00 │                      │      │ 00 │  "jail_id":"yolo-yolo-jail-7f3a"}     │
  │ 00 │                      │      │ 00 │                                       │
  │ 14 │                      │      │ 2f │  ▲ inserted by the HOST, overriding   │
  └────┴──────────────────────┘      └────┴──  any jail_id the client sent ───────┘
   4-byte BE length + JSON body       length recomputed — the body grew
```

**That insertion is "the stamp".** One key, `jail_id`, whose value is the container name. It exists so the audit line the broker's request logging emits reads `jail=yolo-yolo-jail-7f3a` and that field can be *believed* — `internal/hostservice` records a client-supplied `jail_id` verbatim and treats it as untrusted (`tiers_test.go:88-90`), which is exactly what the host-side override upgrades.

**Who the three processes are, and what each one can see:**

| | Where | What it is | Sees the payload? |
|---|---|---|---|
| **terminator** | in the jail | pretends to be `platform.claude.com` on `127.0.0.1:443` so `claude`'s own HTTPS call is intercepted; forwards the result over loopback-TLS | yes — it builds the request |
| **front** | on the host | a ~90-line TLS listener (`svcendpoint/front.go`) that accepts the jail's connection, checks its bearer token, and then runs two `io.Copy` loops — client→daemon and daemon→client | **not today** — though it already frames-and-reads the token off the same stream (§5.2a) |
| **daemon** | on the host | the broker singleton: flock, upstream refresh, writes the shared creds file | yes — it parses the request |

So "the front" is not a component with opinions; it is the framework's TLS front door, and its whole contract is *"authenticate the jail, then copy bytes."*

### 5.2a ⚠ Retracted: "the front never parses, deliberately"

**Raised in review round 1, and the objection is correct.** Both earlier versions of this doc leaned on the front being a component that decodes nothing — `front.go:44-46` ("splice does not parse the stream") and `crossing.go:194` were cited as if they stated a principle the design must protect. The reviewer's question — *"it never decodes anything, except when it decodes the jail's bearer token?"* — dissolves that, and checking the code confirms it.

**The front already reads a length-prefixed frame off the same stream, before any splicing.** `verifyTokenFrame` (`svcendpoint/token.go:97-125`) does:

```
read 4 bytes  →  BigEndian.Uint32  →  cap at tokenFrameMax (4096, "CAP BEFORE
ALLOCATING")  →  read N bytes  →  subtle.ConstantTimeCompare  →  write authAck
```

under a 5-second `handshakeTimeout`, which it then clears to preserve the no-session-deadline contract. Compare `brokerrelay.readFirstMessage`: read 4 bytes, `BigEndian.Uint32`, cap at `firstMsgMax`, read N bytes, under a 5-second `firstMsgTimeout`. **These are the same operation.** The framework is not protocol-blind; it is protocol-blind *after* its own handshake, which is a description of where the splice loop starts, not a property anyone designed to defend.

Two supporting arguments I also had backwards:

- **"The payload format belongs to the daemon."** It does not. `internal/frameproto` is yolo's own package and its doc comment calls it *"the frame protocol v1 spoken between a jail-side client and a host-side loophole daemon … a frozen interop contract."* yolo owns the transport **and** the payload framing. A front that reads frame #1 is reading its own format, not reaching into someone else's.
- **"No per-request audit tier exists for fronted connections."** True, and it is a *consequence* of the splice not parsing — not a reason for it. Citing it as justification was circular.

**What actually differs, and it is the honest residue:**

| | Token frame | The stamp |
|---|---|---|
| bytes are | **consumed and discarded** — never part of the payload | **transformed and forwarded** — must come out the other side |
| a malformed read means | drop the connection (it failed auth) | **forward the original bytes verbatim** and carry on — an unparseable request is not an error, it is a request |
| applies to | every fronted connection, by construction | only daemons speaking `frameproto` — so it must be declared, not unconditional |

That is a difference in **complexity, not in kind**: transform-and-forward needs the verbatim-fallback paths that read-and-discard does not, which is `stampJailID` plus its three fallbacks — order-preserving decode, byte-identical `jsonx` re-encode, recomputed length prefix. Roughly a hundred lines, all of which already exist and work in `internal/brokerrelay`.

**What this changes downstream:** option D in §5.4 gets substantially cheaper, and invariant **I2** stops being a warning about a slippery slope and becomes an ordinary scoping rule. It also moves the weight of OQ-BP2 off "is parsing acceptable" and onto the question that was always the better one — **whether a daemon-visible `jail_id` earns any mechanism at all**, given that nothing in `internal/oauthbroker` reads it.

### 5.3 The pivot nobody has used yet

**The front already knows which jail it is talking to.** It validated a bearer token that was minted per jail and written 0600 into that jail's own directory. The identity is therefore available at the front *before any payload byte is read* — it simply has nowhere to go, because the front's contract is to splice opaque bytes.

That reframes the question from *"how does the front learn the jail?"* (it already has) to *"how does it tell the daemon, without parsing?"* — **and possibly to "does it need to tell the daemon at all?"**, which is OQ-BP2 and is the cheaper answer if `jail_id` is only ever a log field.

### 5.4 Options

| # | Option | What it means | Verdict |
|---|---|---|---|
| A | **Per-jail upstream socket** | the front dials a different Unix path per jail; the daemon infers identity from which socket the connection arrived on | ❌ **Rejected.** The broker is a host singleton by design (one flock, one creds file); giving it N listeners re-creates per-jail state in the one component that must not have it |
| B | **Framework preamble frame** | the front writes a small, framework-owned metadata frame on the upstream Unix connection before splicing | ✅ **RULED — this one, and my hedge on it was wrong** (§5.5). I wrote *"only acceptable if gated by an opt-in manifest key"*, treating "every daemon's read path changes" as the disqualifying cost. It is the **entire point**: one contract, every daemon, no parsing. The gate it needed was not opt-in but an opt-**out** for a dumb pipe |
| C | **`SO_PEERCRED`-style side channel** | identity carried out-of-band on the socket itself | ❌ **Rejected.** Peer credentials identify the *front* process, which is the same process for every jail |
| D | **Declared protocol-aware stamp** | manifest opts in: `stamps: "jail_id"`; the front parses frame #1 for daemons that ask, and only for them | ✅ **Viable and cheap** — cheaper than the first two versions of this doc claimed, since the front already frames-and-reads for auth and yolo owns `frameproto` too (§5.2a). Declared rather than unconditional, because not every fronted daemon speaks the framed protocol |
| E | **Drop the stamp; let the daemon self-report** | broker logs whatever the client says | ❌ **Rejected.** Violates I1 for a field whose entire value is that it is trustworthy |
| F | **Keep a relay-shaped shim in the pack** | the pack ships its own per-jail relay binary | ❌ **Rejected.** `host_daemon` is host-wide and keyed by loophole name; there is no per-jail daemon vocabulary, so this needs a *new* mechanism to avoid a smaller one |
| **G** | **Drop the daemon-visible field; yolo records the identity itself** | no stamp, no parse; the front writes `jail=<id>` into its own audit record from the token it already validated, and the daemon simply never sees a `jail_id` | ⚖️ **Not chosen, and it stays half-true.** Its audit half is kept unconditionally by §5.5 — yolo's tier-1 record is host-derived whatever the daemon sees. Its *other* half, denying the daemon any identity, is what B delivers better: the daemon gets one, without yolo reading its bytes |

~~**Recommendation: G, with D as the fallback.**~~ **Superseded by §5.5 — the answer is B.** Recording the path, because I recommended three different options across three rounds and each was rejected for the same reason: I kept optimizing for *not disturbing existing daemons*, and the ruling each time was that disturbing them uniformly is cheaper than any mechanism that avoids it. D and G both survive as descriptions of roads not taken; neither is the design.

**I2 survives the change and gets simpler:** a connection still emits exactly one connection-level audit record. The connection preamble is not a request and does not create a per-request tier — it is one frame at connection open, in the host→daemon direction only.

### 5.5 RULED — every connection is raw; yolo prepends a connection preamble

**The ruling, 2026-08-15, after two rejected drafts of mine.** Draft one: mandatory payload stamp. Draft two: a `framed`/`raw` declaration, stamping the framed case. Both were rejected on the same ground, and the ground is right:

> *"I don't even want framed in here. Just raw. We can still send a formatted preamble of JSON or whatever, even a frame — but then it's just raw, that's it. And you can turn off that frame if you need a dumb pipe implementation."*

**The design that falls out.** yolo **never decodes the daemon's bytes**. Every connection is an opaque stream. What yolo adds is one **connection preamble** of its own, ahead of the stream, and then it gets out of the way:

```
daemon reads:  [4B BE len][{"jail_id":"yolo-…-7f3a","service":"host-processes","v":1}]  ← yolo's, always first
               [ ...the client's bytes, byte-for-byte untouched, forever... ]           ← never inspected
```

The declaration is no longer about the payload's shape — yolo has no opinion on that — it is one bit about whether the frame is sent at all: **`preamble: true` (default) · `false` for a dumb pipe.**

**Why this is better than both drafts, concretely.** It is not a compromise; it is smaller:

| | payload stamp (rejected) | connection preamble (ruled) |
|---|---|---|
| yolo parses the daemon's protocol | yes, frame #1 | **never** |
| code needed in the framework | decode, insert, re-encode byte-identically (`jsonx` Python-parity), recompute length, plus verbatim fallbacks for oversize / timeout / non-object | **write N bytes, then splice** |
| works for audio, video, HTTP, a database socket | no | yes |
| what a third-party daemon must implement | all of `frameproto` | read one length-prefixed frame |
| `readFirstMessage` + `stampJailID` + their three fallbacks | move into the framework | **deleted** |

That last row is the one that decides it. The relay's protocol-aware trick does not get promoted into `svcendpoint` — it **stops existing**, and with it the only place in yolo that had to re-serialize someone else's JSON byte-identically to keep a frozen wire contract.

**Where it goes (P5, revised).** On the accepted-connection wrapper, `listen.go:194`, where `newCountingConn(conn, l.service, l.jail, …)` already attaches the host-derived identity. The wrapper now **prefixes the read stream** instead of transforming it, which is a smaller change than either draft: no parser, no writer, no fallbacks. One implementation covers both server shapes — a fronted daemon reads it through the front's `io.Copy`, an endpoint-publishing daemon reads it directly — so the Go server library and a third-party daemon do exactly the same thing, which is the property that keeps them from drifting.

**What it carries today** — `jail_id` (host-derived, the reason it exists), `service` (which loophole this listener is), and `v` (the envelope version). It is host→daemon only, exactly once, at connection open; it never appears in the response direction, and the jail-side client never sees it, so a client cannot forge, suppress, or even observe it.

**On the name.** It is *not* called an identity frame, deliberately: identity is what it carries first, not what it is. The frame is the framework's channel for anything it needs to tell a daemon **about the connection** before the connection's own bytes start — and naming it for today's single field would make the second field look like a violation instead of an addition. `v` is what keeps that honest.

**One disambiguation for whoever implements it:** `svcendpoint` now has two length-prefixed frames on one connection, and they are opposites. The **token frame** is client→server, pre-auth, consumed and discarded (`token.go:97-125`). The **preamble** is host→daemon, post-auth, and is the only thing yolo ever *adds* to a stream. Neither is part of the daemon's own protocol.

**What it costs, stated plainly.** Every existing daemon's read path changes once — that is the breaking change, and it is in scope. All three affected daemons are yolo's own (`hostservice`, the journal bridge, the broker), and `hostservice` reading it once covers every Go daemon written since. A daemon that declares `preamble: false` gets a genuinely dumb pipe and, in exchange, nothing the preamble carries — no identity today, and none of whatever it carries later. That is the trade, and it should be the reason to think twice before setting it.

**And the property that holds either way:** yolo's tier-1 connection record derives `jail=` from the published endpoint path (`crossingIdentity`, `crossing.go:196-201`) regardless of the declaration. Turning the frame off costs the daemon its identity, never yolo its audit trail — so `preamble: false` is not a privacy switch, and cannot be used as one.

**Downstream deletions this unlocks:** `frameproto`'s `jail_id` request field becomes vestigial (`hostservice.JailID` reads the frame instead of `Request["jail_id"]`), `yolo-ps` stops self-reporting one, and `hostservice`'s two-tier asymmetry loses its cause — tier 2's `jail=` becomes as host-asserted as tier 1's, which is what the comment at `hostservice.go:37-40` currently has to warn readers about.

---

## 6. What the pack looks like

> [!IMPORTANT]
> **CORRECTED 2026-08-18 by OQ-A10 in [`loophole-activation.md`](loophole-activation.md).** This
> section designed a separate `packs/claude-oauth-broker/`. That is **wrong**: the broker's loophole
> is a **contribution of `packs/claude`**, not a pack of its own. R6's whole argument is that the
> dependency is structural — the broker exists to serve claude — and a separate pack reinstates the
> second selection step R6 deletes. A Bedrock user's escape is `supersedes` on the
> `claude-oauth-refresh` capability, which is already built and already declared, not deselection.
> The layout below is amended accordingly.

```
packs/claude/                        # the EXISTING official pack, one more contribution
  pack.json                          # + { "kind": "loophole", "from": "loopholes/claude-oauth-broker" }
  loopholes/claude-oauth-broker/
    manifest.jsonc                   # from bundled_loopholes/, except:
                                     #   host_daemon.publishes: "socket"      (required of packs)
                                     #   requires.command_on_path: DELETED    (R3/R6 — selecting
                                     #     the pack IS the dependency the sniff approximated)
                                     # preamble defaults to true (§5.5) —
                                     # nothing to declare; only a dumb pipe writes false
```

Everything else in the manifest — `serves`, `intercepts`, `broker_ip`, `ca_cert`, `state_files`,
`doctor_cmd` — is already correct and moves unchanged. The `{state}` token is explicitly designed to
survive a restage (`loopholedecl/tokens.go:20-28`), which is what makes a pack-shipped CA possible at
all.

### 6.1 ✅ RESOLVED 2026-08-19 — `host_daemon.scope` is the vocabulary the spawn path lacked

*The blocker below was found 2026-08-18 by implementing the sprint's other two conversions and then
attempting this one. It was never a decision to make; it was a mechanism that did not exist. It
exists now — here is what was built, then the original statement of the gap, kept because it is the
argument for the shape.*

**What shipped.** One manifest key, in the grammar `publishes` and `request_end` already use:

```jsonc
"host_daemon": {
  "cmd": ["yolo", "internal", "daemon", "claude-oauth-broker", "--socket", "{socket}"],
  "publishes": "socket",
  "scope": "host"          // ← ScopeJail (default) | ScopeHost
}
```

`scope` names the dimension — *what is this daemon shared across* — rather than today's only
interesting answer, for §5.5's reason for calling the preamble a preamble: naming a key for its
current single use makes the second use look like a violation. `ScopeJail` is the default, so no
already-shipped manifest changes, and readers compare against `ScopeHost` so a dropped field costs
a spawn rather than a shared daemon nobody ensured.

Four consequences, each of which is where a defect would have gone:

- **The spawn path DISPATCHES on the record, not the name.** `startLoopholes`'
  `if name == broker.BrokerLoopholeName { o.brokerEnsure(); continue }` is gone; the branch reads
  `hd.Scope == ScopeHost` and calls `startHostSingleton`, which ENSURES the daemon (the existing
  `internal/broker` flock/pid/recheck engine, generalized by one `Deps.Argv` field so the argv
  comes from the manifest) and then fronts it with `svcendpoint.ServeFront`.
- **The framework owns the singleton's path.** `paths.HostSingletonSocket(name)` =
  `/tmp/yolo-<name>.sock`, derived from the loophole NAME and nothing else, because a singleton has
  no jail to be keyed by. For `claude-oauth-broker` that is byte-identical to the existing
  `broker.BrokerSingletonSocket`, which is why the move needed no migration — a test pins the three
  pairs equal, and it has to hold or `yolo broker status`, `yolo check` and the front would each
  reach a different file.
- **`scope: "host"` REQUIRES `publishes: "socket"`, refused at load.** An endpoint file carries one
  jail's bearer token, so a host-wide daemon publishing one would hand every jail the same
  credential. Under `socket` the daemon binds once and each jail gets its own front and its own
  token — the only shape in which "one daemon, N jails" and "one credential per jail" are both true.
- **A jail ending closes ITS FRONT and nothing else.** `startHostSingleton`'s `stop()` deliberately
  does not do what the spawned path's does (SIGKILL the process group, unlink the socket); doing so
  would cut every other live jail off its credential path.

**And the daemon had to learn to read the preamble in the same commit.** `internal/oauthbroker`
called `hostservice.ServeUnix`; behind a front that is the silent-CORRUPTION direction §12 names —
the preamble is consumed AS the request and every refresh fails. It calls `ServeFrontedUnix` now,
which is also what makes its `jail_id` host-asserted again after the relay's stamp was deleted (I1).
The knock-on: `broker.BrokerPing` could no longer speak the protocol from the host side without
forging a jail identity, so liveness became a connect probe (`SingletonReachable`) — the same
predicate every other fronted daemon already uses. The protocol round trip survives in `yolo
check`'s per-jail probe, which goes through a real front and therefore sends a real preamble.

---

*The original statement of the gap follows.*

**The chain, each link verified in the tree:**

1. A pack-shipped loophole **must** declare `publishes: "socket"`. `LoadPackLoophole` applies
   `PackShippedProblems`, and `packPublishesProblems` refuses every other value **including the
   default** (`internal/loopholedecl/packshipped.go`). So the manifest cannot move without the flip.
2. `startLoopholes` builds its spawn list from `set.ManifestHostDaemonSpecs(discovered)` — every
   loophole that declares a `host_daemon`. Under `publishes: "socket"` it calls
   `startExternalService`, which **spawns a fresh daemon** and binds it at
   `frontSocketFile(frontShortHash(socketsDir), name)` — a path **keyed per jail**
   (`internal/cli/run/loopholesruntime.go`).
3. The broker is a **host-wide singleton by design** (§5.2: *"one host-wide process serving every
   jail on the machine — that is its entire reason to exist"*), reached at the fixed
   `broker.BrokerSingletonSocket`, with `yolo broker status`/`stop`, `yolo check`'s broker section
   and `brokerEndpointIsUnpublishable` all reading that one path.

**So the conversion as designed needs a fourth thing nobody has designed: ONE daemon behind N
per-jail fronts.** §7 says the broker's `--socket` "becomes a *fronted* socket rather than a
host-to-host one" and stops there. The spawn path has no vocabulary for *ensure this host-wide
daemon* as against *spawn one for this jail*, and inventing one in a sprint whose subject is
deleting keys is the wrong shape of answer.

**What that does NOT mean.** It is not an argument against the move, and it is not blocked on a
ruling — it is §10 steps 3 and 4 (the preamble/stamp work and the `publishes` flip plus the relay
deletion) turning out to be a **hard prerequisite** for step 5 rather than merely earlier than it.
§10 already says step 4 "must not be split — a half-flipped broker is a jail with no credential
path"; what is added here is that step 5 cannot precede it either.

> [!WARNING]
> **And the reservation is still the trap.** Deleting `bundled_loopholes/claude-oauth-broker/` does
> **not** free the name: `broker.BrokerLoopholeName` is appended to `ReservedLoopholeNames()`
> **unconditionally**, from the broker's own constant, not from the bundled directory. That is what
> makes this move different from the two that shipped on 2026-08-18 — `host-processes` and `audio`
> were reserved only as bundled DIRECTORY names, so `git mv` retired their reservations for free,
> and a reader generalizing from them would ship a commit that refuses every claude user's launch
> (`run.PackLoopholeNameConflicts` is fatal). The reservation, the `startLoopholes` name special-case
> and the contribution must land in ONE commit.

## 7. What this deletes, what it costs, what it forecloses

**Deletes:** `internal/brokerrelay` (~4 files plus its lifecycle in `loopholesruntime.go` — pid file, lock, socket path, reaping, the `relayKill` ordering comments) · one of the four bundled loopholes · the last consumer of the `relayEnsure` special case in the run pipeline.

**DELETED 2026-08-19, and the tally came out larger than this line predicted.** Also gone: the `broker-relay` entry in `yolo internal daemon`'s dispatch, `Options.RelayKillGrace`, the run pipeline's orphan-relay backstop reap and its piggyback on the live-container enumeration, the attach path's relay healing, the pre-loopback-TLS "spare a live legacy relay" upgrade decision, and `svcendpoint`'s only `NoPreamble` user. `internal/prune`'s `ReapRelayOrphans` is KEPT for one release and re-documented as a legacy sweep — a host upgrading has live relays in `/tmp` right now, and the run-path backstop that used to collect them went with the machinery.

**Costs:** the front gains a declared, opt-in parse (§5.4) · the broker's `--socket` becomes a *fronted* socket rather than a host-to-host one, so its threat model changes from "nothing in a jail can reach this" to "the front is what stands in front of this" — the same position every other fronted daemon is already in · one more official pack in the set the "six official packs" tests count.

**Forecloses:** nothing structural. If D proves wrong, the relay can come back as a `host_daemon` of a *different* loophole without touching the front.

---

## 8. Non-goals — what this does not license

- **Not** a general per-jail daemon mechanism. Option F is rejected precisely to avoid inventing one for a single consumer.
- **Not** a change to the transport. Loopback-TLS stays the only hop; this is about what sits behind the front.
- **Not** a widening of the pack-shipped subset *for the broker*. This loophole needs no new host-crossing vocabulary. **But the sprint goal does** — retiring the bundled channel means converting `audio` too, and that is blocked on **OQ-LP14** (§11). The distinction to hold: the broker does not depend on LP14; "no bundled loopholes" does.
- **Not** a fetched-pack broker. Everything about *this* loophole assumes an **official** pack. §3.1 designs the pack-shipped binary capability in general, but whether a **fetched** pack may ship a *host-side* binary is left open (OQ-BP6) and is not needed here.
- **Not** a general artifact-caching or dependency system. §3.1's download is one verified file per platform per loophole, fetched at install and keyed by digest — it is not a package manager, and it should not grow into one.
- **Not** a change to how credentials are merged, harvested or written. That is [`pack-code-separation.md`](pack-code-separation.md) §5, decided separately and landing first.

---

## 9. Risks

| Risk | Mitigation |
|---|---|
| The preamble accretes fields until it is a second protocol | It is **meant** to grow — that is why it is not named for today's contents — so the discipline is not "keep it empty" but "keep it a versioned envelope": `v` is mandatory, the key set is closed *per version* and reviewed as a schema change, and a daemon that does not recognize a version fails loudly rather than guessing. Growth is a decision each time, not a slope |
| `preamble: false` becomes the way to dodge auditing | It cannot: tier-1's `jail=` is derived from the published endpoint path regardless of the declaration (§5.5). Turning the frame off costs the daemon its identity, never yolo its audit trail |
| The frame silently breaks a daemon nobody remembered to update | It is a **breaking change on purpose** and the blast radius is three in-tree daemons; the migration test is that a daemon which does NOT read the frame fails loudly at its first request rather than misparsing one — the frame is length-prefixed, so a naive reader sees a length it cannot use, not a plausible-looking request |
| Deleting the relay loses the bounded-drain behaviour that makes a dial failure a clean EOF | It is not lost — `front.go`'s splice already distinguishes the case and marks `CrossingUnreachable`; the drain semantics must be **pinned by a test** before the relay is deleted, not after |
| A fronted broker socket is reachable by something on the host that the host-only relay socket excluded | The socket path stays where it is (`/tmp`, host-only, 0600); the front is an additional listener, not a relocation |
| The unexercised jail-binary path (§3) turns out to have a real defect once something uses it | Prove it with a throwaway pack shipping a two-line binary **before** committing to the broker move — it is the cheapest possible test of P1 |
| `platforms` forces the pack to enumerate arches yolo's own release does not build | Now on the critical path, since the two ship together (OQ-BP1): the release process must produce the matrix the manifest declares, or `platforms` must narrow to what it actually builds. Declaring more than you build is the failure mode — it turns "unsupported here" into "supported, missing" |
| **Shipping both at once means the sprint fails together** — a stalled binary capability holds a finished broker move hostage | Keep them separable *in the tree* even though they land in one sprint: the broker's manifest works on a baked daemon, so the binary work can slip to a follow-up commit without reverting anything. The ruling is about the sprint's end state, not about coupling the commits |
| A declared download turns `pack install` into a network-dependent step that can fail | Fetch at install only, never at launch (§3.1), so a failure lands where the user is already waiting on the network — and a verified artifact is cached by digest, so a reinstall of the same pin is offline |
| A digest mismatch is treated as a transient error and retried past | It is an **integrity failure**, not a fetch failure: refuse the install, name both digests, and do not fall back to the cached copy — the whole point of P4 is that the bytes are the pin |

---

## 10. Sequencing

What I would build, in order.

**First, settle OQ-BP1**, because it decides whether §3's distribution question exists at all. If the answer is "an official pack may keep baked daemons", steps 2 and 3 shrink to a manifest move.

**Second, prove P1 cheaply** — a throwaway local pack whose `jail_daemon.cmd` is `["{jail_loophole_dir}/bin/hello"]`, carrying a statically linked two-line binary. This is an afternoon, and it converts "the mechanism appears to exist" into "the mechanism works". Do this even if OQ-BP1 says the broker keeps its baked daemon: the finding is worth having on its own.

**Third, the stamp.** Add the declared stamp to the front (§5.4-D) with I1 and I2 as its tests, while the relay is still in place and still the thing running. Two implementations of the stamp can coexist for exactly one commit.

**Fourth, flip the broker to `publishes: "socket"`** and delete `internal/brokerrelay` plus its lifecycle in `loopholesruntime.go`. This is the step that must not be split — a half-flipped broker is a jail with no credential path. **SHIPPED 2026-08-19**, in one commit, and it needed §6.1's `scope` vocabulary plus two things this sequencing did not name: the daemon moving to `ServeFrontedUnix` (or the front's preamble is eaten as the request), and the host-side liveness ping becoming a connect probe (or every healthy broker reads as dead and is respawned on every launch).

**Fifth, move the manifest into an official pack** — `packs/claude`, per OQ-A10, not a pack of its own — and retire the bundled copy. This is also the step where OQ-LP11's consolidation finally gets one channel emptier, which it has been owed since 2026-08-14.

> **Step 5 CANNOT precede steps 3–4, measured 2026-08-18 (§6.1).** The pack-shipped subset requires `publishes: "socket"`, and yolo's spawn path answers that by spawning a daemon **per jail** at a per-jail socket — while the broker is a host-wide singleton by design. The ordering above already implies it; what was not stated is that it is a hard dependency rather than a preference, and that attempting 5 first produces a manifest yolo refuses to load rather than a broker that runs twice. **Both dependencies are discharged as of 2026-08-19**, and step 5 is unblocked.

**Sixth — and it landed FIRST, on 2026-08-18, because it is independent of all five:** gate `brokerEnsure` and `ensureBrokerRelay` on the loophole record (OQ-A11). Until then the singleton ran on every launch for every user with no lookup at all, while the jail was wired to it only when the loophole was Active. Doing it early matters for a reason the ruling names: after the move, a jail that does not select `packs: ["claude"]` has no broker in any surface, and yolo spawning the singleton anyway would be a daemon none of its own surfaces name.

**And the binary capability (§3.1) runs alongside, not after** — OQ-BP1 was ruled *ship both at once*, overruling my "adopt it later". Its own order is unchanged: selection convention, then download-with-digest, then the two gates. What the ruling changes is that it must be *finished* in this sprint rather than queued behind a working broker, so its slowest piece — the release matrix producing per-platform artifacts — should start early rather than last. The one thing I would preserve from the rejected sequencing is **separability in the tree**: the broker's manifest is correct on a baked daemon, so the two can land as independent commits inside one sprint without either blocking the other's review.

---

## 11. What "no bundled loopholes" additionally requires

The second ruling is the larger one: `bundled_loopholes/` should be **empty** at the end of the sprint, not shorter. That is three conversions, and only one of them is this document.

| Loophole | What blocks it becoming a pack | Size |
|---|---|---|
| **claude-oauth-broker** | ~~`publishes` defaults to `endpoint`; the pack-shipped subset accepts **only `socket`**. Plus folding the relay away — and, found by trying it, the fact that `publishes: "socket"` spawns a daemon PER JAIL while this one is a host-wide singleton (§6.1)~~ | this doc · **UNBLOCKED 2026-08-19.** `scope: "host"` closed §6.1, the manifest declares `publishes: "socket"`, the relay is deleted, and `TestBundledManifestsAreInsideThePackShippedSubset` now measures the manifest as subset-clean. **Step 5 (the move into `packs/claude`) is all that is left**, and the §6.1 reservation warning is still live for it |
| **host-processes** | The same `publishes` problem and nothing else: its `host_daemon.cmd` passes `{endpoint}` and publishes for itself, so it converts to `{socket}` + the framework front with no relay and no per-jail anything | ✅ **SHIPPED 2026-08-18** as `packs/host-processes`; the subset accepted the manifest unchanged |
| **audio** | ~~**OQ-LP14.**~~ Its `host_bind_mounts` and `requires.file_exists` both name `${XDG_RUNTIME_DIR}/pulse/native` and `pipewire-0`, which the pack-shipped path rule refused in every spelling | ✅ **SHIPPED 2026-08-18.** LP14 withdrew the rule rather than adding vocabulary; the two audio loopholes merged into `packs/audio` under the plain name, which deleting the bundled copy freed |

Three things follow, and the first is the one to notice:

- **OQ-LP14 stops being adjacent and becomes a hard dependency of the sprint goal.** I recommended the opposite one message ago (OQ-BP3: "proceed beside it"), and that recommendation is **withdrawn for the sprint** while remaining correct for the broker in isolation. The roadmap already carries a leaning for LP14 — a closed, yolo-resolved list of runtime sockets — so this is a ruling to make, not a design to invent.
- **The `publishes` subset rule is the common blocker, and it is load-bearing rather than accidental.** It exists so a pack-shipped daemon cannot get TLS, token handling or endpoint permissions wrong; converting all three onto the framework front is the same work as honoring it. Worth stating because "all three bundled loopholes violate the pack-shipped subset" sounds like a rule that is too strict, and it is not — it is a rule they predate.
- **`host-processes` is the cheap one and should go first.** It exercises the whole conversion path — subset validation, official-pack staging, the front, `doctor_cmd` — with none of the broker's complexity. If something structural is wrong with converting a bundled loophole, it will show up there for a fraction of the cost.

**And one finding worth carrying into §12:** `yolo-ps` already sends its own `jail_id` from inside the jail (`cmd/yolo-ps/main.go:121-122`), which `hostservice` records verbatim as untrusted. So the loophole chosen as the proving ground is *exactly* the one whose attribution the §5.5 ruling fixes — it stops being a client's claim and becomes yolo's assertion.

---

## 12. `host-processes` as the proving ground

**Why this one.** It exercises the entire conversion path — pack-shipped subset validation, official-pack staging, `publishes: "socket"`, the framework front, `doctor_cmd`, and the new identity rule — while having none of the broker's complexity: no relay, no CA, no intercept, no credential file, no single-use token to burn if it goes wrong. If something structural is wrong with converting a bundled loophole, it surfaces here for a fraction of the cost. **Nothing about it is blocked**: it needs no answer from OQ-LP14 (no runtime-dir sockets), OQ-BP5 or OQ-BP6 (no shipped binary — `yolo-ps` stays baked, which an official pack may do).

**What it is today.** `bundled_loopholes/host-processes/manifest.jsonc` declares `requires.command_on_path: ps`, `transport: loopback-tls`, a `host_daemon` whose cmd is `["yolo","internal","daemon","host-processes","--endpoint","{endpoint}"]`, and a `doctor_cmd`. The daemon publishes its own endpoint via `hostservice.ServeEndpoint` → `svcendpoint.Listen` (`hostservice.go:288`). The jail-side client is the baked `cmd/yolo-ps`, which reads `YOLO_SERVICE_HOST_PROCESSES_ENDPOINT` and self-reports a `jail_id` nobody trusts.

**The five changes, and what each one proves:**

| # | Change | What it proves |
|---|---|---|
| 1 | Daemon moves from `ServeEndpoint`/`{endpoint}` to `ServeUnix`/`{socket}`, with `publishes: "socket"` in the manifest | the framework front can carry a real daemon — the same flip the broker needs, without the relay |
| 2 | The connection preamble is prepended on the accepted connection (**P5**), `preamble` defaulting to true | the §5.5 rule works for an endpoint-shaped daemon *and* a fronted one, from one implementation — and `hostservice` reading it once covers every Go daemon |
| 3 | `yolo-ps` stops self-reporting `jail_id` | the field's only source is now the host — and tier 2's `jail=` becomes as trustworthy as tier 1's |
| 4 | Manifest moves to `packs/host-processes/loophole/host-processes/`, an official pack; bundled copy deleted | a bundled loophole can become a pack at all — staging, selection, exclusivity pre-flight, `doctor_cmd` |
| 5 | `requires.command_on_path: ps` and the workspace `host_processes.visible` list keep working | the pack-shipped subset's `requires` rule accepts a real manifest unchanged |

**Settled decisions this rests on**, so implementation does not have to re-litigate them:

- **The connection preamble's home is the accepted-connection wrapper**, not `ServeFront` (§5.5, P5) — one implementation for both server shapes, and a prefix rather than a parse.
- **`preamble` defaults to true**, so no manifest declares anything to keep working. ~~`false` exists for a dumb pipe, of which there are none today.~~ **Half wrong, corrected 2026-08-15:** true of *manifests*, false of the **config** surface. Every `loopholes:` entry in a `yolo-jail.jsonc` that carries a `command` is given `publishes: socket` + loopback-TLS unconditionally (`discover.go:60-73`), and that code's own comment calls such a daemon *"a THIRD-PARTY PROGRAM yolo did not write"*. Those are dumb pipes **by construction** and there are real ones in the tree's own tests. So the default is **off for `Source == SourceConfig`**, with a `preamble` key added to the config spec to opt in — yolo declines to prepend bytes to a program whose protocol it has never seen and whose author never declared anything.
- **`publishes: "socket"` for every converted loophole**, because the pack-shipped subset requires it (`packshipped.go:371-405`) — the three bundled loopholes predate that rule rather than disproving it (§11).
- **A baked client binary is fine for an official pack** — `yolo-ps` does not become a shipped artifact, so §3.1's binary work stays off this critical path.
- ~~**`{endpoint}` survives** for yolo's own non-loophole services (the journal bridge still publishes its own, `journaldcmd.go:75`).~~ **FALSE as of 2026-08-18**: the journal bridge became a pack-shipped loophole, so it had to take `publishes: "socket"` like everything else — `journald.ServeEndpoint` and the `--endpoint` flag are DELETED (the flag refuses, naming `--socket`). `publishes: "endpoint"` now has exactly ONE user left, this document's own subject, which makes the follow-on below smaller than it was rather than moot. Whether the *manifest key* `publishes: "endpoint"` should be retired once its last loophole user is gone is a genuine follow-on — it is not needed to finish the sprint, and OQ-BP4's end state makes it a two-line deletion.

~~**The order to build it in:** change 2 first, while the relay still exists and still stamps. The two coexist without a flag-day because the connection preamble is **additive** — a daemon that has been taught to read it sees `[connection preamble][request]`, and the relay's redundant in-payload `jail_id` is simply ignored rather than conflicting.~~

> **⚠ Retracted, 2026-08-15 — "additive" is FALSE, and this was the most dangerous sentence in the document.** Found by implementation scouting, not by review. The relay does not merely carry a redundant field: `brokerrelay.go:172-185` reads the **first length-prefixed frame** off its fronted connection and `stampJailID` (:136-155) rewrites it. Put a preamble in front of that and the relay stamps **the preamble**, writes it to the broker, and the terminator's real request sits unread behind it — **every Claude OAuth refresh in every jail fails.**
>
> The correction is one line of code and it must land in the *same* commit as the preamble: the relay's front sets `NoPreamble: true` (`brokerrelay.go:272`). The relay is the one deliberate opt-out in the tree, and it stays one until the broker conversion deletes it.
>
> It is at least a loud failure rather than a silent one: `TestRelayFrontPreservesJailIDStamp` (`front_test.go:113-138`) fails under `just test-fast` if the opt-out is forgotten. That test was written for another reason and is now a tripwire on the credential path.

**The order to build it in:** change 2 first, with the relay opt-out in the same commit. Then 1 and 3 together, since 3 is only correct once 2 is in. Then 4, which is the part that either works immediately or teaches us something. Change 5 is a verification, not an edit.

**What "it worked" looks like:** `yolo-ps` returns the same output from inside a jail; the tier-1 connection record and the tier-2 request line agree on `jail=`; a client that sends a *spoofed* `jail_id` sees it overridden in both; `yolo check` still reports the loophole's doctor result; and `bundled_loopholes/` has one fewer directory with nothing in core mentioning `host-processes` by name.

---

## Open Questions

1. **OQ-BP1 — does the broker wait for the pack-shipped binary capability, or move on a baked daemon and adopt it later?**

   *Restated after review.* The original question was whether the capability is needed at all; that is settled — **it is wanted as a general capability** (§3.1). What is left is sequencing, and it decides the size of the broker move: on a baked daemon it is a manifest relocation plus §5's stamp; behind the binary work it is that plus selection, delivery and two gates.

   _Leaning:_ **Move on the baked daemon; adopt binaries after.** [`loophole-packaging-overview.md`](loophole-packaging-overview.md) §1.1 already rules a baked client fine for an official pack, and the same argument covers a daemon: what matters is who is accountable for the code. Coupling them would put a build-and-download matrix in front of a change that is otherwise a manifest move — and the binary capability then lands with a *real* consumer available to test it rather than only a synthetic one.

   **Answer:**
   > **Both at once — ship them together, not one before the other.** *(My leaning was the opposite and is recorded above because the decision has consequences to plan for: the release matrix that produces per-platform artifacts is now on the critical path, and `platforms` must not declare more than the release actually builds — see §9.)* The practical shape: the broker's manifest is correct on a baked daemon either way, so the two can still land as **separate commits inside one sprint**, which keeps a stalled binary capability from holding a finished broker move hostage.

2. **OQ-BP2 — does the front gain a declared protocol-aware stamp (§5.4-D), or does `jail_id` attribution change shape?**

   This is the one genuinely new mechanism in the design. Option D keeps invariant I1 (host-asserted identity) at the cost of making the front's "never parses the stream" property conditional. The alternative worth weighing is not any of A/C/E/F but a **narrower I1**: accept that a fronted daemon's audit record carries the *connection's* jail identity — which the front already knows from the token — recorded by the framework in its own audit line, and stop stamping the payload at all. That preserves trustworthy attribution in yolo's logs while letting the daemon's own view of `jail_id` become untrusted.

   _Leaning:_ **D, but ask this question first**, because the narrower-I1 variant is strictly simpler and may be sufficient. What decides it: does anything in the broker *behave* differently per jail, or is `jail_id` purely diagnostic? Today's reading of `internal/oauthbroker` is that it is purely diagnostic — no `jail_id` reference exists in the package — in which case the narrow variant wins and the front never parses anything.

   _Strengthened by §11:_ `yolo-ps` already self-reports its `jail_id` from inside the jail and `hostservice` records it verbatim as untrusted, so **yolo already ships a loophole with a client-asserted `jail_id`**. Option **G** — yolo records the identity in its own audit line, the daemon never sees the field — makes the broker consistent with the loophole beside it rather than uniquely strict, and needs no new mechanism at all. That is now my recommendation over D.

   _Resolved by:_ grepping the broker's decision paths for any per-jail behaviour, then a maintainer ruling on whether daemon-visible `jail_id` must remain trustworthy.

   **Answer:**
   > **Yes — and for every loophole daemon, on by default, with a declared opt-out.** Full design in §5.5. The short form: a loophole declares `payload: "framed"` (default) and yolo inserts a host-asserted `jail_id` into the opening request; a pure byte pipe declares `payload: "raw"` and is spliced untouched. Either way yolo's own connection record carries a host-derived `jail=`, so the declaration decides what the *daemon* sees, never what the audit log says.
   >
   > *Two of my recommendations were overruled and both were wrong in the same direction — I kept proposing opt-in mechanisms for something that should be the default.* The reviewer's objection to the first "mandatory, no exceptions" draft is what produced the `framed`/`raw` declaration, which is better than either: it stops the framework guessing a connection's shape, which is the actual root of today's two-tier audit split.

3. **OQ-BP3 — does this wait for OQ-LP14, or proceed beside it?**

   [`pack-code-separation.md`](pack-code-separation.md)'s roadmap entry says these two should be designed together because both concern what a pack-shipped loophole may declare. Having written this doc, I now think that is **wrong**: OQ-LP14 is about a new *host-crossing claim class* (a socket in the session runtime dir), and nothing here needs one. The two are adjacent in subject and independent in mechanism.

   _Leaning:_ **Proceed beside it.** Coupling them delays a change that needs no new claim vocabulary behind one that does. I am flagging it because it contradicts what I wrote in the roadmap yesterday.

   > **Withdrawn for the sprint by OQ-BP4's answer, and the distinction is worth keeping.** The *broker* still needs nothing from OQ-LP14 — that part of the leaning stands. But **"no bundled loopholes" does**, through `audio` (§11), so LP14 moves from adjacent question to sprint blocker. The question is left open rather than deleted because it becomes live again the moment the sprint goal is anything smaller than emptying the channel.

   **Answer:**
   > _(superseded by OQ-BP4 — LP14 is a dependency of the sprint, not of this loophole)_

4. **OQ-BP4 — after the move, does `bundled_loopholes/` have any inhabitants left worth keeping as a channel?**

   The broker was the strongest argument for the bundled channel — the one loophole whose "real spawn is reconstructed in Go". With it gone, `host-processes` and `audio` are what remain, and `audio` already exists as an official pack sitting *beside* its bundled copy (§5.1 of the overview). This asks whether the fifth sequencing step should end with the channel's retirement rather than one fewer inhabitant.

   _Leaning:_ **Do not bundle it into this project.** It is a genuine follow-on, it is OQ-LP11's actual finish line, and attaching it here would make a bounded change unbounded. Worth filing the moment step 5 lands.

   **Answer:**
   > **No inhabitants at the end of this work sprint — retire the channel.** *(Again overruling my leaning, and this one changes more than the broker: it is OQ-LP11's finish line, so it is worth having wanted.)* Consequences, in §11: three conversions rather than one, `publishes: "socket"` as the common blocker all three predate, **`host-processes` first** as the cheap end-to-end proof, and — the one that needs your attention — **OQ-LP14 becomes a hard dependency**, because `audio` cannot be expressed as a pack until the runtime-socket vocabulary is ruled. That contradicts OQ-BP3's leaning below, which is withdrawn for the sprint.

5. **OQ-BP5 — download-with-digest only, or also a declared build step?**

   The review asks for both as candidates (§3.1). They are not symmetric: a download can be pinned by `sha256` and therefore satisfies **P4** (a pinned pack pins everything that runs); a build step generally cannot, because builds are not bit-reproducible, so what runs is decided at install time by whatever toolchain the machine happens to have. A build step is also the same risk class as `packdecl.Install.InstallerURL`, which the schema already calls *"the sharpest thing a manifest can name"* and refuses to fetched packs outright.

   _Leaning:_ **Download-with-digest now; no build step until something needs one B cannot serve.** If a build step is added later, it should be jail-side only and origin-gated exactly as `InstallerURL` is — and it should be honest that a built artifact is unpinned, rather than inheriting the word "pinned" from the commit that produced its recipe.

   **Answer:**
   > _(empty — fill in when decided)_

6. **OQ-BP6 — may a *fetched* pack ship a host-side daemon binary?**

   §3.1's two-gate split says a jail-side binary is roughly as sharp as what a pack can already do, while a host-side one is a host-execution grant. This asks whether the second is available to a fetched pack at all, or whether — like `InstallerURL` and `host_files` — it is refused by origin regardless of what the user would approve. Not needed for the broker, which is official; needed before anyone else ships one.

   _Leaning:_ **Allow it, gated by the existing host-execution approval rather than refused by origin.** A fetched pack can already declare a `host_daemon.cmd` naming an arbitrary host argv, so refusing a *binary* while permitting an arbitrary *command* would repeat the halfway-measure shape OQ-LP14 already suffers from — blocking the declarative form of a capability while permitting the imperative one. But this genuinely is a widening and should be answered deliberately.

   **Answer:**
   > _(empty — fill in when decided)_
