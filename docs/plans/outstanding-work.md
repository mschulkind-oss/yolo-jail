# Outstanding work — queued rulings and parked design

**What this is.** Everything not built, in one place, split into **QUEUED** (decided; waiting on
one implementation pass) and **PARKED** (no decision yet). Started 2026-08-03 as a Stage E parked
list, after the pack system reached functional completeness, so that "what is left?" has one
honest answer; the queued half accreted from the 2026-08-04 rulings and divergence audit.

**Implementation intent (maintainer, 2026-08-04):** *"keep adding decided-on work to the docs to
be queued. We will implement it all at once when everything is unblocked and ready."* So the
QUEUED table is a batch, not a backlog — its dependency column is the load-bearing part.

**Most items here are parked by a decision, not by an omission** — none of E1–E5 is a defect in
the pack system. **Two sections are NOT parked:** §6a is a ruling ready to implement, and §6b is
an audit with a suggested order. Two genuine defects used to live in this stage; §7 records what became of them —
one closed outright, one *believed* closed with the check that would confirm it — so neither is
re-opened from a stale reference nor assumed fixed on my word.

**Reads with:** [`BACKLOG.md`](BACKLOG.md) Stage E (the one-line rows this expands),
[`ROADMAP.md`](ROADMAP.md) item 3 (the composed-file follow-ups),
[`../design/composed-file-permissions.md`](../design/composed-file-permissions.md) (the audit
that surfaced most of this),
[`../design/noncontainer-nix-environment.md`](../design/noncontainer-nix-environment.md) (**potential future
work** — a reproducible tool environment at the `host` notch; see §6), and
[`handoff-guest-notch-macos.md`](handoff-guest-notch-macos.md) (the Mac-gated work, including
the one nix item that is a *prerequisite* rather than an option).

---

## Summary

### QUEUED — decided, to implement together once unblocked

Not parked. These are rulings and audit outcomes waiting on a single implementation pass, in
dependency order. **Do §Q1 first: it turns every item below it from a discovered bug into a
compile-time question.**

