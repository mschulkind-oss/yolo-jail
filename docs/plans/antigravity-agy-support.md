# AGY (Google Antigravity CLI) Support Plan

**Date:** 2026-07-22. **Status:** ✅ Done — born directly on the prism; all eight
touchpoints landed (registry, manifest surface, `AgyDir`, `ConfigureAgyPrism`,
boot wiring, preflight, docs, tests).
**Purpose:** Define the design, requirements, touchpoints, and execution plan for adding support for Google Antigravity CLI (`agy`) as an agent in `yolo-jail`.

> [!NOTE]
> **Prism-native by construction.** This plan was revised (2026-07-22) to align
> with the config-composition ("prism") migration
> ([`agent-settings-composition.md`](agent-settings-composition.md),
> [`config-migration-to-prism.md`](../design/config-migration-to-prism.md)).
> AGY has **zero legacy bespoke state** to migrate — it is a brand-new agent —
> so it is the cleanest possible surface to be born *directly on the prism*
> rather than as yet another bespoke `Configure*` writer that Phase C would then
> have to delete. Concretely:
>
> - `agy`'s `settings.json` is a **manifest surface** (`agySettings` in
>   `internal/agentcfg/builtin.go`), rendered by the shared composition engine
>   through the §5 capture-diff overlay loop — identical to how pi and copilot
>   already boot.
> - Its writer is **`ConfigureAgyPrism`** (in `internal/entrypoint/prism.go`),
>   modeled on `ConfigureCopilotPrism` (no host mount — yolo owns the file
>   outright). There is **no** bespoke `ConfigureAgy`; the §4C sketch that an
>   earlier draft carried is intentionally dropped.
> - AGY is **always** rendered through the prism. It was the first agent to live
>   *only* in the unified config system, with no bespoke path to fall back to;
>   the migrating agents have since caught up — the `YOLO_PRISM_SURFACES` cutover
>   gate is retired and every agent now renders through the prism unconditionally.
> - The dynamic `mcp_config.json` is a pure per-boot overwrite (regenerated from
>   live MCP config, no in-jail edits preserved), so it stays a bespoke sibling
>   exactly like copilot's `mcp-config.json` — the prism owns only the static
>   `settings.json`.

---


## 1. Overview & Context

Google Antigravity is Google's agentic AI coding platform. The **AGY CLI** (`agy`) is its standalone Go-compiled terminal interface:

* **Binary:** Native Go binary (`ELF 64-bit x86-64`, dynamic link). Self-contained, lightweight execution (no Node.js/npm runtime needed for the binary core).
* **Installer:** Native shell installer script (`curl -fsSL ... | bash`) installing to `~/.local/bin/agy`.
* **State & Config Path:** `~/.gemini/antigravity-cli/`
  * `config/settings.json` (Global settings, theme, permission policies)
  * `mcp_config.json` (Model Context Protocol server configuration)
  * `brain/` (Conversation logs and artifact storage)
* **YOLO Posture:** `--dangerously-skip-permissions` (bypasses tool execution approval prompts).
* **Briefing Target:** `AGENTS.md` (or `.gemini/antigravity-cli/AGENTS.md`).

---

## 2. Integration Requirements & Invariants

### A. Coexistence with `gemini`
`yolo-jail` already supports the `"gemini"` agent key (pointing to `@google/gemini-cli`).
* **`gemini`** uses state directory: `~/.gemini`
* **`agy`** uses state directory: `~/.gemini/antigravity-cli`

To ensure full isolation and zero path collisions, `agy`'s overlay directory is explicitly specified as `.gemini/antigravity-cli`.

### B. Installation Model
In `internal/agents/agents.go`, `agy` will be registered with `Kind: "native"` (similar to Claude):
* Binary name: `agy`
* Lazy installation: Shims in `~/.yolo-shims/agy` verify/install the native binary into `$HOME/.local/bin/agy` on first use inside the jail.

### C. Authentication & State
`agy` authenticates using tokens stored in `~/.gemini/antigravity-cli/`. In container environments:
* Overlay directory mounting ensures user session state persists across container restarts.
* Project-level instruction files are injected via the standard briefing staging pipeline.

