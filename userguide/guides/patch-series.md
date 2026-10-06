# Follow an Upstream with a Patch Series

Some programs and pi extensions need a few changes of your own. A fork keeps them, but then every
new upstream release means rebasing the fork again by hand. With a **patch series** instead, you
hand yolo the upstream and your changes, and yolo keeps them current:

- it checks the upstream for new versions, at most once an hour, at a launch;
- it applies your patches to the newest version they apply to cleanly;
- it builds the result in a sealed jail and runs it;
- when a new version does not take your patches, it keeps running the last build that worked and
  tells you which patch stopped it.

This works for a program a pack installs, such as `pi`, and for a pi extension. A **patch series**
is a folder of `git format-patch` files, made with `--base` so it records the upstream commit it
was written against. The **good build** is the last build of your series that yolo built and
checked on this machine; it is what runs until a newer one is built.

## Patch a program

Export your changes from a checkout of your fork, naming the upstream commit they start from:

```bash
git format-patch --base=$(git merge-base upstream/main HEAD) -o ~/code/pi-mine/patches upstream/main..HEAD
```

Exporting the same changes again later, from amended commits or with another version of git, is
not a change to your series: yolo keeps the build it has.

Then write a pack beside the series. It looks like a [fork pack](packs-and-skills.md#run-your-own-fork-of-a-program),
except that `source` names the **upstream**, not your fork, and `patches` names the series:

```json
{
  "name": "pi-mine",
  "contributes": [
    { "kind": "program", "bin": "pi", "via": "source", "fork_of": "pi",
      "source": "git+https://github.com/earendil-works/pi?ref=main",
      "patches": "patches",
      "build": "npm ci && npm run build && npm install -g \"$(npm pack --silent)\"",
      "produces": [".npm-global/bin/pi", ".npm-global/lib/node_modules/pi-fork"] }
  ]
}
```

Check it with `yolo pack lint --online ~/code/pi-mine`, then select it as you would any pack:
`"packs": ["pi", "~/code/pi-mine"]`.

- **`?ref=`** names the upstream branch to follow. yolo follows that branch's newest version tag.
  On a branch with no version tags, yolo builds your series on the commit it starts from, keeps it
  there until a tag appears, and says so. Add `"follow": "head"` to follow every commit instead, or
  `"follow": "release:<prefix>"` for tags with a prefix.
- **A tag or a commit in `?ref=`** holds the program at that version: yolo still applies your
  patches to it and builds it, but never moves it.
- **`build` and `produces`** work as they do for a fork pack.

Nothing is pinned in a lock file. Each machine checks the upstream and builds its own good build.

## Patch a pi extension

A pi extension is a folder pi loads, not a program. yolo builds it the same way and puts a
read-only copy where pi loads it as a local package, so pi never installs or updates it itself.

Export the extension fork's changes as above, then declare it in a pack, together with the line in
pi's `packages` list that makes pi load it:

```json
{
  "name": "subagents-mine",
  "contributes": [
    { "kind": "files", "into": ".pi/agent/yolo-patched/pi-subagents",
      "source": "git+https://github.com/nicobailon/pi-subagents?ref=main",
      "patches": "patches",
      "build": "npm install --omit=dev --legacy-peer-deps --ignore-scripts" },
    { "kind": "config-list", "surface": "pi/settings", "path": "/packages",
      "add": ["~/.pi/agent/yolo-patched/pi-subagents"] }
  ]
}
```

- **`into`** is where the built folder lands in the home. Keep it outside
  `~/.pi/agent/extensions/`, where pi would also find it by itself.
- **`build`** is optional: it runs in the extension's checkout, and whatever is in the checkout
  afterwards is the folder pi loads. `produces`, also optional, lists files that folder must have.
- **The list entry is `~/` plus `into`**, or a folder inside it, such as one package of a
  monorepo: `~/.pi/agent/yolo-patched/pi-archimedes/packages/session-name`. Write it with no slash
  at the end, which pi-subagents' MCP setup needs. Without one, the folder is built and mounted but
  pi never loads it, and `yolo pack lint` and each launch warn you.
- **The folder is built only where the entry reaches.** An entry in the `autonomy` kind's `guarded`
  list reaches `yolo host` alone, so `yolo host apply --assert` builds and installs the folder and
  no jail does. One in its `autonomous` list reaches jails alone, so `yolo host` installs nothing.
  A Mac builds no folder for `yolo host`, so there a `guarded` entry reaches nothing that has it.
- **Drop the extension's old `git:` entry in the same edit.** pi would otherwise load the extension
  twice, and `yolo pack lint` warns when one list has both.

Use the extension's **key**, `<pack>/<name>` (here `subagents-mine/pi-subagents`, the last part of
`into`), wherever a command below takes a program's name.

