# Loophole packaging — how a pack ships a loophole

**Status:** DESIGN, 2026-08-13. Not built. Closes **OQ-CAP2** with option **(B)**.

**Why this doc exists.** [`pack-capabilities.md`](pack-capabilities.md) §10 asks whether packs should
be able to ship loopholes, concludes *"the review is right that (B) is the real fix"*, and
recommends deciding (B) **before** building (A). The maintainer ruled: write (B) first, link it from
`pack-capabilities.md` as a prerequisite, then let that doc assume it and simplify. This is (B).
§6 below is the specific list of what survives in `pack-capabilities.md` §§1–9 and what dies.

**Reads with:** [`pack-system.md`](pack-system.md) (the 14 kinds and how a kind is defined),
[`loophole-protocol.md`](loophole-protocol.md) (the wire contract),
[`loophole-transport.md`](loophole-transport.md) (the transport unification, shipped 2026-08-13),
[`pack-capabilities.md`](pack-capabilities.md) (what this replaces),
[`extension-point-principle.md`](extension-point-principle.md) (why design it now).

**What is verified and what is not.** Every code claim below was read on HEAD (`00cf6ae`) and the
import-cycle claim in §3.2 was measured with `go list`. **No pack-shipped loophole has ever run** —
this is design over verified code paths, not over a working instance. §4's headline finding (the
config `loopholes` block already executes an ungated host command) was reproduced empirically in a
prior survey against a freshly built `cmd/yolo`; I re-verified the code path but not the run.

---

## 1. The gap, and why it got acute this week

Loopholes have three distribution channels and only one of them is third-party:

| Source | Constant / entry | Who it is for | State |
|---|---|---|---|
| `bundled_loopholes/`, embedded in the binary | `BundledLoopholesDir` (`internal/loopholes/loopholes.go:89`) | yolo's own three | fine |
| a user loophole dir | `UserLoopholesDir` → `~/.local/share/yolo-jail/loopholes` (`loopholes.go:110`) | one hand-placed local loophole | no fetch, no version, no approval, no manifest travelling with the code |
| the `loopholes` block in `yolo-jail.jsonc` | `synthesizeConfigLoopholes` (`discover.go:29`) | **the only third-party path** | **degraded, and now dead** |

