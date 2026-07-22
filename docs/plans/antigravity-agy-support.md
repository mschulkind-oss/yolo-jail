# AGY (Google Antigravity CLI) Support Plan

**Date:** 2026-07-22. **Status:** Proposed / Planned.
**Purpose:** Define the design, requirements, touchpoints, and execution plan for adding support for Google Antigravity CLI (`agy`) as an agent in `yolo-jail`.

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

Adding `agy` requires modifications across 8 core areas of the `yolo-jail` codebase:

| Touchpoint Area | Target Files | Key Change |
|---|---|---|
| **1. Agent Registry** | [`internal/agents/agents.go`](../../internal/agents/agents.go) | Add `AgentSpec` entry for `"agy"` in `specs` array. |
| **2. Surface Manifests** | [`internal/agentcfg/builtin.go`](../../internal/agentcfg/builtin.go) | Declare `manifest.Surface` for `agySettings`. |
| **3. Path & Env Helpers** | [`internal/entrypoint/env.go`](../../internal/entrypoint/env.go) | Add `(e *Env) AgyDir()` helper (`filepath.Join(e.Home, ".gemini", "antigravity-cli")`). |
| **4. Provisioning & MCP** | [`internal/entrypoint/agent_configs.go`](../../internal/entrypoint/agent_configs.go) | Implement `ConfigureAgy(e *Env) error` (settings + `mcp_config.json`). |
| **5. Container Boot** | [`internal/entrypoint/boot.go`](../../internal/entrypoint/boot.go) | Add `case "agy":` in `configureAgent`. |
| **6. Global Storage** | [`internal/storage/ensure.go`](../../internal/storage/ensure.go) | Include `.gemini/antigravity-cli` in overlay directory creation. |
| **7. Preflight Validation** | [`internal/cli/check/entrypoint.go`](../../internal/cli/check/entrypoint.go) | Add `agentWriters["agy"]` and output validation in `agentOutputs`. |
| **8. Documentation & Tests** | `docs/`, `internal/cli/config_ref.txt`, `*_test.go` | Update docs and golden test suites for `agy`. |

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

### C. Entrypoint Writer (`internal/entrypoint/agent_configs.go`)
```go
func ConfigureAgy(e *Env) error {
    dir := e.AgyDir()
    if err := os.MkdirAll(dir, 0o755); err != nil {
        return err
    }
    
    // Reconcile shared MCP servers -> mcp_config.json
    configured := e.LoadMCPServers()
    mcpConfig := jsonx.NewOrderedMap()
    mcpConfig.Set("mcpServers", configured)
    if err := writeInPlaceString(filepath.Join(dir, "mcp_config.json"), dumpJSONIndent2(mcpConfig)); err != nil {
        return err
    }

    // Write settings.json with forced permissionMode: allow
    settingsPath := filepath.Join(dir, "settings.json")
    current := loadObject(settingsPath)
    current.Set("permissionMode", "allow")
    return writeInPlaceString(settingsPath, dumpJSONIndent2(current))
}
```

---

## 5. Execution Roadmap & Phases

- **Phase 1: Agent Registry & Validation**
  * Update `internal/agents/agents.go` with the `agy` `AgentSpec`.
  * Update `agents_test.go` and golden assertions.
  * Verify `yolo check` and `internal/config/validate.go` accept `"agy"` in `agents` array.

- **Phase 2: Entrypoint & Shims**
  * Add `AgyDir()` to `internal/entrypoint/env.go`.
  * Add `ConfigureAgy` to `internal/entrypoint/agent_configs.go`.
  * Wire `case "agy":` in `boot.go`.
  * Verify lazy native shim creation in `~/.yolo-shims/agy`.

- **Phase 3: Storage & Check Integration**
  * Add `.gemini/antigravity-cli` to `EnsureGlobalStorage` in `internal/storage/ensure.go`.
  * Update `internal/cli/check/entrypoint.go` to include `agy` validation.

- **Phase 4: Documentation & Test Suite Consolidation**
  * Update `docs/design/agent-briefings.md` and `docs/design/mcp-configuration.md`.
  * Update `docs/guides/USER_GUIDE.md` and `internal/cli/config_ref.txt`.
  * Run `just test-fast` and nested-jail verification (`yolo -- bash`).
