# Go-port Stage 0 — Scaffold + walking skeleton (handoff)

**Status:** landed (in-jail criteria). CI/image-bake criteria pending human confirmation.
**Plan:** [`docs/plans/go-port-plan.md`](../plans/go-port-plan.md) Stage 0.

## What landed

| Artifact | Commit | Notes |
|---|---|---|
| `go.mod` (module `github.com/mschulkind-oss/yolo-jail`, go 1.26) | 129d19c | root module; no deps yet (stdlib-only) |
| `cmd/goprobe` | 129d19c | throwaway deployment tripwire; deleted after channels proven |
| `scripts/build-go.sh` + `just build-go` | 129d19c | emits every `cmd/` binary → `dist-go/<goos>-<goarch>/` |
| `flake.nix` `packages.goBinaries` | 129d19c | static CGO-free Linux cross-compile via host `pkgs.go` |
| Justfile mixed recipes | 129d19c | format/lint/lint-ci/test/test-fast grow the Go half |
| `mise.toml` staticcheck pin | 129d19c | `go:honnef.co/go/tools/cmd/staticcheck` |
| `.github/dependabot.yml` gomod | 129d19c | weekly |
| `.gitignore` `dist-go/` | 129d19c | transition binaries never committed |
| `tools/parity/` (shims + `just parity`) | 129d19c, 8a5e59f | recording PATH shims + drift-suite runner stub |

## Verified (commands + observed output)

**1. Go toolchain — build/vet/gofmt clean:**
```
$ go vet ./... && go build ./...      # vet OK; build OK
$ gofmt -l $(git ls-files '*.go')     # (empty — clean)
$ staticcheck --version               # staticcheck 2026.1 (v0.7.0)
```

**2. `dist-go/` build channel:**
```
$ ./scripts/build-go.sh
build-go: goprobe -> dist-go/linux-amd64/goprobe (linux/amd64)
$ ./dist-go/linux-amd64/goprobe smoke
goprobe ok: linux/amd64 (go go1.26.5)
args: [smoke]
```

**3. Novel channel — Nix static cross-compile with NO Linux builder** (the
property the walking skeleton exists to de-risk for Go, mirroring the Python
entrypoint bake):
```
$ nix build --impure .#goBinaries
$ file result/bin/goprobe
result/bin/goprobe: ELF 64-bit LSB executable, x86-64, statically linked, Go BuildID=…
```

**4. Nested-jail live-mount smoke (definition-of-done for cli/entrypoint-adjacent work):**
```
$ ./scripts/build-go.sh
$ yolo -- bash -lc '/opt/yolo-jail/dist-go/linux-amd64/goprobe nested-jail-smoke'
…
⚡ Executing: bash -lc '/opt/yolo-jail/dist-go/linux-amd64/goprobe nested-jail-smoke'
goprobe ok: linux/amd64 (go go1.26.5)
args: [nested-jail-smoke]
```
This proves nix build → nested jail → live-mount `dist-go/` path → static
binary runs with argv passthrough — the full transition-era deployment loop
minus the image bake.

**5. Parity recording shim self-test:**
```
$ python tools/parity/install_shims.py <dir> podman
$ YOLO_PARITY_CAPTURE=cap.jsonl PATH=<dir>:$PATH podman run --rm image:tag
$ cat cap.jsonl
{"argv": ["run","--rm","image:tag"], "env": {"YOLO_FOO":"bar"}, "tool": "podman"}
```

## Human actions needed

- **CI both-halves green** (§10.7): the in-jail agent has no push credentials or
  CI visibility. Push and confirm the `check` job passes with the Go half, and
  that `.#goBinaries` builds on both `ubuntu-latest` and `ubuntu-24.04-arm`.
- **`ociImageMinimal` bake of goprobe on both arches**: the plan's Stage-0 exit
  wants goprobe baked into `.#ociImageMinimal` and run from the image (not just
  the live mount). I did **not** wire goprobe into the image contents — baking a
  throwaway into the production minimal image (and the required `just load &&
  just install`) is a human-gated, image-rebuilding step, and the live-mount
  path (verified above) already exercises the same nix-build → jail channel. The
  `packages.goBinaries` derivation is the reusable piece Stage 10/11 will bake
  in. **Decision to confirm:** is the live-mount proof sufficient for Stage 0, or
  do you want goprobe temporarily in `ociImageMinimal`? (See Open Question below.)

## Deviations from the plan (proposed, for the ledger)

- **goprobe not baked into `ociImageMinimal`.** Plan §Stage 0 lists it; I proved
  the equivalent nix-build→jail channel via the live mount + a standalone
  `nix build .#goBinaries` instead, to avoid mutating the production image with a
  throwaway. `packages.goBinaries` is the durable derivation. Non-blocking.
- **`vendor/` not yet committed.** Plan §3 wants `vendor/` + `vendorHash=null`.
  The tree is stdlib-only so far, so there's nothing to vendor; `goBinaries`
  sets `GOPROXY=off` when `vendor/` is absent (hermetic on an empty module
  graph). First third-party dep (json5 parser, Stage 2) triggers `go mod vendor`
  + the committed-vendor rule.

