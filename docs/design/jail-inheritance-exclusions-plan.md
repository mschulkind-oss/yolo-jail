---
title: "Plan: filter exact members from imported user host configuration"
status: accepted
stage: BUILT
next: "Every task is built; retire this plan when jail-inheritance-exclusions.md graduates"
depends-on:
  - jail-inheritance-exclusions.md
summary: "Tree-grounded build hand-off for user-only JSON Pointer exclusions at the imported host-layer boundary."
---

# Plan: filter exact members from imported user host configuration

**Status:** 2026-10-21. Completed against the tree, written against `e7b52b944d369432a4a19aff303b0b8dd68860dd`.
**Design:** [`jail-inheritance-exclusions.md`](jail-inheritance-exclusions.md). The design wins on behavior; the tree wins on facts; this plan is advice and the first thing to be wrong. Never twist code to match a stale path. Stop and report if a named symbol is gone, a test does not fail as predicted, a fenced file has another writer's changes, a gate fails outside scope, or work needs a path outside the map.

The existing survey in the former sketch is the source for the component map and reuse list below; this completion rechecked those touchpoints at the stamped tree rather than repeating its discovery narrative. This is a build plan, not evidence that any runtime behavior is implemented.

## Map

| Component | Paths | Change |
| :--- | :--- | :--- |
| Config | [`internal/config/config.go`](../../internal/config/config.go), [`internal/config/validate.go`](../../internal/config/validate.go), [`internal/config/inherit.go`](../../internal/config/inherit.go), their tests, [`internal/cli/config_ref.txt`](../../internal/cli/config_ref.txt) | Declare and validate the proposed `host_import_exclusions` setting, scope it to user config, and preserve it only in intended user snapshots. |
| Exact transform | New `internal/agentcfg/hostimportexclusions.go` and `hostimportexclusions_test.go` | Apply strict JSON Pointer object-member removals through the selected surface codec. |
| Launch and boot | [`internal/cli/run/packhostgrants.go`](../../internal/cli/run/packhostgrants.go), [`internal/cli/run/assemble.go`](../../internal/cli/run/assemble.go), [`internal/entrypoint/hostlayerlabel.go`](../../internal/entrypoint/hostlayerlabel.go), [`internal/entrypoint/packsurfaces.go`](../../internal/entrypoint/packsurfaces.go), and their tests | Carry a normalized policy through the existing host-layer wire, apply it only to a delivered user host layer, and fail closed on requested-filter codec failures. |
| macos-user delivery | [`internal/cli/run/macosctxtree.go`](../../internal/cli/run/macosctxtree.go), [`internal/macosuser/runplan.go`](../../internal/macosuser/runplan.go), [`internal/macosuser/ctxtree_test.go`](../../internal/macosuser/ctxtree_test.go), [`internal/macosuser/hostlayers_test.go`](../../internal/macosuser/hostlayers_test.go) | Preserve copied host-source and render provenance parity and carry the same policy in the bootstrap environment. |
| Preview and reference | [`internal/cli/config.go`](../../internal/cli/config.go), [`internal/cli/confighostlayer_test.go`](../../internal/cli/confighostlayer_test.go), config-reference tests | Filter the same staged user host bytes in preview; document the setting and its scope. |
| Real-jail proof | New `integration/hostimportexclusions_test.go` | Verify selected-layer behavior and host-source preservation through a real launch. |

## Reuse before you write

