# Documentation triage — proposed reorganization (for review)

**Status:** ✅ EXECUTED 2026-07-03 (`5eb1643`, `9721660`) — kept as the record of what was archived
and why, not as a pending proposal.

> [!NOTE]
> **RE-RUN 2026-09-09, and this file's taxonomy is what it ran on.** Every doc in `docs/design/` was
> bucketed A/B/C again, in five slices, each verdict checked against the code rather than against
> the doc's own status line. **The sweep was not an archiving pass** — the 2026-07-03 run's job was
> to delete; this one's was to ROUTE, because the failure mode had changed. What it found:
>
> - **Five B docs carried live open questions that no roadmap row reached.** They are now
>   [`roadmap.md`](roadmap.md) 💬 24–💬 28, plus two questions folded into 💬 10.
> - **Three roadmap rows still asked for decisions that had been made**, and were closed (💬 16, 22, 23).
> - **~20 status lines were FALSE against the code, in both directions** — and the dominant
>   direction was the opposite of 2026-07-03's. The 2026-07-03 sweep hunted docs that were *done and
>   still filed as pending*. This one mostly found the inverse: **reference docs whose content rotted
>   while their status line kept asserting a verification date.** `jail-home.md` is the type
>   specimen — spot-verified 2026-08-23, with a mount table and a PATH section that two later
>   changes had falsified.
> - **Two live questions were invisible to the corpus count** because they carried no `💬` glyph in
>   a countable position, and three dead ones were still counted. That is the bias the roadmap's own
>   count NOTE warns about, observed.
>
> **The A/B/C verdicts still hold as a taxonomy; what does not hold is a bucket assignment made once.**
> A doc moves B→C the day it ships, and nothing moves it. That is why this run routed instead of
> archiving, and why the next one should start from the code, not from the status lines.

