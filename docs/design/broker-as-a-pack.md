# Shipping the OAuth broker as a pack — the blocker is one protocol-aware trick, not two missing mechanisms

**Status:** DESIGN SKETCH, 2026-08-15. Nothing built. All code claims verified against the tree on that date.

**The short version.** [`pack-code-separation.md`](pack-code-separation.md) §4 named two things that must be true before the `claude-oauth-broker` can ship as a pack: a **jail-side daemon shippable as a binary**, and the **per-jail relay** becoming expressible. The first is essentially already built — the container mount, the manifest token, the loader and the pack-shipped subset all already permit it, and nobody noticed because nothing has tried. The second is real, but three of the relay's four jobs are *already done by the framework-owned front*. What is left is exactly one thing: the relay stamps a **trustworthy `jail_id`** into the request, and the front is designed never to parse the stream. **That single conflict is this document.**

**The most important section is §5** — everything else is inventory and consequence.

**Reads with:** [`pack-code-separation.md`](pack-code-separation.md) (why this is being done at all; its §4 is what this doc corrects), [`loophole-packaging-overview.md`](loophole-packaging-overview.md) (the `loophole` kind, the front, and the pack-shipped subset — §3 is the contract this leans on), [`loophole-packaging.md`](loophole-packaging.md) (the implementation authority, and OQ-LP11/OQ-LP14), [`agent-credentials.md`](agent-credentials.md) (§2.5, why a broker exists at all), [`loophole-transport.md`](loophole-transport.md) (why loopback-TLS is the only hop).

---

## 1. Verdict up front

**Ship it as a pack, and expect the work to be smaller than §4 estimated — but concentrated somewhere §4 did not look.**

Three claims, each argued below:

- **P1. Jail-daemon-as-binary is not a missing mechanism.** `{jail_loophole_dir}` already resolves to a container path, the module dir is already bind-mounted there `:ro` **without `noexec`**, `nix-ld` already runs non-nix dynamically-linked binaries, and the pack-shipped subset does not restrict `jail_daemon` at all. What is missing is a *build-and-distribution* answer for the binary, not a yolo mechanism. (§3)
- **P2. The relay is 3/4 redundant with the front.** Per-connection upstream dial, TLS termination, endpoint publication, and layer-attributable failure are all in `svcendpoint` already. (§4)
- **P3. The whole design reduces to: who stamps `jail_id`, and how does the daemon come to trust it.** The relay does it by parsing the first frame; the front refuses to parse anything, deliberately and in writing. Resolving that is the design decision. (§5)

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

Not mechanism — **distribution**. A pack shipping `bin/terminator` must answer: which `<goos>/<goarch>` builds does it carry, who builds them, and does a git-fetched pack carry checked-in binaries? That is a question for pack authors and for the fetched-pack origin gate, not for the loophole runtime. For *this* broker the answer is easy and worth stating: it is **yolo's own code**, so it can be built by yolo's own release process and carried in an official pack — the "official pack carries yolo's authority" position that survives [`pack-code-separation.md`](pack-code-separation.md) OQ-4.

**And there is a cheaper option worth naming before any of that.** The terminator does not have to become a separate binary at all: `jail_daemon.cmd` may keep naming a baked subcommand for an *official* pack, because [`loophole-packaging-overview.md`](loophole-packaging-overview.md) §1.1 already rules that a baked client is fine for one. Under that reading, moving the broker into an official pack requires **no new binary and no build story** — it moves the manifest and leaves both daemons baked. That is OQ-BP1.

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
| **stamp `jail_id`** | **none, deliberately** | `front.go:44-46`: *"splice does not parse the stream"*; `crossing.go:194`: parsing the request "is both unavailable at the front" |

So the folding is not "reimplement the relay in the front". It is: **switch the broker's `host_daemon` to `publishes: "socket"`, let the existing front serve it, and delete `internal/brokerrelay` — except for job 3.**

---

## 5. The stamp problem — the actual design decision

### 5.1 Why `jail_id` exists

