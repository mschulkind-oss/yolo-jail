# `github` — GitHub without a token in the jail

The jail's `gh` runs your **host's own** `gh` login, through a broker on the host, against
this workspace's own GitHub repositories. The jail holds no GitHub credential. Every call
the broker is sent is recorded on the host, and `yolo audit` lists them.

Design: [`boundary-broker.md`](../../docs/design/boundary-broker.md). This is step 1 of its
[§11](../../docs/design/boundary-broker.md#11-recommendation-and-the-first-build-slice): **read-only**. A write (`gh pr comment`, `gh issue edit`, `gh api -X POST …`) exits 77
at once: writes need an approval step that is not built yet, so nothing runs and nothing waits,
and the message says to run the command on the host.

## Three contributions

- **`loophole`** `github-broker`: the host daemon (`internal/ghbroker`), one per jail,
  behind yolo's own loopback-TLS front.
- **`intercept`** `gh` → `yolo gh --`: a shim at `~/.yolo/bin/block/gh`, first on the
  jail's `PATH`, so a bare `gh` reaches the broker while the image's own `/bin/gh` stays
  where it is (and `YOLO_BYPASS_SHIMS=1 gh …` runs it). That `gh` has no login in the jail.
  The shim is a forwarder, not a blocker, and its first lines say so.
- **`briefing`** [`briefing/gh.md`](./briefing/gh.md): how `gh` behaves in the jail, so an
  agent does not learn each rule by failing once. It reaches every agent in a jail, and not
  at the host, where `gh` is your own: the contribution `describes` the `intercept`, and a
  briefing about a kind the host does not apply is left out of a real home
  ([BB-D69](../../docs/design/boundary-broker.md#BB-D69)).

## Turning it on

```jsonc
// ~/.config/yolo-jail/config.jsonc — user scope
{
  "packs": ["claude", "github"]                            // 1. the forwarder
}
```

```bash
yolo loopholes enable github-broker    # 2. the broker, for this project only, on the host
```

The broker is switched one project at a time, and only by that command
(`--workspace <path>` names another project; `yolo loopholes disable github-broker` turns it
off again). It writes the project's per-workspace file in `~/.config/yolo-jail/workspaces/`,
never `config.jsonc`, and no jail can read it. A `github-broker` switch in the user config or in
a project's own config is refused.

The host needs `gh` and a login: `gh auth login` on the host, once. The next fresh launch
of a workspace with a GitHub remote asks once, in its config-change prompt, to approve the
**repository scope**, the `owner/repo` of each `github.com` remote in the workspace's
`.git/config`. With no terminal, pass `--accept-config-changes`, or run
`yolo check --accept-config-changes` on the host first.

To add a repository the workspace has no remote for, list it for that one workspace in the
user config, in what yolo calls a **widening entry**; the launch names what it added, and
`yolo config-ref` documents the key:

```jsonc
"brokered": { "github": { "workspaces": { "~/code/app": { "repos": ["org/lib"] } } } }
```

## What runs

| | |
| :--- | :--- |
| Runs | the read-only set, against an approved repository: `pr view/list/diff/status/checks`, `issue view/list/status`, `run view/list/watch`, `workflow view/list`, `repo view/read-file/read-dir`, `release view/list`, `label list`, `secret list`, `search` with an in-scope `--repo`, `api` GET under `repos/OWNER/REPO`, and more |
| Exit 77 | every write, at once: it needs an approval step that is not built yet; run it on the host, where `yolo audit --set read-write` shows the exact command |
| Exit 64 | anything that could print the credential or reach the host (`auth token`, `--web`, `api` to a URL, host-file arguments, …), and anything outside the repository scope, including account-wide commands such as an unqualified `search` or any GraphQL call, which no widening entry admits |
| Exit 69 | no broker in this jail, or no `gh` or login on the host |

stdout, stderr and the exit code of a command that runs cross verbatim, and `--jq` and
`--template` work as they do in `gh`. The repository is `-R OWNER/REPO`, or the workspace's
`origin` remote.

## What it is not

- It does not stop an agent carrying out what a read returned: a read is a read.
- It does not guard `git push`/`fetch`: those use the jail's own credential, or none.
- It is not a control against a process running as you on the host.
