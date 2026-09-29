# GitHub Without a Token in the Jail

The `github` pack lets an agent in a jail use `gh`, the GitHub command-line tool, with **your own
GitHub login on your machine**. The jail never holds a GitHub token. Instead, a small service on
your machine, the **github-broker**, runs each command for the jail and sends back what it printed.

This version is **read-only**. An agent can read pull requests, issues, workflow runs, releases and
files in this project's own GitHub repositories. A command that would change something on GitHub
does not run yet; it stops and says that writes need an approval step, which is still being built.

## Turning it on

You need `gh` installed on your machine and logged in:

```bash
gh auth login          # on your machine, once
```

Then select the pack and switch its loophole on, in your user config
(`~/.config/yolo-jail/config.jsonc`):

```jsonc
{
  "packs": ["claude", "github"],
  "loopholes": {
    "github-broker": { "enabled": true }
  }
}
```

Selecting the pack puts yolo's `gh` in front of the jail's own copy. Enabling the loophole starts the
broker for each jail you launch. Both take effect at the next fresh launch.

## The repositories it can reach

The broker only works on **this project's own GitHub repositories**: the `github.com` remotes in
the project's `.git/config`, such as `origin` and `upstream`. It never reaches your other
repositories, even though your login can.

Because an agent can edit `.git/config`, yolo asks you to approve that list. The first launch of a
project with a GitHub remote shows it at the top of the usual config-change prompt:

```text
⚠  Repository scope changed since last run:

github-broker repository scope, read from /home/you/code/app/.git/config:
  + you/app  remote "origin"  added
  (no scope was approved for this workspace before)

Accept these repository scope changes? [y/N]
```

Answer `y` to approve it. After that, yolo asks again only when the remotes change, for example
after `git remote add`. A remote added while a jail is running is not reachable until the next fresh
launch, and only once you approve it there.

Without a terminal, such as in a script, the launch stops and prints the same list. Approve it for
that launch with `--accept-config-changes`, or ahead of time on your machine with
`yolo check --accept-config-changes`.

## What an agent can run

| The agent runs | What happens |
|---|---|
| `gh pr view 32`, `gh issue list`, `gh run view --log`, `gh workflow list`, `gh repo view`, `gh release list`, `gh api repos/OWNER/REPO/...` and other reads | It runs with your login, and the output comes back as if it ran in the jail |
| `gh pr comment`, `gh issue edit`, `gh pr merge`, `gh api -X POST ...` and other writes | Nothing runs. It exits with code 77: writes need an approval step, not built yet |
| `gh auth token`, `--jq`, `--web`, `gh api` to a full URL, a command that would read or write a file on your machine | Never runs, whatever else is allowed. It exits with code 64 and says why |
| A repository outside this project, or a command across your whole account, such as a search with no `--repo` | Never runs. It exits with code 64 and names the repositories it can reach |

The repository is the one `-R OWNER/REPO` names, or else the project's `origin` remote. To send
text on standard input, pass `-`, as in `--body-file -`.

For output formatting, use `--json` and pipe it into `jq` inside the jail. `--jq` and `--template`
are refused because they could read your machine's environment, where the token lives.

The jail's own copy of `gh` is still there, holding no login: run it with
`YOLO_BYPASS_SHIMS=1 gh ...`.

## What it records

Every command the broker is sent is written to a log on your machine, whether it ran or not. The
log is never visible inside any jail. To read it:

```bash
yolo audit                       # every call, oldest first
yolo audit --since 1h            # the last hour
yolo audit --set refused         # only what was refused
yolo audit --json                # one JSON object per call
```

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

- **`gh` exits 69** saying the jail has no github-broker endpoint: the loophole is off, or the jail
  was started before you turned it on. Turn it on as above and start a fresh jail.
- **`gh` exits 69** saying the host has no `gh` or no login: install `gh` on your machine and run
  `gh auth status` there.
- **`yolo check`** shows the broker's own check: which `gh` it would run, its version, and whether
  it found a login.
