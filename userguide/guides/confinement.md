# Where Agents Run

yolo describes an agent's environment once, and that description can be applied in more than one
place. How much of your machine the agent can reach there is its **confinement**. You choose it; it
is one setting, not the whole product.

| Where | What the agent can reach | Status |
|---|---|---|
| **Jail** | Only your project and what you allow, inside an isolated container | Supported, and the default |
| **Your own machine** (`yolo host`) | Everything you can, as you | Supported: configuration and launch |
| **Guest** | A separate user account on your machine, with a real home and no container | In development |

## The jail

A **jail** is an isolated Linux container. The agent works on your project, mounted read-write at
`/workspace`, with its own home, its own tools and its own network. Your SSH keys, git credentials,
cloud tokens and the rest of your home are not there, and each additional thing it may reach is a
line in your config. Because of that boundary, yolo starts agents in a jail with their permission
prompts off.

Use the jail for autonomous work: an agent that runs commands, installs packages and edits files
without asking you each time.

A jail needs a **container runtime**, the program that runs it:

- **Linux:** [Podman](https://podman.io/), run as your own user.
- **Mac:** [Apple Container](https://github.com/apple/container) is recommended, on an Apple silicon
  Mac with macOS 26 or later. Podman, which runs jails inside one Linux virtual machine, also works
  and supports more features. [macOS](macos.md) compares them.

[Getting Started](../getting-started.md) installs both.

### The `macos-user` sandbox

On a Mac there is a third way to run a jail, **`macos-user`**: the agent runs as a hidden macOS user
inside Apple's built-in sandbox, with no container and no virtual machine. It has the
[fastest fresh start](macos.md#how-they-compare-on-speed) and needs no runtime, but its boundary is
weaker than a container's and several features are missing.
It is still in development, and is planned to become the main Mac setup once it is finished.
[About `macos-user` →](macos.md#the-macos-user-backend)

## Your own machine

yolo can also apply your packs and config to your real home, for agents you run outside any jail.
Nothing is confined here, so treat it as managing your agents' configuration, not as a boundary:

```bash
yolo host apply            # show what would change in your real home; writes nothing
yolo host apply --assert   # write it
yolo host -- claude        # run an agent with its composed environment: keys, profile
```

On your own machine the agents keep their permission prompts on, and yolo warns before it changes a
setting you made yourself. [Writing your own pack](migrating-to-packs.md#part-2--manage-your-host)
walks through it.

### yolo's own copy of your agents

`yolo host -- claude` runs **yolo's own copy** of each agent your selected packs install, not
whichever `claude` happens to be first on the PATH of whatever started it. So the same agent starts
from your terminal, from a Waybar widget, from cron or from an IDE, even when that launcher's PATH
has no `~/.local/bin` and no mise. yolo calls these copies the **host agent floor**, and keeps them
in `~/.local/share/yolo-jail/host-floor`:

- **Where it lives.** One folder yolo owns, readable only by you, that no jail can see. It is on no
  PATH of yours: your shell and any copy you installed yourself (`~/.local/bin/claude`, Homebrew's)
  are left alone. `yolo check` lists each agent, and names any other copy it finds as one `yolo host`
  does not run.
- **How it gets there.** The first `yolo host -- <agent>` installs the agent it names, saying what it
  is doing; `yolo host apply --assert` installs every one that is missing. Selecting the pack is the
  consent: nothing asks. An agent installed with npm (copilot or opencode, for example) runs on the
  floor's own Node, the official release, checked against its published checksum. An agent with its
  own installer (claude, for example) comes from the machine's `yolo capture` of that installer, the
  same copy your jails use, so on Linux the first one may run a capture if the machine has none yet.
- **Keeping it current.** The floor updates an agent the way a jail does: at most once an hour, when
  you start it, unless `agent_updates` freezes that pack. It says when it is checking for a newer
  version, so a slow package registry is not a launch that hangs saying nothing.
- **What it cannot hold yet.** On a Mac, yolo has no copy yet of an agent with its own installer;
  `yolo host` runs the one on your PATH and says so. The same happens on Linux for codex, whose
  installer puts its program where a capture cannot record it, for any agent whose vendor
  publishes no build for your machine, and for an installer agent on a machine with no container
  runtime to capture it with.
- **Choosing.** Set `"host_floor": false` in your user config for a floor of nothing, or
  `"host_floor": {"*": true, "claude": false}` to leave one pack out; `yolo host` then runs that
  agent from your PATH. `yolo host apply --assert` removes the floor's copy of an agent you no
  longer select, or have left out; until it does, `yolo host` does not run that copy, and says so
  when it finds no other.

Just before it hands over, `yolo host` prints one line naming what it starts and where it came from,
so a slow start is visibly the agent's.

### Where `yolo host` looks for your tools, and `host_path`

Everything yolo does not keep a copy of, `yolo host` looks for on the PATH it was started with: a
tool a pack needs (`rg` and `fd` for the guardrails pack, anything a pack lists under `requires`),
an agent yolo keeps no copy of, and any other command you run with `yolo host -- <command>`. yolo
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

## Guest (in development)

**Guest** is planned as a middle ground: the agent would run as a separate user account on your
machine, with its own real home and no container, inside the operating system's own sandbox. It is
not available yet; `yolo --at guest` is refused with a message saying so. Until then, use the jail.

## Choosing for one launch

The `confinement` key in your config picks the default, `jail` unless you change it. `--at` picks
for one launch:

```bash
yolo -- claude              # the jail
yolo --at host -- claude    # your own machine, the same as `yolo host -- claude`
```
