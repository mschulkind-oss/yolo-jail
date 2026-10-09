# Cached-good launch recovery: focused QA

This note records scoped checks for `internal/cli/cachedgood.go`. It is not runtime certification for any operator's machine store.

The selector uses only the check record's exact `Good.Entry`. It requires the original build receipt to be an admission `record` act, verifies the entry's digest and manifest against actual contents, and checks the runnable path/interpreter against the current delivery contract. It does not scan for an older entry, require today's recipe, modify Good/outcomes/backoff, or admit failed output. The final owner-record lock is non-waiting; contention gives a retry step.

A deterministic local upstream/build fixture records an initial Good, then a changed recipe failure. The production caller's exact delivered resolver key is passed through the generated source launcher, install-only readiness, and the production capture-materializer function. The fixture verifies stale-home replacement, no readiness execution, argv/cwd, independent stdout/stderr, and native exit 23. Negative fixtures cover no/pruned Good, corrupt contents, non-record receipt, unsupported arbitrary and Node shebangs (env and absolute without a current Node floor), and an intact ELF of the wrong target machine. Caller coverage preserves an ordinary packaging failure on recovery success, validation refusal and hand refusal; typed patch failure coverage is exercised on recovery success and validation refusal. A new current output declaration remains compatible when the exact recorded runnable path is intact. No actual Pi program, credentials, network service, or API is used.

## Focused commands

All commands were run in the isolated worktree with inherited Go environment/caches unchanged.

```sh
go test -short -count=1 -timeout=120s ./internal/cli -list '^(TestBuildSlot|TestCachedGoodDisclosureKeepsCurrentTypedPatchFailureOnSuccessAndRefusal|TestCachedGoodMaterializerHelperProcess)'
go test -short -count=1 -timeout=120s ./internal/cli -run '^Test(BuildSlot.*|CachedGoodDisclosureKeepsCurrentTypedPatchFailureOnSuccessAndRefusal|CachedGoodMaterializerHelperProcess)$' -v
go test -race -short -count=1 -timeout=120s ./internal/cli -run '^Test(BuildSlot.*|CachedGoodDisclosureKeepsCurrentTypedPatchFailureOnSuccessAndRefusal|CachedGoodMaterializerHelperProcess)$' -v

go test -short -count=1 -timeout=120s ./internal/cli/run -list '^(TestFreshLaunchCarriesCachedGoodOwnerIntoBuildSlot|TestInvalidCachedGoodOwnerRefusesBeforeBuildSlot|TestCachedGoodOwnerRefusesAttach|TestAPatchedForkForNoneOfThisPlatformRefusesNothing|TestNoPatchedForkAdvancesBelowTheAppleContainerFloor)$'
go test -short -count=1 -timeout=120s ./internal/cli/run -run '^Test(FreshLaunchCarriesCachedGoodOwnerIntoBuildSlot|InvalidCachedGoodOwnerRefusesBeforeBuildSlot|CachedGoodOwnerRefusesAttach|APatchedForkForNoneOfThisPlatformRefusesNothing|NoPatchedForkAdvancesBelowTheAppleContainerFloor)$' -v
go test -race -short -count=1 -timeout=120s ./internal/cli/run -run '^Test(FreshLaunchCarriesCachedGoodOwnerIntoBuildSlot|InvalidCachedGoodOwnerRefusesBeforeBuildSlot|CachedGoodOwnerRefusesAttach|APatchedForkForNoneOfThisPlatformRefusesNothing|NoPatchedForkAdvancesBelowTheAppleContainerFloor)$' -v

go test -short -count=1 -timeout=120s ./internal/entrypoint -list '^Test(TheJailsPackLoaderAppliesForks|TheSourceLauncherNeverDeliversTheBasesProgram|TheSourceLauncherMaterializesTheHostsKeyOncePerKey|TheSourceLauncherKeysItsBuildToItsOwnHome|TheCatalogAccountsForAForksOutputs|TheCatalogListsNoForkFile)$'
go test -short -count=1 -timeout=120s ./internal/entrypoint -run '^Test(TheJailsPackLoaderAppliesForks|TheSourceLauncherNeverDeliversTheBasesProgram|TheSourceLauncherMaterializesTheHostsKeyOncePerKey|TheSourceLauncherKeysItsBuildToItsOwnHome|TheCatalogAccountsForAForksOutputs|TheCatalogListsNoForkFile)$' -v
go test -race -short -count=1 -timeout=120s ./internal/entrypoint -run '^Test(TheJailsPackLoaderAppliesForks|TheSourceLauncherNeverDeliversTheBasesProgram|TheSourceLauncherMaterializesTheHostsKeyOncePerKey|TheSourceLauncherKeysItsBuildToItsOwnHome|TheCatalogAccountsForAForksOutputs|TheCatalogListsNoForkFile)$' -v

go vet ./internal/cli ./internal/cli/run ./internal/entrypoint ./internal/packsrc ./internal/packload
git diff --check
```

The CLI recovery, Run selector/attach/platform-floor, and restored entrypoint launcher/call-site suites passed in both regular and race modes, with verbose per-test PASS output. Scoped vet and diff check passed. The two broader run-package tests that failed in the previous report now pass after their fixtures explicitly select host context and an Apple Container version below the read-only floor; ambient `YOLO_VERSION` was not changed globally. Parent-owned whole-tree, nested, and integration gates remain outside this focused QA.
