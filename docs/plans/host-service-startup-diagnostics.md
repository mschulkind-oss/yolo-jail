---
title: "Plan: carry host-service startup causes to the caller"
status: accepted
stage: DECIDED
next: "Audit the interrupted final-worker changes and complete unchecked caller, lifecycle and outcome cases before independent review and parent landing gates"
depends-on:
  - ../design/host-service-startup-diagnostics.md
  - ../research/host-service-startup-diagnostics.md
tags: [plan, host-services, diagnostics, launch]
summary: "A cold-builder map of the manifest, validation, singleton transaction, startup-reason and launcher-result seams for the accepted design."
vantage:
  status-chip: true
---

# Plan: carry host-service startup causes to the caller

**Status:** 2026-10-07. Candidate implementation only, outside main; development stopped for an environment restart. Bounded ordering/isolation and lifecycle/channel workers completed, but the contract/docs/final audit stopped without a final report. Candidate reference edits are drafts, not graduation. Previous incomplete implementations were rejected. No whole-feature acceptance or parent combined landing gates have completed. Re-audit the candidate and every unchecked task before independent review.

**Design:** [`host-service-startup-diagnostics.md`](../design/host-service-startup-diagnostics.md). **Precedence:** design wins on behavior; the tree wins on fact; this plan is advice and the first thing to correct if the tree moves.

## Map

| Path | Change |
| :--- | :--- |
| `internal/loopholedecl/loopholedecl.go` + tests | Add optional `host_daemon.settings_check` argv and `startup_reason` opt-in; enforce valid combinations and tokens. |
| `internal/loopholes/load.go`, `loopholes.go`, `resolver.go`, `runtime.go`, `placement.go` + tests | Carry resolved declarations; retain/rebind `{settings}` only on an opted-in per-jail spawn; resolve validator argv; include it in the same host-code placement refusal and preserve selected-pack origin gates. |
| `internal/hostservice/startupreason.go` + tests (new) | Bounded attempt record, producer helper, framing/validation and sanitizer; no EOF waits. |
| `internal/cli/run/loopholesettings.go`, `loopholesruntime.go`, `packloopholes.go`, `run.go` + focused tests | Build resolved immutable settings bytes; early validator disclosure/preflight; pass a unique immutable settings-file path to each opted-in per-jail daemon; clean it up only with owned-service teardown; singleton transaction; preserve start cause. |
| `internal/broker/brokerlifecycle.go`, `settingsrecord.go` + tests | Let the settings-check-enabled singleton publish its frozen bytes under the singleton flock after validation and before drift/stop; preserve outcome and spawn cause. |
| `internal/cli/check/sections_loopholes.go` + tests | Run the pure validator against current config bytes, not daemon settings paths or `doctor_cmd`. After successful validation keep the existing doctor behavior; if validation refuses or cannot complete, skip only that service's doctor and continue unrelated checks. |
| `internal/cli/run/launchcheck.go`, `keeper.go`, `run.go`, `hostdoorways.go`, native run caller + tests | Carry structured refusal across terminal, keeper framing, native and `yolo host` doorway boundaries; preserve unwind and severity. |
| `internal/awsauth/settings.go`, `internal/awsauthdaemon/main.go` + tests; `packs/aws-auth/loopholes/aws-auth/manifest.jsonc` | Add typed resolver refusal kinds and a resolver-only settings command; project to fixed safe cause/remedy text without forwarding raw errors or configured profile/policy values; opt in the manifest. |
| `docs/reference/loophole-system.md`, `docs/reference/loophole-transport.md`, `docs/reference/agent-credentials.md`, `CHANGELOG.md` | Graduate actual manifest/startup contract and current AWS behavior; add one concise Unreleased user-facing fix note. |

## Reuse before writing

