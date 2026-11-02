# `host-processes` — an allowlisted view of the host's process table

One `loophole` contribution and nothing else. The daemon (`internal/hostprocesses`) and the
in-jail client (`cmd/yolo-ps`) stay **baked into the yolo binaries** — an official pack may
name a baked client, so this conversion needed none of the pack-shipped-binary capability
([`pack-binaries.md`](../../docs/reference/pack-binaries.md#a-packs-own-tree-cannot-carry-an-executable)).

It was `bundled_loopholes/host-processes/` until 2026-08-18, and it is the sprint's
**proving ground** ([the loophole reference](../../docs/reference/loophole-system.md#host-processes-the-proving-ground)): the conversion that exercises pack staging, the
pack-shipped subset, the name pre-flight, `publishes: "socket"` and `doctor_cmd` with none
of the broker's complexity — no relay, no CA, no intercept, no credential file.

**The subset accepted the manifest unchanged.** That was [the proving ground](../../docs/reference/loophole-system.md#host-processes-the-proving-ground)'s change 5 and it is a
verification rather than an edit: `publishes: "socket"` was already declared,
`requires.command_on_path` is not one of the path-scoped fields, and there is no
`jail_env`, no `host_bind_mounts` and no `ca_cert` for the subset to refuse.

## Two gates: select the pack, grant the names

What `yolo-ps` may show is a **grant**, not a setting
([design §4.3](../../docs/design/loophole-access-approval.md#43-each-loopholes-access-and-its-approval)).
The loophole declares one grant kind, `view`, and it is **valued**: its values are the
process names `yolo-ps` may show. `yolo-ps --tree` also shows every process below a granted
one, with its command line, and a name that is not granted is invisible in every mode.

1. **Select the pack**, once, on your machine: `yolo pack add host-processes`. A selected
   loophole runs; there is no switch to turn it on.
2. **Allow `view` on request**, once, in your user config: `yolo allow host-processes
   on_request view`. A valued kind is never requestable by default and is never granted
   under `always`, because its value is the project's to name.
3. **The project asks for names**, in its own file, from inside the jail if you like:
   `yolo request host-processes view postgres redis-server`.
4. **The gate grants them.** Run `yolo config apply` on the host, in the workspace (or start
   the jail), and answer y to the line `+ host-processes: see postgres`.

The resulting config, in TOML:

```toml
# ~/.config/yolo-jail/config.toml — user scope
packs = ["claude", "host-processes"]

[allow.host-processes]
on_request = ["view"]
# fields = ["pid", "comm", "args"]   # optional: the ps -o columns, user scope only
```

```toml
# <workspace>/yolo-jail.toml — the project's request
[requests.host-processes]
view = ["postgres", "redis-server"]
```

An explicit `on_request` list **replaces** the kinds that are requestable by default; for
this loophole that changes nothing, since `view` is its only kind and is not one of them.

Until a name is granted, the daemon runs and refuses every request, exit 64, naming the
next step: the `yolo allow` line when your config does not allow `view` on request, the
`yolo request` line when it does and the project has not asked, and `yolo config apply`
when the request waits on the gate. The refusal reads the words the launch wrote into the
grant file (`see host processes`, `process names`).

**`fields` is user scope.** Which columns of the host's process table cross into the jail
is yours to say, not the agent's. It is frozen at launch: a change needs a restart.

**The grant is not frozen.** The daemon reads its grant file's `grants` block again at
every request, so `yolo config apply` reaches a running jail at its next `yolo-ps`. That
file lives in the loophole's private state dir, outside every mount, and only the launch
and `yolo config apply` write it, from an approved gate.

## What changed for an existing setup

**The `visible` setting and the `enabled` switch are gone.** `visible` was workspace scope,
so an agent could write the list of host processes it was shown; it became the `view`
grant's request, which only an accepted gate grants. A legacy JSONC config is read through
the legacy projection until it is migrated: a user `enabled: true` makes `view` requestable,
a workspace `enabled: true` is a request, and a `visible` list becomes the request's names.
The gate still asks before anything is shown.

**It goes dark on upgrade** unless you select the pack. That is [`OQ-A2`](../../docs/reference/loophole-system.md#oq-a2) —
*"a loophole you never listed behaves like an agent pack you never listed"* — and it is the
rule working rather than a regression.

**The top-level `host_processes` key is gone**, and a config still carrying it is
**refused** with a message naming its replacement. It was honored-with-a-warning through the
step that moved the keys; deleting it silently would have stranded exactly the people who
had migrated nothing. See the 0.9.x section of [`CHANGELOG.md`](../../CHANGELOG.md#09x).

## Verifying

```console
$ yolo pack lint packs/host-processes   # claims + the strict manifest read
$ yolo pack footprint host-processes    # the same claims, from the embedded copy
$ yolo loopholes list                   # host-processes, source `pack`
$ yolo check                            # runs the loophole's own doctor_cmd
$ yolo-ps                               # in a jail whose project holds a view grant
```