**The corpus as of 2026-09-09:** `docs/design/` holds 83 files, `docs/plans/` 38. Whether a
*third-party* archiving sweep is worth running is argued in
[`further-roadmap-ideas.md` §5](further-roadmap-ideas.md#5-the-weakest-idea-in-the-file-kept-because-it-is-nearly-free) — where the verdict is *do the cheap half
only, when passing through*, because a shipped plan is still the best account of why something is
shaped the way it is.

**Purpose:** classify every doc under `docs/` so obsolete/done working-docs get
archived while the reference + active-design docs stay. **The reorg has been
executed**: commit `5eb1643` git-rm'd the 12 C-bucket docs and repointed every
cross-reference; commit `9721660` moved [`handoff-cachix-cache.md`](handoff-cachix-cache.md) into
`docs/plans/`, removed the now-empty `docs/implementation/`, and added
`docs/plans/README.md`. The "Action" column and the [§4](#4-cross-reference-patch-plan) patch plan below record
what was done.

## The 2026-09-09 disposition — the restructure this file's taxonomy finally licensed

**The count was never the problem; the exit was.** `docs/design/` held 81 files and 48,288 lines
against four docs in `docs/reference/`. The `system-doc` genre has a final
phase — built → distill into an evergreen reference → **delete the design doc** — and it had fired
four times against roughly forty docs that qualified. So the planning tree was not full of open
work. It was full of **finished work that never left**, and the mixing is what made the corpus
unmanageable: a reader cannot tell a live proposal from a system description by looking at the
directory.

`AGENTS.md`'s own *Where things live* table was the tell. It pointed at `docs/reference/` for two
topics and at `docs/design/` for nine others, with no principle separating them except when
somebody last did the graduation.

### The buckets, measured 2026-09-09

| Bucket | Count | What it meant here | Disposition |
| :--- | :--- | :--- | :--- |
| **A** | 40 | Reference — describes a built system. **37 of the 40 carried zero live `💬`.** Fourteen were named by `AGENTS.md` as *the* authority for a topic. | → `docs/reference/` |
| **B** | 25 | Active design. 20 carry live questions; 5 are fully ruled. | **Stays in `docs/design/`.** This is what the planning tree is for. |
| **C** | 14 | History — decided, built, and finished. | Folded into a reference where it duplicated one, else archived (`git rm`; git keeps it). |

### What the disposition is NOT allowed to do

> [!IMPORTANT]
> **A reference doc may not carry a live `💬`.** Where a doc mixed a built system with an unanswered
> question, the question stayed behind in `docs/design/` as a **stub at the original filename** — so
> the roadmap row that routes it keeps resolving — and only the built body graduated. Deciding this
> per-doc, rather than by a rule, is why the graduation is a rewrite and not a `git mv`.

> [!WARNING]
> **Rule IDs are an API and archiving breaks them.** `OQ-N`, `P1`, `R3`, `Test 1`, `CFP-2`, `SS-6`
> are cited from Go comments, sibling docs and the roadmap — this repo's convention is that a
> comment cites the design doc explaining *why*. A ruling that survives keeps its **original id** in
> the reference's `## Why it's this way` appendix, because after the design doc is deleted that
> appendix is the only place the id resolves. Measured cost before starting: 25 files cite
> `profiles-as-pack-variants.md`, 21 cite `pack-config-collaboration.md`, 20 cite
> `pack-config-keys.md`, 13 cite `host-apply-staleness.md` (several by `R3`), ~37 cite
> `loophole-activation.md`. **Nine of the fourteen C docs had code citations and so were not freely
> deletable** — only five were.

### Where the work was hardest, and why

Two families were not "docs in the wrong tree" but **one subsystem described three times**, which is
the shape that actually causes drift:

- **Loopholes** — five docs, 6,766 lines. `loophole-protocol` and `loophole-transport` were clean and
  separable; `loophole-activation`, `loophole-packaging-overview` and `loophole-packaging` overlapped
  heavily and all three said "built", while the last buried the subsystem's only two live questions
  at line 2,443. Two of the three needed a status correction the same night, and a kind count that
  had rotted in the overview was asserted identically in two other files. **One fact, four copies,
  three of them wrong** — that is the argument for consolidating, not against it.
- **Packs** — ten docs around `pack-system.md`, which `AGENTS.md` names as the authority. Its opening note said
  fifteen kinds where the code declares nineteen, and its command-surface section described an
  approval prompt deleted four days earlier.

### The lesson for the next run

**Start from the code, not from the status lines.** The 2026-07-03 sweep hunted docs that were *done
and still filed as pending*. This one mostly found the inverse: **reference docs whose content
rotted while their status line kept asserting a verification date.** `jail-home.md` is the type
specimen — "Spot-verified 2026-08-23", with a mount table and a PATH section that two later changes
had falsified. A status line is a claim like any other, and it is the one nobody re-checks.

## The three buckets (your taxonomy)

- **A — Reference (keep in place).** Describes a system that *still exists*, at
  the mental-model / high-level-component / strategy level. Drift-resistant
  because it explains *why/how* things work, not line-by-line specifics. If you
  want to understand a live subsystem, you open one of these.
- **B — Active design (keep, grouped).** A design for something we're *currently*
  implementing or still discussing — a tool to navigate the work. Proposed home:
  `docs/design/active/` and `docs/plans/` stays the active-plan home. (Or leave
  in place with an "ACTIVE" banner — your call in [§5](#5-decisions-settled-with-the-reviewer--the-executed-reorg).)
- **C — Archive (remove; git history preserves it).** Done or obsolete working
  docs. Repo precedent (commit `2c229fb`) is `git rm`, not a move. Any inbound
  link from a surviving doc gets repointed to the replacement ([§4](#4-cross-reference-patch-plan)).

There is effectively no fourth bucket. A couple of docs are **hybrids** (a
reference-quality incident record that also has a stale "plan" framing) — I call
those out and propose keeping the durable part.

---

## 1. `docs/design/` — mostly Reference

| Doc | Bucket | Why | Action |
|---|---|---|---|
| `agent-briefings.md` | **A** | How AGENTS.md/CLAUDE.md injection works — live mechanism. | keep |
| `config-safety.md` | **A** | Config-change confirm workflow — live. | keep |
| `ctrl-z-and-the-tty-proxy.md` | **A** | TTY proxy mental model — live subsystem. | keep |
| `happy-path-principle.md` | **A** | Strategy doc ("fill the matrix"). Pure mental model. | keep |
| `jail-home.md` | **A** | How `/home/agent` is constructed/mounted — live. | keep |
| `jail-state-separation-design.md` | **A** (hybrid) | Header says "implemented 2026-07-03"; but it's the *decision surface* explaining the split-mise-store/neutral-path/per-side-venv model that's now live. Keep as the reference for that design. | keep |
| `jail-version-predictability.md` | **C** | Header: "plan drafted, no decision yet"; the weekly `flake.lock` bump CI it proposed shipped (`.github/workflows/update-flake-lock.yml`). Working-doc, superseded by the running CI + `mise-node-dynamic-linking`. | **archive** |
| `loophole-protocol.md` | **A** | Loophole wire protocol v1 — live spec. | keep |
| `macos-no-vm-direction.md` | **A** | "DECIDED (2026-07-16)" — the standing decision (compose macos-user + AC). Referenced by the revival plan [§0](macos-revival-and-distribution-plan.md#0-standing-decisions--do-not-relitigate). The strategy of record. | keep |
| `mcp-configuration.md` | **A** | MCP wrapper / per-agent config model — live. | keep |
| `mise-node-dynamic-linking.md` | **A** | The `LD_LIBRARY_PATH`/mise-node investigation — explains a live, still-load-bearing behavior (the new `tool-provisioning.md` leans on it). | keep |
| `rocm-passthrough-design.md` | **A** | AMD ROCm passthrough design — shipped + live in `internal/`. | keep |
| `security-shim.md` | **A** | Shim architecture — live. | keep |
| `storage-and-config.md` | **A** | Storage/config/identity model — live. | keep |
| `venv-per-side-design.md` | **C** (hybrid) | Header: "analysis; decision surface is jail-state-separation-design.md … Implemented 2026-07-03 (S2b shipped)." Its recommendation was absorbed into `jail-state-separation-design.md`. Superseded analysis doc. | **archive** |

## 2. `docs/research/` — Reference + incident records

| Doc | Bucket | Why | Action |
|---|---|---|---|
| `claude-oauth-refresh-mechanics.md` | **A** | How Claude OAuth refresh works — live, referenced by the operational logout doc. | keep |
| `claude-token-logouts.md` | **A** | User-facing operational runbook for 401 loops — live. | keep |
| `macos-container-builder-exploration.md` | **B** | Open-questions doc for the AC-based Linux builder = revival plan **J3** (resurrect `internal/containerbuilder`). J3 shipped (`8abb67c`/`c2f0b94`); now reference for a shipped subsystem. | keep (active) |
| `macos-linux-builder-explained.md` | **A** | Explains the macOS Linux-builder concept for a Linux reader — mental model, still accurate. | keep |
| `macos-support-matrix.md` | **A** | **The live tracker** (revival plan [§0](macos-revival-and-distribution-plan.md#0-standing-decisions--do-not-relitigate) names it authoritative). Never archive. | keep |
| `mise-host-jail-path-mismatch.md` | **A** (hybrid) | "superseded as a decision doc … retained as the incident record." Explicitly still the *only* home for the `.mise.toml` trust-hook fixes. Durable incident/reference record. | keep |
| `platform-comparison.md` | **A** | Linux vs macOS architecture comparison — mental model. | keep |
| `repo-root-and-distribution.md` | **A** | Updated this session to describe live resolution + distribution. | keep |
| `rocm-gpu-jail-findings.md` | **A** | GPU findings on real hardware — reference. | keep |
| `sandbox-comparison.md` | **A** | Built-in sandbox vs yolo-jail — strategy/mental model. | keep |
| `tool-provisioning.md` | **A** | New this session — the 4-layer provisioning model. | keep |

## 3. `docs/plans/`, `docs/implementation/`, `docs/qa/` — the working docs

(From the evidence-backed triage; commits cited inline.)

| Doc | Bucket | Why | Action |
|---|---|---|---|
| `plans/macos-revival-and-distribution-plan.md` | **B** | Roadmap of record (2026-07-20). J1/D1/D2/D3/J2/J3/Track-M done; D4 enabled, first-push/Mac-download human-gated; nothing macos-revival-side fully open. | keep (active) |
| `plans/agent-settings-composition.md` | **B** | Design of record; **Phase C complete (2026-07-22)** — the prism is the unconditional config path at boot + check, and the bespoke agent-config `Configure*` writers are deleted. mise/identity surfaces still deferred. | keep (active) |
| `plans/handoff-cachix-cache.md` | **B** | Human-gated procedure = revival plan **D4**. Cachix substituter enabled `flake.nix:13-16`; first-push/Mac-download human-gated. | keep (active) |
| `plans/claude-oauth-mitm-proxy-plan.md` | **C** | Self-declared "preserved for design rationale"; Python refs deleted; broker/terminator shipped in Go; the refresher it centered on was removed (`51f07ea`). | **archive** |
| `plans/macos-backend-direction.md` | **C** | Its "excise macos-user?" premise was *reversed* (macos-user revived). Superseded by `macos-no-vm-direction.md`. | **archive** |
| `plans/macos-nix-shell-backend-proposal.md` | **C** | devShell mechanism superseded by buildEnv (revival plan [§0](macos-revival-and-distribution-plan.md#0-standing-decisions--do-not-relitigate)); decisions folded into revival plan. | **archive** |
| `implementation/handoff-jail-logout-fixes.md` | **C** | All 5 mechanisms fixed (`8f7b550`,`e0ebba5`,`deaf0fb`,`498a84d`,`e1c6d38`); present in current Go. | **archive** |
| `implementation/handoff-macos-nix-shell-spike.md` | **C** | Python-era spike for the superseded devShell mechanism. | **archive** |
| `implementation/handoff-macos-ondemand-builder.md` | **C** | Python-era; QEMU builder demoted to parked fallback (revival Open Decision #3). | **archive** |
| `implementation/handoff-macos-post-ejection.md` | **C** | Footer redirects to the revival plan, which absorbed its findings (2/3/4/5 landed as J1.1-3; 1+6 re-homed in J2). | **archive** |
| `implementation/handoff-macos-user-revive-plan.md` | **C** | Own header: "LARGELY EXECUTED … live tracker is the support matrix." All change units cite deleted `src/cli/*.py`. | **archive** |
| `implementation/rocm-memlock-handoff.md` | **C** | "✅ RESOLVED … verified on GPU host"; clamp logic in Go. | **archive** |
| `implementation/rocm-passthrough-handoff.md` | **C** | "✅ VERIFIED ON HARDWARE"; AMD path in Go. | **archive** |
| `qa/macos-user-review-findings.md` | **C** | "Resolution status (all addressed)"; the review's findings are all fixed. Point-in-time QA artifact. | **archive** |

`docs/guides/` (USER_GUIDE.md, loopholes.md, macos.md) are all **A** (user-facing
reference) — keep. Not shown above.

---

## 4. Cross-reference patch plan

Deleting the **C** docs would dangle these links from **surviving** docs. Each
gets repointed to the durable replacement (or the link demoted to plain text
when the target was purely historical). `yolo_jail.egg-info/` refs are ignored —
that dir is a stale Python-build artifact (untracked, not shipped).

| Surviving doc (link source) | Currently points to (archived) | Repoint to |
|---|---|---|
| [`../reference/macos-no-vm-direction.md`](../reference/macos-no-vm-direction.md) (×3) | `plans/macos-backend-direction.md`, `plans/macos-nix-shell-backend-proposal.md` | `plans/macos-revival-and-distribution-plan.md` [§0](macos-revival-and-distribution-plan.md#0-standing-decisions--do-not-relitigate) (the standing decision), drop the "reads with" line for the excised doc |
| `docs/plans/macos-revival-and-distribution-plan.md` (Inputs header) | `handoff-macos-post-ejection.md`, `macos-nix-shell-backend-proposal.md` | reword to "(archived — see git history)"; the plan already contains their conclusions |
| `docs/research/macos-support-matrix.md` | `handoff-macos-user-revive-plan.md` | repointed to the revival plan |
| `docs/research/macos-linux-builder-explained.md` (×2) | `handoff-macos-ondemand-builder.md` | `research/macos-container-builder-exploration.md` (the live builder direction) |
| [`../reference/mise-node-dynamic-linking.md`](../reference/mise-node-dynamic-linking.md) | `handoff-macos-ondemand-builder.md` | same as above |
| `docs/research/claude-token-logouts.md`, `claude-oauth-refresh-mechanics.md` (×3) | `plans/claude-oauth-mitm-proxy-plan.md` | `bundled_loopholes/claude-oauth-broker/README.md` (live broker architecture) |
| `docs/guides/loopholes.md` | `plans/claude-oauth-mitm-proxy-plan.md` | same broker README |
| `docs/guides/macos.md` | `plans/macos-backend-direction.md` | [`../reference/macos-no-vm-direction.md`](../reference/macos-no-vm-direction.md) |
| `docs/research/rocm-gpu-jail-findings.md`, `docs/reference/rocm-passthrough.md` | `rocm-memlock-handoff.md` | keep the *design* doc's own [§7.2](../reference/rocm-passthrough.md#the-memlock-clamp) (the handoff's durable content); demote the handoff link to "(resolved; see git history)" |
| `docs/qa/macos-user-review-findings.md` | `handoff-macos-user-revive-plan.md` | this doc is itself being archived, so no repoint needed |

## 5. Decisions (settled with the reviewer) + the executed reorg

### Where do the **B** (active-design) docs live? → visible split, executed

`docs/plans/` becomes the **single home for active plans + designs** (the work
we're currently navigating). Concretely:

- `docs/plans/macos-revival-and-distribution-plan.md` — stays (roadmap of record).
- `docs/plans/agent-settings-composition.md` — stays (design of record; Phase C
  complete 2026-07-22 — the prism is the unconditional boot + check config path
  and the bespoke agent-config `Configure*` writers are deleted).
- `docs/plans/handoff-cachix-cache.md` — **moved here** from
  `docs/implementation/` (it's the active D4 procedure). `docs/implementation/`
  is then empty and removed — a "handoffs" dir was a Python-era working-doc
  bucket; live procedures belong with the plans.
- `docs/research/macos-container-builder-exploration.md` — **stays in
  `docs/research/`**. It's the J3 builder direction but reads as a research /
  open-questions doc; moving it would break its matrix/explainer back-links for
  no clarity gain.
- New `docs/plans/README.md` — a one-screen index of what's active and its
  status, so "what are we working on?" has one answer.

The taxonomy after the reorg: `docs/plans/` = **B (active)**; `docs/design/` +
`docs/research/` + `docs/guides/` = **A (reference)**; **C** lives in git history.

### Archive by `git rm` (not `docs/archive/`) — done

`git rm`, matching commit `2c229fb`. History preserves everything;
`git log --follow <path>` recovers any archived doc.

### The two hybrids → rewritten as firm Reference, done

`jail-state-separation-design.md` and `mise-host-jail-path-mismatch.md` were
rewritten (not just bannered): headers reframed to "Reference" / "incident
record," open-questions sections converted to settled-decisions, and their links
to archived docs repointed. They no longer read as in-flight work.

<!-- changelog -->
- [5eb1643] Rewrote jail-state-separation-design header + "Open Questions"→"Decisions (settled)" so it's firmly Reference, not a hybrid decision-surface; repointed its archived-doc links.
- [9721660] Proposed + executed the reorg: docs/plans/ is the single active home, moved handoff-cachix-cache there, removed the empty docs/implementation/, added docs/plans/README.md index; research builder-exploration stays put.
- [5eb1643] Archived the 12 done/obsolete docs via git rm (repo precedent 2c229fb), not a docs/archive/ move.
- [5eb1643] De-hybridized both flagged docs (jail-state-separation-design, mise-host-jail-path-mismatch): rewritten as Reference/incident records, not "plan/decision surface" framing.

---

## The 2026-09-12 graduation assessment — the five docs the sprint built

> [!IMPORTANT]
> **ASSESSMENT ONLY. No graduation was performed.** Graduating five docs is its own body of work
> and the maintainer chooses whether and when ([`OQ-DT1`](#open-question)). What follows is a
> per-doc verdict, walked against the code rather than against the status lines — which is what
> [the lesson for the next run](#the-lesson-for-the-next-run) asked of this one.

A 138-commit sprint built five accepted designs: [`config-ownership-and-promotion.md`](../design/config-ownership-and-promotion.md),
[`report-tiers.md`](../design/report-tiers.md), [`lua-transform-removal.md`](../design/lua-transform-removal.md),
[`macos-user-home-tiers.md`](../design/macos-user-home-tiers.md) and
[`macos-user-provisioning.md`](../design/macos-user-provisioning.md). The `design-doc` genre's
last phase says a built design *graduates* into a `system-doc`; this run asks, for each, whether
it has earned that.

### Why this run asks a different question from the last two

The 2026-07-03 run **deleted**. The 2026-09-09 run **routed**. Neither could ask this question,
because the docs were not built yet. The question now is narrower and has a sharper failure mode
than either: a premature graduation does not merely misfile a doc, it **converts a proposal into
an assertion**. A design doc that describes unmeasured behavior reads as an argument, which is
honest. The same sentences under a `status: current` header with a `verified:` stamp read as a
measurement, which is a lie the reader has no way to detect.

### The bar — and the local one, which is stricter

The genre bar is `system-doc`'s: present tense, no sequencing, no Open Questions, claims anchored
to symbols rather than line numbers, rulings surviving only where a maintainer would otherwise
undo them.

The **local** bar is what decides the two macOS docs, and it is mechanical rather than a
judgement call: **all 41 docs in `docs/reference/` carry a `verified_commit:`**, measured
2026-09-12 —

```console
$ cd docs/reference && ls *.md | wc -l ; grep -l '^verified_commit:' *.md | wc -l
41
41
```

A `verified:` stamp is a claim about work somebody did. It is the line `system-doc` calls the most
valuable in the file, because it tells the reader how far to trust everything above it. A doc that
cannot honestly carry one cannot enter that tree without devaluing the other forty.

The local **shape** of a graduation is already set too, by
[`composed-file-permissions.md`](../design/composed-file-permissions.md) on 2026-09-09: the settled
body moved to [`../reference/composed-file-permissions.md`](../reference/composed-file-permissions.md)
and a **stub stayed at the original filename** carrying the live questions, so the roadmap rows
that route them keep resolving. That is the precedent, and it is what
[the rule about live questions](#what-the-disposition-is-not-allowed-to-do) requires.

### Verdicts

| Doc | Fully built? | Verifiable here? | Verdict |
| :--- | :--- | :--- | :--- |
| [`report-tiers.md`](../design/report-tiers.md) | **Yes** — all eight steps | **Yes, and measured with a control** | ✅ **Graduate first** |
| [`config-ownership-and-promotion.md`](../design/config-ownership-and-promotion.md) | **Yes** — the named hole closed 2026-09-12 | Yes | ⏸ Graduate second, and no longer on the hole: on [§11](../design/config-ownership-and-promotion.md#11-success-criteria)'s criterion |
| [`lua-transform-removal.md`](../design/lua-transform-removal.md) | **Yes** | Yes | ↩ **Do not graduate — archive.** There is no system to describe |
| [`macos-user-home-tiers.md`](../design/macos-user-home-tiers.md) | Code yes, behavior unrun | **No** | ⛔ Blocked on a Mac |
| [`macos-user-provisioning.md`](../design/macos-user-provisioning.md) | Code yes, behavior unrun | **No** | ⛔ Blocked on a Mac |

---

### 1. `report-tiers.md` — graduate first, and it is not close

All eight steps of [§9](../design/report-tiers.md#9-what-i-would-build-in-order) are in the tree.
Walked by symbol: the tier vocabulary (`reportTier`, `tierRun`, `tierLoss`) and the survey that
carries it in `internal/cli`; `printHostApplyVerdict` and `hostApplyVerdict`; the grouped remedies;
the default/detail split; the dependency pre-flight and its gate (`hostDepBlockers`, `gateHostDeps`);
`hostApplyDoc` with the `--assert` refusal of `--format json`; `launchLog` and `LaunchLogName` in
`internal/cli/run`; `catalogPrefix` in `internal/entrypoint`.

**What makes this one different is that its central claim is reproducible in this jail, with a
control.** The jail's baked `/bin/yolo` is 150 commits behind `HEAD` — it predates the entire
sprint — so it is a free before-image. Against the same home, measured 2026-09-12:

| Binary | Default report | Verdict line | `--verbose` |
| :--- | :--- | :--- | :--- |
| Baked `/bin/yolo` (pre-sprint) | **278 lines** | none — closes `observe only — nothing written` | inert, still 278 |
| Fresh `dist-go` build at `HEAD` | **30 lines** | `An --assert would complete.` | **267 lines** |

That is [the doc's own claim](../design/report-tiers.md) — *"the default report is 30 lines where
it was 278, and `--verbose` carries the 267-line detail view"* — reproduced exactly, including the
`--assert --format json` refusal. **No other doc of the five offers a measurement like this**, and
it is the difference between a reference that asserts behavior and one that has watched it.

Its principles are already load-bearing outside the doc: [`AGENTS.md`](../../AGENTS.md) cites
`P4` and [`OQ-RO3`](../design/report-tiers.md#11-decision-ledger) as the rule that *a launch has no quiet mode*, and `TestTheLaunchHasNoQuietFlag`
in `internal/cli` pins it. Those are exactly the near-immortal lines a reference exists to hold —
and they currently resolve into the planning tree.

**Where it would live:** `docs/reference/report-tiers.md`. It would say: the four report tiers and
what each class of line is for; the verdict-line contract (every run ends stating its own result,
on every branch); the default/`--verbose` split and why the rationale moved to the manual; the rule
that a declared-but-missing dependency is a **blocker**, so the dry run reports it and `--assert`
refuses over it; the launch stream under the same tiers, carrying `P4` and the disclosure boundary
verbatim; and the machine-consumer boundary — `--format json` belongs to the dry run, and an acting
verb refuses it rather than growing a second output mode. Six `OQ-RO` ids are cited from Go
(`RO1`, `RO2`, `RO3`, `RO4`, `RO5`, `RO7`) and must survive in a `## Why it's this way` appendix.

⚠ **The one real editorial cost:** [§2](../design/report-tiers.md#2-what-exists-today-measured)
and [§3](../design/report-tiers.md#3-the-diagnosis) are a line-by-line measurement of a report that
no longer exists. They are the best evidence in the doc and they are precisely what `system-doc`
says to cut as *the before-and-after framing*. The 278 → 30 pair belongs in the commit message and
in the roadmap row being closed, not in the reference.

[`report-tiers-plan.md`](../design/report-tiers-plan.md) — self-declared **CONSUMED** — archives
alongside it. It is an `implementation-plan` artifact, and its whole subject shipped.

### 2. `config-ownership-and-promotion.md` — the named hole is closed, and the harder blocker is not

Steps 1–7 of [§10](../design/config-ownership-and-promotion.md#10-what-i-would-build-in-order) are
built and verifiable: `HostManagement` / `KnownHostManagements` / `HostManagementDeclared` in
`internal/config` with `validateHostManagement` behind them; `yolo config promote` across three
files in `internal/cli` including the sensitivity deny-list; `yolo host apply --revert` consuming
the provenance record; the `reads-host` restructure landed on both halves —
`manifest.Surface.ReadsHost` is the declaration and `HasHostLayer()` is the predicate, with five
independent readers, and the jail is a witness through `packload.ParseHostLayerReport` on the
`YOLO_HOST_LOOPBACK` pattern.

**Step 8 shipped with a hole, and the hole is CLOSED as of 2026-09-12.** The `own` mode, its
capture store and host-side `reset` shipped on 2026-09-11; the **one-time adoption archive** landed
the next day — [`adoptionarchive.go`](../../internal/entrypoint/adoptionarchive.go), one call from
the stateful writer both notches share, with
[`OQ-CO7`](../design/config-ownership-and-promotion.md#13-decision-ledger)'s ledger row and
[§6.3.3](../design/config-ownership-and-promotion.md#633-what-survives-as-a-guard) rewritten to what
was built. `hostrender.go` and `staterender.go` no longer carry the ⚠ comments that recorded the gap.

`system-doc`'s rule for this is explicit: anything specified-but-absent **does not appear in present
tense**, and must be dropped or become a roadmap item **by name** rather than evaporating with the
design doc. That is what happened — [`roadmap.md`](roadmap.md) carried it as a 📦 row for one day —
so this blocker is spent and the graduation no longer waits on it.

⚠ **It closed with residue, and the residue is now graduation content rather than a footnote.**
Verification after the build measured **four gaps** — a different four
from the criterion's four axes below, and unrelated to them. **Three were fixed on 2026-09-12**:
two of them things the GATE could not tell apart — yolo's own output from the user's file (which is
what let `yolo config reset` spend the one-per-surface slot), and an absent file from an
existing-but-unreadable one — and the third the REPORT rather than the net, a verdict that closed
an adopting run as *"nothing to apply"* over the archive disclosure printed above it. **One
remains** — a deleted overlay sidecar dropping keys with no archive and no loss line — recorded
in [§6.3.3](../design/config-ownership-and-promotion.md#633-what-survives-as-a-guard), unfixed,
because every candidate fix reverses a ruling. In a design doc that sits honestly as live residue.
**In a reference it becomes present tense** — *here is what the net does and does not catch* — which is the
same kind of re-statement the criterion below needs, and it is the second thing the rewrite owes.

⚠ **The subtler blocker, and the more dangerous one.** The design's own success criterion —
*switching to `own` on a home already applying under `assert` changes zero bytes* — is **PARTLY
MET**, on four axes measured 2026-09-12: a top-level `null` and a top-level `{}` are **deleted**,
JSON key order is **sorted** at every depth, and a TOML surface's user comments are **destroyed**.
The first two are silent key deletion that no loss gate prompts for. In a design doc this sits
honestly as a failed criterion under a `> [!WARNING]`. In a reference it would have to be
re-stated as **the contract** — *`own` composes through the surface's codec, and here is what that
costs you* — which is a rewrite of the claim, not a re-filing of it. That rewrite is the real work
in this graduation and it should be done deliberately, not as a side effect of moving a file.

**Where it would live:** `docs/reference/config-ownership.md`, holding the ownership axis (the
notch does not decide who owns a file; a declared key does), the three modes and their surface
postures, promotion as the way out of capture including the precedence refusal, the deletion
asymmetry, and what the one-time adoption archive does and does not catch. Eight `OQ-CO` ids are cited from Go (`CO1`, `CO2`, `CO4`, `CO5`, `CO7`, `CO8`, `CO9`,
`CO10`).

### 3. `lua-transform-removal.md` — fully built, and that is why it must not graduate

The removal is complete, and it is the cleanest verification of the five because every instrument
is local:

```console
$ go list -deps ./internal/agentcfg | grep -c gopher-lua
0
$ go list -deps ./internal/packload | grep -c gopher-lua
4
```

The seam held exactly as [§4](../design/lua-transform-removal.md#4-the-boundary--what-goes-what-stays-what-moves)
drew it. `internal/luahook` no longer exists at the top level; the surviving package is
`internal/agentcfg/luahook` and its entire exported surface is the derive sandbox — `GopherLuaVM`,
`DeriveCtx`, `DeriveVM`, `Derive`, `DeriveRegistration`, `DeriveRegistrations`. `Transform` and
`Result.Excluded` have zero non-test referents. `Enforce` lives in `internal/agentcfg` as the
pipeline's own step. The three surviving mentions of `LuaVM`, `ValidateSandbox` and `AllowedGlobals`
are all comments explaining what was removed, which is correct.

**But a removal doc has no system to graduate into.** What survived the cut is the pack derive
sandbox — and [`../reference/pack-system.md`](../reference/pack-system.md) is *already* its
reference: it names `internal/agentcfg/luahook` (`DeriveCtx`) in its components table, owns
[the derive slot](../reference/pack-system.md#the-derive-slot), and its layer stack already reads
`defaults < host < workspace < config-overlay < capture-overlay < computed(derive) < managed`, with
no transform in it. Graduating this doc would mint a **second authority for one subsystem** — which
is the failure [this file's own 2026-09-09 run](#where-the-work-was-hardest-and-why) named as the
shape that actually causes drift: *one fact, four copies, three of them wrong*.

So the disposition is **C, archive** — but not freely. [`OQ-LT1`](../design/lua-transform-removal.md#13-decision-ledger) is cited from Go, and there are 20
Go references and 32 doc references to the path. Per
[the Rule-IDs-are-an-API warning](#what-the-disposition-is-not-allowed-to-do), the cheap correct
move is to fold the seam rule and [`OQ-LT1`](../design/lua-transform-removal.md#13-decision-ledger) into `pack-system.md`'s why-appendix — one paragraph
saying *the VM is the derive path's alone, the transform half is gone, and `Enforce` is the
pipeline's not the VM's* — and then archive.

⚠ **One stale line to fix whatever is decided.** The doc's status says two rows of
[§5.6](../design/lua-transform-removal.md#56-documentation) are *"deliberately still open"*:
[`roadmap.md`](roadmap.md) and [`config-ownership-and-promotion.md`](../design/config-ownership-and-promotion.md).
**Both closed.** The ownership doc describes the transform in the past tense throughout, and the
roadmap's row 30 is ✅ with the doc sweeps recorded as closing 2026-09-12. The status line is
stale in the maintainer's favour, which is the direction nobody checks.

### 4 & 5. The two macOS docs — blocked, and the block is structural rather than editorial

Both are genuinely built in Go. `DeriveDarwinHomeLayout`, `InstallDarwinHomeLayout`,
`HomeFileRedirects` and `SandboxMiseData` all exist with real call sites; so do `internal/darwinpkg`
(the floor, with its drift and policy tests), `internal/provision`, `ReadProvisioningFailed` and the
`PROVISIONING FAILED` marker. The short suite is green.

**And not one runtime claim about either has been observed.**
[`macos-user-provisioning.md`](../design/macos-user-provisioning.md) says so in a `> [!WARNING]`
at the top: both halves were implemented from a Linux jail, where there is no `sandbox-exec`, no
`_yolojail` account, and `RunMacosUser` fails closed. What *is* measured is the nix evaluation and
the Go half against fake homes.
[`macos-user-home-tiers.md` §10](../design/macos-user-home-tiers.md#10-what-shipped) is built the
same way — it explicitly separates *"pinned by a test"* from *"reasoned and still owed a Mac"*, and
it names **two defects it does not fix**, both runtime behavior on a backend CI cannot run.

[The runbook](runbooks/macos-user-manual-checks.md) is unambiguous: items 5–9 are new as of
2026-09-12 and **none has ever been run**; they are a dependency chain, not a list. Items 1–4 did
pass on hardware 2026-09-10 — but those cover the pre-existing bootstrap, not this sprint's work.
And an agent cannot close the gap: `sudo -n true` reports that a password is required, and every
`macos-user` argv leads with `sudo --user=_yolojail`.

So the local bar decides it without any judgement call. The only honest `verified:` stamp these two
could carry would cover the unit tests and the nix eval — **not the backend's behavior, which is
the entire subject of both documents**. That is `system-doc`'s failure mode 1, *the doc that reads
authoritative and is wrong*, purchased deliberately.

> [!NOTE]
> **This is the one place where the design doc is the better artifact, not merely the older one.**
> Its genre frames unmeasured claims as argument, and both docs use that framing well — the
> corrections dated 2026-09-12 inside them (the mirror-ordering row that overstated its own rule,
> the `MISE_DATA_DIR` guard that tested a spelling instead of a property, the stage-abort inversion)
> are the visible result of it working. Graduating would strip exactly the framing that makes those
> claims safe to read. **What unblocks them is runbook items 6–10 on a Mac, and nothing else.**

---

### The inverse: one doc whose prose still reads as a proposal

Every one of the five carries an accurate build stamp — the sprint's doc-reconciliation commits did
their job. Sweeping the whole of `docs/design/` for the opposite failure turns up **exactly one**
instance, and it is not from this sprint:

**[`agent-auth-modes.md`](../design/agent-auth-modes.md)** is the only doc in the tree whose status
line asserts neither a build nor *"nothing built"*. It reads `ACCEPTED (2026-08-29)`, and beneath it:

- [§1](../design/agent-auth-modes.md#1-the-shape-of-the-gap-in-one-sentence) states the gap in the
  present tense — *"yolo models one credential channel per agent and **lacks** a first-class way to
  declare and swap cloud providers or auth modes from YOLO config or the CLI"*. That gap is closed.
- Its abstract still advertises `yolo --claude-auth=bedrock` and `yolo --agent-profile pi=glm` —
  flags that were built and then **deleted**, as the doc's own drift note records.
- **Its successor reference already exists.** [`../reference/providers.md`](../reference/providers.md)
  is `status: current`, stamped `verified_commit: 582ae850`, with a `covers:` perimeter over exactly
  the provider and profile code. `use_profiles` is live in `internal/config`, and the two earlier
  spellings are refused by name.

The doc survives only because a 2026-09-02 `> [!NOTE]` warns *"when the body below disagrees with
the code, the code is right"* — a patch over a status line that should read **SUPERSEDED**. It is
therefore not a graduation candidate at all: it is a **C-bucket archive whose replacement shipped on
2026-09-02 (`4e4ca2d1`) and is still filed as an accepted design.**

Re-stamping it is independent of every graduation below and was worth doing first, because it was
the only doc in the tree actively misrepresenting its own status.

> [!NOTE]
> **Done 2026-09-12 — and it was not the one-line fix this section first called it.** The stub
> treatment `noncontainer-nix-environment.md` got does not apply: `providers.md` carries no
> occurrence of `tavily`, `web_search`, `failover`, `429` or `refresh`, so [§2](../design/agent-auth-modes.md#2-measured-state--bedrock-teams-and-the-manual-switch)/[§3](../design/agent-auth-modes.md#3-core-principle-a-mode-is-a-bundle) (the measured
> Bedrock→Teams switch), [§6](../design/agent-auth-modes.md#6-capability-resolution--selective-tool-augmentation-the-web-search-pattern) (capability resolution), [§8](../design/agent-auth-modes.md#8-dynamic-overflow-what-is-reachable-and-what-is-not) (deferred failover and the measured
> `ANTHROPIC_BASE_URL` bearer leak) and [§9](../design/agent-auth-modes.md#9-traps-and-failure-modes) (the credential traps) have **no successor at all**.
> Retired in `pack-profiles.md`'s shape instead — `status: superseded`, a banner naming the
> successor, and a section-by-section table saying which half moved and which is kept. The body
> stays, because eight live anchors reach into it from seven docs and three Go files — one of them
> `internal/cli/config_ref.txt`, which documents that `required_capabilities` is accepted but
> unenforced *by citing that doc's [`OQ-CAP2`](../design/agent-auth-modes.md#12-decision-ledger)*. A stub would have pointed the shipped config
> reference at nothing.

### What a graduation would cost

Inbound references, counted 2026-09-12. Doc references need a path and a live anchor; **Go
references are invisible to `vantage-check`** and are the ones that go silently dangling.

| Doc | Doc refs | Go refs | `OQ` ids cited from Go |
| :--- | ---: | ---: | :--- |
| [`config-ownership-and-promotion.md`](../design/config-ownership-and-promotion.md) | 57 | 47 | 8 |
| [`macos-user-provisioning.md`](../design/macos-user-provisioning.md) | 53 | 27 | 3 |
| [`report-tiers.md`](../design/report-tiers.md) | 36 | 50 | 6 |
| [`macos-user-home-tiers.md`](../design/macos-user-home-tiers.md) | 34 | 17 | 1 |
| [`lua-transform-removal.md`](../design/lua-transform-removal.md) | 32 | 20 | 1 |

[`AGENTS.md`](../../AGENTS.md) itself cites `report-tiers.md` three times with anchors, for the
no-quiet-mode rule. That is an inbound reference a `docs/`-scoped grep misses, and so is
[`roadmap.md`](roadmap.md) — whose rows for this work close in the same commit as any graduation,
not after it.

### Recommendation

1. **Re-stamp [`agent-auth-modes.md`](../design/agent-auth-modes.md) as superseded** by
   [`../reference/providers.md`](../reference/providers.md). One line, no dependencies, and it stops
   the only doc in the tree that lies about its own status.
2. **Graduate [`report-tiers.md`](../design/report-tiers.md), alone.** It is the only one of the
   five whose behavior was reproduced with a control, its principles are already cited as law from
   [`AGENTS.md`](../../AGENTS.md), and it carries no unbuilt step. Archive
   [`report-tiers-plan.md`](../design/report-tiers-plan.md) in the same commit.
3. **Then [`config-ownership-and-promotion.md`](../design/config-ownership-and-promotion.md)**.
   [`OQ-CO7`](../design/config-ownership-and-promotion.md#13-decision-ledger)'s archive **is built**
   (2026-09-12), so what remains is two re-statements, not one: the zero-bytes criterion as a stated
   contract rather than a partly-met goal, and the archive's two remaining gaps as present-tense
   behavior. The first is still gated on a live ruling
   ([`OQ-CO12`](../design/config-ownership-and-promotion.md#12-open-questions)), which under
   [`composed-file-permissions.md`](../design/composed-file-permissions.md)'s precedent means a stub
   at the original filename rather than a reason to wait.
4. **Fold [`lua-transform-removal.md`](../design/lua-transform-removal.md) into
   [`../reference/pack-system.md`](../reference/pack-system.md) and archive it.** Do not mint a
   second reference for one subsystem.
5. **Leave the macOS pair in `docs/design/` until a Mac runs
   [the runbook](runbooks/macos-user-manual-checks.md) items 6–10.** They are not late; they are
   correctly filed.

**Graduate one at a time.** Each move rewrites content, re-points dozens of references and closes a
roadmap row, and batching them is how an anchor gets missed — the failure this file has now recorded
in both directions across three runs.

### Open question

1. 💬 **OQ-DT1: Graduate now, or hold the whole set until the macOS pair can move with them?**

   <!-- vantage: oq id=OQ-DT1 leaning="Graduate report-tiers alone and now; the pair may wait months for a Mac and there is no benefit to coupling them." -->

   _Leaning:_ Graduate [`report-tiers.md`](../design/report-tiers.md) alone, now. The macOS pair is
   blocked on hardware nobody can schedule, and holding a finished reference hostage to it keeps a
   shipped system described in the planning tree for months. The counter-argument is real but
   weaker: five graduations done together share one re-pointing sweep, and
   [`AGENTS.md`](../../AGENTS.md)'s *Where things live* table would be edited once instead of three
   times.

   **Answer:**
   > _(empty — fill in when decided)_
