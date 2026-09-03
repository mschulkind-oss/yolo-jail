---
title: "Plan: catalog and selection"
date: 2026-09-01
status: shipped
tags: [plan, providers, profiles, derives, selection]
summary: "Implementation plan for providers.md — env-emitting derives, deleting the placeholder vocabulary, and the use_profiles rename. The research step that gated build order step 1 closed 2026-09-02, and every step has since run."
---

# Plan: catalog and selection

**Design:** [`providers.md`](../reference/providers.md) (all nine ruled)
· **Status:** SHIPPED 2026-09-02 — step 1's research closed from source (`070a3574`), the
`use_profiles` rename landed whole (`43d24e9e`), the env-emitting derives replaced `env_shape` in
both notches (`f55f2109`, `3144fbed`), codex and then pi/opencode got their selection keys
(`58437e8b`, `6d1d7c54`), and `profiles` + `options` shipped while `kind: "profile"` shrank to
name + provider (`8aceeb0f`, `767170ec`, `714e987e`). · Written against `578c7e5f`, 2026-09-01.

**Precedence:** the design wins on behavior; the tree wins on fact; this file is advice and is the
first thing to be wrong. Never twist code to match it.

Sibling: [`provider-table-fidelity-plan.md`](provider-table-fidelity-plan.md) owns the defect list
(D1–D9). Cited here, never duplicated — the `wire_api` dialect fix and the `base_url`/`endpoints`
refusal are its steps, not these.

## Map

| Path | Change |
| :--- | :--- |
| `internal/agentcfg/luahook/derive.go` | derive gains an env output; `DeriveCtx` gains the active profile + selected provider |
| `internal/entrypoint/packsurfaces.go` | drive an env derive — **not** surface-keyed (see traps) |
| `internal/entrypoint/hostrender.go` | NEW host-side env-derive invocation with real tables — not `hostTableKeys`, which is a sentinel probe |
| `internal/agentenv/agentenv.go` | delete `agentProtocols`, `ProtocolFor`, `Resolve`, `providerVars`; keep `Var`/`Apply` |
| `internal/cli/run/profilechannel.go:93` | drop the `agentenv.Resolve` loop |
| `internal/cli/host.go:432` | same |
| `internal/packdecl/contributes.go` | delete `EnvShape` + validators + placeholder consts (~190-202, ~1301, ~1492-1568); add `options` |
| `internal/config/validate.go` | delete `validateProviderEnvShape` (:1046) and its call (:909); add `profiles` validation |
| `internal/config/config.go:110` | `env_shape` out of `knownProviderKeys`; `options` in; `pack_profiles`→`use_profiles` |
| `internal/config/packs.go` | new — load/validate the `profiles` declaration map |
| `packs/claude/derive.lua` | new env derive; absorbs what `env_shape` did |
| `packs/{codex,pi,opencode}/derive.lua` | write the selection key (steps 3–4) |
| `packs/{zai,claude}/pack.json` | `env_shape` out; `options` in |

## Reuse

- **`internal/packoverlay/packoverlay.go:194`** — the `profile:` gate, keyed on the **target
  surface's** agent. Copy that keying for any new gate; it is what makes a CLI-less pack reachable.
- **`ctx.tombstone` / `ctx.empty_array`** (`luahook/derive.go:65,93-103`) — `LUserData` sentinels
  round-tripping Lua→Go. If anything needs a marker, this is the established shape.
- **`packload.ProfileTable`** — already lowers the profile table and drops nulls correctly. Do not
  re-lower.
- **Retired-key pattern:** `validateAgentProfilesRetired` (`config/validate.go`) — error on host,
  warn in-jail. `agent_profiles` keeps it; `pack_profiles` gets **none** (never in a release —
  landed 2026-08-31, `v0.8.0` is 2026-08-13; same call the maintainer made for `api_key_env`).
- Integration shape: `integration/packs_test.go`, `mcp_test.go`.

## Traps

