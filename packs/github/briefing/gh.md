## `gh` in this jail

`gh` here is `~/.yolo/bin/block/gh`, the github pack's forwarder, not a blocker. Each call goes
to the github-broker on the host, which runs the host's own `gh` login against this workspace's
GitHub repositories. The jail holds no GitHub token; `gh auth status` names the login.

- **The broker grants nothing until the user's config does.** A call refused with `no read
  grant` means the user has not granted read access. Ask them to run this in a host terminal,
  in this workspace, directly (not through `yolo host-run`), and answer y to the approval it
  offers:

  ```
  yolo allow github always read
  ```

  (A user config still in JSONC needs `yolo config migrate` first; the command says so.) You
  cannot grant it from the jail.
- With the read grant, reads run, and their output and exit codes are `gh`'s own. `--jq` and `--template` work as in
  `gh`, and so does piping `--json` output into `jq`.
- Some writes run without asking. With a `create-pr` (or `create-issue`) grant, which the user
  adds with `yolo request github create_pr` in this workspace and approves with
  `yolo config apply` on the host, `gh pr create` (or `gh issue create`) runs, and the PR or
  issue it creates joins this workspace's **working set**. Every write to a PR or issue in the
  working set runs too (comment, edit, review, ready, close, reopen, and `gh api` writes under
  its `pulls/{n}` or `issues/{n}`), until the PR is merged. A merge always asks. Name a PR or
  issue by its number, not a branch: a write without a number always asks.
- With a `runs` grant naming a workflow file (or `*`), which the user adds with
  `yolo request github runs <file>` and approves the same way, three things run unasked:
  `gh workflow run <file>` on the repository's **default branch** only (omit `--ref` or pass the
  default branch's name; name the workflow by its file name, `nightly.yml`, not its id or display
  name); `gh run rerun <run-id>` (with `--failed`, `--debug`, or `--job <job-id>` in place of the
  run id) of a run of that workflow on the default branch of the repository itself; and
  `gh run cancel <run-id>` of any run in the repository. A run or re-run on any other branch, of a
  fork, or without a numeric id asks.
- With an `approve-run` grant naming a GitHub login (or `*`), which the user adds with
  `yolo request github approve_runs <login>` (such as `'github-actions[bot]'`) and approves the
  same way, `gh api -X POST repos/OWNER/REPO/actions/runs/<id>/approve` runs unasked for a run
  that login triggered whose head is the repository itself. A fork's run always asks.
- Every other in-scope write files a pending request, waits briefly for the host, and is
  refused with a menu of every answer, each command on its own line: `yolo approve <id>` once,
  `--for 15m` for that kind of write on it for a time, and, for a creation, the project request
  then `yolo config apply`. The
  human runs these directly on the host, not through `yolo host-run`. Relay the whole menu as
  **Running a command on the host** says, recommend one path, and start `yolo wait <id>` in the
  background; on exit 0, run the same command again, once. An Allow once binds command,
  repository and stdin digest, is one use, and expires after 15 minutes; a legacy set-wide grant
  is not spent. A configured `on_request` hook is optional; its output is never
  consent.
- Exit 64 is a refusal, and its message says why and what to do instead. Refused: anything that
  could print the credential or reach the host, a repository outside this workspace's scope, and
  every call across the account. Raw `gh api graphql` is always refused; `gh`'s own commands
  (`gh pr view`, `gh issue list`, …) use GraphQL inside and work.
- A repository this workspace has no remote for stays out of scope until the user approves it.
  Run `yolo request repos <alias> github.com/OWNER/REPO`, which writes this project's config (if it refuses
  because the config file is read-only here or cannot take the key, ask the user to run the same
  command on the host, in this workspace).
  Then run `yolo config check`, and ask the user to run `yolo config apply` on the host, in
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
