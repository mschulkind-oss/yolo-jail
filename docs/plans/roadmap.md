# Roadmap

**Status: 12 needing you · 4 ready · 0 in progress · 6 waiting · 0 broken · 3 icebox.**

Last updated **2026-09-03**. Counts are tallied from this file's contents, not asserted — one per
`### 💬` heading, one per top-level bullet elsewhere, and each bullet's glyph matches its section.

> [!IMPORTANT]
> **If a row disagrees with the doc it points at, trust the doc and fix the row.** This file groups
> questions that live in design docs; it is a routing table, not an authority.

**Live open questions across the whole corpus are countable, not estimated:**

```console
$ rg -c '^(#{2,4} |\s*[0-9]+[a-z]?\. |\s*[-*] )(<a id="[^"]*"></a> ?)?💬' docs/ --sort path
```

> [!NOTE]
> **The optional anchor-tag group is not decoration — it is a bug this count already had.** A first
> version omitted it and returned 86, silently missing all six of `local-model-endpoints.md`'s
> questions, whose headings begin `1. <a id="oq-lm1"></a>💬 …`. If you add a heading style, check
> that the count moves. This is one of **five** sweeps that keep the corpus honest — links,
> questions, SHAs, code paths, heading anchors — all in
> [`README.md`](README.md#keeping-this-corpus-honest--the-five-checks-so-they-are-re-runnable).

> [!WARNING]
> **A row and the questions it points at are counted twice, and that is a known bias.** A row is a
> *grouping* of questions that already live in their design docs — 💬 16 and its three live `OQ-DF*`
> are four entries for one decision. Worth saying out loud: the last time this count was quietly
> wrong, it was wrong by six.

> [!NOTE]
> **The old vocabulary, for anyone arriving from a doc that still uses it.** Until 2026-08-17 this
> file was a lettered queue — rows **B1 / B1b / B2 / B3 / B4**, **threads A–C**, IDs like **N3** and
> **S5**. Restructuring into states retired the letters, and several sibling docs still cite them.
> Where they went: **B-rows** → [`boundary-broker.md`](../design/boundary-broker.md) §7's own
> numbering; **Thread A** → [`retired-decisions.md`](../design/retired-decisions.md); **Thread C** →
> [`shipped-2026-08-12.md`](shipped-2026-08-12.md); **N3** → `nix OQ-1` in
> [`noncontainer-nix-environment.md`](../design/noncontainer-nix-environment.md); **S5** →
> [`BACKLOG.md`](BACKLOG.md) §Stage E. **Cite a state row or an OQ ID — never a letter.**

Rows below are the *blocking subset*, grouped by decision; the rest are named in
*What the roadmap does not cover* at the end, deliberately. One decision usually closes several
questions.

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

> [!NOTE]
> **This row aggregates three docs while naming one** (reconciled 2026-09-03, recounted 2026-09-04).
> `trust-paths.md` now holds **NO open questions**: TP3/TP4 retired 2026-09-03 under the evergreen
> ruling, **TP8 and TP9 ruled 2026-09-04**, and **TP7 RETIRED the same day because TP9 deleted its
> subject**. What is left in this row is routing to **OQ-X1** and **OQ-LP8** — whose two undelivered
> documentation requirements TP9 makes overdue, and whose **G2b residual TP9 makes moot**.
> The other two live elsewhere and are listed here for routing only: **OQ-X1** is
> [`pack-execution-trust.md`](../design/pack-execution-trust.md) `:316` (the doc this one partly
> supersedes), and **OQ-LP8 / G2b** is [`loophole-packaging.md`](../design/loophole-packaging.md)
> `:2516`, marked *mostly covered* in its overview. Read them there; this row is the routing table.

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

- ✅ **OQ-TP3 and OQ-TP4 — RETIRED 2026-09-03, not answered.** Both were npm-pinning questions, and
  npm no longer takes a pin: [`program-delivery.md`](../design/program-delivery.md) §3.5 rules an
  **agent dependency** evergreen, and every pack either question governed installs an agent CLI.
  TP3's inherited half is ruled as OQ-PD6; TP4's cost analysis (pinning in the manifest makes yolo's
  release cadence the ceiling on agent-CLI freshness) survives as an argument **for** evergreen.
- ✅ **OQ-TP8 — RULED 2026-09-04: ungated, both halves.** Pack `derive.lua` keeps running with no
  origin check, in-jail at boot and host-side under `yolo host -- <cmd>`. The leaning had wanted a
  host-half gate, and it failed a parity check: `host.go:458` folds each pack's **static**
  `kind: "env"` keys into the same process's environment one step EARLIER, ungated — so the derive
  computes a field the manifest can already state literally, and gating the computed path while the
  literal one is open is theatre. A pack also renders `config`/`skills`/`briefing` into the real home
  at that notch. The disclosure stays the commit pin (**OQ-LP8**), not a claim line.
- **OQ-X1** — does a digest-pinned installer script count, given its own fetches are not pinned?
  *(Sharpened: only embedded packs use installers today, and `packdecl` has no digest field at all —
  the scenario is unexpressible until OQ-BP5 lands one.)*
- ✅ **OQ-TP9 — RULED 2026-09-04: the fetched-pack approval prompt is THEATRE, deleted.** Selecting a
  pack means writing user-scope config as the host user — `packs` is inexpressible at workspace scope
  *by construction*, so an agent cannot add one — and a gate that refuses an actor who already passed
  a stronger one is what `gate-placement-principle.md` **Test 1** exists to delete. `userlayer.go`
  had already applied that test the same way to the sibling route. **Keep** `packs` user-scope-only
  (that half passes Test 1) and the startup disclosure banner. ⚠ **Corrected the same day:** the
  follow-on is OQ-LP8's two undelivered DOC requirements, not pin *enforcement* — a launch resolves
  from the local mirror, which only moves at `pack install`, so content is already frozen between
  installs; what deleting the gate does is make the lockfile **write-only at launch**. G2b is moot.
- ⛔ **OQ-TP7 — RETIRED 2026-09-04, subject deleted by TP9.** All six refusal sources gate on
  `p.MayAccessHost` alone, so with no approval there is no refusal for `yolo check` to fail to
  predict and no approve path to be unreachable from CI or offline. Its one durable finding: a future
  preflight predicting a launch refusal must **share** the gate, never copy it —
  `hostaccessgates_test.go` pins two gates and a third would satisfy it vacuously.

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
  freshness, 2026-09-02: the provider arc's own `packs/zai` overlay was deleted the same day —
  `3144fbed`, D3's resolution — so `packs/claude`'s `bedrock` overlay is the shipped contributor
  again, and it is sole on `claude/settings`, so still no collision. The new `profile` gate changes
  whether an overlay participates, not what happens when two active ones share a key.)*
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

