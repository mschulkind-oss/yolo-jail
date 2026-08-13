# yolo-jail loopholes

A **loophole** is a single controlled permeability point between the jail and the host — a sanctioned narrow passage through the wall. The jail talks to something through the loophole, and nothing escapes that's not declared.

Examples:

- [`claude-oauth-broker`](../../bundled_loopholes/claude-oauth-broker/) — MITM proxy that serializes Claude OAuth refreshes (transport: `loopback-tls`, lifecycle: `spawned`, plus an `intercepts` list).
- `host-processes` — allowlisted read-only view of host processes (transport: `loopback-tls`, lifecycle: `spawned`).
- `journal`, `cgroup-delegate` — built-in loopholes surfaced from `loopholes` in `yolo-jail.jsonc`.
- Hypothetical future: `llm-audit` (logs every inference request), `secret-gate` (scrubs outbound traffic).

## Anatomy of a file-backed loophole

```
~/.local/share/yolo-jail/loopholes/<name>/
├── manifest.jsonc          # required
├── ca.crt                  # optional; auto-trusted in the jail
├── <your-daemon>.service   # optional; loophole owns its own lifecycle
└── README.md               # optional; for operators
```

Only `manifest.jsonc` is required. Everything else is up to the loophole.

## Manifest schema (v1)

```jsonc
{
  "name": "my-loophole",          // required; must match directory name
  "description": "…",             // required; one-line human summary
  "version": 1,                   // manifest format; currently 1
  "enabled": true,                // default true; toggle via CLI
  "transport": "loopback-tls",    // or "none"; DEFAULT is "loopback-tls"
  "lifecycle": "external",        // or "spawned" (yolo manages the daemon)
  "intercepts": [                 // optional; presence is what makes it a TLS intercept
    {"host": "example.com"}
  ],
  "broker_ip": "host-gateway",    // intercepts only; container runtime magic value
  "ca_cert": "ca.crt",            // intercepts only; auto-mounted + trusted
  "state_files": ["ca.crt"],      // optional; narrows the state-dir mount
  "jail_env": {"FOO": "bar"},     // any transport
  "doctor_cmd": ["bin", "--ok"]   // optional; run by `yolo doctor`
}
```

**`transport` has exactly two values, and `unix-socket`/`tls-intercept` are
GONE** — removed from the validator, not deprecated, so a manifest naming one is
rejected and the loophole does not load (the error names its replacement). See
[`loophole-transport.md`](../design/loophole-transport.md) §7.4 for why.

- **`loopback-tls`** (the default): the loophole has a host daemon a jail dials.
  yolo substitutes the daemon's publication path into `{endpoint}`, the daemon
  publishes an endpoint file there, and the jail reads it, pins the certificate
  inside it, and presents the token inside it. `{socket}` and `jail_socket` stay
  accepted as aliases for `{endpoint}` and `jail_endpoint`.
- **`none`**: no daemon at all. `audio` is the shipped example — it is only
  `host_bind_mounts` and a `requires` predicate.

**Interception is not a transport.** It is declared by `intercepts` (plus
`broker_ip` and `ca_cert`) on any transport, and that list is what emits the
`--add-host` flags. A manifest that used to say `"transport": "tls-intercept"`
should say `"loopback-tls"` and change nothing else.

