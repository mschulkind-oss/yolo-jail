---
title: "Implementation plan: Mac Claude lab follow-ups"
status: accepted
stage: DECIDED
next: "Build isolated components and review the combined private candidate; native confirmation follows parent landing"
depends-on:
  - mac-claude-lab-followups.md
tags: [macos-user, claude, implementation]
---

# Implementation plan: Mac Claude lab follow-ups

**Status:** 2026-10-27. Written against `9d040116b531b937d595148a42dcb27cee0c1153`. The [accepted behavior](mac-claude-lab-followups.md) wins on behavior, the tree wins on cosmetic facts, and this plan is implementation advice. Existing labs remain unchanged. No native validation or built claim is made.

## Component ownership

This is multi-seam: the final session environment, a process compatibility adapter, and persistent broker logout state are independently testable contracts. Writers use three distinct managed worktrees based on this lane, commit only there, and report their commit, parent, tree, evidence and diff. The lead serially integrates into its own private branch. Main remains untouched.

| Component | Exclusive source fence | Existing caller / fixture | Gate |
| :--- | :--- | :--- | :--- |
| Temp | Claude pack manifest; generic declaration/loading support; macos-user session-temp/run-plan integration and its tests | `BuildRunPlanWithDaemons`, `sessionTmpPlanWith`, `withSessionTmpEnv` | Selected/unselected self/account final env regressions; temp validation; race |
| Security | New compatibility package; existing hidden internal dispatch; host-view integration; entrypoint delivery; new macos-user security-only file; native CLI/run integration and credential-view notices/tests | `hostLaunch` → `hostClaudeView`; keeper and direct native launch; generated program carriers | Actual final PATH invocation + parser/spies, host scope, account write-only/self unchanged |
| Logout | Broker view maintenance/state and broker view tests only | `RegisterView`, `BackgroundRefreshTick`, `ForceRefresh`, `newViewFixture` | Register/write/delete/maintain/refresh/re-register; never-written; retirement; race |

No sibling edits another component's fence. Temp owns the existing native run-plan file. Security may expose its integration via a new security-only file, or request an exact small callsite transfer to temp ownership before integration; it must not independently patch that file. CLI/run files and notice corrections belong only to security. Documentation remains lead-owned.

## Reuse and traps

- Native final env is serialized by the run plan after user layers; updating an earlier env that is discarded is not a repair. Keep profile temp validation and `/tmp` denial. Account mode has a distinct existing layout; no unnecessary self shim.
- Core discovers selected pack declarations, not agent membership. Use a generic declarative program temp-env field or equally narrow existing pack-owned facility; the Claude spelling lives in its pack, not a core agent switch. Naming and in-memory factoring are cheap engineering choices.
- Generated compatibility scripts use the existing `yolo` internal dispatcher, not a new shipped binary or pack `binaries`. A script invoking an absent caller, or a helper tested without generated PATH delivery, is not acceptance.
- Host view selection already checks the installing pack's credential hook and active broker. Preserve that gate, both exec/resident host paths, and view-off behavior. Compute the service from the actual managed store, not an arbitrary wildcard.
- Broker `Written` and `LoggedOut` persist under `refresh.lock`. Registration creates a fresh written mark deliberately. Missing parent directories, bad JSON, symlinks and machine signout are not interchangeable with a removed credential file.

## Tests first and component handoff

### Temp (independent)

Extend the native session fixture with the selected staged Claude pack. Before the fix, assert the final env has `CLAUDE_CODE_TMPDIR == TMPDIR`; watch it fail. Cover both modes where native TMPDIR is available, unselected/native-other modes unchanged, safe override preserved and unsafe override refused by existing granted-root validation. Pin production plan/caller, not only a new getter. Existing self temp tests continue to pin stage/create/remove and profile denial. The exact fixture wiring is local engineering; literal expected variable is `CLAUDE_CODE_TMPDIR`.

Default mechanism: a generic program declaration listing sandbox temp-env followers, consumed from the selected pack tree when the final native sandbox environment is constructed. A direct-command plan mapping alone is insufficient: selected Claude invoked later from plain `yolo --bash` is ordinary startup, not backlog. After the temp component is frozen, security's existing entrypoint/launcher owner consumes that same declaration for per-program execution-time delivery, with the existing temp validation contract. Plain bash and unrelated program environments do not gain Claude's follower. Pin actual shipped declaration and actual generated-launcher invocation, direct/nested parity, quoted temp paths, generic declared-program positive controls, install-only behavior and unsafe override rejection. Do not execute pack code outside confinement or add grants. Stop on a genuine wider-privilege need. Commit: `fix(macos-user): align selected Claude scratch with sandbox temp`.

### Security (independent, with explicit common-file contract)

