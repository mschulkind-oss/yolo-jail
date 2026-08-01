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

1. **The code fix** — autonomy becomes a confinement policy (env-manager Phase 9). Big,
   boot-critical, and **deferred** by decision; the guide is made safe in the meantime.
2. **The doc fixes** — corrections to the guide so it stops misleading a host agent
   *today*, given the code as it currently stands.

---

## Tier 1 — the code change (deferred, designed)

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
  hand-authored allow/deny lists — and *silently*: the drop-notice
  (`noteDroppedManagedEntries`) fires only for object-valued dynamic tables like
  `mcpServers`, never for a scalar/array key like `permissions.deny`.

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

**Status.** Design landed (design doc §4.2, plan Phase 9). **Not implemented** — deferred
by decision (scope this round was "design + plan entry only"). **Open decision (OQ-11):**
how a pack encodes the two postures — a dedicated `autonomy` contribution kind vs a
`confinement`/`whenAutonomy` discriminator on existing `config`/`launch` entries. Both to
be sketched against the real packs before choosing; leaning toward the dedicated kind.

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
