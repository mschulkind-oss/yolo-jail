# Go-port Stage 11 — Jail-side wave (handoff, partial)

**Status:** 3 of the 4 jail-side binaries ported + tested. Image bake +
per-component flag wiring + nested-jail verification remain (human-gated).
**Plan:** [`docs/plans/go-port-plan.md`](../plans/go-port-plan.md) Stage 11.

## What landed

| Binary | Commit | Source | Notes |
|---|---|---|---|
| `cmd/yolo-ps` | df11330 | `src/yolo_ps.py` | frameproto client; end-to-end verified |
| `cmd/yolo-jail-supervisor` | 5ec30cf | `src/jail_daemon_supervisor.py` | env-in/files-out; ParseEnv cross-lang verified; -race clean |
| `cmd/yolo-oauth-terminator` | 21918ea | `src/oauth_broker_jail.py` | TLS terminator; 2-layer 502 attribution; keep-alive disabled |

## Remaining for Stage 11

- **`cmd/yolo-cglimit` + `cmd/yolo-journalctl`**: the generated Python helper
  scripts → Go binaries with entrypoint-emitted thin exec-wrappers. Not yet
  ported (they're small; the entrypoint wrapper emission is the coupling point
  — better done alongside Stage 10's entrypoint port).
- **Image bake**: all jail-side variants baked, per-component flags correlated
  with `YOLO_ENTRYPOINT_IMPL`, ONE image rebuild for the whole wave
  (`just load && just install` — human action).
- **Verification (human/nested-jail)**: supervisor restart-policy/backoff/
  rotation black-box in a real jail; terminator curl-with-CA parity incl. both
  502 flavors byte-compared; `cmd/yolo-cgd`/`yolo-journald` (Stage 7 Commit B)
  in all-Go vs all-Python jail-side modes; per-component revert without rebuild.

## Verified (in-jail, package + binary level)

- `yolo-ps`: real binary vs a framed fake daemon (mode/pid/jail_id stamped,
  stdout streamed, exit passthrough); both exit-2 error paths.
- `yolo-jail-supervisor`: ParseEnv golden (cross-lang vs Python `_parse_env`),
  restart policies, Run-terminates-always-daemon, 5MB log rotation; `-race`.
- `yolo-oauth-terminator`: relay-vs-broker 502 layer attribution, refresh
  400/200, proxy 502/passthrough, IsRefreshGrant golden; `-race`.

## Human actions

- CI (§10.7); the image rebuild + nested-jail all-Go/all-Python jail-side
  verification; the `cglimit`/`journalctl` helpers pair with the Stage 10
  entrypoint port.


---

## Audit addendum (2026-07-18, planning agent) — multi-agent review of the burst

Findings below are from an 8-auditor review with adversarial verification (two
independent verifiers per blocker/major, each instructed to refute). **Nothing
here was refuted**; several were reproduced live by the verifiers. Fix or ledger
each before this stage's seam flag is flipped on the dev host.

These three binaries are dormant (nothing wired, no image bake) which is
correctly stated — but they are **not ready to bake**. The terminator misses the
single hazard the plan and module map both froze for it (verbatim header names),
auto-negotiates HTTP/2 which voids the keep-alive parity work, drops all logging
(breaking the layer-attribution contract the code's own comment cites), and its
502 strings differ from Python's.

### Findings

#### **[BLOCKER · confirmed]** Terminator canonicalizes proxied response header names; 'verbatim' hazard unhandled

The plan (Stage 11) and module map both freeze 'Go header canonicalization vs Python verbatim passthrough' as a hazard needing deliberate handling. The Go server does the naive thing: writeResult uses w.Header().Set(k, v), which canonicalizes names via CanonicalMIMEHeaderKey. I reproduced it empirically with the identical server construction: upstream 'x-request-id' is written as 'X-Request-Id' (Python send_header writes names byte-verbatim, src/oauth_broker_jail.py:276). Request-side headers are also re-cased (flattenHeaders operates on Go's already-canonicalized r.Header, main.go:86-94, vs Python dict(self.headers) preserving client casing). Additionally Go writes headers key-sorted while Python preserves upstream insertion order, and Python emits a 'Server: BaseHTTP/... Python/...' header Go omits. main.go's doc comment lists 'Frozen hazards handled here' (keep-alive, Content-Length) and silently omits this one; no ledger entry exists (ledger has only D1–D4).

*Evidence:* cmd/yolo-oauth-terminator/main.go:96-113 (writeResult, Set at :105) and :86-94 (flattenHeaders); docs/plans/go-port-plan.md:531-532; docs/research/go-port-module-map/host-services.json:299 and :222; src/oauth_broker_jail.py:267-281,330; docs/design/go-port-divergences.md (no Stage 11 entries); empirical repro: Go server wrote 'X-Request-Id: abc' for Set("x-request-id")

*Fix:* Write response header names verbatim via direct map assignment (w.Header()[k] = []string{v} — net/http emits non-canonical keys as stored); add a wire-level test asserting lowercase upstream names survive to the client byte-for-byte. Ledger (proposed) the residual unavoidable divergences: header write order, absent Server header, HTTP/1.1-vs-1.0 status line, request-side name casing.

#### **[MAJOR→MINOR · confirmed]** Terminator TLS server auto-negotiates HTTP/2 (ALPN h2); Python is HTTP/1.0-only

ListenAndServeTLS with a nil TLSNextProto auto-enables HTTP/2. I empirically confirmed the exact server construction (http.Server + SetKeepAlivesEnabled(false) + ServeTLS) negotiates ALPN 'h2' with an h2-offering client. For such clients (curl, some SDKs) this changes the entire wire surface: multiplexed persistent connection (defeating the deliberate keep-alive-disabled parity the plan bolded), Connection: close stripped (illegal in h2), all header names forced lowercase. Claude Code (undici) is HTTP/1.1-only so it is silent today — but the stage exit criterion 'terminator curl-with-CA parity incl. both 502 flavors byte-compared' will diverge because curl negotiates h2.

*Evidence:* cmd/yolo-oauth-terminator/main.go:53-61 (no TLSNextProto/NextProtos override); docs/plans/go-port-plan.md:533-537 (bolded no-keep-alive-or-ledger rule); src/oauth_broker_jail.py:349-359 (ssl context, no ALPN); empirical probe: 'negotiated ALPN: h2' against the same construction

*Fix:* Set srv.TLSNextProto = map[string]func(*http.Server, *tls.Conn, http.Handler){} (or TLSConfig.NextProtos = []string{"http/1.1"}) to pin HTTP/1.1, and add a test that an h2-offering TLS client negotiates http/1.1.

#### **[MAJOR · confirmed]** 502 layer-attribution strings and errno classification diverge from Python

Three drifts from ask_host_broker: (1) Go labels EVERY dial failure 'relay unreachable' (client.go:43-49); Python restricts relay-layer attribution to ENOENT/ECONNREFUSED and uses the generic 'host broker socket {path}: {e}' for other connect errors (e.g. EACCES, timeout) — Go mis-attributes those to the relay layer. (2) Python's relay message body 'the host-side relay for this jail is down ({path}: …)' is dropped; Go emits only 'relay unreachable — <go dial error>', so the 502 detail bodies cannot byte-compare. (3) Read-phase ECONNRESET/EPIPE: Python maps them to 'connection reset mid-request: …' (oauth_broker_jail.py:142-158); Go's read loop just breaks on any ReadFull error and reports 'connection closed without an exit frame' (client.go:67-69,75-77,93-98) — right layer, wrong frozen string. Also brokerMidRequestErr's generic branch omits the socket path Python includes (client.go:132). main.go:15 claims the detail is 'byte-frozen against the Python handler'; tests only assert prefixes/substrings (oauthterminator_test.go:54,86-88).

*Evidence:* internal/oauthterminator/client.go:43-49,67-69,75-77,93-98,127-133 vs src/oauth_broker_jail.py:104-158; cmd/yolo-oauth-terminator/main.go:15; internal/oauthterminator/oauthterminator_test.go:54,86-93

*Fix:* Restrict relay-layer classification to errors.Is(err, syscall.ENOENT)/ECONNREFUSED on dial; reproduce Python's message templates including socket path and the 'the host-side relay for this jail is down' clause; capture the ReadFull error and route reset-class errnos through the mid-request message. Ledger (proposed) the inherently different OS-error suffix text (Python '[Errno 2] …' vs Go strings).

#### **[MAJOR · confirmed]** Go terminator has zero logging; jail log no longer names the failing layer

The port's own comment states the contract: 'the jail log must say WHICH layer failed' (client.go:41). But neither the package nor the cmd logs anything: no request lines (Python logs every request, refresh OK/failed with layer-named error, proxy status lines — oauth_broker_jail.py:290-297,303-309,312-323,333-339), and host-broker stderr frames are dropped (client.go:83 says 'logged by the caller; ignored here' — no caller logs them; Python log.warning's them at :135-138). In production the terminator's stdout/stderr IS captured (the daemon supervisor writes it to ~/.local/state/yolo-jail-daemons/<name>.log), so Python's log lines are observable today and vanish under the Go binary. The layer attribution now only reaches the HTTP 502 body, not the jail log. -v/--verbose flags are parsed but dead (main.go:40-41).

*Evidence:* internal/oauthterminator/client.go:41,83; cmd/yolo-oauth-terminator/main.go:40-41 (dead flags), whole file has no log/fmt output on the request path; src/oauth_broker_jail.py:135-138,290-339; src/jail_daemon_supervisor.py:100-107 (stdout/stderr → log file)

*Fix:* Add stderr logging mirroring Python's lines (request, refresh failed/OK, proxy result, broker-stderr passthrough) in the same '%(asctime)s LEVEL oauth-broker-jail: msg' shape, wire -v/--verbose to debug level, and surface AskHostBroker's stderr frames to the caller for logging.

#### **[MINOR]** ParseEnv edge cases diverge from _parse_env; 'verified cross-language' overstated

Probed both implementations: {"name":5,"cmd":["x"]} → Python keeps name '5', Go drops the entry; {"restart":null} → Python 'None' (= always-restart semantics since it matches neither 'no' nor 'on-failure'), Go defaults to 'on-failure' (= no restart on exit 0); cmd token null → Python token 'None', Go drops the entry; {"restart":5} → Python '5' (always-restart), Go drops. Commit 5ec30cf claims 'matches _parse_env exactly (verified cross-language)' — the committed golden only covers the simple skip cases (supervisor_test.go:11-44). Mitigation: the only writer (loopholes.py _parse_jail_daemon) validates cmd as non-empty string list and restart against VALID_RESTART_POLICIES before emitting, so these inputs are unreachable from first-party code today.

*Evidence:* internal/supervisor/supervisor.go:58-115 vs src/jail_daemon_supervisor.py:57-78 (probe outputs shown in transcript); internal/supervisor/supervisor_test.go:11-44; src/loopholes.py:539-550; commit 5ec30cf message

*Fix:* Either match Python's str() coercions (accept non-string name/restart, stringify null as 'None') or ledger the stricter parse as a proposed divergence; extend the golden with these cases either way.

#### **[MINOR]** cmd/yolo-ps has no committed test; handoff 'end-to-end verified' is session-only

Commit df11330 lands only main.go (117 lines) — no test file anywhere references yolo-ps. The handoff's 'Verified' section presents it alongside package-tested binaries. I independently reproduced the claim (real binary vs framed fake daemon: request stamped {"jail_id":"testjail","mode":"pid","pid":42}, stdout/stderr streamed, exit-frame 7 passed through, both exit-2 paths with byte-matching frozen messages), so the claim is true — but nothing regression-guards it while the binary sits unwired.

*Evidence:* git show df11330 --stat (only cmd/yolo-ps/main.go); rg for yolo-ps test files: none under internal/, cmd/, or Go-side tests; reproduction transcript (exit=7, REQ JSON, nosock_exit=2, badsock_exit=2)

*Fix:* Commit the fake-daemon e2e as a Go test (a framed unix-socket double is ~40 lines; internal/oauthterminator's serveOnce is a ready template) covering mode/pid/jail_id stamping, exit passthrough, and both exit-2 messages.

#### **[MINOR]** Drift risk of the out-of-order landing is unflagged; CI runs zero Go steps

The three binaries landed before Stages 8/9/10 and will sit unwired until the image bake. They are fully dormant (good), but nothing exercises them continuously: .github/workflows/ci.yml has no go build/test/vet step at all (Go tests exist only in the local Justfile test/test-fast recipes), and the handoff lists 'CI (§10.7)' as a human action without connecting it to the rot risk for this wave. Until CI runs go test, a refactor to internal/frameproto or internal/jsonx could silently break these binaries.

*Evidence:* docs/implementation/go-port-stage-11.md (no drift note); .github/workflows/ci.yml (rg 'go ' → no matches); Justfile:177-190 (go test ./... local only); flake.nix:830 (goBinaries exposed as a package, not in the OCI image)

*Fix:* Add one sentence to the handoff naming the rot risk and its mitigation (CI go-test hookup per §10.7 before or with the Stage 10/11 bake); prioritize the CI Go step among the human actions.

#### **[MINOR]** Timeout semantics tightened: absolute 30s whole-exchange deadline vs Python per-op/none

AskHostBroker sets one absolute deadline for the entire framed exchange (client.go:51) where Python sets a 30s per-recv/send socket timeout (oauth_broker_jail.py:102) — a large action=proxy response trickling for >30s total succeeds in Python and 502s in Go. yolo-ps is stricter still: Python has NO timeout after connect (yolo_ps.py:73-74 blocks indefinitely), Go imposes 30s dial + 30s absolute deadline (cmd/yolo-ps/main.go:71,77) and exits 1 on a slow daemon. Neither change is ledgered.

*Evidence:* internal/oauthterminator/client.go:43,51 vs src/oauth_broker_jail.py:101-102; cmd/yolo-ps/main.go:71,77 vs src/yolo_ps.py:72-79

*Fix:* Emulate per-op timeouts by resetting the deadline before each Write/ReadFull (terminator), and drop/lengthen the deadline in yolo-ps (or ledger both as proposed divergences).

#### **[NIT]** Supervisor parallel teardown vs Python serial; two terminator micro-edges

supervisor.Run tears children down concurrently (supervisor.go:248-253) where Python's signal handler terminates serially (jail_daemon_supervisor.py:183-184) — with multiple hung daemons total grace drops from N×5s to ~5s. Go supervisor also logs nothing, but Python's logs already go to DEVNULL (entrypoint runtime.py:148-149) so that is unobservable today. Terminator micro-edges: chunked request bodies (Python reads Content-Length only → chunked body treated empty, oauth_broker_jail.py:262-264; Go reads the full chunked body, main.go:70) and non-object broker JSON (Python's refresh path would 200 a JSON array; Go 502s with 'host broker returned non-object JSON', client.go:107-111).

*Evidence:* internal/supervisor/supervisor.go:248-253 vs src/jail_daemon_supervisor.py:178-184; src/entrypoint/runtime.py:148-149; cmd/yolo-oauth-terminator/main.go:70 vs src/oauth_broker_jail.py:262-264; internal/oauthterminator/client.go:107-111 vs src/oauth_broker_jail.py:171-174,311

*Fix:* Accept-and-ledger (all are unreachable or benign with first-party clients/brokers); the serial-vs-parallel termination could trivially be made serial to match if strictness is preferred.

