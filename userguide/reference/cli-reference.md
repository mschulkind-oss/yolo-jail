# CLI Reference

The commands you are most likely to need, grouped by task. Every command has its own `--help`,
which is the complete reference for its flags, and `yolo --help` lists them all.

## Run agents

```bash
yolo                         # a shell in this project's jail
yolo -- claude               # run a command in the jail; everything after -- is the command's
yolo -p zai -- claude        # the same, on a provider profile, for this launch only
yolo apply                   # start the jail, install every agent its packs select, then exit
yolo stop                    # stop this project's jail; the next launch starts fresh
yolo ps                      # the running jails, and the project each belongs to
```

Running `yolo` in a project whose jail is already running **joins** that jail instead of starting a
new one, and the joined jail keeps the config it started with. To pick up a config change, run
`yolo stop`, then launch again.

A jail that starts installs every agent and tool its packs select before your command runs, and
only the first time in each project. If one cannot be installed, offline included, the launch stops
and says which and why; `YOLO_ALLOW_MISSING_PROGRAMS=1 yolo …` starts the jail without it, and it
installs the first time you run it. On `macos-user` each agent still installs the first time you
run it.

Launch flags, placed before `--`:

| Flag | What it does |
|---|---|
| `-p <profile>`, `--profile <profile>` | Select a provider profile for this launch; see [Providers and Models](../guides/providers-and-models.md) |
| `--with-credentials <provider,…\|all>` | Start the jail holding those providers' keys from `env_sources`, keys only, for every process and later session in it; see [Give a shell a provider's key](../guides/providers-and-models.md#give-a-shell-a-providers-key) |
| `--accept-config-changes` | Approve a changed project config on a launch with no terminal, such as CI; see [Approving config changes](configuration.md#approving-config-changes) |
| `--network bridge\|host` | Choose the network mode for a jail this launch starts, overriding the config's `network.mode` |
| `--at jail\|host` | Choose where to run: `--at host` is the same as `yolo host` |
| `--timing` | Report where the launch spent its time |

## Set up and check

```bash
yolo init-user-config        # create ~/.config/yolo-jail/config.jsonc, full of commented examples
yolo init                    # create this project's yolo-jail.jsonc, and gitignore .yolo/
yolo check                   # check Nix, the runtime, your config, and build the image
yolo check --no-build        # the fast version; the one to run inside a jail
yolo config-ref              # every config key, with types, defaults and examples
yolo update --check          # is a newer yolo out? installs nothing
yolo update                  # install it the same way yolo was installed
```

`yolo doctor` is another name for `yolo check`. Run `yolo check` after every config edit.
[Upgrade](../getting-started.md#upgrade) covers `yolo update` for each way of installing yolo.

## Inspect what yolo builds

```bash
yolo describe                # the whole resolved environment, in one summary
yolo describe --json         # the merged config, for a script or an agent
yolo config ls               # every settings file yolo writes for your agents
yolo config render claude    # what a launch would write for Claude, without writing it
yolo config drift            # inside a jail: does the project config differ from the running one?
```

[Settings Across Agents](../guides/agent-settings.md) covers `yolo config diff`, `reset` and
`promote`.

## Packs

```bash
yolo pack ls                 # the packs you selected, and what each delivers
yolo pack footprint <pack>   # everything a pack claims, before you select it
yolo pack install            # fetch every git pack now
yolo pack update             # re-fetch git packs, and update agents installed from npm
yolo pack status             # the commits you are pinned to
yolo pack init <dir>         # start a pack of your own
yolo pack lint <dir>         # check it
yolo features                # what this yolo can read in a pack, such as patch-series
```

A launch leaves out any part of a pack this yolo cannot read, such as a field a newer yolo added,
and says which part; `yolo pack lint` refuses the same field, so a misspelling is still caught.

See [Packs and Skills](../guides/packs-and-skills.md) and
[Writing Your Own Pack](../guides/migrating-to-packs.md).

## Your own machine

```bash
yolo host apply              # what would change in your real home; writes nothing
yolo host apply --assert     # write it
yolo host apply --revert     # what removing yolo's keys would do; add --assert to do it
yolo host -- claude          # run a host agent with its keys and profile
yolo check-deps              # which tools your packs need that your host lacks
```

See [Writing Your Own Pack](../guides/migrating-to-packs.md#part-2--manage-your-host).

## Logins and host services

```bash
yolo broker status           # the shared Claude login service; also stop, restart, logs
yolo openai-auth status      # the shared ChatGPT login; also import --from, logout
yolo host-daemon status      # every host service shared by all jails
yolo loopholes list          # the loopholes your config selects, and whether each is on
yolo loopholes status        # on the host: run each loophole's self-check
yolo audit                   # on the host: every brokered call, such as the jail's gh commands
```

`yolo audit` takes `--since 1h`, `--jail <id>`, `--workspace <path>`, `--set <name>` and `--json`.
Inside a jail, `yolo gh -- <args>` is what a bare `gh` runs when the `github` pack is selected; see
[GitHub](../guides/github.md).

`yolo openai-auth` and `yolo host-daemon` arrive in the release after 0.10.0. On 0.10.0 the first is
`yolo internal openai-auth`, and the second has no equivalent beyond `yolo broker`.

See [Logins](../guides/authentication.md) and [Host Access and Loopholes](../guides/loopholes.md).

## Disk

```bash
yolo stores                  # everything yolo keeps on disk, its size, and what reclaims it
yolo prune                   # what yolo would reclaim; deletes nothing
yolo prune --apply           # reclaim it
```

See [Storage](../guides/storage.md).

## Inside a jail

```bash
yolo programs ls             # installed programs no pack asks for any more, with sizes
yolo programs remove         # what removing them would delete; add --apply to do it
```

Dropping a pack removes its launcher but not the program it installed, so these clean up what is
left. Setting `"programs": { "autoprune": true }` in your user config removes them at every launch
instead; it is off by default and cannot be undone, so read `yolo programs ls` first.

**`yolo capture <agent>`** records what an agent's vendor installer leaves behind, once per
machine, so every other project puts that copy in place instead of downloading it again. A launch
does this by itself the first time it needs an agent; set `YOLO_NO_AUTO_CAPTURE=1` to skip it, for
example on a metered connection. Old recordings are reclaimed by `yolo prune`.

## macOS

```bash
yolo macos-setup             # create the hidden user the macos-user sandbox runs as
yolo macos-teardown          # remove it again
yolo macos-fix-permissions   # repair sharing on a macos-user project
yolo macos-unshare <dir>     # remove sharing from a project
```

See [macOS](../guides/macos.md#the-macos-user-backend).

## Output

**The first line.** Every `yolo` command first prints one line to stderr naming its version, the
platform, and whether it runs on the host or inside a jail:

```text
yolo-jail 0.10.0 | darwin/arm64 | host
```

Include it in a bug report; `yolo check 2>&1 | pbcopy` captures it on a Mac. It never goes to
stdout, so commands whose output is JSON are unaffected. Inside a jail it shows the version of the
yolo that started the jail. Set `YOLO_NO_BANNER=1` to turn it off.

**Color.** Commands color their output in a terminal and print plain text into a pipe or a file.
Set `NO_COLOR=1` to turn color off everywhere, including inside the jails you launch, following the
[NO_COLOR](https://no-color.org) convention.
