# Loophole packaging — how a pack ships a loophole

**Status:** DESIGN 2026-08-13; **the `loophole` kind LANDED 2026-08-14**, and as of 2026-08-14 **the
whole landing order has landed** — items 0–9, plus OQ-LP9's three parts. **AUDITED AND RESTAMPED
2026-08-23: the design is BUILT and the last channel it was hedging against is GONE** — OQ-LP11
completed 2026-08-19, so every loophole yolo ships is a pack's and `bundled_loopholes/` no longer
exists (postscript below). Closes **OQ-CAP2** with
option **(B)**. Per-section `**Landed**` markers below are the ledger; the landing order at the end of
the doc is the summary. **ONE thing is deliberately NOT built:** the install-gate invariant **G2b**
(§4.3 — content-anchored exec approval, which is a maintainer decision under OQ-LP8 rather than
pending work). **G2a LANDED** — the claim string is the raw, unelided, placeholder-preserving argv,
pinned by two tests in `packload/loopholesource_test.go`. The pack-shipped subset is now **wired at
both seams** (§3.1), `audio` ships as an **official pack** (§7), and **OQ-LP9 is built** (§9).

**Live questions as of 2026-08-23: three** — **OQ-LP5** (conditional `jail_env`), **OQ-LP7** (the
`guest` notch), **OQ-LP8** (an execution approval surviving a moving pin). Everything else is in the
Decision Ledger in §9.

> [!WARNING]
> ### Postscript 2026-08-23 — the channel this doc argues about no longer exists
>
> On **2026-08-19** two commits landed that the body below was written before:
> `7df7c5aa` (*"the daemon says who it is shared across, and the relay stops existing"*) and
> `e391d0f5` (*"the broker moves into packs/claude, and the bundled channel stops existing"*).
> Verified 2026-08-23 against the working tree:
>
> - **`bundled_loopholes/` does not exist** — the directory, its `embed.go`, and
>   `internal/loopholes/embedfallback.go` are all deleted.
> - **`internal/brokerrelay` does not exist.** There is no per-jail relay PROCESS any more: yolo runs
>   the front itself, as a goroutine in the launching `yolo run`
>   (`svcendpoint.ServeFrontWithOptions`, `internal/cli/run/loopholesruntime.go:686`), prepends its
>   own connection preamble, and never parses a daemon's payload.
> - **`loopholes.ReservedLoopholeNames` and `paths.BuiltinLoopholeNames` are both gone** — a reserved
>   name and a pack-shipped name cannot be the same name, and the reservation set was DERIVED from
>   the bundled directory, so emptying the directory emptied it.
> - **Five shipped loopholes, all pack contributions:** `packs/audio`, `packs/host-processes`,
>   `packs/journal`, `packs/cgroup-delegate`, and `claude-oauth-broker` as a contribution of
>   `packs/claude` (`packs/claude/pack.json:12-15`; module at
>   `packs/claude/loopholes/claude-oauth-broker/`) — **not a pack of its own**, because the
>   dependency is structural (`loophole-activation.md` OQ-A10).
>
> **§1–§8 keep their original tense.** They describe the world in which three bundled loopholes and a
> per-jail relay existed, because that is the world the argument was made against, and rewriting them
> would destroy the reasoning that produced the move. What each section's claims cost NOW is
> annotated in place: §1's channel table, **§5.4's reason 2 (retracted — see the heading there)**,
> §7's whole migration table, and OQ-LP11 (now **COMPLETE**). Nothing in the design was refuted by
> the move; its conditional clauses were discharged.

**The last two batches each produced a finding that constrains the DESIGN rather than an
implementation, and both are carried below rather than filed as incidents.** (1) **The pack-shipped
subset cannot express `audio`'s reason to exist** — its two `${XDG_RUNTIME_DIR}` sockets are neither
under `$HOME` nor inside the module dir, so there is no spelling of them in the subset's vocabulary
(§7). (2) **The placement rule must not judge a BUNDLED loophole**, or yolo's own development jail
refuses all three of them on every launch (§4.3a, item 1a).

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

**Reads with:** [`gate-placement-principle.md`](gate-placement-principle.md) (why several gates in
§4 are placed where they are — and why one was deleted), [`pack-system.md`](pack-system.md) (the kind set — **fifteen** since this one landed — and how a kind is defined),
[`loophole-protocol.md`](loophole-protocol.md) (the wire contract),
[`loophole-transport.md`](loophole-transport.md) (the transport unification, shipped 2026-08-13),
[`pack-capabilities.md`](pack-capabilities.md) (what this narrows),
[`extension-point-principle.md`](extension-point-principle.md) (why design it now).

