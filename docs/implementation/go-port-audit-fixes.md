# Go-port — audit-fix incorporation + mid-completion state (handoff)

**Date:** 2026-07-18. **Trigger:** the multi-agent audit addenda (commits
f2b9cc0, a4a787d) landed on the stage handoffs; this session incorporated every
confirmed finding, then continued the port toward "everything runnable in pure
Go."

## Audit findings — confirmed blockers/majors TRIAGED (not all fixed)

> **CORRECTION (2026-07-18 re-audit, `docs/implementation/go-port-audit-2026-07-18.md` §B/§D):**
> the original heading "all confirmed blockers/majors FIXED" was **false**. The
> table below records the fixes that DID land, but **six findings were never
> touched and are still open**, and several "fixed" rows regressed or were
> incomplete. Treat this table as a partial record, not a closure artifact.
>
> **Still OPEN (not in the table below):** the pre-commit-hook blocker (still
> unversioned, still not firing — 4th occurrence); the Stage 6 cross-language
> flock test (still Python-vs-Python, Go broker never started); Go broker
> logging; terminator logging; the Stage 4 conformance gaps (1-req/conn,
> concurrency); the §5.3 drift-suite gaps (dedup keys, shlex, canonical JSON).
>
> **Fixed-but-regressed/incomplete (see §D of the re-audit):** the journald
> truncation regression test does not catch its bug; the tree-mode timeout fix
> emits different stderr bytes than Python (unledgered); the malformed-200 fix
> invents an error code Python never emits; the Stage 5 black-box suite is
> missing the 124 and exit-1 cases it claims.

Each fix below landed as a `fix(go): ...` commit; accepted residues are ledgered
(D5–D7, all still **proposed** — not human-approved). The "verified green" claim
does not cover the OPEN items above.

| Finding (stage) | Fix commit | What changed |
|---|---|---|
| journald Wait/pipe truncation race (7, BLOCKER) | ec888c6 | drain pumps (wg.Wait) BEFORE cmd.Wait; 15-iter 500KB regression test |
| cgd SO_PEERCRED breaks darwin build (7) | ec888c6 | peercred_linux.go + peercred_other.go stub; `GOOS=darwin go build ./...` green |
| journald signals: Setsid, SIGTERM-not-KILL, -N exit (7) | ec888c6 | SysProcAttr{Setsid}; SIGTERM on disconnect; WaitStatus.Signaled→-N |
| daemon header caps (7) | ec888c6 | cgd 4096 / journald 16384; --log-file audit logging both daemons |
| host-processes tree timeout+exit path (5, BLOCKER) | bf02acd | 15s ctx timeout; ps-nonzero-empty→exit 0 (read stdout regardless) |
| host-processes non-string mode (5) | bf02acd | pyStrOrList → truthy non-string stringified → unknown-mode exit 2 |
| yolo-ps 30s deadline breaks 124 (5) | bf02acd | removed dial+session deadline (Python blocks to exit frame) |
| host-processes black-box suite missing (5) | bf02acd | fake-ps shim + socket harness (list/tree/pid/exit-codes/re-read) |
| hostservice signal rc=-1 not -N (4) | 3756946 | WaitStatus.Signaled→-N; spawn-fail panics→handler-error path |
| terminator canonicalizes response headers (11, BLOCKER) | 3756946 | verbatim names via direct map assignment; wire test |
| terminator negotiates HTTP/2 (11) | 3756946 | empty TLSNextProto pins HTTP/1.1; h2-client test |
| terminator 502 layer strings drift (11) | 3756946 | relay-layer only ENOENT/ECONNREFUSED; reset→mid-request; socket path in msg |
| broker creds missing claudeAiOauth key (6) | f2637e5 | oauthFromCreds: missing key→empty (no_refresh_token), err only on read/parse |
| broker Accept-Encoding (6) | f2637e5 | explicit identity on refresh+proxy (2026-05-12 fix) |
| broker proxy dup headers (6) | f2637e5 | last-value; name-canonicalization ledgered D5 |
| broker malformed-200 misclassify (6) | f2637e5 | parseError→upstream_bad_response (not fast-retried); require access_token |
| broker flock silent-proceed (6) | f2637e5 | Flock error → hard error, never unlocked refresh |
| json5 hex int64 wraparound (2, BLOCKER) | 48ff7f0 | math/big → decimal jsonInt (arbitrary precision) |
| json5 leading-zero/overflow/unterm-comment/\1/emoji-ident/U+2028 (2) | 48ff7f0 | all rejected/handled to pyjson5 parity; 9 corpus cases |
| drift suite copy-not-live (2) | 48ff7f0 | test_drift_dump_matches_live.py (proven fails on version.py perturb) |
| console splitlines \n-only (2) | 48ff7f0 | Python str.splitlines() boundaries (\r\n,\r,\v,\f,\x1c-1e,\x85,U+2028/9) |
| stale root yolo-parity binary (2) | 48ff7f0 | git rm + .gitignore root cmd artifacts |
| test_go_drift stale binary reuse (2) | 48ff7f0 | always rebuild dist-go when go present |

