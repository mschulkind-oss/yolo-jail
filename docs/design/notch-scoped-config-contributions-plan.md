# Notch-Scoped Config Contributions — Implementation Sketch

**Status:** SKETCH, 2026-09-27 — incomplete, and unstable while questions are open.

> **Precedence.** This is an implementation sketch companion to
> [`notch-scoped-config-contributions.md`](notch-scoped-config-contributions.md).
> The design doc wins on all behavioral decisions; nothing here makes a design decision.

---

## 1. Affected Codebase Map

| File / Package | Responsibility in this feature |
| :--- | :--- |
| `internal/packdecl/contributes.go` | Add `Notch string` / `Notches []string` to `Contribution`, `ConfigList`, `ConfigOverlay`. Implement normalization and strict validation against `render.SelectableNotches`. |
| `internal/packdecl/kinds.go` | Document the `notches` modifier on `KindConfigList` and `KindConfigOverlay`. |
| `internal/packoverlay/packoverlay.go` | Update `Collect` to accept target notch (`render.Kind`) instead of/in addition to `autonomy bool`. Filter `config-overlay` and `config-list` based on target notch matching. |
| `internal/entrypoint/prism.go` | In `renderSurfaceStateful` and `renderSurfaceRMW`, add list-path sanitization for `readsHost` surfaces: strip entries declared with non-jail notches from `hostBytes`. |
| `internal/cli/apply.go` | Pass `render.KindHost` to `packoverlay.Collect` during `yolo host apply`. |
| `internal/entrypoint/packsurfaces.go` | Pass `render.KindJail` to `packoverlay.Collect` during in-jail provisioning. |
| `internal/cli/config.go` | Pass resolved target notch `t.notch` to `packoverlay.Collect`. |
| `internal/packload/footprint.go` | Disclose notch scope in `yolo pack footprint` and `yolo describe`. |

---

## 2. Planned Changes by Component

### 2.1 Manifest Validation (`internal/packdecl`)

1. **Fields on `Contribution`:**
   ```go
   // Notch is an optional single notch constraint ("jail", "host", "guest").
   Notch string `json:"notch,omitempty"`
   // Notches is an optional list of notch constraints (["jail", "host"]).
   Notches []string `json:"notches,omitempty"`
   ```
2. **Helper `NotchesDeclared() []string`:**
   Normalizes `Notch` and `Notches` into a deduplicated slice.
3. **Validation in `configListProblems` & `configOverlayProblems`:**
   Check each entry against `render.SelectableNotches`:
   ```go
   for _, n := range c.NotchesDeclared() {
       if !render.IsValidNotch(n) {
           problems = append(problems, fmt.Sprintf("%s: unknown notch %q (must be jail, host, or guest)", label, n))
       }
   }
   ```
4. Blocked on [OQ-1](notch-scoped-config-contributions.md#OQ-1) and [OQ-3](notch-scoped-config-contributions.md#OQ-3).

### 2.2 Overlay and List Collection (`internal/packoverlay`)

Update `Collect`:
```go
func Collect(packs []*packload.Pack, notch render.Kind, profiles map[string]string) *OverlaySet
```
- In Pass 2 (`config-overlay`):
  ```go
  if len(ov.Notches) > 0 && !contains(ov.Notches, notch.String()) {
      continue
  }
  ```
- In Pass 3 (`config-list`):
  ```go
  if len(cl.Notches) > 0 && !contains(cl.Notches, notch.String()) {
      continue
  }
  ```

### 2.3 `readsHost` List-Path Sanitization (`internal/entrypoint/prism.go`)

Blocked on [OQ-2](notch-scoped-config-contributions.md#OQ-2).

When preparing `hostBytes` for a `readsHost` stateful surface in a jail:
```go
func sanitizeHostListPaths(surface manifest.Surface, hostBytes []byte, packs []*packload.Pack) []byte
```
1. Extract list paths from selected packs targeting `surface.Key()`.
2. Collect values from list contributions where `notches` is non-empty and does NOT include `"jail"`.
3. If any host-only entries exist, parse `hostBytes` into an object, find arrays at the list paths, subtract matching entries, and re-serialize.
4. Pass the sanitized bytes into `t.ComposeStateful`.

---

## 3. Test Strategy

1. **Unit tests (`internal/packdecl`):**
   - Valid `notch: "host"` and `notches: ["host", "guest"]` decode cleanly.
   - Misspelled notch `"hosst"` is rejected with clear error.
   - Refused on kinds that do not support notch scoping.
2. **Collection tests (`internal/packoverlay`):**
   - `Collect` with `KindHost` includes host-only and unconditional entries; excludes jail-only.
   - `Collect` with `KindJail` includes jail-only and unconditional entries; excludes host-only.
   - Idempotency: multiple packs contributing the same entry to the same notch deduplicate.
3. **End-to-End composition tests (`internal/entrypoint`):**
   - Pack fixture contributing shared package `npm:shared-pkg` and host-only package `npm:@czottmann/pi-automode@1.17.0`.
   - Render at `KindHost`: both entries present in `pi/settings`.
   - Render at `KindJail` with `readsHost: true`: only `npm:shared-pkg` present; `pi-automode` is absent.
   - Verify `pi-settings.list-capture.json` in jail does NOT record `pi-automode`.
