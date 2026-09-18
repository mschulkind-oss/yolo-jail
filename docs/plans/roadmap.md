---
title: "Roadmap"
date: 2026-09-13
status: accepted
tags: [roadmap, routing, open-questions]
summary: "The routing table for open work: one line per decision, the doc that holds it, and what ruling it releases — ordered so the next hour is obvious."
vantage:
  status-chip: true
---

# Roadmap

**Status:** CURRENT — 2026-09-17. **43 rows**: 4 to rule first, 22 needing a decision,
8 ready to build, 7 waiting, 2 iced.

This file is a **routing table, not a place to think**: one line per open decision, naming
the doc that holds it and what a ruling releases. It is **not a record of what happened** —
work that closes leaves the file, and `git log -p` is the history. Every count here is
derived rather than carried forward; re-derive the live-question totals with check 2 of
[`README.md`](README.md#keeping-this-corpus-honest--the-five-checks-so-they-are-re-runnable):

```console
$ rg -c '^(#{2,4} |\s*[0-9]+[a-z]?\. |\s*[-*] )(<a id="[^"]*"></a> ?)?💬' docs/ --sort path
```

**116 live questions across 32 docs**, plus one 🔒 that command cannot see.

## Rule these first

**The ordering basis, so it is checkable:** a defect live in shipped code outranks blocked
build work, which outranks a ruling that only closes a doc; ties break toward the smallest
sitting. All four below are the first class.

| | Rule | Releases | Cost |
|---|---|---|---|
| **1** | [`OQ-2`](../design/cerebras-pack-and-copilot-delivery.md#oq-2) — gate `ANTHROPIC_AUTH_TOKEN` on a declared anthropic endpoint | **Defect, live today, re-verified 2026-09-17.** In `packs/claude/derive.lua`, `if p.api_key then out.ANTHROPIC_AUTH_TOKEN = p.api_key` sits in the branch BEFORE the `elseif routed`, so a provider carrying a key but declaring no anthropic endpoint still gets the key emitted. (Line numbers removed from this cell on purpose: they were already stale and moved again the same day.) | one ruling, three provider shapes |
| **2** | [`OQ-3`](../design/broker-ca-and-nested-hosts.md#7-open-questions) — a `[SKIP]` level for `yolo check` | **Defect, live today.** `internal/cli/check/sections_loopholes.go:23` calls `r.ok` on a section it skipped, and `reporter.go:83` counts it as a pass — ten sites | one ruling, ~10 lines |
| **3** | [`OQ-BR4`](../design/bedrock-plumbing.md#OQ-BR4) — narrow `profile`'s env gate | **Defect, live today.** `internal/packload/packload.go:634` matches a bin *any* selected pack installs, so one agent's profile fires another's env | one ruling |
| **4** | [`OQ-PS2`](../design/provider-switching.md#OQ-PS2) — clear the model id a deselect orphans | **Defect, live today.** `internal/agentcfg/selection.go:168` only ever lifts, so a dropped profile leaves its model pinned | one ruling |

## 💬 Needs you

| # | Decides | Doc | Live | Gate | Releases |
|---|---|---|---|---|---|
| 1 | Whether the backend census is worth building, and whether a `Warned` disposition can be suppressed | [`backend-parity.md`](../design/backend-parity.md) · in-review | 2 | [`OQ-BP-1`](../design/backend-parity.md#open-questions) | **build** — the census itself. `OQ-BP-5` left this row on 2026-09-15, answered by code (an ACE) rather than by a ruling |
| 4 | A stated byte budget, or a policy — **now measured** | [`minimal-disk-footprint.md`](../design/minimal-disk-footprint.md) · in-review | 1 | [`OQ-DF4`](../design/minimal-disk-footprint.md#OQ-DF4) | **doc** — unblocked 2026-09-15; ~70 GiB/yr grows unreclaimed but 99% is two NAMED stores, so the call is "sweep `mise/` + `cache/staticcheck`, or declare them yours" |
| 5 | Provider deselection, aliases and shipped model ids | [`provider-switching.md`](../design/provider-switching.md) · draft | 4 | [`OQ-PS2`](../design/provider-switching.md#OQ-PS2) | **defect** — an orphaned model pin |
| 6 | Whether claude's derive gates its token on an endpoint | [`cerebras-pack-and-copilot-delivery.md`](../design/cerebras-pack-and-copilot-delivery.md) · in-review | 1 | [`OQ-2`](../design/cerebras-pack-and-copilot-delivery.md#oq-2) | **defect** — a key sent to the wrong vendor |
| 7 | What a skipped `check` section reports, and openssl | [`broker-ca-and-nested-hosts.md`](../design/broker-ca-and-nested-hosts.md) · in-review | 5 (3 distinct; [§8](../design/broker-ca-and-nested-hosts.md#8-sequencing) restates two) | [`OQ-3`](../design/broker-ca-and-nested-hosts.md#7-open-questions) | **defect** — `[PASS]` on an unchecked area |
| 8 | Bedrock's schema marker, names, region and env gate | [`bedrock-plumbing.md`](../design/bedrock-plumbing.md) · draft | 7 | [`OQ-BR4`](../design/bedrock-plumbing.md#OQ-BR4) | **defect** — a cross-agent profile leak |
| 9 | Whether a mistyped reference refuses or only reports | [`reference-mismatch-diagnostics.md`](../design/reference-mismatch-diagnostics.md) · in-review | 4 | [`OQ-RM2`](../design/reference-mismatch-diagnostics.md#OQ-RM2) | **defect** — the diagnostic reaches bare stderr only |
| 10 | What the environment manager promises at each notch | [`environment-manager-user-stories.md`](../design/environment-manager-user-stories.md) · no frontmatter | 11 | Q8, [§Open Questions](../design/environment-manager-user-stories.md#open-questions) | **defect** — `internal/cli/briefing.txt:5` says JAIL at every notch |
| 11 | Skill and overlay collisions, and `host_files` modes | [`BACKLOG.md`](BACKLOG.md) Stage E · current | 7 | [`E2`](BACKLOG.md#-e2--readonly-as-a-real-ro-mount-instead-of-0o444) | **defect** — two silent last-one-wins paths |
| 12 | Who provisions a binary at each notch, and in what order | [`provisioner-sets.md`](../design/provisioner-sets.md) · in-review | 13 | [`OQ-PS1`](../design/provisioner-sets.md#OQ-PS1) | **build** — the host notch's provisioner |
| 13 | The contribution shape that replaces `mcp_presets` | [`mcp-presets-removal.md`](../design/mcp-presets-removal.md) · in-review | 6 | [`OQ-MP3`](../design/mcp-presets-removal.md#OQ-MP3) | **build** — a pack-shipped MCP kind |
| 14 | Whether `program` below `jail` is refused or confirm-gated | [`yolo-as-environment-manager.md`](../design/yolo-as-environment-manager.md) · no frontmatter | 2 | [`OQ-EM1`](../design/yolo-as-environment-manager.md#OQ-EM1) | **build** — env-manager Phase 4.3 |
| 15 | How a dotted leaf in `packages:` resolves | [`package-nested-attribute-paths.md`](../design/package-nested-attribute-paths.md) · draft | 1 | [`OQ-1`](../design/package-nested-attribute-paths.md#OQ-1) | **build** — the resolver, or nothing |
| 16 | Whether program-delivery steps three and five survive | [`program-delivery.md`](../design/program-delivery.md) · in-review | 1 | [`OQ-PD19`](../design/program-delivery.md#-oq-pd19--do-steps-three-and-five-still-have-a-subject-after-the-agentproject-split) | **build** — or a retirement |
| 17 | How a pack ships a binary of its own | [`broker-as-a-pack.md`](../design/broker-as-a-pack.md) · no frontmatter | 2 | [`OQ-BP5`](../design/broker-as-a-pack.md#OQ-BP5) | **build** — and the question narrowed on 2026-09-17, when the mechanism was actually run (`packs/hello-daemon`). Declaration is fine and needs no schema change; DELIVERY is half-broken, and not fixably: an EMBEDDED pack cannot carry an exec bit at all, because `embed.FS` reads every file back as `0444`. So this now decides how an OFFICIAL pack ships a program, given that the route [§3.1](../design/broker-as-a-pack.md#31-what-is-actually-unresolved-here) rated ✅ works only for a pack configured by path |
| 18 | What replaces `macos-26-intel` before 26.05 lapses | [`macos-support-matrix.md`](../research/macos-support-matrix.md) · live tracker | 1 | [§5 item 8](../research/macos-support-matrix.md#5-roadmap-ordered) | **build** — a CI commitment, with a 2026 deadline |
| 19 | What an attach does with the attacher's pack set | [`agent-config-packs.md`](agent-config-packs.md) · overtaken | 4 | [`OQ-ACP3`](agent-config-packs.md#-oq-acp3--whether-the-prism-should-become-a-standalone-tool-that-also-manages-host-configs) | **defect** — `internal/cli/run/run.go:766` re-renders before the attach branch at `:774` |
| 20 | Whether `yolo host apply` is a convenience or the path | [`host-render-target.md`](../design/host-render-target.md) · no frontmatter | 3 | 9.2, [§9](../design/host-render-target.md#9-open-questions--the-discussion-part) | **doc** — product posture; [§8](../design/host-render-target.md#8-what-i-would-actually-do-in-order) step 3 is in 📦 regardless |
| 21 | Whether the jail mounts the workspace at the host's path | [`workspace-path-mirroring.md`](../design/workspace-path-mirroring.md) · draft | 12 | [`OQ-WP8`](../design/workspace-path-mirroring.md#OQ-WP8) | **doc** — ratify the no |
| 22 | Whether `macos-user`'s workspace root becomes configurable, and what the profile has to derive from it | [`configurable-workspace-root.md`](../design/configurable-workspace-root.md) · in-review | 4 | [`OQ-CW1`](../design/configurable-workspace-root.md#OQ-CW1) | **build** — the read-deny derived from the root, plus the whitelist that closes four lexical bypasses. ⚠ The whitelist wants to land before anyone relies on the current blacklist; withdrawing an accepted path later is a breaking change |
| 23 | How a `jail_daemon` runs on `macos-user` — argv resolution with no image, and whether it is confined | [`jail-daemon-on-macos-user-plan.md`](../design/jail-daemon-on-macos-user-plan.md) · draft | — (no ids, so the count below cannot see them; both are stated in that plan's §Blockers) | [§Blockers](../design/jail-daemon-on-macos-user-plan.md) | **build** — and it gates the blocked halves of BOTH 📦 [row 1](#-ready) and [row 5](#-ready), which is why it is one row. Every shipped `jail_daemon.cmd` names `yolo-jaild`, which exists only as `bin/linux-<arch>`, and a symlink cannot reach it because both binaries dispatch on plain `args[0]` rather than `argv[0]`. And `DP-L3`'s prescribed placement in `buildBootstrapEnv` would start the daemon UNCONFINED and orphaned, which the doc is silent about |
| 24 | Storage tier and pre-launch update execution for Pi extensions across jails | [`pi-extension-lifecycle.md`](../design/pi-extension-lifecycle.md) · in-review | 3 | [`OQ-1`](../design/pi-extension-lifecycle.md#OQ-1) | **build** — machine-scoped package storage and launcher refresh |

**Rule together, or not at all.** [`E1`](BACKLOG.md#-e1--collapse-host_files-modes-43-copy-merges-into-readonly) · [`E2`](BACKLOG.md#-e2--readonly-as-a-real-ro-mount-instead-of-0o444) · [`OQ-B`](pack-host-management-plan.md#open-questions) are one asymmetry seen three times, and each doc says so.
[`OQ-BR3`](../design/bedrock-plumbing.md#OQ-BR3) and [`OQ-PS3`](../design/provider-switching.md#OQ-PS3) are the same decision in two files.
[`OQ-PS6`](../design/provisioner-sets.md#OQ-PS6) · [`OQ-PS7`](../design/provisioner-sets.md#OQ-PS7) · [`OQ-PS12`](../design/provisioner-sets.md#OQ-PS12) together release [§9](../design/provisioner-sets.md#9-what-i-would-build-in-order) step 4, and nothing else does.
[`OQ-WP8`](../design/workspace-path-mirroring.md#OQ-WP8) closes [`OQ-WP1`](../design/workspace-path-mirroring.md#OQ-WP1) and [`OQ-WP6`](../design/workspace-path-mirroring.md#OQ-WP6) with it.

**Small calls that do not deserve a row**, each blocking nothing by its own doc's account:
[`CFP-1`–`CFP-3`](../design/composed-file-permissions.md#open-questions) (graduated; three postures) ·
[`OQ-9`](../design/agent-auth-modes.md#OQ-9) (AWS's two-part credential already works) ·
[`OQ-LP5`](../design/loophole-packaging.md#oq-lp5) and [`OQ-LP7`](../design/loophole-packaging.md#oq-lp7) (cheap to rule, expensive to discover later) ·
[`SS-6`](../design/jail-state-separation-design.md#ss-6) (file an upstream mise issue; unverifiable from in here) ·
[`Q2`](../design/macos-user-build-step-threat-model.md#-q2--is---accept-flake-config-worth-the-substituter-poisoning-surface) (keep `--accept-flake-config`, or drop it) ·
[`OQ-B`](pack-host-management-plan.md#open-questions) (`0o444` at the host — explicitly taste).
🤷 **Genuinely subjective, wherever they sit:** [`OQ-PS4`](../design/provider-switching.md#OQ-PS4) ·
[`OQ-RM4`](../design/reference-mismatch-diagnostics.md#OQ-RM4) ·
[`OQ-WP7`](../design/workspace-path-mirroring.md#OQ-WP7) · [`OQ-WP12`](../design/workspace-path-mirroring.md#OQ-WP12) ·
[`OQ-BP-3`](../design/backend-parity.md#open-questions) · `OQ-B`.

## 📦 Ready

Ruled, unblocked, and implementable cold — no memory of any conversation required.

| | Build | Why it is ready | Where |
|---|---|---|---|
| 1 | Give the OpenAI subscription service a `macos-user` refresh consumer, make an Apple Container launch ADMIT it cannot carry the service, and add trusted host-only import/logout | the canonical transaction, podman transport, both agent adapters, managed host launches, browser login, status and self-check are built — **re-audited against the tree 2026-09-17**, which found four of the plan's eight steps only PARTIAL: no Codex version floor is pinned anywhere, the Pi extension has no ask-once-more after an unauthorized response, a third concurrent login has no port, and `Broker.Logout` has no production caller at all (tests only). ⚠ **Apple Container is not transport work.** `backendInertReason` records a measurement on `container` 1.1.0 — nothing crosses container→host and `host.containers.internal` does not resolve — while the launch still hands AC the endpoint variable, withholds the inert line for this one pack (`withoutOpenAIAuthPack`), and emits an `unknown` disposition the fatal witness never escalates. So the user is told nothing: that half is a DISCLOSURE fix and the transport is upstream. ⚠ The `macos-user` half waits on 💬 [row 23](#-needs-you) | [`openai-auth-broker.md`](../design/openai-auth-broker.md) · [`openai-auth-broker-plan.md`](../design/openai-auth-broker-plan.md) |
| 2 | Stop the daemon supervisor swallowing a spawn failure | sharper than it looks, and **re-verified 2026-09-17** — with one clause of this cell corrected: `superviseOne` DOES read `c.start()`'s error, as a nil-test that drives the retry; what it discards is the error VALUE, and `c.spec.Restart` appears nowhere in that branch. `waitAndMaybeRestart` is the only production reader of the policy and is reached only after a successful spawn, so `restart: "no"` is honored when a daemon EXITS and looped forever when it fails to SPAWN — 1s→30s, for the life of the jail — while `openLog` has already created an empty `<name>.log`. The observable symptom of any spawn fault is therefore an empty log and no process. **One ruling to make inline:** whether `restart: "always"` still retries forever on a spawn failure (a typo'd `cmd` would keep most of the symptom), and whether the error goes into `<name>.log` — the file a user actually opens — or to the supervisor's own stderr. Two files incl. the test, whose harness already calls the broken function directly. ⚠ Unobservable on `macos-user`, which has no jail-daemon consumer at all (📦 row 5), so "verified" will mean two backends of four; and the same payload carries pack `service` daemons, so this changes their spawn behaviour too | `internal/supervisor/supervisor.go` |
| 3 | Give `packs/pi/derive.lua` a `compat` block for loopback endpoints | measured 2026-09-17: pi's auto-detection for an unknown loopback URL yields the INVERSE of pi's own llama.cpp provider flags (`supportsStore`/`DeveloperRole`/`ReasoningEffort`/`StrictMode`, and `max_completion_tokens`). Nothing else blocks the `llamacpp` pack's pi smoke test | `packs/pi/derive.lua` |
| 4 | Run the capability gate in `yolo check` | the gate shipped 2026-09-17 in the run pre-flight only (`refuseUnmetCapabilities`), so a config `yolo check` calls clean can still be refused at launch — the one thing `check` exists to prevent. **Re-verified 2026-09-17:** `internal/cli/check` mentions capabilities nowhere, there is no import cycle either way, the seat is `sectionMergedConfig` (which already collects `ValidateConfig`'s errors), fixtures exist in `preflight_test.go`, and the repo's own precedent for this exact structure is `checkPresetNullConflicts` — deliberately defined twice rather than imported. **Decide inline:** whether `check` still reports the gap when `YOLO_ALLOW_UNMET_CAPABILITIES` is set (run's rule is silence-unless-suppressing; check exists to predict the launch — opposite answers), and whether a satisfied gate prints a PASS line, which bumps a byte-pinned golden and its counts. ⚠ Must NOT consult pack data: the gate reads the merged user config only, because pack providers compose below it — a check that sees more would pass configs the launch refuses, inverting this row | `internal/cli/check` |
| 5 | Start `jail_daemon` on `macos-user` — the truthful decline now, the daemon after 💬 [row 23](#-needs-you) | only the HOST half of that backend's loophole lifecycle was generalised 2026-09-17: an endpoint is delivered and ACL-granted with no in-jail consumer, because the wiring lives in `loopholes.RuntimeArgsFor`'s `YOLO_JAIL_DAEMONS`, emitted into a container argv only, and `rg 'JailDaemon' internal/macosuser/` is empty. **The old cell understated it, and this is the DEFAULT configuration, not an edge case:** `packs/claude` `needs` both `openai-auth` and `wire-bridge` unconditionally, so a bare `"packs": ["claude"]` selects TWO jail daemons and starts neither — `openai-auth-broker` is `default_enabled: true` with a fail-closed host daemon on this arm, `packs/codex` points `CODEX_REFRESH_TOKEN_URL_OVERRIDE` at the port its missing adapter would bind, and `wire-bridge`'s endpoint is published and granted with nothing listening. **Ready cold:** extract one payload producer, hoist it above the backend dispatch, decline by name per daemon, retract the stale prose in four docs. Depends on 📦 row 2 — today a spawn failure is an empty log and no process | [`jail-daemon-on-macos-user-plan.md`](../design/jail-daemon-on-macos-user-plan.md) · `internal/loopholes`, `internal/macosuser` |
| 6 | Strip `provides` from MCP entries before they reach an agent's config file | pre-existing and now load-bearing: `provides` became a real input on 2026-09-17 rather than an inert annotation, in two independent readers. **Re-verified 2026-09-17, and this cell used to name one agent where FOUR leak:** claude (`~/.claude.json`), agy, copilot and pi all copy the entry verbatim; codex and opencode are already clean because they rebuild it field-by-field. The strip point is the ctx boundary beside `eligibleMCPServers` — the one place both production callers pass through, so no agent can opt out — which also covers the second surface this row's wording misses: `provides` travels in `YOLO_MCP_SERVERS` too, including on `macos-user`. ⚠ **The obvious site is a trap:** `entrypoint/mcp.go` is where `requires_env` is stripped, but it runs UPSTREAM of the derive, so stripping there silently turns capability-driven MCP delivery into a no-op with every test still green. ⚠ Unverified in-tree: whether pi, copilot and agy also ignore the key — asserted only for Claude Code | the MCP projection (`internal/agentcfg/luahook`) |
| 8 | One resolved target for every `yolo config` verb: notch, workspace, store, home root and may-write computed once, disclosed on every invocation, selectable with `--at` | **all eight questions ruled 2026-09-17** across two review rounds ([Decision Ledger](../design/config-target-resolution.md#10-decision-ledger)) and the plan **promoted against the tree** the same day — an eight-step order, the files each step touches, the test file each step's test lands in. It closes five measured defects: one report describing two homes, a `cd` changing the answer at rc 0, `diff` and `reset` disagreeing about the store under `own`, a preview reading the wrong home's `host` layer, and `apply --sealed` reporting *"sealed"* from a directory that is not a workspace. [`OQ-CR1`](../design/config-target-resolution.md#oq-cr1) went against its leaning on review (*"the cwd selects nothing"* is a WRITE-side rule; what the design removes is the predicate PAIR, not the inference), and [`OQ-CR7`](../design/config-target-resolution.md#oq-cr7) did too (provenance is the render's fact, so `diff` drops that block). **The find that shortens it:** `jailHomeHostPath` already IS the resolver the design calls missing — backend-aware, declining for a path the workspace does not back — with one caller today. ⚠ One of the plan's five Blockers is a gap rather than a detail: `host_management` is deliberately not inherited into a jail, so [`OQ-CR6`](../design/config-target-resolution.md#oq-cr6)'s two-case table has no discriminator at the jail notch, and the launch's `YOLO_HOST_LAYERS` report is where that fact has to travel. None of the five blocks steps 1–5 | [`config-target-resolution.md`](../design/config-target-resolution.md) · [`config-target-resolution-plan.md`](../design/config-target-resolution-plan.md) |
| 7 | Bedrock from a host `aws sso login`: a host credential service, plus a jail-side adapter speaking the AWS container-credentials protocol | all six questions ruled 2026-09-17 ([Decision Ledger](../design/sso-backed-bedrock.md#13-decision-ledger)); [§8](../design/sso-backed-bedrock.md#8-behaviour-this-design-specifies) specifies the behaviour and [§12](../design/sso-backed-bedrock.md#12-what-i-would-build-in-order) the order. The plan was **promoted against the tree 2026-09-17** with an eight-step order, the files each step touches, and the `openai-auth` machinery to copy. ⚠ Steps 3 and 5 need a **real rootless host** — a nested jail is structurally blind to the host-loopback hop (AGENTS.md's carve-out). ⚠ The plan's Blockers name five points the design under-rules once written against the tree — `default_enabled` for a setting-conditional spawn, the pointer variable's gate, the N1 bearer's channel and arm switch, the `~/.aws`-grant severity, and done-condition 7 blocked on `bedrock-plumbing.md` — none of which blocks steps 1–3 | [`sso-backed-bedrock.md`](../design/sso-backed-bedrock.md) · [`sso-backed-bedrock-plan.md`](../design/sso-backed-bedrock-plan.md) |

## 🔒 Waiting

> [!IMPORTANT]
> **"A Mac" stopped being a blocker on 2026-09-14.** A self-hosted arm64 runner is registered and
> dispatches itself every five minutes on a new commit
> ([the runbook](runbooks/mac-actions-runner.md)), and `integration/applecontainer_test.go` runs
> five tests green on it. Four rows below still say "a Mac" — **they are now waiting on someone
> writing the test, not on hardware.** That is a much cheaper blocker, and two beliefs this repo
> held for months inverted the first time that hardware ran (apple/container#889 and #1089), so the
> expected value is high rather than confirmatory.

| | Blocked on | What clears it |
|---|---|---|
| [`OQ-WP5`](../design/workspace-path-mirroring.md#OQ-WP5) — deep bind destinations on Apple Container | a Mac; unmeasured since 2026-09-04 | one `container run` against a nested destination |
| `lsp_servers` on `macos-user` | a Mac; wired 2026-09-13, never run | `TestMacosUserDeclaredToolsArrive/lsp_servers` green |
| [`Q3`](../design/macos-user-build-step-threat-model.md#-q3--do-we-want-the-macos-nix-build-sandbox-on-for-yolo-triggered-builds) — the darwin nix build sandbox | a Mac | which packages assume an unsandboxed darwin build |
| Backend-parity's fourteen shipped macOS fixes | a Mac — **one exists now**: a self-hosted arm64 runner landed 2026-09-14 | four ran that day, all green (macOS 26.5, container 1.1.0): the jail starts, #39's machine-wide tier arrives, #44's pack tree is readable, and `:ro` is HONORED — which retires the premise under `roBindsUnsupported`. The rest are still unrun |
| [`OQ-WP9`](../design/workspace-path-mirroring.md#OQ-WP9) — a content-based venv oracle | a measurement | whether reading the recorded interpreter is cheap enough per launch |
| The `llamacpp` mode, end to end | a live `llama-server` — nothing in it has ever run against one | the per-agent smoke tests in [`packs/llamacpp/README.md`](../../packs/llamacpp/README.md). Two are load-bearing rather than confirmatory: **copilot** runs the anthropic endpoint against llama-server's `/v1/messages`, a pairing nobody has seen complete, and its result decides whether the pack should ship that endpoint at all; **codex** is a NEGATIVE test — `~/.codex/config.toml` must carry no `[model_providers.llamacpp]` and no selection, because a half-landed selection there is the bug |
| The image-copy lock, under contention | a ROOTLESS host | several jails launched at once against a cold store: one `image.layer_copy` span and N−1 `image.copy_lock` waits, reported with `podman info --format '{{.Host.Security.Rootless}}'`. A nested jail is rootful and structurally blind to it |

## 🧊 Icebox

| | The uncertainty | What would thaw it |
|---|---|---|
| [`boundary-broker.md`](../design/boundary-broker.md) — a generic approval broker | its own [§5](../design/boundary-broker.md#5-three-tiers-not-two--and-git-wants-the-middle-one) half-undercuts the premise: the motivating GitHub case probably wants a proxy, not a human | a concrete instance the current model blocks. One now exists and deliberately declined to build it: [`OQ-SSO6`](../design/sso-backed-bedrock.md#13-decision-ledger) ruled a lapsed AWS SSO session a message the jail reports, never a request it files, because half an approval mechanism inside a credential pack is the second front door this doc exists to prevent. The first consumer is identified and waiting rather than hypothetical |
| [`cache-relocation.md`](cache-relocation.md) — `yolo cache relocate` | all three questions are HELD by choice: what is undecided is whether we want the feature | ruling [`OQ-CR1`](cache-relocation.md#-oq-cr1--is-cache_relocations-the-right-level-held), which carries CR2 with it |

## What this file does not cover

**Candidate work nobody committed to.** [`further-roadmap-ideas.md`](further-roadmap-ideas.md#1-the-five-i-would-build)
carries five *build it* verdicts — a decision-link checker, a NOTHING-BUILT detector, an
orphaned-program report, one jail-blindness concept in `check`, and the reachability class in
CI. That file is a shelf by its own rule, and none of the five is a task until someone wants it.

**Live questions that block nothing** are listed under *Small calls* above rather than given
rows; they are real and cheap, and none of them is holding anything up.

**Questions with no id, invisible to the count command.** Whether the three `packages:`-declaring
tests should SKIP rather than fail on a builder-less runner
([`handoff-mac-unmeasured-claims.md` §4](handoff-mac-unmeasured-claims.md#4-the-nightly--five-links-all-now-named)).
Whether Go comments should cite a reference doc by named section, and what becomes of the 211
dangling citations that census found ([`doc-triage.md`](doc-triage.md#the-2026-09-12-graduation-assessment--the-five-docs-the-sprint-built)).
And the two that gate 💬 [row 23](#-needs-you): how a declared `jail_daemon.cmd` resolves on a
backend with no image, and whether that daemon runs under the Seatbelt profile — stated in
[`jail-daemon-on-macos-user-plan.md`](../design/jail-daemon-on-macos-user-plan.md)'s §Blockers,
which is the only place they exist. All four want an id before they want a row.

**Doc closures that are bookkeeping, not decisions.** Four docs graduate next, in a ruled order
([`doc-triage.md`](doc-triage.md#recommendation)); `report-tiers` went 2026-09-13 and left two
links to repoint. They are work, not questions.

*A question is promoted to a row the day it starts blocking something, and leaves the day it stops.*
