# Loophole packaging — how a pack ships a loophole

**Status:** DESIGN, 2026-08-13. Not built. Closes **OQ-CAP2** with option **(B)**.

> **Reading this to DECIDE rather than to build? Start with
> [`loophole-packaging-overview.md`](loophole-packaging-overview.md)** — the same design at
> system level, with the five rulings it needs, and no line numbers. This doc is the
> implementation authority and the two are kept in sync; the overview is derived from it.

**Why this doc exists.** [`pack-capabilities.md`](pack-capabilities.md) §10 asks whether packs should
be able to ship loopholes, concludes *"the review is right that (B) is the real fix"*, and
recommends deciding (B) **before** building (A). The maintainer ruled: write (B) first, link it from
`pack-capabilities.md` as a prerequisite, then let that doc assume it and simplify. This is (B).
§6 below is the specific list of what survives in `pack-capabilities.md` and what died — and that
doc has now been **rewritten** to match, not merely annotated.

**Reads with:** [`pack-system.md`](pack-system.md) (the 14 kinds and how a kind is defined),
[`loophole-protocol.md`](loophole-protocol.md) (the wire contract),
[`loophole-transport.md`](loophole-transport.md) (the transport unification, shipped 2026-08-13),
[`pack-capabilities.md`](pack-capabilities.md) (what this narrows),
[`extension-point-principle.md`](extension-point-principle.md) (why design it now).

**What is verified and what is not.** Every code claim was read on HEAD; the import-cycle claim
(§3.2), the `Discover` census (§5.1), the `Collisions` call sites (§3.1) and the `:ro` socket result
(§3.1, §7) were **measured**, not inferred. **No pack-shipped loophole has ever run** — this is
design over verified code paths, not over a working instance (**R2**).

> ### Revision 2 — what three adversarial passes changed
>
> The first draft was attacked from three lenses (trust/approval, defaults/failure modes, and the
> pack-system layer). **All three refuted a load-bearing claim**, and this revision folds in 30
> findings. The four that change the shape of the work:
>
> 1. **The claim enumeration was not total, and the gate short-circuits on an empty one.** A
>    pack-shipped loophole with `host_bind_mounts` + `host_devices` and no daemon produced ZERO
>    approval claims, and `packMayAccessHost` returns `true` on an empty claim set — so a FETCHED
>    pack could bind an arbitrary absolute host path into a jail with no prompt, ever. Strictly
>    wider than anything a fetched pack can do today. §3.3 and §4.3 G2 now enumerate every
>    declaration that crosses. **This was the single worst defect in draft 1.**
> 2. **A 15th kind is an A12-fatal boot break against a stale baked entrypoint** — the `tier`
>    incident, for the third time. `DecodeTolerant`'s own docstring says an unknown *kind* still
>    fails loudly. §3.3a, and it is now item **0** of the landing order.
> 3. **The "for free" collision claim was false**, twice over: `packload.Collisions` is never
>    consulted at launch, and it skips single-pack groups. The fatal-collision rule needs a fourth
>    bespoke pre-flight and cannot live inside `Discover` at all (§3.1, §5.1).
> 4. **`{loophole_dir}` is substituted in exactly one field** — `host_bind_mounts[].host`. The
>    doc's own headline example manifest would have exec'd a literal `{loophole_dir}/acme-daemon.py`
>    (§2.1a).
>
> **One finding is REJECTED on measurement** and one is scoped down; both are argued in place
> (§7 and §4.5). A rejected finding that leaves no trace gets rediscovered.

> ### Revision 3 — maintainer review of the overview
>
> Five comments on [`loophole-packaging-overview.md`](loophole-packaging-overview.md), and two of
> them challenge the design rather than the prose. Folded in here because this doc is the authority:
>
> 1. **"Why three channels?"** — a `file://` pack does what the user loophole directory does, with
>    the same justifying sentence; and a bundled loophole could be an *official pack*, the way
>    `AGENTS.md` already dissolved agents into packs. §5.4's three reasons are re-graded (one
>    survives, one is wrong) and the direction is **OQ-LP10** and **OQ-LP11**.
> 2. **The framework owns the wire, so a pack may not opt out of it** — *"loopholes MUST use TCP, so
>    that should be in the framework not the loophole."* `publishes: "socket"` becomes the only legal
>    value for a pack-shipped loophole (§2.1); self-publishing stays first-party.
> 3. **Platform awareness is missing** — `requires` says *"the thing I need is present"*, never *"I
>    only exist for this platform"*, and packs will ship native code (§3.1, new subsection). Also the
>    named extension point for a future platform-specific transport.
> 4. **The front cannot be the crossing audit log.** Withdrawn: a loophole's protocol can be anything,
>    so connection-level is the honest ceiling (§2.1b hazard 3).
>
> A second round added two more:
>
> 5. **EOF non-propagation is not implemented, not impossible** — it is the relay's frozen teardown
>    imposing a default, so it becomes a per-loophole `request_end` declaration (§2.1b hazard 2).
> 6. **G1 removes per-workspace loopholes with no replacement** — a capability removal the doc was
>    calling a migration. **OQ-LP12** (§4.3).
>
> A third round found the worst one:
>
> 7. **Every gate governs a DECLARATION; none governs the FILE it names.** An agent rewrites a
>    workspace-resident daemon between launches and nothing notices — `file://` is a bare prefix
>    check with no path constraint, so "local" includes a directory the agent writes. **OQ-LP13**,
>    which subsumes G2b and OQ-LP3 (§4.3a).
>
> **And later rounds RULED almost all of it.** §4.3b is the organizing decision: **install is
> user-scope, enable is either**. On top of it: **OQ-LP10** (retire the user loopholes dir),
> **OQ-LP2** (install-shaped keys user-scope-only, with a FATAL error plus an install offer rather
> than warn-then-error), **OQ-LP6** (build the capability system), **OQ-LP11** (bundled become
> official packs, `audio` shipping IN this batch), and **OQ-LP13 — ruled AGAINST hashing**: *"if you
> can edit user-level files, you have all the perms already"*, so the user-scope edit IS the
> confirmation and all that survives is a PLACEMENT rule (§4.3a). OQ-LP12 dissolved, OQ-LP3 folded,
> OQ-LP8 is all but closed. **Only OQ-LP9 is properly open**, and it grew: nested jails need the
> scope model to RECURSE, and measurement says it cannot today. The development escape hatch
> §4.3a wanted is DELETED — *"you can develop a loophole in a jail with jail in jail if you need"*,
> and measured, the loophole runtime has exactly ONE jail-aware branch (`runtime.go:143`, device
> passthrough), so a nested jail is a real development environment and the friction belongs on the
> real machine.

---

## 1. The gap, and why it got acute this week

Loopholes have three distribution channels and only one of them is third-party:

| Source | Constant / entry | Who it is for | State |
|---|---|---|---|
| `bundled_loopholes/`, embedded in the binary | `BundledLoopholesDir` (`internal/loopholes/loopholes.go:89`) | yolo's own three | fine — but **why not an official pack?** OQ-LP11 |
| a user loophole dir | `UserLoopholesDir` → `~/.local/share/yolo-jail/loopholes` (`loopholes.go:110`) | one hand-placed local loophole | no fetch, no version, no approval, no manifest travelling with the code — and **a `file://` pack subsumes it**, OQ-LP10 |
| the `loopholes` block in `yolo-jail.jsonc` | `synthesizeConfigLoopholes` (`discover.go:29`) | **the only third-party path** | **degraded, and now dead** |

