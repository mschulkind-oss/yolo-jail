# Logins

A jail never sees your host's logins: not your `~/.ssh` keys, not your git credentials, not your
cloud tokens, and not the logins your agents keep on your own machine. You give each tool its login
deliberately, in one of three ways:

- **Log in inside the jail**, the way you would on a new computer. This is the usual path.
- **Hand the agent an API key** through a file of environment variables. See
  [Providers and Models](providers-and-models.md).
- **Let a host service keep one shared login fresh** for every jail, where yolo offers one.

Every login you make inside a jail is kept on your host and survives restarts, so you log in once,
not once per launch. The one exception is a `copilot` login you do not let it store in plain text
([below](#log-in-inside-the-jail)).

## Log in inside the jail

```bash
claude                 # Claude Code asks you to /login the first time
codex                  # prints a browser link the first time; see below
agy                    # Google Antigravity: sign in when it asks
gh auth login          # the GitHub CLI
```

Copilot, opencode and pi each have their own login command or prompt, the same as outside a jail.

Copilot normally keeps its login in your system keychain, and a jail has none, except possibly on
`macos-user`, where this has not been tried. So when you log in, `copilot` asks whether it may store
the token in a plain-text file instead. Say yes to keep the login; the token is then kept
unencrypted in the project's `.yolo/` folder. If you say no (the default for `copilot login`),
nothing is saved, and the next `copilot` asks you to log in again.

## How far one login reaches

| Agent | One login covers |
|---|---|
| `claude`, `agy` | Every project on this machine |
| `codex`, `pi` | Every project on this machine, through yolo's shared ChatGPT login (below) |
| `opencode` | On the `codex` profile, every project on this machine, through the same shared ChatGPT login; any other login, one project |
| `copilot`, `omp`, and `gh` | One project: log in once in each |

A per-project login lives in that project's `.yolo/` folder. A machine-wide one lives under
`~/.local/share/yolo-jail/`. [Storage](storage.md#what-persists-across-restarts) lists the folders.

## A shared Claude login that stays fresh

Claude's login uses a refresh token that works only once. When two jails share one login and both
refresh it at the same moment, one of them loses and is logged out. The `claude` pack solves this
with the **Claude OAuth broker**, a small service on your host that takes the refreshes one at a
time and refreshes the shared login ahead of expiry, so an idle jail or a laptop waking from sleep
does not find itself logged out.

It is part of the `claude` pack and needs no setup. Check on it with:

```bash
yolo broker status     # is it running, and what state is the shared login in?
yolo broker restart    # restart it, for example after upgrading yolo
```

The broker never touches your host's own `~/.claude/.credentials.json`, so logging in inside a jail
does not log in Claude Code on your host, and the reverse.

> [!IMPORTANT]
> **The broker does not work on Apple Container or `macos-user` yet.** Claude still logs in and
> works there, but two jails running at the same time can log each other out; log in again when
> that happens. If you run several Claude jails at once on a Mac, Podman avoids it.

<a id="a-shared-chatgpt-login-for-codex-and-pi"></a>

## A shared ChatGPT login for Codex, pi and opencode

The `codex`, `pi` and `opencode` packs bring `openai-auth`, a service on your host that holds one
ChatGPT subscription login for the whole machine. The first time you start `codex` or `pi`
without a login, or `opencode` on the `codex` profile, it prints a browser link, and opens it on
the host when it can. After that one login, Codex, pi and opencode in every project use it, and
the service handles refreshing it; opencode never refreshes it itself.

opencode uses it only on the `codex` profile (`yolo -p codex -- opencode`, or a `profile` that
selects `codex` for opencode). Any other OpenAI login you make in opencode, a ChatGPT login of
its own or an API key, stays opencode's and stays in that project. On your own machine,
`yolo host -p codex -- opencode` offers the shared login as **ChatGPT Plus/Pro (yolo shared
login)** in opencode's `/connect` for OpenAI; pick it once.

```bash
yolo openai-auth status                     # whose login, and when it expires
yolo openai-auth import --from /abs/path/auth.json   # reuse a login `codex login` already made
yolo openai-auth logout                     # remove it, for every project and jail at once
```

These run on your host only, not inside a jail. On yolo 0.10.0 they are spelled
`yolo internal openai-auth status`, and so on; `yolo openai-auth` arrives in the next release.

> [!IMPORTANT]
> **Apple Container cannot reach this service yet**, so `codex`, `pi` and `opencode` print
> `OpenAI login is required.` there and the browser login fails too. Use an API key, or Podman. On
> `macos-user` it works partly, and a long session can lose its login; relaunch to get it back.

## Pushing to git from the jail

Your host's SSH keys and git credentials are not in the jail, so the agent cannot push with them.
yolo does copy your git name and email in, so commits work. To let the jail push:

- create a key inside the jail with `ssh-keygen` and add it to the repository as a **deploy key**,
  which grants access to that one repository; or
- put a `GH_TOKEN` in a dotenv file listed under `env_sources`, ideally a token limited to the
  repositories you need.

If your host has no git name or email set, the agent's first commit fails with
`Please tell me who you are`. Check with `git config --get user.name && git config --get user.email`.

## When a login keeps failing

- Run `yolo check`: it includes a self-check of the Claude broker.
- `yolo host-daemon status` lists every host service yolo runs and whether each is healthy. On yolo
  0.10.0, which lacks it, `yolo broker status` checks the Claude login service.
- Claude Code in a jail keeps its login in the folder every workspace shares, so a fresh `/login`
  inside a jail sticks, and a refresh no longer asks you to log in again at the next launch. A
  jail started before you upgraded yolo still works the old way until you restart it: there, and
  for `agy`, a fresh login can be replaced by a revoked or expired shared one at your next launch.
  yolo records each time this happens in `~/.yolo-shared-creds.log` inside the jail; read it if a
  login keeps not sticking.

[Settings per setup](../reference/settings-per-setup.md#5-do-i-have-to-log-in-again-in-every-workspace-and-in-a-second-jail-at-the-same-time)
has the full per-setup detail.
