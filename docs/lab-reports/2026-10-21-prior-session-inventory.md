---
title: "Recovered prior-session inventory"
date: 2026-10-21
status: open
tags: [lab-report, inventory, verification]
summary: "Preserves all 59 rows from the recovered historical task snapshot without treating the snapshot as runtime or approval evidence."
---

# Recovered prior-session inventory, 2026-10-21

This companion to the [release follow-up](2026-10-21-release-follow-up.md#prior-session-inventory)
preserves the complete historical snapshot row by row. It is evidence recovery, not task approval,
runtime certification, or permission to resume broader work. The 59-row snapshot contains 45
completed rows and 14 unfinished rows; historical statuses are retained verbatim, including rows
whose status was later superseded by evidence.

## Provenance and limits

The native snapshot is session `01a150b3-24de-7204-afd1-87250b2c774e`, entry 8678 (one-based),
`work-control-todo-v1`, at 2026-10-21T01:55:30Z; entry 8679 confirms the 45/59 count. The
recovery used the already-saved full ID/title/status/dependency extraction, with supervisor-approved
reuse recorded in the recovery session `01a15c38-1148-7310-ae55-60e6bd4531cb`, entries 28–35.
No raw conversation is reproduced. Repository-relative paths and commit identifiers below are
preferred as durable corroboration; snapshot locators identify the historical row, not proof that a
runtime trial or review passed.

The recovered extraction did not include dependencies for completed rows. Those cells explicitly
say so rather than reconstructing dependencies. All 14 unfinished-row dependencies are preserved
as recovered. The table separates the original status from later disposition and names the
canonical source for unfinished work where one exists.

## Full original59 snapshot

| Original ID | Original title | Original status | Finished outcome OR unfinished disposition | Original dependency IDs | Exact row/source locators |
|---:|---|---|---|---|---|
| 7 | Record Mac rulings and integrate cleanup | `completed` | Mac cleanup rulings/repairs integrated; later suite evidence records 150 passes, 4 skips, no root-sudo expansion. | Not present in the recovered snapshot; not inferred. | docs/design/macos-user-suite-on-a-developer-mac.md#what-done-looks-like; docs/plans/roadmap.md:75–98; Native snapshot entry 8678, row 7. |
| 20 | Await real broker fixture authorization | `in-progress` | External real GitHub broker fixture scopeA/B/lifetime/current-authority/capability witness awaits explicit approval. Static/help reads and live-config request do not discharge it. | none | docs/design/boundary-broker.md:1264–1270; docs/lab-reports/2026-10-21-release-follow-up.md §Open work; Native snapshot entry 8678, row 20. |
| 21 | Explain all-jails Pants installation | `completed` | Explained persistent Pants installation through mise; no installation performed. | Not present in the recovered snapshot; not inferred. | Native snapshot entry 8678, row 21. |
| 22 | Diagnose Pants bootstrap DNS failure | `in-progress` | Externally handled Pants/VPN DNS; intentionally parked/outside YOLO work and release prerequisites. Historical cause unknown, not fixed; no new investigation. | none | docs/lab-reports/2026-10-19-pants-dns-under-vpn.md; docs/lab-reports/2026-10-21-release-follow-up.md §Open work; Native snapshot entry 8678, row 22. |
| 23 | Supply landed Mac repair evidence | `in-progress` | Functional Mac repairs already landed/local-gated; only genuine remaining hosted evidence-transport/acceptance gap. No duplicate/no-op fix, review loop or release hold. | none | docs/lab-reports/2026-10-21-release-follow-up.md §Open work; docs/design/live-config-apply.md; Native snapshot entry 8678, row 23. |
| 27 | Inventory unreleased changes and labs | `completed` | Unreleased features and lab findings inventoried at the historical source snapshot. | Not present in the recovered snapshot; not inferred. | Native snapshot entry 8678, row 27. |
| 34 | Design concurrent native workspace homes | `completed` | Concurrent-home design/question routing completed, not runtime support; its old undecided architecture is superseded by row45. | Not present in the recovered snapshot; not inferred. | commit `6677869a`; docs/design/macos-user-concurrent-workspaces.md; Native snapshot entry 8678, row 34. |
| 40 | Recap queued post-release work | `completed` | Queued native/post-release designs recapped; small-patch-first was advice, not an owner ruling. | Not present in the recovered snapshot; not inferred. | Native snapshot entry 8678, row 40. |
| 41 | Extract native design owner questions | `completed` | Native owner questions extracted; old HT6/MCW4 unanswered framing later superseded, MCW2/3 remain open. | Not present in the recovered snapshot; not inferred. | docs/design/macos-user-concurrent-workspaces.md:54–79; Native snapshot entry 8678, row 41. |
| 42 | Provide native OQ Vantage filter | `completed` | Provided a historical Vantage-filter answer; deployed-version certainty was not claimed. Later row55 consults installed CLI. | Not present in the recovered snapshot; not inferred. | Native snapshot entry 8678, row 42. |
| 43 | Implement native Pi session profiles | `completed` | Native same-workspace Pi config identity, child environment, lease/capture implementation built; native runtime remains row50. | Not present in the recovered snapshot; not inferred. | current commit29e298ca5; Native snapshot entry 8678, row 43. |
| 44 | Measure native Claude refresh coordination | `in-progress` | Synthetic-first lock/atomicity/recovery and native launch-callsite harness landed007d278; frozen native-vendor/file-versus-Keychain measure NOT complete. Await Mac and separately authorized disposable login/API; default-off/gates unchanged. | none | docs/design/macos-user-claude-refresh.md#the-native-measurement-contract; current64e1dbb7d; docs/design/claude-login-without-interception.md#11-decision-ledger; Native snapshot entry 8678, row 44. |
| 45 | Decide native workspace home architecture | `completed` | Owner selected parallel private workspace homes under exactly ONE guest UID; no per-workspace accounts. | Not present in the recovered snapshot; not inferred. | session entries 6754 and 6758; docs/design/macos-user-concurrent-workspaces.md:14–36; Native snapshot entry 8678, row 45. |
| 46 | Decide native sibling isolation requirements | `in-progress` | Original WAIT_HT6 stale: architecture now selected. MCW2 remains open only for arbitrary same-UID processes outside Seatbelt. Confined sibling physical-home denial already mandatory; no weakening/accounts/privilege grant. | 45 | docs/design/macos-user-concurrent-workspaces.md:32–36,60–69; docs/reference/macos-user-home-tiers.md#decision-ledger; Native snapshot entry 8678, row 46. |
| 47 | Decide native workspace home lifetime | `in-progress` | Original WAIT_HT6 stale. MCW3 explicit persistent-home reclamation policy remains open; preserve live/unknown bytes. Session lease/capture cleanup is not workspace reaping. Native stop exists, no deletion authorization. | 45 | docs/design/macos-user-concurrent-workspaces.md:48,71–79; docs/design/macos-user-concurrent-workspaces-plan.md:170–175,222–223; Native snapshot entry 8678, row 47. |
| 48 | Decide native shared machine resources | `in-progress` | Historical unfinished MCW4 is now SETTLED policy: only selected machine-pack and existing configured shared scope. Physical backing/relative-hook geometry is engineering, not new credential/access authority. | 45 | docs/design/macos-user-concurrent-workspaces.md:34,54–56; docs/design/macos-user-concurrent-workspaces-plan.md:28–34,70–118; Native snapshot entry 8678, row 48. |
| 49 | Implement concurrent native workspace support | `not-started` | Runtime UNBUILT. Source prerequisites45/46/47/48 preserved historically; now45/48 settled, MCW2/3 open. Detailed reader/layout sketch advanced, but native sibling-denial proof and build-ready handoff still required. Keep existing cross-workspace guard. | 45, 46, 47, 48 | docs/design/macos-user-concurrent-workspaces-plan.md:1–34,46–60,190–223; docs/design/macos-user-concurrent-workspaces.md:48–50; internal/cli/run/accounthomehold.go; Native snapshot entry 8678, row 49. |
| 50 | Validate native Pi and fork runtime | `in-progress` | Native immutable0de run39421812427 recorded named passes and three owner-deferred fork failures, host-floor own and legacy Pi path fixture defects. Fixtures later corrected104; real Pi descendants/auth and complete current native fork/lifecycle proof still owed. No duplicate workflow/API trial. | 43 | docs/lab-reports/2026-10-20-native-session-batch-verification.md; docs/lab-reports/2026-10-19-linux-fork-materialization-verification.md; Native snapshot entry 8678, row 50. |
| 51 | Collect approved live Claude measurements | `not-started` | After44 synthetic work, live native Claude vendor-lock/re-read and file-Keychain semantics need separate explicit disposable-login/API authorization. Existing token-view default-off and real-rootless-host-day gates stay. | 44 | docs/design/macos-user-claude-refresh.md#the-native-measurement-contract; docs/design/claude-login-without-interception.md#11-decision-ledger; Native snapshot entry 8678, row 51. |
| 52 | Validate rootless Linux live behavior | `in-progress` | Actual rootless Linux amd64/arm64 witness owed for loopback/store delivery/live context and capability/lifetime behavior. Nested rootful/shared-network passes cannot certify it; authorized external host or exact-source unskipped CI needed. | none | docs/reference/loopback-tls-reachability.md; docs/reference/image-staging-vs-baking.md; docs/design/live-config-apply.md; AGENTS.md#testing; Native snapshot entry 8678, row 52. |
| 53 | Repair remaining Mac launcher findings | `in-progress` | Parser/signal/warning fixes landed. Remaining: builder self-count exemption ruling; Ctrl-C log diagnostic ruling; podman symlink handling; host-HOME refusal; bounded native cleanup entries/timing cause unknown. Mac .local fork defect expressly owner-deferred, not pulled in. | none | docs/plans/roadmap.md:100–178; docs/lab-reports/2026-10-19-launch-diagnostics-on-a-mac.md; docs/design/pi-extension-store-builds.md; Native snapshot entry 8678, row 53. |
| 54 | Confirm release publication outcomes | `completed` | v0.15.0 publication confirmed at immutable b01c111; later failed request targeted a different source, not this publication. | Not present in the recovered snapshot; not inferred. | docs/lab-reports/2026-10-21-release-follow-up.md:25–37; request39503499835, Release39508882822, Publish39509720806, Homebrew39509723871; Native snapshot entry 8678, row 54. |
| 55 | Read Vantage CLI filter grammar | `completed` | Installed Vantage CLI help/filter grammar inspected; bare terms AND, only multiple path terms union. | Not present in the recovered snapshot; not inferred. | Native snapshot entry 8678, row 55. |
| 58 | Land reviewed native Pi isolation | `completed` | Reviewed native Pi slice serially integrated as f5841084; not a native-hardware or real-child authentication certification. | Not present in the recovered snapshot; not inferred. | Native snapshot entry 8678, row 58. |
| 60 | Expose landed native code evidence | `completed` | Actual landed Pi production/test diff evidence supplied after a docs-cutoff packet; no new implementation. | Not present in the recovered snapshot; not inferred. | Native snapshot entry 8678, row 60. |
| 67 | Restore continuous CI release watcher | `in-progress` | Historical continuous observer had natural wakes and many superseded paused definitions; ongoing item, not completion from one tick. Current live-session ownership/automatic delivery must be proven afresh; do not inherit old-session promise. No source/credential widening. | none | docs/lab-reports/2026-10-21-release-follow-up.md#fresh-observer-verification-2026-10-21-03000308-utc; docs/design/agent-event-watchers.md; Native snapshot entry 8678, row 67. |
| 74 | Review Apple memory table parser | `completed` | Apple MEMORY-table parser review completed; numeric MB normalization kept strict unsafe-row refusal and did NOT exempt builder. | Not present in the recovered snapshot; not inferred. | current741e852df; Native snapshot entry 8678, row 74. |
| 78 | Repair failed-refresh allowance sequencing | `in-progress` | Historical in-progress row already superseded by completed81 and shipped6ddb485; sequencing regression test preserves old-result clear/refusal and no second refresh queue. Mark source/history contradiction explicitly, do not propose another fix. | none | docs/lab-reports/2026-10-21-release-follow-up.md:42–49; internal/entrypoint/prelaunchrefreshtiming_test.go:334–373; currentfde7f07f3; Native snapshot entry 8678, row 78. |
| 81 | Land reviewed refresh allowance fix | `completed` | Failed-refresh installed-version hatch fix landed; caller returns before queuing another refresh. | Not present in the recovered snapshot; not inferred. | docs/lab-reports/2026-10-21-release-follow-up.md:42–49; historical6ddb485/currentfde7f07f3; internal/entrypoint/prelaunchrefresh.go; Native snapshot entry 8678, row 81. |
| 82 | Fix cleanup review blockers | `completed` | Cleanup review blockers repaired: still-live unsignalled survivors error; live refreshed empty argv refuses immediately. | Not present in the recovered snapshot; not inferred. | Native snapshot entry 8678, row 82. |
| 83 | Verify fresh refresh-fix prefix | `completed` | Fresh stamped prefix and empty-pack nested launch verified at6ddb485; smoke did not prove hatch feature semantics/rootless. | Not present in the recovered snapshot; not inferred. | Native snapshot entry 8678, row 83. |
| 85 | Diagnose transient cleanup classification | `completed` | Transient cleanup failure diagnosed as unreadable HOME/live empty argv; later PID absence did not identify cause. | Not present in the recovered snapshot; not inferred. | Native snapshot entry 8678, row 85. |
| 87 | Review bounded HOME retry safety | `completed` | Bounded unreadable-HOME retry safety review returned OK; no signal on unresolved ownership. | Not present in the recovered snapshot; not inferred. | Native snapshot entry 8678, row 87. |
| 88 | Triage downloaded-pack repetition fixture | `completed` | Downloaded-pack count-one fixture passed; count10 repeats cached TestMain store and invalidates first-fetch premise. | Not present in the recovered snapshot; not inferred. | Native snapshot entry 8678, row 88. |
| 89 | Land HOME retry and verify | `completed` | HOME retry source landed with exact-source quality and full integration pass; historical/rootless/native cause not certified. | Not present in the recovered snapshot; not inferred. | currenta1e2802b4; Native snapshot entry 8678, row 89. |
| 90 | Reconcile committed final verification evidence | `completed` | Final verification report reconciled source ancestry and actual235s quality/934.621s full pass, retaining older failures/limits. | Not present in the recovered snapshot; not inferred. | docs/lab-reports/2026-10-20-release-ci-fixture-diagnoses.md:30–38; Native snapshot entry 8678, row 90. |
| 91 | Repair Check signal cleanup lifetime | `completed` | Check signal cleanup candidate added real-exit tree-removal regression; review subsequently found races handled by93/95. | Not present in the recovered snapshot; not inferred. | currentbce9189dc; Native snapshot entry 8678, row 91. |
| 92 | Review Check signal cleanup safety | `completed` | Safety review completed with BLOCK for creation-registration gap and post-release tree reallocation; completion is review work, not approval. | Not present in the recovered snapshot; not inferred. | Native snapshot entry 8678, row 92. |
| 93 | Close signal cleanup ownership races | `completed` | Creation/registration staging lease and exit-only embedded/process-tree fence implemented for both reviewed races. | Not present in the recovered snapshot; not inferred. | current68d6cefd8; Native snapshot entry 8678, row 93. |
| 94 | Recheck terminal cleanup lifetime changes | `completed` | Follow-up review completed with BLOCK for concurrent removal versus os.Exit; row95 is the specific repair. | Not present in the recovered snapshot; not inferred. | Native snapshot entry 8678, row 94. |
| 95 | Serialize overlapping staging tree removal | `completed` | Single-flight staging removal implemented; concurrent signal cleanup waits for removal, with real-exit overlap regression. | Not present in the recovered snapshot; not inferred. | currentcd48bc0fa; Native snapshot entry 8678, row 95. |
| 96 | Warn on unrecognized launch environment names | `completed` | Name-only unknown-YOLO launch warnings implemented with CLI→Run wiring, selected declarations, remediation and privacy tests. | Not present in the recovered snapshot; not inferred. | current7a852a5f2; Native snapshot entry 8678, row 96. |
| 97 | Review launch environment warning coverage | `completed` | Recognition/call-site review completed with BLOCK: four legitimate names omitted and warning bypassed launch-log tee. Row98 repairs both; review completion is not approval. | Not present in the recovered snapshot; not inferred. | Native snapshot entry 8678, row 97. |
| 98 | Fix warning recognition and log routing | `completed` | Four exact inherited names added and callback uses live teed o.Stderr; production Run screen/log/privacy regression and affected-package gate recorded. | Not present in the recovered snapshot; not inferred. | current5d001381e; Native snapshot entry 8678, row 98. |
| 99 | Validate prepared warning in nested runtime | `completed` | Exact-candidate nested warning/privacy/remedy smoke passed; observed31 inherited false positives became row100, rootless=false. | Not present in the recovered snapshot; not inferred. | Native snapshot entry 8678, row 99. |
| 100 | Fix inherited launch-name false positives | `completed` | 31 source-backed inherited names recognized, including capture-store; negatives retained, no whole-prefix whitelist. | Not present in the recovered snapshot; not inferred. | currentf6bc93a55/cb7881933; Native snapshot entry 8678, row 100. |
| 101 | Finish interrupted affected-package warning gate | `completed` | Interrupted affected-package warning gate completed at exact4271680: two packages passed164s, vet passed; not a full/native gate. | Not present in the recovered snapshot; not inferred. | Native snapshot entry 8678, row 101. |
| 102 | Correct native stop lifetime draft | `completed` | Draft corrected to existing workspace-scoped native stop; no persistent-home reclamation ruling/runtime change. | Not present in the recovered snapshot; not inferred. | currentae1534456; Native snapshot entry 8678, row 102. |
| 103 | Integrate and land launcher repairs | `completed` | Parser, signal cleanup and launch warning repairs integrated, not deferred by invented freeze. Individual platform waits remain. | Not present in the recovered snapshot; not inferred. | current3b18955a3/a827da18f; docs/lab-reports/2026-10-20-release-ci-fixture-diagnoses.md:34–38; integration fixture changes are separately104–108; Native snapshot entry 8678, row 103. |
| 104 | Correct native Pi smoke fixtures | `completed` | Pi fixtures use published PI_CODING_AGENT_DIR; native fork fixture explicitly opts host_management:own; no HOME fallback/auth widening. | Not present in the recovered snapshot; not inferred. | current127b37e00; Native snapshot entry 8678, row 104. |
| 105 | Capture empty-command cleanup transition | `completed` | Fail-closed empty-command diagnostic added using already-read identity/state/error fields; targeted pass was nonreproduction, not cause/fix proof. | Not present in the recovered snapshot; not inferred. | currentdd42cef61; Native snapshot entry 8678, row 105. |
| 106 | Capture kernel exit-state cleanup flags | `completed` | Kernel exit flags added to bounded diagnostic; focused target passed without reproducing earlier failure or recording failure-time flags. | Not present in the recovered snapshot; not inferred. | current322114bee; Native snapshot entry 8678, row 106. |
| 108 | Retain verified exiting cleanup targets | `completed` | Proven exiting targets retained without signals only after coherent HOME/start bracket and same-state PF_EXITING proof; review ownership blocker repaired. | Not present in the recovered snapshot; not inferred. | docs/lab-reports/2026-10-20-release-ci-fixture-diagnoses.md:34–38,70–78; currentc4106e48c/9d552eb0e; Native snapshot entry 8678, row 108. |
| 112 | Diagnose late HOME-read refusal safely | `completed` | Late HOME diagnostic preserves fail-closed behavior/error identity and zero signals; exact full run failed with ESRCH, motivating114. | Not present in the recovered snapshot; not inferred. | current16d025f41; Native snapshot entry 8678, row 112. |
| 114 | Retain narrowly proven exiting ESRCH | `completed` | Narrow late ESRCH retention landed; first HOME cause/mixed errors/original deadline preserved; local quality/full integration passed. | Not present in the recovered snapshot; not inferred. | current147dfee8d; Native snapshot entry 8678, row 114. |
| 117 | Retain proven exiting EACCES targets | `completed` | Late EACCES added only after existing owned-exit proof; retained target never signalled; B01 serial landing and local gates recorded. | Not present in the recovered snapshot; not inferred. | currentb071031db; docs/lab-reports/2026-10-20-release-ci-fixture-diagnoses.md:80–98; Native snapshot entry 8678, row 117. |
| 119 | Land bounded release evidence reports | `completed` | Bounded release report landed with compliant eight-child evidence, old hosted E46 failure and B01 predicate comparison, not causal/rootless certification. | Not present in the recovered snapshot; not inferred. | current454a2c52a; Native snapshot entry 8678, row 119. |
| 120 | Recover prior native workspace decisions | `completed` | Native owner-history evidence recovered and source reconciled; older September direct conversation remains a corpus gap, not a contrary ruling. | Not present in the recovered snapshot; not inferred. | current5366bd864; Native snapshot entry 8678, row 120. |
| 121 | Rebase onto rewritten origin main | `completed` | Existing report records clean rebase to31d61546 with no local-only commits or push; later judge rejected missing verified operation evidence. Not independently reapproved. | Not present in the recovered snapshot; not inferred. | native session entries 8676 and 8682; Native snapshot entry 8678, row 121. |

## Count and present-day disposition

The row check is **59 unique IDs: 45 `completed`, 12 `in-progress`, and 2 `not-started`**. The
14 unfinished IDs are **20, 22, 23, 44, 46, 47, 48, 49, 50, 51, 52, 53, 67, and 78**; rows 49
and 51 were `not-started`. No missing row was found in the recovered final snapshot. IDs are
non-contiguous; this is the final snapshot, not a census of every task ever created.

Current interpretation is separate from the table's historical statuses:

- Row 48's sharing policy is settled: only selected pack machine scope and existing configured
  sharing. Exact backing/link geometry remains engineering design, not a new access-policy vote.
- Row 78's repair was superseded by completed row 81 and is shipped; its old `in-progress` state
  remains in the historical table.
- Rows 20, 44 and 51 still require separate authorization for external or credential-bearing
  trials. Row 22 remains outside YOLO work; row 23 is evidence transport only. Rows 46 and 47
  remain the open MCW2/MCW3 decisions. Row 49 remains unbuilt; 50 and 52 require real platform
  evidence; 53 retains its named launcher follow-ups and owner-deferred `.local` defect; and 67
  needs current-session ownership plus natural delivery evidence.
- Current-session items 123/124/125 are additions after this 59-row snapshot and are not counted or
  substituted for any historical ID.

The [concurrent-workspace design](../design/macos-user-model.md#7-the-ruled-model-two-modes-one-profile)
records the one-UID architecture, private workspace homes/state, and selected shared scope. Its
[OQ-MCW2](../design/macos-user-model.md#decision-ledger) and
[OQ-MCW3](../design/macos-user-model.md#decision-ledger) remain open. The
[engineering sketch](../design/macos-user-model-plan.md#MMP-D25) remains unbuilt and is
not a build handoff; the existing account-home hold remains in place. No private session text,
credential, or native runtime result is supplied by this inventory.
