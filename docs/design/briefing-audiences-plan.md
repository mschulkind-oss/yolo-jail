---
title: "Plan: audiences for pack briefing and skills"
date: 2026-09-02
status: ready
tags: [packs, briefing, skills, implementation-plan]
summary: "Build hand-off for briefing-audiences.md: the file map, the seams that already exist, the traps that compile, and the order that keeps each step green."
---

# Plan: audiences for pack `briefing` and `skills`

**Design:** [`briefing-audiences.md`](briefing-audiences.md) (DECIDED; OQ-BA1..BA7 all ruled, §9 is
the build order) · **Status:** ready · Written against `95ef881c`, 2026-09-02.
**Precedence:** the design wins on behavior → the tree wins on fact → this plan is advice and is
the first thing to be wrong. Never twist the code to match it.

**The design's anchors were repinned 2026-09-02, then ~11 commits landed.** Re-derive by searching
for the construct, never by offset. Measured drift: `contributes.go:1222`/`:1214-1218` → `:1351`/
`:1343-1347`; `footprint.go:399`/`:425-427`/`:435`/`:460-461`/`:565` → `:410`/`:436-438`/`:446`/
`:544-546`/`:600`; `luahook/derive.go:163` → `:174`; `assemble.go:845`/`:862` → `:843`/`:863`.
Everything the design cites in `hostbriefing.go`, `prepare.go`, `mergedest.go`, `briefingsource.go`,
`config/packs.go`, `kinds.go` and `manifest/load.go` still lands, as does §2's six-pack table.

## Map

| Path | Change |
| :--- | :--- |
| `internal/packdecl/contributes.go` | `Agent string` + `Agents []string` on `Contribution` (field block `:58-99`); refusal on other kinds beside the `Profile` refusal `:1273-1278`; `into` conditional at `:1351` |
| `internal/packload/footprint.go` | new exported agent-name collision pass (copy `LoopholeNameCollisions`, `:600`) |
| `internal/packload/mergedest.go` | `declares` `:141` tests `Into != ""`; `borrowedDestinations` `:174` filtered by selector |
| `internal/entrypoint/hostbriefing.go` | `ComposeHostBriefings` `:133` builds path→`agent` first, filters beside the `prose == ""` skip at `:161` |
| `internal/cli/run/packs.go` | seventh pre-flight beside `:312`/`:328`; `briefings` collection `:165-171`,`:231-234` reshaped or dropped |
| `internal/cli/run/prepare.go` | `ComposePackBriefings` `:154` moves into the loop `:170-186`, which becomes **destination-first**; `briefingStagingName` `:457` keyed by destination |
| `internal/cli/run/assemble.go` | mount loop `:661-688` uses the shared staging key; add the missing `c.Into == ""` guard |
| `internal/cli/apply.go` | gate at `:450` (`applyHostBriefings`); `:277` is where a new collision pass joins |
| `internal/cli/check/packs.go` | mirror the collision pass beside `ConfigSurfaceCollisions` `:183` |
| `internal/jailcontent/skills.go` | step 7: `SkillTarget` `:44` gains identity; `packSkillDirs` `:30` stops being a flat `[]string` |
| `packs/{claude,codex,copilot,opencode,pi,agy}/pack.json` | `"agent": "<bin>"` on `briefing` (later `skills`). **Keep `into`** — trap 4 |

## Reuse

- **`packdecl.ValidBinName` (`contributes.go:1240`) + `binProblem` (`:1250`)** — `agent`/`agents`
  values *are* bin names (OQ-BA1); reusing them is what stops the two namespaces drifting apart.
- **The `Profile` refusal, `contributes.go:1273-1278`** — the shape for "refused on every other
  kind", placed *ahead* of the kind switch so a kind added tomorrow inherits it. Copy the placement.
- **`packload.LoopholeNameCollisions` (`footprint.go:600`)** — the template for the OQ-BA6/BA7 pass,
  structurally verbatim: exported, per-declaration, its own pass because the generic loop skips
  non-`CombineExclusive` kinds (`:446`) and single-pack groups (`:436-438`). Its docstring names the
  wiring precedent; `run.PackLoopholeNameConflicts` (`packs.go:489`, called `:312`) is the message style.
- **`o.checkProfileTargets` (`packs.go:372`)** — the nearest twin of the selector gate: "a selector
  keyed to a CLI name no pack installs", already fatal, already at the right point in the pipeline.
- **`config.UseProfileCLINames` (`config/packs.go:514`) is the WRONG candidate set** — it unions
  `packload.Embedded()`, which AGENTS.md states is deliberately not selection-gated, so it accepts
  `codex` in a jail that never selected codex. P3 wants the `agent` names declared by the packs in
  `loaded`. (Any source that is the *selected* set satisfies this.)
- **`p.BriefingProseFor(c)` (`briefingsource.go:56`)** is the per-CONTRIBUTION resolver the jail half
  needs; `p.BriefingProse()` (`:116`) is the per-pack one whose limit §5 lifts — stop calling it.
