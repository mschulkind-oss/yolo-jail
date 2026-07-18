# Go-port Stage 10 — Go entrypoint boot orchestration + dual-impl image (handoff)

**Status:** landed (in-jail verified). Go arm is OPT-IN behind
`YOLO_ENTRYPOINT_IMPL=go` (default stays python). ONE `just load && just
install` on the host ships the dual image; the CLI `-e` forward is a separate
slice (see Human actions).
**Plan:** [`docs/plans/go-port-plan.md`](../plans/go-port-plan.md) Stage 10.
**Builds on:** Stage 9 generators-lib (all pure content generators already
byte-verified vs live Python via the tree-diff/sha256 golden harness).

## What landed (this burst)

The pure content generators were already ported and byte-verified. This burst
adds the side-effecting boot ORCHESTRATION that ties them into a runnable
binary, plus the image seam.

- **`internal/entrypoint/boot.go`** — `Main()` reproduces `main()`'s exact
  ordering + perf-log labels; `perfLog` (`_perf`/`_perf_dump`);
  `hydrateEnvFromUserEnvFile` (the `_EXPORT_RE` grammar ported verbatim as a
  flattened RE2 pattern, `'\''` unescape, launch-env-wins precedence, sets both
  `e.Vars` and `os.Setenv` so children + later generators agree);
  `setupCgroupDelegation` (socket-probe stderr line, byte-for-byte);
  `trustWorkspaceConfigs`; `copyHostNvimConfig` (copytree with symlink deref +
  same-inode/dangling swallow); `execBash` (`syscall.Exec` of `bash --rcfile
  ~/.bashrc -c <activated>`, deliberately NO `mise hook-env`). Re-attaches the
  two subprocess side effects the pure generators deliberately DEFER:
  `miseUninstallRetired` (tail of `generate_mise_config`) and
  `installClaudePlugins` (tail of `configure_claude`, inside the claude arm).
- **`internal/entrypoint/system_boot.go`** — `configureTimezone` (/run symlink
  writes from `$TZ`), `generateLdCache` (`ldconfig -C /run/ld.so.cache`, 30s
  timeout).
- **`internal/entrypoint/identity.go`** — `configureGit`, `configureJJ`
  subprocess calls (capture_output discarded, best-effort no-op if binary
  absent).
- **`internal/entrypoint/runtime.go`** — `setupPublishedPortLocalnet` (iptables
  PREROUTING DNAT), `startContainerPortForwarding` (socat, Unix-socket +
  TCP-gateway modes), `startJailDaemonSupervisor` (spawns `python3 -m
  src.jail_daemon_supervisor`, PID-file singleton via `os.kill(pid,0)` liveness
  incl. EPERM=alive), `portInUse`.
- **`internal/entrypoint/exec.go`** — `syscall.Exec` wrapper (compiles on
  linux + darwin; no build-tag split needed — execve is on both).
- **`cmd/yolo-entrypoint/main.go`** — thin `argv → entrypoint.Main` shim.
- **`flake.nix`** — `/bin/yolo-entrypoint` wrapper branches on
  `YOLO_ENTRYPOINT_IMPL` (default `python`). Go arm prefers the dev-override
  `/opt/yolo-jail/dist-go/linux-<arch>/yolo-entrypoint`, else the baked
  `${goBinaries}/bin/yolo-entrypoint`. Both impls in every image → per-jail A/B
  with zero host changes after the one rebuild.

Best-effort-never-abort-boot is preserved per step: `genStep` warns and
continues on a generator error; every subprocess/IO failure is swallowed like
Python's per-step `try/except`.

## Verified (in a real nested jail, both arms)

- **Default (python) arm** still boots — no regression; the rebuilt image's
  wrapper carries the new branch (confirmed by `head yolo-entrypoint`).
- **Go arm** (`YOLO_ENTRYPOINT_IMPL=go /bin/yolo-entrypoint '<cmd>'`) boots
  end-to-end: cgroup-delegate line printed, PATH set with real `/mise/shims`,
  execs bash, command runs as root; `node --version`, `go version`, and
  `git config --global --get user.email` (proving `configureGit` ran the
  subprocess) all resolve.
- **Tree parity in-jail:** booting both arms into separate temp HOMEs
  (`YOLO_AGENTS=["claude"]`) yields an IDENTICAL 25-file relpath set, and every
  file is byte-identical after normalizing the absolute HOME to `@HOME@`
  (`.bashrc`, bootstrap, venv-precreate, CA bundle, mise config,
  mcp-wrappers/node, `.claude.json`, shims — the 4 raw "diffs" were only the
  embedded HOME path). This is the Stage 9 golden, reproduced live.
- **User-env hydration round-trip parity** (`boot_test.go`): the Go hydrator vs
  LIVE Python `_hydrate_env_from_user_env_file` over a frozen corpus (all four
  export forms, `'\''` escape, launch-env-wins, comments/blank/malformed).
  Skips (not fails) when python3/uv absent.
- `go test ./internal/entrypoint/` green (incl. the Stage 9 tree harness);
  `GOOS=darwin GOARCH=arm64 go build ./...` passes; `go vet` + `staticcheck`
  clean on `internal/entrypoint` + `cmd/yolo-entrypoint`.

## Preserved surprising behavior (no silent "fixes")

- `forwardEntryPort` panics on a bare non-numeric `YOLO_FORWARD_HOST_PORTS`
  entry, mirroring Python's uncaught `int("garbage")` ValueError that CRASHES
  boot before the exec (module map flags this as a quirk to preserve). Unit
  test pins it.
- Deliberate NON-call of `mise hook-env` (flock deadlock) — the comment is
  carried over.
- CA-bundle env exports (`SSL_CERT_FILE` etc.) happen BEFORE bashrc and before
  any child spawn, so `os.Setenv` propagates to socat/supervisor children — the
  load-bearing ordering.

## Human actions / UNVERIFIED

- **ONE `just load && just install`** on the host to ship the dual-impl image
  (the nested jail already rebuilt it from the live flake in-jail; the host
  needs the same for its own jails).
- **CLI `-e YOLO_ENTRYPOINT_IMPL` forward** (`src/cli/run_cmd.py`) is a SEPARATE
  slice — out of this burst's scope. Until then, A/B is exercised by setting
  the env inside the jail (as the verification above did) or via a manual `-e`.
- **Soak / full `test_jail.py` on both arms** (the plan's CI `impl` matrix
  dimension), repeated-`podman exec` idempotency, two-concurrent-boots race —
  all human/CI-gated, need a real multi-jail host.
- Manual `yolo -- claude` smoke under `YOLO_ENTRYPOINT_IMPL=go`.
