# Stage E — parked items

**What this is.** The design work deliberately *not* built, gathered in one place with the
reason each is parked and what it would take to unpark it. Written 2026-08-03, after the pack
system reached functional completeness, so that "what is left?" has one honest answer.

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

| # | Item | Parked because | Unpark cost |
|---|---|---|---|
| E1 | `host_files` modes 4→3 (`copy` merges into `readonly`) | behavior change on a shipped key; **blocked on E2** | small once E2 is decided |
| E2 | `readonly` as a real `:ro` mount instead of `0o444` | needs a per-surface design pass — you cannot compose *into* a `:ro` mount | a design decision, then small |
| E3 | Capture timing (`yolo config capture` + capture on terminate) | **not urgent** — nothing is lost today, only observability lags | small; the cheap half is one subcommand |
| E4 | Comment preservation on `json`/`toml` surfaces | starts from decisions, not blank | cheapest useful step needs no comment parsing at all |
| E5 | `managed`/`defaults` array-append pinning | no user surface has ever needed it | small, but do not build it speculatively |
| §6a | **RULED 2026-08-04 — `briefing` becomes wholesale-generated at every notch**, pre-existing file ARCHIVED | not parked: ruled, ready to implement | contained; markers have 2 referents, fingerprint must not move |
| §6b | **divergence audit** — 3 more kinds where the jail's mechanism became the kind's definition | D3 is wording; D2 is forced by Phase 7; D1 is a principle to record | see the suggested order at the end of §6b |
| §7 | **`pack lint`'s content rule asks the wrong question** | pre-existing; needs the two conflated checks split | lint-only, no boot path |

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

- **ARCHIVE, never delete.** The pre-existing file moves under the archive root with the apply's
  stamp, reclaimed by `yolo prune` — the mechanism `hostskills.Archive` already provides and
  which `files`/`skills` retirement already uses. This is the whole safety story, so it is not
  optional and it must be reported by path.
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

## 6b. Divergence audit — where else a kind means two different things by notch

**Requested 2026-08-04:** *"do a pass for other such misaligned mechanisms. I want to simplify
everything here so it makes more sense with different containment levels."*

Audited all 14 kinds against the code. The pattern the briefing ruling breaks shows up in
**three more places**, and they are not equally wrong — one is the same defect, one is a real
asymmetry with a decision behind it, and one is a gap masquerading as a policy.

| Kind | Jail mechanism | Non-container mechanism | Verdict |
|---|---|---|---|
| `briefing` | wholesale-generated to a staging file, `:ro` | delimited block inside the user's file | **RULED — unify on wholesale** (§6a) |
| `files` | `:ro` bind mount, pack owns the path outright | write, but REFUSE a path the user owns | **divergence is correct** — see D1 |
| `skills` | merge into staging dir, `:ro` | deliver into the real dir, tiered + ownership manifest | **divergence is correct** — same reason as D1 |
| `config` | four modes (`stateful`/`rmw`/`computed`/`unrendered`) | ONE mode (`rmw`) | **look at this** — see D2 |
| `env` | `-e` on the container | unimplemented ("your shell profile") | **gap, not policy** — see D3 |
| `launch` | argv injection at exec | unimplemented ("nowhere to inject") | **gap, not policy** — see D3 |
| `state`, `mount`, `reads-host` | jail plumbing | refused by name | correct — the concept needs a container |
| `program`, `requires`, `hook`, `autonomy`, `config-overlay` | — | — | aligned, or refused for a stated reason |

### D1 — `files` and `skills`: the divergence is about OWNERSHIP, and it should stay

In a jail, `files` is `CombineExclusive` and the mount replaces whatever was there. In a real
home the same claim cannot mean "overwrite what the user has" — `hostfilestree.go` says it
exactly right: *"exclusivity is a rule about which PACK may claim a path, not a licence over the
user's own files."*

**This is NOT the briefing case, and the difference is worth stating** because it would be easy to
over-apply the ruling. A briefing destination is a file whose *entire purpose* is the content
yolo generates, so claiming it wholesale is coherent. `~/.claude/skills/` and `~/.claude/bin/`
are **shared namespaces**: the user's own skills live beside the pack's, so "yolo owns this
directory" would mean deleting skills the user wrote. Refuse-what-you-cannot-prove-you-wrote is
the right rule there, and the ownership manifest is what makes it checkable.

So the unifying principle is not "always wholesale" — it is **one owner per destination, declared
and enforced**. `briefing` becomes wholesale because the ruling assigns yolo the owner.
`skills`/`files` stay per-entry because the destination is shared by design.

### D2 — `config`: four modes in a jail, one at every other notch

