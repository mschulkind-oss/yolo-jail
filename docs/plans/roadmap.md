# Roadmap

**Status: 10 needing you · 2 ready · 0 in progress · 4 waiting · 2 broken · 2 icebox.**

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

### 💬 1 — Before the reachability flip: which failures may refuse a launch?

📄 [`loopback-tls-reachability.md`](../design/loopback-tls-reachability.md) — **OQ-R4 · OQ-R5**

Both raised *by building* OQ-R2's prerequisites, and both gate the one item in 📦 below. OQ-R2 ruled
that an unreachable service fails the launch; neither question re-opens that. They draw the two
boundaries it left implicit — **which fault classes** count (only the dial failing, or a stale token
and a missing endpoint too), and whether a **nested jail** may fail this way at all. Today's answers
are the conservative ones, arrived at by default rather than by decision, and I lean toward keeping
both. Cheap to answer, and worth answering before a flip makes them load-bearing.

### 💬 2 — Loophole activation: eleven questions, one design

📄 [`loophole-activation.md`](../design/loophole-activation.md)

Six rulings are already recorded (presence never activates; packs declare a default that defaults to
**disabled**; `requires.command_on_path` is deleted; host access is never on by default; install is
user-scope and enable is either; the broker moves inside `packs/claude`). What is left:

