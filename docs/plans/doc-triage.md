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
| `../reference/macos-no-vm-direction.md` (×3) | `plans/macos-backend-direction.md`, `plans/macos-nix-shell-backend-proposal.md` | `plans/macos-revival-and-distribution-plan.md` [§0](macos-revival-and-distribution-plan.md#0-standing-decisions--do-not-relitigate) (the standing decision), drop the "reads with" line for the excised doc |
| `docs/plans/macos-revival-and-distribution-plan.md` (Inputs header) | `handoff-macos-post-ejection.md`, `macos-nix-shell-backend-proposal.md` | reword to "(archived — see git history)"; the plan already contains their conclusions |
| `docs/research/macos-support-matrix.md` | `handoff-macos-user-revive-plan.md` | repointed to the revival plan |
| `docs/research/macos-linux-builder-explained.md` (×2) | `handoff-macos-ondemand-builder.md` | `research/macos-container-builder-exploration.md` (the live builder direction) |
| `../reference/mise-node-dynamic-linking.md` | `handoff-macos-ondemand-builder.md` | same as above |
| `docs/research/claude-token-logouts.md`, `claude-oauth-refresh-mechanics.md` (×3) | `plans/claude-oauth-mitm-proxy-plan.md` | `bundled_loopholes/claude-oauth-broker/README.md` (live broker architecture) |
| `docs/guides/loopholes.md` | `plans/claude-oauth-mitm-proxy-plan.md` | same broker README |
| `docs/guides/macos.md` | `plans/macos-backend-direction.md` | `../reference/macos-no-vm-direction.md` |
| `docs/research/rocm-gpu-jail-findings.md`, `docs/reference/rocm-passthrough.md` | `rocm-memlock-handoff.md` | keep the *design* doc's own [§7.2](../reference/rocm-passthrough.md#72-locked-memory-limit-blocks-queue-creation-in-jail--resolved-by-rocm-72-userspace-2026-06-06) (the handoff's durable content); demote the handoff link to "(resolved; see git history)" |
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
