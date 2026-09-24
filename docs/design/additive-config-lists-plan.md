# Plan: additive config lists (`config-list`)

**Design:** [`additive-config-lists.md`](additive-config-lists.md) · **Status:** ready ·
Written against `495d92a8`, 2026-09-24, with an in-flight working tree. `internal/entrypoint/packrender_test_support.go` is the only mapped file that already holds uncommitted edits.
Precedence: the design wins on behavior, the tree wins on fact, and this file is advice that is the first thing to be wrong.
Anchors are `file:line` at that commit. **M** = measured in the tree, **I** = inferred.

## Decisions this plan makes (the design left the spelling to the build)

| Question | Decision | Why |
| :--- | :--- | :--- |
| Kind | New kind `config-list`: `{"kind":"config-list","surface":"agent/name","path":"/packages","add":[…]}` | A separate operation, per the design; `config-overlay` stays a pure merge patch |
| Path spelling | **RFC 6901 JSON Pointer**, which must be non-empty and start with `/`. `""` (the root) is refused | Real keys contain dots (model ids, server names), so a dotted spelling is ambiguous; the tree already uses RFC 6901 escapes (`internal/cli/run/briefingdest.go:82`, **M**) |
| `add` | Required. A JSON array, which may be empty (a no-op). A `null` element is refused | TOML cannot encode null. A missing `add` is an authoring error, not a no-op |
| Refused fields | `config` on `config-list`; `path` and `add` on every other kind. `profile` is already refused (`contributes.go:2178`, **M**) | The house "does not take" pattern, placed ahead of the kind switch |
| Combine rule | Reuse `CombineOverlay`, with its own `Claims` text | Ordered after the owner, never a collision; no new branch in `packload.Collisions` |
| Element identity | Canonical-JSON equality of `jsonx.Plain`-normalized values | pack.json decodes `1` as float64, while a TOML file's `1` (and jsonx's `jsonInt`) does not; `reflect.DeepEqual` on raw values reports them different (see `intactDefaults`, `prism.go:974`) |
| No owner | Inert and reported, exactly like an ownerless overlay (R2) | Design: "reported just like an ownerless `config-overlay`" |
| Keyless target (`lines`/`raw`) | A `Problems` entry, fatal like a malformed overlay | A keyless surface has no path |
| `unrendered` target | Inert, with a one-line warning | Nothing is written, so there is nothing to refuse |

### Fold order (the ordering rule is [OQ-AL2](./additive-config-lists.md#decision-ledger)'s; the placement is this plan's)

`defaults < host < workspace < config-overlay… ` → **list contributions** (loaded-pack order, then declaration order; append first occurrences that are not already present; existing entries keep their order, and duplicates already in the base are kept) → capture overlay (the merge patch) → **per-entry list capture** (remove, then append adds that are absent) → `computed` → `managed`. When the value at the path is missing it is treated as `[]`, and missing parents are created as `{}`. A non-object parent, or a non-array value at the path, from any layer below capture refuses the surface's render. The refusal names the surface, the path and the pack (rule 3). For `stateful` and `computed` this is a `Compose` error, which is fatal to the boot under A12. For `rmw` it is `refuseRMW`, which warns and leaves the file untouched, matching `packsurfaces.go:446`.

### Per-entry capture: `stateful`

