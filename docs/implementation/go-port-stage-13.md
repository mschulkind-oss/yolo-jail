# Go-port Stage 13 — Config engine (handoff)

**Status:** landed. `internal/config` ports the delegated core of
`src/cli/config.py`; the byte-critical surfaces (snapshot writer, merge/dedup,
validation error strings) are cross-language-verified against the LIVE Python.
Config-consuming commands still delegate to Python (front door owns dispatch);
this stage delivers the library Stages 15 (`check`) and 16 (`run`) consume.
**Plan:** [`docs/plans/go-port-plan.md`](../plans/go-port-plan.md) Stage 13.
**Built on:** `internal/json5`, `internal/jsonx`, `internal/pytext`,
`internal/paths`, `internal/agents`.

## What landed

Commits (main):
- `0bc7411` feat(go): jsonx IsInt/AsInt integer inspection helpers for config
- `fd145f1` feat(go): internal/config loading + merge (config.py parity)
- `cc71a99` feat(go): jsonx AsIntLiteral + FormatFloatRepr for config repr strings
- `bea05fe` feat(go): config derived helpers, snapshot writer, env_sources, difflib
- `5ee3dbb` feat(go): config validator — _validate_config byte-for-byte
- `73850c0` test(go): config differential oracle + 243-case parity corpus
- `2e5e39d` test(go): strict bidirectional snapshot cross-write gate (Stage 13)
- `7207bff` test(go): config Go-native unit tests (loading, includes, snapshot flow)
- `f6c21a5` feat(go): hidden 'yolo internal config-dump' differential subcommand

