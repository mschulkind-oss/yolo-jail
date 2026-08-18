# Roadmap

**Status: 8 needing you · 3 ready · 0 in progress · 4 waiting · 2 broken · 2 icebox.**

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
- ✅ **OQ-TP1 obviated by TP6.** There is nothing to carry into a jail if no jail starts, so the
  origin-gate finding stops being a broken guarantee. It stays in 🛑 until the fatal ships, but the
  fix is now defined rather than undecided.

What is still open:

- **OQ-TP2** — does agent context (skills, briefings) get gated, or just **disclosed**? Today it is
  neither, by explicit classification, while `env` *is* disclosed on reasoning that applies verbatim.
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

- 📦 **Empty `bundled_loopholes/` — the activation sprint is now fully designed.** 📄
  [`loophole-activation.md`](../design/loophole-activation.md) ·
  [`broker-as-a-pack.md`](../design/broker-as-a-pack.md) ·
  [`pack-config-keys.md`](../design/pack-config-keys.md)

  Eleven of thirteen questions ruled; the two left do not gate it. What the sprint now carries, in
  dependency order:

  1. **`default_enabled` replaces `enabled`** (OQ-A9) — one key, renamed, governing all four manifest
     sources; `enabled` becomes recognized-and-refused; `SetEnabled` writes config, not a manifest.
     **Needs a refusal for reverse skew**, not a tolerance: an older yolo ignores the new key and runs
     `audio` on.
  2. **Typed, manifest-declared loophole settings** (OQ-A8) — the prerequisite for anything else
     moving out of core, since it is what lets `host_processes.visible` and `journal` stop being
     hardcoded top-level keys.
  3. **`host-processes` and `audio` become packs**; the broker's loophole becomes a contribution of
     `packs/claude` (OQ-A10). ⚠ The reserved name is **not** freed by deleting the bundled directory —
     the reservation and the `loopholesruntime.go` name special-case must die in the same commit, or
     every claude user's launch breaks.
  4. **`journal` and `cgroup-delegate` become manifest loopholes** (OQ-A6, in-sprint by your ruling),
     with `cgroup-delegate` default-off (OQ-A4). This is what removes core's last two hardcoded
     loophole names. **Accepted cost:** `yolo-cglimit` stops working out of the box.
  5. **`yolo check` learns to read pack-shipped loopholes** (OQ-A12) — same sprint, because the
     conversion moves the only two loopholes that have a `doctor_cmd`.
  6. **Mirror the enable-direction disclosure** (OQ-A13) — `WorkspaceDisabledLoopholes` already
     computes it and throws the `true` case away at `validate_loopholes.go:367`. **Ship it as
     readability, not as a control:** the snapshot it relies on is agent-writable.
  7. **Move the approval snapshot out of the jail's reach** (OQ-D1, ruled) — host-side per-workspace
     state the jail never mounts. This is what turns step 6 from a courtesy into a control. Costs
     nothing at runtime: the prompt already runs host-side and the jail never reads the file. 📄
     [`config-safety.md`](../design/config-safety.md)
  8. **A non-interactive launch with a changed config FAILS** (OQ-D2, ruled) — CI opts in with an
     explicit flag instead of the implicit yes it gets today (`config/snapshot.go:38`).
     ⚠ **Breaking change for existing non-interactive callers**, deliberately: a pipeline silently
     accepting config drift starts failing instead of continuing to not tell anyone. Needs a release
     note, and the failure message must name the flag — the reader of that message cannot be
     prompted. A flag rather than an env var, because this grants an *approval* rather than
     suppressing a diagnosis, and an env var is inherited by every child and outlives the launch.

  This is the largest queued item in the file and it grew on 2026-08-18: OQ-A6 pulled the two builtin
  conversions in rather than deferring them. That was your call and the reasoning is in the doc; the
  argument for deferring is kept there too, because a sprint that silently absorbs a fifth workstream
  is how the other four slip.

