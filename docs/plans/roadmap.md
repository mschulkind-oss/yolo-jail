# Roadmap

**Status: 8 needing you · 1 ready · 0 in progress · 4 waiting · 1 broken · 2 icebox.**

Last updated **2026-08-18**. Counts tallied from this file, not asserted.

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

### 💬 1 — Trust paths: where we extend trust, and where a pin is theatre

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
  origin-gate finding stops being a broken guarantee. It stays in 🛑 until the fatal ships, but the
  fix is now defined rather than undecided.

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

### 💬 2 — Auth mode

📄 [`agent-auth-modes.md`](../design/agent-auth-modes.md)

**auth OQ-6** gates building `claude-bedrock` and is the only one with reach; the doc recommends
*fetched*. **auth OQ-1** resolves by experiment, not ruling (~5 minutes: does Claude Code send a
subscription OAuth bearer to a non-Anthropic base URL?) and it gates boundary-broker B2. **OQ-2 · 3 ·
4 · 9** are smaller. **OQ-7 is moot as phrased** — there is no Teams pack — and needs restating.

### 💬 3 — Non-container nix

📄 [`noncontainer-nix-environment.md`](../design/noncontainer-nix-environment.md)

**nix OQ-1** is the real content of what this file used to call "N3": is `host` a placement or a
backend? Everything else in that doc is subordinate to it. No longer urgent — the auth thread routed
around the `env` refusal that motivated it.

### 💬 4 — Boundary broker

📄 [`boundary-broker.md`](../design/boundary-broker.md)

**OQ-A** sizes B2 (if synchronous-only suffices, most of the durability complexity disappears).
**OQ-C** is a real API-shape decision: does the jail see the *result* or just success? **OQ-B1b**
sizes B1b only. The security half of **OQ-E** is settled; only its packaging half is live.

### 💬 5 — Image staging and baking

📄 [`image-staging-vs-baking.md`](../design/image-staging-vs-baking.md)

**OQ-5** blocks the largest measured reclaim (404 GiB of cached tars). **OQ-3** blocks a
content-addressed image tag; **OQ-1** blocks two more items; **OQ-4** is a scope ruling on a shipped
config key. None of these were in this file before today.

### 💬 6 — macOS, and the environment-manager stories

📄 [`macos-user-build-step-threat-model.md`](../design/macos-user-build-step-threat-model.md) ·
[`environment-manager-user-stories.md`](../design/environment-manager-user-stories.md) ·
[`macos-revival-and-distribution-plan.md`](macos-revival-and-distribution-plan.md)

**user-stories Q1** is called "the biggest question in the document" by its own author. **Q7** asks
whether Linux `guest` is a promise or a hypothesis. **threat-model Q1-Q3** cover the repo-root
refusal, `--accept-flake-config`'s substituter surface (now live — see the shipped item), and a macOS
build sandbox. **OQ-L1** explicitly blocks Track L part 2.

### 💬 7 — The small ones with no design-doc home

These were born in this file and have nowhere else to live: **S5** (a jail resolves a skill-name
collision silently), **OQ-CO**, **OQ-S4**, **OQ-E4**, and **E1/E2/E3/E5** from the backlog. Each is
one paragraph; none blocks anything.

*(**OQ-D1 has left this list.** It stopped being small when OQ-A13 made the config diff the
disclosure for enabling a host-reaching loophole, so it now has stakes, four options and a leaning in
📄 [`config-safety.md`](../design/config-safety.md) — and it is in the sprint below.)*

Two more of the same size, both from the `yolo check` honesty pass. (A third — what `yolo check`
should *print* for a section it skipped — now has a design-doc home as **OQ-3** in
[`broker-ca-and-nested-hosts.md`](../design/broker-ca-and-nested-hosts.md), because the same
`[PASS]`-on-a-skip is what hid a daemon that never started.)

- **`sectionRunningJails` has no in-jail guard.** Run from inside a jail it reports the *nested*
  podman's view — measured `[PASS] No jails currently running` in here while the host had one — and
  that line reads as a statement about the host. Left alone because it is *true of the runtime it can
  see*, and the orphan-cleanup path underneath acts on that same runtime: a behaviour question, not a
  label. `internal/cli/check/check.go:514`.
- **Should `yolo check` validate an npm selector's shape?** Now that a `package` string can carry a
  version, a typo like `foo@@1.2.3` reaches npm and fails at first use *inside* the jail, where the
  diagnosis is worst. Cheap host-side check; needs a ruling only on how strict to be.

### 💬 8 — `pack-host-management` OQ-B, and `pack-capabilities` OQ-CAP

