# Implementation Plan Sketch: Attach Skew and Contract Guardrails

**Status:** SKETCH, 2026-09-26 — incomplete, and unstable while questions are open.

> **Precedence.** This is the companion sketch to
> [`attach-skew-and-contract-guardrails.md`](attach-skew-and-contract-guardrails.md).
> The design doc wins on all behavioral decisions; this sketch holds file maps,
> signature sketches, and implementation notes.

---

## File Map

| Path | Role |
| :--- | :--- |
| `internal/cli/run/attachskew.go` | Current attach skew warning. Expand or replace with capability-aware check. |
| `internal/cli/run/capabilities.go` | *(New)* Defines known yolo binary capabilities and capability resolution. |
| `internal/cli/run/run.go` | `attachExisting` and `deliverChannelOnAttach` call sites. Gate placement. |
| `internal/cli/run/assemble.go` | `commonEnvBlock`: injects `YOLO_CAPABILITIES` into container environment. |
| `internal/packdecl/manifest.go` | Surface declarations: add `requires_capabilities` field. |
| `internal/entrypoint/prism.go` | Config surface rendering: skip surfaces whose capability requirements are unmet. |
| `packs/agy/pack.json`, `packs/claude/pack.json` | Annotate statusline contributions with required capabilities. |

---

## Key Signatures & Schemas

### 1. Capability Definition (`internal/cli/run/capabilities.go`)

```go
package run

// KnownCapabilities is the registered set of capabilities that a yolo-jail
// binary prefix can advertise.
const (
    // CapInternalFooter indicates support for `yolo internal footer`.
    CapInternalFooter = "internal-footer"
    // CapWirebridgeSigV4 indicates wire-bridge SigV4 request signing.
    CapWirebridgeSigV4 = "wirebridge-sigv4"
    // CapProfileChannelV2 indicates per-entry profile delivery via yolo-user-env.sh.
    CapProfileChannelV2 = "profile-channel-v2"
)

// CurrentCapabilities returns the capabilities provided by this build of yolo.
func CurrentCapabilities() []string {
    return []string{
        CapInternalFooter,
        CapWirebridgeSigV4,
        CapProfileChannelV2,
    }
}
```

### 2. Inferring Capabilities of Legacy Jails

For containers started before `YOLO_CAPABILITIES` was exported, infer from `YOLO_VERSION`:

```go
func CapabilitiesFromEnv(envLines []string) []string {
    if raw := envLineValue(envLines, "YOLO_CAPABILITIES"); raw != "" {
        return strings.Split(raw, ",")
    }
    // Fallback: infer based on baked YOLO_VERSION
    version, _ := runtime.BakedYoloVersionFromInspectEnv(envLines)
    return inferLegacyCapabilities(version)
}
```

Blocked on [OQ-SK2](attach-skew-and-contract-guardrails.md#OQ-SK2) — discrete tags vs monotonic epoch.

### 3. Pre-Attach Verification in `run.go`

```go
func (o *Options) verifyAttachCapabilities(envLines []string, staged stagedPacks) (missing []string, ok bool) {
    jailCaps := CapabilitiesFromEnv(envLines)
    reqCaps := staged.RequiredCapabilities()
    
    jailSet := make(map[string]bool)
    for _, c := range jailCaps {
        jailSet[c] = true
    }
    
    for _, req := range reqCaps {
        if !jailSet[req] {
            missing = append(missing, req)
        }
    }
    return missing, len(missing) == 0
}
```

Blocked on [OQ-SK1](attach-skew-and-contract-guardrails.md#OQ-SK1) — prompt vs refusal vs degradation.

---

## Traps & Invariants

1. **Never mutate `/opt/yolo-jail/bin` in place.**
   Generation safety (`flakebundle`) depends on immutable generations. A running jail's
   mount must not be overwritten or re-linked while PID 1 or other processes are running.
2. **TTY checks before prompt.**
   Do not call interactive prompts unless both stdin and stdout are verified TTYs:
   `o.IsTTYStdin() && o.IsTTYStdout()`. In CI or pipe mode, fall back to explicit refusal.
3. **Prism backwards compatibility.**
   If a pack specifies `requires_capabilities` and the target environment lacks it,
   prism must omit that configuration item gracefully rather than erroring or failing
   the entire surface render.
   Blocked on [OQ-SK3](attach-skew-and-contract-guardrails.md#OQ-SK3).