## What a launch shows

Every launch lists each patched program and extension, with the series and the build it runs:

```text
Forks this launch:
  fork pi-mine: pi (in place of pack pi's) is a patched fork of git+https://github.com/earendil-works/pi?ref=main + 7 patches (series 2544fe91), at v1.0.0 (a13d35a7)
Patched extensions this launch:
  extension subagents-mine/pi-subagents: ~/.pi/agent/yolo-patched/pi-subagents, a patched extension of git+https://github.com/nicobailon/pi-subagents?ref=main + 8 patches (series ce6a681d), at 6f1027f7
```

The pack banner also says, for each one, that the upstream's new code arrives unreviewed and is
built on your machine.

When yolo builds something new, the launch builds every patched program and extension at once and
shows one line while they run, then says what the jail runs, once for each. Before a build starts,
it names what it builds, the exact build command and what the sealed jail is denied:

```text
build fork pi-mine/pi: v1.0.0 (a13d35a7) + 7 patches (series 2544fe91), the first build of it on this machine; log: .yolo/build-pi-mine--pi-5b0e2d1c.log
  sealed: no credential, no host file, no env_sources, and a bridged network, never the host's; it runs: npm ci && npm run build && npm install -g "$(npm pack --silent)"
built fork pi-mine/pi: v1.0.0 (a13d35a7) + 7 patches; this jail runs it — store key 0ef427ce4a347926, 4210 paths, 138 MB (52.0s)
```

A later launch that moves to a new version says `updated fork pi-mine/pi: v1.0.0 (a13d35a7) →
v1.0.2 (cd32f772), 7 patches; this jail runs the new build`. Everything the build printed is in
the build's own log, named on its first line, and in `.yolo/launch.log`. A build started from inside a
jail says it uses that jail's network rather than a bridged one, since a nested jail cannot have
its own.

- **The first build** happens at the first launch, which waits for it.
- **A newer version** is built at a later launch, which waits up to 20 minutes for each build.
- **Builds run at once**, up to four, or one at a time on Apple Container, so a launch waits for the
  slowest build rather than all of them in turn.
- **One Ctrl-C stops the whole wait**: every patched program and extension starts on its good build,
  one with no build yet is left out and says so, and a later launch tries again. When the time runs
  out, that build counts as failed and the jail starts on the good build.
- **An attach** to a running jail says which build that jail was handed.
- In a jail, a pi extension's folder is read-only, and each launch gets its own copy of the good
  build.

## When your patches stop applying

When a new version does not take your patches, nothing breaks. The good build keeps running, and its
line in the launch ends with what holds it and what to run:

```text
  fork pi-mine: pi (in place of pack pi's) is a patched fork of … + 7 patches (series 2544fe91), at v1.0.0 (a13d35a7); held at v1.0.0 (a13d35a7): upstream v1.0.2 (cd32f772) does not take 0001-show-fleet-details.patch — `yolo pack rebase pi-mine/pi`
```

yolo also walks back through older versions and builds the newest one your patches do apply to,
if it is newer than the good build.