- `config.UserScopeConfig` in [`internal/config/userlayer.go`](../../internal/config/userlayer.go) strictly reads user config, includes and inherited user scope; `LoadConfig` in [`internal/config/workspacefile.go`](../../internal/config/workspacefile.go) is merged scope and is not the policy source.
- `config.HostManagementDeclared` in [`internal/config/hostmanagement.go`](../../internal/config/hostmanagement.go) is the existing direct user-scope ownership boundary; the feature's non-empty policy is allowed only for effective `none` (explicit or unset).
- `FilterInheritErr` / `FilterInherit` in [`internal/config/inherit.go`](../../internal/config/inherit.go) own preflight and nested snapshots. Extend the existing census and both scope projections; do not hand-build a second snapshot path.
- [`internal/jsonptr.Parse`](../../internal/jsonptr/jsonptr.go) already implements RFC 6901 token decoding, including `/` for an empty key and acceptance of the empty root pointer. Reject the root pointer at the new config boundary; do not fork its parser.
- [`internal/agentcfg/codec.LookupCodec`](../../internal/agentcfg/codec/codec.go) and codec `Decode`/`Encode` are the surface-format boundary. Permit only `KindObject`; `lines` and `raw` have no members.
- `entrypoint.HostLayerWire` / `HostLayerDispositionIn` in [`internal/entrypoint/hostlayerlabel.go`](../../internal/entrypoint/hostlayerlabel.go) already distinguish delivered user bytes from render baselines across backends. Keep one authoritative wire and do not infer provenance from path contents.
- `agentcfg.DropHostProviderKeys` in [`internal/agentcfg/providerkeys.go`](../../internal/agentcfg/providerkeys.go) is adjacent but not reusable for behavior: provider ownership, matching, pruning, and failure semantics differ.
- [`hostLayerFor`](../../internal/cli/config.go) already selects only the launch-staged preview source and drops render baselines. Apply the shared transform after those choices; never probe the host destination or another workspace's `/ctx` copy.
- `captureFromSessionHomes` in [`internal/entrypoint/sessioncapture.go`](../../internal/entrypoint/sessioncapture.go) runs before surface writing. Keep it outside the transform: captured in-jail edits are another layer, not imported host input.

## Traps

