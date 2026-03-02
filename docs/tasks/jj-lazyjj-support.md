# Feature Request: jj (Jujutsu) and lazyjj Support

**Date**: 2026-03-02
**Requested by**: matt (via sysadmin session)

## Context

jj (Jujutsu) is now a primary VCS tool alongside git. lazyjj is a TUI for jj. Both need to work inside the yolo jail.

## Current State

- **jj** (`jujutsu`): Not in the jail base image. Available in nixpkgs as `pkgs.jujutsu` (v0.38.0).
- **lazyjj**: Not in the jail base image. Available in nixpkgs as `pkgs.lazyjj` (v0.6.1).
- **Pager handling**: Already correct — `PAGER=cat` is set in `entrypoint.py` (line 143), which jj respects as its fallback pager when `ui.pager` is not configured.
- **jj config**: No jj config exists inside the jail. Users would need to set up `~/.config/jj/config.toml` inside the persistent home (`~/.local/share/yolo-jail/home/`).

## Workaround (available now)

Users can add jj per-workspace via `yolo-jail.jsonc`:
```jsonc
{
  "packages": ["jujutsu", "lazyjj"]
}
```

This works but requires every workspace that uses jj to declare it, and incurs an image rebuild.

## Requested Changes

### 1. Add `jujutsu` to the base image (flake.nix)

jj is fundamental VCS tooling, same tier as git. It should be in the base image.

```nix
# In flake.nix contents list:
pkgs.jujutsu    # Jujutsu VCS (jj)
```

### 2. Consider adding `lazyjj` to the base image

lazyjj is a TUI tool — primarily useful for humans who `yolo` into an interactive shell, not for agents. Could go either way:
- **Add it**: Small binary, useful for interactive jail sessions.
- **Skip it**: Agents don't use TUIs. Users can add via `packages` when needed.

### 3. jj identity inside the jail

jj needs `user.name` and `user.email` configured. Options:
- **Manual**: User runs `jj config set --user user.name "..."` once inside the jail (persists in `~/.local/share/yolo-jail/home/.config/jj/config.toml`).
- **Entrypoint**: Have `entrypoint.py` generate a minimal jj config with a jail-specific identity (like git config is handled).

Currently the jail doesn't set up git identity either (users do `gh auth login` which handles it). Same pattern could apply to jj.

### 4. `JJ_PAGER` env var consideration

jj doesn't have a dedicated `JJ_PAGER` env var (unlike `GIT_PAGER` for git). It falls back to `$PAGER`. Since the jail already sets `PAGER=cat`, jj pager handling works out of the box. No changes needed here.

## Priority

- **P1**: Add `pkgs.jujutsu` to flake.nix base image
- **P3**: lazyjj — nice-to-have, not blocking
- **P3**: jj identity — manual setup is fine for now
