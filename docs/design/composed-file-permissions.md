---
title: "Composed-file postures — the three questions left open"
date: 2026-07-25
status: in-review
stage: DESIGN
next: "Rule CFP-2: its degradation rows were drafted from the tree on 2026-10-01, so the ruling prices a real table (two rows stand out: below Apple Container 1.1.0 a files directory is a writable bind into the staged pack tree, and macos-user puts no write deny on the git identity or the per-agent env files)"
tags: [config, prism, permissions, open-questions]
summary: "A stub. The taxonomy, the postures, the 0o444 finding and the writer-class split graduated to docs/reference/composed-file-permissions.md; what stays here is the three open questions (CFP-1…CFP-3), which are consequence questions about shipped mechanisms rather than gaps."
---

# Composed-file postures — the three questions left open

**Status:** 2026-09-09 — the settled body of this doc graduated to
[`../reference/composed-file-permissions.md`](../reference/composed-file-permissions.md) — the
Derived/Shared/State taxonomy, the host-linked axis, the `0o444` asymmetry finding, the model,
the `host_files` mode collapse, the home-root symlink decision, the program-operation /
directed-agent split, and the restart axis all live there and describe shipped behavior.

**This file is what remains: three open questions.** Each concerns a shipped mechanism working
as designed rather than a gap, and none blocks anything. Read the reference first; each question below assumes it.