- `HostLayerWire.DispositionFor` must take precedence over a matching delivered path labelled `rendered`; filtering yolo's own host-render baseline corrupts provenance.
- A syntactically valid pointer can miss because the member is absent or an intermediate is scalar/null. The [OQ-IH3 ruling](jail-inheritance-exclusions.md#decision-ledger) makes this a silent no-op, not a warning and not a validation error.
- Dormancy applies only to known capable surfaces in unselected packs. Unknown and incapable targets remain errors; do not apply one target to a similarly named selected surface.
- A transform failure after an exclusion was requested must not pass the original bytes to composition. The source host file remains untouched on success and failure.
- Empty parent objects remain after removal; do not implement generic empty-object pruning or array-index traversal.
- Attach does not restage an already running jail's pack tree; exclusions take effect on the next launch/render, not through attach-time mutation.
- The macos-user plan builder [`internal/macosuser/runplan.go`](../../internal/macosuser/runplan.go) is also in the matched-development-bundles implementation fence. Its exclusion-policy change is serialized after that plan's Task 1 lands; do not have two writers edit it concurrently.

## Tasks

**Task 1 — define, validate, and inherit user policy (independent).**

- **Settled:** only user-scope config and its includes/inherited user layer define the exclusions; workspace config cannot define, replace, or extend them. Preserve-all is the default. Only effective `host_management: none` or unset allows non-empty exclusions; `own` refuses them. Unknown and incapable targets are errors; known capable targets for unselected packs are retained dormant. Do not re-open.
- **Tests first:** extend `internal/config` validation tests and `inherit_test.go`. Exact config fixture: `{"host_import_exclusions":{"claude/settings":["/hooks","/statusLine"]}}`. Assert absent key and `{}` preserve imports; workspace-only and conflicting workspace values refuse; user `host_management:"own"` refuses while absent/`"none"` allows; known unselected capable target survives inherited user snapshots for preflight and nested launch; an unknown target and a known non-object/no-host-layer target fail even when unselected. Keep malformed shape, malformed pointer and root pointer (`""`) refusal. Red: `go test ./internal/config -run 'Test(HostImportExclusions|FilterInherit)'` after adding these cases.
- **Change:** add typed normalization beside config's other user-only settings; register validation in `ValidateConfig`; update the inherit census and `FilterInherit` scopes so the user policy crosses generated user snapshots without admitting workspace input; add the setting to `internal/cli/config_ref.txt` and its generator/source test if required by the current reference test.
- **Gates:** `go test ./internal/config`; `go test ./internal/cli -run 'Test.*ConfigRef'`; inspect `FilterInherit` output for both scopes and prove it contains user policy only.
- **Fences:** no pack-manifest schema, workspace exception, change to `host_management`, or default import change. Do not change `HostManagementDeclared` ownership semantics.
- **Commit:** `feat(config): validate user host import exclusions`.
- **Report:** red output, exact scope/ownership/target cases, inheritance output for each scope, and the config-reference check.

**Task 2 — implement the exact-member transform and container boot path (after 1).**

- **Settled:** remove only exact final object members through object intermediates. Numeric object keys are ordinary names; never traverse arrays. Root-pointer removal is forbidden. Missing members/non-object intermediates quietly do nothing. Duplicate requests and descendants of an already removed ancestor are no-ops. Preserve empty parent objects. Malformed declarations fail validation; host decode/filtered encode errors fail closed rather than using original bytes. Do not re-open.
- **Tests first:** add `internal/agentcfg/hostimportexclusions_test.go` with input `{"hooks":["keep?"],"nested":{"a/b":{"~key":1},"stay":2},"0":"numeric","empty":""}` and pointers `["/hooks","/nested/a~1b/~0key","/0","/missing","/nested/stay/child"]`; assert only the exact final members disappear, key `"0"` is addressed as an object key, absent/non-object paths are quiet, and `nested` remains `{}` after removing its last child. Also cover `/` empty-key removal, duplicate/ancestor order, source-byte immutability, malformed `~2`, empty root rejection, object-codec errors, and non-object codec refusal. Red: `go test ./internal/agentcfg -run '^TestHostImportExclusions'`.
- **Change:** add the codec-aware transform using `jsonptr.Parse`; wire normalized policy through the existing `YOLO_HOST_LAYERS` `HostLayerWire` as its per-surface map; in `renderPlannedSurface`, transform only after `HostLayerDispositionIn` establishes delivered user bytes and before `DropHostProviderKeys`, capture and writing. Keep rendered/unsupported/no-file/unknown delivery behavior unchanged. Emit no policy derived from workspace config.
- **Gates:** `go test ./internal/agentcfg ./internal/entrypoint`; add production-callsite tests in `entrypoint` proving the filter is reached through `ConfigurePackSurfaces`/`renderPlannedSurface`, not only by calling the transform; assert an equal pack/computed/captured member can still appear and a render baseline is untouched.
- **Fences:** no final composed-file scrub, capture deletion, host-file mutation, null tombstone, regex/vendor-key logic, or change to host-layer refusal/provenance.
- **Commit:** `feat(config): filter exact imported host members`.
- **Report:** transform red/green output, production caller proof, failure disposition and before/after source bytes.

**Task 3 — deliver to macos-user and match preview (after 2; macos-user after matched-bundle Task 1).**

- **Settled:** container and macos-user filter the same imported user bytes and nothing else. A render baseline is not filtered. Preview consumes the same staged bytes and normalized policy; unavailable preview input stays unavailable. No host-side write or alternate source probe. Do not re-open.
- **Tests first:** extend `internal/macosuser/ctxtree_test.go` / `hostlayers_test.go` to stage a host file with `{"hooks":["host"],"keep":true}` and verify the policy reaches `BootstrapArgv`, while `YOLO_HOST_LAYERS` still marks rendered files as `render`. Extend `internal/cli/confighostlayer_test.go` so a delivered staged file removes `/hooks` in preview, but a missing/unavailable staged file does not read the host destination and remains unavailable. Red: `go test ./internal/macosuser ./internal/cli -run 'Test(.*HostLayer|.*HostImportExclusion)'`.
- **Change:** pass the policy through container launch assembly and macos-user's existing `HostLayerWire` producer; preserve host source and rendered labels. Apply the shared transform in preview after `hostLayerFor` has rejected absent sources and baselines. Update source-named CLI config reference test only if Task 1's config reference output changes.
- **Gates:** `go test ./internal/cli ./internal/cli/run ./internal/macosuser`; compare container/macOS wire decode and filtering behavior; assert preview and boot use equivalent normalized policy.
- **Fences:** [`internal/macosuser/runplan.go`](../../internal/macosuser/runplan.go), `ctxtree_test.go`, and `hostlayers_test.go` are exclusive to this task after matched-development-bundles Task 1 lands. Do not overlap their edits with that worker. Do not change VM delivery, staged roots, host read grants, or preview source selection.
- **Commit:** `feat(config): preserve exclusions across backend previews`.
- **Report:** backend wire results, preview staged-source result, MacOS-user ordering/dependency, and source immutability.

**Task 4 — prove full launch composition and preserve user-facing boundaries (after 1–3 and matched-bundle Task 4).**

- **Settled:** absent/empty policy preserves current behavior; the filter affects only user host import; matching pack/default/computed/managed/captured values remain eligible; host files and provenance/captures never change. Do not re-open.
- **Tests first:** add `integration/hostimportexclusions_test.go` using the existing `requireJail(t)` serial integration harness. Launch with a host config containing a unique excluded member and an unexcluded sibling; assert the member is absent in the rendered jail file and sibling remains, then assert the host source bytes are identical. Add a second composition case where a selected pack/computed contribution supplies the same excluded pointer and remains present. Include empty policy as unchanged-import control and an unselected known target as dormant. Red: `go test -run '^TestHostImportExclusions$' ./integration` (without `-short` on a configured jail).
- **Change:** exercise the launch route end-to-end; no implementation outside the earlier task map. If the real-jail fixture cannot select an existing surface/contribution to prove same-pointer preservation, stop and report rather than weakening the criterion or modifying shipped pack behavior.
- **Gates:** `go test -short ./internal/config ./internal/agentcfg ./internal/entrypoint ./internal/cli ./internal/cli/run ./internal/macosuser`; `go test -run '^TestHostImportExclusions$' ./integration`; run source references for the key and `OQ-IH[23]`; `git diff --check`.
- **Fences:** no runtime docs beyond `internal/cli/config_ref.txt`, no default behavior or pack edits, no hooks/script copying, and no config key alias.
- **Commit:** `test(config): verify host import exclusions end to end`.
- **Report:** integration command/output, host-source digest or byte comparison, matching-layer result, and every targeted unit gate.

## Ships with

- **Config unit:** absent/empty setting; malformed map, address, list and pointer; root pointer; workspace-only and workspace override; `own` refusal and unset/`none` acceptance; unknown and incapable unselected targets; known capable dormant target; preflight/nested inherited user snapshot.
- **Transform unit:** RFC 6901 escapes, empty key, ordinary numeric object key, scalar/null/array/object final values, object-only intermediate walk, missing/non-object quiet no-op, duplicate/ancestor pointers, preserved empty parent, stable encoding, source immutability, malformed input and codec failures.
- **Production call sites:** entrypoint host import vs render baseline; provider filter ordering; pack/computed/captured same-pointer survival; launch policy serialization; macos-user provenance parity; staged-source preview parity and unavailable preview behavior.
- **Integration:** actual launch removes only requested user host bytes, keeps an unexcluded sibling and equal non-host contribution, leaves source host bytes unchanged, and proves dormant target behavior.
- **Reference/config surface:** `internal/cli/config_ref.txt`; no public guide or pack schema changes are implied by this user-scope key.

## Don't

- Do not interpret an empty pointer list as a request to suppress anything; do not treat the empty root pointer as a member.
- Do not widen to workspace config, pack/default/computed/managed/captured layers, host-render baselines, host apply, final output pruning, or global policy.
- Do not make a missing pointer visible with a warning; do not relax validation of unknown/incapable targets because known unselected targets are dormant.
- Do not parse hook commands, copy hook bodies, add mounts/grants, crawl directories, or mutate host files.

## Stops and completion

Stop without committing if a named callsite or test is gone, the predicted red does not reproduce, workspace config can affect normalized policy, `own` is not refused, an unavailable target cannot be distinguished as unknown/incapable versus known dormant, a transform failure can pass original bytes, filtering reaches anything other than the intended imported user-host bytes (including pack/default/computed/managed/captured values or a host-render baseline), or the fix needs an unmapped file. Report the command and exact mismatch; do not invent a compatibility alias or broaden scope.

The plan is ready to build before implementation acceptance; implementation acceptance requires task gates to pass and real-jail evidence on its configured host. A skipped integration test, unit-only filter test, or a commit claim is not runtime evidence.
