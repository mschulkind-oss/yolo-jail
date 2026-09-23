---
title: "Handoff: the claims that shipped on reading, and the Mac that can settle them"
status: in-review
date: 2026-09-13
tags: [macos-user, handoff, seatbelt, declaration-parity, ci, image]
summary: "Seven threads landed on 2026-09-13 whose correctness was read off source rather than observed. All of them are now measured on hardware, and the nightly's exit 125 turned out to be two more links, both fixed: the three Seatbelt probes ran (nothing inverted — Seatbelt evaluates the target, so DP-L1's mechanism stays a copy), and one launch settled the briefing batch and both notice sets. Two defects were found in the process, both fixed: the workspace reached SeatbeltProfile un-resolved, which made its rules dead and was masking a bypass of the neutral-ground refusal, and the briefing's Packages section offered a resources cap this backend ignores by ruling. DP-L1 (the largest cell, shipped unit-tested-only) is now measured END TO END: `--dry-run` composes the real context tree host-side with no sudo, so byte selection, layout and destination were observed without a password, and the privileged crossing then passed on hardware — all four §6 items, including the fail-closed read — whose stated procedure had to be corrected first, because the per-launch restage re-creates any file removed from the staged tree."
---

# Handoff: the claims that shipped on reading, and the Mac that can settle them

