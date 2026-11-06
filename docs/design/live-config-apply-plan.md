---
title: "Live config apply: implementation plan"
status: accepted
stage: BUILT
next: "Keep the plan with the design until graduation; rootless M1, real-broker, remount-parity and native-Mac evidence remain outstanding"
depends-on: [live-config-apply.md]
tags: [plan, config, mounts, github, live-apply]
---

# Live config apply: implementation plan

**Status:** 2026-10-18. Live-apply code and its review repairs entered the integrated history in merge `6f7c72a25`; the inspected-mount-record repair followed in `388ea8a1b`, retaining the full `e80f3a2ae` branch history. The later `4e7b9b92b` commit changes only a documentation heading. The final bounded closure review accepted the last mount-record repair. Rootless, real-broker and `CAP_SYS_ADMIN` remount obligations below remain outstanding; this status records landed implementation, not proof of those runtime cases.
**Review disposition for candidate `286d051ef`:** Four P1 findings were repaired on the isolated
current-main branch without changing the gate, consent store, attach prompt, or backend policy.
The repair is bounded to four failure paths:

- Before each live mount mutation, invalidate the current record; if that cannot be done, refuse
  that mutation. After a mount cannot be recorded, keep `mounts` pending, stop subsequent mount
  steps, and recognize a missing or mismatched record for a current tokened jail as unknown. Old
  jails without the token remain distinct.
- Bound live-scope discovery and replacement on the broker source transaction by the existing
  apply-step deadline. A discovery error or absent verified live files can never prove a scope
  already matches.
- Let the full planner call broker scope live only after verified files have been read and
  compared with approved repositories; preserve lock/discovery errors as restart-pending results.
- Capture PID start time and mount-namespace identity before dispatch; validate under a pidfd and
  operate through the validated namespace fd before source/destination mutation. The runtime PID
  postcheck is supplementary.

Regression tests cover each path: `TestConfigApplyRecordFailureInvalidatesMountStateAndKeepsRemovalPending`
exercises injection → record-write failure → config removal; `TestConfigApplyDoesNotMutateMountWhenRecordCannotBeInvalidated`
refuses mutation when protection cannot be persisted; `TestLiveScopeTransactionsHonorContextWhileSourceLockIsHeld`
holds the broker lock across discovery and replacement; `TestApplyPlanRequiresVerifiedLiveScopeAfterApprovalIsUnchanged`
reaches the full planner with a changed boot baseline, unchanged approval, and absent/erroring live
files; and `TestInjectCtxMountRefusesChangedRuntimePIDBeforeRunningVerb` plus
`TestAttachRejectsAChangedProcessOrMountNamespaceBeforeSourceMutation` pin the pre-mutation PID
check. The direct nested-Podman path does not verify the rootless branch, a real broker request
after widening, or resistance to remounting read-only injections with the jail's `CAP_SYS_ADMIN`;
those remain M1 and CI/real-host verification, not claims made by these repairs. There is no
separate tracked live-apply integration plan; the integration case and its verification limits
are tracked under Task C below.

