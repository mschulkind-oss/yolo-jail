# audio loophole (bundled)

Ships with the yolo-jail wheel. Bridges the user's audio stack into the jail along three axes — Pulse, native PipeWire, and ALSA — so microphone input and playback work for every common client type (Claude Code's `/voice`, sox, ffmpeg, pipewire-rs, and anything that dlopens libasound).

## Activation

Gated on `requires.file_exists: ${XDG_RUNTIME_DIR}/pulse/native`. The loophole is *present* in every install but only *active* on Linux hosts where PipeWire or classic PulseAudio exposes its user socket at the standard path. This is the default on:

- PipeWire with `pipewire-pulse` (Fedora 34+, Ubuntu 22.04+, Arch, NixOS with the `pipewire.pulse.enable` flag).
- Classic PulseAudio (older distros, user-service mode).

To explicitly disable — e.g. you want the jail silent — add to `yolo-jail.jsonc`:

```jsonc
{
  "loopholes": {
    "audio": { "enabled": false }
  }
}
```

## macOS

Deliberately unsupported. The macOS container runtimes (Apple Container, Podman Machine) run Linux through a hypervisor VM with no CoreAudio passthrough, so there's no equivalent socket to bind. The `requires.file_exists` gate keeps the loophole inactive on macOS with no error noise. If you need voice features with yolo-jail-style isolation on macOS, run Claude Code directly on the host (the shared-credentials loophole keeps your jails and host session in sync).

## What gets wired up

When active, four things land in the jail:

| # | What | Container path | Covers |
|---|---|---|---|
| 1 | Pulse socket bind-mount + `PULSE_SERVER` env | `/run/pulse/native` | libpulse clients: sox, ffmpeg `-f pulse`, parec, parecord, Electron, etc. |
| 2 | Native PipeWire socket bind-mount + `PIPEWIRE_REMOTE` env | `/run/pipewire/pipewire-0` | pipewire-rs clients and the ALSA PipeWire shim |
| 3 | `/etc/asound.conf` (shipped from the loophole dir) | `/etc/asound.conf` | libasound clients: anything that calls `snd_pcm_open("default")` |
| 4 | `/dev/snd` device passthrough (`--device`) | `/dev/snd/*` | ALSA-seq MIDI (rtmidi, gomidi/rtmididrv), raw hardware ALSA, mixers |

The third bridge is the one most people don't know they need. ALSA's default config defines `pcm.default` as the first hardware card (`hw:0,0`). The jail has no `/dev/snd/*` devices visible until bridge 4 lands them, so a libasound consumer that opens `default` *without* asound.conf routing would die with `cannot find card '0'`. Claude Code's voice mode trips this exact path: if a workspace's `yolo-jail.jsonc` pulls in `alsa-lib` (e.g. for a Go MIDI driver), Claude's runtime backend probe finds `libasound.so.2`, picks the ALSA path. The shipped `asound.conf` re-points `pcm.!default` / `ctl.!default` at the PipeWire shim so the call lands on the bridged socket instead of bare hardware.

The fourth bridge is needed because ALSA-seq has no userspace plugin layer — nothing equivalent to `libasound_module_pcm_pipewire.so` exists for `seq`. rtmidi / gomidi open `/dev/snd/seq` directly. Without device passthrough, MIDI input is dead in-jail. `--device /dev/snd` (rather than a bare `-v` bind mount) ensures the cgroup device-allow rules let the container actually read/write the nodes; the user's host-side audio-group membership flows through naturally via the ACL on `/dev/snd/*`.

Pure-PulseAudio hosts (no native PipeWire socket) still work: bridge 2 is skipped silently by `runtime_args_for`, and `sox` / Pulse clients route through bridge 1 as before. The `PIPEWIRE_REMOTE` env is still set; clients that try the native socket fail the same way they would on the host. Hosts with `snd` kernel module not loaded skip bridge 4 the same way.

## Verifying

```sh
# From inside a jail:
yolo loopholes status                # audio: active
env | grep -E "PULSE_SERVER|PIPEWIRE_REMOTE"
ls -l /etc/asound.conf               # ships from the loophole
ls /dev/snd/                         # bridge 4 — should list cards + seq
sox -d -n stat -v                    # libpulse path — prints mic level
pw-cat --record - </dev/null         # pipewire-rs path (if installed)
aconnect -l                          # ALSA-seq MIDI ports (if alsa-utils installed)
```

If `yolo loopholes status` reports audio as *inactive* on a Linux host that clearly has PipeWire running, confirm the socket path:

```sh
ls -l "${XDG_RUNTIME_DIR}/pulse/native"
```

Some minimal environments (headless servers, some SSH sessions) don't have `XDG_RUNTIME_DIR` set. Export it before running `yolo` if needed:

```sh
export XDG_RUNTIME_DIR="/run/user/$(id -u)"
```
