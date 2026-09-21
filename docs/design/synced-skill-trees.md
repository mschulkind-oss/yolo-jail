---
title: "The sync root is not a skill"
date: 2026-09-18
status: in-review
tags: [design, skills, packs, host-notch, claude, adoption, trust]
summary: "~/.claude/skills/synced/<uuid>_<uuid>/ is a sync root, not a skill: a bucket named after the user's Anthropic identity, filled by Claude Code, and regenerated from a registration that lives outside it. yolo has never heard of it, so `yolo host apply` reads it as a single hand-written skill named `synced`, moves the whole tree into the user's local pack, and thereafter reverts every update the syncer pushes — measured, including for an empty bucket. A transition path for such a user therefore starts with a fence, not a copier."
vantage:
  status-chip: true
---

# The sync root is not a skill

**Status:** DESIGN, 2026-09-20 — **the transition is a NOTICE, not a mechanism**, and that
ruling DELETED most of this design. Nothing built; two questions remain
([OQ-ST2](#OQ-ST2) — the fence's declaration site, and [OQ-ST5](#OQ-ST5), which belongs to the
config-ownership axis rather than this one). [OQ-ST1](#OQ-ST1) and [OQ-ST4](#OQ-ST4) DISSOLVED
with the snapshot and the drift report; [§4.3](#43-what-was-deleted-with-the-snapshot-and-why)
records what went and why. The fence — the fix for the measured data loss — is untouched and
was always separable.
[§2.4](#24-two-sync-roots-one-bucket-name-and-only-one-is-exposed),
[§3.2](#32-on-the-host-yolo-eats-it--measured) and [§8](#8-a-worked-migration-state-already-in-a-file-yolo-is-about-to-own)
are **MEASURED**; everything else is read from the tree or from the vendor's binary, dated
where it is claimed.

> **In short.** `~/.claude/skills/synced/<uuid>_<uuid>/` is a **sync root**: a bucket named
> after the user's Anthropic identity, filled by Claude Code, and **regenerated from a
> registration that lives outside it**. yolo cannot own a directory like that, so a transition
> path for a user who has one starts with a fence, not a copier.

**Why it matters.** yolo has never heard of it, so `yolo host apply` takes it as a
hand-written skill named `synced`. Measured: the second apply **deleted a newly synced skill
and reverted an edited one**, reporting `Applied: 1 composed skill.`

**The shape.** A pack-declared **fence** that stops yolo composing inside another tool's tree,
and a **notice** when it finds a non-empty one — naming the path, and pointing at the pack
documentation. yolo copies nothing, moves nothing and tracks nothing.

**Cost.** A user who wants that content in a jail does the copying themselves. That is the
deliberate trade: the snapshot that would have done it for them is
[deleted](#43-what-was-deleted-with-the-snapshot-and-why), because the population needing it is
probably nobody and a mechanism nobody needs is a standing obligation.

**Start at [§3](#3-what-yolo-does-with-it-today)** — what happens today is the design.

**Needs your ruling:** [OQ-ST5](#OQ-ST5) only — and it belongs to the config-ownership axis
rather than this one, so it should be ruled beside
[`CO13`](config-ownership-and-promotion.md#13-decision-ledger). ST1 and ST4 dissolved with the
snapshot; ST2 and ST3 were ruled 2026-09-20. ⚠ **P3's justification was corrected the same day**:
it is mode-dependent and fails at `host_management: none`, so the principle now rests on the
host-read boundary instead.


**Reads with:** [`synced-skill-trees-plan.md`](synced-skill-trees-plan.md) (the implementation
sketch, and the measurement transcript), [`workspace-skills.md`](workspace-skills.md) (the same
composition one scope down), [`../reference/pack-system.md`](../reference/pack-system.md#skills)
(the `skills` kind), [`../plans/setup-support-gaps.md`](../plans/setup-support-gaps.md) (G32 —
[§11](#11-what-this-does-not-propose) says how they relate).

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
- **P3. The jail never reads a host skills tree — but NOT for the reason first given.** The
  deleted `SkillTarget.HostSource` is the cautionary tale
  ([`../../internal/jailcontent/skills.go`](../../internal/jailcontent/skills.go) states it where
  the field was): a jail reading `~/.<agent>/skills` found yolo's own generated output, not the
  user's tree.

  ⚠ **That justification is MODE-DEPENDENT, and it fails at one of the three modes.**
  `host_management` has three values (`none`, `assert`, `own` —
  [`config.KnownHostManagements`](../../internal/config/hostmanagement.go)), and the circularity
  only exists where yolo composes into that tree. At **`none`** yolo does not write the real
  `$HOME` at all, so `~/.<agent>/skills` is the user's genuine hand-written tree and reading it
  would NOT be reading yolo's output. The principle as originally stated is therefore over-broad:
  it asserts *never* on the strength of a reason that holds at `own`, weakens at `assert`, and
  evaporates at `none`.

  **P3 survives anyway, on a reason that does not depend on the mode: it is a HOST READ.** A jail
  reading `~/.<agent>/skills` pulls host-home content across the boundary, and yolo already has
  exactly one channel for that — a declared `host_files` grant, disclosed by name in the launch
  banner ([`AGENTS.md`](../../AGENTS.md)). A skills layer that read the host home silently would
  be a second, undisclosed channel for the same capability, which is the thing the banner exists
  to prevent. So: host material crosses by the host notch composing it into a pack, or by a
  declared and disclosed grant — and by nothing else.

  ⚠ Stated this way because the two reasons have different consequences. If anyone later wants a
  jail to read a host skills tree at `none`, the circularity objection does not stand in their
  way; the disclosure requirement does, and it is satisfiable rather than fatal.

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

The layout facts in this section were read out of the `claude` 2.1.275 binary installed in this
jail on 2026-09-18, and the ones in
[§2.4](#24-two-sync-roots-one-bucket-name-and-only-one-is-exposed) were then measured on disk —
never from vendor docs, following the lesson
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

**Confirmed independently of the parser, on 2026-09-18.** This jail's own `~/.claude` is a
*different home* from the maintainer's — a fresh per-workspace overlay, nothing copied from his
tree — and it carries a bucket with **the identical name he reported**. Same Anthropic identity
(the OAuth credential is shared into the jail), different home, same directory name: the name
is derived from the identity and from nothing on disk. One machine and two homes is weaker
evidence than two machines would be, and it is still the only thing that could have produced
that match.

> [!NOTE]
> **The bucket is minted by identity alone, before anything is installed or synced into it.**
> Measured here: the bucket directory is **empty**, and `~/.claude/plugins/installed_plugins.json`
> reads `{"version": 2, "plugins": {}}`. So "it appeared, therefore something was installed" is
> the wrong inference in both directions — and a user who deletes the directory gets it back,
> because the registration that regenerates it lives elsewhere
> ([§2.4](#24-two-sync-roots-one-bucket-name-and-only-one-is-exposed)).
> **Still unverified:** whether a user switching organizations accumulates buckets or replaces
> one. Both readings produce "one or more buckets, each possibly holding a same-named skill",
> which is all any decision below rests on.

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

### 2.4 Two sync roots, one bucket name, and only one is exposed

There are **two** sync roots under `~/.claude`, and they carry the same bucket name:

| Path | Holds | Is it a yolo-composed destination? |
| :--- | :--- | :--- |
| `~/.claude/skills/synced/<bucket>/` | synced **skills** | **Yes** — `packs/claude` composes `.claude/skills`. This is the exposure in [§3.2](#32-on-the-host-yolo-eats-it--measured) |
| `~/.claude/plugins/synced/<bucket>/` | installed/synced **plugins** | **No**, and since 2026-09-20 nothing yolo runs writes under `.claude/plugins` at all. No pack composes it; the `claude_plugins` hook that used to reconcile it in-jail against the configured LSP servers never wrote *here* either, and is now retired ([`pi-pack-extensions.md`](pi-pack-extensions.md) [`OQ-2`](pi-pack-extensions.md#10-decision-ledger)) |

Measured in this jail on 2026-09-18: the plugins-side root exists and holds an empty bucket,
beside a **zero-byte marker file** named `.bucket-<same uuids>` — the `.bucket-` prefix is a
constant in the same binary module as the bucket parser. The marker is excluded from adoption
twice over: it is dot-prefixed *and* not a directory.

**The tree regenerates.** The registration that brings it back after a delete is not in the
tree: it is `~/.claude/plugins/installed_plugins.json` plus
`~/.claude/plugins/known_marketplaces.json` (here, the `claude-plugins-official` GitHub
marketplace with an `installLocation` and a `lastUpdated`), and the bucket directory itself is
minted from the identity with those empty. So the *bucket* is identity-minted and its *contents*
arrive from a plugin install or a skills sync — both halves are true, and reading either one as
the whole story gets the lifecycle wrong.

That single property does more work in this design than anything else measured
([§4.2](#42-the-notice--what-replaces-the-transition), [§4.3](#43-what-was-deleted-with-the-snapshot-and-why),
[OQ-ST4](#OQ-ST4)): **the source heals itself, and yolo's copy does not.**

> [!NOTE]
> **Not verified here, and this jail cannot verify it:** when the *skills*-side root first
> appears. `~/.claude/skills` in a jail is a `:ro` bind mount of yolo's own staging directory, so
> a vendor process could not create `synced` under it whatever it wanted to do — its absence in
> this jail is explained by the mount and proves nothing about the vendor's behaviour.

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

The scan tests only *non-dot directory*, so **an empty sync root is adopted too.** Measured
against the exact shape this jail holds — one empty bucket directory beside a zero-byte
`.bucket-…` marker, nothing synced into it ever:

```console
$ HOME=$FAKE yolo host apply
  ⚠ 1 skill in your agent skill dirs is yours, not yolo's, and would move into your local pack: synced
```

So the exposure is not limited to hosts with content in the tree. Every host whose Claude Code
has minted a bucket is in it.

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

## 4. The components — and the two this design no longer has

> [!IMPORTANT]
> **RULED 2026-09-20: the transition is a NOTICE, not a mechanism.** [§4.2](#42-the-notice--what-replaces-the-transition)'s
> snapshot verb and [§4.3](#43-what-was-deleted-with-the-snapshot-and-why)'s drift report are
> **deleted**. yolo fences the sync root, says so when it finds a non-empty one, names the command
> that lists what is in there, and points at the documentation for adding a skill to a pack of
> your own. It copies nothing, moves nothing, and tracks nothing.
>
> **Why the big half went away.** The snapshot existed to carry a user across a transition, and
> the population needing that carry is *probably nobody*: `~/.claude/skills/` does not exist at all
> on the maintainer's own host (measured 2026-09-20), which is the machine that motivated this
> document. A transition mechanism, its ownership question, its content digests and its update
> story are a large standing cost — every one of them a thing to keep true — bought for a user
> base that has not been shown to exist. A notice costs one code path and is **strictly better**
> for the one user who does turn up, because it tells them the truth and leaves their tree alone.
>
> Two open questions dissolved rather than being answered ([OQ-ST1](#OQ-ST1), [OQ-ST4](#OQ-ST4)),
> and a third ([OQ-ST3](#OQ-ST3)) stopped being a detail and became the deliverable.



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

### 4.2 The notice — what replaces the transition

One report line, emitted where a user is already looking, when **and only when** a reserved child
exists and is non-empty. It states three things and does nothing:

1. **The fact.** The named directory belongs to another tool, yolo composes around it, and
   nothing yolo does will read, write, move or archive it.
2. **How to look.** The path itself, plus `claude plugin list` for the plugins-side root — the
   user's own tool is the authority on its own tree, and yolo does not parse it.
3. **What to do if they want that content in a jail.** A pointer to the pack documentation for
   adding a skill to a pack of their own. Not a command that does it for them.

**It is a notice, not a refusal, and the distinction is deliberate.** The ruling asked for "an
error"; what ships is loud rather than fatal, for the reason [§4.1](#41-the-fence--the-part-that-is-not-optional)
already gives — refusing the apply would turn a vendor feature into a yolo outage, and a user
with a sync root they do not care about must still be able to run `yolo host apply`. A line they
can read and ignore is the strongest thing that does not break them.

**An empty reserved child produces nothing.** The plugins-side root is empty on the measured
machine and ships a zero-byte `.bucket-` marker ([§2.4](#24-two-sync-roots-one-bucket-name-and-only-one-is-exposed)),
so a notice keyed on existence rather than content would fire for everyone, forever, about
nothing. That is the cried-wolf line that teaches a reader to skip yolo's output.

### 4.3 What was deleted with the snapshot, and why

Kept as a record because a doc that removes a design should name what it removed — and because
each of these is a cost a future proposal would re-incur:

- **The snapshot verb** — a user-invoked copy of chosen skills into a yolo-owned pack, with a
  record of source bucket, item name and content digest per item.
- **The drift report** — the whole update story, comparing those digests against the live sync
  root so a re-run was an update rather than a duplicate.
- **The ownership question** it forced: which pack receives a snapshotted skill
  ([OQ-ST1](#OQ-ST1)), which was the central ruling of the deleted design.
- **The standing obligation** that a forked copy is stale by default — the cost
  [§1](#1-verdict-and-the-principles-it-rests-on) stated plainly rather than pretending to solve.

**What survives is the part that was never optional**: the fence
([§4.1](#41-the-fence--the-part-that-is-not-optional)), which stops yolo composing inside another
tool's tree, and which is separable from every question the deleted design raised. The fence is
the fix for the measured data loss; the snapshot was only ever the migration path afterwards.

## 5. Naming and collisions — mostly moot now, and the part that is not

**RULED 2026-09-20: most of this section dissolved with the snapshot.** Four collision rulings
existed because a *copied* skill had to land somewhere and could clash there. yolo copies nothing
now, so nothing lands and nothing clashes.

What survives is a fact about the tree rather than a rule of this design, and it survives because
the notice has to be honest about it: **the three collision regimes do not agree with each other.**

| Where | Rule today |
| :--- | :--- |
| claude.ai sync root | `~g2` / `~g3` suffix within one bucket; **first copy wins across buckets** for plugin names |
| yolo, host notch | A skills name collision between two **packs** is **FATAL** at apply ([`../reference/pack-system.md`](../reference/pack-system.md#skills)) |
| yolo, jail notch | **Silent last-writer-wins**, the local pack last ([`S5`](../plans/BACKLOG.md#-s5--a-jail-resolves-a-skill-name-collision-silently)) |
| `copilot` | Namespaced for *invocation*, **deduplicated by BARE name** — so even a namespaced delivery can be dropped silently |

That disagreement is why [§4.2](#42-the-notice--what-replaces-the-transition)'s notice points at
the documentation instead of offering a command: a user adding a synced skill to a pack of their
own is walking into those four regimes, and yolo cannot pick for them without choosing a name on
their behalf. **This design still does not fix
[`S5`](../plans/BACKLOG.md#-s5--a-jail-resolves-a-skill-name-collision-silently)**, and it no
longer adds any path that could.

## 6. Degenerate inputs

Reduced 2026-09-20: every row about what a *snapshot* would take is gone with the snapshot. What
is left is what the fence and the notice must handle.

| Input | Behaviour |
| :--- | :--- |
| No `~/.claude/skills/synced` at all | The fence still applies — the directory can appear at any time, and [§2.4](#24-two-sync-roots-one-bucket-name-and-only-one-is-exposed) shows it comes back after a delete. **No notice**: nothing is there to report |
| Sync root present, **bucket empty** | Fenced, **and no notice** — this is the common host, not an edge (MEASURED as the *unfenced* case in [§3.2](#32-on-the-host-yolo-eats-it--measured)), so a notice keyed on existence would fire for everyone forever about nothing |
| A zero-byte `.bucket-<uuids>` marker beside the bucket | Never a candidate — dot-prefixed *and* not a directory, so excluded twice over. It is also why "the directory exists" is the wrong trigger |
| Bucket present, only `.trash` / `.staging` | Nothing to report; the reserved siblings are never candidates |
| Bucket holding one or more items | Fenced, and **the notice fires** |
| `<org>_unbound` bucket | An ordinary bucket ([§2.1](#21-the-two-uuids-are-an-identity-not-a-plugin-and-a-version)) |
| Sync root is a symlink to elsewhere | Followed for reading; still fenced, still never written |
| The syncer writes while yolo is reading | Harmless. yolo only counts and names; there is no copy to be torn |

⚠ **One row is kept although it now describes nothing yolo does**, because it is the reason the
notice must not grow into a copier: a synced item may contain a **symlink**, and the skills
copiers **dereference** on both notches — an item holding a link to `~/.ssh/config` would become
those bytes in a pack and then in every jail. The same caution
[`workspace-skills.md`](workspace-skills.md) reached for the workspace source.

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

[§2.4](#24-two-sync-roots-one-bucket-name-and-only-one-is-exposed) makes this cheaper than it
looks and says so out loud: the source regenerates, so for most users the sync root will already
have refilled itself by the time they run the recovery, and the local pack's copy is a stale
fork to discard rather than the only surviving copy. It is still not automatic — a user who
never re-authenticated, or whose organization has since turned Skills off (the content is then
in `.trash`, not re-downloadable), holds the only copy there is, and yolo cannot tell those
users apart from the others.

## 8. A worked migration: state already in a file yolo is about to own

A sync root is one instance of a general shape, and a reader who has never enabled a Claude
plugin still has to be able to ask the question this section answers: **what happens to state I
already have, in something yolo is about to own?**

The loss needs two properties together, and neither alone is enough:

1. **yolo owns the container** — a whole directory, or a whole KEY inside a config file.
2. **yolo's own assertion is recorded coarser than yolo knows it.** yolo knows exactly which
   skills it composed and exactly which leaves a derive filled; what it records is *"this
   destination is mine"*, *"this key is mine"*. Everything else inside the container is
   indistinguishable from residue, and residue is what a regenerating render drops.

For the sync root the container is a tree ([§3.2](#32-on-the-host-yolo-eats-it--measured)).
Below, the container is a KEY — a second specimen, in a different file, down a different code
path, losing the same thing for the same missing reason.

**Claude Code plugins are the specimen, not the subject.** A reader who has none should take
the pre-flight in [§8.3](#83-how-to-check-your-own-case-before-the-first-apply) and run it
against whatever they do have; it is not plugin-specific and it names every key of every
surface their packs declare.

### 8.1 The tree is safe; the switch is not

[§2.4](#24-two-sync-roots-one-bucket-name-and-only-one-is-exposed) says no pack composes
`~/.claude/plugins`, and that is true and it is not the whole answer. The plugin **tree** is not
a yolo destination. The **switch that turns a plugin on** is, and it lives in a different file:

- `~/.claude/settings.json`'s `enabledPlugins` — a map of `<plugin>@<marketplace>` to a boolean.
  The corpus's own binary read records the activation gate as `enabledPlugins["<spec>"] === true`,
  accepted from user, flag and policy scope — **never from project scope for a plugin whose source
  is not a plain string**
  ([`../research/agent-config-distribution.md`](../research/agent-config-distribution.md)). The
  USER-scope file in that list is `~/.claude/settings.json`, which is exactly what `packs/claude`
  declares as its `claude/settings` surface.
- `~/.claude/plugins/installed_plugins.json` and `known_marketplaces.json` are the registration
  ([§2.4](#24-two-sync-roots-one-bucket-name-and-only-one-is-exposed)), and yolo writes neither.
  It no longer installs or uninstalls a plugin either: the pack's `claude_plugins` hook, whose
  `installClaudePlugins` diffed the registration against the configured LSP servers and shelled
  out to `claude plugins install|uninstall`, is **retired and removed**
  ([`pi-pack-extensions.md`](pi-pack-extensions.md) [`OQ-2`](pi-pack-extensions.md#10-decision-ledger),
  ruled 2026-09-19 and built 2026-09-20). ⚠ **That sharpens this section rather than closing it.**
  The hook was the only thing that ever made the tree match the switch, so an `enabledPlugins`
  entry yolo writes can now name a plugin nothing installs — the paragraph below turned around,
  with the switch present and the tree never arriving.

**The tree survives; the switch does not.** A user whose `enabledPlugins` entry is dropped still
has every byte of the plugin on disk. What they lost is that it loads — and nothing about the
directory tells them so.

### 8.2 What yolo does to it today — MEASURED

`packs/claude/derive.lua` returns `enabledPlugins` as an OBJECT while filling only the three LSP
plugin ids, and `env` as an object while filling only `ENABLE_LSP_TOOL`. **An object-valued
derive key is a table yolo regenerates in full**, and that is the entire mechanism: the
granularity is the key, and the knowledge is the leaf.

Two code paths, one per host posture, both arriving there:

| `host_management` | What replaces the table | Archive |
| :--- | :--- | :--- |
| `assert` — the DEFAULT | `hostTableKeys` probes the derive for object-valued keys; `regenerateManagedTables` then clears the block and rewrites it from the declared layers alone | **None.** The archive nets ADOPTION, and an `assert` render is `rmw` |
| `own` | The first owned render adopts the file, and `dropComputedTables` strips from the residue every top-level key the computed layer holds as a NON-EMPTY object (an empty one asserts nothing and takes nothing — ruled 2026-09-20) | One, named on the surface's line |

**`assert` is where the reader-trap is.** `config-ref` describes it as *"yolo owns the keys your
packs declare and rewrites only those; every other key in the file is yours and is left byte for
byte"*, which is exactly true and is read as a promise it does not make: `enabledPlugins` IS a
key the packs declare, so a user's entries are not *other keys* — they are leaves inside a
declared one, and the sentence has nothing to say about them. That gap between the granularity a
promise is stated at and the granularity a user reads it at is the general hazard in one line.

None of that is news to this corpus, which is why it belongs here as a *migration* story rather
than a discovery: the class is tabulated in
[`config-ownership-and-promotion.md`](config-ownership-and-promotion.md#632-the-three-classes-adoption-does-not-cover),
and the RULING on it is
[that doc's drop-narrowing ruling](config-ownership-and-promotion.md#the-drop-narrows-again--wholesale-against-computed-is-withdrawn-as-the-general-rule)
— the section, not a routing table. (This line pointed at `roadmap.md`'s row `0b` until
2026-09-20. A doc deferring to a planning row for an ANSWER is backwards: the row schedules
work, the section decides what the work is.)

**Two things that section changes for [§8.2](#82-what-yolo-does-to-it-today--measured)'s measurement, both dated 2026-09-20.**

1. **The leaf-level record this whole section waits on ALREADY EXISTS, in one half.**
   `dropComputedTables`' doc comment used to say closing the case needs *"a leaf-level signal
   this function does not have"*; MEASURED against the shipped pack, it does have one. A
   `ctx.tombstone` decodes to a PRESENT key with a nil value, so the computed table's key set
   IS the set of leaves the derive asserted. What is missing is the OTHER half — whether yolo
   fills the table in full — and that is [`OQ-CO13`](config-ownership-and-promotion.md#13-decision-ledger).
2. **An EMPTY computed table no longer takes anything**, so the `assert`-posture trap above is
   narrower than measured: it needs the derive to be asserting something under that key on that
   boot. With no LSP configured, `claude/settings` asserts nothing under either key.

⚠ **The `assert` row of the table above is UNCHANGED by all of it.** The ruling narrowed
`dropComputedTables`, which is the `own`/adoption path; `hostTableKeys` + `regenerateManagedTables`
answer the same question at the host notch with the same coarse rule and were not touched — and
that probe feeds SENTINEL live tables, so `claude/settings` reports `env` and `enabledPlugins` as
wholesale-owned there regardless of what is configured. The measurement below still reproduces.

Measured 2026-09-19 in this jail with the baked `yolo 0.9.0+45.gd4c0e7e3`, against throwaway
homes and never a live one; the fixture is in
[the sketch](synced-skill-trees-plan.md#the-enabledplugins-measurement).

```console
$ jq -c '.enabledPlugins, .env' $FH/.claude/settings.json
{"my-own-plugin@mkt":true,"another-plugin@mkt":true}
{"MY_HAND_WRITTEN":"keepme"}

$ printf 'y\n' | HOME=$FH yolo host apply --assert
⚠ First apply into this home — the following existing values will be REPLACED by what your packs declare:
  claude/settings …/.claude/settings.json
    enabledPlugins.another-plugin@mkt (dropped — not in your config)
    enabledPlugins.my-own-plugin@mkt (dropped — not in your config)
    env.MY_HAND_WRITTEN (dropped — not in your config)
yolo regenerates the keys it manages wholesale, so anything above that is not in your config is dropped. To KEEP them: declare them under `mcp_servers` in …/.config/yolo-jail/config.jsonc — one entry there reaches every agent — then re-run.
  Proceed and replace the values above? [y/N]
…
  ⚠ 3 of your entries were dropped from 1 agent surface: MY_HAND_WRITTEN, another-plugin@mkt, my-own-plugin@mkt

$ jq -c '.enabledPlugins, .env' $FH/.claude/settings.json
{}
{}
```

**Every entry is named, which the sync-root loss was not — and that difference is the most
useful thing on this page.** [§3.2](#32-on-the-host-yolo-eats-it--measured) is silent deletion;
this is disclosed deletion. A user who reads the prompt can act on it. The gap is that naming is
not netting, and the netting is where the two postures part company.

> [!WARNING]
> **On the default posture the prompt is the only net there is.** `confirmHostLosses` fires on a
> FIRST APPLY, and the one-time adoption archive is taken only when a render ADOPTS — so an
> `assert` render into a home yolo has already applied to has neither. Measured the same day:
> switching an already-applied home from `assert` to `own` dropped an `enabledPlugins` entry
> **with no prompt at all**, named it on the result line *after* the write, and archived the file
> as it stood *then* — by which point yolo's earlier `assert` runs had already emptied `env` out
> of it. The archive is **the file as yolo found it at adoption**, never *the file before yolo*.

In a jail, steady state is safe and the corpus pins it: a plugin enabled inside a jail is
captured into the overlay and re-applied over every later render, which
`TestComposeStatefulSteadyStateKeepsComputedObjectSibling` asserts by name against the same key.
What is not safe is the FIRST render with no baseline to diff against — the same adopting branch,
one notch over, with a boot's stderr instead of a prompt.

### 8.3 How to check your own case, before the first apply

Three steps. Only the second is yolo's, and only the second generalises past this specimen.

1. **Look at the file, before anything.** `jq '.enabledPlugins, .env' ~/.claude/settings.json`,
   and for the other half `jq '.plugins' ~/.claude/plugins/installed_plugins.json`.
2. **Ask yolo.** `yolo host apply` with **no `--assert`** is a dry run that writes nothing and
   names every entry of yours it would drop, per surface, with a summary count. This is the
   pre-flight for the general hazard, not for plugins: it answers the question for every key of
   every surface every selected pack declares.
3. **Keep your own copy** — `cp ~/.claude/settings.json ~/.claude/settings.json.pre-yolo` — because
   under the default posture step 2's answer stops being recoverable the moment you answer `y`.

> [!IMPORTANT]
> **An empty `enabledPlugins` does not mean you never had plugins**, and the maintainer's own case
> is the demonstration precisely because it is inconclusive. The host copy of `settings.json` this
> jail was handed reads `"enabledPlugins": {}`, and `~/.claude/plugins/installed_plugins.json`
> reads `{"version": 2, "plugins": {}}` — and the same host file carries a `permissions` block and
> a `skipDangerousModePermissionPrompt` matching `packs/claude`'s `guarded` autonomy floor, so
> yolo has written it. **An empty table after a render and one that was always empty are the same
> bytes.** That is why step 1 is worth nothing once step 2 has been answered `y`, and it is the
> same argument this design makes for the tree: without a record of what yolo asserted, the
> question stops being answerable rather than getting a wrong answer.

### 8.4 What to do today, when the dry run names something

**The report's own remedy is wrong for this case.** Every dropped table entry is offered the same
line — *declare them under `mcp_servers`* — because `mcpEntryRemedy` is written for the one table
that motivated it, and there is no `mcp_servers` declaration that re-enables a plugin.

**The remedy that does work**, measured under both postures, is a `config-overlay` contribution in
the conventional local pack (`~/.config/yolo-jail/local/pack.json`):

```json
{
  "name": "local",
  "contributes": [
    {
      "kind": "config-overlay",
      "surface": "claude/settings",
      "config": { "managed": { "enabledPlugins": { "my-own-plugin@mkt": true } } }
    }
  ]
}
```

With that in place the same apply prints **no loss line at all** and the entry is in the rendered
file under both `assert` and `own`. It is a declaration, which is the posture the host notch asks
for everywhere else — and it is the same move [§4.2](#42-the-notice--what-replaces-the-transition)
makes for a skill, one kind over. It is also a fork, and the two notches fork differently: at the
host notch the declaration is what `hostTableLayer` writes into the table, so the entry is
re-asserted by every apply until the declaration goes too — uninstall the plugin from the client
and yolo puts the switch back. In a jail a `config-overlay` folds BELOW the capture overlay, so an
in-jail disable is captured and wins instead.

> [!WARNING]
> **`yolo pack lint` will not catch a wrong body.** `managed` is the body's one field, and
> `manifest.DecodeOverlay` refuses `defaults` in its place BY NAME — yet measured the same day,
> `yolo pack lint` reported `✓ pack ok` and `contributes keys (owner still wins)` for exactly that
> body, which contributed nothing and lost the entry at the next apply. Verify the declaration by
> re-running step 2's dry run, never by linting the pack.

### 8.5 What the migration becomes once the record exists

The fence and the snapshot need a record of what yolo put in a tree; this needs a record of which
leaves a derive asserted. **It is the same missing thing at two granularities**, which is why the
specimen belongs in this doc rather than beside it: a design that adds the first and not the
second leaves the identical failure one file over, disclosed instead of silent but equally
unrecoverable.

Once a derive's assertion is recorded leaf by leaf, the drop narrows to it — yolo's LSP toggles
are regenerated, everything else under the key is residue that survives, and
[§8.3](#83-how-to-check-your-own-case-before-the-first-apply)'s pre-flight stops being
load-bearing because nothing is silently at stake. What that record does **not** settle on its own
is what should happen to a leaf yolo has never asserted, which is [OQ-ST5](#OQ-ST5).

> [!IMPORTANT]
> **"Once the record exists" is half past tense as of 2026-09-20, and the sentence it disproves
> is the one above it.** MEASURED at the jail's adoption path: a derive's leaf assertions ARE
> recorded — the computed table's key set is exactly them, tombstones included — so the second
> granularity is not missing in the same way the first is, and the two are no longer symmetric.
> What is missing there is a different record: whether the derive fills the table in FULL, which
> decides whether an unasserted leaf is the user's or yolo's own stale output
> ([`OQ-CO13`](config-ownership-and-promotion.md#13-decision-ledger)). The narrowing this section
> describes has shipped for the one case that needs no such record — a computed table asserting
> NOTHING — and is blocked on it for every other
> ([the drop-narrowing ruling](config-ownership-and-promotion.md#the-drop-narrows-again--wholesale-against-computed-is-withdrawn-as-the-general-rule)).

### 8.6 What is not covered, and what stays lost

- **Formatting and key order.** MEASURED: the `assert` → `own` switch re-encoded the file with its
  keys sorted — no value changed, and `theme` moved from first to last. `config-ref` says that
  switch *"changes ZERO bytes"*; at VALUE granularity it does, and a hand-formatted file still
  does not come back formatted.
- **Comments.** The host report has a class for them and states there is no remedy.
- **A keyless (`raw`/`lines`) surface.** Refused outright at the host notch by [`[OQ-CO9](./config-ownership-and-promotion.md#13-decision-ledger)`](./config-ownership-and-promotion.md#13-decision-ledger) and NOT
  refused in a jail, where its first render replaces the file. No shipped pack declares one today,
  so the class is empty rather than handled.
- **An `assert` render's drop.** There is no yolo-side copy to restore from, by design — the
  archive nets adoption. The user's own copy from step 3 is the only recovery.
- **The plugin tree itself.** Nothing here touches it, and a user who loses only the switch
  re-enables through the client rather than through yolo.

## 9. Alternatives considered

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
  ([§5](#5-naming-and-collisions--mostly-moot-now-and-the-part-that-is-not) exists to drop that name), it has no collision answer at
  either notch, it records no provenance, and in-jail edits are silently discarded on the next
  boot. And its destination would land inside the `:ro` skills mount the same pack already
  declares — **an interaction nothing in the tree checks and nobody has measured.** An
  unverified mount interaction is not a foundation, but this is the route to reach for if
  [OQ-ST4](#OQ-ST4) rules that evergreen matters more than naming.
- **Two-way sync — yolo writes accepted changes back into the sync root.** *Rejected, and this
  is the one to keep rejecting.* It makes yolo a second writer of a vendor's tree, which is the
  defect this whole doc is about, pointed the other way.

## 10. Risks

| Risk | Mitigation |
| :--- | :--- |
| **R1.** The fence lands and the recovery in [§7](#7-homes-that-are-already-wrong) does not, leaving existing broken homes frozen instead of fixed | They ship together or the fence ships with a loud report naming the local-pack entry; a fence alone makes the damage permanent by making it unreachable |
| **R2.** A pack declares a reserved child that is not one, silently hiding a real user skill from adoption | A reserved child is reported by name at every apply that meets one — the fence is disclosed, not quiet |
| **R3.** ~~The snapshot's copies rot and the user believes they are current~~ | **DISSOLVED 2026-09-20** — yolo holds no copy, so nothing of yolo's can rot. The risk moved to the USER, who now maintains their own pack copy knowingly rather than yolo maintaining one on their behalf badly. That is the trade [§4.3](#43-what-was-deleted-with-the-snapshot-and-why) accepted |
| **R4.** ~~Org-authored synced content reaches every jail through the local pack, with symlinks dereferenced~~ | **DISSOLVED 2026-09-20** — nothing reaches a jail through yolo, because yolo copies nothing. ⚠ The dereference hazard itself is NOT dissolved; it is why [§6](#6-degenerate-inputs) keeps the symlink row and why the notice must never grow into a copier |
| **R5.** The identity UUIDs leak into jails as directory names | The bucket name is dropped by construction ([§5](#5-naming-and-collisions--mostly-moot-now-and-the-part-that-is-not)) — only item names travel |
| **R6.** A second vendor ships a sync root and nobody notices | P2's pack-declared fence is the only part of this that generalises for free; core needs no change |

## 11. What this does not propose

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
- **Not roadmap row `0b`.** [§8](#8-a-worked-migration-state-already-in-a-file-yolo-is-about-to-own)
  works that row's hazard through as this design's second specimen and does not build it. The two
  are separable in both directions — the fence ships without a leaf-level record, and the record
  ships without a fence — and they are in one doc because they are the same missing thing at two
  granularities, not because either waits on the other. The ruling [§8](#8-a-worked-migration-state-already-in-a-file-yolo-is-about-to-own)
  does add here is [OQ-ST5](#OQ-ST5).

## 12. What I would build, in order

Prose, not tickets. **Three steps, down from five** — the two that were the snapshot and its
blocking ruling are gone ([§4.3](#43-what-was-deleted-with-the-snapshot-and-why)).

1. **The fence**, with its declaration on the agent pack and its report line. It stands alone, it
   is the part that stops ongoing damage, and it depends on no ruling — including
   [OQ-ST2](#OQ-ST2), which decides only *where* the reserved name is declared, not whether the
   fence exists.
2. **The notice** ([§4.2](#42-the-notice--what-replaces-the-transition)): non-empty reserved child
   only, at the host notch only, naming the path and pointing at the pack documentation. It is
   one code path and it ships with the fence or immediately after.
3. **The recovery** in [§7](#7-homes-that-are-already-wrong) — ship it with the fence (R1), even
   if it is only the report half at first.

Then the corpus: [`../../AGENTS.md`](../../AGENTS.md)'s skill-priority paragraph gains the fence;
G32's row is re-read against [§3.2](#32-on-the-host-yolo-eats-it--measured);
[`../reference/pack-system.md`](../reference/pack-system.md#skills) gains the reserved-child rule
beside the tier rule.

## 13. What done looks like

Observable by a human, not by a test name. **Four conditions, down from seven** — the three that
described snapshotting and drift went with them.

- A home with `~/.claude/skills/synced/<bucket>/pdf-tools/` runs `yolo host apply --assert`
  twice, with an upstream edit in between. **The edit survives**, and neither run offered to adopt
  anything called `synced`.
- That run prints **one** line: the sync root exists, yolo composed around it, here is the path
  and where to read about putting a skill in a pack of your own.
- A home whose bucket is **empty** runs the same command and prints **nothing** about it — this is
  the common host, and a line here would be the one that teaches people to skim.
- A home whose local pack already contains `skills/synced/` is told so by name, with the command
  that puts it back, and nothing moves until that command is run.

## 14. Open Questions

Searched before they were opened, for a ruling that already settles any of them:
[`workspace-skills.md`](workspace-skills.md#11-open-questions),
[`../reference/pack-system.md`](../reference/pack-system.md#why-its-this-way),
[`../plans/setup-support-gaps.md`](../plans/setup-support-gaps.md) (G32) and
[`../plans/BACKLOG.md`](../plans/BACKLOG.md) ([`S5`](../plans/BACKLOG.md#-s5--a-jail-resolves-a-skill-name-collision-silently)),
plus a corpus-wide search for a prior ruling on reserved names or vendor-written trees, which
returned nothing. The rulings that *do* bear on this are cited where they bind —
[§1](#1-verdict-and-the-principles-it-rests-on),
[§4.1](#41-the-fence--the-part-that-is-not-optional) and [§5](#5-naming-and-collisions--mostly-moot-now-and-the-part-that-is-not) — and
none of them answers these five. [OQ-ST5](#OQ-ST5) was searched separately and later: the
adoption-granularity class it sits on IS ruled on, in
[`config-ownership-and-promotion.md`](config-ownership-and-promotion.md#632-the-three-classes-adoption-does-not-cover)
and at `dropComputedTables` itself, and both stop at the same edge — they record the residue as
a known boundary and name the missing signal, neither says what to do with an unasserted leaf
once the signal exists.

1. ✅ <a id="OQ-ST2"></a> **OQ-ST2: Is the fence pack-declared, or does core know the name?**

   **Answer (2026-09-20):**
   > **Pack-declared, per P2.** Hardcoding `synced` into core is cheaper today and is exactly the
   > coupling `internal/hostskills`' tier comment already refuses by name — core learning one
   > vendor's directory name is how core learns what an agent is, one string at a time.

   ⚠ **The sub-question is explicitly NOT ruled and is the part worth arguing about:** may a
   *user* add reserved children in their own config? A user-settable escape hatch is a different
   question from where the shipped name is declared, and nothing here forecloses it.

   ⚠ It gates nothing either way — [§12](#12-what-i-would-build-in-order) step 1 ships without it.

2. 💬 **OQ-ST5: When yolo regenerates a table, what happens to the keys inside it that yolo did not write?**

   *Rewritten 2026-09-20 to stand alone. It had accreted three corrections and read as a diff
   against its own earlier self.*

   **The situation, from scratch.** Some config yolo produces is a TABLE — `enabledPlugins`,
   `mcpServers`, mise's `[tools]`. yolo writes some keys inside it. The user may have written
   others. When yolo regenerates that table, it must decide what happens to the user's keys.

   **Two of the three parts are settled; only the third is open.**

   | | | Status |
   | :--- | :--- | :--- |
   | Can yolo tell which keys it wrote? | **Yes, today.** The computed table's key set IS the asserted set — a tombstone decodes to a present key with a nil value. MEASURED against the shipped `claude/settings` derive | settled |
   | What if yolo wrote NONE of them (the table is present but empty)? | **Preserve.** Regenerating nothing claims nothing, so the user's keys survive. Shipped; it moved two `mise` migration goldens | settled |
   | What if yolo wrote SOME of them? | **Open — this question.** | ⬅ |

   **Why the third is hard, and it is not the reason this question originally gave.** The blocker
   is not "which keys did yolo write" — yolo knows. It is **"does this derive fill this table in
   full?"**, which nothing declares. Preserve-always resurrects a deleted MCP server; drop-always
   destroys a user's own `env` entries. MEASURED: choosing preserve for a non-empty table with no
   such declaration reddens six tests across two packages.

   That missing declaration is [`CO13`](config-ownership-and-promotion.md#13-decision-ledger),
   **decided 2026-09-20** — a third derive sentinel beside `ctx.tombstone` / `ctx.empty_array`.
   So this question is no longer blocked on an unknown; it is waiting on that being built.

   **What is actually being asked, once CO13 exists.** For a table the derive declares it fills
   **in full**, is a key the derive did not write **(a) preserved**, **(b) refused**, or
   **(c) dropped reversibly**?

   <!-- vantage: oq id=OQ-ST5 leaning="(a) preserve, but weaker than it was: the empty-table case already took the part of (a) that was free, so what remains is the genuinely contested half." -->

   _Leaning:_ **(a), and weaker than it looks.** The empty-table case already took the part of
   (a) that was free, so what is left is precisely the contested half — a table the derive claims
   to own completely, with a user key in it. (b) is the honest alternative and this leaning is
   not strong.

   ⚠ **This belongs to the config-ownership axis, not to skills.** It is here because
   [§8](#8-a-worked-migration-state-already-in-a-file-yolo-is-about-to-own) needs it, and it
   should be ruled in [`config-ownership-and-promotion.md`](config-ownership-and-promotion.md)
   beside `CO13` rather than in this doc.

   **Answer:**
   > _(empty — fill in when decided)_


## 15. Decision ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="OQ-ST1"></a>**ST1** | ~~Which pack owns a snapshotted skill?~~ **DISSOLVED.** There is no snapshot, so nothing is copied into a pack and nothing owns a copy. This was the central ruling of the deleted design | 2026-09-20 | [§4.3](#43-what-was-deleted-with-the-snapshot-and-why) | n/a |
| <a id="OQ-ST3"></a>**ST3** | **The sync root IS announced, at the host notch only** — the apply, which is where yolo is about to take the folder over. Not at launch: the launch stream discloses what yolo DID to a jail, "there is content elsewhere you did not ask for" is not that, and by [`OQ-RO3`](../reference/report-tiers.md#why-its-this-way) a line added there is permanent | 2026-09-20 | [§4.2](#42-the-notice--what-replaces-the-transition) | no |
| <a id="OQ-ST4"></a>**ST4** | ~~Does drift ever do more than report?~~ **DISSOLVED.** There is no drift report — yolo holds no copy, so there is nothing to compare against | 2026-09-20 | [§4.3](#43-what-was-deleted-with-the-snapshot-and-why) | n/a |
| **The transition itself** | **A NOTICE, not a mechanism.** yolo fences, says so on a non-empty reserved child, names the path, points at the pack documentation — and copies, moves and tracks nothing. The population needing a carried transition is probably nobody: `~/.claude/skills/` does not exist at all on the maintainer's own host | 2026-09-20 | [§4](#4-the-components--and-the-two-this-design-no-longer-has) | no |
