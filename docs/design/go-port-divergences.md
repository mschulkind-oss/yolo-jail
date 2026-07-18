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