**Needs your ruling:** [CFP-1](#CFP-1) (whether `:ro` for a Derived surface needs host-side composition, per-surface or blanket), [CFP-2](#CFP-2) (a documented degradation table for `macos-user` and Apple Container), [CFP-3](#CFP-3) (whether per-workspace is the right scope for the capture overlay sidecars).

**Related decisions tracked elsewhere:** the `0o444`-vs-`:ro` question for `host_files` is
`E1`/`E2` in [`../plans/BACKLOG.md`](../plans/BACKLOG.md) and `OQ-B` in
[`../plans/pack-host-management-plan.md`](../plans/pack-host-management-plan.md). **CFP-1 is
deliberately not a fourth ID for it** — it is the narrower consequence question that only arises
*after* `E2` is answered yes.

---

## Open Questions

IDs (`CFP-*`) minted 2026-08-23.

1. 💬 <a id="CFP-1"></a>**[CFP-1](#CFP-1): Does `:ro` for a Derived surface need host-side composition — and is that
   per-surface or blanket?** Yes to the first half, and it is the cost nobody has priced: you
   cannot compose into a `:ro` mount, so a `:ro` posture gives up `managed`/`defaults`
   and the overlay for that surface and moves its rendering to the host CLI. **This is the
   question `E2` (`readonly` as a real `:ro` mount) turns into the moment it is answered yes**, so
   it decides how expensive `E2` actually is.

   _Leaning:_ per-surface, not blanket. For a pure-overwrite computed surface the trade is clean —
   there is nothing layered to lose. ~~For anything carrying a Lua transform it is not, and a
   blanket rule would silently downgrade those.~~

   **2026-09-12: the second half of that leaning has lost its referent.** The Lua transform is
   removed ([`pack-system.md`](../reference/pack-system.md#oq-lt1)), so no surface carries one and
   the per-surface-vs-blanket call now turns on `managed`/`defaults` and the overlay alone.
   Whoever answers CFP-1 owns that re-weighing; this note does not make it.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 <a id="CFP-2"></a>**[CFP-2](#CFP-2): Should `macos-user` and Apple Container get a documented degradation table?** Both
   can lose `:ro`. Apple Container honors a read-only bind only from version 1.1.0
   (`acROBindsFloor`, `internal/cli/run/backendcaps.go`); below that yolo skips a `mounts` entry
   or a `host_files` directory rather than binding it writable, and `workspace_readonly` paths stay
   writable behind a warning. A single-file composed surface there is copied into the writable
   home either way. `macos-user` has no bind mounts at all, so a `:ro` surface becomes a
   materialized copy whose only protection is file mode against the separate sandbox account.
   This decides whether "read-only" means the same thing on all three backends.

   *Updated 2026-09-24:* [`settings-per-setup.md`](../../userguide/reference/settings-per-setup.md) now
   tabulates this per config key and per setup (`host_files`, `mounts`, `workspace_readonly`),
   which covers the host-file half of the question. It has no per-surface rows for composed
   (Derived) surfaces, so the question stays open.

   *Rows drafted 2026-10-01, from the tree at `d4e435a3`.* Every Derived surface a podman launch
   binds `:ro` into the home, and what the other backends do with it. READ from code, not run:
   every cell outside the podman column is a Mac question.

   | Derived surface | podman | Apple Container below 1.1.0 | Apple Container 1.1.0 and later | macos-user |
   | :--- | :--- | :--- | :--- | :--- |
   | A `skills` destination | `:ro` bind of the launch's staging dir | the same bind, `:ro` ignored: writable onto the host's staging dir, which the next launch re-stages | `:ro` honored | copied in by the home overlay; Seatbelt denies writes at the physical path and anchors every directory above it |
   | A `briefing` destination | `:ro` bind of the staged file | a copy in the home, writable, rewritten each fresh launch | the same copy, kept on every version | home overlay and Seatbelt deny |
   | A `files` directory | `:ro` bind of the directory in the launch's pack tree | the same bind, `:ro` ignored: writable into the staged pack tree an attach reads | `:ro` honored | home overlay and Seatbelt deny |
   | A `files` single file | `:ro` bind | a copy, writable, rewritten each fresh launch | the same copy | home overlay and Seatbelt deny |
   | Git identity, `~/.config/git/config` and `~/.config/git/ignore` | composed on the host, `:ro` bind | written into the home, writable, rewritten each fresh launch | the same | `~/.gitconfig`, which the bootstrap writes from `YOLO_GIT_NAME` and `YOLO_GIT_EMAIL`, with `core.excludesFile` pointed at the host's global gitignore, copied each launch into the root-owned context tree (read-only to the sandbox there): writable and kept, with the identity re-set each launch and a key the host stopped setting removed while it still holds the value yolo forwarded ([git-identity.md](../reference/git-identity.md#clearing-on-macos-user-remove-what-yolo-forwarded)); no Seatbelt deny |
   | Per-agent env files, `~/.config/yolo-agent-env/` | `:ro` bind of a directory the launcher rewrites on every entry | written into the home on every entry, writable | the same | written into the workspace sidecar on every launch; no Seatbelt deny |
   | The home root | the per-jail skeleton, bound `:ro` at `/home/agent` | the workspace state dir, bound writable over the whole home | the same | the sandbox account's home, writable except where the profile denies; a home-root `host_files` file is the skeleton's own relative link into the workspace's `.config` ([HT-D9](../reference/macos-user-home-tiers.md#ht-d9)) |

   Where each row comes from: the skills, briefing, pack-tree and agent-env arms of `assemble.go`
   (`internal/cli/run`), whose skills loop asks no `roBindsUnsupported` question;
   `packFilesMountArgs` (`packfiles.go`); the git arm of `assemble_parts.go`, and
   `podmanBaseMounts` for the home root; `deliverChannel` (`agentenvfiles.go`); and for
   macos-user, the overlay builder (`macoshomeoverlay.go`, whose destinations are the skills,
   briefing and `files` lists only), `ResolveHomeReadonly` (`internal/macosuser/homereadonly.go`),
   `configureGit` (`internal/entrypoint/identity.go`) and `writeMacosUserAgentEnvFiles`.

   Two rows matter more than the rest. The `files` directory row is a write into the very tree
   the pack-tree arm copies rather than binds, because a writable bind of it is *"precisely the
   'an agent that could rewrite a manifest could grant its own pack a host file' escalation"*
   (`acMaterializeTree`); a `files` directory holds no manifest, but it is inside that tree. And on
   macos-user the git identity and the per-agent env files get no Seatbelt deny. The env files
   are rewritten at every launch, while `~/.gitconfig` keeps whatever else is written to it.

   _Leaning:_ yes, per-surface, but only when `macos-user` is next worked on — writing it earlier
   means maintaining a table against a backend nobody is touching. (As of 2026-09-24 `macos-user`
   is being worked on, so that condition has arrived.) Note it has grown a neighbour:
   `render.FieldSet` / `HostUnimplemented` is now the mechanism for "this notch cannot honor that,
   and says so by name", so the table may want to be *code* rather than a doc.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 <a id="CFP-3"></a>**[CFP-3](#CFP-3): Is per-workspace the right scope for the capture overlay sidecars?** They live
   under `<workspace>/.yolo/prism/`, so the same host file composed in two workspaces can diverge
   invisibly in different directions. Unexamined rather than obviously wrong. It is the same scope
   question a sidecar-relocation decision would be blocked on: is a captured edit per-workspace or
   per-machine?

   _Leaning:_ per-workspace is right for the jail, and the answer is already settled for the
   *host* notch in the opposite direction — `render.Target.ProvenanceDir` puts host records under
   the user's own state directory, user-scoped, with the reasoning in its doc comment. So the
   shape of the answer already exists; what is open is whether the *jail* overlay should follow
   it.

   **Answer:**
   > _(empty — fill in when decided)_
