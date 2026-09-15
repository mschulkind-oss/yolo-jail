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

**Status:** CURRENT — 2026-09-15. **44 rows**: 4 to rule first, 19 needing a decision,
13 ready to build, 6 waiting, 2 iced.

This file is a **routing table, not a place to think**: one line per open decision, naming
the doc that holds it and what a ruling releases. It is **not a record of what happened** —
work that closes leaves the file, and `git log -p` is the history. Every count here is
derived rather than carried forward; re-derive the live-question totals with check 2 of
[`README.md`](README.md#keeping-this-corpus-honest--the-five-checks-so-they-are-re-runnable):

```console
$ rg -c '^(#{2,4} |\s*[0-9]+[a-z]?\. |\s*[-*] )(<a id="[^"]*"></a> ?)?💬' docs/ --sort path
```

**109 live questions across 29 docs**, plus one 🔒 that command cannot see.

## Rule these first

**The ordering basis, so it is checkable:** a defect live in shipped code outranks blocked
build work, which outranks a ruling that only closes a doc; ties break toward the smallest
sitting. All four below are the first class.

| | Rule | Releases | Cost |
|---|---|---|---|
| **1** | [`OQ-2`](../design/cerebras-pack-and-copilot-delivery.md#oq-2) — gate `ANTHROPIC_AUTH_TOKEN` on a declared anthropic endpoint | **Defect, live today.** `packs/claude/derive.lua:61` emits the key from a branch that never reads the `routed` flag set at `:58` | one ruling, three provider shapes |
| **2** | [`OQ-3`](../design/broker-ca-and-nested-hosts.md#7-open-questions) — a `[SKIP]` level for `yolo check` | **Defect, live today.** `internal/cli/check/sections_loopholes.go:23` calls `r.ok` on a section it skipped, and `reporter.go:83` counts it as a pass — ten sites | one ruling, ~10 lines |
| **3** | [`OQ-BR4`](../design/bedrock-plumbing.md#OQ-BR4) — narrow `profile`'s env gate | **Defect, live today.** `internal/packload/packload.go:634` matches a bin *any* selected pack installs, so one agent's profile fires another's env | one ruling |
| **4** | [`OQ-PS2`](../design/provider-switching.md#OQ-PS2) — clear the model id a deselect orphans | **Defect, live today.** `internal/agentcfg/selection.go:168` only ever lifts, so a dropped profile leaves its model pinned | one ruling |

## 💬 Needs you

| # | Decides | Doc | Live | Gate | Releases |
|---|---|---|---|---|---|
| 1 | Which uid may read a loophole endpoint file, once the jail is not root in a container | [`backend-parity.md`](../design/backend-parity.md) · in-review | 3 | [`OQ-BP-5`](../design/backend-parity.md#OQ-BP-5) | **build** — the last thing between OQ-BP-4's ruling and loopholes on macos-user |
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
| 17 | How a pack ships a binary of its own | [`broker-as-a-pack.md`](../design/broker-as-a-pack.md) · no frontmatter | 2 | [`OQ-BP5`](../design/broker-as-a-pack.md#OQ-BP5) | **build** — the first such pack |
| 18 | What replaces `macos-26-intel` before 26.05 lapses | [`macos-support-matrix.md`](../research/macos-support-matrix.md) · live tracker | 1 | [§5 item 8](../research/macos-support-matrix.md#5-roadmap-ordered) | **build** — a CI commitment, with a 2026 deadline |
| 19 | What an attach does with the attacher's pack set | [`agent-config-packs.md`](agent-config-packs.md) · overtaken | 4 | [`OQ-ACP3`](agent-config-packs.md#-oq-acp3--whether-the-prism-should-become-a-standalone-tool-that-also-manages-host-configs) | **defect** — `internal/cli/run/run.go:766` re-renders before the attach branch at `:774` |
| 20 | Whether `yolo host apply` is a convenience or the path | [`host-render-target.md`](../design/host-render-target.md) · no frontmatter | 3 | 9.2, [§9](../design/host-render-target.md#9-open-questions--the-discussion-part) | **doc** — product posture; [§8](../design/host-render-target.md#8-what-i-would-actually-do-in-order) step 3 is in 📦 regardless |
| 21 | Whether the jail mounts the workspace at the host's path | [`workspace-path-mirroring.md`](../design/workspace-path-mirroring.md) · draft | 12 | [`OQ-WP8`](../design/workspace-path-mirroring.md#OQ-WP8) | **doc** — ratify the no |

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
| | local inference as a MODE — recipe and source together | all six questions ruled 2026-09-14: a mode not a parallel key, `requires_env` gating, user scope, no `forward_host_ports` gate needed, **both halves at once**, and a two-writers collision is FATAL | [`local-model-endpoints.md`](../research/local-model-endpoints.md) — ⚠ carries one manual smoke test per agent; nothing in it has run against a live server |
| | 💬 **Loopholes on macos-user** — blocked by [`OQ-BP-5`](../design/backend-parity.md#OQ-BP-5) | same ruling, and it needs **no reachability measurement**: `sharesLauncherNetns` already returns true for it, the jail is a native process so `127.0.0.1` is the host loopback, and Seatbelt is `(allow default)`. ⚠ Its blocker is the **credential boundary**: `svcendpoint.Publish` writes the endpoint file 0600 and `DialLocal` rests on *"the same uid that published it"* — but the jail runs as `_yolojail`, a different uid, with no mount to reshape. Widen the mode, add a group, or copy per jail: that is a decision about a bearer token, not a hoist | `loopholeinert.go:69`, `svcendpoint/endpointfile.go:114` |
| 1 | Codex reads `env_key`; the derive writes `api_key_env` | ruled *"ship it alone"*; every custom codex provider is credential-less until it lands | `packs/codex/derive.lua:120` · [§12](../design/bedrock-plumbing.md#12-what-i-would-build-in-order) |
| 2 | Enforce `required_capabilities`, or stop exporting it | ruled a fatal refusal; `internal/cli/run/assemble.go:884` exports `YOLO_REQUIRED_CAPABILITIES` and nothing reads it | [`OQ-CAP2`](../design/agent-auth-modes.md#12-decision-ledger) |
| 3 | Give the `macos-user` backend the pipeline's writers | it prints to `os.Stdout`, so its disclosures never reach `launch.log` | [§7](handoff-macos-user-open-threads.md#7-nothing-the-macos-user-backend-prints-reaches-launchlog) |
| 4 | Make `yolo host apply` report `packages:` as `describe` does | two shipped commands disagree; `internal/cli/apply.go` has no `packages` path at all | [§9 step 3](../design/provisioner-sets.md#9-what-i-would-build-in-order) |
| 5 | Collapse the two render paths into `internal/render` | the load-bearing step, ruled and half-done: five `agentcfg.Compose*` call sites still split across two packages | [§8 step 3](../design/host-render-target.md#8-what-i-would-actually-do-in-order) |
| 6 | Retire the read-in `host` layer | ruled 2026-08-01; `internal/agentcfg/manifest/manifest.go:140` still declares it | [§1](../design/environment-manager-user-stories.md#1-maya--staff-engineer-rust-cli-wants-a-guarantee-not-a-diff) |
| 7 | Give plugin claims their own disclosure class | ruled 2026-09-14 ([`OQ-TP10`](../design/trust-paths.md#decision-ledger)) **with its rendering already decided** — `report-tiers.md` P5/P6/P1 make it ONE counted line per pack, detail to `boot.log`, and P4 forbids gating it; `internal/cli/run/packloopholes.go:108` classes `KindSkills` `disclosureSkip` today | [`trust-paths.md`](../design/trust-paths.md#decision-ledger) · [the launch stream](../reference/report-tiers.md#the-launch-stream) |
| 8 | Prove a pack-shipped jail binary with a throwaway hello pack | ruled *"do this even if…"*; the path has never executed | [§10](../design/broker-as-a-pack.md#10-sequencing) |
| 9 | Serialise image copies across launches, with a machine-wide *image-copy* lock (a host flock, **not** the housekeeping lock) around re-inspecting the ref and then copying | no ruling needed. Layer-aware delivery skips only the layers already committed when a copy starts, and nothing serialises copies across workspaces (`internal/image/autoload.go:772`). Measured 2026-09-14: a reboot launched 11 jails, **5 copied one identical 3.45 GB image at once**, and each spent ~4 min in the store write. The comment at `:594` says a lock across the load would *"buy nothing"*, which was true only of the deleted stream; rewrite it in the same change | `internal/image/autoload.go:594` · test: a stubbed `LayerCopy`, two concurrent loads of one ref → exactly one copy. A nested jail is rootful and cannot verify this; use a rootless host |
| 10 | Finish the OpenAI subscription credential service on `macos-user` and Apple Container, add trusted host-only import/logout, then run the real-host expiry and browser checks | the canonical transaction, podman transport, Codex and Pi adapters, managed host launches, browser login, status and self-check are implemented; nested verification cannot establish rootless reachability or either macOS backend | [`openai-auth-broker.md`](../design/openai-auth-broker.md) · [`openai-auth-broker-plan.md`](../design/openai-auth-broker-plan.md) |
| 11 | Add OMP and the maximal-fidelity Codex-Claude bridge | all three rulings landed: use OMP's pinned official package-manager distribution, map every current Claude feature Codex Responses can represent, and default the profile to `terra` | [`omp-and-codex-claude-profile.md`](../design/omp-and-codex-claude-profile.md) · [`omp-and-codex-claude-profile-plan.md`](../design/omp-and-codex-claude-profile-plan.md) |

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
| [`OQ-BP-4`](../design/backend-parity.md#open-questions)'s *verification* half | ~~a Mac~~ — **one exists since 2026-09-14** | whether an AC container reaches a host loopback listener. Add it to `integration/applecontainer_test.go`, which now runs five tests green on that hardware; the ruling is row 2 above and is not blocked |
| `lsp_servers` on `macos-user` | a Mac; wired 2026-09-13, never run | `TestMacosUserDeclaredToolsArrive/lsp_servers` green |
| [`Q3`](../design/macos-user-build-step-threat-model.md#-q3--do-we-want-the-macos-nix-build-sandbox-on-for-yolo-triggered-builds) — the darwin nix build sandbox | a Mac | which packages assume an unsandboxed darwin build |
| Backend-parity's fourteen shipped macOS fixes | a Mac — **one exists now**: a self-hosted arm64 runner landed 2026-09-14 | four ran that day, all green (macOS 26.5, container 1.1.0): the jail starts, #39's machine-wide tier arrives, #44's pack tree is readable, and `:ro` is HONORED — which retires the premise under `roBindsUnsupported`. The rest are still unrun |
| [`OQ-WP9`](../design/workspace-path-mirroring.md#OQ-WP9) — a content-based venv oracle | a measurement | whether reading the recorded interpreter is cheap enough per launch |

## 🧊 Icebox

| | The uncertainty | What would thaw it |
|---|---|---|
| [`boundary-broker.md`](../design/boundary-broker.md) — a generic approval broker | its own [§5](../design/boundary-broker.md#5-three-tiers-not-two--and-git-wants-the-middle-one) half-undercuts the premise: the motivating GitHub case probably wants a proxy, not a human | a concrete instance the current model blocks |
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
Both want an id before they want a row.

**Doc closures that are bookkeeping, not decisions.** Four docs graduate next, in a ruled order
([`doc-triage.md`](doc-triage.md#recommendation)); `report-tiers` went 2026-09-13 and left two
links to repoint. They are work, not questions.

*A question is promoted to a row the day it starts blocking something, and leaves the day it stops.*
