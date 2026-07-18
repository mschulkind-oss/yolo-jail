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
