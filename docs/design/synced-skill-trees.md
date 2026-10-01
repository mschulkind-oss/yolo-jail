---
title: "The sync root is not a skill — graduated"
date: 2026-09-18
status: accepted
stage: GRADUATED
next: "Nothing is owed. One sub-question of OQ-ST2 is unruled and gates nothing: may a user add reserved children in their own config? Delete this file once that has a home and the verified facts below are moved or no longer cited"
tags: [design, skills, packs, host-notch, claude, adoption, trust, graduated]
summary: "A stub. The fence and the notice graduated on 2026-10-01 into docs/reference/pack-system.md's reserved-children rules: a pack-declared fence that keeps yolo from composing inside another tool's tree, a one-line notice when such a tree holds content, the shipped name withheld, and the report for a home an earlier apply damaged, with OQ-ST2, ST3, ST-N2, ST-R, the principles and the notice ruling in its why-appendix. What stays here: the verified account of Claude Code's sync root, OQ-ST2's one unruled sub-question, and the dissolved ST1 and ST4 and the config-ownership decision ST5."
vantage:
  status-chip: true
---

# The sync root is not a skill — graduated

**Status:** 2026-10-01 — everything this design ruled is built (2026-09-22, the notice compressed
2026-09-28), and its settled body is now
[`pack-system.md`'s reserved-children rules](../reference/pack-system.md#skills), which are the
authority, with [`OQ-ST2`](../reference/pack-system.md#oq-st2), [ST3](../reference/pack-system.md#st3),
[ST-N2](../reference/pack-system.md#st-n2), [ST-R](../reference/pack-system.md#st-r), the principles
[ST-P1](../reference/pack-system.md#st-p1) and [ST-P3](../reference/pack-system.md#st-p3), and
[the notice ruling](../reference/pack-system.md#st-transition) in its why-appendix. UNMEASURED: no
apply over a non-empty `synced/` bucket on a real host is recorded; the fence and the notice are
pinned by tests that drive a real apply over fixtures.

**What stays here:**

- **[What the synced tree actually is](#2-what-the-synced-tree-actually-is--verified)**, verified
  against Claude Code's binary and a live tree, which other documents and a runbook cite. It is a
  record of a vendor's behavior on the dates it gives; where the tree has moved, the reference wins.
- **[OQ-ST2](#OQ-ST2)'s unruled sub-question**: may a *user* add reserved children in their own
  config? It gates nothing, and nothing has ruled it.
- **The [ledger](#15-decision-ledger)'s rows that have no other home**: the dissolved ST1 and ST4,
  and ST5, a config-ownership decision filed here.

The measured loss before the fence, the worked migration through `enabledPlugins`, the
alternatives and the build order are in git history
(`git log --follow -- docs/design/synced-skill-trees.md`), and their transcripts in
[`synced-skill-trees-plan.md`](synced-skill-trees-plan.md).

**Needs your ruling:** nothing owed; see the sub-question above.

| Was | Now |
| :--- | :--- |
| <a id="1-verdict-and-the-principles-it-rests-on"></a>Section 1, the verdict and principles P1 to P5 | [ST-P1](../reference/pack-system.md#st-p1) and [ST-P3](../reference/pack-system.md#st-p3); P2 is [`OQ-ST2`](../reference/pack-system.md#oq-st2), and P4 and P5 described the deleted snapshot |
| <a id="3-what-yolo-does-with-it-today"></a>Section 3, what yolo did with the tree: <a id="31-in-a-jail-nothing-and-that-is-right"></a>3.1 in a jail, <a id="32-on-the-host-yolo-eats-it--measured"></a>3.2 on the host, measured, <a id="33-why-nothing-caught-it"></a>3.3 why nothing caught it | [ST-P1](../reference/pack-system.md#st-p1) records the measured loss; the transcript is [the plan's](synced-skill-trees-plan.md) |
| <a id="4-the-components--and-the-two-this-design-no-longer-has"></a>Section 4: <a id="41-the-fence--the-part-that-is-not-optional"></a>4.1 the fence, <a id="42-the-notice--what-replaces-the-transition"></a>4.2 the notice, <a id="43-what-was-deleted-with-the-snapshot-and-why"></a>4.3 the deleted snapshot | [the reserved-children rules](../reference/pack-system.md#skills), [ST-N2](../reference/pack-system.md#st-n2), [the notice ruling](../reference/pack-system.md#st-transition) |
| <a id="5-naming-and-collisions--mostly-moot-now-and-the-part-that-is-not"></a>Section 5, naming and collisions; <a id="6-degenerate-inputs"></a>section 6, degenerate inputs | [the reserved-children rules](../reference/pack-system.md#skills) |
| <a id="7-homes-that-are-already-wrong"></a>Section 7, homes that are already wrong | [a home an earlier apply already took from](../reference/pack-system.md#a-home-an-earlier-apply-already-took-from), [ST-R](../reference/pack-system.md#st-r) |
| <a id="8-a-worked-migration-state-already-in-a-file-yolo-is-about-to-own"></a>Section 8, the worked migration through `enabledPlugins`: <a id="81-the-tree-is-safe-the-switch-is-not"></a>8.1, <a id="82-what-yolo-does-to-it-today--measured"></a>8.2 measured, <a id="83-how-to-check-your-own-case-before-the-first-apply"></a>8.3, <a id="84-what-to-do-today-when-the-dry-run-names-something"></a>8.4, <a id="85-what-the-migration-becomes-once-the-record-exists"></a>8.5, <a id="86-what-is-not-covered-and-what-stays-lost"></a>8.6 | git history, and [the plan's fixture](synced-skill-trees-plan.md#the-enabledplugins-measurement); since 2026-09-22 yolo writes no `enabledPlugins`, and the adoption rule it led to is [ST5](#ST5) |
| <a id="9-alternatives-considered"></a>Section 9, alternatives; <a id="10-risks"></a>section 10, risks; <a id="11-what-this-does-not-propose"></a>section 11, what this does not propose; <a id="12-what-i-would-build-in-order"></a>section 12, the build order; <a id="13-what-done-looks-like"></a>section 13, done | git history; the fence is [`OQ-ST2`](../reference/pack-system.md#oq-st2) |
| <a id="OQ-ST3"></a>The notice at the host only | [ST3](../reference/pack-system.md#st3) |
| <a id="ST-N"></a>The notice; <a id="ST-N2"></a>the notice, compressed | [ST-N2](../reference/pack-system.md#st-n2) |
| <a id="ST-R"></a>The recovery | [ST-R](../reference/pack-system.md#st-r) |
| <a id="OQ-ST5"></a>What a regenerated table does with keys yolo did not write | [ST5](#ST5) below |

## 2. What the synced tree actually is — verified

The user's report was *"appears to come from installing a Claude plugin."* That is the natural
reading and it is wrong in the way that matters: the directory is not per-plugin, so nothing
about it is per-install.

The layout facts in this section were read out of the `claude` binary installed in this jail on
2026-09-18 and re-checked against two later releases of it on 2026-09-22 — the bucket module is
unchanged — and the ones in
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
tree, and it is not the same one for the two roots. **The skills root is filled by Claude Code's
claude.ai skills sync**: the skills enabled on the claude.ai account or organization, fetched
server-side about every ten minutes and removed when disabled there (the `syncClaudeAiSkills`
setting, whose only honored value is `false`; organization policy `allow_account_skills_sync`).
Read out of the installed Claude Code 2.1.283 binary on 2026-09-28 — its setting description names
`~/.claude/skills/synced` — and corrected here: this paragraph used to name
`~/.claude/plugins/installed_plugins.json` plus `known_marketplaces.json`, which are the
registration of ordinary plugin installs and belong to the *plugins* root (its `syncClaudeAiPlugins`
twin writes `~/.claude/plugins/synced`). `claude plugin list` shows a "Synced from claude.ai"
section; it is not a view of the skills root, and on the maintainer's machine it printed "No plugins
installed" beside a non-empty one. Either way the bucket directory itself is minted from the
identity, and its *contents* arrive from the sync — both halves are true, and reading either one as
the whole story gets the lifecycle wrong.

That single property does more work in this design than anything else measured
([§4.2](#42-the-notice--what-replaces-the-transition), [§4.3](#43-what-was-deleted-with-the-snapshot-and-why),
[OQ-ST4](#OQ-ST4)): **the source heals itself, and yolo's copy does not.**

> [!NOTE]
> **Not verified here, and this jail cannot verify it:** when the *skills*-side root first
> appears. `~/.claude/skills` in a jail is a `:ro` bind mount of yolo's own staging directory, so
> a vendor process could not create `synced` under it whatever it wanted to do — its absence in
> this jail is explained by the mount and proves nothing about the vendor's behaviour.

## 14. Open Questions

1. ✅ <a id="OQ-ST2"></a> **OQ-ST2: Is the fence pack-declared, or does core know the name?**

   <!-- vantage: oq id=OQ-ST2 -->

   **Answer (2026-09-20):**
   > **Pack-declared, per P2.** Hardcoding `synced` into core is cheaper today and is exactly the
   > coupling `internal/hostskills`' tier comment already refuses by name — core learning one
   > vendor's directory name is how core learns what an agent is, one string at a time.

   ⚠ **The sub-question is explicitly NOT ruled and is the part worth arguing about:** may a
   *user* add reserved children in their own config? A user-settable escape hatch is a different
   question from where the shipped name is declared, and nothing here forecloses it.

   ⚠ It gates nothing either way: the fence shipped without it.


## 15. Decision ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="OQ-ST1"></a>**ST1** | ~~Which pack owns a snapshotted skill?~~ **DISSOLVED.** There is no snapshot, so nothing is copied into a pack and nothing owns a copy. This was the central ruling of the deleted design | 2026-09-20 | [§4.3](#43-what-was-deleted-with-the-snapshot-and-why) | n/a |
| <a id="OQ-ST4"></a>**ST4** | ~~Does drift ever do more than report?~~ **DISSOLVED.** There is no drift report — yolo holds no copy, so there is nothing to compare against | 2026-09-20 | [§4.3](#43-what-was-deleted-with-the-snapshot-and-why) | n/a |
| <a id="ST5"></a>**ST5** | *Implementation decision* (2026-09-30), answering [OQ-ST5](#OQ-ST5). **In a table its derive declares in full (`ctx.in_full`), a key the derive did not write is dropped at a first-migration adoption, and the adoption archive is the way back** — option (c), which is the tree's behavior since [`CO13`](config-ownership-and-promotion.md#co13--how-a-derive-says-it-fills-a-computed-table-in-full--decided) was built. Why: on a first migration nothing records what an earlier yolo wrote, so that key is as likely yolo's own stale output as the user's. (a) would keep a removed MCP server and make the declaration inert at adoption; (b) would refuse over yolo's own leftovers. What a user can feel is which tables are declared, and the contested class, the provider catalogs, is [`OQ-CO16`](config-ownership-and-promotion.md#oq-co16)'s. Recorded here because the question was filed here; it belongs to the config-ownership axis | 2026-09-30 | [OQ-ST5](#OQ-ST5) | ✅ nothing to build: `dropComputedTables` takes a declared table whole and `entrypoint.archiveAdoption` copies the file first |
