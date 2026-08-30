# When two packs want the same config file

**Status:** analysis + **rulings**, 2026-08-02. Written to answer a specific question before
changing anything: *"the claude pack needs to add support for the `fileSuggestion` key, then the
fzf pack feeds into this new key?"* Reviewed the same day; §7 records four rulings that
constrain the implementation, and the recommended order (§6) is confirmed.

**Both options are SHIPPED** (2026-08-02). Option 2 wired `config-overlay` at both render
paths, with R2's ownerless-overlay reporting and R3's provenance visibility in `yolo config
diff`; Option 1 then made `config` exclusivity a LOUD COLLISION — named in `yolo pack
footprint`, refused at launch and by `yolo host apply`, naming both packs and teaching the
`config-overlay` conversion — which also settled R4 by removing the state that produced the
double `rendered` line. See §6 for what that means in the code, §8 for what shipping Option 2
settled, and §9 for Option 1.

**Short answer: no, and the reason is worth understanding.** The claude pack needs no change,
and the fzf pack does not feed into anything the claude pack declares. But the mechanism that
makes that true today is **accidental**, and this doc is about the difference between "works"
and "correct".

**Reads with:** [`pack-system.md`](pack-system.md) §5 (the layer model and the four modes),
§3 `config-overlay` (the shipped kind), and
[`../plans/pack-host-management-plan.md`](../plans/pack-host-management-plan.md) (the work
that made host rendering real).

**A note on tense.** §1–§5 are preserved as written, in the present tense of 2026-08-02,
because the argument for *why* Option 2 was the right answer only holds if the state it argued
from is legible. Where they say `config-overlay` "is inert", read "was inert before §8".

---

## 0. Four terms, because the rest of this doc leans on them

Skip if `pack-system.md` §5 is fresh; these are its definitions restated in one place.

**Surface** — one config *file* a pack declares, identified by `agent/name` (e.g.
`claude/settings`). The identity is what yolo keys on internally; `path` is where it lands.
Two packs using the same `agent/name` are talking about the same file, which is the whole
subject of this doc.

**Layers** — a surface is not written directly. yolo folds several inputs together with
RFC-7386 merge semantics, lowest precedence to highest:

```
defaults < host < workspace < config-overlay < capture-overlay < computed(derive) < [lua transform] < managed
```