> [!NOTE]
> **The 📦 companion row is gone as of 2026-09-03, and that is a filing correction rather than a
> change of plan.** It sat in "ready to build" while its own text said *"this row is now entirely
> gated on 💬 16"* — a row disagreeing with itself, in a section whose glyph means "no blockers".
> Everything in it that needed no ruling has shipped (`cc53b591` closed the tar-eviction race; C3
> made podman stream instead of writing tars). What is left is `OQ-DF2`/`OQ-DF3`/`OQ-DF4` below, and
> the standing warning survives with it: **do not start at delete-on-success** — that is OQ-DF2
> option (i), and the component doing the deleting is OQ-DF2's to name.

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

### 💬 20 — An addressed contribution that matches nothing: refuse, or report? **Code has now picked a side, and it is not yours**

📄 [`briefing-audiences.md`](../design/briefing-audiences.md) · the shipped behavior is
`internal/packload/agentaudience.go` + `AgentNames` (`internal/packload/footprint.go:731`)

**P3 makes an unenabled *name* fatal. Risk R1 describes an addressed contribution that matched no
destination as *reported*.** Different sentences about the same user mistake, and the difference is
whether a launch happens.

**The collision case:** a content pack ships `{from: "prose/codex.md", agents: ["codex"]}` into a
jail that selected only `claude`. `codex` is a real agent, spelled correctly — so P3's typo argument
does not obviously reach it, but the prose reaches nobody.

