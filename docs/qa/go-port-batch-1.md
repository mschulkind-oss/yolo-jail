# Go-port QA batch 1 — Stage 2 foundations parity audit

Findings from the adversarial parity audit (workflow `go-port-parity-audit`):
one agent per foundation package read the Go port AND the Python source of
truth, found byte/behavior divergences, and each finding was independently
verified by RUNNING both implementations. Only `real=true` (reproduced)
findings are listed. Each row is `fix` or `ledger` (accept + record in
`docs/design/go-port-divergences.md`).

| # | Package | Divergence | Input | Go | Python | Disposition |
|---|---|---|---|---|---|---|
| 1 | naming | `strings.ToLower` vs Python `.lower()` special-casing of U+0130 (Turkish İ) → different segments after sanitize | workspace basename `aİb` | `yolo-aib-…` | `yolo-ai-b-…` | **fix** — frozen interop contract (mixed-era jails must compute the same name) |
| 2 | jsonx | `Infinity`/`NaN`/`-Infinity` literals rejected on decode (Go can *encode* them but not round-trip) | `Decode("Infinity")` | error | `inf`→`Infinity` | **ledger (D1)** — corrected 2026-07-18: this row previously said "fix", but what shipped is the opposite (decode still rejects, asserted by a test) and the divergence was ledgered as D1, status *proposed* |
| 3 | jsonx | float overflow literal kept verbatim instead of →Infinity | `Decode("1e400")` | `1e400` | `Infinity` | **fix** — overflow →Infinity like Python |
| 4 | jsonx | negative-zero int literal round-trips as `-0` | `Decode("-0")` | `-0` | `0` | **fix** — `-0` integer → `0` |
| 5 | jsonx | lone surrogate replaced with U+FFFD on decode | `"\ud800"` | `"�"` | `"\ud800"` | **ledger** — encoding/json is lenient; no config carries lone surrogates; document |
| 6 | paths | `HOME` unset → relative paths (no pwd fallback); `HOME=""` → relative vs Python `/` | `env -u HOME` / `HOME=""` | `.local/share/yolo-jail` | `/home/agent/.local/…` or `/.local/…` | **fix** — add pwd/`getpwuid` fallback + empty-HOME→`/` |
| 7 | pytext | Go `unicode` 15.0.0 vs Python 15.1.0: code points newly-printable in 15.1.0 get escaped by Go | `"⿼"` etc. | `⿼` | literal | **ledger** — toolchain unicode-table version skew; config error strings don't use these; document |

## Disposition rationale

- **Fixes (1–4, 6)** are genuine parity bugs on inputs that can occur (naming
  is a frozen contract; jsonx round-trips arbitrary request/config bodies;
  paths is a core module and stripped-env is realistic). Each fix lands with a
  regression test and, where the corpus can pin it, a cross-language case.
- **Ledger (5, 7)** are accepted divergences: (5) lone surrogates never appear
  in real config/JSON bodies and encoding/json's substitution is a documented
  Go behavior; (7) the unicode-table version gap is a Go toolchain property, not
  something the port controls, and the affected code points don't appear in
  yolo-jail's validation error strings. Both recorded in the ledger with the
  reachability argument.

## Status

- [x] Findings verified (workflow, run `wf_9ca10408-20c`)
- [ ] Fixes 1–4, 6 landed with tests — **human review of dispositions requested**
- [ ] Ledger entries 5, 7 recorded
