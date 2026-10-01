---
title: "Implementation sketch: Pi Codex provider shadowing"
date: 2026-09-27
status: accepted
stage: BUILT
next: "Nothing here unless OQ-3 rules the broad reading, which grows §2's exclusion into a per-agent list of natively implemented providers"
depends-on:
  - pi-codex-provider-shadowing.md#OQ-3
tags: [providers, codex, pi, openai-auth, shadowing, plan]
summary: "File targets and verification for pi-codex-provider-shadowing.md: the openai-codex exclusion in pi's derive and the needs-closure test helper are built, and a real -p codex launch asserts pi's models.json has no openai-codex row; a broad reading of OQ-3 would widen the exclusion."
---

# Implementation Sketch: Pi Codex Provider Shadowing

**Status:** 2026-10-01 — [§2](#2-pi-derive-changes) and [§3](#3-entrypoint-test-alignment) are built (`92c20cc6`, and the 2026-09-27 test helper), and [§5](#5-verification-checklist)'s last step is an integration launch, a container jail the suite starts, rather than a hand-run one. MEASURED: `TestCodexProfileRendersOneModelListForEveryAgent` ([`codex_model_list_test.go`](../../integration/codex_model_list_test.go)) launches `-p codex` over pi, claude, codex and opencode and finds pi's rendered `models.json` holding no `openai-codex` row beside `defaultProvider = "openai-codex"`; with the exclusion removed the same launch rendered that row and the test failed (revert-checked 2026-10-01). UNMEASURED: no pi session was run, so no request reached the subscription. Unstable while [OQ-3](pi-codex-provider-shadowing.md#OQ-3) is open, which decides whether the exclusion widens past `openai-codex`.

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
| `docs/plans/roadmap.md` | Living roadmap | Links the design doc and its open question for priority ([§4](#4-roadmap-tracking)). |

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
`native` flag that also gates the via row. If [OQ-3](pi-codex-provider-shadowing.md#OQ-3) rules the
broad reading, this guard grows a per-agent list of natively implemented providers.

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

[OQ-1](pi-codex-provider-shadowing.md#OQ-1) and [OQ-2](pi-codex-provider-shadowing.md#OQ-2) are
ruled. [OQ-3](pi-codex-provider-shadowing.md#OQ-3) is open in the design doc, which owns its state;
the roadmap links that question for its place in the order.

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
