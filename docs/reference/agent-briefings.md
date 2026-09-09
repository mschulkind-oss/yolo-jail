---
status: current
verified: 2026-09-09
verified_commit: d8cf1cf8
covers:
  - internal/jailcontent/briefing.go
  - internal/jailcontent/skills.go
  - internal/jailcontent/write.go
  - internal/cli/run/prepare.go
  - internal/cli/run/briefingdest.go
  - internal/cli/run/backendcaps.go
  - internal/entrypoint/hostbriefing.go
tags: [briefings, skills, packs, agents-md, staging]
summary: "Where the text an in-jail agent reads at session start comes from: the four composition parts and what varies per destination, the per-destination staging file and its injective name, the read-only mount that makes an in-jail edit fail, and the inode-preserving refresh that lets a live jail see host edits."
---

# Agent briefings — the text an agent reads at session start

**Status:** CURRENT as of 2026-09-09, verified against `d8cf1cf8`.

Every coding agent reads an instruction file at session start. yolo **composes one per
destination**, host-side, on every invocation, stages it under a per-jail directory, and
bind-mounts it read-only where the agent expects it. Which destinations exist is entirely
pack data: a pack's `briefing` contribution names its own `into` path, and a jail with no
packs gets no briefing at all.

Skills ride the same staging area and the same read-only delivery, so they are covered
here too.

| Component | Lives in |
| :--- | :--- |
| Composition of the jail-managed body | `internal/jailcontent` (`BriefingContent`, `BriefingInput`, `confinementHeader`, `enforcementLines`) |
| The three composition steps around it | `internal/jailcontent` (`ComposeBriefing`, `ComposePackBriefings`, `PrependHostBriefing`) |
| The inode-preserving write | `internal/jailcontent` (`WriteBriefing`) |
| Skills staging and its layering | `internal/jailcontent` (`PrepareSkills`, `SetPackSkillDirs`, `SetPackSkillTargets`, `SkillStagingName`, `PackSkillSource`, `SkillTarget`) |
| The per-invocation refresh, and what it feeds the composer | `internal/cli/run` (`refreshJailBriefings`, `briefingPortsFor`, `briefedResourceLimits`, `briefingLoopholes`) |
| The one enumeration of destinations and staging names | `internal/cli/run` (`briefingDestinations`, `briefingStagingName`, `briefingDest`) |
| A pack's prose entries | `internal/cli/run` (`packBriefingProses`) |
| The ownership record that gates the host prepend | `internal/entrypoint` (`GeneratedHostBriefings`, `HostBriefingManifestPath`) |

