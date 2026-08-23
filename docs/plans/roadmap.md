# Roadmap

**Status: 10 needing you · 1 ready · 0 in progress · 6 waiting · 0 broken · 2 icebox.**

Last updated **2026-08-23**. Counts tallied from this file, not asserted — one per `### 💬` heading,
one per top-level bullet in every other section, and each bullet's glyph now matches the section it
is in. (It did not before: a 📦 bullet was living in 🔒 Waiting and the old line counted it in
neither, which is how "4 waiting" was one short of the five bullets actually there.)

**What this is.** The forward plan and nothing else. **If it is in this file, it is not done.** Work
that ships leaves immediately — the record is the commit history. Decisions *not* to build move to
[`retired-decisions.md`](../design/retired-decisions.md), because a rejected design with no record
gets re-proposed.

**How questions are held.** Open questions live in their **design doc**, with stakes and a leaning,
and this file links to them by ID. It never restates them — that is how the count drifted to 19 while
the real number was closer to 50.

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
- **"Pack set + lockfile makes jails uniform" is false today**, and this jail proves it: its user
  config is `"packs": ["claude"]`, yet `~/.npm-global/lib/node_modules/` holds pi, copilot, codex and
  a stray `fzf` from a deleted test pack. **Dropping a pack removes its launcher and never uninstalls
  its program**, so a jail is the union of every pack ever selected, not the current set.
- **The launcher is PATH-shadowed after first use**, so the poll-and-report OQ-TP5 built is
  unreachable in steady state. The freeze is **total, not throttled** — and the resolve that decides
  everything is the cold one, per workspace.

**The cheapest single win, if you want one before ruling:** mise supports a lockfile and yolo never
enables it. There is no mise lockfile anywhere in the tree.

### 💬 2 — Trust paths: where we extend trust, and where a pin is theatre

📄 [`trust-paths.md`](../design/trust-paths.md) — 25 paths enumerated from the code · partly supersedes
[`pack-execution-trust.md`](../design/pack-execution-trust.md)

**Two rulings on 2026-08-18, both closing a finding by removing a mechanism rather than adding a
gate** — and one of them obviated a question rather than answering it:

- ✅ **OQ-TP5 — no evergreen npm.** `install` obeys the lockfile, `update` is the only act that
  resolves a new version, and the hourly poll is downgraded to informational. *Queued in 📦 below.*
- ✅ **OQ-TP6 — a refused contribution refuses the launch.** No partial packs: fix it, remove it, or
  approve it. *Queued in 📦 below.*
- ✅ **OQ-TP2 — nothing explicit.** Agent context needs no gate and no separate disclosure: the
  lockfile's commit pin closes over the whole tree, prose included. *Inherits OQ-LP8/G2b — the pin is
  recorded and never consulted at launch, so it covers this on paper until enforcement lands.*
- ✅ **OQ-TP1 obviated by TP6.** There is nothing to carry into a jail if no jail starts, so the
  origin-gate finding stops being a broken guarantee. It remains an unenforced one until the fatal
  ships, but the fix is now defined rather than undecided.

What is still open:

- **OQ-TP3** — is pinning worth building at all? **Partly answered:** TP5 settles row 1's behaviour.
  What is left is scope — whether yolo pins its *own* embedded packs, and whether a fetched pack is
  *required* to pin rather than merely permitted.
- **OQ-TP4** *(new)* — **where does an embedded pack's npm version get pinned?** The lockfile is per
  *fetched* pack, but pi, copilot, codex and opencode are all **embedded** — so TP5's ruling has no
  home for the pin in the case that covers nearly every user. This gates implementing TP5.
- **OQ-X1** — does a digest-pinned installer script count, given its own fetches are not pinned?
- **OQ-LP8 / G2b** — you ruled the shape (approval pinned to a commit); what remains is that
  `LockEntry.Commit` is **never consulted at launch**, so the pin does not yet exist.

### 💬 3 — Auth mode

📄 [`agent-auth-modes.md`](../design/agent-auth-modes.md)

**auth OQ-6** gates building `claude-bedrock` and is the only one with reach; the doc recommends
*fetched*. **auth OQ-1** resolves by experiment, not ruling (~5 minutes: does Claude Code send a
subscription OAuth bearer to a non-Anthropic base URL?) and it gates boundary-broker B2. **OQ-2 · 3 ·
4 · 9** are smaller. **OQ-7 is moot as phrased** — there is no Teams pack — and needs restating.

### 💬 4 — Non-container nix

📄 [`noncontainer-nix-environment.md`](../design/noncontainer-nix-environment.md)

**nix OQ-1** is the real content of what this file used to call "N3": is `host` a placement or a
backend? Everything else in that doc is subordinate to it. No longer urgent — the auth thread routed
around the `env` refusal that motivated it.

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

