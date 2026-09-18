---
title: "Protocol resolution — implementation sketch"
date: 2026-09-18
status: draft
tags: [providers, packs, wire-bridge, protocols, sketch]
summary: "Parking lot for the implementation material of protocol-resolution.md: the file map, the refusal strings, the test seams, and what each step may not touch. Not a hand-off artifact while the design's questions are open."
---

# Protocol resolution — implementation sketch

**Status:** SPENT, 2026-09-18 — every step it sketches is built (the design's Status line names the seven commits). It is kept as the record of what the build had to find out that the sketch did not know: there were SIX derives reading the shorthand, not four; `8214` had to keep living in the adapter's own manifest, since that is where the declaration is; and the refusal landed at `packload.AgentEnv` rather than in the run pre-flight, because the gate needs a SELECTION and the pre-flight reads the merged user config only.

**Design:** [`protocol-resolution.md`](protocol-resolution.md). The design wins on behaviour;
nothing here decides any. Entries resting on an unruled question say so.

## File map, by build step

Steps are [§10](protocol-resolution.md#10-what-i-would-build-in-order)'s.

| Step | Files |
| :--- | :--- |
| 1 — the pair rule | [`packs/claude/derive.lua`](../../packs/claude/derive.lua) (the `if p.api_key` branch), and its test beside the other derive tests in [`internal/entrypoint/providerderive_test.go`](../../internal/entrypoint/providerderive_test.go), which runs each pack's REAL `derive.lua` through `deriveComputedLayer` |
| 2 — the agent declaration | [`internal/packdecl`](../../internal/packdecl) (the field, beside the `program` kind's `capabilities`), the shipped packs that declare a program, and the census tests that enumerate kinds |
| 3 — the resolver, outcomes 1 and 4 | a new package or a file beside the provider composition in [`internal/packload`](../../internal/packload); the refusal prints where the capability gate does, in the run pre-flight |
| 4 — the adapter declaration | a NEW contribution kind in [`internal/packdecl`](../../internal/packdecl) (`from`, `to`, address; no daemon assumed), plus the kind census tests; touches `packs/wire-bridge/`, `packs/cerebras/pack.json`, `packs/kilo/pack.json` and [`packs/claude/derive.lua`](../../packs/claude/derive.lua)'s `8215` literal |
| 5 — outcome 3 | the same refusal site as step 3 |
| 6 — delete the shorthand | [`internal/config`](../../internal/config)'s provider validation (the refusal), the four derives that read it — `claude`, `pi`, `opencode`, `omp` — and [`internal/cli/config_ref.txt`](../../internal/cli/config_ref.txt) |
| 7 — configurable address | the loophole `settings` mechanism, which already gives a manifest typed keys with a `user` scope and hands the daemon a file path through the `{settings}` argv token |

## Refusal strings, first drafts

The shorthand (step 6) follows the retired-key shape — a refusal that names its replacement:

```
config.providers.myapi.base_url: removed — say which protocol this URL speaks.
  endpoints.anthropic.base_url  the agent's own wire
  endpoints.openai.base_url     routed through an adapter automatically
```

Outcome 3 names the pack, which is the whole discoverability argument:

```
Refusing to launch: provider 'cerebras' speaks openai; agent 'claude' speaks anthropic.
  wire-bridge adapts openai → anthropic. Add it to `packs` and this pairing resolves.
```

Outcome 4 names both sides and where each was declared, so a wrong declaration is visible in the
refusal rather than in a later request.

## Reuse, with reasons

- **The capability gate is the precedent for the whole of step 3** — a census built from
  declarations, a refusal in the run pre-flight, and a prediction in `yolo check`. It shipped
  2026-09-18 (`f5c26899`); copy its shape including the ⚠ that the check-side copy must not
  consult data the launch cannot see.
- **`internal/wirebridged`'s route selection already does outcome 2's upstream half**: it reads
  the selection table, finds the provider whose anthropic endpoint is jail-local, and serves that
  provider's openai endpoint upstream. The resolver replaces where the *address* comes from, not
  what the bridge does with it.
- **`packs/journal`'s `full` key** is the shape for step 7's setting (`scope: "user"`).

## Traps

- **Do not put a protocol name in core.** The refusal messages quote declarations; they do not
  switch on them. A `case "anthropic":` anywhere in `internal/` is the defect this design exists
  to avoid.
- **Step 2 is inert by design** — if landing it changes any launch, something read it early.
- **The `8215` literal is not cerebras-shaped.** It is claude's own openai-codex branch, pointing
  at `internal/wirebridged`'s `CodexResponsesListenAddr`. It moves with step 4 but it is a
  different case from `8214`: the provider there is `openai-codex`, which declares no endpoint at
  all.
- **`grep -r 8214 packs/`** is step 4's done-condition, and `packs/*/README.md` carries the number
  too — the docs are part of the move, not a follow-up.

## Blocked

**Nothing, and nothing was.** All seven questions are ruled ([Decision ledger](protocol-resolution.md#12-decision-ledger)),
and step 1 has shipped (`bc16e8c3`). What round three changed for this sketch: the adapter is its
own contribution kind carrying `from`, `to` and an address — NOT a field on `service` — so step 4
adds a kind rather than extending one, and `packs/wire-bridge` declares both (the service it runs
and the adaptation it provides) instead of folding them together. An adapter that names a remote
or user-run address declares no service at all, which is the case the coupled shape could not
express.
