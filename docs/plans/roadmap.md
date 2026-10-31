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

**Reconciled:** 2026-10-19; 0.14.0 was tagged 2026-10-15 (release commit `6750951f6`).

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

1. [Enable profile-served daemons on attach without ending sibling sessions](../design/attach-daemon-activation.md)
   — the reported late Bedrock selection reaches a restart prompt; settle additive activation
   before treating restart as the remedy for an already-authorized missing daemon.

2. [Finish ongoing launch preparation and explicit update-and-wait](../design/launch-update-preparation.md)
   — reuse the admitted-build pipeline before optimizing isolated waits; its
   [combined implementation map](../design/launch-update-preparation-plan.md) connects scheduling,
   freshness, HOST, Apple checks and supported ordinary npm capture. Independent review and final
   combined runtime/platform acceptance remain. Producer details stay in the
   [ordinary capture section](../design/launch-update-preparation-plan.md#ordinary-cli-and-mcp-capture).
   [The preparation design's timing acceptance](../design/launch-update-preparation.md#visibility-and-acceptance) bounds current timing claims.

Pi's existing profile-set/provider-seal extension is built, including its required child guard.
[MM-D42](../design/model-lists-and-pickers.md#MM-D42)'s process-local profile-derived child
defaults and model restrictions: **the mechanism is built, and yolo ships no declaration of it**
(2026-10-16). A pack of the user's own may declare `launch_computed` for a pi-subagents that reads
a flag; yolo depends on no fork. Do not invent a yolo settings overlay or rewrite shared settings. Other
accepted limits remain in
[its design's §3.4](../design/simultaneous-auth-and-pack-isolation.md#34-what-the-extension-cannot-close),
and a pi-subagents registry several extensions can share stays in item 28.

3. [Apply config changes to a running jail](../design/live-config-apply.md) — the maintainer's
   2026-10-18 ask, ruled and built the same day; v1 is landed, with rootless CI evidence still owed.
   ⚠ **Five of its own `-short` tests fail on a Mac** (measured 2026-10-19 at `8d8e73653`), in two
   causes, neither visible to Linux CI: three are the darwin symlink class, and two inherit the
   host podman's rootless answer and so assert `rc = 0` where an inconclusive probe gives
   restart-required `rc = 3` — which is also a live question about the feature, since an
   unprovable rootless fact is a real production state. Both are set out in
   [the design's status block](../design/live-config-apply.md). **All five pass on a Mac at
   `94989f129`** (2026-10-20, unresolved `TMPDIR`), so the repairs are verified natively. Covers: read-only and read-write mounts, their removal,
   GitHub scope, and the same gate at an interactive attach. Its later slices are staged here,
   each built on its own after v1: [the next-session moves](../design/live-config-apply.md#LA-S4a),
   [Apple Container's briefing re-copy](../design/live-config-apply.md#LA-S4b),
   [resources through `podman update`](../design/live-config-apply.md#LA-S4c) and
   [live host-port forwards](../design/live-config-apply.md#LA-S4d).

4. [Give each session in one jail its own profile](../design/per-session-profiles.md) — the
   maintainer's 2026-10-18 report: an attach on another profile, or a bare attach of another agent,
   re-renders the files a running pi's subagents read, and they deadlock against its provider
   policy. The jail and existing host session-home mechanism are built. The 2026-10-19 host rulings
   require `own` for launches and normal managed-config capture for owned session edits; neither is
   built yet. Implement them in the [plan](../design/per-session-profiles-plan.md). macos-user and
   other agents remain.

5. [Let the macos-user suite run on a developer Mac](../design/macos-user-suite-on-a-developer-mac.md)
   — the first full `-run '^TestMacosUser'` on a Mac that grants only the run-as rule (2026-10-17,
   128 passed / 3 skipped / 12 failed, every failure reproducible). The four DM rulings are now
   compacted in the design's [Decision Ledger](../design/macos-user-suite-on-a-developer-mac.md#decision-ledger):
   no root `sudo`, honest counted skips for the host log policy, mise shims continue to win, and
   local measurements count with provenance. Original Mac commits `98715e67b` and `41dd11e3f`,
   and the doorway, housekeeping and floor-test repairs remain in current main ancestry. Portable
   guard hardening followed in `cab34b522` and `201c4009e`; the `4e7b9b92b` commit itself changed
   only a documentation heading. **Native-Mac execution is now VERIFIED: 2026-10-19 at
   `c3a38e634`, `-run '^TestMacosUser'` exits 0 on that host — 150 passed, 4 skipped, 0 failed,
   626 s, ledger `executed=100 skipped=14`.** Every one of the twelve 2026-10-17 failures is
   gone, no test asked for root `sudo`, and the host's log policy produces the two counted skips
   the ruling designed, one of them only the `macos_log_store_read_refused` subtest so the other
   nine rules still assert. That covers the re-runs owed for
   [§6.2](../design/macos-user-suite-on-a-developer-mac.md#62-another-sessions-doorway-was-read-as-this-sessions-survivor),
   [§6.3](../design/macos-user-suite-on-a-developer-mac.md#63-the-housekeeping-tests-slot-was-not-due-because-its-stamp-is-the-machines)
   and the floor test's changed diagnostic. **The narrow-sudo cell passed there too** (all three
   tests), which that host satisfies natively rather than by narrowing `sudoers` as CI must. Still
   owed: a `macos-user.yml` run, and the ownership-*enabled* volume, which needs root. That first
   run cleared measurements items 38 and 40, listed in the design.

6. **Retain selected pack roots through `yolo check`'s host-floor read**
   **Linux verification (2026-10-19):** the production-callsite regression passes, and
   disabling retention reproduces the historical warning. **Native Mac (2026-10-20, `94989f129`
   built from source):** `yolo check --no-build` on a host selecting `pi-fork` reports `pi` PASS
   from the floor copy and leaves no new `$TMPDIR/yolo-*` entry. On the same machine the installed
   `0.14.0+562` binary, which predates the fix, still prints the `root cannot be opened` warning.

   **Review note (P2):** a controlled signal during a later check section can call `os.Exit`
   before the cleanup defer runs, leaking the retained temporary pack tree. No credential
   exposure was demonstrated. This landing does not repair the leak; follow-up work should
   connect cleanup to controlled signal teardown and add a subprocess interruption regression.

   — measured 2026-10-19 on a host selecting a patched fork. The Packs section stages each
   entry under one temp tree and removes it when the section returns, but the loaded packs it
   keeps carry a `Root` inside that tree, so the floor section prints the false
   `pi — no floor entry: … the pack's root cannot be opened` warning one screen after
   `[PASS] pi-fork: 143 file(s) stage`. The production-callsite regression keeps the staged
   tree alive for its later reader and verifies cleanup on ordinary return. The full mechanism,
   evidence and rule it breaks are in [the pack system's check/launch callout](../reference/pack-system.md#one-governance-reader).
   No ruling needed.

7. **Apple Container builds no patched fork or extension on any Mac that has a container running**
   — a verified live defect, measured 2026-10-19, with two independent causes and a ruling owed on
   the second. The build-capability gate splits `container ls` on whitespace and demands the
   header's column count, but `MEMORY` renders as two tokens (`12288 MB`), so a 9-field header
   meets 10-field rows and every non-empty listing is *"an unrecognized listing"*; a launch then
   refuses with "no build on this machine". Header-only output parses, which is why no runner has
   caught it — and the unit fixtures are a synthetic 5-column table the CLI never emits, while the
   sibling parser for the same command (`runtime.ParseContainerLsLive`) reads the real table fine.
   ⚠ **Fixing the parse is not enough:** the `yolo-` prefix counts yolo's own
   `yolo-ac-builder` image builder as a running jail, so the decision becomes "Apple Container has
   running yolo jail(s)" and still refuses. Both are set out in
   [the store-build design's Apple arm](../design/pi-extension-store-builds.md). podman is
   unaffected (the gate is `runtime == "container"`). **Cause 1 is fixed and verified (2026-10-20,
   `94989f129`)**: a real Apple Container 1.1.0 listing now parses as valid. The verdict is still
   *"running yolo jail(s): yolo-ac-builder"*, so only the cause-2 ruling remains.

8. **Two launch diagnostics that send a reader the wrong way** — both measured 2026-10-19 and
   written up in [the lab report](../lab-reports/2026-10-19-launch-diagnostics-on-a-mac.md); each
   cost a real wrong conclusion in that session, and each needs a small ruling rather than a
   repair. **(a)** The runtime a launch chose reaches the launch log only (`logRecord` writes
   `LaunchLogOnly`), and `YOLO_BACKEND` — not a yolo dial, the dial is `YOLO_RUNTIME` — is
   accepted in silence, so two Apple Container launches were reported as podman and a
   backend-specific defect (item 7) was attributed to both. ⚠ The screen line needs no registry;
   warning on unknown `YOLO_*` would need one yolo deliberately does not keep. **(b)** A `Ctrl-C`
   during provisioning prints *"Provisioning log could not be written … repair workspace .yolo
   permissions or free disk space"* when neither holds: the writer was merely signalled
   (`PIPESTATUS` `_plog`, setup `_prc` = 141 = SIGPIPE), and the same run then appended
   `PROVISIONING FAILED (exit 141)` to the file it called unwritable. **(a) is RULED 2026-10-19 —
   both halves, name the backend on screen and warn on an unknown `YOLO_*`** — so it is
   actionable now; (b) still wants its small ruling.

9. **A podman launch cannot use a workspace reached through a symlink** — measured 2026-10-19, and
   the first real podman launch on that host. `YOLO_RUNTIME=podman yolo` in `/tmp/jail_test` dies
   with `Error: statfs /tmp/jail_test/.yolo/home/local: no such file or directory` naming a path
   yolo had just created. The cause is the `/tmp` → `private/tmp` symlink: podman's macOS VM
   cannot resolve it, so `/tmp/jail_test` fails to bind while `/private/tmp/jail_test` binds.
   ⚠ This is [AGENTS.md](../../AGENTS.md)'s darwin symlink class on the **production** side, and
   `preparePodmanBindSources` — written because podman's bare `statfs` *"reads as a yolo bug
   rather than a missing directory"* — creates the sources without resolving the path they are
   spelled with. Resolve before handing podman a bind, or preflight and refuse with the resolved
   spelling; [the lab report](../lab-reports/2026-10-19-launch-diagnostics-on-a-mac.md) weighs
   the two. Reproduced twice more the same day, naming `yolo-gitconfig` and then `npm-global`, so
   the path in the error is just whichever bind podman checks first. One launch also left
   `yolo-gitconfig` unwritten, which is unexplained.

10. **`yolo host --` calls its cwd a bind-mounted workspace, so it refuses `aws-auth` in the home**
    — measured 2026-10-19. The host notch takes `os.Getwd()`
    ([`host.go:2399`](../../internal/cli/host.go)) and hands it to the placement rule, whose
    workspace branch is worded for a jail (*"the workspace this launch bind-mounts :rw"*); the
    host notch mounts nothing. Run from `$HOME` that tree contains `~/.local/share/yolo-jail`, so
    the rule refuses the loophole serving Bedrock credentials, and ⚠ the refused tree is
    `embedded-packs` — kept out of `cache/` precisely so no agent writes it. The empty-workspace
    path the rule documents (*"a caller with no workspace in hand"*) already exists and
    `internal/loopholes` uses it; this notch does not. Two notes beside it: the refusal did not
    stop the doorway from opening, and a jail launch would have refused that cwd outright via
    `refuseWorkspaceScope`, which the host notch bypasses. Details and the workaround are in
    [the lab report](../lab-reports/2026-10-19-launch-diagnostics-on-a-mac.md).

11. [Finish the readable-config redesign](../design/readable-config.md) — the maintainer,
    2026-10-24: *"this is important"*. Most of it is built; what remains is listed in its
    [implementation plan](../design/readable-config-plan.md#verified-implementation-state), and
    the redesign graduates to a system doc once that is done. It stays here because the TOML
    release ships with it: [the user config](../design/config-user.md),
    [a project's file](../design/config-project.md), [editing commands and format](../design/config-commands.md),
    [migration](../design/config-migration.md) and [pack manifests](../design/pack-manifests.md).

12. [Complete the computed-briefings plan](../design/computed-briefings-plan.md), from the
    [computed-briefings design](../design/computed-briefings.md) — the maintainer, 2026-10-24:
    a pack's briefing should tell each workspace's agent exactly how to finish a change (commit
    only, push a named branch, or push a new branch) according to the grants that workspace was
    approved, now that push grants exist. It reads the grants by what the broker does, not by
    their config spelling, so it does not wait on the readable-config redesign above.

Native readiness and credential-store work retain their places below: assess
[the privilege prerequisites](../research/sandvault-macos-privileges.md) without broad root
cleanup, and rule [keychain scope/unlock/migration](../design/keychain-from-a-jail.md) without
sharing host credentials. Keep [storage relocation](shared-tool-store-relocation.md) and
[OrbStack](../research/orbstack-as-a-podman-host.md) separate from these urgent launch repairs.

## The queue

1. [Finish the patched-fork mode's verification](../reference/patched-forks.md#14-unbuilt-and-unmeasured), with
   [its pi-extension companion](../reference/pi-extensions.md#16-unbuilt-and-unmeasured) — the compatibility
   release is published; keep the remaining verification ahead of dependent work. [OQ-PPX3](../reference/pi-extensions.md#OQ-PPX3)'s 2026-10-16 ruling (no automatic fallback to an older build) is built. Beside it: [the build line shown by its
   digest](../reference/report-tiers.md#why-its-this-way), built 2026-10-08 with `yolo pack status`; [whether a Mac fork build
   installs its base's `node_floor` first](../design/forked-programs-as-packs.md#OQ-FP11); his test with the migration kit.
2. [Per-jail host daemons (HD-R1)](../design/host-daemon-ownership.md) — brought up to date with main and finished on
   branch `hd-r1-land`, ready for the maintainer to merge. The coexistence call it was held on was answered 2026-10-16
   (*"on 2, of course bring that up to date and merge it. why wouldn't we?"*): jails an older yolo launched keep its
   machine-wide daemon, left alone until they end or the host reboots
   ([HD-D26](../design/host-daemon-ownership.md#HD-D26)). The 2026-10-15 rulings are built on the branch: the at-launch
   catch-up refresh ([OQ-HD9](../design/host-daemon-ownership.md#OQ-HD9)), the supervised restart
   ([OQ-HD5](../design/host-daemon-ownership.md#OQ-HD5)) and the jail-daemon requirement
   ([OQ-HD4](../design/host-daemon-ownership.md#OQ-HD4)). Next because it retires the machine-wide credential daemons;
   after the merge, only a real host and CI can check what item 42 lists.
3. **Implement user-only jail inheritance exclusions first** — [the settled design](../design/jail-inheritance-exclusions.md) and [completed build plan](../design/jail-inheritance-exclusions-plan.md) now define dormant known targets, quiet no-ops for missing members, and exact build gates. The implemented mechanics system is now documented in the [jail-mechanics reference](../reference/jail-mechanics-skill.md); outstanding native macOS and real-rootless observations are scoped in its [verification plan](jail-mechanics-verification.md). Then address rulings holding other builds: [whether a project's mise pin of `pnpm` stays hidden](../design/program-delivery.md#oq-pd19), with [autoprune's `$GOBIN` class](../design/program-delivery.md#oq-pd20) and [a pack's agent-CLI pin](../design/program-delivery.md#oq-pd21); [whether a pack service's in-jail daemon is disclosed](../design/trust-paths.md#OQ-TP11), as no launch line names the wire bridge; [whether copilot and oh-omp alone get `-p bedrock`](../design/bedrock-plumbing.md#OQ-BR24).
4. Rulings that hold a defect's fix: [the Kilo special-casing](../design/gateway-provider-packs.md) (its 1M-window defect is fixed); [`--no-daemon` for in-jail `codex`](../research/codex-background-service.md#OQ-CDX3),
   since `codex agents` starts the daemon yolo turns off; [whose OpenAI login host pi uses](../design/pi-host-openai-auth.md),
   since its first refresh replaces yours, with its catalog-refresh question; and [a leftover `mcp-adapter.json` at the
   host](../reference/mcp-configuration.md#OQ-MC2), which host apply never deletes and pi keeps loading, with
   [OQ-MC1](../reference/mcp-configuration.md#OQ-MC1). For pack files: [skipping files for an unselected agent](../design/pack-files-for-agent-state.md#OQ-FS6)
   instead of refusing the launch, [warning on a folder another pack keeps state in](../design/pack-files-for-agent-state.md#OQ-FS2), and [whether a Mac
   bootstrap stops at a failed home layout](../design/pack-files-for-agent-state.md#OQ-FS4).
5. [Rule whether a successful `yolo host apply --assert` names a next command](../reference/happy-path-principle.md#the-rules)
   — rule 2 calls such a success a dead end, `--assert` ends at its counts, and the line waits on a ruling against [the
   verdict block](../reference/report-tiers.md#the-verdict-block)'s "Only the dry run has a footer"; this entry is the
   work's only home. Rule [the dry run's exit](../reference/report-tiers.md#OQ-RO8) with it: the code exits 1 where
   [OQ-RO5](../reference/report-tiers.md#why-its-this-way) says 0, and the launch gate reads that exit.
6. [Complete the remaining build-update slices](../design/pi-extension-store-builds.md), through
   [ongoing preparation](../design/launch-update-preparation.md) — the Podman built-tree detached
   slice is in main; host delivery/reporting, Apple Container check-only, fork/capture timing and
   native/performance acceptance retain their scope rather than disappearing into a tree-only fix.
   The [matched development-bundle design](../design/matched-development-bundles.md) and its
   [completed implementation plan](../design/matched-development-bundles-plan.md) add the paired,
   build-only launcher plus a checkout-resident, symlinkable wrapper that discovers the source checkout while preserving the invoking workspace, without changing normal installation.
7. [Rule how the Claude footer moves into a Claude Code plugin](../research/claude-code-extensions-footer.md), with [how
   yolo manages Claude Code mods](../research/claude-code-mods-management.md) — the plugin ends a process per refresh,
   and the mods rulings rest on that route.
8. Rule [the VM-local disks design](../design/vm-local-volumes.md): [default or opt-in](../design/vm-local-volumes.md#OQ-VL1),
   [`~/.cache`](../design/vm-local-volumes.md#OQ-VL2), [database folders](../design/vm-local-volumes.md#OQ-VL3) and
   [how a new disk fills](../design/vm-local-volumes.md#OQ-VL4),
   with [the probe that checks its premise](../research/apple-container-file-cost.md#5-a-planned-probe-on-the-mac-runner)
   and [VZ's memory request](../research/macos-vm-runtime-comparison.md#43-draft-feedback-assistant-request) to file — the
   largest Mac speedup measured waits on it (pip install 8.35 s on a shared folder, 2.01 s on Apple Container's own disk).
   Beside it, the maintainer's 2026-10-08 request: [tools from a private Homebrew tap in every jail](../design/private-tap-programs.md),
   whose build waits on [a pack or a config key](../design/private-tap-programs.md#OQ-PT1),
   [brew's copy at the host](../design/private-tap-programs.md#OQ-PT2), [the version policy](../design/private-tap-programs.md#OQ-PT3),
   [never `brew tap`](../design/private-tap-programs.md#OQ-PT4) and [a launch without the token](../design/private-tap-programs.md#OQ-PT5).
9. Approve [the drafted rewordings](../research/macos-backend-performance.md#9-corrections-the-results-feed) of
   [the direction's ruled sentences](../reference/macos-no-vm-direction.md#what-the-numbers-bear-on) — the
   benchmark's other corrections are made, and the two-jail test passes in CI again.
10. [Time what a launch's spans leave out](../reference/perf-logging.md) — built 2026-10-08: the provisioning stage
    writes its duration to the jail perf log, and the Apple Container delivery test reads every dotted span. That test's
    Mac half is compiled, not yet run; one Mac run shows the build skip's saving.
11. Record the Mac runs of 2026-10-02 and 2026-10-03 where each doc's `next` names them — agent work now, ahead of the
    rulings below because the context-mount cases free one: [the AWS doorway](../design/host-notch-services.md), [its
    Bedrock design](../reference/agent-credentials.md#sso-backed-bedrock-credentials-aws-auth), [A2's twin](../reference/macos-user-nix-and-features.md#ordering-and-what-aborts), [the
    render-mark twin](../reference/config-target-resolution.md#where-this-does-not-reach),
    [item 1's twin](runbooks/macos-user-manual-checks.md#1-the-privilege-transition),
    [DP-B6](../design/declaration-parity.md), [the footer](../reference/agent-footer.md#what-has-and-has-not-been-observed), [the
    build sandbox](../design/macos-user-build-step-threat-model.md), [the context mounts](../reference/context-mounts.md),
    [Cachix](../reference/image-staging-vs-baking.md#the-binary-cache), [the in-VM copier](../reference/image-staging-vs-baking.md#the-first-load), [the login
    seed](../reference/jail-home.md) and [the storage classes](../reference/jail-home.md).

    Build [the bounded privilege lifecycle](../design/macos-user-privilege-lifecycle.md) from
    [its plan](../design/macos-user-privilege-lifecycle-plan.md), with the native readiness work:
    an agent can start and stop macos-user jails unattended only once no launch or exit stops at a
    password prompt, which is what testing and developing this backend unattended needs. The ruled
    grant runs commands as the sandbox account alone, never as root, and never account-wide.
12. [Rule the macos-user workspace root](../design/configurable-workspace-root.md), [its whitelist](../design/configurable-workspace-root.md#OQ-CW2)
    first — a `/var/root` home passes until it replaces the blacklist, and tightening later withdraws what users rely on.
13. [Decide whether a workspace config may feed host dotenvs and mounts into a jail](../research/agent-safehouse.md#OQ-AS3)
    — a committed config still can, and notch convergence's `env_sources` item waits on it.
14. [Rule which packs a workspace config may select](../design/workspace-packs.md), and [retire `mcp_presets`](../design/mcp-presets-removal.md)
    — [OQ-PK1](../reference/pack-system.md#oq-pk1)'s 2026-10-23 ruling deletes the key outright and asked for that design first; until it
    lands, a repository cannot give its collaborators the browser, which the presets did.
15. [Rule the slot split's migration window](../design/slots-and-contributions.md#OQ-D6), then [the manifest
    language](../design/manifest-language.md) with [the slots' other calls](../design/slots-and-contributions.md) —
    `exposes` and [the pi extension-tree rework](../design/pi-pack-extensions.md) wait on the first.
16. Provider rulings later work builds on: [whether the natively-implements rule reaches a list a pack declares](../design/pi-codex-provider-shadowing.md#OQ-4),
    which holds what [its broad reading](../design/pi-codex-provider-shadowing.md#65-what-the-build-decided) left in place, and [what `-p`
    names](../design/providers-and-profiles-redesign.md), which the provider reference's rewrite waits on.
17. [Notch convergence's held items](notch-convergence.md) — `env_sources` waits on item 13, the one `rmw` rule on [a
    table not declared in full](../reference/config-ownership.md#OQ-CO15) (rule [OQ-CO16](../reference/config-ownership.md#OQ-CO16)
    with it), and `yolo host -- claude` joining the shared login on the credential view's measures, below.
18. [The provisioner's calls](../design/provisioner-sets.md) are all ruled (2026-10-23); what remains is building
    the one `program` kind that any selected pack may fill, and the separate account's Go trust.
19. [Rule what the environment manager promises at each notch](../design/environment-manager-user-stories.md) — Q1b
    decides [`yolo check --at`](../reference/what-yolo-is.md#check-at-a-notch) and Q7 [the Linux guest notch](environment-manager-plan.md).
20. [Rule the keychain from a jail](../design/keychain-from-a-jail.md), which settles [Copilot's machine-wide
    login](../research/copilot-token-storage.md). Review its broader native-credential-store direction with the
    sandbox account's missing-keychain reports: storing suitable tokens responsibly must not wait on Copilot alone,
    and the account's unlock, workspace scope and migration choices need to be considered together.
21. [Rule the starting tab's Ctrl-C in a shared jail](../design/jail-lifetime-last-session-wins.md#OQ-JL10) — the
    keeper at `yolo host` is ruled; its build waits on [the sidecars and doorbell](../design/agent-event-watchers.md).
22. [The boot snapshot and diagnostic dial](../design/diagnostics-past-the-boundary.md) — a refused boot keeps no record
    of the jail's state, and the readiness act built 2026-10-06 adds one more refusal.
23. [Rule the add-only model lists](../design/model-lists-and-pickers.md) and [web search on Bedrock](../design/bedrock-web-search.md)
    — what most agents' menus show turns on the first, and no agent gets search from Bedrock on runtime.
24. [Decide whether pack-declared traps fold into the agent directory map](../design/agent-directory-map.md) — the map
    would supersede [the traps design](../design/pack-declared-file-diagnostics.md), so rule it before building either.
25. [Measure the in-jail nix root watcher end to end](../reference/in-jail-nix-roots.md) — the running jail observed a tiny build through the watcher, but host root resolution remains unverified; the attempted shared-store root query pruned stale entries, so finish with a safe host-side observation.
26. [Review H4's staged capture store on macos-user](../reference/install-capture.md#build-order), [rule whether integration
    sharding unparks](integration-parallelism.md) with [the test suite's other levers](test-suite-speed.md), and [name
    the real forked program](../design/forked-programs-as-packs.md), the last input its step 7 needs.
    Beside suite speed, [make completion change-aware and read-only](../reference/change-aware-completion.md),
    with [its measured evidence](../reference/change-aware-completion.md#measured-on-2026-10-09)
    — avoid irrelevant repeated gates without changing source landing or CI coverage.
27. [Rule how a jail reaches a worktree's `.git` outside it](../research/herdr-integration.md), with the other herdr calls
    — measured: a read-only bind refuses every commit, a read-write one lets a jail prune the worktree.
28. Calls left at one decision each, each with its facts and a leaning: [the `:ro` degradation rows](../design/composed-file-permissions.md),
    [whether messages name the guide's URL](../design/docs-website.md#OQ-DW3), [the macOS nix build sandbox](../design/macos-user-build-step-threat-model.md),
    [array-append pinning](BACKLOG.md#E5), [the pack system's other calls](../reference/pack-system.md), [relocating
    `.yolo` by a link](../reference/jail-home.md#OQ-JH1), [whether to ask pi-subagents for a registry several
    extensions can share](../design/simultaneous-auth-and-pack-isolation.md#34-what-the-extension-cannot-close), [the shared tool store's relocation contract](../design/shared-tool-store-relocation.md)
    with [its build handoff](shared-tool-store-relocation.md).
    Beside the storage rulings, [rule how one workspace splits across storage tiers](../design/storage-tiers.md), with its
    [implementation sketch](../design/storage-tiers-plan.md) — the maintainer asked for it 2026-10-09, and its disk-presence
    and backend calls are the same as the tool store's, so rule them together.
    Beside them, [isolate ordinary jail caches](../design/cache-isolation.md) — private warm state
    needs safe reclamation alongside it, not another unbounded storage surface.
29. [Rule whether yolo may hold an editor preference](../design/baked-editor-preference.md) — the maintainer asked for
    it, and its first ruling decides an image change every jail pays. Beside it, also asked for by the
    maintainer: [how a SandVault or Safehouse user tries yolo on their existing setup](../design/sandvault-safehouse-compat.md#OQ-NB1),
    then [which backend a SandVault setup gets](../design/sandvault-safehouse-compat.md#OQ-NB2) and
    its two scope calls, [credentials](../design/sandvault-safehouse-compat.md#OQ-NB3) and [the trial's `.yolo/`](../design/sandvault-safehouse-compat.md#OQ-NB5) — the first decides the command
    surface the import and trial build on.
30. [Build agent access approvals in the maintainer's five steps](../design/boundary-broker.md#11-the-build-in-the-maintainers-five-steps) —
    he set the order on 2026-10-23: brokered sources with read and write sets, actionable toasts, a host CLI to
    view and approve, a web UI out of tree on that CLI, then a talk about merging it. First is
    [the broker-core factoring and the git source](../design/git-broker.md), whose factoring waits on nothing and
    whose git source is ruled (a brokered transport, created branches persisted, a `create-branch` grant whose kind the
    user's config allows and whose repository the workspace's config names, joining at the config-change approval) and
    [planned for building](../design/git-broker-plan.md);
    writes ship on the agent's own background wait, as [OQ-BB17](../design/boundary-broker.md#OQ-BB17) ruled, and a
    workspace's own `repos` list still requests repositories at the human's y, as
    [OQ-BB18](../design/boundary-broker.md#OQ-BB18) ruled. On top of the GitHub working set,
    [which writes to a PR or issue run unasked, per user and per workspace](../design/github-write-scopes.md)
    waits on its six rulings. Beside it,
    [workspace widening](../design/workspace-widening.md) graduates once [OQ-WW1](../design/workspace-widening.md#OQ-WW1) is ruled.
    Also beside it, asked for by the maintainer on 2026-10-09 and ruled to build on 2026-10-23: [the paste-to-host
    stopgap](../design/host-run-mailbox.md), whose [first slice](../design/host-run-mailbox-plan.md) is ready to build.
    It removes the copy-back of a host command's output until the broker's store and doorbell exist, with a line any
    agent or human writes by hand, and stays paste-only ([BB-D91](../design/boundary-broker.md#BB-D91)).
    **Neither is done until it is documented for users** (the maintainer, 2026-10-23: *"at the end of this there's
    proper evergreen documents and a user guide section that's up to date with how to precisely use all of this"*):
    each graduates into a system doc under `docs/reference/`, and the user guide gains a section on running a host
    command from the jail and on granting the agent GitHub and git writes (grants, the working set, `yolo approve`).
    Also asked for on 2026-10-09: [image paste in every agent](../design/image-paste.md), where [how far Ctrl+V is
    consumed](../design/image-paste.md#OQ-PA2) shapes the key handling, with [closing macos-user's possibly open
    pasteboard](../design/image-paste.md#OQ-PA1) and [the default](../design/image-paste.md#OQ-PA3); its first slice,
    the host reader and `yolo paste`, waits on none of them.
31. Finish [image build reuse](../reference/image-staging-vs-baking.md) with native Apple Container stock-tag dispatch and warm-launch validation; Linux baked, ambient and lean record reuse now has live evidence. Then [Podman Machines pinned to applehv](../research/macos-vm-runtime-comparison.md#32-on-a-shared-mac-folder),
    as Podman's installer gives libkrun, the slowest shared folder measured. Then
    [make OrbStack a working Podman host](../research/orbstack-as-a-podman-host.md), with [its native verification
    plan](../research/orbstack-as-a-podman-host-plan.md), as its measured shared-folder speed and memory return offer
    another Mac option without restoring Docker first.
32. Builds no ruling holds: [the readiness act on macos-user](../reference/program-readiness.md#JR-D2) — **built 2026-10-06**: host-side admission starts the existing confined stage for an absent/unknown selected program and its generated bootstrap runs the shared readiness act; fixture tests are in `integration/macosuserprogramreadiness_test.go`, with native macOS execution pending; [a provider set's overlay modifier](../design/active-provider-sets.md#13-what-was-built-2026-09-29), which reads only the
    primary; [the empty-list line in `yolo check`](../design/model-lists-and-pickers.md#72-composition-rules);
    [macos-user's item 2 twin](runbooks/macos-user-manual-checks.md#2-seatbelt-is-actually-applied);
    [the color pass's Group B](cli-visual-polish.md); [the unit suite's clock waits](test-suite-speed.md#retaken-2026-10-01-and-which-targets-hold);
    [the Bedrock user-guide recipes](../design/bedrock-plumbing.md#12-what-i-would-build-in-order); [the disk levers'
    re-measure](../reference/disk-footprint.md#owed-measurements); and the pi guide's line on [a package the refresh missed](../reference/pi-extensions.md).
33. Verify [the exact-commit release gate](../reference/release-process.md) before the next publication request,
    because offline fixtures do not prove live Actions or publisher permissions. What the 2026-10-05 and 2026-10-06
    builds still owe: read the first scheduled macos-user nightly with [the vendor
    installs](../reference/agent-install-in-ci.md), and the linux/arm64 copilot cell for [GitHub's own installer](../design/program-delivery.md#OQ-NI1);
    rule the silent drops [the setup census](../design/backend-parity.md#42-what-was-built-2026-10-05-and-what-it-found)
    found; run a dotted `packages` entry on a `macos-user` launch, the one
    [UNMEASURED half of the attribute-path resolver](../reference/image-staging-vs-baking.md#what-a-packages-entry-names).
34. Rulings nothing shipped waits on. Gating one later step: [the host-file permission asymmetry](BACKLOG.md#E2) with
    [its host-side twin](pack-host-management-plan.md), [the skills fan-out](BACKLOG.md#OQ-S4), [sharing a claimed
    generic name](../design/credential-sources-separation.md#OQ-ES6), [the pack-file conventions](../design/pack-conventions.md),
    [loophole env and guest fields](../design/loophole-packaging.md), [workspace MCP files](../design/workspace-mcp-sources.md),
    [Seatbelt deny-default](../research/agent-safehouse.md), [host apply's posture](../design/host-render-target.md),
    [the I/O priority default](../design/io-priority.md), [replacing a pack-files directory yolo made](../design/pack-files-for-agent-state.md#OQ-FS3) with [OQ-HT5](../reference/macos-user-home-tiers.md#oq-ht5), [removing a layout link whose workspace is gone](../design/pack-files-for-agent-state.md#OQ-FS5). Only closing a document: [an unmatched-reference hatch](../design/reference-mismatch-diagnostics.md),
    [path mirroring](../reference/workspace-path-mirroring.md), [pack telemetry](../reference/pack-system.md#oq-acp4), [AWS's key
    pair](../design/credential-sources-separation.md#OQ-ES8), [stateful-surface comments](BACKLOG.md#OQ-E4), [the upstream mise
    issue](../design/jail-state-separation-design.md), and [macos-user's stale home-root file and two workspaces at
    once](../reference/macos-user-home-tiers.md).
35. The four calls the 2026-09-26 open-source audit left to the maintainer: whether one public research doc stays
    public, the scratch-directory `.gitignore` entries, a `last-release` recipe from the template, and when to retire
    the Python-version migration; the audit's report is outside this repository, so this is their only record.
36. Decide how far one login is shared — with the host, and between two agents of one provider
    ([universal-login-sharing.md](../design/universal-login-sharing.md)): the machine store already shares an agent's
    login across every jail, the host notch and the pi/Muse pair are the two gaps, and [its sketch](../design/universal-login-sharing-plan.md)
    waits on all three questions. Rule the canonical copy's location with it, so a `yolo` state reset cannot destroy a login.
    [Model auth for an isolated agent process](../design/isolated-agent-model-auth.md), once beside it, was declined
    2026-10-15 ([IA-D1](../design/isolated-agent-model-auth.md#IA-D1)); its six questions are moot.
37. Work the asks a downstream pack consumer is still waiting on ([downstream-asks.md](downstream-asks.md)) — each
    with the shape a landed version takes; three open ones are owned by [the patched-fork
    design](../reference/patched-forks.md) and [the model-list design](../design/model-lists-and-pickers.md), three
    by [the tracker's own questions](downstream-asks.md#open-questions) (one of them a small diagnostics build,
    two awaiting ratification of what was built 2026-10-15), and the rest are answered, declined or built.
    Beside it, the same consumer's 2026-10-22 ask, which it filed as parked: [a pack's own tool, built from its own
    tree](../design/pack-buildable-programs.md), with a dev override and a verified prebuilt download. Its three
    questions were ruled 2026-10-23, and every decision is built and tested on Linux. What is left is the Mac,
    the last task of [its plan](../design/pack-buildable-programs-plan.md): macos-user's run-as-you mode fails a
    pack-tree build at the capture bootstrap, and Apple Container and a Mac's `yolo host` have not run it yet.

53. [Make store cleanup automatic even after the last launch](../design/automatic-store-gc.md) — the owner wants no routine manual pruning; settle safe idle coverage before automating the current capture sweep.
    Its [companion seed](../design/automatic-store-gc-plan.md) parks surfaced implementation material; the [independent advice and audit](../research/automatic-store-gc-advisory-20261028.md) preserve the evidence behind the choice.

## External waits

38. One session at a Mac, with `sudo`'s password and the runner stopped, completes [the benchmark](../research/macos-backend-performance.md#8-results)
    — [auto-capture](../design/program-delivery.md#decision-ledger), macos-user's `go test`, Podman Machine on applehv
    and [NFS from macOS's `nfsd`](../research/macos-vm-runtime-comparison.md#6-is-there-an-open-stack-with-faster-shared-folders)
    — first, as [the comparison's refresh](../reference/macos-no-vm-direction.md#when-to-refresh-this) waits on it. It
    also runs `yolo host -- claude` and `yolo host -- agy` for [the host floor's installer agents](../reference/host-agent-environment.md#HP-D2),
    and a macos-user launch with a dotted `packages` entry, which no CI job runs. **macos-user's
    `go test` no longer waits on this item**: it ran 2026-10-17 without `sudo`'s password, and what
    that costs the suite is [item 5](#immediate-continuation). The benchmark, Podman Machine and
    NFS halves still want the password and the runner stopped.
39. [The two-jail check](../research/macos-backend-performance.md#7-found-on-the-way-two-apple-container-jails-may-mount-one-ext4-disk)
    has held since each workspace got its own `/mise` disk: run 37832291377 (2026-10-08, `container` 1.1.0)
    logged `AC-KEEPER sweep-spares-a-kept-jail (JL-D7) VERDICT: HOLDS`, and the sweep is strict since. The next
    `apple-container.yml` dispatch re-asks [the keeper's remaining Apple Container runs](../design/jail-lifetime-last-session-wins.md#7-what-i-would-build-in-order);
    someone at the runner Mac upgrades `container` to 1.5.0 (its kernel prompt is interactive) before the VM-local probe.
40. The next `macos-user.yml` run carries the Mac checks built since 2026-10-04: [I/O priority](../design/io-priority.md#9-what-i-would-build-in-order),
    the macos-user keeper, [the capture fixtures](../reference/install-capture.md), the host floor's fixture capture, the Mac floor's
    fork build and [the guest notch](environment-manager-plan.md); each doc's `next` names its test.
    **A developer Mac ran all of them 2026-10-17** ahead of that job
    ([the record](../design/macos-user-suite-on-a-developer-mac.md#7-measurements-this-run-produced)):
    the host floor's fixture capture, the Mac floor's fork build, the patched fork and patched
    extension cells and the sealed build profile's denials all pass, the nine `/Volumes`-less
    relocation measurements ran with volumes attached, all nine vendor-install cells pass on
    darwin (the `via: npm` half had never run there), and [Q3's cache-miss half](../design/macos-user-build-step-threat-model.md) is answered
    for the first time. CI has confirmed none of it, and twelve tests there are red for
    [reasons item 5 owns](#immediate-continuation).
41. A human with a subscription login runs [the credential-view runbook](runbooks/claude-credential-view-measures.md)
    for [Claude's login without interception](../design/claude-login-without-interception.md), on a Mac and for a day
    on a rootless host, which releases [`yolo host -- claude` joining the shared login](../reference/claude-oauth-interposition.md),
    and records one browser login and one shared expiry for [the OpenAI service](../reference/agent-credentials.md#openai-measured).
42. A real rootless Linux host, which a nested jail is not, clears [a real reboot](../reference/podman-readiness.md),
    [a low-space collection with a jail up](../reference/image-retention.md#owed-host-steps), [the keeper's scope move and logout](../design/jail-lifetime-last-session-wins.md#8-what-done-looks-like),
    and HD-R1's first launch once item 2 lands.
43. One live agent session per check, which no test may start: [the footer](../reference/agent-footer.md#what-has-and-has-not-been-observed),
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
44. A human with the cloud account clears [a Bedrock API key or static key pair](../design/bedrock-plumbing.md#64-the-credential-three-are-supported),
    [an SSO lapse mid-turn](../reference/agent-credentials.md#sso-chain-order), [settings-only Bedrock mode](../reference/providers.md#two-channels-split-by-payload-type),
    [an AgentCore gateway](../design/bedrock-web-search.md) and [a Claude usage-limit response](../design/wire-bridge-gateway.md#5-part-4--the-subscription-arm-and-opt-in-failover-ruled).
45. The maintainer's own commits and launches clear [the `matt` pack's `briefing/`](../reference/pack-system.md#briefing),
    [its posture list at the host](../reference/pack-system.md#posture-lists-owed),
    [a dropped pack's host output](../reference/pack-system.md#retiring-a-dropped-packs-host-output), [the fzf
    pack](../examples/claude-fzf-pack/README.md#adopting-it), his pack's move to pi's package folder, [the listen-port fix where reported](../reference/wire-bridge.md#what-can-hold-the-listen-port-before-the-bridge-does)
    and [the runner Mac's published-ports change](runbooks/mac-actions-runner.md).
46. A live `llama-server` and one manual turn per agent clear [the `llamacpp` provider](../../packs/llamacpp/README.md),
    whose spellings have never met a server; copilot is the one to try first.
47. The mise use record's first 30-day window gives [the disk footprint's re-measure](../reference/disk-footprint.md#owed-measurements)
    its first offer to check, on the host.
48. Nix dropping Intel Macs, when nixpkgs 26.05's security fixes stop on 2026-12-31, brings back [what replaces the
    Intel macOS runner](../research/macos-support-matrix.md#OQ-MX1), deferred until then.
49. The maintainer files the upstream reports drafted with the VM-local design, on [VZ's virtio-fs cache policy](../research/apple-container-file-cost.md#21-per-file-cost-not-bandwidth)
    and [VZ keeping freed guest memory](../research/macos-vm-runtime-comparison.md#42-adding-a-balloon-to-apple-container-ourselves)
    — last, since only Apple can ship either fix.
50. Native macOS verification of [JD-10's guest pack-tree lifetime](../reference/macos-user-provisioning.md#JD-10): observe root ownership and modes and Seatbelt execution while overlapping launches use distinct trees, A's daemon restarts from A's unchanged module after B changes or drops its module, and capture leaves both trees unchanged. The source gates passed, but no native execution has established these properties; they remain **UNMEASURED**.
51. **Superseded in part by item 52**: per-launch homes make two workspaces run at once in either mode; re-plan from the ruled model before building. Build the [concurrent-workspace plan](../design/macos-user-concurrent-workspaces-plan.md) for the [selected architecture](../design/macos-user-concurrent-workspaces.md) slice by slice: stable private homes must precede removal of today's account-home hold, and the HOME switch and the hold removal each wait on a native Mac run. Same-workspace [native session profiles](../design/macos-user-session-profiles.md) are independent code already in main (`29e298ca5`), with actual Mac enforcement/descendants still unmeasured; they do not establish multi-workspace concurrency. The [HT6 ruling](../reference/macos-user-home-tiers.md#decision-ledger) and concurrent design's [decision ledger](../design/macos-user-concurrent-workspaces.md#decision-ledger) govern the plan; its layout decisions and native proof are engineering, not a new scope or owner gate. [Claude refresh](../design/macos-user-claude-refresh.md) remains a separate proposal under its existing credential-view gate, not a prerequisite for workspace-home planning.
52. Prototype [macos-user's ruled model](../design/macos-user-model.md#7-the-ruled-model-two-modes-one-profile) (run as you by default under a deny-by-default profile started from Safehouse's, the second account an optional mode on the same profile, loopback denied by default; [OQ-MM1](../design/macos-user-model.md#OQ-MM1) to [OQ-MM5](../design/macos-user-model.md#OQ-MM5)) on a Mac in both modes, with a `~/ctx` link on every backend ([OQ-MM6](../design/macos-user-model.md#OQ-MM6)). The [configurable workspace root](../design/configurable-workspace-root.md)'s questions wait on it.
    The [implementation plan](../design/macos-user-model-plan.md) builds it in thirteen slices; the default flips only after a human reruns the prototype checks against the generated profile.
    Inside that profile [mise cannot reach HTTPS](../design/macos-native-tls.md): its macOS TLS needs the keychain service the profile denies. The nix-built rustls mise is built ([MT-D5](../design/macos-native-tls.md#decision-ledger)); the yolo boundary baseline is now measured against deployed `d159a57f0` (`0.15.1+705`), but the Safehouse synthetic-policy comparison and Copilot's current native request path remain unmeasured. Keep the deny; the [design](../design/macos-native-tls.md) owns the next step and blocks graduation until both are resolved.

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

## Disposition of open items (2026-10-13)

Written after the build-plan synthesis (its HEAD `4ba625fd0`) and the landing pass that followed it on this branch —
the patched-fork background timing (`ab95926c0`), the broker's first user-scope slice (`c177ab83e`), the startup
banner's dimming (`d8f26fc72`), the unit-suite seams (`1382499b0`), the pi-guide sentence (`a1c876717`) and the
native-addon measurement record (`2f7e7d5ff`). It gives every open item — the immediate-continuation list, the queue
(1–37) and [External waits](#external-waits) (38–50) — exactly one class, against the tree as it stands:

- **A — actionable now**, with the smallest next change named;
- **D — decision needed**, with the exact question and who answers it;
- **X — external/blocked**, with the resource named.

No item is moved, deleted or re-scoped, and [Boundaries](#boundaries) is a shelf, not a queue. Where an item mixes
classes, its sub-parts are named and the cross-cutting ones repeat in the two lists below.

### Immediate continuation

| Item | Class | Disposition |
|---|---|---|
| IC1 — profile-served daemons on attach | D | [OQ-AD1](../design/attach-daemon-activation.md#OQ-AD1): activate late-selected profile daemons additively, or refuse conflicting late bridge plans in this slice? **Maintainer** (synthesis leaning: refuse). Today's restart prompt is the ruled [OQ-SK1](../reference/attach-skew.md#why-its-this-way) disposition, not a defect. |
| IC2 — ongoing launch preparation and update-and-wait | A | Review the connected combined candidate and complete final runtime acceptance; reuse the admitted-build pipeline before optimizing isolated waits. |
| [IC3 — selected GPU graphics-driver tree](../design/gpu-graphics.md) | A / X | Build from the [completed plan](../design/gpu-graphics-plan.md); a real Linux GPU host still owes tree and Mesa-version verification, and the separate card-node ioctl remains unproven. |
| IC5 — the macos-user suite on a developer Mac | A | The four rulings are compacted into the [Decision Ledger](../design/macos-user-suite-on-a-developer-mac.md#decision-ledger). The original test changes and the doorway, housekeeping and floor-test repairs remain in main ancestry; portable root-sudo scanner fixes are in `cab34b522` and `201c4009e`. The earlier `4e7b9b92b` commit was documentation-only. Native-Mac reruns remain unverified. |

The section's other pointers are not items of their own: Pi's profile-set extension call is item 28, native readiness
and the credential store are items 11 and 20, and storage relocation and OrbStack are items 28 and 31.

### The queue

| Item | Class | Disposition |
|---|---|---|
| 1 — finish the patched-fork mode's verification | X / D | W9 #2's Linux nested-jail checks pass, and the build-line digest disclosure was already built and is now reverified through its production call-site tests ([evidence](../reference/patched-forks.md#7-the-build-and-the-launch)); no listed Linux-local build remains. Remaining: #3 is X (a real rootless host or CI), #8–10 are X (a Mac), the migration-kit measurement needs the maintainer's real series, and [OQ-FP11](../design/forked-programs-as-packs.md#OQ-FP11) is D; [OQ-PPX3](../reference/pi-extensions.md#OQ-PPX3) was ruled and built 2026-10-16. [OQ-PFK5](../reference/patched-forks.md#why-its-this-way) was ruled (defer macos-user delivery), [OQ-PFK6](../reference/patched-forks.md#why-its-this-way) ruled 2026-10-15 and built 2026-10-16, and [OQ-PFK7](../reference/patched-forks.md#why-its-this-way) settled 2026-10-15 by [PF-D85](../reference/patched-forks.md#PF-D85). |
| 2 — per-jail host daemons (HD-R1) | A | Coexistence answered 2026-10-16 ([HD-D26](../design/host-daemon-ownership.md#HD-D26)); [OQ-HD9](../design/host-daemon-ownership.md#11-decision-ledger), [OQ-HD5](../design/host-daemon-ownership.md#11-decision-ledger) and [OQ-HD4](../design/host-daemon-ownership.md#11-decision-ledger) built on `hd-r1-land`. **Maintainer** merges; the real-host and macos-user checks in item 42 and the design's `next` are X. |
| 3 — settled jail inheritance build, then other build rulings | A / D | Implement [the completed host-import-exclusions plan](../design/jail-inheritance-exclusions-plan.md) first: the design's owner rulings are compacted and the plan is ready to build. Then the maintainer's remaining rulings are [OQ-PD19](../design/program-delivery.md#oq-pd19)–[OQ-PD23](../design/program-delivery.md#decision-ledger) (project mise pin of pnpm; autoprune's `$GOBIN` class; a pack's agent-CLI pin; whether an agent CLI updates once per machine), [OQ-TP11](../design/trust-paths.md#OQ-TP11) and [OQ-BR24](../design/bedrock-plumbing.md#OQ-BR24). **Maintainer** for those rulings. |
| 4 — rulings that hold a defect's fix | D | The Kilo special-casing, [OQ-CDX3](../research/codex-background-service.md#OQ-CDX3) (`--no-daemon` for in-jail codex), whose OpenAI login host pi uses, and the leftover `mcp-adapter.json` ([OQ-MC1](../reference/mcp-configuration.md#OQ-MC1)/[OQ-MC2](../reference/mcp-configuration.md#OQ-MC2)). **Maintainer**. |
| 5 — `yolo host apply --assert`'s next command and the dry run's exit | D | May a successful `--assert` name a next command (happy-path rule 2), and does the dry run exit 0 or 1 ([OQ-RO8](../reference/report-tiers.md#OQ-RO8))? **Maintainer**. |
| 6 — remaining build-update slices | A | The candidate connects host delivery/reporting, Apple check-only and fork/capture timing. Combined review and final runtime acceptance remain; native/performance acceptance is X (a Mac). |
| 7 — Claude footer plugin and mods management | D | Move the footer into a Claude Code plugin, and how yolo manages Claude Code mods. **Maintainer**. |
| 8 — VM-local disks and the private Homebrew tap | D | [OQ-VL1](../design/vm-local-volumes.md#OQ-VL1)–[OQ-VL4](../design/vm-local-volumes.md#OQ-VL4) and [OQ-PT1](../design/private-tap-programs.md#OQ-PT1)–[OQ-PT5](../design/private-tap-programs.md#OQ-PT5). **Maintainer**. The Apple Container file-cost probe is X (a Mac). |
| 9 — macOS direction rewordings | D | Approve the drafted rewordings of the direction's ruled sentences. **Maintainer**. |
| 10 — what a launch's spans leave out | X | One Mac run for the Apple Container delivery test's Mac half; the rest is built. |
| 11 — record the Mac runs of 2026-10-02/03 | X | A Mac (and the named live-session checks). Reviewing the bounded privilege lifecycle with native readiness is D. |
| 12 — macos-user workspace root | D | [OQ-CW2](../design/configurable-workspace-root.md#OQ-CW2), the whitelist, before it replaces the blacklist. **Maintainer**. |
| 13 — workspace config feeding host dotenvs and mounts | D | [OQ-AS3](../research/agent-safehouse.md#OQ-AS3): do `mounts` and `env_sources` become user-scope-only? **Maintainer**. |
| 14 — which packs a workspace config may select | D / A | [OQ-WP1](../design/workspace-packs.md#OQ-WP1)–[OQ-WP5](../design/workspace-packs.md#OQ-WP5) in [the workspace-packs design](../design/workspace-packs.md), which [OQ-PK1](../reference/pack-system.md#oq-pk1) asked for on 2026-10-23. **Maintainer**. The `mcp_presets` retirement that ruling released is A. |
| 15 — the slot split's migration window | D | [OQ-D6](../design/slots-and-contributions.md#OQ-D6), then the manifest language and the slots' other calls. **Maintainer**. |
| 16 — provider rulings | D | Does the natively-implements rule reach a pack-declared list ([OQ-4](../design/pi-codex-provider-shadowing.md#OQ-4)), and what does `-p` name? **Maintainer**. |
| 17 — notch convergence's held items | D | The one `rmw` rule ([OQ-CO15](../reference/config-ownership.md#OQ-CO15)/[OQ-CO16](../reference/config-ownership.md#OQ-CO16)). **Maintainer**. `env_sources` waits on item 13; the shared-login credential view is X (a human session). |
| 18 — the provisioner's remaining calls | D | Corporate-CA trust first; it shipped on leanings under delegation (PS-D10, reversible). **Maintainer**. |
| 19 — what the environment manager promises at each notch | D | Q1b (`yolo check --at`) and Q7 (the Linux guest notch). **Maintainer**. |
| 20 — the keychain from a jail | D | Keychain scope, unlock and migration, which settles Copilot's machine-wide login. **Maintainer**. |
| 21 — the starting tab's Ctrl-C in a shared jail | D | [OQ-JL10](../design/jail-lifetime-last-session-wins.md#OQ-JL10); its build then waits on the sidecars and doorbell. **Maintainer**. |
| 22 — boot snapshot and diagnostic dial | D | [OQ-DB2](../design/diagnostics-past-the-boundary.md#oq-db2): is the boot snapshot always-on (leaning: on give-up paths only)? **Maintainer**. The [§4.6](../design/diagnostics-past-the-boundary.md#46-retention-and-size-decided) byte-cap adoption on the eight unbounded sinks is a separate A fragment. |
| 23 — add-only model lists and Bedrock web search | D | Rule the model lists and web search on Bedrock. **Maintainer**. |
| 24 — pack-declared traps vs the agent directory map | D | Does the map supersede the traps design? Rule before building either. **Maintainer**. |
| 25 — in-jail nix root watcher measurement | A | Running-jail watcher observation is recorded; host resolution remains open. Use a safe host-side observation: the attempted shared-store `nix-store --query --roots` pruned stale entries, so do not repeat without isolation. |
| 26 — H4, suite levers, the forked program, completion | D | Name the real forked program (source, build line and `produces`) for step 7, and rule whether integration sharding unparks. **Maintainer**. The H4 review of the root-owned capture store is X (a Mac); the native-addon measurement is recorded as blocked on a build line (`2f7e7d5ff`). |
| 27 — how a jail reaches a worktree's `.git` | D | Read-only refuses every commit, read-write lets a jail prune the worktree. **Maintainer**. |
| 28 — calls left at one decision each | D | The `:ro` degradation rows, message URLs ([OQ-DW3](../design/docs-website.md#OQ-DW3)), the macOS nix build sandbox, array-append pinning (E5), the pack system's other calls, relocating `.yolo` by a link ([OQ-JH1](../reference/jail-home.md#OQ-JH1)), a pi-subagents shared registry, the shared tool store's relocation contract, storage tiers, and cache isolation. **Maintainer**. |
| 29 — editor preference and SandVault/Safehouse compat | D | May yolo hold an editor preference, and how a SandVault/Safehouse user tries yolo ([OQ-NB1](../design/sandvault-safehouse-compat.md#OQ-NB1), then [OQ-NB2](../design/sandvault-safehouse-compat.md#OQ-NB2)/[OQ-NB3](../design/sandvault-safehouse-compat.md#OQ-NB3)/[OQ-NB5](../design/sandvault-safehouse-compat.md#OQ-NB5))? **Maintainer**. |
| 30 — agent access approvals, five steps | A / D | Step 1 next: factor the broker core out of the github-broker (no behavior change, waits on nothing), then the git source's reads and own-branch pushes ([first slice](../design/git-broker.md#9-the-first-slice)), whose transport was ruled 2026-10-23. Steps 2 and 3 (toast notifier, `yolo approve --json`/`--watch`, the answer path) turn writes on; their rulings are made ([OQ-BB17](../design/boundary-broker.md#OQ-BB17); a host with no notifier only logs a request, [OQ-BB10](../design/boundary-broker.md#OQ-BB10)). Notification delivery needs platform verification. Nothing awaits a ruling. |
| 31 — speed builds the maintainer asked for | A / X | Part B's config-declared, ambient-only and lean-attribute launch records are implemented and runtime-tested in an isolated Linux environment. Part A's tag dispatch and native runtime validation require a Mac. See [launch records](../reference/image-staging-vs-baking.md#packages-launch-records--first-slice-2026-10-13). |
| 32 — builds no ruling holds | D | The W1(e) `yolo run` progress golden sign-off (human approval of the frozen `testdata`). **Maintainer**. Sub-parts already closed: the startup banner (`d8f26fc72`), the unit-suite seams (`1382499b0`) and the pi-guide line (`a1c876717`); (b)/(c)/(g) are built with stale lines. The host-file 0644 regression is authored in `integration/macosuserread0644_test.go`; Linux checks its unsandboxed fixture/probe only. Still X: native Mac execution of the denial twin, the Mac readiness run and the quiet-host re-measure. |
| 33 — the exact-commit release gate | X | Live GitHub Actions and publisher permissions (offline fixtures cannot prove them), plus the first scheduled macos-user nightly on a Mac runner. |
| 34 — rulings nothing shipped waits on | D | The host-file permission asymmetry (E2), skills fan-out ([OQ-S4](./BACKLOG.md#OQ-S4)), shared claimed names ([OQ-ES6](../design/credential-sources-separation.md#OQ-ES6)), pack-file conventions, loophole env/guest fields, workspace MCP files, Seatbelt deny-default, host apply's posture, and the I/O priority default. **Maintainer**. |
| 35 — the four calls from the open-source audit | D | The audit report is outside this repository, so this is their only record. **Maintainer**. |
| 36 — how far one login is shared | D | Sharing with the host and between two agents of one provider, plus the canonical copy's location so a state reset cannot destroy a login. **Maintainer**. Model auth for an isolated agent process was declined 2026-10-15 ([IA-D1](../design/isolated-agent-model-auth.md#IA-D1)). |
| 37 — downstream pack consumer asks | D | The small diagnostics build is gated by [OQ-DA3](downstream-asks.md#OQ-DA3); [OQ-DA4](downstream-asks.md#OQ-DA4) awaits ratification of what was built ([OQ-DA2](downstream-asks.md#OQ-DA2) was ruled 2026-10-16); three carries are owned by items 1 and 23. |

### External waits

All of 38–50 are X; each names the resource that unblocks it.

| Item | Resource |
|---|---|
| 38 | A Mac, with `sudo`'s password and the runner stopped (also `yolo host -- claude`/`agy` and a dotted `packages` entry). |
| 39 | A Mac / the next `apple-container.yml` dispatch, with `container` 1.5.0 at the runner. |
| 40 | The next `macos-user.yml` run on a Mac runner. |
| 41 | A human with a subscription login — a Mac and a day on a rootless host. |
| 42 | A real rootless Linux host (a nested jail cannot see the class) or CI. |
| 43 | One live agent session per check (no test may start one). |
| 44 | A human with the cloud/AWS account. |
| 45 | The maintainer's own commits and launches. |
| 46 | A live `llama-server` and one manual turn per agent. |
| 47 | The mise use record's first 30-day window, on the host. |
| 48 | Time — nixpkgs 26.05's support ending 2026-12-31; not before. |
| 49 | The maintainer files the upstream reports; only Apple can ship either fix. |
| 50 | A Mac — native execution of JD-10's guest pack-tree lifetime. |

### Decisions needed (all answered by the maintainer)

The ruling set, in the order it unblocks work:

- [OQ-DB2](../design/diagnostics-past-the-boundary.md#oq-db2) — is the boot snapshot always-on? (item 22)
- [OQ-BB18](../design/boundary-broker.md#OQ-BB18) — does "a workspace can't widen" reach the GitHub `repos` list too? (item 30)
- [OQ-BB17](../design/boundary-broker.md#OQ-BB17) — may writes ship on the agent's own background wait? (item 30)
- [OQ-AD1](../design/attach-daemon-activation.md#OQ-AD1) — activate late profile daemons additively, or refuse conflicting late bridge plans? (IC1)
- The motivating fork's name — its source, build line and `produces` list (item 26)
- [OQ-FP11](../design/forked-programs-as-packs.md#OQ-FP11) — the open patched-fork ruling (item 1); [OQ-PFK5](../reference/patched-forks.md#why-its-this-way), [OQ-PFK6](../reference/patched-forks.md#why-its-this-way) and [OQ-PFK7](../reference/patched-forks.md#why-its-this-way) are ruled
- The W1(e) `yolo run` progress golden sign-off (item 32)

Plus the queue's remaining rulings: items 2, 3, 4, 5, 7, 8, 9, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 23, 24, 27, 28, 29, 34, 35 and 36.

### External resources

- **A Mac** — items 1, 8 (probe), 10, 11, 17 (credential view), 26 (H4), 31, 32 (readiness), 38, 39, 40, 41, 50.
- **A quiet host** — the W1(f)/W1(h) re-measures (items 32 and 8).
- **A real rootless host or CI** — items 1 (#3), 39, 40, 42.
- **A live human session / account** — items 41, 43, 44, 46.
- **The maintainer's own machine, commits or upstream filing** — items 45, 47, 48, 49.

### Actionable now, in land order

The remaining A work, cheapest first: item 22's byte-cap fragment → item 30's remaining broker integration →
item 6's host delivery/reporting → IC2's ongoing tree scheduling → item 1's nested-jail verification (after
item 6's landed half) → item 25's measurement **last and isolated**, as it mutates `/workspace`. Item 37's
small diagnostics build waits on the ruling in [OQ-DA3](downstream-asks.md#OQ-DA3).