- **OQ-A9 is the one real gap** — `default_enabled` collides with a live `enabled` key that all four
  shipped manifests set and `SetEnabled` writes back. Either reading breaks something, and **it
  blocks two other items** (host-processes step 7, and OQ-LP10's retirement).
- **OQ-A7** — does a loophole-only pack need selecting, or is enabling enough?
- **OQ-A10** — the broker's loophole: inside `packs/claude`, or its own pack? Two docs currently
  disagree, and the reserved name does **not** free itself when the bundled copy is deleted.
- **OQ-A11** — the broker daemon and relay spawn on **every launch with no lookup at all**, so R1 has
  a counterexample in the run pipeline. Also covers the ungated host nix-daemon socket.
- ✅ **OQ-A8 is designed** — 📄 [`pack-config-keys.md`](../design/pack-config-keys.md): a loophole's
  settings are **typed and declared in its manifest**, supplied under `loopholes.<name>.settings`,
  and delivered through a file core writes. Four questions live there (**OQ-K1..K4**), and one of them
  matters beyond this: `journal: "full"` is an **agent-settable host-journal passthrough with no scope
  rule at all** today.
- **OQ-A4 · A5 · A6 · A12 · A13** — cgroup-delegate's opt-in, the three gates on `yolo-ps`, whether
  the builtins become manifests, `yolo check`'s blindness to pack loopholes, and disclosure now that
  *enabling* is the dangerous direction.
- ✅ **A1 · A2 · A3 · A7 answered.** Going dark needs no migration machinery, and a loophole-only pack
  is selected like any other — no special case.

### 💬 3 — Trust paths: where we extend trust, and where a pin is theatre

📄 [`trust-paths.md`](../design/trust-paths.md) — 25 paths enumerated from the code · partly supersedes
[`pack-execution-trust.md`](../design/pack-execution-trust.md)

- **OQ-T1** — the origin gate is **not enforced in the jail** (see 🛑 below). Fix now, or design change?
- **OQ-T2** — does agent context (skills, briefings) get gated, or just **disclosed**? Today it is
  neither, by explicit classification, while `env` *is* disclosed on reasoning that applies verbatim.
- **OQ-T3** — is pinning worth building at all? It changes an outcome in **three of twenty-five** paths.
  *Its top row is now attemptable:* a pack's `package` string could not express a version at all until
  today (the launcher appended a literal `@latest`, so `foo@1.2.3` became `foo@1.2.3@latest`). That is
  fixed as a bug — nothing is pinned by default and no shipped pack changed what it installs — so the
  question is now a live policy choice rather than a blocked one.
- **OQ-X1** — does a digest-pinned installer script count, given its own fetches are not pinned?
- **OQ-LP8 / G2b** — you ruled the shape (approval pinned to a commit); what remains is that
  `LockEntry.Commit` is **never consulted at launch**, so the pin does not yet exist.

### 💬 4 — Auth mode

📄 [`agent-auth-modes.md`](../design/agent-auth-modes.md)

**auth OQ-6** gates building `claude-bedrock` and is the only one with reach; the doc recommends
*fetched*. **auth OQ-1** resolves by experiment, not ruling (~5 minutes: does Claude Code send a
subscription OAuth bearer to a non-Anthropic base URL?) and it gates boundary-broker B2. **OQ-2 · 3 ·
4 · 9** are smaller. **OQ-7 is moot as phrased** — there is no Teams pack — and needs restating.

### 💬 5 — Non-container nix

📄 [`noncontainer-nix-environment.md`](../design/noncontainer-nix-environment.md)

**nix OQ-1** is the real content of what this file used to call "N3": is `host` a placement or a
backend? Everything else in that doc is subordinate to it. No longer urgent — the auth thread routed
around the `env` refusal that motivated it.

### 💬 6 — Boundary broker

📄 [`boundary-broker.md`](../design/boundary-broker.md)

**OQ-A** sizes B2 (if synchronous-only suffices, most of the durability complexity disappears).
**OQ-C** is a real API-shape decision: does the jail see the *result* or just success? **OQ-B1b**
sizes B1b only. The security half of **OQ-E** is settled; only its packaging half is live.

### 💬 7 — Image staging and baking

📄 [`image-staging-vs-baking.md`](../design/image-staging-vs-baking.md)

**OQ-5** blocks the largest measured reclaim (404 GiB of cached tars). **OQ-3** blocks a
content-addressed image tag; **OQ-1** blocks two more items; **OQ-4** is a scope ruling on a shipped
config key. None of these were in this file before today.

### 💬 8 — macOS, and the environment-manager stories

📄 [`macos-user-build-step-threat-model.md`](../design/macos-user-build-step-threat-model.md) ·
[`environment-manager-user-stories.md`](../design/environment-manager-user-stories.md) ·
[`macos-revival-and-distribution-plan.md`](macos-revival-and-distribution-plan.md)

**user-stories Q1** is called "the biggest question in the document" by its own author. **Q7** asks
whether Linux `guest` is a promise or a hypothesis. **threat-model Q1-Q3** cover the repo-root
refusal, `--accept-flake-config`'s substituter surface (now live — see the shipped item), and a macOS
build sandbox. **OQ-L1** explicitly blocks Track L part 2.

### 💬 9 — The small ones with no design-doc home

These were born in this file and have nowhere else to live: **S5** (a jail resolves a skill-name
collision silently), **OQ-D1** (the config-approval snapshot is agent-writable — and see 🛑 below,
where the sweep found the gate also fails open three other ways), **OQ-CO**, **OQ-S4**, **OQ-E4**, and
**E1/E2/E3/E5** from the backlog. Each is one paragraph; none blocks anything.

Two more of the same size, both from the `yolo check` honesty pass:

- **`sectionRunningJails` has no in-jail guard.** Run from inside a jail it reports the *nested*
  podman's view — measured `[PASS] No jails currently running` in here while the host had one — and
  that line reads as a statement about the host. Left alone because it is *true of the runtime it can
  see*, and the orphan-cleanup path underneath acts on that same runtime: a behaviour question, not a
  label. `internal/cli/check/check.go:514`.
- **Should `yolo check` validate an npm selector's shape?** Now that a `package` string can carry a
  version, a typo like `foo@@1.2.3` reaches npm and fails at first use *inside* the jail, where the
  diagnosis is worst. Cheap host-side check; needs a ruling only on how strict to be.

### 💬 10 — `pack-host-management` OQ-B, and `pack-capabilities` OQ-CAP

📄 [`pack-host-management-plan.md`](pack-host-management-plan.md) ·
[`pack-capabilities.md`](../design/pack-capabilities.md)

Should host-side `files` be `0o444`? Same asymmetry as E1/E2 — decide them together. OQ-CAP is a
one-line deliverable that is decided in all but name.

---

# 📦 Up next

**Ordered by:** what unblocks something else, then what protects a live user, then cost.

- 📦 **Close the blocking-open hazard in `svcendpoint.Read` itself.** *A real defect, found by
  mutation, fixed only at the boot path.*

  `svcendpoint.Read` is `os.ReadFile`, and **`os.ReadFile` opens the path**. Opening a writer-less
  fifo blocks forever, and there is no ceiling on the read. At the boot probe that meant **PID 1
  wedged above `genFailuresError` with nothing printed** — a jail that never starts and never says
  why. That call site is now guarded (stat first, refuse anything not a regular file, cap the size)
  and the regression test fails by deadline against the old code.

  **Every other caller still has it**, including the in-jail OAuth terminator reading the same
  read-write-mounted directory: the symptom there is Claude Code never starting, with no error. The
  fix is a stat gate or `O_NONBLOCK` inside `Read`, which changes error semantics for every caller —
  which is why the verification pass scoped itself to the boot path and wrote the limitation down
  rather than landing it unreviewed. `internal/svcendpoint/endpointfile.go`.

- 📦 **Flip the in-jail reachability probe to fatal (OQ-R2).** *Its two code gates are closed. What
  is left is one observation and 💬 1 above.*

  The probe landed in **warn mode** and its call site is already immediately above
  `genFailuresError`. **Built since:** the `YOLO_ALLOW_STALE_IMAGE`-shaped opt-out
  (`YOLO_ALLOW_UNREACHABLE_SERVICES=1`, forwarded into the jail because that is where it is
  honoured), and the **scoping** — the launcher carries `YOLO_HOST_LOOPBACK=requested|unsupported`
  into the jail so an old-passt host reports a known limitation and launches (OQ-R3) while a launch
  that *did* request forwarding and still cannot reach a service is a fault. The flip is now literally
  `reachabilityFatal = true`, both modes are under test, and a **guard test fails if the flip lands
  with the observation still owed** — so it cannot happen by accident or by tidy-up.

  **Still owed, and it is the whole gate:** observe the probe at one real boot on a healthy host. It
  has never run at a genuine container start — every green is a unit test against an in-process
  listener — and this host's own services were unreachable until the launcher fix landed. 📄
  [`loopback-tls-reachability.md`](../design/loopback-tls-reachability.md) §7, §10.

---

# 🛑 Broken

- 🛑 **The origin gate is not enforced where the code runs.** *(Found 2026-08-17, verified twice.)*

  `internal/entrypoint/packsurfaces.go:89` loads **every** staged pack with `mayAccessHost=true`. The
  host computes the refusal, prints a warning, and stages the unmodified `pack.json` anyway — nothing
  carries the decision across the boundary. So a **fetched, unapproved** pack still gets its
  `curl → bash` launcher written. The test asserting the split says *"the JAIL loader trusts the
  staged tree"* and then bypasses the jail loader.

  This is the exact shape `gateAdmitsCrossing` exists to close: **true of the decision, false of its
  enforcement.** How to fix it is **OQ-T1**; that it must be fixed is not in question. 📄
  [`trust-paths.md`](../design/trust-paths.md) §3.1.

- 🛑 **The macOS nightly cannot build an image.** Five consecutive failures since v0.8.0.

  `TestImageSkewOracleAnswers` fails on `nix eval .#installPrefix failed`, and the two lib-farm tests
  fail because the build failed. Plausibly one root cause — nix is broken on that runner — and not in
  our tree, so a re-trigger reproduces it. Needs a Mac. 📄
  [`image-staging-vs-baking.md`](../design/image-staging-vs-baking.md) §7.

---

# 🔒 Waiting

- 🔒 **The pasta fix is built but unverified on a real host.** The launcher now reads `podman info`
  and emits `--network=pasta:--map-host-loopback,…` on the default path, fail-safe in every unproven
  case. The **flag itself is measured** (a real `podman run` reproduces the outage and the flag fixes
  it, podman 5.8.4 + pasta 2026_07_16). What is unverified is a real `yolo` launch on the affected
  host — and a nested jail **cannot** verify it, by construction. The host's passt is `2026_07_16`, which
  **does** carry the flag — so the degraded path is not exercised here at all.

  *Now covered by 19 adverse host shapes assembled through `assembleRunCmd` and compared byte-for-byte
  with the pre-feature argv, so "the worst case is today's behaviour" is measured rather than
  asserted. That is not the same as a real launch.*

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

**Step 7 — the official pack — is blocked on OQ-A9.** (A7 is answered: it is selected like any other
pack.) The broker conversion is blocked on OQ-A10.

**`audio` is UNBLOCKED as of 2026-08-17** — OQ-LP14's path rule is withdrawn as false security, so it
needs no new vocabulary and now shares the same OQ-A9 dependency as the rest. **OQ-A9 is therefore the
single decision standing between here and an empty `bundled_loopholes/`.**

📄 [`broker-as-a-pack.md`](../design/broker-as-a-pack.md) — four of its six questions are answered;
**OQ-BP5** (build step vs download-only) and **OQ-BP6** (may a fetched pack ship a host-side binary?)
remain, and BP1 ruled they ship *in* this sprint.