**Reads with:** [`pack-system.md`](pack-system.md) (the `briefing` and `skills` kinds, and
`agent` / `agents` on a contribution), [`jail-home.md`](jail-home.md#staged-read-only-content)
(how staged content is mounted), [`../design/briefing-audiences.md`](../design/briefing-audiences.md)
(the audience model), [`host-to-jail-handoff.md`](host-to-jail-handoff.md)
(the Handoff section).

---

## The two layers

Agents read instruction files at two levels and yolo treats them completely differently.

| Layer | In-jail path | Who owns it | yolo's role |
| :--- | :--- | :--- | :--- |
| **User-level briefing** | one per `briefing` contribution (`~/.claude/CLAUDE.md`, `~/.codex/AGENTS.md`, …) | yolo | composed per jail, mounted read-only |
| **Project-level file** | an `AGENTS.md` or `CLAUDE.md` at the workspace root | the repository | none — it is a file in the workspace bind, exactly as checked in |

yolo never writes, rewrites or merges the project-level files. Everything below is about the
user-level layer.

**There is no agent registry and no config key naming agents.** Which destinations get a
briefing follows from which packs are selected, and nothing is selected by default.

## What a composed briefing contains

Four parts, in the order they appear in the finished file:

**1. The host user's own briefing, prepended — and only if it *is* the user's.** Driven by
the declaring contribution's `after: "host:<path>"`. If that host file exists **and yolo did
not compose it itself**, its content comes first, separated from the rest by a `---` rule.
This is how a user's global instructions reach every jail *on a machine where the host notch
has never run*. The mapping is filename-exact: a `CLAUDE.md` destination reads `CLAUDE.md`,
and variants like `CLAUDE.local.md` are not picked up.

> [!WARNING]
> **The jail must not read yolo's own host output back in.** Once `yolo host apply` made the
> host destination a file yolo composes *wholesale*, `after: "host:.claude/CLAUDE.md"` named
> yolo's own output: the jail prepended a file already holding every pack's prose and then
> composed the same packs again at part 4, so each pack section appeared **twice**,
> byte-identical. The prepend is gated on **ownership proved from a record** —
> `GeneratedHostBriefings`, reading the same manifest `yolo host apply` writes — never
> inferred from content, because a file that merely *looks* composed is still the user's. It
> **fails open**: an absent or unreadable record prepends as before, because dropping the
> user's instructions from their jail is a worse failure than repeating a pack's prose. On a
> machine that *has* run the host notch, the user's prose arrives through the conventional
> local pack at part 4 instead — the same route the skills half took.

`after: "host:…"` is **jail-only**: at the `yolo host apply` notch the path it names *is* the
generated destination, so the host render ignores it outright. It is no longer origin-gated —
a fetched pack's `after` is honored like anyone else's.

**2. The jail-managed body** — one document describing this specific jail, deliberately
limited to what an agent *cannot* discover through its own mechanisms, with inline manuals
replaced by pointers (`yolo --help`, `yolo config-ref`, `yolo-cglimit --help`) and
conditional sections that appear only when their data exists. Emission order, from
`BriefingContent`:

1. **The confinement header** — see [Confinement](#confinement).
2. **⚠ Provisioning failed** — conditional, when the provisioning log records a failure.
   Refreshed every invocation, so it appears on the next attach after a failed boot.
3. **Handoff** — conditional, when a fresh handover pointer is carried this launch.
4. **Environment** — workspace, home, OS, the network paragraph, the two port sections, and
   the resource limits the backend actually imposes.
5. **The `rg --replace` trap** warning.
6. **What this environment does NOT do for you** — conditional, the backend's own
   limitations. Placed **before** the capability sections deliberately: these are
   constraints that change how everything below them should be read, and a constraint
   discovered after the capability it qualifies has already been read too late.
7. **Loopholes** — conditional, the actual active set by name rather than an instruction to
   enumerate.
8. **Blocked Tools** — conditional, from the blocked-tool config merged with what packs
   contribute.
9. **Additional Context Mounts** — conditional, and filtered to the mounts the backend will
   actually bind.
10. **Limitations**, **Packages & Resource Limits**, **Skills** — the three standing
    sections, the last gaining an extra paragraph in a yolo-jail source tree.

There is no tool inventory and no MCP listing: agents read their own generated config.

**3. `agents_md_extra`**, appended verbatim — a config key for injecting arbitrary extra
instructions into every generated briefing, legal at user or workspace scope.

**4. Each selected pack's prose that this destination's audience admits**, appended last, in
config order, under a `<!-- from pack: NAME -->` provenance header. **The header is not
decoration**: pack prose is *instructions an agent will follow*, and a jail may carry several
packs plus yolo's own briefing plus the user's — so without attribution an agent reading a
surprising rule has no way to find out where it came from, and neither does the human
debugging it. Empty prose is skipped rather than emitting an empty section.

### What the body describes: applied, never configured

Every fact in the jail-managed body is what the launch **applies**, not what the config
asked for. This is one rule with several call sites, and each was a real defect:

- **Network mode** is the applied mode, so a nested jail forced onto host networking is told
  so instead of being told about a bridge it does not have. Both port sections key off the
  same value and are suppressed under host networking, where neither key is honored.
- **Resource limits** come from what the backend imposes, not from the `resources` map — the
  map told an Apple Container jail its pids limit was kernel-enforced when that flag is never
  passed there, and told it nothing about the caps that backend applies by default.
- **Context mounts** are filtered to what the backend binds. A section headed "Additional
  Context Mounts (read-only)" naming paths that were never mounted is the same lie as a
  network mode that was never applied.
- **Loopholes** are gated on the backend as well as on the loophole's own predicates:
  `Honored` has no backend term, so on a backend that starts no host services the unfiltered
  list advertises daemons that do not exist. An agent reading a false capability list does
  not merely lack a feature — it plans around one it does not have.

> [!WARNING]
> **No host probe belongs in the composer.** The fuller truth about networking is one
> `podman info` away, and the temptation arrives exactly here. This function runs on **every**
> invocation including attach, where it is the only work done and runs no subprocess at all —
> and a probe's answer can differ between the launch that started a jail and the attach that
> re-renders its briefing. So the briefing carries the *deterministic* decision only, derived
> from the runtime, the config and a path-existence seam. What the probe decided is already in
> the jail as an environment variable, which the bridge paragraph names.

### Confinement

The briefing opens with a header for the **notch** this environment runs at, and it reads the
notch's *profile*, not just its name. The name picks the title and the framing sentence — that
prose genuinely differs per notch and a human reads it — but the two facts an agent most needs
are **derived**: which primitives actually enforce the boundary, and whether agent autonomy is
on. The vocabulary comes from the same table `yolo describe` prints, so the two human-facing
descriptions of one primitive cannot drift.

That is what makes the header correct for a notch nobody has enumerated yet: an unrecognized
name falls to the default branch and describes its real enforcement vector instead of
asserting a container that may not be there.

> [!WARNING]
> **Never let a branch claim a container it does not have.** An agent told it is in a
> disposable container reasons about its home as throwaway. The `macos-user` backend runs at
> the *jail* notch — the notch dial is a separate axis from the runtime — but has no container
> anywhere in it, and it said "a sandboxed container" until that branch was split out. The
> profile is read for exactly this reason, and a caller that has not resolved a backend must
> not be handed a header that guesses a stronger boundary than it has.

The jail notch's own header bytes are **byte-identical to their historical form** and pinned by
test, deliberately: every jail that boots today renders it, so adding detail there would move a
rendered surface for every existing user to say something the next two lines already say. The
derived enforcement tail is appended on the guest, host and unrecognized paths only.

### Audiences: what varies per destination

**Parts 1–4 are composed per destination, and parts 1 and 4 actually differ.** A `briefing`
contribution may carry an `agents` list — the launcher commands its prose is *for* — and it
then reaches only the destinations whose owning pack declared a matching `agent` identity. A
contribution naming no audience **broadcasts**, which is what every shipped pack does, so the
default composition is unchanged.

**Empty is broadcast** on both sides of the match, and the whole safety of landing the field
ahead of any pack adopting it rests on that: a jail full of packs that name no audience
composes exactly what it did before. A destination that declared no identity still receives
every broadcast and no addressed prose.

**The match is against a declared string, never anything derived.** A content pack names *who*
and never *where*: where an agent reads is that agent pack's business and changes when the
agent changes.

This is why composition happens **inside** the per-destination write loop. Composing once
above it was what made scoping impossible — a pack whose rules applied to one agent had to
broadcast them to all of them or drop them. Two consequences of the move:

- The composition input is one entry **per `briefing` contribution** rather than one text per
  pack, so a pack declaring two contributions with two different `from` files delivers
  **both**. That was a live limit of the jail notch, which the host render never had.
- Identical prose from one pack is composed **once** per destination, with audiences unioned
  and a broadcast absorbing every audience. Two contributions naming no `from` resolve to the
  same conventional file, so without this a pack naming two destinations and no source would
  say everything twice.

## Staging and delivery

Composed files land host-side in the per-jail staging directory, **one file per
DESTINATION**, then bind-mount read-only at that destination:

```
<staging>/briefing-.claude~1CLAUDE.md   →  /home/agent/.claude/CLAUDE.md:ro
<staging>/briefing-.codex~1AGENTS.md    →  /home/agent/.codex/AGENTS.md:ro
```

`briefingDestinations` and `briefingStagingName` are the **one** enumeration of destinations
and the **one** encoding of a staging filename, and both halves of the launch call them: the
refresh composes and writes, the assembler binds. That coupling is structural rather than
conventional for a reason — **a missing bind source for a *file* is not an error the way a
missing directory is**, so a disagreement here does not fail the launch, it produces a jail
whose agent reads a blank briefing.

Destinations are **deduplicated by path, first declaration winning**, because the mount made
that the rule: `briefing` is a concatenating kind — several packs contributing prose at one
path is designed behavior, and the composition merges all of it — but podman rejects a
duplicate mount destination and kills the boot, so exactly one bind per path may be emitted.

A contribution with an empty `into` is dropped, checked rather than assumed: since a
contribution may legally name an audience instead of a path, and the mount half appends the
`into` to the home root, an empty one would bind a single staged file over the jail's entire
home.

> [!WARNING]
> **The staging name must be injective, and the obvious escape is not.** It is RFC 6901's
> JSON-Pointer encoding — `~`→`~0`, `/`→`~1` — because two destinations sharing a staging file
> would deliver one agent's composed briefing to the other, silently. Doubling `~` and mapping
> `/` to `~` was tried and caught by test: it sends both `a/~b` and `a~/b` to `a~~~b`. Two
> escape sequences that cannot prefix each other is what fixes it, which is exactly what
> RFC 6901 chose them for. A hash prefix would be injective only probabilistically. Nothing
> decodes the name — it is a one-way disambiguator, kept readable because the thing being
> debugged is usually "which file did this jail actually mount".

The read-only mount is why an in-jail agent gets `Read-only file system` when it tries to edit
its own briefing: kernel-enforced and intentional. On Apple Container, single-file mounts under
the home are unsupported, so the files are materialized under the workspace state dir instead —
same content, different plumbing. On macos-user there are no mounts at all: the same staged tree
is **copied** over the sandbox home, which is a real difference in kind (the copy is writable)
and is recorded in that backend's launch warning rather than papered over.

## Skills

Skills ride the same staging directory, one subdirectory per `skills` contribution, each
mounted read-only at that contribution's `into`. Staging is **rebuilt every invocation**,
clearing contents *inside* each directory.

**Two layers, in this order:** the built-in skill suite, then every selected pack's skills in
config order. A pack may therefore override a built-in — a legitimate reason to ship one — and
because the conventional local pack is appended last among packs, a personal skill still
outranks every shared pack's.

Each source carries an **audience**, matched against the destination's declared identity by
the same empty-is-broadcast rule the briefing half uses. Without it the source list is global —
every selected pack's skills reach every destination — so an agent-specific skill would be
copied into another agent's tree with nothing able to stop it.

> [!WARNING]
> **There is no third layer reading the host's own `~/.<agent>/skills` tree, and adding one
> back is circular.** That layer named "the user's own skills tree" but was set to the
> *destination* — the host's copy of the very path the staging dir gets mounted over. It was
> right while the destination held loose user files and became circular the moment
> `yolo host apply` **composed** it: the jail read yolo's own generated output back in as the
> user's tree, and since the local pack is an ordinary pack entry, its content arrived twice by
> two routes. Invisible only because a flat copy is last-writer-wins. The slot it described
> already has a home — the conventional local pack is the last pack layer — so a personal skill
> reaches the same precedence by the same route every other pack's content takes.

`PrepareSkills` still takes a home directory and an agent-name list; both are vestigial, kept
because five call sites pass them and churning those would be a bigger diff than the fix with
no behavior in it.

## Refresh — a live jail sees host edits

The refresh runs on **every** `yolo` invocation, fresh launch *and* attach-to-running, so
editing a host briefing file, a skill, or `agents_md_extra` propagates into an already-running
jail the next time any `yolo` command is run against it.

This works only because the refresh **preserves inodes**: the write truncates the existing
file in place, and the skills refresh clears *inside* the staged directories rather than
recreating them. A file→file bind mount is pinned to the inode it captured at container start,
so if either write path switches to unlink-and-recreate, running jails silently stop seeing
refreshes. The general form of the rule is
[`jail-home.md`](jail-home.md#invariants).

Composition happens **before the container exists** and before provisioning runs. That is why
the provisioning-failure signal is a conditional section pointing at the provisioning log
rather than an inline error: the briefing is written before any failure can have happened, and
the read-only mount means nothing in-jail can append to it afterward.

### The handoff is read before the loop and consumed after it

The one-time handoff pointer is **read** before the write loop and **consumed** after it, and
the split is load-bearing: consuming a handoff that was never written anywhere burns it for
good. A jail with no briefing destination writes zero briefings, and an unconditional consume
ate the pointer on exactly the launch that could not deliver it. Consumption is therefore
gated on at least one briefing having actually been written. See
[`host-to-jail-handoff.md`](host-to-jail-handoff.md).

## Customizing, in practice

- **All jails, one destination:** edit the host-level file that destination's `after` names.
  Prepended everywhere, and live-refreshes — on a machine where `yolo host apply` has run, put
  it in the local pack instead.
- **All jails, every destination:** `agents_md_extra` in the user config.
- **One workspace:** `agents_md_extra` in the workspace config, or the repo's own checked-in
  project-level file, which yolo does not touch.
- **One session:** write the handover pointer in the workspace state dir; it is surfaced as a
  **Handoff** section on the next launch and consumed once that briefing is written.
- **Sharing one corpus across agents:** ship it as a pack. Per-agent copies drift, which is
  precisely why the host notch moves the user's own prose and skills into the local pack and
  regenerates every destination from there.

## Gotchas

- The briefing describes the jail as configured **at generation time**. Config edits mid-session
  refresh the text on the next invocation, but the running container's actual mounts and limits
  do not change until restart — so the text can be ahead of reality.
- In-jail skill directories are read-only by the same mechanism. Skill development happens in
  the workspace tree and is promoted host-side.
- A prepended host briefing is unrelated to that agent's host **settings** file, which is a
  separate `reads-host` grant composed into the agent's settings rather than into prose.

> [!WARNING]
> **`BriefingContent` is not golden-pinned.** No test asserts its full output — the tests cover
> the helpers, plus the jail-notch header bytes and a few individual sections. So a section can
> be added or removed without regenerating a golden, and any claim that "the briefing bytes are
> pinned" is true only of the confinement header. Do not rely on a golden that is not there; if
> a change must not move the rendered surface, pin the section it touches.

## What this does not license

- **Not** a briefing destination that core knows about. Destinations are pack declarations, and
  a jail with no packs writes no briefing.
- **Not** a fact in the briefing that was read from config rather than from what the launch
  applied. Every one of those has been a defect.
- **Not** a probe in the composer. It runs on attach, where it must be deterministic.
- **Not** a fourth skills layer that reads a generated destination.
- **Not** an inline manual. Anything a `--help` or `yolo config-ref` answers is a pointer, not
  a copy.

## Current values

Verified at `d8cf1cf8`. The prose above explains what each of these is for; this table is the
only place the values themselves are stated.

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| Staging directory, per jail | `<machine storage>/agents/<container-name>/` | `paths.AgentsDir`, `jailcontent.PrepareSkills` |
| Briefing staging filename | `briefing-<RFC 6901-escaped destination>` | `run.briefingStagingName` |
| Skills staging subdirectory | `skills-<pack>` | `jailcontent.SkillStagingName` |
| Host-briefing prepend selector | `after: "host:<home-relative path>"` on a `briefing` contribution | `packdecl` (`Contribution.After`), `run.briefingHostOverlay` |
| Ownership record gating the prepend | the host-briefing manifest under the user's config dir | `entrypoint.HostBriefingManifestPath`, `HostBriefingOwner` |
| Provenance header | `<!-- from pack: NAME -->` | `jailcontent.ComposePackBriefings` |
| Host/jail separator | `---` between the prepended host prose and the rest | `jailcontent.PrependHostBriefing` |
| Extra-prose config key | `agents_md_extra` (string; user or workspace scope) | `jailcontent.ComposeBriefing`; `yolo config-ref` |
| Built-in skills | one embedded suite, plus a source-tree-only addition | `internal/jailcontent/builtinskills` (`FS`) |
| Provisioning-failure marker | a known string in `<workspace>/.yolo/startup.log` | `jailcontent.ReadProvisioningFailed` |

## Why it's this way

Forward-facing rulings a maintainer would otherwise undo, with their original ids.

| ID | Ruling | Why it stays |
| :--- | :--- | :--- |
| `S3` | Neither briefings nor skills read yolo's own generated host output back in | Once the host notch composes a destination wholesale, reading it back in composes every pack twice — measured, byte-identical duplicates in prose, and last-writer-wins invisibility in skills. The user's own content reaches a jail through the conventional local pack instead, which is an ordinary pack entry with ordinary precedence. |
| `R2` | The destination enumeration and the staging-name encoding live in one place each, called by both halves | A mismatch does not fail the launch — podman binds an absent file source happily — so the failure is a *blank briefing*, which nothing reports. Coupling by comment had already let the two drift. |
| `R4` | A destination that declares no identity can be named by no `agents` selector, but still receives every broadcast | It is the state every pack was in before the field existed, so treating it as an error would break every existing pack, and treating it as matchable would deliver addressed prose to a destination that never claimed the identity. |
| `P2` | An audience-less contribution **broadcasts** | This is what let the field land ahead of any pack adopting it: a jail of packs that name nobody composes exactly what it did before. |
| `P4` | A content pack names **who** its prose is for, never **where** it goes | Where an agent reads is that agent pack's business and changes when the agent changes; a content pack naming a path would have to be edited every time an agent moved its file. |
| `OQ-BA2` | The audience match is against a **declared** string, never anything derived | A derived identity (a pack name, a path segment) would silently change meaning the moment either was renamed, and nothing would report it. |
| `C2` | The confinement header derives its enforcement vector from the notch's profile | A header that claims a container for a notch that has none is the dangerous falsehood — an agent treats its home as disposable. Deriving keeps it true for a notch nobody has enumerated yet. |
| `OQ-TP9` | A `briefing` contribution's `after: "host:…"` is **not** origin-gated | A fetched pack's `after` is honored like anyone else's; the gate was removed rather than extended. Prepending the user's own file is not a credential crossing. |
