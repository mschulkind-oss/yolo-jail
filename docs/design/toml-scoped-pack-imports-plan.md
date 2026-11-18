---
title: "Build hand-off for direct user-level TOML pack scoping"
status: accepted
stage: DECIDED
next: "Continue authorized implementation under the amended inspection contract and its caller regressions"
depends-on:
  - toml-scoped-pack-imports.md
  - ../research/toml-scoped-pack-imports.md
tags: [packs, skills, toml, implementation-plan]
---

# Build hand-off for direct user-level TOML pack scoping

**Status:** 2026-11-17. Written against `a4dfc67d847d7d7c0b5fd06515cf93e86a06ad54`.
The grammar gap and caller seams were confirmed from source at that baseline. The feature remains
unbuilt; no completed implementation is claimed. The carrier and inspection contracts are accepted.
This remains the complete hand-off for the authorized ongoing implementation of the full direct
user-level feature, with the amended inspection contract, not evidence of completed code.

**Behavioral authority:** [the design](toml-scoped-pack-imports.md).
**Evidence:** [the source survey](../research/toml-scoped-pack-imports.md).
The design wins on behavior; the tree wins on cosmetic fact drift; this plan is the first
thing to correct. Do not twist production code to match an overtaken plan.

## Preconditions and operating fence

**Must:** one writer per checkout, serial forward commits, no amend or hook bypass. Stop and
report a noncosmetic mismatch, unexpected green red-test, dirty task file from another writer,
missing named seam, necessary change beyond the task fence, or unrelated gate failure.
Ordinary compiler/test repair inside the assigned unit is permitted; behavior changes are not.

**Must:** preserve invocation HOME/TMPDIR/GOENV and shared Go caches. Run `go env GOCACHE
GOMODCACHE GOENV` before tests. Go fixture helpers may isolate child/user homes through existing
package fixtures; do not globally relocate caches or set literal temporary roots. Manual evidence
and repositories live under owned `.yolo/durable/toml-scoped-pack-imports/` scratch. Retain red
and failed receipts. Never execute real agents/installers/provider APIs. No live host config,
grants, profiles, credentials, IAM, release/deploy, push/PR or changelog edits.

**Must:** implement the accepted resolver-produced directory-kind variant, not a marker-required
narrowing or unchecked skip-marker flag. Tasks preserve the full raw **and** typed feature,
never a wrapper or workspace-only reduction. No owner or engineering decision remains.

## Source map

New file names below are proposed decomposition, not existing interfaces. All existing paths
were opened at the stamped baseline. Tests can remain beside their corresponding production
boundary; exact helper factoring is cheap and delegated.

| Path | Required change |
| :--- | :--- |
| [schema](../../internal/config/schema.go) | Modern user-only entry paths/types; legacy schema unchanged. |
| [naming](../../internal/packsrc/naming.go), [references](../../internal/packsrc/references.go) | Pure modern options and validation, existing naming retained. |
| [readable lowering](../../internal/config/readablepacks.go), [entry/wire](../../internal/config/packs.go) | Separate modern selection, tuple identity, origin, compatible wire. Legacy parser/fields unchanged. |
| [resolver](../../internal/config/packresolve.go), [selected declarations](../../internal/config/selectedpacks.go) | One projection boundary before closure; embedded-FS fast path must use it too. |
| `internal/config/packprojection.go` (new) | Pure candidate selection plus source-specific confined intake; effective result shared by every resolver caller. |
| [governance](../../internal/packload/governance.go), [ordinary enumerator](../../internal/hostskills/deliver.go) | Share existing later-source-wins ordinary enumeration without importing hostskills into packload. |
| [loaded pack](../../internal/packload/packload.go), [metadata binding](../../internal/packload/embeddedmetadata.go) | Effective projection state, declaration and safe clone binding. |
| [payload](../../internal/packsrc/skillpayload.go), [sourced rows](../../internal/packload/sourcedskills.go) | Modern row origin/kind distinction, root raw support, selected rows in one grouped call. |
| [registration](../../internal/hostskills/sourcedregistration.go), [composition](../../internal/hostskills/compose.go) | Common private writer/claim behavior for projected rows; no independent discovery. |
| [needs](../../internal/packload/needs.go), [name collisions](../../internal/packload/namecollisions.go), [selection](../../internal/config/packselection.go) | Scoped-root closure refusal; repeated scoped handles not collapsed; no inert duplicate landing refusal. |
| [refresh caller](../../internal/cli/run/packrefresh.go) | Per-handle refresh dedup; lock-first projection diagnostic/recovery. |
| [staging caller](../../internal/cli/run/packs.go), [fresh payload caller](../../internal/cli/run/gitskillpayloads.go), [tree copy](../../internal/cli/run/gitskillcopy.go) | Stage only effective scoped representation, grouped payloads, inert selected local copies. |
| [tree record](../../internal/packload/packtreerecord.go), [payload tree](../../internal/packload/skillpayloadtree.go) | Persist/validate effective projection and selected carriers outside discovery roots. |
| [attach/tree diff](../../internal/cli/run/packtree.go), [jail loader](../../internal/entrypoint/packsurfaces.go) | Shared record-driven scoped load, no original pack load/enumeration, tuple-aware comparison. |
| [host resolver](../../internal/cli/checkdeps.go), [host selection](../../internal/cli/hostselection.go), [host composition](../../internal/cli/applyhostskills.go) | Same projection/row snapshot, process lifetime, parity; no host in-place unscoped fallback. |
| [pack add/remove](../../internal/cli/packadd.go), [pack help](../../internal/cli/pack.go) | Table-aware transactional writing/matching/disambiguation; exact new flags. |
| [edit validation](../../internal/config/editvalidation.go), [why](../../internal/cli/configwhy.go) | User scope, nested paths, source origins and no-transport validation. |
| [projection printer](../../internal/hostskills/sourcedprojection.go), [pack sourced surfaces](../../internal/cli/packsourcedskills.go), [check](../../internal/cli/check/packs.go) | Shared scoped report, omitted counts and unverified state. |
| [migration](../../internal/configmigrate/packs.go) | Refusal advice only; do not translate legacy filters. |
| [config reference template](../../internal/cli/config_ref.txt), [pack reference](../reference/pack-system.md), [user config](../reference/config/user-config.md), [sourced reference](../reference/git-sourced-skills.md) | Describe implemented behavior only after verified code lands. |
| [setup matrix](../../userguide/reference/settings-per-setup.md), [configuring skill](../../internal/jailcontent/builtinskills/configuring-the-jail/SKILL.md), [diagnosing skill](../../internal/jailcontent/builtinskills/diagnosing-the-jail/SKILL.md) | Update applicable modern config/help instructions, not provider or tool membership. |
| [completion census](../../scripts/completion-census.py) and [generated readers](../../scripts/completion-readers.json) | Rerun after source-reading tests; commit actual generated dependency inventory, not script churn. |

