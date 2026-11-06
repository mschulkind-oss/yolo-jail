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

**Reconciled:** 2026-11-06.

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

Ahead of every numbered item: [legacy configs keep working](../design/legacy-config-compat.md) —
the maintainer, 2026-11-03: *"why do I need to change my configs? that's not the point of this.
the configs keep working."* Every config that launched in the previous release launches unchanged
and means the same, with a corpus test whose allowlist must empty as the enforcer. The rule is
scoped to the TOML switch ([OQ-LC1](../design/legacy-config-compat.md#OQ-LC1)), the broker switch
carries its read grant ([OQ-LC2](../design/legacy-config-compat.md#OQ-LC2)), and the 2026-10-29
security refusals are outside it ([OQ-LC3](../design/legacy-config-compat.md#OQ-LC3)); no ruling is
owed. That document owns its remaining build.

1. **`yolo host --` calls its cwd a bind-mounted workspace, so it refuses `aws-auth` in the home**
   — measured 2026-10-19, and no ruling is needed. As
   [the lab report](../lab-reports/2026-10-19-launch-diagnostics-on-a-mac.md) traces it, the host
   notch passes its cwd (`os.Getwd()` in `internal/cli/host.go`) to the loophole placement rule as
   a workspace, a branch worded for a jail (*"the workspace this launch bind-mounts :rw"*), though
   the host notch mounts nothing. Run from `$HOME`, that tree contains `~/.local/share/yolo-jail`,
   so the rule refuses the loophole serving Bedrock credentials. The rule's empty-workspace path
   already exists for a caller with no workspace in hand; this notch does not use it. The report
   also gives the workaround.

2. [Finish the readable-config redesign](../design/readable-config.md) — the maintainer,
   2026-10-24: *"this is important"*. The approved [RC-D68](../design/readable-config.md#RC-D68)
   and [RC-D69](../design/readable-config.md#RC-D69) (provider names and aliases, fatal
   collisions, the manifest's `[providers.<id>]` shape) and the workspace half of
   [RC-D60](../design/readable-config.md#RC-D60) are not built; then the redesign graduates to a
   system doc and [its plan](../design/readable-config-plan.md) retires. The TOML release ships
   with it: [the user config](../design/config-user.md), [a project's file](../design/config-project.md),
   [editing commands and format](../design/config-commands.md), [migration](../design/config-migration.md)
   and [pack manifests](../design/pack-manifests.md).

3. [Finish ongoing launch preparation and explicit update-and-wait](../design/launch-update-preparation.md)
   — reuse the admitted-build pipeline before optimizing isolated waits; its
   [combined implementation map](../design/launch-update-preparation-plan.md) connects scheduling,
   freshness, HOST, Apple checks and supported ordinary npm capture. Independent review and final
   combined runtime/platform acceptance remain, and
   [the design's timing acceptance](../design/launch-update-preparation.md#visibility-and-acceptance)
   bounds current timing claims.

4. [Give each session in one jail its own profile](../design/per-session-profiles.md) — the
   maintainer's 2026-10-18 report that one attach re-renders the files a running pi's subagents
   read. The jail mechanism and the 2026-10-19 host rulings are built; macos-user's per-launch id
   and the other agents' declarations remain, in [the plan](../design/per-session-profiles-plan.md).

5. [Apply config changes to a running jail](../design/live-config-apply.md) — v1 is landed, and its
   Mac-only `-short` failures are repaired and verified natively; rootless CI evidence is still owed
   ([item 43](#external-waits)). Its later slices are each built on their own after v1:
   [the next-session moves](../design/live-config-apply.md#LA-S4a),
   [Apple Container's briefing re-copy](../design/live-config-apply.md#LA-S4b),
   [resources through `podman update`](../design/live-config-apply.md#LA-S4c) and
   [live host-port forwards](../design/live-config-apply.md#LA-S4d).

6. [Complete the computed-briefings plan](../design/computed-briefings-plan.md), from the
   [computed-briefings design](../design/computed-briefings.md) — the maintainer, 2026-10-24: a
   pack's briefing should tell each workspace's agent exactly how to finish a change according to
   the grants that workspace was approved. It waits on
   [OQ-CBP1](../design/computed-briefings-plan.md#OQ-CBP1) and
   [OQ-CBP2](../design/computed-briefings-plan.md#OQ-CBP2), not on the readable-config redesign.

7. [Enable profile-served daemons on attach without ending sibling sessions](../design/attach-daemon-activation.md)
   — the reported late Bedrock selection reaches a restart prompt; it waits on
   [OQ-AD1](../design/attach-daemon-activation.md#OQ-AD1).

8. **Apple Container builds no patched fork or extension while yolo's own image builder runs** —
   measured 2026-10-19. The listing parse (cause 1) is fixed and verified on a Mac; the gate still
   counts `yolo-ac-builder` as a running jail (cause 2), which waits on
   [OQ-XB2](../design/pi-extension-store-builds.md#OQ-XB2).

9. **A `Ctrl-C` during provisioning is reported as an unwritable log** — measured 2026-10-19; the
   repair waits on [OQ-PLW1](../design/provisioning-interrupt.md#OQ-PLW1).

10. **A podman launch on a Mac cannot use a workspace reached through a symlink** — measured
    2026-10-19; the repair waits on [OQ-SWB1](../design/symlinked-workspace-binds.md#OQ-SWB1), and
    its verification needs podman on a Mac. One launch also left `yolo-gitconfig` unwritten, which
    is unexplained ([the lab report](../lab-reports/2026-10-19-launch-diagnostics-on-a-mac.md)).

Pi's profile-set/provider-seal extension is built, including its required child guard, and
[MM-D42](../design/model-lists-and-pickers.md#MM-D42)'s profile-derived child defaults are a
mechanism yolo ships no declaration of: a pack of the user's own may declare `launch_computed` for a
pi-subagents that reads a flag. Do not invent a yolo settings overlay or rewrite shared settings.
Other accepted limits remain in
[its design's §3.4](../design/simultaneous-auth-and-pack-isolation.md#34-what-the-extension-cannot-close),
and a pi-subagents registry several extensions can share stays in item 27.

Native readiness and credential-store work retain their places below: assess
[the privilege prerequisites](../research/sandvault-macos-privileges.md) without broad root
cleanup, and rule [keychain scope/unlock/migration](../design/keychain-from-a-jail.md) without
sharing host credentials. Keep [storage relocation](shared-tool-store-relocation.md) and
[OrbStack](../research/orbstack-as-a-podman-host.md) separate from these urgent launch repairs.

## The queue

1. [Finish the patched-fork mode's verification](../reference/patched-forks.md#14-unbuilt-and-unmeasured), with
   [its pi-extension companion](../reference/pi-extensions.md#16-unbuilt-and-unmeasured) — keep the remaining
   verification ahead of dependent work. Beside it: [whether a Mac fork build installs its base's
   `node_floor` first](../design/forked-programs-as-packs.md#OQ-FP11), and the maintainer's test with the migration kit.
2. Rulings holding other builds: [whether a project's mise pin of `pnpm` stays hidden](../design/program-delivery.md#oq-pd19),
   with [autoprune's `$GOBIN` class](../design/program-delivery.md#oq-pd20) and [a pack's agent-CLI pin](../design/program-delivery.md#oq-pd21);
   [whether a pack service's in-jail daemon is disclosed](../design/trust-paths.md#OQ-TP11), as no launch line names
   the wire bridge; [whether copilot and oh-omp alone get `-p bedrock`](../design/bedrock-plumbing.md#OQ-BR24).
3. Rulings that hold a defect's fix: [the Kilo special-casing](../design/gateway-provider-packs.md); [`--no-daemon` for in-jail `codex`](../research/codex-background-service.md#OQ-CDX3),
   since `codex agents` starts the daemon yolo turns off; [whose OpenAI login host pi uses](../design/pi-host-openai-auth.md),
   since its first refresh replaces yours, with its catalog-refresh question; and [a leftover `mcp-adapter.json` at the
   host](../reference/mcp-configuration.md#OQ-MC2), which host apply never deletes and pi keeps loading, with
   [OQ-MC1](../reference/mcp-configuration.md#OQ-MC1). For pack files: [warning on a folder another pack keeps state in](../design/pack-files-for-agent-state.md#OQ-FS2) and [whether a Mac
   bootstrap stops at a failed home layout](../design/pack-files-for-agent-state.md#OQ-FS4); [skipping files for an unselected
   agent](../design/pack-files-for-agent-state.md#FS-D6) is ruled and buildable.
4. [Rule whether a successful `yolo host apply --assert` names a next command](../reference/happy-path-principle.md#the-rules)
   — rule 2 calls such a success a dead end, `--assert` ends at its counts, and the line waits on a ruling against [the
   verdict block](../reference/report-tiers.md#the-verdict-block)'s "Only the dry run has a footer"; this entry is the
   work's only home. Rule [the dry run's exit](../reference/report-tiers.md#OQ-RO8) with it: the code exits 1 where
   [OQ-RO5](../reference/report-tiers.md#why-its-this-way) says 0, and the launch gate reads that exit.
5. [Complete the remaining build-update slices](../design/pi-extension-store-builds.md), through
   [ongoing preparation](../design/launch-update-preparation.md) — host delivery/reporting, Apple Container
   check-only, fork/capture timing and native/performance acceptance retain their scope rather than disappearing
   into a tree-only fix. [Matched development bundles](../design/matched-development-bundles.md) and
   [their plan](../design/matched-development-bundles-plan.md) add the paired, build-only launcher and a
   checkout-resident wrapper without changing normal installation.
6. [Rule how the Claude footer moves into a Claude Code plugin](../research/claude-code-extensions-footer.md), with [how
   yolo manages Claude Code mods](../research/claude-code-mods-management.md) — the plugin ends a process per refresh,
   and the mods rulings rest on that route.
7. Rule [the VM-local disks design](../design/vm-local-volumes.md): [default or opt-in](../design/vm-local-volumes.md#OQ-VL1),
   [`~/.cache`](../design/vm-local-volumes.md#OQ-VL2), [database folders](../design/vm-local-volumes.md#OQ-VL3) and
   [how a new disk fills](../design/vm-local-volumes.md#OQ-VL4),
   with [the probe that checks its premise](../research/apple-container-file-cost.md#5-a-planned-probe-on-the-mac-runner)
   and [VZ's memory request](../research/macos-vm-runtime-comparison.md#43-draft-feedback-assistant-request) to file — the
   largest Mac speedup measured waits on it.
   Beside it, the maintainer's 2026-10-08 request: [tools from a private Homebrew tap in every jail](../design/private-tap-programs.md),
   whose build waits on [a pack or a config key](../design/private-tap-programs.md#OQ-PT1),
   [brew's copy at the host](../design/private-tap-programs.md#OQ-PT2), [the version policy](../design/private-tap-programs.md#OQ-PT3),
   [never `brew tap`](../design/private-tap-programs.md#OQ-PT4) and [a launch without the token](../design/private-tap-programs.md#OQ-PT5).
8. Approve [the drafted rewordings](../research/macos-backend-performance.md#9-corrections-the-results-feed) of
   [the direction's ruled sentences](../reference/macos-no-vm-direction.md#what-the-numbers-bear-on).
9. [Time what a launch's spans leave out](../reference/perf-logging.md) — built; the Apple Container delivery test's
   Mac half is compiled, not yet run, and one Mac run shows the build skip's saving.
10. Record the Mac runs of 2026-10-02 and 2026-10-03 where each doc's `next` names them — agent work now, ahead of the
    rulings below because the context-mount cases free one: [the AWS doorway](../design/host-notch-services.md), [its
    Bedrock design](../reference/agent-credentials.md#sso-backed-bedrock-credentials-aws-auth), [A2's twin](../reference/macos-user-nix-and-features.md#ordering-and-what-aborts), [the
    render-mark twin](../reference/config-target-resolution.md#where-this-does-not-reach),
    [the privilege transition's twin](runbooks/macos-user-manual-checks.md#1-the-privilege-transition),
    [DP-B6](../design/declaration-parity.md), [the footer](../reference/agent-footer.md#what-has-and-has-not-been-observed), [the
    build sandbox](../design/macos-user-build-step-threat-model.md), [the context mounts](../reference/context-mounts.md),
    [Cachix](../reference/image-staging-vs-baking.md#the-binary-cache), [the in-VM copier](../reference/image-staging-vs-baking.md#the-first-load), [the login
    seed](../reference/jail-home.md) and [the storage classes](../reference/jail-home.md).

    Build [the bounded privilege lifecycle](../design/macos-user-privilege-lifecycle.md) from
    [its plan](../design/macos-user-privilege-lifecycle-plan.md), with the native readiness work:
    an agent can start and stop macos-user jails unattended only once no launch or exit stops at a
    password prompt. The ruled grant runs commands as the sandbox account alone, never as root, and never account-wide.
11. [Rule the macos-user workspace root](../design/configurable-workspace-root.md), [its whitelist](../design/configurable-workspace-root.md#OQ-CW2)
    first — a `/var/root` home passes until it replaces the blacklist, and tightening later withdraws what users rely on.
12. [Decide whether a workspace config may feed host dotenvs and mounts into a jail](../research/agent-safehouse.md#OQ-AS3)
    — a committed config still can, and notch convergence's `env_sources` item waits on it.
13. [Rule which packs a workspace config may select](../design/workspace-packs.md), [OQ-WP1](../design/workspace-packs.md#OQ-WP1)
    to [OQ-WP5](../design/workspace-packs.md#OQ-WP5) — `mcp_presets` is retired, so until this lands a repository cannot
    give its collaborators the browser, which the presets did.
14. [Rule the slot split's migration window](../design/slots-and-contributions.md#OQ-D6), then [the manifest
    language](../design/manifest-language.md) with [the slots' other calls](../design/slots-and-contributions.md) —
    `exposes` and [the pi extension-tree rework](../design/pi-pack-extensions.md) wait on the first.
15. Provider rulings later work builds on: [whether the natively-implements rule reaches a list a pack declares](../design/pi-codex-provider-shadowing.md#OQ-4),
    which holds what [its broad reading](../design/pi-codex-provider-shadowing.md#65-what-the-build-decided) left in place, and [what `-p`
    names](../design/providers-and-profiles-redesign.md), which the provider reference's rewrite waits on.
16. [Notch convergence's held items](notch-convergence.md) — `env_sources` waits on item 12, the one `rmw` rule on [a
    table not declared in full](../reference/config-ownership.md#OQ-CO15) (rule [OQ-CO16](../reference/config-ownership.md#OQ-CO16)
    with it), and `yolo host -- claude` joining the shared login on the credential view's measures, below.
17. [The provisioner's calls](../design/provisioner-sets.md) are all ruled; what remains is building
    the one `program` kind that any selected pack may fill, and the separate account's Go trust.
18. [Rule what the environment manager promises at each notch](../design/environment-manager-user-stories.md) — Q1b
    decides [`yolo check --at`](../reference/what-yolo-is.md#check-at-a-notch) and Q7 [the Linux guest notch](environment-manager-plan.md).
19. [Rule the keychain from a jail](../design/keychain-from-a-jail.md), which settles [Copilot's machine-wide
    login](../research/copilot-token-storage.md). Consider the sandbox account's unlock, workspace scope and migration
    choices together, and do not hold storing suitable tokens responsibly on Copilot alone.
20. [Rule the starting tab's Ctrl-C in a shared jail](../design/jail-lifetime-last-session-wins.md#OQ-JL10) — the
    keeper at `yolo host` is ruled; its build waits on [the sidecars and doorbell](../design/agent-event-watchers.md).
21. [The boot snapshot and diagnostic dial](../design/diagnostics-past-the-boundary.md) — a refused boot keeps no record
    of the jail's state; it waits on [OQ-DB2](../design/diagnostics-past-the-boundary.md#oq-db2).
    Beside it, [what just happened in this jail](../design/jail-history.md): its first slice is built; the rest waits on [OQ-JH1](../design/jail-history.md#OQ-JH1), [OQ-JH2](../design/jail-history.md#OQ-JH2) and [OQ-JH4](../design/jail-history.md#OQ-JH4).
22. [Rule the add-only model lists](../design/model-lists-and-pickers.md) and [web search on Bedrock](../design/bedrock-web-search.md)
    — what most agents' menus show turns on the first, and no agent gets search from Bedrock on runtime.
    Beside them, [make the narrowed list the only models a Bedrock credential can call](../design/model-allowlist.md),
    which waits on [OQ-MA1](../design/model-allowlist.md#OQ-MA1) to [OQ-MA3](../design/model-allowlist.md#OQ-MA3)
    — a pi subagent called a model its menu no longer offered, and only the credential refuses every path.
23. [Decide whether pack-declared traps fold into the agent directory map](../design/agent-directory-map.md) — the map
    would supersede [the traps design](../design/pack-declared-file-diagnostics.md), so rule it before building either.
24. [Measure the in-jail nix root watcher end to end](../reference/in-jail-nix-roots.md) — host root resolution remains
    unverified, and the shared-store root query pruned stale entries, so finish with a safe, isolated host-side observation.
25. [Review H4's staged capture store on macos-user](../reference/install-capture.md#build-order), [rule whether integration
    sharding unparks](integration-parallelism.md) with [the test suite's other levers](test-suite-speed.md), and [name
    the real forked program](../design/forked-programs-as-packs.md), the last input its step 7 needs.
    Beside suite speed, [make completion change-aware and read-only](../reference/change-aware-completion.md)
    without changing source landing or CI coverage.
26. [Rule how a jail reaches a worktree's `.git` outside it](../research/herdr-integration.md), with the other herdr calls
    — measured: a read-only bind refuses every commit, a read-write one lets a jail prune the worktree.
27. Calls left at one decision each, each with its facts and a leaning: [the `:ro` degradation rows](../design/composed-file-permissions.md),
    [whether messages name the guide's URL](../design/docs-website.md#OQ-DW3), [the macOS nix build sandbox](../design/macos-user-build-step-threat-model.md),
    [array-append pinning](BACKLOG.md#E5), [the pack system's other calls](../reference/pack-system.md), [relocating
    `.yolo` by a link](../reference/jail-home.md#OQ-JH1), [whether to ask pi-subagents for a registry several
    extensions can share](../design/simultaneous-auth-and-pack-isolation.md#34-what-the-extension-cannot-close), [the shared tool store's relocation contract](../design/shared-tool-store-relocation.md)
    with [its build handoff](shared-tool-store-relocation.md).
    Beside the storage rulings, [rule how one workspace splits across storage tiers](../design/storage-tiers.md), with its
    [implementation sketch](../design/storage-tiers-plan.md); its disk-presence and backend calls are the same as the
    tool store's, so rule them together. Beside them, [isolate ordinary jail caches](../design/cache-isolation.md) —
    private warm state needs safe reclamation alongside it, not another unbounded storage surface.
28. [Rule whether yolo may hold an editor preference](../design/baked-editor-preference.md) — the maintainer asked for
    it, and its first ruling decides an image change every jail pays. Beside it, also asked for by the
    maintainer: [how a SandVault or Safehouse user tries yolo on their existing setup](../design/sandvault-safehouse-compat.md#OQ-NB1),
    then [which backend a SandVault setup gets](../design/sandvault-safehouse-compat.md#OQ-NB2) and
    its two scope calls, [credentials](../design/sandvault-safehouse-compat.md#OQ-NB3) and [the trial's `.yolo/`](../design/sandvault-safehouse-compat.md#OQ-NB5) — the first decides the command
    surface the import and trial build on.
29. [Build agent access approvals in the maintainer's five steps](../design/boundary-broker.md#11-the-build-in-the-maintainers-five-steps) —
    he set the order on 2026-10-23: brokered sources with read and write sets, actionable toasts, a host CLI to
    view and approve, a web UI out of tree on that CLI, then a talk about merging it. Alongside that foundation,
    [coordinate the notification gallery, optional queue and broader service-mediation proposals](../design/approval-experience/README.md).
    The git source and the paste-to-host stopgap are built, and the user guide covers them
    ([git](../../userguide/guides/git.md), [running a host command](../../userguide/guides/host-commands.md),
    [approvals](../../userguide/guides/approvals.md)). What remains of the maintainer's 2026-10-23 ask (*"proper
    evergreen documents and a user guide section that's up to date"*) is graduation: [the git broker's
    reference](../reference/git-broker.md) is written, and [its design](../design/git-broker.md) graduates once
    [GB-D28](../design/git-broker.md#GB-D28)'s removal of `yolo approve --add` and `--remember` is built.
    [Which writes to a PR or issue run unasked](../design/github-write-scopes.md) is ruled, including the same removal
    for GitHub; complete [its plan](../design/github-write-scopes-plan.md) against the tree. [Workspace widening](../design/workspace-widening.md)
    graduates once [OQ-WW1](../design/workspace-widening.md#OQ-WW1) is ruled. Also asked for on 2026-10-09:
    [image paste in every agent](../design/image-paste.md), where [how far Ctrl+V is consumed](../design/image-paste.md#OQ-PA2)
    shapes the key handling, with [closing macos-user's possibly open pasteboard](../design/image-paste.md#OQ-PA1) and
    [the default](../design/image-paste.md#OQ-PA3); its first slice, the host reader and `yolo paste`, waits on none of them.
30. Finish [image build reuse](../reference/image-staging-vs-baking.md) with native Apple Container stock-tag dispatch and warm-launch validation. Then [Podman Machines pinned to applehv](../research/macos-vm-runtime-comparison.md#32-on-a-shared-mac-folder),
    as Podman's installer gives libkrun, the slowest shared folder measured. Then
    [make OrbStack a working Podman host](../research/orbstack-as-a-podman-host.md), with [its native verification
    plan](../research/orbstack-as-a-podman-host-plan.md), as its measured shared-folder speed and memory return offer
    another Mac option without restoring Docker first.
31. Builds and sign-offs no ruling holds: native macOS execution of [the readiness act on macos-user](../reference/program-readiness.md#JR-D2);
    [a provider set's overlay modifier](../design/active-provider-sets.md#13-what-was-built-2026-09-29), which reads only the
    primary; [macos-user's item 2 twin](runbooks/macos-user-manual-checks.md#2-seatbelt-is-actually-applied);
    [the color pass's Group B](cli-visual-polish.md), whose `yolo run` progress golden waits on the maintainer's sign-off;
    [the unit suite's clock waits](test-suite-speed.md#retaken-2026-10-01-and-which-targets-hold); [the disk levers'
    re-measure](../reference/disk-footprint.md#owed-measurements); and the pi guide's line on [a package the refresh missed](../reference/pi-extensions.md).
32. Verify [the exact-commit release gate](../reference/release-process.md) before the next publication request,
    because offline fixtures do not prove live Actions or publisher permissions. Read the first scheduled macos-user
    nightly with [the vendor installs](../reference/agent-install-in-ci.md), and the linux/arm64 copilot cell for
    [GitHub's own installer](../design/program-delivery.md#OQ-NI1); rule the silent drops
    [the setup census](../design/backend-parity.md#42-what-was-built-2026-10-05-and-what-it-found) found; run a dotted
    `packages` entry on a `macos-user` launch, the one
    [UNMEASURED half of the attribute-path resolver](../reference/image-staging-vs-baking.md#what-a-packages-entry-names).
33. Rulings nothing shipped waits on. Gating one later step: [the host-file permission asymmetry](BACKLOG.md#E2) with
    [its host-side twin](pack-host-management-plan.md), [the skills fan-out](BACKLOG.md#OQ-S4), [sharing a claimed
    generic name](../design/credential-sources-separation.md#OQ-ES6), [the pack-file conventions](../design/pack-conventions.md),
    [loophole env and guest fields](../design/loophole-packaging.md), [workspace MCP files](../design/workspace-mcp-sources.md),
    [Seatbelt deny-default](../research/agent-safehouse.md), [host apply's posture](../design/host-render-target.md),
    [the I/O priority default](../design/io-priority.md), [replacing a pack-files directory yolo made](../design/pack-files-for-agent-state.md#OQ-FS3) with [OQ-HT5](../reference/macos-user-home-tiers.md#oq-ht5), [removing a layout link whose workspace is gone](../design/pack-files-for-agent-state.md#OQ-FS5). Only closing a document: [an unmatched-reference hatch](../design/reference-mismatch-diagnostics.md),
    [path mirroring](../reference/workspace-path-mirroring.md), [pack telemetry](../reference/pack-system.md#oq-acp4), [AWS's key
    pair](../design/credential-sources-separation.md#OQ-ES8), [stateful-surface comments](BACKLOG.md#OQ-E4), [the upstream mise
    issue](../design/jail-state-separation-design.md), and [macos-user's stale home-root file and two workspaces at
    once](../reference/macos-user-home-tiers.md).
34. The four calls the 2026-09-26 open-source audit left to the maintainer: whether one public research doc stays
    public, the scratch-directory `.gitignore` entries, a `last-release` recipe from the template, and when to retire
    the Python-version migration; the audit's report is outside this repository, so this is their only record.
35. Decide how far one login is shared — with the host, and between two agents of one provider
    ([universal-login-sharing.md](../design/universal-login-sharing.md)): the host notch and the pi/Muse pair are the
    two gaps, and [its sketch](../design/universal-login-sharing-plan.md) waits on all three questions. Rule the
    canonical copy's location with it, so a `yolo` state reset cannot destroy a login.
36. Work the asks a downstream pack consumer is still waiting on ([downstream-asks.md](downstream-asks.md)); the
    open ones are owned by [the patched-fork design](../reference/patched-forks.md) and
    [the model-list design](../design/model-lists-and-pickers.md). Beside it, the same consumer's 2026-10-22 ask:
    [a pack's own tool, built from its own tree](../design/pack-buildable-programs.md), built and tested on Linux. What
    is left is the Mac, the last task of [its plan](../design/pack-buildable-programs-plan.md): macos-user's
    run-as-you mode fails a pack-tree build at the capture bootstrap, and Apple Container and a Mac's `yolo host`
    have not run it yet.
37. [Make store cleanup automatic even after the last launch](../design/automatic-store-gc.md) — the owner wants no
    routine manual pruning; it waits on [OQ-GC1](../design/automatic-store-gc.md#OQ-GC1) to
    [OQ-GC3](../design/automatic-store-gc.md#OQ-GC3). Its [companion seed](../design/automatic-store-gc-plan.md) parks
    implementation material, and [the independent advice and audit](../research/automatic-store-gc-advisory-20261028.md)
    preserve the evidence behind the choice.
38. [Build the selected GPU graphics-driver tree](../design/gpu-graphics.md) from [its completed plan](../design/gpu-graphics-plan.md);
    a real Linux GPU host then owes tree and Mesa-version verification.

## External waits

39. One session at a Mac, with `sudo`'s password and the runner stopped, completes [the benchmark](../research/macos-backend-performance.md#8-results)
    — [auto-capture](../design/program-delivery.md#decision-ledger), Podman Machine on applehv
    and [NFS from macOS's `nfsd`](../research/macos-vm-runtime-comparison.md#6-is-there-an-open-stack-with-faster-shared-folders)
    — first, as [the comparison's refresh](../reference/macos-no-vm-direction.md#when-to-refresh-this) waits on it. It
    also runs `yolo host -- claude` and `yolo host -- agy` for [the host floor's installer agents](../reference/host-agent-environment.md#HP-D2),
    and a macos-user launch with a dotted `packages` entry, which no CI job runs.
40. [The two-jail check](../research/macos-backend-performance.md#7-found-on-the-way-two-apple-container-jails-may-mount-one-ext4-disk)
    holds since each workspace got its own `/mise` disk. The next `apple-container.yml` dispatch re-asks
    [the keeper's remaining Apple Container runs](../design/jail-lifetime-last-session-wins.md#7-what-i-would-build-in-order);
    someone at the runner Mac upgrades `container` to 1.5.0 (its kernel prompt is interactive) before the VM-local probe.
41. The next `macos-user.yml` run confirms in CI what [a developer Mac ran](../design/macos-user-suite-on-a-developer-mac.md)
    natively: the macos-user suite, [I/O priority](../design/io-priority.md#9-what-i-would-build-in-order), the
    macos-user keeper, [the capture fixtures](../reference/install-capture.md), the host floor's fixture capture, the
    Mac floor's fork build and [the guest notch](environment-manager-plan.md); each doc's `next` names its test. The
    ownership-enabled volume still needs a Mac with root.
42. A human with a subscription login runs [the credential-view runbook](runbooks/claude-credential-view-measures.md)
    for [Claude's login without interception](../design/claude-login-without-interception.md), on a Mac and for a day
    on a rootless host, which releases [`yolo host -- claude` joining the shared login](../reference/claude-oauth-interposition.md),
    and records one browser login and one shared expiry for [the OpenAI service](../reference/agent-credentials.md#openai-measured).
43. A real rootless Linux host, which a nested jail is not, clears [a real reboot](../reference/podman-readiness.md),
    [a low-space collection with a jail up](../reference/image-retention.md#owed-host-steps), [the keeper's scope move and logout](../design/jail-lifetime-last-session-wins.md#8-what-done-looks-like),
    [per-jail host daemons'](../design/host-daemon-ownership.md) first launch, [live config apply's](../design/live-config-apply.md)
    rootless evidence, and the patched fork's rootless check.
44. One live agent session per check, which no test may start: [the footer](../reference/agent-footer.md#what-has-and-has-not-been-observed),
    [Claude's LSP plugin](../reference/mcp-configuration.md#lsp-claudes-route-is-a-generated-plugin), [the via route](../design/wire-bridge-gateway.md#41-how-it-is-built),
    [menus under a model list](../design/model-lists-and-pickers.md#13-build-order-and-what-done-looks-like), [a switch in
    a provider set](../design/active-provider-sets.md#9-what-done-looks-like), [Copilot's config migration](../design/agent-directory-map.md#74-copilot),
    [a Kilo session](../design/gateway-provider-packs.md), [the restart prompt](../reference/attach-skew.md),
    [whether the plain agy slot delivers the agent](../design/pack-files-for-agent-state.md#OQ-FS1),
    [pi loading a pack's folder](../reference/pack-system.md#pi-packages), [host Chrome from `yolo host -- claude` and under
    macos-user](../design/mcp-presets-removal.md#13-what-i-would-build-in-order), and [codex, opencode and pi on
    Bedrock](../design/bedrock-plumbing.md#8-behaviour-this-design-fixes) under SSO. Check [Pi launch selection](../design/model-lists-and-pickers.md#1411-pis-provider-flag-travels-only-with-an-explicit-model-built-2026-10-07)
    and [simultaneous native authentication](../design/simultaneous-auth-and-pack-isolation.md) in a live session;
    launch arguments and source inspection do not establish authentication isolation.
45. A human with the cloud account clears [a Bedrock API key or static key pair](../design/bedrock-plumbing.md#64-the-credential-three-are-supported),
    [an SSO lapse mid-turn](../reference/agent-credentials.md#sso-chain-order), [settings-only Bedrock mode](../reference/providers.md#two-channels-split-by-payload-type),
    [an AgentCore gateway](../design/bedrock-web-search.md) and [a Claude usage-limit response](../design/wire-bridge-gateway.md#5-part-4--the-subscription-arm-and-opt-in-failover-ruled).
46. The maintainer's own commits and launches clear [the `matt` pack's `briefing/`](../reference/pack-system.md#briefing),
    [its posture list at the host](../reference/pack-system.md#posture-lists-owed),
    [a dropped pack's host output](../reference/pack-system.md#retiring-a-dropped-packs-host-output), [the fzf
    pack](../examples/claude-fzf-pack/README.md#adopting-it), his pack's move to pi's package folder, [the listen-port fix where reported](../reference/wire-bridge.md#what-can-hold-the-listen-port-before-the-bridge-does)
    and [the runner Mac's published-ports change](runbooks/mac-actions-runner.md).
47. A live `llama-server` and one manual turn per agent clear [the `llamacpp` provider](../../packs/llamacpp/README.md),
    whose spellings have never met a server; copilot is the one to try first.
48. The mise use record's first 30-day window gives [the disk footprint's re-measure](../reference/disk-footprint.md#owed-measurements)
    its first offer to check, on the host.
49. Nix dropping Intel Macs, when nixpkgs 26.05's security fixes stop on 2026-12-31, brings back [what replaces the
    Intel macOS runner](../research/macos-support-matrix.md#OQ-MX1), deferred until then.
50. The maintainer files the upstream reports drafted with the VM-local design, on [VZ's virtio-fs cache policy](../research/apple-container-file-cost.md#21-per-file-cost-not-bandwidth)
    and [VZ keeping freed guest memory](../research/macos-vm-runtime-comparison.md#42-adding-a-balloon-to-apple-container-ourselves)
    — last, since only Apple can ship either fix.
51. Native macOS verification of [JD-10's guest pack-tree lifetime](../reference/macos-user-provisioning.md#JD-10):
    root ownership and modes, Seatbelt execution under overlapping launches with distinct trees, a daemon restarting
    from its own unchanged module, and capture leaving both trees unchanged. No native execution has established these.
52. Prototype [macos-user's ruled model](../design/macos-user-model.md#7-the-ruled-model-two-modes-one-profile) (run as
    you by default under a deny-by-default profile, the second account an optional mode on the same profile; [OQ-MM1](../design/macos-user-model.md#OQ-MM1)
    to [OQ-MM6](../design/macos-user-model.md#OQ-MM6)) on a Mac in both modes, from [its plan](../design/macos-user-model-plan.md);
    the default flips only after a human reruns the prototype checks against the generated profile. Until the account
    mode's [slice 12a](../design/macos-user-model-plan.md#MMP-D25) lands, a second workspace is refused with the
    no-edit step ([MMP-D26](../design/macos-user-model-plan.md#MMP-D26)). The [configurable workspace root](../design/configurable-workspace-root.md)'s
    questions wait on it. Inside that profile [mise cannot reach HTTPS](../design/macos-native-tls.md); the
    design owns the remaining Safehouse and Copilot measurements and blocks graduation until both are resolved.
    [Named setups](../design/named-setups.md) replace `confinement`, `runtime` and `macos_user_mode` with one named set
    of values per launch. Its [plan](../design/named-setups-plan.md) owns slice state: 6a-iii, 6b and slice 11 (one
    machine-wide `yolo ps` and `yolo stores`, [§12](../design/named-setups.md#12-listing-what-runs-yolo-ps-and-yolo-stores))
    are next, and W-SU1/W-SU4 need the owner's Mac. The Mac default moves to `seatbelt-self` only with slice 11's
    default flip, when the owner calls the Seatbelt path ready.
53. [Sharing MCP sign-ins](../design/mcp-auth-sharing.md) — Phase A and Task 12's launch-boundary foundations are
    integrated; the full combined landing gates remain, Tasks 13–15 are separate, and no real sign-in has been
    exercised. [The plan](../design/mcp-auth-sharing-plan.md) records the exact boundary.
54. [Let a pack's broker use the approval machinery](../design/pack-brokers.md), and
    [let the agent read and predict its own access](../design/access-introspection.md) — G3 core is partly integrated,
    and G4's HTTP front is integrated but not activated. The next G4 candidate is blocked on one ownership-lifetime
    gap pending repair and a new review; [Task 3](../design/pack-brokers-http-plan.md#g4-task-3-local-tls-authentication-and-the-host-generation-seal)
    has nothing built, and publication stays off. [OQ-HTTP2](../design/pack-brokers-http.md#OQ-HTTP2) holds only a
    selected client shown incompatible, [OQ-HTTP1](../design/pack-brokers-http.md#OQ-HTTP1) gates only custom
    executor/signing, and `ForAddRemember` stays off under [OQ-BX7](../design/pack-brokers.md#OQ-BX7).
    [The HTTP plan](../design/pack-brokers-http-plan.md) and [the broader seed](../design/pack-brokers-plan.md) record
    the boundaries.
55. [Bound Yolo workspace work in owner-managed resource groups](../design/resource-groups.md) — the default-off
    selection, finite-policy planning and fake-only admission boundary are integrated; no production caller, native
    placement or enforcement exists. Stable-name exclusion, the real writer, placement, device and performance
    evidence, and the combined/native gates remain.

## Boundaries

- **Not committed to:** the unverified candidate ideas filed in [`BACKLOG.md`](BACKLOG.md#ideas-from-deleted-docs).
  **Iced**, with what would thaw each: [`yolo cache relocate`](cache-relocation.md) (someone wanting it), [trust-gated
  grants in a local config](../design/workspace-config-trust.md) (a read-write mount that cannot move into the
  workspace), [in-jail nix on the container Macs](setup-support-gaps.md#2-ranked-gap-backlog) (a user for whom
  `packages:`, mise and macos-user all fall short), [an in-jail agent's request for a live config apply](../design/live-config-apply.md#LA-S5)
  (the boundary broker's request store and doorbell being ready) and [a central host watcher](../research/central-yolo-watcher.md#7-what-would-have-to-be-true-before-building-one)
  (a duty that must hold a listener across launches).
- **Ids Vantage cannot index**, reached here by name: [Q1 to Q9](../design/environment-manager-user-stories.md#Q1),
  [9.2 and 9.6](../design/host-render-target.md#9.2), [CFP-1 to CFP-3](../design/composed-file-permissions.md#CFP-1),
  [Q2 and Q3](../design/macos-user-build-step-threat-model.md#Q2), [SS-6](../design/jail-state-separation-design.md#ss-6),
  [E1, E2, E5 and CO](BACKLOG.md#E1), [the host-files mode](pack-host-management-plan.md#open-questions), and two
  outside this queue: [OQ-SH1](../research/macos-support-matrix.md#OQ-SH1),
  which blocks nothing, and [JD-4](../reference/macos-user-provisioning.md#JD-4), a maintainer follow-up.
- **With no id at all:** [Go comments citing sections by number](README.md#the-dangling-n-citations-in-go-comments),
  [check 2's two marker conventions](README.md#check-2-has-two-known-errors-and-a-convention-question-under-each),
  [the storage lifecycle's `min-free` values](../reference/image-retention.md#owed-host-steps), the unruled half of
  [OQ-ST2](../reference/pack-system.md#oq-st2) on user-reserved skill children, and whether a bare `-p` whose
  packs install no CLI should say it selects nothing (`checkProfileTargets`, `internal/cli/run/packs.go`).
