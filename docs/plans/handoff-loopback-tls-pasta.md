# Handoff — loopback-TLS transport is unreachable under pasta networking

**Audience:** an agent working in the yolo-jail repo.
**Requester's goal (verbatim intent):** *"why is this happening despite journal: user"* —
the journal bridge (and every other loopback-TLS service) fails with `connection refused`
on a rootless-podman host using **pasta** as the rootless network command.

Written 2026-08-16 against **v0.8.0-191-gc49051c**. Every claim below was verified by
running the binary or the podman CLI; commands are inline.

---

## Symptom

Inside a jail whose config sets `"journal": "user"`:

```
$ yolo-journalctl
yolo-journalctl: cannot reach the journal bridge named by /run/yolo-services/journal.endpoint:
  dial tcp 169.254.1.2:36253: connect: connection refused
```

`yolo-ps` fails identically at `169.254.1.2:39351`. This is **not journal-specific** — every
loopback-TLS service (journal, host-processes, claude-oauth-broker) is unreachable, because
they all share one transport and one bind address.

## Environment

- Host: Linux, **rootless podman**.
- `podman info` → `networkBackend: netavark`, `rootlessNetworkCmd: pasta`.
- `podman network inspect podman` → interface `podman0`, subnet `10.88.0.0/16`, gateway `10.88.0.1`.
- Inside the jail, `/etc/hosts` maps `169.254.1.2  host.containers.internal host.docker.internal`.

The mismatch is the whole story: the **netavark** gateway is `10.88.0.1`, but the jail
resolves `host.containers.internal` to **`169.254.1.2`** — pasta's link-local gateway. pasta
overrides the netavark bridge for rootless networking.

## Root cause

The loopback-TLS transport (`internal/svcendpoint/listen.go`) binds `127.0.0.1` and
advertises `host.containers.internal`, on the assumption — spelled out in
`docs/design/loophole-transport.md` §3.0 ("Loopback, so it is not on the LAN") — that the
runtime forwards `host.containers.internal` to the host's loopback.

That assumption holds for **slirp4netns** (`host.containers.internal` → `10.0.2.2`, which
slirp DNATs to `127.0.0.1`). It does **not** hold for **pasta**: the daemon binds loopback,
the jail dials `169.254.1.2`, and the connection is refused — pasta is not forwarding
`169.254.1.2` → `127.0.0.1`.

Evidence, from the host:

```
$ ss -tlnp | grep 36253
LISTEN 0 4096 127.0.0.1:36253 0.0.0.0:* users:(("yolo",pid=648874,fd=4))   # daemon on loopback
$ # jail dials 169.254.1.2:36253 → connection refused
```

## What was tried, and why it failed (already reverted)

An attempt was made to bind the daemon to the **netavark bridge gateway** instead of
loopback, resolved dynamically via `podman info` + `podman network inspect`. Committed as
`a1003b9`, reverted as `c49051c`.

Two independent reasons it was wrong:

1. **`10.88.0.1` is not bindable.** It lives in rootless podman's separate network
   namespace, not the namespace the daemon runs in:

   ```
   $ python3 -c "import socket; s=socket.socket(); s.bind(('10.88.0.1',0))"
   OSError: [Errno 99] Cannot assign requested address
   ```

   So `Listen` failed, the daemon crashed at startup, and the jail regressed from
   "connection refused" to `YOLO_SERVICE_JOURNAL_ENDPOINT is not set` (bridge never starts).

2. **The jail doesn't dial `10.88.0.1` anyway.** It dials `169.254.1.2` (pasta). Binding to
   the netavark gateway targets an address the jail never reaches.

## Open question — the one thing blocking the fix

**Where does pasta forward `169.254.1.2:<port>` to?** Loopback, or the host's primary
interface?

- If **loopback** → the transport should already work, so the refusal is a pasta
  version/config bug, not a yolo bug.
- If **primary interface** → the daemon must bind there (i.e. `0.0.0.0`), which is the
  LAN-exposure tradeoff the maintainer explicitly rejected.

Diagnostic to settle it — on the host:

```sh
# terminal 1 — listen on loopback only
python3 -c "import socket;s=socket.socket();s.bind(('127.0.0.1',9999));s.listen(1);print('loopback ready');s.accept();print('LOOPBACK got a connection')"
# terminal 2 — listen on all interfaces
python3 -c "import socket;s=socket.socket();s.bind(('0.0.0.0',9998));s.listen(1);print('all ready');s.accept();print('ALL got a connection')"
```

