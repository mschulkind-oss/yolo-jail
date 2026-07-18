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
