---
title: "What a fetched pack may execute, and what you are agreeing to"
date: 2026-08-17
status: in-review
tags: [packs, trust, approval, security]
summary: "SUPERSEDED IN PART. The proposal — replace the mechanism list with one property, a fetched pack may execute only content it pins — rests on a premise that proved false. §5 and OQ-X1 are live, §6 is ruled and unbuilt, §3 and §4 are retired; the supersession map in the header says which is which."
---

# What a fetched pack may execute, and what you are agreeing to

**Status:** SUPERSEDED IN PART, 2026-08-17 — **supersession map and every code anchor re-verified
2026-08-23.** §5 and OQ-X1 are live; §6 is RULED and still unbuilt; §3's premise is FALSE and §4's
table is retired. **Read the map below before trusting any section of this document**, because
"superseded in part" without saying which part is worse than no warning at all.

> [!CAUTION]
> **This document's central premise is FALSE, established by the inventory it provoked.** §3 says the
> commit pin "is the rule already applied one level up". It is applied **nowhere**:
> `LockEntry.Commit` has four readers, all display-only, and the launch path re-resolves the
> *config's* ref against the local mirror instead. **Still true, verified 2026-08-23**: the field is
> declared at [`lock.go:39-44`](../../internal/packsrc/lock.go) and every reader prints —
> [`pack.go:1110-1112`](../../internal/cli/pack.go) (the moved-pin message) and
> [`pack.go:1335-1338`](../../internal/cli/pack.go) (the `pack status` listing). Read
> [`trust-paths.md`](./trust-paths.md) first — it enumerates all 25 paths, finds that pinning changes
> an outcome in **three** of them, and surfaced a verified hole that outranked this proposal
> entirely: the origin gate this document generalizes **was not enforced in the jail**. *That hole is
> now closed* — OQ-TP6 made a refused contribution refuse the launch (built 2026-08-18, `6385dfbb`) —
> but it was closed by deleting the split, not by adopting P1.
>
> Written from a review challenge that turned out to be right: *"a fetched pack can't introduce an
> installer — that means you couldn't use a pack in git to install an agent? don't we allow native
> binary downloads? why disallow an installer at that point?"*

**The short version.** Today the origin gate refuses one **mechanism** (`curl | sh`) and permits
another (`npm install -g`) that is equally arbitrary code in the jail. The containment argument
cannot separate them, because both run in the same place. The property that actually separates safe
from unsafe is **pinning**, and this repo just ruled on it one level up: an approval binds to a
*specific commit*. Apply the same rule one level down — **a fetched pack may cause execution only of
content it pins** — and the mechanism list disappears. *That last clause is the sentence that turned
out to be false; see §3.*

### Which parts to trust — the supersession map

| § | What it claims | Status as of 2026-08-23 |
| :--- | :--- | :--- |
| **§1** | the origin gate covers `reads-host`, `mount`, host-prepending `briefing` and `program via installer`; `via: npm` is ungated | ✅ **authoritative** — re-verified today at [`contributes.go:632-646`](../../internal/packdecl/contributes.go) and [`contributes.go:30`](../../internal/packdecl/contributes.go). One thing moved: a refusal is now **fatal**, not a warning (OQ-TP6) |
| **§2** | `npm install -g` runs arbitrary code too, so containment cannot separate the two | ✅ **the observation stands** — but the asymmetry is now a **decided** one rather than an oversight. See the note inside §2 |
| **§3** | P1: a fetched pack may cause execution only of content it pins — *"the rule already applied one level up"* | ⚠️ **premise FALSE, shape survives.** No commit pin is enforced anywhere; P1 itself is neither adopted nor rejected |
| **§4** | the permit/refuse table | ❌ **retired.** Its live-hole row was closed a different way (OQ-TP5, `b3a29ad8`); the rest are unbuilt proposals. Kept as documentation, annotated in place |
| **§5** | a digest-pinned installer script is not a digest-pinned binary | ✅ **authoritative and live** — untouched by anything since, and it is what OQ-X1 turns on |
| **§6** | approval prose must say what is touched, in which direction, on whose machine | ✅ **authoritative, RULED, NOT BUILT** — claims still render as terse tokens ([`contributes.go:655-671`](../../internal/packdecl/contributes.go), verified 2026-08-23) |
| **§7** | four things this does not license | ✅ **three of four hold**; the *"not a new lockfile"* bullet is refuted — see the warning there |

