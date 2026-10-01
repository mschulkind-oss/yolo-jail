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

**Reconciled:** 2026-09-30

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

1. [Rule whether a pack service's in-jail daemon is disclosed](../design/trust-paths.md#OQ-TP11) — the wire
   bridge runs inside every claude jail and no launch line names it, though disclosure is today's whole
   trust boundary.
2. [Rule whether copilot and oh-omp alone get `-p bedrock`](../design/bedrock-plumbing.md#OQ-BR24) — either
   agent by itself reaches nothing there unless another selected pack brings Bedrock in.
3. Rulings that hold a defect's fix: [the Kilo special-casing in two derives](../design/gateway-provider-packs.md), while
    claude gives every Kilo model without a declared window a 1M-token context; [whether a hatch reaches a required daemon's
    refusal](../reference/loopback-tls-reachability.md#OQ-R8), which the documented hatch cannot reach today (no leaning yet,
    so an agent drafts one first); [`--no-daemon` for every in-jail `codex`](../research/codex-background-service.md#OQ-CDX3),
    since `codex agents` there still starts the daemon yolo turns off, stale model list and all; and [how host pi gets its
    OpenAI subscription credential](../design/pi-host-openai-auth.md), since `yolo host -- pi` on the `codex` profile starts
    on another model and lists no ChatGPT model.
4. [Rule whether the pi pack may rewrite its own `packages` list](../design/pi-git-extension-caching.md) — the
    per-commit extension store that ends every jail sharing one npm prefix is rebased and green, and lands on this ruling.
5. [Rule the macos-user workspace root](../design/configurable-workspace-root.md), [its whitelist](../design/configurable-workspace-root.md#OQ-CW2) first — the home check
    now folds case and knows the `/Users` firmlink, but a `/var/root` home still passes until the whitelist replaces
    the blacklist, and tightening later withdraws what users may rely on.
6. [Decide whether a workspace config may feed host dotenvs and mounts into a jail](../research/agent-safehouse.md#OQ-AS3)
    — a committed config still can, and notch convergence's `env_sources` item waits on the answer.
7. [Rule which packs a workspace config may declare](../reference/pack-system.md#OQ-PK1), then [retire `mcp_presets`](../design/mcp-presets-removal.md)
    — that build rests on the boundary the ruling redraws, and ends macos-user's blanket refusal of MCP presets; the
    question has no options or leaning yet, so an agent drafts those first.
8. [Rule the slot split's migration window](../design/slots-and-contributions.md#OQ-D6), then [the manifest language](../design/manifest-language.md)
    with [the slots' other calls](../design/slots-and-contributions.md) — `exposes` and [the pi extension-tree
    rework](../design/pi-pack-extensions.md) wait on the first, and one sitting for the rest rewrites manifests once.
9. Provider rulings later decisions build on: [how far the natively-implements rule reaches](../design/pi-codex-provider-shadowing.md),
    which holds pi's Converse route through the wire bridge, and [what `-p` names](../design/providers-and-profiles-redesign.md),
    which the plain-words rewrite of the provider reference waits on.
10. [Decide whether a jail shell gets the credential grant](../design/credential-sources-separation.md) — a jail's
    `--with-credentials` and [its build sketch](../design/credential-sources-separation-plan.md) wait on it.
11. [Rule one env composition order](notch-convergence.md), and [confirm whether host claude may join the jails'
    login](../reference/claude-oauth-interposition.md#OQ-CI1) — one variable's value depends on how the agent starts,
    and an existing ruling may answer the login, so an agent drafts that leaning first; the plan's other held items
    wait on the workspace-config and jail-credential rulings above and the `assert` ruling next.
12. [Settle what retiring `host_management: "assert"` does to configs on it](../design/config-ownership-and-promotion.md)
    — the retirement cannot start until then, and an agent can draft the missing leaning first.
13. [Rule what serializes a daemon's spawn once the host singleton goes](../design/host-daemon-ownership.md) — retiring
    the machine-wide credential daemons waits on it; prune [its plan](../design/host-daemon-ownership-plan.md) first.
14. [Rule the provisioner override's grain](../design/provisioner-sets.md) — the user-scope preference that re-ranks the
    recipes packs ship waits on it, and macos-user's corporate-CA trust rides in the same design.
15. [Rule what the environment manager promises at each notch](../design/environment-manager-user-stories.md) — Q1b
    decides [`yolo check --at`](../design/yolo-as-environment-manager.md) and Q7 [the Linux guest notch](environment-manager-plan.md).
16. [Choose what replaces the Intel macOS runner](../research/macos-support-matrix.md) — its nixpkgs security window
    closes at the end of 2026, leaving the podman macOS suite unpatched or without a hosted runner.
17. [Rule the keychain from a jail](../design/keychain-from-a-jail.md), which settles [Copilot's machine-wide
    login](../research/copilot-token-storage.md) — until then every workspace asks for its own Copilot login.
18. [Build the keeper's record of a service that dies](../design/jail-lifetime-last-session-wins.md), and rule it with
    [the sidecars and doorbell](../design/agent-event-watchers.md) — later sessions are not told a host service died,
    and no watcher reaches any agent but Claude. Rule both together: each design's host half waits on the other.
19. [Build the boundary broker's widening entry](../design/boundary-broker.md) — a workspace that needs a second
    repository has no way to reach it; the write path's terminal notice also needs the doorbell.
20. Jail-boot rulings: [what a failed agent install does to a launch](../design/jail-notch-readiness.md), since the
    provisioning command leaves every declared agent CLI uninstalled until first use, and [the boot snapshot and
    diagnostic dial](../design/diagnostics-past-the-boundary.md), since a refused boot keeps no record of the jail's state
    when it gave up, such as which process held a port.
21. [Build the model-list check line, then rule the add-only menus](../design/model-lists-and-pickers.md), and measure
    [web search on Bedrock](../design/bedrock-web-search.md) and [Responses at codex's route](../design/wire-bridge-gateway.md)
    — they decide what most agents' menus show, whether Bedrock can search, and whether codex's Bedrock route is real.
22. [Decide whether pack-declared traps fold into the agent directory map](../design/agent-directory-map.md) — the map
    would supersede [the traps design](../design/pack-declared-file-diagnostics.md), so rule it before building either.
23. [Deliver context directories on macos-user](../design/context-mounts.md) — every context mount there refuses, and
    the delivery and its sandbox probes need no human at a Mac.
24. [Register an in-jail build's GC roots under the host's spelling](../design/in-jail-nix-roots.md) — every root a
    podman jail asks for is dead on arrival; the jail's briefing already says so, which keeps it out of the defects above,
    and yolo's own roots need no ruling, so they can be fixed before the rest.
25. Small builds that need no ruling, each one self-contained step: [config keys in the census](../design/declaration-parity.md),
    [per-agent model roles](../research/extension-model-defaults.md), [an oh-omp provider set](../design/active-provider-sets.md),
    [pi's codex exclusion in a real launch](../design/pi-codex-provider-shadowing-plan.md),
    [a prune at every launcher run](../design/disk-levers-and-backfill.md) and [CLI color](cli-visual-polish.md).
26. Two small fixes whose only home is this entry, ranked here because neither breaks anything yet: cap
    [the host-service and socat logs](../research/central-yolo-watcher.md#26-done-by-nobody), which grow without bound
    (at their `O_APPEND` opens in `internal/cli/run/loopholesruntime.go` and `network.go`, bounded as
    `internal/crossaudit` bounds `crossings.log`), and make [the jail boot's drop notice](../design/diagnostics-past-the-boundary.md#31-one-of-the-six-is-not-this-designs-problem)
    name each table's own remedy (one `Fprintf` in `noteDroppedManagedEntries`, `internal/entrypoint/prism.go`); today
    only `claude/config`'s `mcpServers` reaches that notice, and `mcp_servers` is the right remedy for it.
27. Mac CI checks an agent can write now for a scheduled or dispatched Mac job: [the sandbox's user and
    directory](handoff-macos-user-open-threads.md), [the no-darwin-build abort](macos-revival-and-distribution-plan.md),
    [Cachix substitution](handoff-cachix-cache.md), [render-mark parity in the sandbox](handoff-guest-notch-macos.md),
    [I/O priority](../design/io-priority-plan.md), [a sandboxed source build](../design/macos-user-build-step-threat-model.md),
    [declaration parity's macos-user rows](../design/declaration-parity.md#11-what-i-would-build-in-order), and Apple
    Container's [login seed](../design/base-home-legacy-state.md), [storage-classes briefing](../design/durable-scratch-space.md)
    and [keeper measures](../design/jail-lifetime-last-session-wins.md#7-what-i-would-build-in-order).
28. First real runs and measurements an agent can make now, each deciding whether a next build is worth doing:
    [the landing gate](test-suite-speed.md), [a Linux claude capture for the macos-user hand-off](install-capture.md),
    [the stalled in-VM copier](../research/macos-layer-reusing-image-delivery.md), [a stand-in forked program at the host](../design/forked-programs-as-packs.md),
    [sharded integration runs](integration-parallelism.md), [posture lists in a jail](../design/notch-scoped-config-contributions.md),
    [cache relocation under podman](cache-relocation.md#test-plan), [an attach to an older yolo's jail](../design/attach-skew-and-contract-guardrails.md)
    and [capability-driven MCP delivery](../reference/mcp-configuration.md#what-the-derive-boundary-removes).
29. References describing code that moved since their last check, some of whose cells were already false:
    [agent credentials](../reference/agent-credentials.md), [providers](../reference/providers.md),
    [the wire bridge](../reference/wire-bridge.md), [image delivery](../reference/image-staging-vs-baking.md),
    [the jail home](../reference/jail-home.md) and [the host-duty inventory](../research/central-yolo-watcher.md).
30. Finished designs to graduate, since a reader landing on one reads a proposal for code that exists:
    [OpenAI service](../design/openai-auth-broker.md), [credential scope](../design/provider-credential-scope.md),
    [host computed layer](../design/host-computed-layer.md) ([plan](../design/host-computed-layer-plan.md)),
    [workspace skills](../design/workspace-skills.md), [synced skill trees](../design/synced-skill-trees.md),
    [host launch PATH](../design/host-launch-environment.md), [program runtimes](../design/agent-program-runtimes.md),
    [macos-user jail daemon](../design/jail-daemon-on-macos-user-plan.md), [keeper hand-off](../design/jail-lifetime-last-session-wins-plan.md),
    [findings index](proposed-fixes-open-findings.md), [docs website](../design/docs-website.md) ([plan](../design/docs-website-plan.md)).
31. Agent work that leaves a ruling one decision — facts: [a subject for array-append pinning](BACKLOG.md#E5), [pnpm outside mise](../design/program-delivery.md),
    [mise's prune record](../design/minimal-disk-footprint.md), [colliding attribute paths](../design/package-nested-attribute-paths.md),
    [a read-only `.git` bind](../research/herdr-integration.md), [per-version npm trees](../design/pi-extension-lifecycle.md),
    [pi's package loader](../design/pack-pi-resources.md); drafts: [copilot's updater question](native-installer-migration.md),
    leanings for [the pack system's other calls](../reference/pack-system.md), options for
    [vendor installs in CI](../reference/agent-install-in-ci.md), rows for [the `:ro` degradation table](../design/composed-file-permissions.md).
32. [Rule whether yolo may hold an editor preference](../design/baked-editor-preference.md) — the maintainer asked
    for it, and its first ruling decides the rest and an image change every jail pays.
33. [Find whether the config engine's workspace layer is still wanted](agent-settings-composition.md) — one settled
    piece was never built and no document owns it; retire it, or file it where the engine is described.
34. Rulings nothing shipped waits on. Gating one later step: [the host-file permission asymmetry](BACKLOG.md#E2) with
    [its host-side twin](pack-host-management-plan.md) in one sitting, [the jail's skills fan-out](BACKLOG.md#OQ-S4), [pack binary pins](../design/broker-as-a-pack.md),
    [loophole env and guest fields](../design/loophole-packaging.md), [workspace MCP files](../design/workspace-mcp-sources.md),
    [the backend census](../design/backend-parity.md), [Seatbelt deny-default](../research/agent-safehouse.md),
    [host apply's posture](../design/host-render-target.md), [the I/O priority default](../design/io-priority.md). Only closing a
    document: [an unmatched-reference hatch](../design/reference-mismatch-diagnostics.md), [path mirroring](../design/workspace-path-mirroring.md),
    [pack telemetry](agent-config-packs.md), [AWS's key pair](../design/agent-auth-modes.md#OQ-9), [stateful-surface comments](BACKLOG.md#OQ-E4),
    [the upstream mise issue](../design/jail-state-separation-design.md).
35. The four calls the 2026-09-26 open-source audit left to the maintainer: whether one public research doc stays
    public, the scratch-directory `.gitignore` entries, a `last-release` recipe from the template, and when to retire
    the Python-version migration; the audit's report is outside this repository, so this is their only record.

36. [Make the Chrome DevTools MCP server work at `yolo host`](../design/mcp-presets-removal.md#5-the-chrome-devtools-inventory--what-the-pack-carries-what-the-image-keeps)
    — the maintainer wants it, after this week's work (2026-09-30).

    A host agent gets no `chrome-devtools` server today, because host apply expands no MCP preset
    ([HC-D16](../design/host-computed-layer.md#HC-D16)): composing one meant yolo keeping npm packages in a real home,
    and the host agent floor now gives yolo a prefix of its own for that. Build one path that works on every host, per
    the [happy-path principle](../reference/happy-path-principle.md). First stop: HC-D16's reason, then the inventory this links.

## External waits

37. One Mac session checks [whether two Apple Container jails mount the shared mise volume read-write at once](../research/macos-backend-performance.md#7-found-on-the-way-two-apple-container-jails-may-mount-one-ext4-disk)
    — first among the waits because, if they do, the ext4 disk can be corrupted; read from `container`'s source, not seen.
38. A human with a subscription login clears [Claude's login without interception](../design/claude-login-without-interception.md)
    by running [its runbook](runbooks/claude-credential-view-measures.md), on a Mac and for a day on a rootless host,
    and [the OpenAI service](../design/openai-auth-broker-plan.md) by recording one browser login and one shared expiry.
39. One session at a Mac clears [the host capture for installer agents](../design/host-tool-provisioning.md), which runs
    each vendor installer under Seatbelt; the hosted Mac job runs no vendor install until [OQ-CI7](../reference/agent-install-in-ci.md#OQ-CI7) says it may.
40. A real rootless Linux host, which a nested jail is not, clears [a real reboot](../design/podman-reboot-readiness.md#testing-and-the-real-host-check),
    [a low-space collection with a jail up](storage-lifecycle.md) and [the keeper's scope move and logout](../design/jail-lifetime-last-session-wins.md#8-what-done-looks-like).
41. One live agent session per check, which no test may start, clears [the footer](../design/agent-footer.md#21-as-built),
    [Claude's LSP plugin](../reference/mcp-configuration.md#lsp-claudes-route-is-a-generated-plugin), [the via route](../design/wire-bridge-gateway.md#41-how-it-is-built),
    [menus under a model list](../design/model-lists-and-pickers.md#13-build-order-and-what-done-looks-like),
    [a switch inside a provider set](../design/active-provider-sets.md#9-what-done-looks-like),
    [Copilot's config migration](../design/agent-directory-map.md#74-copilot) and [a Kilo session](../design/gateway-provider-packs.md).
42. A human with the cloud account clears [a first Bedrock request](../design/bedrock-plumbing.md),
    [an SSO lapse mid-turn](../design/sso-backed-bedrock.md#11-evidence-and-how-to-re-check-it),
    [settings-only Bedrock mode](../reference/providers.md#two-channels-split-by-payload-type),
    [an AgentCore gateway](../design/bedrock-web-search.md) and
    [a Claude usage-limit response](../design/wire-bridge-gateway.md#5-part-4--the-subscription-arm-and-opt-in-failover-ruled).
43. [Apple Container's published ports on the runner Mac](../design/backend-parity.md#54-which-test-answers-which-row) clear
    with a Local Network grant or Apple's signed `container` package and a rerun, which [the runbook](runbooks/mac-actions-runner.md) records.
44. The maintainer's own commits and launches clear [the `matt` pack's `briefing/`](../reference/pack-system.md#briefing),
    [its posture list at the host](../design/notch-scoped-config-contributions.md#5-fastest-path-to-the-motivating-case),
    [a dropped pack's host output](../reference/pack-system.md#retiring-a-dropped-packs-host-output), [the fzf pack's adoption](handoff-fzf-pack-adoption.md) and
    [the listen-port fix where reported](../reference/wire-bridge.md#what-can-hold-the-listen-port-before-the-bridge-does).
45. A live `llama-server` and one manual agent turn per row clear [the `llamacpp` provider](../../packs/llamacpp/README.md),
    whose per-agent spellings are read from each agent's code and have never met a server; copilot is the one to try first.
46. The next scheduled `macos-user.yml` run clears [the AWS doorway on a Mac](../design/host-notch-services.md), whose
    test now carries the region the launch requires and has never run.
47. One Mac session runs [the benchmark of macos-user against Apple Container](../research/macos-backend-performance.md#appendix-a--the-harness)
    — nothing has compared the two backends on one machine, and the macOS direction's VM-overhead premise rests on that comparison.

## Boundaries

- **Not committed to:** [`further-roadmap-ideas.md`](further-roadmap-ideas.md), a shelf of unverified candidates.
  **Iced**, with what would thaw each: [`yolo cache relocate`](cache-relocation.md) (someone wanting it),
  [trust-gated grants in a local config](../design/workspace-config-trust.md) (a workspace-specific read-write mount that
  cannot move into the workspace), [in-jail nix on the container Macs](setup-support-gaps.md#2-ranked-gap-backlog) (a user
  for whom `packages:`, mise and macos-user all fall short) and [a central host watcher](../research/central-yolo-watcher.md#7-what-would-have-to-be-true-before-building-one)
  (a duty that must hold a listener across launches, or a ruled credential timer).
- **Ids Vantage cannot index**, not being `OQ-` shaped, reached here by name: [Q1 to Q9](../design/environment-manager-user-stories.md#Q1),
  [9.2 and 9.6](../design/host-render-target.md#9.2), [CFP-1 to CFP-3](../design/composed-file-permissions.md#CFP-1),
  [Q2 and Q3](../design/macos-user-build-step-threat-model.md#Q2), [SS-6](../design/jail-state-separation-design.md#ss-6),
  [BP-1](../design/backend-parity.md#OQ-BP-1), [E1, E2, E5 and CO](BACKLOG.md#E1), [the host-files mode](pack-host-management-plan.md#open-questions),
  [H4](install-capture.md#hand-offs--what-is-not-wired-and-the-exact-line-that-wires-it), and two left outside this queue:
  [SH-1](macos-revival-and-distribution-plan.md#oq-sh-1--is-macos-user-self-hosting-worth-pursuing-at-all-maintainer),
  which blocks nothing, and [JD-4](../design/jail-daemon-on-macos-user-plan.md#decision-ledger), a maintainer follow-up.
- **With no id at all:** [Go comments citing sections by number](README.md#the-dangling-n-citations-in-go-comments),
  [check 2's two marker conventions](README.md#check-2-has-two-known-errors-and-a-convention-question-under-each),
  [the storage lifecycle's `min-free` values](storage-lifecycle.md#open-questions), the unruled half of
  [OQ-ST2](../design/synced-skill-trees.md#OQ-ST2) on user-reserved skill children, and whether a bare `-p` whose packs
  install no CLI should say it selects nothing (`checkProfileTargets`, `internal/cli/run/packs.go`).
