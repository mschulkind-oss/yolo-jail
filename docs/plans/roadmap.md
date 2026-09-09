# Roadmap

**Status: 15 needing you · 0 ready · 0 in progress · 5 waiting · 0 broken · 3 icebox.**

Last updated **2026-09-08**. Counts are tallied from this file's contents, not asserted — one per
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
> Where they went: **B-rows** → [`boundary-broker.md` §7](../design/boundary-broker.md#7-what-i-would-build-in-order)'s own
> numbering; **Thread A** → [`retired-decisions.md`](../design/retired-decisions.md); **Thread C** →
> [`shipped-2026-08-12.md`](shipped-2026-08-12.md); **N3** → [`nix OQ-1`](../design/noncontainer-nix-environment.md#9-open-questions) in
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
> `trust-paths.md` closed every question it was carrying: TP3/TP4 retired 2026-09-03 under the evergreen
> ruling, **TP8 and TP9 ruled 2026-09-04**, and **TP7 RETIRED the same day because TP9 deleted its
> subject**. ⚠ **[OQ-TP10](../design/trust-paths.md#-oq-tp10--a-wrapped-plugins-hooks-reach-the-agents-lifecycle-and-appear-in-no-launch-banner)
> was OPENED 2026-09-04 by the TP9 build** — a wrapped plugin's hooks reach the agent's lifecycle
> and appear in no launch banner, which falsifies a sentence TP9's own answer wrote. It needs you. ✅ **[OQ-LP8](../design/loophole-packaging-overview.md#oq-lp8--how-does-an-execution-approval-survive-a-moving-pin--ruled-and-delivered-2026-09-04) closed 2026-09-04** — its two overdue documentation requirements are
> delivered ([overview](../design/loophole-packaging-overview.md#oq-lp8--how-does-an-execution-approval-survive-a-moving-pin--ruled-and-delivered-2026-09-04)):
> *following a mutable ref IS the trust decision*, and **tag pins are the documented shape** for a
> pack carrying code, both written into
> [the packs guide](../guides/migrating-to-packs-and-host-management.md)'s *Sharing a pack with other
> people*. **G2b is MOOT** — TP9 deleted the approval it would have anchored. What is left in this
> row is routing to **[OQ-X1](../design/pack-execution-trust.md#-oq-x1--does-a-digest-pinned-installer-script-satisfy-p1-given-its-own-fetches-are-not-pinned--retired-2026-09-04)**, which lives in
> [`pack-execution-trust.md`](../design/pack-execution-trust.md) (the doc this one partly
> supersedes). Read it there; this row is the routing table.

**Two rulings on 2026-08-18, both closing a finding by removing a mechanism rather than adding a
gate** — and one of them obviated a question rather than answering it:

- ✅ **[OQ-TP5](../design/trust-paths.md#decision-ledger) — no evergreen npm.** `install` obeys the lockfile, `update` is the only act that
  resolves a new version, and the hourly poll is downgraded to informational. **Built 2026-08-18**
  (`b3a29ad8`), minus the pin it has nowhere to record — which is [OQ-TP4](../design/trust-paths.md#decision-ledger) below. ⚠ **REVERSED IN
  CODE 2026-09-04 for the agent class**, by [OQ-PD3](../design/program-delivery.md#decision-ledger)'s narrowing and [OQ-PD12](../design/program-delivery.md#decision-ledger): `_poll_and_report` is
  deleted and an unpinned agent package is updated by the launcher at the user's own invocation.
  The ruling still holds for a PROJECT dependency, and for a PINNED package on either side.
- ✅ **[OQ-TP6](../design/trust-paths.md#decision-ledger) — a refused contribution refuses the launch.** No partial packs: fix it, remove it, or
  approve it. **Built 2026-08-18** (`6385dfbb`). Both carry release-note entries.
- ✅ **[OQ-TP2](../design/trust-paths.md#decision-ledger) — nothing explicit.** Agent context needs no gate and no separate disclosure: the
  lockfile's commit pin closes over the whole tree, prose included. *Inherits [OQ-LP8](../design/loophole-packaging-overview.md#oq-lp8--how-does-an-execution-approval-survive-a-moving-pin--ruled-and-delivered-2026-09-04)/G2b — the pin is
  recorded and never consulted at launch, so it covers this on paper until enforcement lands.*
- ✅ **[OQ-TP1](../design/trust-paths.md#decision-ledger) obviated by TP6.** There is nothing to carry into a jail if no jail starts, so the
  origin-gate finding stops being a broken guarantee. **The fatal has since shipped** (`6385dfbb`),
  so this is now enforced rather than merely defined — the caveat this row used to carry is spent.

What is still open:

- ✅ **[OQ-TP3](../design/trust-paths.md#decision-ledger) and [OQ-TP4](../design/trust-paths.md#decision-ledger) — RETIRED 2026-09-03, not answered.** Both were npm-pinning questions, and
  npm no longer takes a pin: [`program-delivery.md` §3.5](../design/program-delivery.md#35-the-second-axis-who-the-dependency-serves-amendment-2026-09-03) rules an
  **agent dependency** evergreen, and every pack either question governed installs an agent CLI.
  TP3's inherited half is ruled as [OQ-PD6](../design/program-delivery.md#decision-ledger); TP4's cost analysis (pinning in the manifest makes yolo's
  release cadence the ceiling on agent-CLI freshness) survives as an argument **for** evergreen.
- ✅ **[OQ-TP8](../design/trust-paths.md#-oq-tp8--pack-shipped-lua-runs-ungated-on-both-sides-of-the-boundary--is-that-a-ruling-or-an-accident--resolved-2026-09-04) — RULED 2026-09-04: ungated, both halves.** Pack `derive.lua` keeps running with no
  origin check, in-jail at boot and host-side under `yolo host -- <cmd>`. The leaning had wanted a
  host-half gate, and it failed a parity check: `host.go:458` folds each pack's **static**
  `kind: "env"` keys into the same process's environment one step EARLIER, ungated — so the derive
  computes a field the manifest can already state literally, and gating the computed path while the
  literal one is open is theatre. A pack also renders `config`/`skills`/`briefing` into the real home
  at that notch. The disclosure stays the commit pin (**[OQ-LP8](../design/loophole-packaging-overview.md#oq-lp8--how-does-an-execution-approval-survive-a-moving-pin--ruled-and-delivered-2026-09-04)**), not a claim line.
- **[OQ-X1](../design/pack-execution-trust.md#-oq-x1--does-a-digest-pinned-installer-script-satisfy-p1-given-its-own-fetches-are-not-pinned--retired-2026-09-04)** — does a digest-pinned installer script count, given its own fetches are not pinned?
  *(Sharpened: only embedded packs use installers today, and `packdecl` has no digest field at all —
  the scenario is unexpressible until [OQ-BP5](../design/broker-as-a-pack.md#open-questions) lands one.)*
- ✅ **[OQ-TP9](../design/trust-paths.md#-oq-tp9--is-the-fetched-pack-approval-prompt-a-gate-or-theatre--resolved-2026-09-04) — RULED 2026-09-04: the fetched-pack approval prompt is THEATRE, deleted.** Selecting a
  pack means writing user-scope config as the host user — `packs` is inexpressible at workspace scope
  *by construction*, so an agent cannot add one — and a gate that refuses an actor who already passed
  a stronger one is what `gate-placement-principle.md` **Test 1** exists to delete. `userlayer.go`
  had already applied that test the same way to the sibling route. **Keep** `packs` user-scope-only
  (that half passes Test 1) and the startup disclosure banner. ⚠ **Corrected the same day:** the
  follow-on is [OQ-LP8](../design/loophole-packaging-overview.md#oq-lp8--how-does-an-execution-approval-survive-a-moving-pin--ruled-and-delivered-2026-09-04)'s two undelivered DOC requirements, not pin *enforcement* — a launch resolves
  from the local mirror, which only moves at `pack install`, so content is already frozen between
  installs; what deleting the gate does is make the lockfile **write-only at launch**. G2b is moot.
  **Both DOC requirements delivered 2026-09-04** (packs guide, *Sharing a pack with other people*),
  which closes [OQ-LP8](../design/loophole-packaging-overview.md#oq-lp8--how-does-an-execution-approval-survive-a-moving-pin--ruled-and-delivered-2026-09-04).
- ⛔ **[OQ-TP7](../design/trust-paths.md#-oq-tp7--yolo-check-cannot-predict-the-fatal-refusal-and-the-refusal-names-a-fix-that-needs-a-tty-and-a-network--retired-2026-09-04) — RETIRED 2026-09-04, subject deleted by TP9.** All six refusal sources gate on
  `p.MayAccessHost` alone, so with no approval there is no refusal for `yolo check` to fail to
  predict and no approve path to be unreachable from CI or offline. Its one durable finding: a future
  preflight predicting a launch refusal must **share** the gate, never copy it —
  `hostaccessgates_test.go` pins two gates and a third would satisfy it vacuously.

### 💬 5 — Boundary broker

📄 [`boundary-broker.md`](../design/boundary-broker.md) — **OQ-A · OQ-C · OQ-E · [OQ-B1b](../design/boundary-broker.md#9-open-questions-for-the-maintainer)**

**OQ-A** sizes the whole project (if synchronous-only suffices, most of [§7](../design/boundary-broker.md#7-what-i-would-build-in-order) step 3 never gets
written). **OQ-C** is a real API-shape decision: does the jail see the *result* or just success —
i.e. does every verb need a response schema, or none? The security half of **OQ-E** is settled
(authority stays in the unix socket); only its packaging half — which client the human reaches for —
is live. **[OQ-B1b](../design/boundary-broker.md#9-open-questions-for-the-maintainer)** sizes B1b alone: vendor unYOLO's ~2,100-line MIT, stdlib-only policy engine at a
pinned SHA, or re-derive it. *(B1b was created as an ID on 2026-08-23 — [§10.6](../design/boundary-broker.md#106-recommendation--build-b1b-vendor-the-policy-engine-do-not-adopt-gh-broker) had been calling it
"the maintainer's call, see the B1b row in roadmap.md", a row that never existed, while this file
cited the ID back at the doc. Neither end resolved.)*

**Both of this project's upstream blockers dissolved on 2026-09-02** — nix [OQ-1](../design/noncontainer-nix-environment.md#9-open-questions) answered (host =
run) and auth [OQ-1](../design/agent-auth-modes.md#12-decision-ledger) measured — so nothing gates these four questions but themselves. The 2026-09-02
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
H1 are recorded dead in the doc. **[OQ-L1](macos-revival-and-distribution-plan.md#open-questions-blocking)** explicitly blocks Track L part 2. **[OQ-GN1](handoff-guest-notch-macos.md#9-open-questions) · [OQ-GN2](handoff-guest-notch-macos.md#9-open-questions) · [OQ-GN4](handoff-guest-notch-macos.md#9-open-questions)** are
new (2026-08-23) —
**[OQ-GN3](handoff-guest-notch-macos.md#9-open-questions) was answered 2026-09-02** from the Actions log (the Cachix push happened AND the
cache is being read; D4 is down to the Mac download proof) —
in the guest-notch handoff — which now says plainly that its item 1.4 is only *half* answered: the
sandbox reads the staged pack root and runs the toolchain, so what is untested is the
`sudo -u _yolojail` staging step above it, not the confinement.

**And the one with a clock on it, which arrived here by losing its parent.** It was carried by the
🛑 nightly entry and cited [`image-staging-vs-baking.md` "A failed build is fatal"](../reference/image-staging-vs-baking.md#a-failed-build-is-fatal) —
a section about the silent-fallback defect that **never mentions darwin**. Its real home is
[`macos-support-matrix.md` §0](../research/macos-support-matrix.md#0-the-platform-deadline--x86_64-darwin-is-on-a-clock), which now carries it; this row
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

📄 [`BACKLOG.md`](BACKLOG.md) §Stage E — **S5 · OQ-CO · [OQ-S4](BACKLOG.md#-oq-s4--should-the-jail-narrow-its-skills-fan-out-to-match-the-host) · [OQ-E4](BACKLOG.md#-oq-e4--do-stateful-surfaces-get-comment-preservation-too) · E1 · E2 · E5** · plus
[`pack-host-management-plan.md`](pack-host-management-plan.md) **OQ-B**, folded in from the retired
💬 12 (2026-09-02) because it was never a separate decision — see the E1/E2 bullet

**These used to be "born in this file with nowhere else to live", which was half true and is now
fixed.** E1/E2/E5 always had a home in Stage E; **S5** and **OQ-CO** genuinely existed nowhere but
one line of this file — the exact thing this file's own rule forbids. All seven now carry stakes, a
leaning and an empty Answer in Stage E.

- **S5** is the only one that is a live gap rather than a preference: a jail resolves a skill-name
  collision **silently**, where `yolo host apply` refuses. Warn at launch, fail `yolo check`, or refuse
  the boot.
- **E1 + E2 + [`pack-host-management-plan.md`](pack-host-management-plan.md) OQ-B are ONE decision** — the `0o444`-vs-`:ro`
  asymmetry. **Four instances, not three** (2026-08-23): `composed-file-permissions.md` [§7.4](../design/composed-file-permissions.md#74-what-this-means-for-host_files-four-modes) is the
  fourth, and it is cross-linked rather than given its own ID, because minting a fourth name for one
  question is how a decision becomes four decisions. Decide them together or none.
- **OQ-CO and [OQ-S4](BACKLOG.md#-oq-s4--should-the-jail-narrow-its-skills-fan-out-to-match-the-host) are the same question asked of different kinds:** should the two notches agree?
  One is `config-overlay`'s silent last-one-wins; the other is whether a pack's `into` **narrows**
  skills delivery or only adds to it — the jail and the host answer differently today. *(OQ-CO
  freshness, 2026-09-02: the provider arc's own `packs/zai` overlay was deleted the same day —
  `3144fbed`, D3's resolution — so `packs/claude`'s `bedrock` overlay is the shipped contributor
  again, and it is sole on `claude/settings`, so still no collision. The new `profile` gate changes
  whether an overlay participates, not what happens when two active ones share a key.)*
- **[OQ-E4](BACKLOG.md#-oq-e4--do-stateful-surfaces-get-comment-preservation-too)** is the ~15% of E4 that did not ship: do `stateful` surfaces get comment preservation?
  `rmw` preserves, `computed` correctly does not, `json` is provably vacuous.

*(**E3 has left this list — it shipped 2026-08-15**, `29ccf212`, and both this file and the backlog
row were still calling it open. And **E4 is not a question**; only its `stateful` residue is, which
is why the list cites [OQ-E4](BACKLOG.md#-oq-e4--do-stateful-surfaces-get-comment-preservation-too) and not E4.)*

### 💬 10 — `yolo check` tells you about the wrong machine, in three places and one vocabulary

📄 [`broker-ca-and-nested-hosts.md`](../design/broker-ca-and-nested-hosts.md) — **[OQ-3](../design/broker-ca-and-nested-hosts.md#7-open-questions)**

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

📄 [`package-nested-attribute-paths.md`](../design/package-nested-attribute-paths.md) — **[OQ-1](../design/package-nested-attribute-paths.md#8-open-questions)**

This sat in 📦 as *"designed, questions answered, no blockers"* and it is none of those. Its doc is
`**Status:** DESIGN SKETCH, 2026-08-22. Nothing built.` — **still true, re-verified 2026-09-02**
(`packageNameRe` is still single-dot, `parseDottedSpec` unchanged, no resolver anywhere in
`flake.nix`; one worked example — `llvmPackages_16` — has since been removed from the pinned
nixpkgs and the doc notes a substitute) — and **[OQ-1](../design/package-nested-attribute-paths.md#8-open-questions)** is the resolver's central rule: how a dotted path resolves when a
derivation output and a nested collection member claim the same name. It carries a leaning and an
empty Answer, so it gates the whole item rather than one corner of it.

**One thing shipping this costs that the doc did not price.** The refusal it quotes
(`60376fed`, *"a `packages` entry naming a nixpkgs COLLECTION is refused by name"*) is quoted
**truncated**; the full message ends with a normative sentence this design **reverses** — *"A
collection member is NOT selectable from `packages`: use the member's own top-level attribute … and
drop `<entry>`."* That is advice, not a diagnostic, so shipping means **rewriting the message**, not
just narrowing when the throw fires.

### 💬 14 — Pack-shipped binaries: the capability the broker sprint promised and did not finish

📄 [`broker-as-a-pack.md`](../design/broker-as-a-pack.md) — **[OQ-BP5](../design/broker-as-a-pack.md#open-questions) · [OQ-BP6](../design/broker-as-a-pack.md#open-questions)**

**This row exists because the sprint ended and these two did not.** [OQ-BP1](../design/broker-as-a-pack.md#decision-ledger) ruled that the broker's
move and the pack-shipped-binary capability ship **together**; what actually landed on 2026-08-19 was
the move, on a **baked** daemon — which [§3.1](../design/broker-as-a-pack.md#31-what-is-actually-unresolved-here) explicitly permits for an official pack, so nothing is
broken. What is owed is the capability itself, and it is owed to the *next* pack, not to the broker.

- **[OQ-BP5](../design/broker-as-a-pack.md#open-questions)** — download-with-digest only, or also a declared build step? They are not symmetric: a
  download satisfies P1 (the digest *is* what runs); a build generally cannot, so what runs is
  decided at install time by whatever toolchain the machine has.
- **[OQ-BP6](../design/broker-as-a-pack.md#open-questions)** — may a **fetched** pack ship a *host-side* daemon binary? Refusing it while permitting
  a fetched pack's arbitrary `host_daemon.cmd` would block the declarative form of a capability and
  permit the imperative one — the shape [OQ-LP14](../design/loophole-packaging-overview.md#oq-lp14--the-subset-cannot-say-a-socket-in-this-sessions-runtime-dir--resolved-2026-08-17--the-rule-is-withdrawn) already suffers from. *(The premise is now verified
  with a file:line: a fetched `host_daemon.cmd` really is approvable today,
  `loopholesource.go:258-310`.)*

⚠ **The cost [OQ-BP1](../design/broker-as-a-pack.md#decision-ledger) put on the critical path is still unpaid**: the release process has to produce
the matrix a manifest's `platforms` declares. Declaring more than you build turns *"unsupported
here"* into *"supported, missing"* (`broker-as-a-pack.md` [§9](../design/broker-as-a-pack.md#9-risks)).

---

### 💬 15 — Backend parity: the census, and whether macos-user gets briefings at all

📄 [`backend-parity.md`](../design/backend-parity.md) — **OQ-BP-1 · OQ-BP-2 · OQ-BP-3 · OQ-BP-4**

**Born from issue #39 and the sweep behind it.** Fourteen of the twenty-one defects are fixed or
warned (that doc's [§5](../design/backend-parity.md#5-what-is-already-fixed-2026-08-24) is the table); what is left is a decision about the mechanism, not about any
one bug. *(2026-09-02: the doc's [§6](../design/backend-parity.md#6-the-second-shared-fix-compose-the-briefing-from-what-was-applied) briefing fix turned out to have SHIPPED the day it was
written — `28ddea11` — with the doc never updated; it now says so, and OQ-BP-1's "after [§6](../design/backend-parity.md#6-the-second-shared-fix-compose-the-briefing-from-what-was-applied)"
sequencing condition is discharged. The census is the whole remainder.)*

⚠ **The count grew after the sweep ended, and that is the most useful fact in this row.** It was
seventeen. A class test written for three known instances failed immediately on a fourth nobody had
looked for — the host `~/.config/nvim` bind, wrong since long before any of this. Twenty of the
twenty-one were found by a human noticing; one was found by a single narrow invariant over a single
argv shape. **That is OQ-BP-1's case, measured rather than argued** ([§5.2](../design/backend-parity.md#52-the-rule-that-had-no-home)).

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
> made podman stream instead of writing tars). What is left is [`OQ-DF2`](../design/minimal-disk-footprint.md#112-open-questions)/[`OQ-DF3`](../design/minimal-disk-footprint.md#112-open-questions)/[`OQ-DF4`](../design/minimal-disk-footprint.md#112-open-questions) below, and
> the standing warning survives with it: **do not start at delete-on-success** — that is [OQ-DF2](../design/minimal-disk-footprint.md#112-open-questions)
> option (i), and the component doing the deleting is [OQ-DF2](../design/minimal-disk-footprint.md#112-open-questions)'s to name.

📄 [`minimal-disk-footprint.md` §11.2](../design/minimal-disk-footprint.md#112-open-questions) —
**~~[OQ-DF1](../design/minimal-disk-footprint.md#112-open-questions)~~ (ruled 2026-08-25) · [OQ-DF2](../design/minimal-disk-footprint.md#112-open-questions) · [OQ-DF3](../design/minimal-disk-footprint.md#112-open-questions) · [OQ-DF4](../design/minimal-disk-footprint.md#112-open-questions)**

**This row is the other half of the 📦 disk item below, and it exists because of what [OQ-5](../reference/image-staging-vs-baking.md#why-its-this-way) did and
did not settle.** You ruled that the cached tars are a **bug** and that yolo may delete them without
`--apply`. That settles the *premise* and the *goal*; it does not say which component does the
deleting, how far into podman's shared image store yolo may reach, or whether *"minimal"* is ever
written down as a number. Each carries stakes, a leaning and an Answer block in the doc.

**One of the four is now closed.** **[OQ-DF1](../design/minimal-disk-footprint.md#112-open-questions)** — the retention floor — you ruled the same day:
***"stream, keep zero tars."*** That is what C3 implements (shipped 2026-08-25): on podman the load
path writes no tar at all, `cache/images` stays empty on success, and there is no retention knob to
default. It went past the doc's own leaning, which had asked for an opt-in for the disconnected case.
**What it did not cover:** Apple Container, which must keep writing a file because its converters
interpolate a path — and the pre-C3 backlog, which **as of 2026-09-02 is gone from this jail's
nested cache** (3 tars / 10 GiB, the keep-3 fingerprint of a manual `yolo prune --apply`; the
host-side 125 GiB cache is not observable from here). Two new facts from that re-measurement, both
in [`minimal-disk-footprint.md` §2.4](../design/minimal-disk-footprint.md#24-re-measured-2026-09-02--the-backlog-is-gone-here-and-the-device-kept-filling-anyway): the manual recovery
tool demonstrably works (field evidence for [OQ-DF2](../design/minimal-disk-footprint.md#112-open-questions)'s leaning), and **the device lost another
~92 GiB of free space in the same eight days anyway** (87 % full, 516 GiB free) with the nix store
up only 6 GiB — the live growth driver is in **none of the three ledgers**, which is a new input to
[OQ-DF4](../design/minimal-disk-footprint.md#112-open-questions)'s budget question.

**Why the remaining three are not one more thing to get to eventually:** **[OQ-DF3](../design/minimal-disk-footprint.md#112-open-questions) blocks [§10](../design/minimal-disk-footprint.md#10-sequencing--what-i-would-build-in-order)'s first
step**, which is the best bytes-per-effort in the whole doc and is otherwise unblocked — reclaiming
the superseded podman images yolo's own filter structurally cannot see. It is also **the question
that gates the rule replacing `--keep-images 2`**, which C2 made live for the first time: C2 shipped
the *safety* half (dedup by image ID, plus a liveness veto so `prune --apply` cannot force-remove
another workspace's running image), and `4064f720` then made that veto **fail safe** — an unreadable
load ledger now declines the sweep instead of vetoing nothing. **So [OQ-DF3](../design/minimal-disk-footprint.md#112-open-questions) is no longer asking
whether a veto is needed.** What is unruled is the RETENTION RULE and the REACH into a podman store
shared with your non-yolo work — and the retention half now has a price attached: a coexisting
content-tagged image measures **2.836 GB unique** unless it is a same-store-path re-stream, which is
91.36 kB ([`image-staging-vs-baking.md` "Cost model"](../reference/image-staging-vs-baking.md#cost-model)). **[OQ-DF2](../design/minimal-disk-footprint.md#112-open-questions)** decides which
component does the deleting, which is why the 📦 row below is still scoped to the mechanism rather
than to any number.

### 💬 17 — Mistyped names return `[PASS]`: mostly closed by the provider arc; the buried channel remains

📄 [`reference-mismatch-diagnostics.md`](../design/reference-mismatch-diagnostics.md) —
**[OQ-RM1](../design/reference-mismatch-diagnostics.md#9-open-questions) (narrowed) · [OQ-RM2](../design/reference-mismatch-diagnostics.md#9-open-questions) · [OQ-RM3](../design/reference-mismatch-diagnostics.md#9-open-questions) · [OQ-RM4](../design/reference-mismatch-diagnostics.md#9-open-questions)** · executes the amended
[`stringly-typed-references-principle.md`](../design/stringly-typed-references-principle.md)

**You ruled the premise on 2026-08-30, and the provider arc built most of it within 72 hours** —
[§7](../design/reference-mismatch-diagnostics.md#7-sequencing-by-user-visible-payoff) steps 2, 3 and 6 shipped (selection-key validation, the `wire_api` enum, the `base_url`
credential refusal, and the credential preflight — the last with a deliberate selected-pack
rescoping the doc now records). **The 2026-08-30 reproduction this row used to carry is dead**: the
same config now yields three `[FAIL]`s, and the docs' census flips three rows to Reached
(re-verified 2026-09-02; both docs updated).

**What is left is exactly two things:**

- **The buried-warning channel ([§7](../design/reference-mismatch-diagnostics.md#7-sequencing-by-user-visible-payoff) step 1) — unshipped, needs no ruling, and it is the
  highest-payoff item in the doc.** `yolo check` still has two diagnostic channels and the summary
  counts one: bare `Warning:` lines from config resolution and loophole discovery — including the
  best mismatch diagnostic in the tree, the supersession did-you-mean — are invisible to the one
  line a user reads (`reporter.go:84-89`, still non-counting). **Queued 📦 below.**
- **The supersession relocation ([§7](../design/reference-mismatch-diagnostics.md#7-sequencing-by-user-visible-payoff) step 4) and its skew message (step 5)**, gated on **[OQ-RM2](../design/reference-mismatch-diagnostics.md#9-open-questions)**
  (refuse the launch vs. refuse the pack — the widest-blast-radius change left: an unmatched
  `supersedes` currently warns and keeps running, after this it stops the launch) and **[OQ-RM3](../design/reference-mismatch-diagnostics.md#9-open-questions)**
  (lazy vs. eager two-hash computation). **[OQ-RM1](../design/reference-mismatch-diagnostics.md#9-open-questions)** is narrowed by events — steps 2/3 already make
  `check` exit non-zero, so it now decides only the launch-only checks — and **[OQ-RM4](../design/reference-mismatch-diagnostics.md#9-open-questions)** (an escape
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

### ✅ 21 — Shutdown-delay fixes: the six candidates the timing spans exist to name

📄 [`perf-logging.md`](perf-logging.md) (plan; design linked within) · opened 2026-09-06 by the
perf-logging build · **triaged and closed 2026-09-08**

The maintainer's symptom — a 30+s wait between the agent TUI exiting and the shell prompt
returning, with nothing naming the culprit — is now instrumented (`--timing`/`--verbose`, host
spans, podman-cleanup attribution). It surfaced six fix candidates, and **none of them was an open
question**, which is what the triage found: four are decided work waiting on a span rather than a
ruling, and two were not questions at all.

**Answer (2026-09-08):**
> **Nothing here is waiting on a person.**
> - **The rename is RULED and BUILT** — `YOLO_PROFILE` → `YOLO_JAIL_TIMING`
>   ([D13](../reference/perf-logging.md#why-its-this-way), `7a35852d`). The premise that had blocked it
>   — "a host↔jail contract needing coordination across the deploy-skew boundary" — was false:
>   the launcher emits the pair and also generates the bash it belongs to, so both halves move in
>   one commit.
> - **`--verbose`'s vocabulary was always a policy**, not a pending decision
>   ([D14](../reference/perf-logging.md#why-its-this-way)): the first non-timing diagnostic that wants a
>   gate decides it, and nobody can usefully answer that earlier.
> - **The other four are deferred work with named triggers**
>   ([the deferred fixes](../reference/perf-logging.md#deferred-fixes-each-with-the-trigger-that-fires-it)): the tty
>   proxy's unguarded drain, the unbounded `podman ps` in `stopLoopholes`, `hostservice`'s
>   unbounded `inFlight.Wait()`, and serial loophole teardown. Each fires on a span, not on a
>   ruling — and **the fourth currently points AWAY from the work**: the first real runs measured
>   the whole `shutdown.*` chain at **0.045 s**.
>
> The reusable lesson the triage left behind (the design has since been distilled into [its as-built reference](../reference/perf-logging.md), which does not carry it): an
> entry belongs in Open Questions only if a human's answer changes what gets built. "Do X once the
> span shows Y" is a work queue, and parking it in a question list buried the one entry that did
> need a person.

### 💬 22 — The load sentinel is used as a liveness oracle, and it killed four jails

📄 [`the-load-sentinel-is-not-a-liveness-oracle.md`](../design/the-load-sentinel-is-not-a-liveness-oracle.md) ·
opened 2026-09-08 by the incident

A ten-entry list of recently-LOADED nix store paths is the protection two reapers consult. Only a
LAUNCH appends to it, so a jail that is running but not relaunching ages out of the window while
still in use — and on 2026-09-08 the automatic image reap `rmi -f`'d the images of four jails that
had been up 3–4 days, taking the containers with them. The image half is FIXED (`feddc5e0`: ask
`podman ps`, and never force-remove). What needs you is how far the same correction travels:
**[OQ-LS1](../design/the-load-sentinel-is-not-a-liveness-oracle.md#11-open-questions)** (does the
nix GC-root reaper get the same veto, or is losing a root an acceptable rebuild?),
**[OQ-LS2](../design/the-load-sentinel-is-not-a-liveness-oracle.md#11-open-questions)** (should a
declined sweep say so, or is silence about not-acting how the 404 GiB accrued?), and
**[OQ-LS3](../design/the-load-sentinel-is-not-a-liveness-oracle.md#11-open-questions)** 🤷
(`keep=2` is a pure undo buffer now that safety no longer rests on it — still the number you want?).

The doc also records a contradiction worth fixing on sight: `imageroots.go` justifies a destructive
guard with "the image a live container runs is always the most recent sentinel entry", which
`autoload.go` refutes in the same repository.

**Answer:**
> _(empty — fill in when decided; the three OQs can be ruled separately)_

### 💬 23 — Layer-aware image delivery: 3.4 GB re-shipped to deliver 27 MB

📄 [`layer-aware-image-delivery.md`](../design/layer-aware-image-delivery.md) · opened 2026-09-08
off the first real `--timing` measurements

`image.stream_load` is **81.0s of a 96.1s image load** on the maintainer's host (`image.nix_build`
is 14.9s — the build is not the problem). Measured cause: the image is 99 layers / 3.47 GB, the
customisation layer is 27.2 MB — **0.78%** — and a docker-archive tar is sequential, so podman
re-ingests every unchanged base layer on every store-path change. Verdict in the doc is
nix2container + a pinned layer plan + `skopeo copy`; `streamLayeredImage` has no `layers` argument,
so the cheap "just reorder" alternative does not exist.

The go/no-go is
**[OQ-LI1](../design/layer-aware-image-delivery.md#9-open-questions)**: `nix2container` is NOT in the
pinned nixpkgs and its `nix:` transport is a patched skopeo source build absent from
`cache.nixos.org` — is a third-party flake input acceptable on the launch path? Four more
(Apple Container's source, the extras tier, `created` becoming a constant, the rollback window) are
downstream of that answer. One interaction to know: nix2container rejects `created = "now"`, which
`flake.nix` passes, and pinning it to a constant collapses the `CreatedAt` sort key row 22's
reaper uses.

**Answer:**
> _(empty — fill in when decided)_

# 📦 Up next

**Empty.** The one item that was here shipped the same day it was filed. C4 and C5 are
deliberately NOT here — their go/no-go is an explicit 🧊 row.

**The slug-escaping mount bug is FIXED (2026-09-05)** and has left this section. A `reads-host`
grant with no `into` is keyed on the pack's STAGED DIRECTORY on both sides now — one new accessor,
`packload.Pack.StagedSlug`, read by `packload.hostSourceFor` (the jail's host-layer read) and
`run.hostFileArgs` (the host's mount destination) — so the two halves evaluate ONE EXPRESSION over
one string rather than two strings that happen to agree for the names yolo ships.
⚠ **None of the row's three candidates was taken**, and the fourth beats all of them on their own
terms: `p.Name` stays the user's handle, so no message moves (beats (c)); no name is refused
(beats (a)); and the shared escaping that keeps the pack and `host_files` staging namespaces from
colliding is untouched (beats (b)). The third `CtxPath` call site, `HostFileConflicts`, deliberately
did NOT move: it is reporting, both sides of its own comparison use one key, and `Name` is the only
string safe to print there — the check is lint-shaped, and a lint-time pack is loaded out of a
throwaway staging dir whose basename names the temp tree. That trap is closed rather than
documented away: `pack lint`, `pack footprint` and `yolo check` now stage into
`<temp>/<the pack's dir or slug>`, so `StagedSlug` never names a temp dir.
**Measured end-to-end in a nested jail, both directions:** the fixed launcher mounts
`/ctx/host-house_5frules/settings.json` and the composed surface carries the user's own key; the
previous launcher against the same image mounts `/ctx/host-house_rules/` and the surface loses that
key in silence — the fail-open read, reproduced. **No shipped pack's mount path moved**: all
fifteen names are slug-clean, and the only two `reads-host` grants in the tree (`packs/claude`,
`packs/pi`) set `into`, which routes around the pack key entirely.

**Three rows closed 2026-09-05:** the `ShimContent` injection (all four vectors, not the two the
row named), `hostskills.Changed` on a symlinked source, and `Pack.Name` — which turned out to be
the doc rather than the derivation.

**The `Pack.Name` row closed 2026-09-05, and it was the DOC that was wrong** — the second of the
two outcomes it named, so the code is untouched. A pack's effective name comes from the config
line (its explicit `name`, else the last segment of the source address), never from `pack.json`,
and it has to: it is simultaneously the staging dir (`PackEntry.Slug`, so `packstage` rule 3 and
the pack-drop prune key on it), the handle `yolo pack ls` prints and `yolo pack explain` takes,
and — through `packload.CtxPath` — the `/ctx` dir a `reads-host` grant with no `into` is mounted
at, which the jail re-derives from the STAGED DIRECTORY because that is all it has. Every one of
those is fixed from the config line alone, before a git source is fetched or any manifest is read.
`packload.LoadDir`'s manifest rung is real but unreachable: config lowering fills the name in
first, at every production call site. Corrected in `packload.Pack`, `packdecl.Manifest`,
`config.PackEntry`, `pack-system.md` [§2](../design/pack-system.md#2-the-manifest-contributes) and `config-ref`; pinned by
`run.TestConfiguredPackNameComesFromTheAddressNotTheManifest` (the reproduction, on the real
staging path), `run.TestStagedPackNameIsWhatTheJailWillDerive`,
`config.TestEveryLoweredPackEntryCarriesAName` and
`packload.TestEmbeddedPackManifestNamesMatchTheirDirs`. The
`integration/packaudience_test.go` workaround is GONE — its fixture now stages from a
`house-rules/` dir whose manifest says `house`, so the refusal naming `house-rules` is the
end-to-end measurement.

⚠ **The look found a second defect**, a different bug in the same derivation and one that DOES
move a mount path: the `Pack.Name` row was replaced by the slug-escaping row, which was then built
and shipped the same day — see the paragraph at the head of this section.

**The `ShimContent` shell injection is FIXED (2026-09-05)** and has left this section.
`msg`/`sug` are now `shquote.Quote`'d into a bare argv word after `echo` (`echoStderr` in
`internal/entrypoint/shims.go`), which is the same splice contract the 2026-09-03 launcher pass
wrote on `npmLauncherTemplate`. **The emitted grammar did NOT have to move**, so the ruling the row
asked for was not needed: same lines, same order, same `>&2`, same `exit 127`, and the only change
to the script is the literal's delimiter (`"…"` → `'…'`). `shims_behavior_test.go` is untouched and
green, and `TestShimStderrIsExactlyTheConfiguredText` now pins that the bytes the shim PRINTS are
unchanged — the assertion that separates quoting from stripping.
⚠ **Building it found a sibling the row did not name:** `block_flags` and `allow_flags` inject the
same way (`-q) touch /pwn;; -z` closes the `case` arm and opens its own), and they are the one
thing here that **cannot** be quoted — a case pattern is a glob, and quoting the shipped `-*[rR]*`
would silently unblock `grep -rn`. They are validated against a glob vocabulary instead, per
pattern rather than per entry, with a boot warning naming what was dropped. Verified in a nested
jail: payload text printed verbatim, no marker file, shipped glob intact, `grep -n` still passing
through.

**Program delivery [§10](../design/program-delivery.md#10-what-i-would-build-in-order)'s removal act SHIPPED 2026-09-04** and has left this section: it is
`yolo programs ls` / `remove` / `remove --apply` plus the `programs.autoprune` config key, default
off and user-scope only (`internal/entrypoint/orphanremove.go`, `internal/cli/programs.go`,
`internal/config/programs.go`). ⚠ **Its footgun was answered by a design choice, not a special
case:** the candidate set is *the bytes minus the declarations, never a record*, so an orphan whose
sentinel record is gone is the ORDINARY case rather than the dangerous one. Re-measured live at
**7 orphans, 448.6 MB** in this repo's jail.

**Five items shipped on 2026-09-03** and left under the archiving rule: the host-launch gate
(now [`host-apply-staleness.md`](../design/host-apply-staleness.md), IMPLEMENTED),
briefing-audiences steps 3–7, the `packload.Embedded()` temp-dir leak, the launcher-template
splices, and the orphan-message cause. Two of the three small ones corrected the row that queued
them, and one merge decided 💬 20 in code — see that row.

# 🔒 Waiting

**Two rows left this section on 2026-09-05, and neither was built to get out of it.**

- **The slirp4netns fallback** — DROPPED, not deferred. It was waiting on an old-passt host to
  exercise a fallback that is already built and already guarded (it fires only when podman itself
  reports a slirp4netns binary, never a PATH lookup). Verifying it costs hardware nobody has to
  hand; the ruling is that a complaint is a cheaper trigger than a hunt. If one arrives, the code
  is there and the row's analysis is in this file's history.
- **"The fatal witness is not on your host until a `just load`"** — RETIRED as stale, and
  measured rather than assumed: `YOLO_ALLOW_UNREACHABLE_SERVICES` is present in the
  `yolo-entrypoint` baked into the image this jail is running
  (`/opt/yolo-jail/bin/yolo-entrypoint`), so the loaded image already carries the fatal. The row
  described a gap that closed at some `just load` between 2026-08-18 and now.

- 🔒 **Program delivery [§10](../design/program-delivery.md#10-what-i-would-build-in-order) — the two steps that are blocked, not merely unscheduled.** 📄
  [`program-delivery.md` §10](../design/program-delivery.md#10-what-i-would-build-in-order). The unblocked step is in 📦. ⚠ **Order reversed 2026-09-04 ([OQ-CP1](../design/agent-cli-copies.md#-oq-cp1--is-the-disk-justification-retracted-and-is-oq-pd15-reversed--resolved-2026-09-04)): evergreen ships BEFORE capture, carrying A7's V-axis prune; the disk justification that put capture first is retracted.**
  ✅ **EVERGREEN SHIPPED 2026-09-04** ([`evergreen-agent-updates.md`](evergreen-agent-updates.md)),
  A7's V-axis prune with it. **One piece of it did not:** the MCP/LSP transitive refresh (that
  plan's build-order step 7). A yolo-installed MCP or LSP server still moves only when the
  bootstrap reinstalls it; the ruling behind the refresh is unchanged and the agent CLIs are
  evergreen without it.
  - **The user-scope gap receipt** and **obey** — ⚠ **REFRAMED 2026-09-04: these may have lost
    their subject, and that is [OQ-PD19](../design/program-delivery.md#-oq-pd19--do-steps-three-and-five-still-have-a-subject-after-the-agentproject-split).**
    [OQ-TP4](../design/trust-paths.md#decision-ledger) was RETIRED 2026-09-03, and [OQ-PD6](../design/program-delivery.md#decision-ledger) was scoped to *project* dependencies the same day —
    every gap-receipt writer in the tree is an AGENT dependency, which has no pin to obey. The one
    residue is pnpm (`pnpm@latest`, unpinned), and the obvious fix is a trap: pnpm is deliberately
    excluded from mise and nothing records why. **Rule [OQ-PD19](../design/program-delivery.md#-oq-pd19--do-steps-three-and-five-still-have-a-subject-after-the-agentproject-split) before building either.** The
    original analysis, still worth having if it is built: Three of the gap
    receipt's seven decisions *are* that OQ's options (a)/(b)/(c), and
    [`trust-paths.md`](../design/trust-paths.md) forbids either doc retiring the other's ID
    unilaterally — so building it answers [OQ-TP4](../design/trust-paths.md#decision-ledger) by implementation. **Obey reads an artifact the
    gap receipt cannot yet create**, so it cannot precede it. One line worth having when you rule:
    exactly one act changes behavior under obey — the cold-install branch. The poll is
    informational, the PINNED branch already compares offline, and `pack update` WRITES the record
    rather than reading it, so *"install obeys the record"* reads far broader than it is.
  - ~~**The installer capture** ([§6.3](../design/program-delivery.md#63-installers-that-just-do-whatever-capture-the-install-then-treat-the-capture-as-the-package), [`OQ-PD10`](../design/program-delivery.md#decision-ledger)) — not buildable here.~~ ⚠ **Wrong as of
    2026-09-04, and wrong about the premise rather than the schedule.** It IS buildable here:
    podman-in-podman gives this jail real containers, so `yolo capture` runs an installer in a real
    throwaway jail and `integration/capture_test.go` measures the result. **Slices one through six of
    seven are landed** ([`install-capture.md`](install-capture.md)) — the store, the inner driver,
    the `yolo capture <bin>` host act, materialize-from-the-launcher (the slice that pays),
    remove + GC, and macos-user's RECORDING half (its relocation rewrite is handed on; Seatbelt
    itself still needs real hardware). ✅ **Slice seven — the auto-capture trigger — SHIPPED
    2026-09-04** ([OQ-PD18](../design/program-delivery.md#decision-ledger), *"(d), DEFAULT ON"*), so
    ALL SEVEN are landed and the store is no longer empty by construction: a launch captures each
    selected pack's uncaptured `via: "installer"` program before starting the container, warns and
    continues on failure, and `YOLO_NO_AUTO_CAPTURE=1` opts out. Container backends only. The one property still
    unbuilt from [§6.3](../design/program-delivery.md#63-installers-that-just-do-whatever-capture-the-install-then-treat-the-capture-as-the-package)'s prose is *"a jail that writes outside its binds is a finding the capture run
    reports"*: stray writes are left alone and not enumerated, because enumerating them needs a
    whole-home walk (install-capture slice 2, correction (e)).

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
  **[OQ-P1](../design/macos-user-provisioning.md#open-questions) · [OQ-P2](../design/macos-user-provisioning.md#open-questions) · [OQ-P3](../design/macos-user-provisioning.md#open-questions) · [OQ-P4](../design/macos-user-provisioning.md#open-questions)**. A container jail gets tools two ways: an image floor of ~19
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
  handoff (whose [§2](handoff-guest-notch-macos.md#2-the-bug-that-was-fixed-blind--your-first-job-is-to-run-it) item 1.4 — do packs reach a macos-user sandbox? — is still the first thing to
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

- 🧊 **Cache relocation's three held questions — [`OQ-CR1`](cache-relocation.md#-oq-cr1--is-cache_relocations-the-right-level-held) · [`OQ-CR2`](cache-relocation.md#-oq-cr2--whether-the-relocation-should-also-be-reflected-host-side) · [`OQ-CR3`](cache-relocation.md#-oq-cr3--whether-cache_relocations-should-accept-a-per-workspace-override-for-read-only-sharing)** (named 2026-08-23; the
  row said "two" and there were three). Genuinely undecided whether we want the feature, not merely
  unscheduled. **CR1 gates item 11** (`yolo cache relocate`), and **CR2 is the same decision seen
  from the host side** — the doc says so itself, so answering CR1 alone answers half a question.
  📄 [`cache-relocation.md`](cache-relocation.md).

  **[OQ-5](../reference/image-staging-vs-baking.md#why-its-this-way)'s ruling costs this row one of its two motivating consumers, and nothing else.** Relocating
  `cache/images` to a spare disk was the second one, and it is what `yolo prune`'s own hint used to
  recommend — **no longer**: `4064f720` retired that advice on 2026-08-25, and the hint now printed
  (`internal/prune/prunecmd.go:281-284`) tells you to reclaim the backlog instead. Under *"I see no
  reason to keep any of this around"* the right verb for a **regenerable, write-once, read-once**
  artifact is **delete**, not **move**, and the shipped hint now says so. What is left is `huggingface` — **185 GiB of the 241 GiB that prompted the feature**
  (`cache-relocation.md:17-18`), cold and keep-forever, where relocation is the only lever. The
  abstraction-level question and the threat model are untouched by the ruling.
- 🧊 **Boundary broker B2** (approval-gated host credentials) — **no longer gated on anything; iced
  only because nobody has said they want it.** Both of its gates opened on 2026-09-02: nix [OQ-1](../design/noncontainer-nix-environment.md#9-open-questions)
  closed as *run* (the notch B2's approval tier is compelling in exists), and the experiment this
  row used to call "the cheapest thing in the whole file" **was run** — Claude Code sends the
  subscription bearer to any base URL, so a broker interposes by URL alone with no client change
  ([`agent-auth-modes.md` §8.1](../design/agent-auth-modes.md#81-measured-2026-09-02-the-subscription-bearer-follows-anthropic_base_url)). Moving it out of the icebox is
  💬 5's OQ-A ruling plus an appetite. 📄
  [`boundary-broker.md` §8](../design/boundary-broker.md#8-where-this-sits-against-the-rest-of-the-queue--my-priority-read).

- 🧊 **C4/C5 — the opt-in fast image path.** Shape ruled 2026-08-25 (image-staging [OQ-1](../reference/image-staging-vs-baking.md#why-its-this-way): opt-in,
  baked path retained); the gating re-measurement is TAKEN
  ([`image-staging-vs-baking.md` "Cost model"](../reference/image-staging-vs-baking.md#cost-model)) and reports a flat
  curve on the workload it could measure — C3 discharged C4's disk case, C2 its frequency case, the
  52 s cold launch is one C4 does not shorten, and **the one workload C4 exists for is explicitly
  not measured there**. Genuinely awaiting your go/no-go; queueing it before that call would be
  queueing a question. *(Promoted from a preamble paragraph to a row on 2026-09-02 so it stops
  being invisible to the counts.)*

---

# Open threads

### Emptying `bundled_loopholes/` — **done 2026-08-19**, and what it left behind

The goal was **no inhabitants at sprint end** ([OQ-BP4](../design/broker-as-a-pack.md#decision-ledger)), and the channel is gone rather than emptied:
the directory, its `embed.go`, `internal/loopholes/embedfallback.go` and every reader of them are
deleted. All **five** loopholes are pack contributions now, `loopholes.ReservedLoopholeNames` and
`paths.BuiltinLoopholeNames` are deleted whole, and **core's config schema names no loophole at
all** — which was the point of the exercise rather than a side effect.

**[OQ-LP14](../design/loophole-packaging-overview.md#oq-lp14--the-subset-cannot-say-a-socket-in-this-sessions-runtime-dir--resolved-2026-08-17--the-rule-is-withdrawn) is settled too, and by the better of its two answers.** It became a hard dependency the
moment the goal grew from one loophole to the whole channel, and it closed on 2026-08-18 by
**withdrawing the bind-host path rule rather than adding vocabulary for a runtime-dir socket** — a
rule that admitted `~/.ssh` while refusing `${XDG_RUNTIME_DIR}/pulse/native` in every spelling. The
two audio loopholes then merged into `packs/audio` under the plain name.

One residue remains, tracked above rather than here: the **binary capability** [OQ-BP1](../design/broker-as-a-pack.md#decision-ledger) promised
alongside the move (💬 **14**).

📄 [`broker-as-a-pack.md` §13](../design/broker-as-a-pack.md#13-what-empty-the-channel-actually-required--measured-2026-08-19) is the measured account of what
"empty the channel" actually required; its Decision Ledger holds BP1–BP4.

### What the roadmap does not cover, and deliberately

Ideas that are not yet anybody's decision — the ones that would become 💬 rows if you wanted them —
live in [`further-roadmap-ideas.md`](further-roadmap-ideas.md), not here. That file is a **source of
candidate work, not a queue**: nothing in it is committed, and it says which of its own entries it
would drop.

**And some live questions are deliberately not rows**, because a row is a decision you are being
asked to make and these are not blocking anything:

- **[OQ-ACP1](agent-config-packs.md#-oq-acp1--what-happens-when-two-people-attach-to-the-same-jail-with-different-pack-sets) … [OQ-ACP4](agent-config-packs.md#-oq-acp4--whether-pruning-needs-usage-telemetry-to-be-anybodys-job)** in [`agent-config-packs.md`](agent-config-packs.md) — that proposal was
  largely overtaken by what shipped (the `packs` key, host fetch, the lockfile, the origin gate), so
  what survives is four genuine but unpressing questions: two people attaching to one jail with
  different pack sets, opencode's skills gap, the prism as a standalone tool, and whether pruning
  needs telemetry.
- **CFP-1 … CFP-3** (`composed-file-permissions.md`), **SS-6** (`jail-state-separation-design.md`)
  and `host-render-target.md`'s three [§9](../design/host-render-target.md#9-open-questions--the-discussion-part) questions — named 2026-08-23 so they are countable. All
  concern shipped mechanisms working as designed, not gaps.
- **nix [OQ-3](../design/noncontainer-nix-environment.md#9-open-questions) · 4 · 5 · 7 · 8 · 9** in
  [`noncontainer-nix-environment.md`](../design/noncontainer-nix-environment.md) — what remains of
  the retired 💬 4 after [OQ-1](../design/noncontainer-nix-environment.md#9-open-questions) closed (2026-09-02). None blocks anything: [OQ-3](../design/noncontainer-nix-environment.md#9-open-questions) is the `nix profile`
  installer product, [OQ-7](../design/noncontainer-nix-environment.md#9-open-questions) is agents-from-nix (leaning no), [OQ-8](../design/noncontainer-nix-environment.md#9-open-questions)'s reporting half and [OQ-9](../design/noncontainer-nix-environment.md#9-open-questions)'s
  Linux-diagnostics half are worth fixing under any answer and are work, not rulings.
- **auth [OQ-9](../design/agent-auth-modes.md#11-open-questions)** in [`agent-auth-modes.md`](../design/agent-auth-modes.md) — the AWS credential-pair
  gap, carried back in when the retired 💬 3's doc rewrite dropped it unanswered. Working today via
  `env_sources`; likely absorbed by the in-flight env-derive work rather than decided.
- **threat-model Q2 · Q3** in
  [`macos-user-build-step-threat-model.md`](../design/macos-user-build-step-threat-model.md) — still
  open after Q1's mooting, both scoped to Vector A now, neither blocking (they stay summarized under
  💬 7 because that row is where the Mac work already lives).
- The **research** docs' questions — **[OQ-LM1](../research/local-model-endpoints.md#oq-lm1) … [OQ-LM6](../research/local-model-endpoints.md#oq-lm6)** in `local-model-endpoints.md` — are
  exploratory rather than blocking. *(`mise-host-jail-path-mismatch.md` is now CLOSED: its last open
  question had already shipped as `venvShadowMountArgs`, and re-reading it is what surfaced a trap
  documented nowhere else — a per-side path that is a symlink or a regular file cannot be shadowed,
  so the launcher warns and the jail silently sees the host's copy.)*

Plus **[OQ-GN1](handoff-guest-notch-macos.md#9-open-questions) · [OQ-GN2](handoff-guest-notch-macos.md#9-open-questions) · [OQ-GN4](handoff-guest-notch-macos.md#9-open-questions)** in the guest-notch handoff, which are Mac-gated rather than
undecided, and are cited from 💬 7 above. *([OQ-GN3](handoff-guest-notch-macos.md#9-open-questions) left on 2026-09-02 — it asked whether the
Cachix cache had ever been pushed to, and the Actions log answered it: yes, and CI reads from it
too. Chasing it found the six CI `nix build` calls that were discarding the flake's substituter.)*

They are named and countable now, which is the point — a question with an ID can be promoted to a
row the day it starts blocking something, and demoted the day it stops. **That is the whole
difference between this list being 14 rows and being 104.**
