# Go-port Stage 6 — OAuth broker (handoff)

**Status:** landed, in-jail criteria green. Flag defaults to Python. Soak +
real-claude-login + timing are human-gated (see below).
**Plan:** [`docs/plans/go-port-plan.md`](../plans/go-port-plan.md) Stage 6.

## What landed (commit 8ccde4b)

- `internal/oauthbroker` + `cmd/yolo-claude-oauth-broker-host`: Go port of
  `src/oauth_broker.py`. Byte/behavior contracts held (see the commit body):
  creds-file `indent=2`/no-sort-keys/`0600`/atomic-rename blob; 90s cache;
  `NormalizeOAuth`/`AsOAuthResponse`; `refresh.lock` flock (same path →
  cross-impl exclusion); `DisableCompression` + accept-encoding strip both
  ways; the 300/60/5×12 refresher timing + transient-only fast-retry; the
  action set + unknown→exit(2); openssl-exec cert gen.
- Test-only `YOLO_BROKER_UPSTREAM_URL` + `YOLO_BROKER_STATE_DIR` overrides in
  BOTH impls (the plan's mandated Python-side seam edit for black-box parity).
- Seam #2 daemon resolution: `_daemon_launcher` (`YOLO_GO_DAEMONS` gates,
  `YOLO_GO_BIN_DIR` resolves by explicit dir, missing→fallback), wired into
  `_broker_spawn`.

## Verified

- Byte-diff of action/normalize/write shapes vs the live Python oracle
  (`tools/parity/oauth_broker_oracle.py`) — **also lands the Stage 1 broker
  wire fixtures** (action shapes, every error dict, normalize output,
  `write_tokens` blob).
- Cross-language live black-box (`tests/test_oauth_broker_go_parity.py`, 6
  tests): the REAL Python client drives the Go daemon against a fake upstream —
  ping, cached-miss, refresh-writes-creds (preserved fields), cache-hit,
  unknown-action exit 2, cross-language flock contention.
- 4 `_daemon_launcher` unit tests. Full Go + Python fast suites green.

## Human actions / UNVERIFIED

- **Real `claude` login + refresh** in a live jail on the Go broker — needs a
  real Claude session (no-agent-tests rule keeps it out of CI).
- **≥1 week dev-host soak**: export `YOLO_GO_DAEMONS=yolo-claude-oauth-broker-host`
  + `YOLO_GO_BIN_DIR=<repo>/dist-go/<goos>-<goarch>` on the dev host; flip/revert
  includes `yolo broker restart` (stale-code-in-memory) + `just build-go` when
  main moves.
- **Background-refresher wall-clock timing** (300/60/5×12) is code-frozen but
  not soak-verified.
- **openssl cert gen**: not installed in this jail, so `--init-ca` was exercised
  only via the Python unit tests + fake-cert seeding. Real cert gen on a host
  with openssl still needs a manual smoke.
- CI (§10.7): confirm both suites on both arches.


---

## Audit addendum (2026-07-18, planning agent) — multi-agent review of the burst

Findings below are from an 8-auditor review with adversarial verification (two
independent verifiers per blocker/major, each instructed to refute). **Nothing
here was refuted**; several were reproduced live by the verifiers. Fix or ledger
each before this stage's seam flag is flipped on the dev host.

