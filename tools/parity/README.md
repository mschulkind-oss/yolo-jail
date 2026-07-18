# Parity tooling (go-port)

Infrastructure for byte-level A/B comparison between the Python implementation
(source of truth) and the Go port during the strangler migration
(`docs/plans/go-port-plan.md` §5).

Pieces:

- **`shims/`** — fake `podman`/`container`/`tmux`/`kitten`/`ps` executables that
  *record* the argv they were invoked with (or *replay* canned output). Put this
  dir first on `$PATH` and set `YOLO_PARITY_CAPTURE=<file>` so the real CLI's
  spawn sites are captured without a live runtime. This is the Go-compatible
  replacement for Python's `@patch`-based argv assertions — a Go binary can't be
  monkeypatched, so every seam is proven by comparing captured argv instead.
- **`run.py`** — the `just parity <suite>` driver: runs a named suite against
  both implementations and diffs. Byte-exact for argv/files/templates/banners/
  errors; ANSI-stripped for rich output. For wire-protocol JSON bodies it uses an
  order-preserving decode and compares key *sequences* as well as values.

The golden corpus lives in `tests/golden/` (language-agnostic files) and is
regenerated only via `just parity-freeze`, governed by the freeze rule
(§1.9): any commit that changes Python behavior regenerates the affected
goldens in the same commit, or parity CI is red.
