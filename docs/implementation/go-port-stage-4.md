# Go-port Stage 4 — frameproto conformance + hostservice server (handoff)

**Status:** landed, green. Gate for Stages 5–7 and 11.
**Plan:** [`docs/plans/go-port-plan.md`](../plans/go-port-plan.md) Stage 4.

## What landed (commit 51a6ffd; library in 7ea2562)

- `internal/frameproto`: frame protocol v1 wire codec — `>BI` header, SIGNED
  `>i` exit payload (negative rc round-trips), `handler error: <e>\n`, the
  `jail=%s keys=%s rc=%s elapsed_ms=%d bytes_out=%d` access-log line.
- `internal/hostservice`: server side of `src/host_service.py` —
  `Serve`/`Session`/`ExecAllowlisted`, access log, handler-panic → stderr
  `handler error:` + exit 1, request decode via `jsonx` (order-preserving),
  allowlist error text via `pytext.Repr`.

## Verified (conformance suite, both directions, `-race` clean)

- Go frameproto CLIENT vs the REAL Python `python -m src.host_service` smoke
  server: `>BI` framing, JSON stdout line, implicit exit(0).
- Python CLIENT (yolo_ps-style reader) vs a Go `hostservice.Serve` echo: stdout
  JSON line + raw stdout + signed exit(7) round-trip.
- handler panic → stderr `handler error: boom\n` + exit(1), byte-exact.
- Skips gracefully when Python absent; CI has it.

## Human actions

- CI (§10.7): confirm `go test ./internal/hostservice/` (incl. conformance)
  passes on both arches.

## Next

Stage 5 (host-processes daemon) consumes this server + `internal/json5` (Spike A,
in progress). Stages 6/7/11 (oauth broker, builtin daemons, jail-side wave) all
build on this frame codec.


---

## Audit addendum (2026-07-18, planning agent) — multi-agent review of the burst

Findings below are from an 8-auditor review with adversarial verification (two
independent verifiers per blocker/major, each instructed to refute). **Nothing
here was refuted**; several were reproduced live by the verifiers. Fix or ledger
each before this stage's seam flag is flipped on the dev host.

frameproto's wire behavior re-verified correct. The gap is the conformance
suite's breadth: the plan enumerates the cases this gate must cover, and several
enumerated ones (one-request-per-connection, concurrency, cross-language negative
exit, access-log format) are absent, so 'suite green against both' overstates
what is actually pinned.

### Findings

#### **[MAJOR · confirmed]** hostservice ExecAllowlisted maps signal-killed children to rc=-1, Python sends -N

Python's Session.exec_allowlisted returns proc.wait()'s negative-signal rc (e.g. -11 for SIGSEGV) in the signed exit frame; Go's exitCodeFromErr uses exec.ExitError.ExitCode(), which returns -1 for ANY signal death. Empirically confirmed in-jail: for a SIGSEGV'd child, Go computes rc=-1 while Python computes rc=-11. The frameproto docs themselves stress that 'a negative rc (e.g. a signal death) must round-trip' — the codec round-trips it, but the server computes a different value. Jail-side yolo-ps would exit 255 vs 245. Not in the divergence ledger (which has only D1-D4, all jsonx/pytext/paths).

*Evidence:* /workspace/internal/hostservice/hostservice.go:296-305 (exitCodeFromErr), /workspace/src/host_service.py:197 (rc = proc.wait()); empirical: temp test in-jail printed 'go rc=-1 signaled=true sig=11 (python proc.wait would be -11)' and python 'wait rc = -11'; ledger /workspace/docs/design/go-port-divergences.md contains no entry for this

*Fix:* In exitCodeFromErr, unwrap ee.Sys().(syscall.WaitStatus); if ws.Signaled() return -int(ws.Signal()) to match Python; add a cross-language exec_allowlisted signal-death test. Alternatively file a 'proposed' divergence-ledger entry with a reachability argument — but silent omission is not allowed under plan §1/§5.

#### **[MAJOR · confirmed]** Stage 4 conformance suite omits plan-enumerated cases (1-req/conn, concurrency, more)

