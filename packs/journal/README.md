# `journal` — the host's systemd journal, read from inside the jail

One `loophole` contribution and nothing else. The daemon (`internal/journald`) and the
in-jail client (`cmd/yolo-journalctl`) stay **baked into the yolo binaries** — an official
pack may name a baked client, so this conversion needed none of the pack-shipped-binary
capability ([`broker-as-a-pack.md`](../../docs/design/broker-as-a-pack.md) §3.1).

**It was not a bundled loophole. It was not a loophole at all.** Until 2026-08-18 the
bridge was a *builtin service*: a Go function the run pipeline called by hand, switched by
a **top-level `journal` key in yolo's own config schema**, with its name reserved in
`paths.BuiltinLoopholeNames` and refused nowhere. OQ-A6 ruled that channel closed —
*"a channel emptied of everything except the two things yolo happens to have compiled in
has been renamed, not emptied"* — and OQ-K4 supplied the settings half that made it
possible.

## Two gates

```jsonc
// ~/.config/yolo-jail/config.jsonc  — user scope
{
  "packs": ["claude", "journal"],                     // 1. installed
  "loopholes": { "journal": { "enabled": true } }     // 2. running
}
```

Then, in a jail:

```console
$ yolo-journalctl -u some.service -n 50
```

`enabled` is honored from **either** scope (R5), so a workspace `yolo-jail.jsonc` may
switch the bridge on for one project. What a workspace may **not** do is widen what it
reads — see below.

## The mode is one boolean now, and it is user-scope

```jsonc
// ~/.config/yolo-jail/config.jsonc  — USER SCOPE ONLY
"loopholes": { "journal": { "settings": { "full": true } } }
```

| old | new |
| :--- | :--- |
| `"journal": "off"` / absent / `false` | `"loopholes": {"journal": {"enabled": false}}` (or just don't select the pack) |
| `"journal": "user"` / `true` | `"loopholes": {"journal": {"enabled": true}}` |
| `"journal": "full"` | the above **plus** `"settings": {"full": true}`, **in the user config** |

Two things about that table are the point rather than a translation.

**`full` is `scope: "user"`.** `"journal": "full"` was an agent-settable host-journal
passthrough: a workspace `yolo-jail.jsonc` is a file the agent inside the jail can rewrite,
and nothing anywhere said the key was user-scope. OQ-K4 calls the scope *"the security half
of this ruling"*. The asymmetry with `host-processes`.`visible` — which kept
`scope: "workspace"` — is deliberate: that key had an established workspace behaviour worth
preserving, and this one had no scope rule at all.

**It is a boolean, not the old three-valued string.** The settings type set is closed
(`string`, `bool`, `int`, `string_list`) with no `enum`, so a `string` mode could carry any
word and core could not refuse one — while `ParseRequest`'s test is `mode == "user"`, so
every *other* spelling, including a typo, behaves as **full**. A config typo that silently
widens host access is the shape this sprint exists to delete.

## What changed under the hood

- **`publishes: "socket"`.** The pack-shipped subset requires it. The daemon binds a plain
  AF_UNIX socket and yolo runs the TLS front over it, so the endpoint file's mode, its key
  persistence, its constant-time token compare and its length cap are the framework's code.
  **Nothing jail-facing moved:** still `YOLO_SERVICE_JOURNAL_ENDPOINT`, still
  `/run/yolo-services/journal.endpoint`.
- **`--mode` and `--endpoint` are retired flags that refuse**, each naming its replacement.
  Neither falls back, because each silence would be wrong in a different direction:
  ignoring `--mode full` drops an escalation somebody asked for, and honoring `--endpoint`
  publishes a bearer-token regular *file* where the front expects a socket.
- **Linux-only by `platforms`, not by a probe.** `journalctl` missing on a Linux host is
  reported per request (exit 127, naming it) rather than making the loophole vanish from
  `yolo loopholes list` with no explanation.

## Verifying

```console
$ yolo pack lint packs/journal      # claims + the strict manifest read
$ yolo pack footprint journal       # the same claims, from the embedded copy
$ yolo loopholes list               # journal, source `pack`
$ yolo-journalctl -n 5              # in a jail that selected AND enabled it
```