**Reads with:** [`trust-paths.md`](./trust-paths.md) (**read this first** — the inventory that
superseded §3 and §4, and where OQ-TP3/TP4/TP7 live) ·
[`program-delivery.md`](./program-delivery.md) (restates the venue question at wider scope than the
pack system) · [`pack-system.md`](./pack-system.md) (§9, the origin gate) ·
[`loophole-packaging-overview.md`](./loophole-packaging-overview.md) (§4, the four gates) ·
[`broker-as-a-pack.md`](./broker-as-a-pack.md) (§3.1 designs pack-shipped binaries, which is the
other half of this question) · [`gate-placement-principle.md`](./gate-placement-principle.md).

---

## 1. What the gate does today

`NeedsHostAccessContributions` (`internal/packdecl/contributes.go:632-645`) origin-gates exactly four
things: `reads-host`, `mount`, a host-prepending `briefing`, and — the one at issue —
`KindProgram && Via == "installer"`.

A `program` contribution has exactly two forms (`contributes.go:30`):

| form | what it does | origin-gated? |
| :--- | :--- | :--- |
| `via: "installer"` | fetches a URL and runs it as a shell script | ✅ **refused to a fetched pack** |
| `via: "npm"` | `npm install -g <package>` | ❌ **not gated at all** |

The stated rationale is in the schema: an installer URL is *"the sharpest thing a manifest can name: a
URL whose contents run as a shell script … a fetched pack cannot introduce one, because that would
let a git ref execute arbitrary code in the jail."*

> [!NOTE]
> **One thing in this section has moved since it was written: the refusal is now FATAL.** When this
> was drafted the host computed the refusal, printed a warning, and staged the pack anyway — so the
> table's ✅ was true of the *decision* and false of the *outcome*. `stagePacks` now returns
> `refusedLaunchError` and no jail starts (OQ-TP6, built 2026-08-18 `6385dfbb`; verified still in the
> tree 2026-08-23 at [`run/packs.go:229,248`](../../internal/cli/run/packs.go) and
> [`packrefusal.go:104-119`](../../internal/cli/run/packrefusal.go)). The gate's **membership** —
> which four things it covers — is unchanged.

---

## 2. Why that rationale does not hold

> [!WARNING]
> **`npm install -g` runs arbitrary code in the jail too.** npm executes `postinstall` scripts from
> the package. So the gate refuses one path to arbitrary in-jail execution and permits another, from
> the same fetched pack, with no stated reason for the difference. That is the same shape as the
> `host_bind_mounts` path rule that was withdrawn on 2026-08-17 for admitting `~/.ssh` while refusing
> a pulse socket: **two cases that should be treated alike, decided oppositely.**

**And "the binary lives in the jail" cannot be the discriminator**, which was the natural guess. It
applies equally to an npm package, an installer script, and a pack-shipped jail-side binary — all
three execute inside the sandbox. Containment is a property of *where*, and all three are the same
*where*.

**Nor is "we already allow native binary downloads" a contradiction to explain away** — it is the
clue. [`broker-as-a-pack.md`](./broker-as-a-pack.md) §3.1 designs pack-shipped binaries and makes a
`sha256` **mandatory**, with this reasoning: the lockfile's commit pins the pack's tree, a downloaded
artifact is not in that tree, so without a digest "pinned pack" silently means "pinned manifest,
unpinned executable". That is the whole distinction, and it has nothing to do with mechanism.

> [!IMPORTANT]
> **The asymmetry this section calls an oversight was RULED a deliberate one on 2026-08-18, and this
> document lost that argument.** [`trust-paths.md`](./trust-paths.md) §3.1 keeps both halves on
> purpose: *"an npm install names a registry package and is not origin-gated — it is the same trust
> as any dependency the user already installs"*, and the gate *"is about `curl | sh` specifically,
> not about installing things."* The ruling's own reason for the split is not containment (which §2
> correctly demolishes) but **when the bytes change**, not whose they are — and that half was
> answered separately by OQ-TP5 removing the evergreen npm poll.
>
> So §2's *observation* is still correct and worth keeping — a reader who re-derives *"npm runs
> postinstall, therefore the gate is inconsistent"* has found a real fact — but it is no longer an
> open finding. Two cases that look alike were decided oppositely **with a stated reason**, which is
> exactly what the `host_bind_mounts` withdrawal it compares itself to did not have.

---

## 3. The principle

