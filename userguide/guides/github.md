# GitHub Without a Token in the Jail

The `github` pack lets an agent in a jail use `gh`, the GitHub command-line tool, with **your own
GitHub login on your machine**. The jail never holds a GitHub token. Instead, a small service on
your machine, the **github-broker**, runs each command for the jail and sends back what it printed.

This version is **read-only**. An agent can read pull requests, issues, workflow runs, releases and
files in this project's own GitHub repositories. A command that would change something on GitHub
does not run yet: it stops at once and says that writes need an approval step, which is still
being built, so you run that command on your machine instead.

## Turning it on

You need `gh` installed on your machine and logged in:

```bash
gh auth login          # on your machine, once
```

Then select the pack in your user config (`~/.config/yolo-jail/config.jsonc`):

```jsonc
{
  "packs": ["claude", "github"]
}
```

And in each project that should have the broker, run this on your machine:

```bash
yolo loopholes enable github-broker
```

Selecting the pack puts yolo's `gh` in front of the jail's own copy, in every project. The command
turns the broker on for that one project, from its next fresh launch. It never edits your config:
it writes a small file for the project in `~/.config/yolo-jail/workspaces/` and prints its name.
There is no switch that turns the broker on for every project.

### Only in some projects

The broker runs only in the projects you turned it on in. The command switches the folder you
run it in, which is the project a `yolo` started there opens. To take the broker out of one again,
run `yolo loopholes disable github-broker` there. To name a project from another folder, add
`--workspace`, as in `yolo loopholes enable github-broker --workspace ~/code/app`.

A project's own config can't turn the broker on or off: `yolo check` and the launch refuse a
`github-broker` switch in `yolo-jail.jsonc` or `yolo-jail.local.jsonc`, because an agent can edit
those files, and they refuse one in your user config too. A jail can't read the file the command
writes, so an agent in the jail can't change it either.

If you answer `N` at the repository prompt, the launch stops and names
`yolo loopholes disable github-broker`, which keeps the broker out of that project's next launch,
so it no longer asks.

## The repositories it can reach

