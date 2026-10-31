# GitHub Without a Token in the Jail

The `github` pack lets an agent in a jail use `gh`, the GitHub command-line tool, with **your own
GitHub login on your machine**. The jail never holds a GitHub token. Instead, a small service on
your machine, the **github-broker**, runs each command for the jail and sends back what it printed.

The broker supports **host-approved, exact, one-use writes** as well as reads. A write does not run
until you approve its pending request with `yolo approve <id>` on the host. The approval binds the
canonical command, repository and standard-input digest; it covers one use for up to 15 minutes.
The jail waits briefly for your decision. If it times out, the request remains visible to the host
and you can approve it before the agent retries the same command. GitHub does not accept set-wide
`--for` or `--add` decisions. Standing `read-write` policy is refused; remove that entry from
`brokered.github.sets` to require one Allow-once decision per write. Standing read access is unchanged.

A configured `on_request` hook may notify you, but it is optional and its output is never consent.
Pending writes are visible in the host broker log and `yolo approve`; without a hook, run
`yolo approve <id>` on the host to Allow once or `yolo approve <id> --deny`.

## Turning it on

You need `gh` installed on your machine and logged in:

```bash
gh auth login          # on your machine, once
```

Then select the pack, and grant read access, in your user config
(`~/.config/yolo-jail/config.jsonc`):

```jsonc
{
  "packs": ["claude", "github"],
  "brokered": { "github": { "grants": [{ "kind": "read", "repo": "*" }] } }
}
```

> [!NOTE]
> **With a TOML config, run commands instead of editing these blocks.** On your machine,
> `yolo pack add github` selects the pack and `yolo allow github read` grants the read. A project
> asks for the other things with `yolo request github repos you/lib`, `yolo request github create_pr`
> and so on, once your config allows them (`yolo allow github on_request create_pr`). The JSON
> blocks in this guide show the older `brokered` spelling, which `yolo config migrate` converts to
> `[allow.github]` and `[requests.github]`.