> **P1. A fetched pack may cause execution only of content it pins.**
>
> Not *which tool* fetches it. Not *where* it runs. Whether what executes is determined by what you
> approved.

This is the same rule the maintainer just applied one level up — an approval binds to a specific
commit, so a git repo cannot change under you after you said yes. P1 is that rule applied to the
things a pack pulls in from outside its own tree.

> [!WARNING]
> **That justification is FALSE, and it is the sentence the whole section rests on.** The commit
> approval is *recorded* and never *consulted*: `LockEntry.Commit` has four readers and all four
> print ([`pack.go:1110-1112`](../../internal/cli/pack.go),
> [`pack.go:1335-1338`](../../internal/cli/pack.go)), while the launch path re-resolves the
> **config's** `?ref=` against the local mirror. Verified 2026-08-18 by
> [`trust-paths.md`](./trust-paths.md) §1 and re-verified 2026-08-23; the gap is tracked there as
> **OQ-LP8 / G2b**. So P1 is not "the same rule one level down" — there is no rule one level up yet,
> and adopting P1 would mean building the enforcement it claims to inherit.
>
> **What survives is the SHAPE.** Content-addressing really is the only answer to *"is this the same
> code I looked at?"*, and that much is endorsed by `trust-paths.md` §4. What does not survive is the
> claim that adopting it is free because the precedent exists.

> [!NOTE]
> **Two different `P1`s, and a sibling doc cites one of them by that name.** This document's P1 is
> *"a fetched pack may cause execution only of content it pins."* [`trust-paths.md`](./trust-paths.md)
> §1 later introduced its own, unrelated **P1 — *"trust flows DOWNWARD"***. When `trust-paths.md` §4
> says *"P1's shape is right"*, it means **this** one. Neither was renumbered, because both are cited
> as written.

**Why pinning is the right axis and containment is not:** the jail already bounds the *blast radius*
of anything that runs in it, equally for all three forms. What the jail cannot do is tell you that
the thing running today is the thing you reviewed. Only a pin does that.

---

## 4. What P1 permits and refuses — RETIRED, kept as documentation

> [!CAUTION]
> **This table is retired and must not be implemented from as written.** It is kept because two of
> its rows record objections that were investigated and are expensive to re-derive. Row by row, as of
> 2026-08-23:
>
> - **Row 3 (floating `npm` → refuse) is the one row that named a live hole, and the hole was closed
>   by a different mechanism.** OQ-TP5 ruled *no evergreen npm* on 2026-08-18 (built `b3a29ad8`) —
>   `install` obeys what is recorded, `yolo pack update` is the only act that resolves a version, and
>   the hourly poll only reports. That deletes the *silent change*, which was the danger; it does
>   **not** refuse the floating declaration, which is what this row proposed. Anyone re-proposing the
>   refusal is proposing something new, not restoring this.
> - **Row 2's blocking objection is spent, and the row is still not taken.** The table's top rows were
>   written off as inexpressible — the launcher appended `@latest` unconditionally, so `foo@1.2.3`
>   yielded `foo@1.2.3@latest`. **Fixed 2026-08-17** (`65f14342`): the launcher splits the declaration
>   and honours a version, dist-tag or range
>   ([`npmspec.go:62-70`](../../internal/entrypoint/npmspec.go), verified 2026-08-23). A version is
>   **expressible and nothing takes it** — every shipped pack still declares a bare name, and an
>   unversioned declaration still resolves to `@latest` at `npmspec.go:62-66`. Whether a pack should
>   be *required* to pin is [`trust-paths.md`](./trust-paths.md) OQ-TP3; where an embedded pack's pin
>   would even live is OQ-TP4. Neither is this table's to answer.
> - **Rows 4 and 5 are the gate that already exists** (§1), now fatal rather than warned.
> - **Row 1 is a proposal in a different document** — [`broker-as-a-pack.md`](./broker-as-a-pack.md)
>   §3.1 — and nothing here advanced it.

| declaration | pinned? | verdict under P1 | today |
| :--- | :--- | :--- | :--- |
| jail-side binary + mandatory `sha256` | ✅ fully — the digest **is** what runs | ✅ allow | (proposed in broker-as-a-pack §3.1) |
| `npm` with a pinned version **and** integrity | ✅ the registry resolves to fixed bytes | ✅ allow | allowed, unpinned |
| `npm install -g <pkg>` (floating, resolves to latest) | ❌ resolves at run time | ❌ **refuse** — closes a live hole | **allowed** |
| installer URL + `sha256` | ⚠️ the *script* is pinned; what the script downloads is not | ⚠️ **OQ-X1** | refused |
| installer URL, no digest | ❌ contents can change at any moment | ❌ refuse | refused |