**The transport unification killed the last row.** `internal/loopholes/loopholes.go:63` is now
`validTransports = []string{TransportLoopbackTLS, TransportNone}`; both `unix-socket` and
`tls-intercept` were removed rather than deprecated, deliberately
([`loophole-transport.md`](loophole-transport.md) §7.4 item 2: *"a value that still validates is a
value someone will use"*). The retired value survives in two places —
`internal/loopholes/discover.go:60` hardwires `Transport: retiredTransportUnixSocket` into every
config-synthesized loophole, and `load.go:206-217` keeps a migration hint for authors — and the
comment above the first (`discover.go:12-28`) says why:

> *"a config entry's daemon is a THIRD-PARTY PROGRAM yolo did not write: it binds an AF_UNIX socket
> at the path substituted into its `command`, and nothing yolo ships lets it publish a loopback-TLS
> endpoint file instead (`internal/hostservice` is `internal/`, so it is not importable from outside
> this module). Flipping this line to loopback-tls would not migrate those daemons; it would make
> yolo wait five seconds for an endpoint file that never appears and then kill each one."*

That paragraph is accurate as written and §2 dissolves it.

**Two corrections to the premise, because they change where the fix goes.**

**(a) A third-party daemon already gets the full `loopback-tls` treatment — if it ships as a
`manifest.jsonc`, not as a config entry.** Nothing in the spawn path is yolo-specific:
`load.go:78` defaults an absent `transport` to `loopback-tls`; `parseHostDaemon` (`load.go:376-380`)
accepts any argv; `execx.SelfExecArgv` rewrites argv[0] **only** when it is literally `"yolo"`
(`internal/execx/execx.go:40`), so a third-party binary passes through untouched; and the
framework's own test suite spawns `sh -c <script>` as the daemon and asserts the full loopback-TLS
wiring against it (`internal/cli/run/hostservices_test.go:140`). So the framework is already
language-agnostic. What the hand-placed path lacks is **distribution**, and what the daemon lacks is
**an implementation artifact** for the server side of the transport.

**(b) The `loopholes` config block is not a degraded third-party path — it is an ungated one.**
See §4. It executes an arbitrary host command with no prompt, no lockfile, no origin check and no
launch-time notice, and a **workspace** config can write it.

**Why now.** Every host service the plans call for — the crossing audit log, the git credential
proxy, approval-gated credentials — is a loophole. `pack-capabilities.md` was being written to let a
pack *cancel* a loophole while there was still no way to *ship* one; that asymmetry is what
OQ-CAP2 noticed.

---

## 2. The transport contract — and no, it does not have to be Go

**Decision: yolo runs the TLS front. A pack-shipped daemon binds a plain AF_UNIX socket and yolo
publishes the endpoint file in front of it. `internal/svcendpoint` is NOT exported.**

The shim already exists. `internal/svcendpoint/front.go:25`:

```go
func ServeFront(publishPath, advertiseHost, upstreamUnixPath string, stop <-chan struct{}) error
```

`Listen`, then accept, then `go splice(conn, upstreamUnixPath)` (`front.go:26-41`) — an
authenticated loopback-TLS front spliced to a plain Unix socket it does not own. Its doc comment
(`front.go:15-24`) gives the rationale, and it transfers almost word for word:

> *"it exists for ONE reason: a daemon whose core is typed on `*net.UnixConn` and whose teardown
> semantics are frozen cannot simply swap its listener. … Every daemon that CAN take `Listen`
> directly should."*

A third-party daemon "cannot simply swap its listener" for a strictly stronger reason: it cannot
import the package at all. Same shape, arrived at from the other side. It is already in production
for the broker relay (`internal/brokerrelay/brokerrelay.go:270-277`) with a 400-line test file
covering the splice, multi-frame responses, publish failure, and stop-unlinks-endpoint.

### 2.1 The manifest vocabulary: `publishes`

The retired `unix-socket` value conflated two different facts — **what the jail dials** and **what
the daemon binds**. Splitting them retires it honestly:

```jsonc
"host_daemon": {
  "cmd": ["python3", "{loophole_dir}/acme-daemon.py", "--socket", "{socket}"],
  "publishes": "socket"          // "endpoint" (default) | "socket"
}
```

- **`publishes: "endpoint"`** (default, and what all three bundled loopholes do) — the daemon
  publishes the endpoint file itself at `{endpoint}`. Readiness is `svcendpoint.Probe`
  (`internal/cli/run/loopholesruntime.go:424`).
- **`publishes: "socket"`** — the daemon binds AF_UNIX at `{socket}`. yolo waits for that socket to
  appear, **then** starts a `ServeFront` and publishes the endpoint file itself.

`transport` stays `loopback-tls` in both cases, because the transport is what the jail dials and
that does not change. The jail sees one thing; the daemon author picks the easier half.

**REVIEW RULING: a PACK-shipped loophole may only say `publishes: "socket"`.** The maintainer's
framing is that the transport is a property of the framework, not of the loophole — *"loopholes MUST
use TCP, so that should be in the framework not the loophole"* — and the enforcement asymmetry in
§2.3 is exactly why. Self-publishing stays available to a **bundled** loophole (all three do it
today), because that is yolo's own code publishing yolo's own credential. For a distributed artifact
the front is mandatory, which converts the asymmetry from something this doc *documents* into
something a third party cannot express: they cannot get the endpoint mode, the compare or the cap
wrong because they never write them. Cost: one splice hop for a Go-authored third-party daemon that
could have done it correctly — cheap, and reversible if anyone complains. Enforced at load, in the
pack-shipped-subset validation beside the `jail_env` refusal (§3.1).

**`{socket}` and `{endpoint}` must diverge under `publishes: "socket"`.** Today both expand to the
same host path and `{socket}` is a back-compat alias (`loopholesruntime.go:367-372`). Under the new
value `{socket}` is the **upstream** path and `{endpoint}` is the **published** file. A manifest
declaring `publishes: "socket"` while naming `{endpoint}` in its argv is an author error and is
refused at load with the fix, rather than silently publishing nothing.

**The upstream socket lives outside the mounted dir.** `/tmp/yolo-front-<8hex>-<name>.sock`, beside
the relay's, for the reason `loopholesruntime.go:600-606` already records for the relay: leaving it
in the `:rw`-mounted `/run/yolo-services` would keep the retired transport reachable from inside the
jail — which is what retiring it forbids — and would let the jail unlink the daemon's own socket.

### 2.1a `{loophole_dir}` does not reach a `cmd` — the example above does not work today

**Measured:** `rg -n 'loophole_dir' internal/ --glob '!*_test.go'` returns **one** line —
`load.go:302`, inside `parseHostBindMounts`. No bundled manifest ever needed it, because all three
name binaries on `PATH` (`yolo …`, `yolo-jaild oauth-terminator`). So the substitution the headline
example depends on is not implemented anywhere, and the daemon spawn
(`loopholesruntime.go:367-372`, which substitutes only `{endpoint}`/`{socket}`), `RunDoctorChecks`
(`runtime.go:209-222`, verbatim) and the `YOLO_JAIL_DAEMONS` payload (`runtime.go:120-127`,
verbatim) would each exec a literal `{loophole_dir}/acme-daemon.py`.

**Requirement:** extend substitution to `host_daemon.cmd`, `doctor_cmd`, and `jail_daemon.cmd`.

**And the token resolves to two different paths, which the manifest must make visible.** On the
host side it is the pack's **staged module dir**; on the jail side the module dir is bind-mounted at
`/etc/yolo-jail/loopholes/<name>` (`runtime.go:60`, `:72`), so a `jail_daemon.cmd` naming
`{loophole_dir}` needs the container path. One token with two resolutions is the kind of asymmetry
an author discovers by debugging. **Decision: two tokens** — `{loophole_dir}` (host) and
`{jail_loophole_dir}` (container), refused in the wrong half at load. A single token would be
cheaper to implement and is exactly the "works until it doesn't" shape this doc is trying to avoid.

### 2.1b The three hazards — and hazard 1's fix does not fix hazard 1

1. **`ServeFront` publishes before the upstream exists, on purpose** (`front.go:23-24`: *"a
   connection that cannot reach `upstreamUnixPath` is logged and dropped"*). Correct for the relay;
   wrong here, because yolo's 5-second `Probe` wait (`loopholesruntime.go:426-439`) would succeed
   while the child never came up, and the jail would then authenticate successfully and be dropped —
   reading as a daemon failure. **So the shim waits for the child's socket before calling
   `ServeFront`, not after.** That ordering needs no change to `ServeFront` itself.

   **But a bare-existence wait on a deterministic path reproduces the exact failure it prevents.**
   The socket is `/tmp/yolo-front-<8hex>-<name>.sock` — same jail, same name, same path every
   launch. A daemon SIGKILLed after the 5s deadline (`:433-437`), or one that crashed, leaves the
   socket file behind; the next launch's wait is satisfied **instantly**, `ServeFront` publishes,
   `Probe` succeeds, the jail authenticates, and every request is dropped at
   `net.Dial("unix", …)` (`front.go:58-61`). The tree already knows this hazard one level up:
   `retireStaleRelayFiles` (`loopholesruntime.go:585-597`) removes both artifacts before a relay
   spawn precisely because *"the endpoint file, or the publication wait is satisfied INSTANTLY by a
   file naming a port nobody is on"*, and `startExternalService` does the same at `:353`
   (`_ = os.Remove(hostPath)`, *"Remove a dead predecessor's artifact BEFORE the spawn"*).

   **Two requirements, both mirroring shipped code:** unlink the upstream socket **before** the
   spawn, and unlink it in `stopLoopholes` beside `relaySocketFile` (`:218-228`, which today
   covers only the relay's socket and the sockets dir — the new path is in neither). And the
   readiness predicate should be a **connect**, not an existence check, for the same reason `Probe`
   rather than existence is the health predicate everywhere else (§2.3).
2. **`splice()` never propagates the client's EOF upstream** (`front.go:46-66`), tuned to the
   relay's frozen pipe semantics. `frameproto` is length-prefixed so a conforming daemon does not
   need EOF — but a daemon that reads its request *to EOF* works on a bare socket and **hangs
   forever** behind the front. That is a behaviour change the author cannot see, so it is a named
   requirement in the guide, not a footnote.

   **REVIEW ASKED: "impossible or just not implemented yet?" — NOT IMPLEMENTED, and the distinction
   changes what to build.** The constraint comes from the one upstream that exists, not from
   splicing. `front.go:44-55` records it: the relay's core tears down BOTH its sockets on either EOF
   (frozen parity), so half-closing upstream when the request direction ends would cut short a
   response still in flight — which is why `splice` runs the request direction unwaited and returns
   only on the response. A third-party daemon has the opposite requirement, and serving it is
   `up.(*net.UnixConn).CloseWrite()` after the request-direction `io.Copy` returns — the dial is
   `net.Dial("unix", …)` at `front.go:59`, so the assertion holds. **Both behaviours cannot be the
   default, so it is a per-loophole declaration**: beside `publishes: "socket"`, how a request ends
   — `request_end: "framed"` (default, today's behaviour) or `"eof"` (half-close upstream).
   Defaulting to `framed` keeps the relay bit-identical. Documenting it as an inherent limit would
   have taught authors to work around something one field away.
3. **No per-request access log, and — REVIEW CORRECTION — the front can never be one.** Draft 2
   called the front *"the natural home for the crossing audit log later, since every third-party
   crossing would pass through one yolo-owned process."* The maintainer's objection is correct and
   the claim is **withdrawn**: *"seems impossible to generically log here, protocol could be
   anything including video/audio who knows."* The front splices a byte stream it does not parse, and
   nothing constrains a loophole's protocol to be request-shaped — `frameproto` is what yolo's own
   daemons speak, not a property of the transport. **What the front CAN record is connection-level**:
   which loophole, when, which jail, bytes each way, duration. That is a real and useful audit
   surface and it is the honest ceiling. Per-request logging stays a property of daemons using
   `hostservice`'s helper ([`loophole-protocol.md`](loophole-protocol.md) §Access logging); anything
   richer is per-loophole, not framework. Say so in the server-side spec so nobody designs against
   the withdrawn promise.

### 2.1c A daemon that starts and never becomes reachable is COMPLETELY silent

**Found by review, and it is the failure a third-party daemon will actually hit.** Draft 1 said
`startExternalService` *"prints only on failure (`:358`, `:417`)"*. Those two sites cover **a
missing `command` key** and **a `cmd.Start()` error** — i.e. a missing argv[0]. The case where
`python3` exists and the script is broken prints **nothing at all**:

- `:436-439` — on readiness timeout it does `cmd.Process.Kill()` and returns `false` with no print.
- `:161-165` — the caller's else branch is literally `_ = out`, and `out := o.pr(o.Stdout)` at `:98`
  exists for nothing else. The printer was plumbed in and the message was never written.
- `:431` — the early-exit check (`cmd.ProcessState != nil && …Exited()`) is **dead**: `cmd.Wait()`
  is never called (the only `Wait` is `cmd.Process.Wait()` at `:460`, `os.Process.Wait`, which does
  not populate `cmd.ProcessState`). So every dead daemon costs the full 5 s, serially, in the
  `for _, name := range order` loop.

The right shape is already in the same file: `relayEnsure` (`:556-564`) prints a yellow warning
naming the endpoint and the log file, reasoning that *"the failure is otherwise silent until the
agent hits it, so say so here."*

**Requirement, a named deliverable rather than a nicety:** on readiness timeout, print a yellow
warning naming the loophole, the endpoint path, and `logs/host-service-<name>.log` (already opened
at `:385`); and fix the dead `ProcessState` check so an instantly-exiting daemon is reported
immediately rather than after 5 s. The same applies to §2.1's new wait-for-upstream step, which
needs its own timeout and its own message.

### 2.2 What flipping `discover.go:60` costs: nothing, plus one message rewrite

A config-block daemon binding a socket at `{socket}` becomes
`Transport: TransportLoopbackTLS` + `HostDaemon.Publishes = "socket"` — which is **true of it** and
needs no retired vocabulary. Its argv does not change, its behaviour does not change, and the jail
gains a real endpoint. The comment's objection (*"flipping this line would kill each one"*)
dissolves because the daemon is now **wrapped** rather than expected to publish. That closes the last
clause of queue row **T2**.

**Correction: `retiredTransportUnixSocket` has TWO readers, not one.** Beside `discover.go:60`
there is `load.go:211`, the `retiredTransportHint` switch — the one message a migrating third-party
author actually reads. It currently says the daemon *"must then publish an endpoint file at the path
yolo substitutes into `{endpoint}` instead of binding a socket there"*, which under this design
sends them down the harder of two supported paths. `loopholes_test.go:526` pins `{endpoint}` as a
required substring, so the stale text is **test-enforced**. The hint rewrite and its test land with
`publishes`, not after it.

### 2.3 Why not export `internal/svcendpoint`, and why not "just publish the spec"

**Exporting the Go package narrows the author's language to exactly one.** That is the
counter-intuitive part: the option that looks like opening it up is the most restrictive one. The
front opens it widest — anything that can bind AF_UNIX and read a length prefix: Python, Node, Rust,
Go, and a shell script with `socat`. The `nc`-era simplicity that
[`loophole-protocol.md`](loophole-protocol.md) §"Writing a client from scratch" mourns is restored on
the *server* side, behind the front. This is the repo's already-settled position on the adjacent
question ([`third-party-pack-logic.md`](third-party-pack-logic.md) §0: *"I collapsed 'can't be
linked' into 'can't be Go,' which is wrong"*). The real constraint is the `goSrc` fileset, and it
rules out exactly one thing — linking third-party Go into the yolo binary. A separate program spoken
to over a protocol is unaffected.

**Publishing the server-side spec is necessary but cannot be the only path, because of an
enforcement asymmetry.** On the client side a sloppy implementation harms only itself. On the server
side every security-critical property is invisible to yolo:

| Property | Enforced by | Can yolo detect a violation? |
|---|---|---|
| endpoint file `0600` | the daemon's own publish | **No** |
| publication dir `0700`, owner, not-a-symlink | the daemon's own publish | **No** |
| private key never persisted | the daemon | **No** |
| constant-time token compare | the daemon | **No** |
| frame length cap checked before allocation | the daemon | **No** |
| token entropy | — | **Only the format.** `svcendpoint.IsToken` accepts 64 hex zeroes |

A pack is a *distributed* artifact. Spec-only would mean shipping other people's TLS-server and
credential-minting code to strangers' machines, where a `0644` publish is undetectable by the
framework and invisible to the user. That is a materially different proposition from a hand-written
config entry on the author's own machine.

**So: write the spec, and make the front the supported path.** The load-bearing properties of this
transport are all enforced inside one implementation and none are observable from outside it.
Spec-only distributes the *obligation* and keeps zero ability to check it; the front distributes the
*capability* and keeps **one** implementation of the obligation. Same reason `svcendpoint` holds both
halves in one package rather than splitting server from client (`internal/svcendpoint/doc.go:46-53`),
and the same reason row **T2** refuses a second TLS implementation in generated Python.

**Deliverable:** a *"Writing a server from scratch"* section in
[`loophole-protocol.md`](loophole-protocol.md), mirroring the client one, stating plainly that it is
the **unsupervised** path — yolo cannot verify the mode, the compare or the cap, so a daemon on it is
trusted to the degree its author is. **And per §2.1's review ruling, it is reachable only by a
loophole yolo itself ships**: the spec is written so the framework's own front can be understood and
audited, not so a pack can opt out of it. Three couplings belong in it because they are enforced by yolo's
*health* code rather than by the wire, so a conforming-looking daemon dies with a misleading symptom:
the token must be exactly 64 lowercase hex (`svcendpoint` `IsToken`) or the file parses fine, probes
false, and the daemon is SIGKILLed after five seconds with a healthy-looking log; `Probe` and not
existence is the health predicate everywhere; and `yolo check` dials `127.0.0.1` with the *published
port* (`svcendpoint.DialLocal`), so the listener must accept on loopback regardless of what it
advertised.

**Keep the export available and additive.** `svcendpoint` and `frameproto` are stdlib-only leaf
packages with zero internal imports (measured: `go list -deps ./internal/svcendpoint | rg yolo-jail`
returns itself only). If a Go-authored third-party loophole ever appears, moving one costs a `goSrc`
fileset entry plus an API commitment. Nothing here forecloses it.

---

## 3. The 15th kind: `loophole`

```jsonc
{ "kind": "loophole", "from": "loopholes/acme-proxy" }
```

**The contribution POINTS AT a module directory; it does not inline the manifest.** `from` names a
pack-relative directory containing `manifest.jsonc` — the same on-disk shape a bundled or user
loophole already has, so one loader reads all four sources and an author can develop the loophole
standalone and then drop it into a pack unchanged.

### 3.1 Validation

- `from` is **required**, and runs through the traversal guard every path-bearing field of every kind
  gets (`appendPathProblems`, `internal/packdecl/contributes.go`). Absolute paths, `..` and `:` are
  refused as a security property.
- A `from` naming a directory the pack does not contain is **refused by name** — a launch warning and
  a non-zero exit, never a silent skip. Precedent: `skills`' absent-`from` refusal
  ([`pack-system.md`](pack-system.md) §3).

  **Why this is fatal while an unloadable `manifest.jsonc` only warns** (`discover.go:146-159`,
  pinned by `TestInvalidManifestDoesNotBreakOthers`) — review flagged the split as unexplained, and
  it is a **layer** split, not an inconsistency. A missing `from` is a `pack.json` error, decidable
  by `yolo pack lint` without loading any loophole, in a tree the user explicitly selected: refusing
  is a fix. An unloadable manifest is discovered inside `internal/loopholes`, whose contract across
  all four sources is warn-and-continue, because one bad third-party manifest in a shared directory
  must not take the others down with it. The pack layer refuses; the discovery layer warns.
- The loophole's `name` must equal the directory basename. Already enforced by `loadManifest`
  (`internal/loopholes/load.go:58-63`), so it comes free — and it is what lets the footprint name the
  loophole without decoding its manifest.
- **Combine: Exclusive, by loophole NAME.** Exclusivity is per name, not per pack, so a pack shipping
  three loopholes is ordinary — the same rule `program` has per `bin`.
- **A name collision is FATAL, naming both sources** — but the mechanism is NOT free, see below.
  This is S1's skills lesson, and it is stronger here: a shadowed loophole name means a daemon nobody
  audited running under a name the user trusts. Fatal for pack-vs-pack **and** pack-vs-reserved. The
  user loophole dir keeps its current last-wins overwrite (`discover.go:189-202`) because a
  hand-placed directory carries the user's own authority — the same reason a `file://` pack does.
  That asymmetry is deliberate and is named in §9 (OQ-LP3).

**The reserved namespace is larger than "bundled", and draft 1 missed two names.** `bundled_loopholes/`
holds three (`audio`, `claude-oauth-broker`, `host-processes`), but `paths.go:48-51` also reserves
`BuiltinCgroupLoopholeName = "cgroup-delegate"` and `BuiltinJournalLoopholeName = "journal"`, and
`internal/loopholes` never mentions either constant. A pack shipping `loopholes/journal` today would
load, be discovered, and have its daemon skipped **without a word** at `loopholesruntime.go:152-155`
— while `RuntimeArgsFor` still emitted its `--add-host`, `ca_cert`, `--device`, bind mounts and
`jail_env`. Half a loophole, silently. The inconsistency is already visible in-tree:
`config.loopholes.cgroup-delegate` is an explicit config-scope ERROR
(`internal/config/validate_loopholes.go:42-46`) with no manifest-side equivalent.

**Requirement:** define the reserved set ONCE (`paths.Builtin*LoopholeName` + `broker.BrokerLoopholeName`
+ the bundled names) and refuse it by name in the loader, mirroring `validate_loopholes.go:42-46`'s
message. And make the `loopholesruntime.go:152-155` skip **print** when the name did not come from
the builtin.

#### The pack-shipped subset of the manifest, corrected

Draft 1 refused two fields. Review showed the second refusal audits the wrong axis, and measurement
showed it is a no-op for the case that matters.

| Refused for a pack-shipped loophole | Why | Use instead |
|---|---|---|
| `jail_env` | it emits `-e K=V` (`internal/loopholes/runtime.go:156-159`), colliding with the `env` kind's target namespace — and `Collisions` keys on `{kind, target}` (`internal/packload/footprint.go:230-245`), so two *different* kinds claiming one target can never collide | the `env` kind, which the footprint already sees |
| `host_bind_mounts[].host` outside the pack's own tree or the user's home | see below — this is the axis that matters | `mount` (home-relative, `:ro`), or a `host_daemon` that mediates |
| `host_bind_mounts` with `readonly: false` | still refused, but for a **narrower** reason than draft 1 claimed | as above |

**Correction 1 — drop the "disjoint by luck" premise.** Draft 1 justified the `jail_env` refusal
partly with *"today every kind's namespace is disjoint by luck; this would end that."* **False:**
`program` and `launch` already share the bin-name target namespace by design
(`footprint.go:75`, `:122`), and the census pack declares both on `censusbin`
(`applyhostcensus_test.go:88`, `:103`). The conclusion stands — the refusal buys a simpler footprint
— but it rests on the cost of a cross-kind collision pass, not on an invariant that does not exist.

**Correction 2 — `:ro` is not a boundary for a Unix socket. Measured, twice.**

```console
$ podman run --rm -v /tmp/sockro/s.sock:/ro.sock:ro -v /tmp/sockro/s.sock:/rw.sock \
    python:3-alpine python3 -c '<connect to both>'
/ro.sock CONNECT_OK b'HELLO'
/rw.sock CONNECT_OK b'HELLO'
```

Measured in this jail 2026-08-13, reproducing review's own result: a read-only bind of an AF_UNIX
socket is **fully connectable and bidirectional**. The kernel's read-only check exempts
non-REG/DIR/LNK inodes; this is the well-known `docker.sock:ro` result. So the `readonly: false`
refusal applies to **regular files and directories** and buys nothing for sockets — a pack can bind
any host socket `:ro` (a container socket, `ssh-agent`, `gpg-agent`, the PipeWire daemon) and get
unrestricted read-write access to whatever is behind it.

**So the axis draft 1 audited was the wrong one.** It checked rw-versus-ro and missed **path scope**,
which is where a `:ro` loophole bind really is the "back door around `mount`" the sentence forbids.
Compare the `mount` kind on both axes: `packdecl.go:246-263` refuses absolute paths, `..` and `:`
(so `mount` is **home-relative only**), and `packload.go:211-226` `HonoredMounts` refuses **every**
mount when `!p.MayAccessHost` — *"a FETCHED pack cannot read your host home."* A loophole's
`host_bind_mounts[].host`, by contrast, is any non-empty string (`load.go:284-306`), `$HOME` and
`..` and `/` all pass `expandEnv` at `:302`, and `runtime.go:131-141` emits `-v <host>:<container>:ro`
for each.

**Requirements:**

1. Constrain a **pack-shipped** loophole's `host_bind_mounts[].host` to the same home-relative,
   `..`/`:`-guarded namespace `mount` uses. Absolute paths and `$VAR` expansion are refused. (A
   *bundled* loophole keeps the wider vocabulary — `audio` names `/run/user/<uid>/pulse`.)
2. Treat a socket bind as **its own claim class**: it is host IPC, not a host read, and the claim
   string must say so (§3.3).
3. Keep the `readonly: false` refusal, and say what it actually covers.

**Everything else is allowed — and every one of them is CLAIMED (§3.3):** `host_daemon`,
`jail_daemon`, `intercepts` + `broker_ip` + `ca_cert`, `host_bind_mounts` (`:ro`), `host_devices`,
`state_files`, `requires`, `doctor_cmd`, `serves`.

**The `jail_env` refusal has a real cost, stated rather than hidden.** A loophole's `jail_env` is
*conditional on the loophole being active*; the `env` kind is unconditional. `audio` relies on
exactly that (`PULSE_SERVER` only makes sense when the sockets crossed). So a pack-shipped
audio-shaped loophole would set env even when inactive. That is the case that would justify a
cross-kind collision pass, and it is purely additive — same claim model, one more pass beside the
three bespoke ones already there (`footprint.go:311-352`, `:357-384`, `:419-453`).

#### A loophole must declare where it can run — REVIEW ADDITION

The framework owning the wire (§2) makes the **transport** portable. It does not make the **daemon**
portable, and the maintainer's note is that packs will ship native things: *"as we may ship native
things, we also need to be platform aware for what platforms are supported."*

Today's `requires` predicates (`command_on_path`, `file_exists`; parsed at `load.go:250-262`,
evaluated by `RequirementsMet` at `loopholes.go:201`) express
*"the thing I need is present"* — a runtime probe. They cannot express *"I only exist for this
platform"*, and the difference is not cosmetic. A pack shipping a compiled Linux daemon on macOS
should be reported as **unsupported here**, not as a requirement that happened to be unmet (which
reads as "install the missing thing") and certainly not as a spawn that fails five seconds later
through §2.1c's silent path.

**Requirement:** the manifest declares supported platforms — `GOOS`, and `GOARCH` where it matters —
validated statically at load and evaluated host-side during discovery. A selected pack whose loophole
is unsupported on this machine is reported **by name**, once, with the platforms it does support.

**And it shares its mechanism with the inert-backend report (§8), deliberately.** Platform
(`darwin` vs `linux`) and backend (`container` and `macos-user` skip loopholes entirely) are two
different axes with one answer shape: *this loophole does nothing here, and here is why.* Two
mechanisms would give two half-messages for one user-visible situation, which is the B-0 shape again.

**This is also the extension point for a future native transport, named and not designed.** The
maintainer's read: *"until we need a very native platform specific one that can't work with all
runtimes, which we should plan for I guess, but not truly design yet."* A platform declaration is
what makes such a loophole expressible later without a schema break —
[`extension-point-principle.md`](extension-point-principle.md)'s rule exactly, and the cost of
omitting it now is a migration for every manifest in existence at that point.

#### The fatal-collision rule is NOT free, and cannot live in `Discover`

Draft 1 claimed `Collisions`' generic Exclusive pass *"refuses two packs shipping one loophole name
**for free** … with no fourth bespoke pass."* **Wrong in three ways, all measured:**

1. **`packload.Collisions` is never consulted at launch.** Two callers, neither on the run path:
   `internal/cli/pack.go:922` (the `pack footprint` report) and `internal/cli/check/packs.go:173`
   (which passes `packload.Embedded()` — embedded packs only). The launch pre-flight refuses exactly
   three things and says why: `packDestConflicts(loaded, KindFiles)` (`run/packs.go:248`),
   `packFilesShadowedSurfaces` (`:255`), `ConfigSurfaceCollisions` (`:269`) — under a comment at
   `:266-268` reading *"Only the CONFIG collision, not `packload.Collisions` wholesale … so widening
   this to the whole set would refuse launches that work today."*
2. **The generic loop skips single-pack groups** (`footprint.go:247-252`: `if len(packSet) < 2
   { continue }`). One pack declaring `from: "a/acme"` and `from: "vendor/acme"` — both basename
   `acme`, both valid — collides with itself and is not reported. That is the exact hole which forced
   `ConfigSurfaceCollisions` to be its own exported pass.
3. **`Collisions(packs []*Pack)` cannot see bundled or reserved names at all** — they are not
   `*Pack`s — so the pack-vs-reserved case is not expressible there.

**And fatality cannot be implemented inside `Discover`.** Its signature is
`func Discover(opts DiscoverOptions) []*Loophole` (`discover.go:181`) — **no error channel** — and
`resolver.go:26-28` states the invariant every caller relies on: *"Discovery never errors (per-manifest
and per-dir failures are swallowed), so `ok` is always true."* Making it fatal there reverses a
tested invariant across seven call sites.

**Requirement: a FOURTH bespoke pre-flight**, shaped like `ConfigSurfaceCollisions` — per
*declaration* rather than per pack, taking the reserved set as a second input — wired into
`stagePacks` beside the other three (`run/packs.go:248-276`), returning an error so the launch
refuses. This is real work the design previously priced at zero, and it is now in the landing order.

### 3.2 Where the schema has to live — and this is a real blocker, not a case in a switch

**`packload` cannot import `internal/loopholes`. It is a cycle, measured:**

```console
$ go list -f '{{join .Imports "\n"}}' ./internal/loopholes | rg yolo-jail
…/internal/config …
$ go list -deps ./internal/config | rg 'packload'
…/internal/packload
```

`internal/loopholes` → `internal/config` → `internal/packload`. So the `config` kind's precedent does
not transfer: that one works because `packload` *can* decode surfaces
(`internal/agentcfg/manifest` is in its dep set, used at `footprint.go:151-160`). For a loophole,
`FootprintOf` would have nothing to decode the payload with — and the footprint is where the whole
trust story lands (§4.3).

**Recommendation: extract the loophole manifest SCHEMA into a leaf package** —
`internal/loopholedecl`: parse + static validation only, no `exec.LookPath`, no `os.Stat`, no
predicate evaluation. It may import `json5`/`jsonx`/`pytext`, all of which are measured leaves, so
there is no cycle. This is the placement rule `packdecl` (`kinds.go:13-17`) and `pluginpack`
(`pluginpack.go:24-25`) both document, and it has a payoff independent of packs: the schema becomes
readable by the footprint, by `yolo pack lint`, and by a host-side validator without dragging the
runtime predicates along.

The alternative — break the `loopholes` → `config` edge, which is only two files (`resolver.go:3`,
`loopholescmd.go:16`) — is cheaper in lines and worse in shape, because it leaves schema and runtime
fused. Marked **OQ-LP1**; either resolves the cycle, and the doc's design does not depend on which.

**The loader also needs the strict/tolerant split it does not have.** `loadManifest` is a hand-rolled
`jsonx.OrderedMap` walk with **no unknown-key rejection at all** — `"version": 1` is declared by all
three bundled manifests and documented as the schema version, and nothing reads it. Contrast
`packdecl.Decode`'s `DisallowUnknownFields` for authoring plus a deliberately tolerant
`DecodeTolerant` for the version boundary (`packdecl.go:144`, `:206`). A pack-shipped loophole
crosses that boundary — host CLI reads it, and a skewed baked entrypoint may too — so it needs both
halves. Today it would tolerate skew and never tell an author about a typo.

**Sanitize at load, not at display.** Every field that feeds an approval claim (§4.3 G2) — the
`host_daemon.cmd` strings, intercept hosts, bind-mount paths — must refuse **control characters and
newlines**. The prompt renders claims through `richtext.Printf`, which formats first and parses
style tags over the result (`richtext.go:109-112`), and `ToANSI` rewrites only recognized tags, so
raw ESC bytes and newlines pass through untouched. A manifest could otherwise inject fake claim
lines (`"cmd": ["python3", "srv.py\n      [dim]mount ~/Documents -> /ctx/docs[/dim]"]`) or overwrite
the ⚠ header with `\e[2K\e[A` into the one screen the whole trust story rests on. Partly
pre-existing — the same shape applies to today's `mount` claims — but this design is what promotes
that prompt from *"may read a home file"* to *"may run code as you"*, so it must not inherit the
weakness silently. Refusing at load is better than escaping at display: one gate, and the author
hears about it.

### 3.3 Footprint entry — one contribution, several claims, and the enumeration must be TOTAL

`FootprintOf`'s switch appends per-claim in a loop already (`footprint.go:59-184`), so a loophole
emitting several is representable.

**The blocker draft 1 shipped:** it defined exactly two claim classes (the daemon argv and one per
intercept) and then listed `host_bind_mounts (:ro)`, `host_devices` and `state_files` under
"everything else is allowed" with no claim attached to any of them. Combined with
`run/packs.go:392-397` —

```go
want := append(p.Decl.HostAccessClaims(), p.PluginHostAccessClaims()...)
…
if len(want) == 0 {
    return true // reads nothing from the host, runs nothing on it; the gate is moot
}
```

— a `transport: none` loophole with no `host_daemon` and no `intercepts` yields `want == []`, so
`packMayAccessHost` returns **true for a FETCHED pack**, and `yolo pack install` prints a green pin
line and asks nothing. A pack of
`{"name":"nice","transport":"none","host_bind_mounts":[{"host":"$HOME/.ssh","container":"/ctx/keys"},{"host":"/","container":"/ctx/root"}]}`
would mount the user's SSH keys and the whole host filesystem into a jail whose agent runs as UID 0,
with **no prompt at which the user could have stopped it** — strictly wider than anything a fetched
pack can do today, and violating [`pack-system.md`](pack-system.md) §12's second invariant verbatim.
Worse, §7 nominated exactly this shape as the dogfood *"that needs no approval to run"*, so the
design read it as a feature.

**Rule, and it is the load-bearing one in this document: a claim-free loophole must be
unrepresentable.** Every declaration that crosses the boundary emits its own claim:

```
loophole  acme-proxy                RUNS `python3 {loophole_dir}/acme-daemon.py --socket {socket}`
                                    on your machine                                        ⚠ review
loophole  acme-proxy:api.acme.com   INTERCEPTS api.acme.com — installs a CA trusted by every
                                    TLS client in the jail                                 ⚠ review
loophole  acme-proxy:mount:/ctx/x   MOUNTS ~/x -> /ctx/x (read-only)                       ⚠ review
loophole  acme-proxy:ipc:/ctx/s     CONNECTS the jail to the host socket ~/s — read-write
                                    regardless of `:ro` (measured)                         ⚠ review
loophole  acme-proxy:dev:/dev/snd   PASSES THROUGH the host device /dev/snd                ⚠ review
```

- **Target** is the loophole name for the base claim, `<name>:<discriminator>` for each other. So a
  pack with three bind mounts emits three separately-approvable strings.
- **The argv goes in `Detail`, and the words "on your machine" are spelled out.** `ReviewWorthy` is
  one boolean — one severity — and it currently means "reads `~/.claude.json`". Host execution must
  be distinguishable from a host read, and the in-tree precedent solves exactly this without adding
  a severity field: `pluginClaimDetail` spells **"RUNS CODE"** into the Detail string
  (`footprint.go:189`, `:206`).
- `doctor_cmd` is host execution too (`runtime.go:209` `RunDoctorChecks`, called from
  `check/sections_loopholes.go:47` and `loopholescmd.go:138`), so it joins the base claim's argv
  list rather than getting its own line.
- **The intercept claim exists even with no daemon.** A `transport: none` loophole that declares
  `intercepts` runs no host code and still installs a CA into every TLS client in the jail — measured
  in [`loophole-transport.md`](loophole-transport.md) §5.0.1.
- **A device claim is not weaker than a rw bind mount, and draft 1 allowed it while refusing one.**
  `audio`'s own manifest describes `--device` as passing a node *"so the cgroup device-allow rules
  permit reads/writes"* (`manifest.jsonc:73-78`). Same objection, so: same claim, and the
  home-relative constraint above does not apply to a device node — which is precisely why it needs
  the claim.
- **`state_files` needs no claim.** It resolves under `StateDirFor(name)` in yolo's own state tree
  (§8), not into a path the user would recognise as theirs.

**Where the claims are produced — NOT `packdecl`.** Draft 1's §4.3 G2 put them on
`Manifest.HostAccessClaims()`. That method is a pure walk over decoded `pack.json` bytes
(`contributes.go:607-623`); `packdecl` has **zero internal imports** by design (`kinds.go:13-17`),
`Manifest` carries no root path, and the daemon argv lives in a separate file. The naive
implementation would degrade the consent string to a bare `loophole acme` — a string that never
changes no matter what the daemon becomes, which collapses straight into the content-blindness
problem (§4.3 G2b).

**The correct layer already exists and has a precedent:** `PluginHostAccessClaims` lives on
`packload.Pack` (which has `Root`) precisely because plugin claims come from a file outside
`pack.json` (`plugins.go:79-88`). So: a `LoopholeHostAccessClaims()` on `*Pack`, reading through
`internal/loopholedecl`. And state the invariant honestly rather than repeating draft 1's *"read
through one predicate so a caller cannot honor some and miss another"* — **that was already false.**
There are two producers today, appended by hand at each gate (`pack.go:1100`, `run/packs.go:392`),
with a comment at `packs.go:388-391` warning what happens when one is updated and the other is not.
A third makes it worse. **Requirement: one helper that merges all producers, called at both sites,
with a test that fails if a site calls a producer directly.**

**Mechanical costs**, each enforced by an existing test that fails until updated: `kinds_test.go:30`
hardcodes `14`; `kinds_test.go:99-107` hardcodes the review-worthy set; `applyhostcensus_test.go`
fails by name until the kind appears in `apply --host` output — and note it builds its pack from
`packdecl.KnownKinds()` and `t.Fatalf`s on a kind with no census contribution (`:110-115`), so the
new kind needs a census entry whose `from` dir and `manifest.jsonc` the helper must create;
`packkinddocs_test.go` fails until the kind is named in **both** `internal/cli/config_ref.txt` and
`packUsage` (`pack.go:57`). Also: `printPackFootprint` (`pack.go:473-483`) and `reportFootprint`
(`:901-911`) duplicate the claim-formatting loop despite `:464-466` claiming they are shared "so
their output does not drift" — a new marker has to be added twice or the two commands diverge.

**And one cost NO test catches:** `notePackHostAccess` (`run.go:230-243`) switches on
`KindMount, KindReadsHost, KindEnv` and drops every other claim kind. Its own comment calls it
*"the transparency half of the approval model"* — see §4.3 G4, which also fixes its ordering.

**Correction to draft 1's `pack footprint` requirement.** It asked that `pack footprint` *"say which
side of the gate the claim is on"*. That command cannot report a fetched pack at all: `packFootprint`
handles a local/`file://` dir or an embedded pack NAME and errors otherwise (`pack.go:794-836`). So
the wants-versus-gets distinction has no surface there. It belongs at `yolo pack install`'s prompt
(`pack.go:1089-1123`) and in the launch-time refusal lines (`run/packs.go:218-231`) — or
`pack footprint` grows the ability to take a configured pack name, which is a separate, small item.

### 3.3a The kind is an A12-fatal boot break against a stale image — the `tier` incident, third time

**This is the finding draft 1 missed entirely, and it fails the jail rather than the feature.**
`packdecl.DecodeTolerant` is explicit that it does **not** tolerate this class:

> *"Structural validation still runs, so a manifest that is malformed in a way BOTH builds understand
> (**an unknown kind**, a missing required field) still fails loudly here."* (`packdecl.go:196-210`)

It calls `m.Validate()` → `validateContributions` → `ValidateKind(c.Kind)` (`contributes.go:661`).
The in-jail entrypoint calls `packload.TolerateSkew()` (`entrypoint/packsurfaces.go:58`) and then
`LoadJailPacks` returns any problem as an error — *"the boot fails (A12)"* (`:41-51`, `:86-89`).

**Concretely:** a fresh host `yolo` knows `loophole`; the image is frozen at the last host
`just load`; a user selects any pack declaring `{"kind":"loophole"}` →

```
yolo-entrypoint: refusing to start the jail: … load_packs: pack acme: contributes[0]:
  unknown kind "loophole" (expected one of autonomy, briefing, …)
```

and the jail does not start. This is byte-for-byte what `packdecl.go:154-205` was written to record
(`tier`) and its mirror image (`skills_tier`) was written to prevent.

**Decision: make an unknown KIND tolerated under `TolerateSkew()`** — skip the contribution, report
it by name — and land that **before** the kind. It is the same asymmetry `retiredFieldProblems`
already documents at `packdecl.go:154-172`: an author must hear that their declaration is unknown,
and a jail must still boot when the two ends of the version boundary disagree about which kinds
exist. Skipping is the right degradation here specifically because a `loophole` is rendered
**host-side** (§5.1) — a jail that skips the contribution loses nothing it was going to render
anyway. It needs a regression test that a manifest with an unknown kind **boots a jail**, and
AGENTS.md's `git add`-before-nix trap applies to verifying it.

The alternative — "require a host `just load` before any pack may declare it" — is not a mechanism,
it is a hope, and it cannot be stated to a third-party pack author at all.

### 3.4 At the HOST target, where there is no jail: refused — and the naive reason is backwards

`HostFields()` (`internal/render/fieldset.go:122-149`) is an explicit allowlist and `JailFields()` is
*derived* from `packdecl.KnownKinds()` (`:105-111`). `loophole` must land in `refusalReasons`
(`:37-42`) rather than fall through to `Refuse`'s generic *"X is not applicable at this confinement
level"* (`:33`), because the generic line would be the single most confusing sentence in the command.

**The trap: a loophole's effect *is* on the host, so "not applicable off-container" reads as
obviously wrong.** The honest reason is the inverse:

> `loophole` is a host daemon whose only client is a container. With no jail there is no client, no
> `--add-host`, no `YOLO_JAIL_DAEMONS`, and nothing for the endpoint file to be mounted into.

Refused because its **counterparty** is missing, not because its mechanism is.

**And that refusal is a feature for the trust story.** `apply --host` is the one command that mutates
the real machine, and it deliberately never runs pack `hook`s for this reason
(`fieldset.go:86-93`). A `loophole` refused there means *"selecting this pack runs a daemon"* is a
statement about **launching a jail**, not about **applying a config** — which keeps the blast radius
attached to a command the user runs deliberately.

**Correction: "honored in a jail automatically" is not what happens.** Draft 1 read `JailFields()`'s
derivation as a correct default. Measured: `Target.Fields()` has **no production caller** (`rg -n
'\.Fields\(\)' --glob '!*_test.go' internal/` is empty); the only consumer of a `FieldSet` is
`render.HostFields()` at `apply.go:189`. And the jail-side effect of a `loophole` is produced by
`startLoopholes` in the **host CLI before the container exists**, not by
`entrypoint.ConfigurePackSurfaces`. So the honest census answer at `jail` is **"rendered elsewhere —
its actor is the run pipeline"**, not "honored automatically". If the FieldSet is to remain the
census's executable form, `loophole` should be excluded from `JailFields()` explicitly rather than by
derivation, so the census does not assert something no code reads.

**`guest` is the real question and this doc does not pre-answer it.** `Target.Fields()` funnels
`KindGuest` into `HostFields()` today (`fieldset.go:171`). A guest is a real process on the real
machine under an LSM/Seatbelt profile — so a loophole daemon serving it is **coherent**, unlike at
`host`. Recorded as OQ-LP7, not decided.

---

## 4. Trust — the existing hole is real, but the kind is still a WIDENING

### 4.1 The finding

**The `loopholes` block in `yolo-jail.jsonc` already executes an arbitrary host command, gated by
nothing, and a WORKSPACE config can write it.**

- `internal/cli/run/loopholesruntime.go:138-151` scans the **merged** config's `loopholes` map and
  adds every entry with a `command` to the external-service set; `:161` calls
  `startExternalService`, which at `:384` runs `exec.Command(cmdArgs[0], cmdArgs[1:]...)` with
  `Setsid: true` (`:388`), env from the entry's own `env` block with `~` expansion (`:402-412`).
- **No gate of any kind.** No prompt, no lockfile, no origin check, no `allow_exec`, no launch-time
  notice — and per §2.1c a **successful** spawn is silent and so is a **timed-out** one.
- **`loopholes` is not user-scope-only.** Exactly three keys are: `packs`
  (`internal/config/packs.go:484`), `host_files` (`hostfiles.go:938`), `cache_relocations`
  (`validate.go:1025`) — whose message is verbatim the argument that applies here: *"a workspace
  config is agent-editable, so it cannot grant read-write host mounts."* `loopholes` is absent, and
  `loopholescmd.go:60-82` merges the user **and workspace** blocks explicitly, **workspace last**.
- **The retired transport does not disarm it.** Execution precedes the reachability wait: the daemon
  runs, and only after the 5-second deadline is `cmd.Process.Kill()` called — and because of
  `Setsid`, only the direct child.

**Three more host-exec sites draft 1 under-counted or missed:**

1. **`doctor_cmd`**, run by `yolo check` (`check/sections_loopholes.go:47`) and `yolo loopholes
   status` (`loopholescmd.go:138`). Draft 1 named it and then dropped it from the mitigation.
2. **The loophole commands read config with NO validation at all.** `loopholescmd.go:40-53` uses
   `config.LoadJSONCFile` / `config.LoadWorkspaceConfig`, and `config/load.go:27-52` **parses only**
   — no schema pass. Review demonstrated this empirically: a workspace `yolo-jail.jsonc` containing
   only `loopholes: { "evil-doctor": { "description": …, "doctor_cmd": […] } }` — with **no
   `command`**, which `validateInlineService` requires, and with two keys that are unknown per
   `config.go:74` — printed `active evil-doctor (config/unix-socket/spawned)` from
   `yolo loopholes list`. `yolo check` would reject that file; the loophole command honors it anyway.
   (The execution step is code-path-verified, not run-verified: `Status` short-circuits in a jail at
   `loopholescmd.go:127-130`.)
3. **`env` in the OVERRIDE shape reaches a FIRST-PARTY daemon's spawn environment.** A workspace
   entry with no `command` at all can set `loopholes.claude-oauth-broker.env`, which
   `discover.go:96-104` merges into the existing bundled daemon's `HostDaemon.Env`, which
   `ManifestHostDaemonSpecs` (`runtime.go:187-194`) hands to `startExternalService`'s env block
   (`:402-415`). So `LD_PRELOAD` on a daemon **yolo** spawns, from an agent-editable file. This is
   the strongest single argument G1 has and draft 1 never made it.

And the vocabulary reachable from a hand-placed manifest is *broader* than any pack kind:
`host_bind_mounts` accepts `readonly: false` (`load.go:294-306`) and any absolute path, plus
`host_devices`. Ungated by origin or approval.

### 4.2 The honest framing — REWRITTEN, because draft 1's inversion does not follow

Draft 1 argued: *"not 'should we open a new hole' but 'the existing hole is wider than the one we
are being asked to open'"* — that the kind is a step **downward**. **Review refuted this and it is
withdrawn**, for two reasons that are worth keeping visible because the argument is tempting:

1. **It compares against a baseline this same batch removes.** "What must land together" item 1 is
   `loopholes.command`/`env` user-scope-only, described as *"the biggest risk reduction. Ship
   first."* At the moment the 15th kind lands there is no ungated config host-exec left to be
   downward from. The kind is measured against the **post-G1** world the same batch creates, and
   against that world it is net-new host execution.
2. **The two acts are not equivalent, and the repo already encodes the distinction.** Writing
   `loopholes.command` requires an attacker to already be able to write a file on the user's machine.
   A fetched pack is a **distribution channel** that ships and re-ships the code itself.
   `packMayAccessHost` (`run/packs.go:378-381`: embedded/local ⇒ true, fetched ⇒ approval) exists
   precisely because *"a directory the user controls"* and *"someone's git URL"* are not the same
   authority. Draft 1 argued as if they were.

**So: this kind IS a widening, and it should be justified as one.** The justification is not that the
hole already exists — it is that (a) the approval machinery is exactly the kind of thing this
boundary was built for, (b) the claim enumeration is now **total** (§3.3), so nothing crosses without
a string a user saw, and (c) the alternative is that third-party loopholes stay stranded on a
transport that no longer validates, which is not a safer world, only a poorer one.

§4.1 stands on its own as an independent finding. G1 should ship whether or not the kind does.

### 4.3 Four gates, all of them shipped machinery — plus one new invariant

**G1 — the `loopholes` block's host-exec surface becomes user-scope-only.** Draft 1 specified this
over key *names*; the validator is specified over entry **shapes** (`validate_loopholes.go:52-68`:
the *override* shape, whose keys are a subset of `{enabled, env, jail_env}` with no `command`, and
the *inline* shape, which requires `command`). Rewritten over shapes:

| | User-scope-only | Both scopes |
|---|---|---|
| **inline** (the `command` shape) | `command`, `env`, `doctor_cmd` — the whole entry | — |
| **override** | `env` (§4.1 finding 3: `LD_PRELOAD` into a first-party daemon), `doctor_cmd`, and `enabled` **for any loophole with a `host_daemon` or `doctor_cmd`** | `enabled` for a loophole with neither; `jail_env` |

**`enabled` needs both directions restricted, and this is where draft 1's "good defaults survive"
sprang a leak.** Draft 1 said the pure toggles *"stay writable at both scopes, because disabling a
loophole and naming container env vars are not host execution."* That reasoning covers only the OFF
direction — and it is wrong in both:

- **ON is host execution.** `discover.go:94` sets `Enabled` in *both* directions for any discovered
  loophole; `runtime.go:181-183` `ManifestHostDaemonSpecs` skips `!m.Active()`, so `enabled: true` is
  precisely the switch that admits a manifest daemon to the spawn. And workspace **wins**
  (`config/load.go:223`), so a user who turns a pack loophole off in their user config gets it turned
  back on by any repo whose `yolo-jail.jsonc` says `enabled: true` — the exact file the rest of this
  design distrusts. Today's CLI actively directs them at the weaker scope
  (`loopholescmd.go:168-171`).
- **OFF removes a safety property.** `loopholes.claude-oauth-broker.enabled: false` in a workspace
  file drops the broker entirely: `discover.go:209` filters it out, `brokerLoopholeActive` returns
  false (`assemble_parts.go:423-431`), no endpoint env var, no `--add-host`, no relay — and
  **nothing prints**. `yolo check` renders it as a **green pass**
  (`check/sections_loopholes.go:34`: `r.ok("loophole " + lp.Name + ": disabled")`). An agent
  committing one line to a repo config silently reintroduces the single-use-refresh-token race the
  broker exists to prevent. That is the availability half of the risk, and G1 as drafted left it wide
  open while claiming §5.4 kept good defaults safe.

**So, two more requirements beside the scoping:** any workspace-sourced `enabled: false` must print
one launch-time line naming the loophole **and the file that disabled it** (pack-capabilities' own
rule: *"anything that turns something off must name who did it and why"*), and `yolo check` must
**warn**, not `ok`, when the disabling came from a workspace config.

**Drop `jail_endpoint` from G1's list.** Draft 1 listed it among the pure toggles. It is not in
`knownHostServiceKeys = set("command","env","jail_socket")` (`config.go:74`), so it is an
unknown-key **error** on an inline entry today (`validate_loopholes.go:114`) while
`:142-158` prefix-checks it — the doc's own R5 finds the contradiction. Fix `knownHostServiceKeys`
(adding `doctor_cmd`, `description`, `jail_endpoint`) **before** scoping anything over it, or the
canonical validator and the loader keep disagreeing about which keys exist.

**G1 REMOVES A CAPABILITY, not just a spelling — review found the gap.** *"Meaning you can't declare
a loophole in a workspace? then how do you provision some workspaces with a priv and not others?"*
Two halves, already different today:

- **A pack-shipped loophole was never per-workspace.** `packs` is user-scope-only now
  (`config/packs.go:182-184`: *"makes workspace scope inexpressible"*), so selecting a
  loophole-bearing pack per repo is impossible before and after this design.
- **A config-block loophole IS per-workspace today, and G1 removes exactly that** — a workspace
  `yolo-jail.jsonc` with a `command` gets a host daemon for that repo alone. It works *because* it is
  ungated, i.e. via §4.1's hole.

**So after G1 there is no per-workspace loophole mechanism at all**, and there is no per-workspace
scoping anywhere else in the config to borrow: the layering is user → workspace → workspace-local
(`config.go:34`, `load.go:202`), and `yolo-jail.local.jsonc` lives *in the workspace*, so it is as
agent-editable as the tracked file and buys nothing here. Two shapes give the capability back without
giving it to the agent — **(a)** a user-scope declaration naming which workspace paths get it, or
**(b)** the workspace *asks* and the human's approval is recorded host-side, keyed by (workspace,
claim set), re-prompting when the ask widens.

**Recommend (b): it is `yolo pack install`'s shipped y/N-plus-lockfile with a different requester**
(`pack.go:1089-1123`, `packsrc.LockEntry`), so it reuses the machinery G2 already depends on rather
than inventing scoping vocabulary. The tempting counter-argument must be answered rather than
inherited: [`three-decisions.md`](three-decisions.md) §0.1 deleted `pack_requests`, but its stated
reason — *"a repo … already has a git repo and can lay out whatever it likes in the workspace"* —
**does not transfer**, because a repo cannot already run host code. That deletion covered a request
that bought nothing; this one buys the only thing that makes the capability safe. **OQ-LP12**, and it
should be decided WITH OQ-LP2 because it is what G1's warning would point people at.

**And G1 needs a migration, because its population is "everyone who followed the shipped guide."**
`docs/guides/loopholes.md:88` reads *"The `loopholes` block is the workspace-scoped entry point"* and
its worked example (`:91-98`) carries a `command`. A scope violation goes into `errs`, and a config
error refuses the **whole launch** (`run/preflight.go:49-56` → exit 1) — the user does not lose a
loophole, they lose the jail. The check also re-reads the workspace file directly and `/workspace` is
live-mounted, so in-jail `yolo` and nested jails break identically. **Requirement:** one release
where a workspace `command`/`env`/`doctor_cmd` is a **warning** naming the exact fix and is still
honored, then the error; `docs/guides/loopholes.md:88` updated in the same commit; and an explicit
decision on whether the check downgrades in-jail the way `agents` does (**OQ-LP9**).

**G2 — a pack-shipped loophole's host claims join the approval set.** Produced at the `packload`
layer (§3.3), merged into `want` at **both** call sites through one helper, recorded as the sorted
set plus the approving commit in `packsrc.LockEntry` (`lock.go:45-56`), superset ⇒ re-prompt with
the full current set, subset ⇒ carry-forward (`:64-78`). **The prompt's sentence is already written
for this** — `pack.go:1113` says *"⚠ pack %s reads your host **or runs code on it**:"*.

**G2a — the claim string is the RAW argv, unexpanded and unelided.** `HostAccessClaims` is a
**lockfile comparison key** walked for exact matches, not display text. Draft 1 printed the claim
with an ellipsis (`RUNS \`python3 …/acme-daemon.py\``) in two places and said the strings *"match the
footprint Details"*, where a Detail is display text. If the approval string elides, two different
daemons collapse to one approved claim; if it instead carries the **expanded** `{loophole_dir}`
(`load.go:302` resolves it to a staging-specific absolute path), the approved string is
machine-specific, never matches elsewhere, and re-prompts forever — where `promptYesNo` fails closed
on a non-TTY (`pack.go:1147-1160`) and `packMayAccessHost` then refuses the loophole entirely. **Rule:
raw manifest argv, placeholders unexpanded, nothing elided. The footprint Detail may abbreviate, and
the two are deliberately not the same string.**

**G2b — a host-EXEC claim and a host-READ claim have different invariants under a pin move, and
draft 1 treated them as one mechanism.** Draft 1 celebrated: *"because the claim string carries the
argv, changing the argv across a ref bump is a superset and re-prompts. That is exactly the behaviour
you want and it needs no new mechanism."* True for argv edits. **An attacker never needs to edit the
argv.**

- `pack.go:1104-1107`: `if hadPrev && prev.HostAccessApproved(want) { return want, false }` — no
  prompt, even though `treeRoot` is a freshly materialized tree at a NEW commit (`store.Sync` at
  `:1016`, `store.Materialize` at `:1022`).
- `HostAccessApproved` compares claim **strings** only (`lock.go:64-78`).
- `ApprovedAt` is written at `pack.go:1055` and has **zero readers** (measured: `rg -n 'ApprovedAt'
  internal/` returns the declaration, a comment claiming `pack status` uses it, the writer, and
  tests). It is dead data.
- Mutable refs are explicitly supported (`packsrc/addr.go:160-162`), launch re-resolves the ref
  (`store.go:237`) and never compares `lock.Commit`.

So: approve `RUNS 'python3 …/acme-daemon.py'` once; the author (or whoever compromises that repo)
rewrites the script entirely, argv untouched; the next `yolo pack install` prints one yellow
`acme main: abc1234 → def5678` line and **no prompt**; the next `yolo run` executes the new code as
the user. For a host READ the claim string (a path) **is** the risk-bearing fact, which is why the
existing model works. For host EXECUTION the risk-bearing fact is **file content**, which no claim
string can carry, and §4.2's "pinned to a commit" read as protection that no code enforces. The
lockfile's own header (`lock.go:5-9`) describes this exact failure.

**Decision: an approval carrying any execution-bearing claim is anchored to the CONTENT, not the
string.** Re-prompt whenever `Commit != ApprovedAt` for such a pack — giving `ApprovedAt` its first
reader. **Deliberately scoped to exec-bearing claims**: applying commit-anchoring to every claim
would change behaviour for shipped `mount`/`reads-host` approvals, where the string genuinely is the
fact. The friction is real — a pack pinned `?ref=main` re-prompts on every commit — and how to soften
it (fold a digest of the module dir into the claim string instead; or advise tag pins) is
**OQ-LP8**.

**G3 — origin still bounds it, and it fails closed.** `packMayAccessHost` (`run/packs.go:378`):
embedded or local ⇒ true; fetched ⇒ the lock must approve every claim the *staged* tree currently
makes; a nil, missing or corrupt lock approves nothing. A fetched pack whose loophole claim is
unapproved has its loophole **not discovered at all** while its other contributions still work — the
same shape `mount` has today, refusals printed per-claim (`packs.go:218-231`). With §3.3's total
enumeration, `len(want) == 0` is now only reachable for a pack that genuinely crosses nothing.

**G4 — the per-launch disclosure, which today would not mention this kind and prints too late.**
`notePackHostAccess` (`run.go:230-243`) is the *"transparency half of the approval model"* by its own
comment (`:568-571`: *"the effective host access must be visible every launch, not just recorded in a
lockfile"*). It switches on a hardcoded `KindMount, KindReadsHost, KindEnv` and drops everything
else. And the ordering is backwards: `startLoopholes` is `run.go:516`, `notePackHostAccess` is
`run.go:572` — **the spawn precedes the notice by an entire phase**, and the spawn itself is silent
on success. A fetched pack's daemon could start on every launch for months with the only host-side
record being a lockfile the user has to go read.

**Requirements:** add the loophole claim kinds to `notePackHostAccess`, add a test that fails when a
review-worthy kind is missing from it, and print the host-**execution** line **before**
`startLoopholes`. For a read, after is cosmetic; for an exec, after is a notification that something
already happened.

### 4.3a EVERY GATE GOVERNS A DECLARATION; NONE GOVERNS THE FILE — review, and it is the worst gap here

*"The agent could change the loophole host binary if it lived in the workspace and that certainly
would not be noticed. I guess we need everything the loophole runs on the host to be promoted under
some manual confirmation as well."* Correct, **pre-existing**, and closed by none of G1–G4.

G1 decides who may write `command: ["python3", "/workspace/tool.py"]`. G2 records that string in the
lockfile. G3 asks where the pack came from. G4 prints it at launch. **Nothing reads `tool.py`** — and
`/workspace` is bind-mounted `:rw`, so the only artifact that actually executes is the only one no
gate inspects.

**The `file://` escape is wider than §4.4 item 2 said, and that item's "not changing it" is
WITHDRAWN.** Measured: `IsLocal()` is `strings.HasPrefix(p.Source, "file://")` (`config/packs.go:126-128`)
with **no path constraint of any kind**, so `file:///workspace/mypack` is `OriginLocal`,
`MayGrantHostFiles()` returns true (`:175-177`) and `packMayAccessHost` short-circuits to true
(`run/packs.go:378-381`) — no prompt, no lockfile entry, no re-approval, ever. And the code's own
justification is explicitly a READ argument: *"content at a path on this machine — authored or
vendored by the user, and readable by them without yolo's help"*, *"the user's own files, which they
can already read"* (`packs.go:137-139`, `:171-172`). Sound for `host_files`. Unsound for host
execution, and void when the "local" directory is one an agent writes.

**One defect, three faces** — and G2b (§4.3) fixed only the first:

| Where | What changes with nothing to notice |
|---|---|
| a fetched pack at a moving ref | file content; the argv never has to change — G2b's commit anchor |
| a `file://` pack under a workspace | the same, with **no approval at any point** to be stale against |
| a config-block `command` naming a workspace path | the same, and **G1 does not touch it**: it gates the declaration, not the target |

**RULED: not content confirmation — a PLACEMENT rule.** Draft 4 required digesting everything a
loophole would execute. The maintainer refused it: *"not sure there's even any confirmation needed —
if you can edit user-level files, you have all the perms already."* Correct, and it dissolves the
mechanism: writing the user config already demands host access as the user, who could equally use
`~/.bashrc` or cron, so a dialog guarding it protects nothing. **The user-scope edit IS the
confirmation** (§4.3b).

What the permission argument does NOT cover is the second actor. It speaks for the human who writes
the declaration; the agent that rewrites the named FILE has none of those permissions. So:
**installed content may not live where an agent writes** — refused at install, by name, when a
loophole's module dir or argv target resolves inside the workspace being mounted or inside
`paths.GlobalHome()`'s jail-home tree. A path check replaces a digest, a lockfile field and a
re-prompt loop, and it makes user-scope install a sound boundary rather than a holed one.

**Limit:** the rule cannot be complete — yolo knows the workspace it is launching and the homes it
manages, not that `~/code/other-project` is agent-writable in some other jail. It catches the shape
that occurs (a daemon in the repo being worked on); the permission argument covers the rest.

**Two limits to state rather than discover.** (1) The digest covers what yolo can NAME, not
everything that will run: a Python daemon imports, a binary `dlopen`s. It is a tripwire against
silent substitution, not a boundary — and `state_files`/`{state}` content is deliberately outside it,
or every CA regeneration would re-prompt. (2) The development-friction question is moot now that there is
no re-confirmation loop — and the escape hatch draft 3 wanted was separately **RULED AGAINST:
develop the loophole in a jail instead.** Measured: `runtime.go:143` is the ONLY
jail-aware branch in the loophole runtime (it skips device passthrough), so a nested jail spawns host
daemons, binds mounts, publishes endpoints and installs intercepts identically. The nested jail IS
the development environment, its "host" is a container that can be thrown away, and re-confirmation
friction then only applies where it should. An escape hatch would have been a hole in the one gate
that reads content, serving a workflow that already had a better answer.

**Recorded as OQ-LP13, and it reorders the argument.** §4.2 justifies the kind partly on *"the claim
enumeration is now total, so nothing crosses without a string a user saw"* — true of the string and
false of the bytes. G1 stays first in the landing order (it is still the largest reduction in *who
may declare*), but it must stop being described as closing the hole.

### 4.3b THE SCOPE MODEL, RULED: install is user-scope, enable is either

> **Maintainer ruling:** *"loopholes can only be installed at the user level and enabled at the
> workspace level or user level."*

This is the organizing decision for §4 and it resolves or dissolves four open questions. Two verbs:

| | **INSTALL** | **ENABLE** |
|---|---|---|
| Decides | that this code may run on this machine at all | whether an installed loophole is active for this jail |
| Scope | **user only** | **user or workspace** |
| Performable by | a human editing an agent-unwritable file | anyone who can edit `yolo-jail.jsonc`, including an agent |
| Gate | one confirmation, every origin — WHAT it checks is OQ-LP13, still open (§4.3a) | none required |

**The line is drawn where the risk is.** The hazard was never *"a daemon runs"* — it is *"code
nobody vetted runs"*. Install is the vetting point; enable is routing **within an already-vetted
set**, so it is safe in an agent-writable file: the worst an agent achieves is switching on something
a human already read and confirmed.

**Key-by-key, since the `loopholes` block's two shapes cut across the verbs**
(`validate_loopholes.go:52-68`):

| Key | Verb | Scope | Why |
|---|---|---|---|
| `command` (inline) | install | **user** | it *is* the host execution |
| `doctor_cmd` | install | **user** | a second host execution, from two read-only-looking commands (§5.1 sites 5 and 7) |
| `env` — **either shape** | install | **user** | changes WHAT runs, not whether: the override shape reaches a first-party daemon's spawn env (`LD_PRELOAD` into the broker, §4.1 finding 3) |
| `enabled` | enable | **either** | changes WHETHER already-installed content is active |
| `jail_env` | — | **either** | container-side only; no host effect |

**This OVERRIDES G1's `enabled` restriction (§4.3), and the consequence must be carried.** G1 argued
`enabled` be user-only in both directions, because ON admits a manifest daemon to the spawn and OFF
silently drops the broker. Under the ruling ON is bounded by install-time vetting — but **OFF is no
longer protected by scope at all**. So the two disclosure requirements G1 listed stop being
belt-and-braces and become the sole protection: a launch-time line naming the loophole *and the file
that disabled it*, and `yolo check` **warning** rather than `ok`ing a workspace-sourced disable
(`check/sections_loopholes.go:34`).

**Install must become an explicit act for every origin.** Today only `yolo pack install` on a FETCHED
pack prompts; `file://`, the conventional local pack, the user loopholes dir and the config block are
all silent. Under §1.1's consolidation those collapse toward one act, and that act is where §4.3a's
digest is taken and recorded.

**What it dissolves:** **OQ-LP12** (per-workspace) — you install once, each workspace enables from
the vetted set, and the request/grant machinery draft 3 proposed is withdrawn as more than the
problem needs. **OQ-LP3** (`file://` trusted forever) — install confirms every origin, so there is no
trusted-origin bypass to special-case. **OQ-LP8** is likely subsumed too (a commit is a coarse
digest). **OQ-LP2** narrows to the install-shaped rows of the table above.

**Residual, stated rather than discovered:** a workspace can enable ANY installed loophole, not only
the one that repo was installed for. Bounded by install-time vetting; visible via `notePackHostAccess`
(§4.3 G4). If per-loophole "user-enable-only" is ever wanted, that is an additive flag, not a
redesign.

### 4.4 Things to name honestly

1. **`allow_exec` is not this gate and would not even fire.** It gates staging a file with an execute
   bit (`packstage.go:149-156`), is per-pack rather than per-file, and is origin-blind. A daemon
   shipped as `python3 script.py` needs no exec bit at all. It *is* one step short of host execution
   in a different way — `apply --host` delivers an executable staged file at `0o555`
   (`entrypoint/hostfilestree.go:192-201`), which is the live matt-fzf case: a pack-owned script the
   **host's** Claude Code executes. yolo does not exec it; the pack causes host-side code to exist
   where host software runs it.
2. **`file://` is trusted unconditionally, and forever — and "not changing it" is WITHDRAWN
   (§4.3a).** `OriginLocal` is nothing but a `file://` prefix (`config/packs.go:126-128`) and
   `MayGrantHostFiles()` returns true with no approval and no re-approval. Draft 2 kept it on the
   grounds that *a directory the user controls carries the user's own authority*. Review showed the
   premise fails exactly where it matters: the check constrains the path in no way, so a directory an
   **agent** writes is equally "local". Superseded by OQ-LP13's content anchoring, which covers it
   with no special case.
3. **`yes | yolo pack install` grants approval.** `promptYesNo` (`pack.go:1147`) fails closed on a nil
   stdin or EOF, but the call site always passes `os.Stdin` with **no TTY check**. A one-line
   hardening, independent of this design, worth doing in the same batch — and more worth it once a
   `y` means "run this code" rather than "read this file".
4. **`apply --host` silently drops every fetched pack today.** `packForCheckDeps`
   (`checkdeps.go:135-137`) returns nil for anything not embedded and not `file://`, and the printed
   reason blames offline resolution. So the G3 gate is untested at that command. Pre-existing.
5. **Two backends make the whole kind a silent no-op** — see §8 item 2, which draft 1 scoped far too
   narrowly.

### 4.5 Nothing reaps a departed loophole's state — and the mechanism draft 1 cited does not exist

Measured: `rg -c 'loophole' internal/prune/*.go` returns **zero**. So nothing prunes per-loophole
state dirs (`~/.local/share/yolo-jail/state/<name>/`), `host-service-<name>.log` under
`GlobalStorage()/logs`, or the materialized embed cache. For a hand-placed loophole that is untidy.
For a pack-shipped **intercepting** loophole it is a CA private key left behind by a pack the user
deselected.

Draft 1 said this should work *"the way `files` retires its host output"*. **Review showed there is
no path from that precedent to here**, three times over:

1. `files`' host output is retired by `pruneDroppedPackOutput` (`cli/applyhostprune.go:57-73`),
   called **only** from `apply --host` (`apply.go:171`, `:457`) — the command §3.4 refuses this kind
   at. That command never sees a loophole contribution.
2. `yolo prune` sweeps the **host-render archive** (`prune/hostarchive.go`), a different tree from
   `paths.GlobalStorage()/state/<name>`.
3. `StateDirFor` is keyed by loophole **NAME only** (`loopholes.go:113-116`) — which is exactly the
   property §8 relies on to make a pack-shipped CA possible (name-keyed ⇒ outside the staged tree ⇒
   survives restaging). So **nothing on disk records which pack owned a state dir**, and §4.5's
   requirement and §8's benefit are in direct tension. Draft 1 presented both without noticing.

**Requirement: name the three missing artifacts.** A pack→loophole-state **ownership record** written
at staging (the `files` ownership record is the model); a **detector on the launch path**, where
deselection is actually observed (`stagePacks`' prune); and a **`prune` sweeper for the state tree**.
Archived under the state dir rather than deleted, for the same reason it is archived there: the state
may be the only copy of something the user wants back.

**Process teardown, scoped down deliberately.** Review noted that "do not select the pack" is not
revocation: `loopholesruntime.go:388` sets `Setsid: true`, and teardown (`:456-467`) signals
`cmd.Process` alone, so anything the daemon forked survives deselection, the lockfile entry, and
`yolo loopholes list` knowing the name. **Accepted in part: kill the process GROUP on teardown** —
cheap, correct, and it fixes the same leak for today's config loopholes. **Rejected: recording
spawned PIDs in the state dir so a later `prune` can reap them.** That builds a process supervisor
for a threat the finding itself calls marginal (once arbitrary host execution has happened once,
persistence is available through `~/.bashrc` or cron), and a stale PID file is its own class of bug.
**Instead, state it plainly in §5.3:** selection controls **activation**; a daemon that has run once
is outside yolo's ability to revoke, and no packaging design changes that.

---

## 5. Selection and defaults

### 5.1 Selection gates discovery — and the census is seven surfaces, not four

**The MOUNT is the filter for packs** (`AGENTS.md`) — the entrypoint renders whatever is staged. That
does not help here: a pack-shipped loophole is read **host-side, before the container exists**. So
selection has to be enforced inside discovery.

**Draft 1 said "all four `Discover` call sites are pre-launch". Measured: six `Discover` callers plus
a seventh independent walker**, and the three it missed are the ones that answer *"what loopholes do
I have"* and the one that decides config validity:

| # | Site | What it backs | Must it see pack loopholes? |
|---|---|---|---|
| 1 | `cli/run/prepare.go:61` | the briefing | **yes** |
| 2 | `cli/run/assemble_parts.go:423` | `brokerLoopholeActive` | **yes** |
| 3 | `cli/run/assemble_parts.go:566` | container argv | **yes** |
| 4 | `cli/run/loopholesruntime.go:113` | **the host daemon spawn** | **yes** |
| 5 | `loopholes/loopholescmd.go:77` | `yolo loopholes list` / `status` | **yes** — and `status` runs `doctor_cmd` |
| 6 | `loopholes/resolver.go:30` | `config.LoopholeResolver.Known()` → `validateLoopholes` | **yes** — see §5.2 |
| 7 | `loopholes/ValidateLoopholes` (`discover.go:232`) via `check/sections_loopholes.go:23`, `:174` | `yolo check` | **yes** — and it runs `doctor_cmd` |

**Sites 5 and 7 execute host code** (`RunDoctorChecks` at `loopholescmd.go:138` and
`sections_loopholes.go:47`), and neither has pack resolution, a lockfile, or `packMayAccessHost`
anywhere in reach. That produces a fork the design must not leave open:

> either (a) pack module dirs are plumbed in and `yolo check` / `yolo loopholes status` — two
> commands users and AGENTS.md treat as **read-only preflight** — run an unapproved fetched pack's
> `doctor_cmd` on the host with the gate nowhere in the call graph; or (b) they are not, and three
> of this doc's claims are unimplementable at the sites it cites (§5.1's "visible without new
> plumbing", §5.2's toggle, and pack-capabilities' "`loopholes list` must distinguish superseded
> from not shipped").

**Requirement: the pack-aware, lock-gated loophole set is ONE constructed value, produced once on
the host and passed to every consumer** — not seven independent `DiscoverOptions` assemblies. Assert
the convergence in a test. Until it exists, `RunDoctorChecks` must take only loopholes whose origin
gate has been evaluated. (Sites 6 and 7 also run **in-jail**, where the staged root is `/ctx/packs`,
so their wiring is not the same as the run path's.)

**The seam itself is still small and idiomatic.** `DiscoverOptions` (`discover.go:172-178`) already
carries a `Root` override, and `loadFromDir(dir, source)` already iterates child dirs each holding a
`manifest.jsonc`. The caller passes pack-contributed module dirs in, and `internal/loopholes` never
learns what a pack is. A fourth `Source` label — `SourcePack`, beside `SourceBundled|User|Config` —
slots in and is what `yolo loopholes list` prints (`loopholescmd.go:117`). Sequencing is already
right: `stagePacks` runs at `run.go:158`, well before `assembleRunCmd` (`:486`) and `startLoopholes`
(`:516`).

**Precedence — draft 1's line is DELETED.** It said `bundled < pack < user < config-override`, with
pack-vs-bundled collisions fatal. Those two sentences contradict each other, and under a
warn-and-last-wins implementation the precedence line wins: a pack loophole named
`claude-oauth-broker` would **replace** the bundled record, `assemble_parts.go:427-428` would then
evaluate the PACK's `Active()` to decide the terminator/CA/endpoint wiring while
`loopholesruntime.go:156-159` still special-cases the NAME and runs yolo's own broker argv — half
the broker from one manifest, no message. **Corrected: pack-vs-reserved is refused in the pack-side
pre-flight (§3.1) and therefore never reaches an ordering.** What remains is `user` overriding
`pack`, and `config-override` on top, exactly as today.

**One shipped bug to fix while here:** the briefing path (`prepare.go:61-66`) filters on `Enabled`
only, not `Active()`. So an enabled-but-inactive loophole is advertised to the agent as a live
capability. Pre-existing and orthogonal, but a pack-shipped loophole makes it more visible.

### 5.2 `yolo loopholes enable/disable` and the pack-shipped case

**Correction to draft 1's premise.** It claimed the toggle *"would appear to work and silently
evaporate on the next launch"* because `SetEnabled` (`runtime.go:261-285`) read-modify-writes the
manifest inside the staged tree. Measured: `CmdSetEnabled` (`loopholescmd.go:165-172`) never reaches
`SetEnabled` for a non-user-dir loophole — it stats `UserLoopholesDir()/<name>/manifest.jsonc` and
exits 1 with *"For bundled or workspace-inline loopholes, edit the workspace yolo-jail.jsonc"*. So
today it **refuses outright**; the failure is a wrong instruction, not a silent evaporation. (And
that instruction now points at the **weaker** scope, which G1 changes.)

**Decision stands, with a new prerequisite: for a pack-shipped loophole the toggle writes
`loopholes.<name>.enabled` in the USER config**, which `applyWorkspaceOverrides` already honors
(`discover.go:93-95`), and the CLI message is updated to say "user config" rather than "workspace".

**The prerequisite review found:** that config entry is validated through
`config.LoopholeResolver.Known()` (`resolver.go:22-35`), which is **census site 6** and today sees
bundled + user dir only. An unknown name takes the override-shaped fallback
(`validate_loopholes.go:56-65`) and emits *"no loophole named 'x' is installed on this machine —
treating the entry as an override … If the loophole was removed, this entry is a no-op"*, printed at
**every launch** (`preflight.go:46-48`) — the same sentence a user gets when a pack genuinely failed
to stage. So the toggle would be self-warning. Either the resolver joins the converged set (§5.1), or
a pack loophole's disabled state is recorded somewhere the validator already understands.

Unifying *all four* sources on config-side enabled state would also delete `SetEnabled`'s
comment-destroying RMW and is the better end state — but it changes behaviour for bundled and
user-dir loopholes, so it is a separate decision.

### 5.3 Defaults: a pack-shipped loophole is ALWAYS opt-in

**"Nothing is active by default"** (`AGENTS.md`) is the pack system's headline property. A default-on
*third-party* pack would mean yolo selecting code the user did not ask for — which for this kind
means running a daemon on their machine. So:

> **The only default-on loopholes are bundled ones. A pack-shipped loophole activates only by
> selecting its pack.**

That is why `Implicit: true` is **not** the mechanism reached for here. The conventional local pack
is `Implicit: true` (`config/packs.go:275`) and that is precedent for a default-on pack — but it is a
pack the user *is* by definition: their own `~/.config/yolo-jail/local/`, with `MayGrantHostFiles()`
true *"with no special case… there is no third party at all"* (`packs.go:253-256`). Generalizing from
it to a distributable pack would drop the one fact that makes it safe.

**And selection controls ACTIVATION, not REVOCATION.** Deselecting a pack stops the next launch from
starting its daemon. It does not stop a daemon that already ran: the spawn is `Setsid`, teardown
signals one PID (§4.5), and a process that has executed once can persist by means yolo has no view
of. This design does not claim otherwise, and no packaging design could.

### 5.4 So how does the broker stay on by default?

**It stays bundled — but REVIEW CHALLENGED THE CHANNEL ITSELF, and the challenge lands.** Draft 2
argued `bundled_loopholes/` is *"the channel for the things yolo itself is accountable for"*, citing
the `_official/` versus top-level split pack staging already has (`run/packs.go:39`). The maintainer's
objection: *"why not a real pack? it can still come from a built in shipped namespace or whatever."*

**That is the same move this repo already made for agents, and AGENTS.md's headline sentence is the
precedent against draft 2's argument, not for it:** *"AGENTS ARE PACKS. Core does not know what an
agent is. There is no agent registry, no `agents` config key."* The six shipped agents are ordinary
packs that happen to live in the binary. `bundled_loopholes/` **is** a registry, and accountability
is a property of *who wrote it* — which an official pack already carries, since it is embedded in
the same binary.

So of the three reasons below, **only the second survives as an argument about the channel**; the
other two are facts about the specific loopholes. Recorded as **OQ-LP11**, and note the ordering
consequence: this kind is the prerequisite for that consolidation, so nothing here needs to change
to keep the door open.

Three reasons draft 2 gave, re-graded:

1. **Bundled + `requires` already IS the default-on mechanism** — ⚠️ **partly.**
   `claude-oauth-broker` declares `requires: {command_on_path: "claude"}` and `Active()` is
   `Enabled && RequirementsMet()` (`loopholes.go:232`), so a user with Claude Code installed gets
   refresh serialization without knowing they need it. Make it opt-in and anyone who does not select
   it silently gets the single-use-refresh-token race the broker exists to prevent. **The
   requirement is real; the channel is not the only way to meet it.** An *official* pack could carry
   an implicit-selection bit — the mechanism exists today, since the conventional local pack is
   `Implicit: true` (`config/packs.go:275`). What §5.3 rules out is a **third-party** pack being
   default-on, which is a different sentence.
2. **A pack could not express what the broker needs** — ✅ **this is the one that survives.** Its
   `host_daemon.cmd` is **not what runs**: `startLoopholes` special-cases the name
   (`loopholesruntime.go:156-159`) and reconstructs the argv in Go via `broker.BrokerSpawnArgv`. And
   its per-jail relay, the only loopback-TLS hop a jail actually dials for it, has **no manifest
   vocabulary at all** (`ensureBrokerRelay` at `:498`). Packaging it would be ceremony over a thing
   that ignores the package — so it is blocked on giving those two a manifest, not on the channel.
3. **`host-processes` cannot move either** — ❌ **this one does not hold.** Its client is
   `cmd/yolo-ps`, a baked image binary, which rules out a **fetched** pack. An official pack is
   embedded in the same binary, so a baked client is no obstacle at all.

**But "bundled" alone does NOT make the default safe** — review's third refutation, and it is the one
that changes work. A workspace `yolo-jail.jsonc` can set
`loopholes.claude-oauth-broker.enabled: false` and the broker vanishes with no message and a green
`yolo check`. G1's `enabled` scoping and the disclosure requirement (§4.3) are what actually keep the
default; "it stays bundled" only keeps it from being *deselected*, which was never the threat from an
agent-editable file.

**Consequence, and it is the honest one: supersession does not die.** It survives for exactly the
bundled set — which is `pack-capabilities.md` §10's own predicted residue. §6 says how much smaller
that makes it.

---

## 6. What this does to `pack-capabilities.md`

Draft 1 left a survives/dies table and a note that §§1–9 had *"NOT yet been rewritten."* **They have
now been**, per the maintainer's instruction that this doc is the prerequisite and that one assumes
the other. The table below is the record of what was cut and why; the live document is the authority.

| § (draft) | Verdict | Where it went |
|---|---|---|
| **1** the concept | **Survives, scope narrowed** to auto-activating **bundled** loopholes | §1 |
| **2** the two verbs | **Survives**, compressed | §2 |
| **2.1** supersede is not provide | **Survives**, compressed to the test table | §2 |
| **2.2** "why a pack cannot `serve`" | **ARGUMENT DELETED.** Its premise — *"none of the 14 kinds is a daemon"* — is what the 15th kind falsifies. The conclusion survives for a different reason and is one paragraph now | §2, closing note |
| **3** why a capability, not the name | **Survives unchanged** — the strongest section | §3 |
| **4** the rule | **Survives, narrowed**: `Superseded()` is only reachable for loopholes selection cannot remove. Line reference corrected (`loopholes.go:232`, not `:219`) | §4 |
| **5** the namespace inverts the skills rule | **CUT to two sentences.** With three first-party manifests the "someone will reach for the skills rule" hazard is hypothetical | §4, note |
| **6.1** typo refused at load | **Survives** | §5 |
| **6.2** over-broad claim | **CUT.** `because` is already mandatory in §2 and printed in §5; the failure mode needed no section of its own | — |
| **6.3** name who turned it off | **Survives, and grew a second author** | §5 |
| **6.4** two packs disagree; no `needs` | **CUT to one line** in the not-built table. At three bundled loopholes the conflict is unreachable | §6 |
| **7** deliberately not built | **First row DELETED** (`serves` on a pack is expressible now, and lives on the loophole manifest); other two survive | §6 |
| **8** the first-party instance | **Survives as written** | §7 |
| **9** acceptance | **1–6 survive; 1b re-argued** — the message changes from *"a pack has nothing to serve with"* to *"put it on your loophole's manifest"*, a fix rather than a wall | §8 |
| **10** OQ-CAP2 | **Closed with (B)**, compressed from ~75 lines to a short resolution pointing here | §9 |
| **11** OQ-CAP | **Survives, and top-level is now clearly right** | §10 |

**Net effect:** A6 shrinks from a capability system to a capability system *for three bundled
loopholes*. Whether that is still worth the machinery is **OQ-LP6** — but note the extension-point
argument cuts both ways: a loophole manifest is still a public surface, so `serves` is a field third
parties will write even if only bundled loopholes are ever superseded.

---

## 7. Migration — the three bundled loopholes

**Nothing migrates.** Bundled stays bundled; the kind exists for the packs nobody has written yet.
That is what makes it an extension point rather than a refactor.

| Loophole | Verdict | Why |
|---|---|---|
| `claude-oauth-broker` | **stays bundled** | Auto-activates by design (§5.4); its host singleton bypasses its own `host_daemon.cmd`; its per-jail relay has no manifest vocabulary at all. |
| `host-processes` | **stays bundled** | Its client is `cmd/yolo-ps`, a baked image binary. A pack cannot ship it. |
| `audio` | **stays bundled — and becomes the worked example** | `transport: none`, no daemon. It is the one bundled loophole a pack could carry with zero new vocabulary and zero host execution. |

**`audio` is the dogfood, as an example rather than a migration.** Ship a copy under
`docs/examples/` as a pack. **But draft 1 described it wrongly in three ways, and the corrections
matter more than the example does.**

1. **It is NOT the thing "that needs no approval to run".** That sentence was written when bind
   mounts and devices emitted no claims (§3.3). Under the corrected enumeration the audio example
   emits **four** review-worthy claims — three socket/dir binds and `/dev/snd` — and a fetched copy
   of it would prompt. That is the whole point: the example now exercises the approval path too,
   which is strictly better dogfood than draft 1's version.
2. **The `:ro` "cost, measured" paragraph is WITHDRAWN — the measurement says the opposite.** Draft 1
   claimed the example *"must accept `:ro` on the two audio sockets, which `audio` itself sets
   `readonly: false` on because audio frames flow both ways … it is the cost, measured."* Measured
   (§3.1): a `:ro` bind-mounted AF_UNIX socket is **fully connectable and bidirectional**, so the
   audio example would work unchanged. **The honest measured cost of the audio example is the
   `jail_env` conditionality alone** — `PULSE_SERVER`/`PIPEWIRE_REMOTE` must route through the `env`
   kind and become unconditional (§3.1).

   *A review finding is REJECTED here, and it is worth recording why:* one lens argued the opposite —
   that *"connecting to an AF_UNIX socket needs write access, so the example ships a loophole that
   … passes no audio."* That is a reasonable inference and it is false; the two lenses contradict
   each other, and the measurement decides it. The `:ro` refusal is a no-op for sockets in **both**
   directions: it neither protects (§3.1) nor breaks (here).
3. **The `--device` half is unobservable in the repo's own mandated verification environment.**
   `runtime.go:139-142` skips device passthrough whenever the launcher is itself in a jail
   (*"devices cannot nest under rootless podman"*), and nested-jail verification is the mandated loop
   for `cmd/`/`internal/` changes (AGENTS.md). So the example proves discovery, selection, the
   footprint claims, the approval prompt, the `:ro` binds and teardown **in a nested jail**, and the
   `--device` claim only on a non-jail host. Say that rather than claiming end-to-end proof.

---

## 8. Interception survives — and here is the (corrected) proof

`tls-intercept` retired as a *transport*, but `intercepts` is still parsed, still drives
`--add-host`, and the Apple Container skip now keys on the list rather than a transport name.
Interception is **orthogonal to transport**, and it stays that way.

`intercepts` has exactly one behavioural reader plus two emissions, none of which keys on transport,
source, or origin:

| Site | What it does |
|---|---|
| `internal/loopholes/runtime.go:58` | `if runtime == "container" && len(m.Intercepts) > 0 { continue }` — the AC **container-args** skip |
| `runtime.go:63-65` | `--add-host <intercept.host>:<m.BrokerIP>` per intercept |
| `runtime.go:101-121`, `:162-164` | the `ca_cert` path, joined into `-e NODE_EXTRA_CA_CERTS` |

So a pack-shipped loophole declaring `intercepts` needs **no new mechanism**. Two supporting facts,
one of which draft 1 got wrong:

- **`{loophole_dir}` — draft 1 asserted "That works." It does not (§2.1a).** It resolves in exactly
  one field. The claim that a staged module dir is *strictly better* than a hand-placed one stands
  (the staged tree already passed `packstage`'s exec-bit and escaping-symlink refusals); the claim
  that the placeholder works does not.
- **`{state}`** (`load.go:105-106`) resolves to `StateDirFor(name)` under
  `~/.local/share/yolo-jail/state/<name>/`. **Name-keyed, so it is outside the staged tree** and
  survives restaging — what makes a pack-shipped CA possible at all, since a CA regenerated on every
  launch would break every long-lived TLS client in the jail. **And it is exactly why §4.5 has no
  mechanism:** name-keyed means unattributed, so nothing records which pack owned the dir holding a
  private key after that pack is gone. The benefit and the gap are the same property.

Two things the design must state rather than leave to discovery:

1. **An intercept is its own approvable claim** (§3.3, §4.3), separate from the daemon claim, because
   a `transport: none` loophole with `intercepts` runs no host code and still installs a CA trusted
   by every TLS client in the jail.
2. **Two backends make a pack-shipped loophole inert, and draft 1 scoped this to one narrow slice of
   one of them.** It cited `runtime.go:58` and asked for a by-name report only for *"a pack whose
   only contribution is an intercepting loophole"*. Measured, it is much broader:
   - **Apple Container:** `startLoopholes` returns nil for `rt == "container"` before any external
     service starts (`loopholesruntime.go:96-99`). **Every** pack-shipped host daemon is skipped
     there, intercepting or not — a different skip from the one draft 1 cited.
   - **macos-user:** the branch returns at `run.go:114-134`, and `startLoopholes` is at `run.go:516`,
     so the kind is inert on that backend **entirely**.
     [`macos-user-nix-and-features.md`](macos-user-nix-and-features.md):262 already states it. Draft
     1 never mentioned macos-user, deferring to OQ-LP7's future `guest` notch — but macos-user ships
     today.

   **Requirement:** on `container` **and** `macos-user`, a selected pack's loophole contribution
   prints one line saying it is inert on this backend. That is the **B-0 rule** applied to the new
   kind — `run.go:90-104` records B-0 as *"a backend that looked provisioned and configured
   nothing"* and restructured the pipeline to end it. **One mechanism with §3.1's platform-support
   declaration**, which is the same situation on the other axis: platform (`darwin` vs `linux`) and
   backend (`container`, `macos-user`) both answer *this loophole does nothing here, and here is
   why*, and two half-messages for one user-visible situation is how B-0 happened in the first place.

---

## 9. Risks and open questions

### Risks

**R1 — the static-data invariant inverts in spirit while staying true in letter.**
[`pack-system.md`](pack-system.md) §12's first invariant is *"the manifest stays static data — every
claim readable without executing anything."* A declared argv **is** static data, so the sentence
stays literally true. But its spirit was "reading a manifest tells you everything and costs you
nothing", and now: reading the claim is safe, *selecting* it is host execution. **That sentence must
be sharpened in the same commit that adds the kind.** `program` already bends it in-jail; a loophole
breaks it on the host.

**R2 — nothing here has run.** No pack-shipped loophole exists. The two places a first
implementation is most likely to be wrong are still orderings rather than shapes: the **seven-surface
discovery convergence** (§5.1) and the **publish-after-upstream** ordering (§2.1b) — and the second
now has a second failure mode (stale socket) that draft 1's fix did not cover.

**R3 — the front's limits are invisible to the daemon author.** No EOF propagation (§2.1b hazard 2)
and no per-request access log (hazard 3). The first turns a working daemon into a hang; the second is
an audit gap that only shows up when someone asks what a jail requested.

**R4 — `pack footprint`'s review tail conveys nothing for this kind.** `reviewSummary`
(`pack.go:945-960`) counts by kind, so a loophole reads `1 loophole`. For host execution the count is
the least interesting fact. Either it special-cases or the summary changes shape. (And per §3.3, that
command cannot see a fetched pack at all.)

**R5 — doc/code drift found while writing this, all pre-existing.** `docs/guides/loopholes.md`'s
"Manifest schema (v1)" omits `host_daemon`, `jail_daemon`, `host_bind_mounts`, `host_devices` and
`requires` — five loader keys including **every one that causes a host-side effect** — and marks
`description` required when `load.go:65-72` does not. `internal/config/config.go:74` sets
`knownHostServiceKeys = set("command","env","jail_socket")`, so the `description` and `doctor_cmd`
that `discover.go:41`/`:49` read, and that the guide's own example shows, are **unknown-key
validation errors** (§4.3 explains why that mismatch is load-bearing rather than cosmetic); and
`validate_loopholes.go:142-158` prefix-checks `jail_endpoint` while `:114` has already rejected it as
unknown. And `runtime.go:278` still names a `src/loopholes.py` that no longer exists — `SetEnabled`
writes that header into every manifest it toggles.

**R6 — the claim-count grows the prompt, and a long prompt is a skimmed prompt.** With the total
enumeration (§3.3) an audio-shaped pack emits **four** claims (counted from
`bundled_loopholes/audio/manifest.jsonc`: three `host_bind_mounts` — two of them sockets, so the IPC
class — plus `/dev/snd`; no daemon and no intercepts) and a proxy-shaped one emits three or four.
That is honest, and it is also the shape people click through. Nothing here solves it; grouping by
loophole in the display while keeping per-claim strings in the lockfile is the obvious mitigation and
is a display concern, not a model one.

### Open questions

**OQ-LP1 — where does the loophole manifest schema live?** `packload` cannot import
`internal/loopholes` (cycle, measured §3.2), and the footprint needs to read the daemon argv.
Recommend extracting `internal/loopholedecl` as a stdlib+decoder leaf; the alternative is breaking
the `loopholes` → `config` edge (two files). **Resolved by:** choosing one.

**OQ-LP2 — do the `loopholes` block's host-exec keys become user-scope-only now? RULED: YES**, for the
INSTALL-shaped keys (§4.3b's table: `command`, `doctor_cmd`, `env` in both shapes; `enabled` and
`jail_env` stay at either scope). **And the migration is ruled too: a FATAL error, not warn-then-error**
— a workspace enabling an uninstalled loophole fails the launch, names the file that asked, and
OFFERS to install it at user level, which is where the human-in-the-loop moment now lives. That also
replaces today's every-launch "treating the entry as an override" warning, which neither works nor
stops. The offer must require a TTY and fail closed without one, or a workspace could drive its own
promotion. Draft note: **yes**,
independent of this kind, and it is the largest single risk reduction here (§4.3 G1). Note it is
larger than draft 1 said: it covers `command`, `env` (both shapes), `doctor_cmd`, and `enabled` for
daemon-bearing loopholes, and it needs the migration in §4.3 because the shipped guide teaches the
workspace scope. **Resolved by:** a maintainer ruling.

**OQ-LP3 — `file://` packs run host daemons with no prompt, ever.** Local origin is a bare `file://`
prefix with no path constraint, trusted unconditionally and permanently. Draft 2's read was
**leave it**; **withdrawn** (§4.3a) — the premise is that the path is one the user controls, and
nothing checks that, so a live-mounted workspace qualifies. **Mostly subsumed by OQ-LP13**: content
anchoring covers this row without the special case draft 2 was arguing against. What survives
independently is only whether `IsLocal()` should additionally constrain the path. **Resolved by:**
OQ-LP13, plus a ruling on the path constraint. Either way it must be *documented*.

**OQ-LP4 — the front's declaration: `publishes` on `host_daemon`, or a `yolo internal front`
subcommand named in the manifest's own argv?** I recommend `publishes` (§2.1); a manifest naming
`yolo` in its argv is the pack knowing about yolo's CLI, which is the workaround-becomes-API failure.
**Not really open** — recorded because the subcommand is the tempting shortcut and it is a one-way
door.

**OQ-LP5 — does `jail_env` stay refused for pack-shipped loopholes?** §3.1 refuses it to avoid a
cross-kind collision pass, at the cost of conditional env — a cost now visible in the audio example
(§7), which is the *only* remaining cost of that example. The alternative is that pass, which is
purely additive. **Resolved by:** the first real pack that wants conditional env.

**OQ-LP6 — is A6 still worth building for three bundled loopholes? RULED: YES, build it.** The
extension-point argument carries it — a loophole manifest is a public surface regardless, so `serves`
is a field third parties will write, and designing it once now beats retrofitting it later. With selection as the mechanism,
`pack-capabilities.md` applies only to the bundled set (§6). **Resolved by:** a maintainer ruling.

**OQ-LP7 — the `guest` notch.** A loophole is coherent at `guest` and incoherent at `host`, but
`Target.Fields()` funnels both into `HostFields()`. This kind is the first case where that funnel is
wrong for a *reason* — and note §8's finding that macos-user is inert **today**, which makes the
question less hypothetical than draft 1 treated it. **Resolved by:** Phase 7 stating the guest census.

**OQ-LP8 — how does an execution approval survive a moving pin without re-prompting forever?** §4.3
G2b anchors an exec-bearing approval to the commit, giving `ApprovedAt` its first reader. The cost is
that a `?ref=main` pack re-prompts on every commit. Alternatives: fold a **digest of the loophole
module dir** into the claim string (re-prompts only when the daemon's own files change — more
precise, more machinery), or document tag-pinning as the supported shape for exec-bearing packs. My
read: **commit anchoring now, digest later if the friction is real** — the friction is visible and
recoverable, and content-blind approval is neither. **Resolved by:** a maintainer ruling.

**OQ-LP9 — NESTED JAILS NEED THE SCOPE MODEL TO RECURSE. The only properly open question, and review
grew it.** The original form was small (does the scope error downgrade in-jail, the way `agents`
does). Reframed: *"for jail in jail, the outer jail is essentially 'user level' for the inner jail —
we need to support this somehow."* That is §4.3b applied recursively, and it is right: "user level"
is whatever scope owns the machine the daemon runs on, so inside jail A that is jail A's own config,
owned by jail A's agent, because jail A is the blast radius. It is also **load-bearing**, since
§4.3a's escape-hatch ruling sends loophole development into a nested jail.

**Measured in-jail 2026-08-13, and today it does not work:**
`~/.config/yolo-jail/config.jsonc` is a **read-only** bind (`/proc/mounts`), so an agent cannot
install by editing it; but the containing directory IS writable and the config declares
`include_if_found: ["overrides.jsonc"]`. So a seam plausibly exists and is undesigned. **One fact to
verify before anyone leans on it:** where that directory resolves host-side. It must be the jail's
own home tree — if it were ever the human's real `~/.config/yolo-jail/`, an agent writing
`overrides.jsonc` would be editing the host's config, which is a worse bug than the one this asks
about. **Resolved by:** a maintainer ruling on supporting it, after that check.

**OQ-LP13 — what stops an agent swapping the file a loophole runs? RULED: a placement rule, not
hashing.** Raised in review (§4.3a): every gate governs a declaration, the file that executes is
unread, and `file://` imposes no path constraint. Draft 4 proposed content digests; the maintainer
ruled against — *"if you can edit user-level files, you have all the perms already"* — so the
user-scope edit IS the confirmation, and what survives is: **installed content may not resolve inside
the mounted workspace or a jail-home tree**, refused at install by name. Cheaper than a digest and it
closes the actor gap the permission argument leaves open. Incomplete by construction (§4.3a), and
that is stated rather than hidden. **Also subsumes OQ-LP3 and all but one row of OQ-LP8.**

**OQ-LP12 — how does a workspace get a loophole another workspace does not?** Raised in review
(§4.3 G1). G1 removes the only mechanism and packs were never per-workspace, so this is a capability
removal with no replacement unless one is designed. **(a)** user-scope declaration selecting
workspace paths, or **(b)** the workspace asks and the human approves host-side, keyed by (workspace,
claim set). **My read: (b)** — it is `yolo pack install`'s existing approval with a different
requester, and `three-decisions.md`'s deletion of `pack_requests` does not cover it (a repo can
already lay out its own files; it cannot already run host code). **Resolved by:** a maintainer
ruling, alongside OQ-LP2 — G1's warn-then-error migration needs somewhere to point people.

**OQ-LP10 — retire the USER LOOPHOLE DIRECTORY once a pack can carry one? RULED: YES**, after the
kind ships so there is somewhere to go. Raised in review: *"what's
the argument for keeping this? we can easily have local packs."* There is no good one. §3.1 justifies
the directory's last-wins overwrite as *"a hand-placed directory carries the user's own authority —
the same reason a `file://` pack does"* — i.e. **the same sentence justifies both mechanisms**. And
it is the only channel that activates a host daemon with **no selection step at all**: `loadFromDir`
walks it and every manifest found is discovered, which contradicts §5.3's *"nothing is active by
default"* in the one place it matters most.

Two payoffs beyond the deletion. **It resolves §5.2 for free:** `CmdSetEnabled` (`loopholescmd.go:165-172`)
works *only* for user-dir loopholes and refuses every other source, so retiring the directory leaves
that command with no special case and forces enable/disable state into config for all sources —
which §5.2 already calls the better end state and defers as a separate decision. And it removes one
of the four sources `Discover` merges, shrinking §5.1's convergence problem rather than growing it.

**Cost, corrected — review asked "isn't this de facto the local pack now shipping a loophole?" and
the answer is yes**, which makes the migration cheaper than draft 3 priced it: the conventional local
pack is `Implicit: true` (`config/packs.go:275`), so a loophole moved into it is discovered with no
config edit and the drop-a-directory-in ergonomics survive. **But it inherits implicit selection**,
so retirement does NOT fix the no-selection-step objection above — what improves is that a
pack-shipped loophole emits claims and reaches `notePackHostAccess`, where the home directory prints
nothing. Visibility, not gating; say so rather than overclaiming.

**OQ-LP11 — do BUNDLED loopholes become official packs? RULED: YES, and `audio` ships IN this
batch** rather than after it — §7's example is promoted from a doc artifact to a deliverable, which
costs a relabel and buys the consolidation immediately. The broker still waits on its stated blocker. Raised in review: *"why not a real pack? it
can still come from a built in shipped namespace or whatever."* §5.4 re-grades draft 2's three
reasons and only one survives — the broker's manifest is not what runs, so packaging it is ceremony
over a thing that ignores the package. The `host-processes` reason was simply wrong (an official pack
is embedded in the same binary), and the auto-activation reason is a *requirement* that an implicit
official pack could also meet.

The prize is that `AGENTS.md`'s *"AGENTS ARE PACKS. Core does not know what an agent is"* becomes
true of loopholes too: one channel, one loader, no registry, and `internal/loopholes` stops being a
thing core knows about specially. **My read: yes in principle, and not yet** — do `audio` as an
official pack first (§7 already builds it as an example, so this is mostly a relabel), and leave the
broker where it is until `BrokerSpawnArgv` and `ensureBrokerRelay` have manifest vocabulary. That
sequencing also gives the "official pack that is implicitly selected" mechanism a low-stakes first
consumer. **Resolved by:** a maintainer ruling; nothing in this design blocks either answer.

---

## What must land together

Ordered, because the first three make the rest safe to read. Items **0**, **5b** and **5c** are new
in revision 2 and are real work draft 1 priced at zero.

0. **Tolerate an unknown KIND under `TolerateSkew()`** (§3.3a), with a regression test that a
   manifest carrying one still boots a jail. **Before the kind exists**, or every pack that declares
   it bricks a jail running a pre-`just load` image. This is the `tier` incident's third appearance.
1. **The `loopholes` block's host-exec surface goes user-scope-only** (§4.3 G1) — over entry
   *shapes*, including `doctor_cmd` and `enabled` for daemon-bearing loopholes, with the warn-then-
   error migration and the `docs/guides/loopholes.md:88` fix in the same commit. Fix
   `knownHostServiceKeys` first (§4.3). Independent of everything else and the biggest reduction in
   **who may declare** host execution. **Ship first** — but it closes half a hole, not the hole
   (§4.3a), and the migration needs OQ-LP12 decided so the warning has somewhere to point.
   - **1a. Content-anchored confirmation for host execution, every origin** (§4.3a, OQ-LP13). Also
     independent of the kind, also pre-existing, and it is what makes item 1 add up to a closed hole
     rather than a narrowed one. Subsumes item 6's commit anchoring.
   - **1b.** Make `loopholesWithConfig` refuse (or drop) `loopholes` entries that fail
     `validateInlineService` — a command that executes what it reads must not read through a path
     that skips validation (§4.1).
2. **The front + `publishes` + both `{loophole_dir}` tokens** (§2.1, §2.1a), the stale-socket unlink
   on both ends (§2.1b), the loud readiness-failure warning and the dead `ProcessState` fix (§2.1c),
   then flip `discover.go:60` **and rewrite `retiredTransportHint` with its pinned test** (§2.2).
3. **The server-side spec** in [`loophole-protocol.md`](loophole-protocol.md) (§2.3), labelled the
   unsupervised path.
4. **`internal/loopholedecl`** (OQ-LP1), because the footprint depends on it.
5. **The `loophole` kind** (§3): the `refusalReasons` entry and the explicit `JailFields()` exclusion
   (§3.4), the **total** claim enumeration (§3.3), the load-time control-character refusal (§3.2),
   the reserved-name refusal, the home-relative bind-mount constraint, the
   **`publishes: "socket"`-only rule for pack-shipped loopholes** (§2.1), and the
   **platform-support declaration** sharing one mechanism and one message with 5d's
   inert-on-backend report (§3.1, §8).
   - **5b.** The **fourth bespoke pre-flight** for loophole-name exclusivity, wired into `stagePacks`
     beside the other three (§3.1). `packload.Collisions` does not do this and is not called at
     launch.
   - **5c.** The **retirement-on-deselect** artifacts (§4.5): a pack→state ownership record at
     staging, a detector on the launch path, and a `prune` sweeper for the state tree. Plus the
     process-**group** kill on teardown.
   - **5d.** The **seven-surface convergence** (§5.1) as one constructed value, with a test; and the
     inert-on-backend report for `container` and `macos-user` (§8).
6. **The approval invariants**: commit-anchored exec claims (§4.3 G2b, giving `ApprovedAt` its first
   reader), the raw-unelided claim-string rule (§4.3 G2a), one merged claim helper called at both
   gates (§3.3), and `notePackHostAccess` extended and moved **before** `startLoopholes` (§4.3 G4).
7. **`pack-system.md` §12's first invariant, sharpened** (R1) — in the same commit as the kind.
8. **`pack-capabilities.md` rewritten per §6** — done 2026-08-13.
9. **The `audio` example pack** (§7), as the end-to-end proof of discovery, selection, the footprint
   claims, **the approval prompt**, the `:ro` binds and teardown — with `--device` observable only
   off a jail host.
