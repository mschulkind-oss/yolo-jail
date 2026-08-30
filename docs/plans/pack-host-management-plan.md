# Implementation plan — pack-managed host briefings, skills, and files

**Status:** **ALL PHASES SHIPPED** 2026-08-02. Sequences
[`handoff-pack-host-management-gaps.md`](handoff-pack-host-management-gaps.md) (the gap
report — five gaps, each verified by running the binary) into buildable phases.

> **Build status (2026-08-02).** Every phase below is implemented, tested, and committed.
> **The acceptance test passes at both notches**: one pack delivers the fzf
> file finder — the `fileSuggestion` settings key, the executable script, namespaced skills,
> briefing prose — into a real home (idempotent across three asserts, with the user's own
> skill and hand-written prose untouched) and into a jail, where the script is present,
> executable, and runs.
>
> **What running it found that no unit test could.** Seven defects beyond the five gaps in the
> handoff, each caught only by executing the thing end to end:
>
> 1. **Five more silently-skipped kinds.** G1 named `skills` and `briefing`; the
>    no-silent-skip test written for the general invariant found `config-overlay`, `launch`,
>    `env`, `hook`, and `autonomy` too (Phase 2).
> 2. **`files` was inert at EVERY target**, not just the host — it silently did nothing in a
>    jail while `pack lint` and `pack footprint` both reported it working (N1, Phase 6).
> 3. **A `files` tree mounted `:ro` over a directory an agent's config surface must write**
>    killed the boot with "read-only file system", naming the surface rather than the claim
>    that shadowed it — and cross-pack, so neither author could see it (Phase 7).
> 4. **Two packs at one `briefing` path** failed with podman's duplicate-mount-destination,
>    though `briefing` is `CombineConcat` and the prose was already merged (Phase 7).
> 5. **Same bug for `skills`** — `pack-system.md` §14's "known sharp edge", whose documented
>    workaround was unfollowable in the configuration it most matters for. Fixed rather than
>    documented, closing OQ-C (Phase 7).
>
> 6. **A version-skew brick, introduced and then fixed here.** Adding the `tier` field made
>    every jail refuse to start against an older baked image, because the in-jail entrypoint
>    read manifests with `DisallowUnknownFields`. The host CLI and the baked entrypoint
>    legitimately differ in age, so the jail now reads tolerantly while host-side authoring
>    reads stay strict (`packdecl.DecodeTolerant`).
> 7. **Two cosmetic refusals in the plugin adapter** (Phase 10): a plugin whose root is itself
>    a skill had its whole tree — manifest, hooks, agents — copied into a destination that had
>    refused those components by name one line earlier; and the plugin's own manifest was then
>    overwritten by yolo's synthetic one, leaving its `hooks/` and `.mcp.json` on disk while
>    the manifest pointing at them was gone. Both halves were individually correct; only the
>    delivered file showed it.
>
> Empty pack dirs, a warning that would have fired on every stock apply, and a test whose
> substring guard matched its own marker tag were also found this way. The lesson the plan
> already stated — *idempotence is the test* — held; what it understated is that **only a
> real container start catches a mount conflict, and only reading the delivered artifact
> catches a render that undoes itself.**

**Requester's goal:** *"fully manage my host briefings and skills and associated files"*
from packs, including *"packify my fzf customized file finder"* for Claude. Explicitly:
**fix yolo first, before migrating any user config.**

**Acceptance test for the whole plan:** a single pack delivers the fzf file finder — the
`fileSuggestion` settings key, the executable `~/.claude/file-suggestion.sh`, and its host
deps (`fd`, `fzf`) — to **both** a jail and the real host, **while the user can still add
their own skills to the same agent by hand.** When that works, this plan is done.

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

### N5 (NEW) — how the agents themselves namespace skills, and the mechanism to copy