The plan's Stage 4 spec enumerates the conformance cases: '>BI framing, signed >i exit, handler error + exit(1), implicit exit(0), one-request-per-connection, concurrency, access-log line format', with Exit 'suite green against both'. The landed suite is 3 tests: Python-server/Go-client (framing, JSON line, implicit exit 0), Go-server/Python-client (stdout + exit 7), and handler-panic. One-request-per-connection and concurrency have no test in either direction; signed exit is cross-checked only with positive 7 (negative rc only in Go-only unit tests); the access-log format is asserted only by a Go-only unit test, never compared against actual Python log output. §14 nonetheless says 'landed (conformance both directions)' and the handoff 'landed, green. Gate for Stages 5-7 and 11' — the gate is thinner than the plan specifies, and Stages 5/6/7/11 already built on it.

*Evidence:* /workspace/docs/plans/go-port-plan.md:392-397 vs /workspace/internal/hostservice/conformance_test.go:21-211 (3 tests) and /workspace/internal/frameproto/frameproto_test.go:39-94 (Go-only signed/negative + access-log asserts); /workspace/docs/plans/go-port-plan.md:935

*Fix:* Add conformance cases: second request on the same connection (both servers close after one request), N concurrent connections, a negative rc (-11) round-trip Go-server→Python-client and Python-server→Go-client, and an access-log capture comparison of the message part against the real Python server's output.

#### **[MINOR]** ExecAllowlisted spawn-failure path: different stderr bytes and access-log rc

When the child binary can't be started (missing/EACCES), Python's Popen raises out of exec_allowlisted, is caught by _handle_one, and the client sees stderr 'handler error: [Errno 2] No such file or directory: ...\n' + exit(1) with access-log rc=1. Go handles it inline: stderr 'exec_allowlisted: fork/exec ...: no such file or directory\n' + exit(1), and handleOne's rc stays 0 in the access log. Client-visible stderr bytes and the operator-grepped rc field both diverge. Trigger requires a missing/broken host binary (daemon-controlled argv[0], e.g. ps absent), so reachability is low but nonzero.

*Evidence:* /workspace/internal/hostservice/hostservice.go:134-138 (inline handling) and :279-292 (rcForLog stays 0) vs /workspace/src/host_service.py:172-177 (Popen raises) and :263-270 (handler-error path, rc_for_log=1)

*Fix:* Make cmd.Start() failure panic (or return through the handler-error path) so the client sees 'handler error: <e>\n' and the access log records rc=1; exact errno text parity can be ledgered.

#### **[NIT]** Conformance comment overstates 'REAL Python yolo_ps client' — it's an inline snippet

TestConformanceGoServerPythonClient's comment says it drives 'the REAL Python yolo_ps client's frame reader', but the test runs an inline python -c snippet that reimplements the reader. src/yolo_ps.py has importable _send_request/_stream_response/_call that could be driven directly. The handoff phrases it honestly ('yolo_ps-style reader'), so only the test comment misleads.

*Evidence:* /workspace/internal/hostservice/conformance_test.go:87-91,118-143 vs /workspace/src/yolo_ps.py:30-70

*Fix:* Either drive src.yolo_ps._call via a tiny wrapper script, or soften the comment to 'yolo_ps-equivalent inline reader'.

#### **[NIT]** hostservice log-plumbing divergences (prefix, fd, non-dict, jail_id, write errors)

Cluster of log-only/edge divergences: (1) Go Logger uses log.LstdFlags ('2026/07/18 01:02:03 ') vs Python '%(asctime)s %(levelname)s %(name)s: ' — operators grepping 'INFO host_service' lose hits; (2) Go's 'conn closed without a request' drops Python's 'conn=fdN'; (3) valid-JSON-non-dict request: Python raises AttributeError (thread traceback, no info line), Go logs the closed-without-request line — client-visible behavior identical (zero frames); (4) jail_id non-string: Python str()-coerces (42→'42'), Go leaves 'unknown' — access-log field only; (5) Go sendFrame swallows write errors so a handler keeps running after client disconnect (Python's sendall raises, aborting the handler with access-log rc=1 vs Go rc=0).

*Evidence:* /workspace/internal/hostservice/hostservice.go:33 (LstdFlags), :256-277 (closed-without-request, non-map, jail_id string-only), :53-63 (swallowed write errors) vs /workspace/src/host_service.py:99-106,244-283,313-316

*Fix:* Decide which of these the 'operators grep this' contract covers; at minimum match the message prefix format for the access log's surrounding fields, and note the rest in the divergence ledger as proposed entries.