- `loopholes.ResolveSettings` and `SettingsPayload` already produce the declaration-total, default-filled flat map. Serialize once, retain the bytes in yolo, and build each validator/daemon file as a distinct private copy from them; never use a file a pack executable was allowed to modify as the publication source.
- `internal/loopholes/load.go` currently resolves `{settings}` in `host_daemon.cmd` to `SettingsFileFor(name)` during manifest load. For an opted-in per-jail spawn, retain/rebind that placeholder at the runtime call site to the unique private file; do not mutate the shared resolved record, because concurrent launch/check callers may use it.
- `broker.EnsureSingleton` already owns the singleton flock, settings drift comparison, restart and spawn. Add a callback/transaction seam there rather than a second lock implementation; preserve the existing no-kill-before-lock rule.
- `loopholes.Set.ManifestHostDaemonSpecs`, `Set.RunDoctorChecks`, `PlacementProblems` and `startLoopholesMatching` hold the existing origin, activation, backend and placement decisions. Extend the same guarded records; do not add a second un-gated manifest walk.
- `launchcheck.go` owns the current 600-rune rendered line limit and safe rich-text escaping; `frameproto` and `internal/hostservice` provide existing bounded framed-protocol vocabulary, but neither currently carries this host-spawn refusal.
- `startLoopholesDisclosed`/`startPlannedLoopholes`, keeper's actual start-and-frame path, and `HostDoorways.Start` are the output boundaries. A broker-only test cannot establish any of them.

## Traps

- **Must:** distinguish current config bytes from the stable singleton settings file. That file is per loophole and shared across launches; validate and publish the same frozen snapshot.
- **Must:** for a settings-check-enabled per-jail service, never pass the name-keyed shared `SettingsFileFor(name)` path as this attempt's `{settings}`. `load.go` has already resolved that token when it builds the record; rebind only the spawn's argv to a unique immutable file from that launch's frozen bytes, without mutating shared record state. Keep it private and mode `0600` while the owned service may still read it. A second workspace must not be able to substitute bytes between validation and the daemon's read; legacy manifests retain the stable path.
- **Must:** early preflight runs before costly provisioning, but the ordinary daemon-start disclosure currently sits later in `run.go`. Print a separate non-suppressible pack/service/validator line immediately before early execution; retain the full existing disclosure before starting the daemon.
- **Must:** for a validator-enabled singleton, do not publish candidate settings before its preflight passes. Then publish under the existing flock before drift comparison; otherwise invalid config kills the good daemon or a concurrent launch swaps the file. The settings-check-enabled singleton must consume the file before readiness and must not lazily reread the mutable stable path afterward; the lock serializes only through the existing readiness check.
- **Must:** do not treat clean wrapper exit as daemon death when the socket is reachable; the direct spawn path intentionally supports daemonizing wrappers.
- **Must:** never block on inherited-channel EOF. A descendant can inherit the fd indefinitely; use the socket deadline and close the parent endpoint on every outcome.
- **Must:** post-bind `runLaunchChecks` cannot recover a pre-bind cause; carry a typed outcome across caller boundaries and keep the cause above the derivative reachability result.
- **Must:** `awsauthdaemon.SelfCheck` can fetch/mint, and `prepare` runs `aws --version`; the settings validator stops after the existing settings resolver, then maps a typed refusal to fixed safe pack-owned text. Never forward `Resolve`'s raw error into the validator output or attempt-reason channel: configured profile names and arbitrary policy keys can be embedded in it.
- **Must:** `yolo check` still runs the existing doctor for a service whose validator passes. A refusal, timeout or execution failure skips that service's doctor only; unrelated checks continue. Test the validation runner's purity separately from the check's later health-check phase.
- `internal/cli/run/packloopholes.go` has tests pinning source-shaped call paths. Add real fixture behavior/mutation coverage rather than replacing it with a weaker source regex.
- The AWS host daemon is `yolo internal daemon aws-auth`, part of `cmd/yolo`; current shipped packs declare no official `binaries` pin. No separate pack-binary re-pin is expected for this source change; still run `just check-pack-binaries`.

## Build order