📄 [`pack-host-management-plan.md`](pack-host-management-plan.md) ·
[`pack-capabilities.md`](../design/pack-capabilities.md)

Should host-side `files` be `0o444`? Same asymmetry as E1/E2 — decide them together. OQ-CAP is a
one-line deliverable that is decided in all but name.

---

# 📦 Up next

**Ordered by:** what unblocks something else, then what protects a live user, then cost.

- 📦 **Empty `bundled_loopholes/` — unblocked 2026-08-18, and it is the largest item here.** 📄
  [`pack-config-keys.md`](../design/pack-config-keys.md) ·
  [`loophole-activation.md`](../design/loophole-activation.md) ·
  [`broker-as-a-pack.md`](../design/broker-as-a-pack.md)

  OQ-K1..K4 are ruled, so the settings mechanism the three conversions all depend on is designed
  through. In dependency order:

  1. **Typed, manifest-declared loophole settings** (OQ-A8/K1..K4) — declarations are authoritative,
     a workspace may supply values *through the config-change gate*, and `host_processes.visible`
     is **frozen** (resolved once at launch, no per-request re-read).
  2. **`host-processes` and `audio` become packs; the broker's loophole moves into `packs/claude`**
     (OQ-A10). ⚠ Deleting `bundled_loopholes/claude-oauth-broker/` does **not** free the reserved
     name — the reservation and the `loopholesruntime.go` name special-case must die in the same
     commit, or every claude user's launch breaks.
  3. **`journal` and `cgroup-delegate` become manifest loopholes** (OQ-A6, OQ-K4), with
     `cgroup-delegate` default-off (OQ-A4). This deletes **both** loophole names core still hardcodes
     in its own config schema — the thing that makes the conversion mean something.

  ⚠ **Two user-visible breaks that need release notes when they ship:** `host_processes.visible`
  stops applying without a restart, and the top-level `journal` key stops being recognised (which
  needs a migration message, not silence). **Accepted cost:** `yolo-cglimit` stops working out of the
  box.

---

# 🛑 Broken

- 🛑 **The macOS nightly cannot build an image.** Five consecutive failures since v0.8.0.

  `TestImageSkewOracleAnswers` fails on `nix eval .#installPrefix failed`, and the two lib-farm tests
  fail because the build failed. Plausibly one root cause — nix is broken on that runner — and not in
  our tree, so a re-trigger reproduces it. Needs a Mac. 📄
  [`image-staging-vs-baking.md`](../design/image-staging-vs-baking.md) §7.

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

- 🔒 **On a Mac** — five items: the `macos-user` acceptance matrix, Track D4's download proof, the
  guest-notch handoff, and two lib-farm assertions that only fail on darwin. 📄
  [`handoff-guest-notch-macos.md`](handoff-guest-notch-macos.md).

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

### Emptying `bundled_loopholes/` — the sprint

The goal is **no inhabitants at sprint end** (OQ-BP4). `host-processes` steps 1–6 shipped 2026-08-17: the
connection preamble end to end, `ServeFrontedUnix`, the daemon behind the framework front, and
`yolo-ps` no longer self-reporting a `jail_id` nobody trusted.

**Five more steps shipped 2026-08-18** — the `default_enabled` rename (OQ-A9), `yolo check` reading
pack-shipped loopholes (OQ-A12), the enable-direction disclosure (OQ-A13), the approval snapshot
moving host-side (OQ-D1), and the non-interactive config fatal (OQ-D2).

**What is left is the part that actually empties the directory, and it is UNBLOCKED as of
2026-08-18** — OQ-K1..K4 are ruled, so the settings mechanism all three conversions depend on is
designed through. Queued in 📦 above:

- **`host-processes` and `audio` become packs; the broker's loophole moves into `packs/claude`**
  (OQ-A10). ⚠ Deleting `bundled_loopholes/claude-oauth-broker/` does **not** free the reserved name —
  the reservation and the `loopholesruntime.go` name special-case must die in the same commit, or
  every claude user's launch breaks.
- **`journal` and `cgroup-delegate` become manifest loopholes** (OQ-A6), with `cgroup-delegate`
  default-off (OQ-A4). **Accepted cost:** `yolo-cglimit` stops working out of the box.

Both conversions remove core's last hardcoded loophole names, which is what makes this mean something
rather than moving files around.

📄 [`broker-as-a-pack.md`](../design/broker-as-a-pack.md) — four of its six questions are answered;
**OQ-BP5** (build step vs download-only) and **OQ-BP6** (may a fetched pack ship a host-side binary?)
remain, and BP1 ruled they ship *in* this sprint.
