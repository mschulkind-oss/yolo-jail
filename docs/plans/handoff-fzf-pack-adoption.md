# Handoff — finishing the fzf pack, and what changed under it

**Audience:** the next agent picking up `docs/examples/claude-fzf-pack/` (or the maintainer
adopting it by hand).
**Written:** 2026-08-02, after the pack was built and verified at both notches.
**Status of the pack itself:** **works, committed, verified.** This handoff is about the four
things that are *not* done, plus the context that changed while it was being built.

The pack's own README (`docs/examples/claude-fzf-pack/README.md`) covers what it contains and
how to adopt it — read that first, and do not duplicate it here. This doc is only what a
successor needs that the README does not say.

---

## 1. The one thing that must be checked on the HOST before adoption

**The `fileSuggestion` protocol is almost certainly not what the maintainer's real script
assumes**, and this is the highest-value finding of the whole exercise.

Read out of the Claude Code binary (v2.1.220) and independently re-verified:

```js
// executeFileSuggestionCommand, decompiled
let i = Ie(e),                                   // the query, JSON-SERIALIZED
    s = { type: "command", command: n.command },
    a = await q2o(s, "FileSuggestion", "FileSuggestion", i, …);
if (a.aborted || a.status !== 0) return [];      // non-zero exit discards EVERYTHING
return a.stdout.split("\n").map(l => l.trim()).filter(Boolean)   // then capped at 15
```

So:

| fact | consequence |
|---|---|
| the query arrives as **one line of JSON on stdin**, with a `query` field | **there is no `$1`** |
| a **non-zero exit discards all output** | a no-match `fzf --filter` (exit 1) must be swallowed |
| only the **first 15 lines** are used | ranking matters, volume does not |
| **5s timeout**, then results dropped | keep it fast |
| run via `bash -c`, cwd = project dir | `~` expands; relative paths are correct |
| skipped until **workspace trust** is accepted | yolo's claude pack pre-accepts for `${workspace}` |

**Action for whoever adopts this:** open `~/.dotfiles/claude/file-suggestion.sh` on the host
and check whether it reads `"$1"`. If it does, it has been receiving an **empty query** and
returning an unranked dump of the tree — working badly rather than failing, which is why it
would never have surfaced an error. That is a live (if quiet) breakage that predates all of
this work.

`bin/file-suggestion.sh` in the pack is a **reference implementation**, deliberately marked as
such. It implements the contract above correctly (verified: JSON query → ranked matches; empty
stdin → exits 0 without hanging via a bounded `read -t 2`; no-match → swallows exit 1). Swapping
in the real script is a one-file edit — nothing else in the pack references its internals, only
its path.

---

## 2. What is NOT done

### 2.1 The real script is not in the pack

`~/.dotfiles/` is host-side and invisible from a jail (the credential boundary — AGENTS.md
"Limitations"), so the real finder could not be copied in. The pack ships a working stand-in
rather than a guess presented as the maintainer's code. **This is intentional, not an
oversight** — but it does mean the pack is not yet *the* pack, it is the pack's shape.

### 2.2 No `program` contribution — and that is a workaround, not a design choice

The pack needs `fd` and `fzf`. Declaring them the obvious way **breaks the jail**, because a
`program` contribution generates a `~/.yolo-shims/<bin>` launcher that precedes `/bin` on PATH
and execs only `$NPM_CONFIG_PREFIX/bin/<bin>` — never PATH — so a failed install makes the
baked `/bin/fzf` unreachable and the shim exits 1.

Both tools **are already baked into the image** (`flake.nix:658` for `fd`, `:721` for `fzf`),
so omitting `program` is correct today and the pack works. But it means:

- the pack carries **no `install_hints`**, so `apply --host` cannot tell a host user to install
  `fd`/`fzf` — the exact capability Phase 8.3 just added;
- the omission is **invisible** to a reader who does not know why.

**Blocked on:** the Q1.x decisions in
[`../design/program-kind-defects.md`](../design/program-kind-defects.md). If Q1.3 lands a
presence-asserting kind (`requires`?), this pack should adopt it immediately — it is the case
that motivated the question.

### 2.3 The double `rendered` line is expected here

`apply --host` prints `claude/settings rendered` **twice** for this pack — once for the claude
pack's declaration, once for this one. That is ruling **R4** in
[`../design/pack-config-collaboration.md`](../design/pack-config-collaboration.md) and is
tracked as Option 1 in that doc, not a defect in this pack. Do not "fix" it here.

### 2.4 The pack declares `config`, which will eventually be wrong

The pack declares `agent: claude, name: settings` itself — Layout B in
`pack-config-collaboration.md`. The **designed** answer is `config-overlay` (Layout C), which
was inert when this pack was built and is being wired now.

**When `config-overlay` lands, this pack should convert**, becoming:

```jsonc
{ "kind": "config-overlay", "surface": "claude/settings",
  "config": { "managed": { "fileSuggestion": {
      "type": "command", "command": "~/.claude/bin/file-suggestion.sh" } } } }
```

That is strictly better: the claude pack stays the sole owner of `settings.json`, this pack
cannot alter the file's `mode`/`path`/`codec` even by accident, and provenance records which
pack set the key. **Until then Layout B is the working answer** and the pack is correct as
shipped.

---

## 3. The trap that is already defused, and must stay defused

🔒 **`mode` is deliberately OMITTED from the `config` contribution.** Do not add it.

