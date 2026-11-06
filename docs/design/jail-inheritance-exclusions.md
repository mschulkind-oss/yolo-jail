---
title: "Explicit jail inheritance exclusions keep user host bytes separate from managed state"
status: accepted
stage: BUILT
next: "Graduate into a system doc under docs/reference/ and retire jail-inheritance-exclusions-plan.md; validation, the exact-member filter, launch and preview are built"
tags: [config, inheritance, host, packs]
summary: "A user-only, subtractive control over exact members of imported host config bytes, without changing pack, computed, captured, or managed layers."
---

# Explicit jail inheritance exclusions keep user host bytes separate from managed state

**Status:** 2026-11-06. Built: user-scope validation, the exact-member filter (`internal/agentcfg/hostimportexclusions.go`), launch and macos-user delivery, and the preview, with `integration/hostimportexclusions_test.go`; graduation to a system doc is next. The design's existing-code claims were checked at `e7b52b944d369432a4a19aff303b0b8dd68860dd`.

> **In short.** A user may omit exact members of a host config file from a jail's imported host layer without changing the host file. The filter runs on that input alone; composition and capture continue to own every other layer.

**Why it matters.** A host settings file may contain a hook or local preference that should stay on the host, while other host values remain useful in the jail.

**The shape.** A user-scope map addresses selected config surfaces and exact JSON Pointers; one shared filter changes only imported user host bytes.

**Cost.** This is subtractive input control, not a guarantee that a pointer is absent from the final jail file.

