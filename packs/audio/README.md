# `audio` — the official pack that ships the audio loophole

This is the **dogfood** for the 15th contribution kind
([`loophole-packaging.md`](../../docs/design/loophole-packaging.md) §7, OQ-LP11 — *"do
bundled loopholes become official packs? RULED YES"*). Its value is not the audio it adds;
it is that a real, embedded, selectable pack goes through the `loophole` kind's claim
enumeration, its approval gate, its name pre-flight and its inert report.

**What it ships:** one `loophole` contribution (`loopholes/audio` — both host sockets,
`/dev/snd`, and the ALSA→PipeWire routing fragment) and one `env` contribution
(`PULSE_SERVER`, `PIPEWIRE_REMOTE`).

**Select it, and switch it on. Neither is implied by the other:**

```jsonc
// ~/.config/yolo-jail/config.jsonc
{
  "packs": ["claude", "audio"],
  "loopholes": { "audio": { "enabled": true } }
}
```

## It REPLACED the bundled loophole. It used to sit beside it

Until 2026-08-18 this pack shipped only the ALSA half, under the name **`audio-alsa`**,
while `bundled_loopholes/audio` did the real work. Both of those facts were forced, and
both have since been undone by the things that forced them:

| It was… | Because | What changed |
|---|---|---|
| named `audio-alsa` | `audio` was a **reserved** loophole name — the bundled directory names *are* the reserved set, read off the same embed.FS the loader materializes — and `PackLoopholeNameConflicts` refuses a pack claiming one **fatally**, so every jail selecting the pack would have failed to start | deleting the bundled copy retired the reservation **in the same commit**, because it was *derived* from the directory rather than listed beside it. The pack took the plain name back, which matters: `loopholes.audio.enabled` is the key users write |
| ALSA-only | the pack-shipped subset refused a `host_bind_mounts[].host` that expands an environment variable, and every socket a real audio loophole needs is under `${XDG_RUNTIME_DIR}`. There was **no legal spelling** — the variable was refused, the literal was refused as absolute, and it is not under `$HOME` | **OQ-LP14 withdrew that rule** (2026-08-17). It admitted `~/.ssh` and blocked a pulse socket, which is a gate with its two cases inverted. What replaced it is not a narrower gate but total claim enumeration plus the origin approval |

**So "the subset cannot express the real audio loophole" is a retired finding, not a
current one.** It is worth knowing it existed: it is the measurement that killed the rule.

## Four things about the manifest that look wrong and are not

**1. The socket binds say `readonly: true` and are still bidirectional.** The subset
refuses `readonly: false`, and for a socket that costs nothing — measured twice in this
repo, a read-only bind of an AF_UNIX socket is fully connectable and read-write, because
the kernel's read-only check exempts inodes that are not REG/DIR/LNK (the well-known
`docker.sock:ro` result). Audio frames flow both ways exactly as before.

