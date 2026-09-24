---
title: "Roadmap"
date: 2026-09-24
status: accepted
tags: [roadmap, routing, open-questions]
summary: "The routing table for open work: one line per decision, the doc that holds it, and what ruling it releases — ordered so the next hour is obvious."
vantage:
  status-chip: true
---

# Roadmap

**Status:** CURRENT — reconciled against the refreshed docs 2026-09-24. **Row counts are DERIVED,
never written here**: run the command below.

This file is a **routing table, not a place to think**: one line per open decision, naming
the doc that holds it and what a ruling releases. It is **not a record of what happened** —
work that closes leaves the file, and `git log -p` is the history. Every count here is
derived rather than carried forward; re-derive the live-question totals with check 2 of
[`README.md`](README.md#keeping-this-corpus-honest--the-five-checks-so-they-are-re-runnable):

```console
$ rg -c '^(#{2,4} |\s*[0-9]+[a-z]?\. |\s*[-*] )(<a id="[^"]*"></a> ?)?💬' docs/ --sort path
```

⚠ It scores this file 1 (the `## 💬 Needs you` heading, not a question), and it misses a question
written with 🔒, which no regex over `docs/` can see.

## Rule these first

**The ordering basis, so it is checkable:** a defect live in shipped code outranks blocked
build work, which outranks a ruling that only closes a doc; ties break toward the smallest
sitting. Every row below is the first class. **A row with an empty `Gate` is the bug**: file the
question in the doc that owns the model, then point the row at it.

| | Rule | Doc | Gate | Releases | Cost |
|---|---|---|---|---|---|
| **2** | Narrow `profile`'s env gate | [`bedrock-plumbing.md`](../design/bedrock-plumbing.md) | [`OQ-BR4`](../design/bedrock-plumbing.md#OQ-BR4) — rule it with [`OQ-BR3`](../design/bedrock-plumbing.md#OQ-BR3)/[`OQ-PS3`](../design/provider-switching.md#OQ-PS3) (one decision in two files) and [`OQ-BR8`](../design/bedrock-plumbing.md#OQ-BR8) (the same function, the other direction) | **Defect, live today.** `profileActive` (`internal/packload/packload.go`) matches a bin *any* selected pack installs in its wide pass, so one agent's profile fires another's env | one ruling |
| **3** | Clear the model id a deselect orphans | [`provider-switching.md`](../design/provider-switching.md) | [`OQ-PS2`](../design/provider-switching.md#OQ-PS2) | **Defect, live today.** `agentcfg.ApplySelection` only ever lifts, so a dropped profile leaves its model pinned. ⚠ A computed-layer tombstone deletes through every lower layer, so the proposed clear also removes a model set in the user's host file — the ruling has to say whether that is acceptable | one ruling |

## 💬 Needs you

| # | Decides | Doc | Live | Gate | Releases |
|---|---|---|---|---|---|
| 1 | Whether the backend census is worth building, and whether a `Warned` disposition can be suppressed | [`backend-parity.md`](../design/backend-parity.md) · in-review | 2 | [`OQ-BP-1`](../design/backend-parity.md#open-questions) | **build** — the census itself |
| 4 | A stated byte budget, or a policy | [`minimal-disk-footprint.md`](../design/minimal-disk-footprint.md) · in-review | 1 | [`OQ-DF4`](../design/minimal-disk-footprint.md#OQ-DF4) | **doc** — sweep `mise/` + `cache/staticcheck`, or declare them yours. The embedded-pack tree store is reclaimed only by `yolo prune --apply`, which is the same call |
| 5 | Provider deselection, aliases, and shipped model ids | [`provider-switching.md`](../design/provider-switching.md) · draft | 4 | [`OQ-PS2`](../design/provider-switching.md#OQ-PS2) | **defect** — the orphaned model pin ([Rule these first 3](#rule-these-first)). Which credentials a profile lets through is [row 44](#-needs-you) |
| 8 | Bedrock's schema marker, names, region, env gate, what a gate keys on — and every agent reaching every Bedrock model | [`bedrock-plumbing.md`](../design/bedrock-plumbing.md) · in-review | 13 | [`OQ-BR4`](../design/bedrock-plumbing.md#OQ-BR4) + [`OQ-BR8`](../design/bedrock-plumbing.md#OQ-BR8), ruled as one | **defect, both directions** — a profile gate fires for the wrong agent, and a second profile over `bedrock` silently drops every name-gated fact. Gates key on the profile NAME; derives key on the resolved provider. Then **build**: [`OQ-BR9`](../design/bedrock-plumbing.md#OQ-BR9)–[`OQ-BR15`](../design/bedrock-plumbing.md#OQ-BR15) (BR11 ruled: Claude gets native and everything profiles; BR10 answered by the whole-matrix requirement: the bridge signs) shape the all-models direction (one provider per endpoint family, the bridge signing with SigV4, a `models` kind for a company pack, picker rendering, currency); rule BR9 and BR10 first |
| 9 | Whether a mistyped reference refuses or only reports | [`reference-mismatch-diagnostics.md`](../design/reference-mismatch-diagnostics.md) · in-review | 4 | [`OQ-RM2`](../design/reference-mismatch-diagnostics.md#OQ-RM2) | **defect** — the diagnostic reaches bare stderr only |
| 10 | What the environment manager promises at each notch | [`environment-manager-user-stories.md`](../design/environment-manager-user-stories.md) · no frontmatter | 11 | Q8, [§Open Questions](../design/environment-manager-user-stories.md#open-questions) | **defect** — `internal/cli/briefing.txt` prints `YOLO JAIL — AGENT BRIEFING` at every notch |
| 11 | Skill and overlay collisions, and `host_files` modes | [`BACKLOG.md`](BACKLOG.md) Stage E · current | 7 | [`E2`](BACKLOG.md#-e2--readonly-as-a-real-ro-mount-instead-of-0o444) | **defect** — two silent last-one-wins paths |
| 12 | Who provisions a binary at each notch, and in what order | [`provisioner-sets.md`](../design/provisioner-sets.md) · in-review | 13 | [`OQ-PS1`](../design/provisioner-sets.md#OQ-PS1) | **build** — the host notch's provisioner. ⚠ `yolo host apply --assert`'s dependency gate already drives an install with the pack-first order [`OQ-PS4`](../design/provisioner-sets.md#decision-ledger) inverted, so [`OQ-PS2`](../design/provisioner-sets.md#decision-ledger)'s "sequenced last" needs re-ruling against it |
| 14 | The elevation-class batching of install offers that [`OQ-EM1`](../design/yolo-as-environment-manager.md#OQ-EM1) still owes | [`yolo-as-environment-manager.md`](../design/yolo-as-environment-manager.md) · no frontmatter | 1 | [`OQ-EM1`](../design/yolo-as-environment-manager.md#OQ-EM1) | **build** — batching offers by elevation class (sudo first); the one-prompt confirm-gated install already ships |
| 15 | How a dotted leaf in `packages:` resolves | [`package-nested-attribute-paths.md`](../design/package-nested-attribute-paths.md) · draft | 1 | [`OQ-1`](../design/package-nested-attribute-paths.md#OQ-1) | **build** — the resolver, or nothing |
| 16 | Whether program-delivery steps three and five survive | [`program-delivery.md`](../design/program-delivery.md) · in-review | 1 | [`OQ-PD19`](../design/program-delivery.md#-oq-pd19--do-steps-three-and-five-still-have-a-subject-after-the-agentproject-split) | **build** — or a retirement |
| 17 | How an official pack ships an executable of its own | [`broker-as-a-pack.md`](../design/broker-as-a-pack.md) · in-review | 2 | [`OQ-BP5`](../design/broker-as-a-pack.md#OQ-BP5) | **build** — `embed.FS` reads every file back as `0444`, so an embedded pack cannot carry an exec bit. ⚠ Both leanings ([`OQ-BP5`](../design/broker-as-a-pack.md#OQ-BP5), [`OQ-BP6`](../design/broker-as-a-pack.md#OQ-BP6)) lean on gates [`OQ-TP9`](../design/trust-paths.md#decision-ledger) deleted, so each needs restating before it is ruled |
| 18 | What replaces `macos-26-intel` before 26.05 lapses | [`macos-support-matrix.md`](../research/macos-support-matrix.md) · live tracker | 1 | [§5 item 8](../research/macos-support-matrix.md#5-roadmap-ordered) | **build** — a CI commitment, with a 2026 deadline |
| 19 | What an attach does with the attacher's pack set | [`agent-config-packs.md`](agent-config-packs.md) · no frontmatter | 3 | [`OQ-ACP1`](agent-config-packs.md#-oq-acp1--what-happens-when-two-people-attach-to-the-same-jail-with-different-pack-sets) | **defect** — `refreshJailBriefings` (`internal/cli/run/run.go`) re-renders on every invocation, attach included |
| 20 | Whether `yolo host apply` is a convenience or the path | [`host-render-target.md`](../design/host-render-target.md) · no frontmatter | 2 | 9.2, [§9](../design/host-render-target.md#9-open-questions--the-discussion-part) | **doc** — product posture; re-read 9.2's leaning once [`OQ-CO14`](../design/config-ownership-and-promotion.md#oq-co14) is ruled |
| 21 | Whether the jail mounts the workspace at the host's path | [`workspace-path-mirroring.md`](../design/workspace-path-mirroring.md) · draft | 10 | [`OQ-WP8`](../design/workspace-path-mirroring.md#OQ-WP8) | **doc** — ratify the no |
| 22 | Whether `macos-user`'s workspace root becomes configurable, and what the profile has to derive from it | [`configurable-workspace-root.md`](../design/configurable-workspace-root.md) · in-review | 4 | [`OQ-CW1`](../design/configurable-workspace-root.md#OQ-CW1) | **build** — the read-deny derived from the root, plus the whitelist that closes four lexical bypasses. ⚠ The whitelist wants to land before anyone relies on the current blacklist |
| 23 | How a `jail_daemon` runs on `macos-user` — argv resolution with no image, and whether it is confined | [`jail-daemon-on-macos-user-plan.md`](../design/jail-daemon-on-macos-user-plan.md) · accepted | 2 | [`OQ-DP8`](../design/declaration-parity.md#OQ-DP8) · [`OQ-DP9`](../design/declaration-parity.md#OQ-DP9) | **build** — the plan's steps 3–4, which gate 📦 [row 2](#-ready) and route (a) of [row 45](#-needs-you) |
| 24 | Whether a repo's own skills may reach whichever agent the reader chose | [`workspace-skills.md`](../design/workspace-skills.md) · in-review | 6 | [`OQ-WS1`](../design/workspace-skills.md#OQ-WS1) | **build** — or a close: a no makes the deliverable the documented convention in [§4.3](../design/workspace-skills.md#43-baseline-c--conventions-only). [`OQ-WS4`](../design/workspace-skills.md#OQ-WS4) also closes [`OQ-ACP2`](agent-config-packs.md#-oq-acp2--whether-opencodes-skills-gap-should-be-closed-by-writing-into-workspace) |
| 26 | Whether `mounts` and `env_sources` become user-scope-only, like every other key that grants host access | [`agent-safehouse.md`](../research/agent-safehouse.md) · in-review | 3 | [`OQ-AS3`](../research/agent-safehouse.md#OQ-AS3) | **defect-shaped** — neither key has a workspace-scope refusal in `internal/config`, so a repo-committed `yolo-jail.jsonc` can mount a host path and read a host dotenv. [`OQ-AS1`](../research/agent-safehouse.md#OQ-AS1)'s stated precondition (the Seatbelt suite green on macOS) is now met |
| 27 | Whether the hard-coded Kilo special-casing in two agent derives stays or goes | [`gateway-provider-packs.md`](../design/gateway-provider-packs.md) · accepted | 1 | [`OQ-GP4`](../design/gateway-provider-packs.md#OQ-GP4) | **graduation** — the only thing holding that doc back |
| 28 | How packs declare bypassed or unmanaged file diagnostics for yolo check | [`pack-declared-file-diagnostics.md`](../design/pack-declared-file-diagnostics.md) · in-review | 3 | [`OQ-1`](../design/pack-declared-file-diagnostics.md#oq-1) | **build** — declarative traps in `pack.json` or self-check |
| 29 | What replaces the spawn lock that retiring `scope: "host"` removes | [`host-daemon-ownership.md`](../design/host-daemon-ownership.md) · in-review | 4 | [`OQ-HD10`](../design/host-daemon-ownership.md#OQ-HD10) | **build** — the no-singleton retirement (`HD-R1`, ruled 2026-09-20, nothing built); building it also reverts three user-facing docs that describe the built tree |
| 30 | Whether a required jail daemon that cannot publish should refuse the launch with nothing to get past it | [`loopback-tls-reachability.md`](../reference/loopback-tls-reachability.md) · current | — (no id; neither gate's ruling covers the pair) | [`OQ-R2`](../reference/loopback-tls-reachability.md#oq-r2) | **defect-shaped, MEASURED on a host 2026-09-19** — `YOLO_ALLOW_UNREACHABLE_SERVICES=1` downgrades the reachability witness, but `startJailDaemonSupervisor` (`internal/entrypoint/runtime.go`) still refuses through `genStep`, which reads no hatch, so a jail whose required daemon cannot start gets no shell |
| 31 | What `--verbose` means past the host↔jail boundary: a level or a boolean, and whether a boot snapshot at a give-up is always-on | [`diagnostics-past-the-boundary.md`](../design/diagnostics-past-the-boundary.md) · in-review | 5 | [`OQ-DB1`](../design/diagnostics-past-the-boundary.md#oq-db1) · [`OQ-DB2`](../design/diagnostics-past-the-boundary.md#oq-db2) | **defect** — the `paths.VerboseEnv` disjunct in `internal/entrypoint/runtime.go`'s two lifecycle notices can never be true in a jail. Steps 1–2 of [§8](../design/diagnostics-past-the-boundary.md#8-what-i-would-build-in-order) are built |
| 35 | What a pack manifest is written in, once the model stops repeating itself | [`manifest-language.md`](../design/manifest-language.md) · in-review | 4 | [`OQ-M1`](../design/manifest-language.md#OQ-M1) | **doc** — its identity half is fixed by [`OQ-D5`](../design/slots-and-contributions.md#OQ-D5) |
| 36 | Whether the shipped launch refusal on legacy base-home bytes is the answer, and how re-seeding is stopped | [`base-home-legacy-state.md`](../design/base-home-legacy-state.md) · in-review | 8 | [`OQ-BH5`](../design/base-home-legacy-state.md#OQ-BH5) | **doc + build** — the refusal shipped 2026-09-21 against BH5's leaning, recorded only in code; ratify it (which supersedes [§5](../design/base-home-legacy-state.md#5-the-quarantine)'s apply machinery) or reopen it. The seed narrowing ([§11](../design/base-home-legacy-state.md#11-sequencing) step 3) is still unbuilt |
| 37 | How a fork you build yourself is distributed through a pack | [`forked-programs-as-packs.md`](../design/forked-programs-as-packs.md) · draft | 3 | [`OQ-FP7`](../design/forked-programs-as-packs.md#OQ-FP7) | **design** — the build-race lock, per-kind inheritance, and the eager slot on `macos-user` |
| 39 | Whether `exposes` can be BUILT — the migration window above all | [`slots-and-contributions.md`](../design/slots-and-contributions.md) · in-review | 7 | [`OQ-D6`](../design/slots-and-contributions.md#OQ-D6) | **build** — the split itself, and the pi rework it gates. ⚠ D6's evidence assumes a baked entrypoint, false since the image stopped containing yolo, so the window may be much narrower |
| 40 | Whether a workspace's own MCP config may override the canonical table, and whether yolo discloses the override | [`workspace-mcp-sources.md`](../design/workspace-mcp-sources.md) · in-review | 1 | [`OQ-WM1`](../design/workspace-mcp-sources.md#OQ-WM1) | **doc** — a ruling plus at most a disclosure line |
| 41 | Whether yolo may hold an editor preference — neovim is baked into every image and every backend | [`baked-editor-preference.md`](../design/baked-editor-preference.md) · draft | 4 | [`OQ-ED1`](../design/baked-editor-preference.md#OQ-ED1) | **design, and the maintainer's own request** |
| 42 | Whether the jail notch gets the readiness act the host already has | [`jail-notch-readiness.md`](../design/jail-notch-readiness.md) · draft | 3 | [`OQ-JR1`](../design/jail-notch-readiness.md#OQ-JR1) | **defect, live today** — `yolo -- true` exits 0 with no `via: npm` program installed, because the launcher installs on first invocation. The false message was fixed 2026-09-24 |
| 43 | What retiring `host_management: "assert"` does to a config, and a home, already on it | [`config-ownership-and-promotion.md`](../design/config-ownership-and-promotion.md) · in-review | 1 | [`OQ-CO14`](../design/config-ownership-and-promotion.md#oq-co14) | **build** — the retirement itself (ruled 2026-09-20, unbuilt), and the graduation of that doc. ⚠ `layerRetired`, the `YOLO_HOST_LAYERS` mark and `RetiredLayer` stay live under `own` ([§4.5.2](../design/config-ownership-and-promotion.md#452-what-the-ruling-does-not-delete--the-mechanism-tally-corrected)) |
| 44 | Which provider CREDENTIALS reach an agent, and how a profile narrows them | [`provider-credential-scope.md`](../design/provider-credential-scope.md) · draft | 5 | [`OQ-CN1`](../design/provider-credential-scope.md#OQ-CN1) | **defect, live today, MEASURED** — `writeUserEnvFile` writes every hydrated `env_sources` key with no profile gate, so `yolo -p zai -- pi` hands pi every other provider's credential |
| 45 | On `macos-user`, does Codex's refresh adapter come from a launch-owned listener (route b) or wait for native jail daemons (route a)? | [`openai-auth-broker.md`](../design/openai-auth-broker.md) · accepted | 2 | [`OQ-OA6`](../design/openai-auth-broker.md#OQ-OA6) | **build** — 📦 [row 1](#-ready), plan step 9. [`OQ-OA7`](../design/openai-auth-broker.md#OQ-OA7) (pi's ask-once-more after a 401) is a doc-only call beside it |
| 47 | Which models pi's CHILD agents may use under a non-Codex profile | [`pi-model-selection-ux.md`](../research/pi-model-selection-ux.md) · accepted research | 1 | [`OQ-PM1`](../research/pi-model-selection-ux.md#OQ-PM1) | **defect** — `packs/pi/derive.lua` emits `subagents` only for `openai-codex`, so child agents inherit the parent model and nothing rejects an out-of-provider per-run model |

**Rule together, or not at all.** [`E1`](BACKLOG.md#-e1--collapse-host_files-modes-43-copy-merges-into-readonly) · [`E2`](BACKLOG.md#-e2--readonly-as-a-real-ro-mount-instead-of-0o444) · [`OQ-B`](pack-host-management-plan.md#open-questions) are one asymmetry seen three times, and each doc says so.
[`OQ-BR3`](../design/bedrock-plumbing.md#OQ-BR3) and [`OQ-PS3`](../design/provider-switching.md#OQ-PS3) are the same decision in two files.
[`OQ-CN1`](../design/provider-credential-scope.md#OQ-CN1) · [`OQ-BR8`](../design/bedrock-plumbing.md#OQ-BR8) · [`OQ-9`](../design/agent-auth-modes.md#OQ-9) are one cluster, now facing three Bedrock credential routes by ruling ([`OQ-SSO7`](../design/sso-backed-bedrock.md#13-decision-ledger)).
[`OQ-PS6`](../design/provisioner-sets.md#OQ-PS6) · [`OQ-PS7`](../design/provisioner-sets.md#OQ-PS7) · [`OQ-PS12`](../design/provisioner-sets.md#OQ-PS12) together release [§9](../design/provisioner-sets.md#9-what-i-would-build-in-order) step 4, and nothing else does.
[`OQ-WP8`](../design/workspace-path-mirroring.md#OQ-WP8) closes [`OQ-WP1`](../design/workspace-path-mirroring.md#OQ-WP1) and [`OQ-WP6`](../design/workspace-path-mirroring.md#OQ-WP6) with it.

**Small calls that do not deserve a row**, each blocking nothing by its own doc's account:
[`CFP-1`–`CFP-3`](../design/composed-file-permissions.md#open-questions) (graduated; three postures — CFP-2's "when `macos-user` is next worked on" trigger has arrived) ·
[`OQ-LP5`](../design/loophole-packaging.md#oq-lp5) and [`OQ-LP7`](../design/loophole-packaging.md#oq-lp7) (cheap to rule, expensive to discover later) ·
[`SS-6`](../design/jail-state-separation-design.md#ss-6) (file an upstream mise issue; unverifiable from in here) ·
[`Q2`](../design/macos-user-build-step-threat-model.md#-q2--is---accept-flake-config-worth-the-substituter-poisoning-surface) (keep `--accept-flake-config`, or drop it) ·
[`OQ-B`](pack-host-management-plan.md#open-questions) (`0o444` at the host — explicitly taste) ·
[`OQ-ACP4`](agent-config-packs.md#-oq-acp4--whether-pruning-needs-usage-telemetry-to-be-anybodys-job) (pruning telemetry) ·
[`OQ-ST5`](../design/synced-skill-trees.md#OQ-ST5) (rule it once 📦 [row 11](#-ready) lands).
🤷 **Genuinely subjective, wherever they sit:** [`OQ-PS4`](../design/provider-switching.md#OQ-PS4) ·
[`OQ-RM4`](../design/reference-mismatch-diagnostics.md#OQ-RM4) ·
[`OQ-WP7`](../design/workspace-path-mirroring.md#OQ-WP7) ·
[`OQ-BP-3`](../design/backend-parity.md#open-questions) · `OQ-B`.

## 📦 Ready

Ruled, unblocked, and implementable cold — no memory of any conversation required.

| | Build | Why it is ready | Where |
|---|---|---|---|
| 1 | The OpenAI subscription service's remaining work | The Codex version floor is measured (0.56.0) and only needs recording in [`openai-subscription-auth.md`](../research/openai-subscription-auth.md). Plan step 9, the `macos-user` refresh consumer, waits on [`OQ-OA6`](../design/openai-auth-broker.md#OQ-OA6) (💬 [row 45](#-needs-you)); step 12's real-host and rootless checks have no automated twin | [`openai-auth-broker-plan.md`](../design/openai-auth-broker-plan.md) · [`openai-auth-broker.md`](../design/openai-auth-broker.md) |
| 2 | Start `jail_daemon` on `macos-user` | Steps 1–2 (the one composer and the by-name decline) shipped 2026-09-18. Step 5, retracting the prose the host half made false, landed by 2026-09-24. Steps 3–4 wait on 💬 [row 23](#-needs-you) | [`jail-daemon-on-macos-user-plan.md`](../design/jail-daemon-on-macos-user-plan.md) |
| 3 | Bedrock from a host `aws sso login`: steps 5, 7 and 8, and the live-host checks | Steps 1–4 and 6 are built and the endpoint variable is fixed, so the chain is wired in code; steps 5–7 wait on Blockers 3 and 6. Nothing has run against a live `aws sso login` | [`sso-backed-bedrock-plan.md`](../design/sso-backed-bedrock-plan.md#progress) · [`sso-backed-bedrock.md`](../design/sso-backed-bedrock.md) |
| 5 | Pi extensions: the refresh and concurrency tiers | All three ruled 2026-09-20; the storage tier shipped 2026-09-21 and `pi update --extensions` is confirmed, so what is left is the [§3.2](../design/pi-extension-lifecycle.md#32-execution-tier-pre-launch-auto-refresh) materializer and the [§3.3](../design/pi-extension-lifecycle.md#33-concurrency-tier-cross-jail-mutual-exclusion) lock | [`pi-extension-lifecycle.md`](../design/pi-extension-lifecycle.md) |
| 8 | Finish the Claude LSP plugin: pin its injection call site, and delete the LSP install recipes | Ruled ([`OQ-LSP1`](../reference/mcp-configuration.md#oq-lsp1)); the plugin shipped 2026-09-22. (1) Deleting the `jailcontent.SetLSPServers(...)` line in `internal/cli/run/prepare.go` leaves the unit gate green — a `run`-level test asserting the plugin lands in the staged skills tree closes it. (2) Delete `config.lspInstallRecipes`, `LSPInstalls` and `YOLO_LSP_NPM_INSTALL`/`YOLO_LSP_GO_INSTALL` | [`mcp-configuration.md`](../reference/mcp-configuration.md#how-the-plugin-is-rendered) · [unbuilt](../reference/mcp-configuration.md#unbuilt) |
| 9 | Small shipped-code defects, each fixable without a ruling | (1) `internal/cli/check/packs.go` keeps a pack only when `packload.LoadDir` returns no problems, so a pack `check` passes is refused at launch — [`pack-system.md`](../reference/pack-system.md#briefing-governance). (2) `math.random` is reachable from `derive.lua`; one name in `luahook.extraStrippedGlobals` plus a test — [`pack-system.md`](../reference/pack-system.md#derive-determinism). (3) The jail launch never prints the unmatched-audience report ([`R1`](../reference/agent-briefings.md#ba-r1)). (4) `discloseImplicitProviderForwards` has no call-site test in `run.Run` — [`wire-bridge.md`](../reference/wire-bridge.md#where-the-post-mortem-lives). (5) Two owed image guards — [`image-staging-vs-baking.md`](../reference/image-staging-vs-baking.md#the-layer-plan) | the linked reference sections |
| 11 | Build [`CO13`](../design/config-ownership-and-promotion.md#co13--how-a-derive-says-it-fills-a-computed-table-in-full--decided): a third derive sentinel that declares a computed table filled in full | **Decided 2026-09-20, unbuilt; a data-loss defect until it lands.** `dropComputedTables` (`internal/agentcfg/staterender.go`) drops every leaf of a partly-asserted computed table — claude's `env` — on an adopting render; `hostTableKeys` has the same over-claim at the host notch. The empty-table case is already fixed | [`config-ownership-and-promotion.md`](../design/config-ownership-and-promotion.md#632-the-three-classes-adoption-does-not-cover) |
| 12 | Layer-reusing image delivery on the two Mac backends: build the delta archive in `deliverViaArchive` for podman on macOS and Apple Container, then measure the first load and improve it where it measurably can be | All three questions ruled 2026-09-24 ([`OQ-LR1`](../research/macos-layer-reusing-image-delivery.md#OQ-LR1)–[`OQ-LR3`](../research/macos-layer-reusing-image-delivery.md#OQ-LR3)); the delta archive is built ([how it works](../reference/image-staging-vs-baking.md#the-delta-archive)), and the first-load work is not started. The transfer saving is MEASURED on Linux podman (a 27.8 MB delta archive where the full one is 3.45 GB) and SOURCED for Apple Container; the Mac-side numbers wait on a Mac run of the doc's commands | [`macos-layer-reusing-image-delivery.md`](../research/macos-layer-reusing-image-delivery.md#decisions) |

## 🔒 Waiting

> [!IMPORTANT]
> **"A Mac" stopped being a blocker on 2026-09-14.** A self-hosted arm64 runner is registered
> ([the runbook](runbooks/mac-actions-runner.md)), and `integration/applecontainer_test.go` runs
> green on it. The rows below that say "a Mac" are **waiting on someone writing the test, not on
> hardware.**

| | Blocked on | What clears it |
|---|---|---|
| Pack `briefing/` defaults, in a launched jail | per-agent routing of one pack's `briefing/` files has not run in a launched jail, and the integration suite has not run against the convention; the external `matt` pack still needs `git mv AGENTS.md briefing/house-rules.md` (its state INFERRED, it lives outside this repo) | a launch with a two-agent pack plus `just test`, and the `matt` pack's own commit — [`pack-system.md`](../reference/pack-system.md#briefing) |
| The wire-bridge 8214 collision fix, on the host that reported it | a run of `yolo -p kilo -- pi` against `3c20a5d8` on that host — UNMEASURED | one launch; [`wire-bridge.md`](../reference/wire-bridge.md#what-can-hold-the-listen-port-before-the-bridge-does) |
| Claude's generated LSP plugin, in a live session | a human: no `claude` session has run against a rendered `yolo-lsp` plugin, and the no-agent-tests rule keeps it out of CI | one interactive session with `lsp_servers` set — [`mcp-configuration.md`](../reference/mcp-configuration.md#lsp-claudes-route-is-a-generated-plugin) |
| The darwin integration warmup | a fresh macOS nightly measurement: `warmJail` skips darwin on 2026-08-22/23 numbers whose premise has since changed, so [Mode B](../reference/agent-install-in-ci.md#mode-b) is unfixed on macOS | re-enable the warmup or keep the skip on one new nightly — [`agent-install-in-ci.md`](../reference/agent-install-in-ci.md#suite-warmup) |
| Archive delivery on macOS, at the launch call site | a Mac: `deliverViaArchive`, which now sends a [delta archive](../reference/image-staging-vs-baking.md#the-delta-archive), has not run in a real launch on podman/macOS or Apple Container, which is [`OQ-LI2`](../reference/image-staging-vs-baking.md#why-its-this-way)'s outstanding precondition. The podman half is measured on Linux against a remote client; the Apple Container half is UNMEASURED. No job builds `.#imageCopier` on x86_64-darwin | two launches per backend (a first load, then one after a `packages:` change), the research's Apple Container commands, and one darwin `nix build .#imageCopier` — [`image-staging-vs-baking.md`](../reference/image-staging-vs-baking.md#archive-destinations) |
| Bedrock mode with real AWS credentials | `CLAUDE_CODE_USE_BEDROCK` through the `claude/settings` env block has never been re-tested with real credentials (see also 📦 Ready row 3) | one Bedrock-mode launch — [`providers.md`](../reference/providers.md#two-channels-split-by-payload-type) |
| [`OQ-WP5`](../design/workspace-path-mirroring.md#OQ-WP5) — deep bind destinations on Apple Container | a Mac; unmeasured since 2026-09-04 | one `container run` against a nested destination |
| [`Q3`](../design/macos-user-build-step-threat-model.md#-q3--do-we-want-the-macos-nix-build-sandbox-on-for-yolo-triggered-builds) — the darwin nix build sandbox | a Mac; and since 2026-09-24 the agent can trigger host-daemon builds itself (the doc's Vector C), which a yolo argv cannot reach | which packages assume an unsandboxed darwin build, and whether Q3 covers agent-triggered builds |
| Backend-parity's fourteen shipped macOS fixes | a Mac — the runner exists; four ran green 2026-09-14 | the rest are still unrun |
| [`OQ-WP9`](../design/workspace-path-mirroring.md#OQ-WP9) — a content-based venv oracle | a measurement | whether reading the recorded interpreter is cheap enough per launch |
| The `llamacpp` mode, end to end | a live `llama-server` — nothing in it has ever run against one | the per-agent smoke tests in [`packs/llamacpp/README.md`](../../packs/llamacpp/README.md); copilot's and codex's are load-bearing rather than confirmatory |
| The image-copy lock, under contention | a ROOTLESS host | several jails launched at once against a cold store: one `image.layer_copy` span and N−1 `image.copy_lock` waits, reported with `podman info --format '{{.Host.Security.Rootless}}'`. A nested jail is rootful and structurally blind to it |

## 🧊 Icebox

| | The uncertainty | What would thaw it |
|---|---|---|
| [`boundary-broker.md`](../design/boundary-broker.md) — a generic approval broker | its own [§5](../design/boundary-broker.md#5-three-tiers-not-two--and-git-wants-the-middle-one) half-undercuts the premise: the motivating GitHub case probably wants a proxy, not a human | a concrete instance the current model blocks. [`OQ-SSO6`](../design/sso-backed-bedrock.md#13-decision-ledger) is the first identified consumer, and it declined to build one |
| [`cache-relocation.md`](cache-relocation.md) — `yolo cache relocate` | all three questions are HELD by choice: what is undecided is whether we want the feature | ruling [`OQ-CR1`](cache-relocation.md#-oq-cr1--is-cache_relocations-the-right-level-held), which carries CR2 with it |
| [G21](setup-support-gaps.md#2-ranked-gap-backlog) — in-jail nix on the container Macs | none technical: **ruled 2026-09-24 possible, but not planned for now**; a route is measured in [G21/G22's sketch](setup-support-gaps.md#2-ranked-gap-backlog) | a user who needs `nix` inside a container-Mac jail and for whom `packages:`, mise and the `macos-user` backend all fall short |

## What this file does not cover

**Candidate work nobody committed to.** [`further-roadmap-ideas.md`](further-roadmap-ideas.md#1-the-five-i-would-build)
carries five *build it* verdicts. That file is a shelf by its own rule, and none of the five is a
task until someone wants it.

**Live questions that block nothing** are listed under *Small calls* above rather than given
rows; they are real and cheap, and none of them is holding anything up.

**Questions with no id, invisible to the count command.** Each wants an id before it wants a row.

- The safe subset of `packs` a workspace may declare — [`OQ-MP7`](../design/mcp-presets-removal.md#OQ-MP7)'s consequence, which **blocks** [`mcp-presets-removal.md`](../design/mcp-presets-removal.md)'s [§13](../design/mcp-presets-removal.md#13-what-i-would-build-in-order) steps 1–3 (that doc has no open question of its own).
- Whether [`OQ-LS1`](../reference/image-retention.md#why-its-this-way)'s "costs a rebuild, never a running jail" holds on podman/Linux, where the host store is bound over the jail's — [`storage-lifecycle.md`](storage-lifecycle.md#decision-ledger). Read from code, unmeasured, defect-shaped.
- Whether a pack may pin an agent CLI (`packs/omp` does) against P6's "Pin: none" — [`program-delivery.md`](../design/program-delivery.md#35-the-second-axis-who-the-dependency-serves-amendment-2026-09-03).
- Whether [§8](../design/diagnostics-past-the-boundary.md#8-what-i-would-build-in-order) step 1's port-skip report stays on the terminal (`e.warn`) or moves to `boot.log`.
- Whether `awschain`'s exclusivity refusal also covers a static AWS key pair beside the pointer — [`bedrock-plumbing.md` §6.5](../design/bedrock-plumbing.md#65-the-credential-three-are-supported).
- Two `config-list` tails: entries yolo inserted stay in the host file when both owner and contributor drop, and promote does not lift list captures — [`additive-config-lists.md`](../design/additive-config-lists.md).
- Whether the three `packages:`-declaring tests should SKIP rather than fail on a builder-less runner ([`handoff-mac-unmeasured-claims.md` §4](handoff-mac-unmeasured-claims.md#4-the-nightly--five-links-all-now-named)).
- Whether Go comments should cite a reference doc by named section, and what becomes of the dangling citations that census found ([`doc-triage.md`](doc-triage.md#the-2026-09-12-graduation-assessment--the-five-docs-the-sprint-built)).
- Whether a bare `-p <name>` that selects NOTHING should say so, stated only at `checkProfileTargets`' docstring in [`packs.go`](../../internal/cli/run/packs.go).
- Whether the macOS nightly should run any vendor agent install at all ([`agent-install-in-ci.md`](../reference/agent-install-in-ci.md#what-runs-on-macos)).

**Doc closures that are bookkeeping, not decisions.** Docs graduate in a ruled order
([`doc-triage.md`](doc-triage.md#recommendation)). BUILT docs with zero live questions waiting to
join it: [`agent-program-runtimes.md`](../design/agent-program-runtimes.md) with its
[plan](../design/agent-program-runtimes-plan.md),
[`additive-config-lists.md`](../design/additive-config-lists.md) with its
[plan](../design/additive-config-lists-plan.md),
[`gateway-provider-packs-plan.md`](../design/gateway-provider-packs-plan.md) (its design is 💬 row 27),
and [`doc-triage.md`](doc-triage.md) itself, better retired than graduated. They are work, not
questions.

*A question is promoted to a row the day it starts blocking something, and leaves the day it stops.*