---

## 3. Codebase Touchpoint Inventory

Adding `agy` requires modifications across these core areas of the `yolo-jail`
codebase. Two of them (storage, config-validation) **auto-derive** from the
registry — adding the `AgentSpec` is enough; they are listed for completeness.

| Touchpoint Area | Target Files | Key Change |
|---|---|---|
| **1. Agent Registry** | [`internal/agents/agents.go`](../../internal/agents/agents.go) | Add `AgentSpec` entry for `"agy"` in `specs` array (native install, overlay `.gemini/antigravity-cli`). |
| **2. Surface Manifest** | [`internal/agentcfg/builtin.go`](../../internal/agentcfg/builtin.go) | Declare `agySettings` `manifest.Surface`; add it to `BuiltinManifest()`. |
| **3. Path & Env Helper** | [`internal/entrypoint/env.go`](../../internal/entrypoint/env.go) | Add `(e *Env) AgyDir()` (`filepath.Join(e.Home, ".gemini", "antigravity-cli")`). |
| **4. Prism Writer** | [`internal/entrypoint/prism.go`](../../internal/entrypoint/prism.go) | Implement `ConfigureAgyPrism(e *Env) error` — renders `settings.json` through `renderSurfaceStateful` (no host mount) + writes the dynamic `mcp_config.json` sibling. **No bespoke `ConfigureAgy`.** |
| **5. Container Boot** | [`internal/entrypoint/boot.go`](../../internal/entrypoint/boot.go) | Add `case "agy":` in `configureAgent` calling `ConfigureAgyPrism` unconditionally (no gate). |
| **6. Preflight Validation** | [`internal/cli/check/entrypoint.go`](../../internal/cli/check/entrypoint.go) | Add `agentWriters["agy"] = ConfigureAgyPrism` and an `agentOutputs["agy"]` entry validating `settings.json` + `mcp_config.json` parse. |
| **7. Documentation** | [`internal/cli/config_ref.txt`](../../internal/cli/config_ref.txt), `docs/` | Add the `agy` bullet to the valid-agents list; note it in the MCP/briefing docs. |
| **8. Tests** | `*_test.go` | Registry/inject/agentsmd, a `ConfigureAgyPrism` first-migration + idempotent test (mirrors `prism_copilot_test.go`), a `builtin_test.go` `agySettings` assertion. |
| _(auto)_ **Global Storage** | [`internal/storage/ensure.go`](../../internal/storage/ensure.go) | No change — `.gemini/antigravity-cli` flows in via `agents.AllOverlayDirs` once the registry entry lands. |
| _(auto)_ **Config Validation** | [`internal/config/derived.go`](../../internal/config/derived.go) | No change — `validAgentSet` derives from `agents.ValidAgents`, so `"agy"` is accepted automatically. |

---

## 4. Implementation Specification

### A. Registry Entry (`internal/agents/agents.go`)
```go
{
    Name: "agy",
    Install: InstallSpec{
        Kind:         "native",
        Bin:          "agy",
        InstallerURL: "https://antigravity.google.com/install.sh", // official installer URL
    },
    ConfigWriter: "configure_agy",
    Briefing: BriefingSpec{
        Staging:    "AGENTS-agy.md",
        Mount:      ".gemini/antigravity-cli/AGENTS.md",
        HostSource: ".gemini/antigravity-cli/AGENTS.md",
    },
    OverlayDirs: []string{".gemini/antigravity-cli"},
    Skills:      ".gemini/antigravity-cli/skills",
    YoloFlags:   []string{"--dangerously-skip-permissions"},
    Alias:       "",
}
```

### B. Surface Manifest (`internal/agentcfg/builtin.go`)
```go
var agySettings = manifest.Surface{
    Agent: "agy",
    Name:  "settings",
    Path:  "~/.gemini/antigravity-cli/settings.json",
    Codec: "json",
    Managed: map[string]any{
        "permissionMode": "allow",
    },
}
```

### C. Prism Writer (`internal/entrypoint/prism.go`)

