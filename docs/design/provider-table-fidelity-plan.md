---
title: "Plan: provider table fidelity"
date: 2026-09-01
status: ready
tags: [plan, providers, packs, derives]
summary: "Implementation plan for provider-table-fidelity.md's nine defects: the wire_api dialect translation, the composer's three composition bugs, the --profile split, and the census. Written against 578c7e5f."
---

# Plan: provider table fidelity

**Design:** [`provider-table-fidelity.md`](provider-table-fidelity.md) · **Status:** ready ·
Written against `578c7e5f`, 2026-09-01.

**Precedence:** the design wins on behavior · the tree wins on fact · this file is advice and is
the first thing to be wrong. Never twist code to match it.

**Sibling plan owns:** env-emitting derives, deleting `env_shape` / `internal/agentenv`, and D3's
overlay dedupe — all executed in
[`provider-catalog-and-selection-plan.md`](provider-catalog-and-selection-plan.md). Cited here, not
duplicated.

## Map

| Path | Change |
| :--- | :--- |
| `integration/providers_test.go` | **new** — the regression test for D1; step 1, fails today |
| `internal/packdecl/contributes.go` | `knownWireAPIs` (`:1438`) → three canonical names |
| `packs/codex/derive.lua` | `:53` — translate canonical → codex, emit nothing for a protocol it cannot speak |
| `packs/pi/derive.lua` | `:41` — same, pi's spellings |
| `internal/packload/providers.go` | `ComposeProviders` (`:39`) refuse the pair; `requiredProviders` (`:137`) key off the catalog; `mergeUnder` (`:303`) honor null |
| `internal/config/validate.go` | `:919` — keep the user-layer refusal, share its message with the composer's |
| `internal/cli/runcmd.go` | `:209-217` split `--profile`; delete `profileValueAt` (`:149`); rewrite `runUsage` `:48-55` |
| `packs/zai/pack.json` | `wire_api` value follows the new vocabulary |
| `AGENTS.md` · `packs/embed.go` | `:8` / `:20` — pack census, ten → twelve |
| `docs/guides/USER_GUIDE.md` | `:217` — `--profile` no longer means timing |

## Reuse

- `packdecl.KnownWireAPI` / `KnownWireAPIs` — already the single vocabulary; `internal/config`'s
  `validateWireAPI` asks for it. Change the list, not the plumbing.
- `internal/config.reportUnknownKeys` — the census mechanism for any new key check.
- Integration harness: `runYolo` / `runYoloCLI` (`integration/harness_test.go:582,612`); copy the
  shape of `integration/packs_test.go:TestPackFilesTreeReachesTheJail`.
- `packdecl.DecodeTolerant`'s existing skew path already drops an unknown `wire_api` and reports it
  (`unknownWireAPISkip`) — the new vocabulary inherits that for free.

## House style

- Advice: a closed vocabulary lives in **one** list with a `Known*` predicate beside it, exported
  when a second layer reads it. Reason: `knownVias`' doc records a measured drift when it was
  spelled in three places.
- Constraint: `packs/*/derive.lua` is sandboxed Lua — no `os`, no `io` (`luahook/vm.go`
  `openSandboxLibs`). Table/string/math only.

## Traps

- **`providerderive_test.go` runs the REAL derives** — four assertions on `openai-chat`
  (`:93,:121,:175,:208`) plus fixture values at `:55,:160`. It is the test that pins D1's wrong
  behavior — a rewrite, not a repair.
- Codex accepts `wire_api = "responses"` **only**; `chat` was removed from the product
  (`docs/research/local-model-endpoints.md` §"Codex CLI", verified from the 0.145.0 binary). So
  codex + z.ai's OpenAI route is **unreachable** — emit nothing rather than a value that 404s.
