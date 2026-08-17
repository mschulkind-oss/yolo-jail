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
