# Troubleshooting

## Troubleshooting

Start with `yolo check` — it validates your entire setup on both platforms: runtime (podman/container), nix, config, image, running containers, GPU (Linux only), macOS VM backend (macOS only), and the Claude OAuth broker loophole.

```bash
yolo check                    # full check including nix build
yolo check --no-build         # fast — skip nix build
```

### Common Issues (both platforms)

**"Cannot find yolo-jail repo root"** — The CLI needs the source for nix image builds. A packaged install (Homebrew, release archive) ships a flake bundle beside the binary and resolves it automatically, and `just install` stages one for a from-source install. **The working directory is never consulted**, so standing in a checkout is not enough — name it with `YOLO_REPO_ROOT`:

```sh
YOLO_REPO_ROOT=~/code/yolo-jail yolo
```

Every launch prints the flake it resolved and what selected it, before the image build starts:

```
Flake source: /Users/you/.local/share/yolo-jail/flake-bundle (flake bundle staged by `just install`)
```

Set `YOLO_REPO_ROOT` in your shell profile if you always want a live checkout — building from a tree newer than the installed `yolo` is then refused with a message naming `just install`, rather than failing deep inside the boot.

> The old `repo_path` config key was retired (2026-07-23) — if it is still in your `~/.config/yolo-jail/config.jsonc`, `yolo` ignores it and warns; remove it.

**Image build fails**

- Check nix is installed with flakes: `nix --version`
- Ensure flakes are enabled in `~/.config/nix/nix.conf`:
  ```
  experimental-features = nix-command flakes
  ```
- On macOS: no builder is required for the default binary-cache build path, and an uncached build offloads automatically to a container on the running runtime — nothing to verify. (Only if you configured your *own* remote Linux builder as an escape hatch, verify it — `nix store info --store ssh-ng://nix-builder` should respond within a few seconds.)
- Run `yolo check` for detailed diagnostics

**Container won't start**

- Linux: check `podman --version`
- macOS: check that your runtime's VM/daemon is up:
  - Podman Machine: `podman machine list`
  - Apple Container: `container system status`
- Try replacing the jail: `yolo stop`, then launch again
- Check for leftover containers: `yolo ps`

**MCP server not working**

- Verify the preset is enabled in `mcp_presets`
- Check logs (same paths on Linux and macOS): `~/.copilot/logs/` (Copilot), `~/.claude/logs/` (Claude)
- Inside jail, view logs: `tail -100 ~/.copilot/logs/$(ls -1t ~/.copilot/logs | head -1)`

**LSP not responding**

- LSP servers are spawned on-demand, not as background services
- Ensure the language server binary is installed (`mise ls`)
- TypeScript LSP requires `tsconfig.json` or `jsconfig.json` in the workspace root

**Tools missing after restart**

- `eval "$(mise hook-env -s bash)"` to refresh PATH
- Or restart the jail: `yolo stop`, then launch again

**Permission errors on files**


- Linux + Podman: Rootless UID mapping handles ownership automatically
- macOS (any runtime): File ownership is mediated by the VM's virtiofs layer; files inside `/workspace` appear as the jail user and on the host appear as you
- If persistent, check `ls -la ~/.local/share/yolo-jail/home/`

**Claude keeps logging out across jails**

- Full triage walkthrough: [docs/research/claude-token-logouts.md](https://github.com/mschulkind-oss/yolo-jail/blob/main/docs/research/claude-token-logouts.md). It maps each `yolo doctor` symptom to a fix.
- Background: Anthropic rotates refresh tokens single-use, so multiple jails refreshing simultaneously race each other. The `claude-oauth-broker` loophole serializes refreshes behind an `flock` on the host so jails can't race — eliminating the class entirely. **It is a contribution of the official `claude` pack** (since 2026-08-19) and on by default there, so **selecting `"packs": ["claude"]` is what installs it** — it is no longer keyed on `claude` merely being on your PATH, and there is no bundled channel any more.
- Run `yolo check` and look at the Loopholes section for broker health. Common recoveries: `yolo internal daemon claude-oauth-broker --init-ca` if certs are missing, then restart your jail.

### Linux-Specific Issues

**NVIDIA GPU not visible in jail**

- Check `nvidia-smi` on the host works
- Verify NVIDIA Container Toolkit: `nvidia-ctk --version`
- For Podman, ensure the CDI spec exists: `/etc/cdi/nvidia.yaml`
- See [GPU Passthrough](devices-and-gpus.md#gpu-passthrough-nvidia) for the full setup

**Podman rootless permission denied on `/dev/dri` or devices**

- Some device passthrough paths need `--cap-add` which Podman rootless may restrict
- Run Podman rootful (`sudo podman`) for these workloads

### macOS-Specific Issues

**Podman Machine won't start on headless Mac (EC2, CI)**

- Apple's Hypervisor.framework may require a GUI session

- Set `export YOLO_RUNTIME=container` for Apple Container (or drop `YOLO_RUNTIME` — auto-detect will pick it up)

**Nix build hangs or times out**

- Check `nix store info` responds within 2 seconds
- If it hangs, kill determinate-nixd and use the vanilla daemon:
  ```bash
  sudo pkill determinate-nixd
  sudo /nix/var/nix/profiles/default/bin/nix-daemon &
  ```
- If you configured your own remote Linux builder as an escape hatch, verify it: `nix store info --store ssh-ng://nix-builder` (see [macOS guide](macos.md)). The default uncached-build path needs no manual builder — it offloads to a container on the running runtime.

**Port forwarding not working**

- Podman on macOS: YOLO Jail uses a TCP gateway (`host.containers.internal`) instead of Unix sockets because virtiofs rejects sockets. This is automatic.
- Apple Container: uses native `--publish-socket` — no TCP gateway needed. ⚠ **Measured 2026-09-16: this path breaks the launch**, because the socket yolo hands it already exists and because AC's direction is host→container. See the `network.forward_host_ports` row in [Settings per setup](../reference/settings-per-setup.md#resources-devices-and-networking).
- Ensure `socat` is in the container (it's in the default image)

**Apple Container: "virtual machine failed to start"**

- VZ.framework caps how many bind mounts a guest can take. YOLO Jail consolidates the workspace state into a single `/home/agent` mount to stay well under it, but many custom `mounts` entries can still reach it. The cap is real and yolo does not know its value: the issue that reported this named a specific number, nothing in the codebase measures or asserts one, so the number is deliberately not repeated here.
- Try `YOLO_RUNTIME=podman` to sidestep the limit.

**Apple Container: image load fails**

- Apple Container requires OCI-format images. YOLO Jail converts via `skopeo` first (no daemon needed), or `podman` as fallback.
- If you don't have `skopeo` installed, install it: `brew install skopeo`.

**`/tmp` bind mounts fail**

- macOS `/tmp` → `/private/tmp` is a symlink. The CLI resolves this automatically when it builds a mount path. (This line named a `cli.py` until 2026-09-16; nothing in yolo is Python any more.)

See [What works in each setup](../reference/settings-per-setup.md#what-works-in-each-setup) for the per-setup support reference, and [macOS guide](macos.md) for the full macOS-specific setup and the per-backend explanations behind those cells.
