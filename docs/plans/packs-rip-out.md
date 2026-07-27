# The rip-out: what's left to design before implementing

**Status:** plan, 2026-07-26. Written in answer to: *"how much more do we have to design to
start implementing this? I want to implement everything — the rest of the prism stuff,
extract things as packs, agents as packs, all of this. Full rip out and reimplement."*

**Short answer: three decisions, and then you can start — but not on the packs.** Two of the
three are one sitting each. The third is the fork. Meanwhile a substantial amount of work is
**unblocked right now** and is a prerequisite for the rip-out anyway, so "start implementing"
has a real answer today.

**Reads with:** [packs-and-the-prism.md](../design/packs-and-the-prism.md) (the conceptual
frame: phases, contribution kinds, typed exports), [what-yolo-is.md](../design/what-yolo-is.md)
(boundaries + where computation runs), [agent-config-packs.md](agent-config-packs.md) (the
concrete pack proposal — 1,694 lines, mostly still valid),
[composed-config-work.md](composed-config-work.md) (the existing prism work list).

---

## 1. What is already designed well enough to build

Do not re-design these. They are settled at the level of detail implementation needs:

| Area | Where | Confidence |
|---|---|---|
| pack unit, address grammar, fetch-on-host, lockfile, approvals, rollback | `agent-config-packs.md` §1–§7 | high — 1,694 lines, reviewed, with the rejected alternatives recorded so they don't get re-litigated |
| pack → prism **layer** seam (`Inputs.Workspace`) | §6.1 | high — implemented and tested, zero non-test producers |
| the 4 contribution kinds, `provision` vs `compose` phases | `packs-and-the-prism.md` §2.5 | high |
| capability handling (declare → auto-rebuild, no manual step) | §2.5 | high — rides the existing `packages` → `YOLO_EXTRA_PACKAGES` → auto-reload path |
| prism defect list + fixes | `composed-config-work.md` tranches 0–2 | high — each item has a named file and a verified failure |
| what we deliberately don't build | §12 | high |

**One structural caveat.** `agent-config-packs.md` was written on the assumption that packs
are *additive* to Go-owned agent support (bet A: "packs are a sharing feature"). A full
rip-out is bet B/C. Its **mechanisms** survive that change intact — fetch, lock, staging,
layers, phases. Its **phasing** does not: phase 2 "settings fragments" assumes builtin
surfaces stay in Go and packs contribute *fragments to them*. Under a rip-out, surfaces
*are* pack data. Treat §11 as a menu of work items, not an order.

---

## 2. What must be decided first

### 2.1 Where composition runs — **the fork** (item 3.9)

Host-side (before the container exists) or in-jail (today)?

This is not a task, it reprices everything: whether pack logic crosses the boundary, whether
pack failures are pre-flight or fail-open `genStep` warnings, whether `readonly` can be a
real `:ro` mount, and how capture/re-render works. Full argument in
[what-yolo-is.md](../design/what-yolo-is.md); it is available because composition never
probes the container.

**Must be decided before**: any pack work that involves computation, `:ro` posture work
(3.2), capture-timing work (3.3).
**Not blocking**: everything in §3 below.
**The risk is deciding it implicitly** by continuing to render in `entrypoint`.

Needs a Mac answer either way: **macos-user has no mount step**, so "compose then mount
`:ro`" has no meaning there and needs its own story.

### 2.2 Are projections data or code? (new, from typed exports)

The MCP-pack idea ([§2.6](../design/packs-and-the-prism.md)) needs one decision: is an agent
pack's projection of an exported type expressed as **data** (a small mapping language) or
**computation** (Lua)?

**This one has a spec, which makes it tractable in a sitting**: the four projections that
exist in Go today are the acceptance test.

| Agent | What the projection must do |
|---|---|
| codex | near-passthrough of `{command, args, env}` |
| gemini | passthrough **+ synthesize** `<lsp>-lsp` entries from a *different* export type |
| claude | passthrough into `mcpServers` + tombstone-prune managed names |
| **opencode** | **rename** `env`→`environment`, **fold** `command`+`args` into one array, **inject** two constants (`type:"local"`, `enabled:true`) |

Opencode is the hard case and it rules out a plain key-map. Gemini's row is harder still —
it is a *cross-type* derivation (LSP → MCP), which is either a second export type consumed
by the same projection or genuinely computation. **Design against these four; do not design
in the abstract.** Depends on 2.1 only if the answer is "code."

### 2.3 What does "agents as packs" mean for the boot writers?

