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

## Spike B — Go tty proxy (RESOLVED: library form)

**Decision: the LIBRARY form** (`internal/ttyproxy`, consumed in-process by
`run` at Stage 16) — the plan's pre-decided fallback — NOT the two-process
Go-child/Python-parent split (seam #4).

**Rationale.** A pure-Go `run` (Stage 16) runs entirely in one Go process, so
the two-process split (which existed only to let a Go proxy child coexist with
a Python parent during the transition) is unnecessary — the library runs the
proxy loop in the same process that does the `podman run` supervision, exactly
as Python's `run_with_proxy` does today. This is simpler and keeps all signal
teardown in one place, avoiding the double-delivery/four-step-teardown hazards
seam #4 was designed to manage.

**Ported behavior (from tty_proxy.py + docs/design/ctrl-z-and-the-tty-proxy.md):**
non-TTY plain-spawn fallback; ^Z (0x1A) → TARGETED `Kill(getpid(), SIGTSTP)`
(never pgroup-wide — that would stop podman), byte withheld from the child,
post-^Z bytes queued + flushed on resume; NO Setsid; NO `signal.Notify(SIGTSTP)`
(default disposition must stop us); SIGCONT → re-raw; SIGWINCH → TIOCSWINSZ;
SIGHUP/SIGTERM → restore cooked termios + onTerminate + exit 128+n; stdin-EOF →
stop reading stdin, keep pumping master until child exit (the decided
semantics). `//go:build linux` (uses termios/pty syscalls); the darwin tree
still builds (the file is excluded there).

**Verification.** `internal/ttyproxy` tests: non-TTY fallback + exit code,
onStarted callback, REAL-pty passthrough (openPty → raw → bidirectional pump →
child exit), and a targeted-suspend guard (selfSuspend with SIGTSTP ignored
returns promptly, proving it's a self-targeted signal not a broadcast). The
INTERACTIVE ^Z/`fg`/resize/window-close scenarios (Stage 1's three job-control
cases) still need a real controlling-terminal session — recorded as the
manual nested-jail gate for Stage 16's run integration, since `go test` has no
controlling TTY to exercise a real suspend/resume cycle.

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
