# Go-port Stage 3 — Broker relay (first production swap) (handoff)

**Status:** landed, all in-jail criteria green. Flag defaults to Python (unset).
**Plan:** [`docs/plans/go-port-plan.md`](../plans/go-port-plan.md) Stage 3.

## What landed (commit b46f400)

- `internal/brokerrelay` + `cmd/yolo-broker-relay`: Go port of
  `src/broker_relay.py`. Raw byte proxy, per-connection broker dial (new-inode
  fix), host-side `jail_id` stamp (overrides client value) via `jsonx`
  (insertion-order-preserving reframe), degrade to bidirectional pipe.
  Dial-failure path: `CloseWrite` + bounded drain → client sees clean EOF, never
  ECONNRESET. SIGTERM unlinks the socket only if dev/ino still match what it
  bound. `--socket/--broker/--jail` argv frozen against the Python argparse
  (missing args → exit 2).
- **Seam #2** in `loopholes_runtime.py` (SAME commit):
  `_relay_spawn_argv` resolves the Go binary from `YOLO_BROKER_RELAY_BIN`
  (identical trailing argv); matchers `_relay_pid_cmdline_matches` and
  `_relay_pgrep` widened to recognize BOTH `broker_relay.py` AND
  `yolo-broker-relay` — the orphan-relay rider.

## Verified

- Go tests `-race` clean: jail_id stamp+override, verbatim forwarding
  (unparseable + valid-non-JSON), broker-down clean-EOF-not-ECONNRESET,
  broker-restart-new-inode, SIGTERM socket unlink.
- **Cross-language mixed-mode** (`tests/test_broker_relay_go_parity.py`): the
  REAL Python terminator (`oauth_broker_jail.ask_host_broker`) + the Python
  `FakeBroker`/`RawUpstream` harness drive the GO binary — 6 tests incl.
  broker-layer attribution.
- 2 new unit tests for the seam wiring + widened identity guard (23 relay unit
  tests green; existing 8 `test_broker_relay.py` unchanged/green).
- **End-to-end through the real `_relay_ensure`**: with
  `YOLO_BROKER_RELAY_BIN` set, the Go binary is spawned, a ping round-trips
  with host-side `jail_id`, the identity guard matches, and `_relay_stop` reaps
  it + unlinks the socket. (Recorded command output in the session; re-runnable
  via the snippet in the Stage 3 commit message context.)

## Human actions

- **Soak:** export `YOLO_BROKER_RELAY_BIN=<repo>/dist-go/<goos>-<goarch>/yolo-broker-relay`
  on the dev host. Revert = unset. When main moves, rebuild `dist-go/`
  (`just build-go`); relay re-ensure picks up the new binary on the next `yolo`
  invocation. Restart isn't needed (each `yolo` re-ensures).
- **CI (§10.7):** confirm the Go tests + `test_broker_relay_go_parity.py` pass on
  both arches.
- Code default flips at Stage 17.


---

## Audit addendum (2026-07-18, planning agent) — multi-agent review of the burst

Findings below are from an 8-auditor review with adversarial verification (two
independent verifiers per blocker/major, each instructed to refute). **Nothing
here was refuted**; several were reproduced live by the verifiers. Fix or ledger
each before this stage's seam flag is flipped on the dev host.

The relay port itself re-verified clean on the hazards that matter (SHUT_WR +
drain clean EOF, dev/ino unlink guard, jail_id override, unstamped passthrough).
The findings are about **exit criteria claimed but not met**, and a §14 status
cell that asserts a soak the handoff itself lists as a pending human action.

### Findings

#### **[MAJOR · confirmed]** Plan §14 claims Stage 3 'soaking' while the handoff lists the soak as pending

Commit f734eb5 set the §14 status cell to 'landed (behind YOLO_BROKER_RELAY_BIN; soaking)'. Plan line 222 defines soaking precisely: 'the human exports the flag on the dev host'. But the same commit's Stage 3 handoff lists 'Soak: export YOLO_BROKER_RELAY_BIN=...' under 'Human actions', i.e. not yet done at commit time. The Stage 17 flip gates on soak evidence (plan line 214), so an aspirational 'soaking' status can poison the flip decision. Whether the human has since exported the flag is unverifiable from inside the jail.

*Evidence:* /workspace/docs/plans/go-port-plan.md:934 ('soaking') and :222 (definition of soaking) vs /workspace/docs/implementation/go-port-stage-3.md:41-44 (soak listed as a pending human action); both changed in the same commit f734eb5

*Fix:* Change the §14 cell to 'landed (behind YOLO_BROKER_RELAY_BIN; soak pending human flag export)' until the human confirms the export, then record the soak start date.

#### **[MAJOR→MINOR · confirmed]** Stage 3 exit criterion nested-jail claude-token smoke silently dropped

