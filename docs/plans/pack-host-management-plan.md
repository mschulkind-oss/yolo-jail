# Implementation plan — pack-managed host briefings, skills, and files

**Status:** plan, 2026-08-01. Sequences
[`handoff-pack-host-management-gaps.md`](handoff-pack-host-management-gaps.md) (the gap
report — five gaps, each verified by running the binary) into buildable phases.

**Requester's goal:** *"fully manage my host briefings and skills and associated files"*
from packs, including *"packify my fzf customized file finder"* for Claude. Explicitly:
**fix yolo first, before migrating any user config.**

**Acceptance test for the whole plan:** a single pack delivers the fzf file finder — the
`fileSuggestion` settings key, the executable `~/.claude/file-suggestion.sh`, and its host
deps (`fd`, `fzf`) — to **both** a jail and the real host. When that works, this plan is
done.

**Reads with:** the handoff (the evidence);
[`environment-manager-plan.md`](environment-manager-plan.md) (Phases 4/9 built the host
notch this extends); [`../design/pack-system.md`](../design/pack-system.md) §14 (the
schema-vs-shipped gap list this plan shortens);
[`../design/host-render-target.md`](../design/host-render-target.md) (§2.1's census, which
G1 shows the renderer does not implement).

---

## What re-verification found, beyond the handoff

Every handoff claim reproduced against `0.7.1+326.g8f5e3b1`. Three corrections and one
new finding, all material to sequencing.

### N1 (NEW, and it reframes G3) — `files` is inert **everywhere**, not just at the host

The handoff reads G3 as "the host refuses a kind that works in a jail." It does not work in
a jail either. `packdecl.KindFiles` has exactly **two** non-test references in production
code:

```console
$ rg -n "KindFiles" --glob '*.go' . | grep -v vendor | grep -v _test
internal/render/fieldset.go:42      # the host refusal string
internal/packload/footprint.go:85   # footprint reporting only
```

`MountContributions()` (`internal/packdecl/contributes.go:253`) does lower `KindFiles` into
a legacy `Mount`, but **nothing calls that accessor outside tests** — `assemble.go` iterates
`Contributions()` and switches on `KindSkills` (via `packSkillTargets`) and `KindBriefing`
directly, never on `KindFiles`. So a `files` contribution today:

- passes `pack lint` (✓ 4 files stage),
- prints a footprint claim (`files .claude/fkdir read-only tree`),
- is **refused by name** at the host,
- and is **silently dropped in a jail** — no mount, no copy, no warning.