- **Fixtures that already build the state:** `packload/mergedest_test.go:20 agentPack(t, name,
  contributes...)` (in-memory, **bypasses the validator** — the only way to build an `into`-less
  contribution before step 2) and `:28 zeroCeremonyPack`; `packload/skillssource_test.go:14
  skillsPack(t, manifest, dirs...)` for a manifest string through `LoadDir`;
  `entrypoint/hostbriefing_test.go:27 briefingPack` / `:34 briefingPackFrom`;
  `integration/packs_test.go:284 packHome(t, userConfig)`.

## Traps

- **`ResolveDestinations` is HOST-ONLY.** Sole production callers are `internal/cli/apply.go:246`
  and `:262`; the jail launch path never calls it. The `declares()` fix therefore buys nothing in a
  jail — jail routing for an `into`-less contribution comes only from the destination-first
  restructure. §9 step 3 reads as if one change covers both notches.
- **Changing `declares()` leaves the suite GREEN.** `into` is required today, so `Kind == kind` and
  `Kind == kind && Into != ""` are equivalent for every manifest `LoadDir` will accept. The nearest
  test, `mergedest_test.go:188 TestResolveDestinationsInfersPerKind`, catches *cross-kind* leakage,
  not this. Nothing turns red; the failure mode is silent delivery-to-nowhere. Hence step 1.
- **`assemble.go:661-688` has no `c.Into == ""` guard** — unlike `ComposeHostBriefings` (`:141`) and
  `hostBriefingPaths` (`:533`). Line `:685` emits `staged+":/home/agent/"+c.Into+":ro"`, so the first
  `agents`-only briefing mounts a file over `/home/agent`. Guard it in the same commit as the field.
- **The version boundary — the `tier` incident's fifth shape.** `DecodeTolerant` (`packdecl.go:282`)
  ignores unknown *fields*, so adding `agent`/`agents` is skew-safe; but `Validate` still runs over
  what is kept, so the moment `into` is legally omitted an OLD baked entrypoint refuses the boot with
  `kind "briefing" needs "into"`, unrecoverable without `just load`. **Constraint: the six shipped
  packs keep `into` forever** (they declare `agent` *beside* it), so only a user's own pack can reach
  the new shape.
- **`ComposeHostBriefings` is pure with three callers** — `HostBriefingAdoptions` (`:199`),
  `RenderHostBriefings` (`:390`), `PruneHostBriefings` (`:474`) — each passing a *different* pack
  slice. Build the path→`agent` map inside it from that same slice; a map passed in makes the prune
  and the adoption gate answer differently, which is how the two notches diverged before.
- **The jail's selector cannot survive `stagedPacks.briefings`.** It is
  `[]jailcontent.PackBriefing{Name, Text}`, built at `packs.go:168`/`:232` via `packBriefingProse`
  (`packs.go:953`); the contribution is gone by `prepare.go:154`.
- **`skills` is not a filter either.** `jailcontent.SetPackSkillDirs` (`skills.go:34`) is a flat
  `[]string` package global and `SkillTarget` (`:44`) carries no identity — step 7 is the same
  restructure as briefings, not a one-line predicate.

## House rules this change touches

- **Constraint:** `internal/entrypoint` writes generated files through `WriteInPlace` /
  `WriteStringInPlace` (`internal/entrypoint/fsx.go:36`,`:47`), never tmp+rename — a file bind mount
  is pinned to the inode it captured, and a lint rule bans rename-based writes outside fsx.
- **Constraint:** a test must fail when the production CALL SITE is deleted, not only when the helper
  is wrong (shipped five times; AGENTS.md, Testing). Per notch — design R3 is right that the two
  notches change differently.
- **Constraint:** pre-commit runs `just check-ci`; `just format` first; never `--no-verify` or
  `--amend`. Stage with `git add -N`, then `git commit -- <paths>`. `git add` new files before any
  image rebuild — nix sees tracked files only, and a new `pack.json` field is exactly what made ~10
  integration tests fail with `unknown field "tier"` from a stale baked entrypoint; run the suite
  with `YOLO_TEST_REBUILD_IMAGE=1`.

## Build order

1. **The failing test.** `internal/packload/mergedest_test.go`: a content pack declaring
   `{kind: briefing, agents: ["claude"]}` with no `into` (built with `agentPack`, not `skillsPack` —
   the validator would refuse it), set against `agentPack(t, "claude", …)`; assert
   `ResolveDestinations` infers `.claude/CLAUDE.md`. → `go test ./internal/packload`
2. **Field + validation + routing.** `agent`/`agents`, the refusals, the `into` conditional,
   `declares` → `Into != ""`, `borrowedDestinations` filter, the `assemble.go` guard, the six
   `"agent"` lines. Turns step 1 green. → `just test-fast`
3. **Ownership** (§9 step 1). The collision pass plus its three wirings — launch pre-flight,
   `yolo host apply`, `yolo check`. Real from day one though nothing routes on it yet. → `just test-fast`
4. **Host notch filter** in `ComposeHostBriefings`. Smallest visible slice, observable via
   `yolo host apply --observe`. → `go test ./internal/entrypoint ./internal/cli`
