---
title: "Why does every podman jail share one home? It should not — a per-jail skeleton instead"
date: 2026-09-20
status: accepted
tags: [base-home, jail-home, storage, backend-parity, design]
summary: "Podman mounts ONE machine-wide base, <state>/home, read-only at /home/agent in every jail. A jail needs nothing in it: it holds empty mountpoints, three redirect links, the machine's shared credential dirs and a login seed only the host reads. Sharing it is the defect: one workspace's pack dirs, host_files links and old bytes show up in every jail, and a jail that did not select claude can read the machine's Claude credential file. The design: mount a per-jail read-only skeleton built from the SELECTED packs, keep <state>/home as the machine store for the shared dirs and the Claude login seed, delete seedAgentDir, and leave legacy bytes unmounted and unread."
---

# Why does every podman jail share one home? It should not — a per-jail skeleton instead

**Status:** ACCEPTED, 2026-09-25: rewritten around a new premise after the maintainer's review,
then every open question settled the same day. **Steps 1–3 of [§8](#8-build-order-and-done-conditions)
BUILT 2026-09-25** (the seed fixes, the podman skeleton, the refusal's deletion and the reworded
`yolo check` report); `integration/homeskeleton_test.go` PASSED in a nested, rootful jail the
same day, and ROOTLESS in CI on both arches the same evening (`ci.yml` run 36167524940). **Steps 4, 4a and 5 BUILT 2026-09-25**: the
Apple Container seed paths ([OQ-BH12](#OQ-BH12), host side only; a Mac run is still owed), name
reservation over the selected packs only ([OQ-BH14](#OQ-BH14)), and the reference docs and code
comments. **Review follow-ups BUILT 2026-09-25** ([§8](#8-build-order-and-done-conditions),
last bullet): the reaper can now reach a jail's skeletons, a refused launch leaves none, and a
configured pack's shared dir gets its bind source.
MEASURED: what the base holds on two bases, EROFS on the home root, the jail writing through `/workspace/.yolo/home`, the machine credential file sitting in the base, and a
host `rmdir` detaching a bind (in a user+mount namespace; `internal/prune/shadowed.go` records
the same failure in a real jail, 2026-07-04). UNMEASURED: the Apple Container seed defect (no
Mac) and a jail without claude actually reading the credential file (inferred from the mounts).
Rootless ID mapping of a host-built skeleton was MEASURED in CI, 2026-09-25. Evidence: [Appendix A](#appendix-a-evidence).

> **In short.** The shared base home is left over from an older design, and sharing it is the
> bug. Give each podman jail its own read-only skeleton, built from the packs that jail
> selected. Then a pack the jail did not select has no effect inside it.

**Why it matters.** Today one workspace's config reaches every jail on the machine: its
`writable_home_dirs`, its `host_files` links and every shipped pack's directory. Worse, a jail
that did not select the claude pack still sees `~/.claude-shared-credentials/.credentials.json`,
the machine's Claude refresh token, through the base (agy's dir likewise).

**The shape.** A per-jail *skeleton* (defined in [§1](#1-the-question-and-the-answer)) mounted
`:ro` at `/home/agent`. `<state>/home` stays as the *machine store*. `seedAgentDir` is deleted.
**Cost:** four writers move, one bind changes, the quarantine design is dropped
([§7](#7-what-died)). Rootless behavior can only be checked on a real host or in CI.

## Settled, 2026-09-25

The five questions closed in one review ([§10](#10-decision-ledger)). **Two are open for you,
both found in the build:** [OQ-BH15](#OQ-BH15), whether `host_files`' surface-path reservation
narrows to the selected packs too (it is the one rule left that reads every shipped pack), and
[OQ-BH16](#OQ-BH16), whether a launch removes its own skeleton when its jail ends, since on a
machine where one workspace is used alone the reaper reaches that workspace's skeletons only
through `yolo prune --apply`.

- **Yours:** [OQ-BH14](#OQ-BH14): name reservation covers only the **selected** packs'
  directories, so [DIR-BH1](#10-decision-ledger)'s one remaining shipped-set rule is the
  surface-path list that [OQ-BH15](#OQ-BH15) asks about. [OQ-BH10](#OQ-BH10): a new skeleton per
  fresh launch, never edited. [OQ-BH13](#OQ-BH13): the launch refusal and its hatch go with the
  skeleton; `yolo check` keeps a reworded report.
- **Delegated to the build** ("an implementation detail"): [OQ-BH9](#OQ-BH9), the skeleton
  lives under `AgentsDir/<cname>/`; [OQ-BH12](#OQ-BH12), the Apple Container seed gets
  runtime-aware paths, verified on a Mac.

**Decided here without a ruling; object if any is wrong:**

- The skeleton is bound `:ro`, and `<state>/home` stays as the machine store. Your words were
  "per jail"; the read-only root is this design's reading ([§5](#5-alternatives-with-verdicts) B).
- Delete `seedAgentDir`; limit `SyncClaudeJSONSeed`'s forward pass to `claudeJSONSeedKeys`,
  which stays core-owned ([§2.7](#27-the-seed)).

**To measure, not to rule:** whether a `host_files` entry under an unselected shipped pack's dir
fails today ([§2.8](#28-reservation-is-a-rule-about-config-names-not-about-directories)). The
fix no longer depends on the answer.

**Reads with:** [`base-home-legacy-state-plan.md`](base-home-legacy-state-plan.md) (the build
sketch, not a hand-off), [`jail-home.md`](../reference/jail-home.md) (the home layout this
changes), and [`../reference/macos-user-home-tiers.md`](../reference/macos-user-home-tiers.md)
([`OQ-HT4`](../reference/macos-user-home-tiers.md#oq-ht4), the parity ruling this keeps).

---

## 1. The question, and the answer

The maintainer asked: *"Why is this not just per jail? It seems like it's nothing."* It can be
per jail, and for a jail it is nothing.

**Terms used here:**

- **`<state>`** is `paths.GlobalStorage()` (`~/.local/share/yolo-jail`); **the base home** is
  `paths.GlobalHome()` = `<state>/home`, bound `:ro` at `/home/agent` today (`podmanBaseMounts`).
- **`wsState`** is `<workspace>/.yolo/home` (`paths.WorkspaceHomeState`), the per-workspace
  overlay. Podman names its directories with the leading dot stripped (`claude`, `config`).
- **`cname`** is the container name, `runtime.FromWorkspace(workspace)`, one per workspace, so
  on podman "per jail" and "per workspace" mean the same thing.
- **Skeleton** *(coined here)*: a per-jail directory bound `:ro` at `/home/agent`, holding only
  the mountpoints and redirect links a launch needs. It is **not** a home and holds no file
  content.
- **Machine store** *(coined here)*: `<state>/home` once it stops being mounted at
  `/home/agent`. It holds the machine-scope shared dirs and the Claude login seed.
- **Legacy bytes**: files left in `<state>/home` from before 2026-04-07, when it was the
  shared writable home.
- **Fresh-launch path**: a launch that starts a container rather than attaching to one.

**What a podman jail gets from the base, and whether any of it needs to be shared:**

| What | Needs sharing? | After this design |
| :--- | :--- | :--- |
| Empty mountpoints: every shipped pack's dirs, the core dirs, eight single-file mountpoints, and every `writable_home_dirs`/`host_files`/`files`/skills/briefing mountpoint any launch ever made | No | The skeleton, from this jail's selection |
| Three redirect links (`.claude.json`, `.gitconfig`, `.bashrc`) into per-workspace binds | No; their targets are per-workspace | The skeleton |
| A read-only home root, so writing an undeclared path fails with EROFS | No | The skeleton, also bound `:ro` |
| Machine-scope shared dirs (`.claude-shared-credentials`, `.gemini-shared-credentials`, `.pi-shared-npm`) | **Yes, on purpose** | Their own rw bind from `<state>/home/<dir>` when the pack is selected, as today. Today they are also readable through the base when it is **not**; after, they are absent then |
| The Claude login seed, `<state>/home/.claude/claude.json` | **Yes, on purpose** | Unchanged. Only the host reads and writes it |

**Why it was shared: history, not design.** In the first public commit (`7deeb762`,
2026-03-22), `<state>/home` was the one **writable** `/home/agent` every jail shared. Commit
`2bcc4e76` (2026-04-07) made it `:ro`, moved every writable path into per-workspace overlays,
and added `seedAgentDir` to copy the old auth files out. What was left is a shell nobody
designed. It is a union of **every** shipped pack's directories only because `ensureStorage`
runs before `loadAndValidateConfig` (`run.go`), when the selection is not known yet.

**What sharing costs today** ([Appendix A](#appendix-a-evidence)):

- **A jail that did not select claude or agy can read that pack's machine credentials.** The
  shared dirs live inside the base, and their own bind is emitted only for selected packs
  (`packload.SharedDirs(in.packs)`, `assemble.go`). Under rootless podman, jail root maps to the
  host user, so mode `0600` does not stop the read (INFERRED; the file's presence MEASURED).
- **The same jail can read the machine `oauthAccount`**, through the `.claude.json` link into
  the base's `.claude/claude.json`.
- **Other workspaces' leftovers and every unselected pack's dir, with any legacy bytes in it,
  show up in every jail**, and nothing removes them (MEASURED). That includes a workspace-scope
  `host_files` home-root link (INFERRED: `prepareHostFiles`).
- Host-side, separately: **`seedAgentDir` copies every top-level file** of `<state>/home/.<dir>`
  into each new workspace, legacy bytes included, whatever is mounted.

**The rule this design is checked against** is [DIR-BH1](#10-decision-ledger): *"a
non-selected pack can never have an impact."* It has no exceptions: name reservation, the last
one, now covers only the selected packs ([OQ-BH14](#OQ-BH14)).

## 2. The design: a per-jail skeleton

### 2.1 What the skeleton holds

Built from **this launch's** loaded config and selected packs, and nothing else:

- Core's own dirs, `paths.HomeSkeletonCoreDirs()`, and the eight single-file mountpoints
  (`paths.HomeFileMountpoints()`). The core dirs are `paths.BaseHomeCoreDirs()` without the pi
  pack's `.pi/agent`, which in a skeleton would put a `~/.pi` in every jail; the pi pack declares
  `.pi` itself. `BaseHomeCoreDirs` keeps it for the legacy walk, since an old base holds it.
- The mountpoints for the selected packs' `WritableDirs` and `SharedDirs`.
- The three `paths.HomeFileRedirects()` links. A link whose target dir is not bound dangles and
  reads as absent, which is correct.
- Mountpoints for `config.WritableHomeDirs(cfg)`; for `host_files`, the WritableDir mountpoints
  and the Symlink links.
- Mountpoints for the selected packs' `files` targets outside a writable dir, their skills
  destinations and their briefing destinations.

**Every `-v` destination directly under `/home/agent` either exists in the skeleton or sits
inside another bind.** A test over the argv pins that ([§8](#8-build-order-and-done-conditions)).
The mountpoints stay even though `jail-home.md` records that seven experiments could not
reproduce EROFS on a nested mountpoint: they are cheap, and they make mode and ownership
deterministic.

### 2.2 Where it lives: host-only, never in wsState

**Not in `wsState`**: `/workspace/.yolo/home` is writable from inside the jail (MEASURED). A
skeleton there would be read-only in name only, and the host's `MkdirAll`/`touchFile`/
`EnsureSymlink` would follow links the jail planted. It lives under
`paths.AgentsDir()/<cname>/` ([OQ-BH9](#OQ-BH9)): host-only, keyed per jail, already bound into
jails for skills and briefings, and already reaped by liveness (`PruneOrphanAgentStaging`,
which reaps nothing when liveness is unknown, never the launching jail's own dir, and has a
one-hour age floor).

**Found in review, 2026-09-25: the reaper could not reach it.** `PruneOrphanAgentStaging`
keeps every name that is live **or tracked**, and a `--rm` jail's tracking file
(`CONTAINER_DIR/<cname>`, the container-to-workspace map `yolo ps` reads) survived every normal
exit: only a stale-container removal, `yolo ps`, `yolo check`'s cleanup and a capture removed
it. So a workspace in use kept its entry and gained one skeleton per fresh launch. **Fixed:** a
launch removes the tracking file when it sees its jail end (normal exit, the signal arm, a
runtime that never started), and only when the runtime answered that no container of that name
exists and the workspace lock was free (`forgetGoneContainer`,
`internal/cli/run/trackingcleanup.go`). Each of those three ends releases the launch's own lock
first, the normal exit included: the lock's other release waits on a poll for the container to
show as running, so a child that exits before it ever does would otherwise find its own lock
held and keep the file. The reaper then does what [OQ-BH10](#OQ-BH10) assumed.
What it still cannot reach is [OQ-BH16](#OQ-BH16).

### 2.3 When it is built

- **Only on the fresh-launch path, and only for podman**, where `prepareWsState` and
  `prepareHostFiles` run today: after `loadAndValidateConfig`, after the attach decision, and
  under the workspace flock. Never on attach. Apple Container and macos-user build none
  ([§2.9](#29-backends)).
- **Failure.** Today the entries leaving `EnsureGlobalStorage` (core dirs, file mountpoints,
  redirects) are **fatal**: each loop returns its error and `Run` exits 1. The config- and
  pack-driven ones (`prepareWsState`, `prepareHostFiles`, `preparePackFilesGlobal`) are
  **best-effort** (`_ =`). The skeleton keeps that split, and a failure names the path, since
  podman's own error would name neither. It also keeps their ORDER: every fatal entry, the
  redirects included, is made before any best-effort one, so a pack entry landing on a redirect
  name fails on its own, as a warning, instead of making the redirect fail the launch.
- **A launch that does not start a container leaves no skeleton** (found in review,
  2026-09-25). A fatal entry removes the builder's own partial directory, and every return
  between the build and the container start (two pre-flights that read the assembled argv, and
  a runtime that never started) removes the skeleton first (`discardUnheldSkeleton`). No
  container ever held that directory, so [§2.4](#24-the-three-rules-the-shared-base-obeys-by-accident)'s
  rule 2 does not apply to it.

### 2.4 The three rules the shared base obeys by accident

The shared union is host-only, only ever grows and is never pruned. That avoids three hazards
by accident. The skeleton has to state them as rules:

1. **Build it outside the workspace** ([§2.2](#22-where-it-lives-host-only-never-in-wsstate)).
2. **Never remove a mountpoint under a live jail**: a host `rmdir` silently detaches the bind
   inside (MEASURED). **Today the launch path cannot prove that no container named `cname` is
   alive.** A failed probe reads as "no container", and a failed stale-container removal can
   still fall through to the fresh path; the flock does not report whether it was acquired
   (INFERRED from `lifecycle.go`, `run.go`, `flock.go`; detail in
   [Appendix A](#appendix-a-evidence)). So editing a skeleton in place needs two new, UNBUILT
   signals: a tri-state liveness probe that removes only on "known absent", and an `acquired`
   flag on the lock. [OQ-BH10](#OQ-BH10)'s ruling needs neither: a new skeleton per launch
   never removes anything.
3. **Keep `<state>/home` as the machine store** ([§2.5](#25-the-machine-store-stays)).

### 2.5 The machine store stays

Only its mount at `/home/agent` goes. It still backs the shared dirs' rw binds on both container
backends (`assemble.go`, `appleContainerBaseMounts`), the Claude login seed
(`syncClaudeJSONSeed`), the OAuth broker's default `--creds-file` (`defaultCredsPath`,
`internal/oauthbroker/oauthbrokercmd.go`) and the stranded-credential rescue
(`migrateOldOverlay`). `config.agentWritableTrees` keeps `paths.GlobalHome()`, because jails
still write the shared dirs; the host-built `:ro` skeleton does not join that list.

### 2.6 Which writers move

| Symbol | Writes into `<state>/home` today | After |
| :--- | :--- | :--- |
| `storage.EnsureGlobalStorage` | the union (`EmbeddedWritableDirs` + `EmbeddedSharedDirs` + `BaseHomeCoreDirs`), `fileMountpoints`, the redirects, the credential migration | keeps the storage dirs, `EmbeddedSharedDirs` (bind sources) and the credential migration; the rest moves to the skeleton builder |
| `prepareWsState` | `writable_home_dirs` mountpoints, skills destinations, briefing `touchFile`s | skeleton |
| `preparePackFilesGlobal` | `files` mountpoints | skeleton |
| `prepareHostFiles` | WritableDir mountpoints, Symlink links (`EnsureSymlink`) | skeleton |
| `podmanBaseMounts` | binds `paths.GlobalHome()+":/home/agent:ro"` | binds the skeleton at the same place |

Unchanged: the shared-dir binds, `appleContainerBaseMounts`, the seed path and
`migrateOldOverlay`. [OQ-BH10](#OQ-BH10)'s ruling leaves `acquireWorkspaceLock` and the
container probes in `lifecycle.go` unchanged.

### 2.7 The seed

Decided here, not by a ruling:

- **Delete `seedAgentDir`** on every backend. Its only inputs since April are legacy bytes,
  zero-byte mountpoint files, and `claude.json`, which `SyncClaudeJSONSeed` already handles
  (that nothing else is live is INFERRED; the risk is in [§6](#6-risks)). Stopping the mount
  would not stop the copy, because the seed reads `<state>/home` on the host.
- **Limit `SyncClaudeJSONSeed`'s forward pass to `claudeJSONSeedKeys`** (`oauthAccount`,
  `hasCompletedOnboarding`). Today the forward loop copies **every** key the workspace lacks;
  only the reverse pass uses the allowlist (`internal/storage/claudejson.go`). So a legacy
  seed's `projects`/`mcpServers` reach every new workspace. The allowlist stays core-owned.
- Both are independent of the skeleton and ship first ([§8](#8-build-order-and-done-conditions)).

### 2.8 Reservation is a rule about config names, not about directories

`hostFileWritableRoots` (`internal/config/hostfiles.go`) and `reservedHomeDirs`
(`internal/config/writablehome.go`) are built in memory from `packload.Embedded*` and never look
at `<state>/home`. Today they refuse a `host_files` or `writable_home_dirs` entry claiming
**any** shipped pack's directory, so `writable_home_dirs: [".codex"]` is refused in a workspace
that never selects codex. That is an unselected pack's effect, against
[DIR-BH1](#10-decision-ledger).

**Ruled ([OQ-BH14](#OQ-BH14)): reserve only the selected packs' directories.** The maintainer's
reason: packs can come from anywhere and be added or removed at any time, so reserving the
shipped set never covered every pack a jail could select. An unselected pack is treated as if it
does not exist. So:

- One user-scope entry can be valid in one workspace and refused in another, and a pack selected
  later can refuse an entry that passed before. Both are accepted.
- `jail-home.md` treats reserving a name and creating a directory as one thing. It needs
  rewording, and after this change the two sets are the same: the selection's dirs.
- AGENTS.md's bullet "`packload.Embedded*` is deliberately NOT selection-gated" states the
  opposite rule for these lists. The build rewrites that bullet in the same change. The
  embedded-tree leasing half of that bullet is unaffected.

**To measure:** `StagingFor` answers "already under a rw bind" for every shipped pack's dir,
true only when the pack is selected. So a `host_files` entry such as `~/.codex/x` in a
claude-only jail probably fails with EROFS today (INFERRED, untested). Under the ruling the fix is
the same whatever the measurement shows: gate `StagingFor` on the selection too, so an
unselected pack's dir is an ordinary path and the entry gets its normal skeleton link.

**Built 2026-09-25.** The measurement was not taken; the fix does not depend on it.

- `writable_home_dirs` reserves core's names plus the selected packs' writable and shared
  dirs, and a refusal names the pack. The launch passes its staged set; validation, which
  runs before staging, resolves the same selection from the user config and the pack store,
  `needs` closure included (`resolveSelectedPacks`, `internal/config/selectedpacks.go`). A
  configured pack the store cannot resolve reserves nothing, because staging refuses that
  launch anyway.
- `StagingFor` and its three readers (the workspace-overlay half, the skeleton, the argv)
  take the same selected packs.
- **Review fixes, 2026-09-25.** A destination under a selected pack's *shared* dir, or under a
  `writable_home_dirs` entry, is already under a read-write bind and is not staged again:
  staging it was a second bind at one destination, which podman refuses, and the ruling made
  that easy to reach (`writable_home_dirs: [".codex"]` beside `~/.codex/x.json`). Apple
  Container stages nothing: its home is the read-write `wsState` bind, and nothing on that
  backend created the backing dir an emitted bind named. Validation loads a configured pack
  with an `only`/`exclude` filter from a copy staged through the same filter, since the filter
  can drop the manifest.
- **Two reservations still hold in a workspace that selects no pack owning them.** `.claude`
  stays a reserved `writable_home_dirs` segment, because core's `~/.claude.json` redirect
  targets `.claude/claude.json`; that is core reserving its own link's target, not a pack's
  directory. And `host_files` still refuses a destination any SHIPPED pack composes as a
  surface (`builtinSurfacePaths`, e.g. `~/.codex/config.toml`) whatever the selection. That
  list names files rather than directories, so this ruling did not reach it;
  [OQ-BH15](#OQ-BH15) asks whether DIR-BH1 narrows it too.

### 2.9 Backends

| Backend | Today | After |
| :--- | :--- | :--- |
| **podman** | shared `:ro` base at `/home/agent` | per-jail skeleton at `/home/agent`; every other bind and the dot-stripped `wsState` layout unchanged |
| **Apple Container** | `wsState` bound rw, whole, at `/home/agent`; no base mount | unchanged, except that `seedAgentDir` goes and the separate seed defect ([§3](#3-the-apple-container-seed-defect)) |
| **macos-user** | account home `/Users/_yolojail`; returns in `run.go` before `prepareWsState` | unaffected |

Legacy bytes are **left in `<state>/home`, unmounted and unread.** Nothing is moved or deleted,
so R1/R2 (move, never delete) are met; they still bind any future code that touches those
bytes. The old union's empty directories stay too, so a downgrade keeps working. What the
refusal and `yolo check` say about the bytes is [OQ-BH13](#OQ-BH13).

## 3. The Apple Container seed defect

A separate defect, there today whatever happens to podman. `prepareWsState` strips the dot
(`strings.TrimPrefix`) with no runtime branch, so on `rt=container` it seeds and syncs
`wsState/claude/claude.json`. But Apple Container binds `wsState` whole at `/home/agent`, so its
`~/.claude` is `wsState/.claude` and its `~/.claude.json` is `wsState/.claude.json`. The login
seed never reaches an Apple Container jail and never learns from one, and that backend's homes
carry unused undotted dirs (`~/claude`, `~/npm-global`). MEASURED in a `/tmp` copy of HEAD;
**UNMEASURED on hardware.** The fix is [OQ-BH12](#OQ-BH12).

**Built 2026-09-25, host side.** On `rt=container`, `prepareWsState` syncs the seed with
`wsState/.claude.json` (`claudeJSONInWsState`), creates the selected packs' dirs at their
dotted names plus `go`, and creates none of podman's dot-stripped bind sources
(`preparePodmanBindSources`). The one-time legacy migrations (`claude-projects` and the
rest) write to the dotted paths there too (`wsStateHomePath`). Unit tests pin the paths
(`internal/cli/run/acseed_test.go`). Whether a real Apple Container jail then boots logged in
is still unmeasured; it needs a Mac, and the Apple Container parity CI workflow is the
instrument.

**Found in review, 2026-09-25: the seed wrote through links.** `SyncClaudeJSONSeed` read and
wrote the workspace file with calls that follow a symlink, and that file is one the jail can
replace: `wsState/claude/claude.json` on podman, and on Apple Container `wsState/.claude.json`,
which is the jail's own `~/.claude.json`. A jail that planted a link there chose which host file
the next fresh launch overwrote with the seed's JSON, and which host file's login the seed
learned (the hazard [§2.2](#22-where-it-lives-host-only-never-in-wsstate) names). Fixed: a side
that is not a regular file takes no part in the sync, the open is `O_NOFOLLOW`, and the write is
a temp file renamed over the path. MEASURED as a unit test on both backends' paths
(`TestTheSeedDoesNotWriteThroughAJailPlantedLink`, and `internal/storage/claudejsonlink_test.go`).

## 4. What this does not cover

- **Not a migration**: nothing is moved, archived or deleted. Overlays that already hold seeded
  copies stay as they are, in the right tier.
- **Not the machine-scope shared dirs**, and not the `wsState` layout.
- **Not `PruneShadowedHome`.** It still empties and never removes, because a jail started by an
  older yolo still mounts `<state>/home`.
- **Not the macos-user account home.** Whether
  [`OQ-HT2`](../reference/macos-user-home-tiers.md#oq-ht2)'s discard should be reopened for an
  account used for real work belongs to that reference.
- **Not the nested-jail trust relation.** A nested jail's skeleton lives under the outer jail's
  home, which the outer agent can write; the outer jail is that nested jail's host, as today.

## 5. Alternatives, with verdicts

| Alternative | Verdict |
| :--- | :--- |
| **A. Keep the shared base; shadow the unselected pack dirs with empty binds** | **Rejected.** Other workspaces' `writable_home_dirs` and `host_files` leftovers still show, and it is one more mount per unselected pack |
| **B. Copy Apple Container: `wsState` rw at `/home/agent`** | **Rejected by this design, not by a ruling.** On that backend it works around a mount-count limit (`appleContainerBaseMounts` says so). It gives up EROFS on undeclared paths, and would rename every podman `wsState` out of the dot-stripped layout |
| **C. The skeleton inside `wsState`** | **Rejected.** The jail can write it ([§2.2](#22-where-it-lives-host-only-never-in-wsstate)) |
| **D. Keep sharing, build the union after config load** | **Rejected.** Selections differ per workspace, so a shared result still leaks |
| **E. Keep sharing, quarantine the legacy bytes (the previous design)** | **Superseded** ([§7](#7-what-died)) |

## 6. Risks

| Risk | Mitigation |
| :--- | :--- |
| A bind destination has no mountpoint and podman fails with an opaque crun error | The completeness test over the argv ([§2.1](#21-what-the-skeleton-holds)) |
| Rootless ID mapping makes the skeleton's owner or mode differ from today's | The same host user that creates `<state>/home` creates it; checked on a real rootless host ([§8](#8-build-order-and-done-conditions)) |
| A pre-April base holds a pack's only credential, or a file someone hand-placed to seed every workspace; with `seedAgentDir` gone, each NEW workspace on that machine needs one login | Live credentials travel through the shared-credential dirs and brokers; the pack dirs are MEASURED empty on the maintainer's host. That nothing else is live is INFERRED ([§2.7](#27-the-seed)). The seed file is the one deliberate channel, and it stays |
| The prune reaps a skeleton | Only when no container of that name is live or tracked, never the launching jail's own, past the one-hour floor. The next launch rebuilds it |
| Skeletons pile up, one per fresh launch | The tracking file now goes when the launch sees its container gone, so the prune reaches an idle workspace's entry; a launch that starts no container removes its own ([§2.3](#23-when-it-is-built)). A workspace used alone on its machine is reached only by `yolo prune --apply` ([OQ-BH16](#OQ-BH16)) |

## 7. What died

- **The quarantine machinery** of the previous text (archive bucket, manifest, lock, marker,
  EXDEV copy, SQLite sibling rule), and its four-class taxonomy as a launch-path classifier.
  Only detection was built; it survives in `internal/basehome` if `yolo check` keeps its report
  ([OQ-BH13](#OQ-BH13)). Bytes no jail mounts and no seed reads are inert disk.
- **The old invariants** ("a base state dir is a mountpoint whose steady state is empty",
  "top-level roots are never archived"), and **both old harms**: jails reading other
  workspaces' bytes goes with the mount, the seed copying them with `seedAgentDir`.
- **The launch refusal's justification** ("every jail can read the bytes, and `seedAgentDir`
  copies them"). Its comment's claim that every host write into `GlobalHome` is a directory
  `MkdirAll` is also false (`SyncClaudeJSONSeed`, `touchFile`, `EnsureSymlink`); its
  conclusion, that jails cannot write the base, holds.
- **Old questions [OQ-BH1](#10-decision-ledger)–[OQ-BH8](#10-decision-ledger)**, closed in the
  ledger.
- **Code comments citing this doc's pre-rewrite sections**, across `internal/basehome`,
  `internal/paths`, `internal/storage` and the two `basehomedisclosure` files, code and tests.
  Find them with `rg -n 'base-home-legacy-state|OQ-BH' internal` plus the bare `§` citations in
  `internal/basehome` and `basehomedisclosure*.go`. The build re-points or deletes them.

## 8. Build order and done-conditions

1. **The seed fixes** ([§2.7](#27-the-seed)), on every backend. They can ship alone.
2. **The skeleton**: the builder, the writers re-pointed, the podman bind switched, the golden
   argv updated.
3. **The refusal and the check report**, per [OQ-BH13](#OQ-BH13), in the same change as step 2.
4. **The Apple Container seed fix**, per [OQ-BH12](#OQ-BH12). It needs a Mac. Beside it, the
   plan's step 4a: **name reservation over the selected packs**, per [OQ-BH14](#OQ-BH14).
5. **The docs**: `jail-home.md`, `storage-and-config.md`, and the code comments of [§7](#7-what-died).

**Done when** (each followed by where it stands, 2026-09-25):

- Mountinfo shows `/home/agent` bound `:ro` from the skeleton; `touch ~/.x` fails with EROFS.
  *Met in a nested jail, 2026-09-25: `TestPodmanHomeIsAPerJailReadOnlySkeleton`
  (`integration/homeskeleton_test.go`) passed, all three subtests (`claude only`, `codex only`,
  `writable_home_dirs`). That podman was rootful (a nested jail forces `--userns=host`); the
  rootless bullet below was met in CI the same evening.*
- In a claude-only jail, `~/.codex`, `~/.copilot`, `~/.oh-omp`, `~/.pi-lens` and
  `~/.gemini-shared-credentials` do not exist. *Met at the builder
  (`TestTheSkeletonCarriesOnlyTheSelectedPacksDirs`); the integration test above checks
  `~/.codex`, `~/.copilot`, `~/.pi` and `~/.gemini-shared-credentials` in a container, and
  passed in the nested run above. `~/.pi` is checked because review found the first build put
  it in every jail, from core's `.pi/agent` (`TestCoreSkeletonDirsNameNoPacksDir` now pins
  core's list).*
- In a jail selecting neither claude nor agy (e.g. `packs: ["codex"]`), `~/.claude` and
  `~/.claude-shared-credentials` do not exist, `~/.claude.json` dangles, and no machine
  `oauthAccount` or refresh token is readable anywhere under `~`. *The same two tests; the
  integration test passed in the nested run above. The "readable anywhere under `~`" half is
  now asserted by its `codex only` subtest, which plants a machine login in the store first (a
  seed `oauthAccount` and a shared refresh token) and searches all of `~`, links followed, for
  any `.credentials.json` and for the planted values. The plant goes into a machine store
  PRIVATE to the test: the harness's isolated home links the whole state dir back to the
  machine's, so a plant through it would overwrite the developer's real Claude login
  (`TestPlantMachineLoginLeavesTheRealStoreAlone` pins that it cannot). That assertion was
  added after the nested run and has not run yet.*
- No other workspace's `writable_home_dirs`/`host_files` entries appear; a dropped `host_files`
  home-root link is gone at the next fresh launch. Attach leaves the skeleton byte-identical.
  *Met: `TestEachFreshLaunchGetsANewSkeleton`, and `TestAttachLeavesTheSkeletonByteIdentical`,
  which drives the real `attachExisting` up to its exec (a real runtime is the one step it does
  not reach).*
- A fresh launch writes nothing into `<state>/home` except the shared dirs, the seed file and
  the credential migration; a snapshot test holds everything else byte-identical. *Met for the
  writers the snapshot drives: `TestAFreshLaunchLeavesTheMachineStoreByteIdentical`,
  `TestEnsureGlobalStorageProvisionsNoHome`. `TestOnlyTheListedFunctionsNameTheMachineStore`
  pins the call sites: only the functions of `internal/cli/run` it lists may name
  `paths.GlobalHome()`, and each one that writes is one the snapshot drives. Not pinned: a write
  that spells the path another way, or one made in another package.*
- A new claude workspace boots logged in and gets no `claude.json` key outside
  `claudeJSONSeedKeys`. A file planted in `<state>/home/.copilot` reaches no workspace. *Met
  at the unit level: `TestSeedForwardsOnlyTheLoginKeys`, `TestALoggedInWorkspaceStillBackPropagates`,
  `TestAFilePlantedInTheMachineStoreReachesNoWorkspace`; "boots logged in" is not run.*
- A test over the golden argv fails if any destination directly under `/home/agent` has no
  skeleton entry; another fails if the builder's call site in the launch path is deleted. *Met:
  `TestEveryHomeBindHasASkeletonEntry`, `TestRunContainerBuildsTheSkeletonOnTheFreshPath`; each
  was run against a private copy with its wiring removed, and failed. The call-site test pins the
  builder's result into the argv input statement by statement, requires exactly one call, and
  refuses any other caller in the package; its first cut checked only the field's name, and
  review showed a dropped assignment passing it.*
- **Rootless, on a real host or in CI; a nested jail cannot show this**, since it forces
  `--userns=host`. The report includes `podman info --format '{{.Host.Security.Rootless}}'` =
  `true`, a fresh launch boots, `stat -c '%u:%g %a'` of `/home/agent` and of a mountpoint match
  today's, and a `writable_home_dirs` mount works. *Met in CI, 2026-09-25: `ci.yml` run
  36167524940 at `e56d871e`, on `ubuntu-latest` and `ubuntu-24.04-arm`, whose jobs report
  `podman rootless=true`. All three subtests passed on both. The skeleton is mounted `ro`, and a
  write to the home root fails with EROFS. `/home/agent` and `/home/agent/.config` stat
  `0:0 755`, the jail user's own, because jail root maps to the host user who built the skeleton.
  The `writable_home_dirs` subtest wrote through its rw bind nested in the `:ro` skeleton.*
- **Step 4:** a new Apple Container workspace boots logged in from the seed, and its home has
  no undotted `~/claude` or `~/npm-global`. *Met on the host side only:
  `TestAppleContainerSeedReachesTheJailsClaudeJSON`, `TestAppleContainerLoginIsLearnedByTheSeed`,
  `TestPrepareWsStateLaysOutEachBackendsOwnPaths`, `TestLegacyMigrationsLandInEachBackendsLayout`,
  `TestTheSeedDoesNotWriteThroughAJailPlantedLink`, and `runContainer`'s `rt` argument is pinned
  by `TestRunContainerHandsTheSelectionToTheReservationReaders`. The boot is not run; it needs a
  Mac.*
- **Step 4a:** `writable_home_dirs: [".codex"]` passes in a claude-only workspace and is
  refused once codex is selected, naming codex; a `host_files` entry under `~/.codex/` works
  in a claude-only jail. *Met: `TestWritableHomeDirCodexIsLegalUntilCodexIsSelected` (through
  `ValidateConfig`), `TestAConfiguredPacksDirIsReserved`, `TestStagingForKeysOnTheSelectedPacks`,
  `TestAHostFilesEntryUnderAnUnselectedPacksDirValidates`, and
  `TestAHostFilesEntryUnderAnUnselectedPacksDirIsStaged` (the backing dir, the skeleton
  mountpoint and the bind, for claude-only and for codex selected);
  `TestRunContainerHandsTheSelectionToTheReservationReaders` pins `runContainer`'s arguments,
  and `TestTheSkeletonAndTheBindSourcesDeriveWritableHomeDirsFromTheSelection` the skeleton's
  and the bind sources' own. The review fixes: `TestAWritableHomeDirCoversAHostFilesEntryUnderIt`,
  `TestAHostFilesEntryUnderASelectedPacksSharedDirIsNotStaged`,
  `TestAppleContainerStagesNoHostFilesWritableDir`, `TestAFilteredOutManifestReservesNothing`.
  Each was run against a private copy with the old behavior or its wiring restored, and
  failed. Not run in a container.*
- **Step 5:** `jail-home.md`, `storage-and-config.md` and `agent-credentials.md` describe the
  skeleton, the machine store, reservation and the seed; the code comments of [§7](#7-what-died)
  are re-pointed. *Met; the bare `§` numbers in `internal/basehome` are re-pointed at the
  pre-rewrite text (`git show 030c8f52:docs/design/base-home-legacy-state.md`) by that
  package's header rather than one by one.*
- **Review follow-ups, 2026-09-25**, each with the test that pins it. *Met at the unit level;
  none needs a container except the first, whose container half is the reaper's ordinary
  work.*
  - A jail's tracking file goes once its container is known gone, so the reaper reaches its
    skeletons ([§2.2](#22-where-it-lives-host-only-never-in-wsstate)):
    `TestANormalExitForgetsAGoneContainersTracking` drives the real teardown and then the real
    reaper; `TestTrackingStaysUnlessTheContainerIsKnownGone` covers every answer short of
    "gone"; `TestEveryEndOfTheLaunchForgetsTheContainer` pins the signal arm and the
    runtime-never-started branch.
  - A launch that starts no container leaves no skeleton ([§2.3](#23-when-it-is-built)):
    `TestAFatalSkeletonFailureLeavesNoDirectory`, `TestDiscardUnheldSkeletonRemovesOnlyASkeleton`,
    `TestNoReturnAfterTheSkeletonLeaksIt`.
  - The builder's best-effort warnings reach the launch output: `TestTheSkeletonsWarningsArePrinted`.
  - A CONFIGURED pack's machine-scope shared dir gets its bind source in the machine store on
    both container backends; `storage.EnsureGlobalStorage` creates only the shipped packs'
    ones, before config loads (`ensureSharedDirSources`, `internal/cli/run/shareddirsources.go`):
    `TestAConfiguredPacksSharedDirGetsABindSource`, `TestRunContainerCreatesTheSharedDirSources`.
  - Validation writes nothing into the pack store: `resolveSelectedPacks` resolves with
    `packsrc.Store.ResolveExisting`, not the launch's `Resolve`, which checks a missing tree out
    and removes an incomplete one first (`TestValidationLeavesThePackStoreAlone`).
  - Dead code deleted: `storage.EnsureSymlink` and `packload.EmbeddedWritableDirs`, which had no
    production caller, and `internal/config/zz_probe_test.go`, which asserted nothing.

  Each wiring test was run against a private copy of the tree with its call site removed, and
  failed.

## 9. Open Questions

1. ✅ **OQ-BH9: Where does the skeleton live?** Decides who can reach it and what reaps it.
   Options: (a) under `paths.AgentsDir()/<cname>/`; (b) a new `<state>/skeletons/<cname>` with
   its own reaper. `wsState` is ruled out ([§2.2](#22-where-it-lives-host-only-never-in-wsstate)).

   <!-- vantage: oq id=OQ-BH9 leaning="Under paths.AgentsDir()/<cname>/: host-only, keyed per jail, already reaped by PruneOrphanAgentStaging's liveness check. Never wsState, which the jail can write." -->

   _Leaning:_ (a). It is already reaped by the liveness-gated `PruneOrphanAgentStaging`; a
   second reaper is one more liveness rule to get right.

   **Answer:**
   > (a), decided by the build. The maintainer delegated it: *"This is an implementation
   > detail that I don't care about."*

2. ✅ <a id="OQ-BH10"></a>**OQ-BH10: Edit one skeleton in place, add only, or a new one per launch?** Decides
   whether a dropped mountpoint or `host_files` link ever goes away, and whether a launch can
   detach a live jail's bind. Options: (a) reconcile in place, removing only when a new
   tri-state probe says no `cname` container exists and the flock was acquired
   ([§2.4](#24-the-three-rules-the-shared-base-obeys-by-accident)); (b) add only, forever;
   (c) a new directory per fresh launch under the [OQ-BH9](#OQ-BH9) root, never modified after,
   old ones reaped with the jail's `AgentsDir` entry by the existing reaper.

   <!-- vantage: oq id=OQ-BH10 leaning="A new skeleton directory per fresh launch, never modified afterwards; old ones go with the jail's AgentsDir entry through PruneOrphanAgentStaging, which already declines when liveness is unknown. Reconciling in place needs two unbuilt signals, a tri-state liveness probe and a lock-acquired flag." -->

   _Leaning:_ (c). No launch ever removes a mountpoint, so its safety needs no liveness answer,
   and the reaper already declines when liveness is unknown. The cost is a few 16K directories
   per workspace between reaps. (a) needs two signals the launch path lacks; (b) brings the
   leftover pile back within one workspace.

   **Answer:**
   > (c). The maintainer ruled the leaning as written: *"A new skeleton directory per
   > fresh launch, never modified afterwards; old ones go with the jail's AgentsDir entry through
   > PruneOrphanAgentStaging, which already declines when liveness is unknown."*

3. ✅ **OQ-BH12: How is the Apple Container seed fixed?** Every new Apple Container workspace
   starts without the login seed ([§3](#3-the-apple-container-seed-defect)). Options: (a)
   runtime-aware paths in `prepareWsState`; (b) bind that backend's pack dirs the podman way,
   adding mounts where mount count is the constraint.

   <!-- vantage: oq id=OQ-BH12 leaning="Make prepareWsState's seed paths runtime-aware: on rt=container sync wsState/.claude.json and create dotted dirs. Moving Apple Container onto the dot-stripped layout means more mounts on the backend limited by mount count. Needs a Mac to verify." -->

   _Leaning:_ (a), verified on a Mac, landing separately from the podman change.

   **Answer:**
   > (a), decided by the build and verified on a Mac, landing separately from the podman
   > change. The maintainer delegated it: *"Also an implementation detail for you."*

4. ✅ **OQ-BH13: What happens to the launch refusal and `yolo check`'s report?** The refusal
   (`noteLegacyBaseHome`, `internal/cli/run/basehomedisclosure.go`, hatch
   `YOLO_ALLOW_LEGACY_BASE_HOME`) shipped 2026-09-21 on reasons this design removes, printing an
   `mv` rather than offering a verb ([DIR-BH0](#10-decision-ledger)). Options: (a) delete the
   refusal, keep the check report and its printed `mv`; (b) delete both, with
   `internal/basehome`; (c) keep the refusal as a one-line launch disclosure.

   <!-- vantage: oq id=OQ-BH13 leaning="Delete the launch refusal and its hatch in the same change that stops the mount and deletes seedAgentDir; nothing is left to refuse. Keep yolo check's detection-only report, reworded: the bytes are unmounted and unread, and the printed mv (no verb, per the 2026-09-21 call) is optional cleanup." -->

   _Leaning:_ (a), in the same change as the skeleton; the refusal stays until then. A hatch is
   for a user's broken config, never for a yolo bug, and once the mount is gone it guards
   nothing. The report costs nothing, and the bytes are the user's to find.

   **Answer:**
   > (a). The maintainer ruled the leaning as written: delete the refusal and its hatch
   > in the same change that stops the mount and deletes `seedAgentDir`; keep `yolo check`'s
   > detection-only report, reworded, with the printed `mv` as optional cleanup.

5. ✅ **OQ-BH14: Does name reservation stay over every shipped pack?** Today a `host_files` or
   `writable_home_dirs` entry may not claim any shipped pack's directory, selected or not
   ([§2.8](#28-reservation-is-a-rule-about-config-names-not-about-directories)), which is an
   unselected pack's effect under [DIR-BH1](#10-decision-ledger). Options: (a) keep it, as
   DIR-BH1's one named exception; (b) narrow it to the selected packs, validated each launch.

   <!-- vantage: oq id=OQ-BH14 leaning="Keep reservation over every shipped pack as DIR-BH1's one named exception: its effect is a refused config key, never anything in a jail. Narrowing moves the refusal to the day the pack is selected, possibly in another workspace, and makes one user-scope entry valid in some workspaces and refused in others." -->

   _Leaning:_ (a). Its effect is a refused config key, never anything inside a jail. Narrowing
   works, since validation knows the selection, but the refusal would then arrive the day the
   pack is selected, possibly in another workspace, and a user-scope entry would be valid in
   some workspaces and refused in others.

   **Answer:**
   > (b), reserve only the selected packs' directories. The maintainer: *"it has to be
   > like the other packs don't exist … packs could come from anywhere and be added, removed,
   > whatever."* Consequences in [§2.8](#28-reservation-is-a-rule-about-config-names-not-about-directories).

6. 💬 **OQ-BH15: Does `host_files`' surface-path reservation cover only the selected packs
   too?** Found while building [OQ-BH14](#OQ-BH14), which ruled on directories. `host_files`
   still refuses a destination that ANY shipped pack composes as a surface
   (`builtinSurfacePaths`, `internal/config/hostfiles.go`), so `~/.codex/config.toml` is refused
   in a claude-only workspace, where nothing composes it. That is an unselected pack's effect,
   which [DIR-BH1](#10-decision-ledger) rules out. Options: (a) narrow it to the selected packs,
   with validation resolving the selection as `writable_home_dirs` now does
   (`resolveSelectedPacks`); (b) keep it as DIR-BH1's one named exception.

   <!-- vantage: oq id=OQ-BH15 leaning="Narrow it to the selected packs. The two-writers case it guards is still refused at launch for every selected pack, embedded or configured, by config.SurfaceCollisions over the loaded packs, so narrowing loses no refusal a jail could hit; and OQ-BH14's reason (other packs are treated as if they do not exist) applies to a file as much as to a directory." -->

   _Leaning:_ (a). The collision it guards against, two writers for one file, is still refused
   at launch for every selected pack, embedded or configured, by `config.SurfaceCollisions` over
   the loaded packs. So narrowing loses no refusal a jail could hit, and
   [OQ-BH14](#OQ-BH14)'s reason (the other packs are treated as if they do not exist) applies to
   a file as much as to a directory. Against it: the list is read inside `checkHostFiles`,
   which the loader, validation and `SourceLessHostFilesFrom` all call, so each of them would
   need the selection.

7. 💬 **OQ-BH16: Does a launch remove its own skeleton when its jail ends?** Found while
   making [OQ-BH10](#OQ-BH10)'s reaper reach skeletons at all
   ([§2.2](#22-where-it-lives-host-only-never-in-wsstate)). With the tracking file gone at exit,
   `PruneOrphanAgentStaging` reaps a workspace's whole `AGENTS_DIR/<cname>` once no container of
   that name is live and the entry is an hour old. It runs from `yolo prune` and from a host
   launch's housekeeping slot (debounced, `reapSmallAutomaticClasses`), but the slot always
   keeps the launching jail's OWN name. So on a machine
   where one workspace is used alone, that workspace's skeletons, one per fresh launch, go only
   when someone runs `yolo prune --apply`. With two or more workspaces, any other workspace's
   launch reaps the idle one. Options: (a) accept that, as within the ruling's "a few 16K
   directories per workspace between reaps"; (b) at each of the three ends that now remove the
   tracking file, on the same evidence (the runtime answered that no container of that name
   exists, and the workspace lock was free), also remove the skeleton this launch built, which
   is the only launch that knows its name; (c) have the launch's housekeeping remove its own
   name's older skeletons, keeping the one its running container is bound from.

   <!-- vantage: oq id=OQ-BH16 leaning="(b): remove the launch's own skeleton at exit, on the same known-gone evidence that now removes the tracking file. It removes only a directory no other launch knows, only after the one container that could hold it is gone, and leaves the reaper only launches that died without a teardown (SIGKILL, OOM)." -->

   _Leaning:_ (b). It removes only a directory no other launch knows the name of, and only after
   the one container that could hold it is known gone, so it leaves the reaper just the launches
   that died without any teardown (SIGKILL, OOM). Against it: it is a launch removing a
   skeleton, which [OQ-BH10](#OQ-BH10)'s answer ("never modified afterwards") did not provide for, and it
   rests on the tri-state probe that answer was chosen to avoid needing. (c) needs to know which
   skeleton the live container is bound from, one more liveness question. Not built: the build
   did what the ruling says and stopped there.

## 10. Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| R1 | **MOVE** legacy runtime artifacts to an archive, never delete. *Still binds* anything that touches legacy bytes; this design touches none | 2026-09-20 | [§2.9](#29-backends) | — (nothing to move) |
| R2 | **Move over delete**; any delete path is explicit opt-in. *Still binds*, the same way | 2026-09-20 | [§2.9](#29-backends) | — |
| R3 | **Fail closed**: no TTY or unconfirmed safety ⇒ do nothing, leave the marker unstamped, retry later. *Met trivially*: no migration, no marker | 2026-09-20 | [§7](#7-what-died) | — |
| R4 | **Generic**: every pack state dir, every backend (podman, `container`, macos-user). *Still binds*: [§2.9](#29-backends) covers all three | 2026-09-20 | [§2.9](#29-backends) | — |
| DIR-BH0 | **The maintainer's call:** no `yolo base-home --archive` verb; the user runs a printed `mv`. The verb was built and deleted the same day (commit `1f440af3`; the comment recording why, "Why a refusal and not a verb", is in `git show 1f440af3:internal/cli/run/basehomedisclosure.go`, a file step 3 deleted with the refusal) | 2026-09-21 | [OQ-BH13](#OQ-BH13) | yes |
| DIR-BH1 | **Principle, the maintainer's:** *"a non-selected pack can never have an impact."* [OQ-BH14](#OQ-BH14) narrowed name reservation to the selected packs; one shipped-set rule remains, `host_files`' surface-path reservation, open as [OQ-BH15](#OQ-BH15) | 2026-09-25 | [§1](#1-the-question-and-the-answer) | yes, 2026-09-25: the skeleton, including core's dirs (`paths.HomeSkeletonCoreDirs` drops the pi pack's `.pi/agent`), and name reservation ([OQ-BH14](#OQ-BH14)). One shipped-set rule is left in config validation and was not ruled on: `host_files`' surface-path reservation ([§2.8](#28-reservation-is-a-rule-about-config-names-not-about-directories)) |
| DIR-BH2 | **Direction, the maintainer's:** *"So basically this directory seems like it's always empty and there's some complication with sharing across jails because then they could have different packs. Why is this not just per jail? It seems like it's nothing."* After the investigation: *"yes rewrite"*. What he directed: per jail, not shared, the doc rewritten around that. **This design's reading, not his words:** a `:ro` skeleton from the selected packs, `<state>/home` kept as the machine store, alternative B rejected ([§5](#5-alternatives-with-verdicts)) | 2026-09-25 | [§2](#2-the-design-a-per-jail-skeleton) | yes, podman, 2026-09-25 |
| OQ-BH1 | **Superseded by DIR-BH2.** Archive location and retention: no archive exists; legacy bytes stay in place, unmounted | 2026-09-25 | [§7](#7-what-died) | — |
| OQ-BH2 | **Superseded by DIR-BH2: no migration exists.** The same reason closes [OQ-BH3](#10-decision-ledger) (automatic move or confirmation), [OQ-BH6](#10-decision-ledger) (shadow layer, which a per-jail skeleton carries all the way, [§5](#5-alternatives-with-verdicts) A) and [OQ-BH8](#10-decision-ledger) (partial failure of a move). This row was trigger and marker | 2026-09-25 | [§7](#7-what-died) | — |
| OQ-BH4 | **Closed by this design, not by a ruling.** The launch path no longer classifies; the one remaining allowlist is `claudeJSONSeedKeys`, core-owned as today. The code comment citing this id (`internal/paths/basehomecore.go`) points here | 2026-09-25 | [§2.7](#27-the-seed) | — |
| OQ-BH5 | **Closed by this design, not by a ruling, split.** The seed half: delete `seedAgentDir` rather than allowlist it, its inputs since April being legacy-only (INFERRED). The posture half is now [OQ-BH13](#OQ-BH13) | 2026-09-25 | [§2.7](#27-the-seed) | yes, 2026-09-25 (`seedAgentDir` deleted) |
| OQ-BH7 | **Superseded by DIR-BH2 for the container base**: no migration, so nothing to reuse [`OQ-HT2`](../reference/macos-user-home-tiers.md#oq-ht2)'s discard for; macos-user never mounts `<state>/home` or reaches `prepareWsState`. **Its macos-user half is dropped from this doc**: reopening [`OQ-HT2`](../reference/macos-user-home-tiers.md#oq-ht2) for an account used for real work belongs to [`../reference/macos-user-home-tiers.md`](../reference/macos-user-home-tiers.md) | 2026-09-25 | [§4](#4-what-this-does-not-cover) | — |
| OQ-BH9 | **Delegated to the build:** the skeleton lives under `paths.AgentsDir()/<cname>/`, reaped by `PruneOrphanAgentStaging` | 2026-09-25 | [§2.2](#22-where-it-lives-host-only-never-in-wsstate) | yes, 2026-09-25 (`paths.HomeSkeletonRoot`) |
| OQ-BH10 | **The maintainer's ruling:** a new skeleton per fresh launch, never modified afterwards | 2026-09-25 | [§2.4](#24-the-three-rules-the-shared-base-obeys-by-accident) | yes, 2026-09-25 (`buildHomeSkeleton`); the reaping half became true the same day, when a launch started removing a gone jail's tracking file (`forgetGoneContainer`, [§2.2](#22-where-it-lives-host-only-never-in-wsstate)). What the reaper still cannot reach is [OQ-BH16](#OQ-BH16) |
| OQ-BH12 | **Delegated to the build:** runtime-aware seed paths in `prepareWsState`, verified on a Mac, landing separately | 2026-09-25 | [§3](#3-the-apple-container-seed-defect) | host side, 2026-09-25 (`claudeJSONInWsState`, `preparePodmanBindSources`); the Mac run is owed |
| OQ-BH13 | **The maintainer's ruling:** delete the launch refusal and `YOLO_ALLOW_LEGACY_BASE_HOME` with the skeleton; keep `yolo check`'s report, reworded | 2026-09-25 | [§8](#8-build-order-and-done-conditions) | yes, 2026-09-25 |
| OQ-BH14 | **The maintainer's ruling:** reserve only the selected packs' directories; an unselected pack is treated as nonexistent | 2026-09-25 | [§2.8](#28-reservation-is-a-rule-about-config-names-not-about-directories) | yes, 2026-09-25 (`resolveSelectedPacks`, `reservedHomeSegments`, `StagingFor`; AGENTS.md's bullet rewritten) |

## Appendix A: Evidence

From a five-agent investigation on 2026-09-25, one of them a skeptic; load-bearing claims were
re-read against HEAD for this rewrite.

- **MEASURED**: a fresh nested base holds 20 dirs, 13 files or symlinks and **0 non-empty
  files**; the older nested base's only non-empty file is `.claude/claude.json` (keys
  `oauthAccount`, `hasCompletedOnboarding`); on the maintainer's host `.copilot`, `.oh-omp`,
  `.pi-lens`, `.yolo-shims` and `.yolo-launchers` are empty. The nested base is 67 entries, 16K.
- **MEASURED**, leftovers: `.pi-lens` in the host base, which this workspace does not declare;
  `.foo`, `.keeper`, `.dropped`, `.filespack` and `yolo-it-newdir` in the nested base.
- **MEASURED**: in this jail `~/.claude-shared-credentials/.credentials.json` is 555 bytes, mode
  `0600`, bound from `<state>/home/.claude-shared-credentials`, a subdirectory of the base bound
  at `/home/agent`. The claude-oauth-broker README says it holds the jail identity's refresh
  token. In a nested claude-only jail, the unselected shared dirs were visible (empty there).
- **MEASURED**: an `oauthAccount` planted in workspace 1 reached
  `<state>/home/.claude/claude.json`, and from there workspace 2's `~/.claude.json`.
- **MEASURED** (re-measured for this rewrite): `touch /home/agent/.x` gives "Read-only file
  system"; mountinfo shows `/home/agent` bound `ro` from the host's
  `.local/share/yolo-jail/home`; `/workspace/.yolo/home` is writable from the jail.
- **MEASURED**: in a user+mount namespace made with `unshare`, a host-side `rmdir` of a
  mountpoint succeeded and detached the bind inside. 200 fork-per-call `mkdir`/`touch` calls
  took 0.2 s, so skeleton cost is no argument for sharing.
- **READ**, why the launch cannot prove a container absent: `findExistingContainer` and
  `findRunningContainer` (`internal/cli/run/lifecycle.go`) return `""` when the probe did not
  run. In `run.go`, when `removeStaleContainer` fails and the 5-second `waitForRunningContainer`
  returns `""`, the launch continues to `prepareWsState` with that container alive.
  `acquireWorkspaceLock` (`flock.go`) warns on a failed blocking flock and returns the same
  `*workspaceLock` as on success.
- **READ**: `git show 7deeb762` (a shared writable home, *"Global home as base (has auth,
  tools, configs)"*) and `2bcc4e76` (made `:ro`, `seedAgentDir` introduced).