That last state is the exact failure mode the codebase elsewhere treats as unacceptable
(`fieldset.go`'s own doc comment: *"the silent skip is the failure mode G3 shipped"*).
`pack-system.md` §14 lists `config-overlay` as "the one contribution kind that is inert."
**That is now wrong: `files` is inert too, and unlike `config-overlay` it is inert while
`pack lint` and `pack footprint` both report it as working.** Fix the doc as part of this
plan.

This changes the shape of the work: G3 is not "port `files` to the host," it is **"implement
`files`" — jail first, then host.** It also means the fzf script cannot be delivered by a
pack to *any* target today, which is a stronger statement than the handoff makes.

### N2 — G2's cause is right, its evidence is subtly wrong

The handoff says adding `"allow_exec": true` to `pack.json` produces "the identical error."
It does not — it produces a *different* error, which matters because it means the fix is
smaller than proposed:

```console
$ yolo pack lint /tmp/g2/pack     # pack.json HAS "allow_exec": true
yolo pack lint: pack file files/file-suggestion.sh is executable (mode 755) but the pack
does not set "allow_exec": true — …

$ yolo pack lint /tmp/g2b/pack    # same manifest key, but NO exec-bit file
✗ pack pack: pack.json: json: unknown field "allow_exec"
```

`packdecl` decodes with `DisallowUnknownFields`, so `allow_exec` in a manifest **is already
a hard validation error** — handoff item G2.2 ("make it a validation error") is already
done. The real defect is **ordering**: `packLint` runs `packstage.Stage` *first*
(`internal/cli/pack.go:231`) and returns on error at line 236, so the staging refusal
masks the manifest error that would have explained it. The user sees only the misleading
message. So G2 = reword the message (real) + fix lint to report both classes of problem
(real) + `--allow-exec` flag (real). Not a new validation rule.

### N3 — G4 understates the blast radius: `packstage` strips the exec bit *by design*

The handoff scopes G4 to `host_files`' `0o444`/`0o644`. But even with the consumer's
`allow_exec: true`, `packstage.copyFile` forces `0o644` unconditionally, and there is a
**test pinning that**:

```go
// internal/packstage/packstage.go:235 — "Mode is forced to 0o644 … so it must not be
// carried through even when allow_exec permitted the copy."
// internal/packstage/packstage_test.go:76
if fi.Mode().Perm() != 0o644 { t.Errorf("mode = %o, want 644 even with allow_exec", …) }
```

`internal/cli/run/packs.go:copyTree` does the same for embedded packs. So `allow_exec`
today means *"may be present in the pack tree"*, **not** *"arrives executable."* It is a
staging admission gate, not a permission grant. Any fix must change that contract
deliberately and update the pinning test with its reasoning — the plan does this in Phase 3,
not as a drive-by.

### N4 — G5 is real; the count is worse than stated

`internal/cli/config_ref.txt:452` lists **12** kinds and `internal/cli/pack.go:68` lists
**11** (it omits `config-overlay` *and* `autonomy`). Both are hand-maintained prose beside
a closed set that is machine-enumerable (`packdecl.KnownKinds()`). Fixing the text without
fixing the drift mechanism means a 14th kind repeats this. Phase 0 adds a test.

---

## Revised gap table

| # | Gap | Real severity | Phase |
|---|---|---|---|
| **G5** + N4 | `config-ref` (12) and `pack --help` (11) both miss kinds; nothing pins them to `KnownKinds()` | docs + drift | 0 |
| **G2** + N2 | Misleading exec-bit message; lint masks the manifest error behind the staging error | bug | 1 |
| **G1a** | `apply --host` **silently** skips `skills` + `briefing` | **blocker**, and worst-of-three state | 2 |
| **G1b** | Host `skills` render (never-clobber, per-entry) | blocker for the goal | 4 |
| **G1c** | Host `briefing` render (delimited managed block, idempotent) | blocker for the goal | 5 |
| **G4** + N3 | No path delivers an executable: `host_files` caps at `0o444`/`0o644`; `packstage` forces `0o644` even with `allow_exec` | blocker for fzf | 3 |
| **G3** + N1 | `files` is inert at **every** target while lint/footprint report it as working | **blocker**, and a silent drop | 6 (jail), 7 (host) |
| — | Host `program` deps (`fd`/`fzf`) not run at host | known (env-manager Phase 4.3) | 8 (scoped) |

---

## Sequencing rationale

The handoff's order is sound and I keep its spine: **stop lying first, then unblock the
narrow case, then build the real features.** Two deviations:

1. **`files` moves later and grows a jail half.** N1 means it is a feature to *build*, not
   a target to *port*. It is also the only gap with no interim workaround, so it must not
   block the three that do.
2. **The exec-bit work (Phase 3) comes before both host renders.** It is small, it is the
   single change that makes the fzf script deliverable at all (via `host_files` as an
   interim), and Phase 7's host `files` render depends on the mode policy it settles.

Every phase is independently shippable and independently committable.

---

## Phase 0 — Pin the kind list to the code  *(trivial; do it first)*

**Fixes:** G5, N4.

- **0.1** Add `autonomy` to `internal/cli/config_ref.txt:452`'s kind list, with both
  posture sub-objects, matching the `AutonomyPosture` shape in
  `internal/packdecl/contributes.go:155`. Add `config-overlay` + `autonomy` to
  `internal/cli/pack.go:68`'s short table.
- **0.2** In `config_ref.txt`, mark the two inert kinds in the list itself, not only in the
  trailing NOTE: `config-overlay` already says "parses today, but NOT yet applied at boot";
  give `files` the same treatment (**per N1** — until Phase 6 it is inert). A kind
  documented as working while it silently drops is the doc half of the same bug.
- **0.3** **The drift fix.** Add a test in `internal/cli` asserting every
  `packdecl.KnownKinds()` name appears in both `config_ref.txt` and the `pack --help`
  table. This is why G5 recurred; a 14th kind now fails CI instead of shipping undocumented.

**Done when:** `yolo config-ref` and `yolo pack --help` each name all 13 kinds, and a new
kind without docs fails `just test-fast`.

---

## Phase 1 — Make the exec-bit refusal point at the right file  *(small)*

**Fixes:** G2, N2.

- **1.1** Reword `internal/packstage/packstage.go:141`'s error to name the **consumer**
  location and show the real shape (the handoff's wording is good; use it):

  ```
  pack file files/file-suggestion.sh is executable (mode 755). A pack cannot self-grant
  the exec bit — the CONSUMER opts in, in ~/.config/yolo-jail/config.jsonc:
      "packs": [{"source": "file:///…/pack", "allow_exec": true}]
  ```

  Preserve the invariant this protects (handoff's cross-cutting principle 5): consumer
  grants host power, never the pack author. Update the assertion in
  `packstage_test.go:62` — it greps for the literal `allow_exec`, which the new wording
  keeps.
- **1.2** **Fix the masking (N2).** `packLint` returns at `pack.go:236` on a staging error,
  so the manifest error explaining it never prints. Restructure to collect staging problems
  as *problems* and still run `packload.LoadDir`, so an author who put `allow_exec` in
  `pack.json` sees **both** lines: the exec-bit refusal *and* `unknown field "allow_exec"`.
  That pairing is self-explanatory in a way either line alone is not.
- **1.3** Add `--allow-exec` to `yolo pack lint`, threading `packstage.Spec.AllowExec`, so
  an author can lint as a consenting consumer would stage. Document it in the `pack --help`
  lint line.

**Done when:** the message names `~/.config/yolo-jail/config.jsonc`; a manifest carrying
`allow_exec` reports both problems in one run; `pack lint --allow-exec` stages an exec-bit
pack.

---

## Phase 2 — Stop the silent skip  *(small; the honesty fix)*

**Fixes:** G1a. **This is the handoff's "minimum honest fix" and it ships before the
feature it stands in for.**

The bug in one line: `HostFields()` (`internal/render/fieldset.go:63`) promises `skills`
and `briefing` apply, but `RenderHostPack` (`internal/entrypoint/hostrender.go:56`)
iterates `p.Surfaces()` — **config contributions only**. A kind that is *applicable* but
has no surface is neither rendered nor refused; it vanishes. `refusalReasons` has no entry
for them precisely because the census says they *do* apply.

- **2.1** In `applyHost` (`internal/cli/apply.go:153`), the loop over
  `p.Decl.Contributions()` already reports every kind the FieldSet does not honor. Extend
  it to report **honored-but-unimplemented** kinds — the same shape as the existing
  `program` special case at line 157, which is the precedent for "applicable, gated,
  reported":

  ```
  skills     refused — host skills render not implemented (pack-host-management-plan.md Phase 4)
  briefing   refused — host briefing render not implemented; a naive append would duplicate your prose
  ```

  Keep this a **data-driven set** (a `hostUnimplemented map[packdecl.Kind]string`), not two
  `if`s, so Phases 4 and 5 delete an entry each rather than untangling a conditional.
- **2.2** Add a test asserting **every** kind a loaded pack declares appears in
  `apply --host` output exactly once — as rendered, refused, or unimplemented. This is the
  general form of the bug: the census promising what the renderer does not implement.
  Without it, the next kind added to `HostFields()` vanishes the same way.

**Done when:** `apply --host` on a skills+briefing pack names both by name; no declared kind
can be absent from the output.

**Note on ordering:** Phase 2's messages are *deliberately temporary*. Shipping them is
still right — a silent skip is the worst of the three states, and Phases 4/5 may slip.

---

## Phase 3 — Let a managed file be executable  *(small; unblocks fzf)*

**Fixes:** G4, and settles the mode policy Phase 7 needs. **Depends on:** Phase 1 (the
consumer-opt-in message is the contract this extends).

**Per N3, this spans two mechanisms with the same defect.** Do both, or "the pack ships an
executable" stays false:

- **3.1 `host_files` (the handoff's G4).** `internal/entrypoint/hostfiles.go:106-124`:
  `readonly` locks `0o444`, `copy` writes `0o644`. Mask `0o111` **from the source** into the
  rendered mode — source-derived, no new knob, and it matches `host_files`' own meaning
  ("mirror this host file"). Note the `readonly` chmod dance is exec-aware: it restores
  `0o644` to re-truncate, then re-locks `0o444`; that must become `0o555` when the source
  is executable, or the re-truncate fails on the second boot.
- **3.2 `packstage` (N3 — the handoff misses this).** `packstage.go:238` forces `0o644`
  *even when `allow_exec` is set*, with `packstage_test.go:76` pinning it. Change the
  contract: with `AllowExec`, preserve the source's `0o111` bits (as `0o755`); without it,
  the file is refused anyway, so `0o644` remains the only other outcome. Do the same in
  `internal/cli/run/packs.go:copyTree` (the embedded-pack path), whose comment states the
  same now-changed rationale.
- **3.3** Rewrite the pinning test rather than deleting it: assert `0o644` **without**
  `allow_exec` (still the refusal path) and `0o755` **with** it, and rewrite the code
  comments — they currently argue for the old behavior, and a stale rationale beside changed
  code is how the next reader reintroduces the bug. State the new contract: `allow_exec`
  grants the exec bit **through** to the destination, which is what a consumer setting it
  means.
- **3.4** Document in `config_ref.txt`'s `allow_exec` entry and in `pack-system.md` §5 that
  the exec bit is source-derived and consumer-gated.

**Done when:** a `host_files` entry for `file-suggestion.sh` lands executable and survives a
second boot; a pack with `allow_exec: true` stages it `0o755`; without `allow_exec` it is
still refused.

**This is the interim answer for the fzf script** — `host_files` lives in user config, not a
pack, so it does not satisfy "manage it from a pack" (Phase 7 does), but it makes the script
*work* while the rest lands.

---

## Phase 4 — Host `skills` render  *(medium; the main event, half 1)*

**Fixes:** G1b. **Depends on:** Phase 2 (deletes its `skills` entry).

`PrepareSkills` (`internal/agents/skills.go:67`) already composes
`built-in < pack < user's own` into a staging dir the jail bind-mounts `:ro`. The host needs
the same *composition* with an inverted *ownership model*, and the two must not share a code
path naively — `PrepareSkills` calls `clearDirContents(skillsDir)` at line 79, which on a
real `~/.claude/skills` **deletes every hand-written skill.**

Two decisions, both settled here (handoff asked for them explicitly):

- **Ownership → never clear, per-entry, user wins.** Write each pack skill as an
  individual entry; never clear the destination; **skip any name the user already has**.
  That mirrors the jail's existing `pack < user` precedence rather than inventing a second
  rule. Report per-skill `rendered` / `skipped (yours)`.
- **Removal → no.** When a pack drops a skill, the host copy stays. Consistent with "no
  `--revert`" (env-manager OQ-1, resolved), and the alternative is yolo deleting files in a
  real home based on a pack having changed. **But print it**, so it is legible rather than
  silent.

- **4.1** Add a host skills renderer. Compose built-in + pack skills, then for each entry
  check the destination: absent → write; present-and-yolo-wrote-it → rewrite;
  present-and-user's → skip with a report line. Distinguishing the middle case needs a
  provenance marker (see OQ-A) — until then, **present → skip**, which is the safe
  direction.
- **4.2** Wire it into `applyHost`, honoring `observe`/`assert`: observe must print exactly
  what assert would write, matching `RenderHostPack`'s existing contract (`Overwrites`
  computed in both postures, `apply.go:170`).
- **4.3** Reuse `HostRenderResult` so one output loop prints config, skills, and (Phase 5)
  briefing. Extending it (a `Kind` field) beats a parallel result type with its own
  formatting.
- **4.4** Tests: a user-authored skill of the same name survives `--assert` **twice**; a
  built-in lands; `observe` writes nothing; a dropped pack skill is reported, not deleted.

**Done when:** `apply --host --assert` materializes pack skills into `~/.claude/skills`, a
same-named user skill is preserved, and re-running is a no-op.

---

## Phase 5 — Host `briefing` render  *(medium; the main event, half 2)*

**Fixes:** G1c. **Depends on:** Phase 2 (deletes its `briefing` entry).

**The trap, stated plainly.** In a jail, `ComposePackBriefings`
(`internal/agents/agentsmd.go:362`) concatenates pack prose after the user's file and writes
the result to a *different* path (a staging file, bind-mounted `:ro`). On the host, source
and destination are **the same file** — `after: "host:.claude/CLAUDE.md"` writing to
`.claude/CLAUDE.md`. A naive concat therefore **duplicates the user's prose on every
apply**, and it grows without bound. **Do not ship a plain append.**

- **5.1** Implement a **delimited managed block** — the Markdown analogue of `config`'s
  key-level RMW (handoff's cross-cutting principle 2):

  ```markdown
  <!-- yolo:pack-briefing begin (matt-core) -->
  …pack prose…
  <!-- yolo:pack-briefing end -->
  ```

  Re-asserted idempotently; **everything outside the markers is untouched**. Per-pack
  markers (the name in the delimiter) so two packs are two blocks and dropping one pack
  removes only its own. Note this composes with the existing
  `<!-- from pack: NAME -->` provenance header rather than replacing it — that header is
  *inside* the block.
- **5.2** Placement: append the block at end-of-file on first write, then **rewrite in
  place** thereafter. Never relocate a block the user may have moved; find it by marker, not
  by offset.
- **5.3** Missing-file case: create `~/.claude/CLAUDE.md` containing just the block. An
  absent user briefing is normal, not an error.
- **5.4** Malformed-state case: an unterminated `begin` marker (a user edit, or an
  interrupted write) must **refuse with a message**, not guess a boundary and eat prose.
  Fail-closed, matching `host_files`' A12 posture (`hostfiles.go:50`).
- **5.5** Tests: `--assert` twice is byte-identical (**this is the test that catches the
  duplication bug**); hand-written prose outside the markers survives; two packs get two
  blocks; a dropped pack's block is removed while the other survives; an unterminated marker
  refuses.

**Done when:** `apply --host --assert` maintains a delimited block idempotently, and the
user's own prose is never duplicated or lost.

---

## Phase 6 — Implement `files` in the **jail**  *(medium; per N1, this is new work)*

**Fixes:** the jail half of G3/N1 — the silent drop.

`files` is inert (N1). It must work somewhere before "port it to the host" means anything,
and the jail is where the existing delivery machinery lives.

- **6.1** Wire `KindFiles` into the mount assembler, beside the `KindSkills` and
  `KindBriefing` cases already in `assemble.go:351`/`:423`. A pack's `files` tree is
  bind-mounted `:ro` at `into` — that is what the footprint (`read-only tree`) and the
  refusal string ("binds a pack tree into a jail") already claim happens.
- **6.2** Mind the **known sharp edge** (`pack-system.md` §14): the assembler emits one bind
  per contribution with **no dedup by destination**, so two packs sharing an `into` fail the
  jail at boot with podman's "duplicate mount destination". `files` is `CombineExclusive`,
  so a second claimant is *already* a footprint collision — surface it as a **pre-flight
  error naming both packs**, not a podman error at boot. (This is also the fix shape for the
  same-`into` skills edge; keep the check generic enough to reuse, but do not scope-creep
  into fixing skills here.)
- **6.3** `files` mounting a **single file** vs a **dir**: Apple Container cannot bind a
  single file, which is why briefings go through `acMaterialize` (`assemble.go:427`). Route
  the same way, or a `files` contribution naming one file silently vanishes on that backend
  — the same class of bug as this whole plan.
- **6.4** Remove `files` from `pack-system.md` §14's inert list and from Phase 0.2's doc
  marking; add it to the honored set. Update the §14 claim that `config-overlay` is "the one
  contribution kind that is inert" — it is accurate again only after this phase.
- **6.5** Tests: a `files` pack's tree appears at `into` in a jail; two packs claiming one
  `into` fail pre-flight with both names.

**Done when:** a `files` contribution actually delivers its tree into a jail, and a
collision is reported before podman sees it.

---

## Phase 7 — Host `files` render  *(medium; the goal's "associated files")*

**Fixes:** the host half of G3. **Depends on:** Phase 3 (mode policy), Phase 4
(never-clobber discipline), Phase 6 (`files` means something).

The refusal — *"files binds a pack tree into a jail — nothing to bind into off-container"* —
is true of a **bind mount** and false of the **intent**. The host equivalent is writing the
tree, which is exactly what `~/.claude/file-suggestion.sh`, pi's `models.json`, and pi's 6
themes need. Take the handoff's option (1).

- **7.1** Render `files` at the host as a real copy: `0o444`-equivalent for content, `0o555`
  when the source has the exec bit **and** the consumer set `allow_exec` (Phase 3's policy,
  and the invariant that the consumer grants the power).
- **7.2** Reuse Phase 4's never-delete discipline. `files` is `CombineExclusive` — the pack
  "owns" the path — but **ownership of a jail path is not ownership of a real home path**.
  A pre-existing file the user wrote must not be silently replaced; report it as an
  overwrite the way `managedOverwrites` (`hostrender.go:103`) already does for config keys,
  and follow the same always-warn rule Phase 9 established for the host notch.
- **7.3** Remove `KindFiles` from `refusalReasons` (`fieldset.go:42`) and add it to
  `HostFields()`. Delete Phase 2's entry if `files` was listed there.
- **7.4** Tests: the tree lands with correct modes; an exec-bit file without `allow_exec`
  does not become executable; a user-authored file at the same path is reported before being
  touched; `observe` writes nothing; `--assert` twice is idempotent.

**Done when:** a pack owns `~/.claude/file-suggestion.sh` on the host, executable, from a
`files` contribution.

---

## Phase 8 — Host deps for the fzf case  *(scoped; closes the acceptance test)*

**Fixes:** the third leg of the fzf case. **Depends on:** nothing in this plan.

Already designed and partly built: `install_hints` +
`DepRequirements()` (`contributes.go:139`) exist, and `yolo check-deps` consumes them
(env-manager Phase 6, shipped). What is missing is **running** an install at the host notch
(env-manager Phase 4.3 — the confirm UX).

**Scope discipline:** do **not** build the batched-by-elevation-class confirm UX here; that
is env-manager Phase 4.3's own increment with its own resolved OQs (OQ-6/7/9). This plan
needs only that `apply --host` **reports** the missing deps with their remedy, instead of
today's line:

```
program — install below jail is confirm-gated; not run by apply --host yet (Phase 4.3)
```

- **8.1** Make that line resolve the actual dep state: probe each `DepRequirement`'s `Bin`
  and print present/missing plus the `install_hints` remedy for the detected host manager.
  Reuse `check-deps`' probe rather than writing a second one.
- **8.2** Leave the *running* of installs to env-manager Phase 4.3, and say so in the
  output.

**Done when:** `apply --host` on a pack declaring `fd`/`fzf` names which are missing and the
exact install line, without running anything.

---

## Acceptance: the fzf pack, end to end

After Phases 0–8, this pack must work at both notches:

```jsonc
// ~/.dotfiles/claude-fzf/pack.json
{ "name": "claude-fzf",
  "contributes": [
    { "kind": "files", "from": "files", "into": ".claude" },            // Phase 6 (jail) + 7 (host)
    { "kind": "config", "config": [ { "agent": "claude", "name": "settings",
        "managed": { "fileSuggestion": { "type": "command",
                     "command": "~/.claude/file-suggestion.sh" } } } ] },  // works today
    { "kind": "program", "bin": "fzf", "via": "installer", "url": "…",
      "install_hints": { "brew": "fzf", "apt": "fzf", "nix": "fzf" } }   // Phase 8 reports
  ] }
```

```jsonc
// ~/.config/yolo-jail/config.jsonc — the CONSUMER grants the exec bit (Phase 1/3)
{ "packs": [ { "source": "file:///home/matt/.dotfiles/claude-fzf", "allow_exec": true } ] }
```

Checks:

1. `yolo pack lint --allow-exec ~/.dotfiles/claude-fzf` → clean (Phase 1).
2. `yolo apply --host` → every kind named; no kind silently absent (Phase 2).
3. `yolo apply --host --assert` → settings key written, `file-suggestion.sh` at `0o555`,
   skills + briefing block written, deps reported (Phases 3/4/5/7/8).
4. Re-run `--assert` → **byte-identical**; no duplicated prose (Phase 5).
5. `yolo -- claude` → the script is present and executable **in the jail** (Phases 3/6),
   fixing the live breakage the handoff found: the script is currently staged into no jail
   at all, so in-jail Claude's `fileSuggestion` points at a nonexistent file.
6. A hand-written `~/.claude/skills/foo` and hand-written prose outside the markers both
   survive every step (Phases 4/5).

---

## Cross-cutting principles (carried from the handoff, all endorsed)

- **A real `$HOME` is not a jail home.** Every jail path is disposable and `:ro`;
  the host equivalents are the user's own files. `clearDirContents`, wholesale tree
  replacement, and unconditional overwrite are safe in a jail and destructive on a host.
  `PrepareSkills:79` is the concrete trap.
- **RMW, in every format.** `config` does key-level RMW; host `briefing` needs the Markdown
  analogue (delimited block), host `skills`/`files` the filesystem analogue (per-entry,
  user wins).
- **Never silent.** The `files` host refusal is *good behavior*; the `skills`/`briefing`
  skip is not — and per N1, `files` in a **jail** is the same sin. Anything not written says
  so, by name, in both `observe` and `assert`.
- **Idempotence is the test.** `--assert` twice must be a no-op. That is what catches the
  briefing duplication before a user does.
- **The consumer grants host power, not the pack author.** G2's design is right; only its
  message is wrong. Preserved in Phases 3 and 7 (the exec bit needs consumer opt-in).
- **New here: the census must not outrun the renderer.** G1's root cause is a `FieldSet`
  claiming a kind applies while no code implements it. Phase 2.2's test is the structural
  fix; keep it passing rather than adding kinds to `HostFields()` optimistically.

---

## Open questions

- **OQ-A — provenance for host-written pack content.** Phase 4 skips any existing skill
  because it cannot tell "a skill yolo wrote last apply" from "a skill the user wrote."
  Config surfaces have the `managed` layer for this; the filesystem has nothing. A manifest
  under `~/.local/state/yolo-jail/` recording host-written paths would let `apply` update its
  own output while still never touching the user's — and would make Phase 4's "removal → no"
  revisitable. **Recommend deferring**: skip-if-present is safe and unblocks the goal;
  revisit if the "my pack updated but my host skill didn't" complaint materializes.
- **OQ-B — should `files` at the host be `0o444`?** Phase 7.1 mirrors the jail's `:ro`
  posture, but a `:ro` mount is *enforced* while `0o444` is merely *asymmetric*
  (`project_prism_ro_rw_audit` made this distinction). A user who wants to hand-edit a
  pack-delivered file will `chmod` it and lose that edit on the next apply.
  `0o644` + the overwrite warning may be the friendlier default. **Recommend `0o444` for
  consistency**, but flag it for the maintainer — this one is taste, not correctness.
- **OQ-C — does the same-`into` collision check belong to `files` only?** Phase 6.2 fixes it
  for `files` (where it is a genuine `CombineExclusive` violation) but the identical podman
  failure exists for two `skills` contributions sharing an `into`, where the footprint model
  says it *should* be a safe merge. That is a mount-dedup fix, not a collision fix, and it is
  a different change. Kept out of scope; tracked in `project_pack_tooling_gaps`.

---

## Verification notes

`cmd/`/`internal/` changes need nested-jail verification (`AGENTS.md`): after
`just build-go`, run the freshly-built binary **by path**
(`./dist-go/linux-$(go env GOARCH)/yolo -- bash`), not bare `yolo`. Phases 6 and 3.2 change
mount assembly and file modes — the two classes that only fail when a container actually
starts.

Phases 3, 4, 5, 7 change render output, so the **render fingerprint gate**
(`internal/entrypoint/renderfingerprint_test.go`) is the tripwire: it hashes every file the
embedded packs write. Host-only renders should leave it untouched; if it moves, a host change
leaked into the jail boot path. Phase 3.2 (`packstage` modes) may legitimately move it —
verify the diff names only the intended files.

Every `--assert` test must run against a throwaway `$HOME` under `/tmp`, never the real one.
