---
title: "Programs update at the first invocation of a jail start, not once an hour"
date: 2026-11-23
status: in-review
tags: [launchers, updates, program-delivery, macos-user, evergreen]
summary: "Every in-jail launcher drops its hourly throttle. An unpinned program is checked once per jail start, at its first invocation; relaunching the jail is how a user picks up a new version."
stage: DESIGN
next: "Build slice 0 (the jail-start record) and slice 1 (the npm launcher)"
---

# Programs update at the first invocation of a jail start, not once an hour

**Status:** 2026-11-23. Nothing built. Code read at `d2a831cd8`; the macOS rows are UNMEASURED.

> **In short.** The launchers' `UPDATE_INTERVAL` bought nothing a per-jail-start rule does not buy better:
> an update check costs a network round trip, so it must not run on every subagent start, and "once since
> this jail started" bounds it without a clock. The boot writes one fresh jail-start record; a launcher
> checks when it has not yet checked under that record.

**Why it matters.** An hour-old stamp means a user who relaunches the jail right after a release still runs the
old version. The stamp also lives in the machine-wide `~/.cache` while the install it throttles is per
workspace, so another workspace's check can silence this one ([Appendix A](#appendix-a-evidence)).

**The shape.** A **jail-start record** (written by the boot), a per-program **check marker** keyed by it, and a
verdict table saying which outcomes keep the marker.

**Cost.** A jail left running for a week checks each program once. `yolo pack update` or a relaunch is the way to
get a newer version sooner.

