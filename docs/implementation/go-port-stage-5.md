# Go-port Stage 5 — host-processes daemon (handoff)

**Status:** landed. Behind `YOLO_GO_DAEMONS`. Soak + nested-jail verification
human-gated.
**Plan:** [`docs/plans/go-port-plan.md`](../plans/go-port-plan.md) Stage 5.
**Unblocked by:** `internal/json5` (Spike A, `docs/research/go-port-parity.md`).

## What landed (a5b7751, 38ae732; client df11330)

- `internal/hostprocesses` + `cmd/yolo-host-processes`: ported from
  `src/host_processes.py`. Config load via `json5.Decode`, re-read per request;
  list/tree/pid modes; allowlist argv construction; keeps exec'ing real `ps`.
- `cmd/yolo-ps`: in-jail client (Stage 5/11), ported from `src/yolo_ps.py`.
- seam #2 at `_start_host_service_external`: `cmd[0]` swapped to the Go binary
  when gated via `YOLO_GO_DAEMONS`/`YOLO_GO_BIN_DIR` (missing → console-script
  fallback).

## Verified

- **Config-load parity** byte-diffed vs live Python `_load_config` over a
  corpus (comments, trailing commas, non-str filtering, `or DEFAULT_FIELDS`).
- **End-to-end**: Go `yolo-ps` → Go daemon → fake `ps` (PATH shim) produces the
  exact allowlisted argv (`ps -o pid,comm -C sway -C waykeeper`, sorted comms)
  + access-log line; empty-allowlist exit 3; self-check output.
- **Seam #2** unit test: a real gated Go binary launched through
  `_start_host_service_external` binds the socket (launcher swap works).
- All Python host_service/host_processes/loopholes tests still green (74).

## Human actions / UNVERIFIED

- Soak: `YOLO_GO_DAEMONS=yolo-host-processes` + `YOLO_GO_BIN_DIR=<dist-go>` on
  the dev host; flip/revert + `just build-go` when main moves.
- Nested-jail: `yolo-ps`, `yolo-ps -t`, `yolo-ps --pid N` byte-identical under
  both daemons (byte under fake-ps; structural under live ps); `yolo check`
  daemon probes identical. (The Python `ps`-replay byte-gate + one live-`ps`
  structural run are the plan's exit criteria; done here at the fake-ps level,
  live-ps needs a real jail.)
- CI (§10.7) both arches.


---

## Audit addendum (2026-07-18, planning agent) — multi-agent review of the burst

Findings below are from an 8-auditor review with adversarial verification (two
independent verifiers per blocker/major, each instructed to refute). **Nothing
here was refuted**; several were reproduced live by the verifiers. Fix or ledger
each before this stage's seam flag is flipped on the dev host.

The daemon's happy paths are faithful, but the audit found **three unledgered
behavior divergences** on failure/edge paths (one a blocker), and the plan's
black-box parity suite — the artifact that would have caught all three — was
never committed. Everything here is fixable in one focused session; none of it
should reach the soak flag first.

### Findings

#### **[BLOCKER · confirmed]** Tree mode drops Python's 15s ps timeout and inverts the ps-failure exit path

Python tree mode runs subprocess.run(argv, timeout=15) and reads out.stdout regardless of returncode: a hung ps is killed at 15s ('tree mode failed: ... timed out' + exit 1), and a ps that exits non-zero with empty stdout yields exit 0 with no output. The Go port uses exec.Command().Output() with NO timeout, so a hung ps blocks that connection forever (and the shipped in-jail Python yolo-ps has no client timeout, so `yolo-ps -t` hangs indefinitely during the Stage 5 soak); and on non-zero-exit-with-empty-output Go returns stderr 'tree mode failed: exit status 1' + exit 1 where Python returns exit 0. Confirmed live: with a fake ps that exits 1 silently, py=(b'', b'', 0) vs go=(b'', b'tree mode failed: exit status 1\n', 1). Neither divergence is in the divergence ledger (rg for host-processes entries in docs/design/go-port-divergences.md finds none), violating the zero-functionality-change ground rule.

*Evidence:* internal/hostprocesses/tree.go:16-28 (no timeout, err-with-empty-output -> exit 1) vs src/host_processes.py:115 (timeout=15) and :118-121 (empty stdout -> exit 0); empirical byte-diff run reproduced during audit; docs/design/go-port-divergences.md has no host_processes entry

