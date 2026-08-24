# Response to the host-agent review of the pack-migration guide

**What this is.** A single record of the changes we are making in response to the
adversarial review of [`../guides/migrating-to-packs-and-host-management.md`](../guides/migrating-to-packs-and-host-management.md)
(reviewed 2026-08-01 from the persona of a coding agent running on the *real host*, trying
to migrate an existing hand-tuned setup). The review returned **18 confirmed findings and
3 refuted**, each verified against shipped code. This doc says, per finding: what it is,
what we change, and where — separating the **doc fixes** (done this turn) from the **code
fix** (designed, deferred).

**The one finding that matters most.** Five of the eighteen, from four independent review
lenses, converge on the same defect: `yolo apply --host --assert` renders the shipped
agent packs' *jail-safety-bypass* config onto the user's real machine, and overwrites the
user's own permission keys. (`apply --host` defaults to **observe** — a dry-run that prints
what it would do and writes nothing; `--assert` is the flag that makes it actually
**write** the render into the real home. So `--assert` is precisely the step that commits
the damage.) Everything else is either a facet of that, or an accuracy/expectations fix.
So this response has two tiers:

1. **The code fix** — autonomy becomes a confinement policy (env-manager Phase 9).
   **SHIPPED 2026-08-01**, no longer deferred. Verified empirically 2026-08-03: a host
   `apply --assert` into a throwaway home writes `permissions.defaultMode: "default"`,
   `additionalDirectories: []`, and `skipDangerousModePermissionPrompt: false` — the guarded
   posture, not the jail-bypass values R1 describes below. The bypass keys now live in the
   `autonomy` kind's `autonomous` posture and render only at the contained notches.
2. **The doc fixes** — corrections to the guide so it stops misleading a host agent
   *today*, given the code as it currently stands.

---

## Tier 1 — the code change (SHIPPED 2026-08-01; this section is the original analysis)

### R1. `apply --host` leaks jail-bypass config onto the real host — and clobbers the user's own permission keys

*(Confirmed by 5 findings across the safety, does-it-exist, migration, and accuracy
lenses — CRITICAL.)*

**What happens.** `apply --host` renders *every* configured pack (no selection filter).
The shipped `claude` pack's `settings` surface has no `${workspace}`, so it is not refused;
its `managed` block is force-written into the real `~/.claude/settings.json` via
`applyRMWLayer(force=true)`:

- `permissions.defaultMode: "acceptEdits"`, `skipDangerousModePermissionPrompt: true`,
  `permissions.additionalDirectories: ["/"]` — plus the `--dangerously-skip-permissions`
  launch flag. On a machine with **no jail**, these auto-accept edits, suppress the
  dangerous-mode prompt, and treat the whole filesystem as allowed. They were only ever
  safe *because a jail contained the agent*.
