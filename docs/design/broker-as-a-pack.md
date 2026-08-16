# Shipping the OAuth broker as a pack — smaller than it looks, and the last question is whether one audit field is worth keeping

**Status:** DESIGN SKETCH, 2026-08-15. Nothing built. All code claims verified against the tree on that date.

**Two review rounds, same day.** Round 0: §3.1 grew from a deferral into a design — pack-shipped binaries are wanted as a general capability, with per-arch selection and dynamic download — and §5.2 was added, a from-scratch explanation of what the front is and what "the stamp" means on the wire. Round 1 **retracted this doc's central claim** (§5.2a): the front is *not* a component that never parses, and the stamp is not the hard thing the first two versions said it was. The title changed with it.

**Two rulings from round 1 that set the scope, both of which overrule a leaning of mine:**

> **The broker move and the pack-shipped binary capability ship TOGETHER**, not one then the other (OQ-BP1). And **`bundled_loopholes/` has no inhabitants at the end of this work sprint** (OQ-BP4) — the goal is the channel's retirement, not one fewer entry in it.

**Scope note.** That second ruling makes this doc one of three conversions rather than a self-contained change, and the other two are not designed here: `host-processes` needs the same `publishes` change with none of the relay complexity, and `audio` cannot become a pack at all until **OQ-LP14** is answered. §11 states what each needs and what is genuinely blocking; the work belongs to the sprint, not to this document.

**The short version.** [`pack-code-separation.md`](pack-code-separation.md) §4 named two things that must be true before the `claude-oauth-broker` can ship as a pack: a **jail-side daemon shippable as a binary**, and the **per-jail relay** becoming expressible. The first is essentially already built — the container mount, the manifest token, the loader and the pack-shipped subset all already permit it, and nobody noticed because nothing has tried. The second reduces to three of four jobs the framework-owned front *already does*, plus one it does not: the relay stamps a **host-asserted `jail_id`** into the request. Round 1 established that adding that to the front is a modest, mechanical change rather than a violation of anything. **So the real question is no longer "how do we keep the stamp" but "is a daemon-visible `jail_id` worth any mechanism at all"** — it appears nowhere in the broker, and yolo could record the same identity in its own audit line without touching a payload byte. That is OQ-BP2, and it is the only thing standing between here and deleting `internal/brokerrelay`.

**The most important section is §5** — everything else is inventory and consequence.

**Reads with:** [`pack-code-separation.md`](pack-code-separation.md) (why this is being done at all; its §4 is what this doc corrects), [`loophole-packaging-overview.md`](loophole-packaging-overview.md) (the `loophole` kind, the front, and the pack-shipped subset — §3 is the contract this leans on), [`loophole-packaging.md`](loophole-packaging.md) (the implementation authority, and OQ-LP11/OQ-LP14), [`agent-credentials.md`](agent-credentials.md) (§2.5, why a broker exists at all), [`loophole-transport.md`](loophole-transport.md) (why loopback-TLS is the only hop).

---

## 1. Verdict up front

**Ship it as a pack, and expect the work to be smaller than §4 estimated — but concentrated somewhere §4 did not look.**

Three claims, each argued below:

- **P1. Jail-daemon-as-binary is not a missing mechanism.** `{jail_loophole_dir}` already resolves to a container path, the module dir is already bind-mounted there `:ro` **without `noexec`**, `nix-ld` already runs non-nix dynamically-linked binaries, and the pack-shipped subset does not restrict `jail_daemon` at all. What is missing is *selection, delivery and trust* for the binary — designed in §3.1 on review request, and **not on the broker's critical path**, because an official pack may keep a baked daemon. (§3)
- **P2. The relay is 3/4 redundant with the front.** Per-connection upstream dial, TLS termination, endpoint publication, and layer-attributable failure are all in `svcendpoint` already. (§4)
- **P3. The whole design reduces to one question about one audit field: is a daemon-visible, host-asserted `jail_id` worth keeping?** If yes, the front gains a small declared parse (cheap — see §5.2a). If no, the relay's last job disappears and nothing replaces it. Either way the relay goes; the question decides whether anything takes its place. (§5)

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