> [!WARNING]
> **⚠ On 2026-09-03 steps 3–7 shipped the FATAL reading for exactly this case, which is the opposite
> of the leaning recorded below, and it was merged without a ruling.** `AgentNames` is the vocabulary
> of **selected** packs and nothing wider, and its own docstring argues the position directly:
> *"from the jail's point of view `agents: ["cloude"]` and `agents: ["codex"]` in a jail that did not
> select codex are the same mistake."* It explicitly considered and rejected the wider-vocabulary
> source that would have made this a report. `AgentAudienceProblems` then refuses the launch and
> `yolo host apply`, and `integration/packaudience_test.go`'s second case pins it — its own comment
> says codex *"is a real agent and a real shipped pack — it is simply not selected."*
>
> Two consequences. **This row is now more urgent, not less**: the R1 reading is no longer the
> default-by-omission, so choosing it means changing shipped behavior and deleting a test that
> currently passes. And **the split the builder reported as "💬 20 resolved, both sentences intact"
> is a different split than the one below** — it draws the line at *in the selected set / not in the
> selected set*, where the leaning draws it at *exists anywhere / not enabled here*. Those agree on
> a typo and disagree on every real-but-unselected name.

_My leaning, unchanged and now contradicted by the code:_ **report, and keep the fatal for the
name.** *"`codex` is not a thing"* is a typo and stays fatal; *"`codex` is a thing you did not enable
here"* is a portability fact — the shape `env_sources` gets right by warning on a missing host file
rather than refusing. A content pack that travels between jails with different pack sets is the
ordinary case, and refusing it makes an addressed pack unusable anywhere but the machine it was
written on. But P3 is written broadly enough to read the other way, and this is a launch.

**What it decides:** whether an `agents` selector is a portability-safe declaration or a hard
requirement on the jail's pack set — and therefore whether addressed content packs are shareable.
**If you rule "report", the change is `AgentNames`' source set plus one integration case.**

**Answer:**
> _(empty — fill in when decided)_

# 📦 Up next

**Four items.** C4 and C5 are deliberately NOT here — their go/no-go is an explicit 🧊 row, and
queueing them before you call it would be queueing a question.

**Five items shipped on 2026-09-03** and left under the archiving rule: the host-launch gate
(now [`host-apply-staleness.md`](../design/host-apply-staleness.md), IMPLEMENTED),
briefing-audiences steps 3–7, the `packload.Embedded()` temp-dir leak, the launcher-template
splices, and the orphan-message cause. Two of the three small ones corrected the row that queued
them, and one merge decided 💬 20 in code — see that row.

**Ordering basis:** what unblocks the most other work first, then what is cheapest. The injection is
first because it is demonstrated and the fix is small; the removal act follows because it is the
largest reclaim available; the two repairs after that are cheap and independent.

- 📦 **`ShimContent` splices agent-editable text raw into a `/bin/sh` script — injection CONFIRMED
  by demonstration 2026-09-03.** `internal/entrypoint/shims.go:136,147,162` embed `msg`/`sug`
  verbatim inside `echo "…"`, declared at `:117-118` as *"no shell escaping (the frozen contract)"*.
  Those values arrive from `YOLO_BLOCK_CONFIG`, whose workspace half the code itself calls
  **agent-editable** (`:74-78`).

  A `message` of `oops"; touch <path>; echo "done` emits `echo "oops"; touch <path>; echo "done" >&2`
  and the `touch` ran.

  ⚠ **Severity is lower than that sounds, and the reason decides how it is fixed.** In-jail
  (`boot.go:437`) the privilege gain is **nil** — an agent that can edit the workspace config
  already has a shell in that jail. `yolo check`'s probe (`check/entrypoint.go:70`) generates into a
  temp dir and never executes it. **The one context that leaves a container is `darwin.go:50`**, the
  macos-user backend, which writes these shims into the `_yolojail` *host* account's home to be run
  as that account.

  One thing to settle while building: the docstring calls the raw splice a *frozen contract* and
  `shims_behavior_test.go` pins the emitted grammar. Quoting the values should not disturb that
  grammar — if it does, the contract needs a ruling rather than a workaround. The
  positional technique is already in the tree from the 2026-09-03 launcher pass (`shquote.Quote`
  cannot go inside `"…"`; the sentinel has to move to a bare position).

