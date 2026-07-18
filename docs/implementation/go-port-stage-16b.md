# Stage 16b — macOS backend port (as-is)

**Status:** LANDED (Go side; front-door wiring is orchestrator-owned — see "Wiring left to the orchestrator").
**Scope delivered:** `src/cli/macos_user.py` → `internal/macosuser`; `src/cli/builder.py` VM-lifecycle + `src/cli/builder_cmd.py` command bodies → `internal/buildercmd` (scope addition, 2026-07-18). `darwin_packages.py` and `container_builder.py` verified already ported (Stage 14) and reused, not re-ported.
**Directive:** port the CURRENT state as-is (plan §13 / §1.1), including known-broken/unfinished paths. No macOS bug fixes in these commits.

## Commits (this stage)

- `feat(go): Stage 16b — internal/macosuser pure artifact producers`
- `feat(go): Stage 16b — macosuser orchestrator + macos-* command bodies`
- `feat(go): Stage 16b — internal/buildercmd (on-demand macOS Linux builder)`

(Run `git log --oneline` for SHAs — they are on `main`, never amended.)

## What landed

### `internal/macosuser`
Full port of `macos_user.py`:
- **Pure artifact producers** (byte-goldened vs live Python): `SeatbeltProfile` (SBPL, last-match-wins, `_sbpl_str` escaping), `CreateUserCommands`/`DeleteUserCommands`/`SharedRootProvisionCommands`/`StageEntrypointCommands`/`BrokerSocketGrantCommands` (dscl/dseditgroup/chmod argv), `WorkspaceACLAces` + `FixPermissionsScript` + `WorkspaceACLStripScript` (inheriting-ACL ACE strings + `find`-based scripts), `SandboxPath` + `LaunchArgv` (`sudo -u … env -i … sandbox-exec`, env -i ordering, PATH join order, workspace-centric `cd … && exec`), `EntrypointBootstrapScript` (the generated Python bootstrap — repr escaping + nested `json.dumps(json.dumps())` for `YOLO_AGENTS`, the login rc-file re-prepend after `path_helper`), `MacosLogWrapperScript`, `PythonCandidates`/`ResolvePython`, `NextFreeID`, `HomeContaining`.
- **RunPlan builder + invariants**: `BuildRunPlan` / `PlanInvariants` reproduce the `RunPlan` dataclass field-for-field, including the darwin `packages:` threading into the launch PATH and the wiring-bug guard. Reuses `internal/config` (`NormalizeBlockedTools`, `MergeMiseTools`, `EffectivePackages`, `ResolveEnvSources`) and `internal/naming` for the config-derived bootstrap env + cname.
- **Orchestrator + command bodies**: `RunMacosUser` (frozen ordering preserved — see below), `MacosSetup`/`MacosTeardown`/`MacosUnshare`/`MacosFixPermissions`, `refuseIfRoot`. Every subprocess/platform probe is an injectable `Deps` seam; `RealDeps(runProxy, materialize)` wires production. `MacosSandboxEnv` reproduces the git-identity+TERM env with insertion order.

### `internal/buildercmd`
Port of `builder.py`'s VM lifecycle + `builder_cmd.py`'s command bodies:
- `BuilderSetupState`/`BuilderStatus`/`confPath`/`nixConfHasBuilder`, `EnsureBuilder` (exact branch order + reason strings: `not macOS` / `not set up` / `needs first-boot`), `pollUntilReachable` (dead-child short-circuit + final re-check), `RunSetup`, `FirstBootInteractive`.
- Command bodies `BuilderStatusCmd`/`BuilderStartCmd`/`BuilderStopCmd`/`BuilderSetupCmd` + `RunBuilder(deps, sub, args)` clean entry + `parseSetupFlags`.
- Reuses `internal/builder` pure generators (`SSHConfigBlock`/`NixBuildersLine`/`TrustedUsersLine`/`SetupRootScript`) and `internal/storage` probes (`NixCustomConfIncluded`/`DetectNixDaemonLabel`). `RealDeps()` wires the TCP socket dial, PID-file lifecycle, `killpg` stop, and `nix run` spawn.

### `internal/builder` (additive only, with tests)
Added `SSHConfigPath()`, `BuilderPIDFilePath()`, `BuilderLogFilePath()` — exported accessors the buildercmd bodies need. No behavior change to the existing generators.

## Frozen semantics preserved (verified)

