# Roadmap

**Status: 13 needing you · 5 ready · 0 in progress · 6 waiting · 0 broken · 3 icebox.**

Last updated **2026-09-02**. Counts tallied from this file, not asserted — one per `### 💬` heading,
one per top-level bullet in every other section, and each bullet's glyph matches the section it is
in.

> [!IMPORTANT]
> **A full-corpus audit on 2026-09-02 re-verified every row against the tree, and four 💬 rows left
> the file without needing a ruling** — three because the code had already answered them, one
> because the measurement finally got taken:
>
> - **💬 9 (config snapshot's migration window)** — closed in code since 2026-08-29:
>   [`scoped-config-approvals.md`](../design/scoped-config-approvals.md) OQ-S3 (`27b335ce`) makes a
>   fresh non-empty workspace prompt, which deletes the window OQ-D3 was about. Three docs said
>   otherwise; all three now agree with the tree.
> - **💬 3 (auth mode)** — the doc was rewritten and DECIDED on 2026-08-29 and the roadmap kept
>   citing OQs the rewrite deleted. The one live survivor — *does Claude Code send a subscription
>   OAuth bearer to a non-Anthropic base URL?* — **was measured today: YES**, 8/8 requests to a
>   loopback probe carried `Bearer sk-ant-oat0…`
>   ([`agent-auth-modes.md`](../design/agent-auth-modes.md) §8.1). That both unblocks
>   boundary-broker B2's mechanism and turns "a config-writable base URL is credential-sensitive"
>   from caution into measurement. The AWS credential-pair gap (OQ-9) survives as a named,
>   non-blocking question in that doc.
> - **💬 4 (non-container nix)** — nix OQ-1 (*run vs. configure at `host`*) is **answered by
>   events**: `yolo host -- <cmd>` shipped 2026-08-30 with a fully composed launch env, and the
>   provider-catalog rulings made the host notch run the env derive as a constraint. Recorded in
>   that doc's ledger; its six remaining questions block nothing and are listed at the end of this
>   file.
> - **💬 12 (OQ-CAP + OQ-B)** — OQ-CAP was a confirmation of a decision the code had already built
>   and pinned (`supersedes` top-level; the alternative is test-refused), now compacted into
>   [`pack-capabilities.md`](../design/pack-capabilities.md) §10 — and on 2026-09-02 the "one-line
>   residue" this row queued turned out to be shipped too, since `d776c902` (2026-08-15): the audit
>   grepped the CLI for the word *supersede* and missed a generic renderer. OQ-CAP is closed with
>   nothing owed; a surface test now pins it. OQ-B folds into 💬 8, whose row already said the four
>   `0o444`-vs-`:ro` instances are one decision.
>
> **The same audit found the census had a 26th row nobody had written** — pack-shipped `derive.lua`
> runs ungated for every origin, in-jail and now at host launch with real credential-bearing inputs
> — filed as [`trust-paths.md`](../design/trust-paths.md) row 26 + **OQ-TP8** (💬 2). And 💬 17 is
> **four-sixths shipped** by the provider arc, so its row shrank and its no-ruling step is queued
> 📦 below.
>
> *(The 2026-08-25 image-staging rulings that used to fill this box are done history except two
> residues: OQ-DF3 still gates the retention rule — 💬 16 — and the C4/C5 go/no-go is now an
> explicit 🧊 row instead of a paragraph here. The 606 GiB tar headline those rulings were made on
> is also gone: this jail's backlog was reclaimed to 10 GiB — see 💬 16 and
> [`minimal-disk-footprint.md`](../design/minimal-disk-footprint.md) §2.4, including the new
> finding that the device keeps filling from a ledger none of the docs track.)*

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

**And the real number is now countable rather than estimated: the sweep returns 101**, re-run
2026-09-02 after the audit's edits landed (it read 102 before OQ-GN3 was answered later the same
day — see the last row of the table). Against 2026-08-30's **113** it reconciles in two
halves. **−6 before this audit touched anything:**
[`profiles-as-pack-variants.md`](../design/profiles-as-pack-variants.md) went 5 → 0 (its §14 ledger
closed six questions on 2026-09-01) and one more moved in other sessions' work. **−5 from the
audit itself, row-exact:**

| Δ | Why |
|---|---|
| **−1** | `config-safety.md` OQ-D3 — answered (closed in code 2026-08-29; the audit recorded it) |
| **−1** | `noncontainer-nix-environment.md` OQ-1 — answered by events (host = run) |
| **−1** | `macos-user-build-step-threat-model.md` Q1 — mooted by the cwd-walk deletion |
| **−4** | this file: 💬 3, 4, 9, 12 deleted (18 → 14 entries — 13 `### 💬` headings + the Mac-config bullet) |
| **+1** | `agent-auth-modes.md` OQ-9 — the AWS-pair question carried back in; the rewrite had dropped it unanswered |
| **+1** | `trust-paths.md` OQ-TP8 — the pack-Lua census gap, newly named |
| **−1** | `handoff-guest-notch-macos.md` OQ-GN3 — *has the Cachix cache been pushed to?* Answered from the Actions log the same day: yes, and CI reads from it too |

> [!WARNING]
> **A roadmap row and the questions it points at are counted twice, and that is a bias in the tool
> this file trusts.** A row is a *grouping* of questions that already live in their design docs —
> 💬 16 and the three live `OQ-DF*` are four entries for one decision. The "gap between 101 and 13"
> framing below compares a number against a set that contains it. Not worth redefining mid-sprint —
> worth saying out loud, because the last time this count was quietly wrong it was wrong by six.

