# yolo-jail modules

A **module** is a self-contained unit of host-side functionality that the jail's networking and trust store plug into at runtime. Examples:

- [`claude-oauth-broker`](../modules/claude-oauth-broker/) — MITM proxy that serializes Claude OAuth refreshes across jails (the reference implementation).
- Hypothetical future modules: `llm-audit` (logs every inference request), `secret-gate` (scrubs outbound traffic containing secrets), `latency-shaper` (adds simulated packet loss for testing).

The module system is the escape hatch for host-side integrations that would otherwise require forking yolo-jail core. If you need the jail to trust a CA, redirect a hostname, or gain extra env vars at startup, you can achieve all three with a 20-line manifest and no changes to `src/cli.py`.

## Anatomy of a module

```
~/.local/share/yolo-jail/modules/<name>/
├── manifest.jsonc          # required
├── ca.crt                  # optional; auto-trusted in the jail
├── <your-daemon>.service   # optional; module owns its lifecycle
└── README.md               # optional; for operators
```

Only `manifest.jsonc` is required. Everything else is up to the module.

## Manifest schema (v1)

```jsonc
{
  "name": "my-module",           // required; must match directory name
  "description": "…",            // required; one-line human summary
  "version": 1,                  // manifest format; currently 1
  "enabled": true,               // default true. toggle with `yolo modules {enable,disable}`
  "intercepts": [                // optional: DNS overrides for the jail
    {"host": "example.com"}
  ],
  "broker_ip": "host-gateway",   // podman/docker magic value → host-reachable-from-container
  "ca_cert": "ca.crt",           // optional: path rel. to module dir; auto-trusted via NODE_EXTRA_CA_CERTS
  "jail_env": {"FOO": "bar"},    // optional: extra env vars injected into every jail
  "doctor_cmd": ["bin", "--ok"]  // optional: health check run by `yolo doctor`
}
```

What the loader does at each `yolo run`:

1. Scans `~/.local/share/yolo-jail/modules/` for subdirectories with a valid `manifest.jsonc`.
2. Skips any with `"enabled": false`.
3. For each `intercepts[]` entry, appends `--add-host <host>:<broker_ip>` to the docker run command.
4. If `ca_cert` is set, bind-mounts it read-only into the jail at `/etc/yolo-jail/modules/<name>/ca.crt` and builds `NODE_EXTRA_CA_CERTS` with every module's CA concatenated.
5. Merges `jail_env` into the container env.

Invalid manifests are skipped silently at runtime; `yolo modules list` surfaces the error.

## CLI

```bash
yolo modules list              # show every installed module, enabled state, intercepts
yolo modules status            # run every module's doctor_cmd
yolo modules enable <name>     # flip `enabled` → true
yolo modules disable <name>    # flip `enabled` → false
yolo doctor                    # includes module self-checks in the combined report
```

## Lifecycle: the module owns its daemon

The core deliberately does **not** manage module daemons. Each module provides its own:

- systemd user unit (Linux) / launchd plist (macOS) / cron entry — whatever fits its needs.
- Installation steps (usually wired into the module's `README.md` and/or `just deploy`).
- CA/key generation if using TLS termination.

Core only knows about the manifest and the jail-side integration points. A module that wants to log inference traffic, for example, just needs to:

1. Ship a daemon that terminates TLS for `api.anthropic.com`.
2. Generate a CA whose cert is referenced in `manifest.jsonc:ca_cert`.
3. Declare `intercepts: [{"host": "api.anthropic.com"}]`.
4. Install via its own setup script.

Nothing in `src/cli.py` needs to change.

## Example: writing a minimal module

```bash
mkdir -p ~/.local/share/yolo-jail/modules/hello
cat > ~/.local/share/yolo-jail/modules/hello/manifest.jsonc <<'EOF'
{
  "name": "hello",
  "description": "Smoke test — injects HELLO=world into every jail",
  "version": 1,
  "jail_env": {"HELLO": "world"}
}
EOF
yolo modules list                  # => enabled  hello  intercepts=[—]
yolo -- bash -c 'echo $HELLO'     # => world
```

Remove the directory to uninstall. No state lives outside it.

## See also

- [`modules/claude-oauth-broker/README.md`](../modules/claude-oauth-broker/README.md) — reference implementation.
- [`src/modules.py`](../src/modules.py) — loader source + docstring with the machine-readable schema.
- [`docs/claude-oauth-mitm-proxy-plan.md`](claude-oauth-mitm-proxy-plan.md) — design notes that shaped this architecture.
- [`docs/claude-token-logouts.md`](claude-token-logouts.md) — operational triage for Claude logouts; the broker module is Step 3's fix.
