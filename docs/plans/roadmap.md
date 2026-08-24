# Roadmap

**Status: 14 needing you · 0 ready · 0 in progress · 6 waiting · 0 broken · 2 icebox.**

Last updated **2026-08-23**. Counts tallied from this file, not asserted — one per `### 💬` heading,
one per top-level bullet in every other section, and each bullet's glyph matches the section it is
in.

> [!IMPORTANT]
> **The build queue is EMPTY, and that is the headline.** Not "nothing to do" — everything that was
> designed has either shipped or is standing behind a ruling only you can make. The last 📦 item
> (the broker's move into `packs/claude`) shipped **2026-08-19**, and `bundled_loopholes/` is
> deleted. Until something in 💬 gets answered, the honest queue length is zero, so the fastest way
> to create work is to answer 💬 **1**, **2** or **6**.

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

**And the real number is now countable rather than estimated: 73 live questions across 18 docs**, as
of 2026-08-23, after a pass that gave every one of them a `💬` and a stable ID. It used to require
reading ~9,000 lines; it is now one command:

```console
$ rg -c '^(#{2,4} |\s*[0-9]+[a-z]?\. |\s*[-*] )💬' docs/ --sort path
```

**Fourteen rows below is what those 73 group into.** The gap between 73 and 14 is the point of this
file: a row is a *decision*, and one decision usually closes several questions.

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

### 💬 1 — Program delivery: eight questions, and the research corrected both of us

📄 [`program-delivery.md`](../design/program-delivery.md) — **OQ-PD1 … OQ-PD8**

New doc, written because you said this needed research before a decision. It supersedes the framing
of trust-paths' OQ-TP3/TP4 rather than answering them: the goal is **uniformity, not security** — the
security half was settled when OQ-TP5 killed silent npm updates.

**Four findings that change the question, each measured rather than argued:**

- **"Realization is per-workspace" is only half true, and the other half is worse.** npm programs,
  installer programs, LSP and MCP packages do land per-workspace. But **mise is machine-global and
  evergreen on every single launch**, so a launch in workspace B changes the toolchain workspace A
  resolves through — including a jail that is already running. **A per-workspace lockfile cannot
  reach it.**

  📄 **[Exactly what mise shares, and why the sharing is inverted](../design/program-delivery.md#421-exactly-what-mise-shares--and-the-sharing-is-inverted)**
  — you asked whether mise was only meant to share CAS-type caches. That is the right expectation and
  the reality is its **exact inverse**: `MISE_CACHE_DIR` is `/tmp/mise-cache`, **per-container and
  ephemeral**, while `MISE_DATA_DIR` is the machine-wide `/mise`. So the content-addressed part is
  thrown away and the mutable part is shared. Inside `installs/`, the versioned directories are
  effectively CAS and sharing them is the win; the **alias symlinks beside them are mutable
  pointers** that `mise upgrade --yes` repoints, and this repo's own `mise.toml` resolves through
  three of them (`node = "24"`, `go = "1.26"`, `just = "latest"`).
- **npm was never special — it was just the first kind anyone looked at.** mise already carries the
  Go module proxy and PyPI (via `pipx:`) alongside its core backends, all unpinned, all reached
  through a key yolo itself composes. That is your *"too special case for npm"* worry, confirmed.
- **"Pack set + lockfile makes jails uniform" is false today**, and this jail still proves it —
  **though on narrower evidence than this row used to claim.** *(Re-measured 2026-08-23: the config
  now legitimately selects claude, pi, codex, agy and opencode, so their presence proves nothing.
  The row previously cited them and a reader checking it would have found it wrong.)* What survives
  is cleaner: `@github/copilot` and npm `fzf 0.5.2` are installed with **no selecting pack and no
  launcher** — copilot was deselected, and `fzf` came from a test pack that no longer exists.
  **Dropping a pack removes its launcher and never uninstalls its program**, so a jail is the union
  of every pack ever selected, not the current set.
- **The launcher is PATH-shadowed after first use**, so the poll-and-report OQ-TP5 built is
  unreachable in steady state. The freeze is **total, not throttled** — and the resolve that decides
  everything is the cold one, per workspace.

**And the machine-global finding proved itself while being re-checked.** On 2026-08-20 mise
repointed `go/1.26` from `1.26.6` to `1.26.7` and **deleted `1.26.6` from disk** — so the exact
`installs/` path measured five days ago is not merely stale, it is **dangling**. That is the failure
mode the row describes, observed rather than argued, inside the window of one audit.

**The cheapest single win, if you want one before ruling:** mise supports a lockfile and yolo never
enables it. There is no mise lockfile anywhere in the tree. *(And OQ-PD8 has an answer waiting in
evidence: `claude.stamp` is still 2026-08-05 and `fzf.stamp` still 2026-08-02, while the launchers
were regenerated today. The stamp is touched on **every** poll, so an unmoved stamp is a poll that
never ran — the informational channel OQ-TP5 built has emitted nothing here in 18 days.)*

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

Still not urgent — the auth thread routed around the `env` refusal that motivated it, and the
codebase now points at this doc's own Option 2 from inside that refusal's text
(`internal/render/fieldset.go:83-103`).

### 💬 5 — Boundary broker

📄 [`boundary-broker.md`](../design/boundary-broker.md)

**OQ-A** sizes B2 (if synchronous-only suffices, most of the durability complexity disappears).
**OQ-C** is a real API-shape decision: does the jail see the *result* or just success? **OQ-B1b**
sizes B1b only. The security half of **OQ-E** is settled; only its packaging half is live.

### 💬 6 — Image staging and baking

📄 [`image-staging-vs-baking.md`](../design/image-staging-vs-baking.md)

**OQ-5** blocks the largest measured reclaim (404 GiB of cached tars). **OQ-3** blocks a
content-addressed image tag; **OQ-1** blocks two more items; **OQ-4** is a scope ruling on a shipped
config key. None of these were in this file before today.

### 💬 7 — macOS, and the environment-manager stories

📄 [`macos-user-build-step-threat-model.md`](../design/macos-user-build-step-threat-model.md) ·
[`environment-manager-user-stories.md`](../design/environment-manager-user-stories.md) ·
[`macos-revival-and-distribution-plan.md`](macos-revival-and-distribution-plan.md)

**user-stories Q1** is called "the biggest question in the document" by its own author — **and its
leaning is half-built**: it wants capture to become a *staging area* that `yolo config promote`
drains, and that subcommand **does not exist** (`internal/cli/config.go:33-60` lists `ls · render ·
diff · reset · capture · drift · dump`, verified 2026-08-23). So `apply --sealed` can already
*refuse* on an outstanding capture while the user's only remedy is still "discard it" — answering Q1
in the leaning's direction means building the verb, not just ruling. **Q7** asks whether Linux
`guest` is a promise or a hypothesis. **threat-model Q1-Q3** cover the repo-root
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
  collision **silently**, where `apply --host` refuses. Warn at launch, fail `yolo check`, or refuse
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
  hid a daemon that never started. **Ten sections already step aside this way** (measured
  2026-08-23; the doc previously said four, and the undercount was in the leaning's favour).
- **`sectionRunningJails` has no in-jail guard** (`check.go:514`). From inside a jail it reports the
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
- **A fourth PATH exists that claims to match the third and does not** *(found 2026-08-23, while
  correcting AGENTS.md's own PATH line, which was also wrong).* `BootPath`
  (`internal/entrypoint/boot.go:356-361`) is the authority and includes `~/.local/bin`. The PATH set
  for the **mise-trust subprocess** (`boot.go:520-523`) omits it, under a comment saying it *"matches
  the pre-exec PATH set in `main()`"*. Nothing is known to break — `~/.local/bin` holds the native
  agent installs and the chrome-devtools wrapper, none of which that subprocess calls — so this is a
  **drift question, not a defect report**: is the fourth list meant to be a narrower PATH on purpose,
  or is it the third list that has since grown? The cheap fix is to derive it from `BootPath` so the
  question cannot recur; the honest one is to find out which of the two was right.

### 💬 12 — `pack-host-management` OQ-B, and `pack-capabilities` OQ-CAP

📄 [`pack-host-management-plan.md`](pack-host-management-plan.md) ·
[`pack-capabilities.md`](../design/pack-capabilities.md)

Should host-side `files` be `0o444`? Same asymmetry as E1/E2 — decide them together. OQ-CAP is a
one-line deliverable that is decided in all but name.

### 💬 13 — Nested nixpkgs attribute paths in `packages`

📄 [`package-nested-attribute-paths.md`](../design/package-nested-attribute-paths.md) — **OQ-1**

This sat in 📦 as *"designed, questions answered, no blockers"* and it is none of those. Its doc is
`**Status:** DESIGN SKETCH, 2026-08-22. Nothing built.`, and **OQ-1** — how a dotted path resolves
when a derivation output and a nested collection member claim the same name — is carrying a leaning
and an empty **Answer:** block. That is the resolver's central rule, so it gates the whole item
rather than one corner of it.

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

# 📦 Up next

**Empty, as of 2026-08-23** — and it emptied by shipping, not by demotion.

The last item was the broker's move out of `bundled_loopholes/`, and it **shipped in full on
2026-08-19**: the directory, its `embed.go`, `internal/loopholes/embedfallback.go` and the whole
`BundledLoopholesDir` / `SourceBundled` / `IncludeBundled` vocabulary are deleted; the manifest lives
at `packs/claude/loopholes/claude-oauth-broker/` as a *contribution of the claude pack*, not a pack
of its own; `loopholes.ReservedLoopholeNames` is gone whole, because the broker was the last name in
it. 📄 [`broker-as-a-pack.md`](../design/broker-as-a-pack.md) §13 records what emptying the channel
actually required.

**What refills this section:** an answer in 💬. Rows **1**, **2** and **6** each unblock buildable
work the day they are ruled on — program delivery has a ten-step build order waiting behind it
(`program-delivery.md` §10), trust-paths has TP4's pin and TP7's catch-up, and image-staging has the
largest measured reclaim in the repo. **Nothing here is waiting on me to decide what to do next.**

> [!NOTE]
> **An empty queue is not a stable state, it is a reading.** If it stays empty for a week, the
> question to ask is not "what should we build" but "which of the eleven rulings is actually
> blocking, and which is a question I invented" — see
> [`further-roadmap-ideas.md`](further-roadmap-ideas.md) §4, which argues two of them are the
> latter.

---

### Release notes now have a home, and they are caught up

Three shipped behaviour changes had nowhere to be announced — a design ruling discharged its residual
risk as *"a release note"* and no CHANGELOG, NEWS or release-notes file existed anywhere in the repo
(found 2026-08-18 while verifying the `default_enabled` rename).

📄 [`RELEASE-NOTES.md`](../RELEASE-NOTES.md) now carries eighteen entries under `## Unreleased`,
including the original three (**`audio` is now off by default**, **an unreachable host service
refuses the launch**, **a non-interactive launch stops auto-accepting config changes**) and the two
that were still queued when this section was written: **npm-installed agent CLIs no longer update
themselves** (OQ-TP5) and **a pack whose claims you never approved now refuses the launch**
(OQ-TP6). The broker's move ships with its own entry plus an upgrade warning — *restart the broker
singleton after upgrading, or every OAuth refresh on that host fails.* **Newest, 2026-08-23:**
`workspace_readonly` was a silent no-op on `macos-user` and now enforces through Seatbelt, so anyone
who set it there and has been writing to the workspace will start seeing failures.

---

# 🔒 Waiting

- 🔒 **The macOS nightly is GREEN again, and the warmup still burns its whole ceiling warming
  nothing.** Measured on run `32623453131` (2026-08-23, commit `ae0fa1a5` — current `main`):
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
  (`integration/harness_test.go:147-153`). On linux CI the premise holds and the warmup keeps
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
  - ✅ **One real profile bug found and fixed** (`533ccc1`): intermediate workspace dirs were denied,
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

- 🔒 **On a Mac** — **the two lib-farm assertions have left this row.** They were never darwin
  assertions: they failed because the image build did, and both went green the moment
  `x86_64-darwin` could evaluate again (see the nightly row above). Nothing about the lib farm was
  wrong.

  Three items remain, all genuinely host-gated: the `macos-user` acceptance matrix, Track D4's
  download proof, and the guest-notch handoff (whose §2 item 1.4 — do packs reach a macos-user
  sandbox? — is still the first thing to run there). 📄
  [`handoff-guest-notch-macos.md`](handoff-guest-notch-macos.md). **Item 1.4 is now half-answered:**
  the sandbox can read the staged pack root and run the toolchain, so what is untested is the
  `sudo -u _yolojail` staging step above it, not the confinement.

  **What a Mac session on 2026-08-19 did settle, beyond the nightly:** `go test -short ./...` had
  **two** failures no Linux run could see, both now fixed — the GNU-`stat` throttle above
  (`c411650`, a real `macos-user` defect) and a `yolo ps` runtime-default assertion that hardcoded
  Linux's answer (`a35f8c7`, test-only; the resolver was right). `ci.yml`'s `check-macos` job was red
  on `main` for the second of those.

  **And a third, which was neither macOS-specific nor a flake** (`8e77580`): `TestNoTruncationRace`
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

  **And a fourth, from the sandbox measurement above** (`533ccc1`): the Seatbelt profile granted
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
- 🧊 **Boundary broker B2** (approval-gated host credentials) — waits on nix OQ-1 and auth OQ-1, and
  the second resolves by an experiment nobody has run.

---

# Open threads

### Emptying `bundled_loopholes/` — **done 2026-08-19**, and what it left behind

The goal was **no inhabitants at sprint end** (OQ-BP4), and the channel is gone rather than emptied:
the directory, its `embed.go`, `internal/loopholes/embedfallback.go` and every reader of them are
deleted. All six loopholes are pack contributions now, `loopholes.ReservedLoopholeNames` and
`paths.BuiltinLoopholeNames` are deleted whole, and **core's config schema names no loophole at
all** — which was the point of the exercise rather than a side effect.

**OQ-LP14 is settled too, and by the better of its two answers.** It became a hard dependency the
moment the goal grew from one loophole to the whole channel, and it closed on 2026-08-18 by
**withdrawing the bind-host path rule rather than adding vocabulary for a runtime-dir socket** — a
rule that admitted `~/.ssh` while refusing `${XDG_RUNTIME_DIR}/pulse/native` in every spelling. The
two audio loopholes then merged into `packs/audio` under the plain name.

One residue remains, tracked above rather than here: the **binary capability** OQ-BP1 promised
alongside the move (💬 11).

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

They are named and countable now, which is the point — a question with an ID can be promoted to a
row the day it starts blocking something. **That is the whole difference between this list being 14
rows and being a hundred.**