Ledger D5 (proxy header name casing), D6 (relay per-recv timeout), D7
(hostservice exec spawn-error text) — accepted residues, status proposed.

## Port state after this session

**Landed pure-Go (unit + cross-language + nested-jail verified):**
- Foundations (14 pkgs): paths, version, agents, naming, jsonx, shquote,
  pytext, console, frameproto, execx, fsx, json5, tomlx, hostservice.
- Daemons/binaries: broker relay, oauth broker, host-processes + yolo-ps, cgd,
  journald, jail-supervisor, oauth-terminator.
- ttyproxy library (Spike B resolved → library form).
- Front door (cmd/yolo) with seam #1 delegation — nested-jail verified.
- Drift suite + drift-live-guard.

**Delegated (background agents, in flight):** Stage 13 config engine
(internal/config), Stages 9/10 entrypoint generators (internal/entrypoint),
Stage 14 prune (internal/prune).

**Remaining for "everything runnable in pure Go":**
- Stage 10 entrypoint orchestration/boot + flake dual-impl image wiring
  (the human `just load && just install` step).
- Stage 14 remaining slices: builder, loopholes, broker-cmd, ps, storage+image.
- Stage 15 check (needs config engine + storage/image slice).
- Stage 16 run (the finale; consumes config, loopholes_runtime, ttyproxy,
  storage/image, agents_md).
- Stage 7 Commit A (Python thread→subprocess carve-out + lazy cgroup resolve).
- Stage 17 cutover (defaults flip; human-gated soak).

## Human-gated (unchanged)
Push + CI both arches; image rebuilds (10/11); soaks + default flips; real
`claude` login smoke (broker); nested-jail kill-9 (Stage 7 Commit A);
interactive ^Z/fg/resize (ttyproxy, Stage 16); Mac runbooks.

---

## Re-audit (2026-07-18) fixes landed this round

The confirmed CORRECTNESS findings from `go-port-audit-2026-07-18.md` fixed here,
each with a regression test and a `fix(go):`/`test(go):` commit:

| Finding | Fix | Guard |
|---|---|---|
| §B5 golden gate fails OPEN | parity oracles `t.Fatalf` (not skip) when the oracle RAN but errored; `t.Skip` kept only for python-absent; argv-oracle presence canary | `TestArgvOraclePresent` + fail-closed in runcmd/config/checkcmd/loopholes/entrypoint |
| §A/B2 repo-root cwd-walk hijack | cwd-walk requires `flake.nix` AND `src/entrypoint/__init__.py` (runcmd + checkcmd) | `TestResolveRepoRootDoesNotHijackBareFlake`; ledger D12 |
| §A/B3 bundled loopholes dropped | `loopholes.repoRoot()` walks to the yolo-jail checkout in host mode instead of the `/opt` fallback | `TestRepoRootHostModeFindsBundled` (non-monkeypatched) |
| §B/D11 `ps` destroys tracking files on macOS | platform-aware `PsRuntime` + tri-state enumeration (never prune on unconfirmed-empty) | `TestPsEnumerationFailureDoesNotPrune`, `TestPsRuntimePlatformAware`; **D11 WITHDRAWN** (was a bug, not a divergence) |
| §C `hostPlatform` arm64→aarch64 on macOS | keep `arm64` on darwin (Python `platform.machine()` parity); fixed the bug-locking test | `TestHostPlatformNaming` (platform-correct) |
| §C mise `$`-version corruption | `ReplaceAllLiteralString` (Python `re.sub` never expands `$`) | `TestMiseInjectedVersionWithDollar` |
| §B#2 storage migration dead (nil hook) | `run`+`check` wire `MigrateStorageLayout` (canReclaim=false fail-safe) | build + storage tests |