- 📦 **Two trust-path rulings: no evergreen npm, and no partial packs.** 📄
  [`trust-paths.md`](../design/trust-paths.md)

  Both close a finding by **removing a mechanism** rather than adding a gate, which is why they are
  cheap. Ordered by dependency:

  1. **A refused contribution refuses the launch (OQ-TP6).** Today the host computes a refusal,
     prints it, and stages the pack anyway; the jail then grants what was refused. Making it fatal
     deletes that whole class — no decision to carry, and the 🛑 origin-gate finding below closes with
     it. ⚠ **Behaviour change:** a selected-but-unapproved fetched pack now yields *no jail* where it
     used to yield a warning and a working one. The refusal message is therefore the entire user
     experience of the failure — it must name the pack, the unapproved claim, and the three choices
     (fix, remove, approve). **Do not** extend this to a missing bind-mount source or an unknown
     contribution kind; both are deliberate and a jail's boot depends on the second.
  2. **No evergreen npm (OQ-TP5).** Split `install` from `update`: install obeys the lockfile, update
     is the only resolver and writes it, and the hourly poll survives as an informational
     "an update is available". ⚠ **Blocked on OQ-TP4** for the case that matters — the lockfile is
     per *fetched* pack and the four packs declaring npm programs are all *embedded*, so there is
     nowhere to put the pin yet. The install/update split and the poll downgrade can land first.

- 📦 **Bake `openssl`, and make the broker's own failure detector speak.** *One package, plus the
  three layers that stayed quiet about it.* 📄
  [`broker-ca-and-nested-hosts.md`](../design/broker-ca-and-nested-hosts.md)

  The broker mints its CA by shelling out to `openssl`; the jail image bakes none. So on any launch
  where **the host is itself a jail**, the singleton dies at startup — **2,549 times in this jail,
  1.3 MB of log**, silently, for months. You have ruled: bake it.

  The packaging fix is one line. The reason it hid for months is worth the other three:

  1. `brokerWaitForSocket` **detects the dead child in milliseconds** and its caller discards the
     return value. Making that speak is what would have caught this on day one.
  2. The daemon's log has **no reader anywhere in the tree**.
  3. `yolo check` skips loophole checks in-jail and prints **`[PASS]`** — the third finding this
     month of the same shape, reporting on the wrong side of a boundary in the confident direction.

  **Three questions live in the doc** (OQ-1 retire vs. satisfy the `openssl` dependency, OQ-2 should a
  nested jail run its own broker at all, OQ-3 the honest token for "I did not look"). None blocks the
  bake.

---

# 🛑 Broken

- 🛑 **The origin gate is not enforced where the code runs.** *(Found 2026-08-17, verified twice.)*

  `internal/entrypoint/packsurfaces.go:89` loads **every** staged pack with `mayAccessHost=true`. The
  host computes the refusal, prints a warning, and stages the unmodified `pack.json` anyway — nothing
  carries the decision across the boundary. So a **fetched, unapproved** pack still gets its
  `curl → bash` launcher written. The test asserting the split says *"the JAIL loader trusts the
  staged tree"* and then bypasses the jail loader.

  This is the exact shape `gateAdmitsCrossing` exists to close: **true of the decision, false of its
  enforcement.**

  **The fix is now decided (OQ-TP6): the refusal becomes FATAL.** That deletes the problem rather
  than plumbing around it — with no jail starting, there is no decision to carry and the hardcoded
  `mayAccessHost=true` becomes correct for every input it can receive. It stays 🛑 until that ships,
  but it is queued work now, not an open question. 📄
  [`trust-paths.md`](../design/trust-paths.md) §3.1.

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

**Nothing is blocked any more.** OQ-A9 was the single decision standing between here and an empty
`bundled_loopholes/`, and it is ruled — one key, renamed. Step 7 (the official pack), the broker
conversion (OQ-A10: a contribution of `packs/claude`, not its own pack) and `audio` are all designed
through. The work is queued in 📦 above.

📄 [`broker-as-a-pack.md`](../design/broker-as-a-pack.md) — four of its six questions are answered;
**OQ-BP5** (build step vs download-only) and **OQ-BP6** (may a fetched pack ship a host-side binary?)
remain, and BP1 ruled they ship *in* this sprint.