The two-wave audit that first produced this number found 95. The count has since moved in both
directions for the right reasons — rulings close questions, and executing rulings mints new ones.
It used to require reading ~9,000 lines; it is now one command:

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

**Thirteen rows below is what the blocking subset groups into** — the rest are named
in *What the roadmap does not cover* at the end, deliberately. The gap between 101 and 13 is the
point of this file: a row is a *decision*, and one decision usually closes several questions.
**💬 6's four rulings closed a whole row in a single turn**, and the 2026-09-02 audit closed four
rows without any ruling at all — the other way the gap earns its keep, since re-verifying a row
against the tree is what finds the questions the code already answered.

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
  **Narrowed 2026-09-02:** the *observation* half of the leaning's option (b) now exists — install
  receipts (`af46c9b4`) record the resolved npm version, but nothing reads them back — so what is
  left is promoting the record into a row `install` obeys, and it should be one design with
  program-delivery §10's *user-scope gap receipt* step (📦 below).
- **OQ-TP8** *(new 2026-09-02, executing 💬 18's D9)* — **pack-shipped `derive.lua` runs ungated for
  every origin, and its census row did not exist.** Row 26 now records the facts: sandboxed
  allowlist Lua (no `os`/`io`/`require`, timeout-bounded), executed in-jail every boot, host-side
  during `yolo host apply` as a sentinel probe — and, since `3144fbed`, **at every
  `yolo host -- <cmd>` launch with real credential-bearing inputs** (`host.go:458`), where its
  output IS a real host process's environment. A fetched pack may not name a host file to *read*,
  and may ship Lua yolo *runs*. The question is whether ungated is the ruling or the accident;
  leaning is ruled-ungated in-jail, with the host-launch half needing an actual decision now that
  its trigger fired.
- **OQ-X1** — does a digest-pinned installer script count, given its own fetches are not pinned?
  *(Sharpened: only embedded packs use installers today, and `packdecl` has no digest field at all —
  the scenario is unexpressible until OQ-BP5 lands one.)*
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

**Both of this project's upstream blockers dissolved on 2026-09-02** — nix OQ-1 answered (host =
run) and auth OQ-1 measured — so nothing gates these four questions but themselves. The 2026-09-02
audit also gave OQ-A and OQ-C the shipped-precedent facts their leanings were missing: every verb
yolo ships is synchronous, and the oauth broker already returns per-verb response shapes (with the
trust-regime caveat recorded in the doc).

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

**threat-model Q2-Q3** cover `--accept-flake-config`'s substituter surface (live — see the shipped
item) and a macOS build sandbox. **Q1 left this row on 2026-09-02: it was mooted by `46655873`**
(2026-08-31), which deleted the cwd walk-up wholesale — a strictly stronger fix than the
workspace-exclusion Q1 proposed, shipped for source-skew hygiene rather than security. Vector B and
H1 are recorded dead in the doc. **OQ-L1** explicitly blocks Track L part 2. **OQ-GN1 · OQ-GN2 · OQ-GN4** are
new (2026-08-23) —
**OQ-GN3 was answered 2026-09-02** from the Actions log (the Cachix push happened AND the
cache is being read; D4 is down to the Mac download proof) —
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

📄 [`BACKLOG.md`](BACKLOG.md) §Stage E — **S5 · OQ-CO · OQ-S4 · OQ-E4 · E1 · E2 · E5** · plus
[`pack-host-management-plan.md`](pack-host-management-plan.md) **OQ-B**, folded in from the retired
💬 12 (2026-09-02) because it was never a separate decision — see the E1/E2 bullet

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
  skills delivery or only adds to it — the jail and the host answer differently today. *(OQ-CO
  freshness, 2026-09-02: `packs/zai` is now the first shipped `config-overlay` contributor — sole on
  its surface, so still no collision — and the new `profile` gate changes whether an overlay
  participates, not what happens when two active ones share a key.)*
- **OQ-E4** is the ~15% of E4 that did not ship: do `stateful` surfaces get comment preservation?
  `rmw` preserves, `computed` correctly does not, `json` is provably vacuous.

*(**E3 has left this list — it shipped 2026-08-15**, `29ccf212`, and both this file and the backlog
row were still calling it open. And **E4 is not a question**; only its `stateful` residue is, which
is why the list cites OQ-E4 and not E4.)*

### 💬 10 — `yolo check` tells you about the wrong machine, in three places and one vocabulary

📄 [`broker-ca-and-nested-hosts.md`](../design/broker-ca-and-nested-hosts.md) — **OQ-3**

**These were three separate small questions until 2026-08-23; they are one.** `check` has no way for
a section to say *whose* facts it is reporting — the host's, or the runtime it can see from in here —
so every section decides by hand and some decide wrong. The ruling is what a jail-observable section
should **print**: a fourth verdict beside `[PASS]`/`[FAIL]`/`[WARN]`, or a scope suffix.

- **The measurement that makes it one question, not three:** `check`'s reporter has exactly three
  verdict tokens and **no `[SKIP]`**, so a section that steps aside emits `[PASS]` — which is what
  hid a daemon that never started. **Ten call sites already step aside this way** (measured
  2026-08-23, **recounted unchanged 2026-09-02**; `reporter.go` has had zero commits in between) —
  nine distinct sections, because `sectionGPUAmd` holds two of them. **The doc now carries a
  price** so the ruling knows its cost: `[SKIP]` is one ~10-line method, one summary branch, and
  ten mechanical flips; the scope-suffix alternative must first split step-asides from
  wrong-boundary sections, which are different defects.
