---
title: "Current caller proof for Claude's Mac credential view, enrollment, and logout"
date: 2026-10-27
status: accepted
tags: [research, macos, claude, credentials, oauth, broker, tests]
summary: "Source-verified Mac Claude caller paths at private candidate 5925402: selected-program startup and temp handling, account and host security-shim dispatch through the real CLI, fixture fallback and broker adoption, and broker handling of a deleted written view. Separates the earlier CEA run from new caller tests and preserves the native-macOS limits."
---

# Current caller proof for Claude's Mac credential view, enrollment, and logout

**Status:** Source-verified on 2026-10-27 against private candidate `592540281f8bf4ff1f928e931cca2f5e8bc61d37` (tree `e38ff428719b6d821cdd056bfce0f945e98ef005`). The source change is test-only. This is Linux caller evidence with a Darwin launch predicate; it is not a native macOS certificate or item-acceptance verdict.

This report connects the Mac observations in [the 2026-10-25 Claude login session](../plans/runbooks/mac-claude-login-session.md#what-the-table-abbreviates-the-2026-10-25-lab-report-retired-into-this-section) to the current production callers, permanent regressions, and exact scoped test receipts. It distinguishes the earlier CEA validation of the existing source from the new generated-shim caller tests. The source commit `5925402` is unchanged by this report; the follow-on commit contains documentation only.

## Observed requirements

The Mac lab observed three separate boundaries:

- In **account-mode jail enrollment**, Claude's Keychain write through `security` can hang and suppress its writable-file fallback. The measured write-only fail-fast shim returned 50, allowing Claude's normal file fallback and the broker's enrollment path. This observation does not authorize a default Keychain, broader account reads, or changing the selected credential view.
- In **host view mode**, Claude can use a Keychain entry named for the managed store before consulting the managed file. The measured store-specific adapter hid only that exact service: reads and deletes behaved as missing (44), and adds failed promptly (50); other operations and the user's own service remained outside the rule. Wildcard matching or Keychain enumeration is not part of the implementation.
- In **broker logout**, Claude's `/logout` can remove a view file that the broker previously wrote. The broker must distinguish a previously written, now-missing native view from a view that was never written or was retired, and must not republish a logged-out credential on a later tick.

These are the measured observations and limits in the linked lab report. The source/tests below establish which current callers encode the responses; they do not rerun Claude, login, logout, or Keychain on macOS.

## Current source paths

### Selected-program startup and temp handling

`packs/claude/pack.json` declares the program's nested-temp follower. `macosuser.BuildRunPlanWithDaemons` derives that value from the **selected staged program pack**, carries resolved validation context into the native run plan, preserves a safe explicit follower, and refuses an unsafe composed path. `entrypoint.sandboxTmpdirEnvSplices` and its generated shell adapter validate the resolved native metadata immediately before the selected program executes and set only the declared follower. The CLI's hidden validation command is in `internal/cli.runInternal`.

The permanent tests do more than assert the path predicate: `TestBuildRunPlanMapsSelectedProgramTempEnvToSandboxTMPDIR`, `TestBuildRunPlanMapsTempEnvOnlyFromSelectedProgramPack`, and `TestRunPlanCarriesOnlyResolvedTempValidationContextToNativeLaunchers` cover plan composition; `TestSourceLauncherAdaptsDeclaredTempAtProgramExec` and `TestAccountSourceLauncherPreservesAccountTMPDIR` execute generated launchers; `TestASelfModeLaunchMakesAndRemovesItsTmpdir` drives the self-mode run lifecycle. The tests and call sites are in [`internal/macosuser`](../../internal/macosuser/sessiontmp_packenv_test.go), [`internal/entrypoint`](../../internal/entrypoint/sandboxtmpdir.go), and [`internal/cli`](../../internal/cli/sandboxtmpdir_test.go). These establish source and fixture behavior, not native Seatbelt authorization or vendor startup.

### Account-mode adapter delivery and dispatch

`internal/cli/run` derives the launcher-owned account compatibility carrier only for the selected Claude view route. The `Run` arms pass the resolved mode into the environment; `macosuser.BuildRunPlanWithDaemons` relays that carrier to the guest bootstrap. `entrypoint.generateClaudeSecurityAdapter` requires the account carrier and the selected pack's existing Claude shared-credentials hook; it writes the adapter under Claude's scoped launcher tree rather than a jail-wide blocker. The generated adapter calls `yolo internal claude-security account`, and the Claude program launcher prepends its scoped adapter directory to its PATH. `internal/cli.runInternal` dispatches that hidden verb to `claudesecurity.Main`, which parses the adapter arguments and calls the account filter.

Existing `TestMacosUserKeeperRegistersModeSpecificLocationIntoTheFinalEnvironment` observes the final `Run` environment for account and self modes; `TestAConfigEnvEntryCannotSetALauncherOwnedName` protects the carrier. The entrypoint tests `TestGenerateShimsDeliversTheAccountAdapterOnlyThroughClaudeLauncherPath`, `TestGenerateShimsDoesNotInstallTheAccountAdapterWithoutItsResolvedCarrierOrHook`, and `TestGenerateShimsKeepsAnExistingSecurityBlockAheadOfClaudeAdapter` preserve scoped generation and precedence.

The new [`internal/cli/claudesecuritylaunch_test.go`](../../internal/cli/claudesecuritylaunch_test.go) adds the previously missing end-to-end fixture caller: `TestAccountClaudeGeneratedLauncherDispatchesFixtureFallback` generates the selected shipped Claude adapter and launcher, runs a local fake Claude, executes argv and quoted stdin add forms through the generated `security` shim and real CLI hidden dispatch, requires both to return 50, then writes only dummy fallback data. It does not start actual Claude or inspect a credential.

**Limit:** `TestBothMacosUserProductionLaunchArmsCarryTheAccountSecurityAdapter` observes both production arm source paths, while the existing keeper test executes the keeper arm. The new generated-launcher test exercises resolved carrier-to-bootstrap generation independently; it is not a new runtime exercise of the keeperless native `Run` arm.

### Host fixture decoder lifetime

The account-launcher fixture in [`internal/cli/claudesecuritylaunch_test.go`](../../internal/cli/claudesecuritylaunch_test.go) generates shims and launchers from the shipped pack tree. That reaches the in-jail loader, which enables the process-wide tolerant manifest decoder; this is appropriate for booting a possibly version-skewed jail, but the caller fixture runs in the host CLI test process. Before repair, [`TestJailTreeFixturesRestoreTheStrictDecoder`](../../internal/cli/packupdate_test.go) invoked that real account caller as a subtest and then accepted an unknown manifest field, demonstrating that the caller left later host reads tolerant.

The fixture now forces the strict decoder before generation and resets it in its test cleanup. The parent regression checks malformed-manifest rejection only after the caller subtest and its cleanup finish. This is a fixture-lifetime correction; production decoder behavior is unchanged. The caller-plus-restoration selection failed before this correction and passed after it; the complete short `internal/cli` package, the scoped race selection, and `go vet -mod=vendor ./internal/cli` also passed afterward.

This is Linux source/test evidence only. The account caller skips Darwin before any fixed `/usr/bin/security` operation, and this repair does not establish native Keychain or macOS behavior. The same original independent HIGH reviewer separately reviewed the exact four-path test-only correction and returned **OK / no issues** (review ID `2c3848f9-5356-4ea8-8ee2-a50b4bd8ec01`). The reviewer confirmed the actual `GenerateShims` → `LoadJailPacks` tolerance path, that strict restoration is asserted after the account-caller subtest and its cleanup and before later fixtures can reset global mode, and that the control loads a fresh directory rather than the cache. The reviewer inspected the saved writer RED/GREEN, whole-package, race, vet, census, and documentation receipts, but did not rerun tests or mutate source. Those writer receipts came from the repaired pre-commit `05a7742` working tree, not a post-commit `c1ed` execution. This narrow correction review does not replace item acceptance or native Mac evidence.

### Host adapter, exact service, fallback, and broker adoption

`internal/cli.hostClaudeView` gates the host view on the explicit switch, the launched program's own shared-credentials contribution, and the active broker pack. It registers the managed view, and on the Darwin predicate `writeHostClaudeSecurityAdapter` composes the scoped security adapter ahead of the launch PATH while retaining existing blocker precedence. The service is computed by `claudesecurity.ViewService` from the managed view directory. The generated script calls the same hidden CLI verb in view mode with that exact service. It blocks only matching service operations; unrelated/global/ambiguous operations remain passthrough behavior covered by `internal/claudesecurity` fake-runner tests.

Existing `internal/cli/hostclaudeview_test.go` controls include `TestHostClaudeWithTheSwitchReadsTheMachinesLoginFromAManagedStore`, `TestHostClaudeCredentialViewAddsScopedSecurityAdapterToLaunchPath`, `TestHostClaudeViewOffDoesNotInstallDarwinSecurityAdapter`, `TestTheClaudeViewIsOnlyForClaudesOwnPack`, `TestHostClaudeBesideALaunchOwnedServiceCarriesTheView`, and `TestHostClaudeViewWithTheBrokerLoopholeOffSetsNothingAndSaysWhy`. These cover opt-in, owning-pack and broker gates, plus delivery to the final launch environment. Earlier adapter-content assertions and direct `claudesecurity.Run` tests did **not** exercise the generated host shim through hidden CLI dispatch.

The new `TestHostClaudeGeneratedSecurityShimDispatchesFallbackAndBrokerAdoption` closes that caller gap. It calls `hostMain` with the Darwin predicate, takes the final execution environment, checks the generated script contains the service independently derived from the fixture store path, and only then runs a local fake Claude against that environment. The shim runs the test executable through `cli.Main` → `runInternal` → `claudesecurity.Main`; the fixture requires exact find/delete/argv-add/quoted-stdin-add results of 44/44/50/50. Only after those results does the fake Claude write a dummy credential file in the managed store. The user's own fixture is asserted byte-for-byte unchanged.

The test then invokes the real `oauthbroker.BackgroundRefreshTick` over that fixture and a local `httptest` token endpoint. It observes one redemption, the rotated refresh token in canonical storage, and a published access-token view with the refresh token stripped. This joins host launch delivery to the broker caller path; it is not a Claude login or real broker service test. The broker's separate permanent caller coverage includes `TestAViewCarryingARefreshTokenIsAdoptedRedeemedOnceAndStripped` and `TestAHostViewIsRegisteredRefreshedAdoptedAndSignedOutLikeAWorkspaces` in [`internal/oauthbroker`](../../internal/oauthbroker/views_test.go).

Both new tests skip on Darwin before executing security operations. The production dispatcher uses fixed `/usr/bin/security`; this test suite must not accidentally send any operation to a real Mac Keychain. Linux tests run with synthetic homes, dummy values, fake programs, a stubbed broker start, and a local fake upstream. This remains non-native evidence.

### Broker logout and publication

`oauthbroker.BackgroundRefreshTick` reaches view maintenance under refresh ownership. `viewWasLoggedOut` classifies only a previously written view whose native Darwin file has since disappeared, or a surviving file with OAuth state removed. The view publication path rechecks logout under the storage update lock before rewriting, so a subsequent refresh does not resurrect the deleted native view. Never-written, malformed, unsafe, vendor, and retired-directory cases remain distinct controls.

The current-source caller regressions are in `internal/oauthbroker/views_test.go`: `TestAViewCarryingARefreshTokenIsAdoptedRedeemedOnceAndStripped`, `TestAHostViewIsRegisteredRefreshedAdoptedAndSignedOutLikeAWorkspaces`, and adjacent deleted-view tests exercise `BackgroundRefreshTick`, `ForceRefresh`, registration, canonical adoption, stripped publication, and explicit re-registration. The Mac observation and its limit remain in the [Mac session record](../plans/runbooks/mac-claude-login-session.md#what-the-table-abbreviates-the-2026-10-25-lab-report-retired-into-this-section); the Linux fixture does not certify native `/logout` behavior.

## Earlier CEA validation of unchanged source

The CEA source-gap check started at `cea3797a2ad0d5691e39152043b65ec659cb440d`. Production source remained identical between that base and candidate `5925402`; the only candidate changes are the two test files above. Its nine commands were prior evidence, not rerun as part of the later caller commit:

1. **C1 — host CLI callers, startup followers and host view.** Passed. Its exact `-run` expression requested the two nested-temp caller tests, the shipped-launcher tests, host view/on/off/unrelated/service/no-login/owned-service/broker-off controls, and host session-home controls. This is the source baseline's generated launcher/final-environment coverage; it predates the new generated security-dispatch caller.
2. **C2 — account `Run` carrier and environment.** Passed. The optional `TestRunClearsLauncherOwned.*` alternative matched no test; it is not spoof evidence. The separate C6 command below ran the exact launcher-owned-name test.
3. **C3 — macos-user plan and lifecycle.** Passed. Selected pack mapping, resolved context, account roots, scratch creation/removal and composed-path refusal.
4. **C4 — entrypoint launchers/shims.** Passed. Account adapter delivery, hook/carrier/precedence controls and generated temp launcher cases.
5. **C5 — direct security filtering and broker view mechanics.** Passed. Direct filter tests and full broker view package; these did not cover the generated host shim's hidden CLI caller.
6. **C6 — carrier spoof guard.** Passed exact `TestAConfigEnvEntryCannotSetALauncherOwnedName`.
7. **C7 — interactive filter and child-exit race controls.** Passed the named `internal/claudesecurity` race tests.
8. **C8 — targeted vet.** Passed with no diagnostics.
9. **C9 — readiness callers.** Passed selected bootstrap install/warm/refusal/macos-user checks.

The nine exact commands and recorded results were:

```sh
go test -mod=vendor -short -count=1 -v ./internal/cli -run '^(TestAccountNestedTempFollowerPreservesAccountTMPDIR|TestShippedClaudeLauncherCarriesNativeTMPDIRToNestedClaude|TestUnsafeNestedClaudeTempOverrideRefusesBeforeProgramExec|TestNestedTempFollowerIsNativeSelectedAndDeclaredOnly|TestNativeNestedTempFollowerRefusesWithoutResolvedMetadata|TestNestedTempFollowerInstallOnlyDoesNotExecClaude|TestNestedTempValidationContextUsesResolvedRoots|TestHostClaudeWithTheSwitchReadsTheMachinesLoginFromAManagedStore|TestHostClaudeCredentialViewAddsScopedSecurityAdapterToLaunchPath|TestHostClaudeViewOffDoesNotInstallDarwinSecurityAdapter|TestTheClaudeViewIsOnlyForClaudesOwnPack|TestHostClaudeBesideALaunchOwnedServiceCarriesTheView|TestHostClaudeViewWithTheBrokerLoopholeOffSetsNothingAndSaysWhy|TestHostClaudeViewOnAMachineWithNoLoginSaysLoginEnrollsIt|TestHostPiLaunchGetsItsOwnSessionHome|TestHostPiResidentSessionCapturesUserEditNormally|TestHostSessionHomesCloseRemovesUnlessHandedOver)$'

go test -mod=vendor -short -count=1 -v ./internal/cli/run -run '^(TestMacosUserKeeperRegistersModeSpecificLocationIntoTheFinalEnvironment|TestMacosUserKeeperRoundTripsTheResolvedModeInsteadOfReadingConfig|TestMacosUserViewNoticeReflectsTheInstalledCompatibilityPath|TestRunClearsLauncherOwned.*)$'

go test -mod=vendor -short -count=1 -v ./internal/macosuser -run '^(TestBuildRunPlanMapsSelectedProgramTempEnvToSandboxTMPDIR|TestBuildRunPlanMapsTempEnvOnlyFromSelectedProgramPack|TestRunPlanCarriesOnlyResolvedTempValidationContextToNativeLaunchers|TestAccountRunPlanDoesNotInventUserTempValidationRoots|TestASelfModeLaunchMakesAndRemovesItsTmpdir|TestSelfModeRefusesAComposedTmpdirTheProfileDoesNotGrant)$'

go test -mod=vendor -short -count=1 -v ./internal/entrypoint -run '^(TestGenerateShimsDeliversTheAccountAdapterOnlyThroughClaudeLauncherPath|TestGenerateShimsDoesNotInstallTheAccountAdapterWithoutItsResolvedCarrierOrHook|TestGenerateShimsKeepsAnExistingSecurityBlockAheadOfClaudeAdapter|TestSourceLauncherAdaptsDeclaredTempAtProgramExec|TestAccountSourceLauncherPreservesAccountTMPDIR)$'

go test -mod=vendor -short -count=1 -v ./internal/claudesecurity ./internal/oauthbroker

go test -mod=vendor -short -count=1 -v ./internal/cli/run -run '^TestAConfigEnvEntryCannotSetALauncherOwnedName$'
go test -mod=vendor -race -count=1 -v ./internal/claudesecurity -run '^TestInteractive(StopsForwardingWhenChildExitsWithOpenInput|ProductionRunnerStopsAfterChildExitWithOpenInput|FiltersMatchedLineAndForwardsOtherBytes|BlockedOnlyDoesNotStartSecurityAndHandlesEOFBoundaries|RefusesCompletedLineBeforeEOFThenWaitsForNormalEOF)$'
go vet -mod=vendor ./internal/cli ./internal/cli/run ./internal/macosuser ./internal/entrypoint ./internal/claudesecurity ./internal/oauthbroker
go test -mod=vendor -short -count=1 -v ./internal/entrypoint -run '^(TestReadinessInstallsADeclaredProgramBeforeTheCommandRuns|TestAWarmHomeInstallsNothingAndSaysNothing|TestARefusedReadinessNeverReachesTheCommand|TestMacosUserBootstrapCarriesReadinessChecks)$'
```

Each C1–C9 command exited zero in that prior run. C1's correctly spelled `TestTheClaudeViewIsOnlyForClaudesOwnPack` is included in the command above. C2's wildcard optional test matched none, as stated; C6 is the positive exact spoof receipt. The reported package times were: C1 CLI 0.745s; C2 CLI/run 0.361s; C3 macosuser 0.077s; C4 entrypoint 0.092s; C5 claudesecurity 0.009s and oauthbroker 5.324s; C6 CLI/run 0.197s; C7 race 1.010s; C8 vet no diagnostics; C9 entrypoint 4.000s.

## New `5925402` caller validation and review

The focused CLI receipt was:

```sh
go test -mod=vendor -short -count=1 -timeout=90s ./internal/cli -run '^(TestHostClaudeGeneratedSecurityShimDispatchesFallbackAndBrokerAdoption|TestAccountClaudeGeneratedLauncherDispatchesFixtureFallback|TestHostClaudeWithTheSwitchReadsTheMachinesLoginFromAManagedStore|TestHostClaudeWithoutTheSwitchKeepsItsOwnLogin|TestHostClaudeCredentialViewAddsScopedSecurityAdapterToLaunchPath|TestHostClaudeViewOffDoesNotInstallDarwinSecurityAdapter|TestHostClaudeViewIsOnlyForClaudesOwnPack)$'
```

That exact expression matched the two new functions plus `TestHostClaudeWithTheSwitchReadsTheMachinesLoginFromAManagedStore`, `TestHostClaudeWithoutTheSwitchKeepsItsOwnLogin`, `TestHostClaudeCredentialViewAddsScopedSecurityAdapterToLaunchPath`, and `TestHostClaudeViewOffDoesNotInstallDarwinSecurityAdapter`. It did **not** match the misspelled `TestHostClaudeViewIsOnlyForClaudesOwnPack`; that command does not prove `TestTheClaudeViewIsOnlyForClaudesOwnPack` ran. The no-switch function runs its own on/off subcases. The command exited zero, but do not turn its 7 requested alternatives into 7 matched function claims. The correctly named unrelated-pack control was run by C1 above.

- `go test -mod=vendor -race -short -count=1 -timeout=120s ./internal/cli -run '^(TestHostClaudeGeneratedSecurityShimDispatchesFallbackAndBrokerAdoption|TestAccountClaudeGeneratedLauncherDispatchesFixtureFallback)$'` passed.
- `go test -mod=vendor -race -short -count=1 -timeout=120s ./internal/claudesecurity -run '^(TestInteractiveFiltersMatchedLineAndForwardsOtherBytes|TestInteractiveBlockedOnlyDoesNotStartSecurityAndHandlesEOFBoundaries|TestViewModeParsesCommandPositionAndEffectiveServiceOption|TestViewModeRefusesOnlyExactManagedService)$'` passed.
- The two broker caller fixtures passed their focused command: `go test -mod=vendor -short -count=1 -timeout=90s ./internal/oauthbroker -run '^(TestAViewCarryingARefreshTokenIsAdoptedRedeemedOnceAndStripped|TestAHostViewIsRegisteredRefreshedAdoptedAndSignedOutLikeAWorkspaces)$'`.
- `go vet -mod=vendor ./internal/cli ./internal/claudesecurity ./internal/oauthbroker` reported no diagnostics.
- Controlled source deletion of the hidden `claudesecurity.Verb` dispatcher and, separately, controlled breakage of `cli.Main`'s `runInternal(args[1:])` forwarding each made the generated host caller test fail with exit status 1. Both original source files were restored byte-for-byte before candidate commit. This demonstrates caller sensitivity, not a product-source change.

The independent focused HIGH review of candidate `5925402` returned **OK, no blocker**. It independently checked the exact two-file test-only diff, source handoff, exact-service derivation, no real Keychain execution, broker fixture assertions, mutation evidence, and the limits below. The reviewer did not rerun tests. Root retains the item acceptance and serial integration/landing gates.

## Limits

- The measured Mac behavior is from the [2026-10-25 Mac session record](../plans/runbooks/mac-claude-login-session.md#what-the-table-abbreviates-the-2026-10-25-lab-report-retired-into-this-section). It is not a fresh Mac run.
- New generated-shim tests run on Linux with the Darwin launch predicate. They skip Darwin before executing because the production forwarding path is the fixed `/usr/bin/security`; no test reaches a real Keychain.
- No real Claude, login/logout UI, account, credential, Keychain, or external token endpoint was used in the new tests. The fake broker endpoint returns synthetic values and counts one redemption.
- Native account/self/host Keychain semantics, Apple Container behavior, real vendor lifetime, and macOS `/logout` remain unverified by this work.
- The original CEA/`5925402` caller runs did not include whole-tree qualification. The later combined source/test candidate ran one passing `just check-ci` on `094a910bb3213a0697dc9531a590beb99e1df404` and one passing full `go test -count=1 -timeout 0 ./integration` on `d8ce418acb98eb73b7f02436c53f4354e64b4b14`; the check tree differs from the integration tree only by the Mac review-status paragraph. Exact receipts, durations, and the source/test-input bridge are recorded in the [TTY combined verification](./current-terminal-boundary-verification-20261027.md). `just done`, final main landing, and item acceptance remain Root's work.
- The review is a source review, not native acceptance. Root owns item acceptance and any later publication.