**Cycle 2 final repair disposition for candidate `026ab4de`:** Review found a P1 in the successful
mount path: production invalidates the host record to an empty-token tombstone before changing a
mount, then the writer rejected that tombstone and left every successful mutation restart-required.
`updateCtxMountRecord` now restores only the token captured and validated from the running jail's
inspected env, and only if the file remains the tombstone; a competing launch's token is not
replaced. `TestConfigApplyRestoresTheInvalidatedMountRecordThroughRealApply` drives production
apply through the actual invalidator, writer, reader and baseline fold while mocking only namespace
mutation. It proves add then later removal and remove then replacement-add. The competing-token
regression is `TestInvalidatedCtxMountRecordCannotClaimStaleMounts`; the existing
`TestConfigApplyRecordFailureInvalidatesMountStateAndKeepsRemovalPending` still proves failure is
restart-required and not folded. The task-owned skill,
`internal/jailcontent/builtinskills/configuring-the-jail/SKILL.md`, now describes the shared
approval gate and live mount/GitHub-scope cases without an alternate asker, a restart offer or a
new confirmation. Broker lock deadlines, pending missing/erroring discovery, and pre-pinned process
identity remain covered by `brokerscope.TestLiveScopeTransactionsHonorContextWhileSourceLockIsHeld`,
`run.TestApplyPlanRequiresVerifiedLiveScopeAfterApprovalIsUnchanged`,
`run.TestInjectCtxMountRefusesChangedRuntimePIDBeforeRunningVerb`, and
`ctxinject.TestAttachRejectsAChangedProcessOrMountNamespaceBeforeSourceMutation`; the focused
package tests pass. `just build-go` also passed, and the candidate-built `dist-go/linux-amd64/yolo`
was launched from `/tmp/yolo-nested-live-finalrepair-worker` with `YOLO_REPO_ROOT` naming this
checkout; it printed `nested-live-final-repair-ok`. The initial nested attempt was correctly refused
because the selected patched `pi` program was unavailable; the successful retry used the refusal's
`YOLO_ALLOW_MISSING_PROGRAMS=1` hatch. This is candidate-specific boot/command evidence, not a live
apply or rootless-mount test: nested podman forces `--userns=host`. Rootless two-architecture, live
broker, and remount-resistance evidence remain owned by M1 and post-landing verification.

**Final closure review for `e80f3a2ae`: ACCEPT, limited to the narrowed mount-record closure.** The
review verified that restoration accepts only the empty-token tombstone and the exact inspected
launch token; a competing launch token is left untouched. It exercised successful add → removal and
remove → replacement-add through production apply, record invalidation, token restoration, reading
and effective-baseline folding. It also confirmed that record-write failure remains pending and
is not folded, scope discovery/replacement honor their deadline, absent/erroring live files cannot
prove an unchanged scope is applied, and PID start-time plus mount-namespace identity are pinned
before source/destination mutation. The accepted tests include
`TestConfigApplyRestoresTheInvalidatedMountRecordThroughRealApply`,
`TestInvalidatedCtxMountRecordCannotClaimStaleMounts`,
`TestConfigApplyRecordFailureInvalidatesMountStateAndKeepsRemovalPending`,
`TestLiveScopeTransactionsHonorContextWhileSourceLockIsHeld`,
`TestApplyPlanRequiresVerifiedLiveScopeAfterApprovalIsUnchanged`,
`TestInjectCtxMountRefusesChangedRuntimePIDBeforeRunningVerb`, and
`TestAttachRejectsAChangedProcessOrMountNamespaceBeforeSourceMutation`. The exact workflow and
scope limits are recorded in [`october-18-integration.md`](../plans/october-18-integration.md).

**Earlier review sequence:** the first review blocked on four P1s (false completion after mount
record-write failure; unbounded broker-lock acquisition; missing/unreadable scope treated as
already applied; process identity checked after namespace mutation). The first repair review
confirmed those repairs and found a new P1: successful mount mutation invalidated its own record,
so add/remove/replacement never settled. The next repair restored only the inspected token into an
unchanged tombstone; the final closure review above accepted that narrowed fix. The closure does not
certify rootless mount capability, a real broker request after widening, or resistance to a jail's
`CAP_SYS_ADMIN` remount.

**Independent review provenance:** The initial reviewer assessed candidate
`286d051efdc5bea3fd13d451aecf0295d0d9c653` against base
`1a9ded40bd6b22a6e7f78bd3f5b4a022a4429b8c`; it returned **BLOCK** on four P1s. Its recorded
independent test command, `go test -short ./internal/brokerscope ./internal/ctxinject
./internal/ghbroker ./internal/cli/run ./internal/config ./internal/cli`, passed. The recorded
`go test -short ./integration -run '^TestLiveCtxMountInjectAndRemove$' -count=1` only compiled and
skipped the short-gated runtime test; it is not runtime mount evidence.