**`managed`** is the top layer: **the keys yolo asserts and always wins.** It is applied last,
so a `managed` key overrides anything below it — including a value the user or the agent wrote.
`defaults` is the mirror image (yolo's base, freely overridable). So "only its own key in
`managed`" means "this pack insists on exactly this one key and stays out of everything else".

**`mode`** — how the file is maintained across boots. Four values; three matter here:

| mode | behavior | who it is for |
|---|---|---|
| `stateful` | compose from layers *and* capture in-jail edits into a sidecar, replayed next boot | the default |
| `rmw` | read-modify-write: merge `managed` into whatever is in the file, touch nothing else, no sidecars | a file the **agent itself** owns and mutates |
| `computed` | compose from layers and overwrite wholesale every boot | a file **yolo** solely authors |

`mode` is the pivot of §3 and §4: `rmw` and `stateful` are key-scoped writers, `computed` is
not, and that difference decides whether two packs can share a file by accident.

**`config-overlay`** — a contribution *kind* (one of fifteen; see `yolo config-ref`). Where
`config` **declares and owns** a surface, `config-overlay` **contributes keys to a surface
another pack owns**, naming it by identity. It folds in at the `config-overlay` position above
— below `managed`, so the owner still wins a genuine conflict — with per-key provenance so an
override is traceable rather than silent. It is the routing mechanism, and it is **inert**
(§1 Layout C).

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
`managed` (§0: the top layer, the keys yolo asserts and always wins — so this pack insists on
`fileSuggestion` and stays out of every other key). Two packs then both declare the surface
identity `claude/settings`.

This is what I verified working end to end, at both notches, in either pack order. **It is
also the layout with the sharp edge** — see §3.

### Layout C — "the second pack overlays a surface the first owns" (the designed answer)

The fzf pack declares `kind: "config-overlay"` naming `surface: "claude/settings"`, and
contributes only keys. Where `config` declares-and-owns a surface, `config-overlay` contributes
to one **another pack owns** (§0), folding in below `managed` so the owner still wins a genuine
conflict. The `claude` pack stays the sole *owner* of the file; the fzf pack is explicitly a
*contributor*, with per-key provenance so an override is legible.

**And if the owning pack is not selected?** Then the overlay has no surface to fold into, and
the answer is **no effect, reported by name** (ruling R2, §7):

```
config-overlay  no effect — claude/settings has no owner (the `claude` pack is not selected)
```

Deliberately *not* the two tempting alternatives. It must not create the file — that would let
an overlay own a surface by accident, which is the distinction Layout C exists to draw. And it
must not fail the launch — a pack the user simply did not select is not an error. It also fails
in the useful direction: add the `claude` pack later and the overlay starts working with no
further edit.

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
| ~~**1**~~ | ~~**A same-identity surface declaration silently replaces the first**, taking its `mode`/`path`/`codec` with it.~~ **FIXED** (§9): refused at launch, at `yolo host apply`, and named in `pack footprint`. | **general hazard in the pack mechanism** (ruling R1) |
| ~~**2**~~ | ~~`config-overlay` — the designed mechanism for exactly this — is **inert**.~~ **FIXED** (§8). | **missing feature** |
| ~~**3**~~ | ~~`yolo pack footprint` does not flag an identity clash between two `config` contributions.~~ **FIXED** (§9). Footprint also reports `config-overlay` claims now, which it previously skipped. | **unenforced invariant** |
| **4** | No mechanism exists for a second pack to contribute to a `computed` surface. Latent — no shipped pack does this — but it is the case the accident does not cover. | ~~**latent gap**~~ **CLOSED by §8** — all three modes now carry overlays |
| ~~**5**~~ | ~~A duplicated surface prints one `rendered` line per declaring pack.~~ **FIXED** (§9), by refusing the clash rather than deduping the line. | **misleading output** (ruling R4) |

Note that #1, #3 and #5 are one defect seen from three angles: the footprint promises
exclusivity, the merge silently allows a replacement, and the output shows the replacement
happening without calling it that.

**#1 is not about any one pack's politeness.** A pack *can* avoid the flip by matching the
owner's `mode` (see §6), but that fixes one pack, not the mechanism — the next pack anyone
writes can do the same damage to any surface. That is ruling R1, and it is why #1 outranks the
missing feature it would be tempting to fix first.

---

## 6. Options, with a recommendation

### Option 1 — Enforce the documented rule (small, subtractive) — **SHIPPED**

Make two `config` contributions on one surface identity a **loud collision**, named in
`pack footprint` and refused at launch — exactly as `files` already is (that pre-flight was
built in Phase 7 and names both packs). This is the one-writer rule finally enforced instead
of merely written down. Per ruling R4 it also collapses the double `rendered` line: once a
clash is a collision, the second line is reportable as a conflict rather than printed as noise.

Shipped 2026-08-02 — see §9 for what it turned out to require.

Ruling R1 makes this the load-bearing half rather than the tidy-up: the hazard is the
mechanism's, so only enforcement closes it.

**Cost:** it breaks the working Layout B, so it must land *with* Option 2 or the fzf case
regresses from "works by accident" to "refused". On its own it is a regression.

### Option 2 — Wire `config-overlay` (medium, the designed answer) — **SHIPPED**

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

Per ruling R2, an overlay whose owner is not selected is **inert and says so by name** — it
neither creates the file nor fails the launch. Per ruling R3, provenance must be **visible in
`yolo config diff`** (which pack set which key), not merely recorded: provenance nobody can
read does not make an override legible, which was the whole justification for the kind.

**Cost:** it is real work — two render paths (jail + host), provenance plumbing, and the
`config diff` surfacing that R3 makes non-optional.

### Option 3 — Merge same-identity surfaces instead of replacing (medium, tempting, wrong)

Deep-merge two declarations of one identity rather than replacing. This would make Layout B
*safe* without new syntax.

**Rejected.** It answers "whose `managed` keys win" but not "whose `mode` wins" — and `mode`
is not mergeable. Two packs disagreeing about `stateful` vs `rmw` has no correct merge; one
has to lose, which is the original problem wearing a hat. It also erases the ownership
distinction that makes provenance possible: with a merge, "which pack set this key" becomes
unanswerable, which is the thing `config-overlay` was designed to record.

### Recommendation

**Option 2, then Option 1**, in that order, as one body of work — **confirmed in review.** Wire
`config-overlay` first so there is a correct way to express the intent; then enforce exclusivity
so the incorrect way stops silently working. Doing 1 first breaks a working setup; doing 2
without 1 leaves the silent-replacement hazard in place for anyone who takes the shortcut.

Ruling R1 sharpens the second half: because the hazard belongs to the mechanism rather than to
any one pack, **Option 1 is not optional cleanup that can be deferred indefinitely.** Shipping
Option 2 alone would leave a correct path available and the destructive path still working
silently — which is the state we are in now, minus the excuse that no alternative exists.

**In the meantime, Layout B is the working answer and should be documented as a known
shortcut** — with the `stateful`→`rmw` side effect called out, because a user following it today
has no way to learn what it did. A pack taking the shortcut should match the owner's `mode`
(omit it, inheriting `stateful`; verified to work with all keys coexisting). But per R1 that is
**politeness by the pack author, not a fix** — it is worth doing and worth not mistaking for the
repair.

---

## 7. Rulings

All four questions were decided in review (2026-08-02). Recorded here because each one
constrains the implementation.

- **R1 — the `mode` flip is HARMFUL, full stop.** *Ruling: "yes, very harmful. my setup
  doesn't matter. this is a general mechanism."*

  This raises the severity in §5 rather than lowering it. The question was framed wrongly:
  whether *this* maintainer relies on in-jail edit capture for `~/.claude/settings.json` is
  irrelevant, because **a pack silently changing another pack's surface `mode` is a general
  defect in the pack mechanism.** Any pack can do it to any surface, and the victim is
  whichever user's file loses its capture sidecar.

  The consequence for the plan: **matching `mode: "stateful"` in the fzf pack is a workaround,
  not a fix, and must not be presented as one.** It makes one pack polite; it leaves the
  mechanism able to do the same damage on the next pack anyone writes. Problem #1 in §5 stays
  ranked as a real hazard, and the enforcement in Option 1 becomes load-bearing rather than
  tidy-up.

- **R2 — `config-overlay` with no owner: no effect, reported.** *Ruling: "more a no effect,
  which is kinda refuse?"*

  Right, and the distinction is worth keeping precise. Two things it must NOT do: create the
  file (that would make an overlay a covert `config`, owning a surface by accident), or abort
  the launch (a pack the user simply did not select is not an error). So: **the overlay is
  inert and SAYS SO by name**, the same shape as every other honest non-delivery in this
  system —

  ```
  config-overlay  no effect — claude/settings has no owner (the `claude` pack is not selected)
  ```

  That is "no effect" in behavior and "refused by name" in reporting, which is the same answer
  read two ways. It also fails in the right direction: a user who adds the `claude` pack later
  gets the overlay working with no further edit.

- **R3 — provenance must be USER-VISIBLE.** *Ruling: "yes do this. more visibility is
  better."* So wiring `config-overlay` includes surfacing per-key provenance in
  `yolo config diff` — which pack set which key — not merely recording it internally. This is
  scope inside Option 2, not a follow-up: provenance nobody can read does not make an override
  legible, which was the entire justification for the kind.

- **R4 — the double-render line needs fixing.** *Ruling: "yes needs fixing."* The
  `apply --host` output prints one `rendered` line per declaring pack:

  ```
    claude/settings      rendered  /tmp/…/.claude/settings.json
    claude/settings      rendered  /tmp/…/.claude/settings.json
  ```

  Harmless in effect (the second write is idempotent), but it is the collision made visible
  while nothing names it a collision — the same never-silent failure this whole body of work
  exists to remove, inverted: the output is not silent, it is just *uninterpretable*. Folded
  into Option 1, since the collision check is what makes the second line reportable as a
  conflict instead of collapsible as noise.

  Until then: **two identical `rendered` lines for one surface is the tell that two packs are
  fighting over it.**

### What the rulings change about the plan

Option 2 (wire `config-overlay`) grows the provenance-visibility requirement from R3, and
gains the no-owner reporting rule from R2. Option 1 (enforce exclusivity) grows the
double-render fix from R4 and is promoted by R1 from "tidy up an unenforced rule" to "close a
general hazard in the pack mechanism". The **Option 2 → Option 1 order still holds** (confirmed
in review), because a correct expression must exist before the incorrect one is refused.

