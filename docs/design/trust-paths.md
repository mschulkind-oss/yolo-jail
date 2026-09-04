---
title: "Every path by which someone else's content runs in your jail"
date: 2026-09-03
status: in-review
tags: [trust, packs, security, inventory]
summary: "Twenty-six paths, enumerated from the code, each with when trust is extended and whether the content can change afterwards. The answer to 'where does pinning even help' is: three of them — and as of 2026-09-03 the npm arc is gone from this doc entirely, because agent CLIs are ruled evergreen in program-delivery.md §3.5. What is left is the census and three findings."
---

# Every path by which someone else's content runs in your jail

**Status:** INVENTORY, 2026-08-17; **compacted 2026-09-03.** Six questions settled, and **two of the
rulings SHIPPED and are still in the tree** — OQ-TP5 (`b3a29ad8`) and OQ-TP6 (`6385dfbb`), both
2026-08-18, both re-verified against the code **2026-08-23**, anchors repinned **2026-09-02** (the
provider arc moved several files under them; every behaviour is unchanged). **Nine questions settled and NONE open** as of 2026-09-04 — OQ-TP8 ruled ungated, OQ-TP9 deleted the
fetched-pack approval prompt as theatre, and OQ-TP7 was RETIRED because TP9 removed its subject.
Beyond the rulings,
**everything here is inventory** — traced in the code, with the anchors inline.

