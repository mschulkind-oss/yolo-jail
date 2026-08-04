# Stage E — parked items

**What this is.** The design work deliberately *not* built, gathered in one place with the
reason each is parked and what it would take to unpark it. Written 2026-08-03, after the pack
system reached functional completeness, so that "what is left?" has one honest answer.

**Every item here is parked by a decision, not by an omission.** None is a defect in the pack
system. Two genuine defects used to live in this stage; §7 records what became of them —
one closed outright, one *believed* closed with the check that would confirm it — so neither is
re-opened from a stale reference nor assumed fixed on my word.

**Reads with:** [`BACKLOG.md`](BACKLOG.md) Stage E (the one-line rows this expands),
[`ROADMAP.md`](ROADMAP.md) item 3 (the composed-file follow-ups),
[`../design/composed-file-permissions.md`](../design/composed-file-permissions.md) (the audit
that surfaced most of this),
[`../design/host-nix-environment.md`](../design/host-nix-environment.md) (**potential future
work** — a reproducible tool environment at the `host` notch; see §6), and
[`handoff-guest-notch-macos.md`](handoff-guest-notch-macos.md) (the Mac-gated work, including
the one nix item that is a *prerequisite* rather than an option).

---

## Summary

| # | Item | Parked because | Unpark cost |
|---|---|---|---|
| E1 | `host_files` modes 4→3 (`copy` merges into `readonly`) | behavior change on a shipped key; **blocked on E2** | small once E2 is decided |
| E2 | `readonly` as a real `:ro` mount instead of `0o444` | needs a per-surface design pass — you cannot compose *into* a `:ro` mount | a design decision, then small |
| E3 | Capture timing (`yolo config capture` + capture on terminate) | **not urgent** — nothing is lost today, only observability lags | small; the cheap half is one subcommand |
| E4 | Comment preservation on `json`/`toml` surfaces | starts from decisions, not blank | cheapest useful step needs no comment parsing at all |
| E5 | `managed`/`defaults` array-append pinning | no user surface has ever needed it | small, but do not build it speculatively |

---

## E1 + E2 — the `host_files` mode collapse

These are one piece of work in two halves, and E1 cannot land first.

**Today** `host_files` has four modes. The proposal
([composed-file-permissions.md §7.4](../design/composed-file-permissions.md)) is to collapse to
three: `copy` merges into `readonly`, and `readonly` becomes a real `:ro` mount rather than a
`0o444` chmod.

**Why `0o444` is the wrong mechanism, precisely.** It is **asymmetric**: root ignores it
entirely, while a non-root agent gets `EACCES` — and the failure is silent, because the surface
simply stops re-rendering. So the same declaration means "advisory" for one user and "broken"
for another. A `:ro` mount is symmetric and honest.

**Why it is parked.** Two reasons, and the second is the real one:

1. `host_files` is a **shipped** key, so changing what a mode means changes behavior for
   existing configs.
2. **You cannot compose *into* a `:ro` mount.** That is the design pass E2 needs: a surface
   yolo renders and a file yolo mounts read-only are mutually exclusive at the same path, so
   every current `readonly` user has to be classified as one or the other. This is not a
   coding problem; it is a per-surface judgment call.

**Related, already learned:** `:ro` is only *non-persistence*, not confidentiality — this was settled by the prism ro/rw audit. Do not let the collapse imply a
security property it does not have.

---

## E3 — capture timing

**The lag.** A captured edit is written on the **next entrypoint run**, not when the edit
happens (measured 50× over 3 days in this workspace, so usually short).

**Nothing is lost, and this is the part that keeps it parked.** Every surface sits under a
host-backed rw bind and its baseline lives in the workspace, so both the edit and its baseline
survive `--rm` and the next boot captures normally
([composed-file-permissions.md §5.3](../design/composed-file-permissions.md)). What lags is
**observability**: a host-side `yolo config diff` inside that window under-reports.

**The fix, cheap half first.** A `yolo config capture` subcommand — an explicit checkpoint,
which also has the virtue of *naming the mechanism* to a user who does not know capture exists
— then capture in the existing `onTerminate` hook.

**Explicitly rejected: an inotify watcher.** It would solve staleness, not loss, at the price
of debounce logic plus a sidecar race. The cost lands on the boot path; the benefit is a
shorter window on something that is already not lossy.

---

## E4 — comment preservation

**The loss.** A `json` or `toml` surface is re-emitted canonically, so comments in the user's
file do not survive. Reported today as a `Formatting` line on the apply output (so it is
visible before the write) rather than silently dropped.

**Why it is parked, and why it is not blank.** The three sub-questions — staleness, attachment,
and in-jail additions — are already answered in `host-file-staging.md`, so this starts from
decisions rather than from scratch. `raw` is already lossless.

**The cheapest useful step needs no comment parsing at all:** a yolo-authored header pointing at
the `:ro` original. That is most of the value (a reader learns where their comments went) for
almost none of the cost.

---

## E5 — array-append pinning for `managed`/`defaults`

Object merge only, today. Arrays replace wholesale, matching RFC-7386 and the render engine's
`deepMerge`.