- **`_refuse_if_root`**: `refuseIfRoot` + the euid-0 gate in `RunMacosUser` refuse under sudo BEFORE any subprocess (tests assert zero shell-outs). The module self-escalates via internal sudo; running as root would misassign the shared-group ACL to `root`.
- **`run_macos_user` ordering**: dry-run builds+prints the plan and RETURNS before the macOS/root gates (so it runs on Linux CI); cheap preconditions (sandbox-exec, sandbox user) run BEFORE the up-to-30-min nix build; the plan is built AFTER the gates (it reads host git config). Reproduced exactly.
- **Swallowed-exception leniency in `_resolve_env_sources`**: `buildPlan` merges env_sources through `config.ResolveEnvSources` with a no-op warn and never fails the plan on a bad entry.
- **`_sh_quote` vs `shlex.quote`**: `shQuote` always wraps + uses `'\''` escaping (NOT shlex.quote); `_sbpl_str` escapes backslash-then-quote. Both pinned by the differential.
- **SBPL last-match-wins ordering** and **PATH join order** (shims → darwin store dirs → system): byte-pinned.

## Parity gate results (the whole point of this backend)

`go test ./internal/macosuser/` and `./internal/buildercmd/` — **PASS** (Linux, with live Python present):

- `TestParityVsLivePython` (macosuser): every pure producer byte-identical to live `macos_user.py`.
- `TestDryRunArtifactParity` (macosuser): the **full RunPlan artifact dump** — SBPL text, sudo argv lists (stage commands + bootstrap argv), the generated bootstrap script, the launch argv, `plan_invariants` output, and darwin threading — diffed Go-vs-live-Python across a **5-fixture matrix** (minimal, full-config-with-darwin, unresolved-interp, home-workspace-violation, tricky-path with quote/backslash). **Byte-identical.**
- `TestSetupStateParityVsLivePython` (buildercmd): `builder_setup_state` diffed vs live `builder.py` across a 7-fixture file matrix (none / ssh-only / wired-no-key / fully-wired / commented-builder / wrong-host / custom-conf).
- `internal/builder` `TestGeneratorsParity` still green (setup_root_script etc.).

All parity tests **SKIP** (not fail) when Python is absent, so the Python-free dev path still works; CI always has Python.

### Build results
- `go build ./internal/macosuser/ ./internal/buildercmd/ ./internal/builder/` — green on both `GOOS=linux` and `GOOS=darwin GOARCH=arm64`.
- `GOOS=darwin GOARCH=arm64 go build ./...` — **green** once the orchestrator's in-progress `internal/runcmd/run.go` + `cmd/yolo/native.go` edits are complete (verified green with those two WIP files set aside; the failures there are undefined symbols in the orchestrator's own new banner code, unrelated to Stage 16b).
- `gofmt`/`go vet`/`staticcheck` clean on all three packages.

## Bootstrap-vs-entrypoint decision (gates Stage 17 entrypoint deletion)

**Decision: the Go macos-user path emits the SAME Python bootstrap script, byte-identical, that imports and runs the Python `entrypoint` package.**

Rationale:
- Ground rule §1.1 (port AS-IS): the Python backend generates a Python bootstrap that `sys.path.insert`s the staged `entrypoint/` and calls `entrypoint.generate_shims()` etc. Emitting anything else would be a behavior change, not a port.
- The bootstrap runs AS the sandbox uid via `sudo -u … python3 <script>`, importing the root-owned staged copy under `/var/yolo-jail/entrypoint`. `StageEntrypointCommands` still `cp -R`s `src/entrypoint/.` there.
- **Consequence for Stage 17:** this path keeps the Python `entrypoint` package alive on macOS. Stage 17's deletion of `src/entrypoint` is therefore NOT unblocked by 16b alone — the macos-user bootstrap must first be repointed at the Go entrypoint (`cmd/yolo-entrypoint`, ported in Stage 10) as a deliberate, separately-verified change. That repoint is a **follow-up** (a `fix(macos):`/`refactor(go):` commit with its own Mac-runbook verification), explicitly out of scope for the as-is port. The rest of the Python subtree that the delegation seam pinned (typer, pyjson5, rich, config, run_cmd, loopholes) IS released by 16b, since the Go run path now owns macos-user dispatch.

This is called out so Stage 17 does not delete `src/entrypoint` assuming 16b freed it.

## Deliberately-reproduced as-is / known-broken behaviors

Per §1.1, these are ported faithfully (broken stays broken; repairs are separate `fix(macos):` commits, none made here):

