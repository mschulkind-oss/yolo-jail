# `macos-log` — the Mac's unified log, read from inside the jail

One `loophole` contribution and nothing else. The host daemon (`yolo internal daemon
macos-log`, in `internal/journald`) runs Apple's `/usr/bin/log` as you, and the in-jail client
`yolo-log` (`cmd/yolo-log`) streams its output back. Both are baked into the yolo binaries, as
the [`journal`](../journal/README.md) bridge's are. macOS only.

## Turning it on

```jsonc
// ~/.config/yolo-jail/config.jsonc — user scope
{
  "packs": ["claude", "macos-log"],                    // 1. installed
  "loopholes": { "macos-log": { "enabled": true } }    // 2. running
}
```

Then, in a `macos-user` jail:

```console
$ yolo-log show --last 10m --predicate 'process == "myapp"'
$ yolo-log stream --level debug
```

`enabled` is honored from either scope, so a workspace `yolo-jail.jsonc` may switch the bridge
on. Only your user config may widen what it reads:

```jsonc
// ~/.config/yolo-jail/config.jsonc — USER SCOPE ONLY
"loopholes": { "macos-log": { "enabled": true, "settings": { "full": true } } }
```

## What each setting reads

| Setting | Verbs | Output | Entries |
| :--- | :--- | :--- | :--- |
| default | `show` and `stream` only | ndjson | only entries logged by processes running as the macos-user sandbox account (`_yolojail`) |
| `"full": true` | every `log` verb, every argument unchanged | whatever you ask for | every entry on the Mac you can read |

With no arguments `yolo-log` shows the last five minutes. A first argument that is a flag is a
`show` flag, so `yolo-log --last 1m` is `log show --last 1m`.

## Why it is a bridge (the measurement)

Until 2026-10-09 the top-level `macos_log` key (`off` / `user` / `full`) dialled a `yolo-log`
shell wrapper that ran `/usr/bin/log` **inside** the Seatbelt sandbox. macos-user CI run
37940733418 (job 113854140416) measured it, and the wrapper could never have worked:

- **The sandbox account cannot read the log at all.**
  `TestMacosUserMacosLogAsTheSandboxAccountMeasurement` ran `log show --last 1m` and a
  three-second `log stream` as `_yolojail` through `sudo -n`, with **no Seatbelt profile**, under
  the `user` profile and under the `off` one. All six runs read nothing (rc 1). The same store
  read as the runner, an admin, with no profile, read entries: that is the control the dial test
  checks before anything else, and it passed. So no profile rule could fix this. The account is
  the obstacle.
- **The `user` profile also refused the admin.** `TestMacosUserSeatbeltMacosLogDialDecidesTheLogRead`
  failed because the store read under the `user` profile, the one with no log deny, read nothing
  as the runner. Some other rule in the profile also gets in the way. Where the log is concerned,
  that is a second, independent blocker.
- **The evidence does not say why `log` refused the account.** Those probes discarded `log`'s
  stderr, so the run cannot tell "not permitted" from "read and found nothing". The likely
  answer is the documented one, that `log show` and `log stream` need an admin user, but it is
  not what this run measured. The store probe now prints `log`'s exit status and its own words
  when it reads nothing, so the next Mac run records the reason.

The Intel nightly's shard 5 (run 37931358062) failed the same dial test the same way.

## The ledger

Each entry below is an implementation decision taken under the owner's ruling of 2026-10-09
(*"if we need it, do that"*). Each is reversible.

- **ML-D1. A pack and loophole of its own, not an extension of `journal`.** The program, its
  argument language, its platform and its client all differ. `journal` names systemd, and one
  loophole that meant two different programs depending on the host would leave `yolo loopholes
  list` and the settings unable to say which one you get.
- **ML-D2. The `macos_log` key is retired, and refused.** "Every loophole yolo ships is a
  pack's" (AGENTS.md). The schema kept the key only because there was no daemon to declare it
  on. A config still carrying it is refused on the host, and only warned about inside a jail's
  snapshot. The refusal names the pack, the switch and the user-scope `full` setting, and says
  to delete `"off"`. This is `journal`'s conversion, for `journal`'s reason. The key turned a
  capability on, so ignoring it silently would leave a user with no thread back to it.
- **ML-D3. The mode is one boolean, `full`, declared `scope: "user"`.** `off` is `enabled:
  false`. A workspace file can be rewritten by the agent, so a workspace may switch the bridge on
  but may not widen it. A bool also cannot be misspelled into a wider mode.