**Status:** CURRENT — a sequencer whose threads are all measured. The three Seatbelt probes, the
briefing batch, both notice sets and `DP-L1`'s privileged crossing ran on hardware, and
[§4](#4-the-nightly--five-links-all-now-named)'s `exit 125` was two more links, both fixed. What is
left needs no Mac: the `Nightly macOS Integration` workflow is not yet steadily green (the Actions
API, read 2026-09-23: green 2026-09-19 and 2026-09-21, red 2026-09-18, 2026-09-20 and 2026-09-22),
and whether the three NON-STOCK tests should skip on a builder-less runner is unruled.

**Audience:** an agent or human at a real Mac. Each thread says whether it needs Apple Silicon,
a password, or only a Mac.

**Role of this doc:** a *sequencer*, like
[`runbooks/mac-agent-guide.md`](runbooks/mac-agent-guide.md). It does not restate any
procedure — [`runbooks/macos-user-manual-checks.md`](runbooks/macos-user-manual-checks.md) is
the spec for what only a Mac can verify and stays the spec. What this doc adds is **what
changed on 2026-09-13 that is now waiting on hardware, in the order that buys the most.**

> [!NOTE]
> **[§1](#1-the-three-seatbelt-probes--do-these-first) IS DONE — all three probes run on hardware
> 2026-09-13, raw output recorded at
> [`../design/declaration-parity.md` §6.1](../design/declaration-parity.md#61-dp-l1-the-mechanism-is-a-copy-and-what-nobody-has-measured).**
> Nothing inverted: Seatbelt evaluates the TARGET, so `DP-L1`'s mechanism stays a copy, and
> `DP-L4`'s read is real (EPERM for the write, EACCES for the same write unsandboxed — the two
> errnos are what prove it is the profile and not the directory's owner). Probe 2 found the latent
> bug **reachable**, and fixing it exposed a second defect it had been masking: a symlinked
> workspace evaded the neutral-ground refusal. Both fixed in `4b25d7b9`.
>
> **[§2](#2-the-briefing-batch-on-the-arm-no-test-executes) and
> [§3](#3-the-new-stderr-notices-in-real-output) are DONE TOO** — one launch, 2026-09-13. The
> macos-user arm of `run.Run` is reached and every briefing expectation holds; both notice sets
> read correctly and a config declaring none of those keys prints none of the lines. That section's ⚠ was
> fixed by `0d0c26da` before the run, and that run found a **fourth** false place one section lower
> (`## Packages & Resource Limits`, offering a `resources` cap this backend ignores by ruling),
> fixed in `8027814e`.
>
> **What is left in this file: the nightly ([§4](#4-the-nightly--five-links-all-now-named)) only** —
> the nightly was re-dispatched 2026-09-13 (run `34774761002`) and its `exit 125` is still
> unexplained. That needs no Mac.
>
> ⚠ **That last sentence was true when written and is not any more, twice over.** [§6](#6-dp-l1--host-bytes-on-a-backend-that-has-never-carried-them) was
> added to this file *after* this block, so "the nightly only" never covered it; and
> [§4](#4-the-nightly--five-links-all-now-named)'s `exit 125` is no longer unexplained — it
> was two more links, both fixed, and that section now carries the whole five-link chain.
> Read [§4](#4-the-nightly--five-links-all-now-named) and [§6](#6-dp-l1--host-bytes-on-a-backend-that-has-never-carried-them) for the current state; this block is kept for the same
> reason the one below it is.

> [!IMPORTANT]
> **The original lead, kept for its reasoning.** *(Superseded above 2026-09-13.)* **Start at
> [§1](#1-the-three-seatbelt-probes--do-these-first). One measurement settles three
> threads**, two of its three probes need no yolo, and one of them can invert an entire section
> of the parity catalog. Nothing else in this file is close.

**What is NOT here, so you do not re-do it:** whether the backend works. It does —
[`handoff-macos-user-open-threads.md`](handoff-macos-user-open-threads.md) records the first
end-to-end hardware run (2026-09-12, macOS 26.5, arm64) and every runbook item passing. This
file is only about claims made *since*, on reading.

---

## 0. What changed on 2026-09-13

Two waves landed. The first fixed four CI/lint defects; the second implemented steps 1–3 and 6
of [`../design/declaration-parity.md`](../design/declaration-parity.md) [§11](../design/declaration-parity.md#11-what-i-would-build-in-order). Between them they
produced **seven claims about macos-user that no machine has executed**, plus one CI result
that is genuinely new.

**The genuinely new result, which needs nothing from you:** the `macos-user backend` workflow
went **green for the first time ever** (run `34769269355`), including the `lsp_servers` subtest
that was red on 2026-09-12. That closes the last open item of
[`handoff-macos-user-open-threads.md`](handoff-macos-user-open-threads.md) [§1](handoff-macos-user-open-threads.md#1-lsp_servers-installs-nothing-on-this-backend--ruled-and-wired-2026-09-13) — its *"leaving
one Mac run of its integration subtest"* has now happened and passed. Six of the ten runbook
items therefore have a passing automated twin on a schedule.

⚠ **The `Nightly macOS Integration` workflow is a different job and is still not a signal.** See
[§4](#4-the-nightly--five-links-all-now-named).

---

## 1. The three Seatbelt probes — do these first

**Needs:** a Mac. **Probes 1 and 2 need no yolo, no `_yolojail` account and no password.**
**Time:** ~20 minutes. **Source:** [`../design/declaration-parity.md`](../design/declaration-parity.md)
[§6.1](../design/declaration-parity.md#61-dp-l1-the-mechanism-is-a-copy-and-what-nobody-has-measured)'s `CAUTION` block, which spells all three out with their commands.

### Why this one first

It is the only measurement in this file that unblocks more than itself:

1. **It is the gate on the largest unbuilt piece of work in the catalog.** [§11](../design/declaration-parity.md#11-what-i-would-build-in-order) step 5 (`DP-L1`,
   five cells) is the materialize mechanism, and it was deliberately held back from the
   2026-09-13 implementation wave for exactly this reason. Probe 1 decides whether the
   mechanism is a copy or can be a symlink.
2. **It retroactively settles a change that already shipped.** `DP-L4` put `/var/yolo-jail` on
   `macosuser.SandboxPath` on 2026-09-13, so the sandbox can resolve `yolo` by name. That the
   Seatbelt profile permits **read + exec** there is read off the SBPL text — the profile is
   `(allow default)` with read denies only for `/Volumes`, `/Users` and `/Library/Keychains`,
   so `/var/yolo-jail` lands on `(allow default)`. **Probe 3 is precisely that read, and nobody
   has ever made it.** `macosuser.DarwinBootstrapArgv` carries no `sandbox-exec`, so the staged
   tree has only ever been read *unsandboxed*.
3. **Probe 2 pins a latent bug** independent of its own verdict —
   `macosuser.BuildRunPlan` passes `workspace` **raw** into `SeatbeltProfile` while
   `YOLO_HOST_DIR` gets `resolvePathAbs(workspace)` a few lines later. Latent only because
   `HomeContaining` normally pushes workspaces onto a non-symlinked shared root.

### RESULT, 2026-09-13 — all three run, nothing inverted, two defects fixed

| Probe | Result | What followed |
| :--- | :--- | :--- |
| 1 — the crux | `Operation not permitted` for an absolute AND a relative link, with three controls behaving | Target evaluation. The three statements stand, the symlink half is dead, `DP-L1` stays a copy. **No retraction.** |
| 2 — canonicalization | deny `(subpath "/tmp")` → `touch /tmp/canary` **succeeded**; deny `(subpath "/private/tmp")` → the same write **denied** | The latent bug is REACHABLE (Go's `os.Getwd` honours `$PWD`, so an ordinary `cd` reaches it). Measured consequence: the workspace is unwritable under the profile yolo built. **Fixed `4b25d7b9`** — plus the bypass below. |
| 3 — the staged tree | read OK; write `Operation not permitted` sandboxed vs `Permission denied` unsandboxed | The free `:ro` is observed, not predicted. `DP-L4`'s read is real. |

⚠ **Probe 2's fix could not be made alone, and that is the part worth carrying forward.**
`HomeContaining` — the neutral-ground refusal ([DP-D15](../design/declaration-parity.md#7-ruled-divergent-and-the-ones-i-would-re-open)) — read the raw path too, so
`/Users/Shared/yolo/homelink` → `/Users/matt/…` passed with `✓ all plan invariants hold`,
measured. The dead profile was the only thing making that fail-closed. **A reader who "just
resolves the path for the profile" converts a confusing launch failure into a live grant into the
invoking user's home.** One resolution, both consumers.

### The stakes on probe 1

Three statements in this tree agree that Seatbelt evaluates the **target** rather than the
link, and **none of them is an observation**: `macos-user-nix-and-features.md`'s *"Any doc that
says otherwise about this backend is wrong; this one is the authority"*, the shipped
`cache_relocations` warning in `macosuser.buildPlan`, and
[`macos-user-home-tiers.md`](../reference/macos-user-home-tiers.md#the-mirror-and-the-relative-credential-link-it-exists-for)'s
*"resolution happens in the VFS before the policy is consulted"*.

| Probe 1 result | What follows |
| :--- | :--- |
| `Operation not permitted` | The three statements stand, the symlink half is dead, `DP-L1`'s mechanism is a copy, and [§6.1](../design/declaration-parity.md#61-dp-l1-the-mechanism-is-a-copy-and-what-nobody-has-measured) is confirmed as written. |
| **Success** *(did not happen — probe 1 came back denied, both spellings)* | **[§6.1](../design/declaration-parity.md#61-dp-l1-the-mechanism-is-a-copy-and-what-nobody-has-measured) inverts.** A staged symlink becomes legitimate, and *both* `cache_relocations` warnings plus [`macos-user-home-tiers.md`](../reference/macos-user-home-tiers.md#the-mirror-and-the-relative-credential-link-it-exists-for)'s VFS claim need retracting. |

Report the raw command output either way, not a verdict — the second row rewrites shipped
warnings, so the evidence has to outlive the conclusion.

---

## 2. The briefing batch, on the arm no test executes

**Needs:** a Mac with `_yolojail` (i.e. after `yolo macos-setup`). **Time:** one launch.

`DP-L2`, `DP-L7`, `DP-L8`, `DP-L9` and the [`OQ-DP2`](../design/declaration-parity.md#decision-ledger) threading all landed on 2026-09-13 and are
verified on Linux through the real `refreshJailBriefings` → written-file path. The gap is
narrow and specific: **no test in this repo executes the macos-user arm of `run.Run`.** That
the arm is reached with `rt == "macos-user"` is read at the call site and not observed.

So: launch macos-user once and read `.yolo/`'s composed briefing. Expect the network paragraph
to say *"this environment shares the host's network stack"*, **no** ports sections, **no**
`/ctx` mounts, **no** resource limits, and a header describing Seatbelt rather than namespaces.

> [!NOTE]
> **MEASURED 2026-09-13 — the arm IS reached, and every expectation above holds.** One launch in
> `/Users/Shared/yolo/mac-hand`, reading the briefing the sandbox actually got
> (`<ws>/.yolo/home/claude/CLAUDE.md`, 7793 bytes, written by that launch):
>
> | Expectation | Observed |
> | :--- | :--- |
> | the network paragraph | *"**Network**: Host networking — this environment shares the host's network stack. `localhost` / `127.0.0.1` resolves directly to the host. No port mapping needed."* |
> | no ports sections | none |
> | no `/ctx` mounts | none — the string does not occur |
> | no resource limits in the block | none |
> | a Seatbelt header | *"# YOLO Environment — jail (native, no container)"* / *"You are confined by a Seatbelt sandbox on the human's REAL machine, not by a container."* |
>
> The macos-user-specific sections are all correct and well-aimed too — the shared-home warning
> names `.claude-shared-credentials` and `.gemini-shared-credentials` one by one, the
> rendered-from-DEFAULTS warning names both settings files, and the no-network-namespace
> paragraph says *"every port you bind is bound on the human's REAL machine … listed in
> `network.ports` or not."*

~~⚠ **While you have that briefing open, read the `## Environment` block**~~ — **FIXED
2026-09-13 (`0d0c26da`), and the measurement above is of the fixed block.** It was false in three
places at once: `/workspace` described as a bind mount (there is none — the workspace is at its
real path), **Home** as `/home/agent` (it is `/Users/_yolojail`), and **OS** as *"NixOS-based
minimal container"* — contradicting the header three lines above it. The wording ruling it was
waiting on came down as **name the absence, keep `/workspace` canonical**: the three built-in
skills carry 25 `/workspace` references as static markdown, so the bullet became the one place a
macos-user agent is told those references mean its own path.

> [!IMPORTANT]
> **A FOURTH FALSE PLACE, one section lower, which that fix did not reach — now also fixed
> (`8027814e`).** `## Packages & Resource Limits` told the agent to request a *"container-limit
> change"* by editing `resources`: no container, and `resources` is read and IGNORED here by
> ruling ([DP-D1](../design/declaration-parity.md#7-ruled-divergent-and-the-ones-i-would-re-open)).
> Worse than a wrong path — an agent that follows it asks its human for a cap nothing delivers,
> the human grants it, and the limit does not exist. DP-D1's own sentence decided the shape
> (*"a cap a user believes in but that does not hold is worse than a documented absence"*), so
> the native arm is `## Packages`, names only `packages`, and states the absence. `/workspace` is
> kept on both arms so the convention above is not forked.

---

## 3. The new stderr notices, in real output

**Needs:** a Mac with `_yolojail`. **Time:** minutes, same launch as [§2](#2-the-briefing-batch-on-the-arm-no-test-executes).

Two sets of per-key warnings now print on the macos-user arm, both new on 2026-09-13 and both
asserted only against Linux fixtures:

- `DP-L10` — one line each for a declared `devices`, `gpu.enabled` or `kvm`. These deliberately
  do **not** reuse the container path's *"not supported on macOS"* string, because that is a
  **platform** claim, true of podman and Apple Container on a Mac and false here: the key is not
  refused by a platform limit, it is read by nothing.
- `DP-L2`'s stderr half — one line each for a non-empty `network.ports` or
  `forward_host_ports`, naming remap entries separately as the not-satisfiable half.

What to check is not that they appear — Linux proves that — but that they read correctly beside
a real launch's other output, and that a config declaring **none** of these keys produces
**none** of these lines. A warning people learn to skip is worse than none
([`OQ-BP-3`](../design/backend-parity.md#open-questions)), and that is the failure mode here.

> [!NOTE]
> **MEASURED 2026-09-13, both halves.** The negative half first, because it is the one
> [`OQ-BP-3`](../design/backend-parity.md#open-questions) cares about: the real launch above
> declares none of these keys and printed **none** of these lines. The positive half came from a
> throwaway workspace declaring all five (a `--dry-run`, so no password) — every notice fired,
> and none of them reuses the container path's *"not supported on macOS"* string:
>
> ```
> Warning: `devices` is not read on macos-user — usb probe device. Device passthrough attaches a
>   host device to a CONTAINER, and this backend starts none; the sandboxed process reaches
>   devices under ordinary macOS permissions instead, so yolo neither attaches nor restricts
>   anything here.
> Warning: `gpu.enabled` is not read on macos-user — GPU passthrough is a CDI device plus
>   NVIDIA/ROCm environment on a container, and this backend starts none. …
> Warning: `kvm` is not read on macos-user — it asks for /dev/kvm inside a container, and there
>   is neither a container nor a /dev/kvm on macOS.
> Warning: `network.ports` is not honored on macos-user — 18080:8080. … 18080:8080 asks for a
>   port REMAP, which needs a second stack to land on and cannot be delivered at all: the
>   process is reachable on the port it binds.
> Warning: `network.forward_host_ports` is not honored on macos-user — 5432:5432. There is no hop
>   to make: the sandbox is already on this machine's stack, so `localhost:<port>` inside it is
>   this machine's port.
> ```
>
> `DP-L2`'s remap half reads exactly as designed — the entry is named twice, once for what is
> true anyway (the port is published on real interfaces regardless) and once as the
> **not-satisfiable** half. And each `DP-L10` line says *read by nothing* rather than
> *platform-refused*, which is the distinction that section required.
>
> ⚠ **Incidental, and worth knowing before writing a probe config:** `forward_host_ports` is
> `network.forward_host_ports`, not top-level — a top-level spelling is refused as an unknown
> key. The port-shape refusal is one of the better messages in the tree: *"expected
> '&lt;host&gt;:&lt;jail&gt;' or '&lt;ip&gt;:&lt;host&gt;:&lt;jail&gt;' (host side FIRST — the reverse of
> network.forward_host_ports)"*.

---

## 4. The nightly — five links, all now named

**Needs:** no Mac — this is CI. Listed here because it is macOS-gated in practice.

`Nightly macOS Integration` has been red since **2026-09-04** (nine consecutive scheduled runs,
last green 2026-09-04). The cause was diagnosed on 2026-09-13 and fixed: the launcher built the
image **unconditionally**, which the
[`../reference/image-staging-vs-baking.md`](../reference/image-staging-vs-baking.md) chain never
named, and [`OQ-IP4`](../reference/image-staging-vs-baking.md#why-its-this-way) now makes a stock-tagged image skip the build entirely.

> [!WARNING]
> **The run testing that fix is contaminated and must be re-dispatched.** The commit it ran on
> also carried a defect where a launch disclosure was written to the jail command's **stdout**,
> which two integration tests compare exactly. Whatever that run says about the stock tag is
> unreliable. Re-dispatch on a commit that includes the fix
> (`gh workflow run nightly-macos.yml --repo mschulkind-oss/yolo-jail --ref main`).

> [!NOTE]
> **RESOLVED 2026-09-13, and `exit 125` was two more links rather than one.** The report cap was
> widened exactly as planned, the next red nightly said what 125 was, and clearing it exposed
> another cause underneath. The whole chain, in the order each became visible — each was hidden
> by the one before it, which is the thing worth carrying forward about this failure:
>
> | # | Cause | Symptom it presented as | Fixed |
> | :--- | :--- | :--- | :--- |
> | 1 | `imageIdentity` varied by system, so a darwin host could not vouch for a Linux-built image | every launch demanded a rebuild | [`OQ-IP1`](../reference/image-staging-vs-baking.md#why-its-this-way), 2026-09-12 |
> | 2 | the launcher built the image **unconditionally** — there was no rebuild *decision* to fix | `IMAGE BUILD FAILED` | [`OQ-IP4`](../reference/image-staging-vs-baking.md#why-its-this-way) |
> | 3 | `podman machine init -v` REPLACES the default share set, so `-v /nix:/nix` deleted `/Users`, `/private` and `/var/folders` | `exit 125` → `Error: statfs <path>` | `1e55f321` |
> | 4 | two of four shards exceeded `timeout-minutes: 50` once launches did real work | half the run's evidence silently missing | eight shards, `178fa9f2` |
> | 5 | `-v /nix/store:/nix/store:ro` shadowed the image's own store; `/bin/*` are symlinks BY VALUE into it, and a darwin store holds no Linux closure | `exec: "bash": executable file not found` — 50 times, 41 tests | `8f59c674` |
>
> **Link 3's fix is what made link 5 visible**, and link 5 had been latent since 2026-09-08.
> Nothing was flaky; every one was deterministic and each only surfaced once its predecessor
> stopped failing first.
>
> ⚠ **What is NOT fixed, and is expected:** three tests (`TestExtraPackageLibFarm`,
> `TestExtraPackagesFromMountedStore`, `TestDevPackageLinksRuntimeLib`) declare `packages:`, which
> makes them genuinely NON-STOCK, so they correctly build — and this runner has no Linux builder.
> That is the designed behaviour, not a bug. Whether they should SKIP on a builder-less runner
> rather than fail is an open question nobody has ruled.
>
> **Still open:** the run proving link 5 had not finished when this was written. Confirm with
> `exec: "bash"` → 0, `IMAGE BUILD FAILED` → still 3, and the other 38 green.

---

## 6. `DP-L1` — host bytes on a backend that has never carried them

**Needs:** a Mac with `_yolojail`. **Time:** two launches and one `sudo rm`.
**Added 2026-09-13, AFTER the run that closed [§1](#1-the-three-seatbelt-probes--do-these-first)–[§3](#3-the-new-stderr-notices-in-real-output)** — so nothing in this section has been
exercised by anything.

**This is now the highest-risk unmeasured thing in the tree**, and it is a different KIND of
risk from the rest of this file. Everything above was a claim about what a launch SAYS;
this is a claim about what it DOES with bytes from your home directory, and about a
launch that can now REFUSE.

`DP-L1` shipped the parity catalog's largest cell: pack `reads-host` grants and
source-bearing `host_files` entries now reach the sandbox by a host-side copy into
`/var/yolo-jail/ctx/<cname>`, named by `YOLO_CTX_ROOT`. It was built on the strength of
[§1](#1-the-three-seatbelt-probes--do-these-first)'s probe 1 — Seatbelt evaluates the target, so the mechanism had to be a copy — and it is
**unit-tested only**. No test in this repo executes `sudo cp -R`, `sandbox-exec`, or the
darwin bootstrap.

> [!NOTE]
> **THE SUDO-FREE HALF IS MEASURED 2026-09-13, and it is more than expected: `--dry-run`
> composes the real tree on the host.** The copy is what needs a password; *composing what
> would be copied* does not, and `--dry-run` does it. So the whole read side of DP-L1 —
> which bytes are selected, from where, laid out at which destination — is observable with
> no `sudo` at all. What is left for a password is genuinely only the crossing.
>
> Probe: a throwaway workspace, one source-bearing `host_files` entry passed at user scope
> through `--user-layer` (so nothing touched the real user config), source deliberately
> outside `$HOME` so nothing was written to a home directory either.
>
> ```text
> host bytes:  /var/yolo-jail/ctx/yolo-yolo-dpl1-probe-32c78128 (root-owned; the sandbox
>              reads it and cannot write it)
> ```
>
> and the tree that line names, composed under the staging dir and readable without sudo:
>
> ```console
> $ find …/agents/yolo-yolo-dpl1-probe-32c78128/ctx-tree -type f
> …/ctx-tree/host-pi/settings.json
> …/ctx-tree/host-user/.dpl1-probe.json
> $ cat …/ctx-tree/host-user/.dpl1-probe.json
> { "dpl1_probe": "these bytes must reach the sandbox" }
> ```
>
> Three things that were claims and are now observations: the plan's `host bytes:` line is
> **reached** and names a staged tree rather than the `none staged` branch; a source-bearing
> `host_files` entry composes **verbatim** at `host-user/<slug>`; and a pack `reads-host`
> grant composes beside it at `host-<slug>/`, keyed on `StagedSlug` as
> [`macosctxtree.go`](../../internal/cli/run/macosctxtree.go) requires. The argv
> [the copy itself](#6b-the-copy-actually-happens) asks about is readable from the same run — the plan prints the
> `cp -R` → `chmod -R a+rX` → `rm -rf` → `mv -f` replace-by-rename sequence in full, under
> `── privileged commands (run via sudo) ──`.
>
> ⚠ **FAIL-OPEN WAS EXERCISED BY ACCIDENT, AND IT IS WHY THAT TREE HAS NO `host-claude/`.**
> On this Mac `~/.claude/settings.json` is a symlink to `~/.dotfiles/claude/settings.json`
> and **that target does not exist**, so `isFile` (`os.Stat`, which follows the link)
> returns false and the grant is skipped — exactly the documented "fail-open on a source
> that is not there" half, observed rather than reasoned about. `pi`'s identical
> declaration delivered because *its* symlink resolves. **This is not a yolo defect**: at
> pack level the two are indistinguishable — both report `readsHost=true` with a
> `/ctx/host-<slug>/settings.json` source under either autonomy posture, measured directly
> against `SurfacesFor`.
>
> It does mean **whoever runs [end-to-end composition](#6c-end-to-end-composition-which-is-the-whole-point) on this machine must repair that symlink first**, or the one
> sentence DP-L1 exists to make true will be tested against a grant that correctly delivers
> nothing.

> [!IMPORTANT]
> **What still needs a password, and it is only the crossing.** The refusal in
> [the fail-closed read](#6a-the-fail-closed-read--do-this-one-first), the *execution* of [the copy itself](#6b-the-copy-actually-happens) (its argv is
> already read, above), [end-to-end composition](#6c-end-to-end-composition-which-is-the-whole-point) and [the `:ro` half](#6d-the-ro-half-one-leaf-deeper-than-probe-3-measured) all need a real launch, and
> `sudo -n` fails on this Mac (`/etc/sudoers.d/matt` is `ALL = (ALL) ALL`, no `NOPASSWD`).
> The plan says the prompt is survivable — *"sudo may prompt for your password; it's
> forwarded through the TTY proxy so you can answer inline"* — so this needs a human at a
> terminal, not a change to the machine.

> [!NOTE]
> **[§6](#6-dp-l1--host-bytes-on-a-backend-that-has-never-carried-them) IS CLOSED — all four items measured on hardware 2026-09-13** (macOS 26.5, arm64,
> `0.8.0+1546.gd14124b7`), a human answering the sudo prompt. **`DP-L1` no longer has an
> unexercised half.**
>
> | Item | Result |
> | :--- | :--- |
> | 6a fail-closed read | **PASS** — refused, `rc=1`. Procedure had to be corrected first; see below |
> | 6b the copy happens | **PASS** — `root:wheel`, dirs `drwxr-xr-x`, files `-rw-r--r--` (`a+rX` adds no `x` to a non-executable file) |
> | 6c end-to-end composition | **PASS** — `/Users/_yolojail/.dpl1-probe.json`, `-r--r--r--`, owned `_yolojail`, bytes verbatim |
> | 6d the `:ro` half | **PASS** — `ls -laR` of the tree worked; `touch` → `Operation not permitted`. Observed, no longer inferred |
>
> The 6a refusal is **stronger than this section asked for** — it names the surface, the
> staged path, the errno, the destination it declined to write, and the ruling:
>
> ```text
> Error: configure_pi_settings: surface pi/settings: the launch delivered the user's own
> copy of this file to /var/yolo-jail/ctx/<cname>/host-pi/settings.json and it cannot be
> read there: open …: permission denied.
> Refusing rather than composing ~/.pi/agent/settings.json without the host layer — the
> result would be a config file that looks correct and is missing the user's own settings
> (OQ-CO10, docs/design/config-ownership-and-promotion.md)
> …
> yolo-jail macos-user bootstrap: refusing to start the jail: 1 config generator(s) failed
> ```
>
> ⚠ **Two procedural corrections, both worth carrying forward.**
>
> 1. **A `/tmp` workspace cannot be used at all.** The launch pre-flights the workspace for
>    a usable `_yolojail` group ACL and refuses without one, before staging anything. Put
>    the probe workspace under `/Users/Shared/yolo` — that root carries the ACL with
>    `directory_inherit`, so a fresh subdir inherits it and no `macos-fix-permissions` step
>    is needed. (The refusal's *"most likely this project was MOVED"* guess is wrong for
>    this case — a directory created outside the shared root never had the ACL to lose —
>    though the fix it names is still the right one.)
> 2. **`cd` into that workspace.** The cwd selects the workspace and a `--user-layer` entry
>    applies regardless, so a drifted launch still reads the probe file and looks correct;
>    the first 6a run silently exercised the `yolo-jail` repo workspace instead, and only
>    the refusal's cname gave it away. The measurement was still valid, the blast radius
>    was not what it claimed. Same shape as the cwd-drift rule in `AGENTS.md`.

### 6a. The fail-closed read — do this one first

**`YOLO_HOST_LAYERS` no longer reports `unsupported` on this backend.** It reports
`supported` plus the delivered list whenever a tree was staged, which means **macos-user
now refuses a launch when a delivered file is unreadable**, exactly like every other
backend. That behaviour has never existed here before and nothing has executed it.

1. Launch with a source-bearing `host_files` entry (or a pack declaring `reads-host`)
   and confirm the file reaches the agent.
2. ~~`sudo rm` one file out of `/var/yolo-jail/ctx/<cname>/` and launch again.~~
   **This step cannot work — see the correction below.**
3. **Expect a REFUSAL naming the missing file**, not a degraded launch and not a launch
   that silently composes from defaults.

⚠ If it degrades instead of refusing, that is the defect — and it is worse than the gap
it replaced, because the old behaviour at least announced itself as `unsupported`.

> [!CAUTION]
> **STEP 2 IS SELF-DEFEATING, measured 2026-09-13. It was run as written, it reported
> `rc=0`, and that `rc=0` was CORRECT.** Every launch rebuilds the context tree from the
> host source (`buildMacosCtxTree` `os.RemoveAll`s its staging dir) and then replaces the
> staged copy wholesale — `rm -rf <cname>.new` → `cp -R` → `chmod -R a+rX` →
> `rm -rf <cname>` → `mv -f`. So the launch that is supposed to notice the missing file
> **re-creates it first**. Verified after the fact: the removed `host-pi/settings.json` was
> back, byte-identical, same mtime as its siblings.
>
> That rebuild is a feature, not an oversight — it is what makes a *revoked* grant stop
> being delivered, which `macosctxtree.go` calls out by name. The consequence is that the
> staged tree cannot be falsified by hand between launches, so no amount of tampering with
> `<cname>/` tests anything.
>
> **WHAT ACTUALLY REACHES THE REFUSAL.** `internal/entrypoint/packsurfaces.go` refuses when
> four things hold at once: the surface declares `readsHost`; `os.ReadFile` of the staged
> path fails; `YOLO_HOST_LAYERS` parses; and the report says that path was **delivered**.
> The error *kind* is not examined, so any read failure qualifies — which is the opening.
>
> The one lever the restage cannot undo is the mode of **`/var/yolo-jail/ctx` itself**:
> `chmod -R a+rX` is applied to `<cname>.new` only, while the parent is merely `mkdir -p`'d.
> So `sudo chmod 0700 /var/yolo-jail/ctx` survives a relaunch, leaves root's staging working
> (it runs as root), and leaves `packs/` and `home-overlay/` readable since they sit under
> different parents. It isolates exactly the read under test. **Restore the mode afterwards
> in a trap** — left at `0700` it breaks every later launch that delivers host bytes.
>
> Replace step 2 with that, and this section passes. A step 2 that produces `ENOENT` rather
> than `EACCES` would be closer to the original wording and nobody has found one that
> survives the restage; the code does not distinguish them, so it is not worth hunting.

### 6b. The copy actually happens

The unit tests pin the **argv**, never its execution. So confirm that `sudo cp -R`,
`chmod -R a+rX` and the replace-by-rename actually run, and that the tree lands
root-owned and `a+rX`:

```console
$ ls -la /var/yolo-jail/ctx/<cname>/
$ stat -f '%Su %Sp' /var/yolo-jail/ctx/<cname>/host-user/<slug>
```

### 6c. End-to-end composition, which is the whole point

Put a real `~/.claude/settings.json` on the Mac, launch, and confirm the agent's composed
`~/.claude/settings.json` inside the sandbox carries its content. This is the sentence
`DP-L1` exists to make true and no machine has ever run it.

### 6d. The `:ro` half, one leaf deeper than probe 3 measured

[§1](#1-the-three-seatbelt-probes--do-these-first)'s probe 3 observed read-OK / write-EPERM for `/var/yolo-jail/` and
`/var/yolo-jail/packs/`. The new tree is `/var/yolo-jail/ctx/`, under the same root deny
with no re-allow — so it is a **sound inference from a measurement, not a measurement**.
One `touch` inside the sandbox settles it.

### What it would mean if 6a fails

`DP-L1` retired the `unsupported` carve-out on the strength of the mechanism existing. If
the fail-closed read does not work, that retirement was premature and
[`OQ-R3`](../reference/loopback-tls-reachability.md#oq-r3)'s "a host yolo cannot fix degrades
and launches" needs re-reading against this backend specifically — which is a ruling, not a
patch.

---

## 5. What a green run would let us delete

Not work — a consequence worth knowing, because it changes what the next reader trusts.

[`../design/declaration-parity.md`](../design/declaration-parity.md) [§11](../design/declaration-parity.md#11-what-i-would-build-in-order) carries a `CAUTION` stating that every macos-user row in
the catalog is *"ruled against reading, not against measurement"*, and that **two macos-user
launch warnings were retired on 2026-09-12 on the strength of code that has never executed**.
Items [§1](#1-the-three-seatbelt-probes--do-these-first) and
[§2](#2-the-briefing-batch-on-the-arm-no-test-executes) together are most of what that caution
is about. A Mac session that runs both either retires the caution or re-opens a carve-out that
is currently undeclared — and the second outcome is the one worth going looking for.

---

## What to do first, if you want an order

> [!IMPORTANT]
> **Everything in [§1](#1-the-three-seatbelt-probes--do-these-first)–[§3](#3-the-new-stderr-notices-in-real-output) is DONE (2026-09-13).** The order below is kept for its
> reasoning and is superseded by one line: **[§6](#6-dp-l1--host-bytes-on-a-backend-that-has-never-carried-them) is the only section nothing has
> exercised, and [the fail-closed read](#6a-the-fail-closed-read--do-this-one-first) is the only place a launch can now REFUSE where it never could
> before.** Start there. [§4](#4-the-nightly--five-links-all-now-named) needs no Mac and is running on its own.
>
> **AMENDED 2026-09-13 — [§6](#6-dp-l1--host-bytes-on-a-backend-that-has-never-carried-them)'s READ HALF IS NOW MEASURED, so "nothing has exercised it" is
> no longer true of the whole section.** `--dry-run` composes the real context tree on the
> host without touching `sudo`, which turned selection, layout and destination into
> observations. **What is left needs a human at a terminal, not a Mac** — the four remaining
> items are all the same one launch, and the password prompt is the only reason an agent
> cannot run it. Do that launch and [§6](#6-dp-l1--host-bytes-on-a-backend-that-has-never-carried-them) closes.

1. [§1](#1-the-three-seatbelt-probes--do-these-first) probes 1 and 2 — no yolo, no account, no
   password. Twenty minutes, and probe 1 can invert a section.
2. `yolo macos-setup` if the account is absent, then [§1](#1-the-three-seatbelt-probes--do-these-first)
   probe 3 — the read `DP-L4` already depends on.
3. One macos-user launch, reading [§2](#2-the-briefing-batch-on-the-arm-no-test-executes) and
   [§3](#3-the-new-stderr-notices-in-real-output) off the same run.
4. Re-dispatch the nightly ([§4](#4-the-nightly--five-links-all-now-named)) — it
   needs no Mac and can run while you do the rest.

**Report raw output, not verdicts.** Three of these threads can retract something already
shipped, and a verdict without its evidence cannot be re-litigated when the next reader
disagrees.