**Start at [The mechanism](#the-mechanism).**

**Needs your ruling:** nothing.

**Reads with:** [`program-delivery.md`](program-delivery.md#35-the-second-axis-who-the-dependency-serves-amendment-2026-09-03)
(the evergreen ruling and the refusal rules this keeps), [`pi-extension-store-builds.md`](pi-extension-store-builds.md#XB-D14)
(the refresh's stamp scope), [`pi-extensions.md`](../reference/pi-extensions.md#17-the-pre-launch-refresh)
(the refresh's tiers).

---

## The ruling

The owner, 2026-11-23: *"I'm thinking we skip the hour. We drop the hourly throttle entirely. There doesn't
seem to be a good reason for it. It's not like these things update 100 times a day. We want the updates when
the updates come out as long as the check is not expensive."* On the offline case: *"What's the point of
launching a jail without good internet? … just because it should be able to run without network doesn't mean
that it has to always."* On the shape proposed for it: *"yes, only update at first jail launch. relaunch is
how you pick up changes."*

An update check is not cheap on every invocation. A launcher runs for every agent start, including every
subagent (a `pi` fan-out starts it dozens of times), and an npm check is a node start plus a registry round
trip before the exec. Hence the shape: once per jail start.

**Non-goals.** No change to `agent_updates` holds, to pinned programs (they compare their declaration offline and
are untouched), to the no-silent-fallback refusals, or to any bound (`UPDATE_TIMEOUT` and the others). No eager
check at boot: updating every program on every start is the shape [OQ-PD12a](program-delivery.md#decision-ledger) deleted.

## Vocabulary

**Jail start** *(coined here)*: one run of the boot table by the jail's own boot: a container's main process
booting, or one macos-user bootstrap. Not an attach: an exec into a running container runs the table again as a
session pass, and the boot table already marks which steps belong to the jail's own boot alone. On macos-user a
session is a jail start, since that backend has no attach.

**Jail-start record** *(coined here)*: the file `~/.local/state/yolo/jail-start`, holding one random id the
boot step writes whole, so a reader sees the old id or the new one.

**Check marker** *(coined here)*: an empty file `<bin>.<id>` under `~/.local/state/yolo/launch-checks/` (a
program's check), `<store>/.yolo-refresh/<bin>.stamp.<id>` (its pre-launch refresh), or `servers/<package>.<id>`
(an MCP server's refresh). Its existence means: this jail start has had that check, or a check is in flight.
Not a timestamp: no decision reads a clock.

## The mechanism

### The jail-start record

A new boot step, `write_jail_start`, in the boot step table both boots share, declared `notSessionPass` (a session
pass minting an id would make every attach a start). Both boots run it: the container's main process and each
macos-user bootstrap. A launcher reads the id with `$(<file)`, a bash builtin that works under `env -i` and in
bash 3.2, so no environment variable carries it.

The last writer wins, by design: two macos-user sessions of one home share one record, the
later start's id; the earlier session's launchers adopt it, so a start costs at most one extra check per program
and two sessions cannot make each other re-check forever.

A launcher that finds no record (an older boot, a failed write) checks nothing, runs the installed version, and
says so with its next step ([Disclosures](#disclosures)). A second writer minting its own would break one-writer
state.

### The check, as a claim

The launcher decides "is this program's check due" in one atomic step: after the `agent_updates` gate and the
pinned and probe branches, it **claims** the marker by creating `<bin>.<id>` with `set -o noclobber`, which opens
with `O_CREAT|O_EXCL` in the kernel. The creator checks. Any other invocation finds the file, skips the check,
and runs the installed version at once, so a fan-out pays for one check. That is the whole concurrency rule
within one jail, and it needs no lock: see [the mkdir finding](#the-mkdir-finding).

A winner that ends without a verdict **releases** the marker, so the next invocation can check. A verdict keeps it:

| Outcome of the claimed check | Marker | What the launcher says next |
| :--- | :--- | :--- |
| Already current, or updated | kept | today's line (`is already current`, `Updating a → b`) |
| Registry did not answer, or the check timed out | **kept** (the owner's ruling: no retry until the next start) | the installed version runs; the line ends with the next step |
| A newer version found and its install failed | **released** | the refusal, exit 75, unchanged ([PD-NF1](program-delivery.md#PD-NF1)); see [LU-D10](#decision-ledger) |
| `YOLO_USE_INSTALLED` covers the program | kept | today's line; the choice lasts this start |
| Install lock held by another jail | released | `another update is in progress — running the installed version` ([PD-NF5](program-delivery.md#PD-NF5)) |
| Ctrl-C at the act | kept | today's line ([PD-NF4](program-delivery.md#PD-NF4)) |
| Launcher ended by SIGTERM or SIGHUP mid-act | released, by the shield's cleanup | none |
| Launcher killed by SIGKILL mid-act | kept (accepted: no code runs) | none; a relaunch or `yolo pack update` checks |

A cold install (nothing installed) is not a check: it installs, and its success records the marker, because it
installed the latest. Install-only runs and version probes never claim.

**Garbage.** The claimer removes `<bin>.*` markers of other ids whose mtime is over 7 days old. That is garbage
collection and decides nothing.

### Where each consumer lands

| Consumer | Today | New rule |
| :--- | :--- | :--- |
| npm launcher `_update_due`, `UPDATE_INTERVAL` | stamp age over 3600 s | claim the check marker |
| native launcher `_update_due`, `UPDATE_INTERVAL` | same | same; its update verb and installer re-run are the act |
| pnpm launcher `RETRY_INTERVAL` | a failed install retried after 3600 s | a failed install is retried at the next start; the marker records the failure only, and the line names the relaunch and the `rm -f` that retries now |
| source (fork) launcher `UPDATE_INTERVAL` | the refresh's number only; it has no program update | constant deleted; it runs the pre-launch refresh rule |
| pre-launch refresh `_refresh_due` | stamp age over 3600 s | due when changed content is unseen, or no `<bin>.stamp.<id>` exists |
| failed-refresh marker (`<key>.failed`) | content due again after 3600 s | `<key>.failed.<id>`: due again at the next start; a success removes them all |
| MCP server refresh `ServerRefreshInterval` | stamp age over 3600 s | `servers/<package>.<id>`, kept on every outcome except a refusal; the constant is deleted |
| `UPDATE_TIMEOUT`, `UPDATE_GRACE`, `PROBE_TIMEOUT`, `STALE_LOCK`, `REFRESH_HEARTBEAT`, `ServerRefreshTimeout` | bounds and lock liveness | unchanged: none is a throttle |
| host `DefaultUpdateInterval`, `BranchRefreshInterval`, patch check | hourly, on the host | not changed here: [LU-D11](#decision-ledger) |

The refresh keeps its marker-at-the-end model and its lock: its "due" answer comes from the marker alone, and
its changed-content rule ([XB-D14](pi-extension-store-builds.md#XB-D14)'s due-on-change) still fires at once.
The refresh's markers carry the id in their names, so two homes sharing a store never overwrite one record.

State that already exists: old `*.stamp` files and `~/.cache/yolo-agent-stamps` are ignored and never deleted. The
first invocation after the first jail start with the new boot checks every program once.

### The mkdir finding

The launchers' install-prefix lock (`_take_lock`, twice in the shell templates) and the refresh lock
(`_take_refresh_lock`) use the `mkdir` command as a mutex. A recorded fact (2026-11-14) is that `mkdir` on CI's
ubuntu-26.04 runners comes from uutils coreutils, where it succeeds on a racing create, so that mutex is not
exclusive there, and GNU coreutils in the jail hides it. This design does not depend on it: the once-per-start
decision is the `O_EXCL` claim above (a shell builtin's own `open`, no `mkdir` process), and the server refresh is
Go, where `os.Mkdir` is the syscall. The locks keep guarding what they guarded, writers into one install prefix
from different jails. The weakness itself is a separate defect, recorded in
[Found, out of scope](#found-out-of-scope).

## Disclosures

Every stop names the next step ([happy-path-principle.md](../reference/happy-path-principle.md)). Changed lines:

- **Could not ask / timed out.** `claude: could not ask the npm registry for a newer version — running the installed 2.1.0. It asks again the next time this jail starts (yolo stop on the host, then launch), or now with: yolo pack update.`
- **No jail-start record.** `claude: this jail has no start record, so it checked nothing for updates — running the installed 2.1.0. Relaunch the jail (yolo stop on the host, then launch) to write it.`
- **pnpm failed install.** The existing "A run … 60 minutes from now retries it" becomes "Relaunching the jail retries it".
- A skipped check says nothing: silence is the common case on the second invocation.

## Open questions

None open. The draft's two questions were decided by the coordinator on 2026-11-23 as consequences of the owner's
rulings ([LU-D10](#decision-ledger), [LU-D11](#decision-ledger)):

- **A refused install retries on the next invocation.** The refusal releases the marker, because the registry
  answered (no dead-network stall), and keeping it would run the old version that the refusal rule exists to stop.
- **The host-side checks follow the ruling too, through other changes.** The pack branch refresh and the patched
  fork's upstream check are [`local-pack-clones.md`](local-pack-clones.md)'s. The host floor's evergreen poll
  (`hostfloor.DefaultUpdateInterval`, `internal/hostfloor/floor.go:205`) is a follow-up on the roadmap, because
  `yolo host` has no jail start and needs its own observable.

## Found, out of scope

**The `mkdir` mutex is not exclusive on uutils.** Evidence: the 2026-11-14 CI recording above. Reach: the
install-prefix lock in both shell templates and the refresh lock. Consequence: two launchers from different
jails sharing a prefix could both install into it. Candidate fix: an `O_EXCL` file or a symlink creation
(`ln -s`, atomic on every POSIX filesystem) in place of the directory, keeping the owner token and the stale
rule. File this as its own backlog item; it needs the owner's Mac for Apple Container's shared directories.

## Slices

Test-first, smallest first. Every test waits on events (a fake `npm` on `PATH` that logs each call, a jail-start
record the test writes, a named pipe the fake `npm view` blocks on), never on a sleep or an elapsed time, never
touches the network, and never starts an agent.

0. **The record.** Test: run the container boot table twice, and once as a session pass, and read the file:
   ids differ across boots, equal across the session pass; the macos-user target writes it too. Pins the table
   entry (delete it and the test fails); `TestEveryBootStepRunsOrSaysWhy` already forces its exclusions.
1. **The npm launcher.** Replace `TestUnpinnedNpmLauncherUpdatesPastItsStamp` with: one `npm view` per id across
   two invocations; a new id checks again; unreachable keeps the marker; a refusal releases it; a held lock
   releases it; a pinned program, a held `agent_updates`, a probe and install-only claim nothing; no record
   checks nothing and says its line; two launchers, the first blocked in `npm view` on the pipe, the second
   returns without calling npm. Pins `_update_due`'s call site.
2. **The native launcher.** The same table over its update verb and installer, replacing
   `TestNativeLauncherThrottlesOnTheStamp`.
3. **The server refresh.** Go test over the existing `View` and `run` seams: a package marker per id, a
   refusal leaving none, the constant gone. Replaces `TestServerRefreshSharesTheLaunchersThrottle` and
   `TestAFreshStampDoesNoWork`. Pins `serverRefreshDue`.
4. **The pre-launch refresh.** Due on a new id and on changed content, never otherwise; the failed marker per id.
   Replaces the throttle tests in `prelaunchrefresh_test.go`, `prelaunchrefreshdue_test.go` and
   `prelaunchrefreshtiming_test.go`.
5. **The pnpm launcher.** Replaces `pnpmthrottle_test.go`'s hour.
6. **The sweep.** Delete the source launcher's constant; correct the comments and docs that say "hourly" (the
   user guide's agent-updates section, the program-delivery rows this supersedes); run the integration tests that
   read the interval (`integration/macosusernpm_test.go`, `installmechanism_test.go`).
7. **Mac witnesses** (UNMEASURED until run, per `mac-handoff`): two macos-user sessions in one home share the
   later id and one extra check; `set -o noclobber` is exclusive under `/bin/bash` 3.2.

## Decision ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| LU-D1 | **Owner ruling.** The hourly throttle is dropped entirely; an unpinned program is checked once per jail start, at its first invocation, and relaunching is how a user picks up changes (quotes above). Supersedes the "at most once per `UPDATE_INTERVAL`" clause of [OQ-PD12](program-delivery.md#decision-ledger), the hourly stamp of [XB-D14](pi-extension-store-builds.md#XB-D14)'s scope sentence, and [XB-D26](pi-extension-store-builds.md#XB-D26)'s "older than `UPDATE_INTERVAL`" | 2026-11-23 | [The ruling](#the-ruling) | no |
| LU-D2 | **Owner ruling.** A failed or timed-out check runs the installed version and does not retry until the next start | 2026-11-23 | [The check](#the-check-as-a-claim) | no |
| LU-D3 | *Implementation decision, reversible.* A jail start is one run of the jail's own boot, never an attach; its observable is a record the boot step writes whole | 2026-11-23 | [The record](#the-jail-start-record) | no |
| LU-D4 | *Implementation decision, reversible.* The record is a file read with `$(<file)`, not an environment variable, so it survives `env -i` | 2026-11-23 | [The record](#the-jail-start-record) | no |
| LU-D5 | *Implementation decision, reversible.* The marker is created by an `O_EXCL` claim (`noclobber`) before the check; no new `mkdir` mutex | 2026-11-23 | [The mkdir finding](#the-mkdir-finding) | no |
| LU-D6 | *Implementation decision, reversible.* Absent record: check nothing and say so; the boot is the only writer | 2026-11-23 | [The record](#the-jail-start-record) | no |
| LU-D7 | *Implementation decision, reversible.* The pnpm failed-install retry follows the same per-start rule | 2026-11-23 | [Consumers](#where-each-consumer-lands) | no |
| LU-D8 | *Implementation decision, reversible.* The refresh keeps its lock and end-of-act marker; its markers are per id | 2026-11-23 | [Consumers](#where-each-consumer-lands) | no |
| LU-D9 | *Implementation decision, reversible.* Old stamps are ignored, never deleted | 2026-11-23 | [Consumers](#where-each-consumer-lands) | no |
| LU-D10 | *Coordinator decision.* A refused install (exit 75) releases the marker, so the next invocation retries | 2026-11-23 | [Open questions](#open-questions) | no |
| LU-D11 | *Coordinator decision.* This change covers the in-jail launchers only; pack refresh and the patch check are [`local-pack-clones.md`](./local-pack-clones.md)'s, and the host floor's poll is a roadmap follow-up under the same ruling | 2026-11-23 | [Open questions](#open-questions) | no |

## Appendix A: Evidence

Read at `d2a831cd8`; READ FROM CODE unless stated.

- The npm template's throttle: `UPDATE_INTERVAL=3600` at `internal/entrypoint/shims.go:1483`, `_update_due` at
  `:1641`, its call at `:1830`; the native template's at `:1981`, `:2474`, `:2564`; the pnpm `RETRY_INTERVAL=3600`
  at `:2659` and its test at `:2678`; the fork launcher's at `internal/entrypoint/forklauncher.go:276`.
- A refusal removes the stamp so the next invocation retries: `_yolo_update_refused` at
  `internal/entrypoint/updatebound.go:261`, called with `"$STAMP"` at `shims.go:1845` and `:2569`. Every other
  verdict touches it (`shims.go:1617`, `:1702`, `:1734`, `:1739`).
- The refresh: `_refresh_due` at `internal/entrypoint/prelaunchrefresh.go:298`, the failed marker's age test at
  `:250`, the stamp touched on every outcome at `:403`, the lock at `:329`.
- The server refresh: `ServerRefreshInterval` at `internal/entrypoint/serverrefresh.go:111`, `serverRefreshDue` at
  `:754`; a refusal touches no stamp (`refreshOne`, the final return), every other branch does.
- Scope mismatch: the stamp directory is `~/.cache/yolo-agent-stamps` (`shims.go:522`), and `~/.cache` is one
  directory for every workspace ([`jail-home.md`](../reference/jail-home.md)); the npm prefix is a per-workspace
  bind. One workspace's check therefore stamps for another's, which this design removes by scoping to the
  start.
- The boot table and its `notSessionPass` field: `internal/entrypoint/bootsteps.go`. The sessions' own
  directory under `/run/yolo` shows the jail's main process owns per-boot state.
- Host-side hourly throttles: `internal/hostfloor/floor.go:205`, `internal/packsrc/refresh.go:73`.
- NOT MEASURED: that `noclobber` opens with `O_EXCL` in bash 3.2 (slice 1 and slice 7 measure it); that
  `~/.local/state` is per workspace on both macos-user modes (the refresh log already assumes it).
