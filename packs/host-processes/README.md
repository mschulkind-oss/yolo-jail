# `host-processes` — an allowlisted view of the host's process table

One `loophole` contribution and nothing else. The daemon (`internal/hostprocesses`) and the
in-jail client (`cmd/yolo-ps`) stay **baked into the yolo binaries** — an official pack may
name a baked client, so this conversion needed none of the pack-shipped-binary capability
([`broker-as-a-pack.md`](../../docs/design/broker-as-a-pack.md) §3.1).

It was `bundled_loopholes/host-processes/` until 2026-08-18, and it is the sprint's
**proving ground** (§12 of that doc): the conversion that exercises pack staging, the
pack-shipped subset, the name pre-flight, `publishes: "socket"` and `doctor_cmd` with none
of the broker's complexity — no relay, no CA, no intercept, no credential file.

**The subset accepted the manifest unchanged.** That was §12's change 5 and it is a
verification rather than an edit: `publishes: "socket"` was already declared,
`requires.command_on_path` is not one of the path-scoped fields, and there is no
`jail_env`, no `host_bind_mounts` and no `ca_cert` for the subset to refuse. The only edits
the move made were `default_enabled` and the file header.

## Three gates, and they are not ceremony

```jsonc
// ~/.config/yolo-jail/config.jsonc  — user scope
{
  "packs": ["claude", "host-processes"],                       // 1. installed
  "loopholes": { "host-processes": { "enabled": true } }       // 2. running
}
```

```jsonc
// <workspace>/yolo-jail.jsonc  — workspace scope
{
  "loopholes": {
    "host-processes": {
      "settings": { "visible": ["sway", "waykeeper"] }         // 3. what it may show
    }
  }
}
```

`OQ-A5` keeps all three deliberately
([`loophole-activation.md`](../../docs/design/loophole-activation.md) §1.2a). They answer
different questions — **is it installed**, **is it running**, **what may it show** — and
collapsing the first two would mean a non-empty `visible` list *silently starting a host
daemon*, which is the presence-activation that design deletes, wearing a different hat.

Note which scope each lives in. `packs` is user-scope by construction, `enabled` is honored
from either, and `settings.visible` declares `"scope": "workspace"` in the manifest — which
is what makes the allowlist per-project while the *installation* stays the human's
decision.

## What changed for an existing setup

**It goes dark on upgrade** unless you add it to `packs` and enable it. That is OQ-A2 —
*"a loophole you never listed behaves like an agent pack you never listed"* — and it is the
rule working rather than a regression. The population it actually affects is small and
precisely identified: someone who had written a non-empty allowlist, because until then the
daemon showed nothing anyway.

**The top-level `host_processes` key is gone**, and a config still carrying it is now
**refused** with a message naming `loopholes.host-processes.settings.visible`. It was
honored-with-a-warning through the step that moved the keys; deleting it silently would
have stranded exactly the people who had migrated nothing. See
[`RELEASE-NOTES.md`](../../docs/RELEASE-NOTES.md).

## Verifying

```console
$ yolo pack lint packs/host-processes   # claims + the strict manifest read
$ yolo pack footprint host-processes    # the same claims, from the embedded copy
$ yolo loopholes list                   # host-processes, source `pack`
$ yolo check                            # runs the loophole's own doctor_cmd
$ yolo-ps                               # in a jail that selected AND enabled it
```
