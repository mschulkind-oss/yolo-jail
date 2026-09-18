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

**Status:** CURRENT — 2026-09-18. **45 rows**: 4 to rule first, 25 needing a decision,
7 ready to build, 7 waiting, 2 iced. Three 📦 rows CLOSED on 2026-09-18 and left the file
(`eb02ad86`, `67cf4c81`, `f5c26899`): the supervisor's swallowed spawn failure, `provides`
leaking into four agents' config files, and the capability gate `yolo check` never ran.

This file is a **routing table, not a place to think**: one line per open decision, naming
the doc that holds it and what a ruling releases. It is **not a record of what happened** —
work that closes leaves the file, and `git log -p` is the history. Every count here is
derived rather than carried forward; re-derive the live-question totals with check 2 of
[`README.md`](README.md#keeping-this-corpus-honest--the-five-checks-so-they-are-re-runnable):

```console
$ rg -c '^(#{2,4} |\s*[0-9]+[a-z]?\. |\s*[-*] )(<a id="[^"]*"></a> ?)?💬' docs/ --sort path
```

**128 live questions across 35 docs**, plus one 🔒 that command cannot see.

## Rule these first

**The ordering basis, so it is checkable:** a defect live in shipped code outranks blocked
build work, which outranks a ruling that only closes a doc; ties break toward the smallest
sitting. All four below are the first class.

| | Rule | Releases | Cost |
|---|---|---|---|
| **1** | [`OQ-2`](../design/cerebras-pack-and-copilot-delivery.md#oq-2) — take the one-line credential fix now, or wait for the resolver | **Defect, live today, re-verified 2026-09-18.** In `packs/claude/derive.lua`, `if p.api_key then out.ANTHROPIC_AUTH_TOKEN = p.api_key` fires whether or not a base URL was composed, so a provider declaring a non-anthropic protocol and a key sends it to `api.anthropic.com`. The inverse is already handled (`elseif routed` substitutes a dummy token), so this is the missing half of a rule that file keeps. **The DESIGN question moved** to [`protocol-resolution.md`](../design/protocol-resolution.md) and was ruled 2026-09-18; what is left here is sequencing, and its step 3 deletes the branch rather than conflicting with the interim | one `elseif`, or wait |
| **2** | [`OQ-3`](../design/broker-ca-and-nested-hosts.md#7-open-questions) — a `[SKIP]` level for `yolo check` | **Defect, live today.** `internal/cli/check/sections_loopholes.go:23` calls `r.ok` on a section it skipped, and `reporter.go:83` counts it as a pass — ten sites | one ruling, ~10 lines |
| **3** | [`OQ-BR4`](../design/bedrock-plumbing.md#OQ-BR4) — narrow `profile`'s env gate | **Defect, live today.** `internal/packload/packload.go:634` matches a bin *any* selected pack installs, so one agent's profile fires another's env | one ruling |
| **4** | [`OQ-PS2`](../design/provider-switching.md#OQ-PS2) — clear the model id a deselect orphans | **Defect, live today.** `internal/agentcfg/selection.go:168` only ever lifts, so a dropped profile leaves its model pinned | one ruling |

## 💬 Needs you

| # | Decides | Doc | Live | Gate | Releases |
|---|---|---|---|---|---|
| 1 | Whether the backend census is worth building, and whether a `Warned` disposition can be suppressed | [`backend-parity.md`](../design/backend-parity.md) · in-review | 2 | [`OQ-BP-1`](../design/backend-parity.md#open-questions) | **build** — the census itself. `OQ-BP-5` left this row on 2026-09-15, answered by code (an ACE) rather than by a ruling |
| 4 | A stated byte budget, or a policy — **now measured** | [`minimal-disk-footprint.md`](../design/minimal-disk-footprint.md) · in-review | 1 | [`OQ-DF4`](../design/minimal-disk-footprint.md#OQ-DF4) | **doc** — unblocked 2026-09-15; ~70 GiB/yr grows unreclaimed but 99% is two NAMED stores, so the call is "sweep `mise/` + `cache/staticcheck`, or declare them yours" |
| 5 | Provider deselection, aliases and shipped model ids | [`provider-switching.md`](../design/provider-switching.md) · draft | 4 | [`OQ-PS2`](../design/provider-switching.md#OQ-PS2) | **defect** — an orphaned model pin |
| 6 | Whether to take the one-line credential fix before the resolver lands | [`cerebras-pack-and-copilot-delivery.md`](../design/cerebras-pack-and-copilot-delivery.md) · in-review | 1 | [`OQ-2`](../design/cerebras-pack-and-copilot-delivery.md#oq-2) | **defect** — a key sent to the wrong vendor. Its doc was compacted 2026-09-18 (266→190 lines): two answered questions became ledger rows, and the design half moved to [`protocol-resolution.md`](../design/protocol-resolution.md), leaving a sequencing call |
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
| 23 | How a `jail_daemon` runs on `macos-user` — argv resolution with no image, and whether it is confined | [`jail-daemon-on-macos-user-plan.md`](../design/jail-daemon-on-macos-user-plan.md) · draft | — (no ids, so the count below cannot see them; both are stated in that plan's §Blockers) | [§Blockers](../design/jail-daemon-on-macos-user-plan.md) | **build** — and it gates the blocked halves of BOTH 📦 [row 1](#-ready) and [row 3](#-ready), which is why it is one row. Every shipped `jail_daemon.cmd` names `yolo-jaild`, which exists only as `bin/linux-<arch>`, and a symlink cannot reach it because both binaries dispatch on plain `args[0]` rather than `argv[0]`. And `DP-L3`'s prescribed placement in `buildBootstrapEnv` would start the daemon UNCONFINED and orphaned, which the doc is silent about |
| 24 | Whether a repo's own skills may reach whichever agent the reader chose | [`workspace-skills.md`](../design/workspace-skills.md) · in-review | 6 | [`OQ-WS1`](../design/workspace-skills.md#OQ-WS1) | **build** — or a close. A repo that ships `.claude/skills/` has chosen for its readers, and a copilot or codex user gets a worse experience from the same checkout for no reason either of them chose. Four mechanisms are costed against measurements (where each agent reads at project scope, which dedupe by bare name vs real path, and the writability asymmetry that makes the workspace the only jail-side landing place). [`OQ-WS1`](../design/workspace-skills.md#OQ-WS1) is the closure question and it is a security question: `packs` is user-scope ONLY because the workspace is jail-writable, so whether a workspace may contribute CONTENT at all is the thing to rule — a no makes the deliverable the documented convention in [§4.3](../design/workspace-skills.md#43-baseline-c--conventions-only) and closes the doc |
| 27 | How a pack declares that it ADAPTS one wire protocol to another | [`protocol-resolution.md`](../design/protocol-resolution.md) · in-review | 3 | [`OQ-PR1`](../design/protocol-resolution.md#OQ-PR1) | **build** — and the ruled half is already buildable without it (see 📦). Four decisions landed in review 2026-09-18: a pack declares the protocols its program speaks, a declared adapter resolves an otherwise-unusable pairing automatically, the adapter's address is configurable, and the single-protocol `base_url` shorthand is deleted with no transition. [`OQ-PR1`](../design/protocol-resolution.md#OQ-PR1) decides the VOCABULARY the rest hangs off — a new contribution kind, or a field on the `service` kind `wire-bridge` already uses — and with it the two smaller ones: whether a provider declaring no endpoints stays legal, and whether the resolver may join an adapter pack the user did not select |
| 26 | Whether `mounts` and `env_sources` become user-scope-only, like every other key that grants host access | [`agent-safehouse.md`](../research/agent-safehouse.md) · in-review | 1 | [`OQ-AS3`](../research/agent-safehouse.md#OQ-AS3) | **defect-shaped, and it is an ASYMMETRY rather than an oversight.** `packs`, `profiles`/`use_profiles`, source-bearing `host_files`, loophole `settings` and a provider's `base_url` are each refused in a workspace config BY RULING — because that file is agent-editable and travels with the repo. `mounts` (a host dir at `/ctx`) and `env_sources` (a host dotenv whose values become the jail's environment) are NOT: verified 2026-09-18, neither has a workspace-scope refusal anywhere in `internal/config`. So a repo-committed `yolo-jail.jsonc` can name a host path to mount and a host file to read secrets out of, disclosed only by the config-change diff. The comparison that surfaced it argues the fix is a scope rule rather than a trust prompt, which is what [`gate-placement-principle.md`](../reference/gate-placement-principle.md) would say too — `env_sources` is the sharper half, since source-bearing `host_files` is already user-scope-only for exactly this reason and `env_sources` reaches the same host files by another name |
| 25 | Storage tier and pre-launch update execution for Pi extensions across jails | [`pi-extension-lifecycle.md`](../design/pi-extension-lifecycle.md) · in-review | 3 | [`OQ-1`](../design/pi-extension-lifecycle.md#OQ-1) | **build** — machine-scoped package storage and launcher refresh |

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
| 1 | Give the OpenAI subscription service a `macos-user` refresh consumer, and promote `openai-auth` to a public verb | **The Apple Container half and host-only import/logout are DONE 2026-09-18** (`36c47baa`, `4de78ac0`). The `withoutOpenAIAuthPack` exemption is deleted, so no pack is exempt from the inert report and the one service that backend starts is reported with `backendInertReason`'s version-stamped measurement instead of being the single pack the launch said nothing about. Withholding the endpoint variable was deliberately NOT done, with the reasons in `openaiauthbackend.go`: the measurement is per BACKEND, so it would patch one service's pointer against a fact covering every loopback-TLS service there, and the adapter reads that variable per request — withholding it keeps the process and loses the diagnosis. Import/logout are two HANDLERS chosen where the sockets are bound, never a `JailID` check (that field is self-asserted on the private socket); import refuses a symlink, an incomplete token set and a yolo broker view, and the refusal is asserted through the real loopback-TLS front. ⚠ Still hidden behind `yolo internal openai-auth` — promoting it is a registry row plus two help tables. ⚠ The `macos-user` half waits on 💬 [row 23](#-needs-you), and the four PARTIALs from the 2026-09-17 audit are untouched: no Codex version floor, no pi ask-once-more, no port for a third concurrent login, and `Broker.Logout` now has a caller but no public verb | [`openai-auth-broker.md`](../design/openai-auth-broker.md) · [`openai-auth-broker-plan.md`](../design/openai-auth-broker-plan.md) |
| 2 | Give `packs/pi/derive.lua` a `compat` block for loopback endpoints | measured 2026-09-17: pi's auto-detection for an unknown loopback URL yields the INVERSE of pi's own llama.cpp provider flags (`supportsStore`/`DeveloperRole`/`ReasoningEffort`/`StrictMode`, and `max_completion_tokens`). Nothing else blocks the `llamacpp` pack's pi smoke test | `packs/pi/derive.lua` |
| 3 | Start `jail_daemon` on `macos-user` — the decline shipped; the daemon waits on 💬 [row 23](#-needs-you) | **The truthful decline SHIPPED 2026-09-18** (`f6387968`): `loopholes.Set.JailDaemons` is the one composer and `JailDaemonPayload` the one writer of the wire shape — `internal/cli/run` had been building a second copy beside it — hoisted above the backend dispatch, with the container argv pinned byte-identical both as a golden and as the equality of the compose-inside and compose-outside spellings. The native arm declines each daemon BY NAME. **Measured on the default config, and it corrects this row's own prediction: a bare `"packs": ["claude"]` declares THREE, not two** — `oauth-terminator` as well — and none runs, while claude's host broker singleton DOES start there. No classifier was written: nothing is runnable on that arm, so its second branch would have been guesses. Steps 3–4 still need the two unfiled rulings (argv resolution with no image; confinement). The 📦 row 2 dependency is CLEARED (`eb02ad86`) — a spawn failure is now readable instead of presenting as an empty log and no process | [`jail-daemon-on-macos-user-plan.md`](../design/jail-daemon-on-macos-user-plan.md) · `internal/loopholes`, `internal/macosuser` |
| 4 | Bedrock from a host `aws sso login`: the jail-side adapter, the pack, and the live-host checks | **Steps 1–2 BUILT 2026-09-18** (`b94351fe`, `e76e43b2`, `700d7699`): `internal/awsauth` (cache keyed by profile, host-wide flock, mint via the `aws` CLI, narrowing and its refusals, five failure classes) and `internal/awsauthdaemon` (fronted + `.host` sockets, proactive re-mint, `--self-check`), reachable as `yolo internal daemon aws-auth`. The whole AWS surface is one injected `Runner` seam, so no test runs `aws` or touches the network; 26 mutations run, 26 caught. The `SessionToken`→`Token` rename has ONE spelling, so step 3's adapter is a pass-through. Three choices the design left open were forced toward refusing rather than guessing (a session policy with no role, `unnarrowed` beside a `role_arn`, a static-key credential the container protocol cannot carry). **Steps 3–8 remain**, and a SIXTH Blocker opened: the launch-side disclosure [`OQ-SSO1`](../design/sso-backed-bedrock.md#13-decision-ledger) requires has no generic mechanism and must not get a tool-name switch — it lands with step 3, which is what gives it a subject. ⚠ Nothing has run against a live `aws sso login`; every fixture upstream of the seam came from AWS's documented formats, never a recorded invocation | [`sso-backed-bedrock.md`](../design/sso-backed-bedrock.md) · [`sso-backed-bedrock-plan.md`](../design/sso-backed-bedrock-plan.md) |
| 5 | `yolo config`'s remaining two steps: a preview's `host` layer, and a host-side jail-notch `reset` | **Steps 1–5 SHIPPED 2026-09-18** (`3330367c`, `bbb6eccc`, `9c284f0c`, `8b4bdd05`, plus `6019c1e0`'s integration pin): one `configTarget` per invocation — notch, workspace, store, home root, may-write, chosen-by — resolved once in `configRunW` and handed to the verb, disclosed on **stderr** by every verb (stdout would have broken `config dump`'s JSON and `promote --format json`), selectable with `--at`. The retired predicate pair and the four hand-joined `prism*` twins are now internals an AST census keeps there. Closes F1–F3 and F2's row inflation; `diff` reports the captured divergence alone and per-key provenance lives in `ls`. **Step 6 is gated on the plan's Blocker 1** — [`OQ-CR6`](../design/config-target-resolution.md#oq-cr6)'s two-case table has no discriminator at the jail notch, so it needs the fifth `YOLO_HOST_LAYERS` disposition saying whether staged bytes are the user's or yolo's own render, which lives in `run/` + `entrypoint/`; step 7 depends on 6. Three residues: the `--at` token parse is duplicated rather than shared with `apply` (pinned through `render`'s vocabulary), Blocker 3 is open so `apply --sealed` keeps the bare-cwd walk, and [`OQ-CR7`](../design/config-target-resolution.md#oq-cr7) has an unnamed consequence — `diff <pack-declared-agent>` now refuses for want of a capture surface where it used to print an overlay block | [`config-target-resolution.md`](../design/config-target-resolution.md) · [`config-target-resolution-plan.md`](../design/config-target-resolution-plan.md) |
| 6 | Assert the `macos-user` Seatbelt profile at RUNTIME, and take three denies from Agent Safehouse | our 16 Seatbelt tests pin the generated STRING and its ordering; nothing asserts the string denies anything, while Safehouse runs 389 policy assertions on macOS CI. The hard part is already built: `.github/workflows/macos-user.yml` runs on a macOS runner and carries the `YOLO_TEST_MACOS_USER` guard that makes a zero-test run exit non-zero. Then three denies their own suite shows agents survive — `procargs`/`process-info-pidinfo` (cross-process argv and env inspection, with a `same-sandbox` re-allow), tty-only `file-ioctl`, and `/System/Library/Keychains` — in `SeatbeltProfile` (`internal/macosuser/seatbelt.go`), one function with its ordering invariant already pinned. ⚠ **And it closes a live callee-pinned/call-site-unpinned hole:** `PlanInvariants` pins `sandbox-exec -f <profile>` for the provisioning stage and `CapturePlanInvariants` for the capture driver, but **nothing pins it on the AGENT's own argv** — deleting it there leaves the suite green, on the backend where that profile IS the whole trust boundary. Two rulings sit beside this and block none of it: how far toward `deny default` to go, and whether we want a per-agent doc series | [`agent-safehouse.md`](../research/agent-safehouse.md) · `internal/macosuser` |
| 7 | The ruled half of protocol resolution: the pair rule, the agent's protocol declaration, and the resolver's direct/refuse outcomes | **three steps that need no further ruling**, from [`protocol-resolution-plan.md`](../design/protocol-resolution-plan.md)'s build order. **(1)** The pair rule in `packs/claude/derive.lua` — one `elseif`, closing the live leak where `if p.api_key` fires whether or not a base URL was composed, so an openai-only provider's key reaches `api.anthropic.com`. The inverse is already handled (`elseif routed` substitutes a dummy token), so this is the missing half of a rule the file keeps. **(2)** The agent's protocol declaration in `internal/packdecl`, INERT — if landing it changes a launch, something read it early. **(3)** The resolver with outcomes 1 and 4 only (direct, or refuse on no common protocol), which is where the leak becomes unrepresentable rather than guarded. The capability gate shipped 2026-09-18 (`f5c26899`) is the precedent for all of step 3, including the ⚠ that a `yolo check` copy must not consult data the launch cannot see. Steps 4–7 wait on 💬 [row 27](#-needs-you) | [`protocol-resolution.md`](../design/protocol-resolution.md) · `packs/claude`, `internal/packdecl` |

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
