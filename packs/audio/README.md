# `audio` — the official pack that ships a loophole

This is the **dogfood** for the 15th contribution kind
([`loophole-packaging.md`](../../docs/design/loophole-packaging.md) §7, OQ-LP11 — *"do
bundled loopholes become official packs? RULED YES, and it ships with this change"*). Its
value is not the audio it adds; it is that a real, embedded, selectable pack now goes
through the `loophole` kind's claim enumeration, its approval gate, its name pre-flight and
its inert report.

**What it ships:** one `loophole` contribution (`loopholes/audio-alsa`, an ALSA→PipeWire
routing fragment) and one `env` contribution (`PULSE_SERVER`, `PIPEWIRE_REMOTE`).

**Select it like any pack** — nothing is on by default:

```jsonc
{ "packs": ["claude", "audio"] }
```

## It is ADDITIVE. The bundled `audio` loophole is untouched

`bundled_loopholes/audio` still exists, still activates by `requires`, and still does the
whole job: both sockets, `/etc/asound.conf`, `/dev/snd`. **If you do nothing, nothing
changes.** This pack adds a second, differently-named loophole beside it.

That is not a stylistic choice, and two measurements forced it:

| Question | Measurement | Consequence |
|---|---|---|
| Could the pack's loophole be named `audio`? | `PackLoopholeNameConflicts` refuses a pack claiming a reserved name, and the bundled directory names ARE the reserved set (read off the same embed.FS the loader uses). Probed: `loophole "audio" is reserved for the bundled audio loophole … Rename the loophole's directory.` | **No.** A pack named `audio` refuses the launch *fatally*. Removing the bundled copy in the same change would be the only way to take the name — and that trades a working shipped capability for a cosmetic win. The pack is `audio-alsa`. |
| Could it bind `/etc/asound.conf` like the bundled one does? | `podman run -v A:/x:ro -v B:/x:ro` → `Error: /x.txt: duplicate mount destination` | **No.** A jail with both would **refuse to start**. The pack binds `/etc/alsa/conf.d/50-yolo-audio-alsa.conf` instead, which alsa-lib loads *before* `/etc/asound.conf` (its own `alsa.conf` `@hooks` include list). |

Measured with `sox` (a real libasound client) in this repo's jail image:

```console
$ # no routing at all
$ sox -n -t alsa default trim 0 0
ALSA lib confmisc.c:855:(parse_card) cannot find card '0'          # the bug

$ # the pack's conf.d fragment only
$ sox -n -t alsa default trim 0 0
… Cannot open shared library /lib/alsa-lib/libasound_module_pcm_pipewire.so   # ROUTED

$ # BOTH the bundled /etc/asound.conf and the pack's fragment
$ sox -n -t alsa default trim 0 0
… Cannot open shared library /lib/alsa-lib/libasound_module_pcm_pipewire.so   # identical
```

The third case is the one that matters: `pcm.!default` defined in both files is **not** an
ALSA error — the later definition overrides, and both carry the same value. (The
`libasound_module_pcm_pipewire.so` line is expected in a jail whose workspace has not pulled
in `pipewire`; it proves the routing was *reached*, which is exactly what the unrouted case
never gets to.)

`/dev/snd` is deliberately **not** re-declared. The bundled loophole already passes it
through, and a duplicate `--device` is invisible on this repo's mandated verification loop
(device passthrough is skipped whenever the launcher is itself in a jail), so it could only
break on a maintainer's real desktop.

## THE FINDING: the pack-shipped subset cannot express the real audio loophole

This is the most important thing on this page, and it is a **finding about the subset**, not
a limitation of this pack.

`audio`'s reason to exist is two host sockets:

```jsonc
{ "host": "${XDG_RUNTIME_DIR}/pulse/native",  "container": "/run/pulse/native" }
{ "host": "${XDG_RUNTIME_DIR}/pipewire-0",    "container": "/run/pipewire/pipewire-0" }
```

The pack-shipped subset (`internal/loopholedecl/packshipped.go`, §3.1 requirement 1) refuses
a `host_bind_mounts[].host` that is absolute **or** that expands an environment variable,
allowing only `{loophole_dir}/…` and home-relative paths. The refusal's own reasoning is
sound — *"`${XDG_RUNTIME_DIR}` names an absolute host path one indirection later, so
admitting the variable while refusing the literal would be a rule about spelling"* — and its
consequence is absolute:

> **There is no spelling of `/run/user/<uid>/pulse/native` inside the subset's vocabulary.**
> `$XDG_RUNTIME_DIR` is refused, the literal is refused, and it is not under `$HOME`, so
> home-relative cannot reach it. The socket half of `audio` is **unexpressible for a pack**.

So the honest pack ships the ALSA half only. Pinned by test in both directions:
`TestAudioShapedManifestIsRefusedByTheSubset` asserts the real audio shape draws **six**
subset refusals (two `$VAR` hosts, two writable binds, one `jail_env`, and the `$VAR` in
`requires.file_exists`) — the sixth arrived after this README was written, when the
path-scope rule was extended to `requires.file_exists` because an unscoped value there is
a host-filesystem probe whose answer `yolo loopholes list` prints back. And
`TestBundledAudioIsOutsideThePackShippedSubset` (in `internal/loopholedecl`) asserts the
bundled manifests stay outside the subset.

**This is not an argument to weaken the rule.** Widening it to admit `${XDG_RUNTIME_DIR}`
would admit `${HOME}/.ssh` and `${XDG_RUNTIME_DIR}/../../etc` with it — the refusal is doing
real work. What the finding says is that the subset's vocabulary is **incomplete**, not too
permissive: a *runtime-dir socket* is a legitimate, common, non-home host path that a
third-party loophole will want, and the subset has no way to say it. The named fix in the
design's own §3.1 is *"a `host_daemon` that mediates the access"*, which for a PipeWire
socket means writing an audio proxy — a wildly disproportionate answer to "bind the socket
the user's own session already exposes". A declared, enumerated `runtime_socket` vocabulary
(claimed as host IPC, which the claim producer already does) would be the proportionate one.

## The second cost, which the design named in advance: the env is UNCONDITIONAL

`jail_env` is refused for a pack-shipped loophole, so `PULSE_SERVER` and `PIPEWIRE_REMOTE`
are declared with the `env` contribution kind. The difference is real and is §3.1's stated
cost (OQ-LP5's trigger):

- a loophole's `jail_env` applies **only when the loophole is active**;
- the `env` kind is **unconditional** — set on every launch that selects this pack.

So selecting this pack on a machine with no audio socket points `PULSE_SERVER` at a socket
that is not there. In practice that is what the bundled loophole's own manifest already
accepts for its PipeWire variable (*"Both are unconditional because the bind mounts above
use a stable container path"*), and a libpulse client fails the same way it would on a host
with no daemon. It is still a real behaviour difference from the bundled loophole, and the
fix is the cross-kind collision pass §3.1 describes as purely additive.

## What is observable where — R4, stated rather than claimed

The `--device` half of audio is unobservable in this repo's own mandated verification
environment: `RuntimeArgsFor` skips device passthrough whenever the launcher is itself in a
jail (*"devices cannot nest under rootless podman"*), and nested-jail verification is the
mandated loop for `cmd/`/`internal/` changes. This pack declares no devices for exactly that
reason, so nothing about it is host-only *by design* — but the underlying limit is worth
knowing before adding one.

| Observable in a nested jail | Needs a real (non-jail) host |
|---|---|
| discovery (`yolo loopholes list` shows `audio-alsa`, source `pack`) | `--device` passthrough of any kind |
| selection (the pack must be in `packs`; nothing is default-on) | the *bundled* loophole's live sockets crossing into a fresh jail |
| the footprint claims (`yolo pack footprint` / `pack lint`) | audio actually reaching a speaker or microphone |
| the approval path (a **fetched** copy prompts; this embedded one carries yolo's authority) | |
| the `:ro` bind landing at `/etc/alsa/conf.d/50-yolo-audio-alsa.conf` | |
| the inert report on an unsupported platform/backend | |
| teardown (the module dir is staged content; nothing persists) | |

On `darwin` the loophole reports itself inert by name, via its `platforms: ["linux"]`
declaration, and the message says explicitly that **nothing is missing and nothing can be
installed to fix it** — the misattribution that field exists to prevent. On the `container`
(Apple Container) and `macos-user` backends it reports inert for the *backend* reason
instead: backend beats platform, because the actionable line there is "switch backends".

## Verifying

```console
$ yolo pack lint packs/audio          # claims + the strict manifest read
$ yolo pack footprint audio           # the same claims, from the embedded copy
$ yolo loopholes list                 # audio (bundled) AND audio-alsa (pack)
$ ls -l /etc/alsa/conf.d/             # in a jail that selected the pack
```