Surfaces ported (all through `*jsonx.OrderedMap` — key order is load-bearing):
- **Loading/merge** (`load.go`): `LoadJSONCFile`, `LoadJSONCWithIncludes`
  (include chains + cycle detection via a shared seen set; `include_if_found`
  consumed+removed), `LoadWorkspaceConfig` (local-file auto-merge, shared seen
  so it isn't merged twice), `LoadConfig`, `MergeConfig`, `mergeLists`
  (agents replace-not-union; dict recursive merge; scalar/type-mismatch
  override).
- **Canonical dedup key** (`helpers.go` `dedupKey`): `json.dumps(item,
  sort_keys=True, default=str)` equality classes, via `jsonx.DumpsSnapshot`
  (the indent-only-different form induces the identical partition; `default=str`
  never fires for decoded config). Verified by a reordered-keys merge corpus
  case.
- **Snapshot writer** (`snapshot.go`): `SnapshotJSON` = `jsonx.DumpsSnapshot`
  (the byte-critical config-snapshot bytes); `ConfigSnapshotPath`;
  `CheckConfigChanges` with the **rstrip-compare asymmetry** and **non-tty
  auto-accept** preserved; `isTTY` + a `ChangePrompter` interface injected so
  the interactive branch is testable without a terminal.
- **difflib** (`difflib.go`): a faithful port of the difflib slice
  `_check_config_changes` uses (SequenceMatcher incl. autojunk, get_opcodes,
  get_grouped_opcodes(n=3), unified_diff) — verified byte-identical to Python.
- **env_sources** (`envsources.go`): `ParseDotenv`, `ResolveEnvSourcePath`
  (posixpath `expanduser` parity incl. empty/unset HOME), `ResolveEnvSources`
  (user-then-workspace concat resolved later-wins).
- **Validation** (`validate.go`, `validate_loopholes.go`): `ValidateConfig`
  reproduces `_validate_config` and every helper, appending errors/warnings in
  the **exact Python order** for every rejection branch. `{x!r}` rendered via
  `pytext.Repr` (strings) / `pyReprValue` (scalars/lists/dicts).
  `_report_unknown_keys` sorts the MAPPING keys. Loophole override vs
  inline-service dispatch via an injected `LoopholeResolver` (the loopholes
  registry is a separate Stage-14 port; nil = "no known loopholes", matching
  the `except OSError: {}` degrade path).
- **Derived helpers** (`derived.go`): `EffectivePackages`,
  `FilterMCPServersByEnv`, `EffectiveMCPServerNames` (preserves non-string
  preset entries), `SelectedAgents`, `MergeMiseTools`,
  `MergeMiseDisabledTools`, `NormalizeBlockedTools`.
- **Hidden subcommand**: `yolo internal config-dump [--strict] [ws]` — loads +
  merges + validates and prints canonical snapshot JSON + errors/warnings.
  Intercepted before the frozen argv rewrite; never in the Python
  `_SUBCOMMANDS` set; deleted at cutover.

## Verified (byte-parity vs LIVE Python)

- **Differential corpus** (`tools/parity/config_oracle.py` +
  `tools/parity/corpus/config_cases.json`, 243 cases;
  `internal/config/config_parity_test.go`): merged config (compact + snapshot
  forms), config-snapshot bytes, full ordered error/warning lists for every
  validation rejection branch, and every derived helper — all byte-identical.
  Loophole validation made hermetic via an injected known-loopholes spec.
- **Strict bidirectional snapshot cross-write gate**
  (`snapshot_crosswrite_test.go`) — the Stage 2 skip-with-reason test flipped to
  strict (a Stage 13 exit criterion). Python writes → Go reads unchanged (no
  prompt, bytes untouched); Go writes → Python reads unchanged. Both green.
- **Go-native units** (`config_test.go`): loading strict/non-strict, include
  chains + cycles, workspace local-file merge, and the full
  `CheckConfigChanges` control flow (first-run, unchanged, non-tty auto-accept,
  tty-yes updates, tty-no rejects + keeps old snapshot).
- `go build ./...`, `go vet ./...`, `staticcheck ./...`, gofmt-clean (tracked
  `*.go`), and `GOOS=darwin GOARCH=arm64 go build ./...` all clean.
- Mandated Python suites green: `tests/test_go_drift.py`,
  `tests/test_config_merge.py` (81 passed). Drift suite (`cmd/yolo-parity`)
  unaffected — no config surfaces were added to it.

## Divergences

**None proposed.** Every surprising Python behavior encountered was reproduced
exactly and is covered by the corpus, so nothing new was added to
`docs/design/go-port-divergences.md`:
- `_check_config_changes` rstrip-compare asymmetry + non-tty auto-accept —
  preserved.
- `int(str)`/bool-is-a-subclass-of-int in `forward_host_port` /
  `resources.cpus` / `resources.pids_limit` — preserved (`pyInt`, explicit bool
  handling).
- cwd-relative mount-path resolution (`Path(host_path).expanduser().resolve()`)
  — preserved; the parity test aligns cwd to repo root so it compares equal.
- non-string `mcp_presets` entries preserved through
  `EffectiveMCPServerNames`.

Pre-existing ledger entries **D1** (jsonx bare Infinity/NaN decode), **D2**
(lone-surrogate → U+FFFD), **D3** (pytext Unicode-table skew) still apply
transitively; none is reachable from real yolo-jail config values.

## Ready for downstream

`internal/config` is ready for the Stage 15 `check` port and the Stage 16 `run`
port to import: both need the loader (`LoadConfig`) + validator
(`ValidateConfig`) + `CheckConfigChanges`, and the derived helpers
(`SelectedAgents`, `EffectivePackages`, `NormalizeBlockedTools`,
`ResolveEnvSources`, `MergeMiseTools`, `MergeMiseDisabledTools`,
`EffectiveMCPServerNames`, `FilterMCPServersByEnv`).

One integration seam is stubbed for those stages to fill:
- **`LoopholeResolver`** — `ValidateConfig` takes a resolver for the known
  file-backed loophole set. Stage 14's `internal/loopholes` port must supply a
  real one (`discover_loopholes(include_disabled=True)` → name→{HasHostDaemon}).
  A nil resolver = empty known set (the in-jail / OSError-degrade path); the
  loophole-override validation branches are corpus-verified against a hermetic
  injected set.

## Human actions / UNVERIFIED

- CI (§10.7) both arches — the in-jail agent cannot confirm; the parity + cross-
  write gates are the CI-runnable proof.
- No nested-jail step required: config-consuming commands still delegate to
  Python (the library isn't wired into a native command until Stage 15/16).
