# When two packs want the same config file

**Status:** analysis, 2026-08-02. Written to answer a specific question before changing
anything: *"the claude pack needs to add support for the `fileSuggestion` key, then the fzf
pack feeds into this new key?"*

**Short answer: no, and the reason is worth understanding.** The claude pack needs no change,
and the fzf pack does not feed into anything the claude pack declares. But the mechanism that
makes that true today is **accidental**, and this doc is about the difference between "works"
and "correct".

**Reads with:** [`pack-system.md`](pack-system.md) §5 (the layer model and the four modes),
§14 (`config-overlay` is inert), and
[`../plans/pack-host-management-plan.md`](../plans/pack-host-management-plan.md) (the work
that made host rendering real).

---

## 1. The three layouts you could imagine

For a pack to add `fileSuggestion` to `~/.claude/settings.json`, there are three
architecturally distinct options. Naming them is most of the clarity.

### Layout A — "the agent pack owns the schema" (what you asked about)

The `claude` pack gains a `fileSuggestion` key with some placeholder or default, and the fzf
pack fills it in. This is the model where the owning pack must *anticipate* every key a
consumer might want.

**Rejected, and it is worth saying why:** it does not scale past the keys yolo's authors
happened to think of. `settings.json` has dozens of keys and gains more with each Claude
release; a pack system where every key must be pre-declared by the agent pack before anyone
can set it is a pack system that is always behind the tool. It also inverts the ownership
story — the `claude` pack would need to know about fzf.

### Layout B — "the second pack declares the same surface" (what works today)

The fzf pack declares `agent: "claude", name: "settings"` itself, with only its own key in
`managed`. Two packs then both declare the surface identity `claude/settings`.

This is what I verified working end to end, at both notches, in either pack order. **It is
also the layout with the sharp edge** — see §3.

### Layout C — "the second pack overlays a surface the first owns" (the designed answer)

The fzf pack declares `kind: "config-overlay"` naming `surface: "claude/settings"`, and
contributes only keys. The `claude` pack stays the sole *owner* of the file; the fzf pack is
explicitly a *contributor*, with per-key provenance so an override is legible.

**This is what the kind set was designed for, and it is inert.** `config-overlay` parses,
validates, has a combine rule (`CombineOverlay`), and the compose engine accepts overlay
inputs (`Inputs.Overlays`) — but no boot-path code collects `config-overlay` contributions and
feeds them to the assembler. So a `config-overlay` in a manifest does nothing at all
(`pack-system.md` §14).

---

## 2. What actually happens today (Layout B), verified

```jsonc
// ~/.dotfiles/claude-fzf/pack.json — self-contained; the claude pack is untouched
{ "name": "claude-fzf",
  "contributes": [
    { "kind": "files", "from": "bin", "into": ".claude/bin" },
    { "kind": "config", "config": [{
        "agent": "claude", "name": "settings", "codec": "json",
        "path": "~/.claude/settings.json", "mode": "rmw",
        "managed": { "fileSuggestion": { "type": "command",
                     "command": "~/.claude/bin/file-suggestion.sh" } } }] },
    { "kind": "program", "bin": "fzf", "via": "installer", "url": "…",
      "install_hints": { "brew": "fzf", "apt": "fzf", "nix": "fzf" } }
  ] }
```

```jsonc
// ~/.config/yolo-jail/config.jsonc — the CONSUMER grants the exec bit
{ "packs": ["claude",
            { "source": "file:///home/matt/.dotfiles/claude-fzf",
              "allow_exec": true }] }
```

Result, verified on a throwaway `$HOME` and in a real jail:

```console
$ python3 -c 'import json; d=json.load(open("~/.claude/settings.json")); print(sorted(d))'
['fileSuggestion', 'permissions', 'preferences', 'skipDangerousModePermissionPrompt']
```

The fzf pack's key and the claude pack's keys (`preferences` from its `config`,
`permissions`/`skipDangerousModePermissionPrompt` from its `autonomy` posture) coexist. Order
in the `packs` list does not matter — I tested both.

---

## 3. Why it works — and the trap underneath

Two mechanisms combine, and only one of them is intentional.

### The intentional half: `rmw` writes only its own keys

`internal/agentcfg` composes a surface by folding layers, then a `mode: "rmw"` surface
**merges its `managed` keys into whatever is already in the file and leaves every other key
alone**. It does not compose a whole file and overwrite. So two packs asserting disjoint keys
into one `rmw` file both get their keys, regardless of order. That is by design and it is the
reason Layout B functions.