5. **Jail notch move.** Destination-first loop, staging keyed by destination through one helper
   `assemble.go` also calls (design R2). Lifts the one-prose-per-pack limit. → `just test`
6. **Resolution + severity** (§4.3) at the two gates, R3-grade diagnostics. → `just test-fast`
7. **`skills` by substitution**, then targeting in `pack lint` / `pack footprint`. → `just test`

Steps 1–4 are independently shippable. Step 5 is the one to review hardest: it moves a host↔jail
staging-name contract, so both spellings land in one commit.

## Ships with

- **Unit cases** — selector matches a declared `agent`; selector matches nothing (reported, not
  silent — risk R1); `agents` + `into` together (refused); `agents` on a kind that does not take it;
  an unknown *or* unenabled name (fatal, one message for both — OQ-BA3); two packs claiming one name
  across *different* kinds (OQ-BA7); one pack claiming its own name in several kinds (legal —
  `footprint.go:436-438`); a destination declaring no `agent` (never selected, never an error — R4).
- **The integration test that would catch this breaking end to end:** a new case in
  `integration/packs_test.go` beside `TestPackDeliversSkillAndBriefing` (`:23`) — two agent packs
  selected (`claude`, `codex`) plus one content pack with `agents: ["claude"]`; assert the prose is
  in `/home/agent/.claude/CLAUDE.md` and **absent** from `/home/agent/.codex/AGENTS.md`. There is no
  pack-tree helper in that package: tests inline `t.TempDir()` + `os.WriteFile` and select through
  `packHome(t, userConfig)` (`:284`). No unit test can see this — the jail's compose and mount halves
  live in different functions.
- **Rewrite, not repair:** `internal/cli/run/assemble_test.go:406 podmanLinuxGolden` pins the mount
  line `briefing-claude.md:…/.claude/CLAUDE.md:ro` at `:545`, and `:543` comments that the staging key
  is per-PACK — that is exactly what step 5 replaces. `ac_materialize_test.go:45` and the
  `briefingStagingName(...)` readers in `briefinghostoverlay_test.go:100`,
  `briefingapplied_test.go:59`, `briefingbackend_test.go:66`, `consumehandoff_test.go:87` follow the
  helper and adapt without changing meaning. P2 (silence means broadcast) is why
  `internal/cli/applyhostzeroceremony_test.go` and `applyhostbriefings_test.go` should stay green —
  if they do not, the change has broken the unaudienced path.
- **Docs describing the old behavior**, by path: `internal/cli/config_ref.txt:853` (`briefing` kind),
  `:848-852` (`skills`), `:955-960` ("Pack prose is appended to each briefing"), and `:936-938` (the
  note the design already flags as stale on `from`); `internal/cli/pack.go:75-76` (kind table in
  `yolo pack --help`), `:243-245`/`:250-251` (the `pack init` scaffold's own prose — "every selected
  agent's briefing"), `:507-521` (the lint notice whose advice *inverts* under a selector);
  `docs/design/agent-briefings.md:158-160` + `:162-171`; `docs/design/pack-system.md:170-171`, `:343`,
  `:375`, `:1033-1041`, `:1102-1119`; `docs/guides/migrating-to-packs-and-host-management.md:107-108`,
  `:134`, `:222-224`, `:315-330`; `docs/examples/claude-fzf-pack/pack.json:33` and its `README.md:28`.
- **No JSON schema exists** — the doc comments in `internal/packdecl/contributes.go` ARE the pack
  manifest reference, so the new fields' comments are a shipped surface. Claim strings live at
  `internal/packdecl/kinds.go:254-261`; the renderer `internal/cli/pack.go:548-568`
  (`printClaimLines`) has no slot for an audience — step 7.
- **Cheap and yours:** the destination→staging-filename encoding (any injective sanitizer; only the
  two call sites sharing the helper read it) and the shape of the path→agent map.

## Don't

- No config key, CLI flag, or new contribution kind — §7 forecloses all three.
- Don't build a `bin`→pack index to derive a destination's identity: it was this design's own first
  draft and was rejected (OQ-BA2/A2) — nothing in the `-p` chain derives anything, down to
  `packs/claude/derive.lua:5` hardcoding its own name.
- No denylist (`except:`) — OQ-BA3; §4.3's note says why it would buy nothing.
- Don't let `yolo pack lint` refuse an unknown name: it takes a single pack root with no config and
  cannot know the enabled set (§4.3 / R5 — move the gate, do not lower the severity).

## Blockers

- **None unruled** — the design's §10 records zero open questions.
- **Stop and ask** on both of these; each changes behavior the design fixed and is expensive to unwind:
  1. **An `agents` selector that matches no enabled destination — refuse, or report and skip?** P3
     makes an unenabled *name* fatal; risk R1 describes an addressed contribution that matched no
     destination as *reported*. Those are different sentences, and the difference is a launch.
  2. **`packs.go:168`/`:232` — reshape `stagedPacks.briefings` to carry contributions, or delete it
     and read `BriefingProseFor` in the loop?** It crosses the `stagePacks` return signature, which
     the attach path and several call sites depend on.
