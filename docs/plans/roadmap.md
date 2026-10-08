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

**Reconciled:** 2026-10-07

This page owns the order in which open work is taken up, and why. Each linked document owns its state, next step and
gates; read it before starting, because a place on this list is not permission to build.

## Ordering basis

**Verified live defects first**, each found in the code during this reconciliation, with fixes an agent can make now
ahead of fixes awaiting a ruling. **Then work that unblocks other work**: a ruling or step holding a later build.
**Then smaller independent steps**: builds, measurements, re-verification, graduation. **A ruling that only closes a
document ranks last.** Work needing a Mac, a live human session, a real rootless host or an outside account follows
under [External waits](#external-waits).

## Immediate continuation

Take these current launch problems before the older queue below. Each source document owns
candidate state, acceptance gaps and verification; a place here does not make its code landed.

1. [Finish host-service startup diagnostics](../design/host-service-startup-diagnostics.md),
   with [its plan](host-service-startup-diagnostics.md) — the preflight, deadlines and typed
   startup outcomes are built; S1 lifetime cleanup and carrying the typed causes through the
   keeper, native and doorway paths remain.
2. [Enforce the active Pi profile set with a yolo-shipped extension](../design/simultaneous-auth-and-pack-isolation.md)
   — [OQ-PAS2](../design/simultaneous-auth-and-pack-isolation.md#OQ-PAS2) rules out a Pi fork;
   a stock-Pi extension that replaces out-of-set providers is in review.

Native readiness and credential-store work retain their places below: assess
[the privilege prerequisites](../research/sandvault-macos-privileges.md) without broad root
cleanup, and rule [keychain scope/unlock/migration](../design/keychain-from-a-jail.md) without
sharing host credentials. Keep [storage relocation](shared-tool-store-relocation.md) and
[OrbStack](../research/orbstack-as-a-podman-host.md) separate from these urgent launch repairs.

## The queue

1. [Finish the patched-fork mode's verification](../design/patched-forks.md), with [its pi-extension
   companion](../design/patched-extensions.md) — the compatibility release is published; keep the remaining
   verification ahead of dependent work. Beside it: [the build line shown by its
   digest](../reference/report-tiers.md#why-its-this-way), ruled 2026-10-05 and not built; [whether a Mac fork build
   installs its base's `node_floor` first](../design/forked-programs-as-packs.md#OQ-FP11); his test with the migration kit.
2. [Per-jail host daemons (HD-R1)](../design/host-daemon-ownership.md) — built on branch
   `worktree-agent-a1e0c197b66474918` and held, not landed: it awaits the maintainer's choice of how old jails coexist
   with it. Next because it retires the machine-wide credential daemons; [who keeps Claude's login fresh with no jail
   running](../design/host-daemon-ownership.md#OQ-HD9) is still to rule, with the design's two other questions.
3. Rulings that hold a build: [whether a project's mise pin of `pnpm` stays hidden](../design/program-delivery.md#OQ-PD19),
   with [autoprune's `$GOBIN` class](../design/program-delivery.md#OQ-PD20) and [a pack's agent-CLI pin](../design/program-delivery.md#OQ-PD21);
   [whether an agent CLI updates once per machine](../design/program-delivery.md#OQ-PD23), as each workspace downloads
   every release itself; [how a host daemon hands the terminal a line](../design/boundary-broker.md#OQ-BB10), which the
   broker's no-notifier notice waits on; [whether a pack service's in-jail daemon is disclosed](../design/trust-paths.md#OQ-TP11),
   as no launch line names the wire bridge; and [whether copilot and oh-omp alone get `-p bedrock`](../design/bedrock-plumbing.md#OQ-BR24).
4. Rulings that hold a defect's fix: [the Kilo special-casing](../design/gateway-provider-packs.md), while claude gives
   every Kilo model without a declared window a 1M-token context; [`--no-daemon` for in-jail `codex`](../research/codex-background-service.md#OQ-CDX3),
   since `codex agents` starts the daemon yolo turns off; [whose OpenAI login host pi uses](../design/pi-host-openai-auth.md),
   since its first refresh replaces yours, with its catalog-refresh question; and [a leftover `mcp-adapter.json` at the
   host](../reference/mcp-configuration.md#OQ-MC2), which host apply never deletes and pi keeps loading, with
   [OQ-MC1](../reference/mcp-configuration.md#OQ-MC1).
5. [Rule whether a successful `yolo host apply --assert` names a next command](../reference/happy-path-principle.md#the-rules)
   — rule 2 calls such a success a dead end, `--assert` ends at its counts, and the line waits on a ruling against [the
   verdict block](../reference/report-tiers.md#the-verdict-block)'s "Only the dry run has a footer"; this entry is the
   work's only home. Rule [the dry run's exit](../reference/report-tiers.md#OQ-RO8) with it: the code exits 1 where
   [OQ-RO5](../reference/report-tiers.md#why-its-this-way) says 0, and the launch gate reads that exit.
6. [Build the background update mode for what yolo builds](../design/pi-extension-store-builds.md#XB-D42) — pi
   extensions are host builds made in one pool per launch, and `agent_updates: "next-launch"` exists; the background
   advance itself remains, behind a seam that does not read that value yet.
7. [Rule how the Claude footer moves into a Claude Code plugin](../research/claude-code-extensions-footer.md), with [how
   yolo manages Claude Code mods](../research/claude-code-mods-management.md) — the plugin ends a process per refresh,
   and the mods rulings rest on that route.
8. Rule [the VM-local disks design](../design/vm-local-volumes.md): [default or opt-in](../design/vm-local-volumes.md#OQ-VL1),
   [`~/.cache`](../design/vm-local-volumes.md#OQ-VL2), [database folders](../design/vm-local-volumes.md#OQ-VL3) and
   [how a new disk fills](../design/vm-local-volumes.md#OQ-VL4),
   with [the probe that checks its premise](../research/apple-container-file-cost.md#5-a-planned-probe-on-the-mac-runner)
   and [VZ's memory request](../research/macos-vm-memory-reclaim.md#6-draft-feedback-assistant-request) to file — the
   largest Mac speedup measured waits on it (pip install 8.35 s on a shared folder, 2.01 s on Apple Container's own disk).
9. Approve [the drafted rewordings](../research/macos-backend-performance.md#9-corrections-the-results-feed) of
   [the direction's ruled sentences](../reference/macos-no-vm-direction.md#what-the-numbers-bear-on) — the
   benchmark's other corrections are made, and the two-jail test passes in CI again.
10. [Time what a launch's spans leave out](../reference/perf-logging.md) — the provisioning stage in the jail perf log,
    and the Apple Container delivery test's reader past `image.*`, without which it cannot show the build skip's saving.
11. Record the Mac runs of 2026-10-02 and 2026-10-03 where each doc's `next` names them — agent work now, ahead of the
    rulings below because the context-mount cases free one: [the AWS doorway](../design/host-notch-services.md), [its
    Bedrock design](../design/sso-backed-bedrock.md), [A2's twin](macos-revival-and-distribution-plan.md), [the
    render-mark twin](handoff-guest-notch-macos.md) and [its design](../design/notch-scoped-config-contributions.md#43-render-mark-parity-on-macos-user),
    [item 1's twin](handoff-macos-user-open-threads.md) and [its runbook row](runbooks/macos-user-manual-checks.md),
    [DP-B6](../design/declaration-parity.md), [the footer](../design/agent-footer.md#what-i-would-build-in-order), [the
    build sandbox](../design/macos-user-build-step-threat-model.md), [the context mounts](../design/context-mounts.md#4-staging-and-the-tests-that-pin-each-piece),
    [Cachix](handoff-cachix-cache.md), [the in-VM copier](../research/macos-layer-reusing-image-delivery.md), [the login
    seed](../design/base-home-legacy-state.md) and [the storage classes](../design/durable-scratch-space.md).

    Assess [SandVault's setup-authorized macOS privileges](../research/sandvault-macos-privileges.md)
    with the native readiness work: reported startup denials and exit prompts expose prerequisites
    that account existence and compilation do not establish. Borrowing its setup/runtime split
    must not authorize arbitrary root operations or account-wide termination of sibling workspaces.
12. [Rule the macos-user workspace root](../design/configurable-workspace-root.md), [its whitelist](../design/configurable-workspace-root.md#OQ-CW2)
    first — a `/var/root` home passes until it replaces the blacklist, and tightening later withdraws what users rely on.
13. [Decide whether a workspace config may feed host dotenvs and mounts into a jail](../research/agent-safehouse.md#OQ-AS3)
    — a committed config still can, and notch convergence's `env_sources` item waits on it.
14. [Rule which packs a workspace config may declare](../reference/pack-system.md#OQ-PK1), then [retire `mcp_presets`](../design/mcp-presets-removal.md)
    — the retirement rests on that boundary and on the wrapper's browser report, and ends macos-user's refusal of presets.
15. [Rule the slot split's migration window](../design/slots-and-contributions.md#OQ-D6), then [the manifest
    language](../design/manifest-language.md) with [the slots' other calls](../design/slots-and-contributions.md) —
    `exposes` and [the pi extension-tree rework](../design/pi-pack-extensions.md) wait on the first.
16. Provider rulings later work builds on: [whether the natively-implements rule reaches a list a pack declares](../design/pi-codex-provider-shadowing.md#OQ-4),
    which holds what [its broad reading](../design/pi-codex-provider-shadowing-plan.md) left in place, and [what `-p`
    names](../design/providers-and-profiles-redesign.md), which the provider reference's rewrite waits on.
17. [Notch convergence's held items](notch-convergence.md) — `env_sources` waits on item 13, the one `rmw` rule on [a
    table not declared in full](../design/config-ownership-and-promotion.md#OQ-CO15) (rule [OQ-CO16](../design/config-ownership-and-promotion.md#OQ-CO16)
    with it), and `yolo host -- claude` joining the shared login on the credential view's measures, below.
18. [Rule the provisioner's remaining calls](../design/provisioner-sets.md), corporate-CA trust first — it shipped on its
    leanings under delegation (PS-D10, reversible), so a ruling confirms or undoes what every macos-user launch now does.
19. [Rule what the environment manager promises at each notch](../design/environment-manager-user-stories.md) — Q1b
    decides [`yolo check --at`](../design/yolo-as-environment-manager.md) and Q7 [the Linux guest notch](environment-manager-plan.md).
20. [Rule the keychain from a jail](../design/keychain-from-a-jail.md), which settles [Copilot's machine-wide
    login](../research/copilot-token-storage.md). Review its broader native-credential-store direction with the
    sandbox account's missing-keychain reports: storing suitable tokens responsibly must not wait on Copilot alone,
    and the account's unlock, workspace scope and migration choices need to be considered together.
21. [Rule what the keeper holds at `yolo host`](../design/jail-lifetime-last-session-wins.md), with [the sidecars and
    doorbell](../design/agent-event-watchers.md) — each design's host half waits on the other.
22. [The boot snapshot and diagnostic dial](../design/diagnostics-past-the-boundary.md) — a refused boot keeps no record
    of the jail's state, and the readiness act built 2026-10-06 adds one more refusal.
23. [Rule the add-only model lists](../design/model-lists-and-pickers.md) and [web search on Bedrock](../design/bedrock-web-search.md)
    — what most agents' menus show turns on the first, and no agent gets search from Bedrock on runtime.
24. [Decide whether pack-declared traps fold into the agent directory map](../design/agent-directory-map.md) — the map
    would supersede [the traps design](../design/pack-declared-file-diagnostics.md), so rule it before building either.
25. [Measure the in-jail nix root watcher end to end](../design/in-jail-nix-roots.md#8-what-is-built) — built
    2026-10-08; it needs one fresh jail on a host running this yolo: a `nix build` in the workspace, then
    `yolo nix-roots list` and the host's `nix-store --query --roots`.
26. [Review H4's root-owned capture store on macos-user](install-capture.md#build-order), [rule whether integration
    sharding unparks](integration-parallelism.md) with [the test suite's other levers](test-suite-speed.md), and [name
    the real forked program](../design/forked-programs-as-packs.md), the last input its step 7 needs.
27. [Rule how a jail reaches a worktree's `.git` outside it](../research/herdr-integration.md), with the other herdr calls
    — measured: a read-only bind refuses every commit, a read-write one lets a jail prune the worktree.
28. Calls left at one decision each, each with its facts and a leaning: [the `:ro` degradation rows](../design/composed-file-permissions.md),
    [whether messages name the guide's URL](../design/docs-website.md#OQ-DW3), [the macOS nix build sandbox](../design/macos-user-build-step-threat-model.md),
    [array-append pinning](BACKLOG.md#E5), [the pack system's other calls](../reference/pack-system.md), [relocating
    `.yolo` by a link](../reference/jail-home.md#OQ-JH1), [the shared tool store's relocation contract](../design/shared-tool-store-relocation.md)
    with [its build handoff](shared-tool-store-relocation.md), and, once item 11 records its cases, [context sources inside a
    home](../design/context-mounts.md).
    Beside the storage rulings, [choose bulk scratch placement](../design/storage-tiers.md), with its
    [implementation sketch](../design/storage-tiers-plan.md) — settle the capacity-storage contract before
    widening storage abstractions; this does not move code or the existing durable directory.
29. [Rule whether yolo may hold an editor preference](../design/baked-editor-preference.md) — the maintainer asked for
    it, and its first ruling decides an image change every jail pays. Beside it, also asked for by the
    maintainer: [how a SandVault or Safehouse user tries yolo on their existing setup](../design/sandvault-safehouse-compat.md#OQ-NB1),
    then [which backend a SandVault setup gets](../design/sandvault-safehouse-compat.md#OQ-NB2) and
    [the design's three scope calls](../design/sandvault-safehouse-compat.md) — the first decides the command
    surface the import and trial build on.
30. [Build the rest of the broker's step 2](../design/boundary-broker.md#11-recommendation-and-the-first-build-slice) —
    first among the builds, as the maintainer put the broker on the plate for the week of 2026-09-28. Beside it,
    [workspace widening](../design/workspace-widening.md) graduates once [OQ-WW1](../design/workspace-widening.md#OQ-WW1) is ruled.
31. Speed builds the maintainer asked for on 2026-10-04: [Apple Container's stock-image skip, then the skip for a launch
    declaring `packages:`](../reference/image-staging-vs-baking.md), as a nix build that finds nothing to do is 3.2 to
    3.5 s of a 6.9 s fresh launch; and [Podman Machines pinned to applehv](../research/macos-vm-runtime-comparison.md#32-on-a-shared-mac-folder),
    as Podman's installer gives libkrun, the slowest shared folder measured. Then
    [make OrbStack a working Podman host](../research/orbstack-as-a-podman-host.md), with [its native verification
    plan](../research/orbstack-as-a-podman-host-plan.md), as its measured shared-folder speed and memory return offer
    another Mac option without restoring Docker first.
32. Builds no ruling holds: [the provisioner override](../design/provisioner-sets.md#9-what-i-would-build-in-order),
    whose grain was ruled 2026-10-05; [the readiness act on macos-user](../design/jail-notch-readiness.md#JR-D2) — **built 2026-10-06**: host-side admission starts the existing confined stage for an absent/unknown selected program and its generated bootstrap runs the shared readiness act; fixture tests are in `integration/macosuserprogramreadiness_test.go`, with native macOS execution pending; [a provider set's overlay modifier](../design/active-provider-sets.md#13-what-was-built-2026-09-29), which reads only the
    primary; [the empty-list line in `yolo check`](../design/model-lists-and-pickers.md#72-composition-rules); [TP10's
    `boot.log` half](../design/trust-paths.md#outstanding-work); [macos-user's item 2 twin](handoff-macos-user-open-threads.md);
    [the color pass's Group B](cli-visual-polish.md); [the unit suite's clock waits](test-suite-speed.md#unit-tests-one-package-sets-the-wall-time-and-five-of-its-tests-are-waiting-on-clocks);
    [the Bedrock user-guide recipes](../design/bedrock-plumbing.md#12-what-i-would-build-in-order); [the disk levers'
    re-measure](../design/disk-levers-and-backfill.md); and the pi guide's line on [a package the refresh missed](../design/pi-extension-lifecycle.md).
33. Verify [the exact-commit release gate](../design/pre-tag-release-gate.md) before the next publication request,
    because offline fixtures do not prove live Actions or publisher permissions. What the 2026-10-05 and 2026-10-06
    builds still owe: read the first scheduled macos-user nightly with [the vendor
    installs](../reference/agent-install-in-ci.md), and the linux/arm64 copilot cell for [GitHub's own installer](native-installer-migration.md);
    rule the silent drops [the setup census](../design/backend-parity.md#42-what-was-built-2026-10-05-and-what-it-found)
    found; graduate into system docs [config ownership](../design/config-ownership-and-promotion.md), [pi's package
    folder](../design/pack-pi-resources.md), [the extension model defaults](../research/extension-model-defaults.md), and
    [`packages` attribute paths](../design/package-nested-attribute-paths.md) once a Mac has run a dotted entry.
34. Rulings nothing shipped waits on. Gating one later step: [the host-file permission asymmetry](BACKLOG.md#E2) with
    [its host-side twin](pack-host-management-plan.md), [the skills fan-out](BACKLOG.md#OQ-S4), [sharing a claimed
    generic name](../design/credential-sources-separation.md#OQ-ES6), [the pack-file conventions](../design/pack-conventions.md),
    [loophole env and guest fields](../design/loophole-packaging.md), [workspace MCP files](../design/workspace-mcp-sources.md),
    [Seatbelt deny-default](../research/agent-safehouse.md), [host apply's posture](../design/host-render-target.md),
    [the I/O priority default](../design/io-priority.md). Only closing a document: [an unmatched-reference hatch](../design/reference-mismatch-diagnostics.md),
    [path mirroring](../design/workspace-path-mirroring.md), [pack telemetry](agent-config-packs.md), [AWS's key
    pair](../design/agent-auth-modes.md#OQ-9), [stateful-surface comments](BACKLOG.md#OQ-E4), [the upstream mise
    issue](../design/jail-state-separation-design.md), and [macos-user's stale home-root file and two workspaces at
    once](../reference/macos-user-home-tiers.md).
35. The four calls the 2026-09-26 open-source audit left to the maintainer: whether one public research doc stays
    public, the scratch-directory `.gitignore` entries, a `last-release` recipe from the template, and when to retire
    the Python-version migration; the audit's report is outside this repository, so this is their only record.

## External waits

36. One session at a Mac, with `sudo`'s password and the runner stopped, completes [the benchmark](../research/macos-backend-performance.md#8-results)
    — [auto-capture](../design/program-delivery.md#decision-ledger), macos-user's `go test`, Podman Machine on applehv
    and [NFS from macOS's `nfsd`](../research/macos-vm-runtime-comparison.md#6-is-there-an-open-stack-with-faster-shared-folders)
    — first, as [the comparison's refresh](../reference/macos-no-vm-direction.md#when-to-refresh-this) waits on it. It
    also runs `yolo host -- claude` and `yolo host -- agy` for [the host floor's installer agents](../design/host-tool-provisioning.md#HP-D2),
    and a macos-user launch with a dotted `packages` entry, which no CI job runs.
37. The next `apple-container.yml` dispatch reruns [the two-jail check](../research/macos-backend-performance.md#7-found-on-the-way-two-apple-container-jails-may-mount-one-ext4-disk),
    which must pass now that each workspace has its own `/mise` disk, and re-asks [the keeper measures](../design/jail-lifetime-last-session-wins.md#7-what-i-would-build-in-order);
    someone at the runner Mac upgrades `container` to 1.5.0 (its kernel prompt is interactive) before the VM-local probe.
38. The next `macos-user.yml` run carries the Mac checks built since 2026-10-04: [I/O priority](../design/io-priority.md#9-what-i-would-build-in-order),
    the macos-user keeper, [the capture fixtures](install-capture.md), the host floor's fixture capture, the Mac floor's
    fork build and [the guest notch](environment-manager-plan.md); each doc's `next` names its test.
39. A human with a subscription login runs [the credential-view runbook](runbooks/claude-credential-view-measures.md)
    for [Claude's login without interception](../design/claude-login-without-interception.md), on a Mac and for a day
    on a rootless host, which releases [`yolo host -- claude` joining the shared login](../reference/claude-oauth-interposition.md),
    and records one browser login and one shared expiry for [the OpenAI service](../design/openai-auth-broker-plan.md).
40. A real rootless Linux host, which a nested jail is not, clears [a real reboot](../design/podman-reboot-readiness.md#testing-and-the-real-host-check),
    [a low-space collection with a jail up](storage-lifecycle.md), [the keeper's scope move and logout](../design/jail-lifetime-last-session-wins.md#8-what-done-looks-like),
    and HD-R1's first launch once item 2 lands.
41. One live agent session per check, which no test may start: [the footer](../design/agent-footer.md#21-as-built),
    [Claude's LSP plugin](../reference/mcp-configuration.md#lsp-claudes-route-is-a-generated-plugin), [the via route](../design/wire-bridge-gateway.md#41-how-it-is-built),
    [menus under a model list](../design/model-lists-and-pickers.md#13-build-order-and-what-done-looks-like), [a switch in
    a provider set](../design/active-provider-sets.md#9-what-done-looks-like), [Copilot's config migration](../design/agent-directory-map.md#74-copilot),
    [a Kilo session](../design/gateway-provider-packs.md), [the restart prompt](../design/attach-skew-and-contract-guardrails.md),
    [pi loading a pack's folder](../design/pack-pi-resources.md), [host Chrome from `yolo host -- claude` and under
    macos-user](../design/mcp-presets-removal.md#13-what-i-would-build-in-order), and [codex, opencode and pi on
    Bedrock](../design/bedrock-plumbing.md#8-behaviour-this-design-fixes) under SSO. Check [Pi launch selection](../design/pi-launch-selection-flags.md)
    and [simultaneous native authentication](../design/simultaneous-auth-and-pack-isolation.md) in a live session;
    launch arguments and source inspection do not establish authentication isolation.
42. A human with the cloud account clears [a Bedrock API key or static key pair](../design/bedrock-plumbing.md#64-the-credential-three-are-supported),
    [an SSO lapse mid-turn](../design/sso-backed-bedrock.md#11-evidence-and-how-to-re-check-it), [settings-only Bedrock mode](../reference/providers.md#two-channels-split-by-payload-type),
    [an AgentCore gateway](../design/bedrock-web-search.md) and [a Claude usage-limit response](../design/wire-bridge-gateway.md#5-part-4--the-subscription-arm-and-opt-in-failover-ruled).
43. The maintainer's own commits and launches clear [the `matt` pack's `briefing/`](../reference/pack-system.md#briefing),
    [its posture list at the host](../design/notch-scoped-config-contributions.md#5-fastest-path-to-the-motivating-case),
    [a dropped pack's host output](../reference/pack-system.md#retiring-a-dropped-packs-host-output), [the fzf
    pack](handoff-fzf-pack-adoption.md), his pack's move to pi's package folder, [the listen-port fix where reported](../reference/wire-bridge.md#what-can-hold-the-listen-port-before-the-bridge-does)
    and [the runner Mac's published-ports change](runbooks/mac-actions-runner.md).
44. A live `llama-server` and one manual turn per agent clear [the `llamacpp` provider](../../packs/llamacpp/README.md),
    whose spellings have never met a server; copilot is the one to try first.
45. The mise use record's first 30-day window gives [the disk footprint's re-measure](../design/minimal-disk-footprint.md)
    its first offer to check, on the host.
46. Nix dropping Intel Macs, when nixpkgs 26.05's security fixes stop on 2026-12-31, brings back [what replaces the
    Intel macOS runner](../research/macos-support-matrix.md#OQ-MX1), deferred until then.
47. The maintainer files the upstream reports drafted with the VM-local design, on [VZ's virtio-fs cache policy](../research/apple-container-file-cost.md#21-per-file-cost-not-bandwidth)
    and [VZ keeping freed guest memory](../research/macos-vm-memory-reclaim.md#5-adding-it-to-apple-container-ourselves)
    — last, since only Apple can ship either fix.

## Boundaries

- **Not committed to:** [`further-roadmap-ideas.md`](further-roadmap-ideas.md), a shelf of unverified candidates.
  **Iced**, with what would thaw each: [`yolo cache relocate`](cache-relocation.md) (someone wanting it), [trust-gated
  grants in a local config](../design/workspace-config-trust.md) (a read-write mount that cannot move into the
  workspace), [in-jail nix on the container Macs](setup-support-gaps.md#2-ranked-gap-backlog) (a user for whom
  `packages:`, mise and macos-user all fall short) and [a central host watcher](../research/central-yolo-watcher.md#7-what-would-have-to-be-true-before-building-one)
  (a duty that must hold a listener across launches).
- **Ids Vantage cannot index**, reached here by name: [Q1 to Q9](../design/environment-manager-user-stories.md#Q1),
  [9.2 and 9.6](../design/host-render-target.md#9.2), [CFP-1 to CFP-3](../design/composed-file-permissions.md#CFP-1),
  [Q2 and Q3](../design/macos-user-build-step-threat-model.md#Q2), [SS-6](../design/jail-state-separation-design.md#ss-6),
  [E1, E2, E5 and CO](BACKLOG.md#E1), [the host-files mode](pack-host-management-plan.md#open-questions), and two
  outside this queue: [SH-1](macos-revival-and-distribution-plan.md#oq-sh-1--is-macos-user-self-hosting-worth-pursuing-at-all-maintainer),
  which blocks nothing, and [JD-4](../design/jail-daemon-on-macos-user-plan.md#decision-ledger), a maintainer follow-up.
- **With no id at all:** [Go comments citing sections by number](README.md#the-dangling-n-citations-in-go-comments),
  [check 2's two marker conventions](README.md#check-2-has-two-known-errors-and-a-convention-question-under-each),
  [the storage lifecycle's `min-free` values](storage-lifecycle.md#open-questions), the unruled half of
  [OQ-ST2](../design/synced-skill-trees.md#OQ-ST2) on user-reserved skill children, and whether a bare `-p` whose
  packs install no CLI should say it selects nothing (`checkProfileTargets`, `internal/cli/run/packs.go`).