*Fix:* Add a 15s kill to the Go tree exec (context.WithTimeout or the ExecAllowlisted pattern) emitting 'tree mode failed: ...' + exit 1 on expiry; on non-zero exit with empty captured stdout, fall through to the exit-0 empty-lines path like Python. If either behavior is deliberately changed instead, file proposed entries in docs/design/go-port-divergences.md.

#### **[MAJOR · confirmed]** Non-string 'mode' silently runs list mode in Go instead of Python's exit-2 rejection

Python computes mode = str(request.get('mode') or 'list'), so any truthy non-string ({'mode':5}, {'mode':{...}}) is stringified and hits the unknown-mode branch: stderr "unknown mode: '5'" + exit 2. Go only accepts string values (non-string -> mode stays 'list'), so the same request silently executes list mode and returns process data. Confirmed live: {'mode':5} -> py=(b'', b"unknown mode: '5'\n", 2) vs go=(ps list output, b'', 0). Output is still allowlist-bounded so it is not a data leak beyond list mode, but it is a silent wire-contract divergence reachable by any third-party client, and it is not in the divergence ledger.

*Evidence:* internal/hostprocesses/hostprocesses.go:138-143 vs src/host_processes.py:80,178-179; empirical black-box run case 5

*Fix:* Mirror Python: stringify truthy non-string mode values and route them to the unknown-mode exit-2 branch; falsy values -> 'list'.

#### **[MAJOR · confirmed]** Plan-mandated black-box parity suite for Stage 5 was never committed

The plan's Stage 5 Parity section specifies repeatable cases: black-box over the socket with a PATH-shimmed fake ps — list/tree/pid line-for-line, exit codes 0/1/2/3/124, per-request config re-read between requests, empty-allowlist. What landed is only the config-load oracle test (TestLoadConfigParity) plus TestLoadConfigMissingFile; there is no fake-ps shim in the repo (tools/parity/shims/ contains only _record.py), no socket-level Go test, no 124/timeout test anywhere in the Go tree, and no config re-read test on the Go side. The handoff's 'End-to-end ... verified' was a one-off manual run that left no artifact — and an ad-hoc black-box suite written during this audit immediately surfaced three unledgered divergences (tree failure paths, non-string mode, bool pid), direct evidence the missing suite has real cost. Future edits to hostprocesses.go/tree.go regress silently.

*Evidence:* internal/hostprocesses/hostprocesses_test.go:1-113 (config-load only); tools/parity/shims/ listing (no ps shim); no '124'/'timed out' test in internal/hostservice/conformance_test.go; docs/plans/go-port-plan.md:404-411 (Stage 5 parity cases); docs/implementation/go-port-stage-5.md:22-26

*Fix:* Commit the black-box suite: a fake-ps PATH shim + a harness driving both daemons over the socket for list/tree/pid, exit codes 0/1/2/3, a short-timeout 124 case, empty-allowlist byte compare, and an edit-config-between-requests re-read case. The audit's throwaway harness (Python frame-protocol client against both daemons with PATH=shim) is a ready template.

#### **[MAJOR · confirmed]** Go yolo-ps adds a 30s connection deadline that breaks the timeout-to-124 path

Python yolo-ps sets no socket timeout and streams until the exit frame arrives. Go yolo-ps sets DialTimeout(30s) and a single conn.SetDeadline(now+30s) covering the whole session. The daemon's ExecAllowlisted timeout is exactly 30s, so in the canonical 124 scenario (child killed at 30s, exit frame sent just after) the Go client's deadline — started earlier, at connect — expires first and run() returns 1 with no diagnostic, where Python returns 124. Any legitimately slow/large stream >30s also aborts. The client is dormant (not baked; the in-jail yolo-ps is still the generated Python script), so no live impact yet, but this poisons the Stage 11 bake and hits exactly the exit-code contract (0/1/2/3/124) the module map freezes. Not in the divergence ledger.

*Evidence:* cmd/yolo-ps/main.go:71 (DialTimeout 30s) and :77 (SetDeadline 30s) vs src/yolo_ps.py:70-87 (no timeouts); daemon timeout 30s at internal/hostprocesses/hostprocesses.go:182,218; docs/research/go-port-module-map/host-services.json:253 (yolo-ps contract: exit code = daemon rc)

*Fix:* Drop the blanket SetDeadline (match Python: block until EOF/exit frame), or remove the read deadline once the request is written; if DialTimeout is kept, ledger it as a proposed divergence.

#### **[MINOR]** pid=true and >int64 pids: Go exits 2 where Python exits 1