- **ML-D4. The user scope is the sandbox account's processes, enforced on the output.** It is
  journalctl `--user`'s counterpart. The bridge forces `--style ndjson` and sends a line only if
  it is a JSON entry owned by the sandbox account. No predicate the client writes can widen what
  gets through, so the arguments only need to stay off host files and off writes. Ownership:
  - **The entry's own `userID`**, when it has one, under `show` and `stream` alike.
  - **Under `show`, nothing else.** `show` reads history (`--last 7d`), and a pid's owner today
    says nothing about who held it when the entry was logged, so a line without `userID` is
    dropped.
  - **Under `stream`, the live owner of the entry's `processID`** (`kern.proc.pid`, cached for
    one second), only when that process started no later than the entry's own `timestamp`. A
    process that started after the entry holds a recycled pid, and the entry belongs to the
    pid's earlier owner.
  - A line it cannot attribute (not JSON, no owner, a process already gone, a timestamp it cannot
    read) is dropped.
  - **Scope is the ACCOUNT, not one jail.** Every macos-user sandbox on a Mac runs as
    `_yolojail`, so the user scope shows every sandbox's entries, not just this one's. A
    per-jail scope would need the bridge to know this session's process tree, which the host
    daemon does not track.
  - **Unmeasured: whether a current macOS's ndjson carries `userID`, and how it spells
    `timestamp`.** Without `userID`, `yolo-log show` returns nothing in the user scope, and
    `yolo-log stream` while reproducing a problem is the way to read the log. With `full`,
    `show` works as usual. `TestMacosUserMacosLogBridgeScopesToTheSandbox` records which case a
    Mac is in.
  - **An abandoned stream stops.** The filter may send nothing for minutes, so the bridge
    doesn't wait for a failed write to notice the client is gone. It watches the connection, and
    when the client goes, it stops `log`.
- **ML-D5. User-scope arguments are an allowlist.** The verbs are `show` and `stream`. The flags
  are `--start`, `--end`, `--last`, `--predicate`, `--process`, `--style ndjson`, `--color`,
  `--timezone`, `--level`, `--type`, `--timeout`, `--info`, `--debug`, `--signpost`,
  `--backtrace`, `--loss`, `--source`, `--no-pager` and `--mach-continuous-time`. A positional
  argument is refused, because `log show <archive>` reads a host file. `--archive` is refused for
  the same reason, and so are `collect`, `config`, `erase`, `stats` and anything else. Every
  refusal ends by naming the `full` setting and where it goes.
- **ML-D6. The daemon lives in `internal/journald`.** The request line, the frames (1 = stdout,
  2 = stderr, 3 = exit), the spawn and the fronted socket are the journal bridge's, unchanged.
  Only the program, the policy and the stdout filter are new.
- **ML-D7. The client is a new baked binary, `yolo-log`, staged into the macos-user guest.**
  Its name is the one the retired wrapper had. It joins `shippedBinaries` and `guestBinaries`
  (`flake.nix`), `SHIPPED_BINARIES` and `GUEST_BINARIES` (`stage-source-bundle.sh`) and
  `macosuser.GuestClients`. A launch stages it whenever the session env carries
  `YOLO_SERVICE_MACOS_LOG_ENDPOINT`. The bootstrap's `retire_yolo_log` step deletes the old
  wrapper from `~/.local/bin`, which comes before the guest prefix on the sandbox PATH and lives
  in the workspace sidecar, so a leftover wrapper would hide the client. A file there that yolo
  did not write is kept and named.
- **ML-D8. The Seatbelt log deny is unconditional.** With nothing left to read the log inside
  the sandbox, the store and stream denies have no setting to make way for.
- **ML-D9. Darwin hosts only, and meant for macos-user.** The loophole declares
  `platforms: ["darwin"]`. A podman jail on a Mac can reach it too, but by default it shows
  only the sandbox account's processes, and a container runs none of those, so there only
  `full` returns anything. Apple Container cannot reach any host service. A container-shaped
  scope would be a separate decision, left until someone asks for it.

## Verifying

```console
$ yolo pack lint packs/macos-log
$ yolo loopholes list               # macos-log, source `pack`, darwin
$ yolo-log show --last 5m           # in a macos-user jail that selected AND enabled it
```

Needs a Mac: `TestMacosUserMacosLogBridgeScopesToTheSandbox` (the client staged, the user
scope returning the sandbox account's entry and not the runner's, `collect` refused, `full`
returning both). Also `TestMacosUserMacosLogAsTheSandboxAccountMeasurement`, which now records
`log`'s own words.
