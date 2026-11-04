# Follow an Upstream with a Patch Series

Some programs and pi extensions need a few changes of your own. A fork keeps them, but then every
new upstream release means rebasing the fork again by hand. With a **patch series** instead, you
hand yolo the upstream and your changes, and yolo keeps them current:

- it checks the upstream for new versions, at most once an hour, at a launch;
- it applies your patches to the newest version;
- it builds the result in a sealed jail and runs it;
- when a new version does not take your patches, or does not build with them, it stops and tells
  you what stopped it, how to fix it, and how to run the last build that worked meanwhile.

This works for a program a pack installs, such as `pi`, and for a pi extension. A **patch series**
is a folder of `git format-patch` files, made with `--base` so it records the upstream commit it
was written against. The **good build** is the last build of your series that yolo built and
checked on this machine; it is what runs until a newer one is built.

How it works, and why, is in the
[patched forks reference](https://github.com/mschulkind-oss/yolo-jail/blob/main/docs/reference/patched-forks.md)
and, for pi extensions, the
[pi extensions reference](https://github.com/mschulkind-oss/yolo-jail/blob/main/docs/reference/pi-extensions.md).

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
  On a Mac, `yolo host` builds the folder for the Mac as the sandbox account.
- **Drop the extension's old `git:` entry in the same edit.** pi would otherwise load the extension
  twice, and `yolo pack lint` warns when one list has both.

Use the extension's **key**, `<pack>/<name>` (here `subagents-mine/pi-subagents`, the last part of
`into`), wherever a command below takes a program's name.

## Keep your patches on a branch

If you keep your changes on a branch that you merge the upstream into, name that branch as a
`patch_branch` in place of `patches`. yolo merges it into each new upstream version and builds the result:

```json
{ "kind": "program", "bin": "pi", "via": "source", "fork_of": "pi",
  "source": "git+https://github.com/earendil-works/pi?ref=main",
  "patch_branch": "git+https://github.com/you/pi?ref=patches",
  "build": "npm ci && npm run build && npm install -g \"$(npm pack --silent)\"",
  "produces": [".npm-global/bin/pi", ".npm-global/lib/node_modules/pi-fork"] }
```

- **The branch must contain upstream history.** Start it from an upstream commit and merge the
  upstream into it. Never rebase it.
- **yolo checks a branch at a URL when it checks the upstream**, at most hourly. A branch in a clone
  on this machine is checked at every launch (see below). When you push to the
  branch, the next check rebuilds. `yolo pack update` checks now.
- **When the branch does not merge**, the launch stops and names the conflicting files. It also
  prints the `git merge` commands that fix the branch.
- **Name the published URL, and your local clone is used automatically when it's newer.** Anyone who
  installs your pack by URL then gets a working copy, and on your machine yolo builds from your own
  clone of that repository (see [your clone of a published branch](#your-clone-of-a-published-branch)).
- **A relative path also works, but only on your machine**, such as `"patch_branch": "../..?ref=patches"`
  from a pack inside the repository it patches from. The path starts at the pack's directory and cannot
  climb above the pack's own git repository. Whoever installs the pack by URL has their launch refused,
  and so does a `git+file://` path, so `yolo pack lint` warns of both.
- **A `files` extension takes `patch_branch` the same way.** One contribution names `patches` or
  `patch_branch`, never both.
- **`yolo pack status`** shows the patch branch's commit and how many releases it is behind.

### A branch in a clone on this machine

When the patch branch is in a clone on this machine (a relative path or `git+file://`), the branch
has two copies: your clone's local branch and its remote's. yolo builds from whichever is newest and
tells you which one it used:

- **At every launch, yolo fetches the clone's remote** (`origin`, or the first remote by name) into its
  own copy. It never runs `git fetch` in your clone and never changes it. A push from another machine is
  built at your next launch.
- **No local branch**: the remote's branch builds, and yolo says there is no local branch.
- **A local branch behind the remote**: the remote's branch builds, and yolo says the local branch is
  stale.
- **Local commits you have not pushed**: your local branch builds, and yolo counts the unpushed commits.
- **Your branch and the remote's have diverged**: your local branch builds, with a warning that names both
  commits. If you rebased, push with `git -C <repo> push --force-with-lease -- origin <branch>`.
- **The fetch fails** (offline, no credentials, or it takes too long): yolo still builds, from your local
  branch if there is one. It says the branch is not verified, when it last fetched, and the `git fetch` to
  run yourself. Once you have run it, the next launch builds what you fetched.
- **The branch was deleted on the remote**: your local branch builds, and yolo says it is not on origin.
  With no local branch, yolo builds your clone's last copy of the remote's branch and warns that it is
  stale, with the push that puts it back.

yolo's own fetch never asks for git credentials. At a terminal, `yolo pack update` may still let ssh ask
you something, such as whether to trust a new host.

The launch, `yolo pack update`, `yolo pack series check` and `yolo pack status` all print the same line,
for example `mine  origin 4cd0bc6b  local branch is stale, 1 behind origin  fetched just now`.

### Your clone of a published branch

When a pack names its patch branch by a published URL, and you have a clone of that repository, yolo
builds from your clone by the rules above. The line names the clone it used, for example
`mine  local 9e1f22aa  2 unpushed  fetched just now  local clone /home/you/src/pi (local_clones entry ~/src/pi)`.
This works for any pack, including one you installed from someone else's URL.

Your clone is used only for a branch the published repository has: the patch branch must be a branch name,
and your clone must have fetched it from the remote that matched, so `refs/remotes/origin/mine` exists. That
remote must also fetch from the published repository: if its first URL is your fork, or an `insteadOf`
rewrites it to another host, your clone is not used. Then your clone is built by the rules above: the
published tip when your `mine` is behind, your `mine` when it is ahead, and your `mine` with a warning when
it has diverged or the fetch failed. If the published repository deleted the branch, your `mine` still
builds, with a warning, because the pack names that URL. A branch you have only locally, or only on another
remote such as your fork, is built from the URL as if you had no clone, and the line warns that your clone was
not used and why. If you have pushed the branch, `git -C ~/src/pi fetch origin` lets the next launch use your
clone.

yolo knows a clone in two ways:

- **A pack in it.** Any path your user config reads a pack from that lies inside a git clone registers
  that clone, keyed by its remotes. That includes a pack named by path and the local pack in
  `~/.config/yolo-jail/local`. A pack that is plain files outside any repository registers nothing.
- **`local_clones`**, for a clone that holds no pack, such as your fork of the tool itself:

  ```jsonc
  // ~/.config/yolo-jail/config.jsonc
  { "local_clones": ["~/src/pi"] }
  ```

  It is read from your user config only. Write each entry from `/` or `~/`. An entry that is not an
  absolute path, does not exist, is not in a git clone, or whose clone has no remote stops every command
  that resolves patch branches, naming the entry and how to fix it: `yolo check`, the launch, `yolo host`,
  `yolo host apply`, `yolo pack install`, `update` and `status`, and, for a patch branch named by URL,
  `yolo pack series check` and `yolo pack lint --online`.

The URL matches whichever way the remote is spelled: `git@github.com:you/pi.git` and
`https://github.com/you/pi` are the same repository. When two clones match, a `local_clones` entry wins,
then the first in your config.

## Build a pi extension as it is

An extension you have not changed can be built the same way, with no `patches`: yolo builds it
once per machine, in the same sealed jail, and each new jail gets a read-only copy, so pi in your
jail never installs it, and nothing one jail does to it reaches another. `source` is a git address,
as above, or an npm package:

```json
{
  "name": "my-extensions",
  "contributes": [
    { "kind": "files", "into": ".pi/agent/yolo-ext/pi-web-access",
      "source": "npm:pi-web-access", "fallback": "npm:pi-web-access" },
    { "kind": "config-list", "surface": "pi/settings", "path": "/packages",
      "add": ["~/.pi/agent/yolo-ext/pi-web-access/node_modules/pi-web-access"] }
  ]
}
```

- **An npm source** is `npm:<name>`, optionally with `@` and a version, a range or a dist-tag, as
  npm reads them. yolo picks the version npm would install, on your machine, and installs it in the
  jail with pi's own npm command. One difference: yolo does not pass over a version whose
  `engines` field rules out the jail's Node, which npm would. The list entry is `~/` plus `into`, then `/node_modules/<name>`.
- **A git source** follows its branch's newest commit, as pi does with the same `git:` entry. A tag
  or a commit as the `?ref=` holds it there. With no `build`, yolo runs npm's install of the
  extension's dependencies when it has a `package.json`, as pi would.
- **`fallback`**, optional, is the extension's old entry in pi's own spelling. Wherever a launch has
  no build to hand, such as after a failed first build, pi gets
  that entry instead and installs the extension itself, and the launch says so. Without one, pi does
  not start without the extension, as for a patched one.
- **Drop the extension's old entry** in the same edit, or keep it as the `fallback`.

An extension that writes into its own folder while it runs fails here, since the folder is
read-only. Keep such an extension as a plain entry in pi's list.

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
- **Builds run at once**, patched programs and extensions alike, up to four (fewer on a machine
  with under eight CPUs, one at a time on Apple Container), so a launch waits for the slowest build
  rather than all of them in turn. Their lines print in the order the packs list them, under one
  progress line.
- **One Ctrl-C stops the whole wait**: every patched program and extension starts on its good build,
  and one with no build yet is left out and says so; if a pack you selected needs it, the launch
  then stops before the jail starts, as for any missing build below. When the time runs out, that
  build counts as failed, and the launch stops as for any failed build below.
- **An attach** to a running jail says which build that jail was handed.
- In a jail, a pi extension's folder is read-only, and each launch gets its own copy of the good
  build.

## When your patches stop applying

A patch the new version already contains is dropped by itself, even when upstream changed it
again afterwards, as `git rebase` would; yolo says so and names the file you can delete.

When a new version does not take your patches, yolo stops and prints an error. It names the patch
that failed and the files it conflicts in, then the git commands that fix it, then the ways to keep
going without fixing it that work on your machine, each a command you can copy. A jail launch
prints it as soon as its builds are done, before the jail starts, then stops (the commits and paths
are shortened here):

```text
ERROR: fork pi-mine/pi: patch series does not apply to upstream v1.0.2 (cd32f772)
  Patch: 0001-show-fleet-details.patch
  Conflict: packages/tui/src/footer.ts

Repair:
    git clone https://github.com/you/pi.git pi-mine-pi-rebase && cd pi-mine-pi-rebase
    git checkout -q -b yolo-rebase 5d1e07b2… && git am ~/packs/pi-mine/patches/*.patch && git tag yolo-applied
    git rebase cd32f7729e1b…
  resolve each stop (edit, git add, git rebase --continue), then replace the series — this line changes nothing until the rebase is done:
    test -n "$(git rev-list -n 1 yolo-rebase --not cd32f7729e1b… yolo-applied --)" && mkdir ~/packs/pi-mine/patches.new && git format-patch … && mv … && rm -r ~/packs/pi-mine/patches.old

Start anyway:
  YOLO_ALLOW_PATCH_FAILURES=pi-mine/pi yolo    run v1.0.0 (a13d35a7) + 7 patches
  YOLO_SKIP_PATCHES=pi-mine/pi yolo            run upstream without the patches
```

- **Repair** is the rebase, as below.
- **Start anyway** lists only what works on your machine right now; the next section explains each.
- **Every command stops** at the error, even when a good build is on your machine: a jail launch,
  `yolo host`, `yolo host apply --assert`, `yolo pack update` and `yolo capture`. A command that
  writes into your home, such as `yolo host apply --assert`, stops before it writes anything.
- **A newer release clears it.** When upstream tags a version your patches do apply to, the next
  launch that checks upstream builds that one and the error goes away, with no rebase.
- yolo never runs a build your patches did not fully apply to, and it builds an older version only
  when you ask it to.

### Rebase the series

The Repair lines are plain git, with the commits filled in, for you or an agent to run in any
folder. `yolo pack status` prints them again under the conflict, and `yolo pack series check`
prints them too.

1. The first three lines clone the upstream, apply your patches at the version they were made
   against, and rebase them onto the new version. The rebase stops at each patch that conflicts.
2. Resolve each conflict, `git add` the files, and run `git rebase --continue`. An agent can do this
   for you.
3. Then run the last line. It writes the rebased series over your pack's `patches` folder, and does
   nothing until the rebase is finished, so pasting it early is safe.
4. The next launch builds the new series.

For a pack fetched from git, the lines say `<patches>` instead of a folder: run them against your
own clone of the pack's repository, then commit and push the new series. yolo never writes into
your pack itself.

## Check a series

`yolo pack series check <pack folder>` reports whether the newest upstream version takes each patch series, or which patch conflicts and the git commands that repair it. Patch series are checked in a temporary mirror that is deleted when the command ends; `--onto <tag or commit>` checks that upstream revision instead.

A patch branch uses the current recorded check by default and fetches nothing. If its verdict is missing or stale, run `yolo pack update` on the host or start a launch to record one. Add `--online` for a throwaway scratch check; it does not update the durable record. The command writes nothing to your pack.


## Commands

| Command | What it does |
| :--- | :--- |
| `yolo pack update` | Checks every upstream now, applies each series to the newest version, and says whether it applies or prints the error for the patch that conflicts. It builds nothing: the next launch does. |
| `yolo pack status` | Shows each good build, the newest version and what happened when yolo tried it, what holds it, and when the next check is due. It works offline. |
| `yolo pack status <key>` | Prints the build line in full. A launch names the build line by a short digest instead of printing it each time; the build itself always prints it in full first. |
| `yolo capture <bin>` or `yolo capture <pack>/<name>` | Checks now and builds the newest version if your series applies to it, or rebuilds the good build. It stops at a series that does not apply. |
| `yolo pack series check <pack folder>` | Says whether each patch series applies to the newest version, in a disposable scratch mirror. For a patch branch, reads the current recorded check by default; if there is no current record, run `yolo pack update` on the host or start a launch. Add `--online` for a disposable scratch check that does not update the record. |
| `yolo pack lint [--online] <dir>` | Checks a pack before you select it: each series must be one a launch can read. With `--online` it also checks the upstream in a scratch copy it deletes afterwards: that the ref and the series' base exist, what `follow` finds, and that your patches apply at their base. It works in a jail too. |

To keep running what you have, turn `agent_updates` off for the pack: yolo stops checking upstream
and runs your good build, even after a new version failed. If you own the pack, a tag in `?ref=`
holds it too.

## When a build fails

- **A build of a new version fails:** the command stops, even though your good build is still on
  your machine. yolo never falls back to an older build by itself, since a warning is easy to
  miss. The error names the version and the build's error, with the build's last lines and where
  its whole output is, then `yolo capture` to retry and the ways to start anyway
  ([When a build breaks](#when-a-build-breaks)): `YOLO_USE_CACHED_GOOD=pi-mine/pi yolo` runs your
  good build for that launch. Every launch stops the same way until yolo tries the build again
  after a wait, `yolo capture` retries it, or upstream releases a newer version.

  ```text
  ERROR: fork pi-mine/pi: the build failed at upstream v1.0.2 (cd32f772)
    Build: the capture jail exited 1 — nothing was stored

  Repair:
    read the build's output, and once upstream or the build recipe is fixed, retry it:
      yolo capture pi

  Start anyway:
    YOLO_ALLOW_PATCH_FAILURES=pi-mine/pi yolo    run v1.0.0 (a13d35a7) + 7 patches
    YOLO_USE_CACHED_GOOD=pi-mine/pi yolo         run the last good build v1.0.0 (a13d35a7) + 7 patches
    YOLO_SKIP_PATCHES=pi-mine/pi yolo            run upstream without the patches
  ```

- **yolo itself cannot build the new version:** the same happens when the new version never gets
  to build. Its build jail does not start (on Apple Container, because another jail is running),
  another launch's build of it holds its lock past the 20-minute wait, or yolo cannot apply your
  patches because a git command or the network failed, or your git is older than 2.40. The error says
  `yolo could not build`, or `the patch series could not be replayed`, with the cause on the next
  line and what to fix first. A build jail that did not start stops every launch until yolo tries
  again after a wait or `yolo capture` builds it; a replay that could not run stops every launch
  until the next check, an hour later.

  ```text
  ERROR: fork pi-mine/pi: yolo could not build upstream v1.0.2 (cd32f772)
    Cause: Apple Container has running yolo jail(s): yolo-other-project

  Repair:
    once `container ls` shows no running yolo jail, retry it:
      yolo capture pi

  Start anyway:
    YOLO_ALLOW_PATCH_FAILURES=pi-mine/pi yolo    run v1.0.0 (a13d35a7) + 7 patches
    YOLO_USE_CACHED_GOOD=pi-mine/pi yolo         run the last good build v1.0.0 (a13d35a7) + 7 patches
    YOLO_SKIP_PATCHES=pi-mine/pi yolo            run upstream without the patches
  ```

- **yolo cannot reach upstream:** offline, or the upstream is down. This is not a failure: yolo
  cannot know whether there is a new version, so it runs your good build and says it could not
  check. That holds for `yolo host` too.
- **Your own edit fails:** if you change the series, `build` or `produces` and the result does not
  build, yolo tries your series on its base version. If that fails too, nothing is left to run.
- **Nothing to run:** when a patched program, or a pi extension a pack loads, has no build at all,
  the launch stops before the jail starts, whatever you asked it to run. It says why once, for
  every build the same cause left without one, says whether the fix is yours or a bug in yolo to
  report, then lists `yolo capture <key>` to build it again and the ways to start anyway
  ([When a build breaks](#when-a-build-breaks)). `yolo pack series check` and the rebase it prints
  help when a series no longer applies, and dropping the list entry or the pack runs without it.

```text
ERROR: 2 patched extensions pack pi loads have no build on this machine
  extensions mine/ext-a and mine/ext-b: their build jail refused to start on the host:
      writing pi's automode file (~/.pi/agent/extensions/automode/settings.json), which pack mine declares, failed:
        ~/.pi is mounted read-only in that jail
    This is a bug in yolo, not in pack mine: the build jail refused a config your own launch accepts.
    Report it at https://github.com/mschulkind-oss/yolo-jail/issues, with /home/you/project/.yolo/build-mine--ext-a-1a2b3c4d.log; once it is fixed, retry.

Retry:
  yolo capture mine/ext-a
  yolo capture mine/ext-b

Start anyway:
  YOLO_SKIP_EXTENSIONS=mine/ext-a,mine/ext-b yolo    start pi without it
```

## When a build breaks

When the error or the refusal stops you, put one of these in front of the command for that one
run. Each names what it applies to: `1` for everything, or a comma-separated list of the key the
error prints (`pi-mine/pi`), the pack (`pi-mine`) or the program or extension (`pi`). A name that
matches nothing here only prints a warning, so one left in your shell profile does no harm.

| Command | What you get |
| :--- | :--- |
| `YOLO_ALLOW_PATCH_FAILURES=pi-mine/pi yolo` | Runs the last good build of your current patches, skipping the new version that does not apply or does not build. |
| `YOLO_ALLOW_OLDER_UPSTREAM=pi-mine/pi yolo` | Tries your patches on older upstream releases, newest first, and runs the first one that builds. It says which version it picked, and keeps it on later launches until upstream releases something newer. |
| `YOLO_USE_CACHED_GOOD=pi-mine/pi yolo` | Runs the last good build yolo recorded, for one fresh launch, without checking or building anything: after a new version failed to build, or when your own edit is what broke. It does not include that edit. |
| `YOLO_SKIP_PATCHES=pi-mine/pi yolo` | Runs the upstream program or extension with none of your patches, built and kept separately, so your own good build is untouched. |
| `YOLO_SKIP_EXTENSIONS=mine/ext-a yolo` | Starts pi without that extension. |
| `YOLO_ALLOW_MISSING_PROGRAMS=1 yolo` | Starts the jail without a patched program that has no build. The shell works; the program does not. |

For `yolo host` and `yolo capture`, the error prints the same lines with that command in place of
`yolo`. On an attach to a running jail or inside a jail the variables do nothing; the launch says so.

## Where it works

| Where | Patched program | Patched pi extension |
| :--- | :--- | :--- |
| Jail, Podman (Linux, or a Mac's Podman machine) | Works | Works |
| Jail, Apple Container 1.1.0 or later | Works. A new build cannot start while another jail is running: the launch stops and says so, and `yolo capture` builds it once the other jails stop. To run your good build meanwhile, use `YOLO_USE_CACHED_GOOD`. | Works, the same way |
| Jail, older Apple Container | Doesn't: the launch says so | No new builds; a good build already on this Mac is still copied in |
| Seatbelt setup | Works: the launch builds it for your Mac under the setup's selected account, one build at a time. Until a build exists the launch stops and names a container setup. | Works, the same way: built for your Mac and copied into the sandbox home, read-only |
| `yolo host`, Linux | Works: `yolo host -- <bin>` and `yolo host apply --assert` build and install it | Works: `yolo host apply --assert` builds it and links `~/<into>` to a copy yolo keeps, and so does `yolo host -- pi` with `host_apply_on_launch` on. `yolo host -- pi` does not start pi while an extension it loads has no build. |
| `yolo host`, macOS | Works: built for your Mac in the selected host setup; `host-yolo-account` needs `yolo yolo-account create` first | Works the same way, in the selected host setup |

On a Linux host under host management, `yolo pack update` also runs `yolo host apply --assert`. That
run installs only builds that already exist; it builds nothing new.

Everywhere, applying your patches needs **git 2.40 or newer** on your machine (the host, not the
jail). Run `git --version` to check. Debian 12's and Ubuntu 22.04's git are older, and so is the git
in Apple's Xcode 16 Command Line Tools; Homebrew's `git` is new enough. With an older git, nothing is
built, and the launch stops and says to update git, even when a good build is on your machine.

## Going back to a fork

Remove the patched pack from `packs`, and select your fork pack again.

A yolo older than patch series refuses any pack that uses `patches`, and on the host that refusal
fails **every** launch, not just the patched program's. So take the patched pack out of `packs`
before you go back to an older yolo, and before a machine still on one reads your config.

To check a machine, run `yolo features`. It lists `patch-series` and `patched-extensions` where yolo
reads them, and a yolo without the `features` command is older than both. A yolo that has the command
no longer fails a launch over a pack it cannot fully read: it leaves out the part it cannot read and
says which one.
