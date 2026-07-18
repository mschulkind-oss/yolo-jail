# Go-port — audit-fix incorporation + mid-completion state (handoff)

**Date:** 2026-07-18. **Trigger:** the multi-agent audit addenda (commits
f2b9cc0, a4a787d) landed on the stage handoffs; this session incorporated every
confirmed finding, then continued the port toward "everything runnable in pure
Go."

## Audit findings — all confirmed blockers/majors FIXED

Each fix landed as a `fix(go): ...` commit with a regression test; accepted
residues are ledgered (D5–D7). Verified green: `just lint-ci` + `go test ./...`
+ the cross-language parity pytests.

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
