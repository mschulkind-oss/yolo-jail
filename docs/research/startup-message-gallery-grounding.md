---
title: "Startup-message gallery grounding: existing seams and their limits"
date: 2026-11-17
status: accepted
stage: CURRENT
tags: [research, startup, messages, terminal, gallery]
summary: "A bounded source survey retained for the local gallery design: caller tests, approval fixtures, renderer and progress seams, and the declaration-only scope prompt. This is evidence about existing code, not gallery implementation authority."
---

# Existing caller tests can ground a local message gallery

**Verification:** The original bounded source survey was inspected on 2026-11-17 at
`a2b6554ce4439fb695ccdf85ab9a9649a0f9ad2c`. Publication checked the finite source locators below
against `7883c182dd61aaf53d0b3e0c934f39ddfc569bff`; the named capture, rendering and scope-gate
seams have no intervening source diff. This retains the existing findings, not a new inventory.
The [design](../design/startup-message-gallery.md) owns the proposed architecture and its open choice.

Yolo manages a coding environment declaratively and renders it where an agent runs; confinement is
one setting. Making startup output readable must preserve the [report contract](../reference/report-tiers.md#why-its-this-way):
no quiet launch and no suppression of pack read/exec trust disclosures. The
[happy-path principle](../reference/happy-path-principle.md) requires a truthful next step at every stop.

## 1. Existing evidence and reusable patterns

| Existing source | Verified behavior | Limit for the proposed gallery |
| :--- | :--- | :--- |
| [`gallery.go`](../../internal/approvalfixtures/gallery.go), `Gallery` | Deterministic static HTML through `html/template`, with preview and native-evidence labels | Approval requests and notification previews, not terminal streams or physical emit-call identity |
| [`fixture.go`](../../internal/approvalfixtures/fixture.go), `Validate` | Supported/synthetic, captured/handwritten and evidence states; sanitized workspace/repository spellings | These labels do not prove a launch caller ran |
| [`manifest.go`](../../internal/approvalfixtures/manifest.go), `Fold` and `Encode` | One manifest-fold route and canonical JSON encoding | No terminal source mapping |
| [`gallery_test.go`](../../internal/approvalfixtures/gallery_test.go#L17), `TestGalleryIsCurrent` | In-memory generation compared to committed HTML; explicit update variable writes it | A fixture freshness check, not caller-deletion protection |
| [`Justfile`](../../Justfile#L68), `dev-storybook` | Focused fold/gallery tests, then a Python HTTP server bound to loopback | Does not refresh broker-captured request objects; public site build is a separate path |
| [`approvalcapture_test.go`](../../internal/cli/approvalcapture_test.go), `TestApprovalCapture` | Production approval constructors/presentation in fake host worlds; request-field comparisons with fixtures | Not an end-to-end network broker capture and not a launch-output capture |
| [`configapproval_test.go`](../../internal/cli/run/configapproval_test.go#L468) | Gate-level refusal/no/config-only acceptance tests check presentation and records | Output assertions are not a browsable catalogue |
| [`brokeredscope_test.go`](../../internal/cli/run/brokeredscope_test.go#L246) and [`scopeapproval_test.go`](../../internal/config/scopeapproval_test.go#L42) | Scope ordering and approval-record preservation | Existing scope behavior, not a new broker permission policy |
| [`launchflagdisclosure_test.go`](../../internal/cli/run/launchflagdisclosure_test.go#L27) | Before/after argv disclosure and a production call-site guard | One family, not all startup messages |
| [`patchedtreescallsite_test.go`](../../internal/cli/run/patchedtreescallsite_test.go) | `Run`-driven tree-disclosure tests with fakes | Useful precedent, not proof a complete startup can already be faked |
| [`inventory_test.go`](../../internal/tty/inventory_test.go) | Go syntax-tree inventory of ANSI emitters and private terminal probes | Not an exhaustive human-message census |

The [existing message audit](message-rewrites/README.md) provides breadth, but its line citations are a
historical snapshot. [`message-style.md`](../design/message-style.md) is still a draft: its proposed
rewrites are not settled style authority. None of the inspected machinery combines recorded caller
bytes, stream and physical emit-call identity into the proposed browsable gallery.

## 2. The scope-prompt trio

The source explains how declaration metadata can trigger a prompt without added roots:

1. [`WithCallBrokerDeclarations`](../../internal/config/callbroker.go#L33) adds selected call-broker
   disclosure text and declared roots. Git-remotes discovery is a separate branch.
2. `CallBrokerDisclosure` in that same file prints credential **path names** and environment **names**,
   not credential values.
3. [`compareScope`](../../internal/config/scopeapproval.go#L506) compares disclosure against the
   declaration record key independently of root/grant deltas. A changed non-empty disclosure prevents
   the empty-source skip. Dormant sources take their own early branch; declaration change alone is
   not a universal claim about every inactive source.
4. [`scopeBlock`](../../internal/config/scopeapproval.go#L610) defaults origin to
   "this workspace's .git/config" and replaces it only when a Git-config source exists; entry
   filenames can be appended. Counts describe roots, not declaration differences.
5. [`changePrompter.PromptReport`](../../internal/cli/run/preflight.go#L645) chooses the scope header,
   prints scope/grant blocks and counts, then presents config identity separately.
   [`printScopeBlock`](../../internal/cli/run/preflight.go#L482) physically prints the block;
   `scopeBlock` authored it. These are different origins, and both differ from the setting's origin.

Thus a non-dormant active broker's changed metadata can produce "Repository scope changed" with zero
root deltas and "Workspace config: no change." The finding does not establish that every empty or
inactive capability prompts. It does not authorize bypassing the gate or fixing wording in this task.

The design contrasts inactive/empty capability, active metadata change with unchanged admitted roots,
and a true root addition, each with an already-approved control. It must record the actual gate
outcome at the capture commit. The [scoped broker design](../design/scoped-broker-policy.md) owns the
separate permission contract; this gallery supplies no new assertion that a prompt must appear.

## 3. Rendering, width and streams are separate evidence

[`richtext`](../../internal/richtext/richtext.go) owns markup-to-ANSI/plain rendering. Known style tags
are processed; literal brackets such as `[y/N]` survive. Its inspected API has no hyperlink facility.
[`console.go`](../../internal/cli/run/console.go#L82) connects it to the launch's writer/color decision.
[`tty`](../../internal/tty) owns terminal width/color probes; [`termsafe`](../../internal/termsafe)
makes untrusted controls visible. HTML escaping does not establish terminal safety.

[`approvalPresentationWidth`](../../internal/cli/run/approvalpresentation.go#L546) combines an injected
terminal predicate with a real `os.Stderr` probe. Width zero disables hard wrapping; terminal
presentation has a 40-column floor. Injecting a boolean alone cannot prove a chosen-width capture.
Scope-table byte alignment and approval wrapping must be shown as production emits them, not repaired
by browser layout.

[`progress.Config`](../../internal/progress/progress.go#L71) supplies clock/tick/width seams. Default
grace is two seconds and heartbeat fifteen seconds. Warm steps stay silent; live redraws use carriage
return and erase-line. `TransientWriter` excludes redraws from persistent logs. The
[`launch progress wrapper`](../../internal/cli/run/launchprogress.go) and
[`launch tee`](../../internal/cli/run/launchlog.go#L264) preserve that distinction.
[`entrypoint.Env`](../../internal/entrypoint/env.go) separates terminal stderr from log-only output;
host launch and boot logs are not one universal transcript. Generated shell output is not Go printer
output. A browser preview is therefore a constrained interpretation of captured bytes, not proof of
what every terminal draws.

## 4. Bounded safety checks

Publication confirmed the proposed local HTML output is Git-ignored. No generated HTML, capture
JSON, new test, recipe, runtime service or production dependency was added by publishing the design.

The existing effect-suppression spellings are source-enforced:

- [`NoAutoCaptureEnv`](../../internal/cli/autocapture.go#L88): `YOLO_NO_AUTO_CAPTURE`.
- [`NoProgramReadinessEnv`](../../internal/paths/paths.go#L397): `YOLO_NO_PROGRAM_READINESS`.
- [`DisableEnv`](../../internal/selfupdate/channel.go#L50): `YOLO_NO_UPDATE_CHECK`.

Their existence is not proof a proposed scenario is harmless. The design still requires synthetic
inputs, injected effects and post-run checks; an entry that cannot be safely driven remains uncovered.
The survey's renderer/progress package tests passed during grounding, but no new gallery exists to
validate, and publication ran only document gates. No real transcript, user config, grants, credential
state or provider invocation was used as input.

## 5. Representative producer domains

These are the original survey's thirteen domains, not a fresh or exhaustive emit-call census. Their
source links identify the inspected ownership seams; the design defines the later per-site coverage
mechanism. A source that builds wording is not thereby the source that prints it.

| Domain | Existing source seams | Distinction retained |
| :--- | :--- | :--- |
| Front-door/host launch | [`startupbanner.go`](../../internal/cli/startupbanner.go), [`banner.go`](../../internal/cli/run/banner.go), [`run.go`](../../internal/cli/run/run.go) | Version banner versus launch facts and early refusals |
| Dependencies and selection | [`programneeds.go`](../../internal/packload/programneeds.go), [`packtree.go`](../../internal/cli/run/packtree.go), [`preflight.go`](../../internal/cli/run/preflight.go) | Selection closure versus missing binary/capability; unprobed is not missing |
| Forks/patches/extensions | [`forkbuild.go`](../../internal/cli/run/forkbuild.go), [`patchedforkline.go`](../../internal/cli/run/patchedforkline.go), [`patchedtrees.go`](../../internal/cli/run/patchedtrees.go) | Inventory, build, fallback, refusal and actual handed build |
| Pack trust boundaries | [`run.go`](../../internal/cli/run/run.go), [`packloopholes.go`](../../internal/cli/run/packloopholes.go), [`packhostgrants.go`](../../internal/cli/run/packhostgrants.go) | Delivered claims, inert capabilities and disclosure before execution |
| Command modification | [`launchflagdisclosure.go`](../../internal/cli/run/launchflagdisclosure.go) | Before/after argv versus a no-change path |
| Missing dotenv/environment | [`envsets.go`](../../internal/config/envsets.go), [`profilechannel.go`](../../internal/cli/run/profilechannel.go), [`sources.go`](../../internal/config/sources.go) | Missing/unreadable files, names versus values, hydration origin |
| Config/scope/grant approval | [`scopeapproval.go`](../../internal/config/scopeapproval.go), [`callbroker.go`](../../internal/config/callbroker.go), [`approvalpresentation.go`](../../internal/cli/run/approvalpresentation.go) | Separate config, scope, grant and declaration identities |
| Service readiness/reachability | [`packservices.go`](../../internal/cli/run/packservices.go), [`requiredservice.go`](../../internal/entrypoint/requiredservice.go), [`reachability.go`](../../internal/entrypoint/reachability.go) | Started versus reachable/required-ready, refusal versus override |
| Build/install/live progress | [`launchprogress.go`](../../internal/cli/run/launchprogress.go), [`readiness.go`](../../internal/entrypoint/readiness.go), [`progress`](../../internal/progress) | Warm silence, cold frames, subprocess summaries; no installation for preview |
| Attach/re-entry | [`run.go`](../../internal/cli/run/run.go), [`patchedforkline.go`](../../internal/cli/run/patchedforkline.go), [`patchedtrees.go`](../../internal/cli/run/patchedtrees.go) | Running tree/build versus current staging; attach decline versus fresh-launch exit |
| Entrypoint/runtime | [`bootsteps.go`](../../internal/entrypoint/bootsteps.go), [`surfaceloop.go`](../../internal/entrypoint/surfaceloop.go), [`bootrefusal.go`](../../internal/entrypoint/bootrefusal.go) | Ordered boot passes and generator failures; generated shell is not Go output |
| Failure/next step | [`preflight.go`](../../internal/cli/run/preflight.go), [`patchfailures.go`](../../internal/cli/run/patchfailures.go), [`reporter.go`](../../internal/cli/check/reporter.go) | Cause, outcome, responsible actor and remedy |
| Persistent/perf records | [`launchlog.go`](../../internal/cli/run/launchlog.go), [`launchcontext.go`](../../internal/cli/run/launchcontext.go), [`bootlog.go`](../../internal/entrypoint/bootlog.go), [`perf`](../../internal/perf) | Terminal-visible, transient, log-only, boot and timing records |