Then inside the jail:

```sh
nc host.containers.internal 9999   # does the loopback listener fire?
nc host.containers.internal 9998   # does the all-interfaces listener fire?
```

## Fix options, once the forwarding target is known

| Option | Tradeoff |
|---|---|
| Bind `0.0.0.0` | Works regardless of forwarding, but puts the TLS port on the LAN — the exact thing the loopback bind exists to avoid (§3.0). TLS + per-jail token still gate access, so it's visibility, not auth, that's lost. |
| Bind the primary-interface IP | Same LAN exposure as `0.0.0.0`, narrower surface. |
| Reintroduce AF_UNIX on Linux | Robust and LAN-free (bind-mounted socket, no network hop), but reopens the two-transport drift the unification (`loophole-transport.md` §7.4) retired for macOS/virtiofs reasons. A stale `--socket` daemon from Aug 15 confirms this was the prior working path. |
| Detect pasta vs slirp4netns | Only helps if pasta has a *bindable* forwarding target; if pasta forwards to the primary interface, this collapses to option 2. |

## Relevant code & docs

- `internal/svcendpoint/listen.go` — `Listen`/`listenWith` hardcode the loopback bind; `AdvertiseHost()`/`DefaultAdvertiseHost` publish the gateway name.
- `internal/cli/run/loopholesruntime.go` — `advertiseHostFor` picks the advertise host per network namespace.
- `docs/design/loophole-transport.md` — §3.0 security model ("loopback bind | anything on the LAN"), §7.4 the unification decision that retired `unix-socket`.
- `cmd/yolo-journalctl/main.go` — the in-jail client; its `ErrEndpointMissing`/`ErrAuthRejected`/default branches distinguish "not started" from "refused", which is how the regression above was spotted.

---

# Triage addendum, 2026-08-17 — the blocking open question is ANSWERED, and two findings this doc did not have

Written after a four-angle investigation. Every claim below was verified by running something in a
live jail on the affected host, or by `git log -L` on the exact line. Where I could not verify, I say so.

## 1. The maintainer's question: transition fallout, or a real break?

**A real break, and the in-flight preamble/pack sprint is innocent.** The crux is one line, and its
whole history is three commits:

```console
$ git log -L '/net.Listen("tcp"/,+2:internal/svcendpoint/listen.go'
c49051c  Revert "fix(svcendpoint): bind loopback-TLS daemons to the bridge gateway…"
a1003b9  fix(svcendpoint): bind loopback-TLS daemons to the bridge gateway…   ← cancels out
58ce9ee  feat(svcendpoint): the unified loopback-tls transport, server and client
```

No sprint commit appears, and `DefaultAdvertiseHost`'s history is `58ce9ee` alone. The sprint's
entire diff to `listen.go` is preamble machinery (`pre []byte`, `encodePreamble`, a `preamble bool`
parameter); its only run-pipeline change is one `NoPreamble` field. **Mechanically it also cannot be
the cause:** the failure is `ECONNREFUSED` at `connect(2)`, so no TLS ClientHello, no token frame and
no preamble byte is ever written. A broken preamble presents as connect-then-hang or a protocol
error — never as "connection refused".

**It broke at `58ce9ee` (2026-08-13 12:41)**, and each service broke as it crossed onto the new
transport: `462729e` yolo-ps (13:07), `e9160dd`/`9b77742` the broker's jail hop (13:31/13:41),
`992e775` the journal bridge (2026-08-15). `210be1e` (14:02) removed the escape route by retiring
`unix-socket`. **The first real failure is timestamped two days before the sprint began**:
`claude-oauth-broker.log`, `2026/08/13 21:10:02`.

**"Nobody had a pasta host" is false**, and this is the part worth sitting with: `169.254.1.2` has
been in this repo since `1dfb1ea` (2026-02-19), and `46d5417` (2026-04-17) already fixed this exact
class of bug on this exact host, concluding that the address *"is a container-side pasta tunnel, not
a real interface on the host, so bind(2) returns EADDRNOTAVAIL."* The knowledge was in the tree four
months before the design that contradicted it.

