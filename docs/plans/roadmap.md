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

1. [Close the happy path principle's last two gaps](../reference/happy-path-principle.md#the-rules) — first
    because a successful `yolo host apply --assert` still ends at its counts with no next command, which rule 2
    calls a dead end; adding one waits on a ruling against [the verdict block](../reference/report-tiers.md#the-verdict-block)'s
    "Only the dry run has a footer". The other gap an agent can close now: rule 4's hint check does not read a command
    a message spells without backticks, such as the `then: yolo check` many `yolo check` notes end with. This entry
    is the work's only home.
2. [Rule whether a project's own mise pin of `pnpm` stays hidden](../design/program-delivery.md) — a declared `pnpm` gets its pnpm
    now; what [OQ-PD19](../design/program-delivery.md#oq-pd19) still decides is whether a project's `mise.toml` pin is hidden when yolo supplies the pnpm.
3. [Rule whether the jail's config copy keeps other workspaces' widening entries](../design/boundary-broker.md#OQ-BB11) — `.yolo/config-assembled.json`,
    which a jail can read, holds every workspace's `brokered` entries today; rule it with
    [how a host daemon hands the launching terminal a line](../design/boundary-broker.md), which the write path's notice waits on.
4. [Rule whether a pack service's in-jail daemon is disclosed](../design/trust-paths.md#OQ-TP11) — the wire
   bridge runs inside every claude jail and no launch line names it, though disclosure is today's whole
   trust boundary.
5. [Rule whether copilot and oh-omp alone get `-p bedrock`](../design/bedrock-plumbing.md#OQ-BR24) — either
   agent by itself reaches nothing there unless another selected pack brings Bedrock in.
6. Rulings that hold a defect's fix: [the Kilo special-casing in two derives](../design/gateway-provider-packs.md), while
    claude gives every Kilo model without a declared window a 1M-token context; [whether a hatch reaches a required daemon's
    refusal](../reference/loopback-tls-reachability.md#OQ-R8), which the documented hatch cannot reach today; [`--no-daemon` for every in-jail `codex`](../research/codex-background-service.md#OQ-CDX3),
    since `codex agents` there still starts the daemon yolo turns off, stale model list and all; and [how host pi gets its
    OpenAI subscription credential](../design/pi-host-openai-auth.md), since `yolo host -- pi` on the `codex` profile starts
    on another model and lists no ChatGPT model.
7. [Rule whether the pi pack may rewrite its own `packages` list](../design/pi-git-extension-caching.md) — the
    per-commit extension store that ends every jail sharing one npm prefix is rebased and green, and lands on this ruling.
8. [Rule the macos-user workspace root](../design/configurable-workspace-root.md), [its whitelist](../design/configurable-workspace-root.md#OQ-CW2) first — the home check
    now folds case and knows the `/Users` firmlink, but a `/var/root` home still passes until the whitelist replaces
    the blacklist, and tightening later withdraws what users may rely on.
9. [Decide whether a workspace config may feed host dotenvs and mounts into a jail](../research/agent-safehouse.md#OQ-AS3)
    — a committed config still can, and notch convergence's `env_sources` item waits on the answer.
10. [Rule which packs a workspace config may declare](../reference/pack-system.md#OQ-PK1), then [retire `mcp_presets`](../design/mcp-presets-removal.md)
    — that build rests on the boundary the ruling redraws, and ends macos-user's blanket refusal of MCP presets, and also decides whether the compose
    engine's [`workspace` layer](agent-settings-composition.md) gets a producer.
11. [Rule the slot split's migration window](../design/slots-and-contributions.md#OQ-D6), then [the manifest language](../design/manifest-language.md)
    with [the slots' other calls](../design/slots-and-contributions.md) — `exposes` and [the pi extension-tree
    rework](../design/pi-pack-extensions.md) wait on the first, and one sitting for the rest rewrites manifests once.
12. Provider rulings later decisions build on: [how far the natively-implements rule reaches](../design/pi-codex-provider-shadowing.md),
    which holds pi's Converse route through the wire bridge, and [what `-p` names](../design/providers-and-profiles-redesign.md),
    which the plain-words rewrite of the provider reference waits on.
13. [Decide whether a jail shell gets the credential grant](../design/credential-sources-separation.md) — a jail's
    `--with-credentials` and [its build sketch](../design/credential-sources-separation-plan.md) wait on it.
14. [Rule one env composition order](notch-convergence.md), and [confirm whether host claude may join the jails'
    login](../reference/claude-oauth-interposition.md#OQ-CI1) — one variable's value depends on how the agent starts,
    and an existing ruling may answer the login, so an agent drafts that leaning first; the plan's other held items
    wait on the workspace-config and jail-credential rulings above and the `assert` ruling next.
15. [Settle what retiring `host_management: "assert"` does to configs on it](../design/config-ownership-and-promotion.md)
    — the retirement cannot start until then.
16. [Rule what serializes a daemon's spawn once the host singleton goes](../design/host-daemon-ownership.md), against
    [the plan's table of what the spawn flock covers](../design/host-daemon-ownership-plan.md) — retiring the machine-wide credential
    daemons waits on it, including the OpenAI legacy-state migration and the `yolo host -- codex|pi` spawns the question does not name.
17. [Rule the provisioner override's grain](../design/provisioner-sets.md) — the user-scope preference that re-ranks the
    recipes packs ship waits on it, and macos-user's corporate-CA trust rides in the same design.
18. [Rule what the environment manager promises at each notch](../design/environment-manager-user-stories.md) — Q1b
    decides [`yolo check --at`](../design/yolo-as-environment-manager.md) and Q7 [the Linux guest notch](environment-manager-plan.md).
19. [Choose what replaces the Intel macOS runner](../research/macos-support-matrix.md) — its nixpkgs security window
    closes at the end of 2026, leaving the podman macOS suite unpatched or without a hosted runner.
20. [Rule the keychain from a jail](../design/keychain-from-a-jail.md), which settles [Copilot's machine-wide
    login](../research/copilot-token-storage.md) — until then every workspace asks for its own Copilot login.
21. [Rule what the keeper holds at `yolo host`](../design/jail-lifetime-last-session-wins.md), with
    [the sidecars and doorbell](../design/agent-event-watchers.md) — the keeper at `yolo host` and macos-user and the doorbell wait on
    them, and each design's host half waits on the other.
22. Jail-boot rulings: [what a failed agent install does to a launch](../design/jail-notch-readiness.md), since the
    provisioning command leaves every declared agent CLI uninstalled until first use, and [the boot snapshot and
    diagnostic dial](../design/diagnostics-past-the-boundary.md), since a refused boot keeps no record of the jail's state
    when it gave up, such as which process held a port.
23. [Rule the add-only model lists, then whether a list refuses without `only`](../design/model-lists-and-pickers.md), and
    [web search on Bedrock](../design/bedrock-web-search.md) — what most agents' menus show turns on the first two, and the 2026-10-01
    reading found no agent gets search from Bedrock on runtime, so the search questions now decide whether yolo supplies one.
24. [Decide whether pack-declared traps fold into the agent directory map](../design/agent-directory-map.md) — the map
    would supersede [the traps design](../design/pack-declared-file-diagnostics.md), so rule it before building either.
25. [Rule what triggers an in-jail build's GC root, and whether `gcroots/auto` may be bound in](../design/in-jail-nix-roots.md) — yolo's
    own in-jail roots are registered under the host's spelling now, and a root a user or an agent makes is still dead on arrival.
26. [Approve the host capability gate](../design/declaration-parity.md) (DP-B46: `yolo host --` skips the capability gate a jail
    launch applies) — a nod, not a ruling, and the last item of that census.
27. [Rule how macos-user reaches the capture store](install-capture.md) and [whether integration sharding unparks](integration-parallelism.md)
    with [the test suite's two other levers](test-suite-speed.md),
    and [name the real forked program](../design/forked-programs-as-packs.md) — the host notch's floor arms are built on a stand-in fork,
    and the real one is the last input step 7 needs.
28. [Rule how a jail reaches a worktree's `.git` outside it](../research/herdr-integration.md#OQ-HR3), with herdr's other two open questions,
    [what a jail pane does after a herdr restart](../research/herdr-integration.md#OQ-HR2) and
    [whether a jailed agent may drive herdr](../research/herdr-integration.md#OQ-HR4) — the 2026-10-01 measurement showed a read-only bind
    refuses every commit and a read-write one lets a jail prune the outside worktree, so the herdr questions now rest on facts.
29. Calls the 2026-10-01 groundwork left at one decision each: [mise's prune record](../design/minimal-disk-footprint.md),
    [colliding attribute paths](../design/package-nested-attribute-paths.md), [pi's package loader](../design/pack-pi-resources.md),
    [who installs a pi package the refresh missed](../design/pi-extension-lifecycle.md), [the `:ro` degradation rows](../design/composed-file-permissions.md),
    [whether messages name the guide's URL](../design/docs-website.md#OQ-DW3), [the macOS nix build sandbox](../design/macos-user-build-step-threat-model.md),
    [copilot's updater](native-installer-migration.md), [whether array-append pinning closes](BACKLOG.md#E5), [the pack system's other calls](../reference/pack-system.md) and
    [relocating `.yolo` by a link](../reference/jail-home.md#OQ-JH1)
    — each has its facts and a leaning now, so each is a short sitting.
30. [Rule whether yolo may hold an editor preference](../design/baked-editor-preference.md) — the maintainer asked
    for it, and its first ruling decides the rest and an image change every jail pays.
31. Rulings nothing shipped waits on. Gating one later step: [the host-file permission asymmetry](BACKLOG.md#E2) with
    [its host-side twin](pack-host-management-plan.md) in one sitting, [the jail's skills fan-out](BACKLOG.md#OQ-S4), [pack binary pins](../design/broker-as-a-pack.md),
    [loophole env and guest fields](../design/loophole-packaging.md), [workspace MCP files](../design/workspace-mcp-sources.md),
    [the backend census](../design/backend-parity.md), [Seatbelt deny-default](../research/agent-safehouse.md),
    [host apply's posture](../design/host-render-target.md), [the I/O priority default](../design/io-priority.md). Only closing a
    document: [an unmatched-reference hatch](../design/reference-mismatch-diagnostics.md), [path mirroring](../design/workspace-path-mirroring.md),
    [pack telemetry](agent-config-packs.md), [AWS's key pair](../design/agent-auth-modes.md#OQ-9), [stateful-surface comments](BACKLOG.md#OQ-E4),
    [the upstream mise issue](../design/jail-state-separation-design.md).
32. The four calls the 2026-09-26 open-source audit left to the maintainer: whether one public research doc stays
    public, the scratch-directory `.gitignore` entries, a `last-release` recipe from the template, and when to retire
    the Python-version migration; the audit's report is outside this repository, so this is their only record.

33. [Make the Chrome DevTools MCP server work at `yolo host`](../design/mcp-presets-removal.md#5-the-chrome-devtools-inventory--what-the-pack-carries-what-the-image-keeps)
    — the maintainer wants it, after this week's work (2026-09-30).

    A host agent gets no `chrome-devtools` server today, because host apply expands no MCP preset
    ([HC-D16](../design/host-computed-layer.md#HC-D16)): composing one meant yolo keeping npm packages in a real home,
    and the host agent floor now gives yolo a prefix of its own for that. Build one path that works on every host, per
    the [fill-the-matrix principle](../reference/fill-the-matrix-principle.md). First stop: HC-D16's reason, then the inventory this links.

## External waits

34. One Mac session checks [whether two Apple Container jails mount the shared mise volume read-write at once](../research/macos-backend-performance.md#7-found-on-the-way-two-apple-container-jails-may-mount-one-ext4-disk)
    — first among the waits because, if they do, the ext4 disk can be corrupted; read from `container`'s source, not seen.
35. A human with a subscription login clears [Claude's login without interception](../design/claude-login-without-interception.md)
    by running [its runbook](runbooks/claude-credential-view-measures.md), on a Mac and for a day on a rootless host,
    and [the OpenAI service](../design/openai-auth-broker-plan.md) by recording one browser login and one shared expiry.
36. One session at a Mac clears [the host capture for installer agents](../design/host-tool-provisioning.md), which runs
    each vendor installer under Seatbelt; the hosted Mac job runs no vendor install until [OQ-CI7](../reference/agent-install-in-ci.md#OQ-CI7) says it may.
37. A real rootless Linux host, which a nested jail is not, clears [a real reboot](../design/podman-reboot-readiness.md#testing-and-the-real-host-check),
    [a low-space collection with a jail up](storage-lifecycle.md) and [the keeper's scope move and logout](../design/jail-lifetime-last-session-wins.md#8-what-done-looks-like).
38. One live agent session per check, which no test may start, clears [the footer](../design/agent-footer.md#21-as-built),
    [Claude's LSP plugin](../reference/mcp-configuration.md#lsp-claudes-route-is-a-generated-plugin), [the via route](../design/wire-bridge-gateway.md#41-how-it-is-built),
    [menus under a model list](../design/model-lists-and-pickers.md#13-build-order-and-what-done-looks-like),
    [a switch inside a provider set](../design/active-provider-sets.md#9-what-done-looks-like),
    [Copilot's config migration](../design/agent-directory-map.md#74-copilot), [a Kilo session](../design/gateway-provider-packs.md)
    and [a codex, opencode and pi turn on Bedrock](../design/bedrock-plumbing.md#8-behaviour-this-design-fixes) under `aws-auth`'s SSO credential.
39. A human with the cloud account clears [a request under a Bedrock API key or a static key pair](../design/bedrock-plumbing.md#64-the-credential-three-are-supported)
    (the SSO credential [reached runtime's Messages and Responses routes](../design/wire-bridge-gateway.md#24-the-first-live-requests-measured-2026-10-01) from a jail on 2026-10-01, sent by hand with no agent),
    [an SSO lapse mid-turn](../design/sso-backed-bedrock.md#11-evidence-and-how-to-re-check-it),
    [settings-only Bedrock mode](../reference/providers.md#two-channels-split-by-payload-type),
    [an AgentCore gateway](../design/bedrock-web-search.md) and
    [a Claude usage-limit response](../design/wire-bridge-gateway.md#5-part-4--the-subscription-arm-and-opt-in-failover-ruled).
40. [Apple Container's published ports on the runner Mac](../design/backend-parity.md#54-which-test-answers-which-row) clear
    with a Local Network grant or Apple's signed `container` package and a rerun, which [the runbook](runbooks/mac-actions-runner.md) records.
41. The maintainer's own commits and launches clear [the `matt` pack's `briefing/`](../reference/pack-system.md#briefing),
    [its posture list at the host](../design/notch-scoped-config-contributions.md#5-fastest-path-to-the-motivating-case),
    [a dropped pack's host output](../reference/pack-system.md#retiring-a-dropped-packs-host-output), [the fzf pack's adoption](handoff-fzf-pack-adoption.md) and
    [the listen-port fix where reported](../reference/wire-bridge.md#what-can-hold-the-listen-port-before-the-bridge-does).
42. A live `llama-server` and one manual agent turn per row clear [the `llamacpp` provider](../../packs/llamacpp/README.md),
    whose per-agent spellings are read from each agent's code and have never met a server; copilot is the one to try first.
43. The next scheduled `macos-user.yml` run clears [the AWS doorway on a Mac](../design/host-notch-services.md), whose
    test now carries the region the launch requires and has never run.
44. One Mac session runs [the benchmark of macos-user against Apple Container](../research/macos-backend-performance.md#appendix-a--the-harness)
    — nothing has compared the two backends on one machine, and the macOS direction's VM-overhead premise rests on that comparison.
45. The next `macos-user.yml` run reads [the context-mount cases](../design/context-mounts.md), after which
    [OQ-CX7](../design/context-mounts.md#OQ-CX7), sources inside a home, can be ruled.
46. The scheduled Mac jobs run the nine macos-user, Cachix and Apple Container checks written 2026-10-01, a dispatched
    `apple-container.yml` runs [the keeper measures](../design/jail-lifetime-last-session-wins.md#7-what-i-would-build-in-order), and the next nightly
    reads [the stalled in-VM copier](../research/macos-layer-reusing-image-delivery.md), which now dumps its stacks at the deadline.

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
