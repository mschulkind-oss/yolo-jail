# Research: Docker → Podman Migration

**Date:** 2026-02-16  
**Status:** Complete

## Objective

Evaluate migrating yolo-jail from Docker to Podman, with the additional goal of enabling nested container execution inside the jail (podman-in-podman).

## Motivation

1. **Nested containers**: Develop jail-in-jail, debug nix image builds inside a jail
2. **Daemonless**: Podman doesn't require a root daemon
3. **Drop-in replacement**: CLI-compatible with Docker

## Environment

- **Host OS**: Arch Linux, kernel 6.18.6
- **Podman version**: 5.7.1
- **Nix image format**: `dockerTools.buildLayeredImage` → `.tar.gz`

## Test Results

### Image Loading
```
$ podman load < result
Loaded image: localhost/yolo-jail:latest
```
- **Result**: ✅ Works with compressed `.tar.gz` from nix `buildLayeredImage`
- **Note**: Older podman versions may need `compressLayers = false`. Current 5.7.1 handles it fine.
- **Image naming**: Podman prefixes `localhost/` but short name `yolo-jail:latest` works for all commands.

### Flag Compatibility
All flags used in `cli.py` tested:

| Flag | Docker | Podman | Status |
|------|--------|--------|--------|
| `--rm` | ✅ | ✅ | Works |
| `--init` | ✅ (tini) | ✅ (catatonit) | Works |
| `--shm-size=2g` | ✅ | ✅ | Works |
| `--tmpfs /tmp` | ✅ | ✅ | Works |
| `-u UID:GID` | ✅ | ✅ | Requires /etc/subuid + /etc/subgid |
| `--net=host` | ✅ | ✅ | Works |
| `-v host:container:ro` | ✅ | ✅ | Works |
| `-e VAR=val` | ✅ | ✅ | Works |
| `--name` | ✅ | ✅ | Works |
| `-i` / `-t` | ✅ | ✅ | Works |
| `--workdir` | ✅ | ✅ | Works |

### Container Operations
- `podman exec` into named containers: ✅
- `podman ps --filter "name=^/yolo-..."`: ✅ (both `^/name$` and `^name$` work)
- `podman ps --format "table {{.Names}}..."`: ✅
- `podman stop` / `podman rm`: ✅
- `podman load` from stdin pipe: ✅

### Prerequisites (Arch Linux)
- **`/etc/subuid` and `/etc/subgid`**: Must exist with user mapping (e.g., `matt:100000:65536`)
  - Without these, `-u UID:GID` fails with "potentially insufficient UIDs or GIDs"
  - Run `podman system migrate` after adding entries
- **NixOS**: Use `users.users.<name>.subUidRanges` / `subGidRanges` declaratively

## Nested Podman (Podman-in-Podman)

### Approaches

| Mode | Security | Nested Support | Notes |
|------|----------|---------------|-------|
| Rootless-in-rootless | High | Poor | `newuidmap` failures, capability limits |
| Privileged | Low | Full | `--privileged` + `--security-opt label=disable` |
| Socket mount | Medium | Good | Mount `/run/podman/podman.sock`, uses host daemon |
| Rootful outer, rootless inner | Medium | Moderate | Most practical for real nesting |

### Recommendation for yolo-jail
- **Socket mount** is simplest: mount host podman socket, inner jail uses `podman --remote`
- **Privileged mode** works but defeats security purpose
- **Best compromise**: Socket mount for most use cases (nix builds, self-development), with optional `--privileged` flag for true nesting needs
- Can be a per-workspace config option: `"nested_containers": "socket" | "privileged" | false`

## Nix Image Compatibility

- `dockerTools.buildLayeredImage` produces Docker v2.2 manifests — compatible with podman
- No need for `compressLayers = false` on podman 5.7.1+
- No need for `ociTools` — current format works
- `streamLayeredImage` (alternative) also works with podman

## Implementation Scope

### CLI Changes (src/cli.py)
All `docker` string literals need to become configurable:
- Line 43: `docker ps` (find_running_container)
- Line 180: `docker load` (auto_load_image)
- Lines 353-361: `docker exec` (container reuse)
- Lines 434-528: `docker run` (new container)
- Line 538: `docker ps` (ps command)

### Approach: Runtime Selection
```python
def _runtime() -> str:
    """Return container runtime: podman or docker."""
    # Check config, then env, then auto-detect
    ...
```

### Justfile
Lines 10, 19: Replace `docker` with runtime variable.

### flake.nix
No changes needed — `buildLayeredImage` output works for both.

## Open Questions

1. Should we support both Docker and Podman (auto-detect), or hard-switch to Podman?
2. For nested containers: socket mount vs privileged — should this be configurable per-workspace?
3. Should we include `podman` binary inside the nix jail image for nested use?
4. Docker socket (`/var/run/docker.sock`) is a known pattern — should we support podman socket mount too?
