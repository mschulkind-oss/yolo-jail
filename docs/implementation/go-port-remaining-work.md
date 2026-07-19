# Go port — remaining work to a fully-Go world (TODO)

**Date:** 2026-07-18. **Context:** the final-pass verification (`ec3b22f`)
returned **GO for supervised Linux dogfooding** via `scripts/go-front-door.sh`
with Python kept installed as bail-back. This doc is the running checklist from
"good for dogfooding" to "Python deleted, modules consolidated, looks native
Go."

Companions: [../guides/runbooks/mac-go-port-verification.md](../guides/runbooks/mac-go-port-verification.md)
(the Mac agent's runbook), [go-port-audit-2026-07-18.md](go-port-audit-2026-07-18.md)
(the review this derives from), and
[go-port-post-transition.md](go-port-post-transition.md) (everything deferred to
*after* Go-only).

The maintainer's plan: cursory test that it works → **wipe Python entirely** →
**combine the modules into one** → make current state look always-Go (history
kept). This checklist is ordered around that.

---

## Status legend
- [x] done this session
- [ ] open — owner noted
- 🔒 human-gated (can't be done from inside the jail / needs a decision)

---

## ★ Transition path — do these IN ORDER (2026-07-19: "transition ASAP, fast-follow")

**Every deferrable code fix is already committed this session** (§A, §A′), and
the code tree is green on both platforms. The critical path to a Go-only world is
now just **three maintainer-gated steps + the wipe.**

**2026-07-19 directives that shortened this path:**
- **No soak.** Removed as a gate — fast-follow with fixes instead.
- **Mac verification → fast-follow.** Temporary macOS breakage is acceptable, so
  full Mac hardware verification AND the macos-user bootstrap-repoint move to
  *after* the wipe (§I). This removes the `src/entrypoint` deletion gate that
  used to block §H.
- **Pull nothing forward.** All post-cutover work (distribution config beyond the
  minimum, nix-ld, consolidation, OSS sweep) is in
  [go-port-post-transition.md](go-port-post-transition.md) — no Python+Go twins
  during the transition.

### The critical path

| # | Step | Owner | Detail |
|---|---|---|---|
| 1 | **Cursory-test the Go path on Linux** via `scripts/go-front-door.sh`: a real `yolo-go -- claude` session, `check`, `ps`, `prune --dry-run`. Go/no-go smoke. | you | §"safe to use now" below |
| 2 | **Build the distribution pipeline** — goreleaser + release.yml + publish.yml (go-to-wheel, PyPI kept), so the Go binary IS the shipped `yolo`. Copy swarf verbatim. **The one hard pre-wipe blocker:** deleting the Python console-script with no Go release = users have no `yolo`. | you | post-transition §2 |
| 3 | **WIPE PYTHON** — the manifest in §H. `src/entrypoint/` is now deletable (Mac deferred). | you | §H |
| 4 | **Consolidate modules + strip parity scaffolding + always-Go cosmetics.** | you | §G, post-transition §3 |

### Fast-follow (post-wipe — temporary breakage accepted)

| Step | Owner | Note |
|---|---|---|
| **Full Mac hardware verification** — [the runbook](../guides/runbooks/mac-go-port-verification.md) (macos-user OQ-1, Apple Container, builder, `check`). | Mac agent | was blocker F.2 |
| **macos-user bootstrap repoint** — point it at the Go entrypoint so the native macOS backend works again. **macos-user launch is BROKEN between the wipe and this** (see §H note). Apple Container on macOS is unaffected (uses the image's Go entrypoint). | you+Mac | was F.7 |
| **Ledger sign-off** — record accept/reject of D1–D17 (they're already live; this is formal acceptance, not a functional gate). | you | §E |
| **nix-ld, deep consolidation, OSS-hygiene sweep** | you | post-transition §1/§3/§4 |

### Optional / your call (not gating, given the fast-follow posture)

- **Stage-1 parity-CI freeze** (§D) — was "the safety net that makes deletion
  irreversible-safe." With history kept (Python is `git revert`-recoverable) and
  a fast-follow posture, its value drops: once Python is deleted there's no
  Python left to drift against, and the live-Python oracles already cover
  argv/config/prune/check pre-wipe. **Skippable** — but if you want a
  characterization snapshot captured *before* the reference disappears, do it
  before step 3. Recommended, not required.
- **Housekeeping** (pre-commit hook, author email) — anytime, non-blocking.

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

## A′. Code fixes — delegated this session (ALL LANDED + VERIFIED)

Handed to background agents; each landed with RED-then-GREEN tests and was
independently re-verified (build both platforms, staticcheck clean, security
audited where relevant):

- [x] **journald truncation guard bites** (`e2da469`) — test rewritten to size
  the payload past the AF_UNIX send-buffer + a delayed-read client so the pump
  blocks with the tail unread; end-of-stream marker makes truncation a precise
  assertion. Verified RED with the race reintroduced, GREEN with the fix (3× each).
- [x] **`TestHostPlatformNaming` covers darwin** (`497055e`) — extracted a pure
  `platformMachine(goos, goarch)` and table-tested all four combos incl.
  darwin/arm64→arm64 (the bug's home). Verified RED with the unconditional
  arm64→aarch64 map reintroduced.
- [x] **broker-relay orphan reap on the Go `run` path** (`5c3f6df`) — ported
  host-only, piggybacking the live-container enumeration, declining on unknown
  liveness, reusing the byte-verified `prune.ReapRelayOrphans` engine. Regressions
  `TestRelayReapOrphans{CnameFold,UnknownLivenessDeclines}`. Verified both platforms.
- [x] **broker operational logging** (`af21e83` broker, `d1d7493` terminator) —
  ported the full incident-derived contract (2026-04-23 invalid_grant,
  2026-05-12 logout-loop): per-request/refresh/cache/proxy-mirror log sites,
  `--log-file` + `-v` flags matching the cgd/journald pattern, `SetupLog`.
  **Security independently verified:** every token-bearing log line uses
  `TokenFP`/`fpOf` fingerprints (sha256[:8]) or timestamps — no raw token bytes;
  the terminator's malformed-proxy dump is redacted (`redactedResp`). Tests:
  `TestTokenFP`, `TestDescribeCredsRedaction`, `TestRefreshLoggingEndToEnd`, the
  `redactedResp` leak assertion. This clears blocker **F.5**.

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

## F. Cutover gates — RECLASSIFIED (2026-07-19: fast-follow posture)

The single source of truth for ordering is the ★ table at the top. This section
records why each former "hard blocker" was reclassified — the fast-follow +
no-soak + Mac-deferred directives collapsed the bar to essentially one gate.

1. [ ] 🔒 **Go binary distribution — THE hard pre-wipe gate (still #1).** No
   goreleaser today; the Python console-script is the only shipped `yolo`.
   Python cannot be removed until the Go binary IS the shipped `yolo`. (★ step 2.)
2. [ ] 🔒 **macOS hardware verification — MOVED to fast-follow.** Temporary macOS
   breakage accepted; certify after the wipe. (No longer gates cutover.)
3. [ ] **Stage-1 parity-CI freeze — OPTIONAL now** (§D / ★ "optional"). History
   keeps Python `git revert`-recoverable and the posture is fast-follow, so it's
   a recommended-not-required pre-wipe snapshot, not a gate.
4. [x] ~~**Soak confirmations**~~ — **DROPPED** per directive. Fast-follow with
   fixes instead of a soak period.
5. [x] **Broker operational logging** (§A′, `af21e83`/`d1d7493`) — landed +
   verified this session.
6. [ ] **Ledger sign-off — MOVED to fast-follow.** D1–D17 are already live in
   code; formal accept/reject is bookkeeping, not a functional gate. (§E.)
7. [ ] **macos-user bootstrap repoint — MOVED to fast-follow.** With Mac deferred,
   `src/entrypoint/` is deleted at the wipe and macos-user launch breaks until
   this lands (see §H gate note). Repoint the bootstrap at the Go entrypoint
   (`cmd/yolo-entrypoint`) and verify on a Mac. Linux + Apple Container unaffected.

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

**The detailed post-transition backlog** (distribution via goreleaser+tap+
go-to-wheel copied from swarf, the nix-ld image change, module consolidation, and
the OSS-hygiene sweep) is queued in
[go-port-post-transition.md](go-port-post-transition.md). Nothing there is on the
critical path to running Go-only — it's the "restabilize in a Go world" phase.
Note **F.1 (distribution)** and that backlog's §2 are the same work: authoring the
goreleaser/release/publish config is safe to do pre-cutover, but *cutting over* to
it (retiring the PyPI→poet→formula chain) is post-Python.

---

## H. The Python-wipe manifest (★ step 3 — mechanical, but NOT `rm -rf src/`)

⚠ **`src/` is not all Python.** The Go binary still reads data + build assets
under `src/` at runtime and build time. A naive `rm -rf src/` breaks the Go
build and the image. Delete surgically, in this order, verifying the build stays
green after each group.

**`src/entrypoint/` — delete it now, accept the macOS breakage** (2026-07-19:
Mac deferred to fast-follow). The macos-user bootstrap still generates a Python
script that imports the `entrypoint` package (`internal/macosuser/bootstrap.go`),
so **the `runtime: "macos-user"` launch path breaks the moment `src/entrypoint/`
is gone** — it stays broken until the fast-follow bootstrap-repoint. Two things
that are NOT affected: (a) Apple Container on macOS (`runtime: "container"`) uses
the Go entrypoint baked into the nix image, not this bootstrap; (b) Linux is
entirely unaffected. So the blast radius of deleting it early is *only* the
native macos-user backend on Apple Silicon, which the fast-follow restores.

### Delete (pure Python, no Go/runtime dep)
- [ ] `src/cli/` — the whole Python CLI (run/check/config/prune/loopholes_runtime/…).
- [ ] `src/*.py` daemons: `broker_relay.py`, `oauth_broker.py`,
  `oauth_broker_jail.py`, `host_processes.py`, `host_service.py`,
  `jail_daemon_supervisor.py`, `yolo_ps.py`, `prune.py`, `loopholes.py`.
- [ ] `src/__init__.py`, `src/_version.py`, `src/shims/` (verify Go doesn't read
  `src/shims` — grep showed only mise `/shims`, unrelated).
- [ ] `tools/parity/` — all oracles + corpus + drift dump (`cmd/yolo-parity`,
  `py_drift_dump.py`, `*_oracle.py`, `config_cases.json`). The drift suite dies
  with Python.
- [ ] `pyproject.toml` `[project.scripts]` (the 4 console scripts) + the
  `setuptools`/`setuptools-scm` build-system — replaced by goreleaser (★ step 2).
  Decide the whole `pyproject.toml`'s fate with go-to-wheel (post-transition §2c
  generates the wheel metadata from CLI flags — swarf has NO pyproject.toml).
- [ ] `tests/test_*.py` for the deleted Python (config_merge, cli_commands,
  entrypoint, jail, oauth_broker_*, host_processes, etc.) — the Go regression
  tests are the surviving spec.
- [ ] `src/entrypoint/` — delete now; breaks macos-user launch until the
  fast-follow repoint (see the gate note above). Linux + Apple Container
  unaffected.

### KEEP (Go reads/builds these — relocate later in §G, don't delete)
- `src/bundled_loopholes/` — **Go reads it at runtime** (`internal/loopholes/
  loopholes.go:94`, `BundledLoopholesDir`). Relocate out of `src/` in §G and
  update `BundledLoopholesDir()`; do NOT delete.
- `flake.nix` (root — `.#ociImage`), `flake.lock` — the image build.
  ⚠ `src/flake.nix` exists too — **verify** whether it's the live image source or
  a stale duplicate before touching either.
- The nix image's baked binaries are Go already (`cmd/yolo-*`); no Python there.

### Delete the Go-side bail-back apparatus (same commit or right after)
- [ ] `internal/config/load.go` — the `_in_jail()` / `loadAssembledSnapshot`
  in-jail snapshot-copy fork (only existed to keep the shared snapshot honest
  across the Python/Go boundary; with one impl it's moot — re-evaluate, may
  simplify rather than delete).
- [ ] `internal/frontdoor/frontdoor.go` — `goImplEnabled` (YOLO_IMPL gate),
  `goDisabled` (YOLO_GO_DISABLE valve). Once Go IS `yolo`, everything is
  unconditionally native; the gate + valve are transition-only.
- [ ] `cmd/yolo/main.go` — `delegateToPython` + the `YOLO_GO_DELEGATED` loop
  breaker + the `os.execv` forward. No Python to delegate to.
- [ ] `cmd/yolo/native.go` — collapse `nativeDispatch`/gated-vs-unconditional
  into plain dispatch (nothing to fall through to).
- [ ] `scripts/go-front-door.sh`, `just install-go`, seam #11 forwarding in
  `internal/runcmd/assemble.go` (the `YOLO_IMPL=go`/`YOLO_GO_BIN_DIR`/
  `YOLO_PYTHON`/`PYTHONPATH` `-e` block) — the whole four-var shim.
- [ ] `tests/conftest.py` seam-env scrub (the `YOLO_IMPL`/`YOLO_GO_*` delenv
  block) — no longer needed once no Python test runs.

### Verify after the wipe
- [ ] `go build ./...` + `GOOS=darwin GOARCH=arm64 go build ./...` green.
- [ ] `go test ./...` green (the parity oracles are gone; pure Go tests remain).
- [ ] `staticcheck ./...` clean; `just check-ci` green (now Go-only).
- [ ] Nested-jail smoke (Linux): `yolo -- bash -lc 'yolo internal | head -1'`
  shows the Go usage; a real `yolo -- claude` session boots.
- [ ] **Expected-broken (fast-follow):** `runtime: "macos-user"` launch on Apple
  Silicon — the deleted Python entrypoint bootstrap. NOT a regression to chase;
  it's restored by the fast-follow repoint. `GOOS=darwin` *build* still passes;
  it's only the runtime macos-user path that's down. Apple Container on macOS and
  all of Linux keep working.

---

## Quick reference: what's safe to use NOW (Linux, via the shim)

Per the review's per-command risk table:
- **LOW:** `run` (argv byte-identical), `doctor`, `ps` (on Linux).
- **MODERATE→now LOW:** `check` (the §C gap is fixed this session).
- **CAUTION:** `prune` — destructive; `diff` against `uv run python -m src.cli
  prune` once before trusting the reclaim decisions.
- **USABLE:** `broker` — operational logging now landed (§A′); forensics on par
  with Python. Soak dropped as a gate (fast-follow); the single-use-refresh-token
  edge is now watched via the new logging rather than gated behind a soak.
- **DEFER (fast-follow):** `builder`, `macos-*` — certify on a real Mac *after*
  the wipe; macos-user launch is temporarily broken post-wipe by design.
- **NEVER bare:** always use `scripts/go-front-door.sh` (four-var shim). Bare
  `YOLO_IMPL=go` drops bundled loopholes → silent TLS/auth failure.