**Parked because no user surface has needed it.** It is shape-checked at config time, so a
config that expects append semantics fails **loudly** rather than silently doing the wrong
thing — which is what makes waiting safe.

This is the one item on the list worth naming as a **do-not-build-speculatively**: an append
mode is a second merge semantics for the same field, and the layer model's legibility is its
main asset.

---

## 6. Potential future work — a host nix environment

[`../design/host-nix-environment.md`](../design/host-nix-environment.md) is a full design study
for giving the `host` notch a reproducible tool environment via nix, rather than
`install_hints` printing a remedy the user runs by hand. It is **not** a Stage E item — it is
larger, it is a product question rather than a deferred repair, and §8 offers four options with
a recommendation instead of one plan.

What a reader should know before opening it:

- **§7 is the load-bearing finding**: a host tool environment is *not* orthogonal to
  confinement, which is why this cannot be treated as an isolated add-on.
- **§8 Option 1** (rename `yoloDarwinPackages` → system-neutral, un-hardcode
  `aarch64-darwin`, add a gcroot, report the resolved path) is a **prerequisite for
  env-manager Phase 7.2 regardless of what is decided about `host`**. It is tracked in
  [`handoff-guest-notch-macos.md`](handoff-guest-notch-macos.md) §5 for that reason.
- **§8 Option 2** (`yolo --at host -- <cmd>`) is the shape the codebase is already built for,
  but it is a bigger product claim: "yolo launches your host agent" rather than "yolo
  configures it."
- One verified fact worth carrying: `packages.yoloDarwinPackages` is **already per-system** and
  resolves for `x86_64-linux`. The name is the lie, not the mechanism.
- The devShell pollution was measured, not guessed: 22 PATH entries, 121 env vars.

---

## 7. Closed — do not re-open from a stale reference

These were listed as Stage E ⚠ *defects* (as opposed to parked improvements). Checked
2026-08-03 by reading code, and recorded here because a stale pointer to a fixed defect is
worse than no pointer. **Read the confidence note on the first one** — it is believed closed,
not observed closed.

- **`copilot/config` could lose an OAuth token.** It rendered `stateful` with
  `Defaults: {"yolo": true}` and no host layer, so an absent or corrupt sidecar reduced a
  token-bearing file to one key. **Believed closed:** `packs/copilot/pack.json` declares
  `mode: "rmw"` for that surface, and `rmw` keeps no sidecar at all (`prism.go:552-558`) — the
  existing file *is* the host layer, so there is no sidecar whose absence can reduce it. The
  specified "adopt-on-first-migration" fix is therefore unnecessary: the de-compose half alone
  removes the surface from the capture path.

  **Confidence caveat, stated because it matters for whether to trust this row.** That is read
  from the manifest and the render code, not observed: `copilot` is not in this workspace's
  pack list, so `yolo config ls` cannot show the surface here and the defect's original probe
  was not re-run. Before relying on it, select `copilot` and confirm `config ls` reports
  `rmw` (not `capture`) for `copilot/config`. The `rmw` mode has been on that surface since the
  pack was created (`6d4a050`), which raises a question this row cannot answer: whether the
  original defect report predates the pack manifest and described the *pre-pack* writer. Worth
  five minutes with a `copilot` pack selected before deleting the ROADMAP row.
- **BACKLOG E8, the nightly-macOS builder arch mismatch.** Closed 2026-08-03; it had **three**
  instances of one hardcoded-arch assumption rather than the one its entry named. One caveat
  remains and is not a defect: `publish.yml` is tag-triggered, so the multi-arch image does not
  reach GHCR until the next release and the nightly stays red until then.

**New finding, 2026-08-03 — `yolo pack lint` rejects every pack yolo ships.** All six fail
with *"pack has neither a skills/ dir nor an AGENTS.md — it would stage files nothing reads"*
(`internal/cli/pack.go:372`). Verified against `HEAD~1`, so it is **pre-existing**, not a
regression from the skills-`from` work that surfaced it.

The rule assumes a pack exists to ship CONTENT. yolo's own packs are config-only — `pack.json`
plus `derive.lua`, and `pi` is `pack.json` alone — so the premise is false for the packs the
tool is built around. Their `skills` contribution exists to *name the destination other packs
merge into*, which is a legitimate shape the rule cannot express.

This matters more than a cosmetic lint complaint: it is the check an author runs to learn
whether their pack is well-formed, and it currently says "no" to the six reference examples.
Whoever fixes it should decide whether the rule wants a *third* answer (a pack that declares
destinations but ships no files is fine) rather than loosening it into never firing — the rule
does catch a real authoring mistake for content packs.

**Still open, and NOT a Stage E item** (it is a validation gap, tracked in
[composed-file-permissions.md §4.5](../design/composed-file-permissions.md)): **reserved
destinations miss symlink targets.** `~/.config/git/config`, `~/.config/bashrc`, and
`~/.claude/claude.json` validate while their aliases are rejected. Verified still live —
`internal/config/helpers.go:87-95` resolves symlinks lexically with an `EvalSymlinks` fallback,
but the reserved-destination guard (`writablehome.go:54`) matches on the declared path.
