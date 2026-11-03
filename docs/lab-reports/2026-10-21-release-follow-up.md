---
title: "Release follow-up and recovered work"
date: 2026-10-21
status: open
tags: [lab-report, release, verification, macos-user]
summary: "Separates the published v0.15.0 evidence from current-source CI failures and the native, rootless, and owner-authorized follow-ups that remain open."
---

# Release follow-up and recovered work, 2026-10-21

A [lab report](README.md). This record distinguishes an immutable publication from a later
workflow target, records confirmed outcomes, and routes unfinished work to its canonical home.
It does not add a release gate or authorize live-agent, credential, or native-Mac trials.

## Observed

The prior work inventory recorded 59 rows: 45 complete and 14 unfinished. Those are inherited
roll-up counts, not a row-by-row re-audit. The completed outcomes that can be tied to current
source and publication evidence are grouped below; unfinished items keep their existing owners,
dependencies, and authorization boundaries.

### Published release and completed source outcomes

- **v0.15.0 is published** from immutable source commit
  [`b01c111`](https://github.com/mschulkind-oss/yolo-jail/commit/b01c1110e84b3900af88c0b39b156a512c7c0130)
  at 2026-10-20 22:39:23 UTC. The [release](https://github.com/mschulkind-oss/yolo-jail/releases/tag/v0.15.0)
  has four platform archives, checksums, and a publication claim. Release request
  [39503499835](https://github.com/mschulkind-oss/yolo-jail/actions/runs/39503499835),
  the targeted Release run [39508882822](https://github.com/mschulkind-oss/yolo-jail/actions/runs/39508882822),
  Publish run [39509720806](https://github.com/mschulkind-oss/yolo-jail/actions/runs/39509720806)
  (all 13 jobs succeeded), and Homebrew run
  [39509723871](https://github.com/mschulkind-oss/yolo-jail/actions/runs/39509723871) succeeded.
- **Do not conflate target with workflow HEAD.** The tag and release request identify `b01c111`;
  Release, Publish, and Homebrew jobs ran from workflow HEAD `94989f1` while explicitly targeting
  that tag/request. A separate request, [39504109994](https://github.com/mschulkind-oss/yolo-jail/actions/runs/39504109994),
  failed eligibility for target `94989f1`; it was not a failed publication of v0.15.0.
- **The original release-follow-up snapshot was later source, but runtime and test inputs matched the published tag.** At that initial snapshot, checkout HEAD was `31d61546ccf7aa5ae763f4e5541011e007e5cb10`. `git diff v0.15.0 -- cmd internal integration packs go.mod go.sum vendor flake.nix flake.lock` was empty; the complete diff from the tag was documentation-only. This tree comparison is not an ancestry inference, and it describes the pre-repair snapshot rather than the current main HEAD.
- **The refresh-fallback sequencing repair is already shipped.** Commit
  [`6ddb485`](https://github.com/mschulkind-oss/yolo-jail/commit/6ddb485188c638a9586469ae023f9e5a6e2cc0cc)
  is in v0.15.0. The generated-launcher regression
  [`TestAFailedBackgroundRefreshRefusesWhenTheRetryFails`](../../internal/entrypoint/prelaunchrefreshtiming_test.go#L334-L373)
  and production caller [`prelaunchrefresh.go`](../../internal/entrypoint/prelaunchrefresh.go#L613-L628)
  match the tag. The test-first fix prevents `YOLO_USE_INSTALLED` from queuing another refresh
  after handling a failed background result; this record does not claim a new fix.
- **The selected-pack root retention fix and its caller test are shipped.** Commit
  [`bd08859`](https://github.com/mschulkind-oss/yolo-jail/commit/bd08859d6f73458bea33fb30c379a4e047f98338)
  is an ancestor of v0.15.0. [`Check`](../../internal/cli/check/check.go) retains staged selected
  pack roots through later report sections, and
  [`TestCheckKeepsSelectedForkRootThroughTheHostFloor`](../../internal/cli/check/selectedpackroot_test.go)
  exercises the production `Check` order and verifies cleanup afterwards. These source and test
  files are unchanged from the tag.
- **Per-session profile isolation and owned-host capture are built for the container and
  owned-host paths.** The [settled profile design](../design/per-session-profiles.md)
  records per-session entry snapshots and rendered homes, with host boundary and capture
  outcomes; those fixtures do not prove a real agent's subagent behavior.
- **The native Pi per-session adaptation is implemented, with native runtime still unmeasured.**
  The [macos-user profile record](../design/macos-user-session-profiles.md) reports synthetic
  bootstrap, capture and child-inheritance fixtures plus Darwin cross-builds; it does not
  establish Seatbelt behavior on a Mac or real Pi descendants.
- **Synthetic Claude coordination is completed within its authorized boundary.** The
  [native Claude record](../design/macos-user-claude-refresh.md#the-native-measurement-contract)
  documents the synthetic-first contract and passing Linux fixture slice. It is not a real
  Claude login, refresh, Keychain or vendor-lock measurement.
- **Developer-Mac suite repairs have a recorded native run.** The
  [suite record](../design/macos-user-suite-on-a-developer-mac.md#12-what-done-looks-like)
  reports `TestMacosUser` passing on the host of record (150 passed, 4 skipped, 0 failed)
  with the narrow-sudo cell passing. This is that dated suite evidence, not a run performed
  for this follow-up.
- **The repaired developer-Mac live-config unit cases are recorded as passing** at `94989f1`:
  the [live-config design](../design/live-config-apply.md) and
  [canonical roadmap](../plans/roadmap.md#immediate-continuation) retain the still-owed rootless,
  real-broker, and parity evidence. Native unit success does not discharge those separate checks.

## Historical baseline failure and repair evidence

The pre-repair CI failure is historical, not current status. Run [39529735230](https://github.com/mschulkind-oss/yolo-jail/actions/runs/39529735230) targeted baseline SHA `31d61546ccf7aa5ae763f4e5541011e007e5cb10`. The Go and macOS unit jobs each reported the same four distinct failing tests: `TestAnAttachToAJailTheLastReleaseLaunchedNeverRidesAlong`, `TestShippedPacksDecodeUnderTheLastRelease`, `TestModelListCheckPackWideMetadataPreservesTheLastReleasesProgram`, and `TestTheJudgeAcceptsWhatTheLastReleaseReportsOnABareMac`. The release fixtures selected ancestral v0.2.0, which has neither the required `go.mod` nor `packs/` tree; failures were missing-module/build and missing-pack-archive inputs. The integration aggregate failed because its required shard set was incomplete, not because a full integration run completed and failed. This is a real baseline CI RED, but it is not proof the v0.15.0 publication failed.

Do not recast the initial repair's fixture/implementation REDs as an intentional old-algorithm demonstration. The separately reported permanent witness is the green synthetic ancestry test: a fixture with ancestral `v0.2.0` and disconnected `v0.15.0` proves `git describe HEAD` selects the old tag while the local-tag resolver selects the newer one, with no `go.mod` in the fixture. It is a durable assertion, not evidence that an original implementation was first run and intentionally failed. The first reported intentional test-first RED is separate: `TestLatestIgnoresLeadingZeroSemVerComponents` selected invalid `v099.0.0` over valid `v2.0.0` before the numeric-component parser fix and passed afterward. That red is recorded by the integration evidence report; its standalone raw red command is not among the retained gate logs. See the [permanent resolver tests](../../internal/testsupport/releasebaseline/releasebaseline_test.go#L11-L69) and [SemVer cases](../../internal/testsupport/releasebaseline/releasebaseline_test.go#L96-L119).

The four production-caller test bodies were compared across baseline `31d61546`, source-review candidate `44c73a75db21bb5df84e356c83751d03d1ff88b2`, and final source `ae25d96a68abefe1c8bc47b0e7c3329503fa869b`: all four bodies were identical in all three revisions. The candidate changes how each caller resolves the historical tag; it does not delete the existing compatibility assertions.

| Existing caller / assertion | Release-specific resolver path |
| :--- | :--- |
| [`TestAnAttachToAJailTheLastReleaseLaunchedNeverRidesAlong`](../../internal/cli/run/packskew_test.go#L372-L411) checks the legacy shared pack tree remains unchanged and no longer-composable state is refused; it does not require every old tree to decode. | Its `lastReleaseTag` helper calls `releasebaseline.LatestBefore(root, "HEAD")` ([helper](../../internal/cli/run/packskew_test.go#L416-L441)); all tags peeling to the candidate commit are excluded. |
| [`TestShippedPacksDecodeUnderTheLastRelease`](../../packs/releasedecode_test.go#L270-L467) checks current shipped packs against release-specific decode allowances and verifies unaffected declared program identities remain checked. | Its `lastRelease` helper calls `LatestBefore(root, "HEAD")` ([helper](../../packs/releasedecode_test.go#L597-L627)). |
| [`TestModelListCheckPackWideMetadataPreservesTheLastReleasesProgram`](../../packs/releasedecode_test.go#L472-L519) keeps the synthetic safe/unsafe metadata contrast: safe metadata retains `synthetic`; unsafe program-field metadata retains none, including with v0.15.0. | Uses the same `lastRelease` → `LatestBefore(root, "HEAD")` helper. |
| [`TestTheJudgeAcceptsWhatTheLastReleaseReportsOnABareMac`](../../tools/tap-install-check/release_test.go#L73-L104) requires the current judge accept the real release-generated bare-Mac report and keeps unreported allowlist failures visible; this is a hermetic report fixture, not a real installed-formula or native-Mac trial. | Its local helper calls `releasebaseline.Latest(root)` ([helper](../../tools/tap-install-check/release_test.go#L108-L140)), intentionally including tags on HEAD. |

The shared [`releasebaseline` resolver](../../internal/testsupport/releasebaseline/releasebaseline.go#L25-L79) reads local `refs/tags`, parses version-shaped names, peels tags to commits, optionally excludes the resolved candidate commit, and sorts by version. It does not traverse ancestry or query publication status. Its parser and comparator are in the same file ([version parsing](../../internal/testsupport/releasebaseline/releasebaseline.go#L94-L194)). The CI workflow keeps full tag history with `fetch-depth: 0` and documents local-skip versus CI-fail behavior ([workflow](../../.github/workflows/ci.yml#L49-L62)); absent local tags skip locally but fail on GitHub Actions. Release tag immutability remains governed by the separate [release preflight](../../tools/release-wiring/preflight.sh#L46-L76), not by local-tag resolution.

### Exact final-source gates and nested attempt

The source-review candidate `44c73a75db21bb5df84e356c83751d03d1ff88b2` received a saved independent source-review **PASS / SOURCE_REVIEW_OK — OK with notes**. That verdict is scoped to that exact seven-file source candidate, not a full second review of final main. Final integrated source was `ae25d96a68abefe1c8bc47b0e7c3329503fa869b`, tree `f9810c6571af59c21bccbdf91414ecf2b739fb8e`; the baseline-to-final diff was nine files, 870 insertions and 66 deletions, with no production runtime implementation change. The final source differs from the reviewed candidate by the HOME-reader sketch completion and resolver leading-zero fix. The exact source-review candidate's review verdict is not silently widened to cover those follow-ups.

For the exact final source SHA, the recorded commands were:

| Command | Candidate working directory and result | Bound |
| :--- | :--- | :--- |
| `just build-go` | `/workspace/.yolo/durable/worktrees/pi-subagents/workspace/pi-worktree-2b3a67bc-cf85-4e5d-b91d-09b31125434d-s0-0`; exit `0`, built `dist-go/linux-amd64`. | Shipping build only. |
| `just check-ci` | Same candidate cwd, clean status; exit `0`. Linux/Darwin vet and staticcheck, short tests and repository checks passed. | Not the slow integration suite, a remote CI run, or native/rootless certification. |
| `just done` | Same candidate cwd, clean status; exit `0`, selectively verified `ae25d96a` since `44c73a75`. | Included changed-document checks; explicitly excluded integration, nested-jail and native execution. |
| Candidate nested launch without hatch | From the same candidate cwd, throwaway workspace `/tmp/yolo-nested-recovered-stack-ae25d96a`, binary `/workspace/.yolo/durable/worktrees/pi-subagents/workspace/pi-worktree-2b3a67bc-cf85-4e5d-b91d-09b31125434d-s0-0/dist-go/linux-amd64/yolo`, `YOLO_REPO_ROOT` set to that candidate. The launcher printed that same path as `Flake source: … (YOLO_REPO_ROOT)`. | Selected `pi` program was unavailable: provisioning refusal was exit `78`; outer launcher/log wrapper returned `79`. This was not a successful nested bash launch. |
| Candidate nested launch with `YOLO_ALLOW_MISSING_PROGRAMS=1` | Same binary and `YOLO_REPO_ROOT`, throwaway cwd `/tmp/yolo-nested-recovered-stack-ae25d96a-allow-missing`; exit `0` after `candidate nested bash ready: HOME=/home/agent`. | Missing `pi` remained disclosed. This proves only bash startup with the documented hatch, not readiness or an agent trial. |
| Primary checkout `just done` before this report update | `/workspace`, HEAD `ae25d96a68abefe1c8bc47b0e7c3329503fa869b`, tree `f9810c6571af59c21bccbdf91414ecf2b739fb8e`, clean index/worktree; exit `0`, already selectively verified with nothing changed. | Not a second `just check-ci`; does not include integration, nested, or native execution. |

The two nested rows use the exact recorded candidate binary, cwd, and flake root; they should not be rewritten as a generic “nested smoke passed.” Their output also records shared host networking and does not certify rootless delivery/loopback paths. The `just check-ci` and `just done` results above were already green on `ae25d96a`; this documentation repair does not rerun either full source gate or the full integration suite.

## Prior-session inventory

The earlier roll-up was too coarse to verify. The [recovered prior-session inventory](2026-10-21-prior-session-inventory.md) now records each of the 59 snapshot rows—45 completed, 14 unfinished—with its original ID, title and status, completed outcome or unfinished disposition, recovered dependencies, and source/session locator. The row list keeps historical status separate from present-day interpretation; it does not authorize unfinished trials or treat a completed review action as an approval. Current-session rows 123/124/125 remain outside that historical denominator.

## Open work and disposition

The [roadmap](../plans/roadmap.md) orders the shared backlog; each linked design owns its detail.
These are follow-ups with their stated dependencies, not newly declared release holds.

- **Real broker after config widening:** still needed by the
  [live-config design](../design/live-config-apply.md), alongside rootless M1 on both required
  architectures and remount/parity checks. The nested jail cannot stand in for a real rootless
  host or a real broker request. Live-config's current-source/native result is not the same as
  hosted landed-evidence transport; preserve the Mac result with its source identity and durable
  acceptance evidence rather than treating a local note as the hosted witness.
- **External GitHub broker fixture authorization is separate and still owed.** Before an
  external fixture is run, obtain explicit approval for scope A/B and evidence of the broker's
  lifetime, current-authority checks and capability witness, as described by the
  [broker handler contract](../design/boundary-broker.md#L1264-L1270). A live-config request
  after widening does not authorize or discharge this distinct fixture and witness.
- **Rootless delivery and loopback reachability:** use the
  [image delivery reference](../reference/image-staging-vs-baking.md) and
  [reachability reference](../reference/loopback-tls-reachability.md). A nested jail's forced
  rootful and shared-network modes cannot verify the rootless store-delivery or host-to-jail
  loopback-forwarding branches. Obtain evidence from a real rootless host or CI before claiming
  those behaviors verified.
- **Externally handled Pants/VPN DNS report:** the existing
  [DNS lab report](2026-10-19-pants-dns-under-vpn.md) remains explicitly outside this work. It is
  not fixed here, and this follow-up does not reopen or investigate it.
- **Native Claude vendor behavior and credential view:** the
  [native Claude measurement design](../design/macos-user-claude-refresh.md) still requires a
  native measurement of Claude's file-lock/re-read and file-versus-Keychain behavior. Testing the
  already-built view requires a separately authorized disposable-login trial; the
  [owning view design](../design/claude-login-without-interception.md#11-decision-ledger) keeps it
  off by default until its measures and the independent real-rootless-host day are satisfied.
  This report performed no agent/API call and read no credentials.
- **Concurrent macOS workspaces:** the architecture is already selected: one guest user, distinct
  persistent private workspace homes/state, and only selected pack machine-scope paths plus
  existing configured shared paths. That decision and its boundaries live in the
  [concurrent-workspace design](../design/macos-user-model.md#7-the-ruled-model-two-modes-one-profile)
  and [home-tier ledger](../reference/macos-user-home-tiers.md#decision-ledger). MCW4's sharing
  policy is settled; the physical backing and relative-link geometry remain engineering work, not
  a new permission grant. [OQ-MCW2](../design/macos-user-model.md#decision-ledger) remains
  the same-UID/outside-Seatbelt threat-claim question, and
  [OQ-MCW3](../design/macos-user-model.md#decision-ledger) remains the reclamation policy.
  The [implementation sketch](../design/macos-user-model-plan.md#MMP-D25) is not a build
  handoff: concrete layout, reader inventory, lifecycle, and authorized native sibling-denial
  proof remain before implementation.
- **Native Pi/fork and remaining Mac launch evidence:** the
  [Linux fork report](2026-10-19-linux-fork-materialization-verification.md) and
  [roadmap](../plans/roadmap.md#immediate-continuation) retain the native Pi/fork runtime work and
  the Mac launcher findings. The [Mac repair evidence record](2026-10-21-macos-repair-evidence.md)
  maps the landed source/test repairs and records the unresolved row23 hosted receipt plus row53's
  individual dependencies; it does not close hosted acceptance or the remaining native/owner gates.
  The `.local` sidecar defect remains explicitly owner-deferred and unverified; do not silently
  promote it into a fix. Check each current disposition in the roadmap before taking it up.
- **Watcher ownership:** a watcher that wakes an agent must be owned by the current live session;
  an old-session-only promise is insufficient. Keep this criterion with the
  [agent-event watcher design](../design/agent-event-watchers.md), not as an implementation
  claim in this release report.

## Fresh observer verification (2026-10-21 03:00–03:08 UTC)

This later read-only observation supersedes the current-status snapshot above. At
03:04 UTC, fresh local probes and a separate remote ref read both identified main
as `31d61546ccf7aa5ae763f4e5541011e007e5cb10`.

The matching-SHA [regular CI run](https://github.com/mschulkind-oss/yolo-jail/actions/runs/39529735230)
failed in `check-go` at “Quality gate (vet + staticcheck + gofmt + short tests)”
and in `check-macos` at “Unit tests (macOS)”; `integration-complete` also failed
its “Every integration shard succeeded” aggregate. `secrets-scan` passed. These
are observed job/step outcomes, not diagnoses: no failure cause is inferred here.
The matching-SHA [Apple Container parity run](https://github.com/mschulkind-oss/yolo-jail/actions/runs/39530138735)
passed, including its parity and archive-delivery checks. Earlier test-name and
cause details above were not re-established by these fresh reports and should
not be treated as a current diagnosis.

The parent session's native schedule history independently records five
completed natural observer fires at 03:00:44, 03:02:44, 03:04:44, 03:06:44,
and 03:08:44 UTC; each generated an automatic completion notification in the
owning parent session. The first three fires correspond to separate
timestamped read-only GitHub observations summarized here. The observer was
configured every two minutes, bound to its creating session, non-quiet, with
overlap skipped and no catch-up. Its prior-generation schedule was paused with
its definition and history retained. These are schedule-history fire times,
not claimed workflow-completion times; this records event delivery, not a
promise that a process survives restart.

The observations used read-only local Git probes and authenticated GitHub reads
through the existing broker. No token or credential was read, copied, or
logged; no GitHub write, retry, cancellation, dispatch, or release action was
performed. The immutable v0.15.0 publication at `b01c111` remains distinct
from current-main CI, as recorded above.

## Limits

This report records public project outcomes and evidence, not private session transcripts. It makes
no claim that all 45 historical completed rows were re-audited, no claim that current CI passed, and
no new release hold. Native Claude use, credentials, interactive agents, live VPN/DNS investigation,
rootless host trials, and primary-branch landing were not performed here. The durable project queue
remains the [roadmap](../plans/roadmap.md).