**user-stories Q1** is called "the biggest question in the document" by its own author. **Q7** asks
whether Linux `guest` is a promise or a hypothesis. **threat-model Q1-Q3** cover the repo-root
refusal, `--accept-flake-config`'s substituter surface (now live — see the shipped item), and a macOS
build sandbox. **OQ-L1** explicitly blocks Track L part 2.

### 💬 8 — The small ones with no design-doc home

These were born in this file and have nowhere else to live: **S5** (a jail resolves a skill-name
collision silently), **OQ-CO**, **OQ-S4**, **OQ-E4**, and **E1/E2/E3/E5** from the backlog. Each is
one paragraph; none blocks anything.

*(**OQ-D1 has left this list.** It stopped being small when OQ-A13 made the config diff the
disclosure for enabling a host-reaching loophole, so it now has stakes, four options and a leaning in
📄 [`config-safety.md`](../design/config-safety.md) — and it is in the sprint below.)*

**And one that arrived here by losing its parent.** The 🛑 nightly entry carried it and cited
[`image-staging-vs-baking.md`](../design/image-staging-vs-baking.md) §7 — but that section is the
silent-fallback defect and **never mentions darwin**, so the citation was wrong and this genuinely
has no doc home. It is the only item here with a deadline attached, so it should get one before the
deadline decides it for us.

- **26.05 is the LAST nixpkgs supporting `x86_64-darwin`**, security-fixed only to end of 2026. The
  nightly needs `macos-26-intel` because GitHub's Apple Silicon runners cannot nest a VM for Podman
  Machine, so when 26.05 lapses the choice is a self-hosted arm64 Mac runner or macos-user-only
  macOS tests. **Needs you, but not yet** — a deadline rather than a bug. The nearest measured
  evidence is 📄
  [`noncontainer-nix-environment.md`](../design/noncontainer-nix-environment.md) §5.1, where every
  probe throws `Nixpkgs 26.11 has dropped support for x86_64-darwin`: the drop has already happened
  upstream, so `927fb9f`'s `nixpkgs-26.05-darwin` is a **pin on a dead branch**, not a supported
  line, and the clock is the security-fix window rather than a release date.

Two more of the same size, both from the `yolo check` honesty pass. (A third — what `yolo check`
should *print* for a section it skipped — now has a design-doc home as **OQ-3** in
[`broker-ca-and-nested-hosts.md`](../design/broker-ca-and-nested-hosts.md), because the same
`[PASS]`-on-a-skip is what hid a daemon that never started.)

- **`sectionRunningJails` has no in-jail guard.** Run from inside a jail it reports the *nested*
  podman's view — measured `[PASS] No jails currently running` in here while the host had one — and
  that line reads as a statement about the host. Left alone because it is *true of the runtime it can
  see*, and the orphan-cleanup path underneath acts on that same runtime: a behaviour question, not a
  label. `internal/cli/check/check.go:514`.
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

### 💬 9 — `pack-host-management` OQ-B, and `pack-capabilities` OQ-CAP

📄 [`pack-host-management-plan.md`](pack-host-management-plan.md) ·
[`pack-capabilities.md`](../design/pack-capabilities.md)

Should host-side `files` be `0o444`? Same asymmetry as E1/E2 — decide them together. OQ-CAP is a
one-line deliverable that is decided in all but name.

### 💬 10 — Nested nixpkgs attribute paths in `packages`

📄 [`package-nested-attribute-paths.md`](../design/package-nested-attribute-paths.md) — **OQ-1**

This sat in 📦 as *"designed, questions answered, no blockers"* and it is none of those. Its doc is
`**Status:** DESIGN SKETCH, 2026-08-22. Nothing built.`, and **OQ-1** — how a dotted path resolves
when a derivation output and a nested collection member claim the same name — is carrying a leaning
and an empty **Answer:** block. That is the resolver's central rule, so it gates the whole item
rather than one corner of it.

---

# 📦 Up next

**Ordered by:** what unblocks something else, then what protects a live user, then cost.

