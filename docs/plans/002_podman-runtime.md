# RFC: Podman Runtime Support

**Date:** 2026-02-16  
**Status:** Approved  
**Research:** [2026-02-16_podman-migration.md](../research/2026-02-16_podman-migration.md)

## Summary

Add Podman as a supported container runtime alongside Docker. Users select runtime via `YOLO_RUNTIME` env var or `"runtime"` key in `yolo-jail.jsonc`. Nested container support is configurable per-workspace.

## Design

### Runtime Selection

Priority (highest wins):
1. `YOLO_RUNTIME=podman|docker` environment variable
2. `"runtime": "podman"` in workspace `yolo-jail.jsonc`
3. `"runtime": "docker"` in user `~/.config/yolo-jail/config.jsonc`
4. Auto-detect: prefer `podman` if available, else `docker`

Implementation:
```python
def _runtime(config: dict) -> str:
    env = os.environ.get("YOLO_RUNTIME")
    if env and env in ("podman", "docker"):
        return env
    cfg = config.get("runtime")
    if cfg and cfg in ("podman", "docker"):
        return cfg
    for rt in ("podman", "docker"):
        if shutil.which(rt):
            return rt
    raise SystemExit("No container runtime found. Install podman or docker.")
```

### CLI Changes

Replace all hardcoded `"docker"` strings with `runtime` variable:
- `find_running_container()`: `[runtime, "ps", ...]`
- `auto_load_image()`: `[runtime, "load"]`
- Container exec path: `[runtime, "exec", ...]`
- Container run path: `[runtime, "run", ...]`
- `ps()` command: `[runtime, "ps", ...]`
- All `os.execvp("docker", ...)` → `os.execvp(runtime, ...)`
- Error messages: `"docker command not found"` → `f"{runtime} command not found"`
- Status messages: `"Loading into Docker..."` → `f"Loading into {runtime}..."`

### Nested Containers (Phase 2)

New config key in `yolo-jail.jsonc`:
```jsonc
{
  "nested_containers": false  // or "socket" or "privileged"
}
```

| Value | Behavior |
|-------|----------|
| `false` (default) | No nested container support |
| `"socket"` | Mount host runtime socket into jail |
| `"privileged"` | Add `--privileged --security-opt label=disable` + include podman in image |

Deferred to a follow-up implementation.

### Justfile

Replace `docker` with variable or document manual override.

### flake.nix

No changes needed for basic migration.

## Open Questions

- [ ] For nested socket mode, should we auto-detect the socket path or make it configurable?
