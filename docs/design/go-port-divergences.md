# Go-port divergence ledger

The single authoritative list of INTENTIONAL, human-approved behavior
divergences between the Go port and the Python source of truth. QA batch docs
(`docs/qa/go-port-batch-*.md`) *propose* entries; this file *records* the
approved ones (go-port plan §1.1).

Every entry states: the divergence, the exact input that triggers it, why it is
accepted (reachability argument — the input cannot occur in real yolo-jail
operation, or the cost of matching is disproportionate), and the guarding test.

Status legend: **proposed** (awaiting human sign-off) · **accepted**.

---

## D1 — jsonx: bare `Infinity`/`NaN`/`-Infinity` literals are not decoded

**Status:** proposed (QA batch 1, finding #5-adjacent)

**Divergence.** `internal/jsonx.Decode` is built on Go's `encoding/json` token
stream, which rejects the non-standard constants `Infinity`, `-Infinity`, and
`NaN` at tokenization. Python's stdlib `json.loads` accepts them (via
`parse_constant`) → `inf`/`-inf`/`nan`, and `internal/jsonx` DOES encode those
float values back to those literals. So Go can emit them but not decode them.

- Input: `Decode([]byte("Infinity"))` (or nested `[NaN, Infinity]`).
- Go: error (`invalid character 'I' looking for beginning of value`).
- Python: `json.loads("Infinity") == inf`.

**Why accepted.** `jsonx.Decode` decodes exactly two kinds of real input: (a)
config-snapshot bytes that `jsonx` itself wrote (which only contain non-finite
literals if a config value was already a non-finite float — and config values
come from JSONC via pyjson5/`internal/json5`, not stdlib json); (b) broker/
loophole request bodies (small `{"action": ...}` JSON objects from Claude Code
and yolo-ps — never bare non-finite numbers). Neither carries `Infinity`/`NaN`.
Note the config parser pyjson5 accepts `Infinity`/`NaN` but ERRORS on the
overflow literal `1e400` (unlike stdlib json), so the config path has its own
behavior captured in `internal/json5`, not here. Matching would require
replacing the `encoding/json` token stream with a full custom scanner solely
for inputs that never occur.

**Overflow IS fixed, not ledgered:** `1e400` → `Infinity` and integer `-0` → `0`
are on the common number path and were fixed (jsonx `numberToValue`,
`TestDecodeNumberNormalizations`) — this entry covers only the BARE-literal
decode.

**Guard.** `internal/jsonx` documents the limitation; if a future consumer
needs bare-literal decode, promote to a fix (custom scanner or delegate to
`internal/json5`, which is a full parser).

---

## D2 — jsonx: lone unpaired surrogate is replaced with U+FFFD on decode

