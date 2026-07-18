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

## D11 — ps: native `yolo ps` uses the shallow runtime detect, not `_runtime()`

**Status:** proposed (awaiting sign-off).
**Reachable only under `YOLO_IMPL=go`** (the gate defaults to Python).

Python's `ps()` resolves the runtime via `_runtime()`, which probes daemon
connectivity (`podman info` / `container system status`), rejects a
non-Apple `container` binary, and `sys.exit(1)` with a red message when no
runtime is reachable. The native Go `ps` (internal/pscmd) resolves via
`runtime.DetectRuntime` — the shallow `_detect_runtime` variant (YOLO_RUNTIME
env or "podman"), with no connectivity probe and no process-exit.

**Effect.** On a host with no reachable runtime, Python `yolo ps` exits 1 with
"No container runtime found…"; the Go arm instead runs the probe (`podman ps …`)
which fails to spawn, the RunCmd seam returns an error → empty output → "No
running jails." + exit 0.

**Why accepted (proposed).** The connectivity-probe + process-exit machinery is
the `run` path's concern (`_runtime()` is shared, but its exit-on-missing
semantics matter for launching a container, not for listing them). For a
read-only `ps`, "no reachable runtime" and "no running jails" are observationally
close, and the Go arm never crashes. The full `_runtime()` port (with
`_is_apple_container` + `_runtime_is_connectable` + sys.exit) lands with the
broader runtime-selection slice; until then this narrowing is documented rather
than papered over. `ps` is gated behind `YOLO_IMPL=go`, so the default path is
unaffected.

**Guard.** Documented; `internal/pscmd` behavior tests cover the empty-output →
"No running jails." path. The table/stuck/workspace rendering is byte-parity
(internal/runtime parity tests).
