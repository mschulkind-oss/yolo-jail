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
inside Apple's built-in sandbox, with no container and no virtual machine. It starts fastest and
needs no runtime, but its boundary is weaker than a container's and several features are missing.
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