- **Env composition is a HOST-launch-time thing today; a derive runs IN-JAIL at boot.** Three call
  sites resolve it now — `profilechannel.go:93` (container argv), `host.go:432` (`yolo host`, where
  **there is no jail at all**), and macos-user via `Options.PackEnv`. `agentenv`'s package doc names
  jail/host parity as the reason it is one implementation. **Constraint:** an env derive that runs
  only in the entrypoint silently drops `yolo host`. **`hostrender.go:377` is NOT the seam** —
  `hostTableKeys` probes for KEY NAMES against a sentinel, and its comment is explicit that content
  deliberately does not cross ("a jail's derived MCP table embeds jail-absolute paths … which is why
  a host render passes no computed layer"). A host-side env derive needs REAL tables: a new
  invocation, not a reuse.
- **Derives are keyed `(agent, surface)`** and the boot loop iterates surfaces
  (`packsurfaces.go:193`). Env is not a surface, so it needs a driver that is not the surface loop.
  Reusing `Surface: "env"` is the cheap route; it collides with a real surface named `env`.
- **Derive errors already propagate.** `deriveComputedLayer` wraps, `genStep`→`genFailure` fails
  the boot. The design's "needs error propagation" is only true of the new env path — do not build
  a second reporting channel.
- **`ActiveProfiles` (`packload.go:~223`) iterates the pack's own `InstallBins()`**, so a CLI-less
  pack's variant never activates. Relevant to the `kind: "profile"` shrink; the modifier form has
  no such case.
- **`options`: `null` means *declared, no default*, NOT delete.** Departs from merge-patch
  everywhere else in this config. Say it in the key's doc comment.

## Build order

1. **Research pi's and opencode's selection surface.** No code. pi's row is EMPTY, opencode's is
   INFERRED (`"<provider>/<model>"`, from the format its `models` command prints; the shipped binary
   is minified). **Constraint:** AGENTS.md forbids running agent CLIs beyond `--version` — read
   source/docs, do not launch `pi` to look. Write the findings into design §3 with dates.
   → nothing to run; the gate is that §3 has no empty row.
2. **`use_profiles` rename, alone.** 92 non-test + 88 test occurrences of
   `pack_profiles`/`YOLO_PACK_PROFILES`/`PackProfiles`. Mechanical, its own commit, rides nothing.
   → `just check-ci`
3. **Env-emitting derives + delete the placeholder vocabulary**, both notches, `packs/claude`
   converted. This is the big one and it is all-or-nothing: the deletions and the derive must land
   together or claude loses its endpoint. → `just test-fast`, then a real jail launch.
4. **Selection for codex** (verified row: `model_provider` + `model`). One derive, testable alone.
   → `just check-ci`
5. **Selection for pi and opencode**, once step 1 says where. → `just check-ci`
6. **`profiles` + `options` config surface**; `kind: "profile"` shrinks to name + provider.
   → `just check`

## Ships with

- **Integration, and it is the point:** first-ever coverage for zai/profiles/providers. Assert the
  rendered `~/.codex/config.toml` carries `model_provider`, and that claude's env carries
  `ANTHROPIC_BASE_URL`. Package `integration`, `requireJail(t)`, **no `t.Parallel()`** (AGENTS.md).
- **Unit:** provider with no endpoint (bedrock — must reach no catalog); profile naming an
  undeclared option (errors, naming what the provider accepts); `options` null; no-profile launch
  writes **nothing** to a selection key (OQ-CS2).
- **Rewrites, not repairs:** `internal/agentenv/agentenv_test.go` (367 lines) largely goes with
  `Resolve`. `packdecl`/`packload` env_shape skew tests assert a vocabulary that no longer exists —
  delete, do not fix. `internal/cli/run/agentprofileenv_test.go`, `providershapeenv_test.go`,
  `hostprovidershape_test.go` assert the old composition path.
- **Docs describing the old behavior:** `packs/zai/README.md` (the "what lands where" table and its
  env_shape story), `yolo config-ref` provider/profile text (`internal/cli/configref.go`),
  `AGENTS.md` pack census (says ten; there are twelve), `packs/embed.go:20` same list.
- **Config surfaces:** `providers.*.options`, top-level `profiles`, `use_profiles` — all
  **user-scope only** (OQ-CS5), the rule `packs` follows. `env_shape` removed from the census.
- Norms: `just check-ci` before each commit; `just format` first.

## Don't

- **Don't gate the catalog on selection.** Option B, rejected — pi and opencode have interactive
  model pickers and a populated directory is the feature.
- **Don't add `extends` to profiles** (OQ-CS9). Option defaults already remove the duplication.
- **Don't add value validation for options** (OQ-CS7). Core checks the key census only; the derive
  validates. A typechecker here is `wire_api`'s enum one layer up.
- **Don't keep `agentProtocols` "just for the host path."** It is the agent-name table core is
  supposed to stop holding; if the host notch still needs a protocol, the host derive supplies it.

## Blockers

- **Step 1 is a hard gate.** Design §3's pi row is empty. Steps 5 cannot be designed around it, and
  guessing produces exactly D1 (a value written into an agent's config that the agent rejects).
- **The host notch runs the env derive. Constraint, not a choice** — `yolo host -- claude` composing
  the same environment is behaviour `host-agent-environment.md` §2.2 fixes, and OQ-CS10 was withdrawn
  for having asked it as though it were open. Budget the new host-side invocation (see Traps); do not
  budget a decision.
- **Cheap and yours:** how the env derive's return is keyed and shaped. Advice: whatever the surface
  loop's marshaller already gives you — this is one map of strings.
