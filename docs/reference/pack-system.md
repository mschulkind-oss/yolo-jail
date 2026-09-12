---
status: current
verified: 2026-09-09
verified_commit: a3922298
covers:
  - internal/packdecl/
  - internal/packload/
  - internal/packstage/
  - internal/packoverlay/
  - internal/packsrc/
  - internal/entrypoint/packsurfaces.go
  - internal/entrypoint/packhooks.go
  - internal/entrypoint/launchercollision.go
  - internal/cli/run/packs.go
  - internal/cli/run/packhostgrants.go
  - internal/cli/run/packloopholes.go
  - internal/cli/pack.go
  - internal/agentcfg/
  - internal/loopholedecl/settings.go
  - internal/loopholedecl/capabilities.go
  - packs/
tags: [packs, config, kinds, manifest, prism, trust, disclosure]
---

# The pack system — how a jail gets everything in it

**Status:** CURRENT as of 2026-09-09, verified against `a3922298`.

A **pack** is a directory of jail configuration — skills, briefing prose, composed config
files, environment variables, and optionally a tool to install — that yolo delivers into
every jail it launches. This is the whole of how a jail is populated: with no packs
configured a jail has nothing but the built-in shell and skills, and no coding agent.

**Agents are packs.** Core has no notion of "an agent": no agent registry, no `agents`
config key, no `YOLO_AGENTS`. The packs that install an agent CLI are packs like any
other, selected by bare name in the one `packs` list. Most of what yolo ships installs no
CLI at all — a loophole, a provider, a set of blocked-tool refusals, an in-jail service.

This is the reference for **authoring, debugging, or changing a pack**: what a pack
declares, how the declaration is validated, how packs are selected and fetched, and how
they are rendered into a jail.

| Component | Lives in |
| :--- | :--- |
| Manifest schema — the closed kind registry and every field | `internal/packdecl` (`Manifest`, `Contribution`, `Kind`, `footprints`) |
| Manifest loading, footprints, collisions, host grants | `internal/packload` (`Pack`, `FootprintOf`, `Collisions`, `Embedded`) |
| Staging a pack's tree into the mounted root | `internal/packstage` (`Spec`, the three content rules) |
| Fetch, the content-addressed store, the lockfile | `internal/packsrc` (`Store`, `LockEntry`) |
| Selection, pre-flights, disclosure at launch | `internal/cli/run` (`stagePacks`, `notePackHostAccess`, `startLoopholesDisclosed`) |
| Jail-side render — one loop, no switch on a tool name | `internal/entrypoint` (`ConfigurePackSurfaces`, `packhooks.go`) |
| Config surfaces, the layer fold, the four modes | `internal/agentcfg` (`manifest.Surface`, `Compose`, `ManifestWith`) |
| `config-overlay` fold and the `profile` gate | `internal/packoverlay` |
| The Lua sandbox: `yolo.derive` / `yolo.env` | `internal/agentcfg/luahook` (`DeriveCtx`) |
| The packs yolo ships | `packs/` (each a directory; `packs/embed.go` is the embed) |
| CLI surface | `internal/cli/pack.go` (`packMain`) |

**Reads with:** [`providers.md`](providers.md) (the `provider` and `profile` kinds, the
`profile:` modifier, and everything a selection does — the authority for all of it),
[`../guides/loopholes.md`](../guides/loopholes.md) (what a loophole is, and its settings
block), [`wire-bridge.md`](wire-bridge.md) (`needs`, and the `service`
kind), [`../design/trust-paths.md`](../design/trust-paths.md) (the 25 trust paths, and why
there is no approval gate), [`../design/program-delivery.md`](../design/program-delivery.md)
(the launcher, its PATH position, and evergreen updates).

For the `packs` config key, its precedence and its entry schema, run `yolo config-ref`. For
the manifest field by field, read `internal/packdecl` — the doc comments are the reference.

---

## Principles

Everything below follows from three rules. Read these first; the rest is their mechanism.
Sibling docs and code comments cite them by number.

1. **Every file on disk has exactly one writer.** A pack either OWNS a file outright, or it
   does not write that file — it contributes typed INPUTS to a neutral core-owned assembler
   that writes it. Two packs claiming one owned path is an error, caught before anything
   runs. Two packs feeding one assembled file is the feature. There is no third case.

2. **Core knows the DOMAIN, not the TOOL.** Core names domain nouns from a closed,
   tool-independent set — `program`, `skills`, `briefing`, `config`, `state`, and so on. It
   never names `claude` or `copilot`. A pack maps its tool onto those nouns. The boot path
   renders every pack in one loop with no switch on any tool name; the *selection of which
   packs stage* is the only filter.