> **What it does cost is the CLAIM's class, and that is worth knowing before someone
> "fixes" it.** `packload.bindIsIPC` splits the read-write-host-IPC claim from the mount
> claim on `readonly: false` **or** a `.sock`/`.socket` basename — and these sockets are
> named `native` and `pipewire-0`. So they land in the **mount** class. Nothing is
> understated (that class's text carries *"an AF_UNIX SOCKET here is read-write host IPC
> regardless of `:ro`"* verbatim); the discriminator is just coarser than the design's
> *"a socket bind is its own claim class"* wanted. The precise fix is a **declared socket
> bit** in the manifest schema, which `bindIsIPC`'s own comment names.

**2. It declares `platforms: ["linux"]` instead of probing for the socket.** The bundled
manifest gated on `requires.file_exists: "${XDG_RUNTIME_DIR}/pulse/native"`. That did not
come across, for three reasons: `requires.file_exists` is one of the two fields the subset
*still* path-scopes (it emits no approval claim while its answer leaks through `yolo
loopholes list`, so widening it would leave an unclaimed probe with a readout); the two
fields answer different questions (`requires` = "install it", `platforms` = "nothing you
can do"); and what the probe bought is nearly free now that the loophole is off by
default. Ask for audio on a host with none and you get *"skipping bind mount, host source
missing"* per mount — which is the better answer than silence for someone who asked.

**3. The ALSA fragment goes to `/etc/alsa/conf.d/50-yolo-audio-alsa.conf`, not
`/etc/asound.conf`.** That destination was chosen to avoid a collision that no longer
exists (podman refuses two binds on one destination whose sources differ — measured:
`Error: /x.txt: duplicate mount destination` — so a jail with both would have **refused to
start**). The choice survives the collision because alsa-lib loads `/etc/alsa/conf.d`
*before* `/etc/asound.conf` (its own `alsa.conf` include list) and this is the spelling
measured working in this repo's jail with `sox`. Moving to the freed path would be an
unmeasured edit made for tidiness.

**4. `PULSE_SERVER`/`PIPEWIRE_REMOTE` are the pack's `env` contribution, so they are
UNCONDITIONAL.** `jail_env` is refused for a pack-shipped loophole, and the difference is
real: a loophole's `jail_env` applied only when the loophole was active; the `env` kind is
set on every launch that selects the pack. So selecting this pack on a machine with no
audio socket points `PULSE_SERVER` at a socket that is not there, and a libpulse client
fails the same way it would on a host with no daemon. That is §3.1's named cost and
OQ-LP5's trigger; the fix is the cross-kind collision pass, which is purely additive.

## Off by default

`default_enabled: false` ([`loophole-activation.md`](../../docs/design/loophole-activation.md)
R1/R4 — *"we don't give host access by default"*). Audio used to be on for everyone whose
host had a Pulse socket, which is host presence deciding activation.

Note the sibling `audio-alsa` shipped `default_enabled: **true**`, on an argument that was
sound for what it was — R4's subject is host access, and an ALSA config fragment the pack
itself ships reaches none. That argument does not survive the merge: this loophole binds
two host sockets and passes a device through.

## What gets wired up

| # | What | Container path | Covers |
|---|---|---|---|
| 1 | Pulse socket bind + `PULSE_SERVER` | `/run/pulse/native` | libpulse clients: sox, `ffmpeg -f pulse`, parec, Electron |
| 2 | Native PipeWire socket bind + `PIPEWIRE_REMOTE` | `/run/pipewire/pipewire-0` | pipewire-rs clients and the ALSA PipeWire shim |
| 3 | ALSA conf.d fragment | `/etc/alsa/conf.d/50-yolo-audio-alsa.conf` | anything that dlopens libasound and calls `snd_pcm_open("default")` |
| 4 | `/dev/snd` passthrough (`--device`) | `/dev/snd/*` | ALSA-seq MIDI (rtmidi, gomidi/rtmididrv), raw hardware ALSA, mixers |

Bridge 3 is the one most people do not know they need. ALSA's default config defines
`pcm.default` as the first hardware card (`hw:0,0`), and a jail has no `/dev/snd/*` until
bridge 4 lands them — so a libasound consumer that opens `default` without routing dies
with `cannot find card '0'`. Claude Code's voice mode trips exactly this path.

Bridge 4 exists because ALSA-seq has no userspace plugin layer — there is no `seq`
equivalent of `libasound_module_pcm_pipewire.so`, so rtmidi and gomidi open `/dev/snd/seq`
directly. `--device` (rather than a bind) is what makes the cgroup device-allow rules
permit reads and writes.

Measured with `sox` in this repo's jail image:

```console
$ # no routing at all
$ sox -n -t alsa default trim 0 0
ALSA lib confmisc.c:855:(parse_card) cannot find card '0'          # the bug

$ # with the conf.d fragment
$ sox -n -t alsa default trim 0 0
… Cannot open shared library /lib/alsa-lib/libasound_module_pcm_pipewire.so   # ROUTED
```

The second message is expected in a jail whose workspace has not pulled in `pipewire`; it
proves the routing was *reached*, which the unrouted case never gets to.

## What is observable where

`--device` is unobservable in this repo's own mandated verification environment:
`RuntimeArgsFor` skips device passthrough whenever the launcher is itself in a jail
(*"devices cannot nest under rootless podman"*), and nested-jail verification is the
mandated loop for `cmd/`/`internal/` changes. The declaration moved across from the
bundled manifest unchanged and its claim class is pinned by test — but a **change** to it
can only be proven on a real desktop.

| Observable in a nested jail | Needs a real (non-jail) host |
|---|---|
| discovery (`yolo loopholes list` shows `audio`, source `pack`) | `--device` passthrough of any kind |
| selection (the pack must be in `packs`; nothing is default-on) | the live sockets crossing into a fresh jail |
| the footprint claims (`yolo pack footprint` / `pack lint`) | audio actually reaching a speaker or microphone |
| the approval path (a **fetched** copy prompts; this embedded one carries yolo's authority) | |
| the `:ro` binds landing at their destinations | |
| the inert report on an unsupported platform/backend | |

On `darwin` the loophole reports itself inert by name via `platforms: ["linux"]`, and the
message says explicitly that **nothing is missing and nothing can be installed to fix it**
— the misattribution that field exists to prevent. On the `container` (Apple Container) and
`macos-user` backends it reports inert for the *backend* reason instead: backend beats
platform, because the actionable line there is "switch backends".

## Verifying

```console
$ yolo pack lint packs/audio          # claims + the strict manifest read
$ yolo pack footprint audio           # the same claims, from the embedded copy
$ yolo loopholes list                 # audio, source `pack`
$ ls -l /etc/alsa/conf.d/             # in a jail that selected AND enabled the pack
```