1. Add the failing regression through the selected pack/real `startLoopholesDisclosed` caller: pre-bind configuration cause and user-scope remedy reach output, while a contradictory shared old log does not. Run the focused test and preserve the red output before changing production forwarding.
2. Add manifest declarations and generic validation record/resolution/placement tests. Add launch-preflight snapshot tests and `yolo check` tests for absent/stale staged settings. The pure validator runner has no doctor/credential/network action. A rejected or uncheckable candidate skips only its own service doctor; unrelated doctors continue, and a valid candidate still takes the existing doctor path.
3. For an opted-in per-jail daemon, create a private immutable settings file from the exact bytes that passed validation and pass its unique path in that spawn's `{settings}` argument. Race two workspace starts through the real per-jail caller: hold both daemon reads until both validators have run, then prove each daemon reads its own snapshot and the files are retained until service teardown. Assert a legacy manifest still gets the stable path.
4. Implement singleton publication under the existing lock and verify a live-valid singleton + invalid desired settings keeps current PID/settings/front usable while a new launch is refused. Add concurrent distinct snapshots to prove validation and publication use the same bytes.
5. Implement bounded attempt socketpair + producer helper; test valid reason, child-held fd, no record, process exit, wrong service/id, invalid version/class/frame, over-cap/noisy text and timeout cleanup.
6. Wire actual singleton and per-jail spawn results; test refusal, missing executable, crash, timeout, ready socket without channel, clean daemonizing wrapper and real front/transport faults.
7. Propagate through keeper framing, native Run and host doorway callers; run production-call mutation checks for each forwarding edge and retain existing loopback-severity/disclosure regressions.
8. Add the AWS resolver-only command and pack declaration. Use typed refusal kinds to project fixed safe cause/remedy text; never forward `Resolve`'s raw error. Test sentinel profile names and arbitrary policy keys/values are absent from validator output, attempt records and rendered diagnostics while missing narrowing and its user-scope remedy remain clear. Keep valid-candidate doctor behavior under a separate fixture; never call AWS APIs in tests. Run targeted race tests, then update references and changelog. `just check-pack-binaries` confirms the current no-pin set.

## Ships with

- **Unit:** manifest/schema/token admission; settings snapshots/defaults; per-jail private-path isolation and cleanup; 2-second timeout, 4-KiB cap, sanitizer and protocol attribution; AWS typed refusal projection and safe resolver command.
- **Run tests:** `internal/cli/run/loopholesettings_test.go`, `singletonsettings_test.go`, `hostservices_test.go`, `launchcheck_test.go`, `launchcheckrefusal_test.go`, `hostdoorwaypreamble_test.go`/disclosure tests, and `macosuserloopholes_test.go` as applicable. Each must assert the production caller's rendered result and cleanup, not only helper output. Add an interleaved two-workspace test through the real per-jail spawn caller and prove legacy stable-path behavior remains.
- **Check tests:** extend `internal/cli/check/sections_loopholes_test.go` for current desired config with no settings file, stale staged settings, safe failure detail and phase-specific execution: validator has no doctor/mint/network side effect; invalid/uncheckable candidate skips only its own doctor; valid candidate still invokes its doctor; unrelated services' doctors still run.
- **Keeper/native integration:** extend existing real framed keeper and native `Run` fixtures; if package tests cannot exercise their actual boundary, add one harmless local integration fixture under `integration/` (never an agent CLI or AWS API).
- **Tests that must stay green:** reachability requested/shared versus unsupported/unknown severity, host disclosure-before-exec, valid singleton settings drift, setting value redaction, and clean daemonizing wrapper readiness.
- **Pack:** update `packs/aws-auth/loopholes/aws-auth/manifest.jsonc`; no new config key, migration, daemon log format, runtime dependency or environment dial. If the manifest schema adds a key, update its docs and lint fixtures.
- **Docs:** update the three references in the map only after verifying the built behavior; keep the research record as evidence and the design/plan as the implementation contract. `CHANGELOG.md` gets one concise user-facing Unreleased entry because the cause/remedy is visible behavior.

## Don't

- Do not name `aws-auth`, AWS setting keys, profiles, IAM or narrowing in core schema/lifecycle logic; only its pack command knows that policy.
- Do not scrape host service logs, parse stderr, include settings/argv/env, forward raw resolver errors, infer a refusal from endpoint absence, attach a new launch to the old profile, kill a valid singleton on failed preflight, widen workspace settings, or present unreachable-service bypass as a config fix.
- Do not broaden to readiness-success/wait descriptors, a longer unlock budget, a new severity rule, per-jail profiles, or an AWS API/credential test. Those are out of scope, not blockers.
- Cheap and yours: helper/type decomposition and test fixture names, provided every design bound and output outcome remains fixed.

## Blockers

None. All product/compatibility choices are settled in the design; correct a stale code map against the tree rather than stopping for a preference decision.
