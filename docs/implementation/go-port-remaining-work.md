# Go port — remaining work to a fully-Go world (TODO)

**Date:** 2026-07-18. **Context:** the final-pass verification (`ec3b22f`)
returned **GO for supervised Linux dogfooding** via `scripts/go-front-door.sh`
with Python kept installed as bail-back. This doc is the running checklist from
"good for dogfooding" to "Python deleted, modules consolidated, looks native
Go."

Companion: [go-port-mac-verification.md](go-port-mac-verification.md) (the Mac
agent's runbook) and [go-port-audit-2026-07-18.md](go-port-audit-2026-07-18.md)
(the full review this is derived from).

The maintainer's plan: cursory test that it works → **wipe Python entirely** →
**combine the modules into one** → make current state look always-Go (history
kept). This checklist is ordered around that.

---

## Status legend
- [x] done this session
- [ ] open — owner noted
- 🔒 human-gated (can't be done from inside the jail / needs a decision)

---

## A. Code fixes — DONE this session (post-review)

- [x] **`check` accumulated-fail early-exit (§C, MODERATE)** — the only open
  correctness gap on the check surface. Now short-circuits after Merged
  Configuration on any accumulated failure, matching Python; never runs a
  surprise `nix build` on an unhealthy host. Regression:
  `TestCheckAccumulatedFailEarlyExit` (verified RED without the fix).
- [x] **seam-#1 `os.execv` guard** — a stale/wrong-arch `dist-go/yolo` passed
  `os.access(X_OK)` then tracebacked on every `yolo` call; now `try/except
  OSError` → silent fall-through to Python, honoring the documented contract.

## A′. Code fixes — delegated this session (verify on completion)

These were handed to background agents; confirm each landed with RED-then-GREEN
tests before trusting:

- [ ] **journald truncation guard bites** — the fix is correct but the test
  passed with the race reintroduced. Make it force the race window / assert the
  full byte count.
- [ ] **`TestHostPlatformNaming` covers darwin** — refactor the OS→machine map
  to a pure function and table-test linux/darwin × amd64/arm64 (the
  darwin/arm64→arm64 case is where the bug lives; the test skipped it).
- [ ] **broker-relay orphan reap on the Go `run` path** — Python reaps orphaned
  relays every run; the Go path omitted it (attach-ended jails leak a relay).
  Port host-only, piggybacking the live-container enumeration.
- [ ] **broker operational logging** — Go broker had zero logging; port the
  incident-derived contract (2026-04-23 invalid_grant, 2026-05-12 logout-loop)
  matching the cgd/journald `--log-file` pattern, with token-fingerprint
  redaction. Blocks unattended broker use.

## B. Findings that turned out to be NON-issues (verified, no work needed)

- [x] **§5.3 drift gaps (dedup keys, shlex, canonical JSON)** — the review said
  "neither fixed nor deferred." Investigated: all three are already covered by
  **dedicated live-Python differential tests** — `internal/shquote`
  (`TestQuoteParity` via `text_oracle.py`), `internal/jsonx` (`parity_test.go`),
  and the config parity corpus (14 `merge` cases incl. list-dedup, run through
  live Python at `config_parity_test.go:97`). They are simply not ALSO in the
  standalone `cmd/yolo-parity` drift dump. Since the dedicated tests provide the
  real protection and the whole drift-dump apparatus is deleted at cutover,
  folding them into the dump is low-value churn. **No action** — recorded here
  so it isn't re-flagged.

## C. Still-open code items — NOT done (owner: next Go session)

- [ ] **Gated subcommands swallow unknown flags (exit 0) where Typer exits 2.**
  Minor, and it's a *transient* Typer contract: post-cutover the Go CLI should
  emit its OWN usage + exit 2, not mimic Typer. Building delegation scaffolding
  to reproduce Typer's exit-2 now is churn that gets deleted at Stage 17.
  **Recommendation:** fold into the Stage-17 CLI-surface pass (define the Go
  argument-parsing/usage contract once, natively), not before.
- [ ] **Stage 4 frameproto conformance gaps** — 1-request-per-connection and
  concurrency cases still missing from the conformance suite. Not
  destructive; add before the broker/relay run unattended.

## D. Process / CI / housekeeping — mostly 🔒 human-gated

- [ ] 🔒 **Versioned pre-commit hook actually firing** — flagged 4 times, still
  unversioned + not enforced. This is the meta-reason false-closures and unformatted
  commits keep slipping through. Install a committed hook (`.pre-commit` or a
  `just`-driven git hook) that runs `just check-ci`.
- [ ] 🔒 **Stage 1 characterization + parity-CI freeze** — `tests/golden/`, a
  `just parity-freeze`, and a CI job that byte-diffs Go vs live Python on every
  push. §1.9's freeze rule has NEVER activated (that's how `host_pi_files`
  drifted). **This is the safety net that makes deleting Python safe** — see §F.
  The live-Python oracles (now fail-closed) are a partial substitute covering
  argv/config/prune/check but not UX strings, entrypoint boot order, tty
  job-control, or most config-schema constants.
- [ ] 🔒 **Author email** — the ~46 go-port commits are under
  `matt.schulkind@hyperscience.com`, not the canonical `mschulkind@gmail.com`.
  Fix the clone's `user.email`. (History-rewrite of the existing commits is the
  maintainer's call; the user said history is fine.)
- [ ] 🔒 **Ledger sign-off** — D1–D17 are all `proposed`; D8/D10/D15/D16/D17
  shipped before the §1.1-required human sign-off. Review + accept/reject each
  in `docs/design/go-port-divergences.md`. (D11 correctly WITHDRAWN.)
- [ ] 🔒 **Doc drift** — the plan's seam table (§4) and Stage-15/16 handoffs still
  present the gate as bare `YOLO_IMPL=go`; the safe path is the four-var shim.
  seam #11 undocumented; Stages 8/9/12/16 have no handoff doc. Low priority if
  Python is about to be deleted (these docs describe the transition), but worth a
  sweep if any survive cutover.

## E. Ledger items shipped pre-sign-off (need the human's accept/reject)

`proposed` divergences that are LIVE in code (D1–D17). The load-bearing ones for
dogfooding:
- **D15** stdin-EOF: Go keeps the pty master open (decided Stage-1 semantics);
  Python closes it.
- **D16** terminator wire bytes: HTTP/1.1 + `Connection: close` + no `Server:`
  vs Python's HTTP/1.0. Cosmetic; client-visible code/body/header-names match.
- **D17** malformed-200: Go returns typed `upstream_bad_response`→400 vs Python
  crash→502.
- **D5/D6/D7** broker/relay/hostservice residues.
See `docs/design/go-port-divergences.md` for the full set + rationale.

## F. Blockers to actually DELETING Python (Stage 17) — the bar rises

Once bail-back is gone, every "open but low-risk-with-Python-present" item above
becomes must-fix. Plus these hard blockers:

1. [ ] 🔒 **Go binary distribution** — no goreleaser today; the Python
   console-script is still the only `yolo` entry point. **Python cannot be
   removed until the Go binary IS the shipped `yolo`.** This is the #1 blocker
   for the maintainer's "wipe Python" goal — build the release/install path first.
2. [ ] 🔒 **macOS verified on real hardware** — see the Mac runbook. macos-user
   launch, OQ-1 path_helper, builder VM. Un-verifiable from Linux/in-jail.
3. [ ] 🔒 **Stage 1 freeze actually running** (§D) — the irreversibility of
   deletion is only safe behind it.
4. [ ] 🔒 **Soak confirmations** — broker single-use refresh token (Stage 3/6),
   real `claude` login smoke.
5. [ ] **Broker operational logging** (§A′) landed + verified — before it runs
   with no Python fallback.
6. [ ] 🔒 **Ledger sign-off** (§E).
7. [ ] **macos-user bootstrap repoint** — the macos-user path currently emits the
   *Python* entrypoint bootstrap (Stage 16b decision). Deleting `src/entrypoint`
   requires first repointing it at the Go entrypoint (`cmd/yolo-entrypoint`),
   verified on a Mac. Otherwise macOS boot breaks at cutover.

## G. The "restabilize in a Go world" endgame (maintainer-owned)

After the above clears and cursory testing passes:
1. **Wipe Python** — remove `src/cli`, `src/entrypoint`, the Python daemons, the
   `pyproject.toml` console scripts, `tools/parity/*` oracles, the drift suite,
   and the delegation seam (`YOLO_GO_DELEGATED`, `YOLO_GO_DISABLE`, the
   in-jail snapshot-copy's `_in_jail` Python fork — the whole bail-back
   apparatus). ⚠ Sequence matters: distribution (F.1) + macOS repoint (F.7)
   MUST land first.
2. **Combine modules into one** — the port is split across ~60 `internal/*`
   packages + `cmd/*` binaries mirroring the Python module boundaries. Consolidate
   to whatever structure reads as native Go (the Python-mirroring seams are
   transition scaffolding). The many `cmd/yolo-*` daemons may stay separate
   binaries or fold into one multi-call binary (a Stage-10 measurement question
   already noted in the plan).
3. **Drop the parity/divergence machinery** — once Python is gone, the byte-parity
   contracts, ledger, and `divergences.md` are historical; the code becomes the
   spec. Keep the regression *tests* (they still pin behavior); drop the
   live-Python *oracles* (nothing to compare against).
4. **Make it look always-Go** — remove Python-referencing comments/"ports X"
   docstrings, rename `*_parity_test.go` where they're now just unit tests, and
   fold the go-port docs (`docs/implementation/go-port-*`, this file) into an
   archive or delete. History stays; current state reads native.

---

## Quick reference: what's safe to use NOW (Linux, via the shim)

Per the review's per-command risk table:
- **LOW:** `run` (argv byte-identical), `doctor`, `ps` (on Linux).
- **MODERATE→now LOW:** `check` (the §C gap is fixed this session).
- **CAUTION:** `prune` — destructive; `diff` against `uv run python -m src.cli
  prune` once before trusting the reclaim decisions.
- **USABLE but BLIND→pending:** `broker` — logging being added (§A′); fine with
  Python daemons present.
- **DEFER:** `builder`, `macos-*` — certify on a real Mac.
- **NEVER bare:** always use `scripts/go-front-door.sh` (four-var shim). Bare
  `YOLO_IMPL=go` drops bundled loopholes → silent TLS/auth failure.
