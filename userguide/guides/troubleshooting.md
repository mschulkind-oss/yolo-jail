# Troubleshooting

Start with `yolo check`. It checks the runtime, Nix, your config, the jail image, running jails and,
where they apply, GPUs, the Mac VM and the Claude login service, and each failing row names its fix:

```bash
yolo check              # the full check, including the image build
yolo check --no-build   # the fast version
```

[Getting Started](../getting-started.md#check-the-setup) explains the rows a healthy new machine
still shows. Mac-specific problems are in [macOS](macos.md#troubleshooting).

When you report a bug, include the first line every `yolo` command prints, such as
`yolo-jail 0.15.0+g1a2b3c4d5e6f | darwin/arm64 | host`: it names the version, the platform and whether you ran it
inside a jail.

## Installing and launching

**"Cannot find yolo-jail repo root".** yolo was installed without its build files, the Nix recipe it
builds each jail from. Homebrew, a release archive and a from-source `just deploy` include them;
`go install` and pipx do not. See [Other ways to install](../getting-started.md#other-ways-to-install).
Every launch prints which copy of the build files it used on a `Flake source:` line.

**The image build fails.** The launch stops and shows Nix's own error; yolo never falls back to an
older image.

- Check that Nix is on your PATH: `nix --version`.
- On a Mac, the Nix daemon must trust your user; see
  [Trust your user](../getting-started.md#trust-your-user-required-on-a-mac). A package in
  no binary cache is built in a temporary container, so keep your runtime running.

**The jail will not start.**

- On Linux, check that `podman info` works and prints `true` for
  `podman info --format '{{.Host.Security.Rootless}}'`. Run Podman and yolo as your own user, never
  with `sudo`.
- Just after a reboot, a launch may wait up to a minute on `Checking that podman is running` while
  Podman finishes its own cleanup, printing each error Podman gives meanwhile. It stops at once only
  when the error cannot clear by itself, and then says what to fix. Each launch also leaves one line
  in `~/.local/share/yolo-jail/logs/launches.log`, so you can see which workspaces came back.
- On a Mac, check the runtime is up: `container system status`, or `podman machine list`. A busy
  Podman machine gets up to a minute to answer; one that still has not answered is reported as not
  answering. Wait for it, and if it never answers, restart it with `podman machine stop`, then
  `podman machine start`. That stops every container in the machine, other jails included.
- Start a fresh jail: `yolo stop`, then launch again. `yolo ps` lists running jails.

**"This jail has no outbound network access".** Each launch checks, in the background and for a
few seconds at most, that the jail can open a connection to the internet: to the hosts your packs
install from or send requests to, and to `github.com`. When none of them answers, the launch says
so, lists what each attempt got (a name that did not resolve, a connection that timed out, a proxy
that could not be reached or refused), and still starts, since a jail may be offline on purpose.
Without a network, an agent's login or token refresh, package installs and model calls all fail
later, each naming itself rather than the network. The next step depends on the setup:

- Apple Container: if the Mac itself is online, check for a VPN or an endpoint-security network
  filter that blocks the container network, then run `container system stop && container system start`
  and launch again.
- Podman: check the host is online and that no VPN or firewall rule drops traffic from Podman's
  network. On a Mac, restart the Podman machine with `podman machine stop`, then
  `podman machine start`.
- `macos-user`: the sandbox uses the Mac's own network, so check the Mac's connection and any VPN
  or network filter that applies to the sandbox account.
- Behind a proxy: give the jail `HTTPS_PROXY` (and `NO_PROXY` if needed) with an `env_sources`
  entry; the check goes through it. A proxy listening on your computer's `127.0.0.1` cannot be
  reached at that address from inside a container.

**"DNS lookups in this jail took longer than 3s; outbound access was not confirmed".** The same
check, when a name lookup was still waiting for an answer at the end of its few seconds. That is a
slow resolver, often a VPN's, not proof that the jail is offline, so the launch says it could not
confirm access rather than that there is none. Time a lookup inside the jail or sandbox:

- Apple Container and Podman: `time getent hosts github.com`.
- `macos-user`, which says "this sandbox" (macOS has no `getent`):
  `time dscacheutil -q host -a name github.com`.

If it answers, the network works and DNS is slow: look at the VPN or the resolver your host hands
out. If it never answers, follow the steps for no outbound network above.

**"No packs are configured, so this jail has no coding agent".** Add an agent pack to your user
config; see [Choose an agent](../getting-started.md#choose-an-agent).

**My other terminal's agent stopped when I quit the first one.** It no longer does: a jail lives
while any terminal in it does. Quitting an agent, or closing its terminal, ends that terminal's
agent and nothing else, whichever terminal started the jail; the terminal says the jail stays up
for the others, and `yolo -- <agent>` there joins them again. The last terminal to quit ends the
jail and shows it shutting down. A terminal that says its jail stopped names why: `yolo stop`, a
restart from another terminal, or nothing recorded, which is an out-of-memory kill, a crash or a
`podman stop` from outside yolo.

**"Refusing to enter … its keeper … is gone".** Each running jail has a small background process,
`yolo internal daemon jail-keeper`, which every launch names and which holds the jail's logins
through yolo, port forwards and cgroup delegate. When it was killed, the terminals already in the
jail keep running without those services, and a new one is refused. Run `yolo stop`, then `yolo`,
to start the jail fresh; or let its terminals finish, and the last one to quit cleans the jail up.
Its log is `~/.local/share/yolo-jail/logs/jail-keeper-<jail name>.log`.

**"Host service … of this jail has been down since …".** One of the services the keeper started
for this jail stopped while the jail was up and is no longer being restarted. A host service such
as a login service is restarted when it dies, up to five times in a row, so for one of those the
line means the keeper stopped restarting it: it kept dying, its pack was removed from your `packs`
list while the jail was up, or you stopped it with `yolo host-daemon stop`. A port forward whose
`socat` exited and the cgroup delegate are never restarted. Until the jail starts again,
what in the jail uses that service fails. Each death and restart is also a line in the workspace's
`.yolo/logs/launch.log` and in the service's own log. A terminal that was in the jail is
told as it quits, and one that joins later as it enters; the line names how the service ended and
its log. Run `yolo stop`, then `yolo`, to start the jail and its services fresh.

**"The previous jail of this workspace is still shutting down".** A launch right after the last
terminal quit, or after `yolo stop`, waits for that jail's keeper to finish before it starts a new
one. If the wait runs out, the message names the keeper's pid and log; `kill <pid>` ends it in
order.

## After a config change

**My edit did nothing.** Running `yolo` while the project's jail is running joins that jail, which
keeps the config it started with. Run `yolo stop`, then `yolo` again.

**A script or CI job stops with a config diff.** A launch with no terminal cannot ask you to
approve a changed config. Pass `--accept-config-changes` for that one launch; see
[Approving config changes](../reference/configuration.md#approving-config-changes).

**A tool I installed is missing after a restart.** Tools installed with `npm -g`, `go install` or
`uv tool` are kept per project. An installer that writes its own home folder, such as `~/.bun`,
fails with `Read-only file system` until you list that folder in `writable_home_dirs`. To refresh a
mise tool's PATH in the current shell, run `eval "$(mise hook-env -s bash)"`.

## Agents and logins

**Claude keeps logging out across jails.** Claude's refresh token works only once, so two jails
refreshing at the same moment can log each other out. On Podman, the Claude login service that comes
with the `claude` pack prevents this. Each jail runs its own copy: check them with
`yolo broker status`, and restart a jail's copy by leaving that jail and starting it again. On Apple Container and `macos-user` that service does not work yet, so log
in again when it happens, or use Podman for several Claude jails at once.
[Logins](authentication.md#when-a-login-keeps-failing) has more.

**`codex` or `pi` says `OpenAI login is required.` on Apple Container.** The shared ChatGPT login
cannot be reached from Apple Container yet. Use an API key, or Podman.

**An MCP server does not appear.** Check it is listed in `mcp_servers`, or that the pack that ships
it (such as `chrome-devtools`) is in `packs`, and that you restarted the jail after adding it. Agent logs are in `~/.claude/` and `~/.copilot/logs/` inside
the jail, for example `tail -100 ~/.copilot/logs/$(ls -1t ~/.copilot/logs | head -1)`.

**A language server does not respond.** yolo installs none: the server's program must be on the
jail's PATH, for example from `mise_tools`. Servers start when an agent opens a matching file. A
TypeScript server also needs a `tsconfig.json` or `jsconfig.json` in the project.

## Files and permissions

**Files the agent writes are owned by `root` on Linux.** Your Podman runs as root. Run Podman as
your own user, and new files are yours.

**On `macos-user`, files show as owned by `_yolojail`.** That is the sandbox account; you can still
read and write them, and `git` may print ownership warnings.

## Linux devices

**An NVIDIA GPU is not visible in the jail.** Check `nvidia-smi` works on the host,
`nvidia-ctk --version` shows the NVIDIA Container Toolkit, and a CDI spec exists at
`/etc/cdi/nvidia.yaml`. See [NVIDIA GPU passthrough](devices-and-gpus.md#gpu-passthrough-nvidia).

**Permission denied on `/dev/dri` or another device, on rootless Podman.** Your user needs the
device's group on the host, such as `render` or `video` (`sudo usermod -aG render,video "$USER"`,
then log in again). A rootful Podman can open more devices, but a jail on it cannot reach yolo's host
services, so keep it to the jails that need the device.

## Still stuck

- `yolo check` again, and read every `[FAIL]` and `[WARN]` note.
- `<project>/.yolo/logs/launch.log` holds everything the last launch printed, and `<project>/.yolo/logs/boot.log`
  what happened as the jail started. `<project>/.yolo/logs/boot.session.log` holds the same for the
  last terminal that entered the jail, and each log keeps the one before it with `.prev` on the
  end. A jail started by an older yolo writes these logs directly in `<project>/.yolo/` instead.
- [Settings per setup](../reference/settings-per-setup.md#what-works-in-each-setup) says whether a
  feature works on your setup at all.