3. **The manifest is static data.** Every claim a pack makes is readable without executing
   anything — that is what keeps linting, disclosure, and content hashing honest. Lua has
   exactly one job in a pack (the [derive slot](#the-derive-slot)): it computes config
   *values*, and never declares *effects*.

Principle 2 has a boundary, and it is worth knowing where: the **assembly** layer achieved
it, and the **imperative** layer did not, because only one tool ever needed a side effect.
Those side effects are the [`hook`](#hook) kind, and its set is closed — a pack requests a
named behavior core implements, and cannot ship the behavior itself. New behavior means a
new named hook in core.

## Invariants

- **Reading a manifest is inert; SELECTING one is not.** Every claim is static data —
  readable, diffable, and printable without running any of it, which is what makes
  `yolo pack footprint`, `pack lint` and the startup disclosure banner possible at all. What
  that does **not** extend to is honoring the claim: `program` installs a tool in the jail,
  and `loophole` starts a process **on your machine**. The cost of *looking* is zero and the
  cost of *choosing* is exactly what the claim says it is — which is why a claim must name
  its target precisely (the raw argv, the exact path) rather than summarize, and why
  **selection, not a prompt, is where the decision lives**.

- **The credential boundary is drawn at SELECTION, and no pack reads the host
  UNDISCLOSED.** Origin does not decide host access: a fetched pack's claims are honored
  exactly like an embedded pack's. What holds the line is that naming a pack in `packs`
  requires writing the user config as the host user — user scope only, inexpressible at
  workspace scope by construction, so **an agent cannot add a pack** — plus the startup
  banner, which says what each loaded pack actually reaches this launch. See
  [the credential boundary](#the-credential-boundary-disclosure-not-consent).

- **The claim enumeration must be TOTAL.** A crossing with no claim is a crossing that
  appears in **no report at all**, because the footprint and the launch banner are the only
  reports there are. Every declaration that crosses the boundary emits its own
  separately-disclosed claim.

- **Packs stay user scope.** A workspace config naming one is a hard error.

- **The source set for `derive` stays closed and core-owned.** A pack *projects* from
  core's tables into its tool's dialect; it never invents a source.

- **`derive` is deterministic.**

- **The MOUNT is the filter.** The entrypoint renders every pack it finds under the mounted
  pack root, so staging only the selected packs — and clearing the tree first — is what
  makes a dropped pack stop rendering.

- **Clear a staging destination's CONTENTS, never the directory itself.** A running jail's
  bind mount captured that directory's inode, and recreating it silently detaches the
  mount. `packstage` rule 2 states it; `PrepareSkills` and the generated-script dirs
  document it independently.

> [!WARNING]
> **Reservation lists are deliberately NOT selection-gated.** The lists that say which home
> roots a `host_files` entry may write into, which path segments `writable_home_dirs` may
> not claim, and which global-home subdirs to create are the union over every pack yolo
> SHIPS (`packload.Embedded`). Gating them on the loaded set would let a `host_files` entry
> claim a path a pack added tomorrow needs, and the collision would surface as a mount
> conflict with no obvious cause. The guarantee is bounded honestly: a *configured* pack's
> writable dir is not reserved.

> [!WARNING]
> **`packload.Embedded()` is ONE temp tree for the whole process, released on the way out.**
> The returned `Pack.Root` values are handles into it, nothing can know when the last read
> happens, and the first caller is a package-level var in `internal/config` evaluated at
> init — so the tree exists before any command has started, and process lifetime is the
> shortest honest answer. Do not make a second process-lifetime copy: three call sites did,
> and each leaked a never-removed directory on every invocation of every command. A caller
> that wants its own lifetime calls `MaterializeEmbedded` and deletes the destination
> itself.

---

## What a pack is, on disk

A pack is a directory. Nothing in it is mandatory.

```
my-pack/
├── pack.json      # optional — the manifest
├── AGENTS.md      # optional — prose concatenated into every briefing
├── derive.lua     # optional — Lua producers for config dynamic layers
└── skills/        # optional — one dir per skill, each with a SKILL.md
    └── rust-review/
        └── SKILL.md
```

The zero-ceremony pack — an `AGENTS.md` plus a `skills/` tree, no `pack.json` — is a
complete, useful pack: house rules and a skill corpus applied in every jail. `yolo pack
init` scaffolds exactly this.

`AGENTS.md` is the **only** conventional briefing filename (`packdecl.DefaultBriefingFiles`).
`CLAUDE.md` was the second candidate until 2026-08-17 and is not read by anything now: it is
Claude Code's own name, where `AGENTS.md` is the cross-tool convention, and yolo picks one.
A pack whose prose lives elsewhere names it with an explicit `from`, which is what `from` is
for.

> [!WARNING]
> **`DefaultBriefingFiles` is the authority, and the blast radius of changing it is every
> site that re-lists the pair.** `yolo pack lint` carried its own hardcoded copy and went on
> counting a root `CLAUDE.md` as content some reader picks up after every reader had stopped
> — so a pack whose only content was a `CLAUDE.md` linted clean while briefing nothing.
> `TestPackLintTracksTheBriefingConvention` pins the behavior rather than the list, because
> a test on the literal stays green through exactly that drift.

Content rules are enforced at staging by `internal/packstage`, and a violation is **fatal,
not skipped** — a pack that half-stages is worse than one that fails loudly:

| Rule | Enforcement |
| :--- | :--- |
| A symlink whose target escapes the pack root is refused | staging error |
| Staging clears a destination dir's *contents*, never the dir itself | avoids clobbering a mountpoint |

Skills staging deliberately *does* dereference symlinks, because its source is the user's
own home — a different source, so a different rule.

A pack **ships its tools**: a file carrying the exec bit stages executable, so a skill can
deliver the script it tells an agent to run.

> [!WARNING]
> **Do not re-add an `allow_exec` gate on the exec bit.** It read as a trust boundary and
> was not one — `bash file.sh` never needed the bit — so it stopped nothing an adversary
> would do while failing on the honest case it kept meeting. Two narrower things replaced
> it, one enforcing and one informing: a destination on the **jail's PATH** is refused in
> the manifest (`packdecl.appendJailPathProblems` — the dir itself, a parent of it, or
> anything inside it), so a pack reaches PATH by declaring [`program`](#program) and nothing
> else; and the executables a pack ships are counted as a **claim** in its footprint, since
> a mode bit is a property of the tree with no manifest line a reader could find it on.

`yolo pack lint` runs the real staging executor against a directory and reports what it
would stage and what it would drop, so these rules are checkable before a pack is
configured.

## The manifest

`pack.json` carries a handful of per-pack facts plus one list of typed contributions. Every
field is optional; a pack with no `pack.json` behaves as an empty manifest. Decoding is
strict (`DisallowUnknownFields`) and reports *every* problem, not the first, so a typo in
one contribution does not mask a second.

| Top-level key | What it is |
| :--- | :--- |
| `name` | informational — **not** the pack's effective name (below) |
| `description` | prose for `yolo pack ls` |
| `contributes` | the pack's effects: one list of typed contributions, each with a `kind` |
| `skills_tier` | the pack's opt-in to having its own skills namespaced |
| `supersedes` | the pack's claim that a [capability](#capabilities-and-supersession)'s job no longer needs doing |
| `needs` | [conditional pack dependency](#needs--conditional-pack-dependency): another pack joins the launch when a condition holds |

The last three are **top-level rather than contribution kinds**, and the argument is the
same each time: a kind is something the pack DELIVERS at a target, with a `Combine` rule
saying how two claims on that target resolve. None of these delivers anything or owns a
target — they are per-pack facts about how the pack relates to its environment. A
contribution that contributes nothing is a category error, and `supersedes` is pinned
rejected as a kind by `TestSupersedesIsNotAContributionKind`.

> [!IMPORTANT]
> **`name` is informational — it is not the pack's effective name.** That comes from the
> `packs` entry that selected the pack (its explicit `name`, else the last segment of its
> source address), or for a pack yolo ships, its directory under `packs/`. The effective
> name is both what the staging directory derives from and the handle `yolo pack ls` prints
> and `yolo pack explain` takes — fixed from the config line alone, before any `pack.json`
> is read and before a git source is fetched, so the manifest cannot supply it. The
> **staging directory** is `PackEntry.Slug`'s escaping of that name, and it — not the name —
> is what a `reads-host` grant with no `into` is mounted under, because it is the only
> string the jail can name a pack by (`Pack.StagedSlug`). A shipped pack's `name` is pinned
> equal to its directory so the two cannot drift
> (`packload.TestEmbeddedPackManifestNamesMatchTheirDirs`).

```jsonc
{
  "name": "claude",
  "contributes": [
    { "kind": "program", "bin": "claude", "via": "installer", "url": "https://claude.ai/install.sh" },
    { "kind": "skills",  "from": "skills", "into": ".claude/skills" }
    // ...
  ]
}
```

Each contribution has a required `kind` drawn from a closed set core owns, plus the fields
that kind uses. The struct is a flat superset — a contribution carries only the fields its
kind reads, and a field belonging to another kind is **refused by name** rather than
ignored, because a field that is silently never read is a declaration that does nothing
with nothing to say why.

**Every path is relative and points into `$HOME` or the pack.** Absolute paths, `..`
segments, and `:` are rejected as a security property, not a style rule: a pack —
especially a fetched one — naming `/etc/shadow` or `../../` must never validate. This check
runs on every path-bearing field of every kind.

### Unknown kinds across the version boundary

An unknown `kind` is a loud load error **at authoring** — every host-side read — and, across
the version boundary only, a skipped-and-reported contribution instead. The in-jail load
runs `packload.TolerateSkew()`, so a manifest using a kind a pre-`just load` entrypoint does
not know still boots the jail, warning by name.

> [!WARNING]
> **Tolerance is for a kind, not for a CONSTRAINT.** A settings declaration is refused on
> the tolerant decoder too, and that inversion is deliberate: an unknown key that a newer
> build would read as a *restriction* must not be dropped, or core validates a value against
> half a rule. `TestUnknownSettingDeclarationKeyIsRefusedByBOTHDecoders` pins it.

## The kinds and how two claims combine

The kind set is closed and core-owned. **`packdecl`'s `footprints` map is the authority for
the whole set** — the map key is what `KnownKinds()` derives from, so there is deliberately
no second list to drift, and each entry carries that kind's combine rule, its one-line
claim description, and whether it can produce a review-worthy claim. `KnownKinds()` returns
the set sorted alphabetically; the map is in declaration order.

What a kind's **combine rule** says is how two claims on the *same target* resolve. This is
the semantic axis, and it is short:

| Combine | Meaning | Kinds that use it |
| :--- | :--- | :--- |
| **Exclusive** | one owner per target; a second claim is an error | `program` (by bin) · `files` (by path) · `config` (by surface identity) · `launch` (by bin) · `autonomy` · `loophole` (by loophole name) · `service` (by service name) · `provider` (by provider name) · `blocked-tool` (by bin) · `profile` (by pack + name) |
| **Shared** | many independent claimants are the ordinary case | `requires` · `reads-host` · `mount` |
| **Merge** | many inputs into one target is the feature | `skills` · `env` (a key claimed twice collides) |
| **Concat** | ordered concatenation | `briefing` |
| **Overlay** | later-wins with per-key provenance | `config-overlay` |
| **Scoped** | the same subtree at two scopes is an error | `state` |
| **PerHook** | per hook name | `hook` |

Exclusivity is **per target, not per pack**: one pack shipping three loopholes, two
programs or two providers is ordinary; two packs claiming one name is the collision. The
reasoning is the same everywhere — the name is the whole identity. A shadowed loophole name
is a daemon nobody audited running under a name the user trusts, and everything downstream
keys on the name: the state dir, the endpoint, the `enabled` toggle, the disclosed claim.

The **footprint** is this table applied to a concrete set of packs: the union of every
claim, plus the collisions where an Exclusive or Scoped target is claimed twice. `yolo pack
footprint` prints it and `yolo check` folds it in. Some claims are flagged `⚠ review`
because they widen the trust surface — machine-scope state (it leaks across workspaces), a
`reads-host` grant, a `mount` of a host directory, an installer URL, a briefing that
prepends a host file. The review flag is an invitation to look, not a refusal. A second,
strictly narrower flag marks a claim whose crossing is **execution on the user's machine**,
so a reader scanning a footprint does not have to notice that one of a dozen
identically-flagged lines happens to say RUNS.

### Collisions the generic loop cannot see

The generic loop keys collisions by `(kind, target)`. Four things need their own pass
because they do not fit that shape, and all four live in `internal/packload/footprint.go`:

- **`AgentNameCollisions`** — the AGENT NAME is exclusive **across kinds**, and it is the
  only namespace that is. A pack claims one by installing its launcher (`program`),
  injecting that launcher's flags (`launch`), or declaring where that agent reads
  (`briefing`/`skills` `agent`). Two packs claiming one name is fatal at launch, at `yolo
  host apply` and at `yolo check` — and two of the four claiming kinds merge by design, so
  the generic loop cannot express it. `requires` is deliberately **not** a claim on the
  name: it is Shared for a reason, and a content pack asserting `claude` beside the pack
  that provides it is an ordinary dependency.
- **`ConfigSurfaceCollisions`** — see [config surfaces](#config-surfaces-and-the-compose-engine);
  a pack can commit this against *itself*, which the generic loop skips.
- **`LoopholeNameCollisions`** — the claims come from a file outside `pack.json`.
- **`pluginNameCollisions`** — same, for a wrapped plugin.

### The per-kind rules worth knowing

`internal/packdecl` is the field-by-field reference. What follows is the subset where the
rule is not derivable from the field name — and the traps.

#### `program`

Installs a tool and puts it on PATH via a launcher that installs on first invocation — and
thereafter mediates **every** invocation, which is what keeps an agent dependency current.
The launcher goes in `~/.yolo/bin/launch/<bin>`, which is **second** on PATH: immediately
after the blocked-tool shims (which stay first, because interception must outrank
installation) and **ahead of every install prefix**, and so ahead of `/bin`.
`entrypoint.BootPath` is the authority for that order, and there are two more
independently-written copies of it — the `.bashrc` export, compared to `BootPath` entry by
entry by `TestBashrcPathMatchesBootPathOrder`, and `macosuser.SandboxPath`.

> [!WARNING]
> **This position is load-bearing and the obvious alternative is a defect.** Ordering the
> launch dir *after* the prefixes it installs *into* makes a launcher unreachable the moment
> it succeeds, so the lazy install runs exactly once per home and the update never runs
> again. An **agent dependency** wants to be current, so the launcher has to mediate every
> invocation, not just the first.
> [`../design/program-delivery.md`](../design/program-delivery.md)
> [§3.5](../design/program-delivery.md#35-the-second-axis-who-the-dependency-serves-amendment-2026-09-03)
> (P6) is the standing account.

What the old position bought is now bought by a check at generation time:
`internal/entrypoint/launchercollision.go` writes **no launcher at all** for a name `/bin`,
`/usr/bin`, a declared `mise_tools` entry, or the store-delivered package farm already
provides. Same outcome, weaker guarantee — the failure is **handled** rather than
unrepresentable, which is the honest cost. The consequence to know is unchanged: a name the
**image** bakes still wins over the pack's declared version, now because the launcher
declines to exist.

> [!WARNING]
> **The collision check must never be spelled *"is this name already resolvable on
> PATH?"*** — spelled that way it is a silent kill switch that destroys the feature. After
> one successful install the installed binary exists in a prefix, so the next boot writes no
> launcher, so PATH resolves it directly, and evergreen works exactly once: green, silent,
> and identical to the freeze the reorder exists to end. The install prefixes are excluded
> from the probe by the property that *defines* them — every one lives under the jail home,
> and nothing the image ships does. **Declared, not installed, for mise too:**
> `GenerateAgentLaunchers` runs before mise is configured, so the shim directory is empty on
> a cold boot and a check that read it would shadow a project dependency on exactly the
> boots where the project is new.

Version selectors decide whether the launcher polls. **No selector means `@latest`**, which
the launcher re-checks against the registry once an hour — so an unversioned declaration
changes what runs with nobody present. **A selector turns that poll off**: the registry's
`latest` is not an answer to a declaration that named its own version, and for a tag or a
range it would never compare equal, so polling would reinstall hourly forever. A pinned
launcher compares the recorded spec against the declared one, offline, so moving a pack from
one version to another still takes effect. The recorded spec is what npm **installed**,
never merely what was asked for — an upgrade leaves the previous binary in place, so
recording a failed attempt would shut both exits at once and freeze the jail on the old
version with nothing left to retry.

`update` is the argv that makes the program update ITSELF, with the bin omitted. The
launcher runs it when the program is due an update; absent, it falls back per `via` —
re-running the declared installer, or a global npm install. It is a vendor's argv and
therefore deliberately NOT a closed enum: the vendors disagree, and core hardcoding one is
how `yolo pack update` came to skip the installer class entirely. `update` is read on
`program` alone and refused by name on every other kind.

`install_hints` maps a host package manager to the package that provides `bin` there. Used
below the `jail` notch, where yolo bakes no image, by `yolo check-deps` / `apply` to probe
for the binary and emit a runnable manifest. One key names an installer *flavor* rather than
a manager: **`brew-cask`**, because a Brewfile `brew` line naming a cask token fails and
bare `brew install <token>` silently prefers a same-named *formula* — brew's `copilot`
formula is AWS's deprecated ECS CLI, not the CLI this pack means. `brew-cask` wins over
`brew` when a pack declares both; the detected manager stays plain `brew`.

> [!WARNING]
> **Do not add `nix` hints to a pack whose tool ships its own installer and updater.** The
> six agent packs dropped theirs: routing a user through nixpkgs hands them whatever that
> repo has, with nothing in the output to say so (measured once at 16 releases behind), and
> `detectManager` reaches `nix` only by *elimination*, so a user cannot select it
> deliberately anyway. `nix` hints belong on genuine third-party dependencies where the
> user's own package manager is the right answer. A printed `curl … | sh` is a **suggestion
> the user runs**, never something yolo runs.

#### `requires`

A binary that must **already exist**. Asserts presence and installs nothing — no launcher,
nothing on PATH — so unlike `program` it cannot shadow the very binary it asserts.

`program` and `requires` are *install* vs *presence*, and conflating them was a real defect:
a pack needing a tool the image already bakes, or the user already has, had only `program`,
so it either lied — declaring an npm install for a baked binary, which then shadowed it — or
declared nothing and lost `install_hints` entirely.

`via`/`package`/`url`/`update` are **refused by name**: those belong to `program`, and a
`requires` carrying one is the author reaching for the other kind. At the jail and guest
notches a missing bin is a **warning naming the bin**, never a boot failure — the pack's
other contributions are fine, and a fatal here would stop the jail you need in order to fix
the pack. At the host notch it feeds `check-deps` / `apply` exactly as `program`'s hints do,
which is what lets a content-only pack carry a remedy.

#### `skills`

A skills tree merged into an agent's skills dir. Precedence is built-in < pack < the user's
own tree, so a local skill always wins. `from` defaults to `skills/`, and is honored at both
notches and by wrapped-plugin discovery through one resolver (`packload.SkillsSourceDir`).
`into` is required unless the contribution names an `agents` audience instead.

A `from` naming a directory the pack does not contain delivers nothing and is **reported by
name** — a warning at launch, a `refused` line and a non-zero exit at `yolo host apply` —
rather than silently falling back. The *conventional* `skills/` being absent is the one
exemption, and not an oversight: the agent packs declare `from: "skills"` and carry no
skills of their own (their contribution exists to NAME the destination other packs merge
into), so complaining there would fire on every launch of a stock config.

`skills_tier` is a **per-pack** choice, not per contribution, and that is the whole of the
ruling behind it: a tier decides what a skill is CALLED, which is a global property.
Declared per contribution it could not express a consistent name — and a zero-ceremony pack,
which declares no destinations at all and borrows them from the other selected packs,
*inherited* a tier per destination, so one skill acquired two invocation names without the
pack ever choosing either. Values are unnamespaced (the default), `flat` (the same thing
said out loud), and `namespaced` (one subtree per destination, invoked `<pack>:<skill>`).
A pack still carrying a per-contribution `"tier"` is refused BY NAME with the migration in
the message, rather than failing on the strict decoder's bare unknown-field error.

> [!WARNING]
> **A skills name collision between two packs is FATAL at apply time**, naming both packs,
> both source paths, and both remedies (rename, or opt one pack into namespacing). At flat
> tier one pack's skill silently won and the loser produced no output line at all. The error
> costs the deliberate flat-tier override, and that is the trade: an intentional override
> and an accidental clash are the same declaration, so yolo cannot tell them apart and the
> user should. **Adoption preserves, declaration refuses** — migrating a user's pre-existing
> tree keeps both copies (`mine`, `mine-from-codex`), because those are two different
> situations.

#### `briefing`

Prose concatenated into a briefing file, attributed to its pack. **The destination is
generated wholesale at every notch**, so a hand edit does not survive.

`from` is optional; absent, the candidate is the conventional `AGENTS.md`. Both notches
resolve it through `packload.BriefingProseFor`, and the precedence is a **fallback chain**
rather than a single choice — a declared `from` that is not in the pack's content falls back
to the convention and WARNS, naming the file that was not read. `skills` refuses in the same
situation; the difference is deliberate, because narrowing the briefing chain would break
packs that relied on the host behavior.

`into` is required unless the contribution names an `agents` audience — and deliberately not
conventionalized: a source has one right answer per KIND, a destination one per AGENT, so
inferring it would mean inferring the agent set. `agent` is the IDENTITY a destination
declares for itself (the launcher command whose agent reads it), declared by the pack that
OWNS that name; nothing is derived from the pack's `program`/`requires` bins — the string is
declared, carried, and compared literally. `agents` is the AUDIENCE, and **absent means
broadcast**. A contribution gives `into` OR `agents`, never both: a content pack that
hardcoded `.claude/CLAUDE.md` would be coupled to a fact only the claude pack can keep
current. The vocabulary is the SELECTED packs' agent names and nothing wider — naming an
agent your `packs` do not provide is fatal at launch and at `yolo host apply`.

`after: "host:<path>"` prepends the user's own briefing at that host path ahead of the
composed content, so a personal `AGENTS.md` still outranks the pack's. It is **jail-only**:
at the host notch the path it names IS the generated destination, so there is no
user-maintained file left to prepend.

> [!WARNING]
> **"The user's own" is CHECKED, not assumed.** Once a machine has run `yolo apply --at
> host`, that same path holds yolo's own composition — so the jail prepended every pack's
> prose and then composed the same packs again, and each pack arrived twice. The run pipeline
> asks `entrypoint.GeneratedHostBriefings`: ownership is proved from
> `host-briefing-manifest.json`, never inferred from content, and it fails OPEN so an absent
> record still prepends.

The first apply into a destination the user wrote is CONFIRMED, once, and fails closed on a
non-interactive stdin — taking wholesale ownership of a file the user wrote is a one-way
door. Their prose is MOVED into the conventional local pack
(`~/.config/yolo-jail/local/AGENTS.md`), from which yolo composes it back into every
destination, so their instructions keep reaching their agents. To add personal prose, edit
the local pack, not the destination. Dropping the last contributing pack **archives** the
destination rather than leaving a generated file with no owner; nothing is ever deleted.

#### `files`

An opaque tree the pack owns outright, bind-mounted `:ro` at `into` in the jail. `from` is
required and honored: there is no conventional location for an opaque tree, so the
declaration is the only thing that can name it. The source bound is the pack's **staged**
tree, so `packstage`'s escaping-symlink refusal has already run on it — `files` is not a
channel around it.

Sole ownership is enforced before the container starts: two contributions claiming one
`into` are refused at launch, naming both packs, rather than reaching podman as a "duplicate
mount destination" error that names neither.

> [!WARNING]
> **`files` is deliberately NOT deduplicated the way `skills` and `briefing` are.** Those
> two emit one bind per contribution over content the assembler had *already* merged, so
> deduplicating per destination was the fix. A second `files` claimant on one path is a
> genuine sole-ownership violation, so it stays a pre-flight refusal naming both packs —
> deduping there would let one pack's content shadow another's. Same podman symptom,
> opposite correct response.

A `files` claim is a `:ro` mount over its whole destination, so a surface the entrypoint
must write beneath that path would hit a read-only filesystem. That is caught in pre-flight,
before the container exists, with the remedy (narrow the `into`) — it used to refuse the
boot with an error naming the *surface* rather than the claim that shadowed it, and usually
cross-pack, so neither author could see it.

Apple Container cannot bind-mount a single file, so a `files` contribution naming one FILE
is copied into the jail home there instead of mounted. A directory is mounted on both
backends. Rendering `files` at the HOST is refused by name: a bind mount means nothing
off-container, and writing the tree into a real `$HOME` is a different posture with its own
never-clobber and file-mode rules.

#### `state`

A writable home subtree that persists. `scope` is `workspace` (the default, backed
per-workspace) or `machine` (backed by the global home, and **leaking across workspaces by
design**). `because` is **required** for `scope: machine`, so a cross-workspace leak is a
conscious, documented decision.

A machine-scope state claim is review-worthy in the footprint but is deliberately **not** on
the launch banner: it is a subtree of the JAIL's home that yolo owns, not a path on the host
the pack reads. `TestDisclosureCoversEveryReviewWorthyKind` names it as the deliberate
exclusion, so this reasoning has to be restated or refuted by anyone who changes it.

#### `reads-host` and `mount`

Two host-home reads. `reads-host` names one **file**, whose `/ctx` destination defaults to
the pack's staging slug plus the basename, and which becomes the `host` layer of a config
surface by basename match. `mount` names a directory (or a file) and picks an arbitrary
`/ctx` destination, for making a reference tree — a dataset, a shared prompt library —
visible in the jail. An absent source is skipped with a warning rather than failing the
jail: the user simply has not created it, and the surface falls back to its defaults layer.

Both are Shared: many readers of one host file is fine. **Neither is origin-gated any
more** — see [the credential boundary](#the-credential-boundary-disclosure-not-consent).

A `mount` is the one host grant with no relocation available. `reads-host` and the
`host_files` config key materialize into a per-workspace directory on Apple Container
because their reader is the ENTRYPOINT, which can be redirected; a `mount`'s reader is the
AGENT, following the `/ctx` path its own briefing names, so there is nowhere else to put it.
Both forms are dropped with a reason on that backend.

#### `env`

Static environment variables set in the jail. Values are **literal strings only** — no
interpolation, no secrets, no host references — so `env` never reads the host. A key two
packs both set collides. For values that must reference a secret or a host path, the user
config's `env_sources` is the channel, kept out of a distributable pack on purpose. `env` is
shown on the launch banner anyway, because it changes what the agent inside the jail sees,
which is the other thing a user checks a launch for.

#### `hook`

A named request for a core-provided imperative behavior. The set is **closed**
(`packdecl.KnownHooks`, drift-pinned by `entrypoint.TestHookSetsAgree`): a pack requests a hook
by name and supplies its parameters; it cannot ship the hook's logic, which would put
arbitrary effect code in a fetched pack. New behavior means a new named hook in core. A
parameter a given hook does not use is an error rather than ignored, so a misplaced field is
not a declaration that silently does nothing.

The `shared_credentials` hook's contract is *"symlink this file into this machine-scoped
dir"*, and its rule is that **the shared file always wins**:

```
already the right symlink        → done
real file + EMPTY shared         → copy local into shared, then symlink
real file + POPULATED shared     → discard local, then symlink
anything else                    → symlink
```

The copy-if-empty branch is not a freshness rule — it is what makes a first login in a fresh
install survive. There is deliberately no freshness comparison in any schema: a
schema-specific merge is what made a generically-named hook claude-shaped, and the second
tool to consume the hook exposed the seam.

> [!WARNING]
> **A revoked or expired shared credential is sticky, by design.** If the shared file holds
> a dead credential and a jail logs in again, that fresh login is discarded at the next boot
> and the jail is back on the dead token. Re-login fixes it until the next boot, so the exit
> is to `rm` the shared file, not to change the rule. What must survive any change here is
> the metadata the credential file carries beside the token trio — a file carrying only the
> trio reads as *not logged in* to a current agent — which under "shared always wins"
> survives for free so long as the broker keeps preserving it on refresh.

#### `loophole`

A loophole MODULE the pack ships: `from` names a pack-relative directory holding a
`manifest.jsonc`. The contribution **points at** the module rather than inlining the
manifest, so the on-disk shape is the one a user loophole already has — one loader reads
every source, and an author can develop a loophole standalone and drop it into a pack
unchanged. `from` is required (the directory's basename *is* the loophole's name) and there
is no `into`: the host half runs on the host and the jail half is mounted at a path core
owns, so there is no destination for a pack to name, and one is refused by name rather than
accepted and ignored.

**The sharpest kind, and the only one whose claim is host code EXECUTION.** Four things
follow, and none is optional:

1. **Its claims come from outside `pack.json`**, so they are produced at the `packload`
   layer (`Pack.loopholeClaims`), not in `packdecl`, which has no pack root and no internal
   imports — a claim computed there could only be a bare `loophole <name>`, a line blind to
   the daemon it discloses. Same layer and same reason as a wrapped plugin's claims.
2. **The enumeration is TOTAL.** One separately-disclosed claim per crossing: the daemon
   argv (with `doctor_cmd` folded in — it is host execution too), one per intercept (which
   claims even with no daemon: it installs a CA every TLS client in the jail trusts), one per
   bind mount, one per **socket** bind as its own read-write host-IPC class (`:ro` is no
   boundary for an AF_UNIX socket), and one per device. `state_files` needs none — it stays
   inside yolo's own state tree.
3. **The claim string is the raw argv** — placeholders unexpanded, nothing elided, joined
   with `shquote.Join`. **The reason is INJECTIVITY:** two different argvs must never render
   to one claim. An ellipsis collapses two daemons onto one line; a bare space join collapses
   `["sh","-c","a b"]` and `["sh","-c","a","b"]` — the same failure arrived at by accident.
   Nothing ever execs the string (the spawn reads the argv list), and `{loophole_dir}` stays
   unexpanded so the line reads the same on every machine. The footprint's *Detail* and the
   banner's `Claim.DisclosureSentence` are **renderings** of that identity and may
   abbreviate; the record and the prose are deliberately different strings, and
   `TestDisclosureSentenceDistinguishesClaimsThatDiffer` pins the injectivity.
4. **Exclusive by NAME**, and every instance is review-worthy — which no other kind can say
   of all its instances.

The launch refuses a loophole-name collision outright: it is the fourth launch pre-flight,
and it is FATAL (`run.PackLoopholeNameConflicts` over `packload.LoopholeNameCollisions`).

> [!WARNING]
> **Do not re-add a reserved-loophole-name list.** It looks like the obvious hardening and it
> is the exact change that was backed out. Once `claude-oauth-broker` became a contribution
> of `packs/claude`, a reserved name and a pack-shipped name would be the same name — and
> because the collision pre-flight is fatal, the reservation would refuse every launch that
> selected the pack. The collision check is the mechanism that replaced it. One constant
> survives, `paths.BuiltinCgroupLoopholeName`.

`loophole` is refused at the **host** notch, and the reason is the inverse of the generic
one: a loophole is a host daemon whose only client is a container, so with no jail there is
no client, no `--add-host`, no `YOLO_JAIL_DAEMONS`, and nothing for its endpoint file to be
mounted into. Refused because its **counterparty** is missing, not its mechanism — which
also keeps "selecting this pack runs a daemon" a statement about launching a jail rather
than about applying a config.

#### `service`

A daemon with an endpoint file under `/run/yolo-services/`, and **no boundary crossing at
all**: no grant, no host read, no host execution. The machinery is loophole-shaped on
purpose — a supervised daemon, an endpoint file, a reachability witness — and the TRUST is
not. The grant-shaped fields are refused on the kind, so a service that wanted a host read
is unrepresentable, and the footprint marks it never review-worthy for that reason rather
than as an omission. A daemon that DOES cross is a `loophole` declaration with the
per-crossing review that kind carries. What a service runs is in-jail, and its claim Detail
says what, so a reader sees the argv without opening the manifest.

#### `blocked-tool`

Refuses a tool inside the jail, printing a message and an alternative instead of running it.
Exclusive, because the blocker is a FILE at `~/.yolo/bin/block/<bin>` — the same reason
`program` is exclusive. Never review-worthy: a blocker writes a refusing shim INSIDE the
jail and crosses nothing. A fetched pack blocking a tool can make a jail less useful; it
cannot make it less contained.

**A pack concern, not a core one.** Core used to block `grep -r` and `find` by default — a
default that silently assumed the image bakes `rg` and `fd`, true of the container backends
and false of `macos-user`, where nothing is baked and the suggestion named a binary that did
not exist. Moving the list into a pack makes the assumption explicit: the pack that blocks a
tool is the pack that can say what replaces it, and selecting it is the opt-in. A blocker is
generated **only when its declared replacement is on the agent's PATH** (`agentPath` is the
one authority for which PATH counts), so a block can never leave a jail with neither the
tool nor its alternative. A generated shim is unconditional at run time unless
`YOLO_BYPASS_SHIMS=1`. A user's own `security.blocked_tools` entry naming the same tool
REPLACES the pack's whole.

A tool that is both blocked and pack-declared gets one of each, and the blocker wins by
position — the two generated script dirs are adjacent at the head of PATH, and their order
relative to each other is what carries the meaning. They share one bind-mount anchor at
`~/.yolo/bin`, so both are cleared contents-only. **Nothing may put that shared parent on
PATH**, or a launcher would be reachable from the blockers' position.

#### `provider` and `profile`

A `provider` declares a service's facts — endpoints by protocol, wire protocol, model
aliases, the *name* of the environment variable holding the credential, and the options a
profile may tune. A `profile` is a selection and nothing else: `{name, provider}`.
Everything a profile used to carry as a body is now an ordinary contribution gated by the
`profile:` modifier, which is accepted on `env` and `config-overlay` and refused by name on
every other kind.

[`providers.md`](providers.md) is the authority for all of it: the canonical `wire_api`
vocabulary, the composition of the provider table, the credential pre-flight, selection, and
per-agent delivery. Nothing here restates it.

## `needs` — conditional pack dependency

A pack declares that ANOTHER pack belongs in the launch when a condition on the selected set
holds — `needs: [{pack, when_bins}]`. It delivers nothing and owns no target: its effect is
to EXTEND SELECTION, and its conflict rule is "already present = no-op", a rule about the
selected SET that no per-target combine rule can state. That is why it is a top-level key.

`internal/packdecl/needs.go` refuses only version-invariant structure (an empty pack name, a
name carrying `=`, an empty bin entry). The two facts about the pack UNIVERSE are checked
where the universe is known, at `packload.ResolveNeeds`. Four rules govern it, and
[`wire-bridge.md`](wire-bridge.md)
[`wire-bridge.md`](wire-bridge.md#kind-service--the-vocabulary-it-landed-as) is the authority:

- **The named pack must be embedded** (WB-D9). A fetched pack needs-ing another fetched pack
  would make selection itself a supply-chain channel; refusing keeps `packs:` the only place
  unreviewed code enters a launch.
- **Resolution is a transitive closure run at selection, BEFORE staging** (WB-D10), because
  the mount is the filter. Explicit user selection is joined, never overridden.
- **The auto-inclusion prints** — on the launch banner and in `yolo check`. A silent join is
  the one forbidden behavior (WB-D12).
- **User config carries no `needs` key** (WB-D11). Manifests only.

## The one-writer rule and the neutral assembler

Principle 1 restated concretely. There are two ways a file reaches the jail:

- **Sole ownership.** `files`, `program`, `launch`, and a pack's own `skills`/`briefing`
  tree — the pack owns the target, and two packs claiming one path is an error before
  anything runs.

- **Shared production via a neutral owner.** When two packs affect one file, no pack writes
  it. Each emits typed contributions and a **core-owned assembler** consumes all of them and
  writes the file into a staging tree it wholly owns; the staging tree is mounted read-only
  into the jail. The compose engine is that owner for config; the stage-then-`:ro`-mount
  pattern is it for skills and briefings.

```
   packs' typed contributions                 core assembler                 the jail
  ┌────────────────────────────┐          (the ONLY writer)            ┌──────────────────┐
  │ claude:  config inputs ─────┼──┐                                   │                  │
  │ house-rules: config overlay─┼──┼──►  compose ──► staging/ ──:ro──► │ ~/.claude/       │
  │ claude:  skills tree ───────┼──┼──►  merge   ──► staging/ ──:ro──► │   settings.json  │
  │ house-rules: skills tree ───┼──┘                  │                │   skills/        │
  │ claude:  briefing prose ────┼─────►  concat  ─────┘  (collision-   │   CLAUDE.md      │
  │ house-rules: briefing ──────┼─────►             checked, tracked)  │                  │
  └────────────────────────────┘                                      └──────────────────┘
        declare inputs                  one module writes every file      never written in place
```

Three properties fall out: no file is ever written twice (a "collision" is detected among
inputs, never raced on disk); provenance is free (one module writing from declared inputs
can say which contribution produced which key); and the staging layer is a safety boundary
(a bad contribution corrupts a throwaway tree, never the live home).

## Config surfaces and the compose engine

A `config` contribution declares one or more **surfaces**. A **surface** is one generated
file plus its layer data, identified by `(agent, name)` — that identity is what yolo keys
on internally, and `path` is where it lands. Two packs using one identity are talking about
the same file.

`agentcfg.BuiltinManifest()` is **core's own surfaces and nothing else** (`mise/config`
today); every other surface, agent and non-agent alike, lives in a pack's `pack.json`, and
callers wanting the full set merge pack surfaces via `ManifestWith`. The surface schema is
validated by the config engine and its fields are documented in `internal/agentcfg/manifest`
— `codec` is the decode/encode round-trip, `defaults` is yolo's freely-overridable base
layer, `managed` is the keys yolo asserts and always wins, `retireOnFirstRender` names stale
sidecars to clean up.

The engine composes a surface by folding layers with RFC-7386 merge semantics, lowest to
highest precedence:

```
defaults < host < workspace < config-overlay < capture-overlay < computed(derive) < managed
```

- **`host`** is derived, not declared: a `reads-host` grant whose basename matches the
  surface path becomes this layer, read from its `/ctx` mount. This is how an agent's own
  host-side settings compose into the jail with no second declaration.
- **`config-overlay`** carries the keys OTHER packs contribute to a surface this one owns, in
  `packs`-list order (later wins). Below `managed`, so the owner still wins a genuine
  conflict.
- **`capture-overlay`** carries a user's in-jail edits across regeneration, for `stateful`
  surfaces.
- **`computed`** is the per-boot dynamic layer produced by [`derive`](#the-derive-slot); a
  null value there is an RFC-7386 tombstone that deletes the key.
- **`managed`** is the floor yolo always wins.

`${workspace}` in a map key is substituted with the container workspace path.

### The four modes

`mode` says how the file is maintained across boots, and the set is closed: `stateful` (the
default — compose from layers *and* capture in-jail edits back into the overlay), `computed`
(compose and overwrite every boot, discarding in-jail edits), `rmw` (read-modify-write an
agent-owned file: merge yolo's managed keys into whatever the agent wrote, no sidecars), and
`unrendered` (declared but never written).

`rmw` is the mode for a file the agent itself owns and mutates at runtime. yolo regenerates
only the keys it manages and leaves the rest alone; where it owns a top-level key it
regenerates that key wholesale each boot, and a server a UI adds at that scope is
overwritten with a boot-time drop notice. **yolo does not reconcile; it regenerates.**

An **`rmw` surface has no layer fold** — it merges keys into a file the agent owns — so it
expresses the same precedence by write order instead: overlays are asserted first, then the
derived tables, then `managed`, then `defaults` fill only where absent. Same outcome, one
mechanism short.

**Comments survive an `rmw` render, and the mode is why.** "Preserve everything yolo does not
declare" covers the prose as well as the keys, so on a `toml` surface a comment is put back
beside the key it explains — with one exception, which is a rule rather than a limitation: a
comment above a key the render CHANGES is dropped, because a stale `# pinned to …` sitting
above a new value misleads worse than no comment at all. Every such drop is named in `yolo
host apply`'s output. A `json` surface has nothing to preserve — strict JSON has no comment
syntax, so a commented file never decodes and `rmw` refuses it untouched. The other two
composing modes are unchanged for different reasons: `computed` is a file yolo solely
authors, so there is no user comment in it; `stateful` composes from many layers, which makes
preserving a comment a PROJECTION out of the `host` layer rather than an in-place edit.

### When two packs want one config file

A second pack contributing to a surface writes `config-overlay`, naming the target by
identity and carrying only keys. The owning pack stays the sole *owner* of the file; the
contributor is explicitly a *contributor*, with per-key provenance so an override is legible.

> [!WARNING]
> **Do not declare the same surface identity from a second pack, and do not "fix" the
> collision by deep-merging two declarations.** Declaring it twice is refused (below) because
> the merge used to resolve one identity last-writer-wins, WHOLE: the survivor brought its
> own `mode`, `path`, `codec` and `defaults`, so a pack could flip another pack's surface
> from `stateful` to `rmw` and silently disable in-jail edit capture for a file it did not
> own. Deep-merging instead answers "whose `managed` keys win" but not "whose `mode` wins" —
> and `mode` is not mergeable, so one declaration still has to lose. It also erases the
> ownership distinction that makes provenance possible: under a merge, "which pack set this
> key" becomes unanswerable. A pack CAN avoid the flip by matching the owner's `mode`, and
> that is politeness by the author, **not** a fix: the hazard belongs to the mechanism, so
> only the refusal closes it (R1).

`packload.ConfigSurfaceCollisions` is the single detector, consumed at three refusal sites:
`packload.Collisions` (so `yolo pack footprint` and `yolo check` report it), `stagePacks`
(so a launch is refused before the container exists), and `yolo host apply` (so nothing is
written into a real home). It is **its own pass**, not a row in the generic exclusive loop,
for two structural reasons: a pack can commit this against ITSELF (two declarations inside
one manifest are just as silent, and the generic loop correctly skips a single-pack group),
and the REMEDY is a different KIND rather than a different target — "give them different
paths" is right for `files` and useless here, because two packs wanting keys in one config
file is a legitimate intent with a correct expression, so the message has to teach the
conversion.

**The refusal names the DIVERGENCE, not just the rule.** Two packs agreeing on everything
but `managed` are still refused, but where the declarations disagree on `mode`/`path`/`codec`
the message says which field and which two values. That is the concrete damage, and a reader
who sees it does not have to take the rule on faith. In a SELF-collision both sides would
carry the same pack name, so there the labels are `declaration 1` / `declaration 2`.

`yolo pack lint` and `yolo pack footprint <dir>` hold ONE pack by construction, so they
cannot see a cross-pack collision — and the most likely one, a surface a pack yolo ships
already owns, is exactly the case an author hits. Both single-pack views therefore compare
against the (not selection-gated) embedded set and **warn** by name, with the
`config-overlay` shape to copy. A warning rather than a lint failure: whether two packs are
ever selected together is a config question these commands cannot answer, so the refusal
stays where the pack set is known.

Two things the exclusivity refusal is deliberately NOT. An `autonomy` posture patching a
surface the same pack owns is not a second declaration — it merges into the base surface's
managed layer, which is what keeps the agent packs launchable. And a `config-overlay`
alongside a `config` on one identity is the supported shape, not a clash.

### Overlay rules

- **An overlay body may set ONLY `managed`.** Every field that would redefine the *surface*
  (`agent`, `name`, `path`, `codec`, `mode`, `defaults`, `retireOnFirstRender`)
  is refused BY NAME at decode, with the rule in the message rather than a generic
  unknown-field error — each of those keys is real, it is just not a contributor's to set.
  That refusal is what makes "the contributor cannot change the file's mode, path or codec"
  mechanical instead of conventional; without it the `mode` flip comes straight back through
  the correct syntax. `defaults` is refused for a subtler reason worth stating: an overlay
  folds at exactly ONE precedence slot, so a contributor has no second, lower position to
  occupy.
- **A malformed overlay is FATAL; an ownerless one is not.** An unselected owner is not the
  author's mistake — there is nothing to fix — whereas a body that redeclares the surface is
  the author asserting something the mechanism will never honor.
- **If the target surface has no owner** among the selected packs, the overlay is **inert and
  reported by name** (R2). It neither creates the file — that would let an overlay own a
  surface by accident, the exact distinction the kind exists to draw — nor fails the launch:

  ```
  config-overlay  no effect — claude/settings has no owner (the `claude` pack is not selected)
  ```

  It also fails in the useful direction: add the owning pack later and the overlay starts
  working with no further edit.
- **An overlay onto a CORE surface is inert too, with its own message.** The kind contributes
  to a surface *another pack* owns, and core's surfaces belong to no pack — so it is reported
  as core-owned rather than as an unknown identity, and the message does not send a user
  hunting a typo that is not there.
- **`rmw` asserts an overlay's keys rather than defaulting them.** An overlay body says
  `managed` — "keep this key at this value" — so fill-if-absent would make it work on the
  first boot and then never pick up a changed value.

### Provenance, and what `config diff` can say

R3: provenance must be **user-visible**, not merely recorded, because provenance nobody can
read does not make an override legible — which was the entire justification for the kind.
`yolo config diff <agent>` therefore reads pack declarations *and* the render's own record,
and reports one of five outcomes per contributed key: `set by <pack>` when it won,
`contributed by <pack> but <layer> won` (naming the layer, measured, not guessed),
`contributed by <pack> but the key is not in the rendered file` (a tombstone dropped it), `contributed by <pack> (winner not measured at the <notch> notch — <reason>)`,
and `written by a past apply for <pack>, which no longer asserts it`.

> [!WARNING]
> **Never infer the winner from the declarations.** That inference is what made this command
> print `contributed by X but managed won` for a key no `managed` layer even declared — at
> the host notch, where no provenance sidecar is written at all, so it told users their
> overlay had lost when it had won. A confident wrong answer is worse than an unknown, which
> is why the no-record case names *which* absence it is.

An `rmw` or `computed` surface writes no provenance sidecar by design, and that reads as
"winner unknown — this surface's mode keeps no provenance sidecar" rather than as a loss:
sending a user to investigate a by-design absence is its own kind of misreport.

## Composed-file posture: what "writable" means

Every composed file has a read/write posture, from a three-way taxonomy:

- **Derived** — yolo is the sole author; the file is a pure function of layers. Made
  effectively read-only in the jail (`computed` mode); an in-jail edit is discarded next
  boot.
- **Shared** — yolo and the agent both write, on disjoint keys (`rmw`); yolo asserts its
  managed keys and preserves the agent's.
- **State** — the agent owns it; yolo only seeds and persists it (`stateful` with capture, or
  a `state` dir).

A second axis is whether a surface is **host-linked** (has a `host` layer from a `reads-host`
grant). The posture and the host-link together decide whether an in-jail edit survives, and
whether the file is safe to regenerate.

**Ownership does not carry over from the jail to the host.** Every jail path is disposable
and `:ro`; the host equivalents are the user's own files. So the host render refuses any path
it cannot prove yolo wrote, and retires its own output by ARCHIVING it under the state dir
rather than deleting (reclaimed by `yolo prune`).

Skills and briefings are **composed wholesale** at every notch, and the user's own content
MOVES into the local pack (`~/.config/yolo-jail/local/`), where yolo composes it back into
every destination. That is the point of the rule rather than a side effect: a personal skill
used to live in each agent's dir independently and drift per agent, with no command
reporting the divergence. One copy cannot diverge.

- **Precedence is the LAYER ORDER**, so the local pack — appended last — outranks every
  shared pack. The per-entry rule it replaced asked "did THIS PACK write it?", which refused
  any pack overwriting another's recorded name whatever the order; composition asks only "is
  this yolo's?", so the refusal is unrepresentable rather than handled.
- **Migration collisions are resolved by CONTENT, not by name.** Byte-identical copies of one
  name union silently. DIFFERING content is a real conflict — both survive as `<name>` and
  `<name>-from-<agent>`, warned about ONCE, at the migration, naming both sources. Losing one
  of two hand-written skills silently is the failure that rule exists to prevent.

## The derive slot

A surface whose content depends on live configuration — which MCP servers are set, which LSP
servers are enabled, which provider a profile selected — cannot be static data. That dynamic
layer is the one place a pack runs Lua, and it is tightly bounded.

A pack ships `derive.lua` at its root and registers producers. There are two registrations,
each with a key space of its own: `yolo.derive(agent, surface, fn)` produces a surface's
computed layer, and `yolo.env(agent, fn)` emits process environment (see
[`providers.md`](providers.md)).

```lua
yolo.derive("opencode", "config", function(ctx)
  -- ctx's source tables are live and read-only.
  -- return the computed layer for opencode's `config` surface.
end)
```

The contract:

- A `derive` function is a **producer**: it returns the computed layer (a config value),
  which the engine folds in at the `computed` slot. It runs *before* the merge — it does not
  mutate the composed file.
- Its inputs are **live source tables**, read-only, and the set is closed and core-owned: a
  pack *projects* from these into its tool's dialect, and never invents a new source.
  `DeriveCtx` in `internal/agentcfg/luahook` is the enumeration.
- `ctx.tombstone` is a sentinel that round-trips to Go `nil` so a key can be deleted — bare
  Lua `nil` in a table just drops the entry, which cannot express "delete this key" — and
  `ctx.empty_array` is the sentinel for an intentional empty JSON array, which Lua cannot
  distinguish from an empty object.
- It runs in the sandboxed Lua VM, and it must be deterministic.

The **canonical MCP-server type** lives in core: `name → {command, args, env}`, open and
additively versioned, so a new transport is a new optional field that never breaks an
existing projection. Each agent pack's derive projects that canonical table into its tool's
shape — folding `command`+`args` into one array and renaming keys, or tombstoning a table out
of one file because that tool keeps MCP in another.

> [!WARNING]
> **The `yolo.*` set is a version boundary, and it reads tolerantly on the rendering side.** A
> `derive.lua` is staged by the host and executed by the in-jail entrypoint, baked at the last
> `just load` — so a script may legitimately call an API newer than the build running it.
> Reading a member this build never registered yields a no-op and one warning naming it,
> rather than failing the boot; the surface's own producer still runs. This landed after a new
> registration arriving in a shipped pack bricked every jail on an older image with a
> non-function call. The trade is the same one the unknown-kind rule makes: a **typo** in a
> registration is a warning at boot rather than an error. The one strict reader left is the
> host-side env composition, `packload.AgentEnv`, which refuses an unknown member by name.

This is the whole of pack-supplied logic. There is no reshape op DSL and no pack-supplied
effect code; a projection a fixed combine rule cannot express is a `derive` function, and
nothing else in a pack executes.

## Capabilities and supersession

A **capability** *(coined in this repo, 2026-08-13)* is a **named job** — not a name for the
thing that does the job. `claude-oauth-refresh` is *"serializing OAuth token refreshes so
concurrent consumers do not burn a single-use refresh token"*; the broker loophole is one
implementation of it, and a different implementation would serve the same capability. Not a
loophole name, and not a feature flag: the capability is the **invariant**, the loophole is
the **implementation**, and a pack should only ever have to know the invariant.

Two verbs, and the asymmetry between them is a principle in its own right:

```jsonc
// a loophole's manifest.jsonc — a statement about ITSELF
"serves": ["claude-oauth-refresh"]

// a pack manifest — a claim about SOMETHING ELSE, so it costs more to make
"supersedes": [
  { "capability": "claude-oauth-refresh",
    "because": "Bedrock overrides the OAuth path entirely; no token is ever refreshed" }
]
```

**`serves` is a bare string list; `supersedes` requires an object with `because`.** A claim
about yourself is cheap; a claim about another component is not. Saying "this is my job"
needs no justification. Saying "your job does not need doing" is an assertion about code you
did not write, and the person who later finds their loophole silently absent deserves a
sentence explaining why. The mandatory `because` is **printed wherever the supersession takes
effect** — `yolo loopholes list`, `yolo doctor`, `yolo pack footprint` — so the justification
travels with the consequence.

`serves` lives on the **loophole** manifest and travels *inside* the pack, and it is refused
on a pack manifest with a message naming the distinction: a statement about an implementation
belongs with the implementation. The gate is one field read in `Loophole.Active()`:

```
Active()     = Enabled && !Superseded() && SupportedHere() && RequirementsMet()
Superseded() = serves is NON-EMPTY  AND  every served capability is superseded by some selected pack
```

Supersession is **second**, beside `Enabled`, because both are decisions a user's
configuration made where the two below are facts about the machine; `InactiveReason()`
branches in the same order, which is what stops the gate and the explanation from disagreeing.

- **`every`, not `any`.** A loophole serving two jobs, one of them superseded, still has a
  job. Superseding *all* of them retires the loophole — an arithmetic consequence of retiring
  each job, not a separate "retire this loophole" power.
- **Non-empty `serves` is required.** Silence means "not participating", never a default
  claim, so adding this mechanism cannot change the behavior of any manifest that does not
  opt in.
- **Granularity is always per job, never per component.** There is deliberately no way to say
  "turn that component off": `enabled: false` exists for that and is honest about being a
  blunt instrument where this is a statement about work.
- **Two declarations naming one capability string is the mechanism working, not a
  collision.** A capability name is an **interface** — a rendezvous point — where a skill name
  is an **identity**. Bare strings, no prefix. The footprint claim for `supersedes` is a
  display label deliberately absent from the closed kind registry, so two packs superseding
  one capability cannot be reported as a collision.

> [!WARNING]
> **Supersede is NOT provide.** The test is: *after this pack is selected, does the job still
> need doing?* `supersedes` claims the DEMAND vanished, never that SUPPLY moved — and
> superseding when you meant providing silently stops the job being done with nothing taking
> over. Nothing in the system can detect it, because "I will do it instead" is exactly the
> claim `supersedes` does not make. There is deliberately no `needs: [<capability>]`: it
> would invent conflict resolution before the conflict exists, and it is purely additive if
> it ever bites.

> [!WARNING]
> **A supersession matching no `serves` is REPORTED, loudly — not refused.** The structure is
> refused at load on both decoders (an empty `capability`, a missing `because`, a duplicate, a
> control character: all version-invariant). The MATCH cannot be, for three reasons: the claim
> is decodable long before the loopholes are (`pack lint` and the in-jail entrypoint have no
> loophole set and cannot get one — the loopholes → config → packload cycle); a pack
> superseding a capability served only by a newer manifest would brick every jail on a
> pre-`just load` image; and the failure direction of a warning is safe, because an unmatched
> claim leaves the loophole running while a refusal would take down `yolo loopholes list` —
> the command a user runs to find out what happened. The message is most of the value: *"no
> loophole serves `claude-oauth-refersh` — did you mean `claude-oauth-refresh`?"* is a fix.

There is deliberately **no yolo-owned registry of capability names**: core does not know what
an agent is, and a central registry would rebuild the registry the pack system exists to
avoid. Capabilities are declared by whoever holds the fact.

## A pack's own config keys

A loophole a pack ships gets a `settings` block under its own name —
`loopholes.<name>.settings.<key>` — whose keys are **declared and typed in the loophole's
manifest**. Core validates them at the existing validation point, resolves them from the
merged config, and writes them to a file it owns. `yolo config-ref` is the authority for the
config keys; [`../guides/loopholes.md`](../guides/loopholes.md) is the authority for the
manifest block. The four halves are `internal/loopholedecl/settings.go` (declare),
`internal/config/validate_loopholesettings.go` (validate), `internal/loopholes/settings.go`
plus `internal/cli/run/loopholesettings.go` (resolve and write).

> [!WARNING]
> **An opaque settings map is a trust regression regardless of where it is placed.** If core
> validates only "it is an object", it cannot tell one key from another — and the
> user-scope-only refusal on a loophole's `env` exists precisely to keep `LD_PRELOAD` out of a
> host daemon's spawn environment. Opacity does not dodge that rule; it launders it. So the
> keys are typed and declared, from a closed type set: a free-form JSON Schema would put
> validation outside core's hands and be a second config system.

Placement decides severity, and that is why the block lives under `loopholes.<name>`: an
unknown **top-level** key refuses the launch, while an unknown entry inside a loophole's block
is a warning. The inner key set beside `settings` stays closed, so a nested map keeps the
closure intact — an unknown key *inside* `settings` is checked against the declaration, an
unknown key *beside* it is still the existing error.

**Delivery is a file yolo owns, named by a manifest token** (`{settings}`, exactly as the
manifest already names the socket and the state dir). **A path is the one thing a spawn may
carry**, and the *contents* are written by core after validation rather than by whoever edited
the config: the workspace supplies **values**, never **environment**. It also closes a hole
rather than relocating one — a daemon reading the raw workspace file per request lets an agent
rewrite the allowlist mid-session with no relaunch and therefore no approval gate, so a
core-written file makes the value **launch-frozen**. Changing what such a daemon may reveal
requires a jail restart, which is where the config-approval gate lives.

> [!WARNING]
> **The settings file must never cross into the jail.** A loophole with a `jail_daemon` gets
> its state dir bind-mounted into the container, and an ABSENT `state_files` means the *whole
> directory* crosses — so writing resolved settings there published them to the agent,
> including a key declared user-scope whose entire purpose is to stay out of the agent's
> reach. The `0600` is no barrier: a jail's agent runs as UID 0. The combination is now
> **unrepresentable**: a manifest declaring `settings` AND a `jail_daemon` must declare
> `state_files`, and may not list the settings file in it — both refused at load
> (`loopholedecl.refuseSettingsFileCrossingIntoTheJail`, pinned by
> `TestSettingsFileMayNotCrossIntoTheJail`), so the manifest fails and the loophole vanishes
> rather than leaking. Excluding one file from a mount the author declared was rejected as a
> carve-out the author cannot see.

> [!WARNING]
> **A user-scope list is a ceiling a workspace cannot narrow — only widen.** The config merge
> union-merges every list at every depth, and the replace-wholesale exception was deleted
> precisely because a workspace value replacing the user's is what let an agent-editable file
> decide what entered the jail. So for an allowlist the weak, agent-writable scope can only
> **add** capability. The per-key `scope` field is the answer: a capability-widening key is
> declared `scope: "user"` and a workspace cannot contribute to it at all. **`user` is the
> default** — an author who says nothing gets the strict answer, and a widening key has to ask
> in writing.

A workspace **may** supply values that reach a host daemon, and the conditional is the whole
ruling: **as long as they go through the config-change gating**. A typed, declared setting core
validates and writes is a different object from an arbitrary key/value pair injected into a
process environment — but "different object" alone would not be enough. What closes the gap is
that the approval snapshot lives in host-side state the jail never mounts, and a
non-interactive launch does not auto-accept a changed config. **If either is ever reverted,
this answer goes with it:** a workspace-supplied value reaching a host daemon is only as strong
as the approval that admits it.

`enabled` is **not** pack-declared. It is universal and core declares it for every loophole;
what the manifest supplies is its *default*. One config key, one manifest field declaring its
default, and the arbitration dissolves rather than needing one.

Scope is a **host-notch** property only. Inside a jail the config directory is a read-write
bind of the workspace's own tree, so user-vs-workspace is not a boundary there.

## Selection and the load path

### The `packs` key

Packs are selected by the `packs` config key, **user scope only** — a workspace config naming
a pack is a hard error, because a workspace is agent-editable and travels with the repo.
Nothing is active by default: an empty `packs` yields a jail with no agent, and says so at
launch (`cli.warnIfNoPacks`). `internal/config/validate.go` hard-errors on the retired
`agents` key on the host.

Three source forms:

```jsonc
"packs": [
  "claude",                                              // a pack yolo ships, by bare name
  "file:///home/me/code/my-pack",                        // a local directory
  "git+ssh://git@github.com/org/repo//subdir?ref=main"   // a fetched pack (ref mandatory)
]
```

The object form adds `name` and `only`/`exclude` globs, for per-project narrowing of a shared
corpus. A config still carrying the retired `allow_exec` is refused as an unknown key, because
a key that does nothing must not be accepted quietly.

### Fetch, lock, offline launch

Fetching happens in exactly one place: `yolo pack install` (and its alias `update`).
Everything else is offline.

- A git source is cloned host-side into a content-addressed store: a bare mirror per repo, a
  checkout per commit. Fetches run with fsck-on-transfer so malformed third-party content is
  rejected at the boundary, and with terminal prompts disabled so a missing credential errors
  instead of hanging. **The jail has no git credentials by design**, so fetch is host-only.
- Because trees are keyed by commit, a moving ref never corrupts an existing checkout.
- Launch resolves pins from the local store and **never fetches**; a missing pin errors and
  points at `yolo pack install`. `yolo pack status` flags drift between the config address and
  the lock.
- The lockfile records the asked-for `source`, the resolved `commit`, and the `ref` — **and
  nothing else. There is no approval record in it, and the absence is a ruling rather than an
  omission**: a field asserting an approval nothing enforces is worse than no field, and
  `packsrc.LockEntry`'s own doc comment refuses to have one back without a design ruling. A
  launch resolves a fetched pack from the local mirror at the config's ref
  (`packsrc.Store.resolveFromStore`) and consults no lock entry, so the lockfile is
  **write-only at launch**. The mirror moves only when `install`/`update` runs — the one
  network step — so content is frozen between installs, and **choosing to follow a branch IS
  the trust decision**. A tag pin is the shape for a pack carrying host execution.

### Host-side staging, then jail-side render

- The host stages only the **selected** packs into the mounted pack tree (`YOLO_PACK_ROOT`),
  clearing it first, because **the mount is the filter**.
- A dropped pack must be UNSTAGED or it keeps rendering. The embedded root is cleared
  wholesale (it is derived from the binary's embed). Each configured pack's directory is
  pruned when its slug leaves `packs` — **contents-only, never the staging root itself**. A
  pack still configured but unresolvable this launch (an offline git remote) is **KEPT**, not
  pruned: clear-then-restage would discard a pack the user still wants merely because it could
  not be fetched.
- A declared pack that cannot be staged is a **fatal** error, and resolution failure is
  reported by name with the command that fixes it. A jail must not come up silently missing a
  pack it was told to load, and an empty pack view is **not** a deactivation signal.
- In the jail, the entrypoint renders every staged pack in **one loop with no switch on any
  tool name**: for each contribution it dispatches on `kind`. This loop is the concrete proof
  of principle 2.

## The credential boundary: disclosure, not consent

**Host access is six crossings**: a `reads-host` file read; a `mount` directory or file read;
`program` via `installer`, a curl-to-shell install URL; `briefing` with `after: "host:…"`,
prepending the user's own briefing file; a wrapped **plugin's** code-running components; and a
shipped **loophole's** daemon, intercepts, host binds and devices. Static `env` is not host
access — its values are literal strings — and neither is `derive.lua`, which is sandboxed and
whose one escape (`yolo.env`) is the same field the static `env` channel already carries
literally.

A pack has an **origin**: embedded (ships with yolo), local (a `file://` directory the user
controls), or fetched (cloned from a git ref). **Origin does not decide host access.** It names
the delivery route — a fetched pack must be `yolo pack install`ed to reach the store, and gets
a lockfile entry with a commit — and nothing more. `packload.HonoredHostFiles`,
`HonoredMounts`, `HonoredInstalls`, `HonoredLoopholes` and `HonoredPlugins` refuse nothing;
their `refused` return is retained and always nil, and a future refusal source must not quietly
refill them.

### Why there is no approval gate

Selecting a pack means writing `packs` in the user config, as the host user. `packs` is **user
scope only and inexpressible at workspace scope by construction** — that is the load-bearing
restriction, and it is the one that survives, because a workspace config travels with a repo
and is agent-editable. **An agent cannot add a pack.** So a prompt at `yolo pack install`
refuses an actor who has already passed a strictly stronger gate, which
[`../reference/gate-placement-principle.md`](../reference/gate-placement-principle.md) Test 1 calls
theatre — and `internal/config/userlayer.go` already applied the same test, the same way, to
`--user-layer`, the other route into `packs`.

> [!WARNING]
> **Do not re-add a prompt, at `pack install`, `check`, `footprint` or the entrypoint.** The
> reason is not that prompting is unpleasant. The gate's original containment rationale — *a
> fetched pack must not `curl | sh`* — was refuted in-house: `npm install -g` runs
> `postinstall` from the same fetched tree, ungated, so the set refused one path to arbitrary
> in-jail execution while permitting another. **The absence is pinned, not merely documented.**
> `internal/packload/hostaccessgates_test.go`'s `TestNoFetchedPackHostAccessGateExists` walks
> every non-test `.go` file under `internal/`, `cmd/` and `packs/` over the **AST** and fails if
> any of fourteen named retired gate identifiers reappears (comments are exempt by
> construction, because the prose recording the deletion has to be able to *name* what it
> deleted). Its sibling `TestTheDisclosureThatReplacedTheGateIsStillWired` fails if the
> disclosure that replaced it goes missing, and `internal/cli/run/packnohostgate_test.go` is
> the behavioural half. **A hit in any of them is a claim that the ruling was reversed, and
> that belongs in [`../design/trust-paths.md`](../design/trust-paths.md) before it belongs in a
> `.go` file.** Deleting a row from the list to get green is the specific failure the test was
> rewritten to prevent: its predecessor enumerated the two gates *by name*, so a third gate
> copied into `yolo check` would have satisfied it vacuously. A scan for zero gates has no such
> hole.

> [!WARNING]
> **`npm install -g` and `curl | sh` really are treated alike now, and the asymmetry that
> remains is deliberate with a stated reason.** A reader who re-derives *"npm runs postinstall,
> therefore gating only the installer is inconsistent"* has found a real fact — it is just no
> longer an open finding. The distinction that survives is **when the bytes change**, not whose
> they are: an unversioned npm declaration re-checks the registry hourly, and a selector turns
> that off. Containment is not the discriminator, and cannot be: an npm package, an installer
> script, and a pack-shipped jail-side binary all execute inside the same sandbox.

> [!WARNING]
> **A digest-pinned installer script is not a digest-pinned binary.** An installer pinned by
> digest gives you *"the recipe is pinned"*, not *"what runs is pinned"*: the script fetches
> more at run time and those fetches are unpinned. A binary artifact has no second hop — the
> digest covers exactly what executes. Any rule that treats the two as equivalent because both
> carry a digest is overselling the installer case. This is why a pack-shipped downloaded
> artifact carries a mandatory `sha256`: the lockfile's commit pins the pack's *tree*, and a
> downloaded artifact is not in that tree, so without a digest "pinned pack" silently means
> "pinned manifest, unpinned executable".

### Transparency at every launch

The startup banner lists what each loaded pack reads this launch — its mounts, host-file
reads, installer URLs, host-prepended briefings, and env. **Disclosure is not consent**, and
that is the point: it stays because it tells the user what actually crossed, which no prompt at
install time can do.

Reads and executions print in different places, and the split is not cosmetic.
`run.notePackHostAccess` prints reads on the banner; host **execution** prints at the spawn
boundary, *before* the daemon starts (`startLoopholesDisclosed`), because for an exec a banner
line would be a notification that something already happened.

> [!IMPORTANT]
> **Which kinds the disclosure covers is DATA, not a switch at the print site.**
> `disclosureClasses` in `internal/cli/run/packloopholes.go` classifies every kind in
> `packdecl`'s closed set into read / exec / skip, and
> `TestDisclosureClassifiesEveryKnownKind` fails when a kind is added and not classified. The
> hardcoded set it replaced was wrong for a year and nothing noticed, because "which kinds does
> the disclosure cover" was a fact only the printer knew. An *unclassified* kind defaults to
> exec, so a new kind's claims are announced before anything spawns even before someone writes
> the row down — correct at runtime, loud in review.

> [!WARNING]
> **A wrapped plugin's `hooks` and `mcpServers` are reported under the `skills` kind, which the
> banner's filter classifies as skip.** They therefore show in `yolo pack footprint` and in **no
> launch banner**, while the agent runs the hook at every tool call. Tracked as
> [`OQ-TP10`](../design/trust-paths.md#-oq-tp10--a-wrapped-plugins-hooks-reach-the-agents-lifecycle-and-appear-in-no-launch-banner) in [`../design/trust-paths.md`](../design/trust-paths.md), and pinned where the behaviour
> actually is by `run.TestWrappedPluginHooksAreDeliveredAndDisclosed`, whose doc comment names
> the banner as the gap.

## Command surface

| Verb | What it does |
| :--- | :--- |
| `yolo pack init [dir]` | scaffold a valid skeleton (`AGENTS.md`, an example skill, `README.md`); never a `pack.json` |
| `yolo pack lint [dir]` | run the real staging executor **and** validate the manifest — every problem, not the first — then print the pack's footprint |
| `yolo pack ls` | list configured packs and what each stages |
| `yolo pack explain <name>` | stage one pack and show what it stages and what it dropped (`file://` local only) |
| `yolo pack footprint [ref]` | claims + cross-pack collisions + review summary; `[ref]` may be an embedded pack name or a local path, so you can inspect a pack you are authoring |
| `yolo pack install` / `update` | fetch configured packs, materialize each commit into the store, write the lockfile, report whether each pin **moved**, prune the entries of packs that left the config — the only network step |
| `yolo pack status` | show locked commits and flag config/lock drift |

**No `yolo pack` verb asks a question, and `packMain` takes no stdin at all.** `install` and
`update` fetch and report; every other verb inspects. That is a property of the whole surface
rather than an omission from one row: the only reader ever threaded through here was the
fetched-pack approval prompt. `install` and `update` share one body and differ only in name and
intent — the distinction a user cares about is *did my pins move*, which the output reports
directly.

## What this does not license

- **Not a top-level config key per pack.** A contribution kind that declared one dies four
  ways: the inherit census is static and total-by-test in *both* directions, the inherit filter
  silently drops an unclassified key at the jail boundary, and the kind registry demands a
  Combine rule and a footprint claim string.
- **Not a route to `env` for a host daemon.** Settings are values written by core into a file;
  they are not a key/value channel into a process environment.
- **Not a reordering of validation after staging.** Two of the three `ValidateConfig` callers
  never stage at all, so there is nothing to reorder against.
- **Not pack content as an image input.** Content and config values are read at compose time;
  nothing about them needs to be in the derivation, and baking them would make editing a prompt
  cost a rebuild.
- **Not a claim that scope is a boundary in-jail.** User-vs-workspace is a host-notch property.
- **Not a second implementation of anything the framework owns.** A pack points at a loophole
  module rather than inlining its manifest; a pack projects from core's derive sources rather
  than inventing one; a pack requests a named hook rather than shipping its body.

## Why it's this way

Rulings a future change would otherwise undo, kept with their original IDs — those IDs are
cited from code comments and sibling docs, and this appendix is where they resolve. The two
rows marked *(profiles)* resolve in
[`../design/profiles-as-pack-variants.md`](../design/profiles-as-pack-variants.md)'s ledger,
which is still the index for that arc's other rulings.

| Ruling | Why it holds |
| :--- | :--- |
| **R1** — a same-identity `config` declaration is a loud collision, not a merge | The `mode` flip is a general defect in the mechanism, not one pack's impoliteness; matching the owner's mode fixes one pack and leaves the next one able to do the same damage. |
| **R2** — an ownerless `config-overlay` is inert and reported by name | Creating the file would let an overlay own a surface by accident; failing the launch would make an unselected pack an error. It also fails in the useful direction. |
| **R3** — provenance must be USER-VISIBLE, in `yolo config diff` | Provenance nobody can read does not make an override legible, which was the whole justification for the kind. |
| **R4** — the double `rendered` line is fixed by REFUSING, not deduping | Deduping hides the clash; refusing removes the state that produced the second line. |
| **R5** (corrected) — a user-scope list is a ceiling a workspace can only WIDEN | Lists union-merge at every depth and the replace-wholesale exception was deleted deliberately, so "the weak scope is bounded by the strong one" is false for any list-shaped setting. |
| **[OQ-K1](#why-its-this-way)** — settings declarations are AUTHORITATIVE, never advisory | Launch is strictly offline and an unresolvable pack is already fatal, so there is no launch where a configured pack's declaration is missing and the jail starts anyway. |
| **[OQ-K2](#why-its-this-way)** — a workspace may supply values reaching a host daemon, gated by the config-change flow | The conditional IS the ruling: it would have been unsafe before the approval snapshot moved out of the workspace and non-interactive auto-accept was removed. |
| **[OQ-K3](#why-its-this-way)** — freeze the host-processes visibility list | Live re-read of the workspace file is indistinguishable from the hole: the same property that lets you widen without restarting lets an agent widen its own, mid-session, with no approval gate. |
| **[OQ-K4](#why-its-this-way)** — a loophole with a top-level core config key becomes an ordinary pack | The scope rule arrives for free by making it ordinary; leaving the key in core is separation in appearance only. |
| **[OQ-CAP](#why-its-this-way)** — `supersedes` is a top-level manifest key, not a kind | A contribution that contributes nothing is a category error; the thing that IS a contribution is the loophole. Pinned rejected by `TestSupersedesIsNotAContributionKind`. |
| **[OQ-TP6](../design/trust-paths.md#decision-ledger)** — a refused pack contribution refuses the launch | Withheld-with-a-notice let a jail come up missing what it was told to load. |
| **[OQ-TP9](../design/trust-paths.md#decision-ledger)** — no fetched-pack host-access approval gate; disclosure replaces it | The gate refused an actor who had already passed a stronger one, and its containment rationale was refuted by `npm install -g` running `postinstall` ungated. |
| **[OQ-1](../design/profiles-as-pack-variants.md#14-decision-ledger)** (profiles) — `autonomy` and `profile` stay two kinds | The confinement-conditional keys live in `autonomy` "and nowhere else", and the selectors are asymmetric: autonomy keys off the constructor-only fail-closed notch, profile names arrive through a merge an agent can edit. |
| **[OQ-16](../design/profiles-as-pack-variants.md#14-decision-ledger)/[OQ-17](../design/profiles-as-pack-variants.md#14-decision-ledger)** (profiles) — the `profile` gate on `config-overlay` reads user-scope config at the HOST notch | A gated overlay rewrites real-home keys and a workspace config is agent-editable; inside a jail the blast radius is the disposable home, so the jail notch uses the full effective table. |
| **Q1.3** — `requires` is its own kind, CombineShared | Install and presence are different claims; conflating them made a pack either lie about a baked binary or lose its `install_hints` entirely. `requires` owns no path, so many packs requiring one binary is not a collision. |
| **Q2.1** — several `program` contributions per pack, each with its own launcher | Exclusivity is per `bin`; `shellcheck` + `shfmt` in one pack is ordinary, and there is no case for constricting packs. |
| **Q3.1** — prune only unconfigured slugs, contents-only; keep the unresolvable | Clear-then-restage would discard a pack the user still wants because it could not be fetched; the staging root's inode is captured by a live jail's bind. |
| **WB-D9..D12** — `needs` names an embedded pack, resolves before staging, joins user selection, and always prints | A fetched pack needs-ing another would make selection a supply-chain channel; a silent join is the one forbidden behavior. |

## Current values

Verified at `a3922298`. The prose above explains what each of these is for; this table is the
only place the values themselves are stated.

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| Kind set | the `footprints` map key, from which `KnownKinds()` derives (sorted alphabetically) | `packdecl.footprints`, count-pinned by `packdecl.TestKnownKindsCoverEveryConstant` |
| Hook set | `shared_credentials`, `per_jail_history`, `claude_plugins` | `packdecl.KnownHooks`, drift-pinned by `entrypoint.TestHookSetsAgree` |
| Manifest top-level keys | `name`, `description`, `contributes`, `skills_tier`, `supersedes`, `needs` | `packdecl.Manifest` |
| Conventional briefing source | `AGENTS.md` (alone since 2026-08-17) | `packdecl.DefaultBriefingFiles` |
| Conventional skills source | `skills/` | `packdecl.Contribution.SkillsSource` |
| Blocker dir (first on PATH) | `~/.yolo/bin/block` | `entrypoint.BootPath` |
| Launcher dir (second on PATH) | `~/.yolo/bin/launch` | `entrypoint.BootPath` |
| PATH order | `block : launch : $NPM_CONFIG_PREFIX/bin : <mise-shims> : $GOPATH/bin : $HOME/.local/bin : /run/yolo/packages/bin : /bin : /usr/bin` | `entrypoint.BootPath` (and two independently-written copies: the `.bashrc` export, `macosuser.SandboxPath`) |
| Shim bypass | `YOLO_BYPASS_SHIMS=1` | `internal/entrypoint` |
| Unversioned npm launcher poll | hourly | `entrypoint` launcher generation |
| `install_hints` managers | `brew`, `brew-cask`, `apt`, `dnf`, `pacman`, `nix` | `packdecl` install-hints validation |
| Mounted pack root | `YOLO_PACK_ROOT` | `internal/cli/run/packs.go`, `packsrc.Store` |
| Host staging root | `<global storage>/agents/<container>/packs/<slug>` | `paths.AgentsDir`, `PackEntry.Slug` |
| Lockfile | `~/.config/yolo-jail/packs.lock.json` (beside the user config) | `packsrc/lock.go` |
| Conventional local pack | `~/.config/yolo-jail/local` (`AGENTS.md`, `skills/`) | `paths.LocalPackDir` |
| Config-surface layer order | `defaults < host < workspace < config-overlay < capture-overlay < computed < managed` | `internal/agentcfg` |
| Surface modes | `stateful` (default), `computed`, `rmw`, `unrendered` | `internal/agentcfg/manifest` |
| `state` scopes | `workspace` (default), `machine` (requires `because`) | `packdecl` |
| `skills_tier` values | `""` / `flat` (default), `namespaced` | `packdecl.Manifest.SkillsTier` |
| Derive registrations | `yolo.derive(agent, surface, fn)`, `yolo.env(agent, fn)` | `internal/agentcfg/luahook` |
| Derive ctx sentinels | `ctx.tombstone`, `ctx.empty_array` | `luahook/derive.go` |
| Derive ctx sources | `ctx.mcp_servers`, `ctx.lsp_servers`, `ctx.providers`, `ctx.selected_provider`, `ctx.profile_name`, `ctx.profile` | `luahook.DeriveCtx` |
| Loophole settings token | `{settings}` in a manifest `cmd` | `internal/loopholes/settings.go` |
| Settings types | `string`, `bool`, `int`, `string_list` | `loopholedecl/settings.go` |
| Settings scope default | `user` | `loopholedecl/settings.go` |
| Cgroup loophole name constant | `paths.BuiltinCgroupLoopholeName` | `internal/paths` |
| Core-owned config surfaces | `mise/config` and nothing else | `agentcfg.BuiltinManifest` |
