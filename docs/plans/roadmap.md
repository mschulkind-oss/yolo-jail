# Roadmap

**Status: 17 needing you · 3 ready · 0 in progress · 6 waiting · 0 broken · 2 icebox.**

Last updated **2026-09-01**. Counts tallied from this file, not asserted — one per `### 💬` heading,
one per top-level bullet in every other section, and each bullet's glyph matches the section it is
in.

> [!IMPORTANT]
> **Four rulings on 2026-08-25 turned the largest measured reclaim in the repo from a question into
> queued work — and two of the three rows it queued shipped the same day.** 💬 6 — image staging — has
> left this file. What came out of it was **three 📦 rows and one new 💬 row**, and a design doc that
> did not exist this morning. **C2 and C3 left this file the same day they were queued** (2026-08-25);
> what remains of that sequence is the retention rule (R3), still gated on OQ-DF3, and the
> reclamation half of the disk row:
>
> - **OQ-4** — `packages:` stays workspace-scope. *"Yes, has to be."* Fix the cost, not the scope.
> - **OQ-3** — content-addressed image tags win, and `localhost/yolo-jail:latest` is *"definitely
>   not"* a public surface anyone may depend on by name. *(The cachix caveat attached to that ruling
>   is about the **nix binary cache**, a different surface from the podman tag — do not conflate
>   them.)* **SHIPPED as C2 in `be7b8591`, 2026-08-25.**
> - **OQ-1** — if C4/C5 ship at all they ship as an **opt-in fast path with the baked path retained**.
>   The go/no-go was gated on the re-measurement after C2+C3, and **that measurement has now been
>   taken** — [`image-staging-vs-baking.md`](../design/image-staging-vs-baking.md) §1.8, the same
>   day. It reports a flat curve on the workload it could measure: C3 discharged C4's disk case, C2
>   discharged its frequency case, the 52 s cold launch is one C4 does not shorten, and the one
>   workload C4 exists for is explicitly NOT MEASURED there. **C4/C5 stay not queued below** —
>   taking the measurement discharges the gate's step, not the gate; the call is yours.
> - **OQ-5** — the cached tars are a **bug**: *"I see no reason to keep any of this around … we need
>   to use minimal disk space"*, and the GC work that has shipped is *"nowhere near enough"*. Executed
>   in [`minimal-disk-footprint.md`](../design/minimal-disk-footprint.md) — queued 📦 below, with its
>   four mechanism questions as 💬 **16**. **One of those four is already ruled:** OQ-DF1 —
>   *"stream, keep zero tars"* — which is what **C3** implements (shipped 2026-08-25). The remaining
>   three are open, and the pre-C3 backlog is still on disk.
>
> **The number those rulings were made on has moved, and it was already understated.**
> [`image-staging-vs-baking.md`](../design/image-staging-vs-baking.md) §1.6 measured 404.4 GiB across
> 125 tars on 2026-08-15. Re-measured here **2026-08-25: 480.71 GiB across 148 tars** — plus a
> **second image cache that doc never looked at**, 125.41 GiB across 36 tars host-side. **606.12 GiB
> across 184 tars**, on a root device that is **84 % full with 608 GiB free**.
>
> **Still fourteen rows need you, and that is not a row count that failed to move — it is a
> substitution.** 💬 6 left and 💬 **16** was born in its place: OQ-5 ruled the *premise* of the disk
> work, and [`minimal-disk-footprint.md`](../design/minimal-disk-footprint.md) executing that ruling
> minted four questions about the *mechanism* (`OQ-DF1 … OQ-DF4`). One of them — **OQ-DF3** — blocks
> the cheapest reclaim in the whole doc. *(💬 1 — program delivery — left on 2026-08-24: ten questions
> ruled the same day, four build steps shipped within hours, and the remainder is the last 📦 row
> below. The 48-agent backend-parity census behind issue #39 — 74 mechanisms, **21** distinct defects
> (17 at the sweep's close; four more surfaced after it, one of them by a class test written for
> three known instances) — is 💬 **15**.)*

**What this is.** The forward plan and nothing else. **If it is in this file, it is not done.** Work
that ships leaves immediately — the record is the commit history. Decisions *not* to build move to
[`retired-decisions.md`](../design/retired-decisions.md), because a rejected design with no record
gets re-proposed.

**How questions are held.** Open questions live in their **design doc**, with stakes and a leaning,
and this file links to them by ID. It never restates them — that is how the count drifted to 19 while
the real number was closer to 50.

> [!NOTE]
> **The old vocabulary, for anyone arriving from a doc that still uses it.** Until 2026-08-17 this
> file was a lettered queue — **rows B1 / B1b / B2 / B3 / B4**, **threads A–C**, and IDs like **N3**
> and **S5**. Restructuring into states retired the letters, so several sibling docs cite names this
> file no longer holds. Where they went: **B-rows** are now [`boundary-broker.md`](../design/boundary-broker.md)
> §7's own numbering; **Thread A** is in [`retired-decisions.md`](../design/retired-decisions.md);
> **Thread C** closed in [`shipped-2026-08-12.md`](shipped-2026-08-12.md); **N3** is `nix OQ-1` in
> [`noncontainer-nix-environment.md`](../design/noncontainer-nix-environment.md); **S5** is in
> [`BACKLOG.md`](BACKLOG.md) §Stage E. **Cite a state row or an OQ ID — never a letter.**