## Reuse and traps

- **Must reuse** `config.ResolvePack`/`ResolvePackForProcess`; `SelectPacks` gathers effective
  declarations before `Selection.Close`. Avoid per-surface projection implementations.
- **Must reuse** `GovernedSources` declaration-order metadata and `collectSkills` later-source-wins
  logic. Move/share the minimal enumerator below both packages if needed; avoid a dependency cycle.
- **Must reuse** `ResolveSelectedSkillPayloads`, immutable payload planning/publication, private
  registration, row claims, archive and planned-link sync. No parallel filtered payload store.
- **Must reuse** `configedit.ApplyTargeted`, `configdoc` paths, complete edit validation, expected-byte
  verification and transactional writes; no text-format table identity matching.
- **Must reuse** [the TOML fixtures](../../internal/config/readablepacks_test.go),
  [CLI fixture](../../internal/cli/packadd_test.go), and
  [source-free staged fixture](../../internal/cli/run/gitskillsubtrees_test.go).
- **Trap:** branching only after ordinary `packstage.Check` rejects unsafe unselected Git support
  and reads raw manifests. Scoped intake must branch before whole-pack Check/Stage/LoadDir.
- **Trap:** `SelectedPackDeclarationsDetailedWithStore` handles embedded FS directly, bypassing
  `ResolvePack`; parsing the new fields alone leaves config validation and check unprojected.
- **Trap:** `Pack.origDecl` can retain unprojected contributions after a clone; effective scoped
  governance and metadata binding must not reference the original full declaration.
- **Trap:** `SourcedSkillRows` uses declaration indices and grouped labels containing `Pack.Name`;
  legal repeated handles need stable per-import row labels, not one handle-only label.
- **Trap:** `NameCollisions` accepts one namespace owner already but refuses duplicate landings.
  Scoped entries have no file/home landing claim; do not weaken collisions between whole packs.
- **Trap:** `ResolveNeeds` joins through a name map and ignores already selected targets. Check a
  canonical scoped shipped root before that no-op. Ordinary third-party needs stay refused.
- **Trap:** `diffPackSets` maps only by name. Legal repeated handles would hide selection changes.
  Key by import tuple, compare digest separately; a changed selector remains the same import.
- **Trap:** refresh lock source is per handle. Preserve base authored source/ref for lock and
  stamps; append `subdir` only to the checkout address at the resolved commit. Dedup refresh
  requests per handle. Never let two raw entries overwrite one handle's lock with different roots.
- **Trap:** process-only carrier copies must survive host composition; declaration scratch removed
  on resolver return cannot supply skill data. Use process tree lifetime where bytes are needed.
- **Trap:** old no-record fallback scans directories. Scoped carriers stay in reserved inert roots;
  both fallback readers skip them. Do not reinterpret missing scoped record as whole pack.
- **Trap:** an early stage reads sourced rows, while `prepareFreshSkillPayloads` publishes later.
  Invalid selection must fail before selected publication; existing running trees stay untouched.

## Settled behavior needed by the builder

**Must, do not re-open.** These are execution constraints, not a second design argument:

