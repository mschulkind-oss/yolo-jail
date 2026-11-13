---
title: "Combined preparation: implementation map and remaining verification"
status: accepted
stage: DECIDED
next: "Real rootless-host and native Darwin acceptance, the landing gate with the slow suite, and the runtime gaps the 2026-11-13 record names"
---

# Combined preparation: implementation map and remaining verification

**Status:** 2026-11-13. The combined candidate connects the independent demand worker,
source freshness, timer, host consumers, Apple checks and ordinary npm captures. The
independent review ran on 2026-11-12 and its defects are fixed. A fresh non-agent nested launch
exercised the ordinary npm route on 2026-11-13 ([the record](#runtime-2026-11-13)). Real
rootless and native macOS behavior remain unproven; this plan is not a shipped claim.

**Design:** [preparation contracts](launch-update-preparation.md). The design wins on behavior;
the tree wins on fact. This map describes the selected implementation, not another registry.
Ordinary CLI/MCP producer details are in [Ordinary CLI and MCP capture](#ordinary-cli-and-mcp-capture).

## Source map

| Component | Source | Constraint |
| :--- | :--- | :--- |
| Timer and selection | [scheduler](../../internal/cli/preparationscheduler.go), [state](../../internal/cli/preparationstate.go) | One scheduler state; per-key generation and retained diagnostic evidence are not admission. |
| Authenticated lifecycle | [control](../../internal/cli/preparationcontrol.go), [publication](../../internal/cli/preparationpublication.go) | Random lifetime identity and kernel lease; unknown ownership neither admits nor destroys. File sync, rename and directory sync must succeed. |
| Independent production | [shared demand](../../internal/cli/shareddemand.go) | Requester leases attach to the same worker. Losing one requester cannot cancel surviving demand. Workspace-only declarations remain foreground. |
| Source request boundaries | [request identity](../../internal/cli/preparationrequest.go), [short ledger](../../internal/packsrc/checkstart.go) | An epoch and sequence identify a check start after registration. Recreated ledgers and older in-flight checks cannot satisfy freshness. |
| Plain pin publication | [plain requests](../../internal/cli/preparationplainrequest.go), [fork lock](../../internal/packsrc/forklock.go) | The independent worker publishes under the fork lock and current freshness authority. Timers never move a standing pin. |
| Actual launch slot | [build pool](../../internal/cli/buildpool.go), [launch slot](../../internal/cli/run/buildslot.go) | Registration boundaries precede pooled work, below attach decisions. Exact unchanged artifacts reuse their admission. |
| Host consumption | [host trees](../../internal/cli/hosttrees.go), [floor](../../internal/cli/hostfloor.go), [readiness](../../internal/cli/hostreadiness.go) | Exact recorded entry and full selected declaration; independent installed copies and unknown-session retention. |
| Apple capability | [checks](../../internal/cli/preparationapple.go), [listing](../../internal/cli/applebuildcapability.go) | Linux timer checks only. Exact reuse precedes required-build admission; malformed or unavailable listing never proves absence. |
| Ordinary npm | [npm adapter](../../internal/cli/programnpm.go), [demand](../../internal/cli/programcapturedemand.go) | The selected sealed producer joins this worker, not a second builder. Unsupported/customized runtime inputs retain their bounded declared route. |
| Reporting | [status](../../internal/cli/preparationstatus.go), [phase timing](../../internal/cli/buildreport.go), [log append](../../internal/cli/logsink.go) | Read-only diagnostic status; real phase spans and confined locked appends at their production callers. |

## Invariants and traps

- Source freshness is an epoch/sequence boundary with current guarded publication, not proof from
  historical sequence numbers. Ordinary npm has its separate check sequence.
- A source requester never becomes the production owner. Cancellation detaches its lease;
  positively observed zero demand cancels the owned child. Unknown demand retains the child but
  cannot authorize admission. Teardown uses the original backend and a bounded cleanup context.
- Scheduler control uses an authenticated random incarnation, never a PID, executable digest,
  wake file or stop file as owner authority. Replacement drains before releasing its lease.
- A retained workspace's original ownership is checked before asking whether a new Apple build
  may start. Runtime admission cannot retarget retained evidence to the current backend.
- Freshness is not force-rebuild. Explicit failure cannot deliver historical success;
  [PF-D81](../reference/patched-forks.md#PF-D81) strict failure and
  [PF-D82](../reference/patched-forks.md#PF-D82) ordinary disclosed historical recovery remain distinct.
- A generated warm launcher validates its actual runtime before marker success or exec. This
  validation cannot install, materialize, mutate receipts or run a registry lookup.
- In a test of host exec, install the exec-capture fixture before calling the real front door.
  Otherwise syscall exec can replace the test process and falsely finish the suite with zero.
- Add new files to the reviewed index before Nix verification; its fileset does not see untracked
  source. Regenerate the completion-reader census after source changes.

## Ordinary CLI and MCP capture

The machine-level preparation of ordinary CLI and yolo-installed MCP programs. The combined candidate connects the selected sealed npm producer,
launchers, MCP refresh and HOST consumers through the independent shared-demand worker.
Permanent synthetic producer/consumer tests are present. **Design:**
[preparation boundary](launch-update-preparation.md) and
[OQ-PD23](program-delivery.md#decision-ledger). No new owner ruling is needed.

### Settled scope — do not reopen

Ordinary CLI and yolo-installed MCP programs prepare once per machine per compatible resolved
input, through repeatable sealed credential-free producers, and share immutable admitted
artifacts across workspaces. No universal cross-OS binary, captured login/config/state,
credential widening, agent registry or second builder. Explicit pins/version constraints,
strict patch failures, historical cached-good recovery and running-session stability survive.
Unsupported legacy installers retain their honest bounded existing route.

Scheduler, tree, patched-program, recorded plain-pin, host next-launch and Apple check-only
work proceeds through the [source map](#source-map) above.

### Implemented independent contracts

| Source | Contract / constraint |
| :--- | :--- |
| [`capture.ProgramInputs.Identity`](../../internal/capture/programinputs.go) | Hashes selected owner/bin/declaration/resolver, resolved release/source, recipe, dependency set, runtime/toolchain identity, platform and applicable observed Node ABI. No workspace path or universal floor is an input. |
| [`CaptureReceipt`](../../internal/entrypoint/capturereceipt.go), [`captureRecords`](../../internal/cli/capturematerialize.go) | Additive ordinary compatibility identity through the existing receipt writer/reader/adapter. Old receipts still answer their established empty-identity route; they cannot prove a new resolved identity. |
| [`capture.Select`](../../internal/capture/select.go), [`capture.PruneSupersededCaptures`](../../internal/capture/gc.go) | Exact ordinary identities are separate selection keys; prune remains the complement of the reader and keeps each reachable identity's winner. Materialized copies need no session registry. |
| [`capture.Run`](../../internal/capture/inner.go), [`artifactSurfaces` / `ValidateArtifacts`](../../internal/capture/artifacts.go) | Opt-in artifact roots, not whole prefixes/home. Reject traversal, overlaps, yolo state, unselected/outside links, dangling/cyclic producer links, special files and unsafe permissions. Installer env is allowlisted with capture-private prefixes. Linux artifact mode uses an isolated same-binary subreaper and pinned no-follow descriptor copies; cleanup proof precedes validation. Unsupported artifact platforms refuse only this new route. Legacy nil-artifact capture is unchanged. |
| [`runCaptureRun`](../../internal/cli/capture.go) | Repeated hidden `--artifact=HOME-RELATIVE-PATH` arguments reach the real driver. This mode refuses a missing sealed-build marker before executing the installer. **A marker is not sandbox evidence**: the host producer must actually use the seal. |
| [`Store.AdmitEntryGuarded` / `AdmitEntryValidated`](../../internal/capture/store.go) | Digest/freeze and keyed CAS contention happen outside the caller's short selected-input publication guard; rename/completion happen within it. Identical bytes produced by different input identities serialize at the existing store. The validated route checks the retained canonical manifest before publication; incompatible metadata refuses without changing existing metadata or receipts. |
| [`prepareProgramCapture`](../../internal/cli/programcapture.go) | Existing receipt selection, pidlock, staging, immutable admission and original-runtime/keeper liveness proof. Same-identity waiters reread the winner; unchanged identity skips production. Cancellation/stale authority admits nothing; unknown runtime retains staging. The selected npm adapter supplies the producer callback; unsupported inputs retain their declared installer. |
| [`materializeCapture`](../../internal/cli/capturematerialize.go) | Hidden `--compatibility=IDENTITY` selects exact ordinary bytes and preserves that identity in the materialize receipt. Existing fork `--key` and legacy native selection stay separate. Existing relocation/confinement machinery places independent copies. |

The compatibility tuple must contain **resolved facts**. Passing a declaration such as `latest`
as its resolved release, a backend name as its complete runtime/toolchain identity, or an unapplied
MCP list as its dependency vector does not meet this contract. The selected adapter must also
include output/refresh instruction changes in its recipe identity.

### Stable component handoff contract

- **Driver:** hidden `capture-run --home=ABS --out=ENTRY --scan-content-refs`
  with repeated `--artifact=HOME-RELATIVE-PATH`, followed by `-- INSTALLER ARGV`.
  The selected producer must use an actual non-nil seal and supply the sealed-build marker;
  the marker alone remains an accidental-use assertion. Config/login roots, whole shared
  install prefixes, unselected link targets and yolo state are excluded/refused.
- **Ownership:** Linux artifact mode self-execs the existing binary's hidden
  `capture-installer-supervise` helper. Only that helper enables subreaping. Its lifetime pipe
  closes on driver cancellation/death; after direct installer exit or cancellation it kills
  only kernel-reported direct children, including successive adopted escaped/session-changing
  generations, and reaps to `ECHILD`. Cleanup has a three-second bound. Cancellation also bounds a stopped helper: the driver
  waits four seconds for cleanup, then kills only its own helper and refuses without
  descendant-completion proof; the owned sealed runtime must finish teardown. Missing ownership,
  failed installer, missing completion evidence or unreaped descendants refuse before transfer;
  stop the owned sealed runtime and retry. No global subreaper or broad process kill exists.
  Unsupported platforms retain legacy/native installation and refuse only new artifact capture,
  naming the supported sealed Linux route.
- **Transfer:** pin HOME before installation; open every source ancestor and regular file
  fd-relative with no-follow checks, and read links without following them. Artifact mode
  copies changed leaves from pinned descriptors into the private output tree, even on one
  mount. It does not use the legacy rename optimization or pathname source cleanup/chown;
  its throwaway runtime owns source cleanup. Link existence/cycle checks use captured entries,
  not installer-controlled paths. Descriptor confinement remains necessary even after reaping.
- **Admission:** `prepareProgramCapture` receives resolved `ProgramInputs`, exact roots,
  producer callback and short publication guard. Before attaching identity to same-byte CAS,
  `AdmitEntryValidated` validates the retained canonical manifest under the content lock, outside
  the publication guard: platform, selected roots/entries, full relocation scan, relocation
  disposition/reference instructions, and original home when not relocatable. It never upgrades
  old canonical metadata. Incompatible reuse returns an error, publishes no new receipt, and
  leaves legacy metadata/receipts intact. Warm exact winners also validate canonical metadata.
  Different ABI identities with genuinely compatible metadata may still share one CAS entry.
- **Boundary:** ownership cleanup and descriptor reads do not seal a producer. The selected
  adapter below must call the actual empty-pack seal; its caller and assembled-runtime negatives
  are separate acceptance evidence from the artifact-driver tests.

### Selected npm production mechanics

**Constraint:** supported npm installation inputs contain only owner, bin, package selector and
Node floor. Custom install flags, source/native programs and vendor self-update recipes retain
their existing bounded route. MCP contributes only packages of effective enabled entries; never
server args, env or credential-bearing tables. Empty vectors do nothing.

**Advice:** resolve a package lock with scripts disabled in an actual sealed empty-pack runtime,
observing Node version/ABI, npm version, platform and image identity there. Persist the lock's
resolved version, source/integrity and complete dependency graph as immutable resolution facts.
The selected floor is satisfied through the existing mise toolchain, never a guessed ABI.
A second sealed runtime installs with `npm ci` only if exact admission has no winner. Validate
its observed runtime against the resolution before scripts run. Both phases use the existing
owned child/stream/cancellation lifecycle and original-runtime known-gone witness.

**Constraint:** the production call to `runCaptureJail` has a non-nil seal and an explicitly empty
pack selection. No selected pack manifest, hook, provider or sibling program crosses; only the
installation input file enters the scratch workspace. The existing seal withholds host config,
credential channel, services, grants, MCP tables and shared caches from the first runtime start.
Use the hidden artifact driver for the install phase, full relocation scan and exact roots.

**Advice:** one declaration-keyed resolution record stores the latest successful resolved facts
and started-check sequence. Normal warm delivery reads that record and exact receipts without
runtime/registry work; fresh requests register before checking and require a later sequence.
Keep check/entry exclusion separate from short publication transactions. Runtime recipe changes
invalidate the record. Guard declaration/runtime identity again immediately before publication.
Failed/cancelled manual resolution never turns old-good bytes into successful freshness.

**Constraint:** exact per-launch compatibility decisions feed generated launchers, readiness,
enabled MCP materialization and the host's actual declared owner/applied MCP vector. Independent
identity-named payload directories keep running programs stable; only the next launch/apply
moves entry links. Timer work prepares immutable bytes and never materializes a home or settings.
Workspace-only declarations the user resolver cannot authorize retain the real foreground route.
Shared ownership extends the committed shared-demand worker/result protocol with an
`ordinary-program` discriminator and exact ordinary result. It does not mint a source-fork
request, carry a manifest/executable path, or create a second worker, monitor, registry or pool.
The existing unique requester/kernel lease, detached worker, current user resolver, timer lease
and final no-wait demand/subscription guards remain the owners. Short metadata contention is
retried outside those locks without repeating resolution or installation; cancellation and
revocation still forbid success. Scheduler dispatch lives in the existing persistent loop.

**Constraint:** sealed reuse supports only the independently matching **baseline** runtime.
The image marker identifies the base flake, not `packages:` or store-delivered loader variants.
Customized packages, configured Node toolchains, loader/tool overrides in `env_sources` or the
composed channel, and file-backed env sources whose runtime effect is not established use the
existing authorized bounded installers. They are disclosed, not refused or silently dropped;
`--update` must still finish its qualifying legacy check or fail before the target executes.
Do not widen the empty-config seal to inherit caller packages or credentials to claim equality.

**Constraint:** the HOST floor selects a capture only after its own Node binary/version/ABI,
npm, platform and actual baseline identity match every producer fact. Missing baseline identity,
custom loader environment and unsupported Darwin artifact mode retain the real host installer,
its freshness/wait/cancellation and fixed-selector rules. No new native producer is required.
The compatible route holds one exact decision through floor publication, keeps independent
copies, and can consume a timer-prepared generation without a mutable registry poll.

#### Source and test map

- New selected producer/inner npm driver under `internal/cli`; dispatch in the existing hidden
  internal command table. Reuse `prepareProgramCapture`, validated CAS admission, exact receipts,
  materializer and the existing capture-child lifecycle.
- `programcapturedemand.go` resolves installation authority from current user selection and
  adapts ordinary requests/results to `shareddemand.go`; `programcapturescheduler.go` adds
  ordinary records/dispatch to `preparationscheduler.go`, never another timer loop.
- `programcapturehost.go` wires compatibility-gated ordinary reuse into the actual host floor;
  its independent bounded installer remains the fallback, including unsupported platforms.
- Public run and HOST tests in `programcapturedemand_test.go`, `programcapturehost_test.go` and
  `programcaptureruntime_test.go` exercise real callbacks/consumers with private synthetic npm
  fixtures and an observed installed Node. `programcapturelifetime_test.go` tests the actual
  shared worker's second-demand retention, zero-demand teardown and selection revocation;
  `programcapturescheduler_test.go` drives the existing production timer and checks running-home
  stability. Generated readiness/MCP/bootstrap consumers use the actual hidden materializer.
- Fresh container preparation callsite in `internal/cli/run`, wired only by the normal CLI front
  door. The callback returns exact compatibility decisions, never copies or a global generation.
- Entrypoint generated npm launchers and enabled-server refresh read those exact decisions;
  legacy capture/fork selection stays separate. Host floor resolves its declared ordinary owner.
- Permanent synthetic tests must reach these production callers: cold/warm cross-workspace reuse,
  lock/runtime/ABI conflicts, fresh newer registry/fixed pins, cancellation and stale guard,
  running-generation stability, and actual assembled seal mount/config/env/input negatives.
  Test registries/installers are local fixtures; no agent execution or provider API calls.

**Gates:** targeted caller tests, affected short suites, vet, Linux build and Darwin compile,
format/diff check and document check. Combined gates, pins, census and nested/native acceptance
remain once-only landing-owner work. Preserve actual unfinished source outside the disposable tree.

### Candidate implementation checklist

The selected adapter now supplies the real non-nil seal and empty staged pack tree, resolved
npm lock/runtime facts, original-runtime witness, STARTED boundaries and exact admission.
Public run/host callbacks, generated readiness/MCP/bootstrap materialization, generic shared
demand dispatch and ordinary records in the persistent scheduler are wired in this candidate.
The source/test map above names those callsites; this is not acceptance of a live container.
Legacy `yolo capture` still rejects npm and retains its nil-artifact installer semantics.
Unsupported runtime/installer routes keep their existing bounded checks and fixed constraints.
The seal still withholds MCP/server env/args; timer work admits bytes without rendering homes,
and an attach keeps its immutable running-launch vector.

### Verification and fences

Permanent local synthetic fixtures reach hidden driver/materializer dispatch and the actual
admission adapter: excluded secrets/state, two workspace copies, exact input isolation and prune,
concurrent same-identity reuse, distinct identities sharing identical CAS bytes, cancelled demand,
stale publication rejection and unknown-runtime staging retention. Inputs tests cover release,
recipe/dependencies, source, declaration, runtime, platform and Node ABI mismatches. Security
regressions cover successful survivors, immediate double-fork/new-session children, escaped
cancellation, missing helper evidence, parent swaps with excluded regular files, incompatible
Linux/Darwin same-byte reuse, historical symlink-only metadata and retained relocation instructions.

Targeted commands: `go test -short ./internal/capture ./internal/entrypoint ./internal/cli`,
`go test -race -short ./internal/capture ./internal/cli -run 'TestProgram'`,
`go vet ./internal/capture ./internal/entrypoint ./internal/cli`, and a build of `./cmd/yolo`.
These establish local behavior, **not** a sealed runtime launch or capture replacement at readiness.

The ordinary adapter extends only the generic shared-demand dispatch and scheduler record kind.
Independent source-fork, host/Apple/liveness repairs retain their committed owning-task behavior
and permanent tests. Existing
[PF-D81 / PF-D82](../reference/patched-forks.md#why-its-this-way) authority is untouched; no new compatibility
field is required of their historically valid receipts.

## Remaining acceptance

<a id="review-2026-11-12"></a>**The independent review, 2026-11-12.** Four reviewers read the combined
source: the scheduler, control tick and plain pins; shared demand and the capture store; the
launch-side update path; and the HOST, launcher and MCP consumers. Each defect below got a
failing test through its production caller, then the fix:

| Defect | Fix |
| :--- | :--- |
| A timer plain admission held the scheduler state lock through the whole store admission, including a wait of up to 20 minutes on the entry lock, stalling every launch's registration | `8c3642879`: only the rename and completion run under the guard |
| Resolver lock contention on an ordinary program was recorded as a failure with an hour's retry | `8c3642879`: it is busy, with no retry time |
| Every one-second control tick hashed the whole yolo executable | `be900065f`: it re-hashes only when the file's stat identity changes |
| A plain build left at `building` kept saying so after its worker stopped (minor) | `c59758e65`: it is relabeled interrupted |
| The host floor deleted complete older program versions after each install, with no liveness knowledge, and a deselecting apply removed them all | `0c7c10b71`: complete versions are retained, as host trees are |
| The host floor could publish a capture proven against the installed record's Node on the newly shipped Node | `0c7c10b71`: the decision is made with the Node the install uses |
| One workspace's launch deactivated a machine-wide ordinary subscription its own config lacked | `c8d8e9cbb`: only the user resolver revokes one |
| A successful capture leaked its producer workspace and jail home | `c8d8e9cbb`: they are reclaimed under the known-gone witness, and kept when liveness is unknown |

<a id="runtime-2026-11-13"></a>**The nested runtime run, 2026-11-13.** A fresh dev bundle (`just build-dev-bundle`)
was launched by absolute path from a throwaway workspace with a private `HOME` and a scrubbed
environment, all under the lane's durable scratch directory, never the live workspace home.
The user config selected only the shipped `chrome-devtools` pack, whose
`chrome-devtools-mcp` is an ordinary npm program, under `agent_updates = "next-launch"`. Every
command was non-agent: `true`, `sleep`, and one `chrome-devtools-mcp --version` probe. The
outer jail's Podman is rootful (`podman info --format '{{.Host.Security.Rootless}}'` printed
`false`), and the nested jail shares its network.

| Check | Observed | Result |
| :--- | :--- | :--- |
| Cold launch | The launch waited on the shared worker, which ran a sealed resolve jail and a sealed install jail. The launch materialized the admitted capture and exited 0 in 8 to 27 seconds across three runs | pass |
| Warm launch | No wait and no preparation line; 1.3 to 2.3 seconds | pass |
| Launch materialization | `~/.npm-global/bin/chrome-devtools-mcp` links into an identity-named payload directory under `~/.local/lib/yolo-npm/`. The probe printed `1.13.0`, the release the resolution recorded | pass |
| Timer progress without a launch | After an explicit stop, the resolution record's `CheckedAt` was moved back two hours to make it due. `yolo pack prepare start` then ran a sealed resolve with no workspace jail running. The check sequence advanced and status moved to `unchanged` | pass; the due time was simulated, and no newer release existed to admit |
| Binary replacement | A launch by a newer bundle replaced the older bundle's worker with a new incarnation | pass |
| `--update` | Each run did a fresh check (the sequence advanced) and reused the unchanged admitted entry, with no rebuild. The in-jail refresh ran before the target | pass; the `⚡ Executing` line prints before that refresh |
| `--update` on attach | Refused, naming `yolo stop` then a fresh launch. An ordinary attach still entered | pass |
| `agent_updates = false` | The launch reused the admitted entry, made no check, and deactivated the subscription. `yolo pack prepare start` started nothing, while `--update` still ran a fresh check | pass after fixes |
| Policy back on | The command the status names re-enabled it, and the next launch re-registered it as pending work | pass after fixes |
| Status output | Each record shows its phase, admitted entry, update time and log. The worker line shows the incarnation, or says it is not running and names the next step | pass after fixes |

The run found four defects. Each got a failing test first, then the fix:

| Defect | Fix and its test |
| :--- | :--- |
| The wait line named an exited worker's pid, or pid 0. The lock file keeps the last holder's pid, and a requester read it before the new worker had taken the lock | It names a pid only while that pid holds the lock, and says the worker is starting otherwise: `TestSharedDemandRequesterNamesNoExitedWorker` |
| After `yolo pack prepare stop`, status read the same as a crash or an idle exit, though the design says status names the stop | Status names the stop and its time: `TestPreparationStatusNamesAnExplicitStop` |
| An `agent_updates`-off subscription was labelled `unresolved` and asked for a fresh launch, which under that policy registers nothing | It is `inactive` and names the policy, `yolo --update`, and the `yolo config set` command that turns the timer back on: `TestOrdinarySubscriptionRevokedByAgentUpdatesOffFromAnotherWorkspace`, `TestSelectedNPMTimerActualDispatchPreparesWithoutMovingRunningHome` |
| A re-enabled subscription stayed labelled `inactive` while active, and status printed `inactive (inactive)` | Re-registration makes it pending work, and status says `inactive` once: the same ownership test and `TestPreparationStatusNamesAnExplicitStop` |

**Not shown by this run.** A nested Podman launch is rootful and shares its network here, so it
proves neither rootless mapping or image-store delivery nor host-loopback reachability. Native
Darwin, Apple Container and Seatbelt were not run. The registry had no newer
`chrome-devtools-mcp`, so the run saw no timer admission of a new generation between launches.
It also did not exercise concurrent launches, Ctrl-C or crash recovery, patched or tree forks,
`yolo host`, or a failed check. Synthetic tests cover those; real runtime evidence is still
outstanding. Elapsed times are single samples on a shared machine, not the controlled
cold/warm measurements the design asks for.

After source stability, run the full landing gate and the slow integration suite once on the
combined commit. Build shipping binaries and launch the fresh binary by path from a private
throwaway workspace, pointed at the live candidate. Never install inside this jail or launch a
nested jail against the live workspace home.

A nested Podman launch proves neither rootless mapping/image-store delivery nor host-loopback
reachability. Those require a real rootless host or CI. Native macOS requires actual Apple and
Seatbelt observations, including the readable host-file denial test. Controlled cold/warm,
production capture and complete wall-clock measurements remain separate from synthetic source
fixtures. No acceptance check starts a coding agent or calls its API.

**Ordinary capture acceptance still external:** combined census/full gate,
the runtime gaps the [2026-11-13 record](#runtime-2026-11-13) names,
real rootless and native Darwin sealing evidence, controlled cold/warm wall-clock measurements
and platform-specific runtime evidence. No automated check starts a coding agent or calls its API. Exact identity selection retains each still-reachable compatibility winner;
no age-based reap or new global liveness registry was added.
