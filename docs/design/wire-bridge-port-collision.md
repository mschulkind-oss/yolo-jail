---
title: "Something holds 127.0.0.1:8214 before the bridge does"
date: 2026-09-19
status: in-review
tags: [wire-bridge, providers, networking, diagnosis]
summary: "A launch refuses because yolo-jaild wire-bridge cannot bind 127.0.0.1:8214 in a container that was created seconds earlier. Reading the code finds one mechanism that produces exactly that, and it is not a second bridge: ComposeProviders mutates the caller's own providers map, so the adapter's jail-loopback address is read back as if the USER had asked for a host-loopback forward, and the in-jail socat for that forward binds 8214 four lines before the supervisor starts. Three rival hypotheses are eliminated by reading. Every hypothesis carries the one command that settles it on the affected host."
---

# Something holds 127.0.0.1:8214 before the bridge does

**Status:** DESIGN, 2026-09-19 — a diagnosis, nothing built. Evidence read against the tree at `5e117131`.

> **In short.** Nothing else in the jail wants port 8214 — but `localProviderForwards`
> is handed a providers map that `ComposeProviders` has already written the bridge's own
> address into, so the launcher forwards 8214 as though the user had asked for it, and
> the in-jail `socat` takes the port before the supervisor is started.

**Why it matters.** The launch refuses and tears the container down, so the one place the
answer lives — a listener inside the failed jail — has never been looked at. That is why this
has cost two investigations.

