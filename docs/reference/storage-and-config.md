---
status: current
verified: 2026-09-09
verified_commit: d8cf1cf8
covers:
  - internal/paths/
  - internal/config/load.go
  - internal/config/assembled.go
  - internal/config/drift.go
  - internal/config/snapshot.go
  - internal/config/packs.go
  - internal/storage/ensure.go
  - internal/packsrc/store.go
  - internal/cli/run/flock.go
  - internal/entrypoint/identity.go
  - internal/entrypoint/bootlog.go
  - integration/harness_test.go
tags: [storage, config, scopes, identity, locks, state]
summary: "Where yolo's bytes live and who owns them: the three config scopes and how they merge, the machine-wide storage tree, the per-workspace state dir, the config-ownership principle that governs everything yolo generates inside a jail, git identity, and which shared paths carry no lock."
---

# Storage, config scopes and identity

**Status:** CURRENT as of 2026-09-09, verified against `d8cf1cf8`.

Every durable byte yolo writes lands in one of three places, and which one is a
consequence of a single question: **is this state one truth per machine, one per
workspace, or one per jail?** This document is the map — the config scopes and their merge
order, the machine-wide storage tree, the per-workspace state dir, and the ownership rules
that decide who may write what. How those paths become `/home/agent` is
[`jail-home.md`](jail-home.md).

| Component | Lives in |
| :--- | :--- |
| Every storage path, and the one-writer rule for deriving them | `internal/paths` (`GlobalStorage`, `GlobalStorageUnder`, `WorkspaceStateDir`, `WorkspaceHomeState`) |
| Config load, merge, includes | `internal/config` (`MergeConfig`, `LoadJSONCWithIncludes`, `LoadConfig`) |
| The assembled config and the frozen boot baseline | `internal/config` (`WorkspaceAssembledConfigPath`, `WorkspaceConfigBootPath`) |
| Approval snapshots | `internal/config` (`snapshot.go`), `paths.ApprovalsDir` |
| Pack selection's scope rule | `internal/config` (`LoadPacks`) |
| Pack address resolution, and the staged-tree fallback | `internal/packsrc` (`Store.Resolve`, `Resolved.StagedFrom`, `Store.Getenv`) |
| Machine base construction and layout migration | `internal/storage` (`EnsureGlobalStorage`, `MigrateStorageLayout`) |
| The launch lock | `internal/cli/run` (`flock.go`) |
| Git identity composition | `internal/cli/run` (`gitIdentityMountArgs`, `composeGitconfig`), `internal/entrypoint` (`configureGit`, darwin only) |
| Boot and provisioning logs | `internal/entrypoint` (`bootlog.go`) |