**`state_files`** is the jail-visible slice of the per-loophole state dir
(`~/.local/share/yolo-jail/state/<name>/`, mounted at
`/var/lib/yolo-jail/loopholes/<name>/` when the loophole has a `jail_daemon`).
Entries are paths relative to the state dir, each mounted as a single `:ro`
file; an entry that does not exist host-side is skipped rather than mounted, so
the runtime never materializes it as an empty directory. **Omit the key and the
whole state dir crosses** — the historical behavior, which is why the shipped
`claude-oauth-broker` declares it: without it the CA's private key was readable
in every jail ([#33](https://github.com/mschulkind-oss/yolo-jail/issues/33)).
Declare it whenever your state dir holds anything the jail does not read.

What the loader does at each `yolo run`:

1. Scans `~/.local/share/yolo-jail/loopholes/` for subdirectories with a valid `manifest.jsonc`.
2. Skips any with `"enabled": false`.
3. For loopholes declaring `intercepts`: emits `--add-host <host>:<broker_ip>` for each intercept, bind-mounts the CA cert into the jail at `/etc/yolo-jail/loopholes/<name>/ca.crt`, and sets `NODE_EXTRA_CA_CERTS` to all loophole CAs concatenated. **Note:** Apple Container (`runtime=container`) does not support `--add-host` ([apple/container#673](https://github.com/apple/container/issues/673)), so an intercepting loophole is skipped entirely on that runtime.
4. For `loopback-tls` / `spawned` loopholes — either a bundled manifest with a `host_daemon`, or the `loopholes` shorthand in `yolo-jail.jsonc`; yolo handles spawning the daemon, bind-mounting its published path into the jail, and cleanup.
5. Merges `jail_env` into the container env.

A manifest yolo cannot load produces a **warning naming the file and the reason**,
and that loophole is absent for the rest of the run — no daemon, no endpoint, no
injected env var. `yolo loopholes list` also surfaces the error.

## `loopholes` in `yolo-jail.jsonc`

The `loopholes` block is the workspace-scoped entry point. yolo spawns the daemon process at jail startup, bind-mounts its published path into the jail, and tears down on exit. Entries appear in `yolo loopholes list` alongside file-backed loopholes so the whole picture lives in one command.

```jsonc
"loopholes": {
  "host-processes": {
    "description": "Allowlisted view of host processes",
    "command": ["yolo", "internal", "daemon", "host-processes", "--endpoint", "{endpoint}"],
    "doctor_cmd": ["yolo", "internal", "daemon", "host-processes", "--self-check"]
  }
}
```

> **A config entry still gets a plain Unix socket, and it is the last thing that
> does.** The retirement above is about the manifest vocabulary: `unix-socket` is
> unwritable in a `manifest.jsonc` and unreachable from one. A `loopholes:` entry
> is different, because its daemon is a program yolo did not write and cannot
> re-link — `internal/hostservice` is `internal/`, so nothing yolo ships lets a
> third-party daemon publish a `loopback-tls` endpoint file. Flipping the config
> path would not migrate those daemons, it would kill them. `yolo loopholes list`
> prints `config/unix-socket/spawned` for such an entry, which is the truth about
> what it gets. This resolves when the two remaining built-in AF_UNIX clients
> (`yolo-cglimit`, `yolo-journalctl`) are ported and the framework can offer an
> external daemon a supported way to publish.

The example above matches the bundled manifest — compare
[`bundled_loopholes/host-processes/manifest.jsonc`](../../bundled_loopholes/host-processes/manifest.jsonc),
which is `loopback-tls` because its daemon is `yolo` itself.
The bundled daemons live behind `yolo internal daemon <name>` rather than
separate binaries, because the host ship set is deliberately just `yolo`.
Third-party daemons are any executable on the host's PATH.

Writing the daemon: use the [`internal/hostservice`](../../internal/hostservice)
helper package (see below).

## CLI

```bash
yolo loopholes list              # show every loophole, transport, enabled state
yolo loopholes status            # run every doctor_cmd
yolo loopholes enable <name>     # flip `enabled` → true (file-backed only)
yolo loopholes disable <name>    # flip `enabled` → false
yolo doctor                      # includes loophole self-checks in the combined report
```

## The `hostservice` helper package

Writing a `spawned` loophole used to mean reimplementing the frame protocol, signal handling, the bind/umask dance, per-connection concurrency, and structured logging — and under `loopback-tls` it would now also mean a TLS listener, an ephemeral certificate, endpoint publication, and constant-time token verification. The package takes all of that off your plate: `Serve` takes the path to publish at, and hands your handler only authenticated connections. The whole API is still `hostservice.Serve` + `hostservice.Session`:

```go
package main

import (
    "os"
    "time"

    "github.com/mschulkind-oss/yolo-jail/internal/hostservice"
    "github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

var allowedComms = map[string]struct{}{
    "layout-manager": {},
    "sway":           {},
}

func reqComm(req *jsonx.OrderedMap) string {
    v, _ := req.Get("comm")
    s, _ := v.(string)
    return s
}

func handle(s *hostservice.Session) {
    if _, ok := allowedComms[reqComm(s.Request)]; !ok {
        s.Stderr("comm not allowlisted\n")
        s.Exit(2)
        return
    }
    s.ExecAllowlisted(
        func(req *jsonx.OrderedMap) []string {
            return []string{"ps", "-o", "pid,comm,args", "-C", reqComm(req)}
        },
        allowedComms,
        nil,            // default: validate every argv element after argv[0]
        30*time.Second, // child timeout
    )
}

func main() {
    // os.Args[1] is the path yolo substituted into {endpoint}: where to
    // PUBLISH, not a socket to bind. Serve owns everything after that.
    if err := hostservice.Serve(handle, os.Args[1], nil); err != nil {
        os.Exit(1)
    }
}
```

The package takes care of:

- **Frame protocol v1** — see [`docs/design/loophole-protocol.md`](../design/loophole-protocol.md).
- **Access logging** — one structured line per request (jail id, request keys, elapsed, bytes out). No opt-in.
- **Command-injection guard** — `Session.ExecAllowlisted(argvBuilder, allowlist, positions, timeout)` validates argv strings against a server-owned allowlist before invoking the subprocess. `positions == nil` checks everything after `argv[0]`; pass an explicit index set to validate `argv[0]` too. Daemons that skip this and shell out manually are on their own; the helper makes the safe path the short path.
- **JSON output convenience** — `Session.JSON(obj)` emits one newline-terminated JSON line on stdout. Agents parse JSON; humans can use `--table` on the client side.
- **The transport** — `Serve` delegates to [`internal/svcendpoint`](../../internal/svcendpoint): it binds `127.0.0.1:0`, mints a certificate whose private key never leaves the process, mints this jail's token, and publishes all three atomically `0600`. Its `Accept` returns **only authenticated connections**, so a daemon cannot forget to authenticate and none of the code you write learns which transport carried its bytes.
- **Signal-safe teardown** — SIGTERM / SIGINT shut down the accept loop cleanly, the published endpoint file is removed on exit (which retires the token with it).
- **Goroutine-per-connection** — cheap, stdlib-only.

The package is `internal/`, so it isn't importable from outside the module. An external daemon in any language can still speak the frame protocol — it is frozen and fully specified ([`loophole-protocol.md`](../design/loophole-protocol.md)), with `internal/frameproto` as the reference codec — **but it must now also implement the transport underneath it**: a TLS listener, endpoint publication, and token verification. That is a real cost of unifying, and it is why a `loopholes:` config entry still gets a plain socket (above) rather than being flipped along with the manifests.

## Example: adding a minimal smoke-test loophole

```bash
mkdir -p ~/.local/share/yolo-jail/loopholes/hello
cat > ~/.local/share/yolo-jail/loopholes/hello/manifest.jsonc <<'EOF'
{
  "name": "hello",
  "description": "Smoke test — injects HELLO=world into every jail",
  "version": 1,
  "transport": "none",
  "jail_env": {"HELLO": "world"}
}
EOF
yolo loopholes list                # => enabled  hello  (none/external)
yolo -- bash -c 'echo $HELLO'     # => world
```

Remove the directory to uninstall. No state lives outside it.

## Discovery from inside the jail

Agents inside the jail shouldn't need the briefing to enumerate every capability; the briefing instead points at the discovery command:

- `yolo loopholes list` — what's active and reachable from here.

Keeps the briefing tight and prevents drift when loopholes come and go.

## See also

- [`docs/design/loophole-protocol.md`](../design/loophole-protocol.md) — wire protocol spec.
- [`bundled_loopholes/claude-oauth-broker/`](../../bundled_loopholes/claude-oauth-broker/) — reference intercepting loophole (`loopback-tls` + `intercepts`).
- [`internal/loopholes/`](../../internal/loopholes) — loader source (`loopholes.go`'s package doc has the canonical schema).
- [`internal/hostservice/`](../../internal/hostservice) — helper package.
- [`internal/hostprocesses/`](../../internal/hostprocesses) — reference `loopback-tls` consumer of the helper, reachable as `yolo internal daemon host-processes`.
- [`internal/svcendpoint/`](../../internal/svcendpoint) — the transport itself: endpoint file, cert pinning, token frame. Both halves in one package on purpose.
- [`docs/design/loophole-transport.md`](../design/loophole-transport.md) — why there is one transport and what it defends against.
- [`internal/frameproto/`](../../internal/frameproto) — reference codec for the wire format.
- [`bundled_loopholes/claude-oauth-broker/README.md`](../../bundled_loopholes/claude-oauth-broker/README.md) — the broker architecture that shaped this (the older mitm-proxy design notes are in git history).
- [`docs/research/claude-token-logouts.md`](../research/claude-token-logouts.md) — operational triage for Claude logouts; the broker loophole is Step 3's fix.