The repair reviewer assessed candidate `026ab4de96d6d84f259aecbcfc19d8e89f887230` and returned
**BLOCK**. It reported passing short tests for `internal/brokerscope` and `internal/ctxinject`,
a focused `internal/cli/run` selection matching
`TestConfigApply|TestApplyPlan|TestChangedBootScope|TestInjectCtxMount|TestLiveApplyClosure|TestInvalidatedCtxMountRecord`,
and `internal/cli -run TestCtxAttachVerb`. The repair report does not retain full command lines or
flags for those focused runs. Its reviewed coverage included broker lock deadlines, pending
missing/erroring-scope discovery, pre-pinned process identity, and fail-closed record-write/removal
behavior; it did not show successful mount-record recovery. A broader `internal/cli/run` invocation hit its
120-second command deadline and is **not** a pass. That review found the successful-mount record
lifecycle still broken: fail-closed record-write handling did not restore the token after a
successful mutation. Thus it did not establish that ordinary add/remove/replacement worked.

The final reviewer assessed the exact range
`026ab4de96d6d84f259aecbcfc19d8e89f887230..e80f3a2ae3acc3724b83aaa66b207edd9cbd18f4`, verified
ancestry and a clean candidate, and returned **OK only for the narrowed mount-record closure**—not a
renewed broad review. Its report records a focused four-package run with
`-short -count=1 -timeout=90s`, verbose targeted executions of the lifecycle, token, failure-pending
and skill regressions, the retained deadline/scope/identity regressions, and
`git diff --check 026ab4de HEAD`; these passed. The report does not retain the four package names or
the full verbose-command strings, so neither is inferred here. The separately recorded combined
landing gate is not attributed to this review. A later rootful probe, also outside these reviews,
remounted both launch-time and injected read-only binds writable with effective `CAP_SYS_ADMIN`;
that is a parity result, not remount resistance. No review here accepted rootless behavior,
real-broker widening, or remount resistance; rootless runs on both architectures remain outstanding.
The review dispositions are also summarized
in [`october-18-integration.md`](../plans/october-18-integration.md).