- 📦 **Program delivery §10 — the removal act, plus default-off autoprune.** 📄
  [`program-delivery.md`](../design/program-delivery.md) §10. The biggest reclaim available:
  **418 MB of cataloged npm orphans plus ~1.2 GB uncataloged**.

  ⚠ **It is the only destructive step, and it has a footgun confirmed live 2026-09-03:**
  `~/.local/bin/claude` is an **absolute symlink** into a versions dir
  (`~/.local/share/claude/versions/2.1.220`), so unlinking it strands **~1 GB** with nothing naming
  it. The act must follow an installer-kind orphan to its versions dir, or decline and say why —
  declining is defensible, silently stranding a gigabyte is not.

  The rest of §10 is blocked, not unscheduled — see 🔒.

- 📦 **`hostskills.Changed` reports "changed" for a symlinked source, forever.** `treeDigest`
  records link targets while `copyTree` materializes through them, so a source tree deployed by a
  dotfile manager never compares equal. Pre-existing — it already affects the `files` kind and the
  flat-skills archive gate — but as of 2026-09-03 it is also an **R3 hazard** for the host-launch
  gate ([`host-apply-staleness.md`](../design/host-apply-staleness.md) R3): a launch would prompt
  on every start and never converge. Found while building that gate and deliberately left alone
  there. Needs no ruling.

- 📦 **`Pack.Name` may not prefer the manifest over the directory basename.** Documented as
  *"config override, else manifest, else dir"* (`internal/packload/packload.go:31`), but a
  `file://` entry with no explicit `name` reported the source directory's basename even though its
  `pack.json` declared one — which made an audience refusal read `pack 002` instead of `pack house`
  (found 2026-09-03 via `integration/packaudience_test.go`, worked around there by naming the
  entry). Either the doc is wrong or the derivation is; whichever it is, `p.Name` feeds a lot of
  user-facing messages. Needs no ruling, just a look.

# 🔒 Waiting

