---
status: accepted
stage: BUILT
next: "Root combined gate and native macOS lifecycle verification"
---

# Stop maintaining a Claude view when its next jail launch selects Off

**Status:** 2026-10-29. Metadata-retirement implementation is built in the reviewed candidate and assembled for the combined gate. **Measured natively 2026-10-29** ([Mac evidence](macos-native-tls.md#mac-evidence-witness-run), [OQ-MT9](macos-native-tls.md#OQ-MT9)): On then Off retired the workspace's registration (2 views to 1; run at `6f651f8c4`); the matched-launcher nested smoke passed only an isolated empty-pack shell startup and does not witness Claude retirement. The earlier inherited-config smoke failed before verifying the candidate launch.

**Status:** 2026-10-29. Written against `8c0bc42f3a72e19b9f27a06f33b1fdf4a63c4679`;
production keeper Off transition reproduced with private fake tokens.
**Authority:** [Claude login without interception](claude-login-without-interception.md),
plus accepted tactical lane D scope: retire/report stale registration, preserve unsafe-link refusal.
Design wins on behavior; tree wins on fact; this plan is perishable advice.

## Consumer and fence map

| Path | Owned change / consumer |
| :--- | :--- |
| `internal/cli/run/claudecredentialview.go` | Resolve exact runtime/mode location before On/Off; Off retires metadata only |
| `internal/cli/run/claudeviewretirement_test.go` | Permanent real-store, production keeper RED and transition tests |
| `internal/oauthbroker/viewretirement.go` | New exact-location metadata retirement under existing refresh lock |
| `internal/oauthbroker/viewretirement_test.go` | New broker idempotence, sibling/logout/link/failure/status tests |
| `internal/oauthbroker/views.go` | Optional minimal reuse factoring only if error-returning drop needed |
| `docs/design/claude-view-retirement-plan.md` | This bounded handoff; outcome accurately stamped |
| `scripts/completion-census.json` | Only if completion census actually changes this generated path |

`run.go` macos-user registration and `keeper.go` unsealed registration already call the helper
unconditionally. No edits to those consumers, A's inherited-snapshot scope or C's
`programcompatibility.go`/`shims.go` scope. No known shared production hunk.
Host `hostclaudeview.go` also skips registration reconciliation when Off, but its managed store
can serve existing concurrent host sessions: separate finding, not this workspace-layout correction.

## Reuse and traps

- `oauthbroker.registrationFile` identifies exactly workspace+subdir; never sweep siblings.
- `oauthbroker.lockedErr` serializes retirement with registration and broker publication, propagating lock errors.
- `viewRegistration.drop` removes registry metadata but currently only logs errors; retirement must return failures.
- `DescribeStore` consumes registrations: a retired location disappears naturally, without a new status schema.
- `newViewFixture`, `registrationFor`, `assertView` in broker tests already build synthetic store and login state.
- `startKeeperFixtureWith` and `relayWithin` execute the production keeper caller; the fixture changes HOME.
- Retirement is **metadata-only**: no credential reads, sanitization, unlink, symlink following or directory creation.
  The subsequent existing Off layout delivery is not this API's responsibility. Canonical/shared/sibling bytes stay untouched.
- No registration or broker state is an idempotent no-op. Existing state with inaccessible registry/lock or failed removal
  is a nonfatal, actionable launch warning, not a claimed revocation. Retry remains possible.
- Sealed keeper gate remains intact; no new broker authority or token endpoint calls.

## Independent implementation task

**Settled — do not reopen:** On retains existing behavior. Off retires only the selected jail location's
registration, so broker ticks no longer write it or repeatedly refuse an interception link.
Unsafe jail-writable links remain refused by existing broker reads/writes. Do not change machine login,
refresh-token sharing, SecurityServer denies, host registration semantics, defaults or switch recognition.

**Preconditions:** clean implementation tree at the committed planning ref; exact RED below reproduces.
Stop/report if RED differs, named source is absent, fence needs expansion, or unrelated gate fails.

**Tests first:** complete permanent test is `internal/cli/run/claudeviewretirement_test.go`.
`go test -short ./internal/cli/run -run '^TestClaudeViewOnToOffRetiresProductionRegistration$' -count=1`
is RED: `view-off production launch left 1 stale registrations active`. It uses the actual keeper caller;
deleting that caller must break the final GREEN. Extend self/account/container transition matrix, repeated Off,
never-On Off, sibling (including dotted/undotted), persisted loggedout, unsafe leaf/ancestor links, deterministic
registry removal/lock failure, warning/status, and post-retirement publication. No real secrets or network.

**Change:** new broker retirement API using `registrationFile` + `lockedErr`; exact os.Remove under lock,
ENOENT idempotent and other errors returned. Avoid creating missing broker state. In launch helper resolve
location before selection branch; Off configures broker store and calls retirement. Error wording gives
`yolo claude-auth status` and correction/relaunch next step; successful actual retirement discloses the change.
Cheap: helper/result names and concise phrasing, fixture factoring. No new data model.

**Gates:** affected short tests in `internal/cli/run`, `internal/oauthbroker`, `internal/claudeview`;
`just format`; `python3 scripts/completion-census.py` if external-package readers change;
scoped credential-store integration and `just build-dev-bundle`/absolute launcher throwaway nested smoke
where runtime available. Record exact unavailable executor witness rather than functional success.
Root owns combined check-ci/full launch integration once; do not run either here.

**Commit:** `fix(claude): retire disabled workspace credential views`.
One coherent implementation commit after this planning commit; no amend/reset/stash/rebase/landing/push.
Report candidate SHA, exact gates, failures, durable evidence and diffstat. Leave manual tree locked.

## Review backlog — not fixed

- Add separate production-caller cases for switch Off with the broker still selected, and switch On with the broker deselected; the existing keeper case combines both causes.
- Add a live selected-view publication case: after retirement, publish a changed login and verify the retired view stays unchanged while its sibling updates. Existing post-publication coverage retires a logged-out view that publication already suppresses.
- Native macOS transition remains outstanding. The matched-launcher generic shell startup is not a Claude retirement witness; the earlier inherited-config smoke did not verify the candidate launch.


- Permanent production-caller regression plus broker contract tests; existing access-token-only sibling and logout tests retained.
- Existing status consumes removed metadata; launch success/failure disclosure is the observable surface.
- No new credential store, CLI/config surface, changelog, or owner credential/keychain operation.
- Native Mac On→Off sequence remains unexecuted here; fake-source proof is not native confirmation.
- Failed metadata removal retains registration; report it honestly, never label an unsafe-link refusal a fix.
