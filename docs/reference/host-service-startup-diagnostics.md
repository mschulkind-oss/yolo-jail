---
status: accepted
stage: GRADUATED
verified: 2026-10-10
verified_commit: 3fe15ecbc
covers:
  - internal/loopholedecl/loopholedecl.go
  - internal/loopholes/settingscheck.go
  - internal/loopholes/settings.go
  - internal/hostservice/boundedcommand.go
  - internal/hostservice/startupreason.go
  - internal/hostservice/startupoutcome.go
  - internal/broker/brokerlifecycle.go
  - internal/cli/run/loopholesettings.go
  - internal/cli/run/loopholesruntime.go
  - internal/cli/run/launchcheck.go
  - internal/cli/run/packloopholes.go
  - internal/cli/run/keeper.go
  - internal/cli/run/hostdoorways.go
  - internal/cli/run/run.go
  - internal/cli/check/sections_loopholes.go
  - internal/awsauthdaemon/main.go
  - internal/awsauth/settings.go
  - packs/aws-auth/loopholes/aws-auth/manifest.jsonc
tags: [reference, diagnostics, host-services, loopholes, launch, credentials]
summary: "How a pack-declared host service's settings are validated before any shared state changes, how a cooperative daemon reports one attempt-specific refusal, and how that cause reaches every launch front door ahead of its socket and reachability symptoms."
---

# Host-service startup diagnostics: the settings preflight and the attempt-reason channel

**Status:** Verified 2026-10-10 against `3fe15ecbc`. UNMEASURED: native macOS behavior, real
rootless loopback forwarding and live AWS authentication have not been observed; Linux fixtures
and Darwin compilation are the evidence ([the QA record](../plans/host-service-startup-diagnostics-qa.md)).

A pack-declared host service can refuse its configuration for a good reason, and the launch then
has to say that reason, with the pack's remedy, before the missing socket and the unreachable
endpoint that follow from it. Two opt-in manifest contracts carry it. A **settings preflight**
validates the exact settings a launch is about to use before anything shared changes. An
**attempt-reason channel** lets a daemon that refuses at spawn hand back one bounded, typed refusal
tied to that one spawn. The resulting cause travels as a typed value through the terminal launch,
the keeper, the macos-user arm and `yolo host`.

| Component | Lives in |
| :--- | :--- |
| Manifest keys `host_daemon.settings_check` and `host_daemon.startup_reason` | `internal/loopholedecl` (`HostDaemon.SettingsCheck`, `HostDaemon.StartupReason`) |
| Validator runner and its private input file | `internal/loopholes` (`RunSettingsCheck`, `WritePrivateSettingsSnapshot`, `FrozenSettingsBytes`) |
| Bounded direct execution and text sanitizing | `internal/hostservice` (`RunBoundedCommand`, `SafeCommandText`) |
| Attempt-reason record, producer and reader | `internal/hostservice` (`StartupReason`, `NewStartupReasonChannel`, `WriteStartupReasonFromEnv`, `ReadStartupReasonOutcome`) |
| Typed startup outcomes | `internal/hostservice` (`StartupOutcome`, `StartupKind`, `StartupReasonReadOutcome`) |
| Host-wide singleton transaction | `internal/broker` (`EnsureSingleton`, `Deps.PublishSettings`, `Deps.PrepareLocked`) |
| Launch preflight, publication, per-jail spawn | `internal/cli/run` (`prepareLoopholeSettingsForStart`, `publishLoopholeSettings`, `startExternalService`, `startHostSingleton`) |
| The refusal value and its wording | `internal/cli/run` (`hostStartupRefusal`, carried in `olderDaemonRefusal`) |
| `yolo check` validation phase | `internal/cli/check` (the loophole section) |
| The AWS pack's validator and refusals | `internal/awsauthdaemon` (`SettingsCheck`, `safeSettingsRefusal`, `refuseStartup`), `internal/awsauth` (`ResolveRefusal`) |

