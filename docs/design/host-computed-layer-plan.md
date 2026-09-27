# What a jail derives, the host leaves empty — implementation sketch

**Status:** SKETCH, 2026-09-27 — incomplete, and unstable while [OQ-HC1](host-computed-layer.md#OQ-HC1),
[OQ-HC2](host-computed-layer.md#OQ-HC2) and [OQ-HC3](host-computed-layer.md#OQ-HC3) are open.
Evidence read at `b0460995`. Nothing here is built.

> **Precedence.** This is the implementation sketch beside
> [`host-computed-layer.md`](host-computed-layer.md). The design wins on every behavior, and
> nothing here makes a design decision. Do not build from this file while it is stamped SKETCH;
> `implementation-plan` owns what it must become first.

---

## 1. Fixes that wait on nothing

Symbols, not lines. Each row is a decision recorded in the
[design's ledger](host-computed-layer.md#12-decision-ledger).

| # | Decision | Where | Notes |
| :--- | :--- | :--- | :--- |
| 1 | [HC-D1](host-computed-layer.md#HC-D1) | `packs/pi/pack.json`, the `pi/models` config entry | Add `"defaults": {"providers": {}}`. The regression test renders `pi/models` for a pi-only pack set through the jail boot (`ConfigurePackSurfaces`) and through `RenderHostPack` into an empty home, and asserts an object-valued `providers`. It must fail with the default removed. No pi CLI: pi's `ModelsConfigSchema` requirement is the one fact the test restates, so its comment names pi's version and file |
| 2 | [HC-D2](host-computed-layer.md#HC-D2) | `mcpEntryRemedy` and `mcpEntryRemedyKey` in `internal/cli/hostapplyremedy.go`; its three callers in `internal/cli/apply.go` and the remedy group | Every caller is host apply, so no notch parameter is needed today. The text names a per-surface `config-overlay` in `~/.config/yolo-jail/local` |
| 3 | [HC-D3](host-computed-layer.md#HC-D3) | `internal/cli/config_ref.txt`, the host-notch `provider` line | Text only |
| 4 | [HC-D4](host-computed-layer.md#HC-D4) | The in-sync count in `internal/cli/hostapplyverdict.go`, and whatever predicate feeds it from `RenderHostPack`'s change detection | Check first whether the predicate compares the rendered bytes with an absent file's empty read |
| 5 | [HC-D5](host-computed-layer.md#HC-D5) | `tableLosses` in `internal/entrypoint/hostrender.go`, which predicts losses without the mechanism; `render.GeneratedHeader` for the `own` header | `GeneratedHeader` takes a surface, not a target, so the host spelling needs the target passed in |

## 2. If the host derives, by B1

Blocked on [OQ-HC1](host-computed-layer.md#OQ-HC1).

- **The registration option** ([HC-D9](host-computed-layer.md#HC-D9)).
  `GopherLuaVM.DeriveRegistrations` (`internal/agentcfg/luahook/derive.go`) records it;
  `packload.DerivedSurfaces` returns it beside the surface key; the host census and
  `yolo config ls --at host` read it. A script passing the option to an older runtime must not
  fail: check whether the Lua binding rejects a fourth argument.
- **One provider composition per invocation.** `composedHostProviders` lives in `internal/cli`, and
  `RenderHostPack` sees one pack at a time, which is why it already takes the cross-pack
  `overlays` as a parameter. The provider table is a second parameter of the same kind, composed
  once in `applyHost` over every selected pack. Reuse, do not copy, `composedHostProviders`.
- **The input tables** ([HC-D6](host-computed-layer.md#HC-D6)). `Env.mcpServersWith` builds the
  jail's table from `YOLO_MCP_PRESETS` and `YOLO_MCP_SERVERS`; the host needs the user-scope
  `mcp_servers` with no preset map, and a `requires_env` lookup over `composeHostVars`' result.
- **The key-name probe.** `hostTableKeys` stays for undeclared surfaces. For a host-derivable
  one, the real derive's `inFull` names its tables, so the sentinel probe is not run.
- **The confirmation gate** ([HC-D8](host-computed-layer.md#HC-D8)). `confirmHostLosses` fires on
  `FirstApply && EntryLosses`; "first apply" becomes per table key for a key the provenance record
  does not list as yolo's in this home.
- **Tests.** The jail-path test of
  [§6.7](host-computed-layer.md#67-what-done-looks-like) item 4 renders every declared surface at
  the host with a preset enabled and greps the output for the jail prefixes. It must fail when the
  preset is let through. `TestTheHostNotchLeavesPiOnItsOwnCodexCatalog` inverts. The launch-path
  rule applies: run `go test ./integration` as well as `-short`.
- **UNMEASURED:** whether `composeStatefulSurface` encodes yaml at the host, which
  `oh-omp/models` needs under [OQ-HC2](host-computed-layer.md#OQ-HC2)'s leaning.

## 3. If `own` renders `computed` through `stateful`

Blocked on [OQ-HC2](host-computed-layer.md#OQ-HC2). `render.HostOwnedModes` moves `computed` from
`excluded` to a coercion onto `stateful`. The census's `Mechanism` fallback needs a sole composing
mechanism and this census has two, so the coercion is explicit rather than the fallback. Re-check
`hostStatefulRefusal` for the keyless carve-out [OQ-CO9](config-ownership-and-promotion.md#13-decision-ledger)
keeps.

## 4. If host apply selects the `use_profiles` variant

Blocked on [OQ-HC3](host-computed-layer.md#OQ-HC3). `effectiveHostProfiles(cfg, "", "")` over the
user-scope config is the selection source. The jail's selection record and its per-key deselect
rule need a host home: beside the provenance record under `render.Target.ProvenanceDir`.