It is an **audit attribution field**, not a routing or authorization field. `internal/hostservice` reads `Request["jail_id"]` (defaulting to `"unknown"`) and emits it as `jail=<id>` in the per-request record (`hostservice.go:38,73`). The security property is stated at `svcendpoint/crossing.go:105`: the per-jail token is what *makes the relay's host-side stamp trustworthy*. Compare `hostservice`'s tier-2 behaviour, where a client-supplied `jail_id` is recorded verbatim and is explicitly untrusted (`tiers_test.go:88-90`).

So the invariant to preserve is narrow and worth naming:

> **I1.** A `jail_id` in an audit record was asserted by the **host**, never by the jail — a jail cannot forge another jail's identity into the log.

### 5.2 The pivot nobody has used yet

**The front already knows which jail it is talking to.** It validated a bearer token that was minted per jail and written 0600 into that jail's own directory. The identity is therefore available at the front *before any payload byte is read* — it simply has nowhere to go, because the front's contract is to splice opaque bytes.

That reframes the question from *"how does the front learn the jail?"* (it already has) to *"how does it tell the daemon, without parsing?"*

### 5.3 Options

| # | Option | What it means | Verdict |
|---|---|---|---|
| A | **Per-jail upstream socket** | the front dials a different Unix path per jail; the daemon infers identity from which socket the connection arrived on | ❌ **Rejected.** The broker is a host singleton by design (one flock, one creds file); giving it N listeners re-creates per-jail state in the one component that must not have it |
| B | **Framework preamble frame** | the front writes a small, framework-owned metadata frame on the upstream Unix connection before splicing | ⚠️ **Viable, but it changes the daemon-side contract for *every* fronted daemon** — `audio`, `host-processes` and any third party would have to skip a frame they never asked for. Only acceptable if gated by an opt-in manifest key |
| C | **`SO_PEERCRED`-style side channel** | identity carried out-of-band on the socket itself | ❌ **Rejected.** Peer credentials identify the *front* process, which is the same process for every jail |
| D | **Declared protocol-aware stamp** | manifest opts in: `stamps: "jail_id"`; the front parses frame #1 for daemons that ask, and only for them | ✅ **Recommended.** It keeps "the front does not parse" true by default, makes the exception *declared and readable in the manifest*, and confines the loophole-protocol knowledge to the framework that already owns the wire format |
| E | **Drop the stamp; let the daemon self-report** | broker logs whatever the client says | ❌ **Rejected.** Violates I1 for a field whose entire value is that it is trustworthy |
| F | **Keep a relay-shaped shim in the pack** | the pack ships its own per-jail relay binary | ❌ **Rejected.** `host_daemon` is host-wide and keyed by loophole name; there is no per-jail daemon vocabulary, so this needs a *new* mechanism to avoid a smaller one |

**Recommendation: D.** It is the smallest change that preserves I1, and it is the shape this repo already prefers — an opt-in declaration in the manifest rather than a behaviour every daemon inherits. It also matches the existing precedent that the framework, not the loophole, owns the wire ([`loophole-packaging-overview.md`](loophole-packaging-overview.md) §3).

**The cost of D, stated plainly:** the front stops being *unconditionally* protocol-blind, and `crossing.go`'s "nothing parses the stream" becomes "nothing parses the stream unless the manifest asked". That sentence is load-bearing today — it is why fronted connections carry no per-request tier — so the opt-in must not silently enable per-request auditing along with the stamp. **I2: a stamping front still emits exactly one connection-level record; the stamp changes the payload, not the audit tier.**

---

## 6. What the pack looks like

Under the recommended path (D + the OQ-BP1 "keep the daemons baked" reading):

```
packs/claude-oauth-broker/           # an OFFICIAL pack, embedded in the binary
  pack.json                          # { "kind": "loophole", "from": "loophole/broker" }
  loophole/broker/
    manifest.jsonc                   # unchanged from bundled_loopholes/, except:
                                     #   host_daemon.publishes: "socket"      (new)
                                     #   host_daemon.stamps: "jail_id"        (new, §5.3-D)
                                     #   platforms: [...]                     (if a binary ships)
```