- 🔒 **Program delivery §10 — the three steps that are blocked, not merely unscheduled.** 📄
  [`program-delivery.md`](../design/program-delivery.md) §10. The unblocked step is in 📦. ⚠ **Order reversed 2026-09-04 ([OQ-CP1](../design/agent-cli-copies.md#-oq-cp1--is-the-disk-justification-retracted-and-is-oq-pd15-reversed--resolved-2026-09-04)): evergreen ships BEFORE capture, carrying A7's V-axis prune; the disk justification that put capture first is retracted.**
  - **The user-scope gap receipt** and **obey** — blocked on 💬 2's `OQ-TP4`. Three of the gap
    receipt's seven decisions *are* that OQ's options (a)/(b)/(c), and
    [`trust-paths.md`](../design/trust-paths.md) forbids either doc retiring the other's ID
    unilaterally — so building it answers OQ-TP4 by implementation. **Obey reads an artifact the
    gap receipt cannot yet create**, so it cannot precede it. One line worth having when you rule:
    exactly one act changes behavior under obey — the cold-install branch. The poll is
    informational, the PINNED branch already compares offline, and `pack update` WRITES the record
    rather than reading it, so *"install obeys the record"* reads far broader than it is.
  - ~~**The installer capture** (§6.3, `OQ-PD10`) — not buildable here.~~ ⚠ **Wrong as of
    2026-09-04, and wrong about the premise rather than the schedule.** It IS buildable here:
    podman-in-podman gives this jail real containers, so `yolo capture` runs an installer in a real
    throwaway jail and `integration/capture_test.go` measures the result. **Slices one, two and
    three of six are landed** ([`install-capture.md`](install-capture.md)) — the store, the inner
    driver, and the `yolo capture <bin>` host act. What remains is slice four (materialize from the
    launcher — the slice that actually pays), five (remove + GC), and six (macos-user, which is
    genuinely not buildable here: it needs Seatbelt on real hardware). The one property still
    unbuilt from §6.3's prose is *"a jail that writes outside its binds is a finding the capture run
    reports"*: stray writes are left alone and not enumerated, because enumerating them needs a
    whole-home walk (install-capture slice 2, correction (e)).

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

  1. 💬 ~~**The Mac's config still uses the REMOVED `agents` key**~~ — **CLOSED 2026-09-03, and
     replaced by a narrower one.** Measured on the Mac: installed `yolo` is `0.8.0+881.ga6f61864`
     (HEAD — the "531 commits stale" reading is retired) and the config is on `packs`.
     `yolo check --no-build` under `YOLO_RUNTIME=macos-user` is **29 passed, 6 warnings, 0 failed**.
     What still stops a launch is the pack SOURCES: they are `file:///home/matt/.dotfiles/yolo-packs/…`,
     absolute **Linux** paths in a dotfiles tree shared with this host, and `/home/matt` does not
     exist on macOS — so resolution fails with `local pack … is not a directory` before the backend
     is reached. yolo expands neither `~` nor env vars in a local pack source, so one config cannot
     currently name one tree on both machines. Fix it in the dotfiles (that config already includes
     a machine-local `overrides.jsonc`) or in yolo. **Still your config, your call.**
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

  **And the loop cannot close on itself — measured 2026-09-03.** A macos-user jail cannot launch a
  macos-user jail: `sudo` cannot exec inside ANY Seatbelt sandbox (refused even under a bare
  `(allow default)` profile), and `sandbox_apply` refuses any profile that is not effectively
  identical to the active one — an equality constraint, so "hand the inner jail a stricter profile"
  is not available either. A helper OUTSIDE the sandbox spawning the inner jail on request DOES
  work mechanically, and was **proposed and rejected the same day**: it is the `docker.sock`
  antipattern (the jail asks a privileged daemon instead of holding a privilege), and on this
  backend the daemon cannot even tell WHICH jail is calling — every jail is the one `_yolojail`
  uid. What podman-in-podman actually gives is the opposite shape: the jail runs its OWN engine,
  so the host gains no request surface. The macOS analogue is a VM started from inside the sandbox
  (four SBPL rules, no daemon, unreproduced here), and the option needing no code at all is to
  develop on the Mac in a **podman** jail, leaving macos-user as the backend under test. 📄
  [`macos-revival-and-distribution-plan.md`](macos-revival-and-distribution-plan.md) §Self-hosting,
  **OQ-SH-1**.

- ✅ ~~**OQ-GR-1 — `find` is blocked outright while `grep` is blocked by flag.**~~ **ANSWERED
  2026-09-04, and it was never an oversight.** Recorded here because the row asked the question
  badly: it framed the asymmetry as unexamined, and it is not. 📄
  [`packs/guardrails/README.md`](../../packs/guardrails/README.md).

  **Both rules exist for one reason — the replacement is faster for the same work.** Not safety,
  not scope, not token cost. `fd` beats `find`, `rg` beats `grep -r`, and a block is how the
  faster tool actually gets used rather than merely being available.

  **`grep` is only half-blocked because `... | grep <foo>` is extremely common and is NOT what
  `rg` is better at.** Filtering a pipe is not a recursive search, so refusing it would cost a
  familiar tool and return nothing. `find` needs no equivalent carve-out because it has no
  equivalent common non-recursive use — and nothing in its syntax marks the recursive case, since
  it is recursive by nature and only has flags that LIMIT it.

  `allow_flags` shipped the same day as a wired, unused extension point, so "block `find` unless
  depth-limited" becomes expressible if anyone ever wants it — without that refactor changing any
  rule, which was the explicit instruction.

- 💬 **macos-user has no package floor and no provisioning stage, so four config keys render and
  install nothing.** 📄 [`macos-user-provisioning.md`](../design/macos-user-provisioning.md) —
  **OQ-P1 · OQ-P2 · OQ-P3 · OQ-P4**. A container jail gets tools two ways: an image floor of ~19
  baked packages (git, node, mise, ripgrep, fd…) and an imperative stage the launch runs inside it
  (`mise install`, the generated `~/.yolo-bootstrap.sh` that npm-installs LSP servers and MCP
  presets). This backend has NEITHER — only `packages:`, containing exactly what the user
  declared. So `mise_tools`, `lsp_servers`, `mcp_presets` and the lazy agent-CLI installers are all
  inert; four now warn and the agent-CLI case is still silent, which is the one that fails on a
  user's first real command rather than at launch. **The two halves are strictly ordered** — the
  stage cannot run without the floor, since `mise install` needs mise and the bootstrap script
  needs npm — so there is no partial credit. Four rulings needed: how much floor, GNU or BSD
  userland, where the stage's state lives given one shared home (which blocks on the home split),
  and whether the stage runs eagerly.

- 💬 **The macos-user home has one tier where it needs two, and content delivery just made it
  bite.** 📄 [`macos-user-home-tiers.md`](../design/macos-user-home-tiers.md) — **OQ-HT-1 ·
  OQ-HT-2 · OQ-HT-3**. `SandboxHome()` is the constant `/Users/_yolojail`, so the machine tier
  (credentials — correct, and the point of a dedicated account), the workspace tier (pack
  `state`, agent history) and the session tier are one directory. Two symptoms were static
  information leakage between workspaces and were warned about. The third is new and is a
  write-write RACE: since 2026-09-03 skills and briefings are composed and copied over that home
  on every entry, so a second workspace launching while the first runs replaces its briefing —
  per-project prose an agent is mid-session reading. The proposal is the two-tier structure the
  container backends already have, in the one account this backend has: a per-workspace home
  under `/Users/_yolojail/workspaces/<cname>`, with credentials reached by the symlinks
  `shared_credentials` already uses. **The trap is that a naive split repairs the workspace tier
  by breaking the machine one** — the single home IS the credential-sharing mechanism here — so
  the fix must restore both explicitly, and OQ-HT-2 asks what happens to the credentials already
  sitting in the old layout.

- 🔒 **Four manual checks are all that is left on a Mac, and they are written down.** 📄
  [`runbooks/macos-user-manual-checks.md`](runbooks/macos-user-manual-checks.md). Everything that
  could be automated was, on 2026-09-04: two darwin-gated harnesses run on any Mac with NO
  privilege and cover the generated home, a blocker actually refusing, the composed overlay
  landing and replacing a removed pack's subtree, the staging commands really executing, the mode
  bits, and the J2 fresh-inode rule. What is left needs root or a kernel — the privilege
  transition, Seatbelt actually loading, the `packages:` acceptance bar, and content reaching the
  agent — and it is four commands, not a project.

- 🔒 **On a Mac — three things need the hardware, and the first is a config rename.** *(The
  headline used to announce what had LEFT this row, which tells a reader nothing about what is in
  it. For the record: the two lib-farm assertions were never darwin assertions — they failed
  because the image build did, and both went green the moment `x86_64-darwin` could evaluate again.
  Nothing about the lib farm was wrong.)*

  Three items remain, all genuinely host-gated — and **before any of them, that Mac's config names
  its packs by Linux paths, so no `yolo` launches there at all** (see the sandbox row above; the
  `agents`-key blocker this line used to name was closed on 2026-09-03).
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
