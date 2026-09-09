---
title: "Every path by which someone else's content runs in your jail"
date: 2026-09-06
status: in-review
tags: [trust, packs, security, inventory]
summary: "Twenty-six paths, enumerated from the code, each with when trust is extended and whether the content can change afterwards. Pinning changes an outcome in three of them, because every gate keys on a declaration and none on content. Nine of ten questions are settled — the fetched-pack approval prompt among them, deleted as theatre — and one is open: a wrapped plugin's hooks reach the agent's lifecycle and appear in no launch banner."
---

# Every path by which someone else's content runs in your jail

**Status:** INVENTORY, 2026-08-17; **compacted 2026-09-06.** Ten questions filed, **nine settled**
(six ruled, three retired) and **one open** —
[`OQ-TP10`](#-oq-tp10--a-wrapped-plugins-hooks-reach-the-agents-lifecycle-and-appear-in-no-launch-banner).
Every code anchor below was re-checked against the tree on 2026-09-06: the ones that had drifted are
repinned, and the ones that named code the 2026-09-04 rulings deleted are rewritten to say so (the
compacting commit lists both). Beyond the rulings, **everything here is inventory** — traced in the
code, with the anchors inline.

**The short version.** Twenty-six paths deliver someone else's content into a jail.
**Pinning changes an outcome in three of them** ([§1](#1-the-verdict)); everywhere else it is
theatre, because **every gate in this system keys on a DECLARATION and none on CONTENT.** Two
rulings made elsewhere reshaped this document without touching that verdict: agent CLIs are
**evergreen** ([`program-delivery.md`](./program-delivery.md)
[§3.5](./program-delivery.md#35-the-second-axis-who-the-dependency-serves-amendment-2026-09-03), 2026-09-03),
which removed the npm-pinning questions rather than answering them; and the fetched-pack approval
prompt is **theatre** ([`OQ-TP9`](#decision-ledger), 2026-09-04), which deleted the one gate this
system had and left disclosure in its place.

**Why this exists.** A proposal ([`pack-execution-trust.md`](../reference/pack-system.md#why-there-is-no-approval-gate)) argued that
a fetched pack should only execute content it pins. The review response was *"they're all just as
weak. anything can be an installer. we're ultimately extending trust somewhere. I'm not entirely
sure where this pinning even helps."* That is right, and this document is the ground truth the
proposal should have been built on.

> [!IMPORTANT]
> **The proposal's central premise is false, and I verified it myself.**
> [`pack-execution-trust.md`](../reference/pack-system.md#why-there-is-no-approval-gate) [§3](../reference/pack-system.md#why-there-is-no-approval-gate)
> says the commit pin "is the rule already applied one level up". It is not applied anywhere. See
> [§1's lockfile finding](#1-the-verdict).

**What pinning actually buys, stated honestly:** it bounds trust in **time**, never in scope. It
cannot make code safe; a pinned malicious binary is malicious. Its only claim is *"the thing you
approved is the thing that runs, and you will be asked again when it changes"* — which defends
against exactly one threat, the silent update.

---

## Decision Ledger

| ID | Ruling / Decision | Date | Settled in |
| :--- | :--- | :--- | :--- |
| **OQ-TP1** | **Obviated.** There is no decision to carry into a jail, because a refused contribution refuses the launch ([`OQ-TP6`](#decision-ledger)). The ruling kept the jail's hardcoded `mayAccessHost=true`, since the jail cannot read origin and deriving it would be a regression. ⚠ That parameter was **deleted 2026-09-04** with the gate it fed ([`OQ-TP9`](#decision-ledger)); the argument survives as the trap it still is | 2026-08-18 | [§3.1](#31-a-refused-contribution-refuses-the-launch-) |
| **OQ-TP2** | **Nothing explicit.** Agent context needs no gate and no separate disclosure — the lockfile's commit pin closes over it, because it closes over the whole tree | 2026-08-18 | [§2](#2-the-inventory) |
| **OQ-TP3** | **RETIRED, not answered.** *"Is pinning worth building, and where first?"* — its ranking put npm first, and npm no longer takes a pin. Its still-open half (*must a pack pin, or merely may it?*) was inherited at wider scope as [`program-delivery.md`](./program-delivery.md) [`OQ-PD6`](./program-delivery.md#decision-ledger) and ruled there: the receipt is the pin, for **project** dependencies; an agent dependency has no pin to obey | 2026-09-03 | [`program-delivery.md`](./program-delivery.md) [§3.5](./program-delivery.md#35-the-second-axis-who-the-dependency-serves-amendment-2026-09-03) |
| **OQ-TP4** | **RETIRED as posed.** *"Where does an EMBEDDED pack's npm version get pinned?"* — nowhere, because it is not pinned at all. Its three venues (manifest / lockfile / user config) all recorded something the evergreen ruling deletes the need for. **What must not be re-derived:** pinning in the manifest makes yolo's release cadence the ceiling on agent-CLI freshness — the objection [`program-delivery.md`](./program-delivery.md) [§5.1](./program-delivery.md#51-a1--bake-everything-into-the-image) hits, now an argument *for* the ruling | 2026-09-03 | [`program-delivery.md`](./program-delivery.md) [`OQ-PD12`](./program-delivery.md#decision-ledger) |
| **OQ-TP5** | **No evergreen npm** — `install` obeys the lockfile, `update` alone resolves, the hourly poll only reports. **Built 2026-08-18 (`b3a29ad8`)**, minus the pin it had nowhere to record. ⚠ **SUPERSEDED 2026-09-03** by [`OQ-PD12`](./program-delivery.md#decision-ledger) and **REVERSED IN CODE 2026-09-04**: the packs it governed are agent dependencies, ruled evergreen — updated by the launcher at the user's own invocation, never on the boot path (B2, [`OQ-PD12a`](./program-delivery.md#decision-ledger)). `_poll_and_report` is gone; a PINNED package still resolves nothing. **What must not be re-derived:** the mechanism never fired in steady state — its launcher was `PATH`-shadowed ([`OQ-PD8`](./program-delivery.md#decision-ledger)) — and it froze the agents it governed six weeks stale | 2026-08-18 · superseded 2026-09-03 · reversed 2026-09-04 | [§1 row 1](#1-the-verdict) |
| **OQ-TP6** | **A refused contribution is a refused launch.** No partial packs — fix the pack, remove the pack, or approve it. **Built 2026-08-18 (`6385dfbb`)**. ⚠ **Its subject was deleted 2026-09-04** by [`OQ-TP9`](#decision-ledger): nothing produces a refusal any more, so the rule stands with nothing to apply to, and binds any future refusal source | 2026-08-18 | [§3.1](#31-a-refused-contribution-refuses-the-launch-) |
| **OQ-TP7** | **RETIRED, not answered — [`OQ-TP9`](#decision-ledger) deleted its subject.** *"`yolo check` reports PASS on a config the launch refuses, and the refusal's APPROVE option needs a tty and a network."* Every refusal source gated on the deleted `MayAccessHost`, so there is no refusal to predict and no approve path to be unreachable — both gaps dissolved rather than closed. **Preserved:** the third-gate trap — a preflight that predicts a launch refusal must SHARE the gate, never copy it; the test that pinned *two* gates by name could be satisfied vacuously by a third, and now pins *zero* | 2026-09-04 | [§3.1](#31-a-refused-contribution-refuses-the-launch-) (the third-gate warning) |
| **OQ-TP8** | **Ungated, both halves — a recorded ruling, not an accident.** Pack `derive.lua` runs with no origin check, in-jail at boot and host-side under `yolo host -- <cmd>`. The leaning's host-half gate failed a parity check: the same command folds each pack's **static** `kind: "env"` keys into the process environment one step EARLIER, ungated, so the derive computes a field the manifest can already state literally — gating the computed path while the literal one is open is theatre. The disclosure is the commit pin ([`OQ-LP8`](../reference/loophole-system.md#oq-lp8)), not a claim line. Reopens if the VM gains I/O, exec, network or an unbudgeted loop, or if `ctx` grows a field static `env` cannot carry | 2026-09-04 | [§2](#2-the-inventory), [the pack-Lua section](#pack-shipped-lua-is-ungated-on-both-sides-and-that-is-the-ruling) |
| **OQ-TP9** | **The fetched-pack approval prompt is THEATRE — deleted.** Selecting a pack means writing user-scope config as the host user (`packs` is inexpressible at workspace scope *by construction*), so the gate refused an actor who had already passed a stronger one — [`gate-placement-principle.md`](../reference/gate-placement-principle.md) [Test 1](../reference/gate-placement-principle.md#test-1--the-authority-test-could-this-actor-already-do-it), already applied this way to the sibling `--user-layer` route. Its original containment rationale was refuted in-house ([`pack-execution-trust.md`](../reference/pack-system.md#why-there-is-no-approval-gate) [§2](../reference/pack-system.md#why-there-is-no-approval-gate)). **Kept:** `packs` user-scope-only (that half PASSES Test 1) and the startup disclosure banner, onto which [`pack-execution-trust.md`](../reference/pack-system.md#why-there-is-no-approval-gate) [§6](../reference/pack-system.md#why-there-is-no-approval-gate) is retargeted. **Corrected same day:** the pin is effectively honored already (a launch resolves from the local mirror, which only moves at `pack install`), so the follow-on was [`OQ-LP8`](../reference/loophole-system.md#oq-lp8)'s two documentation requirements (delivered 2026-09-04), not enforcement; the lockfile is write-only at launch, and G2b is moot. Retires [`OQ-TP7`](#decision-ledger); opens [`OQ-TP10`](#-oq-tp10--a-wrapped-plugins-hooks-reach-the-agents-lifecycle-and-appear-in-no-launch-banner) | 2026-09-04 | [§3.1](#31-a-refused-contribution-refuses-the-launch-) |

> [!WARNING]
> **This document's questions were renumbered on 2026-08-18, and the reason is worth keeping.** They
> were [`OQ-T1..T4`](../reference/loophole-transport.md#why-its-this-way) and
> collided with [`loophole-transport.md`](../reference/loophole-transport.md), which already owned
> [`OQ-T1..T9`](../reference/loophole-transport.md#why-its-this-way) — and *those*
> are the ones cited from code, by name:
> [`OQ-T2`](../reference/loophole-transport.md#why-its-this-way)
> (`internal/loopholes/loopholescmd.go:201`),
> [`OQ-T5`](../reference/loophole-transport.md#why-its-this-way)
> (`internal/macosuser/macosuser.go:555`),
> [`OQ-T7`](../reference/loophole-transport.md#why-its-this-way)
> (`internal/svcendpoint/doc.go:44`; all three re-checked 2026-09-06).
>
> Two docs answering to one ID space is worse than a rename: a reader grepping an ID landed in
> whichever file they opened first. This doc yielded because its IDs were cited only from
> `roadmap.md`, which moved in the same commit; the transport's are cited from three code files and
> did not move. **A stale
> [`OQ-T1..T4`](../reference/loophole-transport.md#why-its-this-way) referring to
> trust-paths therefore means "written before 2026-08-18" — it is not a dangling reference, it is an
> old spelling of [`OQ-TP1..TP4`](#decision-ledger).**

> [!NOTE]
> **The section numbers and question headings are an API too.** [`§1 row 1`](#1-the-verdict) is
> cited from `internal/cli/packupdate.go`, `internal/cli/pack.go`, `internal/entrypoint/shims.go`,
> `internal/entrypoint/npmspec.go` and two tests (`packupdate_test.go`, `npmlauncher_test.go`);
> [`§3.1`](#31-a-refused-contribution-refuses-the-launch-) from [`RELEASE-NOTES.md`](../RELEASE-NOTES.md)
> and three sibling docs (the code that cited it, `run/packrefusal.go`, is deleted). The headings of
> [`OQ-TP7`](#-oq-tp7--yolo-check-cannot-predict-the-fatal-refusal-and-the-refusal-names-a-fix-that-needs-a-tty-and-a-network--retired-2026-09-04),
> [`OQ-TP8`](#-oq-tp8--pack-shipped-lua-runs-ungated-on-both-sides-of-the-boundary--is-that-a-ruling-or-an-accident--resolved-2026-09-04),
> [`OQ-TP9`](#-oq-tp9--is-the-fetched-pack-approval-prompt-a-gate-or-theatre--resolved-2026-09-04) and
> [`OQ-TP10`](#-oq-tp10--a-wrapped-plugins-hooks-reach-the-agents-lifecycle-and-appear-in-no-launch-banner)
> are linked by slug from seven other docs (counted 2026-09-06), which is why the three settled
> ones survive below as pointers rather than being deleted. Renumber or rename only with a grep in
> hand.

## 1. The verdict

**Pinning changes an outcome in three of twenty-six paths.** Everywhere else it is theatre, and the
reason is structural: **every gate in this system keys on a DECLARATION** — a URL, a path, a
component name, a config value — **and none on CONTENT.** That is a coherent design. It answers *"did
the footprint grow?"* well. It cannot answer *"is this the same code I looked at?"* at all.

**And the sharpest form of your instinct, which I had not seen:** `pack install` syncs the mirror and
writes the lockfile **in the same loop iteration**
([`pack.go`](../../internal/cli/pack.go#L1128-L1199) — `store.Sync` at `:1151`, the `lock.Set`
write at `:1186`; repinned 2026-09-06). The act that moves the content *is* the act that moves the
pin. A pin advanced by the same command that changes the bytes is a receipt. It becomes a gate only
if three things hold together — (i) enforced at use, (ii) advanced by a *different* act than the one
that changes content, (iii) that act shows you what changed. Today **none** hold, and nobody is
proposing to fix (ii).

### The lockfile is a receipt, not a gate

`LockEntry` ([`lock.go`](../../internal/packsrc/lock.go#L33-L44)) records `Name`, `Source`, `Commit`
and `Ref` — and, since 2026-09-04, deliberately nothing else:

| Field | Read at launch? | Evidence |
| :--- | :--- | :--- |
| `ApprovedHostAccess` | ⛔ **DELETED 2026-09-04** by [`OQ-TP9`](#decision-ledger), with the gate that read it. It was the lockfile's only launch-time reader, so **the lockfile is now write-only at launch** — `run/packs.go` says so where it used to be read, and `LockEntry`'s doc comment records the deletion and refuses reintroduction without a design ruling | was the one real gate; ruled theatre |
| `Commit` · `Ref` | **No.** Every reader is **display-only**: the moved-pin message and the `pack status` listing, both in [`internal/cli/pack.go`](../../internal/cli/pack.go). The launch re-resolves the **config's ref** against the local mirror | verified 2026-08-18, **still true 2026-09-06**. Cited by SYMBOL rather than by line: the four `#L` anchors this row carried all drifted or died within a month |

**What bounds this, and why it is a meaning defect rather than a security one** (the correction
[`OQ-TP9`](#decision-ledger) made to its own ruling, the same day): a launch resolves a fetched pack
from the **local mirror** at the config's ref (`packsrc.Store.resolveFromStore`), and the mirror
moves only when `yolo pack install`/`update` runs — the one network step in the product. So content
is frozen between installs, and the lock's commit and the mirror's ref agree right after either
command writes both. Making resolution read the lock's commit instead of the mirror's ref is what a
lockfile means everywhere else, and worth doing — but it is correctness-of-meaning, not a gate.
[`OQ-LP8`](../reference/loophole-system.md#oq-lp8)
ruled the substance — *"choosing to follow a branch IS the trust decision"* — and its two
documentation requirements (say that in one plain sentence; document **tag pins** as the shape for
a pack carrying code) were **delivered 2026-09-04**. G2b, the pin-anchoring follow-on, is moot: it
would have anchored an approval that no longer exists. (Not because *"`ApprovedAt` is written and
read by nothing"* — there is no such field, and has not been since `04410aa1`, 2026-08-15, deleted
it for exactly that reason.)

**Two structural facts about the file itself.** They were the origin of [`OQ-TP4`](#decision-ledger),
now retired — kept because they remain true of the lockfile and constrain anything built on it:

- **There is nowhere to put an npm version.** `LockEntry`'s fields are everything about a *git* pin
  and nothing about a package one. It needs a new field, and `LockSchema` is versioned precisely so
  this kind of change is a bump rather than a silent misread.
- **The file exists per FETCHED pack.** `Commit` is *"empty for a local pack — a directory has no
  commit, and pretending otherwise would invent a pin"*
  ([`lock.go`](../../internal/packsrc/lock.go#L39-L40)), and an embedded pack has no row at all. The
  three packs that declare npm programs — **pi, copilot, opencode** (codex moved to its vendor's
  installer on 2026-09-04, `dadafbde`) — are all embedded.

### Where a pin would change the outcome

1. **`program via npm`** — because nothing *is* pinned, and **since 2026-09-04 that is the ruling.**
   Every shipped npm pack declares a bare package name.
   [`program-delivery.md`](./program-delivery.md)
   [§3.5](./program-delivery.md#35-the-second-axis-who-the-dependency-serves-amendment-2026-09-03)
   classifies an agent CLI as an **agent dependency** and rules that class **evergreen**: the
   launcher updates an unpinned package at the user's own invocation of the agent, throttled by a
   stamp (`UPDATE_INTERVAL=3600` seconds) and gated by the `agent_updates` policy (`_update_due`,
   `_locked_update` in [`shims.go`](../../internal/entrypoint/shims.go)); `YOLO_PACK_UPDATE=1` —
   set only by `yolo pack update` ([`packupdate.go`](../../internal/cli/packupdate.go)) — refreshes
   NOW, ignoring both. There is no boot step anywhere in the shipped design. **A PINNED package is
   untouched:** the launcher honours a version, dist-tag or range
   ([`npmspec.go`](../../internal/entrypoint/npmspec.go)) and resolves nothing for it. That half is
   why this row's anchor is still live and cited from code.

   > [!NOTE]
   > **The anchor [`§1 row 1`](#1-the-verdict) is kept deliberately, and the code comments that
   > cite it describe SUPERSEDED behaviour.** [`OQ-TP5`](#decision-ledger) ruled *no evergreen npm* on 2026-08-18 and
   > built it (`b3a29ad8`: the hourly poll reported, `yolo pack update` alone resolved). Two
   > measurements on 2026-09-03 retired it. The mechanism had never fired in steady state — its
   > launcher sat last on `PATH` and was shadowed by the real binary the moment the first install
   > landed ([`OQ-PD8`](./program-delivery.md#decision-ledger); B2 moved the launch dir to second on
   > 2026-09-04, which is what makes any launcher reachable at all). And the agents it governed were
   > **six weeks stale**: copilot 1.0.48 against 1.0.82, codex 0.145.0 against 0.153.1, pi 0.82.1
   > against 0.84.4. The silent update the ruling defended against never happened; the freeze it
   > caused did.

   > [!WARNING]
   > **npm and `installer` are treated alike for AUTHORITY and differently for DISCLOSURE — do not
   > re-derive the old split.** Until 2026-09-04 a `curl`-piped installer was refusable for a
   > fetched pack and an npm install was not, on the reasoning that a registry package is *"the same
   > trust as any dependency the user already installs"* — while `npm install -g` from the same tree
   > runs `postinstall`, ungated ([`pack-execution-trust.md`](../reference/pack-system.md#why-there-is-no-approval-gate)
   > [§2](../reference/pack-system.md#why-there-is-no-approval-gate)).
   > [`OQ-TP9`](#decision-ledger) deleted the refusal for both. What remains is a disclosure
   > asymmetry, and it is the right one: only the `via: installer` instance of `program` crosses
   > anything (a fetched script), so the footprint marks it review-worthy and the launch banner
   > prints it, while an npm install stays silent ([`footprint.go`](../../internal/packload/footprint.go)
   > — `installer:` review-worthy, `npm` not; the `KindProgram` comment in
   > [`packloopholes.go`](../../internal/cli/run/packloopholes.go)). Under
   > [`OQ-PD13`](./program-delivery.md#decision-ledger), which prefers a vendor's native installer
   > over npm for an agent CLI, flipping a pack's `via` therefore adds a banner line, not a prompt.
2. **A loophole's daemon FILE, and a plugin's HOOK BODIES** — the two crossings whose claim string
   genuinely does not cover the bytes. `["python3","{loophole_dir}/acme.py"]` is one claim string
   forever; `plugin <name> hooks (runs code at agent lifecycle events)` is a **constant** with no
   path and no digest in it. This is
   [`OQ-LP8`](../reference/loophole-system.md#oq-lp8)'s
   subject, ruled: the commit pin is the disclosure.

   > **"Plugin" and "hook body", defined — they are the AGENT's extension mechanism, not yolo's.**
   > A pack may ship a **Claude Code plugin**: a `.claude-plugin/plugin.json` manifest that the agent
   > reads directly. yolo delivers it and reports what it declares, but never interprets it. A
   > manifest can declare six component kinds, and yolo marks three of them as running code
   > ([`pluginpack.go`](../../internal/pluginpack/pluginpack.go#L131-L149)):
   >
   > | Component | What it does | Runs code |
   > | :--- | :--- | :--- |
   > | `hooks` | runs code at agent lifecycle events | ✅ |
   > | `mcpServers` | starts MCP server processes | ✅ |
   > | `lspServers` | starts language server processes | ✅ |
   > | `commands` · `agents` · `outputStyles` | slash commands, sub-agent definitions, output styles | ❌ |
   >
   > A **hook body** is the script a `hooks` entry names — the thing the *agent* executes when it
   > reaches one of its own lifecycle events. yolo never runs it and never reads it.
   >
   > **That is exactly why the claim is uncoverable.** The string yolo shows is the table row above,
   > verbatim and constant. It names the *category*, not the file — so a plugin can rewrite its hook
   > script and the string a user saw is byte-identical. Compare a loophole's `command`, which at
   > least names a path: that one is uncovered because the path's *contents* move, where this one
   > has no path in it at all. (Whether that string appears on any launch banner at all is
   > [`OQ-TP10`](#-oq-tp10--a-wrapped-plugins-hooks-reach-the-agents-lifecycle-and-appear-in-no-launch-banner).)
3. **Editing `?ref=` in config without reinstalling.** The mirror already holds every branch and tag,
   so a config-only edit resolves offline at the next launch and delivers new content with **no
   install, no network and no prompt**. `pack status` calls this drift; nothing on the launch path
   consults it.

   > **What `?ref=` is.** A fetched pack is named by a URL-shaped *address* in your user config, and
   > `?ref=` is the query parameter on it that selects which git ref to use
   > ([`addr.go`](../../internal/packsrc/addr.go#L41-L55)):
   >
   > ```text
   > git+https://github.com/acme/mono//tools/agent-pack?ref=main
   > └─ scheme ──┘└──── repo ───────┘└── subpath ────┘└── ref ──┘
   > ```
   >
   > The ref may be a **branch, a tag, or a full commit SHA**, and for a git address it is always
   > non-empty — there is no "unspecified ref" form to fall back on. (A local `file://` pack has no
   > ref at all; it is a directory, and none of this row applies to it.) So `?ref=v2.1.0` and
   > `?ref=6461be6…` are already pins *in the address*; `?ref=main` is a moving target by
   > construction. `pack install` syncs a mirror of the **whole repository**, so every branch and tag
   > is already on disk: changing `main` to `some-other-branch` is a text edit that resolves against
   > that mirror at the next launch. The bytes that run change because a config line changed.

   > [!IMPORTANT]
   > **This is a HUMAN path, not an agent-escalation path — and the reason is stronger than "not
   > without reapproval".** A pack address is **inexpressible from a workspace**, by construction
   > rather than by validation. `packs` is USER-SCOPE ONLY and is read from `paths.UserConfigPath()`
   > **directly, not from the merged config** ([`packs.go`](../../internal/config/packs.go#L3-L18)) —
   > so a workspace file cannot name a pack even to be refused. The package comment states the reason
   > in this document's own terms: *"a workspace config travels with the repo and is agent-editable,
   > so it must not be able to name content that enters the jail."*
   >
   > And an agent cannot reach the file where it *is* expressible: the host's
   > `~/.config/yolo-jail/config.jsonc` is **never mounted into a jail**, and the `config.jsonc` a
   > jail sees at that path is **generated per consumer** from the merged result
   > (`assemble_test.go` pins both facts: *"user config mount: none"*). Editing the in-jail copy
   > changes a generated artifact and nothing else.
   >
   > The gap is real but narrower than it reads: a **person** who edits `?ref=` gets new code with no
   > prompt, because nothing on the launch path compares the ref they are now running against the
   > ref they installed. Same missing enforcement as the lockfile finding above.

### P1. Trust flows DOWNWARD, and a parent controlling its child is not a finding

**Each level is the host for the next: user → jail → nested jail.** A user configures their jail; a
jail configures the jails it launches. That is the model, not a leak in it, and this principle exists
because the inventory below will otherwise keep growing rows that are the model working.

**The worked example, since it looks alarming until the direction is stated.** The implicit local
pack directory — `~/.config/yolo-jail/local`, which needs no config line, is appended last so it
outranks every configured entry, and carries `file://` origin — **is writable from inside a jail.**
Measured 2026-08-18: `/home/agent` is a `ro` bind of host-side state, but `/home/agent/.config` is a
**rw** bind of `<workspace>/.yolo/home/config` (`assemble_parts.go`, re-checked 2026-09-06), with
only `config.jsonc` and `inherited-launch.jsonc` pinned `ro` file-by-file. So an agent can create
that directory, and a **nested** launch — whose `yolo` runs inside this jail and therefore resolves
`paths.LocalPackDir()` to this path — loads it at full local-pack authority with no prompt.

**That is correct.** An agent that can already run arbitrary code in this jail configuring a jail it
launches is exactly the direction trust is supposed to travel. Refusing it would mean a jail could
not set up its own children, which is the dev loop this repo runs on. (The one nested launch the
launcher now refuses is on `/workspace` itself — the live-overlay guard, `9440d9c9`, 2026-09-06 —
and that protects the running session's own home overlay from being regenerated, not the child's
authority. P1 is untouched by it.)

**What the principle does NOT license**, and the boundary that stays load-bearing:

- **Nothing flows upward.** The host's own `~/.config/yolo-jail/` is not mounted into a jail at all —
  verified in the same measurement — so none of this reaches the machine. A path that let a jail
  change what its PARENT runs would be a finding, and a serious one.
- **It is not a licence to stop disclosing what enters from OUTSIDE.** A fetched pack's content is
  someone else's, at every level. P1 is about the *relationship between levels*, not about origin.

> [!NOTE]
> **This principle is why a measurement can be true and still not be a finding.** "The directory is
> writable" is a fact; "therefore it is a hole" needs the direction of trust, and downward is the
> permitted one. Anyone re-deriving the measurement should stop here rather than filing it.

### Where it is theatre — the four that matter

- **Pinning a pack tree at all**, in the dominant case: see the same-loop-iteration finding above.
- **Pinning execution kinds while `skills` and `briefing` are ungated.** A fetched pack can rewrite
  every `SKILL.md` and every line of briefing prose with no claim, no lockfile entry and no launch
  disclosure — they are classified `disclosureSkip` as "jail-internal by construction"
  ([`packloopholes.go`](../../internal/cli/run/packloopholes.go)). A skill that says *"run this
  command"* is an execution path with extra steps.
- **Pinning anything while `~/.config/yolo-jail/local` exists.** The implicit local pack needs no
  config line, has no lockfile entry, no commit, no claim, gets **full trust**, and is appended
  **last** so it outranks everything — selected by one `os.Stat` that follows symlinks
  ([`packs.go`](../../internal/config/packs.go#L297)).
- **Pinning a refusal that is not enforced where it executes.** Retired twice over — first by
  [§3.1](#31-a-refused-contribution-refuses-the-launch-)'s ruling, then by the deletion of the
  refusal itself — and kept in this list because it is the shape to check any *new* gate against.

---

## 2. The inventory

Ordered from most-trusted origin to least. "Silent change" is the column the exercise exists for, and
this table is the evidence for [§1](#1-the-verdict)'s "three of twenty-six". Since 2026-09-04 no row
has a prompt in its "trust extended" column: **trust is extended once, when a person writes the
config line, and everything after that is disclosure** — the launch banner for host reads, the
pre-spawn block for host execution, `yolo pack footprint` on demand.

| # | Path | Grants | Trust extended | Can change silently? |
| :-- | :--- | :--- | :--- | :--- |
| 1 | the yolo binary — built-in skills + composed briefing | agent context | never | only via your own upgrade |
| 2 | **embedded pack `program via installer`** (claude, agy, codex) | in-jail exec as UID 0 | **never** — embedded origin honors unconditionally | **yes, and that is the RULING** — agent CLIs are evergreen ([`program-delivery.md`](./program-delivery.md) [§3.5](./program-delivery.md#35-the-second-axis-who-the-dependency-serves-amendment-2026-09-03)). Two movers: the URL's bytes, and the launcher running the pack's **declared update verb** ([`OQ-PD14`](./program-delivery.md#decision-ledger)) at the user's invocation, on a 3600 s stamp, under `agent_updates`. *The launcher this replaced ran `"$REAL_BIN" install` hourly — a no-op for most vendors; measured 2026-09-03, claude in this workspace had not moved since 2026-07-24* |
| 3 | **`program via npm`** (pi, copilot, opencode) | in-jail exec (postinstall + deps) | **never**, for any origin | **yes, and that is the ruling** — evergreen, resolved at the user's own invocation since 2026-09-04 ([§1 row 1](#1-the-verdict)); a PINNED selector resolves nothing. Silent in the launch banner by design: an npm install crosses nothing |
| 4 | `flake.nix` / `flake.lock` | in-jail exec (everything on PATH) | implicit, at PR merge | no for inputs (locked revs, hermetic build) |
| 5 | **the implicit local pack** `~/.config/yolo-jail/local` | everything, at maximum trust | **never**, and deliberately | **yes, continuously** — live dir, re-read every launch, no record |
| 6 | explicit `file://` local pack | same as 5 | implicit in the config line | yes, every launch — no copy, no hash |
| 7 | `--user-layer` / `YOLO_USER_LAYER` | the full user scope | never, by explicit ruling | re-read per invocation; inert unless named |
| 8 | user-scope `loopholes.<name>.command` | **host execution** | never, by the same ruling | yes for the bytes — the config pins an argv, nothing reads the program |
| 9 | workspace `mounts` | host read | at the config diff, which since 2026-08-29 fires on a fresh clone too ([§3.3](#33-the-config-gate-is-closed-and-the-scope-model-it-leaves)) | yes — `git pull`, the agent's own edit, or the host dir's contents |
| 10 | workspace `env_sources` | host read, exfiltration-shaped | at the config diff, same as 9 | yes — re-read live each launch; a missing file warns and skips |
| 11 | workspace `mcp_servers` / `lsp_servers` / `packages` / `mise_tools` | in-jail exec | at a diff that shows the NAME, never what it resolves to | mixed — the most useful contrast in the table |
| 12 | **the config gate itself** (`CheckConfigChanges`) | — it *is* the gate | — | **closed 2026-08-29** (`27b335ce`): a fresh workspace with declared config prompts, a non-TTY changed config refuses; attach still skips it, by design ([§3.3](#33-the-config-gate-is-closed-and-the-scope-model-it-leaves)) |
| 13 | workspace `yolo-jail.config.lua` — **activated by existing** | agent context, transitively in-jail exec | **never**; not a config key, so outside the diff, drift and snapshot | yes, every boot, with nothing to diff against |
| 14 | workspace `mise.toml` | in-jail exec | **never** — trust asserted *for* you on the podman argv | yes — `git pull`, and `latest` resolves at install |
| 15 | `agents_md_extra`, blocked-tool messages, source-less `host_files` | agent context | at the diff, which does carry the prose | covered by the diff; the finding is scope asymmetry |
| 16 | **`.yolo/handover.md`** | agent context, framed as an authoritative task list | **never** — no key, no prompt, no validation, no attribution | **yes, continuously** — an ordinary file any agent can write |
| 17 | fetched pack — **content** (skills, briefing, files, config-overlay) | agent context | **never** — no claim, no disclosure | yes, on every mechanism at once |
| 18 | fetched pack — `env` | in-jail exec in practice (no key allowlist, so `LD_PRELOAD` etc.) | **never**, explicitly; disclosed on the banner every launch | yes; the banner shows the value, nothing compares it |
| 19 | **fetched pack — loophole with only a `jail_daemon`** | in-jail exec, supervised, restart-policied, UID 0 | **never** — and it produces **no claim at all**, so no footprint line and no launch line ([§3.2](#32-jail_daemon-is-a-claim-free-crossing-to-supervised-in-jail-execution)) | yes trivially; nobody is told it exists |
| 20 | **fetched pack — `program via installer`** | in-jail exec as UID 0 | **never, since 2026-09-04** ([`OQ-TP9`](#decision-ledger)) — honored like an embedded pack's, disclosed on the banner as a review-worthy host read | yes — unpinned URL, plus the declared update verb at the user's invocation |
| 21 | fetched pack — wrapped agent plugin (hooks / MCP / LSP) | in-jail exec at lifecycle events | **never, since 2026-09-04** — and **no launch disclosure**: the claim is filed under `KindSkills`, which is `disclosureSkip` ([`OQ-TP10`](#-oq-tp10--a-wrapped-plugins-hooks-reach-the-agents-lifecycle-and-appear-in-no-launch-banner)) | **yes — the weakest claim string in the system**, a constant with no path or digest, and shown nowhere at launch |
| 22 | fetched pack — `reads-host` / `mount` / host-prepending `briefing` | host read | **never, since 2026-09-04**; disclosed on the banner every launch | yes — a moved ref changes the bytes under an unchanged banner line |
| 23 | fetched pack — loophole with a `host_daemon` | **host execution** + a CA trusted in-jail | **never, since 2026-09-04**; disclosed at the spawn boundary, BEFORE the daemon starts (`startLoopholesDisclosed`) | yes — the line pins the argv, not the file ([`OQ-LP8`](../reference/loophole-system.md#oq-lp8)) |
| 24 | `yolo host apply` | **host write** into your real home | explicit per invocation, `--assert` required | for a local pack, yes — source re-read each apply |
| 25 | the mirror + ref resolution behind rows 17–23 | selects which bytes every row above delivers | — | **three verified mechanisms** ([§1](#1-the-verdict) row 3) |
| 26 | **any pack's `derive.lua`** (`yolo.derive` + `yolo.env`) — row added 2026-09-02 from the providers defect report's D9 (distilled into [`providers.md`](../reference/providers.md); on the roadmap it was review thread 💬 18, closed the same day), which found this census had no entry for pack-shipped Lua | **sandboxed Lua execution** — in-jail at every boot with live tables (`deriveComputedLayer`, [`packsurfaces.go`](../../internal/entrypoint/packsurfaces.go)); host-side as a sentinel-input key probe during `yolo host apply` (`hostTableKeys`, [`hostrender.go`](../../internal/entrypoint/hostrender.go)); and host-side at every `yolo host -- <cmd>` launch with REAL inputs, credential included (`packload.AgentEnv`, called from [`host.go`](../../internal/cli/host.go); the jail-launch twin is [`profilechannel.go`](../../internal/cli/run/profilechannel.go); since `3144fbed`). The VM is allowlist-built — `SkipOpenLibs`, no `os`/`io`/`require`/`load`, fresh state, instruction budget ([`vm.go`](../../internal/agentcfg/luahook/vm.go)) — so the grant is *unvalidated config-surface and env output* plus whatever `ctx` carries, **not** process exec | **never, any origin — and that is the ruling** ([`OQ-TP8`](#decision-ledger)): `packload.DeriveScript` reads `<pack root>/derive.lua` with no origin check and no claim ([`deriveenv.go`](../../internal/packload/deriveenv.go)) | yes — the mirror re-resolves, and a derive is content, not a claim |

### Agent context needs no gate of its own

**RULED ([`OQ-TP2`](#decision-ledger), 2026-08-18): nothing explicit.** Skills, briefing prose and the
rest of the agent-facing surface (rows 17, 15, 16) get no gate and no separate disclosure line,
because **the lockfile already pins a commit, and a commit closes over the whole tree** — prose
included. A second mechanism aimed at the same bytes would be the halfway-measure shape this repo
keeps deleting.

**The scope is exactly right, which is worth stating because it looks narrower than it is.** A commit
pin covers *fetched* packs only. That is not a gap: **fetched packs are the only ones whose content
someone else controls.** A local pack is your own files under your own authority, and an embedded one
is yolo's own code. (An embedded pack's *tree* is yolo's own code; the npm **package** it names is
not — which is why [`OQ-TP4`](#decision-ledger) used to sit alongside this ruling. It is retired: that
package is an **agent dependency**, ruled evergreen, so no pin covers it and none is wanted.)

**This ruling inherits the enforcement gap**, and is worth exactly as much as that gap is closed:
until `LockEntry.Commit` is consulted at launch ([§1](#1-the-verdict)), "the pin covers it" is a
statement about the design rather than about a running system.

### Pack-shipped Lua is ungated on both sides, and that is the ruling

**RULED ([`OQ-TP8`](#decision-ledger), 2026-09-04): (a), both halves.** *"doesn't this get to install
actual things that changes stuff on the host? doesn't this mean it has a million injection
capabilities anyway? don't think we need to gate this."* Row 26 keeps the facts; this section keeps
the reason, because the leaning was half wrong and the way it was wrong is the reusable part.

**The leaning gated the host half, and that failed a parity check — not closely.** `yolo host --
<cmd>` folds each pack's **static `kind: "env"` keys** into the launched process's environment as
step (1) of its fold (`packload.EnvFold`, [`host.go`](../../internal/cli/host.go)), *before* the
derive runs at step (3) (`packload.AgentEnv`), with no origin check. So the derive computes the same
field a manifest can already state literally, at the same notch, through the same command. Gating
the computed path while the literal path is open is not containment; it is a gate that reads as
answered while the channel it names stays open.

**The same holds one level up.** At the host notch a pack renders `config`, `config-overlay`,
`skills` and `briefing` into the invoking user's **real home** (`render.HostFields`,
[`fieldset.go`](../../internal/render/fieldset.go)) — content an agent reads and acts on. Against
that, a compute hook that cannot exec, cannot read a file, cannot reach the network and runs under
an instruction budget is not the marginal capability worth a prompt.

**What this does NOT say.** It is not *"packs are trusted."* It is that **this channel adds nothing
to a pack's existing reach**, so a claim line here would be disclosure theatre. The honest
disclosure is the one rows 17–19 lean on: the commit pin
([`OQ-LP8`](../reference/loophole-system.md#oq-lp8)),
which closes over `derive.lua` exactly as it closes over every other file a pack ships. Its two
real powers stay named: (i) **unvalidated output into config surfaces** — the same trust as row 17's
content — and (ii) **reading whatever `ctx` carries**, which under
[`OQ-PT9`](../reference/providers.md#why-its-this-way) includes resolved provider credentials, with the
auditability trade that ruling recorded (a derive reading a secret is silent; a written config
artifact shows in `yolo config diff`).

> [!WARNING]
> **What would reopen this:** a derive gaining a capability the sandbox denies today — I/O, exec,
> network, or an unbudgeted loop — or `ctx` growing a field the static `env` channel cannot already
> carry. Both are edits to [`vm.go`](../../internal/agentcfg/luahook/vm.go) or to the derive's input
> table, and either should re-ask the question rather than inherit its answer.

---

## 3. Three findings that outrank the entire pinning question

### 3.1 A refused contribution refuses the launch ⚠

**RULED and BUILT 2026-08-18 ([`OQ-TP6`](#decision-ledger), which obviates
[`OQ-TP1`](#decision-ledger)); its SUBJECT DELETED 2026-09-04 ([`OQ-TP9`](#decision-ledger)).** *"If
the installer is refused, that should be fatal. We can't run packs with selective things disabled by
refusals. Fix the pack, remove the pack, approve. Those are the choices."*

This began as the only verified break of a guarantee the codebase actively claimed: the host computed
a refusal, printed `Warning: refused installer …`, and then staged the unmodified `pack.json` anyway,
so the jail — which loaded packs with a hardcoded permissive `mayAccessHost` — wrote the
`curl → bash` launcher regardless. The warning was true about the *decision* and false about the
*outcome*. **The ruling did not close that gap; it deleted the problem.** There is nothing to carry
across the boundary if no jail starts. `stagePacks` collected every refusal the `Honored*` family
reported and returned one `refusedLaunchError` — **before** the mechanical pre-flights, because this
one was about CONSENT and those are about pack mechanics.

**Past tense since 2026-09-04.** [`OQ-TP9`](#decision-ledger) deleted the gate every one of those
refusals came from, so `internal/cli/run/packrefusal.go` is deleted and `run/packs.go` says so where
the fold used to be. The `Honored*` family still exists and its `refused` return is **always nil**
(kept only because twelve call sites read the shape); the pre-flights that remain in `stagePacks` are
all about pack mechanics — destination collisions, name exclusivity, profile and provider names.
The ruling itself is untouched: it is about consent, not cadence, and it binds any future refusal
source.

**It also retired the partial-pack concept**, which is the deeper change and the part that outlives
its subject. A pack that half-loads is a pack whose behaviour nobody can predict from reading it: the
manifest says one thing, the running system does another, and the difference is a warning scrolled
past ten minutes ago. The three choices — **fix the pack, remove the pack, approve it** — were
exhaustive precisely because they were the only three that end with the manifest and the runtime
agreeing.

#### Origin decides nothing about trust any more — and what it still names

Until 2026-09-04 origin decided exactly one thing — *"whether a host-access declaration is honored"*
— through a predicate (`MayGrantHostFiles`, `false` for a fetched pack) that fed every gate in the
product. [`OQ-TP9`](#decision-ledger) deleted the predicate with the gates: every caller wanted
`true`, and an always-true predicate is worse than none — *a reader sees a gate and stops looking*
([`gate-placement-principle.md`](../reference/gate-placement-principle.md)
[the artifact form](../reference/gate-placement-principle.md#the-artifact-form-a-name-that-states-a-guarantee)).
[`packs.go`](../../internal/config/packs.go#L151-L161) now says in capitals that origin **decides
nothing about trust**. What it still names is the **delivery route**:

| Origin | What it is | How its content arrives |
| :--- | :--- | :--- |
| **embedded** | compiled into the yolo binary (`packs/*`) | already in the binary; no lockfile row |
| **local** | `file:///path/to/pack` on your own disk | read in place, every launch; a lockfile row with no commit |
| **fetched** | `git+https://…?ref=…`, content someone else controls | `yolo pack install` into the store; a lockfile row with a commit |

That is what `pack install`, `pack status` and the drift report key on — and nothing else does.

#### Why the gate was theatre — the argument, preserved

[`gate-placement-principle.md`](../reference/gate-placement-principle.md)
[Test 1](../reference/gate-placement-principle.md#test-1--the-authority-test-could-this-actor-already-do-it) —
*"If performing the guarded act already required at least as much authority as the gate protects,
the gate is theatre — and worse than nothing, because it looks like protection while the real gap
stays open."* Selecting a pack means writing `packs` in `~/.config/yolo-jail/config.jsonc`, as the
host user; `packs` is **inexpressible at workspace scope by construction**
([`packs.go`](../../internal/config/packs.go#L3-L18) calls that *"the whole security model of the
feature"*). The only other route is `--user-layer`, which requires the ability to run `yolo`, and
[`userlayer.go`](../../internal/config/userlayer.go) already applies Test 1 to it, ruling the other
way from the prompt: *"A gate here would refuse an actor who has already passed a stronger one —
pure ceremony, and the kind that teaches people to click through prompts."* **An agent cannot add a
pack.** The gate's original containment rationale — a git ref must not execute arbitrary code in the
jail — had been refuted in-house before the ruling
([`pack-execution-trust.md`](../reference/pack-system.md#why-there-is-no-approval-gate)
[§2](../reference/pack-system.md#why-there-is-no-approval-gate): `npm install -g` from the same
fetched tree runs `postinstall`, ungated). The prior art the review named agrees: a `nvim` plugin
manager clones a repo and runs its Lua on the real host with no prompt — the config line is the
consent — and where that prior art is *stricter* is exactly yolo's gap: its lockfile is consulted on
every load.

**What was kept, and is not a gate.** `packs` user-scope-only, which PASSES Test 1 — *"the actor
genuinely changes: a workspace config travels with a repo and is agent-editable"* — and the startup
disclosure: the launch banner for host reads (`notePackHostAccess`), the pre-spawn block for host
execution (`startLoopholesDisclosed`), and `yolo pack footprint` on demand, all reading one
enumeration (`packload.FootprintOf`). Disclosure is not consent, costs nothing, and is what actually
tells a user what crossed. [`pack-execution-trust.md`](../reference/pack-system.md#why-there-is-no-approval-gate)
[§6](../reference/pack-system.md#why-there-is-no-approval-gate)
— *"approval must be readable"* — is **retargeted, not retired**: the banner is now the only place a
user sees what a pack reaches, so "understandable by a new user" applies to it and matters more than
it did. The per-contribution property the old refusal insisted on (*never decide once for the whole
pack, or an installer URL gets smuggled through beside an innocent npm install*) survives as
per-contribution disclosure: each crossing is its own line.

> [!WARNING]
> **The jail-side `mayAccessHost` — deleted, and the reason deriving it was never possible is the
> part to keep.** The jail used to load packs with a hardcoded `true`, and an earlier draft called
> that untidy "defence in depth". It was neither: from inside a jail an embedded pack, a local pack
> and a fetched pack are identical directories under `YOLO_PACK_ROOT`, and origin is a fact about
> the *user config*, which the jail deliberately cannot read — the same credential boundary that
> makes `packs` user-scope-only. Deriving `false` for anything outside `_official/` would have
> refused the declarations of packs the user *did* select while protecting nothing.
> [`OQ-TP9`](#decision-ledger) deleted the parameter with the gate (`LoadJailPacks`,
> [`packsurfaces.go`](../../internal/entrypoint/packsurfaces.go): *"THERE USED TO BE A THIRD ARGUMENT
> HERE"*). The trap: **any future in-jail gate on origin is unbuildable, not merely untidy** — the
> jail has nothing to derive it from, so it has to be decided on the host or not at all.

> [!IMPORTANT]
> **Two things that look like partial packs and must NOT become fatal.** Both exist, both are
> deliberate, and collapsing them into any future refusal would break a jail's ability to boot.
>
> - **A declared bind mount whose host path is absent** is skipped with a warning
>   ([`runtime.go`](../../internal/loopholes/runtime.go#L235): *"skipping bind mount, host source
>   missing"*). That is *adaptation inside a capability the user already selected* — nothing was
>   refused, the thing simply is not there.
> - **A contribution whose KIND this build does not recognise** is skipped, not fatal, because the
>   host CLI and the baked entrypoint legitimately differ in age
>   ([`packdecl.go`](../../internal/packdecl/packdecl.go#L307-L319): a newer build's kind staged for
>   an older baked entrypoint *"is skew, not corruption"*; `DecodeTolerant` is the reader). That is
>   **skew tolerance**, not a refusal.
>
> The distinction that keeps these separate: **a refusal is about a claim yolo UNDERSTOOD and
> declined.** Something absent, or something from the future, is neither.

> [!WARNING]
> **The third-gate trap, from [`OQ-TP7`](#decision-ledger): a preflight that predicts a launch
> refusal must SHARE the gate, never copy it.** While the gate existed, `yolo check` reported `[PASS]`
> on a config the very next launch refused — `check` loaded packs with the origin predicate alone and
> never called the launch's fold — and the refusal's APPROVE option needed a tty and a network the
> reader might not have (CI, an offline laptop after a yolo upgrade added a claim producer). The
> tempting fix was a copy of the launch gate inside `check`. It would have satisfied the test that
> pinned *"exactly two gates exist, by name"* **vacuously**, and the next claim producer would have
> been merged into two of three sites. That test is now inverted:
> [`hostaccessgates_test.go`](../../internal/packload/hostaccessgates_test.go) pins **zero** gates —
> `TestNoFetchedPackHostAccessGateExists` walks every production identifier against the retired
> gate names — which has no such hole: every reintroduction is a new name, and every new name is a
> hit. A hit is a claim that [`OQ-TP9`](#decision-ledger) was reversed, and that belongs in this
> document before it belongs in a `.go` file. The shape to check any future fatal against: **the
> reader must be able to act on it from every place they read it**, including `yolo check`.

#### What building the ruling found, and what survives of it

1. **`briefing after: host:<path>` was withheld silently for a fetched pack** — one
   `&& p.MayAccessHost` inside `run/prepare.go`, no message. A pack whose only host claim was
   *"prepend the user's own AGENTS.md before my prose"* produced a jail with the pack's prose and none
   of the user's. It gained a reporter (`RefusedBriefingOverlays`), which
   [`OQ-TP9`](#decision-ledger) then deleted with the gate; today the claim is honored for every pack
   and disclosed on the banner as a host read
   ([`contributes.go`](../../internal/packdecl/contributes.go#L100-L103)).
2. **The launch never consulted `HonoredPlugins`** — its one production caller was `yolo host apply`'s
   skills compose, so [row 21](#2-the-inventory)'s hook bodies travelled into a jail inside the pack's
   skills tree with the refusal computed nowhere on that path. [`OQ-TP6`](#decision-ledger) put it in
   the fatal; [`OQ-TP9`](#decision-ledger) removed the fatal; the hooks now arrive with **no launch
   line at all** — which is
   [`OQ-TP10`](#-oq-tp10--a-wrapped-plugins-hooks-reach-the-agents-lifecycle-and-appear-in-no-launch-banner).
3. **No escape hatch, deliberately.** Every other fatal in this system has one
   (`YOLO_ALLOW_UNREACHABLE_SERVICES`, `YOLO_ALLOW_STALE_IMAGE`) because the user may be unable to
   repair the cause from where they are standing. A fourth choice here would have been the partial
   pack the ruling retires. Moot with the fatal gone; the reasoning stands for any future one.

#### "So should we just remove the gate?" — asked in August, ruled in September

Asked on review of [`OQ-TP6`](#decision-ledger), and the right question to ask of any guarantee that
turns out not to hold — an unenforced gate is worse than no gate, because the warning tells the user
something false. The answer then was **no**, on one fact: `installer <URL>` was one row of a general
approval model that also covered mounts, `reads-host`, briefing injection, plugin hooks and loophole
daemons, so removing it alone would have deleted one entry from the prompt and left `curl | sh` the
only ungated claim — an inconsistency, not a simplification. The same answer said where removal
*would* be right: *a decision about the **whole** approval model, retiring the prompt rather than one
line of it.* [`OQ-TP9`](#decision-ledger) is exactly that decision — on Test 1 rather than on
containment — and it retired the whole prompt, consistently.

### 3.2 `jail_daemon` is a claim-free crossing to supervised in-jail execution

A fetched pack declaring a loophole with only a `jail_daemon` produces **zero claims**: the module
enumeration (`moduleClaims`, [`loopholesource.go`](../../internal/packload/loopholesource.go#L275))
emits a claim per host daemon, doctor command, intercept, bind mount and device, and none for a
jail daemon. Verified end to end 2026-08-17; re-checked 2026-09-06. Under the old gate that meant
the grant-on-empty branch fired and the daemon was emitted with no prompt. With the gate gone the
shape is starker: **no claim means no footprint line and no launch line**, for a supervised,
restart-policied process running as UID 0 in the jail. This is the same disclosure shape
[`OQ-TP10`](#-oq-tp10--a-wrapped-plugins-hooks-reach-the-agents-lifecycle-and-appear-in-no-launch-banner)
is open on — in-jail code that arrives with nothing said — with one difference worth a ruling's
attention: TP10's hooks *have* a claim in the wrong class, where this crossing has none to classify.

### 3.3 The config gate is closed, and the scope model it leaves

**This finding was CLOSED on 2026-08-29** (`27b335ce`, built the day it was ruled —
[`scoped-config-approvals.md`](../reference/config-safety.md)
[`OQ-S3`](../reference/config-safety.md#why-its-this-way) and
[`config-safety.md`](../reference/config-safety.md) [`OQ-D2`](../reference/config-safety.md#why-its-this-way)). It read:
*"`CheckConfigChanges` auto-accepts with no snapshot (i.e. a fresh clone), auto-accepts on any
non-TTY, and is skipped on attach."* Verified against
[`snapshot.go`](../../internal/config/snapshot.go#L177) on 2026-09-06, two of the three are false
now: a fresh workspace with declared config **prompts** (diffed against empty; only an empty config
is accepted silently), and a changed config on a non-TTY **refuses** with the diff unless
`--accept-config-changes` is passed for that launch. The third is still true and is deliberate:
attaching to a running jail skips the check, because the container was already started with its
config (`run.go`, the container arm's own comment). Kept here because a reader who finds the old
sentence quoted elsewhere should know which half died.

**What remains true is the scope model.** `mounts` and `env_sources` — both host-read — have **no
scope rule** (re-checked 2026-09-06), so they are declarable in the workspace file an agent inside
the jail can write; the config gate is what stands between that edit and the next launch. The
user-scope-only set, by contrast, has grown well past the four this section used to list: `packs`
(by construction), source-bearing `host_files`, `programs`, `profiles`/`use_profiles`,
`cache_relocations`, `host_wrappers`, `host_apply_on_launch`, `agent_updates`, and the loophole
install, `env`, `doctor_cmd` and `settings` keys (every one a `user-scope only` refusal in
`internal/config`, counted 2026-09-06).

> [!WARNING]
> **And the scope model inverts one level down.** Measured: `/home/agent/.config/yolo-jail` and
> `/workspace/.yolo/home/config/yolo-jail` **share an inode**. So "user scope" — the property the
> pack system argues for at length — holds at the host notch and does not hold inside a jail, which
> is where a nested jail gets its user scope from. This is
> [P1](#p1-trust-flows-downward-and-a-parent-controlling-its-child-is-not-a-finding) working, not a
> finding — recorded so nobody re-measures it as one.

---

## 4. What this says about the proposal

[`pack-execution-trust.md`](../reference/pack-system.md#why-there-is-no-approval-gate) should be read with three corrections:

1. **Its [§3](../reference/pack-system.md#why-there-is-no-approval-gate) premise is false** — no commit pin is
   enforced anywhere ([§1](#1-the-verdict)).
2. **Its permit/refuse table's top row is expressible but not taken.** It used to be flatly
   inexpressible — `npm` could not carry a version through the launcher template — and that was fixed
   on 2026-08-17 ([`npmspec.go`](../../internal/entrypoint/npmspec.go)). Under the evergreen ruling
   no shipped pack should take it; the proposal gained an option, not an argument.
3. **Its scope is too narrow to matter.** It gates execution kinds while `skills`, `briefing` and
   `env` — all of which reach the agent, and `env` of which reaches execution — stay ungated; and
   since 2026-09-04 nothing gates the execution kinds either.

**What survives:** P1's *shape* is right — content-addressing is the only answer to "is this the same
code" — but it is worth building in the three places of [§1](#1-the-verdict) and nowhere else.

> [!NOTE]
> **"P1" in the paragraph above is [`pack-execution-trust.md`](../reference/pack-system.md#why-there-is-no-approval-gate)'s P1** (*a
> fetched pack may cause execution only of content it pins*), **not this document's**
> [P1](#p1-trust-flows-downward-and-a-parent-controlling-its-child-is-not-a-finding) (*trust flows
> DOWNWARD*), which was added later in [§1](#1-the-verdict) and is unrelated. Neither was renumbered —
> both are cited as written, and a rename would break more than it clarifies. The two never appear in
> the same argument; this note exists so a reader who lands here from a grep does not merge them.

---

## Open Questions

> [!NOTE]
> **One question is open.** The nine settled ones live in the [Decision Ledger](#decision-ledger) and
> in the body sections it points at. Three of them keep a heading below — a pointer, not the
> question — because seven other docs link to those headings by slug (counted 2026-09-06); delete a
> heading only after relinking them.

### 💬 [`OQ-TP10`](#-oq-tp10--a-wrapped-plugins-hooks-reach-the-agents-lifecycle-and-appear-in-no-launch-banner) — a wrapped plugin's hooks reach the agent's lifecycle and appear in no launch banner

Opened 2026-09-04 by the [`OQ-TP9`](#decision-ledger) build, which found it while deleting the gate.
**This punctures a claim TP9's own answer makes**, so it is filed rather than absorbed.

**The facts (re-checked 2026-09-06).** A wrapped plugin's `hooks` and `mcpServers` are reported under
`KindSkills` ([`footprint.go`](../../internal/packload/footprint.go#L488-L497)), and
[`packloopholes.go`](../../internal/cli/run/packloopholes.go#L108) classifies that kind
`disclosureSkip`. So they appear in `yolo pack footprint` — an on-demand report nobody runs — and in
**no launch banner at all**. Pinned by `TestWrappedPluginHooksAreDeliveredAndDisclosed` in
[`packnohostgate_test.go`](../../internal/cli/run/packnohostgate_test.go), which asserts the footprint
claim and states the gap in its doc rather than papering over it.

**Why TP9 makes it matter, when it did not before.** Under the old gate a *fetched* pack's
code-running plugin components were refused outright — there was nothing to disclose because nothing
crossed — and an *embedded* pack's were nobody's concern. TP9 removed the refusal on the correct
reasoning that selecting a pack already exceeds what the gate withheld. But TP9 also **kept the
startup banner as the compensating disclosure**, in those words: *"the only place a user sees what a
pack reaches."* That sentence is now false for exactly the contribution that runs code on the agent's
lifecycle events. This is row 21 reopening on the disclosure side — and
[§3.2](#32-jail_daemon-is-a-claim-free-crossing-to-supervised-in-jail-execution)'s `jail_daemon` is
the same shape with no claim at all, which a ruling here should say it does or does not cover.

**What it decides:** whether the banner's coverage is completed, or its claim is narrowed.

| | Candidate | Cost |
| :--- | :--- | :--- |
| **(a)** | Give plugin claims their own disclosure class, so hooks and `mcpServers` render on the banner beside mounts and host reads | The honest completion of TP9's kept half. Costs a class and a rendering decision — a hook body is not a path, so "what is touched, which direction, whose machine" needs a spelling for it |
| **(b)** | Qualify the claim — say the banner covers host crossings, and point at `yolo pack footprint` for in-jail code | Free, and dishonest in the way TP9 objected to elsewhere: it leaves the compensating disclosure not compensating |
| **(c)** | Reclassify `KindSkills` off `disclosureSkip` wholesale | Cheapest to write, worst to read — a pack's skills tree is prose an agent reads, and announcing every skill file would bury the hooks in the noise that made `disclosureSkip` right in the first place |

<!-- vantage: oq id=OQ-TP10 leaning="(a) — give plugin claims their own disclosure class, so hooks and `mcpServers` render on the banner beside mounts and host reads. TP9's argument was about authority, not visibility; keeping the banner as the compensating disclosure while leaving a hole in it is the shape this census exists to catch. (c) is the tempting cheap version and destroys the signal; (b) is acceptable only if (a) turns out to have no honest rendering." -->

_Leaning:_ **(a).** TP9's argument was that the gate withheld nothing the user had not already granted
— which is true of *authority* and says nothing about *visibility*. Disclosure was the half TP9 kept
precisely because it is not consent; keeping it while leaving a hole in it is the shape this census
exists to catch. (c) is the tempting cheap version and it destroys the signal. (b) is only acceptable
if (a) turns out to have no honest rendering, which should be discovered rather than assumed.

**Answer:**
> _(empty — fill in when decided)_

### ⛔ [`OQ-TP7`](#-oq-tp7--yolo-check-cannot-predict-the-fatal-refusal-and-the-refusal-names-a-fix-that-needs-a-tty-and-a-network--retired-2026-09-04) — `yolo check` cannot predict the fatal refusal, and the refusal names a fix that needs a tty and a network — RETIRED (2026-09-04)

**Retired, not answered — compacted 2026-09-06.** [`OQ-TP9`](#decision-ledger) deleted the refusal
this question was about, so both of its measured gaps dissolved. The ruling is the
[ledger row](#decision-ledger); the part that outlives it — the **third-gate trap**, and the shape
to check any future fatal against — is the last warning in
[§3.1](#31-a-refused-contribution-refuses-the-launch-). *("The preflight" in the old text meant the
`yolo check` command, not `stagePacks`' mechanical pre-flights on the launch path.)*

### ✅ [`OQ-TP9`](#-oq-tp9--is-the-fetched-pack-approval-prompt-a-gate-or-theatre--resolved-2026-09-04) — is the fetched-pack approval prompt a gate, or theatre? — RESOLVED (2026-09-04)

**Theatre — deleted; compacted 2026-09-06.** The ruling and its same-day correction are the
[ledger row](#decision-ledger); the argument (Test 1, the refuted containment rationale, the prior
art, what was kept) is [§3.1](#31-a-refused-contribution-refuses-the-launch-); the lockfile
correction (content is frozen between installs; the follow-on was documentation, not enforcement) is
[§1](#1-the-verdict). Built in `3d6ae7be`.

### ✅ [`OQ-TP8`](#-oq-tp8--pack-shipped-lua-runs-ungated-on-both-sides-of-the-boundary--is-that-a-ruling-or-an-accident--resolved-2026-09-04) — pack-shipped Lua runs ungated on both sides of the boundary — is that a ruling or an accident? — RESOLVED (2026-09-04)

**A ruling — ungated, both halves; compacted 2026-09-06.** The ruling is the
[ledger row](#decision-ledger); the facts are [row 26](#2-the-inventory); the parity check that
overturned the leaning's host-half gate, and what would reopen the question, are
[the pack-Lua section](#pack-shipped-lua-is-ungated-on-both-sides-and-that-is-the-ruling) under
[§2](#2-the-inventory).