Everything else in the manifest — `serves`, `intercepts`, `broker_ip`, `ca_cert`, `state_files`, `requires`, `doctor_cmd` — is already correct and moves unchanged. The `{state}` token is explicitly designed to survive a restage (`loopholedecl/tokens.go:20-28`), which is what makes a pack-shipped CA possible at all.

---

## 7. What this deletes, what it costs, what it forecloses

**Deletes:** `internal/brokerrelay` (~4 files plus its lifecycle in `loopholesruntime.go` — pid file, lock, socket path, reaping, the `relayKill` ordering comments) · one of the four bundled loopholes · the last consumer of the `relayEnsure` special case in the run pipeline.

**Costs:** the front gains a declared, opt-in parse (§5.3) · the broker's `--socket` becomes a *fronted* socket rather than a host-to-host one, so its threat model changes from "nothing in a jail can reach this" to "the front is what stands in front of this" — the same position every other fronted daemon is already in · one more official pack in the set the "six official packs" tests count.

**Forecloses:** nothing structural. If D proves wrong, the relay can come back as a `host_daemon` of a *different* loophole without touching the front.

---

## 8. Non-goals — what this does not license

- **Not** a general per-jail daemon mechanism. Option F is rejected precisely to avoid inventing one for a single consumer.
- **Not** a change to the transport. Loopback-TLS stays the only hop; this is about what sits behind the front.
- **Not** a widening of the pack-shipped subset. This design needs no new host-crossing vocabulary, which is what distinguishes it from **OQ-LP14** — that one is blocked on a *new* claim class, this one is not.
- **Not** a fetched-pack story. Everything here assumes an **official** pack; a third-party broker would additionally have to answer §3.1's distribution question and pass the origin gate.
- **Not** a change to how credentials are merged, harvested or written. That is [`pack-code-separation.md`](pack-code-separation.md) §5, decided separately and landing first.

---

## 9. Risks

| Risk | Mitigation |
|---|---|
| The opt-in parse in the front becomes the place every future protocol feature gets bolted on | Keep it a **closed set of stamp names** in the schema, the same discipline `packdecl.KnownHooks` uses for the imperative hooks; one name today |
| Deleting the relay loses the bounded-drain behaviour that makes a dial failure a clean EOF | It is not lost — `front.go`'s splice already distinguishes the case and marks `CrossingUnreachable`; the drain semantics must be **pinned by a test** before the relay is deleted, not after |
| A fronted broker socket is reachable by something on the host that the host-only relay socket excluded | The socket path stays where it is (`/tmp`, host-only, 0600); the front is an additional listener, not a relocation |
| The unexercised jail-binary path (§3) turns out to have a real defect once something uses it | Prove it with a throwaway pack shipping a two-line binary **before** committing to the broker move — it is the cheapest possible test of P1 |
| `platforms` forces the pack to enumerate arches yolo's own release does not build | Only bites if a binary ships; OQ-BP1's "keep it baked" reading avoids the question entirely |

---

## 10. Sequencing

What I would build, in order.

**First, settle OQ-BP1**, because it decides whether §3's distribution question exists at all. If the answer is "an official pack may keep baked daemons", steps 2 and 3 shrink to a manifest move.

**Second, prove P1 cheaply** — a throwaway local pack whose `jail_daemon.cmd` is `["{jail_loophole_dir}/bin/hello"]`, carrying a statically linked two-line binary. This is an afternoon, and it converts "the mechanism appears to exist" into "the mechanism works". Do this even if OQ-BP1 says the broker keeps its baked daemon: the finding is worth having on its own.

**Third, the stamp.** Add the declared stamp to the front (§5.3-D) with I1 and I2 as its tests, while the relay is still in place and still the thing running. Two implementations of the stamp can coexist for exactly one commit.