The open question behind Phase 4 was *"can a user still add a skill directly to an agent
whose skills a pack manages?"* — i.e. is there any namespace, or must yolo either detect
collisions or forbid them. Researched against
[code.claude.com/docs/en/skills.md](https://code.claude.com/docs/en/skills.md),
[plugins-reference.md](https://code.claude.com/docs/en/plugins-reference.md), and GitHub's
Copilot CLI docs, plus the on-disk trees in this jail. **Claude has the mechanism; Copilot
does not.**

**Claude — plain skills are flat with SILENT override.** Precedence is
enterprise > personal (`~/.claude/skills/`) > project > bundled; same name at two levels
means the higher wins with **no warning** (skills.md, "Where skills live"). The invocation
name comes from the **directory name** for personal/project skills — frontmatter `name:` is
display-label only. So a flat merge into a real `~/.claude/skills/` is exactly as dangerous
as the handoff assumed.

**Claude — plugin skills cannot collide**, because they are namespaced `plugin:skill`
(skills.md: *"Plugin skills use a `plugin-name:skill-name` namespace, so they cannot conflict
with other levels."*). That this is load-bearing is visible in Anthropic's own marketplace:
of 469 plugins, **three ship `name: configure` and three ship `name: access`**
(discord/imessage/telegram, near-identical frontmatter), coexisting. Collisions one level up
— between *plugins* — are resolved by registry fiat: the marketplace manifest carries a
`renames` table (`adlc`→`agentforce-adlc`, `vals`→`valtown`). Neither half is available to
yolo, which has no registry and whose packs are the user's own.

**THE MECHANISM — "skills-directory plugins"** (documented and supported, not an
implementation detail; plugins-reference.md §"Skills-directory plugins"): *"Any folder under
a skills directory that contains a `.claude-plugin/plugin.json` manifest is loaded as a
plugin named `<name>@skills-dir` on the next session, with no marketplace and no install
step … discovered in place rather than copied into the plugin cache."* Layout verified by
running the real scaffolder (`claude plugin init matt-core --with skills`) against a
throwaway `$HOME`:

```
~/.claude/skills/matt-core/.claude-plugin/plugin.json   # {"name":"matt-core","skills":["./"]}
~/.claude/skills/matt-core/SKILL.md                     # → /matt-core
~/.claude/skills/matt-core/skills/example/SKILL.md      # → /matt-core:example
```

So **a pack can own one namespaced subtree inside the user's own `~/.claude/skills/` without
touching a single sibling entry.** Personal scope has no trust gate (project scope needs the
workspace trust dialog). Disable is `claude plugin disable <name>@skills-dir`; removal is
deleting the folder. This is what Phase 4 adopts, and it is why OQ-A is resolved rather than
deferred.

Two adjacent knobs worth knowing: personal/project skills dirs **follow symlinks** (and a
target reachable from two locations loads once), and `skillOverrides` in `settings.json`
accepts `"off" | "name-only" | "disabled"` per skill name — a user-side escape hatch if a
pack skill is unwanted.

**Copilot — no namespacing at all, and the precedence runs the other way.** Skills load from
`.github/skills`, `.agents/skills`, `.claude/skills` (project), `~/.copilot/skills`,
`~/.agents/skills` (personal), then plugin `skills/` dirs, then `COPILOT_SKILLS_DIRS`. The
CLI plugin reference states this is **first-found-wins**, deduplicated by the frontmatter
`name` field, and that a shadowed plugin skill is *"silently ignored"* — a warning is
documented only for duplicate MCP servers, not skills. There is **no** documented
`plugin:skill` syntax. Note this inverts Claude's model: for Copilot, **project beats
personal**, and a plugin can never override either.

Consequences for the plan: the `@skills-dir` trick is **Claude-specific** and must be
declared per pack, not assumed. Also note yolo's `copilot` pack declares
`into: ".copilot/skills"`, which is a real Copilot personal-scope path — that is correct, but
it is a **flat, first-found-wins** namespace with no namespacing available, so for Copilot
the honest options are the ones the handoff offered (skip-on-collision, or disallow).
`~/.copilot/skills` does not exist by default; the `builtin-skills/` dir inside the npm
package is shipped content, not a user location.

### N6 (NEW) — Copilot reads Claude's plugin manifests, and namespaces plugin skills

The docs say Copilot has no skill namespacing (N5). **The shipped code says otherwise**, and
this is a documentation gap, not a behavior gap. Verified by grepping the real bundle,
`~/.npm-global/lib/node_modules/@github/copilot/app.js` (v1.0.48), then corroborated against
GitHub's own issue tracker:

> **github/copilot-cli#1766**, *"Support plugin/skill namespacing parity with Claude CLI"* —
> **CLOSED** 2026-04-08. GitHub's closing comment: *"Since v0.0.389, plugin skills are
> automatically namespaced using the `/plugin-name:skill-name` format … matching the
> colon-based namespacing convention used by Claude CLI."*

**Methodology warning, because it cost real time here.** A docs-only research pass reported
(a) that Copilot does not read `.claude/skills`, (b) that namespacing does not exist, and
(c) that #1766 was still open. **All three were wrong.** `docs.github.com`'s add-skills page
is simply incomplete; Copilot's own in-app help text, in the bundle, says:

> Skills are loaded from:
> • Project: `.github/skills/`, `.agents/skills/`, or `.claude/skills/`
> • Personal: `~/.copilot/skills/`, `~/.agents/skills/`, or `~/.claude/skills/`

For questions like this, **grep the installed bundle and check the issue tracker; do not
trust the vendor docs alone.**

The bundle evidence:

```js
// plugin manifest discovery — note the last entry in each list
P9  = "plugin.json"
N9  = [".plugin", ".", ".github/plugin", ".claude-plugin"]
yWr = ["marketplace.json", ".plugin/marketplace.json",
       ".github/plugin/marketplace.json", ".claude-plugin/marketplace.json"]

// skill invocation name
function IJ(t){ return t.pluginName ? `${t.pluginName}:${t.name}` : t.name }
```

Three consequences, in descending order of usefulness:

1. **`.claude-plugin/plugin.json` and `.claude-plugin/marketplace.json` are first-class
   Copilot inputs.** Copilot also carries a `"claude-command"` skill source for
   `.claude/commands/`. So a Claude-plugin-shaped tree is portable to Copilot *for free* —
   one layout serves both agents, which is what makes the plugin-as-pack idea (Phase 10)
   cheap rather than per-agent work.
2. **Copilot namespaces plugin skills `pluginName:name`** — the same shape as Claude. So the
   tier-A set is `{claude, copilot}`, not `{claude}`.
3. **But its dedup key is the BARE name.** The loader does `s.has(m.value.name) || (…)`,
   where `name` is the frontmatter `name` falling back to the directory name. So a plugin
   skill whose *bare* name is already taken is **silently dropped**, even though its
   qualified name would have been unambiguous. **Namespaced for invocation, flat for
   collision.** That asymmetry is real and Claude does not have it — so tier A still needs
   the collision *report* (Phase 9.3), just not the archive/manifest machinery.

Installed Copilot plugins land in a state-dir cache (`installed-plugins/`, `plugin-data/`),
copied rather than read in place — like Claude's marketplace installs, unlike Claude's
`@skills-dir`.

**Caveat on durability:** the plugin-manifest search paths are undocumented, so they are
bundle-verified implementation details that could change without notice (the namespacing
itself is safer — GitHub closed #1766 on it deliberately). Phase 9's tier table is therefore
**declared data with a probe**, not a hardcoded assumption — see 9.1.

**On the other agents.** The Agent Skills standard (agentskills.io) standardizes the
`SKILL.md` format and directory layout **only** — it mandates no discovery paths and no
namespacing, and its own implementation guidance recommends project-over-user precedence plus
*"log a warning when a collision occurs so the user knows a skill was shadowed"* (which is
Phase 9.3's rule, arrived at independently). `.agents/skills/` is the emerging cross-tool
interop path, and opencode reads `.claude/skills` as well. None of that changes the tier
model; it means **tier membership must be probed per agent** rather than assumed from the
vendor's docs, and that a future agent may join tier A without yolo changing.

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
| **G1b** + N5 | Host `skills` render — one `@skills-dir` subtree per pack, so a user skill cannot collide | blocker for the goal | 4 |
| **G1c** | Host `briefing` render (delimited managed block, idempotent) | blocker for the goal | 5 |
| **G4** + N3 | No path delivers an executable: `host_files` caps at `0o444`/`0o644`; `packstage` forces `0o644` even with `allow_exec` | blocker for fzf | 3 |
| **G3** + N1 | `files` is inert at **every** target while lint/footprint report it as working | **blocker**, and a silent drop | 6 (jail), 7 (host) |
| — | Host `program` deps (`fd`/`fzf`) not run at host | known (env-manager Phase 4.3) | 8 (scoped) |
| **N5/N6** | Delivery quality is bounded per agent; tier-B agents need provenance + safe removal | design gap | 9 |
| — | Pull in a Claude plugin as a pack (works for claude **and** copilot per N6) | feature ask | 10 |

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

## Phase 0 — Pin the kind list to the code  *(trivial; do it first)*  *(SHIPPED)*

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

## Phase 1 — Make the exec-bit refusal point at the right file  *(small)*  *(SHIPPED)*

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

## Phase 2 — Stop the silent skip  *(small; the honesty fix)*  *(SHIPPED)*

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
  `yolo host apply` output exactly once — as rendered, refused, or unimplemented. This is the
  general form of the bug: the census promising what the renderer does not implement.
  Without it, the next kind added to `HostFields()` vanishes the same way.

**Done when:** `yolo host apply` on a skills+briefing pack names both by name; no declared kind
can be absent from the output.

**Note on ordering:** Phase 2's messages are *deliberately temporary*. Shipping them is
still right — a silent skip is the worst of the three states, and Phases 4/5 may slip.

---

## Phase 3 — Let a managed file be executable  *(small; unblocks fzf)*  *(SHIPPED)*

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

## Phase 4 — Host `skills` render  *(medium; the main event, half 1)*  *(SHIPPED)*

**Fixes:** G1b. **Depends on:** Phase 2 (deletes its `skills` entry).
**Design settled by N5 (below): one pack = one `@skills-dir` subtree, not a flat merge.**

`PrepareSkills` (`internal/agents/skills.go:67`) already composes
`built-in < pack < user's own` into a staging dir the jail bind-mounts `:ro`. The host needs
the same *composition* with an inverted *ownership model*, and the two must not share a code
path naively — `PrepareSkills` calls `clearDirContents(skillsDir)` at line 79, which on a
real `~/.claude/skills` **deletes every hand-written skill.**

**The question this phase used to hinge on** — *"can a user still add a skill directly to an
agent whose skills yolo manages?"* — is answered **yes, structurally**, by adopting the
skills-directory-plugin layout (N5). yolo writes ONE folder per pack; every sibling entry in
`~/.claude/skills/` stays the user's. There is no name to collide on, so no provenance
marker is needed and OQ-A dissolves.

- **4.1** Render each pack's skills as a **skills-directory plugin** at
  `~/.claude/skills/<pack>/`:

  ```
  ~/.claude/skills/<pack>/.claude-plugin/plugin.json   # {"name":"<pack>","skills":["./"]}
  ~/.claude/skills/<pack>/skills/<skill>/SKILL.md      # → /<pack>:<skill>
  ```

  Verified layout (see N5). yolo owns that ONE directory outright and never writes a sibling.
- **4.2** **Ownership → the pack dir is yolo's, everything beside it is the user's.** Inside
  `~/.claude/skills/<pack>/` a full rewrite is legitimate (it is yolo's own subtree, the same
  posture `config` surfaces have for their managed keys). **Never** touch a sibling entry.
  Guard the one collision that remains: a user directory that already exists at
  `~/.claude/skills/<pack>/` and is NOT a yolo-written plugin dir — refuse it by name rather
  than absorbing it (fail-closed, matching `hostfiles.go:50`).
- **4.3** **Removal → yes, within the pack dir only.** Because the subtree is unambiguously
  yolo's, a dropped pack skill CAN be removed — which is better than the
  "no-removal, print it" compromise a flat merge forced, and it matches how an unset MCP
  server is dropped in a jail today. Removing the whole pack dir when a pack is dropped from
  config is the same argument; print both.
- **4.4** Built-in skills: keep staging them into the **jail** as today. Do **not** write
  yolo's built-ins into a real `$HOME` — `jail-startup`/`diagnosing-the-jail` are about
  being in a jail and are noise on the host. Report them as `skipped (jail-only)` so the
  omission is legible rather than silent.
- **4.5** Wire into `applyHost`, honoring `observe`/`assert`: observe must print exactly
  what assert would write, matching `RenderHostPack`'s existing contract (`Overwrites`
  computed in both postures, `apply.go:170`). Reuse `HostRenderResult` (add a `Kind` field)
  so one output loop prints config, skills, and (Phase 5) briefing.
- **4.6** Tests: a sibling user skill of any name survives `--assert` **twice**; a
  pre-existing non-yolo dir at the pack's name is refused, not overwritten; a dropped pack
  skill is removed from within the pack dir; `observe` writes nothing; built-ins are not
  written to the host.

**Done when:** `yolo host apply --assert` materializes pack skills into
`~/.claude/skills/<pack>/`, every sibling entry is untouched, and re-running is a no-op.

**Deliberately NOT changing the jail path.** The jail keeps its flat
`built-in < pack < user` merge, so a skill is `/foo` in a jail and `/<pack>:foo` on the host.
That divergence is real and I am accepting it rather than hiding it: the jail's flat merge is
safe precisely because the destination is a disposable `:ro` mount, and unifying the two
would mean changing every shipped pack's skills destination plus the built-in staging path —
a bigger change than this plan should carry. Revisit only if the two-names problem actually
bites. **Print the host-side name** in `yolo host apply` output so it is discoverable.

---

## Phase 5 — Host `briefing` render  *(medium; the main event, half 2)*  *(SHIPPED)*

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

**Done when:** `yolo host apply --assert` maintains a delimited block idempotently, and the
user's own prose is never duplicated or lost.

---

## Phase 6 — Implement `files` in the **jail**  *(medium; per N1, this is new work)*  *(SHIPPED)*

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

## Phase 7 — Host `files` render  *(medium; the goal's "associated files")*  *(SHIPPED)*

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

## Phase 8 — Host deps for the fzf case  *(scoped; closes the acceptance test)*  *(SHIPPED)*

**Fixes:** the third leg of the fzf case. **Depends on:** nothing in this plan.

Already designed and partly built: `install_hints` +
`DepRequirements()` (`contributes.go:139`) exist, and `yolo check-deps` consumes them
(env-manager Phase 6, shipped). What is missing is **running** an install at the host notch
(env-manager Phase 4.3 — the confirm UX).

**Scope discipline:** do **not** build the batched-by-elevation-class confirm UX here; that
is env-manager Phase 4.3's own increment with its own resolved OQs (OQ-6/7/9). This plan
needs only that `yolo host apply` **reports** the missing deps with their remedy, instead of
today's line:

```
program — install below jail is confirm-gated; not run by apply --host yet (Phase 4.3)
```

- **8.1** Make that line resolve the actual dep state: probe each `DepRequirement`'s `Bin`
  and print present/missing plus the `install_hints` remedy for the detected host manager.
  Reuse `check-deps`' probe rather than writing a second one.
- **8.2** Leave the *running* of installs to env-manager Phase 4.3, and say so in the
  output.
- **8.3 — SHIPPED (2026-08-02).** All six shipped packs now carry verified `install_hints`, so
  the "print the remedy" branch is the *common* path: `check-deps` and `yolo host apply` name the
  exact install line instead of landing in the no-remedy case. Every name was verified against
  the manager's own index (brew's `formulae.brew.sh` API, `nix eval nixpkgs#<attr>` plus the
  nixpkgs expression's `mainProgram`/`installPhase`, Arch's `packages/search/json` and the
  package file list, Fedora `mdapi`/dist-git, Debian+Ubuntu name and contents search) — never
  inferred from the binary or npm name. What shipped:

  | pack (bin) | brew | nix | pacman | dnf | apt |
  |---|---|---|---|---|---|
  | claude (`claude`) | `claude-code` (cask) | `claude-code` | — | — | — |
  | copilot (`copilot`) | `copilot-cli` (cask) | `github-copilot-cli` | — | — | — |
  | codex (`codex`) | `codex` (cask) | `codex` | `openai-codex` | — | — |
  | opencode (`opencode`) | `opencode` (formula) | `opencode` | `opencode` | — | — |
  | pi (`pi`) | `pi-coding-agent` (formula) | `pi-coding-agent` | — | `pi-coding-agent` | — |
  | agy (`agy`) | `antigravity-cli` (cask) | `antigravity-cli` | — | — | — |

  **Genuinely empty, and why — do not re-research these:**

  - **apt is empty for all six.** No Debian or Ubuntu suite packages any of them, in any
    release. These are npm-distributed or curl-to-shell tools; Debian's only near-misses are
    unrelated (`librust-codex-dev`, the Haskell `libghc-copilot-*` stream-language libraries,
    `ctwm` "Claude's Tab Window Manager"). Debian's own contents search finds no
    `/usr/bin/{claude,codex,copilot,agy,opencode,pi}` in unstable.
  - **dnf is empty except pi.** Only `pi-coding-agent` exists in Fedora, and only in
    **Rawhide** (0.80.3-4.fc45; dist-git has just `rawhide`/`main`, so F43/F44 have nothing).
    It is kept anyway: it is correct where it resolves, and it provides `/usr/bin/pi`. No
    dist-git repo exists for the other five under any plausible name.
  - **pacman is empty except codex and opencode.** Only `extra/openai-codex` (which ships
    `/usr/bin/codex`) and `extra/opencode` are in the official repos. The other four exist
    **only in the AUR** (`claude-code` 87 votes, `github-copilot-cli` 14, `antigravity-cli` 22
    but flagged out-of-date, `pi-coding-agent` 18) — and an AUR package is not a `pacman`
    package (`pacman -S` cannot install one), so per the hint contract they are omitted.
  - **No manager anywhere packages a bare `copilot`.** Two live traps: the brew **formula**
    `copilot` is AWS's ECS/Fargate CLI (deprecated, unrelated), and nixpkgs `copilot-cli` is a
    `throw` ("removed due to upstream end-of-life") while `gh-copilot` is a `throw` pointing at
    `github-copilot-cli`. The correct names are brew **cask** `copilot-cli` and nix
    `github-copilot-cli`. This pair is exactly the wrong-hint hazard 8.3 refused to guess at.
  - **`antigravity` is three different products.** nixpkgs `antigravity` is an alias to
    `antigravity-ide` (the IDE, `mainProgram = "antigravity-ide"`); the CLI is
    `antigravity-cli`, whose `installPhase` is `install -Dm755 antigravity $out/bin/agy`. Brew
    has all three tokens (`antigravity` the desktop app, `antigravity-ide`, `antigravity-cli`);
    only the `antigravity-cli` cask targets `bin/agy`.
  - **Four of the five confirmed brew hints are casks, not formulae** (`claude-code`, `codex`,
    `copilot-cli`, `antigravity-cli`); no formula of any of those names exists. Bare
    `brew install <token>` is right — brew's `load_formula_or_cask` falls back to a cask when no
    formula matches — so the printed remedy is correct. **But `depcheck.Manifest` emits
    `brew "<token>"`, and a Brewfile `brew` entry runs `brew install --formula <name>`
    (`Library/Homebrew/bundle/brew.rb`), which cannot resolve a cask.** The generated Brewfile
    is therefore wrong for those four; see the new defect below.
  - **nix covers all six**, but `claude-code`, `github-copilot-cli`, and `antigravity-cli` are
    `unfree` — a bare `nix profile install` on them refuses until `allowUnfree`. The remedy line
    is the right package; it is just not sufficient on its own for those three.

- **8.4 — SHIPPED (2026-08-02). `depcheck.Manifest` could not express a brew cask.** It wrote
  every package as `brew "<pkg>"`, but brew bundle installs a `brew` entry with `--formula`, so
  the four cask-only hints above produced a Brewfile that fails. Fixed with the `brew-cask`
  hint key (chosen over a per-hint struct: `install_hints`' virtue is one line per manager, and
  a nested object would rewrite every existing hint for one flag). `installCmd` maps it to
  `brew install --cask <pkg>`, `Manifest` emits `cask "<pkg>"` after the formulae, and the
  lookup consults `brew-cask` before `brew` — `DetectManager` still returns plain `brew`, since
  the flavor is a property of the package, not the host. All four tokens re-verified against
  `formulae.brew.sh` as cask-200 / formula-404: `claude-code`, `copilot-cli`, `codex`,
  `antigravity-cli`. `opencode` and `pi-coding-agent` are genuine formulae and stayed on `brew`.
  Explicit `--cask` in the one-liner too, even though bare `brew install` would fall back to
  one: the fallback prefers a same-named FORMULA, which is how brew's `copilot` (AWS's
  deprecated ECS CLI) would get installed instead of the `copilot-cli` cask.

**Done when:** `yolo host apply` on a pack declaring `fd`/`fzf` names which are missing and the
exact install line, without running anything.

---

## Phase 11 — three `program`/staging defects found by building the real pack  *(OPEN)*

Found by authoring the fzf pack (`docs/examples/claude-fzf-pack/`) rather than by any test.
All three are why that pack ships **no `program` contribution** — declaring one made the jail
worse. Ranked by blast radius.

- **11.1 — A `program` contribution SHADOWS an image-provided binary and breaks it.** The
  generated `~/.yolo-shims/<bin>` launcher precedes `/bin` on PATH, and when its lazy install
  fails it exits 1 rather than falling through to the real binary. Verified independently for
  `fd` and `fzf`, both of which the image already bakes. So a pack that honestly declares "I
  need fzf" *removes* working fzf from the jail — the opposite of the contribution's meaning,
  silently.

  The fix is a judgment call, which is why it is recorded rather than patched: either the
  launcher falls through to an existing binary on PATH when its install fails (safe, but
  masks a genuinely broken install), or `program` is skipped at generation time when the bin
  already resolves in the image (cheaper, but then the shim's laziness never applies to a
  package the image later drops). Needs a decision.

- **11.2 — Only the FIRST `program` per pack installs in a jail.** `InstallContribution()`
  (`internal/packdecl/contributes.go:123`) `return`s inside its loop, so a pack declaring `fd`
  **and** `fzf` gets a launcher for `fd` only — while `DepRequirements()` (the host path)
  correctly returns both. An asymmetry between the two notches with no diagnostic either way.
  The accessor's name is singular, so this may be an intended one-program-per-pack rule that
  is simply unenforced and undocumented; if so the fix is a validation error, not a loop.

- **11.3 — A dropped pack's staged tree is never cleared, so it keeps rendering.**
  `stagePacks` clears `_official` only (`internal/cli/run/packs.go:97`); a CONFIGURED pack's
  staging dir under `paths.AgentsDir()/<cname>/packs/<slug>` survives removal from config.
  Observed live: a deleted test pack kept regenerating its broken `fzf` shim across launches
  until the dir was removed by hand.

  This **contradicts the invariant `AGENTS.md` states** — *"`stagePacks` copies only the
  SELECTED packs into the mounted tree (and clears it, so a dropped pack stops rendering)"* —
  so either the code or the doc is wrong, and the code is the one causing the surprise. Note
  the fix must preserve rule 3 from `packstage` (clear CONTENTS, never the dir itself: a
  running jail's bind mount captured the inode).

---

## Phase 9 — Capability tiers: be as nice as each agent allows  *(medium)*  *(SHIPPED)*

**Motivated by:** N5 + N6. Skill delivery quality is **bounded by the agent**, so the plan
must encode that rather than pick one global rule. **Depends on:** Phase 4 (tier A is what
Phase 4 builds). **Supersedes:** OQ-D.

The insight to encode: *how nice yolo can be is a property of the target tool, and it is
knowable.* Two tiers today.

| Tier | Agents | Mechanism | Collision | Removal |
|---|---|---|---|---|
| **A — namespaced** | `claude`, `copilot` | pack owns `<skills-dir>/<pack>/` with `.claude-plugin/plugin.json`; skills invoke as `<pack>:<skill>` | structurally impossible (but see N6.3 for copilot's bare-name dedup) | safe — delete within the pack dir |
| **B — flat** | `codex`, `pi`, `agy` | entries written directly into the agent's skills dir | possible; must be detected | needs a manifest |

- **9.1 Declare the tier, and probe it.** Add the tier to the pack's `skills` contribution
  (defaulting to B — the safe tier — when unstated), NOT inferred from the destination path.
  Inference was the earlier recommendation and it is wrong: it hardcodes
  `.claude/skills → tier A` into core, which is exactly the "core knows a tool's name"
  coupling `AGENTS.md` forbids. A pack declares what its tool can do; core renders it.
  **Probe before trusting it** — tier A requires that a `.claude-plugin/plugin.json` in the
  destination is actually honored; N6 is bundle-verified, undocumented, and could regress. If
  the probe is inconclusive, degrade to tier B and say so. Fail toward the safe tier.
- **9.2 The manifest (tier B).** Record what yolo wrote, per (pack, destination, entry), under
  `~/.local/state/yolo-jail/host-manifest.json`. This is the provenance data OQ-A wanted, now
  needed **only** for tier B. It answers the one question the filesystem cannot: *did yolo put
  this here, or did the user?* Write it only on `--assert`; never on `observe`.
- **9.3 Warn before overwriting (both tiers).** A destination entry that exists and is **not**
  in the manifest is the user's. Report it by name before touching it, reusing the
  always-warn posture `managedOverwrites` (`hostrender.go:103`) established for config keys.
  Tier A needs this too, for copilot's bare-name dedup (N6.3): a pack skill can be namespaced
  and still shadowed, and the user deserves to hear that.
- **9.4 Archive, don't delete.** When a skill leaves a pack (or a pack leaves the config),
  move yolo's copy to `~/.local/state/yolo-jail/archive/<timestamp>/<pack>/<skill>/` rather
  than `rm`. **The user's own content is never archived** — it is never touched at all; only
  yolo-written entries (per the manifest) are ever moved. Print the archive path so the
  action is reversible by hand.
  - *Why archive rather than delete, when Phase 4 argued delete is safe for tier A:* both are
    right at their tier. Inside a tier-A pack dir the subtree is unambiguously yolo's, so
    delete is honest. In tier B, "yolo wrote this" rests on a manifest that can be stale (the
    user edited the file; the state dir was pruned; two machines share a config), and a stale
    manifest plus `rm` is data loss. Archiving makes the failure mode recoverable instead of
    terminal. **Use archive for tier B; keep straight delete for tier A** — and archive there
    too if the entry is unexpectedly not what the manifest describes.
- **9.5 Prune the archive.** An unbounded archive is a disk leak (this repo already has a
  `yolo prune` verb and a shared-block-device constraint). Wire archive cleanup into
  `yolo prune`, oldest-first, and report reclaimed space. Do **not** auto-prune during
  `apply` — a destructive cleanup should not be a side effect of a render.

**Done when:** a tier-B agent's skills are delivered with the manifest tracking them, a
user-authored skill is warned about and never touched, a removed pack skill lands in the
archive with its path printed, and `yolo prune` can reclaim it.

---

## Phase 10 — Adapt a Claude plugin as a pack  *(medium; the "trivially pull in plugins" ask)*  *(SHIPPED)*

**Depends on:** Phase 4 (the `@skills-dir` layout), Phase 9 (tiers). **Enabled by N6:** the
same tree serves claude *and* copilot, so this is one adapter, not one per agent.

A Claude plugin already *is* nearly a pack: a directory with a manifest declaring skills,
agents, hooks, MCP servers. The pack system's job is to carry someone else's content into a
managed environment — the same job. So the adapter should be thin.

- **10.1 Recognize a plugin tree.** A pack whose root (or a subdir) carries
  `.claude-plugin/plugin.json` is a plugin-shaped pack. Read the manifest's `name`,
  `description`, and `skills` paths. **Do not** re-implement the plugin schema — read the
  fields yolo needs and pass the tree through intact, so a plugin feature yolo does not model
  still works when the tree lands in place.
- **10.2 Deliver by copying the tree, not by translating it.** For a tier-A destination, the
  plugin tree goes to `<skills-dir>/<name>/` verbatim — manifest included. That is precisely
  what both claude (`<name>@skills-dir`) and copilot (`.claude-plugin/plugin.json` in its
  search path, N6) already load. **Translation is the trap to avoid**: lowering a plugin into
  yolo's own `skills`/`hook`/`config` kinds would drop everything yolo does not model
  (agents, output-styles, `.mcp.json`) and would need updating every time the plugin schema
  grows.
- **10.3 Tier-B degradation, stated honestly.** A tier-B agent cannot load a plugin manifest,
  so only the plugin's `skills/` subtrees are deliverable, flat, under Phase 9's manifest and
  collision rules. Everything else the plugin declares (hooks, MCP servers, agents) is
  **refused by name** — never silently dropped. This is the same never-silent rule as the rest
  of the plan.
- **10.4 The trust question, and it is the important one.** A plugin is *someone else's
  repo*, and a plugin manifest can declare **hooks and MCP servers — i.e. code that runs**.
  yolo's existing gate is the right one and must not be bypassed: fetched packs get the
  `pack install` y/N host-access approval, and `allow_exec` is a **consumer** opt-in (G2's
  invariant). A plugin-shaped pack that declares hooks/MCP must surface those in
  `pack footprint` and in the install approval as the specific claims they are. **Do not**
  let plugin-as-pack become a path by which a fetched tree runs code without the approval an
  ordinary pack needs. Claude itself gates project-scope `@skills-dir` plugins behind the
  workspace trust dialog for exactly this reason (N5).
- **10.5 `yolo pack init --from-plugin <dir>`** to scaffold the wrapper, mirroring
  `claude plugin init`. Small, and it is what makes the ask "trivial" rather than "documented".

**Done when:** `yolo pack init --from-plugin` wraps an existing Claude plugin, and applying
it delivers working namespaced skills to both claude and copilot at both notches, with
non-skill components refused by name on tier B and every code-running claim visible at
install.

**Deliberately out of scope:** consuming a plugin *marketplace* (resolving
`marketplace.json`, version pinning, updates). yolo has its own fetch/lock/approve pipeline
(`packsrc`), and marrying the two registries is a separate design with its own trust model.
A plugin pulled in as a pack is pinned the way every other pack is pinned.

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
2. `yolo host apply` → every kind named; no kind silently absent (Phase 2).
3. `yolo host apply --assert` → settings key written, `file-suggestion.sh` at `0o555`,
   skills at `~/.claude/skills/claude-fzf/` + briefing block written, deps reported
   (Phases 3/4/5/7/8).
4. Re-run `--assert` → **byte-identical**; no duplicated prose (Phase 5).
5. `yolo -- claude` → the script is present and executable **in the jail** (Phases 3/6),
   fixing the live breakage the handoff found: the script is currently staged into no jail
   at all, so in-jail Claude's `fileSuggestion` points at a nonexistent file.
6. **The user's own skills are untouched and still addable**: every sibling entry in
   `~/.claude/skills/` survives, a skill added by hand *after* an apply survives the next
   one, and hand-written prose outside the briefing markers survives (Phases 4/5). This is
   the check that the namespacing decision (N5) actually holds.

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

- **OQ-A — provenance for host-written pack content. RESOLVED (N5): not needed for Claude.**
  The `@skills-dir` layout makes the pack's subtree unambiguously yolo's, so "did yolo write
  this?" is answered by the path, not by a side manifest. Phase 4 therefore gets safe
  *removal* as well as safe *update*, which the flat-merge design could not have. **Still
  open for Copilot** and any other agent with no namespace mechanism: there, skip-on-collision
  (never delete, never overwrite) remains the only safe rule, and it is what Phase 4's
  fallback path must do. A provenance manifest under `~/.local/state/yolo-jail/` is the
  eventual answer if that fallback proves too weak; not needed to ship this plan.
- **OQ-D — per-agent skills delivery strategy. SUPERSEDED by N6 + Phase 9/10.** The answer is
  a declared **capability tier** per destination, not inference: tier A (plugin-namespaced)
  for claude + copilot, tier B (flat, manifest-tracked) for the rest. See N6 and Phase 9.
- 💬 **OQ-B — should `files` at the host be `0o444`?** Phase 7.1 mirrors the jail's `:ro`
  posture, but a `:ro` mount is *enforced* while `0o444` is merely *asymmetric*
  (`project_prism_ro_rw_audit` made this distinction). A user who wants to hand-edit a
  pack-delivered file will `chmod` it and lose that edit on the next apply.
  `0o644` + the overwrite warning may be the friendlier default.

  _Leaning:_ **`0o444` for consistency** — but this one is taste, not correctness, and it is the
  same asymmetry as backlog items E1/E2, so **decide all three together or none of them.**

  **Answer:**
  > _(empty — fill in when decided)_
- ~~**OQ-C — does the same-`into` collision check belong to `files` only?**~~ **RESOLVED
  2026-08-02.** Phase 6.2 fixed it for `files` (a genuine `CombineExclusive` violation); the
  identical podman failure for two `skills` contributions sharing an `into` was fixed
  separately as the mount-dedup it actually is — `seenSkillDest` in
  `internal/cli/run/assemble.go` emits ONE bind per destination, which is correct because
  `PrepareSkills` has already merged every pack's skills into each staging dir, so the second
  mount would carry identical content. The old advice ("do not declare an `into` another pack
  uses") was unfollowable in the case that matters most: an agent pack naming
  `~/.claude/skills` plus a user pack sharing a corpus is the whole point of the kind.

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