**The shape.** One aliasing defect in host-side composition
([§2](#2-the-primary-hypothesis-the-adapters-address-is-read-back-as-a-user-request)),
made fatal by one ordering fact in the boot
([§3](#3-the-ordering-is-a-fact-not-a-hypothesis)).

**Cost.** No fix is proposed here; the remedies are two different seams and which one is
load-bearing is a ruling ([OQ-PC1](#oq-pc1)).

**Start at [§2](#2-the-primary-hypothesis-the-adapters-address-is-read-back-as-a-user-request)** —
it is the only hypothesis that survives reading, and the rest of the doc is the elimination of
its rivals and the commands that settle it.

**Needs your ruling:** [OQ-PC1](#oq-pc1), [OQ-PC2](#oq-pc2), [OQ-PC3](#oq-pc3).

**Reads with:** [`wire-bridge.md`](../reference/wire-bridge.md) (the bridge as designed —
the listen port comes from the provider's `anthropic` base_url and nowhere else),
[`../reference/protocol-resolution.md`](../reference/protocol-resolution.md) (the adapter injection this
doc finds leaking), [`loopback-tls-reachability.md`](../reference/loopback-tls-reachability.md)
(the witness that emits the second, misleading error).

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

Two facts about that string are worth naming before anything else, because both have
already misled one investigation:

- **It is the FIRST bind attempt, not a later one.** The text arrives through the readiness
  pipe (`internal/entrypoint/runtime.go`, the `failed` branch of
  `startJailDaemonSupervisor`), and the entrypoint stops reading the pipe as soon as every
  required name has reported. A bridge that bound successfully and failed on some later
  restart could not produce this message at all.
- **The follow-on line calling wire-bridge a "host service" is wording, not classification.**
  It comes from the shared reachability reporter and from `hostServiceEnvVar(bridge.Name)` in
  `internal/cli/run/packservices.go`; `yolo requested host-loopback forwarding` is this
  launch's `YOLO_HOST_LOOPBACK` **disposition**, which is per-launch and says nothing about
  which service was forwarded. Neither line means wire-bridge was given a forward.

### 1.2 What is established, and what I re-checked

| Established | Holds up? |
| :--- | :--- |
| Bridge mode, private netns; no other jail can reach this loopback | **Yes.** `/proc` and the netns are the container's own — yolo passes no `--pid` flag (only `--pids-limit`, `internal/cli/run/backendcaps.go`). |
| Nothing on the host holds 8214 (`ss -ltnp` empty) | **Yes — and it does not exclude a forward.** The host half of a forward is `socat UNIX-LISTEN:…`, a filesystem socket (`internal/cli/run/hostports.go`, `SocatArgv`). The jail half is the TCP listener. Measured in this jail: with nothing on the host's 8214, `ss -ltn \| grep -c 8214` on the host side is `0` while the jail-side `socat` holds `127.0.0.1:8214`. |
| No `8214` anywhere in `~/.config/yolo-jail/` | **Yes, and it is not exculpatory** — see [§2](#2-the-primary-hypothesis-the-adapters-address-is-read-back-as-a-user-request). The number is composed at launch, not written by the user. |
| Service names are deduped across packs | **Yes.** `serviceEndpointEnvArgs` takes the first `wire-bridge` contribution and the name is sole-owned, so two `needs`-ing packs yield one daemon, one env var, one readiness name. |
| The clean isolated-HOME repro came up with nothing on 8214 | **Yes, and it could not have reproduced this.** That config declares no `providers` of its own, which is the precondition [§2](#2-the-primary-hypothesis-the-adapters-address-is-read-back-as-a-user-request) turns on. |

---

## 2. The primary hypothesis: the adapter's address is read back as a user request

**Confidence: high.** The mechanism is proven by running the code; only its precondition —
one line in the user's config — is unverified.

### 2.1 The design intent, stated in the tree

`internal/cli/run/providerlocal.go` opens by drawing the boundary this defect crosses:

> User-declared provider URLs are a host-facing configuration surface: a user who writes
> localhost is naming an inference server on the machine that launches yolo, not a service
> an agent happened to start inside its private network namespace. **Packs are deliberately
> excluded — their endpoints are service facts, and a pack such as wire-bridge legitimately
> names jail loopback.**

So the rule is right and it is written down. What fails is that by the time
`localProviderForwards` reads the user's map, a pack's address is inside it.

### 2.2 The mechanism, in four steps

1. **`ComposeProviders` puts the USER'S OWN map object into the composed table.** For a
   provider name no selected pack ships, `internal/packload/providers.go:95`/`:100` does
   `out.Set(name, u)` — `u` is the caller's `*jsonx.OrderedMap`, stored by reference, not
   copied. (`mergeUnder`'s `dst.Set(k, v)` at `:556` aliases the same way, one level down,
   for any key the pack-shipped entry lacks — which is how the `claude` pack's endpoint-less
   `bedrock` row can acquire the user's `endpoints` map.)
2. **The adapter pass then writes through that alias.** `adaptEndpoints`
   (`internal/packload/providers.go:141`) runs LAST over the finished table and calls
   `addEndpoint` (`:184`), which sets `endpoints.anthropic.base_url` to the wire-bridge
   pack's `http://127.0.0.1:8214`. The write lands in the user's config map.
3. **`localProviderForwards` then reads the poisoned map.** In
   `internal/cli/run/profilechannel.go`, `composedProviders(cfg, packs)` runs at `:114` and
   `localProviderForwards(cfgMap(cfg, "providers"))` at `:128` — **the same
   `cfgMap(cfg, "providers")` pointer, after the mutation.** It sees a loopback URL and
   reports port 8214 as a host-loopback forward the user asked for.
4. **The launch forwards it.** `applied == "bridge"` merges the implicit forwards into
   `forwardHostPorts` (`internal/cli/run/assemble.go:312`, `internal/cli/run/run.go:1246`),
   the host starts `socat UNIX-LISTEN:…/port-8214.sock … TCP:127.0.0.1:8214`, and
   `YOLO_FORWARD_HOST_PORTS=[…,8214]` crosses into the jail.

Steps 1–3 were **run, not reasoned about**, against this tree on 2026-09-19 (a throwaway
test compiled into `internal/packload` and `internal/cli/run` via `go test -overlay`, kept
out of the tree):

```text
user BEFORE: {"myprovider": {"endpoints": {"openai": {...}}, "api_key_env_name": "..."}}
user AFTER:  {"myprovider": {"endpoints": {"openai": {...},
                             "anthropic": {"base_url": "http://127.0.0.1:8214"}}, ...}}
forwards BEFORE compose: []
forwards AFTER compose:  [8214]
merged forward_host_ports: [8214]
```

### 2.3 The precondition, stated exactly

The alias only exists for a `providers.<name>` entry that **no selected pack ships**. The
packs that ship providers are `cerebras`, `kilo`, `zai`, `openrouter`, `llamacpp`, `bedrock`
(from `claude`) and `openai-codex` (from `openai-auth`); those compose into a fresh entry and
are safe. `providers.<name>.endpoints` is refused in a workspace config
(`config.validateProviderAddressScope`), so the entry must be **user-scope**.

So the config that fires this is:

> a `providers.<name>` in `~/.config/yolo-jail/config.jsonc` where `<name>` is not one of the
> seven above, carrying `endpoints.openai` (or `endpoints.openai-responses`, which gets 8215
> the same way) and **no** `endpoints.anthropic`.

That is not an exotic config — it is the shipped shape for a local inference server, which
[`local-model-endpoints.md`](../research/local-model-endpoints.md) ruled into the provider
system deliberately, and it is the exact case `localProviderForwards` exists to serve.

> [!WARNING]
> **`grep 8214 ~/.config/yolo-jail/` returning nothing does not clear this.** The number is
> the wire-bridge pack's, composed in at launch; the user's config only has to name a
> provider for it to land there.

### 2.4 Why no disclosure caught it

Nothing prints the implicit forward. The briefing's **Forwarded Host Ports** section is fed
by `briefingPortsFor` (`internal/cli/run/prepare.go:60`, defined at `:279`), which reads
`network.forward_host_ports` off the config section only — the `localProviderForwards`
contribution is merged in two other call sites and reaches no briefing, no banner and no
launch line.

### 2.5 Why the unit test did not catch it

`TestLocalProviderForwardsFindsOnlyUserLoopbackURLs`
(`internal/cli/run/providerlocal_test.go:10`) builds a providers map by hand and calls
`localProviderForwards` on it. It is the exact shape
[`AGENTS.md`](../../AGENTS.md) calls "a test that pins the CALLEE while the CALL SITE is
unpinned": the function is correct on every input it is given, and nothing in the suite ever
gives it an input that has been through `ComposeProviders`.

**The measurement:** [M1](#m1) (settles it with no launch at all), corroborated by
[M2](#m2) and [M3](#m3).

---

## 3. The ordering is a fact, not a hypothesis

`internal/entrypoint/boot.go` runs, in this order and four lines apart:

```text
648  startContainerPortForwarding(e)      // binds YOLO_FORWARD_HOST_PORTS in the jail
652  genStep(e, "start_jail_daemon_supervisor", …)
```

The forwarder is `startContainerPortForwarding` (`internal/entrypoint/runtime.go:348`). It
skips a port already held (`portInUse`, `:336`/`:385`) and otherwise spawns
`socat TCP-LISTEN:<port>,bind=127.0.0.1,fork,reuseaddr …` (`:396`). So on a launch carrying a
forward for 8214, the socat **always** wins: it runs first, unconditionally, and its
`reuseaddr` does not help the loser.

This is what turns [§2](#2-the-primary-hypothesis-the-adapters-address-is-read-back-as-a-user-request)
from a wrong table entry into a refused launch. It is also the reason the error is the
bridge's and not socat's: the bridge is the second binder, so it is the one that reports.

---

## 4. What I eliminated by reading

### 4.1 Hypothesis zero — the idle crash left something on the port. **Eliminated.**

The crash `fix(wire-bridge): a healthy idle crashed the daemon instead of idling` fixed
was in `idleUntilStopped`, and **every branch that reaches it returns before the bind**:
`serve` (`internal/wirebridged/boot.go`) idles on a missing Codex endpoint or a missing
provider credential, and `net.Listen` is at `:213`, below both. A crashed predecessor
therefore never held 8214, and a restart loop of crashing idles reports
*"provider credential is unavailable"*, not a bind error.

This also means the report **persisting after that fix is not evidence for it** — the fix was
always orthogonal. **The measurement:** [M6](#m6), which is worth running only to close the
question out loud.

### 4.2 The bridge collides with itself. **Eliminated for a fresh container.**

Three independent reasons, any one sufficient:

- **Restarts are strictly sequential.** `child.waitAndMaybeRestart`
  (`internal/supervisor/supervisor.go:209`) calls `cmd.Wait()` and only then returns to
  `superviseOne`'s `c.start()` (`:274`/`:286`). The predecessor is reaped — and its listener
  closed — before the successor is spawned. There is no window.
- **No TIME_WAIT class either.** Go's `net.Listen` sets `SO_REUSEADDR` on every TCP listener
  (`$GOROOT/src/net/sockopt_linux.go`), so even a port littered with `TIME_WAIT` sockets
  rebinds.
- **Two supervisors cannot coexist in a fresh container.** `anyJailDaemonSupervisorAlive`
  (`internal/entrypoint/runtime.go:240`) reads PID files on `/tmp`, which is tmpfs and
  therefore empty in a container created seconds ago; the entrypoint runs once per fresh
  container.

**The measurement:** [M4](#m4).

### 4.3 Some other adapter or service lands on 8214. **Eliminated for the shipped tree.**

The only adapters declared anywhere in this tree are `packs/wire-bridge`'s two —
8214 (`openai → anthropic`) and 8215 (`openai-responses → anthropic`). The other in-jail
daemons are the OpenAI auth adapter on `127.0.0.1:1460` and the AWS one on `127.0.0.1:1461`.
The `adapters` config key can only move an address to a **literal** the user writes, is
user-scope only (`internal/config/adapters.go`), and no `8214` appears in either machine's
user config.

> [!NOTE]
> This elimination is about the packs yolo ships. The affected host also selects **local
> packs**, which I cannot read, and a local pack may declare an `adapter`, a `service` with a
> `jail_daemon`, or a `provider` of its own. [M1](#m1) asks for those manifests for that
> reason — a second declared `127.0.0.1:8214` would be a different bug with the same message.

Two providers both routed at the bridge does not double anything either: `routeFor`
(`internal/wirebridged/boot.go`) walks candidates in sorted agent order and **returns the
first**, so `cerebras` and `kilo` both carrying the injected `127.0.0.1:8214` yields one
listener.

### 4.4 The previous handoff's leading hypothesis. **Right room, wrong door.**

[`scratch/wire-bridge-handoff.md`](../../scratch/wire-bridge-handoff.md) proposed that yolo
classifies wire-bridge as a host-facing endpoint and arranges a forwarding listener for it,
which then occupies 8214. **A forwarding listener does occupy 8214 — for an unrelated
reason.** `forwardHostPorts` has exactly two sources, `network.forward_host_ports` and
`localProviderForwards` (`internal/cli/run/assemble.go:273`-`:312`,
`internal/cli/run/run.go:1244`-`:1246`); `internal/cli/run/packservices.go` composes only
`YOLO_JAIL_DAEMONS` and two `-e` vars and adds no forward. The service classification is a
wording defect that cost this investigation days, and it is worth fixing on that ground
alone — but it is not the cause.

### 4.5 `TestNonStreamRoundTrip`'s `connection reset by peer`. **Noise.**

The test stands up two `httptest` servers on ephemeral ports and posts with the default HTTP
client, whose transport pools keep-alive connections across tests
(`internal/wirebridged/handler_test.go:62`). A reset from a pooled connection to a
just-closed `httptest` server is the classic form of that flake. No fixed port is involved
and 8214 never appears. Unrelated.

---

## 5. The measurements, in the order I would run them

Each settles one hypothesis. M1 needs no launch; M2 needs no timing.

### M1

**Settles [§2](#2-the-primary-hypothesis-the-adapters-address-is-read-back-as-a-user-request).
Does the config carry the precondition?**

```console
$ sed -n '/"providers"/,$p' ~/.config/yolo-jail/config.jsonc
$ grep -l '"kind": *"\(adapter\|provider\|service\)"' ~/.config/yolo-jail/local/*/pack.json \
    ~/.config/yolo-jail/packs/*/pack.json 2>/dev/null | xargs -r grep -H '8214\|"address"'
```

**Confirms** if any `providers.<name>` has `endpoints.openai` (or `endpoints.openai-responses`)
and no `endpoints.anthropic`, for a `<name>` outside
`cerebras kilo zai openrouter llamacpp bedrock openai-codex`. **Kills it** if `providers` is
absent, or every entry is one of those seven, or every entry already declares `anthropic`.

The second command is the local-pack half of
[§4.3](#43-some-other-adapter-or-service-lands-on-8214-eliminated-for-the-shipped-tree): a
locally-declared adapter or service naming 8214 would be a different bug with this same
message, and it is the one input to this diagnosis I cannot read.

### M2

**Settles [§2](#2-the-primary-hypothesis-the-adapters-address-is-read-back-as-a-user-request)
from the other end, post-mortem, no timing needed.**

```console
$ ls -lt ~/.local/share/yolo-jail/logs/*-socat.log
```

`startHostPortForwarding` creates `<cname>-socat.log` only when the launch has at least one
forward (`internal/cli/run/network.go`). **Confirms** if a file whose mtime matches the failed
launch exists — with no `network.forward_host_ports` in the config, the only possible source
is `localProviderForwards`. **Kills it** if no such file was created for that launch.

The bridge's own post-mortem survives teardown too, in the workspace overlay:

```console
$ cat ~/code/juce-projects/.yolo/home/local/state/yolo-jail-daemons/wire-bridge.log
```

### M3

**Sees the listener. The single most decisive observation, if the jail will boot.**

```console
$ cd ~/code/juce-projects && yolo -- bash     # no -p: WillServe is false, so the launch is not refused
# then, inside:
$ tr '\0' '\n' < /proc/1/environ | grep YOLO_FORWARD_HOST_PORTS
$ ss -ltnp | grep -E ':(8214|8215)'
```

`serviceEndpointEnvArgs` emits the readiness name only when `wirebridged.WillServe` is true,
so a launch with no bridged profile active does not refuse and **still carries the forward** —
`localProviderForwards` does not depend on `-p`. **Confirms** on
`YOLO_FORWARD_HOST_PORTS=[…,8214]` plus a `socat` holding `127.0.0.1:8214`.

If that launch refuses too, the config's `use_profiles` already selects a bridged provider;
fall back to watching from the host during the failing launch:

```console
$ ls -l /tmp/yolo-fwd-*/ ; pgrep -af socat
```

### M4

**Settles [§4.2](#42-the-bridge-collides-with-itself-eliminated-for-a-fresh-container) —
was the container really fresh, and is anything orphaned?**

```console
$ podman ps -a --format '{{.Names}}\t{{.Status}}\t{{.CreatedAt}}' | grep -i juce
# and, inside any working jail:
$ cat /tmp/yolo-jaild.pid ; pgrep -af yolo-jaild
```

**Confirms** the elimination if no earlier container for that workspace was alive and each
jail shows exactly one `yolo-jaild supervise` owning one `yolo-jaild wire-bridge`. **Revives**
it if a `wire-bridge` process is found whose parent is PID 1.

### M5

**Settles that this is not the nested/shared-netns collision class.**

```console
$ echo "${YOLO_VERSION:-<host>}"
$ podman info --format '{{.Host.Security.Rootless}} {{.Host.RootlessNetworkCmd}}'
```

A nested launch is forced onto `--net=host`, where the inner and outer jails share one
loopback and a genuine cross-jail 8214 collision IS possible. `YOLO_VERSION` empty proves the
failing launch was not that.

### M6

**Closes hypothesis zero out loud.** After `just install` carrying `5e117131`, re-run
`yolo -p kilo -- pi`. The bind error persisting is the measured form of
[§4.1](#41-hypothesis-zero--the-idle-crash-left-something-on-the-port-eliminated).

---

## 6. The orphan reclaimer — is its own stated purpose real?

`c450d991 fix(supervisor): reclaim orphaned jail daemons` adds `reclaimOrphanedJailDaemons`
(`internal/entrypoint/runtime.go:215`): after **both** supervisor PID files prove dead, walk
`/proc`, and `SIGKILL` any process whose full argv matches a configured daemon.

**The handoff's argument is correct about this incident, and for the right reason.** `/proc`
inside a podman container is the container's own PID namespace — yolo passes no `--pid` flag —
so the scan cannot see another jail's processes at all, and a container created seconds ago
has an empty tmpfs `/tmp` and no daemon of its own. On the launch that failed, the reclaimer
found nothing and killed nothing. It is not an explanation, and "another jail owns this jail's
127.0.0.1:8214" is not a thing that can happen in bridge mode.

**Its own stated purpose is a different claim, and that claim is real.** The failure it is
written against is a daemon reparented to PID 1 **inside one container** after its supervisor
died, and every link of that chain exists:

- the supervisor is started detached as a child of the entrypoint, which then `exec`s bash as
  PID 1 — so a daemon whose supervisor dies reparents to PID 1 and **keeps its listener**;
- the entrypoint runs again in the **same** container on every attach
  (`podman exec yolo-entrypoint`), and `anyJailDaemonSupervisorAlive` correctly answers "no"
  once that supervisor is gone;
- the new supervisor then spawns a new `yolo-jaild wire-bridge` against the orphan's live
  8214 — **and it prints the same string this incident printed.**

So: the shape is representable, it is scoped to one container, and it is one of two faults
that share a symptom. What is missing is any observed instance — the supervisor blocks on
`<-stop` for the life of the jail (`supervisor.Run`), so reaching this state needs it to be
OOM-killed, signalled, or to panic. My read: **keep the purpose, distrust the remedy.** A
`SIGKILL` by argv match is a strong action taken on an inference, and a second supervisor
adopting the live daemon — or simply refusing with the orphan's PID named — answers the same
failure without killing anything. That is [OQ-PC3](#oq-pc3).

---

## 7. What this does not cover

- **No fix is proposed or ranked.** The remedies sit at two different seams and choosing
  between them is [OQ-PC1](#oq-pc1).
- **The `macos-user` backend is out of scope.** There is no network namespace there, so an
  adapter's ports are host ports and the same leak has a different blast radius; it needs its
  own pass.
- **The wording defects** — wire-bridge reported as a "host service", the host-loopback
  disposition quoted as if it named the service — are named in
  [§4.4](#44-the-previous-handoffs-leading-hypothesis-right-room-wrong-door) and not designed
  away here.
- **Nothing here is measured on the affected host.** Everything in
  [§2](#2-the-primary-hypothesis-the-adapters-address-is-read-back-as-a-user-request) is code
  behaviour reproduced in this jail; the precondition is a claim about a config I cannot see.

---

## Open Questions

1. <a id="oq-pc1"></a>💬 **[OQ-PC1](#oq-pc1): Which seam is the fix — the mutation, or the read?**
   The leak needs both an aliasing write (`ComposeProviders` storing and then mutating the
   caller's map) and a late read (`localProviderForwards` running after it, on the same
   pointer). Closing either one ends this bug; closing only the read leaves a host-side
   composer that silently edits the config every other consumer reads.

   <!-- vantage: oq id=OQ-PC1 leaning="Fix the mutation: ComposeProviders must not write through its input. Deep-copy the user entry at out.Set(name, u) and at mergeUnder's dst.Set(k, v). Reordering the read is a second, cheap belt." -->

   _Leaning:_ Fix the mutation. A composer that writes through its input is a one-writer
   violation, and `localProviderForwards` is only the first consumer to be caught by it —
   the same poisoned map is what every later reader of `cfg` sees. Reordering the read is a
   cheap second belt, not the fix.

   **Answer:**
   > _(empty — fill in when decided)_

2. <a id="oq-pc2"></a>💬 **[OQ-PC2](#oq-pc2): Should an implicit provider forward be disclosed?**
   Today a port the user never wrote is bound inside their jail and named nowhere — not the
   briefing ([§2.4](#24-why-no-disclosure-caught-it)), not the banner, not `launch.log`. This
   decides whether that stays true after the bug is fixed, and
   [`report-tiers.md`](../reference/report-tiers.md) has the standing rule that a disclosure
   is never suppressible.

   <!-- vantage: oq id=OQ-PC2 leaning="Yes — one line naming each implicitly forwarded port and the provider that asked for it, and the briefing's Forwarded Host Ports section fed from the merged list rather than from the config section." -->

   _Leaning:_ Yes, and cheaply: one launch line naming each implicit port with the provider
   that asked for it, and `briefingPortsFor` fed from the merged list rather than from
   `netSec`. A forward is a hole into the host; it is exactly the class that may not be
   silent.

   **Answer:**
   > _(empty — fill in when decided)_

3. <a id="oq-pc3"></a>💬 **[OQ-PC3](#oq-pc3): Keep, narrow, or replace the orphan reclaimer?**
   [§6](#6-the-orphan-reclaimer--is-its-own-stated-purpose-real) finds its purpose real and
   its remedy strong: `SIGKILL` on an argv match, from an inference about ownership, with no
   observed instance of the failure it treats. The handoff asks for a straight revert.

   <!-- vantage: oq id=OQ-PC3 leaning="Keep the detection, change the action: the second supervisor adopts the live daemon, or refuses naming the orphan's PID. A revert loses a real in-container failure mode that prints this same error." -->

   _Leaning:_ Keep the detection, change the action — adopt the live daemon, or refuse and
   name its PID. A straight revert throws away the only guard against an in-container fault
   that prints this identical error, which is the worst possible thing to be blind to while
   this incident is open.

   **Answer:**
   > _(empty — fill in when decided)_
