---
status: current
verified: 2026-09-09
verified_commit: 38873c0d
covers:
  - internal/cli/run/assemble_parts.go
  - internal/entrypoint/identity.go
  - internal/storage/ensure.go
tags: [git, identity, credentials, mounts, allowlist]
summary: "The jail's git identity is a two-key allowlist — user.name and user.email — composed fresh on the host every run and delivered read-only at git's default global path. Nothing else from the host's git config crosses, which is what keeps credentials, pagers and signing config out."
---

# git identity in the jail

**Status:** CURRENT as of 2026-09-09, verified against `38873c0d`.

An agent committing in a jail needs to be the right author, and needs nothing else from the
host's git configuration. So identity is a **forward of an enumerated two-key allowlist** —
`user.name` and `user.email` — and never a mount or an include of the host's `~/.gitconfig`.

On the **container backends** the host composes a small `gitconfig` fresh every run from those
two keys and delivers it **read-only** at git's default global path. On **`macos-user`**, which
has no mount namespace, the same two keys are forwarded as environment variables and replayed
imperatively.

| Component | Lives in |
| :--- | :--- |
| Reading the host keys, composing the file, mounting it | `internal/cli/run` (`gitIdentityMountArgs`, `composeGitconfig`, `hostGitConfigGet`, `gitConfigValue`, `gitIncludeHeader`) |
| The Apple Container materialize path | `internal/cli/run` (`acMaterialize`) |
| The imperative replay, `macos-user` only | `internal/entrypoint` (`configureGit`, reading `YOLO_GIT_*`) |
| The home-root alias that makes `~/.gitconfig` resolve | `internal/storage` (`EnsureSymlink`) |

**Reads with:** [`composed-file-permissions.md`](composed-file-permissions.md) (what `:ro`
actually buys, and why this surface is *Derived*),
[`config-migration-to-prism.md`](config-migration-to-prism.md) (the stateful render this surface
deliberately does not use), [`agent-credentials.md`](agent-credentials.md)
(the wider credential boundary).

---

## The allowlist is the design

Only the named keys cross. Everything else in the host's git config simply never appears, which
satisfies both requirements for free:

- **No credentials cross.** `credential.helper`, `user.signingkey`, `url.*.insteadOf` rewrites,
  OAuth and PAT helpers — none are named, so none are forwarded. The jail gets identity without
  ever getting the means to authenticate as the user. Credentials that genuinely need to work in
  a jail arrive through their own explicit channels (a workspace deploy key, a token in an env
  file), never by inheriting host git config.
- **No UI or agent-confusing settings cross.** `core.pager`, `pager.*`, `core.editor`,
  `color.ui`, pretty-print defaults — none are named. The jail runs its own env hygiene for
  agents (`PAGER=cat`, `GIT_PAGER=cat`, `EDITOR=cat`), and inheriting host preferences would
  fight it. An allowlist means yolo never has to *strip* these; they were never in scope.