**Status:** proposed (QA batch 1, finding #5)

**Divergence.** For a JSON string containing a lone surrogate escape
(`"\ud800"`), Go's `encoding/json` substitutes U+FFFD (replacement char); the
ensure-ascii encoder then emits `"�"`. Python's `json` preserves the lone
surrogate and re-emits `"\ud800"`.

- Input: `Decode([]byte("\"\\ud800\""))`.
- Go: `"�"` (both DumpsSnapshot and DumpsCompact).
- Python: `"\ud800"`.

**Why accepted.** Lone surrogates are malformed Unicode; they never appear in
yolo-jail config values, agent registry data, or loophole request bodies (all
of which are well-formed UTF-8). `encoding/json`'s substitution is documented Go
behavior. Matching would require a custom string decoder that preserves invalid
surrogates through the Go string type (which can't natively hold them).

**Guard.** Documented here; no real input triggers it.

---

## D3 — pytext: Unicode table version skew (Go 15.0.0 vs Python 15.1.0)

**Status:** proposed (QA batch 1, finding #7)

**Divergence.** `internal/pytext.Repr` uses Go's `unicode.IsPrint` to decide
whether a non-ASCII rune stays literal or is `\u`-escaped, mirroring Python's
`str.isprintable()`. Go's `unicode` package and CPython track different Unicode
versions (observed: Go 15.0.0 vs Python 15.1.0). For code points assigned
printable in 15.1.0 but not present in 15.0.0 (e.g. U+2FFC–U+2FFF, U+31EF,
U+2EBF0–U+2EE5D), Go escapes them while Python keeps them literal.

- Input: `Repr("⿼")` (U+2FFC).
- Go: `'⿼'` (escaped).
- Python: `'⿼'` (literal).

**Why accepted.** The affected code points are newly-assigned CJK/ideographic
description characters that do not appear in yolo-jail's validation error
strings (the only consumer of `pytext.Repr` — it renders `{x!r}` for config
paths, package names, loophole names, and env keys, which are ASCII or common
scripts). This is a Go-toolchain property, not something the port controls; it
self-heals when Go's `unicode` package catches up to Python's Unicode version.

**Guard.** Documented here; the pytext cross-language parity test covers the
common (pre-15.1.0) code-point range that real inputs use.

---

## D4 — paths: fixed by adding a pwd fallback (NOT ledgered)

Recorded here only to note it was a **fix**, not an accepted divergence: with
`HOME` unset/empty, Python's `Path.home()` falls back to `pwd.getpwuid` (or `/`
for empty `HOME`), keeping paths absolute; the initial Go port returned relative
paths. Fixed in `internal/paths` (see the fix commit). Listed so the audit
trail is complete.

---

## D5 — oauth broker/terminator: proxied response header NAME casing

**Status:** proposed (audit addendum, Stage 6 + Stage 11)

**Divergence.** The OAuth broker's `DoProxy` reads upstream response headers via
Go's `net/http`, which stores header keys canonicalized (`CanonicalMIMEHeaderKey`:
`x-request-id` → `X-Request-Id`). Python builds the dict from
`resp.headers.items()` with names byte-verbatim as upstream sent them. Duplicate
values ARE now matched (both take the last; fixed). Only the NAME casing of the
broker→relay proxy dict differs.

- Input: upstream sends `x-request-id: abc`.
- Go broker dict: `{"X-Request-Id": "abc"}`.
- Python broker dict: `{"x-request-id": "abc"}`.

**Why accepted.** The jail-side terminator (`cmd/yolo-oauth-terminator`) now
writes response header NAMES verbatim to the client (the Stage 11 BLOCKER fix,
via direct map assignment), so what Claude Code ultimately sees is governed by
the terminator, not this intermediate broker dict. Capturing raw upstream header
names in Go requires bypassing `net/http`'s reader (a custom response parser)
for a difference that doesn't reach the client. The values (incl. duplicate
collapse) match; only the intermediate dict's key casing differs.

**Guard.** The terminator's verbatim-header wire test pins the client-visible
behavior; a broker-side proxy-shape oracle scenario is a follow-up.

---

## D6 — broker relay: first-message read timeout is whole-frame, not per-recv

**Status:** proposed (audit addendum, Stage 3)

**Divergence.** Python's `_read_first_message` sets `settimeout(5.0)`, which
applies PER `recv()`; a client dripping bytes slower than one per 5s never times
out and still gets its `jail_id` stamped. Go sets a single absolute
`SetReadDeadline(now+5s)` for the whole frame, so a slow-dripping client is
downgraded to verbatim-unstamped forwarding after 5s total.

**Why accepted.** Real clients (the jail-side terminator) send the framed
request in ONE write; a byte-dripping client is not a real corridor. The only
consequence is losing host-side `jail_id` attribution in the broker log for such
a client — no traffic is dropped (the relay forwards verbatim and keeps working).

**Guard.** Documented; the relay parity suite covers the one-shot path.

---

## D7 — hostservice: exec spawn-failure stderr text (OS-string residue)

**Status:** proposed (audit addendum, Stage 4)