The broker only works on **this project's own GitHub repositories**: the `github.com` remotes in
the project's `.git/config`, such as `origin` and `upstream`, and any repository the project's
config lists, [below](#adding-a-repository-the-project-has-no-remote-for). It never reaches your
other repositories, even though your login can.

Because an agent can edit `.git/config` and the project's config, yolo asks you to approve that
list. The first launch of a project with a GitHub remote shows it at the top of the usual
config-change prompt:

```text
⚠  Repository scope changed since last run:

github-broker repository scope, read from /home/you/code/app/.git/config:
  + you/app  remote "origin"  added
  (no scope was approved for this workspace before)

github-broker: 1 added, 0 removed, 0 source changed
Accept these repository scope changes? [y/N]
```

Answer `y` to approve it. After that, yolo asks again only when the list changes, or where a
repository comes from changes: a remote added, removed or renamed, or a repository added to the
config. A change made while a jail is running is not reachable until the next fresh launch, and
only once you approve it there.

Without a terminal, such as in a script, the launch stops and prints the same list. Approve it for
that launch with `--accept-config-changes`, or ahead of time on your machine with
`yolo check --accept-config-changes`.

### Adding a repository the project has no remote for

A project sometimes needs another repository, such as a library it depends on. List it in the
project's `yolo-jail.jsonc`, or in `yolo-jail.local.jsonc` beside it for a repository the project
should not commit:

```jsonc
{
  "brokered": {
    "github": { "repos": ["you/lib"] }
  }
}
```

The agent can add it too. When it asks for a repository outside the list, the refusal tells it
where to add the repository and to ask you to restart the jail. Nothing changes until you do: the
next fresh launch shows the repository as a row of the prompt, naming the file it came from, and
asks you to approve it:

```text
github-broker repository scope, read from /home/you/code/app/.git/config and yolo-jail.jsonc:
    you/app  remote "origin"   unchanged
  + you/lib  yolo-jail.jsonc  added
```

After `y`, that project can reach `you/lib` exactly as it reaches its own remotes, and the launch
names the scope in one line:

```text
github-broker: scope for this workspace: you/app (remote "origin"), you/lib (yolo-jail.jsonc)
```

No other project gets it. yolo does not git-ignore `yolo-jail.local.jsonc`, so check your
`.gitignore` before keeping a private repository there. No entry allows a command across your
whole account, such as a search with no `--repo`. `yolo config-ref` has the details.

The old way, a `brokered.github.workspaces` entry in your user config keyed by the project's
folder, is retired. A user config that still has one stops every launch, and the message names
the entry to put in each project's `yolo-jail.local.jsonc` instead.

## What an agent can run

| The agent runs | What happens |
|---|---|
| `gh pr view 32`, `gh issue list`, `gh run view --log`, `gh workflow list`, `gh repo view`, `gh release list`, `gh api repos/OWNER/REPO/...` and other reads | It runs with your login, and the output comes back as if it ran in the jail |
| `gh pr comment`, `gh issue edit`, `gh pr merge`, `gh api -X POST ...` and other writes | Nothing runs, and nothing waits. It exits with code 77 at once: writes need an approval step, not built yet, so the agent asks you to run the command on your machine |
| `gh auth status` | Says which of your GitHub accounts the broker uses and which repositories the jail can reach. It never shows the token or its scopes |
| `gh auth token`, `--web`, `gh api` to a full URL, a command that would read or write a file on your machine | Never runs, whatever else is allowed. It exits with code 64 and says why |
| A repository outside this project, or a command across your whole account, such as a search with no `--repo` | Never runs. It exits with code 64 and names the repositories it can reach, and, for a repository, [where to add it](#adding-a-repository-the-project-has-no-remote-for) |
| A search whose words could reach another repository: a `repo:`, `org:`, `user:` or `owner:` in the query, a parenthesis, or the word `OR` or `NOT`, in `gh search`, or in `gh pr list`, `gh issue list` or `gh discussion list` with `--search` or a filter | Never runs. It exits with code 64 and says which words. Search with plain words, and filter the `--json` output inside the jail instead |

The repository is the one `-R OWNER/REPO` names, or else the project's `origin` remote. To send
text on standard input, pass `-`, as in `--body-file -`.

For output formatting, `--jq` and `--template` work as they do in `gh`, and so does piping
`--json` output into `jq` inside the jail.

In the jail, `type gh` shows `~/.yolo/bin/block/gh`. That is the pack's forwarder, not a blocked
tool. The jail's own copy of `gh` is still there, holding no login: run it with
`YOLO_BYPASS_SHIMS=1 gh ...`.

## What it records

Every command the broker is sent is written to a log on your machine, whether it ran or not. yolo
mounts that log into no jail. If a `mounts` entry of your own reaches it, yolo allows the mount
and warns you that the jail can read it. To read the log:

```bash
yolo audit                       # every call, oldest first
yolo audit --since 1h            # the last hour
yolo audit --set refused         # refused for the credential or for your machine
yolo audit --set out-of-scope    # refused because it reached outside the project
yolo audit --json                # one JSON object per call
```

A command a jail sent can contain any characters. `yolo audit` shows control characters as escapes,
such as `\x1b`, so a command cannot change what your terminal shows.

## Where it works

| Setup | |
|---|---|
| Podman on Linux | Works |
| Podman on a Mac | Should work; not yet run end to end on a Mac |
| Apple Container | Does not work: a jail there cannot connect back to your Mac, and the launch says so |
| `macos-user` | Starts, but not yet run on a Mac |
| `yolo host` | Not offered: there the agent already is you, and can run your own `gh` |

## What it does not protect against

- **What a read returns.** An agent can read a private repository in the project's scope and send
  what it read elsewhere over its own network.
- **`git push` and `git fetch`.** They do not go through the broker. They use whatever credential the
  jail has, or none.
- **A token you give the jail yourself**, such as a `GH_TOKEN` in `env_sources` or in a file in the
  project. A `gh` that finds one uses it directly.
- **Programs running as you on your machine.** The broker keeps the token out of the jail; it is not
  a guard against your own account.

## If something goes wrong

- **`gh` exits 69** saying the jail has no github-broker endpoint: the broker is off in this
  project, or the jail was started before you turned it on. Run the command the message prints,
  `yolo loopholes enable github-broker`, on your machine, and start a fresh jail.
- **`gh` exits 69** saying the host has no `gh` or no login: install `gh` on your machine and run
  `gh auth status` there.
- **`gh` exits 69** saying the broker will not run the host `gh`: the first `gh` on your `PATH` is
  inside the project or inside yolo's jail home, where an agent could have put it. The broker
  also skips relative `PATH` entries such as `./bin`. Make sure the `gh` you installed comes first
  on your `PATH`, from a directory outside the project.
- **`yolo check`** shows the broker's own check: which `gh` it would run, its version, and whether
  it found a login.
