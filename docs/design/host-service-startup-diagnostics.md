---
title: "Host-service startup failures should carry their cause, not its aftermath"
status: accepted
stage: DECIDED
next: "Audit the interrupted final-worker changes and complete unchecked caller, lifecycle and outcome cases before independent review and parent landing gates"
tags: [design, diagnostics, host-services, loopholes, launch, credentials]
summary: "A selected host service must validate the exact desired settings before replacing shared state, and cooperative daemons can return a bounded, attempt-specific refusal without log scraping."
vantage:
  status-chip: true
---

# Host-service startup failures should carry their cause, not its aftermath

**Status:** 2026-10-08. Landed: the `settings_check` preflight and its `yolo check` phase, the opt-in `startup_reason` channel with its singleton and per-jail readiness deadlines, private per-jail settings snapshots, the AWS pack's safe validator, removal of the routine `unnarrowed` launch notice, and a typed owner-local startup outcome from both owners (collected per launch, not yet rendered or sent to a keeper). Open: the lifetime cleanup items, and carrying typed outcomes through keeper, native and host-doorway callers; the unchecked rows in the [tasks](../plans/host-service-startup-diagnostics-tasks.md) are the list.

> **In short.** A failed host service should explain the refusal that prevented startup, with the pack's safe remedy, before socket and reachability symptoms obscure it. Validate the immutable settings this launch intends to use before touching a working shared service, and accept daemon-provided reasons only through a bounded channel tied to the exact spawn attempt.

**Why it matters.** A host daemon can correctly reject an unsafe credential configuration while the launch reports only a missing socket, dead endpoint and network failure. A shared historical log cannot identify which startup attempt produced a line.

**The shape.** A pack-declared pure settings validator runs on a private snapshot; a cooperative daemon may return one typed refusal over an inherited attempt channel; the launch carries that result through its real caller paths.

**Cost.** A new opt-in manifest contract and small lifecycle protocol; legacy daemons keep their existing behavior. Invalid desired settings stop a fresh launch rather than letting it attach to a previous profile.

