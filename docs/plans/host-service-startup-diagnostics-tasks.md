---
title: "Tasks: host-service startup diagnostics"
status: accepted
stage: BUILT
next: "None: built. Graduate the remaining contract text into the references when this design is next touched"
depends-on:
  - ../design/host-service-startup-diagnostics.md
  - host-service-startup-diagnostics.md
tags: [tasks, host-services, diagnostics, launch]
summary: "Ordered implementation and verification slices for safe settings preflight, attempt-attributed daemon refusals and real launch propagation."
vantage:
  status-chip: true
---

# Tasks: host-service startup diagnostics

**Status:** 2026-10-08. Landed, every task row checked: the `settings_check` preflight and its `yolo check` phase (backend-aware, skipping only the refused service's doctor), the opt-in `startup_reason` channel with its readiness deadlines, private per-jail settings snapshots and their cleanup, the singleton's prepare-then-publish transaction, the AWS pack's safe validator, removal of the routine `unnarrowed` launch notice, and typed startup outcomes carried to every front door: the terminal, the keeper's relayed output, the macos-user arm and `yolo host`. Open: none; the [QA record](host-service-startup-diagnostics-qa.md) lists what only a real Mac or rootless host can show.

## 1. Capture the missing-cause regression first

- [x] Build a selected/approved fake loophole pack and run its real host-service disclosure/start boundary (`startLoopholesDisclosed`), not only a parser or broker helper.
- [x] Fake a cooperative pre-bind refusal with a safe actionable remedy; assert the launch-facing output contains the cause and remedy, not a stale log line.
- [x] Seed the historical shared service log with a contradictory refusal and prove it has no effect on the launch output.
- [x] Run the focused test against unchanged production code and save the failing command/output with the preserved candidate evidence. This old-call-site failure is required before implementation.
- [x] Add behavioral mutation checks for the keeper, native `Run`, and host doorway caller as each is wired; remove its production forwarding call and observe that its test fails, then restore it and record green. (`startupfrontdoor_test.go`: deleting the keeper's relay, either arm's preflight refusal, `startPlannedLoopholes`' hand-back or `HostDoorways.Start`'s post-start hand-back fails a test; the doorway refusal test now asserts no daemon exec disclosure, which fails when only `HostDoorways.Start`'s own preflight is deleted.)

## 2. Add generic manifest and pure settings validation

- [x] Extend `host_daemon` schema with optional `settings_check` argv and `startup_reason` opt-in. Test decode, malformed argv, placeholder resolution, invalid field placement and pack lint; legacy manifests remain accepted.
- [x] Include the validator argv in resolved records and host-code placement checks. Assert unapproved, disabled, inactive, unsupported-backend and agent-editable placement fixtures never execute it. (`TestPrepareSettingsValidatorAdmissionAtLaunchCaller`, `TestRunAdmissionNegativeCasesDoNotExecuteSelectedSettingsValidator`, and the check-side admission tests.)
- [x] Resolve current merged config to one declaration-total frozen serialized snapshot. Test defaults, secret-bearing setting values, unique private validator-input paths, mode `0600`, cleanup after pass/fail/timeout and stable settings file unchanged during preflight. Keep the authoritative bytes in yolo; a fixture validator that rewrites its input must not change the daemon's later publication bytes. (`TestLaunchPreflightValidatorInputIsPrivateAndAlwaysRemoved`, `TestManifestSettingsCheckUsesAndRetainsLaunchPrivateSnapshot`.)
- [x] Test direct argv execution with a fixture executable: exit zero, nonzero cause/remedy, missing executable, 2-second timeout, 4-KiB capture cap, empty output and terminal-control/noisy text sanitization. No shell, AWS CLI, credential, state or API call.
- [x] Place launch preflight after applicable exec disclosures but before expensive provisioning and any settings publication/spawn/front. A nonzero refusal prints cause, remedy, host `yolo check --no-build` and retry, and is fatal without a bypass suggestion. (Caller-order regressions and Step A evidence; broader dynamic caller-output cases remain unchecked below.)
- [x] Extend `yolo check --no-build`: evaluate freshly edited config even if daemon settings files are absent or stale; validation itself never publishes settings or invokes the service doctor. A valid candidate preserves its existing doctor; a refusal skips only that service's doctor, while unrelated services' doctors continue. The doctor remains a separate health phase after validation.
- [x] Add doctor-skip regressions for validator timeout and execution failure, in addition to the covered refusal path. (`TestCheckLoopholesNamesValidatorOutcomeAndSkipsOnlyItsDoctor`.)

## 3. Isolate settings-check-enabled per-jail daemon snapshots

- [x] At the real per-jail spawn caller, give each opted-in daemon a unique private settings path written from the exact validated bytes, not `SettingsFileFor(name)`. The daemon's bytes survive a validator rewriting its private input, and the file remains through owned-service teardown and is removed afterward.
- [x] Assert the private daemon snapshot's `0600` mode and cleanup on spawn failure; separately pin that a legacy manifest continues using the stable name-keyed settings path. (`TestPerJailPrivateSnapshotIsRemovedWhenTheSpawnFails`, `TestLegacyPerJailManifestKeepsTheStableSettingsPath`.)
- [x] Force two different workspaces to interleave: both validate distinct settings, pause both daemons before they open `{settings}`, then allow each to read. Assert the second publication/spawn cannot substitute bytes into the first daemon's path; assert paths differ and both contents match their own validated snapshot.
- [x] Prove a legacy per-jail manifest without `settings_check` retains the existing stable name-keyed path/behavior. Assert private files are cleaned on spawn failure and normal owned-service teardown, but never unlinked while a service may still read them. (Same tests, plus teardown removal in `TestManifestSettingsCheckUsesAndRetainsLaunchPrivateSnapshot`.)

## 4. Make singleton settings a frozen transaction

- [x] Add a lifecycle callback/transaction in `broker.EnsureSingleton`: after acquiring the existing flock, validate success has already been established for this exact snapshot; atomically publish those bytes before running settings drift/kill/spawn logic.
- [x] Assert an invalid desired snapshot is refused while a valid singleton remains live with its settings record, socket and already-published front intact; `HostDoorways.Start` returns before its doorway callback. (Local process/front fixture; the full client-request/new-launch front matrix remains open below.)
- [x] Assert a valid changed snapshot still follows the existing disclosed host-wide restart behavior and its launch reaches the daemon with exactly the snapshot that passed validation. A fixture daemon reads settings before making its readiness socket available, matching the opt-in singleton contract; prove a later launch cannot swap bytes before that read and the daemon does not depend on lazy rereads after readiness. (`TestValidatedSingletonSettingsChangeRestartsOntoTheValidatedBytes`.)
- [x] Race two launches with distinct frozen snapshots and force interleaving around publication. Each spawn record must match its transaction's supplied snapshot; no shared-file reread may cross attempts. Later valid shared settings changes retain current last-serialized behavior. (Broker flock test; not a simultaneous pair of full validating launches or proof that all affected live fronts are disclosed.)
- [x] Cover singleton-lock failure and failed atomic settings publication; neither may replace current settings or proceed to kill/spawn. Validator timeout/refusal is covered at its own runner seam.
- [x] Exercise validator refusal/timeout and migration/preparation failures together with a live singleton lifecycle; none may falsely stamp an unvalidated candidate as active. (Caller level: validator refusal and timeout, and a failed preparation, in `TestInvalidSingletonCandidatePreservesLiveDaemonAndExistingFront` and `TestFailedSingletonCandidateLeavesTheLiveDaemonOnItsSettings`; a migration failure stops the live daemon by design and is pinned at broker level. Preparation now runs before publication, so a failed preparation publishes nothing.)

## 5. Add bounded attempt-reason transport

- [x] Implement the opt-in socketpair protocol, child fd/env setup, random attempt token and producer helper. Test version, service, attempt token, allowed class, length-prefix and 4-KiB maximum.
- [x] Test accepted refusal/remedy transport, wrong service/token, malformed attribution/framing, oversized/noisy text and control/markup sanitization without rendering raw bytes. (Protocol unit tests; caller tests render configuration refusals.)
- [x] Test absent reason, late record, peer holding its endpoint open and bounded reads with no EOF wait. (Hostservice tests.)
- [x] Exercise no-record/child-exit/readiness-timeout cleanup combinations and prove absent reason plus an accepted service socket stays ready; preserve a clean daemonizing wrapper with a reachable child. (`TestPerJailRecordlessStartupReasonCleanup`, `TestPerJailDaemonizingWrapperWithStartupReasonIsReady`.)

## 6. Return typed outcomes from both service ownership paths

- [x] Wire the host-wide singleton spawn and launch/keeper-owned child spawn to capture the attempt reason; keep shared singleton alive on failed preflight and kill only a process this attempt owns on a terminal failed startup when existing lifecycle rules require it.
- [x] Keep configuration refusal, validator exec failure/timeout, missing daemon executable, cooperative refusal, process exit, live readiness timeout, channel fault and front/socket/endpoint transport failure distinguishable in tests and output. (Owner-local: `hostservice.StartupOutcome` keeps them distinct in both owners' results, with tests in `internal/broker` and `internal/cli/run`; output and validator-category bridging remain.) (Owner-local outcomes plus output: configuration refusals refuse the launch, other cooperative refusals print first as warnings, transport and readiness keep their warnings.)
- [x] Confirm old shared log contents cannot replace any current attempt outcome; the log path remains manual follow-up only.
- [x] Run `internal/cli/run` tests proving service cause appears before derived socket/reachability symptoms; old-daemon and front refusal behavior remains accurate. (`TestPerJailNonConfigurationRefusalIsPrintedBeforeTheDerivedSymptom`, `TestAFreshContainerLaunchKeepsANonConfigurationRefusalAWarning`.)

## 7. Carry failures through every launch caller

- [x] Expand the current `olderDaemonRefusal` boundary to carry typed host startup causes without collapsing distinct outcomes. (Only a `configuration`-class cause is fatal, through `olderDaemonRefusal.startup`; other classes stay distinct warnings, with severity unchanged by [design §4.2](../design/host-service-startup-diagnostics.md#42-outcomes-and-presentation).)
- [x] Exercise fresh container output through keeper's real plan/start/frames/final result; cause and remedy reach the launch client, not just keeper stderr/log. (`TestAFreshContainerLaunchRelaysADaemonRefusalThroughItsKeeper`, `TestAFreshMacosUserLaunchRelaysADaemonRefusalThroughItsKeeper`.)
- [x] Exercise native launch (`run.go` native arm) and `HostDoorways.Start` / `yolo host --`; assert the same cause, remedy and cleanup at each actual front door. (`TestAFreshMacosUserLaunchRefusesInvalidSettingsBeforeItsKeeper`, `TestHostDoorwaysStartPropagatesSettingsRefusalBeforeStartingDoorway`, `TestAHostDoorwayStartReturnsADaemonRefusal`; the keeperless macos-user arm now prints the start's refusal before the OpenAI credential refusal, as the keeper does.)
- [x] Keep failed-launch cleanup ownership narrow: stop launch-owned fronts/children; never tear down a shared singleton for a rejected desired snapshot. (A refusing daemon this attempt spawned is stopped by that attempt; a live shared singleton is never stopped for a rejected candidate.)
- [x] Retain all disclosure-before-exec assertions and the `YOLO_HOST_LOOPBACK=requested|shared|unsupported|unknown` severity matrix. No new hatch or severity branch. (No severity code changed; the full `internal/cli/run` suite passes.)

## 8. Integrate the AWS pack without teaching core AWS

- [x] Add a resolver-only AWS settings command in `internal/awsauthdaemon` and declare it in `packs/aws-auth/loopholes/aws-auth/manifest.jsonc`. Reuse `awsauth.Settings.Resolve` for the decision, but never forward `err.Error()` into validator output or the attempt channel. Give resolver refusals typed kinds and map them in the AWS pack to fixed, safe cause/remedy text; preserve the missing-narrowing cause and explicit user-scope remedy without values.
- [x] Include a sentinel profile plus a policy containing sentinel object keys/values. Assert none appear in settings-validator stdout/stderr, and prove the safe reason/remedy identifies the missing narrowing and user-scope settings path; do not rely on truncation or terminal sanitization as secret redaction.
- [x] Assert AWS sentinel values are absent from actual attempt-reason records and terminal launch diagnostics; record and rendered-launch regressions use profile and policy sentinels.
- [x] Prove the settings-only validator does not invoke a fake `aws` CLI and retain check tests for valid-doctor and refusal-skip behavior; no AWS API or credential tests. Unrelated doctors continue after another service refuses.
- [x] Exercise `yolo check` doctor skip/continuation on validator timeout and executable failure, not just refusal. (See [§2](#2-add-generic-manifest-and-pure-settings-validation).)
- [x] Add generic non-AWS fixtures: the selected startup-reason fixture also validates declared settings with `settings_check`; check/runtime tests cover generic validators. Added-line review of changed core lifecycle/schema paths found no AWS-specific behavior branch or setting keys.
- [x] Run targeted AWS and generic unit suites, selected `go test -race` coverage and `just check-pack-binaries` (no official pack binary currently declares a pin).

## 9. Update user-facing and authority docs after behavior exists

- [x] Audit candidate drafts and update `docs/reference/loophole-system.md` and `docs/reference/loophole-transport.md` only after the manifest fields and runtime/transport semantics are accepted and land; the transport reference explicitly records the remaining non-configuration outcome limitation.
- [x] Audit candidate draft and update `docs/reference/agent-credentials.md` for the AWS settings preflight and refusal path. Recheck its AWS daemon, check, log and host-scope statements rather than changing links alone.
- [x] Audit the candidate and add one short user-facing `CHANGELOG.md` Unreleased fix note; no released section edits.
- [x] Re-run `uvx vantage-check check --strict` on every changed Markdown document, `uvx vantage-check index` and `uvx vantage-check index --roadmap docs/plans/roadmap.md`; repair only task-owned findings. (One stale anchor was corrected before the final passing run.)
- [x] Run targeted unit/race tests and `just check-pack-binaries`. Parent owns final combined `just check-ci`, `just build-go`, fresh-binary nested launch and applicable full integration suite; nested jail is not evidence of real rootless loopback behavior.

## Stop conditions and boundaries

- A genuine behavior conflict with the accepted design is the only reason to stop for a decision; ordinary symbol/file drift is corrected in the plan.
- No change to AWS permission policy, config scope, existing-client validity, backend reachability severity or credentials may be inferred from a passing unit test.
- No agent CLI, AWS API, real credentials, host configuration, daemon, keychain or interrupted worktree is used in verification.
- Keep the worktree index unstaged. Do not commit, push, dispatch CI, publish or run the parent-owned combined gates from this implementation stage.
