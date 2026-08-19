---
title: "Letting a pack declare its own config keys"
date: 2026-08-17
status: accepted
tags: [packs, config, loopholes, design]
summary: "Core's schema names two loopholes by hand. This is how a pack declares its own settings instead — typed, name-scoped, advisory when unseen, and delivered through a file yolo owns rather than an env channel the workspace controls."
---

# Letting a pack declare its own config keys

**Status:** BUILT 2026-08-18. All four questions ruled — see the Decision Ledger — and the mechanism
they describe now exists end to end:

| Half | Where |
| :--- | :--- |
| the manifest DECLARES (`settings`, typed, per-key `scope`) | [`internal/loopholedecl/settings.go`](../../internal/loopholedecl/settings.go) |
| core VALIDATES (`loopholes.<name>.settings`) | [`internal/config/validate_loopholesettings.go`](../../internal/config/validate_loopholesettings.go) |
| core RESOLVES + WRITES (`{settings}`) | [`internal/loopholes/settings.go`](../../internal/loopholes/settings.go), [`internal/cli/run/loopholesettings.go`](../../internal/cli/run/loopholesettings.go) |
| the first consumer, frozen (OQ-K3) | [`internal/hostprocesses/`](../../internal/hostprocesses) |

**Both of those steps have since SHIPPED (2026-08-18)**, which is what the mechanism was for.
`host-processes` and `journal` are official packs, and both top-level keys are REFUSALS naming their
replacements — so core's config schema names no loophole at all, which is §1.4's whole point in
[`loophole-activation.md`](./loophole-activation.md). `journal`'s settings landed as ONE BOOLEAN
rather than the ported three-valued string; §5.2 says why.

**One thing the build tightened past what §2.2 wrote.** The design left an undecodable declaration as
"treat as a refusal"; the implementation makes that refusal apply in the TOLERANT decoder too, which
is the only placement that achieves it — see the note under OQ-K1 below.

**This unblocks the rest of the loophole sprint.** It was the gate on the three conversions that
actually empty `bundled_loopholes/`: `host-processes` and `audio` becoming packs, the broker's
loophole moving into `packs/claude`, and `journal`/`cgroup-delegate` becoming manifest loopholes.
Sequenced in [`roadmap.md`](../plans/roadmap.md). Constraints established by reading the code; anchors
inline.

**The short version.** A loophole gets a `settings` block under its own name —
`loopholes.<name>.settings.<key>` — whose keys are **declared and typed in the pack's manifest**, not
opaque. Core validates them at the existing validation point using the resolver it *already* injects,
degrading to a **warning** rather than an error when the declaration cannot be seen. Values are
delivered by **yolo writing a settings file it owns** and handing the daemon a path, because no
existing channel can carry them without handing the workspace an env channel into a host daemon.

**Why it is needed:** core's config schema names `host_processes` and `journal` by hand
(`config.go:59`, `validate.go:557-570`, `inherit.go:116-121`). Converting a loophole to a pack while
leaving its key in core is separation in appearance only — [`loophole-activation.md`](./loophole-activation.md)
OQ-A8.

> [!IMPORTANT]
> **The single most decisive constraint, and it kills the obvious design.** An **opaque** settings map
> is a trust regression regardless of where it is placed. If core validates only *"it is an object"*,
> it cannot tell `settings.visible` from `settings.ld_preload` — and the user-scope-only refusal on
> `env` exists precisely to keep `LD_PRELOAD` out of a host daemon's spawn environment
> (`validate_loopholes.go:11-22, 196-201`). Opacity does not dodge that rule; it launders it. **So the
> keys must be typed and declared.** This overrules my own earlier leaning in OQ-A8, which proposed
> exactly the opaque map.

---

## Decision Ledger

