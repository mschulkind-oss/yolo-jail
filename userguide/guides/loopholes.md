# Host Access and Loopholes

A jail is separate from your machine, but an agent sometimes needs a piece of it: a folder of
reference code, a database running on your host, a USB device, a shared login. yolo makes each of
those a deliberate choice you write in your config, and never an accident. This page lists the
ways in, then covers **loopholes**, the one kind that runs something on your host.

## Every way the jail reaches your machine

| What | How you allow it | Guide |
|---|---|---|
| Your project | Always: it is mounted read-write at `/workspace` | [Getting Started](../getting-started.md#launch) |
| Other folders, read-only (or read-write from your user config) | `mounts` | [Configuration](../reference/configuration.md) |
| Single host files, such as a dotfile | `host_files`, in your user config | [Settings per setup](../reference/settings-per-setup.md#workspace-mounts-and-host-files) |
| Services on your host's network | `network.forward_host_ports`, or `host.containers.internal` | [Networking](networking.md) |
| USB devices, GPUs, `/dev/kvm` | `devices`, `gpu`, `kvm` (Linux only) | [Devices and GPUs](devices-and-gpus.md) |
| A host capability behind a service | A loophole, from a pack, plus a grant | Below |
| Your own machine, with no jail | `yolo host` | [Where Agents Run](confinement.md) |

Everything else stays out: your SSH keys, git credentials, cloud tokens, and the rest of your home.

## What a loophole is

A **loophole** is a named connection from a jail to one specific capability on your host, such as
a login service or a reader for your system log. It is not an escape from the jail: a pack ships it,
the pack's footprint lists exactly what it touches, and it runs only when you select its pack. Many
loopholes run a small service on your host that does the privileged part itself, so the jail gets
the answer without ever holding the credential or the access behind it.

**Selecting a pack gives no project anything.** The loophole's service runs, and a project gets
access through it only once you approve a **grant**: a named thing a project may do, such as
`read` for the journal. There is no on/off switch to set.

## The loopholes yolo ships

| Loophole (its pack) | What it gives the jail | What gives a project access |
|---|---|---|
| `claude-oauth-broker` (`claude-auth`, which `claude` brings) | One shared Claude login that stays fresh across jails ([Logins](authentication.md#a-shared-claude-login-that-stays-fresh)) | Your login: nothing is served until you log in |
| `openai-auth-broker` (`codex-auth`, which `codex` brings) | One shared ChatGPT login for Codex, pi and opencode ([Logins](authentication.md#a-shared-chatgpt-login-for-codex-and-pi)) | Your login |
| `aws-auth` (`aws-auth`, which `bedrock` brings) | AWS Bedrock with credentials from your host's `aws sso login`, narrowed to one role | Your settings: nothing is minted until you set a profile |
| `github-broker` (`github`) | `gh` in the jail, run by your host's own GitHub login against this workspace's repositories, with no token in the jail. Reads run under the read grant; a write runs unasked only under a grant (`create_pr`, `create_issue`, `dispatch_workflows`, `approve_runs`, or a PR or issue in the agent's working set), and every other write asks you with `yolo approve` ([GitHub](github.md)) | `read`, with `yolo allow github read` ([GitHub](github.md#turning-it-on)) |
| `git-broker` (`git`) | `git fetch` and `git push` in the jail through your host's own credentials, its SSH keys or agent or, for HTTPS, its git credential helper, against this workspace's approved remotes. A public HTTPS fetch is tried anonymously even without a read grant or approved remote; no credential is used. A push runs unasked only under a grant (`new_branches`, `push`, `push_tags`) or to a branch in the agent's working set; every other push asks you with `yolo approve` ([Git](git.md#letting-the-agent-push-its-own-branches)) | `fetch`, with `yolo allow git fetch` ([Git](git.md#turning-it-on)) |
| `journal` (`journal`) | The host's systemd journal, with `yolo-journalctl` (Linux hosts) | A grant: `read` (your own journal) or `read_all` (the whole journal) |
| `macos-log` (`macos-log`) | The Mac's unified log, with `yolo-log` (Mac hosts) | A grant: `read` (entries from processes the `macos-user` sandbox runs) or `read_all` (every entry) |
| `host-processes` (`host-processes`) | A list of the host processes you name, with `yolo-ps` (Linux hosts) | A grant: `view`, with the process names |
| `serial` (`serial`) | USB serial devices on the host, with the `yolo-serial` command | A grant: `devices`, with the device patterns |
| `audio` (`audio`) | The host's microphone and speakers through PipeWire or PulseAudio (Linux hosts) | A grant: `use` |
| `cgroup-delegate` (`cgroup-delegate`) | Lets the jail cap the CPU and memory of its own jobs, with `yolo-cglimit` (Linux hosts) | Nothing to grant: it can only limit the jail itself |

## Giving a project access

Access takes two steps, and they are the same for every loophole that has grants.

**1. The project asks.** Run this in the project, in the jail or on your machine:

```bash
yolo request journal read
yolo request host-processes view postgres redis-server
yolo request serial devices '/dev/ttyUSB*'
```

Each writes a line under `[requests.<pack>]` in the project's `yolo-jail.toml` and prints the next
step. The agent can run it too: a request is only a request.

**2. You approve it, on your machine.** The next launch of that project shows each grant in its
config-change prompt, or run `yolo config apply` in the project. Answer y and the project holds the
grant from then on, in every jail of that project. Answer n and nothing is granted.

**What a project may ask for is up to you.** Your user config sets a ceiling for each pack, under
`[allow.<pack>]`. A request outside it is skipped, never an error, and `yolo check`, every launch
and the `yolo config apply` prompt each say so, naming the command that would allow it:

```text
[WARN] yolo-jail.toml:2: this project requests host-processes view postgres.
  Your config does not allow view on request, so this project runs without it.
  To allow it for every project you work on (in your config, not this file), run on the host:
    yolo allow host-processes on_request view
  then approve it for this workspace, if that command did not already ask:
    yolo config apply
```

```bash
yolo allow host-processes on_request view   # let any project ask to see processes
yolo allow journal read                     # grant your journal to every project
```

`on_request` lets a project ask; the bare form (`always`) grants it to every project, which each
project still shows you once at its next prompt. `journal` and `macos-log` let a project ask for
`read` with no ceiling line at all; `read_all` always needs one. A grant that carries values, such
as the process names or device patterns, can only be allowed on request, since only a project
knows which ones it needs.

To take a grant back, remove the request or the ceiling line and run `yolo config apply`. A
running jail's journal, log, process and serial access changes at once; `audio` changes at the
next fresh launch.

The git and GitHub brokers hold their grants the same way, and can also ask you for **one
action**, a push or a `gh` write no grant covers, which you answer with `yolo approve` on your
machine. [Approving What the Agent Asks For](approvals.md) covers both kinds of asking, and how to
be notified of a request.

Some loopholes also take settings of their own, such as `aws-auth`'s SSO profile and role or the
`ps` columns `host-processes` shows. They go in your user config under `[allow.<pack>]`:

```bash
yolo config set allow.aws-auth.profile my-sso-profile
```

Settings never give a project anything by themselves. Each pack's README in the repository lists
its settings, and `yolo check` reports one that is missing or misspelled.

**Coming from an older config?** `"loopholes": {"<name>": {"enabled": true}}` and
`yolo loopholes enable` are gone. An older JSONC config keeps working as grants until you convert
it, and `yolo config migrate` carries every access you had into `[allow.<pack>]` and
`[requests.<pack>]` lines without losing any. `yolo loopholes enable` now refuses and names the
`yolo request` and `yolo allow` lines to use instead.

## Loopholes on each setup

Loopholes work fully on Podman on Linux. On a Mac they depend on the runtime:

- **Podman on a Mac:** they should work, apart from the ones that need a Linux host. The connection
  from the jail to your Mac is tested nightly; the services themselves have not all been run end to
  end on a Mac.
- **Apple Container:** none work yet. A jail on Apple Container cannot connect back to the Mac, so
  the launch lists each loophole it had to skip, and the config-change prompt shows no grant for one.
  This includes the shared Claude and ChatGPT logins.
- **`macos-user`:** the host services start, and the ChatGPT login service and Bedrock
  (`aws-auth`) work: the piece the agent talks to runs beside the launch, outside the sandbox, on
  the Mac's own loopback, which the agent shares. A pack's own in-jail helper runs inside the
  sandbox, confined like the agent. The shared Claude login does not work: its in-jail half needs
  a container, and the launch says so.
- **`yolo host`, with no jail:** Bedrock through `aws-auth` works too. `yolo host -- pi` (or any
  agent on a Bedrock profile) runs the credential helper for that one command, on your machine's
  own loopback, and stops it when the agent exits. A profile in your `~/.aws` that holds
  credentials still wins: the one `AWS_PROFILE` names, or `[default]` when it is unset.
  It finds a region the way a jail launch does, the `aws-auth` profile's in `~/.aws/config`
  included; a region only in the agent's own settings does not count. Tested with stand-ins for
  the agent and for `aws`, not yet with a real agent. Other grants do not apply there: the agent
  already runs as you.

[Settings per setup](../reference/settings-per-setup.md#the-loopholes-host-services-a-jail-can-use)
has the per-loophole detail.

## See what a project holds, and check it

```bash
yolo loopholes list      # every loophole your config selects, and what this project holds
yolo loopholes status    # on the host: run each loophole's own self-check
yolo host-daemon status  # the host services each running jail started, and whether each answers
yolo pack footprint serial   # what a pack's loophole touches, before you select it
```

Every launch also prints what each pack reads from your machine, names each host service just
before it starts it, and says for each loophole that has grants what this project holds or the
command that would grant it. When a command in the jail is refused for want of a grant, its message names the same
next step.

## Build your own

- [Your Own Host Service](host-services.md): run a program of your own on the host for the jail,
  declared in your config.
- [Writing a Loophole](writing-loopholes.md): package a loophole in a pack, for yourself or to
  share.