**Reads with:** [`loophole-system.md`](loophole-system.md) (the manifest schema these keys
extend), [`loophole-transport.md`](loophole-transport.md#cooperative-host-daemon-startup-refusal)
(the channel's wire summary), [`agent-credentials.md`](agent-credentials.md) (the AWS service),
[`happy-path-principle.md`](happy-path-principle.md) and [`report-tiers.md`](report-tiers.md)
(why every stop names a next step and no disclosure is suppressible). The observed incident that
motivated this is in [the research record](../research/host-service-startup-diagnostics.md).

---

## Terms

**Settings preflight** *(coined by this work's design, 2026-10)*: an opt-in, pack-declared program
that answers whether one exact settings snapshot is acceptable, without starting the service or
doing external work. It is not a `doctor_cmd`: a doctor is a health check that may contact the
outside world (AWS's fetches and may mint credentials), while a preflight must be pure and cheap.

**Attempt reason** *(coined by the same design)*: one structured refusal a daemon sends over a
private channel inherited only by the process yolo just spawned. It is not stderr, not a log line,
not a readiness signal, and never evidence that a socket is healthy.

**Frozen settings bytes** *(coined here)*: the serialized, declaration-total, default-filled flat
settings map a launch resolves once from its merged config and then holds in memory. Every file
handed to a validator or a daemon is a fresh copy of these bytes. It is not the stable settings
file on disk, which is keyed by loophole name and shared by every launch on the machine.

**Singleton** and **per-jail daemon** are the two ownership models of
[`host-daemon-ownership.md`](../design/host-daemon-ownership.md): a host-wide daemon
(`scope: "host"`) behind per-jail fronts, and a child owned by one launch or its keeper.

## Invariants

1. **Validation precedes every shared lifecycle change.** A fresh launch runs the preflight before
   image or provisioning work, before publishing shared settings, before stopping or replacing a
   singleton, before spawning a per-jail child and before publishing a front. A refused snapshot
   therefore leaves a valid running singleton, its settings file and its existing fronts exactly as
   they were, and refuses only the new launch, which never attaches to the old settings instead.
2. **What is published is what was validated, and never the validator's file.** The validator is
   host code that could rewrite its own input, so its file is a throwaway copy; publication copies
   the frozen bytes yolo kept.
3. **Socket acceptance is the readiness authority.** Neither a closed reason channel nor a clean
   wrapper exit means ready. A daemonizing wrapper that exits zero while its child binds stays
   supported.
4. **No log and no stderr is evidence of a cause.** The cause comes from the validator's bounded
   output or the attempt's own channel. A shared service log cannot say which attempt wrote a line,
   so it is only ever named for manual reading.
5. **Core knows no service's settings.** The manifest interface is generic; no AWS key, profile or
   policy name appears in core. Only a pack's own program knows its policy.
6. **Severity is unchanged except for the new cause.** Host-loopback dispositions keep their rule
   ([`loopback-tls-reachability.md`](loopback-tls-reachability.md)), and no `YOLO_ALLOW_*` hatch
   exists for this. A preflight refusal and a cooperative refusal of class `configuration` are
   fatal to the new launch; every other cooperative class stays a warning.

## The settings preflight

### Manifest contract

`host_daemon.settings_check` is an optional argv vector. The loader requires it to be non-empty,
free of control characters and jail-side tokens, to name `{settings}`, to belong to a manifest that
declares at least one `settings` entry, and to sit beside a `host_daemon.cmd` that itself consumes
`{settings}`. `{loophole_dir}` is substituted at load; `{settings}` is substituted per run with the
private input path. The argv executes directly, never through a shell, with yolo's self-exec rule
applied, so a leading `yolo` runs the current binary. Its argv joins the pack's host-execution
claim beside `cmd` and `doctor_cmd`.

The validator's contract with yolo:

- it reads only its snapshot and performs no credential or API call, no daemon start, no state
  write and no other side effect;
- exit zero accepts the snapshot, and only that: it is not an approval of anything else;
- nonzero is a configuration refusal, explained by a diagnostic that states the cause and a
  configuration remedy, naming the user-scope file where scope matters;
- the diagnostic never carries setting values or secrets. A pack whose resolver errors can embed
  configured values projects typed failure kinds to fixed text instead of forwarding them.

The preferred diagnostic is one JSON object, `{"reason": …, "remedy": …}` (helper:
`loopholes.SettingsCheckMessage`). Anything else is taken as one opaque reason; empty output gets
a generic "refused without a diagnostic" reason and a generic remedy, never the log path.

### Bounds and outcomes

The runner caps wall time and captured output (see [Current values](#current-values)). Its
outcomes stay distinct: accepted, refused, timed out, and could not start. Only *refused* blames
the settings; a timeout or a start failure gets a remedy naming the pack's maintainer, because
nothing says the settings are wrong. Child stdout and stderr are socketpair ends installed as
files, so a descendant that keeps them open cannot hold the runner past the direct child's exit.

Before display, every message drops control characters, Unicode format characters (bidi
overrides, zero-width spaces) and the `[`/`]` of terminal markup, collapses to one line and is
capped per field. Settings bytes, argv, environment, credential responses and log contents are
never printed.

### Where it runs on a launch

The preflight runs once per fresh launch, from `prepareLoopholeSettingsForStart`, for exactly the
services that launch will start: selected and enabled, active on this host, admitted on this
backend, passing the pack-origin gate and the placement rule. Those gates are evaluated before any
pack code runs.

| Arm | When |
| :--- | :--- |
| Container launch (`Run`) | After the attach decision, before auto-capture, fork builds, image work or any publication |
| macos-user arm | Before capture and provisioning, unless the session joins an existing key |
| `yolo host` (`HostDoorways.Start`) | Before the exec disclosure and the doorway services' start |
| Keeper | Never re-runs it: the launch's frozen bytes and its prepared flag travel in the keeper plan, and a keeper missing a snapshot refuses rather than resolving again |

Because it runs earlier than the ordinary daemon-start disclosure, the launch first prints its own
non-suppressible line naming the source pack, the service and that the settings validator is about
to run on this machine; the full host-code disclosure still precedes the daemon itself.

Any non-accepted result refuses the launch: `Refusing to launch: host service '<name>' refused
startup (<class>): <reason>`, then `Remedy:` and a closing step. A `configuration` refusal closes
with "correct the settings, run `yolo check --no-build`, then retry"; any other class closes with
the retry alone, and a remedy already naming `yolo check --no-build` is not repeated. A settings
map that cannot even be resolved is refused the same way with class `settings-resolution`.

### Publication, by ownership model

After validation, `publishLoopholeSettings` publishes the frozen bytes without rereading config or
the validator's file:

- **Per-jail daemon with a preflight.** A new private `0600` file, written from the frozen bytes,
  replaces the stable path in this spawn's argv. A concurrent launch in another workspace cannot
  substitute bytes between validation and the daemon's read. The file is removed on spawn failure,
  and otherwise by the service's own teardown, after the daemon group is stopped.
- **Singleton with a preflight.** The bytes go to the stable path, but only inside
  `EnsureSingleton`, under the singleton lifecycle flock, in this order: locked preparation, then
  atomic publication, then any migration stop or settings-drift restart. A failed preparation
  publishes nothing; a failed publication leaves the current daemon untouched and refuses with
  class `settings-publication`. The singleton contract is that the daemon reads its settings
  before its socket becomes ready and never rereads the mutable stable path afterwards.
- **No preflight declared.** The legacy path: the bytes are written to the stable name-keyed file
  and the daemon receives that path, as before.

A later *valid* settings change keeps the existing host-wide semantics: it may restart the
singleton, which affects every front using it, as the launch already discloses.

### In `yolo check`

`yolo check` (including `--no-build`) resolves the snapshot from the current host config and runs
the same validator on its own private copy. It never reads, stages or overwrites a daemon settings
file, so it validates edited settings even when the staged file is absent or stale, and it never
changes a running daemon. It applies the same gates first: a service this backend would not start
is reported as not started and not validated, and an ungated module or a placement problem is a
warning with no validator run.

The validator is a phase of its own, before the existing doctor. On acceptance the service's
`doctor_cmd` runs exactly as it always has. On refusal, timeout or start failure, the check reports
that failure and skips **only that service's** doctor; every other service is still checked.

## The attempt-reason channel

### Producer contract

A host daemon opts in with `host_daemon.startup_reason: true`. For each spawn yolo creates a local
stream socketpair and a random attempt token, passes only the child end as fd 3, and names it in
three environment variables, never in argv. A daemon without the key receives no descriptor and no
variables.

The daemon may send **at most one** record: a 4-byte big-endian length, then a JSON object.

```json
{"version": 1, "service": "aws-auth", "attempt": "<token>", "class": "configuration",
 "reason": "No AWS credential mode is configured.", "remedy": "Set loopholes.aws-auth.settings.role_arn …"}
```

`class` is one of `configuration`, `dependency`, `permission` and `internal`. Text is single-line,
secret-free prose with no settings values, credentials, argv, environment or log excerpts. The Go
helpers give a daemon the whole lifecycle: `ProtectStartupReason` marks the fd close-on-exec before
the daemon starts any child of its own, `WriteStartupReasonFromEnv` writes the one record and then
closes the fd and unsets the variables, and `ReleaseStartupReason` closes and unsets without
writing once the daemon is serving.

The channel carries refusal only. It is not a general `ready`/`waiting` protocol.

### What the parent accepts

The parent reads only inside the service's existing readiness window, with the same absolute
deadline, so a reason can never extend it, and it never waits for EOF. A record counts only when
its frame length, JSON shape (unknown fields and trailing data refused), version, service name,
attempt token and class all match and its sanitized reason is non-empty. Anything else (late,
partial, overlong, malformed, misattributed or empty) is a typed channel fault, never a cause.

The parent closes its end on every outcome: readiness, an accepted record, process exit and
timeout. A descendant still holding the child end therefore cannot hold up cleanup. An accepted
record ends the readiness wait at once, as a failure, unless the service was already reachable:
readiness keeps its authority.

### What happens on a refusal

- **Class `configuration`** becomes the launch's `hostStartupRefusal` and refuses it, worded as
  above.
- **Any other class** stays a warning, printed as `refused startup: <reason> Fix: <remedy>` before
  the derived "cannot reach it" line, and the launch's existing severity rule decides the rest.
- **A refusing daemon this attempt spawned and that is still alive is stopped.** A per-jail child
  gets a short grace to exit first so its exit status is recorded, then its process group is
  killed. A singleton is signalled under the flock, because a refusing process left holding the
  pid file would block the next ensure after the user fixes the cause.

## Typed startup outcomes

Every start attempt yields a `StartupOutcome`: its owner (singleton or launch-owned child), the
last phase reached, a primary kind, what readiness was actually established, the process state
and the reason-channel read. The kinds keep the failures the user must tell apart separate:
preflight refusal, validator timeout or start failure, daemon start failure, cooperative refusal,
exit before readiness, readiness timeout while alive, channel fault and transport failure, among
others; the closed set is in `internal/hostservice`. A cooperative refusal is evidence about that
spawn only.

The outcome is launch-local and not serialized. What crosses each launch boundary is the refusal:

| Front door | How the cause reaches the user |
| :--- | :--- |
| Terminal launch | `startPlannedLoopholes` returns it as `olderDaemonRefusal.startup` ahead of the post-bind launch checks, which could not recover a pre-bind cause |
| Keeper | The keeper's output is a framed stream; it prints the refusal's markup through it and unwinds only what this launch owns |
| macos-user arm | The same keeper path, headed "Refusing the macos-user launch"; the preflight refusal returns before the keeper starts |
| `yolo host` | `HostDoorways.Start` returns the refusal, opens no doorway, and words it for the host |

An unwind after a refusal stops only resources the refused launch owns. It never stops a shared
singleton after a failed preflight.

## AWS permission-mode presentation

The `aws-auth` pack is the first producer of both contracts. Its manifest declares
`settings_check` as `yolo internal daemon aws-auth --settings-check --settings {settings}` and
`startup_reason: true`. The validator (`awsauthdaemon.SettingsCheck`) runs the settings resolver
and stops there: it runs no `aws`, detects no SSO form, fetches and mints nothing. A resolver
refusal carries a typed kind (`awsauth.ResolveRefusal`), and `safeSettingsRefusal` projects each
kind to fixed text naming the missing or conflicting key and the user-scope config file. The raw
resolver error, which can contain the configured profile or policy keys, is never forwarded. At
spawn, the daemon's own `prepare` sends the same projections over the attempt channel as class
`configuration`, and a missing AWS CLI as class `dependency`.

Using the configured permission set as-is (`unnarrowed: true`) is a valid, intentional choice, not
unrestricted AWS access, so a launch prints no routine notice for it: the setting declares no
`disclose` sentence. It stays an explicit user-scope opt-in, default false; conflicting settings are
refused; real errors and the pack's read/exec trust disclosures are unchanged.

## Not covered

- No readiness-success or `waiting` lines, and no longer readiness budget. The keychain design's
  long-unlock wait ([KC-D13](../design/keychain-from-a-jail.md#KC-D13)) is separate, unbuilt work
  that this failure-only channel does not satisfy.
- A singleton still holds one set of settings for the whole machine; there are no per-jail
  profiles behind it.
- Loopback reachability dispositions are unchanged, workspace-scope AWS settings stay refused, and
  the doctor and launch-check contracts are not replaced.
- Tests never run an agent CLI or an AWS API; the AWS fixtures use sentinel profile and policy
  values and assert they never reach output.

## Why it's this way

> [!WARNING]
> **Do not use `doctor_cmd` as the preflight.** A doctor may fetch or mint credentials; a launch
> preflight and `yolo check`'s validation phase must be pure. The two stay separate phases.

> [!WARNING]
> **Do not publish from the validator's input file, or validate the stable settings path.** The
> validator is host code that could rewrite its input, and the stable path is shared by every
> launch on the machine: either breaks "what was validated is what runs".

> [!WARNING]
> **Do not move singleton publication outside the flock, or before preparation.** Published early,
> an invalid or concurrent snapshot can kill a good daemon or be swapped by another launch; published
> before a preparation that then fails, it leaves a file naming settings no daemon runs.

> [!WARNING]
> **Do not infer a cause from a log, stderr or a missing endpoint.** A shared log cannot attribute a
> line to an attempt; a planted contradictory log entry must never change the result.

> [!WARNING]
> **Do not present a service bypass as the remedy.** The fix for a refused configuration is the
> configuration; `YOLO_ALLOW_UNREACHABLE_SERVICES` is not offered as one.

> [!WARNING]
> **Do not reintroduce a routine `unnarrowed` notice** (owner ruling, 2026-10-07, superseding the
> older disclosure requirement in [the SSO design's ledger](../design/sso-backed-bedrock.md#13-decision-ledger)),
> and do not add an AWS-name branch or a new severity feature in core to express it.

## Current values

Verified at `3fe15ecbc`. The prose above explains what each of these is for; this table is the only
place the numbers themselves are stated.

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| Validator wall time | 2s | `hostservice.SettingsCheckTimeout` |
| Validator captured output (stdout + stderr) | 4 KiB | `hostservice.SettingsCheckOutputMax` |
| Rendered reason / remedy field cap | 600 runes | `hostservice` (`startupReasonTextMax`) |
| Attempt-reason record cap | 4096 bytes | `hostservice.StartupReasonMaxBytes` |
| Attempt-reason record version | 1 | `hostservice.WriteStartupReasonFromEnv` |
| Attempt token | 24 random bytes, hex | `hostservice.NewStartupReasonChannel` |
| Child fd | 3 | `startExternalService`, the broker's spawn |
| Channel variables | `YOLO_HOST_SERVICE_REASON_FD`, `YOLO_HOST_SERVICE_ATTEMPT`, `YOLO_HOST_SERVICE_NAME` | `hostservice.StartupReason*Env` |
| Per-jail readiness window | 5s | `run.serviceReadyTimeoutDefault` |
| Singleton readiness window | 5s | `broker.BrokerSpawnTimeout` |
| Per-jail refusing-child exit grace | 1s | `run.refusalExitGrace` |
| Private snapshot location | `<loophole state dir>/settings-<settings file name>-*`, mode `0600` | `loopholes.WritePrivateSettingsSnapshot` |