`claude/settings` is `stateful` (the default). Declaring `mode: "rmw"` on the same surface
identity **silently replaces the whole surface definition** — `manifest.Merge` is last-writer-
wins (`internal/agentcfg/manifest/load.go:124`, `byKey[k] = s`) — flipping claude's settings
from `stateful` to `rmw` and **disabling in-jail edit capture for `~/.claude/settings.json`**
with nothing reported.

This is ruling **R1**: *"very harmful. my setup doesn't matter. this is a general mechanism."*

`pack.json` is strict JSON, so a `// why` comment fails to parse — which is exactly why the
rationale lives in the README under a "DO NOT ADD IT" heading. **If you edit the pack.json,
re-read that section first.** Verified state: `yolo pack lint` reports
`config claude/settings stateful → ~/.claude/settings.json`, and capture sidecars are present
*and populated* in-jail (`rmw` writes no sidecar at all, so their presence is the proof).

---

## 4. Context that changed while the pack was built

Everything below shipped in the same session and affects how the pack behaves. A successor
reading only the pack would miss these.

| change | why it matters to this pack |
|---|---|
| **`files` kind implemented** (jail + host) | the pack's script delivery *only just started working*; before, `files` was inert at every target while `pack lint` reported it fine |
| **exec bit now survives** `packstage`/`copyTree`/`host_files` | the script arrives `0o555`/`0o755` instead of `0o644`; `allow_exec` now grants the bit THROUGH, not just admission |
| **`allow_exec` is a CONSUMER opt-in** | the pack cannot self-grant it; the config entry needs `"allow_exec": true` or staging refuses. The error message now says so and names `~/.config/yolo-jail/config.jsonc` |
| **briefing is a delimited managed block** | the pack's prose is re-asserted idempotently inside markers; the user's own prose outside them is untouched |
| **`files` → `.claude` would shadow the settings surface** | why `into` is `.claude/bin`. A `files` tree is a `:ro` mount, so claiming the whole dir makes the boot refuse with "read-only file system". Now caught in pre-flight |
| **mount dedup for briefing + skills** | two packs at one destination used to fail with podman's duplicate-mount-destination; that is why this pack can declare a briefing at `.claude/CLAUDE.md` alongside the claude pack |
| **`install_hints` on all six shipped packs** (8.3) | the model this pack should follow once §2.2 unblocks — and the reason its absence is a gap rather than a non-issue |
| **manifests read tolerantly in-jail** (`DecodeTolerant`) | a new `pack.json` field no longer bricks a jail running an older baked image |

---

## 5. Three product defects this pack surfaced

All three are why §2.2 exists. Full context and the decisions needed are in
[`../design/program-kind-defects.md`](../design/program-kind-defects.md); Phase 11 of the plan
lists them as work items. Summarized so a successor does not rediscover them:

1. **A `program` contribution shadows a baked binary and breaks it** — the launcher execs a
   single hardcoded path and exits 1 rather than falling through to PATH. Verified for `fd` and
   `fzf`.
2. **Only the FIRST `program` per pack installs in a jail** — `InstallContribution()` returns
   inside its loop, while the host path's `DepRequirements()` returns all of them. Whether this
   is a bug or an unenforced one-per-pack rule is an open product question.
3. **A dropped pack's staged tree is never cleared, so it keeps rendering** — contradicts an
   invariant `AGENTS.md` states as fact. Observed live: a deleted test pack kept regenerating
   its broken shim until the dir was removed by hand.

---

## 6. Checklist for a successor

- [ ] Check the real `~/.dotfiles/claude/file-suggestion.sh` for `"$1"` (§1). **Do this first
      — it may be a live bug independent of the pack.**
- [ ] Copy the real script over `bin/file-suggestion.sh`, keep the filename and the exec bit,
      re-run `yolo pack lint --allow-exec <dir>`.
- [ ] Copy the pack to `~/.dotfiles/claude-fzf/` (or wherever personal packs live) and add the
      config entry from the README — **including `"allow_exec": true`**.
- [ ] Do NOT add `mode` to the `config` contribution (§3).
- [ ] When `config-overlay` lands, convert the `config` contribution to it (§2.4).
- [ ] When Q1.x is decided, add the `fd`/`fzf` dependency declaration with `install_hints`
      (§2.2).
- [ ] Expect two `claude/settings rendered` lines until R4 is fixed (§2.3) — not a bug here.

## 7. How to verify after any change

The sequence used originally, all against a throwaway `$HOME` under `mktemp -d` — **never a
real home**:

1. `yolo pack lint --allow-exec <pack dir>` → clean, and confirm it says **`stateful`**.
2. `yolo apply --host` (observe) → writes nothing.
3. `yolo apply --host --assert` → script at `0o555`, `fileSuggestion` present alongside
   claude's own `preferences`/`permissions`, briefing block written.
4. **Second `--assert` → byte-identical.** This is the test that catches an accumulating
   render; run it every time.
5. Nested jail, with the freshly built binary **by path**
   (`just build-go && ./dist-go/linux-$(go env GOARCH)/yolo -- bash -lc '…'`, `YOLO_REPO_ROOT=/workspace`)
   → script present, executable, and **runs**. Not bare `yolo` — that is the baked launcher.
   `git add` new files first; a nested image build only sees git-tracked files.
6. Confirm the mode did not flip: lint says `stateful`, and capture sidecars exist in-jail.
