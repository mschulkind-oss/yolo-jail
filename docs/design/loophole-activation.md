---
title: "Nothing reaches your host because it happened to be there — loophole activation"
date: 2026-08-18
status: accepted
tags: [loopholes, packs, activation, security, config]
summary: "A loophole is active today because it was present and something it named happened to exist on the host. Six rulings replace that end to end: presence stops implying activation, a pack declares a default, and the default is disabled. All thirteen questions are settled; what is left is building it."
---

# Nothing reaches your host because it happened to be there — loophole activation

**Status:** DECIDED 2026-08-18, and **substantially BUILT the same day**. Six rulings (§2) and
**all thirteen questions settled** (Decision Ledger below). §1.3's table is the honest progress
report: seven of its eight rows are ✅, and the one that is not — the broker's jail wiring — is
blocked on a mechanism gap rather than a decision ([`broker-as-a-pack.md`](broker-as-a-pack.md)
§6.1). Sequenced in [`roadmap.md`](../plans/roadmap.md).

**The one real gap is closed.** `default_enabled` collided with a live `enabled` key and the design
never said which won; OQ-A9 ruled one key, renamed, governing all four manifest sources.

> [!WARNING]
> **Two traps that will bite the implementation, both easy to walk into.** Deleting
> `bundled_loopholes/claude-oauth-broker/` does **not** free the reserved name, so the reservation and
> the `loopholesruntime.go` name special-case must die in the same commit or every claude user's
> launch breaks ([§2](#2-the-rulings)). And the key rename needs a **refusal** for reverse skew, not a
> tolerance: an older yolo ignores `default_enabled` and runs `audio` **on** ([§2](#2-the-rulings),
> [§4](#4-what-it-costs)).
>
> **The first trap is now DISCHARGED FOR EVERYTHING BUT THE BROKER, and the discharged part is
> only half misleading — read which half.** Four names left the reserved set on 2026-08-18 by two
> different mechanisms. `host-processes` and `audio` were reserved only as bundled DIRECTORY names,
> read off the same embed.FS the loader materializes, so `git mv` retired them with no code change.
> `journal` and `cgroup-delegate` were **constants** (`paths.BuiltinLoopholeNames`), so each had to
> be deleted BY HAND in the commit that shipped its manifest — and that list is now gone entirely.
> The broker's has the second shape: `broker.BrokerLoopholeName` is appended unconditionally, from
> the broker's own constant. A reader generalizing from the two FREE cases would ship the
> launch-breaking commit; a reader who follows what `journal` and `cgroup-delegate` did will not.

The doc grew thirteen questions on purpose: every one came from asking *"what else reaches the host,
and why is it on?"*, and the answer kept being "something different each time".

**The short version.** A loophole is active today because it was *present* and something it named
happened to exist on the host — bundled, plus a `requires` predicate that sniffs `PATH`. That is
being replaced end to end: **presence stops implying activation.** A pack declares a loophole's
default state, that declaration defaults to **disabled**, config overrides it at either scope, and
the `requires.command_on_path` sniff is **deleted** rather than fixed — it is the mechanism, not a
bug in the mechanism. The principle behind it, in the maintainer's words: *"we don't give host
access by default."*

**If you read one section, read §1.3** — the inventory of everything that reaches your host and why
it is currently on. No two rows agree, and that is the whole argument.

§1.4 is the finding that should worry you most: core's config schema names two loopholes by hand —
and after OQ-A6 both of those names go, which is what makes the conversion mean something.
**Both went on 2026-08-18.** `host_processes` and `journal` are refusals now, `paths.BuiltinLoopholeNames`
is deleted, and core's schema names no loophole at all. §1.3's table has one row left that is not
✅ — the broker's jail wiring, blocked on a mechanism gap (see `broker-as-a-pack.md` §6.1).

**Reads with:** [`broker-as-a-pack.md`](broker-as-a-pack.md) (the sprint this came out of; §5.5 is
the connection preamble, §12 the `host-processes` conversion),
[`loophole-packaging-overview.md`](loophole-packaging-overview.md) (§5 "Defaults, and what stays
bundled" — this supersedes its activation story),
[`gate-placement-principle.md`](gate-placement-principle.md) (why a second gate over the same act is
worse than none).

## Decision Ledger

The six rulings live in [§2](#2-the-rulings) and are not repeated here. These are the questions that
have since been settled and folded into the body.

| ID | Ruling | Date | Settled in |
| :--- | :--- | :--- | :--- |
| **OQ-A1** | The broker ships `default_enabled: true` **inside `packs/claude`** — selecting the pack is what turns it on | 2026-08-16 | [§4](#4-what-it-costs) |
| **OQ-A2** | **Going dark is fine.** No migration machinery, no upgrade notice — a loophole you never listed behaves like an agent pack you never listed | 2026-08-17 | [§4](#4-what-it-costs) |
| **OQ-A3** | `default_enabled: true` stays available to **fetched** packs, unrestricted — the origin gate is the gate | 2026-08-16 | [§3](#3-what-this-does-not-license) |
| **OQ-A4** | The cgroup delegate becomes **opt-in**, like everything else — no presence-activated exception · ✅ **BUILT 2026-08-18** | 2026-08-18 | [§1.2](#12-the-three-things-this-doc-first-ignored--raised-in-review-and-one-is-a-real-hole), [§1.3](#13-everything-that-reaches-your-host-and-how-it-turns-on) |
| **OQ-A5** | **Keep all three gates** for `yolo-ps` — they answer different questions, and only two of them are new | 2026-08-18 | [§1.2](#12-the-three-things-this-doc-first-ignored--raised-in-review-and-one-is-a-real-hole) |
| **OQ-A6** | `journal` and `cgroup-delegate` **become manifest loopholes — in this sprint**, not after it · ✅ **BUILT 2026-08-18** | 2026-08-18 | [§5](#5-the-structural-questions-this-opened) |
| **OQ-A7** | A loophole-only pack **needs selecting**. No special case: shipped in the binary is not installed | 2026-08-17 | [§5](#5-the-structural-questions-this-opened) |
| **OQ-A8** | A loophole's settings are **typed and declared in its manifest**, not an opaque map — 📄 [`pack-config-keys.md`](pack-config-keys.md) | 2026-08-17 | [§1.4](#14-the-finding-that-undercuts-the-conversion--core-hardcodes-two-loopholes-by-name) |
| **OQ-A10** | The broker's loophole is a contribution of **`packs/claude`**, not its own pack — and `broker-as-a-pack.md` §6 is corrected rather than left standing | 2026-08-18 | [§2](#2-the-rulings) |
| **OQ-A11** | **Gate the broker daemons** on the loophole record; **leave the nix socket** ungated and say why | 2026-08-18 | [§1.3](#13-everything-that-reaches-your-host-and-how-it-turns-on) |
| **OQ-A12** | `yolo check` learns to read pack-shipped loopholes **in this sprint**, as part of the conversion | 2026-08-18 | [§4](#4-what-it-costs) |
| **OQ-A13** | **R5 stands** — a workspace may still enable. Mirror the existing OFF-direction disclosure onto ON, and treat it as readability rather than a control | 2026-08-18 | [§2](#2-the-rulings) |
| **OQ-A9** | **One key, renamed.** `default_enabled` *is* `enabled` with the default flipped, governing all four manifest sources. `SetEnabled` stops writing manifests; it instructs rather than writing config (see the note in §2) | 2026-08-18 | [§2](#2-the-rulings) |

---

## 1. What activates a loophole today

Three layers already exist and are well-named (`internal/loopholes/discover.go:643-680`):

| | today | meaning |
|---|---|---|
| `Enabled` | defaults **true** from the manifest; config may override | *"the user's switch"* |
| `Active` | `Enabled` **and** not superseded **and** `platforms`/`requires` are satisfied | *"the machine can run it"* |
| `Honored` | `Active` **and** the origin gate admits it | *"the pack it came from may touch the host"* |

The layering is right. What is wrong is what feeds the first one: `enabled` defaults to **true** when
a manifest omits it (`loopholedecl.go:509-511`), and **all four shipped manifests set it explicitly
anyway** — the three bundled ones and the official audio pack's. So a loophole is on the moment it is
*present*, and presence was never a decision the user made.

*(Two corrections from the completeness sweep, §6. `Active` also requires `!Superseded()`
(`loopholes.go:230-232`) — a capability another pack has taken over is inactive however enabled it
is, and the code carries a note that a design doc already got this table wrong once. And the default
above was first cited as `discover.go:50`, which is the **config-block** synthesis path, not the
manifest decoder.)*

### 1.1 The sniff, and the bug it is causing right now

`requires.command_on_path` is `exec.LookPath` **on the host** (`internal/loopholes/loopholes.go:176`).
Two manifests use it:

- `claude-oauth-broker` requires `claude` on the host's PATH, meaning *"only run the broker if Claude
  Code is installed on the host."*
- `host-processes` requires `ps`, which its own manifest comment admits is a POSIX staple and a
  formality.

**The broker's use is a live bug — and the sweep found my first description of it was wrong in a way
that makes it worse, not better.** yolo-jail exists to run agents *inside jails*, and agent CLIs
install lazily in the jail (`~/.yolo-launchers/`). A user who only ever runs `claude` in a jail has
no host `claude`. I wrote that "the broker never activates". What actually happens is **both halves
fail, in opposite directions**:

- **The daemon runs regardless.** `run.go:392-398` calls `brokerEnsure()` and `ensureBrokerRelay()`
  on every launch with **no loophole lookup at all**, and `broker.BrokerSpawn`
  (`brokerlifecycle.go:272-306`) contains no enablement check of any kind. So the host singleton and
  one relay per jail run for *everybody* — including a user with `packs: []` who has never heard of
  claude.
- **The jail is not wired to it.** What `requires` actually gates is `brokerLoopholeActive`
  (`assemble_parts.go:408-435`), which decides the endpoint env var, the CA mount and the in-jail
  terminator. Without host `claude`, none of those land, so the jail's own claude refreshes directly
  against Anthropic — unserialized, which is exactly the concurrent single-use-refresh-token race the
  broker exists to prevent ([`agent-credentials.md`](agent-credentials.md) §2.5).

So the user gets a host daemon they never asked for *and* no protection from it. It works on the
maintainer's machine because that host has claude installed. A predicate that is true for the author
and false for the product's core use case is the worst shape a default can have — and **R1 already
has a counterexample sitting in the run pipeline**, which R6 does not by itself remove (OQ-A11).

**The dependency it was approximating is structural, not observational.** "Is there anything to
refresh for" is really "is the claude pack selected" — which the pack system can express directly,
and which no `PATH` lookup can answer correctly.

### 1.2 The three things this doc first ignored — raised in review, and one is a real hole

*This section exists because the first draft scoped itself to manifest-declared loopholes, and the
reviewer asked the obvious question: don't you already have to enable `yolo-ps` somehow? And what
about journalctl? Both answers change the picture.*

**(a) `yolo-ps` already has a per-workspace gate, and it is not `enabled`.** `host_processes.visible`
is an allowlist of process names read from the workspace config, and it defaults to **empty** —
`LoadConfig`'s own comment calls that *"feature effectively disabled"*
(`internal/hostprocesses/hostprocesses.go:30-33`), and the daemon's self-check reports
*"config … has no `host_processes.visible` entries"* (`selfcheck.go:35`). So today the loophole
activates automatically but the capability is **empty until the workspace opts in**, name by name.

That is R5's shape — user-scope install, workspace-scope opt-in — invented ad hoc for one loophole
before the general rule existed. Two consequences:

- **The upgrade population is much smaller and far better identified than §4 said.** `yolo-ps` only
  "goes dark" for someone who *already* wrote a non-empty `host_processes.visible`. That is also a
  precise trigger for OQ-A2's notice: a workspace with entries but no selected pack is exactly the
  case worth printing a line for, and it costs nothing to detect.
- **After the ruling there are three gates for one feature** — select the pack, enable the loophole,
  list the processes.

  **RULED (OQ-A5, 2026-08-18): keep all three, and do nothing clever.** They answer different
  questions — is it installed, is it running, what may it show — and collapsing them would mean a
  non-empty `visible` list **silently starting a host daemon**, which is the presence-activation this
  whole document deletes, wearing a different hat. The ceremony is the price of the rule being true.

  Note the count is smaller than it looks: the third gate is the **status quo** (it predates this
  design), so the rulings add **two** steps, not three. Where the cost should be softened is the
  *message*, not the mechanism.

**(b) The journal bridge is already opt-in, and it is the precedent.** It starts only when the
top-level `journal` key says so (`internal/cli/run/loopholesruntime.go:89-90, 108`). So R1 is not a
new idea in this codebase — it is one service's local practice, and this doc generalizes it.

**(c) The cgroup delegate is presence-activated, and nothing gates it.** It starts whenever the
platform allows — *"Linux only, cgroup v2 only"* (`loopholesruntime.go:104-107`), no config key at
all. That is precisely the shape R1 deletes, in a host-side daemon, and the first draft did not
mention it.

**RULED (OQ-A4, 2026-08-18): it becomes opt-in, same as everything else.** No exception. The
delegate hands a jail control of **its own** cgroup rather than reading host state, so R4's "we don't
give host access by default" argument is genuinely weaker here — but weaker is not absent, and R1 is
about the *mechanism*, not the severity. The moment one builtin stays presence-activated, "presence
never activates" stops being a rule anyone can rely on while reading the code. The practical
consequence to accept: `yolo-cglimit` no longer works out of the box.

### 1.3 Everything that reaches your host, and how it turns on

*Added on review — "this is getting to be a lot" is the correct reaction, and the reason is that
five things reach the host through four different channels with four different switches. The table
is the argument for unifying them.*

| | channel | on today because… | its config key | after the rulings |
|---|---|---|---|---|
| **broker daemon + relay** | *not gated at all* | `run.go` spawned the singleton every launch, no lookup | *none* | ✅ **DONE 2026-08-18** — gated on the loophole record (OQ-A11), launch path and attach path both |
| **broker jail wiring** | bundled loophole | manifest `enabled: true` **and** host `claude` on PATH | `loopholes.claude-oauth-broker.enabled` | inside `packs/claude`, `default_enabled: true` — 🛑 **blocked**, see [`broker-as-a-pack.md`](broker-as-a-pack.md) §6.1 |
| **host-processes** | bundled loophole | manifest `enabled: true` **and** host `ps` | `loopholes.host-processes.enabled` **plus** top-level `host_processes.visible` | ✅ **DONE 2026-08-18** — own pack, `default_enabled: false`, and the top-level key is now REFUSED |
| **audio** | bundled loophole *and* an official pack beside it | manifest `enabled: true` **and** the pulse socket exists | `loopholes.audio.enabled` | ✅ **DONE 2026-08-18** — own pack, `default_enabled: false`; the two merged under the plain name and the `requires` probe became `platforms: ["linux"]` |
| **journal** | **builtin service**, hardcoded in the run pipeline | the top-level `journal` key says so | top-level `journal` | ✅ **DONE 2026-08-18** — own pack, `default_enabled: false`; the top-level key is REFUSED and the mode is the typed `full` setting, `scope: "user"` (OQ-K4) |
| **cgroup-delegate** | **builtin service**, hardcoded | Linux + cgroup v2. No key exists. | *none* | ✅ **DONE 2026-08-18** — own pack, `default_enabled: false`, gated on `Honored` (its record is a pack's, so the origin gate is live). `yolo-cglimit` is opt-in, as ruled |
| **host nix daemon** | mounted by the run pipeline | the socket exists on the host | *none* | **stays ungated** (OQ-A11) — image infrastructure, not a capability a jail reaches for; gating it is a `--no-nix` feature |
| a user's own | `loopholes:` config block | `enabled` defaults true | `loopholes.<name>.*` | unchanged |

Read down the "on today because…" column and the diagnosis writes itself: **no two of these turn on
the same way**, and only one of them was ever a decision the user made deliberately.

**RULED (OQ-A11, 2026-08-18): gate the broker daemons on the loophole record; leave nix ungated, and
say why.** *(BUILT 2026-08-18, ahead of the move it was meant to precede — it is independent of every
other step, and each launch it went unbuilt was a host daemon nobody asked for. `brokerEnsure` and
`ensureBrokerRelay` now sit behind `brokerLoopholeActive(cfg)`, the same predicate
`hostServicesMountArgs` already consulted, on the launch path and the attach path both. The
services DIRECTORY is deliberately outside the gate: it is not the broker's, and every loophole's
endpoint file lands in it.)* The broker is squarely in scope — it is the loophole R6 is already moving, and the fix is
to route `brokerEnsure` through the same record everything else consults, rather than calling it from
the run pipeline with only an `rt != "container"` guard. Without that, R6 makes things *worse* before
better: after the move, a jail that does not select the claude pack has no broker in any surface
(`loopholes list` will not name it, the briefing will not mention it) while yolo keeps spawning the
singleton on that host at every launch. **A daemon none of yolo's own surfaces name is worse than one
that is merely on.**

Nix is a different animal and stays as it is: it is infrastructure the *image* depends on rather than
a capability a jail reaches for, so gating it is a `--no-nix`-shaped feature, not an activation
ruling. **The row stays in the table either way** — the table's credibility is the argument, and an
inventory that quietly omits the crossing it cannot justify is worth less than one that names it.

*Two rows were added by the completeness sweep (§6) and both matter. The broker splits in half — the
daemon is ungated, only the jail wiring is — and the **host nix daemon socket** is mounted into jails
because it exists, with no key anywhere. A writable socket to the host's nix-daemon builds and
realises store paths on the host's behalf, which is a strictly larger crossing than `audio`'s pulse
socket. This table is the doc's central argument, so a reader finding a sixth row five minutes later
would sink it.*

### 1.4 The finding that undercuts the conversion — core hardcodes two loopholes by name

Chasing "how would a workspace enable this?" turns up something worse than a missing feature.

**Two loopholes have their own top-level keys in yolo's config schema.** `config.go:59` lists
`"loopholes", "host_processes", "journal"` together; `validate.go:557-570` validates
`host_processes.visible` against `knownHostProcessesKeys`; `inherit.go:116-121` classifies both as
*"RESERVED loophole names carried as their own top-level keys"*. So core's config schema names two
specific loopholes.

> [!NOTE]
> **FIXED 2026-08-18, both of them, and the second one is what made it mean something.** Each key is
> now a targeted REFUSAL naming its replacement (`validateHostProcessesRetired`,
> `validateJournalRetired`), both are classified into NEITHER inherit scope so no generated inner
> config can carry a key this build refuses, `knownHostProcessesKeys` and `journalModes` are gone
> with their validators, and the `cgroup-delegate` name-refusal in `validate_loopholes.go` went too
> — it would have made the delegate's own switch unwritable. Pinned as a PROPERTY rather than as
> two absences (`TestCoresSchemaNamesNoLoopholeInEitherInheritScope`), so a third loophole name
> creeping into core's schema fails even though nobody thought to name it in a test.

**That is exactly the residue [`pack-code-separation.md`](pack-code-separation.md) exists to
delete**, recurring one layer down: core does not know what an *agent* is any more, but it very much
knows what `host_processes` is. Converting the loophole to a pack while leaving `host_processes` in
`knownTopLevelConfigKeys` would move the manifest out of core and leave core's schema naming it —
the *appearance* of the separation with none of the substance.

**And no pack can declare a config key.** Of the fifteen contribution kinds (`packdecl/kinds.go`),
`config` and `config-overlay` write **agent** config files inside the jail — `settings.json` and its
kin. Nothing writes into yolo's own schema. So a pack-shipped loophole that needs settings has
nowhere to put them.

**RULED (OQ-A8, 2026-08-17): a pack declares its config keys, typed.** 📄
[`pack-config-keys.md`](pack-config-keys.md) is the design — a loophole's settings are declared in
its own manifest with types and a per-key `scope`, supplied by the user under
`loopholes.<name>.settings`, validated through the resolver core already injects, and delivered
through a **file core writes** rather than an env channel the workspace controls. Four questions live
there (OQ-K1..K4); none of them blocks this document.

> [!WARNING]
> **The obvious cheap answer — an opaque `settings` map — is a trust regression, and it was my
> leaning until it was priced.** If core validates only *"it is an object"*, it cannot tell
> `settings.visible` from `settings.ld_preload`. That launders the user-scope-only refusal that
> exists to keep `LD_PRELOAD` out of a host daemon's spawn: a workspace file an agent can edit would
> reach a host process's environment. **Do not re-propose the untyped map.**
>
> A second thing that made it look free and is not: core never tells the host-processes daemon
> anything. `host_processes.visible` works *only* because **the daemon opens the workspace file
> itself, per request**. Making it a core-delivered setting means either serializing into the spawn —
> ending a per-request re-read that package treats as a frozen contract — or teaching the daemon
> config merging. Either is real work, not a rename.

Two alternatives were considered and rejected: **leaving `host_processes` in core** (honest about the
coupling, but it *is* the residue, and the next loophole that wants settings hits the same wall), and
**a sixteenth contribution kind** for arbitrary config keys (the most general answer and by far the
most machinery, justified only if a pack ever needs a key *outside* its own loophole's namespace —
nothing does).

> [!NOTE]
> **Cost still owed either way:** this makes `host_processes.visible` a **deprecated alias**, so it
> needs a migration and a removal date rather than a rename.

---

## 2. The rulings

**R1. Presence never activates.** A loophole is active only if something said so.

**R2. A pack declares its loophole's default state, and that declaration defaults to disabled.**
`default_enabled`, on the loophole manifest. Absent means off. This is what lets a pack "do the
right thing by default" without yolo guessing on its behalf.

> **RULED (OQ-A9, 2026-08-18): one key, renamed, governing all four manifest sources.**
> `default_enabled` **is** `enabled` with the default flipped — not a second key beside it. `enabled`
> becomes a **recognized-and-refused** key whose error names the rename, `SetEnabled` stops writing
> manifest files, and the four shipped manifests are updated in the same commit. Two booleans over
> one state would give the manifest, `loopholes list` and `SetEnabled` three ways to disagree.

> [!NOTE]
> **Half of this clause shipped differently, deliberately, and the doc is amended rather than the
> code (2026-08-18).** The ruling as written said `SetEnabled` *"is fixed to write **config** rather
> than a manifest file"*. The manifest-writing hazard is closed — nothing anywhere writes a manifest
> now — but the command does **not** write config either: `CmdSetEnabled`
> ([`loopholescmd.go#L315-L325`](../../internal/loopholes/loopholescmd.go#L315-L325)) prints the
> exact key to add, names the file, warns that the workspace scope is the weaker place to put it, and
> exits 1.
>
> The argument for stopping there is sound and worth keeping: **having `yolo loopholes enable`
> silently rewrite a user's own config file is a bigger behaviour than this ruling contemplated**, and
> it is a separate decision. The ledger records what shipped. If the write is wanted, it is a small
> follow-up rather than a correction.

> This settles the sweep's headline finding: the design introduced `default_enabled` onto a schema
> that already had a live `enabled` key with the opposite default, and never said which won.

> [!WARNING]
> **Reverse skew is the cost, and it needs a refusal rather than a tolerance.** An *older* yolo
> reading a *newer* manifest ignores `default_enabled` and falls back to enabled-defaults-**true** —
> so `audio` ships default-off and an older build runs it **on**. Deletion-shaped schema changes
> cannot rely on the unknown-key skew note, whose wording tells the reader a *newer* build knows the
> key: the exact opposite of the truth for a removed one (§4).

**R3. `requires.command_on_path` is deleted from the schema.** Not corrected — deleted. It is the
sniffing mechanism itself, and both of its uses are the argument against it: one is wrong for the
product's main case, the other is a formality on a POSIX staple. A loophole whose program is missing
should fail loudly at spawn, not vanish silently from a list.

**R4. Host access is never on by default.** `audio` ships `default_enabled: false`. Being useful is
not a reason to be automatic.

> [!CAUTION]
> **R5 is FALSE for list-shaped settings, established 2026-08-17.** `MergeConfig` union-merges every
> list at every depth (`load.go:63-118`), and the replace-wholesale exception was **deleted** — so a
> user-scope *ceiling* that a workspace *narrows* is inexpressible, and a workspace can only **widen**.
> For an allowlist like `host_processes.visible` that inverts the intended property: the weak,
> agent-writable scope can only add capability. The claim below holds for a scalar switch and not for
> a list. See [`pack-config-keys.md`](./pack-config-keys.md) §3, whose per-key `scope` field is the
> answer.

**R5. Install is user-scope; enable is either scope.** Already true and kept: `packs` is read from
the user config only (`internal/config/packs.go`), install-shaped keys are refused in workspace
scope, and `loopholes.<name>.enabled` is honored from both. So a workspace may switch on only what
the user already installed — the weak, agent-editable scope is bounded by the strong one, which is
what makes per-workspace enablement safe to offer at all.

> **RULED (OQ-A13, 2026-08-18): R5 stands for the ON direction, and the disclosure is mirrored.**
> R5 was written when `enabled: true` was **inert** — the manifest default was already true, so the
> only meaningful workspace power was turning things **off**. R2 inverts that and makes it the
> activation verb. R5 is not narrowed in response: restricting enablement to user scope would cost
> the per-workspace opt-in R5 exists for, which §1.2a shows `yolo-ps` already depends on.
>
> Instead the existing disclosure becomes **symmetric**. `WorkspaceDisabledLoopholes`
> (`validate_loopholes.go`) already computes exactly this and discards the `true` case; feeding it to
> the two surfaces an OFF already reaches — the launch line and `yolo check`'s
> warning-instead-of-green — points existing machinery at the new dangerous direction and keeps one
> vocabulary rather than inventing a second.
>
> **BUILT 2026-08-18.** The seam was WIDENED rather than twinned: it is now
> `WorkspaceLoopholeSwitches`, returning `{File, Enabled}` so one function answers "what did
> workspace scope say about this switch" in both directions. Absence still means *"workspace scope
> said nothing"* — a zero value would read as a disable nobody wrote. Two details the ruling did not
> settle and the implementation had to:
>
> - **The `yolo check` row discloses and then falls THROUGH**, where the OFF row stops. Off means
>   there is nothing left to measure; on means the loophole is about to run, and its `doctor_cmd` is
>   the next thing a reader wants. Stopping there would undo OQ-A12 for exactly the activations
>   nobody expected. What the row replaces is the greenest line in the section — `[PASS] loophole X:
>   disabled`, read off the manifest default (that walk resolves no config) with the file that
>   overrode it named nowhere.
> - **A workspace file that merely RESTATES the manifest default is disclosed too.** The launch
>   surface *cannot* suppress that case — its `LoopholeInfo` is `Name` + `HasHostDaemon`, with no
>   default in it — and two disclosures contradicting each other over one file is worse than one
>   redundant line. An explicit `enabled` in an agent-editable file is a deliberate act either way,
>   which is what keeps this off ordinary launches.

> [!WARNING]
> **Written when this was readability only; both blockers have since landed.** The config-approval
> diff renders a workspace `enabled: true` and prompts for it, so disclosure was never absent. What
> it lacked was integrity: the baseline it compared against lived at
> `<workspace>/.yolo/config-snapshot.json` under an rw bind mount, so whatever wrote the key could
> rewrite the baseline — and a non-TTY launch **auto-accepted** it.
>
> **A disclosure an agent can suppress is not a control.** Both fixes shipped 2026-08-18: **OQ-D1**
> moved the approval record host-side to `~/.local/share/yolo-jail/approvals/<container-name>.json`,
> which the jail never mounts, and **OQ-D2** made a non-interactive launch with a changed config a
> refusal that CI opts out of with `--accept-config-changes`. See
> 📄 [`config-safety.md`](config-safety.md). The mirrored line in this section is therefore now
> backed by a control, not just by a human happening to be watching.

**R6. The broker's loophole moves inside `packs/claude/`.** It exists only to serve claude, so
selecting the claude pack is the dependency — and R3's deletion is then free rather than a
regression, because the sniff was standing in for exactly this.

> **RULED (OQ-A10, 2026-08-18): a contribution of `packs/claude`, not a pack of its own.**
> [`broker-as-a-pack.md`](broker-as-a-pack.md) §6 designs a separate `packs/claude-oauth-broker/`;
> that is now **wrong and gets corrected there** rather than left as a second answer in a sibling
> doc. R6's whole argument is that the dependency is structural, and a separate pack reinstates the
> second selection step R6 deletes. A Bedrock user's escape is `supersedes` on the
> `claude-oauth-refresh` capability — already built, already declared — not deselection.

> [!WARNING]
> **Deleting `bundled_loopholes/claude-oauth-broker/` does NOT free the name, and getting this wrong
> breaks every launch for every claude user.** The reserved name is appended **unconditionally**
> (`discover.go:322-325`) — it is *not* derived from the bundled directory — and a pack claiming a
> reserved name fails the **whole launch** (`packs.go:310-311`). So the first commit adding the
> loophole contribution must also retire the reservation **and** the name special-case at
> `loopholesruntime.go:211-214`, in the same change. `audio` escaped this by renaming itself
> `audio-alsa`; the broker cannot, because `loopholes.claude-oauth-broker.enabled` is a user-visible
> config key.

---

## 3. What this does NOT license

- **Not** a second gate over host execution. `default_enabled` feeds `Enabled`; a fetched pack's
  host crossing still needs `Active` and `Honored`, so declaring yourself default-on cannot buy
  host access without the origin gate's approval. Adding an origin restriction *specifically* to
  `default_enabled` would be the halfway-measure shape [OQ-LP14 was criticized for](loophole-packaging-overview.md).

  **RULED (OQ-A3, 2026-08-16): `default_enabled: true` stays available to fetched packs,
  unrestricted.** *"A pack I fetched can declare itself on"* is a sentence worth reading twice, and
  it survives the reading: what a fetched pack may **do** is already decided by the origin gate at
  `Honored`, and a declaration about a default cannot widen it. The practical bound is the real
  reassurance — a fetched pack cannot be **selected** without a user editing their user-scope config,
  so declaring yourself default-on changes nothing until someone installs you deliberately.
- **Not** a change to `requires.file_exists`, which stays. It answers "can this machine run it",
  which is a real question — `audio` uses it — and it does not decide activation on its own.
- **Not** pack-level dependencies. R6 avoids needing them; nothing here introduces a pack that
  depends on another pack.
- **Not** a change to the three-layer model. `Enabled`/`Active`/`Honored` are right; only what feeds
  `Enabled` changes.
- **Not** a licence to "fix" `inJailActive`. Inside a jail, `requires` is answered by whether the
  bind mount landed (`loopholes.go:188-198`) — presence deciding activation, deliberately, because
  **the mount IS the host's decision made visible**. Someone implementing R1 by grepping for
  presence checks will read this as a violation and break every jail-side `Active()` evaluation.
- **Not** a change to per-mount presence skipping. A declared bind mount or device whose host path
  is absent is skipped with a warning (`runtime.go:214-236`). That is adaptation *inside* a
  capability the user already consented to, and it is warned rather than silent — the same
  reasoning that keeps `requires.file_exists`.

---

## 4. What it costs

**Every currently-active loophole goes dark on upgrade** unless its pack declares
`default_enabled: true` or the user enables it.

**RULED (OQ-A2, 2026-08-17): going dark is fine — build no migration machinery.** *"Even if packs
ship built in, the user still needs to list them in their user config to get them, just like agents.
No special case here."* A loophole you never listed behaving exactly like an agent pack you never
listed is the rule working, and inventing an upgrade notice for it would carve out the special case
this document exists to delete. The general "no packs configured" guidance already covers a user who
wonders where something went.

> [!NOTE]
> **The alternative, and why it is backwards.** A migration could write the currently-active set into
> user config as explicit `enabled: true` entries. That makes the ruling a **no-op for precisely the
> people who already have host daemons running** — the population it most exists to inform.
>
> Scope, for the record: "goes dark" means exactly **two** loopholes, `yolo-ps` and `audio`. §1.2a
> narrows even that — `yolo-ps` was already inert for anyone who had not written
> `host_processes.visible`, so the genuinely affected population is users with a non-empty `visible`
> list.

**The broker does NOT gain a way to be silently off** — settled by OQ-A1: it ships
`default_enabled: true` inside `packs/claude`, so selecting the claude pack is what turns it on, and
the only way to end up without it is to not be running claude. That is strictly better than the
status quo, where a jail-only user is silently unprotected (§1.1). Deleting `requires` therefore
costs no warning surface here: there is nothing left to warn about.

**`Active` gets thinner.** With `command_on_path` deleted, `requires` is just `file_exists`. That is
a simplification, not a loss — but the "loophole silently inactive" reports it used to produce were
at least *diagnosable*, and a missing program now surfaces as a daemon that fails to spawn. Worth
checking that failure reads well before shipping.

**RULED (OQ-A12, 2026-08-18): `yolo check` learns to read pack-shipped loopholes in this sprint.**
Not after it. The health section reads only the non-pack sources, which costs nothing today because
the only pack-shipped loophole (`audio-alsa`) has no `doctor_cmd` — but **this sprint moves the only
two loopholes that have one**, the broker and `host-processes`. Landing the conversion without the
fix means that on the day it ships, `yolo check` prints a cheerful "no loopholes installed" while the
broker's cert freshness, liveness and self-check go unreported.

That compounds the diagnosability cost in the paragraph above, on exactly the command a user reaches
for when a loophole is silently off. It is also the honest completion of
[`pack-code-separation.md`](pack-code-separation.md)'s doctor ruling: `check` should read loophole
health through the manifest surface rather than hand-rolled Go.

**And R3 is a silent WIDENING for manifests we do not ship — priced wrong above.** The sweep's
sharpest small finding: deleting a key that *grants* is not symmetric with deleting one that
*restricts*. A hand-placed or third-party manifest that wrote
`requires: {command_on_path: "acme-agent"}` meaning *"only run my host daemon when acme is
installed"* loses the condition on upgrade and **spawns everywhere** — announced, if at all, by an
unknown-key skew note whose wording tells the reader a *newer* build knows the key, which is the
exact opposite of the truth for a removed one. R3 needs a refusal that names the removal, not a
tolerance that shrugs at it.

---

## 5. The structural questions this opened

*Three questions from review that are bigger than the rulings and should not be answered inside
them. Each gets an OQ; this section is the context they share.*

**RULED (OQ-A6, 2026-08-18): they become manifest loopholes, and it happens IN this sprint.**
*(BUILT 2026-08-18. `packs/journal` and `packs/cgroup-delegate` ship, `paths.BuiltinLoopholeNames`
is deleted, the spawn loop's builtin-name skip is deleted, and core's config schema names no
loophole. Three things the ruling did not anticipate and the implementation had to settle are
recorded at the end of this section.)*
*"Make them manifests, and do it as part of this work."* My leaning was to file it and convert them
afterwards, on the grounds that the sprint was already carrying a preamble, a pack conversion, a
schema change and a deletion. **Overruled on scope, and the reason is sound**: the unification is the
point of the sprint, and a channel emptied of everything except the two things yolo happens to have
compiled in has not been emptied — it has been renamed. Deferring the conversion would leave §1.3's
table with two rows that still answer "why is it on?" differently from every other row.

Three consequences to carry, since this is now in scope rather than filed:

- **`journal`'s top-level config key goes.** §1.4 named it as one of the two loopholes core's schema
  hardcodes; converting it to a manifest is what removes the second name. Its settings move to the
  typed manifest-declared keys OQ-A8 rules — which is what makes this conversion possible at all, and
  is why the two questions could not have been sequenced the other way round.
- **`cgroup-delegate` gets a `default_enabled`**, and per OQ-A4 that value is **false**. The two
  rulings agree rather than merely coexisting: A4 says it stops starting itself, A6 says the switch
  it now needs lives in a manifest like every other.
- **The sprint's honest size grows.** Recorded rather than argued: the reason I wanted this deferred
  does not disappear because the ruling went the other way, and a sprint that silently absorbs a
  fifth workstream is how the other four slip.

> [!NOTE]
> **Three things the build had to settle that the ruling did not name** (2026-08-18). None
> reopens it; each is the kind of question only implementing it asks.
>
> - **The mode is a BOOLEAN, not the old `off | user | full` string.** The settings type set is
>   closed and has no `enum`, so a `string` mode is unvalidatable by core — while `ParseRequest`
>   narrows on the exact literal `"user"`, meaning *every other spelling behaves as full*. A
>   config typo that silently widens host access is the shape this sprint deletes, so the
>   declared key is `full: bool` and `off` is `enabled: false`. The three-valued vocabulary was
>   two questions wearing one key.
> - **The delegate's gate is `Honored`, not `Active` — and it is SHADOWABLE.** `brokerLoopholeActive`
>   may stop at `Active()` because the broker's record is bundled under a reserved name; retiring
>   this reservation means a pack a user installs can ship a `cgroup-delegate` loophole and turn
>   yolo's own in-process delegate on. That is not a new hole — it is exactly what **OQ-A3**
>   already admits (*"a fetched pack can declare itself on"*, bounded by the origin gate rather
>   than by the declaration) — but it is a property the broker deliberately does **not** have, and
>   the difference is the reservation. Worth knowing before OQ-A10 retires the broker's.
> - **The retired config keys are not symmetric.** `host_processes` CONFIGURED a daemon;
>   `journal` TURNED ONE ON. A silently-ignored `journal: "full"` leaves an agent that cannot read
>   the host's logs with no thread back to the key, which is why both are refusals and why the
>   `journal` message has to carry three instructions (select, enable, and — for `full` only —
>   write the setting **in the user config**).

*The original framing, kept because it is the argument for the ruling:*

**Is "builtin service" a channel worth keeping?** `journal` and `cgroup-delegate` are not loopholes
in the manifest sense at all — they are Go functions called from the run pipeline
(`loopholesruntime.go:104-112`), with reserved names in `paths.go` and bespoke switches. Everything
this doc argues for — one activation model, one place to read what reaches your host, one gate — is
easier if they are manifests like everything else. Against: they are yolo's own code with no
distribution problem to solve, and a manifest for something that is compiled in anyway is ceremony.
**The asymmetry is the real evidence**: one of the two is opt-in and the other cannot be turned off,
and nobody decided that — it is just where each landed.

**Are official packs installed by default?** Terms first, because the question hides an ambiguity:
an official pack is **embedded in the binary** (always present — `packs/embed.go` carries seven) but
never **selected** (`packs.go:69`: *"a bare `packs: ["claude"]` entry selects one; nothing is on by
default"*). So "installed" is already free; what costs a line is selection. Which makes the sharper
question: **does a loophole-only pack need selecting at all?** For `claude`, selection means
something — install this agent. For `host-processes`, whose entire content is one loophole,
selection and enablement are the same intent expressed twice, which is the ceremony OQ-A5 names.

**RULED (OQ-A7, 2026-08-17): it needs selecting. No special case.** *"Even if packs ship built in,
the user still needs to list them in their user config to get them, just like agents."* My leaning
was to let an embedded loophole-only pack be reachable by `enabled` alone, to save a line of
ceremony. Overruled, and rightly: **"shipped in the binary" is not "installed"**, and a rule with one
exception is two rules. `host-processes` is listed in `packs` like anything else, and then enabled.

> [!NOTE]
> The rejected third option is worth naming because it is the tempting one: **make all embedded packs
> default-selected** and rely on enablement as the only gate. It contradicts `AGENTS.md`'s *"nothing
> is active by default"* for every other kind a pack can ship — a pack's skills, config and briefing
> would land because it happened to be compiled in, which is presence-activation moved from the
> daemon to the surfaces.

**Where do a loophole's own settings live?** Settled — see [§1.4](#14-the-finding-that-undercuts-the-conversion--core-hardcodes-two-loopholes-by-name)
and 📄 [`pack-config-keys.md`](pack-config-keys.md) (**OQ-A8**).

---

## 6. What the completeness sweep found

Six angles swept the codebase for what this design had not considered — other host-reaching
surfaces, every other presence-activated behaviour, the agent-facing surfaces, prior rulings that
might contradict these, config/migration, and an adversarial read. Verdict: **substantially complete
on its own terms, with the gap concentrated in one place** — R2 introduces `default_enabled` onto a
schema that already has a live `enabled` key with the opposite default, and never says which wins.

It corrected three of this doc's own claims (§1's table, §1.1's mechanism, §1.3's inventory) and
raised five questions, OQ-A9 through OQ-A13.

**Refuted on inspection — recorded so they are not re-raised.** Each of these sounded like a problem
and is not:

- **"R5+R6 give the broker a silent off switch."** The disable path is real but **not silent**:
  `validate_loopholes.go:258-261` discloses it at launch by name and file, and
  `sections_loopholes.go:39-45` turns the same condition into a `yolo check` **warning that never
  passes green**.
- **"`default_enabled: true` lets a fetched pack buy host access."** It cannot. `Honored()` applies
  the origin gate independently of `Active()`, and `moduleClaims` enumerates every host crossing for
  the install prompt. §3's defence holds and OQ-A3's answer stands. What remains is a question about
  the *moment of consent*, which is OQ-A13 — not about access.
- **"`inJailActive` violates R1."** By construction and correctly — see §3.
- **"`yolo check` reports a fully-dark jail as all-green."** Each line is individually true, and a
  fresh install having nothing enabled *is* the ruling working. The real defect underneath is
  OQ-A12.
- **"The pack-shipped subset refuses R4/R6 and nobody priced it."** Priced already:
  [`broker-as-a-pack.md`](broker-as-a-pack.md) §11 names `publishes: "socket"` as the common blocker
  for all three conversions.

**Worth knowing, changing no decision:** `yolo loopholes enable` works for **zero** loopholes — it
used to stat the user-loopholes dir and write into a *manifest file*, and since OQ-LP10 retired that
dir it writes nothing at all and prints the `loopholes.<name>.enabled` config key instead · `yolo loopholes status` and `yolo check` disagree about whether a disabled loophole's
`doctor_cmd` runs · the briefing has **no zero-state**: it is built from `Honored()` and two built-in
skills point at `yolo loopholes list` unconditionally, so a fully-dark jail tells the agent nothing
while its skills still promise the feature · disabling the broker degrades to *shared-and-
unserialized* credentials, not to per-jail ones, because the shared dir is a separate `state`
contribution.

---