**Fourth, flip the broker to `publishes: "socket"`** and delete `internal/brokerrelay` plus its lifecycle in `loopholesruntime.go`. This is the step that must not be split — a half-flipped broker is a jail with no credential path.

**Fifth, move the manifest into an official pack** and retire the bundled copy. This is also the step where OQ-LP11's consolidation finally gets one channel emptier, which it has been owed since 2026-08-14.

---

## Open Questions

1. **OQ-BP1 — may an official pack's loophole keep a baked daemon, or must a pack-shipped loophole carry its own binaries?**

   This is the closure question for §3 and it decides the size of the whole project. If baked daemons are acceptable for an official pack, the broker move is a manifest relocation plus §5's stamp, and no build-and-distribution story is needed. If not, yolo's release process must start producing per-platform binaries carried inside a pack. [`loophole-packaging-overview.md`](loophole-packaging-overview.md) §1.1 already ruled that *"a baked client is fine for an official pack"* — but it ruled that about a *client*, and this asks it about a *daemon*.

   _Leaning:_ **Yes, baked is fine for an official pack.** The property that matters is who is accountable for the code, and an official pack carries yolo's own authority. Requiring binaries would make the strictest packaging demand of the one author who least needs to be constrained, and would put a build matrix in front of a change that is otherwise a manifest move.

   **Answer:**
   > _(empty — fill in when decided)_

2. **OQ-BP2 — does the front gain a declared protocol-aware stamp (§5.3-D), or does `jail_id` attribution change shape?**

   This is the one genuinely new mechanism in the design. Option D keeps invariant I1 (host-asserted identity) at the cost of making the front's "never parses the stream" property conditional. The alternative worth weighing is not any of A/C/E/F but a **narrower I1**: accept that a fronted daemon's audit record carries the *connection's* jail identity — which the front already knows from the token — recorded by the framework in its own audit line, and stop stamping the payload at all. That preserves trustworthy attribution in yolo's logs while letting the daemon's own view of `jail_id` become untrusted.

   _Leaning:_ **D, but ask this question first**, because the narrower-I1 variant is strictly simpler and may be sufficient. What decides it: does anything in the broker *behave* differently per jail, or is `jail_id` purely diagnostic? Today's reading of `internal/oauthbroker` is that it is purely diagnostic — no `jail_id` reference exists in the package — in which case the narrow variant wins and the front never parses anything.

   _Resolved by:_ grepping the broker's decision paths for any per-jail behaviour, then a maintainer ruling on whether daemon-visible `jail_id` must remain trustworthy.

   **Answer:**
   > _(empty — fill in when decided)_

3. **OQ-BP3 — does this wait for OQ-LP14, or proceed beside it?**

   [`pack-code-separation.md`](pack-code-separation.md)'s roadmap entry says these two should be designed together because both concern what a pack-shipped loophole may declare. Having written this doc, I now think that is **wrong**: OQ-LP14 is about a new *host-crossing claim class* (a socket in the session runtime dir), and nothing here needs one. The two are adjacent in subject and independent in mechanism.

   _Leaning:_ **Proceed beside it.** Coupling them delays a change that needs no new claim vocabulary behind one that does. I am flagging it because it contradicts what I wrote in the roadmap yesterday.

   **Answer:**
   > _(empty — fill in when decided)_

4. **OQ-BP4 — after the move, does `bundled_loopholes/` have any inhabitants left worth keeping as a channel?**

   The broker was the strongest argument for the bundled channel — the one loophole whose "real spawn is reconstructed in Go". With it gone, `host-processes` and `audio` are what remain, and `audio` already exists as an official pack sitting *beside* its bundled copy (§5.1 of the overview). This asks whether the fifth sequencing step should end with the channel's retirement rather than one fewer inhabitant.

   _Leaning:_ **Do not bundle it into this project.** It is a genuine follow-on, it is OQ-LP11's actual finish line, and attaching it here would make a bounded change unbounded. Worth filing the moment step 5 lands.

   **Answer:**
   > _(empty — fill in when decided)_