**And the real number is now countable rather than estimated: the sweep returns 113**, re-run
2026-08-30. *(It read **104** on 2026-08-25; sixteen docs have changed since, so the delta is NOT
reconciled row-by-row the way the 2026-08-25 table below reconciles its own. What is accounted for: **+9** from the two docs written 2026-08-29/30 — [`profiles-as-pack-variants.md`](../design/profiles-as-pack-variants.md) contributes **5**, [`reference-mismatch-diagnostics.md`](../design/reference-mismatch-diagnostics.md) **4** — plus **+1** for 💬 17 in this file. The rest is other sessions' work and was not audited here; treat the reconciliation table below as history, not as current arithmetic.)* **It is one below yesterday's and almost none of it is the same
questions** — which is the most useful thing this count has done yet. It reconciles exactly, to −1:

| Δ | Why |
|---|---|
| **−4** | [`image-staging-vs-baking.md`](../design/image-staging-vs-baking.md)'s OQ-1 · OQ-3 · OQ-4 · OQ-5 moved to its §10.1 ledger. It contributes **0** now |
| **+4** | [`minimal-disk-footprint.md`](../design/minimal-disk-footprint.md) minted `OQ-DF1 … OQ-DF4` executing OQ-5's ruling |
| **−1** | `OQ-DF1` ruled the same day (*"stream, keep zero tars"*) and implemented as C3; that doc contributes **3** |
| **−1** | 💬 6 deleted from this file |
| **+1** | 💬 16 born in this file |

**A flat count hid a complete turnover, so read the composition, not the total.** Note the third and
fourth rows: **the sweep counts this file too** — `docs/plans/roadmap.md` contributes **15** of the
104 (fourteen `### 💬` headings plus one `1. 💬` bullet inside the macOS sandbox row). That is worth
knowing before anyone reasons from the number.

> [!WARNING]
> **A roadmap row and the questions it points at are counted twice, and that is a bias in the tool
> this file trusts.** A row is a *grouping* of questions that already live in their design docs, so
> 💬 6 and the four image-staging OQs were **five** entries for **one** decision, and 💬 16 and the
> four `OQ-DF*` are five for one today (four, since OQ-DF1 was ruled). It follows that the "gap
> between 104 and 14" framing below
> compares a number against a set that contains it. Not worth redefining mid-sprint — worth saying
> out loud, because the last time this count was quietly wrong it was wrong by six.

The two-wave audit that produced this number found 95; program-delivery's ten have since been ruled
out of it, and the corpus still grew, which says what the command is for. The audit gave every
question a `💬` and a stable ID. It used to require reading ~9,000 lines; it is now one command:

```console
$ rg -c '^(#{2,4} |\s*[0-9]+[a-z]?\. |\s*[-*] )(<a id="[^"]*"></a> ?)?💬' docs/ --sort path
```

*(That is one of **five** sweeps that keep this corpus honest — links, questions, SHAs, code paths,
heading anchors. All five, and the allowlists they need, are in
[`README.md`](README.md#keeping-this-corpus-honest--the-five-checks-so-they-are-re-runnable).)*

> [!NOTE]
> **The optional anchor-tag group is not decoration — it is the bug this count already had.** A
> first version of the regex omitted it and returned **86**, silently missing all six of
> `local-model-endpoints.md`'s questions, whose headings begin `1. <a id="oq-lm1"></a>💬 …`. Six live
> questions invisible to the tool that exists to count them is exactly the failure this whole pass
> was about, found by an adversarial re-check of the pass itself. If you add a heading style, check
> the count moves.

**Fourteen rows below is what the blocking subset groups into** — the rest are named
in *What the roadmap does not cover* at the end, deliberately. The gap between 104 and 14 is the
point of this file: a row is a *decision*, and one decision usually closes several questions.
**💬 6's four rulings closed a whole row in a single turn**, which is the gap doing exactly what it
is for — and 💬 16 opening in the same pass is the other half of the same mechanism, since executing
a ruling is what finds the questions the ruling did not reach.

| | Means |
|---|---|
| 💬 | **needs you** — a decision only you can make. |
| 📦 | **ready to build** — designed, questions answered, no blockers. |
| 🏗️ | **in progress** in the active session. |
| 🔒 | **waiting** on a machine, a real host, or an external dependency. |
| 🛑 | **broken** — actively failing. |
| 🧊 | **icebox** — genuinely unsure we want it, or awaiting outside evidence. |

---

# 💬 Needs you

Grouped by decision, not by question. Each row names its design doc; the doc holds the stakes and my
leaning. **Nothing here asks you to pick an execution order** — sequencing is mine.

### 💬 2 — Trust paths: where we extend trust, and where a pin is theatre

📄 [`trust-paths.md`](../design/trust-paths.md) — 25 paths enumerated from the code · partly supersedes
[`pack-execution-trust.md`](../design/pack-execution-trust.md)

**Two rulings on 2026-08-18, both closing a finding by removing a mechanism rather than adding a
gate** — and one of them obviated a question rather than answering it:

- ✅ **OQ-TP5 — no evergreen npm.** `install` obeys the lockfile, `update` is the only act that
  resolves a new version, and the hourly poll is downgraded to informational. **Built 2026-08-18**
  (`b3a29ad8`), minus the pin it has nowhere to record — which is OQ-TP4 below.
- ✅ **OQ-TP6 — a refused contribution refuses the launch.** No partial packs: fix it, remove it, or
  approve it. **Built 2026-08-18** (`6385dfbb`). Both carry release-note entries.
- ✅ **OQ-TP2 — nothing explicit.** Agent context needs no gate and no separate disclosure: the
  lockfile's commit pin closes over the whole tree, prose included. *Inherits OQ-LP8/G2b — the pin is
  recorded and never consulted at launch, so it covers this on paper until enforcement lands.*
- ✅ **OQ-TP1 obviated by TP6.** There is nothing to carry into a jail if no jail starts, so the
  origin-gate finding stops being a broken guarantee. **The fatal has since shipped** (`6385dfbb`),
  so this is now enforced rather than merely defined — the caveat this row used to carry is spent.

What is still open:

- **OQ-TP3** — is pinning worth building at all? **Partly answered:** TP5 settles row 1's behaviour.
  What is left is scope — whether yolo pins its *own* embedded packs, and whether a fetched pack is
  *required* to pin rather than merely permitted.
- **OQ-TP4** *(new)* — **where does an embedded pack's npm version get pinned?** The lockfile is per
  *fetched* pack, but pi, copilot, codex and opencode are all **embedded** — so TP5's ruling has no
  home for the pin in the case that covers nearly every user. This gates implementing TP5.
- **OQ-X1** — does a digest-pinned installer script count, given its own fetches are not pinned?
- **OQ-TP7** *(new, and it is the shipped fatal's own loose end)* — **OQ-TP6 made the refusal fatal
  and left the loop around it behind.** Re-measured 2026-08-23: `yolo check` still reports `[PASS]`
  on the very pack the next launch refuses — both loads pass `e.MayGrantHostFiles()`
  (`check/packs.go:130,162`) and **`packRefusals` has no caller anywhere under `internal/cli/check/`**.
  The `approve` half has since gone **half-caught-up**, which narrows the question rather than
  closing it: `resolveHostApproval`'s non-interactive refusal now says *"requires an interactive
  terminal … rerun from a terminal"* (`pack.go:1228-1236`), while the **launch** refusal still says
  only "run `yolo pack install`" — so the two ends of one flow now give different amounts of help.
  The ruling is scope: how much of the loop catches up before "the reader can act on it" is true
  from everywhere a user meets the refusal.
- **OQ-LP8 / G2b** — you ruled the shape (approval pinned to a commit); what remains is that
  `LockEntry.Commit` is **never consulted at launch**, so the pin does not yet exist. **Verified
  2026-08-23, and the code says so itself:** `HostAccessApproved` *"COMPARES CLAIM STRINGS ONLY —
  never the commit the approval was granted against"* (`internal/packsrc/lock.go:78-96`), and the
  hole is pinned by `TestHostAccessApprovedComparesClaimStringsOnly` rather than only described —
  **so closing it means changing a test that currently asserts the gap.** That is the good version
  of this situation: the defect cannot rot silently, and the fix has a named landing site.

### 💬 3 — Auth mode

📄 [`agent-auth-modes.md`](../design/agent-auth-modes.md)

**auth OQ-6** gates building `claude-bedrock` and is the only one with reach; the doc recommends
*fetched*. **auth OQ-1** resolves by experiment, not ruling (~5 minutes: does Claude Code send a
subscription OAuth bearer to a non-Anthropic base URL?) and it gates boundary-broker B2. **OQ-2 · 3 ·
4 · 9** are smaller — each now carries a leaning and an empty Answer block, so they can be ruled on
in the doc rather than here.

**OQ-7 has been restated** (2026-08-23) rather than left moot. It asked *"does the **Teams** pack own
the model IDs"*, and Thread A abolished the Teams pack by collapsing the two-auth-pack shape into one
`claude-bedrock`. The substance survived the collapse — it is about where a **pin** lives, not how
many packs there are — so the doc now asks it of the auth pack generally, with the leaning that the
base `claude` pack pins nothing.

### 💬 4 — Non-container nix

📄 [`noncontainer-nix-environment.md`](../design/noncontainer-nix-environment.md)

**nix OQ-1** is the real content of what this file used to call **N3**, and the doc phrases it as
**run vs. configure** — does yolo *run* a process at `host`, or only *configure* one? *(This row used
to render it as "placement or backend", a phrasing that appears nowhere in the doc. Reconciled
2026-08-23; the ID is unchanged and both siblings still cite it as `N3/OQ-1`.)*

**Two corrections to what this row used to imply.** First, **roughly half of that doc has shipped** —
Option 0 landed 2026-08-02 and Option 1 mostly landed 2026-08-05 as N1+N2, which renamed the
attribute the doc proposed (`yoloNoncontainerPackages`) and explicitly rejected its
`yoloHostPackages`. Second, *"everything else is subordinate to OQ-1"* is **not true**: **OQ-7** is
independent, and **OQ-8**'s reporting half and **OQ-9** are worth fixing under any answer. Seven
questions are live there (OQ-1 · 3 · 4 · 5 · 7 · 8 · 9); two settled into its Decision Ledger.

Still not urgent — the auth thread routed around the `env` refusal that motivated it. *(A claim
here that the refusal text "points at this doc's own Option 2" was **wrong**, and an adversarial
re-check caught it: `fieldset.go:88` names `yolo --at host -- <cmd>` and cites the **env-manager**
design §4.1, not `noncontainer-nix-environment.md`. The refusal does frame the limit as the
*command's* rather than the notch's, which is the part worth knowing.)*

### 💬 5 — Boundary broker

📄 [`boundary-broker.md`](../design/boundary-broker.md) — **OQ-A · OQ-C · OQ-E · OQ-B1b**

**OQ-A** sizes the whole project (if synchronous-only suffices, most of §7 step 3 never gets
written). **OQ-C** is a real API-shape decision: does the jail see the *result* or just success —
i.e. does every verb need a response schema, or none? The security half of **OQ-E** is settled
(authority stays in the unix socket); only its packaging half — which client the human reaches for —
is live. **OQ-B1b** sizes B1b alone: vendor unYOLO's ~2,100-line MIT, stdlib-only policy engine at a
pinned SHA, or re-derive it. *(B1b was created as an ID on 2026-08-23 — §10.6 had been calling it
"the maintainer's call, see the B1b row in roadmap.md", a row that never existed, while this file
cited the ID back at the doc. Neither end resolved.)*

### 💬 7 — macOS, and the environment-manager stories

📄 [`macos-user-build-step-threat-model.md`](../design/macos-user-build-step-threat-model.md) ·
[`environment-manager-user-stories.md`](../design/environment-manager-user-stories.md) ·
[`macos-revival-and-distribution-plan.md`](macos-revival-and-distribution-plan.md)

**Two of the three defects these stories are built on have been FIXED** (G1 on 2026-08-01, G3 on
2026-08-12), and the doc now carries a dated verdict at every gap — read those before hunting for a
bug. What is still live is **Gap 1, and it is worse than the story says**: `config ls`'s `host`
column is not derived from `HostSource` at all but from a hand-maintained two-entry map
(`surfaceHasHostLayer`, `internal/cli/configls.go:196-202`), so any *pack* surface that really does read machine state shows
**no host layer**.

**user-stories Q1** is called "the biggest question in the document" by its own author — **and its
premise needs narrowing**: capture does *not* outrank every declared layer — it loses to
`computed`, `transform` and `managed` (`internal/agentcfg/compose.go:357-379`). Q1 is still the
biggest question, on the narrower and better ground that capture is **undeclared**, not that it wins
everything. **Its leaning is also half-built**: it wants capture to become a *staging area* that
`yolo config promote` drains, and that subcommand **does not exist** (`internal/cli/config.go:33-60` lists `ls · render ·
diff · reset · capture · drift · dump`, verified 2026-08-23). So `apply --sealed` can already
*refuse* on an outstanding capture while the user's only remedy is still "discard it" — answering Q1
in the leaning's direction means building the verb, not just ruling.

**Q7** asks whether Linux `guest` is a promise or a hypothesis — **and events have overtaken its
leaning.** It wanted the vocabulary withheld until
`guest` renders; the three-notch vocabulary **already shipped** (`confinement: guest` validates,
`apply --at guest` parses, the briefing has a guest body) while `bwrap`/Landlock exist only as a
profile constant and a label (`internal/render/confinement.go:132-136`, `modes.go:185`). So the
question is now the harder one: *we shipped it — does the Linux row stay?*

**threat-model Q1-Q3** cover the repo-root
refusal, `--accept-flake-config`'s substituter surface (now live — see the shipped item), and a macOS
build sandbox. **OQ-L1** explicitly blocks Track L part 2. **OQ-GN1 … OQ-GN4** are new (2026-08-23),
in the guest-notch handoff — which now says plainly that its item 1.4 is only *half* answered: the
sandbox reads the staged pack root and runs the toolchain, so what is untested is the
`sudo -u _yolojail` staging step above it, not the confinement.

**And the one with a clock on it, which arrived here by losing its parent.** It was carried by the
🛑 nightly entry and cited [`image-staging-vs-baking.md`](../design/image-staging-vs-baking.md) §7 —
a section about the silent-fallback defect that **never mentions darwin**. Its real home is
[`macos-support-matrix.md`](../research/macos-support-matrix.md) §0, which now carries it; this row
keeps the summary because it is the only item in this file with a **deadline** rather than a
question, and a deadline unanswered decides itself.

- **26.05 is the LAST nixpkgs supporting `x86_64-darwin`**, security-fixed only to end of 2026. The
  nightly needs `macos-26-intel` because GitHub's Apple Silicon runners cannot nest a VM for Podman
  Machine, so when 26.05 lapses the choice is a self-hosted arm64 Mac runner or macos-user-only
  macOS tests. **Needs you, but not yet** — a deadline rather than a bug.

  > [!WARNING]
  > **⚠ This row used to say the opposite of the truth, and the correction matters more than the
  > deadline.** It claimed `927fb9f`'s `nixpkgs-26.05-darwin` was *"a pin on a dead branch, not a
  > supported line"* and that every probe throws `Nixpkgs 26.11 has dropped support for
  > x86_64-darwin`. **Re-measured 2026-08-23:** `nix eval
  > '#packages.x86_64-darwin.yoloNoncontainerPackages.name'` **succeeds**, emitting only
  > *"Nixpkgs 26.05 will be the last release to support x86_64-darwin"*. `927fb9f5` added a **second
  > nixpkgs input pinned to 26.05 for that system alone**, which is precisely what keeps Intel Macs
  > working — an Intel Mac gets **5 of 6** agent CLIs today, not zero. 26.05 is not a dead branch; it
  > is the last release that *does* support the platform.
  >
  > What survives is the deadline itself: the clock is the security-fix window (end of 2026), not a
  > release date, and the question of what replaces the Intel runner is unchanged.

### 💬 8 — Packs and `host_files`: the tail, now with a home

📄 [`BACKLOG.md`](BACKLOG.md) §Stage E — **S5 · OQ-CO · OQ-S4 · OQ-E4 · E1 · E2 · E5**

**These used to be "born in this file with nowhere else to live", which was half true and is now
fixed.** E1/E2/E5 always had a home in Stage E; **S5** and **OQ-CO** genuinely existed nowhere but
one line of this file — the exact thing this file's own rule forbids. All seven now carry stakes, a
leaning and an empty Answer in Stage E.

- **S5** is the only one that is a live gap rather than a preference: a jail resolves a skill-name
  collision **silently**, where `yolo host apply` refuses. Warn at launch, fail `yolo check`, or refuse
  the boot.
- **E1 + E2 + `pack-host-management-plan.md` OQ-B are ONE decision** — the `0o444`-vs-`:ro`
  asymmetry. **Four instances, not three** (2026-08-23): `composed-file-permissions.md` §7.4 is the
  fourth, and it is cross-linked rather than given its own ID, because minting a fourth name for one
  question is how a decision becomes four decisions. Decide them together or none.
- **OQ-CO and OQ-S4 are the same question asked of different kinds:** should the two notches agree?
  One is `config-overlay`'s silent last-one-wins; the other is whether a pack's `into` **narrows**
  skills delivery or only adds to it — the jail and the host answer differently today.
- **OQ-E4** is the ~15% of E4 that did not ship: do `stateful` surfaces get comment preservation?
  `rmw` preserves, `computed` correctly does not, `json` is provably vacuous.

*(**E3 has left this list — it shipped 2026-08-15**, `29ccf212`, and both this file and the backlog
row were still calling it open. And **E4 is not a question**; only its `stateful` residue is, which
is why the list cites OQ-E4 and not E4.)*

### 💬 9 — The config snapshot's migration window

📄 [`config-safety.md`](../design/config-safety.md) — **OQ-D3**

**OQ-D1 is decided AND built** (2026-08-18): the approval snapshot moved host-side, out of the rw
bind mount, because a record the jail can rewrite is not a record. **Its successor is live.**
**OQ-D3** — the *migration signal* still lives in the mount it is signalling about, so deleting
`<workspace>/.yolo/config-snapshot.json` turns a changed config into a silent "first run" accept.
That reopens exactly OQ-D1's hole, for as long as a workspace goes un-launched after the upgrade.
Confirmed live in code 2026-08-23 (`internal/config/snapshot.go:221-227`), and the doc warns against
the obvious-looking wrong fix — adopting the legacy file's *content* as a baseline.

### 💬 10 — `yolo check` tells you about the wrong machine, in three places and one vocabulary

📄 [`broker-ca-and-nested-hosts.md`](../design/broker-ca-and-nested-hosts.md) — **OQ-3**

**These were three separate small questions until 2026-08-23; they are one.** `check` has no way for
a section to say *whose* facts it is reporting — the host's, or the runtime it can see from in here —
so every section decides by hand and some decide wrong. The ruling is what a jail-observable section
should **print**: a fourth verdict beside `[PASS]`/`[FAIL]`/`[WARN]`, or a scope suffix.

- **The measurement that makes it one question, not three:** `check`'s reporter has exactly three
  verdict tokens and **no `[SKIP]`**, so a section that steps aside emits `[PASS]` — which is what
  hid a daemon that never started. **Ten call sites already step aside this way** (measured
  2026-08-23; the doc previously said four, and the undercount was in the leaning's favour) —
  nine distinct sections, because `sectionGPUAmd` holds two of them.
- **`sectionRunningJails` has no in-jail guard** (`check.go:556`). From inside a jail it reports the
  *nested* podman's view — measured `[PASS] No jails currently running` in here while the host had
  one. Left alone so far because it is *true of the runtime it can see*, and the orphan-cleanup path
  underneath acts on that same runtime. **On its own this is a wording preference; as an input to the
  vocabulary question it is evidence.**
- **`sectionGPUNvidia` has none either** (`sections_devices.go:38`) — three `[FAIL]`s for host facts,
  where its AMD twin guards both checks. That asymmetry is 🔒 below because deciding *which rows* to
  guard needs a host with a card; the *vocabulary* does not.

### 💬 11 — Two that are nobody else's question

- **A concurrent launch attaches by re-running the entrypoint inside a jail that may still be
  booting.** Found while shipping the waiting notice (`c2188bba`) — the question that entry was
  sitting on top of. The wait now explains itself, but it ends in a `podman exec` running the FULL
  in-jail entrypoint boot (shims, launchers, `.bashrc`, bootstrap) inside a container whose first
  boot may still be provisioning, so "graceful attachment" means two entrypoints writing the same
  generated files at once. Clean in 4/4 real runs; unexamined, not failing. The ruling is only
  whether it is worth examining — the cheap fix is to serialise the second entrypoint behind the
  first, the honest one is to find which generated files can collide. Note `stopLoopholes`
  (`loopholesruntime.go:327`) already does its own non-blocking acquire on this same lock with its
  own notice, so two places reason about this lock's contention and neither knows about the other.
- **Should `yolo check` validate an npm selector's shape?** Now that a `package` string can carry a
  version, a typo like `foo@@1.2.3` reaches npm and fails at first use *inside* the jail, where the
  diagnosis is worst. Cheap host-side check; needs a ruling only on how strict to be.
- **A fourth spelling of the port gate survives the applied-mode fix.** The nested-ports repair
  gated `-p`, `forward_host_ports` and the route_localnet sysctl on `appliedNetMode`; the
  host-side socat spawner (`run.go`, ~:599-621) still re-derives the CONFIGURED mode inline, so a
  nested launch declaring `forward_host_ports` starts socats the jail is never told about —
  harmless (killed at exit; the feature was already meaningless under a shared netns), but it is
  the last site not reading the shared predicate. Cheap fix, no ruling needed beyond scheduling.
- **Pack manifest strings are spliced into launcher bodies unquoted.** `__YOLO_URL__` and the npm
  package name land in generated bash raw (beside the now-correctly-quoted receipt uses of the
  same strings), so a fetched pack's `url`/`package` reaches `bash` as code — post-approval, and
  embedded packs are trusted, which is why it has never bitten. Pre-existing; found by the
  2026-08-24 receipts review. The fix is `shquote` at the splice, template-wide; wants its own
  careful pass because tests pin template fragments.
- ~~**Three CODE COMMENTS the docs now outrank.**~~ **FIXED 2026-08-23** (`6e84f1cf`), comment-only,
  `check-ci` green: `mcp.go` advertised `${VAR}` interpolation removed by ruling on 2026-08-03 while
  its own header 45 lines above said otherwise; `footprint.go` called a loophole-name collision
  *"not yet fatal at launch"* when it has been fatal since `PackLoopholeNameConflicts`
  (`run/packs.go:313`); `seatbelt.go` described the readonly denies as one `deny` form **per entry**
  when it emits one form with one `subpath` clause per entry. **Kept as a row for the lesson, not
  the work:** the seatbelt error had already been copied into a research doc before anyone read the
  function body. *A comment nobody re-reads is where the next doc's wrong claim comes from.*
- **A fourth PATH exists that claims to match the third and does not** *(found 2026-08-23, while
  correcting AGENTS.md's own PATH line, which was also wrong).* `BootPath`
  (`internal/entrypoint/boot.go:356-361`) is the authority and includes `~/.local/bin`. The PATH set
  for the **mise-trust subprocess** (`boot.go:528-532`) omits it, under a comment saying it *"matches
  the pre-exec PATH set in `main()`"*. Nothing is known to break — `~/.local/bin` holds the native
  agent installs and the chrome-devtools wrapper, none of which that subprocess calls — so this is a
  **drift question, not a defect report**: is the fourth list meant to be a narrower PATH on purpose,
  or is it the third list that has since grown? The cheap fix is to derive it from `BootPath` so the
  question cannot recur; the honest one is to find out which of the two was right.

### 💬 12 — `pack-host-management` OQ-B, and `pack-capabilities` OQ-CAP

📄 [`pack-host-management-plan.md`](pack-host-management-plan.md) ·
[`pack-capabilities.md`](../design/pack-capabilities.md)

**OQ-B** — should host-side `files` be `0o444`? **This is not its own decision.** It is the
`0o444`-vs-`:ro` asymmetry in a **fourth** place; the others are `E1`, `E2` (💬 8) and
`composed-file-permissions.md` §7.4. `:ro` is *enforced*; `0o444` is only *asymmetric* — a `chmod`
defeats it, and `hostfilestree.go:157` shows the writer chmodding a `0o444` file back to writable to
reopen it, which is exactly the move a hand-editing user makes and then loses. **Decide all four
together or none.**

**OQ-CAP** — where `supersedes` lives (manifest top level vs a `contributes[]` entry). Uncontested
since 2026-08-13. **I think this should leave the list**: a question with one plausible answer and a
one-line edit behind it is a confirmation, not a decision, and it spends the scarcest thing here.
Either queue the line or retire the idea — the argument is in
[`further-roadmap-ideas.md`](further-roadmap-ideas.md) §4 b.

### 💬 13 — Nested nixpkgs attribute paths in `packages`

📄 [`package-nested-attribute-paths.md`](../design/package-nested-attribute-paths.md) — **OQ-1**

This sat in 📦 as *"designed, questions answered, no blockers"* and it is none of those. Its doc is
`**Status:** DESIGN SKETCH, 2026-08-22. Nothing built.` — **still true, re-verified 2026-08-23**
(`packageNameRe` is still single-dot, `parseDottedSpec` unchanged, no resolver anywhere in
`flake.nix`) — and **OQ-1** is the resolver's central rule: how a dotted path resolves when a
derivation output and a nested collection member claim the same name. It carries a leaning and an
empty Answer, so it gates the whole item rather than one corner of it.

**One thing shipping this costs that the doc did not price.** The refusal it quotes
(`60376fed`, *"a `packages` entry naming a nixpkgs COLLECTION is refused by name"*) is quoted
**truncated**; the full message ends with a normative sentence this design **reverses** — *"A
collection member is NOT selectable from `packages`: use the member's own top-level attribute … and
drop `<entry>`."* That is advice, not a diagnostic, so shipping means **rewriting the message**, not
just narrowing when the throw fires.

### 💬 14 — Pack-shipped binaries: the capability the broker sprint promised and did not finish

📄 [`broker-as-a-pack.md`](../design/broker-as-a-pack.md) — **OQ-BP5 · OQ-BP6**

**This row exists because the sprint ended and these two did not.** OQ-BP1 ruled that the broker's
move and the pack-shipped-binary capability ship **together**; what actually landed on 2026-08-19 was
the move, on a **baked** daemon — which §3.1 explicitly permits for an official pack, so nothing is
broken. What is owed is the capability itself, and it is owed to the *next* pack, not to the broker.

- **OQ-BP5** — download-with-digest only, or also a declared build step? They are not symmetric: a
  download satisfies P1 (the digest *is* what runs); a build generally cannot, so what runs is
  decided at install time by whatever toolchain the machine has.
- **OQ-BP6** — may a **fetched** pack ship a *host-side* daemon binary? Refusing it while permitting
  a fetched pack's arbitrary `host_daemon.cmd` would block the declarative form of a capability and
  permit the imperative one — the shape OQ-LP14 already suffers from.

⚠ **The cost OQ-BP1 put on the critical path is still unpaid**: the release process has to produce
the matrix a manifest's `platforms` declares. Declaring more than you build turns *"unsupported
here"* into *"supported, missing"* (`broker-as-a-pack.md` §9).

---

### 💬 15 — Backend parity: the census, and whether macos-user gets briefings at all

📄 [`backend-parity.md`](../design/backend-parity.md) — **OQ-BP-1 · OQ-BP-2 · OQ-BP-3 · OQ-BP-4**

**Born from issue #39 and the sweep behind it.** Fourteen of the twenty-one defects are fixed or
warned (that doc's §5 is the table); what is left is a decision about the mechanism, not about any
one bug.

⚠ **The count grew after the sweep ended, and that is the most useful fact in this row.** It was
seventeen. A class test written for three known instances failed immediately on a fourth nobody had
looked for — the host `~/.config/nvim` bind, wrong since long before any of this. Twenty of the
twenty-one were found by a human noticing; one was found by a single narrow invariant over a single
argv shape. **That is OQ-BP-1's case, measured rather than argued** (§5.2).

- **OQ-BP-1 — is a per-backend census worth 2–3 days?** It makes the SILENT half unrepresentable
  and cannot touch the WRONG half — the two most serious findings emitted an argv the backend then
  could not execute, and a census marks both `Honored`. The deciding work is already done; the
  cells are the sweep's own reasoning. **The four-state vocabulary is the part to read**: eleven of
  forty-two candidates were refuted because the backend achieved the outcome *another way*, so a
  boolean census would cry wolf a quarter of the time and be switched off.
- **OQ-BP-2 — do briefings and skills get DELIVERED to macos-user?** Today the agent starts there
  with no AGENTS.md, no CLAUDE.md and no skills, including the built-in suite — while the
  blocked-tool shims *are* generated, so `grep -r` exits 127 with nothing explaining it. Warned as
  of today. My leaning is deliver it, and land it *with* a Mac session rather than blind.
- **OQ-BP-3 — do the fourteen new launch warnings need suppressing?** It was ten when the question
  was written and grew by four the same afternoon. A warning people learn to skip is worse than
  none. My leaning is still not yet, and per-key when it comes — and the cost is less uniform than
  the count suggests: most fire only when you declared the thing, and only two are unconditional
  (both macos-user).
- **OQ-BP-4 — is Apple Container's loophole skip still justified?** *This is your own question —
  "shouldn't the broker be in use here?" — and chasing it found something.* The skip's stated
  reason is *"no socket bind-mount there"*, which is true of the **unix-socket era**: under
  loopback-TLS a jail learns its service from a 0600 endpoint **file in a bind-mounted
  directory**, and that backend mounts directories fine. What still blocks it is `--add-host`,
  which stops **intercepting** loopholes only — the broker intercepts, `journal` and
  `host-processes` do not. **The stakes: with no broker there, concurrent jails on a Mac race the
  single-use refresh token** — exactly what it exists to prevent. Needs a Mac to settle.

---

### 💬 16 — Minimal disk footprint: you ruled the premise, not the mechanism

📄 [`minimal-disk-footprint.md`](../design/minimal-disk-footprint.md) §11 —
**~~OQ-DF1~~ (ruled 2026-08-25) · OQ-DF2 · OQ-DF3 · OQ-DF4**

**This row is the other half of the 📦 disk item below, and it exists because of what OQ-5 did and
did not settle.** You ruled that the cached tars are a **bug** and that yolo may delete them without
`--apply`. That settles the *premise* and the *goal*; it does not say which component does the
deleting, how far into podman's shared image store yolo may reach, or whether *"minimal"* is ever
written down as a number. Each carries stakes, a leaning and an Answer block in the doc.

**One of the four is now closed.** **OQ-DF1** — the retention floor — you ruled the same day:
***"stream, keep zero tars."*** That is what C3 implements (shipped 2026-08-25): on podman the load
path writes no tar at all, `cache/images` stays empty on success, and there is no retention knob to
default. It went past the doc's own leaning, which had asked for an opt-in for the disconnected case.
**What it did not cover, and what is therefore still yours:** the pre-C3 backlog on disk, and Apple
Container, which must keep writing a file because its converters interpolate a path.

**Why the remaining three are not one more thing to get to eventually:** **OQ-DF3 blocks §10's first
step**, which is the best bytes-per-effort in the whole doc and is otherwise unblocked — reclaiming
the superseded podman images yolo's own filter structurally cannot see. It is also **the question
that gates the rule replacing `--keep-images 2`**, which C2 made live for the first time: C2 shipped
the *safety* half (dedup by image ID, plus a liveness veto so `prune --apply` cannot force-remove
another workspace's running image), and `4064f720` then made that veto **fail safe** — an unreadable
load ledger now declines the sweep instead of vetoing nothing. **So OQ-DF3 is no longer asking
whether a veto is needed.** What is unruled is the RETENTION RULE and the REACH into a podman store
shared with your non-yolo work — and the retention half now has a price attached: a coexisting
content-tagged image measures **2.836 GB unique** unless it is a same-store-path re-stream, which is
91.36 kB ([`image-staging-vs-baking.md`](../design/image-staging-vs-baking.md) §1.8). **OQ-DF2** decides which
component does the deleting, which is why the 📦 row below is still scoped to the mechanism rather
than to any number.

### 💬 17 — Mistyped names return `[PASS]`: you ruled the premise, the surfaces are open

📄 [`reference-mismatch-diagnostics.md`](../design/reference-mismatch-diagnostics.md) —
**OQ-RM1 · OQ-RM2 · OQ-RM3 · OQ-RM4** · executes the amended
[`stringly-typed-references-principle.md`](../design/stringly-typed-references-principle.md)

**You ruled the premise on 2026-08-30 — *"it's breaking, so it breaks, what's wrong with that? we're
early, we can break things."*** That settles severity, and it settles it against the position the
code currently holds. What is open is **which surface each check lands on**, which is a different
question and the one the four OQs ask.

**The stake, reproduced rather than argued** — measured in this jail 2026-08-30, `yolo` 0.8.0+614:

```jsonc
{ "agent_profiles": { "cloude": "bedrock" },
  "providers": { "bedrock": { "base_url": "https://user:sk-secret@x.com/v1",
                              "wire_api": "totally-not-a-wire-api" } } }
```

→ **`[PASS] Merged config is semantically valid`**, `30 passed, 2 warnings`. Three defects in four
lines: a profile set for a pack that does not exist, an invented protocol that passes straight
through into `models.json` and `config.toml`, and **a plaintext credential in a git-tracked file**.
Move one typo from a *value* to a *key* in the same file and you get
`[FAIL] config.providers.bedrock.wire_apid: unknown key`. **Field names are enforced; names that
reference a component are not** — same file, same command, opposite outcome.

**And there is a second finding that is worth more than any single check.** `yolo check` has two
diagnostic channels and the summary counts one of them. Measured the same day: three bogus
`env_sources` entries produce **five** `Warning:` lines and a summary that reads **`30 passed, 2
warnings`**. Everything emitted by config resolution and loophole discovery — including the best
mismatch diagnostic in the tree, `unmatchedSupersessions`' did-you-mean — is invisible to the one
line a user reads. **§7 step 1 of the design doc is that fix, it needs no ruling, and everything
else is worth less until it lands.**

**What this row is NOT.** Not a new config key, manifest field, or contribution kind — every name
checked here already exists. Not a re-litigation of `env_sources`' permissiveness, which is correct
and stays (a missing host file is portability, not a typo). Not `pack-fragment` target resolution:
that mechanism does not exist, and the first version of the principle doc's census listed it as a
live test case while [`pack-profiles.md`](../design/pack-profiles.md) cited the principle back as its
authority — two same-day docs each grounding the other in something unbuilt. Both have been
corrected; see [`profiles-as-pack-variants.md`](../design/profiles-as-pack-variants.md) §8.

**Three of the six steps need no ruling and are independent of everything in flight** — the warning
channel (§7 step 1), `pack_profiles` key validation (§7 step 2, and it is the smallest change in the
doc), and the `wire_api` enum plus the `base_url` credential refusal (§7 step 3). The three that do wait are the
supersession relocation (**OQ-RM2**), the skew message's cost model (**OQ-RM3**), and whether
`yolo check` exits non-zero for a reference mismatch at all (**OQ-RM1**, which is
[💬 10](#-10--yolo-check-tells-you-about-the-wrong-machine-in-three-places-and-one-vocabulary)'s
question wearing different clothes — a `check` that passes on a config the next launch refuses).

**The behaviour change with the widest blast radius is the supersession one**, §6 row 4: today an
unmatched `supersedes` warns and the loophole keeps running — the safe direction — and after this it
stops the launch. That is the intended trade under your ruling, and it is the one row worth reading
§6 for before agreeing to the rest.

### 💬 18 — The provider table is checked in yolo's vocabulary and delivered in everyone else's

📄 [`provider-table-fidelity.md`](../design/provider-table-fidelity.md) —
**OQ-PT1 · OQ-PT2 · OQ-PT3 · OQ-PT5** open · ~~OQ-PT4~~ ~~OQ-PT6~~ ~~OQ-PT7~~ ~~OQ-PT8~~ ruled 2026-09-01 · follow-on to
[`profiles-as-pack-variants.md`](../design/profiles-as-pack-variants.md) and
[`zai-plumbing.md`](../design/zai-plumbing.md), both of which shipped 2026-08-29 → 2026-09-01 and are
otherwise sound · continues 💬 **17** §7 step 3 rather than contradicting it

**Review finding, 2026-09-01, against `980aed71`.** The provider/profile machinery is right and its
tests pin call sites, not callees — two production call sites mutated, both failed loudly. What is
wrong sits at the **edges of the abstraction**: a value validated against a set yolo owns, then
handed verbatim to consumers that own different sets.

**The one that puts a wrong value in a file an agent reads.** `knownWireAPIs` is
`{anthropic, openai-chat, openai-completions, responses}` and the derives emit it unchanged into
codex's `wire_api` and pi's `api`. This repo's own source-verified research says codex accepts
**`responses` only** — `chat` was removed from the product
([`local-model-endpoints.md`](../research/local-model-endpoints.md) §"Codex CLI", *verified from
source: codex-cli 0.145.0 binary, 2026-08-20*) — and pi's attested spellings are
`openai-completions` / `openai-responses`. **`openai-chat` is nobody's value.** `18045688` made it
codex's derive *default*, so this is wider than zai: every codex provider that omits `wire_api` now
gets an invalid value where it previously got the only legal one. The commit's z.ai measurement is
correct and load-bearing (`/v4/responses` 404s on both routes); its **inference** is not — that is a
fact about the provider's HTTP surface, not about codex's config vocabulary. The honest conclusion
is that **codex cannot reach z.ai's OpenAI route at all**, which is a fact to record, not a bug to
fix in code.

**The same shape, twice more.** The composed table can hold the `base_url`/`endpoints` pair that
`validateProviders` refuses when a user writes it directly — measured: a user `base_url` over
`packs/zai` sends pi/codex/opencode to the user's URL and claude to the pack's, silently, falsifying
`agentenv.Resolve`'s own comment that the two *"cannot disagree about where a protocol points"*. And
`980aed71` now spells one z.ai URL **twice** in `packs/zai/pack.json` with no test pinning them
equal — the duplication `d1e45e8d` had explicitly declined one commit earlier, for the right reason.

**And the enum is not four protocols but three.** `openai-chat` and `openai-completions` name the
same wire protocol under two agents' spellings, responses carries only codex's, and copilot's
`{completions, responses}` is a fifth dialect nothing models. The set was assembled by collecting
the spellings that appeared in the derives, which is why it validates and cannot translate — there
is no canonical member to translate *from*.

**Four more, sharing no cause — the largest raised in review, not found in the code.** **"Profile"
names three things**: a pack's declared variant, a global free-form mode string any pack may gate on
(`-p`, `ctx.pack_profiles`, the new `config-overlay` gate), and `render.Profile`'s confinement
preset. Only the first has a schema, and **only providers have a user layer** — `pack_profiles` is a
selector, so "take `bedrock` plus one launch flag" has no spelling short of forking the pack.
Measured: `packs/zai`'s own `kind: "profile"` has an **empty body**, its `requires_provider` is
already implied by the provider half, and **deleting it leaves the whole suite green** — and a body
there would have been dead too, because a variant activates only through a CLI its **own** pack
installs, and zai installs none. The `config-overlay` `profile` modifier `568d5a3a` landed gates on
the *target* surface's agent instead, so it is **strictly more reachable than the kind**; zai ships
both and only the modifier does anything, which is what puts OQ-PT8 — *is the kind just sugar?* — on
the table. Then: a
`providers: {"bedrock": null}` — the documented opt-out — refuses a `claude` launch outright
(measured); `--profile` means startup timing *or* a pack profile depending on the next token, after
two commits patching that parse; and the census — this work made `zai` the **twelfth** pack and the
first that installs no CLI *and* ships no loophole, while `AGENTS.md:8` still says ten.

**What needs no ruling, and is worth doing regardless:** the missing integration test — nothing in
`integration/` mentions `zai`, `pack_profiles` or `providers`, though the pattern exists and **an
in-jail assertion on the rendered `~/.codex/config.toml` is the single check that would have caught
the headline defect**; and the census lines (`AGENTS.md`, `packs/embed.go`, `USER_GUIDE.md:217`).
The doc's §9 puts the test first deliberately: it fails today.

**What this row is NOT.** Not a retraction of either parent doc — the credential boundary, the
three-level skew handling and the backend-parity repairs all re-measured clean. Not a proposal to
reopen the enum, which would restore what 💬 **17** §7 step 3 closed. Not a new contribution kind:
the `config-overlay` `profile` gate `568d5a3a` landed is the right mechanism, and OQ-PT3 asks only
whether it may carry the placeholder vocabulary `env_shape` already has.


### 💬 19 — A catalog and a selection are two features, and only one of them ships

📄 [`provider-catalog-and-selection.md`](../design/provider-catalog-and-selection.md) —
**OQ-CS3 · OQ-CS4 · OQ-CS5** open · ~~OQ-CS1~~ ~~OQ-CS2~~ ruled 2026-09-01 · sibling to 💬 **18**, which reports defects in the same machinery ·
splits what [`zai-plumbing.md`](../design/zai-plumbing.md) §5 assumed was one thing

**The maintainer's own framing, 2026-09-01:** *"populating a directory of providers in an agent
config"* and *"starting an agent using a specific profile"* are two features, and yolo mixes them.
They are — and separating them shows the second is largely **unbuilt**.

**Two of the three questions were ruled the same day.** *"Activating a profile should work for all"*
takes option D (selection written into each agent's own key), and *"default can be left to the
specific agent"* settles that yolo writes nothing when no profile is active. A third ruling landed
in 💬 **18**: *"pack presence means in the dictionary, which also means fatal errors if no API key
found"* keys the credential requirement to **catalog membership** instead of the pack declaration —
which dissolves OQ-PT4 rather than answering it, since a `null`-dropped provider then stops being
required. **And a fourth ruling defined the word.** *"That's what I want a profile to be. User declared, user
intent … and the config surface of a profile needs to be defined by the provider."* A **profile** is
a named selection over a provider, written in user config, whose legal tunables the provider
defines — one meaning, in one place, and the one a user already assumes when they type `-p zai`.
That closes 💬 **18**'s OQ-PT6, OQ-PT7 and OQ-PT8 together: the pack-variant body is not a profile
at all but contributions gated on a profile name, so it moves to the `profile:` modifier — which
also repairs the measured defect that a variant body is **unreachable** for a pack installing no
CLI. `kind: "profile"` shrinks to name + provider.

**What remains is OQ-CS3 (which model a selection picks), OQ-CS4 (is the profile's field set derived
from the provider's shape or declared by it — I lean derived, no schema), OQ-CS5 (where user
profiles live, and at what scope — I lean a `profiles` key, user-scope only, for the reason `packs`
is), and the research gap below.**

**Measured in a live jail today**, `packs: ["claude","zai"]` with `providers.zai` set:

| Agent | Catalog | Selection key | Set? |
|---|---|---|---|
| pi | `providers: {bedrock-mantle, zai}` | the file has only a `providers` key | **absent** |
| opencode | `provider: {zai}` | top-level `model` | **absent** |
| codex | `[model_providers.zai]` | `model_provider` / `model` | **absent** |
| claude | *(no catalog)* | `ANTHROPIC_BASE_URL` + token | **set** |

So *"what do pi and opencode do if no provider is specified?"* — they fall through to their own
built-in default and the `zai` entry sits in the directory unused. **`-p zai` changes the behaviour
of exactly one agent of four**, while [`zai-plumbing.md`](../design/zai-plumbing.md)'s stated want is
*"`-p zai` works anywhere; every selected agent fires at z.ai"*. The catalog half works everywhere;
the selection half is claude-only, and nothing prevents fixing it — the derives are ordinary Lua in
the packs that own each agent.

**And the disable complaint falls out of the same conflation.** A provider entry can only express
catalog presence, so "off" can only mean "absent", the only absence-maker is `null`, and `null`
replaces the settings it disables — with no alternative, since `knownProviderKeys` is closed and
`enabled: false` would be refused as an unknown key. Once the two features are separate the want is
trivial: the entry stays, nothing selects it.

**Step 1 needs no ruling and blocks the rest:** the doc's §3 per-agent selection table has one row
empty (pi — its `models.json` carries only `providers`, and where pi records "the model I use" is
not established) and one inferred rather than source-verified (opencode's `"<provider>/<model>"`).
That is the same unverified-vocabulary class 💬 **18** exists to report, so this doc writes the rule
down before building rather than after.

**What this row is NOT.** No new vocabulary — two words, **catalog** and **selection**, both already
meaningful; the "mode" coinage floated in 💬 18 is withdrawn. No new config key in the recommended
option, which writes into keys the agents already define. No change to `kind: "provider"`, the
composition, or the credential boundary — that is 💬 18's territory.


# 📦 Up next

**Three items.** 💬 6's four rulings (2026-08-25) unblocked **C2** and **C3** out of
[`image-staging-vs-baking.md`](../design/image-staging-vs-baking.md) §11 and created the
disk-footprint item the maintainer asked for. **C2 and C3 shipped in `be7b8591`, 2026-08-25**, the same day they
were queued, and left this file under the rule at the top of it. **C4 and C5 are deliberately NOT
here**: OQ-1 ruled their *shape* — opt-in fast path, baked path retained as fallback — and left the
go/no-go gated on §11 step 5's re-measurement. **That measurement has now been taken** (§1.8,
2026-08-25), so what stands between C4 and this section is no longer an observation — it is your
call. Queueing it before you make one would still be queueing a question.

**Ordering basis — this section has never stated one, so:** what unblocks the most other work first,
then what is cheapest. Of the image/disk sequence, what remains is the **retention rule (R3)**, still
gated on **OQ-DF3**, and the reclamation half of the disk row — the two are one decision and should
be made once. Program delivery is last, not by importance: nothing else in this file waits on it, and
it is the larger of the two.

- 📦 **Minimal disk footprint — OQ-5's ruling, which is broader than the tars.** 📄
  [`minimal-disk-footprint.md`](../design/minimal-disk-footprint.md). The ruling is *"bug, for sure …
  I see no reason to keep any of this around … we need to use minimal disk space"*, and the
  sub-question *"may yolo delete cached tars without `--apply`"* is answered **yes**. It is
  deliberately **stronger than the doc's own leaning**, which asked only for an automatic keep-N
  sweep.

  ⚠ **The premise is ruled and OQ-DF1 with it; the rest of the mechanism is 💬 16** (`OQ-DF2 ·
  OQ-DF3 · OQ-DF4`). **The streaming half of §10 step 3 (A4) has SHIPPED** as C3 under the OQ-DF1
  ruling *"stream, keep zero tars"* — podman writes no tar, so that half is off this row. What is
  left and does not wait on a ruling is **§10 step 2 only: close the tar-eviction race** (P4) before
  anything becomes automatic — now an Apple-Container-side exposure on the launch path, since podman
  has no file between the two steps to race for. **Delete-on-success (A3) is OQ-DF2 option (i)**, the
  component that does the deleting is OQ-DF2's, and the podman-store reach is OQ-DF3's, so **do not
  start there**. The pre-C3 backlog is also still untouched by design: C3 stopped the creation, not
  the accumulation already on disk.

  **The stake, re-measured here 2026-08-25 — still the largest fully reclaimable artifact in the
  repo, and bigger than the number the ruling was made on.** `cache/images` in this jail's state dir
  holds **148 tars / 480.71 GiB** (§1.6 measured 125 / 404.4 GiB ten days earlier); the **host-side
  cache §1.6 never looked at** holds another **36 / 125.41 GiB** — **606.12 GiB across 184 tars**,
  every byte of it regenerable. `/nix/store` is at **659 GiB** (`du -sh`), and the root device
  carrying store, home and workspace is **84 % full with 608 GiB free**. Arithmetic rather than a
  fresh observation: against the 69 % recorded on 2026-08-15 that is **~11 days of headroom** at the
  ten-day rate, ~14 at the 34-day one — which is what *"I will be addressing this issue soon"* is
  about. **One machine, one jail** — the same caveat §1.6 records as its R7.

  **My one addition to the ruling's framing:** at 659 GiB against the tars' 606 GiB, the store is now
  the larger raw line item. It is *not* the larger **reclaim** — most of it is pinned by the durable
  §1 GC roots doing exactly their job — but a doc that scopes itself to the tars will reach
  *"minimal disk space"* and stop short of it. [`storage-lifecycle.md`](storage-lifecycle.md) already
  marks the store **UNBOUNDED** (`:132` — `min-free = 0`, `max-free = MAX`, no yolo GC root, no yolo
  GC), and its `min-free`/`max-free` remedy at `:235` is the **only unchecked box in that whole
  file** — host-owned, because yolo must not edit host `nix.conf`.

  **Where the shipped work stops is the whole gap.**
  [`storage-lifecycle.md`](storage-lifecycle.md) §1–§4 made a GC **safe** — a durable per-image GC
  root, fail-safe reaping, a bounded opt-in `--nix-gc` — and never made anything **automatic**, never
  lowered a retention default, and never touched this artifact class. *"Nowhere near enough"* is
  precisely that distinction.

  Three things the design doc has to reckon with, all verified here 2026-08-25:

  - **Nothing in `internal/prune` has an automatic caller.** Every one of its reclaimers is reached
    only from `internal/prune/prunecmd.go`, and `prune.Run` only from `internal/cli/commands.go:240`
    via the CLI dispatch table. No timer, no hook, no launch-path call, no `just` recipe, and zero
    integration coverage. The 20 GiB hint (`prunecmd.go:209`, fired at `:274`) is §1.6's *"twenty-four
    days and 395 GiB"* sentence with ten more days on it — **34 days and ~471 GiB** since the
    2026-07-22 baseline of 9.5 GiB, with no prune. It **used** to advise relocating to HDD, which is
    the advice this ruling retired; `4064f720` replaced it the same day, and the hint now printed
    (`prunecmd.go:281-284`) says these tars are a legacy backlog that `yolo prune --apply` reclaims.
    **The wording changed and the number did not** — which is this bullet's point, not a footnote to it.
  - **podman's own image store is the ledger with no working reclaimer for a nameless row.**
    `PruneOldImages` passes `yolo-jail` as a **name filter** (`internal/prune/probes.go:265`) and an
    orphaned image is *untagged*, so it never matches. Measured pre-C2 in this jail: **2 images,
    6.391 GB, 100 % reclaimable**, of which yolo could see one. Cheap and independent of the rest — and
    the reason **OQ-DF3** is 💬 16's sharpest item: the fix is ready except for how far into a shared
    podman store yolo may reach.
    **C2 changed the shape of what that filter returns** (one row per NAME, one permanent tag per
    config) and therefore armed a pass that had never fired; the SAFETY half landed with it — dedup by
    image ID plus a liveness veto (`internal/prune/probes.go:211-256`, made fail-safe in `4064f720`) — but not the NUMBER, which is
    still OQ-DF3's. Re-measured here after C2 (`podman system df -v`): **eight rows over seven
    images** — four permanent content tags, `:latest` aliasing the newest, and **three** nameless
    rows, of which **exactly one predates C2** (`f3f0380b0645`, whose `NamesHistory` is `:latest`
    and nothing else). The other two were orphaned *after* C2, by two mechanisms that are both still
    live: a `:latest` move (`pointLatestAt`) and a same-store-path re-stream. What C2 stopped is
    narrower than "orphaning" — an image whose *only* name is `:latest` can no longer come out of
    the normal load path. So C2 shrank the leak and grew the retained set at the same time, which is
    exactly the trade OQ-DF3's number has to price — and the degraded fallback still manufactures
    the old shape, which that number has to price too.
  - **The largest artifact class is the least tested.** There is no `imagecache_test.go` and no
    `shadowed_test.go` in `internal/prune`, so `PruneImageCache`'s keep-N eviction branch — the only
    thing that has ever deleted a tar — is executed by no test. Three `Run` call sites are unpinned
    entirely (hardlink dedup, host-render archive, image GC roots) and four are header-only: the
    AGENTS.md *"does it fail if I delete the call site?"* shape, in the package about to be rewritten.

- 📦 **Program delivery §10, the remainder.** Its ten questions were all ruled on 2026-08-24, and four
  steps shipped the same day (`a16403e2` no more per-launch mise upgrade + committed `mise.lock` ·
  `0a4d241c` unknown-`via` tolerance · `af46c9b4` install receipts + the boot orphan catalog ·
  `28ddea11` the briefing-from-applied item that used to be this section's sole occupant). 📄
  [`program-delivery.md`](../design/program-delivery.md) §10 — in its own order:
  the **reconcile** (generalise the LSP sentinel: compare receipts + locks against disk, offline,
  report only — it also inherits the "newer version available" channel OQ-PD8 found dead);
  the **user-scope gap receipt** in `packs.lock.json` (the scope split's second half — the
  workspace JSONL that shipped is the observation log, not the pin); the **removal act** plus the
  default-off autoprune option (the catalog half shipped and promptly found three orphans nobody
  knew about); **obey** (install honors the receipt — OQ-PD6/PD7's wiring); and last, the
  **installer capture** (§6.3, OQ-PD10 — an ephemeral jail as the AUR chroot).

**What refills this section:** an answer in 💬 — **and both rows still here are what that looks
like.** The disk row is OQ-5's ruling executed; program delivery is what its own ten rulings left
over on 2026-08-24. This section held **four** rows when 💬 6 was retired hours earlier: **C2 and C3
were two of them and shipped the same day they were queued**, so they left under the rule at the top
of this file. A section that shrinks from four to two in a day is that rule working, not a queue
that stalled. Row **2** is now the remaining 💬 that would unblock buildable work the day it is ruled
on: trust-paths has TP4's pin and TP7's catch-up.

> [!NOTE]
> **The queue emptying on 2026-08-24 was not the interesting part; how it refilled is.** Nothing in
> 💬 was answered that day — an outside report arrived, and auditing the class behind it produced
> seventeen defects and one buildable item. That is a real limit of a decision-shaped roadmap: it
> tracks what I am waiting on you for, and cannot surface what nobody has looked at yet. If the queue
> is empty, the question is not "what should we build" but "which of the fourteen rulings is actually
> blocking, and which is a question I invented" — see
> [`further-roadmap-ideas.md`](further-roadmap-ideas.md) §4 and §4a, which argue **three** of them
> are the latter.

---


- 📦 **Scoping pack content to the agents it is for.** 📄
  [`briefing-audiences.md`](../design/briefing-audiences.md) — **DECIDED 2026-08-31, all seven
  questions ruled**; reuses [`profiles-as-pack-variants.md`](../design/profiles-as-pack-variants.md)
  §2.5's CLI-name namespace and R2 of
  [`stringly-typed-references-principle.md`](../design/stringly-typed-references-principle.md).
  A pack's briefing prose and skills reach **every** agent with no way to say who they are for:
  prose is composed once and written to every destination
  ([`prepare.go:154`](../../internal/cli/run/prepare.go#L154), before the per-destination write
  loop at [`:170`](../../internal/cli/run/prepare.go#L170)), and a pack declaring
  `into: ".claude/skills"` still merges into `.pi/agent/skills`
  ([`mergedest.go:74-76`](../../internal/packload/mergedest.go#L74-L76)). Both outcomes are live
  here — `matt-fzf`'s `fileSuggestion` prose was **deleted** rather than scoped, and
  `matt-local`'s Pi Agent Config section is **broadcast** to four agents that cannot act on it.
  The fix is an optional `agents` selector on both kinds, naming **launcher commands** (not the
  pack slug); the destination **declares** its own `agent` rather than anything being derived; a
  name has **one owner across all kinds**, since `claude-official` and `claude-matt-fork` both
  launch as `claude` and cannot both be enabled; naming an agent this jail has not enabled is
  **fatal**, with no laxer tier and no denylist; and a content pack names its audience, **never a
  path**. Cheap half first — `ComposeHostBriefings` is already per-destination and takes a filter
  ([`hostbriefing.go:133-172`](../../internal/entrypoint/hostbriefing.go#L133-L172)) — and the
  jail half's composition move **lifts a limit already recorded in the tree**
  ([`briefingsource.go:106-108`](../../internal/packload/briefingsource.go#L106-L108) names
  per-destination composition as the fix it declined to build). **Review the path-free half
  hardest** (§9 step 3): `declares`
  ([`mergedest.go:141-148`](../../internal/packload/mergedest.go#L141-L148)) returns true for
  *any* contribution of the kind, so an `agents`-only briefing that misses that change skips
  destination inference and **delivers nowhere**, silently.

# 🔒 Waiting

- 🔒 **The macOS nightly is GREEN, and the warmup that burned twelve minutes a night is GONE.**
  *(This headline said the waste was ongoing until 2026-08-23; the body below it says otherwise, and
  the body is right — `e5b60902` removed it the same day.)*

  Measured on run `32623453131` (2026-08-23, commit `ae0fa1a5`, which was `main` at the time and is
  **116 commits back** as of this edit):
  `build-image` **success**, `integration-macos` **success**, suite `ok … 3915.734s`. The night
  before (`32557449248`, `cb966c27`) it was `FAIL … 5073.141s`. **This row replaces the 🛑 entry,
  which was wrong about which test was failing and about what could fail at all** — the section is
  now empty and gone.

  **What the 🛑 entry got wrong, measured rather than argued.** It blamed `TestAgentToolsAvailable`
  timing out at 20 minutes. That test **cannot** time out on this job: it is gated by
  `requireRealPackInstalls` (`integration/harness_test.go`), and `YOLO_TEST_REAL_PACK_INSTALLS` is
  set **only** in `packs.yml` — so on both nightlies it reported `--- SKIP … (0.00s)`, as did
  `TestPackInstallsVersionsAndConfigures`, the neighbour the old entry reasoned from. The real
  08-22 failure was `TestExtraPackageLibFarm`, at **1216.11s against the job's 1200s cap**, and it
  was the only one.

  **The budget half is fixed, and proven on the platform that broke.** `01a51dc4` gave both
  `packages:`-setting tests an explicit 40-minute `withTimeout(nixBuildJailTimeout)`
  (`integration/packages_test.go`), and both passed last night: the lib farm at **812.37s** (683s →
  1059s → timeout → 812s) and `TestDevPackageLinksRuntimeLib` at **310.15s** (256s → 345s → 641s →
  310s). Their cost is a real `--impure` nix image build, not misattributed warmup.

  **The warmup half is fixed too, and by deletion rather than by a bigger number** (`e5b60902`,
  2026-08-23 — later the same day this row was first written, which is why the paragraph it replaces
  said the opposite). The twelve minutes were never mysterious once the premise was checked: a
  warmup exists to pre-pay a **container start**, and on darwin every launch **realises an image**,
  because a loaded image can never match a darwin `nix eval`. So the warmup was a full nix build
  wearing a warmup's name. `warmJail` now returns early on `GOOS == "darwin"` with that measurement
  in the log line, and the first container test absorbs the one-time cost instead
  (`integration/harness_test.go:159-165`). On linux CI the premise holds and the warmup keeps
  earning its 1m56s.

  **What is left is one observation, not an investigation.** Nobody has watched a nightly run
  *with* the skip in place: the expectation is `integration-macos` losing ~12 minutes of wall clock
  and the first container test growing by roughly the image realisation. Needs the nightly; nobody
  here has an Intel Mac. **If the next green run shows that shape, this row leaves the file.**

  *(Why the predecessor entry took 29 nights to be worth writing down, and why the paragraph above
  refuses to guess: the recorded diagnosis — "nix is broken on that runner and not in our tree" —
  was exactly backwards and had never been measured. It was our flake, on every Intel Mac,
  reproducible in 0.2s with `nix eval .#installPrefix`. Four nights were spent re-triggering a run
  that could not have passed. The warmup ceiling was the same question asked again — and it answered
  the same way: the fix was not a bigger ceiling but noticing the premise did not hold on that
  platform. **Twice now the measured cause has been in our tree while the recorded one blamed the
  runner.**)*

- 🔒 **The fatal witness is live in the tree, and not on your host until a `just load`.**

  Since 2026-08-18 an enabled jail-facing service the jail cannot use **refuses the launch**, in all
  three fault classes. Your jails keep the old warn behaviour until the image is reloaded, so this is
  the moment to know what changes:

  - **A dead broker singleton refuses every jail on the host**, not just one — its endpoint variable
    is wired with no publish gate, which is deliberate and was accepted. This is the release-note
    line.
  - `YOLO_ALLOW_UNREACHABLE_SERVICES=1 yolo …` is the way past any refusal, and the refusal names it.
  - **If you want it back as a warning, it is one boolean.** `reachabilityFatal = true`
    (`internal/entrypoint/reachability.go:106-119`) is OQ-R2's flip, deliberately isolated so the
    severity can be reversed without touching the witness. Verified 2026-08-23.
  - `unsupported`, `unknown` and an absent disposition **never** refuse — a host yolo could not ask
    is never punished for what it cannot help.

  Nested launches were refused by this and are not any more (measured both ways, 2026-08-18); the fix
  is at the promise rather than the severity, so the host case above is untouched. 📄
  [`loopback-tls-reachability.md`](../design/loopback-tls-reachability.md) §7.

- 🔒 **The slirp4netns fallback is built, and it is two flags rather than one.** An old-passt host now
  falls back instead of merely being told why it cannot work — but only when **podman itself** reports
  a slirp4netns binary, never a PATH lookup, because podman is what execs it.

  Building it corrected the design: `allow_host_loopback=true` alone does **not** fix yolo, because
  podman aims `host.containers.internal` at the host's *global* address under slirp4netns. It needs
  `--add-host=host.containers.internal:10.0.2.2` alongside — measured here with bare `podman run`. The
  **pre-existing slirp4netns-host arm had the same defect** and was shipping the option alone while
  reporting `requested`, i.e. claiming a fix that never reached the advertised name; both arms now
  emit the pair. **Unverified on a real old-passt host** — nobody here has one. 📄
  [`loopback-tls-reachability.md`](../design/loopback-tls-reachability.md) §3.2.1.

- 🔒 **Developing yolo-jail inside its own `macos-user` sandbox: measured 2026-08-19, and the
  sandbox half is DONE.** The motivating ask was a host-side jail good enough to work in without
  approving every command. Measured by extracting the profile a real `--dry-run` emits for the Mac
  checkout and running the actual work under `sandbox-exec`:

  ⚠ **Everything in this row is about THE MAC.** "The host", "your config" and "your home" below
  all mean that machine, and none of them describe the Linux host you work on daily — whose config
  is on the current `packs` key and whose `yolo` is current. Said explicitly because the scope
  used to live only in this heading, and a reader arriving at item 1 read a Mac-only blocker as a
  statement about whatever machine they were on.

  - ✅ **`go build ./...` and the full `go test -short ./...` — all 58 packages — pass**, as does
    `just test-fast`. `git status`/`log`/`ls-files` work; nix reaches the daemon (`Trusted: 1`).
  - ✅ **The isolation holds where it matters.** Host SSH keys, `~/.claude`, `~/.aws`, `~/.dotfiles`,
    `/Library/Keychains` and the login keychain are all `Operation not permitted`; writes outside the
    workspace are refused. Network is open, by SandVault-parity design.
  - ✅ **One real profile bug found and fixed** (`2e327fa2`): intermediate workspace dirs were denied,
    so `git ls-files` could not walk to the repo boundary and `just format` died claiming
    `Invalid path '/Users/Shared/yolo'`. See the Mac row below for why it stayed hidden.

  **Two things stand between that and a working `yolo -- claude` ON THE MAC, and only the first
  needs you:**

  1. 💬 **The Mac's `~/.config/yolo-jail/config.jsonc` still uses the REMOVED `agents` key**, so
     *no* current yolo launches on that machine, on any backend — `yolo check` and every `yolo` run
     refuse with the config-invalid fatal. All four names (`claude`, `pi`, `codex`, `agy`) exist as
     packs, so the fix is renaming the key to `packs`; nothing else in that file needs to change.
     **Not applied — your config, your call.** (The Mac's installed `yolo` was **531 commits stale**
     at 2026-08-19, which is why the refusal has not surfaced there in daily use. The Linux host is
     unaffected on both counts — measured 2026-08-23 from a running jail's inherited config
     snapshot: `packs` key, `yolo` at `0.8.0+412.gaef73cce`.)
  2. **A toolchain has to come from `packages:`, not the Mac's home.** `deny file-read* (subpath
     "/Users")` blocks `~/.local/share/mise`, so that host's mise-shimmed `go`/`just` are invisible
     inside the sandbox — correctly. Everything above was measured with `go`, `just` and `git`
     realized from nix, which is exactly what `packages:` materializes
     (`yoloNoncontainerPackages`); the Mac checkout's workspace config declares only `just` today,
     so `go` and `git` need adding. Not a gap, just unconfigured.

  Cost of entry: one sudo password per launch, prompted inline through the TTY proxy — not per
  command. **What is NOT yet proven end-to-end** is the launch itself (the `sudo -u _yolojail` +
  bootstrap path); the Seatbelt confinement is proven, the user-switch around it is not, and that is
  the remaining item in the row below.

- 🔒 **On a Mac — three things need the hardware, and the first is a config rename.** *(The
  headline used to announce what had LEFT this row, which tells a reader nothing about what is in
  it. For the record: the two lib-farm assertions were never darwin assertions — they failed
  because the image build did, and both went green the moment `x86_64-darwin` could evaluate again.
  Nothing about the lib farm was wrong.)*

  Three items remain, all genuinely host-gated — and **before any of them, that Mac's config still
  uses the removed `agents` key, so no `yolo` launches there at all** (see the sandbox row above).
  The three: the `macos-user` acceptance matrix, Track D4's download proof, and the guest-notch
  handoff (whose §2 item 1.4 — do packs reach a macos-user sandbox? — is still the first thing to
  run there). 📄
  [`handoff-guest-notch-macos.md`](handoff-guest-notch-macos.md). **Item 1.4 is now half-answered:**
  the sandbox can read the staged pack root and run the toolchain, so what is untested is the
  `sudo -u _yolojail` staging step above it, not the confinement.

  **What a Mac session on 2026-08-19 did settle, beyond the nightly:** `go test -short ./...` had
  **two** failures no Linux run could see, both now fixed — the GNU-`stat` throttle above
  (`c411650`, a real `macos-user` defect) and a `yolo ps` runtime-default assertion that hardcoded
  Linux's answer (`a35f8c7`, test-only; the resolver was right). `ci.yml`'s `check-macos` job was red
  on `main` for the second of those.

  **And a third, which was neither macOS-specific nor a flake** (`b23c95c2`): `TestNoTruncationRace`
  was red on `main` on BOTH Linux and macOS at a flat ~30.8s, and the cause was a real daemon bug —
  `journald.Serve`'s stop watcher unlinked the socket in a goroutine **nothing waits for**, so a
  caller re-serving the same path had its new socket file deleted by its predecessor and every dial
  failed forever. Three things about how it hid are worth carrying forward:

  - **`GOMAXPROCS=1` is the variable a fast dev box hides.** It passed here `-count=3` and reproduced
    3/3 the moment the scheduler was pinned to one thread. Reach for that before calling a
    CI-only failure a slow runner.
  - **A green test that never ran is not evidence.** `18f2330` removed this test's `-short` skip, and
    every recipe in this repo passes `-short` — so it had never executed in CI in its life. Nothing
    regressed on 08-17; a latent bug became visible. That commit's own thesis ("they read as coverage
    and were not") landed on the commit itself.
  - **The readiness budget had already been raised 5s→30s for this same symptom.** Raising a timeout
    is what you do to a slow test; doing it twice is a signal you are looking at the wrong layer.

  **And a fourth, from the sandbox measurement above** (`2e327fa2`): the Seatbelt profile granted
  `/Users`, `/Users/Shared` and the workspace subpath, and its comment asserted "the workspace is NOT
  under any `/Users/<name>` home, so **no ancestor grant is needed**". That is true only at depth
  ONE — and the shipped test used `/Users/Shared/proj`, the single depth where the gap is invisible,
  while *asserting the absence of an ancestor grant as if it were the invariant*. A real workspace at
  `/Users/Shared/yolo/yolo-jail` therefore left `/Users/Shared/yolo` denied. **The same test-fixture
  lesson as the entry above, on a different mechanism: a fixture chosen for convenience picked the one
  input that cannot fail.** Worth noting how it presented — `git ls-files` walks up for the repo
  boundary and reported `fatal: Invalid path …`, i.e. a *broken repo*, and gofmt then failed on the
  empty list. Two errors, neither naming the sandbox.

  Also declared while fixing the socket bug: `t.TempDir()` embeds the test's own name, and macOS's 104-byte
  `sun_path` left that test **14 bytes of headroom** — a rename could have spent it and produced
  `bind: invalid argument`, which is what a socket test looks like when it is really measuring the
  length of its own identifier.

- 🔒 **On an NVIDIA host** — `sectionGPUNvidia` has no in-jail guard while its AMD twin guards both of
  its checks, so a jail with `gpu.enabled` prints three `[FAIL]`s for host facts read from the wrong
  side (`nvidia-ctk not found`, `runc not found`, `No CDI spec found`). Not uniformly wrong, which is
  why it is here rather than fixed: `nvidia-ctk` *does* inject `nvidia-smi` into a passthrough
  container, so the enumeration rows are a legitimate in-jail check while the toolkit/runc/CDI rows
  are not. Deciding which rows to guard needs a host with a card.
  `internal/cli/check/sections_devices.go:38`.

---

# 🧊 Icebox

- 🧊 **Cache relocation's three held questions — `OQ-CR1 · OQ-CR2 · OQ-CR3`** (named 2026-08-23; the
  row said "two" and there were three). Genuinely undecided whether we want the feature, not merely
  unscheduled. **CR1 gates item 11** (`yolo cache relocate`), and **CR2 is the same decision seen
  from the host side** — the doc says so itself, so answering CR1 alone answers half a question.
  📄 [`cache-relocation.md`](cache-relocation.md).

  **OQ-5's ruling costs this row one of its two motivating consumers, and nothing else.** Relocating
  `cache/images` to a spare disk was the second one, and it is what `yolo prune`'s own hint used to
  recommend — **no longer**: `4064f720` retired that advice on 2026-08-25, and the hint now printed
  (`internal/prune/prunecmd.go:281-284`) tells you to reclaim the backlog instead. Under *"I see no
  reason to keep any of this around"* the right verb for a **regenerable, write-once, read-once**
  artifact is **delete**, not **move**, and the shipped hint now says so. What is left is `huggingface` — **185 GiB of the 241 GiB that prompted the feature**
  (`cache-relocation.md:17-18`), cold and keep-forever, where relocation is the only lever. The
  abstraction-level question and the threat model are untouched by the ruling.
- 🧊 **Boundary broker B2** (approval-gated host credentials) — waits on nix OQ-1 and auth OQ-1, and
  the second **resolves by an experiment nobody has run**: point Claude Code at a non-Anthropic base
  URL with a subscription OAuth token in place and watch whether it sends the bearer. ~5 minutes.
  **This is the cheapest thing in the whole file** — it is the only 🧊 item that a measurement, not a
  ruling, can move, and it also unblocks 💬 3's OQ-1. 📄
  [`agent-auth-modes.md`](../design/agent-auth-modes.md) §10.

---

# Open threads

### Emptying `bundled_loopholes/` — **done 2026-08-19**, and what it left behind

The goal was **no inhabitants at sprint end** (OQ-BP4), and the channel is gone rather than emptied:
the directory, its `embed.go`, `internal/loopholes/embedfallback.go` and every reader of them are
deleted. All **five** loopholes are pack contributions now, `loopholes.ReservedLoopholeNames` and
`paths.BuiltinLoopholeNames` are deleted whole, and **core's config schema names no loophole at
all** — which was the point of the exercise rather than a side effect.

**OQ-LP14 is settled too, and by the better of its two answers.** It became a hard dependency the
moment the goal grew from one loophole to the whole channel, and it closed on 2026-08-18 by
**withdrawing the bind-host path rule rather than adding vocabulary for a runtime-dir socket** — a
rule that admitted `~/.ssh` while refusing `${XDG_RUNTIME_DIR}/pulse/native` in every spelling. The
two audio loopholes then merged into `packs/audio` under the plain name.

One residue remains, tracked above rather than here: the **binary capability** OQ-BP1 promised
alongside the move (💬 **14**).

📄 [`broker-as-a-pack.md`](../design/broker-as-a-pack.md) §13 is the measured account of what
"empty the channel" actually required; its Decision Ledger holds BP1–BP4.

### What the roadmap does not cover, and deliberately

Ideas that are not yet anybody's decision — the ones that would become 💬 rows if you wanted them —
live in [`further-roadmap-ideas.md`](further-roadmap-ideas.md), not here. That file is a **source of
candidate work, not a queue**: nothing in it is committed, and it says which of its own entries it
would drop.

**And some live questions are deliberately not rows**, because a row is a decision you are being
asked to make and these are not blocking anything:

- **OQ-ACP1 … OQ-ACP4** in [`agent-config-packs.md`](agent-config-packs.md) — that proposal was
  largely overtaken by what shipped (the `packs` key, host fetch, the lockfile, the origin gate), so
  what survives is four genuine but unpressing questions: two people attaching to one jail with
  different pack sets, opencode's skills gap, the prism as a standalone tool, and whether pruning
  needs telemetry.
- **CFP-1 … CFP-3** (`composed-file-permissions.md`), **SS-6** (`jail-state-separation-design.md`)
  and `host-render-target.md`'s three §9 questions — named 2026-08-23 so they are countable. All
  concern shipped mechanisms working as designed, not gaps.
- The **research** docs' questions — **OQ-LM1 … OQ-LM6** in `local-model-endpoints.md` — are
  exploratory rather than blocking. *(`mise-host-jail-path-mismatch.md` is now CLOSED: its last open
  question had already shipped as `venvShadowMountArgs`, and re-reading it is what surfaced a trap
  documented nowhere else — a per-side path that is a symlink or a regular file cannot be shadowed,
  so the launcher warns and the jail silently sees the host's copy.)*

Plus the **OQ-GN1 … OQ-GN4** in the guest-notch handoff, which are Mac-gated rather than
undecided, and are cited from 💬 7 above.

They are named and countable now, which is the point — a question with an ID can be promoted to a
row the day it starts blocking something, and demoted the day it stops. **That is the whole
difference between this list being 14 rows and being 104.**