**Start at [§3](#3-settings-validation-precedes-shared-lifecycle-changes)** — the safety invariant. The rest follows from it.

**Reads with:** [`../research/host-service-startup-diagnostics.md`](../research/host-service-startup-diagnostics.md) (anonymized observed failure and source investigation), [`../plans/host-service-startup-diagnostics.md`](../plans/host-service-startup-diagnostics.md) (tree-grounded implementation handoff), [`keychain-from-a-jail.md`](keychain-from-a-jail.md) (KC-D13's separate long-unlock readiness work), [`../reference/happy-path-principle.md`](../reference/happy-path-principle.md) and [`../reference/report-tiers.md`](../reference/report-tiers.md) (actionable stop and mandatory launch disclosures).

---

## 1. Scope and terms

A **settings preflight** *(coined here)* is an opt-in, pack-declared executable that answers whether the exact resolved settings snapshot can be accepted without starting the service or performing external work. It is not the existing `doctor_cmd`: AWS's doctor fetches and may mint credentials, while launch validation must be pure and cheap.

An **attempt reason** *(coined here)* is one structured refusal a daemon sends over a private channel inherited only by the process yolo just spawned. It is not stderr, a log line, a readiness-success signal, or evidence that a socket is healthy.

This design repairs startup diagnosis for pack-declared host services, including the two ownership models already in the tree: a host-wide singleton behind per-jail fronts, and a launch/keeper-owned child with an optional front. It does not change AWS policy, expand credential access, or make core know any service's setting names. [The research record](../research/host-service-startup-diagnostics.md) owns the observed event; this design owns the behavior.

## 2. Evidence in the tree

- `startPlannedLoopholes` starts services and then runs post-bind launch checks (`internal/cli/run/packloopholes.go`); a pre-bind refusal has no handle for those checks to inspect.
- A fresh terminal launch, the keeper, the native arm, and `yolo host` doorway startup cross different output/result boundaries. In particular, keeper output is framed and it has no terminal (`internal/cli/run/keeper.go`). A callee-only error is not a launch diagnostic.
- `SettingsFileFor(name)` is name-keyed and shared across workspaces, and `startExternalService` writes settings before starting the per-jail child (`internal/loopholes/settings.go`, `internal/cli/run/loopholesruntime.go`). Atomic replacement prevents partial reads, not a different launch replacing the path before a child opens it. Manifest loading currently resolves `{settings}` to this stable path (`internal/loopholes/load.go`), so an opted-in per-jail spawn must bind a private path at the runtime call site without mutating the shared record.
- `EnsureSingleton` may stop a live daemon whose recorded settings differ before spawning its replacement. A new invalid settings file must not reach that transition.
- `awsauth.Settings.Resolve` interpolates the configured profile into its missing-narrowing error, while `validPolicyJSON` reports arbitrary input keys (`internal/awsauth/settings.go`). The pack must project typed refusal kinds to fixed safe text; bounds and terminal sanitization alone do not prevent value leakage.
- `yolo check` currently runs declared doctors through `Set.RunDoctorChecks` (`internal/cli/check/sections_loopholes.go`), and aws-auth's `doctor_cmd` invokes `SelfCheck`, which can fetch/mint (`packs/aws-auth/loopholes/aws-auth/manifest.jsonc`). The new pure validator is a separate phase, not a replacement for valid-candidate health checks.
- The daemon's real socket acceptance remains the readiness authority. Existing per-jail startup also accepts a wrapper exiting cleanly when a child has made the service reachable; that behavior stays.

## 3. Settings validation precedes shared lifecycle changes

### 3.1 Contract

A host-daemon manifest may optionally declare `host_daemon.settings_check`, an argv vector using the same host-side token resolution as the daemon command. It executes directly—never through a shell—and receives a private `0600` file containing the exact flat, default-filled settings snapshot the daemon would receive. Keep the frozen serialized bytes in the caller; do not reread the validator's file as the publication source, since the validator runs as host code and could modify its input.

For a settings-check-enabled singleton, yolo atomically publishes those frozen bytes to the stable settings path while holding the existing singleton lifecycle flock; the singleton contract is to read/accept its startup settings before it makes its service socket ready, so a later launch cannot replace that path before consumption. No settings-check-enabled singleton may lazily reread the mutable stable path after readiness. For a settings-check-enabled per-jail daemon, yolo writes a separate private `0600` file from those same frozen bytes for that spawn and substitutes that unique path for `{settings}`. The validator's input and daemon's settings file are separate files. Yolo never relies on the name-keyed shared path for that opt-in spawn. Keep the per-jail file while the owned service can still read it; remove it only after spawn failure or when existing service teardown establishes that the owned service lifetime has ended. Legacy manifests keep the current stable path and behavior.

The validator is an explicit pack contract: it reads only its snapshot, performs no credential/API calls, daemon startup, state write or other side effect, and exits zero for acceptable settings. Nonzero means configuration refusal. It writes no setting values or secrets. Its bounded diagnostic must state the cause and a configuration remedy, using an explicit user-scope path where scope matters; core does not interpret its text or promote a service bypass as a repair. Empty output yields a generic validator-failed message and the log path is not used as a substitute. A pack must not forward raw resolver errors when those errors can contain configured values; it projects typed failure kinds to fixed safe reason/remedy text before producing a validator or attempt diagnostic.

The runner caps execution at **2 seconds** and captured stdout/stderr at **4 KiB**. Timeout, executable failure and nonzero refusal are distinct. Before display, discard terminal control/markup, collapse to bounded single-line fields, and cap each rendered message/remedy at **600 runes**. Never print settings bytes, argv contents, environment, credential responses, or historical log contents.

### 3.2 Launch and `yolo check`

For a launch, validate only a service that is selected, enabled, active on this host, admitted for this backend, passes the pack-origin gate and the manifest placement rule, and will actually be started for that launch. For `yolo check --no-build`, use its current configured pack selection and existing activity/origin/placement gates, without requiring a particular launch to be underway. These gates are evaluated before invoking pack code. Because launch preflight moves ahead of the ordinary daemon-start disclosure, print a separate non-suppressible line naming the source pack, service and settings-validator execution before invoking it; keep the complete existing host-code disclosure before the daemon itself starts. `yolo check` likewise names the selected pack's validator before invoking it.

Resolve once from the current merged configuration into frozen serialized settings bytes. Create the validator's private input file from those bytes. On a fresh launch, run the pure validator before expensive image/provisioning work and before publishing shared settings, killing/replacing a singleton, starting a per-jail child, or publishing a front. If validation passes, publish/launch from the frozen bytes held by yolo—not by rereading the validator's file. A settings-check-enabled singleton publishes those bytes atomically while holding the singleton lifecycle flock, before drift evaluation or any stop; it must consume them before making its socket ready, and may not lazily reread the shared path afterward. A settings-check-enabled per-jail spawn gets a separate unique private settings file written from those same bytes after validation; a concurrent workspace launch cannot replace it. Keep it until that owned service can no longer read it. Services without the opt-in retain the legacy name-keyed settings path. A validator result for `yolo check` never writes either daemon settings path.

A preflight refusal is fatal to that new launch and names the service, original reason, pack-provided remedy, `yolo check --no-build` on the host, and retry. It does not spawn a front or fall back to a currently running service. A valid already-running singleton and its current clients remain untouched on this refusal; they continue with the previous settings. A later valid settings change retains existing host-wide behavior: it may restart the singleton and affects every front using it, as the disclosure already states.

`yolo check --no-build` resolves settings from the current host config and runs the same pure validator against its own private snapshot. It does not stage or overwrite daemon settings files, consult absent/stale staged bytes, invoke `doctor_cmd` as a substitute for validation, or change a running daemon. The validation runner itself never invokes `doctor_cmd`, `SelfCheck`, minting or fetching. Preserve the existing doctor as a separate health check: after successful validation, run this service's declared doctor exactly as today; when this opt-in validator refuses, times out or cannot execute, report that validation failure and skip only this service's doctor for this check invocation. Continue checks for other services. Tests must prove valid candidates still reach the existing doctor path and invalid candidates do not.

A manifest with no `settings_check` keeps current behavior. Validator output is never treated as approval: exit zero only means that this snapshot passed that pack's declared check.

## 4. Attempt-specific daemon refusals

### 4.1 Channel and producer contract

A host daemon may opt into `host_daemon.startup_reason: true`. Yolo creates a private local stream socket pair per spawn, passes only the child endpoint as an inherited extra file, and supplies its descriptor plus a random attempt token in environment variables (never argv). The child sends at most one length-prefixed JSON record, at most **4 KiB**, containing protocol version, exact loophole name, exact attempt token, a closed reason class, a short reason and an optional remedy. Allowed classes are `configuration`, `dependency`, `permission`, and `internal`. Producer text must be secret-free, single-line prose and must not contain settings values, credentials, argv, environment or log excerpts.

The parent reads only during the existing **5-second** host-service readiness window; it never waits for EOF. A record is accepted only when size, version, service, attempt token, class and framing all match. The parent closes both ends on readiness, refusal, process exit or timeout. A descendant retaining its endpoint cannot hold cleanup open; late, malformed, overlong, wrong-attempt or noisy data is discarded and reported only as a channel fault. Closing the reason channel never establishes readiness: socket/endpoint acceptance remains authoritative.

The channel carries refusal/failure only, not a general `ready`/`waiting` protocol. An opted-in daemon that supplies no valid reason remains compatible with the existing socket, exit and timeout outcomes. Daemons without the declaration receive no descriptor and run exactly as before. No stderr or shared service log is parsed for a cause.

### 4.2 Outcomes and presentation

Represent the start result distinctly as: configuration refusal; validator execution failure/timeout; executable start failure; cooperative daemon refusal; process exit before readiness; readiness timeout while alive; reason-channel fault; and socket/front/endpoint transport failure. A cooperative refusal is evidence about that spawn only. The corresponding log remains useful for manual diagnosis, but no log content is read to infer the cause.

Print the trusted, sanitized reason and remedy as the primary diagnostic; keep a downstream socket or reachability failure subordinate as a consequence. Preserve existing host-loopback severity exactly: requested/shared dispositions still refuse when an enabled service is unreachable, unsupported/unknown dispositions do not become fatal merely because they are unsupported. No new `YOLO_ALLOW_*` hatch is added. A settings preflight refusal is independently fatal because the new launch must not silently use a different profile.

Carry structured outcomes through the production boundaries, not only the service starter: terminal launch, keeper framed result/stream, native `Run`, and `HostDoorways.Start`/`yolo host`. A fresh-launch refusal must unwind only resources owned by that launch. It must not stop a shared singleton after an invalid preflight. Keeper logs alone are not user-visible propagation.

## 5. Compatibility and limits

- Legacy daemons with neither opt-in field continue through the current start path; no mandatory protocol, argv or environment change applies to them.
- Clean daemonizing wrappers remain supported when a service is reachable. For those wrappers the channel can report only a failure actually sent by the cooperative producer; the connected socket, not wrapper liveness, determines readiness.
- Crash, timeout, missing executable, endpoint/front transport failure and malformed channel remain separate outcomes. The channel is not a log scraper or an alternate reachability check.
- Host-singleton settings remain host-shared. This work protects a valid running service from a *preflight-invalid* replacement; it does not make one singleton hold concurrent per-jail profiles. Valid later settings changes retain the disclosed shared restart semantics. An opt-in per-jail daemon gets a unique immutable settings path so another workspace cannot substitute its startup bytes; legacy services retain the existing shared settings path.
- No agent CLI or AWS API is run by tests. The AWS validator reuses `Resolve` for the accept/refuse decision, but the pack maps typed refusal kinds to fixed safe diagnostics and never forwards raw `Resolve` errors, which can contain configured profile names or policy keys. Tests prove those values do not appear. The pure validator does not run `aws --version`, detect SSO form, fetch, mint or widen credentials. `yolo check` may still run the existing doctor after a successful validator; tests use a fixture doctor, not an AWS API.
- The existing fixed readiness budget remains unchanged. The keychain design's KC-D13 unlock wait, `ready`/`waiting` protocol and larger budget remain a separate future design/build requirement; this failure-only channel does not satisfy them.

## 6. What done looks like

1. A selected AWS fixture with missing narrowing fails at the actual launch boundary with a fixed safe reason naming the missing narrowing and an explicit user-scope remedy; it does not echo the configured profile. No networking workaround is presented as the repair.
2. `yolo check --no-build` validates newly edited desired settings even when the stable settings file is absent or stale. The validator runner itself has no doctor/mint/API behavior. For a valid candidate the existing doctor still runs; for a refusal, timeout or validator execution failure only that service's doctor is skipped, while unrelated checks continue.
3. With a valid singleton already serving, an invalid new snapshot leaves its process/settings/fronts intact and refuses the new launch before it can attach to the old profile. Concurrent distinct snapshots cannot cause one attempt to validate bytes from another.
4. Two concurrent workspaces starting the same settings-check-enabled per-jail service receive distinct immutable `{settings}` paths; each delayed daemon reads its own validated bytes even when the other launch publishes and spawns first. The private file remains until owned-service teardown; a legacy daemon still receives the stable path.
5. A real spawned cooperative daemon refusal reaches terminal, keeper, native and host doorway users; planting a contradictory historical log entry never changes that result. Deleting any production forwarding call makes its behavioral regression fail.
6. Missing executable, crash, timeout, transport failure and malformed/late/wrong-attempt/oversized reason records remain distinct, bounded and attributable. A child-held endpoint cannot hang cleanup.
7. No setting/secret, raw argv, environment or log excerpt reaches user output. AWS profile sentinels and arbitrary policy-key/value sentinels are absent from validator output, attempt records and rendered diagnostics; existing disclosure and reachability severity tests stay true.
8. A legacy daemon and a clean daemonizing wrapper still start under existing rules; socket acceptance remains the readiness authority.
9. The manifest interface is exercised by generic fixtures and core contains no AWS-specific key, name or policy branch.

## 7. AWS permission-mode presentation

The 2026-10-07 owner ruling supersedes the older routine-disclosure requirement in
[the SSO decision ledger](sso-backed-bedrock.md#13-decision-ledger): remove routine
`unnarrowed` launch/startup notices entirely, not merely their warning color. Using the
configured permission-set/profile policy as-is is a valid intentional route, not unrestricted
AWS access. Keep explicit user-scope opt-in, default false, conflict refusals, actual errors
and unrelated pack read/exec trust disclosures. Keep the route inspectable on request.
Do not add an AWS-name branch or a new generic severity feature solely for this request.

This change remains unaccepted outside main, alongside the startup-diagnostics candidate.
The published runtime still has the older notice; the ruling does not change credentials,
configuration scope or permission policy.

## 8. Decisions and deferred work

The scope, optional manifest contract, failure classes, ownership-specific settings snapshot rules, doctor compatibility, safe pack-owned error projection, outcome forwarding, default bounds and legacy behavior above are settled. No owner ruling is outstanding. The exact helper/type decomposition is an implementation choice only where it preserves these observable contracts.

Deferred, not silently included: readiness-success/wait lines or unlock-duration expansion for KC-D13; adding per-jail credential profiles behind a host singleton; changing current loopback reachability dispositions; allowing workspace-scope AWS settings; replacing the existing daemon doctor/launch check; or broad diagnostics redesign.