## 2. The blocking question — what does pasta forward `169.254.1.2` TO? — is answered

**The host's GLOBAL address, not its loopback.** Differential probe from inside a jail:

| probe | result | what it proves |
|---|---|---|
| `169.254.1.2:22` | connects, `SSH-2.0-OpenSSH_10.4` | the mapping is real and reaches the host |
| `169.254.1.3:22` | times out | control — the mapping is specific, not a catch-all |
| the two yolo loopback ports | **refused**, not timed out | the packet reached a host stack and got an RST |
| `192.168.1.131:22` | refused | pasta copies the host's address onto the guest |

**So "pick a better bind address" is structurally dead**, not merely awkward: the launcher's netns
holds only loopback and host-global addresses, so the only candidates are loopback (unreachable) or
`0.0.0.0`/host-global (on the LAN — the thing §3.0 exists to prevent). That kills the first two rows
of the fix table above.

**Why `a1003b9` failed** — it hit both failure modes at once by choosing `10.88.0.1`, the **rootful**
netavark gateway: unbindable from the launcher's namespace (`EADDRNOTAVAIL`, so the daemon died at
startup) *and* an address a pasta jail never dials.

## 3. Severity: total outage since 2026-08-13 — but it fails CLOSED

Verified live, same jail, same moment: an AF_UNIX connect to `cgroup-delegate.sock` **succeeds**
while both loopback-TLS endpoints in the same directory are refused. The only host service still
working is the one that stayed on the retired transport.

`claude-oauth-broker.log` carries **24** `dial tcp 169.254.1.2` failures. That is the #31-class
outage the transport was built to prevent, reproduced on the platform the build ran on.

**The reassuring half, and it is load-bearing:** the jail cannot silently mint its own refresh
instead. `/etc/hosts` pins `platform.claude.com` to `127.0.0.1` (confirmed in this jail), and the
terminator's only route out is the relay — so the single-use-refresh-token **race stays prevented**.
What is lost is availability, not safety. `curl` to the pinned host returns a clean
`502 {"error":"broker_unavailable"}`.

**Blast radius is not one odd machine:** podman has defaulted rootless networking to pasta since 5.0.
And any pack shipping a `host_daemon` is dead on arrival, since `loopback-tls` is the manifest default.

## 4. Two findings this doc did not have, and both are about why nobody noticed

- **`yolo check` reports PASS exactly when the jail is broken.** Its probe uses `DialLocal`, which
  substitutes `127.0.0.1` for the advertised host (`dial.go:15`; called at
  `check/sections_loopholes.go:143` and `:271`). So the health check dials the one address the jail
  **cannot** use, and is green during a total outage. **Fixing this is non-optional companion work
  whichever transport fix wins** — a probe that cannot fail when the thing it probes is down is
  worse than no probe.
- **Nested-jail verification is STRUCTURALLY incapable of catching this**, so AGENTS.md's
  "verify in a nested jail" instruction is not merely insufficient here, it is misleading: a nested
  podman is forced onto `--net=host`, which is precisely the configuration where the bug cannot
  reproduce. This needs an explicit carve-out in AGENTS.md, plus integration coverage, of which
  there is currently none.

## 5. Recommended fix — change the runtime's FORWARDING, not the bind

Leave `internal/svcendpoint` untouched. Emit an explicit per-backend network option at launch:
`--network=pasta:--map-host-loopback,<addr>` when `podman info` reports `rootlessNetworkCmd=pasta`,
and `--network=slirp4netns:allow_host_loopback=true` for slirp. The `127.0.0.1` bind, cert pinning,
the per-jail token and the single-transport decision all survive verbatim —
`TestAdvertiseHostDiffersFromBindHost` keeps passing unmodified, which is real signal that nothing
security-shaped moved.

Note podman already passes `--map-guest-addr 169.254.1.2`; if passt rejects the same address for both
options, use a distinct one and advertise that literal — `svcendpoint` supports it because the cert's
ServerName is a fixed label (`cert.go:19`), not a hostname.

**Fallback, and it must be treated as a real decision rather than a workaround:** AF_UNIX on Linux is
option 3 in the table above, and it reopens something `loophole-transport.md` §7.4 retired **on
purpose**. If the host's passt predates `--map-host-loopback`, that is the escape hatch — but it needs
an amendment to §7.4, not a quiet revival.
