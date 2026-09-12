# Roadmap

**Status: 17 needing you · 0 ready · 0 in progress · 7 waiting · 0 broken · 3 icebox.**

Last updated **2026-09-12**. Counts are tallied from this file's contents, not asserted — one per
`### 💬` heading, one per top-level bullet elsewhere, and each bullet's glyph matches its section.

> [!NOTE]
> **Reconciled 2026-09-12 against a 138-commit sprint, and the tally moved on two axes.** It was
> **17 · 2 · 0 · 6 · 0 · 3**. *Ready* lost both its rows — ✅ **29** (report tiers) and ✅ **30**
> (the Lua transform) — and briefly gained one: [OQ-CO7](../design/config-ownership-and-promotion.md#13-decision-ledger)'s
> adoption archive, the single ruling of the five shipped designs' thirty-one with no code behind
> it, **filed and built the same day**, so *Ready* ends at zero. *Waiting* gained ONE row — the six
> never-run Mac runbook items the macos-user pair
> added, which had been recorded inside an ✅ bullet where no count could see them, the same failure
> the C4/C5 row was promoted out of a preamble to fix. ⚠ **The status line above still read
> `1 ready` until it was recomputed on 2026-09-12** — the 📦 row became an ✅ record in the same
> commit that built it, and the tally was not re-run. That is the drift the counting rule at the
> top of this file exists to make cheap to catch, caught by running it.
> *Needs you* did not move, and that is the
> honest result rather than an oversight: **nothing that shipped closed a question**. 💬 **25**
> shipped and kept one question the BUILD opened; 💬 **31** never shipped at all and still holds
> thirteen. The previous note recorded a one-day drift in *waiting* caused by `36bee3e8`; it is
> superseded and lives in this file's history.

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
> numbering; **Thread A** → [`retired-decisions.md`](../plans/retired-decisions.md); **Thread C** →
> [`shipped-2026-08-12.md`](shipped-2026-08-12.md); **N3** → [`OQ-NX1`](../design/provisioner-sets.md#decision-ledger) (re-prefixed from the retired nix doc's bare numbering by the 2026-09-11 merge) in
> [`noncontainer-nix-environment.md`](../design/provisioner-sets.md); **S5** →
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
[`pack-execution-trust.md`](../reference/pack-system.md#why-there-is-no-approval-gate)

> [!NOTE]
> **This row aggregates three docs while naming one** (reconciled 2026-09-03, recounted 2026-09-04).
> `trust-paths.md` closed every question it was carrying: TP3/TP4 retired 2026-09-03 under the evergreen
> ruling, **TP8 and TP9 ruled 2026-09-04**, and **TP7 RETIRED the same day because TP9 deleted its
> subject**. ⚠ **[OQ-TP10](../design/trust-paths.md#-oq-tp10--a-wrapped-plugins-hooks-reach-the-agents-lifecycle-and-appear-in-no-launch-banner)
> was OPENED 2026-09-04 by the TP9 build** — a wrapped plugin's hooks reach the agent's lifecycle
> and appear in no launch banner, which falsifies a sentence TP9's own answer wrote. It needs you. ✅ **[OQ-LP8](../reference/loophole-system.md#oq-lp8) closed 2026-09-04** — its two overdue documentation requirements are
> delivered ([the loophole system reference](../reference/loophole-system.md#oq-lp8)):
> *following a mutable ref IS the trust decision*, and **tag pins are the documented shape** for a
> pack carrying code, both written into
> [the packs guide](../guides/migrating-to-packs-and-host-management.md)'s *Sharing a pack with other
> people*. **G2b is MOOT** — TP9 deleted the approval it would have anchored.
> ⚠ **Corrected 2026-09-09:** this row used to say its remainder was routing to **[`OQ-X1`](../reference/pack-system.md#why-there-is-no-approval-gate)**, which was
> itself RETIRED 2026-09-04 (subsumed by TP9, which deleted the gate it asked about). **The one live
> item here is [OQ-TP10](../design/trust-paths.md#-oq-tp10--a-wrapped-plugins-hooks-reach-the-agents-lifecycle-and-appear-in-no-launch-banner)**, and it lives in
> [`trust-paths.md`](../design/trust-paths.md). Read it there; this row is the routing table.

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
  lockfile's commit pin closes over the whole tree, prose included. *Inherits [OQ-LP8](../reference/loophole-system.md#oq-lp8)/G2b — the pin is
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
  at that notch. The disclosure stays the commit pin (**[OQ-LP8](../reference/loophole-system.md#oq-lp8)**), not a claim line.
- ⛔ **[OQ-X1](../reference/pack-system.md#why-there-is-no-approval-gate) — RETIRED 2026-09-04**, subsumed by TP9, which deleted the gate it asked
  about. It asked whether a digest-pinned installer script counts, given its own fetches are not
  pinned. The finding survives as documentation in
  [`pack-execution-trust.md` §5](../reference/pack-system.md#why-there-is-no-approval-gate) — a
  pinned script is not a pinned binary — and the scenario stays unexpressible either way:
  `packdecl` has no digest field until [OQ-BP5](../design/broker-as-a-pack.md#open-questions) lands one.
- ✅ **[OQ-TP9](../design/trust-paths.md#-oq-tp9--is-the-fetched-pack-approval-prompt-a-gate-or-theatre--resolved-2026-09-04) — RULED 2026-09-04: the fetched-pack approval prompt is THEATRE, deleted.** Selecting a
  pack means writing user-scope config as the host user — `packs` is inexpressible at workspace scope
  *by construction*, so an agent cannot add one — and a gate that refuses an actor who already passed
  a stronger one is what `gate-placement-principle.md` **Test 1** exists to delete. `userlayer.go`
  had already applied that test the same way to the sibling route. **Keep** `packs` user-scope-only
  (that half passes Test 1) and the startup disclosure banner. ⚠ **Corrected the same day:** the
  follow-on is [OQ-LP8](../reference/loophole-system.md#oq-lp8)'s two undelivered DOC requirements, not pin *enforcement* — a launch resolves
  from the local mirror, which only moves at `pack install`, so content is already frozen between
  installs; what deleting the gate does is make the lockfile **write-only at launch**. G2b is moot.
  **Both DOC requirements delivered 2026-09-04** (packs guide, *Sharing a pack with other people*),
  which closes [OQ-LP8](../reference/loophole-system.md#oq-lp8).
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

**Both of this project's upstream blockers dissolved on 2026-09-02** — nix [`OQ-NX1`](../design/provisioner-sets.md#decision-ledger) answered (host =
run) and auth [OQ-1](../design/agent-auth-modes.md#12-decision-ledger) measured — so nothing gates these four questions but themselves. The 2026-09-02
audit also gave OQ-A and OQ-C the shipped-precedent facts their leanings were missing: every verb
yolo ships is synchronous, and the oauth broker already returns per-verb response shapes (with the
trust-regime caveat recorded in the doc).

### 💬 7 — macOS, and the environment-manager stories

📄 [`macos-user-build-step-threat-model.md`](../design/macos-user-build-step-threat-model.md) ·
[`environment-manager-user-stories.md`](../design/environment-manager-user-stories.md) ·
[`macos-revival-and-distribution-plan.md`](macos-revival-and-distribution-plan.md)

**All three defects these stories are built on are now FIXED** (G1 on 2026-08-01, G3 on
2026-08-12, **Gap 1 on 2026-09-12**), and the doc carries a dated verdict at every gap — read those
before hunting for a bug. Gap 1 was the one this row kept alive: `config ls`'s `host` column came
from a hand-maintained two-entry map (`surfaceHasHostLayer`) rather than from the render, so any
*pack* surface that really did read machine state showed **no host layer**. The map is retired and
every column is derived — `builtinLayers` asks `manifest.Surface.HasHostLayer`, which is now the
surface's own `ReadsHost` declaration and the SAME predicate the boot render's host-layer read
consults ([`OQ-CO10`](../design/config-ownership-and-promotion.md#13-decision-ledger), 💬 25's step 6).

**user-stories Q1** is called "the biggest question in the document" by its own author — **and its
premise needs narrowing**: capture does *not* outrank every declared layer — it loses to `computed`
and `managed` (`internal/agentcfg/compose.go`'s ascending fold). ⚠ **That list used to name
`transform` as a third, and there is no transform layer any more** — see ✅ 30. Q1 is still the
biggest question, on the narrower and better ground that capture is **undeclared**, not that it wins
everything. ✅ **Its leaning is no longer half-built**: it wants capture to become a *staging area*
that `yolo config promote` drains, and **that verb shipped 2026-09-12** (`4223ca95`,
[§5](../design/config-ownership-and-promotion.md#5-promotion--the-way-out-of-capture)) — `promote` is
in `yolo config`'s dispatch and its usage text. So the remedy for an outstanding capture that
`apply --sealed` refuses on is no longer "discard it", and **answering Q1 in the leaning's direction
is now a ruling rather than a build**. What Q1 still decides is whether capture is *defined* as a
staging area — the verb existing does not make it one.

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
  asymmetry. **Four instances, not three** (2026-08-23): `composed-file-permissions.md` [§7.4](../reference/composed-file-permissions.md#what-this-means-for-host_files-modes) is the
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

### 💬 10 — `yolo check` tells you about the wrong machine — plus the two questions the same doc has been carrying unrouted

📄 [`broker-ca-and-nested-hosts.md`](../design/broker-ca-and-nested-hosts.md) — **[OQ-1](../design/broker-ca-and-nested-hosts.md#7-open-questions) · [OQ-2](../design/broker-ca-and-nested-hosts.md#7-open-questions) · [OQ-3](../design/broker-ca-and-nested-hosts.md#7-open-questions)**

⚠ **This row carried only [OQ-3](../design/broker-ca-and-nested-hosts.md#7-open-questions) until 2026-09-09.** Its doc has three live questions and the other two
had no home on this file. Both are about the same bake that closed their urgency and reopened their
substance, and both are cheap to rule:

- **[OQ-1](../design/broker-ca-and-nested-hosts.md#7-open-questions) — do we retire the `openssl` dependency, or just satisfy it?** The bake landed
  2026-08-18 (`imagePkgs.openssl` is in `flake.nix`, with a comment naming that doc), so the question
  is now purely about the **port**: `svcendpoint` mints certs with `crypto/x509`, while
  `oauthbroker/cert.go` still shells out to `openssl` and writes `ca.key`/`server.key` to disk — what
  issue #33 was about. *Leaning: bake now, port later, and write the port down as owed* — "deferred"
  has already survived one incident, and deferral with no record is how that happened.
- **[OQ-2](../design/broker-ca-and-nested-hosts.md#7-open-questions) — should a nested jail run its own broker singleton at all?** With `openssl`
  baked it already does, so an unanswered question here means **a never-executed path runs
  unattended** rather than staying dormant. *Leaning: let it run* — "a jail is a host for its
  children" is the model everywhere else — *but exercise it on purpose before relying on it.*

**The vocabulary question, [OQ-3](../design/broker-ca-and-nested-hosts.md#7-open-questions):**

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
  permit the imperative one — the shape [OQ-LP14](../reference/loophole-system.md#oq-lp14) already suffers from. *(The premise is now verified
  with a file:line: a fetched `host_daemon.cmd` really is approvable today,
  `loopholesource.go:258-310`.)*

⚠ **The cost [OQ-BP1](../design/broker-as-a-pack.md#decision-ledger) put on the critical path is still unpaid**: the release process has to produce
the matrix a manifest's `platforms` declares. Declaring more than you build turns *"unsupported
here"* into *"supported, missing"* (`broker-as-a-pack.md` [§9](../design/broker-as-a-pack.md#9-risks)).

---

### 💬 15 — Backend parity: the census, and whether macos-user gets briefings at all

📄 [`backend-parity.md`](../design/backend-parity.md) — **OQ-BP-1 · ~~OQ-BP-2~~ (answered by code) · OQ-BP-3 · OQ-BP-4**

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
- ✅ **OQ-BP-2 — ANSWERED BY CODE 2026-09-03; noticed here 2026-09-09.** It asked whether briefings
  and skills get DELIVERED to macos-user, and described an agent starting there with no AGENTS.md,
  no CLAUDE.md and no skills. **That is no longer true, and the leaning was taken:** content
  delivery landed on its own terms — `buildMacosHomeOverlay` composes them host-side
  (`internal/cli/run/macoshomeoverlay.go`), `runplan.go` stages them as `YOLO_DARWIN_HOME_OVERLAY`,
  `InstallHomeOverlay` copies them over the sandbox home (`internal/entrypoint/darwin.go`), and the
  launch says so: *"briefings and skills are delivered by COPY on macos-user"*. Nothing to rule.
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

### ✅ 16 — Minimal disk footprint: closed 2026-09-09, three of four ruled and built

📄 [`minimal-disk-footprint.md` §11.1](../design/minimal-disk-footprint.md#111-decision-ledger) —
**~~[OQ-DF1](../design/minimal-disk-footprint.md#111-decision-ledger)~~ · ~~[OQ-DF2](../design/minimal-disk-footprint.md#111-decision-ledger)~~ · ~~[OQ-DF3](../design/minimal-disk-footprint.md#111-decision-ledger)~~** ·
[OQ-DF4](../design/minimal-disk-footprint.md#112-open-questions) is 🔒 below

**Answer (assembled from four rulings, none of them made by this row):**
> - **[OQ-DF1](../design/minimal-disk-footprint.md#111-decision-ledger) — *"stream, keep zero tars"*** (2026-08-25). Built as C3: on podman the load
>   path writes no tar at all. Apple Container still writes one, deliberately — its converters
>   interpolate a path.
> - **[OQ-DF2](../design/minimal-disk-footprint.md#111-decision-ledger) — ANSWERED 2026-09-08 by composition rather than by a choice.** Which
>   component deletes stopped being a question once the reclaimers were built: the launch path runs
>   the same veto-protected sweep `yolo prune --apply` runs, debounced.
> - **[OQ-DF3](../design/minimal-disk-footprint.md#111-decision-ledger) — all three halves ruled and shipped.** SAFETY retired in advance by C2 +
>   `4064f720` (fail-safe veto); NUMBER ruled 2026-09-06 (`--keep-images` stayed 2, as an undo
>   margin rather than the safety mechanism — ⚠ **superseded in mechanism 2026-09-09: the flag is
>   deleted, see the shipped item above**); TRIGGER shipped with it (`AutoReapOldImages`); REACH
>   shipped 2026-09-08 — `mkOciImage` bakes `org.yolo-jail.owner` and the candidate list is a UNION
>   of the repository-name probe and a `--filter label=` probe, two queries because podman refuses
>   both in one (MEASURED). An image that lost its tag is still provably yolo's.
> - ⚠ **The standing warning survives the closure:** do not start at delete-on-success. That was
>   [OQ-DF2](../design/minimal-disk-footprint.md#111-decision-ledger) option (i) and it is not what shipped.
>
> **[OQ-DF3](../design/minimal-disk-footprint.md#111-decision-ledger)'s NUMBER ruling has since been superseded in shape, not in value**, by
> [OQ-LS3](../design/the-load-sentinel-is-not-a-liveness-oracle.md#111-decision-ledger): the global
> `keep=2` window was the wrong *mechanism*, and its replacement **shipped 2026-09-09** — there is
> no `--keep-images` to touch any more. Read the ✅ item above.

### 💬 17 — Mistyped names return `[PASS]`: mostly closed by the provider arc; the buried channel remains

📄 [`reference-mismatch-diagnostics.md`](../design/reference-mismatch-diagnostics.md) —
**[OQ-RM1](../design/reference-mismatch-diagnostics.md#9-open-questions) (narrowed) · [OQ-RM2](../design/reference-mismatch-diagnostics.md#9-open-questions) · [OQ-RM3](../design/reference-mismatch-diagnostics.md#9-open-questions) · [OQ-RM4](../design/reference-mismatch-diagnostics.md#9-open-questions)** · executes the amended
[`stringly-typed-references-principle.md`](../reference/stringly-typed-references-principle.md)

**You ruled the premise on 2026-08-30, and the provider arc built most of it within 72 hours** —
[§7](../design/reference-mismatch-diagnostics.md#7-sequencing-by-user-visible-payoff) steps 2, 3 and 6 shipped (selection-key validation, the `wire_api` enum, the `base_url`
credential refusal, and the credential preflight — the last with a deliberate selected-pack
rescoping the doc now records). **The 2026-08-30 reproduction this row used to carry is dead**: the
same config now yields three `[FAIL]`s, and the docs' census flips three rows to Reached
(re-verified 2026-09-02; both docs updated).

**What is left is exactly two things:**

- **The buried-warning channel ([§7](../design/reference-mismatch-diagnostics.md#7-sequencing-by-user-visible-payoff) step 1) — HALF SHIPPED 2026-09-02, and the
  remaining half is smaller than this row said.** ⚠ **Corrected 2026-09-09** (it claimed the whole
  step unshipped and "queued 📦 below" while 📦 was empty): the **config-resolution** half landed in
  `d6d8edc2`. `warningLine` is deleted, every config loader in the package hands findings to one
  graded sink, and `internal/cli/check/warningchannel_test.go` fails if anyone re-declares the
  second channel. What is left is the **loophole** half, in two separable pieces: grading
  `internal/loopholes`' package-level `warnf` needs no ruling, but making `check` *see* the
  supersession did-you-mean at all is step 4 below — the loopholes section calls `ValidateSet`,
  which deliberately bypasses `Discover`, so the diagnostic never reaches an emission site there.
  Grading alone does not fix that.
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

### ✅ 22 — The load sentinel is used as a liveness oracle, and it killed four jails

📄 [`the-load-sentinel-is-not-a-liveness-oracle.md`](../design/the-load-sentinel-is-not-a-liveness-oracle.md) ·
opened 2026-09-08 by the incident · **all three ruled 2026-09-08/09**

A ten-entry list of recently-LOADED nix store paths was the protection two reapers consulted. Only a
LAUNCH appends to it, so a jail that is running but not relaunching ages out of the window while
still in use — and on 2026-09-08 the automatic image reap `rmi -f`'d the images of four jails that
had been up 3–4 days, taking the containers with them. The image half was fixed the same day
(`feddc5e0`: ask `podman ps`, and never force-remove).

**Answer (2026-09-08, [OQ-LS3](../design/the-load-sentinel-is-not-a-liveness-oracle.md#111-decision-ledger) sharpened 2026-09-09):**
> - ✅ **[OQ-LS1](../design/the-load-sentinel-is-not-a-liveness-oracle.md#111-decision-ledger) — NO liveness veto for the GC-root reaper; an age cutoff at ONE WEEK**, size cap
>   only after age. Ruled AGAINST the leaning: liveness is a wrong predictor in both directions.
>   `PruneOrphanImageRoots` lost its `protected` set and its `liveKnown` gate — **built** `93f21f07`.
> - ✅ **[OQ-LS2](../design/the-load-sentinel-is-not-a-liveness-oracle.md#111-decision-ledger) — a decline says so: an ERROR where the user asked, and nothing where they did
>   not.** `yolo prune` exits non-zero naming the missing evidence; a debounced automatic pass stays
>   silent. The candidate listing moved BEFORE the guards so "declined" means *prevented work* rather
>   than *fresh machine* — **built** `3c9e8de9`.
> - ✅ **[OQ-LS3](../design/the-load-sentinel-is-not-a-liveness-oracle.md#111-decision-ledger) — `keep` is the wrong MECHANISM, not the wrong number, and there is NO undo.**
>   *"I don't know that I've ever rolled back, only evolved forward."* **Ruled, unbuilt — it is the
>   ✅ item below — SHIPPED 2026-09-09**, and it is what replaced `--keep-images 2`.
>
> The contradiction the doc recorded is gone with LS1: `imageroots.go` no longer justifies a
> destructive guard with a liveness claim, because it makes no liveness claim.

### ✅ 23 — Layer-aware image delivery: 3.4 GB re-shipped to deliver 27 MB

📄 [`layer-aware-image-delivery.md`](../design/layer-aware-image-delivery.md) · opened 2026-09-08
off the first real `--timing` measurements · **all six ruled 2026-09-08/09**

`image.stream_load` is **81.0s of a 96.1s image load** on the maintainer's host (`image.nix_build`
is 14.9s — the build is not the problem). Measured cause: the image is 99 layers / 3.47 GB, the
customisation layer is 27.2 MB — **0.78%** — and a docker-archive tar is sequential, so podman
re-ingests every unchanged base layer on every store-path change.

**Answer (2026-09-08, authorized 2026-09-09):**
> - ✅ **[OQ-LI1](../design/layer-aware-image-delivery.md#91-decision-ledger) — take the flake input; BUILD the copier when it is needed.** The premise
>   was wrong: `cache.nixos.org` is nix's built-in default and this flake already ships
>   `extra-substituters` for its own cachix, so no third-party cache is being added. **The project
>   cachix may never be load-bearing** — a miss costs time only, and no functional fallback is wired
>   to one.
> - ✅ **[OQ-LI2](../design/layer-aware-image-delivery.md#91-decision-ledger) — Apple Container ships in the SAME pass**, against the leaning. Its
>   measurement becomes a precondition of the default flip rather than a reason to defer.
> - ✅ **[OQ-LI3](../design/layer-aware-image-delivery.md#91-decision-ledger) — keep the extras tier; three tiers.** C4/C5 store delivery is opt-in and
>   podman-on-Linux only, so the tier is not transition scaffolding.
> - ✅ **[OQ-LI4](../design/layer-aware-image-delivery.md#91-decision-ledger) — order prune's keep-window by the sentinel's recency; refuse a per-build
>   timestamp.** Subsumed by [OQ-LS3](../design/the-load-sentinel-is-not-a-liveness-oracle.md#111-decision-ledger).
> - ✅ **[OQ-LI5](../design/layer-aware-image-delivery.md#91-decision-ledger) — DISSOLVED: no rollback window, because there is no fallback.**
>   `streamLayeredImage` and `YOLO_LEGACY_IMAGE_STREAM` are deleted by the change that adds the new
>   path. *An escape hatch is for a config the USER broke, not for yolo's own mechanism being broken.*
> - ✅ **[OQ-LI6](../design/layer-aware-image-delivery.md#92-open-questions) — BUILD IT, and take the measurement FIRST.** Ruled 2026-09-09. The
>   authorization is the 📦 row below; **the first act is a number on your host, not a commit.**

### 💬 24 — Bedrock's native arm: one credential, three clients, seven unruled names

📄 [`bedrock-plumbing.md`](../design/bedrock-plumbing.md) — **[`OQ-BR1`](../design/bedrock-plumbing.md#13-open-questions) · [`OQ-BR2`](../design/bedrock-plumbing.md#13-open-questions) ·
[`OQ-BR3`](../design/bedrock-plumbing.md#13-open-questions) · [`OQ-BR4`](../design/bedrock-plumbing.md#13-open-questions) · [`OQ-BR5`](../design/bedrock-plumbing.md#13-open-questions) · [`OQ-BR6`](../design/bedrock-plumbing.md#13-open-questions) · [`OQ-BR7`](../design/bedrock-plumbing.md#13-open-questions)** · opened 2026-09-04, **routed here 2026-09-09** by the design-doc audit

**Nothing is built** (verified 2026-09-09: no `bedrock-gpt*` profile in `packs/`, no
`AWS_BEARER_TOKEN_BEDROCK` in the tree — `packs/claude`'s `bedrock` overlay is the only Bedrock
thing that ships). The work is not an endpoint: codex, opencode and pi each already ship a native
`amazon-bedrock` provider on the same credential, so what yolo owes is a region, a key and a model
id — and the three clients default to *different* endpoint families with different model-id
spellings, which is why the design ships the family twice (`-p bedrock-gpt`, `-p bedrock-gpt-mantle`).

⚠ **One adjacent question from another doc belongs with [`OQ-BR4`](../design/bedrock-plumbing.md#13-open-questions), and it is the only one here that
is live in shipped code** (routed 2026-09-09): [`OQ-2`](../design/cerebras-pack-and-copilot-delivery.md#oq-2) in
[`cerebras-pack-and-copilot-delivery.md`](../design/cerebras-pack-and-copilot-delivery.md) — `packs/claude/derive.lua` emits
`ANTHROPIC_AUTH_TOKEN` from `api_key` alone, never consulting the `routed` flag it sets only when
`endpoints.anthropic.base_url` exists. **No shipped pack reaches it** (`packs/zai` and
`packs/cerebras` both declare `endpoints.anthropic`; `packs/claude`'s own `bedrock` provider
declares no `api_key_env_name`), so the trigger is a **user-declared** provider whose
single-protocol `base_url` shorthand names no protocol. It carried no `💬` until 2026-09-09, so the
corpus count could not see it. Rule it with BR4 — both are the same D2 leak decision on the same
derive machinery.

**What blocks it is naming, not mechanism.** [`OQ-BR1`](../design/bedrock-plumbing.md#13-open-questions) (profile/provider names), [`OQ-BR2`](../design/bedrock-plumbing.md#13-open-questions) (how a derive
recognises an endpoint-less provider) and [`OQ-BR7`](../design/bedrock-plumbing.md#13-open-questions) (`endpoint_family` as its own field or a fallout)
shape the schema; [`OQ-BR4`](../design/bedrock-plumbing.md#13-open-questions) is a live D2 leak decision; [`OQ-BR3`](../design/bedrock-plumbing.md#13-open-questions)/[`OQ-BR5`](../design/bedrock-plumbing.md#13-open-questions)/[`OQ-BR6`](../design/bedrock-plumbing.md#13-open-questions) are per-agent. **Ruling
[`OQ-BR1`](../design/bedrock-plumbing.md#13-open-questions)/[`OQ-BR2`](../design/bedrock-plumbing.md#13-open-questions)/[`OQ-BR7`](../design/bedrock-plumbing.md#13-open-questions) unblocks the build**; the other four can wait for it.

**Answer:**
> _(empty — fill in when decided)_

### 💬 25 — Who owns the config file: SHIPPED, with one question the build opened

📄 [`config-ownership-and-promotion.md`](../design/config-ownership-and-promotion.md) — **the one live question is
[`OQ-CO12`](../design/config-ownership-and-promotion.md#12-open-questions)**, and the BUILD opened it · **all eleven the DESIGN opened are
settled and compacted into the [Decision Ledger](../design/config-ownership-and-promotion.md#13-decision-ledger)** (CO1–CO8 on 2026-09-10/11,
CO9–CO11 on 2026-09-11) · written 2026-09-09, **routed here the same day**

yolo inferred config-file ownership from the confinement notch rather than asking, and the
inference was wrong for anyone who adopted `yolo host apply`. This row took over the *promote* half
of 💬 7's user-stories Q1, which reported the same missing subcommand from the capture side — Q1
asks whether capture should become a staging area, this doc designed the drain, and **the drain is
now built**, which narrows Q1 to the definition rather than the verb.

✅ **BUILT 2026-09-12 — all eight steps of
[§10](../design/config-ownership-and-promotion.md#10-what-i-would-build-in-order)'s order, with one
carve-out.** The key and its fail-closed read (`6012ff9e`); `none`/`assert` wired (`24c5eb90`);
`--revert` (`0c935f2b`); `promote --plan` and its write path (`4223ca95`); both halves of the
`reads-host` restructure (`cc674ac1`); and `own` — the host notch composing `stateful`, with the
capture store and host-side `reset` in the same commit (`c2c4873e`). `assert` remains the undeclared
default, so nothing moved for a user who set nothing.

✅ **THE ONE-TIME ADOPTION ARCHIVE DID NOT SHIP WITH `own`; it shipped a day later, 2026-09-12.**
Step 8 listed it as landing in `own`'s commit and
[§9](../design/config-ownership-and-promotion.md#9-risks)'s risk table named it as the mitigation for
two data-loss rows, while no adoption path wrote a `config` bucket — which made
[`OQ-CO7`](../design/config-ownership-and-promotion.md#13-decision-ledger) the single ruling of the
five shipped designs' thirty-one with no code behind it. `86c4ad0e` recorded that at both adoption
sites in code, comment-only; the build replaced those comments with one call in the shared stateful
writer. It matters most where the guard is weakest: `confirmHostLosses` reads `EntryLosses` and fires
only on first apply, so the `assert` → `own` switch — the exact transition that drops a deep-merged
leaf — is unprompted, and the jail's own first-migration half has no TTY for a prompt at all.
⚠ **Four gaps the net does not reach were measured after it shipped, and are open residue** —
[§6.3.3](../design/config-ownership-and-promotion.md#633-what-survives-as-a-guard) lists them and
the ✅ record under [*Up next*](#-up-next) summarises them. They are not new questions for you: each
is unfixed because its fix would reverse a ruling, which is a call to make when one of them costs
somebody something.

**[§10](../design/config-ownership-and-promotion.md#10-what-i-would-build-in-order) was six steps when it was written, and the build needed
eight.** Steps 6 and 7 — the
`reads-host` restructure, declaration-onto-the-surface then read-fails-closed — were unplanned: no
version of [§10](../design/config-ownership-and-promotion.md#10-what-i-would-build-in-order) scheduled them, and step 5 asks the predicate they change. **Both shipped anyway**
(`cc674ac1`; `manifest.Surface.ReadsHost` is the field, `Surface.HasHostLayer()` returns it, and
`surfaceHasHostLayer` survives only in comments recording its retirement). They were added AFTER
step 5 rather than before it, because promote binds to `HasHostLayer()` and is invisible to what
populates it, while the restructure touches the boot render on every backend (`3dc58314`).

> [!NOTE]
> "Never scheduled" here means **absent from the plan, not absent from the tree** — two readers in a
> row took it the other way. Unplanned-and-shipped and ruled-and-unbuilt are different states; the
> second had one inhabitant ([`OQ-CO7`](../design/config-ownership-and-promotion.md#13-decision-ledger), above) and it is
> now built, so this row owes nothing.

✅ **Two defects the build fixed that nobody had filed** — distinct from the two the audit
surfaced below, and worth naming because neither was in scope and the first was live for every user:

- **`yolo config diff` misreported a fully redundant capture as a new in-jail edit, on every TOML
  surface** (`027bd9bd`). `readLastRenderKeys` decoded every `last_render` sidecar with `jsonx`, but
  the sidecar holds the exact bytes of the last render, so its format is the SURFACE's — a TOML
  sidecar failed to decode, the baseline came back empty, and empty reads as *"(added in-jail)"*.
  Measured on this repo's own jail: of the four surfaces carrying a non-empty capture overlay, both
  TOML ones (`codex/config`, `mise/config`) misreported. It is upstream of more than the display —
  `promote` drops the keys this comparison calls redundant, so a wrong answer here would have become
  a wrong promotion.
- **The mode census was half wired, and `own` could have shipped leaving it a false statement**
  (`67b649ac`). `Records()` had one production caller; `Runs()`, `Excludes()` and `Undecided()` had
  none, and the host's *"every surface is `rmw`"* was an unconditional call rather than the census.
  The two AGREED, which is the hazard — `HostModes` could have been edited to say anything with
  every host render still doing `rmw`. `ModeSet.Mechanism` now answers the question a render entry
  actually asks, and the census is pinned per row rather than per dispatch (`3ca670fc`).

💬 **What still needs you is one question the BUILD opened, not one the design left:**
[`OQ-CO12`](../design/config-ownership-and-promotion.md#12-open-questions) — is the `assert` → `own` switch required to be
byte-invariant, or only key-invariant? [§11](../design/config-ownership-and-promotion.md#11-success-criteria)
says zero bytes; measured, the switch is zero bytes for the leaf case the criterion was written
for and changes bytes on four other axes, two of them silent key deletion. The section carries
the measurements and the leaning.

⚠ **Review round 0 landed 2026-09-10 and this row is smaller than it was.** Two of the seven are
ruled and one is new, so the gating sentence this row used to carry is retired:

- ✅ **[`OQ-CO2`](../design/config-ownership-and-promotion.md#13-decision-ledger) — RULED: neither prompt nor warn.** The undeclared state is
  `assert`, silently; each value explains itself at the point of the act and `apply --sealed` is the
  one place undeclaredness bites. **Against the leaning**, and it deleted the migration prompt from
  the build order's step 1.
- ✅ **[`OQ-CO3`](../design/config-ownership-and-promotion.md#13-decision-ledger) — RULED: yes, host capture under `own` only** — and it is a
  *precondition* of adoption rather than an added capability, so it lands in the same commit as `own`.
- 🆕 **[`OQ-CO9`](../design/config-ownership-and-promotion.md#13-decision-ledger) — opened by the same round**: a KEYLESS host surface (`raw`,
  `lines`) is never adopted by `ComposeStateful`, which is safe in a disposable jail home and not on
  a real one. Empty class today, which is why it is worth ruling before someone adds one.

~~**[`OQ-CO1`](../design/config-ownership-and-promotion.md#13-decision-ledger) is now the only gate**~~ — **spent: CO1 ruled three values, and CO2–CO8 followed.** The
remainder are promotion mechanics ([`OQ-CO4`](../design/config-ownership-and-promotion.md#13-decision-ledger)–[`OQ-CO7`](../design/config-ownership-and-promotion.md#13-decision-ledger):
default destination, in-jail refusal, precedence loss, archiving) plus CO9's carve-out.

**What the round changed about the WORK, which is the useful part:** both rulings removed a mechanism
the design had proposed, and in both cases the replacement was already shipped — the `assert` default,
and `ComposeStateful`'s first-migration adoption (`internal/agentcfg/staterender.go`, the
`B1 (⚠ DATA LOSS FIX)` branch). So the first owned render is byte-identical to the file on disk *by construction*, and the
adoption-diff confirmation the design wanted is not built at all.

⚠ **AUDITED 2026-09-11, and the headline is that three of these were already ruled — by you,
on 2026-08-01, in a ledger this doc never read.** [`environment-manager-plan.md`](environment-manager-plan.md)'s
Decision Ledger carries **[`OQ-1`](environment-manager-plan.md#resolved) — `--revert` on the host target? RESOLVED: NO**, **[`OQ-3`](environment-manager-plan.md#resolved) — retire
the `reads-host` read-in layer? RESOLVED: YES** (express personal settings as a local pack
instead), and **[`OQ-4`](environment-manager-plan.md#resolved)/[`OQ-5`](environment-manager-plan.md#resolved) — pure `rmw`; whole-file `stateful` + capture REJECTED, "capture buys
nothing"**. This design proposes `--revert` (build step 3), asks whether the host layer survives
([`OQ-CO11`](../design/config-ownership-and-promotion.md#13-decision-ledger)), and renders `own` as `stateful` with a host capture store. **Four
review rounds argued that ground fresh because the rulings live in a plan doc's ledger rather
than in the design's own.**

Two consequences, and the first needs you before anything else here moves:

- **[`OQ-CO3`](../design/config-ownership-and-promotion.md#13-decision-ledger), which you settled in review round 0, reverses [`OQ-4`](environment-manager-plan.md#resolved)/[`OQ-5`](environment-manager-plan.md#resolved).** Either
  the 2026-08-01 ruling stands and `own` needs rethinking, or it is superseded and that ledger
  needs a dated reversal row. The audit flagged rather than flipped it, correctly.
- **[`OQ-CO11`](../design/config-ownership-and-promotion.md#13-decision-ledger) may already be answered "retire it".** The irony worth noting:
  [`OQ-3`](environment-manager-plan.md#resolved)'s stated migration — *"express personal settings as a local pack"* — is exactly what this
  doc's `promote --to local` builds, so [§5](../design/config-ownership-and-promotion.md#5-promotion--the-way-out-of-capture) is the migration story for a ruling it did not know
  existed. **If [`OQ-3`](environment-manager-plan.md#resolved) stands, CO11 and [`OQ-CO10`](../design/config-ownership-and-promotion.md#13-decision-ledger) both dissolve and this doc gets
  materially smaller.**

**ALL ELEVEN QUESTIONS THE DESIGN OPENED ARE SETTLED** (2026-09-11) and the doc is
`status: accepted` — it moved to 📦 the moment [`OQ-CO9`](../design/config-ownership-and-promotion.md#13-decision-ledger), [`OQ-CO10`](../design/config-ownership-and-promotion.md#13-decision-ledger) and [`OQ-CO11`](../design/config-ownership-and-promotion.md#13-decision-ledger) were ruled. CO11 was
decided *by* CO10 rather than separately: ruling a mechanism's binding, failure direction and
coverage decides that it exists, so asking in the same breath whether to delete it was incoherent.

⚠ **That reverses four rulings in [`environment-manager-plan.md`](environment-manager-plan.md#blocks-phase-4-host-render)'s
2026-08-01 ledger** — [`OQ-1`](environment-manager-plan.md#blocks-phase-4-host-render) (no `--revert`), [`OQ-3`](environment-manager-plan.md#blocks-phase-4-host-render) (retire the read-in `host`
layer) and [`OQ-4`](environment-manager-plan.md#blocks-phase-4-host-render)/[`OQ-5`](environment-manager-plan.md#blocks-phase-4-host-render) (pure `rmw`, capture rejected). **The dated reversal rows are now in that ledger**,
because their absence is what let this design re-argue settled ground for four rounds. The lesson
is worth more than the rows: **before opening a question, search sibling ledgers** — a cross-document
collision is invisible by construction.

✅ **Two code defects the audit surfaced, neither of which needed a ruling — both closed by the
build.** Adoption dropped every object-valued key wholesale while `dropOverriddenKeys` — three
functions later — called that same blanket approach *"simpler and wrong"*, so `permissions.ask`-
shaped leaves were **silently lost at `assert→own`** and in any jail that lost its `last_render`;
adoption now narrows in two passes and the leaf survives, pinned as a byte golden across the
switch. And the `host` layer **silently dropped on `macos-user`** (no `/ctx`, pack grants neither
mounted nor filtered while `host_files` are, and the read was fail-open) — the parity defect stated
in [`macos-user-home-tiers.md`](../design/macos-user-home-tiers.md#50-the-constraint-that-outranks-the-layout-choice-one-mechanism-every-backend);
[§10](../design/config-ownership-and-promotion.md#10-what-i-would-build-in-order) step 7 made the read fail closed with the launcher reporting what it delivered, and
`macos-user` reports `unsupported` rather than being refused for what it cannot do.

**Answer:**
> _(empty — fill in when decided; the live question is now [`OQ-CO12`](../design/config-ownership-and-promotion.md#12-open-questions), above. The 2026-08-01 ledger in [`environment-manager-plan.md`](environment-manager-plan.md#resolved) carries its dated reversal rows.)_

### 💬 26 — The same model has a different name in every provider, and switching leaves the old one behind

📄 [`provider-switching.md`](../design/provider-switching.md) — **[`OQ-PS1`](../design/provider-switching.md#10-open-questions) · [`OQ-PS2`](../design/provider-switching.md#10-open-questions) ·
[`OQ-PS3`](../design/provider-switching.md#10-open-questions) · [`OQ-PS4`](../design/provider-switching.md#10-open-questions)** · split out of [`bedrock-plumbing.md`](../design/bedrock-plumbing.md) (💬 24), which raised
it and correctly refused to solve it · **routed here 2026-09-09**

A model id is provider-local, so every provider switch is a rename and yolo does half of it. **One
live defect, two conveniences.** The defect is the missing fourth row in the selection state
machine: `agentcfg/selection.go`'s `!selected` branch keeps the id yolo itself wrote — its comment
cites the recorded ruling by ID as the reason — so a deselected profile leaves the agent asking a new endpoint for the old provider's
model.

**[`OQ-PS2`](../design/provider-switching.md#10-open-questions) is the gate** — clearing on deselect *reverses a recorded ruling*, so it needs your word,
not mine, and it can be ruled alone. [`OQ-PS1`](../design/provider-switching.md#10-open-questions) (do claude's vendor-name reads move to capability
aliases) and [`OQ-PS3`](../design/provider-switching.md#10-open-questions) (does yolo ship model ids, or only the empty provider shape) are downstream;
PS3 is additionally blocked on verifying Bedrock's geographic prefix set. Nothing built.

**Answer:**
> _(empty — fill in when decided; [`OQ-PS2`](../design/provider-switching.md#10-open-questions) alone unblocks the only defect)_

### 💬 27 — Conditional env, and whether `guest` gets its own field census

📄 [`loophole-packaging.md`](../design/loophole-packaging.md) — **[`OQ-LP5`](../design/loophole-packaging.md#open-questions) · [`OQ-LP7`](../design/loophole-packaging.md#open-questions)** · **routed here
2026-09-09**; these two lived at line 2,443 of a 3,167-line doc and appeared nowhere in `docs/plans/`

Everything else in that design is built. Both are cheap to rule and expensive to discover later.

- **[`OQ-LP5`](../design/loophole-packaging.md#open-questions) — does `jail_env` stay refused for a pack-shipped loophole?** The refusal buys yolo out
  of a cross-kind collision pass; the shipped `packs/audio` pays for it by setting
  `PULSE_SERVER`/`PIPEWIRE_REMOTE` on every launch that selects the pack, socket or no socket
  (verified: `loopholedecl/packshipped.go` refuses it, `packs/audio` declares them through the `env`
  kind). *Leaning: keep the refusal, revisit at the first pack that cannot absorb it.*
- **[`OQ-LP7`](../design/loophole-packaging.md#open-questions) — does `guest` get its own field census, or keep borrowing `HostFields()`?**
  `Target.Fields()` funnels both into one set (`internal/render/fieldset.go`). It blocks nothing
  shipped; it decides the shape of the first macos-user loophole. *Leaning: split when env-manager
  Phase 7 lands and not before.* **Interaction: this is the same `guest` notch as 💬 7's Phase 7
  work — rule them in one sitting.**

**Answer:**
> _(empty — fill in when decided)_

### 💬 28 — Mirroring the host's paths into the jail: the narrow no, and the maximal no

📄 [`workspace-path-mirroring.md`](../design/workspace-path-mirroring.md) — **[`OQ-WP1`](../design/workspace-path-mirroring.md#open-questions) … [`OQ-WP12`](../design/workspace-path-mirroring.md#open-questions)** ·
written 2026-09-04, **routed here 2026-09-09**; it had zero inbound links from any doc

The recommendation is **no** for both the narrow question (mount the workspace at the host path) and
the maximal one (mirror user, home, workspace and toolchain store), and the doc exists to make that
*no* checkable: three confirmed absolute-path problems, seven candidates disproved, and a central
objection — you can mirror a name but not its content, so a cross-boundary reference resolves to an
ABI-incompatible artifact instead of failing loudly. **Nothing is built and nothing should be, but
twelve questions are live with no home**, two of which stand whether or not mirroring ever happens:

- ✅ **[`OQ-WP2`](../design/workspace-path-mirroring.md#open-questions) — SHIPPED 2026-09-09** (`8ff96e15`), and the measurement is settled after two
  wrong answers. `scripts/build-go.sh` now passes `-trimpath`, pinned against `flake.nix` by
  `internal/entrypoint/gobuildflags_test.go`. **Measured from this tree: 460 distinct
  `/workspace/{internal,cmd,vendor}/…` source paths without the flag, zero with it.**
  ⚠ **My earlier retraction of the doc's number was wrong and is withdrawn.** It measured the
  checked-in `dist-go/` binary, found no `/workspace/…` paths and called the finding conditional.
  The paths were there wearing a different root: Go's build cache reuses a compile action recorded
  under another directory, so a jail-built binary can carry **`/home/<user>/code/yolo-jail/…`** —
  the host's home layout — instead. Grepping one root and declaring the class absent is what went
  wrong, twice.
- **[`OQ-WP4`](../design/workspace-path-mirroring.md#open-questions)** — workspace-scope `mounts` are inert only by an undocumented fail-safe. That is a
  [`trust-paths.md`](../design/trust-paths.md) scope-model question (💬 2's family), not a mirroring one.
- **[`OQ-WP12`](../design/workspace-path-mirroring.md#open-questions)** is a maintainer tier call about macos-user's single shared home.

**Answer:**
> _(empty — fill in when decided; [`OQ-WP2`](../design/workspace-path-mirroring.md#open-questions) and [`OQ-WP4`](../design/workspace-path-mirroring.md#open-questions) can be ruled without touching the mirroring verdict)_

### ✅ 29 — One report, three readers: SHIPPED, and the report went 278 lines to 30

📄 [`report-tiers.md`](../design/report-tiers.md) — **all seven RULED**, the last on 2026-09-11 ·
**BUILT 2026-09-11, all eight steps of [§9](../design/report-tiers.md#9-what-i-would-build-in-order)**; measured 2026-09-12 ·
`status: accepted` · written 2026-09-10, **routed here the same day**

The maintainer's complaint — *"hard to read, not intuitive, and not so actionable"* — was measured at
**277 lines, about sixteen of them actionable**, for one observe-posture `yolo apply --at host` over
this development jail's home. Nineteen lines were verbatim kind-refusal paragraphs printed once per
*contribution* while the text is per *kind*; **210 lines stated one fact** (fourteen skills in five
agent dirs are yours and would move to the local pack) at three granularities; and the roll-up
counted destinations rather than files and carried no loss count.

**Answer (2026-09-11) — and the build consumed it the same day:**
> **Nothing here waits on a person.** All seven were ruled and the doc is `status: accepted`.

✅ **MEASURED AFTER THE BUILD, in this jail on 2026-09-12: the default report is 30 lines where it
was 278, and `--verbose` carries the 267-line detail view.** *(277 and 278 are the same report on two
days — the diagnosis above was taken at `48f47e56` on 2026-09-10, the before/after pair on 2026-09-12
against a freshly built binary. Neither number is the other rounded.)* Every step landed — the survey's tiers
and loss counts, the verdict line (`f8e265ea`), notch facts said once (`2a363580`), tier 3 grouped by
remedy (`7e05bfcc`), the dependency pre-flight with its prompt and fatal decline (`3914ec75`,
`f94b2c97`), the default/`--verbose` split with the rationale's move to the manual under the kind-doc
gate (`ae21e151`), the launch side (`7150319d`, `5dc1c26d`), and JSON for the dry run with its
`--assert` refusal (`866aa2e1`, `896f0cde`).

⚠ **Two of this row's own supporting findings are now false, and they are false BECAUSE of the
build** — worth keeping rather than deleting, because each was the argument that authorized the
thing that falsified it:

- *"`--verbose` has only timing consumers today"* — that is what made this the *"first non-timing
  diagnostic"* [`reference/perf-logging.md`](../reference/perf-logging.md) D14 said would decide the
  flag's meaning. `internal/cli/hostapplydetail.go` is that consumer, and D14 is spent. ⚠ It honors
  BOTH spellings (a typed `--verbose`/`-v` and an inherited `YOLO_VERBOSE`), which is deliberate and
  is the easy thing to copy wrong — `explicitVerbose()` is the narrower accessor and the wrong one
  here.
- *"there is no quiet mode anywhere"* — that is now a **ruling**, not an observation.
  [`OQ-RO3`](../design/report-tiers.md#11-decision-ledger) and P4 say a launch has no quiet mode;
  progress and provenance may be COMPRESSED to a line, but a **disclosure is never suppressible**,
  because the pack read/exec banners are the entire trust boundary today. `TestTheLaunchHasNoQuietFlag`
  fails if a flag appears on `runFlags`, everything the launcher prints is teed to
  `<workspace>/.yolo/launch.log`, and the rule is in `AGENTS.md`.

⚠ **The review moved the thesis before the build consumed it.** It was *"the report has no author;
give every line a tier."* It became **the command states its own result — the reader never computes
it**, carried by **P7** (the result) and **P8** (*facts, not rationale*), which is what collapsed the
repeated 40-word kind-refusal paragraphs into a manual entry.

✅ **A missing host dependency became a blocker, and that authorized env-manager Phases 6.4 + 4.3
rather than designing them.** [`OQ-RO7`](../design/report-tiers.md#11-decision-ledger) ruled both
kinds fatal with only `program` getting the offer — a missing `requires` refuses with the remedy
named, because offering to install one would contradict the kind's own definition. ⚠ **The shipped
gate is a one-prompt shape, and env-manager [`OQ-9`](environment-manager-plan.md#open-questions-to-resolve-before-their-phase)
rules the batching two-class** (one approval for no-elevation remedies, one for `sudo`, sudo first).
Both ends are marked and **neither is ruled**: whether Phase 6.4 still wants two classes, or the
shipped shape becomes the answer, is an open decision nobody has taken.

### ✅ 30 — The Lua config transform: REMOVED, and the derive VM is standing

📄 [`lua-transform-removal.md`](../design/lua-transform-removal.md) — **both questions RULED
2026-09-11** ([`OQ-LT1`](../design/lua-transform-removal.md#13-decision-ledger) ·
[`OQ-LT2`](../design/lua-transform-removal.md#13-decision-ledger)) — **SHIPPED 2026-09-11**,
[§10](../design/lua-transform-removal.md#10-what-i-would-do-in-order)'s order, with the doc sweeps closing 2026-09-12 ·
`status: accepted` ·
written 2026-09-10, **routed here the same day**

The maintainer's instinct — *"I'm not sure it's fully thought through"* — checked out: the
`config.lua` hook between the merge and the managed-enforce step had no user, its one worked example
was served by the declarative `autonomy` kind, four of its parts were disconnected, and the
determinism the design required was unenforced (`math.random` was reachable in the sandbox). The
removal's hard part was never the deletion — the VM is shared with six packs' `derive.lua`, so the
cut had to split `luahook` along a per-symbol seam and lift the managed floor (`Enforce`) into
`internal/agentcfg` first.

✅ **BUILT 2026-09-11, and the seam held.** `internal/agentcfg` **no longer links gopher-lua**;
`internal/packload` still does, and the derive path renders every shipped pack at boot. The order
was: pin the shared sandbox and the managed floor first (`9761ba6a`), lift `Enforce` (`a1496d0e`),
cut the transform producers (`2c5c84a1`), then retire the channels — the `config_transform` key, the
`Surface.Transform` field and the `config.lua` mount — as one breaking change (`e6c45b77`), leaving
`luahook` as the pack derive sandbox and nothing else (`e958ed96`).

⚠ **This row used to name `ctx.stage.exclude`, `ValidateSandbox` and the `config_transform` key as
parts that were disconnected "today". All three are now DELETED**, along with `Result.Excluded`,
`Inputs.Script` and the `transform` layer itself — so the compose order is
`defaults → host → workspace → config-overlay → capture → computed → managed`, with no transform
slot. Seven other docs printed the old order as measured current behaviour and were corrected
(`d285b7f7`). [§5.6](../design/lua-transform-removal.md#56-documentation) left exactly TWO rows open when the removal landed —
this file and [`config-ownership-and-promotion.md`](../design/config-ownership-and-promotion.md),
which was in review and could not be edited under its reviewer. **Both are closed now**: that doc's
four transform references are dated and historical, and this row's was the one clause 💬 7 names,
fixed above.

**Answers (2026-09-11):**
> - ✅ **[`OQ-LT1`](../design/lua-transform-removal.md#13-decision-ledger) — came out SIMPLER than its
>   leaning.** *"Nobody is using it. Just delete it and pretend it never existed"* — no refusal
>   machinery, no deprecation window. Safe because the loudness is **free**: `validate.go` already
>   emits `unknown key` for a dropped config key and `manifest/load.go` calls `DisallowUnknownFields()`,
>   so a stale pack surface fails to decode. The one genuinely silent case is accepted on measured
>   grounds — the two auto-loaded files load *by existence*, so a non-empty one stops applying with
>   nothing said, and known instances are **zero**.
> - ✅ **[`OQ-LT2`](../design/lua-transform-removal.md#13-decision-ledger) — principle 4 retires with
>   the transform**, with the reason it existed recorded: *"I just didn't want to create a generic DSL
>   out of JSON."* The two capability gaps [§6](../design/lua-transform-removal.md#6-what-is-genuinely-lost)
>   names are accepted, not waved away, and the pack-side `yolo.transform` is the shape to revisit
>   first if either ever bites.

# 📦 Up next

**No live rows.** All three filed here on 2026-09-09 out of the disk/image sprint's rulings have
shipped, and so has the one the 2026-09-11/12 sprint left unbuilt — filed and closed on 2026-09-12.
Both ✅ records are parked below, for the measurements and the carve-outs they left in `AGENTS.md`.
C4 and C5 are deliberately NOT here: their go/no-go is an explicit 🧊 row.

- ✅ **1. Layer-aware image delivery — SHIPPED 2026-09-09** (`04e39353`), and it left this section
  the day its gate cleared. 📄 [`layer-aware-image-delivery.md`](../design/layer-aware-image-delivery.md) ·
  [`OQ-LI6`](../design/layer-aware-image-delivery.md#92-open-questions). `.#ociImage` is a nix2container `image.json` over a
  pinned three-tier layer plan; `.#imageCopier` is the patched skopeo; delivery is
  `internal/image/layercopy.go`. **`streamLayeredImage`, `StreamRepoTag`, the stream pipe, the
  Apple Container converter pair, the retained tar and the ssh-to-builder helper are DELETED** —
  no knob, no fallback, per [`OQ-LI5`](../design/layer-aware-image-delivery.md#91-decision-ledger).

  **Measured, and the delta case is the one that matters:**

  | | before | after |
  | :--- | :--- | :--- |
  | cold, empty store | 39.5 s · 99 layers | **24.0 s · 91 layers** |
  | `flake.nix`-only edit | **12.8 s floor** — re-spools 3.47 GB for an identical image | **2.2 s · 1 layer · 26 MB** |

  Full integration suite green including `YOLO_TEST_REBUILD_IMAGE=1`. Zero store paths in two
  layers (575 paths, 0 duplicates).

  ⚠ **The design left a gap that would have shipped an unrunnable image, and the build closed it.**
  [§3.4](../design/layer-aware-image-delivery.md#34-which-backends-get-it) said podman/macOS stays *"unchanged (stream into `podman load`)"* — but
  [`OQ-LI5`](../design/layer-aware-image-delivery.md#91-decision-ledger) deleted the stream, so that row named a mechanism
  that no longer existed. Left as written, every macOS podman launch would have copied into the
  Mac's own containers-storage, which the Podman Machine VM never reads. It now takes an archive
  plus `podman load -i`, chosen by backend before anything runs.

  ⚠ **NEITHER macOS BACKEND IS VERIFIED, and one is a new way the nightly can go red.** No Mac was
  available. Three checks, in order: (1) `nix build .#imageCopier` on **x86_64-darwin** — nixpkgs
  there resolves skopeo **1.22.2** against unstable's 1.24.0, and the patch targets
  `vendor/go.podman.io/image/v5`, which exists in both, so the question is whether every hunk
  applies across two minors. It fails loudly at build time, and `nightly-macos.yml` runs on
  `macos-26-intel`. (2) Apple Container `container image load -i` against a skopeo-written
  `oci-archive`. (3) podman/macOS `podman load -i` against a skopeo-written `docker-archive` — new
  code. Commands are in [`docs/guides/macos.md`](../guides/macos.md).

  ⚠ **SAME-DAY FOLLOW-UP: it took `main` red for two hours, on a class the design had already
  named.** Every container CI job failed with `Error during unshare(...): Operation not permitted`.
  Writing a **rootless** `containers-storage` reproduces each layer's ownership under `/etc/subuid`,
  so containers/storage needs an unprivileged user namespace — and Ubuntu 24.04's default
  `apparmor_restrict_unprivileged_userns=1` denies one. **Proven causally on a VM:** same binary,
  same command, knob `1` fails and knob `0` succeeds. Fixed in `424342c3` by running the SAME copy
  as `podman unshare -- <copier> copy …` when `podman info` says rootless, decided before the copy
  and never by retrying — [`OQ-LI7`](../design/layer-aware-image-delivery.md#91-decision-ledger), ruled 2026-09-09. One
  mechanism, one destination, layer negotiation kept on every Linux host.

  ⚠ **The instructive part is not the bug, it is that R7 named the fix and its own reassurance
  suppressed it.** R7's mitigation column, written before the build, said *"the copy runs under
  `podman unshare` — a change to how the copier is invoked, not to the design. Verify on the first
  real host, not in a nested jail."* The clause before it said *"Our layers are entirely root-owned
  … which is the case that works"* — and that is backwards: root-owned layers are precisely the
  case that NEEDS the mapping, which is why this failed on every rootless host rather than an exotic
  one. A risk register that carries the right mitigation behind a wrong premise reads as closed.
  R7 is retired as behaviour; R8 is what made it fatal rather than a degrade.

  ⚠ **And a nested jail could not have caught it, for a reason now in `AGENTS.md` as a second
  carve-out:** podman-in-podman forces `--userns=host` and runs as ROOT, so a nested jail reports
  `rootless: false` and takes every rootful branch. Every rootless-only path is invisible to it.

  **New CI cost:** every image-building job also builds `.#imageCopier` (~2 min cold per nixpkgs, 0
  warm). `publish.yml` and `just cachix-push` push it — an optimization only, with nothing wired to
  a cache miss ([`OQ-LI1`](../design/layer-aware-image-delivery.md#91-decision-ledger)).

- ✅ **2. The one-time adoption archive — [`OQ-CO7`](../design/config-ownership-and-promotion.md#13-decision-ledger),
  SHIPPED 2026-09-12.** 📄 [`config-ownership-and-promotion.md`
  §10 step 8](../design/config-ownership-and-promotion.md#10-what-i-would-build-in-order) ·
  [§6.3.3](../design/config-ownership-and-promotion.md#633-what-survives-as-a-guard). Filed here the
  same day by the ruled-but-unbuilt sweep — it was the one ledger row of the five shipped designs'
  thirty-one with no code behind it — and closed the same day. `entrypoint.archiveAdoption`, called
  from `persistStatefulSurface`, copies the pre-existing file once before an adopting render composes
  over it; `render.Target.ArchivePath` says where, at both notches.

  **ONE call site, both notches, which is the part worth remembering.** The host's `own` adoption and
  a jail's `firstMigration` go through the same stateful writer, so they cannot end up with different
  nets — or with one silently missing, which is how the gap survived the sprint that ruled it. It
  matters most where the guard is weakest: `confirmHostLosses` reads `EntryLosses` and fires only on
  first apply, so the `assert` → `own` switch is unprompted — the exact transition that drops a
  deep-merged leaf — and an unattended boot has no TTY for a prompt at all.

  **Three things the ruling left open, decided at build time** and recorded in
  [§6.3.3](../design/config-ownership-and-promotion.md#633-what-survives-as-a-guard) plus a ledger row: the bucket is keyed by
  SURFACE rather than by the `<stamp>/` generation the other buckets use (under the stamped layout
  `yolo prune`'s keep-newest-3 would sweep the originals of every surface but the newest few);
  idempotency is the archive's own existence (a second adoption would otherwise overwrite the user's
  original with yolo's output); and a copy that cannot be written REFUSES the adoption, leaving the
  file untouched, rather than warning past it.

  ⚠ **It shipped with four measured gaps, and they are recorded rather than closed over.**
  Verification after the build found that `yolo config reset` spends the one-per-surface slot on
  yolo's own output; that deleting a surface's overlay sidecar while keeping `last_render` drops the
  adopted keys with no archive, no loss line and no prompt; that an existing-but-UNREADABLE file
  reaches the gate as zero bytes and is replaced wholesale; and that an adopting render is still
  filed as *in sync* in `yolo host apply`'s verdict. None is a regression — each is a loss that
  predates the archive — and none is fixed, because every candidate fix reverses a ruling, so each
  is yours to call. They live in
  [§6.3.3](../design/config-ownership-and-promotion.md#633-what-survives-as-a-guard), beside the
  ruling they qualify. **This row stays ✅:** the step is built, and a ✅ that quietly meant
  "complete" is what [`OQ-CO7`](../design/config-ownership-and-promotion.md#13-decision-ledger)'s own
  history is the case study for.

  ✅ **The design's ledger now has a Built column** — added by the same sweep that filed this row,
  which is what made the gap recoverable from the doc at all.
  [`the-load-sentinel-is-not-a-liveness-oracle.md`](../design/the-load-sentinel-is-not-a-liveness-oracle.md)
  and [`disk-levers-and-backfill.md`](../design/disk-levers-and-backfill.md) were the models. Still
  worth adding to [`report-tiers.md`](../design/report-tiers.md)'s.

### 💬 31 — Which package manager an environment actually has, and who picks it

📄 [`provisioner-sets.md`](../design/provisioner-sets.md) — **thirteen live questions** after the 2026-09-11 carve:
[`OQ-PS1`](../design/provisioner-sets.md#OQ-PS1) · [`OQ-PS5`](../design/provisioner-sets.md#OQ-PS5) · [`OQ-PS6`](../design/provisioner-sets.md#OQ-PS6) · [`OQ-PS7`](../design/provisioner-sets.md#OQ-PS7) ·
[`OQ-PS8`](../design/provisioner-sets.md#OQ-PS8) · **[`OQ-PS9`](../design/provisioner-sets.md#OQ-PS9) · [`OQ-PS10`](../design/provisioner-sets.md#OQ-PS10) · [`OQ-PS11`](../design/provisioner-sets.md#OQ-PS11) ·
[`OQ-PS12`](../design/provisioner-sets.md#OQ-PS12)** (new) · [`OQ-NX4`](../design/provisioner-sets.md#OQ-NX4) · [`OQ-NX5`](../design/provisioner-sets.md#OQ-NX5) · [`OQ-NX8`](../design/provisioner-sets.md#OQ-NX8) ·
[`OQ-NX9`](../design/provisioner-sets.md#OQ-NX9) — with [`OQ-PS2`](../design/provisioner-sets.md#decision-ledger), [`OQ-PS3`](../design/provisioner-sets.md#decision-ledger) and [`OQ-PS4`](../design/provisioner-sets.md#decision-ledger) RULED · written
2026-09-11, a sibling of [`program-delivery.md`](../design/program-delivery.md) rather than an
extension of it — [`OQ-PD16`](../design/program-delivery.md#decision-ledger) ruled that doc
jail-only, so extending it would have reversed a ledger row.

**The reframe that produced it.** `program` and `requires` look redundant, and at the host notch
today they are: both mean *"check, and hand you a command."* Two models were tried and both failed —
*install vs presence* describes the check rather than the cause, and *yolo produces it vs external*
dies on `via: npm` being an external manager yolo merely drives, and on a user having `claude` from
brew. What survives: **an environment has a SET of available provisioners, and a pack should declare
a need rather than a resolver.** The notch only correlates; the environment decides.

**Three findings, each measured:**

- **The host is the only notch where yolo drives no provisioner at all** — jail has the nix image,
  guest has `MaterializeDarwin`, the host has nothing. That is why `program` degenerates there.
- **The system package manager is modelled and never executed.** `install_hints` produces brew/dnf
  commands and `check-deps` writes a Brewfile; Phase 6.4's offer-to-run is deferred **by name in the
  code**, with its confirm UX already ruled as [`OQ-9`](environment-manager-plan.md#open-questions-to-resolve-before-their-phase).
- ⚠ **`depcheck.Check` ranks the declaring pack's OWN installer FIRST and keeps the manager's
  command as a fallback token** — with a stated reason: *"a tool with a first-party installer has a
  first-party updater, and a distro package silently pins it to whatever that repo has."* So
  "Homebrew as the host default" **reverses a considered ranking**, and the thing it trades away is
  version currency.

**And one premise correction worth carrying:** *"there's no nix package"* is wrong — nixpkgs has
**6/6** of the agent CLIs; three are `unfree`, so a bare `nix profile install` refuses. A licensing
gate, not an absence. Coverage elsewhere: `brew` 6/6 (4 casks), `pacman` 2, `dnf` 1, `apt` 0 — so
"prefer the system manager" is right on macOS and collapses on Linux, where nix is the only manager
covering all six.

✅ **The overlap this row used to warn about is GONE, because the two docs are one.**
`noncontainer-nix-environment.md` had owned the host notch since 2026-08-02 and kept six live
questions on it; `9b9da960` merged it into this doc and re-prefixed its bare numbering to `NX`. Its
two questions that overlapped were not routed out of scope in the end — they were **absorbed**: the
retired doc's bare `3` folded into [`OQ-PS1`](../design/provisioner-sets.md#OQ-PS1) and its bare `7` into
[`OQ-PS6`](../design/provisioner-sets.md#OQ-PS6). ⚠ This row read *"PS1 cannot be ruled without PS1"* until 2026-09-12, which
is what a dependency looks like after both of its ends are mapped onto the same id; the dependency is
real and is now internal to one question.

⚠ **[`OQ-PS1`](../design/provisioner-sets.md#OQ-PS1) is not a unique id across the corpus.** A live `oq` directive defines
that spelling in **both** this doc and
[`provider-switching.md`](../design/provider-switching.md#10-open-questions) (💬 26), which are
different questions — `vantage-check` only enforces uniqueness within a document, so nothing flags
it, and an unlinked reference is ambiguous. Always link it. Same hazard
[`config-ownership-and-promotion.md` §12](../design/config-ownership-and-promotion.md#12-open-questions) already records for `OQ-CO`.

⚠ **The carve raised the count on purpose, 11 → 13.** [`OQ-PS1`](../design/provisioner-sets.md#OQ-PS1), [`OQ-PS5`](../design/provisioner-sets.md#OQ-PS5) and [`OQ-PS7`](../design/provisioner-sets.md#OQ-PS7) each asked two
things, so none could be ruled: *"I need these split out into OQs, I can't follow this subquestion thing."*
They are now [`OQ-PS9`](../design/provisioner-sets.md#OQ-PS9) (does yolo help install nix), [`OQ-PS10`](../design/provisioner-sets.md#OQ-PS10) (does the
host's nix provisioner leave anything behind), [`OQ-PS11`](../design/provisioner-sets.md#OQ-PS11) (do `program` and `requires`
collapse) and [`OQ-PS12`](../design/provisioner-sets.md#OQ-PS12) (may an override name a recipe no pack ships). No id was
renumbered. [`OQ-PS8`](../design/provisioner-sets.md#OQ-PS8) was deliberately **kept joined** and retitled — its two halves fail
the could-be-ruled-separately test, because the env half is on the table only as the alternative mechanism
for the interactivity half.

⚠ **And one sub-question was withdrawn as PROVABLY VACUOUS rather than answered.** `PS1(c)` was gated on
*"only if `--sealed` is best-effort"* — which the maintainer could not parse, and checking the premise
explains why: `applySealed` refuses **exactly two** inputs, a present `yolo-jail.local.jsonc` and
outstanding capture keys (`internal/cli/apply.go:800-833`), and reads no toolchain or store path at all.
A nix profile at a path yolo names is Declared-impure — `mise_tools`' own tier — so sealing never had an
opinion. The conditional is withdrawn and [§6.3](../design/provisioner-sets.md#63-nix-profile---profile-dir-the-only-candidate-that-reaches-a-users-own-path)'s "the closure table gains a row" is retracted in place.
**A question the reviewer cannot parse is a broken question**, and this one was broken because it was
wrong.

**What is actually blocking, as of 2026-09-12 — because "thirteen" is the wrong unit.** With ✅ 29
and ✅ 30 shipped and 💬 25 down to one question the build opened, this row is where most of what is
left to decide now sits. The thirteen do not block equally, and
[§9](../design/provisioner-sets.md#9-what-i-would-build-in-order)'s build order is what sorts them:

- ✅ **Steps 1–3 are blocked by NOTHING and are defect-shaped.** Two provisioners armed and
  unreachable on the guest, silently; the profile report running only inside the macos-user `check`
  section when the predicate `describe` already uses would run it anywhere; and `yolo host apply`
  disagreeing with `describe` about whether the host manages `packages:`. They are ruled by P3 and by
  the narrow halves of [`OQ-NX8`](../design/provisioner-sets.md#OQ-NX8)/[`OQ-NX9`](../design/provisioner-sets.md#OQ-NX9), which lean and do not wait.
  **Nothing in this row's count needs answering to start them.**
- **Step 4 — the precedence order, PRINT-ONLY — is the first thing a ruling gates**, and it
  needs exactly three: [`OQ-PS6`](../design/provisioner-sets.md#OQ-PS6) for its default, [`OQ-PS7`](../design/provisioner-sets.md#OQ-PS7) for its grain,
  [`OQ-PS12`](../design/provisioner-sets.md#OQ-PS12) for its scope. **Those three are the real blocker.**
- **[`OQ-PS11`](../design/provisioner-sets.md#OQ-PS11) is upstream of [`OQ-PS5`](../design/provisioner-sets.md#OQ-PS5)** — under
  [`OQ-PS3`](../design/provisioner-sets.md#decision-ledger)'s ruling a `requires` is already a need with an empty recipe list,
  so you may be naming ONE kind rather than re-spelling one of two. Rule PS11 first or PS5 is
  unanswerable.
- ⚠ **[`OQ-PS8`](../design/provisioner-sets.md#OQ-PS8) was opened by a MEASUREMENT, not a review**, and is the only one
  here with hardware behind it: codex's installer prompted `Start Codex now? [y/N]` on `/dev/tty`
  mid-`--version`-probe and a human answered `N`. It honors `CODEX_NON_INTERACTIVE=1` and
  `packdecl.Install` has no field that can carry it.
- **The four remaining — [`OQ-PS1`](../design/provisioner-sets.md#OQ-PS1), [`OQ-PS9`](../design/provisioner-sets.md#OQ-PS9), [`OQ-PS10`](../design/provisioner-sets.md#OQ-PS10),
  [`OQ-NX4`](../design/provisioner-sets.md#OQ-NX4)/[`OQ-NX5`](../design/provisioner-sets.md#OQ-NX5)** — block step 5 or nothing at all, and step 5 is
  ruled to come last on purpose: it is the first mutation of a real machine and must not be the
  increment that also introduces the preference surface.

**Answer:**
> _(empty — fill in when decided. **[`OQ-PS3`](../design/provisioner-sets.md#decision-ledger) — the model question — is RULED**: a pack
> declares a need plus the recipes that can produce it, and privileges none of them. That cascaded into five
> places, so what remains is narrower than the count suggests — and the smallest useful sitting is
> **[`OQ-PS6`](../design/provisioner-sets.md#OQ-PS6) · [`OQ-PS7`](../design/provisioner-sets.md#OQ-PS7) · [`OQ-PS12`](../design/provisioner-sets.md#OQ-PS12)**, which unblocks
> step 4 and leaves the other ten where they are.)_

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

- 🔒 **[OQ-DF4](../design/minimal-disk-footprint.md#112-open-questions) — does yolo owe the machine a stated NUMBER, or only a policy?** 📄
  [`minimal-disk-footprint.md` §11.2](../design/minimal-disk-footprint.md#112-open-questions). **Filed here 2026-09-09 when ✅ 16
  closed** — it is the last of that doc's four and it is BLOCKED rather than undecided. *Leaning:
  policy, not a number* — if the write path bounds itself, the budget is a property of the design
  rather than a dial, and "minimal" is not a number a user should have to discover. **Held loosely,
  and the instrument now exists:** `yolo stores` is the machine-wide inventory, so the measurement
  that flips this is takeable. What it is waiting on is a post-reclaimer re-measurement showing
  whether any residual is caught only by a ceiling. ⚠ **A byte-budget config key is downstream of
  this, not a way to start it** — building the dial before the policy it parameterises is the wrong
  order.

- 🔒 **Program delivery [§10](../design/program-delivery.md#10-what-i-would-build-in-order) — the two steps that are blocked, not merely unscheduled.** 📄
  [`program-delivery.md` §10](../design/program-delivery.md#10-what-i-would-build-in-order). The unblocked step is in 📦. ⚠ **Order reversed 2026-09-04 ([OQ-CP1](../reference/agent-cli-copies.md#oq-cp1)): evergreen ships BEFORE capture, carrying A7's V-axis prune; the disk justification that put capture first is retracted.**
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

- ✅ **macos-user has no package floor and no provisioning stage, so four config keys render and
  install nothing.** — **BOTH HALVES SHIPPED 2026-09-12** (the floor, then the confined stage). 📄 [`macos-user-provisioning.md`](../design/macos-user-provisioning.md) —
  **[`OQ-P1`](../design/macos-user-provisioning.md#decision-ledger) · [`OQ-P2`](../design/macos-user-provisioning.md#decision-ledger)** — [`OQ-P3`](../design/macos-user-provisioning.md#decision-ledger) and
  [`OQ-P4`](../design/macos-user-provisioning.md#decision-ledger) were **answered and compacted 2026-09-11**. A container jail
  gets tools two ways: an image floor of **36** baked packages (git, node, mise, ripgrep, fd…) and
  an imperative stage the launch runs inside it
  (`mise install`, the generated `~/.yolo-bootstrap.sh` that npm-installs LSP servers and MCP
  presets). This backend has NEITHER — only `packages:`, containing exactly what the user
  declared. So `mise_tools`, `lsp_servers`, `mcp_presets` and the lazy agent-CLI installers are all
  inert; four now warn and the agent-CLI case is still silent, which is the one that fails on a
  user's first real command rather than at launch. **The two halves are strictly ordered** — the
  stage cannot run without the floor, since `mise install` needs mise and the bootstrap script
  needs npm — so there is no partial credit.

  ⚠ **Two of the four are now ruled, and the "~19" above was wrong** (audited 2026-09-11 against
  `corePackagesFromNixpkgs`, which has **36** entries — every line citation in the doc was stale,
  and [`OQ-P1`](../design/macos-user-provisioning.md#decision-ledger)'s maximum doubles). **[`OQ-P3`](../design/macos-user-provisioning.md#decision-ledger)** — the state partition
  follows the container's: mise *data* machine-wide (its premise that sharing it is a collision
  was backwards — the container shares it by design), config/npm/`~/.local` per-workspace.
  **[`OQ-P4`](../design/macos-user-provisioning.md#decision-ledger)** — eager, because the lazy launchers cover agent CLIs only,
  never `mise_tools` or LSP servers. **What still needs you: how much floor
  ([`OQ-P1`](../design/macos-user-provisioning.md#decision-ledger)) and GNU or BSD userland ([`OQ-P2`](../design/macos-user-provisioning.md#decision-ledger))** — rule P2 first,
  since nine of the 36 are its GNU set.

  ⚠ **And the second half is a NEW confined step, not a port.** The doc implied the bootstrap was
  already sandboxed; it is not — `DarwinBootstrapArgv` emits
  `sudo --user=… /usr/bin/env -i … <yolo> internal darwin-bootstrap` with no `sandbox-exec`
  (`internal/macosuser/runplan.go`, verified 2026-09-11 and still true after the build: the STAGE is
  the confined step, and `PlanInvariants` refuses a launch whose `ProvisionArgv` has lost
  `/usr/bin/sandbox-exec`).

  ⚠ **BOTH RULED 2026-09-11, and HALF ONE IS BUILT 2026-09-12
  ([§9](../design/macos-user-provisioning.md#9-what-shipped-half-one)).**
  [`OQ-P1`](../design/macos-user-provisioning.md#decision-ledger) went **against** its leaning: the floor is **everything the
  container image bakes, minus an EXPLICIT darwin exclusion list** — *"I'd rather pain than
  something silently skipped… if we have a fatal error, then we have the opportunity to fix it."*
  [`OQ-P2`](../design/macos-user-provisioning.md#decision-ledger) ruled **no GNU userland**, which populates the first entries of
  that list. ⚠ **The two compose with a gap:** an unbuildable package is caught by the fatal, but a
  GNU one **builds fine and ships silently**, so the policy exclusions got their own assertion —
  `internal/darwinpkg/floor_policy_test.go`, a predicate over the DERIVED floor, which catches a GNU
  package added to the image core with no list edit at all.

  **What the eval said, derived from this Linux jail rather than a Mac** — the composition is
  [§9.1](../design/macos-user-provisioning.md#91-the-floors-composition-and-how-it-was-derived), which
  is the authority and prints the resolved list. As of 2026-09-12 the exclusion list is **ten of the
  image core's thirty-six** — one by necessity (`iptables`, the only entry unbuildable on either
  `aarch64-darwin` or `x86_64-darwin`) and nine by policy — leaving a **26-package** floor.

  ⚠ **`procps` is NOT Linux-only**, though this row and the design's own risk table both said so —
  on darwin nixpkgs resolves that attr to `unixtools.procps`, a wrapper around the Mac's own BSD
  `ps`/`pgrep`/`pkill`, which is exactly what [`OQ-P2`](../design/macos-user-provisioning.md#decision-ledger)
  wants. **Guessing the exclusion list would have removed a working tool** for a reason nobody
  would have re-checked; deriving it is what caught that.

  ⚠ **And the same sentence got its other half WRONG in the shipping direction, which is the
  one that costs something.** The Go comment keeping `which` off the policy exclusions asserted
  *"`which`, `procps` — on darwin nixpkgs resolves both through `unixtools`"*. Re-measured against
  the same locked inputs: `pkgs.which` is **GNU `which-2.25`** and is not one of `pkgs.unixtools`'
  attrs at all. So GNU `which` shipped on a floor whose governing ruling is *no GNU userland*,
  **with the policy gate green** — the exact silent shipment that gate exists to prevent. Fixed
  2026-09-12 (`76306dcc`); macOS's own `/usr/bin/which` serves instead. The general lesson is
  [§9.1](../design/macos-user-provisioning.md#91-the-floors-composition-and-how-it-was-derived)'s:
  the policy predicate's unprefixed half is a known-incomplete **supplement**, not an enumeration —
  `cpio`, `ed`, `m4`, `nano`, `bc`, `time`, `texinfo`, `groff` and `wget` would all pass it green,
  and the honest oracle (`meta.homepage`) is a nix evaluation a Go test cannot perform.

  **Two costs shipped with the floor and were accepted, not overlooked:** **every macos-user launch
  now needs the repo root** (the empty-`packages:` exemption is gone; `--dry-run` still exempt), and
  the first launch on a machine builds the whole native closure — ⚠ **NOT MEASURED**, like every
  other runtime claim about this change, which was implemented where macOS cannot run.

  ⚠ **HALF TWO IS BUILT TOO, the same day
  ([§10](../design/macos-user-provisioning.md#10-what-shipped-half-two)).** The launch grew a
  third privileged step between the bootstrap and the agent — `mise install` plus the generated
  bootstrap script, under `sandbox-exec` with the session's own profile — and the two launch
  warnings that said these keys install nothing here were retired, because both named a
  mechanism that is now false. Four things are worth carrying out of it:

  - **It is a NEW CONFINED step, and the confinement buys less than it sounds like.** The
    profile is `(allow default)` with targeted denies, so it bounds the stage OUTSIDE the
    sandbox and promises nothing inside it — which is exactly what the 2026-09-11 hardware run
    already showed, when two vendor installers under this very profile rewrote yolo's generated
    rc files and prompted on the tty.
  - ⚠ **The stage does NOT go through `sudo --login`, and must not.** That flag does not execve
    its argv — it concatenates, backslash-escapes, and leaves `$` for a login shell — so a
    script full of `${PIPESTATUS[0]}` and `$(date …)` would provision nothing and exit 0. The
    LAUNCH argv keeps the flag (it is the `path_helper` fix, measured); the stage takes the
    bootstrap argv's shape instead, and a plan invariant pins the difference so the natural
    "make them consistent" edit fails loudly.
  - **The design said the bootstrap script goes at `~/.yolo-bootstrap.sh`. It does not.** That
    path is a BIND on the container; here it would put one workspace's generated script in a
    home every workspace shares — the write-write race the home split exists to end, re-created
    by half two. It and the LSP sentinel go in the workspace sidecar instead.
  - **The takeaway that generalises:** the home split linked DIRECTORIES, and the container also
    binds a set of home-root FILES per workspace that the layout covers none of. Any future
    generator writing a home-root file on this backend has to place it itself.

  ⚠ **A FIFTH thing, added after the build and not in the four above: the failure policy the code
  did not implement.** [§4](../design/macos-user-provisioning.md#4-the-proposed-shape) states in bold
  that *a failing stage must not abort the launch*, and the orchestrator did the opposite on every
  path where the stage could not START — `sudo` refusing authorization, `sandbox-exec` rejecting the
  profile, a missing `/bin/bash` are all non-zero for reasons nobody chose, and those are items 1 and
  4 of what a Mac has to settle. A workspace that merely *declared* `mise_tools` could not launch,
  and the message blamed the user. It now discriminates on the `PROVISIONING FAILED` marker, which
  exists if and only if the script ran; an unreadable log is treated as a veto, because honoring a
  veto that was not given is recoverable and ignoring one that was is not
  ([§10.7](../design/macos-user-provisioning.md#107-the-failure-policy-the-code-did-not-implement),
  `3dbc10f8`).

  ⚠ **NOT MEASURED**, all of it — half two was built from the same Linux jail as half one. What
  a Mac has to settle is listed at
  [§10.8](../design/macos-user-provisioning.md#108-what-a-mac-has-to-settle), ordered by what
  would invalidate the most: that `sandbox-exec` accepts the stage process under this session's
  profile at all, that the confined stage reaches the network, and that it can write the
  prefixes it installs into. ⚠ **That list grew a correction of its own** — item 1 used to say *"a
  profile already loaded for this session"*, and nothing has loaded one when the stage runs: the
  orchestrator runs it at step 3.5, **before** the agent launch, so the stage is the FIRST process
  under that profile, not a second. **Every item on that list is a runbook entry now** — items 6-10
  of [`macos-user-manual-checks.md`](runbooks/macos-user-manual-checks.md) — and **none has been
  run**; see the 🔒 row below.

- ✅ **The macos-user home has one tier where it needs two, and content delivery just made it
  bite — RULED, and BUILT 2026-09-12
  ([§10](../design/macos-user-home-tiers.md#10-what-shipped)).** The layout lands above genStep
  #1 in `RunDarwinBootstrap`, the `SharedDirs` mirror with it, and the content overlay stopped
  `RemoveAll`-ing `~/.claude` to deliver `.claude/skills`. Three things shipped **beyond** the
  four rulings because A′ is not finished without them: `MISE_DATA_DIR` named
  (`macosuser.SandboxMiseData`, which is also [`OQ-P3`](../design/macos-user-provisioning.md#decision-ledger)'s
  answer), the login rc files stopped baking one workspace's PATH into a shared `$HOME`, and
  the machine-wide-state warning was retired along with four stale in-code claims. ⚠ **Every
  runtime claim is NOT MEASURED** — the sandbox uid creating the sidecar through the ACL, the
  refusal a pre-A′ account gets, `path_helper` after the rc indirection — and the four manual
  Mac checks below are owed a re-run for it.
  📄 [`macos-user-home-tiers.md`](../design/macos-user-home-tiers.md) — **[`OQ-HT2`](../design/macos-user-home-tiers.md#decision-ledger) was the only one left**;
  [`OQ-HT1`](../design/macos-user-home-tiers.md#decision-ledger), [`OQ-HT3`](../design/macos-user-home-tiers.md#decision-ledger) and
  [`OQ-HT4`](../design/macos-user-home-tiers.md#decision-ledger) were **answered and compacted 2026-09-11**.
  `SandboxHome()` is the constant `/Users/_yolojail`, so the machine tier
  (credentials — correct, and the point of a dedicated account), the workspace tier (pack
  `state`, agent history) and the session tier are one directory. Two symptoms were static
  information leakage between workspaces and were warned about. The third is new and is a
  write-write RACE: since 2026-09-03 skills and briefings are composed and copied over that home
  on every entry, so a second workspace launching while the first runs replaces its briefing —
  per-project prose an agent is mid-session reading.

  ⚠ **The shape changed, and so did the trap** (audited and compacted 2026-09-11). The proposal is
  **A′** — `HOME` stays `/Users/_yolojail` and `~/.claude` and kin become symlinks into
  `<workspace>/.yolo/home/`, which is the location every other backend already uses — **not** the
  per-workspace `/Users/_yolojail/workspaces/<cname>` home this row used to describe.
  [`OQ-HT4`](../design/macos-user-home-tiers.md#decision-ledger) ruled it on the parity constraint: one mechanism on every
  backend, or packs have to feature-detect. **The sharpest form of that argument, reached
  independently on the Mac side:** the container backends *do not move their home per workspace* —
  `HOME` is `/home/agent` always, and specific subdirectories are mounted in from the workspace
  sidecar — so the original proposal was diverging from the very model it claimed to adopt.

  ⚠ **"The single home IS the credential-sharing mechanism" is RETRACTED.** The mechanism is the
  `shared_credentials` hook — `Env.linkSharedCredential` writing a *relative* symlink into a
  pack-declared `scope: machine` dir — and colocation only supplies that dir's backing. **The real
  trap is narrower and worse:** the link is relative *so it resolves through whichever mount backs
  the state dir*, so a bare `~/.claude → <ws>/.yolo/home/claude` symlink reproduces the host's
  **dangling** view. Every `SharedDirs` entry must be mirrored into the sidecar.

  **[`OQ-HT2`](../design/macos-user-home-tiers.md#decision-ledger) is re-scoped and is now the single ruling between here and
  buildable**: credentials never move under A′, so it asks only what happens to the
  workspace-scope state already sitting in `/Users/_yolojail`.

  ⚠ **ALL FOUR RULED as of 2026-09-11 — this row is done and the design is `status: accepted`.**
  [`OQ-HT2`](../design/macos-user-home-tiers.md#decision-ledger) closed last and closed *smaller* than its leaning: **no
  migration at all.** *"Nobody is using it. No transition needed. If I need to wipe it first,
  that's fine."* So A′ ships with no one-shot mutation, no `.pre-tiers-<date>` directory and no
  first-launch copy path — `sudo rm -rf /Users/_yolojail` before the first launch **is** the
  migration. What that gives up (the workspace tier's agent history) is affordable because the
  backend's only session was the 2026-09-11 hardware run, whose content is yolo-generated.
  **[`macos-user-provisioning.md`](../design/macos-user-provisioning.md)'s half two is no longer
  blocked by the home split** — which is now built rather than merely unblocking.

  ⚠ **The build shipped TWO runtime defects unfixed, deliberately**, because neither leaves the tree
  red and both are behaviour on a backend CI cannot run
  ([§10](../design/macos-user-home-tiers.md#10-what-shipped); runbook item 10):

  1. **A transient pack-load failure permanently poisons the account home.** The link set is derived
     from the loaded packs and a `LoadJailPacks` error does not abort the bootstrap (A12: every step
     still runs), so `install_home_overlay` creates a REAL `~/.claude` and every later launch
     refuses forever — with a remedy (`sudo rm -rf <home>`) that destroys the machine tier the
     shared-credentials hook exists to preserve.
  2. **An occupied MIRROR path prints a remedy that cannot fix it.** The refusal always names
     `rm -rf` of the ACCOUNT home; a mirror's path is in the WORKSPACE SIDECAR, which that command
     does not touch — so following the instruction leaves the launch refusing forever. It is
     reachable today: `run/prepare.go`'s `migrateOldOverlay` COPIES every `packload.SharedDirs`
     entry into `<ws>/.yolo/home` and never deletes, so any workspace that ever ran a container
     launch on that machine may already hold one.

  ⚠ **A mutation pass is what found them, and it found two unpinned rules the table above claimed.**
  Truncating the mirror loop to its first entry passed the whole short suite — both mirror tests used
  a one-element list, and `packs/agy` declares a second machine-scope dir, so `packs: ["claude","agy"]`
  was exactly the untested case. And substituting literal slices for `packload.WritableDirs`/`SharedDirs`
  left the suite fully green, so a pack added tomorrow would have got no link and no mirror, silently.
  Both are pinned now. **The reusable form is AGENTS.md's:** a test that pins the callee while the
  call site is unpinned is not a test — ask whether it fails when you delete the call site.

- ✅ **The four manual Mac checks are RUN, and all four PASSED.** 📄
  [`runbooks/macos-user-manual-checks.md`](runbooks/macos-user-manual-checks.md). Everything that
  could be automated was, on 2026-09-04: two darwin-gated harnesses run on any Mac with NO
  privilege and cover the generated home, a blocker actually refusing, the composed overlay
  landing and replacing a removed pack's subtree, the staging commands really executing, the mode
  bits, and the J2 fresh-inode rule. The remaining four needed root or a kernel, and were four
  commands rather than a project.

  **Measured 2026-09-10** in one session on the maintainer's Apple Silicon Mac (macOS 26.5,
  arm64), host `yolo` `0.8.0+1293.g520e848d` — **the first live `macos-user` session there.**
  The privilege transition returns `_yolojail` and the workspace path. The sandbox is refused
  both `/Users/<host user>/.ssh` and `/Library/Keychains`, which settles the one fact no unit
  test in this repo can reach: **the kernel really loads the profile.** `packages:` reaches the
  agent natively — `just` and `fzf` both resolved into the store profile, and that pair was
  chosen because each had a competing host copy (Homebrew's `fzf`, mise's `just`) that would
  have won had the login-rc re-prepend lost to `path_helper`, **which answers [`OQ-1`](runbooks/mac-go-port-verification.md#2-macos-user-backend--real-launch-oq-1-the-load-bearing-unknown)**. And all
  fourteen built-in skills plus the native-backend briefing landed in the sandbox home.

  ⚠ **The runbook has grown from four items to ten, and items 5-10 have never been run.** They are
  the 🔒 row immediately below, promoted out of this bullet on 2026-09-12 so a never-run check is
  visible to the counts rather than parked inside an ✅. One of them also invalidates an item that
  DID pass: item 3 (`packages:` beating `path_helper`) is worth re-running, because the login rc now
  re-prepends a **variable** rather than a baked PATH.

  **This closed the Mac-gated column as it stood; it does not retire the runbook** — none of the four is
  pinned by a test, so a change to the privilege transition, the Seatbelt profile, the native
  nix chain or content staging needs them run again. The measurement also corrected the runbook
  twice: the two refusals do NOT print the same message (`EACCES` for the home path, `EPERM` for
  the keychain), and item 3 now says to pick a package that has a host rival, since one without
  cannot tell a working re-prepend from a lucky PATH. ⚠ **These four are a human's to run:**
  `sudo -n true` reports `a password is required` and every macos-user argv leads with
  `sudo --user=_yolojail`, so an agent attempting the launch hangs on the prompt.

- 🔒 **Runbook items 5-10 have NEVER BEEN RUN, and every runtime claim the macos-user pair
  shipped on 2026-09-12 rests on them.** 📄
  [`runbooks/macos-user-manual-checks.md`](runbooks/macos-user-manual-checks.md) items 5-10. **Filed
  as its own row 2026-09-12**, out of the ✅ bullet above, because a never-run check recorded inside
  a closed item is invisible to this file's counts — the same reason C4/C5 was promoted from a
  preamble paragraph on 2026-09-02.

  **Both designs say so themselves.** [`macos-user-home-tiers.md`
  §10](../design/macos-user-home-tiers.md#10-what-shipped) and
  [`macos-user-provisioning.md` §9](../design/macos-user-provisioning.md#9-what-shipped-half-one) /
  [§10](../design/macos-user-provisioning.md#10-what-shipped-half-two)
  each carry a NOT MEASURED warning over everything runtime: both halves were implemented from this
  Linux jail, on a backend that cannot run here. What IS pinned is the Go half — the composed script
  (parsed with `bash -n`), the plan invariants, the generated bytes, the layout's position, the
  derived floor and the policy predicate, each verified by deleting its call site.

  **What the six settle**, in the order that would invalidate the most:

  - **5 — the per-workspace home layout** (the sandbox uid creating `<ws>/.yolo/home` through the
    shared-group ACL; the refusal a pre-A′ account gets; `MISE_DATA_DIR` holding a real store; the
    login-rc re-prepend still beating `path_helper` now that its value arrives by variable).
  - **6 — the floor is on the sandbox's PATH**, which is also what would retire
    [§2](../design/macos-user-provisioning.md#2-what-this-costs-today)'s *"no node, no npm"* row for
    the `via: npm` agent launchers.
  - **7 — the provisioning stage runs, and is confined**: `sandbox-exec` accepting the stage
    process, the confined stage reaching the network, and it being able to write the prefixes it
    installs into. Three things composing correctly, none measured.
  - **8 — a stage that cannot START does not kill the launch**, the half the code was wrong about
    until `3dbc10f8`.
  - **9 — `mise_tools` and `lsp_servers` actually arrive.** The runbook's *known-absent* list
    struck both entries on this claim alone; until a Mac says otherwise, an absence there is a bug
    worth reporting with `<workspace>/.yolo/startup.log` attached.
  - **10 — the two layout defects a mutation pass found**, both refusal-message quality rather than
    data loss, and both found by reasoning rather than measurement.

  ⚠ **These are a human's to run, like the four above:** `sudo -n true` reports
  `a password is required`, so an agent attempting the launch hangs on the prompt.

- ✅ **The FIVE provisioner measurements are RUN too, and one of them found a defect nothing else
  could see.** 📄 [`../design/provisioner-sets.md` §15](../design/provisioner-sets.md#15-what-a-mac-session-should-measure),
  the list `9b9da960` added the same morning. One session on the same Mac, **2026-09-11**, host
  `yolo` `0.8.0+1336.gecb17e8c` — `just install` first, because two of the 43 pending commits were
  capture/`agentcfg` fixes M4 exercises. **Four of the five corrected the item that asked them**,
  which is the reason to read the results and not the verdicts:

  **What the guest can provision (M1).** The installer row works and the npm row does not:
  `claude`, `codex` and `agy` install and run (two from scratch), `opencode` and `pi` print
  `npm: command not found` and exit 1. So
  [`macos-user-provisioning.md` §2](../design/macos-user-provisioning.md#2-what-this-costs-today)'s
  single "launcher generated, but no node and no npm — silent" row **splits per `via`**, and its
  npm half is loud rather than silent. What is still unproven there is EVERGREEN, not install:
  `claude`'s update failed `124`, unbounded, because `timeout(1)` does not exist on macOS and
  `shims.go:1020-1029` rules that explicitly.

  **What the two hint mechanisms do on hardware (M2, M3).** The generated Brewfile parses, casks
  included — [`OQ-PS2`](../design/provisioner-sets.md#decision-ledger)'s precondition, met. The unfree
  refusal is real on darwin, **and `NIXPKGS_ALLOW_UNFREE=1` does not lift it**: flake evaluation is
  pure, so the variable is never read and `--impure` is required. That one had been asserted from a
  Linux jail and is wrong on both platforms; it matters because a flag is something yolo would be
  choosing for the user, unlike a variable the user exported.

  **What capture does (M4).** The recording half works end to end on hardware — first kernel load
  of capture's *own* Seatbelt profile, which is the last thing
  `internal/macosuser/capture.go`'s "no Seatbelt profile has been loaded by a kernel" was still
  right about (`0f14a2f5` fixes the comment). The staging path is transient; the store entry is the
  artifact.

  **What needed no Mac at all (M5).** darwin resolves `..` physically, same as Linux, so
  [`OQ-HT2`](../design/macos-user-home-tiers.md#decision-ledger)'s A′ mirror stands — and the fixture fails
  *unsandboxed*, because resolution happens in the VFS before the policy is consulted. An item is
  only worth a password when the sandbox could change the answer.

  **[`OQ-PS8`](../design/provisioner-sets.md#OQ-PS8) is new, opened by a measurement rather than a
  review:** codex's installer prompted `Start Codex now? [y/N]` on `/dev/tty` mid-`--version`-probe
  and a human answered `N` — which also closes a *"could not verify"* in
  [`native-installer-migration.md`](native-installer-migration.md). It honors
  `CODEX_NON_INTERACTIVE=1` and `packdecl.Install` has no field that can carry it. Leaning: detach
  the tty in core rather than add manifest vocabulary. Second-order and unfiled: `agy`'s installer
  appends a PATH export to four rc files, inverting the block/launch precedence B2 fixed on Linux
  until the next launch rewrites them.

- 🔒 **A `macos-user` launch does not forward the command it was given — a live defect, and 🔒
  rather than 🛑 only because every instrument for a fix is on that Mac.** 📄
  [`macos-user-provisioning.md` §1.1](../design/macos-user-provisioning.md#11-the-forwarded-command-is-not-passed-through-faithfully).
  Found while running the list above, and it invalidated that run's first attempt: `sudo --login`
  concatenates the command *"separated by spaces, after escaping each character (including white
  space) with a backslash — except for alphanumerics, underscores, hyphens, and dollar signs"*
  (sudo(8) `-i`). Measured, both halves: `bash -lc $'echo A\necho B'` → **`Aecho B`** (the newline
  became a `\`-continuation), and `bash -lc 'X=inner; echo got=$X'` → **`got=`** (the intermediate
  login shell expanded `$X` first). **Nothing errors** — the wrong command runs and exits 0, which
  is exactly why nine collapsed probes reported five successes.

  **Backend parity:** every container backend passes argv through `podman exec` untouched, so the
  same `yolo -- …` means different things per backend and only this one rewrites it. **The obvious
  fix is wrong** — `--login` is what makes the login-rc PATH re-prepend work, i.e.
  [`OQ-1`](runbooks/mac-go-port-verification.md#2-macos-user-backend--real-launch-oq-1-the-load-bearing-unknown)'s
  acceptance bar. The candidate recorded instead: the outer login shell's rc work is **already
  discarded** by the `env -i` that follows it, so `sudo` without `--login` plus an inner `zsh -l -c`
  may buy a faithful argv for nothing. Unverified, needs a plan-level test that fails on the
  current shape first, and needs the same one-password launch — **so it is a decision, not a
  ready-to-build.**

- 🔒 **On a Mac — three things need the hardware, and the first is a config rename.** *(The
  headline used to announce what had LEFT this row, which tells a reader nothing about what is in
  it. For the record: the two lib-farm assertions were never darwin assertions — they failed
  because the image build did, and both went green the moment `x86_64-darwin` could evaluate again.
  Nothing about the lib farm was wrong.)*

  Three items remain, all genuinely host-gated. **The pre-blocker this row used to name is GONE**
  — it said that Mac's config names its packs by Linux paths so no `yolo` launches there at all,
  and both halves closed: `c55b6571` taught `localPackAddress` to expand a plain `~/` pack path
  (`internal/config/packs.go`), the maintainer's config moved to that spelling, and a jail
  launched there on 2026-09-10. (The `agents`-key blocker the line named before that was closed
  on 2026-09-03.) What briefly replaced it was not a config fault at all but a **354-commit-stale
  host `yolo`**, which failed `yolo check` on `config.perf_logging: unknown key` — a key the tree
  knows and that binary predated; `just install` cleared it to 41 passed, 0 failed. Expect that
  shape on any Mac left alone for a week: the config moves with the tree, the host binary only
  when a human installs.
  The three: the `macos-user` acceptance matrix, Track D4's download proof, and the guest-notch
  handoff (whose [§2](handoff-guest-notch-macos.md#2-the-bug-that-was-fixed-blind--your-first-job-is-to-run-it) item 1.4 — do packs reach a macos-user sandbox? — is still the first thing to
  run there). 📄
  [`handoff-guest-notch-macos.md`](handoff-guest-notch-macos.md). **Item 1.4's remaining half is
  now answered too:** the sandbox could already read the staged pack root and run the toolchain,
  and the `sudo -u _yolojail` staging step above it executed on 2026-09-10 — the launch reached
  `yolo-jail macos-user bootstrap ok` and ran a command as that user.

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
  only because nobody has said they want it.** Both of its gates opened on 2026-09-02: nix [`OQ-NX1`](../design/provisioner-sets.md#decision-ledger)
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

**[OQ-LP14](../reference/loophole-system.md#oq-lp14) is settled too, and by the better of its two answers.** It became a hard dependency the
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
- **[CFP-1 … CFP-3](../design/composed-file-permissions.md)** and **[SS-6](../design/jail-state-separation-design.md#ss-6)**
  and `host-render-target.md`'s three [§9](../design/host-render-target.md#9-open-questions--the-discussion-part) questions — named 2026-08-23 so they are countable. All
  concern shipped mechanisms working as designed, not gaps.
  ⚠ **Those first two links point at `docs/design/`, not `docs/reference/`, and that is deliberate.**
  Both docs graduated on 2026-09-09 leaving a STUB at the original path holding only the live
  questions, so the basename now exists in both trees: the built system is in `docs/reference/`, the
  unanswered questions are here. A basename-driven link sweep has already got this wrong once and
  pointed a reference doc at itself. **Check which tree you mean before repointing either.**
- ⚠ **The retired 💬 4's nix questions are NO LONGER in this list, and this bullet used to say
  they were.** `9b9da960` merged `noncontainer-nix-environment.md` into
  [`provisioner-sets.md`](../design/provisioner-sets.md) and re-prefixed its bare numbering to `NX`,
  so every one of them is now inside 💬 **31**'s thirteen — which makes them **rows**, and this
  section is for questions that are not. Corrected 2026-09-12; the merge created the contradiction
  and nothing was tracking it. The claim that survives is the one worth keeping: their *narrow*
  halves — [`OQ-NX8`](../design/provisioner-sets.md#OQ-NX8)'s reporting half and
  [`OQ-NX9`](../design/provisioner-sets.md#OQ-NX9)'s Linux-diagnostics half — are work rather than
  rulings and block nothing, which is exactly why they are steps 2 and 3 of that doc's build order.
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

**And one body of work was unowned rather than undecided, found 2026-09-09.**
[`self-documenting-cli.md`](../reference/self-documenting-cli.md)'s P1 tail routes every generic CLI item
to *"generic CLI backlog (proposed: `docs/plans/self-documenting-cli.md`, not yet created)"* — and
that file was never created, so four items have had no owner for weeks. None needs a ruling. Named
here so they stop being invisible:

- ⚠ **The bar's own [§4](../reference/self-documenting-cli.md#4-top-level-help-lists-every-registered-command) is violated, and by more than the doc says.** *"Top-level help lists every
  registered command"* is a MUST, and **four registry commands are absent from `commandHelp`** —
  measured 2026-09-09 by diffing the two maps: `macos-teardown`, `macos-unshare`, `doctor` and
  **`host`**. The doc says "the three unlisted macos commands"; there are two of those, plus `doctor`
  and `host`. `host` is the one that matters — `yolo host -- <cmd>` / `yolo host apply` is the whole
  host-execution surface, shipped 2026-08-30, and it is unreachable from `yolo --help`.
- **The reverse-sync test exists in one direction only.** `TestUsageListedCommandsAreRegistered`
  catches a help line naming a command that does not exist; nothing catches a command that exists and
  is not in help — which is why the four above went unnoticed. That test is the fix, and it is what
  the doc's enforcement item 2 asks for.
- **≥1 copyable example per command** — only `run` and `pack` have one.
- **`--format json`** on `ps`, `check`, `loopholes list`, `prune`, `broker status` (`internal/cli/ps.go`
  has none today). This is the item an in-jail agent feels, and it is the doc's enforcement item 4.
- **The `config-ref` coverage test** (enforcement item 3). ⚠ **Its companion, action item 8, is
  partly MOOT**: `host_processes` and `prune` are documented, and `repo_path` is a RETIRED key
  (`internal/config/inherit.go`), so what is left of item 8 is the `host_services`→`loopholes`
  retitle — and `host_services` appears nowhere in `docs/reference/` any more either. Check before
  building it.

### Found by building, 2026-09-09 — work with no decision attached

The disk/image/CLI builds and the corpus restructure surfaced these. **None needs a ruling**; they
are named here so they are owned rather than rediscovered. Two that DO need a ruling are marked.

**Keys that validate something nothing consumes.** Both are the same shape and both are small:

- **`provides` on an `mcp_servers` entry.** Its only reader was `FilterMCPServersByCapabilities`,
  deleted 2026-09-09 as dead — so the key never suppressed a server. What survives is a
  *collision* check in `internal/config/validate.go`: two servers declaring one capability is a
  hard error. So a launch can be refused over an ambiguity in a value nothing resolves. Implement
  the selection or drop the key; today it is neither.
- **`required_capabilities`.** Accepted, validated, inherited by nested launches and forwarded as
  `YOLO_REQUIRED_CAPABILITIES` — and its design's FATAL refusal ([`OQ-CAP2`](../design/agent-auth-modes.md#11-open-questions))
  is **unbuilt**, so declaring a capability records a requirement and checks nothing. Now
  documented in `config-ref` including that caveat, which is the honest interim.

**Gaps a test would have caught, in the places tests do not reach:**

- **`integration/` has no `yolo prune` coverage**, and [OQ-LS3](../design/the-load-sentinel-is-not-a-liveness-oracle.md#111-decision-ledger)'s build made that matter: it added a
  non-zero exit that fires on a *healthy* machine which has not launched since the upgrade. The
  unit suite stubs the runtime, so it structurally cannot see that path.
- **Unknown flags are silently ignored, and by MORE commands than I first wrote.** Corrected
  2026-09-09: not just `run` and `init` — **`check` does it too and says so in `checkOptions`' own
  comment** (*"any stray flag is ignored"*), and `ps`/`prune` have no general refusal either
  (`prune` refuses only flags it used to have, by name). That is
  [`self-documenting-cli.md`](../reference/self-documenting-cli.md)'s requirement 3, whose other half shipped 2026-09-09:
  unknown `--format` VALUES now exit 2 with stdout empty. A typo'd flag on the commands everybody
  runs is the worst place left for silence, and the fix wants to be one parse helper rather than
  five per-command branches.
- **The `--format json` refusal on *acting* verbs is a per-verb branch, not a mechanism.** A new
  reporting verb gets the flag automatically; a new acting verb needs its guard added by hand.
  Handled rather than unrepresentable — the honest cost, stated so nobody assumes otherwise.

**One-line residuals the builds named rather than hid:**

- The [OQ-LS3](../design/the-load-sentinel-is-not-a-liveness-oracle.md#111-decision-ledger) pointer write sits just OUTSIDE `AutoLoadImage`'s existing
  housekeeping-lock bracket, leaving a few-ms window where a sweep sees an image with no pointer.
  Worst case is a failed `podman run`. The fix is to move the write inside that bracket; it was
  deliberately not taken to keep the diff out of `internal/image/`.
- **`yolo loopholes enable|disable` writes nothing** — the comment-preserving JSONC edit is
  unbuilt and was tracked nowhere.
- **`scripts/measure-macos-vm.sh` has no destination any more.** It exists to paste VM
  measurements into `macos-no-vm-direction.md`, and that doc is now a reference, which by genre
  carries no measurements. Wrong in kind, not just in path.
- **A fetched pack shipping a host daemon has never been spawned end to end.** An embedded pack
  carries yolo's own authority, so the fetched-and-executing path is unexercised. Fixture-shaped.

**Two that DO need your word**, and are small enough not to deserve rows of their own:

- 💬 **Does a pack's `supersedes` have a consumer, or is the capability namespace the wrong
  shape?** The mechanism is complete and **no pack in the tree declares `supersedes`** — the
  worked example its design used (a `claude-bedrock` pack) was never built, because Bedrock landed
  as a *profile* inside `packs/claude` instead. [`OQ-LP6`](../reference/loophole-system.md#oq-lp6) is the standing question.
- 💬 **How does an agent that needs an installed adapter extension get its MCP projection?**
  One shipped agent pack has none for that reason. The options are auto-install the adapter,
  detect-and-hint, or gate it behind a config key — plus a global-vs-project placement and a
  version-pinning question. Stripped out of [`mcp-configuration.md`](../reference/mcp-configuration.md#unbuilt) on graduation and
  named there.

Plus **[OQ-GN1](handoff-guest-notch-macos.md#9-open-questions) · [OQ-GN2](handoff-guest-notch-macos.md#9-open-questions) · [OQ-GN4](handoff-guest-notch-macos.md#9-open-questions)** in the guest-notch handoff, which are Mac-gated rather than
undecided, and are cited from 💬 7 above. *([OQ-GN3](handoff-guest-notch-macos.md#9-open-questions) left on 2026-09-02 — it asked whether the
Cachix cache had ever been pushed to, and the Actions log answered it: yes, and CI reads from it
too. Chasing it found the six CI `nix build` calls that were discarding the flake's substituter.)*

They are named and countable now, which is the point — a question with an ID can be promoted to a
row the day it starts blocking something, and demoted the day it stops. **That is the whole
difference between this list being 14 rows and being 104.**
