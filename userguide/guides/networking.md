# Networking

A jail has outbound internet and its own private network. Inside it, `localhost` is the jail itself,
not your computer. This page covers the two directions you may want to open: letting your computer
reach a server running in the jail, and letting the jail reach a service running on your computer.

## Open a jail's server from your computer

Publish the port with `network.ports`, host side first:

```jsonc
// yolo-jail.jsonc
{
  "network": {
    "ports": ["3000:3000"]
  }
}
```

Then start the server inside the jail **listening on `0.0.0.0`**, not `127.0.0.1`, and open
`http://localhost:3000` on your computer. A server bound to `127.0.0.1` inside the jail cannot be
published. Listening on `0.0.0.0` is safe here: the published port is the only way in.

The format is `"HOST:JAIL"`, so `"8080:3000"` makes the jail's port 3000 answer at `localhost:8080`
on your computer. `"IP:HOST:JAIL"` and a `/udp` suffix are accepted too.

## Reach a service on your computer from the jail

**Usually you need no setting.** Connect to `host.containers.internal`, a name that points at your
computer from inside the jail, for example `psql -h host.containers.internal`. On Podman run as your
own user, this reaches services bound only to your computer's `127.0.0.1` too.

**When something must see `localhost`**, such as a client with the address built in, forward the
port with `network.forward_host_ports`:

```jsonc
{
  "network": {
    "forward_host_ports": [5432, "8080:9090"]
  }
}
```

- A number, such as `5432`, uses the same port on both sides: your computer's `127.0.0.1:5432`
  answers at `localhost:5432` in the jail.
- A string is `"JAIL:HOST"`, **jail side first**, the reverse of `ports`: `"8080:9090"` makes your
  computer's port 9090 answer at `localhost:8080` in the jail.

This needs `socat` installed on your computer.

## Sharing your computer's network

`"mode": "host"` makes the jail share your computer's network directly, so `localhost` is the same
on both sides and neither port key is needed. It also removes the jail's network isolation, so
leave it unset unless you need it. The default is `"bridge"`, the private network described above.

## On each setup

| | Podman on Linux | Podman on a Mac | Apple Container | `macos-user` |
|---|---|---|---|---|
| `network.ports` | Works | Works, for a server on `0.0.0.0` | Works, for a server on `0.0.0.0`, at `127.0.0.1` on the Mac; measured once | Not needed: a port the agent opens is already open on the Mac |
| `host.containers.internal` | Works | Works | Does not work | Use `localhost`: the sandbox is on the Mac's own network |
| `network.forward_host_ports` | Works, with `socat` on the host | Should work, with `socat` on the Mac; not yet tested | **Stops the launch.** Leave it unset | Same-port entries already work; `"8080:9090"` is not supported |
| `"mode": "host"` | Works | Applies to the Podman Machine, not the Mac | Not supported; leave it unset | Always the case |

On Podman run as root, the jail cannot reach services on your computer's `127.0.0.1`, including
yolo's own login services, and the launch warns. Run Podman as your own user; see
[Getting Started](../getting-started.md#linux-podman).

Network settings are fixed when a jail starts. After an edit, `yolo stop` and launch again.