**The transport unification killed the last row.** `internal/loopholes/loopholes.go:63` is now
`validTransports = []string{TransportLoopbackTLS, TransportNone}`; both `unix-socket` and
`tls-intercept` were removed rather than deprecated, deliberately
([`loophole-transport.md`](loophole-transport.md) §7.4 item 2: *"a value that still validates is a
value someone will use"*). The retired value survives in exactly one place —
`internal/loopholes/discover.go:60` hardwires `Transport: retiredTransportUnixSocket` into every
config-synthesized loophole — and the comment above it (`discover.go:12-28`) says why:

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
launch-time notice, and a **workspace** config can write it. That reframes the trust question, so §4
opens with it rather than with "should we open a new hole".

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

**`{socket}` and `{endpoint}` must diverge under `publishes: "socket"`.** Today both expand to the
same host path and `{socket}` is a back-compat alias (`loopholesruntime.go:367-372`). Under the new
value `{socket}` is the **upstream** path and `{endpoint}` is the **published** file. A manifest
declaring `publishes: "socket"` while naming `{endpoint}` in its argv is an author error and is
refused at load with the fix, rather than silently publishing nothing.

**The upstream socket lives outside the mounted dir.** `/tmp/yolo-front-<8hex>-<name>.sock`, beside
the relay's, for the reason `loopholesruntime.go:600-606` already records for the relay: leaving it
in the `:rw`-mounted `/run/yolo-services` would keep the retired transport reachable from inside the
jail — which is what retiring it forbids — and would let the jail unlink the daemon's own socket.

**Three hazards to design deliberately, not discover:**

1. **`ServeFront` publishes before the upstream exists, on purpose** (`front.go:23-24`: *"a
   connection that cannot reach `upstreamUnixPath` is logged and dropped"*). Correct for the relay;
   wrong here, because yolo's 5-second `Probe` wait (`loopholesruntime.go:426-439`) would succeed
   while the child never came up, and the jail would then authenticate successfully and be dropped —
   reading as a daemon failure. **So the shim waits for the child's socket before calling
   `ServeFront`, not after.** That ordering needs no change to `ServeFront` itself.
2. **`splice()` never propagates the client's EOF upstream** (`front.go:46-66`), tuned to the
   relay's frozen pipe semantics. `frameproto` is length-prefixed so a conforming daemon does not
   need EOF — but a daemon that reads its request *to EOF* works on a bare socket and **hangs
   forever** behind the front. That is a behaviour change the author cannot see, so it is a named
   requirement in the guide, not a footnote.
3. **No per-request access log.** `hostservice`'s structured line
   ([`loophole-protocol.md`](loophole-protocol.md) §Access logging) is a property of daemons using
   the helper. The front sees bytes, not requests, so a spliced third-party daemon's requests are
   unlogged by yolo. A known limit — and the natural home for the crossing audit log later, since
   every third-party crossing would pass through one yolo-owned process.

### 2.2 What flipping `discover.go:60` costs: nothing

A config-block daemon binding a socket at `{socket}` becomes
`Transport: TransportLoopbackTLS` + `HostDaemon.Publishes = "socket"` — which is **true of it** and
needs no retired vocabulary. Its argv does not change, its behaviour does not change, and the jail
gains a real endpoint. The comment's objection (*"flipping this line would kill each one"*)
dissolves because the daemon is now **wrapped** rather than expected to publish. That closes the last
clause of queue row **T2** and deletes `retiredTransportUnixSocket`'s final reader.

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
trusted to the degree its author is. Three couplings belong in it because they are enforced by yolo's
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
  ([`pack-system.md`](pack-system.md) §3), and there is no conventional-location exemption here
  because there is no convention.
- The loophole's `name` must equal the directory basename. Already enforced by `loadManifest`
  (`internal/loopholes/load.go:58-63`), so it comes free — and it is what lets the footprint name the
  loophole without decoding its manifest.
- **Combine: Exclusive, by loophole NAME.** Exclusivity is per name, not per pack, so a pack shipping
  three loopholes is ordinary — the same rule `program` has per `bin`.
- **A name collision is FATAL, naming both sources.** This is S1's skills lesson, and it is stronger
  here: a shadowed loophole name means a daemon nobody audited running under a name the user trusts.
  Fatal for pack-vs-pack **and** pack-vs-bundled. The user loophole dir keeps its current
  last-wins overwrite (`discover.go:189-202`) because a hand-placed directory carries the user's own
  authority — the same reason a `file://` pack does. That asymmetry is deliberate and is named in
  §9 (OQ-LP3).

**The pack-shipped subset of the manifest is smaller than the bundled one.** Two fields are refused
by name, each with a named alternative:

| Refused for a pack-shipped loophole | Why | Use instead |
|---|---|---|
| `jail_env` | it emits `-e K=V` (`internal/loopholes/runtime.go:156-159`), colliding with the `env` kind's target namespace — and `Collisions` keys on `{kind, target}` (`internal/packload/footprint.go:230-245`), so two *different* kinds claiming one target can never collide. Today every kind's namespace is disjoint by luck; this would end that. | the `env` kind, which the footprint already sees |
| `host_bind_mounts` with `readonly: false` | `load.go:294` defaults `readonly` to true but accepts false — a **read-write** host path into the jail, which no pack kind can express. `mount` is `:ro` as a credential-boundary property ([`pack-system.md`](pack-system.md) §3), and a loophole must not be a back door around it. | `mount` (`:ro`), or a `host_daemon` that mediates |

Everything else is allowed and claimed: `host_daemon`, `jail_daemon`, `intercepts` + `broker_ip` +
`ca_cert`, `host_bind_mounts` (`:ro`), `host_devices`, `state_files`, `requires`, `doctor_cmd`,
`serves`.

**The `jail_env` refusal has a real cost, stated rather than hidden.** A loophole's `jail_env` is
*conditional on the loophole being active*; the `env` kind is unconditional. `audio` relies on
exactly that (`PULSE_SERVER` only makes sense when the sockets crossed). So a pack-shipped
audio-shaped loophole would set env even when inactive. That is the case that would justify a
cross-kind collision pass, and it is purely additive — same claim model, one more pass beside the
three bespoke ones already there (state scopes, plugin names, config identities:
`footprint.go:311-352`, `:357-384`, `:419-453`).

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
predicate evaluation. It may import `json5`/`jsonx`/`pytext`, all of which are measured leaves
(`go list -deps` on each returns itself, plus `jsonx` for `json5`), so there is no cycle. This is the
placement rule `packdecl` (`kinds.go:13-17`) and `pluginpack` (`pluginpack.go:24-25`) both document,
and it has a payoff independent of packs: the schema becomes readable by the footprint, by
`yolo pack lint`, and by a host-side validator without dragging the runtime predicates along.

The alternative — break the `loopholes` → `config` edge, which is only two files (`resolver.go:3`,
`loopholescmd.go:16`) — is cheaper in lines and worse in shape, because it leaves schema and runtime
fused. Marked **OQ-LP1**; either resolves the cycle, and the doc's design does not depend on which.

**The loader also needs the strict/tolerant split it does not have.** `loadManifest` is a hand-rolled
`jsonx.OrderedMap` walk with **no unknown-key rejection at all** — `"version": 1` is declared by all
three bundled manifests and documented as the schema version, and nothing reads it
(`rg -n '"version"' internal/loopholes/*.go` is empty). Contrast `packdecl.Decode`'s
`DisallowUnknownFields` for authoring plus a deliberately tolerant `DecodeTolerant` for the version
boundary (`packdecl.go:144`, `:206`, with the `tier` incident written up at `:154-205`). A
pack-shipped loophole crosses that boundary — host CLI reads it, and a skewed baked entrypoint may
too — so it needs both halves. Today it would tolerate skew and never tell an author about a typo.

### 3.3 Footprint entry — one contribution, several claims

`FootprintOf`'s switch appends per-claim in a loop already (`footprint.go:59-184`), so a loophole
emitting several is representable. Two classes:

```
loophole  acme-proxy              RUNS `python3 …/acme-daemon.py --socket {socket}` on your machine   ⚠ review
loophole  acme-proxy:api.acme.com INTERCEPTS api.acme.com — installs a CA trusted by every TLS
                                  client in the jail                                                 ⚠ review
```

- **Target** is the loophole name for the base claim, `<name>:<host>` per intercept. So
  `Collisions`' generic Exclusive pass refuses two packs shipping one loophole name **for free**
  (`footprint.go:230-267`), with no fourth bespoke pass.
- **The argv goes in `Detail`, and the words "on your machine" are spelled out.** `ReviewWorthy` is
  one boolean — one severity — and it currently means "reads `~/.claude.json`". Host execution must
  be distinguishable from a host read, and the in-tree precedent solves exactly this without adding
  a severity field: `pluginClaimDetail` spells **"RUNS CODE"** into the Detail string
  (`footprint.go:189`, `:206`) with the reasoning that *"this claim is the one place a user learns
  that installing a pack of 'skills' also starts an MCP server."*
- `doctor_cmd` is host execution too (`internal/loopholes/runtime.go:209` `RunDoctorChecks`, called
  from `internal/cli/check/sections_loopholes.go:47` and `loopholes/loopholescmd.go:138`), so it
  joins the base claim's argv list rather than getting its own line.
- **The intercept claim exists even with no daemon.** A `transport: none` loophole that declares
  `intercepts` runs no host code and still installs a CA into every TLS client in the jail — measured
  in [`loophole-transport.md`](loophole-transport.md) §5.0.1: `entrypoint/system.go` folds every
  `NODE_EXTRA_CA_CERTS` entry into one bundle that `SSL_CERT_FILE`, `CURL_CA_BUNDLE`,
  `GIT_SSL_CAINFO` and `REQUESTS_CA_BUNDLE` all point at. So curl, git, python-requests and Node
  alike. Two claim classes, because they are two different powers.
- **`pack footprint` must say which side of the gate the claim is on.** `packFootprintLocal` passes
  `mayAccessHost=true` on purpose (`internal/cli/pack.go:861`) so a host-gated claim shows up while
  authoring. For a host *read*, "what does this pack want" is the right question. For host
  *execution*, wants-versus-gets is the whole trust story, so the line must be explicit.

Mechanical costs, each enforced by an existing test that fails until updated: `kinds_test.go:30`
hardcodes `14`; `kinds_test.go:99-107` hardcodes the review-worthy set; `applyhostcensus_test.go`
fails by name until the kind appears in `apply --host` output; `packkinddocs_test.go` fails until the
kind is named in **both** `internal/cli/config_ref.txt` and `packUsage` (`pack.go:57`). Also:
`printPackFootprint` (`pack.go:473-483`) and `reportFootprint` (`:901-911`) duplicate the
claim-formatting loop despite `:464-466` claiming they are shared "so their output does not drift" —
a new marker has to be added twice or the two commands diverge.

### 3.4 At the HOST target, where there is no jail: refused — and the naive reason is backwards

`HostFields()` (`internal/render/fieldset.go:122-149`) is an explicit allowlist and `JailFields()` is
*derived* from `packdecl.KnownKinds()` (`:105-111`), so a new kind is honored in a jail
automatically and refused off-container by default. That default is right
(`fieldset.go:155-162`: *"a kind wrongly refused is a message, a kind wrongly honored is a write
nobody asked for"*). But `loophole` must land in `refusalReasons` (`:37-42`) rather than fall through
to `Refuse`'s generic *"X is not applicable at this confinement level"* (`:33`), because the generic
line would be the single most confusing sentence in the command.

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

**`guest` is the real question and this doc does not pre-answer it.** `Target.Fields()` funnels
`KindGuest` into `HostFields()` today (`fieldset.go:171`), with the comment noting its census is
Phase 7's to state. A guest is a real process on the real machine under an LSM/Seatbelt profile — so
a loophole daemon serving it is **coherent**, unlike at `host`. This kind is therefore the first
concrete case where the guest-into-host funnel gives the wrong answer for a reason, not just
conservatively. Recorded, not decided.

---

## 4. Trust — the hole already exists, and it is wider than the one we are opening

### 4.1 The finding

**The `loopholes` block in `yolo-jail.jsonc` already executes an arbitrary host command, gated by
nothing, and a WORKSPACE config can write it.**

- `internal/cli/run/loopholesruntime.go:138-151` scans the **merged** config's `loopholes` map and
  adds every entry with a `command` to the external-service set; `:161` calls
  `startExternalService`, which at `:384` runs `exec.Command(cmdArgs[0], cmdArgs[1:]...)` with
  `Setsid: true` (`:388`), env from the entry's own `env` block with `~` expansion (`:402-412`).
- **No gate of any kind.** No prompt, no lockfile, no origin check, no `allow_exec`, no launch-time
  notice — `startExternalService` prints only on *failure* (`:358`, `:417`). A successful third-party
  host daemon spawn is silent.
- **`loopholes` is not user-scope-only.** Exactly three keys are: `packs`
  (`internal/config/packs.go:484`), `host_files` (`hostfiles.go:938`), `cache_relocations`
  (`validate.go:1025`). `loopholes` is absent, and `internal/loopholes/loopholescmd.go:60-82` merges
  the user **and workspace** blocks explicitly. A workspace config is agent-editable and
  repo-committed.
- **The retired transport does not disarm it.** Execution precedes the reachability wait: the daemon
  runs, and only after the 5-second deadline (`:426-439`) is `cmd.Process.Kill()` called — and
  because of `Setsid`, only the direct child.
- Second ungated host-exec site: `doctor_cmd`, run by `yolo check`
  (`internal/cli/check/sections_loopholes.go:47`) and `yolo loopholes status`
  (`loopholescmd.go:138`).

And the vocabulary reachable from a hand-placed manifest is *broader* than any pack kind:
`host_bind_mounts` accepts `readonly: false` (`load.go:294-306`) — an arbitrary read-write host path
into the jail — plus `host_devices`. Ungated by origin or approval.

### 4.2 So the honest question is inverted

Not *"should we open a new hole"* but *"the existing hole is wider than the one we are being asked to
open, and closing it is the same work."*

A pack-shipped loophole would be **fetched, pinned to a commit, claim-listed, approved, and
re-prompted on a moving pin**. A config-block loophole is an unversioned argv in an agent-editable
file that runs as the user with no prompt. `pack-capabilities.md` §10 frames the pack path as *"a
real trust step"* — it is, but the step is **downward from where we already are** for the
third-party case, and only upward relative to the *pack system's* current posture (no pack kind
causes host execution today: hooks run in the entrypoint, `program` installs in-jail).

### 4.3 Three gates, all of them shipped machinery

**G1 — `loopholes.command` and `loopholes.env` become user-scope-only.** Join `packs`,
`host_files`, `cache_relocations`, with the identical argument: a workspace is agent-editable and
this key runs code on your machine. The pure-toggle keys (`enabled`, `jail_env`, `jail_endpoint`)
stay writable at both scopes, because disabling a loophole and naming container env vars are not
host execution. **This is the single largest risk reduction in this document and it is independent
of packs** — it should ship whether or not the kind does.

**G2 — a pack-shipped loophole's host claims join `HostAccessClaims()`.** The existing set is
`reads-host`, `mount`, `installer`, host-prepending `briefing`
(`internal/packdecl/contributes.go:607-623`), read through one predicate so a caller cannot honor
some and miss another. Two new strings, matching the footprint Details of §3.3:

```
loophole acme-proxy — RUNS `python3 …/acme-daemon.py --socket {socket}` on your machine
loophole acme-proxy intercepts api.acme.com — installs a CA trusted by every TLS client in the jail
```

Approved at `yolo pack install` by `resolveHostApproval` (`internal/cli/pack.go:1089-1123`),
recorded as the sorted set plus the approving commit in `packsrc.LockEntry.ApprovedHostAccess`
(`internal/packsrc/lock.go:45-56`), superset ⇒ re-prompt with the full current set, subset ⇒ silent
carry-forward (`:64-78`). **The prompt's sentence is already written for this** —
`pack.go:1113` says *"⚠ pack %s reads your host **or runs code on it**:"*.

And a property that falls out free: because the claim string carries the argv, **changing the argv
across a ref bump is a superset and re-prompts.** That is exactly the behaviour you want and it needs
no new mechanism.

**G3 — origin still bounds it, and it fails closed.** `packMayAccessHost`
(`internal/cli/run/packs.go:378`): embedded or local ⇒ true; fetched ⇒ the lock must approve every
claim the *staged* tree currently makes; a nil, missing or corrupt lock approves nothing. A fetched
pack whose loophole claim is unapproved has its loophole **not discovered at all** while its other
contributions still work — the same shape `mount` has today, refusals printed per-claim
(`packs.go:218-231`).

### 4.4 Four things to name honestly

1. **`allow_exec` is not this gate and would not even fire.** It gates staging a file with an execute
   bit (`internal/packstage/packstage.go:149-156`), is per-pack rather than per-file, and is
   origin-blind. A daemon shipped as `python3 script.py` needs no exec bit at all. It *is* one step
   short of host execution in a different way — `apply --host` delivers an executable staged file at
   `0o555` (`internal/entrypoint/hostfilestree.go:192-201`), reasoning that `allow_exec` was the gate
   and "honoring it here needs no second gate", which is the live matt-fzf case: a pack-owned script
   the **host's** Claude Code executes. yolo does not exec it; the pack causes host-side code to
   exist where host software runs it.
2. **`file://` is trusted unconditionally, and forever.** `OriginLocal` is nothing but a `file://`
   prefix (`internal/config/packs.go:125-127`, `:154`) and `MayGrantHostFiles()` returns true with no
   approval and no re-approval. `git clone` someone's pack, point `file://` at it, and the fetched
   gate never runs. **Not changing it** — the origin model's whole claim is that a directory the user
   controls carries the user's own authority, and special-casing one kind would make the model
   incoherent. It is this design's largest residual risk (OQ-LP3).
3. **`yes | yolo pack install` grants approval.** `promptYesNo` (`pack.go:1147`) fails closed on a nil
   stdin or EOF, but the call site always passes `os.Stdin` with **no TTY check**. A one-line
   hardening, independent of this design, and worth doing in the same batch.
4. **`apply --host` silently drops every fetched pack today.** `packForCheckDeps`
   (`internal/cli/checkdeps.go:135-137`) returns nil for anything not embedded and not `file://`,
   and the printed reason blames offline resolution. So the G3 gate is untested at that command
   because nothing fetched reaches it. Pre-existing; named so it is not mistaken for this design's
   doing.

### 4.5 Nothing reaps a departed loophole's state — and this kind makes that matter

Measured: `rg -n 'loophole' internal/prune/*.go` returns zero hits outside relay comments. So
nothing prunes per-loophole state dirs (`~/.local/share/yolo-jail/state/<name>/`),
`host-service-<name>.log` under `GlobalStorage()/logs`, or the materialized embed cache. For a
hand-placed loophole that is untidy. For a pack-shipped **intercepting** loophole it is a CA private
key left behind by a pack the user deselected.

**Deselecting a pack must retire its loopholes' state the way `files` retires its host output** —
archived under the state dir rather than deleted, reclaimed by `yolo prune`
([`pack-system.md`](pack-system.md) §14). Deleting is wrong for the same reason it is wrong there:
the state may be the only copy of something the user wants back. This is a real gap the kind creates
and it belongs in the same batch.

---

## 5. Selection and defaults

### 5.1 Selection gates discovery, and today nothing does

**The MOUNT is the filter for packs** (`AGENTS.md`) — the entrypoint renders whatever is staged. That
does not help here: a pack-shipped loophole is read **host-side, before the container exists**. All
four `Discover` call sites are pre-launch: `internal/cli/run/prepare.go:61` (the briefing),
`assemble_parts.go:423` (`brokerLoopholeActive`), `assemble_parts.go:566` (container argv),
`loopholesruntime.go:113` (**the host daemon spawn**). So selection has to be enforced inside
discovery.

**The seam is small and idiomatic.** `DiscoverOptions` (`discover.go:172-178`) already carries a
`Root` override, and `loadFromDir(dir, source)` already iterates child dirs each holding a
`manifest.jsonc`. So the caller passes **pack-contributed module dirs in**, and
`internal/loopholes` never learns what a pack is. A fourth `Source` label — `SourcePack`, beside
`SourceBundled|User|Config` (`loopholes.go:70-72`) — slots in, and it is what `yolo loopholes list`
prints (`loopholescmd.go:117`), so provenance is visible without new plumbing. Sequencing is already
right: `stagePacks` runs at `run.go:158`, well before `assembleRunCmd` (`:486`) and `startLoopholes`
(`:516`).

**Precedence:** `bundled < pack < user < config-override`, with pack-vs-pack and pack-vs-bundled
collisions **fatal** (§3.1) rather than resolved. Only the user dir overrides silently.

**The four `Discover` calls must not disagree.** They already take different options, which is the
substrate for skew, and `assemble_parts.go:395-401` already worries about exactly this ("the two can
no longer disagree"). A pack-shipped loophole must reach all four or it will be half-active — argv
without a daemon, or a daemon nothing dials. Worth an assertion, not just care.

**One shipped bug to fix while here:** the briefing path (`prepare.go:61-66`) filters on `Enabled`
only, not `Active()`. So an enabled-but-inactive loophole is advertised to the agent as a live
capability. Pre-existing and orthogonal, but a pack-shipped loophole makes it more visible.

### 5.2 `yolo loopholes enable/disable` must stop writing the manifest

`SetEnabled` (`internal/loopholes/runtime.go:261-285`) read-modify-writes
`<modulePath>/manifest.jsonc` in place, and admits it drops JSONC comments by design (`:258-260`).
For a pack-shipped loophole `modulePath` is inside the **staged** tree, which staging clears and
recreates — so the toggle would appear to work and silently evaporate on the next launch.

**Decision: for a pack-shipped loophole the toggle writes `loopholes.<name>.enabled` in the user
config**, which `applyWorkspaceOverrides` already honors (`discover.go:93-95`). The config is the
durable place; the staged tree is derived. Unifying *all four* sources on config-side enabled state
would also delete `SetEnabled`'s comment-destroying RMW and is the better end state — but it changes
behaviour for bundled and user-dir loopholes, so it is a separate decision, not a side effect of
this one.

### 5.3 Defaults: a pack-shipped loophole is ALWAYS opt-in

**"Nothing is active by default"** (`AGENTS.md`) is the pack system's headline property: an empty
config yields a jail with no coding agent and says so. A default-on *third-party* pack would mean
yolo selecting code the user did not ask for — which for this kind means running a daemon on their
machine. So:

> **The only default-on loopholes are bundled ones. A pack-shipped loophole activates only by
> selecting its pack.**

That draws the line cleanly, and it is why `Implicit: true` is **not** the mechanism reached for
here. The conventional local pack is `Implicit: true` (`internal/config/packs.go:275`) and that is
precedent for a default-on pack — but it is a pack the user *is* by definition: their own
`~/.config/yolo-jail/local/`, with `MayGrantHostFiles()` true "with no special case… there is no
third party at all" (`packs.go:253-256`). Generalizing from it to a distributable pack would drop
the one fact that makes it safe.

### 5.4 So how does the broker stay on by default?

**It stays bundled.** `bundled_loopholes/` is not a legacy channel to be migrated away from; it is
the channel for *the things yolo itself is accountable for*. Two channels, one loader, and the
difference is who is accountable — precisely the `_official/` versus top-level split pack staging
already has (`internal/cli/run/packs.go:39`).

Three reasons this is right and not a dodge:

1. **Bundled + `requires` already IS the default-on mechanism.** `claude-oauth-broker` declares
   `requires: {command_on_path: "claude"}` and `Active()` is `Enabled && RequirementsMet()`
   (`loopholes.go:232`), so a user with Claude Code installed gets refresh serialization without
   knowing they need it. Make it opt-in and anyone who does not select it silently gets the
   single-use-refresh-token race the broker exists to prevent.
2. **A pack could not express what the broker needs.** Its `host_daemon.cmd` is **not what runs** —
   `startLoopholes` special-cases the name (`loopholesruntime.go:156-159`) and reconstructs the argv
   in Go via `broker.BrokerSpawnArgv`. And its per-jail relay, the only loopback-TLS hop a jail
   actually dials for it, has **no manifest vocabulary at all** (`ensureBrokerRelay` at
   `loopholesruntime.go:498`, argv at `:627`, endpoint at `:613`). Migrating it would mean inventing
   two manifest features for one consumer — the exact thing
   [`extension-point-principle.md`](extension-point-principle.md) says not to do from one use case.
3. **`host-processes` cannot move either**: its client is `cmd/yolo-ps`, a baked image binary. A
   pack-shipped loophole whose client must be in the image is a contradiction.

**Consequence, and it is the honest one: supersession does not die.** It survives for exactly the
bundled set — which is `pack-capabilities.md` §10's own predicted residue: *"supersession is only
needed for things that AUTO-ACTIVATE."* Three bundled loopholes, of which one auto-activates in a
way a pack can reasonably want to cancel. That is a much smaller thing to design than §§1–9 as
written, and §6 says exactly how much smaller.

---

## 6. What this does to `pack-capabilities.md` §§1–9

| § | Verdict |
|---|---|
| **1** the concept (a capability is a named job) | **Survives**, scope narrowed: it is no longer the answer to "turn off the broker", it is the answer to "turn off an **auto-activating** loophole". |
| **2** the two verbs, `serves` bare / `supersedes` with `because` | **Survives unchanged.** The asymmetry (a claim about yourself is cheap; about another component is not) is independent of where implementations live. |
| **2.1** supersede is not provide | **Survives unchanged.** The demand-versus-supply test is orthogonal. |
| **2.2** "why a pack cannot `serve` — a hard line, not a policy" | **The ARGUMENT DIES; the CONCLUSION survives for a different reason.** Its premise is *"a pack is a bundle across a closed set of 14 kinds… and none of them is 'a daemon'"* — false the moment the 15th kind exists. But `serves` still does not belong on `pack.json`: the implementation it ships **has a manifest of its own**, and a statement about an implementation belongs there. So `serves` stays on the loophole manifest and travels *inside* the pack. The section must be rewritten, not deleted. Its closing claim that pack-to-pack provision is *"unexpressible"* becomes false and must go. |
| **3** why a capability and not the loophole's name | **Survives unchanged**, and is the strongest section in the doc. |
| **4** the rule (`Active()` gains `!Superseded()`) | **Survives, narrowed.** `Superseded()` is only *reachable* for loopholes a selection change cannot remove — i.e. bundled ones. A pack-shipped loophole is deselected by deselecting its pack. Also: the cited line is wrong — `Loophole.Active()` is `internal/loopholes/loopholes.go:232`, not `:219` (`:219` is inside `RequirementsMet`'s host branch). |
| **5** the namespace inverts the skills rule | **Survives unchanged.** Interface-versus-identity is orthogonal to distribution. |
| **6.1** a typo is refused at load | **Survives, and gets easier**: the served namespace is now closed by the selected pack set too, so the "did you mean" list is still decidable. |
| **6.2** an over-broad claim, mitigated by `because` | **Survives.** |
| **6.3** name who turned it off and why | **Survives, and gains a second author.** "No pack ships it" is now a reason a loophole is absent, and `loopholes list` must distinguish *superseded* from *not shipped* from *requirements unmet*. |
| **6.4** two packs disagree; no `needs` | **Survives.** |
| **7** what is deliberately not built | **First row dies** ("`serves` on a pack — unexpressible"), replaced by "expressible, and it lives on the loophole manifest inside the pack". The other two rows (`needs`, a central registry) survive. |
| **8** the first-party instance | **Survives as written.** The broker stays bundled (§5.4), so the example manifest is unchanged. |
| **9** acceptance | **1, 2, 3, 4, 5, 6 survive. 1b is re-argued**: `serves` on a `pack.json` is still refused, but the message changes from *"a pack has nothing to serve with"* to *"put it on your loophole's manifest"* — which is now a fix rather than a wall. |
| **10** OQ-CAP2 | **Closed with (B).** Its own recommendation ("decide (B) first") is followed. |
| **11** OQ-CAP (top-level vs `contributes[]` for `supersedes`) | **Survives, and top-level is now clearly right** — `supersedes` remains a property *of the pack*, while the thing that *is* a contribution is the loophole. |

**Net effect on A6:** it shrinks from a capability system to a capability system *for three bundled
loopholes*. Whether that is still worth §§1–9's machinery, or whether the bundled set is small
enough for something blunter, is the maintainer's call — but it is now a decision about three
first-party manifests rather than about a public extension surface, which is a very different
question. Recorded as **OQ-LP6**.

---

## 7. Migration — the three bundled loopholes

**Nothing migrates.** Bundled stays bundled; the kind exists for the packs nobody has written yet.
That is what makes it an extension point rather than a refactor, which is the whole point of
[`extension-point-principle.md`](extension-point-principle.md).

| Loophole | Verdict | Why |
|---|---|---|
| `claude-oauth-broker` | **stays bundled** | Auto-activates by design (§5.4); its host singleton bypasses its own `host_daemon.cmd` (`loopholesruntime.go:156-159`); its per-jail relay has no manifest vocabulary at all. |
| `host-processes` | **stays bundled** | Its client is `cmd/yolo-ps`, a baked image binary. A pack cannot ship it. |
| `audio` | **stays bundled — and becomes the worked example** | `transport: none`, no daemon: pure `host_bind_mounts` + `host_devices` + `jail_env`. It is the one bundled loophole a pack could carry with **zero new vocabulary and zero host execution**. |

**`audio` is the dogfood, as an example rather than a migration.** Ship a copy under
`docs/examples/` as a pack, which proves the kind end to end — discovery, selection, the footprint
claim, the `--device` and `:ro` bind mounts, teardown — **without** any host process running. That
is the right first instance: it exercises every part of the mechanism except the one part that needs
approval, so the two can be verified independently.

It also exercises the §3.1 refusals honestly: the example must route `PULSE_SERVER` /
`PIPEWIRE_REMOTE` through the `env` kind (losing the conditionality — §3.1's stated cost, visible in
a real artifact rather than argued in prose) and must accept `:ro` on the two audio sockets, which
`audio` itself sets `readonly: false` on because audio frames flow both ways. **So the example
cannot be a byte copy, and where it differs is exactly where the pack subset is narrower.** That is
worth more than a passing example: it is the cost, measured.

---

## 8. Interception survives — and here is the proof

`tls-intercept` retired as a *transport*, but `intercepts` is still parsed, still drives
`--add-host`, and the Apple Container skip now keys on the list rather than a transport name.
Interception is **orthogonal to transport**, and it stays that way.

`intercepts` has exactly one behavioural reader plus two emissions, none of which keys on transport,
source, or origin:

| Site | What it does |
|---|---|
| `internal/loopholes/runtime.go:58` | `if runtime == "container" && len(m.Intercepts) > 0 { continue }` — the AC skip, re-keyed off the transport string by T1 with the reasoning in the comment at `:51-57` |
| `runtime.go:63-65` | `--add-host <intercept.host>:<m.BrokerIP>` per intercept |
| `runtime.go:101-121`, `:162-164` | the `ca_cert` path, joined into `-e NODE_EXTRA_CA_CERTS` |

So a pack-shipped loophole declaring `intercepts` needs **no new mechanism at all**. Two things must
be true, and both already are:

- **`{loophole_dir}`** (`load.go:302`) resolves to the pack's **staged** module dir. That works, and
  it is strictly better than the hand-placed case: the staged tree already went through
  `packstage`'s exec-bit and escaping-symlink refusals, so a pack-shipped intercepting loophole's own
  files passed the content gate that a user-dir loophole's never did.
- **`{state}`** (`load.go:105-106`) resolves to `StateDirFor(name)` under
  `~/.local/share/yolo-jail/state/<name>/`. **Name-keyed, so it is outside the staged tree** and
  survives restaging. That is what makes a pack-shipped CA possible at all — a CA regenerated on
  every launch would break every long-lived TLS client in the jail — and it is also why §4.5's
  retirement rule matters: that dir holds a private key after the pack is gone.

Two things the design must state rather than leave to discovery:

1. **An intercept is its own approvable claim** (§3.3, §4.3), separate from the daemon claim, because
   a `transport: none` loophole with `intercepts` runs no host code and still installs a CA trusted
   by every TLS client in the jail.
2. **On Apple Container a loophole with intercepts is skipped WHOLESALE** (`runtime.go:58`). So a
   pack whose only contribution is an intercepting loophole silently does nothing there. That must be
   **reported by name**, the same rule an ownerless `config-overlay` follows
   ([`pack-system.md`](pack-system.md) §3: *"inert and reported by name"*) — a pack that does nothing
   on this backend is a sentence the user should read, not a silence they should infer.

---

## 9. Risks and open questions

### Risks

**R1 — the static-data invariant inverts in spirit while staying true in letter.**
[`pack-system.md`](pack-system.md) §12's first invariant is *"the manifest stays static data — every
claim readable without executing anything."* A declared argv **is** static data, so the sentence
stays literally true. But its spirit was "reading a manifest tells you everything and costs you
nothing", and now: reading the claim is safe, *selecting* it is host execution. **That sentence must
be sharpened in the same commit that adds the kind**, or the pack system's headline safety property
reads as false to the first person who checks it. `program` already bends it in-jail; a loophole
breaks it on the host.

**R2 — nothing here has run.** No pack-shipped loophole exists. Every claim is design over verified
code paths. In particular the four-call-site discovery convergence (§5.1) and the
publish-after-upstream ordering (§2.1) are the two places I expect a first implementation to be
wrong, because both are orderings rather than shapes and neither is asserted by a test today.

**R3 — the front's limits are invisible to the daemon author.** No EOF propagation (§2.1 hazard 2)
and no per-request access log (hazard 3). The first turns a working daemon into a hang; the second is
an audit gap that only shows up when someone asks what a jail requested.

**R4 — `pack footprint`'s review tail conveys nothing for this kind.** `reviewSummary`
(`internal/cli/pack.go:945-960`) counts by kind, so a loophole reads `1 loophole`. For a host read
that shape is fine; for host execution the count is the least interesting fact. Either it
special-cases or the summary changes shape.

**R5 — doc/code drift found while writing this, all pre-existing.** `pack-capabilities.md:116` cites
`Active()` at `loopholes.go:219`; it is `:232`. `docs/guides/loopholes.md`'s "Manifest schema (v1)"
omits `host_daemon`, `jail_daemon`, `host_bind_mounts`, `host_devices` and `requires` — five loader
keys including **every one that causes a host-side effect** — and marks `description` required when
`load.go:65-72` does not. `internal/config/config.go:74` sets
`knownHostServiceKeys = set("command","env","jail_socket")`, so the `description` and `doctor_cmd`
that `discover.go:41`/`:49` read and that `docs/guides/loopholes.md`'s own example shows are
**unknown-key validation errors**; and `internal/config/validate_loopholes.go:142-158` prefix-checks
`jail_endpoint` while `:114` has already rejected it as unknown, so the key its own function calls
canonical does not validate. And `internal/loopholes/runtime.go:278` still names a `src/loopholes.py`
that no longer exists — `SetEnabled` writes that header into every manifest it toggles.

### Open questions

**OQ-LP1 — where does the loophole manifest schema live?** `packload` cannot import
`internal/loopholes` (cycle, measured in §3.2), and the footprint needs to read the daemon argv.
Recommend extracting `internal/loopholedecl` as a stdlib+decoder leaf; the alternative is breaking
the `loopholes` → `config` edge (two files). **Resolved by:** choosing one. Nothing else in this
design depends on which.

**OQ-LP2 — do `loopholes.command` / `loopholes.env` become user-scope-only now?** My read: **yes**,
independent of this kind, and it is the largest single risk reduction here (§4.3 G1). It is a
breaking config change for anyone who put a `command` in a workspace file. **Resolved by:** a
maintainer ruling, plus a `yolo check` error naming the fix — the same shape `packs` already has.

**OQ-LP3 — `file://` packs run host daemons with no prompt, ever.** Local origin is a `file://`
prefix and is trusted unconditionally and permanently (`internal/config/packs.go:125-127`). My read:
**leave it** — the origin model's coherence is worth more than a special case, and the same is
already true of `mount` and `reads-host`. But a host daemon sharpens it, and a one-time confirmation
for this kind specifically is a defensible alternative. **Resolved by:** a maintainer ruling. Either
way it must be *documented*, not left to be discovered.

**OQ-LP4 — the front's declaration: `publishes` on `host_daemon`, or a `yolo internal front`
subcommand named in the manifest's own argv?** I recommend `publishes` (§2.1) and treat the
subcommand as an implementation detail nobody's manifest names — a manifest naming `yolo` in its argv
is the pack knowing about yolo's CLI, which is the workaround-becomes-API failure
[`extension-point-principle.md`](extension-point-principle.md) exists to prevent. **Not really open**
— recorded because the subcommand is the tempting shortcut and it is a one-way door.

**OQ-LP5 — does `jail_env` stay refused for pack-shipped loopholes?** §3.1 refuses it to avoid a
cross-kind collision pass, at the cost of conditional env. The alternative is that pass, which is
purely additive. **Resolved by:** the first real pack that wants conditional env. Until then the
refusal is right and the cost is documented.

**OQ-LP6 — is A6 still worth building for three bundled loopholes?** With selection as the mechanism,
`pack-capabilities.md` §§1–9 apply only to the bundled set (§6). Whether that still justifies a
capability namespace, or whether three first-party manifests want something blunter, is a genuine
call. **Resolved by:** a maintainer ruling, after this doc is accepted. Note the
extension-point argument cuts *both* ways here: a loophole manifest is still a public surface, so
`serves` is still a field third parties will write — even if only bundled loopholes are ever
superseded.

**OQ-LP7 — the `guest` notch.** A loophole is coherent at `guest` (a real process under an
LSM/Seatbelt profile has a counterparty) and incoherent at `host`, but `Target.Fields()` funnels both
into `HostFields()` (`internal/render/fieldset.go:171`). This kind is the first case where that
funnel is wrong for a *reason* rather than conservatively. **Resolved by:** Phase 7 stating the guest
census. Deliberately not pre-answered here.

---

## What must land together

Ordered, because two of these make the others safe to read:

1. **`loopholes.command`/`env` user-scope-only** (§4.3 G1). Independent of everything else and the
   biggest risk reduction. Ship first.
2. **The front + `publishes`** (§2), then flip `discover.go:60` (§2.2). Closes row **T2**'s last
   clause and makes a third-party daemon's transport a solved problem before any pack ships one.
3. **The server-side spec** in [`loophole-protocol.md`](loophole-protocol.md) (§2.3), labelled the
   unsupervised path.
4. **`internal/loopholedecl`** (OQ-LP1), because the footprint depends on it.
5. **The `loophole` kind** (§3), with the `refusalReasons` entry (§3.4), the two claim classes
   (§3.3), the fatal-collision rule (§3.1), and the retirement-on-deselect rule (§4.5).
6. **`pack-system.md` §12's first invariant, sharpened** (R1) — in the same commit as the kind.
7. **`pack-capabilities.md` rewritten per §6**, and its §10 closed.
8. **The `audio` example pack** (§7), as the end-to-end proof that needs no approval to run.
