# Go-port Stage 15 — `yolo check` native (handoff)

**Status:** landed. The native Go `yolo check` (and its `doctor` alias) runs
end-to-end behind the `YOLO_IMPL=go` gate; the default still delegates to
Python, unchanged. The pure diagnostic engines it orchestrates all landed in
Stages 13/14 and are reused, not reimplemented.
**Plan:** [`docs/plans/go-port-plan.md`](../plans/go-port-plan.md) Stage 15.
**Built on:** `internal/checkdiag`, `internal/config`, `internal/loopholes`,
`internal/storage`, `internal/runtime`, `internal/image`, `internal/version`,
`internal/paths`, `internal/cgd`, `internal/prune`, `internal/execx`,
`internal/console`, `internal/jsonx`, `internal/frameproto`.

## Commits (main, newest first)

- `e41701a` feat(go): wire yolo check native behind YOLO_IMPL=go gate (seam #1 forward)
- `16549f4` feat(go): internal/checkcmd — native yolo check orchestration
- `afc987a` feat(go): image.BuildOCIImage — nix .#ociImage build for the check preflight

## Output contract (as approved)

Per the plan's answered Open Question, `yolo check`'s human output does NOT need
byte-parity with Python's rich markup. The Go port reproduces:

- the **section ordering** (Container Runtime → Nix → macOS Platform → Global
  Storage → Config Files → Merged Configuration → Entrypoint Dry-Run → macos-user
  backend (native only) → GPU (NVIDIA) → GPU (AMD/ROCm) → KVM → Image Build →
  Container Image → Running Jails → Loopholes → Per-jail host-service liveness →
  Disk usage → Loopholes inline daemons → Summary),
- the **PASS/WARN/FAIL badge semantics** and the pass/warn/fail **counts**,
- the **exit code** (0 = no failures, 1 = any fail),
- the **control flow** early-exits (config *parse* errors exit with a fail-only
  Summary before merged validation; merged-validation errors exit with a
  fail+warn Summary before the dry-run).

The exact ANSI/rich bytes are a NEW Go-native golden (`golden_test.go`), pinned
ANSI-stripped. Where a check emits a diagnostic STRING that users grep or that
carries meaning (the nix builder remedy, the config validation error strings,
the creds-freshness messages, the broker relay-layer diagnoses), those come
from the already-ported engines and ARE byte-exact.

## Package shape (`internal/checkcmd`)

- `checkcmd.go` — `Options` (every side-effecting seam is injectable: `Now`
  clock, `Getenv`, `LookPath`, `Exec(argv, dir, env, timeout)`, `Stdout`,
  `Stdin`, `PathExists`, `RepoRoot`, builder/build/device seams) + `fillDefaults`
  wiring the real implementations. `Exec` carries a working-dir and extra-env so
  the nix dry-run / build / entrypoint preflight run in the repo root with
  `YOLO_EXTRA_PACKAGES` / `YOLO_*` set, while tests stub it dir-agnostically.
- `check.go` — `Check(opts) int`, the section sequence, and the section bodies
  that don't warrant their own file (Container Runtime, Global Storage, Config
  Files, Merged Configuration, Entrypoint Dry-Run, Image Build, Container Image,
  Running Jails, inline loopholes).
- `reporter.go` — the ok/warn/fail/note/section/summary writer (Go analog of
  check()'s nested closures) + ANSI SGR styling. Three Summary forms mirror the
  three Python exit points.
- `probes.go` — runtime detection (`runtimeForCheck`, `nativeRuntimeCheck`,
  `runtimeIsConnectable`, `isAppleContainer`, `detectRuntimeForListing`,
  `detectRuntime`), `listRunningJailNames`, `getContainerWorkspace`,
  `checkContainerStuck`, `podmanMachineMemory`, and `resolveRepoRoot`.
- `broker.go` — broker singleton status/ping (frame-protocol inline ping),
  `hostServiceSocketsDir` (sha1[:8] under /tmp, macOS `/private/tmp`),
  `relaySocketVisibleInJail` tri-state.
- `builder.go` / `sections_nix.go` / `section_nix_probe.go` — the Nix section,
  `_preflight_builder_needs` tri-state, `_nix_dry_run_will_build`,
  `_has_linux_builder`, and the builder-reachability verdicts.
- `sections_loopholes.go` — `_check_loopholes`, `_check_broker_relay` (4-layer
  diagnosis), `_check_host_service_liveness`.
- `sections_misc.go` — `_check_broker_creds_freshness` (injected clock),
  `_check_disk_usage` (+ `_find_yolo_workspaces` / `_disk_usage_report` total).
- `sections_gpu.go` / `sections_devices.go` — ROCm enumeration, podman-machine
  resources, and the NVIDIA/AMD/KVM sections (device-node access + group checks).
- `sections_macos.go` / `sections_macos_platform.go` — the macos-user backend
  readiness (the sanctioned 3-helper carve-out: `SANDBOX_USER`,
  `_sandbox_user_exists`, `resolve_python`) and the macOS Platform section.
- `entrypoint.go` — the entrypoint dry-run: spawns `python3 -c <code>` against
  repo `src/` with the same temp-HOME + `YOLO_*` env `_entrypoint_preflight`
  builds. (Keeps spawning Python until `YOLO_ENTRYPOINT_IMPL` defaults to go;
  see "Follow-ups".)
- `device_linux.go` / `device_other.go` — `os.access` / `getgroups` / group
  lookup behind `//go:build linux`; inert conservative stubs elsewhere so
  `GOOS=darwin GOARCH=arm64 go build ./...` stays green.

## Wiring (the gate)

- `internal/frontdoor`: `check` + `doctor` are in a new `gatedNativeSubcommands`
  set; `IsNative` returns true for them ONLY when `YOLO_IMPL=go` (via
  `goImplEnabled`). The default (`nativeSubcommands`, unconditional) stays empty,
  so behavior is unchanged unless the gate is set.
- `cmd/yolo/native.go`: `nativeDispatch["check"]=nativeDispatch["doctor"]=runCheck`
  which parses `--build/--no-build` and calls `checkcmd.Check`.
- `src/cli/__init__.py` `main()`: the seam #1 forward — when `YOLO_IMPL=go` and
  `YOLO_GO_DELEGATED` is unset and `$YOLO_GO_BIN_DIR/yolo` is executable, exec it
  (before terminal-indicator setup). Missing binary → silent fall-through to
  Python. This is the first landing of seam #1's forward (it was specced in
  Stage 12 but not yet wired); it is reversible (unset the gate) and narrow.