| ID | Ruling / Decision | Date | Settled in |
| :--- | :--- | :--- | :--- |
| **OQ-K1** | Declarations are **authoritative**. The advisory case does not exist — an unresolvable pack is already a fatal launch, not a degraded one | 2026-08-18 | [§2.2](#22-validation-happens-where-validation-already-happens) |
| **OQ-K2** | A workspace **may** supply values that reach a host daemon, **gated by the config-change flow** — which became a control only when OQ-D1/D2 shipped | 2026-08-18 | [§3b](#3b-workspace-scope-and-what-makes-it-safe-now) |
| **OQ-K3** | **Freeze** `host_processes.visible`. No live reload; changing it needs a restart, which is where the approval gate lives | 2026-08-18 | [§5.1](#51-host_processesvisible-stops-being-live) |
| **OQ-K4** | `journal` becomes a **pack**, settings under `loopholes.journal.settings`, and its top-level core key is deleted · ✅ **BUILT 2026-08-18** | 2026-08-18 | [§5.2](#52-journal-becomes-a-pack-and-its-top-level-key-goes) |

---

## 1. The four constraints that shape it

Everything below follows from these. Each was measured.

**C1 — Placement decides severity.** An unknown **top-level** key is a hard error that refuses the
launch (`validate.go:46,92-101` → `preflight.go:48-56`, exit 1). An unknown **`loopholes.<name>`**
entry is only a *warning* (`validate_loopholes.go:168-178`). Since a pack's declaration is not always
visible (C2), the block must live under `loopholes.<name>`, where not-knowing degrades.

**C2 — The pack view is partial, not absent, and its emptiness is not a deactivation signal.**
`resolvePackLoopholeModules` skips any pack it cannot resolve — *"never fetched, moved remote,
offline — not a deactivation signal"* (`packs.go:600`). In-jail, **embedded** packs resolve while
fetched ones do not. So "the pack did not resolve" must never mean "the key is unknown", or a valid
config becomes a refused launch on exactly the launches (offline, nested) the codebase already ruled
must boot.

**C3 — There is no channel from core to a loophole daemon.** Verified, and it is the finding that
forces new work: the manifest spawns `--socket {socket}` with no config argument
(`manifest.jsonc:48`), `startExternalService` never sets `cmd.Dir` (`loopholesruntime.go:614`), and
`YOLO_HOST_PROCESSES_CONFIG` is set by **nobody**. `host_processes.visible` is per-workspace *only*
because the daemon opens the raw workspace file itself from an inherited cwd
(`hostprocessescmd.go:54-62`). That is a coincidence of process cwd, not a designed path.

**C4 — Anything reaching a host daemon's spawn is user-scope-only, by ruling.** `env` is refused at
workspace scope because *"'env' reaches a host daemon's spawn environment, and the file is
agent-editable"*. Only `enabled` and `jail_env` are either-scope. **A workspace-writable map
serialized into the daemon's spawn is `env` under a new name.**

---

## 2. The design

### 2.1 The manifest declares; the config supplies

```jsonc
// in the loophole's manifest — the pack author declares WHAT is configurable
"settings": {
  "visible": {
    "type": "string_list",          // closed type set, no free-form schema
    "scope": "workspace",           // "workspace" (either) or "user" (user config only)
    "default": [],
    "description": "process names this loophole may reveal"
  }
}
```

```jsonc
// in a config — the user supplies values, under the loophole's own name
"loopholes": {
  "host-processes": {
    "enabled": true,
    "settings": { "visible": ["sway", "waykeeper"] }
  }
}
```

**Why declared rather than opaque** is the callout above. **Why `settings` rather than reusing the
block's existing keys**: the block's inner key set is deliberately closed
(`knownLoopholeOverrideKeys`), and a nested map keeps the closure intact — an unknown key *inside*
`settings` is checked against the pack's declaration, an unknown key *beside* it is still the existing
error.

### 2.2 Validation happens where validation already happens

The ordering objection is real but **not a wall**: `ValidateConfig` already takes an injected
`LoopholeResolver` (`validate.go:23-39`), whose lazy pack-backed implementation is registered by
`init()` and is live on the launch path. **Core already validates pack-declared facts today** — that
is how a `loopholes.<name>` entry naming a pack's loophole is accepted. This design extends the same
seam by one level: the resolver already answers *"does this loophole exist?"*, and now also answers
*"what settings does it declare?"*.

> [!WARNING]
> **The resolver's severity contract must be inverted for this use, and reusing it unchanged is a
> trap.** Its documented behaviour is *silent-and-empty on every failure*. That is fail-**safe** for
> the name question — an unseen loophole degrades to a warning — and fail-**loud-and-wrong** for the
> key question, where the identical failure would yield `unknown key` and exit 1 **on a correct
> config**. So: an unseen declaration means *unvalidated*, never *invalid*. See OQ-K1.


**RULED (OQ-K1, 2026-08-18): declarations are AUTHORITATIVE, and the case that argued for advisory
does not exist.** The question assumed core might be unable to see a pack's declaration at launch and
therefore had to accept values unvalidated. *"Either we already fetched it previously and we use it,
or we haven't and need to be online. We don't launch half jails."*

**The code already works that way**, verified 2026-08-18:

- **Launch is strictly offline by design** — it resolves from the local store and never reaches out
  mid-boot ([`store.go`](../../internal/packsrc/store.go#L5-L12)). A previously-installed pack
  therefore resolves fine with no network.
- **A pack that cannot be resolved is a FATAL launch error**, not a degradation:
  [`packs.go`](../../internal/cli/run/packs.go#L713-L717) — *"Resolution failure is reported later by
  packRoot as a fatal error naming `yolo pack install`; it is emphatically not a deactivation
  signal."* The message even names the command that fixes it.

So there is no launch in which a configured pack's declaration is missing and the jail starts anyway.
Validation keeps its teeth, and the typo protection C2 traded away is not traded away after all.

> [!NOTE]
> **One narrower case does survive, and it is governed elsewhere.** A pack can resolve perfectly while
> *this build's decoder* does not understand a declaration — ordinary version skew between a host CLI
> and a manifest written for a newer one. That is not "unseen because unresolvable"; it is the skew
> tolerance `packload` already implements, and the same reasoning applies: core must not hand a host
> daemon a value it could not validate. Treat an undecodable declaration as a refusal, not as a
> silent pass-through.

**Built, and it needed to be sharper than "treat as a refusal" reads.** The refusal lives in
`parseSettings` and fires in **`DecodeTolerant` as well as `Decode`** — the one placement that
actually delivers the paragraph above. Every other cross-version tolerance in `loopholedecl` exists
because a key only a newer build knows must not make a loophole vanish; a settings declaration
inverts that, because the unknown key is a CONSTRAINT and dropping it means validating a value
against half a rule. A manifest writing `{"type": "string", "enum": ["a","b"]}` under an older yolo
must not have its `enum` ignored and then have core accept anything.

The cost is named rather than discovered, and it is the same one the `enabled` retirement already
accepts: the manifest fails to load, so `loadFromDir` warns and the loophole is ABSENT. No loophole
means no daemon and no values — fail-closed in every direction, which is what makes the refusal
affordable here. Pinned by `TestUnknownSettingDeclarationKeyIsRefusedByBOTHDecoders`.

### 2.3 Delivery: a file yolo owns, named by a token

Since no channel exists (C3) and the obvious one is forbidden (C4), yolo creates one:

1. Core resolves the settings from the merged config and validates them against the declaration.
2. Core writes them to a file **it owns**, in the loophole's state dir.
3. The manifest names it with a token, exactly as it already names the socket and the state dir:
   `"cmd": ["yolo","internal","daemon","host-processes","--settings","{settings}"]`.

This is allowed where an `env` map is not, and the difference is not cosmetic: **a path is the one
thing a spawn may carry** (`run.go:541-545` states it as a bootstrap invariant), and the *contents*
are written by core after validation rather than by whoever edited the config. The workspace supplies
**values**; it never supplies **environment**.

**It also closes a hole rather than merely relocating one.** Today the daemon reads the raw workspace
file per request, so an agent can rewrite `yolo-jail.jsonc` mid-session and the host daemon's
allowlist changes on the next request — no relaunch, no gate, and the config diff is not in that
causal path at all. Reading a core-written file makes the value **launch-frozen**, which is a
deliberate behaviour change and the subject of OQ-K3.

> [!WARNING]
> **The state dir is where the file lives, and the state dir is also a thing that CROSSES —
> found 2026-08-18, adversarially.** A loophole with a `jail_daemon` gets its state dir
> bind-mounted into the container, and an ABSENT `state_files` means the *whole directory*
> crosses (the historical whole-dir mount, narrowed by issue #33 only for manifests that name
> their files). So "core writes it into the loophole's state dir" quietly published the resolved
> settings of any loophole that also ran a jail daemon — including a key declared `scope:
> "user"`, whose entire purpose is to keep its value out of the agent's reach. The `0600` the
> writer relies on is no barrier: a jail's agent runs as UID 0.
>
> The file stays in the state dir; what changed is that the combination is now **unrepresentable**.
> A manifest declaring `settings` AND a `jail_daemon` must declare `state_files`, and may not
> list the settings file in it — both refused at load, so the manifest fails and the loophole
> vanishes rather than leaking (`loopholedecl.refuseSettingsFileCrossingIntoTheJail`, pinned by
> `TestSettingsFileMayNotCrossIntoTheJail`). The alternative — excluding one file from a mount
> the author declared — was rejected as a carve-out the author cannot see.

### 2.4 `enabled` stays core's, and OQ-A9 falls out

`enabled` is **not** pack-declared — it is universal, and core declares it for every loophole. What
the manifest supplies is its *default*. That is exactly [`loophole-activation.md`](./loophole-activation.md)'s
**OQ-A9** recommendation arriving for free: one config key (`enabled`), one manifest field declaring
its default (`default_enabled`), and the collision dissolves rather than needing an arbitration.

---

## 3. A correction this forces — R5 is false for lists

> [!CAUTION]
> [`loophole-activation.md`](./loophole-activation.md) **R5** says the weak scope is *"bounded by the
> strong one"*. **That is false for any list-shaped setting**, and `host_processes.visible` is the live
> case.
>
> `MergeConfig` union-merges **every list at every depth** (`load.go:63-118`), and the
> replace-wholesale exception was **deleted** — precisely because a workspace value replacing the
> user's is what let an agent-editable file decide what entered the jail. Pinned by a test over every
> top-level key.
>
> So a user-scope *ceiling* list that a workspace *narrows* is *inexpressible*. A workspace can only
> **widen**. For an allowlist — which is what `visible` is — that inverts the intended safety
> property: the weak, agent-writable scope can only add capability.

The design's answer is the per-key `scope` field: a capability-widening list can be declared
`scope: "user"`, and then the workspace cannot contribute to it at all. But R5's general claim has to
be corrected rather than relied upon.

> [!NOTE]
> **What shipped for `visible`, and why it is not the `user` this paragraph reaches for.** The
> manifest declares **`scope: "workspace"`**, matching §2.1's own example and — more importantly —
> matching what the key has always been: the old manifest's description read *"Workspace controls
> visibility via the top-level `host_processes.visible` list"*, and every existing config that uses
> the feature at all uses it from a workspace file. Declaring `user` here would have made the
> migration a **second** break on top of the freeze, silently un-configuring every such workspace at
> the moment the new spelling was adopted, and OQ-K3 froze only the key's LIVENESS.
>
> The paragraph above is still the argument that decides it, and what it decides is that the
> **default** is `user`: an author who says nothing gets the strict answer, and a widening key has to
> ask in writing. `visible` asks. If the ceiling-vs-widening question is to be answered differently
> for this key, that is a deliberate second break with its own release note — not a side effect of
> moving where the key is spelled.

---

## 3b. Workspace scope, and what makes it safe now

**RULED (OQ-K2, 2026-08-18): yes, a workspace may supply values that reach a host daemon — *as long
as they go through the config-change gating*.** The conditional is the whole ruling, not a caveat on
it.

§4.3b refuses `env` at workspace scope on the grounds that a workspace file travels with the repo and
is agent-editable. A **typed, declared** setting that core validates and writes is a different object
from an arbitrary key/value pair injected into a process environment — the per-key `scope` field is
what makes that statable rather than assumed. But "different object" alone would not be enough; what
closes the gap is that the config-approval gate is now a **control** rather than a courtesy:

- the approval snapshot moved out of the workspace to host-side state the jail never mounts, so the
  record of what you approved is no longer writable by the thing being approved (`config-safety.md`
  OQ-D1, shipped 2026-08-18);
- a non-interactive launch stops auto-accepting a changed config, so the gate cannot be skipped by
  running without a terminal (OQ-D2, shipped the same day).

> [!IMPORTANT]
> **This ruling is dated for a reason: it would have been unsafe a day earlier.** Before those two
> landed, "gated by the config-change flow" meant gated by a diff whose baseline an agent could
> rewrite, on a launch that auto-accepted with no TTY. If either is ever reverted, this answer goes
> with it — a workspace-supplied value reaching a host daemon is only as strong as the approval that
> admits it.

## 4. What this does not license

- **Not** a top-level key. A sixteenth contribution kind that declares one dies four ways: the inherit
  census is static and total-by-test in *both* directions, `FilterInherit` silently drops an
  unclassified key at the jail boundary, and the kind registry demands a Combine rule and a footprint
  claim string. Precedent: `supersedes` was made a top-level manifest key rather than a kind for
  exactly this reason.
- **Not** a free-form JSON Schema. A closed type set keeps validation in core's hands; an arbitrary
  schema language is a second config system.
- **Not** a reordering of validation after staging. Two of the three `ValidateConfig` callers never
  stage at all (`yolo check`, `config-dump`), so there is nothing to reorder against.
- **Not** a route to `env`. Settings are values written by core into a file; they are not a key/value
  channel into a process environment, and the §4.3b ruling stands untouched.
- **Not** a claim that scope is a boundary in-jail. Inside a jail `/home/agent/.config` is a rw bind of
  the workspace's own tree, so user-vs-workspace is a **host-notch** property only.

---

## 5. Migration

**Cheap and mechanical:** the new inner key, the inherit entry (free — `loopholes` is already
classified `{preflight, nested}`), the `config_ref.txt` line, and the shape dispatch so a
settings-only entry does not error `command: required`.

**Expensive, and not a rename:** `host_processes` and `journal` must **stay** in
`knownTopLevelConfigKeys` through any migration, or every existing config becomes a launch-refusing
error — the file's own retirement precedent (`agents`, `repo_path`) is the pattern. And the alias must
be honored **where the value is read**, not only in the validator: `journal` needs it inside
`resolveJournalMode`, `host_processes` inside `internal/hostprocesses`, a package with no access to
the merged config. A validator-only alias makes `yolo check` go green while the daemon honors the old
spelling forever.

---

### 5.1 `host_processes.visible` stops being live

**RULED (OQ-K3, 2026-08-18): freeze it. No live reload, for now.** Today the host-processes daemon
re-reads the workspace config **on every request**, so editing `visible` takes effect without a
restart. That affordance is real, and it is indistinguishable from the hole: the same property that
lets you widen an allowlist without restarting lets an **agent** widen its own, mid-session, with no
launch and therefore no approval gate.

Frozen means the value is resolved once, by core, into the settings file the loophole is handed at
launch. **Changing what `yolo-ps` may show now requires a jail restart** — which is exactly the point,
because a restart is where the config-approval gate lives.

If live reload is wanted back later, it re-reads **core's file**, refreshed by an explicit act — never
the workspace config directly.

### 5.2 `journal` becomes a pack, and its top-level key goes

**RULED (OQ-K4, 2026-08-18): pack-declared, like everything else.** *"It should be pack declared and
turned into a pack, just like all the others. Gating is done by the loophole settings object as with
others, no special core jsonkey."*

So the answer is not merely "give it a scope rule" — the scope rule arrives for free by making it
ordinary. Three consequences:

- **The top-level `journal` key is deleted from core's schema.** With `host_processes` going the same
  way, that removes **both** of the loophole names core currently hardcodes
  (`config.go:59`, `validate.go:557-570`, `inherit.go:116-121`) — which is what makes the conversion
  mean something rather than moving a file, per `loophole-activation.md` §1.4.
- **Its settings live under `loopholes.journal.settings`**, typed and declared in its own manifest,
  validated and delivered exactly like every other loophole's. `journal: "full"` — today an
  agent-settable host-journal passthrough with **no scope rule at all** — becomes a declared key with
  a `scope`, which is the security half of this ruling.

  **BUILT as `full: bool`, `scope: "user"`, and the TYPE is a second security decision the ruling
  did not name.** The obvious port — a `string` mode carrying `off | user | full` — is
  unvalidatable: the type set is closed and has no `enum`, so core could not refuse `"usr"`, and
  `ParseRequest` narrows on the exact literal `"user"`, meaning **every other spelling behaves as
  full**. A config typo would have been a silent widening of host access. Two of the three values
  were also saying something `enabled` already says, so what was left to declare was one bit.
  `off` is `enabled: false`; `user` is `enabled: true`; `full` is that plus the setting, from the
  user config only.
- **It composes with `loophole-activation.md` OQ-A6**, which already ruled that `journal` and
  `cgroup-delegate` become manifest loopholes *in* this sprint. K4 supplies the settings half that
  ruling needed.

> [!WARNING]
> **Both of these are user-visible breaks and need release notes when they ship**, not before:
> `host_processes.visible` stops applying without a restart, and a top-level `journal` key stops being
> recognised. The second needs a migration path — a config that still writes it must be told where it
> went rather than silently ignored, which is the deletion-shaped-change rule §4 of
> `loophole-activation.md` already argues for.
>
> **All of them are written, 2026-08-18** — 📄 [`RELEASE-NOTES.md`](../RELEASE-NOTES.md) carries four
> entries from this sprint. The `journal` one has to carry THREE instructions rather than the
> expected one, and that is the thing to copy for the next conversion: migrating the value alone
> leaves `yolo-journalctl` just as broken, so the message names the pack selection, the `enabled`
> switch, and the setting — plus the FILE the setting has to go in, because `full` is user-scope and
> a workspace supplying it is refused by name.

## 6. Risks

| Risk | Mitigation |
| :--- | :--- |
| A pack declares everything `scope: "workspace"`, so an agent can set it | Installing the pack was already a decision, and the origin gate still applies — but core should **default** a setting reaching a host daemon to `user`, mirroring §4.3b, so silence is the safe choice |
| The typed set is too narrow for a real pack | Start with `string`, `bool`, `int`, `string_list`; widening a closed set later is additive, unlike narrowing |
| An unseen declaration silently accepts a typo forever | It is the price of C2, and it is bounded: the key is namespaced under a loophole, so the blast radius of a typo is one ignored setting rather than a wrong one. OQ-K1 |
| The settings file becomes a second config system | Keep it a flat resolved map, written by core, read once — no includes, no layering, no comments |

---
