---
status: current
verified: 2026-11-06
summary: "When yolo moves, renames, converts or writes a user's file, it acts on the name it was given. A symlink is moved as a link, never written through, and yolo never guesses what its target means."
covers:
  - internal/configmigrate/
  - internal/config/newreadableconfig.go
tags: [principle, config, files, symlinks]
---

# The no-symlink-guessing principle

**Status:** PRINCIPLE, adopted 2026-11-06.

**Author:** Matt Schulkind, the maintainer. The name is coined here, by the agent that wrote this
document down, from the maintainer's words: *"guessing about symlinks leads to sadness."*

**Audience:** anyone writing code that moves, renames, converts, backs up or replaces a file the
user owns: config files, includes, homes, anything in a workspace.

**Sibling principles:** [`happy-path-principle.md`](happy-path-principle.md) (every stop names a
next step), [`fill-the-matrix-principle.md`](fill-the-matrix-principle.md) (one path per cell).

---

## The principle

> **yolo acts on the name it was given. When a user's file is a symlink, yolo moves the link as a
> link, writes any new file as a regular file at the new name, and never touches the target or
> infers anything from where the link points.**

A symlink is the user's own arrangement, usually made by a tool yolo does not know: a dotfiles
manager, a side-file store, a script. yolo cannot tell whether the target is a dotfiles checkout,
a shared file several workspaces read, or a store that will re-create the link on its own. Every
"smart" behavior built on one of those guesses is wrong for the users whose setup is different.
So yolo does the plain thing, and the result is predictable for everyone.

In practice:

- **Reading through a link is fine.** Loading a config reads the file the link reaches, as any
  program does.
- **Writing, moving or converting never goes through a link.** The link itself is renamed, and
  the new file is a regular file beside it. The target and its directory are left alone.
- **No relocation by inference.** yolo never writes a new file next to a link's target, never
  re-points a link at a new file, and never decides that the target is "where the user really
  keeps it".
- **Messages may describe a link; behavior may not depend on guessing about one.** A refusal or a
  warning can say a file is a link, that `rm` removes only the link, or that something re-created
  it. That tells the user what is there, and they decide what it means.

If the plain behavior leaves the user with work to do, such as copying the new file back into their
dotfiles, yolo says so in its output (the [happy path principle](happy-path-principle.md)). It does
not do that work by guessing.

## Where this came from

- **[RC-D41](../design/readable-config.md#RC-D41) (2026-11-01).** `yolo config migrate` moves a
  linked config's link to `<name>.pre-migrate` and writes the converted TOML as a regular file. The
  maintainer: *"I did want to write a normal non-linked file, because we can't know how their
  dotfiles work or if it is like mine. just do the dumb thing."* This replaced an earlier reading
  that converted the file inside the dotfiles tree and re-pointed the link.
- **[RC-D70](../design/readable-config.md#RC-D70) (2026-11-06).** A side-file tool re-created the
  moved link after migration, and the launch refused because both formats were present. The fix
  improved the refusal's wording and left the migration as it was. When a link-aware migration was
  proposed (write beside the target and link the new name to it), the maintainer said: *"no I
  don't like that. just like with "yolo config migrate" let's not play any games with symlinks. just
  do what we're doing. that "buggy" behavior is fine. guessing about symlinks leads to sadness.
  make this a general principle somewhere."*

## What this does not cover

Security handling of links is a separate matter: refusing to follow a link out of a confined tree,
or reading with `O_NOFOLLOW` (see [`agent-briefings.md`](agent-briefings.md#the-workspace-layer)),
protects host bytes and is not a guess about intent. Neither rule overrides the other.
