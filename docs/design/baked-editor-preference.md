---
title: "yolo has an editor, and it should not — neovim is baked into every jail on every backend"
date: 2026-09-22
status: draft
tags: [image, flake, core-floor, env, preferences, packs, macos-user]
summary: "neovim sits in coreFloorNames, so it is baked into the image AND installed into the darwin nix profile, and its stated justification is that yolo sets VISUAL=nvim unconditionally — a circular argument yolo wrote for itself. Seven places carry the preference. The fix is the move the repo already made for blocked tools: the core floor carries yolo's dependencies, a human's editor comes from their own config, and the only real question is what VISUAL becomes."
vantage:
  status-chip: true
---

# yolo has an editor, and it should not — neovim is baked into every jail on every backend

**Status:** DESIGN, 2026-09-22. Nothing built — re-verified 2026-09-24: `"neovim"` is still in
`coreFloorNames` and both `VISUAL=nvim` copies are still constants. Code is cited by symbol, not by
line.

> **In short.** The core floor should carry what yolo's own machinery cannot run without. An editor
> is a human's preference, and it belongs in that human's config.

**Why it matters.** The justification is circular, and it is written down as one:
the `coreFloorNames` entry in [`flake.nix`](../../flake.nix) says neovim is baked *"because the run env sets `VISUAL=nvim`
unconditionally … nvim must exist without any `mise_tools` entry."* yolo set that variable, so yolo
created the requirement it is now satisfying. Meanwhile a user who wants a different editor cannot
remove this one — `packages:` adds and never subtracts.

**The shape.** One entry leaves `coreFloorNames`; `VISUAL` stops being a constant; the
editor-specific boot step and aliases become conditional on somebody asking for them.

**Cost.** The image moves once for everyone (it moves for any `flake.nix` change), and `VISUAL`
becomes a thing that can be unset — which is a behaviour change for Copilot's ctrl-g.