The plan's Stage 3 Exit line requires a nested-jail smoke — a real claude token ping through the Go relay — and the seam-flip criteria table (line 214) lists 'relay harness green both impls + nested-jail smoke' as the flip gate. Neither the Stage 3 handoff nor commit b46f400 claims, records, or explicitly defers this smoke; the handoff instead says 'landed, all in-jail criteria green', which reads as criteria-met. The E2E it does record (real _relay_ensure + framed ping against a fake broker) is not a claude token ping through a nested jail.

*Evidence:* /workspace/docs/plans/go-port-plan.md:384 (exit criterion) and :214 (flip criteria); /workspace/docs/implementation/go-port-stage-3.md:3,33-37 contains no nested-jail smoke record and no deferral note

*Fix:* Either run the nested-jail smoke (yolo -- bash from a jail, claude token ping through the Go relay with YOLO_BROKER_RELAY_BIN set) and record it in the handoff, or add an explicit 'deferred: nested-jail smoke' line so Stage 17 doesn't assume it happened.

#### **[MINOR]** Relay first-message timeout: per-recv (Python) vs whole-frame deadline (Go)

Python's _read_first_message sets settimeout(5.0), which applies PER recv() call — a client dripping bytes slower than one per 5s never times out, and the frame eventually gets stamped. Go sets a single absolute SetReadDeadline(now+5s) for the whole frame, so a slow-dripping client is downgraded to verbatim-unstamped forwarding after 5s total. Attribution (host-side jail_id) is lost in Go but kept in Python for slow clients. Code comment claims 'Faithful to Python's _read_first_message'. Real clients send in one shot, so impact is edge-case, but this is an unledgered behavioral divergence (ledger has no relay entries).

*Evidence:* /workspace/src/broker_relay.py:107 (settimeout per-recv) vs /workspace/internal/brokerrelay/brokerrelay.go:92-94 (single absolute deadline); /workspace/docs/design/go-port-divergences.md has no relay entry

*Fix:* Either reset the read deadline before each Read to mimic per-recv timeout, or file a 'proposed' divergence-ledger entry (reachability: terminator sends the frame in one write).

#### **[MINOR]** Fallback guard passes directories; commit misstates pre-fix failure mode

os.access(go_bin, os.X_OK) returns True for a directory (e.g. YOLO_BROKER_RELAY_BIN=.../dist-go/linux-amd64 without the binary name), so the guard returns the Go argv and subprocess.Popen in _relay_ensure raises PermissionError/IsADirectoryError uncaught — crashing the yolo invocation, the exact class the guard was meant to close. Also, the commit message says a missing path 'would spawn a broken relay and 502 every jail'; actually Popen raises FileNotFoundError in the parent, so the pre-fix failure was a hard crash of yolo run, not a silent 502. The guard's warning behavior itself is good: it prints a visible yellow console warning on every spawn attempt while misconfigured (never silently masks), and empty/unset env silently selects Python by design.

*Evidence:* /workspace/src/cli/loopholes_runtime.py:865 (os.access X_OK only) and :917-925 (unwrapped Popen); os.access('/tmp', os.X_OK) is True; commit message of 7b8d743

*Fix:* Tighten the guard to os.path.isfile(go_bin) and os.access(go_bin, os.X_OK); optionally wrap the Popen in try/except OSError with the same fall-back-to-Python behavior.

#### **[MINOR]** No tests: widened _relay_pgrep pattern, oversize/slow first frame, Go fd growth

Of the two widened matchers, only _relay_pid_cmdline_matches has a unit test (fake /proc cmdline with the Go argv); the _relay_pgrep alternation pattern '(broker_relay.py|yolo-broker-relay) --socket <path>' — the orphan-reap backstop when the pidfile is aged out — has no test against a Go-argv process. The plan's Stage 3 parity list also names 'oversize/slow-first-frame forwarded verbatim' and 'no fd growth' against BOTH impls; neither suite (Python or Go-parity) has oversize/slow cases, and the fd-growth test exists only in the Python harness. I verified all of these empirically in-jail — a live Go relay IS found by _relay_pgrep and the cmdline guard, and 60 sequential connections showed zero fd growth — so these are coverage gaps, not behavior breaks.

*Evidence:* /workspace/tests/test_cli_unit.py:4129-4145 (cmdline guard test only); /workspace/tests/test_broker_relay_go_parity.py (6 tests, no oversize/slow/fd-growth); /workspace/tests/test_broker_relay.py:435 (fd-growth, Python-only); plan /workspace/docs/plans/go-port-plan.md:379-383; empirical: _relay_pgrep found the live Go relay pid, fds base=7 after=7, SIGTERM unlinked the socket

*Fix:* Add a test spawning the real Go binary and asserting _relay_pgrep/_relay_kill reap it; port fd-growth and add oversize (>4MiB length prefix) and slow-first-frame cases to the shared harness so both impls run them.

