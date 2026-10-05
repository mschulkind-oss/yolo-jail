# Writing a Loophole

This page is for pack authors. It describes how to build a **loophole**, a named connection from a
jail to one capability on your host, and ship it in a pack. To use the loopholes yolo already
ships, see [Host Access and Loopholes](loopholes.md). For a simpler way to run a program of your
own on the host for the jail, declared straight in your config, see
[Your Own Host Service](host-services.md).

The shipped loopholes are the best examples to read alongside this page. Each lives in its pack's
folder in the repository:

- [`claude-oauth-broker`](https://github.com/mschulkind-oss/yolo-jail/blob/main/packs/claude/loopholes/claude-oauth-broker),
  in the `claude` pack: a host service that intercepts Claude's login refreshes and takes them one
  at a time.
- [`host-processes`](https://github.com/mschulkind-oss/yolo-jail/blob/main/packs/host-processes):
  an allowlisted, read-only view of host processes, with a host service, a self-check and its own
  settings.
- [`audio`](https://github.com/mschulkind-oss/yolo-jail/blob/main/packs/audio): no service at all,
  only host sockets mounted into the jail.
- [`journal`](https://github.com/mschulkind-oss/yolo-jail/blob/main/packs/journal),
  [`serial`](https://github.com/mschulkind-oss/yolo-jail/blob/main/packs/serial),
  [`aws-auth`](https://github.com/mschulkind-oss/yolo-jail/blob/main/packs/aws-auth),
  [`openai-auth-broker`](https://github.com/mschulkind-oss/yolo-jail/blob/main/packs/openai-auth/loopholes/openai-auth-broker)
  and [`cgroup-delegate`](https://github.com/mschulkind-oss/yolo-jail/blob/main/packs/cgroup-delegate).

Every loophole comes from a pack, yolo's own included. Start yours in the **local pack**, the
folder `~/.config/yolo-jail/local/`, which yolo selects automatically whenever it exists. Once it
works, move the same folder into a pack you share; nothing in it changes.

## A first loophole

This one needs no program on the host. It mounts a folder from the loophole into every jail, and
sets a variable pointing at it:

```bash
mkdir -p ~/.config/yolo-jail/local/loopholes/hello/assets
echo "hello from the host" > ~/.config/yolo-jail/local/loopholes/hello/assets/greeting.txt
cat > ~/.config/yolo-jail/local/loopholes/hello/manifest.jsonc <<'EOF'
{
  "name": "hello",
  "description": "Smoke test: mounts a greeting into every jail",
  "version": 1,
  "default_enabled": true,
  "transport": "none",
  "host_bind_mounts": [
    {"host": "{loophole_dir}/assets", "container": "/opt/hello", "readonly": true}
  ]
}
EOF
cat > ~/.config/yolo-jail/local/pack.json <<'EOF'
{
  "name": "local",
  "contributes": [
    {"kind": "loophole", "from": "loopholes/hello"},
    {"kind": "env", "vars": {"HELLO_FILE": "/opt/hello/greeting.txt"}}
  ]
}
EOF
yolo pack lint ~/.config/yolo-jail/local       # checks the manifest
yolo loopholes list                            # shows hello as active
yolo -- bash -c 'cat "$HELLO_FILE"'            # prints the greeting
```

If your local pack already has a `pack.json`, add the two `contributes` entries to it instead of
replacing the file. To remove the loophole, delete its folder and its two entries.

## The files

```
~/.config/yolo-jail/local/
├── pack.json                  # names the loophole: {"kind": "loophole", "from": "loopholes/<name>"}
└── loopholes/<name>/
    ├── manifest.jsonc         # required
    ├── ca.crt                 # optional; a certificate the jail trusts
    ├── <your program>         # optional; what runs on the host (or a download, below)
    └── README.md              # optional; for people using it
```

- **`from` is required** and points at the loophole's folder inside the pack. It may not be
  absolute or contain `..`, and a folder that does not exist stops the launch with its name.
- **The loophole's `name` must equal its folder's name.**
- **A loophole name belongs to one pack.** Two selected packs shipping the same name stop the
  launch, naming both.

## The manifest

```jsonc
{
  "name": "my-loophole",          // required; must match the folder name
  "description": "…",             // one-line summary, shown by `yolo loopholes list`
  "version": 1,                   // manifest format; currently 1
  "default_enabled": false,       // on when the pack is selected? Absent means off
  "transport": "loopback-tls",    // or "none" for no host program; default "loopback-tls"
  "lifecycle": "spawned",         // "spawned" when yolo starts a host_daemon, else "external"
  "intercepts": [                 // optional; TLS hostnames the jail is sent to you for
    {"host": "example.com"}
  ],
  "broker_ip": "host-gateway",    // intercepts only; where those hostnames point
  "ca_cert": "ca.crt",            // intercepts only; mounted and trusted in the jail
  "state_files": ["ca.crt"],      // optional; which state files the jail may read
  "doctor_cmd": ["bin", "--ok"],  // optional; run by `yolo check` and `yolo loopholes status`
  "host_daemon": {                // optional; a program yolo runs ON THE HOST
    "cmd": ["python3", "{loophole_dir}/my-daemon.py", "--socket", "{socket}"],
    "env": {"FOO": "bar"},        // optional; the program's environment, never the jail's
    "publishes": "socket",        // required in a pack: write it every time
    "request_end": "framed",      // or "eof"; default "framed"
    "preamble": true,             // default true
    "scope": "jail",              // or "host" (one per machine); default "jail"
    "launch_check": false         // answers the launch's check, below; default false
  },
  "jail_daemon": {                // optional; a program run INSIDE the jail
    "cmd": ["{jail_loophole_dir}/my-agent", "--listen", "{listen}"],
    "listen": "127.0.0.1:1470",   // optional; the jail address it serves at
    "restart": "on-failure",      // or "always" / "no"; default "on-failure"
    "caller_token": true,         // optional; default false
    "host_cmd": ["yolo", "…", "--listen", "{listen}"]  // optional; yolo's own packs only, below
  },
  "host_bind_mounts": [           // optional; host paths mounted into the jail
    {"host": "{loophole_dir}/assets", "container": "/opt/thing", "readonly": true}
  ],
  "host_devices": ["/dev/snd"],   // optional; device nodes passed through
  "requires": {                   // optional; if unmet, the loophole is inactive
    "command_on_path": "claude",
    "file_exists": ".config/pulse/cookie"   // relative to your home
  },
  "platforms": ["linux", "darwin/arm64"],  // optional; omit for every platform
  "binaries": {                   // optional; programs downloaded for it, below
    "my-agent": {"linux/amd64": {"url": "https://…", "sha256": "…"}}
  },
  "settings": {}                  // optional; config keys of its own, below
}
```

Only `name` is required. A key yolo does not know is an error in `yolo pack lint`, so a typo is
caught while you write; a launch only warns about one, so a manifest written for a newer yolo still
loads.

### Keys for turning it on

**`default_enabled`** is your choice, as the pack's author, for what happens when the user says
nothing. Leave it out, and the loophole stays off until the user turns it on. Set it to `true` only
when the pack is broken without it: `claude-oauth-broker` does, because Claude jails without it log
each other out.

**`loopholes.<name>.enabled`** in a config file is the user's switch, and it overrides yours in
either direction. The user writes it, or runs `yolo loopholes enable <name>` in a project to switch
it for that project alone; see [The loopholes yolo ships](loopholes.md#the-loopholes-yolo-ships). A manifest that
still says `enabled`, the key's old name, is refused with a message naming the rename.

**`requires`** makes the loophole inactive on a machine that lacks something:
`command_on_path` names a program that must be on the host's `PATH`, and `file_exists` names a
file inside the loophole's folder (`{loophole_dir}/<file>`) or a path relative to your home
(`.acme/credentials`).

**`platforms`** says where the loophole can run at all, such as a daemon compiled only for Linux.
Each entry is an OS, or an OS and architecture, spelled the way Go spells them: `linux`,
`darwin/arm64`. Leave it out for every platform; an empty list is refused. On a platform it does
not support, the launch names the loophole and the platforms it does support.

### Placeholders

A manifest can name files that ship beside it. The loophole's folder has one path on the host and
another in the jail, so there is a placeholder for each:

| Placeholder | Becomes | Use it in |
|---|---|---|
| `{loophole_dir}` | the loophole's folder, on the host | `host_daemon.cmd`, `doctor_cmd`, `host_bind_mounts[].host` |
| `{jail_loophole_dir}` | the same folder in the jail: `/etc/yolo-jail/loopholes/<name>` in a container, and yolo's read-only copy of it on `macos-user` | `jail_daemon.cmd` |
| `{socket}` | the Unix socket your host program listens on | `host_daemon.cmd` |
| `{state}` | this loophole's state folder on the host | `ca_cert` |
| `{listen}` | the jail address from `jail_daemon.listen` | `jail_daemon.cmd`, and `env` values |
| `{settings}` | a file holding the user's settings for this loophole | `host_daemon.cmd`, `doctor_cmd` |
| `{repository_scope}` | a file holding the repositories this launch approved, for a `brokered` loophole | `host_daemon.cmd` |
| `{binary:<name>}` | the downloaded program `<name>`, built for your machine | `host_daemon.cmd`, `doctor_cmd` |
| `{jail_binary:<name>}` | the downloaded program `<name>`, built for the jail, at `/etc/yolo-jail/loophole-binaries/<loophole>/<name>` | `jail_daemon.cmd` |

Each is refused where it would mean the wrong thing, such as a host path in the jail's command.

### A program that runs a host login's commands for the jail

A loophole whose host program runs commands with a login of yours, such as the `github` pack's
`github-broker`, declares a **`brokered`** block, so it only ever touches the project's own
repositories:

```jsonc
"brokered": {
  "source": "github",                 // the name the approved list is kept under
  "remote_host": "github.com",        // whose git remotes make up the list
  "credential_paths": ["~/.config/gh", "$GH_CONFIG_DIR"]  // where the login lives on the host
}
```

At every fresh launch that starts the program, yolo reads the project's git remotes on
`remote_host` as text, asks the user to approve the list in the config-change prompt, and hands
the approved list to the program in the file `{repository_scope}` names. Your `host_daemon.cmd`
must name that placeholder, and the program must refuse anything outside the list. With your pack
selected, a project's `mounts` entry that reaches a `credential_paths` entry, or yolo's own broker
folder, is refused.

A `brokered` loophole is turned on one project at a time, only by `yolo loopholes enable <name>`
run in that project, so its manifest may not set `default_enabled` to `true`, and a user's or a
project's config file may not switch it.

### A program on the host

A **host daemon** is a program yolo starts on your machine for the jail. With `transport:
"loopback-tls"`, the default, and a `host_daemon`, it works like this:

1. Your program listens on a plain Unix socket at the path yolo puts in `{socket}`. Any language
   that can open a Unix socket works: Python, Node, Go, a `socat` script.
2. yolo waits for the socket, then runs its own encrypted, authenticated front in front of it, and
   gives the jail a small file saying how to reach it. The variable
   `YOLO_SERVICE_<NAME>_ENDPOINT` in the jail names that file.
3. When the jail exits, yolo stops your program.

`"publishes": "socket"` selects this, and a pack must write it: the other value, which is also the
default, has your program implement the encrypted front itself, and packs may not do that. The
messages that travel over the socket follow yolo's
[frame protocol](https://github.com/mschulkind-oss/yolo-jail/blob/main/docs/reference/loophole-protocol.md).

Three keys shape the connection:

- **`request_end`**: set it to `"eof"` if your program reads a request until the connection's end
  before it answers. With the default, `"framed"`, such a program waits forever. Leave the default
  if your messages carry their own length.
- **`preamble`**: every connection starts with a short message from yolo naming the jail, so your
  program knows which jail is calling. Set `false` only if your protocol cannot accept that message;
  you then know only what the caller claims.
- **`scope`**: `"jail"`, the default, runs one copy of your program per jail and stops it with the
  jail. `"host"` runs one copy for the whole machine, shared by every jail, and it keeps running
  when a jail exits. Use `"host"` when a second copy would be a bug, such as a program holding a
  single-use login. Check on it with `yolo loopholes status`.

**`launch_check: true`** lets your program warn the user before their jail starts. Right after
starting your program, or finding it already running, the launch sends it one request,
`{"action": "launch-check", "budget_ms": <n>}`, and your program answers
`{"warnings": [...], "notes": [...]}`: each warning prints as a yellow line naming your loophole,
each note as a dim one, and then the jail starts anyway. Answer within the budget, from what your
program already knows; a program that does not answer in time, or at all, gets a dim line saying
so. A `"host"` program started by an older yolo keeps running after the user upgrades, and when
it answers `unknown action: launch-check` on stderr with a non-zero exit, the launch prints a
yellow line saying it predates this yolo and naming `yolo host-daemon restart <name>`. The launch
asks only when the jail uses your loophole's jail program, or when you declare none, and a new
agent attaching to a running jail is asked about the same way. `aws-auth` uses it to say that an SSO session has lapsed. The
[frame protocol](https://github.com/mschulkind-oss/yolo-jail/blob/main/docs/reference/loophole-protocol.md#the-launch-check)
has the details.

**Where the program lives matters.** yolo refuses a host program, or a loophole folder, inside the
project being launched or inside the jail's home, because an agent in the jail could rewrite it
before the next launch. Keep it in your pack, or somewhere like `~/.local/bin`.

The program's output is logged at `~/.local/share/yolo-jail/logs/host-service-<name>.log`. Once
that log passes 4 MiB, the next launch keeps its newest 4 MiB in `host-service-<name>.log.1`,
replacing the older copy there, and empties the log.

### A program in the jail

A **jail daemon** is a program yolo starts and restarts inside the jail. When it serves on the
jail's own network, give its address once, in `listen`, and write `{listen}` wherever it is needed:
in its `cmd`, and in a pack `env` entry that tells a client where to find it. Mark such an `env`
entry with `served_by` and the loophole's name:

```jsonc
{"kind": "env", "served_by": "my-loophole", "vars": {"MY_URL": "http://{listen}/v1"}}
```

In most jails the address is the one you wrote. A jail that shares the host's network, or a jail
inside another jail, gets a free port picked for that launch instead, so two of them do not collide.

**`caller_token: true`** makes the jail daemon accept only callers that hold a secret. The jail's
network is not private in every setup: a jail on `network.mode: "host"` shares your machine's. With
it set, each launch creates a new random token and gives it to your daemon and to the jail as
`YOLO_SERVICE_<NAME>_TOKEN`, and also writes it to `/run/yolo/caller-tokens/YOLO_SERVICE_<NAME>_TOKEN`.
Your daemon refuses any request without it.

**`host_cmd`** runs the same helper on your machine instead of inside the jail, for a launch whose
agent shares your machine's network, which on a Mac is the `macos-user` backend. The launch starts
it outside the sandbox, on the port it picked for `listen`, hands it the caller token, and stops it
when the agent exits. It needs `listen` and `caller_token`, and `{listen}` is the only placeholder
it takes. Only yolo's own packs may use it: the Codex and AWS credential helpers do. In any other
pack yolo does not run it and says so at launch, and the `jail_daemon` is handled as if `host_cmd`
were not there: it runs inside the sandbox, unless the sandbox cannot run it as written. A `cmd`
that names `{jail_loophole_dir}`, like the example above, runs there from yolo's copy of your
loophole's folder, so the program it names must be one a Mac can run: a script, or a macOS
build. A Linux executable is one the sandbox cannot run, and the launch then says the helper runs
nowhere and that a container runtime runs it (`YOLO_RUNTIME=podman` or `YOLO_RUNTIME=container`
for one launch).

### A program your pack downloads

A program you ship in the loophole's folder works when the pack is selected by its path. On
`macos-user` it must be one a Mac runs, a script or a macOS build. It does not work for a pack
yolo ships inside itself, whose files cannot be executable. Either way, a compiled program needs
one build per machine. So a loophole can name its program as a
**download** instead: one build per platform, each with an `https` address and the file's
`sha256`, which is required.

```jsonc
"binaries": {
  "my-agent": {
    "linux/amd64": {"url": "https://example.com/releases/v1/my-agent-linux-amd64",
                    "sha256": "<64 hex digits, as sha256sum prints them>"},
    "linux/arm64": {"url": "https://example.com/releases/v1/my-agent-linux-arm64",
                    "sha256": "…"}
  }
},
"jail_daemon": {"cmd": ["{jail_binary:my-agent}", "--listen", "{listen}"], "listen": "127.0.0.1:1470"}
```

- **The platform** is an OS and an architecture, both, spelled the way Go spells them. A program
  the jail runs needs a `linux/<architecture>` build: the jail is Linux on your machine's own
  architecture, a Mac included. A program your machine runs needs a build for your machine, such
  as `darwin/arm64`.
- **Name it where it runs**: `{jail_binary:<name>}` in `jail_daemon.cmd`, `{binary:<name>}` in
  `host_daemon.cmd` or `doctor_cmd`. Each name must be declared, and each declared program must
  be named somewhere.
- **`yolo pack install` downloads it**, checks it against the `sha256`, and keeps it with its
  execute bit set. A launch never downloads one: until you run install, the launch says the
  loophole is waiting for its program and names the command. A file whose `sha256` does not match
  is refused, and install fails.
- **On a machine with no build**, the loophole does nothing and the launch says so, listing the
  platforms you do build for.
- **On `macos-user`**, a jail program named this way does not run, because the sandbox has no
  copy of the jail's file. A host program does.

The address and the `sha256` show in `yolo pack footprint`, so a user can check what they would
run. Pin the `sha256` of each release, and publish a new address when the file changes.

### Intercepting a website

`intercepts` sends the jail's TLS connections for the hostnames you list to your host daemon
instead of the real site. `ca_cert` is the certificate authority your daemon signs with; yolo
mounts it into the jail and makes Node programs there trust it. `claude-oauth-broker` works this
way. A `ca_cert` must be a file your pack ships, such as `"ca.crt"`, or one in the loophole's state
folder, such as `"{state}/ca.crt"`; a state file survives restarts, so clients keep trusting it.

**`state_files`** lists which files in the state folder the jail may read. A loophole with a jail
daemon gets its state folder mounted read-only in the jail, and without `state_files` that is the
whole folder, so declare it whenever the folder holds anything private, such as the certificate
authority's key.

### Mounts and devices

`host_bind_mounts` mounts host paths into the jail, and must stay `readonly: true`. A host path may
use `{loophole_dir}`, `$HOME` or an absolute path, but not `..` or `:`. A read-only mount does
**not** protect a Unix socket: a jail can still talk both ways over it, so treat a socket mount as
full access to whatever is behind it. Name a socket `*.sock` so that yolo describes it that way.

`host_devices` passes device nodes, such as `/dev/snd`, into the jail with read and write access.

When a pack `env` entry tells a client where to find something your loophole mounts, mark it with
`served_by` and the loophole's name, as you would for a jail daemon:

```jsonc
{"kind": "env", "served_by": "my-loophole", "vars": {"MY_SOCKET": "unix:/run/my-loophole/sock"}}
```

The variable is then set only in a jail that gets your mounts: a container jail with the loophole
on and active on that machine. `yolo host` and `macos-user` mount nothing into a jail, and a jail
with the loophole off gets none of its mounts, so each of these leaves the variable out and says
so at launch. A client there uses its own default instead, which under `yolo host` is your
machine's own service. The `audio` pack marks `PULSE_SERVER` and `PIPEWIRE_REMOTE` this way.

### What a pack's manifest may not use

yolo holds every manifest to a few rules, because a pack is something other people install:

| Not allowed | Use instead |
|---|---|
| `jail_env` | a pack `env` contribution. It applies whenever the pack is selected, even if the loophole is not active, unless you mark it `served_by` the loophole |
| `readonly: false` in a mount | a read-only mount, or a host daemon that does the writing |
| `publishes` left out, or `"endpoint"` | `"publishes": "socket"` |
| an absolute or `$VAR` path in `ca_cert` or `requires.file_exists` | a path inside the loophole's folder; `{state}/…` for `ca_cert`; a path relative to your home for `file_exists` |
| a name another selected pack uses | a different folder name |

`yolo pack lint` names each problem and what to write instead. The reasons behind each rule are in
[the loophole system reference](https://github.com/mschulkind-oss/yolo-jail/blob/main/docs/reference/loophole-system.md#the-pack-shipped-subset).

## Settings of its own

A loophole can declare config keys for the user to set, with types and defaults:

```jsonc
// in the manifest
"settings": {
  "visible": {
    "type": "string_list",     // string, bool, int or string_list
    "scope": "workspace",      // "workspace" (either config file) or "user" (user config only)
    "default": [],
    "description": "process names this loophole may reveal"
  }
}
```

```jsonc
// in the user's config
"loopholes": {
  "host-processes": { "settings": { "visible": ["sway", "waykeeper"] } }
}
```

- A key you did not declare, or a value of the wrong type, is a config error that names the keys
  that exist.
- A key with no `scope` can be set only in the user config. Write `"scope": "workspace"` to let a
  project's `yolo-jail.jsonc` set it too. For a list, a project can only add entries, never remove
  the user's.
- yolo reads the values once, when the jail starts, and writes them to a file your program gets
  through `{settings}`. A change takes effect at the next jail start.

## What the user sees

Before selecting your pack, a user can run `yolo pack footprint <pack>`: it lists everything the
pack's loopholes do on the host, one line each, and marks a host program
`⚠ RUNS CODE ON YOUR MACHINE`. Every launch lists the same things again before it starts anything.
The lines come straight from your manifest, so your program's command appears as you wrote it.

Anyone who can add your pack to their config can already run programs as themselves, so selecting
the pack is the whole decision; yolo asks nothing more. Because a pack can change between fetches,
people sharing a pack that runs a host program should pin it to a tag; see
[Sharing a pack](migrating-to-packs.md).

When a user removes your pack from `packs`, the next launch does not start its loopholes. yolo
moves their state folder and log into `~/.local/share/yolo-jail/state/.retired/`, rather than
deleting them, and
`yolo prune` removes old copies. Removing the pack cannot undo what a program already did while it
ran.

## Where loopholes run

| Setup | What runs |
|---|---|
| Podman, on Linux or a Mac | Everything |
| Apple Container | Nothing reaches the host yet: the jail cannot connect to a host program |
| `macos-user` | Host daemons run. Jail daemons run inside the sandbox, a program from your loophole's folder included, except a Linux executable, one whose `cmd` names `{jail_binary:<name>}`, and one that intercepts a website; yolo's own credential helpers run outside it (`host_cmd`) |

In each case the launch names the loopholes that do nothing and says why.
[What works on each setup](../reference/settings-per-setup.md#the-loopholes-host-services-a-jail-can-use)
has the detail.

## Commands

```bash
yolo pack lint <folder>          # check a pack you are writing, loopholes included
yolo pack footprint <folder>     # what it touches on the host
yolo pack install                # download the programs your selected packs declare
yolo loopholes list              # every loophole, and whether each is active
yolo loopholes status            # run each loophole's doctor_cmd
yolo loopholes enable <name>     # on for this project only, from its next fresh launch
yolo check                       # includes each loophole's self-check
```

Inside a jail, `yolo loopholes list` shows what is active there.

## See also

- [Frame protocol](https://github.com/mschulkind-oss/yolo-jail/blob/main/docs/reference/loophole-protocol.md):
  the messages between the jail and a host program.
- [Loophole system reference](https://github.com/mschulkind-oss/yolo-jail/blob/main/docs/reference/loophole-system.md):
  how loopholes are selected and disclosed, and the reason for each rule on this page.
- [Loophole transport](https://github.com/mschulkind-oss/yolo-jail/blob/main/docs/reference/loophole-transport.md):
  how the encrypted front works and what it protects against.
- [Writing Your Own Pack](migrating-to-packs.md): the rest of a pack.
