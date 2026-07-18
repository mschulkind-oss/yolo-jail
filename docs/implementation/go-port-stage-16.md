# Go-port Stage 16 — `yolo run` native (the finale) — handoff

**Status:** landed. The native Go `yolo run` runs end-to-end behind the
`YOLO_IMPL=go` gate; the default (bare `yolo -- cmd`, which rewrites to `run`)
still delegates to Python, unchanged. All five named sub-phases landed, each
committed separately, and the nested-jail gate passed on both arms.
**Plan:** [`docs/plans/go-port-plan.md`](../plans/go-port-plan.md) Stage 16.
**Built on:** `internal/runmount`, `internal/network`, `internal/runtime`,
`internal/image`, `internal/agentsmd`, `internal/storage`, `internal/loopholes`,
`internal/config`, `internal/agents`, `internal/shquote`, `internal/ttyproxy`,
`internal/cgd`, `internal/execx`, `internal/naming`, `internal/paths`,
`internal/checkdiag`, `internal/version`, `internal/jsonx`.

## Commits (main, newest first)

- `06704c8` fix(go): Stage 16 e2e fixes from nested-jail verification
- `b954756` feat(go): Stage 16 sub-phase 4 — lifecycle + locks + wire run behind YOLO_IMPL gate
- `5e9b1c8` feat(go): Stage 16 sub-phase 3 — ordered container argv assembly (the golden gate)
- `e1e984b` feat(go): Stage 16 sub-phase 2 — network/storage/image auto-load wiring
- `76b72bc` feat(go): Stage 16 sub-phase 1 — runcmd probes (repo-root staging, config gate, runtime pick)

(The sub-phase-4 commit message body carries some incidental noise: backticks in
the `-m` text triggered a shell command-substitution that accidentally launched
a nested jail and interleaved its output into the message. The staged CONTENT is
correct; only the message text is cosmetically affected. Not amended — the
never-amend rule holds.)

## Package shape (`internal/runcmd`)

- `runcmd.go` — `Options` (every side-effecting seam injectable: `Exec`, `Now`,
  `Getenv`, `LookPath`, `PathExists`, `RepoRoot`, `Getpid`, `IsTTYStdout/Stdin`,
  `Stdout/Stderr/Stdin`, `Workspace`, `IsMacOS`) + `fillDefaults` + `realExec`
  (timeout ≤ 0 = no deadline) + `isTTY` (ioctl-based) + `NewDefaultOptions`.
- `run.go` — `Run(opts) int` and `runContainer`: the full post-config flow.
- `probes.go` — `resolveRepoRoot` (env / cwd-walk / installed-wheel staging /
  user-config), `stageInstalledWheel` (the FROZEN rename-aside invariant),
  `expandUser`, `configRuntime`.
- `preflight.go` — config load+validate+preset-null+approval prompt, `_runtime`
  resolution, `isAppleContainer`/`runtimeIsConnectable`, `changePrompter`.
- `identity.go` — git/jj identity env; `loopholeResolver`.
- `assemble.go` / `assemble_parts.go` — the ordered container argv (the golden
  gate). `helpers2.go` / `hostclaude.go` / `hostprobes.go` / `mounts.go` /
  `cfgval.go` — the argv sub-builders + config accessors.
- `prepare.go` / `storagehelpers.go` — `_refresh_jail_briefings`, ws_state prep,
  seed/migrate/claude.json-sync, LSP-install/mise-store/version wiring.
- `lifecycle.go` — container lookups, `_live_yolo_containers` tri-state,
  owner-PID + reaping polarity, `_stop_jail`, OOM warning, podman-machine memory.
- `loopholesruntime.go` + `cgddaemon_{linux,other}.go` — start/stop_loopholes
  (FROZEN guard stack), in-process cgroup delegate (goroutine + lazy resolve),
  external services, broker singleton + per-jail relay ensure/kill.
- `command.go` — `final_internal_cmd` (frozen byte-golden).
- `userenv.go` — `yolo-user-env.sh` writer (frozen grammar).
- `flock.go` / `network.go` / `retire.go` / `imageload.go` / `brokerping.go` /
  `console.go` / `helpers3.go` / `fsutil.go` / `syscalls_{linux,other}.go` /
  `proxy_{linux,other}.go`.
- `image.AutoLoadImage` (added to `internal/image`) — the `auto_load_image` port.

## Wiring (the gate)

