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

**Status:** CURRENT — 2026-09-17. **44 rows**: 5 to rule first, 23 needing a decision,
6 ready to build, 8 waiting, 2 iced.

This file is a **routing table, not a place to think**: one line per open decision, naming
the doc that holds it and what a ruling releases. It is **not a record of what happened** —
work that closes leaves the file, and `git log -p` is the history. Every count here is
derived rather than carried forward; re-derive the live-question totals with check 2 of
[`README.md`](README.md#keeping-this-corpus-honest--the-five-checks-so-they-are-re-runnable):

```console
$ rg -c '^(#{2,4} |\s*[0-9]+[a-z]?\. |\s*[-*] )(<a id="[^"]*"></a> ?)?💬' docs/ --sort path
```

**123 live questions across 34 docs**, plus one 🔒 that command cannot see.

## Rule these first

**The ordering basis, so it is checkable:** a defect live in shipped code outranks blocked
build work, which outranks a ruling that only closes a doc; ties break toward the smallest
sitting. All five below are the first class.

| | Rule | Releases | Cost |
|---|---|---|---|
| **1** | [`OQ-2`](../design/cerebras-pack-and-copilot-delivery.md#oq-2) — gate `ANTHROPIC_AUTH_TOKEN` on a declared anthropic endpoint | **Defect, live today, re-verified 2026-09-17.** In `packs/claude/derive.lua`, `if p.api_key then out.ANTHROPIC_AUTH_TOKEN = p.api_key` sits in the branch BEFORE the `elseif routed`, so a provider carrying a key but declaring no anthropic endpoint still gets the key emitted. (Line numbers removed from this cell on purpose: they were already stale and moved again the same day.) | one ruling, three provider shapes |
| **2** | [`OQ-3`](../design/broker-ca-and-nested-hosts.md#7-open-questions) — a `[SKIP]` level for `yolo check` | **Defect, live today.** `internal/cli/check/sections_loopholes.go:23` calls `r.ok` on a section it skipped, and `reporter.go:83` counts it as a pass — ten sites | one ruling, ~10 lines |
| **3** | [`OQ-BR4`](../design/bedrock-plumbing.md#OQ-BR4) — narrow `profile`'s env gate | **Defect, live today.** `internal/packload/packload.go:634` matches a bin *any* selected pack installs, so one agent's profile fires another's env | one ruling |
| **4** | [`OQ-PS2`](../design/provider-switching.md#OQ-PS2) — clear the model id a deselect orphans | **Defect, live today.** `internal/agentcfg/selection.go:168` only ever lifts, so a dropped profile leaves its model pinned | one ruling |
| **5** | [`OQ-CR3`](../design/config-target-resolution.md#oq-cr3) — which store `yolo config diff` reads | **Defect, live on any `host_management: own` home.** `reset` resolves its store through `render.Target`; `diff` and `ls` call `prismOverlayPath` unconditionally, so they report no divergence and `reset` then discards an edit the user was never shown (MEASURED 2026-09-16) | one ruling; the other five in that doc can wait |

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
| 17 | How a pack ships a binary of its own | [`broker-as-a-pack.md`](../design/broker-as-a-pack.md) · no frontmatter | 2 | [`OQ-BP5`](../design/broker-as-a-pack.md#OQ-BP5) | **build** — and the question narrowed on 2026-09-17, when the mechanism was actually run (`packs/hello-daemon`). Declaration is fine and needs no schema change; DELIVERY is half-broken, and not fixably: an EMBEDDED pack cannot carry an exec bit at all, because `embed.FS` reads every file back as `0444`. So this now decides how an OFFICIAL pack ships a program, given that the route §3.1 rated ✅ works only for a pack configured by path |
| 18 | What replaces `macos-26-intel` before 26.05 lapses | [`macos-support-matrix.md`](../research/macos-support-matrix.md) · live tracker | 1 | [§5 item 8](../research/macos-support-matrix.md#5-roadmap-ordered) | **build** — a CI commitment, with a 2026 deadline |
| 19 | What an attach does with the attacher's pack set | [`agent-config-packs.md`](agent-config-packs.md) · overtaken | 4 | [`OQ-ACP3`](agent-config-packs.md#-oq-acp3--whether-the-prism-should-become-a-standalone-tool-that-also-manages-host-configs) | **defect** — `internal/cli/run/run.go:766` re-renders before the attach branch at `:774` |
| 20 | Whether `yolo host apply` is a convenience or the path | [`host-render-target.md`](../design/host-render-target.md) · no frontmatter | 3 | 9.2, [§9](../design/host-render-target.md#9-open-questions--the-discussion-part) | **doc** — product posture; [§8](../design/host-render-target.md#8-what-i-would-actually-do-in-order) step 3 is in 📦 regardless |
| 21 | Whether the jail mounts the workspace at the host's path | [`workspace-path-mirroring.md`](../design/workspace-path-mirroring.md) · draft | 12 | [`OQ-WP8`](../design/workspace-path-mirroring.md#OQ-WP8) | **doc** — ratify the no |
| 22 | Whether `macos-user`'s workspace root becomes configurable, and what the profile has to derive from it | [`configurable-workspace-root.md`](../design/configurable-workspace-root.md) · in-review | 4 | [`OQ-CW1`](../design/configurable-workspace-root.md#OQ-CW1) | **build** — the read-deny derived from the root, plus the whitelist that closes four lexical bypasses. ⚠ The whitelist wants to land before anyone relies on the current blacklist; withdrawing an accepted path later is a breaking change |
| 22 | Which home a `yolo config` verb is about, and whether it says so | [`config-target-resolution.md`](../design/config-target-resolution.md) · draft | 6 | [`OQ-CR1`](../design/config-target-resolution.md#oq-cr1) | **defect** — four measured and all silent: one report describes two homes, a `cd` changes the answer at rc 0, an owned host's `diff` and `reset` disagree ([row 5](#rule-these-first)), and `render` previews with the wrong home's `host` layer |
| 23 | How an `aws sso login` becomes Bedrock-only access inside a jail, and what refreshes it | [`sso-backed-bedrock.md`](../design/sso-backed-bedrock.md) · draft | 1 | [`OQ-SSO6`](../design/sso-backed-bedrock.md#OQ-SSO6) | **build** — five of six ruled 2026-09-17 ([Decision Ledger](../design/sso-backed-bedrock.md#14-decision-ledger)). The last one asks only whether a lapsed session may *request* a host login rather than report one, and blocks nothing: [`boundary-broker.md`](../design/boundary-broker.md) owns that queue |
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
| 1 | Finish the OpenAI subscription credential service on `macos-user` and Apple Container, add trusted host-only import/logout, then run the real-host expiry and browser checks | the canonical transaction, podman transport, Codex and Pi adapters, managed host launches, browser login, status and self-check are implemented; nested verification cannot establish rootless reachability or either macOS backend | [`openai-auth-broker.md`](../design/openai-auth-broker.md) · [`openai-auth-broker-plan.md`](../design/openai-auth-broker-plan.md) |
| 2 | Stop the daemon supervisor swallowing a spawn failure | no ruling needed, and it is sharper than it looks: `supervisor.superviseOne` binds the error from `c.start()` and never reads it, then sleeps, doubles a 1s→30s backoff and retries for the life of the jail — while `openLog` has already created an empty `<name>.log`. So a `restart: "no"` daemon is honored when it EXITS and looped forever when it fails to SPAWN, and the observable symptom of any spawn fault is an empty log and no process. Found by the hello-daemon experiment, and it swallows every spawn failure, not that one | `internal/supervisor/supervisor.go` |
| 3 | Give `packs/pi/derive.lua` a `compat` block for loopback endpoints | measured 2026-09-17: pi's auto-detection for an unknown loopback URL yields the INVERSE of pi's own llama.cpp provider flags (`supportsStore`/`DeveloperRole`/`ReasoningEffort`/`StrictMode`, and `max_completion_tokens`). Nothing else blocks the `llamacpp` pack's pi smoke test | `packs/pi/derive.lua` |
| 4 | Run the capability gate in `yolo check` | the gate shipped 2026-09-17 in the run pre-flight only, so a config `yolo check` calls clean can still be refused at launch — which is the one thing `check` exists to prevent | `internal/cli/check` |
| 5 | Start `jail_daemon` on `macos-user` | only the HOST half of that backend's loophole lifecycle was generalised 2026-09-17: an endpoint is delivered and ACL-granted with no in-jail consumer, because the wiring lives in `loopholes.RuntimeArgsFor`'s `YOLO_JAIL_DAEMONS`, emitted into a container argv only, and nothing in `internal/macosuser` reads `JailDaemon` | `internal/macosuser`, the darwin bootstrap |
| 6 | Strip `provides` from MCP entries before they reach an agent's config file | pre-existing and now load-bearing: `~/.claude.json` carries a key Claude Code does not know, and `provides` became a real input on 2026-09-17 rather than an inert annotation | the MCP projection |

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
| The `llamacpp` mode, end to end | a live `llama-server` — nothing in it has ever run against one | the per-agent smoke tests in [`packs/llamacpp/README.md`](../../packs/llamacpp/README.md). Two are load-bearing rather than confirmatory: **copilot** runs the anthropic endpoint against llama-server's `/v1/messages`, a pairing nobody has seen complete, and its result decides whether the pack should ship that endpoint at all; **codex** is a NEGATIVE test — `~/.codex/config.toml` must carry no `[model_providers.llamacpp]` and no selection, because a half-landed selection there is the bug |
| The image-copy lock, under contention | a ROOTLESS host | several jails launched at once against a cold store: one `image.layer_copy` span and N−1 `image.copy_lock` waits, reported with `podman info --format '{{.Host.Security.Rootless}}'`. A nested jail is rootful and structurally blind to it |

## 🧊 Icebox

| | The uncertainty | What would thaw it |
|---|---|---|
| [`boundary-broker.md`](../design/boundary-broker.md) — a generic approval broker | its own [§5](../design/boundary-broker.md#5-three-tiers-not-two--and-git-wants-the-middle-one) half-undercuts the premise: the motivating GitHub case probably wants a proxy, not a human | a concrete instance the current model blocks — [`OQ-SSO6`](../design/sso-backed-bedrock.md#OQ-SSO6) is the first candidate: a lapsed AWS SSO session that the jail can only report, where a request-and-approve tier would let it ask |
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
