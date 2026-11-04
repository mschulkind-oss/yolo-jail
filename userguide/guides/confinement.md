# Where Agents Run

yolo describes the environment an agent works in — its tools, config, skills and credentials — and
applies it through a named **setup**. A setup has two independent values: **confinement**, the
technology that sets the boundary, and **run_as**, the account that runs the agent. The
[settings-per-setup reference](../reference/settings-per-setup.md) compares what each can do.

| Setup | What it runs | Availability |
|---|---|---|
| `podman-self` | Linux container; Podman runs directly on Linux and in a Podman Machine VM on macOS | Linux default; available on macOS when Podman is installed |
| `apple-container-self` | Linux container in its own small VM | macOS 26 or later on Apple silicon; macOS default |
| `seatbelt-self` | Agent on macOS, as you, under yolo's deny-by-default Seatbelt rules | macOS |
| `seatbelt-yolo-account` | Agent on macOS, as the separate yolo account, under the same Seatbelt rules | macOS; create the account once with `yolo yolo-account create` |
| `landlock-self`, `landlock-yolo-account` | Planned Linux setups | Not built |
| `host-self`, `host-yolo-account` | No confinement; only `yolo host` can run these | `host-self` is the default for `yolo host`; the yolo-account variant is available on macOS |

The ordinary launch defaults to `podman-self` on Linux and `apple-container-self` on macOS. It does
not probe for another installed setup or fall back to the host. Choose another setup for one launch
with `--setup`, through `YOLO_SETUP`, or as your user-wide default with `setup = "<name>"` in your
user config. A project file cannot choose or define a setup: the choice belongs to you, not to a
workspace an agent can edit.

```bash
yolo setup list                                  # names every built-in and your own setups
yolo setup show seatbelt-self                    # show its fields
yolo --setup seatbelt-self -- claude             # one launch, as you under Seatbelt
YOLO_SETUP=podman-self yolo -- claude             # one shell's default
# In ~/.config/yolo-jail/config.toml:
# setup = "seatbelt-self"
yolo config set setup seatbelt-self --user        # write that user-wide default
yolo setup new mac-as-me --from seatbelt-self     # define a setup of your own, in user config
yolo config set setups.mac-as-me.run_as self --user
```

The removed launch flag `--at` is refused and names `--setup`; `--at host` names `yolo host`. The
removed `YOLO_RUNTIME` environment variable is refused on the host and warns if inherited inside a
running session. Removed config keys `runtime`, `confinement` and `macos_user_mode` are refused,
never treated as aliases. `yolo config migrate` converts only user values that name one setup; use
`yolo config-ref` and the refusal's suggested command for values it cannot convert. A project file
holding one of these old keys is an error too; remove it with the named `--project` command. See
[configuration](../reference/configuration.md) for config scope.

## The jail

A **jail** is an isolated environment for autonomous work. The container setups run a Linux
container with its own home and network; your SSH keys, git credentials, cloud tokens and the rest
of your home are not there. Each additional thing it may reach is a line in your config. Because of
that boundary, yolo starts agents in a jail with their permission prompts off.

On Linux, the default `podman-self` runs rootless Podman containers. On an Apple silicon Mac with
macOS 26 or later, the default `apple-container-self` runs each container in its own small Linux
virtual machine. Podman also works on macOS inside one Linux virtual machine (the Podman Machine),
and supports features Apple Container may not. [Getting Started](../getting-started.md) installs
the container setup for your platform.

### Seatbelt setups

On macOS, `seatbelt-self` runs the agent as you inside Apple's built-in Seatbelt sandbox, with no
container, virtual machine or Linux image. `seatbelt-yolo-account` uses the same deny-by-default
rules but runs the agent as the separate yolo account, which holds none of your files. Create that
account once with `yolo yolo-account create`; the command asks for your administrator password.
These setups are available to choose explicitly; they are not the current macOS default.