**What is verified and what is not.** Every code claim was read on HEAD as of the date it carries;
the import-cycle claim (§3.2), the `Discover` census (§5.1), the `Collisions` call sites (§3.1) and
the `:ro` socket result (§3.1, §7) were **measured**, not inferred. Measurements the implementation
has since invalidated are marked in place rather than deleted, because a measurement that quietly
becomes false is how the next reader is misled. **A pack-shipped loophole now exists and runs** — the
official `audio` pack ships one (item 9, §7), so **R2 is closed at the level that mattered**: a real
embedded pack goes through the claim enumeration, the name pre-flight, the subset loader, discovery
and the container argv. What is still unexercised is narrower and named in R2: no *fetched* pack's
loophole has been approved at a prompt and spawned as a host daemon.

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
> OQ-LP8 is all but closed. **OQ-LP9 was the last properly open one, and it is now BUILT** (§9,
> landing item 10): it had grown from a small question into a structural one — nested jails need the
> scope model to RECURSE, and measurement said it could not — and the maintainer's three-part split
> shipped as the inner-scope census, the two generated per-consumer files and a global
> `--user-layer` flag. The development escape hatch §4.3a wanted is DELETED — *"you can develop a
> loophole in a jail with jail in jail if you need"* — and measured, the loophole runtime has exactly
> ONE jail-aware branch (`runtime.go:143`, device passthrough), so a nested jail is a real
> development environment and the friction belongs on the real machine. That ruling is now **real
> rather than aspirational**: it is verified end to end (§9's OQ-LP9 Landed note).
>
> An eighth round produced the two findings this doc had to absorb as design constraints rather than
> fixes: **the subset cannot express a runtime-dir socket** (§7) and **the placement rule must exempt
> yolo's own bundled content** (§4.3a).

---

## 1. The gap, and why it got acute this week

Loopholes had three distribution channels and only one of them was third-party — the state this
section was written against. **There are THREE now, and the membership changed twice.** The
pack-shipped channel landed 2026-08-14 (`packs/audio`, §7, is its first inhabitant), taking the count
to four; then **OQ-LP10 was carried out and the hand-placed user directory was retired**, taking it
back to three. The bundled retirement (OQ-LP11) has still not happened.

| Source | Constant / entry | Who it is for | State |
|---|---|---|---|
| `bundled_loopholes/`, embedded in the binary | `BundledLoopholesDir` (`internal/loopholes/loopholes.go:89`) | yolo's own three | fine — but **why not an official pack?** OQ-LP11. All three are still here, and `audio` cannot be retired at all until OQ-LP14 |
| ~~a user loophole dir~~ | ~~`UserLoopholesDir` → `~/.local/share/yolo-jail/loopholes`~~ | ~~one hand-placed local loophole~~ | **RETIRED (OQ-LP10, carried out).** No fetch, no version, no approval, no manifest travelling with the code — and a `file://` pack subsumes it. Discovery no longer reads it and the `SourceUser` label is deleted; what survives is `RetiredUserLoopholesDir` + a migration notice (`internal/loopholes/retired.go`) that names every stranded module and the `mv` into `~/.config/yolo-jail/local/` |
| the `loopholes` block in `yolo-jail.jsonc` | `synthesizeConfigLoopholes` (`discover.go:29`) | **was the only third-party path** | revived by the front (§2.2) and scoped by the ruled install/enable model (§4.3b) |
| **a pack, `{"kind": "loophole", "from": …}`** | `loaderFor(SourcePack)` (`discover.go`) | **the third-party path now** | landed 2026-08-14; subset-constrained, claim-enumerated, origin-gated |

> [!NOTE]
> **2026-08-23 — the count is ONE, not three.** OQ-LP11 completed 2026-08-19: the bundled row of this
> table was deleted outright (`bundled_loopholes/`, its embed, `internal/loopholes/embedfallback.go`,
> `loopholes.ReservedLoopholeNames` and `paths.BuiltinLoopholeNames` — none of them resolve in the
> tree, verified 2026-08-23). The user-dir row was already retired by OQ-LP10. What survives is the
> `loopholes` block in `yolo-jail.jsonc` (a CONFIG surface, scoped by §4.3b — not a distribution
> channel) and the pack row. **`internal/loopholes` stops being a thing core knows about specially**,
> which is precisely the prize §5.4 named. The table's original three-channel framing is left standing
> because §1's whole argument is *why three was one too many*.

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

**Landed 2026-08-13** (`716d19b`): `{loophole_dir}` reaches `host_daemon.cmd` and `doctor_cmd`,
`{jail_loophole_dir}` reaches `jail_daemon.cmd`, each refused in the wrong half at load
(`internal/loopholes/load.go`). The measurement above is history.

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
   the withdrawn promise. **Said:** that doc's §Access logging now records the connection-level
   ceiling, shipped 2026-08-13.

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

**Landed 2026-08-13** (`50293dd`, teardown group-kill in `9ad0209`): the timeout prints the yellow
warning naming the loophole, the awaited path and the log; a real `cmd.Wait()` in a goroutine
replaced the dead `ProcessState` read, so a crashed daemon is reported at once. The measurements
above are history. One refinement the requirement did not anticipate: a status-**0** pre-readiness
exit keeps polling to the deadline, because a daemonizing wrapper forks the server and exits — both
halves pinned 2026-08-14.

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

**Landed 2026-08-13** (`66d4091`, `dd364d6`, `44966e1`, `ba43bfb`): `publishes`/`request_end` parse,
the front runs in front of every `publishes: "socket"` daemon, `discover.go` is flipped, and the hint
now sends a migrating author at `publishes: "socket"` with the pin updated to match
(`load.go:215-220`, `loopholes_test.go:566`). Both stale-text measurements are history.

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

**Landed:** [`loophole-protocol.md`](loophole-protocol.md) §"Writing a server from scratch" is that
section, shipped 2026-08-13.

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

**Landed 2026-08-14.** The set is composed once in `loopholes.ReservedLoopholeNames()` — three
contributors (`paths.BuiltinLoopholeNames`, `broker.BrokerLoopholeName`, and the bundled dir names
read off the **same `embed.FS` the loader materializes**, so adding a bundled loophole extends the
reservation with no second list to forget) — and refused fatally in the launch pre-flight, not in the
loader: `Discover` has no error channel by contract (§3.1's own "fatality cannot be implemented inside
`Discover`"). Composed rather than written as a literal *because a literal is the thing that drifted*:
`journal` was in `paths.go` and in nobody's refusal. Each reserved name carries an ORIGIN string —
prose, never a key — because the whole point of the rule is that the message names both sides.

The `loopholesruntime.go` skip now prints too, and the message says the part that made the silence
dangerous: the daemon is not started *but its bind mounts, devices and `jail_env` DID cross into this
jail*. It fires only when the name did not come from yolo's own builtin — and a **pack** can no longer
reach it at all (the pre-flight refuses the name at staging), which leaves the hand-placed user
directory, the one case that is not refused and therefore the one that needs saying out loud.

#### The pack-shipped subset of the manifest, corrected

Draft 1 refused two fields. Review showed the second refusal audits the wrong axis, and measurement
showed it is a no-op for the case that matters.

| Refused for a pack-shipped loophole | Why | Use instead |
|---|---|---|
| `jail_env` | it emits `-e K=V` (`internal/loopholes/runtime.go:156-159`), colliding with the `env` kind's target namespace — and `Collisions` keys on `{kind, target}` (`internal/packload/footprint.go:230-245`), so two *different* kinds claiming one target can never collide | the `env` kind, which the footprint already sees |
| `host_bind_mounts[].host` outside the pack's own tree or the user's home | see below — this is the axis that matters | `mount` (home-relative, `:ro`), or a `host_daemon` that mediates — **and that second answer turned out to be no answer for the commonest case; see "the vocabulary is incomplete" below** |
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
4. **Added 2026-08-14, from verification: `ca_cert` and `requires.file_exists` are path-scoped on
   the same axis, through the same classifier.** Both were listed under "everything else is allowed"
   below and both name a HOST PATH that a pack could set to anything.
   `ca_cert` is the SHARPEST of the three, not the mildest: it is bind-mounted from the host *and*
   its container path is joined into `NODE_EXTRA_CA_CERTS`, so an absolute value hands every node
   client in the jail a certificate authority the user never chose — and the resolver deliberately
   passes an absolute value through as-is. Legal for a pack: a relative path it ships, or `{state}/x`
   (name-keyed, survives restaging, which is what makes a pack-shipped CA possible at all).
   `requires.file_exists` is the one scoped field that crosses NOTHING — no mount, no exec, just a
   `stat` — and it is scoped because the ANSWER leaks: `InactiveReason` prints the resolved absolute
   path, so `yolo loopholes list` turned an unscoped field into a host-filesystem probe with a
   readout (`$HOME/.ssh/id_ed25519`). It gets no claim for the same reason: §3.3's rule is that a
   CROSSING must claim, and a line in the approval prompt for a stat dilutes a prompt whose value is
   that every line is a real capability. `command_on_path` is untouched — it asks PATH about a
   program name, and the answer names something installable.

**Everything else is allowed — and every one of them is CLAIMED (§3.3):** `host_daemon`,
`jail_daemon`, `intercepts` + `broker_ip` + `ca_cert`, `host_bind_mounts` (`:ro`), `host_devices`,
`state_files`, `requires`, `doctor_cmd`, `serves`. Two qualifications learned by verifying the landed
kind: `broker_ip` is claimed *inside* the intercept claim rather than on its own (it is not a separate
crossing — it is WHERE the intercept points, and leaving it out made two manifests differing only in
it compare as one approval), and `requires` is claimed by nothing (see requirement 4).

**The `jail_env` refusal has a real cost, stated rather than hidden.** A loophole's `jail_env` is
*conditional on the loophole being active*; the `env` kind is unconditional. `audio` relies on
exactly that (`PULSE_SERVER` only makes sense when the sockets crossed). So a pack-shipped
audio-shaped loophole would set env even when inactive. That is the case that would justify a
cross-kind collision pass, and it is purely additive — same claim model, one more pass beside the
three bespoke ones already there (`footprint.go:311-352`, `:357-384`, `:419-453`).

##### THE SUBSET'S VOCABULARY IS INCOMPLETE — the one finding in this batch that says the DESIGN is unfinished

Requirement 1 above is the rule. Writing the first manifest that has to obey it (item 9's `audio`
pack, §7) established that the rule's vocabulary **cannot express a legitimate, common host path**,
and that is a finding about this section rather than about the pack:

> **`audio`'s reason to exist is inexpressible for a pack.** Its two sockets are
> `${XDG_RUNTIME_DIR}/pulse/native` and `${XDG_RUNTIME_DIR}/pipewire-0`. The `$VAR` spelling is
> refused (requirement 1, and correctly — `${XDG_RUNTIME_DIR}` names an absolute path one indirection
> later). The literal `/run/user/<uid>/pulse/native` is refused as absolute. And it is **not under
> `$HOME`**, so home-relative cannot reach it either. There is no third spelling. The socket half of
> the one loophole this design nominated as its own dogfood cannot be declared by a pack at all.

**This is not an argument to weaken the rule**, and the temptation to read it that way is why it is
stated as a vocabulary gap. Admitting `${XDG_RUNTIME_DIR}` admits `${HOME}/.ssh` and
`${XDG_RUNTIME_DIR}/../../etc` with it: the refusal is doing exactly the work requirement 1 describes.
What the finding says is that the vocabulary is **too narrow**, not too permissive — a *runtime-dir
socket* is the ordinary shape of host IPC on Linux, a third-party loophole will want one, and the
subset has no way to say it.

**And this section's own named fallback does not survive contact with the case.** The table above
offers *"or a `host_daemon` that mediates"*. Applied here that reads: to bind the PipeWire socket the
user's own session already exposes, write an audio proxy — a host daemon that speaks the PipeWire
protocol, in a pack, forwarding frames — and thereby trade one `:ro` bind for **arbitrary host
execution plus a claim that says so**. That is not a mitigation, it is a strictly larger grant reached
by a much longer route, and it would be the *only* way an author could ship working audio. A rule
whose escape hatch is "run code on the host instead" is pushing authors toward the sharpest
capability in the system to obtain the mildest one.

**The proportionate fix, named and not designed** (per
[`extension-point-principle.md`](extension-point-principle.md), the same reasoning that produced
`platforms`): a **declared runtime-socket vocabulary** — an enumerated spelling for "a socket under
this session's runtime dir", resolved by yolo rather than by the manifest's own `$VAR` expansion, and
claimed as **host IPC**, which the claim producer already emits as its own class (§3.3). It is
declarative, it is not a path the author can widen, and the claim string stays machine-independent
because the *declaration* is what is approved, not the resolved path. Recorded as **OQ-LP14**; the
subset stays as it is until it is decided, and the guide states the limit rather than implying a pack
can do what a bundled loophole does.

> [!IMPORTANT]
> **SUPERSEDED 2026-08-17 by OQ-LP14's answer, and BUILT 2026-08-18.** The two paragraphs above
> argue that the path rule is right and the vocabulary is missing. Both halves lost: the rule
> admits `~/.ssh` and refuses a pulse socket, so its two cases are inverted, and the proposed
> runtime-socket vocabulary is an allowlist wearing an extension point's clothes. **The rule was
> withdrawn for `host_bind_mounts[].host`** — what survives there is a correctness rule (a `..`
> segment or a `:` makes the approved claim and the mounted path differ), and total claim
> enumeration plus the origin approval do the work the gate was pretending to.
> `ca_cert` and `requires.file_exists` stay path-scoped, each for a reason the ruling does not
> reach: a `ca_cert` is a TRUST INSTALL rather than a read, and a `file_exists` probe emits no
> claim at all, so the enumeration that replaced the rule does not cover it.
> The text is kept because the argument that lost is worth reading.

Both directions are pinned by test, so the finding cannot rot into an opinion:
`TestAudioShapedManifestIsRefusedByTheSubset` asserts the audio shape draws **four** subset
refusals (two writable binds, one `jail_env`, and the `$VAR` in `requires.file_exists`) — it was
six until the bind-host rule was withdrawn — and `TestBundledAudioIsOutsideThePackShippedSubset`
asserts the bundled manifests stay outside the subset.

**Landed 2026-08-14 as a SCHEMA-level subset, and WIRED at three seams.**
`internal/loopholedecl/packshipped.go` implements all three refusals (`jail_env`, the home-relative
`host` constraint, `readonly: false`) plus §2.1's `publishes: "socket"`-only rule, each with the
message naming what to write instead — and every problem is reported, not just the first, because
these are four independent declarations rather than a parse. Two loaders pair it with a decoder:
`loopholedecl.LoadDirPackShipped` (STRICT + subset, the authoring answer) and
`loopholes.LoadPackLoophole` (TOLERANT + subset, the discovery answer, since version skew is
orthogonal to the subset — an unknown key must not make a pack's loophole vanish while a field a pack
may not ship is refused whatever build reads it).
**It had a window where neither loader had a production caller**, and that window is history: the
SOURCE LABEL now selects the loader in `loadModuleDirs` (`loaderFor`), so discovery refuses a
violating pack manifest, `yolo pack lint` applies the subset at the authoring seam, and
`ValidateLoopholes` (`yolo check`'s own walker) reports the same violation. Measured while it was
open: a manifest with all four violations was discovered, Active, and produced `-v /:/ctx/hostroot`
plus `-e LD_PRELOAD=/ctx/evil.so`, and `pack lint` printed "pack ok" for it. See the item-5 note in
the landing order for the whole account.

**And the subset's real cost is now measured rather than predicted, by the pack that had to live
inside it.** The `audio` pack (§7) is the first manifest ever written against these rules, and
building it established that `audio`'s two `${XDG_RUNTIME_DIR}` sockets have **no spelling at all**
inside the subset's vocabulary. That is a statement about the *design*, not the implementation, and it
is argued in §7 rather than here because the example is where the evidence lives.

Three implementation facts worth keeping, since they are decisions the requirements did not make.
**(1) Pack-shippedness is the CALLER's fact, not the manifest's** — expressed by which loader you
call, never by a field or an option struct, because a manifest cannot declare that a pack shipped it
(it would simply lie) and every reader already knows which of the four sources it came from.
**(2) The default `publishes` is refused too**: an absent key decodes to `endpoint`, so a daemon that
says nothing about publication has declared the mode it may not have, and since the fix is identical
either way the manifest needs no declared-versus-defaulted bit to carry a better message.
**(3) `$VAR` is refused alongside absolute paths** in a bind `host`, because `"${XDG_RUNTIME_DIR}"`
names an absolute path one indirection later — admitting the variable while refusing the literal would
be a rule about spelling. And the reporting projection (`Loophole.PackShippedProblems`) goes back
*through* `loopholedecl` rather than reimplementing the rules, because two checkers over one subset is
how a refusal and a consent string come to disagree about what a pack may ship.

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

**Landed 2026-08-14** (`f3f160c`) as `platforms`, in `internal/loopholedecl/platforms.go`. Four
decisions the requirement left open, each with a reason the next reader would otherwise re-derive:

- **The vocabulary is Go's, and CLOSED on both halves.** An entry is `<goos>` or `<goos>/<goarch>`
  spelled as `go tool dist list` spells them. The closed list is the whole point: under an open one
  `"platforms": ["darwins"]` is a loophole supported NOWHERE, on every machine, forever, with no
  message — the exact silent-nothing shape this field exists to end. Go's own list rather than "the
  platforms yolo runs on", because the field's units are GOOS/GOARCH: a manifest declaring `windows`
  is honest and merely never supported, while coupling the enum to backend support would make
  tomorrow's new backend a migration for every manifest in existence.
- **Absent means every platform** (so every manifest written before the key keeps its meaning), and an
  explicitly EMPTY list is an ERROR — honoring it literally makes the loophole inert everywhere,
  honoring it loosely ignores what the author wrote, and neither is a good silence.
- **The declaration is static; its EVALUATION is a pure function of `(GOOS, GOARCH)`.** The schema
  package never reads `runtime.GOOS` — the platform of the machine is not a fact about the schema, and
  a leaf that reads the world grows an import. `internal/loopholes` supplies the pair, which also
  makes every combination testable from one process.
- **The message says nothing is missing.** `PlatformsUnsupportedReason` ends *"Nothing is missing on
  this machine and nothing can be installed to fix it"*, because the failure the field exists to fix is
  a Linux-only daemon reported on macOS as an unmet `requires` — and a reader who is not told
  otherwise spends the afternoon proving there is nothing to install.

**The skew shape, both directions.** A build predating the key reads it tolerantly (unknown ⇒ skipped
and reported), so it treats the loophole as supported everywhere — today's behaviour, which is why
adding the key is safe. But **values are not tolerated**, so a GOOS/GOARCH only a newer Go knows is a
refusal on an older build: that is the `tier` incident's shape, and it is why the lists live beside the
enums they resemble.

**And "one mechanism with the inert-backend report" is now literal rather than intended** — see §8's
Landed note: the platform producer shipped as a VALUE (`loopholes.InertNote`, one `Line()` rendering)
with zero callers, built expecting the backend half to plug in as a second `Axis`. Evaluated **before**
the `requires` probes and before the in-jail branch, because that ordering is what makes the
categorical answer win over the probe answer for a loophole that is both.

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

**Landed 2026-08-14** (`7fd8e8f`): `run.PackLoopholeNameConflicts`, per declaration, taking the
reserved set as its second input, wired into `stageRunPacks` and returning an error so the launch
refuses. Three details the requirement did not specify.
**(1) It lives in `internal/cli/run`, not exported from `packload`**, for the same cycle §3.2 measures:
`packload` cannot import `internal/loopholes`, so the reserved set is not *nameable* there.
**(2) The declaration carries the DECLARED `from` beside the resolved dir**, because a user reading
"two claims on `acme`" needs the two lines of the two manifests to edit — two absolute paths inside a
staging tree they have never looked at do not tell them that. `from` is resolved against the **staged**
root, so an `only`/`exclude` filter that removed the module dir is visible here rather than as a
loophole that "does nothing".
**(3) Pack-vs-reserved is reported INSTEAD of pack-vs-pack, not as well**, when both apply: it is the
stronger refusal and reporting both would name one mistake twice. Both messages name both sides, and
both spell out the consequence that makes the rule fatal rather than cosmetic — the loser's manifest
would still contribute its binds, devices and `jail_env` while the winner's daemon ran.

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

**Landed 2026-08-14 — OQ-LP1 RESOLVED BY EXTRACTION** (`f9c5990`, `dcd933e`): `internal/loopholedecl`
is the schema as a leaf (decode + static validation, no `exec.LookPath`, no `os.Stat`, no predicate
evaluation), and `internal/loopholes` reads the manifest **through** it. The recommended option, and
the cycle is unchanged rather than broken.

Two things the extraction had to decide, both of which keep "one vocabulary" true rather than merely
claimed. **`internal/loopholes/schema.go` re-exports the schema as type ALIASES and one-line
delegations, not new definitions** — twenty-odd call sites across `internal/cli` already spell
`loopholes.TransportLoopbackTLS` and `*loopholes.HostDaemon`, and they are talking about the same
things; two definitions would be two things a manifest could disagree with, which is the failure the
extraction exists to *prevent*, not to relocate. And the split of responsibility is stated where a
reader picks a package: reach for `loopholedecl` to READ a manifest (the footprint, `pack lint`, a
host-side validator — none of which may import the runtime), and for `loopholes` to get a RESOLVED
loophole (paths substituted, `requires` evaluated, container argv emitted).

**The loader also needs the strict/tolerant split it does not have.** `loadManifest` is a hand-rolled
`jsonx.OrderedMap` walk with **no unknown-key rejection at all** — `"version": 1` is declared by all
three bundled manifests and documented as the schema version, and nothing reads it. Contrast
`packdecl.Decode`'s `DisallowUnknownFields` for authoring plus a deliberately tolerant
`DecodeTolerant` for the version boundary (`packdecl.go:144`, `:206`). A pack-shipped loophole
crosses that boundary — host CLI reads it, and a skewed baked entrypoint may too — so it needs both
halves. Today it would tolerate skew and never tell an author about a typo.

**Landed 2026-08-13/14** (`5ba8331`, `2ab713f`): `loopholedecl.Decode` refuses an unknown key,
`DecodeTolerant` skips it and **reports it by name** in a `skipped` slice the caller surfaces as a
warning. Both strictnesses phrase it for their own audience — *"unknown key … it declares nothing"*
versus *"ignoring unknown key … a build that knows it will read it"* — and the key census is one
vocabulary per object so the walk and the census cannot disagree about what is known. `jail_env` and
`host_daemon.env` are deliberately excluded from the census: their keys are environment variable
names, so every key in them is known by construction. `"version"` is now *recognized and read by
nothing*, which is a different and better state than "declared and unnoticed"; it is not an enum yolo
checks. One consequence to know: `yolo check`'s own walker still reads **tolerantly**, deliberately —
a preflight answering "would this loophole load" must not report a manifest as broken that the loader
then happily uses, so adopting the strict decoder there is a behaviour change that belongs with
whichever change makes unknown keys an author-visible `check` error.

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

**Landed 2026-08-14** (`internal/loopholedecl/sanitize.go`): every claim-feeding value refuses C0
controls (newline and tab included), DEL and the C1 range, plus invalid UTF-8 as a backstop —
`host_daemon.cmd`, `doctor_cmd`, `jail_daemon.cmd`, intercept hosts, bind-mount host *and* container
paths, device nodes, `ca_cert`, `state_files`, and the loophole's own name, which is every claim's
target. The error names the rune and the byte offset and says *why*, so the author does not read it as
prudishness about odd bytes. Two scope decisions worth keeping: it is deliberately **not** a general
"no weird characters" rule — `description` and env keys/values are not sanitized, because they feed no
claim target or detail today and widening the refusal to fields with no consumer rejects manifests for
no reason (**a field that starts feeding a claim must join the list in the same change**) — and the
invalid-UTF-8 branch is knowingly a backstop, since `json5.Decode` already substitutes U+FFFD, kept
because "a value that reaches the approval prompt is text" must not depend on a decoder detail one
layer down.

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
loophole  acme-proxy:api.acme.com   INTERCEPTS api.acme.com -> host-gateway — installs a CA
                                    trusted by every TLS client in the jail                ⚠ review
loophole  acme-proxy:ca:{state}/…   TRUSTS the CA in {state}/ca.crt — mounted from your host
                                    and trusted by every node client in the jail           ⚠ review
loophole  acme-proxy:mount:/ctx/x   MOUNTS ~/x -> /ctx/x (read-only)                       ⚠ review
loophole  acme-proxy:ipc:/ctx/s     CONNECTS the jail to the host socket ~/s — read-write
                                    regardless of `:ro` (measured)                         ⚠ review
loophole  acme-proxy:dev:/dev/snd   PASSES THROUGH the host device /dev/snd                ⚠ review
```

Two rows above are corrections from verifying the landed producer, not from draft 1:

- **the `ca:` row exists at all.** `ca_cert` was in neither claim class and IS a crossing —
  bind-mounted from the host, then joined into `NODE_EXTRA_CA_CERTS`. With no claim, a
  `transport: none` loophole declaring only `ca_cert` reached `packMayAccessHost` with an EMPTY set
  and was granted. Its text names the CAPABILITY (a trusted CA) rather than the mount, because the
  capability is what an intercept's own claim exists to disclose and a module-relative `ca_cert`
  reaches it without declaring an intercept.
- **the intercept row carries `-> <broker_ip>`.** Without it, two manifests differing only in
  `broker_ip` produced the identical approved string, so an approval of an intercept pointed at
  yolo's own front silently covered the same hostname pointed anywhere. The default is spelled out
  when absent, so `broker_ip: "host-gateway"` and no `broker_ip` are ONE approval — the alternative
  is a rule about spelling.

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
- **`requires` needs no claim either — added 2026-08-14, and it is the boundary of the rule.** The
  rule is "a CROSSING must claim". `requires.file_exists` crosses nothing: no mount, no exec, just a
  `stat` whose boolean decides `Active`. Claiming it would put a line in the approval prompt for
  something that neither mounts nor runs, diluting a prompt whose whole value is that every line in it
  is a real capability. It is nonetheless **path-scoped** for a pack (§3.1 requirement 4), because the
  ANSWER leaks through `InactiveReason` into `yolo loopholes list` — the enumeration governs
  crossings, and a readable probe is a different problem with a different fix.

**Landed 2026-08-14** (`423c4af`) in `internal/packload/loopholesource.go`, with the table above as
the spec. Three things the enumeration turned out to need that the design did not state, and one it
stated in a form the implementation could not keep.

**A `jail_daemon` gets no claim either**, for the reason `state_files` gets none: it is a process
inside the container, which is the one place a pack's code was always allowed to run.

**An UNREADABLE manifest is a claim, not the absence of one.** The rule was written about a crossing
that claims nothing, and the same short-circuit fires when the manifest cannot be parsed at all — so a
module whose manifest fails to load still yields a module record carrying one fail-closed claim
(*"declaration UNREADABLE at &lt;from&gt; — its claims cannot be enumerated"*), marked as host
execution, because a manifest this build cannot read may well declare a daemon. A REFUSED declaration
(absent dir, name collision) yields no module and so no claim, which is right: nothing crosses,
because nothing will be discovered, and the refusal is reported by the paths that act on it.

**The claims are read TOLERANTLY, and that is the gate's choice rather than the author's.** A key only
a newer build knows is skew; refusing the manifest at the claim producer would turn a working loophole
into an unreadable one, re-prompt for approval, and — since the prompt fails closed on a non-TTY —
refuse the loophole permanently. Tolerant enumerates exactly what THIS build understands, which is
exactly what it will honor, so the claim set and the effect cannot disagree. The STRICT read is
`pack lint`'s.

**CORRECTION — the socket class's discriminator is coarser than this section specified, and it could
not have been otherwise.** The design says *"treat a socket bind as its own claim class"*, which reads
as a fact about the path. Nothing in the producer may stat it, for two independent reasons: the `host`
value is **raw** (`{loophole_dir}/asound.conf`, `${XDG_RUNTIME_DIR}/pulse/native`), and resolving
either is exactly what G2a forbids because it makes the claim machine-specific; and a stat is a fact
about *this machine at this moment*, so a claim that changed class when the socket happened to be
absent would re-prompt on the machine where it is missing. So the test is the only static evidence
there is: **`readonly: false`** — the manifest itself saying the bind is bidirectional, which is what
every socket bind in-tree declares and how this doc's own audio count reads them — **or a
`.sock`/`.socket` basename**. A `:ro` bind of a socket with a non-obvious name therefore lands in the
MOUNT class, which is why that class's text carries the socket caveat **verbatim** rather than claiming
"read-only" and stopping: nothing is understated, only the discriminator is coarse. **The precise fix
is a declared socket bit in `internal/loopholedecl`**, which makes the class a fact the author states
rather than one yolo infers.

One rendering decision, because it looks cosmetic and is not: the argv is joined with **shell
quoting**, and that is about INJECTIVITY rather than about a shell. Nothing execs the string (the spawn
reads the argv list), but a claim is a comparison key, and a bare space join is not injective —
`["sh","-c","a b"]` and `["sh","-c","a","b"]` collapse onto one approved claim, which is the same
failure an ellipsis would cause, arrived at by accident.

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

**Landed 2026-08-14** (`497c76e`), both halves. `LoopholeHostAccessClaims()` lives on `*Pack`, reading
through `internal/loopholedecl`, exactly as the precedent said; and `Pack.HostAccessClaims()`
(`internal/packload/hostaccess.go`) is the merged union of all three producers, called by
`resolveHostApproval` (which PROMPTS and records the lockfile) and by `packMayAccessHost` (which CHECKS
it at launch), with `hostaccessgates_test.go` failing at the source level if either gate reaches for a
producer directly. Two implementation notes: the union is **deduplicated**, because a set reached by
two producers is one thing to approve and a duplicate would make the approved set's length depend on
how the claim was derived; and it returns **nil** rather than an empty slice for no claims, because
`len(want) == 0` is the gates' own "nothing to approve" test and both spellings must read the same
there. The drift this closes is asymmetric and both directions are bad — a producer added to the
prompt only honors an unapproved crossing, and one added to the launch only refuses a pack with no
route to approving it.

**Mechanical costs**, each enforced by an existing test that fails until updated: `kinds_test.go:30`
hardcodes `14`; `kinds_test.go:99-107` hardcodes the review-worthy set; `applyhostcensus_test.go`
fails by name until the kind appears in `apply --host` output — and note it builds its pack from
`packdecl.KnownKinds()` and `t.Fatalf`s on a kind with no census contribution (`:110-115`), so the
new kind needs a census entry whose `from` dir and `manifest.jsonc` the helper must create;
`packkinddocs_test.go` fails until the kind is named in **both** `internal/cli/config_ref.txt` and
`packUsage` (`pack.go:57`). Also: `printPackFootprint` (`pack.go:473-483`) and `reportFootprint`
(`:901-911`) duplicate the claim-formatting loop despite `:464-466` claiming they are shared "so
their output does not drift" — a new marker has to be added twice or the two commands diverge.

**All paid 2026-08-14.** `kinds_test.go` reads `15`; the review-worthy set includes `loophole`; the
census pack gained a `loophole` contribution with a real module dir and manifest; both docs name the
kind. The claim-formatting duplication was fixed rather than paid twice — one shared `printClaimLines`,
so the comment that claimed they were shared is now true — and it carries **two** markers rather than
one, because `ReviewWorthy` is a single boolean now carrying two very different propositions:
`⚠ RUNS CODE ON YOUR MACHINE` for host execution and `⚠ review` for a host read. R4's complaint about
`reviewSummary` is fixed in the same place: executions are counted separately and **first**, named for
what they do, so a pack that runs a daemon no longer reads as *"1 loophole"*.

**And one cost NO test catches:** `notePackHostAccess` (`run.go:230-243`) switches on
`KindMount, KindReadsHost, KindEnv` and drops every other claim kind. Its own comment calls it
*"the transparency half of the approval model"* — see §4.3 G4, which also fixes its ordering.

**CORRECTION, found when that fix landed: this was never only about the `loophole` kind.** The
sentence above implies the hardcoded set was correct for the kinds that existed and merely lacked the
new one. It was **already wrong for two SHIPPED kinds** — `program via installer` (a curl-to-shell
URL) and `briefing after host:` (which reads the host home) are both host reads that
`HostAccessClaims` produces an approval claim for, and **neither appeared at any launch**. That is
what the missing test was hiding, and it is why the fix is the general one (a data table, exhaustive
over the kind set by test) rather than "add the loophole kinds". See §4.3 G4's Landed note.

**Correction to draft 1's `pack footprint` requirement.** It asked that `pack footprint` *"say which
side of the gate the claim is on"*. That command cannot report a fetched pack at all: `packFootprint`
handles a local/`file://` dir or an embedded pack NAME and errors otherwise (`pack.go:794-836`). So
the wants-versus-gets distinction has no surface there. It belongs at `yolo pack install`'s prompt
(`pack.go:1089-1123`) and in the launch-time refusal lines (`run/packs.go:218-231`) — or
`pack footprint` grows the ability to take a configured pack name, which is a separate, small item.
**Still true 2026-08-14** (the command's argument handling is unchanged), and one deliberate decision
about the footprint's loophole claims sits beside it: they are **not** gated on `MayAccessHost`, unlike
`reads-host`/`mount`. Those two report what will be HONORED; a loophole claim reports what the pack
**WANTS**, which is the question a footprint answers — hiding a fetched pack's daemon argv from the
footprint would hide exactly the line the reader came for.

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

**Landed 2026-08-13** (`2e38115`, `254e778`): `DecodeTolerant` drops an unknown kind and reports it in
`skipped`; `packload` carries the notes as `SkewNotes`; `LoadJailPacks` warns each one so the
degradation is audible, never silent. The brick above is history. The regression test asked for here
arrived 2026-08-14 at the LoadJailPacks seam (`internal/entrypoint/packskew_test.go`) — err == nil is
the boot decision, and the note on Stderr is the audibility half; a container was not needed for
either, and no integration test is outstanding.

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

**Landed 2026-08-14, both halves as specified.** `refusalReasons` carries the inverse-reason sentence
verbatim (*"a host daemon whose only client is a container"*), so the generic line is unreachable; and
`loophole` is an explicit `JailFields()` **exclusion** rather than a derived `true`, with the comment
recording why — its jail-side effects are real (`--add-host`, bind mounts, `jail_env`) but they are
produced by `startLoopholes` in the host CLI **before the container exists**, so if anything renders a
loophole in a jail, it is not the entrypoint's surface loop.

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
same shape `mount` has today, refusals printed per-claim (`packs.go:218-231`).

> [!WARNING]
> **The clause above is RETIRED as of 2026-08-18 — "while its other contributions still work" is no
> longer true, and this paragraph is kept only so the change is legible.** OQ-TP6 in
> [`trust-paths.md`](trust-paths.md) rules that **a refused contribution refuses the LAUNCH**: there
> are no partial packs, so an unapproved loophole claim no longer degrades a pack into its
> still-permitted half. The three choices are fix the pack, remove the pack, or approve it.
>
> The rest of G3 stands unchanged and is load-bearing: origin still bounds host access, a fetched
> pack still needs every claim approved against the lock, and a nil, missing or corrupt lock still
> approves nothing. What changed is only the **consequence** of that verdict — withheld became
> refused. With §3.3's total
enumeration, `len(want) == 0` is now only reachable for a pack that genuinely crosses nothing.

**LANDED 2026-08-14, and the enforcement had to be built — the DECISION alone shipped first.** The
per-module gate (`loopholes.PackModule.HostExecApproved`, set from `p.MayAccessHost` in
`run.packLoopholeModules`) was correct and had exactly ONE production reader, `RunDoctorChecks`.
`ManifestHostDaemonSpecs` filtered on `FromConfig`/`HostDaemon`/`Active` and `RuntimeArgsFor` on
`FromConfig`/`Active`; neither consulted it. So an unapproved fetched pack's daemon entered the spawn
list and RAN, and its binds, devices, intercepts and CA reached the container argv — while
`packMayAccessHost` answered false and `run/packs.go`'s own comment said the two were *"the SAME gate,
not a second one that could disagree"*. True of the decision, false of its enforcement.

Three parts, and the shape of each is the argument:

- **Enforced in the CALLEE.** Both functions now honor NO `SourcePack` record without a gate, and the
  gated forms are `Set` methods — the shape `RunDoctorChecks` already had, for its stated reason: A
  SLICE CARRIES NO GATE, so the only place the check cannot be forgotten is inside the function that
  acts on the records. Both are exported and take `[]*Loophole`, so any caller assembling records
  another way would otherwise walk past the boundary. The ungated path also WARNS: a caller reaching a
  host crossing with no origin decision is a programming error, and a silently-degraded jail is how
  this stayed invisible.
- **A fourth surface: the BRIEFING.** It advertised an unapproved loophole as a live capability, which
  is `Active()`'s answer — an unapproved loophole is enabled, on the right platform, requirements met,
  and crosses nothing. So `Set.Honored()` (Active + the gate) is a third predicate beside
  Enabled/Active, and the briefing reads it. Identical failure mode to §5.1's shipped bug one axis
  over: the agent goes and debugs host wiring that was deliberately withheld.
- **Discovery is UNCHANGED**, which is how "not discovered at all" and §5.1's visibility requirement
  are reconciled. Nothing of the loophole crosses; `yolo loopholes list`/`status` still show it, as
  `unapproved`. A missing entry is indistinguishable from a pack that failed to stage, and the fix
  ("`yolo pack install` records the approval") is not discoverable from an absence.

**And "refusals printed per-claim" was unimplemented — there was no `HonoredLoopholes`.** The
withholding shipped silently, which is worse than the `mount` case it cites: a missing mount is a
missing directory, a missing loophole looks like a broken one. Added beside the three shipped
`Honored*` reporters, PER MODULE rather than per claim — claims are the right unit for approval (each
separately approvable) and the wrong unit for a refusal, since the gate is per pack and the fix is one
action, so claim-granularity prints five identical lines about one decision.

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

**Landed 2026-08-14**, with the fix generalized past "add the loophole kinds" — because the
hardcoded set was **already wrong for two shipped kinds**, which is what the missing test was
hiding. `program via installer` (a curl-to-shell URL) and `briefing after host:` are both host
reads that `HostAccessClaims` produces an approval claim for, and neither appeared at any launch.
So the covered set is now DATA (`run/packloopholes.go`'s `disclosureClasses`), exhaustive over
`packdecl.KnownKinds()` **by test**, with an unclassified kind defaulting to **exec** — the only
fail-closed direction, since `skip` reproduces the original defect and `read` reproduces the
ordering one. A second test asserts every review-worthy kind is covered, with `state` and
`skills` as *named* exclusions (a jail-home subtree and a plugin running code *in the jail* are
not host access) so that reasoning has to be refuted rather than quietly changed.

The ordering is structural, not positional: `startLoopholesDisclosed` is the **sole** call site of
`startLoopholes` (pinned by a test that scans for a second one), so it cannot be broken by moving
a line — which is exactly how it broke. And the ordering test reads the spawn side's own **first
side effect** (the per-jail host-services dir), because asserting only that the line printed
passes under the OLD ordering. Verified by deliberately reverting the two statements: the test
fails.

**The read/exec split is PER CLAIM, not per kind** — settled when the kind landed in the same
batch and brought `Claim.RunsHostCode` with it. One `loophole` contribution emits several claims
and only some execute, so a kind-level answer is wrong in both directions: all-exec would put a
CA and a passed-through device in the block whose value is that every line in it is about to run,
and all-read would print the daemon argv after the spawn. `RunsHostCode` is the precise fact
(HOST execution, deliberately not "code runs somewhere"), so the disclosure defers to it and a
non-executing loophole claim degrades to a READ rather than disappearing. An **unreadable**
declaration is disclosed as exec, agreeing with the claim producer's own fail-closed reading —
the one case where yolo cannot see what will run must not be the one case it announces late.

**A BANNER THAT SHOWS A REFUSED DAEMON AS PENDING IS WORSE THAN SILENCE — fixed 2026-08-14, and
the two reports' questions are what the fix rests on.** The disclosure printed an UNAPPROVED
pack's daemon argv under *"This launch runs pack code on your machine"*, because `FootprintOf`
deliberately does not gate a loophole claim on `MayAccessHost` — which is right for
`pack footprint`, whose own doc says the point is to see what a pack WANTS before trusting it.
The launch asks a different question (what is ABOUT TO HAPPEN), and the pre-spawn block's whole
value is that every line in it is imminent, so a withheld daemon shown as pending is false in the
one place a user reads to decide whether to hit ctrl-c — and it teaches them the block cannot be
trusted. Subtracted at the DISCLOSURE (`claimWillHappen`), never at the footprint. Written as a
rule over host-crossing classes rather than a special case for `loophole`, because
`program via installer` and `briefing after host:` are ungated in the footprint too (their gates
apply where they are HONORED), so they had the same latent defect. `env` is the one named
exception: literal strings from `pack.json`, origin-gated nowhere, so a refused pack still gets
them and the launch must still say so.

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

**AND IT MUST NOT JUDGE A BUNDLED LOOPHOLE — landed 2026-08-14, found by the mandated nested-jail
smoke, and the exemption is the rule's own reasoning rather than a concession.** The rule as first
shipped refused all three bundled loopholes on **every launch of yolo's own development jail**:

```
Refusing to start loophole claude-oauth-broker: module dir
/workspace/bundled_loopholes/claude-oauth-broker is inside the workspace this launch
bind-mounts :rw ... Install the loophole outside that tree.
```

That is advice nobody can follow about content they did not install, and it takes out the OAuth
broker, the audio pass-through and `host-processes` together. **Root cause is the self-hosting case:**
`BundledLoopholesDir` prefers the repo checkout when yolo runs from its own source tree
(`loopholes.go`'s `reporoot.Resolve` branch), so `<repo>/bundled_loopholes/*` **is** inside the tree
the launch mounts `:rw` — the one configuration where the two paths coincide.

The fix is a `Source == SourceBundled` exemption on the module-dir face
(`internal/loopholes/placement.go`), and it is right on the merits, not merely expedient: the rule
exists because installed content in an agent-writable tree can be swapped by an actor with none of the
authority that installed it, and **a bundled loophole is the yolo binary's own content — the same
artifact that implements this check.** An agent that can rewrite it has already rewritten the checker,
so the gate protects nothing it does not already presuppose, which is
[`gate-placement-principle.md`](gate-placement-principle.md)'s **Test 1** exactly. **PACK and USER
loopholes stay judged**, and the regression pins all three cases in one test: bundled-in-the-workspace
passes, pack and user at the *same path* are still refused. Without those controls the exemption would
be indistinguishable from disabling the rule; dropping the `SourceBundled` clause fails the test.

**The reusable lesson is about verification, not about loopholes.** No unit test could have caught
this: every placement test builds its module dir under `t.TempDir()`, so the configuration where the
bundled dir and the workspace coincide is the one nobody constructs. **A rule about how two REAL paths
relate cannot be verified by a test that invents both of them** — which is why AGENTS.md makes the
nested-jail run mandatory rather than advisory, and why the `-short` suite staying green was not
evidence of anything here.

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
| Gate | one confirmation, every origin — WHAT it checks is OQ-LP13, RULED and landed as the placement rule (§4.3a) | none required |

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
   **Landed 2026-08-13** (`b8c0339`): approval requires a terminal the tty ioctl confirms, refused
   BEFORE the prompt is shown so a pipe is never invited to answer it, and the claims still print so
   a CI log shows what is waiting on a human. Both branches of the predicate are pinned (a non-file
   reader, and a real `os.Pipe` with bytes waiting — the second added 2026-08-14).
4. **`apply --host` silently drops every fetched pack today.** `packForCheckDeps`
   (`checkdeps.go:135-137`) returns nil for anything not embedded and not `file://`, and the printed
   reason blames offline resolution. So the G3 gate is untested at that command. Pre-existing.
5. **Two backends make the whole kind a silent no-op** — see §8 item 2, which draft 1 scoped far too
   narrowly.

### 4.5 Nothing reaps a departed loophole's state — and the mechanism draft 1 cited does not exist

Measured **as of the finding**: `rg -c 'loophole' internal/prune/*.go` returned **zero**. So nothing
pruned per-loophole state dirs (`~/.local/share/yolo-jail/state/<name>/`), `host-service-<name>.log`
under `GlobalStorage()/logs`, or the materialized embed cache. For a hand-placed loophole that is
untidy. For a pack-shipped **intercepting** loophole it is a CA private key left behind by a pack the
user deselected.

*(That count is no longer zero — see "Landed" below. The **materialized embed cache** is still
unswept, and deliberately so: it is content-addressed, derived from the binary's `embed.FS`, and
identical on every machine running that build, so it is regenerable cache rather than state anyone
owns. It belongs with the cache-age sweep if it is ever worth reclaiming, not with retirement.)*

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

**Landed 2026-08-14.** `internal/packstage/loopholeowners.go` (record + `RetireLoopholeState`),
`run/loopholeretire.go` called from `stageRunPacks` (so it covers attach too), and
`prune.PruneRetiredLoopholeState` with its own report section. The archive also takes
`host-service-<name>.log`, which this section names beside the state dir, and carries a `.pack`
marker: the record that named the pack is about to forget it, and *"whose key is this?"* is the
first question a user asks of an archived directory. Three refusals the requirement did not
list — **retire before record** (one edit can drop a pack and select a different one shipping the
same loophole name; recording first would hand the new pack the old pack's CA), an **unknown**
configured-pack set retires nothing, and a **corrupt** record is neither acted on nor overwritten.

**One gap left open, and it is the same tension this section is about.** A pack still in `packs`
that has STOPPED DECLARING a loophole is not detected. Its evidence is indistinguishable from a
momentarily-unreadable pack tree, and the cost of being wrong here is a moved private key — so
retirement keys only on the signal the user typed: the pack leaving `packs`.

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

**Landed 2026-08-14** (`7fea38e`, `e6987f0`, `ff32876`, `f764d73`): `loopholes.Set` is the constructed
value and `loopholes.NewHostSet` is THE constructor — bundled + the pack modules this process recorded
+ the user dir + the given config block — so a consumer cannot assemble a different view, cannot forget
`IncludeBundled` (whose zero value is `false`, which every old call site had to remember to set), and
cannot bypass the origin gate. The convergence is asserted structurally: one test walks the census
files, and another pins that the census is **seven over six files plus the walker** (`assemble_parts.go`
carries two of the seven, which is why the counts differ).

**The requirement's "until it exists" clause became permanent, and that is the load-bearing half.**
`RunDoctorChecks` does not merely *take* gated loopholes — it **refuses to execute a pack-sourced
record** unless the caller recorded that the pack's host access was approved, and a `Set` assembled by
hand (`SetOf`) carries no gate at all, so `MayRunHostCode` is false for every pack record in it. The
reason it is enforced in the callee is exactly the fork above: sites 5 and 7 have no pack resolution,
no lockfile and no `packMayAccessHost` in reach, so a rule they were merely *asked* to follow is a rule
the next call site does not know about — and a slice carries no gate. The refused record comes back
with `RC=nil` and an explanation naming `yolo pack install`, rather than silently.

Three more things the requirement did not list. **`PackModules()` is empty-is-fail-safe at every
branch** — a process with neither a staged record nor a resolver sees no pack loopholes at all, because
a pack loophole missing from `yolo loopholes list` is a visible omission while an unaudited daemon
self-check executing under a read-only preflight would not be. **`ValidateLoopholes` (site 7) stays a
walker rather than becoming a `Discover` call**, because its whole job is reporting bad manifests and
`Discover` swallows per-manifest failures by contract; it reads the *same* recorded modules, and pairs
with the gate through `ValidateSet` so a caller that both reports and executes gets both from one walk.
And **the briefing bug §5.1 names below was fixed in the same change** — see there.

**Precedence — draft 1's line is DELETED.** It said `bundled < pack < user < config-override`, with
pack-vs-bundled collisions fatal. Those two sentences contradict each other, and under a
warn-and-last-wins implementation the precedence line wins: a pack loophole named
`claude-oauth-broker` would **replace** the bundled record, `assemble_parts.go:427-428` would then
evaluate the PACK's `Active()` to decide the terminator/CA/endpoint wiring while
`loopholesruntime.go:156-159` still special-cases the NAME and runs yolo's own broker argv — half
the broker from one manifest, no message. **Corrected: pack-vs-reserved is refused in the pack-side
pre-flight (§3.1) and therefore never reaches an ordering.** What remains is `user` overriding
`pack`, and `config-override` on top, exactly as today.

**Landed 2026-08-14 exactly as corrected:** pack modules sit between bundled and the user dir in
`Discover`'s ordering, so a hand-placed user directory still overrides a pack's loophole (it carries the
user's own authority), and a pack claiming a reserved name never reaches the ordering because the
pre-flight refused it at staging. The broker lookup is additionally pinned unshadowable by a test.

**Superseded in part by OQ-LP10's retirement:** there is no user dir to override anything any more, so
the live ordering is bundled < pack < config. The reserved-name half is unchanged.

**One shipped bug to fix while here:** the briefing path (`prepare.go:61-66`) filters on `Enabled`
only, not `Active()`. So an enabled-but-inactive loophole is advertised to the agent as a live
capability. Pre-existing and orthogonal, but a pack-shipped loophole makes it more visible.
**Fixed 2026-08-14**, and it belongs to the convergence rather than beside it: every other consumer of
the set already keyed on `Active()` (the container argv, the daemon spawn, the broker predicate), so the
briefing was the one surface that did not — which is precisely the divergence the convergence exists to
remove. The concrete harm is that the briefing is *instructions the agent acts on*: telling it audio is
available when the sockets never crossed is how an agent comes to debug ALSA instead of reading one line
saying the loophole is inactive here.

### 5.2 `yolo loopholes enable/disable` and the pack-shipped case

**Correction to draft 1's premise.** It claimed the toggle *"would appear to work and silently
evaporate on the next launch"* because `SetEnabled` read-modify-wrote the manifest inside the staged
tree. Measured: `CmdSetEnabled` never reached `SetEnabled` for a non-user-dir loophole — it stat'd
`UserLoopholesDir()/<name>/manifest.jsonc` and exited 1 with *"For bundled or workspace-inline
loopholes, edit the workspace yolo-jail.jsonc"*. So it **refused outright**; the failure was a wrong
instruction, not a silent evaporation. (And that instruction pointed at the **weaker** scope, which G1
changed.) *Both functions are described in the past tense because `SetEnabled` is now deleted — see
the ledger at the end of this section.*

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

Unifying *all* sources on config-side enabled state would also delete `SetEnabled`'s
comment-destroying RMW and is the better end state — but it changes behaviour for bundled and
user-dir loopholes, so it is a separate decision.

**Landed 2026-08-14 — the prerequisite is met by the FIRST of its two options.** The resolver joined
the converged set (it reads `PackModules()`), so `loopholes.<name>.enabled` on a pack-shipped loophole
now resolves to a real `LoopholeInfo` and takes the OVERRIDE path instead of the unknown-name fallback
that printed *"no loophole named 'x' is installed on this machine"* at every launch. The second option
was rejected for a reason worth keeping: recording a pack loophole's state somewhere else would give
one name two homes. The CLI message was retargeted at the user config in the same batch as G1.

**Then OQ-LP10 landed and forced the rest of the way — half of it.** `SetEnabled` is DELETED: with the
hand-placed directory retired it had nothing left to write, since a bundled manifest is the binary's
own content (go:embed'd, so on an installed binary there is no file at all) and a pack's manifest is a
staged copy the next `pack install` overwrites. `CmdSetEnabled` therefore serves no source at all
today: it PRINTS `loopholes.<name>.enabled`, the user config path and the value, and exits 1.

**Why it prints rather than writes, and this is the open half.** The write is a read-modify-write of
`~/.config/yolo-jail/config.jsonc` — a hand-written, commented file nothing in yolo writes today — and
the json5 → `jsonx.DumpsIndent` round trip drops every comment in it. That degradation was documented
and accepted for a yolo-GENERATED manifest; it is a different proposition for a file the human wrote.
The obvious dodge is already refused by this codebase: a conventionally-named auto-merged state file
beside the config is **withdrawn with cause** in `internal/config/userlayer.go`'s header ("it activates
because a file EXISTS, invisibly at the call site"). So the remaining work is a comment-preserving
JSONC edit (or a ruling that comment loss is acceptable here), and it is tracked as its own item
rather than smuggled in with the retirement.

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
starting its daemon, and (since 2026-08-14) retires the state that daemon left behind (§4.5). It does
not stop a daemon that already ran, and a process that has executed once can persist by means yolo
has no view of. This design does not claim otherwise, and no packaging design could.

**Corrected 2026-08-14: teardown no longer "signals one PID".** That sentence described the code §4.5
set out to fix, and the fix shipped — `killServiceGroup` SIGTERMs (then SIGKILLs) the whole process
**group**, which the `Setsid` spawn makes reachable through a negative pid. So what survives
deselection is narrower than this section used to say: not everything the daemon forked, but only
what it deliberately placed outside its own group — a `~/.bashrc` line, a cron entry, a
double-forked reparented process. The claim stays bounded rather than absolute, which is the point;
the boundary is just in a different place, and the kill is now pinned by a test rather than left to
inspection.

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

#### ⚠ Retracted 2026-08-23: reason 2 — *"a pack could not express what the broker needs"*

The one argument this section graded ✅ **survives is the one the implementation refuted.** Both of
its two legs are gone, and each was closed by adding vocabulary rather than by conceding the point:

| Reason 2's leg | What happened |
|---|---|
| *"its `host_daemon.cmd` is not what runs — `startLoopholes` special-cases the NAME"* | `host_daemon.scope: "host"` is the vocabulary the spawn path lacked (`internal/loopholedecl/enums.go:115`, `ScopeHost`). The branch now reads `hd.Scope == ScopeHost` and ensures the daemon through `internal/broker`'s existing flock/recheck/spawn engine, generalized by ONE `Deps.Argv` field so the argv is the MANIFEST's. **`startLoopholes` no longer tests a loophole's name.** `paths.HostSingletonSocket(name)` (`internal/paths/paths.go:254`) derives the rendezvous from the loophole name, because a singleton has no jail to be keyed by. Load-time rule: `scope: "host"` REQUIRES `publishes: "socket"` (`loopholedecl.go:914`) — an endpoint file carries ONE jail's bearer token, so a host-wide publisher would hand every jail the same credential. |
| *"its per-jail relay … has no manifest vocabulary at all (`ensureBrokerRelay`)"* | **`internal/brokerrelay` is DELETED** (`7df7c5aa`, 2026-08-19). There is no relay process to give vocabulary to. The front is a goroutine in the launching `yolo run` (`svcendpoint.ServeFrontWithOptions`, `loopholesruntime.go:686`); yolo prepends its own connection preamble and never parses the daemon's payload, so the daemon behind the front still never learns its transport. A jail ending closes ITS FRONT and nothing else — no process-group kill, no socket unlink — or every other live jail would lose its credential path. |

**Reason 1 is also discharged, exactly as this section predicted it could be.** `default_enabled:
true` on the pack's manifest is what makes *selecting `packs/claude`* sufficient — the
implicit-selection bit §5.4 pointed at, used. And the `requires.command_on_path: "claude"` gate that
reason 1 leaned on was **deleted**, because it was a HOST-side `exec.LookPath` that read false for
exactly the user yolo exists for: someone who installs `claude` inside the jail and never on the
host. It silently took refresh serialization away from them, with the loophole reporting inactive and
no reason given.

> [!WARNING]
> **The trap this retraction must NOT be read as licensing.** `claude-oauth-broker` is a
> **contribution of `packs/claude`**, not a pack of its own. The dependency is structural
> (`loophole-activation.md` OQ-A10): a separate broker pack would reinstate a selection step, i.e.
> hand back the every-jail default-on failure reason 1 exists to prevent. And with
> `ReservedLoopholeNames` gone, what protects the name `claude-oauth-broker` — the one name yolo
> still reaches by literal, from `yolo broker status`, `yolo check`, `brokerEnsure` and the
> terminator's endpoint variable — is (1) `packs/claude` OCCUPYING it, since loophole names are
> sole-owned across packs, **fatally**, and (2) the origin gate. There is no reservation list to
> add a name back to.

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

**Nothing migrates.** All three bundled loopholes are still bundled; the kind is an extension point
rather than a refactor. **One official pack now ships a loophole BESIDE the bundled set** (`audio`,
below) — added, not migrated, and for reasons that turned out to be forced rather than cautious.

| Loophole | Verdict | Why |
|---|---|---|
| `claude-oauth-broker` | **stays bundled** | Auto-activates by design (§5.4); its host singleton bypasses its own `host_daemon.cmd`; its per-jail relay has no manifest vocabulary at all. |
| `host-processes` | **stays bundled** | Its client is `cmd/yolo-ps`, a baked image binary. A pack cannot ship it. |
| `audio` | **stays bundled — and an ADDITIVE official pack beside it is the worked example** | `transport: none`, no daemon. It is the one bundled loophole a pack could carry with zero new vocabulary and zero host execution — though only *half* of it, as the finding below establishes. |

### ⚠ Retracted 2026-08-23: "Nothing migrates" — all three rows did

> [!WARNING]
> This section's headline and **every row of the table above are now false**, and the doc keeps them
> because each verdict names a real blocker and each blocker was closed by a named mechanism rather
> than waived. Verified 2026-08-23: `bundled_loopholes/` does not exist.
>
> | Row | Its stated blocker | How it was closed |
> |---|---|---|
> | `claude-oauth-broker` | *"host singleton bypasses its own `host_daemon.cmd`"* + *"per-jail relay has no manifest vocabulary at all"* | `host_daemon.scope: "host"` gave the singleton a declaration, and the relay was **deleted** rather than described. Full accounting at §5.4's Retracted heading. Shipped `e391d0f5`, 2026-08-19, as a **contribution of `packs/claude`**. |
> | `host-processes` | *"its client is `cmd/yolo-ps`, a baked image binary. A pack cannot ship it."* | §5.4 reason 3 already graded this ❌ — an OFFICIAL pack is embedded in the same binary, so a baked client is no obstacle. `packs/host-processes` shipped `3d5805cd`, and *"goes dark until you ask"*: the allowlist is now resolved once, at launch, by core (`55c18ed4`). |
> | `audio` | *"only half of it"* — the `${XDG_RUNTIME_DIR}` sockets were inexpressible | OQ-LP14 **withdrew the bind-host path rule** instead of adding vocabulary (2026-08-17, built 2026-08-18), and the two audio loopholes merged into one pack-shipped `audio` under the plain name (`ea6f5e5b`). Already recorded in the SUPERSEDED block below — that block is now the whole story, not a partial one. |
>
> **What did NOT change:** the claim enumeration, the origin gate, the subset's `readonly: false`
> refusal and its claim-class consequence, and the `jail_env`→`env` unconditionality cost (OQ-LP5,
> still live). The migration happened; the design that governs it did not move.

**`audio` is the dogfood, as an example rather than a migration.** Draft 1 said to ship a copy under
`docs/examples/`; **it shipped as a real official pack instead** (`packs/audio`, embedded in the
binary), per OQ-LP11's ruling that it ships IN this batch. **And draft 1 described it wrongly in three
ways, and the corrections matter more than the example does.**

1. **It is NOT the thing "that needs no approval to run".** That sentence was written when bind
   mounts and devices emitted no claims (§3.3). Under the corrected enumeration the audio *shape*
   emits **four** review-worthy claims and a fetched copy of it would prompt. That is the whole point:
   the example exercises the approval path too, which is strictly better dogfood than draft 1's
   version. **The count is right and draft 1's composition of it was wrong:** it said *"three
   socket/dir binds and `/dev/snd`"*, and measured against the shipped producer only **two** of the
   three binds are socket-class (the two that declare `readonly: false`) — the third,
   `{loophole_dir}/asound.conf`, is a `readonly: true` regular file and lands in the MOUNT class. So
   the four are **two IPC + one MOUNT + one device**, pinned exactly (not as a floor) by
   `TestAudioShapedManifestEnumeratesEveryCrossingClass`. The distinction is not cosmetic: the MOUNT
   class's text carries the socket caveat verbatim precisely *because* the two classes are separate,
   and collapsing them would make that caveat meaningless.
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

**LANDED 2026-08-14 as `packs/audio`, an OFFICIAL pack — and building it produced the batch's one
DESIGN finding.** What shipped: one `loophole` contribution (`loopholes/audio-alsa`) and one `env`
contribution (`PULSE_SERVER`, `PIPEWIRE_REMOTE`), embedded in the binary and selected like any pack
(`"packs": ["claude", "audio"]`). It is **additive** — `bundled_loopholes/audio` is untouched, still
activates by `requires`, and still does the whole job — so a user who does nothing sees no change.

**THE FINDING: `audio`'s reason to exist is inexpressible for a pack.** This is a constraint on the
DESIGN, argued in full at §3.1 ("the subset's vocabulary is incomplete") and recorded as **OQ-LP14**.
In one paragraph: the loophole exists to pass through `${XDG_RUNTIME_DIR}/pulse/native` and
`${XDG_RUNTIME_DIR}/pipewire-0`, and the pack-shipped subset has **no spelling for either** — the
`$VAR` form is refused, the literal `/run/user/<uid>/…` is refused as absolute, and the path is not
under `$HOME` so home-relative cannot reach it. So the honest pack ships the **ALSA half only** (a
`conf.d` fragment via `{loophole_dir}`), and this section's own fallback — *"a `host_daemon` that
mediates"* — means **writing an audio proxy to bind a socket the user's own session already exposes**,
i.e. trading a `:ro` bind for arbitrary host execution. Draft 1's *"the one bundled loophole a pack
could carry with zero new vocabulary"* was therefore half right: the claim classes need no new
vocabulary, and the *host paths* need one that does not exist.

> [!IMPORTANT]
> **SUPERSEDED 2026-08-18 — both shapes were undone by the things that forced them, and the
> FINDING is retired.** OQ-LP14 withdrew the bind-host path rule (it admitted `~/.ssh` and refused
> a pulse socket), so the sockets became expressible for a pack; deleting `bundled_loopholes/audio`
> then freed the reserved name, because the reservation was DERIVED from the directory rather than
> listed beside it. The two audio loopholes merged into one pack-shipped `audio`, and the pack is
> now a REPLACEMENT rather than additive. The `conf.d` destination survives the collision it was
> chosen to avoid: it is the spelling measured working, and moving to the freed path would be an
> unmeasured edit. What remains true is the `jail_env`/`env` cost below, and one thing this text
> did not anticipate — the subset's surviving `readonly: false` refusal costs a socket bind its
> read-write-IPC CLAIM CLASS, because `packload.bindIsIPC` splits on that bit. See
> `packs/audio/README.md`.

**Two shapes of the pack were forced by measurement, and each would otherwise look like a stylistic
choice:**

- **The loophole is named `audio-alsa`, not `audio`.** `audio` is a RESERVED name (the bundled
  directory names *are* the reserved set, §3.1) and `run.PackLoopholeNameConflicts` refuses a pack
  claiming one **fatally**. Probed: *"loophole \"audio\" is reserved for the bundled audio loophole …
  Rename the loophole's directory."* Taking the plain name would have meant deleting a working shipped
  capability for a cosmetic win.
- **It binds `/etc/alsa/conf.d/50-yolo-audio-alsa.conf`, not `/etc/asound.conf`.** Measured: podman
  refuses two binds on one destination whose sources differ (*"duplicate mount destination"*), so a
  jail selecting this pack **while the bundled loophole is active** would refuse to start. alsa-lib
  loads `conf.d` *before* `/etc/asound.conf` (its own `alsa.conf` `@hooks` list), so the fragment
  delivers the same routing at a destination nothing else claims — verified with `sox`, a real
  libasound client, in all three cases (unrouted → *"cannot find card '0'"*; fragment-only → routed;
  both → identical to fragment-only, no duplicate-definition error, because a later `pcm.!default`
  simply overrides an earlier one carrying the same value).

**Item 2's cost accounting is one cost short, and it is now two.** *"The honest cost is the `jail_env`
conditionality alone"* was written before the subset's bind-host rule existed. The costs are the
`jail_env` conditionality (`PULSE_SERVER`/`PIPEWIRE_REMOTE` go through the `env` kind and become
**unconditional** — §3.1's named cost, OQ-LP5's trigger) **and** the home-relative bind constraint (the
sockets, above). `/dev/snd` is deliberately **not** re-declared: the bundled loophole already passes it
through, and a duplicate `--device` is unobservable on the mandated verification loop, so it could only
break on a real desktop — item 3's rule applied rather than restated.

**And a defect this pack made reachable, worth recording because the shape recurs.** The lazy
resolver that backs the three census surfaces which never stage (config validation, `yolo loopholes
list`/`status`, `yolo check`) **skipped every embedded pack**, under a comment saying an embedded pack
ships no loophole. `audio` is the first that does. Measured with it selected: `yolo loopholes list`
omitted `audio-alsa` entirely, and config validation warned *"no loophole named 'audio-alsa' is
installed on this machine"* at **every launch** — the same sentence a user gets when a pack genuinely
failed to stage, so the one case the warning fired on was the case where nothing was wrong. The answer
the old comment did not reach for is that `packload.Embedded()` materializes the tree (cached, once per
process). **Found by running the freshly-built binary, not by reading** — the defect is invisible to
every test that stages.

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

   **Landed 2026-08-14** — `run/loopholeinert.go`, one mechanism as required: the backend axis and
   §3.1's `platforms` declaration render through one function, with the platform half read from
   `loopholedecl.PlatformsUnsupportedReason` rather than re-matched (two matchers over one
   declaration is how a report and a gate come to disagree). It hangs off the same spawn boundary
   as §4.3 G4's exec disclosure — `startLoopholesDisclosed` — because everything a user must know
   before host code runs, *or before concluding that it did*, belongs at one place.
   **One rule the requirement did not state: backend beats platform when both apply.** An inert
   backend starts no host service whatever the platform says, so the platform answer would be a
   second reason for one outcome — and the actionable line is "switch backends", not "get a
   different machine". An unreadable manifest prints nothing here, since the discovery layer's
   warn-and-continue contract already reports that same file and a second complaint would read as
   a second bug.
   **"One mechanism" is now literal, not just intended.** The platform producer landed
   independently in the same batch as a VALUE type (`loopholes.InertNote`, with `Axis` and a
   single `Line()` rendering) and **zero callers** — it was built expecting a backend producer to
   plug in. So the backend half constructs `InertNote{Axis: AxisBackend}` and renders through the
   same `Line()`; nothing in the run pipeline formats its own inert sentence. Had each side kept
   its own rendering, the result would have been exactly the two half-messages §3.1 forbids, with
   one of them unreachable.

---

## 9. Risks and open questions

### Risks

**R1 — the static-data invariant inverts in spirit while staying true in letter.**
[`pack-system.md`](pack-system.md) §12's first invariant is *"the manifest stays static data — every
claim readable without executing anything."* A declared argv **is** static data, so the sentence
stays literally true. But its spirit was "reading a manifest tells you everything and costs you
nothing", and now: reading the claim is safe, *selecting* it is host execution. **That sentence must
be sharpened in the same commit that adds the kind.** `program` already bends it in-jail; a loophole
breaks it on the host. — **Done 2026-08-14**: sharpened in `pack-system.md` §12, which now says the
cost of *looking* is unchanged and the cost of *selecting* is not, and records the old sentence so the
change is legible rather than silent.

**R2 — nothing here has run.** No pack-shipped loophole exists. The two places a first
implementation is most likely to be wrong are still orderings rather than shapes: the **seven-surface
discovery convergence** (§5.1) and the **publish-after-upstream** ordering (§2.1b) — and the second
now has a second failure mode (stale socket) that draft 1's fix did not cover.
**MOSTLY CLOSED 2026-08-14, and what is left is one clause of the original sentence.** Both orderings
shipped with tests (the convergence structurally, the readiness path with deadline-bounded round
trips), so the risk is no longer "these two are probably wrong". And a pack-shipped loophole now
EXISTS: item 9 landed as `packs/audio` (§7), so discovery, selection, the subset loader, the claim
enumeration, the name pre-flight, the inert report, the container argv and teardown are all exercised
by a real instance rather than only by fixtures — and building it found two live defects that no test
had (§7's lazy-resolver omission, and the subset finding).
**The residual, stated precisely:** the `audio` pack declares **no `host_daemon`**, so no pack-shipped
loophole has yet been *approved at a prompt* and *spawned as a host daemon*. The gap is now the
FETCHED-and-executing path, not the kind: an embedded pack carries yolo's own authority, so its
approval is true by construction and never reaches `promptYesNo`. Closing it needs a fetched pack with
a daemon, which is a test-fixture-shaped task rather than a design one.

**R3 — the front's limits are invisible to the daemon author.** No EOF propagation (§2.1b hazard 2)
and no per-request access log (hazard 3). The first turns a working daemon into a hang; the second is
an audit gap that only shows up when someone asks what a jail requested. — **Half closed 2026-08-13**:
EOF propagation is now a declared `request_end`, so the hang is one manifest word away rather than
invisible, and the guide says so. The audit gap stands at its honest ceiling (connection-level).

**R4 — `pack footprint`'s review tail conveys nothing for this kind.** `reviewSummary`
(`pack.go:945-960`) counts by kind, so a loophole reads `1 loophole`. For host execution the count is
the least interesting fact. Either it special-cases or the summary changes shape. (And per §3.3, that
command cannot see a fetched pack at all.) — **Fixed 2026-08-14** by the first option: executions are
counted separately from the by-kind tail and printed **first**, named for what they do
(*"N RUNNING CODE ON YOUR MACHINE"*), because that outranks every read in the list. The per-claim lines
carry two markers for the same reason (`⚠ RUNS CODE ON YOUR MACHINE` versus `⚠ review`). The
fetched-pack half of the parenthesis is unchanged.

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

**Status 2026-08-14, item by item, because they closed at different times.**

- The five omitted keys: **fixed** — the guide's schema block carries all of them, plus `publishes`,
  `request_end` and now `platforms`, and it names the second half of the census as *"every key with a
  host-side effect"* so the omission cannot recur unnoticed.
- `description` marked required: **fixed** — the guide states that only `name` is required.
- `knownHostServiceKeys`: **fixed** — it is now
  `set("command","env","jail_socket","jail_endpoint","doctor_cmd","description")`, so the canonical
  validator and the loader agree about which keys exist. The `jail_endpoint` contradiction went with it
  (`jail_endpoint` is the canonical override and `jail_socket` an accepted alias).
- `src/loopholes.py`: **STILL PRESENT** — `internal/loopholes/runtime.go`'s `SetEnabled` writes
  `// yolo-jail loophole manifest. See src/loopholes.py for schema.` into every manifest it toggles,
  and that file has not existed since the Go port. The correct reference is now
  `internal/loopholedecl`. It is a one-line fix in a `.go` file and is the last live row of R5.

**R6 — the claim-count grows the prompt, and a long prompt is a skimmed prompt.** With the total
enumeration (§3.3) an audio-shaped pack emits **four** claims (counted from
`bundled_loopholes/audio/manifest.jsonc`: three `host_bind_mounts` — two of them sockets, so the IPC
class — plus `/dev/snd`; no daemon and no intercepts) and a proxy-shaped one emits three or four.
That is honest, and it is also the shape people click through. Nothing here solves it; grouping by
loophole in the display while keeping per-claim strings in the lockfile is the obvious mitigation and
is a display concern, not a model one. — **Still open 2026-08-14**, and the count is now confirmed
against the shipped producer (the two `readonly: false` binds land in the IPC class, `asound.conf` in
MOUNT). What did improve is legibility rather than length: R4's two markers and the exec-first tail
mean the *one* line that matters in a long list is the one that stands out.

### Decision Ledger

Compacted 2026-08-23. Every ID below keeps its exact spelling — they are cited from
`internal/packsrc/lock.go`, `loophole-packaging-overview.md` and `loophole-activation.md`. The verbose
entries survive under "Open questions" below, because several carry refuted objections and measured
traps that are load-bearing rationale rather than history.

| ID | Ruling / Decision | Date | Settled in |
| :--- | :--- | :--- | :--- |
| OQ-LP1 | Schema lives in a new `internal/loopholedecl` leaf; `internal/loopholes` re-exports it as type ALIASES — resolves the `packload`→`loopholes` cycle by extraction | 2026-08-14 | §3.2 |
| OQ-LP2 | Install-shaped `loopholes` keys (`command`, `doctor_cmd`, `env`) are user-scope-only; migration is a **FATAL** error + a TTY-gated install offer, not warn-then-error | 2026-08-14 | §4.3b, §4.3 G1 |
| OQ-LP3 | Folded into OQ-LP13 — install confirms every origin, so there is no trusted-`file://` bypass to special-case | 2026-08-14 | §4.3a |
| OQ-LP4 | The front is declared by `publishes` on `host_daemon`, never by a manifest naming `yolo internal front` in its own argv (workaround-becomes-API) | 2026-08-14 | §2.1 |
| **OQ-LP5** | **LIVE** — does `jail_env` stay refused for pack-shipped loopholes? | — | below |
| OQ-LP6 | Build the capability system (A6) — a loophole manifest is a public surface regardless | 2026-08-14 | §6 |
| **OQ-LP7** | **LIVE** — does `guest` get its own field census, or keep borrowing `HostFields()`? | — | below |
| **OQ-LP8** | **LIVE** — how does an execution approval survive a moving pin? G2b unbuilt; `LockEntry.Commit` written but never read at launch | — | below, §4.3 G2b |
| OQ-LP9 | Nested jails RECURSE the scope model: inner-scope census + two generated per-consumer files + a global `--user-layer` flag. Built | 2026-08-14 | §9 |
| OQ-LP10 | Retire the user loopholes dir — a `file://` pack subsumes it. Carried out; `SourceUser` deleted, migration notice left behind | 2026-08-14 | §1, §5.1 |
| OQ-LP11 | Bundled loopholes become packs. **COMPLETE** — `bundled_loopholes/` deleted, all five shipped loopholes are pack contributions | 2026-08-19 | §5.4, §7, below |
| OQ-LP12 | Dissolved by §4.3b's scope model — install once at user scope, each workspace enables | 2026-08-14 | §4.3b |
| OQ-LP13 | Ruled AGAINST hashing: *"if you can edit user-level files, you have all the perms already."* The user-scope edit IS the confirmation; only a PLACEMENT rule survives, and it must EXEMPT yolo's own shipped content | 2026-08-14 | §4.3a |
| OQ-LP14 | Runtime-dir sockets: the bind-host path rule is **WITHDRAWN, not extended** — its cases are inverted (admits `~/.ssh`, refuses a pulse socket). What survives is a correctness rule, not a gate. This is what unblocked OQ-LP11 | 2026-08-17 (built 08-18) | §3.1, §7, below |
| OQ-CAP2 | Closed with option (B) — write the packaging design before building A | 2026-08-13 | header |

### Open questions

Three are live: **OQ-LP5**, **OQ-LP7**, **OQ-LP8**. The rest are kept in place, stamped, because
their refuted objections and measured traps are documentation.

**OQ-LP1 — where does the loophole manifest schema live? ✅ RESOLVED BY EXTRACTION, 2026-08-14.**
`packload` cannot import `internal/loopholes` (cycle, measured §3.2), and the footprint needs to read
the daemon argv. The recommendation was taken: `internal/loopholedecl` is the schema as a
stdlib+decoder leaf (`json5`/`jsonx`/`pytext` only), `internal/loopholes` reads through it and
re-exports the vocabulary as type ALIASES so there is one `HostDaemon` type rather than two that
happen to match, and the `loopholes` → `config` edge is untouched. The payoff the recommendation
predicted is what `packload.LoopholeHostAccessClaims` uses: the schema is readable by the footprint,
by `pack lint` and by a host-side validator without dragging the runtime predicates along.

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

💬 **OQ-LP5 — does `jail_env` stay refused for pack-shipped loopholes?** §3.1 refuses it to avoid a
cross-kind collision pass, at the cost of conditional env. **The cost is no longer hypothetical: the
shipped `audio` pack pays it** — `PULSE_SERVER`/`PIPEWIRE_REMOTE` are declared through the `env` kind
and are therefore set on every launch that selects the pack, including on a machine where no socket
ever crossed (§7). ~~It is *not* the only remaining cost of that example any more; the bind-host
constraint is the other, and it is OQ-LP14.~~ **Corrected 2026-08-23: it is the ONLY remaining cost
again** — OQ-LP14 withdrew the bind-host path rule on 2026-08-17, so the sockets became expressible
and this is what is left. The alternative is the collision pass, which is purely additive.

**What the answer decides:** whether a pack can ever set an env var *conditionally on a loophole
actually activating*, or whether "the pack is selected" stays the only granularity yolo offers. Every
future pack whose loophole is predicate-gated inherits this.

_Leaning:_ **keep the refusal.** `audio` wants conditional env and tolerates the unconditional form,
which is the whole evidence base — a cost one consumer absorbs is not yet a reason for a
cross-kind collision pass. Revisit at the first pack that CANNOT absorb it.

**Answer:**
> _(empty — fill in when decided)_

> **✅ RESOLVED 2026-08-17 — the path rule is WITHDRAWN, not extended.** The vocabulary was never the
> problem: the rule admits `~/.ssh` and refuses a pulse socket, so its two cases are inverted. Claim
> enumeration plus the approval already do the work; what survives is a correctness rule (the approved
> string must resolve to the bound path), not a gate. Full reasoning at the OQ-LP14 entry in
> [`loophole-packaging-overview.md`](loophole-packaging-overview.md). `audio` needs no new vocabulary.

✅ **OQ-LP14 — the subset has no vocabulary for a RUNTIME-DIR SOCKET — RESOLVED 2026-08-17, BUILT
2026-08-18, and the ruling went AGAINST the leaning below.** Raised 2026-08-14 from building the
pack. **The vocabulary was never the problem: the bind-host path rule was WITHDRAWN, not extended.**
Its two cases are inverted — the rule admits `~/.ssh` and refuses a pulse socket — so it was
protecting nothing while blocking the one legitimate case. Claim enumeration plus the approval
already do the work; what survives is a **correctness rule** (the approved string must resolve to
the bound path), not a gate. Consequence, and it is why this entry is load-bearing rather than
archaeology: withdrawing the rule made the sockets expressible, which made `bundled_loopholes/audio`
DELETABLE, which freed the reserved name `audio` — because the reservation set was DERIVED from the
bundled directory rather than listed beside it. **That is the domino that let OQ-LP11 finish on
2026-08-19.** The original entry follows unedited, because the leaning it argues for is the tempting
answer and the reason it was rejected is the useful part.

> **⚠ Do not re-derive "add a declared, enumerated runtime-socket vocabulary."** It was the leaning,
> it was ruled against, and the withdrawal is strictly simpler. Adding vocabulary to a rule whose
> cases are inverted keeps the inversion.

**OQ-LP14 (original entry, 2026-08-14) — the subset has no vocabulary for a RUNTIME-DIR SOCKET, which is the commonest non-home
host path there is.** §3.1's requirement 1 admits
`{loophole_dir}/…` and home-relative paths and refuses everything else, correctly (see the argument
there). Measured consequence: `${XDG_RUNTIME_DIR}/pulse/native` has **no legal spelling** — `$VAR` is
refused, the literal is refused as absolute, and it is not under `$HOME` — so the socket half of
`audio`, the loophole this design nominated as its own dogfood, is inexpressible for a pack (§7). The
section's own fallback (*"a `host_daemon` that mediates"*) means shipping an audio proxy, i.e. reaching
for arbitrary host execution to obtain a `:ro` bind, which inverts the risk ordering the whole design
rests on. **My read: add a declared, enumerated runtime-socket vocabulary** — yolo resolves the
session runtime dir, the manifest names only the socket within it, and the claim is emitted in the
existing host-IPC class so the approval string stays machine-independent. **Do NOT widen the path
rule**, which would admit `${HOME}/.ssh` and `${XDG_RUNTIME_DIR}/../../etc` with it. **Resolved by:** a
maintainer ruling; nothing shipped depends on the answer, and the guide states the limit meanwhile.

**OQ-LP6 — is A6 still worth building for three bundled loopholes? RULED: YES, build it.** The
extension-point argument carries it — a loophole manifest is a public surface regardless, so `serves`
is a field third parties will write, and designing it once now beats retrofitting it later. With selection as the mechanism,
`pack-capabilities.md` applies only to the bundled set (§6). **Resolved by:** a maintainer ruling.

💬 **OQ-LP7 — the `guest` notch.** A loophole is coherent at `guest` and incoherent at `host`, but
`Target.Fields()` funnels both into `HostFields()`. This kind is the first case where that funnel is
wrong for a *reason* — and note §8's finding that macos-user is inert **today**, which makes the
question less hypothetical than draft 1 treated it.

**What the answer decides:** whether `guest` gets its own field census or keeps borrowing the host's.
It blocks nothing shipped (macos-user starts no host services at all), but it determines the shape of
the first macos-user loophole rather than being discovered by it.

_Leaning:_ split the census when Phase 7 lands and not before — the funnel is wrong for a reason, but
inventing a third field set with zero consumers is how the vocabulary grows faster than the system.

**Answer:**
> _(empty — fill in when decided)_

💬 **OQ-LP8 — how does an execution approval survive a moving pin without re-prompting forever? STILL
OPEN, re-verified 2026-08-23.** §4.3
G2b anchors an exec-bearing approval to the commit, giving `ApprovedAt` its first reader. The cost is
that a `?ref=main` pack re-prompts on every commit. Alternatives: fold a **digest of the loophole
module dir** into the claim string (re-prompts only when the daemon's own files change — more
precise, more machinery), or document tag-pinning as the supported shape for exec-bearing packs.

> [!WARNING]
> **Header revision 3 said this was "all but closed" by OQ-LP13. It is not closed, and the gap is
> still live in the code.** Verified 2026-08-23: `LockEntry.Commit` exists and is WRITTEN at install
> and update time (`internal/cli/pack.go:1110`, `:1129`, and read back for display at `:1335-1338`),
> but it is **never consulted at launch** — `rg Commit` across `internal/cli/run/`,
> `internal/packload/` and `internal/packstage/` returns nothing. `HostAccessApproved`
> (`internal/packsrc/lock.go:82-96`) **compares claim STRINGS only**, and its own doc comment says
> so: *"never the commit the approval was granted against."* The `ApprovedAt` field was deliberately
> NOT added rather than left unused (`lock.go:60-70`), because a field nothing reads is a guarantee
> that was never there (`gate-placement-principle.md`, "The artifact form"). The hole is pinned by
> an assertion instead — `TestHostAccessApprovedComparesClaimStringsOnly` — so it fails if the
> behaviour changes silently. **The gap in one sentence: a fetched pack at a mutable ref whose daemon
> FILE changes under an unchanged argv re-installs with no prompt** (§4.3 G2b).

**What the answer decides:** whether yolo's host-execution approval is content-anchored or
argv-anchored. Argv-anchored is what ships. It is the last unbuilt item of the landing order and the
only invariant in §4.3 that is a maintainer decision rather than pending work.

_Leaning:_ **commit anchoring now, digest later if the friction is real** — the friction is visible
and recoverable, and content-blind approval is neither. Unchanged since 2026-08-14; OQ-LP13's
placement rule narrowed the exposure (the user-scope edit IS the confirmation) but did not remove
this row, because a *fetched* pack's module dir is not user-scope-edited.

**Answer:**
> _(empty — fill in when decided)_

**OQ-LP9 — NESTED JAILS NEED THE SCOPE MODEL TO RECURSE. ✅ BUILT 2026-08-14** (all three parts; the
Landed note is at the end of this entry). It was the last properly open question, and review grew it.
The original form was small (does the scope error downgrade in-jail, the way `agents` does). Reframed:
*"for jail in jail, the outer jail is essentially 'user level' for the inner jail — we need to support
this somehow."* That is §4.3b applied recursively, and it is right: "user level" is whatever scope owns
the machine the daemon runs on, so inside jail A that is jail A's own config, owned by jail A's agent,
because jail A is the blast radius. It is also **load-bearing**, since §4.3a's escape-hatch ruling
sends loophole development into a nested jail.

**Measured in-jail 2026-08-13, then REVISED on maintainer follow-up — the raw bind is the wrong
method.** Facts first: the file is a single-file `:ro` bind of the human's real config
(`userConfigMountArgs`, `assemble_parts.go:530-560`, `ROFileMountArg` at `:557`) into a jail-owned
writable directory, so writing beside it is jail-local — keep that property. And it serves more than
nesting: `yolo check`, `yolo loopholes list` (`loopholescmd.go:60-82`) and `yolo pack` read user
scope in-jail on every setup.

**Why raw inheritance is wrong** (maintainer: *"mounts never work inside the jail, and other things
won't apply — maybe some things will?"*): the bind carries keys whose meaning does not survive the
boundary — `cache_relocations` targets and loophole socket paths that do not exist in the container
(so in-jail `yolo check` evaluates a world with the referents absent), and `host_files`, whose
"host home" silently rebinds to the jail's own. Worse, only `config.jsonc` + `config.lua` cross;
`include_if_found` files do not (the loop at `:547` mounts exactly two names) — so the in-jail user
scope is neither the effective config nor a designed subset. The raw bind already filters, by
accident.

**DESIGN SETTLED — the maintainer's three-part split, which supersedes both of my proposals** (the
one-big-filtered-snapshot AND `config.local.jsonc`): *"write the file 'yolo check and loopholes'
needs, and put a comment saying that's the case; set whatever runtime is needed for jail-in-jail and
mark that as why; and the yolo CLI has a CLI arg that allows a layered-in file like it was
user-level."* Split by CONSUMER, not by key class:

1. **The preflight file** — generated for the in-jail readers that exist (`yolo check`,
   `yolo loopholes list`, `yolo pack`; census sites 5–7 in §5.1), carrying only keys they evaluate
   meaningfully in-jail, header comment naming purpose/generator/launch time. Kills the false-error
   class: `cache_relocations` is not in the file, so in-jail check never evaluates it.
2. **The nested-launch input** — the keys an inner launcher composes a jail from (`packages`,
   `packs`, mise), written ONLY where nesting is possible and marked as existing for that reason.
   Non-nesting backends get nothing, by construction.
3. **`--user-layer <file>`** (name TBD) — layer a file at user-level precedence, explicitly, per
   invocation. Replaces `config.local.jsonc`, WITHDRAWN with cause: a conventionally-named
   auto-merged file is the `include_if_found`/`overrides.jsonc` accident one notch more designed —
   it activates because a file exists, invisibly. The arg is visible at the call site, testable,
   inert unless passed. Safe everywhere by gate-placement Test 1 (passing an argv requires command
   execution, which exceeds anything the arg grants). This is the nested-development path: the
   in-jail agent writes a layer file in its own home and passes the arg.

Both generated files are renders of the ONE computation `yolo config dump` (`configdrift.go:77-85`)
already produces, so they cannot drift from the source; the census survives as two named
per-consumer manifests with a drift test (a new key must be assigned to files or explicitly to
neither). Launch-frozen, which is the jail's normal contract; `config drift` covers staleness.
Testing (maintainer: *"tricky to test completely, but worth it"*): golden renders per file + one
integration case per false-error class; the full nested matrix stays a by-hand per-release check.

**LANDED 2026-08-14 — all three parts, as `internal/config/inherit.go` (the census + the filter),
`internal/cli/run/inheritscope.go` (the delivery) and `--user-layer` (the flag). Four things the
specification got wrong or left open, each corrected by measurement rather than by judgement.**

**(1) `cache_relocations` is the WRONG example for the false-error class, and this section used it
twice.** The design named it as *the* named class. Measured with a real in-jail `yolo check
--no-build`: `cache_relocations` and `host_files` were **already silent in-jail**, each hand-patched
with a `!inJail()` argument (`internal/config/validate.go`'s `checkCacheRelocations` call and
`hostfiles.go`'s `checkHostFiles` call, plus `LoadCacheRelocations` returning nil in-jail outright).
The classes that were **still live**, and that nobody had noticed, are three others:

| key | what an in-jail `yolo check` actually printed |
|---|---|
| `gpu` | **four fails** — `nvidia-smi` / `nvidia-ctk` / `runc` "not found", "No CDI spec" — about a host GPU the human had configured correctly |
| `mounts` | one WARN per entry: *"host path does not exist and will be skipped"* |
| `env_sources` | *"env_sources file not found, skipping"* per host dotenv path |

**This STRENGTHENS the ruling rather than weakening it**, and that is the reason to correct the
sentence rather than annotate it: a hand-added `!inJail()` guard fixes the *incident* someone
complained about, and a census fixes the *pattern*. Only two keys had ever been patched, one at a
time, over the feature's whole life — while three louder classes sat unguarded. Under the census the
keys are not in the file at all, so no section can evaluate them, and the two pre-guarded keys are
covered by the same test as a **regression** (a future refactor that reasonably deletes a now-redundant
guard cannot revive the false error). Each class has a control asserting the check *does* report it
when the key is present, so the silence is provably the filter's doing.

**(2) FIVE KEYS EARN BOTH MEMBERSHIPS, measured not judged** — and the first pass got them wrong by
reasoning from the shape of the key. `security`, `mise_tools`, `mcp_servers`, `mcp_presets` and
`lsp_servers` are in **both** files, because `yolo check`'s entrypoint dry-run
(`check/entrypoint.go`) feeds exactly these into a temp home as
`YOLO_BLOCK_CONFIG`/`YOLO_MISE_TOOLS`/`YOLO_LSP_SERVERS`/`YOLO_MCP_SERVERS`/`YOLO_MCP_PRESETS` and runs
the real generators over them, while the run pipeline feeds the same five to a real container
(`assemble.go`). One consumer VALIDATES, the other COMPOSES: both memberships are *earned*, and a
nested launch that dropped them would silently lose the human's MCP servers and blocked-tool shims.
Same for `journal` and `host_processes` (two reserved loophole names carried as top-level keys). The
lesson for the split: "in one file" must not be allowed to mean "in both" by default, so the
distinctness assertion moved to a key that is genuinely preflight-only (`agents_md_extra`, prose for
this jail's own briefing) — without such a case, nothing would notice the two filters collapsing into
one.

**(3) A DEFECT ONLY THE REAL NESTED RUN CAUGHT: the host wrote the nested-launch file and NOTHING READ
IT.** R2's file was generated correctly and consumed nowhere, so `packages`, `env_sources`,
`resources` and `network` reached a jail and stopped there. Measured across two real nesting levels:
the file at **depth 2 had LOST `packages` and `env_sources`** relative to depth 1, because depth 1's
effective config never contained them. That is exactly the *"a rule changes with nesting"* failure R6
forbids, and it made R2's whole file decoration. Fixed by folding the inherited file into the
user-scope read (`applyInheritedLaunch`, **under** `config.jsonc` — what the outer scope handed down
loses to the more local statement, the same direction as user-under-workspace one level up), and after
the fix both generated files are byte-identical at depth 1 and depth 2. **Two unit tests passed while
this was broken**, which is the second time in this batch a thing was built and not wired (the first
is §4.3 G3's origin gate) — the pattern is *the value was computed correctly and then not consumed*,
and it is why the nested run is mandatory rather than advisory.

**(4) The layer arg is GLOBAL, consumed in `Main` before `RewriteArgv`.** The design said "a CLI arg"
without saying where. It had to be global because **four** commands read user scope in-jail — `run`
(so a nested launch composes from the layer), `check` (so an agent can verify what it just wrote),
`loopholes` (so a freshly-declared loophole is listed) and `pack` (so a pack named only in the layer
resolves). A flag reaching only `run` would change a launch but not the command you verify it with,
which sends an agent hunting for a bug in the feature instead of reading the flag it forgot to pass;
and four flag parsers would guarantee the fifth command forgets it. It is stripped before
`RewriteArgv` (or it would look like a leading positional and make `yolo --user-layer x -- bash`
resolve as an unknown command) and after the help branch (so `yolo --help` answers without touching
config), and it stops at `--` so an inner command's own `--user-layer` is left alone. It is published
through an env var rather than a threaded parameter because it must reach the three readers that
deliberately bypass the merged config and read the user file directly (`LoadPacks`, `LoadHostFiles`,
`LoadCacheRelocations`) — a parameter would reach only the ones someone remembered to update, and a
layer that silently failed to carry `packs` would be worse than no flag.

**R5, the nested-development path, is VERIFIED END TO END in a real nested container** — which is what
turns §4.3a's *"develop the loophole in a jail"* ruling from an argument into an instruction. An in-jail
agent wrote a pack shipping a loophole, wrote a layer naming it, and `pack install`, `loopholes list`
(active, `pack/none`), `check` (*"devpack: 2 file(s) stage"*) and a nested launch all saw it. **One
constraint users need, and it is the first thing anyone will trip over: the jail's home ROOT is `:ro`,
so `mkdir ~/mypack` fails.** The pack must live somewhere writable — `~/.local/share/…` (an rw anchor)
or the workspace. That is jail-home policy ([`jail-home.md`](jail-home.md)), not a limit of the flag,
and the layer file itself lands fine because `~/.config` *is* writable — which is R8's single-file
delivery doing exactly its job. `yolo config-ref` carries the copy-pasteable sequence.

**R7 could not be TAUGHT user-half drift, so it NAMES the scope it cannot compare.** `config drift`
compares the workspace config against the jail's boot baseline; the user half is now structurally
incomparable from inside a jail — what a jail sees as user scope is a *generated* render, so there is
no host file in here to diff and diffing the generated file against itself answers a question nobody
asked. Before this change the host's real `config.jsonc` was bind-mounted live, so a user-half edit was
visible instantly and drift never came up. So the command prints the limit **in-jail only**, on **both**
the in-sync and drifted paths (the limit is a property of the command, not of the result), and **exit
codes 0/3/4 are untouched** so an agent keying on them keeps working. Saying it matters because
"In sync" with no caveat invites an agent to conclude nothing changed anywhere and then debug a stale
`packs` list as if it were a code problem — the same over-claim shape this batch fixed elsewhere.

**Also landed with it, and not in the specification:** `include_if_found` **content** now crosses into
a jail. The old mount loop named exactly two files, so a user whose config opens with
`include_if_found: ["overrides.jsonc"]` had the included half stay host-side — the in-jail "user scope"
was neither the effective config nor a designed subset. Rendering from the *effective* config fixes it
for free, because the includes are already merged by the time the filter runs. The asymmetry is
deliberate and pinned: the include's **content** survives, the **directive** does not (re-resolving a
host-relative sibling path inside a jail would hunt for a file that is not there).

**Resolved by:** nothing — it was built as specified, with the four corrections above.

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

**OQ-LP10 — retire the USER LOOPHOLE DIRECTORY once a pack can carry one? RULED: YES — ✅ CARRIED
OUT.** After the kind ships so there is somewhere to go. Raised in review: *"what's
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

**SHIPPED.** `Discover` and `ValidateLoopholes` no longer walk the directory, the `SourceUser`
constant is deleted (ordering is now bundled < pack < config), and `SetEnabled` — the manifest
rewriter that existed only to serve it — is deleted with it. `resolve()`'s default source label moved
from the permissive `user` to the FAIL-SAFE `pack`: an unlabelled record now cannot run host code
without a recorded origin gate, and is judged rather than exempted by the placement rule.

**Migration is loud, because it has to be.** Whatever sat there was running a host daemon until the
upgrade that removed the channel, so a silent drop was never acceptable. A populated directory
produces one stderr notice per process from discovery, plus a graded `yolo check` row, both naming the
directory, every stranded module, and the exact `mv` + `pack.json` for the conventional local pack
(`~/.config/yolo-jail/local/`, implicitly selected). The notice also states the one thing that can
still fail after a correct move: a pack's loophole is held to the pack-shipped subset, so `jail_env`,
an absolute or writable bind host, and `publishes: "endpoint"` are refused at load.

**One consequence worth recording, since it was not obvious before doing it.** `user` was the only
source label that was BOTH trusted to run host code AND judged by the placement rule. With it gone,
the module-dir face of the placement rule applies to PACK loopholes only — bundled content is exempt
(§4.3a's Test-1 reasoning) and a config entry has no module dir. That is not a weakening: the retired
directory's manifests are not read at all now, which is strictly stronger than judging where they sat.

**The first payoff below is only HALF collected, and deliberately so** — see §5.2's ledger.
`CmdSetEnabled` no longer has a special case, but it PRINTS the config key rather than writing it,
because writing it means a comment-destroying read-modify-write of the user's hand-written
`config.jsonc`.

**Cost, corrected — review asked "isn't this de facto the local pack now shipping a loophole?" and
the answer is yes**, which makes the migration cheaper than draft 3 priced it: the conventional local
pack is `Implicit: true` (`config/packs.go:275`), so a loophole moved into it is discovered with no
config edit and the drop-a-directory-in ergonomics survive. **But it inherits implicit selection**,
so retirement does NOT fix the no-selection-step objection above — what improves is that a
pack-shipped loophole emits claims and reaches `notePackHostAccess`, where the home directory prints
nothing. Visibility, not gating; say so rather than overclaiming.

✅ **OQ-LP11 — do BUNDLED loopholes become official packs? RULED: YES — COMPLETE 2026-08-19.**
The prize this entry names — *"`AGENTS.md`'s 'AGENTS ARE PACKS. Core does not know what an agent is'
becomes true of loopholes too"* — is **collected**. Verified 2026-08-23: `bundled_loopholes/` does not
exist; all five shipped loopholes are pack contributions. The four-commit finish, in order, each
closing the blocker this entry priced:

| Commit | Date | What it closed |
|---|---|---|
| `3d5805cd` | 2026-08-19 | `packs/host-processes` — the blocker was §5.4 reason 3, already graded ❌ (an official pack is embedded in the same binary, so `cmd/yolo-ps` is no obstacle). It *"goes dark until you ask"*, after `55c18ed4` moved allowlist resolution to launch time, in core. |
| `ea6f5e5b` | 2026-08-19 | the two audio loopholes become ONE, in the pack, **under the plain name** — unblocked by OQ-LP14's withdrawal freeing the reserved name. |
| `7df7c5aa` | 2026-08-19 | `host_daemon.scope: "host"` + **`internal/brokerrelay` deleted** — the two halves of this entry's *"the broker still waits on its stated blocker"*. |
| `e391d0f5` | 2026-08-19 | the broker moves into `packs/claude`; `ReservedLoopholeNames`, `ReservedName` and the pack-vs-reserved branch of the pre-flight all deleted with it. |

**The sequencing prediction below was RIGHT, and it is worth keeping for that reason:** the entry
called `audio` the low-stakes first consumer of implicit selection, and said the remaining half
depended on OQ-LP14. Both held — OQ-LP14 resolved 2026-08-17, and the rest followed two days later.
`journal` and `cgroup-delegate` were never in this entry's scope (they were Go functions the run
pipeline called by hand, not bundled modules) and became packs in the same sweep.

> [!WARNING]
> **What replaced the reservation, because there is no list to add a name back to.** `packs/claude`
> OCCUPYING `claude-oauth-broker` is the protection — loophole names are sole-owned across packs,
> **fatally** — plus the origin gate. And the broker is a **contribution of `packs/claude`, not a
> pack of its own**: a second pack would reinstate the selection step, i.e. hand back the default-on
> failure §5.4 reason 1 exists to prevent (`loophole-activation.md` OQ-A10).

**Original entry, 2026-08-14 (first step): `audio` shipped IN this
batch.** §7's example is promoted from a doc artifact to a
deliverable, which costs a relabel and buys the consolidation immediately. The broker still waits on
its stated blocker. Raised in review: *"why not a real pack? it
can still come from a built in shipped namespace or whatever."* §5.4 re-grades draft 2's three
reasons and only one survives — the broker's manifest is not what runs, so packaging it is ceremony
over a thing that ignores the package. The `host-processes` reason was simply wrong (an official pack
is embedded in the same binary), and the auto-activation reason is a *requirement* that an implicit
official pack could also meet.

The prize is that `AGENTS.md`'s *"AGENTS ARE PACKS. Core does not know what an agent is"* becomes
true of loopholes too: one channel, one loader, no registry, and `internal/loopholes` stops being a
thing core knows about specially. **My read: yes in principle, and not yet** — do `audio` as an
official pack first, and leave the broker where it is until `BrokerSpawnArgv` and `ensureBrokerRelay`
have manifest vocabulary. That sequencing also gives the "official pack that is implicitly selected"
mechanism a low-stakes first consumer. **Resolved by:** a maintainer ruling; nothing in this design
blocks either answer.

**What LANDING it established, and it is not the relabel this entry priced.** The pack is **ADDITIVE**,
not a migration: `bundled_loopholes/audio` is KEPT and untouched, the pack's loophole is named
`audio-alsa` because the plain name is RESERVED and claiming it is a fatal launch refusal, and it binds
a `conf.d` fragment rather than `/etc/asound.conf` because podman refuses two binds on one destination
— a jail with both would refuse to start (§7 carries the measurements). So **this step does not
consolidate anything**: it adds a fourth channel's first inhabitant beside the bundled one rather than
replacing it. Full consolidation still needs the bundled copy to be *removable*, which needs OQ-LP14's
runtime-socket vocabulary — the pack cannot express the sockets, so it cannot replace what it sits
beside. That is a real reordering: OQ-LP11's remaining half now depends on OQ-LP14, where before it
looked independent.

---

## What must land together

Ordered, because the first three make the rest safe to read. Items **0**, **5b** and **5c** are new
in revision 2 and are real work draft 1 priced at zero.

**Ledger, 2026-08-14: EVERY ITEM IS DONE — 0 through 10.** Item 9 (`audio` as a real official pack)
and item 10 (OQ-LP9's three parts, added to this list when it was built) both landed. ONE residual
sits *inside* a done item and is the whole of what is left: **G2b** is unbuilt — deliberately, as a
decision under OQ-LP8 rather than as pending work (item 6). **G2a landed** with the loophole claim
producer, and the pack-shipped **subset is wired at three seams** (item 5).

**Two findings from the last two batches are NOT work items and must not be read as residuals.** Each
says something about the design that no amount of implementing closes: **OQ-LP14** — the subset has no
vocabulary for a runtime-dir socket, so `audio`'s own reason to exist is inexpressible for a pack
(§3.1, §7); and the **bundled exemption** on the placement rule, which is the rule's own Test-1
reasoning applied to yolo's own content (§4.3a, item 1a). The first needs a ruling; the second is
settled and shipped.

**Five defects found by adversarial verification of the landed kind, all fixed 2026-08-14.** Every one
had the same shape — *the security decision was computed correctly and then not enforced* — which is
worth recording as a pattern rather than five incidents:

1. **`ca_cert` was a crossing with no claim** (§3.3, on a key draft 1's table listed under "everything
   else is allowed"). `RuntimeArgsFor` bind-mounts it and joins the container path into
   `NODE_EXTRA_CA_CERTS`; the producer never mentioned it. So `{"transport":"none","ca_cert":"/abs"}`
   yielded ZERO claims, `packMayAccessHost` took its `len(want) == 0` branch, and a fetched pack got an
   arbitrary host path mounted into a UID-0 jail AND trusted as a CA by every node client in it, with
   no prompt. Now claimed (keyed by the raw path, naming the CAPABILITY rather than the mount) and
   path-scoped for a pack. Also: `Loophole.subsetManifest` projected three fields under a comment
   claiming the omissions would "fail loudly" — they fail SILENTLY, in the granting direction, which is
   how the field's own scope rule reported a violating record as clean. It projects everything now,
   pinned by reflection.
2. **The origin gate was computed and ignored at the spawn** (§4.3 G3). `HostExecApproved` had ONE
   production reader (`RunDoctorChecks`); `ManifestHostDaemonSpecs` and `RuntimeArgsFor` never
   consulted it, so an unapproved fetched pack's daemon RAN and its binds/devices/intercepts/CA reached
   the argv. `run/packs.go`'s comment — *"the SAME gate, not a second one that could disagree"* — was
   true of the decision and false of its enforcement. Both functions now refuse a `SourcePack` record
   outright and the gated forms are `Set` methods, which is `RunDoctorChecks`' own shape and for its
   argument: a slice carries no gate. A FOURTH surface had it too — the briefing advertised an
   unapproved loophole as a live capability (`Active()` is true for one), so it reads `Set.Honored()`.
   Discovery is unchanged, which is how "not discovered at all" and §5.1's visibility requirement are
   reconciled: nothing crosses, `loopholes list` still says `unapproved`.
3. **No refusal was printed, and the disclosure lied.** There was no `HonoredLoopholes` beside the
   three shipped `Honored*` reporters, so the withholding in item 2 was SILENT — worse than the `mount`
   case it was modelled on, since a missing mount is a missing directory while a missing loophole looks
   like a broken one. And the pre-spawn block printed the withheld daemon's argv under *"This launch
   runs pack code on your machine"*, because the footprint deliberately keeps that claim (it answers
   what a pack WANTS). Fixed at the disclosure, as a rule over host-crossing classes rather than a
   special case: `program via installer` and `briefing after host:` had the same latent defect.
4. **The subset was enforced NOWHERE.** `LoadPackLoophole` and `LoadDirPackShipped` had zero production
   callers — measured in the ledger below and left as a residual. Now the SOURCE LABEL selects the
   loader (`loaderFor`), and `pack lint` applies the subset too: it had printed "pack ok" for a
   manifest every launch refuses.
5. **Two unclaimed fields.** `broker_ip` produced `--add-host <host>:<ip>` and appeared in no claim, so
   two manifests differing only in it had IDENTICAL approvals — approve an intercept at
   `host-gateway`, then move the pin and redirect the hostname anywhere, no re-prompt. Folded into the
   intercept claim (it is not a separate crossing; it is where the intercept points). And
   `requires.file_exists` was an unscoped `$VAR`-expanded `stat` whose ANSWER is readable —
   `InactiveReason` prints the resolved path in `loopholes list` — so a fetched pack could probe
   `$HOME/.ssh/id_ed25519` and read the result. RULED: path-scope the field, do NOT claim it (a stat
   crosses nothing, and §3.3's rule is about crossings), and keep printing the path (the active/inactive
   label answers the probe anyway, so hiding it would remove the diagnostic and leave the probe).

**And one test hole, closed by a second mechanism rather than a stronger AST rule.**
`packload/hostaccessgates_test.go` pins that each gate CALLS the merged claim helper. It cannot see a
POST-HOC FILTER — a loop dropping every `loophole ` claim after the call left the scan satisfied and
the whole `-short` suite green. Seeing that needs dataflow, and any rule crude enough for an AST walk
would forbid ordinary code in a security-critical function. So the invariant's other half is
BEHAVIOURAL and per producer (`run/hostaccessgateeffect_test.go`): a fetched pack whose only claim
comes from producer X is refused without approval and granted with it. The refusal case is what
catches a filter, because a dropped claim makes the gate GRANT (`len(want) == 0` reads as "moot")
rather than refuse.

0. **Tolerate an unknown KIND under `TolerateSkew()`** (§3.3a), with a regression test that a
   manifest carrying one still boots a jail. **Before the kind exists**, or every pack that declares
   it bricks a jail running a pre-`just load` image. This is the `tier` incident's third appearance.
   — **done 2026-08-13** (tolerance + boot audibility), regression test **2026-08-14**.
1. **The `loopholes` block's host-exec surface goes user-scope-only** (§4.3 G1) — over entry
   *shapes*, including `doctor_cmd` and `enabled` for daemon-bearing loopholes, with the warn-then-
   error migration and the `docs/guides/loopholes.md:88` fix in the same commit. Fix
   `knownHostServiceKeys` first (§4.3). Independent of everything else and the biggest reduction in
   **who may declare** host execution. **Ship first** — but it closes half a hole, not the hole
   (§4.3a), and the migration needs OQ-LP12 decided so the warning has somewhere to point.
   — **done 2026-08-13**; two follow-ups **2026-08-14**: override `doctor_cmd` is refused once, at
   either scope, instead of pointing at a user config that also refuses it; and `config-dump` got the
   real loophole resolver, without which its enable-uninstalled verdict disagreed with `yolo check`.
   - **1a. Content-anchored confirmation for host execution, every origin** (§4.3a, OQ-LP13). Also
     independent of the kind, also pre-existing, and it is what makes item 1 add up to a closed hole
     rather than a narrowed one. Subsumes item 6's commit anchoring.
     **Landed as the ruled PLACEMENT rule — partly — 2026-08-14.** Item 1 shipped without it, so the
     hole stayed open one batch (measured: a user-config `command: ["python3", "/workspace/tool.py"]`
     validated clean and spawned). Now refused for the two trees a launch hands an agent (the mounted
     workspace, `paths.GlobalHome()`) in `internal/config/loopholeplacement.go`, called from
     `ValidateConfig` (so `yolo check` + the launch preflight), from `LoopholeEntryErrors` (so
     `loopholes list`/`status`, which executes `doctor_cmd`), and from `startExternalService` — the
     spawn face, which is the only one that also sees a MANIFEST's `host_daemon.cmd`.
     **The manifest faces then landed too, 2026-08-14** (`internal/loopholes/placement.go`, called at
     the spawn): the module dir, `host_daemon.cmd` and `doctor_cmd`. The seam is a thin method
     projecting onto the ONE tree comparison in `internal/config`, never a second copy of the rule —
     the config faces and the manifest faces have to agree about what "inside the workspace" means, and
     two comparisons is how they would come to disagree. It lives in `internal/loopholes` because two
     of the three targets are RUNTIME resolutions (the module dir after symlinks, the argvs after
     `{loophole_dir}` substitution) and a resolved record is the first place those exist; and it is
     enforced at the SPAWN rather than at discovery for the reason a name collision cannot be enforced
     in `Discover` either — no error channel, and the spawn is the last moment before the code runs. A
     refused MODULE DIR **suppresses** the argv refusals under it, because `{loophole_dir}` resolves to
     that dir, so a module dir in an agent-writable tree means every host-side field names an
     agent-writable target — including the ones the rule cannot see (a Python daemon's imports, a
     binary's `dlopen`) — and checking the dir is a statement about all of them at once: one mistake,
     one message. A caller with no workspace (the doctor path) narrows the rule to the jail-home tree
     rather than disabling it. The check is deliberately conservative about what counts as a path
     (no whitespace, no shell metacharacters), because a false positive refuses a working loophole at
     every launch; §4.3a's "cannot be complete" limit now has a second, narrower edge to name.
     **Nothing is owed here now** — but note the rule reaches a pack's module dir only through the
     resolved record, i.e. at the spawn, not at `pack install`, which is where §4.3a's *"refused at
     install, by name"* wording pointed.
     **AND IT SHIPPED ONE LAUNCH TOO BROAD — fixed 2026-08-14, and the fix is a lesson about
     verification.** As first landed, the module-dir face judged BUNDLED loopholes too, which in
     yolo's own development jail refused the broker, `audio` and `host-processes` **on every launch**
     with *"module dir /workspace/bundled_loopholes/… is inside the workspace this launch bind-mounts
     :rw … Install the loophole outside that tree"* — advice nobody can follow about content they did
     not install. Cause: `BundledLoopholesDir` prefers the repo checkout when yolo runs from its own
     source tree, so `<repo>/bundled_loopholes/*` **is** inside the `:rw` workspace. The exemption
     (`Source == SourceBundled`) is the rule's own reasoning rather than a concession — a bundled
     loophole is the binary's own content, the same artifact implementing the check, so an agent that
     can rewrite it has already rewritten the checker: gate-placement **Test 1** (§4.3a carries the
     argument). Pack and user loopholes stay judged, pinned by the same test at the *same path*.
     **Why no unit test could catch it:** every placement test builds its module dir under
     `t.TempDir()`, so the one configuration where the bundled dir and the workspace COINCIDE is the
     one nobody constructs. A rule about how two REAL paths relate cannot be verified by a test that
     invents both of them — which is the reusable half, and it is why AGENTS.md's nested-jail smoke is
     mandatory rather than advisory. The `-short` suite was green throughout.
   - **1b.** Make `loopholesWithConfig` refuse (or drop) `loopholes` entries that fail
     `validateInlineService` — a command that executes what it reads must not read through a path
     that skips validation (§4.1). — **done 2026-08-13** (`3bf3a5e`); the refusal names the actual
     origin file, `yolo-jail.local.jsonc` included, since **2026-08-14**.
2. **The front + `publishes` + both `{loophole_dir}` tokens** (§2.1, §2.1a), the stale-socket unlink
   on both ends (§2.1b), the loud readiness-failure warning and the dead `ProcessState` fix (§2.1c),
   then flip `discover.go:60` **and rewrite `retiredTransportHint` with its pinned test** (§2.2).
   — **done 2026-08-13**, plus `request_end` (hazard 2's one field) which the item did not list.
   **2026-08-14:** the EOF round-trip tests are deadline-bounded (one hung a whole package for ten
   minutes), the clean-exit readiness branch is pinned, and a fronted `stop()` now waits for the
   front's listener Close rather than racing it.
3. **The server-side spec** in [`loophole-protocol.md`](loophole-protocol.md) (§2.3), labelled the
   unsupervised path. — **done 2026-08-13**; corrected **2026-08-14**, where it still described the
   front's EOF non-propagation as an inherent limit ("read to the length prefix") — the framing §2.1b
   hazard 2 forbids — and still called the mechanism unbuilt. `publishes`/`request_end` are also in
   `docs/guides/loopholes.md`'s schema now, which is where hazard 2 asked for them.
4. **`internal/loopholedecl`** (OQ-LP1), because the footprint depends on it.
   — **done 2026-08-14** (`f9c5990`, `dcd933e`), by extraction rather than by breaking the
   `loopholes` → `config` edge, plus the strict/tolerant split (`5ba8331`) the old loader did not have
   at all. See §3.2.
5. **The `loophole` kind** (§3): the `refusalReasons` entry and the explicit `JailFields()` exclusion
   (§3.4), the **total** claim enumeration (§3.3), the load-time control-character refusal (§3.2),
   the reserved-name refusal, the home-relative bind-mount constraint, the
   **`publishes: "socket"`-only rule for pack-shipped loopholes** (§2.1), and the
   **platform-support declaration** sharing one mechanism and one message with 5d's
   inert-on-backend report (§3.1, §8).
   — **done 2026-08-14** (`423c4af` for the kind and the claims, `f3f160c` for `platforms`,
   `a8474cc` for the subset, `c213f84` for the placement rule's manifest faces). `KnownKinds()`
   returns fifteen; `from` is required and traversal-guarded; Exclusive by loophole NAME; the
   `refusalReasons` entry and the `JailFields()` exclusion are both explicit; the control-character
   refusal covers every claim-feeding field.
   **THE PACK-SHIPPED SUBSET IS NOW WIRED AT THREE SEAMS — 2026-08-14** (discovery, `pack lint`, and
   `yolo check`'s walker). It had been built and
   unreachable: the `jail_env` refusal, the home-relative bind constraint, the `readonly: false`
   refusal and the `publishes: "socket"`-only rule were implemented and tested in
   `internal/loopholedecl/packshipped.go`, and **neither `LoadDirPackShipped` (authoring) nor
   `loopholes.LoadPackLoophole` (discovery) had a production caller** — so §3.1's requirements 1 and 3
   applied to nothing. Measured then: a pack manifest with all four violations was discovered, Active,
   and produced `-v /:/ctx/hostroot` (readonly:false honored, so no `:ro`) plus
   `-e LD_PRELOAD=/ctx/evil.so`; and `yolo pack lint` printed "pack ok" for it.
   Now the **SOURCE LABEL selects the loader** in `loadModuleDirs` (`loaderFor`), which is the right
   seam because pack-shippedness is the CALLER's fact — a manifest cannot declare it without lying,
   and that function's `source` parameter already IS the fact. `ValidateLoopholes` (`yolo check`'s own
   walker) and `LoopholeDeclProblems` (`pack lint`) go through the same predicate, because a preflight
   or an authoring tool that is KINDER than the loader produces exactly the report/gate disagreement
   the subset was factored into one package to prevent — in `lint`'s case, an author told their
   loophole is fine who learns otherwise from a launch warning on a stranger's machine.
   Both loaders stay TOLERANT: the subset is orthogonal to version skew, so an unknown key is skipped
   and reported while a forbidden field is refused, in one read. Two more fields joined the subset with
   the wiring — `ca_cert` and `requires.file_exists`, both path-scoped through the same classifier as
   the bind host (see the five-defect note above).
   **And the first manifest written against the wired subset found its LIMIT** (item 9): `audio`'s
   `${XDG_RUNTIME_DIR}` sockets have no legal spelling in it at all, so the rules are now known to be
   too narrow for a legitimate host path rather than merely unenforced. **OQ-LP14**, §3.1.
   - **5b.** The **fourth bespoke pre-flight** for loophole-name exclusivity, wired into `stagePacks`
     beside the other three (§3.1). `packload.Collisions` does not do this and is not called at
     launch.
     — **done 2026-08-14** (`7fd8e8f`): `run.PackLoopholeNameConflicts`, per DECLARATION (so a single
     pack declaring two same-basename module dirs is caught, which the generic loop's
     `len(packSet) < 2` skip cannot see), taking the reserved set as its second input, returning an
     error from `stageRunPacks`. It lives in `internal/cli/run` rather than `packload` because the
     reserved set is not *nameable* there (the §3.2 cycle), and pack-vs-reserved is reported instead
     of — not as well as — pack-vs-pack, so one mistake is named once.
   - **5c.** The **retirement-on-deselect** artifacts (§4.5): a pack→state ownership record at
     staging, a detector on the launch path, and a `prune` sweeper for the state tree. Plus the
     process-**group** kill on teardown.
     — **done 2026-08-14.** All three: `internal/packstage/loopholeowners.go` (the record, plus
     `RetireLoopholeState`, which archives the state dir **and** `host-service-<name>.log` under
     `<state>/.retired/<stamp>/<loophole>/` with a `.pack` marker); the detector in
     `run/loopholeretire.go`, called from `stageRunPacks` — so it covers the attach path too;
     `prune.PruneRetiredLoopholeState`, its own section in the report, keep-newest-3. The
     process-group kill was verified still in place (`killServiceGroup`) and is now pinned by a
     test rather than left to inspection.
     **Three refusals the item did not list, each protecting a private key rather than
     tidiness:** retire-before-record (one config edit can drop a pack and select a different one
     shipping the same loophole name — recording first would hand the new pack the old pack's
     CA); an **unknown** configured-pack set retires nothing (a malformed `packs` list read as
     "no packs" would archive every loophole's state at once — `pruneDroppedPackOutput`'s own
     guard); and a **corrupt** ownership record is neither acted on nor overwritten, since
     overwriting turns unreadable into empty and orphans every existing state dir permanently.
     **One honest gap:** a pack that is still configured but STOPPED DECLARING a loophole is not
     detected. That evidence is indistinguishable from a momentarily-unreadable tree, and the
     cost of being wrong is a moved private key — so retirement keys only on the signal the user
     typed themselves.
   - **5d.** The **seven-surface convergence** (§5.1) as one constructed value, with a test; and the
     inert-on-backend report for `container` and `macos-user` (§8).
     — **done 2026-08-14**, the two halves landed independently and then converged. The
     **inert report** is `run/loopholeinert.go`: one line per inert loophole on both backends,
     with the §3.1 platform declaration feeding the SAME report — and "same" is literal, since
     the platform producer shipped as a VALUE (`loopholes.InertNote`, one `Line()` rendering)
     with zero callers, and the backend half plugs into it as `AxisBackend` rather than
     formatting a second sentence. Backend beats platform when both apply, because the
     actionable line is "switch backends", not "get a different machine".
     The **convergence** is `loopholes.NewHostSet` (`7fea38e`, `e6987f0`), pinned by two tests: one
     walks the census files, one asserts the census is seven over six files plus the walker. Its
     load-bearing half is not the value but the **callee-enforced gate**: `RunDoctorChecks` refuses to
     execute a pack-sourced record without a recorded approval, and a hand-assembled `Set` carries no
     gate — so the two read-only-looking preflight commands cannot run an unapproved fetched pack's
     `doctor_cmd` even by mistake, which a rule they were merely asked to follow could not guarantee.
     It also fixed two silences the item did not list: the briefing advertising enabled-but-INACTIVE
     loopholes to the agent (§5.1), and the builtin-name daemon skip that dropped a daemon without a
     word while its binds and devices crossed (§3.1).
6. **The approval invariants**: commit-anchored exec claims (§4.3 G2b, giving `ApprovedAt` its first
   reader), the raw-unelided claim-string rule (§4.3 G2a), one merged claim helper called at both
   gates (§3.3), and `notePackHostAccess` extended and moved **before** `startLoopholes` (§4.3 G4).
   — **G4 done 2026-08-14.** The kind coverage is no longer a hardcoded switch but DATA
   (`run/packloopholes.go`'s `disclosureClasses`), exhaustive over `packdecl.KnownKinds()` **by
   test** — which is the half that mattered, since the hardcoded set was wrong and nothing
   noticed. **Two kinds it had been silently dropping, beyond the `loophole` kind the item
   anticipated:** `program via installer` (a curl-to-shell script) and `briefing after host:`
   both read the user's host and appeared at no launch. An unclassified kind now defaults to
   **exec**, the only fail-closed direction. The ordering is enforced structurally rather than by
   statement order: `startLoopholesDisclosed` is the sole call site of `startLoopholes` (pinned),
   and the test reads the spawn side's own first side effect — asserting merely that the line
   printed would pass under the OLD ordering, which is how it survived.
   **The merged claim helper also landed 2026-08-14** (`497c76e`): `packload.Pack.HostAccessClaims`
   is the one union of all three producers, called by both gates, with a source-level test that fails
   if either gate reaches for a producer directly (§3.3).
   **G2a LANDED too, 2026-08-14.** The rule is enforced where the only exec-bearing claim is
   produced: the loophole producer renders the RAW manifest argv through `shquote.Join` — nothing
   elided, `{loophole_dir}` unexpanded, injective — pinned by `TestClaimStringIsRawArgv` and
   `TestDaemonArgvRenderingIsInjective`. Both halves are asserted because both fail
   catastrophically rather than cosmetically: an elided argv collapses two different daemons onto
   one approved claim, and an expanded one makes the approval machine-specific, so it re-prompts
   forever and `promptYesNo` fails closed on a non-TTY. The rule is not separately asserted for the
   read-only producers, where a claim string is a path and there is nothing to elide.
   **Still owed from item 6: G2b ALONE, and it is a DECISION rather than pending work.**
   `ApprovedAt` is written by `pack install` and read by nothing, and `HostAccessApproved` compares
   claim STRINGS only — so a fetched pack at a mutable ref whose daemon FILE changes under an
   unchanged argv re-installs with no prompt. Whether to close that is OQ-LP8: content-anchoring may
   be redundant under §4.3a's ruling that the user-scope edit IS the confirmation, and the placement
   rule (landed) closes the second-actor gap that argument leaves open. Not implemented on purpose.
7. **`pack-system.md` §12's first invariant, sharpened** (R1) — in the same commit as the kind.
   — **done 2026-08-14**: the invariant now separates the cost of *looking* from the cost of
   *selecting*, and quotes the sentence it replaces so the change is legible.
8. **`pack-capabilities.md` rewritten per §6** — done 2026-08-13.
9. **The `audio` example pack** (§7), as the end-to-end proof of discovery, selection, the footprint
   claims, **the approval prompt**, the `:ro` binds and teardown — with `--device` observable only
   off a jail host.
   — **DONE 2026-08-14** as `packs/audio`, a real embedded OFFICIAL pack rather than a
   `docs/examples/` copy (OQ-LP11 ruled it ships in this batch). It is **additive**: the bundled
   loophole is kept and untouched, the pack's loophole is `audio-alsa` (the plain name is RESERVED and
   claiming it is a fatal launch refusal), and it binds a `conf.d` fragment rather than
   `/etc/asound.conf` because podman refuses two binds on one destination — a jail with both would
   refuse to start. Verified with `sox`, a real libasound client, in all three routing cases.
   **The item's own framing had to be corrected twice**, and both corrections are in §7:
   **(a) the pack cannot be `audio`**, because two of `audio`'s declarations are outside the subset —
   `jail_env` (which moves to the `env` kind and becomes unconditional, OQ-LP5's cost, now *paid* by a
   shipped pack rather than predicted) and the `${XDG_RUNTIME_DIR}` bind hosts, which have **no legal
   spelling at all** for a pack. That second one is a finding about the DESIGN and is now **OQ-LP14**:
   the socket half of the loophole this design nominated as its own dogfood is inexpressible, and the
   subset table's suggested fallback (*"a `host_daemon` that mediates"*) means writing an audio proxy
   — reaching for arbitrary host execution to obtain a `:ro` bind.
   **(b) the four-claim count is right and its composition was wrong**: two IPC (the `readonly: false`
   binds) + one MOUNT (`asound.conf`, a `readonly: true` regular file) + one device, not "three
   socket/dir binds and `/dev/snd`".
   **What it proved, and what it did not.** It closes R2 at the level that mattered — a real instance
   goes through the enumeration, the pre-flight, the subset loader, discovery, the inert report and the
   container argv, and building it found two defects no test had (the lazy resolver skipping embedded
   packs; the subset finding). It does **not** exercise the approval prompt or a host daemon spawn:
   the pack declares no `host_daemon`, and an embedded pack carries yolo's own authority so its
   approval is true by construction and never reaches `promptYesNo`.
10. **Nested-jail user scope** (OQ-LP9, §9), per the three-part split: the generated preflight file,
    the nesting-only launch input, and the `--user-layer` CLI arg. Added to this list when it was
    built; it was previously tracked only as an open question, which understated it — §4.3a's ruling
    that loopholes are developed in a nested jail has nowhere to send anyone until an in-jail agent
    can write at user scope.
    — **DONE 2026-08-14.** Four corrections the specification needed, all argued in §9's OQ-LP9
    Landed note: **`cache_relocations` was the wrong example** for the false-error class (it and
    `host_files` were already hand-guarded; the live classes were `gpu`, `mounts` and `env_sources`,
    which nobody had noticed — a census fixes the pattern where a guard fixes an incident); **five keys
    earn BOTH memberships**, measured off `yolo check`'s entrypoint dry-run rather than judged from the
    shape of the key; **the nested-launch file was written and never read**, which two real nesting
    levels caught and two unit tests missed — the second time in this batch a value was computed
    correctly and then not consumed; and **the flag is GLOBAL**, consumed before `RewriteArgv`, because
    four commands read user scope in-jail and a flag reaching only `run` would change a launch but not
    the command you verify it with. R5 is verified end to end in a real nested container (with one
    constraint users hit first: the jail's home ROOT is `:ro`, so the pack must live under
    `~/.local/share/…` or the workspace), and R7 makes `config drift` NAME the scope it cannot
    compare, in-jail only, on both paths, with exit codes untouched.
