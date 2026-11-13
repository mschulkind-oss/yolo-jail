---
title: "Roadmap"
status: accepted
stage: CURRENT
tags: [roadmap, priorities]
summary: "The order in which yolo-jail's open work should be taken up, with the reason for each place; each linked document owns its own state and next step."
vantage:
  status-chip: true
---

# Roadmap

**Reconciled:** 2026-11-13.

This page owns the order in which open work is taken up, and why. Each linked document owns its state, next step and
gates; read it before starting, because a place on this list is not permission to build.

## Ordering basis

Open work falls into three groups, in this order, by what it waits on:

- **[Buildable now](#buildable-now-by-an-agent):** an agent can do it without an owner ruling, a Mac, a rootless
  host or an outside account.
- **[Waiting on an owner ruling](#waiting-on-an-owner-ruling):** a filed 💬 question holds it.
- **[Waiting on a Mac, CI, a rootless host or an outside account](#waiting-on-a-mac-ci-a-rootless-host-or-an-outside-account).**

Within each group: **verified live defects first**; **then work that unblocks other work**; **then smaller
independent steps** (builds, graduations, records, measurements). **A ruling that only closes a document ranks
last.** Many documents appear in more than one group: an agent half sits in the first group, and the half that
needs a ruling or a Mac sits in the others.

## Buildable now by an agent

1. Fix the tests that failed once in the 2026-11-12 landing gates and passed alone
   ([the backlog's list](BACKLOG.md#landing-gate-flakes-2026-11-12)), with
   [the 2026-11-09 list](BACKLOG.md#landing-gate-flakes-2026-11-09) — a flaky landing gate costs every lane a rerun.

2. Graduate [legacy config compat](../design/legacy-config-compat.md) into [the config reference](../reference/config/README.md)
   and delete the design. Every row is built, the message sweep last, enforced by
   `TestNoMessageNamesALegacyConfigFileOrASnippet`; [migration](../design/config-migration.md)'s old-reader removal
   waits for a later release.

3. [Finish ongoing launch preparation and explicit update-and-wait](../design/launch-update-preparation.md) — its
   [combined-source review](../design/launch-update-preparation-plan.md#review-2026-11-12) ran on 2026-11-12 and its
   defects are fixed; a fresh non-agent nested launch is agent work, and the rootless and Darwin acceptance is not; the
   [build-update slices](../design/pi-extension-store-builds.md) reuse the same pipeline.

4. [The approval and broker foundation](../design/boundary-broker.md#11-the-build-in-the-maintainers-five-steps) —
   the maintainer set its order on 2026-10-23: brokered sources with read and write sets, actionable toasts, a host
   CLI to view and approve, a web UI out of tree on that CLI, then a talk about merging it. The agent steps now:
   - [Pack brokers](../design/pack-brokers.md): [the HTTP plan](../design/pack-brokers-http-plan.md)'s Task 2
     closure is sealed; Task 4, publication and the per-cell proof, is next, and publication stays off until then.
   - [The approval queue's history](../design/approval-experience/request-history-and-privacy.md): the eviction
     count is built; routing the draft host-history extension through the boundary broker is next.
   - [Access introspection](../design/access-introspection.md)'s Task 4 waits on the authorization its plan names.

5. Repair the reference docs that drifted ([the backlog's list](BACKLOG.md#doc-drift-2026-11-12)): four describe
   `mcp_presets` as live, and patched forks and the pack system each carry one stale row.

6. Smaller builds no ruling holds:
   - [The color pass](cli-visual-polish.md)'s glyph and status-column items, which change plain output; Group C's
     color is done.
   - [Resource groups](../design/resource-groups.md)' independent review of the descriptor-relative prototype, from
     [its plan](../design/resource-groups-plan.md), with mapped execution kept refused; its native evidence needs a
     real Linux host.

## Waiting on an owner ruling

Each entry names the 💬 questions that hold it. Defects whose repair waits on a ruling come first.

1. Launch defects measured 2026-10-19: Apple Container builds no patched fork while yolo's own image builder runs
   ([OQ-XB2](../design/pi-extension-store-builds.md#OQ-XB2)); a `Ctrl-C` during provisioning is reported as an
   unwritable log ([OQ-PLW1](../design/provisioning-interrupt.md#OQ-PLW1)); a podman launch on a Mac cannot use a
   workspace reached through a symlink ([OQ-SWB1](../design/symlinked-workspace-binds.md#OQ-SWB1)), and its
   verification needs podman on a Mac.

2. Rulings that hold a defect's fix: [the Kilo special-casing](../design/gateway-provider-packs.md#OQ-GP4);
   [`--no-daemon` for in-jail `codex`](../research/codex-background-service.md#OQ-CDX3), since `codex agents` starts
   the daemon yolo turns off; [whose OpenAI login host pi uses](../design/pi-host-openai-auth.md), since its first
   refresh replaces yours; and [a leftover `mcp-adapter.json` at the host](../reference/mcp-configuration.md#OQ-MC2),
   which host apply never deletes and pi keeps loading, with [OQ-MC1](../reference/mcp-configuration.md#OQ-MC1).

3. Calls filed 2026-11-12, each holding a build: [OQ-GW16](../design/github-write-scopes.md#OQ-GW16), on REST deletes,
   holds [the GitHub work-kinds plan](../design/github-write-scopes-plan.md)'s Task 2 (Task 1 is built);
   [OQ-SP6](../design/per-session-profiles.md#OQ-SP6), where codex's session homes live, holds the rest of the codex
   unlock of per-session profiles, whose model-menu half is built; and [OQ-KH1](../design/claude-keychain-at-host.md#OQ-KH1)
   with [OQ-KH2](../design/claude-keychain-at-host.md#OQ-KH2) hold [Claude's sign-ins in the macOS Keychain at
   `yolo host`](../design/claude-keychain-at-host.md), so a Mac's `yolo host` has no one MCP sign-in until they are
   ruled; its Mac checks size the second.

4. Calls that had no filed home until 2026-11-12: [the setup census's silent drops](../design/backend-parity.md#OQ-BP6)
   with [OQ-BP7](../design/backend-parity.md#OQ-BP7); [the pi extension's leaks](../design/simultaneous-auth-and-pack-isolation.md#OQ-PAS3)
   with [OQ-PAS4](../design/simultaneous-auth-and-pack-isolation.md#OQ-PAS4); [the color pass's frozen `yolo run`
   progress golden](cli-visual-polish.md#OQ-VP1); [the drafted rewordings of the macOS direction](../research/macos-backend-performance.md#OQ-MB2);
   [whether a successful `yolo host apply --assert` names a next command](../reference/report-tiers.md#OQ-RO10); and
   the 2026-09-26 open-source audit's four calls, [OQ-OS1](open-source-hygiene.md#OQ-OS1) to
   [OQ-OS4](open-source-hygiene.md#OQ-OS4).

5. [Enable profile-served daemons on attach without ending sibling sessions](../design/attach-daemon-activation.md#OQ-AD1)
   — the reported late Bedrock selection reaches a restart prompt.

6. [Computed briefings](../design/computed-briefings-plan.md), [OQ-CBP1](../design/computed-briefings-plan.md#OQ-CBP1)
   and [OQ-CBP2](../design/computed-briefings-plan.md#OQ-CBP2) — the maintainer, 2026-10-24: a pack's briefing should
   tell each workspace's agent exactly how to finish a change according to the grants that workspace was approved.

7. Rulings holding other builds: [program delivery](../design/program-delivery.md)'s [mise pin of `pnpm`](../design/program-delivery.md#oq-pd19), with
   [autoprune's `$GOBIN` class](../design/program-delivery.md#oq-pd20) and [a pack's agent-CLI pin](../design/program-delivery.md#oq-pd21);
   [whether a pack service's in-jail daemon is disclosed](../design/trust-paths.md#OQ-TP11), as no launch line names
   the wire bridge; [whether copilot and oh-omp alone get `-p bedrock`](../design/bedrock-plumbing.md#OQ-BR24);
   [live config apply's next-session moves](../design/live-config-apply.md#OQ-LA7-question); [whether a Mac fork build
   installs its base's `node_floor` first](../design/forked-programs-as-packs.md#OQ-FP11), beside the maintainer
   naming the real forked program its measurement needs; [the boundary broker's write kinds for every
   workspace](../design/boundary-broker.md#OQ-BB23); and [workspace widening](../design/workspace-widening.md#OQ-WW1),
   which its built mechanism awaits.

8. [The slot split's migration window](../design/slots-and-contributions.md#OQ-D6) — `exposes`,
   [the pi extension-tree rework](../design/pi-pack-extensions.md) and the worktree lines all wait on it. Then
   [the slots' other calls](../design/slots-and-contributions.md).

9. [Which packs a workspace config may select](../design/workspace-packs.md#OQ-WP1), then
   [what a collaborator sees and can refuse](../design/workspace-packs.md) — `mcp_presets` is retired, so until this lands a repository cannot
   give its collaborators the browser, which the presets did.

10. [The VM-local disks](../design/vm-local-volumes.md): [default or opt-in](../design/vm-local-volumes.md#OQ-VL1),
   [`~/.cache`](../design/vm-local-volumes.md#OQ-VL2), [database folders](../design/vm-local-volumes.md#OQ-VL3) and
   [how a new disk fills](../design/vm-local-volumes.md#OQ-VL4) — the largest Mac speedup measured waits on it. Beside
   it, the maintainer's 2026-10-08 request for [tools from a private Homebrew tap in every jail](../design/private-tap-programs.md),
   [OQ-PT1](../design/private-tap-programs.md#OQ-PT1) to [OQ-PT5](../design/private-tap-programs.md#OQ-PT5).

11. Provider rulings later work builds on: [whether the natively-implements rule reaches a list a pack
   declares](../design/pi-codex-provider-shadowing.md#OQ-4); [what `-p` names](../design/providers-and-profiles-redesign.md#OQ-PP1),
   which the provider reference's rewrite waits on; [the provider gate's narrow form and the removed `null`
   plan's case](../design/provider-reachability-and-model-names.md); [which set member's overlay wins a shared key](../design/active-provider-sets.md#OQ-AP4);
   [web search on Bedrock](../design/bedrock-web-search.md), since no agent gets search from Bedrock on runtime; and
   [making the narrowed list the only models a Bedrock credential can call](../design/model-allowlist.md) — a pi
   subagent called a model its menu no longer offered, and only the credential refuses every path.

12. [Notch convergence](notch-convergence.md)'s `rmw` rule on [a table not declared in full](../reference/config-ownership.md#OQ-CO15),
    with [OQ-CO16](../reference/config-ownership.md#OQ-CO16).

13. [The starting tab's Ctrl-C in a shared jail](../design/jail-lifetime-last-session-wins.md#OQ-JL10), whose keeper
    build waits on [the sidecars and doorbell](../design/agent-event-watchers.md); [the boot snapshot and
    diagnostic dial](../design/diagnostics-past-the-boundary.md), since a refused boot keeps no record of the jail's
    state; and [what just happened in this jail](../design/jail-history.md), whose slices after the first wait on its
    questions.

14. [Whether pack-declared traps fold into the agent directory map](../design/agent-directory-map.md#OQ-AM8) — the map
    would supersede [the traps design](../design/pack-declared-file-diagnostics.md), so rule it before building either;
    the rest of [the map's questions](../design/agent-directory-map.md) hold its pi slice.

15. [How the Claude footer moves into a Claude Code plugin](../research/claude-code-extensions-footer.md), with
    [how yolo manages Claude Code mods](../research/claude-code-mods-management.md), whose later build steps wait on it.

16. [How far one login is shared](../design/universal-login-sharing.md) — with the host, and between two agents of one
    provider; [its sketch](../design/universal-login-sharing-plan.md) waits on all three questions. Rule the canonical
    copy's location with it, so a `yolo` state reset cannot destroy a login.

17. [Store cleanup that runs even after the last launch](../design/automatic-store-gc.md) — the owner wants no routine
    manual pruning. [Its seed](../design/automatic-store-gc-plan.md) and
    [the independent advice](../research/automatic-store-gc-advisory-20261028.md) keep the evidence.

18. [What the environment manager promises at each notch](../design/environment-manager-user-stories.md) — Q7 decides
    the Linux guest, now [a reserved setup](../design/named-setups.md#decision-ledger).

19. Ask-driven designs: [image paste](../design/image-paste.md#OQ-PA2), asked for on 2026-10-09, where how far Ctrl+V
    is consumed shapes the key handling, with [OQ-PA1](../design/image-paste.md#OQ-PA1) and
    [OQ-PA3](../design/image-paste.md#OQ-PA3); [whether yolo may hold an editor preference](../design/baked-editor-preference.md),
    asked for by the maintainer, whose first ruling decides an image change every jail pays; and
    [how a SandVault or Safehouse user tries yolo](../design/sandvault-safehouse-compat.md#OQ-NB1), then
    [which backend a SandVault setup gets](../design/sandvault-safehouse-compat.md#OQ-NB2), with
    [credentials](../design/sandvault-safehouse-compat.md#OQ-NB3) and [the trial's `.yolo/`](../design/sandvault-safehouse-compat.md#OQ-NB5).

20. [How a jail reaches a worktree's `.git` outside it](../research/herdr-integration.md#OQ-HR2), with [the other herdr
    calls](../research/herdr-integration.md) — measured: a read-only bind refuses every commit, a read-write one lets a jail prune the worktree. Beside
    it, [where herdr's approval front end ships](../design/herdr-approvals.md).

21. Storage: [the shared tool store's relocation contract](../design/shared-tool-store-relocation.md) with
    [its build handoff](shared-tool-store-relocation.md); [how one workspace splits across storage tiers](../design/storage-tiers.md)
    with [its sketch](../design/storage-tiers-plan.md), whose disk-presence and backend calls are the tool store's, so
    rule them together; and [isolating ordinary jail caches](../design/cache-isolation.md), which needs safe
    reclamation alongside it.

22. Calls left at one decision each, each with its facts and a leaning: [the `:ro` degradation rows](../design/composed-file-permissions.md),
    [whether messages name the guide's URL](../design/docs-website.md#OQ-DW3), [the wording style of yolo's messages](../design/message-style.md),
    [the macOS nix build sandbox](../design/macos-user-build-step-threat-model.md#Q2), [array-append pinning](BACKLOG.md#E5),
    [the pack system's other calls](../reference/pack-system.md), [relocating `.yolo` by a link](../reference/jail-home.md#OQ-JH1), [where the workspace layer's links are
    ignored](../reference/agent-briefings.md),
    [the suite's remaining lever](test-suite-speed.md#OQ-TS4), [whether integration sharding gets a local recipe](test-suite-speed.md#OQ-TS5), [the I/O priority default](../design/io-priority.md#OQ-IO7),
    [the AWS doorway's fetched host half](../design/host-notch-services.md#OQ-HS5),
    [the dry run's exit](../reference/report-tiers.md), which the code sets to 1 where the ruling says 0, and
    [the declaration-parity call](../design/declaration-parity.md#OQ-DP10).

23. [The macos-user workspace root](../design/configurable-workspace-root.md), [its whitelist](../design/configurable-workspace-root.md#OQ-CW2)
    first — on hold in its own doc until the
    second-account mode's plan reaches it, since the default mode allows a workspace anywhere.

24. Rulings nothing shipped waits on. Gating one later step: [the host-file permission asymmetry](BACKLOG.md#E2) with
    [its host-side twin](pack-host-management-plan.md), [the skills fan-out](BACKLOG.md#OQ-S4),
    [sharing a claimed generic name](../design/credential-sources-separation.md#OQ-ES6),
    [the pack-file conventions](../design/pack-conventions.md), [loophole env and guest fields](../design/loophole-packaging.md),
    [workspace MCP files](../design/workspace-mcp-sources.md#OQ-WM1), [host apply's posture](../design/host-render-target.md),
    [warning on a folder another pack keeps state in](../design/pack-files-for-agent-state.md#OQ-FS2),
    [whether a Mac bootstrap stops at a failed home layout](../design/pack-files-for-agent-state.md#OQ-FS4),
    [replacing a pack-files directory yolo made](../design/pack-files-for-agent-state.md#OQ-FS3) with
    [macos-user's stale home-root file](../reference/macos-user-home-tiers.md), and [removing a layout link whose workspace is
    gone](../design/pack-files-for-agent-state.md#OQ-FS5). Only closing a document:
    [an unmatched-reference hatch](../design/reference-mismatch-diagnostics.md#OQ-RM4),
    [pack telemetry](../reference/pack-system.md#oq-acp4), [AWS's key pair](../design/credential-sources-separation.md#OQ-ES8),
    [stateful-surface comments](BACKLOG.md#OQ-E4), [the upstream mise issue](../design/jail-state-separation-design.md),
    and [the `cache_relocations` level](cache-relocation.md#OQ-CR1).

## Waiting on a Mac, CI, a rootless host or an outside account

1. One session at a Mac, with `sudo`'s password and the runner stopped, completes [the benchmark](../research/macos-backend-performance.md#8-results)
   — [auto-capture](../design/program-delivery.md#decision-ledger), Podman Machine on applehv and
   [NFS from macOS's `nfsd`](../research/macos-vm-runtime-comparison.md#6-is-there-an-open-stack-with-faster-shared-folders)
   — first, as [the comparison's refresh](../reference/macos-no-vm-direction.md#when-to-refresh-this) waits on it. It
   also runs `yolo host -- claude` and `yolo host -- agy` for [the host floor's installer agents](../reference/host-agent-environment.md#HP-D2),
   and a macos-user launch with a dotted `packages` entry, the
   [unmeasured half of the attribute-path resolver](../reference/image-staging-vs-baking.md#what-a-packages-entry-names).

2. The owner's Mac and the Mac runner, for work whose code is in: [named setups](../design/named-setups-plan.md)' W-SU4
   step and the Mac witnesses of slices 11d to 11f, after which the Mac default moves to `seatbelt-self` when the owner calls the Seatbelt path ready;
   [the account mode](../design/macos-user-model.md)'s remaining slices and witnesses, with
   [W-TMP1](../design/macos-user-model-plan.md#W-TMP1) for its shared `/tmp`; [the Seatbelt profile's disclosure on a
   real launch](../research/agent-safehouse.md); [the keychain witness
   W-KC5](../design/keychain-from-a-jail.md); [the native TLS witnesses](../design/macos-native-tls.md);
   [the provisioner's Mac confirmations](../design/provisioner-sets.md), before its nix closure is built;
   [a pack's own tool built from its tree](../design/pack-buildable-programs.md), which fails under run-as-you mode;
   [JD-10's guest pack-tree cases](../reference/macos-user-provisioning.md#JD-10) a native run has not yet shown;
   [MCP sign-in sharing](../design/mcp-auth-sharing-plan.md)'s Mac witnesses (the owner checked Linux `yolo host`
   on 2026-11-13);
   [secret env scoping](../reference/providers.md#the-two-env-sets-public-and-private)'s macos-user rows;
   [the Bedrock key provider](../design/bedrock-key-provider.md)'s W-BK2 rerun with a static pair; and
   [the patched fork's remaining verification](../reference/patched-forks.md#14-unbuilt-and-unmeasured), with the
   maintainer's test of the migration kit.

3. [Image build reuse](../reference/image-staging-vs-baking.md)'s native Apple Container warm-launch validation (its
   dispatch passed on 2026-11-13); then [Podman Machines pinned to applehv](../research/macos-vm-runtime-comparison.md#32-on-a-shared-mac-folder),
   as Podman's installer gives libkrun, the slowest shared folder measured; then
   [OrbStack as a Podman host](../research/orbstack-as-a-podman-host-plan.md)'s native proof, when the maintainer
   dispatches `orbstack.yml`; and the maintainer pushes and dispatches [the Apple Container volume
   probe](../research/apple-container-file-cost.md#5-a-planned-probe-on-the-mac-runner).
   [The two-jail check](../research/macos-backend-performance.md#7-found-on-the-way-two-apple-container-jails-may-mount-one-ext4-disk)
   holds; the next `apple-container.yml` dispatch re-asks [the keeper's remaining Apple Container
   runs](../design/jail-lifetime-last-session-wins.md#7-what-i-would-build-in-order), and someone at the runner Mac
   upgrades `container` to 1.5.0 (its kernel prompt is interactive) before the VM-local probe.

4. The next `macos-user.yml` run confirms in CI what [a developer Mac ran](../design/macos-user-suite-on-a-developer-mac.md):
   the macos-user suite, [the bounded privilege lifecycle](../design/macos-user-privilege-lifecycle.md)'s narrow-sudo
   step, the macos-user keeper and [the capture fixtures](../reference/install-capture.md); it reads [the manual checks' item 4
   twin](runbooks/macos-user-manual-checks.md) for the first time; and it runs
   [I/O priority](../design/io-priority.md)'s hardware check for the first time. The ownership-enabled volume still
   needs a Mac with root.

5. A human with a subscription login finishes [the credential-view runbook](runbooks/claude-credential-view-measures.md)
   for [Claude's login without interception](../design/claude-login-without-interception.md): its Mac half ran on
   2026-10-24 and 2026-10-25, and the Linux parts and the rootless day remain. That releases
   [`yolo host -- claude` joining the shared login](../reference/claude-oauth-interposition.md), notch convergence's
   last held item, and records one browser login and one shared expiry for
   [the OpenAI service](../reference/agent-credentials.md#openai-measured).

6. A real rootless Linux host, which a nested jail is not, clears [a real reboot](../reference/podman-readiness.md),
   [a low-space collection with a jail up](../reference/image-retention.md#owed-host-steps),
   [the keeper's scope move and logout](../design/jail-lifetime-last-session-wins.md#8-what-done-looks-like),
   [per-jail host daemons'](../design/host-daemon-ownership.md) first launch, [live config apply's](../design/live-config-apply.md)
   rootless evidence, with [M6](../design/live-config-apply.md#LA-M6) and a live forward's traffic, [resource
   groups](../design/resource-groups-plan.md)' native delegation and placement, the patched fork's rootless check, [the in-jail nix root watcher](../reference/in-jail-nix-roots.md)'s
   host-side observation and [the approval queue's real-host checks](../design/approval-experience/browser-trust-boundary.md).
   A real Linux GPU host verifies [the graphics-driver tree](../design/gpu-graphics.md) before it graduates.

7. One live agent session per check, which no test may start: [the footer](../reference/agent-footer.md#what-has-and-has-not-been-observed),
   [Claude's LSP plugin](../reference/mcp-configuration.md#lsp-claudes-route-is-a-generated-plugin),
   [the via route](../design/wire-bridge-gateway.md#41-how-it-is-built),
   [menus under a model list](../design/model-lists-and-pickers.md#13-build-order-and-what-done-looks-like),
   [a switch in a provider set](../design/active-provider-sets.md#9-what-done-looks-like),
   [Copilot's config migration](../design/agent-directory-map.md#74-copilot), [a Kilo session](../design/gateway-provider-packs.md),
   [the restart prompt](../reference/attach-skew.md), [whether the plain agy slot delivers the agent](../design/pack-files-for-agent-state.md#OQ-FS1),
   [pi loading a pack's folder](../reference/pack-system.md#pi-packages), [host Chrome from `yolo host -- claude` and under
   macos-user](../reference/mcp-configuration.md#the-chrome-devtools-pack), [pi's one MCP sign-in at `yolo host -- pi`](../design/mcp-auth-sharing-plan.md), [codex, opencode and pi on
   Bedrock](../design/bedrock-plumbing.md#8-behaviour-this-design-fixes) under SSO, the Claude and Copilot unlocks of
   per-session profiles, [Pi launch selection](../design/model-lists-and-pickers.md#1411-pis-provider-flag-travels-only-with-an-explicit-model-built-2026-10-07)
   and [simultaneous native authentication](../design/simultaneous-auth-and-pack-isolation.md); launch arguments and
   source inspection do not establish authentication isolation.

8. A human with the cloud account clears [an SSO lapse mid-turn](../reference/agent-credentials.md#sso-chain-order),
   [settings-only Bedrock mode](../reference/providers.md#two-channels-split-by-payload-type),
   [an AgentCore gateway](../design/bedrock-web-search.md) and
   [a Claude usage-limit response](../design/wire-bridge-gateway.md#5-part-4--the-subscription-arm-and-opt-in-failover-ruled),
   which the failover build waits on.

9. The maintainer's own commits and launches clear [the `matt` pack's `briefing/`](../reference/pack-system.md#briefing),
   [its posture list at the host](../reference/pack-system.md#posture-lists-owed),
   [a dropped pack's host output](../reference/pack-system.md#retiring-a-dropped-packs-host-output),
   [the fzf pack](../examples/claude-fzf-pack/README.md#adopting-it), his pack's move to pi's package folder,
   [the listen-port fix where reported](../reference/wire-bridge.md#what-can-hold-the-listen-port-before-the-bridge-does)
   and [the runner Mac's published-ports change](runbooks/mac-actions-runner.md).

10. A live `llama-server` and one manual turn per agent clear [the `llamacpp` provider](../../packs/llamacpp/README.md),
    whose spellings have never met a server; copilot is the one to try first.

11. The mise use record's first 30-day window gives [the disk footprint's re-measure](../reference/disk-footprint.md#owed-measurements)
    its first offer to check, on the host.

12. Nix dropping Intel Macs, when nixpkgs 26.05's security fixes stop on 2026-12-31, brings back [what replaces the
    Intel macOS runner](../research/macos-support-matrix.md#OQ-MX1), deferred until then.

13. The maintainer files [VZ's memory request](../research/macos-vm-runtime-comparison.md#43-draft-feedback-assistant-request)
    and the upstream reports drafted with the VM-local design, on [VZ's virtio-fs cache policy](../research/apple-container-file-cost.md#21-per-file-cost-not-bandwidth)
    and [VZ keeping freed guest memory](../research/macos-vm-runtime-comparison.md#42-adding-a-balloon-to-apple-container-ourselves)
    — last, since only Apple can ship either fix.

## Boundaries

- **Not committed to:** the unverified candidate ideas filed in [`BACKLOG.md`](BACKLOG.md#ideas-from-deleted-docs).
  **Iced**, with what would thaw each: [`yolo cache relocate`](cache-relocation.md) (someone wanting it),
  [in-jail nix on the container Macs](setup-support-gaps.md#2-ranked-gap-backlog) (a user for whom `packages:`, mise
  and macos-user all fall short), [an in-jail agent's request for a live config apply](../design/live-config-apply.md#LA-S5)
  (the boundary broker's request store and doorbell being ready) and
  [a central host watcher](../research/central-yolo-watcher.md#7-what-would-have-to-be-true-before-building-one) (a
  duty that must hold a listener across launches).
- **Ids Vantage cannot index**, reached here by name: [Q1 to Q9](../design/environment-manager-user-stories.md#Q1),
  [9.2 and 9.6](../design/host-render-target.md#9.2), [CFP-1 to CFP-3](../design/composed-file-permissions.md#CFP-1),
  [Q2 and Q3](../design/macos-user-build-step-threat-model.md#Q2), [SS-6](../design/jail-state-separation-design.md#ss-6),
  [E1, E2, E5 and CO](BACKLOG.md#E1), [the host-files mode](pack-host-management-plan.md#open-questions), and two
  outside this queue: [OQ-SH1](../research/macos-support-matrix.md#OQ-SH1), which blocks nothing, and
  [JD-4](../reference/macos-user-provisioning.md#JD-4), a maintainer follow-up.
- **With no id at all:** [Go comments citing sections by number](README.md#the-dangling-n-citations-in-go-comments),
  [check 2's two marker conventions](README.md#check-2-has-two-known-errors-and-a-convention-question-under-each),
  [the storage lifecycle's `min-free` values](../reference/image-retention.md#owed-host-steps), the unruled half of
  [OQ-ST2](../reference/pack-system.md#oq-st2) on user-reserved skill children, and whether a bare `-p` whose packs
  install no CLI should say it selects nothing.
