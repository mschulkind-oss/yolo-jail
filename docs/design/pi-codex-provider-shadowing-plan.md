# Implementation Sketch: Pi Codex Provider Shadowing

**Status:** SKETCH, 2026-09-25 — incomplete, and unstable while questions are open.

This sketch holds implementation notes, file targets, and test verification details for
[`pi-codex-provider-shadowing.md`](pi-codex-provider-shadowing.md). The design doc wins on
all questions of behavior, architecture, and invariants.

---

## 1. File Map

| File | Role | Change Summary |
| :--- | :--- | :--- |
| `packs/pi/derive.lua` | Pi configuration derive script | Exclude `openai-codex` from the `models` catalog derive loop ([§2](#2-pi-derive-changes)). Blocked on [OQ-1](pi-codex-provider-shadowing.md#OQ-1). |
| `internal/entrypoint/pi_codex_profile_test.go` | Entrypoint Pi profile tests | Include `openai-auth` in `TestPiCodexProfileSelectsBuiltInProviderAndExplicitModel` to activate the shadowing check ([§3](#3-entrypoint-test-alignment)). |
| `docs/plans/roadmap.md` | Living roadmap | Track the design doc and open questions ([§4](#4-roadmap-tracking)). |

---

## 2. Pi Derive Changes

In `packs/pi/derive.lua`, inside `yolo.derive("pi", "models", function(ctx) ...)` around line 299:

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

Blocked on [OQ-1](pi-codex-provider-shadowing.md#OQ-1) — if the user rules on an explicit
provider attribute instead of a name check, this guard will check that attribute instead.

---

## 3. Entrypoint Test Alignment

In `internal/entrypoint/pi_codex_profile_test.go`, in `TestPiCodexProfileSelectsBuiltInProviderAndExplicitModel`
([lines 18–32](../../internal/entrypoint/pi_codex_profile_test.go#L18-L32)):

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
present during render. The existing assertion at lines 71–75:

```go
	models := r.piModels(t)
	if catalog, _ := models["providers"].(map[string]any); catalog != nil {
		if _, shadowed := catalog["openai-codex"]; shadowed {
			t.Fatalf("models.json shadows Pi's built-in openai-codex provider: %#v", catalog)
		}
	}
```

will fail if `packs/pi/derive.lua` has not excluded `openai-codex`, and pass when the fix is in place.

---

## 4. Roadmap Tracking

Record the design doc in `docs/plans/roadmap.md` under `## 💬 Needs you`, linking
[OQ-1](pi-codex-provider-shadowing.md#OQ-1) and [OQ-2](pi-codex-provider-shadowing.md#OQ-2).

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
4. **Nested jail verification:**
   `just build-go`
   Launch a nested jail with profile `codex`:
   `YOLO_REPO_ROOT=/workspace /workspace/dist-go/linux-$(go env GOARCH)/yolo -p codex -- pi -c`
   Verify `~/.pi/agent/models.json` has no `openai-codex` row, while `~/.pi/agent/settings.json`
   has `defaultProvider = "openai-codex"`.