> [!IMPORTANT]
> **The npm arc left this document on 2026-09-03, and that is the largest change it has had.**
> OQ-TP3 and OQ-TP4 are **RETIRED** and OQ-TP5 is **SUPERSEDED** — see the
> [Decision Ledger](#decision-ledger). The reason is a ruling made elsewhere:
> [`program-delivery.md`](./program-delivery.md) §3.5 draws a boundary this document never had, between
> a dependency that serves the **agent** and one that serves the **project**, and rules the agent
> class **evergreen**. All four packs OQ-TP5 governed (pi, copilot, codex, opencode) are agent CLIs,
> so *"no evergreen npm"* is now false of every member it had. There is no npm pin to place, no
> lockfile row to design, and no install/update split to enforce.
>
> **What that leaves is what this document should always have been:** the census, the verdict about
> declarations-vs-content, and the three findings in §3 — none of which the ruling touches.
> Roughly a hundred lines of npm argument came out; nothing else moved.
>
> ⚠ **`§1 row 1` is still a live anchor and was deliberately NOT renumbered.** Six code sites cite it
> (`internal/cli/pack.go:190`, `internal/cli/packupdate.go:9`, `internal/entrypoint/shims.go:622` and
> `:729`, `internal/cli/packupdate_test.go`, `internal/entrypoint/npmlauncher_test.go`). Those
> comments describe code that still behaves exactly as they say; they go stale when the evergreen
> work lands, and that commit owns updating them.

**The short version.** Twenty-six paths deliver someone else's content into a jail.
**Pinning changes an outcome in three of them** ([§1](#1-the-verdict)); everywhere else it is
theatre, because **every gate in this system keys on a DECLARATION and none on CONTENT.** That
verdict is unchanged by the evergreen ruling — it removed one row's *question*, not the property the
census measures.

**Why this exists.** A proposal ([`pack-execution-trust.md`](./pack-execution-trust.md)) argued that
a fetched pack should only execute content it pins. The review response was *"they're all just as
weak. anything can be an installer. we're ultimately extending trust somewhere. I'm not entirely
sure where this pinning even helps."* That is right, and this document is the ground truth the
proposal should have been built on.

> [!IMPORTANT]
> **The proposal's central premise is false, and I verified it myself.** §3 of
> [`pack-execution-trust.md`](./pack-execution-trust.md) says the commit pin "is the rule already
> applied one level up". It is not applied anywhere. See
> [§1's lockfile finding](#the-lockfile-is-a-receipt-not-a-gate) — which is also the crux of both
> npm questions.

**What pinning actually buys, stated honestly:** it bounds trust in **time**, never in scope. It
cannot make code safe; a pinned malicious binary is malicious. Its only claim is *"the thing you
approved is the thing that runs, and you will be asked again when it changes"* — which defends
against exactly one threat, the silent update.

---

## Decision Ledger

| ID | Ruling / Decision | Date | Settled in |
| :--- | :--- | :--- | :--- |
| **OQ-TP1** | **Obviated.** There is no decision to carry into a jail, because a refused contribution refuses the launch (OQ-TP6). The hardcoded `mayAccessHost=true` stays — deriving it would be a regression | 2026-08-18 | [§3.1](#31-a-refused-contribution-refuses-the-launch-) |
| **OQ-TP2** | **Nothing explicit.** Agent context needs no gate and no separate disclosure — the lockfile's commit pin closes over it, because it closes over the whole tree | 2026-08-18 | [§2](#agent-context-needs-no-gate-of-its-own) |
| **OQ-TP3** | **RETIRED, not answered.** *"Is pinning worth building, and where first?"* — its ranking put npm first, and npm no longer takes a pin. Its still-open half (*must a pack pin, or merely may it?*) was inherited at wider scope as `program-delivery.md` OQ-PD6 and ruled there: the receipt is the pin, for **project** dependencies; an agent dependency has no pin to obey | 2026-09-03 | [`program-delivery.md`](./program-delivery.md) §3.5, OQ-PD6 |
| **OQ-TP4** | **RETIRED as posed.** *"Where does an EMBEDDED pack's npm version get pinned?"* — nowhere, because it is not pinned at all. Its three options (manifest / lockfile / user config) were all venues for a record that the evergreen ruling deletes the need for. **What must not be re-derived:** option (a)'s cost — pinning in the manifest makes yolo's release cadence the ceiling on agent-CLI freshness — which is the same objection `program-delivery.md` §5.1 hits, and is now an argument *for* the ruling rather than a cost of one option | 2026-09-03 | [`program-delivery.md`](./program-delivery.md) §3.5, OQ-PD12 |
| **OQ-TP5** | **No evergreen npm.** `install` obeys the lockfile; `update` is the only act that resolves a new version; the hourly poll may only *report*. **Built 2026-08-18 (`b3a29ad8`)**, minus the pin it had nowhere to record. ⚠ **SUPERSEDED 2026-09-03 by `program-delivery.md` OQ-PD12**: all four packs it governed are agent dependencies, which are now ruled **evergreen**, updated on the boot path at every launch. The ruling stands as a description of the code **as it is today** — the reversal is ruled, not built | 2026-08-18 · superseded 2026-09-03 | [§1 row 1](#where-a-pin-would-change-the-outcome) |
| **OQ-TP6** | **A refused contribution is a refused launch.** No partial packs — fix the pack, remove the pack, or approve it. **Built 2026-08-18 (`6385dfbb`)**. Untouched by the evergreen ruling — it is about consent, not cadence | 2026-08-18 | [§3.1](#31-a-refused-contribution-refuses-the-launch-) |
| **OQ-TP8** | **Ungated, both halves — a recorded ruling, not an accident.** Pack `derive.lua` keeps running with no origin check, in-jail at boot and host-side under `yolo host -- <cmd>`. The leaning's host-half gate fails a parity check: `host.go:458` folds each pack's **static** `kind: "env"` keys into the same process's environment one step EARLIER, ungated — so the derive computes a field the manifest can already state literally, and gating the computed path while the literal one is open is theatre. A pack also renders `config`/`skills`/`briefing` into the real home at that notch. The disclosure is the commit pin (OQ-LP8), not a claim line. Reopens if the VM gains I/O, exec, network or an unbudgeted loop, or if `ctx` grows a field static `env` cannot carry | 2026-09-04 | [§12 OQ-TP8](#-oq-tp8--pack-shipped-lua-runs-ungated-on-both-sides-of-the-boundary--is-that-a-ruling-or-an-accident--resolved-2026-09-04) |
| **OQ-TP9** | **The fetched-pack approval prompt is THEATRE — deleted.** Selecting a pack means writing user-scope config as the host user (`packs` is inexpressible at workspace scope *by construction*), so the gate refuses an actor who has already passed a stronger one — [`gate-placement-principle.md`](gate-placement-principle.md) Test 1, already applied this way to the sibling `--user-layer` route. Its original containment rationale was refuted in-house by `pack-execution-trust.md` §2 (ungated `npm postinstall` is the same arbitrary in-jail execution). **Keep** `packs` user-scope-only (that half PASSES Test 1) and the startup disclosure banner; ⚠ **CORRECTED same day:** the pin is effectively honored already (a launch resolves from the local mirror, which only moves at `pack install`), so the follow-on is OQ-LP8's two undelivered DOC requirements, not enforcement; deleting the gate makes the lockfile write-only at launch, and **G2b is moot**. ⛔ Retires OQ-TP7 | 2026-09-04 | [§12 OQ-TP9](#-oq-tp9--is-the-fetched-pack-approval-prompt-a-gate-or-theatre--resolved-2026-09-04) |

> [!NOTE]
> **Both builds re-verified in the tree 2026-08-23, by anchor rather than by commit — anchors
> repinned 2026-09-02** (the install-receipts feature and the provider arc pushed `shims.go` and
> `run/packs.go` down by hundreds of lines; every quoted behaviour is unchanged at its new line).
> A merged commit is not evidence that the behaviour is still there, so:
> **TP5** — [`shims.go:680-687`](../../internal/entrypoint/shims.go) is `_poll_and_report`, which runs
> `npm view` and *prints* `"<installed> → <latest> is available. Run 'yolo pack update'"`; the only
> resolving path is `_update`, reachable solely through
> [`shims.go:743-755`](../../internal/entrypoint/shims.go)'s `YOLO_PACK_UPDATE=1` guard, which exits
> instead of exec'ing; `yolo pack update` is the one setter
> ([`packupdate.go:60`](../../internal/cli/packupdate.go)); the cold branch
> ([`shims.go:757-766`](../../internal/entrypoint/shims.go)) is still deliberately untouched.
> **TP6** — [`run/packs.go:228`](../../internal/cli/run/packs.go) accumulates `packRefusals(p)` and
> `:247` returns `refusedLaunchError`, ahead of the mechanical pre-flights; the message itself is
> [`packrefusal.go:104-119`](../../internal/cli/run/packrefusal.go) and still names the pack, the
> claim and all three ways out.

> [!WARNING]
> **This document's questions were renumbered on 2026-08-18, and the reason is worth keeping.** They
> were `OQ-T1..T4` and collided with [`loophole-transport.md`](loophole-transport.md), which already
> owned `OQ-T1..T9` — and *those* are the ones cited from code, by name:
> *"loophole-transport.md OQ-T2"* (`loopholescmd.go:195`), *"loophole-transport.md OQ-T5"*
> (`macosuser.go:388`), `OQ-T7` (`svcendpoint/doc.go:44`).
>
> Two docs answering to one ID space is worse than a rename: a reader grepping `OQ-T3` landed in
> whichever file they opened first. This doc yielded because its IDs were cited only from
> `roadmap.md`, which moved in the same commit; the transport's are cited from three code files and
> did not move. **A stale `OQ-T1..T4` referring to trust-paths therefore means "written before
> 2026-08-18" — it is not a dangling reference, it is an old spelling of `OQ-TP1..TP4`.**

> [!NOTE]
> **The section numbers are an API too.** `§1 row 1` is cited from `internal/cli/packupdate.go`,
> `internal/cli/pack.go`, `internal/entrypoint/shims.go` and two tests; `§3.1` from
> `internal/cli/run/packrefusal.go`, `internal/entrypoint/packsurfaces.go`, `internal/cli/run/packs.go`
> and `docs/RELEASE-NOTES.md`. Renumber a section only with a grep in hand.

## 1. The verdict

**Pinning changes an outcome in three of twenty-six paths.** Everywhere else it is theatre, and the
reason is structural: **every gate in this system keys on a DECLARATION** — a URL, a path, a
component name, a config value — **and none on CONTENT.** That is a coherent design. It answers *"did
the footprint grow?"* well. It cannot answer *"is this the same code I looked at?"* at all.

**And the sharpest form of your instinct, which I had not seen:** `pack install` syncs the mirror and
writes the lockfile **in the same loop iteration**
([`pack.go`](../../internal/cli/pack.go#L1104-L1143) — `store.Sync` at `:1104`, the `lock.Set`
write now at `:1140-1143`). The act that moves the content *is* the act
that moves the pin. A pin advanced by the same command that changes the bytes is a receipt. It
becomes a gate only if three things hold together — (i) enforced at use, (ii) advanced by a
*different* act than the one that changes content, (iii) that act shows you what changed. Today
**none** hold, and nobody is proposing to fix (ii).

### The lockfile is a receipt, not a gate

This is the finding both open questions turn on, so it gets stated once, here, rather than repeated
per row. `LockEntry` ([`lock.go`](../../internal/packsrc/lock.go#L33-L53)) records `Name`, `Source`,
`Commit`, `Ref` and `ApprovedHostAccess`. Its two halves behave completely differently:

| Field | Enforced at launch? | Evidence |
| :--- | :--- | :--- |
| `ApprovedHostAccess` | **Yes** — `packMayAccessHost` ([`run/packs.go`](../../internal/cli/run/packs.go#L876)) grants a fetched pack host access only for claims recorded here, and a claim set that grew re-prompts | this is a real gate |
| `Commit` · `Ref` | **No.** Every reader is **display-only**: the moved-pin message ([`pack.go:1121-1126`](../../internal/cli/pack.go#L1121-L1126)) and the `pack status` listing ([`pack.go:1350`](../../internal/cli/pack.go#L1350)). The launch path never consults either — it re-resolves the **config's ref** against the local mirror | verified 2026-08-18, **still true 2026-09-02** (anchors repinned): four readers, all printing |

That split is **OQ-LP8 / G2b**, already open and already ruled in shape. It is the same shape the
origin gate had before [§3.1](#31-a-refused-contribution-refuses-the-launch-): *true of the decision,
false of its enforcement.*

**Two structural facts about the file itself.** They were the origin of OQ-TP4, which is now
retired — kept because they remain true of the lockfile and constrain anything built on it:

- **There is nowhere to put an npm version.** `LockEntry`'s fields are everything about a *git* pin
  and nothing about a package one. It needs a new field, and `LockSchema` is versioned precisely so
  this kind of change is a bump rather than a silent misread.
- **The file exists per FETCHED pack.** `Commit` is *"empty for a local pack — a directory has no
  commit, and pretending otherwise would invent a pin"*
  ([`lock.go`](../../internal/packsrc/lock.go#L39-L41)), and an embedded pack has no row at all
  (`ApprovedHostAccess` is *"unused for embedded/local packs"*). The four packs that declare npm
  programs — **pi, copilot, codex, opencode** — are all embedded.

### Where a pin would change the outcome

1. **`program via npm`** — because nothing *is* pinned. Every shipped pack declares a bare package
   name, which the launcher resolves to `@latest`.

   > [!IMPORTANT]
   > **SUPERSEDED 2026-09-03 — this row no longer asks for a pin, and the anchor is kept only
   > because code cites it.** OQ-TP5 ruled *no evergreen npm* on 2026-08-18 and it was built
   > (`b3a29ad8`): the hourly poll became informational, and `YOLO_PACK_UPDATE=1` — which only
   > `yolo pack update` sets — became the sole path that resolves a version. **That is still exactly
   > what the code does**, which is why the six code comments citing `§1 row 1` are accurate today.
   >
   > What changed is the ruling above it. [`program-delivery.md`](./program-delivery.md) §3.5
   > classifies all four npm-declaring packs (pi, copilot, codex, opencode) as **agent
   > dependencies** and rules that class **evergreen** — updated on the boot path at every launch,
   > with no pin, because there is nothing for an agent CLI to be reproducible against. So this
   > row's open question (*where does the resolved version get recorded?*, OQ-TP4) is retired
   > unanswered: nothing records it because nothing needs to obey it.
   >
   > **Two measurements settled it**, both 2026-09-03. The mechanism OQ-TP5 built has never fired
   > in steady state — the launcher hosting it sits last on `PATH` and is shadowed by the real
   > binary the moment the first install lands (`program-delivery.md` OQ-PD8). And the four agents
   > it governs were **six weeks stale**: copilot 1.0.48 against 1.0.82, codex 0.145.0 against
   > 0.153.1, pi 0.82.1 against 0.84.4. The silent update the ruling defended against never
   > happened; the freeze it caused did.

   > [!WARNING]
   > **npm installs are deliberately NOT origin-gated, and NEITHER ruling changes that.**
   > `HonoredInstalls` ([`packload.go`](../../internal/packload/packload.go#L493-L516)) gates a
   > `curl`-piped installer and lets an npm install through, on the reasoning that a registry package
   > is *"the same trust as any dependency the user already installs."* That reasoning should stay:
   > this row is about **when the bytes change**, not about **whose bytes they are.**
   >
   > This becomes load-bearing under `program-delivery.md` OQ-PD13, which prefers a vendor's native
   > installer over npm for agent CLIs wherever one exists. Flipping a pack's `via` from `npm` to
   > `installer` moves that contribution from **ungated** to **approvable and refusable**
   > ([§3.1](#31-a-refused-contribution-refuses-the-launch-)). For the embedded packs that ship
   > today it is moot — embedded origin grants unconditionally — but the first *fetched* pack to
   > ship an agent CLI inherits a prompt it would not have had under `via: npm`.

   **A version is expressible, and nothing takes it.** The launcher splits the declaration
   ([`npmspec.go`](../../internal/entrypoint/npmspec.go)) and honours a version, dist-tag or range,
   skipping the poll for anything it did not resolve to `latest` itself. (It used to append `@latest`
   unconditionally, so `foo@1.2.3` yielded `foo@1.2.3@latest`.) That was a bug fix and **not** a
   decision, and under the evergreen ruling no shipped pack should take it.
2. **A loophole's daemon FILE, and a plugin's HOOK BODIES** — the two gated crossings whose approval
   string genuinely does not cover the bytes. `["python3","{loophole_dir}/acme.py"]` is one claim
   string forever; `plugin <name> hooks (runs code at agent lifecycle events)` is a **constant** with
   no path and no digest in it. This is OQ-LP8/G2b, already open on purpose.

   > **"Plugin" and "hook body", defined — they are the AGENT's extension mechanism, not yolo's.**
   > A pack may ship a **Claude Code plugin**: a `.claude-plugin/plugin.json` manifest that the agent
   > reads directly. yolo delivers it and reports what it declares, but never interprets it. A
   > manifest can declare six component kinds, and yolo marks three of them as running code
   > ([`pluginpack.go`](../../internal/pluginpack/pluginpack.go#L130-L147)):
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
   > **That is exactly why the claim is uncoverable.** The approval string yolo shows is the table row
   > above, verbatim and constant. It names the *category*, not the file — so a plugin can rewrite its
   > hook script and the string a user approved is byte-identical. Compare a loophole's `command`,
   > which at least names a path: that one is uncovered because the path's *contents* move, where this
   > one has no path in it at all.
3. **Editing `?ref=` in config without reinstalling.** The mirror already holds every branch and tag,
   so a config-only edit resolves offline at the next launch and delivers new content with **no
   install, no network and no prompt**. `pack status` calls this drift; nothing on the launch path
   consults it.

   > **What `?ref=` is.** A fetched pack is named by a URL-shaped *address* in your user config, and
   > `?ref=` is the query parameter on it that selects which git ref to use
   > ([`addr.go`](../../internal/packsrc/addr.go#L45-L60)):
   >
   > ```
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
   > **directly, not from the merged config** ([`packs.go`](../../internal/config/packs.go#L1-L20)) —
   > so a workspace file cannot name a pack even to be refused. The package comment states the reason
   > in this document's own terms: *"a workspace config travels with the repo and is agent-editable,
   > so it must not be able to name content that enters the jail."*
   >
   > And an agent cannot reach the file where it *is* expressible: the host's
   > `~/.config/yolo-jail/config.jsonc` is **never mounted into a jail**, and the `config.jsonc` a
   > jail sees at that path is **generated per consumer** from the merged result (`assemble_test.go`
   > pins both facts: *"user config mount: none"*). Editing the in-jail copy changes a generated
   > artifact and nothing else.
   >
   > The gap is real but narrower than it reads: a **person** who edits `?ref=` gets new code with no
   > re-approval and no prompt, because nothing on the launch path compares the ref they are now
   > running against the ref they approved. Same missing enforcement as the lockfile finding above.

### P1. Trust flows DOWNWARD, and a parent controlling its child is not a finding

**Each level is the host for the next: user → jail → nested jail.** A user configures their jail; a
jail configures the jails it launches. That is the model, not a leak in it, and this principle exists
because the inventory below will otherwise keep growing rows that are the model working.

**The worked example, since it looks alarming until the direction is stated.** The implicit local
pack directory — `~/.config/yolo-jail/local`, which needs no config line, is appended last so it
outranks every configured entry, and carries `file://` origin and therefore unconditional host access
— **is writable from inside a jail.** Measured 2026-08-18: `/home/agent` is a `ro` bind of host-side
state, but `/home/agent/.config` is a **rw** bind of `<workspace>/.yolo/home/config`, with only
`config.jsonc` and `inherited-launch.jsonc` pinned `ro` file-by-file. So an agent can create that
directory, and a **nested** launch — whose `yolo` runs inside this jail and therefore resolves
`LocalPackDir()` to this path — loads it at full local-pack authority with no prompt.

**That is correct.** An agent that can already run arbitrary code in this jail configuring a jail it
launches is exactly the direction trust is supposed to travel. Refusing it would mean a jail could
not set up its own children, which is the dev loop this repo runs on.

**What the principle does NOT license**, and the boundary that stays load-bearing:

- **Nothing flows upward.** The host's own `~/.config/yolo-jail/` is not mounted into a jail at all —
  verified in the same measurement — so none of this reaches the machine. A path that let a jail
  change what its PARENT runs would be a finding, and a serious one.
- **It is not a licence to stop gating what enters from OUTSIDE.** A fetched pack's content is
  someone else's, at every level. P1 is about the *relationship between levels*, not about origin.

> [!NOTE]
> **This principle is why a measurement can be true and still not be a finding.** "The directory is
> writable" is a fact; "therefore it is a hole" needs the direction of trust, and downward is the
> permitted one. Anyone re-deriving the measurement should stop here rather than filing it.

### Where it is theatre — the four that matter

- **Pinning a pack tree at all**, in the dominant case: see the same-loop-iteration finding above.
- **Pinning execution kinds while `skills` and `briefing` are ungated.** A fetched pack can rewrite
  every `SKILL.md` and every line of briefing prose with no claim, no prompt, no lockfile entry and
  no launch disclosure — they are classified `disclosureSkip` as "jail-internal by construction". A
  skill that says *"run this command"* is an execution path with extra steps.
- **Pinning anything while `~/.config/yolo-jail/local` exists.** The implicit local pack needs no
  config line, has no lockfile entry, no commit, no claim, gets **full trust**, and is appended
  **last** so it outranks everything — selected by one `os.Stat` that follows symlinks.
- **Pinning a refusal that is not enforced where it executes.** Retired as of §3.1's ruling, and kept
  in this list because it is the shape to check any *new* gate against.

---

## 2. The inventory

Ordered from most-trusted origin to least. "Silent change" is the column the exercise exists for, and
this table is the evidence for §1's "three of twenty-six" (row 26 arrived 2026-09-02 and pinning
does not move it — a derive is inside the commit the lockfile already closes over).

| # | Path | Grants | Trust extended | Can change silently? |
| :-- | :--- | :--- | :--- | :--- |
| 1 | the yolo binary — built-in skills + composed briefing | agent context | never | only via your own upgrade |
| 2 | **embedded pack `program via installer`** (claude, agy) | in-jail exec as UID 0 | **never** — embedded origin grants unconditionally | **yes, and as of 2026-09-03 that is the RULING, not a gap** — agent CLIs are evergreen (`program-delivery.md` §3.5). Two independent movers: the URL's bytes, and the vendor's own updater. ⚠ *The old text said "the vendor's own **hourly** self-update"; measured 2026-09-03 that is wrong twice over — yolo's launcher calls `"$REAL_BIN" install` on an hourly stamp, not the vendor, and with no `--force` and no target it is a **no-op when already installed**. Claude in this workspace had not moved since 2026-07-24* |
| 3 | **`program via npm`** — any pack, any origin | in-jail exec (postinstall + deps) | **never**, for any origin | **as the code stands: no.** The hourly poll only reports and `yolo pack update` is the only act that resolves (OQ-TP5, built 2026-08-18). ⚠ **Ruled to change**: agent CLIs become evergreen and resolve at every launch (`program-delivery.md` OQ-PD12), and the four packs on this row are all agent CLIs. Measured 2026-09-03, the mechanism has never fired — the launcher is `PATH`-shadowed, and all four were six weeks stale |
| 4 | `flake.nix` / `flake.lock` | in-jail exec (everything on PATH) | implicit, at PR merge | no for inputs (locked revs, hermetic build) |
| 5 | **the implicit local pack** `~/.config/yolo-jail/local` | everything, at maximum trust | **never**, and deliberately | **yes, continuously** — live dir, re-read every launch, no record |
| 6 | explicit `file://` local pack | same as 5 | implicit in the config line | yes, every launch — no copy, no hash |
| 7 | `--user-layer` / `YOLO_USER_LAYER` | the full user scope | never, by explicit ruling | re-read per invocation; inert unless named |
| 8 | user-scope `loopholes.<name>.command` | **host execution** | never, by the same ruling | yes for the bytes — the config pins an argv, nothing reads the program |
| 9 | workspace `mounts` | host read | implicit at the config diff; **never on a fresh clone** | yes — `git pull`, the agent's own edit, or the host dir's contents |
| 10 | workspace `env_sources` | host read, exfiltration-shaped | implicit; never on a fresh clone | yes — re-read live each launch; a missing file warns and skips |
| 11 | workspace `mcp_servers` / `lsp_servers` / `packages` / `mise_tools` | in-jail exec | implicit at a diff that shows the NAME, never what it resolves to | mixed — the most useful contrast in the table |
| 12 | **the config gate itself** (`CheckConfigChanges`) | — it *is* the gate | — | **fails open three ways in 40 lines** (§3.3) |
| 13 | workspace `yolo-jail.config.lua` — **activated by existing** | agent context, transitively in-jail exec | **never**; not a config key, so outside the diff, drift and snapshot | yes, every boot, with nothing to diff against |
| 14 | workspace `mise.toml` | in-jail exec | **never** — trust asserted *for* you on the podman argv | yes — `git pull`, and `latest` resolves at install |
| 15 | `agents_md_extra`, blocked-tool messages, source-less `host_files` | agent context | implicit at the diff, which does carry the prose | covered by the diff; the finding is scope asymmetry |
| 16 | **`.yolo/handover.md`** | agent context, framed as an authoritative task list | **never** — no key, no prompt, no validation, no attribution | **yes, continuously** — an ordinary file any agent can write |
| 17 | fetched pack — **content** (skills, briefing, files, config-overlay) | agent context | **never** for a claim-free pack | yes, on every mechanism at once |
| 18 | fetched pack — `env` | in-jail exec in practice (no key allowlist, so `LD_PRELOAD` etc.) | **never**, explicitly | yes; no claim, so nothing to compare |
| 19 | **fetched pack — loophole with only a `jail_daemon`** | in-jail exec, supervised, restart-policied, UID 0 | **never** — excluded from the claim table by design | yes trivially; it was never approved (§3.2) |
| 20 | **fetched pack — `program via installer`** | in-jail exec as UID 0 | explicit prompt, and since 2026-08-18 an unapproved one refuses the launch (§3.1) | yes — unpinned URL plus hourly self-update |
| 21 | fetched pack — wrapped agent plugin (hooks / MCP / LSP) | in-jail exec at lifecycle events | explicit prompt for the code-running components — **first enforced at launch 2026-08-18** (§3.1) | **yes — the weakest claim string in the system**, a constant with no path or digest |
| 22 | fetched pack — `reads-host` / `mount` / host-prepending `briefing` | host read | **explicit, once** | yes — a moved ref with unchanged claim strings carries the approval forward |
| 23 | fetched pack — loophole with a `host_daemon` | **host execution** + a CA trusted in-jail | explicit, per crossing | yes — the claim pins the argv, not the file (OQ-LP8) |
| 24 | `yolo host apply` | **host write** into your real home | explicit per invocation, `--assert` required | for a local pack, yes — source re-read each apply |
| 25 | the mirror + ref resolution behind rows 17–23 | selects which bytes every row above delivers | — | **three verified mechanisms** |
| 26 | **any pack's `derive.lua`** (`yolo.derive` + `yolo.env`) — **row added 2026-09-02**; this census had no entry for pack-shipped Lua, the gap 💬 18's D9 filed | **sandboxed Lua execution** — in-jail at every boot with live tables (`packsurfaces.go:193`); **host-side** during `yolo host apply` as a sentinel-input key-name probe (`hostrender.go:377`); and — since `3144fbed`, the same day this row was written — **host-side at every `yolo host -- <cmd>` launch with REAL inputs**: `packload.AgentEnv` runs the pack's env derive over the resolved provider table, credential included (`internal/cli/host.go:458`; the jail-launch twin is `run/profilechannel.go:97`). The VM is allowlist-built (no `os`/`io`/`require`/`load`, fresh state, timeout — `agentcfg/luahook/vm.go`), so the grant is *unvalidated config-surface and env output* plus whatever `ctx` carries — under 💬 18's OQ-PT9 ruling, resolved provider credentials — **not** process exec | **never, any origin** — `DeriveScript` reads `<pack root>/derive.lua` with no origin gate and no claim (`packload/deriveenv.go:35`), while the same fetched pack may not *name a host file to read* | yes — the mirror re-resolves, and a derive is content, not a claim |

### Agent context needs no gate of its own

**RULED (OQ-TP2, 2026-08-18): nothing explicit.** Skills, briefing prose and the rest of the
agent-facing surface (rows 17, 15, 16) get no gate and no separate disclosure line, because **the
lockfile already pins a commit, and a commit closes over the whole tree** — prose included. A second
mechanism aimed at the same bytes would be the halfway-measure shape this repo keeps deleting.

**The scope is exactly right, which is worth stating because it looks narrower than it is.** A commit
pin covers *fetched* packs only. That is not a gap: **fetched packs are the only ones whose content
someone else controls.** A local pack is your own files under your own authority, and an embedded one
is yolo's own code. (An embedded pack's *tree* is yolo's own code; the npm **package** it names is
not — which is why OQ-TP4 used to sit alongside this ruling. It is retired: that package is an
**agent dependency** and is now ruled evergreen, so no pin covers it and none is wanted.)

**This ruling inherits the enforcement gap**, and is worth exactly as much as that gap is closed:
until `LockEntry.Commit` is consulted at launch ([§1](#the-lockfile-is-a-receipt-not-a-gate)), "the
pin covers it" is a statement about the design rather than about a running system.

---

## 3. Three findings that outrank the entire pinning question

### 3.1 A refused contribution refuses the launch ⚠

**RULED and BUILT 2026-08-18 (OQ-TP6, which obviates OQ-TP1).** *"If the installer is refused, that
should be fatal. We can't run packs with selective things disabled by refusals. Fix the pack, remove
the pack, approve. Those are the choices."*

This began as the only verified break of a guarantee the codebase actively claims: the host computed
a refusal, printed `Warning: refused installer …`, and then staged the unmodified `pack.json` anyway,
so the jail — which loads packs with a hardcoded permissive `mayAccessHost` — wrote the `curl → bash`
launcher regardless. The warning was true about the *decision* and false about the *outcome*. **The
ruling does not close that gap; it deletes the problem.** There is nothing to carry across the
boundary if no jail starts.

`stagePacks` now collects every refusal the `Honored*` family reports and returns
[`refusedLaunchError`](../../internal/cli/run/packrefusal.go) instead of the four warnings it used to
print — **before** the mechanical pre-flights, because this one is about CONSENT and those are about
pack mechanics. Refusals accumulate across the whole configured set, so two broken packs cost one
launch rather than two.

**It also retires the partial-pack concept**, which is the deeper change. A pack that half-loads is a
pack whose behaviour nobody can predict from reading it: the manifest says one thing, the running
system does another, and the difference is a warning scrolled past ten minutes ago. The three choices
— **fix the pack, remove the pack, approve it** — are exhaustive precisely because they are the only
three that end with the manifest and the runtime agreeing.

#### What the gate decides, and what must not be simplified away

**Origin decides exactly one thing** in this system — the package comment is explicit that a user
pack and an official pack are the same kind of thing, and that *"the only difference is ORIGIN, and
origin decides exactly one thing — whether a host-access declaration is honored."*

| Origin | What it is | May reach the host |
| :--- | :--- | :--- |
| **embedded** | compiled into the yolo binary (`packs/*`) | ✅ always — it *is* yolo |
| **local** | `file:///path/to/pack` on your own disk | ✅ always — your own files, your own authority |
| **fetched** | `git+https://…?ref=…`, content someone else controls | ⚠️ **only what you approved** |

`MayAccessHost` is that verdict, carried on the loaded pack. For the first two it is `true` by
construction ([`packs.go`](../../internal/config/packs.go#L182):
`MayGrantHostFiles() { return p.Origin() != OriginFetched }`). For a **fetched** pack it is decided
per launch by `packMayAccessHost` ([`run/packs.go`](../../internal/cli/run/packs.go#L876)), and it is
`true` only when the lockfile records approval for **every** host-access claim the staged pack
*currently* makes: a fresh install never run through `yolo pack install` **fails closed**; a pin that
moved and **gained** a claim fails closed and re-prompts; a missing or corrupt lockfile **approves
nothing**. So the gate is not *"fetched packs may never"* — it is *"a fetched pack reaches the host
only for the things you were shown and said yes to."* The claims are strings computed from the
manifest ([`contributes.go`](../../internal/packdecl/contributes.go#L655-L671)) — `reads-host <path>`,
`mount <host> -> /ctx/<into>`, `briefing <src>`, and **`installer <URL>`** — and they gate host
directory mounts, `curl`-piped installers, a wrapped plugin's code-running components, and a shipped
loophole's daemon, intercepts, binds and devices. All of them flow through one merged helper on
purpose: **both ends of the approval must compute the same union, or the gate disagrees with the
prompt.**

Two properties of the refusal are load-bearing, and any future change must preserve both:

- **It is PER CONTRIBUTION, not per pack.** A pack may mix an npm install with a `curl`-to-shell
  installer, and only the second is gated. Deciding once for the whole pack is worse in both
  directions: it would either refuse the innocent npm install, or — *"far worse"* — let a fetched
  pack **smuggle an installer URL through beside one**.
- **An npm install is deliberately ungated.** *"An npm install names a registry package and is not
  origin-gated — it is the same trust as any dependency the user already installs."* The gate is
  about `curl | sh` specifically, not about installing things.

> [!IMPORTANT]
> **The jail's hardcoded `mayAccessHost = true` must STAY. Deriving it would be a REGRESSION** — and
> an earlier draft of this section was wrong to call it merely untidy "defence in depth".
>
> From inside a jail, an **embedded** pack, a **local** pack and an **approved fetched** pack are
> three identical directories under `YOLO_PACK_ROOT`. Origin is a fact about the *user config*, which
> the jail deliberately cannot read — the same credential boundary that makes `packs` user-scope-only
> in the first place. So passing `false` for anything outside `_official/` would refuse the host
> files, mounts and installers of packs the user **did** approve, while protecting nothing: a pack
> with an unapproved claim never reaches the jail at all now.
>
> It is a named constant, `jailPackHostAccess`
> ([`packsurfaces.go`](../../internal/entrypoint/packsurfaces.go#L41-L64)), carrying that argument —
> which is the defence-in-depth actually available here. The next reader is protected by the name and
> the comment, not by a derivation that cannot be made correct.

> [!IMPORTANT]
> **Two things that look like partial packs and must NOT become fatal.** Both already exist, both are
> deliberate, and collapsing them into this ruling would break a jail's ability to boot at all.
>
> - **A declared bind mount whose host path is absent** is skipped with a warning
>   ([`runtime.go`](../../internal/loopholes/runtime.go#L214)). That is *adaptation inside a
>   capability the user already consented to* — nothing was refused, the thing simply is not there.
> - **A contribution whose KIND this build does not recognise** is skipped, not fatal, because the
>   host CLI and the baked entrypoint legitimately differ in age
>   ([`packdecl.go`](../../internal/packdecl/packdecl.go#L219-L246): a newer build's kind staged for
>   an older baked entrypoint *"is skew, not corruption"*, and the boot path treats any problem as
>   fatal). That is **skew tolerance**, not a refusal.
>
> The distinction that keeps these separate: **this ruling is about a claim yolo UNDERSTOOD and
> declined.** Something absent, or something from the future, is neither.

> [!WARNING]
> **The refusal message is now the entire user experience of the failure.** A user with a selected
> but unapproved fetched pack used to get a warning and a working jail; now they get no jail. That is
> the point — but it means the message must name all three choices, the pack, and the specific claim
> that was not approved. A fatal the reader cannot act on would be worse than the warning it
> replaces. Whether it succeeds at that from every place a user reads it is
> [OQ-TP7](#-oq-tp7--yolo-check-cannot-predict-the-fatal-refusal-and-the-refusal-names-a-fix-that-needs-a-tty-and-a-network--retired-2026-09-04).

#### Three things the build found that this section did not say

1. **`briefing after: host:<path>` had no reporter at all.** It is an approvable claim like the other
   four, and the launch withheld it in a single `&& p.MayAccessHost` inside `run/prepare.go` —
   silently. A pack whose only host claim was *"prepend the user's own AGENTS.md before my prose"*
   produced a jail with the pack's prose and none of the user's, and nothing anywhere said so. It now
   has `packload.RefusedBriefingOverlays`, which is a **REPORTER rather than a gate**: the gate stays
   in `prepare.go`, the only place that knows the host home.
2. **The launch never consulted `HonoredPlugins`** — its one production caller was `yolo host apply`'s
   skills compose. So [row 21](#2-the-inventory)'s hook bodies travelled into a jail inside the
   pack's skills tree with the refusal computed nowhere on that path. It is in the fatal now, which
   is the first time that row is enforced at launch at all.
3. **No escape hatch, deliberately.** Every other fatal in this system has one
   (`YOLO_ALLOW_UNREACHABLE_SERVICES`, `YOLO_ALLOW_STALE_IMAGE`) because the user may be unable to
   repair the cause from where they are standing. The argument that it does not hold here — *the
   approve path is one command away* — is exactly what OQ-TP7 disputes for CI and offline runs. A
   fourth choice would be the partial pack this ruling retires.

#### "So should we just remove the gate?"

Asked on review, and it is the right question to ask of any guarantee that turns out not to hold —
an unenforced gate is worse than no gate, because the warning tells the user something false.

**The answer is no, and one fact decides it: `installer <URL>` is already an approvable claim**
([`contributes.go`](../../internal/packdecl/contributes.go#L664)). It is enumerated at
`yolo pack install`, shown in the approval prompt, and recorded in the lockfile's
`ApprovedHostAccess` alongside every other claim. So this is not a bespoke prohibition sitting off to
one side — it is one row in a general approval model that already works. **Removing it** would not
delete a rule, it would delete **one entry from the approval prompt**, leaving mounts, `reads-host`,
briefing injection, plugin hooks and loophole daemons all approvable and `curl | sh` alone ungated —
an inconsistency, not a simplification, and it is the one claim whose payload is *arbitrary code from
a URL that can serve different bytes tomorrow*.

**Where removal WOULD be the right answer:** if we decided that a jail is a strong enough boundary
that running unreviewed code inside it needs no approval at all. That is a coherent position — close
to the argument that already leaves npm installs ungated — but it is a decision about the **whole**
approval model, not about this one claim, and it would retire the prompt rather than one line of it.
Nobody has proposed that.

### 3.2 `jail_daemon` is a claim-free crossing to supervised in-jail execution

A fetched pack declaring a loophole with only a `jail_daemon` produces **zero claims**, so
`HostAccessClaims` is empty, so the grant-on-empty branch fires and the daemon is emitted. Verified
end to end. No prompt, at any point, for a supervised restart-policied process running as UID 0.

### 3.3 The config gate fails open, and the workspace file is agent-writable

`CheckConfigChanges` auto-accepts with **no snapshot** (i.e. a fresh clone), auto-accepts on any
**non-TTY**, and is skipped on attach. Meanwhile `mounts` and `env_sources` — both host-read — have
**no scope rule**, so they are declarable in the workspace file that an agent inside the jail can
write. The only four user-scope-only things are `packs`, source-bearing `host_files`,
`cache_relocations`, and the loophole install keys.

> [!WARNING]
> **And the scope model inverts one level down.** Measured: `/home/agent/.config/yolo-jail` and
> `/workspace/.yolo/home/config/yolo-jail` **share an inode**. So "user scope" — the property the
> pack system argues for at length — holds at the host notch and does not hold inside a jail, which
> is where a nested jail gets its user scope from.

---

## 4. What this says about the proposal

[`pack-execution-trust.md`](./pack-execution-trust.md) should be read with three corrections:

1. **Its §3 premise is false** — no commit pin is enforced anywhere
   ([§1](#the-lockfile-is-a-receipt-not-a-gate)).
2. **Its permit/refuse table's top row is expressible but not taken.** It used to be flatly
   inexpressible — `npm` could not carry a version through the launcher template — and that was fixed
   on 2026-08-17 ([`npmspec.go`](../../internal/entrypoint/npmspec.go)). The correction it becomes is
   narrower and still bites: nothing pins by default, so §1's ranking is untouched and the proposal
   gains an option it never had, not an argument.
3. **Its scope is too narrow to matter.** It gates execution kinds while `skills`, `briefing` and
   `env` — all of which reach the agent, and `env` of which reaches execution — stay ungated and
   undisclosed.

**What survives:** P1's *shape* is right — content-addressing is the only answer to "is this the same
code" — but it is worth building in the three places of §1 and nowhere else.

> [!NOTE]
> **"P1" in the paragraph above is `pack-execution-trust.md`'s P1** (*a fetched pack may cause
> execution only of content it pins*), **not this document's** [P1](#p1-trust-flows-downward-and-a-parent-controlling-its-child-is-not-a-finding)
> (*trust flows DOWNWARD*), which was added later in §1 and is unrelated. Neither was renumbered —
> both are cited as written, and a rename would break more than it clarifies. The two never appear in
> the same argument; this note exists so a reader who lands here from a grep does not merge them.

---

## Open Questions

> [!NOTE]
> **Two questions were RETIRED here on 2026-09-03 and live in the [Decision Ledger](#decision-ledger):
> OQ-TP3 and OQ-TP4, both npm-pinning questions.** They are not answered — they are moot.
> [`program-delivery.md`](./program-delivery.md) §3.5 rules that an **agent dependency** is
> evergreen, and every pack either question governed installs an agent CLI. There is no pin to
> place, so *"where does it live"* and *"who must carry one"* have no subject left.
>
> The two things worth keeping from them are in the ledger rows, not here: TP4's cost analysis of
> pinning in the manifest (it makes yolo's release cadence the ceiling on agent-CLI freshness — now
> an argument **for** evergreen), and TP3's inherited half, ruled as `program-delivery.md` OQ-PD6.
>
> **Nothing below is open.** OQ-TP9 (the approval prompt is theatre) and OQ-TP8 (pack Lua stays
> ungated) were both ruled 2026-09-04 and are kept in place with their reasoning pending the next
> compaction; OQ-TP7 is RETIRED because TP9 deleted its subject, and is kept for the third-gate trap
> its analysis records.

### ⛔ OQ-TP7 — `yolo check` cannot predict the fatal refusal, and the refusal names a fix that needs a tty and a network — RETIRED (2026-09-04)

> [!IMPORTANT]
> **RETIRED, not answered — [OQ-TP9](#-oq-tp9--is-the-fetched-pack-approval-prompt-a-gate-or-theatre--resolved-2026-09-04) deletes its subject.** Every one of the six refusal
> sources gates on `p.MayAccessHost` and nothing else (`HonoredHostFiles`, `HonoredMounts`,
> `HonoredInstalls`, `HonoredLoopholes`, `HonoredPlugins`, `RefusedBriefingOverlays`). With the
> approval gate gone, `packRefusals` is always empty, `refusedLaunchError` never fires, and there is
> no refusal for `yolo check` to fail to predict and no approve path to be unreachable from CI or
> offline. **Both gaps below are dissolved rather than closed.**
>
> What must not be re-derived if a future refusal source appears: the **third-gate trap**.
> `hostaccessgates_test.go:88-93` pins that exactly two gates exist, so a third copied into `check`
> would satisfy the scan *vacuously* — worse than the silence, because the next producer gets merged
> into two of three sites. Any future preflight that predicts a launch refusal must SHARE the gate,
> not copy it. The analysis below is kept for that reason and for its measured citations.

[OQ-TP6](#decision-ledger) made a refused pack contribution **fatal to the launch** (2026-08-18, built
`6385dfbb`). That ruling holds: every path to the fatal was walked, each is intended, and the two
look-alikes [§3.1](#31-a-refused-contribution-refuses-the-launch-) names are still non-fatal and
pinned by tests. What did not ship with it is the rest of the loop around it — two gaps, both
measured, both still live.

*("The preflight" here means the **`yolo check` command**, the thing AGENTS.md tells a human to run
before asking for a restart. Not §3.1's **mechanical pre-flights**, which are four checks inside
`stagePacks` on the launch path. Different code, different time.)*

#### Gap 1 — `check` reports PASS on a config that cannot start a jail

Measured: a fetched pack selected in `packs`, present in the store, never run through
`yolo pack install`, declaring a `program via installer`. `sectionPacks` prints
`[PASS] acme: 3 file(s) stage`, `failed=0` — and the very next launch refuses.

The cause is one argument, at two sites: both `packload.LoadDir` calls pass `e.MayGrantHostFiles()`
([`check/packs.go:141`](../../internal/cli/check/packs.go) and
[`:173`](../../internal/cli/check/packs.go)), which is `false` for every fetched pack whether
approved or not — so `check` cannot ask the question at all. The launch's fold, `run.packRefusals`,
has **no caller anywhere under `internal/cli/check/`**.

`check` already holds itself to the opposite standard for a config-surface collision, in its own
words at [`check/packs.go:189`](../../internal/cli/check/packs.go): *"the launch refuses it … so
reporting it as a warning would mean `yolo check` passing on a config that cannot start a jail"*
(pinned by [`packs_test.go:285`](../../internal/cli/check/packs_test.go)). That was defensible while
this refusal was a warning. OQ-TP6 made it fatal and left `check` behind.

#### Gap 2 — the refusal's third option is not always available

`refusedLaunchError` ([`packrefusal.go:104`](../../internal/cli/run/packrefusal.go)) offers FIX,
REMOVE, or *"APPROVE — run `yolo pack install`, which shows every claim the pack makes and records
your yes in the lockfile."* Approve is unavailable in two states, and the message names neither:

- **No terminal.** `resolveHostApproval` refuses before reading a byte when stdin is not a tty
  ([`pack.go:1253`](../../internal/cli/pack.go)) — right on its own terms, since `yes | yolo pack
  install` is not consent. CI and any scripted run therefore have FIX and REMOVE only.
- **Offline.** `yolo pack install` is the only place network access happens, and `store.Sync` failing
  `continue`s past the `lock.Set`, so no approval is recorded. A launch resolves offline and does not
  care — until the claim set changes under a fixed lockfile, which **a yolo upgrade that adds a claim
  producer** does to a pack the user never touched. That user is offline, refused, and cannot approve.

The two ends have drifted apart rather than together: `resolveHostApproval` now says *"approval
requires an interactive terminal, and stdin is not one"*, while the launch refusal — the one OQ-TP6
made the entire user experience of the failure — still says none of it.

#### Why this is a question and not a bug report

Closing gap 1 needs the LAUNCH's gate (`run.packMayAccessHost`: origin, else the lockfile approval
over `packload.Pack.HostAccessClaims`). But
[`hostaccessgates_test.go:88-93`](../../internal/packload/hostaccessgates_test.go) pins that there are
exactly **two** gates — naming `internal/cli/pack.go`'s `resolveHostApproval` and
`internal/cli/run/packs.go`'s `packMayAccessHost` — and that neither may hand-build the claim union.
**A third gate copied into `check` would satisfy that scan vacuously**, which is worse than the
silence: the next producer gets merged into two of three sites.

So the decision is **where the shared gate lives**:

| | Home | Cost |
| :--- | :--- | :--- |
| (i) | Exported from `run` | An inverted import edge — `check` depends on the run pipeline |
| (ii) | Beside `MayGrantHostFiles` in `internal/config` | `config` grows a dependency on `packsrc`, i.e. on approval STATE |
| (iii) | A fourth home | Names nothing yet; the honest placeholder |

Whichever wins, the `hostAccessGates` row and `run.packRefusals` move with it. Gap 2 needs no such
decision — it asks only whether the refusal should state its own preconditions, and whether an
approval should be recordable without a fetch.

**What it decides:** whether OQ-TP6's *"the reader can act on it"* is true from every place a user
actually reads it — a CI log, an offline laptop, and `yolo check`, the command the workflow tells
them to run before restarting.

> ⚠ **Citations re-pinned 2026-09-04 and five had drifted** since the 2026-09-02 pass:
> `check/packs.go` 130→141, 162→173, 157→168, 170→189; `packs_test.go` 250→285; `pack.go`
> 1240-1246→1253. Cite this cluster by SYMBOL when it moves again.

### ✅ OQ-TP9 — is the fetched-pack approval prompt a gate, or theatre? — RESOLVED (2026-09-04)

Opened and ruled 2026-09-04, from a review challenge: *"an agent can't add packs here unless they
already have host access. so what's the danger?"*

**The facts.** Selecting a pack means writing `packs` in `~/.config/yolo-jail/config.jsonc`, as the
host user. `packs` is **user-scope only, and inexpressible at workspace scope by construction** —
`internal/config/packs.go`'s header calls that *"the whole security model of the feature"*, because
*"a workspace config travels with the repo and is agent-editable."* The only other route is
`--user-layer`, which requires the ability to run `yolo`. **An agent cannot add a pack.**

**The principle that decides it already exists in this repo.**
[`gate-placement-principle.md`](gate-placement-principle.md) **Test 1 — the authority test**: *"If
performing the guarded act already required at least as much authority as the gate protects, the gate
is theatre — and worse than nothing, because it looks like protection while the real gap stays
open."* And `internal/config/userlayer.go` already applies exactly that test to the **sibling** route
into `packs`, ruling the other way from this prompt: *"A gate here would refuse an actor who has
already passed a stronger one — pure ceremony, and the kind that teaches people to click through
prompts."*

**The original rationale was already refuted in-house.** The gate began as a flat refusal of
`program via installer` to fetched packs, on the ground that it *"would let a git ref execute
arbitrary code in the jail."*
[`pack-execution-trust.md`](pack-execution-trust.md) §2 showed `npm install -g` runs `postinstall`
from the same fetched pack, ungated — *"two cases that should be treated alike, decided oppositely."*
The split survived 2026-08-18 on a **different** reason (when the bytes change, not whose they are),
which is a pinning argument, not an approval argument.

**Answer:**
> **Theatre. Delete the approval prompt; keep the user-scope restriction; enforce the pin instead.**
>
> The three parts are one decision:
>
> 1. **Delete the prompt and the launch gate.** `resolveHostApproval`'s y/N, the lockfile approval
>    record, and `packMayAccessHost` go. A fetched pack's `reads-host`, `mount`,
>    `program via installer`, host-prepending `briefing`, loophole and plugin-hook claims are honored
>    the same as an embedded pack's. The person who put a git URL in their own user config has
>    already granted strictly more than the prompt was withholding.
> 2. **Keep `packs` user-scope-only.** This one PASSES Test 1 and the principle doc says so:
>    *"the actor genuinely changes: a workspace config travels with a repo and is agent-editable, so
>    allowing the key would hand an agent something it could not otherwise get."* It is the load-
>    bearing half, and it is not what this ruling touches.
> 3. **Enforce the commit pin — [OQ-LP8 / G2b](loophole-packaging.md).** This is the condition, not a
>    nice-to-have. The one thing the prompt did that had content was re-firing when a **moved pin
>    gained a claim**; today the lockfile records a commit and *nothing consults it at launch*. So
>    yolo built a prompt where the obvious prior art built a pin, and the prompt is the part that
>    does not work. A consulted pin is a strictly better version of the same notification, and it
>    also covers content drift **within** an approved claim set, which the prompt never did.
>
> [!WARNING]
> **CORRECTED 2026-09-04, same day, after reading the resolution path.** This ruling first stated the
> pin as *"recorded and never consulted at launch"* and made enforcing it **the condition**. That
> overstates it, in a way that matters:
>
> - A launch resolves a fetched pack from the **local mirror at the config's ref**
>   (`packsrc.Store.resolveFromStore` → `resolveCommit(mirror, a.Ref)`), and **the mirror only moves
>   when `yolo pack install`/`update` runs** — the one network step in the product
>   (`store.Sync`, `internal/cli/pack.go:1112`). So content is frozen between installs, and the
>   lock's commit and the mirror's ref agree right after either command writes both.
> - The lockfile IS read at launch, and today its **only** launch-time job is the approval gate
>   (`packs.go:175` → `packMayAccessHost`). **Deleting the gate makes the lockfile write-only at
>   launch**, which is the accurate version of the concern.
>
> So the follow-on is **documentation, not enforcement**, and OQ-LP8 already ruled the substance —
> *"choosing to follow a branch IS the trust decision"* — with two requirements it never delivered:
> say that in one plain sentence, and **document tag pins as the shape for a pack carrying host
> execution.** With the prompt gone those are the only thing between a user and a mutable ref, so
> they are overdue rather than optional. **G2b** (`ApprovedAt` written and read by nothing) is moot
> once the approval is gone. Making resolution read the lock's commit instead of the mirror's ref is
> still worth doing — it is what a lockfile means everywhere else — but it is correctness-of-meaning,
> **not a security fix**, and it is not a condition on this ruling.

> **The prior art, since the challenge named it.** A `nvim` plugin manager clones a git repo and runs
> its Lua on the **real host**, unsandboxed, with no approval prompt — the act of putting the repo in
> your config is the consent. yolo's fetched pack runs in a container, and asks anyway. Where that
> prior art is *stricter* is exactly yolo's gap: its lockfile pins commits and is **consulted on
> every load**.
>
> **What is kept, and is not a gate.** The startup transparency banner that lists what each loaded
> pack reads this launch stays. Disclosure is not consent, costs nothing, and is what actually tells
> a user what crossed.
>
> **And it inherits a ruling.** [`pack-execution-trust.md`](pack-execution-trust.md) §6 — *"approval
> must be readable"*, RULED and never built — was aimed at the terse one-token-per-line claim strings
> the prompt printed. TP9 deletes that renderer, but the ruling is **retargeted, not retired**: the
> banner (`packload.FootprintOf`, a separate rendering) becomes the ONLY place a user sees what a
> pack reaches, so "understandable by a new user" now applies to it and matters more than it did.
>
> **What this retires:** [OQ-TP7](#-oq-tp7--yolo-check-cannot-predict-the-fatal-refusal-and-the-refusal-names-a-fix-that-needs-a-tty-and-a-network--retired-2026-09-04)
> entirely (no refusal to predict, no approve path to be unreachable). **What it leaves standing:**
> [OQ-TP6](#decision-ledger)'s rule — a refused contribution refuses the launch — which is about
> consent, not cadence, and stays correct with no subject. **What it makes overdue:** OQ-LP8's two
> undelivered documentation requirements — see the correction above; G2b itself is moot.

### ✅ OQ-TP8 — pack-shipped Lua runs ungated on both sides of the boundary — is that a ruling or an accident? — RESOLVED (2026-09-04)

Added 2026-09-02, executing 💬 18's **D9**, which found this census had no row for the one channel
where a pack ships *code yolo runs* rather than content an agent reads. Row 26 now records the
facts; this question asks for the ruling the row cannot supply.

**The facts (row 26's evidence, spelled out):** `packload.DeriveScript` reads
`<pack root>/derive.lua` for **every** pack with no origin check
(`internal/packload/deriveenv.go:35` — contrast `LoadDir`'s explicit `MayGrantHostFiles` gate
parameter). It executes **in-jail on every boot** with live tables
(`internal/entrypoint/packsurfaces.go:193` → `deriveComputedLayer`), and — less obviously —
**host-side during `yolo host apply`**, as `hostTableKeys`' sentinel-input probe
(`internal/entrypoint/hostrender.go:377`), i.e. a fetched pack's Lua runs as the host user on the
real host. The differential D9 named is real: the same fetched pack **may not name a host file to
read** without approval, and may ship Lua yolo executes with none.

**What bounds the stakes, and why this is narrower than "arbitrary code":** the VM is an
allowlist sandbox — `SkipOpenLibs`, only base/string/table/math opened, `os`/`io`/`require`/`load*`
deleted, fresh state per run, instruction-budget timeout (`internal/agentcfg/luahook/vm.go`). So a
derive cannot exec, read files, or reach the network. Its two real powers are (i) **unvalidated
output into config surfaces** — the same trust as row 17's content and the place 💬 18's headline
defect lived — and (ii) **reading whatever `ctx` carries**, which under OQ-PT9's ruling includes
resolved provider credentials, with the auditability trade that ruling recorded (a derive reading a
secret is silent; a written config artifact shows in `yolo config diff`).

**What it decides:** whether ungated stays the documented rule, or pack Lua joins the claim table.
Options: (a) **rule it ungated and record why** — rows 18/19 already grant fetched packs equivalent
in-jail channels knowingly, the sandbox bounds it, and the commit pin (OQ-LP8, once enforced)
closes over `derive.lua` like all content; (b) an approval claim for *fetched* packs' derives
("runs sandboxed config code at boot and at host apply"), which adds a prompt line but no new
mechanism; (c) gate only the **host-side** probe on origin, since that is the one place the Lua
runs outside the jail.

_Leaning (HALF NOT TAKEN — see the Answer):_ **(a) for the in-jail half; (c) for the host half**, on
the grounds that `3144fbed` (2026-09-02) put the env derive on the `yolo host -- <cmd>` launch path
with real, credential-bearing inputs (`internal/cli/host.go:458`), so a fetched pack's Lua computes
the environment a real host process runs with. (a) was ruled for both halves; the (c) half did not
survive the parity check below.

**Answer:**
> **(a), BOTH halves — ungated, and recorded as a ruling rather than left as an accident.**
> *"doesn't this get to install actual things that changes stuff on the host? doesn't this mean it
> has a million injection capabilities anyway? don't think we need to gate this."* — 2026-09-04.
>
> **The (c) leaning fails a parity check, and it is not close.** `yolo host -- <cmd>` folds each
> pack's **static `kind: "env"` keys** into the launched process's environment at
> [`internal/cli/host.go:458`](../../internal/cli/host.go) — step (1) of the fold, *before* the derive
> runs at all, with no origin check and no approval. So the derive computes the same field a manifest
> can already state literally, at the same notch, through the same command. Gating the computed path
> while the literal path is open is not containment; it is a gate that reads as answered while the
> channel it names stays open.
>
> **The same holds one level up.** At the host notch a pack renders `config`, `config-overlay`,
> `skills` and `briefing` into the invoking user's **real home** (`render.HostFields()`,
> `internal/render/fieldset.go`) — content an agent reads and acts on. Against that, a compute hook
> that cannot exec, cannot read a file, cannot reach the network and runs under an instruction budget
> is not the marginal capability worth a prompt.
>
> **What this does NOT say.** It is not *"packs are trusted."* It is that **this channel adds nothing
> to a pack's existing reach**, so an approval claim here would be disclosure theatre — the objection
> the in-jail half already carried, now shown to apply to the host half for the same reason. The
> honest disclosure remains the one rows 18/19 lean on: the **commit pin** (OQ-LP8, once enforced),
> which closes over `derive.lua` exactly as it closes over every other file a pack ships. Row 26
> keeps the facts; the ledger keeps the reason.
>
> **What would reopen it:** a derive gaining a capability the sandbox denies today — I/O, exec,
> network, or an unbudgeted loop — or `ctx` growing a field the static `env` channel cannot already
> carry. Both are edits to `internal/agentcfg/luahook/vm.go` or to the derive's input table, and
> either should re-ask this question rather than inherit its answer.

**Answer:**
> _(empty — fill in when decided)_