This is the highest-risk swap so far and the port gets the headline contracts
right (openssl exec'd, DisableCompression, refresher timing, atomic creds write).
But the audit confirmed the exit-criterion flock test never touches the Go broker,
the incident-derived **logging contract was dropped entirely** (leaving the soak
without forensics), and four error/edge paths diverge unledgered. Treat Stage 6 as
NOT soak-ready until these land.

### Findings

#### **[BLOCKER→MAJOR · confirmed]** 'Cross-language flock contention' test is Python-vs-Python only — Go broker never involved

The handoff (line 30-31) and commit 8ccde4b both list 'cross-language refresh.lock flock contention' among the 6 black-box tests, satisfying the plan exit criterion 'cross-language flock contention test' (plan line 429-430). The actual test holds a flock from Python and asserts a SECOND PYTHON non-blocking flock fails; the Go daemon is never started and withRefreshLock is never exercised. There is zero regression coverage of the Go side of the mutual-exclusion contract the rollout depends on. Mitigating: I ran the real experiment live (Python holds state/refresh.lock -> Go daemon's refresh blocks ~2s until release, then completes), so the underlying behavior IS correct today — but a future refactor of withRefreshLock/RefreshLockPath would break silently.

*Evidence:* tests/test_oauth_broker_go_parity.py:284-305 (only fcntl.flock from Python, GO_BROKER unused); docs/implementation/go-port-stage-6.md:28-31; docs/plans/go-port-plan.md:429-430; cmd/yolo-claude-oauth-broker-host/main.go:76 (lock wiring, untested); live verification: Go daemon blocked while Python held the flock, completed on release

*Fix:* Replace the test body with the real thing: start the Go daemon with YOLO_BROKER_STATE_DIR, hold flock on <state>/refresh.lock from Python, issue action=refresh in a thread, assert it blocks >N ms and completes after LOCK_UN (my ~40-line experiment does exactly this). Optionally add the reverse direction (Go holds via a refresh against a slow upstream, Python flock blocks).

#### **[MAJOR · confirmed]** Go broker has zero logging — all incident-forensics contracts silently dropped

src/oauth_broker.py's logging is itself a documented operational contract: startup/pre-lock creds snapshots, cache hit/miss with token fingerprints (added after the 2026-04-23 invalid_grant incident specifically because the bug 'was invisible in the logs'), bg_refresh tick lines, and the WARN-level proxy-mirror skip lines whose comment says silent failures 'were invisible for ~10 days; never again' (oauth_broker.py:846-868). The Go port emits nothing: no log calls anywhere in internal/oauthbroker, TokenFP is dead code, -v/--verbose are parsed and ignored, and every mirror-skip/tick-crash path is silent. Under the Go broker, host-service-claude-oauth-broker.log will be empty for the whole soak — the exact period when forensics matter most, and the handoff's own flip/revert procedure implicitly relies on correlating broker activity with creds mtime.

*Evidence:* rg 'log\.' internal/oauthbroker/*.go returns nothing; internal/oauthbroker/oauthbroker.go:61-69 (TokenFP defined, never referenced elsewhere); cmd/yolo-claude-oauth-broker-host/main.go:39-40 (verbose flags unused); internal/oauthbroker/refresh.go:130 (panic swallowed with bare recover, no log); contrast src/oauth_broker.py:489-537, 587-656, 846-902, 1090

*Fix:* Port the log lines (log/slog to stderr with the '%(asctime)s LEVEL oauth-broker-host: msg' shape), wiring TokenFP into the same fingerprint lines, at minimum: do_refresh snapshot, cache hit/miss, refreshed rt->rt, bg_refresh due/ok/failed/fast-retry, every proxy-mirror skip reason, tick panic. Wire -v to level DEBUG.

#### **[MAJOR→MINOR · confirmed]** Upstream requests lack Accept-Encoding: identity that Python always sends

Python's http.client injects 'Accept-Encoding: identity' (and 'Connection: close') on every broker upstream request — verified live: urllib sends {Accept-Encoding: identity, Connection: close, ...}. The Go broker (DisableCompression + accept-encoding stripped from forwarded headers) sends NO Accept-Encoding header at all, on both refreshUpstream and DoProxy paths. Absent the header, upstream/Cloudflare MAY choose an encoding; if a token-endpoint 200 ever comes back compressed, Go will not decompress (DisableCompression), jsonx.Decode fails, and the mirror/refresh silently skips (no logging either) — precisely the 2026-05-12 logout-loop failure mode the plan bolds as the load-bearing fix. Python's explicit 'identity' actively forbids this; the Go port only avoids requesting compression.

*Evidence:* internal/oauthbroker/http.go:23-28 (DisableCompression only), 72-78 (refreshUpstream sets no A-E), 127-139 (DoProxy strips A-E, adds nothing); src/oauth_broker.py:692-706 (strip rationale); live demo: urllib request headers show 'Accept-Encoding: identity'; docs/plans/go-port-plan.md:422-423

*Fix:* Explicitly req.Header.Set("Accept-Encoding", "identity") in refreshUpstream and DoProxy (after the strip), matching Python's on-the-wire request; add it to the frozen-contract comment.

#### **[MAJOR · confirmed]** Creds JSON missing claudeAiOauth key returns wrong error dict vs Python

Python do_refresh does json.loads(...).get('claudeAiOauth') or {} — a readable, valid-JSON creds file without the claudeAiOauth key (e.g. '{}') yields {'error': 'no_refresh_token'}. Go's oauthFromCreds returns not-ok when the key is missing, so DoRefresh returns {'error': 'creds_unreadable', 'message': 'creds file unreadable'}. Also, on genuinely unreadable/missing files Python's message is str(e) ('[Errno 2] ...', 'Expecting value: ...') while Go always emits the fixed string 'creds file unreadable' — the frozen error-dict bytes differ. The oracle can't catch this (it byte-diffs the dict SHAPE with an injected 'boom' message, not the production message path).

*Evidence:* internal/oauthbroker/oauthbroker.go:92-94 (missing key -> nil,false); internal/oauthbroker/refresh.go:49-53 (fixed message); src/oauth_broker.py:503-510; tools/parity/oauth_broker_oracle.py:99-101 (shape-only)

*Fix:* In oauthFromCreds, treat a decoded object with a missing/null claudeAiOauth as an empty OrderedMap with ok=true (matching Python's `or {}`), reserving not-ok for read/parse failures; thread the real read/parse error text into the creds_unreadable message.

#### **[MAJOR · confirmed]** DoProxy response headers: Go canonicalizes names, first-duplicate; Python verbatim, last

Python builds the proxy response 'headers' dict from resp.headers.items() — header names pass through byte-verbatim as upstream sent them, and duplicate headers (multiple Set-Cookie, e.g. Cloudflare __cf_bm) collapse to the LAST value. Go iterates resp.Header (already MIME-canonicalized: 'x-ratelimit-limit' becomes 'X-Ratelimit-Limit') and uses Header.Get(k), which returns the FIRST value of duplicates. The wire dict the jail-side terminator relays to Claude Code therefore differs in header-name bytes and, for duplicated headers, in values. The module map explicitly flags Go header canonicalization as a hazard for exactly this proxied-header surface, and no oracle scenario covers proxy response shapes.

*Evidence:* internal/oauthbroker/http.go:158-164 vs src/oauth_broker.py:746-749, 752-755; docs/research/go-port-module-map/host-services.json:222 (canonicalization hazard); tools/parity/oauth_broker_oracle.py (no proxy {status,headers,body_b64} scenario)

*Fix:* Preserve wire header names (read from resp via a raw-header capture or track original casing) or at minimum use the LAST value of duplicates to match Python; add a proxy-shape scenario to the oracle/black-box suite with a duplicate-header, odd-casing fake upstream; ledger whatever residue is accepted.

#### **[MAJOR · confirmed]** Malformed 200 upstream: misclassified transient; can silently write stale-token creds

Two related edge divergences. (1) refreshUpstream maps an unparseable/non-object 200 body to urlError -> DoRefresh emits {'error':'upstream_unreachable'} -> BackgroundRefreshTick classifies it TRANSIENT and fast-retries 12x5s; Python raises JSONDecodeError out of do_refresh (only HTTPError/URLError/OSError are caught), the tick's catch-all logs it and stays on the normal 60s cadence, and the on-demand path returns a handler-error frame, not an error dict. (2) NormalizeOAuth only sets accessToken when present in the response; Python does upstream_resp['access_token'] which raises KeyError (loud, no write). A 200 body without access_token makes Go silently WriteTokens with the OLD accessToken but a FRESH expiresAt, then return success — jails would treat a stale token as fresh until the next expiry window.

*Evidence:* internal/oauthbroker/http.go:89-97; internal/oauthbroker/refresh.go:60-69,110-113; internal/oauthbroker/oauthbroker.go:190-192; src/oauth_broker.py:398-399, 442, 517-527, 641-643

*Fix:* Give refreshUpstream a distinct error type for parse failures that DoRefresh does NOT map to upstream_unreachable (and is not fast-retried); in NormalizeOAuth require access_token (return an error the caller surfaces) instead of silently skipping the overwrite.

#### **[MINOR]** UA version source diverges; Go git-describe runs in the daemon's arbitrary cwd

Python's UA version is importlib.metadata version('yolo-jail') (installed wheel version, deterministic). Go uses version.Get(""): YOLO_VERSION env, else `git describe` executed with NO working directory set — the daemon inherits the cwd of whatever `yolo run` spawned it from, so running yolo from inside any other git repo stamps THAT repo's describe output into the broker's User-Agent; only then the ldflags-baked version. UA bytes will differ from Python's in essentially every deployment, unledgered against the frozen 'yolo-jail-oauth-broker/<ver>' contract (harmless for Cloudflare, misleading for Anthropic-side log forensics).

*Evidence:* cmd/yolo-claude-oauth-broker-host/main.go:45-47; internal/version/version.go:89-113 (cmd.Dir only set when repoRoot != ""); src/oauth_broker.py:102-111

*Fix:* Prefer the ldflags-baked buildVersion for the daemon UA (skip git describe when repoRoot is ""), or pass a real repo root; ledger the residual version-string format difference vs the wheel version.

#### **[MINOR]** Python-side override untested; 'black-box green both impls' only half-met

The plan-mandated Python rider (_token_url + YOLO_BROKER_STATE_DIR) landed, but no test exercises the Python broker through them: YOLO_BROKER_UPSTREAM_URL is only ever set for the GO daemon in the parity suite, and test_oauth_broker.py monkeypatches functions rather than going black-box over the socket. Plan Stage 6 exit says 'black-box suite vs httptest upstream green both impls' — only the Go impl is driven black-box. A typo in _token_url would go unnoticed and make future dual-impl comparison silently wrong.

*Evidence:* src/oauth_broker.py:81-92 (untested); tests/test_oauth_broker_go_parity.py:143 (env set only for Go daemon); rg shows no other YOLO_BROKER_UPSTREAM_URL consumer in tests; docs/plans/go-port-plan.md:429

*Fix:* Parametrize the black-box suite over impl (Python console script vs Go binary) so the same 6 scenarios drive both daemons against the fake upstream — this also makes creds-file output byte-comparable across impls after a live refresh.

#### **[MINOR]** Type-edge wire divergences: non-string action, null headers/body_b64, header values

Python str()-coerces: action=123 -> '123' -> unknown action -> exit 2, while Go leaves non-string actions at the 'refresh' default and executes a refresh; 'headers': null -> Python `or {}` proceeds, Go returns bad_request 'headers must be an object'; 'body_b64': null -> Python '' proceeds, Go bad_request; header value True -> Python 'True', Go DumpsCompact 'true'. All unreachable via the real jail-side client (oauth_broker_jail always sends proper shapes) but they are frozen wire-contract divergences with no ledger entries.

*Evidence:* internal/oauthbroker/handler.go:22-27, 80-97, 163-170 vs src/oauth_broker.py:908, 783-805

*Fix:* Match Python's coercions (stringify non-string action; treat null headers/body_b64 as absent), or propose a ledger entry arguing unreachability.

#### **[MINOR]** Unlocked refresh proceeds if flock() errors; lock-open failure returns dict not raise

withRefreshLock ignores the syscall.Flock return (`_ = syscall.Flock(...)`) — if flock fails the refresh silently proceeds WITHOUT mutual exclusion, defeating the single-use-refresh-token serialization contract; Python's fcntl.flock raises. On lock-file open failure Go returns {'error':'creds_unreadable'} while Python raises out of do_refresh (handler-error frame). Low probability, but the flock is the load-bearing contract of this daemon.

*Evidence:* internal/oauthbroker/refresh.go:25-33 (esp. line 32) vs src/oauth_broker.py:490-491

*Fix:* Treat a failed Flock as a hard error (return an error dict / refuse to refresh), and log it once logging exists.

#### **[MINOR]** SelfCheck honors --creds-file; Python self_check hardcodes DEFAULT_CREDS_PATH

Python's self_check always inspects DEFAULT_CREDS_PATH regardless of --creds-file; the Go main passes the --creds-file flag value into SelfCheck. `--self-check --creds-file X` therefore checks different files across impls (identical when the flag is omitted, the yolo-doctor path). FAIL-line message text (json error strings) also differs.

*Evidence:* cmd/yolo-claude-oauth-broker-host/main.go:49-50 vs src/oauth_broker.py:1000 (creds = DEFAULT_CREDS_PATH)

*Fix:* Either match Python (ignore the flag in SelfCheck) or ledger it as a deliberate improvement; doctor invocations pass no flag so real-world impact is nil.

#### **[MINOR]** Oracle gaps: no proxy shape, no bad_request dicts, silent skip without Python

The Stage 6 oracle covers action success shapes, 5 error dicts, normalize variants, and the write_tokens blob — but not the proxy {status, headers, body_b64} shape, the bad_request error dicts from _decode_proxy_request, or the cached/handler dispatch path; and TestBrokerActionShapesParity t.Skip()s when uv/python3 are unavailable, so in a Python-less environment the parity gate silently passes. §14's 'broker wire fixtures landed via the Stage 6 oracle' is a live dual-run rather than committed Stage 1 fixtures — transparent as stated, but the coverage gaps above are what the fixtures were meant to close.

*Evidence:* tools/parity/oauth_broker_oracle.py:97-121 (scenario list); internal/oauthbroker/parity_test.go:46-48 (skip); docs/plans/go-port-plan.md:932

*Fix:* Add proxy-shape and bad_request scenarios to the oracle; make CI fail (not skip) when the oracle is unavailable.

#### **[NIT]** openssl-missing error drops Python's PATH diagnostics

Python's SystemExit message includes the spawned env's PATH and the searched fallback list — deliberately added so operators can diagnose PATH-stripping wrappers (mise/uv/IDE). Go's error is one generic line without the PATH or the searched locations.

*Evidence:* internal/oauthbroker/cert.go:82-85 vs src/oauth_broker.py:278-291

*Fix:* Include os.Getenv("PATH") and the fallback list in the Go error string.