Before implementing, add permanent regressions at actual host/native launch PATH and dispatch. Expected refusal for account add is 50, host view find/delete 44 and add 50; self and view-off preserve existing PATH behavior. Fake `/usr/bin/security` behavior is supplied by local spies, never real Keychain access. Both `security add-generic-password ... -s <service>` and `security -i` receiving quoted commands must be exercised; unknown/multiple-command forwarding must retain bytes and non-secret control semantics.

Use exact service scope for the actual host view store; never hide bare credentials or account reads. The adapter must not log argv secrets or stdin. A compatibility setup failure names a supported next step and must not silently make the selected as-shipped Claude broken. Correct obsolete native temp/write-hang notices only after the chosen mechanism is present. Commit: `fix(claude): route Mac credential saves through the existing file boundary`.

### Logout (independent)

Use the real broker fixture, register a native `macos-user` view, write a canonical login, remove the resulting file and call background maintenance, then forced refresh. Assert the removed workspace stays signed out, canonical credentials and a sibling remain live, and explicit `RegisterView` reports `WasLoggedOut` and restores the view. Watch this fail before changing maintenance. Include host Darwin registration if deletion is tied to actual host OS; preserve Linux/podman missing-file recovery. Use an explicit existing-runtime or host-platform seam so Linux tests execute the demonstrated Darwin decision rather than skipping it. Never-written controls and machine signout/reset remain mandatory. Commit: `fix(oauthbroker): honor removed Darwin credential views as workspace logout`.

## Validation custody

Before each Go invocation capture source HEAD/tree/diff/hash, command, Go env, relevant ambient env and actual umask. Preserve normal HOME, cache paths, loader flags, NODE_OPTIONS and TMPDIR; no environment stripping, cache clearing, load management, arbitrary timeouts or weakened assertions. Store separate stdout/stderr and finite PRE/LIVE/terminal exit/signal/EOF evidence outside the tracked tree. Unit/race/scoped vet/staticcheck only; no full landing suite, interactive Claude/API/auth or native host run. Darwin compile is labeled compile-only. The lead checks artifacts and one fresh high-effort review; the parent runs combined landing qualification.

## Residual baseline validation

`TestStageFontCopyConcurrentLaunchesAgree` in [the native font-copy tests](../../internal/macosuser/fontcopy_test.go) has an observed pre-existing concurrency failure: some concurrent copy results have an empty path. Before temp adoption, the lead's complete macos-user source still matched published `9d040116b531b937d595148a42dcb27cee0c1153`. The direct race reproducer passed once, then the bounded twenty-run reproducer failed with exit 1 and no signal:

```console
$ go test -race -count=1 -run '^TestStageFontCopyConcurrentLaunchesAgree$' ./internal/macosuser
$ go test -race -count=20 -run '^TestStageFontCopyConcurrentLaunchesAgree$' ./internal/macosuser
```

The temp writer's broader scoped race command also failed at this same macos-user test; it is not a packload failure. This is backlog outside the demonstrated Claude repairs. Keep the test and assertions unchanged, do not alter timeouts, environment, caches or load policy, and do not retry the unchanged twenty-run reproducer in this lane. Focused temp caller/race evidence remains required. The parent owns the combined landing gate and disclosure of this residual; no claim that all race checks are green is made.

## Focused native confirmation after parent publication

**Built on main (checked 2026-11-23):** temp `60549a751`, security `173b67c77` and `61d860711`, logout `b2f28da6b`. **Run these steps at main `61d860711` or later**; none has had a native run yet (the 2026-10-27 Mac session ran before these landed).

1. Use the parent's published candidate and a disposable valid workspace under `/Users/Shared/yolo`, selected Claude only; do not repeat the full backend suite. Record launcher/Claude versions. After any user-scope mode or config edit, run `yolo check --no-build` before relaunching.
2. In self mode verify direct Claude startup and selected Claude invoked from plain `yolo --bash`, without manual temp/store overrides; record that scratch uses the same session TMPDIR, not the host `/tmp/claude-<uid>`. Bash and an unrelated program must not receive Claude's follower.
3. Human `/login` in account mode: confirm prompt completion, broker enrollment and a refresh-token-free view. Do not inspect or alter other Keychains.
4. Human Darwin `YOLO_CLAUDE_CREDENTIAL_VIEW=1 yolo host -- claude`: confirm the managed store is used, login enrolls through its file, and the user's own bare Keychain entry remains unchanged. View off remains the own-login path.
5. Human `/logout` in one native workspace: confirm maintenance/refresh do not recreate its view, another registered view remains signed in, and the next explicit launch reports re-sign-in. Machine logout is not requested.

Record only status, fingerprints and non-secret paths. Subscription metadata/onboarding remain backlog; Apple Container networking remains a separate host diagnostic. Historical reports are not rewritten into successful results.