**Start at [§1](#1-the-boundary-is-the-feature)** — the input boundary is the security and behavior contract.

**Needs your ruling:** None.

**Reads with:** [`config-ownership.md`](../reference/config-ownership.md) (user-only host ownership and capture), [`config-target-resolution.md`](../reference/config-target-resolution.md) (host-render provenance), [`pack-system.md`](../reference/pack-system.md) (surface composition), [`config-migration-to-prism.md`](../reference/config-migration-to-prism.md) (existing captured edits), [`jail-inheritance-exclusions-plan.md`](jail-inheritance-exclusions-plan.md) (completed implementation hand-off; design wins on behavior).

---

## 1. The boundary is the feature

This controls which **user host bytes** enter a jail render; it is not a policy for the final file. Removing a member from the host input does not suppress a default, computed value, pack contribution, managed value, or captured in-jail edit at the same pointer. There is no global deny list over the composed output.

The settled owner constraints are:

- **Imports are unchanged by default.** An absent key, an empty map, or an empty pointer list preserves today's host import.
- **The declaration is user-scope only.** Workspace config cannot set, replace, or extend it. Resolve it from user scope directly, never from the merged workspace config. The same rule holds when a jail passes user settings to a nested jail: an inherited user snapshot may carry the user declaration, but a workspace snapshot is never its source.
- **Only effective user `host_management: none` or unset (`none`) permits a non-empty exclusion.** Under `own`, refuse the explicit exclusion and name the surface and pointers. Explain that host-owned values must be configured through the pack contribution's existing host-versus-jail targeting, or the exclusion removed. Do not infer ownership from the confinement backend.
- **Known unselected surfaces stay dormant.** Validate every configured target against the shipped surface vocabulary and its ability to read an object host layer. An unknown or incapable target is a configuration error even when its pack is not selected. A known capable target whose pack is not selected has no effect until selected; do not apply it to a similarly named surface.
- **Missing members are quiet no-ops.** A syntactically valid pointer that encounters a missing member or non-object intermediate changes nothing and emits no warning or summary. The pointer is an exact filter, not a schema assertion; it never guesses another path.
- **Filter only imported user host bytes.** Do not filter a pack/default/computed/managed/captured layer, a host-render baseline, or in-jail session-home bytes. Do not clear captures or provenance marks.
- **The host source is read-only input.** Filtering must not write the original host file or copy filtered bytes back to it.
- **Core knows no vendor setting names.** A pointer expresses the user's choice; `/hooks` has no special meaning to core.

The owner's rationale for the second ruling: “These are just filters acting like filters. If the thing to be filtered out doesn't exist, it won't get filtered out because it's not there anyway.”

The ownership rule already has a security seam: [`HostManagementDeclared`](../../internal/config/hostmanagement.go#L86-L109) reads user scope directly. Workspace config is merged later by [`LoadConfig`](../../internal/config/workspacefile.go), so reading the merged map for this policy would violate the boundary. Host bytes reach composition in [`renderPlannedSurface`](../../internal/entrypoint/packsurfaces.go#L704-L737), after the launcher labels yolo-rendered baselines in [`packhostgrants.go`](../../internal/cli/run/packhostgrants.go#L213-L253). Preserve those boundaries.

## 2. Proposed declaration and exact-pointer contract

The proposed core key is `host_import_exclusions`: a map from a surface address (`agent/name`) to exact JSON Pointer strings. This spelling is recommended, not a blocker or a separately owner-decided choice. It adds no pack-manifest field or vendor schema.

```json
{
  "host_import_exclusions": {
    "claude/settings": ["/hooks", "/statusLine"]
  }
}
```

Use strict RFC 6901 pointer syntax through [`internal/jsonptr`](../../internal/jsonptr/jsonptr.go#L23-L49). Reject the empty root pointer: the control removes object members, not whole documents. `/` is valid and names the empty-string object key. Decode only selected, object-codec config surfaces that can receive a host layer; non-object codecs have no members to exclude.

The proposed removal behavior is deliberately literal:

- Resolve pointer tokens through objects only. A numeric token is an ordinary object key; array indices and `-` are never traversed.
- Remove the final object member and its whole value, including null, array, object, or scalar values. Preserve now-empty parent objects.
- A missing member or non-object intermediate is a quiet no-op: no warning, summary, heuristic, or schema-based substitute.
- Duplicate pointers are one request. An ancestor removal makes its descendants no-ops; declaration order does not affect the result.
- Malformed pointers and the empty root pointer are configuration errors. An invalid declaration never degrades to an unfiltered import.
- If active exclusions cannot decode the host bytes as an object, or cannot encode the filtered object using the surface codec, fail closed for that surface/preview. Never pass the unfiltered bytes through after an exclusion was requested.
- An absent host file remains the existing normal no-layer case. It is not an error and does not cause another path to be probed.

The existing [`DropHostProviderKeys`](../../internal/agentcfg/providerkeys.go#L51-L85) is not this filter: its provider-owned keys, regex matching, null/pruning, and codec-error behavior differ. Reuse pointer grammar, not that removal contract.

## 3. Target scope and launch flow

Targets are checked against the shipped surface vocabulary, including surfaces from unselected packs. Unknown targets and known surfaces that cannot receive an object host layer are configuration errors; a known capable target is dormant when its pack is not selected and applies only when that pack is selected. Never substitute a similarly named surface. The workspace declaration is a validation error with a repair pointing to `~/.config/yolo-jail/config.jsonc`. Under `own`, any configured non-empty exclusion refuses before reading host bytes; unset is the same effective `none` value as explicitly setting `none`.

At launch, deliver only the normalized per-surface exclusion policy needed by this render; never pass `host_management` into the jail. The jail must first honor the existing host-layer delivery report and host-render provenance label. Apply the shared filter only when those bytes are a user host layer, before provider-key filtering and before `captureFromSessionHomes`/the writer. Unsupported delivery and a missing file remain no-host-layer cases. A delivered-but-unreadable source or a filter codec failure remains fail-closed.

The existing launch report [`HostLayerWire`](../../internal/entrypoint/hostlayerlabel.go) already distinguishes delivery from a host-render baseline and is emitted for container backends by [`hostLayerEnv`](../../internal/cli/run/packhostgrants.go#L123-L165); `buildMacosCtxTree` copies and labels the same host bytes for macos-user ([`macosctxtree.go`](../../internal/cli/run/macosctxtree.go#L159-L220)). Preserve this parity rather than deriving provenance in the jail.

`yolo config` previews must use the same normalized policy and the same staged source. [`hostLayerFor`](../../internal/cli/config.go#L736-L771) refuses to substitute a host destination or another workspace's staged `/ctx` copy and drops a render baseline. Apply the same helper after those decisions. A preview without this jail's staged bytes remains unavailable; it must not read another source to make the preview appear complete.

## 4. Existing homes and capture

This is a **future-import filter**, not a purge or migration of captures:

- On the next jail render, the excluded host member is absent from the host layer.
- On an established home, an unchanged value from the previous render falls out under the existing last-render comparison. A genuine captured in-jail edit remains in its capture layer and can still produce that member.
- First adoption/migration continues to use the existing archive and capture behavior; the exclusion neither erases captured residue nor claims an unbaselined value was imported from the host.
- Session-home capture, reset, and later capture reconciliation operate on their in-jail bytes unchanged. The filter does not delete captured state.
- Removing an exclusion restores future imports; it cannot recover or alter a host value because the host file was never changed. Policy takes effect on a launch that stages and renders it; attach does not restage a running jail's immutable pack tree.

Success means an exact user member is absent from both boot composition and a matching staged preview, while an equal pack/computed/captured value can still appear; a prior yolo-rendered baseline is not imported; codec failures do not leak unfiltered bytes; and host files and provenance/capture state remain unchanged on every backend.

## 5. Alternatives and non-goals

| Alternative | Disposition |
| :--- | :--- |
| PR 55 automatic regex dropping | Rejected and superseded. The existing assessment demonstrated that working `printf` text and a quoted space-path script were falsely removed. Do not implement that behavior. |
| Filter the final composed file | Rejected. It would suppress pack, default, computed, managed, or captured values and turn input control into a vendor-key policy. |
| Null/tombstone the key | Rejected. A tombstone crosses layers and can suppress a pack value. |
| Mutate the source or host-managed destination | Rejected. It makes a jail-only import choice write into the host's config. |
| Copy hook bodies as part of exclusions | Deferred. It is a separate capability with a different file-read boundary. |

**Non-goals:** no default import change; no workspace declaration; no vendor-specific schema; no host writes; no guarantee a pointer is absent from final output; no new host path/credential grant; no attach-time reconfiguration; no hook-shell parsing, implicit reads, crawling, regex, wildcard, recursive rule, value match, or array-index selector.

## 6. Deferred hook-body copying

Hook-body copying must not delay or condition the filter. A later bounded assessment may consider only finite explicit source/destination pairs below already-authorized roots, with capped no-follow regular-file reads. It must establish containment, symlink/special-file rejection, file/count/total caps, collision behavior, and a failure remedy. No shell parsing, implicit reads, recursive discovery, new grants, home mounts, or command rewriting; copied scripts are not thereby portable or runnable.

## Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| OQ-IH1 | **User config only.** Workspace configuration cannot declare, replace, or extend host-import exclusions. The host-management gate remains a direct read of user scope. | 2026-10-21 | [§1](#1-the-boundary-is-the-feature) | — |
| OQ-IH2 | **Keep it dormant.** Validate targets against shipped capable surfaces; a known target for an unselected pack remains dormant. Unknown and incapable targets remain validation errors. | 2026-10-21 | [§3](#3-target-scope-and-launch-flow) | — |
| OQ-IH3 | **Quietly do nothing.** A valid pointer missing a member or crossing a non-object intermediate changes nothing and emits no diagnostic. | 2026-10-21 | [§2](#2-proposed-declaration-and-exact-pointer-contract) | — |
