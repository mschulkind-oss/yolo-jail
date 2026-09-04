---
title: "How many copies of an agent CLI does a machine need — and who deletes the rest"
date: 2026-09-04
status: in-review
tags: [delivery, disk, capture, workspaces, packs, prune, reflink]
summary: "The 1.2 GB that justified building a content-addressed store decomposes into two independent axes, and capture only collapses one of them — the smaller one, conditionally on reflink, once. Capture is still right; the disk argument for it was wrong, and the sequencing that argument bought should be reversed."
---

# How many copies of an agent CLI does a machine need — and who deletes the rest

**Status:** DESIGN, 2026-09-04. **Nothing built here.** Re-examines a premise
[`program-delivery.md`](program-delivery.md) already ruled on
([OQ-PD15](program-delivery.md#decision-ledger)) and holds one of its live questions
([OQ-PD17](program-delivery.md#decision-ledger))
up against the corrected facts. Four of six capture slices are landed
([`../plans/install-capture.md`](../plans/install-capture.md)); this doc does not propose
reverting any of them.

**The short version.** The number that justified the capture-first ordering — *"1.2 GB of claude per
workspace"* — is real, and it is **two costs added together, not one**. Call them the **N axis**
(one machine, many workspaces, each holding its own copy of the same version) and the **V axis**
(one workspace, many *versions*, because the vendor's updater never removes the old one) — both
*coined here*. Measured in this jail 2026-09-04, the split is **204.7 MiB on the N axis and 1018.6
MiB on the V axis: 83.3 % of the number is the V axis.** Capture collapses the **N axis only**, only
for the **cold install**, and only **where the filesystem supports reflink** — on ext4 it makes the
machine-wide total *slightly worse*. So the disk argument for capture was never the argument;
capture's real case is **lockability, an observable install, and offline materialize**, none of which
this doc disputes. My verdict: **keep capture, retract its disk justification, reverse the
sequencing that justification bought, and give the V axis its own fix — a delete-on-success version
prune, which is not a stopgap because capture never deletes it.**

**The most important section is [§3](#3-which-axis-each-mechanism-collapses)** — the axis table. Every
disagreement with this doc should be locatable as a disagreement with one of its rows.

**Scope note — what this doc owns, and what it hands back.** It owns **the comparison**: how many
copies of an agent CLI should exist on a machine, which mechanism collapses which axis, and what each
one costs across filesystems and workspace counts. It does **not** own the mechanisms themselves.
The capture design is [`program-delivery.md`](program-delivery.md) §6.3's and stays there. The
question *"who triggers a reclaimer"* is [`minimal-disk-footprint.md`](minimal-disk-footprint.md)'s
(OQ-DF2), and §9 below routes the answer there rather than re-deciding it. The home layout is
[`jail-home.md`](jail-home.md)'s. Nothing in §2–§4 restates a measurement those docs already carry;
where a figure appears in both, I re-took it.

**Reads with:**

- [`program-delivery.md`](program-delivery.md) — the doc whose §5 alternative set this extends and
  whose OQ-PD15/PD17 this re-examines. Its §6.3 is the capture design; §3.5 is the evergreen policy.
- [`minimal-disk-footprint.md`](minimal-disk-footprint.md) — *"a mechanism whose only trigger is a
  human noticing is not a mechanism, it is a suggestion."* Its §8 explicitly puts per-workspace
  overlay growth out of scope; this doc is that exclusion, taken seriously.
- [`jail-home.md`](jail-home.md) — the tier model (`:ro` machine base, per-workspace writable
  overlays) that made `~/.local` per-workspace in the first place.
- [`macos-user-home-tiers.md`](macos-user-home-tiers.md) — the existence proof for the shared
  prefix, and the honest catalogue of what that sharing costs.
- [`../plans/install-capture.md`](../plans/install-capture.md) — what is actually built (slices 1–4
  and the recording half of 6), and slice 5's blocking correction.

---

## 1. The verdict, and four principles

**Verdict.** Capture was the right call for the reasons §6.3 gives second, not the reason it gives
first. Concretely, five rulings I would ask for:

1. **Retract the disk justification.** *"Capture removes the per-workspace disk cost"* is true of at
   most 16.7 % of the measured cost, once, on reflink-capable filesystems only. Say so in §6.3 and in
   OQ-PD10's ledger row.
2. **Reverse [OQ-PD15](program-delivery.md#decision-ledger).** Its whole reasoning was *"evergreen
   multiplies exactly the disk cost capture removes"* and *"under (a) there is nothing to prune."*
   Both premises are false (§5.1, §7.2): evergreen multiplies the **V** axis, capture collapses the
   **N** axis, and the two do not meet. Evergreen is unblocked today.
3. **Build the V-axis prune, and stop calling it a stopgap.** Keep-newest-K version dirs per program,
   executed by the act that installed the new one. It has no reference-oracle problem, no filesystem
   dependence, and capture does not delete it — under capture it is exactly as necessary.
4. **Do not build the CAS garbage collector as OQ-PD17 frames it.** That question asks for an
   *unreferenced* oracle. Reclaiming a capture entry is **never unsafe on any materialize arm**
   (§4.2, measured) — it only forces a re-capture. The question is therefore an efficiency question
   with a policy answer, not a correctness question needing an oracle.
5. **Price the shared prefix before the next store is built.** It is not "a second sharing mechanism"
   — it is the *fourth* instance of a mechanism yolo already ships, with a declared manifest
   vocabulary (§5.3). If it is refused, it should be refused on its concurrency and blast-radius
   costs, which are real, and not on the circular ground that capture would replace it.

Four principles the rest of the doc leans on:

- **P1. Name the axis before pricing the fix.** A per-machine program cost is `N × V × S`
  (workspaces × retained versions × bytes per version). A mechanism that collapses one factor is not
  interchangeable with one that collapses another, and adding them up as "1.2 GB" hides which.
- **P2. A design that is excellent on btrfs and harmful on ext4 must say so in those terms.** The
  filesystem is not a deployment detail here; it inverts the sign of the change (§4.1).
- **P3. Prefer a mechanism whose reference set is local and complete.** A per-workspace versions
  directory has one referrer, the symlink beside it. A machine-global store has an unknown set of
  referrers, which is the entire content of OQ-PD17. Locality is not a nicety — it is the difference
  between a rule and a research question.
- **P4. A shipped mechanism with no trigger is evidence about triggers, not about mechanisms.** Two
  of the five options below are already in the tree and have never run (§3.1). Building a third
  before triggering either is how a project accumulates reclaimers.

---

## 2. The number, decomposed

**MEASURED 2026-09-04, this development jail, one workspace.** `~/.local/share/claude/versions`
holds five single-file builds; `~/.local/bin/claude` is an absolute symlink naming exactly one of
them.

| Version | Bytes | Written | Live? |
| :--- | ---: | :--- | :--- |
| 2.1.165 | 244 917 968 | 2026-06-05 | |
| 2.1.218 | 273 177 584 | 2026-07-22 | |
| 2.1.219 | 275 004 400 | 2026-07-24 | |
| 2.1.220 | 275 012 592 | 2026-07-24 | |
| **2.1.260** | **214 687 216** | 2026-09-03 | ✅ `~/.local/bin/claude` → here |
| **total** | **1 282 799 760 (1223.4 MiB)** | | |

**One claude is 204.7 MiB. The other 1018.6 MiB — 83.3 % — is four builds nothing references.**

Two facts make this a *decomposition* rather than an anecdote:

- **The V axis is the vendor's updater, not yolo's launcher.** The launcher installs once per
  (workspace × binary) and is PATH-shadowed forever after
  ([`program-delivery.md`](program-delivery.md) §5.2, [OQ-PD8](program-delivery.md#decision-ledger)) —
  it cannot have produced four extra builds. claude's own updater did, and the dates prove the
  mechanism: the four builds on disk span 2026-06-05 to 2026-07-24; the tree then sat at 2.1.220 with
  a captured `env.DISABLE_AUTOUPDATER=1` in force from 2026-08-05; and 2.1.260 landed **the same
  evening that capture was cleared**, 2026-09-03 ([`program-delivery.md`](program-delivery.md) §4.1).
  Four builds in seven weeks, then six weeks of nothing, then one within hours of the thaw.
- **claude is not the whole bill.** The same workspace's other two program surfaces
  (`paths.HomeSurfaces()`, `internal/paths/paths.go:415-421`) hold **963 MiB** of `~/.npm-global`
  (`@github/copilot` 359 M, `@openai/codex` 347 M, `@earendil-works` 160 M, plus LSP/MCP servers) and
  a **180.4 MiB** `~/.local/bin/agy`. **2366.8 MiB — 2.31 GiB — of program bytes in one workspace**,
  of which claude's stale versions alone are **43 %**.

So the shape to design against is:

$$\text{machine total} \;=\; \sum_{\text{programs}} N \times V \times S$$

where **N** is workspaces on the machine, **V** is versions retained per workspace, and **S** is
bytes per version. *(N axis, V axis — coined here. They are not existing yolo vocabulary and no
sibling doc uses them; if they outlive this doc they belong in the glossary.)*

---

## 3. Which axis each mechanism collapses

This is the table the rest of the doc is a commentary on.

| Mechanism | Collapses | Filesystem dependence | Reference oracle it needs | Status |
| :--- | :--- | :--- | :--- | :--- |
| **Capture + materialize** ([§6.3](program-delivery.md#63-installers-that-just-do-whatever-capture-the-install-then-treat-the-capture-as-the-package)) | **N**, cold install only | **reflink**, else it *adds* one copy (§4.1) | machine-wide, unknown referrers — **OQ-PD17** | slices 1–4 landed; 5 blocked |
| **Host-side hardlink dedup** (§5.4, `prune.HardlinkDuplicateFiles`) | **N**, post-hoc | hardlink: universal, but **one mount** | kernel-maintained `st_nlink` | **shipped, never triggered** |
| **Machine-global program prefix** (§5.3) | **N**, *and the download*, structurally | none | none — there is one copy | not built (container backends) |
| **Keep-newest-K version prune** (§5.1) | **V** | none | **local and complete**: the symlink beside the versions dir | not built |
| **Disable the vendor self-updater** (§5.2) | **V**, at source | none | none | config fix, available today |
| **Evergreen** ([§3.5](program-delivery.md#35-the-second-axis-who-the-dependency-serves-amendment-2026-09-03)) | nothing — it *drives* V | none | n/a | planned, blocked by OQ-PD15 |

Three readings fall out, and all three are the point of this doc.

**(a) Capture and the measured number are on different axes.** Capture's materialize branch lives
inside `_do_install`, which the launcher reaches only from `if [ ! -x "$REAL_BIN" ]`
(`internal/entrypoint/shims.go:1019-1027,1084-1096`, read 2026-09-04). Every arm that is *not* the
cold install calls `"$REAL_BIN" install` and has no materialize path. Under evergreen that arm
becomes a real vendor update, which downloads. **So capture saves exactly one copy per workspace,
once — and every update after it escapes the store.** After *k* evergreen updates the machine holds
`S + N×k×S`, asymptotically the same as today's `N×k×S`. The saving does not compound; it dilutes.

**(b) The 83 % has no owner.** No row above collapses V except the last two, and neither is built or
scheduled. [OQ-PD15](program-delivery.md#decision-ledger) explicitly *deleted* the prune — *"under (a)
there is nothing to prune, because a materialized tree comes from the CAS and old versions were never
per-workspace to begin with."* The second clause is the error: old versions are per-workspace under
capture too, because the self-updater writes into the workspace's writable home, and
[`../plans/install-capture.md`](../plans/install-capture.md)'s own Traps section says so approvingly
— *"claude's updater writes new version dirs (new inodes) and is safe."* Safe for the store; not
absent from the workspace.

**(c) §6.3's escape clause is the V-axis fix, and it does not need capture.** The claim that *"vendor
self-updaters are structurally neutered … a self-update is either disabled or becomes drift the
reconcile reports"* is unbuilt, and its first arm — disabling the updater — is a settings key
available today, with no store, no manifest and no reflink. [OQ-PD15](program-delivery.md#decision-ledger)
says as much in its own last paragraph (*"the `DISABLE_AUTOUPDATER` class of config-capture bug is
separately fixable today and is not gated on any of this"*) without noticing that this is the fix for
83 % of the number the ruling was made on.

### 3.1 Two mechanisms already ship for the N axis, and neither has ever run

**`prune.HardlinkDuplicateFiles`** (`internal/prune/prune.go:126-197`) groups files across workspaces
by `(size, sha256)` and hardlinks the duplicates, using the atomic link-to-tmp-then-rename discipline
and skipping same-inode pairs. Its input is `WalkDedupableWorkspaces` (`:88-99`), which walks exactly
`<ws>/.yolo/home/{npm-global,local,go}` — **the same three surfaces capture walks**, by construction:
both derive from `paths.HomeSurfaces()` (`internal/paths/paths.go:415-421`, whose doc comment names
both consumers). It is **on by default** in `yolo prune` unless `--no-hardlink`
(`internal/prune/prunecmd.go:313-334`), and `--dedup-global` extends it to the shared caches.

For the installer class it is close to ideal: a vendor ships one file per version, two workspaces on
one version hold byte-identical files, and the kernel then maintains the reference count for free —
P3, satisfied by the filesystem rather than by a design. It degrades quietly rather than dangerously:
`os.Link` failing (two workspaces on different mounts) is a `continue` (`prune.go:183-186`), i.e. a
missed opportunity, and an incomplete workspace list from `FindYoloWorkspaces`
(`internal/prune/probes.go:148-174`, which reads `podman ps -a` and cannot see a workspace whose
container was removed) is *also* only a missed opportunity — the exact same enumeration that
[`../plans/install-capture.md`](../plans/install-capture.md)'s Traps section correctly refuses as a
**GC** oracle is perfectly adequate as a **dedup** driver, because dedup's failure mode is doing less
and GC's is deleting live bytes.

**And it has never run**, for [`minimal-disk-footprint.md`](minimal-disk-footprint.md) §1's reason:
every reclaimer yolo owns is reachable only from a human typing `yolo prune`. That doc names
`HardlinkDuplicateFiles` in its list of sixteen. This is P4 in its sharpest form: **before building a
third N-axis mechanism, it is worth knowing what the first one would have reclaimed**, and nobody on
this machine knows, because nobody has typed the command.

> [!NOTE]
> **The hazard hardlink dedup carries is the same one the capture plan calls its sharpest trap, and it
> is already shipped.** *"A hardlinked CAS file is the running program's bytes"* — an installer that
> opens a materialized file for write reaches every workspace at once. `HardlinkDuplicateFiles`
> creates exactly that relationship between two workspaces' copies today, with no admit-time
> read-only freeze to bound it. The plan's own mitigating observation applies unchanged (claude's
> updater writes new inodes), and so does its warning that this is not a general guarantee. If dedup
> is triggered automatically, it should acquire the freeze that capture's admit path already has.

---

## 4. Materialize is conditional on reflink, and the sign inverts without it

**RE-MEASURED 2026-09-04**, independently of the capture build, across two binds of one btrfs
(`/workspace` and `/home/agent/.local`, identical `st_dev` 61):

| operation, 32 MiB | result |
| :--- | :--- |
| `link(2)` | **`EXDEV`** — the kernel compares the mount, not the device |
| `rename(2)` | **`EXDEV`** — same reason |
| `FICLONE` | **OK** — destination is its own inode, `nlink 1` on both sides |

Both halves of the capture design's amendment hold. The consequence the amendment states — that
`st_nlink` can no longer be a GC oracle — also holds, and is OQ-PD17.

### 4.1 The ext4 inversion, in the terms P2 asks for

`internal/capture/clone_linux.go:41-45` already states the exposure in the code: *"btrfs, XFS with
reflink=1, and ZFS support it; ext4 does not, and ext4 is the default filesystem of most Linux
installs and of every GitHub runner."* What is nowhere written down is what that does to the
machine-wide arithmetic. For one program at one captured version:

| Filesystem | Store | Per workspace | Machine total, N workspaces | vs. today |
| :--- | :--- | :--- | :--- | :--- |
| reflink (btrfs, XFS `reflink=1`, ZFS ≥2.2.3) | S | ≈0 (shared extents) | **S** | −(N−1)·S |
| **no reflink (ext4), or store and home on different filesystems** | S | **S** (full copy) | **(N+1)·S** | **+S** |

So on the filesystem that most Linux machines and every GitHub runner use, capture's *disk* effect is
to add one machine-wide copy while changing nothing per workspace. It still saves the **download**
(N−1 fetches of ~205 MiB), which is a real and filesystem-independent win — but a download saving is
not a disk saving, and OQ-PD15 was ruled on a disk number.

*(Filesystem support is stated from the kernel/filesystem documentation and from the repo's own claim
above, **NOT MEASURED here** — only btrfs is available in this jail. The one number I would want
before shipping a default is what share of real yolo installs are on ext4; nobody has it, which is
itself an argument for a design that does not depend on the answer.)*

### 4.2 Reclaiming a capture entry is never unsafe — which reframes OQ-PD17

**MEASURED 2026-09-04.** A 16 MiB file was reflinked from `/workspace` into `/home/agent/.local`, the
**source was then unlinked**, and the destination's sha256 was unchanged and its content fully
readable. That is copy-on-write semantics working as specified, and it means:

| materialize arm | delete the store entry | workspace loses |
| :--- | :--- | :--- |
| reflink | destination keeps its own inode and the extents | nothing — a re-capture is needed only to materialize into a *new* workspace |
| hardlink | `nlink` drops; the workspace's link survives | nothing |
| copy | independent bytes | nothing |

[`../plans/install-capture.md`](../plans/install-capture.md) slice 5 already says this for the copy
arm — *"which strands nothing (it has its own bytes) and only forces a re-capture"* — and then asks
for an unreferenced oracle anyway. **On the corrected facts the oracle is not load-bearing for
correctness on any arm.** OQ-PD17's stakes line (*"whether captures can be reclaimed at all … entries
accumulate with no way to remove them"*) overstates it: entries can always be removed; what is at
stake is only whether a removal is *wasteful*. That is answerable by the two idioms the plan already
calls safe under any oracle — keep-newest-K per (bin, platform) and an age floor — and it does not
need candidate (a), (b) or (c). See [OQ-CP3](#-oq-cp3--is-oq-pd17-answered-by-no-oracle-bound-by-policy--resolved-2026-09-04).

---

## 5. Alternatives, each with a verdict

[`program-delivery.md`](program-delivery.md) §5 weighed six (bake / lazy install / pin-and-cache /
regenerate / do nothing / borrow lockfiles). These five are not among them. Numbering continues that
doc's series so the set reads as one.

### 5.1 A7 — Prune stale versions, executed by whoever installed the new one

A `keep-newest-K` rule over a program's version directory, run **by the act that created the new
version**, in the same workspace, immediately after it succeeds. No store, no enumeration, no oracle:
the referrer set for `~/.local/share/claude/versions/*` is `~/.local/bin/claude`, one symlink, in the
same directory tree, and everything else there is unreferenced *by construction for that workspace*.
Default `K = 2` (the live one plus one rollback target); the unit is versions, not bytes. **`K` is
not `N`:** `N` is the workspace count throughout this doc, and the plan's own spelling of this idiom
is "keep-newest-N" — renamed here to keep the two apart.

**What it does and does not solve.** Solves the V axis completely and on every filesystem — 1018.6 of
1223.4 measured MiB, at `K = 1`. Solves nothing on the N axis: two workspaces still each hold a live copy.
Buys no record, no manifest, no offline install, no drift detection — everything capture is actually
for.

**Why it is not a stopgap.** OQ-PD15 deleted it on the ground that capture would delete it. Capture
does not: the self-updater keeps writing new version dirs into a writable per-workspace home whether
or not the first one arrived from a store, so under capture this prune has exactly the same work to
do. The trigger placement is the interesting half, and it is
[`minimal-disk-footprint.md`](minimal-disk-footprint.md) OQ-DF2's option (i) — delete-on-success at
the write path — which is that doc's own leaning, for its own reasons (thrash-free by construction,
narrowest blast radius). Under evergreen the write path is `_update`, which is being built anyway.

> **Verdict: adopt, and land it with evergreen rather than before or after it.** It is the only
> option that touches 83 % of the measured cost, it has no filesystem dependence and no oracle
> problem (P3), and it is ~30 lines in a code path evergreen is already opening. Its trigger is
> OQ-DF2's to rule, not this doc's.

### 5.2 A8 — Disable the vendor self-updater instead of cleaning up after it

The V axis exists because a program yolo installed then updates itself outside yolo's knowledge.
Turning that off collapses V to 1 at source.

**Why it is not the answer on its own.** It is in direct tension with the ruled evergreen policy:
[OQ-PD11/PD12](program-delivery.md#decision-ledger) make agent dependencies evergreen *on purpose*,
and the only mechanism that then updates them is the launcher's update arm — which is a *yolo* act
writing a new version dir, so V grows again, just under yolo's control instead of the vendor's. It
also has a live counterexample: `pi` ships no auto-updater at all
([`program-delivery.md`](program-delivery.md) §3.5), so a policy of "disable the updater" would leave
one agent permanently frozen while looking correct everywhere else.

> **Verdict: adopt as hygiene, reject as the V-axis answer.** Disabling *unmanaged* self-updates is
> right — it is what makes the reconcile's drift report meaningful — but it converts V's driver from
> the vendor to yolo rather than removing it. A7 is still needed.

### 5.3 A9 — One machine-global program prefix, shared into every jail

Instead of `~/.local`, `~/.npm-global` and `~/go` being per-workspace binds
(`internal/cli/run/assemble_parts.go:108-110`), a program prefix backed by machine-global storage,
shared by every jail on the machine. One copy per machine per version, structurally — no CAS, no
reference oracle, no reflink, and **[OQ-PD17](program-delivery.md#decision-ledger)
ceases to exist rather than being answered**.

**It is not a new sharing mechanism; it is the fourth instance of an existing one.** The same podman
argv already carries three machine-global mounts beside the three per-workspace ones
(`assemble_parts.go:105-161`, read 2026-09-04): `paths.GlobalHome()` at `/home/agent` `:ro`,
`paths.GlobalCache()` at `~/.cache` **read-write**, and `paths.GlobalMise()` at `/mise`
**read-write and mutated in place**. Pack manifests can already declare it: `KindState` takes
`scope: machine` (`internal/packdecl/kinds.go:82-85` — *"leaks across workspaces by design and is
review-worthy"*), `because` is required for it, and two packs use it today for credentials
(`packs/claude/pack.json:155-159`, `packs/agy/pack.json:92-96`). So the vocabulary, the storage root,
the mount pattern and the review discipline all exist.

**`macos-user` is the existence proof, and it works.** `SandboxHome()` is the constant
`/Users/_yolojail` (`internal/macosuser/macosuser.go:55-56`) with no workspace and no session
component; `SandboxPath` (`:456-470`) puts `~/.local/bin`, `~/.npm-global/bin` and `~/go/bin` on
PATH. Every workspace on that machine shares one set of installed programs, and
[`macos-user-home-tiers.md`](macos-user-home-tiers.md) catalogues three defects it causes —
**and none of the three is about installed programs.** They are: pack `state` dirs (histories,
sessions) machine-wide; the Seatbelt read-boundary leaking through the shared home; and a write-write
race on *composed content* (skills and briefings), which that doc calls *"qualitatively worse"*
because an agent can read a briefing for the wrong project. Program bytes are the one part of that
home whose sharing nobody has complained about. `backend-parity.md:295-297` refuses splitting the
home — and refuses it to protect **credentials**, not binaries.

**The per-workspace scope of `~/.local` is inherited, not chosen for agent CLIs — the history is
unusually clear.** On 2026-02-17 commit `98ea96eb` invented `<ws>/.yolo/home/` and *kept these exact
dirs global*: *"Shared tools (npm-global, go, mcp-wrappers) remain global."* Twenty-seven minutes
later `0616b590` reverted the whole change because *"each workspace started with an empty home"* broke
**auth** — nothing about tool versions — and the tools went per-workspace as collateral. Today's shape
is `2bcc4e76` (2026-04-07), which lists them as *"tools (.npm-global, .local, go)"* in the same bullet
as agent config dirs and generated configs; the benefit it claims is *"Remove flock serialization
(per-workspace overlays eliminate contention)"*. The one place a designer reasoned about `.local`'s
scope on purpose treats it as a fact to route around, not a property to defend
(`jail-state-separation-design.md` SS-1). **No test asserts that these three surfaces must be
per-workspace.** The two that touch them pin something else:
`TestHomeSurfacesPinsBothSpellings` (`internal/paths/paths_test.go:263-284`) pins the two *spellings*
of each pair because the mapping is not derivable, and `internal/cli/run/assemble_test.go:429` is a
golden-argv line. Delete the scope and neither goes red for the right reason.

**What it genuinely costs, and this is where it should be judged.**

- **Concurrency.** The claimed benefit of the per-workspace split is exactly this: *"two simultaneous
  launches write different directories and cannot collide"*
  ([`program-delivery.md`](program-delivery.md) §3.5). Sharing gives that up and needs the install-prefix
  lock that §3.5 already specifies for macos-user (never waits, never fails, proceeds without
  updating) — **unbuilt**. Worse, the repo's confidence here is partly unearned:
  `storage-and-config.md:163-165` asserts *"Concurrent startup is safe because jails don't share
  writable paths"*, which is already false for `~/.cache`, `/mise` and the two credential dirs; and
  `.yolo-entrypoint.lock` is mounted, touched and reserved in four places while **nothing in Go ever
  flocks it** — the guard existed in the Python CLI (`c007b09b`, 2026-04-05) and was lost in the port.
- **Blast radius.** A corrupt or half-written install breaks every workspace instead of one.
- **It ends per-workspace version pinning.** For agent dependencies that is aligned with the ruled
  policy (evergreen, no pin — [OQ-PD11](program-delivery.md#decision-ledger)); for anything in those
  prefixes that a project *should* pin it is a regression, which is an argument for sharing a
  narrower, program-only prefix rather than `~/.local` wholesale.
- **`~/.local` cannot be shared wholesale anyway.** It contains `~/.local/state` and
  `~/.local/share/yolo-jail` — the nested jail's own state dir (`paths.GlobalStorageRel()`,
  `internal/paths/paths.go:349`), which `capture` already has to exclude for the same reason. The
  shareable unit is a *program prefix*, not the XDG data dir.
- **It complicates capture rather than replacing it.** A capture needs a fresh, empty home; with a
  shared prefix the container backends would need the same fenced-staging trick macos-user's capture
  already implements (`internal/macosuser/seatbeltcapture.go:1-35`).

> **Verdict: not adopted, and not dismissed — costed and held open as
> [OQ-CP2](#-oq-cp2--should-the-program-prefix-become-machine-global).** It is the *only* option that
> collapses the N axis on every filesystem with no store, no oracle and no post-hoc sweep, and the
> earlier one-line dismissal (*"invents a second sharing mechanism that capture would then replace"*)
> is both factually wrong — it is the fourth instance of a shipped mechanism with a declared manifest
> vocabulary — and circular, since it assumes capture ships. What it actually costs is a lock that
> does not exist and a blast radius that does. **If capture ships as planned, this should be refused
> explicitly on those two grounds and written down**, so the next person does not re-derive it.

### 5.4 A10 — Trigger the hardlink dedup that already ships

Run `HardlinkDuplicateFiles` over the workspaces automatically (§3.1). Collapses the N axis
post-hoc, on every filesystem, with a kernel-maintained reference count.

**Its two real weaknesses.** It is **post-hoc**: every workspace still downloads and writes its own
copy first, so it saves steady-state disk but no bandwidth and no transient disk. And it costs I/O —
`sha256` of every file in the three surfaces of every workspace, behind a size prefilter
(`prune.go:132-140`); at 2.31 GiB of program bytes per workspace this is not free at N=30.

> **Verdict: adopt as the measurement, before adopting anything as the fix.** Whatever else happens,
> somebody should type `yolo prune` on a multi-workspace machine and report what the dedup line says.
> That number is the only honest input to *"how much is the N axis actually worth on a real
> machine"*, and it is currently unknown to everyone (P4). Its trigger is again OQ-DF2's.

### 5.5 A11 — Capture as designed, disk claim intact

The status quo of the plan: finish slices 5 and 6, keep the sequencing, keep the justification.

> **Verdict: rejected as a *justification*, adopted as a *mechanism*.** §7 prices what capture buys
> that nothing else does, and it is enough to keep building. What must go is the claim that it is the
> disk fix, and the ordering that claim purchased.

---

## 6. Across the user distribution — where each option is best and worst

Reasoning from one machine is what produced the original error, so: **N** is workspaces on the
machine, and the filesystem is whatever the user's home is on. Cells are machine-wide bytes for one
program, with V versions retained, in steady state.

| Option | reflink FS (btrfs / XFS `reflink=1` / ZFS) | **ext4** (Debian/Ubuntu default, every GitHub runner) | `macos-user` (APFS, no containers) |
| :--- | :--- | :--- | :--- |
| **Today** | `N·V·S` | `N·V·S` | `V·S` — already shares |
| **A7 prune only** | `N·S` | `N·S` | `S` |
| **A9 shared prefix only** | `V·S` | `V·S` | already this |
| **A10 dedup only** | `V·S`* | `V·S`* (hardlinks work everywhere) | n/a — one home already |
| **Capture only** | `S + N·(V−1)·S` | `(N+1)·V·S` — **worse than today** | `S + (V−1)·S` |
| **Capture + A7** | **`S`** | `2S` | `2S` |
| **A9 + A7** | **`S`** | **`S`** | **`S`** |
| **A10 + A7** | `S`* | `S`* | n/a |

\* after the sweep runs, and only for workspaces the enumeration can see and that share a mount.

Reading it by population:

- **N=2, any filesystem.** The N axis barely exists; the V axis is the whole bill. `N·V·S` at N=2 is
  2.39 GiB for claude alone, of which **1.99 GiB is stale versions**. **A7 alone is the win**, and
  capture's N-axis saving here is one copy of 204.7 MiB — 8 % of the total.
- **N=30, reflink.** The N axis is now 30 copies of the live version, **6.0 GiB**, and capture (or A9,
  or A10) earns its keep. This is the maintainer's own machine class and where the original number
  came from. **Best: capture + A7, or A9 + A7 — identical result.**
- **N=30, ext4.** Capture is the **worst** option on this row: it adds a copy and saves no disk at
  all. A9 and A10 both deliver the full N-axis collapse here and capture does not. **This is the row
  the current design has never been written down against**, and P2 says it must be.
- **CI (N=1 per job, ext4, cold every time).** No axis exists to collapse — one workspace, one
  version. Capture's value here is entirely the *download* saving, and only if a store survives
  between jobs, which on a GitHub runner it does not.
- **`macos-user`, any N.** Already shares the prefix, so the N axis is already collapsed and A9 is a
  no-op. **Only A7 buys anything**, and capture's disk value on this backend is a strict `+S`. Its
  *other* values (manifest, relocation, offline) are undiminished — and slice 6 landed only the
  recording half, so the paying half is not there anyway.

**The distribution-level statement, which is the one the doc owes:** the V axis is worth more than
the N axis at every N below roughly V, on every filesystem, on every backend — and it is the axis with
no owner. The N axis is worth a great deal at high N on reflink filesystems and *negative* at any N
on ext4 through capture specifically.

---

## 7. What capture buys that pruning and sharing do not

Priced honestly, because this is where the verdict comes from.

### 7.1 The four things only capture gives

1. **A manifest — the install becomes observable.** A capture records what the installer wrote, path
   by path. Today reconcile compares a single file's digest
   (`internal/entrypoint/reconcile.go:218`). This is the direct answer to the doc's own title question
   (*"what makes two jails the same"*) for the one class that has no lockfile to borrow, and neither
   prune nor sharing produces a byte of it.
2. **Offline, deterministic materialize.** A new workspace gets an agent with no network and gets
   *the same bytes*, not `@latest` on a different day — which is
   [`program-delivery.md`](program-delivery.md) §4.1's freeze defect *solved* rather than frozen
   differently. Sharing gets the bytes without the record; prune gets neither.
3. **Drift becomes reportable.** Because there is an immutable reference tree, *"the vendor updated
   itself under you"* is a statement the reconcile can make. Under prune or sharing there is nothing
   to compare against, so a self-update is invisible by construction.
4. **A lockable identity for a class that has no lockfile.** [OQ-PD9](program-delivery.md#decision-ledger)
   says yolo writes its own record only where no native one exists; the installer class is that gap,
   and the capture hash is the identity that fills it.

**None of those is a disk property**, and three of them are exactly what
[`program-delivery.md`](program-delivery.md) §2 says the whole doc is for. That is why my verdict is
*keep capture* rather than *replace it*: the mechanism was chosen for the right reasons and then sold
on the wrong one.

### 7.2 The two things it was credited with and does not deliver

1. **"The per-workspace refetch cost dies."** Half true: the *download* dies for the captured
   version, which is real. The *disk* cost dies only on reflink filesystems, and only for the cold
   install — every subsequent version arrives through an arm with no materialize branch (§3(a)).
2. **"Under capture there is nothing to prune."** False. The self-updater — or, under evergreen,
   yolo's own update arm — writes full-size version dirs into the workspace regardless of where the
   first one came from. This is the load-bearing sentence in
   [OQ-PD15](program-delivery.md#decision-ledger)'s ruling, and it is the one I would ask to be
   retracted.

---

## 8. What happens to OQ-PD15 and OQ-PD17, per option

| Option | OQ-PD15 (capture before evergreen) | OQ-PD17 (the unreferenced oracle) |
| :--- | :--- | :--- |
| **A7 prune** | **Dissolves the ordering.** Its premise was that evergreen multiplies a cost capture removes; A7 removes that cost directly, so evergreen can ship first. | Untouched — A7 creates no store. |
| **A9 shared prefix** | Dissolves it — the N axis is collapsed structurally, no store to build first. | **Ceases to exist.** One prefix, one copy, no store, no referrers to count. |
| **A10 dedup** | Same: the N axis gets an answer that does not gate evergreen. | Untouched, and it demonstrates the contrast — dedup's oracle is `st_nlink`, kept by the kernel, which is precisely what the CAS gave up. |
| **Capture as designed** | Stands as ruled — but on premises §3 and §7.2 show to be false. | Stands, and blocks slice 5. |
| **My recommendation (§9)** | **Reverse it.** Evergreen ships next; capture continues in parallel on its own merits. | **Answer it "no oracle — bound by policy"** (§4.2): keep-newest-K per (bin, platform) plus an age floor, on the measured ground that reclaiming an entry is never unsafe on any arm. |

**The cost of the current ordering, stated plainly.** Evergreen is the fix for the defect that
started all of this: every agent CLI in this jail was six weeks stale on 2026-09-03, and
[OQ-PD15](program-delivery.md#decision-ledger) knowingly carries that (*"the freeze is still a live
defect for as long as capture takes"*). What is left of capture is slice 5, **blocked on
OQ-PD17**, and slice 6's second half — the relocation rewrite, hand-off H2 — which is unbuilt and
**cannot be verified in this jail at all**, because that backend needs Seatbelt on real hardware. So
the ordering currently trades a live, fleet-wide staleness defect against a remainder with an open
blocker and a hardware gate, in exchange for a disk saving that is 16.7 % of the number it was
justified by and negative on ext4.
That trade should be re-made with the corrected numbers on the table.

---

## 9. What I would build, in order

**First, the V-axis prune, inside evergreen.** Keep-newest-2 version directories per program,
executed by the act that installed the new one, in the workspace it installed into. No enumeration,
no store, no oracle. Reclaims 1018.6 of 1223.4 measured MiB per workspace at `K = 1`, on every
filesystem and every backend. It is small enough to be a slice of the evergreen plan rather than a
plan of its own, and its trigger placement is
[`minimal-disk-footprint.md`](minimal-disk-footprint.md) **OQ-DF2's option (i)** — the write path —
which is that doc's own leaning and should be ruled there, not here.

**Second, evergreen.** Unblocked by the first step, and the reason the first step is worth doing now:
under evergreen, V grows on a schedule instead of by accident, so the prune's value goes from one-off
to recurring — which is the argument OQ-PD15 made in the opposite direction.

**Third, type `yolo prune` on a real multi-workspace machine and report the dedup line.** The N axis
has never been measured across workspaces by anybody. That single number decides how much the
remaining options are worth, and it costs one command (P4).

**Fourth, finish capture on its own merits, at its own pace.** The manifest, the offline materialize
and the drift report are worth building and are not in a race with anything. Slice 5 becomes small
under §4.2's reading: keep-newest-K per (bin, platform) plus an age floor, no oracle, and a store
sweep that is safe to be wrong about. Slice 6's materialize half stays hardware-gated.

**Not sequenced, deliberately: the shared prefix.** It is the cleanest answer to the N axis on paper
and the one with the most unbuilt safety work under it (a prefix lock that does not exist, and an
entrypoint lock the repo believes it has and does not). It should be ruled — adopted or refused with
reasons — before anyone builds a *fifth* N-axis mechanism, and not before capture has shipped what it
is actually for.

---

## 10. What this does NOT cover

- **The capture design itself.** Layout, admit, manifests, relocation, the Seatbelt capture profile:
  [`program-delivery.md`](program-delivery.md) §6.3 and
  [`../plans/install-capture.md`](../plans/install-capture.md). This doc questions capture's
  *justification* and *sequencing*, never its shape, and proposes reverting none of the four landed
  slices.
- **Where an automatic reclaimer lives.** [`minimal-disk-footprint.md`](minimal-disk-footprint.md)
  OQ-DF2 owns the trigger question for every reclaimer yolo has. §9's first step names its answer as
  that OQ's option (i); it does not rule it.
- **The image ledgers.** Cache tars, the nix store closure and podman's image store are that same
  doc's §3, and they remain the larger line item by a wide margin — the host image-tar cache alone
  measured **11 GiB** here on 2026-09-04, against 2.31 GiB of program bytes in this workspace.
  Nothing in this doc changes a byte of them.
- **Whether the home tier model should change generally.** [`jail-home.md`](jail-home.md) owns the
  tiers; §5.3 asks only about the three program surfaces, and explicitly *not* about pack `state`
  dirs, credentials, `~/.config` or `~/.ssh`, whose per-workspace scope is load-bearing for reasons
  this doc does not dispute.
- **`macos-user`'s three recorded home-tier defects.** [`macos-user-home-tiers.md`](macos-user-home-tiers.md)
  OQ-HT-1/2/3. §5.3 cites that backend as an existence proof for sharing *programs* and takes no
  position on sharing *state*, which is what those defects are about.
- **Trust and provenance.** A capture records what you got, not that a publisher signed it;
  `trust-paths.md` owns that, and sharing a prefix does not change it.
- **Any claim about the population of filesystems.** §4.1's table says what happens on each; it does
  not say how many users are on which, because nobody knows.

---

## 11. Risks

| # | Risk | Mitigation |
| :--- | :--- | :--- |
| R1 | **This doc is wrong about the split, because it measured one workspace.** N=1 here, so the N axis is inferred from the bind layout rather than observed across workspaces. | The V-axis figure (83.3 %) is measured directly and does not depend on N. The N-axis claim depends only on `assemble_parts.go:108-110` being a per-workspace bind, which is read from code. §9's third step is exactly the missing measurement, and it is one command. |
| R2 | **Retracting the disk justification reads as retracting capture.** Four slices are landed and a reader skimming §1 could conclude the work was wasted. | §7.1 states the four things only capture buys, and §1 and §9 both say keep building. No landed slice is proposed for reversion. |
| R3 | **Sunk cost inverted — overturning a ruling for the pleasure of overturning it.** The corrected number is a reason to re-decide, not a reason to decide the other way. | The verdict keeps the mechanism and changes only the two claims that are measurably false (§7.2). OQ-PD10 stands; only OQ-PD15's ordering and OQ-PD10's disk sentence move. |
| R4 | **Landing evergreen before capture ships the recurring-disk regression OQ-PD15 exists to avoid.** | Only if the V-axis prune does not land with it — which is why §9 orders the prune *first* and inside the same plan, not after. Without the prune, R4 is real and OQ-PD15 was right. |
| R5 | **A delete-on-success prune deletes a version something else is using.** A second jail on the same workspace, a running agent process holding the old binary open. | The rule is per-workspace and keeps `K ≥ 2`, so the live symlink target and one predecessor always survive; a running process holds an open fd and survives an unlink on POSIX regardless. The one case to state is a *concurrent launch on the same workspace*, which is [roadmap](../plans/roadmap.md)'s unfiled shim-dir race and needs the same guard. |
| R6 | **Triggering the hardlink dedup automatically arms a hazard nobody has reviewed.** It creates cross-workspace shared inodes with no admit-time freeze (§3.1's note). | Measure first (§9 step three is a dry run, which mutates nothing), and give the dedup capture's read-only freeze before giving it a trigger. |
| R7 | **A shared prefix ships on a concurrency guarantee that does not exist.** The install-prefix lock is specified and unbuilt, `storage-and-config.md:163-165` overstates today's isolation, and `.yolo-entrypoint.lock` is mounted but never flocked. | Do not adopt A9 before the lock exists. Named as [OQ-CP2](#-oq-cp2--should-the-program-prefix-become-machine-global) rather than sequenced. The two documentation defects are worth fixing on their own. |
| R8 | **Filesystem support is asserted, not measured.** Only btrfs was available here. | Stated as NOT MEASURED at the point of use (§4.1). The recommendation is deliberately the one that does not depend on the answer. |

---

## 12. Open Questions

### 💬 OQ-CP1 — is the disk justification retracted, and is OQ-PD15 reversed?

The load-bearing question; everything else is downstream. [OQ-PD15](program-delivery.md#decision-ledger)
sequenced capture ahead of evergreen on two premises this doc measures as false: that evergreen
multiplies the cost capture removes (it multiplies V; capture collapses N — §3(a)), and that *"under
capture there is nothing to prune"* (the self-updater and, later, yolo's own update arm keep writing
full-size version dirs into the workspace — §7.2). **What it decides:** whether the six-week fleet
staleness keeps waiting on a remainder with an open blocker (slice 5) and a hardware gate (slice 6's
relocation half).

_Leaning:_ **Reverse it — evergreen next, with the V-axis prune inside it; capture continues in
parallel.** I hold this firmly on the numbers and loosely on the calendar: if capture's remaining
slices are days rather than weeks, the ordering costs little and the ruling's *"sooner was never the
goal"* still applies. What should not survive either way is the **claim** — the ledger row and §6.3
should stop saying capture is the disk fix, whatever order the work lands in.

**Answer:**
> _(empty — fill in when decided)_

### 💬 OQ-CP2 — should the program prefix become machine-global?

The N axis has four possible answers (capture, dedup, sharing, nothing) and sharing is the only one
that needs no store, no oracle, no sweep and no reflink — and the only one that works identically on
ext4 and btrfs. It is a shipped pattern (three machine-global mounts in the same argv; `scope:
machine` in the manifest vocabulary) and a shipped *backend* (`macos-user`, whose three recorded
sharing defects are all about state, none about programs). Its per-workspace scope was inherited from
a 2026-02-17 auth revert, not chosen for agent CLIs. **What it decides:** whether yolo builds a
fourth N-axis mechanism or reuses the tier it already has. **What it costs:** an install-prefix lock
that does not exist, a wider blast radius, and a narrower shareable unit than `~/.local`
(§5.3).

_Leaning:_ **Refuse it — but refuse it explicitly and in writing, on the lock and the blast radius,
not on "capture would replace it."** The concurrency guarantee the per-workspace split provides is
real and currently free; buying it back costs a lock nobody has written, and the repo has just been
shown to be wrong about its own locking twice. If capture's manifest and offline materialize are
worth having anyway, sharing's marginal win over capture is limited to the ext4 row — which is a real
row, which is why this is a question and not a footnote.

**Answer:**
> _(empty — fill in when decided)_

### ✅ OQ-CP3 — is OQ-PD17 answered by "no oracle, bound by policy"? — RESOLVED (2026-09-04)

[OQ-PD17](program-delivery.md#decision-ledger)
asks which of three candidates supplies an *unreferenced* oracle for a capture entry, and states that
until it is answered *"entries accumulate with no way to remove them."* MEASURED 2026-09-04 (§4.2): a
reflinked destination survives its source's deletion with identical content, so **reclaiming an entry
is safe on all three materialize arms** — the only cost is a re-capture. That makes the question an
efficiency question with a policy answer, not a correctness question needing an oracle. **What it
decides:** whether slice 5 is blocked at all. **The honest counter:** a re-capture is not free — it is
a download plus an installer run plus a throwaway jail — so a policy that reaps too eagerly is a
real, if bounded, cost.

_Leaning:_ **Yes — answer OQ-PD17 with "no unreferenced oracle; bound by keep-newest-K per (bin,
platform) plus an age floor", the two idioms the plan already calls safe under any oracle.** Both are
policy, both are local to the store, neither claims anything about referrers. This is a ruling I
would want the capture author to sanity-check rather than one I would land unilaterally — it is their
question and my measurement.

**Answer:**
> **Yes — and the sanity-check the leaning asked for came back sharper than the leaning.**
> Ruled 2026-09-04 as [OQ-PD17](program-delivery.md#decision-ledger).
>
> The measurement stands: reclaiming is safe on all three arms, so this is a policy question. But
> *keep-newest-K plus an age floor* is still a policy invented for the store, and the store already
> has one. `resolveCaptureFor` (`internal/cli/capturematerialize.go:183`) selects **newest by receipt
> time per (bin, platform)**, so **the reap rule is the complement of the resolver**: delete every
> entry the resolver would not select. Derived from the reader rather than agreed with it, so the two
> cannot drift.
>
> Both of the leaning's idioms fall out of that, and neither survives:
>
> - **`K = 1`, not `K`.** The rollback target has nowhere to be used — the store is not a version
>   history, and a materialized older version is updated by evergreen within `UPDATE_INTERVAL`
>   anyway. Rollback lives on the V axis, in the vendor's own version dir, which is
>   [A7](#51-a7--prune-stale-versions-executed-by-whoever-installed-the-new-one)'s job.
> - **No age floor.** It guarded an in-flight window the completion marker already covers, and the
>   one real race — GC unlinking an entry mid-materialize — is not fixed by it and needs no fix,
>   because a failed materialize is a miss and a miss falls through to the installer.
>
> **The honest counter in this question survives and is answered.** A re-capture is genuinely not
> free, so eager reaping has a real cost — but the complement rule never reaps anything the resolver
> could have returned, which is the tightest possible bound on that cost. And keeping each reaped
> entry's `capture-manifest.json` (it sits beside `tree/`, not in it) preserves drift comparison for
> kilobytes.
>
> **What ruling it surfaced.** Every line above assumes the store has entries. It has none, anywhere:
> `yolo capture` is its only writer and no launch path calls it — filed as
> [OQ-PD18](program-delivery.md#decision-ledger), which is the question
> that decides whether any of this runs.

### 💬 OQ-CP4 — does an evergreen update get to materialize from the store?

Capture's materialize branch is reachable only from the cold-install arm
(`_try_materialize`, `internal/entrypoint/shims.go:1009-1017`, called from `_do_install` at `:1024`;
`_do_install` itself is called at `:1084-1085` under `if [ ! -x "$REAL_BIN" ]`). Under evergreen, every *subsequent* version arrives through the update
arm, which has no materialize path — so the store serves the first install of each workspace and
nothing after it, and its saving dilutes toward zero as updates accumulate (§3(a)). Closing that
would need a new capture per vendor release, but `yolo capture` is a **host act** and slice 4(f)
deliberately gives the capture jail no store mount, so a launcher cannot trigger one. **What it
decides:** whether capture's N-axis saving is one-off or recurring — i.e. whether §6's `S + N·(V−1)·S`
row ever improves.

_Leaning:_ **Leave it one-off, and say so.** An automatic re-capture on every vendor release is a
host-side scheduled act running third-party installers unattended, which is a much larger trust and
lifecycle surface than anything capture has today — and it would make the store grow on the vendor's
cadence, reintroducing the retention problem at the machine tier. Better to accept that capture
serves the cold install, let the V-axis prune handle the rest, and write the limitation into §6.3's
*"what it buys"* list beside the honest limits already there.

**Answer:**
> _(empty — fill in when decided)_

---

## Appendix — corrections to sibling docs found while writing this

Small, cheap, and each one a claim about code that a reader would stop checking at. Recorded rather
than fixed, because this doc commits only itself.

| Where | Claim | Correction |
| :--- | :--- | :--- |
| [`program-delivery.md`](program-delivery.md) §6.3; `internal/macosuser/seatbeltcapture.go:12` | The macos-user shared home is *"a refused design point (`internal/cli/run/run.go:235-250`)"* | **That range is the config-change-approval block** (read 2026-09-04) and says nothing about a home. §6.3 already corrected this citation once, from `:156-159`, and landed on a second wrong range. The refusal is at **`run.go:272-287`** (*"Splitting the home would break the MACHINE tier … the single home IS the shared-credentials mechanism"*), with a second statement at `backend-parity.md:295-297`. |
| [`storage-and-config.md`](storage-and-config.md):163-165 | *"Concurrent startup is safe because jails don't share writable paths."* | False as written: `~/.cache` (`GlobalCache`), `/mise` (`GlobalMise`) and the two `scope: machine` credential dirs are all shared and writable (`assemble_parts.go:120,156-161`; `packs/claude/pack.json:155-159`). |
| `.yolo-entrypoint.lock` | Mounted (`assemble_parts.go:138`), touched (`prepare.go:352`), reserved (`storage/ensure.go:32`, `config/writablehome.go:73`) | **Nothing in Go ever flocks it.** The serialization it names existed in the Python CLI (`c007b09b`, 2026-04-05, *"rmtree+recreate races caused FileNotFoundError"*) and was lost in the port. Its scope is now per-workspace, which could not serialize two different workspaces in any case. Relevant to R7 and to the roadmap's unfiled shim-dir race. |
