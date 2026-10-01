---
title: "Handoff — finishing the fzf pack, and what changed under it"
status: accepted
stage: DECIDED
next: "The maintainer, on the host: copy the real finder over bin/file-suggestion.sh, then copy the pack out and select it at user scope (§6); nothing is left in the tree"
---

# Handoff — finishing the fzf pack, and what changed under it

**Audience:** the next agent picking up `docs/examples/claude-fzf-pack/` (or the maintainer
adopting it by hand).
**Written:** 2026-08-02, after the pack was built and verified at both notches.
**Status:** 2026-08-02 — the pack **worked, was committed and was verified at both notches**
([§7](#7-how-to-verify-after-any-change)). Re-checked against the tree 2026-09-30: of the four
things this handoff said were not done, the `requires` declaration shipped
([§2.2](#22-no-program-contribution--a-workaround-not-a-design-choice--adopted-requires-2026-08-03)),
and the two host-side copies ([§6](#6-checklist-for-a-successor)) are the maintainer's. The
pack stopped linting on 2026-09-23 and lints clean again from 2026-09-30, its prose moved under
`briefing/` and addressed to claude by name; the unit suite now lints every example pack, so the
next schema change that would strand it fails there first
([§2.5](#25-the-pack-does-not-lint--fixed-2026-09-30)). Nothing is left in the tree.

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

> **✅ CHECKED 2026-08-02 (from the host — no bug).** The real script reads the JSON from
> **stdin**, not `$1`:
> ```bash
> QUERY=$(jq -r '.query // ""')
> PROJECT_DIR="${CLAUDE_PROJECT_DIR:-.}"
> …fd … | fzf --filter "$QUERY" | head -15
> ```
> It already matches the decompiled contract on every point: stdin JSON with a `query`
> field, `head -15` matching the 15-line cap, and `| head` making a no-match `fzf --filter`
> (exit 1) irrelevant since the pipeline's exit status is `head`'s 0. It also reads
> `$CLAUDE_PROJECT_DIR` rather than assuming cwd, which the reference implementation does
> not.
>
> So the [§1](#1-the-one-thing-that-must-be-checked-on-the-host-before-adoption) hazard **did not apply to this user** — but the finding stands for anyone whose
> script uses `$1`, and the protocol table is the useful artifact either way.
>
> One real difference to preserve when swapping it in: the real script adds gitignored files
> from `scratch/` only, and excludes `.git`/`node_modules`/`__pycache__`/`.venv`. Don't lose
> that on the copy.

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

### 2.2 ~~No `program` contribution — a workaround, not a design choice~~ — ADOPTED `requires` 2026-08-03

The pack declares **`requires` for `fd` and `fzf`**, with `install_hints` for
brew/apt/dnf/pacman/nix. Q1.3 landed the presence-asserting kind and this pack adopted it
immediately, as predicted — it was the case that motivated the question.

What the workaround was, and why it is gone. Declaring the tools the obvious way **broke the
jail**: a `program` contribution generated a `~/.yolo-shims/<bin>` launcher that preceded
`/bin` on PATH and execed only `$NPM_CONFIG_PREFIX/bin/<bin>` — never PATH — so a failed
install made the baked `/bin/fzf` unreachable and the shim exited 1. Both tools **are** baked
into the image (`fd` through [`flake.nix`](../../flake.nix)'s `coreFloorNames`, `fzf` through
its `fullPackages`), so omitting `program` was correct at the time and the pack worked. The
cost was the two things this item flagged:

- the pack carried **no `install_hints`**, so `apply --host` could not tell a host user to
  install `fd`/`fzf` — the exact capability Phase 8.3 had just added;
- the omission was **invisible** to a reader who did not know why.

Three fixes had to land, and the order matters because each was hiding the next:

1. **the PATH split** (2026-08-02) — launchers moved to `~/.yolo-launchers`, ordered after
   `/bin`, so an installer can no longer shadow a baked binary;
2. **the plural install accessor** (2026-08-03) — only the *first* `program` per pack
   installed, so even post-split a two-binary pack could not be expressed;
3. **the `requires` kind** (2026-08-03) — the actual fix here. Neither 1 nor 2 would have
   been right on their own: `program` means *"yolo installs this"*, and the pack's claim is
   *"this must exist"*. `requires` asserts presence, generates nothing (so nothing to
   shadow), reports a missing bin by name at boot, and feeds `check-deps`/`yolo host apply`
   through the same hint plumbing.

The `nix` hints stay on **this** pack while the six shipped agent packs dropped theirs, and
the asymmetry is the rule: `fd`/`fzf` are genuine third-party deps where the user's package
manager is right and nixpkgs is current, whereas an agent CLI ships its own installer *and*
updater (and nixpkgs was 16 releases behind on `github-copilot-cli`).

### 2.3 ~~The double `rendered` line is expected here~~ — FIXED 2026-08-02

`apply --host` used to print `claude/settings rendered` **twice** for this pack — once per
declaring pack. That was ruling **R4** in
[`../reference/pack-system.md#config-surfaces-and-the-compose-engine`](../reference/pack-system.md#config-surfaces-and-the-compose-engine), and Option 1
settled it by **refusing** the clash rather than deduping the line: two `config` declarations of
one identity are now a collision, so the state that produced the second line cannot arise. An
overlay-based pack (which this one now is) prints exactly one line — and always did.

### 2.4 ~~The pack declares `config`, which will eventually be wrong~~ — CONVERTED 2026-08-02

The pack declared `agent: claude, name: settings` itself — Layout B in
`pack-config-collaboration.md`. It now declares Layout C, which is what it should always have
been:

```jsonc
{ "kind": "config-overlay", "surface": "claude/settings",
  "config": { "managed": { "fileSuggestion": {
      "type": "command", "command": "~/.claude/bin/file-suggestion.sh" } } } }
```

Strictly better, and all three halves are now mechanical rather than conventional: the claude
pack stays the sole owner of `settings.json`; this pack **cannot** alter the file's
`mode`/`path`/`codec` (every surface-defining field is refused by name at decode); and the
boot's provenance sidecar records `fileSuggestion → config-overlay:claude-fzf`, which
`yolo config diff claude` reads out.

The conversion landed **with** Option 1 rather than before it, deliberately: the old `config`
form is no longer merely discouraged — selecting it alongside `claude` refuses the launch, so
leaving the example on Layout B would have shipped a pack that cannot start a jail.

### 2.5 ~~The pack does not lint~~ — FIXED 2026-09-30

From `d7178caf` (2026-09-23) until this fix, `yolo pack lint docs/examples/claude-fzf-pack`
exited 1, and so would every launch selecting a copy, since a `packload.LoadDir` problem is fatal
there. The `briefing` contribution read `"from": "AGENTS.md"`, and a pack may no longer ship a
file named `AGENTS.md`, `CLAUDE.md` or `GEMINI.md` as prose, because agent tools read those names
as a repository's own instructions at any depth
([P1 (briefing defaults)](../reference/pack-system.md#briefing-p1); the refusal is
`packdecl.ReservedBriefingSourceProblem`). Nothing in the suite read the example, so the commit
that added the refusal stayed green while the one pack the docs hold up as the worked example
stopped loading.

The fix moves the prose to `briefing/claude-fzf.md` and declares it as
`{"kind": "briefing", "from": "briefing/claude-fzf.md", "agents": ["claude"]}`. The `into:
".claude/CLAUDE.md"` it carried is replaced by `agents`, not kept: a content `into` narrows only
at `yolo host apply` and is a broadcast in a jail
([the note under the briefing rules](../reference/pack-system.md#briefing-non-goals)), and lint
says so and suggests `agents`, so keeping it would have left the example drawing lint's own
advice. The README's snippet and file table moved with it.

`TestExamplePacksLintClean` (`internal/cli/packexamples_test.go`) is the guard: it runs
`yolo pack lint`'s own entry point over every directory under `docs/examples/` and fails on a
refusal or on any advice line. Before the fix it failed with the refusal above. With the old
`into` kept and only the file moved, it fails on the advice.

MEASURED 2026-09-30 at the host notch, in-process on a throwaway home selecting `claude` and a
copy of the pack: `--assert` delivers the script at `0o555`, the `fileSuggestion` key and the
prose in `~/.claude/CLAUDE.md`, and a second `--assert` writes nothing. UNMEASURED: the jail
notch since the move ([§7](#7-how-to-verify-after-any-change) steps 5 and 6). The `agents` shape
is the one the claude pack's own `briefing/worktrees.md` uses in every jail.

---

## 3. The trap that was defused by convention, and is now closed by the mechanism

🔒 **The old rule was "`mode` is deliberately OMITTED from the `config` contribution — do not
add it."** It is obsolete, and how it became obsolete is the interesting part.

`claude/settings` is `stateful` (the default). Declaring `mode: "rmw"` on the same surface
identity **silently replaced the whole surface definition** — `manifest.Merge` is last-writer-
wins (`internal/agentcfg/manifest/load.go`, `byKey[k] = s`) — flipping claude's settings
from `stateful` to `rmw` and **disabling in-jail edit capture for `~/.claude/settings.json`**
with nothing reported.

This is ruling **R1**: *"very harmful. my setup doesn't matter. this is a general mechanism."*
And R1 is precisely why omitting `mode` was never the fix: it made ONE pack polite while the
mechanism stayed able to do the same damage through the next pack anyone wrote.

**Both halves are now shipped**, so the hazard is unreachable rather than avoided:

- the pack declares `config-overlay`, whose body may carry only `managed` — `mode`, `path`,
  `codec` and every other surface-defining field are refused **by name** at decode, so a
  contributor *cannot* flip the owner's mode even deliberately;
- a second `config` declaration of one identity is a **refused collision** at launch, at
  `yolo host apply`, and in `yolo pack footprint`/`yolo check`, naming both packs and the
  conversion.

Verified state: `yolo pack lint <dir>` reports
`config-overlay claude/settings contributes keys (owner still wins)`, and the in-jail capture
sidecars are present *and populated* (`rmw` writes none at all, so their presence is the proof)
with `fileSuggestion → config-overlay:claude-fzf` in the provenance record.

---

## 4. Context that changed while the pack was built

Everything below shipped in the same session and affects how the pack behaves. A successor
reading only the pack would miss these.

| change | why it matters to this pack |
|---|---|
| **`files` kind implemented** (jail + host) | the pack's script delivery *only just started working*; before, `files` was inert at every target while `pack lint` reported it fine |
| **exec bit now survives** `packstage`/`copyTree`/`host_files` | the script arrives `0o555`/`0o755` instead of `0o644`; `allow_exec` then granted the bit THROUGH, not just admission. Since 2026-08-30 there is no gate: the bit stages through with no opt-in |
| **`allow_exec` is a CONSUMER opt-in** | the pack cannot self-grant it; the config entry needed `"allow_exec": true` or staging refused. **Retired 2026-08-30**: a config entry still carrying `allow_exec` is now refused as an unknown key |
| **briefing is a delimited managed block** | the pack's prose was re-asserted idempotently inside markers, and the user's own prose outside them was untouched. **No longer**: the destination is now generated wholesale at every notch, so a hand edit to `~/.claude/CLAUDE.md` does not survive the next apply. The first apply onto a file the user wrote asks once and moves their prose into the local pack, and personal prose goes there ([`briefing`](../reference/pack-system.md#briefing)) |
| **`files` → `.claude` would shadow the settings surface** | why `into` is `.claude/bin`. A `files` tree is a `:ro` mount, so claiming the whole dir makes the boot refuse with "read-only file system". Now caught in pre-flight |
| **mount dedup for briefing + skills** | two packs at one destination used to fail with podman's duplicate-mount-destination; that is why this pack could declare a briefing at `.claude/CLAUDE.md` alongside the claude pack. It now addresses claude by `agents` instead ([§2.5](#25-the-pack-does-not-lint--fixed-2026-09-30)) |
| **`install_hints` on all six shipped packs** (8.3) | the model this pack should follow once [§2.2](#22-no-program-contribution--a-workaround-not-a-design-choice--adopted-requires-2026-08-03) unblocks — and the reason its absence is a gap rather than a non-issue |
| **manifests read tolerantly in-jail** (`DecodeTolerant`) | a new `pack.json` field no longer bricks a jail running an older baked image |

---

## 5. Three product defects this pack surfaced

> [!NOTE]
> **All three are fixed, checked 2026-09-30.** (1) No launcher is written for a name the image
> already provides (`internal/entrypoint/launchercollision.go`, the generation-time half of
> [`OQ-PD12a`](../design/program-delivery.md#decision-ledger)). (2) Every `program` in a pack installs
> ([`pack-system.md`'s Q2.1](../reference/pack-system.md#q2-1)).
> (3) Each launch stages a new pack tree holding only the selected packs
> (`internal/cli/run/packtree.go`, `b2e796a7`). The list below is the 2026-08-02 record.

All three are why [§2.2](#22-no-program-contribution--a-workaround-not-a-design-choice--adopted-requires-2026-08-03) exists. Full context and the decisions needed are in
[`../reference/pack-system.md#program`](../reference/pack-system.md#program); Phase 11 of the plan
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

- [x] ~~Check the real `~/.dotfiles/claude/file-suggestion.sh` for `"$1"`~~ — **done
      2026-08-02: no bug.** It reads stdin via `jq -r '.query // ""'` and already satisfies
      the whole contract ([§1](#1-the-one-thing-that-must-be-checked-on-the-host-before-adoption)).
- [ ] Copy the real script over `bin/file-suggestion.sh`, keep the filename and the exec bit,
      re-run `yolo pack lint <dir>`.
- [ ] Copy the pack to `~/.dotfiles/claude-fzf/` (or wherever personal packs live) and add the
      config entry from the README. **Not** `"allow_exec": true`, which this list used to ask
      for: the key was retired 2026-08-30 and is now refused as unknown.
- [x] ~~Do NOT add `mode` to the `config` contribution~~ — moot: the pack declares
      `config-overlay`, which cannot set `mode` at all ([§3](#3-the-trap-that-was-defused-by-convention-and-is-now-closed-by-the-mechanism)).
- [x] ~~When `config-overlay` lands, convert the `config` contribution to it~~ — **done
      2026-08-02** ([§2.4](#24-the-pack-declares-config-which-will-eventually-be-wrong--converted-2026-08-02)).
- [x] ~~When Q1.x is decided, add the `fd`/`fzf` dependency declaration with `install_hints`~~
      — **done 2026-08-03**: `pack.json` declares both as `requires`, with hints
      ([§2.2](#22-no-program-contribution--a-workaround-not-a-design-choice--adopted-requires-2026-08-03)).
- [x] ~~Expect two `claude/settings rendered` lines until R4 is fixed~~ — one line now; two
      would mean a collision, which is refused ([§2.3](#23-the-double-rendered-line-is-expected-here--fixed-2026-08-02)).

## 7. How to verify after any change

The sequence used originally, all against a throwaway `$HOME` under `mktemp -d` — **never a
real home**:

1. `yolo pack lint <pack dir>` → clean with no `ℹ` advice line, and confirm it says
   **`config-overlay claude/settings`** — a `config` claim there would mean the pack regressed
   to Layout B, which now refuses the launch. For the copy in this repository,
   `TestExamplePacksLintClean` checks the first half (clean, no advice) on every unit-suite
   run.
2. `yolo host apply` (observe) → writes nothing.
3. `yolo host apply --assert` → script at `0o555`, `fileSuggestion` present alongside
   claude's own `permissions`, briefing block written, and **exactly one**
   `claude/settings rendered` line (two would be the R4 tell, and is now impossible).
4. **Second `--assert` → byte-identical.** This is the test that catches an accumulating
   render; run it every time.
5. Nested jail, with the freshly built binary **by path**
   (`just build-go && ./dist-go/linux-$(go env GOARCH)/yolo -- bash -lc '…'`, `YOLO_REPO_ROOT=/workspace`)
   → script present, executable, and **runs**. Not bare `yolo` — that is the baked launcher.
   `git add` new files first; a nested image build only sees git-tracked files.
6. Confirm the owner's mode survived: capture sidecars exist in-jail under
   `<ws>/.yolo/prism/claude-settings.*` (an `rmw` surface writes none, so their presence is the
   proof), and `claude-settings.provenance` names the contributor —
   `fileSuggestion → config-overlay:claude-fzf`.