1. **`run_macos_user` docstring claims a passwordless-sudo + interpreter-runnable-as-sandbox precondition that the code does NOT check.** The real code only checks macOS, euid≠0, `sandbox-exec`, and sandbox-user existence before the nix build; there is no passwordless-sudo probe and no "runnable as the sandbox user" probe. Ported the CODE (§1.10 source-wins), not the docstring.
2. **`_bootstrap_argv` uses the last python candidate (`/usr/bin/python3`, the xcode-select stub) as the argv interpreter when none resolves.** `BuildRunPlan` sets `interpStr = pythonCandidates[-1]` when `interp` is unresolved so the plan is still printable; `plan_invariants` flags it, but the argv itself carries the stub. Reproduced.
3. **Darwin `packages:` store profile has no GC root** (darwin_packages.py review #12): a `nix-collect-garbage` mid-session could reap the store path the baked PATH points at. The Go port threads the same store bin dir with no GC root. Unchanged (a per-workspace indirect GC root is the documented future fix).
4. **`builder start` first-boot path is interactive/unverified on real hardware** (OQ pending): `FirstBootInteractive` runs `nix run nixpkgs#darwin.linux-builder` in the foreground and treats a SIGINT-terminated child as success if the key installed. Ported as-is; real-Mac verification stays the runbook step.
5. **`path_helper` login-rc fix (OQ-1) is unverified on a Mac.** The bootstrap writes `.zprofile`/`.zshrc`/`.bash_profile` re-prepending the sandbox PATH after macOS `path_helper`. Cannot be tested on Linux (no path_helper). The Go port reproduces the rc-file writes byte-for-byte and goldens them (`TestDryRunArtifactParity` includes the bootstrap with `path_prefix`); the runtime effect stays the Mac-runbook gate.
6. **`builder status`/other rich output is width-wrapped by rich in Python but not by the Go printer.** Parity is defined on TEXT CONTENT (the runcmd/checkcmd precedent), not on rich's terminal-width line-wrapping; the artifact byte-contract (SBPL/argv/bootstrap) is separate and IS byte-pinned. Non-artifact human chatter reproduces the text, drops the markup, and does not re-wrap.

None of these were repaired.

## Wiring left to the orchestrator (NOT done here, by task constraint)

The task reserves `cmd/yolo/native.go` and `internal/frontdoor/frontdoor.go` for the orchestrator. `internal/macosuser` and `internal/buildercmd` expose clean importable entrypoints for one-line handlers:

- `macosuser.RunMacosUser(deps, opts) int` + `macosuser.RealDeps(runProxy, materialize)` + `macosuser.Options{...}`.
- `macosuser.MacosSetup/MacosTeardown/MacosUnshare/MacosFixPermissions(deps) int` (unshare/fix-permissions take a path arg).
- `buildercmd.RunBuilder(deps, sub, args) int` + `buildercmd.RealDeps()`.

Still orchestrator-owned (Stage 16b exit criteria the orchestrator completes):
- The Go `run` dispatch's `runtime == macos-user` branch → call `macosuser.RunMacosUser` (currently `run.go` prints "must be handled by the Python delegation seam").
- Deleting **seam #8** from the flag registry + `run.go`.
- The `RealDeps` wiring must pass: (a) `runProxy` = `internal/runcmd`'s `runWithProxy` (or `internal/ttyproxy`); (b) `materialize` = a thin adapter over `internal/darwinpkg` that runs the streaming nix build and maps `DarwinPackages` → `macosuser.Darwin` (returning `ok=false` + err on `DarwinPackagesError`). `internal/darwinpkg` currently exposes only the pure argv/env builders (`BuildProfileArgv`, `BuildEnv`, `ProfilePaths`, `ParseSkippedNames`); the streaming `materialize` subprocess wrapper still lives in Python and must be added to the run wiring (darwinpkg's own doc comment notes this).

## Exit criteria status

| Criterion | Status |
|---|---|
| dry-run artifact dump byte-identical across fixtures | ✅ `TestDryRunArtifactParity` (5 fixtures) |
| 4 macos-* commands' argv byte-identical | ✅ producer differential + orchestrator unit tests (the command bodies emit the goldened command lists) |
| builder {setup,start,stop,status} ported (scope addition) | ✅ `internal/buildercmd` + setup-state parity |
| `GOOS=darwin go build ./...` green | ✅ (with orchestrator WIP set aside; my packages green standalone on darwin) |
| Python macos modules NOT deleted | ✅ (Stage 17's job) |
| seam #8 deleted from Go run dispatch | ⏳ orchestrator-owned wiring |
| backend working end-to-end on real hardware | ❌ explicitly NOT required (§13) — Mac-runbook step |

## Verification commands

```
go test -count=1 ./internal/macosuser/ ./internal/buildercmd/ ./internal/builder/
go test -run TestDryRunArtifactParity -v ./internal/macosuser/
GOOS=darwin GOARCH=arm64 go build ./internal/macosuser/ ./internal/buildercmd/ ./internal/builder/
```

## Open Questions

- **OQ-1 (unchanged, still open):** the `path_helper` login-shell PATH fix is unverified on a Mac. The Go port reproduces the rc-file writes byte-for-byte; runtime verification stays runbook `mac-macos-user-e2e` §5. Not blocking 16b (§13 excludes hardware verification from the exit criteria).
- No new blocking questions raised.