## 5. The stamp problem — the actual design decision

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
| B | **Framework preamble frame** | the front writes a small, framework-owned metadata frame on the upstream Unix connection before splicing | ⚠️ **Viable, but it changes the daemon-side contract for *every* fronted daemon** — `audio`, `host-processes` and any third party would have to skip a frame they never asked for. Only acceptable if gated by an opt-in manifest key |
| C | **`SO_PEERCRED`-style side channel** | identity carried out-of-band on the socket itself | ❌ **Rejected.** Peer credentials identify the *front* process, which is the same process for every jail |
| D | **Declared protocol-aware stamp** | manifest opts in: `stamps: "jail_id"`; the front parses frame #1 for daemons that ask, and only for them | ✅ **Viable and cheap** — cheaper than the first two versions of this doc claimed, since the front already frames-and-reads for auth and yolo owns `frameproto` too (§5.2a). Declared rather than unconditional, because not every fronted daemon speaks the framed protocol |
| E | **Drop the stamp; let the daemon self-report** | broker logs whatever the client says | ❌ **Rejected.** Violates I1 for a field whose entire value is that it is trustworthy |
| F | **Keep a relay-shaped shim in the pack** | the pack ships its own per-jail relay binary | ❌ **Rejected.** `host_daemon` is host-wide and keyed by loophole name; there is no per-jail daemon vocabulary, so this needs a *new* mechanism to avoid a smaller one |
| **G** | **Drop the daemon-visible field; yolo records the identity itself** | no stamp, no parse; the front writes `jail=<id>` into its own audit record from the token it already validated, and the daemon simply never sees a `jail_id` | ✅ **Now my recommendation.** It preserves the property that actually matters — a jail cannot forge another jail's identity into *your* logs — with **zero** new mechanism, and it is available precisely because nothing in `internal/oauthbroker` reads the field. Its cost is honest and small: a daemon's own logs lose the field, so a third-party daemon wanting per-jail behaviour would need D after all |

**Recommendation: G, with D as the fallback.** *(This was "D" in the first two versions; §5.2a and §11 both moved it.)* G preserves the property I1 exists for — a jail cannot forge another jail's identity into **your** audit log — while building nothing at all, and `yolo-ps` is precedent that a client-asserted `jail_id` inside a daemon is already acceptable here (§11). D remains the right answer the moment a fronted daemon needs the field to be *trustworthy inside the daemon*, and §5.2a shows that is a modest change rather than a violation — so this is a "not yet", not a "no".

**The cost of D, restated after §5.2a:** smaller than first claimed. The front already frames-and-reads for auth, and yolo owns `frameproto`, so the honest cost is the verbatim-fallback paths and the fact that the parse must be *declared* rather than universal. One property does still need protecting: **I2 — a stamping front still emits exactly one connection-level audit record.** The opt-in buys a payload edit, not a per-request audit tier; those are separate things and the manifest key must not quietly enable the second.

---

## 6. What the pack looks like

Under the recommended path (D + the OQ-BP1 "keep the daemons baked" reading):

```
packs/claude-oauth-broker/           # an OFFICIAL pack, embedded in the binary
  pack.json                          # { "kind": "loophole", "from": "loophole/broker" }
  loophole/broker/
    manifest.jsonc                   # unchanged from bundled_loopholes/, except:
                                     #   host_daemon.publishes: "socket"      (new)
                                     #   host_daemon.stamps: "jail_id"        (new, §5.4-D)
                                     #   platforms: [...]                     (if a binary ships)
```

Everything else in the manifest — `serves`, `intercepts`, `broker_ip`, `ca_cert`, `state_files`, `requires`, `doctor_cmd` — is already correct and moves unchanged. The `{state}` token is explicitly designed to survive a restage (`loopholedecl/tokens.go:20-28`), which is what makes a pack-shipped CA possible at all.

---

## 7. What this deletes, what it costs, what it forecloses

