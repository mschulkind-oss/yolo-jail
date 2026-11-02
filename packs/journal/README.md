# `journal` — the host's systemd journal, read from inside the jail

One `loophole` contribution and nothing else. The daemon (`internal/journald`) and the
in-jail client (`cmd/yolo-journalctl`) stay **baked into the yolo binaries** — an official
pack may name a baked client, so this conversion needed none of the pack-shipped-binary
capability ([`pack-binaries.md`](../../docs/reference/pack-binaries.md)).

**It was not a bundled loophole. It was not a loophole at all.** Until 2026-08-18 the
bridge was a *builtin service*: a Go function the run pipeline called by hand, switched by
a **top-level `journal` key in yolo's own config schema**, with its name reserved in
`paths.BuiltinLoopholeNames` and refused nowhere. OQ-A6 ruled that channel closed —
*"a channel emptied of everything except the two things yolo happens to have compiled in
has been renamed, not emptied"* — and OQ-K4 supplied the settings half that made it
possible.

## Selected, then granted

Selecting the pack runs the bridge; a **grant** decides what a request may read
([`loophole-access-approval.md`](../../docs/design/loophole-access-approval.md) §4.3). The
loophole declares two grant kinds:

| Kind | What a request reads | Requestable by default |
| :--- | :--- | :--- |
| `read` | your user journal: `--user` is forced onto every request | yes |
| `read_all` | the whole host journal: the request's arguments pass through unchanged (needs host journal read access) | no |

With `read_all` granted the bridge reads the whole journal; else with `read` granted, the
user journal; else it refuses, exit 64, naming `read` and the next step. The daemon reads
only this launch's grant file, never your config, and re-reads it at every request, so a
`yolo config apply` takes effect at the next one.

For your user journal in one project:

```toml
# ~/.config/yolo-jail/config.toml  — user scope
packs = ["claude", "journal"]
```

```toml
# yolo-jail.toml  — the project asks; `yolo request journal read` writes this
[requests.journal]
read = true
```

Then answer y at the launch's config-change gate, or run `yolo config apply` on the host in
the workspace. In a jail:

```console
$ yolo-journalctl -u some.service -n 50
```

For the whole host journal, the user allows it first, since `read_all` is not requestable by
default, and the project asks for it. An `on_request` list you write replaces the default, so
it names `read` too, or `read` stops being requestable:

```toml
# ~/.config/yolo-jail/config.toml
[allow.journal]
on_request = ["read", "read_all"]
```

```toml
# yolo-jail.toml
[requests.journal]
read_all = true
```

A kind under `always` instead of `on_request` (`[allow.journal] always = ["read"]`) is held
for every project without a request; each project's gate still asks once.

With no grant, `yolo-journalctl` refuses and names the step:

```console
$ yolo-journalctl -n 1
yolo-journalctl: this project holds no grant to read the host's journal, so nothing ran.
  The journal pack is selected and its service is running; it is waiting on a grant.
  Ask for this project:   yolo request journal read
  then the human runs, on the host, in this workspace:  yolo config apply   and answers y.
```

### From the old spellings

| old | now |
| :--- | :--- |
| `"journal": "user"` / `true`, or `"loopholes": {"journal": {"enabled": true}}` | `read` |
| `"journal": "full"`, or `"settings": {"full": true}` | `read_all` |

A legacy JSONC config is read through these rows while the old format is still accepted:
`enabled: true` in the user config holds `read` for every project, `settings.full: true`
there adds `read_all`, and `enabled: true` in a workspace file is a request for `read`. The
`full` setting is gone from the manifest. Reading the whole host journal was once settable
from a workspace `yolo-jail.jsonc`, a file the agent inside the jail can rewrite; it is now a
kind only your user config can make requestable and only a gate can grant.

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
$ yolo-journalctl -n 5              # in a jail that selected it and holds a grant
```
