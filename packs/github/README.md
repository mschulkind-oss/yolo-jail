# `github` — GitHub without a token in the jail

The jail's `gh` runs your **host's own** `gh` login, through a broker on the host, against
this workspace's GitHub repositories: the ones its remotes name, and any its config lists, as you
approved them at launch. The jail holds no GitHub credential. Every call
the broker is sent is recorded on the host, and `yolo audit` lists them.

Design: [`boundary-broker.md`](../../docs/design/boundary-broker.md), and for the repositories a
project lists, [`workspace-widening.md`](../../docs/design/workspace-widening.md). See
[§11](../../docs/design/boundary-broker.md#11-the-build-in-the-maintainers-five-steps) for its read and
write paths, including exact host approval. A write files a pending request; `yolo approve <id>` on the host allows
that canonical command, repository and stdin digest once, for up to 15 minutes, and
`yolo approve <id> --for <duration>` allows that kind of write on its PR, issue or repository for
that long. Legacy set-wide grants do not authorize a write. The broker waits
briefly; without a host decision nothing runs and the agent can retry after approval. Pending
requests are visible in the host broker log and `yolo approve`, whether or not `on_request` is
configured.

## Three contributions

- **`loophole`** `github-broker`: the host daemon (`internal/ghbroker`), one per jail,
  behind yolo's own loopback-TLS front.
- **`intercept`** `gh` → `yolo gh --`: a shim at `~/.yolo/bin/block/gh`, first on the
  jail's `PATH`, so a bare `gh` reaches the broker while the image's own `/bin/gh` stays
  where it is (and `YOLO_BYPASS_SHIMS=1 gh …` runs it). That `gh` has no login in the jail.
  The shim is a forwarder, not a blocker, and its first lines say so.
- **`briefing`** [`briefing/gh.md`](./briefing/gh.md): a pointer of a few sentences that
  every agent loads, naming the `yolo-gh` skill.
- **`skills`** [`skills/yolo-gh`](./skills/yolo-gh/SKILL.md): how `gh` behaves in the jail,
  so an agent does not learn each rule by failing once, read when it is about to write
  ([BD-D2](../../docs/reference/agent-briefings.md#BD-D2)). Both reach every agent in a jail, and
  not at the host, where `gh` is your own: each contribution `describes` the `intercept`, and
  content about a kind the host does not apply is left out of a real home
  ([BB-D69](../../docs/design/boundary-broker.md#BB-D69),
  [BD-D3](../../docs/reference/agent-briefings.md#BD-D3)).

## Turning it on

On your machine, `yolo pack add github` selects the pack. In a legacy JSONC user config the same selection, and the rest of this setup, reads:

```jsonc
// ~/.config/yolo-jail/config.jsonc — user scope
{
  "packs": ["claude", "github"],                           // 1. the forwarder and the broker
  "brokered": {
    "github": { "grants": [{ "kind": "read", "repo": "*" }] } // 2. read access, every workspace
  }
}
```

Selecting the pack starts the broker in every workspace, and it grants nothing: with no grant,
every call is refused, naming the grant. The read grant is what lets each workspace read its own
approved repositories, and it is never in force unseen: each workspace's config-change prompt,
or `yolo config apply` on the host, shows it as a line of the repository-scope block, marked as
your user config's, beside the repositories it covers, and asks you to approve it. Without the
grant, the prompt asks nothing about the broker. The broker has no switch, so `yolo loopholes
enable github-broker` refuses; to stop the broker, take the pack out of `packs`.

The host needs `gh` and a login: `gh auth login` on the host, once. Once the read grant
applies, the next fresh launch of a workspace with a GitHub remote asks once, in its config-change prompt, to approve the
**repository scope**, the `owner/repo` of each `github.com` remote in the workspace's
`.git/config`. With no terminal, pass `--accept-config-changes`, or run
`yolo check --accept-config-changes` on the host first.

To add a repository the workspace has no remote for, run `yolo request repos lib github.com/org/lib` in the
project, which writes the workspace's own config and runs `yolo check`. In a legacy JSONC project,
list it by hand in `yolo-jail.jsonc`, or `yolo-jail.local.jsonc` for one the project should not
commit. The next
fresh launch shows it as a row of the repository-scope prompt, naming its file, and asks you to
approve it; `yolo config-ref` documents the key:

```jsonc
"brokered": { "github": { "repos": ["org/lib"] } }
```

An agent may add one: a repository outside the scope is refused with a message telling the agent
to run `yolo request repos <alias> github.com/<owner>/<repo>` and to ask you to run `yolo config apply` on the host and approve it; the running
jail takes it without a restart.

An entry may be a whole-segment wildcard instead of one repository: `*/*` is every repository the
login can reach, `OWNER/*` is one owner's, and `*/REPO` is that name under any owner. A wildcard
is approved at the same prompt, which names what it admits, and it widens only which repositories
run: account-wide commands stay refused.

## What runs

| | |
| :--- | :--- |
| Runs | the read-only set, against an approved repository: `pr view/list/diff/status/checks`, `issue view/list/status`, `run view/list/watch`, `workflow view/list`, `repo view/read-file/read-dir`, `release view/list`, `label list`, `secret list`, `search` with an in-scope `--repo`, `api` GET under `repos/OWNER/REPO`, and more |
| Pending write | Filed for exact host approval; see `yolo approve <id>` in the broker log or host CLI; `on_request` is optional |
| Exit 64 | anything that could print the credential or reach the host (`auth token`, `--web`, `api` to a URL, host-file arguments, …), and anything outside the repository scope, including account-wide commands such as an unqualified `search` or any GraphQL call, which no `[repos.<name>]` entry admits |
| Exit 69 | no broker in this jail, or no `gh` or login on the host |

stdout, stderr and the exit code of a command that runs cross verbatim, and `--jq` and
`--template` work as they do in `gh`. The repository is `-R OWNER/REPO`, or the workspace's
`origin` remote.

## What it is not

- It does not stop an agent carrying out what a read returned: a read is a read.
- It does not guard `git push`/`fetch`: those use the jail's own credential, or none.
- It is not a control against a process running as you on the host.
