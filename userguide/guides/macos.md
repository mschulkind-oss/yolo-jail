# macOS

yolo runs on macOS as well as on Linux. [Getting Started](../getting-started.md#quick-install) has
the install steps for every Mac setup; this page explains the choices behind them, what each setup
can and cannot do, and how to fix the problems specific to a Mac.

A Mac cannot run a Linux container directly, so every container setup runs a small Linux
**virtual machine** (VM) for you. On an Apple silicon Mac that VM runs ARM Linux natively, with no
emulation. The only time emulation appears is when you pull an image that exists for Intel Linux
only, such as some database images; that is a property of the image, not of yolo.

> [!NOTE]
> **Today, use Apple Container.** It is the recommended Mac setup, and Podman is fully supported
> too. `macos-user`, which needs no VM at all, is planned to become the main Mac setup once it is
> finished; it is still in development.

## Choosing a runtime

| Runtime | What it is | Choose it when |
|---|---|---|
| **Apple Container** (recommended) | Apple's own container tool: each jail runs in its own small VM, and there is no VM for you to manage | You have an Apple silicon Mac on macOS 26 (Tahoe) or later |
| **Podman** | Every jail runs inside one Linux VM, the **Podman Machine** | You have an Intel Mac or macOS before 26, or you need a feature Apple Container lacks, such as the shared Claude login or published ports |
| **`macos-user`** (in development) | The agent runs as a hidden macOS user inside Apple's built-in sandbox, with no container and no VM | You want the fastest start and accept a weaker boundary and missing features |

If both container runtimes are installed and running, yolo uses Apple Container. `macos-user` is
used only when you ask for it. To choose, set the `runtime` key in your config or the
`YOLO_RUNTIME` environment variable to `container`, `podman` or `macos-user`:

```bash
export YOLO_RUNTIME=podman     # for this shell; or "runtime": "podman" in your config
```

[Settings per setup](../reference/settings-per-setup.md#what-works-in-each-setup) compares all
setups feature by feature.

## Apple Container

[Apple Container](https://github.com/apple/container) runs each jail in its own lightweight VM,
with its own memory and CPU limits. It needs an Apple silicon Mac on macOS 26 or later, and yolo
needs `container` 1.1.0 or later for read-only folders. Install it with `brew install container`,
then `container system start`, which offers to install a Linux kernel the first time; the full
steps are in [Getting Started](../getting-started.md#macos-apple-container-recommended).

yolo builds the image Apple Container loads by itself; nothing else needs installing.

### Apple Container (`runtime: container`) — what it does not do

Apple Container is younger than Podman, and several features do not work there yet. Most of them
are announced when a jail starts; the ones that are not are marked **silent**.

**Host services.** A jail on Apple Container cannot connect back to the Mac, so none of yolo's
[loopholes](loopholes.md) work. In practice:

- Two jails sharing one Claude login can log each other out, because the service that takes Claude's
  refreshes one at a time cannot be reached. Claude still logs in and works.
- `codex`, `pi` and `opencode` cannot use a ChatGPT subscription login; they print
  `OpenAI login is required.` Use an API key instead. Claude's `codex` profile needs the same
  login, so it does not work there either.
- AWS Bedrock through `aws-auth`, serial devices and your own host services are skipped, one line
  each at launch.

**Networking.**

- `network.ports` is accepted but carries no data: opening `localhost:3000` on the Mac connects and
  receives nothing (**silent**). Connect to the jail's own address instead; `container ls` shows it.
- `network.forward_host_ports` stops the launch. Leave it unset on Apple Container.
- `network.mode: "host"` is not supported; leave the key unset.
- The jail has outbound internet. On macOS 15 there are two network faults, fixed below in
  [No outbound internet](#apple-container-no-outbound-internet-macos-15).

**Stopping jails.** `yolo stop` in a project stops its jail. `yolo prune` does not see stopped
Apple Container jails, so remove those with Apple's own commands:

```bash
container ls --all           # stopped jails too
container rm <name>
```

**Limits and storage.**

- With no `resources` setting, a jail still gets a cap: about half your memory (at least 4 GB) and
  half your cores (at least 2). `resources.cpus` must be a whole number here; `1.5` stops the launch.
- There is no process-count limit (**silent**).
- `/tmp` and the other scratch folders are always held in memory, whatever `ephemeral_storage` says
  (**silent**), so a build that writes large temporary files counts against the memory cap.
- `cache_relocations` is skipped with a warning.

**Folders from your Mac.** Read-only `mounts`, `workspace_readonly` and folder sources in
`host_files` need Apple Container 1.1.0 or later (`container --version`); below that each is skipped
with a warning rather than made writable. A read-write `mounts` entry works on any version.

**Not available:** USB devices, GPUs, `/dev/kvm`, containers inside the jail, and `nix` inside the
jail.

## Podman on a Mac

[Podman](https://podman.io/) runs every jail inside one Linux VM, the **Podman Machine**. It
supports the most of yolo's features on a Mac, and it is the runtime to use on an Intel Mac or on
macOS before 26. Install it and create the machine as in
[Getting Started](../getting-started.md#macos-podman).

**The shared folders matter.** A jail can use only a Mac folder the Podman Machine shares. Each
`-v` on `podman machine init` names one, and the list replaces Podman's defaults (`/Users`,
`/private`, `/var/folders`) rather than adding to them, which is why the command in Getting Started
repeats them. A Homebrew install of yolo also needs Homebrew's `Cellar` folder shared, because the
programs yolo hands to each jail live there. The list is fixed when the machine is created; to
change it, run `podman machine rm` and create the machine again. Check a folder with
`podman machine ssh -- test -d <folder> && echo shared`. yolo reads the machine's list too: a
launch that would use a folder the machine does not share stops before the jail starts, and names
the folder and the `podman machine init` command that adds it. `yolo check` reports whether your
project and yolo's own files are shared.

**The machine does not start by itself.** It keeps its settings across restarts; run
`podman machine start` when yolo says the runtime is not running.

**Its size is every jail's ceiling.** A `resources` limit larger than the machine is quietly
reduced to what the machine has. Check the machine with `podman machine inspect`, and resize it
with `podman machine stop`, then `podman machine set --memory 8192 --cpus 4`.

**What differs from Podman on Linux:**

- Loopholes reach the Mac through the machine, at `host.containers.internal`. That connection is
  tested nightly, but the services themselves have not all been run end to end on a Mac.
- A dev server published with `network.ports` must listen on `0.0.0.0`, not `127.0.0.1`, inside
  the jail.
- USB devices, GPUs, `/dev/kvm` and `nix` inside the jail are not available.
- `resources.io`, the disk-priority setting, has no effect, and the launch says so.
- The first launch copies the jail image into the machine, which can take many minutes.

### Running a live checkout on Podman

This is for working on yolo itself. When you run yolo from a clone of its repository, named with
`YOLO_REPO_ROOT`, the Linux programs a jail needs are built into your Nix store, `/nix/store`, which
the Podman Machine does not share by default. yolo refuses that launch and names the fix: create the
machine sharing `/nix` as well, and say so with `YOLO_NIX_HOST_DAEMON=1`:

```bash
podman machine init --cpus 4 --memory 8192 -v /nix:/nix \
  -v /Users:/Users -v /private:/private -v /var/folders:/var/folders
podman machine start
export YOLO_NIX_HOST_DAEMON=1
YOLO_REPO_ROOT=~/code/yolo-jail yolo
```

An installed yolo, from Homebrew, a release archive or `just deploy`, needs none of this.

## The macos-user backend

`macos-user` runs the agent directly on macOS, as a hidden user named `_yolojail`, inside Apple's
built-in sandbox (Seatbelt). There is no container, no VM and no Linux image: the jail's `packages`
are built as native Mac programs with Nix. It needs no container runtime, only Nix with your user
trusted, and a one-time setup.

> [!NOTE]
> **`macos-user` is in development.** It works, but several agents have not been tried on it yet
> and the features listed below are missing. It is planned to become the main Mac setup once it is
> finished; until then, use Apple Container.

**One-time setup.** Run it as your normal admin user, **not** with `sudo`; it asks for your password
for each step that needs it:

```bash
yolo macos-setup      # creates the hidden _yolojail user and /Users/Shared/yolo
```

**Where projects live.** The sandbox user cannot work inside your home folder, so a project must be
outside every user's home. Setup prepares `/Users/Shared/yolo` for this; put projects under it:

```bash
cd /Users/Shared/yolo/my-project
YOLO_RUNTIME=macos-user yolo -- claude     # or "runtime": "macos-user" in the project config
```

A launch from a project inside your home stops before it builds anything, and prints the
commands that move the project under `/Users/Shared/yolo` and share it there. A fresh clone or a
copy made there is shared as it is created.

`sudo` asks for your password once per launch to enter the sandbox; that is expected.

**Check without changing anything.** Run `yolo check` from the project. It checks each thing a
launch refuses to start without: that you are on macOS and not running as root, Seatbelt, the
sandbox user and its home folder, that the project is outside every user's home and shared with
the sandbox, and Nix. Each failure names the command that fixes it.
`YOLO_RUNTIME=macos-user yolo --dry-run` prints the full launch plan. Neither needs `sudo`.

**Remove it.**

```bash
yolo macos-teardown                                # removes the _yolojail user and its home
yolo macos-unshare /Users/Shared/yolo/my-project   # removes the sharing from one project
```

A file you *move* into a project, rather than create there, does not inherit the sharing, and the
next launch stops and names `yolo macos-fix-permissions`, which repairs it.

### macos-user trade-offs

What you gain: no runtime to install, no Linux image to build, the fastest start, and native Mac
builds of your `packages`.

What you give up:

- **A weaker boundary.** The sandbox confines what the agent can read and write, but there is no VM
  and no separate network: the agent is on your Mac's own network, and a port it opens is open on
  the Mac.
- **No resource limits.** Nothing caps memory or CPU, and a runaway build can slow the whole Mac.
- **Mac tools, not Linux ones.** The agent uses macOS's own `sed`, `grep`, `find` and `tar`, whose
  flags differ from the Linux ones, so a script written for Linux can fail. Without Xcode's command
  line tools (`xcode-select --install`) there is no `cc` or `make`.

### macos-user (native, no VM) — what it does not do

- **The shared Claude login is not coordinated between sessions.** Host services start on the
  Mac, so the ChatGPT login service and Bedrock through `aws-auth` work: the small helpers the
  agent talks to for them run for each launch on the Mac, outside the sandbox, answer only
  requests that carry that launch's token, which any program in its sandbox can read, and stop
  when it exits. The Claude login's in-jail half needs a container, and
  the launch names it on one `Declined:` line.
- **`mounts` only from folders outside every home.** A `mounts` entry, or a pack's `mount`, works
  when its folder is a read-only one under `/Users/Shared` or elsewhere on the startup disk (such as
  `/opt`), or a read-write one under `/Users/Shared/yolo`. The agent finds it under
  `$YOLO_CONTEXT_DIR`, not `/ctx`, as a link to the folder itself, so tools that resolve paths show
  the real path, and a subfolder the sandbox account may not read stays unreadable. Any other
  folder, including one in your home, on another disk, in the project or under `/tmp`, stops the
  launch and says why: move it, or use a container setup for that workspace.
  This has not yet been tried on a Mac. Folder sources in `host_files` are not delivered; single
  files are.
- **No `per_side_paths`**: a `.venv` or `node_modules` in the project is shared between your Mac and
  the sandbox.
- **No `cache_relocations`, `resources`, devices or GPU settings.** Each is named at launch.
- **MCP presets are not delivered**, although your own `mcp_servers` work if their commands exist on
  your Mac.
- **Language servers**: as on every setup, you bring the server program yourself.

Everything else in your config takes effect at every launch, since each `yolo` starts a fresh
sandbox. Mac GPU programs using Metal should work as they do for any Mac program.

## Building the image on macOS (cache vs. Linux builder)

The jail image is a Linux image, and a Mac cannot build Linux programs itself. Two things make that
a non-event:

1. **yolo's binary cache.** Most of the image comes from Nix's public cache, and the parts built from
   yolo's own source come from yolo's cache, `yolo-jail.cachix.org`, so a Mac downloads them instead
   of building. `yolo check` says "every image path is served from the binary cache" when nothing
   needs building.
2. **A temporary builder.** Anything in neither cache, such as a package you add under `packages`,
   is built by a temporary Linux container that yolo starts on your running runtime and removes
   afterwards. You need no VM of your own and no `sudo`; keep the runtime running. `yolo check`
   names any package that must be built this way.

Both need the **Nix daemon to trust your user**: Nix uses a project's own cache, and hands a build
to another machine, only for a trusted user. The recommended Nix install command does this for you;
[Trust your user](../getting-started.md#trust-your-user-required-on-a-mac) has the details.

> [!WARNING]
> **Do not set `extra-platforms = aarch64-linux` in your Nix config.** It tells Nix to run Linux
> programs on the Mac, which fails. `yolo check` warns if it is set.

If you already run your own Linux builder, such as nix-darwin's `linux-builder` or a Linux machine in
`/etc/nix/machines`, Nix uses it and yolo starts no container of its own; `yolo check` shows
"Linux builder configured".

### Nested Nix builds inside the jail (advanced)

On Linux, a jail can run `nix build` through your host's Nix. On a Mac it cannot: your Mac's Nix
store holds Mac programs, and the jail is Linux. This is not planned for now. Instead, add the tool
to `packages` and restart the jail, install a language runtime with mise, or use `macos-user`, whose
sandbox uses your Mac's own `nix`.

On Podman there is an expert opt-in, for a Podman Machine created with `/nix` shared **and** a Mac
Nix store that really holds the jail's Linux programs, for example because a Linux builder put them
there:

```bash
export YOLO_NIX_HOST_DAEMON=1       # the Podman Machine shares /nix
export YOLO_NIX_HOST_STORE_LINUX=1  # and the store holds the jail's Linux programs
```

> [!WARNING]
> **Set the second variable only if it is true.** The jail's own programs live in its Nix store, and
> this replaces that store with your Mac's. If the Mac's store lacks them, the jail cannot start and
> fails with `exec: "bash": executable file not found`. Apple Container has no such opt-in.

## Stopping a jail and reclaiming space

A jail normally ends when you exit the terminal session that started it. For one left running,
run `yolo stop` from the project folder. On `macos-user` there is nothing to stop. To reclaim
disk, use `yolo prune`, a dry run until you add `--apply`; see [Storage](storage.md).

## Troubleshooting

Start with `yolo check`. On a Mac it also checks the Nix daemon, whether it trusts you, the Nix
store volume, the runtime, and whether any package must be built from source.

### `yolo check` reports "Nix daemon: connected but user is NOT trusted"

It is only a warning, but fix it before your first launch: an untrusted user cannot use yolo's
binary cache or the temporary builder, so parts of the image cannot be built on a Mac. Add yourself
to the daemon's trusted users and restart it, as in
[Trust your user](../getting-started.md#trust-your-user-required-on-a-mac):

```bash
echo "extra-trusted-users = $(whoami)" | sudo tee -a /etc/nix/nix.conf
sudo launchctl kickstart -k system/org.nixos.nix-daemon
```

The row's note gives the fix for the Nix your Mac runs, with your account name filled in.

### `yolo check` reports "Nix daemon: connection failed"

The Nix daemon did not answer, and the rest of the row is Nix's own error. Restart the daemon:

```bash
sudo launchctl kickstart -k system/org.nixos.nix-daemon
```

Do the same when the row says `store operation timed out` instead, which means the daemon may be
hung. The row's note names the restart for the daemon your Mac runs.

### A build fails or hangs

1. Run `yolo check`. If it says the Nix daemon does not answer,
   [restart the daemon](#yolo-check-reports-nix-daemon-connection-failed).
2. If `yolo check` says a package must be built from source, make sure your runtime is running
   (`container system start` or `podman machine start`) and run `yolo` again.
3. A failed build stops the launch and shows Nix's own error; yolo never falls back to an older
   image.

The first build downloads a lot; later builds reuse what is in your Nix store. Copying the image
into the runtime adds more minutes on a Mac, and a later upgrade copies only what changed.

### Podman Machine won't start

A Mac that is itself a virtual machine, such as a cloud or CI Mac, needs nested virtualization for
the Podman Machine, and not every host offers it. Otherwise, recreate the machine; this deletes it
and every image in it, and the next `yolo` loads the jail image again:

```bash
podman machine stop
podman machine rm
```

Then create and start it with the command in [Getting Started](../getting-started.md#macos-podman).

### Podman: `statfs …: no such file or directory` when a jail starts

The Podman Machine does not share that folder. yolo normally stops before this and names the
folder; you see podman's own error only when yolo could not read the machine's list of shared
folders. Recreate the machine with the shared-folder list in
[Getting Started](../getting-started.md#macos-podman), adding the folder.

### Apple Container: "default kernel not configured for architecture arm64"

Apple Container needs a Linux kernel to boot its VMs. Install the recommended one:

```bash
container system kernel set --recommended
```

### Apple Container: "virtual machine failed to start"

Apple's virtualization framework limits how many folders one VM can share. yolo keeps the jail's
home in a single shared folder to stay under it, but many `mounts` entries can still reach the
limit. Remove some, or use Podman.

### Apple Container: image load fails

Check that the runtime is running (`container system status`) and that the disk has room, then run
`yolo` again. yolo builds its own image copier and needs no image tool on your PATH, so a missing
skopeo is never the cause. On yolo 0.10.0, `yolo check` still warns `No OCI conversion tool for
Apple Container`; ignore it. Later releases no longer check for one.

### Apple Container: no outbound internet (macOS 15)

Apple now supports Apple Container on macOS 26 only; this is for an older install on macOS 15. There,
a jail can start fine and then hang on `npm install` or on the agent's first request: it can reach
the gateway `192.168.64.1` and nothing beyond it. `yolo check` detects both variants below and
prints the fix.

**No forwarding.** macOS 15 fails to route the containers' traffic out. Supply the route yourself,
replacing `en0` with your network interface (`route -n get default | grep interface`):

```bash
sudo sysctl -w net.inet.ip.forwarding=1
echo 'nat on en0 from 192.168.64.0/24 to any -> (en0)' | \
  sudo pfctl -a 'com.apple/yolo-vmnet-nat' -f -
```

Both settings reset at reboot, so run them again after a restart. Upgrading to macOS 26, or using
Podman, fixes it for good.

**Mismatched network.** If a jail cannot reach even `192.168.64.1`, the container network came up
inconsistently. Restart it with `container system stop && container system start`. If it recurs, pin
the network in `~/.config/container/config.toml`, under `[network]`:
`subnet = "192.168.64.1/24"`.

### Loading the image by hand

If a launch cannot load the image and you are working from a clone of yolo's repository, you can
build and load it yourself. There is no `yolo build` command; call Nix directly from the clone:

```bash
nix --extra-experimental-features 'nix-command flakes' build --impure --accept-flake-config .#ociImage .#imageCopier

# Podman:
./result-1/bin/skopeo --insecure-policy copy \
    "nix:$(readlink -f ./result)" "docker-archive:/tmp/jail-image.tar:localhost/yolo-jail:latest"
podman load -i /tmp/jail-image.tar && rm /tmp/jail-image.tar

# Apple Container:
./result-1/bin/skopeo --insecure-policy copy \
    "nix:$(readlink -f ./result)" "oci-archive:/tmp/jail-image.oci:yolo-jail:latest"
container image load -i /tmp/jail-image.oci && rm /tmp/jail-image.oci
```

[Troubleshooting](troubleshooting.md) covers problems common to every platform.
