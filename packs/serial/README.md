# Serial Pack (Host USB & Serial Bridge)

The `serial` pack enables safe, scoped access to host USB serial devices (ESP32, STM32, Arduino, Raspberry Pi Pico, etc.) from inside YOLO Jail via `yolo-serial`.

## Activation

What `yolo-serial` may list and open is a **grant**, not a setting
([design §4.3](../../docs/design/loophole-access-approval.md#43-each-loopholes-access-and-its-approval)).
The loophole declares one grant kind, `devices`, and it is **valued**: its values are the
device globs `yolo-serial` may list and open. A device no granted glob matches is refused.
There are no default globs.

1. **Select the pack**, once, on your machine: `yolo pack add serial`. A selected loophole
   runs; there is no switch to turn it on.
2. **Allow `devices` on request**, once, in your user config: `yolo allow serial on_request
   devices`. A valued kind is never requestable by default and is never granted under
   `always`, because its value is the project's to name.
3. **The project asks for devices**, in its own file, from inside the jail if you like:
   `yolo request serial devices '/dev/ttyUSB*' '/dev/ttyACM*'`.
4. **The gate grants them.** Run `yolo config apply` on the host, in the workspace (or start
   the jail), and answer y to the lines `+ serial: open /dev/ttyUSB*` and
   `+ serial: open /dev/ttyACM*`.

The resulting config, in TOML:

```toml
# ~/.config/yolo-jail/config.toml — user scope
packs = ["serial"]

[allow.serial]
on_request = ["devices"]
# default_baud = 115200   # optional, user scope only
```

```toml
# <workspace>/yolo-jail.toml — the project's request
[requests.serial]
devices = ["/dev/ttyUSB*", "/dev/ttyACM*"]
```

Until a glob is granted, the daemon runs and refuses every request, exit 64, naming the next
step: the `yolo allow` line when your config does not allow `devices` on request, the
`yolo request` line when it does and the project has not asked, and `yolo config apply` when
the request waits on the gate. A device outside the granted globs is refused the same way,
naming that device as the value to request.

**The grant is not frozen.** The daemon reads its grant file's `grants` block again at every
request, so `yolo config apply` reaches a running jail at its next `yolo-serial` call. That file
lives in the loophole's private state dir, outside every mount, and only the launch and
`yolo config apply` write it, from an approved gate.

## Settings

**`default_baud`** (integer, default `115200`) is the one setting, and it is **user scope**:
how the host's hardware is driven is yours to say, not the agent's. Set it under
`[allow.serial]` in your user config. It is frozen at launch: a change needs a restart.

### What changed for an existing setup

**The `allowed_devices` setting and the `enabled` switch are gone.** `allowed_devices` was
workspace scope, so an agent could write the list of host devices it may open, and it defaulted
to five globs (`/dev/ttyUSB*`, `/dev/ttyACM*`, `/dev/cu.usbserial*`, `/dev/cu.usbmodem*`,
`/dev/serial/by-id/*`). It became the `devices` grant's request, which only an accepted gate
grants. A legacy JSONC config is read through the legacy projection until it is migrated: a
user `enabled: true` makes `devices` requestable, a workspace `enabled: true` is a request, and
an `allowed_devices` list becomes the request's globs. A legacy config that switched serial on
and wrote no list reads as a request for those five default globs, which is what it ran on. The
gate still asks before anything is opened.

## Commands inside Jail

- `yolo-serial list` — list host serial ports and permissions
- `yolo-serial read /dev/ttyUSB0 --baud 115200 --timeout 2s` — read data from device
- `yolo-serial write /dev/ttyUSB0 "ping"` — send commands to device
- `yolo-serial monitor /dev/ttyUSB0 --baud 115200` — stream serial output live with auto-reconnect across device resets