`Configure*Prism` is ~2,200 lines and not all data — claude's `.claude.json`
read-modify-write that must never wipe 33 keys of live state is the sharp example. Three
options: express it in the projection language (2.2), keep a Go remainder, or de-compose
those surfaces so the stateful reconciliation disappears (which
[composed-config-work.md](composed-config-work.md) 2.2b already wants for a *correctness*
reason, independently of packs).

**2.2b is the answer, and it is already on the list.** Decide it as "de-compose first, then
the remainder is small enough to be data" — but that ordering makes tranche 2 a **hard
prerequisite** of agents-as-packs, not a parallel track.

---

## 3. What you can implement today, blocked on nothing

This is the real answer to "how much more design before I start." **None of it is
pack work, and all of it is prerequisite to the rip-out.** Doing packs first means porting
known defects into a new mechanism where they're harder to see.

**Ordered:**

1. **Tranche 0 — remove `gemini`.** Subtractive, so every later table is smaller. Google is
   deprecating the CLI. Also deletes one of the four projections 2.2 must satisfy.
2. **Tranche 1 — the correctness cluster** (items 1.1–1.9). Independently shippable, ~one
   sitting each. Two of these matter disproportionately for the rip-out:
   - **1.9 `Surface.Transform` is inert** — a documented, schema-validated key that does
     nothing. This *is* the per-surface computation seam, so pack projections-as-Lua would be
     built on a hook that has never run. Fix before designing on it.
   - **1.7 + 1.8 skills gap + steering** — `pi`/`codex`/`opencode` get no skills at all.
     Two registry lines, and it is Phase 0 of the pack plan regardless.
3. **Item 3.8 — parameterize `/workspace` out of `builtin.go`.** Surface data currently
   hardcodes the jail path, so surfaces cannot become pack data until this lands. Small, and
   a hard prerequisite for agents-as-packs.
4. **Tranche 2 — the data-loss chain** (needs its one decision, 2.1-of-that-doc: how the
   engine distinguishes first-migration from user-asked-to-discard). Includes **2.2b
   de-compose**, which §2.3 above makes a prerequisite.

**Sequencing claim:** 1 → 2 → 3 → 4 is ~all of the existing prism work, unblocks every open
question above, and leaves the codebase in a state where "agents as packs" is a data-modelling
exercise rather than a rewrite. **Start here.**

---

## 4. Honest scope assessment of the rip-out

Since the ask is explicitly a full rip-out, the parts that deserve a flag before starting:

- **The four Go artifacts differ enormously in difficulty.** Registry (340 lines), surfaces
  (441), skills (files already), briefings (prose already) are genuine data — extract cleanly.
  Boot writers (2,207) are not, per §2.3. A plan that treats all four as one step will stall
  on the fourth.
- **`AgentSpec.HostFiles` must stay in Go.** It is a deliberately unwidenable allowlist; the
  retired `host_claude_files` keys are the counter-example. If a pack can name a host file,
  the pack system has reopened a closed credential hole. This is the one hard "never" and it
  should be settled in writing before the first agent pack.
- **Compile-time → runtime error trade.** `builtin.go` is type-checked; a pack is validated
  at best. Under 2.1-host-side this is largely mitigated (failures become pre-flight); under
  in-jail rendering with fail-open `genStep`, a malformed official pack means an agent boots
  silently misconfigured. **This is the strongest argument for deciding 2.1 before the
  rip-out, not after.**
- **Two pack kinds will exist** — embedded (official, offline, in the image) and fetched — so
  "structurally identical to a user pack" is aspirational. `bundled_loopholes/` shows the
  honest version: one `Discover`, two sources, and the difference is visible.
- **Backend parity is a real cost, not a footnote.** Apple Container can't bind-mount single
  files (`acMaterialize`); macos-user has no mounts at all. Every staging mechanism needs
  three answers, and the Mac ones can't be verified from here.

---

## 5. The answer, in one paragraph

**Design remaining: three decisions** — where composition runs (the fork, §2.1),
projections-as-data-or-code (§2.2, with a four-case spec so it's tractable), and what
agents-as-packs means for the stateful boot writers (§2.3, whose answer is "de-compose
first"). **Everything else is designed.** But none of those three block the ~15 items in §3,
which are all prerequisite to the rip-out and can start immediately. Do §3 first: it fixes
five verified defects, retires the inert `transform` key that a pack projection language
would otherwise be built on, and un-hardcodes `/workspace` from the surface data that has to
become pack data. Then decide §2.1, because it changes what the pack mechanism costs. Then
rip out.