### The accidental half: surface identity is LAST-WRITER-WINS, silently

`manifest.Merge` (`internal/agentcfg/manifest/load.go:124`) keys surfaces by identity and
**replaces**:

```go
for _, s := range append(append([]Surface{}, base...), extra...) {
    k := s.Key()
    if _, seen := byKey[k]; !seen { order = append(order, k) }
    byKey[k] = s          // ← the second declaration REPLACES the first, whole
}
```

There is no merge of two same-identity surfaces and no collision error. The surface that
survives brings **its own `mode`, `path`, `codec`, `defaults`, and `managed`** — everything.

**Here is the part that matters, and it is why the claude pack's own behavior made this look
simpler than it is.** The shipped `claude` pack declares:

| surface | mode |
|---|---|
| `claude/config` (`~/.claude.json`) | `rmw` |
| `claude/settings` (`~/.claude/settings.json`) | **`stateful`** (the default) |

`claude/settings` is **`stateful`**, not `rmw`. My fzf pack declared the same identity with
`mode: "rmw"` — and because the later declaration replaces the earlier one wholesale, **my
pack silently changed claude's settings surface from `stateful` to `rmw`.**

That is a real behavior change, not a cosmetic one. `stateful` captures a user's in-jail edits
into a sidecar overlay and replays them across regeneration; `rmw` has no sidecars at all
(`pack-system.md` §5). So the working configuration in §2 quietly disabled in-jail edit
capture for `~/.claude/settings.json`, and nothing reported it. `yolo pack footprint` lists
both surfaces without flagging the identity clash.

**So Layout B "works" for the fzf case by a coincidence of two facts** — that `rmw` is
key-scoped, and that losing `stateful` on this particular file has consequences a user is
unlikely to notice immediately. Neither is a guarantee. If the two packs had disagreed about
`path`, `codec`, or `defaults`, one would have silently lost, and the failure would surface
as "my config is mysteriously wrong" rather than as an error.

---

## 4. Your insight, stated precisely: this got simpler because Claude writes the file too

You put it as: *"settings is also modified by claude, so if this was a fully generated file,
we'd need my sort of routing."* That is exactly right, and it is worth spelling out because it
identifies the real design boundary.

**Because `settings.json` is a file the agent itself owns and mutates**, yolo cannot treat it
as a generated artifact. It must merge into whatever is there and leave the rest — which is
precisely what `rmw` is for, and what `stateful`'s capture overlay exists to preserve. yolo
regenerating the whole file would destroy the agent's own writes.

**That constraint is what makes multi-pack contribution work by accident.** A key-scoped
writer is *inherently* composable: two writers asserting disjoint keys do not conflict, so
"who owns this file" never has to be answered. The one-writer rule is satisfied vacuously.

**Now consider a fully generated file** — a `computed`-mode surface, where yolo is the sole
author and the file is a pure function of its layers, overwritten every boot. Two packs
declaring that same surface would be a genuine, unavoidable conflict: the second replaces the
first, the first pack's contribution vanishes entirely, and there is no key-scoped merge to
save it. You would get exactly one pack's file.

**That is where routing becomes mandatory, not optional** — and it is what `config-overlay`
is: the routing layer. It names *one* owner of the file and lets others contribute keys that
fold in at a defined precedence (`defaults < host < workspace < config-overlay <
capture-overlay < computed < managed`), with provenance recorded so an override is traceable
rather than silent.

The uncomfortable conclusion: **`config-overlay` is inert, so yolo currently has no correct
mechanism for a second pack to contribute to a generated config file at all.** For `rmw` and
`stateful` files the accident covers for it. For `computed` files there is no answer — the
second pack just loses. Nobody has hit this yet because the composed surfaces that matter are
agent-owned files, which is why the gap has stayed invisible.

---

## 5. What is actually wrong, ranked

| # | Problem | Severity |
|---|---|---|
| **1** | **A same-identity surface declaration silently replaces the first**, taking its `mode`/`path`/`codec` with it. Verified: a pack can flip claude's settings from `stateful` to `rmw` with no warning. | **real hazard** |
| **2** | `config-overlay` — the designed mechanism for exactly this — is **inert**. It parses, validates, has a combine rule and engine support, and no boot-path code collects it. | **missing feature** |
| **3** | `yolo pack footprint` does not flag an identity clash between two `config` contributions, though the footprint model calls `config` `CombineExclusive` (*"a second writer must be `config-overlay`"*). The rule is documented and unenforced. | **unenforced invariant** |
| **4** | No mechanism exists for a second pack to contribute to a `computed` surface. Latent — no shipped pack does this — but it is the case the accident does not cover. | **latent gap** |

