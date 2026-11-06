## `git` remotes in this jail

**First check whether the broker runs for this jail:** `echo "${GIT_CONFIG_COUNT:-unset}"`.

- **`unset`: the git broker did not start for this launch.** It runs whenever the git pack is
  selected, so this is a backend that runs no host services (Apple Container) or a broker that
  failed to start. Plain git behaves exactly as it would without the git pack: no remote is
  rewritten, and a remote that needs a credential fails for want of one, since the jail holds
  none. Ask the user to run `yolo check` on the host, in this workspace.
- **A number: the broker runs.** This jail's `git` is the real one, but its `ssh://`,
  `git+ssh://`, `ssh+git://`, `https://` and `git@` remotes go through the git pack's broker on
  the host: `GIT_CONFIG_COUNT` and the `GIT_CONFIG_KEY_<n>` variables rewrite them to `yolo::<url>`,
  and git runs
  `git-remote-yolo` (`~/.yolo/bin/block/git-remote-yolo`, a forwarder to `yolo git-remote`). The
  host connects with its own credentials, its SSH keys or agent for an SSH remote and its git
  credential helper for an HTTPS one; the jail holds no git credential.

With the broker running:

- **It grants nothing until the user's config does.** SSH fetches and pushes refused with `no read
  grant` mean the user has not granted read access. Ask them to run this in a host terminal, in
  this workspace, directly (not through `yolo host-run`), and answer y to the approval it offers:

  ```
  yolo allow git fetch
  ```

  (A user config still in JSONC needs `yolo config migrate` first; the command says so.) The
  running broker takes it at its next call. You cannot grant it from the jail, and a workspace config
  edit does not either. A public HTTPS fetch is the exception: it is tried anonymously, and never
  asks the host credential helper.
- With the read grant, `git fetch`, `git pull` and `git ls-remote` work against this
  workspace's approved remotes, over SSH or HTTPS. An HTTPS credential is used only after that
  exact remote and read grant are approved.
- **A push goes through unasked only when the user granted it ahead of time.** With a
  `create-branch` grant on the repository, you may push a branch that does not exist on the
  remote yet; once the remote accepts it, it joins this workspace's working set, and later pushes
  to it (updates, force-pushes, deletes) go through too, in this jail and in later ones. A
  `branch` grant in the workspace config declares a branch (such as `main`), or every branch a
  pattern matches (such as `patch/*`), in the working set too: you may create, push, force-push
  and delete it like one you created. A `tag` grant covers the tags it names (such as `v*`) the
  same way. Either may name one repository or `"*"`, every approved remote. Unless the working set
  holds it, push your work to a new branch of your own, not to `main` or a branch someone else
  created.
- **Any other push of branches or tags asks the user.** It files one request for the whole push
  and waits up to 30 seconds for an answer on the host. Allowed in that time, it goes through.
  Otherwise it is refused whole, nothing is pushed, each such ref's line in git's report names
  the request (`! [remote rejected] <ref> (needs approval: r-<id>)`), and the refusal lists every
  path that answers it, each command on its own line: `yolo approve r-<id>` once, `--for 15m`,
  the project request here then `yolo config apply` on the host, and the push from the host. Relay that whole menu as **Running a command on the host** says,
  recommend one path, and start `yolo wait r-<id>` in the background; on exit 0, run the same
  push again, once. Allow once covers exactly that push, once, within 15 minutes: a different
  push (another commit, another ref) asks again. `(needs approval: r-<id>, denied on the host)`
  means the user said no: do not push it again without asking them. A new branch the user
  allowed once joins the working set like one a `create-branch` grant created. To push a branch
  without asking each time, request it here (or `yolo request git new_branches` for any new
  branch), which writes the workspace config:

  ```
  yolo request git push <branch>
  ```

  and the human approves it on the host, directly:

  ```
  yolo config apply
  ```

  A ref outside `refs/heads/` and `refs/tags/` is never pushed through the broker, and a push
  that holds one asks nothing: push that from the host.
- A remote outside the approved scope is refused for SSH and push. A public HTTPS fetch may be
  tried anonymously outside that scope; if it asks for credentials, it is refused naming the
  scope. A new remote joins only when the user approves it in the config-change prompt on the
  host: add it with `git remote add`, then ask the user to run `yolo config apply` on the host, in
  this workspace, and answer y.
- An HTTPS fetch the host has no credential for is refused naming the `git credential fill` the
  user can run on the host, but only after its URL and read grant are approved; public fetches
  never ask the helper. The broker never prompts.
- `http://`, `git://`, `file://`, local paths, and scp-style URLs that do not start with `git@`
  (including `host:path` and `alice@host:path`) are not rewritten, so they follow jail Git's
  ordinary behavior without this broker. Other broker-helper inputs such as helper schemes are
  refused; they do not open host file or command access.
- Never set or export `GIT_CONFIG_COUNT` or `GIT_CONFIG_KEY_<n>` yourself: yours replaces
  the pack's, so the rewrite is gone and every fetch then fails for want of a credential.
- If git says the broker's endpoint is missing, the broker is not running for this jail: ask the
  user to run `yolo check` on the host.
