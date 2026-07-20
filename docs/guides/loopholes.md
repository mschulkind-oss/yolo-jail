# yolo-jail loopholes

A **loophole** is a single controlled permeability point between the jail and the host — a sanctioned narrow passage through the wall. The jail talks to something through the loophole, and nothing escapes that's not declared.

Examples:

- [`claude-oauth-broker`](../../bundled_loopholes/claude-oauth-broker/) — MITM proxy that serializes Claude OAuth refreshes (transport: `tls-intercept`, lifecycle: `external`).
- `host-processes` — allowlisted read-only view of host processes (transport: `unix-socket`, lifecycle: `spawned`).
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
  "transport": "tls-intercept",   // or "unix-socket" or "none"
  "lifecycle": "external",        // or "spawned" (yolo manages the daemon)
  "intercepts": [                 // tls-intercept only
    {"host": "example.com"}
  ],
  "broker_ip": "host-gateway",    // tls-intercept only; container runtime magic value
  "ca_cert": "ca.crt",            // tls-intercept only; auto-mounted + trusted
  "jail_env": {"FOO": "bar"},     // any transport
  "doctor_cmd": ["bin", "--ok"]   // optional; run by `yolo doctor`
}
```

What the loader does at each `yolo run`:

1. Scans `~/.local/share/yolo-jail/loopholes/` for subdirectories with a valid `manifest.jsonc`.
2. Skips any with `"enabled": false`.
3. For `tls-intercept` loopholes: emits `--add-host <host>:<broker_ip>` for each intercept, bind-mounts the CA cert into the jail at `/etc/yolo-jail/loopholes/<name>/ca.crt`, and sets `NODE_EXTRA_CA_CERTS` to all loophole CAs concatenated. **Note:** Apple Container (`runtime=container`) does not support `--add-host` ([apple/container#673](https://github.com/apple/container/issues/673)), so `tls-intercept` loopholes are skipped entirely on that runtime.
4. For `unix-socket` / `spawned` loopholes — declare them via the `loopholes` shorthand in `yolo-jail.jsonc`; yolo handles spawning the daemon, creating the socket, bind-mounting it into the jail, and cleanup.
5. Merges `jail_env` into the container env.

Invalid manifests are skipped silently at runtime; `yolo loopholes list` surfaces the error.

## `loopholes` in `yolo-jail.jsonc`

The `loopholes` block is the workspace-scoped entry point. Each entry is treated as a `unix-socket` + `spawned` loophole — yolo spawns the daemon process at jail startup, creates a Unix socket, bind-mounts it into the jail, and tears down on exit. They appear in `yolo loopholes list` alongside file-backed loopholes so the whole picture lives in one command.

```jsonc
"loopholes": {
  "host-processes": {
    "description": "Allowlisted view of host processes",
    "command": ["yolo", "internal", "daemon", "host-processes", "--socket", "$SOCKET"],
    "doctor_cmd": ["yolo", "internal", "daemon", "host-processes", "--self-check"]
  }
}
```

That's the real shipping shape — compare
[`bundled_loopholes/host-processes/manifest.jsonc`](../../bundled_loopholes/host-processes/manifest.jsonc).
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

Writing a `unix-socket`/`spawned` loophole used to mean reimplementing the frame protocol, signal handling, the bind/umask dance, per-connection concurrency, and structured logging. The package takes that off your plate. The whole API is `hostservice.Serve` + `hostservice.Session`:

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
- **Signal-safe teardown** — SIGTERM / SIGINT shut down the accept loop cleanly, the socket is removed on exit.
- **Goroutine-per-connection** — cheap, stdlib-only.

The package is `internal/`, so it isn't importable from outside the module. External daemons in any language can still speak the protocol directly — it's a frozen, fully specified wire format ([`loophole-protocol.md`](../design/loophole-protocol.md)), and `internal/frameproto` is the reference codec.

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
- [`bundled_loopholes/claude-oauth-broker/`](../../bundled_loopholes/claude-oauth-broker/) — reference `tls-intercept` implementation.
- [`internal/loopholes/`](../../internal/loopholes) — loader source (`loopholes.go`'s package doc has the canonical schema).
- [`internal/hostservice/`](../../internal/hostservice) — helper package.
- [`internal/hostprocesses/`](../../internal/hostprocesses) — reference `unix-socket` consumer of the helper, reachable as `yolo internal daemon host-processes`.
- [`internal/frameproto/`](../../internal/frameproto) — reference codec for the wire format.
- [`bundled_loopholes/claude-oauth-broker/README.md`](../../bundled_loopholes/claude-oauth-broker/README.md) — the broker architecture that shaped this (the older mitm-proxy design notes are in git history).
- [`docs/research/claude-token-logouts.md`](../research/claude-token-logouts.md) — operational triage for Claude logouts; the broker loophole is Step 3's fix.
