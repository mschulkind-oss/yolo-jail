---
summary: "How yolo checks and updates its host installation, which install channels are complete, and why a from-source install is a snapshot."
---

# Self-update

yolo manages the agent environment delivered to a workspace. Keeping the host
launcher and the jail-side bundle together is therefore the first rule of
self-update; replacing only the host executable is not a complete update.

## Install channels

| Channel | Check | Apply |
|---|---|---|
| From source (`just install`) | None: a snapshot has no release to compare against | Changes nothing; prints the two ways to update it, below |
| Homebrew | Background or explicit GitHub release check | `HOMEBREW_NO_INSTALL_CLEANUP=1 brew upgrade yolo-jail`, followed by version verification |
| `go install` | Background or explicit GitHub release check | Refused: binary-only install has no jail bundle; install a complete bundle instead |
| pipx / uv tool | Background or explicit GitHub release check | Refused for the same missing-bundle reason |
| Release archive | Background or explicit GitHub release check | Prints the release download link |

Homebrew and from-source installs are self-contained: each installs both the
host launcher and the flake bundle that supplies the jail binaries. The Go and
Python package channels contain only the host binary, so they cannot launch a
jail by themselves. Install a complete bundle through Homebrew, a release
archive, or `just install`; `YOLO_REPO_ROOT` no longer supplies the missing launch
source. Remove it from your shell configuration. Checkout development instead uses
`just dev` or a PATH symlink to `scripts/dev-wrapper.sh` (`just deploy-dev [name]`
installs one), which builds and invokes a matched launcher from the checkout on
every run.

Neither complete install deletes what running jails use. A launch bind-mounts
its jail binaries and flake bundle from the installation it ran from: for
Homebrew that is the versioned Cellar keg, which `brew upgrade` removes by
default, so the update sets `HOMEBREW_NO_INSTALL_CLEANUP=1` and the old keg
stays until a `brew cleanup` removes it. `just install` and `just deploy` stage
a new bundle generation beside the one running jails mount and reap a generation
only once no container uses it. Running jails keep the old binaries until they
are restarted.

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

From-source installs and development bundles get no automatic check, notice or
launch offer.

## From-source installs

`just install` installs a snapshot of the checkout, the same as a release built
at that commit. The binary records its version, commit, snapshot identity and
the build kind `installed`, and nothing about where the checkout is, so nothing
in the checkout affects the host afterwards
([OQ-IP1](../design/installed-launcher-provenance.md#OQ-IP1)). yolo identifies
the channel by that build kind alone.

A snapshot of an arbitrary commit has no release to compare against, so
`yolo update` and `yolo update --check` change nothing there. They say so and
print the two next steps: run `just install` in your checkout, or name it with
`yolo update --from <checkout>`. `--check` exits 0; a plain `yolo update` exits 1
because it installed nothing. `yolo check` shows the same two steps in its
Updates section.

The launcher `just install` puts on your `PATH` and the copy of `yolo` in the
bundle for the host's own platform are one build: the launcher is copied from
the bundle, never compiled twice
([OQ-IP2](../design/installed-launcher-provenance.md#OQ-IP2)). On macOS the
bundle carries no darwin `yolo`, and the macOS-account guest stages the
running launcher itself. A Linux jail on a Mac runs the bundle's Linux `yolo`,
which is by nature a different program from the darwin launcher.

`yolo update --from <checkout>` runs `just deploy` in the named checkout exactly
as it stands, installing beside the running binary: no fetch, no pull and no
branch check, because naming the checkout is naming what to deploy. It is
accepted only on a from-source install. It refuses a checkout with uncommitted
changes; `--autostash` stashes tracked and untracked changes, deploys a clean
build, and restores them even after a failed deploy. `--check --from
<checkout>` prints the deploy and changes nothing.

### What `--from` runs

It runs `git status` and, with `--autostash`, `git stash` in the checkout, with
that checkout's own Git configuration and hooks, then the checkout's `Justfile`
through `just deploy`. All of it runs on the host, as you. `just deploy`
restarts the Claude OAuth broker, so any jail using that broker loses it
briefly.

Developing yolo in a jail and deploying from that checkout runs what the jail
wrote, by design: building a checkout runs its contents, and yolo adds no
guard there ([the product cell](host-execution-from-the-workspace.md#the-two-axes)).

## Version identity

Every build's version string names the commit it was built from, releases
included. A build on its tag reads `<tag>+g<hash>`; a source or flake build off
the tag adds its distance from the tag before the hash, and a dirty tree adds
`dirty` (`version.Normalize` in `internal/version`). Every build channel stamps
the same form: `.goreleaser.yaml` for releases, the Homebrew formula
(`tools/release-wiring/update-homebrew.sh`, which stamps from the release commit
because the tarball carries no git metadata), the wheels (`tools/build-wheels`),
`scripts/build-go.sh` (which `just install` uses) and the flake (`buildStamp` in
`flake.nix`). Build metadata after `+` never orders a version, so an update
check compares `0.14.0+g<hash>` as `0.14.0` (`selfupdate.Newer`).

`yolo --version --full` adds the build stamp, the commit, the sha256 of the
running executable, the snapshot identity and the build kind
(`version.FullLines`). The sha256 stays because two builds from one dirty tree
share a version string.

Docs record no per-rule version history: no reference or `yolo config-ref`
entry says which version added a rule.

## State and controls

The cached answer and background-check lock live under
`~/.local/share/yolo-jail/update-check/`, outside directories mounted writable
into a jail. The cache is keyed by installation kind and version. Updating
invalidates the answer but preserves the one-time disclosure.

`"update_check": false` in the user config disables automatic checks plus cached
notices and launch offers. `YOLO_NO_UPDATE_CHECK=1` is the per-shell equivalent.
Neither setting disables an explicit `yolo update` or `yolo update --check`.

## Why it's this way

| ID | Ruling | Why it holds |
| :--- | :--- | :--- |
| OQ-DA2 | Every version string carries the commit hash, releases and Homebrew included; the version that added a rule is not recorded (owner, 2026-10-16) | The owner's words: *"hashes are _actually_ immutable"*, and recording when each rule was added is *"impossible ongoing work. I don't want to set that precedent."* Dropping the hash from release builds would make two binaries from different commits indistinguishable from outside. |
