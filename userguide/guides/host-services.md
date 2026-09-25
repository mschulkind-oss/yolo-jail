# Host Services (Loopholes)

## Loopholes (spawned host services)

**A way to split the jail boundary cleanly.** A *spawned* loophole is a process that runs on the host (outside the jail) and publishes an address the jail can reach through a per-jail directory bind-mounted at `/run/yolo-services/`. The agent inside the jail can talk to the loophole without ever holding its secrets, credentials, or privileges. See [Loopholes](loopholes.md) for the broader loophole system (including intercepting loopholes like the Claude OAuth broker).

> **Two shapes, one transport.** A loophole shipped as a `manifest.jsonc` uses the framework's `loopback-tls` transport: it publishes `/run/yolo-services/<name>.endpoint`, a `0600` file naming a `127.0.0.1` listener plus the certificate to pin and this jail's bearer token. A service declared in the `loopholes:` config block below **gets the same thing** — it used to get a plain Unix socket at `/run/yolo-services/<name>.sock`, on the reasoning that nothing yolo shipped let a daemon it did not write publish an endpoint file. yolo's *front* removed that objection: the daemon still binds an ordinary AF_UNIX socket, now at a host-only path, and yolo runs an authenticated front over it and publishes the endpoint file itself. So the **daemon** side of this section is unchanged from the socket era, and the **client** side is not: from inside the jail you dial a TCP address with a pinned certificate and a bearer token, never a socket file.

The privileged-operations pattern this generalizes is the cgroup delegate — a host-side listener performs cgroup operations on behalf of the container so the jail itself doesn't need `CAP_SYS_ADMIN` or rw cgroup mounts. The delegate is not itself one of the services below (it runs in-process in `yolo` and is the one service still on a bind-mounted socket, for the reason under [Security model](#security-model)), but the `loopholes` config block lets you define your own host-side trust in the same shape.

### When to use it

- **Auth / credential brokers.** A service holds API keys, OAuth tokens, or signed JWTs and answers scoped requests from the agent. The jail never sees the raw credentials.
- **Access control proxies.** A service fronts an internal API and enforces "agent X may only call endpoint Y with payload Z" rules outside the jail.
- **Audit / logging sinks.** A service receives structured events from the agent and writes them to a host-side log the jail can't tamper with.
- **Resource brokers.** Anything where you want a small piece of host-side trust without pulling the entire dependency into the jail.

### Configuration

```jsonc
{
  "loopholes": {
    "auth-broker": {
      // Command to launch on the host when the jail starts.
      // "{endpoint}" is substituted with the HOST-ONLY socket path the
      // service should bind — under /tmp, deliberately outside the
      // directory the jail sees, so the jail reaches the daemon only
      // through yolo's front.  ("{socket}" is an accepted alias.)
      "command": ["~/code/auth-broker/serve.py", "--socket", "{endpoint}"],

      // Optional environment variables for the host daemon (NOT the jail).
      "env": {
        "KEYS_FILE": "~/secrets/broker-keys.json",
        "LOG_LEVEL": "info"
      },

      // Optional override of where the ENDPOINT FILE appears inside the
      // jail.  Must start with /run/yolo-services/ — that's the only
      // directory that gets bind-mounted in, and the prefix is validated.
      // Default: /run/yolo-services/<name>.endpoint
      // ("jail_socket" is the older spelling of this key, still accepted.)
      "jail_endpoint": "/run/yolo-services/auth-broker.endpoint"
    }
  }
}
```

The service name (`auth-broker` above) must match `^[a-zA-Z][a-zA-Z0-9_-]{0,63}$`. **No loophole name is reserved any more.** `cgroup-delegate` stopped being reserved on 2026-08-18, and `claude-oauth-broker` — the last reserved name — stopped when it became a contribution of the `claude` pack rather than a built-in. Both are ordinary pack-shipped loopholes now, enabled the same way as any other: `"loopholes": {"cgroup-delegate": {"enabled": true}}` (see below).

### Lifecycle

For each service, on `yolo run`:

