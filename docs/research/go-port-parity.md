# Go-port parity — evergreen findings

Cumulative record of Python-behavior-to-preserve discovered during the port, so
later stages don't rediscover it. Per-stage handoffs live in
`docs/implementation/go-port-stage-*.md`; accepted divergences live in
`docs/design/go-port-divergences.md`. This file is the durable domain doc
(agent-standards §Research with KB persistence).

## Spike A — json5 parser (RESOLVED)

**Question (plan §14):** which Go JSON5/JSONC parser to adapt for
`internal/json5`?

**Answer: hand-written recursive-descent parser, zero dependencies.** Rationale:
- The module was dependency-free; adding a third-party JSON5 lib means
  `go mod vendor` + committing `vendor/` (plan §3). A hand-written lexer/parser
  (~2 files, `internal/json5/{json5,scan}.go`) keeps the zero-dep property and
  makes quirk fixes local — exactly the "vendor it so quirk fixes are local"
  intent, without the vendor tree.
- It decodes into the SAME value model as `internal/jsonx` (`*jsonx.OrderedMap`
  / `[]any` / string / bool / nil / jsonx integer|float via `jsonx.NumberValue`),
  so a json5-parsed config round-trips through `jsonx.DumpsSnapshot/Compact`
  byte-identically. `jsonx.NumberValue` was added (exported) so the int/float
  parity logic (−0→0, overflow→±Inf) lives in ONE place.

**Features supported (driven to observed equivalence with pyjson5):**
comments (`//`, `/* */`), trailing commas, single-quoted strings, unquoted
(identifier) object keys, hex integers (`0xff`), leading `+`, leading-dot
(`.5`) and trailing-dot (`5.`) floats, `Infinity`/`-Infinity`/`NaN`, string
line continuations, `\x`/`\u` escapes + surrogate pairs. The **hard
requirement** (comments + trailing commas, plan §14 user decision) is met; the
rest of the JSON5 grammar pyjson5 accepts is ALSO supported, so no config
feature is ledger-accepted as divergent.

**Verification:** `internal/json5.Decode` byte-diffed against `pyjson5.loads`
(re-encoded through `jsonx.DumpsSnapshot`) over a 46-doc dialect corpus AND
every real repo `.jsonc` (`yolo-jail.jsonc` + the 3 bundled loophole
manifests), agreeing on accept/reject. `go test -fuzz` (8M+ execs) found no
panics. No divergences surfaced → no ledger entry needed for json5.

**Consumers now unblocked:** the host-processes daemon config load (Stage 5),
and `internal/config` (Stage 13) — both can call `json5.Decode(path bytes)` and
get the jsonx value model.

## Spike B — Go tty proxy prototype (NOT STARTED)

The plan's Stage 2 Spike B (naked-Go tty prototype run under the Stage 1 pty
harness) is **not done**. It gates the Stage 8 binary-vs-library decision and
needs the Stage 1 pty harness first (also not built). Deferred — flagged in the
Stage 8 task. The decision (targeted-suspend two-process design vs library
fallback) must be made on harness evidence, per the plan, before Stage 8 is
scheduled.

## internal/tomlx (NOT STARTED)

The plan lists `internal/tomlx` (TOML parse parity for mise.toml venv
discovery, codex config.toml, pyproject reads). Not yet ported — needed by
Stage 16 (run) and Stage 14 (storage venv discovery). No blocker; slot it in
when the first TOML-consuming slice lands.

## Byte-parity divergences found + fixed (Stage 2 audit)

See `docs/qa/go-port-batch-1.md` (findings) and
`docs/design/go-port-divergences.md` (D1–D3 **proposed** — awaiting human
sign-off; do NOT treat them as approved). Fixed, not ledgered:
jsonx `-0`→0 + overflow→Infinity; paths HOME pwd-fallback; naming U+0130
`.lower()` special-case. Ledgered: jsonx bare `Infinity`/`NaN` decode (D1),
lone surrogate → U+FFFD (D2), pytext unicode-table version skew (D3).