**Two changes fall out, and they point in opposite directions** — which is the sign the rule is doing
work rather than rationalising:

- It **closes** a hole: floating `npm` from a fetched pack stops being permitted.
- It **opens** the case the review asked about: a pack in git *can* install an agent, provided what it
  installs is pinned. The answer to *"couldn't you use a pack in git to install an agent?"* is
  **you can today** — via `npm`, which is ungated — and under P1 you still can, but honestly.

---

## 5. The shallow-pin problem

An installer script pinned by digest gives you *"the recipe is pinned"*, not *"what runs is pinned"*:
the script itself fetches more at run time, and those fetches are unpinned. A binary artifact has no
such second hop — the digest covers exactly what executes.

So the two are **not** equivalent even when both carry a digest, and any rule that treats them as
equivalent is overselling the installer case. That is OQ-X1, and it is the only genuinely open
question here.

---

## 6. Approval must be readable — RULED, and still unbuilt

*"Print the claims at approval time, but they need to be understandable by a new user."*

**This is the one ruling in this document that nothing has superseded, and nothing has implemented
either.** Verified 2026-08-23: claims still render as terse tokens, one line each, from
[`HostAccessClaims`](../../internal/packdecl/contributes.go) at `contributes.go:655-671` — and that
is the string the approval prompt prints ([`pack.go:1224-1227`](../../internal/cli/pack.go)), the
string the lockfile records, and the string the launch gate compares. Exactly as below:

```text
reads-host .claude/settings.json
mount /home/you/notes -> /ctx/notes
installer https://example.com/install.sh
briefing .claude/CLAUDE.md
```

A new user cannot act on that. `reads-host` is jargon, the path is relative to an unstated root, and
nothing says what the pack will *do* with it or what the risk is. The approval prompt must render
each claim as a sentence naming **what is touched, in which direction, and on whose machine** — for
example:

```text
This pack wants to:
  • READ a file from YOUR HOME on this machine:  ~/.claude/settings.json
  • SHOW a folder from YOUR MACHINE to the jail: ~/notes  (read-only, as /ctx/notes)
  • RUN an installer downloaded from the internet inside the jail:
      https://example.com/install.sh   (pinned: sha256 3f9a…)
```

**Requirements this creates**, so the work is not merely "reword it":

- The underlying claim string stays the machine-comparable identity (it is what `HostAccessApproved`
  compares, and what the lockfile records). The prose is a **rendering**, never the record — two
  claims that differ must never render identically.
- Every claim says whose machine, because "host" means nothing to a new user.
- A pinned thing shows its pin; an unpinned thing must say so in words, since after P1 that is the
  distinction that decides whether it is allowed at all.

---

## 7. What this does not license

- **Not** a change for `file://` packs. A local pack is your own files: you edit them, that is your
  business, and nothing here gates them.
- **Not** a second gate over host *access*. P1 is about execution of fetched content. Reading your
  host home is still governed by the origin gate and its approval.
- **Not** a new lockfile. `packsrc.LockEntry` already records the resolved commit; the digests P1
  needs are declared in the manifest, which the commit already pins.

  > [!WARNING]
  > **This bullet is REFUTED and the correction is load-bearing.** The lockfile exists **per fetched
  > pack** and has nowhere to record a package version: `LockEntry`'s fields are entirely about a
  > *git* pin (`Name`, `Source`, `Commit`, `Ref`, `ApprovedHostAccess` —
  > [`lock.go:33-53`](../../internal/packsrc/lock.go), verified 2026-08-23), `Commit` is documented
  > *"empty for a local pack"*, and an embedded pack has no row at all. The four packs that declare
  > npm programs — **pi, copilot, codex, opencode** — are all embedded. So "no new lockfile" holds
  > only for digests declared inside a pack's own tree; the moment a pin has to describe something
  > the tree does not contain, there is no home for it. That is
  > [`trust-paths.md`](./trust-paths.md) **OQ-TP4**, and
  > [`program-delivery.md`](./program-delivery.md) argues the venue question is not the pack system's
  > alone.
- **Not** a claim that pinning makes code safe. A pinned malicious binary is still malicious. Pinning
  makes approval *meaningful*; it does not make the thing trustworthy.

---

## Decision Ledger

