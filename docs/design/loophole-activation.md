# Nothing reaches your host because it happened to be there — loophole activation

**Status:** RULED 2026-08-15, nothing built. Six rulings; **thirteen open questions**, five answered
in review and ten raised by review or by the completeness sweep (§6). The doc grew that way on purpose — every question below
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

**If you read two sections: §1.3** (the inventory) **and OQ-A9** (the one real gap the sweep found —
`default_enabled` collides with a live `enabled` key and the design never says which wins).

**§1.3** — the six-row table of everything that reaches your host and
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
| **broker daemon + relay** | *not gated at all* | `run.go:392-398` spawns them every launch, no lookup | *none* | **undecided — OQ-A11** |
| **broker jail wiring** | bundled loophole | manifest `enabled: true` **and** host `claude` on PATH | `loopholes.claude-oauth-broker.enabled` | inside `packs/claude`, `default_enabled: true` |
| **host-processes** | bundled loophole | manifest `enabled: true` **and** host `ps` | `loopholes.host-processes.enabled` **plus** top-level `host_processes.visible` | own pack, `default_enabled: false` |
| **audio** | bundled loophole *and* an official pack beside it | manifest `enabled: true` **and** the pulse socket exists | `loopholes.audio.enabled` | own pack, `default_enabled: false` |
| **journal** | **builtin service**, hardcoded in the run pipeline | the top-level `journal` key says so | top-level `journal` | **undecided — OQ-A6** |
| **cgroup-delegate** | **builtin service**, hardcoded | Linux + cgroup v2. No key exists. | *none* | **undecided — OQ-A4** |
| **host nix daemon** | mounted by the run pipeline | the socket exists on the host | *none* | **undecided — OQ-A11** |
| a user's own | `loopholes:` config block | `enabled` defaults true | `loopholes.<name>.*` | unchanged |

Read down the "on today because…" column and the diagnosis writes itself: **no two of these turn on
the same way**, and only one of them was ever a decision the user made deliberately.

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
   - **Keys — and this paragraph conflated two different things, which is a fair thing to be
     confused by.** There are **two** switches, not one, and only one of them survives R2:

     | | what it is | whose opinion | after R2 |
     | :--- | :--- | :--- | :--- |
     | **manifest `enabled`** | a field in the loophole's own manifest, today defaulting to **true** | the pack **author's** | **goes away** — renamed to `default_enabled`, default flipped to false (OQ-A9) |
     | **config `loopholes.<name>.enabled`** | a key in your user or workspace config | **yours** | **stays, unchanged** — it is the per-workspace switch R5 is about |

     So nothing redundant is being kept. The author declares a default; you override it. The one
     being deleted is the manifest field, which is exactly the collision OQ-A9 exists to settle —
     two keys over one state was never intended, it is what the codebase has today.

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

   **Answer (2026-08-17):**
   > **Going dark is fine — build no migration machinery.** *"Even if packs ship built in, the user
   > still needs to list them in their user config to get them, just like agents. No special case
   > here."* A loophole you never listed behaving exactly like an agent pack you never listed is the
   > rule working, and inventing an upgrade notice for it would carve out the special case the whole
   > document exists to delete. The general "no packs configured" guidance already covers a user who
   > wonders where something went.
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

7. **OQ-A7 — does a loophole-only pack need selecting, or is enabling enough? — ✅ RESOLVED (2026-08-17)**

   **Answer:**
   > **It needs selecting. No special case.** *"Even if packs ship built in, the user still needs to
   > list them in their user config to get them, just like agents."* My leaning was to let an
   > embedded loophole-only pack be reachable by `enabled` alone, to save a line of ceremony — that
   > is overruled, and rightly: "shipped in the binary" is not "installed", and a rule with one
   > exception is two rules. `host-processes` is listed in `packs` like anything else, and then
   > enabled.

   *(Original question and leaning preserved below.)*

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

