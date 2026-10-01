---
title: "Why an in-jail nix build is never a GC root, and the one string that fixes it"
date: 2026-09-28
status: in-review
tags: [nix, gc-roots, podman, mount-namespaces, storage, design]
summary: "A podman jail builds through the host nix daemon, and every root it asks for is recorded under the jail's own spelling of the out-link path. The host daemon resolves that spelling on the HOST filesystem, where it does not exist, so the root is deleted as stale at the next GC or root query and the build is unprotected. Measured end to end from an untrusted jail: an indirect root registered under the HOST spelling of the same link is honored, and dies when the link does, exactly as host nix behaves. The design question is what registers that string: an explicit verb, a nix wrapper, or a watcher on a read-only view of the host's gcroots/auto."
stage: DESIGN
next: "Rule OQ-NR1, what triggers a translated root for a link a user or an agent makes, and OQ-NR2, the read-only bind of the host's gcroots/auto that OQ-NR1's leaning needs. The map and the one-operation daemon client that trigger would use are built (§8)"
vantage:
  status-chip: true
---

# Why an in-jail nix build is never a GC root, and the one string that fixes it

**Status:** 2026-10-01; questions triaged 2026-09-30. yolo's own in-jail roots are translated
roots, [NR-D2](#NR-D2)'s consumer, which needed only [§4](#4-the-translated-root): a launch that
mounts the host nix daemon states the map, and an in-jail `yolo` registers its image, prefix and
store-delivered profile roots under the host's spelling ([§8](#8-what-is-built)). A link a user
or an agent makes is still not a root, and the briefing still says so. That needs a trigger,
which is [OQ-NR1](#OQ-NR1)'s. Two questions are open, [OQ-NR1](#OQ-NR1) and [OQ-NR2](#OQ-NR2).
[OQ-NR3](#OQ-NR3) and [OQ-NR4](#OQ-NR4) were decided as implementation choices
([NR-D1](#NR-D1), [NR-D2](#NR-D2)), and both are built. MEASURED: every mechanism claim in
[§3](#3-measured-in-this-jail), against the maintainer's host daemon (Nix 2.35.2) from an
untrusted jail client (Nix 2.34.8). MEASURED 2026-10-01 against the same daemon: a nested launch
handed a map registered its image and prefix roots, the host's `nix-store --query --roots` listed
both under the host's spelling, and both died with their links ([§8](#8-what-is-built)).
UNMEASURED: a jail whose own host launcher states the map, because this jail's launcher predates
it and every measurement handed the map over by hand; and the root watcher of
[§5](#5-what-registers-the-translated-root) on a real host, because the host's `gcroots/auto`
cannot be mounted into this jail.

> **In short.** The host daemon records whatever path string the client sends for an indirect root
> and later resolves it on the host. So a jail is protected if it sends the **host's** spelling of
> its own link, and it can already do that: no trust, no new mount, and the root's lifetime is the
> link's, the same as host nix.

**Why it matters.** A `result` link, a `nix profile` install, a `nix develop --profile` or a
nix-direnv cache made in a jail is not protected from the host's garbage collector. A host
`nix-collect-garbage`, or the daemon's own `min-free` auto-GC, can delete it between two commands
of a running session. The host's `gcroots/auto` held more than a hundred dead links from jails and test runs
when this was measured, and every one was a root someone asked for and did not get.

**The shape.** A *translated root* *(coined here)*: an indirect GC root registered under the host
path of a link the jail created, over the existing daemon socket. A launcher-written map turns jail
paths into host paths. [OQ-NR1](#OQ-NR1) decides what triggers the registration.

**Cost.** One daemon-protocol operation implemented in Go, and one map the launcher already has the
facts for. The watcher option adds a read-only mount of the host's `gcroots/auto` and a daemon
inside the jail.

**Start at [§2](#2-the-mechanism-why-the-root-is-lost).** Everything else follows once it is clear
which process resolves the path, and on which filesystem.

**Needs your ruling:** [OQ-NR1](#OQ-NR1), [OQ-NR2](#OQ-NR2). [OQ-NR3](#OQ-NR3) and
[OQ-NR4](#OQ-NR4) were decided as implementation choices ([NR-D1](#NR-D1), [NR-D2](#NR-D2)).

**Reads with:** [`workspace-path-mirroring.md`](workspace-path-mirroring.md) (the ruled-against way
to make paths agree; this fix does not need it),
[`../reference/nix-across-backends.md`](../reference/nix-across-backends.md) (the host-side roots
yolo already holds), [`../plans/setup-support-gaps.md`](../plans/setup-support-gaps.md#2-ranked-gap-backlog)
(G21, the open "who reaps an in-jail build's output" row this answers).

---

## 1. Verdict

Build the translated root, and trigger it with a watcher on a read-only view of the host's
`gcroots/auto` ([§5](#5-what-registers-the-translated-root), option C). My reasons, in order:

1. **It is exactly host semantics.** The root lives as long as the link exists on disk and dies when
   the link is deleted. It survives the jail stopping, because the link lives in the workspace or
   the home overlay, both of which are host directories.
2. **It cannot break a nix command.** Nothing sits in nix's path: no wrapper, no proxy, no changed
   `NIX_REMOTE`. If the watcher is down, the jail is exactly as it is today.
3. **It covers every tool at once.** `nix build`, `nix-build`, `nix profile`, `nix develop --profile`,
   nix-direnv and the flake-registry cache all create their roots through the same daemon call
   ([§2.2](#22-what-the-client-sends)), and the watcher sees that call's result, not the tool's argv.
4. **It adds no capability.** An untrusted jail can already register an indirect root at any host
   path it likes ([§3](#3-measured-in-this-jail), M6). The fix only makes it register the right one.

What I did **not** build, and why: every trigger is new product surface (a verb, a wrapper, or a
mount plus a daemon), and the mount exposes a host directory listing. That is a ruling, not an
implementation detail.

## 2. The mechanism: why the root is lost

### 2.1 Who resolves the path, and where

An indirect root is two symlinks: `/nix/var/nix/gcroots/auto/<hash>`, pointing at the out-link,
which points at the store path. At GC time `LocalStore::findRoots` walks `gcroots/`, and for each
link under `auto/` it `readLink`s the out-link path and then **lstats it** (`pathExists` is `lstat`).
If the path is absent it logs `removing stale link from … to …` and unlinks the auto entry. If it
is present, the out-link must be a symlink whose single target is inside the store.

- **Source:** [`src/libstore/gc.cc` `findRoots`](https://github.com/NixOS/nix/blob/c621c2b3727700e439d4c3e5bff3ce5b35a24851/src/libstore/gc.cc#L231-L305),
  stale deletion at L263–268. It is the same in 2.18.9, 2.24, 2.28, Lix and Determinate Nix.
- **The namespace is always the host's.** The process doing this is either root's
  `nix-collect-garbage` opening the local store directly, or a daemon worker, for a daemon-routed
  GC or the `min-free` auto-GC. The upstream `nix-daemon.service` sets no mount-namespace options.
- **A root query deletes too.** The daemon's `FindRoots` op runs the same walk, so even
  `nix-store --query --roots` from a jail prunes stale auto links ([§3](#3-measured-in-this-jail), M1).
- **Two hops, exactly.** A third symlink, or a relative second target, roots nothing.

So a jail's `/workspace/result` becomes `gcroots/auto/<sha1("/workspace/result")> → /workspace/result`,
and on the host `/workspace` does not exist. The root is deleted at the next walk. The auto entry is
named by the path string alone, so every jail's `/workspace/result` shares one auto entry. That
collision is harmless, because the entry is dead either way.

### 2.2 What the client sends

`nix build --out-link X` and `nix-store --add-root X` call `IndirectRootStore::addPermRoot`, which
for a daemon client runs **on the client**:

1. send `AddTempRoot(storePath)`;
2. create the symlink `X` locally;
3. send `AddIndirectRoot(absPath(X))`.

The daemon's handler takes that string verbatim and creates the auto entry. It has **no trust
check**.

- **Sources:** [`indirect-root-store.cc`](https://github.com/NixOS/nix/blob/c621c2b3727700e439d4c3e5bff3ce5b35a24851/src/libstore/indirect-root-store.cc#L20-L45)
  and [`daemon.cc` `AddIndirectRoot`](https://github.com/NixOS/nix/blob/c621c2b3727700e439d4c3e5bff3ce5b35a24851/src/libstore/daemon.cc#L745-L756).
- **`absPath` does not resolve symlinks.** A path reached through a symlink is sent as spelled
  ([§3](#3-measured-in-this-jail), M7).
- **Every root-making tool goes through this call.** `nix profile` and `nix develop --profile`
  create each generation link with `addPermRoot` (`profiles.cc` `createGeneration`), and nix-direnv
  roots its `.direnv/flake-profile-*` with `nix build --out-link`.
- **Non-root profiles have no other root.** `~/.local/state/nix/profiles` is rooted only through
  auto links. Root's profiles under `/nix/var/nix/profiles` are scanned directly, and a jail has
  none.

`AddPermRoot` (op 47, protocol 1.36, Nix 2.20) would create the link on the daemon's side, but it
**requires a trusted client**, and no CLI sends it over the unix socket. Upstream has no supported
way for an untrusted remote client to make a permanent root: [NixOS/nix#11812](https://github.com/NixOS/nix/issues/11812)
and [#7138](https://github.com/NixOS/nix/issues/7138) are open. A 2023 comment on #7138 describes
this exact case, docker containers sharing the host socket. The per-user gcroots directory that
once served it was removed in Nix 2.14 ([#5226](https://github.com/NixOS/nix/pull/5226)).

### 2.3 What already protects a jail

Two things protect a jail today, and both are tied to a process being alive:

- **Runtime roots.** The GC scans every `/proc/<pid>`: its `exe`, `cwd`, `fd/*`, `maps` and
  `environ`. The host `/proc` includes the jail's processes. So a running `nix shell` or
  `nix develop`, whose `PATH` names its store paths, is protected while it runs (M2, M3). On Linux
  there is no setting that turns this off. It does stop working under the unprivileged-daemon mode
  (`use-roots-daemon`, Nix 2.34), which cannot read other processes' `/proc`.
- **Temp roots.** These last exactly as long as the client's daemon connection
  (`temproots/<worker-pid>`, liveness by file lock). `nix build` holds its temp roots only for its
  own run (M4).

So the gap is precisely **between commands**: a `result` link, a profile or a devshell cache that no
live process is using right now.

> [!WARNING]
> **A Nix 2.36 client will stop sending temp roots to a 2.35 daemon.** [PR #16113](https://github.com/NixOS/nix/pull/16113)
> (merged 2026-09-17, unreleased) sends client temp roots only when the daemon advertises
> `addTempRoots`, with no fallback. The jail's nix comes from the image and the daemon from the
> host, so an image bump can open a window *during* a build that nothing here would notice. That
> matters to whoever bumps the image's nix, and it is independent of this design.

## 3. Measured in this jail

All of these were run 2026-09-28 from a podman jail on Linux: host daemon Nix 2.35.2, protocol
1.38, jail client Nix 2.34.8, `nix store info` reporting `Trusted: 0`. The probe was a fresh
`builtins.toFile` store path that nothing else references. "Dead" means it appeared in
`nix-store --gc --print-dead`, which is a dry run that deletes nothing.

| # | What the jail did | Host's verdict |
|---|---|---|
| M1 | `nix-store --add-root /tmp/…/link -r P` (a jail-only path) | **dead**; the next root query logged `removing stale link … to "/tmp/…/link"` |
| M2 | held `P` open on an fd in a jail process | **alive**, listed as a `{censored}` runtime root |
| M3 | a jail process with `P` only in an environment variable | **alive** (runtime root through `environ`) |
| M4 | `AddTempRoot(P)` over a connection held open by a 34-line Python client | **alive** while connected, **dead** once closed |
| M5 | `nix build --out-link` at a path bound at the **host's spelling** (a nested user and mount namespace binding `/workspace` at `/home/matt/code/yolo-jail`) | **alive**, listed uncensored under the host path |
| M6 | a symlink at `/workspace/.yolo/x`, then `AddIndirectRoot("$YOLO_HOST_DIR/.yolo/x")` from the same Python client | **alive**; after `rm` of the link, **dead** again |
| M7 | `nix-store --add-root /tmp/lnk/p` where `/tmp/lnk → /workspace/.yolo` | the daemon recorded `/tmp/lnk/p` verbatim: the client does not resolve symlinks |
| M8 | nested `podman run --read-only -v …:/home/matt/.local/share/…/x` | the mountpoint was created on the read-only root fs |

**M6 is the design.** It needs no mount, no trust and no change to how nix is invoked. `YOLO_HOST_DIR`
already tells the jail the workspace's host path. M5 proves the same property the mount-based way.

The first root query also deleted more than a hundred stale auto links. They pointed into jail homes, test
temp dirs, `/workspace/.claude/worktrees/…`, and `/home/agent/.cache/nix/flake-registry.json`, which
nix roots on every flake evaluation. They protected nothing, so deleting them cost nothing. They show
the scale of the problem: every one was a root the jail asked for and never had.

## 4. The translated root

A **translated root** *(coined here)* is an `AddIndirectRoot` sent with the host's absolute
spelling of a link the jail created. It is **not** a new kind of nix root. It is the ordinary
indirect root, carrying the string the host can resolve.

**The map.** A jail path translates only if it lies under a bind mount whose host source the
launcher knows. The launcher states that set; the jail does not derive it (the `root` field of
`mountinfo` is relative to the filesystem, a btrfs subvolume here, and is not a host path). Behavior:

- The **workspace** maps `/workspace/…` to `YOLO_HOST_DIR/…`, a variable the jail already has.
- The **home overlay binds** map `/home/agent/.local`, `.config`, `.cache` and the rest to their
  sources under `<workspace>/.yolo/home/` and the machine state dir.
- **A path under no mapped bind is not translated.** That covers the container root fs, the
  anonymous `/tmp` and `/var/tmp` volumes ([OQ-NR3](#OQ-NR3), decided as [NR-D1](#NR-D1)), and
  `/nix/store` itself. The root
  stays exactly as dead as it is today, and nothing is reported.
- **Longest destination prefix wins**, because the home binds nest (`/home/agent/.claude/skills`
  inside `/home/agent/.claude`).
- **A nested launch composes.** It translates its own sources through its parent's map before
  writing its own. A source the parent cannot translate is dropped, with the same effect as above.

**What is registered.** A translated root is sent only when the jail-side path is a symlink whose
target is a literal `/nix/store/…` path. Anything else would be two hops or no root at all
([§2.1](#21-who-resolves-the-path-and-where)). The registration is idempotent: the auto entry is
named by the path's hash, so sending twice creates one entry.

**Failure.** Every failure is silent to the nix command and degrades to today's behavior. That
covers an unreachable socket, a protocol error and an untranslatable path. The daemon rejecting
the operation is the one case worth a line in the launch or boot log, because it would mean a host
nix changed the rule in [§2.2](#22-what-the-client-sends).

**Lifetime and reaping.** Nothing in yolo reaps translated roots. The root is the link: deleting the
`result` frees the closure at the next GC, as on the host. A jail that leaves links behind pins host
disk the way a host user's forgotten `result` does. Today's jail cannot do that, so it is a real, if
small, change in who can hold host disk.

**The protocol client.** yolo speaks one operation: the handshake, then `AddIndirectRoot`. It
advertises an old client version (1.37 in the measurement) so it never negotiates the 1.38 feature
set. The daemon keeps backward compatibility with old clients as a matter of course; this is the
same op the nix CLI has used since `IndirectRootStore` landed in 2.17.

## 5. What registers the translated root

| | Trigger | Covers | Can it break nix? | New surface |
|---|---|---|---|---|
| **A** | An explicit verb (`yolo nix keep <link>…`), taught by the briefing | whatever an agent remembers to keep | no | one verb |
| **B** | A `nix`/`nix-build` wrapper that registers `result*` and `--out-link` after a successful run | `nix build` and `nix-build`; not `nix profile`, nix-direnv or `--profile` without per-subcommand parsing | yes: argv parsing, exit codes and signals all pass through it | a wrapper class on PATH |
| **C** | A **root watcher** *(coined here)*: an in-jail daemon watching a read-only bind of the host's `/nix/var/nix/gcroots/auto`, translating each new entry whose target is a jail path | every tool, because it watches the daemon call's result ([§2.2](#22-what-the-client-sends)) | no, it is outside nix's path | a read-only mount and an in-jail daemon |
| **D** | A *mirrored roots directory* *(coined here)*: one host dir bound at its identical absolute path, exported as a variable, with tools steered into it (`NIX_STATE_HOME`, `direnv_layout_dir`) | only what is steered; never the default `./result` | no | a host-spelled path inside the jail |

How option C behaves, completely:

- **Trigger.** An inotify create or rename on the bound `auto/` directory. Watching the inode
  delivers host-side creations through a bind mount. At startup it makes one pass over the existing
  entries, to catch links made while it was down.
- **Filter.** Translate only if the entry's target is a jail path that exists **in this jail** as a
  symlink into the store. That also drops other jails' identically-spelled entries
  ([§2.1](#21-who-resolves-the-path-and-where)), unless this jail has the same link, in which case
  rooting it is correct anyway.
- **Race.** The creating client holds a temp root until it exits ([§2.2](#22-what-the-client-sends)
  step 1), so the window between its exit and the watcher's registration is milliseconds. Deletion
  of the jail-spelled entry by a GC is irrelevant: the watcher needs to see the creation, not the
  entry's survival.
- **One writer.** Only the watcher registers translated roots. It never writes into `auto/`; the
  bind is read-only, and the daemon does the write.
- **Where it runs.** A container jail with the host nix mounted, which is the one place the mount
  namespaces differ. `macos-user` needs none of this: it shares the host's filesystem, and an
  indirect root made there is already valid (measured: [`setup-support-gaps.md`](../plans/setup-support-gaps.md) row 12 of its measurement table).

Option A comes free with C: the verb is the watcher's translation step run by hand. So "C" means
"C, with A as its test surface and escape hatch".

Option D is why the measurement exists (M5), and it is worse than C on every axis. It protects
nothing by default. It puts a host-home-spelled path into the jail's filesystem, which is the
credential-boundary signal [`workspace-path-mirroring.md` §12.5](workspace-path-mirroring.md#125-the-credential-boundary--the-argument-that-could-have-killed-it-and-does)
weighs heavily. And the steering it needs changes where `nix profile` keeps its state.

## 6. Alternatives rejected

| Alternative | Verdict |
|---|---|
| **Protocol proxy**: a yolo socket in front of the daemon that rewrites `AddIndirectRoot` in flight | Rejected. It is the only fully transparent option, but the worker protocol is not self-delimiting, so the proxy must frame every op, NAR streams included, for every protocol version. One bug breaks every nix command in every jail |
| **Mirror the workspace at its host path** | Out of scope here, and not needed. [`workspace-path-mirroring.md`](workspace-path-mirroring.md#OQ-WP1) asks whether a fourth absolute-path problem exists. This is one, but translated roots fix it without mirroring, so it does not move that verdict |
| **Trust the jail** (`trusted-users`) for `AddPermRoot` | Rejected. The root would still be created in the daemon's namespace at a path the jail must spell for the host, so translation is still needed. And a trusted client is root-equivalent on the host |
| **Temp-root keeper**: an in-jail process holding one connection and `AddTempRoot`ing what the jail references | Rejected as the primary mechanism. It protects only while the jail runs, needs a list of what to keep, and has no way to drop one root short of reconnecting |
| **Host-side reconciler** reading `gcroots/auto` and re-rooting jail-spelled entries | Rejected. It cannot tell which jail an entry came from (one auto entry per path string), and it races the stale deletion that any root query performs |
| **`keep-outputs` / `keep-derivations`** | Irrelevant. They widen what an existing root keeps; they create no roots |

## 7. Non-goals

- **Rooting jail-only paths.** `/tmp` in a jail is scratch. A link there stays unrooted
  ([OQ-NR3](#OQ-NR3), decided as [NR-D1](#NR-D1)).
- **Reaping.** No yolo reaper for translated roots; the link is the root ([§4](#4-the-translated-root)).
- **Apple Container and the container Macs.** In-jail nix there is ruled "possible, but not planned"
  ([`setup-support-gaps.md`](../plans/setup-support-gaps.md#2-ranked-gap-backlog) G21). Everything here applies unchanged if that ever changes.
- **The unprivileged-daemon mode.** Runtime roots stop protecting jail processes under it
  ([§2.3](#23-what-already-protects-a-jail)). Translated roots are unaffected, so this design is the
  better position if a host ever adopts that mode.

## 8. What is built

The briefing states the gap, wherever the launch mounts the host nix daemon. It says that nix here
uses the host daemon and store, that a link made here is not a root the host honors, that a running
process keeps what it uses, and to rebuild a dangling link. That line is true today and remains true
for anything a translated root cannot cover, so it outlives this design. Its wording changes when
[OQ-NR1](#OQ-NR1) is built.

**yolo's own roots are translated roots** ([NR-D2](#NR-D2), built 2026-10-01). Three pieces, and
none of them is a trigger for a user's links:

- **The map.** A podman launch that mounts the host nix daemon, and is not sealed, sets
  `YOLO_HOST_PATH_MAP` in the jail: a JSON object from each mount's destination to the host
  directory it is, read off the argv the launch is about to run (`hostPathMapEnvArgs` in
  [`translatedroots.go`](../../internal/cli/run/translatedroots.go)). A read-write bind of an
  absolute source translates. A read-only bind, a volume and a tmpfs stay in the map with an
  empty value, a *mask* *(coined here)*: mounts nest, and longest-prefix matching would otherwise
  translate a path under one through the mount above it. A nested launcher passes each source
  through its own jail's map first, and a source that map cannot spell becomes a mask. A map in
  which nothing translates is not set at all.
- **The client.** [`internal/nixroots`](../../internal/nixroots) speaks the handshake at
  protocol 1.37 and `AddIndirectRoot`, nothing else. `Registrar.Root` resolves the link's
  directory, translates it, replaces the link and sends the host's spelling. A link it cannot
  translate is left uncreated, and the daemon is not dialled.
- **The consumers.** `gcRooter` gives a host launch `nix-store --add-root` and an in-jail one the
  translated root, for four roots: the image's, the prefix's, the image extras' and the
  store-delivered packages' profile. A jail whose launcher stated no map gets none of them, the
  old skip. Of every failure, only the daemon refusing the root prints, as a warning on the
  launch stream.

The tests drive each of the four call sites against a fake daemon
([`translatedroots_test.go`](../../internal/cli/run/translatedroots_test.go)).

MEASURED 2026-10-01, from this jail against the host daemon (Nix 2.35.2, `Trusted: 0`). This
jail's own launcher predates the map, so every run that needed one was handed
`{"/workspace": "$YOLO_HOST_DIR"}` by hand, with `HOME` under the workspace:

- a nested launch from a workspace under `/tmp` registered its image root and its prefix root.
  The host's `nix-store --query --roots` listed both under the host's spelling of the link, and
  once the links were deleted it listed neither;
- the same production path rooted a fresh `builtins.toFile` probe. `nix-store --gc --print-dead`
  listed the probe before the root, did not list it with the root in place, and listed it again
  once the link was gone;
- the nested jail's own map translated only its `~/.cache`, whose source lay under the workspace.
  Its `/workspace` and `~/.local`, whose sources lay under `/tmp`, were masks, as
  [NR-D1](#NR-D1) intends;
- with no map handed over, the nested launch made no root links and set no map in its jail.

**Not translated, and not one of the four:** the `--out-link` that yolo's own in-jail
`nix build`s write (`image.JailPrefixOutLink`, `image.ImageCopierOutLink`) still registers under the
jail's spelling, dead as before. The prefix's is backed by the prefix root above. Nothing backs
the image copier's, so unless the host roots the same copier for itself, a host GC can still cost
a nested launch a rebuild of it.

## Open Questions

1. 💬 <a id="OQ-NR1"></a>**OQ-NR1: What triggers a translated root?** This decides whether in-jail nix is protected
   by default or on request, and how much new surface ships. The options are compared in the
   [§5](#5-what-registers-the-translated-root) table.

   - **A — An explicit verb.** Cheapest. It protects only what an agent remembers to keep.
   - **B — A `nix` wrapper.** Automatic for `nix build`, blind to profiles and nix-direnv, and it
     sits in the path of every nix command.
   - **C — The root watcher, with A as its hand-run form.** Automatic for every tool, and outside
     nix's path. Costs a read-only mount ([OQ-NR2](#OQ-NR2)) and an in-jail daemon.
   - **D — A mirrored roots directory.** Protects only what is steered into it, and puts a
     host-spelled path in the jail.

   <!-- vantage: oq id=OQ-NR1 leaning="C with A as its hand-run form: it is host nix's own semantics, covers every root-making tool because it watches the daemon call rather than argv, and cannot break a nix command because nothing sits in nix's path." -->

   _Leaning:_ C, with A as its hand-run form. It gives host nix's own semantics, covers every
   root-making tool because it watches the daemon call rather than argv, and cannot break a nix
   command because nothing sits in nix's path.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 <a id="OQ-NR2"></a>**OQ-NR2: May the host's `/nix/var/nix/gcroots/auto` be bound read-only into a jail?** This
   gates option C. The jail would see the names of every host user's indirect-root links (the
   entries are hashes; their targets are host paths). An untrusted jail can already read most of
   that through the daemon's `FindRoots`, which returned host paths such as
   `/home/matt/sysadmin/obsrec` uncensored in [§3](#3-measured-in-this-jail). The difference is
   that the mount also shows dead entries and shows changes as they happen.

   <!-- vantage: oq id=OQ-NR2 leaning="Yes, disclosed at launch like every other mount: it exposes paths, not contents, and FindRoots already hands an untrusted jail most of the same list." -->

   _Leaning:_ Yes, disclosed at launch like every other mount. It exposes paths, not contents,
   and `FindRoots` already hands an untrusted jail most of the same list.

   **Answer:**
   > _(empty — fill in when decided)_

3. ✅ <a id="OQ-NR3"></a>**OQ-NR3: Do the anonymous `/tmp` and `/var/tmp` volumes translate?** The host launcher can
   learn their host paths from `podman volume inspect`, but a nested launcher cannot, and the
   documented nested-jail workspace is `/tmp/yolo-nested`. Translating them would root builds made
   under nested workspaces; leaving them out keeps the map to binds the launcher itself wrote.

   _Leaning:_ Binds only in the first version. `/tmp` is scratch, and a nested jail's own roots are
   better handled by [OQ-NR4](#OQ-NR4) than by translating volumes.

   <!-- vantage: oq id=OQ-NR3 -->

   **Answer:**
   > Decided as an implementation choice ([NR-D1](#NR-D1)), reversible: binds only. The map holds
   > the binds the launcher wrote, and a link under `/tmp` or `/var/tmp` stays unrooted, as it is
   > today.

4. ✅ <a id="OQ-NR4"></a>**OQ-NR4: Should yolo's own in-jail roots use translated roots?** A nested launch skips
   `image.RegisterImageRoot` and `image.RegisterPrefixRoot` in-jail
   ([`imageload.go`](../../internal/cli/run/imageload.go) `rootImageFn`,
   [`jailprefix.go`](../../internal/cli/run/jailprefix.go)), because such a root was dead. Those
   links live under `/home/agent/.local/share/yolo-jail/build`, which is a mapped bind, so they
   would translate. The prefix is protected by runtime roots while a nested jail runs, but not
   between nested launches.

   _Leaning:_ Yes, as the first consumer. It replaces two skips with the real root. One detail to
   check when building it: the host's reapers enumerate the host's own `build/roots` directories,
   so a nested jail's roots under a workspace's home overlay would need their own reaping story.

   <!-- vantage: oq id=OQ-NR4 -->

   **Answer:**
   > Decided as an implementation choice ([NR-D2](#NR-D2)), reversible: yes, as the first
   > consumer. There are three skips to replace, not two: the store-delivered packages' extras
   > profile skips its root in-jail too. Their reaping is the existing reapers', run by the jail
   > that made the links.

## Decision Ledger

No rulings yet: [OQ-NR1](#OQ-NR1) and [OQ-NR2](#OQ-NR2) are open. The two rows below are
implementation decisions, and both are built.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="NR-D1"></a>NR-D1 | *Implementation decision, [OQ-NR3](#OQ-NR3).* **Binds only: the map holds the binds the launcher itself wrote, and the anonymous `/tmp` and `/var/tmp` volumes do not translate.** Those volumes are per-launch scratch that yolo deletes once the jail exits, so a root there could outlive nothing but the launch. A running process that uses the store path is already kept by runtime and temp roots ([§2.3](#23-what-already-protects-a-jail)). A nested launcher cannot learn a volume's host path, while the host launcher could from `podman volume inspect`, so translating volumes would give one link two answers depending on who launched. A nested jail's own roots are [NR-D2](#NR-D2)'s. Reversible: the host launcher can add its volumes to the map later | 2026-09-30 | [§4](#4-the-translated-root), [§7](#7-non-goals) | 2026-10-01: the map holds every mount the argv makes, and a volume, a tmpfs or a read-only bind is a mask, so a link under `/tmp` or `/var/tmp` translates to nothing ([§8](#8-what-is-built)). MEASURED in a nested launch: its `/tmp` workspace was a mask |
| <a id="NR-D2"></a>NR-D2 | *Implementation decision, [OQ-NR4](#OQ-NR4).* **Yes: yolo's own in-jail roots become translated roots, the first consumer of the protocol client in [§4](#4-the-translated-root).** Three skips go, not the two the question names: `rootImageFn` (the image root, [`imageload.go`](../../internal/cli/run/imageload.go)), the in-jail skip of `image.RegisterPrefixRoot` ([`jailprefix.go`](../../internal/cli/run/jailprefix.go)), and the in-jail skip of `rootExtrasProfile` for the store-delivered packages' extras profile ([`storepackages.go`](../../internal/cli/run/storepackages.go)). All three root under `paths.BuildDir()`, which in a jail is inside `/home/agent/.local`, a mapped bind, so each one translates. This consumer needs no trigger from [OQ-NR1](#OQ-NR1), because yolo registers its own links directly. **Reaping stays with the existing reapers, run where the links live.** A translated root dies with its link. A nested launcher's `yolo prune` sweeps its own `build/roots` and `build/prefix-roots` under the same retention rules the host's sweep follows ([`OQ-LS1`](../reference/image-retention.md#why-its-this-way)'s week, [`OQ-LS4`](../reference/image-retention.md#why-its-this-way)'s liveness), so nothing new reaps. The leaning's concern holds, and it is why this is so: the host's reapers never walk a workspace's home overlay, so the jail that made a link reaps it. What remains is [§4](#4-the-translated-root)'s *who can hold host disk*: a nested jail's root holds its closure until that jail's own sweep removes the link. Reversible: keep the skips | 2026-09-30 | [OQ-NR4](#OQ-NR4) | 2026-10-01: `internal/nixroots` and `gcRooter` ([§8](#8-what-is-built)). Four skips went, not three: the store-delivered packages' own profile root (`storeProfileRootLink`) was skipped in-jail too. MEASURED for the image and prefix roots in a nested launch; the two profile roots are pinned by the unit tests only. Nothing reaps `build/package-roots`, on the host or in a jail, so a nested jail's two profile roots last until their links are deleted, as the host's do |

## Appendix: reproducing M4 and M6

The measurement client, verbatim. It speaks the worker-protocol handshake, sends one
`AddTempRoot` (`temp`) or `AddIndirectRoot` (`indirect`), and holds the connection for the given
seconds. Check the verdict with `nix-store --gc --print-dead | rg <hash>`, a dry run.

```python
# Minimal nix worker-protocol client: AddTempRoot / AddIndirectRoot on ONE connection, then hold it.
import socket, struct, sys, time
M1, M2, LAST = 0x6e697863, 0x6478696f, 0x616c7473
def wu(s, n): s.sendall(struct.pack('<Q', n))
def ru(s):
    b = b''
    while len(b) < 8:
        c = s.recv(8 - len(b))
        if not c: raise EOFError
        b += c
    return struct.unpack('<Q', b)[0]
def rb(s, n):
    b = b''
    while len(b) < n: b += s.recv(n - len(b))
    return b
def ws(s, t):
    t = t.encode(); wu(s, len(t)); s.sendall(t + b'\0' * ((8 - len(t) % 8) % 8))
def rs(s):
    n = ru(s); d = rb(s, n); rb(s, (8 - n % 8) % 8); return d.decode()
def stderr(s):
    while True:
        m = ru(s)
        if m == LAST: return
        if m == 0x63787470: raise RuntimeError('daemon error (STDERR_ERROR)')
        if m == 0x64617416: print('log:', rs(s)); continue  # STDERR_NEXT
        raise RuntimeError('unhandled stderr msg %x' % m)
s = socket.socket(socket.AF_UNIX); s.connect('/nix/var/nix/daemon-socket/socket')
wu(s, M1); assert ru(s) == M2; dv = ru(s); print('daemon proto %d.%d' % (dv >> 8, dv & 0xff))
wu(s, (1 << 8) | 37); wu(s, 0); wu(s, 0)
print('daemon version', rs(s)); print('trusted', ru(s)); stderr(s)
op, path = sys.argv[1], sys.argv[2]
wu(s, {'temp': 11, 'indirect': 12}[op]); ws(s, path); stderr(s); print(op, 'root ->', ru(s))
hold = int(sys.argv[3]) if len(sys.argv) > 3 else 0
sys.stdout.flush(); time.sleep(hold)
```

```console
$ python3 nixproto.py temp /nix/store/<hash>-probe 40 &      # M4: alive while held
$ ln -s /nix/store/<hash>-probe /workspace/.yolo/x
$ python3 nixproto.py indirect "$YOLO_HOST_DIR/.yolo/x"      # M6: alive until the link goes
```
