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
# 1. Nix — then open a new terminal
curl -fsSL https://install.determinate.systems/nix | sh -s -- install
# 2. Let Nix use yolo's prebuilt downloads
echo "trusted-users = root $(whoami)" | sudo tee -a /etc/nix/nix.custom.conf
sudo launchctl kickstart -k system/systems.determinate.nix-daemon
# 3. Apple Container — answer Y when it offers to install a kernel
brew install container
container system start
# 4. yolo
brew install mschulkind-oss/tap/yolo-jail
yolo check
```

**Linux** (Ubuntu or Debian shown; [Step 2](#linux-podman) has Fedora and Arch), with
[Homebrew](#homebrew) installed:

```bash
# 1. Nix — then open a new terminal
curl -fsSL https://install.determinate.systems/nix | sh -s -- install
# 2. Let Nix use yolo's prebuilt downloads
echo "trusted-users = root $(whoami)" | sudo tee -a /etc/nix/nix.custom.conf
sudo systemctl restart nix-daemon
# 3. Podman
sudo apt-get update && sudo apt-get install -y podman passt slirp4netns uidmap
# 4. yolo
brew install mschulkind-oss/tap/yolo-jail
yolo check
```

Then [choose an agent and launch it →](#step-4-first-launch)

The steps below explain each command, and cover Intel Macs, older macOS, other Linux distributions and
other ways to install yolo.

## What you need

| | Mac, Apple silicon | Mac, Intel | Linux (x86_64 or arm64) |
|---|---|---|---|
| **Nix** | [Determinate installer](#step-1-install-nix) | [Official installer](#other-ways-to-get-nix) | [Determinate installer](#step-1-install-nix) |
| **Container runtime** | [Apple Container](#macos-apple-container-recommended) on macOS 26 or later; [Podman](#macos-podman) otherwise | [Podman](#macos-podman) | [Podman](#linux-podman), rootless |
| **yolo** | [Homebrew](#homebrew) | [Homebrew](#homebrew) or a [release archive](#other-ways-to-install) | [Homebrew](#homebrew) or a [release archive](#other-ways-to-install) |

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
> **Intel Macs work today, but support is ending.** Apple Container, Podman 6 and the Determinate Nix
> installer have all dropped Intel Macs, and the Nix packages yolo uses on an Intel Mac stop receiving
> fixes at the end of 2026. Plan on an Apple silicon Mac.

## Step 1: Install Nix

yolo uses Nix to build the jail's contents, so every setup needs it. Install it with the
[Determinate Nix installer](https://github.com/DeterminateSystems/nix-installer). We recommend it
because it turns on **flakes** (the Nix feature yolo's build is written in) for you, keeps working
across macOS updates, supports Fedora's SELinux, and uninstalls cleanly:

```bash
curl -fsSL https://install.determinate.systems/nix | sh -s -- install
```

It installs Determinate Nix, Determinate Systems' distribution of Nix, on Apple silicon Macs and on
x86_64 and arm64 Linux. On a Mac you can use its
[graphical installer](https://install.determinate.systems/determinate-pkg/stable/Universal) instead.
Open a new terminal when it finishes, so `nix` is on your PATH.

### Let yolo use its binary cache

A **binary cache** is a server of packages that are already built. yolo publishes the pieces of its
jail to its own cache, and Nix uses a cache a project asks for only when the **Nix daemon** (the
background service that does Nix's builds) trusts your user. Add yourself to `trusted-users`, then
restart the daemon:

```bash
echo "trusted-users = root $(whoami)" | sudo tee -a /etc/nix/nix.custom.conf
sudo launchctl kickstart -k system/systems.determinate.nix-daemon   # macOS
sudo systemctl restart nix-daemon                                   # Linux
```

**On a Mac this step is required.** A Mac cannot build Linux programs itself, so the parts of the
jail that are not in Nix's public cache must come from yolo's cache, or be built in a temporary Linux
container, and Nix allows both only for a trusted user. On Linux it is optional, and saves building
those parts on your machine the first time.

A trusted user can change how the daemon builds, which the
[Nix manual](https://nix.dev/manual/nix/latest/command-ref/conf-file) calls essentially equivalent to
root access. Add only your own account, on a computer you control.

### Check that Nix works

```bash
nix --version
nix config show experimental-features   # lists flakes and nix-command
nix store info                          # shows "Trusted: 1" once the daemon trusts you
```

### Other ways to get Nix

- **Intel Mac.** The Determinate installer no longer supports Intel Macs. Use the
  [official installer](https://nixos.org/download/), then turn on flakes and trust yourself in its
  config file:

  ```bash
  curl --proto '=https' --tlsv1.2 -L https://nixos.org/nix/install | sh
  echo "experimental-features = nix-command flakes" | sudo tee -a /etc/nix/nix.conf
  echo "trusted-users = root $(whoami)" | sudo tee -a /etc/nix/nix.conf
  sudo launchctl kickstart -k system/org.nixos.nix-daemon
  ```

  The same two config lines apply if you prefer the official installer anywhere else. On Linux, run
  it as `sh -s -- --daemon` and restart with `sudo systemctl restart nix-daemon`. Without the
  `experimental-features` line, `yolo check` on a Mac reports `Nix daemon: connection failed`.
- **Ubuntu and Debian.** The `nix-bin` package is several releases behind (2.18 on Ubuntu 24.04, 2.8
  on Debian 12). Use the Determinate installer above.
- **Fedora.** Use the Determinate installer above. The official installer sets up a single-user Nix
  on systems with SELinux, Fedora's default, and a single-user Nix gives the jail no `nix` command
  of its own.
- **Arch Linux.** Arch ships Nix as a regular package. Install it, start its daemon, then add the two
  config lines and restart:

  ```bash
  sudo pacman -S nix
  sudo systemctl enable --now nix-daemon.service
  echo "experimental-features = nix-command flakes" | sudo tee -a /etc/nix/nix.conf
  echo "trusted-users = root $(whoami)" | sudo tee -a /etc/nix/nix.conf
  sudo systemctl restart nix-daemon
  ```

  Older guides also add you to a `nix-users` group; the current package has none, so skip that. You
  need no Nix channel either. See the [ArchWiki](https://wiki.archlinux.org/title/Nix).
- **NixOS.** Nix is already installed. Add these to `/etc/nixos/configuration.nix` (the last line is
  Step 2), then run `sudo nixos-rebuild switch`:

  ```nix
  nix.settings.experimental-features = [ "nix-command" "flakes" ];
  nix.settings.trusted-users = [ "root" "your-username" ];
  virtualisation.podman.enable = true;
  ```

  See the NixOS wiki on [flakes](https://wiki.nixos.org/wiki/Flakes) and
  [Podman](https://wiki.nixos.org/wiki/Podman).

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
last `-v` and the last line. `yolo check` warns if the machine has less than 4096 MB of memory.

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

Podman 5.1 or later works best; check with `podman --version`. Ubuntu 24.04 and Debian 12 ship older
versions, and yolo still works on them: it falls back to a slower network helper and says so when a
jail starts. Nothing needs configuring for Ubuntu 24.04's AppArmor user-namespace restriction, which
yolo already works around.

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
[just](https://github.com/casey/just), or [mise](https://mise.jdx.dev) to install both for you:

```bash
git clone https://github.com/mschulkind-oss/yolo-jail.git
cd yolo-jail
mise install    # the pinned Go and just; skip if you have them
just setup
just deploy     # builds and installs yolo, together with its build files
```

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
[the live-checkout rule](guides/macos.md#the-same-rule-now-decides-whether-a-live-checkout-can-launch-at-all).

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

The shipped agent packs are `claude`, `copilot`, `codex`, `opencode`, `pi` and `agy`; list as many as
you like. An agent installs inside the jail the first time you run it. Only your user config can
select packs; a project's own config cannot. [How packs work →](features.md#packs-and-agents)

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
| On a Mac: `No OCI conversion tool for Apple Container` | An outdated check: yolo builds its own image copier and uses nothing on your PATH | Ignore it, or `brew install skopeo` to silence it |
| On a Mac: `Podman Machine: not configured` | Podman is installed, but you use Apple Container | Ignore it |
| On a Mac: `A package must be built from source …` | Part of the image is in no binary cache | Keep the runtime running: the launch builds it in a temporary container |

And these need fixing before you launch:

| Row | Fix |
|---|---|
| `nix not found` | [Step 1](#step-1-install-nix), then open a new terminal |
| On a Mac: `Nix daemon: connection failed` | Turn on flakes: the `experimental-features` line in [Other ways to get Nix](#other-ways-to-get-nix) |
| On a Mac: `Nix daemon: connected but user is NOT trusted` | [Let yolo use its binary cache](#let-yolo-use-its-binary-cache). It is only a `[WARN]`, but the first launch needs it |
| `No container runtime installed`, or `… installed but not started` | [Step 2](#step-2-install-a-container-runtime), or the start command the note names |
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

yolo can also apply the same agents, skills and settings to your own machine, with no jail, through
`yolo host apply`. [Migrating to Packs](guides/migrating-to-packs.md#part-2--manage-your-host) covers
that once you have a setup worth keeping.

---

## Authentication

Inside the jail, log in to your tools:

```bash
gh auth login          # GitHub CLI
claude                 # Claude Code: runs /login on first launch
agy                    # Google Antigravity: sign in when it asks
```

The jail does not reuse the logins on your host; you log in inside the jail. Every login is kept on the host and survives jail restarts, so you do **not** log in again each time you start a jail. How far one login reaches depends on the tool:

- `claude` and `agy` keep one login for the whole machine: log in once, and the jails in all your other projects use it too.
- `codex` and `pi` share one OpenAI login through a login service yolo runs on your host. This does not work on every setup, and not on Apple Container; see [Do I have to log in again in every workspace?](reference/settings-per-setup.md#5-do-i-have-to-log-in-again-in-every-workspace-and-in-a-second-jail-at-the-same-time).
- `gh`, `copilot`, `opencode` and `omp` keep a separate login for each project, so you log in once per project.

### Claude OAuth broker (refresh serialization)

Anthropic uses single-use refresh tokens — when multiple jails share the same `.credentials.json` and two of them try to refresh in the same window, one loses the race and gets logged out. YOLO Jail ships the **claude-oauth-broker** loophole: a host-side daemon that serializes refreshes behind a flock. Jails route their refresh requests through it instead of calling Anthropic directly. A **loophole** is a named connection from a jail to one capability on your host; see [Loopholes](guides/loopholes.md).

The broker refreshes **both on demand and proactively**:

- **On demand** — when a jail asks for a refresh, a token with headroom is returned from cache; otherwise the broker refreshes upstream once and hands the result back.
- **Proactively** — the host daemon also runs a background refresher **by default**: it wakes every 60 s and refreshes when the shared token is within 5 minutes of expiry, retrying every 5 s (up to 12 times) while upstream is transiently unreachable. Pass `--no-background-refresh` to turn it off.

The background loop is not an optimization. Claude Code has no proactive refresh of its own for Pro/Max tokens — it refreshes reactively, after a 401 — so a jail that idles past expiry, or a laptop that suspends through it, would otherwise wake up to a logout. Refreshing ahead of expiry on the host is what prevents that.

**Selecting the `claude` pack is what turns the broker on**, and there is nothing else to set up: the broker creates its certificates in `~/.local/share/yolo-jail/state/claude-oauth-broker/` the first time it starts. `yolo check` includes a broker self-check covering those certificates and whether the credentials file parses. On Apple Container and `macos-user`, refreshes do not go through the broker yet, so jails there refresh on their own and can log each other out.

> **Security note:** Auth tokens are stored separately from your host credentials. The jail never accesses your host `~/.ssh/`, `~/.gitconfig`, or cloud credentials. The broker reads and refreshes exactly one file — the machine-shared `~/.local/share/yolo-jail/home/.claude-shared-credentials/.credentials.json`, which every jail on this machine symlinks to. **It never writes your host `~/.claude/.credentials.json`.** So a `/login` inside a jail does not keep host Claude Code logged in, and a broker refresh cannot disturb a host session.

---

## If something goes wrong

Start with `yolo check`: it names the fix for most setup problems. A few that come up while installing:

- **"Cannot find yolo-jail repo root"**: yolo was installed without its build files. See
  [Other ways to install](#other-ways-to-install).
- **On a Mac, `Nix daemon: connection failed`**: flakes are off. Add the `experimental-features` line
  from [Other ways to get Nix](#other-ways-to-get-nix) and restart the Nix daemon.
- **On a Mac with Podman, `statfs …: no such file or directory` when a jail starts**: the Podman
  Machine cannot see that folder. Recreate the machine with the share list in
  [macOS: Podman](#macos-podman) (`podman machine rm`, then `init` and `start` again).
- **A build that fails stops the launch** and shows Nix's own error. yolo never falls back to an older
  image.

[Troubleshooting →](guides/troubleshooting.md) covers more, and the [macOS guide](guides/macos.md)
covers Mac-specific problems.

## Upgrade

Exit your running jails first: an upgrade replaces the programs a running jail was started from.

```bash
brew upgrade yolo-jail            # Homebrew
cd yolo-jail && git pull && just deploy   # from source
yolo host-daemon status           # restart any it reports unhealthy: yolo host-daemon restart <name>
```

For a release archive, unpack the new one over the old folder.

## Uninstall

1. **Exit every jail**, then stop yolo's host services: `yolo host-daemon status` lists them, and
   `yolo host-daemon stop <name>` stops one. If you set up `macos-user`, run `yolo macos-teardown`.
2. **Remove yolo:** `brew uninstall yolo-jail`, or delete the release archive's folder, or remove
   `yolo` from Go's bin directory.
3. **Remove what yolo stored:** `rm -rf ~/.local/share/yolo-jail ~/.config/yolo-jail`. This deletes
   the logins your jails kept and your user config. Each project also has a `.yolo/` folder, and a
   `yolo-jail.jsonc` if you ran `yolo init`. On Linux, if `rm` reports permission errors, run it as
   `podman unshare rm -rf …`.
4. **Remove the jail images** from your runtime: `podman images` or `container image list` shows
   them, named `yolo-jail`.
5. **Remove the runtime and Nix** if nothing else uses them. For Apple Container, `brew uninstall
   container`; for Podman on a Mac, `podman machine rm`, then uninstall Podman. Determinate Nix
   uninstalls with `/nix/nix-installer uninstall`; for the official installer, follow the
   [Nix manual](https://nix.dev/manual/nix/latest/installation/uninstall).
