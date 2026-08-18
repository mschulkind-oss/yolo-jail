---
title: "Letting a pack declare its own config keys"
date: 2026-08-17
status: in-review
tags: [packs, config, loopholes, design]
summary: "Core's schema names two loopholes by hand. This is how a pack declares its own settings instead — typed, name-scoped, advisory when unseen, and delivered through a file yolo owns rather than an env channel the workspace controls."
---

# Letting a pack declare its own config keys

**Status:** DESIGN, 2026-08-17. Nothing built. Constraints established by reading the code; anchors
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

---

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

## 6. Risks

| Risk | Mitigation |
| :--- | :--- |
| A pack declares everything `scope: "workspace"`, so an agent can set it | Installing the pack was already a decision, and the origin gate still applies — but core should **default** a setting reaching a host daemon to `user`, mirroring §4.3b, so silence is the safe choice |
| The typed set is too narrow for a real pack | Start with `string`, `bool`, `int`, `string_list`; widening a closed set later is additive, unlike narrowing |
| An unseen declaration silently accepts a typo forever | It is the price of C2, and it is bounded: the key is namespaced under a loophole, so the blast radius of a typo is one ignored setting rather than a wrong one. OQ-K1 |
| The settings file becomes a second config system | Keep it a flat resolved map, written by core, read once — no includes, no layering, no comments |

---

## Open Questions

### 💬 OQ-K1 — is an unseen pack declaration advisory, or authoritative?

If a pack cannot be resolved (offline, fetched, older build), core cannot see its declaration.
**Advisory** — accept the value unvalidated — is the only option that survives an offline launch, a
nested launch where only embedded packs resolve, and a build older than the manifest. It gives up typo
protection.

_Leaning:_ **Advisory, and say so once.** A refused launch on a correct config is a worse failure than
an ignored typo, and C2 says the partial view is normal rather than exceptional. Print a single line
naming the packs whose settings could not be checked.

**Answer:**
> _(empty — fill in when decided)_

### 💬 OQ-K2 — may a workspace supply values that reach a host daemon at all?

The §4.3b ruling says **no** for `env`, on the stated grounds of agent-editability. This design says
**yes** for typed, core-validated, core-written settings. Either settings are meaningfully different
from `env` — which is the argument in §2.3, and which requires core to inspect them enough to know —
or the ruling needs amending rather than routing around.

_Leaning:_ **Yes, and the difference is real, but it must be argued in §4.3b rather than assumed.** A
declared typed value that core validates and writes is not the same object as an arbitrary key/value
pair injected into a process environment. The per-key `scope` field is what makes it safe to state.

**Answer:**
> _(empty — fill in when decided)_

### 💬 OQ-K3 — does `host_processes` keep its live per-request re-read?

Today an agent can rewrite the workspace config mid-session and the host daemon's allowlist changes on
the next request — no relaunch, no gate. Freezing at spawn closes that and removes a documented
operator affordance (*"edits take effect without a restart"*).

_Leaning:_ **Freeze it.** The affordance is real but it is indistinguishable from the hole: the same
property that lets you edit an allowlist without restarting lets an agent widen its own. If live
reload is wanted back, it should re-read **core's** file, refreshed by an explicit act.

**Answer:**
> _(empty — fill in when decided)_

### 💬 OQ-K4 — is `journal` in scope, and what is its scope rule?

`journal` cannot be pack-declared while it is compiled-in Go (OQ-A6), and it has **no scope rule at
all** today: `validateJournal` does no scope pass, and `journal: "full"` passes journalctl arguments
through unchanged. So it is an agent-settable host-journal-passthrough dial right now.

_Leaning:_ **Give it a scope rule immediately, independently of this design.** Whether it becomes
pack-declared can wait for OQ-A6; that it is currently ungated should not.

**Answer:**
> _(empty — fill in when decided)_
