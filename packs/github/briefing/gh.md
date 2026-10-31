## `gh` in this jail

`gh` here is `~/.yolo/bin/block/gh`, the github pack's forwarder, not a blocker. Each call goes
to the github-broker on the host, which runs the host's own `gh` login against this workspace's
GitHub repositories. The jail holds no GitHub token; `gh auth status` names the login.

- **The broker grants nothing until the user's config does.** A call refused with `no read
  grant` means the user has not granted read access: ask them to add
  `{"kind": "read", "repo": "*"}` to `brokered.github.grants` in their user config on the host,
  then run `yolo config apply` on the host, in this workspace, and approve the grant. You cannot
  grant it from the jail.
- With the read grant, reads run, and their output and exit codes are `gh`'s own. `--jq` and `--template` work as in
  `gh`, and so does piping `--json` output into `jq`.
- Some writes run without asking. With a `create-pr` (or `create-issue`) grant, which the user
  adds with `yolo request github create_pr` in this workspace and approves with
  `yolo config apply` on the host, `gh pr create` (or `gh issue create`) runs, and the PR or
  issue it creates joins this workspace's **working set**. Every write to a PR or issue in the
  working set runs too (comment, edit, review, ready, close, reopen, and `gh api` writes under
  its `pulls/{n}` or `issues/{n}`), until the PR is merged. A merge always asks. Name a PR or
  issue by its number, not a branch: a write without a number always asks.
- With a `dispatch-workflow` grant naming a workflow file (or `*`), which the user adds with
  `yolo request github dispatch_workflows <file>` and approves the same way,
  `gh workflow run <file>` runs unasked on the repository's **default branch** only: omit `--ref`
  or pass the default branch's name. Name the workflow by its file name (`nightly.yml`), not its
  id or display name. A run on any other ref asks.
- Every other in-scope write files a pending request and waits briefly for the host. Tell the
  user to run `yolo approve <id>` on the host to allow that exact command once,
  `yolo approve <id> --add` to add the PR or issue it writes to the working set,
  `yolo approve <id> --for 15m` to allow that kind of write on it for a time, or
  `yolo approve <id> --deny`. An Allow once binds command, repository and stdin digest, is one
  use, and expires after 15 minutes; a legacy set-wide grant is not spent. If the request times
  out, it remains visible in the host broker log and `yolo approve`, and the same command can be
  retried after approval. A configured `on_request` hook is optional; its output is never
  consent.
- Exit 64 is a refusal, and its message says why and what to do instead. Refused: anything that
  could print the credential or reach the host, a repository outside this workspace's scope, and
  every call across the account. Raw `gh api graphql` is always refused; `gh`'s own commands
  (`gh pr view`, `gh issue list`, …) use GraphQL inside and work.
- A repository this workspace has no remote for stays out of scope until the user approves it.
  Run `yolo request github repos OWNER/REPO`, which writes this project's config (if it refuses
  because the config file is read-only here or cannot take the key, ask the user to run the same
  command on the host, in this workspace).
  Then run `yolo check --no-build`, and ask the user to run `yolo config apply` on the host, in
  this workspace, and answer y to the repository-scope prompt. In a jail started by this yolo,
  the running broker takes the repository at its next call; an older one needs a restart.
  Nothing changes before that y.
- A wildcard entry reaches more than one repository: `*/*` is every repository the host login can
  reach, `OWNER/*` is one owner's, `*/REPO` is that name under any owner. A whole segment is the
  only wildcard form. Use one only when the user asks for that reach; it widens only the repository
  scope. Writes still require exact host approval unless a grant or the working set covers them, and commands that act across the account remain
  refused (a search with no `--repo`, `gh repo list`, `gh status`).
- In a `gh api` path, percent-encode a slash in a branch name as usual:
  `repos/OWNER/REPO/branches/MS%2Fmain`.
- Exit 69: no broker in this jail, or no `gh` login on the host.
