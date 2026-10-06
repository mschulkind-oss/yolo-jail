---
summary: "How yolo checks and updates its host installation, which install channels are complete, and why source checks are explicit."
---

# Self-update

yolo manages the agent environment delivered to a workspace. Keeping the host
launcher and the jail-side bundle together is therefore the first rule of
self-update; replacing only the host executable is not a complete update.

## Install channels

| Channel | Check | Apply |
|---|---|---|
| From source (`just deploy`) | Explicit Git check | `git pull --ff-only`, then `just deploy` |
| Homebrew | Background or explicit GitHub release check | `HOMEBREW_NO_INSTALL_CLEANUP=1 brew upgrade yolo-jail`, followed by version verification |
| `go install` | Background or explicit GitHub release check | Refused: update the binary and `YOLO_REPO_ROOT` checkout together |
| pipx / uv tool | Background or explicit GitHub release check | Refused for the same binary/bundle split |
| Release archive | Background or explicit GitHub release check | Prints the release download link |

Homebrew and from-source installs are self-contained: their update path replaces
both the host launcher and the flake bundle that supplies the jail binaries.
The Go and Python package channels contain only the host binary. Their launches
therefore depend on a separate checkout selected by `YOLO_REPO_ROOT`; replacing
only the binary can pair a new launcher with an old entrypoint and is refused.
For the same reason, a Homebrew or source update is not offered when an explicit
`YOLO_REPO_ROOT` selects a different checkout. Update that checkout and binary
together, or unset the override to use the installation's own bundle.

Neither complete update deletes what running jails use. A launch bind-mounts
its jail binaries and flake bundle from the installation it ran from: for
Homebrew that is the versioned Cellar keg, which `brew upgrade` removes by
default, so the update sets `HOMEBREW_NO_INSTALL_CLEANUP=1` and the old keg
stays until a `brew cleanup` removes it. `just deploy` stages a new bundle
generation beside the one running jails mount and reaps a generation only once
no container uses it. Running jails keep the old binaries until they are
restarted.

## Automatic checks

For release-based channels, ordinary host commands read a cached answer and may
start one detached GitHub Releases API request when the answer is at least 24
hours old. A failed request is also cached for 24 hours; a check that dies
before recording an answer may retry after its ten-minute lock expires.

The first automatic request waits for stderr to be a terminal, prints the
network disclosure, records that the disclosure was shown, and only then starts
the check. Later checks may run from non-interactive commands because the
machine-wide disclosure has already happened. No automatic check runs in a jail
or when `CI` is set.

Source installs are deliberately different: no ordinary command runs the
updater's remote Git check against their checkout. A yolo checkout may itself
be the workspace exposed to a jailed agent, and repository-local Git
configuration is part of the
[host-execution control plane](host-execution-from-the-workspace.md). Run
`yolo update --check` or `yolo update` explicitly to check source.

## Source checks and updates

A source check reads the checkout's current branch and upstream, asks the
upstream for its tip, and counts commits affecting the Go/image inputs or the
install and bundle scripts. If the tip is not already in the local object
database, the check fetches commit objects without updating a ref, FETCH_HEAD,
or tags. `git status` remains unchanged, but the object database is written.

The update refuses a detached checkout, a branch different from the one stamped
into the binary, or uncommitted changes. `--autostash` stashes tracked and
untracked changes, deploys a clean build, and restores them even after a failed
update.

`--from <checkout>` is the recovery for a moved or re-cloned source tree and the
explicit override for deploying another branch. A non-check invocation always
redeploys so the replacement checkout is stamped into the new binary, even when
there is no newer upstream commit. When that checkout has no usable upstream
(or it cannot be reached), yolo deploys the named checkout as-is instead of
making the recovery depend on a `git pull` that cannot succeed.

### What a source check or update runs

Both run `git` in the checkout with that checkout's own Git configuration and
hooks. The check reaches the upstream with `git ls-remote` and `git fetch`; the
update runs `git pull`, and `git stash` when you pass `--autostash`. The update
then runs the checkout's `Justfile` through `just deploy`, with
`YOLO_INSTALL_KEEP_TREE=1` so the install deploys the pulled tree exactly: it
never re-pins an official pack program into the checkout, and a program it
cannot build to its pin is reported, its loophole left off on this machine, rather
than failing the update
([BP-D31](../design/broker-as-a-pack.md#BP-D31)). All of it
runs on the host, as you. yolo ends Git's options before the remote, refuses an
upstream whose name starts with `-`, and never lets Git prompt, but a
repository's configuration can still name programs Git runs, such as a hook or
`remote.<name>.uploadpack`.

When a jail uses the checkout as its workspace, the agent in it can write
those files. List `.git/config`, `.git/hooks`, `.git/info` and `Justfile` in
that checkout's `workspace_readonly` to make them read-only in the jail; see
[locking the blind cell](host-execution-from-the-workspace.md#locking-the-blind-cell-workspace_readonly)
for why the Git control plane has to be locked as a unit.

## State and controls

The cached answer and background-check lock live under
`~/.local/share/yolo-jail/update-check/`, outside directories mounted writable
into a jail. The cache is keyed by installation kind and version; source
entries also include the checkout path and stamped branch. Updating invalidates
the answer but preserves the one-time disclosure.

`"update_check": false` in the user config disables automatic checks plus cached
notices and launch offers. `YOLO_NO_UPDATE_CHECK=1` is the per-shell equivalent.
Neither setting disables an explicit `yolo update` or `yolo update --check`.
