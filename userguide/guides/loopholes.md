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
| A host capability behind a service | A loophole, from a pack | Below |
| Your own machine, with no jail | `yolo host` | [Where Agents Run](confinement.md) |

Everything else stays out: your SSH keys, git credentials, cloud tokens, and the rest of your home.

## What a loophole is

A **loophole** is a named connection from a jail to one specific capability on your host, such as
a login service or a reader for your system log. It is not an escape from the jail: a pack ships it,
the pack's footprint lists exactly what it touches, and it is on only when its pack is selected and,
for most, when you turn it on by name. Many loopholes run a small service on your host that does the
privileged part itself, so the jail gets the answer without ever holding the credential or the
access behind it.

## The loopholes yolo ships

| Loophole (its pack) | What it gives the jail | On when you select the pack? |
|---|---|---|
| `claude-oauth-broker` (`claude`) | One shared Claude login that stays fresh across jails ([Logins](authentication.md#a-shared-claude-login-that-stays-fresh)) | Yes |
| `openai-auth-broker` (`openai-auth`, brought in by `claude`, `codex`, `opencode` and `pi`) | One shared ChatGPT login for Codex, pi and opencode ([Logins](authentication.md#a-shared-chatgpt-login-for-codex-and-pi)) | Yes |
| `aws-auth` (`aws-auth`, also brought in by `bedrock`, which `claude`, `codex`, `opencode` and `pi` bring in) | AWS Bedrock with credentials from your host's `aws sso login`, narrowed to one role | No |
| `github-broker` (`github`) | `gh` in the jail, run by your host's own GitHub login against this workspace's repositories, read-only for now, with no token in the jail ([GitHub](github.md)) | No |
| `serial` (`serial`) | USB serial devices on the host, through an allowlist, with the `yolo-serial` command | No |
| `journal` (`journal`) | The host's systemd journal, with `yolo-journalctl` (Linux hosts) | No |
| `host-processes` (`host-processes`) | A filtered list of host processes, with `yolo-ps` (Linux hosts) | No |
| `audio` (`audio`) | The host's microphone and speakers through PipeWire or PulseAudio (Linux hosts) | No |
| `cgroup-delegate` (`cgroup-delegate`) | Lets the jail cap the CPU and memory of its own jobs, with `yolo-cglimit` (Linux hosts) | No |

To turn one on, select its pack and switch it on in your user config:

```jsonc
{
  "packs": ["claude", "serial"],
  "loopholes": {
    "serial": { "enabled": true }
  }
}
```

Some loopholes take their own settings under `settings`, such as `aws-auth`'s SSO profile and role;
each pack's README in the repository describes them, and `yolo check` reports a setting that is
missing or misspelled. A loophole you turn on starts with the next fresh jail, not when you join one
that is already running.

## Loopholes on each setup

Loopholes work fully on Podman on Linux. On a Mac they depend on the runtime:

- **Podman on a Mac:** they should work, apart from the ones that need a Linux host. The connection
  from the jail to your Mac is tested nightly; the services themselves have not all been run end to
  end on a Mac.
- **Apple Container:** none work yet. A jail on Apple Container cannot connect back to the Mac, so
  the launch lists each loophole it had to skip. This includes the shared Claude and ChatGPT logins.
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
  the agent and for `aws`, not yet with a real agent.

[Settings per setup](../reference/settings-per-setup.md#the-loopholes-host-services-a-jail-can-use)
has the per-loophole detail.

## See what is on, and check it

```bash
yolo loopholes list      # every loophole your config selects, and whether it is on
yolo loopholes status    # on the host: run each loophole's own self-check
yolo host-daemon status  # the host services shared by every jail, and whether each is healthy
yolo pack footprint serial   # what a pack's loophole touches, before you select it
```

`yolo host-daemon` arrives in the release after 0.10.0; on 0.10.0, `yolo broker status` checks the
Claude login service.

Every launch also prints what each pack reads from your machine, and names each host service just
before it starts it.

## Build your own

- [Your Own Host Service](host-services.md): run a program of your own on the host for the jail,
  declared in your config.
- [Writing a Loophole](writing-loopholes.md): package a loophole in a pack, for yourself or to
  share.
