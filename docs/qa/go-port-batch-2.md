# Go-port QA batch 2 — Stage 9/10 entrypoint generators parity

Findings from porting the `src/entrypoint/*.py` PURE content-generators to Go
(`internal/entrypoint`) and building the Stage 9 tree-diff/sha256 golden harness
(`tools/parity/entrypoint_oracle.py` + `internal/entrypoint/entrypoint_parity_test.go`).
Every generated surface was byte-verified sha256-identical to the LIVE Python
generators over a committed 9-scenario env matrix (`tools/parity/entrypoint_matrix.json`).

QA batch docs *propose* entries; approved ones are recorded in
`docs/design/go-port-divergences.md`. Status legend: **proposed** · **accepted**.

## Verified byte-parity (no divergence)

Sha256-identical across the matrix (HOME-prefix normalized to `@HOME@`):

- shims (blocked-tool argv-filter matrix, incl. message/suggestion + exit 127),
  agent lazy-launchers (npm + native), pnpm package-manager launcher
- `.bashrc`, `.yolo-bootstrap.sh`, `.yolo-venv-precreate.sh`
- `yolo-cglimit`, `yolo-journalctl`, `yolo-ps`, the `yolo` shim + `_yolo_bootstrap.py`
- MCP wrappers (chrome-devtools / node / npx)
- mise `config.toml` (fresh / dedup / retired / system / injected / ws surgery)
- all six agents' configs + managed-MCP sidecars — including codex's
  `~/.codex/config.toml` (TOML round-trip via `tomlx.DecodeOrdered`), claude's
  three-way host-settings merge, credentials symlink + harvest (the one
  sanctioned tmp+rename, 0o600), and history-isolation symlink
- CA bundle (`.yolo-ca-bundle.crt`)
- `bash -n` clean on every generated shell script; the shim argv-filter contract
  verified behaviorally (executed, not just byte-compared)

## Proposed notes / edge-behavior (candidate ledger entries)

| # | Surface | Behavior | Input | Go | Python | Disposition |
|---|---|---|---|---|---|---|
| E1 | agent configs | An existing config file that is valid JSON but NOT a top-level object | e.g. `~/.gemini/settings.json` = `[1,2]` | Go treats it as `{}` (declines to act) | Python `current = json.loads(...)` keeps the list, then `.setdefault`/item-access raises → caught → writes nothing (gemini) OR raises at `current.setdefault` (copilot has no guard) | **note, not ledgered** — no real agent config is a non-object; both effectively don't corrupt; behavior on this unreachable input differs only in which no-op path is taken |
| E2 | codex/mise TOML | A user config table nested beyond what the writer models (non-`mcp_servers` table) | `[some.other.table]` in `~/.codex/config.toml` | dropped with the same stderr warning Python emits | dropped + warning | **matches** (both drop; warning text identical) |
| E3 | credentials harvest | `expiresAt` as a non-numeric string | `"expiresAt":"abc"` | `expiresAtMs` → 0 (never newer) | Python `int("abc")` raises → caught → 0 | **matches** (both → 0) |

## Harness hermeticity fix (not a port divergence — a test-infra correctness fix)

The tree oracle initially inherited the dev jail's real environment. The jail
exports a live `TAVILY_API_KEY`, which the Python generators read via
`os.environ` while the Go generators read only their explicit `*Env` matrix —
so the `requires_env` gate on a `tavily` MCP server flipped on the Python side
alone, producing a spurious tree diff. Fixed by making
`entrypoint_oracle.py` clear all env except a system safelist before applying
the matrix, so both sides see the identical minimal environment. This is the
kind of env-leak the plan warns about; captured here so the exclusion discipline
is deliberate.

## Dynamic-output normalization list (committed, plan §9)

The pure generators emit no dynamic content. The dynamic surfaces the FULL boot
would touch — deliberately NOT invoked by the oracle — are enumerated in the
`entrypoint_oracle.py` docstring: `~/.yolo-perf.log` (wall-clock timings),
`~/.cache/yolo-agent-stamps/` (launcher run-time stamps), `/workspace/.yolo/startup.log`
(provisioning timestamps), and shell/agent history files. Note
`~/.claude/history.jsonl`'s symlink TARGET is deterministic from the matrix
(`sha256(YOLO_HOST_DIR)[:12]`) so it IS asserted (as a symlink); only its
session-data contents are never generated here.

## Scope deferred to Stage 10 orchestration/boot (NOT content, correctly out of scope)

Side effects the generators trigger in Python but that are boot sequencing, not
file content — deferred to the Stage 10 orchestration + image-wiring sub-phase:

- `mise uninstall --all <tool>` subprocesses (mise.py)
- `claude plugins install/uninstall` subprocesses (agent_configs `_install_claude_plugins`)
- `git`/`jj config` identity subprocesses (identity.py) — pure side effect, no file this package owns
- `ldconfig` / timezone `/run` symlinks (system.py `generate_ld_cache` / `configure_timezone`)
- `os.environ` mutation by `generate_ca_bundle` (the FILE is ported; the env
  export is boot's job — `GenerateCABundle` returns the path for the caller)
- port-forwarding, supervisor spawn, `mise trust`, `exec_bash`, perf logging,
  `main()` ordering, user-env hydration into os.environ

## Status

- [x] All 9 matrix scenarios byte-identical (tree golden green, Python-vs-Go)
- [x] `bash -n` clean on all generated shell; shim contract behaviorally verified
- [x] `go build`/`vet`/`staticcheck`/`gofmt`/darwin-cross all clean; Python suite green
- [ ] Human review of E1–E3 dispositions (proposed as notes, none require a ledger entry)
