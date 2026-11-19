---
title: "Scoped imports: inputs and selection"
status: accepted
stage: GRADUATED
verified: 2026-11-17
verified_commit: 35055d92094c248c1cde87dfe3273fc3a5255e29
covers: [internal/config/readablepacks.go, internal/config/packs.go, internal/config/schema.go, internal/config/packprojection.go, internal/packsrc/scoping.go, internal/packload/skillprojection.go, internal/packload/skilldirectories.go]
tags: [packs, skills, toml, selection]
---

# Scoped imports: inputs and selection

The user TOML producer preserves selection presence, roots, naming and fragment origins.
The resolver selects candidates before dependency closure. This page uses the terms in the
[system overview](README.md#selection-before-activation).

## Whole, typed and raw modes

```toml
packs = [
  { pack = "local/corpus", skills = { only = ["review*"] } },
  { git = "https://example.invalid/team/repo", ref = "v1",
    skills = { dir = "skills/core-tools", only = ["skill-a", "skill-b"] } },
  { pack = "local/mono", subdir = "packs/tool" },
]
```

The last entry imports a whole relocated pack. It is not skills-only.
The first entry uses typed mode; the second uses raw mode. Paths are relative to the entry's
existing source root, with `skills.dir` relative to the relocated pack root if `subdir` exists.

`skills` must contain `dir`, `only` or `exclude`; an empty table refuses.
`[[packs]]` is the same TOML table-list model. Strings cannot carry selection.
Shipped entries permit typed selectors, not `subdir` or `skills.dir`.
The conventional automatically appended user `local` pack is not an authored scoped entry.

## Typed candidates

Typed mode reads the source declaration without activating it. It enumerates immediate child
directories of governed broadcasting skills trees: the conventional tree and declared extra
trees. Loose files are ignored. Later governing trees win duplicate ordinary names before
selection, preserving the ordinary whole-pack enumerator's precedence.

An ordinary directory can contain a regular marker or no marker. Its governor and audience
travel with the row. A destination declaration contributes no source candidates.
A manifest sourced row is a separate candidate, named by its alias or selected basename;
its explicit Git ref, audience, license and marker requirement remain intact.

Plugin-classified children are excluded. A literal selector naming one refuses with advice
to import the pack whole. No candidates refuses with advice to choose a raw directory.
Plugin classification must not become delivery or activation.

## Raw candidates

Raw mode never parses the original pack manifest, even if it is malformed. It does not run
conventional home, briefing, overlay or native plugin discovery.
If the selected directory has a direct regular `SKILL.md`, the root is one candidate; its
children are support, not more registrations. For `dir = "."`, its name comes from `subdir`
when supplied, otherwise the handle's last component.

Without a root marker, only immediate marker-bearing child directories are candidates.
Enumeration is nonrecursive. Markerless siblings are support and cannot become directory-kind
rows. No candidates refuses and can suggest deeper marker directories as raw-root choices.
Raw rows broadcast to all agent destinations.

## Selectors and refusal

Selectors use Go `path.Match` over upstream candidate names, not filesystem paths.
They are case-sensitive. Slashes, backslashes, NUL, recursive double-star patterns, malformed
globs, dot names, duplicates and empty strings refuse offline. An explicit list must be nonempty.

1. Include all candidates if `only` is omitted, otherwise every name matching any inclusion.
2. Remove every name matching any exclusion.
3. Refuse an unmatched selector in either list, an inclusion whose matches are entirely
   excluded, or an empty final selection.

A stale exclusion is an error, not silent widening. Refusals name origin, authored index,
candidates and the repair. Remove a selector key to restore omission; do not write an empty list.
A failed selection never becomes whole-pack or partial delivery.

## Roots and trust

Roots are clean relative forward-slash paths, literal rather than glob or Git pathspec.
`subdir = "."` refuses; omit it. `skills.dir = "."` selects the pack root legally.
Traversal, absolute paths, tilde, empty/dot segments and `.git` components refuse.
Git roots require real directory ancestry; root symlinks refuse even when their target stays
inside the repository. Selected Git links remain inside the selected directory.

Local roots are resolved and must stay inside the source boundary. Existing trusted-local
selected-content dereferencing remains; this is not permission to follow workspace links.
Pure local optional license/support reporting must not impose immutable-Git validation on
otherwise eligible local links or charge unselected bytes. See [inspection](inspection.md#verified-unverified-and-refused).

## Names and tuple identity

Names apply existing entry namespace/bare rules to upstream names. Roots and selectors do not
change the default namespace. Reserved/core-owned names and registration collisions refuse;
selection does not invent a per-skill alias option in user config.

Import identity is handle plus normalized `subdir` plus normalized raw directory.
Distinct nonconflicting roots of a handle may coexist, including two raw directories and a
relocated whole pack. Entries sharing the handle must share the authored ref, including the
omitted-versus-explicit distinction. The lock still has one handle record.
Duplicate tuples, duplicate physical roots with the same tuple, whole/scoped entries at the
same root and two typed selections at the same root refuse.

Selection identity contributes to staged slug and restart diff, not mirror or payload identity.
Changing only selectors does not fetch or create a second source cache.

## Effective declaration

Only selected rows remain in the effective declaration. Needs and other contribution kinds
are omitted before closure, never suppressed only at rendering time.
A live dependency targeting a scoped shipped root refuses with the needing pack and scope;
closure cannot silently widen that entry.
Modern fields do not populate legacy filters or sourced-row withholding.

## Editing ownership

`PackEntry` and the modern schema in `internal/config` own authored presence and origins.
`ProjectSkills` in `internal/packload` shares candidate enumeration with whole-pack delivery.
`SkillSelection` and `ScopedPath` in `internal/packsrc` own offline selector/path validation.
The [inspection reference](inspection.md#editing-and-removal) describes the bound CLI transaction.