> [!CAUTION]
> **Do not inherit `~/.gitconfig` wholesale** — not by bind-mounting it, not by pointing
> `GIT_CONFIG_GLOBAL` at it, not by `[include]`-ing it. It drags in the pager and editor (the
> exact agent confusion the jail's env hygiene exists to prevent), it drags in credential helpers
> (the exact leak), and **it can break committing entirely**: with `commit.gpgsign = true` and no
> key material in the jail, `git commit` fails hard at signing. A signing host would make every
> in-jail commit fail. That alone rules the approach out.

### What is on the list, and why nothing else is

| Setting | Crosses | Why |
| :--- | :--- | :--- |
| `user.name`, `user.email` | yes | author identity — the whole point |
| `core.excludesFile` | composed, not forwarded | points at the **in-jail** global gitignore path, which the host also delivers |
| `user.signingkey`, `commit.gpgsign`, `tag.gpgsign` | no | credential-adjacent, useless without key material, and signing without a key breaks every commit |
| `credential.helper`, `url.*.insteadOf` | no | credential leak; `insteadOf` can silently reroute a fetch through a host credential path |
| pagers, editors, colors | no | fights the jail's deliberate `PAGER`/`EDITOR` hygiene |
| `commit.template`, `core.hooksPath` | no | point at host paths that do not exist in the jail |

Read the table downward: everything genuinely *useful* to forward is either a credential, UI, or
a host-path reference that will not resolve. The only clean additions would be cosmetic
preferences (`init.defaultBranch`, workflow toggles like `pull.rebase`), whose value is
marginal — `init.defaultBranch` was considered and declined. **The allowlist being essentially
two keys is a feature, not a gap.**

## How it is delivered

The host reads `user.name` and `user.email` with `git config --get`, so a repo-local value for
the host's working directory wins — matching what a human's own `git commit` would use there.
It renders a minimal INI carrying only those keys (each omitted when empty), plus a `[core]
excludesFile` when a global gitignore resolves, and delivers it so git reads it at its default
global path:

- **podman** — write the composed file into the workspace's host-side state dir and bind-mount it
  read-only over the in-jail path. Kernel-enforced, even against the jail's root.
- **Apple Container** — no nested single-file `:ro` bind exists, so the composed content is
  materialized into the state tree that backs the whole home bind.
- **`macos-user`** — no mount namespace at all, so the two keys ride as `YOLO_GIT_*` environment
  variables and `configureGit` replays them with `git config --global`. This mirrors how the
  global gitignore already diverges on that backend.

With **no identity and no gitignore**, nothing is emitted at all: a bare, identity-less jail,
whose launch argv is byte-identical to one where the feature does not apply.

### Fresh composition is what fixes staleness

The imperative setter this replaced wrote each key **only when its variable was present and
non-empty, and never removed it.** So when a host cleared `user.email`, the jail wrote nothing
new and the previously-written value stayed in the persistent `~/.gitconfig` forever — no
snapshot to roll back from, no managed sidecar, the value simply accreted.

Regenerating the whole file every run and mounting it read-only removes the accretion by
construction: **there is no persistent file to accrete into.** A cleared host key produces a file
without that line on the very next boot. `TestGitIdentityMountStaleClearedEmail` pins the
regression.

That is also why this surface is *not* a prism surface. Composition on the host plus a read-only
delivery gets the same freshness with no codec, no sidecars and no capture — see
[`config-migration-to-prism.md`](config-migration-to-prism.md) for the mechanism it declined.

> [!WARNING]
> **Do not build a keyed-INI prism surface for git identity.** There is no git-kv codec and no
> `identity` surface, and adding one rebuilds a thing that was deliberately rejected: it would
> reintroduce a persistent, jail-writable file as the source of truth for the one surface whose
> whole bug was persistence.

## The writable sibling, and the alias that still lies

Because the composed file is read-only, in-jail `git config --global` cannot write it. That is
accepted: the file is regenerated every run, so a persisted edit would be a lie.

What is *not* acceptable is being undiagnosable, and it was. A second mechanism points at the
same inode from the other side: a home-root symlink materializes `~/.gitconfig` →
`~/.config/git/config`, one of the escape-hatch aliases meant to resolve into a writable overlay,
whose target for this one file is the read-only bind. So `~/.gitconfig` looks like an ordinary
writable dotfile and `git config --global --add` fails with `Device or resource busy`, naming
neither yolo nor the mount.

Two things close most of that gap:

- The composed file **opens with a header** stating that it is generated, regenerated read-only
  every run, and that `git config --global` writes to the included file instead.
- It **`[include]`s a writable sibling, listed FIRST.** Git applies includes in order and the
  last definition of a key wins, so putting the include first keeps yolo's identity
  authoritative for the keys it sets while letting anything else — aliases, `pull.rebase`, a
  per-user override — persist in a file the jail can write.
  `TestComposeGitconfigIncludesWritableSiblingFirst` pins the order.

> [!WARNING]
> **`git config --global` still targets `~/.gitconfig` and still fails.** The include needs no
> environment variable, deliberately: a `GIT_CONFIG_GLOBAL` export would only be set for shells
> that source `.bashrc`, so a git invoked from an agent subprocess or a script with a sanitized
> environment would miss it and fail exactly as before. What changed is that the file it fails on
> now tells the reader where to write. Making `--global` itself succeed would mean making
> `~/.gitconfig` writable, which reopens the composition hole the read-only mount exists to
> close.

## What this does not license

- **Not a general host-config forward.** Adding a key to the allowlist is a decision about
  credentials and agent behavior, not a convenience.
- **Not a writable global git config.** The keys yolo owns are yolo's; everything else belongs in
  the included sibling or in the repo.
- **Not a `jj` (Jujutsu) equivalent.** yolo carried identity plumbing for `jj` that was a
  permanent no-op — `jj` was never in the image — and it was removed entirely: host collector,
  jail setter, macOS backend, tests and docs. Do not re-add a forward for a tool the jail does
  not have.
- **Not a claim that `:ro` means immutable.** It means *non-persistent*: the staged file lives
  inside the workspace state tree, so the same inode is reachable through a writable path. See
  [`composed-file-permissions.md`](composed-file-permissions.md).

## Why it's this way

| Ruling | Why it holds |
| :--- | :--- |
| **The allowlist is `user.name` + `user.email`** (plus the in-jail `core.excludesFile`) | It satisfies "keep author identity, pass no credentials, do not confuse agents with UI settings" with nothing else in scope. Everything else useful is a credential, UI, or a dead host path. |
| **Host-compose + a `:ro` bind at git's default path** — not `GIT_CONFIG_GLOBAL`, not an imperative unset-and-set | Fresh composition kills the staleness bug by construction rather than by a scoped reconciler, and reusing the global-gitignore delivery machinery introduces no new env var and no new mechanism. |
| **`macos-user` keeps the imperative replay** | Seatbelt has no mount namespace, so there is no `:ro` file to bind; this is the same container-vs-native split every other mount-shaped surface has. |
| **The `[include]` goes first, and carries no `GIT_CONFIG_GLOBAL`** | Last-definition-wins means an include placed first keeps yolo's keys authoritative; an env var would be absent for exactly the invocations that need it most. |

## Current values

Verified at `38873c0d`. The prose above explains what each of these is for; this table is the
only place the values themselves are stated.

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| Forwarded keys | `user.name`, `user.email` | `Options.gitIdentityMountArgs` |
| In-jail global config path | `~/.config/git/config` (git's default) | `Options.gitIdentityMountArgs` |
| Writable sibling | `~/.config/git/config.local`, `[include]`d first | `gitLocalConfigInJail`, `gitIncludeHeader` |
| Host-side staged file | `<workspace state>/yolo-gitconfig` | `Options.gitIdentityMountArgs` |
| Host key lookup | `git config --get <key>`; `--global --get core.excludesFile` | `Options.hostGitConfigGet` |
| `macos-user` variables | `YOLO_GIT_NAME`, `YOLO_GIT_EMAIL` — **and NOT `YOLO_GLOBAL_GITIGNORE`**, which `configureGit` reads and nothing sets (see the note below) | `macosuser.MacosSandboxEnv` sets; `entrypoint.configureGit` reads |
| Home-root alias | `~/.gitconfig` → `.config/git/config` | `storage.EnsureSymlink` |

> [!WARNING]
> **`YOLO_GLOBAL_GITIGNORE` is read and never set — the global gitignore does NOT
> replay on `macos-user`** (measured 2026-09-11). `entrypoint.configureGit`
> (`internal/entrypoint/identity.go:22`) reads it and points `core.excludesFile` at
> it; **nothing in the tree writes it.** `MacosSandboxEnv`
> (`internal/macosuser/orchestrator.go`) forwards exactly two pairs —
> `YOLO_GIT_NAME`/`user.name` and `YOLO_GIT_EMAIL`/`user.email` — and the container
> backends do not use the env route at all: `Options.gitIdentityMountArgs` replaced
> it with a composed gitconfig plus a `:ro` mount of the gitignore, precisely so a
> CLEARED host key can be reflected (an add-only setter could never remove one).
>
> So the variable is a leftover of the replaced mechanism, and the user-visible
> consequence is real: on `macos-user`, git *identity* replays and the global
> *gitignore* does not. Whether that backend should carry the gitignore at all is
> undecided — it has no bind mounts, so the container answer does not port.