| | Work | Ruled / found | Depends on |
|---|---|---|---|
| ✅ Q1 | ~~`Kind` an explicit field + a `KindGuest` member~~ **DONE 2026-08-04** (`84ff918`). Also added `KindUnset` at iota 0, which was not in the spec and is the right call: with `KindJail` at 0 a bare `render.Target{}` claimed the STRONGEST notch — D2's bug with the safety inverted, and newly reachable once the field stopped being derived | §6b D2 | — |
| ✅ Q2 | ~~Wire up `render.Profile`~~ **DONE 2026-08-04** (`f454595` + `a3fa08c`). `ProfileFor` is the notch→preset table; `Target.Profile()` reads it. THREE of the four literals are gone — `packoverlay.Collect`'s parameter was already parameterized and its three callers pass literals, so converting it would just move them one frame out; tracked as §6c's remaining half | §6c step 1 | — |
| ✅ Q3 | ~~D3 wording~~ **DONE 2026-08-04** (`e923f49`) — both refusals now name the COMMAND's limitation, so a future guest target reading the same map is not told a kind is inapplicable when it can honor it | §6b D3 | — |
| ✅ Q4 | ~~**`briefing` wholesale-generated at every notch**, pre-existing prose MOVED to the local pack's `AGENTS.md`~~ **DONE 2026-08-04**. The marker mechanism is gone; §6a-4's jail-side `from` fix landed with it (one resolver, `packload.BriefingProseFor`). Three defects found only by running the lifecycle — see §6a-6 | §6a + §6a-2 (ruled) | Q1, Q5 |
| ✅ Q5 | ~~The conventional LOCAL PACK~~ **DONE** (`374f995`) — — `~/.config/yolo-jail/local/`, implicitly included, zero config | §6a-2 (ruled) | **F1** (a zero-ceremony pack renders nothing at the host today). Q9 shipped without F1 — the lint half was separable, the host-render half was not |
| Q6 | **`skills` wholesale-composed at every notch**, user's tree MOVED into the local pack (union + suffix on differing content, warn), archive only as fallback | §6a-2 (ruled) | Q4 (prove the mechanism on the smaller kind), Q5 |
| ✅ Q7 | ~~`describe` prints the primitive vector~~ **DONE** (`62d1052`). Sources the vector from the resolved MECHANISM, not the notch name alone — so `runtime: macos-user` prints separate-user+Seatbelt even at `jail`, because that backend has no container and no image | §6c step 2 | — |
| ✅ Q8 | ~~Mode set as a target property~~ **DONE** (`a537cfd`) — `render.ModeSet`, the mode-side twin of `FieldSet`; `guest` gets `UndecidedModes(reason)`, fail-closed, for Phase 7 to state | §6b D2 | — |
| Q9 | ~~**`pack lint`: split the two conflated checks** + F5, and `footprint --allow-exec` (F6b)~~ **DONE 2026-08-04** — see §7. **F1 is still open** and was never a lint problem: it is the host render skipping an undeclared skills destination | §7, F5, F6b | nothing |
| ✅ Q10 | ~~Core stops knowing notch NAMES~~ **DONE 2026-08-04** (`51ff674`, `a4e6200`, `592f580`). `render.KindForNotch` / `Kind.String()` are the inbound and outbound edges; `describe` and `config diff` reason about a Kind between them; the last two autonomy literals (the BOOT loop's) now read `Target.Profile()`. Remaining names: `ResolveConfinement` (parsing), `render.kindNames` (the boundary table itself, drift-tested against `config.KnownConfinements`), and the briefing prose — which SHOULD read the Profile and is deferred to Q4, whose agent owns that file | §6c step 3 | Q1, Q2, Q7 |

Field findings F2/F3/F4/F6 are queued separately in
[`feedback-real-pack-adoption.md`](feedback-real-pack-adoption.md) with their own order; F2 and F3
are **dissolved** by Q5 and Q4 respectively rather than needing their own fix.

### PARKED — no decision yet

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

## 6. Potential future work — a non-container nix environment

[`../design/noncontainer-nix-environment.md`](../design/noncontainer-nix-environment.md) is a full design study
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

## 6a. RULED — briefings are fully generated and controlled

**Maintainer ruling, 2026-08-04:** *"I want to claim briefings as fully generated and
controlled. If there's something on the host already at apply, we archive it. This is the
convention, and I will adapt my packs. If you want host instructions, it's like skills, make a
pack."*

So the delimited managed block goes away, and `briefing` becomes **wholesale-generated at every
notch**, matching what the jail already does. A pre-existing file at the destination is
**archived**, not merged, not appended to, not preserved in place.

### Why this is the simplification and not a loss

The marker mechanism existed to solve "source and destination are the same file, so an append
grows without bound and a wholesale write eats the user's prose." That framing accepted a premise
the ruling rejects: that a briefing file is **jointly owned**. Once yolo owns it outright, the
problem it solved does not exist.

What the ruling buys, in order of value:

1. **One mechanism at every notch.** Today the jail regenerates wholesale (to a separate
   staging file, bind-mounted `:ro`) while the host maintains a block inside the user's file.
   Two code paths for one kind, diverging on a property of the destination. That divergence was
   about to become a defect at Phase 7, whose `guest` home is BOTH a real home and one yolo
   provisions — the first destination with both properties.
2. **It makes `briefing` behave like `skills`, which is the model that works.** "Want host
   instructions? Make a pack" is the same answer skills already give, and skills is the kind
   users reported as the headline win (five personal skills reaching four agents instead of
   one). Prose gets the same story: declare it once, it renders everywhere.
3. **The ownership question disappears rather than being answered per-notch.** No
   "is this block mine?", no marker parser, no fail-closed-on-crossed-markers branch, no
   first-apply duplication (F3 evaporates — see below).

### What must be true for this to be safe

- **MOVE to the local pack, not archive** — amended 2026-08-04 by the same ruling that gave
  `skills` a local pack (§6a-2). The user's existing briefing prose moves into
  `~/.config/yolo-jail/local/AGENTS.md`, where yolo composes it back into every destination. So
  the migration is behavior-PRESERVING (their instructions still reach their agents, now through
  the layer model) rather than merely non-destructive. Archiving remains the fallback for prose
  that cannot be moved, so nothing is ever deleted.
- **The union caveat applies here and is worse than for skills.** Prose has no name to
  disambiguate, so several agents' briefings merging into one local `AGENTS.md` must concatenate
  in a stated order with a provenance comment per section (the
  `<!-- from pack: <name> -->` marker `ComposePackBriefings` already emits), warn that it
  happened, and leave the editing to the user. Do not attempt dedup-by-similarity.
- **Warn on the FIRST apply that adopts a destination**, in the shape `confirmHostLosses`
  already established: a user whose hand-written `~/.claude/CLAUDE.md` is about to become
  yolo-owned must be told, once, before it happens. Wholesale ownership of a file the user wrote
  is exactly the one-way door that gate exists for.
- **The retirement path must archive on drop too.** Dropping the last pack contributing a
  briefing destination means yolo no longer owns that file; leaving a generated file behind with
  no owner is the orphan case §11 of `proposed-fixes-open-findings.md` closed for the other
  kinds.

### Consequences to carry into the implementation

- **F3 in [`feedback-real-pack-adoption.md`](feedback-real-pack-adoption.md) is DISSOLVED, not
  fixed.** The duplication it reports is an artifact of the append-based first write. With
  wholesale generation there is no append, so nothing can double. The "adopt the prose into the
  markers" fix — which I rejected for claiming ownership of text yolo did not write — becomes
  moot: the ruling claims that ownership *explicitly and up front*, which is the honest version
  of the same move.
- **`after: "host:<path>"` loses its remaining purpose at the host** and should be re-examined.
  It exists to pull the user's file INTO a jail staging copy; if the host no longer preserves the
  user's file, a declaration that names it is at best decorative and at worst misleading.
- **Blast radius is small and contained**: the markers are referenced only by
  `internal/entrypoint/hostbriefing.go` and its test. No pack declares a marker; no other kind
  reads one.
- **The render fingerprint gate will NOT move.** The jail already generates wholesale, so this
  changes the host path only — which means the fingerprint staying identical is a real check that
  the change is scoped correctly.

## 6a-2. RULED — `skills` wholesale-owned, migrated into a conventional LOCAL PACK

**Maintainer rulings, 2026-08-04.** Two, and the second replaces the archive answer:

> *"I want to force user-level skills migration so we can cleanly own them. Out-of-sync skills
> between agents is the bigger risk."*
>
> *"The answer here is actually not archive. We should designate a default 'local pack' dir,
> something like `~/.config/yolo-jail/local/`, and maybe have that be a default included path?
> Conventions over configuration are always nice… this way we can just move files out of the
> agent dirs and into the local pack and it's like a no-op essentially during migration, rather
> than just an ignored archived dir."*

So: yolo **composes each skills destination wholesale at every notch**, as the jail already does
— and the user's own skills move into a **conventional local pack** rather than an archive.

### Why the local pack is the better answer, stated plainly

Archiving is *safe* but it is not a *migration*: the user's skills end up in a timestamped
directory nothing reads, and getting them back is manual. Moving them into a pack yolo already
composes makes the migration **behavior-preserving** — the same skills reach the same agents,
now through the layer model instead of loose files. That is the difference between "we did not
destroy your work" and "your work still works."

It also fixes the thing the ruling names as the real risk. Today a user's skill lives in each
agent's directory INDEPENDENTLY, so the same skill drifts per agent (`claude` has v2, `codex` has
v1, `pi` never got it) with nothing reporting the divergence. One local pack means one copy,
composed into every destination — which is exactly the win packs already delivered for shared
skills, applied to personal ones.

### The convention

```
~/.config/yolo-jail/
├── config.jsonc          ← already the user-config convention
└── local/                ← NEW: an implicitly-included pack, zero config
    ├── skills/
    └── AGENTS.md
```

Beside `config.jsonc` because that is already where user-scope yolo config lives
(`paths.userConfigSuffix`), so the convention extends an existing one rather than inventing a
location. **Implicitly included** — it needs no `packs` entry, which is the whole point: a user
with a few personal skills should never author a manifest. It is an ordinary pack in every other
respect (`packload.LoadDir` on a dir with no `pack.json` is already zero-ceremony, so this needs
no new pack machinery — see Q8/F1, which must land first or the local pack renders nothing at the
host).