**Design:** [`live-config-apply.md`](live-config-apply.md), every question ruled
([Decision Ledger](live-config-apply.md#decision-ledger)).

Precedence: the design wins on behavior, the tree wins on fact (cosmetic drift only), this file is
advice and is the first thing to be wrong. **Stop and report; do not commit** when a test does not
fail the way a task predicts, a file a task names is dirty with someone else's work, a symbol a task
names is gone, the fix needs a file outside the task's fence, or a gate fails outside the task's
scope.

## Map

| Path | Lane | Change |
| :--- | :--- | :--- |
| `internal/cli/run/liveapply.go` | A | new: `ConfigApply` entry, the sequence of [design §3.2](live-config-apply.md#32-the-sequence), attach entry, report, exit codes |
| `internal/cli/run/liveapplyplan.go` | A | new: per-key, per-backend outcome table ([design §4](live-config-apply.md#4-which-changes-can-reach-a-running-jail)), pure |
| `internal/cli/run/preflight.go` | A | `changePrompter` gains pre-question lines and an attach decline variant |
| `internal/cli/run/run.go` | A | one call in `attachExisting`, after the session count, before `refreshJailBriefings` (~`:2955`) |
| `internal/cli/config.go` | A | register `apply` in `configColorVerbs`, `notchlessVerbs`, `configHelpToken`, usage text |
| `internal/config/drift.go` | A | `FoldBootBaselineKey(workspace, key, value)`; drift labels say "config this jail has" |
| `internal/brokerscope/file.go` (or new `live.go`) | B | `LiveScope` field, `LiveFiles`, `ReplaceRepos` |
| `internal/ghbroker/daemon.go`, `authority.go` | B | classify against the re-read file; write the capability at bind; refusal names `yolo config apply` |
| `internal/ctxinject/` | C | new package: the five-step injector and removal, Linux only, a darwin stub |
| `internal/cli/internalctxattach.go`, `internal/cli/internal.go` | C | new hidden verb `yolo internal ctx-attach` |
| `internal/cli/run/ctxmountrecord.go` | C | new: host-side record of the config mounts a jail has; diff against desired |
| `internal/cli/run/ctxinjectdrive.go` | C | new: host driver choosing direct vs `podman unshare`, pid lookup, deadline |
| `internal/cli/run/assemble.go`, `contracttags.go` | C | `/ctx` tmpfs flag, `live-ctx-mounts` tag, record token env |
| `integration/livectxmount_test.go` | C | new: inject, read, write refused, remove; runs in CI rootless jobs |

## Contracts between lanes (binding)

Lane A codes against these and stubs them with package-level function variables until task 4
wires the real ones. Lanes B and C implement them with exactly these signatures.

```go
// lane B, package brokerscope
type LiveFile struct {
	Path string
	File File // as read, identity fields included
}
// LiveFiles: every scope file of source whose Container == container and Workspace == workspace,
// whose launching PID and BrokerPID are alive. Read under brokerlock.WithTransaction.
func LiveFiles(source, container, workspace string) ([]LiveFile, error)
// ReplaceRepos rewrites Repos only, under the source's transaction, refusing when the file's
// identity fields differ from expect's. Every other field, broker_* and live_scope included, is kept.
func ReplaceRepos(path string, expect File, repos []string) error
// File gains: LiveScope bool `json:"live_scope,omitempty"` — set by BindBroker for this build.

// lane C, package run
type ctxMountEntry struct{ Source, Dest string; RW bool }
func (o *Options) ctxMountsLive(rt, cname string, envLines []string) (ok bool, reason string)
func readCtxMountRecord(cname string, envLines []string) ([]ctxMountEntry, bool)
func writeCtxMountRecord(cname, token string, entries []ctxMountEntry) error
func diffCtxMounts(have []ctxMountEntry, want []configCtxMount) (add []configCtxMount, remove []ctxMountEntry)
func (o *Options) injectCtxMount(rt, cname string, m configCtxMount) error // 10 s deadline inside
func (o *Options) removeCtxMount(rt, cname string, e ctxMountEntry) error
```

`reason` strings are the outcome line's words, for example `this jail started before live mounts
existed`, `Apple Container shares are fixed when its VM boots`, `the Podman Machine path is not built
yet`.

## Reuse

- Gate: `(*Options).checkConfigChanges` (`preflight.go:416`) is the launch's gate. Lane A calls it,
  or a sibling that shares its body, so the read (`config.ReadWorkspaceForGate`), the scope
  (`brokeredScopeCheck`), the prompter and `recordApprovedScopes` are one code path. The approved
  scope is in `o.approvedScopes` afterwards, per loophole name.
- Config and runtime: `loadAndValidateConfig` (`preflight.go:23`), `resolveRuntime`
  (`preflight.go:198`), `runtime.FromWorkspace`, `NewDefaultOptions` + `fillDefaults`.
- Lock: `holdLaunchLock(cname)` / `releaseLaunchLock` (`flock.go`). An attach already holds it at
  the hook point.
- Probe: `probeRunningContainer` (`lifecycle.go:96`), tri-state. Env: `inspectContainerEnv`
  (`run.go:3358`).
- Mount desire: `configCtxMounts(rt, cfg, note)` (`ctxmounts.go:54`), the launch's own reading;
  `rwMountDisclosure` for the read-write line; `rootfulPodmanHost` and `o.podmanFacts` for the branch.
- Scope disclosure lines: `writeScopeFiles` (`brokeredscope.go:79`) shows the shape of the
  per-repository line.
- Host record location: mirror `jailgrant.go`'s `paths.AgentsDir()/<cname>/grant/` (outside every
  bound dir); use `paths.AgentsDir()/<cname>/ctx-mounts/record.json`.
- Internal verb: mirror `internalimagecopy.go` (flag set, usage, exit 2 on bad args).
- Syscalls: `unix.OpenTree`, `unix.MountSetattr`, `unix.MoveMount`, `unix.Openat2`, `unix.Setns`,
  `unix.Unshare` are vendored in `golang.org/x/sys/unix`.

## Traps

- `configCtxMounts` reads read-write elements from user scope (`config.LoadRWMounts`), not from
  `cfg`, and they are absent from `.yolo/config-boot.json`. So mount state comes from the host
  record, never from the boot baseline.
- `brokerscope.Write` drops `broker_*` fields; never use it to rewrite a live file.
- `daemon.go:435` classifies against `b.scope` before the authority is consulted, so a widened file
  is never seen unless classification moves to the re-read scope.
- A Go process is multi-threaded: `runtime.LockOSThread()` then `unix.Unshare(CLONE_FS)` before
  `Setns(CLONE_NEWNS)`, or `EINVAL`.
- The decline lines say "Exiting." An attach that is declined does not exit (LA-D14).
- A nested jail runs podman with `--userns host` and reports rootful: the direct branch. It proves
  nothing about rootless; only CI's rootless jobs do (AGENTS.md carve-out).
- `launchContractTags` is shared by every container backend; Apple Container must still report
  *at next restart* for mounts (the backend check comes before the tag).
- `/ctx` must stay read-only to the jail after the tmpfs change: `ro=true` on the tmpfs.

## Tasks

### Task A: the command, the plan table and the attach gate (independent)

- **Settled, do not re-open:** [design §3](live-config-apply.md#3-the-command) and [§6](live-config-apply.md#6-outcomes-and-what-each-party-sees). `yolo config apply [--record-only]
  [--accept-config-changes]`, host only (in a jail: refuse naming the host command, exit 1). Lock
  for the whole run. Probe: not running → record only, say the next `yolo` launches with the
  changes; could not ask → record only, name the runtime error. Gate exactly the launch's. N →
  nothing recorded, exit 1. Outcome block printed before the question (after the diff) and after.
  Exit 0 nothing pending, 3 restart owed, 1 declined/refused/failed before record. Never offer a
  restart; print `yolo stop`, then `yolo`. Attach: stdin a terminal (`o.IsTTYStdin()`) or
  `--accept-config-changes` → same sequence, N continues into the session with a decline that names
  `yolo config apply`; otherwise one notice line naming `yolo config apply` and `yolo config drift`
  when the boot baseline drifts or the approval record differs. A failed step never fails an attach.
- **Outcome table (liveapplyplan.go):** `mounts` → applied now when `ctxMountsLive` is true, else
  next restart with its reason; macos-user → next session. `brokered` and scope → applied now when
  every live broker file has `LiveScope`, next restart otherwise; Apple Container → next restart
  ("no broker runs in this jail"). `env_sources`, `providers`, `profiles`, `profile`,
  `model_facts`, `adapters` → next session on every backend. `agents_md_extra`,
  `briefing_provenance` → next session, Apple Container next restart. Every other key → next
  restart; on macos-user every key → next session.
- **Effective record:** after each successful step, `config.FoldBootBaselineKey` replaces the one
  top-level key in `.yolo/config-boot.json` with the current workspace value (or deletes it). Fold
  next-session keys at once. A key with any failed element is not folded. macos-user writes none.
- **Tests first** (`internal/cli/run/liveapply_test.go`): the gate is the launch's (delete the call
  → a test fails: record bytes equal a launch's for the same fixture); N exits 1 and writes nothing;
  not running → record only, exit 3 when a restart key changed; `network.ports` change → `at next
  restart` line naming `yolo stop`; in-jail refusal; attach non-TTY prints the notice and does not
  prompt; attach TTY + N starts the session; plan table cases for every row above.
- **Gates:** `go test ./internal/cli/run/ ./internal/cli/ ./internal/config/ -run 'Apply|Drift|Config'`,
  then `just test-fast`.
- **Fences:** nothing in `internal/ghbroker`, `internal/brokerscope`, `internal/ctxinject`,
  `assemble.go`, `contracttags.go`, `agentenvfiles.go`. In `run.go`, only the one hook.
- **Commit:** `feat(config): yolo config apply and the attach-time gate`.

### Task B: live GitHub scope (independent)

- **Settled, do not re-open:** [design §7](live-config-apply.md#7-github-scope-live). The broker classifies every request against the scope
  file it re-reads; the apply rewrites `Repos` only under the broker transaction, keeping identity
  fields; the broker advertises the capability with a field written at bind. A removed repository
  is refused at its next request. The out-of-scope refusal says to ask the user to run
  `yolo config apply` on the host where the broker takes live scope.
- **Tests first:** in `internal/ghbroker`: widen the file through `ReplaceRepos` and see a request
  for the new repository admitted; narrow and see it refused; the bound file carries
  `live_scope: true`. In `internal/brokerscope`: `ReplaceRepos` keeps `broker_start`, `broker_pid`,
  `live_scope`; refuses a changed identity; `LiveFiles` skips a dead PID and another container.
- **Gates:** `go test ./internal/ghbroker/ ./internal/brokerscope/`, then `just test-fast`.
- **Fences:** only `internal/ghbroker`, `internal/brokerscope`.
- **Commit:** `feat(ghbroker): classify against the live scope file`.

### Task C: mount injection on podman (independent)

- **Settled, do not re-open:** [design §5](live-config-apply.md#5-mount-injection-per-backend). Five steps: `open_tree(source, CLONE|RECURSIVE)`,
  `mount_setattr(RECURSIVE, RDONLY)` unless read-write, `setns` into the jail's mount namespace,
  `mkdirat` through a private writable clone of `/ctx` after checking `/ctx` is tmpfs, `move_mount`.
  Destination opened with `RESOLVE_NO_SYMLINKS|RESOLVE_NO_MAGICLINKS`. Removal: `setns`, `umount2
  MNT_DETACH`, `rmdir` through the clone; an empty mountpoint whose move failed is removed. The
  injector refuses when `/ctx` is not a tmpfs. Rootless: run the verb under `podman unshare`;
  rootful (including nested): directly; macOS Podman Machine and Apple Container: not built, *at
  next restart* with a reason. Launch: `--mount type=tmpfs,destination=/ctx,ro=true` on podman only,
  the `live-ctx-mounts` tag, a random token in `YOLO_CTX_MOUNTS_TOKEN`, and the record of the config
  mounts this launch bound, written with that token. `readCtxMountRecord` answers false when the
  token in the jail's env differs.
- **Tests first:** unit: `diffCtxMounts` (add, remove, source change is remove+add, mode change is
  remove+add); record round trip and token mismatch; argv carries the tmpfs flag on podman, not on
  Apple Container. Integration (`integration/livectxmount_test.go`, `requireJail`): launch a jail,
  capture its PID start-time and mount-namespace identity, then pass that pin to each hidden
  injector invocation; inject a read-only directory, read a file inside, writing it fails with
  EROFS, `mkdir /ctx/x` fails, remove it and it is gone; repeat read-write and write succeeds. The
  test logs `podman info --format '{{.Host.Security.Rootless}}'`.
- **Gates:** focused broker-lock, planner, process-pin, and record-failure regression tests; then
  `go test -count=1 -run LiveCtx ./integration` in a fresh nested workspace. Do not run the full
  slow suite for this repair. The nested run proves only the direct rootful branch: it does not
  verify rootless `podman unshare`, a real broker request, or a jail `CAP_SYS_ADMIN` remount of a
  read-only injection. Rootless CI/real-host verification remains M1.
- **Fences:** no `liveapply*.go`, `preflight.go`, `config.go`, `internal/ghbroker`.
- **Commit:** `feat(run): inject context mounts into a running podman jail`.

### Task 4: integration (after A, B, C; the lead)

Merge B, C, then A; replace A's stubs with the real functions; run `just check-ci`, the
integration tests the change reaches, a nested-jail launch and attach from `/tmp`; update
`CHANGELOG.md`, `docs/reference/config-safety.md` ("An attached session's config is frozen until
restart" is now false), `docs/reference/context-mounts.md`, the design's Built column.

## Ships with

- Docs: `docs/reference/config-safety.md`, `docs/reference/context-mounts.md`,
  [`workspace-widening.md` §3.5](workspace-widening.md#35-what-restart-means)'s claim that an attach is silent, `yolo config --help`,
  `CHANGELOG.md` `[Unreleased]`.
- Tests that will break and should be rewritten to the new behavior: any argv golden that lists the
  podman run flags (the tmpfs flag joins it); any assertion that the attach prints nothing about
  config changes.

## Don't

- Don't add a second approval path or a second consent store. The record is the approval snapshot.
- Don't read anything from inside the jail to decide a source (design P5).
- Don't build the later slices (`LA-S4b` to `LA-S4d`) or the macOS Podman Machine injector in this
  plan's scope. `LA-S4a` was built on its own, 2026-11-06 (design [LA-D18](live-config-apply.md#LA-D18)
  to [LA-D22](live-config-apply.md#LA-D22)).