**Reads with:** [`jail-home.md`](jail-home.md) (how these paths are mounted into a jail),
[`pack-system.md`](pack-system.md) (a pack's own config surfaces, and `packs` selection),
[`config-safety.md`](config-safety.md) (the approval flow),
[`image-staging-vs-baking.md`](image-staging-vs-baking.md) (the image and package caches
under `build/`). **`yolo config-ref` is the authority for config keys** — this document
describes scopes and ownership, never the key list.

---

## Principles

**One truth per fact, at exactly one scope.** Nothing is stored twice at two scopes with a
reconciliation rule; a fact is machine-wide, per-workspace, or per-jail, and the choice is
made once.

**A path is derived in one place.** `internal/paths` owns every storage path. A caller that
has *already resolved a home* must pass it in (`GlobalStorageUnder`, `WrapDirUnder`,
`CapturesDirUnder`) rather than re-deriving from `$HOME` — the two differ the moment
anything renders into a home it was handed, and re-deriving silently writes into the
invoking user's real state dir.

**The workspace tree belongs to the operating agent.** `/workspace` is bind-mounted from
the host and yolo leaves it alone. The only exceptions are narrow, enumerated
isolation-boundary shadows.

**Config ownership: yolo composes generated config into the jail USER scope only.** Stated
in full below — it is the rule that constrains every generated file in a jail.

## The config scopes

Four sources, merged in order, later overriding earlier:

| Scope | File | What may live here |
| :--- | :--- | :--- |
| User | `~/.config/yolo-jail/config.jsonc` | anything, including every install-shaped key |
| Workspace | `<workspace>/yolo-jail.jsonc` | the repo's own needs; committed |
| Workspace-local | `<workspace>/yolo-jail.local.jsonc` | per-machine tweaks; kept out of version control |
| Environment | a small set of `YOLO_*` variables | last-resort overrides |

`yolo-jail.local.jsonc` is auto-merged whenever it sits beside `yolo-jail.jsonc` — no
`include_if_found` entry needed. Any file at any scope may pull in more with
`include_if_found`, resolved relative to the *including* file's directory; a missing
include is skipped, later wins, and cycles are detected.

**The merge rule is uniform and recursive: maps merge key-by-key, lists union, and
anything else is replaced by the override.** There is no per-key exception — the one that
existed (a workspace value replacing the user's for agent selection) was deleted along with
the key it served, rather than left inert.

> [!CAUTION]
> **A user-scope ceiling that a workspace narrows is inexpressible.** Because every list
> unions at every depth, a workspace can only ever **widen** a list — which for an
> allowlist inverts the intended property. Any setting that needs to be bounded from above
> must be a scalar, or must declare its own scope. This is the correction recorded as `R5`
> in [`loophole-system.md`](loophole-system.md#why-its-this-way).

**Install-shaped keys are user-scope only, and are read from the user file directly.**
`LoadPacks` deliberately takes no merged config: reading the user file is what makes
workspace scope *inexpressible* rather than merely refused. The same reasoning applies to
`cache_relocations`, which mounts an arbitrary host path read-write, and to a `host_files`
entry that names a `source`. The general shape: a weak, agent-editable scope may switch on
only what the strong scope already installed.

### The two per-launch artifacts

Both live in the workspace state dir, beside each other, and they answer different
questions.

- **`config-assembled.json`** — the merged config the host assembled for *this* launch,
  read verbatim in-jail. Every launch rewrites it. It is how an in-jail `yolo` sees the
  same effective config the launcher used without re-reading a user file it cannot reach.
- **`config-boot.json`** — the frozen *workspace* config the running jail was built from.
  It is the baseline `yolo config drift` compares against, which is why it is the
  workspace half rather than the merged whole: folding in the user scope would make "the
  config changed" fire on a user-config edit that this jail's workspace never saw.

**The approval record is not among them.** A config-change approval is stored host-side
under `approvals/<container-name>.json` and is **never mounted into a jail**, so the record
of what a human approved cannot be rewritten by whatever edited the config.

### The config-ownership principle

> **yolo composes generated config into the jail USER scope only. The workspace tree is the
> operating agent's, and mirrors the host.**

This governs every config file yolo *generates* inside a jail — agent settings, MCP and LSP
config, the global mise config, git identity — as distinct from yolo's own
`yolo-jail.jsonc`, which the scopes above govern.

- **User scope, yolo-owned.** yolo writes each generated file under `/home/agent`, into a
  per-workspace writable overlay. This is the *only* config surface yolo regenerates.
- **Workspace scope, agent-owned and host-mirrored.** yolo writes no agent's
  project-scope config. The enumerated exceptions are the `/dev/null` shadows over a
  VS Code MCP file and an overmind socket — isolation artifacts, not agent config.
- **Managed scope, yolo-owned and outside both.** Security-boundary keys go to an agent's
  *managed* config where one exists, which yolo owns outright with no contention.

The consequence that matters for regeneration: yolo and the agent write the **same**
user-scope file — yolo regenerates it each boot, the agent persists its own in-jail edits
there — so surviving regeneration needs a capture-diff overlay at the user scope,
uniformly, for every agent. No agent gets a "yolo owns a separate file" shortcut, because
the only separate file is a workspace file yolo will not touch.

## Machine-wide storage

Everything persistent lives under `~/.local/share/yolo-jail/`. **The set of subdirectories
is not listed here** — it has grown steadily and any list in prose is stale within a
sprint. `internal/paths` is the enumeration; `rg -n 'GlobalStorage\(\)' internal/` finds
every one. The four that carry meaning for how a jail behaves:

- **`home/`** — the machine-wide base home, mounted `:ro` at `/home/agent`. Auth tokens and
  base configs; see [`jail-home.md`](jail-home.md#why-the-base-is-read-only-with-symlink-hatches).
- **`cache/`** — mounted read-write at `~/.cache` in every jail. A shared download cache.
- **`mise/`** — the jail-land mise store, mounted at `/mise`. Shared by every jail. **The
  host's own mise installation is never a party to this** and is never mounted.
- **`agents/<container-name>/`** — the per-jail staging tree for composed briefings and
  merged skills, rebuilt on every invocation and mounted `:ro`.

Also worth knowing by name, because each is a distinct on-disk contract rather than a
cache: `approvals/` (never mounted), `captures/` (the machine-wide install-capture store,
mounted `:ro` at `/ctx/captures`), `packs/` (the content-addressed pack store),
`flake-bundle/` (what `just install` stages), `locks/`, and `bin/wrap` (the one generated
directory a *user* is asked to prepend to their own PATH).

### What is shared and writable, and what serializes it

Three tiers are shared across jails **and writable**, so "jails do not share writable
paths" is false as a general statement:

- `cache/`, at `~/.cache`;
- `mise/`, at `/mise`;
- every pack `state` contribution declared `scope: "machine"`, mounted from `home/` rather
  than from the workspace.

The first two are content-addressed caches, where concurrent writers are benign. The
machine-scope credential dirs are **not** caches: one of them is precisely what the OAuth
broker's host-side lock exists to serialize, because two jails refreshing at once burn a
single-use refresh token.

What the host CLI does guard:

- **Image build** — the flake builds in place, and nix's own store handles concurrent
  builds atomically. The run-result link uses a per-PID unique path so one build cannot
  delete another's.
- **Two launches of one workspace** — an exclusive `flock` under `locks/`, keyed on the
  container name, which derives from the workspace path. Taken before a fresh launch and
  released once the container is visible, so the loser attaches instead of racing. **It is
  per workspace and says nothing about two different workspaces writing the shared tier
  above.**

**No cross-jail interference on the per-workspace tier** is therefore a consequence of the
*split*, not of a lock: a jail's install prefixes, overlays and generated script dirs are
all backed by its own workspace state dir, so two jails on different workspaces write
different directories and cannot collide.

> [!WARNING]
> **`~/.yolo-entrypoint.lock` is a vestige — its name is the only thing that claims a
> lock.** It is created, mounted and reserved against `writable_home_dirs`, and **nothing
> in Go ever `flock`s it.** It is residue from a real guard in an earlier CLI, which worked
> because `/home/agent` was then shared and writable, so a lock in `$HOME` was
> machine-wide. Both premises are gone: the base is `:ro`, generation targets the
> per-workspace overlays, and this file is itself one of those overlays — so even `flock`ed
> it could not serialize two workspaces. It is **not** the launch lock, which is host-side
> and under `locks/`. Do not read the filename as a guarantee.

The one race still live at that file's per-workspace scope is a *same-workspace* one: the
blocker and launcher generators wipe-then-repopulate their dirs, and the host lock releases
when the container reports running rather than when provisioning is done, so a second
launch's entrypoint can empty the blocker dir mid-populate. Deleting the vestigial file is
correct only once that is either serialized or ruled out.

## Per-workspace state

Each workspace carries a gitignored `.yolo/` directory. Its two documented halves:

- **`.yolo/home/`** — the writable overlays bind-mounted over the `:ro` base. One
  subdirectory per rw home path, one file per single-file bind, one backing dir per
  `writable_home_dirs` entry, and `venv-shadows/` holding the per-side backing for
  `/workspace/.venv` and any other `per_side_paths` entry (a `/` in an entry becomes `__`
  in the directory name). **The contents are pack data**, not a fixed list: which state
  dirs exist follows from which packs are selected.
- **Logs and per-launch config** — `config-assembled.json` and `config-boot.json` as
  above, `startup.log` from the last new-container provisioning run, and `boot.log` (plus
  one rotation) which carries everything the entrypoint said.

`boot.log` exists because it **outlives the container**: a boot that *refused* leaves no
jail to ask, and the provisioning log is written by a shell wrapper that only runs on a
fresh container, so it cannot cover an exec or a refusal.

First boot for a new workspace installs tools into empty overlay dirs; later boots reuse
what is there.

## Identity

Git identity is **composed on the host and mounted read-only** on the container backends.
The host's `user.name` and `user.email` are read with `git config --get` (so a repo-local
identity wins, matching what a human would get), rendered into a whole config file, and
bound `:ro` over the jail's global git config. The host's own `~/.gitconfig` is never
mounted — it may hold credentials, tokens and aliases.

Regenerating the **whole file** every run is the point: it reflects an identity that was
*changed or cleared*, which the old env-forward plus in-jail `git config --global` replay
structurally could not — an add-only setter can never remove a key. On macos-user, where
there are no mounts, `configureGit` still performs that replay from forwarded env vars.

The composed file opens with an `[include]` of a **writable sibling** in the same overlay
directory. Git applies includes in file order and last-wins per key, so putting the include
first keeps yolo's identity authoritative for the keys it sets while letting anything else
— aliases, `pull.rebase`, a per-user override — persist.

> [!WARNING]
> **Do not make `~/.gitconfig` writable to "fix" `git config --global`.** That command
> still fails, deliberately: the home-root path is a symlink onto the `:ro` composed file.
> What the include buys is that the file it fails on now *tells* the user where to write.
> Making the write succeed means making the composed file writable, which reopens the
> identity-composition hole the `:ro` mount exists to close. Pairing it with a
> `GIT_CONFIG_GLOBAL` export is not a fix either — that is only set for shells that source
> the generated bashrc, so a git invoked from an agent subprocess with a sanitized
> environment fails exactly as before.

The global gitignore *is* mounted, `:ro`, when the host's `core.excludesFile` (or the
conventional path) resolves to a real file, and the composed config's `excludesFile` points
at the in-jail path. With no identity **and** no gitignore, nothing is emitted at all.

## Pack address resolution inside a jail

A jail can see neither the host paths a local pack was selected by nor the machine's pack
store, so both resolve to a "not a directory" / "never fetched" failure in there for the
same structural reason. **The staged tree the launcher mounted is what was actually
delivered**, so `Store.Resolve` tries the address first and falls back to that tree when
the address fails.

Three properties of that fallback are easy to get wrong:

- **It keys on the failure, not on `IsLocal()` or on "am I in a jail".** Keying on the
  failure covers the fetched-pack case for free, and it cannot misfire on a host, where no
  staged tree is mounted and the branch never fires.
- **`Resolved.StagedFrom` names the tree it fell back to**, and is empty for every ordinary
  resolve. That is what lets `yolo check` report `staged at <path>` while the launch stays
  silent — for a nested launch the delivered copy is the *normal* source, not a
  degradation.
- **The delivered directory is named by the entry's slug, not its name.** They agree for
  every conventional name and diverge silently for one needing escaping, so both callers
  pass the slug. And the name has to be *threaded* rather than derived: it is a config fact
  (an explicit `name:`, else the source URL's last path segment), so recovering it inside
  `packsrc` would be a second spelling of a rule `internal/config` owns.

> [!WARNING]
> **Do not copy this fallback into a second caller.** It lives inside `Resolve` so every
> caller — including one written tomorrow — is correct by construction. The bug it fixed
> was exactly this shape: the rule existed, tested, in `yolo check` alone, and the launch
> path never learned it, so a nested launch refused outright while the preflight printed
> `[PASS]`. The layering cost is real and named where it happens: a store that resolves
> *addresses* now knows something about *mounts*.

## Test-harness isolation

Container integration tests **isolate `HOME` by default**, in `requireJail` rather than in
any project-writing helper: `requireJail` is the line every container test already writes,
so a new test cannot read machine state by forgetting a helper — it would have to skip the
gate that makes it a container test. `isolateHome` wraps the shared-store seeding so there
is one implementation of that rule, and `ambientHome(t, reason)` is the opt-out, which
must name its reason.

The reason is not flakiness. **An ambient user config does not only break assertions; it
can satisfy them.** Blocked tools, mise tools, MCP servers and loopholes all merge into the
jail a test launches, so a machine-local config can make a test pass for a reason the test
never states. And it is **asymmetric with CI**: a fresh runner has no user config, so CI
structurally cannot observe either direction.

Two details the isolation needs and a naive version gets wrong:

- **The host home is captured on first redirect.** A packs-needing test isolates twice, and
  reading `$HOME` the second time links the second home's shared stores to the *first* temp
  home's symlinks — a chain that dangles as soon as that tempdir is removed.
- **The nix fetcher cache and nix config are shared stores.** The nix *store* is shared
  regardless, but the cache mapping a locked flake input to its store path is HOME-rooted,
  and the nix config holds the substituter that decides download-versus-compile. Hiding
  them sends nix to the network for inputs it already had.

## What this does not license

- **Not** a second place to derive a storage path. `internal/paths` is the only one, and a
  resolved home is passed in rather than re-read.
- **Not** writing agent project-scope config. The workspace tree mirrors the host; the
  `/dev/null` shadows are the whole enumerated exception.
- **Not** an install-shaped key at workspace scope. The scope split is what makes
  per-workspace enablement safe to offer at all.
- **Not** a lock inferred from a filename. The one real launch lock is host-side and under
  `locks/`; the shared writable tiers carry none, and the parts that need serialization say
  so where they need it.

## Current values

Verified at `d8cf1cf8`. The prose above explains what each of these is for; this table is
the only place the values themselves are stated.

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| Machine storage root | `~/.local/share/yolo-jail/` | `paths.GlobalStorage`, `paths.GlobalStorageUnder` |
| Workspace state dir | `<workspace>/.yolo/` | `paths.WorkspaceStateDir` |
| Workspace home overlays | `<workspace>/.yolo/home/` | `paths.WorkspaceHomeState` |
| User config | `~/.config/yolo-jail/config.jsonc` | `paths.UserConfigPath` |
| Workspace config, and its local sibling | `yolo-jail.jsonc`, `yolo-jail.local.jsonc` | `internal/config/load.go` |
| Per-launch merged config | `<workspace>/.yolo/config-assembled.json` | `config.WorkspaceAssembledConfigPath` |
| Frozen drift baseline | `<workspace>/.yolo/config-boot.json` | `config.WorkspaceConfigBootPath` |
| Approval snapshot (host-only, never mounted) | `<machine storage>/approvals/<container-name>.json` | `paths.ApprovalsDir` |
| Launch lock | `<machine storage>/locks/<container-name>.lock` | `internal/cli/run/flock.go` |
| Storage layout version | 2 | `storage.StorageLayoutVersion` |
| Boot log, and its one rotation | `<workspace>/.yolo/boot.log`, `boot.log.prev` | `internal/entrypoint/bootlog.go` |
| Provisioning log (fresh containers only) | `<workspace>/.yolo/startup.log` | `internal/cli/run/command.go` |
| Host launch-wrapper dir (the one a user prepends) | `<machine storage>/bin/wrap` | `paths.WrapDir` |
| mise store, in-jail | `/mise`; `yolo-mise-data-v2` named volume on macOS | `internal/cli/run/assemble.go` |
| Jail env: the mise block | `MISE_DATA_DIR=/mise`, `MISE_TRUSTED_CONFIG_PATHS=/workspace`, `MISE_ENV=jail`, `RUSTUP_HOME=/mise/rustup`, `CARGO_HOME=/mise/cargo` | `internal/cli/run/assemble.go` |
| Jail env: the loader path | `LD_LIBRARY_PATH=/lib:/usr/lib:/usr/lib/<multilib>` | `internal/cli/run/assemble.go`, `storage.LinuxMultilib` |
| Jail env: install prefixes | `NPM_CONFIG_PREFIX=/home/agent/.npm-global`, `GOPATH=/home/agent/go` | `internal/cli/run/assemble.go` |
| Jail env: no interactive UI | `PAGER=cat`, `GIT_PAGER=cat`, `EDITOR=cat`, `VISUAL=nvim` | `internal/cli/run/assemble.go` |
| Jail env: the full set | one `-e` block plus the per-entry channel file | `internal/cli/run/assemble.go`, `writeUserEnvFile` |
| Writable git-config sibling | `~/.config/git/config.local`, pulled in by an `[include]` | `internal/cli/run/assemble_parts.go` (`gitLocalConfigInJail`) |

## Why it's this way

Forward-facing rulings a maintainer would otherwise undo. Ids keep their original
spelling — they are cited from code comments and integration tests, and after the design
doc was deleted this table is where they resolve.

| ID | Ruling | Why it stays |
| :--- | :--- | :--- |
| <a id="oq-sc1"></a>[`OQ-SC1`](#oq-sc1) | The staged-tree fallback lives **inside `packsrc.Store.Resolve`**, not copied into the launch path | One writer, every caller correct by construction rather than by remembering. The alternative had already shipped once as its own defect: the rule existed and was tested in `yolo check` alone, so a nested launch refused while the preflight passed. Supersedes an earlier *inheritance* framing entirely — the generated config was never the defect, because a local pack's host address is its true provenance. |
| <a id="oq-sc2"></a>[`OQ-SC2`](#oq-sc2) | **Withdrawn**: "should the preflight predict this refusal" was the wrong question | `yolo check` is *ahead* of the launcher here, not behind it — it had already resolved the staged tree and was reporting `[PASS]`. The general concern that a preflight does not predict a launch refusal stands on its own for the fetched-pack case; this was not an instance of it. |
| <a id="oq-sc3"></a>[`OQ-SC3`](#oq-sc3) | Container integration tests **isolate `HOME` by default**, and an ambient-config test must opt in and name its reason | It finishes a rule the harness already half-enforced by refusing a workspace `packs` key. An ambient config can *satisfy* an assertion, and CI — with no user config — structurally cannot see either direction, so the exposure is invisible exactly where it would be caught. |
| `OQ-D1` | The approval snapshot is host-side and never mounted | The record of what a human approved must not be rewritable by whatever edited the config. Settled in [`config-safety.md`](config-safety.md). |
| `A5` | The composed git config includes a **writable sibling**, placed first | Without it `~/.gitconfig` was a decoy: the write failed on a path that looks writable with an error explaining nothing. Include-first keeps yolo's keys authoritative while everything else persists. |
