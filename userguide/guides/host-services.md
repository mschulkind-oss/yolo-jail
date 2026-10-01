# Your Own Host Service

A **host service** is a program that runs on your machine, outside the jail, and answers requests
from inside it. It lets an agent use something that must stay on the host, such as an API key, an
internal service or a privileged operation, without the jail ever holding it. yolo's own shared
login services work this way. This page shows how to run one of your own, declared in your config;
it is the simplest kind of [loophole](loopholes.md).

To ship a service inside a pack instead, for yourself or for others, see
[Writing a Loophole](writing-loopholes.md).

## When to use one

- **A credential broker.** The service holds API keys or tokens and answers scoped requests; the
  jail never sees the raw secret.
- **An access-control proxy.** The service fronts an internal API and allows only the calls you
  decide.
- **An audit log.** The service records events from the agent in a file on the host that the jail
  cannot change.

## Declare it

Host services go in your **user config**, `~/.config/yolo-jail/config.jsonc`. A project's
`yolo-jail.jsonc` cannot declare one, because that would let an agent run a program on your host.

```jsonc
{
  "loopholes": {
    "auth-broker": {
      // The program to start on the host when the jail starts. yolo replaces
      // "{endpoint}" with the path of the Unix socket your program should listen on.
      "command": ["python3", "~/code/auth-broker/serve.py", "--socket", "{endpoint}"],

      // Environment variables for the host program only, never the jail.
      "env": { "KEYS_FILE": "~/secrets/broker-keys.json" }
    }
  }
}
```

The name (`auth-broker` here) must start with a letter and use only letters, digits, `-` and `_`.
Keep the program itself somewhere the jail cannot write, such as `~/.local/bin` or a folder outside
your project: yolo refuses a command that lives inside the project or the jail's home, because an
agent could rewrite it between launches. `yolo check` confirms the command exists.

A service you declare is on by default. `"enabled": false` switches it off without deleting it.

## What happens at launch

1. yolo starts your program on the host and waits up to 5 seconds for it to accept connections on
   the socket. A program that exits or never listens is reported, and the jail starts without it.
2. yolo puts its own authenticated front in front of your socket, and gives the jail a small file
   describing how to reach it, at `/run/yolo-services/auth-broker.endpoint`.
3. Inside the jail, the environment variable `YOLO_SERVICE_AUTH_BROKER_ENDPOINT` names that file.
   The variable is `YOLO_SERVICE_<NAME>_ENDPOINT`, with the name upper-cased and any other character
   replaced by `_`.
4. When the jail exits, yolo stops your program.

Your program's output is logged on the host at
`~/.local/share/yolo-jail/logs/host-service-<name>.log`. Once that log passes 4 MiB, the next
launch keeps its newest 4 MiB in `host-service-<name>.log.1`, replacing the older copy there, and
empties the log.

## A minimal service

This toy broker hands out secrets from an allowlist. It runs on the host, reads the keys there, and
answers one JSON request per connection:

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
        key = json.loads(line).get("key")
        if key in {"OPENAI_API_KEY", "STRIPE_SECRET"}:
            conn.sendall(json.dumps({"value": KEYS[key]}).encode() + b"\n")
        else:
            conn.sendall(json.dumps({"error": "key not allowed"}).encode() + b"\n")
    finally:
        conn.close()
```

The program speaks its own protocol on a plain Unix socket; yolo's front passes the bytes through
unchanged.

## Talking to it from the jail

A client inside the jail does not open a socket file. It reads the endpoint file, which holds a
local address, a certificate and a token for this jail only, then connects over TLS to that
address, trusting only that certificate, and sends the token first. Only then does it speak your
program's protocol. That needs a TLS library, so a shell one-liner will not do; the steps are in the
[loophole protocol reference](https://github.com/mschulkind-oss/yolo-jail/blob/main/docs/reference/loophole-protocol.md#writing-a-client-from-scratch),
and yolo's own `yolo-ps` command is a working client.

## Security model

- The boundary is your own user account. Anything running as you on the host can already do what
  your service does; the jail can reach it only through yolo's front, which checks the per-jail
  token.
- Each jail gets its own endpoint file and token, so one jail cannot use another jail's connection.
- What your service allows, logs and rate-limits is up to your service.

## Setups where it does not work

Host services need a jail that can connect back to your host. On Apple Container that connection
does not work yet, so a service you declare is skipped with one line at each launch. Use Podman on
a Mac if you need one. [Settings per setup](../reference/settings-per-setup.md#the-loopholes-host-services-a-jail-can-use)
has the detail for every setup.