8. **OQ-A8 — where do a pack-shipped loophole's own settings live? — DESIGNED 2026-08-17**

   > **📄 [`pack-config-keys.md`](./pack-config-keys.md) is the answer**, and it overrules the leaning
   > below: the opaque `settings` map is a **trust regression**, because core that validates only
   > "it is an object" cannot tell `settings.visible` from `settings.ld_preload` — which launders the
   > user-scope-only refusal that exists to keep `LD_PRELOAD` out of a host daemon's spawn. The keys
   > are **typed and declared in the manifest** instead, validated through the resolver core already
   > injects, delivered through a file core writes rather than an env channel the workspace controls.
   > Four questions remain there (OQ-K1..K4); none of them blocks this document.

   > **Confirmed independently in review, 2026-08-17:** *"I don't understand the yolo-ps visible flag
   > though — doesn't that mean the yolo config schema has a pack-shipped loophole baked in?"*
   >
   > **Yes. That is exactly the finding, arrived at from the other direction.** `host_processes` is a
   > top-level key in core's own config schema (`config.go:59`, validated at `validate.go:557-570`,
   > classified in `inherit.go:116-121` as *"RESERVED loophole names carried as their own top-level
   > keys"*) — and `journal` is the second one. So core's schema names two specific loopholes by
   > hand.
   >
   > Which makes this question sharper than it was filed as: moving the manifest into a pack while
   > leaving `host_processes` in `knownTopLevelConfigKeys` is the **appearance** of separation with
   > none of the substance. §1.4 has the three ways out; the reason it is still open is that all
   > three cost something.

   §1.4's three options: leave `host_processes` in core; widen the per-loophole config block with an
   opaque `settings` map; or add a sixteenth contribution kind for config keys. This is the question
   that decides whether the `host-processes` conversion separates anything real or just moves a file
   while core keeps naming it.

   > **⚠ The leaning below was priced wrong, and the sweep caught it.** "Hands it to the daemon"
   > assumes a channel that does not exist: core never tells the host-processes daemon anything, and
   > the only reason `host_processes.visible` works at all is that **the daemon opens the workspace
   > file itself, per request**. Making `loopholes.host-processes.settings.visible` real means either
   > serializing the map into the spawn — which ends a per-request re-read the package treats as a
   > frozen contract — or teaching the daemon config merging. Still my preference, but it is not the
   > free option the next paragraph claims.

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

9. **OQ-A9 — does `default_enabled` REPLACE `enabled`, or sit beside it — and which manifest sources
   does it govern?**

   *The sweep's headline finding, and the one place the design has a real gap.* `enabled` is a live
   top-level manifest key (`keys.go:27`, in the closed `topKeys` census), it defaults to **true** when
   absent (`loopholedecl.go:509-511`), **all four shipped manifests set it explicitly**, and
   `SetEnabled` (`runtime.go:420`) writes it back into the manifest file. `default_enabled` exists
   nowhere but this document.

   Both readings break something. If `enabled` keeps winning, R2 is a **no-op for every manifest that
   exists** — including the official audio pack's, which would violate R4 on day one. If
   `default_enabled` wins, an author's explicit `enabled: true` is silently ignored and `SetEnabled`
   writes a key nothing reads. And R2 is phrased as *"a **pack** declares…"* while R4's target
   (`audio`) is a **bundled** manifest and the user-loopholes dir is a third source — so if
   `default_enabled` is pack-only, **R4 has no mechanism at all**.

   _Leaning:_ **one key, renamed, governing all four sources.** `default_enabled` *is* `enabled` with
   the default flipped; `enabled` becomes a recognized-and-refused key whose error names the rename,
   `SetEnabled` is fixed to write config rather than a manifest, and the four shipped manifests are
   updated in the same commit. Two booleans over one state would give the manifest, `loopholes list`
   and `SetEnabled` three ways to disagree.

   _Price this in the answer:_ **reverse skew.** An *older* yolo reading a *newer* manifest ignores
   `default_enabled` and falls back to enabled-defaults-true — so `audio` ships default-off and an
   older build runs it on. Deletion-shaped schema changes need a refusal, not a tolerance (§4).

   **Answer:**
   > _(empty — fill in when decided)_