[The macOS guide](macos.md#choosing-a-setup) explains the tradeoffs and the one-time account setup.

## Your own machine

yolo can also apply your packs and config to your real home, for agents you run outside any jail.
Nothing is confined here, so treat it as managing your agents' configuration, not as a boundary:

```bash
yolo host apply
yolo host apply --assert
yolo host -- claude
yolo host --setup host-yolo-account -- claude
```

On your own machine the agents keep their permission prompts on, and yolo warns before it changes a
setting you made yourself. [Writing your own pack](migrating-to-packs.md#part-2--manage-your-host)
walks through it.

Two jail habits come along to `yolo host -- <agent>`. Tools the `guardrails` pack (or your own
`security.blocked_tools`) blocks are blocked for that agent too, and the launch says so. And when
your repository keeps skills under another agent's folder, such as `.claude/skills`, the launch
puts one link at the agent's own folder (`.codex/skills`) pointing at those skills, and adds it to
`.gitignore` once; a repository that already has the agent's folder is left alone. Whether each
agent follows a linked folder is not yet measured.

<a id="yolos-own-copy-of-your-agents"></a>

## Host agent floor

`yolo host -- claude` runs **yolo's own copy** of each agent your selected packs install, not
whichever `claude` happens to be first on the PATH of whatever started it. So the same agent starts
from your terminal, from a Waybar widget, from cron or from an IDE, even when that launcher's PATH
has no `~/.local/bin` and no mise. yolo calls these copies the **host agent floor**, and keeps them
in `~/.local/share/yolo-jail/host-floor`:

- **Where it lives.** One folder yolo owns, readable only by you, that no jail can see. It is on no
  PATH of yours: your shell and any copy you installed yourself (`~/.local/bin/claude`, Homebrew's)
  are left alone. `yolo check` lists each agent, and names any other copy it finds as one `yolo host`
  does not run.
- **How it gets there.** Every `yolo host -- <command>` first installs every program your selected
  packs declare that is missing, as a jail does, saying what it is doing; `yolo host apply --assert`
  installs them too. Selecting the pack is the consent: nothing asks. When one cannot install, the
  launch stops and names the fix; set `YOLO_ALLOW_MISSING_PROGRAMS=1` to start the command anyway
  ([the host readiness act](https://github.com/mschulkind-oss/yolo-jail/blob/main/docs/reference/host-agent-environment.md#the-host-readiness-act)). An agent installed with npm (opencode or pi, for example) runs on the
  floor's own Node, the official release, checked against its published checksum. An agent with its
  own installer (claude or copilot, for example) comes from the machine's `yolo capture` of that
  installer, so the first one may run a capture if the machine has none yet. On Linux with a
  container runtime that is the same capture your jails use. With none, yolo runs the installer on
  your machine instead, confined by Landlock to a throwaway home, and that capture serves yolo's own
  copy alone: your jails capture their own. On a Mac the installer runs as the yolo account, created
  once with `yolo yolo-account create`, through the rule setup installs, with no password.
- **Keeping it current.** The floor updates an agent the way a jail does: at most once an hour, when
  you start it, unless `agent_updates` freezes that pack. It says when it is checking for a newer
  version, so a slow package registry is not a launch that hangs saying nothing. An agent with its
  own installer is captured again once its capture is a day old, and the floor moves only to a newer
  release, never back to an older one. If a newer release fails to install, the launch stops and
  gives the command that runs the installed one: `YOLO_USE_INSTALLED=<agent> yolo host -- <agent>`.
- **What it cannot hold yet.** On a Mac before the yolo account has been created, yolo has no copy of an agent with
  its own installer, and the launch stops and names `yolo yolo-account create`. The same happens on Linux for an
  installer agent on a machine with neither a container runtime nor Landlock to capture it with. On
  NixOS without nix-ld, or on a musl system such as Alpine, the agents yolo copies cannot start,
  because they need a dynamic loader those systems do not have: the launch stops and names the
  fix, `programs.nix-ld.enable = true;` on NixOS. yolo never runs a copy from your PATH in their
  place. An agent whose vendor publishes no build for your machine is the one exception: `yolo host`
  runs the copy on your PATH and says so.
- **Choosing.** Set `"host_floor": false` in your user config for a floor of nothing, or
  `"host_floor": {"*": true, "claude": false}` to leave one pack out; `yolo host` then installs
  nothing for that agent and runs it from your PATH. This is how to keep using a copy you
  installed yourself. The floor's copy of an agent you no longer select, or have left out, is never
  run, and stays until it is removed: under `"host_management": "own"` `yolo host apply --assert`
  removes it, and otherwise `yolo check` names the command that removes it by hand.
- **Getting an agent from your package manager instead.** List the ways you prefer under
  `"provisioners": {"host": ["brew"]}` in your user config, most preferred first: `pack` (the pack's
  own recipe), `apt`, `dnf`, `pacman`, `brew` or `nix`. Where a manager you rank first is on your
  PATH and the pack names a package for it, that manager supplies the agent: yolo keeps no copy of
  it, `yolo host` runs the manager's, and `yolo host apply` and `yolo check-deps` offer the manager's
  install command. Until you install the manager's copy, `yolo host` stops and prints that command.
  Anything you leave out keeps yolo's usual order, so an agent your manager has no
  package for still comes from its own installer. A manager updates on its own schedule, which can
  lag the vendor's. `nix` does not supply an agent yet: yolo keeps its own copy, and `yolo check`
  says so.

Just before it hands over, `yolo host` prints one line naming what it starts and where it came from,
so a slow start is visibly the agent's.

### Where `yolo host` looks for your tools, and `host_path`

Everything yolo does not keep a copy of, `yolo host` looks for on the PATH it was started with: a
tool a pack needs (`rg` and `fd` for the guardrails pack, anything a pack declares as a `program` with no `via`),
an agent yolo keeps no copy of because you left its pack out of `host_floor`, gave it to a package
manager or its vendor publishes no build for your machine, and any other command you run with `yolo host -- <command>`. yolo
has no other way to know where you keep your tools. A terminal's PATH usually has them. A Waybar
button, a cron job or a hotkey launcher often starts yolo with only `/usr/bin:/bin`, so from there a
tool in `~/.cargo/bin` or behind mise's shims is not found.

`host_path` in your user config lists folders to search after that PATH:

```jsonc
// ~/.config/yolo-jail/config.jsonc
{
  "host_path": ["~/.cargo/bin", "~/.local/share/mise/shims", "/opt/homebrew/bin"]
}
```

- **It only adds.** Each folder goes after the PATH yolo was started with, unless that PATH already
  has it, so nothing your own PATH finds first changes. With no PATH at all, yolo checks these
  folders alone, and the agent gets the usual system folders ahead of them.
- **The agent gets them too.** The agent's PATH is the PATH yolo was started with, then these
  folders, then yolo's own copies of your agents, so the agent's own commands find the same tools.
- **Nothing else is added.** yolo guesses no folder of its own and runs no mise command. To reach
  mise's tools from every launcher, list its shims folder, as above.
- **Only your user config counts.** A project's `yolo-jail.jsonc` cannot set it, since a folder
  here decides which program `yolo host` runs.
- **Write each folder absolute, or starting with `~/`.** yolo expands no variable, so an entry such
  as `$HOME/.cargo/bin` is ignored. When a program is not found, the line yolo prints (below) names
  an ignored entry and how to write it, and so does `yolo check`.

When `yolo host apply` or `yolo check-deps` cannot find a tool your packs need, or
`yolo host -- <command>` cannot find the command, it prints one line saying which program is
missing, which pack needs it, the whole PATH it searched and the `host_path` fix:

```text
yolo host: rg (required by the guardrails pack) is not on this launch's PATH, /usr/bin:/bin, the PATH yolo was started with. If rg is installed, add its folder to "host_path" in ~/.config/yolo-jail/config.jsonc; ~/.cargo/bin has one.
```

The last part names a common folder, such as `~/.cargo/bin` or `~/.local/bin`, only when that
folder really holds the program. It is a hint for your config line: yolo never runs a program it
found that way, and never counts it as present. `yolo check` has a **Host launch PATH** section that
shows the PATH it searched, each `host_path` folder, and whether each tool your packs need is
found. It reads the PATH of the shell you run it in, so a launcher with a different PATH can still
get a different answer.

A launch checks the tools your packs need only when `host_apply_on_launch` is on in your user
config. That key is off unless you set it, or turn `host_wrappers` on, which turns it on too. With
it off, `yolo host -- <agent>` says nothing about a missing tool, and the agent's first use of it
fails; run `yolo host apply` or `yolo check-deps` from the same launcher to see the line. The
command you start is looked up either way, and a miss prints the line.

## Choosing a setup

For one launch, pass a setup name. `yolo setup list` shows the built-ins and your own setups, the
current selection and why:

```bash
yolo --setup podman-self -- claude
YOLO_SETUP=seatbelt-self yolo -- claude
yolo host -- claude                         # host-self
yolo host --setup host-yolo-account -- claude
```

The precedence is `--setup`, `YOLO_SETUP`, the `setup` value in your user config, then the OS
default (`podman-self` on Linux, `apple-container-self` on macOS). To make a lasting selection, use
`yolo config set setup <name> --user`. The `setup` key and `[setups.<name>]` tables are user-scope
only; a project cannot select its own walls or define a setup.

A setup has two independent fields, `confinement` and `run_as`. `yolo setup show <name>` displays
them, and `yolo setup new <name> --from <setup>` copies a built-in or one of your setups into a new
user-config table. `host` setups are only run with `yolo host`; they are never selected by an
ordinary launch, project config or automatic fallback.