AGY is born on the prism, so its writer is a thin sibling of
`ConfigureCopilotPrism`: render `settings.json` through the shared engine (no
host mount — yolo owns the file), then write the dynamic `mcp_config.json`
sibling. The `permissionMode: "allow"` posture lives in the `agySettings`
manifest's **Managed** layer (§4B), so the writer never hand-sets it.

```go
// ConfigureAgyPrism configures the Google Antigravity CLI (agy). AGY is a
// brand-new agent with zero legacy bespoke state, so it is born directly on the
// prism — there is no bespoke ConfigureAgy. It:
//
//  1. renders ~/.gemini/antigravity-cli/settings.json through the engine with
//     §5 overlay capture and the §3.2 first-migration bootstrap. AGY has NO host
//     mount (yolo owns the file), so hostBytes is nil and there is no computed
//     layer, so the render is defaults<overlay<managed (the managed
//     permissionMode:"allow" is the YOLO posture);
//  2. writes the dynamic mcp_config.json sibling from live MCP config — a pure
//     per-boot overwrite (no in-jail edits preserved), exactly like copilot's
//     mcp-config.json. The prism owns only the static settings.json.
func ConfigureAgyPrism(e *Env) error {
    if err := os.MkdirAll(e.AgyDir(), 0o755); err != nil {
        return err
    }
    // settings.json: no host source (yolo owns it outright), no computed layer.
    if _, err := renderSurfaceStateful(e, "agy", "settings", nil, nil); err != nil {
        return err
    }
    // Dynamic mcp_config.json sibling (pure overwrite, regenerated every boot).
    mcpConfig := jsonx.NewOrderedMap()
    mcpConfig.Set("mcpServers", e.LoadMCPServers())
    return writeInPlaceString(filepath.Join(e.AgyDir(), "mcp_config.json"), dumpJSONIndent2(mcpConfig))
}
```

### D. Boot Wiring (`internal/entrypoint/boot.go`)

```go
case "agy":
    genStep(e, "configure_agy", func() error { return ConfigureAgyPrism(e) })
```

`configureAgent` calls every agent's `Configure*Prism` writer unconditionally —
there is one config path now, so AGY needs no special-casing.

---

## 5. Execution Roadmap & Phases

- **Phase 1: Registry, manifest, env helper**
  * Add the `agy` `AgentSpec` to `internal/agents/agents.go` (native install,
    overlay `.gemini/antigravity-cli`, briefing `AGENTS.md`, YOLO flag
    `--dangerously-skip-permissions`).
  * Add `agySettings` to `internal/agentcfg/builtin.go` and to
    `BuiltinManifest()`; add a `builtin_test.go` assertion.
  * Add `(e *Env) AgyDir()` to `internal/entrypoint/env.go`.
  * Verify `internal/config` accepts `"agy"` (auto — `validAgentSet` derives from
    the registry).

- **Phase 2: Prism writer + boot**
  * Add `ConfigureAgyPrism` to `internal/entrypoint/prism.go`.
  * Wire `case "agy":` in `boot.go` (unconditional — no gate).
  * TDD: a first-migration + idempotent test mirroring `prism_copilot_test.go`
    (config lands with `permissionMode:"allow"`, sidecars seeded, overlay `{}`,
    `mcp_config.json` written).
  * Verify lazy native shim creation in `~/.yolo-shims/agy`
    (`GenerateAgentLaunchers` already handles `Kind:"native"`).

- **Phase 3: Check integration**
  * Add `agentWriters["agy"] = ConfigureAgyPrism` and an `agentOutputs["agy"]`
    entry (`settings.json` + `mcp_config.json` parse) in
    `internal/cli/check/entrypoint.go`.

- **Phase 4: Docs & verification**
  * Add the `agy` bullet to `internal/cli/config_ref.txt`; note it in
    `docs/design/agent-briefings.md` and `docs/design/mcp-configuration.md` and
    `docs/guides/USER_GUIDE.md`.
  * Run `just test-fast`; nested-jail verify (a throwaway `{"agents":["agy"]}`
    workspace, two boots to prove the §5 capture loop).