`yolo pack rebase <key>` sets up the rebase for you. Run it on your machine; in a jail, see
[Check or rebase a series in a jail](#check-or-rebase-a-series-in-a-jail):

```bash
yolo pack rebase pi-mine/pi
```

1. It clones the upstream into `./pi-mine-pi-rebase` (`--into <dir>` picks another folder),
   applies your patches, and stops at the patch that conflicts, with the conflict markers in place.
2. You resolve each conflict and run the `git rebase --continue` line it prints. You can also start
   an agent there, `cd <dir> && yolo`, to resolve them.
3. Then run the export line it prints. That one line writes the rebased series over your pack's
   `patches` folder, and does nothing until the rebase is finished. For a pack fetched from git, the
   printed lines instead commit the new series to a clone of the pack's repository and push it.
4. The next launch builds the new series.

`--onto <tag or commit>` rebases onto another version, and `--restart` starts a clone over. Running
the command again on its clone prints its next steps again. It never writes into your pack itself.

## Check or rebase a series in a jail

A jail cannot see your machine's copy of the upstream, or what yolo recorded about it. So two
commands work from the pack's own folder instead, with a copy of the upstream they fetch for
themselves and delete when they finish. Both also work on your machine.

- **`yolo pack series check <pack folder>`** says whether each series in the pack still applies: it
  applies, or the first patch that conflicts and its files. That is what `yolo pack status` would
  show for the same series. `--onto <tag or commit>` checks that version instead of the newest.
- **`yolo pack rebase <key> --pack <pack folder>`** sets up the rebase as above, onto the newest
  version or `--onto`. In a jail the pack folder must be inside the workspace. The export line it
  prints writes the new series into that folder, as it would on your machine.

Neither command writes into your pack itself, or changes what a launch runs.

## Commands

| Command | What it does |
| :--- | :--- |
| `yolo pack update` | Checks every upstream now, applies each series to the newest version, and says whether it applies or which patch conflicts. It builds nothing: the next launch does. |
| `yolo pack status` | Shows each good build, the newest version and what happened when yolo tried it, what holds it, and when the next check is due. It works offline. |
| `yolo capture <bin>` or `yolo capture <pack>/<name>` | Checks now and builds the newest version that applies, or rebuilds the good build. |
| `yolo pack rebase <key>` | Sets up a rebase of the series onto a version it does not apply to, as above. Add `--pack <pack folder>` to run it from the pack's folder, in a jail too. |
| `yolo pack series check <pack folder>` | Says whether each series in a pack folder applies to the newest version, or which patch conflicts. It works in a jail. |
| `yolo pack lint [--online] <dir>` | Checks a pack before you select it: each series must be one a launch can read. With `--online` it also checks the upstream in a scratch copy it deletes afterwards: that the ref and the series' base exist, what `follow` finds, and that your patches apply at their base. It works in a jail too. |

To keep running what you have, turn `agent_updates` off for the pack, or put a tag in `?ref=`.

## When a build fails

- **A build of a new version fails:** the good build keeps running, the failure is said once with
  the build's last lines and where its whole output is, and yolo tries again after a wait.
  `yolo capture` retries at once.
- **Your own edit fails:** if you change the series, `build` or `produces` and the result does not
  build, yolo tries your series on its base version. If that fails too, the program is missing
  until you fix or revert the edit, and the launch says so.
- **A pi extension with no build:** pi does not start without an extension it is set to load. In
  the jail, running `pi` says which extension has no build and why, and that you can drop its list
  entry to run without it. The shell is unaffected. A `yolo -- pi` launch says so too, before the
  jail starts.

## Where it works

| Where | Patched program | Patched pi extension |
| :--- | :--- | :--- |
| Jail, Podman (Linux, or a Mac's Podman machine) | Works | Works |
| Jail, Apple Container 1.1.0 or later | Works. A new build cannot start while another jail is running: the good build runs meanwhile, and `yolo capture` builds it once the other jails stop. | Works, the same way |
| Jail, older Apple Container | Doesn't: the launch says so | No new builds; a good build already on this Mac is still copied in |
| `macos-user` | Doesn't yet. The launch says so and names a container backend, where it works. | Doesn't yet; the same line |
| `yolo host`, Linux | Works: `yolo host -- <bin>` and `yolo host apply --assert` build and install it | Works: `yolo host apply --assert` builds it and links `~/<into>` to a copy yolo keeps, and so does `yolo host -- pi` with `host_apply_on_launch` on. `yolo host -- pi` does not start pi while an extension it loads has no build. |
| `yolo host`, macOS | Doesn't: builds are for Linux. The line names a jail that has it. | Doesn't; the line names `YOLO_RUNTIME=podman yolo -- pi` |

On a Linux host under host management, `yolo pack update` also runs `yolo host apply --assert`. That
run installs only builds that already exist; it builds nothing new.

Everywhere, applying your patches needs **git 2.40 or newer** on your machine (the host, not the
jail). Run `git --version` to check. Debian 12's and Ubuntu 22.04's git are older, and so is the git
in Apple's Xcode 16 Command Line Tools; Homebrew's `git` is new enough. With an older git, nothing is
built, and the launch says to update git.

## Going back to a fork

Remove the patched pack from `packs`, and select your fork pack again.

A yolo older than patch series refuses any pack that uses `patches`, and on the host that refusal
fails **every** launch, not just the patched program's. So take the patched pack out of `packs`
before you go back to an older yolo, and before a machine still on one reads your config.

To check a machine, run `yolo features`. It lists `patch-series` and `patched-extensions` where yolo
reads them, and a yolo without the `features` command is older than both. A yolo that has the command
no longer fails a launch over a pack it cannot fully read: it leaves out the part it cannot read and
says which one.