- The pi half of the dialect table is **two data points, not source** (design §3.0a's note).
  Verifying it from pi's `dist/` is a build step, not an assumption.
- `subhelp.go:66` gives `"run"` an **empty** `valueFlags` on purpose (see its comment). Making `-p`
  unambiguous may need an entry there, or `yolo run -p zai --help` misparses.
- `internal/cli/host.go` has its own `-p`/`--profile` parse (`:116,:618`) with **no timing meaning**
  — already unambiguous. D5 touches the run path only; do not "unify" them.
- `os.WriteFile` applies a mode only on create. Any file-mode work needs an explicit `Chmod` —
  the trap `ebcdf82f` already paid for.

## Build order

1. **Integration test first — it fails today.** `integration/providers_test.go`: launch with
   `packs: ["claude","zai"]` + a `providers.zai`, assert the rendered `~/.codex/config.toml` and
   `~/.pi/agent/models.json` carry values those agents accept. → `just test`
2. **Verify pi's `api` vocabulary from installed source**, write the finding into design §3.0a.
   No code. Unblocks step 3's pi row.
3. **Canonical vocabulary + per-agent translation.** `knownWireAPIs` → the three names; each derive
   maps canonical → its own dialect and emits nothing for an unspeakable protocol. Rewrite
   `providerderive_test.go`. → `just test-fast` then step 1's test goes green.
4. **Composer, one slice** — refuse the composed `base_url`+`endpoints` pair; key
   `requiredProviders` off the composed catalog, not `p.Decl.Providers()`; make `mergeUnder` honor
   the null-drop. → `go test ./internal/packload ./internal/cli/run`
5. **`--profile` split.** Rename the timing flag, delete `profileValueAt`, rewrite `runUsage`
   `:48-55`. → `go test ./internal/cli/...`
6. **Census and guide.** `AGENTS.md:8`, `packs/embed.go:20`, `USER_GUIDE.md:217`. → `just done`

Steps 1–2 unblock everything. Step 6 is independent and zero-risk — land it whenever.

## Ships with

- **Unit:** canonical→dialect translation per agent, including *no mapping* (emits nothing);
  composed `base_url`+`endpoints` refused; a `null`-dropped provider no longer required;
  `models.<alias>: null` removes the alias.
- **Integration:** step 1's test. It is the only level that would have caught D1 — the unit tests
  all passed while the wrong value shipped.
- **Rewrites, not repairs** — these assert the old behavior and must be re-pointed at the new:
  `internal/entrypoint/providerderive_test.go` (four `openai-chat` assertions, two fixtures);
  `internal/cli/run/providerpreflight_test.go:TestCheckProviderCredentialsRefusesAMissingProvider`
  (asserts the null-drop refusal D4 removes); `internal/packdecl/skewwireapi_test.go` and
  `internal/config/validate_test.go` (enum members).
- **Docs:** `USER_GUIDE.md:217`; `packs/zai/README.md` "What lands where" — record that codex cannot
  reach z.ai's OpenAI route (design §3.3); `AGENTS.md:8`; `packs/embed.go:20`.
- **CLI surface:** `runUsage` `:48-55`, and the timing flag's new name wherever help lists it.
- **Norm:** `just done` before each commit — it runs vet, staticcheck and the short suite.
- **Cheap and yours:** the dialect table's Go shape (map vs switch), the new timing flag's name.
- **Stop and ask:** nothing here. All nine questions are ruled; the design is DECIDED.

## Don't

- **Don't schedule D9.** `derive.lua` being ungated executable pack content absent from
  `trust-paths.md`'s census is filed there as a census gap, not a verdict. Not this plan's work.
- Don't reopen the enum as a free string — that restores what
  [`reference-mismatch-diagnostics.md`](reference-mismatch-diagnostics.md) §7 step 3 closed.
- Don't fix D3 (`packs/zai`'s duplicated URL) here — the ruled fix is composing the provider's fact,
  which needs the sibling plan's derive work. A test pinning the two literals equal is the interim
  if that plan slips.
- Don't "unify" the run and host `-p` parsers (see Traps).

## Blockers

- **Step 2 blocks step 3's pi row**, and it is research with no ruling attached. The codex row is
  source-verified already; the pi row is not.
