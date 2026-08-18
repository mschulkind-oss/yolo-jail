---
title: "Every path by which someone else's content runs in your jail"
date: 2026-08-17
status: in-review
tags: [trust, packs, security, inventory]
summary: "Twenty-five paths, enumerated from the code, each with when trust is extended and whether the content can change afterwards. The answer to 'where does pinning even help' is: three of them."
---

# Every path by which someone else's content runs in your jail

**Status:** INVENTORY, 2026-08-17; **two rulings since**, both 2026-08-18 and both closing a finding
by *removing* a mechanism rather than adding a gate — no evergreen npm ([§1 row 1](#where-a-pin-would-change-the-outcome)),
and a refused contribution refuses the launch ([§3.1](#ruled-2026-08-18-a-refused-contribution-is-a-refused-launch)),
which obviates OQ-T1. Nothing built. Every row was traced in the code; anchors are
inline and the check date is today.

**Why this exists.** A proposal ([`pack-execution-trust.md`](./pack-execution-trust.md)) argued that
a fetched pack should only execute content it pins. The review response was *"they're all just as
weak. anything can be an installer. we're ultimately extending trust somewhere. I'm not entirely
sure where this pinning even helps."* That is right, and this document is the ground truth the
proposal should have been built on.

> [!IMPORTANT]
> **The proposal's central premise is false, and I verified it myself.** §3 of
> [`pack-execution-trust.md`](./pack-execution-trust.md) says the commit pin "is the rule already
> applied one level up". It is not applied anywhere. `LockEntry.Commit` has exactly **four readers,
> all display-only** — the moved-pin message and `pack status`
> ([`pack.go`](../../internal/cli/pack.go#L1095)). The launch path never consults it: it re-resolves
> the **config's ref** against the local mirror. The lockfile is a receipt, not a gate.

**What pinning actually buys, stated honestly:** it bounds trust in **time**, never in scope. It
cannot make code safe; a pinned malicious binary is malicious. Its only claim is *"the thing you
approved is the thing that runs, and you will be asked again when it changes"* — which defends
against exactly one threat, the silent update.

---

## 1. The verdict

**Pinning changes an outcome in three of twenty-five paths.** Everywhere else it is theatre, and the
reason is structural: **every gate in this system keys on a DECLARATION** — a URL, a path, a
component name, a config value — **and none on CONTENT.** That is a coherent design. It answers *"did
the footprint grow?"* well. It cannot answer *"is this the same code I looked at?"* at all.

**And the sharpest form of your instinct, which I had not seen:** `pack install` syncs the mirror and
writes the lockfile **in the same loop iteration**
([`pack.go`](../../internal/cli/pack.go#L1076-L1109)). The act that moves the content *is* the act
that moves the pin. A pin advanced by the same command that changes the bytes is a receipt. It
becomes a gate only if three things hold together — (i) enforced at use, (ii) advanced by a
*different* act than the one that changes content, (iii) that act shows you what changed. Today
**none** hold, and nobody is proposing to fix (ii).

### Where a pin would change the outcome

1. **`program via npm`** — because nothing *is* pinned and the content changes with **zero user
   action**. Every shipped pack declares a bare package name, which the launcher resolves to
   `@latest` and then keeps current on an hourly poll, so the binary changes between two invocations
   with nobody present.

   > **What the hourly poll is, since it is ours and it is deliberate — and it is being removed.**
   > A pack that declares `program via npm` does not get its binary at image-build time. The
   > entrypoint generates a *launcher* script for the command name, and the first invocation installs
   > it ([`shims.go`](../../internal/entrypoint/shims.go#L320-L390)). That script then keeps the
   > package fresh on its own: on the first call after a jail boot, and thereafter at most once an
   > hour (`UPDATE_INTERVAL=3600`), it runs `npm view <pkg> version` and **reinstalls** if the
   > registry's latest differs from what is installed.
   >
   > It was built for a real problem: agent CLIs install lazily into a jail home that *persists across
   > boots*, so with no refresh they freeze at whatever was current the day that home was created.

   > **RULED, 2026-08-18: no evergreen npm packages. Install and update become different acts.**
   > *"This is not something we can keep with pack-sourced things. I don't want magical evergreen npm
   > packages. We need to split to an install/update thing. If there's a committed lockfile, install
   > installs from that version, update is how you get new versions. Let's just downgrade to
   > informational messages if there are updates available, since we already have this mech."*
   >
   > This retires row 1 as a *silent-change* path — which makes it the first row in this document to
   > be closed by removing the mechanism rather than by adding a gate.
   >
   > **The three rules:**
   >
   > | | Rule |
   > | :--- | :--- |
   > | **install** | installs the version the lockfile records, and **never asks the registry what is latest** |
   > | **update** | the only act that resolves a new version, and it **writes the lockfile** |
   > | **the poll** | keeps running, but may only **say** that a newer version exists. It may never reinstall |
   >
   > The poll surviving as informational is the reason this is cheap: the mechanism already exists,
   > already runs on a sane schedule, and already knows the answer. What changes is that it reports
   > instead of acting.
   >
   > **What stands in the way today, stated so nobody is surprised mid-implementation:**
   >
   > - **`install` and `update` are literally the same code path.** `internal/cli/pack.go:175` reads
   >   `case "install", "update":`. The split is real work, not a flag — and the two must end up
   >   *behaving* differently, not merely printing differently.
   > - **The lockfile has nowhere to put an npm version.** `LockEntry`
   >   ([`lock.go`](../../internal/packsrc/lock.go#L32-L55)) records `Name`, `Source`, `Commit`, `Ref`
   >   and `ApprovedHostAccess` — everything about a *git* pin and nothing about a package one. It
   >   needs a new field, and `LockSchema` is versioned precisely so this kind of change is a bump
   >   rather than a silent misread.
   > - **Embedded packs have no lockfile entry at all.** The comment on `ApprovedHostAccess` says the
   >   lockfile is "unused for embedded/local packs". But this row's own text says the silent-change
   >   problem *"applies to embedded packs (pi, copilot, codex, opencode) as much as fetched ones"* —
   >   and those four are exactly the packs that declare npm programs. **So the ruling has no home for
   >   the pin it depends on for the majority case.** That is OQ-T4 below; it is not a detail, it is
   >   the question of whether this ruling can be implemented as stated.
   > - **npm installs are deliberately NOT origin-gated** — `HonoredInstalls`
   >   ([`packload.go`](../../internal/packload/packload.go#L258-L276)) gates a `curl`-piped installer
   >   and lets an npm install through, on the reasoning that a registry package is *"the same trust
   >   as any dependency the user already installs"*. That reasoning is untouched by this ruling and
   >   should stay: this is about **when the bytes change**, not about **whose bytes they are**.

   *Caveat, since RETIRED (2026-08-17):* a version used to be **not expressible** — the template
   appended `@latest` to the package string, so `foo@1.2.3` yielded `foo@1.2.3@latest`. The
   launcher now splits the declaration (`internal/entrypoint/npmspec.go`), honours a version,
   dist-tag or range, and skips the poll for anything it did not resolve to `latest` itself. That
   was a bug fix and **not** the decision: nothing is pinned by default and the shipped packs
   resolve exactly as before. What changed is that the row can now be *attempted* — the question
   is once again "should a pack pin?" rather than "can it?".
2. **A loophole's daemon FILE, and a plugin's HOOK BODIES** — the two gated crossings whose approval
   string genuinely does not cover the bytes. `["python3","{loophole_dir}/acme.py"]` is one claim
   string forever; `plugin <name> hooks (runs code at agent lifecycle events)` is a **constant** with
   no path and no digest in it. This is OQ-LP8/G2b, already open on purpose.

   > **"Plugin" and "hook body", defined — they are the AGENT's extension mechanism, not yolo's.**
   > A pack may ship a **Claude Code plugin**: a `.claude-plugin/plugin.json` manifest that the agent
   > reads directly. yolo delivers it and reports what it declares, but never interprets it.
   >
   > A plugin manifest can declare six component kinds, and yolo marks three of them as running code
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
   > reaches one of its own lifecycle events (before a tool call, after an edit, and so on). yolo
   > never runs it and never reads it.
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
   > construction.
   >
   > **Why editing it is a path in this list:** `pack install` syncs a local mirror of the whole
   > repository, not just the one ref. Every branch and tag is therefore already on disk. Changing
   > `main` to `some-other-branch` in config is a text edit that resolves against that mirror at the
   > next launch — no `pack install`, no network fetch, and nothing that prompts. The bytes that run
   > change because a config line changed.

   > **Who can make that edit, since the row read as though anyone could — and the answer is
   > stronger than "not without reapproval".** A pack address is **inexpressible from a workspace**,
   > by construction rather than by validation. `packs` is USER-SCOPE ONLY and is read from
   > `paths.UserConfigPath()` **directly, not from the merged config**
   > ([`packs.go`](../../internal/config/packs.go#L1-L20)) — so a workspace file cannot name a pack
   > even to be refused. The package comment states the reason in the same terms this document uses:
   > *"a workspace config travels with the repo and is agent-editable, so it must not be able to name
   > content that enters the jail."*
   >
   > And an agent cannot reach the file where it *is* expressible. The host's
   > `~/.config/yolo-jail/config.jsonc` is **never mounted into a jail**; the `config.jsonc` a jail
   > sees at that path is **generated per consumer** from the merged result
   > (`assemble_test.go` pins both facts: *"user config mount: none"*). Editing the in-jail copy
   > changes a generated artifact and nothing else.
   >
   > **So this row is a HUMAN path, not an agent-escalation path**, and the doc should not have left
   > that ambiguous. The gap is real but narrower than it read: a person who edits `?ref=` gets new
   > code **with no re-approval and no prompt**, because nothing on the launch path compares the ref
   > they are now running against the ref they approved. It is the same missing enforcement as
   > OQ-LP8/G2b — the lockfile records `Ref` *and* the `Commit` it resolved to, and every reader of
   > those fields is display-only.

### Where it is theatre — the four that matter

- **Pinning a pack tree at all**, in the dominant case: see the same-loop-iteration finding above.
- **Pinning execution kinds while `skills` and `briefing` are ungated.** A fetched pack can rewrite
  every `SKILL.md` and every line of briefing prose with no claim, no prompt, no lockfile entry and
  no launch disclosure — they are classified `disclosureSkip` as "jail-internal by construction". A
  skill that says *"run this command"* is an execution path with extra steps.
- **Pinning anything while `~/.config/yolo-jail/local` exists.** The implicit local pack needs no
  config line, has no lockfile entry, no commit, no claim, gets **full trust**, and is appended
  **last** so it outranks everything — selected by one `os.Stat` that follows symlinks.
- **Pinning a refusal that is not enforced where it executes.** See §3.

---

## 2. The inventory

Ordered from most-trusted origin to least. "Silent change" is the column the exercise exists for.

| # | Path | Grants | Trust extended | Can change silently? |
| :-- | :--- | :--- | :--- | :--- |
| 1 | the yolo binary — built-in skills + composed briefing | agent context | never | only via your own upgrade |
| 2 | **embedded pack `program via installer`** (claude, agy) | in-jail exec as UID 0 | **never** — embedded origin grants unconditionally | **yes, twice over**: the URL's bytes, and the vendor's own hourly self-update, which no pin touches |
| 3 | **`program via npm`** — any pack, any origin | in-jail exec (postinstall + deps) | **never**, for any origin | **yes — `@latest` on an hourly timer.** Since 2026-08-17 a declaration *may* name a version, which stops the poll; no shipped pack does, so the row is unchanged in practice (§1) |
| 4 | `flake.nix` / `flake.lock` | in-jail exec (everything on PATH) | implicit, at PR merge | no for inputs (locked revs, hermetic build) |
| 5 | **the implicit local pack** `~/.config/yolo-jail/local` | everything, at maximum trust | **never**, and deliberately | **yes, continuously** — live dir, re-read every launch, no record |
| 6 | explicit `file://` local pack | same as 5 | implicit in the config line | yes, every launch — no copy, no hash |
| 7 | `--user-layer` / `YOLO_USER_LAYER` | the full user scope | never, by explicit ruling | re-read per invocation; inert unless named |
| 8 | user-scope `loopholes.<name>.command` | **host execution** | never, by the same ruling | yes for the bytes — the config pins an argv, nothing reads the program |
| 9 | workspace `mounts` | host read | implicit at the config diff; **never on a fresh clone** | yes — `git pull`, the agent's own edit, or the host dir's contents |
| 10 | workspace `env_sources` | host read, exfiltration-shaped | implicit; never on a fresh clone | yes — re-read live each launch; a missing file warns and skips |
| 11 | workspace `mcp_servers` / `lsp_servers` / `packages` / `mise_tools` | in-jail exec | implicit at a diff that shows the NAME, never what it resolves to | mixed — the most useful contrast in the table |
| 12 | **the config gate itself** (`CheckConfigChanges`) | — it *is* the gate | — | **fails open three ways in 40 lines** (§3) |
| 13 | workspace `yolo-jail.config.lua` — **activated by existing** | agent context, transitively in-jail exec | **never**; not a config key, so outside the diff, drift and snapshot | yes, every boot, with nothing to diff against |
| 14 | workspace `mise.toml` | in-jail exec | **never** — trust asserted *for* you on the podman argv | yes — `git pull`, and `latest` resolves at install |
| 15 | `agents_md_extra`, blocked-tool messages, source-less `host_files` | agent context | implicit at the diff, which does carry the prose | covered by the diff; the finding is scope asymmetry |
| 16 | **`.yolo/handover.md`** | agent context, framed as an authoritative task list | **never** — no key, no prompt, no validation, no attribution | **yes, continuously** — an ordinary file any agent can write |
| 17 | fetched pack — **content** (skills, briefing, files, config-overlay) | agent context | **never** for a claim-free pack | yes, on every mechanism at once |
| 18 | fetched pack — `env` | in-jail exec in practice (no key allowlist, so `LD_PRELOAD` etc.) | **never**, explicitly | yes; no claim, so nothing to compare |
| 19 | **fetched pack — loophole with only a `jail_daemon`** | in-jail exec, supervised, restart-policied, UID 0 | **never** — excluded from the claim table by design | yes trivially; it was never approved |
| 20 | **fetched pack — `program via installer`** | in-jail exec as UID 0 | nominally a prompt; **in fact never** (§3) | yes — unpinned URL plus hourly self-update |
| 21 | fetched pack — wrapped agent plugin (hooks / MCP / LSP) | in-jail exec at lifecycle events | explicit prompt for the code-running components | **yes — the weakest claim string in the system**, a constant with no path or digest |
| 22 | fetched pack — `reads-host` / `mount` / host-prepending `briefing` | host read | **explicit, once** | yes — a moved ref with unchanged claim strings carries the approval forward |
| 23 | fetched pack — loophole with a `host_daemon` | **host execution** + a CA trusted in-jail | explicit, per crossing | yes — the claim pins the argv, not the file (OQ-LP8) |
| 24 | `yolo apply --host` | **host write** into your real home | explicit per invocation, `--assert` required | for a local pack, yes — source re-read each apply |
| 25 | the mirror + ref resolution behind rows 17–23 | selects which bytes every row above delivers | — | **three verified mechanisms** |

---

## 3. Three findings that outrank the entire pinning question

### 3.1 The origin gate is not enforced where it executes ⚠

**The only verified break of a guarantee the codebase actively claims.**

### What the gate is supposed to do

`HonoredInstalls` ([`packload.go`](../../internal/packload/packload.go#L266-L277)) walks a pack's
install contributions and refuses one specific thing:

```go
if in.InstallerURL != "" && !p.MayAccessHost {
    refused = append(refused, fmt.Sprintf(
        "pack %s: refused installer %q — a FETCHED pack cannot run a curl-piped "+
            "installer in the jail.", p.Name, in.InstallerURL))
    continue
}
granted = append(granted, in)
```

### What `mayAccessHost` is, and what it is protecting

**Origin decides exactly one thing** in this system — the package comment is explicit that a user
pack and an official pack are the same kind of thing, and that *"the only difference is ORIGIN, and
origin decides exactly one thing — whether a host-access declaration is honored."*

There are three origins, and they collapse into two answers:

| Origin | What it is | May reach the host |
| :--- | :--- | :--- |
| **embedded** | compiled into the yolo binary (`packs/*`) | ✅ always — it *is* yolo |
| **local** | `file:///path/to/pack` on your own disk | ✅ always — your own files, your own authority |
| **fetched** | `git+https://…?ref=…`, content someone else controls | ⚠️ **only what you approved** |

`MayAccessHost` is that verdict, carried on the loaded pack. For the first two it is `true` by
construction ([`packs.go`](../../internal/config/packs.go#L175-L177):
`MayGrantHostFiles() { return p.Origin() != OriginFetched }`). For a **fetched** pack it is decided
per launch by `packMayAccessHost`
([`run/packs.go`](../../internal/cli/run/packs.go#L772-L800)), and it is `true` only when the
lockfile records approval for **every** host-access claim the staged pack *currently* makes:

- a fresh install never run through `yolo pack install` → **fails closed**;
- a pin that moved and **gained** a claim → fails closed, and re-prompts;
- a missing or corrupt lockfile → **approves nothing**.

**So the gate is not "fetched packs may never".** It is *"a fetched pack reaches the host only for the
things you were shown and said yes to."* The claims are strings computed from the manifest
([`contributes.go`](../../internal/packdecl/contributes.go#L655-L670)) — `reads-host <path>`,
`mount <host> -> /ctx/<into>`, `briefing <src>`, and, importantly for §3.1, **`installer <URL>`**.

**What it gates, concretely:** host directory mounts (`HonoredMounts`), `curl`-piped installers
(`HonoredInstalls`), a wrapped plugin's code-running components, and a shipped loophole's daemon,
intercepts, binds and devices. All of them flow through one merged helper on purpose — both ends of
the approval must compute the same union, or the gate disagrees with the prompt.

### The refusal itself

Two properties of it are load-bearing and worth stating before the defect, because the fix must
preserve both:

- **It is PER CONTRIBUTION, not per pack.** A pack may mix an npm install with a `curl`-to-shell
  installer, and only the second is gated. The comment says why deciding once for the whole pack is
  worse in both directions: it would either refuse the innocent npm install, or — *"far worse"* — let
  a fetched pack **smuggle an installer URL through beside one**.
- **An npm install is deliberately ungated.** *"An npm install names a registry package and is not
  origin-gated — it is the same trust as any dependency the user already installs."* The gate is
  about `curl | sh` specifically, not about installing things.

### What actually happens

The decision is made **twice, on two sides of the boundary, from different inputs** — and only the
first side has the input that matters.

| | Host, at launch | Jail, at boot |
| :--- | :--- | :--- |
| What loads the pack | the run pipeline | [`packsurfaces.go:89`](../../internal/entrypoint/packsurfaces.go#L89) |
| `mayAccessHost` | derived from the pack's **origin** and the lockfile approval | **the literal `true`** |
| `HonoredInstalls` verdict | *refused* for a fetched, unapproved installer | *granted* — the refusal branch is unreachable |
| What it does about it | prints `Warning: refused installer …` | writes the launcher |

The host computes the refusal, prints it, and then **stages the unmodified `pack.json` anyway**.
Nothing carries the decision across the boundary — no marker file, no env var, no rewritten
manifest. The jail re-derives the same verdict from an input that is hardcoded to the permissive
answer, so `GenerateAgentLaunchers` writes the `curl → bash` launcher for a **fetched, unapproved**
pack. The warning the user saw was true about the *decision* and false about the *outcome*.

### Why the tests do not catch it

The test that "asserts" the split states the assumption in its own words — *"The JAIL loader trusts
the staged tree (the host already applied the gate)"* — and then **bypasses the jail loader**, so it
verifies the sentence rather than the system. That sentence is the whole bug: the staged tree is
exactly where the gate's decision is *not* recorded.

This is the identical shape `gateAdmitsCrossing` was written to close for loopholes: **true of the
decision, false of its enforcement.**

### What it is and is not

- **It is not remote code execution by a stranger.** A fetched pack must still be *selected* by name
  in the user's own user-scope config (§ row 3 above), so someone deliberately installed this pack.
- **It is the failure of the specific promise made to that person.** They were told, in a warning
  they can quote, that the installer was refused. It ran.
- **The blast radius is the jail, not the host.** The launcher executes inside the container — which
  is the point of the container, and is why this is a broken guarantee rather than a breach.

**Consequence for the pinning proposal: extending an unenforced refusal to more mechanisms adds
rules, not safety.** Any new gate proposed in this document inherits this same host-decides /
jail-executes split, and would need the decision *staged* to mean anything. That is why OQ-T1 asks
whether to fix this first rather than build on top of it.

### RULED (2026-08-18): a refused contribution is a REFUSED LAUNCH

*"If the installer is refused, that should be fatal. We can't run packs with selective things
disabled by refusals. Fix the pack, remove the pack, approve. Those are the choices."*

**This does not fix the enforcement gap — it deletes the problem.** Everything above describes a
decision made on the host and then *carried*, badly, into a jail that re-derives it from a hardcoded
input. Under this ruling there is nothing to carry: the host refuses, and **no jail starts**. The
`mayAccessHost=true` at `packsurfaces.go:89` stops being a security defect and becomes merely
untidy — any pack that reaches a jail now has every claim approved, so the permissive default is
accidentally correct for every input it can receive. (Worth fixing anyway, as defence in depth and so
the next reader is not misled, but it is no longer load-bearing.)

**It also retires the partial-pack concept**, which is the deeper change. A pack that half-loads is a
pack whose behaviour nobody can predict from reading it: the manifest says one thing, the running
system does another, and the difference is a warning scrolled past ten minutes ago. The three choices
the ruling names — **fix the pack, remove the pack, approve it** — are exhaustive precisely because
they are the only three that end with the manifest and the runtime agreeing.

> [!WARNING]
> **This is a behaviour change, and the direction is deliberate.** Today a user with a selected but
> unapproved fetched pack gets a warning and a working jail; afterwards they get no jail. That is the
> point — but it means the refusal message is now the entire user experience of the failure, so it
> must name all three choices, the pack, and the specific claim that was not approved. A fatal the
> reader cannot act on would be worse than the warning it replaces.

> [!IMPORTANT]
> **Two things that look like partial packs and must NOT become fatal.** Both already exist, both are
> deliberate, and collapsing them into this ruling would break a jail's ability to boot at all.
>
> - **A declared bind mount whose host path is absent** is skipped with a warning
>   ([`runtime.go`](../../internal/loopholes/runtime.go#L214)). That is *adaptation inside a
>   capability the user already consented to* — nothing was refused, the thing simply is not there.
> - **A contribution whose KIND this build does not recognise** is skipped, not fatal, because the
>   host CLI and the baked entrypoint legitimately differ in age
>   ([`packload.go`](../../internal/packload/packload.go#L283-L296): every read in the jail is a
>   cross-version read, and *"the wrong one is the boot path, where the cost is a jail that will not
>   start"*). That is **skew tolerance**, not a refusal.
>
> The distinction that keeps these separate: **this ruling is about a claim yolo UNDERSTOOD and
> declined.** Something absent, or something from the future, is neither.

### "So should we just remove the gate?"

Asked on review, and it is the right question to ask of any guarantee that turns out not to hold —
an unenforced gate is worse than no gate, because the warning tells the user something false.

**The answer is no, and one fact decides it: `installer <URL>` is already an approvable claim**
([`contributes.go`](../../internal/packdecl/contributes.go#L663-L664)). It is enumerated at
`yolo pack install`, shown in the approval prompt, and recorded in the lockfile's
`ApprovedHostAccess` alongside every other claim. So this is not a bespoke prohibition sitting off to
one side — it is one row in a general approval model that already works.

That reframes both options:

- **Removing it** would not delete a rule, it would delete **one entry from the approval prompt**,
  leaving mounts, `reads-host`, briefing injection, plugin hooks and loophole daemons all approvable
  and `curl | sh` alone ungated. That is an inconsistency, not a simplification — and it is the one
  claim in the list whose payload is *arbitrary code from a URL that can serve different bytes
  tomorrow*.
- **Enforcing it** does not need new machinery either. The host already computes the refusal; it just
  stages the pack unmodified afterwards. Having it stage a `pack.json` with the refused contribution
  **removed** makes the jail's hardcoded `mayAccessHost=true` harmless — there is nothing left to
  grant — and it matches the principle this subsystem already runs on: *"**The MOUNT is the filter**
  … a dropped pack has to be UNSTAGED or it keeps rendering"* (`AGENTS.md`). Enforcement by
  construction rather than by a boolean crossing a boundary.

**Where removal WOULD be the right answer:** if we decided that a jail is a strong enough boundary
that running unreviewed code inside it needs no approval at all. That is a coherent position — it is
close to the argument that already leaves npm installs ungated — but it is a decision about the
**whole** approval model, not about this one claim, and it would retire the prompt rather than one
line of it. Nobody has proposed that.

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

1. **Its §3 premise is false** — no commit pin is enforced anywhere (see the callout at the top).
2. ~~**Its permit/refuse table's top row is not expressible**~~ — **RETIRED 2026-08-17.** It was
   true: `npm` could not carry a version through the launcher template, which appended `@latest` to
   whatever the pack declared. The launcher now splits the declaration and honours a version,
   dist-tag or range ([`npmspec.go`](../../internal/entrypoint/npmspec.go)). Kept as a correction
   rather than deleted, because the correction it becomes is narrower and still bites: the row is
   *expressible*, not *taken* — nothing pins by default, so §1's ranking is untouched and the
   proposal gains an option it never had, not an argument.
3. **Its scope is too narrow to matter.** It gates execution kinds while `skills`, `briefing` and
   `env` — all of which reach the agent, and `env` of which reaches execution — stay ungated and
   undisclosed.

**What survives:** P1's *shape* is right — content-addressing is the only answer to "is this the same
code" — but it is worth building in the three places of §1 and nowhere else, and only after §3.1 is
closed. A refusal that does not hold where the code runs is not a foundation to build on.

---

## Open Questions

### ✅ OQ-T1 — is §3.1 a bug to fix now, or a design change? — RESOLVED (2026-08-18)

Carrying the origin decision into the jail could be a marker in the staged tree, an env var, or
staging a *modified* `pack.json` with the refused contribution removed. The third is the only one a
jail cannot ignore.

**A fourth option was raised on review — delete the gate and its warning.** It is answered in
[§3.1](#so-should-we-just-remove-the-gate) rather than here, because the answer is factual rather
than a matter of taste: `installer <URL>` is already an approvable claim flowing through the same
prompt and the same lockfile as every other host-access claim, so removing it would leave `curl | sh`
as the single unapproved crossing in a model that approves everything else. Removal is only coherent
as a decision about the whole approval model.

_Leaning:_ **Fix it now, by staging the refusal.** It is the one place where a documented guarantee
is false rather than weak, and it is load-bearing for every other rule that keys on origin. Staging
the modified manifest is also the cheapest of the three: no new file, no new variable, and it makes
the jail's permissive default harmless instead of leaving it as a trap for the next reader — the same
shape as *"the MOUNT is the filter"*, which this subsystem already relies on for pack selection.

**Answer:**
> **Neither — the question is obviated.** A refused contribution is a **refused launch**
> ([§3.1](#ruled-2026-08-18-a-refused-contribution-is-a-refused-launch)), so there is no decision to
> carry into a jail and no degraded pack to carry it for. All three mechanisms above were ways of
> delivering a *partially disabled* pack, and the ruling says that pack should not exist.
>
> **What survives of the finding:** the hardcoded `mayAccessHost=true` at `packsurfaces.go:89` should
> still be fixed, but as tidiness and defence in depth rather than as a broken guarantee — once the
> host refuses, every pack that reaches a jail has every claim approved, so the permissive default is
> correct for every input it can now receive.
>
> **What replaces it as the work:** making the refusal fatal, and writing a refusal message that
> names the pack, the specific unapproved claim, and the three choices (fix, remove, approve).

### 💬 OQ-T2 — does agent context (skills, briefing) get gated at all, or is "jail-internal" the ruling?

Today it is ungated and undisclosed by explicit classification, while `env` **is** disclosed on
reasoning that applies verbatim to skills. Either the reasoning is wrong or the classification is.

_Leaning:_ **Disclose, do not gate.** Prompt content is not execution and gating it would make packs
unusable; but a fetched pack silently rewriting every skill the agent reads should appear in the
launch banner exactly as `env` does.

**Answer:**
> _(empty — fill in when decided)_

### 💬 OQ-T3 — given §1, is pinning worth building at all, and where first?

The honest ranking from this inventory is: **npm `@latest` first** (highest plausibility, affects
embedded packs, changes with nobody present), then the OQ-LP8 file/hook bodies, then `?ref=` drift.
Everything else is theatre until §3.1 is closed — **and it now closes by ruling rather than by
machinery**: a refused contribution refuses the launch (2026-08-18), so the unenforced-gate finding
stops being a prerequisite for anything else in this list.

_Leaning:_ **Yes, but only #1, and only after the launcher can express a version.** The other two are
real and rarer; do them when their consumers exist.

> **The leaning's precondition is met as of 2026-08-17** and the question is therefore live rather
> than blocked. A `package` string may now carry a version, dist-tag or range, the launcher honours
> it, and a pinned launcher stops polling the registry
> ([`npmspec.go`](../../internal/entrypoint/npmspec.go)). That was a bug fix, **not** an answer to
> this question: nothing pins by default and every shipped pack still declares a bare name, so #1's
> risk is exactly what §1 describes. What is now decidable — and is what this OQ is asking — is
> whether yolo should pin its OWN packs, and whether a *fetched* pack should be required to.

**Answer (partial, 2026-08-18):**
> **Row 1 is decided: yes, and by removing the mechanism rather than adding a gate.** No evergreen
> npm — `install` installs the lockfile's version, `update` is the only act that resolves a new one,
> and the launcher's hourly poll is downgraded to an informational "an update is available". See the
> ruling in [§1 row 1](#where-a-pin-would-change-the-outcome).
>
> **What that leaves of this question, still open:** it settles the *behaviour* and not the two scope
> halves the paragraph above names — whether yolo pins its OWN embedded packs (which is
> [OQ-T4](#-oq-t4--where-does-an-embedded-packs-npm-version-get-pinned), and the ruling cannot be
> implemented for the majority case until it is answered), and whether a **fetched** pack is
> *required* to pin rather than merely permitted to. Rows 2 and 3 are also untouched: both are
> enforcement gaps on pins that already exist, not missing pins.

### 💬 OQ-T4 — where does an EMBEDDED pack's npm version get pinned?

Raised by the 2026-08-18 ruling in §1 row 1, and it decides whether that ruling is implementable as
stated rather than only for the minority of packs.

The ruling is *"if there's a committed lockfile, install installs from that version."* The lockfile
is `packsrc`'s, and it exists **per fetched pack** — `LockEntry`'s own comment says the approval
field is *"unused for embedded/local packs (their origin already permits host access)"*. But the four
packs that actually declare npm programs — **pi, copilot, codex, opencode** — are **embedded**, and
row 1 says the silent-change problem applies to them *as much as* to fetched ones.

So the majority case has no lockfile row to install from, and the ruling needs somewhere to put the
pin.

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

**Answer:**
> _(empty — fill in when decided)_
