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

**That directory shape is the same wherever the loophole comes from** — bundled
under `bundled_loopholes/<name>/`, hand-placed in your home as above, or shipped by
a pack as `{ "kind": "loophole", "from": "loopholes/<name>" }`. One loader reads all
four sources, which is what makes a loophole developable standalone and then
droppable into a pack unchanged. See
[Shipping a loophole inside a pack](#shipping-a-loophole-inside-a-pack).

## Manifest schema (v1)

```jsonc
{
  "name": "my-loophole",          // required; must match directory name
  "description": "…",             // optional; one-line human summary
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
  "doctor_cmd": ["bin", "--ok"],  // optional; run by `yolo check` and `yolo loopholes status`
  "host_daemon": {                // optional; yolo spawns this ON THE HOST at jail startup
    "cmd": ["python3", "{loophole_dir}/my-daemon.py", "--socket", "{socket}"],
    "env": {"FOO": "bar"},        // optional; the daemon's spawn environment
    "publishes": "socket",        // or "endpoint" (you publish); default "endpoint"
    "request_end": "framed"       // or "eof"; default "framed"; socket-mode only
  },
  "jail_daemon": {                // optional; supervised INSIDE the jail by yolo-jaild
    "cmd": ["{jail_loophole_dir}/my-agent"],
    "restart": "on-failure"       // or "always" / "no"; default "on-failure"
  },
  "host_bind_mounts": [           // optional; host paths mounted into the jail
    {"host": "{loophole_dir}/assets", "container": "/opt/thing", "readonly": true}
  ],
  "host_devices": ["/dev/snd"],   // optional; device nodes passed through
  "requires": {                   // optional; unmet host prerequisites => loophole inactive
    "command_on_path": "claude",
    "file_exists": "$HOME/.config/pulse/cookie"
  },
  "platforms": ["linux", "darwin/arm64"]  // optional; omit = every platform
}
```

Note the second half of that census — `host_daemon`, `jail_daemon`,
`host_bind_mounts`, `host_devices`, `requires` — is every key with a host-side
effect: spawning a process on the host, mounting host paths, passing devices
through. Only `name` is required; `description` is optional (the loader does not
demand it). [`internal/loopholedecl`](../../internal/loopholedecl) is the authority
on their exact shapes: it is the manifest SCHEMA as a leaf package — decode plus
static validation, no `exec.LookPath`, no `os.Stat`, no predicate evaluation — so
a tool can read what a manifest declares without dragging the runtime along.
`internal/loopholes` keeps everything the schema cannot decide on its own
(resolving `{loophole_dir}`/`{state}` against real paths, evaluating `requires`,
discovery order, the container argv).

**A key the schema does not know is reported, not dropped.** Two strictnesses,
deliberately: an authoring read (`loopholedecl.Decode`) refuses an unknown key,
because a typo is otherwise a declaration that silently does nothing; discovery
reads TOLERANTLY (`LoadDirTolerant`) and prints one warning per unknown key
naming it, because a key only a newer build knows is version skew and refusing
it would take the whole loophole down. Either way you hear about it — a
half-configured loophole whose symptom names something else is the failure mode
this package keeps paying for. `"version"` is recognized and read by nothing; it
is not an enum yolo checks.

### The two module-dir tokens

A manifest can name files that ship beside it, and the module directory has **two
paths** — one on the host, one inside the jail (it is bind-mounted at
`/etc/yolo-jail/loopholes/<name>/`). One token with two resolutions is the kind of
asymmetry an author discovers by debugging, so there are two tokens and each is
refused in the wrong half at load:

| Token | Resolves to | Legal in |
|---|---|---|
| `{loophole_dir}` | the module dir **on the host** | `host_daemon.cmd`, `doctor_cmd`, `host_bind_mounts[].host` |
| `{jail_loophole_dir}` | `/etc/yolo-jail/loopholes/<name>` | `jail_daemon.cmd` |

Two more, both host-side: `{endpoint}` is where your daemon publishes (or, under
`publishes: "socket"`, where **yolo** publishes in front of you) and `{socket}` is
your own upstream socket path; `{state}` in `ca_cert` resolves to this loophole's
state dir. Under `publishes: "socket"` the first two **diverge** — see below.

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

### `host_daemon.publishes` — who implements the TLS server

- **`"endpoint"`** (the default): your daemon implements the whole loopback-TLS
  server itself — bind, certificate, token, atomic publish, constant-time compare.
  That is the unsupervised path, and
  [`loophole-protocol.md`](../design/loophole-protocol.md) spells out every step
  it must get right.
- **`"socket"`**: your daemon binds a plain AF_UNIX socket at `{socket}` and
  nothing else. yolo waits for that socket to accept, runs its own audited TLS
  front over it, and publishes the endpoint file itself — so the jail sees exactly
  what it would see from an `"endpoint"` daemon (same `YOLO_SERVICE_<NAME>_ENDPOINT`
  variable, same mounted endpoint file). Anything that can bind AF_UNIX qualifies:
  Python, Node, a `socat` script. Under `"socket"` the two tokens **diverge** —
  `{socket}` is your upstream path, `{endpoint}` is what yolo publishes in front of
  it — so a `cmd` naming `{endpoint}` here is refused at load with the fix, rather
  than publishing nothing while yolo publishes over it.

### `host_daemon.request_end` — how your request ends, behind the front

Socket-mode only, and **declare it if your daemon reads its request to EOF**:

- **`"framed"`** (the default): your protocol is length-prefixed, or otherwise
  knows where a request ends. The front does **not** propagate the client's EOF to
  your socket.
- **`"eof"`**: your daemon reads until EOF and then answers. The front half-closes
  the upstream socket the moment the request direction ends, so that read returns.

Getting this wrong is invisible from inside the daemon: a to-EOF daemon that works
perfectly on a bare socket **hangs forever** behind a `framed` front, because its
read never returns. The default cannot be `"eof"` — the broker relay's teardown
parity depends on the EOF not propagating — so it is one word in your manifest.

**Interception is not a transport.** It is declared by `intercepts` (plus
`broker_ip` and `ca_cert`) on any transport, and that list is what emits the
`--add-host` flags. A manifest that used to say `"transport": "tls-intercept"`
should say `"loopback-tls"` and change nothing else.

### `platforms` — where the loophole can run at all

`requires` says *"the thing I need is present"* — a runtime probe. It cannot say
*"I only exist for this platform"*, and the difference is not cosmetic: a compiled
Linux daemon on macOS gated only by `requires` is reported as an unmet
prerequisite, which reads as *install the missing thing* and can never succeed, and
a manifest with no `requires` at all goes Active, spawns, and dies five seconds
later through the readiness path. So the declaration is static:

```jsonc
"platforms": ["linux", "darwin/arm64"]
```

An entry is `<goos>` or `<goos>/<goarch>`, spelled exactly as **Go** spells them
(`go tool dist list`), and both halves are checked against a closed list —
`"darwins"` is a load error, not a loophole supported nowhere forever. A
GOOS-only entry matches every architecture on it. **Omit the key and every
platform is supported**, which is what every manifest written before the key
existed already meant; an explicitly *empty* list is refused, because it declares
support for nothing. A loophole unsupported here is reported **by name**, once,
with the platforms it does support and the sentence that nothing can be installed
to fix it — through the same one-line report that names a loophole made inert by
the backend (see the two-backend note below).

A build that predates the key reads it tolerantly (unknown key ⇒ skipped and
reported), so it treats the loophole as supported everywhere — i.e. exactly the
old behavior, which is why adding the key is safe. The other direction is not
tolerant: a GOOS/GOARCH **value** only a newer Go knows is a refusal on an older
build.

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
4. For `loopback-tls` / `spawned` loopholes — either a bundled manifest with a `host_daemon`, or the `loopholes` shorthand in the user config (see below); yolo handles spawning the daemon, bind-mounting its published path into the jail, and cleanup.
5. Merges `jail_env` into the container env.

A manifest yolo cannot load produces a **warning naming the file and the reason**,
and that loophole is absent for the rest of the run — no daemon, no endpoint, no
injected env var. `yolo loopholes list` also surfaces the error.

## The `loopholes` config block

**Install is user-scope; enable is either scope** — the ruled model
([`loophole-packaging.md`](../design/loophole-packaging.md) §4.3b). The
install-shaped keys — `command`, `env`, `doctor_cmd` (plus `description`) —
declare host execution, so they are legal only in the USER config,
`~/.config/yolo-jail/config.jsonc`. A workspace `yolo-jail.jsonc` (or
`yolo-jail.local.jsonc`) carrying any of them is a config ERROR host-side,
naming the file and the fix; inside a jail the same violation downgrades to a
warning, because the workspace is live-mounted and a hard error would refuse
every nested launch. `enabled` and `jail_env` are legal at either scope.

**And the program itself may not live where an agent writes.** Scope decides who
may *declare* host execution; it says nothing about the file that actually runs.
So an install naming a program inside the workspace yolo is about to bind-mount
`:rw` — or inside the jail-home tree (`~/.local/share/yolo-jail/home`, which *is*
`/home/agent`) — is refused, at either scope, by `yolo check`, by `yolo loopholes
list`/`status`, and at launch (§4.3a). The user who wrote the entry has host
access; the agent that can rewrite `tool.py` between launches does not. Keep
loophole daemons somewhere the jail cannot reach, e.g. `~/.local/bin`. The rule
covers the two trees yolo knows it hands over, not every directory some other jail
mounts.

One key is not a scope question at all: `doctor_cmd` on an entry that OVERRIDES a
manifest-backed loophole. The manifest fixes that loophole's `doctor_cmd`, and an
override only ever carries `enabled`, `env`, `jail_env` — so the key is refused in
the user config too, with "not overridable … remove this key". `doctor_cmd` is
user-scope-only in the INLINE shape, where it is part of the install.

yolo spawns the daemon process at jail startup, bind-mounts its published path into the jail, and tears down on exit. Entries appear in `yolo loopholes list` alongside file-backed loopholes so the whole picture lives in one command.

In `~/.config/yolo-jail/config.jsonc`:

```jsonc
"loopholes": {
  "host-processes": {
    "description": "Allowlisted view of host processes",
    "command": ["yolo", "internal", "daemon", "host-processes", "--endpoint", "{endpoint}"],
    "doctor_cmd": ["yolo", "internal", "daemon", "host-processes", "--self-check"]
  }
}
```

A workspace `yolo-jail.jsonc` may then route within the installed set:

```jsonc
"loopholes": {
  "host-processes": { "enabled": false },                  // disclosed at every launch
  "audio":          { "jail_env": { "PULSE_LOG": "1" } }
}
```

Because `enabled` stays workspace-writable, two disclosures replace what scope
no longer protects:

- A workspace `enabled: false` on an installed loophole prints one launch-time
  line naming the loophole and the file that disabled it, and `yolo check`
  **warns** instead of passing it green. This is the only protection left for
  a default-on loophole like `claude-oauth-broker` — an agent committing one
  line must not silently drop it.
- A workspace `enabled: true` naming a loophole that is **not installed** is a
  fatal config error that names the file and shows the user-config entry that
  would install it. That error is the human-in-the-loop moment: installing is
  a decision made in an agent-unwritable file. (`enabled: false` on an unknown
  name stays a harmless-no-op warning.)

> **A config entry's daemon keeps binding a plain Unix socket — and the jail now
> gets a real loopback-TLS endpoint anyway.** The daemon is a program yolo did
> not write and cannot re-link, so it binds an AF_UNIX socket at the path yolo
> substitutes into `{socket}` (host-only, outside the mounted services dir).
> yolo waits for that socket to accept, then runs its own TLS front over it and
> publishes the endpoint file itself — so the entry gets the same
> `YOLO_SERVICE_<NAME>_ENDPOINT` variable and mounted endpoint file a
> `manifest.jsonc` loophole gets, with its argv unchanged. `yolo loopholes list`
> prints `config/loopback-tls/spawned` for such an entry, which is the truth
> about what its jail dials. (The two remaining built-in AF_UNIX clients,
> `yolo-cglimit` and `yolo-journalctl`, are still ported in their own change.)
>
> The config block has no `request_end` key, so such a daemon is always fronted in
> `framed` mode: **a config-entry daemon that reads its request to EOF hangs behind
> the front** (see `request_end` above). A daemon of that shape needs a
> `manifest.jsonc`, where the field exists.

The example above matches the bundled manifest — compare
[`bundled_loopholes/host-processes/manifest.jsonc`](../../bundled_loopholes/host-processes/manifest.jsonc),
which is `loopback-tls` because its daemon is `yolo` itself.
The bundled daemons live behind `yolo internal daemon <name>` rather than
separate binaries, because the host ship set is deliberately just `yolo`.
Third-party daemons are any executable on the host's PATH.

Writing the daemon: use the [`internal/hostservice`](../../internal/hostservice)
helper package (see below).

## Shipping a loophole inside a pack

`loophole` is the **15th contribution kind** and it is the sharpest one: it is the
only kind whose claim is *host code execution* rather than a host read. Design:
[`loophole-packaging.md`](../design/loophole-packaging.md) (the authority), with
[`loophole-packaging-overview.md`](../design/loophole-packaging-overview.md) as its
readable half. `yolo config-ref` (the `packs` section) and
[`pack-system.md`](../design/pack-system.md) §3 carry the per-kind field reference.

A pack contributes a loophole by **pointing at a directory**, not by inlining a
manifest:

```jsonc
// pack.json
{ "contributes": [ { "kind": "loophole", "from": "loopholes/acme-proxy" } ] }
```

`from` is **required** (there is no conventional fallback directory the way
`skills` has one) and runs through the same traversal guard every path-bearing
field of every kind gets — absolute paths, `..` and `:` are refused. A `from`
naming a directory the staged pack does **not** contain is refused **by name**, not
skipped: you named a specific path and got nothing, and there is nowhere to fall
back to. There is no `into`: the host half runs on the host and the jail half
mounts at a path core owns (`/etc/yolo-jail/loopholes/<name>/`).

The directory `from` points at holds a `manifest.jsonc` — the **exact on-disk
shape** a bundled loophole under `bundled_loopholes/` or a hand-placed one under
`~/.local/share/yolo-jail/loopholes/` already has. That is deliberate and it is the
whole ergonomic point: develop the loophole standalone (drop the directory in your
home, `yolo loopholes list`, iterate until it works), then drop that same directory
into a pack **unchanged**. One loader reads all four sources, so there is no
pack-flavoured manifest dialect to port to.

**The loophole's `name` must equal the directory basename.** Enforced wherever a
manifest is read (`loopholedecl`: *"name='x' disagrees with directory 'y' — they
must match"*), which is what lets tooling name the loophole — and key its claims,
and run the name-exclusivity pre-flight — **without decoding the manifest at all**.

**Sole-owned by loophole NAME, not per pack.** A pack shipping three loopholes is
ordinary (the rule `program` has per `bin`); two packs shipping one name is fatal
at launch, naming both packs and both `from` paths. So is one pack declaring
`from: "a/acme"` and `from: "vendor/acme"` — same basename, so one name, and the
launch refuses rather than letting one shadow the other. A shadowed loophole name
is a daemon nobody audited running under a name you trust: the loser's manifest
would still push its bind mounts, devices and `jail_env` into the jail while the
winner's daemon ran.

**Selecting the pack is what activates the loophole; deselecting it is what stops
the loophole starting next launch.** There is no second switch to throw — no
`loopholes.<name>.enabled: true` needed, and no default-on: *nothing pack-shipped
is ever active by default*, because for this kind "active by default" would mean
yolo running a daemon on your machine that you did not ask for. Deselecting also
**retires the state that loophole left behind** — its state dir and its
`host-service-<name>.log` are archived (not deleted) under
`<state>/.retired/<timestamp>/<loophole>/` with a marker naming the pack that owned
them, and `yolo prune` reclaims old generations keeping the newest three. Archived
rather than deleted because that directory may hold the only copy of a CA private
key your long-lived TLS clients still trust. One gap worth knowing: a pack that is
still in `packs` but has **stopped declaring** a loophole is not detected —
retirement keys only on the signal you typed, the pack leaving `packs`, because
"the pack no longer declares it" is indistinguishable from "the pack tree was
momentarily unreadable" and the cost of guessing wrong is a moved private key.
Read the trust section below before relying on the deselect half for anything else.

**Two commands read your loophole before it ever runs.** `yolo pack lint` decodes
the module manifest **strictly** — an unknown key is an error, so a misspelled
`host_deamon` is caught at authoring instead of surfacing later as a missing
endpoint — and `yolo pack footprint` prints one line per claim the module makes,
with `⚠ RUNS CODE ON YOUR MACHINE` on the execution one. Discovery, by contrast,
reads **tolerantly**: a key only a newer build knows is version skew, and refusing
it would take the whole loophole down.

### The pack-shipped SUBSET, and why each rule exists

A pack-shipped loophole may declare **less** than a bundled one, and the asymmetry
is the point: a bundled manifest is yolo's own code in yolo's own repository,
reviewed by whoever reviews yolo, while a pack-shipped manifest is a distributed
artifact that lands on a stranger's machine. So the subset is not "the safe
fields" — it is *the fields whose enforcement does not depend on reading somebody
else's source*. Each row is a rule with a reason, not a nervous restriction:

| Rule | Why | What to use instead |
|---|---|---|
| `jail_env` **refused** | it emits `-e K=V` into the container, which is the `env` kind's target namespace, and collision detection keys on `{kind, target}` — so two *different* kinds claiming one variable could never be reported as a collision. (Not because namespaces are otherwise disjoint: `program` and `launch` already share the bin-name namespace by design. What is avoided is a fourth bespoke cross-kind collision pass) | the `env` kind: `{"kind": "env", "vars": {…}}`, which the footprint already reports and collides on — **and the honest cost is that it becomes UNCONDITIONAL.** A loophole's `jail_env` is set only while the loophole is *active*; an `env` contribution is set always. An audio-shaped pack would export `PULSE_SERVER` even on a machine where the sockets never crossed, pointing a client at a socket that is not there. That cost is tracked as **OQ-LP5** and the fix (the cross-kind pass) is purely additive |
| `host_bind_mounts[].host` **home-relative only** — no absolute path, no `..` segment, no `$VAR`, no `:` | this is the axis that matters. The `mount` kind is already home-relative-only and origin-gated; a loophole's `host` accepts *any* string, so leaving it wide would make `host_bind_mounts` a back door around `mount` — `{"host": "/", "container": "/ctx/root"}` in a jail whose agent runs as UID 0. `$VAR` is refused with the literals because `"${XDG_RUNTIME_DIR}"` names an absolute path one indirection later, and admitting the variable while refusing the literal would be a rule about spelling | a path inside your own module dir (`{loophole_dir}/<file>` — content your pack ships, already vetted by staging), or a path relative to the user's home. For a host path outside both: `mount` for an ordinary read, or a `host_daemon` that *mediates* the access rather than handing it over. (A **bundled** loophole keeps the wider vocabulary — `audio` names `/run/user/<uid>/pulse`, which no home-relative path can reach) |
| `readonly: false` **refused** | keeps a pack from asking for a writable host bind — **and note exactly what it does NOT cover: `:ro` is no boundary for a SOCKET.** Measured twice in this repo: a read-only bind of an AF_UNIX socket is fully connectable and bidirectional (the well-known `docker.sock:ro` result), because the kernel's read-only check exempts inodes that are not REG/DIR/LNK. So this rule only ever covers regular files and directories; binding a host socket `:ro` gives the jail unrestricted read-write access to whatever is behind it (a container socket, `ssh-agent`, `gpg-agent`, PipeWire) | omit the key, which defaults to `true`. If the bind IS a socket you lose nothing by the refusal — and gain nothing either; it is a no-op for sockets in *both* directions. Because of that measurement a socket bind is its own claim class, worded as host **IPC** rather than as a host read. If your pack genuinely has to WRITE a host file, declare a `host_daemon` that mediates it |
| `host_daemon.publishes` **must be `"socket"`** — and the *default* is refused too | the transport is a property of the framework, not of the loophole. Under `"endpoint"` your daemon would implement the loopback-TLS server itself — endpoint file mode, key persistence, constant-time token compare, frame length cap — and **yolo cannot detect a violation of any of them.** Tolerable for something you hand-wrote on your own machine; a different proposition for an artifact distributed to strangers. Self-publishing stays available to loopholes yolo itself ships, which are yolo's own code minting yolo's own credential. An absent `publishes` decodes to `"endpoint"`, so saying nothing has declared the mode you may not have — and since the fix is identical either way, the message does not distinguish them | write `"publishes": "socket"`: bind a plain AF_UNIX socket at `{socket}` and yolo runs the audited front over it and publishes the endpoint file for you. Costs one splice hop and buys the inability to get the TLS properties wrong. Declare `request_end: "eof"` if your daemon reads its request to EOF, or it hangs behind the front |
| **`platforms` declaration** (not a refusal — a field to use) | the front makes the *transport* portable; it does not make the *daemon* portable, and packs will ship native code. `requires` says *"the thing I need is present"* — a runtime probe — and cannot say *"I only exist for this platform."* Without the distinction, a compiled Linux daemon on macOS reads as an unmet requirement ("install the missing thing", advice that can never succeed) or fails five seconds later through a silent spawn path | declare the platforms (OS, and architecture where it matters) — see [`platforms`](#platforms--where-the-loophole-can-run-at-all) above. An unsupported loophole is reported **by name**, once, with the platforms it does support, through the *same one-line report* that names an inert backend: *"this loophole does nothing here, and here is why"* is one user-visible situation, not two |
| `ca_cert` **module-relative or `{state}` only** — no absolute path, no `$VAR`, no `..`, no `:` | the sharpest of the path-bearing fields, not the mildest. The file is bind-mounted from your host AND its container path is joined into `NODE_EXTRA_CA_CERTS`, so an absolute value hands **every node client in the jail** a certificate authority the user never chose — and the resolver passes an absolute value through as-is (it deliberately discards the module dir, or `filepath.Join` would produce `<module>/<abs>`) | a certificate your pack SHIPS (`"ca.crt"`, which resolves inside your module dir) or one in your own state dir (`"{state}/ca.crt"` — name-keyed, so it survives restaging, which is what makes a pack-shipped CA possible at all: a CA regenerated on every launch would break every long-lived TLS client in the jail) |
| `requires.file_exists` **module-relative or home-relative only** | the one scoped field that crosses NOTHING — no mount, no exec, just a `stat` whose boolean decides whether the loophole is active. It is scoped because the **answer leaks**: `yolo loopholes list` prints the resolved absolute path beside the inactive reason, so an unscoped field is an arbitrary host-filesystem probe with a readout — `"$HOME/.ssh/id_ed25519"`, and the pack reads the result out of the user's own command. (It gets no approval CLAIM, for the same reason: a claim is for a crossing, and a line in the prompt for a stat dilutes a prompt whose value is that every line is a real capability) | a path inside your module dir or relative to the user's home. To require a PROGRAM, use `requires.command_on_path`, which is untouched — it asks PATH about a name, and the answer names something installable |
| **reserved names refused** | a shadowed loophole name means a daemon nobody audited running under a name the user trusts — and it would be *half* a loophole: yolo's own daemon runs while the manifest's binds, devices and `jail_env` still cross into the jail. The reserved set is larger than "the bundled three": `audio`, `claude-oauth-broker`, `host-processes`, plus `cgroup-delegate` and `journal`, which are built-in service names with no manifest at all | pick another name (the loophole's name is its directory basename, so rename the directory). Refused at launch, fatally, naming both sides — the same pre-flight that refuses two packs claiming one name |

**Where each rule fires, and it is everywhere now.** The reserved-name and
name-exclusivity refusals are the launch pre-flight. The schema-level rules above
are implemented in [`internal/loopholedecl`](../../internal/loopholedecl)
(`PackShippedProblems`) and applied at **three** places, all through that one
predicate so none of them can be kinder than another:

- **`yolo pack lint`** — the authoring seam. A subset violation is a lint failure
  naming the field and the fix, so you hear it on your machine rather than from a
  launch warning on somebody else's.
- **discovery**, i.e. every launch. A pack module is loaded through
  `loopholes.LoadPackLoophole` (the source label selects the loader), so a manifest
  outside the subset is not discovered: no daemon, no binds, no devices, no
  `jail_env` — and a warning naming the reason.
- **`yolo check`** — its own walker reports the same violation as an invalid
  manifest, before you launch.

All three stay **tolerant of an unknown key**: a key only a newer yolo knows is
version skew, not a violation, so it is skipped and reported rather than making
your loophole vanish. That is deliberate and it is orthogonal to the subset — a
field you MAY NOT ship is refused by every build; a field this build has never
heard of is not.

Until 2026-08-14 none of the three applied the subset (both loaders existed and
had no production callers), so a `jail_env` or a `publishes: "endpoint"` manifest
was accepted where the rules said it should be refused. If you wrote a pack
against that behaviour, `yolo pack lint` now tells you exactly what to change.

Two backends make the whole thing inert regardless of your manifest: Apple
Container starts no loophole host services at all (a wider skip than the
`--add-host` one), and the macOS no-VM backend returns before loophole startup is
reached. Both now say so by name, one line per loophole, rather than looking
provisioned and configuring nothing — and **backend beats platform** when both
apply, because the line you can act on is "switch backends", not "get a different
machine".

### The trust story a pack author has to understand

**Every declaration that crosses the boundary becomes a claim string the user is
shown at install.** Not a summary of your pack — one approvable string per
crossing. The enumeration is **total**, and that is the load-bearing rule of the
whole design: a loophole that crosses the boundary and claims nothing has to be
*unrepresentable*, because the origin gate reads "claims nothing" as "nothing to
approve" and returns *yes* — which is how a draft of this design would have let a
fetched pack bind `~/.ssh` and `/` into a UID-0 jail with no prompt at all.

What your manifest emits, exactly:

| Declaration | Claim, one per | Reads as |
|---|---|---|
| `host_daemon.cmd` + `doctor_cmd` | one **base** claim per loophole | `RUNS <argv> and <argv> on your machine` — host EXECUTION. `doctor_cmd` folds in here rather than getting its own line because it is host execution too (`yolo check` and `yolo loopholes status` both run it), and one claim per program is the honest unit |
| `intercepts[]` + `broker_ip` | one per host | `INTERCEPTS <host> -> <broker_ip> — installs a CA trusted by every TLS client in the jail`. **This claim exists even with `transport: "none"` and no daemon:** an intercept runs no host code and still installs a standing capability over every hostname the jail dials. **`broker_ip` is folded in here rather than claimed separately** — it is not a second crossing, it is WHERE this intercept points, and leaving it out of the string made two manifests differing only in it compare as ONE approval: approve an intercept aimed at yolo's own front, and the address could later move with no re-prompt. The default is spelled out when the key is absent, so `"broker_ip": "host-gateway"` and no `broker_ip` are one approval rather than two |
| `ca_cert` | one claim | `TRUSTS the CA in <path> — mounted from your host and trusted by every node client in the jail`. Keyed by the raw path, so two different certificates are two approvals. **It is a crossing in its own right, not a detail of the intercept claim:** the file is bind-mounted from the host and its container path is joined into `NODE_EXTRA_CA_CERTS`, so a module-relative `ca_cert` with no intercepts at all still installs a CA every node client in the jail trusts |
| `host_bind_mounts[]` that looks like a **socket** | one per bind | `CONNECTS the jail to the host socket … — read-write host IPC`. Its own class because `:ro` is no boundary for a socket (measured — see the subset table) |
| every other `host_bind_mounts[]` | one per bind | `MOUNTS <host> -> <container>`, carrying the socket caveat verbatim rather than claiming "read-only" and stopping |
| `host_devices[]` | one per node | `PASSES THROUGH the host device <path> (reads and writes)`. Not weaker than a writable bind: `audio`'s own manifest describes `--device` as passing a node *so the cgroup device-allow rules permit reads/writes*, and the home-relative constraint does not reach a device node — which is precisely why it needs a claim |
| a manifest yolo **cannot read** | one claim, fail-closed | `DECLARATION UNREADABLE at <from> — its claims cannot be enumerated`, treated as host execution, because a manifest this build cannot parse may well declare a daemon. An unreadable declaration is not "no claims" — that is the empty set the gate reads as consent |
| `jail_daemon`, `state_files`, `requires` | **none, deliberately** | a `jail_daemon` is a process inside the container, the one place a pack's code was always allowed to run; `state_files` resolves inside yolo's own state tree, not a path you would recognise as yours; and `requires` crosses nothing at all — it is a `stat`, so a claim for it would put a line in the approval prompt for something that neither mounts nor runs. It is **path-scoped** instead (see the subset table), because the answer is readable even though nothing crosses |

**The socket class's discriminator is coarser than the design specified, and that is
worth knowing when you author.** The design says "a socket bind is its own claim
class"; the implementation cannot stat the path to find out, for two independent
reasons — the `host` value is **raw** (`{loophole_dir}/x`,
`${XDG_RUNTIME_DIR}/pulse/native`: resolving either would make the claim string
machine-specific), and a stat is a fact about *this machine at this moment*, so a
claim that changed class when a socket was absent would re-prompt on the machine
where it is missing. So the test is the only static evidence available:
**`readonly: false`, or a basename ending `.sock`/`.socket`.** A `:ro` bind of a
socket with a non-obvious name therefore lands in the MOUNT class — which is why
that class's text carries the socket caveat verbatim: nothing is understated, only
the discriminator is coarse. The named fix is a **declared socket bit** in
`internal/loopholedecl`, making the class a fact you state rather than one yolo
infers. Until then: if your bind is a socket, name it `*.sock` or declare
`readonly: false`, and note that under the pack-shipped subset the second of those
is refused — so **name it `*.sock`**.

**Host EXECUTION reads differently from a host read**, and it has to. "Reads a
file in your home" and "runs this argv as you" cannot share a line in a prompt, so
the claim spells out that it *runs* something *on your machine*, the same way a
wrapped plugin's claims spell out "RUNS CODE" — and the footprint marks it
`⚠ RUNS CODE ON YOUR MACHINE` rather than the plain `⚠ review` a host read gets.
`yolo pack footprint`'s review tail counts those separately and first, so a pack
that runs a daemon does not read as *"1 loophole"*. Two practical consequences for
you: the claim carries the **raw** argv from your manifest, placeholders
unexpanded and nothing elided, because it is a lockfile comparison key and not
display text; and it is rendered with shell quoting for **injectivity**, not for a
shell — nothing execs the string, but `["sh","-c","a b"]` and `["sh","-c","a","b"]`
must not collapse onto one approved claim.

**A fetched pack needs approval where a local one does not.** Origin bounds the
gate: an embedded pack or one at a path you control is permitted; a pack fetched
from a git URL must have every claim its *staged tree currently makes* approved in
the lockfile, and a missing or corrupt lockfile approves **nothing**. Approval
requires a real terminal — `yes | yolo pack install` is refused *before* the prompt
is shown, so a pipe is never invited to answer it.

**What "unapproved" costs, precisely, and what it does not.** NOTHING of the
loophole crosses: no host daemon, no bind mounts, no devices, no `--add-host`, no
CA, no `jail_env`. Its `doctor_cmd` cannot run either — `yolo check` and
`yolo loopholes status` refuse to execute a pack loophole's self-check without a
recorded approval. The pack's OTHER contributions still work; the refusal is
per-loophole, not per-pack. And you are **told**: the launch prints one line naming
the loophole, saying it was refused, and pointing at `yolo pack install` — before
that line existed, a pack whose whole purpose was a loophole silently did nothing,
which looks exactly like a loophole that is broken.

It is still **listed**. `yolo loopholes list` and `status` show it as `unapproved`
rather than omitting it, because a missing entry is indistinguishable from a pack
that failed to stage, and the route to approving it is not discoverable from an
absence.

And the per-launch disclosure names the host access in effect *every* launch — the
execution lines print **before** anything spawns, because after the spawn a line is
not a disclosure, it is a notification that something already happened. It prints
only what will ACTUALLY happen: a refused loophole's daemon argv is not in that
block, because the block's whole value is that every line in it is imminent, and a
withheld daemon shown as pending would be false in the one place you read to decide
whether to stop the launch. (`yolo pack footprint` still shows it — that command
answers what a pack WANTS, which is the question you ask before trusting it.)

**One thing the approval does not carry: the file's content.** Nothing compares
what changed between commits — approval is a string match, so if you rewrite your
daemon script without touching its argv, the next install prints one "moved from
abc → def" line and no prompt. The design's answer is not a digest (writing the
user config already requires host access as the user, so a dialog guarding it
protects nothing); it is the placement rule below, plus the plain advice that **a
tag pin is the documented shape for a pack carrying host execution** — following a
branch is a continuous trust decision, not a permission you exercised once.

**Installed content may not live where an agent writes** — the placement rule, and
here is *why* it exists rather than a content check. Every gate above governs a
**declaration**: who may write it, what string is recorded, where the pack came
from, what prints at launch. None of them reads the *file* the declaration names.
And those are two different actors: the human who wrote
`command: ["python3", "/workspace/tool.py"]` had all the permissions, while the
agent that rewrites `tool.py` afterwards has none of them. So a loophole whose
module dir, `host_daemon.cmd` target or `doctor_cmd` target resolves inside the
workspace being mounted `:rw`, or inside the jail-home tree yolo manages, is
refused by name. A refused **module dir** suppresses the argv refusals under it:
`{loophole_dir}` resolves to that dir, so a module dir in an agent-writable tree
means every host-side field names an agent-writable target — including the ones no
rule can see (a Python daemon's imports, a binary's `dlopen`). Keep your daemon
somewhere the jail cannot reach.

**And the limit, stated rather than discovered:** yolo knows the workspace it is
launching and the home trees it manages; it cannot know that `~/code/other-project`
is agent-writable in some *other* jail. The rule is a tripwire on the shape that
actually occurs — a daemon sitting in the repo being worked on — not a boundary.
The check is also deliberately conservative about what counts as a path (no
whitespace, no shell metacharacters), because a false positive would refuse a
working loophole at every launch.

**Selection controls ACTIVATION, not REVOCATION.** Deselecting your pack stops the
*next* launch from starting your daemon, and retires the state it left behind. It
does **not** stop a daemon that already ran. Teardown SIGTERMs (then SIGKILLs) the
whole process **group** — the spawn is `setsid`, so the group is reachable — so what
survives is narrower than "everything the daemon forked": it is whatever the daemon
deliberately placed *outside* its own group, a `~/.bashrc` line, a crontab entry, a
double-forked reparented process. Bounded, not absent. **A process that has executed
once on someone's host is outside yolo's ability to revoke, and no packaging design
changes that** — this one does not claim to. That asymmetry is exactly why the gate
that matters is the **install**-time one: the risk was never "a daemon runs", it is
"code nobody vetted runs".

## CLI

```bash
yolo loopholes list              # show every loophole, transport, enabled state
yolo loopholes status            # run every doctor_cmd
yolo loopholes enable <name>     # flip `enabled` → true (user-dir loopholes only)
yolo loopholes disable <name>    # flip `enabled` → false
yolo doctor                      # includes loophole self-checks in the combined report
```

For bundled, pack-shipped or config-inline loopholes the toggle is
`loopholes.<name>.enabled` in the user config,
`~/.config/yolo-jail/config.jsonc` (a workspace config can also set it — with
the disclosures above). `enable`/`disable` rewrite a `manifest.jsonc` in place, so
they only serve the hand-placed user directory; every other source is refused with
that instruction. A pack-shipped loophole named in `loopholes.<name>.enabled`
resolves to a real installed loophole — so it takes the override path rather than
the every-launch *"no loophole named 'x' is installed"* warning it would once have
produced.

`yolo loopholes list` prints each loophole's source, so a pack-shipped one shows
`pack/<transport>/<lifecycle>` beside the `bundled`, `user` and `config` rows.

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
- [`internal/loopholedecl/`](../../internal/loopholedecl) — the manifest schema itself: decode + static validation, no filesystem, no predicates. The authority on every key's shape.
- [`internal/loopholes/`](../../internal/loopholes) — the host-side registry: token resolution, `requires` evaluation, discovery order, runtime argv.
- [`internal/hostservice/`](../../internal/hostservice) — helper package.
- [`internal/hostprocesses/`](../../internal/hostprocesses) — reference `loopback-tls` consumer of the helper, reachable as `yolo internal daemon host-processes`.
- [`internal/svcendpoint/`](../../internal/svcendpoint) — the transport itself: endpoint file, cert pinning, token frame. Both halves in one package on purpose.
- [`docs/design/loophole-transport.md`](../design/loophole-transport.md) — why there is one transport and what it defends against.
- [`docs/design/loophole-packaging.md`](../design/loophole-packaging.md) — the `loophole` pack kind: the subset rules, the claim enumeration, the install/enable scope model. [`loophole-packaging-overview.md`](../design/loophole-packaging-overview.md) is its readable half.
- [`docs/design/pack-system.md`](../design/pack-system.md) §3 — the closed kind set the `loophole` kind is the 15th member of, and its footprint row.
- [`internal/frameproto/`](../../internal/frameproto) — reference codec for the wire format.
- [`bundled_loopholes/claude-oauth-broker/README.md`](../../bundled_loopholes/claude-oauth-broker/README.md) — the broker architecture that shaped this (the older mitm-proxy design notes are in git history).
- [`docs/research/claude-token-logouts.md`](../research/claude-token-logouts.md) — operational triage for Claude logouts; the broker loophole is Step 3's fix.
