## `gh` in this jail

`gh` here is `~/.yolo/bin/block/gh`, the github pack's forwarder, not a blocker. Each call goes
to the github-broker on the host, which runs the host's own `gh` login against this workspace's
GitHub repositories. The jail holds no GitHub token; `gh auth status` names the login.

- Reads run, and their output and exit codes are `gh`'s own. `--jq` and `--template` work as in
  `gh`, and so does piping `--json` output into `jq`.
- A write (`gh pr comment`, `gh issue edit`, `gh api -X POST …`) runs nothing and exits 77 at
  once: writes from a jail need an approval step that is not built yet. Ask the user to run it
  on the host.
- Exit 64 is a refusal, and its message says why and what to do instead. Refused: anything that
  could print the credential or reach the host, a repository outside this workspace's scope, and
  every call across the account. Raw `gh api graphql` is always refused; `gh`'s own commands
  (`gh pr view`, `gh issue list`, …) use GraphQL inside and work.
- In a `gh api` path, percent-encode a slash in a branch name as usual:
  `repos/OWNER/REPO/branches/MS%2Fmain`.
- Exit 69: no broker in this jail, or no `gh` login on the host.
