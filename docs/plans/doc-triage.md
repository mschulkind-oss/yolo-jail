# Documentation triage — proposed reorganization (for review)

**Purpose:** classify every doc under `docs/` so obsolete/done working-docs get
archived while the reference + active-design docs stay. **The reorg has been
executed**: commit `5eb1643` git-rm'd the 12 C-bucket docs and repointed every
cross-reference; commit `9721660` moved `handoff-cachix-cache.md` into
`docs/plans/`, removed the now-empty `docs/implementation/`, and added
`docs/plans/README.md`. The "Action" column and the §4 patch plan below record
what was done.

## The three buckets (your taxonomy)

- **A — Reference (keep in place).** Describes a system that *still exists*, at
  the mental-model / high-level-component / strategy level. Drift-resistant
  because it explains *why/how* things work, not line-by-line specifics. If you
  want to understand a live subsystem, you open one of these.
- **B — Active design (keep, grouped).** A design for something we're *currently*
  implementing or still discussing — a tool to navigate the work. Proposed home:
  `docs/design/active/` and `docs/plans/` stays the active-plan home. (Or leave
  in place with an "ACTIVE" banner — your call in §5.)
- **C — Archive (remove; git history preserves it).** Done or obsolete working
  docs. Repo precedent (commit `2c229fb`) is `git rm`, not a move. Any inbound
  link from a surviving doc gets repointed to the replacement (§4).

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
| `macos-no-vm-direction.md` | **A** | "DECIDED (2026-07-16)" — the standing decision (compose macos-user + AC). Referenced by the revival plan §0. The strategy of record. | keep |
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
| `macos-support-matrix.md` | **A** | **The live tracker** (revival plan §0 names it authoritative). Never archive. | keep |
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
| `plans/macos-nix-shell-backend-proposal.md` | **C** | devShell mechanism superseded by buildEnv (revival plan §0); decisions folded into revival plan. | **archive** |
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
| `docs/design/macos-no-vm-direction.md` (×3) | `plans/macos-backend-direction.md`, `plans/macos-nix-shell-backend-proposal.md` | `plans/macos-revival-and-distribution-plan.md` §0 (the standing decision), drop the "reads with" line for the excised doc |
| `docs/plans/macos-revival-and-distribution-plan.md` (Inputs header) | `handoff-macos-post-ejection.md`, `macos-nix-shell-backend-proposal.md` | reword to "(archived — see git history)"; the plan already contains their conclusions |
| `docs/research/macos-support-matrix.md` | `handoff-macos-user-revive-plan.md` | repointed to the revival plan |
| `docs/research/macos-linux-builder-explained.md` (×2) | `handoff-macos-ondemand-builder.md` | `research/macos-container-builder-exploration.md` (the live builder direction) |
| `docs/design/mise-node-dynamic-linking.md` | `handoff-macos-ondemand-builder.md` | same as above |
| `docs/research/claude-token-logouts.md`, `claude-oauth-refresh-mechanics.md` (×3) | `plans/claude-oauth-mitm-proxy-plan.md` | `bundled_loopholes/claude-oauth-broker/README.md` (live broker architecture) |
| `docs/guides/loopholes.md` | `plans/claude-oauth-mitm-proxy-plan.md` | same broker README |
| `docs/guides/macos.md` | `plans/macos-backend-direction.md` | `design/macos-no-vm-direction.md` |
| `docs/research/rocm-gpu-jail-findings.md`, `docs/design/rocm-passthrough-design.md` | `rocm-memlock-handoff.md` | keep the *design* doc's own §7.2 (the handoff's durable content); demote the handoff link to "(resolved; see git history)" |
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