- 📦 **One inhabitant left in `bundled_loopholes/`: the broker.** *Needs no ruling — it needs three
  steps in one order.* 📄 [`broker-as-a-pack.md`](../design/broker-as-a-pack.md) §6.1, §10

  **Shipped 2026-08-18:** typed manifest-declared settings (OQ-A8/K1..K4), `host-processes` and
  `audio` converted, `journal` and `cgroup-delegate` converted with the delegate now opt-in (OQ-A6,
  OQ-A4), **core's config schema stops naming any loophole**, and the broker singleton stopped
  spawning on every launch for every user (OQ-A11).

  **Why the broker did not follow, measured rather than judged.** A pack-shipped loophole must
  declare `publishes: "socket"` (`packPublishesProblems` refuses every other value, including the
  default), and yolo answers that value by spawning a daemon at a **per-jail** socket
  (`loopholesruntime.go:564`). The broker is a **host-wide singleton** — that is its entire reason to
  exist. So the move needs one daemon behind N per-jail fronts, which is §10 steps 3 and 4 turning
  out to be a hard *prerequisite* for step 5 rather than merely earlier than it.

  Order, and it is not negotiable:

  1. the connection preamble and stamp work (§10 step 3);
  2. the `publishes` flip **plus** the relay deletion — *"must not be split: a half-flipped broker is
     a jail with no credential path"*;
  3. then the contribution moves into `packs/claude`.

  ⚠ **And the reservation is still the trap, differently from the two that just shipped.**
  `host-processes` and `audio` were reserved only as bundled *directory* names, so `git mv` retired
  their reservations for free. `broker.BrokerLoopholeName` is appended **unconditionally** from the
  broker's own constant. A reader generalising from the two easy ones ships a commit that refuses
  **every claude user's launch**. The reservation, the `startLoopholes` name special-case and the
  contribution land in ONE commit.

---

### Release notes now have a home

Three shipped behaviour changes had nowhere to be announced — a design ruling discharged its residual
risk as *"a release note"* and no CHANGELOG, NEWS or release-notes file existed anywhere in the repo
(found 2026-08-18 while verifying the `default_enabled` rename).

📄 [`RELEASE-NOTES.md`](../RELEASE-NOTES.md) carries the three: **`audio` is now off by default**
(with its un-fixable downgrade hazard), **an unreachable host service refuses the launch** (a dead
broker singleton refuses every jail on that host), and **a non-interactive launch stops auto-accepting
config changes** (CI needs `--accept-config-changes`).

The two rulings still queued in 📦 — no evergreen npm, and a refused contribution refusing the
launch — get their entries when they ship, not before.

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

  **What is NOT fixed, and it is why this is a row rather than a deletion.** The suite's warmup was
  killed at **exactly its 12m0s darwin ceiling**, having warmed nothing — the same shape as the
  **20m7s** kill the night before. The same commit set that ceiling (5 min linux / 12 min darwin),
  so it bounded the WASTE and did not cure the cause: the nightly still spends twelve minutes a
  night on an optimisation whose entire purpose is to save time. The log shows those minutes going
  into a full nix build and image load (`Image load needed: first run (no images loaded into podman
  yet)`, in a job whose previous step already `podman load`ed one) rather than the container start
  the warmup exists to pre-pay — but **where they actually go is still unconfirmed.** That is the
  old entry's demand, inherited rather than discharged, and it is the next thing to measure. Needs
  the nightly or an Intel Mac; nobody here has one.

  *(Why the predecessor entry took 29 nights to be worth writing down, and why the paragraph above
  refuses to guess: the recorded diagnosis — "nix is broken on that runner and not in our tree" —
  was exactly backwards and had never been measured. It was our flake, on every Intel Mac,
  reproducible in 0.2s with `nix eval .#installPrefix`. Four nights were spent re-triggering a run
  that could not have passed. The warmup ceiling is the same question asked again: do not move that
  number until somebody measures what it is bounding.)*

- 🔒 **The fatal witness is live in the tree, and not on your host until a `just load`.**

  Since 2026-08-18 an enabled jail-facing service the jail cannot use **refuses the launch**, in all
  three fault classes. Your jails keep the old warn behaviour until the image is reloaded, so this is
  the moment to know what changes:

  - **A dead broker singleton refuses every jail on the host**, not just one — its endpoint variable
    is wired with no publish gate, which is deliberate and was accepted. This is the release-note
    line.
  - `YOLO_ALLOW_UNREACHABLE_SERVICES=1 yolo …` is the way past any refusal, and the refusal names it.
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

- 🧊 **Cache relocation's two held questions** — marked HELD in their own doc; genuinely undecided
  whether we want the feature, not merely unscheduled. 📄 [`cache-relocation.md`](cache-relocation.md).
- 🧊 **Boundary broker B2** (approval-gated host credentials) — waits on nix OQ-1 and auth OQ-1, and
  the second resolves by an experiment nobody has run.

---

# Open threads

### Emptying `bundled_loopholes/` — one inhabitant left

The goal is **no inhabitants at sprint end** (OQ-BP4). As of 2026-08-18 the directory holds exactly
one: `claude-oauth-broker`.

`host-processes` and `audio` are packs; `journal` and `cgroup-delegate` are manifest loopholes and
the "builtin service" channel no longer exists; core's own config schema names no loophole at all,
which was the point of the exercise rather than a side effect. The broker's remaining move is queued
in 📦 above with the order it has to happen in.

📄 [`broker-as-a-pack.md`](../design/broker-as-a-pack.md) — four of its six questions are answered;
**OQ-BP5** (build step vs download-only) and **OQ-BP6** (may a fetched pack ship a host-side binary?)
remain, and BP1 ruled they ship *in* this sprint.