---

## 8. What building Option 2 settled that this doc did not

Five decisions the rulings did not reach. Each is a place the implementation had to choose,
and the choice is recorded here rather than only in a code comment.

**An overlay body may set ONLY `managed`.** The doc says an overlay "contributes only keys"
without saying what happens if it says more. Every surface-defining field — `agent`, `name`,
`path`, `codec`, `mode`, `transform`, `defaults`, `retireOnFirstRender` — is now refused BY
NAME at decode, with the rule in the message rather than a generic "unknown field" (each of
those keys is real; it is just not a contributor's to set). This is what makes §6's promise
mechanical: *"the fzf pack contributes one key and cannot change the file's mode, path, or
codec."* Without the refusal that is a convention, and R1's `mode` flip would come straight
back through the correct syntax.

`defaults` is refused for a subtler reason worth stating: an overlay folds at exactly ONE
precedence slot, so a contributor has no second, lower position to occupy. A `defaults` key
in an overlay body would either silently behave as `managed` or need a slot the layer model
does not have.

**A malformed overlay is FATAL; an ownerless one is not.** R2 covers the second case only.
The split follows the same line every other manifest problem does: an unselected owner is not
the author's mistake (nothing to fix), whereas a body that redeclares the surface is the
author asserting something the mechanism will never honor — the same class as a malformed
surface, which is already A12-fatal. Reporting a redefinition as merely "no effect" would be
the silent-misconfiguration failure this kind exists to remove, wearing the R2 message.

**`rmw` asserts an overlay's keys rather than defaulting them.** An `rmw` surface has no
layer fold, so precedence becomes write order: overlays first, then the derived tables, then
the owner's `managed`, then `defaults` fill only where absent. The keys are force-written, not
seeded, because an overlay body says `managed` — "keep this key at this value" — so
fill-if-absent would make the fzf case work on the first boot and then never pick up a changed
value. That mode matters: `claude/settings` is `stateful` but `claude/config` is `rmw`, and §4
predicted the fully-generated (`computed`) case is where routing becomes mandatory. All three
modes now carry overlays.

**A `config-overlay` onto a CORE surface (`mise/config`) is inert, with its own message.**
The kind is defined as contributing to a surface *another pack* owns, and core's surfaces
belong to no pack. It is reported as core-owned rather than as an unknown identity, so the
message does not send a user hunting a typo that is not there.

**`config diff` reads pack declarations, not just the sidecar.** R3 says provenance must be
visible; the sidecar alone cannot deliver it. The sidecar records only the WINNER of each key,
so a contribution the owner's `managed` layer beat leaves no entry at all — and *"my overlay
did nothing"* is precisely the case a user needs answered. So the command lists contributions
from the manifests and annotates each with the sidecar's winner where a boot recorded one:
`set by <pack>` when it won, `contributed by <pack> but managed won` when it lost. An `rmw` or
`computed` surface writes no provenance sidecar by design, and that reads as *"winner
unknown — this surface's mode keeps no provenance sidecar"* rather than as a loss, because
sending a user to investigate a by-design absence is its own kind of misreport.

### Still open after Option 2

- ~~**Option 1** (§6)~~ **SHIPPED** — see §9.
- ~~**`yolo pack footprint` does not show overlay claims.**~~ **FIXED** with Option 1:
  `FootprintOf` now emits one `config-overlay` claim per contribution, keyed by the identity
  it targets. It does not collide (`CombineOverlay`), so it is a claim line only.

- **At the HOST notch, `config diff` reports a plausible winner rather than a measured one.**
  Found while verifying Option 2 end to end. The paragraph above covers the case where a
  surface's *mode* keeps no sidecar; this is a different absence — the **host render writes no
  provenance sidecar at all**, whatever the mode, because it is a pure RMW into a real home
  with no `.yolo/prism` tree to write beside.

  Measured: an overlay contributing `fileSuggestion` to `claude/settings` (a `stateful`
  surface) at the host notch reports

  ```
    fileSuggestion  contributed by fzf-overlay but managed won
  ```

  while the value that actually landed in `~/.claude/settings.json` is **the overlay's** — and
  the `claude` pack does not declare that key at all, so there was no `managed` value to win.
  The data is right (the overlay is correctly listed as a contributor); the annotation is
  guessed from declarations and states the opposite of what happened.

  This is worse than the by-design absence above, because it does not read as "unknown" — it
  reads as a confident wrong answer, and it will tell a user their overlay lost when it won.
  Two candidate fixes: teach the host render to emit a provenance record (it has the Result;
  it just has nowhere it currently writes one), or make the host path report
  `contributed by <pack> (winner not recorded at the host notch)` and stop inferring. The
  second is cheap and honest; the first is what R3 actually asked for.

---

## 9. What building Option 1 settled that this doc did not

Shipped 2026-08-02, deliberately AFTER Option 2 (§6's recommended order, confirmed in review):
a correct expression had to exist before the incorrect one could be refused. Five decisions the
implementation had to make.

**Three refusal sites, one detector.** `packload.ConfigSurfaceCollisions` is the single pass;
it is consumed by `packload.Collisions` (so `yolo pack footprint` and `yolo check` report it),
by `stagePacks` (so a launch is refused before the container exists), and by `yolo host apply`
(so nothing is written into a real home). The `files` pre-flight's shape, reused rather than
re-invented: same call site in `stagePacks`, same reason — that is where the pack set becomes
complete, and it covers the attach path too.

**Its own pass, not a row in the generic exclusive loop.** Two reasons, and both are structural
rather than stylistic. (1) A pack can commit this against ITSELF: every other exclusive kind
collides on a destination the runtime then rejects, so "one pack, one claim" is safe enough
there and the generic loop correctly skips a single-pack group. A surface identity is resolved
in Go by `manifest.Merge`, which is just as silent for two declarations inside one manifest.
(2) The REMEDY is a different KIND, not a different target — "give them different paths" is
right for `files` and useless here, because two packs wanting keys in one config file is a
legitimate intent with a correct expression. So the message has to teach the conversion, which
no generic reason string can.

**The refusal names the DIVERGENCE, not just the rule.** Two packs agreeing on everything but
`managed` are still refused (R1: the hazard is the mechanism, not the impoliteness), but where
the declarations disagree on `mode`/`path`/`codec` the message says which field and which two
values — `mode (claude: "stateful" vs claude-fzf: "rmw")`. That is the concrete damage, and a
reader who sees it does not have to take the rule on faith. In a SELF-collision both sides
would carry the same pack name, so there the labels are `declaration 1` / `declaration 2`.

**R4 is settled by refusing, not by deduping.** The double `rendered` line was one line per
declaring pack for one file. Deduping it would have hidden the clash; refusing the apply
removes the second line by removing the state that produced it. Measured both ways: pre-fix,
`apply --host` on a doubly-declared surface printed the line twice and exited 0; post-fix it
prints the refusal and exits 1, and an OVERLAY-based pack prints exactly one line (it always
did — that path was not touched).

**The single-pack views need a shipped-pack advisory.** `pack lint` and `pack footprint <dir>`
hold ONE pack by construction, so they cannot see a cross-pack collision — and the most likely
one, a surface a pack yolo ships already owns, is exactly the fzf case. Without help an author's
pack lints clean and then fails to boot, which makes the pre-configure check the one check that
misses. So both single-pack views compare against the (not-selection-gated) embedded set and
warn by name, with the `config-overlay` shape to copy. A WARNING rather than a lint failure:
whether the two packs are ever selected together is a config question these commands cannot
answer, so the refusal stays where the pack set is known.

### What it did NOT change

- **No shipped pack collides.** All six declare disjoint identities (`claude/config`,
  `claude/settings`, `copilot/config`, `copilot/lsp`, `copilot/mcp`, `codex/config`,
  `opencode/config`, `pi/settings`, `agy/mcp`, `agy/settings`), and five use `autonomy` to
  patch a surface they already own — which is not a second declaration, because
  `foldPostureManaged` merges into the base rather than appending. Pinned by two tests, one at
  the detector and one at `stagePacks`.
- **The render fingerprint is byte-identical.** Refusing a collision changes no rendered file
  for a valid pack set, which is the invariant `TestRenderFingerprintStable` exists to hold.
- **`docs/examples/claude-fzf-pack/` converted** to `config-overlay` in the same change, per
  `docs/plans/handoff-fzf-pack-adoption.md` §2.4. It would otherwise have been the first thing
  the new refusal broke — by design, since it used Layout B because Layout C did not work yet.
