# Go-port Stage 2 — Foundations + drift suite (handoff)

**Status:** foundation packages + drift suite landed and green. json5 spike (Spike A)
in progress (delegated). Spike B (tty prototype) not started.
**Plan:** [`docs/plans/go-port-plan.md`](../plans/go-port-plan.md) Stage 2.

## What landed

| Package | Commit | Parity gate |
|---|---|---|
| `internal/paths` | 13331aa | drift suite (constants) |
| `internal/version` | 13331aa | table test + drift; `0.1.0+dirty` byte contract; YOLO_VERSION verbatim |
| `internal/agents` | 13331aa | drift suite (full registry, order-preserving) |
| `internal/naming` | 13331aa | drift suite (sanitize+hash over resolved paths) |
| `internal/jsonx` | 13331aa | 27-case cross-language corpus vs live Python `json.dumps` |
| `internal/shquote` | 7ea2562 | golden + cross-language oracle over adversarial argv |
| `internal/pytext` | 7ea2562 | golden + cross-language oracle vs Python `repr()` |
| `internal/console` | 7ea2562 | ANSI-stripped badge/note goldens |
| `internal/frameproto` | 7ea2562 | round-trip + exact-bytes tests (conformance in Stage 4) |
| `internal/execx` | 7ea2562 | tri-state liveness tests (EPERM=alive) |
| `internal/fsx` | 7ea2562 | inode-preservation / clear-contents / relative-symlink tests |
| `cmd/yolo-parity` drift suite | ef5073e | `tests/test_go_drift.py` byte-diff in check-ci |

## Verified

- `go test ./internal/...` green; `-race` clean on the concurrent packages.
- Cross-language parity (all skip gracefully without Python; CI has it):
  jsonx (incl. float-repr boundaries, astral surrogate pairs, control chars),
  shquote (metachars, null bytes, unicode), pytext (repr quote-selection,
  control chars, emoji).
- **Drift suite proven bidirectional**: `tests/test_go_drift.py` PASSES today
  (Go dump byte-identical to Python), and goes RED when a Python constant
  (`JAIL_IMAGE`) is perturbed, GREEN again when restored — the freeze-rule
  tripwire works. `just parity drift` runs the same comparison.

## Divergences found by the Stage-2 adversarial parity audit (workflow)

An adversarial audit (one agent per package reading Go + Python, each finding
independently verified by running both sides) surfaced confirmed byte/behavior
divergences. Status tracked in `docs/qa/go-port-batch-1.md`. Fixes land as
`fix(go): ...` commits with a regression test each; ledger-accepted ones go in
`docs/design/go-port-divergences.md`.

## In progress / next

- **Spike A (`internal/json5`)**: delegated — differential oracle vs pyjson5,
  comments + trailing commas mandatory, exotic JSON5-isms may be ledger-accepted.
  Gates the config-consuming commands and the host-processes daemon (Stage 5).
- **Spike B (Go tty prototype)**: NOT started — needed before Stage 8, not
  before the current work.

## Human actions needed

- CI confirmation (§10.7): push and confirm the Go half of `check` (build/vet/
  staticcheck/gofmt + `go test -short`) and `tests/test_go_drift.py` pass on
  both arches. The in-jail agent has no push credentials or CI visibility.