`prism.go:552-565` documents this deliberately: in a jail `rmw` is one mode among four and the
surfaces that matter are `stateful`; at the host `rmw` is the ONLY mode (OQ-4), so "rmw records
nothing" became "the host records nothing" — which is what made `config diff` infer a winner from
declarations and report an overlay as having LOST a key it had WON. That specific bug is fixed
(host provenance now written after the surface write).

**The question the ruling raises for it:** if the host has one mode, are the other three
*jail-only features* or *unfinished elsewhere*? `stateful` (capture in-jail edits into a sidecar)
is genuinely jail-shaped — it exists because a jail home is disposable and the edit must survive
`--rm`. But a `guest` home is NOT disposable, so at Phase 7 "which modes apply" needs an answer,
and today's answer is implicit in a `KindOf() == KindHost` branch rather than declared. Cheapest
honest step: make the mode set a property of the TARGET (like `FieldSet` is for kinds) rather
than a runtime `if`, so `guest` has to state its answer instead of inheriting whichever branch it
falls into.

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

### The through-line

Three of the four are the same shape: **a mechanism chosen for the jail became the definition of
the kind**, and the non-container notches got either a second mechanism (briefing), an implicit
narrowing (config modes), or a refusal phrased in terms of the jail's plumbing (env/launch). The
kind should be defined by what it CLAIMS; the notch should decide how that claim is honored.
`FieldSet` already does this for applicability — D2 and D3 are the same idea applied to
*mechanism* and *reason*.

Suggested order, cheapest and least controversial first: **D3 wording** (docs/strings only) →
**§6a briefing unification** (ruled, contained) → **D2 mode-set-as-target-property** (do it as
part of Phase 7, where it is forced) → **D1: no change, but record the principle** so the
briefing ruling is not over-applied to shared namespaces.

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

**New finding, 2026-08-03/04 — `pack lint`'s "nothing reads this pack" rule asks the wrong
question.** Independently reported as F5 in
[`feedback-real-pack-adoption.md`](feedback-real-pack-adoption.md) from a real migration, and
confirmed here by probe. Pre-existing (verified against `HEAD~1`), not a regression from the
skills-`from` work that surfaced it.

The rule (`internal/cli/pack.go:372`) fires
*"pack has neither a skills/ dir nor an AGENTS.md — it would stage files nothing reads"*. It
rejects **all six packs yolo ships**, and a real user's pure-`files` pack.

**Why it is wrong, not just noisy.** It asks *"did this pack stage `skills/` or `AGENTS.md`?"*
as a proxy for *"does anything read this pack?"*. That proxy was true when a pack could only
ship content. A pack now contributes any of **14 kinds**, so config-only and `files`-only are
legitimate, useful shapes and the proxy is simply false. Probed 2026-08-04:

| Pack shape | Should warn? | Does today |
|---|---|---|
| config-only (renders a surface, ships no files) | **no** — it does real work | ✗ warns |
| **no contributions at all** | **yes** — does nothing | ✗ warns, *same message* |
| declares `skills` w/ default `from`, ships none (the shipped-pack shape) | no | ✗ warns |
| `files` + `config-overlay`, no `AGENTS.md` (F5's real case) | no | ✗ warns |
| typo'd `from: "my-skils"` | **yes** | ✓ warns, correctly |

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

**Recommended fix** (lint-only — no boot path, no fingerprint risk; `SkillsSources()` has
exactly one non-test consumer, this rule):

- warn when a pack declares **zero contributions** — and say so in those words, not in terms of
  `skills/`;
- keep the declared-source-stages-nothing check as-is;
- drop the "neither a skills dir nor an AGENTS.md" rule entirely, or narrow it to the case it
  is actually about (a pack that stages FILES which no contribution claims — files that really
  would be read by nothing).

While in there, F1 from the field feedback is the same family and worth fixing together: a
zero-ceremony pack (no `pack.json`) lints `✓ pack ok`, stages files, and renders **nothing** at
the host — because the jail infers a destination (`SkillsSourceDirs`' `if !declared` fallback)
and the host iterates declared contributions only. Confirmed by probe 2026-08-04.

**Still open, and NOT a Stage E item** (it is a validation gap, tracked in
[composed-file-permissions.md §4.5](../design/composed-file-permissions.md)): **reserved
destinations miss symlink targets.** `~/.config/git/config`, `~/.config/bashrc`, and
`~/.claude/claude.json` validate while their aliases are rejected. Verified still live —
`internal/config/helpers.go:87-95` resolves symlinks lexically with an `EvalSymlinks` fallback,
but the reserved-destination guard (`writablehome.go:54`) matches on the declared path.
