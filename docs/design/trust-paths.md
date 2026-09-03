---
title: "Every path by which someone else's content runs in your jail"
date: 2026-08-17
status: in-review
tags: [trust, packs, security, inventory]
summary: "Twenty-six paths, enumerated from the code, each with when trust is extended and whether the content can change afterwards. The answer to 'where does pinning even help' is: three of them. (Row 26 — pack-shipped derive.lua — was added 2026-09-02; pinning does not change its outcome, so the three stand.)"
---

# Every path by which someone else's content runs in your jail

**Status:** INVENTORY, 2026-08-17. Four questions settled since, and **two of them SHIPPED and are
still in the tree** — OQ-TP5 (`b3a29ad8`) and OQ-TP6 (`6385dfbb`), both 2026-08-18, both re-verified
against the code **2026-08-23**, anchors repinned **2026-09-02** (the provider arc moved several
files under them; every behaviour is unchanged). **Four remain open — [OQ-TP3](#-oq-tp3--given-1-is-pinning-worth-building-at-all-and-where-first),
[OQ-TP4](#-oq-tp4--where-does-an-embedded-packs-npm-version-get-pinned),
[OQ-TP7](#-oq-tp7--the-refusal-is-fatal-the-preflight-and-the-approve-path-are-not-caught-up) and
[OQ-TP8](#-oq-tp8--pack-shipped-lua-runs-ungated-on-both-sides-of-the-boundary--is-that-a-ruling-or-an-accident)
(added 2026-09-02 with census row 26)** —
and the first two are one question wearing two hats: *where does an npm pin live, and who is required
to carry one?* This document is organised to answer them. Beyond the two rulings that shipped,
**everything here is inventory** — traced in the code, with the anchors inline.

**The short version.** Twenty-six paths deliver someone else's content into a jail.
**Pinning changes an outcome in three of them** ([§1](#1-the-verdict)); everywhere else it is
theatre, because **every gate in this system keys on a DECLARATION and none on CONTENT.** Of the
three, row 1 (`program via npm`) is ruled and half built: the silent-change half is gone, and what is
left is that nothing records *which* version an update landed on — because the lockfile exists per
**fetched** pack and every npm-declaring pack is **embedded**. That gap is the live question.

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
| **OQ-TP5** | **No evergreen npm.** `install` obeys the lockfile; `update` is the only act that resolves a new version; the hourly poll may only *report*. **Built 2026-08-18 (`b3a29ad8`)**, minus the pin it has nowhere to record (OQ-TP4) | 2026-08-18 | [§1 row 1](#where-a-pin-would-change-the-outcome) |
| **OQ-TP6** | **A refused contribution is a refused launch.** No partial packs — fix the pack, remove the pack, or approve it. **Built 2026-08-18 (`6385dfbb`)** | 2026-08-18 | [§3.1](#31-a-refused-contribution-refuses-the-launch-) |

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

**Two structural facts about the file itself, which is where OQ-TP4 comes from:**

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
   name, which the launcher resolves to `@latest`. **The zero-user-action half was removed on
   2026-08-18** (OQ-TP5, below); what survives of this row is that the version an explicit update
   lands on is **not recorded anywhere**, which is OQ-TP4.

   > **RULED and BUILT, 2026-08-18: no evergreen npm packages. Install and update are different
   > acts.** *"I don't want magical evergreen npm packages. If there's a committed lockfile, install
   > installs from that version, update is how you get new versions. Let's just downgrade to
   > informational messages if there are updates available, since we already have this mech."*
   >
   > | | Rule | State |
   > | :--- | :--- | :--- |
   > | **install** | installs the version the lockfile records, and **never asks the registry what is latest** | built — but there is no version to record yet (OQ-TP4), so it leaves what is there alone |
   > | **update** | the only act that resolves a new version, and it **writes the lockfile** | resolves; does not yet write (OQ-TP4) |
   > | **the poll** | keeps running, but may only **say** that a newer version exists | built |
   >
   > **What the system does now.** The launcher's `_poll_and_report`
   > ([`shims.go`](../../internal/entrypoint/shims.go)) keeps the same `npm view` and the same hourly
   > throttle (`UPDATE_INTERVAL=3600`) and prints `<installed> → <latest> is available. Run 'yolo pack
   > update'` instead of reinstalling. The ONE input that still resolves a version is
   > `YOLO_PACK_UPDATE=1`, which `yolo pack update` sets and nothing else does; in that mode the
   > launcher installs and **exits without exec'ing**, so refreshing a list of agent CLIs never starts
   > one. The **cold path is untouched by design** — a first install is not a poll, and without it a
   > fresh jail would have no agent CLI at all. `internal/cli/packupdate.go` is the whole of the
   > difference, and it walks the **staged packs** rather than the launcher dir, which also holds
   > native and package-manager launchers.
   >
   > The evergreen poll was ours and it was deliberate, and **the problem it solved did not go away**:
   > agent CLIs install lazily into a jail home that *persists across boots*, so with no refresh they
   > freeze at whatever was current the day that home was created. `yolo pack update` is that same
   > refresh, asked for rather than assumed.

   > [!WARNING]
   > **npm installs are deliberately NOT origin-gated, and this ruling does not change that.**
   > `HonoredInstalls` ([`packload.go`](../../internal/packload/packload.go#L493-L516)) gates a
   > `curl`-piped installer and lets an npm install through, on the reasoning that a registry package
   > is *"the same trust as any dependency the user already installs."* That reasoning should stay:
   > this row is about **when the bytes change**, not about **whose bytes they are.**

   **A version is expressible, and nothing takes it.** The launcher splits the declaration
   ([`npmspec.go`](../../internal/entrypoint/npmspec.go)) and honours a version, dist-tag or range,
   skipping the poll for anything it did not resolve to `latest` itself. (It used to append `@latest`
   unconditionally, so `foo@1.2.3` yielded `foo@1.2.3@latest`.) That was a bug fix and **not** a
   decision: every shipped pack still declares a bare name. The question is once again *"should a
   pack pin?"* rather than *"can it?"* — which is OQ-TP3.
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
| 2 | **embedded pack `program via installer`** (claude, agy) | in-jail exec as UID 0 | **never** — embedded origin grants unconditionally | **yes, twice over**: the URL's bytes, and the vendor's own hourly self-update, which no pin touches |
| 3 | **`program via npm`** — any pack, any origin | in-jail exec (postinstall + deps) | **never**, for any origin | **no longer silently — 2026-08-18.** The hourly poll now only reports; `yolo pack update` is the only act that resolves a version. What remains is that the version it resolves is unrecorded (OQ-TP4), so *which* bytes an update lands on is still unpinned — but no longer unasked-for (§1) |
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
not — which is the whole of why OQ-TP4 exists alongside this ruling.)

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
> [OQ-TP7](#-oq-tp7--the-refusal-is-fatal-the-preflight-and-the-approve-path-are-not-caught-up).

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

> [!IMPORTANT]
> **A sibling doc proposes dispositions for the two npm questions below, and it has not been applied
> here.** [`program-delivery.md`](./program-delivery.md) §8.1 (2026-08-18) argues that **OQ-TP4 is
> RETIRED as posed** — all three of its venues are inside the pack system, and the identical question
> is live for mise, the LSP recipes and claude plugins, none of which is a pack — superseding it with
> OQ-PD1 (*where does the receipt live?*) and OQ-PD5. It argues **OQ-TP3's still-open half is
> INHERITED** at wider scope as OQ-PD6, with the reframe *"once a receipt exists, a declaration need
> not carry a pin, because the receipt is the pin."*
>
> **Both questions below stay OPEN and stay spelled as they are.** The dispositions are *proposed*,
> not applied — `program-delivery.md` says so explicitly — and neither doc may retire the other's ID
> unilaterally. What survives from OQ-TP4 regardless of which way it goes: **option (a)'s cost**, that
> pinning in the manifest makes yolo's release cadence the ceiling on agent-CLI freshness. Do not
> re-derive it.

### 💬 OQ-TP3 — given §1, is pinning worth building at all, and where first?

The honest ranking from this inventory is: **npm first** (highest plausibility, affects embedded
packs, used to change with nobody present), then the OQ-LP8 file/hook bodies, then `?ref=` drift.

_Leaning:_ **Yes, but only #1.** The other two are real and rarer; do them when their consumers
exist.

**Answer (partial, 2026-08-18):**
> **Row 1's BEHAVIOUR is decided: no evergreen npm, closed by removing the mechanism rather than
> adding a gate** — `install` obeys the lockfile, `update` is the only act that resolves a new
> version, and the poll is downgraded to informational
> ([§1 row 1](#where-a-pin-would-change-the-outcome), built the same day).
>
> **What is still open is SCOPE**, in two halves:
> 1. Does yolo pin its **OWN** embedded packs? That is inseparable from
>    [OQ-TP4](#-oq-tp4--where-does-an-embedded-packs-npm-version-get-pinned) — the ruling cannot be
>    implemented for the majority case until TP4 has a home for the pin.
> 2. Is a **fetched** pack *required* to pin, or merely permitted to? A `package` string may carry a
>    version, dist-tag or range today; no shipped pack does.
>
> Rows 2 and 3 are untouched either way: both are enforcement gaps on pins that already exist, not
> missing pins.

### 💬 OQ-TP4 — where does an EMBEDDED pack's npm version get pinned?

Raised by the OQ-TP5 ruling and **sharpened by its build, which stopped exactly here.** The
behavioural halves shipped; what is missing is the **RECORD**. `update` resolves a version and
nothing writes it down, so `install` has no version to install and falls back to "leave what is there
alone". That is a coherent state — nothing changes without being asked — but it is not the ruling as
stated: rule 1 says *install installs the version the lockfile records*, and today there is no such
version.

The lockfile is `packsrc`'s and it exists **per fetched pack**
([§1](#the-lockfile-is-a-receipt-not-a-gate)), while the four packs that declare npm programs — **pi,
copilot, codex, opencode** — are **embedded**. So the majority case has no lockfile row to install
from.

**What it decides:** whether "no evergreen npm" is true of yolo's own shipped agent CLIs, or only of
third-party packs — which would be the reverse of where the risk concentrates, since the embedded
four are what nearly every user runs.

Three shapes, none free:

| | Option | Cost |
| :--- | :--- | :--- |
| **(a)** | The **manifest** carries the version: `program via npm: "@anthropic-ai/claude-code@1.2.3"`, already expressible since the `npmspec` fix | The pin ships with the binary, so updating an agent CLI means shipping a new yolo. That is a release cadence coupling yolo does not have today |
| **(b)** | The **lockfile grows an embedded section**, written on first resolve and updated by `pack update` | Keeps the release cadences separate and makes `update` mean one thing for every pack kind. Costs a lockfile that is no longer only about fetched content, and a first-run resolve that has no pin to obey |
| **(c)** | **User config** may pin, and absent a pin the current behaviour stands | Honest and does nothing by default — which is the status quo this ruling exists to end |

_Leaning:_ **(b).** It is the only one where `install` and `update` mean the same thing for every pack
kind, which is the property the ruling is really asking for; (a) makes yolo's release cadence the
upper bound on how fresh an agent CLI can be, and (c) leaves the default exactly where it is today.
The honest cost of (b) is the first run: with no lockfile row yet, *something* has to resolve a
version, and that act should be `install` recording what it got rather than the launcher resolving
`latest` behind everyone's back.

**New fact since the options were written (2026-09-02):** the **observation half of (b) now
exists.** Install receipts (`af46c9b4`, 2026-08-24) record the *resolved* npm version per install —
`_resolved_version()` reads the installed `package.json` and `_yolo_receipt` appends it to
`<workspace>/.yolo/receipts.jsonl` (`internal/entrypoint/shims.go:633-661`). But a receipt is not a
pin: the file is workspace-scoped, append-only, and **nothing reads it back** — the same
receipt-not-a-gate shape as `LockEntry.Commit` above. So (b)'s remaining work is narrower than it
was: not "start recording versions" but "promote the recorded version into a row `install` obeys" —
which is also exactly [`program-delivery.md`](./program-delivery.md) §10's *user-scope gap receipt*
step. The two should land as one design. For completeness: `mise.lock` covers no npm agent CLI
(checked — none of the four packages appear in any mise file), and the live `packs.lock.json` is
`{"schema":1,"packs":{}}` with embedded packs explicitly skipped before `lock.Set`
(`pack.go:1086`).

**A second, structural blocker nobody had recorded (2026-09-02).** **The lockfile has no delivery
channel into a jail.** Measured: `grep -c packs.lock /proc/self/mountinfo` → **0**, and the in-jail
`~/.config/yolo-jail/packs.lock.json` is a **2026-07-29 fossil** that no launch refreshes — only
`config.jsonc` and `inherited-launch.jsonc` arrive as `:ro` single-file binds. Every npm install
happens **inside** a jail, by a generated launcher, so *"install obeys the record"* requires either
a fourth single-file bind beside those two, or the pin **baked into the launcher template** at
generation time (which is what `receiptsFile` already does for the receipts path, for the reason
`shims.go` gives: a `${YOLO_WORKSPACE:-…}` in the template would have written every macos-user
jail's receipts to a container path that does not exist there, silently). **This is not a detail to
settle after the ruling — it may change which of (a)/(b)/(c) is cheapest**, so weigh it when
answering.

**Answer:**
> _(empty — fill in when decided)_

### 💬 OQ-TP7 — the refusal is fatal; the preflight and the approve path are not caught up

Raised by an adversarial verification of the OQ-TP6 build (2026-08-18). The ruling holds — every path
to the fatal was walked and each is intended, and the two look-alikes [§3.1](#31-a-refused-contribution-refuses-the-launch-)
names are still non-fatal and pinned by tests that go red when broken. What the build did not carry
with it is the rest of the loop around the fatal, in two measured places.

**1. `yolo check` does not predict the refusal.** Measured: a fetched pack selected in `packs`,
present in the store, never run through `yolo pack install`, declaring a `program via installer` —
`sectionPacks` reports `[PASS] acme: 3 file(s) stage`, `failed=0`, and the very next launch refuses.
That is the exact outcome [`check/packs.go`](../../internal/cli/check/packs.go) refuses to allow for a
config-surface collision, in its own words: *"the launch refuses it, so reporting it as a warning
would mean `yolo check` passing on a config that cannot start a jail."* It was defensible while the
refusal was a warning; OQ-TP6 made it a fatal and left the preflight behind. The cause is one line —
that loop loads the staged tree with `e.MayGrantHostFiles()`, which is `false` for every fetched pack
whether approved or not, so it cannot ask the question at all.

> **Still true, re-verified 2026-08-23.** Both load sites still pass `e.MayGrantHostFiles()`
> ([`check/packs.go:130`](../../internal/cli/check/packs.go) and
> [`:162`](../../internal/cli/check/packs.go)); the `[PASS]` line is
> [`:157`](../../internal/cli/check/packs.go) (`r.ok("%s: %d file(s) stage")`); and `packRefusals` —
> the launch's fold — has **no caller anywhere under `internal/cli/check/`**. The sentence quoted
> above is still the standard `check` holds itself to, at
> [`check/packs.go:170`](../../internal/cli/check/packs.go) and its test at
> [`packs_test.go:250`](../../internal/cli/check/packs_test.go). The two-gate scan is unchanged too:
> [`hostaccessgates_test.go:88-93`](../../internal/packload/hostaccessgates_test.go) still names
> exactly `internal/cli/pack.go`'s `resolveHostApproval` and `internal/cli/run/packs.go`'s
> `packMayAccessHost`, so a third gate copied into `check` would still satisfy it vacuously.

**It is deliberately not a four-line fix, which is why it is a question and not a bug report.**
Answering it needs the LAUNCH's gate (`run.packMayAccessHost`: origin, else the lockfile approval over
`packload.Pack.HostAccessClaims`), and
[`hostaccessgates_test.go`](../../internal/packload/hostaccessgates_test.go) pins that there are
**two** gates and that neither may hand-build the claim union. A third gate copied into `check` would
satisfy that scan *vacuously* — the scan names two files — which is worse than the silence, because
the next producer would be merged into two of three sites. So the question is **where the gate lives**
if a third caller needs it: exported from `run` (an inverted import edge, `check` → the run pipeline),
moved beside `MayGrantHostFiles` in `internal/config` (which then imports `packsrc`, i.e. config grows
a dependency on approval STATE), or a fourth home. Whichever wins, the `hostAccessGates` row moves
with it and the refusal fold (`run.packRefusals`) becomes shared the same way.

**2. The APPROVE path the refusal names is not always available.** The ruling's no-escape-hatch
argument is explicit that this fatal differs from `YOLO_ALLOW_UNREACHABLE_SERVICES` and
`YOLO_ALLOW_STALE_IMAGE` because *"the approve path is one command the user can run right now."* Two
states where it is not:

- **No terminal.** `resolveHostApproval` refuses before reading a byte when stdin is not a tty
  ([`pack.go`](../../internal/cli/pack.go#L1240)) — correct on its own terms (`yes | yolo pack install`
  is not consent), but it means CI and any scripted run have **FIX and REMOVE only**.
- **Offline.** `yolo pack install` is the only place network access happens; `store.Sync` failing
  `continue`s past the `lock.Set`, so no approval is recorded. A launch resolves offline and does not
  care — until the claim set changes under a fixed lockfile, which a **yolo upgrade that adds a claim
  producer** does for a pack the user never touched. That user is offline, refused, and cannot approve.

Neither is an argument for a "run it anyway" flag — that is the partial pack the ruling retires. The
question is whether the refusal message should SAY so (it currently names `yolo pack install` with no
hint that it wants a terminal and a network), and whether a recorded approval should be expressible
without a fetch.

> **Still true, re-verified 2026-08-23 and again 2026-09-02 (anchors repinned), and the two ends
> have drifted apart rather than together.**
> `refusedLaunchError` ([`packrefusal.go:104-119`](../../internal/cli/run/packrefusal.go)) still
> spells the third choice as *"APPROVE — run `yolo pack install`, which shows every claim the pack
> makes and records your yes in the lockfile"* — **no mention of a terminal, none of a network.**
> Meanwhile the *other* end did catch up: `resolveHostApproval`'s own non-tty refusal
> ([`pack.go:1240-1246`](../../internal/cli/pack.go)) now says *"approval requires an interactive
> terminal, and stdin is not one … rerun `yolo pack install` from a terminal"*. So a user who has
> already reached `pack install` is told; a user who only ever sees the launch refusal is not, and
> the launch refusal is the one this ruling made the entire user experience of the failure.

**What it decides:** whether OQ-TP6's "the reader can act on it" claim is true from every place a user
actually reads it — a CI log, an offline laptop, and the preflight command the workflow tells them to
run before restarting.

**Answer:**
> _(empty — fill in when decided)_

### 💬 OQ-TP8 — pack-shipped Lua runs ungated on both sides of the boundary — is that a ruling or an accident?

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

_Leaning:_ **(a) for the in-jail half; (c) needs an actual decision now, because its trigger
already fired.** The in-jail half is squarely inside what rows 18/19 already extend, and a claim
for a sandboxed compute hook would be disclosure theatre — the honest disclosure is the lockfile
pin. But the host side is no longer only a sentinel probe: **`3144fbed` (2026-09-02) put the env
derive on the `yolo host -- <cmd>` launch path with real, credential-bearing inputs**
(`host.go:458`). A fetched pack's Lua now computes the environment a process runs with *on the real
host* — still sandboxed, still unable to exec or do I/O itself, but its output IS that process's
env (`LD_PRELOAD` and friends), which is row 18's in-jail concern arrived at the host notch. That
is precisely the crossing this census exists to name before it becomes load-bearing by habit. The
cheap containment if (c) is taken: run only *embedded/local* packs' env derives at the host notch
until a fetched case exists and is ruled on.

**Answer:**
> _(empty — fill in when decided)_