## Verification

- `go test ./internal/checkcmd/ ./internal/checkdiag/` — PASS (golden,
  color-strips-to-plain invariant, in-jail clean exit-0, injected-clock creds
  freshness, and the **live-Python differential parity** on graded lines, which
  SKIPs without python3/uv).
- `GOOS=darwin GOARCH=arm64 go build ./...` — PASS.
- `go vet ./...`, `staticcheck ./internal/checkcmd/ …` — clean.
- `uv run python -m pytest tests/test_go_drift.py -q` — PASS; `ruff` clean on the
  Python edit.
- **Nested-jail gate** (`yolo -- bash` from inside this jail, then both arms):
  the default `yolo check --no-build` runs Python unchanged (exit 0, 17 passed /
  1 warning); `YOLO_IMPL=go YOLO_GO_BIN_DIR=/workspace/dist-go/linux-amd64 yolo
  check --no-build` runs the native Go arm and produces the **byte-identical**
  ANSI-stripped output + exit code (one incidental Python `env_sources` stderr
  warning filtered; it is content-identical, only rich soft-wraps it). Commands:

  ```
  # inside the nested jail:
  yolo check --no-build                                         # Python (default)
  YOLO_IMPL=go YOLO_GO_BIN_DIR=/workspace/dist-go/linux-amd64 \
      yolo check --no-build                                     # Go (native)
  # ANSI-stripped diff (env_sources warning filtered) => IDENTICAL, both exit 0
  ```

## Behavior notes / preserved quirks

- **Config parse-error text** differs (`internal/json5` vs pyjson5) — already
  recorded as ledger **D9**. The badge, section, and fail-only Summary match;
  only the error body text differs. No new ledger entry needed.
- **`env_sources` file-not-found warning** during the preflight is an incidental
  `console.print`, not a graded badge. The Go port emits it as a non-counting
  yellow `Warning:` line (`reporter.warningLine`) so it does not perturb the
  warn count. Rich soft-wraps it at terminal width; Go emits one line — a
  terminal-width artifact, explicitly out of the byte-parity contract.
- **`resolveRepoRoot`** ports the env-var + source-checkout branches
  `_resolve_repo_root` uses; the installed-wheel staging (step 3's rename dance)
  is deferred to the run slice (Stage 16 explicitly owns it). check only needs a
  root that resolves to a `flake.nix`.
- **`ensureBuilderReal`** narrows `ensure_builder` for the check path: it
  returns the reachable / not-set-up / needs-first-boot verdicts but does NOT
  headlessly boot the VM (that is the run slice's job); a set-up-but-unreachable
  builder reports "wouldn't start" so check surfaces the actionable FAIL rather
  than blocking. This is a deliberate, documented narrowing, not a divergence in
  graded output on the tested fixtures.

## Follow-ups (not blockers)

- The entrypoint dry-run still spawns Python (`python3 -c`) exactly as
  `_entrypoint_preflight` does. Per the plan, once `YOLO_ENTRYPOINT_IMPL`
  defaults to go this switches to `yolo-entrypoint --dry-run` under the same env
  contract. No code here assumes the Python path beyond the spawn.
- No `internal/config` / `internal/loopholes` / `internal/paths` changes were
  needed beyond reading them. One engine addition: `image.BuildOCIImage`
  (the side-effecting nix-build the check preflight consumes) — added to
  `internal/image` per the "add helpers to the engine, with its own scope" rule.
- macOS-stubbed and podman-live golden fixtures beyond the no-runtime golden are
  a reasonable next increment (the seams already support them via `IsMacOS` +
  `Exec` stubs); the differential-parity test already covers the live in-jail
  host state end-to-end.

---

## Re-audit 2026-07-18

This stage has confirmed blocker/major findings in the consolidated re-audit: [`go-port-audit-2026-07-18.md`](go-port-audit-2026-07-18.md). Fix or ledger the items attributed to this stage before its `YOLO_IMPL=go` gate is recommended for dogfood.