Selecting the pack puts yolo's `gh` in front of the jail's own copy and starts the broker, in
every project, and on its own it **grants nothing**: without the read grant, every call is
refused, naming the grant to add. The read grant lets each project read its own repositories,
[below](#the-repositories-it-can-reach); `"*"` means each project's own, never every repository
your login can reach.

The grant never takes effect unseen. Each project's next config-change prompt, or
`yolo config apply` run on your machine in the project, shows it as a line of the
repository-scope block, marked as coming from your user config, beside the remotes it covers,
and asks you to approve it. A running jail's broker takes it at its next call, with no restart.
Without the read grant, the prompt asks nothing about the broker at all; withdrawing the grant
shows the remotes and the grant removed once, and after that the prompt is silent again.

### Turning it off

The broker has no on/off switch: `yolo loopholes enable github-broker` refuses, and `yolo check`
and the launch refuse a `github-broker` switch in your user config or in a project's
`yolo-jail.jsonc` or `yolo-jail.local.jsonc`. To stop the broker, take `github` out of `packs`.
An older yolo's per-project switch is ignored with a warning; `yolo loopholes disable
github-broker` removes it.

If you answer `N` at the repository prompt, the launch stops and says the same: take the pack out
of `packs`, or the next launch asks again.

## The repositories it can reach

The broker only works on **this project's own GitHub repositories**: the `github.com` remotes in
the project's `.git/config`, such as `origin` and `upstream`, and any repository the project's
config lists, [below](#adding-a-repository-the-project-has-no-remote-for) — a specific one, or,
with a wildcard, every repository it can reach. With no such entry it never reaches your other
repositories, even though your login can.

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

A project sometimes needs another repository, such as a library it depends on. Run
`yolo request github repos you/lib` in the project (add `--dry-run` to see the edit first). It adds the
repository to the project's config, runs `yolo check`, and names the host step, `yolo config apply`.
In a legacy JSONC project you can list it by hand, in `yolo-jail.jsonc`, or in
`yolo-jail.local.jsonc` beside it for a repository the project should not commit:

```jsonc
{
  "brokered": {
    "github": { "repos": ["you/lib"] }
  }
}
```

The agent can add it too. When it asks for a repository outside the list, the refusal tells it
to run `yolo request github repos <owner/repo>` and to ask you to run `yolo config apply` on the host. Nothing changes
until you do: that command, or the next launch, shows the repository as a row of the prompt,
naming the file it came from, and asks you to approve it. A running jail takes it at once, with no
restart:

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

### Every repository, or every repository of one owner

When a project needs reach beyond a fixed list, list a wildcard instead. `*/*` is every
repository your login can reach, `you/*` is every repository under one owner, and `*/lib` is that
repository name under any owner:

```jsonc
{
  "brokered": {
    "github": { "repos": ["*/*"] }
  }
}
```

A wildcard is a whole segment between the slashes: `*/*`, `you/*` and `*/lib` are entries, and a
partial glob such as `you*/*` is a mistake that `yolo check` refuses.

It is approved the same way, and the prompt says what you are approving rather than leaving you to
decode the token:

```text
  + */*  yolo-jail.jsonc  added (every repository on github.com)
```

A wildcard widens which repositories the agent can reach, and nothing else. It stays read-only:
a command that would change something still does not run. It also does not allow a command that
acts across your whole account rather than on a repository, such as `gh search code` with no
`--repo`, `gh repo list` or `gh status` — those are refused whatever the scope holds.

The wildcard is a workspace entry like any other: the project's own config file, or the local one
beside it, honored only from files inside the workspace, and taking effect at the next fresh
launch once you approve it.

The old way, a `brokered.github.workspaces` entry in your user config keyed by the project's
folder, is retired. A user config that still has one stops every launch, and the message names
the entry to put in each project's `yolo-jail.local.jsonc` instead.

## What an agent can run

| The agent runs | What happens |
|---|---|
| `gh pr view 32`, `gh issue list`, `gh run view --log`, `gh workflow list`, `gh repo view`, `gh release list`, `gh api repos/OWNER/REPO/...` and other reads | It runs with your login, and the output comes back as if it ran in the jail |
| `gh pr create` or `gh issue create` under a `create-pr` or `create-issue` grant, and `gh pr comment 32`, `gh pr edit 32`, `gh pr review 32`, `gh issue close 32` and other writes to a PR or issue in the project's working set | It runs without asking. What the agent creates joins the working set, and a PR leaves it when it is merged. See [the working set](#the-working-set) |
| `gh workflow run nightly.yml` under a `dispatch-workflow` grant naming that workflow, on the repository's default branch | It runs without asking. See [running a workflow](#running-a-workflow) |
| `gh pr merge`, and other in-scope writes nothing above covers | A request is filed and the broker waits briefly for host approval. Run `yolo approve <id>` on the host to allow that exact command once, or `yolo approve <id> --deny`; without a decision the command does not run and the agent can retry after approval |
| `gh auth status` | Says which of your GitHub accounts the broker uses and which repositories the jail can reach. It never shows the token or its scopes |
| `gh auth token`, `--web`, `gh api` to a full URL, a command that would read or write a file on your machine | Never runs, whatever else is allowed. It exits with code 64 and says why |
| A repository outside this project, or a command across your whole account, such as a search with no `--repo` | Never runs. It exits with code 64 and names the repositories it can reach, and, for a repository, [where to add it](#adding-a-repository-the-project-has-no-remote-for) |
| A search whose words could reach another repository: a `repo:`, `org:`, `user:` or `owner:` in the query, a parenthesis, or the word `OR` or `NOT`, in `gh search`, or in `gh pr list`, `gh issue list` or `gh discussion list` with `--search` or a filter | Never runs. It exits with code 64 and says which words. Search with plain words, and filter the `--json` output inside the jail instead |

The repository is the one `-R OWNER/REPO` names, or else the project's `origin` remote. To send
text on standard input, pass `-`, as in `--body-file -`.

`yolo approve` is the host answer path, with or without a configured notification hook. Each GitHub
Allow once binds the canonical argv, resolved repository and stdin digest; it is one use and expires
after 15 minutes. `yolo approve <id> --add` instead adds the PR or issue the write targets to the
project's working set, and `yolo approve <id> --for 15m` allows that kind of write on it (or, for
a creation or a repository-level write, in its repository) for that long, at most 12 hours; a
merge takes Allow once or `--for`, never `--add`. A write that names a PR by branch rather than
number takes Allow once only. A legacy set-wide grant is not spent for a GitHub write.

### The working set

The working set is the PRs and issues the agent may write to without asking. One joins when the
agent creates it under a grant or an approval, or when you run `yolo approve <id> --add` on a
request that writes to it. A PR leaves when it is merged (the broker checks before each write) or
when you revoke its entry. To let the agent open PRs without asking in one project, allow the
kind on request in your user config and request it in the project:

```sh
yolo allow github on_request create_pr   # on the host: your ceiling
yolo request github create_pr            # in the project (or the jail)
yolo config apply                        # on the host, in the project: approve the grant
```

`yolo allow github always create_pr` gives every project the grant instead, with no request.

`yolo approve` lists each project's working set as `w-<id>` entries; `yolo approve --revoke
w-<id>` ends one. A merge always asks. If the host config makes `read-write` standing, the
broker refuses writes and tells you to remove that entry; ordinary standing reads continue to work.

### Running a workflow

To let the agent start a workflow without asking, allow the kind and name the workflow file in
the project:

```sh
yolo allow github on_request dispatch_workflows          # on the host: your ceiling
yolo request github dispatch_workflows nightly.yml       # in the project (or the jail)
yolo config apply                                        # on the host, in the project: approve it
```

The project file then holds `dispatch_workflows = ["nightly.yml"]` under `[requests.github]`;
`"*"` there names every workflow in the repository. With it, `gh workflow run nightly.yml` runs
without asking, with any inputs (`-f`, `-F`, `--json`), but only on the repository's default
branch: leave out `--ref`, or pass the default branch's name. The broker sends that run as the
full branch ref (`refs/heads/main`), so a tag with the same name cannot stand in for the
branch. A run on any other branch or tag
asks, because a workflow runs as its file is on that ref, with the repository's secrets, and an
agent that can push a branch could change the file there first. The agent must name the workflow
by its file name; a numeric id or display name asks. Re-running, cancelling, enabling and
disabling workflows always ask.

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

- **`gh` exits 64** saying the workspace holds no read grant: add the read grant
  [above](#turning-it-on) to your user config, then run `yolo config apply` on your machine, in
  the project, and approve it.
- **`gh` exits 69** saying the jail has no github-broker endpoint: the broker did not start for
  this launch, or the jail was started before you selected the pack. Run `yolo check` on your
  machine, in the project, and start a fresh jail.
- **`gh` exits 69** saying the host has no `gh` or no login: install `gh` on your machine and run
  `gh auth status` there.
- **`gh` exits 69** saying the broker will not run the host `gh`: the first `gh` on your `PATH` is
  inside the project or inside yolo's jail home, where an agent could have put it. The broker
  also skips relative `PATH` entries such as `./bin`. Make sure the `gh` you installed comes first
  on your `PATH`, from a directory outside the project.
- **`yolo check`** shows the broker's own check: which `gh` it would run, its version, and whether
  it found a login.