**Conventions over configuration, applied elsewhere too** (maintainer: *"perhaps we can apply
that to other places. Now is the time to break backwards compatibility."*). Candidates worth
considering in the same pass, each of which currently requires an explicit declaration for a
thing that has exactly one sensible location:
`~/.config/yolo-jail/local/AGENTS.md` as the user's own briefing prose (same dir, same rule);
the personal-tree question §6a-2 previously left open (now answered — it is the local pack);
and `from: "skills"` on a `skills` contribution, which every shipped pack declares redundantly.

### Collisions — union, warn, and the empirical case is milder than it looks

The ruling accepts the cost: *"we'll have to be careful to prevent collisions with suffixes… maybe
just union everything and let the user deal with the fallout. We'll clearly warn."*

**Measured before designing** (this jail's four agent skills dirs, 2026-08-04):

| | Finding |
|---|---|
| Names shared across agents | `configuring-the-jail`, `developing-yolo-jail`, `diagnosing-the-jail`, `jail-startup` |
| Are they the same content? | **Byte-identical — 1 distinct hash each across claude/codex/pi** |
| Names unique to one agent | `agent-standards`, `headful-browser`, `new-project`, `open-source-project`, `user-stories` |

So the overwhelming majority of cross-agent duplication is **yolo's own built-ins**, which the
composition already writes as layer 1 and which collide with themselves harmlessly. The
genuinely user-owned skills were unique to a single agent. **A union will usually be conflict-free
in practice**, which means the collision machinery needs to be correct but will rarely fire — the
opposite of the assumption that made it sound expensive.

Design consequences:

- **Union into one local pack, per destination-agnostic name.** Identical content under one name
  is not a collision at all (compare content, not just names) — that alone resolves the common
  case silently and correctly.
- **DIFFERING content under one name is a real conflict and must not be silently resolved.**
  Today `copySkillSubdirs` does `os.RemoveAll(target)` then copies — **silent last-writer-wins**
  (`internal/agents/skills.go:146-150`). That is fine while the loser is a lower layer by design,
  and wrong for two user skills that merely share a name. Suffix them (`<name>-from-claude`), keep
  both, and warn naming both sources: losing one of two hand-written skills silently is the
  failure this whole ruling exists to prevent.
- **`AGENTS.md` union is harder and should NOT try to be clever.** Prose has no name to
  disambiguate and no way to detect semantic duplication. Concatenate in a stated order with a
  provenance comment per section (`ComposePackBriefings` already emits
  `<!-- from pack: <name> -->`), warn that it happened, and let the user edit. Do not attempt
  dedup-by-similarity.
- **Warn loudly, once, at migration.** The migration is the only moment the user has the context
  to fix a conflict; a warning on every subsequent apply trains them to ignore it.

### What the implementation must do

The jail's composition is the specification (`internal/agents/skills.go:73-110`): clear the
destination, then built-ins < each pack in config order < **the local pack last** (so a personal
skill outranks a shared pack's, preserving today's "the user's own copy wins" precedence).

Migration, on the first apply that would adopt a destination:

1. Detect user-owned entries at each destination (the ownership manifest already proves which are
   yolo's).
2. **MOVE them into `~/.config/yolo-jail/local/skills/`**, unioning across agents, suffixing only
   on differing content, warning on every suffix and on every `AGENTS.md` concat.
3. Print what moved, per path, and confirm before doing it (`confirmHostLosses` shape) — this
   moves real user work, even though it moves rather than archives.
4. Re-render, at which point every agent gets every skill and the result is a no-op in effect.

The archive stays as the **fallback for anything that cannot be moved** (a name collision the user
declines to resolve, an unreadable entry), so nothing is ever deleted.

### What this dissolves

- **F2's dangling symlinks** ([`feedback-real-pack-adoption.md`](feedback-real-pack-adoption.md)) —
  a broken link is absent input to a regenerated directory, not a path to negotiate over. And the
  rcm/stow case gets better than "documented recipe": the migration MOVES the real files into the
  local pack, which is what the user was going to do by hand.
- **The tier A/B split may collapse.** Tiers exist because a flat merge into a shared directory
  needs provenance to know what it may touch. If yolo owns the directory outright, namespaced vs
  flat becomes a question about how the AGENT invokes a skill (`pack:skill` vs `skill`), not about
  what yolo may overwrite. Worth checking during implementation — it would delete a concept.
- **The "where does layer 4 live?" question §6a-2 originally left open.** Answered: the local
  pack. No new config key.

## 6a-3. Conventions over configuration — the wider pass

**Maintainer, 2026-08-04:** *"Conventions over configuration are always nice. Perhaps we can apply
that to other places. Now is the time to break backwards compatibility."*

The local pack (§6a-2) is the first instance. Recorded here so the batch does one deliberate
convention pass rather than accreting them, and so the backwards-compatibility break happens
ONCE. Each candidate is a place where an explicit declaration is required for a thing that has
exactly one sensible answer.

| Candidate | Today | Convention | Breaks compat? |
|---|---|---|---|
| **Personal skills / prose** | no home at all — loose in each agent's dir | `~/.config/yolo-jail/local/{skills,AGENTS.md}`, implicitly included | yes: the agent dirs stop being user-writable |
| **`from` on a `skills` contribution** | REQUIRED by `validateContribution`, and all six shipped packs declare the same literal `"skills"` | default to `skills/`; the resolver already does, so only the validator disagrees | no — strictly loosening |
| **`from` on `briefing`** | required; every shipped pack says `AGENTS.md` | default to `AGENTS.md`, then `CLAUDE.md` (the fallback `hostBriefingProse` already implements) | no — strictly loosening |
| **`into` on a `skills`/`briefing` contribution** | required, and it is the per-agent destination | keep required — it is the one field with genuinely several right answers, and F1 shows inferring it is what the JAIL does and the host does not | n/a |

**The `from` rows are the cheap, obvious win and should be part of the same commit as Q9's lint
rewrite**, because they are the same bug: the schema demands a field whose value is always the
same literal, so a pack author writes ceremony that carries no information — and (verified) the
`skills` resolver already defaults it while the validator refuses it, so the two halves of the
code already disagree about whether it is required.

**What NOT to conventionalize, and why it is worth stating**: `into`. F1 is the cautionary case —
the jail INFERS a skills destination when none is declared, the host does not, and that
asymmetry is precisely the silent-no-op bug. A destination has one right answer *per agent*, not
one right answer, so inferring it means inferring the agent set. That is what the pack list is
for. Keep it explicit.

**Compat break, stated once for the whole batch.** After Q4–Q6 the agent skills dirs and briefing
files are yolo-owned: a file dropped there by hand is not read (it is composed away on the next
apply). That is the break, it is deliberate, and it needs to be in the migration guide's
"what changes" section rather than discovered. The migration MOVES existing content into the local
pack, so no user loses anything — but a user who later hand-edits `~/.claude/skills/foo/SKILL.md`
will see it disappear, and the warning at that moment must name the local pack as the place to
put it.

## 6a-4. ✅ FIXED — the JAIL ignored `from` on `briefing`

**Fixed 2026-08-04 with Q4.** Both readers now go through `packload.BriefingProseFor`, over
`packdecl.Contribution.BriefingCandidates()` — one resolver, the `skills` precedent applied.
`run.readPackBriefing` is gone (replaced by `Options.packBriefingProse`).

One thing the fix had to decide that the finding did not raise: `briefing`'s precedence is a
FALLBACK CHAIN, not a single choice. `BriefingCandidates()` returns `[from, AGENTS.md, CLAUDE.md]`
and the host notch has always read the first one that exists — so a declared `from` that is absent
resolves to the convention there. Making the jail refuse instead (which is what `skills` does)
would have converged the two by CHANGING the host, breaking any pack that relied on it. So the
chain stayed and the fallback stopped being silent: it warns, naming the file that was not read,
with two messages — one for "your prose came from somewhere else" and one for "this pack briefs
nothing." A declaration honored differently is still worth a line even when the outcome is fine.

**The original finding, for the record** (verified 2026-08-04, `internal/cli/run/packs.go:429-436`):

    func readPackBriefing(dest string) (string, bool) {
        for _, name := range []string{"AGENTS.md", "CLAUDE.md"} { ... }
    }

It takes a DIRECTORY, not the contribution — so a pack declaring `from: "house-rules.md"` has it
honored at the host (`hostBriefingProse` builds `[c.From, "AGENTS.md", "CLAUDE.md"]`) and silently
ignored in the jail. That is exactly the accepted-and-ignored defect `skills` had until 2026-08-03,
in the sibling kind, and it is a fifth instance of §6b's through-line: the jail's mechanism became
the kind's definition.

`packdecl.Contribution.BriefingCandidates()` now exists as the single authority for the
from-first-then-convention precedence, so the fix is to route both readers through it. **Queued as
part of Q4** (the briefing wholesale rewrite touches this reader anyway), rather than as its own
item — doing it separately would mean editing the same function twice.

## 6a-6. Found shipping Q4: three defects the design did not predict

All three were found by RUNNING the lifecycle (a temp `$HOME`, a real binary, apply → re-apply →
drop) rather than by reading it, and each is a case where the wholesale mechanism inherits a
question the delimited block never had to answer.

1. **A shared ownership record made every briefing look like a dropped pack's output.** The
   briefing record's owner is a PSEUDO-owner (`entrypoint.hostBriefingOwner` = `yolo/briefing`),
   because a composed destination belongs to the pack SET — recording whichever contributor came
   first would make dropping that pack read as "the file is the user's" while the others still
   compose into it. But `droppedPackOrphans` reads every owner in the skills/files record as a
   PACK NAME and archives the paths of any owner absent from `packs`. So a pseudo-owner no config
   can ever name meant every composed briefing was retired on the very next apply. **Fix:** the
   briefing record is its own file (`host-briefing-manifest.json`). Two questions, two key spaces.
2. **The migration's prose only reached the destinations one apply LATE.** The migration CREATES
   the local pack, and the local pack is included by CONVENTION — on the strength of the directory
   existing. So the pack set resolved before the migration cannot contain it, and the very apply
   that promised "your instructions still reach your agents" removed them for one run. **Fix:**
   re-resolve the pack set once, after a confirmed migration only.
3. **An unresolvable pack's briefing was archived.** Under the delimited block this mistake
   self-healed for free — the block re-rendered from prose inside the pack the moment the remote
   was reachable — which is why the briefing prune could key on the RESOLVED set while the skills
   prune keyed on the CONFIGURED one, and why a shipped test asserted that divergence. A wholesale
   destination has no such affordance: archiving it costs the user a trip to the state dir. **Fix:**
   `HostBriefingRequest.PackSetComplete`, fail-closed at its zero value, so the two thresholds are
   now deliberately the same. The old test now pins the convergence rather than the split.

**Also settled: `after: "host:<path>"` is JAIL-ONLY**, which §6a flagged for re-examination. It is
not decorative — the jail case is still real (it prepends the user's host file to a `:ro` staging
copy, so a personal `AGENTS.md` outranks a pack's IN A JAIL) — but at the host the path it names is
now the generated destination, so there is nothing left to prepend. Documented at the field and in
`pack-system.md` rather than removed: it means one thing at one notch and nothing at the other, and
that is worth stating rather than deleting a working feature. It remains a HOST-ACCESS CLAIM at
both notches, since declaring it means reading the host home whatever the notch.

## 6a-5. Found verifying Q5: the local pack LOSES at flat tier

**Probed 2026-08-04, after `374f995` landed.** The local pack is appended LAST in the entry order
(`internal/config/packs.go:205-216`), which is correct — but at `tier: "flat"` a later pack cannot
overwrite an earlier pack's entry at all:

    # codex (tier=flat) + a flat shared pack + the local pack, all claiming skill "mine"
    skills  mine  rendered  invoke as /mine
    skills  mine  skipped (yours)  exists and belongs to pack "sflat" — left untouched
    -> ~/.codex/skills/mine/SKILL.md contains the SHARED pack's body, NOT the local one

So the local pack's "the user's own copy always wins" precedence **does not hold at flat tier**.
The rule is `if occupied && !man.OwnedBy(dest, req.Pack)` (`internal/hostskills/deliver.go:368`):
one pack may not overwrite another's recorded entry, whatever the order. **Pre-existing** — it
arrived with tiered delivery in `aa044eb`, not with the local pack — and it is correct for two
*shared* packs contesting a name. It is wrong for the local pack, which is defined as the layer
that outranks everything.

**Not visible at namespaced tier**, which is why it took a deliberate probe: there each pack gets
its own subtree (`skills/<pack>/skills/<name>`), so a collision is unrepresentable and precedence
is moot. Mixed tiers ship today — `claude`/`copilot` are namespaced, `agy`/`codex`/`pi` are flat —
so a real user hits the flat path.

**Fix belongs to Q6, not a patch here.** Q6 replaces this negotiation with wholesale composition
(clear the destination, then built-ins < packs in config order < the local pack), in which
"later wins" is the mechanism rather than something the ownership manifest must permit. Patching
`deliverFlat` to special-case the local pack would add a rule Q6 then deletes. Q6's acceptance
test must include the flat-tier collision above.

## 6b. Divergence audit — where else a kind means two different things by notch

**Requested 2026-08-04:** *"do a pass for other such misaligned mechanisms. I want to simplify
everything here so it makes more sense with different containment levels."*

Audited all 14 kinds against the code. The pattern the briefing ruling breaks shows up in
**three more places**, and they are not equally wrong — one is the same defect, one is a real
asymmetry with a decision behind it, and one is a gap masquerading as a policy.

| Kind | Jail mechanism | Non-container mechanism | Verdict |
|---|---|---|---|
| `briefing` | wholesale-generated to a staging file, `:ro` | ~~delimited block inside the user's file~~ **wholesale-generated** | ✅ **UNIFIED 2026-08-04** (§6a, Q4) |
| `skills` | **wipe + recompose**: built-ins < packs < user's tree, `:ro` | deliver per-entry, REFUSE what yolo cannot prove it wrote | **same misalignment as briefing** — see D1 |
| `files` | `:ro` bind mount, pack owns the path outright | write, but REFUSE a path the user owns | **correct** — no layer model to recompose; see D1 |
| `config` | four modes (`stateful`/`rmw`/`computed`/`unrendered`) | ONE mode (`rmw`) | **look at this** — see D2 |
| `env` | `-e` on the container | unimplemented ("your shell profile") | **gap, not policy** — see D3 |
| `launch` | argv injection at exec | unimplemented ("nowhere to inject") | **gap, not policy** — see D3 |
| `state`, `mount`, `reads-host` | jail plumbing | refused by name | correct — the concept needs a container |
| `program`, `requires`, `hook`, `autonomy`, `config-overlay` | — | — | aligned, or refused for a stated reason |

### D1 — `skills`: the jail ALREADY owns the directory, so the divergence is real and the
### earlier "it should stay" verdict was WRONG

**Corrected 2026-08-04 after the maintainer asked "why don't we own the skills directory
entirely?" The answer is that in the jail we already do**, and my previous entry here — which
called the divergence correct and warned against unifying — had the jail's mechanism backwards.

`PrepareSkills` (`internal/agents/skills.go:73-110`) builds each destination by:

1. `clearDirContents(skillsDir)` — **wipes the staging dir**, then
2. writes the built-in skill suite,
3. copies each pack's skills in config order (a pack may override a built-in),
4. copies the user's own tree **last**, so a same-named local skill wins,

and the result is bind-mounted `:ro`. So the jail's posture is **total ownership with the user's
tree as one INPUT LAYER** — precisely the composition model `config` uses for keys. The user's
skills are not "beside" yolo's; they are the highest-precedence layer of a directory yolo
regenerates from scratch every launch.

The host does something categorically different: it delivers per-entry into the live directory
and REFUSES any path it cannot prove it wrote, tracking ownership in a manifest
(`internal/hostskills/`). That is not the same idea expressed differently — it is a different
idea. One composes; the other negotiates.

**So `skills` is the same misalignment as `briefing`, and the briefing ruling applies to it by
the same argument.** A `~/.claude/skills` yolo composes wholesale — built-ins < packs < the
user's own tree, exactly as the jail does — is coherent, and it is strictly more useful than
today's host behavior: a user's local skill would still win (layer 4), while a pack's update
would actually land instead of hitting `skipped (yours)`. It also dissolves F2 from
[`feedback-real-pack-adoption.md`](feedback-real-pack-adoption.md): a dangling symlink is not a
path to negotiate over, it is absent input to a regenerated directory.

**What the host would need that the jail gets for free**, and why this is a decision rather than
a patch:

- **A source for the user's own layer — RESOLVED by §6a-2's local-pack ruling.** In the jail,
  layer 4 reads `homeDir/<into>` (the host home) and writes to a staging dir, so source and
  destination are distinct. At the host they are the SAME directory — the identical problem the
  briefing markers were invented for. The answer is the local pack: the user's own skills MOVE to
  `~/.config/yolo-jail/local/skills/` once, and layer 4 reads from there forever after. Not an
  archive, and not a new config key.
- **It is still a bigger migration than the briefing one**, which is why §6a-2 requires a confirm
  and a printed list of what moved: a briefing is one file most users never hand-wrote, while
  `~/.claude/skills` may hold real work. The move makes it a no-op in effect, but it is moving
  real files and must say so.
- **`files` is NOT in this argument.** It is `CombineExclusive` over arbitrary paths
  (`~/.claude/bin/…`, pi's `models.json`), which are not a namespace yolo composes — there is no
  layer model to regenerate from. `hostfilestree.go`'s refuse-what-you-cannot-prove rule stays
  correct there.

### D2 — `config` modes, and the rot the `KindHost` branch exposes

**Two of my earlier claims here do not survive the maintainer's challenge** (*"jail home is bind
mounted, no? so it's not disposable? does this argument really hold?"* and *"special casing on
KindHost seems dangerous? does that expose something rotten?"*). Both were right.

#### The disposability premise was wrong

I justified `stateful` as "genuinely jail-shaped, because a jail home is disposable and the edit
must survive `--rm`." **The jail home is not disposable.** It is bind-mounted from
`paths.GlobalHome()` (`assemble_parts.go:69`, `:ro` at the root with rw binds nested), so it is
host-backed and persists across containers. And the sidecars live under
`<workspace>/.yolo/prism/` — the workspace being a LIVE host bind — so they persist too
(verified: this workspace's `.yolo/prism/` holds `*.last_render` and `*.overlay.json` from
previous boots).

So the real reason `stateful` exists is not persistence. It is that **the destination file inside
a jail is a rendered artifact yolo regenerates every boot**, so an in-jail edit would be lost at
the next render unless it is captured into a sidecar first. That is a fact about *regeneration*,
not about *disposability* — and it means the mode is not jail-specific at all: it applies to any
notch where yolo regenerates a file the agent may edit in place, which includes `guest`.

#### The `KindHost` branch is a symptom; the rot is that the notch is INFERRED

`prism.go:565` is the only live `KindHost` special-case in the codebase (audited: two hits total,
the other is a comment). But look at how the notch is decided (`render/target.go:99-110`):

```go
func (t Target) KindOf() Kind {
    if t.Workspace == "" { return KindHost }
    if t.Home == t.Workspace { return KindPreview }
    return KindJail
}
```

**The notch is inferred from struct shape, not declared.** "No workspace" *means* host; "home
equals workspace" *means* preview. That is load-bearing on an absence, and it has two
consequences:

1. **There is no `KindGuest`, and a guest Target would silently resolve to `KindJail`.** A guest
   home is a real home *with* a workspace, and `Home != Workspace`, so it takes the jail branch
   by default — inheriting jail semantics for provenance, sidecars, and modes, with nothing
   anywhere saying that was a choice. Note `internal/render/confinement.go` DOES have
   `GuestProfileMacOS` / `GuestProfileLinux`, so guest is expressible as a *confinement profile*
   and inexpressible as a *render target*. The two halves of the same notch disagree about
   whether it exists.
2. **A future target that happens to have no workspace becomes "host" by accident**, gaining the
   provenance write and the every-surface-rmw posture without asking for either.

So yes — the special-case exposes something rotten, and the rot is not the `if`. It is that
`Kind` is derived rather than stated, so adding a notch is not a compile error anywhere. **Make
`Kind` an explicit field set by the constructors**, keep `KindOf()` as an accessor, and add
`KindGuest` with no behavior — the point is that every `switch` on Kind then fails to compile (or
falls to a default that must be written) until Phase 7 states its answer. That is the change that
turns "guest inherits whatever branch it falls into" into "guest cannot be added without
deciding."

**Then the mode set becomes a property of the target**, the way `FieldSet` already is for kinds,
rather than a runtime `if` — which is the same fix D3 needs and the same shape the whole audit
keeps arriving at.

### D3 — `env` and `launch`: refused with a *mechanism* reason, which hides a real gap

Both are in `hostUnimplemented` with honest-sounding text — *"the only place to set these
off-container is your shell profile, which apply --host does not write"* and *"launch flags need a
launcher — apply --host configures your tools but never runs them."*

**Both reasons are true of `apply --host` and false of the notch.** They describe a missing
*verb*, not an inapplicable *kind*: `yolo --at host -- <cmd>` (the design's own §4.1 escape valve,
Option 2 of [`../design/noncontainer-nix-environment.md`](../design/noncontainer-nix-environment.md))
would make both renderable immediately, because yolo would be the one launching the process. And
at `guest` the verb ALREADY exists — macos-user execs the agent today — so `env` and `launch` are
not "unavailable below jail" at all. They are unavailable in the one sub-case where yolo never
launches anything.

That is the misalignment: the census says these kinds apply at a *notch*, but the refusal is
written about a *command*. A `guest` target inheriting that text would refuse two kinds it can
actually honor. Fixing the wording is trivial; the useful part is that it identifies
`--at <notch> -- <cmd>` as the thing that unblocks two kinds rather than as a convenience.

### The through-line, and the one root cause

Four of the fourteen kinds diverge, and three of the four are the same shape: **a mechanism
chosen for the jail became the definition of the kind.** The non-container notches then got a
second mechanism (`briefing`, `skills`), an implicit narrowing (`config` modes), or a refusal
phrased in terms of the jail's plumbing (`env`/`launch`).

**Underneath all of them is D2's finding: the notch is INFERRED, not declared.** `Kind` is
derived from whether a Target happens to have a `Workspace`, so there is no place where adding a
confinement level forces anyone to answer "what does this kind mean here?" — a guest Target
resolves to `KindJail` and inherits jail semantics silently. Every other item in this audit is
what that looks like one kind at a time.

So the simplification the maintainer asked for has a concrete first move that is smaller than any
of the individual fixes: **make `Kind` an explicit field with a `KindGuest` member**, so the
compiler asks the question instead of a struct's shape answering it by accident.

Suggested order:

1. **`Kind` explicit + `KindGuest`** (D2's root cause). Mechanical, and it makes everything below
   a compile-time question rather than a discovered bug.
2. **D3 wording** — strings only; stop describing a missing verb as an inapplicable kind.
3. ✅ **§6a briefing unification** (ruled) — **DONE 2026-08-04**. It WAS contained and the
   fingerprint did NOT move (host-side only, as predicted). What the estimate missed is that
   three defects only appeared when the lifecycle was RUN, all of them about ownership records and
   resolution ORDER rather than about composition — see §6a-6, and expect the same class in Q6.
4. **`skills` wholesale composition** (D1) — the same ruling extended, but flag the migration
   cost first: a briefing is one file most users did not hand-write, while `~/.claude/skills` may
   hold real work.
5. **Mode set as a target property** (D2's second half), as part of Phase 7 where it is forced.
6. **`files`: no change**, and record why, so the ruling is not over-applied to a kind with no
   layer model.

## 6c. Could confinement be packs? — "right in spirit," and closer than expected

**Maintainer, 2026-08-04:** *"what's the chance we could turn confinement into packs? That seems
wrong in practice, but right in spirit. Can we come closer to this in a sane way? Ideally core
doesn't even know about guest/host/jail/whatever. They're just composable modules of some sort."*

**The short answer: the model already exists and is not wired up.** `internal/render/confinement.go`
models confinement exactly as asked — independent PRIMITIVES with the notches as presets over
them:

```go
PrimNamespaces  PrimVM  PrimSeatbelt  PrimLandlock  PrimSeparateUser  PrimBakedImage

JailProfile(useVM)   = {namespaces|VM, bakedImage}     autonomy on
GuestProfileMacOS()  = {separateUser, seatbelt}        autonomy on
GuestProfileLinux()  = {namespaces, landlock}          autonomy on
HostProfile()        = {}                              autonomy off
```

That is "composable modules with presets," written down, with a doc comment explaining that the
combinations are real (a separate user without Seatbelt; a namespace with neither).

**And nothing consumes it.** Audited 2026-08-04: `render.Profile`, `render.Primitive`, and all
three preset constructors have **zero production callers** — every reference outside
`confinement.go` is a doc comment. Meanwhile the policy the Profile is supposed to carry is
decided by **hardcoded literals at four call sites**:

```
packoverlay.go:114   p.SurfacesFor(autonomy)   ← the only one that takes a parameter
packoverlay.go:176   p.SurfacesFor(true)       ← literal
hostoverlayprune.go  p.SurfacesFor(false)      ← literal
hostrender.go:126    p.SurfacesFor(false)      ← literal
```

So `Profile.AgentAutonomy` exists, is documented as the §4.2 policy bit, and is never read. The
notch's behavior lives in `true`/`false` constants chosen per file — which is the same rot as D2's
inferred `Kind`, in a second place.

### Should confinement literally become packs? No — and the reason is worth keeping

A pack is **content plus declarations that yolo renders**. Confinement is **enforcement yolo
executes**, and it is the security boundary that decides whether a pack may read the host at all.
Making confinement a pack inverts that: the thing being confined would declare its own
confinement, and `MayAccessHost` — the gate that makes a fetched pack safe — would be decided by
a pack. That is not a slippery slope, it is a direct contradiction.

The two also differ in what "composable" means. Pack kinds compose by MERGE (later pack wins a
key, skills concatenate). Confinement primitives compose by INTERSECTION of what is permitted —
adding a primitive can only remove capability. A mechanism whose composition rule is "later wins"
cannot express "strictest wins" without becoming a different mechanism.

### What "closer, in a sane way" looks like — three steps, each shippable

1. **Wire up the model that exists.** Make `Profile` the single source for `AgentAutonomy` and
   delete the four literals. Cheap, removes a class of "which file did I edit?" bug, and gives the
   primitive vector its first real consumer. **This is the whole of what makes the rest possible.**
2. **`describe` prints the primitive vector.** The file's own comment already says this is the
   intent (*"an implementation fact that `describe` can print"*) and it does not. Once printed,
   "what does guest actually give me?" is answerable without reading Go — and a preset that
   composes nothing (today's `HostProfile`) becomes visibly the weakest rather than nominally a
   level.
3. **Then core stops knowing notch NAMES.** With (1) and (2) done, the remaining `"jail"`/`"guest"`/
   `"host"` string comparisons in core are: `config.ResolveConfinement` (config parsing — a name
   must exist somewhere, and this is the right somewhere) and `agents/agentsmd.go:66-77` (the
   briefing's per-notch prose). Both become "look up a preset by name at the edge, pass a Profile
   inward." That is as close to the maintainer's goal as is coherent: **core reasons about
   primitives; only the config boundary knows the names.**

   **SHIPPED 2026-08-04** (`51ff674`, `a4e6200`, `592f580`). The two edges are
   `render.KindForNotch` (a name in) and `Kind.String()` (a label out); `describe` and `config
   diff` each cross once and reason about a Kind after. Three things worth carrying forward:

   - **The audit found MORE literals than the step predicted, and they were the load-bearing
     ones.** Step 1 was recorded as leaving only `packoverlay.Collect`'s three callers, but the
     BOOT loop (`ConfigurePackSurfaces`) and `ConfigurePackByName` each also passed autonomy
     implicitly through `p.Surfaces()` — the wrapper that hardcodes `true`. So the notch policy
     on the path that reaches a real jail was still a constant.
   - **Mutation caught a test gap the byte gate could not.** Negating the boot loop's autonomy
     left the whole suite green: every jail-notch posture assertion (and
     `TestRenderFingerprintStable` itself) drives `ConfigurePackByName`, the non-boot entry.
     `bootautonomy_test.go` now covers the loop directly, both directions.
   - **`packoverlay.Collect` keeps its bool**, promoted from leftover to boundary: every caller
     derives the bit from a notch now, and a package whose job is resolving overlays against
     owners needs one bit, not the confinement model.

   The briefing prose (`agents/agentsmd.go:66-77`) was NOT converted — it should read the
   Profile (describe the PRIMITIVES a notch composes, so the text is accurate for a notch
   nobody has enumerated), and that file belongs to Q4's briefing-wholesale work. Tracked
   there rather than done twice.

**What stays out of scope deliberately:** letting a user hand-assemble a primitive vector in
config. `happy-path-principle.md` rules, and `confinement.go`'s own comment already draws this
line — three named presets are selectable, the vector is an implementation fact. Wiring the model
up does not change that, and should not be read as a step toward it.

### Why this matters beyond tidiness

It is the same finding as D2 from a different angle. D2: the render notch is INFERRED from struct
shape, so `guest` resolves to `KindJail` silently. Here: the confinement notch's policy is a
LITERAL per call site, so `guest` has no way to express its own answer at all. **Both mean the
same thing — there is no single place that says "this is the notch, here is what it implies," so
adding one is invisible instead of impossible.** Step 1 above and D2's explicit `Kind` field are
the same fix applied to the two halves of the notch, and they should land together.

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

  **Confirmed by probe 2026-08-04**, so the earlier caveat on this row is withdrawn. With
  `copilot` selected and `~/.copilot/config.json` seeded with
  `{"github_token":"ghu_SENTINEL_TOKEN","other":"mine"}`, a host `apply --assert` produced
  `{"github_token":"ghu_SENTINEL_TOKEN","other":"mine","yolo":true}` — token intact, the user's
  own key intact, yolo adding only its one managed key. `config render copilot` does not even
  list the surface: `rmw` and `unrendered` surfaces are skipped because there is no layer fold
  to preview (`internal/cli/config.go:212`), which is the same reason there is no sidecar whose
  absence could reduce the file. Note `rmw` has been on that surface since the pack was created
  (`6d4a050`), so the original defect report most likely described the **pre-pack** writer.
  The ROADMAP row can be deleted.
- **BACKLOG E8, the nightly-macOS builder arch mismatch.** Closed 2026-08-03; it had **three**
  instances of one hardcoded-arch assumption rather than the one its entry named. One caveat
  remains and is not a defect: `publish.yml` is tag-triggered, so the multi-arch image does not
  reach GHCR until the next release and the nightly stays red until then.

**FIXED 2026-08-04 (Q9 + F5 + F6b) — `pack lint`'s "nothing reads this pack" rule asked the
wrong question.** The account below is kept as the diagnosis; what shipped is at the end of
this section. Independently reported as F5 in
[`feedback-real-pack-adoption.md`](feedback-real-pack-adoption.md) from a real migration, and
confirmed here by probe. Pre-existing (verified against `HEAD~1`), not a regression from the
skills-`from` work that surfaced it.

The rule (`internal/cli/pack.go:372`) fired
*"pack has neither a skills/ dir nor an AGENTS.md — it would stage files nothing reads"*. It
rejects **all six packs yolo ships**, and a real user's pure-`files` pack.

**Why it is wrong, not just noisy.** It asks *"did this pack stage `skills/` or `AGENTS.md`?"*
as a proxy for *"does anything read this pack?"*. That proxy was true when a pack could only
ship content. A pack now contributes any of **14 kinds**, so config-only and `files`-only are
legitimate, useful shapes and the proxy is simply false. Probed 2026-08-04:

| Pack shape | Should warn? | Did (pre-fix) | Does now |
|---|---|---|---|
| config-only (renders a surface, ships no files) | **no** — it does real work | ✗ warns | ✓ clean |
| **no contributions at all** | **yes** — does nothing | ✗ warns, *same message* | ✓ "declares ZERO contributions" |
| declares `skills` w/ default `from`, ships none (the shipped-pack shape) | no | ✗ warns | ✓ clean |
| `files` + `config-overlay`, no `AGENTS.md` (F5's real case) | no | ✗ warns | ✓ clean |
| typo'd `from: "my-skils"` | **yes** | ✓ warns, correctly | ✓ warns, and only once |

The second row is the tell: a pack that does **absolutely nothing** and a working config pack
get the *identical* message. The rule cannot distinguish them, so it is useless in the one case
it should catch.

**Two questions, currently conflated into one bad check.** The maintainer's framing
(2026-08-04): *"a pack that does absolutely nothing should be warned about, but not one that
leaves out a part it could ship."* Those are separate:

1. **Does this pack do anything?** Answerable from the manifest, and `pack footprint` ALREADY
   answers it exactly — `(no declared claims)` vs. a listed claim. So the check is "zero
   contributions", not "no `skills/` dir".
2. **Does a declared source deliver nothing?** Already implemented and already correct — the
   non-conventional-`from` check, the last row above.

**The convention exemption is a patch on the wrong rule, and should go away with it.** When
`from` became honored, keying the missing-source warning on `from != ""` warned on all six
shipped packs (each declares `from: "skills"` and ships none, because its contribution exists
to NAME the destination other packs merge into). Exempting the conventional dir silenced that
correctly — but it is a workaround for a rule whose premise had already expired. Ask question 1
directly and the exemption is unnecessary: a shipped pack's `skills` contribution *does* claim
something reachable, so it passes without a special case.

**What shipped, 2026-08-04** (lint-only — no boot path; the entrypoint fingerprint test is
unchanged). Two honest checks in place of one bad one, mutually exclusive by construction so
one mistake never draws two lines:

- **"pack declares ZERO contributions and ships nothing read by convention"** — in those words.
  Requires BOTH halves: a manifest-less `skills/`+`AGENTS.md` pack does real work through the
  jail's zero-ceremony merge, so keying on the manifest alone would fail-lint the pack
  `pack init` scaffolds.
- **The declared-source-stages-nothing check, kept**, and now resolved through
  `SkillsSource()` instead of `c.From` — so it is correct either way when `from` becomes
  optional for skills/briefing.
- **The "neither a skills dir nor an AGENTS.md" rule, NARROWED** to the case it was actually
  about: staged content that NO contribution's source claims and that sits in no
  conventionally-read location, reported with the offending filenames. It fires only when *not
  one* staged content file is claimed — a pack whose content mostly lands correctly does not
  need a linter nitpicking a stray file. Root-level `pack.json` / `derive.lua` / `README.md` /
  `LICENSE` / `CHANGELOG.md` / `.gitignore` / `.gitattributes` are not content and are exempt,
  which is what lets a config-only pack carry a README.
- **F6b**: `pack footprint` takes `--allow-exec`, so a pack you can `lint` you can also
  inspect. The refusal without the flag stands — the flag supplies the consumer's half of the
  decision, it does not remove the gate.

**The convention exemption STAYED, and the §7 prediction above was wrong about why.** Asking
question 1 directly does not make it redundant: the missing-source complaint is
*per-contribution* while both replacement checks are *about the pack as a whole*, so a shipped
pack passing them (it has contributions, and stages no unclaimed content) cannot silence a
complaint about its own `skills/` being absent. The exemption is what keeps a shipped pack's
destination-naming contribution from drawing it. `TestShippedPacksLintClean` now asserts all
six lint clean outright, which is the assertion that would have caught the original defect.

**F1 is NOT fixed here** and remains open: a zero-ceremony pack (no `pack.json`) lints
`✓ pack ok`, stages files, and renders **nothing** at the host — the jail infers a destination
(`SkillsSourceDirs`' `if !declared` fallback) while the host iterates declared contributions
only. Confirmed by probe 2026-08-04. It is a host-render gap, not a lint one; lint is now
correct to accept that pack, since a jail really does read it.

**Still open, and NOT a Stage E item** (it is a validation gap, tracked in
[composed-file-permissions.md §4.5](../design/composed-file-permissions.md)): **reserved
destinations miss symlink targets.** `~/.config/git/config`, `~/.config/bashrc`, and
`~/.claude/claude.json` validate while their aliases are rejected. Verified still live —
`internal/config/helpers.go:87-95` resolves symlinks lexically with an `EvalSymlinks` fallback,
but the reserved-destination guard (`writablehome.go:54`) matches on the declared path.
