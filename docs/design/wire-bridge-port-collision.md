---
title: "Something holds 127.0.0.1:8214 before the bridge does"
date: 2026-09-19
status: in-review
tags: [wire-bridge, providers, networking, diagnosis]
summary: "A launch refused because yolo-jaild wire-bridge could not bind 127.0.0.1:8214 in a container created seconds earlier. The cause was one aliasing defect in host-side composition: ComposeProviders stored the caller's own providers map, the adapter pass then wrote the bridge's jail-loopback address through that alias, and localProviderForwards read it back as a host-loopback forward the USER had asked for — so the in-jail socat took 8214 four lines before the supervisor started the bridge. Fixed by deep-copying the user layer on the way into the composer. Three rival hypotheses were eliminated by reading, and the reasons this cost four hypotheses are recorded because each one is reusable."
---

# Something holds 127.0.0.1:8214 before the bridge does

**Status:** BUILT 2026-09-19 (`3c20a5d8`). MEASURED in composition — reverting the deep copy
turns three tests red, the launch-path one reporting the incident's own `[8214]`. UNMEASURED on
the affected host: no `yolo -p kilo -- pi` has been run against the fix.

> **In short.** A host-side composer that wrote through its own input was enough to bind a port
> inside a container: the adapter pass put the wire bridge's `127.0.0.1:8214` into the *user's*
> `providers` map, so `localProviderForwards` read it back as a forward the user had asked for
> and the in-jail `socat` took the port before the supervisor ever started the bridge.

**Why it matters.** The launch refuses and tears the container down, so the one place the answer
lives — a listener inside the failed jail — was never looked at. That is why this cost three
investigations and four wrong hypotheses.