Note that #1 and #3 are the same defect seen from two angles: the footprint promises
exclusivity, the merge silently allows a replacement.

---

## 6. Options, with a recommendation

### Option 1 — Enforce the documented rule (small, subtractive)

Make two `config` contributions on one surface identity a **loud collision**, named in
`pack footprint` and refused at launch — exactly as `files` already is (that pre-flight was
built in Phase 7 and names both packs). This is the one-writer rule finally enforced instead
of merely written down.

**Cost:** it breaks the working Layout B, so it must land *with* Option 2 or the fzf case
regresses from "works by accident" to "refused". On its own it is a regression.

### Option 2 — Wire `config-overlay` (medium, the designed answer)

Collect `config-overlay` contributions in the boot path and the host render, feed them to
`Inputs.Overlays` (which the engine already accepts), and record per-key provenance. Then:

```jsonc
// claude-fzf/pack.json — the honest declaration
{ "kind": "config-overlay", "surface": "claude/settings",
  "config": { "managed": { "fileSuggestion": {
      "type": "command", "command": "~/.claude/bin/file-suggestion.sh" } } } }
```

The `claude` pack remains the sole owner of `~/.claude/settings.json` and keeps its
`stateful` mode; the fzf pack contributes one key and cannot change the file's mode, path, or
codec. It also closes the last inert kind, and it is the only option that answers the
`computed`-surface case in §4.

**Cost:** it is real work — two render paths (jail + host), provenance plumbing, and
`config diff` should show which pack set which key or the provenance is pointless.

### Option 3 — Merge same-identity surfaces instead of replacing (medium, tempting, wrong)

Deep-merge two declarations of one identity rather than replacing. This would make Layout B
*safe* without new syntax.

**Rejected.** It answers "whose `managed` keys win" but not "whose `mode` wins" — and `mode`
is not mergeable. Two packs disagreeing about `stateful` vs `rmw` has no correct merge; one
has to lose, which is the original problem wearing a hat. It also erases the ownership
distinction that makes provenance possible: with a merge, "which pack set this key" becomes
unanswerable, which is the thing `config-overlay` was designed to record.

### Recommendation

**Option 2, then Option 1**, in that order, as one body of work. Wire `config-overlay` first
so there is a correct way to express the intent; then enforce exclusivity so the incorrect way
stops silently working. Doing 1 first breaks a working setup; doing 2 without 1 leaves the
silent-replacement hazard in place for anyone who takes the shortcut.

Until then, **Layout B is the working answer and should be documented as a known shortcut**,
with the `stateful`→`rmw` side effect called out — because right now a user following it has
no way to learn what it did.

---

## 7. Open questions for you

- **OQ-1 — Is the `stateful` → `rmw` flip on `~/.claude/settings.json` actually harmful in
  practice?** It disables in-jail edit capture for that file. If you never rely on capture
  there (`yolo config diff claude` would show what it holds), the fzf pack can simply **omit
  `mode`** and inherit `stateful`, matching the claude pack — same mode, disjoint keys, no
  behavior change. **VERIFIED working**: with `mode` omitted, `fileSuggestion`, `preferences`,
  and `permissions` all coexist in the rendered file. That reduces this from "hazard" to
  "unenforced rule", and it is the shape the shortcut should be documented as.

- **OQ-4 (found while verifying OQ-1) — a duplicated surface renders TWICE.** The
  `apply --host` output prints `claude/settings rendered` twice, once per declaring pack:

  ```
    claude/settings      rendered  /tmp/…/.claude/settings.json
    claude/settings      rendered  /tmp/…/.claude/settings.json
  ```

  Harmless in effect (the second write is idempotent) but it is the collision made visible in
  the output while nothing calls it a collision. Whatever fix lands for #1/#3 should collapse
  this to one line — and until then, **two identical `rendered` lines for one surface is the
  tell that two packs are fighting over it.**
- **OQ-2 — Should `config-overlay` be able to reach a surface no loaded pack owns?** If the
  `claude` pack is not selected, an overlay on `claude/settings` has no owner. Refuse by name
  (consistent with the never-silent rule), or create the file? I lean refuse.
- **OQ-3 — Does provenance need to be user-visible, or just internally recorded?** §14 says
  "per-key provenance recorded so an override is legible rather than silent". Surfacing it in
  `yolo config diff` is more work but is what makes the feature honest.
