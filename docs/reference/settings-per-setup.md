# Settings per setup

Every config key, and what each of the four setups does with it. This is the detailed companion to the
user guide's [What works in each setup](../guides/USER_GUIDE.md#what-works-in-each-setup): start there
for the short answers (can I launch, install an agent, log in, share files), and come here when you need
to know what one particular key does on your setup. Three blocks, grouped the way the config file is,
then what a running jail picks up when you run `yolo` again, and what a pack can contribute.

The four setups are the pairings of a backend with a host OS, described in the guide's
[setups table](../guides/USER_GUIDE.md#what-works-in-each-setup): `podman` / Linux, `podman` / macOS,
`container` / macOS ([Apple Container](https://github.com/apple/container)), and `macos-user` / macOS
(a sandboxed macOS process with no container and no VM).

How to read the tables:

- Each cell says what happens on that setup:

  | Cell | Meaning |
  |---|---|
  | `works` | It works. Some cells add how: `works — <how>`. |
  | `works differently` | It works, but not the way it does on Linux; the footnote explains. |
  | `absent, warns` | Not available on this setup, and the launch tells you so. |
  | `absent, silent` | Not available on this setup, and **nothing tells you**: the setting is accepted and has no effect. These cells are in bold, and they are the ones to check. |
  | `refuses` | yolo stops with an error that says what to do instead. |
  | `n/a` | Does not apply to this setup. |

- The **Takes effect** column says when an edit to that setting is picked up. `fresh launch` means
  when a new jail starts; `any entry` means every time you run `yolo`. Running `yolo` in a workspace
  whose jail is already running joins that jail instead of starting a new one, so a `fresh launch`
  setting you just edited waits until you stop the jail and launch again — see
  [What a running jail picks up when you run `yolo` again](#what-a-running-jail-picks-up-when-you-run-yolo-again).

## Workspace, mounts, and host files

This block is what the jail can see of your machine's filesystem, and what it may write. Read the table with two things in mind.

- **Takes effect** is when an edit to the key reaches a jail. Almost everything here is passed on the container command line, so it is frozen at the **fresh** launch: re-entering a running jail returns before the config-change check, and an edited value does nothing until you stop the jail and launch again. `macos-user` has no re-entry at all — every invocation builds a new sandbox — so on that column every row behaves as `any entry`.
- **Bolded cells are silent.** The key passes `yolo check`, the launch prints nothing, and the behaviour is simply not there.

| Row | `podman`/Linux | `podman`/macOS | `container`/macOS | `macos-user`/macOS | Takes effect |
|---|---|---|---|---|---|
| `mounts` — host dirs read-only at `/ctx` | works | works — VM must share the source[^vm-share] | works on 1.1.0+; older: `absent, warns`[^acro] | absent, warns[^mumounts] | fresh launch |
| `workspace_readonly` — lock workspace sub-paths | works — read-only overlay per path | works | works on 1.1.0+; older: **paths stay writable**, warns[^acro] | works — sandbox policy rule, not a mount | fresh launch |
| … and the `yolo-jail.jsonc` lock it also performs | works | works | works on 1.1.0+; below, lost **silently**[^acro] | **absent, silent** — config stays agent-writable[^mujsonc] | fresh launch |
| `per_side_paths` — `.venv`/`node_modules` not shared | works — private dir mounted over each path[^leftover] | works | works — unverified on Apple silicon[^achw] | `absent, warns` — host and sandbox share them[^mupsp] | fresh launch |
| `writable_home_dirs` — extra writable `$HOME` paths | works — read-write bind per path | works | works — the whole home is writable already | works — sandbox home is writable; dir not pre-created | fresh launch |
| `ephemeral_storage` — `volume` vs `tmpfs` scratch | works | works — volumes sit on the VM's disk | **always RAM-backed, silent**[^aceph] | `n/a` — the machine's real `/tmp`[^mueph] | fresh launch |
| `cache_relocations` — move a jail cache to other storage | works — a provisioning failure refuses the launch | expected to work, never measured[^vm-share] | `absent, warns` — not built[^cachegap] | `absent, warns` — not built[^cachegap] | fresh launch |
| `host_files` modes `readonly` / `once` / `copy` | works | works | works | works[^dac] | fresh launch |
| `host_files` mode `capture` — keep your local edits | works | works | works | works — needs a workspace ACL[^acl] | fresh launch |
| `host_files` source is a **file** | works — read-only bind | works[^vm-share] | works — copied in, **agent-writable**[^achostlayer] | works — root-owned copy at launch[^snap] | fresh launch |
| `host_files` source is a **directory** | works | works[^vm-share] | works on 1.1.0+; older: `absent, warns`[^acdir] | `absent, warns` — not built yet[^mudir] | fresh launch |
| `host_files` destination: writable, and private per workspace | works | works | works — whole home is per-workspace | works under `~/.config`; home-root files **shared, silent**[^mutier] | fresh launch |
| `host_files` entries with a `source:` come from your user config only | works | works | works | works | n/a — a repo's config cannot name host bytes |
| `host_management`, `host_wrappers`, `host_apply_on_launch`, `promotion_target` | works | works | works | works | any entry — host-side keys[^hostside][^wrappath] |
| Host-side verbs refuse when run **inside** the jail | works | works | works | **does not refuse, silent**[^muinjail] | any entry |
| `programs: { autoprune: true }` | works[^prunetime] | works[^prunetime] | works[^prunetime] | **absent, silent** — never prunes, never reports | fresh launch (switch)[^prunetime] |
| `yolo programs ls` / `remove` from inside the jail | works | works | works | **wrong answer, exits 0**[^muprograms] | any entry |

[^acro]: Apple Container honours read-only binds from version 1.1.0 (measured on macOS 26.5, Apple silicon). Check yours: `container --version`. Below that floor yolo refuses to bind a `mounts` entry rather than binding it writable, and prints one skip line per entry. `workspace_readonly` is the exception: those paths sit inside the writable workspace and cannot be skipped, so they arrive writable behind a loud warning that names every declared entry — but never the `yolo-jail.jsonc` lock. All of these lines are printed while the launch builds the container, so re-entering a running jail never shows them.

[^vm-share]: `podman` on macOS runs a Linux VM, and a bind source must be a path that VM shares — `$HOME` and `/private` by default. List yours: `podman machine inspect --format '{{range .Mounts}}{{.Source}} {{end}}'`; add one with `podman machine init -v <path>`. yolo does not probe this set, so a source outside it passes the host-side existence check and then fails inside the VM — an empty directory, or `statfs …: no such file or directory` at container start. For `cache_relocations` a target under `$HOME` is expected to work and one on `/Volumes` is expected to fail the launch outright; nobody has confirmed either on hardware.

[^mumounts]: Nothing binds `mounts` on `macos-user`, so no `/ctx` tree appears. The launch prints one line naming each entry it cannot deliver and suggests a container runtime. Copying an arbitrary host folder in is not planned.

[^mujsonc]: On the container backends, setting `workspace_readonly` at all also locks the workspace's own `yolo-jail.jsonc` against the agent — worth knowing before you protect an unrelated path. `macos-user` emits no such rule, so the jail's config file stays agent-writable there; an agent's edit shows up as a y/N diff at the next launch instead of being blocked.

[^leftover]: A "shadow" is a private directory mounted over `.venv`, `node_modules` and your declared paths so host and jail do not share build output. The mountpoint is created inside your live workspace, so an empty root-owned `.venv`/`node_modules` can be left behind after the jail exits.

[^achw]: Expected to work by construction — the same nested-bind shape Apple Container already uses for the cache — but never run on Apple silicon. Each entry also consumes one directory-sharing slot against an undocumented per-container limit.

[^mupsp]: There is no mount namespace here, so one path cannot show different contents inside and outside; the launch warns and the paths are shared. The warning names only *your* entries — the always-on `.venv` and `node_modules` are shared with no line at all. Pointing the sandbox at its own venv and module prefix would cover that default set, and is unbuilt.

[^aceph]: Apple Container always gets RAM-backed scratch for `/tmp`, `/var/tmp` and the container directories, whatever the key says, and nothing is printed. A build that writes large temp files can hit the memory ceiling this key exists to avoid. `YOLO_RUNTIME=podman` if you need disk-backed scratch.

[^mueph]: No container and no read-only root filesystem, so the sandbox writes the machine's real `/tmp` and `/var/folders` with no configuration. Only the RAM-backed (`tmpfs`) *choice* is unavailable, and asking for it is ignored without a word.

[^cachegap]: One yellow line per launch names the cache subdirectories that stayed put and points at `YOLO_RUNTIME=podman`. Neither cell is a backend limitation: Apple Container already nests a writable bind at exactly the depth a relocation needs, and on `macos-user` the policy that denies the target is one yolo generates itself. Both are unbuilt work. On the container backends the line is printed at launch assembly, so a re-entry never shows it.

[^dac]: These modes set file permission bits, not enforcement. In a container the agent is root, so `readonly`'s `0444` is a speed bump; on `macos-user` the sandbox runs as a separate account, so the bits actually bite.

[^acl]: `capture` is the one mode that writes into your workspace (it stores your local edits beside the composed file), so on `macos-user` the workspace needs the inherited `group:_yolojail allow` entry. Check with `ls -lde <workspace>`; repair with `yolo macos-fix-permissions`. Without it the launch dies as `mkdir …/.yolo/prism: permission denied`, naming neither ACLs nor the fix.

[^achostlayer]: Apple Container cannot bind a single file, so the host bytes are copied into the jail's home. That home is bound writable, so the agent can rewrite the host layer its own composed file is built from — where `podman` keeps that layer read-only. Nothing warns.

[^snap]: A root-owned copy taken at launch, not a live view. Every consumer reads its config at boot, so this is equivalent in practice. Symlinking the real file was measured and rejected: the sandbox runs as a different account, and a macOS home need not be readable by it.

[^acdir]: A directory source is bound read-only from Apple Container 1.1.0. Below that version, or when yolo cannot read `container --version`, the entry is skipped with one `Skipping host_files directory ~/<path> …` line naming the reason, rather than bound writable, and nothing appears at the destination. Like the `mounts` skip lines, it prints only while a fresh launch builds the container.

[^mudir]: One yellow line names each undelivered destination and points at `runtime: "container"`, which binds a directory source only from Apple Container 1.1.0[^acdir]. This is unbuilt work, not a limit of the backend: the same per-file copy that already delivers single-file sources needs to walk the tree, optionally bounded by size so the warning survives for genuinely huge trees.

[^mutier]: Composed files under `~/.config/…` land in a per-workspace directory and are fine. A destination at the home root (`~/.npmrc`, `~/.netrc`) lands in the sandbox account home that *every* workspace on the machine shares, so one workspace's launch overwrites another's — or, under `once`, finds the other's file already there and never seeds its own. Nothing warns.

[^hostside]: These four are host-CLI keys with no reader in any jail, so the backend is not the axis. Two behaviours to know: an unreadable or unparseable user config resolves `host_management` to `none` (yolo writes nothing) rather than to the default `assert`, so a malformed config looks like yolo going quiet; and `promotion_target` accepts only `local` or `pack:<name>`, silently falling back to `local` for anything else. ⚠ **The first of those is ruled out of existence, and is not fixed yet.** A ruling dated 2026-09-20 retires `host_management: "assert"` — two values remain, `none` and `own` — and makes **`none` the default**, which is what the parse-failure fallback already picks. So the divergence this footnote warns about ends by disappearing rather than by being patched. Until it is built the shipped default is still `assert` and the trap above is live; the decision lives in [`config-ownership-and-promotion.md`](../design/config-ownership-and-promotion.md#4-declaring-ownership--the-host_management-key).

[^wrappath]: `host_wrappers` puts small scripts ahead of the real agent binaries on your PATH, and `host_apply_on_launch` only ever fires through one of them. The macOS half of that placement is not handled: the shell-init line is appended to your shell rc, which runs *after* macOS's own `path_helper` for interactive shells — so a terminal session is fine while a GUI- or IDE-launched agent silently gets the real binary. Unmeasured on a Mac.

[^muinjail]: Nothing marks a `macos-user` session as being inside a jail, so `yolo host apply` and `yolo config promote` do not refuse there: they act on the shared sandbox account home while every message says "your real home", and `yolo config ls`/`diff` describe the host rather than the session you are in. Unbuilt: one launch variable, plus a sweep of everything that reads it.

[^prunetime]: A time-axis oddity worth knowing: the *switch* is frozen at the fresh launch, but the pruning itself re-runs on every re-entry — so turning autoprune off does not stop a running jail from pruning until you relaunch. Read from your user config only (a repo must not delete your binaries), and off unless explicitly on.

[^muprograms]: `yolo programs ls` inside a `macos-user` session prints "No staged packs here — run `yolo programs` in the jail" and exits 0. You *are* in the jail; the session just does not carry the variable that says so. Unbuilt, and the same omission as the previous note.

## Resources, devices, and networking

Two things to read before the table:

> [!WARNING]
> **"Unconfigured" is not "uncapped" on Apple Container.** With no `resources` block, the `container` backend still gets a memory and CPU cap — **yolo's own arithmetic**, roughly half your RAM and half your cores. The startup banner does not mention it (it prints only what you wrote); the agent's briefing does. On both podman backends, unset really does mean unlimited.

> [!IMPORTANT]
> **Every key in this table is frozen at the fresh launch.** All of them ride the container command line, so re-entering an already-running jail returns before the config-change prompt and before the command line is rebuilt. An edited value does nothing, silently — and worse, the agent's briefing *is* re-rendered from your edited file, so the agent is told a cap or a port that is not in force. `yolo config drift` is the detector; nothing points you at it. Exit the jail and relaunch.

| Key | `podman` / Linux | `podman` / macOS | `container` / macOS | `macos-user` / macOS | Takes effect |
|---|---|---|---|---|---|
| `resources.memory` | works — kernel-enforced [^cgroups] | works — enforced in the VM, clipped to it [^vm-limits] | works — **but a cap you never wrote** [^acdefault] | absent, warns — not built yet [^mures] | fresh launch [^muentry] |
| `resources.cpus` | works — kernel-enforced [^cgroups] | works — clipped to the VM's vCPUs, silently [^vm-limits] | works — half your cores unless set [^acdefault] | absent, warns — not built yet [^mures] | fresh launch [^muentry] |
| `resources.pids_limit` | works — always capped, default applied | works — always capped | absent, **silent** — no surface mentions it [^acpids] | absent, warns — not built yet [^mures] | fresh launch [^muentry] |
| `devices` (raw path, `usb:`, `cgroup_rule`) | works — all three forms [^lsusb] | absent, warns — refused by OS, never probed [^macdev] | absent, warns [^acdev] | absent, warns — device control is blocked [^mudev] | fresh launch [^muentry] |
| `gpu` — `vendor: nvidia` | works — CDI device + driver env [^nvidia] | absent, warns [^gpumac] | absent, warns [^gpumac] | absent, warns — GPU already reachable [^mugpu] | fresh launch [^muentry] |
| `gpu` — `vendor: amd` | works — `mode: devices`; `mode: cdi` can fail hard [^amdcdi] | absent, warns [^gpumac] | absent, warns [^gpumac] | absent, warns [^mugpu] | fresh launch [^muentry] |
| `kvm` | works — needs host group membership [^kvmhost] | absent, warns — not built yet [^kvmmac] | absent, warns [^acdev] | absent, warns — no Linux kernel to ask | fresh launch [^muentry] |
| `network.mode: "bridge"` (default) | works — own network namespace | works — namespace inside the VM [^vm-limits] | works — own network per container | absent, **silent** — no isolation at all [^mubridge] | fresh launch |
| `network.mode: "host"` | works — and drops both port keys [^hostdrop] | **applies to the VM, not the Mac — silent** [^hostmac] | absent, warns — runs bridged; port keys still work [^achost] | works — the only mode it has | fresh launch |
| `network.ports` (HOST:JAIL) | works — passed to podman [^dnat] | works for a `0.0.0.0` listener — **measured** [^vm-limits] [^dnat] | **accepted and inert — measured** [^acports] | absent, warns — every bound port is already open [^muports] | fresh launch |
| `network.forward_host_ports` (JAIL:HOST) | works — needs `socat` on the host [^socat] | works — TCP to the VM gateway, unmeasured [^vm-limits] | **breaks the launch — measured** [^acfwd] | same-port entries already true; a remap is not built [^mufwd] | fresh launch |

Terms: **rootless podman** runs as your own user with no root daemon; **Podman Machine** is the Linux VM podman uses on macOS; **CDI** is the Container Device Interface, a host-side YAML/JSON file describing a GPU; a **remap** is a port entry whose two numbers differ (`5432:3306`).

The `--network` CLI flag does **not** override the config: a `network.mode` in `yolo-jail.jsonc` wins over `yolo --network …`, with no warning. And the validator's warning that the port keys are "ignored when `network.mode` is `'host'`" — the one network message that does fire on any entry — is **false on `container`** (which runs bridged and keeps the ports) and misleading on `macos-user` (both keys are ignored whatever the mode).

**Can the jail reach a service on your host's `127.0.0.1` with no port setting?** `podman` / Linux (rootless): yes, at `host.containers.internal`; a rootful podman warns that it cannot. `podman` / macOS: yes, at `host.containers.internal`. Apple Container: no, and `forward_host_ports` stops the launch there [^acfwd]. `macos-user`: yes, at `localhost`.

[^cgroups]: Needs cgroup v2 with delegation. On a **rootless** podman on a cgroup-v1 host, podman ignores memory/CPU/pids limits and yolo does not pre-flight it: the flag is emitted and does nothing. Check yours: `podman info --format '{{.Host.CgroupVersion}} rootless={{.Host.Security.Rootless}}'`.
[^vm-limits]: On macOS, podman runs inside a Linux VM, so a limit is enforced against the VM and a network namespace is created inside it. Asking for more memory or more CPUs than the machine has is accepted and silently clipped — for memory the only signal is a hint printed *after* an out-of-memory kill; for CPUs there is none. Check the machine's size with `podman machine inspect`, and resize with `podman machine set --memory … --cpus …`.
[^acdefault]: When the key is unset, yolo probes host RAM and core count and emits half of each (memory floored at 4 GB, CPUs at 2). **A fractional `resources.cpus` breaks the launch here, measured 2026-09-16:** yolo's validator permits any positive number, and `container run --cpus 1.5` dies as `Error: The value '1.5' is invalid for '--cpus <cpus>'` followed by a usage block — a backend's argument parser shown to a user for a value yolo called valid. Integers are fine (`--cpus 2` runs). The same config works on podman.
[^mures]: `macos-user` runs the agent as an ordinary sandboxed process, so there is no cgroup to write; the launch prints one yellow line naming every `resources` key you set, and the agent briefing deliberately claims nothing. A runaway build can take the whole Mac down. This is **not built rather than impossible**: a sampled host-side watchdog on the session's process tree could contain and report a breach for memory and process count, and `GOMAXPROCS`/`-j`-style concurrency limits would honor the `cpus` number cooperatively. None of that exists today.
[^muentry]: `macos-user` is the one setup with no re-entry: every invocation starts a fresh sandbox, so it re-reads config every time. The frozen-at-launch rule applies to the three container setups.
[^acpids]: No process cap is passed and no banner, briefing, or warning mentions it — a fork bomb in the jail is unbounded. Also **not built rather than impossible**, and the mechanism is now measured: the container's cgroup filesystem IS writable on `container` 1.1.0 (`cgroup.controllers` reads `cpuset cpu io memory hugetlb pids`, and `+pids` into `cgroup.subtree_control` succeeds), so the jail's own boot can write the cap the CLI cannot pass.
[^lsusb]: `usb: "vendor:product"` needs `lsusb` on the host PATH or it degrades to a warning, and a replugged device changes its bus path — a stale raw path becomes a skipped line, never an error. Whether `cgroup_rule` is actually honored under cgroup v2 is unverified.
[^macdev]: The refusal is by host OS, before any check of *where* the device lives — yet yolo itself passes a VM-internal device path on the same launch, so a path that exists inside the Linux VM (a tun device, a loop device) is refused for a reason that does not apply. USB additionally needs macOS 15 or later (`sw_vers -productVersion`) plus Podman Machine support.
[^acdev]: Apple Container passes no devices at all — **verified against hardware** 2026-09-16: `container run --help` on 1.1.0 has no `--device`, no CDI flag, and no device selector of any kind.
[^mudev]: There is nothing to pass through: the sandboxed process opens device files under ordinary macOS file permissions. But the sandbox refuses device control calls (`ioctl`) on everything except terminals, so a serial port or other hardware cannot actually be driven, and no setting reopens it.
[^mugpu]: Not read on `macos-user`, and the launch names the key. The agent is an ordinary Mac program, so Metal (Apple's GPU API) should be reachable as it is for any program you run; there is no NVIDIA or AMD card to pass on a Mac.
[^nvidia]: Needs the NVIDIA Container Toolkit, `runc` as the runtime (CDI fails under crun), and a CDI spec whose driver version matches the host driver. ⚠ The launch probe accepts only a `.yaml` spec: a host whose spec is `nvidia.json` is silently downgraded to no GPU. Check with `nvidia-smi -L` and `ls /etc/cdi /var/run/cdi`. A failed probe always warns and starts without the GPU, never refuses.
[^gpumac]: Apple silicon has no NVIDIA or ROCm hardware and neither macOS VM does PCIe passthrough, so CUDA/ROCm in a Linux guest is out. But the cell is **narrower than "impossible"**: a podman machine on the libkrun provider exposes a virtual GPU to the container, which gives Vulkan *compute* translated to Metal — a worse mechanism for the same goal ("the jail gets GPU compute"). yolo cannot express it: config accepts only the `nvidia` and `amd` vendors, and device passthrough is refused by host OS before any path is examined.
[^amdcdi]: With `mode: devices` the raw GPU device nodes are passed and the pre-flight probes for them. With `mode: cdi` the pre-flight does **not** check for an AMD CDI spec, so on a host with the driver but no spec the launch dies on a raw runtime error instead of yolo's usual warn-and-start-without-GPU. Check with `ls /etc/cdi/amd.json /var/run/cdi/amd.json`.
[^kvmhost]: Needs CPU virtualization extensions, the `kvm` module, and (rootless) your user in the `kvm` group — `yolo check` covers all three.
[^kvmmac]: The warning fires by host OS, and the device check it skips would have looked on the Mac rather than inside the Linux VM where the device would live. Reaching it needs Apple's nested virtualization (macOS 15+, M3 or later) plus Podman Machine support, and then a probe of the VM rather than the Mac.
[^mubridge]: A written `"mode": "bridge"` is not read: the sandboxed process shares the launcher's network stack and every port it binds is on the Mac's real interfaces. The agent's briefing says so; the human who wrote the key is told nothing. A warning on an explicit key is the small missing piece — the isolation itself the sandbox cannot provide.
[^hostdrop]: By design: under host networking yolo stops requesting host-loopback forwarding and drops **both** port keys, because there is nothing left to map. It also tells the jail that the namespace is shared, which makes an unreachable yolo service refuse the launch rather than warn.
[^hostmac]: The flag is applied — to the VM's network namespace. Two silent consequences: the agent is told "localhost resolves directly to the host", which is false (it is the VM's loopback); and the jail is told the launcher's namespace is shared, which **escalates** — with a loopback service enabled, the launch can be refused for a boundary that was never crossed.
[^achost]: Apple Container accepts no network selector, so the key cannot be honored as written; the warning names the key and the consequence. It was thought to withhold nothing because the port keys still worked here — **measured 2026-09-16, neither does** [^acports] [^acfwd]. The *goal* remains reachable and unbuilt, and the raw material is the one transport that does cross this boundary: a published UNIX socket, which carries data both ways in the host→container direction.
[^dnat]: A jail service bound to `127.0.0.1` (rather than `0.0.0.0`) is *meant* to stay publishable: yolo installs an address translation at boot for each published port. **On podman/macOS it does not work, and that is now measured** — with the rule installed, `route_localnet=1` and the listener confirmed, the Mac's dial to the published port connects and receives nothing, while an identical `0.0.0.0` listener answers. So **bind `0.0.0.0` in the jail**, and the two documentation surfaces that say a `127.0.0.1` listener is not publishable (including the agent briefing) are RIGHT rather than stale. The mechanism is that rootless port forwarding hands the connection over inside the network namespace, which never traverses the `PREROUTING` chain the fixup writes — so expect the same on **rootless Linux**, where it remains unmeasured. Your deciding host fact: `podman info --format '{{.Host.Security.Rootless}} {{.Host.RootlessNetworkCmd}}'`. On `container` none of this machinery is emitted at all, and published ports do not work there for any bind address [^acports].
[^socat]: `socat` must be installed **on the host**; absent, you get one warning and no forwarding. This is the one network key with an end-to-end test, and that test runs only on Linux.
[^acver]: Apple Container's version is the whole axis for this backend — `container --version`. One documentation claim about the mechanism ("no socat") is wrong: `socat` runs on both sides. The port keys were untested here until 2026-09-16; both are now measured, and both are worse than the corpus believed [^acports] [^acfwd].
[^acports]: **Measured on `container` 1.1.0 / macOS 25.5:** the mapping is recorded — `container inspect` shows `{"hostAddress": "0.0.0.0", "hostPort": …}` — and carries nothing. Dialling the Mac's `127.0.0.1:<hostPort>` connects and returns no data (the jail-side listener sees the connection arrive and reset), while dialling the container's own vmnet IP on the container port works normally. This is the same shape as the container→host outage: on this backend host↔container TCP establishes in both directions and carries data in neither. Nothing warns; `-p` is emitted as if it worked.
[^acfwd]: **Measured on `container` 1.1.0 — this one fails the launch.** yolo starts host-side `socat` *before* creating the container, and Apple Container then refuses the flag naming that socket: `Error: host socket <path> already exists and may be in use`. Even with no `socat` installed, the direction is inverted — AC's `--publish-socket host_path:container_path` creates the host socket and forwards a *host* connection inward to a container-side listener, which is the opposite of what this key needs. A published unix socket is nonetheless the one transport measured to cross this boundary at all, so it is the raw material for a fix rather than a dead end.
[^muports]: There is nothing to publish and nothing to confine: a port the sandboxed agent binds is on the Mac's real interfaces whether you list it or not. A launch prints one line per declared key saying so. A **remap** (differing numbers) is named as undeliverable — and that half is unbuilt rather than impossible: a small host-side relay would deliver it, at the cost of leaving the inner port exposed too.
[^mufwd]: A same-port entry (`5432:5432`) is already satisfied — the sandbox is on the Mac's stack. A remap (`5432:3306`) is warned and not delivered; a loopback relay in the launcher would deliver it, which is why this is a gap rather than a limit.

## Packages, tools, and agent configuration

**Nothing is active by default.** An empty config gives you a jail with *no coding agent* — a coding agent arrives only because a pack installs one, and the launch says so when `packs` is empty. Your effective pack set is also larger than what you typed: a pack may pull in others through its own dependencies, so the launch prints the resolved set rather than your list.

**Config scope matters before anything else.** Keys marked † below are read from your **user** config only (`~/.config/yolo-jail/config.jsonc` and its includes). A workspace `yolo-jail.jsonc` cannot set them at all — spelling one there is a fatal pre-flight error that names the file to move it to. Every other key here is settable at either scope, workspace winning.

| Key / capability | `podman` / Linux | `podman` / macOS | `container` / macOS | `macos-user` / macOS | Takes effect |
|---|---|---|---|---|---|
| `packages` (nix packages on PATH) | works — baked into the image | works — yolo starts a Linux builder when needed[^builder] | works — yolo starts a Linux builder when needed[^builder] | works — native darwin build | fresh launch |
| `packages` per-entry `platforms` | works | works — but `"darwin"` means *absent*[^plat] | works — same inversion[^plat] | works — this is what it is for | fresh launch |
| `packages` entry that cannot build | refuses — nix error at launch | refuses — nix error at launch[^builder] | refuses — nix error at launch[^builder] | refuses — names your real target | fresh launch |
| One image per machine (`YOLO_STORE_PACKAGES=1`) | works — packages from the host store | absent, warns — not built yet[^storedel] | absent, warns — not built yet[^storedel] | n/a — says it is ignored: `packages:` already come from the nix store | fresh launch |
| `mise_tools` (runtime pins) | works — tool store on the host | works — store inside the VM[^vm-store] | works — store inside the VM[^vm-store] | works — one store for the whole machine | fresh launch |
| `mcp_presets` | works | works | works | absent, warns — broken entry still written[^presetmac] | fresh launch |
| `mcp_servers` | works | works | works | works — `command` must exist on your Mac | fresh launch |
| `mcp_servers.requires_env` | works | works | works | **silently** drops gated servers[^reqenv] | fresh launch |
| `lsp_servers` | works — config only, you bring the binary[^lsp] | works — config only, you bring the binary[^lsp] | works — config only, you bring the binary[^lsp] | works — config only, you bring the binary[^lsp] | fresh launch |
| `security.blocked_tools` | works — blocker shims on PATH | works | works | works — but blocks the Mac's BSD tool[^bsd] | fresh launch |
| `packs` † | works | works — workspace must be VM-shared[^vm-store] | works — staged copy, never re-read | works — some surfaces inert, said aloud[^packsmac] | fresh launch[^packspartial] |
| `providers` | works | works | works — lost on re-entry[^reentry] | works | any entry[^reentry] |
| `profiles` † | works | works | works — lost on re-entry[^reentry] | half-arrives, **silent**[^profmac] | any entry[^reentry] |
| `use_profiles` † / `-p <name>` | works — refuses a `-p` it cannot honor | works | **silently** ineffective on re-entry[^reentry] | selection arrives, body does not[^profmac] | any entry[^reentry] |
| `agent_updates` † | works | works | works | works | fresh launch |
| `env_sources` (dotenv files) | works | works | **silently** lost on re-entry[^reentry] | works — per-session, not editable in-jail | any entry[^reentry] |
| `nix build` usable inside the jail | works — host daemon, store read-only[^gcroot] | **absent, silent** — possible, not planned for now[^nixmac] | **absent, silent** — possible, not planned for now[^nixmac] | works — the host's own `nix`, through its daemon[^munix] | fresh launch |
| GNU behaviour of `sed`/`find`/`grep`/`tar` | works — GNU userland baked | works | works | BSD tools — GNU flags fail[^bsd] | fresh launch |
| Build toolchain (`cc`, `make`, `strace`) | works — baked | works | works | works only with Xcode CLT[^clt] | fresh launch |
| Browser for the chrome-devtools MCP | works — chromium baked | works | works — measured | absent — no browser wired | fresh launch |

Two things cut across the whole table. First, **`macos-user` has no re-entry**: every `yolo` invocation is a fresh sandbox, so every row above takes effect on your next command there, with no restart — the "fresh launch" answers cost you nothing on that column. Second, the container backends freeze most of this on the container command line at the **fresh** launch; re-entering a running jail returns before the config-change gate, so an edit you just made is not in the session you just joined. `yolo check` before you restart; a restart is what delivers.

**One gap to know about, not a limitation:** `providers` is *not* user-scope-contained the way `packs`, `profiles`, `use_profiles` and `agent_updates` are. A repo-committed, agent-editable workspace config can rewrite a provider's base URL or endpoints — including one your own user-scope profile selects — and nothing warns. Treat a workspace config you did not write as able to redirect the agent's API traffic. Closing this is unbuilt work, not a permanent property.

[^builder]: A `packages:` entry forces a **Linux** image build. When the nix binary cache does not have a package, yolo starts a temporary Linux builder container for you; that needs the runtime running and your user trusted by the nix daemon. A build that fails stops the launch with nix's own error.

[^plat]: On a Mac running a *container* backend the jail is Linux, so `{"platforms": ["darwin"]}` means the package is **not** in your jail and `{"platforms": ["linux"]}` means it is. Nothing at launch names the entries it dropped.

[^storedel]: Delivering `packages:` from the host nix store — one image per machine instead of one per distinct package list — is a Linux-podman-only fast path today. On macOS the dial prints an "ignored" line and the launch bakes as normal, which costs a rebuild per distinct list. Not a ruled-out design; simply not built for these setups yet.

[^vm-store]: The podman machine has one fixed share set, chosen at `podman machine init`, and Apple Container has its own VM. Consequences: the mise tool store lives inside the VM (invisible from the Mac's filesystem, and lost to a `podman machine reset`), and a workspace or yolo state directory outside your home may fail to bind at all. See yours with `podman machine inspect --format '{{.Mounts}}'`.

[^presetmac]: On `macos-user` a yellow launch line tells you presets are not delivered — but the preset's entry is still written into every agent's MCP config, so it fails at first use on a path yolo chose not to create.

[^reqenv]: A server gated on `requires_env` is removed from every agent's config on `macos-user`, even when the variable *will* be in the agent's environment.

[^lsp]: yolo installs no language server on any setup: each declared server reaches the agents that read one (Claude through a generated plugin, Copilot natively), and its `command` must already be on `PATH` — bring it with `mise_tools`, a pack program or an absolute path. Until 2026-09-25 three names (`python`, `typescript`, `go`) had install recipes; those are deleted, and nothing uninstalls what they installed earlier. On the container backends the boot catalog reports that leftover as an orphan for `yolo programs remove`. On `macos-user` no boot catalog runs and `yolo programs` refuses inside the sandbox, so remove it by hand from the workspace's `.yolo/home/npm-global` and `.yolo/home/go/bin` ([`mcp-configuration.md`](mcp-configuration.md#binaries-are-the-users)).

[^bsd]: `macos-user` runs against your Mac's own userland, so `sed -i` eats the next argument, `find -printf` and `tar --wildcards` are unknown, and `grep -P` and `ls --color` error out — scripts that pass on the container backends fail here. `security.blocked_tools` also measures the *sandbox* PATH, so a block replaces the BSD tool. Homebrew's GNU builds (`brew --prefix coreutils`) are not on the sandbox PATH under their plain names.

[^packsmac]: On `macos-user` a pack's skills and briefing are writable by the agent and are overwritten on the next launch, and pack-shipped MCP presets are not delivered. A pack-shipped loophole is **half** delivered, which is the precise version of what this note used to call "inert": its HOST daemon starts and its endpoint is delivered (with a per-file ACL grant for the sandbox account), while its `jail_daemon` — the in-jail half — runs for nothing at all, because this backend has no in-jail supervisor. So an *intercepting* loophole does nothing useful even with its daemon up: `claude-oauth-broker`'s TLS terminator is its jail half. The launch says all of it rather than failing quietly: one disclosure line per host daemon it starts, and one `Declined:` line per jail daemon it will not.

[^packspartial]: On podman, adding a pack and *re-entering* a running jail is the worst of both: the pack's config surfaces and hooks render, while its skills, briefing, files and host-file grants do not, and its loopholes never start. Half-arrived, with nothing said. Restart for a whole pack.

[^reentry]: Apple Container receives the provider/profile/`env_sources` channel as a **copied** file, made only on a fresh launch. Re-entering a running jail prints the delivery line, exits 0, and runs the previous launch's providers, profiles and dotenv values. On podman the same channel is a live file, so it does reach the next entry.

[^gcroot]: The image turns on `nix-command` and `flakes` in `/etc/nix/nix.conf`, so plain `nix build` and `nix shell` work with no flags. It needs a multi-user nix on the host, the kind with a nix daemon. An in-jail `nix build`'s result gets no durable garbage-collection root, so a host `nix-collect-garbage` can delete a store path a running jail is executing from, with no warning in either place.

[^nixmac]: In-jail nix on both Mac container backends is **possible, but not planned for now** (ruled 2026-09-24; the measured route — two podman volumes for `/nix/store` and `/nix/var` plus three `nix.conf` lines — is recorded in [G21](../plans/setup-support-gaps.md#2-ranked-gap-backlog)). Use `packages:` and a restart, mise, or the `macos-user` backend instead. Reaching the host nix daemon from a jail on a Mac needs a store the jail can see holding *Linux* paths, and (on podman) a machine initialised with `-v /nix:/nix` — which cannot be added to an existing machine. Check with `command -v nix` and `podman machine inspect | grep -i /nix`. Do not force the store-view dial on: it replaces the view the jail's own binaries live in and the jail will not boot. By default the launch says nothing; the one line you may see prints only when `YOLO_NIX_HOST_DAEMON` is set without `YOLO_NIX_HOST_STORE_LINUX`. Apple Container has no opt-in: it never mounts the host's nix, whatever is set.

[^munix]: Each launch puts the host's own `nix` client — the one it just used to build the sandbox's tools — on the sandbox PATH, and sets `NIX_REMOTE=daemon` and a `NIX_CONFIG` that turns on `nix-command` and `flakes`. A `NIX_REMOTE` you set replaces yolo's. A `NIX_CONFIG` you set is kept, and yolo's `extra-experimental-features` line is added after it, unless yours already sets `experimental-features` or `extra-experimental-features`. It needs a multi-user nix; with a single-user one (no daemon socket) the sandbox gets no `nix`, and the launch prints `nix is not available inside the sandbox:` with the reason — so this row is never silent. Built on a 2026-09-16 hardware probe that ran `nix build` inside the sandbox profile; the delivery itself passed on the `macos-user` CI job on 2026-09-24 ([run 36050645052](https://github.com/mschulkind-oss/yolo-jail/actions/runs/36050645052)). Details: [nix inside the sandbox](macos-user-nix-and-features.md#nix-inside-the-sandbox).

[^clt]: Without Xcode Command Line Tools there is no `cc` or `make` in the `macos-user` sandbox and yolo does not say so; with them installed, the Mac's own toolchain is what you get. Check with `xcode-select -p`. Linux-only tools such as `strace` are absent either way.

## What a running jail picks up when you run `yolo` again

Re-running `yolo` in a workspace whose jail is still running does **not** start a new jail — it *re-enters* the one you have (the banner says `Attaching to existing jail`). Settings passed when the container was created stay as they were. Skills, briefings and pack files are rebuilt on every entry, so you see the boot run again, yet some edits still wait for a restart. This table says which.

| What you changed | `podman` re-entry (Linux, macOS) | `container` re-entry | `macos-user` |
|---|---|---|---|
| Skills (built-in, pack, your own) | `works` | `works` | `works differently`[^macuserre] |
| Briefings (`AGENTS.md`, `CLAUDE.md`), `agents_md_extra`, a filed handoff | `works` | **absent, silent** | `works differently`[^macuserre] |
| Adding or dropping a pack | `partly`[^packpartly] | **absent, silent**[^acpack] | `works` |
| Pack launch flags (`--yolo`, `--dangerously-skip-permissions`) | `works` | `works` | `works` |
| `-p <profile>` / a rotated key in `env_sources` | `works` — live env file | **absent, silent** — and prints as if delivered | `works differently` |
| `mcp_servers`, `lsp_servers`, `mise_tools`, `blocked_tools` | **absent, silent**[^envhalf] | **absent, silent** | `works` |
| `resources`, `network`, `ports`, `mounts`, `devices`, `gpu`, `packages`, `host_files` | **absent, silent** | **absent, silent** | `works`[^macuserkeys] |
| The config-change diff prompt (your y/N review) | **absent, silent** | **absent, silent** | `works` — every invocation shows the diff |
| The frozen config snapshot `yolo config drift` compares against | **absent, silent**[^drift] | **absent, silent** | `n/a` — never written, drift says "cannot determine" |
| The jail's own `yolo` version | `absent, warns` — one dim line naming `yolo stop` | `absent, warns` | `n/a` — re-staged every launch |

**On `macos-user` every `yolo` starts a fresh sandbox from your current config**, so every setting it reads takes effect on the next launch. What differs there is which settings it reads at all.

**To get a fresh jail:** `yolo stop`, then `yolo -- <cmd>`. On Apple Container, `yolo stop` cannot see the jail yet: it prints `No jail running for this workspace` while the jail is running. Find the jail's name with `container ls` and stop it with `container stop <name>`, including whenever yolo suggests `yolo stop` there.

[^macuserre]: Rebuilt on every launch and copied into the sandbox home, so it works — but the copy is **writable**, so the agent can edit its own skills and briefing, and the next launch overwrites those edits. The agent's briefing tells it so.
[^packpartly]: A newly added pack's config files, skills, briefing, hooks and launchers arrive on a re-entry; its mounts and host services do not. A pack that needs to write somewhere new in the home can make the re-entry fail outright. Dropping a pack works cleanly. Restart the jail to add one.
[^acpack]: Apple Container gets a copy of the packs at launch, so the agent cannot rewrite them, and that copy is not refreshed on a re-entry. A running jail keeps the packs it started with: their files, skills and hooks.
[^envhalf]: These cross as environment variables on the container command line. An added MCP server does not appear in the agent's list until you stop and relaunch, even though the boot visibly regenerated the MCP config.
[^drift]: After a re-entry, in-jail config readers still see the config the jail was *launched* with, and `yolo config drift` compares against a baseline that may be several edits old — so it reports drift for edits you thought you had applied.
[^macuserkeys]: For the keys this setup reads. Resource limits are not enforced here, and `mounts` binds nothing.

## What a pack can contribute, per setup

Every contribution kind is delivered on all four setups — config files and their overlays, autonomy postures, hooks, blocked-tool shims, skills trees, workspace and machine-scope state dirs, read-only host-file grants, providers, `requires` assertions, briefings, and `program` launchers[^capture] — **except these**:

| Contribution | podman/Linux | podman/macOS | container/macOS | macos-user/macOS |
|---|---|---|---|---|
| `env` — static vars | works | works | works, **silent on re-entry**[^acfreeze] | works — carried in the launch env |
| `files` — a tree in the agent's home | works | works | works — writable copy | **absent, silent**[^files] |
| `mount` — host dir read-only at `/ctx/<into>` | works | works | works — needs Apple Container 1.1.0+[^acver-mount] | **absent** — and the banner says otherwise[^mountmu] |
| `service` — an in-jail daemon (the wire bridge) | works | works | works | absent — named at launch, breaks a shipped default[^svc] |
| `profile` — a named `-p` selection | works | works | works, **silent on re-entry**[^acfreeze] | partly — the vars land, the config surfaces do not[^profmu] |
| `loophole` — a host service | works | works — the jail-to-Mac connection is tested[^machop] | one starts, none usable[^theone] | all start, none fully usable[^theone] |

On **podman** and **macos-user**, content contributions are re-rendered on any entry, so a host-side edit reaches a jail you re-enter. On **Apple Container** they are a snapshot of the last fresh launch: an edited pack, a changed setting, or a fresh `.yolo/handover.md` is composed, announced as delivered, and does not arrive until you restart the jail.[^acfreeze]

[^capture]: `program` launchers (a name on PATH plus a lazy installer that keeps the tool current) are delivered on all four. What `macos-user`, and Apple Container before 1.1.0, lack is the install-capture store that pre-seeds those installs, so a first use downloads the vendor installer instead — slower, same result.
[^acfreeze]: Apple Container renders pack surfaces from a per-launch copy of the pack tree rather than a live read-only bind, and the copy is only refreshed by a fresh launch. Consequence for `env` and `profile`: `yolo -p <name> -- <agent>` against a running jail prints the selection it made, and the jail keeps the previous one. Restart the jail after any config or pack edit on this backend.
[^files]: `packs: ["pi"]` on macos-user silently omits the extension file the pack ships into the agent's home, so the OpenAI broker runs and the code that dials it never arrives. A gap not yet built, not a limit of the backend.
[^acver-mount]: Read your own value with `container --version`. From 1.1.0 read-only binds are honored and this works; below it, or if the version cannot be read, the mount is skipped with a yellow line and `/ctx/<into>` does not exist. Closing the below-floor case is unbuilt work, not a permanent limit.
[^mountmu]: On macos-user the tree is not delivered at all, and the launch's host-access banner still discloses the read as if it were — the one place here where the launch is actively misleading. Unbuilt, and known to be buildable.
[^svc]: The wire bridge is an in-jail daemon that some providers route through; the `cerebras` pack pulls it in automatically when `claude` or `copilot` is selected. On macos-user nothing runs it — the launch prints a `Declined:` line naming it — so a default `["claude", "cerebras"]` composition points the agent at a local address with no listener. Also note on every setup: the daemon set is fixed at the fresh launch, so a newly selected service pack needs a restart.
[^profmac]: The provider and profile variables reach the sandbox, but the agent config files that depend on the selected profile are written as if no profile were chosen. Not planned yet.
[^profmu]: On macos-user the provider and profile variables reach the sandbox environment, but the resolved profile table does not reach the config-rendering half, so surfaces that depend on the selected profile render without it.
[^machop]: The jail reaches your Mac's services through the Podman Machine VM at `host.containers.internal`, and the macOS nightly dials a host service from inside a real jail. The services themselves have not been run end to end on a Mac. If one cannot be reached, the launch warns but still starts; you meet it as `yolo-serial` or a login failing at runtime.
[^theone]: See [the loophole table](../guides/USER_GUIDE.md#the-loopholes-host-services-a-jail-can-use). On Apple Container only the OpenAI login service is started, and the jail cannot reach it; every other loophole prints one yellow line per launch naming the backend and the reason. On `macos-user` every host service starts and the launch discloses each one, but no in-jail half runs (one `Declined:` line each), so only the OpenAI login service is usable, and only partly.