## Second round (2026-07-18) — re-audit §A–§E findings

> **CORRECTION (2026-07-18 final-pass verification):** the original heading
> "remaining OPEN items closed / The OPEN list above is now cleared" was **false**
> — a recurrence of the first-round false-closure. The table below genuinely
> resolves the re-audit §A–§E findings (verified: those fixes are real and mostly
> well-tested). But it does NOT clear the six-item OPEN list at the top of this
> doc. Actual status of that list: **flock cross-language test — FIXED** (now
> starts the Go broker, `tests/test_oauth_broker_go_parity.py:139`); **§5.3 drift
> — PARTIAL** (config-schema constants added; dedup-keys / shlex / canonical-JSON
> still absent from `cmd/yolo-parity`); **STILL OPEN:** versioned pre-commit hook
> (4th occurrence), Go broker logging, terminator logging, Stage 4 conformance
> (1-request-per-connection + concurrency). Two "fix landed but its test does not
> pin the bug" cases also persist: the journald truncation guard passes with the
> race reintroduced, and `TestHostPlatformNaming` skips the darwin case where the
> arm64→aarch64 bug lives.

The following genuinely resolve the re-audit §A–§E findings — each FIXED
(regression test + `fix(go):` commit) or LEDGERED for human sign-off:

| Item | Resolution | Commit / guard |
|---|---|---|
| AC 2-of-4 `_ac_materialize` (yolo-user-env.sh + briefings) | FIXED — `acMaterialize` branch added at both `runtime=container` sites | `TestAppleContainerMaterializesSingleFiles` |
| native `run` startup banner + tmux/kitty indicator | FIXED earlier this session — `emitStartupBanner` + `SetupJailIndicator` | run.go / native.go |
| `ca_cert` absolute-path `filepath.Join` | FIXED — `filepath.IsAbs` guard (pathlib `/` discards base) | `TestRuntimeArgsAbsoluteCACert` |
| tree-timeout stderr text | FIXED — byte-match `str(TimeoutExpired)` via `pyReprStrList` | `TestTreeTimeoutStderrMatchesPython` (live oracle) |
| `YOLO_GO_DISABLE` valve | FIXED — wired into `IsNative` as top-priority delegate | `TestGoDisableValve` |
| pi host-file mount + `host_pi_files` key | FIXED — `hostPiFileArgs` + `knownTopLevelConfigKeys` (27 keys match) | config parity corpus + `TestHostPiFileArgs` |
| 14 undrifted config-schema constants | FIXED — 26 constants now in the cross-language drift dump | `tests/test_go_drift.py` (byte-identical) |
| terminator `HTTP/1.1`+`Connection` wire bytes | LEDGERED **D16** — cosmetic metadata; per-request-close + code + body + header names all match | ledger D16 |
| malformed-200 invented code | LEDGERED **D17** — Go typed `upstream_bad_response`→400 vs Python crash→502; better operability | ledger D17 |
| stdin-EOF | LEDGERED **D15** — Go keeps master open (the DECIDED Stage-1 semantics); Python closes it | ledger D15 |

**Human-owned (unchanged):** ledger sign-off (D1–D17 all proposed; D8/D10
shipped pre-signoff), versioned pre-commit hook, Stage-1 freeze/CI, author email,
soak confirmation.