**Deletes:** `internal/brokerrelay` (~4 files plus its lifecycle in `loopholesruntime.go` — pid file, lock, socket path, reaping, the `relayKill` ordering comments) · one of the four bundled loopholes · the last consumer of the `relayEnsure` special case in the run pipeline.

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
| The opt-in parse in the front becomes the place every future protocol feature gets bolted on | Keep it a **closed set of stamp names** in the schema, the same discipline `packdecl.KnownHooks` uses for the imperative hooks; one name today |
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

**Fourth, flip the broker to `publishes: "socket"`** and delete `internal/brokerrelay` plus its lifecycle in `loopholesruntime.go`. This is the step that must not be split — a half-flipped broker is a jail with no credential path.

**Fifth, move the manifest into an official pack** and retire the bundled copy. This is also the step where OQ-LP11's consolidation finally gets one channel emptier, which it has been owed since 2026-08-14.

**And the binary capability (§3.1) runs alongside, not after** — OQ-BP1 was ruled *ship both at once*, overruling my "adopt it later". Its own order is unchanged: selection convention, then download-with-digest, then the two gates. What the ruling changes is that it must be *finished* in this sprint rather than queued behind a working broker, so its slowest piece — the release matrix producing per-platform artifacts — should start early rather than last. The one thing I would preserve from the rejected sequencing is **separability in the tree**: the broker's manifest is correct on a baked daemon, so the two can land as independent commits inside one sprint without either blocking the other's review.

---

## 11. What "no bundled loopholes" additionally requires

The second ruling is the larger one: `bundled_loopholes/` should be **empty** at the end of the sprint, not shorter. That is three conversions, and only one of them is this document.

| Loophole | What blocks it becoming a pack | Size |
|---|---|---|
| **claude-oauth-broker** | `publishes` defaults to `endpoint`; the pack-shipped subset accepts **only `socket`** (`packshipped.go:371-405`). Plus the relay and the stamp question | this doc |
| **host-processes** | The same `publishes` problem and nothing else: its `host_daemon.cmd` passes `{endpoint}` and publishes for itself, so it converts to `{socket}` + the framework front with no relay, no stamp, no per-jail anything | small |
| **audio** | **OQ-LP14.** Its `host_bind_mounts` and `requires.file_exists` both name `${XDG_RUNTIME_DIR}/pulse/native` and `pipewire-0`, which the pack-shipped path rule refuses in every spelling. The official `audio` pack today therefore sits *beside* the bundled copy rather than replacing it | blocked on a ruling |

Three things follow, and the first is the one to notice:

- **OQ-LP14 stops being adjacent and becomes a hard dependency of the sprint goal.** I recommended the opposite one message ago (OQ-BP3: "proceed beside it"), and that recommendation is **withdrawn for the sprint** while remaining correct for the broker in isolation. The roadmap already carries a leaning for LP14 — a closed, yolo-resolved list of runtime sockets — so this is a ruling to make, not a design to invent.
- **The `publishes` subset rule is the common blocker, and it is load-bearing rather than accidental.** It exists so a pack-shipped daemon cannot get TLS, token handling or endpoint permissions wrong; converting all three onto the framework front is the same work as honoring it. Worth stating because "all three bundled loopholes violate the pack-shipped subset" sounds like a rule that is too strict, and it is not — it is a rule they predate.
- **`host-processes` is the cheap one and should go first.** It exercises the whole conversion path — subset validation, official-pack staging, the front, `doctor_cmd` — with none of the broker's complexity. If something structural is wrong with converting a bundled loophole, it will show up there for a fraction of the cost.

**And one finding that lands in this doc's favour:** `yolo-ps` already sends its own `jail_id` from inside the jail (`cmd/yolo-ps/main.go:121-122`), which `hostservice` records verbatim as untrusted. So yolo *already ships* a loophole whose `jail_id` is client-asserted, and has done without complaint. That is the strongest evidence for option **G** in §5.4 — dropping the daemon-visible field rather than building a mechanism to keep it trustworthy — because it makes the broker consistent with the loophole beside it instead of uniquely strict.

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
   > _(empty — fill in when decided)_

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