- **`sectionRunningJails` has no in-jail guard** (`check.go:622`). From inside a jail it reports the
  *nested* podman's view — measured `[PASS] No jails currently running` in here while the host had
  one. Left alone so far because it is *true of the runtime it can see*, and the orphan-cleanup path
  underneath acts on that same runtime. **On its own this is a wording preference; as an input to the
  vocabulary question it is evidence.**
- **`sectionGPUNvidia` has none either** (`sections_devices.go:38`) — three `[FAIL]`s for host facts,
  where its AMD twin guards both checks. That asymmetry is 🔒 below because deciding *which rows* to
  guard needs a host with a card; the *vocabulary* does not.

### 💬 11 — One that is nobody else's question

*(This row held six bullets until 2026-09-02. Four resolved into work and moved to 📦 Small
repairs — the port-gate spelling, the launcher-splice quoting, the npm shape check, and the fourth
PATH, whose "drift question" got its answer: git archaeology shows the mise-trust `Setenv` block's
subprocess was deleted on 2026-08-05 (`3a309da4`) and the block is dead code whose comment was
false from its first commit. A fifth — the fixed code comments — was a lesson, not work, and the
lesson lives in AGENTS.md's workflow rules now.)*

- **A concurrent launch attaches by re-running the entrypoint inside a jail that may still be
  booting.** Found while shipping the waiting notice (`c2188bba`). **Examined 2026-09-02 — the
  inventory the row used to ask for now exists, and it changes the fix menu.** The race is real and
  has two doors: `run.go:539-542` checks `existingCID` *before* the workspace lock is ever taken
  (a running-but-still-provisioning container is attached to immediately), and the lock winner
  releases at *podman-reports-running* (`lifecycle.go:55-74`), not at provisioning-done, so the
  loser's full entrypoint boot races the first. The dangerous collision points are exactly two:
  `GenerateShims` and `GenerateAgentLaunchers` both **wipe-then-repopulate** their dirs
  (`resetAnchorDir`/`ClearContents`), so a second boot can empty `~/.yolo/bin/block` mid-populate —
  a window where a blocked tool is briefly unblocked. Everything else writes deterministic bytes
  in place. **And the obvious fix is banned**: `fsx.go:1-24` mandates truncate-in-place over
  tmp+rename repo-wide, because file→file bind mounts pin inodes (a 2026-07-04 regression) — so the
  fix shape is serializing the second entrypoint behind a provisioning-done sentinel, not atomic
  writes. `stopLoopholes` (`loopholesruntime.go:327-346`) still does its own uncoordinated
  non-blocking acquire on the same lock. The ruling: is that serialization worth building now, or
  does clean-in-4/4-runs plus a named two-door window stay accepted?

### 💬 13 — Nested nixpkgs attribute paths in `packages`

📄 [`package-nested-attribute-paths.md`](../design/package-nested-attribute-paths.md) — **OQ-1**

This sat in 📦 as *"designed, questions answered, no blockers"* and it is none of those. Its doc is
`**Status:** DESIGN SKETCH, 2026-08-22. Nothing built.` — **still true, re-verified 2026-09-02**
(`packageNameRe` is still single-dot, `parseDottedSpec` unchanged, no resolver anywhere in
`flake.nix`; one worked example — `llvmPackages_16` — has since been removed from the pinned
nixpkgs and the doc notes a substitute) — and **OQ-1** is the resolver's central rule: how a dotted path resolves when a
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
  permit the imperative one — the shape OQ-LP14 already suffers from. *(The premise is now verified
  with a file:line: a fetched `host_daemon.cmd` really is approvable today,
  `loopholesource.go:258-310`.)*

⚠ **The cost OQ-BP1 put on the critical path is still unpaid**: the release process has to produce
the matrix a manifest's `platforms` declares. Declaring more than you build turns *"unsupported
here"* into *"supported, missing"* (`broker-as-a-pack.md` §9).

---

### 💬 15 — Backend parity: the census, and whether macos-user gets briefings at all

📄 [`backend-parity.md`](../design/backend-parity.md) — **OQ-BP-1 · OQ-BP-2 · OQ-BP-3 · OQ-BP-4**

**Born from issue #39 and the sweep behind it.** Fourteen of the twenty-one defects are fixed or
warned (that doc's §5 is the table); what is left is a decision about the mechanism, not about any
one bug. *(2026-09-02: the doc's §6 briefing fix turned out to have SHIPPED the day it was
written — `28ddea11` — with the doc never updated; it now says so, and OQ-BP-1's "after §6"
sequencing condition is discharged. The census is the whole remainder.)*

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
**What it did not cover:** Apple Container, which must keep writing a file because its converters
interpolate a path — and the pre-C3 backlog, which **as of 2026-09-02 is gone from this jail's
nested cache** (3 tars / 10 GiB, the keep-3 fingerprint of a manual `yolo prune --apply`; the
host-side 125 GiB cache is not observable from here). Two new facts from that re-measurement, both
in [`minimal-disk-footprint.md`](../design/minimal-disk-footprint.md) §2.4: the manual recovery
tool demonstrably works (field evidence for OQ-DF2's leaning), and **the device lost another
~92 GiB of free space in the same eight days anyway** (87 % full, 516 GiB free) with the nix store
up only 6 GiB — the live growth driver is in **none of the three ledgers**, which is a new input to
OQ-DF4's budget question.

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

### 💬 17 — Mistyped names return `[PASS]`: mostly closed by the provider arc; the buried channel remains

📄 [`reference-mismatch-diagnostics.md`](../design/reference-mismatch-diagnostics.md) —
**OQ-RM1 (narrowed) · OQ-RM2 · OQ-RM3 · OQ-RM4** · executes the amended
[`stringly-typed-references-principle.md`](../design/stringly-typed-references-principle.md)

**You ruled the premise on 2026-08-30, and the provider arc built most of it within 72 hours** —
§7 steps 2, 3 and 6 shipped (selection-key validation, the `wire_api` enum, the `base_url`
credential refusal, and the credential preflight — the last with a deliberate selected-pack
rescoping the doc now records). **The 2026-08-30 reproduction this row used to carry is dead**: the
same config now yields three `[FAIL]`s, and the docs' census flips three rows to Reached
(re-verified 2026-09-02; both docs updated).

**What is left is exactly two things:**

- **The buried-warning channel (§7 step 1) — unshipped, needs no ruling, and it is the
  highest-payoff item in the doc.** `yolo check` still has two diagnostic channels and the summary
  counts one: bare `Warning:` lines from config resolution and loophole discovery — including the
  best mismatch diagnostic in the tree, the supersession did-you-mean — are invisible to the one
  line a user reads (`reporter.go:84-89`, still non-counting). **Queued 📦 below.**
- **The supersession relocation (§7 step 4) and its skew message (step 5)**, gated on **OQ-RM2**
  (refuse the launch vs. refuse the pack — the widest-blast-radius change left: an unmatched
  `supersedes` currently warns and keeps running, after this it stops the launch) and **OQ-RM3**
  (lazy vs. eager two-hash computation). **OQ-RM1** is narrowed by events — steps 2/3 already make
  `check` exit non-zero, so it now decides only the launch-only checks — and **OQ-RM4** (an escape
  hatch?) leans no-hatch.

**What this row is NOT.** Not a new config key, manifest field, or contribution kind — every name
checked here already exists. Not a re-litigation of `env_sources`' permissiveness, which is correct
and stays (a missing host file is portability, not a typo).

### 💬 18 — The provider table is checked in yolo's vocabulary and delivered in everyone else's

📄 [`provider-table-fidelity.md`](../design/provider-table-fidelity.md) —
**all nine ruled 2026-09-01** — the doc is DECIDED; **D9** is the finding that outgrew it · follow-on to
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
(measured); `--profile` meant startup timing *or* a pack profile depending on the next token, after
two commits patching that parse — OQ-PT5 has since taken the timing meaning away (`--timing`) and
left the flag name-only; and the census — this work made `zai` the **twelfth** pack and the
first that installs no CLI *and* ships no loophole, while `AGENTS.md:8` still said ten — 67f87f36
has since said twelve.

**What needs no ruling, and is worth doing regardless:** the integration test this called missing —
nothing in `integration/` mentioned `zai`, `pack_profiles` or `providers`, though the pattern existed
and **an in-jail assertion on the rendered `~/.codex/config.toml` is the single check that would have
caught the headline defect**. `cee9c1fc` landed that test on 2026-09-02, **RED on purpose** — its
assertions name only what pi, codex and opencode themselves accept — and it went green in the same
cycle: `7fa624ba` turned the D10/D11 subtests green and `0f04632d` the D1 subtests, so all four
subtests now pass (re-verified by running it). `pack_profiles` is the one word of the original
complaint still without coverage **in `integration/`** — its unit coverage exists
(`internal/config/useprofilekeys_test.go` — renamed with the key in `43d24e9e` —
`internal/cli/configoverlayprofile_test.go`). The census
half is done: `67f87f36` rewrote `AGENTS.md` and `packs/embed.go`, and `886a9191` fixed
`USER_GUIDE.md:217` — which was never a census line but §5.2's `--profile` doc half, since renamed
`--timing`. The doc's §9 puts the test first deliberately: it landed red, which is how it caught the
defect.

**Eight of nine ruled in one review round, and two of my leanings were overturned.** OQ-PT1 takes
three protocol-shaped canonical names that are deliberately **nobody's dialect**, so a pass-through
cannot work by accident. OQ-PT2 — *"why would we allow both of these? that right there seems
broken"* — **refuses** the `base_url`/`endpoints` pair outright, and §7's A4 row is corrected: I had
rejected the refusal claiming it made per-field override unspellable, and it does not (you override
with `endpoints.<protocol>.base_url`, the shape the pack already used). OQ-PT5 gives the profile the
good name — *"make the pack profile stuff short and easy"* — so the startup-timing flag is renamed
and `profileValueAt`'s next-token heuristic, which cost two fix commits, is deleted rather than made
more careful.

**OQ-PT9 closed, against both positions I had argued.** *"So it can set environment vars that can do
literally anything, but we won't let it pass through a token, in a way that would simplify
everything?"* — right, and checked: a derive **already** controls its surface's computed layer
including `mcp_servers` `command`/`args` with no schema validation, and
[`trust-paths.md`](../design/trust-paths.md) **row 18 already records a fetched pack's `env` as
"in-jail exec in practice (no key allowlist, so LD_PRELOAD etc.)" with approval "never,
explicitly."** The capability is not being withheld; it is granted knowingly one row down. So the
derive gets the resolved credential, and `{endpoint}`/`{key}`/`{region}`/`{model:alias}`, the
`yolo.secret()` sentinel and the declared `credential_env` **all go** — the substitution vocabulary
disappears rather than being replaced. The cost is recorded honestly as an auditability trade, not a
security one: a derive reading a token from `ctx` is silent, while exfiltration through a written MCP
command leaves an artifact `yolo config diff` shows.

**And settling it found D9, which outranks the whole row.** `derive.lua` is **Lua that yolo executes**,
loaded from any pack's root with **no origin gate** — the check `host_files` gets
(`MayGrantHostFiles`: fetched content may never name a host file) has no counterpart — and its output
becomes a config surface unvalidated. **It has no row in `trust-paths.md`'s 25-path census**: "lua"
occurs once in that document, inside "re-derive". Row 13 is a *workspace* `yolo-jail.config.lua`; row
17 is fetched-pack **content**, which is data. So: **a fetched pack may not name a host file to READ,
and may ship code yolo RUNS.** The weaker capability is gated, the stronger is not. Filed as a census
gap rather than a verdict — ungated derives may well be the right answer, given rows 18 and 19 — but
no ruling exists to point at, and `trust-paths.md` owns that call.

**What this row is NOT.** Not a retraction of either parent doc — the credential boundary, the
three-level skew handling and the backend-parity repairs all re-measured clean. Not a proposal to
reopen the enum, which would restore what 💬 **17** §7 step 3 closed. Not a new contribution kind:
the `config-overlay` `profile` gate `568d5a3a` landed is the right mechanism, and OQ-PT3 asks only
whether it may carry the placeholder vocabulary `env_shape` already has.


### 💬 19 — A catalog and a selection are two features, and only one of them ships

📄 [`provider-catalog-and-selection.md`](../design/provider-catalog-and-selection.md) —
**all ruled 2026-09-01** — the doc is DECIDED; a tenth was raised and withdrawn · plans: [`catalog`](../design/provider-catalog-and-selection-plan.md) · [`fidelity`](../design/provider-table-fidelity-plan.md) · ~~OQ-CS1~~ ~~OQ-CS2~~ ~~OQ-CS4~~ ~~OQ-CS6~~ ruled · sibling to 💬 **18**, which reports defects in the same machinery ·
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

**Two review objections corrected the draft, and both were mine to fix.** *"What happens with
bedrock? It can't be default enabled because a user may not have creds."* — I had restated the rule
as "pack presence means in the dictionary", and pack presence is **not** catalog membership: a
selected pack's providers all reach the composed table, but only endpoint-bearing ones reach a
catalog, because that is what the derives gate on. Measured: with `packs: ["claude"]`, `bedrock`
composes with **no endpoints and no credential pointer** — so it reaches no catalog, demands nothing
with nothing hydrated, and **a user who has never touched AWS is never asked for anything**. Bedrock
is a *selection-only* provider, and that falls out of "has no endpoint" rather than being an
exemption. And *"profiles are a generic thing without a provider, and now also sorta provider
profiles with a magically enforced schema — isn't this overloaded?"* — yes, and I built the overload:
a property offering optional declaration for backward compatibility recreated the two-meanings
problem. **Inverted:** every profile names a provider, declaration is mandatory (costs nothing —
both profiles in the tree already do), and the schema is **two core-defined fields**, `provider` and
`model`, with the provider defining only the legal *values*. No derivation, no reflection.

**Two more rulings, and one overruled me.** *"Model can't be the only config we'll want, which is
why I said the config surface/schema is dictated by the provider"* settles OQ-CS4 **against** my
lean-conservative answer: a provider declares an `options` block, a profile is an instance of it, and
the agent's own derive decides where each option lands — core learns no option names. And *"reversing
old decisions is fine, we're debugging a mess of a design"* confirms OQ-CS6: `profiles-as-pack-variants.md`
OQ-5's free-form names are **superseded**, so an undeclared profile name becomes a reportable error
instead of a silent no-op.

**And tracing "what do we actually use `ZAI_API_KEY` for" answered it and found a defect.** Three of
four agents get the **name** and read the env themselves (pi `apiKeyEnv`, codex `api_key_env`,
opencode `{env:…}`) — yolo never touches the secret and **it is embedded in no agent's config**.
claude is the exception and it is a *rename*, not an embed: it reads `ANTHROPIC_AUTH_TOKEN`, nothing
can alias one env var to another, so `{key}` copies the value. The defect: `writeUserEnvFile` writes
the hydrated secrets to `<workspace>/.yolo/yolo-user-env.sh` at **0644** — measured `-rw-r--r--` with
two keys in plaintext — while [`packs/zai/README.md`](../../packs/zai/README.md) tells the user to
keep that key in a file that is *"untracked, 0600"*. Gitignored, so not a commit risk; the mode
downgrade is real. Recorded as **D8** in 💬 **18**, along with the argv exposure the claude path
carries structurally.

**And the follow-up question found the mirror of 💬 18's headline defect.** *"Yolo must know how auth
passes in case a rename is necessary … it's more that the claude pack specifically needs the env_name
config, although that could be shared with other agents that need this rename."* Right, and stronger:
**`env_shape` is declared by the PROVIDER and describes the AGENT.** `packs/zai` names
`ANTHROPIC_BASE_URL` and `ANTHROPIC_AUTH_TOKEN` — facts about claude, which z.ai has no opinion
about. D1 hoisted an agent's vocabulary up into a core enum; this pushes an agent's requirement down
into a provider record. Two costs: every provider serving claude restates claude's variable names
(the maintainer's config already carries a `CEREBRAS_API_KEY`, so the third is not hypothetical),
and — because nothing declares what claude needs — **each provider guesses a different subset**. zai
declares endpoint + token and no model vars; bedrock declares models + region and no endpoint or
token. Visible consequence: `packs/zai` ships `models: {default: glm-4.7, fast: glm-4.7-air}` and
**nothing delivers them to claude**, since the `{model:…}` placeholders live only in `packs/claude`'s
bedrock declaration. Whether that breaks anything at runtime is NOT established — z.ai's Anthropic
route may remap the model name, and settling it needs an authenticated request this repo's tests may
not make.

The fix is the rule §3 is already built on: **each agent pack declares how a selection reaches it**,
per protocol — codex and opencode name a config key, claude names a set of env vars — so `packs/zai`
declares no `env_shape` at all and claude stops being the special case. New **OQ-CS8** asks whether
that is a new kind or the existing field relocated (I lean relocate).

**Two follow-ups, and the first found a naming problem worth fixing before anything ships.** *"Why do
we have profiles and pack profiles? What's the diff?"* — they ARE two things (a **declaration**, name
→ provider + options, durable; and a **selection**, which profile each CLI uses), and that split is
the catalog/selection one a level up, so merging them re-creates the conflation this row exists to
undo. **But `pack_profiles` names neither.** Its keys are **CLI names**, validated as such by
`config.PackProfileCLINames`; no pack is named in it anywhere. It was `agent_profiles` until
2026-09-01 and the rename's stated reason — *"the keys were always CLI names and core knows packs,
not agents"* — is true about `agent` and does not make `pack` right. Standing a new `profiles` key
beside it would leave two near-synonyms. I lean renaming the selection key to `use_profile`
(*"use profile zai for claude"*), folded into OQ-CS5.

*"Should new profiles point to a provider, or copy and override another profile? Option for both?"* —
new **OQ-CS9**, leaning **provider-pointing only**, because OQ-CS4 already removed most of
inheritance's value: a provider declares its options WITH DEFAULTS, so a profile states only what it
changes and there is little to duplicate. `extends` costs cycles, chains and nested-override
semantics to save one field. The residual case is honest and unresolved — a variant of a heavily
customized profile would restate everything — but no profile in the tree has more than one option
yet, so that evidence does not exist.

**Both ruled: `use_profiles`, and (a).** The selection key becomes **`use_profiles`** — *"use profile
zai-fast for claude"* — so the config reads `profiles` for what a profile IS and `use_profiles` for
which CLI uses it, with no two keys that sound alike. And **profiles POINT at a provider; no
`extends`** (OQ-CS9): provider-declared option defaults already remove the duplication inheritance
would fix, and the residual case (a variant of a heavily customized profile) is recorded as the
evidence that would reopen it, which does not exist yet.

**The rename is one step, not two, by the maintainer's own precedent.** `pack_profiles` landed
2026-08-31 and `v0.8.0` is dated 2026-08-13, so the intermediate name **has never been in a
release** — it gets no deprecation path and becomes an ordinary unknown key, exactly as
`api_key_env` did this morning. `agent_profiles` KEEPS its by-name message, because that spelling is
in every host-generated jail snapshot in existence. Size on the record before scheduling: **92
non-test occurrences** of `pack_profiles`/`YOLO_PACK_PROFILES`/`PackProfiles` across 12+ files plus
88 in tests — a config key, an env var, a Lua `ctx` field the derives read, and the Go identifiers
between them. Mechanical, but its own commit.

**A review round closed three more, and two of them dissolved because I had not propagated 💬 18's
OQ-PT9 into this doc.** *"Who is doing this selecting? Isn't this up to derive?"* — yes: **core
resolves no model at all** (OQ-CS3), it hands the derive the active profile and the provider entry
and the derive writes its agent's key, so `default` stays an ordinary alias and core reserves
nothing. *"'Move' means the pack handles it? I need clearer details here."* — the leaning said "move
the field" when OQ-PT9 had already deleted the field. **Nothing declares the binding; the agent's
pack composes it in its own env-emitting derive** (OQ-CS8), and the doc now carries the Lua and the
deletion list: `env_shape` plus its validators, the four placeholder constants, the env_shape skew
tests, and most of a 250-line `internal/agentenv` — **including core's `agentProtocols` agent →
protocol table**, which is `pack-code-separation.md`'s "core does not know what an agent is" arriving
somewhere it had been explicitly excepted. And *"yes of course user"* settles OQ-CS5's scope: user
scope only, both keys.

**The last one closed, and the review corrected its shape.** OQ-CS7: core validates **nothing** — a
typechecker there is `wire_api`'s enum again, a set core owns delivered verbatim to consumers that
own different ones — and the derive validates with errors propagating. What core does keep is the
**key census** this repo already runs everywhere (`knownProviderKeys`, `reportUnknownKeys`), which is
a different thing from validating a value and is what makes the answer not a half-schema. So a
provider declares its option names and defaults, a mistyped profile key errors *naming what the
provider accepts*, and the footprint can print `accepts: model, thinking`.

**And the shape got simpler under review:** *"I don't get why `{options: {model: {default: foo}}}`
and not just `{options: {model: foo}}`"* — no reason. Once `kind`, `values` and enum checking were
gone, `default` was the only field left and the wrapper object was the half-schema keeping a hole to
climb back through. It is now a **flat name → default-value map**, which also matches its neighbour
`models`. One wrinkle written into the ruling rather than left to be discovered: **`null` means
*declared, no default*, NOT the delete convention** — un-declaring an option is a thing nobody wants,
while "keep the option, drop the default" is a real override.

**Writing the plans raised a tenth question and it was withdrawn on sight** — *"I don't get it, why
would we NOT support env on the host too?"* There is no case on the other side: `yolo host -- claude`
composing the same environment is behaviour [`host-agent-environment.md`](../design/host-agent-environment.md)
§2.2 already fixes. Asking it as open invited a "no" nobody wants. **The fact underneath is real and
moved to the plan as a constraint:** env composition is host-launch-time with three consumers —
`profilechannel.go:93`, `host.go:432` (**no jail there**) and macos-user — while a derive runs in-jail
at boot, so OQ-CS8's work must give the host notch a way to run the env derive. And the seam that was
cited for it is wrong: `hostrender.go:377` is a **key-name probe against sentinel inputs**, whose own
comment says content deliberately does not cross ("a jail's derived MCP table embeds jail-absolute
paths"). A host-side env derive needs real tables — a new invocation, not a reuse. Both corrections
are in the plan.

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

**Five items** (was three; the 2026-09-02 audit added two that need no ruling). **C4 and C5 are
deliberately NOT here**: their go/no-go moved to 🧊 as an explicit row — the shape is ruled, the
measurement is taken, and queueing them before you call it would be queueing a question.

**Ordering basis:** what unblocks the most other work first, then what is cheapest. The warning
channel is first because every other diagnostic in the tree is worth less while findings are
invisible; the small repairs are second because they are nearly free and two of them close named
holes; the disk and program-delivery rows follow as before; briefing-audiences is last only because
it is the largest.

- 📦 **The warning channel: `yolo check`'s summary counts one of its two diagnostic channels.** 📄
  [`reference-mismatch-diagnostics.md`](../design/reference-mismatch-diagnostics.md) §7 step 1 — the
  only unshipped step that needs no ruling, and the doc's own sequencing puts it first: bare
  `Warning:` lines from config resolution and loophole discovery (including the supersession
  did-you-mean) never reach the summary line (`reporter.go:84-89` is deliberately non-counting;
  three callers). Route them through the reporter or count them. Interacts with 💬 10's `[SKIP]`
  vocabulary — building both at once touches the same small file and should be one pass if 💬 10
  gets ruled first.

- 📦 **Small repairs — validated, no rulings left, each its own commit.** Four graduated from 💬 11
  on 2026-09-02 when the audit answered their questions; one is OQ-CAP's residue:
  - **Port-gate fourth spelling:** the host-side socat spawner re-derives the CONFIGURED net mode
    inline (`run.go:770-784` today; the row used to say ~:599-621) instead of reading
    `appliedNetMode` — `a38fe0ab`'s own commit message tracks this residue. Make it read the shared
    predicate.
  - **Delete the dead mise-trust PATH block** (`boot.go:527-532`): its subprocess was deleted
    2026-08-05 (`3a309da4`), its comment ("matches the pre-exec PATH set in main()") was false from
    its first commit, and nothing between it and the next PATH write execs or looks up anything.
    Verify that last clause once more in the commit that deletes it.
  - **`shquote` the launcher-template splices:** `__YOLO_URL__` lands raw on a `curl` argv
    (`shims.go:817`) and `__YOLO_PKG__`/`__YOLO_SPEC__` raw inside double quotes (`:579-580`),
    beside receipt fields that are correctly quoted — and one generator quotes `STAMP_DIR` while
    two splice it raw. Post-approval surface, so no ruling; wants its own careful pass because
    tests pin template fragments.
  - **npm selector shape check, host-side:** `foo@@1.2.3` still reaches npm in-jail where diagnosis
    is worst (`splitNpmSpec` is deliberately verbatim; `packdecl` checks presence only). Refuse the
    shapes npm itself refuses — whitespace, quotes, `@@` — and nothing stricter, which dissolves
    the old "how strict" hesitation.
  - ~~**`yolo pack footprint` prints `supersedes`**~~ — **not work at all: already shipped**
    (`d776c902`, 2026-08-15). This bullet existed because the audit grepped `internal/cli/pack.go`
    for *supersede* and found nothing, while the claim reaches the output through
    `packload.FootprintOf` and a renderer that formats `string(c.Kind)` generically. Closed
    2026-09-02 with a surface test instead of a feature.

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

  **The stake, re-measured 2026-09-02 — the headline backlog is GONE and the disk problem is not.**
  This jail's nested `cache/images` holds **3 tars / 10 GiB** (was 148 / 480.71 GiB on 08-25): the
  keep-3 fingerprint of a manual `yolo prune --apply`, with nothing written since — C3 working. The
  host-side cache (36 / 125.41 GiB on 08-25) is not observable from this session. **And the device
  lost ~92 GiB of free space in those same eight days anyway** — 87 % full, 516 GiB free — with the
  store up only +6 GiB (659 → 665), so the live growth driver is in **none of the three ledgers**
  ([`minimal-disk-footprint.md`](../design/minimal-disk-footprint.md) §2.4). Two consequences: the
  manual recovery path is field-proven (OQ-DF2 leaning iii), and *"minimal disk space"* now needs at
  least a pointer at the untracked remainder or it will be declared reached while the device fills
  at ~11.5 GiB/day. **One machine, one jail** — §1.6's R7 caveat, unchanged.

  **The store framing survives the re-measurement:** at 665 GiB it is the larger raw line item but
  not the larger *reclaim* — most is pinned by the durable §1 GC roots doing their job.
  [`storage-lifecycle.md`](storage-lifecycle.md) already
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
    *(2026-09-02: someone followed the hint — the nested backlog is reclaimed to keep-3. The
    structural point stands: nothing in `internal/prune` has an automatic caller, re-verified, so
    the next backlog waits for the next human.)*
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
    the old shape, which that number has to price too. *(2026-09-02: this snapshot cannot be
    re-taken from here — the in-jail podman graph root lives outside the persistent home and reset
    with the container. Ledger C's state is only observable on a store that survives, i.e. the
    host's.)*
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
  destination inference and **delivers nowhere**, silently. *(Its code anchors were repinned
  2026-09-02 in the doc itself — the provider arc moved every file under them.)*

**What refills this section:** an answer in 💬, or an audit finding work that needs none — **and
this queue holds both kinds today.** The disk and program-delivery rows are rulings executed; the
warning channel and the small repairs are the 2026-09-02 audit's no-ruling residue. The 💬 that
would unblock the most buildable work the day it is ruled is still **💬 2**: trust-paths has TP4's
pin (now half-built by the receipts), TP7's catch-up, and TP8's host-launch decision.

> [!NOTE]
> **The queue emptying on 2026-08-24 was not the interesting part; how it refilled is.** Nothing in
> 💬 was answered that day — an outside report arrived, and auditing the class behind it produced
> seventeen defects and one buildable item. The 2026-09-02 refill is the other shape: a full-corpus
> audit closed four 💬 rows without a ruling and queued five buildable items, three of which had
> been sitting *inside* 💬 rows disguised as questions. If the queue is empty, the question is not
> "what should we build" but "which of the rulings is actually blocking, and which is a question I
> invented" — see [`further-roadmap-ideas.md`](further-roadmap-ideas.md) §4 and §4a.

# 🔒 Waiting

- 🔒 **`check-macos` was RED on `main` from 2026-08-31; the fix is in, awaiting a darwin CI run.**
  `TestEnvSourcesAnchorBesideTheDeclaringFile` (added `7f600ef7`) compared loader-derived paths
  against `t.TempDir()` spellings, which disagree on darwin through the `/private` symlink — every
  `check-macos` run failed from `1f36023a` onward, and nothing tracked it until the 2026-09-02
  audit pulled the CI history. Fixed the way `configls_test.go` already does (`EvalSymlinks` both
  roots, `db854ca7`); verified green on Linux, unverifiable on darwin from this jail. **The next
  push's `check-macos` settles it; if green, this row leaves.**

  *(The nightly row that used to sit here left the file the same day: the warmup-skip fix
  (`e5b60902`) was verified live in the job logs of **seven consecutive nightlies** —
  `[integration] skipping the jail warmup on darwin` on every run since 08-24, first-test absorbing
  the image realisation, the warmup+first-test pair never regressing (0.5–9.2 min saved/night). One
  honest correction to its expectation: total-job wall clock did NOT cleanly drop ~12 minutes —
  night-to-night suite variance of ±15–20 min swamps it, and the ~12-minute figure was one bad
  night's warmup cost, never a promise about totals. The mechanism-level claim is what was
  verified, and it holds.)*

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
- 🧊 **Boundary broker B2** (approval-gated host credentials) — **no longer gated on anything; iced
  only because nobody has said they want it.** Both of its gates opened on 2026-09-02: nix OQ-1
  closed as *run* (the notch B2's approval tier is compelling in exists), and the experiment this
  row used to call "the cheapest thing in the whole file" **was run** — Claude Code sends the
  subscription bearer to any base URL, so a broker interposes by URL alone with no client change
  ([`agent-auth-modes.md`](../design/agent-auth-modes.md) §8.1). Moving it out of the icebox is
  💬 5's OQ-A ruling plus an appetite. 📄
  [`boundary-broker.md`](../design/boundary-broker.md) §8.

- 🧊 **C4/C5 — the opt-in fast image path.** Shape ruled 2026-08-25 (image-staging OQ-1: opt-in,
  baked path retained); the gating re-measurement is TAKEN
  ([`image-staging-vs-baking.md`](../design/image-staging-vs-baking.md) §1.8) and reports a flat
  curve on the workload it could measure — C3 discharged C4's disk case, C2 its frequency case, the
  52 s cold launch is one C4 does not shorten, and **the one workload C4 exists for is explicitly
  not measured there**. Genuinely awaiting your go/no-go; queueing it before that call would be
  queueing a question. *(Promoted from a preamble paragraph to a row on 2026-09-02 so it stops
  being invisible to the counts.)*

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
- **nix OQ-3 · 4 · 5 · 7 · 8 · 9** in
  [`noncontainer-nix-environment.md`](../design/noncontainer-nix-environment.md) — what remains of
  the retired 💬 4 after OQ-1 closed (2026-09-02). None blocks anything: OQ-3 is the `nix profile`
  installer product, OQ-7 is agents-from-nix (leaning no), OQ-8's reporting half and OQ-9's
  Linux-diagnostics half are worth fixing under any answer and are work, not rulings.
- **auth OQ-9** in [`agent-auth-modes.md`](../design/agent-auth-modes.md) — the AWS credential-pair
  gap, carried back in when the retired 💬 3's doc rewrite dropped it unanswered. Working today via
  `env_sources`; likely absorbed by the in-flight env-derive work rather than decided.
- **threat-model Q2 · Q3** in
  [`macos-user-build-step-threat-model.md`](../design/macos-user-build-step-threat-model.md) — still
  open after Q1's mooting, both scoped to Vector A now, neither blocking (they stay summarized under
  💬 7 because that row is where the Mac work already lives).
- The **research** docs' questions — **OQ-LM1 … OQ-LM6** in `local-model-endpoints.md` — are
  exploratory rather than blocking. *(`mise-host-jail-path-mismatch.md` is now CLOSED: its last open
  question had already shipped as `venvShadowMountArgs`, and re-reading it is what surfaced a trap
  documented nowhere else — a per-side path that is a symlink or a regular file cannot be shadowed,
  so the launcher warns and the jail silently sees the host's copy.)*

Plus **OQ-GN1 · OQ-GN2 · OQ-GN4** in the guest-notch handoff, which are Mac-gated rather than
undecided, and are cited from 💬 7 above. *(OQ-GN3 left on 2026-09-02 — it asked whether the
Cachix cache had ever been pushed to, and the Actions log answered it: yes, and CI reads from it
too. Chasing it found the six CI `nix build` calls that were discarding the flake's substituter.)*

They are named and countable now, which is the point — a question with an ID can be promoted to a
row the day it starts blocking something, and demoted the day it stops. **That is the whole
difference between this list being 14 rows and being 104.**
