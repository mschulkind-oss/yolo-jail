---
title: "Plan: pack briefing defaults"
date: 2026-09-22
status: accepted
tags: [plan, packs, briefing, skills, defaults]
summary: "Build hand-off for pack-briefing-defaults.md, grounded in the tree at 5df91b7d: the file map split into five disjoint build steps, the one governance predicate all three notch sites call, and the traps (the ResolveDestinations clone, the self-borrow skip, host/jail spacing parity, AGENTS.md fixtures)."
vantage:
  status-chip: true
---

# Plan: pack briefing defaults

**Status:** BUILT 2026-09-23. MEASURED: see the design's [Status line](pack-briefing-defaults.md) —
nested-jail verified. Written against `5df91b7d`; the plan is spent and kept for its map and traps.

**Design:** [`pack-briefing-defaults.md`](pack-briefing-defaults.md), every question ruled
([ledger](pack-briefing-defaults.md#decision-ledger)). **Precedence:** the design wins on behaviour,
the tree wins on fact, and this file is the first thing to be wrong. Never twist code to match it.

## The governance predicate

The contract P3 and R5 require. **Constraint:** the jail composer, `SkillsSources` and
`borrowingSources` all derive from it, and none keeps its own `declared` gate.

```go
// internal/packload/governance.go (new)
type GovernedSource struct {
	Rel      string                // cleaned, slash-separated, pack-relative: "briefing/a.md", "files/pi.md", "skills"
	By       packdecl.Contribution // the ONE governing content contribution; Contribution{Kind: kind} when Implicit
	Implicit bool                  // named by no declaration: the P2 broadcast
}
func (p *Pack) GovernedSources(kind packdecl.Kind) (sources []GovernedSource, problems []string)
```

- **Kinds:** `briefing` and `skills` only. `files` returns nil.
- **Destinations are skipped.** A contribution with `Agent` set governs nothing (P5). If it counted as
  an omitted-`from` content contribution, it would switch off an agent pack's implicit broadcast.
- **Briefing:** a `from` names that one file. An omitted `from` names every
  `briefing/*.md` that no other contribution names. The rest is `Implicit`. **Skills:** an omitted
  `from` means `"skills"`. The tree is one unit.
- **Sorted.** Briefing sources sort byte-wise by `Rel`, across the whole pack. That is this plan's
  reading of [§3.1](pack-briefing-defaults.md#31-the-briefing-directory-is-what-a-pack-ships-agentsmd-is-never-read)'s "within a pack, its files are ordered by filename"; it also keeps the order
  independent of the `contributes` order ([§3.3](pack-briefing-defaults.md#33-a-file-is-governed-by-the-contribution-that-names-it)). Skills sources keep declaration order, with the
  implicit source last.
- **Problems:** [§3.4](pack-briefing-defaults.md#34-a-declared-source-is-the-only-source) only. A declared `from` that is absent, not a file, empty, whitespace-only or
  escaping delivers nothing and is reported, at today's warning severity. An absent convention is
  silent.
- **Reads the ORIGINAL declaration** (see [Traps](#traps)).

## Map, by build step

Each file belongs to exactly one step. "By function" means the step owns only the named functions or
tests in that file.

| Step | Path | Change |
|---|---|---|
| **1 core_validation** | `internal/packdecl/contributes.go` | `validateContribution`'s `KindSkills, KindBriefing, KindFiles` arm (`:2166`): neither `into` nor `agents` is valid for briefing/skills. `Agent` set still needs `into`. `from` on an `Agent`-set briefing/skills is refused, with the `files` message shape at `:2184`. `files` keeps `req("into")`, and its message now gives the reason. Reserved-basename `from` refusal. **New:** `DefaultBriefingDir = "briefing"`, `RepositoryInstructionFile(rel) bool` (basename is `AGENTS.md`, `CLAUDE.md` or `GEMINI.md`, exact case), `ConventionalBriefingFile(rel) bool` (`briefing/<x>.md`, one level), and `validateDuplicateContentSources` ([OQ-PB5](pack-briefing-defaults.md#decision-ledger)), wired into `validateContributions` (`:1556`). Rewrite the `Agents` doc (`:159`) and the arm's comment block. **Leave** `DefaultBriefingFiles`/`BriefingCandidates` compiling, because steps 2 and 3 still call them |
| 1 | `internal/packdecl/contributes_test.go`, plus a new `briefingdefaults_test.go` | Rewrite `"briefing no into"` (`:130`) and the `BriefingCandidates` tests (`:584-615`) |
| 1 | `packs/*/pack.json` | **No change.** Verified: no shipped destination carries `from`, and no shipped pack has a root `AGENTS.md` or a `briefing/` |
| **2 core_resolution** | `internal/packload/governance.go` (new) | The predicate above |
| 2 | `internal/packload/briefingsource.go` | Rebuild `BriefingProseFor` (or replace it) over `GovernedSources`. Delete the fallback chain, the "used instead" message and `isConventionalBriefingFile` |
| 2 | `internal/packload/skillssource.go` | `SkillsSourceDir` returns `""` for an `Agent`-set contribution. `SkillsSources` is built on the predicate. Fix the false "precedence matches `briefing`'s" in the header (`:16-19`) |
| 2 | `internal/packload/mergedest.go` | `borrowingSources` (`:273`) returns one borrower per into-less governing contribution, plus `{Kind}` if anything is `Implicit`. Delete `declares` (`:307`). `carriesFor` asks the predicate. Remove the self-skip in `borrowedDestinations` (`:402`) |
| 2 | `internal/packload/packload.go` | `Pack` gains an unexported original-declaration field, set by the `ResolveDestinations` clone. `LoadDir` (`:739`) adds a fatal problem for a reserved basename inside `briefing/` ([OQ-PB2](pack-briefing-defaults.md#decision-ledger)), naming the move |
| 2 | `internal/packload/footprint.go` | `audienceTarget`'s neither-branch (`:280`) shows `every agent`, not a blank |
| 2 | `internal/cli/run/packs.go` | `packBriefingProses` (`:1060`): one `PackBriefing` per governed source, in the predicate's order. No `!declared` branch |
| 2 | `internal/entrypoint/hostbriefing.go`, by function | `ComposeHostBriefings` (`:138`) and `appendHostBriefingSection` (`:184`) gather each pack's sources per destination, sort them, and append them as ONE section with one label |
| 2 | `internal/jailcontent/briefing.go` | `ComposePackBriefings` (`:825`) emits the provenance label once for each run of consecutive entries from the same pack. Fix the `PackBriefing.Text` doc |
| 2 | Tests | `internal/packload/*_test.go`, `internal/cli/run/*_test.go`, `internal/jailcontent/briefing_test.go`, `internal/entrypoint/hostbriefingaudience_test.go`, `internal/entrypoint/hostbriefing_test.go` (by function: all but the migration tests), `internal/cli/applyhost*_test.go` except `applyhostbriefings_test.go`'s migration tests, `internal/cli/hostapplydetail_test.go`, `integration/packs_test.go` |
| **3 lint_scaffold** | `internal/cli/pack.go` | `packUsage` (`:58`, `:68`); `packInit` scaffold (`:275`) writes `briefing/<pack>.md`; `packLint` gets a delivery listing and not-shipped/not-read info lines; the advisory at `:584-595`; `stagedContent` (`:718`) claims the predicate's `Rel`s instead of `DefaultBriefingFiles()`; the case-1 and case-2 messages (`:516`, `:534`); root `AGENTS.md`, `CLAUDE.md` and `GEMINI.md` join `packNonContentFiles` (`:698`) |
| 3 | `internal/cli/pack_test.go`, `packinitplugin_test.go` (`:183`), `packlintaudience_test.go`, plus a new `packlintdeliveries_test.go` | Rewrite the scaffold assertions. Add the [§3.6](pack-briefing-defaults.md#36-every-delivery-and-every-refusal-to-deliver-is-shown-before-launch) cases |
| **4 migration** | `internal/cli/applyhostbriefings.go` | `localPackBriefingPath` (`:63`) targets `local/briefing/<name>.md`. A new pre-step moves `local/AGENTS.md` there, reports it, and refuses if the target exists |
| 4 | `internal/entrypoint/hostbriefing.go`, by function | `HostBriefingRequest.LocalPackAGENTS` (rename it), `MigrateHostBriefings` (`:311`), `appendToLocalPackBriefing` (`:374`), the file header's local-pack sentence, and any new move helper |
| 4 | `internal/cli/apply.go` | Comment only (`:402`) |
| 4 | Tests | The migration tests in `applyhostbriefings_test.go` and `hostbriefing_test.go`, plus a new `applyhostlocalmove_test.go` |
| **5 docs** | `docs/reference/pack-system.md` | The `:165-178` tree and prose; `#### briefing` (`:542-576`); `:1533`; `:1605`; `:1616` |
| 5 | `docs/reference/agent-briefings.md` | Audiences (`:199-219`): absent means broadcast, now in a manifest too, and per-file governance |
| 5 | `internal/cli/config_ref.txt` | `packs` (`:1086`) and the `NOTE` (`:1346`) |
| 5 | `docs/guides/migrating-to-packs-and-host-management.md` | `:119-162`, `:250`, `:397` |
| 5 | `docs/reference/information-at-the-point-of-need.md`, `packs/guardrails/README.md` | "only `AGENTS.md` is a briefing source" becomes `briefing/` (`:87`, `:102-105`; README `:6`) |
| 5 | `internal/jailcontent/builtinskills/configuring-the-jail/SKILL.md` | `:234`, "omit `from` to have yolo read the pack's own `AGENTS.md`". Shipped into every jail |
| 5 | `docs/design/slots-and-contributions.md` | One line under [OQ-D9](slots-and-contributions.md#OQ-D9) saying its premise ("no manifest can declare broadcast") is now false. Do not rule it |
| 5 | `docs/design/pack-briefing-defaults.md` | The ledger's `Built` column, the status line, and the Reads-with line that still calls this file a sketch |

## Reuse

- **Sibling refusal:** mirror `validateAddressedFiles` (`contributes.go:1636`) for [OQ-PB5](pack-briefing-defaults.md#decision-ledger). Same
  strict-only placement, and a message naming both indices. The launcher decodes strictly, so this is
  also the launch refusal.
- **Containment:** keep `BriefingProseFor`'s lexical escape check verbatim. R4's "compare cleaned
  paths" is `path.Clean` on `from`, the same cleaning the escape check does.
- **Fatal at launch:** a `LoadDir` problem is already fatal at every launch site
  (`run/packs.go:213`, `:267`, `:312`) and at lint. Adding the `briefing/` refusal there covers
  both in one place. `yolo check` is not one of them: `check/packs.go` drops a pack whose
  `LoadDir` reports problems without printing them, so it passes a pack the launch refuses. That
  gap predates this build and is outside its files.
- **Delivery listing:** build it in `pack.go` from `GovernedSources`. Advice: do not add implicit
  claims to `FootprintOf`, which feeds `Collisions` and the launch banners, so a new claim there
  changes launch output.
- **Stage-level test harness:** `packHome`, `writePack`, `writeUserPacks` and `o.stagePacks` in
  `internal/cli/run/jailbriefingaudience_test.go`. Host-level tests: `writeFile` (`config_test.go:14`) and
  `applyWith`, as `applyhostaudience_test.go` uses them.

## Traps

- **Never compute governance from a `ResolveDestinations` clone's `Decl`.** The clone appends
  synthesized `{into, from}` copies of each borrower, so every explicit `from` has two governors and
  every implicit borrower becomes an omitted-`from` declaration. Symptom: the implicit broadcast
  disappears at the host notch only. The clone keeps a pointer to the original, and the predicate
  reads that.
- **A synthesized contribution is matched to its governor by cleaned `From`.** [OQ-PB5](pack-briefing-defaults.md#decision-ledger) makes `From`
  unique among a pack's content contributions of a kind, and `""` stands for the omitted-`from` or
  implicit remainder. Every host-side reader works from this key.
- **The `other == p` skip in `borrowedDestinations` must go.** P2 includes the broadcasting pack's
  own destinations. So does [§3.5](pack-briefing-defaults.md#35-a-destination-ships-nothing)'s "addressed to itself" for an agent pack. The jail's nil audience
  already reaches them. Keep the skip and the notches diverge.
- **Spacing parity is byte-exact** (`appendHostBriefingSection` against `ComposePackBriefings`).
  Neither has a cross-notch test today; add one in `internal/cli/run` (packs → `packBriefingProses` →
  `ComposePackBriefings` vs `entrypoint.ComposeHostBriefings`, with provenance on and off, after
  trimming the jail's leading `\n\n`).
- **The host compose appends in contribution order today** (`hostbriefing.go:147-173`), with
  `d.Packs` once per contribution. Group by pack, then sort, or the host order will not match the
  jail's.
- **`agent` without `into` is still refused.** The relaxation is for content contributions only.
- **Don't fix the jail/host `into` asymmetry.** In a jail, a content `{into: X}` broadcasts, because
  `PackBriefing` has no `into`. `ResolveDestinations`' doc records that as deliberate, and it is out
  of scope.
- **Fixtures named `from: "AGENTS.md"` become refusals**, not fallbacks: `applyhostbriefings_test.go`
  (`:36`, `:272`, `:304`), `applyhostcensus`, `applyhostdepgate`, `applyhostprune`,
  `hostapplydetail`, `run/packbriefingfrom`, `run/packfiles` (`:536`), `run/packnohostgate`,
  `packload/briefingsource`, `packload/mergedest`, and `integration/packs_test.go` (`:492`). Move the
  prose to `briefing/x.md`, or name it `from: "prose/x.md"`.
- **Fixtures that rely on a root `AGENTS.md` being read** go quiet. Examples are the zero-ceremony and
  local-pack fixtures (`applyhostzeroceremony`, `applyhostlocalpack`, `applyhostagentname`,
  `hostbriefing_test`'s `briefingPack` helper) and `integration/packs_test.go:36`. **These are rewrite
  targets.** Where a test pinned the old default (`briefingsource_test.go`'s "AGENTS.md is the
  convention" and whitespace-fallback cases, and `packbriefingfrom_test.go:158`), rewrite it to the
  new behaviour. Never relax it until it passes.
- **Tests that pin a dedup now pin a refusal.** `TestJailBriefingComposesIdenticalProseOnce`
  (`jailbriefingaudience_test.go:223`: two `into`s, no `from`) and
  `TestSkillsSourceDedupesRepeatedSource` are [OQ-PB5](pack-briefing-defaults.md#decision-ledger) cases now. The tolerant jail decode does not run
  sibling checks, so keep a dedup in the readers as a fallback.
- **The reserved-name check is for sources only, not destinations.** Five shipped destinations are
  `…/AGENTS.md` (`packs/agy`, `codex`, `omp`, `opencode`, `pi`). `after: "host:AGENTS.md"` names a
  host file, not a source.
- **Not this feature:** the `AGENTS.md` in `packsrc/store_test.go`, `packstage_test.go`,
  `execclaim_test.go`, `check/packs_test.go:208` and `integration/isolation_test.go` is arbitrary
  staged content or a workspace file. Leave it.
- **The local-pack move target must not be named `AGENTS.md`**, because [OQ-PB2](pack-briefing-defaults.md#decision-ledger) refuses it inside
  `briefing/`. Give the move and the migration writer one name (cheap choice; `briefing/local.md` is
  enough). If the move is refused, stop the briefing render before compose. Otherwise the same apply
  recomposes every destination without the user's prose.
- **A local `pack.json` naming `from: "AGENTS.md"` is refused at launch** ([§4](pack-briefing-defaults.md#4-state-that-already-exists)'s second exception). By
  `contributes.go:1278`'s own record, the maintainer's local packs do exactly that. The refusal text
  must spell the edit. The step 4 move does not rewrite `pack.json`.
- **`config.InJail()` is true here and false in CI.** A test that goes through `after: "host:…"` pins
  it with `briefingreadback.go`'s `launcherInJail` seam.
- **Integration:** `TestPackDeliversSkillAndBriefing` also asserts `from pack:`, but provenance has
  been off by default since `0c74ff45`. Fix that assertion while you are in the file, or report it as
  part of the known failing baseline.

## Build order

The design's [§9](pack-briefing-defaults.md#9-what-i-would-build-in-order) puts visibility first. It moves to step 3 here, because the lint listing is a reader
of the predicate, and building it first would spell the rule twice. Final behaviour is unchanged.

1. **core_validation.** Run
   `env -u YOLO_VERSION -u YOLO_HOST_LAYERS go test -short ./internal/packdecl/...`. Other packages
   go red on the fixtures above. That is expected, and step 2 owns them.
2. **core_resolution.** Run
   `env -u YOLO_VERSION -u YOLO_HOST_LAYERS go test -short ./...`. **This is the first green
   checkpoint: commit steps 1 and 2 together.**
3. **lint_scaffold.** Run `env -u YOLO_VERSION -u YOLO_HOST_LAYERS go test -short ./internal/cli/`.
   Then, after `just build-go`, run `dist-go/linux-$(go env GOARCH)/yolo pack init` by path (never
   bare `yolo`, which is the frozen baked launcher) into a temp directory, follow the scaffold's own advice, and run lint to see **two** deliveries ([§10](pack-briefing-defaults.md#10-what-done-looks-like)).
4. **migration.** Run
   `env -u YOLO_VERSION -u YOLO_HOST_LAYERS go test -short ./internal/cli/ ./internal/entrypoint/`. Runs after step 2,
   because `hostbriefing.go` is shared by function.
5. **docs.** Run `uvx vantage-check` over every touched doc.

Then a sweep, by the orchestrator: delete `DefaultBriefingFiles` and `BriefingCandidates` once
`rg -n 'DefaultBriefingFiles|BriefingCandidates' internal cmd` shows only their definitions (done:
both are deleted). Then run `just check-ci`. Steps 2 and 3 also need the nested-jail check (AGENTS.md
[Testing](../../AGENTS.md#testing)).

## Ships with

- **Unit tests, step 1** (through `Decode`, never through the helper):
  - `{"kind":"briefing"}` and `{"kind":"skills"}` are valid.
  - `files` with neither `into` nor `agents` is refused, with the reason.
  - `from` on an agent destination is refused, naming `{"agents":[…],"from":…}`.
  - A `from` of `AGENTS.md`, `x/CLAUDE.md` or `GEMINI.md` is refused.
  - Two omitted-`from` contributions are refused, and so are `from: "a.md"` beside `"./a.md"` (R4),
    each naming both indices.
- **Unit tests, step 2:**
  - `briefing/` is read one level deep, `*.md` only, byte-wise.
  - An empty file in `briefing/` is silent. A missing declared `from` delivers nothing, is reported,
    and no other file arrives in its place.
  - The [§3.3](pack-briefing-defaults.md#33-a-file-is-governed-by-the-contribution-that-names-it) matt shape: `briefing/house-rules.md` beside `{from: "files/pi-rules.md", agents: ["pi"]}`
    delivers both.
  - A named `briefing/pi.md` is narrowed, and the rest still broadcasts.
  - `{into: ".claude/CLAUDE.md"}` sends `briefing/` only there.
  - Reordering `contributes` changes nothing.
  - `briefing/AGENTS.md` is a `LoadDir` problem.
  - Provenance puts one label on a multi-file section.
- **Call-site tests.** Each must FAIL when its call site is reverted, and each step reports the
  mutation it ran:
  - Jail: the matt shape through `o.stagePacks`. Mutation: restore `if !declared`.
  - Host: the same shape through the apply entry point. Mutation: restore the `declares` gate in
    `borrowingSources`.
  - Lint: an `implicit broadcast` line in `packMain lint` output. Mutation: drop the listing call.
  - Migration: `local/AGENTS.md` moves, and the prose is still in `~/.claude/CLAUDE.md` after one
    apply. Mutation: drop the move call.
- **Integration** (compiled by the short gate, run by CI): update `integration/packs_test.go`
  (`:36`, `:482-510`) to `briefing/`. That is the test that would catch a notch that stopped reading
  the new convention end to end.
- **Cheap and yours:**
  - The internal shape of `GovernedSources` beyond the contract above.
  - Whether `BriefingProseFor` survives as a wrapper.
  - The local-pack filename.
  - The wording of the lint lines, which must still carry [§3.6](pack-briefing-defaults.md#36-every-delivery-and-every-refusal-to-deliver-is-shown-before-launch)'s content.
  - The survey tier of the move line (advice: `tierLoss`, as for the migration lines, so it stays
    itemised).
- **Roadmap:** move row 9 of `docs/plans/roadmap.md` to `📦` in the commit that lands this file.

## Don't

- Don't read `AGENTS.md` behind a flag or a variable. There is no hatch, by ruling ([OQ-PB3](pack-briefing-defaults.md#decision-ledger)), and no
  notice at launch or at host apply.
- Don't make `files` broadcastable, and don't route by filename ([§5](pack-briefing-defaults.md#5-non-goals)).
- Don't let fetched packs behave differently ([OQ-PB4](pack-briefing-defaults.md#decision-ledger)).
- Don't change the severity of a mistyped `from` ([§3.4](pack-briefing-defaults.md#34-a-declared-source-is-the-only-source) leaves it to
  [`reference-mismatch-diagnostics.md`](reference-mismatch-diagnostics.md)).
