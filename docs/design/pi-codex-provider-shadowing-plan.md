---
title: "Implementation sketch: Pi Codex provider shadowing"
date: 2026-09-27
status: accepted
stage: DECIDED
next: "Build §2.1, OQ-3's broad reading (ruled 2026-10-05): each agent pack declares its built-in provider names, pi's, omp's and opencode's derives write no row under one, via rows included, and a profile's plan selects the agent's own provider or the launch says it cannot; hold the openai-codex list and pi's native Bedrock row as they are until OQ-4 rules"
depends-on:
  - pi-codex-provider-shadowing.md#OQ-3
tags: [providers, codex, pi, openai-auth, shadowing, plan]
summary: "File targets and verification for pi-codex-provider-shadowing.md: the openai-codex exclusion in pi's derive and the needs-closure test helper are built, and a real -p codex launch asserts pi's models.json has no openai-codex row. OQ-3 ruled the broad reading on 2026-10-05, so the exclusion widens to every provider an agent has built in, declared per agent pack; that build is not started."
---

# Implementation Sketch: Pi Codex Provider Shadowing

**Status:** 2026-10-05 — [OQ-3](pi-codex-provider-shadowing.md#OQ-3) ruled the broad reading, so
[§2.1](#21-the-broad-reading-the-build) is the next build, not started;
[OQ-4](pi-codex-provider-shadowing.md#OQ-4), filed the same day, holds two of its rows. Before
that ruling, 2026-10-01: [§2](#2-pi-derive-changes) and [§3](#3-entrypoint-test-alignment) are built (`92c20cc6`, and the 2026-09-27 test helper), and [§5](#5-verification-checklist)'s last step is an integration launch, a container jail the suite starts, rather than a hand-run one. MEASURED: `TestCodexProfileRendersOneModelListForEveryAgent` ([`codex_model_list_test.go`](../../integration/codex_model_list_test.go)) launches `-p codex` over pi, claude, codex and opencode and finds pi's rendered `models.json` holding no `openai-codex` row beside `defaultProvider = "openai-codex"`; with the exclusion removed the same launch rendered that row and the test failed (revert-checked 2026-10-01). UNMEASURED: no pi session was run, so no request reached the subscription.

This sketch holds implementation notes, file targets, and test verification details for
[`pi-codex-provider-shadowing.md`](pi-codex-provider-shadowing.md). The design doc wins on
all questions of behavior, architecture, and invariants.

---

## 1. File Map

| File | Role | Change Summary |
| :--- | :--- | :--- |
| `packs/pi/derive.lua` | Pi configuration derive script | Exclude `openai-codex` from the `models` catalog derive loop ([§2](#2-pi-derive-changes)). Built in `92c20cc6`, as [OQ-1](pi-codex-provider-shadowing.md#OQ-1) ruled. |
| `internal/entrypoint/pi_codex_profile_test.go` | Entrypoint Pi profile tests | Compose `TestPiCodexProfileSelectsBuiltInProviderAndExplicitModel` from pi's `needs` closure, which brings `openai-auth`, to activate the shadowing check ([§3](#3-entrypoint-test-alignment)). Built 2026-09-27. |
| `internal/entrypoint/packclosure_test.go` | Test helper | `testPacksForAgent`, the `needs` closure through the launch's resolver (the design doc's R2). Built 2026-09-27. |
| `packs/*/pack.json`, `internal/packdecl` | Agent pack manifests | Each agent pack declares the names of its agent's built-in providers ([§2.1](#21-the-broad-reading-the-build) step 1). Not built. |
| `packs/pi/derive.lua`, `packs/omp/derive.lua`, `packs/opencode/derive.lua` | Catalog and settings derives | Write no row, via rows included, under a declared name, and no yolo model id for that provider; select the agent's own provider for a profile's plan (steps 2 to 4). Not built. |
| `docs/plans/roadmap.md` | Living roadmap | Links this plan as the build, and the design doc's open question for priority ([§4](#4-roadmap-tracking)). |

---

## 2. Pi Derive Changes

In `packs/pi/derive.lua`, inside `yolo.derive("pi", "models", function(ctx) ...)`, as this sketch first
proposed it:

```lua
  local providers = {}
  for name, prov in pairs(ctx.providers) do
    -- openai-codex is Pi's built-in subscription provider backed by OAuth
    -- credentials, not a custom third-party endpoint with an API key.
    -- Shadowing it in models.json overrides Pi's native openai-codex-responses
    -- client and causes ambient API keys to be sent to ChatGPT's backend.
    if name ~= "openai-codex" then
      local baseUrl, api = piReachable(prov)
      if baseUrl then
        ...
      end
    end
  end
```

[OQ-1](pi-codex-provider-shadowing.md#OQ-1) ruled the name check. Built in `92c20cc6` as a
`native` flag that also gates the via row. [OQ-3](pi-codex-provider-shadowing.md#OQ-3) ruled the
broad reading on 2026-10-05, so this guard grows into a per-agent list of built-in providers
([§2.1](#21-the-broad-reading-the-build)).

### 2.1 The broad reading: the build

Not started. The design's [what the ruling settles](pi-codex-provider-shadowing.md#what-the-ruling-settles)
wins on behavior; what follows is where it lands. Agent facts were read from the copies installed
in this jail on 2026-10-05 (pi 1.0.1, opencode 1.18.34), not run, so the build re-reads them from
the versions it pins.

1. **Each agent pack declares its built-in provider names**, and never a model. The field is new
   in `internal/packdecl`, and core reads it only as a list of names, so it knows no agent. Every
   agent pack whose derive writes a catalog declares one: pi, omp and opencode, and codex, whose
   `openai-codex` check moves into its list. Whether a list holds every built-in name or only the
   ones a shipped provider can collide with is the build's choice; the whole list also catches a
   user's provider of that name. The list goes stale with an agent release (the design's R3).
2. **No derive writes a row under a declared name.** pi's `native` flag becomes membership in the
   list, gating the catalog row and the via row as it does for `openai-codex` now; omp's and
   opencode's catalog loops take the same check. The settings derives write no yolo model id for
   such a provider either: pi's settings derive still names `glm-4.6` in `enabledModels`, which
   pi's own `zai` does not list.
3. **A profile's plan selects the agent's own provider for it.** The agent pack names, beside its
   list, its own provider for a yolo provider whose plan needs another address: for opencode, yolo's
   `zai` is `zai-coding-plan`, which reads `ZHIPU_API_KEY`, so the credential reaches opencode under
   that name. pi 1.0.1's own `zai` already calls the coding plan. omp's is read first. When the agent
   has no provider for the plan, the launch prints one line saying the profile cannot reach that
   agent's own client, naming what to run instead.
4. **Via stops for those providers** in that agent: no via row, and the launch's existing "the via
   has no effect" line says so (`viapack_test.go`). [WG-I36](wire-bridge-gateway.md#WG-I36)'s
   Converse row may not sit on pi's `amazon-bedrock`; that route is wire-bridge-gateway.md's build.
5. **Two rows wait for [OQ-4](pi-codex-provider-shadowing.md#OQ-4)** and stay as they are: the
   `openai-codex` list (pi's `yolo-openai-codex-models.json`, which the extension registers, and
   opencode's `openai` model rows) and pi's native Bedrock row under `amazon-bedrock`.
6. **Tests.** Per agent, compose the `needs` closure with every shipped provider
   (`testPacksForAgent`) and assert no catalog row under any declared name, revert-checked by
   dropping one name from the pack's list. Pin the call site, not the list: removing the check from
   a derive must fail the test. One integration launch, `-p zai`, renders pi's `models.json` with no
   `zai` row and opencode's config selecting `zai-coding-plan`.

---

## 3. Entrypoint Test Alignment

Built 2026-09-27 with a helper instead of a hand-added pack: `testPacksForAgent(t, "pi")` returns
pi and whatever `packload.Selection.Close` adds, so the fixture follows pi's `needs`. The sketch
this section first carried, in `TestPiCodexProfileSelectsBuiltInProviderAndExplicitModel` in
`internal/entrypoint/pi_codex_profile_test.go`:

```go
func TestPiCodexProfileSelectsBuiltInProviderAndExplicitModel(t *testing.T) {
	pi := shippedPiPack(t)
	openaiAuth, err := embeddedPack("openai-auth")
	if err != nil {
		t.Fatalf("embedded openai-auth: %v", err)
	}
	packs := []*packload.Pack{pi, openaiAuth}
	providers, err := packload.ComposeProviders(nil, packs)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := packload.ResolveProfiles(packs, nil, providers)
	if err != nil {
		t.Fatal(err)
	}
	...
```

With `openai-auth` included in the composed providers, `ctx.providers["openai-codex"]` is
present during render. The shadow assertion, as the test carried it then:

```go
	models := r.piModels(t)
	if catalog, _ := models["providers"].(map[string]any); catalog != nil {
		if _, shadowed := catalog["openai-codex"]; shadowed {
			t.Fatalf("models.json shadows Pi's built-in openai-codex provider: %#v", catalog)
		}
	}
```

will fail if `packs/pi/derive.lua` has not excluded `openai-codex`, and pass when the fix is in place.
Revert-checked 2026-09-27: removing the exclusion fails the edited test and passes the pre-edit one.

---

## 4. Roadmap Tracking

[OQ-1](pi-codex-provider-shadowing.md#OQ-1), [OQ-2](pi-codex-provider-shadowing.md#OQ-2) and
[OQ-3](pi-codex-provider-shadowing.md#OQ-3) are ruled. The roadmap links this plan for the build's
place in the order, and [OQ-4](pi-codex-provider-shadowing.md#OQ-4), open in the design doc, for
its own.

---

## 5. Verification Checklist

Once the design is decided and ready to implement:

1. **Reproduce failure first:**
   Update `TestPiCodexProfileSelectsBuiltInProviderAndExplicitModel` to include `openai-auth` without
   editing `packs/pi/derive.lua`. Watch it fail with `models.json shadows Pi's built-in openai-codex provider`.
2. **Apply derive fix:**
   Add `name ~= "openai-codex"` in `packs/pi/derive.lua`.
3. **Run unit tests:**
   `go test -short ./internal/entrypoint -run TestPiCodex`
   `just test-fast`
4. **Launch verification:** ✅ 2026-10-01, as an integration launch instead of a hand-run
   nested jail. `TestCodexProfileRendersOneModelListForEveryAgent`
   ([`codex_model_list_test.go`](../../integration/codex_model_list_test.go)) launches `-p codex`
   and asserts both halves: `~/.pi/agent/models.json` has no `openai-codex` row, and
   `~/.pi/agent/settings.json` has `defaultProvider = "openai-codex"`. It runs `true` rather than
   `pi -c`, since no test starts an agent. The launch composes `openai-codex` alone, so the passing
   file is an empty catalog (`"providers": {}`); with the `native` check in
   `packs/pi/derive.lua`'s catalog loop set to `false`, the same launch wrote the row and the
   assertion failed.
5. **[§2.1](#21-the-broad-reading-the-build)'s build, once it lands:** its step 6, then
   `just test-fast` and the integration tests the change reaches (`rg integration/` for
   `models.json`, `zai` and the via disclosure), since the derives' output is read at a real
   launch.
