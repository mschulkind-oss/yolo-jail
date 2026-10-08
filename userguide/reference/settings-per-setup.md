---
status: current
verified: 2026-09-25
verified_commit: 71acddac
covers:
  - internal/config/config.go
  - internal/config/validate.go
  - internal/config/inherit.go
  - internal/config/hostmanagement.go
  - internal/cli/run/assemble.go
  - internal/cli/run/assemble_parts.go
  - internal/cli/run/loopholeinert.go
  - internal/cli/run/backendlimits.go
  - internal/macosuser/runplan.go
  - internal/setupcensus/configkeys.go
  - internal/setupcensus/kinds.go
tags: [config, backends, setups, podman, apple-container, macos-user]
summary: "Every config key and every pack contribution kind, and what each of the four setups (a backend paired with a host OS) does with it. Also which edits a running jail picks up when you run yolo again and which need a fresh launch."
---

# Settings per setup

The short answers for each way to run yolo come first. The tables below then describe what each
configuration key and pack contribution does on those setups.

## What works in each setup

yolo can run a jail four ways, depending on your computer and what you have installed. They do not all
support the same things; find your column below.

| Column | `runtime` value | Host OS | What it is |
|---|---|---|---|
| `podman` / Linux | `podman` | Linux | Containers on your own Linux machine. Everything in this guide works here. |
| `podman` / macOS | `podman` | macOS | Containers inside a Linux virtual machine that Podman runs on your Mac (the Podman Machine). |
| `container` / macOS | `container` | macOS | [Apple Container](https://github.com/apple/container): each jail runs in its own small Linux virtual machine. **The Mac default.** |
| `macos-user` / macOS | `macos-user` | macOS | An ordinary Mac program running inside Apple's built-in sandbox. No container and no virtual machine, so your project stays at its real path and host folders cannot be added. |

**Words used below.** A **pack** is an add-on you list under `packs` in your user config; most install
an agent, such as `claude`. Some packs bring a **host service**: a small program yolo runs on your real
machine that the jail may talk to, such as the login services that let several jails share one login.
yolo calls a host service a **loophole**, because it is a deliberate hole in the jail's wall. A
**restart** means `yolo stop`, then `yolo`. Running `yolo` while the project's jail is already running
**joins** that jail instead, and a joined jail keeps the settings it started with.

### What you can do on each setup

The short answers, in the order you are likely to need them. **Yes** means it works. **Should work**
means it is expected to work, but nobody has tried it on that setup yet. **No** means it does not, and
the cell says whether a fix is coming: *planned* means the maintainers have scheduled it; *not planned
yet* means they have not. The subsections and tables further down have the detail.

| You want to… | `podman` / Linux | `podman` / macOS | `container` / macOS | `macos-user` / macOS |
|---|---|---|---|---|
| **Start a jail** | Yes. The first launch builds the jail image and takes a few minutes; later launches take seconds. | Yes, once the Podman Machine VM is running and shares the folders yolo needs ([setup](../getting-started.md#macos-podman)). The first launch is slow.[^cap-first-mac] | Yes, and it is the Mac default.[^cap-ac-stop] | Yes, after a one-time `yolo macos-setup`. Projects must be outside every home folder; `/Users/Shared/yolo` is set up for you.[^cap-mu-start] |
| **Open a second session** | Yes. `yolo` in the same project joins the running jail; another project gets its own jail. | Yes, the same. | Yes, the same, but jails for two different projects can log each other out of `claude`.[^refresh-ac] | Yes. Each `yolo` gets its own sandbox, and a second one in the same project shares the host services the first started, which stay up until the last of them ends; two `claude` sessions at once can still log each other out.[^mu-keeper] A launch in another project stops while a session runs, and names the project it belongs to.[^shared] |
| **Install an agent** | Yes. Add its pack to your user config; it installs the first time you type its name and keeps itself up to date. | Yes. `omp` is not available on Apple silicon.[^cap-omp] | Yes, except `omp`, which is not available on Apple silicon, the only kind of Mac Apple Container runs on.[^cap-omp] | Yes for `claude` and `codex`. `agy`, `copilot`, `opencode`, `pi` and (on Apple silicon only) `omp` should work. |
| **Run an agent without permission prompts** | Yes, automatically, for every agent except `omp`.[^cap-yolo-mode] | Yes, the same. | Yes, the same. | Yes, the same. |
| **Log in to an agent** | Yes. Log in once inside a jail; `claude`, `agy`, `codex`, `pi`, and `opencode` on the `codex` profile, then work in every project, while `copilot`, `omp` and opencode's other logins ask once per project.[^cap-logins] | Yes for `claude`, `agy`, `copilot`, `opencode` and `omp`.[^cap-logins] The shared `codex`, `pi` and `opencode` subscription login should work.[^lh-mac] | Yes for `claude`, `agy`, `copilot` and `opencode`.[^cap-logins] **No for `codex`, `pi` and `opencode` subscription logins**; not planned yet.[^cap-ac-openai] | Yes, except that `codex`'s shared login has not been tried on a real Mac and can be lost in a long session (fix planned), and `pi`'s has not been tried on a real Mac either.[^login-mu] |
| **Use API keys and other providers** | Yes. Put keys in a dotenv file listed under `env_sources`; `yolo -p <profile>` picks the provider and model. A changed key reaches your next `yolo`.[^cap-models] | Yes, the same. | Yes. A changed key or `-p` reaches a running jail when you run `yolo` in it again (checked by tests, not yet on a Mac).[^reentry] | Yes, read fresh at every launch.[^cap-mu-keys] |
| **Give a shell or script a provider's key** | Yes. `yolo --with-credentials zai -- bash` starts the jail holding zai's key from `env_sources`, keys only, for every process and every later session in it, and removes them when the jail stops. A running jail's grant cannot grow: `yolo stop`, then launch again with the flag. A profile you select when you rejoin still hands its agent that provider's key, and says so. | Should work, the same; not yet run on a Mac. | Should work, the same; not yet run on a Mac. | Should work, for that one `yolo`'s sandbox session, since each is its own launch; not yet run on a Mac. |
| **Use your host's SSH keys, git credentials or `gh` login** | No, by design. Your git name and email do arrive, so commits work. To push, give the jail its own key or token. | No, the same. | No, the same. | No, the same. |
| **Work on your project** | Yes. It is at `/workspace`, live and read-write, and on rootless podman new files are yours. | Yes, the same, if the project is in a folder the VM shares. | Yes, the same. | Yes, in place at its real path; nothing is mounted. |
| **See other host folders and files** | Yes: folders with `mounts`, read-only or (from your user config) read-write, and single files with `host_files`. | Yes, if they are in a folder the VM shares. | Yes. Read-only `mounts` and `host_files` folders need Apple Container 1.1.0 or later; a read-write mount works on any version. | Yes, for folders outside every home: `mounts`, read-only from `/Users/Shared` or outside `/Users`, read-write from `/Users/Shared/yolo`, and single files with `host_files`. A folder in your home stops the launch. Not yet tried on a Mac.[^mumounts] |
| **Add tools with `packages`** | Yes. Nix builds them into the jail image the next time the jail starts. With a nix daemon on the host, `YOLO_STORE_PACKAGES=1` skips the image rebuild. | Yes, but slower: each different list builds a whole Linux image.[^cap-pkg-mac] | Yes, but slower, the same as `podman` / macOS. | Yes, as native Mac builds. A package with no Mac build stops the launch.[^cap-pkg-mu] |
| **Add language runtimes with mise** | Yes, with `mise_tools` or the project's `mise.toml`. One tool store serves every project. | Yes, the same. | Yes, but each project keeps its own tool store, so a tool one project installed is installed again for another. | Yes, the same as `podman`: one tool store serves every project. |
| **Install things yourself** (`npm -g`, `uv tool`, `go install`) | Yes, and they are kept per project. The rest of the home is read-only unless you list a folder.[^cap-selfinstall] | Yes, the same. | Yes, kept per project. The whole home is writable. | Yes, kept per project. They are Mac programs, not Linux ones. |
| **Use `nix` inside the jail** | Yes, through your host's nix daemon.[^cap-nix] | No. It is possible, but not planned for now.[^cap-nix-mac] | No. It is possible, but not planned for now.[^cap-nix-mac] | Yes: the sandbox uses your Mac's own `nix`, through its nix daemon.[^cap-nix-mu] |
| **Use yolo's host services** (shared logins, AWS Bedrock credentials, a USB serial port…) | Yes. The login services run by themselves; you turn the others on. | Should work, apart from the Linux-only ones.[^lh-mac] | No: the jail cannot connect back to the Mac.[^lh-ac] | Partly. yolo starts them, and the helpers that must run inside the sandbox run there too, so the OpenAI login service and Bedrock (`aws-auth`) work.[^login-mu] A serial port and the host-process view should work too (not yet tried on a Mac). Sharing one Claude login between sandboxes does not.[^musrefresh] |
| **Reach a service running on your host** | Yes. List the port in `network.forward_host_ports` (for example `[5432]`) and it appears on the jail's `localhost`; this needs `socat` on the host. Or connect to `host.containers.internal`.[^lh-rootful] | Yes, at `host.containers.internal`. | No; not planned yet. Do not set `forward_host_ports` here: it stops the launch. | Yes. The sandbox is on the Mac's own network, so `localhost` is the Mac and `forward_host_ports` is not needed. A remapped port (`"8080:9090"`) is relayed while the command runs (TCP only; not yet tried on a Mac).[^mufwd] |
| **Reach the internet** | Yes. | Yes. | Yes. On macOS 15, run `yolo check` first.[^cap-mac15] | Yes, and your local network too. |
| **Open a jail's dev server from your host** | Yes. Add the port to `network.ports` (for example `"ports": ["3000:3000"]`) and bind the server to `0.0.0.0`, then open `localhost:3000` on your host. | Yes, the same. | Yes, the same, measured once on a Mac.[^acports] | Yes. A port the agent opens is already open on the Mac. |
| **Use a GPU** | Yes, with `gpu` (NVIDIA or AMD). | No. | No. | No setting, but Metal should work as it does for any Mac program. |
| **Use a USB or serial device** | Yes, with `devices`, or a serial port through the `serial` host service. | Not with `devices`. A serial port through the `serial` host service should work.[^lh-mac] | No. | A serial port should work: name its `/dev/cu.*` node in `devices`, or use the `serial` host service. Not yet tried on a Mac.[^mudev] |
| **Run containers inside the jail** | Yes. podman is built in. | Should work (same image). | No. | No. |
| **Cap the jail's memory and CPU** | Yes, with `resources`. | Yes, within the VM's size. | Yes. With no setting the jail gets about half your RAM and cores. | Partly. `resources.memory` stops the session's largest process when the session goes over, and `resources.cpus` sets common build tools' parallelism. Neither is enforced by the kernel, and the launch says so.[^mures] |
| **Keep a jail's builds from stalling your disk** | Yes, with `resources.io`, on a disk whose scheduler honors it; the launch says when yours does not.[^ioprio] | No: the workspace reaches the Mac over VirtioFS, and the launch says so.[^iopriovm] | No, the same.[^iopriovm] | Yes, with `resources.io`: the session runs at a lower macOS disk priority.[^mures] |

[^cap-models]: The same on every setup. Copilot shows a provider's whole model list, beside GitHub's own models when you are signed in to GitHub, from a `providers.json` yolo writes beside its other per-agent settings; `yolo host` writes none, so there Copilot starts on the list's default model alone. yolo ships no Amazon Bedrock model list: each agent starts on its own Bedrock default, and Copilot, which has none, on `openai.gpt-oss-120b-1:0`. See [Providers and Models](../guides/providers-and-models.md).

[^cap-first-mac]: yolo builds a Linux image and copies it into the VM. On the Intel Mac that yolo's CI uses, the copy alone takes 15 to 22 minutes. If Apple Container is also installed, yolo picks it instead; set `YOLO_RUNTIME=podman` to keep podman.

[^cap-ac-stop]: On Apple Container a jail left behind by a closed window is not cleaned up. Run `yolo stop` in its project to stop it.

[^cap-mu-start]: A project inside a home folder is refused. `yolo macos-setup` prepares `/Users/Shared/yolo`, so a project created under it, such as `/Users/Shared/yolo/<name>`, needs nothing more. Launch with `YOLO_RUNTIME=macos-user yolo`, and expect `sudo` to ask for your password. There is no image to build, but the first launch builds the sandbox's tools with nix, which can take many minutes. See [the macos-user backend](../guides/macos.md#the-macos-user-backend).

[^cap-omp]: A jail on an Apple silicon Mac runs ARM Linux, and the `omp` vendor publishes no ARM Linux build, so the launch says `omp` is unavailable. The same is true on an ARM Linux machine. On `macos-user` it is the other way round: `omp` has a build for Apple silicon Macs but not for Intel ones. That depends on the vendor, not on yolo.

[^cap-yolo-mode]: yolo adds each agent's own no-prompts flag or setting, for example `--dangerously-skip-permissions` for `claude` and `--yolo` for `copilot`, and the launch prints the command it ran. `omp` has no such setting. Inside a jail this is always on; there is no switch to turn it off.

[^cap-logins]: Your host's own agent logins are not reused, and a login you make inside a jail survives a restart, with one exception. On every setup except `macos-user`, `copilot` finds no system keychain in the jail, so when you log in it asks whether it may store its token in a plain-text file instead. If you say no (the default for `copilot login`), it saves nothing, and the next `copilot` asks you to log in again. Say yes to keep the login; the token is then kept unencrypted in the project's `.yolo/` folder. On `macos-user` this has not been tried. `claude` and `agy` keep one login for the whole machine. `codex` and `pi` share one OpenAI login through a login service yolo runs on your host, and so does `opencode` on the `codex` profile. `copilot`, `omp`, and `opencode`'s other logins, keep one per project; sharing them is not planned yet. Details: [Do I have to log in again in every workspace?](#5-do-i-have-to-log-in-again-in-every-workspace-and-in-a-second-jail-at-the-same-time)

[^cap-ac-openai]: The jail cannot reach yolo's OpenAI login service on Apple Container, so `codex` and `pi` print `OpenAI login is required.` and the browser login fails too. The launch says so. This was measured on Apple Container 1.1.0; a later Apple release may change it, so check `container --version`.

[^login-mu]: yolo's OpenAI login service must come up for a `macos-user` launch to go ahead; if it does not, the launch stops. `codex` refreshes its token through a helper that each launch runs on the Mac, outside the sandbox, so a long session keeps its login; the sandbox shares the Mac's network, so `codex` reaches it there, and it answers only requests that carry that launch's token, which any program in that sandbox can read. `pi` reaches the same login service through the extension yolo copies into its home and does not need that helper. Bedrock through `aws-auth` works the same way: its credential helper runs outside the sandbox when an agent is on a Bedrock provider (`-p bedrock`, or a profile or provider of your own with `"platform": "aws-bedrock"`). Each helper listens on a port the launch picks, so two sandboxes do not collide, and stops when the launch ends. None of this has been tried on a real Mac yet.

[^cap-mu-keys]: The keys are kept in a file that only the sandbox can read, and yolo deletes it when the session ends. Closing the session's window or ending yolo with `kill` deletes it too, before yolo exits; only a yolo killed outright (`kill -9`) leaves it in yolo's state folder, and the next launch deletes it. If yolo cannot run `sudo` without a password at that moment, it prints the command that deletes the file. Not yet tried on a Mac.

[^cap-pkg-mac]: The jail is Linux, so every package is built for Linux. When the nix binary cache does not have one, yolo starts a temporary Linux builder container for you; that needs the runtime running and your user trusted by the nix daemon (see [Trust your user](../getting-started.md#trust-your-user-required-on-a-mac)).

[^cap-pkg-mu]: Packages are built on top of a built-in set of common tools (`git`, `node`, `python`, `go`, `mise`, `ripgrep`, `jq`, `uv`, `gh`, `neovim` and more), which does not include GNU `sed`, `grep`, `find` or `tar`. Mark a Linux-only package `{"name": "strace", "platforms": ["linux"]}` to skip it here.

[^cap-selfinstall]: `npm -g`, `go install`, `uv tool` and `pip --user` land in home folders that are kept with the project. `cargo install` goes to a store that every project shares, and needs Rust from mise first. An installer that writes its own home folder (`~/.bun`, say) fails with `Read-only file system` until you add the folder to `writable_home_dirs`. The built-in `python3` has no `pip`: use `uv`, or install Python with mise.

[^cap-nix]: `nix shell`, `nix build` and `nix eval` work with no extra flags: the jail image turns on `nix-command` and `flakes` in `/etc/nix/nix.conf`, and your own `~/.config/nix/nix.conf` in the jail layers on top. It needs a multi-user nix on the host, the kind that runs a nix daemon; with a single-user nix the jail gets no `nix` at all. The store is read-only and builds go through your host's daemon. `nix-shell -p` does not work, because the jail has no nixpkgs channel, and a garbage collection on the host can delete what you built.

[^cap-nix-mac]: A jail on a Mac is Linux, and your Mac's nix store holds Mac programs, so the jail cannot simply use it. Giving these jails a nix of their own is possible, but not planned for now. Use one of these instead: add the tool to `packages` and restart the jail, install a language runtime with mise, or switch to the `macos-user` backend, where `nix` works inside the sandbox. On `podman` only, there is an expert opt-in: a Podman Machine created with `/nix` shared, whose store holds the jail's Linux builds, can set `YOLO_NIX_HOST_DAEMON=1` plus `YOLO_NIX_HOST_STORE_LINUX=1`. Set the second one wrongly and the jail will not boot. See [Nested Nix builds inside the jail](../guides/macos.md#nested-nix-builds-inside-the-jail-advanced).

[^cap-nix-mu]: The sandbox gets the same `nix` that yolo used to build its tools, and every build goes through your Mac's nix daemon, so `nix build` and `nix eval` need no extra flags. It needs a multi-user nix, the kind that runs a daemon. Each launch prints one line saying whether `nix` is available in the sandbox and, if it is not, why. If you set `NIX_CONFIG` yourself, yolo keeps it and adds `extra-experimental-features = nix-command flakes` on a line of its own. If your `NIX_CONFIG` already sets `experimental-features` or `extra-experimental-features`, yolo leaves it exactly as you wrote it. A `NIX_REMOTE` you set replaces yolo's.

[^cap-mac15]: Apple Container on macOS 15 has two network faults that show up only after the jail boots: `npm install` hangs, and the agent's first request times out. `yolo check` detects both and prints the fix. See [Does the jail have outbound internet?](#3-does-the-jail-have-outbound-internet)

A few pointers for the rows above:

- **Choose a backend on a Mac:** set `YOLO_RUNTIME` (or the `runtime` key) to `podman`, `container` or
  `macos-user`. See [Which runtime yolo picks](../getting-started.md#which-runtime-yolo-picks).
- **Install an agent:** put `"packs": ["claude"]` in `~/.config/yolo-jail/config.jsonc`, then type
  `claude` in the jail. A project's `yolo-jail.jsonc` cannot set `packs`. See
  [Packs, and the host services they bring](#packs-and-the-host-services-they-bring).
- **Use an API key or another provider:** list a dotenv file in `env_sources`, add the provider's pack,
  and pick it with `yolo -p <profile> -- <agent>`. The key reaches only the agent whose profile
  selects that provider, never another agent or a plain shell, and a value you set yourself on
  the command line wins over the profile's. See
  [Providers and Models](../guides/providers-and-models.md).
- **Push from the jail:** create a key inside with `ssh-keygen` and add it to the repository as a deploy
  key, or put a `GH_TOKEN` in an `env_sources` file.
- **Share more of your machine:** `"mounts": ["~/notes"]` for a read-only folder,
  `{"host": "~/scratch", "mode": "rw"}` in your user config for one the agent may write, and `host_files`
  in your user config for single files. See
  [Settings per setup](settings-per-setup.md#workspace-mounts-and-host-files).
- **Add a tool:** add it to `packages` in `yolo-jail.jsonc`, run `yolo check`, then restart the jail. See
  [Nix Packages](../guides/packages-and-tools.md#nix-packages-image-level).
- **Turn on a host service:** add its pack and `"loopholes": {"<name>": {"enabled": true}}`. See
  [The loopholes](#the-loopholes-host-services-a-jail-can-use).
- **Use a GPU:** `"gpu": {"enabled": true, "vendor": "nvidia"}`. See
  [GPU Passthrough](../guides/devices-and-gpus.md#gpu-passthrough-nvidia).
- **Pick up a config edit:** `yolo stop`, then `yolo` again. See
  [When a change takes effect](configuration.md#when-a-change-takes-effect).

### Common questions

#### 1. Who owns the files the agent writes in my repo?

| Setup | Who owns new files |
|---|---|
| `podman` / Linux, rootless (the usual setup) | You.[^own-rootless] |
| `podman` / Linux, rootful | **`root`**, and nothing warns you. Your editor cannot save them. |
| `podman` / macOS | You.[^vmshare] |
| `container` / macOS | Not yet tested. yolo sets no owner mapping, so Apple Container decides. |
| `macos-user` / macOS | The sandbox account `_yolojail`, but you can read and write them.[^macuserown] |

[^own-rootless]: The agent runs as root inside the container, which on a rootless podman is your own user on the host. Check yours: `podman info --format '{{.Host.Security.Rootless}}'`. A rootful podman makes it the host's root instead, and the launch does not say so.
[^vmshare]: Measured on a Mac: a file the agent writes is owned by your own user. The project must be in a folder the Podman Machine VM shares (your home folder by default); a project outside it fails at launch as a mount that cannot be found.
[^macuserown]: The agent is a real macOS account, so there is no owner mapping. Your access comes from a shared group plus access-control entries inherited from the project folder, so `ls -l` shows `_yolojail` and `git` may print ownership warnings. Two consequences: the project must sit outside every user's home folder (`/Users/Shared/yolo` is set up for you; a project under `/Users/<name>` is refused), and a file *moved* into the folder inherits nothing, so the next launch stops and names `yolo macos-fix-permissions`.

#### 2. Can the agent make a git commit?

Yes, on every setup, if your host has a git identity. yolo copies your host's `user.name` and
`user.email` into the jail at launch. If either is empty on your host, nothing warns you and the agent's
first commit fails with `Please tell me who you are`; on `podman` / macOS the same happens if `git` is not
on the PATH of the shell you launch from.

Check yours before launching: `git config --get user.name && git config --get user.email`. On Apple
Container and `macos-user` the agent can change the copied identity for the session.

#### 3. Does the jail have outbound internet?

Yes, on every setup, and it cannot be turned off. On `macos-user` the agent also reaches your local
network and your Mac's own services.

**Apple Container on macOS 15:** a jail can start fine and then hang on `npm install` or on the agent's
first request. Run `yolo check` once; it finds the problem and prints the command that fixes it. macOS 26
does not have this problem (`sw_vers -productVersion` shows your version).

#### 4. I edited my config and re-ran `yolo`, and nothing changed

Running `yolo` while the jail is already running joins it, and a joined jail keeps the settings it
started with. **After any config edit, run `yolo stop`, then `yolo`.** On podman and Apple Container, a new API key or
`-p` choice, and yolo's built-in skills, reach you without a restart.
On `macos-user` every `yolo` starts fresh, so there is nothing to restart. The full per-setting list is in
[Settings per setup](settings-per-setup.md#what-a-running-jail-picks-up-when-you-run-yolo-again).

#### 5. Do I have to log in again in every workspace? And in a second jail at the same time?

It depends on the agent more than on the setup. Agents whose pack keeps its login machine-wide log in once per machine; the rest log in once per workspace.

| Agent | A second workspace on the same machine |
|---|---|
| `claude`, `agy` | Yes: one login per machine, on every setup.[^shared] |
| `codex`, `pi` | Yes: one login per machine, through yolo's OpenAI login service. Not on Apple Container[^login-ac], only partly on `macos-user`[^login-mu], and not yet tried end to end on `podman` / macOS. |
| `opencode` | On the `codex` profile, yes: the same OpenAI login service as `codex` and `pi`, with the same limits; checked by tests only, no opencode session has run on it. Any other login: no, a fresh one in every workspace.[^perws] |
| `copilot`, `omp` | No: a fresh login in every workspace, on every setup.[^perws] |

Two jails at once is a different question, and the answer is about refreshing a login that is about to
expire:

| Setup | Two jails refreshing the same login |
|---|---|
| `podman` / Linux | Yes. A service on your host takes the refreshes one at a time. |
| `podman` / macOS | Should work. |
| `container` / macOS | No. The jail cannot reach that service, and the launch says so.[^refresh-ac] |
| `macos-user` / macOS | No; not planned yet.[^musrefresh] |

Without this, two jails running the same agent can log each other out; log in again when that happens.

One more hazard, on every setup, for `agy`: if the shared credential is **revoked or expired**, a fresh login inside a jail can be **thrown away at your next entry**, and the dead shared credential put back. Logging in again then works only until the next `yolo`. `claude` no longer has this problem: it keeps its login in the shared folder itself, so a fresh login and every refresh land there. yolo records each time this happens in `~/.yolo-shared-creds.log`; read it if a login keeps not sticking.

[^shared]: The credential lives in a folder shared by every workspace. Claude Code reads and writes it in that folder directly; for `agy`, each boot links the agent's credential file to it. On `macos-user` the credential is also shared by the whole machine, while history and config are kept per project. Every `macos-user` project uses the same sandbox account, though, so a second project's launch stops while another project's session runs, and names that project: quit that session, or use a container runtime for the second project. Two sessions of one project work. Not yet tried on a Mac. On the first `macos-user` launch only, a real folder where a link belongs makes the launch stop and name the path; removing `/Users/_yolojail` fixes it, but it also removes every login the `macos-user` sandbox keeps.
[^mu-keeper]: On `macos-user` one background process per project, the keeper, holds the host services every `yolo` in that project uses: the login services, the Codex and AWS sign-in helpers and a provider bridge. It ends with the last `yolo` in the project. `yolo stop` in the project ends every session and the keeper, and a launch names it when the keeper was killed. Not yet tried on a Mac.
[^login-ac]: On Apple Container the jail cannot reach yolo's OpenAI login service, because traffic from a container to the Mac does not get through. `codex` prints `OpenAI login is required.` and the browser login fails the same way. The launch says so. This was measured on Apple Container 1.1.0, and a later Apple release may change it; check `container --version`. Not planned yet on yolo's side.
[^perws]: yolo can keep a login machine-wide, but the `omp` pack does not ask for it, nor does the `opencode` pack for any login but the ChatGPT subscription's. Not planned yet. `copilot` is different: it keeps its login in the system keychain, which only a `macos-user` sandbox might reach, or else in a file of its own state, next to records that must stay per project, such as which folders you trust. So there is no login file yolo could share on its own, and how to share Copilot's login is still being decided ([what was measured](https://github.com/mschulkind-oss/yolo-jail/blob/main/docs/research/copilot-token-storage.md)).
[^refresh-ac]: Apple Container carries no traffic from a container to the Mac (measured on Apple Container 1.1.0), so the Claude login service cannot be used. Claude still logs in and works; only the coordination between jails is missing.
[^musrefresh]: On `macos-user`, the part of the Claude login service that has to run inside the jail cannot run there, so two sandboxes can still log each other out of Claude. Claude still logs in and works.

### Packs, and the host services they bring

What each kind of pack contribution (`env`, `files`, `mount`, `profile`, services and the rest) does on each setup is in [Settings per setup](settings-per-setup.md#what-a-pack-can-contribute-per-setup).

#### The loopholes: host services a jail can use

**Selecting the pack is not always enough.** Most loopholes stay off until you turn them on by name, next
to their pack — `"packs": ["serial"]` plus `"loopholes": {"serial": {"enabled": true}}`, or
`yolo loopholes enable serial` run in a project for that project alone. The two login
services are the exception: they come on with their pack. A loophole you turn on starts the next time
the jail starts, not when you join a running jail. `yolo loopholes list` shows the ones your config
selects.

| Loophole (its pack) | What it does | On by default | `podman` / Linux | `podman` / macOS | `container` / macOS | `macos-user` / macOS |
|---|---|---|---|---|---|---|
| `claude-oauth-broker` (`claude`) | Lets several jails share one Claude login without logging each other out | Yes | Yes[^lh-rootful] | Should work[^lh-mac] | No[^lh-ac] | No; not planned yet[^musrefresh] |
| `openai-auth-broker` (`openai-auth`, brought in by `claude`, `codex`, `opencode` and `pi`) | One OpenAI subscription login for `codex`, `pi`, and `opencode` on the `codex` profile, in every jail | Yes | Yes | Should work[^lh-mac] | No[^lh-ac] | Yes[^login-mu] |
| `aws-auth` (`aws-auth`) | Bedrock with credentials from your host's `aws sso login`, narrowed to a role before they reach the jail | No | Yes[^lh-aws] | Should work[^lh-mac] | No[^lh-ac] | Yes[^login-mu] |
| `github-broker` (`github`) | `gh` in the jail, run by your host's GitHub login against the project's own repositories; read-only for now | No | Yes[^lh-gh] | Should work[^lh-mac] | No[^lh-ac] | Starts; not yet run on a Mac |
| `serial` (`serial`) | A USB serial device on the host, through an allowlist (`yolo-serial`) | No | Yes | Should work[^lh-mac] | No[^lh-ac] | Should work: `yolo-serial` is installed in the sandbox when the loophole is on. Not yet run on a Mac. |
| `journal` (`journal`) | The host's systemd journal (`yolo-journalctl`) | No | Yes | No: needs a Linux host[^lh-linux] | No: needs a Linux host[^lh-linux] | No; `macos_log` instead[^maclog] |
| `host-processes` (`host-processes`) | A filtered list of host processes (`yolo-ps`); nothing shows until you list names | No | Yes | Should work[^lh-mac] | No[^lh-ac] | Should work[^muprocs] |
| `audio` (`audio`) | The host's microphone and speakers (PipeWire or PulseAudio) | No | Yes | No: needs a Linux host[^lh-linux][^audiomac] | No: needs a Linux host[^lh-linux][^audiomac] | No[^audiomac] |
| `cgroup-delegate` (`cgroup-delegate`) | Lets the jail cap its own sub-jobs (`yolo-cglimit`) | No | Yes, with cgroup v2[^cgv2] | No: needs a Linux host[^lh-linux] | No: needs a Linux host[^lh-linux] | No |
| Provider helper (`wire-bridge`) | Lets `claude` or `copilot` use the Cerebras and Kilo providers, and `claude` your ChatGPT subscription | Comes in automatically | Yes | Yes | Yes | Yes: each launch that needs it starts it outside the sandbox, for that launch only[^svc] |

[^lh-rootful]: On rootless podman (the usual setup) yolo asks podman to forward your host's loopback into the jail, and if a service is still unreachable the launch stops with an error rather than continuing without it. On a rootful podman the jail cannot reach yolo's host services at all, and the launch warns; switch to rootless podman, or use `"network": {"mode": "host"}` at the cost of the jail's network isolation. Check yours: `podman info --format '{{.Host.Security.Rootless}}'`.
[^lh-mac]: The jail reaches your Mac's services through the Podman Machine VM at `host.containers.internal`, and that connection is tested nightly. The services themselves have not yet been run end to end on a Mac. If a service cannot be reached, the launch warns but still starts.
[^lh-ac]: Apple Container carries no traffic from a container back to the Mac (measured on Apple Container 1.1.0), so no host service can be used, and the launch lists each one it had to skip. This can change only with an Apple Container release.
[^lh-aws]: Turn it on in your user config with the SSO profile and the role to narrow to, then use the `bedrock` profile: `yolo -p bedrock -- claude`. The credential service inside the jail runs only when an agent is on a Bedrock provider (`-p bedrock`, or a profile or provider of your own with `"platform": "aws-bedrock"`), and only that agent can use it. See [the `aws-auth` pack](https://github.com/mschulkind-oss/yolo-jail/blob/main/packs/aws-auth/README.md). It is in daily use with a real AWS SSO login on Linux, and a running jail picks up a new login with no relaunch; a turn during a longer lapse has not been watched yet.
[^lh-gh]: Turned on one project at a time, by `yolo loopholes enable github-broker` run in it on the host; no config file switches it. Needs `gh` logged in on the host. The first launch of a project with a GitHub remote asks you to approve its repositories, and every call is recorded for `yolo audit`. A repository the project has no remote for can be listed in the project's own config as `brokered.github.repos`, and the next fresh launch asks you to approve it. Tested against a stand-in for `gh`, never a real GitHub login. See [GitHub](../guides/github.md#adding-a-repository-the-project-has-no-remote-for).
[^lh-linux]: These need Linux on the host. On a Mac, turning one on does nothing, and the launch says so in one line naming the loophole.
[^cgv2]: Needs cgroup v2 on the host: `test -e /sys/fs/cgroup/cgroup.controllers && echo v2`.
[^maclog]: `macos-user` offers Apple's unified log instead, behind its own `macos_log` key (`off` / `user` / `full`) and a `yolo-log` helper. Under `off`, the default, the sandbox cannot read the log at all, not only through the helper; `user` and `full` let it. Not yet tried on a Mac.
[^muprocs]: The `macos-user` sandbox sees which processes are running on your Mac but not their command lines; `yolo-ps` shows the ones you list, with their command lines. Not yet run on a Mac.
[^audiomac]: On a Mac the `audio` pack sets neither `PULSE_SERVER` nor `PIPEWIRE_REMOTE`, because no jail there gets the host's audio sockets, and the launch names both as left out. Audio tools in the jail use their own defaults.

### Things that will not work on a Mac

- **NVIDIA or AMD GPUs, and `/dev/kvm`.** Macs have no GPU or virtualization device a Linux jail can use;
  the launch warns if you set `gpu` or `kvm`. Run that work on a Linux machine. On `macos-user`, Metal
  should work as it does for any Mac program.
- **Linux programs on `macos-user`.** It runs Mac programs and Mac tools: `sed -i` needs a suffix,
  and `find -printf`, `tar --wildcards` and `grep -P` are missing. The agent's briefing says so;
  the launch does not. Write portable scripts, or use a container setup.

---

## Workspace, mounts, and host files

This block is what the jail can see of your machine's filesystem, and what it may write. Read the table with two things in mind.

- **Takes effect** is when an edit to the key reaches a jail. Almost everything here is passed on the container command line, so it is frozen at the **fresh** launch: re-entering a running jail returns before the config-change check, and an edited value does nothing until you stop the jail and launch again. `macos-user` has no re-entry at all — every invocation builds a new sandbox — so on that column every row behaves as `any entry`.
- **Bolded cells are silent.** The key passes `yolo check`, the launch prints nothing, and the behaviour is simply not there.

| Row | `podman`/Linux | `podman`/macOS | `container`/macOS | `macos-user`/macOS | Takes effect |
|---|---|---|---|---|---|
| `mounts` — host dirs read-only at `/ctx` | works | works — VM must share the source[^vm-share] | works on 1.1.0+; older: `absent, warns`[^acro] | works for a folder outside every home, at `$YOLO_CONTEXT_DIR`; others refuse the launch — never run on a Mac[^mumounts] | fresh launch |
| `mounts` read-write form — `{"host", "mode": "rw"}`, user config only | works — named on every launch[^rwmount] | works — VM must share the source; never run on a Mac[^vm-share] | works on any version — never measured on a Mac[^rwmount] | works under `/Users/Shared/yolo`; elsewhere refuses the launch — never run on a Mac[^mumounts] | fresh launch |
| `workspace_readonly` — lock workspace sub-paths | works — read-only overlay per path | works | works on 1.1.0+; older: **paths stay writable**, warns[^acro] | works — sandbox policy rule, not a mount | fresh launch |
| … and the `yolo-jail.jsonc` lock it also performs | works | works | works on 1.1.0+; below, lost **silently**[^acro] | works — the same sandbox rule as the entries[^mujsonc] | fresh launch |
| `per_side_paths` — `.venv`/`node_modules` not shared | works — private dir mounted over each path[^leftover] | works | works — unverified on Apple silicon[^achw] | `absent, warns` — host and sandbox share them; uv gets its own venv[^mupsp] | fresh launch |
| `writable_home_dirs` — extra writable `$HOME` paths | works — read-write bind per path | works | works — the whole home is writable already | works — sandbox home is writable; dir not pre-created | fresh launch |
| `ephemeral_storage` — `volume` vs `tmpfs` scratch | works | works — volumes sit on the VM's disk | **always RAM-backed, silent**[^aceph] | `n/a` — the machine's real `/tmp`[^mueph] | fresh launch |
| `cache_relocations` — move a jail cache to other storage | works — a provisioning failure refuses the launch | expected to work, never measured[^vm-share] | `absent, warns` — not built[^cachegap] | works, `~/.cache` only — not yet tried on a Mac[^mureloc] | fresh launch |
| `host_files` modes `readonly` / `once` / `copy` | works | works | works | works[^dac] | fresh launch |
| `host_files` mode `capture` — keep your local edits | works | works | works | works — needs a workspace ACL[^acl] | fresh launch |
| `host_files` source is a **file** | works — read-only bind | works[^vm-share] | works — copied in, **agent-writable**[^achostlayer] | works — root-owned copy at launch[^snap] | fresh launch |
| `host_files` source is a **directory** | works | works[^vm-share] | works on 1.1.0+; older: `absent, warns`[^acdir] | works — root-owned copy at launch, size-capped[^mudir] | fresh launch |
| `host_files` destination: writable, and private per workspace | works | works | works — whole home is per-workspace | works under `~/.config` and at the home root (home root never run on a Mac); a new top-level folder **shared, silent**[^mutier] | fresh launch |
| `host_files` entries with a `source:` come from your user config only | works | works | works | works | n/a — a repo's config cannot name host bytes |
| `host_management`, `host_wrappers`, `host_apply_on_launch`, `host_floor`, `host_path`, `provisioners`, `promotion_target` | works | works | works | works | any entry — host-side keys[^hostside][^wrappath] |
| Host-side verbs refuse when run **inside** the jail | works | works | works | works — unverified on a Mac[^muinjail] | any entry |
| `programs: { autoprune: true }` | works[^prunetime] | works[^prunetime] | works[^prunetime] | works — unmeasured on a Mac[^prunetime] | fresh launch (switch)[^prunetime] |
| `yolo programs ls` / `remove` from inside the jail | works | works | works | works — unmeasured on a Mac[^muprograms] | any entry |

[^acro]: Apple Container honours read-only binds from version 1.1.0 (measured on macOS 26.5, Apple silicon). Check yours: `container --version`. Below that floor yolo refuses to bind a `mounts` entry rather than binding it writable, and prints one skip line per entry. `workspace_readonly` is the exception: those paths sit inside the writable workspace and cannot be skipped, so they arrive writable behind a loud warning that names every declared entry — but never the `yolo-jail.jsonc` lock. All of these lines are printed while the launch builds the container, so re-entering a running jail never shows them.

[^vm-share]: `podman` on macOS runs a Linux VM, and a bind source must be a path that VM shares — `/Users`, `/private` and `/var/folders` by default. Check one folder with `podman machine ssh -- test -d <folder> && echo shared`. Folders are added only when the machine is created, and `podman machine init -v <path>` replaces the defaults rather than adding to them, so repeat them in the same command ([Getting Started](../getting-started.md#macos-podman) shows the full list). yolo reads the machine's own list before the container starts: a source outside it stops the launch, naming the folder and the `podman machine init` command that adds it, and `yolo check` reports whether your project and yolo's own files are shared. If yolo cannot read the list, it says nothing, and a source outside it fails inside the VM as `statfs …: no such file or directory`. For `cache_relocations` a target under `$HOME` is expected to work and one on `/Volumes` is expected to stop the launch; nobody has confirmed either on hardware.

[^mumounts]: `macos-user` has no container to mount into, so each entry, and each pack `mount`, is a link in `$YOLO_CONTEXT_DIR` (not `/ctx`) to the folder itself, and the sandbox decides what the agent may do there. Before the sandbox starts, the launch checks as the sandbox account that it can read the folder (and write it, for a read-write entry). The folder must sit outside every user's home: a read-only one under `/Users/Shared` or outside `/Users`, a read-write one under `/Users/Shared/yolo`. A folder in a home, on another volume, overlapping the project or a place the sandbox writes such as `/tmp`, or an `at` outside `/ctx`, stops the launch, naming each entry and why. Tools that resolve paths show the real folder, and a subfolder the sandbox account may not read stays unreadable. An entry whose folder does not exist is skipped with a warning, as everywhere. Every launch names a read-write entry, as on the other setups. None of this has been run on a Mac yet.

[^rwmount]: A read-write entry is refused anywhere but your user config (or a `--user-layer`), and refused when its folder is, or contains, your home or one of yolo's own folders, or overlaps the project. Every launch prints one line naming it and warning that anything on your machine that later reads the folder reads what the jail wrote. On rootless podman the agent's files land as yours; on a rootful one they are root's, and the line says so. The same `$YOLO_CONTEXT_DIR` names `/ctx` on every container setup.

[^mujsonc]: Setting `workspace_readonly` at all also locks the workspace's own `yolo-jail.jsonc` (or the `yolo-jail.json` yolo reads when that is the file you keep) against the agent — worth knowing before you protect an unrelated path. On `macos-user` the file joins the same sandbox rule as your entries, and a config that is a link locks the file it points at too, wherever that is; not yet tried on a Mac. The lock covers that one file, on every setup: the agent can still turn the key off in a `yolo-jail.local.jsonc` or an included file, neither of which is locked, or by adding a `yolo-jail.jsonc` beside a locked `yolo-jail.json`. Each of those shows up in the y/N config diff at the next launch.

[^leftover]: A "shadow" is a private directory mounted over `.venv`, `node_modules` and your declared paths so host and jail do not share build output. The mountpoint is created inside your live workspace, so an empty root-owned `.venv`/`node_modules` can be left behind after the jail exits.

[^achw]: Expected to work by construction — the same nested-bind shape Apple Container already uses for the cache — but never run on Apple silicon. Each entry also consumes one directory-sharing slot against an undocumented per-container limit.

[^mupsp]: There is no mount namespace here, so one path cannot show different contents inside and outside; the paths are shared, and the launch warns, naming your entries and each of `node_modules`, `.venv` and a mise-declared venv that the project has or would make. `uv` is pointed at its own `.venv-macos-user` (`UV_PROJECT_ENVIRONMENT`; a value you set wins), so the host's `.venv` survives a `uv sync` in the sandbox. `python -m venv`, poetry, pipenv and mise's `_.python.venv` still use the shared path, and nothing redirects `node_modules`. Not yet tried on a Mac.

[^aceph]: Apple Container always gets RAM-backed scratch for `/tmp`, `/var/tmp` and the container directories, whatever the key says, and nothing is printed. A build that writes large temp files can hit the memory ceiling this key exists to avoid. `YOLO_RUNTIME=podman` if you need disk-backed scratch.

[^mueph]: No container and no read-only root filesystem, so the sandbox writes the machine's real `/tmp` and `/var/folders` with no configuration. Only the RAM-backed (`tmpfs`) *choice* is unavailable, and asking for it prints a warning.

[^cachegap]: One yellow line per launch names the cache subdirectories that stayed put and points at `YOLO_RUNTIME=podman`. It is not a backend limitation: Apple Container already nests a writable bind at exactly the depth a relocation needs, and it is unbuilt work. The line is printed at launch assembly, so a re-entry never shows it.

[^mureloc]: `~/.cache/<subdir>` in the sandbox account's home becomes a link to your folder, and each launch names it. The folder may be on another disk under `/Volumes`, but not in a home or in the project. A folder yolo creates is opened to the sandbox account as it is created; one that already holds files needs `yolo macos-fix-permissions <folder>`, and the launch stops, saying so, until the sandbox can write it. A Mac tool that caches under `~/Library/Caches` instead (Go's build cache; likely pip and playwright) is not moved.

[^dac]: These modes set file permission bits, not enforcement. In a container the agent is root, so `readonly`'s `0444` is a speed bump; on `macos-user` the sandbox runs as a separate account, so the bits actually bite.

[^acl]: `capture` is the one mode that writes into your workspace (it stores your local edits beside the composed file), so on `macos-user` the workspace needs the inherited `group:_yolojail allow` entry. Check with `ls -lde <workspace>`; repair with `yolo macos-fix-permissions`. Without it the launch dies as `mkdir …/.yolo/prism: permission denied`, naming neither ACLs nor the fix.

[^achostlayer]: Apple Container cannot bind a single file, so the host bytes are copied into the jail's home. That home is bound writable, so the agent can rewrite the host layer its own composed file is built from — where `podman` keeps that layer read-only. Nothing warns.

[^snap]: A root-owned copy taken at launch, not a live view. Every consumer reads its config at boot, so this is equivalent in practice. Symlinking the real file was measured and rejected: the sandbox runs as a different account, and a macOS home need not be readable by it.

[^acdir]: A directory source is bound read-only from Apple Container 1.1.0. Below that version, or when yolo cannot read `container --version`, the entry is skipped with one `Skipping host_files directory ~/<path> …` line naming the reason, rather than bound writable, and nothing appears at the destination. Like the `mounts` skip lines, it prints only while a fresh launch builds the container.

[^mudir]: The folder is copied at every launch, as a container copies it into the home at boot, and each file keeps its permissions. Only what is inside the folder is copied: a link in it that leads elsewhere is left out. A folder over 32 MiB or 4096 files and folders (64 MiB and 8192 across a launch's directory entries) stops the launch and names the entry; split it into file entries, or use `runtime: "podman"`, or `"container"` from Apple Container 1.1.0[^acdir]. Not yet tried on a Mac.

[^mutier]: Composed files under `~/.config/…` and at the home root (`~/.npmrc`, `~/.netrc`) land in a per-workspace directory and are fine; the home-root half has not yet run on a Mac. A file in a new top-level folder (`~/.aws/config`) lands in the sandbox account home that *every* workspace on the machine shares, so one workspace's launch overwrites another's — or, under `once`, finds the other's file already there and never seeds its own. Nothing warns. After an upgrade, a launch may stop once on a home-root file an earlier version left in the sandbox account; run the `sudo rm` it prints. `~/.zprofile`, `~/.zshrc` and `~/.bash_profile` are yolo's own on this backend, rewritten on every launch, so an entry naming one stops the launch; for zsh, declare `~/.zshenv` instead.

[^hostside]: These are host-CLI keys with no reader in any jail, so the backend is not the axis. Two behaviours to know: `host_management` is `none` (yolo writes nothing) when it is unset and when the user config cannot be read, so a malformed config looks like the default; and `promotion_target` accepts only `local` or `pack:<name>`, silently falling back to `local` for anything else. A `host_management` of `"assert"`, which is retired, is refused.

[^wrappath]: `host_wrappers` puts small scripts ahead of the real agent binaries on your PATH, and `host_apply_on_launch` only ever fires through one of them. An IDE or desktop launcher never reads your shell rc, on Linux or macOS, so the PATH line does not reach it: point it at the wrapper's full path instead, `~/.local/share/yolo-jail/bin/wrap/<program>`, which starts yolo by its full path too. For a program yolo keeps no copy of because you left its pack out of `host_floor` (or gave it to a package manager, or its vendor publishes no build for your machine), that launcher also needs `host_path` to name the folder holding the program (`~/.local/bin` for claude's own installer); `yolo check` says when. Any other program a selected pack declares runs from yolo's own copy, and a launch that cannot install it stops instead of searching a PATH.

[^muinjail]: `yolo host apply` (with `--assert` or `--revert`) and `yolo config promote` refuse inside a jail on all four setups and write nothing, naming the command to run on the host. On `macos-user` the session carries the same jail marker as a container, so the same refusal applies there; that has not yet been run on a Mac.

[^prunetime]: A time-axis oddity worth knowing: the *switch* is frozen at the fresh launch, but the pruning itself re-runs on every re-entry — so turning autoprune off does not stop a running jail from pruning until you relaunch. On `macos-user` every command is a fresh launch, so the switch takes effect on the next one. Read from your user config only (a repo must not delete your binaries), and off unless explicitly on.

[^muprograms]: On `macos-user` the session names the staged packs and the workspace, so `yolo programs ls` and `remove` run inside the sandbox and read the workspace's install records. A launch that staged no packs still prints "No staged packs here". Not yet run on a Mac.

## Resources, devices, and networking

Two things to read before the table:

> [!WARNING]
> **"Unconfigured" is not "uncapped" on Apple Container.** With no `resources` block, the `container` backend still gets a memory and CPU cap — **yolo's own arithmetic**, roughly half your RAM and half your cores. The startup banner does not mention it (it prints only what you wrote); the agent's briefing does. On both podman backends, unset really does mean unlimited.

> [!IMPORTANT]
> **Every key in this table is frozen at the fresh launch.** All of them ride the container command line, so re-entering an already-running jail returns before the config-change prompt and before the command line is rebuilt. An edited value does nothing, silently — and worse, the agent's briefing *is* re-rendered from your edited file, so the agent is told a cap or a port that is not in force. `yolo config drift` is the detector; nothing points you at it. Exit the jail and relaunch.

| Key | `podman` / Linux | `podman` / macOS | `container` / macOS | `macos-user` / macOS | Takes effect |
|---|---|---|---|---|---|
| `resources.memory` | works — kernel-enforced [^cgroups] | works — enforced in the VM, clipped to it [^vm-limits] | works — **but a cap you never wrote** [^acdefault] | works, sampled — not kernel-enforced; the largest process is stopped [^mures] | fresh launch [^muentry] |
| `resources.cpus` | works — kernel-enforced [^cgroups] | works — clipped to the VM's vCPUs, silently [^vm-limits] | works — half your cores unless set [^acdefault] | works, cooperatively — common build tools' parallelism, not a cap [^mures] | fresh launch [^muentry] |
| `resources.pids_limit` | works — always capped, default applied | works — always capped | absent, **silent** — no surface mentions it [^acpids] | absent, warns — an open question [^mures] | fresh launch [^muentry] |
| `resources.io` (disk I/O priority) | works on bfq and mq-deadline disks; the launch names a disk that ignores it [^ioprio] | absent, warns — VirtioFS carries no priority [^iopriovm] | absent, warns — the same [^iopriovm] | works — the session's macOS disk priority [^mures] | fresh launch [^muentry] |
| `devices` (raw path, `usb:`, `cgroup_rule`) | works — all three forms [^lsusb] | absent, warns — refused by OS, never probed [^macdev] | absent, warns [^acdev] | raw `/dev` path: works — device control allowed, named at launch; `usb:` and `cgroup_rule`: absent, warns [^mudev] | fresh launch [^muentry] |
| `gpu` — `vendor: nvidia` | works — CDI device + driver env [^nvidia] | absent, warns [^gpumac] | absent, warns [^gpumac] | absent, warns — GPU already reachable [^mugpu] | fresh launch [^muentry] |
| `gpu` — `vendor: amd` | works — `mode: devices`; `mode: cdi` needs an AMD CDI spec, and warns without one [^amdcdi] | absent, warns [^gpumac] | absent, warns [^gpumac] | absent, warns [^mugpu] | fresh launch [^muentry] |
| `kvm` | works — needs host group membership [^kvmhost] | absent, warns — not built yet [^kvmmac] | absent, warns [^acdev] | absent, warns — no Linux kernel to ask | fresh launch [^muentry] |
| `network.mode: "bridge"` (default) | works — own network namespace | works — namespace inside the VM [^vm-limits] | works — own network per container | absent, **silent** — no isolation at all [^mubridge] | fresh launch |
| `network.mode: "host"` | works — and drops both port keys [^hostdrop] | **applies to the VM, not the Mac — silent** [^hostmac] | absent, warns — runs bridged; port keys still work [^achost] | works — the only mode it has; drops both port keys' remaps [^muports] | fresh launch |
| `network.ports` (HOST:JAIL) | works — passed to podman [^dnat] | works for a `0.0.0.0` listener — **measured** [^vm-limits] [^dnat] | works — measured once on a Mac [^acports] | works — a remap is relayed (TCP); every bound port is already open [^muports] | fresh launch |
| `network.forward_host_ports` (JAIL:HOST) | works — needs `socat` on the host [^socat] | works — TCP to the VM gateway, unmeasured [^vm-limits] | refuses the launch, naming the key [^acfwd] | works — same-port entries already true, a remap is relayed (TCP) [^mufwd] | fresh launch |

Terms: **rootless podman** runs as your own user with no root daemon; **Podman Machine** is the Linux VM podman uses on macOS; **CDI** is the Container Device Interface, a host-side YAML/JSON file describing a GPU; a **remap** is a port entry whose two numbers differ (`5432:3306`).

The `--network` CLI flag overrides `network.mode` for a jail that launch starts, and each setup applies the mode it chose as the two `network.mode` rows above say; with no flag the key decides. Like the keys, it does nothing to a jail that is already running: re-entering one keeps the network it started with, and the agent's briefing is re-rendered from that re-entry's flag and your file, as the note above the table describes. The validator's warning that the port keys are "ignored when `network.mode` is `'host'`" — the one network message that does fire on any entry — is **false on `container`** (which runs bridged and keeps the ports) and misleading on `macos-user` (both keys are ignored whatever the mode).

**Can the jail reach a service on your host's `127.0.0.1` with no port setting?** `podman` / Linux (rootless): yes, at `host.containers.internal`; a rootful podman warns that it cannot. `podman` / macOS: yes, at `host.containers.internal`. Apple Container: no, and `forward_host_ports` stops the launch there [^acfwd]. `macos-user`: yes, at `localhost`.

[^cgroups]: Needs cgroup v2 with delegation. On a **rootless** podman on a cgroup-v1 host, podman ignores memory/CPU/pids limits and yolo does not pre-flight it: the flag is emitted and does nothing. Check yours: `podman info --format '{{.Host.CgroupVersion}} rootless={{.Host.Security.Rootless}}'`.
[^vm-limits]: On macOS, podman runs inside a Linux VM, so a limit is enforced against the VM and a network namespace is created inside it. Asking for more memory or more CPUs than the machine has is accepted and silently clipped — for memory the only signal is a hint printed *after* an out-of-memory kill; for CPUs there is none. Check the machine's size with `podman machine inspect`, and resize with `podman machine set --memory … --cpus …`.
[^acdefault]: When the key is unset, yolo probes host RAM and core count and emits half of each (memory floored at 4 GB, CPUs at 2). **A fractional `resources.cpus` breaks the launch here, measured 2026-09-16:** yolo's validator permits any positive number, and `container run --cpus 1.5` dies as `Error: The value '1.5' is invalid for '--cpus <cpus>'` followed by a usage block — a backend's argument parser shown to a user for a value yolo called valid. Integers are fine (`--cpus 2` runs). The same config works on podman.
[^mures]: `macos-user` runs the agent as an ordinary sandboxed process, so there is no cgroup to write, and each key does what macOS allows. `io`: `"low"` and `"idle"` set the session's macOS disk I/O policy (`IOPOL_UTILITY` and `IOPOL_THROTTLE`), which every process in it inherits; it is advisory, and a `nix build` runs in the host's nix daemon at its own priority. `memory`: a helper inside the sandbox adds up the session's resident memory every two seconds and, when it is over, stops the largest process; it is not enforced by the kernel, so a spike between two checks is missed, the agent can stop the helper, and a process that detaches from the session is not counted. `cpus`: `GOMAXPROCS`, `CARGO_BUILD_JOBS`, `RAYON_NUM_THREADS` and `OMP_NUM_THREADS` are set to the number (rounded up, and never above your Mac's CPU count), unless your own environment sets them; a program that ignores them is not limited. `pids_limit` is read and ignored, and the launch says so. The launch prints one line for `cpus` and one for `memory` saying how far each goes. A runaway build can still slow the whole Mac. None of the three has been run on a Mac yet.
[^ioprio]: `"io": "low"` (best effort, level 7) or `"io": "idle"` lowers the disk priority of every process in the jail, so a build yields the disk when your desktop wants it too. Only two disk schedulers honor it: bfq honors both values and mq-deadline honors `"idle"` only. kyber and none, the usual default for NVMe drives, ignore it; check yours with `cat /sys/block/<disk>/queue/scheduler`. The launch prints one line naming each disk under the workspace that ignores the value, re-entering the jail repeats it, and `yolo check` grades the disk and names the host change. It is advisory, not a cap: a process can raise its own priority again, and buffered writes, most of a build's output, are flushed later by the kernel at its own priority. A `nix build` runs its builders in the host's nix daemon, outside the jail, so it keeps the daemon's priority. A nested jail that declares nothing inherits the outer jail's.
[^iopriovm]: The jail runs in a Linux VM, and your workspace reaches the Mac over VirtioFS, whose protocol has no priority field, so no priority set in the VM reaches the Mac's disk for build output. yolo does not pass the value, and the launch prints one line saying so.
[^muentry]: `macos-user` is the one setup with no re-entry: every invocation starts a fresh sandbox, so it re-reads config every time. The frozen-at-launch rule applies to the three container setups.
[^acpids]: No process cap is passed and no banner, briefing, or warning mentions it — a fork bomb in the jail is unbounded. Also **not built rather than impossible**, and the mechanism is now measured: the container's cgroup filesystem IS writable on `container` 1.1.0 (`cgroup.controllers` reads `cpuset cpu io memory hugetlb pids`, and `+pids` into `cgroup.subtree_control` succeeds), so the jail's own boot can write the cap the CLI cannot pass.
[^lsusb]: `usb: "vendor:product"` needs `lsusb` on the host PATH or it degrades to a warning, and a replugged device changes its bus path — a stale raw path becomes a skipped line, never an error. Whether `cgroup_rule` is actually honored under cgroup v2 is unverified.
[^macdev]: The refusal is by host OS, before any check of *where* the device lives — yet yolo itself passes a VM-internal device path on the same launch, so a path that exists inside the Linux VM (a tun device, a loop device) is refused for a reason that does not apply. USB additionally needs macOS 15 or later (`sw_vers -productVersion`) plus Podman Machine support.
[^acdev]: Apple Container passes no devices at all — **verified against hardware** 2026-09-16: `container run --help` on 1.1.0 has no `--device`, no CDI flag, and no device selector of any kind.
[^mudev]: There is nothing to pass through: the sandboxed process opens device files under ordinary macOS file permissions. The sandbox refuses device control calls (`ioctl`) on everything except terminals and the `/dev` nodes you list in `devices`, so a serial adapter should work once its `/dev/cu.*` node is listed (`ls /dev/cu.*` shows the ones plugged in), and the launch names each node it allowed. Raw disks stay refused, and an entry outside `/dev` is skipped with a warning; `usb:` and `cgroup_rule` entries name no node and are not read. Not yet tried on a Mac.
[^mugpu]: Not read on `macos-user`, and the launch names the key. The agent is an ordinary Mac program, so Metal (Apple's GPU API) should be reachable as it is for any program you run; there is no NVIDIA or AMD card to pass on a Mac.
[^nvidia]: Needs the NVIDIA Container Toolkit, `runc` as the runtime (CDI fails under crun), and a CDI spec whose driver version matches the host driver. ⚠ The launch probe accepts only a `.yaml` spec: a host whose spec is `nvidia.json` is silently downgraded to no GPU. Check with `nvidia-smi -L` and `ls /etc/cdi /var/run/cdi`. A failed probe always warns and starts without the GPU, never refuses.
[^gpumac]: Apple silicon has no NVIDIA or ROCm hardware and neither macOS VM does PCIe passthrough, so CUDA/ROCm in a Linux guest is out. But the cell is **narrower than "impossible"**: a podman machine on the libkrun provider exposes a virtual GPU to the container, which gives Vulkan *compute* translated to Metal — a worse mechanism for the same goal ("the jail gets GPU compute"). yolo cannot express it: config accepts only the `nvidia` and `amd` vendors, and device passthrough is refused by host OS before any path is examined.
[^amdcdi]: With `mode: devices` the raw GPU device nodes are passed and the pre-flight probes for them. With `mode: cdi` the pre-flight also needs an AMD CDI spec: any `.json` or `.yaml` file of kind `amd.com/gpu`, whatever its name, in `/etc/cdi`, `/var/run/cdi` or a directory containers.conf's `cdi_spec_dirs` names, which is where podman reads it from. Without one the launch warns, names the command that writes it (`sudo amd-ctk cdi generate --output=/etc/cdi/amd.json`), and starts without the GPU. `yolo check` reports the same spec the same way.
[^kvmhost]: Needs CPU virtualization extensions, the `kvm` module, and (rootless) your user in the `kvm` group — `yolo check` covers all three.
[^kvmmac]: The warning fires by host OS, and the device check it skips would have looked on the Mac rather than inside the Linux VM where the device would live. Reaching it needs Apple's nested virtualization (macOS 15+, M3 or later) plus Podman Machine support, and then a probe of the VM rather than the Mac.
[^mubridge]: A written `"mode": "bridge"` is not read: the sandboxed process shares the launcher's network stack and every port it binds is on the Mac's real interfaces. The agent's briefing says so; the human who wrote the key is told nothing. A warning on an explicit key is the small missing piece — the isolation itself the sandbox cannot provide.
[^hostdrop]: By design: under host networking yolo stops requesting host-loopback forwarding and drops **both** port keys, because there is nothing left to map. It also tells the jail that the namespace is shared, which makes an unreachable yolo service refuse the launch rather than warn.
[^hostmac]: The flag is applied — to the VM's network namespace. Two silent consequences: the agent is told "localhost resolves directly to the host", which is false (it is the VM's loopback); and the jail is told the launcher's namespace is shared, which **escalates** — with a loopback service enabled, the launch can be refused for a boundary that was never crossed.
[^achost]: Apple Container accepts no network selector, so the key cannot be honored as written; the warning names the key and the consequence. The port keys are unaffected by the mode: `network.ports` works here [^acports], and `network.forward_host_ports` stops the launch in either mode [^acfwd]. The *goal* remains reachable and unbuilt, and the raw material is the one transport that does cross this boundary: a published UNIX socket, which carries data both ways in the host→container direction.
[^dnat]: A jail service bound to `127.0.0.1` (rather than `0.0.0.0`) is *meant* to stay publishable: yolo installs an address translation at boot for each published port. **On podman/macOS it does not work, and that is now measured** — with the rule installed, `route_localnet=1` and the listener confirmed, the Mac's dial to the published port connects and receives nothing, while an identical `0.0.0.0` listener answers. So **bind `0.0.0.0` in the jail**, and the two documentation surfaces that say a `127.0.0.1` listener is not publishable (including the agent briefing) are RIGHT rather than stale. The mechanism is that rootless port forwarding hands the connection over inside the network namespace, which never traverses the `PREROUTING` chain the fixup writes — so expect the same on **rootless Linux**, where it remains unmeasured. Your deciding host fact: `podman info --format '{{.Host.Security.Rootless}} {{.Host.RootlessNetworkCmd}}'`. On `container` none of this machinery is emitted at all, so bind `0.0.0.0` there too: that is the listener the one measured run reached [^acports].
[^socat]: `socat` must be installed **on the host**; absent, you get one warning and no forwarding. This is the one network key with an end-to-end test, and that test runs only on Linux.
[^acports]: **Measured on `container` 1.1.0 / macOS 26.5, on 2026-10-03:** a published port answered the Mac at `127.0.0.1:<host port>`, under the default network mode and under `network.mode: "host"` alike. Earlier runs on the same Mac, in September, got nothing back, and what changed in between is not recorded. On those runs macOS Local Network privacy was blocking the Mac's own connections to the container, so if a published port accepts a connection and returns nothing, look under System Settings → Privacy & Security → Local Network for Apple Container's helper programs; whether allowing them fixes it has not been tested. The launch says nothing either way. The port listens on IPv4 only, so dial `127.0.0.1`, not `[::1]`.
[^acfwd]: A forward cannot work on `container` 1.1.0, measured: Apple Container carries no traffic from a container back to the Mac, and its own `--publish-socket host_path:container_path` creates the host socket and forwards a *host* connection inward to a container-side listener, the opposite of what this key needs. So the launch stops before anything starts, naming `network.forward_host_ports` and its entries; remove the key for this runtime, or launch with `YOLO_RUNTIME=podman`. Earlier releases passed the flag anyway, and the launch failed inside Apple Container with `Error: host socket <path> already exists and may be in use`. A published unix socket is nonetheless the one transport measured to cross this boundary at all, so it is the raw material for a fix rather than a dead end.
[^muports]: There is nothing to confine: a port the sandboxed agent binds is on the Mac's real interfaces whether you list it or not, and a launch prints one line per declared key saying so. A same-port entry needs nothing. A **remap** (differing numbers) is relayed while the command runs: yolo listens on the entry's address, or on every interface when it names none, and passes each connection to the port on the Mac's `127.0.0.1`. That publishes whatever listens there, the agent's service or one of yours, even one bound to `127.0.0.1` alone, which podman never does; the launch line says so, and an entry such as `127.0.0.1:8000:3000` keeps it on the Mac. TCP only: a `/udp` remap is named and not relayed. `network.mode: "host"` or `--network host` drops both keys, as on podman. Not yet tried on a Mac.
[^mufwd]: A same-port entry (`5432:5432`) is already satisfied — the sandbox is on the Mac's stack. A remap (`5432:3306`) is relayed while the command runs: yolo listens on the Mac's `127.0.0.1:5432` and passes each connection to `127.0.0.1:3306`, with no `socat` needed. A port something already holds is named with the command that finds the holder, and the relay takes it over once it frees (so a second terminal on the same workspace keeps the remap when the first closes). Not yet tried on a Mac.

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
| `mcp_presets` | works | works | works | absent, warns[^presetmac] | fresh launch |
| `mcp_servers` | works | works | works | works — `command` must exist on your Mac | fresh launch |
| `mcp_servers.requires_env` | works | works | works | works — the same per-agent check; not yet run on a Mac[^reqenv] | fresh launch |
| `lsp_servers` | works — config only, you bring the binary[^lsp] | works — config only, you bring the binary[^lsp] | works — config only, you bring the binary[^lsp] | works — config only, you bring the binary[^lsp] | fresh launch |
| `security.blocked_tools`[^hostblock] | works — blocker shims on PATH | works | works | works — but blocks the Mac's BSD tool[^bsd] | fresh launch |
| `packs` † | works | works — workspace must be VM-shared[^vm-store] | works — staged copy, never re-read | works — the launch says which surfaces it does not deliver[^packsmac] | fresh launch[^packspartial] |
| `providers` | works | works | works[^reentry] | works | any entry[^reentry] |
| `profiles` † | works | works | works[^reentry] | works[^profmac] | any entry[^reentry] |
| `profile` † / `-p <name>`, a list for pi[^profset] | works — refuses a `-p` it cannot honor | works | works[^reentry] | works[^profmac] | any entry[^reentry] |
| `agent_updates` † | works | works | works | works | fresh launch |
| `agent_updates` set to `"next-launch"` (pi's extension update in the background) | works[^nextlaunch] | works[^nextlaunch] | works[^nextlaunch] | works[^nextlaunch] | fresh launch |
| `env_sources` (dotenv files) | works | works | works[^reentry] | works — per-session, not editable in-jail | any entry[^reentry] |
| `nix build` usable inside the jail | works — host daemon, store read-only[^gcroot] | **absent, silent** — possible, not planned for now[^nixmac] | **absent, silent** — possible, not planned for now[^nixmac] | works — the host's own `nix`, through its daemon[^munix] | fresh launch |
| GNU behaviour of `sed`/`find`/`grep`/`tar` | works — GNU userland baked | works | works | BSD tools — GNU flags fail[^bsd] | fresh launch |
| Build toolchain (`cc`, `make`, `strace`) | works — baked | works | works | works only with Xcode CLT[^clt] | fresh launch |
| Browser for the chrome-devtools MCP | works — chromium baked | works | works — measured | absent — no browser wired | fresh launch |

Two things cut across the whole table. First, **`macos-user` has no re-entry**: every `yolo` invocation is a fresh sandbox, so every row above takes effect on your next command there, with no restart — the "fresh launch" answers cost you nothing on that column. Second, the container backends freeze most of this on the container command line at the **fresh** launch; re-entering a running jail returns before the config-change gate, so an edit you just made is not in the session you just joined. `yolo check` before you restart; a restart is what delivers.

**Where a provider's credential goes is user-scope too.** `packs`, `profiles`, `profile` and `agent_updates` are read from your user config only, and so is every provider field that decides where a credential goes: its `endpoints` (the address, and which protocols it answers), its `api_key_env_name` (which variable holds its key), and removing a provider with `null`. A workspace config that sets one is refused, naming the field, so a repository cannot redirect an agent's API traffic or send a provider a key it was not meant to get. A provider's `models`, `options`, `region` and `capabilities` merge from either scope. A `region` must be a region name such as `us-east-1` in any config, because agents build their Bedrock address from it.

[^builder]: A `packages:` entry forces a **Linux** image build. When the nix binary cache does not have a package, yolo starts a temporary Linux builder container for you; that needs the runtime running and your user trusted by the nix daemon. A build that fails stops the launch with nix's own error.

[^plat]: On a Mac running a *container* backend the jail is Linux, so `{"platforms": ["darwin"]}` means the package is **not** in your jail and `{"platforms": ["linux"]}` means it is. Nothing at launch names the entries it dropped.

[^storedel]: Delivering `packages:` from the host nix store — one image per machine instead of one per distinct package list — is a Linux-podman-only fast path today. On macOS the dial prints an "ignored" line and the launch bakes as normal, which costs a rebuild per distinct list. Not a ruled-out design; simply not built for these setups yet.

[^vm-store]: The podman machine has one fixed share set, chosen at `podman machine init`, and Apple Container has its own VM. Consequences: the mise tool store lives inside the VM (invisible from the Mac's filesystem, and lost to a `podman machine reset`), and a workspace or yolo state directory outside your home may fail to bind at all. yolo stops a launch that would bind a folder outside the share set and names the folder; check one yourself with `podman machine ssh -- test -d <folder> && echo shared`.

[^presetmac]: On `macos-user` a launch line names each preset that is not delivered, and no agent's MCP config gets an entry for it. Configure the server yourself in `mcp_servers` if you need it there. For Chrome DevTools the `chrome-devtools` pack delivers the server here, with the Mac's own Chrome.

[^reqenv]: On every setup a server gated on `requires_env` is written into the config of each agent whose environment will hold the variables it names, and left out of the others. The jail prints one line as it starts naming each server it left out and the variables that were missing. On `macos-user` the check reads the same per-agent values the launch writes for each agent before the sandbox starts.

[^lsp]: yolo installs no language server on any setup: each declared server reaches the agents that read one (Claude through a generated plugin, Copilot natively), and its `command` must already be on `PATH` — bring it with `mise_tools`, a pack program or an absolute path. Until 2026-09-25 three names (`python`, `typescript`, `go`) had install recipes; those are deleted, and nothing uninstalls what they installed earlier. On every setup the boot catalog reports that leftover as an orphan for `yolo programs remove` ([`mcp-configuration.md`](https://github.com/mschulkind-oss/yolo-jail/blob/main/docs/reference/mcp-configuration.md#binaries-are-the-users)).

[^bsd]: `macos-user` runs against your Mac's own userland, so `sed -i` eats the next argument, `find -printf` and `tar --wildcards` are unknown, and `grep -P` and `ls --color` error out — scripts that pass on the container backends fail here. `security.blocked_tools` also measures the *sandbox* PATH, so a block replaces the BSD tool. Homebrew's GNU builds (`brew --prefix coreutils`) are not on the sandbox PATH under their plain names.

[^hostblock]: `yolo host -- <program>` applies the same blocks, the `guardrails` pack's and your user config's list (never a project's), to the program it starts, and says so at every launch. A login shell that resets PATH, or an agent's own command of the same name, gets past them: Claude Code's built-in shell runs its own `grep` and `find`, in a jail too.

[^packsmac]: On `macos-user` a pack's skills and briefing are writable by the agent and are overwritten on the next launch, and pack-shipped MCP presets are not delivered. A pack-shipped loophole is **half** delivered, which is the precise version of what this note used to call "inert": its HOST daemon starts and its endpoint is delivered (with a per-file ACL grant for the sandbox account), and its `jail_daemon` — the in-jail half — runs inside the sandbox, confined by the same profile as the agent. The OpenAI and AWS credential helpers of yolo's own packs, and the same kind of helper in a pack you select by its path, are the exception: each launch runs them on the Mac, outside the sandbox, answering only requests that carry that launch's token, which any program in that sandbox can read. A few other kinds of in-jail half do not run there: one that intercepts a hostname (`claude-oauth-broker`'s TLS terminator, which needs a container), the wire bridge's (its host half runs instead), a pack service's that publishes an endpoint file, and a Linux program, or a downloaded jail program, named in its command. A program a pack ships in the loophole's folder runs there, from yolo's read-only copy of the pack. The launch says all of it rather than failing quietly: one line per host daemon it starts, one for the in-jail helpers it starts, and one `Declined:` line per in-jail half it will not run, with the reason.

[^packspartial]: On podman and Apple Container a running jail keeps the packs it started with. Re-entering it after you add, drop or edit a pack changes none of them, and yolo says which packs differ and that a restart picks them up.

[^reentry]: On Apple Container the jail's home is the workspace's own `.yolo/home`, and every entry, a re-entry included, rewrites the provider/profile/`env_sources` channel and each agent's own env file there before the command starts. Checked by tests; not yet run on a Mac. On podman the same files are live binds.

[^nextlaunch]: Pi starts without waiting, the update runs while you work, and the launch after it runs what it installed. On podman and Apple Container, a jail that exits while the update runs ends it and leaves its lock behind. For ten minutes every jail on the machine then skips pi's extension update, and a launch whose pi settings name a new extension first waits up to a minute. A launch after that runs the update again, and the project it stopped in says it did not finish. Checked by tests, which also run on macOS; not yet run in a jail on a Mac.

[^gcroot]: The image turns on `nix-command` and `flakes` in `/etc/nix/nix.conf`, so plain `nix build` and `nix shell` work with no flags. It needs a multi-user nix on the host, the kind with a nix daemon. An in-jail `nix build`'s result gets no durable garbage-collection root, so a host `nix-collect-garbage` can delete a store path a running jail is executing from, with no warning in either place.

[^nixmac]: In-jail nix on both Mac container backends is **possible, but not planned for now**. Use `packages:` and a restart, mise, or the `macos-user` backend instead. Reaching the host nix daemon from a jail on a Mac needs a store the jail can see holding *Linux* paths, and (on podman) a machine initialised with `-v /nix:/nix` — which cannot be added to an existing machine. Check with `command -v nix` and `podman machine inspect | grep -i /nix`. Do not force the store-view dial on: it replaces the view the jail's own binaries live in and the jail will not boot. By default the launch says nothing; the one line you may see prints only when `YOLO_NIX_HOST_DAEMON` is set without `YOLO_NIX_HOST_STORE_LINUX`. Apple Container has no opt-in: it never mounts the host's nix, whatever is set.

[^munix]: Each launch puts the host's own `nix` client — the one it just used to build the sandbox's tools — on the sandbox PATH, and sets `NIX_REMOTE=daemon` and a `NIX_CONFIG` that turns on `nix-command` and `flakes`. A `NIX_REMOTE` you set replaces yolo's. A `NIX_CONFIG` you set is kept, and yolo's `extra-experimental-features` line is added after it, unless yours already sets `experimental-features` or `extra-experimental-features`. It needs a multi-user nix; with a single-user one (no daemon socket) the sandbox gets no `nix`, and the launch prints `nix is not available inside the sandbox:` with the reason — so this row is never silent. Details: [nix inside the sandbox](https://github.com/mschulkind-oss/yolo-jail/blob/main/docs/reference/macos-user-nix-and-features.md#nix-inside-the-sandbox).

[^clt]: Without Xcode Command Line Tools there is no `cc` or `make` in the `macos-user` sandbox and yolo does not say so; with them installed, the Mac's own toolchain is what you get. Check with `xcode-select -p`. Linux-only tools such as `strace` are absent either way.

## Other keys

The keys the tables above leave out. Most of them do the same thing on every setup. When an edit to one reaches a running jail is covered in [When a change takes effect](configuration.md#when-a-change-takes-effect).

| Key | `podman` / Linux | `podman` / macOS | `container` / macOS | `macos-user` / macOS |
|---|---|---|---|---|
| `runtime` — which setup a launch uses | works | works — without it, yolo picks Apple Container when it is installed | works — the Mac default | works — only this key or `YOLO_RUNTIME` picks it |
| `confinement` — how confined the agent is | works — `jail`, the default, launches; `guest` and `host` refuse the launch and say what to use instead | works — the same | works — the same | works — the same |
| `required_capabilities` — what the jail must be able to do | works — a capability nothing in your config or its packs provides refuses the launch | works — the same | works — the same | works — the same |
| `adapters` — where an adapted provider is reached | works | works | works | works — except for a helper the launch starts itself, which uses the port that launch picked |
| `loopholes` — turn a pack's host service on or off | works | works — a Linux-only one is named and skipped[^lh-linux] | absent, warns — only the OpenAI login service starts, the jail cannot reach it, and the launch names each one it skips[^lh-ac] | works — every one starts, and the launch names each in-jail half the sandbox declines[^theone] |
| `brokered` — more repositories for the GitHub host service in one project | works | works | absent, warns — the GitHub host service does not start here, and the launch names it[^lh-ac] | works |
| `macos_log` — what the sandbox may read from the Mac's log | `n/a` — no macOS log | `n/a` — the jail is Linux and cannot read the Mac's log | `n/a` — the same | works |
| `agents_md_extra`, `briefing_provenance` — your text in the agent's briefing | works | works | works — a running jail keeps the briefing it started with | works |
| `include_if_found`, `prune`, `perf_logging`, `update_check` | works | works | works | works |

## What a running jail picks up when you run `yolo` again

Re-running `yolo` in a workspace whose jail is still running does **not** start a new jail — it *re-enters* the one you have (the banner says `Attaching to existing jail`). Settings passed when the container was created stay as they were, and so do its packs. Skills and briefings are rebuilt on every entry from the packs the jail started with, so you see the boot run again, yet some edits still wait for a restart. This table says which.

| What you changed | `podman` re-entry (Linux, macOS) | `container` re-entry | `macos-user` |
|---|---|---|---|
| Skills (built-in, pack, your own) | `partly`[^packcontent] | `partly`[^packcontent] | `works differently`[^macuserre] |
| The repository's own skills (`.claude/skills`, `.agents/skills`, …)[^wsskills] | `works` | `works` | `works differently`[^macuserre] |
| Briefings (`AGENTS.md`, `CLAUDE.md`), `agents_md_extra`, a filed handoff | `partly`[^packcontent] | **absent, silent** | `works differently`[^macuserre] |
| Adding, dropping or editing a pack | `absent, warns`[^packpartly] | `absent, warns`[^acpack] | `works` |
| Pack launch flags (`--yolo`, `--dangerously-skip-permissions`) | `works` — the flags of the packs the jail started with | `works` — the same | `works` |
| `-p <profile>` / a rotated key in `env_sources` | `works` — live env file | `works` — rewritten on every entry, not yet run on a Mac[^reentry] | `works differently` |
| `--with-credentials <provider>` | `refuses` unless the jail was launched with it; the refusal names `yolo stop` and the fresh launch | `refuses` the same, not yet run on a Mac | `works` — every invocation is its own launch |
| `mcp_servers`, `lsp_servers`, `mise_tools`, `blocked_tools` | **absent, silent**[^envhalf] | **absent, silent** | `works` |
| `resources`, `network`, `ports`, `mounts`, `devices`, `gpu`, `packages`, `host_files` | **absent, silent** | **absent, silent** | `works`[^macuserkeys] |
| The config-change diff prompt (your y/N review) | **absent, silent** | **absent, silent** | `works` — every invocation shows the diff |
| The frozen config snapshot `yolo config drift` compares against | **absent, silent**[^drift] | **absent, silent** | `works` — written at every launch; a session whose project was launched again since says so instead of comparing |
| The jail's own `yolo` version | `absent, warns` — one dim line naming `yolo stop`, or a restart question when the older jail cannot take what you selected[^olderjail] | `absent, warns` | `n/a` — re-staged every launch |

**On `macos-user` every `yolo` starts a fresh sandbox from your current config**, so every setting it reads takes effect on the next launch. What differs there is which settings it reads at all.

**To get a fresh jail:** `yolo stop`, then `yolo -- <cmd>`.

[^macuserre]: Rebuilt on every launch and copied into the sandbox home rather than mounted. The copy is **read-only to the agent**: the sandbox profile refuses edits, renames and deletes of the delivered skills and briefing, as every container backend's read-only mount does. Not yet measured on a Mac.
[^packcontent]: yolo's built-in skills, `agents_md_extra` and a filed handoff reach a re-entry. A pack's own skills and briefing text come from the packs the jail started with, so an edit to one, your local pack's included, waits for a restart, and the re-entry names the pack that changed.
[^wsskills]: Skills a repository keeps for one agent also reach the agents that do not read that directory, as the lowest layer, and are read again on every entry. They never replace yolo's own skills or a pack's in what yolo gives an agent, and the launch names each one held back and every symlink that points outside the repository. An agent that reads the repository's directory by itself still sees the repository's copy there, and the launch says when that copy shares a name with one of yolo's or a pack's skills. On your own machine, `yolo host -- <agent>` does something smaller: when the repository has none of the agent's own skills folders, it puts one link at the first of them, pointing at the first skills folder the repository keeps for another agent, and adds that link to `.gitignore` once. See [the workspace layer](https://github.com/mschulkind-oss/yolo-jail/blob/main/docs/reference/agent-briefings.md#the-workspace-layer).
[^packpartly]: A running jail keeps the packs it started with, whatever you add, drop or edit: their files, skills, briefing, hooks, launchers and launch flags. The re-entry says which packs differ and that `yolo stop`, then a new launch, picks them up. A profile only a newly added pack provides, or a jail whose packs this yolo cannot read (one an earlier release started with the claude pack, say), asks `Restart jail now? [Y/n]` at a terminal and is refused elsewhere; `YOLO_ALLOW_ATTACH_SKEW=1` re-enters without it.
[^acpack]: Apple Container gets a copy of the packs at launch, so the agent cannot rewrite them. As on podman, a running jail keeps the packs it started with, and a re-entry says which differ. A jail an earlier release started is the exception: yolo cannot tell which packs it copied, says so, and uses your configured ones. The restart it names is `yolo stop`, then a new launch. Not yet run on a Mac.
[^envhalf]: These cross as environment variables on the container command line. An added MCP server does not appear in the agent's list until you stop and relaunch, even though the boot visibly regenerated the MCP config.
[^olderjail]: A jail keeps the yolo it started with. When a newer yolo re-enters it with a profile whose credentials that jail cannot receive, yolo asks `Restart jail now? [Y/n]` and says how many sessions the restart ends. Without a terminal it stops and tells you to run `yolo stop` first. `YOLO_ALLOW_ATTACH_SKEW=1` re-enters anyway, without delivering the profile, and the agent's briefing then says which yolo the jail started with and what it did not receive (on podman; an Apple Container jail keeps the briefing it started with).
[^drift]: After a re-entry, in-jail config readers still see the config the jail was *launched* with, and `yolo config drift` compares against a baseline that may be several edits old — so it reports drift for edits you thought you had applied.
[^macuserkeys]: For the keys this setup reads. Resource limits are not kernel-enforced here, and a `mounts` entry is a link rather than a bind.

## What a pack can contribute, per setup

Every contribution kind is delivered on all four setups — config files and their overlays, autonomy postures, hooks, blocked-tool and intercept shims, skills trees, workspace and machine-scope state dirs, read-only host-file grants, providers, the `models` lists that shape them and the `adapter` conversions they route through, `mcp` server entries, `requires` assertions, briefings, `program` launchers[^capture] and the agent files a `program` names, such as Copilot's model list[^agentfiles] — **except these**:

| Contribution | podman/Linux | podman/macOS | container/macOS | macos-user/macOS |
|---|---|---|---|---|
| `env` — static vars | works | works | works — a pack edit needs a restart[^acfreeze] | works — carried in the launch env[^servedby] |
| `files` — a tree in the agent's home | works | works | works — read-only, but a single file arrives as a writable copy | works — protected copy[^files] |
| `program` with `patches` — a patched fork, built from its upstream and your patch series | works | works | works on 1.1.0+; a new build waits for the other jails to stop[^patchedac] | not delivered, and the launch says where it is[^patched] |
| `files` with `source` and `patches` — a patched pi extension | works — read-only copy | works — read-only copy | works on 1.1.0+, with the same wait; below 1.1.0 no new build, and a good build already on this Mac is copied in[^patchedac] | not delivered, and the launch says where it is[^patched] |
| `mount` — host dir read-only at `/ctx/<into>` | works | works | works — needs Apple Container 1.1.0+[^acver-mount] | a single file: copied at launch; a folder: refuses the launch[^mountmu] |
| `service` — an in-jail daemon (the wire bridge) | works | works | works | works — the wire bridge's host half runs for the launch that needs it; another service's daemon runs in the sandbox unless it publishes an endpoint file[^svc] |
| `profile` — a named `-p` selection | works | works | works — a pack edit needs a restart[^acfreeze] | works[^profmu] |
| `loophole` — a host service | works | works — the jail-to-Mac connection is tested[^machop] | absent, warns — one starts, and the jail cannot reach it[^theone] | works — every one starts, and the launch says which halves the sandbox declines; the loophole table says which are usable[^theone] |

On **macos-user** every launch is fresh, so a pack edit reaches your next command. On **podman** and **Apple Container** a running jail keeps the packs it started with: a pack edit arrives at the next fresh launch, and a re-entry says which packs differ. On Apple Container a fresh `.yolo/handover.md` waits for the restart too.[^acfreeze]

[^servedby]: An `env` contribution that declares `served_by` points a client at something a jail gets: a helper, such as codex's sign-in refresh address or the Bedrock credential address, or a socket a loophole mounts, such as the `audio` pack's `PULSE_SERVER`. macos-user runs those two helpers for each launch outside the sandbox, so it sets their variables, at the port that launch picked, and mounts nothing, so it leaves `audio`'s out. `yolo host -- <agent>` runs the helper the agent it starts needs (the Codex refresh helper for `codex`, the Bedrock credential helper for an agent on a Bedrock provider with `aws-auth` on) and leaves the rest out, `audio`'s included, so a program it starts finds your machine's own audio. A jail launch leaves one out only when it does not run that helper or mount that loophole, such as a loophole left disabled. Every launch names what it left out.
[^agentfiles]: An agent file is one yolo writes beside an agent's per-agent environment file, owner-only, on every launch and re-entry, and removes when the profile that needed it is no longer selected. `yolo host` writes none, so a program that would read one gets its environment alone there. See [Providers and Models](../guides/providers-and-models.md).

[^capture]: `program` launchers (a name on PATH plus a lazy installer that keeps the tool current) are delivered on all four. What Apple Container before 1.1.0 lacks is the install-capture store that pre-seeds those installs, so a first use downloads the vendor installer instead — slower, same result. On `macos-user` a program its vendor's installer installs is installed once per machine and every project reuses it. A program a fork builds from source (`via: "source"`) is built only on a container backend, so on `macos-user` and Apple Container before 1.1.0 it is not delivered, and the launch says so.
[^patched]: A patched fork or patched pi extension is checked against its upstream at most hourly, its patch series applied to the newest version it fits, and the result built in a sealed jail on this machine; see [Follow an Upstream with a Patch Series](../guides/patch-series.md). The build is a Linux one, so `macos-user` and `yolo host` on a Mac run neither, and say so, naming a container backend's launch that has it. `yolo host` on Linux builds and installs both: `yolo host -- <bin>` and `yolo host apply --assert` for a program, `yolo host apply --assert` for an extension, whose `~/<into>` becomes a link to a copy yolo keeps.
[^patchedac]: A build runs in a capture jail, which Apple Container cannot start beside a running jail. The good build runs meanwhile, and `yolo capture <bin>` (for an extension, `yolo capture <pack>/<name>`) builds it once the other jails stop. Below 1.1.0 a patched fork is not delivered at all, as for any fork.
[^acfreeze]: Apple Container renders pack surfaces from a per-launch copy of the pack tree rather than a live read-only bind, and the copy is only refreshed by a fresh launch. An edited pack therefore does not arrive until you restart the jail. A `-p <name>` selection and the `env` values it composes do reach a re-entry, since the channel files are rewritten on every entry[^reentry], but they are rendered through that launch's copy of each pack. Restart the jail after any pack edit on this backend.
[^files]: Copied into the sandbox home at every launch, and the sandbox profile denies the agent writes to it, as the read-only mount does on the container backends. A tree whose source the pack does not ship is skipped with the same warning a container launch prints. Not yet run on a Mac.
[^acver-mount]: Read your own value with `container --version`. From 1.1.0 read-only binds are honored and this works; below it, or if the version cannot be read, the mount is skipped with a yellow line and `/ctx/<into>` does not exist. Closing the below-floor case is unbuilt work, not a permanent limit.
[^mountmu]: On macos-user a pack's `mount` of a single FILE is copied into `$YOLO_CONTEXT_DIR` at launch, read-only, and the agent's briefing says a host edit arrives at the next launch. A `mount` of a FOLDER is delivered the way a `mounts` entry is, as a link the sandbox decides access to, but its folder is in your home (`~/<from>`), and this setup links only folders outside every home. So a selected pack's folder `mount` whose folder exists stops the launch, naming the pack and the home its folder is in; a source that does not exist is skipped with a warning. Not yet tried on a Mac.
[^svc]: The wire bridge is a daemon that some providers route through; the `cerebras` pack pulls it in automatically when `claude` or `copilot` is selected, and `claude` always brings it. On macos-user its in-jail daemon does not run (the launch's `Declined:` line says its host half runs instead), so a launch whose profile needs the bridge, such as `-p cerebras -- claude`, starts the bridge's host half outside the sandbox, on a port that launch picked, and stops it when the launch ends (checked by tests, not yet run on a Mac). `yolo host` does the same for its one command. A profile that sends an agent through the bridge, such as `bedrock-bridge`, or plain `bedrock` for copilot, which has no Bedrock client of its own, starts it the same way. At `yolo host` that holds for an agent that reads the bridge's address from its environment (claude, copilot); pi, opencode, oh-omp and codex read it from their own config files, which `yolo host` does not write per launch, so there they keep their own clients and the launch says why. macos-user writes those files per launch, so it starts the bridge for them too. A pack of your own, selected by its path, runs its service the same way. One fetched from a git repository runs only its in-jail half: in a container jail, and on macos-user inside the sandbox (checked by tests, not yet run on a Mac), unless it publishes an endpoint file, in which case the launch names a container backend to run it. `yolo host` runs none of it. A service that stops while the agent runs is started again on the same address. A pack service no provider routes through starts beside the command at `yolo host --`, and on macos-user when it has no in-jail half. Also note on every container setup: the daemon set is fixed at the fresh launch, so a newly selected service pack needs a restart.
[^profmac]: The agent config files that depend on the selected profile are written from it, as in a container. The profile's variables reach only the program the invocation starts ([^cap-mu-keys]). Not yet run on a Mac.
[^profset]: A list of profiles, `-p pi=zai,openrouter` or `"profile": {"pi": ["zai", "openrouter"]}`, puts pi or opencode on every listed provider at once, on every setup; see [Several providers in one session](../guides/providers-and-models.md#several-providers-in-one-session). A list for Claude Code, Codex, Copilot or oh-omp is refused. Entering a running jail that an older yolo started, with a list, asks to restart it first. Checked by tests; no pi or opencode session has been run on one.
[^profmu]: On macos-user the profile's variables reach the program the invocation starts, and the config surfaces that depend on the selected profile render from it, as in a container. Not yet run on a Mac.
[^machop]: The jail reaches your Mac's services through the Podman Machine VM at `host.containers.internal`, and the macOS nightly dials a host service from inside a real jail. The services themselves have not been run end to end on a Mac. If one cannot be reached, the launch warns but still starts; you meet it as `yolo-serial` or a login failing at runtime.
[^theone]: See [the loophole table](settings-per-setup.md#the-loopholes-host-services-a-jail-can-use). On Apple Container only the OpenAI login service is started, and the jail cannot reach it; every other loophole prints one yellow line per launch naming the backend and the reason. On `macos-user` every host service starts and the launch discloses each one, and the in-jail halves run inside the sandbox, except the few it declines with one `Declined:` line each. A started service the sandbox then cannot use stops the launch, as on rootless podman (`YOLO_ALLOW_UNREACHABLE_SERVICES=1` continues); not yet run on a Mac.
