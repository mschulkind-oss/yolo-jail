---
title: "The sync root is not a skill"
date: 2026-09-18
status: draft
tags: [design, skills, packs, host-notch, claude, adoption, trust]
summary: "~/.claude/skills/synced/<uuid>_<uuid>/ is not a plugin install and not a skill — it is claude.ai's sync landing zone, one directory per Anthropic identity, written continuously by a process yolo does not control. yolo has never heard of it, so `yolo host apply` reads it as a single hand-written skill named `synced`, moves the whole tree into the user's local pack, and thereafter reverts every update the syncer pushes. A transition path for such a user therefore starts with a fence, not a copier."
vantage:
  status-chip: true
---

# The sync root is not a skill

**Status:** DESIGN, 2026-09-18 — nothing built; four questions open.
[§3.2](#32-on-the-host-yolo-eats-it--measured) is **MEASURED**; everything else is read from
the tree or from the vendor's binary, dated where it is claimed.

> **In short.** `~/.claude/skills/synced/<uuid>_<uuid>/` is a **sync root**, not a plugin
> install: one bucket per Anthropic identity, rewritten continuously by Claude Code. A
> transition path for a user who has one therefore starts with a fence, not a copier.

**Why it matters.** yolo has never heard of it, so `yolo host apply` takes it as a
hand-written skill named `synced`. Measured: the second apply **deleted a newly synced skill
and reverted an edited one**, reporting `Applied: 1 composed skill.`

**The shape.** A pack-declared **fence** that stops yolo composing inside another tool's tree;
an explicit **snapshot** that copies chosen skills into a yolo-owned pack; a **drift report**
that is the whole update story.

**Cost.** A snapshot is a second copy of content a vendor keeps updating. This design accepts
the divergence and makes it visible rather than pretending to solve it.

**Start at [§3](#3-what-yolo-does-with-it-today)** — what happens today is the design.

**Needs your ruling:** [OQ-ST1](#OQ-ST1), [OQ-ST2](#OQ-ST2), [OQ-ST3](#OQ-ST3), [OQ-ST4](#OQ-ST4).

**Reads with:** [`synced-skill-trees-plan.md`](synced-skill-trees-plan.md) (the implementation
sketch, and the measurement transcript), [`workspace-skills.md`](workspace-skills.md) (the same
composition one scope down), [`../reference/pack-system.md`](../reference/pack-system.md#skills)
(the `skills` kind), [`../plans/setup-support-gaps.md`](../plans/setup-support-gaps.md) (G32 —
[§10](#10-what-this-does-not-propose) says how they relate).

---

## 1. Verdict, and the principles it rests on

**A tree another tool writes is never yolo's to compose.** Everything below is that sentence
applied somewhere specific.

- **P1. A vendor-owned tree is fenced, not adopted.** If a process outside yolo writes a
  directory continuously, yolo may read it on the host and copy out of it. It may never own
  it, move it, or compose over it.
- **P2. Core learns no tool's directory names.** `synced` is Claude Code's word. The fence is
  declared by the pack that declared the destination, exactly as tier is
  ([`../reference/pack-system.md`](../reference/pack-system.md#skills)). Hardcoding
  `".claude/skills means synced is special"` into core is the coupling
  [`../../AGENTS.md`](../../AGENTS.md) forbids, and `internal/hostskills`' tier doc comment
  already refuses the identical shortcut in as many words.
- **P3. The jail never reads a host skills tree.** The deleted `SkillTarget.HostSource` is the
  cautionary tale ([`../../internal/jailcontent/skills.go`](../../internal/jailcontent/skills.go)
  states it where the field was): a jail reading `~/.<agent>/skills` found yolo's own generated
  output, not the user's tree. Host material crosses by the host notch composing it into a
  pack — the channel that already exists — and by nothing else.
- **P4. A transition is an adoption, so it preserves.** The existing rule, and it is already
  written down: *"Adoption preserves, declaration refuses"*
  ([`../reference/pack-system.md`](../reference/pack-system.md#skills)). A name conflict
  arising from taking over a user's pre-existing content keeps both copies under a suffix; a
  name conflict between two packs' declarations is fatal.
- **P5. Two copies is a fact, not a bug to hide.** Once yolo snapshots a synced skill there are
  two copies of it on the machine and they diverge the moment the vendor ships an update.
  yolo's job is to say so, on a schedule the user can predict, and never to resolve it
  silently in either direction.

> [!IMPORTANT]
> **Defined here.** A **sync root** *(coined here)* is a directory inside an agent's skills
> directory that the agent's own vendor process writes and rewrites — today, exactly
> `~/.claude/skills/synced/`. A **bucket** *(coined here)* is one identity-scoped child of a
> sync root. Neither is a skill, and neither is a plugin. **Adoption** is *not* coined here: it
> is `internal/hostskills`' existing word for "an entry at a composed destination that yolo
> cannot prove it wrote", and this doc argues a sync root must stop qualifying as one.

## 2. What the synced tree actually is — verified

The user's report was *"appears to come from installing a Claude plugin."* That is the natural
reading and it is wrong in the way that matters: the directory is not per-plugin, so nothing
about it is per-install.

Everything in this section was read out of the `claude` 2.1.275 binary installed in this jail
on 2026-09-18 — never from vendor docs, following the lesson
[`../plans/pack-host-management-plan.md`](../plans/pack-host-management-plan.md#n6-new--copilot-reads-claudes-plugin-manifests-and-namespaces-plugin-skills)
records. It is a private layout with no compatibility promise; the cheap re-check on a version
bump is in [the sketch](synced-skill-trees-plan.md#where-the-claudeai-facts-came-from).

### 2.1 The two UUIDs are an identity, not a plugin and a version

`~/.claude/skills/synced/<uuid>_<uuid>/` is **one bucket per Anthropic identity**:
`<organizationUuid>_<accountUuid>`, both lowercased, joined by a single underscore.

The binary's own parser is unambiguous about this: it
splits on `_`, requires exactly two parts, validates each against
`/^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/i`, and returns a record whose two fields the
vendor's own code names **`org`** and **`account`**. Three corroborating details from the same
module:

| Fact | Evidence in the binary |
| :--- | :--- |
| The account half may be absent | It is then the literal string `unbound`, and the parser accepts that spelling in place of a UUID |
| The ids come from the signed-in identity | Minted from `CLAUDE_CODE_ORGANIZATION_UUID` / `CLAUDE_CODE_ACCOUNT_UUID`, falling back to the stored auth record's `organizationUuid` / `accountUuid` |
| The shape is the vendor's own redaction template | A telemetry helper returns the literal strings `"<org>_<account>"` and `"<org>_unbound"` for exactly these two cases |

So a user signed into two organizations has **two buckets**, and the same skill name can appear
in both. That is the collision this design has to answer — not a collision between opaque
directory names, because the skills inside a bucket are named by human display name, not by id.

> [!NOTE]
> **Inferred, not verified:** that the bucket is created on first sync rather than at login, and
> that a user switching orgs accumulates buckets instead of replacing one. Neither changes any
> decision below — both readings produce "one or more buckets, each possibly holding a
> same-named skill".

### 2.2 What lives in a bucket, and how Claude loads it

A bucket's children are synced **items**, each a directory. Two kinds, and the difference
decides which transition route applies:

- **A synced skill** — a directory with a `SKILL.md`. It gets a synthetic namespace: when their
  bare names cannot be checked for collisions, the binary reports that synced skills *"answer
  only to `anthropic-skills:<name>` this session"*. So on the host these skills already have a
  two-level name the user may be typing.
- **A synced plugin** — a directory carrying a plugin manifest. It loads through the same
  reader that turns `~/.claude/skills/<name>/` into `<name>@skills-dir`, under a third source
  kind the binary spells `claude.ai-synced`. Only **one synced copy per plugin name** is
  considered, the rest are reported not-loaded, and a local copy shadows a synced one.

Item **names** are sanitized display names, not ids: `<>:"|?*\/` become `_`, trailing dots and
spaces are stripped, and a duplicate name gets a `~g2` / `~g3` … generation suffix. This is the
half that makes the transition easier than the UUID directory suggests — **the bucket name is
throwaway and the item names are already human** — and the half that plants a trap, because
`review~g2` is a legal thing to find on disk and a nonsense thing to hand to `codex`.

### 2.3 Reserved siblings

Inside the sync root, `.trash` and `.staging` are reserved. The trash is load-bearing for this
design: when an organization turns Skills off, the binary's own changelog says synced skills
*"now move to the recoverable trash"* rather than being deleted. So the sync root holds content
the user may still want and is **not** a pure cache — which rules out "just delete it" as a
transition.

`synced` is itself a reserved name in the skills directory: the binary refuses an imported rule
named `synced` because *"its name is the skills directory name Claude Code reserves for synced
skills, so the skill would never load."*

## 3. What yolo does with it today

Checked against the working tree on 2026-09-18, at the commit whose subject is *fix(run):
Ctrl-C left the terminal wearing the jail's tab*.

### 3.1 In a jail: nothing, and that is right

A skill in `~/.claude/skills` on the host does not reach a jail by any path. Confirmed on
2026-09-18: `jailcontent.PrepareSkills` stages the built-in suite plus each selected pack's
sources and nothing else, and the destination-reading layer that once existed
(`SkillTarget.HostSource`) is deleted with its reasoning left in place. A case-insensitive
search for `synced` across `internal/` and `packs/` returns no hit about this mechanism at
all — every match is unrelated.

Two routes already reach the outcome for a user who wants it: the conventional local pack, and
a `packs` entry pointing at a directory of their own. Neither reads the sync root, and neither
should. **This half needs no change** — see [OQ-ST3](#OQ-ST3) for whether the user is *told*.

### 3.2 On the host: yolo eats it — MEASURED

`packs/claude` declares `{"kind": "skills", "agent": "claude", "into": ".claude/skills"}`, so
`yolo host apply` composes the user's **real** `~/.claude/skills` wholesale. Its adoption scan
(`hostskills.Adoptions`) walks that directory's top-level entries and excludes four kinds:
a path in the composed record, a path in the legacy record, a yolo-marked plugin dir, and a
hand-authored plugin dir. It also skips dot-entries and non-directories.

`synced` is a directory, is not dot-prefixed, is in no record, and carries no manifest of its
own — the manifests are two levels down. **It passes every exclusion and is reported as one
adoption named `synced`.**

Measured against a throwaway home holding a bucket with two skills plus one hand-written
skill — transcript compressed, the full run is in
[the sketch](synced-skill-trees-plan.md#the-measurement):

```console
$ HOME=$FAKE yolo host apply -v
⚠ yolo COMPOSES these skills directories wholesale, and they currently hold 2 skill(s) yolo did not write:
  …/.claude/skills/my-own-skill
  …/.claude/skills/synced
  skills     synced    would move to your local pack  moved to …/.config/yolo-jail/local/skills/synced

$ printf 'y\n' | HOME=$FAKE yolo host apply --assert
Applied: 1 config file, 2 skills moved into your local pack.
```

After that apply the sync root — bucket, `.trash` and all — sits in
`~/.config/yolo-jail/local/skills/synced/`, and a byte-identical copy has been composed back
into `~/.claude/skills/synced/`. **The tree looks unchanged, which is precisely why nobody
would notice.** What has actually happened is that a directory with one writer now has two,
and the second one holds a frozen copy.

Then the syncer does its job. Simulating one upstream update — a new skill and an edit to an
existing one — and applying again:

```console
$ # upstream adds new-from-upstream/, and rewrites pdf-tools/SKILL.md
$ printf 'y\n' | HOME=$FAKE yolo host apply --assert
Applied: 1 composed skill.

$ ls $FAKE/.claude/skills/synced/<bucket>/
brand-voice
pdf-tools                 # ← reverted to the frozen copy
                          # ← new-from-upstream is gone
```

`new-from-upstream` was **deleted** and `pdf-tools` was **reverted**, in one line of output
that names neither. This is the finding the doc exists for.

### 3.3 Why nothing caught it

Not an oversight in the adoption scan — a missing concept. Every exclusion it implements
answers *"is this yolo's?"*; none of them answers *"is this somebody else's?"*, because until
now the only two answers were "yolo wrote it" and "the user wrote it". A vendor-written tree is
a third thing, and the confirmation prompt cannot rescue it: the prompt names the entry
`synced`, the user says `y` once for the whole list, and `synced` is a plausible-looking name
for a skill.

The jail half is unaffected today only because nothing there reads the host — but the moment a
host apply has run, the local pack carries `synced/` and the jail composition copies it in
wholesale (`copySkillSubdirs` copies every immediate subdirectory with no `SKILL.md` test),
which puts the organization and account UUIDs of the user's Anthropic identity into every jail
and every other agent's skills directory. **Code-read, not measured** — it follows from the
copier, but no jail was launched to watch it.

## 4. The three components

### 4.1 The fence — the part that is not optional

A `skills` destination may declare **reserved children**: names at its top level that are never
adoption candidates, never composed over, and never archived. Declared by the pack that
declared the destination (P2), so `packs/claude` says `synced` and core says nothing.

Behaviour, exhaustively:

| Situation | What happens |
| :--- | :--- |
| A reserved child exists at a destination | Excluded from adoption; **one report line** naming it and saying yolo composes around it |
| A reserved child does not exist | Nothing at all — no line, no directory created |
| A pack's `skills` contribution ships a directory whose name is a reserved child of its destination | **Refused at apply and at launch**, naming the pack and the reserved name (it could never load anyway — [§2.3](#23-reserved-siblings)) |
| Two packs declare reserved children at one destination | The union; a name is reserved if any declaring pack says so |
| A reserved child is a dangling symlink | Left alone. The dangling-link carve-out exists for *absent content*; a reserved name is not yolo's to reason about either way |

The fence is an **exclusion, never a refusal of the apply**. A user with a sync root who does
not care must still be able to run `yolo host apply` — refusing would make a vendor feature
into a yolo outage.

### 4.2 The snapshot — the transition itself

An explicit, user-invoked verb. Never automatic, never on a timer, never part of a launch, and
never part of `yolo host apply`: the trigger is the user typing it and nothing else.

- **Unit: one skill.** Not the tree, not the bucket. The sync root's own structure — buckets,
  `.trash`, `.staging` — is Claude's and does not travel.
- **Direction: a COPY.** The host original stays exactly where it is, still owned and still
  updated by the syncer. Moving is not on the table: the tree is the syncer's working
  directory, and [§3.2](#32-on-the-host-yolo-eats-it--measured) is what moving it looks like.
  (What a syncer does when its root vanishes entirely is *not* measured here — the measured run
  put a copy straight back. The point stands without it: a move makes yolo the owner of a
  directory another process is mid-write in.)
- **Destination: a pack** — which one is [OQ-ST1](#OQ-ST1).
- **A record of what was taken.** Each copied item records its source bucket, its item name, and
  a content digest. That record is what makes [§4.3](#43-the-drift-report--the-whole-update-story)
  possible and what lets a re-run be an update rather than a duplicate.
- **One writer.** The snapshot verb is the only writer of the copies it made. The syncer is the
  only writer of the sync root. Neither ever writes the other's side.
- **Failure of one item does not fail the run.** An unreadable item, an item without a
  `SKILL.md`, an item whose content changes while it is being read — each is skipped, named,
  and leaves nothing half-written at the destination; the rest of the run continues and the
  exit code reports that something was skipped.

### 4.3 The drift report — the whole update story

A vendor ships a new version of a synced skill; the syncer updates the host tree; yolo's copy is
now stale. yolo does **not** watch, poll, or re-copy on its own.

What it does instead: wherever it is already reading the host home — the `yolo host apply`
report, and a listing verb — it compares each recorded source against the sync root and prints
one line per item that has changed, gone, or appeared. Re-running the snapshot is how the user
acts on it. Whether that comparison ever escalates beyond a report is [OQ-ST4](#OQ-ST4).

Three states, all named rather than merged:

- **changed** — the recorded digest and the sync root disagree. The user re-runs the snapshot.
- **gone** — the item is no longer in the sync root (unsynced, or moved to `.trash`). yolo's
  copy is kept and reported as orphaned; deleting a user's only remaining copy of something a
  vendor withdrew is not yolo's call.
- **new** — an item in the bucket that was never snapshotted. Reported, never taken; taking it
  would make the snapshot an automatic sync by another name.

## 5. Naming and collisions

The bucket name is dropped. The item name is kept. That is most of the naming question, and it
is easier than an opaque-UUID framing suggests, because the UUIDs never name a skill
([§2.2](#22-what-lives-in-a-bucket-and-how-claude-loads-it)).

What is left is genuinely hard, and it is the interaction between four collision rules that do
not agree with each other:

| Where | Rule today | What a snapshotted skill meets |
| :--- | :--- | :--- |
| claude.ai sync root | `~g2` / `~g3` suffix within one bucket; **first copy wins across buckets** for plugin names | Two orgs can each hold `review` and nothing on disk disambiguates them |
| yolo, host notch | A skills name collision between two **packs** is **FATAL** at apply time ([`../reference/pack-system.md`](../reference/pack-system.md#skills)) | A snapshotted `review` in the local pack turns a previously-working apply into a refusal |
| yolo, jail notch | **Silent last-writer-wins**; the local pack is last ([`S5`](../plans/BACKLOG.md#-s5--a-jail-resolves-a-skill-name-collision-silently)) | A snapshotted `review` silently outranks a shared pack's `review` in every jail |
| `copilot` | Namespaced for *invocation*, **deduplicated by BARE name** | Even a namespaced delivery can be dropped silently there |

Four rulings, all following from P4 and none of them new mechanism:

1. **Within one bucket, keep the vendor's name verbatim, `~g2` suffix included.** Rewriting it
   would break the one thing the user can currently predict — what they type. That a
   `review~g2` then exists in a pack is ugly and honest; the alternative is yolo inventing a
   second name for something already ambiguous.
2. **Across buckets, keep both under a suffix and warn, naming both sources.** This is exactly
   `MigrateHostSkills`' existing `kept both (renamed)` behaviour and it needs no new rule —
   adoption preserves.
3. **Identical content under one name is not a collision.** Also existing behaviour, measured
   before it was designed: the second copy is absorbed silently. A user in two orgs sharing one
   corporate skill is the common case and should cost no words.
4. **A snapshot that would make a *declaration* collision fatal is refused before it writes**,
   naming the pack it would collide with and offering the rename. Turning the user's next
   `yolo host apply` into a refusal as a side effect of a transition is the one outcome that
   must not be discovered later.

This design **does not** fix [`S5`](../plans/BACKLOG.md#-s5--a-jail-resolves-a-skill-name-collision-silently),
and must not add another silent path — every ruling above either warns or refuses.

## 6. Degenerate inputs

| Input | Behaviour |
| :--- | :--- |
| No `~/.claude/skills/synced` at all | The fence still applies (the directory may appear tomorrow); the snapshot verb reports "nothing to take" and exits zero |
| Sync root present, no buckets | Same as above |
| Bucket present, only `.trash` / `.staging` | Nothing to take; the reserved siblings are never candidates |
| Exactly one synced skill | The ordinary path; nothing special |
| `<org>_unbound` bucket | An ordinary bucket ([§2.1](#21-the-two-uuids-are-an-identity-not-a-plugin-and-a-version)) |
| Two buckets, same skill name, same bytes | Absorbed silently (ruling 3) |
| Two buckets, same skill name, different bytes | Both kept, suffixed, warned (ruling 2) |
| Item with no `SKILL.md` | Not a skill to any of these tools. Skipped and named — never copied |
| Malformed `SKILL.md` frontmatter | **Copied unchanged.** yolo does not parse `SKILL.md` and will not start; the tool reports the parse failure where the author can act on it, which is the same stance `pluginpack.Load` takes on a malformed plugin manifest |
| Item carrying references, scripts, or data beside `SKILL.md` | Copied wholesale — a skill is a directory, not a file |
| Item that is a **plugin** (carries a plugin manifest) | Not snapshotted. `yolo pack init --from-plugin <dir>` already wraps a plugin tree verbatim so its skills invoke `/<plugin>:<skill>`; the snapshot points at it by name rather than flattening a plugin into loose skills. This mirrors `Adoptions`' existing refusal to migrate a hand-authored plugin |
| Item containing a symlink | ⚠ The skills copiers **dereference** symlinks on both notches. A synced item holding a link to `~/.ssh/config` becomes those bytes in a pack and then in every jail. Escaping links are refused by path before anything is copied — the same caution [`workspace-skills.md`](workspace-skills.md) reached for the workspace source |
| Sync root is a symlink to elsewhere | Followed for reading; still fenced, still never written |
| The syncer writes during a snapshot | The item's digest is re-read after the copy; a mismatch reports `changed while being read`, discards that item, and asks for a re-run |

## 7. Homes that are already wrong

This is not hypothetical state to migrate — any user who has run `yolo host apply --assert`
against a home with a sync root already has one, and it is invisible because the tree looks
right.

The recovery is its own decision and the doc states it rather than leaving it to the
implementer: when the local pack holds a `skills/` entry whose name is a reserved child of any
declared destination, yolo **reports it and offers to put it back**, and does nothing without
being told. Not automatic, because the two copies may have diverged by then and yolo cannot
know which one the user wants; not silent, because the whole defect is that it was silent.

What the offer restores: the sync root's content moves back under the destination, the local
pack entry goes away, and the composed record forgets it. Any *newer* content the syncer has
since written into the destination wins — the copy in the local pack is by definition the
older one.

## 8. Alternatives considered

- **Do nothing; document it.** *Rejected.* It is not a rough edge, it is silent deletion of
  content a vendor pushed — measured, with the report saying `Applied: 1 composed skill.`
- **Refuse `yolo host apply` when a sync root is present.** *Rejected.* Correct and unusable: a
  vendor turning on a feature would break an unrelated yolo command for every user of it.
- **Teach core the name `synced`.** *Rejected for the mechanism, kept as a fallback.* It is
  three lines and it works. It is also the exact coupling `internal/hostskills`' own doc
  comment refuses (*"Inference (`.claude/skills` means tier A) would hardcode a tool's name
  into core"*), and the second vendor to ship a sync root would need another three.
- **Make the jail read `~/.claude/skills`.** *Rejected outright.* P3; the deleted
  `SkillTarget.HostSource` is the recorded cost of trying.
- **Mount the sync root into the jail (`mount` kind → `/ctx/…`).** *Rejected as the transition.*
  It is expressible — a `mount`'s source is home-relative, so `.claude/skills/synced` is a legal
  `host` — but a `mount` makes a tree *visible* at a `/ctx` path; it does not put skills where an
  agent loads them, it is `:ro` with no composition, and it is dropped entirely on Apple
  Container ([`../reference/pack-system.md`](../reference/pack-system.md#reads-host-and-mount)).
  Useful for letting an agent *look at* the tree; not a delivery path.
- **A `host_files` entry naming the bucket as a directory source.** *Rejected as the transition,
  and it is the strongest alternative on this list* — strong enough that the reasons matter.
  `host_files` does take a directory (a trailing `/` on the source), bind-mounts it `:ro` under
  `/ctx/host-user/` and recursively copies it into the destination, and in the implicit `copy`
  mode it re-renders **every boot** — so alone among the options it is evergreen for free, with
  no snapshot, no record and no drift report. What it cannot do is any of the rest of this
  design: it delivers the vendor's tree *verbatim*, bucket directory and all
  ([§5](#5-naming-and-collisions) exists to drop that name), it has no collision answer at
  either notch, it records no provenance, and in-jail edits are silently discarded on the next
  boot. And its destination would land inside the `:ro` skills mount the same pack already
  declares — **an interaction nothing in the tree checks and nobody has measured.** An
  unverified mount interaction is not a foundation, but this is the route to reach for if
  [OQ-ST4](#OQ-ST4) rules that evergreen matters more than naming.
- **Two-way sync — yolo writes accepted changes back into the sync root.** *Rejected, and this
  is the one to keep rejecting.* It makes yolo a second writer of a vendor's tree, which is the
  defect this whole doc is about, pointed the other way.

## 9. Risks

| Risk | Mitigation |
| :--- | :--- |
| **R1.** The fence lands and the recovery in [§7](#7-homes-that-are-already-wrong) does not, leaving existing broken homes frozen instead of fixed | They ship together or the fence ships with a loud report naming the local-pack entry; a fence alone makes the damage permanent by making it unreachable |
| **R2.** A pack declares a reserved child that is not one, silently hiding a real user skill from adoption | A reserved child is reported by name at every apply that meets one — the fence is disclosed, not quiet |
| **R3.** The snapshot's copies rot and the user believes they are current | [§4.3](#43-the-drift-report--the-whole-update-story) reports every changed item wherever yolo already reads the host home; silence means nothing changed, never "nothing was checked" |
| **R4.** Org-authored synced content reaches every jail through the local pack, with symlinks dereferenced | Escaping links refused before the copy ([§6](#6-degenerate-inputs)); and the snapshot is explicit, so the content arrives because the user asked for it by name |
| **R5.** The identity UUIDs leak into jails as directory names | The bucket name is dropped by construction ([§5](#5-naming-and-collisions)) — only item names travel |
| **R6.** A second vendor ships a sync root and nobody notices | P2's pack-declared fence is the only part of this that generalises for free; core needs no change |

## 10. What this does not propose

- **Not a claude.ai sync client.** yolo never downloads, never authenticates, never refreshes.
- **Not a plugin importer.** `yolo pack init --from-plugin` exists and is the route for a synced
  plugin ([§6](#6-degenerate-inputs)).
- **Not a fix for `S5`.** The jail still resolves a skill-name collision silently; this design
  must not add a fourth path that does.
- **Not a change to the jail's skills composition.** [§3.1](#31-in-a-jail-nothing-and-that-is-right)
  is correct as it stands.
- **Not G32.** G32 asks whether the literal `~/.claude/skills` path should reach the *jail*.
  This doc **sits beside it and does not answer it**: everything here is host-notch, and its
  ruling holds whichever way G32 goes. What this doc does change about G32 is its premise —
  G32 says "two routes already reach the outcome" and treats the host tree as inert, and
  [§3.2](#32-on-the-host-yolo-eats-it--measured) shows one part of it is not inert at all.
- **Not a general "import anything into a pack" facility.** The unit is a synced skill, from a
  declared sync root, and nothing else.

## 11. What I would build, in order

Prose, not tickets; the sketch carries the file map.

1. **The fence**, with its declaration on the agent pack and its report line. It stands alone,
   it is the part that stops ongoing damage, and nothing below depends on any ruling.
2. **The recovery** in [§7](#7-homes-that-are-already-wrong) — ship it with the fence (R1), even
   if it is only the report half at first.
3. **Rule [OQ-ST1](#OQ-ST1).** The snapshot cannot be written until its destination is chosen.
4. **The snapshot and its record**, then the drift report on top of the record.
5. **The corpus:** [`../../AGENTS.md`](../../AGENTS.md)'s skill-priority paragraph gains the fence; G32's row is re-read
   against [§3.2](#32-on-the-host-yolo-eats-it--measured);
   [`../reference/pack-system.md`](../reference/pack-system.md#skills) gains the reserved-child
   rule beside the tier rule.

## 12. What done looks like

Observable by a human, not by a test name:

- A home with `~/.claude/skills/synced/<bucket>/pdf-tools/` runs `yolo host apply --assert`
  twice, with an upstream edit in between. **The edit survives**, and neither run offered to
  adopt anything called `synced`.
- The same run prints one line saying the sync root exists and yolo composed around it.
- A home whose local pack already contains `skills/synced/` is told so by name, with the
  command that puts it back, and nothing moves until that command is run.
- After snapshotting `pdf-tools`, `ls ~/.config/yolo-jail/local/skills` shows `pdf-tools` and
  no bucket directory; `ls ~/.claude/skills/synced/<bucket>` is unchanged.
- The agent inside a jail can invoke that skill; `codex` and `pi` in the same jail get it too.
- An upstream edit to `pdf-tools` afterwards produces one `changed` line at the next host apply
  and **no** write on either side.
- A user in two orgs with a differing `review` in each gets both, suffixed, with a warning
  naming both buckets — and a user whose two orgs ship identical `review` gets one copy and no
  warning at all.

## 13. Open Questions

Searched before they were opened, for a ruling that already settles any of them:
[`workspace-skills.md`](workspace-skills.md#11-open-questions),
[`../reference/pack-system.md`](../reference/pack-system.md#why-its-this-way),
[`../plans/setup-support-gaps.md`](../plans/setup-support-gaps.md) (G32) and
[`../plans/BACKLOG.md`](../plans/BACKLOG.md) ([`S5`](../plans/BACKLOG.md#-s5--a-jail-resolves-a-skill-name-collision-silently)),
plus a corpus-wide search for a prior ruling on reserved names or vendor-written trees, which
returned nothing. The rulings that *do* bear on this are cited where they bind —
[§1](#1-verdict-and-the-principles-it-rests-on),
[§4.1](#41-the-fence--the-part-that-is-not-optional) and [§5](#5-naming-and-collisions) — and
none of them answers these four.

1. 💬 **OQ-ST1: Which pack owns a snapshotted skill?** The central ruling, and the one
   everything in [§4.2](#42-the-snapshot--the-transition-itself) waits on. **(a) The
   conventional local pack** — layer 4 already, composes into every destination and every jail
   already, and its union/suffix machinery is the one this design keeps citing; the cost is that
   a vendor-supplied skill becomes indistinguishable from the user's own hand-written ones, and
   dropping the whole corpus later means picking entries out by hand. **(b) A dedicated pack**
   yolo mints for this (one per sync root, or one per bucket) — provenance is the path, dropping
   it is one line in `packs`, and with `skills_tier: namespaced` the skills invoke
   `<pack>:<skill>`, which is the closest thing to the `anthropic-skills:<name>` the user is
   already typing on the host ([§2.2](#22-what-lives-in-a-bucket-and-how-claude-loads-it)). Its
   cost is real too: a namespaced pack changes the invocation name for *every other* agent in
   the jail, and a per-bucket pack puts an org UUID back into a name after
   [§5](#5-naming-and-collisions) worked to drop it. One fact that cuts slightly against (a):
   the local pack is **not auto-created** — `config.LoadPacks` stats
   `~/.config/yolo-jail/local` and an absent one is silent and free — so (a) means the snapshot
   mints a directory in the user's config that they never asked for, while (b) was always going
   to mint something.

   <!-- vantage: oq id=OQ-ST1 leaning="(b), a dedicated namespaced pack — provenance in the path, droppable in one line, and it preserves the two-level name the user already types; (a) is simpler and loses the ability to tell a vendor's skill from your own." -->

   _Leaning:_ **(b)**, named for the sync root rather than the bucket. Provenance-in-the-path is
   what makes the drift report and the recovery honest, and it is the one property (a) cannot
   have at any price. I hold it loosely: (a) is materially less code and reuses machinery that
   is already measured.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-ST2: Is the fence pack-declared, or does core know the name?** P2 says pack-declared,
   and [§8](#8-alternatives-considered) keeps the hardcode as a fallback because the honest
   comparison is three lines against a manifest field, a validator, a launch-side read and a
   refusal. What it decides: whether the second vendor to ship a sync root costs a config field
   or a code change — and whether a *user* can fence a directory of their own at an agent's
   skills dir without yolo shipping a release.

   <!-- vantage: oq id=OQ-ST2 leaning="Pack-declared, per P2 — the hardcode is cheaper today and is the coupling internal/hostskills already refuses by name; a user-settable escape hatch is the part worth arguing about." -->

   _Leaning:_ **pack-declared.** The precedent is right there in `internal/hostskills`' tier
   comment refusing the identical shortcut. The sub-question I have no strong view on is whether
   a *user* may add reserved children in their own config.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 **OQ-ST3: Is an untransitioned sync root announced, and where?** Day one for every new
   user is "nothing reaches the jail", and [§3.1](#31-in-a-jail-nothing-and-that-is-right) keeps
   it that way. The question is whether yolo *says* so. **(a) Host-notch only** — the apply
   report and a listing verb, both already reading the host home. **(b) Also at launch** — the
   launcher is host-side and can stat the directory, so a jail launch could say "N synced skills
   on your host are not in this jail". **(c) Never** — the user knows what they installed.
   Against (b): a launch has no quiet mode by ruling
   ([`OQ-RO3`](../reference/report-tiers.md#why-its-this-way)), so a line added there is a line
   on every launch forever, and this one is not a disclosure of anything yolo *did*.

   <!-- vantage: oq id=OQ-ST3 leaning="(a) host-notch only — a launch line would be permanent by OQ-RO3 and would report an absence rather than an action; the apply report is where the user is already being told what yolo sees in their home." -->

   _Leaning:_ **(a).** The launch stream exists to disclose what yolo did to this jail; "there is
   content elsewhere you did not ask for" is not that, and it cannot be turned off once added.

   **Answer:**
   > _(empty — fill in when decided)_

4. 💬 **OQ-ST4: Does drift ever do more than report?** [§4.3](#43-the-drift-report--the-whole-update-story)
   rules report-only, which makes the transition a deliberate one-way snapshot the user refreshes
   by hand. The alternative is a `--refresh` posture that re-copies every recorded item whose
   digest moved — still explicit, still one command, but it makes yolo something the user can
   forget about, which is also how yolo ends up a second syncer by increments. What it decides:
   whether a user who transitions in January is still running January's skills in June without
   having chosen to. A *yes* to evergreen-at-any-cost has a third answer that needs no snapshot
   at all — the `host_files` route in [§8](#8-alternatives-considered), which re-renders every
   boot and gives up naming, collisions and provenance to get there.

   <!-- vantage: oq id=OQ-ST4 leaning="Report-only, with an explicit refresh verb the report names — never an automatic re-copy, and never on a launch or an apply; the line between 'a command that updates' and 'a background syncer' is the trigger, and it should stay the user's keystroke." -->

   _Leaning:_ **report-only, plus an explicit refresh verb the report names in its own output.**
   The distinction that matters is the trigger, not the amount of copying: a verb the user types
   is fine at any size; a copy that happens because an apply ran is the start of the thing
   [§8](#8-alternatives-considered) rejects.

   **Answer:**
   > _(empty — fill in when decided)_
