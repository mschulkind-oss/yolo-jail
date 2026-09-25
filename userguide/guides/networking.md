# Networking

## Network & Ports

### Bridge Mode (Default)

The jail runs in an isolated network. Use `ports` to publish container services to the host:

```jsonc
{
  "network": {
    "mode": "bridge",
    "ports": ["8000:8000"]
  }
}
```

A service running on port 8000 inside the jail is accessible at `localhost:8000` on the host.

### Host Mode

Share the host's network stack directly:

```jsonc
{
  "network": {
    "mode": "host"
  }
}
```

All ports work as if running on the host. No port mapping needed.

### Host Port Forwarding

Make host services appear on `localhost` inside the jail — useful for databases, APIs, or other services already running on your machine:

```jsonc
{
  "network": {
    "forward_host_ports": [5432, 6379, "8080:9090"]
  }
}
```

- **Integer** (`5432`): Same port on both sides — host `127.0.0.1:5432` appears as jail `127.0.0.1:5432`
- **String** (`"8080:9090"`): Port remapping — host `127.0.0.1:9090` appears as jail `127.0.0.1:8080`

This uses socat via Unix sockets (requires `socat` on the host). Only works in bridge mode.

---