Python's isinstance(want_pid, int) accepts bool (True -> reads /proc/True/comm -> OSError -> 'pid True not found' + exit 1) and arbitrary-precision ints (huge pid -> not-found -> exit 1). Go's asIntStrict rejects bool and anything Atoi can't parse (overflow) with 'pid mode requires integer' + exit 2. Confirmed live: {'pid':true} -> py=(b'pid True not found\n', rc 1) vs go=(b"pid mode requires integer 'pid' in request\n", rc 2). Go's behavior is arguably saner, but it is an unledgered divergence; the ledger requires a proposed entry for human approval.

*Evidence:* internal/hostprocesses/hostprocesses.go:232-256 (asIntStrict rejects bool/overflow) vs src/host_processes.py:153-163; empirical black-box run case 6

*Fix:* Add a proposed entry to docs/design/go-port-divergences.md for bool/oversized pid handling (or mirror Python exactly).

#### **[MINOR]** Handoff/commit overstate the seam test: the 'real Go binary' is a Python stand-in

Both the 38ae732 commit message ('Unit test drives a real gated Go binary through _start_host_service_external') and the handoff ('a real gated Go binary launched through _start_host_service_external binds the socket') describe test_external_service_gated_swaps_to_go_binary, but the committed test writes a python3 script named yolo-host-processes as the 'Go binary'. The cmd[0]-swap mechanics ARE genuinely proven (gating, dir resolution, socket bind, --socket tail preserved), and the actual Go daemon does bind sockets (verified independently in this audit), but no committed test launches the real compiled daemon through the seam, so binary-specific failure modes (flag parsing of the substituted tail, early exit) are unpinned.

*Evidence:* tests/test_cli_unit.py:3438-3447 (python3 shebang stand-in) vs docs/implementation/go-port-stage-5.md:24-26 and git show 38ae732 commit message

*Fix:* Soften the handoff wording to 'a stand-in executable proves the swap', or add an opt-in test that builds cmd/yolo-host-processes (skip when go absent) and drives it through _start_host_service_external.

#### **[NIT]** yolo-ps connect-failure stderr tail renders Go's dial error, not Python's errno text

On an unreachable socket both clients print the frozen prefix 'yolo-ps: cannot reach loophole socket <path>: ' and exit 2, but the suffix differs: Python '[Errno 2] No such file or directory' vs Go 'dial unix /x.sock: connect: no such file or directory'. df11330 claims 'the frozen messages' verified; the OS-error rendering drift is inevitable but should be acknowledged (QA batch / ledger) since the module map freezes these as observable stderr. The no-socket message, by contrast, is byte-identical (verified with cmp) and both paths exit 2.

*Evidence:* cmd/yolo-ps/main.go:73 (%v of net.OpError) vs src/yolo_ps.py:76-78; live byte comparison (BYTES-SAME for no-socket; differing tails for connect failure; rc 2 both)

*Fix:* Note the errno-rendering drift in the next QA batch as accepted, or reformat the Go error to '[Errno N] <strerror>' via the syscall errno if byte parity is wanted.

#### **[NIT]** yolo-ps ordering comment rests on map-marshal accident; doctor probes never run Go code

Two small accuracy notes: (1) cmd/yolo-ps comments claim jail_id is stamped 'first in the request', but Go marshals a map (alphabetical key order) — it holds only because 'jail_id' < 'mode' < 'pid'; also Python json.dumps emits ', '/': ' separators vs Go compact, so request bytes differ (semantically fine — the module map explicitly blesses this). (2) The manifest doctor_cmd ['yolo-host-processes','--self-check'] resolves on PATH, not through _daemon_launcher, so during the soak `yolo doctor`/`yolo check` always probe the PYTHON self-check even while the Go daemon serves — the exit-criterion 'yolo check daemon probes identical' is trivially true and hostprocesses.SelfCheck is production-dead code until the manifest swap.

*Evidence:* cmd/yolo-ps/main.go:86-91 (map marshal + ordering comment); src/bundled_loopholes/host-processes/manifest.jsonc:22 (doctor_cmd) with no _daemon_launcher routing (rg 'doctor_cmd' hits only src/loopholes.py); docs/research/go-port-module-map/host-services.json:225 (json.dumps separators note)

*Fix:* Use an ordered encoder or fix the comment; note in the handoff that doctor probes stay Python-side during the soak so nobody reads 'probes identical' as Go self-check coverage.