- `internal/frontdoor`: `run` added to `gatedNativeSubcommands` — `IsNative`
  returns true ONLY when `YOLO_IMPL=go`. The default (unconditional
  `nativeSubcommands`) stays empty, so bare `yolo -- cmd` delegates to Python
  unless the gate is explicitly exported. `run` is the DEFAULT subcommand, so
  the gate defaulting off was verified with care.
- `cmd/yolo/native.go`: `nativeDispatch["run"]=runRun`, which parses
  `--network/--new/--profile/--dry-run` + the post-`--` command, scanning the
  WHOLE args (the front-door rewrite puts flags before the injected `run` token).

## Output contract

Per the check-slice precedent, run's human chatter is NOT under byte-parity (it
is rich-markup-stripped plain text). What IS byte-exact and verified:

- the **ordered container argv** (flags-before-image, the `-e` block, mount
  order, host-service `-e` at index(image)) — proven byte-identical to LIVE
  Python over a 10-fixture matrix AND in the live nested jail (219 argv elts,
  zero diffs);
- the **`final_internal_cmd`** bash payload (frozen golden captured from live
  Python);
- the **`yolo-user-env.sh`** bytes + the export-line grammar (Go-writer /
  Python-reader round-trip closes the Stage-9 corpus);
- shlex quoting (via `internal/shquote`);
- the frozen host-state contracts (container naming, tracking/owner/lock files,
  sockets-dir sha1[:8] naming, the rename-aside build-root invariant).

## Verification

- `go test ./internal/runcmd/ ./internal/image/` — PASS. Includes: the static
  podman/linux argv golden; the **live-Python ordered-argv parity** over 10
  fixtures (`tools/parity/run_argv_oracle.py`); the `final_internal_cmd` golden;
  the **user-env Go-writer/Python-reader round-trip**; owner-PID reaping
  polarity; flock exclusivity; the AutoLoadImage state machine.
- `go test ./...`, `go vet ./...`, `staticcheck ./...` — clean.
- `GOOS=darwin GOARCH=arm64 go build ./...` — PASS (Linux-only syscalls guarded:
  `syscalls_linux.go`/`syscalls_other.go`, `cgddaemon_linux.go`/`_other.go`,
  `proxy_linux.go`/`_other.go`; ttyproxy is Linux-only, so the darwin `run`
  container path uses a plain-spawn fallback — macos-user itself delegates to
  Python before ever reaching here).
- `uv run python -m pytest tests/test_go_drift.py -q` — PASS; `ruff` clean on the
  new Python oracle.

### Nested-jail gate (the definition of done)

Run from inside this jail against a throwaway workspace `/tmp/nj`
(`{ "agents": ["claude"] }`), with `dist-go/` freshly built (`./scripts/build-go.sh`).

**Ordered-argv byte-parity (both arms, `YOLO_DEBUG=1`):**

```
# Python default arm:
YOLO_DEBUG=1 yolo --new -- true </dev/null >/dev/null 2>py.err
# Go arm:
YOLO_DEBUG=1 YOLO_IMPL=go YOLO_GO_BIN_DIR=/workspace/dist-go/linux-amd64 \
    /workspace/dist-go/linux-amd64/yolo --new -- true </dev/null >/dev/null 2>go.err
# shlex-split both `podman run …` lines and diff element-by-element:
#   => 219 argv elements each, ZERO real diffs.
```

**Both arms boot + execute:**

```
yolo --new -- bash -lc 'echo PYTHON-ARM-OK; whoami'          # => PYTHON-ARM-OK / root
YOLO_IMPL=go YOLO_GO_BIN_DIR=… yolo --new -- bash -lc 'echo GO-ARM-OK; whoami'  # => GO-ARM-OK / root
# In-jail $YOLO_BLOCK_CONFIG byte-correct on the Go arm ("[path]", "-*[rR]*" intact).
```

**Cross-impl lifecycle (both directions):**

```
# Python starts a jail (sleep 60, background); Go attaches:
YOLO_IMPL=go … yolo -- bash -lc 'echo GO-ATTACHED-MARKER; …'
#   => "Attaching to existing jail (yolo-nj-…)..." + GO-ATTACHED-MARKER
# Go starts a jail; Python attaches:
yolo -- bash -lc 'echo PY-ATTACHED-MARKER; …'
#   => "Attaching to existing jail (yolo-nj-…)..." + PY-ATTACHED-MARKER
```

**Concurrent-launch flock race (3 parallel Go fresh launches):**

```
for i in 1 2 3; do ( YOLO_IMPL=go … yolo -- bash -lc "echo RACE-$i-OK; sleep 15" & ); done
#   => racer 1 STARTS the container; racers 2 & 3:
#      "Attaching to jail started by another process (yolo-nj-…)..." — exactly ONE container.
```

