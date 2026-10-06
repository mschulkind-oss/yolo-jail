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

**Reconciled:** 2026-10-03

This page owns the order in which open work is taken up, and why. Each linked document owns its state, next step and
gates; read it before starting, because a place on this list is not permission to build.

## Ordering basis

**Verified live defects first**: behavior contradicting a ruling, a promise the CLI makes, or what a launch discloses,
each found in the code during this reconciliation, with fixes an agent can make now ahead of fixes awaiting a ruling.
**Then work that unblocks other work**: a ruling or step holding a later build, or a ruling with a dated deadline.
**Then smaller independent steps**: builds, tests, measurements, re-verification, graduation. **A ruling that only
closes a document ranks last**, since nothing waits on it. Work needing a Mac, a live human session, a real rootless
host or an outside account follows under [External waits](#external-waits).

## The queue

1. [Name a server the `requires_env` gate dropped for what it is](../reference/mcp-configuration.md#the-rules-the-one-loader-enforces)
   — first because the boot's drop notice calls it "not in config" and tells the user to declare a server they already
   declared; the fix is an agent's, in the notice that already tells a capability-withheld server apart.
2. [Make an AMD `gpu.mode: cdi` launch require an AMD CDI spec](setup-support-gaps.md) — G28: the launch's probe never
   looks for one, though `yolo check` does; a small podman/Linux fix that needs no ruling.
3. [Walk the durable dir only in the jail's own boot](../design/durable-scratch-space.md#54-the-durable-dir-report) — every
   attach walks it again and prints its line, against [DS-D11](../design/durable-scratch-space.md#DS-D11)'s "never at an
   attach", and the walk is already every boot pass's largest step (0.95 s on 2026-10-04 in the maintainer's own workspace).
4. Fix the two live defects ruled 2026-10-05, next among the fixes an agent can make now, the second proved only by an
   `apple-container.yml` dispatch: [let `YOLO_ALLOW_UNREACHABLE_SERVICES=1` reach a required daemon's refusal](../reference/loopback-tls-reachability.md#OQ-R8), which the
   documented hatch cannot, in a refusal that names the wire bridge and the next step; and [a `/mise` disk per workspace on Apple
   Container](../research/macos-backend-performance.md#OQ-MB1), since while one such jail runs a jail in another workspace cannot start,
   which also stopped the 2026-10-03 Apple Container run's keeper sweep measure.
5. [Rule whether a project's own mise pin of `pnpm` stays hidden](../design/program-delivery.md) — a declared `pnpm` gets
   its pnpm now; what [OQ-PD19](../design/program-delivery.md#OQ-PD19) still decides is whether a project's `mise.toml` pin
   is hidden when yolo supplies the pnpm. [Autoprune's `$GOBIN` class](../design/program-delivery.md#OQ-PD20), whose leaning
   retires it once a release ships past the 2026-09-25 deletion (0.11.0 did, on 2026-09-28), and [a pack's agent-CLI pin](../design/program-delivery.md#OQ-PD21) ride in the same sitting.
6. [Rule whether an agent CLI updates once per machine](../design/program-delivery.md#OQ-PD23) — each workspace downloads every
   release itself, and the hourly stamp all workspaces share can keep one on an old version while another keeps it fresh.
7. [Rule how a host daemon hands the launching terminal a line](../design/boundary-broker.md#OQ-BB10) — the broker's
   no-notifier notice waits on it, and so does saying an untested host `gh` version at launch.
8. [Rule whether a pack service's in-jail daemon is disclosed](../design/trust-paths.md#OQ-TP11) — the wire bridge runs
   inside every claude jail and no launch line names it, though disclosure is today's whole trust boundary.
9. [Rule whether copilot and oh-omp alone get `-p bedrock`](../design/bedrock-plumbing.md#OQ-BR24) — either agent by itself
   reaches nothing there unless another selected pack brings Bedrock in.
10. Rulings that hold a defect's fix: [the Kilo special-casing in two derives](../design/gateway-provider-packs.md), while claude gives every
   Kilo model without a declared window a 1M-token context; [`--no-daemon` for every in-jail `codex`](../research/codex-background-service.md#OQ-CDX3), since `codex agents`
   there still starts the daemon yolo turns off; [how host pi gets its OpenAI subscription credential](../design/pi-host-openai-auth.md), since
   `yolo host -- pi` on the `codex` profile lists no ChatGPT model; and [an old pi or a leftover `mcp-adapter.json` at the host](../reference/mcp-configuration.md#OQ-MC2),
   since host apply never deletes that file and pi-mcp-adapter keeps loading it (the `--revert` that removes the servers you added with
   `pi mcp add` runs only under `assert`, so its fix is the `host_management: "assert"` retirement below, not [OQ-MC1](../reference/mcp-configuration.md#OQ-MC1)).
11. [Build the patched-fork mode](../design/patched-forks.md), with [its companion for pi
    extensions](../design/patched-extensions.md), at every notch — the maintainer asked on 2026-10-04
    for both "out as soon as possible" so he can test them, and delegated their open questions, which
    were decided on their leanings. The one step of his first patch-series request still open is a
    tagged release carrying the patched mode and the launch's skip of what a yolo cannot read, so each
    of his hosts can be put on a known build: until a host leaves a build older than the mode,
    `patches` in `matt` or `pi-fork` refuses every launch there
    ([what an older yolo does](../design/pack-conventions.md#2-what-an-older-yolo-does-with-a-convention)).

    His first patched launch (2026-10-05) left four questions, each changing what his next launch
    prints or spends. Three were ruled that day and build with the patched mode: [a build line shown
    by its digest](../reference/report-tiers.md#why-its-this-way), once every build prints the line
    whole before it runs, since every launch prints his whole build line, about 600 characters;
    [no user `mise_tools` in a build or capture jail](../design/forked-programs-as-packs.md#FP-D19),
    since every build of his fork downloads his neovim nightly; and [a launch refused before it
    boots when a patched build it needs is missing](../design/patched-extensions.md#PPX-D40), since
    the host line saying pi will not start is being built now and the refusal lands at the same
    point. The fourth, [whether a launch restarts a host daemon older than
    itself](../design/host-daemon-ownership.md#OQ-HD11), was ruled the same day: no, and a fresh launch
    refuses instead, naming the restart; that build is queued with the day's other builds below.
12. [Build the background update mode for what yolo builds](../design/pi-extension-store-builds.md#XB-D42) — pack-declared
    pi extensions are host builds with one read-only copy per jail since 2026-10-05
    ([XB-D35](../design/pi-extension-store-builds.md#XB-D35)–[XB-D41](../design/pi-extension-store-builds.md#XB-D41)),
    with the parallel advance for extensions and the launcher's refresh fixes, and `agent_updates`' `"next-launch"`
    runs pi's pre-launch refresh in the background ([OQ-PD30](../design/program-delivery.md#decision-ledger)). What
    remains is the background advance of what yolo builds, whose launch half waits behind a seam that does not read
    that value yet, and the parallel advance for patched forks.
13. [Rule how the Claude footer moves into a Claude Code plugin](../research/claude-code-extensions-footer.md) — the
    plugin ends a process per refresh and keeps yolo's segment beside your own status line, and its first build
    step, disclosing a wrapped plugin's default-location hooks, is under way. [How yolo installs and manages
    Claude Code mods](../research/claude-code-mods-management.md) rides in the same sitting, since its rulings and
    its first build step rest on that plugin route and that disclosure fix.
14. [Rule whether a successful `yolo host apply --assert` names a next command](../reference/happy-path-principle.md#the-rules)
   — last among the defects, since [rule 2](../reference/happy-path-principle.md#the-rules) calls a success naming no next
   command a dead end, an `--assert` ends at its counts, and the line waits on a ruling against [the verdict
   block](../reference/report-tiers.md#the-verdict-block)'s "Only the dry run has a footer"; this entry is the work's only
   home. Rule [whether the dry run's exit follows OQ-RO5](../reference/report-tiers.md#OQ-RO8) in the same sitting: the code
   exits 1 where the ruling says 0, and the launch gate reads that exit.
15. [Design VM-local disks behind the folders only a jail uses](../research/apple-container-file-cost.md) on Apple Container and Podman
    Machine, with [the probe that fixes its shape](../research/apple-container-file-cost.md#5-a-planned-probe-on-the-mac-runner) — first of the
    2026-10-04 Mac speed work, as the largest speedup measured there waits on it (pip install 8.35 s on a shared folder, 2.01 s on
    Apple Container's own disk, without yolo); it builds on the per-side set yolo already keeps, its `~/.cache` half is still open now that
    [OQ-MB1](../research/macos-backend-performance.md#OQ-MB1) has put `/mise` on a disk per workspace (2026-10-05), and the upstream report drafts ride along, with [VZ's memory request](../research/macos-vm-memory-reclaim.md).
16. [Amend the harness's green-run precondition](../research/macos-backend-performance.md#appendix-a--the-harness) — ahead of the read-back, as the
    precondition it amends, which the two-jail test blocks, holds the Mac session (48). [The benchmark's remaining corrections](../research/macos-backend-performance.md#9-corrections-the-results-feed)
    and draft rewordings of [the direction's ruled sentences](../reference/macos-no-vm-direction.md#what-the-numbers-bear-on) for the maintainer ride with it,
    finishing [the comparison](../reference/macos-no-vm-direction.md#what-each-macos-path-costs-measured) he asked to see.
17. [Time what a launch's spans leave out](../reference/perf-logging.md#known-gaps): `macos-user` past its dispatch, the provisioning stage in the
    jail perf log, and the Apple Container delivery test's reader past `image.*` — without them [the comparison's refresh](../reference/macos-no-vm-direction.md#when-to-refresh-this)
    cannot split `macos-user`'s 5.5 s launch, nor the delivery test show the build skip's saving on Apple Container.
18. Read the Mac runs of 2026-10-02 and 2026-10-03 into the docs that wait on them — agent work now, and ahead of the
    rulings below because two of the reads unblock work: [the AWS doorway](../design/host-notch-services.md) and
    [its Bedrock design](../design/sso-backed-bedrock.md), [A2's twin](macos-revival-and-distribution-plan.md),
    [the render-mark twin](handoff-guest-notch-macos.md) and [its design](../design/notch-scoped-config-contributions.md#43-render-mark-parity-on-macos-user),
    [item 1's twin](handoff-macos-user-open-threads.md) and [its runbook row](runbooks/macos-user-manual-checks.md),
    [DP-B6's narrowed row](../design/declaration-parity.md), [the footer's Mac check](../design/agent-footer.md#what-i-would-build-in-order),
    [the I/O policy's verdict](../design/io-priority.md#9-what-i-would-build-in-order), which frees step 5,
    [the build sandbox's Q3 runs](../design/macos-user-build-step-threat-model.md), [the context-mount
    cases](../design/context-mounts.md#4-staging-and-the-tests-that-pin-each-piece), which free the ruling on
    context sources inside a home, [the Cachix lines](handoff-cachix-cache.md), [the in-VM
    copier](../research/macos-layer-reusing-image-delivery.md), which completed, [the login seed](../design/base-home-legacy-state.md),
    [the storage classes](../design/durable-scratch-space.md),
    which hold, and [the keeper measures](../design/jail-lifetime-last-session-wins.md#7-what-i-would-build-in-order), three of
    which do not hold on [the 2026-10-03 Apple Container run](https://github.com/mschulkind-oss/yolo-jail/actions/runs/37133569003).
    Each other doc's `next` names its run and verdict.
19. [Rule the macos-user workspace root](../design/configurable-workspace-root.md), [its whitelist](../design/configurable-workspace-root.md#OQ-CW2) first
    — the home check now folds case and knows the `/Users` firmlink, but a `/var/root` home passes until the whitelist replaces the
    blacklist, and tightening later withdraws what users may rely on.
20. [Decide whether a workspace config may feed host dotenvs and mounts into a jail](../research/agent-safehouse.md#OQ-AS3)
    — a committed config still can, and notch convergence's `env_sources` item waits on the answer.
21. [Rule which packs a workspace config may declare](../reference/pack-system.md#OQ-PK1), then [retire `mcp_presets`](../design/mcp-presets-removal.md)
    — that build rests on the boundary the ruling redraws, ends macos-user's blanket refusal of MCP presets, and
    decides whether the compose engine's [`workspace` layer](agent-settings-composition.md) gets a producer.
22. [Rule the slot split's migration window](../design/slots-and-contributions.md#OQ-D6), then [the manifest language](../design/manifest-language.md)
    with [the slots' other calls](../design/slots-and-contributions.md) — `exposes` and [the pi extension-tree
    rework](../design/pi-pack-extensions.md) wait on the first, and one sitting for the rest rewrites manifests once.
23. Provider work later decisions build on: [whether the natively-implements rule reaches a list a pack declares](../design/pi-codex-provider-shadowing.md#OQ-4),
    which holds two rows [the rule's broad reading](../design/pi-codex-provider-shadowing-plan.md) left in place when it was
    built on 2026-10-05; pi's Converse route through the wire bridge may no longer sit on `amazon-bedrock` under that
    build; and [what `-p` names](../design/providers-and-profiles-redesign.md), which the plain-words rewrite of the
    provider reference waits on.
24. [Decide whether a jail shell gets the credential grant](../design/credential-sources-separation.md) — a jail's
    `--with-credentials` and [its build sketch](../design/credential-sources-separation-plan.md) wait on it.
25. [Notch convergence's held items](notch-convergence.md) — after the workspace-config and jail-credential rulings above
    and the `assert` ruling next, which they wait on. Its env composition order and [whether every jail on a machine shares one
    Claude login](../reference/claude-oauth-interposition.md#OQ-CI1) were ruled 2026-10-05, so `yolo host -- claude` joining
    that login waits only on the credential view's measures, under [External waits](#external-waits).
26. [Settle what retiring `host_management: "assert"` does to configs on it](../design/config-ownership-and-promotion.md)
    — the retirement cannot start until then.
27. [Rule who keeps Claude's login fresh with no jail running](../design/host-daemon-ownership.md), then [plan per-jail host
    daemons](../design/host-daemon-ownership-plan.md) — three Mac runs answered what serializes their spawn (2026-10-05: a
    per-workspace guard on macos-user), so retiring the machine-wide credential daemons, the OpenAI legacy-state migration
    and the `yolo host -- codex|pi` spawns included, waits on whether it adds that refresh.
28. [Rule the provisioner override's grain](../design/provisioner-sets.md) — the user-scope preference that re-ranks the
    recipes packs ship waits on it, and macos-user's corporate-CA trust rides in the same design.
29. [Rule what the environment manager promises at each notch](../design/environment-manager-user-stories.md) — Q1b
    decides [`yolo check --at`](../design/yolo-as-environment-manager.md) and Q7 [the Linux guest notch](environment-manager-plan.md),
    whose two unstated Phase 7 choices an agent drafts as a proposal first.
30. [Rule the keychain from a jail](../design/keychain-from-a-jail.md), which settles [Copilot's machine-wide
    login](../research/copilot-token-storage.md) — until then every workspace asks for its own Copilot login.
31. [Rule what the keeper holds at `yolo host`](../design/jail-lifetime-last-session-wins.md), with [the sidecars and doorbell](../design/agent-event-watchers.md)
    — each design's host half waits on the other, and the keeper at `yolo host` and macos-user waits on both. [Whether an interrupt before
    ready spares a jail another session entered](../design/jail-lifetime-last-session-wins.md#OQ-JL10) goes in the same sitting.
32. Jail-boot rulings: [the boot snapshot and diagnostic dial](../design/diagnostics-past-the-boundary.md), since a refused boot
    keeps no record of the jail's state when it gave up, and the 2026-10-05 ruling that a launch which cannot install a declared
    program stops adds one more refusal of that kind.
33. [Rule the add-only model lists, then whether a list refuses without `only`](../design/model-lists-and-pickers.md), and
    [web search on Bedrock](../design/bedrock-web-search.md) — what most agents' menus show turns on the first two, and the 2026-10-01
    reading found no agent gets search from Bedrock on runtime, so the search questions now decide whether yolo supplies one.
34. [Decide whether pack-declared traps fold into the agent directory map](../design/agent-directory-map.md) — the map
    would supersede [the traps design](../design/pack-declared-file-diagnostics.md), so rule it before building either.
35. [Rule what triggers an in-jail build's GC root, and whether `gcroots/auto` may be bound in](../design/in-jail-nix-roots.md) — yolo's
    own in-jail roots are registered under the host's spelling now, and a root a user or an agent makes is still dead on arrival.
36. [Approve the host capability gate](../design/declaration-parity.md) (DP-B46: `yolo host --` skips the capability gate a jail
    launch applies) — a nod, not a ruling, and the last item of that census.
37. [Rule how macos-user reaches the capture store](install-capture.md) and [whether integration sharding unparks](integration-parallelism.md)
    with [the test suite's two other levers](test-suite-speed.md), and [name the real forked program](../design/forked-programs-as-packs.md)
    — the host notch's floor arms are built on a stand-in fork, and the real one is the last input step 7 needs.
38. [Rule how a jail reaches a worktree's `.git` outside it](../research/herdr-integration.md#OQ-HR3), with [a jail pane after a herdr
    restart](../research/herdr-integration.md#OQ-HR2) and [a jailed agent driving herdr](../research/herdr-integration.md#OQ-HR4) — the
    2026-10-01 measurement put the herdr questions on facts: a read-only bind refuses every commit, a read-write one lets a jail prune the worktree.
39. Calls the 2026-10-01 groundwork left at one decision each:
    [who installs a pi package the refresh missed](../design/pi-extension-lifecycle.md), [the `:ro` degradation rows](../design/composed-file-permissions.md),
    [whether messages name the guide's URL](../design/docs-website.md#OQ-DW3), [the macOS nix build sandbox](../design/macos-user-build-step-threat-model.md),
    [whether array-append pinning closes](BACKLOG.md#E5), [the pack system's other calls](../reference/pack-system.md),
    [relocating `.yolo` by a link](../reference/jail-home.md#OQ-JH1) and, once the Mac runs' read-back records its cases,
    [context sources inside a home](../design/context-mounts.md) — each has its facts and a leaning, so each is a short sitting.
40. [Rule whether yolo may hold an editor preference](../design/baked-editor-preference.md) — the maintainer asked
    for it, and its first ruling decides the rest and an image change every jail pays.
41. [Build the rest of the broker's step 2](../design/boundary-broker.md#11-recommendation-and-the-first-build-slice) — first
    among the builds because the maintainer put the broker on the plate for the week of 2026-09-28, and only its
    no-notifier notice and its ping wait on a ruling or another design. Beside it,
    [move the widening entry into the workspace config](../design/workspace-widening.md), ruled 2026-10-05: an agent
    asks for a repository and the human approves it at the next launch, built that day from [its plan](../design/workspace-widening-plan.md). Next, the maintainer's ruling on its [OQ-WW1](../design/workspace-widening.md#OQ-WW1), whether a remote's rename asks, which the build follows the leaning of.
42. Speed builds the maintainer asked for on 2026-10-04, next among the builds since neither holds other work:
    [Apple Container's stock-image skip, then the same skip for a launch declaring `packages:`](../reference/image-staging-vs-baking.md), as the nix
    build that finds nothing to do is 3.2 to 3.5 s of Apple Container's 6.9 s fresh launch and a median 1.7 s per launch of this repository,
    which declares `packages:` (33 builds in its host perf log, 2026-09-14 to 2026-10-04, not in the repository), and the second also covers
    Apple Container launches that declare `packages:`; and [Apple-silicon Podman Machines pinned to applehv](../research/macos-vm-runtime-comparison.md#32-on-a-shared-mac-folder),
    as Podman's own installer gives a new machine libkrun, the slowest shared folder measured.
43. Builds and small steps no ruling holds, after the broker and the speed builds since none holds other work: [a provider set's two gaps](../design/active-provider-sets.md#13-what-was-built-2026-09-29), where
    the overlay modifier and the host remedy line read only the set's primary; [the empty-list line in `yolo check`](../design/model-lists-and-pickers.md#72-composition-rules);
    [the `boot.log` half of TP10's disclosure](../design/trust-paths.md#outstanding-work); [macos-user's item 2 twin](handoff-macos-user-open-threads.md);
    [Group B of the CLI's color pass](cli-visual-polish.md); [the unit suite's clock waits](test-suite-speed.md#unit-tests-one-package-sets-the-wall-time-and-five-of-its-tests-are-waiting-on-clocks);
    [the Bedrock user-guide recipes](../design/bedrock-plumbing.md#12-what-i-would-build-in-order); [the disk levers' re-measure](../design/disk-levers-and-backfill.md),
    its jail half now; [a shared-dir hook whose `at` names no machine `state` of its pack](../design/pack-conventions.md#7-considered-and-not-proposed),
    which `yolo pack lint` and `yolo check` pass and the boot then refuses, starting with the failing lint test; and graduating
    [the extension model defaults](../research/extension-model-defaults.md) into the provider reference and
    [`packages` attribute paths](../design/package-nested-attribute-paths.md), built 2026-10-06, into a system doc.
44. The builds the 2026-10-05 CI and testing rulings released, beside those, since none holds other work either:
    [real vendor installs on the macos-user nightly](../reference/agent-install-in-ci.md#OQ-CI7), one hard-failing job per pack
    with the npm packs first, since no CI job installs a vendor's Mac build before a user does; [copilot from GitHub's own
    installer](native-installer-migration.md), its own updater left on; and [the per-setup census](../design/backend-parity.md),
    whose test fails when a config key or pack kind has no answer for one of the four setups. Five more of that day's rulings
    released builds placed here for the same reason: [the Bedrock list yolo fetches](../design/model-lists-and-pickers.md#OQ-MM6)
    where no pack supplies one, with claude in its own Bedrock mode behind the bridge, to land no later than [the shipped
    Bedrock list's removal](../design/model-lists-and-pickers.md#MM-D32), since a launch with neither leaves claude on
    `-p bedrock-bridge` without prompt caching and starting on an id Bedrock refuses; [pi's package
    folder](../design/pack-pi-resources.md), one entry per content pack in place of a list of files, registering
    addressed trees only, since whether a patched extension there may drop its list entry is
    [OQ-PC3](../design/pack-conventions.md#OQ-PC3), open and not gating the patch-series work; [the jail's readiness
    act](../design/jail-notch-readiness.md#OQ-JR1), which installs every program a selected pack declares before your command
    runs and stops a launch that cannot, offline included, since `yolo -- true` still leaves every agent CLI uninstalled;
    [the refusal of a host-wide daemon older than the launching yolo](../design/host-daemon-ownership.md#OQ-HD11), since an
    upgraded launch still proceeds without the check that warns what would fail its agents' requests; and [nested attribute paths in
    `packages`](../design/package-nested-attribute-paths.md#OQ-1), each resolved as `nix build` resolves it, since
    `rocmPackages.clr` still fails the image build.
45. [Make the Chrome DevTools MCP server work at `yolo host`](../design/mcp-presets-removal.md#5-the-chrome-devtools-inventory--what-the-pack-carries-what-the-image-keeps)
    — the maintainer wants it, after the week's work (2026-09-30); host apply expands no MCP preset
    ([HC-D16](../design/host-computed-layer.md#HC-D16)), and the host agent floor now gives yolo a prefix to put one in.
    Build one path for every host, per the [fill-the-matrix principle](../reference/fill-the-matrix-principle.md).
46. Rulings nothing shipped waits on. Gating one later step: [the host-file permission asymmetry](BACKLOG.md#E2) with [its host-side
    twin](pack-host-management-plan.md) in one sitting, [the jail's skills fan-out](BACKLOG.md#OQ-S4), [pack binary pins](../design/broker-as-a-pack.md),
    [loophole env and guest fields](../design/loophole-packaging.md), [workspace MCP files](../design/workspace-mcp-sources.md),
    [Seatbelt deny-default](../research/agent-safehouse.md), [host apply's posture](../design/host-render-target.md), [the I/O priority default](../design/io-priority.md).
    Only closing a document: [an unmatched-reference hatch](../design/reference-mismatch-diagnostics.md), [path mirroring](../design/workspace-path-mirroring.md),
    [pack telemetry](agent-config-packs.md), [AWS's key pair](../design/agent-auth-modes.md#OQ-9), [stateful-surface comments](BACKLOG.md#OQ-E4),
    [the upstream mise issue](../design/jail-state-separation-design.md).
47. The four calls the 2026-09-26 open-source audit left to the maintainer: whether one public research doc stays
    public, the scratch-directory `.gitignore` entries, a `last-release` recipe from the template, and when to retire
    the Python-version migration; the audit's report is outside this repository, so this is their only record.

## External waits

48. One session at a Mac, with `sudo`'s password and the runner stopped, completes [the benchmark](../research/macos-backend-performance.md#8-results):
    [auto-capture](../design/program-delivery.md#decision-ledger) on current main, once about 90% of an Apple Container launch, macos-user's
    `go test`, new binaries' first run with and without the developer-tool setting, Podman Machine through yolo on the Mac's applehv machine,
    and [NFS from macOS's own `nfsd`](../research/macos-vm-runtime-comparison.md#6-is-there-an-open-stack-with-faster-shared-folders), the one open shared-folder
    candidate left, since libkrun with complete permission semantics and QEMU's virtiofsd port did not beat VZ's share — first among the waits,
    as [the comparison's refresh](../reference/macos-no-vm-direction.md#when-to-refresh-this) waits on it; it measures the build skip and spans if landed, waiting for neither.
49. The next `apple-container.yml` dispatch reruns [the two-jail check](../research/macos-backend-performance.md#7-found-on-the-way-two-apple-container-jails-may-mount-one-ext4-disk),
    which every parity run on 1.1.0 since 2026-10-03 has failed and which must now pass, as each workspace has had a `/mise`
    disk of its own since [OQ-MB1](../research/macos-backend-performance.md#OQ-MB1)'s build (2026-10-05); someone at the runner Mac
    upgrades `container` from 1.1.0 to 1.5.0 (its kernel prompt is interactive), and the VM-local probe needs the maintainer's push
    and one dispatch, after the upgrade for answers that hold on 1.5.0.
50. A human with a subscription login clears [Claude's login without interception](../design/claude-login-without-interception.md)
    by running [its runbook](runbooks/claude-credential-view-measures.md), on a Mac and for a day on a rootless host,
    which also releases [`yolo host -- claude` joining the shared login](../reference/claude-oauth-interposition.md#OQ-CI1),
    and [the OpenAI service](../design/openai-auth-broker-plan.md) by recording one browser login and one shared expiry.
51. One session at a Mac clears [the host capture for installer agents](../design/host-tool-provisioning.md), which runs
    each vendor installer under Seatbelt; the hosted macos-user job's own vendor installs, ruled 2026-10-05
    ([OQ-CI7](../reference/agent-install-in-ci.md#OQ-CI7)), are queued above as a build.
52. A real rootless Linux host, which a nested jail is not, clears [a real reboot](../design/podman-reboot-readiness.md#testing-and-the-real-host-check),
    [a low-space collection with a jail up](storage-lifecycle.md) and [the keeper's scope move and logout](../design/jail-lifetime-last-session-wins.md#8-what-done-looks-like).
53. One live agent session per check, which no test may start, clears [the footer](../design/agent-footer.md#21-as-built),
    [Claude's LSP plugin](../reference/mcp-configuration.md#lsp-claudes-route-is-a-generated-plugin), [the via route](../design/wire-bridge-gateway.md#41-how-it-is-built),
    [menus under a model list](../design/model-lists-and-pickers.md#13-build-order-and-what-done-looks-like), [a switch inside a provider set](../design/active-provider-sets.md#9-what-done-looks-like),
    [Copilot's config migration](../design/agent-directory-map.md#74-copilot), [a Kilo session](../design/gateway-provider-packs.md), [the restart prompt at a
    real terminal](../design/attach-skew-and-contract-guardrails.md) and [a codex, opencode and pi turn on Bedrock](../design/bedrock-plumbing.md#8-behaviour-this-design-fixes) under `aws-auth`'s SSO.
54. A human with the cloud account clears [a request under a Bedrock API key or a static key pair](../design/bedrock-plumbing.md#64-the-credential-three-are-supported),
    [an SSO lapse mid-turn](../design/sso-backed-bedrock.md#11-evidence-and-how-to-re-check-it), [settings-only Bedrock mode](../reference/providers.md#two-channels-split-by-payload-type),
    [an AgentCore gateway](../design/bedrock-web-search.md) and [a Claude usage-limit response](../design/wire-bridge-gateway.md#5-part-4--the-subscription-arm-and-opt-in-failover-ruled).
55. The maintainer's own commits and launches clear [the `matt` pack's `briefing/`](../reference/pack-system.md#briefing),
    [its posture list at the host](../design/notch-scoped-config-contributions.md#5-fastest-path-to-the-motivating-case),
    [a dropped pack's host output](../reference/pack-system.md#retiring-a-dropped-packs-host-output), [the fzf pack's adoption](handoff-fzf-pack-adoption.md),
    [the listen-port fix where reported](../reference/wire-bridge.md#what-can-hold-the-listen-port-before-the-bridge-does) and
    [the runner Mac's change that let published ports hold](runbooks/mac-actions-runner.md), for its runbook.
56. A live `llama-server` and one manual agent turn per row clear [the `llamacpp` provider](../../packs/llamacpp/README.md),
    whose per-agent spellings are read from each agent's code and have never met a server; copilot is the one to try first.
57. Nix's support for Intel Macs ending, when nixpkgs 26.05's security fixes stop on 2026-12-31, brings back [what replaces
    the Intel macOS runner](../research/macos-support-matrix.md#OQ-MX1): the maintainer deferred the choice until then on
    2026-10-05, and the podman macOS suite stays on `macos-26-intel` meanwhile; ahead of the upstream reports, as it has a date.
58. The maintainer files the upstream reports drafted with the VM-local design, on [VZ's virtio-fs having no cache policy](../research/apple-container-file-cost.md#21-per-file-cost-not-bandwidth)
    and on [VZ keeping freed guest memory](../research/macos-vm-memory-reclaim.md#5-adding-it-to-apple-container-ourselves)
    — last, since only Apple can ship either fix and nothing in yolo waits on one.

## Boundaries

- **Not committed to:** [`further-roadmap-ideas.md`](further-roadmap-ideas.md), a shelf of unverified candidates.
  **Iced**, with what would thaw each: [`yolo cache relocate`](cache-relocation.md) (someone wanting it),
  [trust-gated grants in a local config](../design/workspace-config-trust.md) (a workspace-specific read-write mount that
  cannot move into the workspace), [in-jail nix on the container Macs](setup-support-gaps.md#2-ranked-gap-backlog) (a user
  for whom `packages:`, mise and macos-user all fall short) and [a central host watcher](../research/central-yolo-watcher.md#7-what-would-have-to-be-true-before-building-one)
  (a duty that must hold a listener across launches, or a ruled credential timer).
- **Ids Vantage cannot index**, reached here by name: [Q1 to Q9](../design/environment-manager-user-stories.md#Q1), [9.2 and 9.6](../design/host-render-target.md#9.2),
  [CFP-1 to CFP-3](../design/composed-file-permissions.md#CFP-1), [Q2 and Q3](../design/macos-user-build-step-threat-model.md#Q2),
  [SS-6](../design/jail-state-separation-design.md#ss-6), [E1, E2, E5 and CO](BACKLOG.md#E1),
  [the host-files mode](pack-host-management-plan.md#open-questions), [H4](install-capture.md#hand-offs--what-is-not-wired-and-the-exact-line-that-wires-it),
  and two outside this queue: [SH-1](macos-revival-and-distribution-plan.md#oq-sh-1--is-macos-user-self-hosting-worth-pursuing-at-all-maintainer),
  which blocks nothing, and [JD-4](../design/jail-daemon-on-macos-user-plan.md#decision-ledger), a maintainer follow-up.
- **With no id at all:** [Go comments citing sections by number](README.md#the-dangling-n-citations-in-go-comments),
  [check 2's two marker conventions](README.md#check-2-has-two-known-errors-and-a-convention-question-under-each),
  [the storage lifecycle's `min-free` values](storage-lifecycle.md#open-questions), the unruled half of
  [OQ-ST2](../design/synced-skill-trees.md#OQ-ST2) on user-reserved skill children, and whether a bare `-p` whose packs
  install no CLI should say it selects nothing (`checkProfileTargets`, `internal/cli/run/packs.go`).