**Divergence.** When `ExecAllowlisted`'s child binary can't be started, both
impls now emit a `handler error: <e>\n` stderr frame + exit(1) + access-log
rc=1 (the Go port panics so `handleOne`'s recover reproduces Python's
Popen-raises path — fixed). The residue is the `<e>` TEXT: Go's
`fork/exec …: no such file or directory` vs Python's `[Errno 2] No such file or
directory: …`.

**Why accepted.** The exit code, frame shape, stream, and access-log rc all
match; only the human-readable OS-error string differs, and it's reachable only
via a missing/broken daemon-controlled `argv[0]` (e.g. `ps` absent). Matching
CPython's exact `[Errno N]` text is not worth a translation layer.

**Guard.** Documented; the handler-error frame shape is conformance-tested.

---

## D8 — entrypoint boot: a failing content generator warns-and-continues instead of aborting

**Status:** proposed (Stage 10)

**Divergence.** Python's `entrypoint.main()` calls the `generate_*` functions
(shims, bashrc, bootstrap, venv-precreate, mise config, mcp-wrappers, the four
helper scripts) WITHOUT a surrounding `try/except`. An exception from any of
them (e.g. an `OSError` writing a file) therefore propagates out of `main()`,
the process exits non-zero, and boot ABORTS before `exec_bash` — the jail never
gets a shell. (The `configure_*` agent writers, by contrast, already swallow
their own exceptions internally in Python and print `Error configuring X`.)

The Go `Main()` wraps every generator in `genStep`, which prints a
`Warning: <label>: <err>` line to stderr and CONTINUES to the final
`exec_bash`. So where a mid-boot generator IO error kills the Python jail, the
Go jail warns and still drops the user into bash (with that one artifact
possibly missing).

- Input: a generator returns a non-nil error mid-boot (e.g. `$HOME` read-only,
  ENOSPC, a bind-mounted target that has become unwritable).
- Python: exception propagates → boot aborts, no shell.
- Go: warn to stderr → boot continues → shell starts anyway.

**Why accepted.** This is the plan's explicit, bolded directive for Stage 10:
"best-effort never-abort-boot semantics per step (Go's error idiom invites
accidental fail-fast — boot must never abort)". Aborting boot on a single
best-effort artifact is strictly worse operationally than starting the shell
without it; the CA-bundle / PATH env is already set before the generator block,
so the shell is usable. In real jails these generators do not fail (fresh
per-workspace writable overlays); the divergence is only reachable under a
genuine filesystem fault, where "give me a shell to debug it" beats "no jail".
The two subprocess side effects Python itself makes best-effort (mise
uninstall, claude plugins) are unaffected — they were already never-abort on
both sides.

**Guard.** Documented; the never-abort wrapper is `genStep` in
`internal/entrypoint/boot.go`. The happy-path tree parity (Stage 9 golden +
in-jail dual-arm byte compare) proves no divergence when generators succeed,
which is every real boot.

---

## D9 — loopholes: JSON-syntax-error message body differs (json5 vs pyjson5 text)

**Status:** proposed (Stage 14, internal/loopholes)

**Divergence.** `_load_manifest` wraps a parse failure as
`LoopholeError(f"{manifest_path}: {e}")` where `{e}` is the underlying parser's
exception string. Python uses `pyjson5.Json5Exception`; the Go port uses
`internal/json5`. The file-path prefix is byte-identical, but the trailing
parser-error TEXT differs.

- Input: a malformed `manifest.jsonc` (e.g. `"{not valid json"`), surfaced via
  `validate_loopholes` (the third tuple element).
- Python: `…/manifest.jsonc: ("Expected b'colon' near 6, found U+0076", {}, 'v')`.
- Go: `…/manifest.jsonc: json5: expected ':' after object key at offset 5`.

**Why accepted.** This is exactly the `internal/json5`-vs-`pyjson5` error-text
gap already ledgered in spirit for the config path — the two hand-written
parsers report syntax errors in their own words. `discover_loopholes` and
`_load_from_dir` SKIP malformed manifests silently (they only catch
`LoopholeError` to drop the entry), so the differing text is user-visible only
through `yolo loopholes` diagnostics (`validate_loopholes`), never in the
runtime wiring path. Real bundled/user manifests parse cleanly on both sides
(proven by the bundled-manifest parity tests); matching pyjson5's exact
tuple-repr wording would require reimplementing its error format solely for
manifests an author is actively fixing.

**Guard.** `TestValidateParity` compares the full error string for every
*structural* (post-parse) validation failure byte-for-byte — those DO match; it
deliberately does not assert on raw JSON-syntax bodies. The bundled manifests'
clean-parse parity is guarded by `TestBundledManifestsParse` +
`TestBundledAudioParity`/`TestBundledBrokerParity`.

---

## D10 — loopholes: a non-object manifest is a skippable error, not an AttributeError crash

**Status:** proposed (Stage 14, internal/loopholes)

**Divergence.** After a successful parse, `_load_manifest` calls
`data.get("name")` with no check that the top-level JSON value is an object. A
manifest whose top-level value is a list/number/string/bool parses fine, then
`data.get(...)` raises an uncaught `AttributeError` ('list' object has no
attribute 'get') — which is NOT a `LoopholeError`, so it propagates out of
`discover_loopholes` / `validate_loopholes` and aborts the whole scan. The Go
port type-asserts the decoded value to `*jsonx.OrderedMap` and, on failure,
returns a `LoopholeError` ("manifest must be a JSON object"), so the entry is
skipped (discover) or reported (validate) like any other malformed manifest.

- Input: `manifest.jsonc` containing e.g. `[1, 2, 3]`.
- Python: `AttributeError` escapes → `discover_loopholes` raises → the scan
  aborts (every other loophole in the dir is lost too).
- Go: the offending dir yields a skippable/reportable `LoopholeError`; sibling
  loopholes still load.

**Why accepted.** The Go behavior is strictly more robust and matches Python's
INTENT for every other malformed-manifest shape (skip silently in discovery,
report in validate). A top-level non-object manifest cannot occur in a real
authored loophole (the schema is an object with a required `name`); the only way
to hit it is hand-writing a syntactically-valid-but-wrong-shape manifest, at
which point "skip the bad one, keep the rest" beats "crash the entire loophole
scan and take down `yolo run`/`yolo loopholes`". Reproducing the crash would
mean deliberately re-introducing an unhandled-exception path.

**Guard.** Documented; the guard is the `decoded.(*jsonx.OrderedMap)` assertion
in `loadManifest` (and the mirror in `SetEnabled`). No parity test asserts the
crash (it is the accepted improvement); the skippable-error path shares the same
`LoopholeError` plumbing that `TestValidateParity` covers for other shapes.

## D11 — ps: runtime detection + never-prune-on-unconfirmed-empty (WITHDRAWN — the bug was FIXED)

**Status:** WITHDRAWN. The original D11 proposed *accepting* a shallow-runtime
narrowing; the 2026-07-18 re-audit (§B/D11) correctly found that unsound (it was
destructive, not benign), so it is not a divergence to accept — it was a BUG,
now fixed. No divergence remains to sign off.

**The bug (audit §B/D11, confirmed live).** Native `yolo ps` resolved the runtime
via the shallow `runtime.DetectRuntime` (`YOLO_RUNTIME` or literal "podman"), with
no platform awareness. On a macOS host running Apple Container with `YOLO_RUNTIME`
unset, it ran `podman ps` → empty → then `PruneStaleTrackingFiles(empty)`,
**deleting the tracking files of live AC jails** while printing "No running
jails." The original D11 text wrongly called `ps` "read-only" and scoped the
effect to "no reachable runtime".

**Fix (landed).**
1. **Platform-aware runtime** — `runtime.PsRuntime(isMacOS, hasBinary)` mirrors
   Python `_runtime()`'s candidate order (macOS: container→podman; Linux: podman;
   `YOLO_RUNTIME` overrides), so ps talks to the runtime that actually has the
   jails. `DetectRuntime` (shallow) stays for prune/check.
2. **Tri-state enumeration** — the ps probe seam now returns `(out, ok)`; `ok=false`
   ("could not enumerate", the None-vs-empty polarity) makes ps DECLINE to prune
   and print "Could not query the <rt> runtime…" instead of "No running jails."
   Pruning happens ONLY on a confirmed-empty enumeration.

**Guard.** `internal/pscmd` regression tests: `TestPsEnumerationFailureDoesNotPrune`
(a failed probe never deletes tracking files, never claims "No running jails") and
`TestPsRuntimePlatformAware` (macOS prefers container). The full `_runtime()`
connectivity-probe + `sys.exit` is still the run path's concern (unrelated to ps
safety); if a future ps needs it, that is a separate, non-destructive follow-up.

---

## Re-audit challenges (2026-07-18) — human adjudication needed

The 2026-07-18 re-audit (`docs/implementation/go-port-audit-2026-07-18.md`) flags
issues with the entries above and lists divergences that were never ledgered.

**D11 is UNSOUND as written** (audit §B1). Its acceptance rests on two false
premises: (1) it calls `ps` "read-only", but `ps` calls
`PruneStaleTrackingFiles` and DELETES tracking files; (2) it scopes the effect to
"no reachable runtime", but on macOS with Apple Container running and
`YOLO_RUNTIME` unset the shallow `DetectRuntime()` picks `podman`, which is
reachable, returns empty, prints "No running jails" while AC jails are live, and
then prunes their tracking files. Reject or rewrite D11; do not sign it off as-is.

**Unledgered behavior divergences the audit confirmed (need proposed entries or
fixes):** repo-root cwd-walk + `YOLO_REPO_ROOT`-required vs Python `__file__`
anchoring (audit §A B2); bundled-loopholes `/opt/yolo-jail` fallback (§A B3);
`ca_cert` absolute-path `filepath.Join` vs pathlib `/` (§C); tree-mode timeout
stderr text; the malformed-200 `upstream_bad_response` invented error code;
stdin-EOF (the plan required a ledger entry if observable behavior differs — it
does); terminator `HTTP/1.1`+`Connection` vs Python `HTTP/1.0`+`Server`;
`hostPlatform` `arm64→aarch64` on macOS; the `host_pi_files` config-key rejection.

**Process note:** D8 and D10 are behavior changes that shipped before the human
sign-off §1.1 requires *first*; all of D1–D11 remain `proposed`.

---

## D12 — repo-root resolution: cwd-walk requires the yolo-jail marker (audit §B2 fix)

**Status:** proposed (the FIX is landed; the residual behavior needs sign-off).

Python's `_resolve_repo_root` anchors to the package `__file__`. The Go binary has
no `__file__`, so it (1) trusts `YOLO_REPO_ROOT` when set, then (2) walks up from
cwd. The audit found the cwd-walk matched ANY `flake.nix`, which would hijack a
user's own flake workspace as the yolo-jail repo (the `nix build .#ociImage`
target, bind-mounted `:ro`). **Fixed** (`internal/runcmd/probes.go`,
`internal/checkcmd/probes.go`): the cwd-walk now requires BOTH `flake.nix` AND
`src/entrypoint/__init__.py` (the same yolo-jail marker the env-var + staging
branches use), with a regression test (`TestResolveRepoRootDoesNotHijackBareFlake`).