**Start at [§2](#2-dependency-or-preference--the-test-the-floor-does-not-apply)** — the test that
decides what the floor is for. The seven sites fall out of it.

**Needs your ruling:** [OQ-ED1](#OQ-ED1), [OQ-ED2](#OQ-ED2), [OQ-ED3](#OQ-ED3), [OQ-ED4](#OQ-ED4).

**Reads with:** [`../reference/image-staging-vs-baking.md`](../reference/image-staging-vs-baking.md)
(what baking costs and when the image moves),
[`../reference/macos-user-provisioning.md`](../reference/macos-user-provisioning.md) (the
non-container floor, and [`OQ-P1`](../reference/macos-user-provisioning.md#oq-p1)'s exclusion list), [`program-delivery.md`](program-delivery.md) (the delivery routes an editor would
use instead).

---

## 1. Where the preference lives — seven sites

| # | Site | What it does |
| :--- | :--- | :--- |
| 1 | `coreFloorNames` in [`flake.nix`](../../flake.nix) | `"neovim"` in the image core, unconditional |
| 2 | `noncontainerFloorNames` in [`flake.nix`](../../flake.nix) | the **non-container** floor derives from the same list, so `macos-user` installs it into the darwin nix profile too |
| 3 | [`assemble.go`](../../internal/cli/run/assemble.go) | `-e VISUAL=nvim` on the container argv |
| 4 | [`shell.go`](../../internal/entrypoint/shell.go) | `export VISUAL=nvim` in the generated `.bashrc` — a second, independent copy |
| 5 | [`shell.go`](../../internal/entrypoint/shell.go) | `alias vi='nvim'` and `alias vim='nvim'` — yolo redirects two commands to a third program |
| 6 | `copyHostNvimConfig` in [`boot.go`](../../internal/entrypoint/boot.go), marked as the `nvim_config` boot step; the host half is the `--- host nvim config ---` bind in [`assemble.go`](../../internal/cli/run/assemble.go) | a whole boot step copying `/ctx/host-nvim-config` into `$HOME/.config/nvim` |
| 7 | the `/ctx` mountpoint `mkdir` in [`flake.nix`](../../flake.nix) | `$out/ctx/host-nvim-config` — an editor-specific mountpoint baked into the image |

The comment at site 1 also records that this was **promoted** rather than designed:
*"Was `mise_tools: {neovim: stable}` by default; a tool yolo wants in EVERY jail belongs in the
image, not a per-workspace mise store."* So the preference existed as a default `mise_tools` entry,
and the fix for a delivery problem moved it somewhere harder to remove.

> [!NOTE]
> **The briefing line was stale and has been corrected; it now has to move with this doc.**
> [`briefing.txt`](../../internal/cli/briefing.txt) used to tell every agent *"Editors: nvim
> (stable by default, configurable via `mise_tools`)"*. Since `ead07715` (2026-09-22) it reads
> *"Editors: nvim, baked into the image; override it with mise_tools"*, which is true today — and
> becomes false the moment [§3](#3-the-proposal) item 1 lands, so the briefing line is part of this
> change, not a follow-up.

## 2. Dependency or preference — the test the floor does not apply

The floor is worth having. The question is what belongs in it, and there is a test that separates
the entries cleanly: **does yolo's own machinery stop working if this is absent?**

| Entry | Absent ⇒ what breaks | Verdict |
| :--- | :--- | :--- |
| `git` | identity composition, `packsrc` | dependency |
| `mise` | the whole tool-provisioning path | dependency |
| `nodejs_24` | the MCP/LSP wrappers, every `via: npm` program | dependency |
| `cacert` | TLS for every child yolo spawns | dependency |
| `neovim` | **nothing** — except `VISUAL=nvim`, which yolo set itself | **preference** |

That is the whole argument. Every other entry is something yolo needs in order to be yolo; neovim is
something a person might like, and the only thing that "needs" it is a variable yolo chose to export.

**The repo has already made this exact move once.** `defaultBlockedList()` is **empty**, and
`grep -r`/`find` interception moved into an opt-in **`guardrails`** pack — because a default that
assumed the image bakes `rg` and `fd` was false on `macos-user`. Same shape: a helpful default,
baked on an assumption, relocated to something a user opts into.

**And `flake.nix` already states the principle, one backend short.** The non-container floor drops
its own copy of anything the user declared themselves, and says why (the comment on
`noncontainerFloorNames`, [`flake.nix`](../../flake.nix)):

> *"Dropping the floor's copy lets the user's spec win, which is the only answer that is not a
> surprise."*

That reasoning is not backend-specific. It is applied on `macos-user` only because that is where a
version collision made it urgent.

## 3. The proposal

1. **`neovim` leaves `coreFloorNames`.** The fatal floor gate needs no change — dropping an entry is
   one of the two fixes its own error message offers
   (`noncontainerFloorPackages`, [`flake.nix`](../../flake.nix)). A user who wants it writes one `mise_tools` or `packages`
   entry in their own config, which is where a preference belongs.
2. **`VISUAL` stops being a constant.** It becomes derived from what the user asked for, with no
   yolo-chosen default — [OQ-ED1](#OQ-ED1) is which form that takes. `EDITOR=cat` **stays**: it is
   not a preference, it is what stops `git commit` hanging an agent, and it has a stated reason.
3. **The two `.bashrc` aliases go**, or become derived from the same answer — [OQ-ED3](#OQ-ED3).
   Aliasing `vi` and `vim` to a third program is the strongest form of the opinion, because it
   overrides a command the user may have installed deliberately.
4. **The `nvim_config` boot step and its mount become conditional** on the user having asked for an
   editor at all — [OQ-ED2](#OQ-ED2). Today the step's only condition is that `/ctx/host-nvim-config`
   exists as a directory, and the image bakes that directory unconditionally (site 7), so the
   condition never does any work: the step runs on every boot, copying an empty directory when
   nothing is bound there. The host-side bind is gated only on the host having `~/.config/nvim` (and
   skipped on an Apple Container below the read-only floor), never on anyone having asked for an
   editor.

### What must not change

- **`EDITOR=cat`.** It is a dependency of agent behaviour, not a taste.
- **The `/lib` farm and the dynamic-linking story.** Removing an entry from the floor must not
  change how anything else resolves.
- **The floor's fatality.** A hole in the floor stays fatal; this removes an entry rather than
  softening the gate.
- **Anyone's existing setup, silently.** A user relying on baked nvim should be told once, not
  discover it when `vi` stops working.

## 4. Alternatives, with verdicts

| Alternative | Verdict |
| :--- | :--- |
| **A. Leave it; one editor in the image is harmless** | **Rejected** — it is not removable, so "harmless" is only true for people who happen to agree. `packages:` adds and never subtracts, which makes a baked preference permanent. |
| **B. Add an exclusion key so a user can drop a core entry** | **Runner-up, and the more general fix.** A `packages` subtraction would solve this and every future instance. Rejected *for this doc* because it is a new config surface answering a question that goes away once the floor holds dependencies only — and it would let a user break a real dependency. |
| **C. Move it to an opt-in `editor` pack** | **Plausible, and the closest match to the `guardrails` precedent.** It buys a place for the aliases, the `VISUAL` value and the host-config mount to live together. Costs a pack for what may be one config line. [OQ-ED2](#OQ-ED2) decides whether the host-config machinery needs a home like this. |
| **D. Keep it baked but make `VISUAL` configurable** | **Rejected as the worst of both** — it keeps ~200 MB of somebody else's editor in every image while admitting the preference was wrong. |
| **E. Put it back as a default `mise_tools` entry** | **Rejected** — that is where it came from, and site 1's comment records the real problem with it: a per-workspace mise store is the wrong place for something wanted in every jail. The answer is *not in every jail*, not *a different store*. |

## 5. Risks

| Risk | Mitigation |
| :--- | :--- |
| **R1.** Copilot's ctrl-g stops working for a user who set nothing. | [OQ-ED1](#OQ-ED1) is exactly this question. Whatever it rules, the launch says once that no editor is configured — the failure must not be a keystroke that silently does nothing. |
| **R2.** A user's muscle-memory `vi` breaks. | The aliases going is the visible half of the change and belongs in release notes. `vi` resolving to nothing is a clearer signal than `vi` silently being a different editor. |
| **R3.** The image rebuild lands on everyone at once. | It is one rebuild, and the image moves for any `flake.nix` change already. The image gets smaller, which is the direction [`minimal-disk-footprint.md`](minimal-disk-footprint.md) wants. |
| **R4.** `macos-user` loses an editor it only just gained. | That backend's floor exists so the backend is usable at all; an editor is not part of that claim. The exclusion machinery it already has is where a carve-out would go if one is wanted. |
| **R5.** The change is made and nothing fails when the removal is reverted. | The done-conditions in [§6](#6-what-done-looks-like) are observable states of a fresh jail, not test names. |

## 6. What done looks like

1. A fresh jail with an empty config has **no `nvim`** on `PATH`, and nothing in the launch or the
   boot log mentions an editor.
2. `VISUAL` in that jail is whatever [OQ-ED1](#OQ-ED1) ruled — and if that is "unset", it is unset in
   **both** copies (the container `-e` and the `.bashrc`), which is the pair that has drifted before.
3. `EDITOR` is still `cat`, and `git commit` in that jail still does not hang.
4. A user who declares an editor in their **own** config gets it, on both container backends and
   `macos-user`.
5. `yolo check` does not report the floor as holed, and `internal/darwinpkg/floor.go`'s drift gate
   still agrees with `flake.nix`.
6. The image is smaller than the previous generation, and nothing else moved.

## 7. Open Questions

1. 💬 <a id="OQ-ED1"></a>**[OQ-ED1](#OQ-ED1): what does `VISUAL` become?**
   `EDITOR=cat` is deliberate and stays, which makes `VISUAL` the human's only escape from it — it
   is what Copilot's ctrl-g uses to open a real editor. Three forms: unset unless the user names an
   editor; derived from a new config key; or derived from what the user's `mise_tools`/`packages`
   happen to provide. The stakes: whether "no editor configured" is a legible state or a keystroke
   that quietly does nothing.

   <!-- vantage: oq id=OQ-ED1 leaning="Unset unless the user names one, with the launch saying so once. A variable yolo invents is how the current circular justification happened; and an unset VISUAL makes ctrl-g fail in a way a user can act on, where VISUAL pointing at a missing binary fails in a way they cannot." -->

   _Leaning:_ **Unset unless the user names one**, with the launch disclosing it once. A
   yolo-invented value is how the present circularity happened, and an unset `VISUAL` fails in a way
   the user can act on, where a `VISUAL` pointing at a missing binary does not.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 <a id="OQ-ED2"></a>**[OQ-ED2](#OQ-ED2): does the host-nvim-config machinery survive, and where does it live?**
   Sites 6 and 7 are a mount, a boot step, a copy-merge into the `.config` overlay and a documented
   `macos`-guide behaviour — real machinery serving people who do want their own nvim in the jail.
   Either it stays in core gated on an editor being configured, or it moves to an opt-in pack, or it
   goes. The stakes: whether core keeps editor-shaped code after the preference leaves.

   <!-- vantage: oq id=OQ-ED2 leaning="Move it to an opt-in pack. It is the guardrails precedent applied exactly: a genuinely useful editor-specific behaviour that core has no reason to know about, and a pack is where the mount, the boot copy and the VISUAL value can travel together." -->

   _Leaning:_ **Move it to an opt-in pack** — the `guardrails` precedent applied exactly. The mount,
   the copy step and the `VISUAL` value are one feature, and a pack is where they can travel
   together instead of core knowing about an editor.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 <a id="OQ-ED3"></a>**[OQ-ED3](#OQ-ED3): do `alias vi` / `alias vim` go, and is there a general rule?**
   These are the sharpest edge: yolo rewrites two commands to a third program, so a user who
   installs real `vim` still gets nvim. Dropping them is easy; the question worth asking is whether
   the repo wants a stated rule — *core never aliases one program's name to another's* — since a
   one-off deletion invites the next one. The stakes: a line in `AGENTS.md`, or nothing.

   <!-- vantage: oq id=OQ-ED3 leaning="Drop them, and state the rule. The blocker shims already occupy the legitimate version of this (refuse and suggest, never silently substitute), so an alias that silently substitutes is the same act without the disclosure." -->

   _Leaning:_ **Drop them and state the rule.** The blocker shims already hold the legitimate form of
   this — refuse and suggest, never silently substitute — so an alias that silently substitutes is
   that act with the disclosure removed.

   **Answer:**
   > _(empty — fill in when decided)_

4. 💬 <a id="OQ-ED4"></a>**[OQ-ED4](#OQ-ED4): is `neovim` the only preference in the floor?**
   [§2](#2-dependency-or-preference--the-test-the-floor-does-not-apply)'s test is cheap to apply to
   the rest of the list, and `gh` is the obvious next candidate — nothing in yolo requires the GitHub
   CLI. `ripgrep` and `fd` are the interesting case: they were dependencies of the *default* blocked
   list, and that default is now empty and opt-in, so their justification may have moved out from
   under them. The stakes: whether this is one removal or a floor audit.

   ✅ **MEASURED 2026-09-22, and the answer is: it is a floor AUDIT.** `coreFloorNames` holds **36**
   entries (not the ~40 this doc guessed), and `darwinpkg.ImageCoreNames` matches it entry-for-entry,
   so the drift gate holds. Applying [§2](#2-dependency-or-preference--the-test-the-floor-does-not-apply)'s
   test mechanically — zero exec, zero env reference, zero manifest reference across `internal/`,
   `cmd/` and `packs/` — splits them **21 dependency, 13 preference, 2 pack-dependency**.

   The 13 with no consumer: `neovim`, `gh`, `sox`, `overmind`, `which`, `gnupatch`, `diffutils`,
   `gzip`, `bzip2`, `xz`, `gnutar`, `unzip`, `zip`. In measured-bytes order the only ones worth a
   decision are **neovim ~220 MB, sox ~100 MB, gh 41 MB, overmind 18 MB**; the remaining nine are
   under 10 MB combined and are not worth a doc between them.

   > [!WARNING]
   > **Two of this question's own guesses were WRONG, and both would have cost something.**
   >
   > - **`ripgrep`/`fd` are NOT stale.** They are live dependencies of `packs/guardrails`, which
   >   declares `requires` for both — and a blocker shim is generated only when its `replacement`
   >   resolves on the agent's PATH. Drop them and guardrails **silently stops blocking** `grep`
   >   and `find`, which is the failure mode that pack exists to prevent.
   > - **`overmind`'s baked justification is false, but not for the reason to act on.** `flake.nix`
   >   says "exercised by overmind isolation tests"; the test only echoes `$OVERMIND_SOCKET` and
   >   `cat`s the socket path. The binary is never run.
   >
   > And one adjacent doc defect the audit found: `briefing.txt` tells every agent "GitHub CLI (gh)
   > is pre-authenticated", but nothing grants a token and `~/.config/gh` does not exist in a jail —
   > so that line is false regardless of what this question rules.

   _Leaning:_ **Rule on `neovim` alone here; the table above is what a follow-up sprint would take.**
   That was the leaning before the measurement and it survives it — the audit's value is that the
   follow-up now has a priority order set by bytes rather than by guesswork.

   **Answer:**
   > _(empty — fill in when decided)_

## 8. Decision Ledger

No rulings yet. Rows land here as [§7](#7-open-questions)'s questions are answered, and the ruling
moves into the body section it governs.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| — | — | — | — | — |
