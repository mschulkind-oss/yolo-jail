---
title: "Implementation Sketch: Disk I/O Priority and Cgroup Weight"
date: 2026-09-27
status: draft
tags: [sketch, plan, io, resources, cgroups, implementation]
summary: "Companion implementation sketch for docs/design/io-priority.md: code layout, schema changes, syscall wiring, and test matrix."
vantage:
  status-chip: true
---

# Implementation Sketch: Disk I/O Priority and Cgroup Weight

**Status:** SKETCH, 2026-09-27 — incomplete, and unstable while questions are open.

> [!IMPORTANT]
> This is a companion implementation sketch for
> [`io-priority.md`](io-priority.md). It holds concrete
> file locations, schema definitions, and syscall details. Precedence rule:
> [`io-priority.md`](io-priority.md) wins on all architectural and behavioral decisions.

---

## 1. Codebase Map and File Locations

| File | Change | Purpose |
| :--- | :--- | :--- |
| `internal/config/validate.go` | `validateResources` | Validate `resources.io` schema (priority string, weight range 1..10000). |
| `internal/cli/run/backendcaps.go` | `appliedResourceLimits` | Parse `io` configuration, probe cgroup delegation, populate runtime limits. |
| `internal/cli/run/assemble_parts.go` | `resourceArgs` | Append `--cgroup-conf io.weight=<N>` to Podman arguments when delegated. |
| `internal/entrypoint/boot.go` | `Main` / `execBash` | Call `applyIOPriority()` on Linux container initialization. |
| `internal/entrypoint/ioprio_linux.go` | New file | Pure Go implementation of `SYS_IOPRIO_SET` for Linux architectures. |
| `internal/entrypoint/ioprio_darwin.go` | New file | Darwin stub / `IOPOL_THROTTLE` integration for macOS. |
| `cmd/yolo-cglimit/main.go` | `--io-priority` | CLI flag to run one-off commands under explicit I/O scheduling classes. |
| `internal/cli/check/check_resources.go` | `checkStorageScheduler` | Diagnostic check reporting storage schedulers (BFQ vs Kyber/none) and cgroup `io` delegation. |

---

## 2. Configuration Schema & Types

In `internal/config`:

```go
type IOConfig struct {
    Priority string `json:"priority,omitempty"` // "idle", "low", "normal"
    Weight   int    `json:"weight,omitempty"`   // 1..10000
}

type ResourcesConfig struct {
    Memory    string    `json:"memory,omitempty"`
    CPUs      any       `json:"cpus,omitempty"`
    PIDsLimit any       `json:"pids_limit,omitempty"`
    IO        *IOConfig `json:"io,omitempty"`
}
```

Validation rules:
- `priority`: must be one of `"idle"`, `"low"`, `"normal"`. Blocked on [OQ-3](io-priority.md#OQ-3).
- `weight`: must be an integer between `1` and `10000`.

---

## 3. Syscall Details for `ioprio_set`

In `internal/entrypoint/ioprio_linux.go`:

```go
//go:build linux

package entrypoint

import (
    "syscall"
)

const (
    IOPRIO_WHO_PROCESS = 1
    IOPRIO_CLASS_RT    = 1
    IOPRIO_CLASS_BE    = 2
    IOPRIO_CLASS_IDLE  = 3

    IOPRIO_CLASS_SHIFT = 13
    IOPRIO_PRIO_MASK   = (1 << IOPRIO_CLASS_SHIFT) - 1
)

func ioprioValue(class, data int) int {
    return (class << IOPRIO_CLASS_SHIFT) | (data & IOPRIO_PRIO_MASK)
}

func applyIOPriority(priority string) error {
    var prioVal int
    switch priority {
    case "idle":
        prioVal = ioprioValue(IOPRIO_CLASS_IDLE, 0)
    case "low":
        prioVal = ioprioValue(IOPRIO_CLASS_BE, 7)
    case "normal", "":
        return nil
    default:
        return nil
    }

    _, _, errno := syscall.Syscall(
        syscall.SYS_IOPRIO_SET,
        uintptr(IOPRIO_WHO_PROCESS),
        uintptr(0), // 0 means current process
        uintptr(prioVal),
    )
    if errno != 0 {
        return errno
    }
    return nil
}
```

---

## 4. Cgroup Delegation Check

In `internal/cli/run/backendcaps.go`:

```go
// hasCgroupIODelegation probes whether the rootless user slice allows io control.
func hasCgroupIODelegation() bool {
    uid := os.Getuid()
    path := fmt.Sprintf("/sys/fs/cgroup/user.slice/user-%d.slice/cgroup.controllers", uid)
    data, err := os.ReadFile(path)
    if err != nil {
        return false
    }
    controllers := strings.Fields(string(data))
    for _, c := range controllers {
        if c == "io" {
            return true
        }
    }
    return false
}
```

Blocked on [OQ-2](io-priority.md#OQ-2) for behavior when `hasCgroupIODelegation()` returns false.

---

## 5. Test Matrix

1. **Unit Tests (`internal/config`):**
   - Test validation of `resources.io`: string shorthand (`"low"`, `"idle"`), structured object (`{"priority": "low", "weight": 20}`), out-of-range weights (`0`, `10001`), invalid priorities.
2. **Entrypoint Syscall Tests (`internal/entrypoint`):**
   - Test `applyIOPriority("low")` and `applyIOPriority("idle")` under Linux; verify via `SYS_IOPRIO_GET`.
3. **Argv Assembly Tests (`internal/cli/run`):**
   - Verify `--cgroup-conf io.weight=20` emitted when cgroup delegation is mocked true.
   - Verify flag is suppressed and warning emitted when cgroup delegation is mocked false.
4. **Integration Test (`integration/`):**
   - Spawn a test jail with `"resources": {"io": "low"}`. Inspect process I/O priority via `/proc/<pid>/io` or `ionice -p <pid>`. Verify child processes inherit the priority.