- **The sidecar is new and has a single job:** `<agent>-<name>.list-capture.json`, in `Target.SidecarDir()` beside the overlay, written through `writeSidecar` (host mode 0600). Format: `{"<pointer>": {"add": [...], "remove": [...]}}`, with sorted keys and both arrays always present. The overlay itself is not changed. Every overlay reader (diff, promote, `overlayEntryCount`, the host prune) would otherwise read a reserved marker as a user key.
- **The list-path set is sticky and self-describing:** it is the live contribution paths plus every key already in the sidecar. Each render writes an entry, empty if need be, for every live path. The reason is that `captureSurfaceAt` (`configdiff.go:936`) and capture-on-terminate call `ComposeStateful` with `render.Layers{}`, so they can learn the list paths only from the sidecar. The file is created only when the set is non-empty, so surfaces without lists are byte-identical.
- **Steady state:** for each list path P, compare last_render@P with current@P.
  - Array to array: added = new − old and removed = old − new, by whole-value presence. Neutralize P in the current value before `mergeDiff` (set it to last@P), so the merge patch never records P.
  - Accumulate symmetrically, keeping `add ∩ remove = ∅`. An added e leaves `remove` and joins `add`; a removed e leaves `add` and joins `remove`.
  - Key deleted, or replaced by a non-array: leave it to `mergeDiff` (a tombstone or whole value) and clear P's record. This is rule 4's "delete or replace the whole array". See [Blockers](#blockers).
  - A file array reappearing at P clears any whole-value capture at P from the overlay.
- **No convergence retirement for list records.** A user's removal of a contributed entry must survive a pack drop and re-add ("a per-entry removal the capture holds").
- **Narrowing:** drop P's record when `computed` or `managed` holds P, or any ancestor of P, as a non-object. This mirrors `dropOverriddenKeys` (`staterender.go`), because such a record is provably dead.
- **Adoption** (no trusted last_render): adopt only adds, computed as current@P − B@P. B is the fold below capture **without** contributions. So an entry already in the user's real file that a pack also contributes is adopted as the user's, and it survives a pack drop. That is the design's "host apply never removes a user's independently declared matching package", on `own`. The cost: after a deleted jail last_render, previously contributed entries become the user's.
- **Migration** (the overlay holds an ARRAY at a path that is now a list path, left by the old whole-array capture). This happens on the first steady-state render, before this boot's delta.
  - Take O = overlay@P and B = the fold below capture without contributions. Use B rather than the last render, because the last render at P *is* O.
  - Set `add = O − B` and `remove = B − O`, delete P from the overlay, and prune any `{}` parents left empty.
  - **Lost:** O's ordering relative to B (B's order wins and adds append in O's order), duplicate entries within O, and O's absolute pin. From then on, lower-layer changes at P show through.
  - A legacy **tombstone** at P stays a whole-value deletion and keeps masking contributions. Print a boot note naming `yolo config reset`.

### Per-entry capture: `rmw` (the baseline is yolo's own record)

- **A new record:** `<agent>-<name>.list-record.json` at a new `Target.ListRecordPath`, joined onto `ProvenanceDir()`. It lives there because the host under `assert` has no `SidecarDir` (`target.go:382`, **M**), while every constructed target has a ProvenanceDir. Format: `{"<pointer>": {"inserted": [...], "declined": [...]}}`.
- **For each render, with F = the file's array and K = the contributions:**
  - An entry in `inserted` that is missing from F was removed by the user: move it to `declined`.
  - An entry in `inserted` that is not in K belongs to a dropped pack: delete it from F and from `inserted`.
  - For e in K: skip it if declined. If it is present but not recorded, it is the user's own: leave it and do not record it. If it is absent, append it and record it.
  - `declined` persists.
- **Fail-safe read:** an unreadable record claims nothing, the same as `readProvenanceRecord`.
- **Placement:** the list application goes inside `applyRMWLayers` (`prism.go:1168`), so observe and assert agree. The record is persisted only on the write posture.

### The "does not capture per entry yet" refusal

- A data table in agentcfg, `ListCaptureRefusal(mechanism string, s manifest.Surface) string`, keyed on the **mechanism** that `Modes().Mechanism` resolved. A `stateful` surface on the host under `assert` is therefore judged as `rmw`.
- At the start of the build every row refuses. Each ENGINE step clears its row.
- It fires in two places:
  - `renderDeclaredSurface` (`packsurfaces.go:405`), as a returned error. That is fatal under A12, so `yolo check` and the launch both refuse. It must **not** be an `rmwRefusedError`, which only warns.
  - `RenderHostPack`'s mechanism switch (`hostrender.go:~290`), as a `refused: config-list …` row.

### Provenance

- Top-level `Result.Provenance` keeps its meaning. A key that only list contributions created gets a new label, `config-list`. `LayerAsserted` must return false for it, so that retirement and revert never remove the user's whole array.
- New `Result.Lists []ListProvenance`, one per list path, holding the entries in final order, the source of each (`base`, `config-list:<pack>`, `captured`), and a replaced-by field (`computed` or `managed`, or empty).
- `rmwProvenance` (`prism.go:865`) emits the same top-level label. Add a row to the parity corpus (`provenanceparity_test.go:92`).
- Nothing new is persisted for provenance. `config ls` derives replacement by managed from the declared `Managed`, using the narrowing predicate.

## Map (partition = owner; files are disjoint)

| Owner | Path | Change |
| :--- | :--- | :--- |
| SCHEMA | `internal/jsonptr/jsonptr.go` (new) and a test | `Parse`/`Format` for RFC 6901. A leaf that both packdecl and agentcfg import; packdecl must stay engine-free |
| SCHEMA | `internal/packdecl/kinds.go` | `KindConfigList`; `footprints` entry (`:377` sibling) |
| SCHEMA | `internal/packdecl/contributes.go` | `Path string`, `Add json.RawMessage` (`omitempty`, so absent ≠ `[]`); validation (`:2427` sibling, "does not take" block by `:2178`); `ConfigListContributions()` (mirror `:1575`) |
| SCHEMA | `internal/packdecl/kinds_test.go`, `configlist_test.go` (new) | Count 19→20 (`:31`), combine row, validation cases |
| SCHEMA | `internal/render/fieldset.go` | `HostFields` honors `KindConfigList` (`:240`). Final state, with no `hostUnimplemented` entry |
| SCHEMA | `internal/cli/run/packloopholes.go` | `disclosureClasses[KindConfigList] = disclosureSkip` (`:201` sibling) |
| SCHEMA | `internal/cli/pack.go` (the `packUsage` row only), `internal/cli/config_ref.txt` (the kind row by `:1216`) | Rows for `TestEveryKindIsDocumented` |
| SCHEMA | `internal/cli/applyhostcensus_test.go` | Census body (`writeCensusPack`, `:76`) targeting the census pack's own surface |
| ENGINE | `internal/agentcfg/listcontrib.go` (new) | `ListContribution{Pack, Path, Add}`; apply, equality, delta/accumulate/migrate, the rmw algorithm, the refusal table, the dead-path predicate |
| ENGINE | `internal/agentcfg/compose.go` | `Inputs.Lists`, `Inputs.ListCapture`; split the fold at `:487` around the capture overlay; `Result.Lists`; the `config-list` label |
| ENGINE | `internal/agentcfg/staterender.go` | `StatefulInputs.ListCaptureJSON`, `StatefulOutput.ListCaptureJSON`; capture, adoption, migration and narrowing as above |
| ENGINE | `internal/agentcfg/manifest/manifest.go` | `ModeRMW` doc: "writes no sidecars" becomes false once a list targets it |
| ENGINE | `internal/packoverlay/packoverlay.go` | Collect lists in the same two passes (`:154`); `ListsFor`; `OrphanOverlay.Kind`; applied-lists report; `Problems` for pointer, surface id or keyless target |
| ENGINE | `internal/render/surface.go`, `target.go`, `modes.go` | `Layers.Lists`; `State.ListCaptureJSON`; `ListCapturePath`, `ListRecordPath`; the `JailModes` rmw reason text |
| ENGINE | `internal/entrypoint/prism.go`, `packsurfaces.go`, `hostrender.go`, `hostrevert.go`, `packrender_test_support.go` | Thread lists; read, write and persist the capture and record; refusal; boot notice; `HostRenderResult.Lists`; revert removes `inserted` entries (and a `config-list`-labelled key left empty) |
| ENGINE | `internal/cli/configdiff.go`, `configcapture.go` (`:88-89`), `configtarget.go` | `captureSurfaceAt` reads and writes the list capture; reset deletes it (`:476` loop); diff prints `+`/`-` lines per list path and counts a non-empty record as a capture |
| ENGINE | `integration/configlist_test.go` (new) | See [Ships with](#ships-with) |
| SURFACES | `internal/cli/config.go` | `renderSurface` (`:401`) folds overlays **and** lists (Collect at `t.notch`, as `configprovenance.go:126` does); `--explain` prints each list path's ordered entries, their sources and any replacement |
| SURFACES | `internal/cli/configprovenance.go`, `configls.go` | The `ls` provenance block lists contributors per path, and managed replacement; the ls capture count includes list records |
| SURFACES | `internal/cli/apply.go` | `config-list entries from: …` per surface (beside `:670`); orphan and problem lines (beside `:541-553`) |
| SURFACES | `internal/packload/footprint.go` | Claim loop beside `:614`: target `agent/name` plus the pointer, with the entry count in the detail (this is also what `pack lint` prints) |
| DOCS | `docs/reference/pack-system.md` | Kind row (`:401`), layer order (`:1192`, `:1961`), a `config-list` section, and the [OQ-LT2](../reference/pack-system.md#oq-lt2) row (`:1932`). Its "nothing declarative edits inside a host-supplied array" is now false |
| DOCS | `docs/reference/config-migration-to-prism.md` | `:97` "three writes": the list capture is a conditional fourth, plus the migration paragraph |
| DOCS | `docs/design/additive-config-lists.md` | Example spelling becomes `"/packages"`; the Built column at landing |

## Reuse before you write

- Test fixtures: `overlayOwnerPack`, `overlayContributorPack`, `overlayRenderEnv` and `readRenderedJSON` (`packoverlayrender_test.go:28-79`). Mirror every test in that file for lists, at both notches.
- `jsonx.Plain` for equality. `marshalOverlay` and `parseOverlayKind` show the fail-safe shape for the new sidecar. `writeSidecar` (`prism.go:83`) handles modes.
- `refuseRMW` for rmw type conflicts. `genStep` for fatal boot problems (`reportOverlayResolution`, `packsurfaces.go:213`).
- `Modes().Mechanism` for the refusal key; never `ResolvedMode()` alone.
- The integration helpers `packHome`, `writeProject` and `runYolo` (`integration/packs_test.go:~520`).

## Traps

- **Capture-on-terminate freezes the list again.** `captureSurfaceAt` passes no layers. If it does not read and write the list capture, the host-side teardown captures the whole array into the overlay, and the bug is back. The stateful refusal row must stay until this lands **in the same commit**.
- **rmw writes `defaults` last and fill-if-absent** (`prism.go:1190`). If lists apply before the defaults fill, a default `packages: [a]` is lost when the key was absent, which is a parity break with `Compose`.
  - Apply lists after the defaults fill.
  - Skip paths that managed or computed asserted.
  - The parity corpus row catches this.
- **`config render` does not fold overlays today** (`config.go:412` passes only `HostBytes`, **M**), and its doc comment says so. Rule 5 needs both folded, so rewrite that comment and any test pinning the old scope.
- **`config diff` is capture-only by ruling** ([OQ-CR7](../reference/config-target-resolution.md#oq-cr7)). Contributor accounts belong to `ls` and `render --explain`; do not put them back in `diff`.
- **Fingerprint gate:** `TestRenderFingerprintStable` (`internal/entrypoint/renderfingerprint_test.go`). Splitting the fold must be byte-identical when there are no lists.
- **The census test** requires the string `config-list` in the `host apply` output. At the end of step 1 that string comes from the refusal row; later it comes from the applied line.
- Run tests with `env -u YOLO_VERSION -u YOLO_HOST_LAYERS` (four `internal/cli` tests go red in-jail otherwise).

## Build order (each step green: `env -u YOLO_VERSION -u YOLO_HOST_LAYERS go test -short <pkgs>`, `gofmt -l`, `GOOS=darwin go vet`)

1. **SCHEMA + ENGINE collection and refusal: one commit.** The kind-exhaustive gates make an unconsumed kind unrepresentable. Contents: the kind, validation, jsonptr, the registries, Collect, and the refusal table, with every row refusing and wired at both entries. → `./internal/packdecl/... ./internal/jsonptr ./internal/packoverlay ./internal/render ./internal/entrypoint ./internal/cli/...`
2. **ENGINE `computed`:** the `Compose` fold, type conflicts, provenance, `Result.Lists`. Clear the computed row. → `./internal/agentcfg/... ./internal/entrypoint`
3. **ENGINE `stateful`:** the list-capture sidecar, capture, accumulate, adoption, migration and narrowing; `captureSurfaceAt`, capture-on-terminate, reset and diff. Clear the stateful row. **This is the step that must not be split.**
4. **ENGINE `rmw`:** the record, the algorithm inside `applyRMWLayers`, revert, and the parity row. Clear the rmw row.
5. **SURFACES ∥ DOCS,** in parallel. Then nested-jail verification (AGENTS.md "Workflow" step 2) from `/tmp/yolo-nested`, with `packs: ["pi", "file://<pack>"]`.

## Ships with

- **Unit tests (agentcfg):**
  - Append, dedupe, order, empty `add`, and a missing path; type conflicts at a parent and at the path.
  - An overlay replacing the array, followed by a re-add ([OQ-AL2](./additive-config-lists.md#decision-ledger)); managed or computed replacing the final array.
  - Delta and accumulate symmetry; a user removal of a contributed entry that persists across a drop and re-add.
  - A deletion and a non-array edit captured whole; migration from an array and from a tombstone.
  - Adoption keeps the user's matching entry; a sidecar whose paths are all empty; a corrupt sidecar.
  - TOML int against JSON float equality.
- **Unit tests (entrypoint), at both notches:** jail stateful, computed and rmw; host `assert` (rmw) and `own` (stateful); the refusal is fatal at boot and a `refused:` row on host; orphan; `unrendered` inert; revert removes only `inserted`.
- **Mutation-check every new call site:** Collect's list pass, each refusal wiring, the list threading at `renderDeclaredSurface`, `RenderHostPack`, `composeStatefulSurface` and `captureSurfaceAt`, the persist of each sidecar, and the reset deletion. Report each one.
- **Integration:** `integration/configlist_test.go`, a single test with three launches in one workspace.
  1. First launch with pi and a local contributing pack: `settings.json` holds pi's list plus the contributed entry, once.
  2. In-jail, `jq` appends an entry (standing in for `pi install`; no agent runs). Relaunch: both entries are present.
  3. Drop the contributing pack and relaunch: the contributed entry is gone and the edit is kept.

  This is the only test that exercises capture-on-terminate's host-side `captureSurfaceAt` end to end.
- **Tests to rewrite, not repair:** anything pinning `renderSurface`'s "defaults < host < managed" scope, and `ModeRMW`/`JailModes` sentences asserting that rmw writes no sidecar.
- **Roadmap:** `docs/plans/roadmap.md` row 10 links this plan, moved in the same commit as this file. The row leaves at landing.
- **Cheap choices, left to the implementer:** the explain line layout, the helper factoring, and whether the render signatures take a `Contributions{Overlays, Lists}` struct. Advice: take the struct, since six signatures grow a parameter otherwise.

## Don't

- Do not make arrays additive in `mergeValue`/`mergeAccumulate`, and do not redefine `null`. The design's warning applies.
- Do not store list records inside the overlay JSON. Every overlay reader would read the marker as a user key, and `promote` would copy it into a pack.
- Do not promote list records (`config promote`), and do not extend `PruneHostOverlayKeys` to the case where both the owner and the contributor are dropped. Both are separate roadmap items.
- Do not add a `profile` gate on `config-list`. The design does not ask for one.

## Blockers

These are readings to confirm before step 3. Neither blocks steps 1–2.

1. **Rule 4 against [OQ-AL1](./additive-config-lists.md#decision-ledger).** Rule 4 says captured edits keep the power to "replace or delete the whole array", while [OQ-AL1](./additive-config-lists.md#decision-ledger) says capture is "per entry, never the whole array". This plan reads the two together:
   - Array-to-array edits are per entry.
   - Deleting the key, or replacing it with a non-array, is whole-value and outranks contributions.
   - An array emptied to `[]` is per entry, so an entry first contributed *later* still appears.

   **Stop and ask** if that last consequence is wrong.
2. **Rule 5 names `config diff`,** which [OQ-CR7](../reference/config-target-resolution.md#oq-cr7) made capture-only. This plan puts the per-entry *captured* lines in `diff` and the contributor account in `render --explain` and `ls`. Confirm.