10. **OQ-A10 — is the broker's loophole a contribution of `packs/claude`, or its own official pack?**

    R6 says *"inside `packs/claude/`"*. [`broker-as-a-pack.md`](broker-as-a-pack.md) §6 designs a
    **separate** pack, `packs/claude-oauth-broker/`. Two docs in one sprint, two answers, and the
    difference is user-visible: can a Bedrock user drop the broker without dropping claude?

    **The trap underneath is worse than either doc says.** The reserved name is appended
    **unconditionally** (`discover.go:322-325`) — it is *not* derived from the bundled directory —
    and a pack claiming a reserved name fails the **whole launch** (`packs.go:310-311`). So deleting
    `bundled_loopholes/claude-oauth-broker/` does **not** free the name: the first commit adding the
    loophole contribution breaks every launch for every claude user until the reservation *and* the
    name special-case at `loopholesruntime.go:211-214` die in the same change. `audio` escaped this
    by renaming itself `audio-alsa`; the broker cannot, because `loopholes.claude-oauth-broker.enabled`
    is a user-visible config key.

    _Leaning:_ **inside `packs/claude`, and fix §6 rather than leaving two answers standing.** R6's
    whole argument is that the dependency is structural; a separate pack reinstates the second
    selection step R6 deletes. A Bedrock user's escape is `supersedes` on the `claude-oauth-refresh`
    capability — already built, already declared — not deselection.

    **Answer:**
    > _(empty — fill in when decided)_

11. **OQ-A11 — do the ungated host daemons get gated: the broker singleton, the per-jail relay, and
    the host nix-daemon socket?**

    §1.1 and §1.3: `run.go:392-398` spawns the broker singleton and a relay **every launch with no
    lookup**, and the host nix-daemon socket is mounted because it exists. Both are host crossings
    that no key controls, so **R1 has counterexamples in the run pipeline today** — and R6 by itself
    does not remove them: after the move, a jail that does not select the claude pack has no broker
    in any surface (`loopholes list` will not name it, the briefing will not mention it) while yolo
    keeps spawning the singleton on that host at every launch. A daemon none of yolo's own surfaces
    name is worse than one that is merely on.

    _Leaning:_ **gate the broker daemons on the loophole record; leave nix for now and say why.** The
    broker is squarely in scope — it is the loophole R6 is already moving, and the fix is to route
    `brokerEnsure` through the same record everything else uses. Nix is a different animal: it is
    infrastructure the *image* depends on rather than a capability a jail reaches for, and gating it
    is a `--no-nix`-shaped feature, not an activation ruling. But §1.3 must carry the row either way
    — the table's credibility is the argument.

    **Answer:**
    > _(empty — fill in when decided)_

12. **OQ-A12 — `yolo check` cannot see pack-shipped loopholes; does that get fixed in this sprint?**

    The health section reads only the non-pack sources, so today it costs nothing: the only
    pack-shipped loophole is `audio-alsa`, which has no `doctor_cmd`. **This sprint moves the only
    two loopholes that have one** — the broker and host-processes — out of the surface that reports
    their health. On the day the conversion lands, `yolo check` prints a cheerful "no loopholes
    installed" while the broker's cert freshness, liveness and self-check go unreported.

    _Leaning:_ **fix it in the sprint, as part of the conversion rather than after it.** §4 already
    worries that R3 costs diagnosability; landing this in the same sprint would compound it on
    exactly the command a user reaches for when a loophole is silently off. It is also the honest
    completion of [`pack-code-separation.md`](pack-code-separation.md)'s doctor ruling, which said
    `check` should read loophole health through the manifest surface rather than hand-rolled Go.

    **Answer:**
    > _(empty — fill in when decided)_

13. **OQ-A13 — enabling is now the dangerous direction, and nothing discloses it. Does it need a
    surface?**

    Today `enabled: true` in an agent-editable workspace file is **inert** — the manifest default is
    already true — so the only meaningful workspace power is turning things **off**, which is
    precisely the direction yolo discloses at launch and warns about in `yolo check`. R2 makes
    `enabled: true` **the activation verb** and R5 keeps it available at workspace scope. That path
    produces no violation, no disclosure, no launch line and no warning.

    The lockfile half is the same shape one surface over: a fetched pack the user reviewed, approved
    and deliberately never enabled can flip `default_enabled: false → true` in a later commit with a
    byte-identical claim set, so the approval check still passes.

    _Leaning:_ **yes — mirror the existing disclosure, do not invent a new one.** The machinery is
    already there for the opposite direction; pointing it at the new dangerous direction is a small
    change and keeps one vocabulary. The lockfile half is the sharper question and may belong with
    OQ-LP8's content-anchoring rather than here.

    **Answer:**
    > _(empty — fill in when decided)_