**Residual divergence (this ledger entry):** off a source checkout and without
`YOLO_REPO_ROOT`, the Go binary cannot self-locate its bundled source the way
Python's `__file__` does — it relies on `YOLO_REPO_ROOT` (set by the jail shim /
`go-front-door.sh`) or the installed-wheel staging. A Go distribution must ship
`share/yolo-jail/` beside the binary (step 3) or the shim must set the env. This
is why `YOLO_IMPL=go` is only safe via the four-var `go-front-door.sh`, not a bare
export. Accept (with the shim as the documented enablement) or require a Python-
equivalent `os.Executable`-relative self-resolve before default flip.

---

## D13 — broker restart: an unlaunchable broker binary degrades to exit 1, not a crash

**Status:** proposed (Stage broker — `internal/brokercmd` / `internal/brokerlifecycle`).

**Divergence.** Python's `_broker_spawn` calls `subprocess.Popen([*launcher,
"--socket", ...])` with **no** surrounding `try/except`. If the launcher token
cannot be executed — the console script isn't on PATH, or a `YOLO_GO_DAEMONS`-
gated Go binary was resolved but is unexecutable between the `os.access` check
and the `Popen` — `Popen` raises `FileNotFoundError`/`OSError`, which propagates
out of `_broker_spawn` → `broker_restart_cmd`, so `yolo broker restart` exits
with an **uncaught-exception traceback** (non-zero, but not the graded exit 1).
The Go `BrokerSpawn` treats a spawn error as best-effort: it returns the socket
path, `BrokerIsAlive` then reports the broker did not come up, and `Restart`
prints `Broker failed to become live after spawn.  Check <log>` and returns the
graded **exit 1**.

- Input: `yolo broker restart` when the broker launcher (`yolo-claude-oauth-
  broker-host`) is absent/unexecutable.
- Python: `Popen` raises → traceback, no health-hint line.
- Go: exit 1 + the `Check <log-path>` hint line (the same message Python prints
  only when the process launched but failed to bind).

**Why accepted.** The Go path is strictly more graceful and lands on the SAME
graded outcome the restart command already documents for the launch-but-no-bind
case (exit 1 + the log-path hint) — it just also absorbs the never-launched
case, which in Python is an unhandled crash. In real operation the broker binary
is always present (it ships in the wheel / the console-scripts entry point, and
the Go-gated path only fires when `YOLO_GO_DAEMONS` lists it AND
`$YOLO_GO_BIN_DIR/<name>` is executable), so neither impl hits this outside a
broken install. `DaemonLauncher` already prints the "using the Python daemon"
warning for the missing-gated-binary sub-case, matching Python. Reproducing the
crash would mean deliberately re-introducing an unhandled-exception path in a
command whose whole job is operational hygiene.

**Guard.** Documented; `internal/brokercmd.TestRestartFailure` pins the exit 1 +
log-path-hint outcome (via a spawn that never binds), and
`internal/brokerlifecycle.TestBrokerSpawn*` cover the spawn-error /
already-alive / stale-socket / dead-child branches. The byte-exact spawn argv is
parity-tested against live Python (`TestParityVsLivePython`).

---

## D14 — prune: TIED disk-breakdown display lines are name-ordered, not iterdir-ordered

**Status:** proposed (Stage 14 prune — `internal/prunecmd`).

**Divergence.** `prune_cmd` renders the global-storage breakdown and the cache
top-5 with `sorted(items, key=lambda kv: kv[1], reverse=True)` — a STABLE sort
keyed only on the byte value, so entries with EQUAL byte totals keep their
relative order from the source dict, which was built by iterating
`Path.iterdir()` (filesystem-arbitrary, non-reproducible across hosts/runs).
The Go `sortByValueDesc` sorts by value descending and breaks exact-value ties
by NAME ascending (deterministic), because Go map iteration is randomized and an
unbroken tie would render nondeterministically.

- Input: a `GLOBAL_STORAGE` whose two direct children have byte-identical
  totals (e.g. `cache` and `home` both exactly N bytes) — only the two display
  lines' ORDER can differ.
- Python: whatever order `iterdir()` yielded them (arbitrary, run-dependent).
- Go: the tied pair is ordered by name (`cache` before `home`).

**Why accepted.** This is display-order ONLY, strictly inside the Stage 14/15
prune output contract (the byte-exact obligations are the reclaim DECISIONS, the
`FmtBytes` numbers, and the removed-NAME lists — all of which are unaffected: a
tie means equal bytes, and the same set of names is printed either way). Python's
own order is non-deterministic (a re-run can reorder the tied lines), so there is
no stable Python behavior to match; the Go order is a deterministic
tie-break that makes the report reproducible. Byte totals in each line are
identical. Distinct-value breakdowns (the overwhelmingly common case) sort
identically in both impls and ARE covered by the differential parity test.

**Guard.** Documented; `internal/prunecmd.TestParityVsLivePython` drives the live
`prune_cmd` body over a shared tree with DISTINCT breakdown values and asserts
byte-identical ANSI-stripped output (a tied fixture is deliberately avoided
there because Python's order is unstable). The tie-break itself is a pure,
value-preserving reordering in `sortByValueDesc`.

---

## D15 — tty proxy: host-stdin EOF does NOT close the pty master (the DECIDED semantics; Python differs)

**Status:** proposed (Stage 1 harness decision; §"Answered questions").

**Divergence.** On host-stdin EOF, Python's `tty_proxy.py` (~278-288) does
`os.close(master)` — closing the write end so the wrapped child sees EOF on its
own stdin too — then sets a `master = -1` sentinel and keeps pumping until child
exit. The Go `internal/ttyproxy` (ttyproxy.go:216-224) instead sets
`stdinClosed = true`, stops polling stdin, and **keeps the master OPEN**, pumping
it until the child exits. So a wrapped child blocked on `read(stdin)` receives
EOF under Python but NOT under Go (its stdin simply goes quiet).

- Input: `yolo -- some-cmd </dev/null` (or any invocation where host stdin hits
  EOF while the child is still running and reading stdin).
- Python: child's stdin sees EOF (master closed).
- Go: child's stdin stays open-but-idle; the child keeps running until it exits
  for another reason, then the proxy tears down.

**Why accepted (this is the DECIDED behavior, not an accident).** The go-port
plan's Stage-1 harness question was explicitly Answered (changelog 70a0ede9):
"stop reading stdin on EOF, keep pumping the master until child exit — pin it in
the harness for BOTH implementations, ledger note if Python's observable
behavior differs." Closing the master on EOF risks killing an interactive child
prematurely (the common `yolo` case is an interactive agent REPL, where a
transient stdin EOF must NOT tear down the session). The Go behavior is the
intended target semantics; Python's `os.close(master)` is the observable
difference this entry records. The Stage-1 harness pins the decided behavior for
both impls, so Python is expected to converge to it (its close-on-EOF path is
the near-dead `-1`-sentinel branch the plan calls out).

**Guard.** Documented; the ttyproxy code comment (ttyproxy.go:219-222) cites the
decided semantics. The Stage-1 job-control harness exercises the proxy's
master-pump lifecycle; the EOF-does-not-teardown behavior is what scenario (c)
(child keeps running while the proxy is quiescent) protects.

---

## D16 — oauth terminator: response status-line version + Server header (net/http vs BaseHTTPRequestHandler)

**Status:** proposed (audit 2026-07-18, Stage 11).

**Divergence.** The in-jail terminator's HTTP response METADATA differs from
Python's `BaseHTTPRequestHandler` in three cosmetic ways:

1. **Status line version.** Go's `net/http` server writes `HTTP/1.1 <code>`;
   Python's handler defaults `protocol_version = "HTTP/1.0"`, so it writes
   `HTTP/1.0 <code>`.
2. **Connection header.** Go sends `Connection: close` explicitly (+
   `SetKeepAlivesEnabled(false)`) to force per-request close; Python's HTTP/1.0
   default closes with NO `Connection` header on the wire.
3. **Server header.** Python's `send_response` emits `Server:` (and `Date:`)
   headers; Go omits `Server:`.

The observable CONNECTION BEHAVIOR — the client sees the socket close after each
response and reconnects — is IDENTICAL; only these header/status-line bytes
differ. The client-visible header NAMES and the JSON body are byte-exact (the
Stage 11 BLOCKER fix — D5's verbatim-name path).

- Input: any refresh/proxy request to the terminator.
- Go: `HTTP/1.1 200`, `Connection: close`, no `Server:`.
- Python: `HTTP/1.0 200`, no `Connection`, `Server: BaseHTTP/… Python/…`.

**Why accepted.** Claude Code parses the status CODE and the JSON body, not the
HTTP version token, the `Connection` header, or `Server:`. Emitting an HTTP/1.0
status line + suppressing the auto `Connection` management would require
bypassing `net/http`'s response writer with a custom raw-socket writer — a large
surface for a difference the client never acts on. Matching Python's
version-specific `Server: BaseHTTP/0.6 Python/3.13.x` string is actively
undesirable (it would leak the interpreter version and drift per Python build).
The per-request-close semantics, status code, JSON body, and header names all
match.

**Guard.** Documented; the terminator's verbatim-header wire test pins the
client-visible header names + body. The connection-close behavior is enforced by
`SetKeepAlivesEnabled(false)` + the explicit `Connection: close`.

---

## D17 — oauth broker: malformed-200 upstream body → typed `upstream_bad_response`, not an unhandled crash → 502

**Status:** proposed (audit 2026-07-18, Stage 6).

**Divergence.** When the upstream token endpoint returns a 200 whose body is
not valid JSON (or lacks `access_token`), Python's `do_refresh` has no `except`
for `JSONDecodeError`/`KeyError` around `_refresh_upstream`'s
`json.loads(resp.read())` (oauth_broker.py:517-527 catches only
`HTTPError`/`URLError`/`OSError`). The exception therefore propagates OUT of
`do_refresh` as an unhandled handler exception: the session tears down, and the
in-jail terminator's `ask_host_broker` raises `RuntimeError` → HTTP **502**
`broker_unavailable`. The Go broker instead detects the parse failure and returns
a typed `{"error": "upstream_bad_response", "message": …}` dict, which the
terminator maps to HTTP **400**.

- Input: upstream 200 with body `not json` (or `{}` with no `access_token`).
- Python: unhandled exception → session teardown → terminator 502
  `broker_unavailable`.
- Go: `{error: upstream_bad_response}` → terminator 400.

**Why accepted.** Python's outcome is an accidental crash path, not a designed
response: a malformed upstream 200 is a distinct, diagnosable condition, and the
Go code makes it explicit (a typed error the background refresh tick does NOT
fast-retry — only `upstream_unreachable` is retried, matching Python's retry
gate). Reproducing the crash would mean deliberately NOT handling a parse error
so the handler dies — worse operability for a byte-match on an error path that
only fires on a broken/hostile upstream. The error CODE (`upstream_bad_response`)
is Go-introduced by necessity: Python emits no code here because it never returns
a value on this path.

**Guard.** Documented; `internal/oauthbroker` refresh tests cover the
parse-failure branch. The status difference (400 vs 502) is confined to the
malformed-upstream-200 path.

---

## D18 — version resolution: build-time stamp wins over live `git describe`

**Status:** proposed (distribution work ★ step 2, 2026-07-19).

**Divergence.** `src/cli/version.py:_git_describe_version` resolves
YOLO_VERSION → live `git describe` → baked fallback. Go
(`internal/version.gitDescribe`) now resolves YOLO_VERSION → **baked
`buildVersion` stamp** → live `git describe`.

- Input: a stamped binary (goreleaser release, brew formula, wheel,
  `scripts/build-go.sh` output) run anywhere.
- Python order (describe-first) on a compiled binary: `git describe --always`
  succeeds inside ANY git repository, so an installed binary run from a user's
  project reports *that repo's* hash; a stale `dist-go/` binary reports the
  *live checkout's* version instead of its own.
- Go order (stamp-first): the binary reports the version it was built from —
  the compiled-binary analog of Python reading the installed wheel's baked
  setuptools-scm version (which is exactly what an installed Python `yolo` did;
  the describe-first order only ever served the editable-checkout case, which
  an interpreted CLI is and a compiled one is not).

YOLO_VERSION still wins unconditionally and verbatim, so the in-jail banner
parity contract is untouched. Unstamped `go build`/`go install` binaries keep
the live-describe → "unknown" behavior. A legacy literal-`"unknown"` stamp
(pre-D18 `build-go.sh`) is ignored rather than shadowing describe.

**Observable skew during transition:** a stale `dist-go/` binary now truthfully
reports its build-time `+dirty`/describe suffix where Python reported the live
tree's. Benign, and arguably the bug fixed.

**Guard.** `TestBuildVersionPrecedence` (runs inside the checkout, so live
describe IS available — the stamp winning proves the order); `TestNormalize`
byte contract unchanged.

**Addendum (★ step 2 review):** two extensions landed with the distribution
work. (1) Unstamped binaries with no repo root no longer run `git describe` in
the process cwd at all — an unstamped `go install` binary standing in a foreign
checkout reported THAT repo's version (reproduced with a v5.2.0-tagged
stranger repo); they now report "unknown", the §2d sane default. (2) `yolo
--version` is answered natively by the Go front door (byte format
`yolo-jail {v}` — identical to Python's typer callback; the brew formula's
`test do` depends on it). Value follows this divergence's stamp-first order,
so a stamped binary reports its build identity, YOLO_VERSION still wins
verbatim.
