# Getting Started

yolo sets up the environment an AI coding agent works in: the agent, its settings, skills and tools,
and what it may reach on your machine. By default it runs the agent in a **jail**, an isolated Linux
container that keeps your host's SSH keys and cloud logins out of reach. To get there you install three
things on your computer, then pick an agent and launch it.

## Quick install

You need **Nix**, a reproducible package builder that builds the jail; a **container runtime**, the
program that runs the jail; and **yolo** itself. On a Mac the recommended runtime is
[Apple Container](https://github.com/apple/container), which needs an Apple silicon Mac on macOS 26
(Tahoe) or later. On Linux it is [Podman](https://podman.io/), run as your own user.

**Mac with Apple silicon**, with [Homebrew](https://brew.sh/) installed:

```bash
# 1. Nix, trusting your user (required on a Mac) — then open a new terminal
curl -sSfL https://artifacts.nixos.org/nix-installer | sh -s -- install --extra-conf "extra-trusted-users = $(whoami)"
# 2. Apple Container — answer Y when it offers to install a kernel
brew install container
container system start
# 3. yolo
brew install mschulkind-oss/tap/yolo-jail
yolo check
```

**Linux** (Ubuntu or Debian shown; [Step 2](#linux-podman) has Fedora and Arch), with
[Homebrew](#homebrew) installed:

```bash
# 1. Nix, trusting your user (optional on Linux) — then open a new terminal
curl -sSfL https://artifacts.nixos.org/nix-installer | sh -s -- install --extra-conf "extra-trusted-users = $(whoami)"
# 2. Podman
sudo apt-get update && sudo apt-get install -y podman passt slirp4netns uidmap
# 3. yolo
brew install mschulkind-oss/tap/yolo-jail
yolo check
```

The Nix installer asks for your password itself, so do not put `sudo` in front of it. Pressing
Enter at each of its questions is fine.

`yolo check` ends with a Summary line. A few `[WARN]` rows are expected at this point, including
`No packs are configured`, because you have not chosen an agent yet; any `[FAIL]` row names its fix.
[What the rows mean →](#check-the-setup) Its first run also builds the jail's image, which can take
many minutes.

Then [choose an agent and launch it →](#step-4-first-launch)

The steps below explain each command, and cover Intel Macs, older macOS, other Linux distributions and
other ways to install yolo.

## What you need

| | Mac, Apple silicon | Mac, Intel | Linux (x86_64 or arm64) |
|---|---|---|---|
| **Nix** | [NixOS Nix installer](#step-1-install-nix) | [Install script on nixos.org](#other-ways-to-get-nix) | [NixOS Nix installer](#step-1-install-nix) |
| **Container runtime** | [Apple Container](#macos-apple-container-recommended) on macOS 26 or later; [Podman](#macos-podman) otherwise | [Podman](#macos-podman) | [Podman](#linux-podman), rootless |
| **yolo** | [Homebrew](#homebrew) | A [release archive](#other-ways-to-install), or [Homebrew](#homebrew) | [Homebrew](#homebrew) or a [release archive](#other-ways-to-install) |

On a Mac you have three ways to run the jail, and yolo supports all three:

- **Apple Container** is the recommended one today. Each jail runs in its own small Linux virtual
  machine (VM), and there is no VM for you to manage.
- **Podman** runs every jail inside one Linux VM called the Podman Machine. It supports more of yolo's
  features than Apple Container, and it is the container runtime to use on an Intel Mac or on macOS
  before 26.
- **`macos-user`** is coming, but it is not the recommended setup yet. It runs the agent as a hidden
  macOS user inside Apple's built-in sandbox, with no container and no VM. It will become the main Mac
  setup once it is finished. [About `macos-user` →](guides/macos.md#the-macos-user-backend)

> [!NOTE]
> **Intel Macs work today, but support is ending.** Apple Container, Podman 6 and the NixOS Nix
> installer have all dropped Intel Macs, and the Nix packages yolo uses on an Intel Mac stop receiving
> fixes at the end of 2026. Homebrew no longer builds packages for Intel Macs either, so a Homebrew
> install there can compile yolo and its Go toolchain from source; the
> [release archive](#other-ways-to-install) avoids that. Plan on an Apple silicon Mac.

## Step 1: Install Nix

yolo uses Nix to build the jail's contents, so every setup needs it. Install it with the
[NixOS Nix installer](https://github.com/NixOS/nix-installer), the official installer the NixOS
community maintains. It works on Apple silicon Macs and on x86_64 and arm64 Linux, keeps Nix working
across macOS updates, supports Fedora's SELinux, and uninstalls cleanly. On an Intel Mac, see
[Other ways to get Nix](#other-ways-to-get-nix).

**Mac with Apple silicon.** A Mac requires the Nix daemon to trust your user, so the command does
that too ([why](#trust-your-user-required-on-a-mac)):

```bash
curl -sSfL https://artifacts.nixos.org/nix-installer | sh -s -- install --extra-conf "extra-trusted-users = $(whoami)"
```

It needs macOS 14 (Sonoma) or later, and it will not run in a terminal that runs under Rosetta,
Apple's translator for Intel programs.

**Linux** with systemd, the service manager most distributions use, such as Ubuntu, Debian and
Fedora. Trusting your user is optional on Linux, and the second command does it in the same step:

```bash
curl -sSfL https://artifacts.nixos.org/nix-installer | sh -s -- install
# or, trusting your user too:
curl -sSfL https://artifacts.nixos.org/nix-installer | sh -s -- install --extra-conf "extra-trusted-users = $(whoami)"
```

It stops on a system without systemd, on NixOS (see [Other ways to get Nix](#other-ways-to-get-nix)),
on WSL 1 (the first version of Windows' Linux layer), and where Nix is already installed. If yours
is, keep that Nix and go on to [Trust your user](#trust-your-user-required-on-a-mac). With SELinux, the security layer Fedora turns on by
default, it needs the `semodule` and `restorecon` commands, and installs an SELinux policy for Nix.

Run the installer as yourself, without `sudo`: it asks for your password when it needs it, and asks
you to confirm before it changes anything. Pressing Enter at each of its questions is fine. Open a
new terminal when it finishes, so `nix` is on your PATH.

### Trust your user (required on a Mac)

The jail is Linux, and a Mac cannot build Linux programs itself. On a Mac, the parts of the jail
that are not in Nix's public cache come from yolo's own binary cache, or are built in a temporary
Linux container, and the **Nix daemon** (the background service that does Nix's builds) allows
both only for a user it trusts. Without this step the first launch on a Mac fails. On Linux it is
optional and only saves building those parts yourself the first time.

The install commands above that carry `--extra-conf` do this for you. If you installed without it,
or with the [install script on nixos.org](#other-ways-to-get-nix), add the line yourself and restart
the daemon:

```bash
echo "extra-trusted-users = $(whoami)" | sudo tee -a /etc/nix/nix.conf
sudo launchctl kickstart -k system/org.nixos.nix-daemon   # macOS
sudo systemctl restart nix-daemon                         # Linux
```

`extra-trusted-users` adds you to the users the daemon already trusts, rather than replacing them.

A trusted user can change how the daemon builds, which the
[Nix manual](https://nix.dev/manual/nix/latest/command-ref/conf-file) calls essentially equivalent to
root access. Add only your own account, on a computer you control.

### Check that Nix works

In a new terminal:

```bash
nix --version    # prints "nix (Nix)" and a version number
```

Once yolo is installed in [Step 3](#step-3-install-yolo), `yolo check` checks the rest: that the
Nix daemon answers, and on a Mac that it trusts you, shown as
`Nix daemon: connected, user is trusted`.

### Other ways to get Nix

- **Intel Mac.** The NixOS Nix installer has no build for Intel Macs, so use the
  [install script on nixos.org](https://nixos.org/download/). On a Mac it sets up the Nix daemon
  by default. Then trust yourself and restart the daemon:

  ```bash
  curl --proto '=https' --tlsv1.2 -L https://nixos.org/nix/install | sh
  echo "extra-trusted-users = $(whoami)" | sudo tee -a /etc/nix/nix.conf
  sudo launchctl kickstart -k system/org.nixos.nix-daemon
  ```

  Open a new terminal afterwards. This script has no uninstaller; see [Uninstall](#uninstall).
- **Ubuntu's and Debian's own `nix-bin` package.** It is far behind on Ubuntu 24.04 (Nix 2.18), so
  use the NixOS Nix installer above instead.
- **Arch Linux.** Arch ships Nix as a regular package. Install it and start its daemon; the last two
  lines trust your user, which is optional on Linux:

  ```bash
  sudo pacman -S nix
  sudo systemctl enable --now nix-daemon.service
  echo "extra-trusted-users = $(whoami)" | sudo tee -a /etc/nix/nix.conf
  sudo systemctl restart nix-daemon
  ```

  Older guides also add you to a `nix-users` group; the current package has none, so skip that. You
  need no Nix channel either. See the [ArchWiki](https://wiki.archlinux.org/title/Nix).
- **NixOS.** Nix is already installed. Add these to `/etc/nixos/configuration.nix`, then run
  `sudo nixos-rebuild switch`. The first line trusts your user, which is optional on Linux; the
  second is Step 2:

  ```nix
  nix.settings.trusted-users = [ "root" "your-username" ];
  virtualisation.podman.enable = true;
  ```

  See the NixOS wiki on [Podman](https://wiki.nixos.org/wiki/Podman).

## Step 2: Install a container runtime

### macOS: Apple Container (recommended)

Apple Container needs an **Apple silicon Mac running macOS 26 (Tahoe) or later**; Apple does not
support it on older macOS. Check yours with `sw_vers -productVersion`. On an older Mac, use
[Podman](#macos-podman).

```bash
brew install container
container system start    # the first time, answer Y to install the recommended Linux kernel
container system status   # says it is running
```

If you skipped the kernel prompt, or start the service with `brew services`, install the kernel
yourself with `container system kernel set --recommended`. Apple also offers a signed installer
package on its [releases page](https://github.com/apple/container/releases). yolo needs
`container` 1.1.0 or later for read-only folders; check with `container --version`. After a restart,
if yolo says the runtime is not running, run `container system start` again.

> [!IMPORTANT]
> **Apple Container does not run yolo's host services yet.** A host service is a helper yolo runs on
> your Mac for the jail, such as the one that keeps a shared Claude login fresh. So on this runtime,
> two jails sharing one Claude login can log each other out, `codex` and `pi` cannot use a ChatGPT
> subscription login, and published ports do not work. If you need any of these, use Podman.
> [What Apple Container does not do →](guides/macos.md#apple-container-runtime-container--what-it-does-not-do)

### macOS: Podman

Choose Podman on an Intel Mac, on macOS before 26, or when you need a feature Apple Container lacks.

- **Apple silicon:** `brew install podman`. Podman's own
  [installer package](https://podman.io/docs/installation) also works, and is the one Podman
  recommends.
- **Intel:** Podman 6 no longer supports Intel Macs, and Homebrew has no Intel Mac build of it. Install
  `podman-installer-macos-amd64.pkg` from the newest 5.8 release on
  [Podman's releases page](https://github.com/containers/podman/releases).

Then create and start the **Podman Machine**, the Linux VM your jails run in:

```bash
podman machine init --cpus 4 --memory 8192 --disk-size 50 \
  -v /Users:/Users -v /private:/private -v /var/folders:/var/folders \
  -v "$(brew --prefix)/Cellar:$(brew --prefix)/Cellar"
podman machine start
podman info                                            # answers once the machine is up
podman machine ssh -- test -d "$(brew --prefix)/Cellar" && echo shared
```

Each `-v` names a Mac folder the machine can see. The list replaces Podman's defaults rather than
adding to them, so it repeats them (`/Users`, `/private`, `/var/folders`) and adds Homebrew's
`Cellar` folder, where a Homebrew install of yolo keeps the programs it hands to each jail. The list
can be set only when the machine is created. If you did not install yolo with Homebrew, leave out the
last `-v` and the last line. `yolo check` warns if the machine has less than 4096 MB of memory, and
fails if it does not share your project or yolo's own files.

If yolo later says the runtime is not running, run `podman machine start`. If Apple Container is
installed and running too, yolo picks it; set `YOLO_RUNTIME=podman` or `"runtime": "podman"` to use
Podman.

### macOS: the macos-user sandbox (coming)

`macos-user` needs no container runtime at all: only Nix, with your user trusted, and a one-time
`yolo macos-setup` that creates the hidden user. It is still in development, and several agents have
not been tried on it yet. [Set up `macos-user` →](guides/macos.md#the-macos-user-backend)

### Linux: Podman

Install Podman from your distribution, together with the network helpers yolo uses:

```bash
sudo apt-get update && sudo apt-get install -y podman passt slirp4netns uidmap   # Ubuntu, Debian
sudo dnf install -y podman                                                        # Fedora
sudo pacman -S podman                                                             # Arch
```

On NixOS, set `virtualisation.podman.enable = true;` (see [Other ways to get Nix](#other-ways-to-get-nix)).

**Run Podman, and yolo, as your normal user, never with `sudo`.** This is called **rootless** Podman.
A jail on a Podman run as root cannot reach yolo's host services, such as the helpers that keep shared
logins fresh. Check that your setup is rootless and that your account has a range of user IDs
for containers:

```bash
podman info --format '{{.Host.Security.Rootless}}'   # prints true
grep "^$(whoami):" /etc/subuid /etc/subgid           # prints one line for each file
```

If `grep` prints nothing, add a range, then let Podman pick it up:

```bash
sudo usermod --add-subuids 100000-165535 --add-subgids 100000-165535 "$(whoami)"
podman system migrate
```

Podman 5.1 or later works best; check with `podman --version`. Ubuntu 26.04 and Debian 13 ship
Podman 5, so they are the smoothest. Ubuntu 24.04 ships Podman 4.9, and yolo works there too: it falls
back to a slower network helper and says so when a jail starts. Older versions, such as Debian 12's
Podman 4.3, are untested. Nothing needs configuring for the AppArmor user-namespace restriction on
Ubuntu 24.04 and later, which yolo already works around.

### Which runtime yolo picks

| Setup | When yolo uses it |
|---|---|
| `podman` / Linux | Always, on Linux |
| `container` / macOS | When Apple Container is installed and running |
| `podman` / macOS | Otherwise, when Podman is installed and its machine is running |
| `macos-user` / macOS | Only when you choose it |

If no installed runtime is running, yolo stops before building anything and prints the command that
starts each one. To choose yourself, set the `runtime` config key or the `YOLO_RUNTIME` environment
variable to `podman`, `container` or `macos-user`. `command -v container podman` shows which runtimes
are installed.

## Step 3: Install yolo

### Homebrew

[Homebrew](https://brew.sh/) is a package manager for macOS and Linux, and the easiest way to install
and upgrade yolo on both:

```bash
brew install mschulkind-oss/tap/yolo-jail
yolo --version
```

The formula comes from yolo's own
[Homebrew tap](https://github.com/mschulkind-oss/homebrew-tap), a third-party package list, and builds
yolo from source, so the install can take a few minutes. It installs yolo only: Nix and the runtime come
from steps 1 and 2.

> [!NOTE]
> Homebrew and the release archives install yolo's latest release. Two commands this guide uses,
> `yolo host-daemon` and `yolo openai-auth`, arrive in the release after 0.10.0. If `yolo --version`
> says 0.10.0, use `yolo broker status`, `stop` and `restart` for the shared Claude login service,
> and `yolo internal openai-auth` in place of `yolo openai-auth`.

**Homebrew on a Mac:** install it from [brew.sh](https://brew.sh/), then follow the "Next steps" it
prints to add `brew` to your PATH.

**Homebrew on Linux** needs a compiler and a few tools first, then a line in your shell profile. See
[Homebrew on Linux](https://docs.brew.sh/Homebrew-on-Linux) for details:

```bash
sudo apt-get install -y build-essential procps curl file git             # Ubuntu, Debian
# Fedora: sudo dnf group install development-tools && sudo dnf install -y procps-ng curl file
# Arch:   sudo pacman -S base-devel procps-ng curl file git
/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
echo 'eval "$(/home/linuxbrew/.linuxbrew/bin/brew shellenv)"' >> ~/.bashrc   # ~/.zshrc for zsh
eval "$(/home/linuxbrew/.linuxbrew/bin/brew shellenv)"
```

### From source

For working on yolo itself, or running an unreleased version. You need `git`, plus Go (the version
[`go.mod`](https://github.com/mschulkind-oss/yolo-jail/blob/main/go.mod) names) and
[just](https://github.com/casey/just), a command runner. If you have both:

```bash
git clone https://github.com/mschulkind-oss/yolo-jail.git
cd yolo-jail
just setup
just deploy     # builds and installs yolo, together with its build files
```

If you have neither, [mise](https://mise.jdx.dev/getting-started.html), a tool-version manager, can
install the versions the repository pins. `mise trust` tells mise it may read the clone's
`mise.toml`, which it will not do for a new folder without asking:

```bash
git clone https://github.com/mschulkind-oss/yolo-jail.git
cd yolo-jail
mise trust
mise install              # the pinned Go and just
mise exec -- just setup
mise exec -- just deploy
```

`mise exec --` runs a command with mise's tools on its PATH. If you have
[activated mise in your shell](https://mise.jdx.dev/getting-started.html#activate-mise), plain
`just setup` and `just deploy` work instead.

`just deploy` installs `yolo` into Go's bin directory, `$(go env GOPATH)/bin` unless you set `GOBIN`,
so make sure that directory is on your PATH.

### Other ways to install

yolo builds each jail from its **build files**: a Nix recipe (`flake.nix`, the "flake"), its lockfile,
and the Linux programs that run inside the jail. It looks for them next to its own binary, never in
your current directory, and every launch prints which copy it used on a `Flake source:` line.

| How you installed yolo | Works on its own? |
|---|---|
| Homebrew | Yes |
| From source, with `just deploy` or `just install` | Yes |
| GitHub release archive | Yes, if you keep `yolo` and its `share/` folder together |
| `go install github.com/mschulkind-oss/yolo-jail/cmd/yolo@latest` | No: installs the binary only |
| `pipx install yolo-jail`, or `uvx --from yolo-jail yolo` | No: installs the binary only |

**Release archive.** Download `yolo-jail_<version>_<os>_<arch>.tar.gz` for your system from the
[releases page](https://github.com/mschulkind-oss/yolo-jail/releases), unpack it into a folder of its
own, and add that folder to your PATH:

```bash
mkdir -p ~/.local/opt/yolo-jail
tar -xzf yolo-jail_*_linux_amd64.tar.gz -C ~/.local/opt/yolo-jail
export PATH="$HOME/.local/opt/yolo-jail:$PATH"   # add this line to your shell profile too
```

The archive holds `yolo` and a `share/` folder beside it. Keep them together: move the whole folder,
never `yolo` alone.

**Binary-only channels.** With `go install`, pipx or uvx, the first launch stops with
"Cannot find yolo-jail repo root". Either switch to a channel marked Yes, or clone the repository and
point yolo at the clone with `export YOLO_REPO_ROOT=/path/to/yolo-jail`, in your shell profile to keep
it. Running `yolo` from inside the clone is not enough on its own. On a Mac with Podman, a clone named
this way is also refused unless the Podman Machine was created sharing `/nix`; see
[the live-checkout rule](guides/macos.md#running-a-live-checkout-on-podman).

## Step 4: First launch

### Choose an agent

No agent is selected by default, so first choose one. An agent arrives as a **pack**, an add-on you
list in your user config at `~/.config/yolo-jail/config.jsonc`. Create the file, then add the pack
line inside its outer braces:

```bash
yolo init-user-config    # writes the file, full of commented examples
```

```jsonc
{
  "packs": ["claude"],
  // …the commented examples the file already holds…
}
```

The shipped agent packs are `claude`, `copilot`, `codex`, `opencode`, `pi`, `agy` and `omp`; list as
many as you like. `omp`'s vendor publishes no ARM Linux build, so it does not run in a jail on an Apple
silicon Mac. An agent installs inside the jail the first time you run it. Only your user config can
select packs; a project's own config cannot. [How packs work →](guides/packs-and-skills.md)

### Check the setup

```bash
cd ~/code/my-project     # any repository
yolo check
```

`yolo check` looks at Nix, the runtime, your config and the jail's build. Each row starts with
`[PASS]`, `[WARN]` or `[FAIL]`, and the run ends with a Summary line. **Fix every `[FAIL]`**: a
failure stops the check before it builds anything, and the row's note names the fix. The first run
also builds the jail's image, which takes a while; `yolo check --no-build` is the fast version.
Optionally, `yolo init` adds a commented `yolo-jail.jsonc` to the project for its own settings; you do
not need one to start.

A healthy fresh machine still shows a few warnings. These are expected:

| Warning | What it means | What to do |
|---|---|---|
| `No '…yolo-jail' image loaded` | The check builds the image, but only a launch loads it into the runtime | Nothing: your first launch loads it |
| `nix min-free = 0 — the daemon's automatic GC is OFF` | Nix never cleans up its own store unless you set a floor | Optional: add the `min-free` and `max-free` lines the note shows, sized for your disk |
| `loophole claude-oauth-broker: … not yet generated`, or `… does not exist — run Claude and /login first` | The helper that keeps a shared Claude login fresh has not run yet | Nothing: it sets itself up on your first launch and login |
| `Skipped nix build (--no-build)` | You ran the fast check | Nothing |
| On a Mac, with yolo 0.10.0: `No OCI conversion tool for Apple Container` | An outdated check, removed in the release after 0.10.0: yolo builds its own image copier and uses nothing on your PATH | Ignore it; you do not need skopeo |
| On a Mac: `Podman Machine: not configured` | Podman is installed, but you use Apple Container | Ignore it |
| On a Mac: `A package must be built from source …` | Part of the image is in no binary cache | Keep the runtime running: the launch builds it in a temporary container |

And these need fixing before you launch:

| Row | Fix |
|---|---|
| `nix not found` | [Step 1](#step-1-install-nix), then open a new terminal |
| On a Mac: `Nix daemon: connection failed` or `Nix daemon: store operation timed out` | The daemon did not answer; the rest of the row is Nix's own error. Restart it with the command the note names, usually `sudo launchctl kickstart -k system/org.nixos.nix-daemon` |
| On a Mac: `Nix daemon: connected but user is NOT trusted` | [Trust your user](#trust-your-user-required-on-a-mac). It is only a `[WARN]`, but the first launch needs it |
| On Linux: `Nix daemon: store operation timed out` or `Nix daemon: connection failed` | Restart the daemon with the command the note names, usually `sudo systemctl restart nix-daemon`. The launch builds its image through it |
| `No container runtime installed`, or `… installed but not started` | [Step 2](#step-2-install-a-container-runtime), or the start command the note names |
| `… installed but not answering` | The runtime did not answer within a minute: it may be busy or still starting. On a Mac, `podman machine list` shows whether the machine is running; wait for it, then run `yolo check` again, and restart it if it still does not answer. On Linux, `podman info` shows why |
| `No packs are configured, so this jail has no coding agent` | [Choose an agent](#choose-an-agent) |

Run `yolo check` again after every edit to your config.

### Launch

```bash
yolo -- claude
```

yolo starts the jail with your project mounted read-write at `/workspace`, then runs Claude Code in
it with its permission prompts turned off, since the jail is the safety boundary. Plain `yolo` gives
you a shell in the jail instead. Before it starts, the launch lists what each pack reads from your
machine, such as your own Claude `settings.json`; that list is expected.

The first time, Claude Code asks you to log in with `/login`. The login is kept on your machine, so
every jail after this one uses it too; see [Authentication](#authentication).

The first launch is slow: it builds the jail image and copies it into the runtime, which can take
many minutes, longest on a Mac. After that, launches take seconds, and running `yolo` again in the
same project joins the jail that is already running.

### What the first launch does

1. **Builds the Linux image with Nix.** Most of it downloads from Nix's public binary cache and
   yolo's own. On a Mac, anything in neither cache, such as a package you add under `packages`, is
   built in a temporary Linux container on your running runtime, which is removed afterwards. Never
   set `extra-platforms` to a Linux system in your Nix config: it makes Nix try to run Linux programs
   on the Mac, and `yolo check` warns if it is set.
   [Building the image on macOS →](guides/macos.md#building-the-image-on-macos-cache-vs-linux-builder)
2. **Copies the image into your runtime.** yolo sends only the layers the runtime does not already
   have, so a later upgrade moves only what changed.
3. **Installs the agent and its tools** into storage that survives restarts. Language servers are not
   installed for you; see [LSP servers](guides/mcp-and-lsp.md#lsp-servers).
4. **Runs your command**, or a shell if you gave none.

### What to do next

- **Add the tools your project needs**: [Packages and Tools](guides/packages-and-tools.md).
- **Give your agents shared skills and house rules**: [Packs and Skills](guides/packs-and-skills.md).
- **Open a dev server or reach a host database**: [Networking](guides/networking.md).
- **Use another model provider or an API key**: [Providers and Models](guides/providers-and-models.md).
- **Apply the same setup to your own machine**, with no jail:
  [Writing Your Own Pack](guides/migrating-to-packs.md#part-2--manage-your-host).

---

## Authentication

Log in to each tool inside the jail, the way you would on a new computer:

```bash
claude                 # Claude Code: runs /login the first time
codex                  # Codex: prints a browser link the first time
gh auth login          # the GitHub CLI
```

The jail does not reuse the logins on your host, and every login you make in it is kept on your
host, so you log in once, not at every launch. Claude's and Antigravity's (`agy`) logins, and the
ChatGPT login Codex and pi share, then work in every project on the machine; the others, such as
`copilot`, `opencode`, `omp` and `gh`, are kept per project.

On Podman, a service on your host keeps the shared Claude login fresh, so several jails at once do
not log each other out. On Apple Container that service and the shared ChatGPT login do not work
yet. [Logins →](guides/authentication.md) covers each agent, API keys, and pushing to git from a
jail.

---

## If something goes wrong

Start with `yolo check`: it names the fix for most setup problems. A few that come up while installing:

- **"Cannot find yolo-jail repo root"**: yolo was installed without its build files. See
  [Other ways to install](#other-ways-to-install).
- **On a Mac, `Nix daemon: connection failed`**: the Nix daemon did not answer, and the rest of the
  row is Nix's own error. Restart the daemon as in [Trust your user](#trust-your-user-required-on-a-mac).
- **On a Mac with Podman, `statfs …: no such file or directory` when a jail starts**: the Podman
  Machine cannot see that folder. Recreate the machine with the share list in
  [macOS: Podman](#macos-podman) (`podman machine rm`, then `init` and `start` again).
- **A build that fails stops the launch** and shows Nix's own error. yolo never falls back to an older
  image.

[Troubleshooting →](guides/troubleshooting.md) covers more, and the [macOS guide](guides/macos.md)
covers Mac-specific problems.

## Upgrade

```bash
yolo update --check     # is a newer yolo out?
yolo update             # install it the same way yolo was installed
yolo host-daemon status # restart any it reports unhealthy: yolo host-daemon restart <name>
```

`yolo update` runs `brew upgrade yolo-jail` for a Homebrew install. For a from-source install it
pulls the clone yolo was built from and runs `just deploy` there. For a release archive it prints the
download link: unpack the new archive over the old folder, after exiting your running jails, because
that replaces the programs they were started from. With `go install`, pipx or uvx, update the binary
and the clone `YOLO_REPO_ROOT` names together.

From source, `yolo update --autostash` sets aside changes you have not committed and puts them back
afterwards. If you moved or re-cloned the repository, `yolo update --from <clone>` updates from the
new place. Both work only for a from-source install.

A jail that is already running keeps working through a Homebrew or from-source update, and uses the
new version once you exit it and start it again. From source, `just deploy` restarts the shared
Claude login service, so a running jail loses it for a moment.

yolo checks for a new release once a day and prints one line when there is one, and it tells you the
first time, before it checks. A from-source install is checked only when you run `yolo update`. When
an update is waiting and you start a jail, yolo offers to install it and start again. To turn the
check, the line and the offer off, add `"update_check": false` to `~/.config/yolo-jail/config.jsonc`.

On yolo 0.10.0, which has neither `yolo update` nor `yolo host-daemon`, run `brew upgrade yolo-jail`,
or `git pull && just deploy` in your clone, and then `yolo broker restart`.

## Uninstall

1. **Exit every jail**, then stop yolo's host services: `yolo host-daemon status` lists them, and
   `yolo host-daemon stop <name>` stops one (on yolo 0.10.0, `yolo broker stop`). If you set up
   `macos-user`, run `yolo macos-teardown`.
2. **Remove yolo:** `brew uninstall yolo-jail`, or delete the release archive's folder, or remove
   `yolo` from Go's bin directory.
3. **Remove what yolo stored:** `rm -rf ~/.local/share/yolo-jail ~/.config/yolo-jail`. This deletes
   the logins your jails kept and your user config. Each project also has a `.yolo/` folder, and a
   `yolo-jail.jsonc` if you ran `yolo init`. On Linux, if `rm` reports permission errors, run it as
   `podman unshare rm -rf …`.
4. **Remove the jail images** from your runtime: `podman images` or `container image list` shows
   them, named `yolo-jail`.
5. **Remove the runtime and Nix** if nothing else uses them. For Apple Container, `brew uninstall
   container`; for Podman on a Mac, `podman machine rm`, then uninstall Podman. Nix from the NixOS
   Nix installer uninstalls with `/nix/nix-installer uninstall`, which asks for your password
   itself. The install script on nixos.org has no uninstaller: follow the macOS steps in the
   [Nix manual](https://nix.dev/manual/nix/latest/installation/uninstall) instead.