1. Per-jail directory `/tmp/yolo-host-services-<8hex>/` is created on the host, mode `0700`, and bind-mounted into the jail at `/run/yolo-services/`. (It is **not** under your workspace — an earlier revision of this page said `<workspace>/.yolo/host-services/`, which was never true and is actively misleading now that a manifest loophole's endpoint file in there is a credential.)
2. yolo substitutes `{endpoint}` in the service's command with the path the daemon should bind, e.g. `/tmp/yolo-front-a1b2c3d4-auth-broker.sock`. (`{socket}` is an accepted alias.) That path is **not** in the directory from step 1: leaving it there would keep a raw socket reachable from inside the jail, and would let the jail unlink the daemon's own socket.
3. yolo launches the command as a child process. The service is expected to bind the socket at the substituted path.
4. yolo waits up to 5 seconds for the service to become reachable — for a daemon that binds a socket, that the socket accepts a *connect*, never that the file exists, which a leftover from a dead predecessor would satisfy instantly; for a daemon that publishes its own endpoint file, that the file *parses*, since a truncated file would otherwise read as healthy forever. If the service exits early or doesn't publish in time, yolo logs the failure and continues without that service.
5. yolo starts its own authenticated front over the daemon's socket, which publishes `/run/yolo-services/auth-broker.endpoint`. This happens only *after* step 4 succeeds: a front that bound earlier would make the endpoint look healthy while nothing was behind it, and every authenticated connection would be dropped at the dial. The container starts, and the agent inside reads that file.
6. When the container exits, yolo sends `SIGTERM` to each service it spawned, waits 5 seconds, then `SIGKILL`s its process group. **A *host-scoped* daemon is exempt, and the asymmetry is the point:** a loophole manifest may declare `"scope": "host"` in its `host_daemon` block, meaning one daemon per machine serving every jail on it. yolo *ensures* such a daemon rather than spawning it, and gives each jail its own front over the one socket — so a jail ending closes **only its own front** and never signals the daemon, which other jails are still using. It keeps running after your last jail exits, so nothing about your jail's teardown is how you inspect or cycle one: `yolo loopholes status` runs each loophole's own host-side self-check, and the loophole's own tooling replaces the daemon (for the Claude broker, `yolo broker restart`). Which shipped loopholes declare it changes, so derive the set rather than trusting a list: `rg -n '"scope": "host"' packs/*/loopholes/*/manifest.jsonc`.
7. The per-jail directory is removed — which is also how a manifest loophole's bearer token is retired, since the token lives only in the file inside it.

Service stdout and stderr are captured to `~/.local/share/yolo-jail/logs/host-service-<name>.log` for debugging.

### Discovering the service from inside the jail

For each service, yolo injects an env var so the agent doesn't need to hard-code the path:

```
YOLO_SERVICE_AUTH_BROKER_ENDPOINT=/run/yolo-services/auth-broker.endpoint
```

The variable name is `YOLO_SERVICE_<UPPERCASED-NAME>_ENDPOINT`, with non-alphanumeric characters replaced by underscores, and it is the same variable for both shapes. Its value is always a **path to the endpoint file** and never an address — the address lives inside the file, so it can change without relaunching the jail, whose environment is frozen at container start.

The `_SOCKET` spelling still exists, for exactly one service: the cgroup delegate, whose value really is a socket path (see [Security model](#security-model) for why that one cannot be fronted). It is a retiring spelling, not a second mechanism. The two suffixes are deliberately distinct rather than one being reused: the value's meaning differs, and a client that dials a regular file as though it were a socket reports something obscure, where a client that finds its variable absent reports "not wired up in this jail" and exits cleanly.

### Minimal example service

A trivial Python broker that hands out a single secret. The service runs on the host, holds the secret, and never reveals it to the jail — the jail just gets the resolved value for the key it asks about.

```python
# ~/code/auth-broker/serve.py
import json, os, socket, sys

KEYS = json.load(open(os.environ["KEYS_FILE"]))

sock_path = sys.argv[sys.argv.index("--socket") + 1]
srv = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
srv.bind(sock_path)
srv.listen(8)

while True:
    conn, _ = srv.accept()
    try:
        line = b""
        while not line.endswith(b"\n"):
            chunk = conn.recv(4096)
            if not chunk:
                break
            line += chunk
        req = json.loads(line)
        # Toy access control: the agent can only ask for keys in an allowlist.
        key = req.get("key")
        if key in {"OPENAI_API_KEY", "STRIPE_SECRET"}:
            conn.sendall(json.dumps({"value": KEYS[key]}).encode() + b"\n")
        else:
            conn.sendall(json.dumps({"error": "key not allowed"}).encode() + b"\n")
    finally:
        conn.close()
```

Hook it up in your workspace config:

```jsonc
{
  "loopholes": {
    "auth-broker": {
      "command": ["python3", "~/code/auth-broker/serve.py", "--socket", "{endpoint}"],
      "env": {"KEYS_FILE": "~/secrets/keys.json"}
    }
  }
}
```

The daemon above needs no change from what it would have been on the retired socket transport — it binds a socket and speaks its own newline-JSON protocol, and yolo's front splices bytes without translating them. **The client does.** This page used to show a `nc -U "$YOLO_SERVICE_AUTH_BROKER_SOCKET"` one-liner here, and there is no shell equivalent of what replaced it: a client reads `$YOLO_SERVICE_AUTH_BROKER_ENDPOINT`, reads *that file*, TLS-dials the address in it with the certificate in it as its only trust root, writes the token in it as a length-prefixed frame, and only then speaks the daemon's protocol. That needs a TLS library. `cmd/yolo-ps` is the reference implementation and the steps are enumerated in [`loophole-protocol.md`](https://github.com/mschulkind-oss/yolo-jail/blob/main/docs/reference/loophole-protocol.md) §["Writing a client from scratch"](https://github.com/mschulkind-oss/yolo-jail/blob/main/docs/reference/loophole-protocol.md#writing-a-client-from-scratch).

The secret never enters the jail filesystem, env vars, or any bind mount.

### Security model

**The boundary is "whatever runs as your user", on either shape.** That matches the host — anything running as you can already read your credentials or act as you — and a jail extends it unchanged. What differs is how it is enforced:

- **A fronted service** (config-declared): the per-jail directory is one half of the isolation — other jails cannot see it, because it is a separate mount — and the per-jail bearer token in its `0600` endpoint file is the other. The daemon's own socket is host-only, so nothing inside the jail can reach it at all; what the jail can reach is the front, which authenticates.
- **A manifest loophole** (`loopback-tls`): identical, minus the front — the daemon publishes the endpoint file itself. Either way the token is the enforcement, because a loopback TCP port has no "can connect implies authorized" property to inherit. `0600` on a path and a pre-shared token on a port say the same thing.
- **`SO_PEERCRED` does not survive a front, and that is why one service refused to move.** On a bind-mounted socket, a Linux service can read the connecting peer's host PID off the kernel; put a front in between and it reads *yolo's* PID instead, because yolo is the process that dialled. The cgroup delegate needs the real one (it writes the caller into a cgroup), so it is the one service still on a bind-mounted socket and the one still named by a `_SOCKET` variable. Even there the credential cannot separate the jail from a same-user host process: rootless podman maps the container's UID 0 to your uid, so both arrive carrying the same one. Treat it as attribution, not as a boundary.
- What the service does with secrets, scopes, audit logging, and rate limiting is entirely up to the service. yolo just wires the plumbing.
- The cgroup delegate is this pattern applied to a privileged operation — proof that host-side trust in front of a jail is enough to carry one safely — though it is a listener inside `yolo` rather than a spawned child, so it is not literally one of the services configured here.

### Validation

`yolo check` verifies that each configured service's command exists and is executable. Catches typos before the next jail start.

### Apple Container caveat

Every host service but one is skipped on the `container` runtime. The allowance is by name — `openai-auth-broker`, the OpenAI credential broker — and it is the reason the services directory is mounted there at all; every other service, yours included, prints one yellow inert line per launch and does not start. Use `podman` if you need this feature on macOS.

The reason **used to be** the transport: Apple Container doesn't bind-mount Unix sockets through virtiofs. The `loopback-tls` transport removes that obstacle — it is a TCP connection, not a socket file — and the mount question is answered too, since an endpoint file crosses in an ordinary directory bind that this backend handles. What holds the skip in place now is a **measurement**: on `container` 1.1.0 a container→host connection completes its handshake and then carries nothing (by two alternating mechanisms), no bind address helps, and `host.containers.internal` does not resolve. A loopback-TLS loophole needs exactly that dial, so it could not be reached from the jail even if it were started. This is **deferred on a measured blocker an upstream release can expire**, not a design decision — `integration/applecontainer_test.go`'s host-loopback witness is what to re-run before believing it still holds.

---