- `permissions.allow: []` and `permissions.deny: []` **overwrite** the user's own
  hand-authored allow/deny lists — and *silently*.

  **Why silently, and why the difference the reviewer noticed?** The RMW render has two
  write paths, and the distinction is which **layer** a key lives in, not its value shape:
  - The `managed` block is written by `applyRMWLayer(force=true)`
    (`prism.go:528-541`): for each key it does a plain `obj.Set(k, v)` — a
    **replace-whole-value** that never reads the prior value and emits nothing. This is how
    every `permissions.*` key is written. (Note: `permissions` is itself an *object*, and
    `applyRMWLayer` recurses into it and is still silent — so the trigger is the layer, not
    "scalar vs object." The claim in an earlier draft that this was a "scalar/array key"
    distinction was imprecise.)
  - The only notice in the whole path, `noteDroppedManagedEntries` (`prism.go:494-511`),
    belongs to the *dynamic-table* layer (`mcpServers`), and reports a categorically
    different event: a **named sub-entry dropped** during a wholesale regenerate ("you had
    `mcpServers.foo`, config no longer lists it, so it's gone"). A whole-value replace has
    no member-level diff to report, so nothing analogous exists for `permissions.deny`.
  - On the **host** path specifically it is worse: `RenderHostPack` passes `computed=nil`
    (`hostrender.go:72`), so the dynamic-table path — and thus the only notice emitter —
    never runs at all. The overwrite is total and unannounced.

  **Should we always warn (the reviewer's suggestion)? Yes, at the host notch — folded into
  the Phase 9 work.** It is not a free toggle: `applyRMWLayer` computes no before/after, so
  a symmetric "always warn on overwrite" would (a) require adding a prior-vs-new diff that
  does not exist, and (b) be pure noise on every *jail* boot, where re-asserting managed
  keys is the whole point (and would also perturb the byte-equality boot gate's
  expectations). The useful, minimal version: when `apply --host` is about to overwrite a
  key whose existing value **differs** from the pack's, print it (a real diff line, not the
  path-only preview — this is the same gap as finding D2). Phase 9 makes most of this moot
  for the dangerous keys by rendering the *guarded* posture at `host` so they are not
  written; the differ-and-warn is the backstop for any managed key that legitimately still
  overwrites a user value off-jail.

The same bypass recipe exists per agent — `claude`, `codex`
(`approval_policy: "never"` + `--dangerously-bypass-approvals-and-sandbox`), `agy`
(`permissionMode: "allow"`), `opencode` (`permission: "allow"`). `pi` and `copilot` carry
**no** such posture; `pi` is in fact permissive *by default*.

**The change we will make (env-manager plan Phase 9, design §4.2).** Autonomy stops being
unconditional pack config and becomes a **confinement policy**:

- An `agent-autonomy` policy on `render.Profile` (composed beside the enforcement
  primitives): **on** at `jail`/`guest`, **off** at `host`, and a settable knob when
  composing a custom confinement. `describe` prints it.
- Packs declare autonomy as data, **bidirectionally** — both an *autonomous* posture
  (no-prompts) and a *guarded* posture (prompts). Guarded-by-default agents
  (`claude`/`codex`/`agy`/`opencode`) are *loosened* by confinement; permissive-by-default
  `pi` is *tightened* at `host`. One selector, run in whichever direction the pack's
  default sits — **not** "always inject a bypass."
- At render: `profile.agentAutonomy ? pack.autonomous : pack.guarded`. Benign always-safe
  keys (auto-updater off, trust dialog) stay unconditional. **Invariant:** the `jail`-on
  path renders byte-identical to today (`renderfingerprint_test.go`); only host/guest
  behavior changes.

**Status.** ~~**Not implemented** — deferred by decision (scope this round was "design + plan
entry only").~~ **SHIPPED 2026-08-01 as env-manager Phase 9** (`dbeae3e`..`8f5e3b1`) — verified
2026-08-23: `render.Profile.AgentAutonomy`, the `autonomy` contribution kind, and the five migrated
packs are all in the tree, and `apply --host` warns before overwriting a managed key. Design landed
in design doc §4.2 / plan Phase 9. **OQ-11 is now RESOLVED:** a
pack encodes its two postures with a **dedicated `autonomy` contribution kind** (each posture
a named block of config patches + launch flags), not a `when` discriminator on existing
entries. Both encodings were sketched against the real `claude`/`codex`/`agy`/`opencode`/`pi`
packs in plan §9.0; the dedicated kind wins because it keeps confinement-conditional keys
physically out of the unconditional `config` (a bypass key can't be left in the always-on
part by accident), prints as one legible block in `describe`/`footprint`, and reuses the
existing notch-gated `config-overlay` + `launch` machinery. Phase 9 also folds in the
host-notch overwrite warning the reviewer asked for (see R1's silent-overwrite note above).

**Until it ships, the guide carries a hard warning** (see D1) and a known-defect banner
sits at the top of the plan doc.

---

## Tier 2 — the doc fixes (applied this turn)

All of the following are corrections to the guide, made now, because they mislead a host
agent regardless of when Phase 9 lands.

### D1. State the security hazard, and stop calling `--assert` safe RMW

*(CRITICAL — the doc face of R1.)*

- Add a top-of-guide **security banner**: do not `apply --host --assert` with a shipped
  agent pack in your config yet; enumerate the exact jail-bypass keys; explain that
  `apply --host` renders *every* configured pack; point at Phase 9.
- Rewrite the RMW section (was "leaves every key you wrote yourself untouched"). The
  precise truth: a key the pack does **not** declare survives; a key inside the pack's
  `managed` block is **overwritten** — including `permissions.allow`/`deny`/`defaultMode` —
  with no merge and no notice. Replace the reassuring `theme` example (a deliberately
  non-colliding key) with the collision that actually bites.

### D2. Warn that the observe preview hides the payload

*(HIGH.)* `apply --host` observe prints only *paths* (`claude/settings would render …`),
not the keys/values. "Preview first" therefore gives false assurance. Add a callout in the
observe step: read the pack's `managed` block yourself (`yolo pack lint <pack>` or the
`pack.json`) before asserting; the path-only preview is not adequate review.

### D3. Stop claiming "only shipped commands" — these verbs are unreleased

*(HIGH ×3.)* `describe`, `apply` (incl. `--host`/`--sealed`), `check-deps`, the newer
`pack` subcommands (`lint` manifest-validation, `footprint`, `install`, `status`), and
`config drift`/`dump` are in-progress and **not in a released `yolo`**. On an older build
they error `unknown command` / `unknown subcommand` (exit 2).

- Add a top **version banner**: verify each verb with `--help` before use; if it errors,
  the installed `yolo` predates this work.
- Replace the intro's "everything here uses only shipped commands" with "every command is
  real but several are unreleased — verify first."
- Add the unreleased-surface fact to "What is not built yet."

### D4. Admit migration is manual re-authoring (no import verb)

*(MEDIUM + a PLAUSIBLE echo.)* There is no `pack import`/`adopt`/`extract`, and `pack init`
does **not** read or convert an existing `~/.claude/settings.json`. Add an explicit note
after Step 1: migration means opening your current config and transcribing by hand the
keys you want managed into the pack's `managed` block.

### D5. Say that `skills`/`briefing` do not port to the host

*(LOW.)* `apply --host` renders config surfaces only. The `skills` tree and `AGENTS.md`
authored in Part 1 are honored by the FieldSet census but **not written** by `apply --host`
— and they are not in the "refused" list either, so their absence is silent. Note this in
the observe step and in "What is not built yet"; copy them by hand if wanted on the host.

### D6. Reconcile the "inheritance retired" claim with the still-live `reads-host`

*(LOW.)* The guide said `~/.claude/settings.json` is "no longer special-cased." What is
gone is the composed *magic layer* (settings merged into what yolo writes); the shipped
`claude` pack **still** mounts that file read-only into the jail via a `reads-host` entry.
Reword to: the silent merge-into-what-yolo-writes is gone; the read-only mount for the
agent to *see* remains.

### D7. Regenerate the fabricated sample output

*(LOW.)* The observe example invented a `mount refused` line (no pack in the example
declares a `mount`) and omitted the refusals that actually occur. Replace with the real
shape for `packs: [claude, my-agent-pack]`: the `${workspace}` config refusal and the
`reads-host`/`state` refusals.

### D8. Replace the real config hash with an obvious placeholder

*(REFUTED as a defect, fixed anyway — zero cost.)* The example `describe` output used
`sha256:d6a00e0e…`, which is the truncation of a real config's hash. Swapped for
`sha256:0000…example` so no reader treats a specific value as meaningful. (The review
judged the original clearly illustrative; changing it removes all doubt.)

---

## Refuted findings — no change

Recorded so they are not re-raised:

- **"Step 2's example collides with the claude pack's reserved surface identity / should use
  `config-overlay`."** Premise true (the example reuses `claude`'s surface identity) but
  both claimed consequences misread the compose engine; no collision or silent override
  occurs as described. No change.
- **"The whole guide relies on verbs absent from any installed host `yolo`."** Overstated:
  the verbs exist in-repo (they are unmerged/unreleased, not nonexistent). The accurate
  version of this concern is handled by **D3**. No further change.
- **"The example `describe` hash is presented as canonical."** It sits in an illustrative
  console block; a reader reads it as sample output. Addressed cosmetically anyway by **D8**.

---

## Where the changes live

| Change | File | Status |
|---|---|---|
| R1 design (autonomy = confinement policy) | `../design/yolo-as-environment-manager.md` §4.2 | committed `5ec0af0` |
| R1 plan (Phase 9 + OQ-11 + defect banner) | `environment-manager-plan.md` | committed `5ec0af0` |
| D1–D8 (guide corrections) | `../guides/migrating-to-packs-and-host-management.md` | committed `787ba47` |
| R1 implementation (Phase 9) | `internal/render`, `internal/packdecl`, `packs/*` | **not started** (gated on OQ-11) |
