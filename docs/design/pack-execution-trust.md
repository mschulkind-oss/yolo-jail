---
title: "What a fetched pack may execute, and what you are agreeing to"
date: 2026-08-17
status: in-review
tags: [packs, trust, approval, security]
summary: "The gate names mechanisms — curl|sh refused, npm install -g permitted — and both run arbitrary code in the jail. Replace the list with one property: a fetched pack may cause execution only of content it pins."
---

# What a fetched pack may execute, and what you are agreeing to

**Status:** DESIGN, 2026-08-17. Nothing built. Written from a review challenge that turned out to be
right: *"a fetched pack can't introduce an installer — that means you couldn't use a pack in git to
install an agent? don't we allow native binary downloads? why disallow an installer at that point?"*

**The short version.** Today the origin gate refuses one **mechanism** (`curl | sh`) and permits
another (`npm install -g`) that is equally arbitrary code in the jail. The containment argument
cannot separate them, because both run in the same place. The property that actually separates safe
from unsafe is **pinning**, and this repo just ruled on it one level up: an approval binds to a
*specific commit*. Apply the same rule one level down — **a fetched pack may cause execution only of
content it pins** — and the mechanism list disappears.

**Reads with:** [`pack-system.md`](./pack-system.md) (§9, the origin gate) ·
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

---

## 3. The principle

> **P1. A fetched pack may cause execution only of content it pins.**
>
> Not *which tool* fetches it. Not *where* it runs. Whether what executes is determined by what you
> approved.

This is the same rule the maintainer just applied one level up — an approval binds to a specific
commit, so a git repo cannot change under you after you said yes. P1 is that rule applied to the
things a pack pulls in from outside its own tree.

**Why pinning is the right axis and containment is not:** the jail already bounds the *blast radius*
of anything that runs in it, equally for all three forms. What the jail cannot do is tell you that
the thing running today is the thing you reviewed. Only a pin does that.

---

## 4. What P1 permits and refuses

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

## 6. Approval must be readable — RULED

*"Print the claims at approval time, but they need to be understandable by a new user."*

Claims render today as terse tokens (`contributes.go:658-668`):

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
- **Not** a claim that pinning makes code safe. A pinned malicious binary is still malicious. Pinning
  makes approval *meaningful*; it does not make the thing trustworthy.

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

**Answer:**
> _(empty — fill in when decided)_
