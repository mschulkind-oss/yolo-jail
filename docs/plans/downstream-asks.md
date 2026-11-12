---
title: "What a downstream pack consumer is still waiting on from yolo"
date: 2026-10-13
status: accepted
stage: CURRENT
next: "Nothing is owed: every ask is landed, declined or settled, and no question is open."
depends-on:
  - ../reference/patched-forks.md#OQ-PFK6
  - ../reference/patched-forks.md#OQ-PFK7
  - ../design/model-lists-and-pickers.md#OQ-ML4
  - ../design/model-lists-and-pickers.md#OQ-ML5
tags: [handoff, downstream, packs, launcher, tracker]
summary: "The yolo-side index of the asks a downstream pack consumer (the dotfiles repository) carried in its 2026-10-13 handoff, and two filed since: which are still open at HEAD, what a landed version would look like, and where each one's design question is owned. It exists because none of these were in yolo's own docs before now."
vantage:
  status-chip: true
---

# What a downstream pack consumer is still waiting on from yolo

**Status:** 2026-10-18, re-verified at main `4e7b9b92bfc8d6dbd0e0b5190d0c73efeb6d96c1`; [OQ-DA2](#OQ-DA2) ruled 2026-10-16; asks 6 and 8 landed 2026-10-17. **Why this file exists:** the handoff's nine asks
were, with four exceptions now owned elsewhere, recorded nowhere in yolo's own documentation, so a
reader of the roadmap or a design doc could not tell which of them yolo had accepted, declined, or
never seen. This is their one index.

**How to read.** Each ask carries its **state at HEAD**, the **evidence** a reader can re-run, what
**landed** would look like, and its **owner** — the design doc that owns the decision, or this file
where no design doc owns it. An ask whose every part is open is the whole target; one whose yolo
half is done says so.

**One boundary.** This file is a work queue, like [`BACKLOG.md`](BACKLOG.md); it holds reasoning
only where a question has no design doc of its own. The source handoff lives outside version control
(a consumer's repository), so its long-form attachments (`yolo-fix-requests.md`,
`pi/carries/*.md`, `pi/model-inheritance.md`) are not links here.

## The asks at a glance

| # | Ask, in one line | State at HEAD | Owner |
| :--- | :--- | :--- | :--- |
| [1](#1-remove-for-config-list) | a `remove:` sibling for `config-list` | landed, with a profile gate | [`pack-system.md`](../reference/pack-system.md#adding-entries-to-an-array-config-list); ratify [OQ-DA4](#OQ-DA4) |
| [2](#2-a-pack-cannot-declare-the-yolo-version-it-needs) | a pack can declare a minimum yolo, or lint judges it against an older build | declined | [OQ-DA1](#decision-ledger) |
| [3](#3-a-build-has-no-version-identity) | two binaries from one revision are distinguishable, and a broadcast clause records its version | first half landed, releases included; second declined | [OQ-DA2](#OQ-DA2), ruled 2026-10-16 |
| [4](#4-a-reduced-window-child) | a reduced-window child does not silently inherit the small window | narrowed — yolo's part done | decision recorded below; the owner's declaration is the consumer's |
| [5](#5-a-clean-replay-discards-the-clone-it-built) | a clean replay keeps the clone, and a stale series is signalled | settled — no clone, base lag built | [OQ-PFK7](../reference/patched-forks.md#why-its-this-way) |
| [6](#6-a-declared-model-row-loses-the-catalogs-thinking-levels) | a `models.json` row keeps pi's catalog thinking levels | landed 2026-10-17, on pi's own Bedrock client | [OQ-ML4](../design/model-lists-and-pickers.md#OQ-ML4), [ML-D13](../design/model-lists-and-pickers.md#ML-D13) |
| [7](#7-the-carry-key) | a `files` contribution may name a commit graph instead of a series | landed, as `patch_branch` | [OQ-PFK6](../reference/patched-forks.md#why-its-this-way) |
| [8](#8-model-inheritance--emit-sharedmodels-and-extends) | the pi derive emits `sharedModels` and `extends` on both surfaces | landed 2026-10-17, for a pi or a fork of it declaring `reads_shared_models` | [OQ-ML5](../design/model-lists-and-pickers.md#OQ-ML5), [ML-D14](../design/model-lists-and-pickers.md#ML-D14) |
| [9](#9-the-yolo-check-notice-for-a-bypassed-append_systemmd) | `yolo check` names a bypassed `~/.pi/agent/APPEND_SYSTEM.md` | declined 2026-10-16 (the skill half landed) | [OQ-DA3](#OQ-DA3) |
| [10](#10-model-auth-for-an-isolated-agent-process) | an isolated agent process gets model auth without a copy of the login | declined 2026-10-15 | [IA-D1](../design/isolated-agent-model-auth.md#IA-D1) |
| [11](#11-the-host--p-profile-governs-pi-subagents-children) | an explicit `yolo host -p` governs pi-subagents' child defaults and model scope | yolo ships nothing for it (2026-10-16); the consumer declares `launch_computed` in their own pack | [MM-D42](../design/model-lists-and-pickers.md#MM-D42) |

## 1. `remove:` for `config-list`

**Ask.** A `remove:` sibling so one contribution can take an element out of an array another
contribution owns, without restating the array.

**State.** `landed` 2026-10-15 (`e57c7862c`), on [OQ-DA4](#OQ-DA4)'s leaning. **Evidence:** a
`config-list` takes `remove:` beside `add` ([`contributes.go`](../../internal/packdecl/contributes.go)):
every element equal to a listed entry leaves the array the lower layers produced, an absent one is a
silent no-op, and within one contribution `remove` applies before `add`. It also takes `profile`, so
a gated `add` or `remove` applies only while that profile is active. The kind is documented in
[`pack-system.md`](../reference/pack-system.md#adding-entries-to-an-array-config-list) and
`yolo config-ref`.

**Why it matters.** An array-targeting overlay replaces the whole array, so without `remove:` the
only way to drop one element is to restate every sibling's — silently owning them.

**Landed.** A contribution can remove a named entry; removing an absent one is a no-op.

**Owner.** [OQ-DA4](#OQ-DA4), ratified 2026-10-16.

## 2. A pack cannot declare the yolo version it needs

**Ask.** A pack names a minimum yolo, or `yolo pack lint --for-version <v>` judges a pack against
an older build, so prose legal on one build and fatal on another is checkable instead of tragic.

**State.** `declined` by the maintainer, 2026-10-15: *"Nobody's running old versions."* Neither
spelling exists, and neither will. What covers the case instead: since 0.12.0 a launch skips, with
a warning, a contribution holding a field its yolo does not know
([`skew.go`](../../internal/packdecl/skew.go)), and a host `yolo` older than the source it launches
refuses, naming `just install` (`version.SourceSkew`). The enforceability objection stays on the
record in [`keys.go`](../../internal/loopholedecl/keys.go).

**Owner.** [OQ-DA1](#decision-ledger), compacted.

## 3. A build has no version identity

**Ask.** Bake an identity into a build so two binaries can be told apart from the outside, and have
a broadcast clause record the version that added it.

**State.** The identity half is `landed`; the version-record half is `declined`, both by the
owner's rulings of 2026-10-16 ([OQ-DA2](#OQ-DA2)). **Evidence:** every build's version string
carries the commit hash, releases included: a release prints `0.14.0+g<12 hex digits>`, and a
source or flake build adds its distance from the tag and a dirty mark. Every build channel stamps
it: [`.goreleaser.yaml`](../../.goreleaser.yaml), the Homebrew formula
([`update-homebrew.sh`](../../tools/release-wiring/update-homebrew.sh), from the release commit,
since its tarball has no git metadata), the wheels, `scripts/build-go.sh`, `just install` and the
flake (`e5c807f13`, `buildStamp` in [`flake.nix`](../../flake.nix)). Update checks ignore the hash
when ordering versions. `yolo --version --full` also prints the commit, the build stamp and the
sha256 of the running executable (`8198098ab`, [`identity.go`](../../internal/version/identity.go));
the sha256 stays, because builds from one dirty tree share a version string. `yolo config-ref`
records no version for a rule: its "as of 0.9.0" on the broadcast clause is removed.

**Owner.** [OQ-DA2](#OQ-DA2), compacted.

## 4. A reduced-window child

**Ask.** Stop a child inheriting a reduced-window parent when a same-`base` full-window sibling
exists.

**State.** `narrowed`; **yolo's part is done.** **Evidence:** `profiles.<name>.subagents.default`
ships ([`modelroles.go`](../../internal/packload/modelroles.go),
[`luahook.Subagents`](../../internal/agentcfg/luahook/derive.go)), the explicit form yolo chose over
an automatic floor, and `yolo config-ref` documents it under `profiles.<name>.subagents`
(`9ff3b207c`). Whether a profile declares it is the consumer's decision, not yolo's.

## 5. A clean replay discards the clone it built

**Ask.** (a) A clean-replay export hands over the clone instead of deleting it. (b) A signal names
a series gone stale against its upstream.

**State.** `settled` 2026-10-15 by the maintainer's ruling
[PF-D85](../reference/patched-forks.md#PF-D85), which compacted
[OQ-PFK7](../reference/patched-forks.md#why-its-this-way). **Evidence:** `yolo pack rebase` and its clone are gone
([PF-D85](../reference/patched-forks.md#PF-D85)): every conflict prints the git commands that rebase
the series, so the clone is the user's own, and `yolo pack status` prints the same commands under a
series' base lag when the newest version takes it as it stands, which re-anchors it. The base lag
names a stale series on `yolo pack status` and on the launch's fork line
([`baselag.go`](../../internal/packsrc/baselag.go)); no `yolo check` warning exists. A launch's
replay also drops a member upstream already carries, even one upstream edited again, as `git rebase`
does ([PF-D86](../reference/patched-forks.md#PF-D86)).

**Owner.** [OQ-PFK7](../reference/patched-forks.md#why-its-this-way), compacted.

## 6. A declared model row loses the catalog's thinking levels

**Ask.** A `models.json` row that states no thinking levels keeps pi's catalog map, so
`--thinking xhigh` is not silently clamped on a declared model.

**State.** `landed` 2026-10-17 ([ML-D13](../design/model-lists-and-pickers.md#ML-D13)), after the
menu-file half on 2026-10-16 ([ML-D12](../design/model-lists-and-pickers.md#ML-D12)). **Evidence:**
on pi's own Bedrock client, a row naming a catalog id is written to `models.json` as pi's
`modelOverrides` entry, which pi merges over its catalog; stock pi 1.0.1's own composer, run by
`TestPiOnBedrockKeepsTheCatalogsThinkingLevelsInModelsJSON` where pi is installed, keeps
`global.openai.gpt-6-luna`'s `xhigh` with yolo's window. A row with `base`, `wire_id` or a
tombstone is still written whole. A provider pi ships no catalog for has no catalog entry to
keep.

**Landed.** The derive merges catalog facts into a declared row that states none, the first of
the two outcomes the ask offered; the ruling took it.

**Owner.** [OQ-ML4](../design/model-lists-and-pickers.md#OQ-ML4), ruled 2026-10-16: the derive
merges a row over pi's catalog field by field, in both files, and a fact goes only when the row
writes it as `null` ([ML-D10](../design/model-lists-and-pickers.md#ML-D10)). Built.

## 7. The `carry` key

**Ask.** Let a `files` contribution name either `patches` or a `carry`: a git repository plus a
branch, resolved to a commit, against which the launch asks git whether the carry merges cleanly
(`git merge-tree`, no working tree) and refuses, naming the paths, when it does not. Report the
carry's lag behind upstream.

**State.** `landed` 2026-10-16 as `patch_branch`, the owner's name for the key ([OQ-PFK6](../reference/patched-forks.md#why-its-this-way), [PF-D89](../reference/patched-forks.md#PF-D89)); a pack that writes `carry` is refused as an unknown field. **Evidence before it:** no `Carry` field existed and `yolo pack lint` refused
`json: unknown field "carry"`; worse, an unknown contribution field is *skipped* with a warning, so
a pack cannot migrate to it even if it were ignored. The full ruled model is the consumer's
(`pi/carries/`), not yolo's.

**Landed.** (The consumer's acceptance, in its words; the key is `patch_branch`.) A `carry` contribution lints clean; the launch builds the merged tree keyed on the
resolved commit; a conflict refuses naming paths; naming both `patches` and `carry` is refused by
name; `patches` is otherwise unchanged.

**Owner.** [OQ-PFK6](../reference/patched-forks.md#why-its-this-way), ruled 2026-10-15: both `carry` and `patches` are supported, and one contribution naming both is a fatal conflict; on 2026-10-16 the owner named the key `patch_branch` ([PF-D87](../reference/patched-forks.md#PF-D87)). Built 2026-10-16, on `files` extensions and forked programs alike; the lag is reported as the patch branch's merge base behind upstream releases ([PF-D90](../reference/patched-forks.md#PF-D90)).

**2026-10-18 caller-review disposition — code merged into the integrated history at `08e6566c4`; `4e7b9b92b` later changed only a documentation heading.** The retained full patch-branch history includes the clone-isolation and record-only repairs; the final caller repair hydrates only the host-established mapping belonging to the explicitly named workspace pack. The mapping owner is the staged pack/contribution pair, `<staged-slug>/<extension-name>`; it is transferred only when the mapping's workspace-relative directory resolves to the exact explicitly named pack directory and that same contribution still declares a relative branch. The command keeps the manifest loaded from that directory as authority and rejects an unrelated clone; no guest identity fallback or mirror access is used for the offline recorded verdict. The production regression uses separate host and guest checkouts and identities, removes mirrors, denies all git invocations, checks a good recorded verdict and a changed-manifest stale verdict, and rejects an unrelated clone. The final caller review returned **ACCEPT**; see [`october-18-integration.md`](october-18-integration.md) for the full serial history and regression list. This closure was reviewed on the isolated candidate before its source merge.

## 8. Model inheritance — emit `sharedModels` and `extends`

**Ask.** The pi derive and the model-lists resolver emit a `sharedModels` entry per referenced
model and `extends: "<name>"` on the rows, on **both** rendered surfaces —
`~/.pi/agent/models.json` and `~/.pi/agent/yolo-model-lists.json`.

**State.** `landed` 2026-10-17 on the yolo side; resolving the form is the consumer's pi build's.
**Evidence:** the declaration is `"reads_shared_models": true` on the `program` contribution that
supplies pi's bytes, a fork's included. The consumer set it on their fork pack and the render did
not switch, because the fork rewrite kept the base's value; it now takes the fork's
([ML-D14](../design/model-lists-and-pickers.md#ML-D14),
`TestAPiForkDeclaringReadsSharedModelsSwitchesTheRender`). `models.json` then holds the facts once
as `sharedModels` with `extends` rows; the model-lists file carries the same keys beside full rows,
since yolo's own extension reads it. One exception: on pi's own Bedrock client a row naming a
catalog id is a `modelOverrides` entry ([ask 6](#6-a-declared-model-row-loses-the-catalogs-thinking-levels)),
which stays full, with no `extends`. A dangling `extends` is refused by `yolo check`.

**Landed.** (Verbatim acceptance from the consumer's design.) The rendered surfaces carry
`sharedModels`/`extends` rather than restated facts; no route reports pi's 128000/16384 defaults; a
test fails on a same-value restatement; an unresolvable reference is refused by `yolo check` and by
pi naming the row; a route lowering `contextWindow` under an inherited `workingContextWindow` fails
loudly rather than clamping.

**Owner.** [OQ-ML5](../design/model-lists-and-pickers.md#OQ-ML5), ruled 2026-10-16: facts may be
shared across a model's routes, and every route can override any shared fact, since providers
differ for the same model. The shared form is written only for a pi that reads it, and full
merged rows otherwise, because stock pi ignores both keys without an error
([ML-D11](../design/model-lists-and-pickers.md#ML-D11)); so the verbatim acceptance above changes
where it fails a test on a repeated value, which a route may now restate on purpose. Built.
⚠ `extends` is a different
mechanism from the `base` key of [ask 6](#6-a-declared-model-row-loses-the-catalogs-thinking-levels);
the consumer's design argues why `base` does not subsume it.

## 9. The `yolo check` notice for a bypassed `APPEND_SYSTEM.md`

**Ask.** A `yolo check` notice naming a `~/.pi/agent/APPEND_SYSTEM.md` that no projection mounts
into a jail, pointing at the briefing audience as the route.

**State.** `declined` 2026-10-16 for the notice ([OQ-DA3](#OQ-DA3)); the skill half landed. **Evidence:** the shipped `configuring-the-jail` skill states that
the host's `~` is never projected and names `host_files` and the briefing route
([`SKILL.md`](../../internal/jailcontent/builtinskills/configuring-the-jail/SKILL.md)); the
`yolo check` notice itself does not exist.

**Landed.** `yolo check` prints the notice.

**Owner.** [OQ-DA3](#OQ-DA3): declined 2026-10-16, so the notice will not be built.

## 10. Model auth for an isolated agent process

**Ask.** A second Pi process in a jail, with its own agent directory and candidate extension code,
gets model auth without a copy of the login's refresh material.

**State.** `declined` 2026-10-15 by the owner ([IA-D1](../design/isolated-agent-model-auth.md#IA-D1)).
Nothing is built. **Evidence:** [`isolated-agent-model-auth.md`](../design/isolated-agent-model-auth.md)
laid out four options (an access-only view, a proxy that injects auth, a provider-scoped key, mocked
dispatch); its [reasons for the decline](../design/isolated-agent-model-auth.md#10-why-it-was-declined)
are that a brokered token is no boundary inside one jail, no per-token revocation exists for either
subscription, Anthropic-subscription use through an intermediary is flagged for Legal Counsel, and
mocked offline tests are not blocked. [OQ-IA1](../design/isolated-agent-model-auth.md#OQ-IA1) to
[OQ-IA6](../design/isolated-agent-model-auth.md#OQ-IA6) are moot.

**Owner.** None; the decline is [IA-D1](../design/isolated-agent-model-auth.md#IA-D1).

## 11. The host `-p` profile governs pi-subagents' children

**Ask.** An explicit `yolo host -p` profile governs pi-subagents' profile-derived child defaults and
model scope, not only the parent session.

**State.** `yolo ships nothing for it` (2026-10-16). The generic
[`launch_computed`](../reference/pack-system.md#launch_computed) kind hands a `-p` profile's
computed block as argv for one launch; the pi pack's declaration of a pi-subagents flag was removed
by the maintainer's ruling, *"yolo shouldn't depend on my custom pi fork in any way, or even know
it exists"*, since upstream pi-subagents registers no such flag. The consumer declares
`launch_computed` for `pi` in a pack that forks the `pi` program (a patched pi-subagents extension alone is not enough)
([LC-D1](../reference/pack-system.md#LC-D1)); the pi derive still composes the `subagents` block
it reads.

**Owner.** [MM-D42](../design/model-lists-and-pickers.md#MM-D42).

## Open Questions

The mechanism asks owned by a design doc of their own are all ruled there:
[OQ-PFK6](../reference/patched-forks.md#why-its-this-way) (2026-10-15),
[OQ-ML4](../design/model-lists-and-pickers.md#OQ-ML4) and
[OQ-ML5](../design/model-lists-and-pickers.md#OQ-ML5) (2026-10-16), all built; [OQ-PFK7](../reference/patched-forks.md#why-its-this-way)
is settled. Of the four with no other home, [OQ-DA1](#decision-ledger), [OQ-DA2](#OQ-DA2), [OQ-DA3](#OQ-DA3) and
[OQ-DA4](#OQ-DA4) are all settled; none remains open.

## Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| — | A reduced-window child is the profile's declaration, not an automatic floor: `profiles.<name>.subagents.default` shipped instead. | 2026-10-12 | [ask 4](#4-a-reduced-window-child) | ✅ |
| <a id="OQ-DA3"></a>[OQ-DA3](#OQ-DA3) | **Maintainer ruling, B — no `yolo check` notice.** *"I don't see how we could possibly do this YOLO check thing. That's like, how could you warn about every single possible thing that could go wrong? ... there's no way we can help with that one."* A host file in the user's own home that an agent reads (here pi's `~/.pi/agent/APPEND_SYSTEM.md`) reaches no jail by design; the shipped skill says so, and a pack's briefing is the route. Rejected: A, a check for one agent's file | 2026-10-16 | [ask 9](#9-the-yolo-check-notice-for-a-bypassed-append_systemmd) | — (nothing to build) |
| <a id="OQ-DA4"></a>[OQ-DA4](#OQ-DA4) | **Maintainer ruling, A — `config-list` takes `remove:`**, ratifying what was built: *"of course we can say remove an entry. It seems super useful ... why would we not have this?"* An entry equal to a listed one leaves the array; an absent one is a no-op; within one contribution `remove` runs before `add`; a `profile` gate applies to both | 2026-10-16 | [ask 1](#1-remove-for-config-list) | ✅ `e57c7862c` |
| OQ-DA1 | **Maintainer ruling, A — neither:** *"Nobody's running old versions."* No pack version declaration and no `yolo pack lint --for-version`. The unknown-field skip a launch has made since 0.12.0 and the source-skew refusal cover a pack ahead of its yolo. Rejected: B, an advisory declaration; C, a lint against an older build | 2026-10-15 | [ask 2](#2-a-pack-cannot-declare-the-yolo-version-it-needs) | — (nothing to build) |
| <a id="OQ-DA2"></a>[OQ-DA2](#OQ-DA2) | **Owner's ruling:** *"our version string should always have the git hash or a chunk of it in it ... why does it matter when rules were added? that seems like impossible ongoing work. I don't want to set that precedent. why can't nix know a commit?"* and, on releases, *"yes, releases name the hash too. hashes are _actually_ immutable."* Every build's version string carries the commit hash, releases and Homebrew included: a release prints `<version>+g<hash12>`, and ordering ignores the hash. `--full`'s binary sha256 stays for builds that share a string (a dirty tree). The ask's second half, recording the version that added a rule, is declined: docs carry no per-rule version history, as with [OQ-DA1](#decision-ledger). Superseded: the A/B/C options of the 2026-10-15 question | 2026-10-16 | [ask 3](#3-a-build-has-no-version-identity) | 2026-10-16, `e5c807f13`: `buildStamp` in `flake.nix`; pinned by `version.TestTheFlakeStampsWhatBuildGoStamps` and the integration `TestTheJailsYoloCarriesTheCheckoutsCommit`. Releases: `.goreleaser.yaml`, `update-homebrew.sh` and the wheels, pinned by `TestAReleaseBuildPrintsItsVersionAndCommit`, `TestHomebrewFormulaHeredocNeverExecutesLiteralYoloForNormalOrBackfill` and `TestWheelBinariesNameTheReleaseCommit`. The sha256 line since `8198098ab` |