**This document owns exactly one OQ ID — `OQ-X1` — and it is still open.** Everything else that
settled about this proposal was settled *elsewhere*, so those rows carry the ID of the question that
settled them; rows ruled inside this document are identified by their **section**, because minting an
`OQ-` namespace here would create IDs that nothing in the tree cites.

| ID | Ruling / Decision | Date | Settled in |
| :--- | :--- | :--- | :--- |
| **§6** | Approval prose must name **what is touched, in which direction, and on whose machine**; the claim string stays the machine-comparable record and the prose is only a rendering. **Ruled, not built** | 2026-08-17 | [§6](#6-approval-must-be-readable--ruled-and-still-unbuilt) |
| **§4 row 2** | The launcher's unconditional `@latest` append is fixed (`65f14342`), so a version, dist-tag or range is expressible. The objection is spent; the row is still not *taken* | 2026-08-17 | [§4](#4-what-p1-permits-and-refuses--retired-kept-as-documentation) |
| **§3** | P1's justification — *"the rule already applied one level up"* — is **false**. The commit pin is recorded and never consulted; P1's *shape* survives, its *precedent* does not | 2026-08-17 | [§3](#3-the-principle) · `trust-paths.md` §1 |
| **§7** | *"Not a new lockfile"* is **refuted**: the lockfile is per **fetched** pack and has no field for a package version; every npm-declaring pack is embedded | 2026-08-18 | [§7](#7-what-this-does-not-license) · `trust-paths.md` OQ-TP4 |
| **OQ-TP6** | The gate **stays** and its refusal is now **fatal** — no partial packs. This is what §1/§2 said was missing, closed by deleting the host/jail split rather than by adopting P1. Ruled and built (`6385dfbb`). The same ruling keeps `npm` **deliberately ungated**: the gate is about `curl \| sh`, not about installing things | 2026-08-18 | `trust-paths.md` §3.1 |
| **OQ-TP5** | §4 row 3's live hole is closed by **removing the evergreen mechanism**, not by refusing the floating declaration: `install` obeys the record, `yolo pack update` is the only act that resolves, the poll only reports. Built (`b3a29ad8`) | 2026-08-18 | `trust-paths.md` §1 row 1 |
| **OQ-TP1** | **Obviated** by OQ-TP6 — there is nothing to carry into a jail if no jail starts | 2026-08-18 | `trust-paths.md` §3.1 |

**Still open elsewhere, and this document does not answer them:** `trust-paths.md` **OQ-TP3** (is a
pack *required* to pin, or merely permitted?), **OQ-TP4** (where an embedded pack's npm pin would
live), **OQ-TP7** (the fatal's preflight and approve path), **OQ-LP8 / G2b** (the commit pin is never
consulted at launch), and `program-delivery.md` **OQ-PD1…PD8**, which restate the venue question at
wider scope than the pack system.

---

## Open Questions

### 💬 OQ-X1 — does a digest-pinned installer script satisfy P1, given its own fetches are not pinned?

§5 is the tension. A pinned script is strictly better than an unpinned one and strictly worse than a
pinned binary, and P1 as written does not say which side of the line it falls on.

**What it decides:** whether an agent distributed only by `curl | sh` can be installed by a fetched
pack at all, or whether such packs must wait for the agent to ship a binary or a pinned npm version.

_Leaning:_ **Allow it, labelled honestly as a shallow pin.** Refusing buys little once pinned `npm`
is permitted — an npm package's postinstall fetches whatever it likes too, so the second hop exists
there as well and we would be refusing the honest spelling while permitting the disguised one. That
is exactly the inversion this document exists to remove. The condition is that the approval prose
(§6) must *say* it is a shallow pin, rather than showing a digest that implies more than it delivers.

> [!NOTE]
> **One premise of that leaning drifted, and it makes the question easier rather than harder.** The
> leaning says *"once pinned `npm` is permitted"* — as of 2026-08-23 npm is permitted **whether or
> not it is pinned**, and OQ-TP5 chose to remove the silent-update mechanism instead of requiring a
> pin. So the comparison is no longer "pinned npm vs. pinned installer" but "**un**pinned npm vs.
> pinned installer", which strengthens the leaning: refusing a digest-pinned script while permitting
> a bare package name would be the inversion at its sharpest. What has *not* changed is §5's
> substance — the installer's second hop is real, and the approval prose still has to say so.

**Answer:**
> _(empty — fill in when decided)_
