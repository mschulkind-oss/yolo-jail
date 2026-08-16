# Nothing reaches your host because it happened to be there — loophole activation

**Status:** RULED 2026-08-15, nothing built. Six rulings; **eight open questions**, three answered in
review the same day and five raised BY it. The doc grew that way on purpose — every question below
came from asking "what else reaches the host, and why is it on?", and the answer kept being
"something different each time". Taken during the `host-processes` conversion; this doc records them and works
out what they cost.

**The short version.** A loophole is active today because it was *present* and something it named
happened to exist on the host — bundled, plus a `requires` predicate that sniffs `PATH`. That is
being replaced end to end: **presence stops implying activation.** A pack declares a loophole's
default state, that declaration defaults to **disabled**, config overrides it at either scope, and
the `requires.command_on_path` sniff is **deleted** rather than fixed — it is the mechanism, not a
bug in the mechanism. The principle behind it, in the maintainer's words: *"we don't give host
access by default."*

**The most important section is §1.3** — the six-row table of everything that reaches your host and
why it is currently on. No two rows agree, and that is the whole argument. §1.4 is the finding that
should worry you most: core's config schema names two loopholes by hand.

**Reads with:** [`broker-as-a-pack.md`](broker-as-a-pack.md) (the sprint this came out of; §5.5 is
the connection preamble, §12 the `host-processes` conversion),
[`loophole-packaging-overview.md`](loophole-packaging-overview.md) (§5 "Defaults, and what stays
bundled" — this supersedes its activation story),
[`gate-placement-principle.md`](gate-placement-principle.md) (why a second gate over the same act is
worse than none).

---

## 1. What activates a loophole today

Three layers already exist and are well-named (`internal/loopholes/discover.go:643-680`):

| | today | meaning |
|---|---|---|
| `Enabled` | defaults **true** from the manifest; config may override | *"the user's switch"* |
| `Active` | `Enabled` **and** `requires`/`platforms` are satisfied | *"the machine can run it"* |
| `Honored` | `Active` **and** the origin gate admits it | *"the pack it came from may touch the host"* |

The layering is right. What is wrong is what feeds the first one: `enabled` defaults to **true** in
the manifest (`discover.go:50`), and every bundled manifest sets it. So a loophole is on the moment
it is *present*, and presence was never a decision the user made.

### 1.1 The sniff, and the bug it is causing right now

`requires.command_on_path` is `exec.LookPath` **on the host** (`internal/loopholes/loopholes.go:176`).
Two manifests use it:

- `claude-oauth-broker` requires `claude` on the host's PATH, meaning *"only run the broker if Claude
  Code is installed on the host."*
- `host-processes` requires `ps`, which its own manifest comment admits is a POSIX staple and a
  formality.

**The broker's use is a live bug.** yolo-jail exists to run agents *inside jails*, and agent CLIs
install lazily in the jail (`~/.yolo-launchers/`). A user who only ever runs `claude` in a jail has
no host `claude`, so **the broker never activates** — silently — and that user gets exactly the
concurrent single-use-refresh-token race the broker exists to prevent
([`agent-credentials.md`](agent-credentials.md) §2.5). It works on the maintainer's machine because
that host has claude installed. A predicate that is true for the author and false for the product's
core use case is the worst shape a default can have.

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
  list the processes. Two of them are genuinely different questions (is the daemon running vs. what
  may it show) but the ceremony is real, and it is worth deciding deliberately rather than
  discovering. See OQ-A5.

**(b) The journal bridge is already opt-in, and it is the precedent.** It starts only when the
top-level `journal` key says so (`internal/cli/run/loopholesruntime.go:89-90, 108`). So R1 is not a
new idea in this codebase — it is one service's local practice, and this doc generalizes it.

**(c) The cgroup delegate is presence-activated, and nothing gates it.** It starts whenever the
platform allows — *"Linux only, cgroup v2 only"* (`loopholesruntime.go:104-107`), no config key at
all. That is precisely the shape R1 deletes, in a host-side daemon, and the first draft did not
mention it. Whether it should be exempt is OQ-A4; that it needs an explicit answer is not in doubt.

### 1.3 Everything that reaches your host, and how it turns on

*Added on review — "this is getting to be a lot" is the correct reaction, and the reason is that
five things reach the host through four different channels with four different switches. The table
is the argument for unifying them.*

| | channel | on today because… | its config key | after the rulings |
|---|---|---|---|---|
| **broker** | bundled loophole | manifest `enabled: true` **and** host `claude` on PATH | `loopholes.claude-oauth-broker.enabled` | inside `packs/claude`, `default_enabled: true` |
| **host-processes** | bundled loophole | manifest `enabled: true` **and** host `ps` | `loopholes.host-processes.enabled` **plus** top-level `host_processes.visible` | own pack, `default_enabled: false` |
| **audio** | bundled loophole *and* an official pack beside it | manifest `enabled: true` **and** the pulse socket exists | `loopholes.audio.enabled` | own pack, `default_enabled: false` |
| **journal** | **builtin service**, hardcoded in the run pipeline | the top-level `journal` key says so | top-level `journal` | **undecided — OQ-A6** |
| **cgroup-delegate** | **builtin service**, hardcoded | Linux + cgroup v2. No key exists. | *none* | **undecided — OQ-A4** |
| a user's own | `loopholes:` config block | `enabled` defaults true | `loopholes.<name>.*` | unchanged |

Read down the "on today because…" column and the diagnosis writes itself: **no two of these turn on
the same way**, and only one of the five was ever a decision the user made deliberately.

### 1.4 The finding that undercuts the conversion — core hardcodes two loopholes by name

Chasing "how would a workspace enable this?" turns up something worse than a missing feature.

**Two loopholes have their own top-level keys in yolo's config schema.** `config.go:59` lists
`"loopholes", "host_processes", "journal"` together; `validate.go:557-570` validates
`host_processes.visible` against `knownHostProcessesKeys`; `inherit.go:116-121` classifies both as
*"RESERVED loophole names carried as their own top-level keys"*. So core's config schema names two
specific loopholes.

**That is exactly the residue [`pack-code-separation.md`](pack-code-separation.md) exists to
delete**, recurring one layer down: core does not know what an *agent* is any more, but it very much
knows what `host_processes` is. Converting the loophole to a pack while leaving `host_processes` in
`knownTopLevelConfigKeys` would move the manifest out of core and leave core's schema naming it —
the *appearance* of the separation with none of the substance.

**And no pack can declare a config key.** Of the fifteen contribution kinds (`packdecl/kinds.go`),
`config` and `config-overlay` write **agent** config files inside the jail — `settings.json` and its
kin. Nothing writes into yolo's own schema. So a pack-shipped loophole that needs settings has
nowhere to put them.

Three ways out, and this needs an answer before `host-processes` can honestly leave core (OQ-A8):

1. **Leave `host_processes` in core.** Honest about the coupling, but it is the residue, and the
   next loophole that wants settings faces the same wall.
2. **Widen the per-loophole config block.** Its key set is closed today at
   `{enabled, env, jail_env, command, doctor_cmd}`. Add an opaque `settings` map:
   `loopholes.host-processes.settings.visible: [...]`, passed to the daemon untouched. Core
   validates that it is an object and nothing more — it never learns what `visible` means.
3. **A sixteenth kind** that lets a pack declare config keys with a schema. The most general and by
   far the most machinery.

---

## 2. The rulings

**R1. Presence never activates.** A loophole is active only if something said so.

**R2. A pack declares its loophole's default state, and that declaration defaults to disabled.**
`default_enabled`, on the loophole manifest. Absent means off. This is what lets a pack "do the
right thing by default" without yolo guessing on its behalf.

**R3. `requires.command_on_path` is deleted from the schema.** Not corrected — deleted. It is the
sniffing mechanism itself, and both of its uses are the argument against it: one is wrong for the
product's main case, the other is a formality on a POSIX staple. A loophole whose program is missing
should fail loudly at spawn, not vanish silently from a list.

**R4. Host access is never on by default.** `audio` ships `default_enabled: false`. Being useful is
not a reason to be automatic.

**R5. Install is user-scope; enable is either scope.** Already true and kept: `packs` is read from
the user config only (`internal/config/packs.go`), install-shaped keys are refused in workspace
scope, and `loopholes.<name>.enabled` is honored from both. So a workspace may switch on only what
the user already installed — the weak, agent-editable scope is bounded by the strong one, which is
what makes per-workspace enablement safe to offer at all.

**R6. The broker's loophole moves inside `packs/claude/`.** It exists only to serve claude, so
selecting the claude pack is the dependency — and R3's deletion is then free rather than a
regression, because the sniff was standing in for exactly this.

---

## 3. What this does NOT license

- **Not** a second gate over host execution. `default_enabled` feeds `Enabled`; a fetched pack's
  host crossing still needs `Active` and `Honored`, so declaring yourself default-on cannot buy
  host access without the origin gate's approval. Adding an origin restriction *specifically* to
  `default_enabled` would be the halfway-measure shape [OQ-LP14 was criticized for](loophole-packaging-overview.md).
- **Not** a change to `requires.file_exists`, which stays. It answers "can this machine run it",
  which is a real question — `audio` uses it — and it does not decide activation on its own.
- **Not** pack-level dependencies. R6 avoids needing them; nothing here introduces a pack that
  depends on another pack.
- **Not** a change to the three-layer model. `Enabled`/`Active`/`Honored` are right; only what feeds
  `Enabled` changes.

---

## 4. What it costs

**Every currently-active loophole goes dark on upgrade** unless its pack declares
`default_enabled: true` or the user enables it. That is the ruling working as intended, but it is a
silent behaviour change for anyone already relying on `yolo-ps` or `audio`, and silence is the part
worth fixing: a one-time launch notice naming what *was* active and the exact line to restore it
costs little and turns a mystery into a decision. **Open — see OQ-A2.**

**The broker does NOT gain a way to be silently off** — settled by OQ-A1. It ships
`default_enabled: true` inside `packs/claude`, so selecting the claude pack is what turns it on, and
the only way to end up without it is to not be running claude. That is strictly better than the
status quo, where a jail-only user is silently unprotected (§1.1). Deleting `requires` therefore
costs no warning surface here: there is nothing left to warn about.

**`Active` gets thinner.** With `command_on_path` deleted, `requires` is just `file_exists`. That is
a simplification, not a loss — but the "loophole silently inactive" reports it used to produce were
at least *diagnosable*, and a missing program now surfaces as a daemon that fails to spawn. Worth
checking that failure reads well before shipping.

---

## 5. The structural questions this opened

*Three questions from review that are bigger than the rulings and should not be answered inside
them. Each gets an OQ; this section is the context they share.*

**Is "builtin service" a channel worth keeping?** `journal` and `cgroup-delegate` are not loopholes
in the manifest sense at all — they are Go functions called from the run pipeline
(`loopholesruntime.go:104-112`), with reserved names in `paths.go` and bespoke switches. Everything
this doc argues for — one activation model, one place to read what reaches your host, one gate — is
easier if they are manifests like everything else. Against: they are yolo's own code with no
distribution problem to solve, and a manifest for something that is compiled in anyway is ceremony.
**The asymmetry is the real evidence**: one of the two is opt-in and the other cannot be turned off,
and nobody decided that — it is just where each landed. **OQ-A6.**

**Are official packs installed by default?** Terms first, because the question hides an ambiguity:
an official pack is **embedded in the binary** (always present — `packs/embed.go` carries seven) but
never **selected** (`packs.go:69`: *"a bare `packs: ["claude"]` entry selects one; nothing is on by
default"*). So "installed" is already free; what costs a line is selection. Which makes the sharper
question: **does a loophole-only pack need selecting at all?** For `claude`, selection means
something — install this agent. For `host-processes`, whose entire content is one loophole,
selection and enablement are the same intent expressed twice, which is the ceremony OQ-A5 names.
**OQ-A7.**

**Where do a loophole's own settings live?** §1.4. `host_processes.visible` is the concrete case and
the answer decides whether the conversion actually separates anything. **OQ-A8.**

---

## Open Questions

1. **OQ-A1 — is the broker `default_enabled: true` inside the claude pack, or off like everything
   else?**

   R4 says host access is never on by default; the maintainer also observed that *"the oauth broker
   is host incidental only really — you could run the broker in a container too if you wanted"*,
   which says its host-ness is not its defining property. Those two pull in opposite directions and
   this is where they meet. What it decides: whether installing the claude pack gives you working
   token serialization, or whether that is a second step you can forget.

   _Leaning:_ **`default_enabled: true`, inside the claude pack.** The thing being switched on is
   not "host access" in any sense the user chose it for — it is *part of running claude correctly*,
   and its absence is not a missing feature but a silently corrupted credential shared across every
   jail. R4's principle is about not reaching onto a host the user never pointed us at; selecting
   the claude pack is pointing us at it. If that reads as an exception to R4, the honest framing is
   that R4 governs *reaching for a host resource* and this governs *doing the job the pack exists to
   do*.

   **Answer:**
   > **Enabled, inside the claude pack — that was the point of moving it there.** So R6 and OQ-A1
   > are one decision, not two: the broker stops being reachable by a `PATH` sniff and becomes part
   > of what the claude pack *is*. R4 is unbroken because nothing here reaches for a host resource
   > the user did not select — and this is the case that shows R4 was never about host-ness per se,
   > which is the same observation that says the broker could just as well run in a container.

2. **OQ-A2 — does the upgrade say anything, or is it a silent clean break?**

   *Rewritten after review: "everyone's loopholes go dark" was too abstract to act on. Concretely —*

   **Two independent things change, and it is worth separating them because only one is about packs.**

   - **Packs.** `host-processes` and `audio` become packs, and `packs` is read from the **user
     config only**, so they must be *installed* by name where today they are simply present in the
     binary. The broker does not move channels this way — it moves *inside* `packs/claude`, so
     anyone already running claude keeps it.
   - **Keys.** `loopholes.<name>.enabled` is unchanged: same key, same two scopes, same meaning.
     What changes is the **default when nobody writes it**. Today every manifest ships
     `"enabled": true` and `discover.go:50` defaults to true anyway, so a loophole is on unless you
     turn it off. After R2 the default is off unless the pack declares `default_enabled: true`.

   **So, per loophole:**

   | | today | after | user action needed |
   |---|---|---|---|
   | **broker** | on iff host `claude` is on PATH — *silently off for a jail-only user* (§1.1) | on whenever the claude pack is selected (OQ-A1) | **none**, and it starts working for people it was silently failing |
   | **`yolo-ps`** | daemon on always, but **shows nothing** until the workspace writes `host_processes.visible` (§1.2a) | install `host-processes` in user config, then enable it — `visible` unchanged | **two lines, and only for someone who already wrote `visible`** |
   | **audio** | on iff the pulse socket exists | install the audio pack, then enable it (R4) | **two lines** |

   So "goes dark" means exactly two things — `yolo-ps` and `audio` — and §1.2a narrows even that:
   `yolo-ps` was already inert for anyone who had not written `host_processes.visible`, so the
   affected population is *users with a non-empty `visible` list*, which is a condition yolo can
   detect exactly. That is the ruling working, not a bug; the question is only whether the moment is
   legible.

   _Leaning:_ **a one-time launch notice** naming what was active and the exact lines to restore it —
   and, for `host-processes` specifically, **trigger it on the detectable condition**: a workspace
   with a non-empty `host_processes.visible` and no selected pack is a user who demonstrably wanted
   this and will otherwise file a bug.
   The alternative worth naming is a migration that writes the currently-active set into user config
   as explicit `enabled: true` entries — but that makes the ruling a no-op for precisely the people
   who already have host daemons running, which is backwards. Silence is cheap for us and expensive
   for them, and the population is small enough that the notice can name the loopholes literally.

   **Answer:**
   > _(empty — fill in when decided)_

3. **OQ-A4 — does the cgroup delegate become opt-in too?**

   Raised in review (§1.2c). It is the one host-side daemon that still starts purely because the
   platform allows it — Linux + cgroup v2, no key. R1 says presence never activates, and it is
   presence-activated, so either it is an exception with a stated reason or it gets a gate.

   What it decides: whether `yolo-cglimit` keeps working out of the box. The delegate hands a jail
   control of **its own** cgroup rather than reading host state, so the R4 argument for `audio`
   ("we don't give host access by default") is genuinely weaker here — but "weaker" is not "absent",
   and R1 is about the *mechanism*, not the severity.

   *Overlaps OQ-A6, which asks the structural version of this — whether both builtins become
   manifests. A4 is the narrow question: whatever channel it lives in, does it keep starting itself?*

   _Leaning:_ **make it opt-in, same as everything else.** An exception costs more than the
   convenience: the moment one builtin is presence-activated, "presence never activates" stops being
   a rule anyone can rely on when reading the code. The natural gate already exists in spirit — a
   jail that never calls `yolo-cglimit` does not need the delegate, and a jail that does is one whose
   config already talks about `resources`.

   **Answer:**
   > _(empty — fill in when decided)_

4. **OQ-A5 — three gates for `yolo-ps`: is that the intended shape?**

   After the ruling, showing a host process takes: select the pack (user scope), enable the loophole
   (either scope), and list the process names (workspace scope). The first two are new; the third
   already existed (§1.2a).

   _Leaning:_ **keep all three, and do nothing clever.** They answer different questions — is it
   installed, is it running, what may it show — and collapsing them would mean a non-empty `visible`
   list silently starting a host daemon, which is the presence-activation this whole doc deletes,
   wearing a different hat. The ceremony is the price of the rule being true. Where it should be
   softened is the *message*, not the mechanism: OQ-A2's notice makes the two-step discoverable at
   exactly the moment someone hits it.

   **Answer:**
   > _(empty — fill in when decided)_

5. **OQ-A3 — does `default_enabled: true` stay available to *fetched* packs?**

   §3 argues no extra gate is needed, because the origin gate already stands between a fetched pack
   and the host. But "a pack I fetched can declare itself on" is a sentence worth reading twice
   before it is true.

   _Leaning:_ **yes, unrestricted.** The origin gate is the gate; a second rule keyed on the same
   act is the thing this repo keeps deleting. Note the practical bound: a fetched pack still cannot
   be *selected* without a user editing their user-scope config, so declaring yourself default-on
   changes nothing until someone installs you deliberately.

   **Answer:**
   > **Yes, unrestricted.** No origin test on `default_enabled`. What a fetched pack may *do* is
   > already decided by the origin gate at `Honored`; a declaration about a default cannot widen it.

6. **OQ-A6 — do `journal` and `cgroup-delegate` become manifest loopholes, or stay builtin with a
   uniform gate?**

   Raised in review: *"shouldn't cgroups and journalctl be similar?"* They are not, and the
   difference was never decided — `journal` is opt-in via its own top-level key, `cgroup-delegate`
   has no key at all (§1.3). Whatever else happens, those two should stop disagreeing.

   What it decides: whether "one activation model for everything that reaches your host" is literally
   true or true-with-two-exceptions. It also decides whether §1.4's `journal` top-level key survives.

   _Leaning:_ **make them manifests, but AFTER this sprint.** The unification is right — a reader
   should be able to answer "what can reach my host and why is it on" from one place — and being
   yolo's own code is not a reason to be a special case, since that argument is exactly what
   `bundled_loopholes/` is being emptied to refute. But converting them is not needed to empty the
   channel, and this sprint is already carrying a preamble, a pack conversion, a schema change and a
   deletion. File it, do not fold it in.

   **Answer:**
   > _(empty — fill in when decided)_

7. **OQ-A7 — does a loophole-only pack need selecting, or is enabling enough?**

   Official packs are embedded and always present; selection is a separate line
   (`packs.go:69`). For an agent pack, selection means something. For `host-processes` — one pack,
   one loophole, no other surface — selecting *and* enabling is one intent written twice.

   Options: (a) keep both, uniform and dumb; (b) let an **embedded** pack whose only contribution is
   a loophole be reachable by `loopholes.<name>.enabled` alone, with selection implied; (c) make all
   embedded packs default-selected and rely on enablement as the only gate — which contradicts
   AGENTS.md's *"nothing is active by default"* for every other kind a pack can ship.

   _Leaning:_ **(b), and only for embedded packs.** It is the option where the ceremony disappears
   without the safety doing so: an embedded pack carries yolo's own authority, and a loophole-only
   pack has nothing else to stage, so "selected" carries no information that "enabled" does not.
   (c) is too broad — it would let a pack's skills, config and briefing land because it happened to
   be compiled in, which is presence-activation for the surfaces rather than the daemon.

   _Resolved by:_ deciding whether `packs` means "what may be used" or "what is used" — the whole
   question is downstream of that.

   **Answer:**
   > _(empty — fill in when decided)_

8. **OQ-A8 — where do a pack-shipped loophole's own settings live?**

   §1.4's three options: leave `host_processes` in core; widen the per-loophole config block with an
   opaque `settings` map; or add a sixteenth contribution kind for config keys. This is the question
   that decides whether the `host-processes` conversion separates anything real or just moves a file
   while core keeps naming it.

   _Leaning:_ **the opaque `settings` map.** It needs no new kind and no new validator: core checks
   that it is an object and hands it to the daemon, which is the only party that knows what
   `visible` means. It is scoped by construction — a loophole can only be configured under its own
   name — and it composes with what that block already does for `env` and `jail_env`. The sixteenth
   kind is the right answer only if a pack ever needs to add a key *outside* its own loophole's
   namespace, and nothing does.

   _Note:_ this makes `host_processes.visible` a **deprecated alias** for
   `loopholes.host-processes.settings.visible`, so it needs a migration and a removal date, not just
   a rename. That is the real cost and it should be priced before choosing.

   **Answer:**
   > _(empty — fill in when decided)_