**Orphan reaping (dead owner PID):**

```
# Start a Go jail, overwrite its owner-PID file with a dead PID, relaunch:
YOLO_IMPL=go … yolo --new -- bash -lc 'echo AFTER-REAP-OK'
#   => "Reaping orphaned jail yolo-nj-… (owner pid 999999 is gone)..." + AFTER-REAP-OK
```

## E2E fixes (found by the nested-jail gate, commit `06704c8`)

1. **isatty** — use a `TCGETS`/`TIOCGETA` ioctl, not a char-device mode check
   (`/dev/null` is a char device but not a tty → spurious container `-t`).
2. **runRun arg parse** — the front-door `RewriteArgv` inserts `run` at the `--`
   position, so pre-`--` flags land BEFORE the `run` token; scan the whole args.
3. **cgroup delegate** — run it IN-PROCESS (goroutine, matching Python threads)
   reusing `internal/cgd.Handle` with lazy container-cgroup resolution +
   `SO_PEERCRED`; the earlier subprocess spawn of `yolo-cgd` failed (not on
   PATH), dropping the delegate `-e` from the argv.
4. **realExec** — `timeout ≤ 0` now means NO deadline; the find_running/existing
   probes passed 0 and were killed instantly, so the attach decision always
   missed and every launch hit a name clash.
5. **YOLO_DEBUG dump** — write RAW (bracket sequences in the argv were eaten by
   the rich-tag strip regex; debug-print-only, the real env was always correct).

## Behavior notes / deliberate narrowings

- **macos-user** is a delegation seam (`exec python -m src.cli run …`), NOT
  ported — the front door delegates to Python before the Go native path when the
  runtime is macos-user; `run.go` also declines defensively if it somehow
  reaches there. The `--dry-run` non-macos-user error is reproduced.
- **macOS from-source build-offload** (the container-builder session in
  `auto_load_image`) is a documented narrowing: the Go port takes the plain-build
  path on macOS and relies on `DiagnoseNixBuildFailure` for the actionable
  "needs a Linux builder" message — mirroring the Stage-15 builder narrowing. The
  Linux path is byte-faithful. (macOS container runtimes are Mac-runbook-gated.)
- **`host-processes` external loophole fails to launch** on both arms in this
  jail (`yolo-host-processes` not on PATH) — this is PARITY (the Python arm emits
  the same failure), not a Go regression. It resolves once the jail-side binaries
  land on PATH (Stage 11/17).
- **Relay/broker reaping** uses the PID-file + socket-connectable liveness; the
  argv-pgrep identity-guard fallback of `_relay_kill` is simplified in this slice
  (the PID-file path is the common case; a recycled-PID misfire is bounded by the
  pidAlive check). Not observed in the nested-jail runs.

## Ledger / read-only-package requests

- **No divergence-ledger entries.** The five e2e fixes are bug fixes toward
  parity, not accepted divergences; the argv, final-cmd, and user-env bytes all
  match live Python exactly.
- **No read-only-package changes requested.** `internal/frontdoor` (editable)
  gained the `run` gate entry; `internal/image` (editable) gained
  `AutoLoadImage`. No edits to `internal/config`, `internal/jsonx`,
  `internal/loopholes`, `internal/entrypoint`, or `internal/paths` were needed.

## Follow-ups (not blockers)

- The shadow burn-in mechanism from the plan (§6 St.16) was NOT built — the argv
  goldens over the 10-fixture matrix PLUS the live nested-jail byte-diff give the
  confidence it was meant to provide (the plan explicitly allows this: "if argv
  goldens over fixtures give confidence, note that you used them instead").
- `_relay_kill`'s full pgrep + `/proc/<pid>/cmdline` identity guard (and
  `_relay_reap_orphans` orphan sweep) are a reasonable next increment for the
  broker-relay lifecycle; the current slice reaps the current jail's relay on
  exit via the sockets-dir hash.
- The journal-bridge builtin (`journal` config key) is not wired in
  `startLoopholes` (only cgroup-delegate + external services); a small follow-up.
- `test_jail.py` / `test_runtime.py` through the Go front door on both CI arches
  is the human-gated CI exit criterion (the in-jail agent has no CI visibility).

---

## Re-audit 2026-07-18

This stage has confirmed blocker/major findings in the consolidated re-audit: [`go-port-audit-2026-07-18.md`](go-port-audit-2026-07-18.md). Fix or ledger the items attributed to this stage before its `YOLO_IMPL=go` gate is recommended for dogfood.