## Open Questions

### Is the live-mount + standalone `nix build .#goBinaries` proof sufficient for Stage 0, or must goprobe be baked into ociImageMinimal?
The plan's Stage-0 exit says "baked into `.#ociImageMinimal` on both CI arches
AND run from the live-mount `dist-go/` path in a nested jail." I did the second
(verified) and added a standalone `packages.goBinaries` derivation that builds
the static binary with no Linux builder — but did not bake the throwaway into
the production minimal image (that needs `just load && just install` and mutates
the shipping image for a probe that's deleted next stage).
_Leaning:_ live-mount + `.#goBinaries` is sufficient; the image bake is
genuinely exercised at Stage 10/11 with the real entrypoint binary, and baking a
throwaway now adds a human rebuild for no durable gain. If you disagree, I'll add
goprobe to `ociImageMinimal.contents` behind the minimal variant.
**Answer:**
> 

## What's next

Stage 2 (foundations) is the highest-leverage next step and is fully in-jail
verifiable: `internal/{paths,version,jsonx,json5,tomlx,shquote,pytext,fsx,execx,
console,frameproto,agents}` + the drift suite (`cmd/yolo-parity`) wired into
`just check-ci`. Stage 1 (characterization goldens) is interleaved — the drift
suite is the first piece of parity CI. Stages 3–5 (broker relay, frameproto,
host-processes) are the first production swaps and also fully in-jail testable.

---

## Review addendum (2026-07-18, planning agent) — READ BEFORE THE NEXT SESSION

Stages 2 (partial), 3, and 4 landed after this handoff was written
(`13331aa`/`7ea2562`/`ef5073e`, `b46f400`+`568e633`, `51a6ffd`) **without
handoff docs of their own and without updating the plan's §14 stage table**
(it still reads "not started" for everything). This addendum is the review of
that work and the corrective queue; it lives here because this is the only
handoff the next session will find.

### Quality verdict on what landed

High. Keep doing this:
- The Stage 3 relay swap hit every mapped hazard (per-connection dial,
  SHUT_WR + bounded-drain clean EOF, dev/ino-guarded unlink, jail_id override
  via order-preserving reframe) and widened the identity matchers in the SAME
  commit per the seam-#2 rider, with a dual-impl harness
  (`tests/test_broker_relay_go_parity.py`).
- The drift suite is wired into the fast tier (pre-commit gates every commit)
  and already caught a real divergence: the U+0130 (İ) lowercasing mismatch
  (`568e633`) — exactly the container-naming incident class it exists for.

### Deviations to correct, in priority order

1. **Stage 1 was skipped, so the freeze rule is NOT live** (there are no
   goldens to freeze). Stages 3–4 self-insured with per-stage harnesses, so no
   damage yet — but Stage 6 (OAuth broker) consumes Stage 1's wire fixtures
   (broker action shapes, error dicts) and Stage 8 depends on the pty harness.
   **Land Stage 1 (or at minimum: wire-protocol fixtures + config/UX byte
   goldens + freeze-rule CI job) before starting Stage 5/6.** "Interleaved"
   (this doc's What's-next) is not license to defer it past the stages that
   consume it.
2. **The two Stage 2 risk spikes did not run.** No `internal/json5`, no
   `internal/tomlx`, no naked-Go tty prototype verdict. Spike A (json5
   differential oracle) gates every config-consuming slice; Spike B (tty)
   exists to make the binary-vs-library decision BEFORE Stage 8 is scheduled,
   and needs the Stage 1 pty harness first. Stage 2 is *partial* until both
   spike verdicts are recorded in `docs/research/go-port-parity.md`.
3. **Backfill the living state:** write
   `docs/implementation/go-port-stage-{2,3,4}.md` (what landed, verification
   commands + observed output, what's unverified) and update the plan §14
   table (0: landed, CI arms pending human confirmation; 2: partial — spikes
   outstanding; 3: landed — soak state UNKNOWN, see below; 4: landed).
4. **Stage 3's exit criteria are unrecorded and partly unmet on this host:**
   no record of the nested-jail claude-token smoke, no record of whether
   `YOLO_BROKER_RELAY_BIN` is exported on the dev host — and `dist-go/` does
   not currently exist, so if the flag IS exported, the next relay ensure
   spawns a nonexistent path. Related footgun: `just clean` now
   `rm -rf dist-go/`, which can yank a soaking binary out from under a live
   env flag. **Fix in the Stage 3 backfill: verify/record the host flag
   state, rebuild `dist-go/`, and add a missing-binary guard at
   `_relay_spawn_argv` (warn + fall back to the Python relay when the
   flag's path doesn't exist) — propose it as a seam-hardening commit.**
5. Housekeeping: delete `cmd/goprobe` once the human confirms the CI arms
   (per this doc's own note), and the human still owes an answer to this
   doc's Open Question above (image-bake sufficiency — the Leaning is
   reasonable; it was never surfaced for an answer).

### Standing reminders that were missed this round

Per plan §6/§10: every stage ends with its handoff written and §14 updated —
a stage without a handoff is not done, no matter how good the code is. New
Open Questions must be pointed out to the human in the session summary, not
just left in a doc.

---

## Audit addendum #2 (2026-07-18, planning agent) — process review of the 16-commit burst

An 8-auditor review with adversarial verification (2 independent refuters per
blocker/major) audited commits `f057d96..5f811ee`. **No finding was refuted.**
Per-stage technical findings are in each stage's own handoff addendum; this
section covers cross-cutting process and the status of the first addendum's
corrective queue.

### What the audit confirmed as done well

- **Corrective item 3 (backfills) honored**, and the discipline stuck: every
  subsequent stage commit was paired with a docs commit updating §14.
- **Corrective item 4 partly honored**: the relay missing-binary fallback landed
  (`7b8d743`, `os.access(X_OK)` guard + operator warning + test) and `dist-go/`
  was rebuilt (all 10 binaries present).
- **Spike A landed before its consumer** (json5 `ebfd7f1` precedes Stage 5
  `a5b7751`) with its verdict recorded in `docs/research/go-port-parity.md`.
- **The drift suite is genuinely bidirectional** — verifiers reproduced it going
  red on a perturbed Python constant and green on restore.
- **Git hygiene**: all 16 messages conventional, no AI trailers, no amends, tree
  clean. Ledger entries correctly left `proposed`.

### [BLOCKER · confirmed] The `check-ci` pre-commit gate was not in force for the burst

Plan §1.4 makes the hook the ONLY mechanical gate and forbids `--no-verify`. At
`8ccde4b`/`4bfcc3e`/`5d1cc37`/`608ddf0`, `ruff check` and `ruff format --check`
both fail — the hook would have blocked all four. They landed anyway. Root cause
found by the auditor: the reflog shows the burst arrived here via
`pull: Fast-forward` at 2026-07-18 09:59:34, i.e. **the commits were authored in a
different clone**, where the unversioned `.git/hooks/pre-commit` does not exist
and nothing in the repo installs it. Corroborating: consecutive commits 33–67s
apart cannot fit `check-ci`'s test tier. This is the second occurrence of the
class (`edb7c6b` cleaned up after `ef5073e`).

*Fix:* version the hook (`.githooks/pre-commit` + `core.hooksPath`, or a
`just init-hooks` recipe) and add "hook installed and firing" to §10's
per-session preconditions. **Human:** confirm which clone produced the burst and
that its hook now runs.

### Corrective queue (supersedes addendum #1's)

1. **Stage 1 is now a hard gate — it must be the next session.** Item 1 of the
   first addendum was not honored: Stages 5, 6, 7-B and most of 11 landed with
   no Stage 1 session (only the broker-wire slice was folded into Stage 6).
   There is still no `tests/golden/`, no pty harness, no ordered-argv matrix, no
   config/UX byte goldens, and **no parity-CI freeze job — so the freeze rule
   (§1.9) remains dormant** while Python keeps moving.
2. **Fix the six confirmed blockers before any seam flag is flipped** (each in
   its stage's addendum): json5 hex int64 wraparound (St. 2); host-processes
   tree-mode timeout drop + inverted exit path (St. 5); the flock "cross-language"
   test that never starts the Go broker (St. 6); yolo-journald's output-truncation
   race (St. 7); terminator header canonicalization (St. 11); and this hook gap.
3. **Spike B (tty prototype) is still not started and gates Stage 8** — it needs
   the Stage 1 pty harness first, which is another reason Stage 1 comes next.
4. **Stop over-claiming in §14 and in handoffs.** Corrected this session: Stage 2
   → partial, Stage 3 soak → unconfirmed, Stage 5/6/7/11 → caveated. Going
   forward, handoffs must embed **literal command transcripts** (§6/§10.5) as
   stage-0 did — the burst's handoffs used prose "Verified" bullets and stage-3
   admits its output was "recorded in the session", i.e. lost.
5. **§1.5 nested-jail records are missing for Stages 3/5/6** (all edited
   `src/cli/loopholes_runtime.py`). Run and transcribe them.
6. **Forward-fix the "accepted" wording**: `f057d96`'s message and
   `docs/research/go-port-parity.md` call D1–D3 accepted; the ledger correctly
   says `proposed`. Also `docs/qa/go-port-batch-1.md` row #2 records disposition
   "fix" when the opposite shipped (ledgered as D1).

### Human decisions still owed

- The **unanswered Open Question above** (image-bake sufficiency for Stage 0) —
  still the only empty `**Answer:**` in any go-port handoff.
- **Divergence ledger D1–D3** need accept/reject.
- **Stage 3 soak state**: is `YOLO_BROKER_RELAY_BIN` actually exported on the dev
  host? Nothing in the repo records it, and §14 asserted it.
- **CI confirmations** for Stages 0–7/11 (none recorded yet); `cmd/goprobe`
  deletion is gated on the Stage 0 one.
- **Commit authorship**: the whole burst is authored as
  `matt.schulkind@hyperscience.com`, not the canonical
  `mschulkind@gmail.com` (§11). Permanent — history is never rewritten — but
  worth fixing the porting clone's `user.email` now.