**The shape.** One aliasing defect in host-side composition
([§2](#2-the-cause-the-adapters-address-was-read-back-as-a-user-request)), made fatal by one
ordering fact in the boot ([§3](#3-the-ordering-is-a-fact-not-a-hypothesis)). The fix is one deep
copy at the composer's read.

**Cost.** Nothing user-visible: the composed table is unchanged and the user's config map is no
longer written. Two follow-ups the investigation surfaced are untouched by the fix — whether an
implicit forward is disclosed ([OQ-PC2](#oq-pc2)) and the orphan reclaimer's remedy
([OQ-PC3](#oq-pc3)).

**Start at [§2](#2-the-cause-the-adapters-address-was-read-back-as-a-user-request)** — the cause
and its fix; the rest of the doc is the elimination of its rivals and the reasons they cost so
much.

**Needs your ruling:** [OQ-PC2](#oq-pc2), [OQ-PC3](#oq-pc3).

**Reads with:** [`wire-bridge.md`](../reference/wire-bridge.md) (the bridge as designed — the
listen port comes from the adapter's declared `address` and nowhere else, and it is this doc's
graduation target), [`../reference/protocol-resolution.md`](../reference/protocol-resolution.md)
(the adapter injection this doc found leaking),
[`diagnostics-past-the-boundary.md`](diagnostics-past-the-boundary.md) (the instrument gap
[§4.6](#46-why-this-cost-four-hypotheses) names, with this failure as its worked case),
[`loopback-tls-reachability.md`](../reference/loopback-tls-reachability.md) (the witness that
emits the second, misleading error).

---

## 1. The failure, and what the code says about it

### 1.1 What was reported

On a rootless podman host (pasta), default bridge mode, `yolo -p kilo -- pi` in
`~/code/juce-projects`:

```text
Error: start_jail_daemon_supervisor: jail daemon "wire-bridge" cannot publish its
required endpoint: cannot bind 127.0.0.1:8214: listen tcp 127.0.0.1:8214: bind:
address already in use
```

Two facts about that string are worth naming, because both misled an investigation:

- **It is the FIRST bind attempt, not a later one.** The text arrives through the readiness pipe
  (`internal/entrypoint/runtime.go`, the `failed` branch of `startJailDaemonSupervisor`), and the
  entrypoint stops reading the pipe as soon as every required name has reported. A bridge that
  bound successfully and failed on some later restart could not produce this message at all.
- **The follow-on line calling wire-bridge a "host service" is wording, not classification.** It
  comes from the shared reachability reporter and from `hostServiceEnvVar(bridge.Name)` in
  `internal/cli/run/packservices.go`; `yolo requested host-loopback forwarding` is this launch's
  `YOLO_HOST_LOOPBACK` **disposition**, which is per-launch and says nothing about which service
  was forwarded. Neither line means wire-bridge was given a forward.

### 1.2 What is established

| Established | Holds up? |
| :--- | :--- |
| Bridge mode, private netns; no other jail can reach this loopback | **Yes.** `/proc` and the netns are the container's own — yolo passes no `--pid` flag (only `--pids-limit`, `internal/cli/run/backendcaps.go`). |
| Nothing on the host holds 8214 (`ss -ltnp` empty) | **Yes — and it does not exclude a forward.** The host half of a forward is `socat UNIX-LISTEN:…`, a filesystem socket (`internal/cli/run/hostports.go`, `SocatArgv`). The jail half is the TCP listener. Measured in this jail: with nothing on the host's 8214, `ss -ltn \| grep -c 8214` on the host side is `0` while the jail-side `socat` holds `127.0.0.1:8214`. |
| No `8214` anywhere in `~/.config/yolo-jail/` | **Yes, and it is not exculpatory** — the number is composed at launch, not written by the user. See [§2](#2-the-cause-the-adapters-address-was-read-back-as-a-user-request). |
| Service names are deduped across packs | **Yes.** `serviceEndpointEnvArgs` takes the first `wire-bridge` contribution and the name is sole-owned, so two `needs`-ing packs yield one daemon, one env var, one readiness name. |
| The clean isolated-HOME repro came up with nothing on 8214 | **Yes, and it could not have reproduced this.** That config declares no `providers` of its own, which is the precondition [§2.3](#23-the-precondition-stated-exactly) needs. |

---

## 2. The cause: the adapter's address was read back as a user request

**Fixed in `3c20a5d8`** by deep-copying the user layer on the way into `ComposeProviders`. The
mechanism below is kept because it is a trap with three other ways in, not because it is history:
a composer that stores a value its caller still owns is a writer of its caller's state, and this
was the first consumer to be caught by it rather than the only one exposed.

### 2.1 The design intent, stated in the tree — three days before the producer broke it

`internal/cli/run/providerlocal.go` opens by drawing the boundary this defect crossed:

> User-declared provider URLs are a host-facing configuration surface: a user who writes
> localhost is naming an inference server on the machine that launches yolo, not a service an
> agent happened to start inside its private network namespace. **Packs are deliberately
> excluded — their endpoints are service facts, and a pack such as wire-bridge legitimately
> names jail loopback.**

That comment landed with the consumer in `bf6a6e52` (2026-09-15). The producer that broke it —
`adaptEndpoints` and `addEndpoint`, the adapter pass — landed in `b7373476` (2026-09-18), three
days later. **The rule was already written down, in the file that depends on it, and nothing
read it.** A rule stated only as a comment on the reader cannot fail a build; the same invariant
is now stated on the *writer* as well, at `ComposeProviders`, and pinned by a test that runs the
real composition.

### 2.2 The mechanism, in four steps

1. **`ComposeProviders` put the USER'S OWN map object into the composed table.** For a provider
   name no selected pack ships, `out.Set(name, u)` stored `u` — the caller's
   `*jsonx.OrderedMap` — by reference, not by copy. (`mergeUnder`'s `dst.Set(k, v)` aliased the
   same way one level down, for any key the pack-shipped entry lacked, which is how the `claude`
   pack's endpoint-less `bedrock` row could acquire the user's `endpoints` map.)
2. **The adapter pass then wrote through that alias.** `adaptEndpoints` runs LAST over the
   finished table and calls `addEndpoint`, which sets `endpoints.anthropic.base_url` to the
   wire-bridge pack's `http://127.0.0.1:8214`. The write landed in the user's config map.
3. **`localProviderForwards` then read the poisoned map.** In
   `internal/cli/run/profilechannel.go`, `composedProviders(cfg, packs)` runs and
   `localProviderForwards(cfgMap(cfg, "providers"))` runs fourteen lines later — **the same
   `cfgMap(cfg, "providers")` pointer, after the mutation.** It saw a loopback URL and reported
   port 8214 as a host-loopback forward the user had asked for.
4. **The launch forwarded it.** `applied == "bridge"` merges the implicit forwards into
   `forwardHostPorts` (`mergeHostForwards`, from two call sites in `internal/cli/run`), the host
   started `socat UNIX-LISTEN:…/port-8214.sock … TCP:127.0.0.1:8214`, and
   `YOLO_FORWARD_HOST_PORTS=[…,8214]` crossed into the jail.

Steps 1–3 were **run, not reasoned about**, against this tree on 2026-09-19 — a throwaway test
compiled into `internal/packload` and `internal/cli/run` via `go test -overlay`, which the fix
then landed as two permanent tests:

```text
user BEFORE: {"myprovider": {"endpoints": {"openai": {...}}, "api_key_env_name": "..."}}
user AFTER:  {"myprovider": {"endpoints": {"openai": {...},
                             "anthropic": {"base_url": "http://127.0.0.1:8214"}}, ...}}
forwards BEFORE compose: []
forwards AFTER compose:  [8214]
merged forward_host_ports: [8214]
```

> [!WARNING]
> **The copy has to be DEEP, and a one-level clone looks sufficient.** The value is a tree: clone
> only the provider entry and `endpoints` stays shared, so `addEndpoint` writes through it
> unchanged and nothing about the symptom changes. `jsonx.DeepCopy` is the copier and it lowers
> nothing — unlike `jsonx.Plain`, key order and integer literals survive, so a copied config
> still re-encodes byte-identically. One copy at the read covers all four sinks: the malformed
> passthrough, both `out.Set(name, u)` paths, and `mergeUnder` writing sub-values of the user's
> entry into a pack-shipped one.

### 2.3 The precondition, stated exactly

The alias only existed for a `providers.<name>` entry that **no selected pack ships**. The packs
that ship providers are `cerebras`, `kilo`, `zai`, `openrouter`, `llamacpp`, `bedrock` (from
`claude`) and `openai-codex` (from `openai-auth`); those compose into a fresh entry
(`shippedProviderEntry` allocates every level) and were always safe. `providers.<name>.endpoints`
is refused in a workspace config (`config.validateProviderAddressScope`), so the entry had to be
**user-scope**.

So the config that fired this was:

> a `providers.<name>` in `~/.config/yolo-jail/config.jsonc` where `<name>` is not one of the
> seven above, carrying `endpoints.openai` (or `endpoints.openai-responses`, which got 8215 the
> same way) and **no** `endpoints.anthropic`.

That is not an exotic config — it is the shipped shape for a local inference server, which
[`local-model-endpoints.md`](../research/local-model-endpoints.md) ruled into the provider system
deliberately, and it is the exact case `localProviderForwards` exists to serve.

> [!NOTE]
> **`grep 8214 ~/.config/yolo-jail/` returning nothing never cleared this**, and that is why the
> first two investigations discounted the config. The number is the wire-bridge pack's, composed
> in at launch; the user's config only had to name a provider for it to land there.

### 2.4 Why no disclosure caught it

Nothing prints the implicit forward, and the fix did not change that. The briefing's **Forwarded
Host Ports** section is fed by `briefingPortsFor` (`internal/cli/run/prepare.go`), which reads
`network.forward_host_ports` off the config section only — the `localProviderForwards`
contribution is merged at two other call sites and reaches no briefing, no banner and no launch
line. Whether it should is [OQ-PC2](#oq-pc2).

### 2.5 Why the unit test did not catch it

`TestLocalProviderForwardsFindsOnlyUserLoopbackURLs`
(`internal/cli/run/providerlocal_test.go`) builds a providers map by hand and calls
`localProviderForwards` on it. It is the exact shape [`AGENTS.md`](../../AGENTS.md) calls "a test
that pins the CALLEE while the CALL SITE is unpinned": the function is correct on every input it
is given, and nothing in the suite ever gave it an input that had been through
`ComposeProviders`. **That test is still hand-built and still correct to be** — the fix added the
missing kind rather than converting it. `internal/cli/run/providerforwardalias_test.go` now runs
`composedProviders` and `localProviderForwards` in the launch's own order and fails with the
reported `[8214]` if the copy is reverted; `internal/packload/provideraliasing_test.go` composes
the shipped wire-bridge adapter under a user-declared local-inference provider and asserts the
caller's map is byte-identical and shares no object by identity.

---

## 3. The ordering is a fact, not a hypothesis

`internal/entrypoint/boot.go` runs, in this order and four lines apart:

```text
startContainerPortForwarding(e)      // binds YOLO_FORWARD_HOST_PORTS in the jail
genStep(e, "start_jail_daemon_supervisor", …)
```

The forwarder is `startContainerPortForwarding` (`internal/entrypoint/runtime.go`). It skips a
port already held (`portInUse`) and otherwise spawns
`socat TCP-LISTEN:<port>,bind=127.0.0.1,fork,reuseaddr …`. So on a launch carrying a forward for
8214, the socat **always** wins: it runs first, unconditionally, and its `reuseaddr` does not
help the loser.

This is what turned [§2](#2-the-cause-the-adapters-address-was-read-back-as-a-user-request) from
a wrong table entry into a refused launch. It is also why the error was the bridge's and not
socat's: the bridge is the second binder, so it is the one that reports. **The ordering was not
changed and does not need to be** — nothing legitimate ever puts an adapter's address in the
forward list, so the fix was to stop the list acquiring one.

---

## 4. What I eliminated by reading

### 4.1 Hypothesis zero — the idle crash left something on the port. **Eliminated.**

The crash `5e117131 fix(wire-bridge): a healthy idle crashed the daemon instead of idling` fixed
was in `idleUntilStopped`, and **every branch that reaches it returns before the bind**: `serve`
(`internal/wirebridged/boot.go`) idles on a missing Codex endpoint or a missing provider
credential, and `net.Listen` is below both. A crashed predecessor therefore never held 8214, and
a restart loop of crashing idles reports *"provider credential is unavailable"*, not a bind
error.

This also means the report **persisting after that fix was not evidence for it** — the fix was
always orthogonal. It is the cheapest way to spend an investigation: a real defect, fixed, in the
same daemon, on the same day.

### 4.2 The bridge collides with itself. **Eliminated for a fresh container.**

Three independent reasons, any one sufficient:

- **Restarts are strictly sequential.** `child.waitAndMaybeRestart`
  (`internal/supervisor/supervisor.go`) calls `cmd.Wait()` and only then returns to
  `superviseOne`'s `c.start()`. The predecessor is reaped — and its listener closed — before the
  successor is spawned. There is no window.
- **No TIME_WAIT class either.** Go's `net.Listen` sets `SO_REUSEADDR` on every TCP listener
  (`$GOROOT/src/net/sockopt_linux.go`), so even a port littered with `TIME_WAIT` sockets rebinds.
- **Two supervisors cannot coexist in a fresh container.** `anyJailDaemonSupervisorAlive`
  (`internal/entrypoint/runtime.go`) reads PID files on `/tmp`, which is tmpfs and therefore
  empty in a container created seconds ago; the entrypoint runs once per fresh container.

### 4.3 Some other adapter or service lands on 8214. **Eliminated for the shipped tree.**

The only adapters declared anywhere in this tree are `packs/wire-bridge`'s two — 8214
(`openai → anthropic`) and 8215 (`openai-responses → anthropic`). The other fixed loopback ports
a launch arranges are `127.0.0.1:1460` (`openai-auth-broker`) and `127.0.0.1:1461` (`aws-auth`),
and **both of those daemons are `scope: "host"`** — they run on the host and the jail reaches them
through the forwarded loopback, so neither is a candidate for holding a port inside the container
at all. The `adapters` config key can only move an address to a **literal** the user writes, is
user-scope only
(`internal/config/adapters.go`), and no `8214` appeared in either machine's user config.

> [!NOTE]
> This elimination is about the packs yolo ships. The affected host also selects **local packs**,
> which I could not read, and a local pack may declare an `adapter`, a `service` with a
> `jail_daemon`, or a `provider` of its own. A second declared `127.0.0.1:8214` would be a
> different bug with the same message, and the fix in
> [§2](#2-the-cause-the-adapters-address-was-read-back-as-a-user-request) would not touch it.

Two providers both routed at the bridge does not double anything either: `routeFor`
(`internal/wirebridged/boot.go`) walks candidates in sorted agent order and **returns the
first**, so `cerebras` and `kilo` both carrying the injected `127.0.0.1:8214` yields one
listener.

### 4.4 The previous handoff's leading hypothesis. **Right room, wrong door.**

[`scratch/wire-bridge-handoff.md`](../../scratch/wire-bridge-handoff.md) proposed that yolo
classifies wire-bridge as a host-facing endpoint and arranges a forwarding listener for it, which
then occupies 8214. **A forwarding listener did occupy 8214 — for an unrelated reason.**
`forwardHostPorts` has exactly two sources, `network.forward_host_ports` and
`localProviderForwards` (`internal/cli/run/assemble.go`, `internal/cli/run/run.go`);
`internal/cli/run/packservices.go` composes only `YOLO_JAIL_DAEMONS` and two `-e` vars and adds no
forward. The service classification is a wording defect that cost this investigation days, and it
is worth fixing on that ground alone — but it was not the cause, and it is still there.

### 4.5 `TestNonStreamRoundTrip`'s `connection reset by peer`. **Noise.**

The test stands up two `httptest` servers on ephemeral ports and posts with the default HTTP
client, whose transport pools keep-alive connections across tests
(`internal/wirebridged/handler_test.go`). A reset from a pooled connection to a just-closed
`httptest` server is the classic form of that flake. No fixed port is involved and 8214 never
appears. Unrelated.

### 4.6 Why this cost four hypotheses

Three reasons, each verified against the tree, and each one reusable on a failure that has
nothing to do with ports:

1. **The invariant was already written down — on the READER.** `providerlocal.go`'s comment
   ([§2.1](#21-the-design-intent-stated-in-the-tree--three-days-before-the-producer-broke-it))
   states the exact rule the adapter pass went on to break three days later. A rule stated as a
   comment on the consumer costs nothing to write and cannot fail a build; the producer is where
   it has to be enforced.
2. **The test suite pinned the callee and left the call site open.**
   [§2.5](#25-why-the-unit-test-did-not-catch-it). `localProviderForwards` was tested on inputs
   nobody composed, so the composition could acquire a new behaviour with the gate green.
3. **The decisive measurement — which process in the jail holds the listening socket — existed
   nowhere.** Host-side `ss -ltnp` was empty because the host half of a forward is a UNIX socket,
   and the container was gone: it runs with `--rm` and the entrypoint's refusal exits, so the
   process table, the listeners and the daemon state go with it. That is the gap
   [`diagnostics-past-the-boundary.md`](diagnostics-past-the-boundary.md) is about, and its first
   half shipped the same day as this fix: `YOLO_HOLD_ON_REFUSAL=1` (`6c54c575`) makes a refused
   boot print how to get in and then block, so the failed state can be `podman exec`'d into
   instead of reconstructed.

---

## 5. What the measurements found

Six measurements were written to settle this on the affected host. They are not reproduced here,
because the code answered first: steps 1–3 of
[§2.2](#22-the-mechanism-in-four-steps) were reproduced in-process against this tree, which made
the config-reading and log-reading probes redundant and turned the diagnosis into a fix. Two
notes worth keeping from them:

- **The bridge's own post-mortem survives teardown**, in the workspace overlay at
  `<workspace>/.yolo/home/local/state/yolo-jail-daemons/wire-bridge.log` — and
  `<cname>-socat.log` under `~/.local/share/yolo-jail/logs/` is created only when a launch has at
  least one forward (`startHostPortForwarding`, `internal/cli/run/network.go`), so its existence
  alone is evidence of a forward on a config that declares none.
- **A nested launch is the one place a genuine cross-jail 8214 collision IS possible**, because
  it is forced onto `--net=host` and the two jails share one loopback. `YOLO_VERSION` empty
  proves a failing launch was not that; it was checked, and it was not.

---

## 6. The orphan reclaimer — is its own stated purpose real?

`c450d991 fix(supervisor): reclaim orphaned jail daemons` adds `reclaimOrphanedJailDaemons`
(`internal/entrypoint/runtime.go`): after **both** supervisor PID files prove dead, walk `/proc`,
and `SIGKILL` any process whose full argv matches a configured daemon.

**The handoff's argument is correct about this incident, and for the right reason.** `/proc`
inside a podman container is the container's own PID namespace — yolo passes no `--pid` flag — so
the scan cannot see another jail's processes at all, and a container created seconds ago has an
empty tmpfs `/tmp` and no daemon of its own. On the launch that failed, the reclaimer found
nothing and killed nothing. It is not an explanation, and "another jail owns this jail's
127.0.0.1:8214" is not a thing that can happen in bridge mode.

**Its own stated purpose is a different claim, and that claim is real.** The failure it is written
against is a daemon reparented to PID 1 **inside one container** after its supervisor died, and
every link of that chain exists:

- the supervisor is started detached as a child of the entrypoint, which then `exec`s bash as
  PID 1 — so a daemon whose supervisor dies reparents to PID 1 and **keeps its listener**;
- the entrypoint runs again in the **same** container on every attach
  (`podman exec yolo-entrypoint`), and `anyJailDaemonSupervisorAlive` correctly answers "no" once
  that supervisor is gone;
- the new supervisor then spawns a new `yolo-jaild wire-bridge` against the orphan's live 8214 —
  **and it prints the same string this incident printed.**

So: the shape is representable, it is scoped to one container, and it is one of two faults that
share a symptom. What is missing is any observed instance — the supervisor blocks on `<-stop` for
the life of the jail (`supervisor.Run`), so reaching this state needs it to be OOM-killed,
signalled, or to panic. My read: **keep the purpose, distrust the remedy.** A `SIGKILL` by argv
match is a strong action taken on an inference, and a second supervisor adopting the live daemon —
or simply refusing with the orphan's PID named — answers the same failure without killing
anything. That is [OQ-PC3](#oq-pc3), and the reclaimer still kills.

---

## 7. What this does not cover

- **The `macos-user` backend is out of scope.** There is no network namespace there, so an
  adapter's ports are host ports and the same leak has a different blast radius; it needs its own
  pass. The fix in [§2](#2-the-cause-the-adapters-address-was-read-back-as-a-user-request) is
  backend-independent — it is host-side composition — but the *consequence* of a leak there is
  not.
- **The wording defects are not fixed** — wire-bridge reported as a "host service", the
  host-loopback disposition quoted as if it named the service. Both are named in
  [§4.4](#44-the-previous-handoffs-leading-hypothesis-right-room-wrong-door) and both still
  print.
- **Nothing here was measured on the affected host.** Everything in
  [§2](#2-the-cause-the-adapters-address-was-read-back-as-a-user-request) is code behaviour
  reproduced in this jail; that the fix ends the reported failure is an inference from the same
  reproduction, not an observation of that launch succeeding.
- **No disclosure was added.** [OQ-PC2](#oq-pc2).

---

## Open Questions

1. <a id="oq-pc2"></a>💬 **[OQ-PC2](#oq-pc2): Should an implicit provider forward be disclosed?**
   A port the user never wrote is bound inside their jail and named nowhere — not the briefing
   ([§2.4](#24-why-no-disclosure-caught-it)), not the banner, not `launch.log`. The aliasing bug
   is fixed, so the ports in that list are legitimate again; this decides whether a legitimate
   one is still silent. [`report-tiers.md`](../reference/report-tiers.md) has the standing rule
   that a disclosure is never suppressible.

   <!-- vantage: oq id=OQ-PC2 leaning="Yes — one line naming each implicitly forwarded port and the provider that asked for it, and the briefing's Forwarded Host Ports section fed from the merged list rather than from the config section." -->

   _Leaning:_ Yes, and cheaply: one launch line naming each implicit port with the provider that
   asked for it, and `briefingPortsFor` fed from the merged list rather than from `netSec`. A
   forward is a hole into the host; it is exactly the class that may not be silent. It is also
   the disclosure that would have named this bug on the first launch.

   **Answer:**
   > _(empty — fill in when decided)_

2. <a id="oq-pc3"></a>💬 **[OQ-PC3](#oq-pc3): Keep, narrow, or replace the orphan reclaimer?**
   [§6](#6-the-orphan-reclaimer--is-its-own-stated-purpose-real) finds its purpose real and its
   remedy strong: `SIGKILL` on an argv match, from an inference about ownership, with no observed
   instance of the failure it treats. The handoff asks for a straight revert. Nothing about the
   [§2](#2-the-cause-the-adapters-address-was-read-back-as-a-user-request) fix touches it, and it
   still kills.

   <!-- vantage: oq id=OQ-PC3 leaning="Keep the detection, change the action: the second supervisor adopts the live daemon, or refuses naming the orphan's PID. A revert loses a real in-container failure mode that prints this same error." -->

   _Leaning:_ Keep the detection, change the action — adopt the live daemon, or refuse and name
   its PID. A straight revert throws away the only guard against an in-container fault that
   prints this identical error.

   **Answer:**
   > _(empty — fill in when decided)_

---

## Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| OQ-PC1 | **Fix the mutation, not the read.** `ComposeProviders` deep-copies the user's entry on the way in, so the composed table holds nothing the caller owns; the read was left where it is. A composer that writes through its input is a one-writer violation, and `localProviderForwards` was the first consumer caught by it rather than the only one exposed — reordering that one read would have left every later reader of `cfg` looking at an edited config. Answered by `3c20a5d8` choosing a seam, not by a ruling. | 2026-09-19 | [§2](#2-the-cause-the-adapters-address-was-read-back-as-a-user-request), and `ComposeProviders`' own doc comment | ✅ |

> [!NOTE]
> **Graduation waits on the two questions above.** The durable half of this doc — what can hold
> the port before the bridge does, the ordering fact in
> [§3](#3-the-ordering-is-a-fact-not-a-hypothesis), and the invariant that packs' endpoints are
> service facts while user providers are forwarded — is reference material, and
> [`wire-bridge.md`](../reference/wire-bridge.md) is its home. Source comments cite this file by
> section number — `2`, `2.2`, `2.5` and `3` — and a sibling design doc links it, so when it does
> graduate it leaves a stub with a mapping table, the way
> [`protocol-resolution.md`](protocol-resolution.md) did — it is not deleted.
