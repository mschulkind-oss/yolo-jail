# Go-port audit — 2026-07-18 (Stages 8–16 burst + 15 follow-up commits)

**Method.** Two multi-agent passes with adversarial verification (two independent
refuters per blocker/major, each instructed to refute). Pass 1: 128 agents over the
82-commit burst (Stages 8–16). Pass 2: 44 agents over the 15 commits fast-forwarded
mid-review (Stage 16 e2e fixes, native `ps`/`loopholes` slices, seam #11 in-jail
forward, the `go-front-door.sh` shim, and the partial `host_pi_files` port). Findings
here survived verification; several were reproduced live in-jail. This is the current
state as of HEAD (origin/main + the plan's macOS/16b change).

**One-line verdict.** The Go port has reached an impressive milestone — `run`, `check`,
`ps`, `doctor` are natively dispatched behind `YOLO_IMPL=go`, and the in-jail argv is
byte-identical to Python across a real fixture matrix. But the burst wired those seams
**live**, and this audit finds the `YOLO_IMPL=go` path is **not yet safe to make the
default** for anyone: it silently drops behaviors on some hosts/runtimes, one native
command (`ps`) is destructive on macOS, several prior "fixed" findings were not, and the
mechanical commit gate has never fired. None of this is data-loss on the default (Python)
path — it is all gated behind an opt-in flag — but the flag is being documented as
dogfood-ready, and it isn't.

**Credit where due (verifier-confirmed).** The ordered-container-argv assembler is
byte-identical to live Python over a 10-fixture matrix and held against 22 additional
adversarial fixtures a verifier added. `internal/shquote`, `internal/jsonx` (25k-float
fuzz), `internal/pytext` are byte-faithful. The frozen `stop_loopholes` guard-stack order,
the `_resolve_repo_root` rename-aside invariant, and the `_live_yolo_containers` tri-state
polarity in run are all preserved. The `go-front-door.sh` shim (5e88ecf) is a genuine,
thoughtful mitigation of the host-enablement blockers. The pi-feature gap and the seam #10
forward were recorded honestly in §14. Sequencing (Spike A before its consumer) was right.

---

## A. The headline: `YOLO_IMPL=go` is not host-default-safe yet

The `go-front-door.sh` shim reshaped this since pass 1 — it exports the four vars
(`YOLO_IMPL`, `YOLO_GO_BIN_DIR`, `YOLO_PYTHON=<venv python>`, `YOLO_REPO_ROOT=<repo>`) that
make the happy path work. Re-adjudicated against current HEAD:

- **B1 (delegation crash) — DOWNGRADED to a minor robustness note.** Pass 1 reported
  `YOLO_IMPL=go yolo <delegated>` crashing with `ModuleNotFoundError`. Verification showed
  the reproduced crash was an `env -i` PATH-stripping artifact: the real Python prelude
  (`src/cli/__init__.py:473-478`) requires `YOLO_GO_BIN_DIR` too, so bare `YOLO_IMPL=go`
  falls through to pure Python safely, and the two-var partial case works under the actual
  `uv run` launch. Residual: `cmd/yolo/main.go:58` falls back to bare `python3` when
  `YOLO_PYTHON` is unset (the "set by the shim" comment is still misleading — the shim is
  the only setter). Loud, not silent; transition-only. **Fix:** self-resolve the editable
  venv relative to `os.Executable`, or hard-fail with an actionable message rather than a
  raw traceback; fix the comment.

- **B2 (repo-root hijack) — MAJOR (was blocker).** `internal/runcmd/probes.go:37-48` and
  the duplicate `internal/checkcmd/probes.go:333-348` resolve the repo by walking up from
  **cwd** for any `flake.nix`, where Python anchors to the package `__file__`
  (`run_cmd.py:224`). With the shim's `YOLO_REPO_ROOT` set it resolves correctly; without
  it, in a user's own flake workspace, it silently picks that flake as the yolo-jail repo
  (bind-mounted `:ro` into the jail; the target of `nix build .#ociImage`). Verified live:
  `resolveRepoRoot` in a stub-flake dir returns the stub with `ok=true`. **Not in the
  ledger.** **Fix:** require `flake.nix` **and** `src/entrypoint/__init__.py` (the predicate
  step 1 already uses), or resolve relative to `os.Executable`; fix `checkcmd` in the same
  commit; ledger the residual `YOLO_REPO_ROOT`-required divergence.

- **B3 (bundled loopholes dropped) — MAJOR (was blocker).** `internal/loopholes/loopholes.go:55-67`
  `repoRoot()` falls back to the in-jail `/opt/yolo-jail` when `YOLO_REPO_ROOT` is unset,
  where Python's `bundled_loopholes_dir()` anchors to `__file__`. On a host with only a
  partial gate (`YOLO_IMPL` + `YOLO_GO_BIN_DIR`, `YOLO_REPO_ROOT` unset), a Go `yolo run`
  discovers **zero** bundled loopholes → no `--add-host platform.claude.com:127.0.0.1`, no
  broker-CA mount, no `NODE_EXTRA_CA_CERTS`, no `YOLO_JAIL_DAEMONS`, no audio, no
  host-processes; the jail boots looking healthy and Claude auth fails with TLS errors.
  `yolo check` likewise prints "No loopholes installed". The full four-var shim avoids it;
  the module map elevates bundled-manifest location to an explicit contract, and this is
  **unledgered**. **Fix:** resolve like Python (reuse `resolveRepoRoot`, or ship
  `bundled_loopholes/` beside the binary at `<bindir>/../share/...`; `go:embed` is NOT an
  option because `runtime_args_for` bind-mounts the real dir); add a host-mode test that
  does not monkeypatch the package var.

**Common root cause of B1/B2/B3:** the Go binary relies on env vars (`YOLO_PYTHON`,
`YOLO_REPO_ROOT`) the shim must supply, where the Python CLI self-resolves from `__file__`
with no env at all. That is a real, unrecorded divergence from ground rule §1.1. Either
give the Go binaries a Python-equivalent self-resolve, or ledger it and make the shim the
**only** documented enablement (today the plan seam table §4 and the Stage-15/16 handoffs
still say/imply bare `YOLO_IMPL=go` — see §E).

## B. Confirmed live blockers (Go-gated path, unaffected by the new commits)

1. **`yolo ps` on macOS/Apple Container misroutes and destroys tracking files (D11 unsound).**
   Python `ps()` resolves the runtime via the platform-aware `_runtime()` (prefers Apple
   Container on macOS); Go `ps` uses the shallow `DetectRuntime()` (`YOLO_RUNTIME` or literal
   `podman`, `probe.go:37-42`), no platform awareness. On macOS with AC running and
   `YOLO_RUNTIME` unset (the shim doesn't set it), Go runs `podman ps` → empty → prints
   "No running jails" **while real AC jails are live**, then calls
   `PruneStaleTrackingFiles(empty)` (`pscmd.go:51/68`), **deleting the tracking files for
   those live jails**. Ledger D11 calls `ps` "read-only" (false — it mutates the tracking
   dir) and scopes the effect to "no reachable runtime" (false — AC is reachable and
   produces wrong output). **Fix:** use the platform-aware runtime resolution for `ps`;
   rewrite or reject D11; never prune on an unconfirmed-empty set.

2. **Storage layout migration never runs under the gate.** `EnsureGlobalStorage(migrate)`
   is passed `nil` at both live callsites (`runcmd/run.go:62`, `checkcmd/check.go:31`), so
   the v2 dangling-mise-symlink heal and the `layout-version` stamp silently never happen;
   `MigrateStorageLayout` is fully-written dead code. Fails safe (Python re-runs it next
   Python invocation) but is a silent wiring omission no test catches. **Fix:** pass the
   closure, or call it directly inside `EnsureGlobalStorage` as Python does.

3. **Apple Container: `yolo-user-env.sh` and per-agent briefings never materialized.**
   Python calls `_ac_materialize_under_ws_state` in **four** places on the AC runtime; Go
   implements **two** (`assemble.go:168-171, 322-327`). On `runtime=container`, the Go path
   emits no mount/copy for `yolo-user-env.sh` (sourced with `2>/dev/null`, so every
   `env_sources` var silently vanishes) or for the agent's staged `AGENTS.md`/`CLAUDE.md`.
   No test touches the AC runtime at all. **Fix:** add the two missing calls; add an AC
   fixture to the argv oracle.

4. **Native `run` drops the startup banner and the tmux/kitty jail indicator.**
   `_print_startup_banner` (all three launch paths, incl. the baked stale-jail-version
   signal on attach) and `_tmux_rename_window("JAIL")` have no Go equivalent on the run
   path; `frontdoor.StartupBanner`/`SetupJailIndicator` are ported but have **zero
   production callers**. The jail indicator is a safety affordance (how the user knows a
   terminal is inside a jail). Unledgered output-parity break. **Fix:** call
   `SetupJailIndicator` on the native branch in `cmd/yolo/main.go` (never when delegating —
   Go must not brand-then-delegate), emit `StartupBanner` at the `run_cmd.py:2651` point on
   all three paths, port `_container_baked_yolo_version` for attach.

5. **The "golden gate" fails OPEN.** `internal/runcmd/parity_test.go:73` (and the config,
   loopholes, entrypoint oracle tests) `t.Skipf` on oracle error instead of `t.Fatalf`. A
   verifier replaced the oracle with a raising stub → the suite reported `ok`, exit 0. So
   any Python-side change that breaks the oracle silently disarms the argv/config/loopholes
   parity gate — the exact drift the gate exists to catch, and the concrete cost of Stage 1
   never running (committed goldens fail closed; live oracles fail open). **Fix:** convert
   oracle-failure paths to `t.Fatalf`; keep `t.Skip` only for the genuine "python/uv absent"
   precondition, with one canary asserting the oracle IS present in-repo.

6. **`docs/implementation/go-port-audit-fixes.md` claims "all confirmed blockers/majors
   FIXED" — false.** Six findings it marks closed are still open and absent from its table:
   the pre-commit-hook blocker, the Stage 6 cross-language flock test (still
   Python-vs-Python, Go broker never started), broker logging, terminator logging, the
   Stage 4 conformance gaps, and the §5.3 drift gaps. As the closure artifact the next
   session reads, this removes their only tracking. **Fix (do now):** retitle to "triaged"
   and add an OPEN section. *(Corrected this session — see §F actions.)*

7. **The pre-commit gate is unversioned and provably not firing** — the 4th occurrence.
   The hook exists only as a local `.git/hooks/pre-commit` (no `.githooks`, no
   `core.hooksPath`, no `just init-hooks`). Burst commits fail `ruff format --check` at
   their own refs (`e41701a` the live seam-#1 forward, later cleaned by `9590d91`), and HEAD
   still shipped `src/cli/run_cmd.py` unformatted (fixed this session, `b57308f`). **Fix:**
   version the hook + a `just init-hooks` recipe + a §10 per-session precondition; the human
   should install it in the clone that produces the commits.

## C. Parity majors by module (Go-gated path)

**Config engine (Stage 13) — the byte-critical one:**
- `host_pi_files` unknown-key: Go `check`/`config-dump` exit 1 on a config Python accepts
  (exit 0) — the config-key half of the pi feature is unported (`0197e73` records it).
- Strict-mode non-object config error string diverges (Python double-wraps via its own
  `except Exception`); non-TTY config-change path prints nothing in Go where Python prints
  the full diff + "Non-interactive mode: auto-accepting"; `env_sources` warnings lose the
  `Warning: ` prefix on the run path.
- 14 config-schema constant sets (`KNOWN_*`, `JOURNAL_MODES`, `EPHEMERAL_STORAGE_MODES`,
  `VALID_MCP_PRESETS`, `DEFAULT_MISE_TOOLS`, …) are re-declared in Go with **zero
  drift-suite protection** — this is exactly what let `host_pi_files` diverge silently.

**Entrypoint (Stage 10):**
- `mise` versions containing `$` are corrupted: `regexp.ReplaceAllString` expands
  `$1/$name` in the replacement; Python `re.sub` expands backslashes.
- Go warns on nonzero `iptables` exit where Python is silent (`subprocess.run` no `check`).
- No test pins `Main()`'s boot order or perf labels (the load-bearing item); parity tests
  green-SKIP when the oracle fails; the image-size-delta exit criterion was never measured.
- Go pi merge (`be41a04`) is exercised by **no test** (566ad15 tests are Python-only and
  mock the host loader); the tree-parity matrix selects pi but never provides a host
  `settings.json`.

**TTY proxy / terminator (Stage 8/11):**
- Signaled-child exit code is `128+N` in Go where Python returns `-N` (a third convention).
- SIGCONT re-raw is async in Go, leaving a cooked-mode window after `fg` (Python's handler
  runs synchronously inside the stopped process).
- Terminator wire bytes diverge: Go `HTTP/1.1` + `Connection` header vs Python `HTTP/1.0` +
  `Server:` header, no `Connection` — despite the prior "HTTP/1.1 pinned" fix claim.
- stdin-EOF divergence still unledgered (the plan required a ledger entry if observable
  behavior differs — it does: Go never closes the master so the child never sees EOF).

**Engines (Stage 14) / loopholes:**
- `AutoLoadImage` drops the macOS container-builder offload on a live path (unledgered).
- "Prune engines ready" is false — half of `prune.py` unported, including the
  mount-anchor-preserving guard.
- `ca_cert` absolute paths: `filepath.Join(modulePath, s)` concatenates where pathlib `/`
  discards the base — an absolute `ca_cert` silently loses its bind mount and
  `NODE_EXTRA_CA_CERTS`. Systemic Python→Go trap; audit sibling `filepath.Join`-as-`/` sites.
- `RunDoctorChecks` failure text diverges from CPython (`"timeout"` vs `str(e)`).

**check (Stage 15):**
- Config-Files early exit uses a parse-only flag, not Python's accumulated fail count — so
  a host with a failing runtime/nix probe runs sections (incl. the real `nix build` and the
  orphan-`rm -f` prompt) Python would never reach.
- Gated `check`/`doctor` swallow unknown flags (exit 0) where Typer exits 2.
- The live Python seam (`__init__.py` forward) has zero test coverage; `os.execv` is
  unguarded (a stale/wrong-arch `dist-go` binary tracebacks every `yolo` call, contradicting
  the "silent fall-through" contract).

**Front door (Stage 12):**
- `YOLO_GO_DISABLE` rollback valve (frozen in the flag registry, "demonstrated" required by
  Stage 12 exit) **does not exist in the code**.
- `hostPlatform` maps `arm64→aarch64` unconditionally — wrong on macOS/Apple Silicon
  (`platform.machine()` is `arm64` there), and a test locks the bug.

**seam #11 (new):** the in-jail `YOLO_IMPL=go` forward has no §4 seam-catalog or
flag-registry entry and no runtime verification that in-jail `yolo` actually runs Go.

## D. Prior "fixes" that regressed or were incomplete (audit-fix verification)

- **journald truncation regression test does not catch its bug** — a verifier reintroduced
  the pre-fix `Wait`/pipe ordering and the 500KB test stayed green over many runs.
- **Stage 6 "cross-language flock" test is still Python-vs-Python** — the Go broker is never
  started; the single-use-refresh-token serialization contract has no Go-side coverage.
- **tree-mode timeout fix emits different stderr bytes than Python** (fixed string vs
  `str(TimeoutExpired)`), unledgered and untested.
- **malformed-200 fix invents an error code Python never emits** (`upstream_bad_response`);
  the jail sees 400 where Python produces a 502-class handler error.
- **Go broker still has zero logging** — the incident-derived operational contract
  (2026-04-23 invalid_grant, 2026-05-12 logout-loop) was never ported; a soak has no
  forensics.
- **Stage 5 black-box suite** claims exit-code coverage `0/1/2/3/124` it doesn't have (no
  124-timeout, no exit-1 case).
- **§5.3 drift gaps** (dedup keys, shlex, canonical JSON) neither fixed nor deferred.

## E. Process & honesty

- **Stage 1 has still not run at ~100 commits.** No `tests/golden/`, no `just parity-freeze`,
  no parity-CI job — so §1.9's freeze rule has **never activated**, and a Python behavior
  change breaks no build. The `host_pi_files` drift is the freeze rule's dormancy made
  concrete: nothing went red because the drift suite dumps only `paths/version/naming/agents`
  (no config-schema constants) and the config oracle fails open (§B5). Native gates
  (run/check/ps/doctor) are now **ahead** of the characterization wave meant to pin them.
- **Ledger discipline:** D8 and D10 are behavior changes shipped before the human sign-off
  §1.1 requires *first*; all of D1–D11 are still `proposed`. B2/B3/D11 and the pi divergence
  are unledgered entirely.
- **Documentation drift:** the plan's seam table (§4) and the Stage-15/16 handoffs still
  present the gate as bare `YOLO_IMPL=go` (host env export) — but the safe path needs the
  four-var shim; only the plan's Stage-16 §14 row (added this session) warns against bare
  export. seam #11 is undocumented. `§10.5` parity-findings updates stopped at commit 9 of
  82. Four landed stages (8, 9, 12, 16) have no handoff doc; Stage 16's record was a commit
  message.
- **Scope honesty:** Stage 2's row calls its own written exit criteria (≥10k corpus,
  cross-write gate, lint rules) "nice-to-have".
- **Author email:** the burst continues under `matt.schulkind@hyperscience.com`, not the
  canonical `mschulkind@gmail.com` (§11). Permanent; worth fixing the clone's `user.email`.

## F. Actions taken this session (planning agent)

- Rebased the branch onto the latest `origin/main` (it moved 3× during review); resolved the
  §14 conflict keeping origin's newer Stage-16 row + the macOS/16b change.
- Fixed forward `src/cli/run_cmd.py` formatting (`b57308f`) — HEAD was shipping it
  unformatted (hook still not firing).
- Retitled `go-port-audit-fixes.md`'s false "all FIXED" header and added an OPEN section
  (§B6). *(see the accompanying commit.)*
- Updated §14 to point at this report and corrected the dangling "audit addendum in
  stage-16" reference.

## G. Consolidated human decision list

Owed by you (nothing here is automatable):
1. **Do NOT make `YOLO_IMPL=go` a default, and only enable it via `go-front-door.sh`
   (four vars)** until B2/B3/D11 + the banner/indicator/storage/AC blockers clear. Bare or
   partial-gate host use is unsafe.
2. **Ledger adjudication:** D1–D11 are all `proposed`; D8/D10 shipped before sign-off; D11
   is unsound (see §B1). Plus the unledgered divergences: B2, B3, ca_cert, tree-stderr,
   malformed-200, stdin-EOF, terminator wire bytes, hostPlatform.
3. **Install the versioned pre-commit hook** in the clone producing the commits (4th miss).
4. **Confirm the Stage-3/6 soak state** and CI status (still unrecorded, unverifiable in-jail).
5. **Direct the sequencing question:** should native gates keep advancing before Stage 1
   (characterization + freeze rule) lands? The audit's recommendation is no — Stage 1 next.
6. **Author email** on the porting clone.
7. Unanswered Open Questions: OQ-1 (macOS path_helper, unverified on a Mac) and the Stage-0
   image-bake question remain open.
