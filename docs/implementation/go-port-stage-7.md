# Go-port Stage 7 — Builtin daemons (cgd + journald) (handoff)

**Status:** Commit B (Go daemons) landed + tested. **Commit A (Python
thread→subprocess carve-out) NOT done** — the risky live-path piece, flagged
below.
**Plan:** [`docs/plans/go-port-plan.md`](../plans/go-port-plan.md) Stage 7.

## What landed (commit 5d1cc37)

- `internal/cgd` + `cmd/yolo-cgd`: cgroup-delegate. Single-line-JSON protocol,
  `_validate_cgroup_name`, `_parse_memory_value`, create_and_join (cpu.max
  quota formula, 1MB memory floor, pid range, warnings), destroy (procs-refusal
  / idempotent-absent / read-error==remove-error), SO_PEERCRED PID + chmod 0777.
- `internal/journald` + `cmd/yolo-journald`: journal bridge. `>BI` framing with
  stream IDs **1/2/3** (not frameproto's 0/1/2), arg validation, user-mode
  `--user` prepend, exit codes 2/127/1.

## Verified

- Go `-race` clean. cgd validate/parse byte-diffed vs the live Python oracle
  (`tools/parity/cgd_oracle.py`); create/join/destroy over a fake cgroup tree +
  a real Unix-socket round trip.
- journald: exact frame-header bytes, stream-id distinctness, arg-validation
  golden, AND an end-to-end run of the real `yolo-journald` binary with a fake
  `journalctl` on PATH → stdout=frame1, stderr=frame2, exit=frame3, `--user`
  prepended.

## REMAINING — Commit A (live-path surgery, needs a focused pass)

The plan's Stage 7 Commit A is a behavior-preserving Python refactor that moves
the cgd + journal **threads** (in `loopholes_runtime.py`) to spawned
subprocesses matching the `_start_host_service_external` lifecycle, **with
`PR_SET_PDEATHSIG(SIGTERM)`** so they still die when `yolo run` dies by any
means incl. SIGKILL (today's thread crash-lifetime). Only after that does the
binary swap ride `YOLO_GO_DAEMONS` at `_start_host_service_external`.

This was deferred because:
- It rewrites the live `yolo run` threading model — higher risk than the pure
  daemon-logic port, and the OOM-prone environment makes big live-path edits
  risky to do without frequent commits.
- Its exit criteria are **not unit-testable** (the plan says so): a **kill -9
  test in thread-mode vs subprocess-mode diffing surviving processes/sockets**,
  and cgroup writes (`cpu.max`/`memory.max`/`pids.max`) diffed identical — both
  require a real nested jail.

**Next session for Stage 7:** do Commit A + the `YOLO_GO_DAEMONS` gate for
`cgroup-delegate`/`journal` (reusing `_daemon_launcher`, already built in
Stage 6), then the nested-jail kill -9 lifetime verification in both modes.

## Human actions

- CI (§10.7): confirm `go test ./internal/cgd ./internal/journald` on both arches.
- Commit A's nested-jail kill -9 verification is a human/nested-jail step.


---

## Audit addendum (2026-07-18, planning agent) — multi-agent review of the burst

Findings below are from an 8-auditor review with adversarial verification (two
independent verifiers per blocker/major, each instructed to refute). **Nothing
here was refuted**; several were reproduced live by the verifiers. Fix or ledger
each before this stage's seam flag is flipped on the dev host.

Commit B's daemons are well-built but contain a **confirmed data-loss race**
(reproduced live: 7 of 20 runs truncated journalctl output while Python delivered
10/10 complete), plus signal-semantics and logging gaps. Separately, yolo-cgd's
interface cannot actually be wired at Commit A, and it breaks the macOS build of
the whole Go tree. Commit A must not proceed until these are resolved.

### Findings

#### **[BLOCKER · confirmed]** yolo-journald silently truncates journalctl output (Wait/pipe race) — confirmed live

cmd/yolo-journald calls cmd.Wait() while the pump goroutines are still draining StdoutPipe/StderrPipe; per os/exec docs, Wait closes those pipes after process exit, discarding kernel-buffered data. Reproduced with the real binary + a fake journalctl bursting 500,000 bytes: 7 of 20 runs delivered truncated output (483328/491520/495616 bytes) with a clean rc=0 exit frame — a silent data loss the client cannot detect. The Python daemon (pumps read to EOF, wait() doesn't close pipes) delivered 500,000/500,000 in 10/10 runs of the identical scenario. This is exactly the tail of `journalctl -n`/non-follow output that users read. Dormant today, but the handoff stamps this code 'landed + tested', so nothing forces a re-look before the flag flips at Commit A.

*Evidence:* cmd/yolo-journald/main.go:127 (cmd.Wait()) runs before main.go:135 (wg.Wait()); pumps read the pipes from main.go:88-89 at main.go:103-121. Python contrast: src/cli/loopholes_runtime.py:1646-1651 (read to EOF), 1671-1673 (wait then join). Live differential recorded in this audit (20-run Go probe vs 10-run Python baseline).

*Fix:* Drain to EOF before reaping: move wg.Wait() above cmd.Wait() (pumps get EOF when the child exits and closes its ends — this also mirrors Python's pump-until-EOF semantics), or replace StdoutPipe/StderrPipe with manually created os.Pipe pairs whose parent ends only the pumps close. Add a burst-and-exit regression test (fake journalctl, ~500KB, N iterations) to internal/journald or a cmd-level test.

#### **[MAJOR · confirmed]** yolo-cgd cannot be wired at Commit A: lazy container-cgroup resolution is missing

The Go daemon requires --container-cgroup at spawn (exits 2 without it), but loopholes start BEFORE the container exists — run_cmd.py explicitly starts host services 'BEFORE the container so their sockets exist'. The Python daemon handles this by lazily resolving the container cgroup on the first request and answering {"ok": false, "error": "Container cgroup not yet available"} until then; the module map pins this ('Container cgroup lazily resolved on first request'). The Go binary has neither the resolution logic (_resolve_container_cgroup needs cname+runtime, i.e. podman inspect) nor the not-yet-available response. So the next session's stated plan — 'do Commit A + the YOLO_GO_DAEMONS gate... reusing _daemon_launcher' — cannot wire this binary as-is; this is the concrete cost of landing B before the seam existed, and the handoff's REMAINING section doesn't mention it.

*Evidence:* cmd/yolo-cgd/main.go:27-32 (required flag, no resolution path, no 'not yet available' response anywhere in internal/cgd); src/cli/run_cmd.py:2948-2953 (loopholes start before container); src/cli/loopholes_runtime.py:1450-1481 (lazy resolve + 'Container cgroup not yet available' at 1473); docs/research/go-port-module-map/loopholes.json:148 (frozen contract includes lazy resolution).

*Fix:* Either port lazy resolution into yolo-cgd (accept --cname/--runtime, resolve on first request, emit the byte-identical 'Container cgroup not yet available' error until resolvable), or redesign the Commit A wiring so Python resolves and passes the path late — the latter changes startup semantics and would need a ledger entry. Decide before Commit A, not during it.

#### **[MAJOR · confirmed]** cmd/yolo-cgd breaks the darwin build of the entire Go tree

peerCredPID uses syscall.GetsockoptUcred/SO_PEERCRED with no build tag; GOOS=darwin go build ./... fails on exactly this package (everything else compiles). scripts/build-go.sh is set -e and builds every cmd/, so `just build-go` on a Mac now aborts the whole dist-go channel — and macOS is a supported host (macos-user runtime, check-macos CI job, the active Mac test runbooks), with the Stage 6 Go broker soak being a plausible near-term darwin consumer. The Python original even carries a macOS LOCAL_PEERPID branch (dead code, since the daemon is skipped on macOS, but the build must still succeed).

*Evidence:* cmd/yolo-cgd/main.go:108 (syscall.GetsockoptUcred, syscall.SO_PEERCRED — linux-only); reproduced: `GOOS=darwin GOARCH=arm64 go build ./...` fails only on cmd/yolo-cgd; scripts/build-go.sh:13 (set -euo pipefail) + 26-30 (builds every cmd/*/); .github/workflows/ci.yml:58-59 (check-macos job exists, though it doesn't build Go yet).

*Fix:* Split peerCredPID into peercred_linux.go (//go:build linux) and a peercred_other.go stub returning 0 (or implement LOCAL_PEERPID 0x002 for darwin to mirror the Python branch). Add `GOOS=darwin go build ./...` to lint-ci or CI so the next linux-only syscall is caught at commit time.

#### **[MAJOR · confirmed]** Both Go daemons drop the per-request audit logging the module map freezes

The Python cgd daemon logs every request ('op=... peer_pid=... request=...') and every response to GLOBAL_STORAGE/logs/<cname>-cgd.log, plus cgroup-resolution outcomes; the journal daemon logs '[journal] mode=... args=...' to <cname>-journal.log. The module map records this logging as part of the frozen daemon contract for both. The Go binaries have no log-file plumbing at all (only startup errors to stderr), so flipping YOLO_GO_DAEMONS silently loses the audit trail for privileged cgroup operations performed on behalf of the jail — the class of quiet behavioral regression the port rules exist to prevent.

*Evidence:* src/cli/loopholes_runtime.py:1189-1191 (request log), 1231 (response log), 1455-1464 (resolution log), 1608-1612 (journal request log); docs/research/go-port-module-map/loopholes.json:148 ('Every request logged to GLOBAL_STORAGE/logs/<cname>-cgd.log') and :154 ('Logged to GLOBAL_STORAGE/logs/<cname>-journal.log'); cmd/yolo-cgd/main.go and cmd/yolo-journald/main.go contain no log-file writes (grep: only fmt.Fprintln(os.Stderr,...) at startup).

*Fix:* Add a --log-file flag (or have the Commit A spawner hand the daemons an already-open append fd / redirect stdout) and emit the byte-compatible request/response lines; alternatively propose a ledger entry if stderr-capture-to-logfile via the spawner is deemed equivalent — but that still requires the daemons to print the lines.

#### **[MAJOR · confirmed]** journald signal semantics diverge: no Setsid, SIGKILL vs SIGTERM, -1 vs -N exit

Three related signal-semantics breaks in cmd/yolo-journald vs the Python daemon: (1) Python spawns journalctl with start_new_session=True; Go sets no SysProcAttr, so journalctl shares the daemon's process group — after Commit A, any group-directed signal at the daemon (e.g. the plan's SIGTERM→5s→SIGKILL stop, or PDEATHSIG cascades) will also hit a live journalctl, which Python's session isolation prevents. (2) On client disconnect Python sends SIGTERM (proc.terminate()); Go sends SIGKILL (cmd.Process.Kill()). (3) When journalctl dies by signal, Python's proc.wait() yields -signum and that int32 goes into the exit frame; Go's ee.ExitCode() returns -1 for any signal death — a byte-level protocol divergence in the exit frame payload (e.g. -15 vs -1) that the in-jail yolo-journalctl client passes to the user.

*Evidence:* Go: cmd/yolo-journald/main.go:86-89 (no SysProcAttr/Setsid), main.go:113 (cmd.Process.Kill()), main.go:127-133 (ee.ExitCode()). Python: src/cli/loopholes_runtime.py:1614-1623 (start_new_session=True with rationale comment), 1653-1656 (proc.terminate()), 1671+1676 (proc.wait() rc packed '>i'). Plan freeze: docs/plans/go-port-plan.md:445-448 lists the journal framing among Commit B's frozen items; audit brief lists signal semantics as a hunt target.

*Fix:* Set SysProcAttr{Setsid: true}; use Process.Signal(syscall.SIGTERM) on write failure; derive rc via ee.Sys().(syscall.WaitStatus): if ws.Signaled() { rc = -int(ws.Signal()) } else { rc = ws.ExitStatus() }. All three are small, testable changes.

#### **[MINOR]** Go daemons read request headers unbounded; Python caps at 4096 (cgd) / 16384 (journal)

Python stops accumulating at the cap and errors out ('to avoid a runaway client hanging the daemon thread'); the module map pins both caps ('single JSON line ≤4096B', '≤16384B header'). Go's bufio ReadBytes('\n') grows without bound, so an over-cap request Python rejects is accepted by Go, and a newline-less client can grow daemon memory indefinitely. The main.go comment 'Read a single line (up to 4096 bytes), matching the Python recv loop' is wrong about its own code.

*Evidence:* src/cli/loopholes_runtime.py:1156 (4096 cap), 1559 (16384 cap); cmd/yolo-cgd/main.go:63-65 (comment + unbounded ReadBytes); cmd/yolo-journald/main.go:64-65; docs/research/go-port-module-map/loopholes.json:148,154.

*Fix:* Enforce the caps (e.g. io.LimitReader or manual accumulation mirroring the Python loop, including its post-cap error/malformed-request behavior), and fix the comment.

#### **[MINOR]** Cluster of unledgered edge-input divergences (error strings, tri-state, regex, lengths)

Several small behavioral divergences exist with no ledger proposal or handoff mention: (a) invalid-JSON/non-object cgd requests: Python replies with str(exception) ('Expecting value: line 1 column 1 (char 0)', "'list' object has no attribute 'get'"), Go replies 'invalid request' (verified live); (b) journal non-object JSON: Python's handler raises AttributeError uncaught (no frames, thread dies, conn closed), Go politely frames exit-2; (c) falsy non-null args (0/""/false): Python's `request.get("args") or []` treats them as empty, Go errors; (d) trailing-newline cgroup names: Python re.match '$' matches before a final \n so 'job\n' validates, Go's regexp rejects it; (e) arg length: Python counts characters, Go counts bytes (multibyte args over ~1024 bytes but under 1024 chars diverge); (f) invalid cpu_pct/pids warning text: Python embeds the Python exception ('cpu.max: invalid literal for int()...'), Go says 'cpu.max: invalid cpu_pct' — these surface to the jail client in the warnings array; (g) status/destroy use Path.exists() (true for files) vs Go dirExists (IsDir). Individually pathological, but the process (plan §1.1) requires divergences be proposed, not silently accumulated.

*Evidence:* (a) live diff this audit vs src/cli/loopholes_runtime.py:1224-1227 / cmd/yolo-cgd/main.go:74-77; (b) loopholes_runtime.py:1565-1585 (only JSONDecodeError caught, try/finally at 1556/1679) vs internal/journald/journald.go:76-79; (c) loopholes_runtime.py:1580 vs journald.go:82-91; (d) loopholes_runtime.py:1125-1128 vs internal/cgd/cgd.go:27-33; (e) loopholes_runtime.py:1592 vs journald.go:101; (f) loopholes_runtime.py:1308-1316 vs internal/cgd/ops.go:76,106; (g) loopholes_runtime.py:1195,1375 vs internal/cgd/cgd.go:174,202-205 and ops.go:138.

*Fix:* Triage each into fix-or-ledger at the Stage 7 QA pass (batch-2 style doc): (c) and (f) are cheap to match exactly; (a)/(b) are exception-string divergences that likely warrant proposed ledger entries with reachability arguments; (d)/(e)/(g) need a decision either way. None should stay undocumented.

#### **[MINOR]** Handoff omits Commit A's orphan-sweep requirement; oracle docstring overstates coverage

Two documentation gaps: (1) The plan's Commit A includes 'Belt-and-braces: extend the relay orphan-sweep pattern to these daemons; any residual lifetime divergence goes to the ledger' — the handoff's REMAINING/Next-session list names PDEATHSIG and the kill -9 diff test but not the orphan sweep, so the next session may skip it. (2) tools/parity/cgd_oracle.py's docstring claims it emits 'the cpu.max quota formula for a fixed nproc + pct matrix' but the output dict contains only validate_name and parse_memory; no quota matrix exists on either side.

*Evidence:* docs/plans/go-port-plan.md:444 (orphan-sweep requirement) vs docs/implementation/go-port-stage-7.md:28-48 (no mention); tools/parity/cgd_oracle.py:9-11 (docstring) vs :59-62 (emitted keys).

*Fix:* Add the orphan-sweep item to the handoff's Commit A checklist; either implement the quota-matrix emission (it would pin the '{quota} 100000' byte format cross-language) or fix the docstring.