| Surface | Exact contract |
| :--- | :--- |
| Modern syntax | User entry tables add only `subdir` and `skills = { dir, only, exclude }`. `skills` presence changes mode; without `dir` typed, with `dir` raw. Whole `subdir` alone preserves whole-pack semantics. Strings unchanged; array-of-tables accepted. |
| Scope | User TOML/fragments only. Workspace and legacy-origin new fields refuse; shipped allows typed selectors but not `subdir`/`dir`. Implicit conventional local pack is not scopeable. |
| Selector grammar | Go path.Match over one upstream name. Refuse empty, dot/dot-dot, slash/backslash/NUL, invalid syntax, non-string, duplicate within-list. |
| Selector evaluation | Omitted only means all; inclusion then exclusion. Explicit empty lists/table refuse. Every only/exclude must match a candidate. Inclusion whose every match is excluded refuses naming both selectors. Empty result refuses independently. |
| Refusal detail | Origin file and packs index, available names and actionable repair. Zero typed: use skills.dir; zero raw: list bounded deeper marker directories as suggested dir values. No partial/whole fallback. |
| Typed enumeration | Governed immediate child directories, no required marker, loose files ignored, later source wins ordinary names; audience retained. Destination rows source nothing. Manifest sourced aliases are candidates with unchanged own source/ref/license/audience. Plugin wrapper children excluded; literal plugin selector says import whole. |
| Raw enumeration | No manifest/conventions/plugin classification. Direct regular root marker means single directory, children are data. Otherwise immediate marker-bearing child directories only. Raw broadcasts. Markerless child literal is no-match. |
| Root names | Single raw name is dir basename; for dot use subdir basename if set, else handle basename. Entry namespace/bare then applies; repository namespace is unaffected by subdir. |
| Paths | Clean literal relative forward-slash paths. No leading slash/tilde, traversal, empty/dot segments, backslash/NUL/.git. Empty/dot subdir refuses (omit); dot raw dir allowed. Git ancestry real directories. Local roots must resolve inside source root; selected local content retains existing trust. |
| Identity | Tuple handle/subdir/dir, same tuple refuses including physical aliases. Whole plus scoped at same root and duplicate typed at same root refuse. Different raw dir or subdir allowed. All entries at one handle share authored ref, omitted and explicit distinct. |
| No activation | Effective selected rows only before closure: no needs/programs/hooks/surfaces/home/overlays/briefing/description/plugin. Other live closure reaching scoped shipped root refuses, naming both. |
| Naming | Existing bare/source/own namespace and override exclusions unchanged. No config per-skill alias or destination override. Reserved/core claims refuse; common sourced collision rules unchanged. Same-handle ownership shared, no inert landing claim. |
| Directory row | Resolver-produced typed ordinary child only; request carries root/governor/child evidence. Marker absent accepted, present regular fully validated, any other direct marker entry refuses. No synthesized marker. Internal requests/records explicit kind/state; no entry-authored knob. |
| Marker row | Raw and manifest sourced always direct regular marker-required; manifest sourced non-root/explicit-ref validation unchanged. Existing whole-pack sourced rows retain old compatibility. |
| License | Own conventional license, otherwise root license; conflict/unsafe/oversized refuses, none recorded. Direct config adds no license path. Per-row source/pin/directory/origin provenance. |
| Inspection | One printer for configured explain, check and add dry-run: candidates/selected/excluded/names/kind/marker state/carrier/source/license/origin and omitted-kind counts. Markerless directory is folder, delivered but not discoverable, excluded from skill counts. Explain shows every same-handle tuple as its own group, never first match; scoped groups never touch a staged tree. Author lint and footprint keep existing arguments and meanings and never show configured scope. Intentional omission is not warning. |
| Pure reads | No pack fetch on why/edit-validation/add dry-run/explain/check/attach. No write of any kind for a scoped entry on those surfaces, or for any entry in explain. Check keeps its existing whole-entry [readiness pass](toml-scoped-pack-imports.md#editing-and-inspect-surfaces) (throwaway staging and cleanup) unchanged; do not migrate its readers. Syntax always checked; cached/local candidate errors refuse; never-fetched or not-locally-readable selection is unverified with authored facts only, next launch resolves, not failure. No scoped footprint counts. |
| Advanced-pin failure | Lock-first/no rollback. New commit, failing selector and current candidates in error, prior commit only offline. Publish nothing invalid/partial, never use old selection; running tree unchanged. Edit selection or known-good ref remedies; unchanged fresh read repeats refusal. |
| Record | No original scoped root in discovery; selected rows outside discovery, one recorded effective projection. Row kind/state/governor persisted/versioned. Source-free replay verifies rows, marker state and eligibility; missing/duplicate/unknown/forged/downgrade refuse. No re-enumeration. |
| CLI/edit | Add handle or git mutually exclusive; exact flags subdir/skills-dir/only/exclude/bare/namespace/dry-run. Table only when options require it. Remove derives handle and deletes one tuple, ambiguity refuses with roots/index/origin. Nested generic edits/why support provenance. No writer emits empty/default/legacy keys. |
| Legacy | Reader/wire/withholding unchanged; migration refuses and retains filtered objects byte-for-byte, suggests manual skills.only/exclude rewrite, never claims equivalence. |

## Required representation and caller flow

**Must, do not re-open:** keep modern entry selection separate from legacy `Only`/`Exclude`.
A presence-bearing selection value distinguishes absence from an explicit invalid empty table.
Carry `subdir`, raw `dir`, selector presence/lists and config origin through readable lowering and
YOLO_PACKS. Keep ordinary `Identity` physical; add import-tuple identity and projection digest
separately. Exact Go container names are cheap; externally observable states are not.

**Must:** keep source/ref locking distinct from checkout root and projection. For Git, resolve
one handle pin, then read confined candidates at that immutable commit. Modern projected rows
refer to the pinned commit, not a floating ref. A selection change must reuse existing payload
identity, even when it changes the staged slug. Normal entries retain old slugs.

**Must:** projection output carries mode, candidate/selected/excluded names, omitted-kind counts,
normalized options, digest, origin and effective selected rows. A row carries upstream/registration
name, carrier, confined path, source/pin or staged location, audience and license/provenance.
Persist it once in the tree record. Create a shared record-driven loader consumed by both run
and entrypoint. Never reload a scoped original manifest to recover rows.

**Must:** pure validation uses pinned local content or returns explicitly unverified; no transport.
Syntax fails offline; candidate mismatch on available content fails before effects. Effectful
stage/payload publication consumes only a completely validated per-entry plan. Existing shared
payload concurrency/locks remain the writer authority. No selection-dependent shared object.

**Inspection inputs and evidence:** author lint takes a directory; footprint takes a shipped name,
a local author directory or no argument for the shipped set. Preserve their existing argument
recognition, output and exit behavior; add no configured-handle dispatch or shadowing and no new
explain/lint/footprint flag. These CLI shapes were writer-reported to the clarification author,
not source-read by that author. Publication corroborated the finite [CLI callers](../../internal/cli/pack.go)
and [footprint callee](../../internal/packload/footprint.go); this is source proof, not a runtime witness.

**Must:** for a scoped entry or several entries, each explain group header gives `packs[N]`,
origin file, `subdir`, `skills.dir` and whole/typed/raw mode in configured order. Whole groups use the existing explain body; a single
unscoped entry retains baseline output. Scoped groups branch on projection before staged-tree
access. Use [the three scoped inspection states](toml-scoped-pack-imports.md#editing-and-inspect-surfaces):
verified local/shipped/already-readable cached pin with its basis; unverified authored facts only;
readable invalid selection with the resolver's refusal text. No fetch, mirror, stamp, lock,
checkout materialization, payload publication or staged copy for scoped inspection. Source,
carrier, governed-source eligibility and license facts must come from the actual read-only plan.
Supporting-data omissions are printed only if that plan supplies them; otherwise “determined at
publication”. No candidates, rows, counts, license or omitted-kind counts in an unverified group.
An unverified group succeeds; a refused group uses the existing failure convention.

## Checkable execution units

- [ ] [Task 1](#task-1--modern-producer-wire-roots-and-tuple-identity): parser/wire/tuple gates and forward commit.
- [ ] [Task 2](#task-2--pure-projection-and-no-activation-caller-wiring): projection/closure gates and commit.
- [ ] [Task 3](#task-3--carrier-requests-common-registration-and-grouped-payloads): both row kinds and grouped carriers.
- [ ] [Task 4](#task-4--immutable-staged-record-reload-attach-and-restart-comparison): source-free record/downgrade controls.
- [ ] [Task 5](#task-5--refresh-control-failed-projection-and-pure-readers): advanced pin/no-publication/recovery.
- [ ] [Task 6](#task-6--table-aware-editing-remove-controls-and-shared-inspect): add/edit/remove/why and inspect.
- [ ] [Task 7](#task-7--implemented-documentation-permanent-controls-and-landing-evidence): documentation, census and final gate.
- [ ] Read-only committed-range review, findings resolved forward, clean committed completion.

## Serial tasks

Each task is one coherent forward commit with tests first. **Common report:** red receipt,
green command/result, changed paths, deviations, cleanup evidence and unstaged index. Per-task
package gates are not the whole landing gate. Source-reading tests register census dependencies.

### Task 1 — modern producer, wire, roots and tuple identity

Independent. **Settled, do not re-open:** modern user TOML only; strict selectors; separate legacy
wire; same-handle distinct raw roots allowed; explicit/omitted refs cannot mix at one handle.

**Tests first:** add the following caller test to a new config test file. It uses the existing
fixture helpers in the same package; no production-only seam is created for tests.

```go
func TestLoadPackEntriesReadsDirectUserScopedTOML(t *testing.T) {
    home := readablePackTestHome(t)
    root := filepath.Join(home, ".config", "yolo-jail")
    pack := filepath.Join(root, "packs", "corpus")
    if err := os.MkdirAll(pack, 0755); err != nil { t.Fatal(err) }
    write(t, filepath.Join(root, paths.UserConfigTOMLName),
        `packs = [{ pack = "local/corpus", skills = { dir = "skills/core-tools", only = ["skill-a", "skill-b"] } }]`+"\n")
    entries, problems, err := LoadPackEntries()
    entries = withoutDefaultSelected(entries)
    if err != nil || len(problems) != 0 || len(entries) != 1 {
        t.Fatalf("direct scoped TOML: entries=%+v problems=%v err=%v", entries, problems, err)
    }
    if len(entries[0].Only) != 0 || len(entries[0].Exclude) != 0 {
        t.Fatalf("modern selection leaked into legacy fields: %+v", entries[0])
    }
    wire, err := MarshalPacks(entries)
    if err != nil { t.Fatal(err) }
    roundtrip, err := UnmarshalPacks(wire)
    if err != nil { t.Fatal(err) }
    again, err := MarshalPacks(roundtrip)
    if err != nil || again != wire { t.Fatalf("wire drift: %s %s %v", wire, again, err) }
}
```

**Predicted red:** unknown `skills` entry key, not a compiler error.

```bash
go test -short ./internal/config -run '^TestLoadPackEntriesReadsDirectUserScopedTOML$' -count=1
```

**Change:** modern schema/parser/lowering/entry representation and offline validation only.
Add table-driven syntax cases from A-12/A-13/A-14; fragment provenance, array-of-tables, shipped
selectors, workspace and legacy-origin refusal. Pin no-new-keys and ordinary naming/slug parity.
Test two raw directories and two subdirs sharing a handle, physical aliases and mismatched refs.
**Gates:** same red command green, then `go test -short ./internal/config ./internal/packsrc`.
**Fence (tier 3 procedural):** no legacy parser key-list or legacy filter/withholding changes.
**Commit:** `feat(config): read direct scoped TOML pack entries`.

### Task 2 — pure projection and no-activation caller wiring

After 1. **Settled:** typed and raw candidates; strict no-match/exclusion; effective skill-only
declaration before closure; source-specific confined path rules and plugin classification only.

**Tests first:** same-package resolver caller test, using Task 1's supported TOML. Add imports
already used by existing config tests (`os`, `filepath`, `testing`, `paths`).

```go
func TestResolvePackRawScopeDoesNotLoadPoisonManifest(t *testing.T) {
    home := readablePackTestHome(t)
    root := filepath.Join(home, ".config", "yolo-jail")
    pack := filepath.Join(root, "packs", "corpus")
    if err := os.MkdirAll(filepath.Join(pack, "skills/core-tools/skill-a"), 0755); err != nil { t.Fatal(err) }
    write(t, filepath.Join(pack, "skills/core-tools/skill-a/SKILL.md"), "selected\n")
    write(t, filepath.Join(pack, "pack.toml"), "this is not valid TOML [\n")
    write(t, filepath.Join(root, paths.UserConfigTOMLName),
        `packs = [{ pack = "local/corpus", skills = { dir = "skills/core-tools", only = ["skill-a"] } }]`+"\n")
    entries, problems, err := LoadPackEntries()
    entries = withoutDefaultSelected(entries)
    if err != nil || len(problems) != 0 || len(entries) != 1 { t.Fatalf("producer: %v %v %+v", err, problems, entries) }
    resolved, err := ResolvePack(entries[0], ResolvePackSpec{ReadOnlyStore: true})
    if err != nil || resolved.Pack == nil || len(resolved.Problems) != 0 {
        t.Fatalf("raw scope interpreted manifest: %v %+v", err, resolved)
    }
    if len(resolved.Pack.InstallBins()) != 0 || len(resolved.Pack.Plugins()) != 0 ||
        len(resolved.Pack.Decl.DeclaredNeeds()) != 0 || resolved.Pack.SourcedSkillsWithheld != "" {
        t.Fatalf("scope widened or withheld: %+v", resolved.Pack)
    }
}
```

**Predicted red after 1:** full pack resolver reports invalid original manifest.

```bash
go test -short ./internal/config -run '^TestResolvePackRawScopeDoesNotLoadPoisonManifest$' -count=1
```

**Change:** common candidate enumerator; pure selection plan; branch scoped intake before whole-pack
load/stage/check. Typed declaration intake retains audiences/sourced aliases, then constructs an
inert effective declaration with safe metadata binding. Raw never loads manifest/plugin classifier.
Wire the embedded-FS declaration fast path. Keep unverified state distinct from invalid selection.
**Expand tests:** A-01/A-04–A-08/A-11–A-15, every selector failure, raw root name and nested suggestions,
local root escape vs trusted selected local contents, typed later-source winner, plugin literal.
Drive `SelectedPackDeclarations` with poison needs/home/program metadata; no callee-only coverage.
**Gates:** exact red green, `go test -short ./internal/config ./internal/packload ./internal/hostskills`.
**Fence (tier 3):** no cache publication or CLI editing yet.
**Commit:** `feat(packs): project scoped skill contributions before closure`.

### Task 3 — carrier requests, common registration and grouped payloads

After 2. **Settled:** Git repository/commit identity, raw root marker;
local/shipped staged selected directories; selected manifest sourced rows unaffected by legacy
withholding; no marker invented for markerless ordinary typed rows.

**Tests first:** add this real TOML→resolver→common composer test in the CLI package. It uses
existing fixture types, not a new markerless production test seam. Required imports: bytes,
os, filepath, testing, config, hostskills, packdecl, packload, testconfig.

```go
func TestDirectScopedTypedFolderDoesNotSynthesizeMarker(t *testing.T) {
    project := testconfig.Workspace(t, "")
    user := testconfig.User(t,
        `packs = [{ pack = "local/corpus", skills = { only = ["chosen"] } }]`+"\n")
    t.Setenv("HOME", user.Root)
    source := filepath.Join(filepath.Dir(user.Path), "packs", "corpus")
    for _, name := range []string{"chosen", "hidden"} {
        dir := filepath.Join(source, "skills", name)
        if err := os.MkdirAll(dir, 0755); err != nil { t.Fatal(err) }
        if err := os.WriteFile(filepath.Join(dir, "notes.md"), []byte(name), 0644); err != nil { t.Fatal(err) }
    }
    entries, problems, err := config.LoadPackEntries()
    if err != nil || len(problems) != 0 { t.Fatalf("producer: %v %v", err, problems) }
    var selected *packload.Pack
    for _, entry := range entries {
        if entry.Name != "local/corpus" { continue }
        resolved, err := config.ResolvePackForProcess(entry, config.ResolvePackSpec{})
        if err != nil || len(resolved.Problems) != 0 { t.Fatalf("resolve: %v %+v", err, resolved) }
        selected = resolved.Pack
    }
    if selected == nil { t.Fatal("producer lost scoped entry") }
    agent := &packload.Pack{Name: "reader", Root: project.Root, Decl: &packdecl.Manifest{
        Contributes: []packdecl.Contribution{{Kind: packdecl.KindSkills, Agent: "fixture", Into: ".fixture/skills"}},
    }}
    selected = selected.ResolveDestinations([]*packload.Pack{selected, agent}).Pack
    home := t.TempDir()
    dests := hostskills.ComposeHostSkillsFor([]*packload.Pack{selected, agent}, home,
        func(string, packdecl.Kind) bool { return true })
    req := hostskills.ComposeRequest{Composed: &hostskills.Manifest{},
        ArchiveRoot: hostskills.ArchiveRoot(t.TempDir()), Stamp: "fixture", PackSetComplete: true,
        Configured: map[string]bool{"local/corpus": true, "reader": true}}
    if _, err := hostskills.RenderHostSkills(dests, req, false); err != nil { t.Fatal(err) }
    dest := filepath.Join(home, ".fixture/skills")
    items, err := os.ReadDir(dest)
    if err != nil || len(items) != 1 || items[0].Name() != "corpus-chosen" {
        t.Fatalf("scoped directories: %v %v", items, err)
    }
    folder := filepath.Join(dest, "corpus-chosen")
    if _, err := os.Lstat(filepath.Join(folder, "SKILL.md")); !os.IsNotExist(err) {
        t.Fatalf("marker synthesized: %v", err)
    }
    got, err := os.ReadFile(filepath.Join(folder, "notes.md"))
    if err != nil || !bytes.Equal(got, []byte("chosen")) { t.Fatalf("support: %q %v", got, err) }
}
```

```bash
go test -short ./internal/cli -run '^TestDirectScopedTypedFolderDoesNotSynthesizeMarker$' -count=1
```

**Predicted red after 2:** selected staged directory registration is absent, or existing marker-required
carrier refuses. If Task 2 already implemented this carrier prematurely, stop and report the unexpected
green rather than weakening the assertion. Repeat through a fake named Git source and direct Git
URL rewritten by a fixture Git executable, marker-bearing sibling, raw root, author-directory
lint controls and configured inspection.
The direct fake URL has owner/repository shape; do not use a deep absolute file URL that the
modern direct parser correctly rejects. Named Git sources may use local file transport.

**Change:** internal per-row kind (`marker`/`directory`) and marker state (`present`/`absent`),
with governed source and child eligibility evidence for typed directory requests. Only scoped
resolver produces directory kind; no user/manifest/CLI/entry-wire spelling. Raw/manifest rows
remain marker kind. Validate direct marker entry (symlink/directory refuses, not absence), structural
eligibility, selected-link confinement and ordinary bounds. Missing/unknown/ineligible kinds refuse;
whole-pack sourced requests are marker kind by definition. Safe root joining must preserve raw
root support without relaxing manifest non-root rules. Integrate requests
into the one `ResolveSelectedSkillPayloads` group. Drop unselected manifest rows before store work.
For local copied carriers, inspect the original direct marker entry before trusted local-link copying:
copying must not turn a prohibited marker symlink into an apparently valid regular marker. Ordinary
selected local supporting links retain the established local-pack trust; staged regular content
is then validated. Git source links retain selected-directory confinement.
Registration distinguishes absent marker from empty marker, keeps private name rewriting when
present and license rules. Staged local/shipped carriers use only selected directories with
process/per-launch lifetime; audiences and claims route through common composition.
**Expand tests:** A-03/A-05/A-09/A-10/A-19/A-21/A-23–A-25/A-28/A-29; unsafe selected vs omitted
support, bounds, plugin inertness, reserved names, source collisions, duplicate names, empty
directory and changed marker. No synthesized marker, including empty folders and differently cased
ordinary marker filenames. One shared payload for mixed kinds, no row-specific stamp/cache.
**Gates:** `go test -short ./internal/packsrc ./internal/packload ./internal/hostskills ./internal/config`.
**Fence (tier 3):** manifest sourced root/marker validation unchanged; no generic skip-validation flag.
**Commit:** `feat(skills): register scoped imports through existing payload carriers`.

### Task 4 — immutable staged record, reload, attach and restart comparison

After 3. **Settled:** no original source root staged; rows outside discovery; self-contained
running record, missing/duplicate/unrecorded row rejection; selection changes require restart.

**Tests first:** this exact run-package caller test starts the source-free record regression.
Use imports from the existing staged fixture plus `testconfig`.

```go
func TestDirectScopedPackRecordedAttachIgnoresDeletedSource(t *testing.T) {
    user := testconfig.User(t,
        `packs = [{ pack = "local/corpus", skills = { only = ["*"] } }]`+"\n")
    t.Setenv("HOME", user.Root)
    t.Setenv("YOLO_VERSION", "")
    source := filepath.Join(filepath.Dir(user.Path), "packs", "corpus")
    for name, data := range map[string]string{"with-marker/SKILL.md": "chosen\n", "no-marker/notes.md": "folder\n"} {
        file := filepath.Join(source, "skills", name)
        if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil { t.Fatal(err) }
        if err := os.WriteFile(file, []byte(data), 0644); err != nil { t.Fatal(err) }
    }
    var report bytes.Buffer
    o := &Options{Workspace: t.TempDir(), Stderr: &report, Stdout: &report}
    const cname = "yolo-test-direct-scoped-record"
    root, packs, briefings, err := o.stagePacks(cname)
    if err != nil { t.Fatal(err) }
    t.Cleanup(func() { discardPackTree(cname, root) })
    staged := stagedPacks{root: root, packs: packs, briefings: briefings}
    if !o.prepareFreshSkillPayloads(cname, "podman", staged) { t.Fatal(report.String()) }
    if err := os.RemoveAll(source); err != nil { t.Fatal(err) }
    if err := packsrc.RemoveSealedSkillTree(user.Root, paths.PacksDir()); err != nil { t.Fatal(err) }
    host, err := loadPackTree(root)
    if err != nil { t.Fatal(err) }
    guest, err := entrypoint.LoadJailPacks(&entrypoint.Env{
        Home: user.Root, Workspace: o.Workspace, Vars: map[string]string{"YOLO_PACK_ROOT": root},
    })
    if err != nil { t.Fatal(err) }
    for _, loaded := range [][]*packload.Pack{host, guest} {
        found := false
        for _, p := range loaded {
            if p.Name != "local/corpus" { continue }
            found = true
            if len(p.SourcedSkillRows()) != 2 || len(p.Plugins()) != 0 || len(p.InstallBins()) != 0 {
                t.Fatalf("record lost projection: %+v", p)
            }
        }
        if !found { t.Fatal("record lost scoped entry") }
    }
}
```

`RemoveSealedSkillTree` already accepts an owned base/root and no-ops when absent; preserve
its exact caller contract rather than ordinary `RemoveAll` over sealed Git payloads. Expand this
fixture through common composition and `runningJailPackView`, plus fake Git carrier. Tamper one
row at a time (missing/duplicate/unrecorded/path escape/wrong carrier), require refusal and no
rediscovery. Use two same-handle imports so name-only comparison cannot hide a row.

```bash
go test -short ./internal/cli/run -run '^TestDirectScopedPackRecordedAttachIgnoresDeletedSource$' -count=1
go test -short ./internal/entrypoint -run '^TestLoadJailPacksUsesRecordedScopedProjection$' -count=1
```

**Predicted red:** existing loaders/diffs load original pack paths or cannot hydrate projected rows.
**Change:** shared record-driven loader; effective row record (kind/state/governed source), digest
including kind/state, staged selected locations; reserved carrier-root skipping in both no-record
fallbacks. New versioned scoped record shape must be rejected even by the previous decoder—an
extra ignored `schema` field alone is not enough. Advice: use a versioned `packs` envelope for
scoped records whose JSON shape the old array decoder cannot accept, while new readers accept
old whole-pack arrays. Prove that with the exact baseline decoder struct as a fixture, not merely
a new decoder configured with an older max version. Missing/unknown version, forged/missing/unknown
kind, incompatible projection kind or marker-state tampering refuses; no default/conversion. Hydrate all rows against record,
not host declarations. Compare import tuple and projection digest, never name-only maps or
selection-dependent shared cache. Remap reading paths for podman/Apple/macos-user using existing
payload target machinery; preserve adoption of live tree and discard fresh comparison tree.
**Expand tests:** A-02/A-17/A-18/A-19/A-26/A-27, host fixture parity, two local imports and shared Git payload,
changed only/exclude/mode/audience/naming, old record and old normal entry compatibility.
**Gates:** exact reds green; `go test -short ./internal/cli/run ./internal/entrypoint ./internal/packload`.
**Fence (tier 3):** no running tree restage, no source/cache access during adopted-row hydration.
**Commit:** `feat(packs): persist scoped projections for source-free attach`.

### Task 5 — refresh control, failed projection and pure readers

After 4. **Settled:** source refresh remains lock-first, per handle, unchanged repository/ref
stamps. Invalid advanced selection publishes nothing for that entry; running trees unchanged.
Offline unavailable source is unverified, not failure. No stale subset fallback.

**Tests first:** the following production refresh→stage test uses a local Git **named** source,
which permits a filesystem repository without direct owner/repository URL restrictions. A new
branch has no fresh stamp, so the real refresh sees its pin advance without waiting an hour.
Imports: bytes, fmt, os, os/exec, filepath, strings, testing, packsrc, paths, testconfig, testsupport.

```go
func TestDirectScopedRefreshFailureKeepsAdvancedPinWithoutPublication(t *testing.T) {
    repo := t.TempDir()
    runGit := func(args ...string) string {
        t.Helper()
        cmd := exec.Command("git", args...)
        cmd.Dir = repo
        cmd.Env = append(testsupport.HermeticGitEnv(packsrc.CleanGitEnv(os.Environ())),
            "GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid",
            "GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid")
        out, err := cmd.CombinedOutput()
        if err != nil { t.Fatalf("git: %v %s", err, out) }
        return strings.TrimSpace(string(out))
    }
    dir := filepath.Join(repo, "corpus/skills/skill-a")
    if err := os.MkdirAll(dir, 0755); err != nil { t.Fatal(err) }
    if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("first\n"), 0644); err != nil { t.Fatal(err) }
    runGit("init", "-b", "main")
    runGit("add", "--all")
    runGit("-c", "commit.gpgsign=false", "commit", "-m", "first")
    first := runGit("rev-parse", "HEAD")
    cfg := func(ref, name string) string {
        return fmt.Sprintf("packs = [{ pack = \"demo/corpus\", skills = { only = [%q] } }]\n[pack_sources.demo]\ngit = %q\nref = %q\n", name, "file://"+filepath.ToSlash(repo), ref)
    }
    user := testconfig.User(t, cfg("main", "skill-a"))
    t.Setenv("HOME", user.Root)
    t.Setenv("YOLO_VERSION", "")
    t.Setenv("YOLO_PACK_ROOT", "")
    var report bytes.Buffer
    refresh := func() { RefreshConfiguredPacks(func(s string) { fmt.Fprintln(&report, s) }, func(s string) { fmt.Fprintln(&report, s) }) }
    refresh()
    runGit("checkout", "-b", "next")
    runGit("mv", "corpus/skills/skill-a", "corpus/skills/skill-b")
    runGit("-c", "commit.gpgsign=false", "commit", "-m", "rename")
    second := runGit("rev-parse", "HEAD")
    if err := os.WriteFile(user.Path, []byte(cfg("next", "skill-a")), 0644); err != nil { t.Fatal(err) }
    refresh()
    lock, err := packsrc.LoadLock(packsrc.LockPath(paths.UserConfigPath()))
    if err != nil { t.Fatal(err) }
    pin, ok := lock.Get("demo/corpus")
    if !ok || pin.Commit != second { t.Fatalf("pin did not advance: %+v", pin) }
    o := &Options{Workspace: t.TempDir(), Stderr: &report, Stdout: &report}
    root, _, _, err := o.stagePacks("yolo-test-scoped-stale")
    if err == nil || root != "" { t.Fatalf("invalid scope published: root=%s err=%v", root, err) }
    for _, want := range []string{second, "skill-a", "skill-b", "ref"} {
        if !strings.Contains(err.Error(), want) { t.Fatalf("missing recovery detail %q: %v", want, err) }
    }
    for _, recovery := range []struct{ ref, name string }{{"next", "skill-b"}, {first, "skill-a"}} {
        if err := os.WriteFile(user.Path, []byte(cfg(recovery.ref, recovery.name)), 0644); err != nil { t.Fatal(err) }
        refresh()
        tree, _, _, err := o.stagePacks("yolo-test-scoped-recovery")
        if err != nil { t.Fatalf("recovery %+v: %v", recovery, err) }
        discardPackTree("yolo-test-scoped-recovery", tree)
    }
}
```

Expand with payload publication snapshots, repeated unchanged refusal, existing live tree digest
and attach before/after refusal, and a **same-branch** advance using Store.Refresh's injected
Now/Force seam instead of sleeps. Pin the `Run` refresh callsite via the existing dispatch callback
fixture as well: removing `o.refreshPacks()` must fail the regression. Two raw directories sharing
one handle must refresh once and bind the same commit. No script performs network transport.

```bash
go test -short ./internal/cli/run -run '^TestDirectScopedRefreshFailureKeepsAdvancedPinWithoutPublication$' -count=1
```

**Predicted red:** current refresh cannot represent modern selection failure/publishing barrier.
**Change:** base-source/ref refresh dedup, resolved-checkout-root split, selection diagnostic before
fresh publication. Keep shared store locking/concurrency and default-head pinning unchanged.
**Pure-reader controls:** fake Git executable records local object reads and refuses transport;
run config edit validation, why, explain/check and attach. Run author lint and footprint only as
unchanged-output controls beside a scoped user config, not as scoped readers. Assert no fetch,
clone, refresh stamp, mirror creation or lock write. Missing content reports unverified; cached
stale selectors refuse consistently. Exercise host apply observe versus controlled fixture writer.
**Expand:** A-30/A-31 repeat refusal without config change, selector edit recovery and explicit known-good
full commit recovery; new commit/selector/candidates in message, previous pin only without transport.
**Gates:** exact red green; `go test -short ./internal/cli/run ./internal/cli/check ./internal/config`.
**Fence (tier 3):** no rollback or lock-transaction redesign, no new credentials/transport route.
**Commit:** `fix(packs): refuse invalid refreshed scopes without partial publication`.

### Task 6 — table-aware editing, remove controls and shared inspect

After 5. **Settled:** add writes tables only for table options; handle/URL exclusivity; remove
exactly one tuple, refuse ambiguity; generic paths/why retain origin; configured explain, check
and add dry-run share the printer; explain groups every same-handle tuple.

**Tests first:** exact caller test in the CLI package using existing fake-user helpers:

```go
func TestPackAddScopedTOMLDryRunIsPure(t *testing.T) {
    project := testconfig.Workspace(t, "")
    user := testconfig.User(t, "packs = []\n")
    t.Setenv("HOME", user.Root)
    t.Setenv("YOLO_VERSION", "")
    dir := filepath.Join(filepath.Dir(user.Path), "packs", "corpus", "skills/core-tools/skill-a")
    if err := os.MkdirAll(dir, 0755); err != nil { t.Fatal(err) }
    if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("chosen\n"), 0644); err != nil { t.Fatal(err) }
    before, err := os.ReadFile(user.Path)
    if err != nil { t.Fatal(err) }
    rc, out, errw := runPackCapture(t, project.Root, []string{
        "add", "local/corpus", "--skills-dir", "skills/core-tools", "--only", "skill-a", "--dry-run",
    })
    if rc != 0 || errw != "" { t.Fatalf("rc=%d out=%s err=%s", rc, out, errw) }
    after, err := os.ReadFile(user.Path)
    if err != nil || !bytes.Equal(before, after) { t.Fatalf("dry-run wrote: %q %v", after, err) }
    for _, want := range []string{"skill-a", "skills/core-tools", "Dry run"} {
        if !strings.Contains(out, want) { t.Fatalf("missing %q: %s", want, out) }
    }
}
```

**Predicted red:** unknown `--skills-dir`.

```bash
go test -short ./internal/cli -run '^TestPackAddScopedTOMLDryRunIsPure$' -count=1
```

**Change:** `packEditArgs` typed options; `userPackItems` derives identities from string/table values
and effective source definitions; authoring target origins retained. ApplyTargeted writes complete
TOML tables and nested edits; help lists exact flags. Remove ambiguity lists tuple paths and needs
explicit roots; never remove several entries or the wrong fragment. Shared projection printer
shows rows, omitted counts and unverified state without warning for intentional omission.
**Inspection caller controls:** through the real command entry, cover one scoped group with no
staged-tree dereference; every tuple of one legal same-handle multi-entry fixture; single unscoped
baseline output; never-fetched explain/check authored facts only; cached stale-selector refusal;
transport-refusing Git and store snapshots proving no scoped-inspection or explain writes, with
check's whole-entry [readiness pass](toml-scoped-pack-imports.md#editing-and-inspect-surfaces) staging measured as its own baseline in mixed fixtures; author lint/footprint
unchanged-output controls, including a handle-spelled argument. The multi-entry fixture keeps
existing distinct-root, authored-ref, physical identity and whole/scoped conflict rules. No test
may relax identity proof to create it. Preserve the API-mismatch lint/footprint red receipts as
such, not as historical behavior bugs.
**Deletion controls:** permanent tests delete one of two raw entries, preserve the other/comment
bytes; ambiguous removal writes nothing; wrong tuple writes nothing; fragment removal uses its
actual author; selector removal leaving an empty table/list refuses transactionally. Round-trip
add→load→projection→why→edit→remove and direct Git flags. In-jail writes remain refused with host-run.
**Migration:** legacy filtered file retained byte-for-byte; update advice, not old semantics.
**Gates:** exact red green; `go test -short ./internal/cli ./internal/config ./internal/configmigrate`.
**Fence (tier 3):** no legacy JSONC writer feature; no hand-written config file mutation.
**Commit:** `feat(cli): author and inspect scoped TOML pack imports`.

### Task 7 — implemented documentation, permanent controls and landing evidence

After 6. **Settled:** only verified code warrants implemented docs/stage; no agent installs or
provider calls. Acceptance rows A-01–A-33 are all required, not optional pragmatic scope.

**Tests first:** exact integration caller regression below uses the harness's isolated home/image
links, then replaces only its owned legacy config with an empty modern main file and a user fragment.
No real agent pack is selected. Imports: os, filepath, strings, testing.

```go
func TestDirectUserScopedPackImportsAcrossWorkspaces(t *testing.T) {
    requireJail(t)
    packHome(t, `{}`)
    configDir := filepath.Join(os.Getenv("HOME"), ".config", "yolo-jail")
    if err := os.Remove(filepath.Join(configDir, "config.jsonc")); err != nil { t.Fatal(err) }
    if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte("# fixture\n"), 0644); err != nil { t.Fatal(err) }
    corpus := filepath.Join(configDir, "packs", "corpus")
    for _, name := range []string{"skill-a", "skill-b", "hidden"} {
        dir := filepath.Join(corpus, "skills/core-tools", name)
        if err := os.MkdirAll(dir, 0755); err != nil { t.Fatal(err) }
        if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(name+"\n"), 0644); err != nil { t.Fatal(err) }
    }
    // Raw mode must never interpret this original manifest.
    if err := os.WriteFile(filepath.Join(corpus, "pack.toml"), []byte("broken TOML [\n"), 0644); err != nil { t.Fatal(err) }
    reader := filepath.Join(configDir, "packs", "reader")
    if err := os.MkdirAll(reader, 0755); err != nil { t.Fatal(err) }
    if err := os.WriteFile(filepath.Join(reader, "pack.toml"),
        []byte("[[skills]]\nagent = \"fixture\"\ninto = \".fixture/skills\"\n"), 0644); err != nil { t.Fatal(err) }
    fragments := filepath.Join(configDir, "config.d")
    if err := os.MkdirAll(fragments, 0755); err != nil { t.Fatal(err) }
    body := `agent_updates = false
packs = ["local/reader", { pack = "local/corpus", skills = { dir = "skills/core-tools", only = ["skill-a", "skill-b"] } }]
`
    if err := os.WriteFile(filepath.Join(fragments, "scoped.toml"), []byte(body), 0644); err != nil { t.Fatal(err) }
    script := `set -eu
cat "$HOME/.fixture/skills/corpus-skill-a/SKILL.md"
cat "$HOME/.fixture/skills/corpus-skill-b/SKILL.md"
test ! -e "$HOME/.fixture/skills/corpus-hidden"
printf 'SCOPED-ONLY\n'
`
    for range 2 {
        workspace := writeProject(t, `{}`)
        r := runYolo(t, workspace, script, withEnv("YOLO_NO_PROGRAM_READINESS=1", "YOLO_NO_AUTO_CAPTURE=1",
            "YOLO_NO_AUTO_IMAGE_REAP=1", "YOLO_NO_UPDATE_CHECK=1", "YOLO_AGENT_UPDATES=false"))
        if r.rc != 0 || !strings.Contains(r.stdout, "SCOPED-ONLY") {
            t.Fatalf("direct scoped workspace: rc=%d stdout=%s stderr=%s", r.rc, r.stdout, r.stderr)
        }
    }
}
```

Expand raw test with explicit poison program/
hook/need/plugin fixtures; typed mixed rows and fake Git sourced rows are separate same-suite cases.
No scripted marker can call an agent or install endpoint. Assertions must fail when the production
TOML→resolver→staging→entrypoint→composer callsite is removed, not only when a helper is changed.

```bash
go test -short ./integration -run '^TestDirectUserScopedPackImportsAcrossWorkspaces$' -count=1
go test -count=1 -timeout 0 ./integration -run '^TestDirectUserScopedPackImportsAcrossWorkspaces$'
```

**Change:** config-ref, pack help/reference, user config and setup matrix, applicable agent help.
Reconcile the sourced reference's non-root/marker claims so manifest sourced rows remain accurately
distinct from new direct rows. Document sibling reachability from actual receipts. Update accepted
design/plan built evidence only after the full feature passes; graduate to an implemented system
reference through owning skill, then remove planning files/closed roadmap entry coherently.
**Source-reading census:** run `python3 scripts/completion-census.py`, inspect the generated diff,
commit its output with the tests that read outside their package. No silent census omission.
**Commit:** `docs(packs): document verified direct TOML scoped imports`.

## Applicable gates and independent review

The landing owner runs the whole applicable gate **once on the final combined tree**, not each
agent or task. Keep any failed receipts; fix forward, then rerun the failing applicable gate.

```bash
just format
python3 scripts/completion-census.py
just check-ci
go test -count=1 -timeout 0 ./integration
just build-dev-bundle
```

This changes the launch path, so full integration is applicable once at landing. Relevant tests
may run earlier, not duplicate full-suite claims. Gate tools must remain offline for docs checks:

```bash
UV_OFFLINE=1 scripts/vantage-check.sh check --strict docs/design/toml-scoped-pack-imports.md docs/design/toml-scoped-pack-imports-plan.md docs/research/toml-scoped-pack-imports.md docs/plans/roadmap.md
UV_OFFLINE=1 scripts/vantage-check.sh index --format json
```

**Nested emitted-launcher probe:** invoke the absolute launcher emitted by `just build-dev-bundle`
from an owned durable throwaway workspace, never bare installed yolo and never install/deploy.
Use controlled fixture HOME/config only in that child; leave controller HOME/TMPDIR/GOENV/caches
unchanged. Set `YOLO_NO_PROGRAM_READINESS=1`, `YOLO_NO_AUTO_CAPTURE=1`,
`YOLO_NO_AUTO_IMAGE_REAP=1`, `YOLO_AGENT_UPDATES=false`, and `YOLO_NO_UPDATE_CHECK=1`.
The fixture user TOML also sets `agent_updates = false`; the wire is a JSON boolean, not `0`. Capture effective fixture config and assert selected only rows; no
capture/install/provider markers. Remove only fixture-owned trees/containers, retain receipts.
Nested evidence does not establish native Mac reachability or rootless-only behavior.

**Review gate:** a read-only high-capability reviewer checks the committed range against the
behavioral design, acceptance/test map and retained receipts; no index/source mutation. Required
focus: no activation/discovery fallback, modern/legacy separation, pure readers, same-handle
lock/labels/diff behavior, source-free tamper refusal, no accidental installs, and removal controls.
Review findings must be resolved in forward commits before acceptance.

**Completion:** conventional commits; clean status and empty staged index; `just done` after the
final commit. Report full baseline/final commits, gate receipts, changed paths and residual native
witnesses. Never claim all setups verified from Linux fixtures.

## Coverage assignment and permanent deletion controls

| Cases | Primary evidence boundary |
| :--- | :--- |
| A-01/A-02/A-07/A-08/A-15 | Real user producer, common resolver/closure and two-workspace integration. |
| A-03/A-05/A-06/A-09/A-10/A-13/A-21 | Payload/common registration, root and marker variant, legacy beside modern. |
| A-04/A-11/A-12/A-14 | Modern parser plus real resolver, scope/path/identity refusals. |
| A-16/A-20/A-22 | CLI/edit/why/read-only transport spies, removal and migration byte retention. |
| A-17 | Mixed row kinds share payload, tuple-aware restart diff, no selection-only fetch. |
| A-18/A-19/A-26/A-27 | Record→run/jail loader + composer after source/cache deletion; forged/downgraded kind/state refusal, host parity. |
| A-23–A-25/A-28/A-29 | Typed marker/markerless parity, marker-required other modes, nonregular marker refusal, inert data and unchanged legacy. |
| A-30/A-31 | Real refresh→resolution: advanced lock/no publication, unchanged live tree, repeat refusal and both tested recoveries. |
| A-32/A-33 | Real explain/check entry: every legal same-handle tuple, local/cached/unverified/refused states; no scoped-inspection or explain writes, no scoped staged-tree access; whole-entry check [readiness-pass](toml-scoped-pack-imports.md#editing-and-inspect-surfaces) staging matches its own baseline in mixed fixtures; single unscoped output baseline; unchanged author lint/footprint input/output controls. |

Permanent controls must go red if production ignores selection, clears only modern withholding,
retains original `needs`, re-enumerates on reload, skips the common registration caller, keys diff
by handle only, invokes transport on pure readers, removes all matching table entries, or starts
a readiness/capture install in the synthetic probe. Use observed output/state, never comments
as the assertion. A test timeout is a generous hang guard; no sleep or elapsed pass budget.

## Don't

- Do not teach the legacy reader new keys, populate old filters from TOML or change legacy
  withholding. Do not migrate path-glob semantics by guesswork.
- Do not use a wrapper pack/workspace-only import as the implementation. Do not omit typed
  sourced rows, roots, table removal, source-free reload or inspect surfaces to shrink the task.
- Do not activate payloads/plugins, stage original scoped manifests, mutate shared checkouts or
  invent selection caches/stamps. Do not run real programs, agent sign-ins or provider calls.
- Do not manufacture marker content or quietly require markers in typed ordinary directories.
  Use the accepted explicit kind/state eligibility and hydration contract, never an unchecked flag.
