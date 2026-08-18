---
title: "Every path by which someone else's content runs in your jail"
date: 2026-08-17
status: in-review
tags: [trust, packs, security, inventory]
summary: "Twenty-five paths, enumerated from the code, each with when trust is extended and whether the content can change afterwards. The answer to 'where does pinning even help' is: three of them."
---

# Every path by which someone else's content runs in your jail

**Status:** INVENTORY, 2026-08-17. Nothing built. Every row was traced in the code; anchors are
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
   `@latest` and re-checks with an hourly `npm view` poll
   ([`shims.go`](../../internal/entrypoint/shims.go)), so the binary changes between two
   invocations with nobody present. This is the highest-plausibility silent change in the inventory
   and it applies to **embedded** packs (pi, copilot, codex, opencode) as much as fetched ones.
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
3. **Editing `?ref=` in config without reinstalling.** The mirror already holds every branch and tag,
   so a config-only edit resolves offline at the next launch and delivers new content with **no
   install, no network and no prompt**. `pack status` calls this drift; nothing on the launch path
   consults it.

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
[`packsurfaces.go`](../../internal/entrypoint/packsurfaces.go#L89) loads **every** staged pack with
`mayAccessHost=true`. The host computes the refusal, prints `Warning: refused installer …`, and then
stages the unmodified `pack.json` anyway; nothing carries the decision across the boundary — no
marker file, no env var. So `GenerateAgentLaunchers` writes the `curl → bash` launcher for a
**fetched, unapproved** pack.

The test that "asserts" the split says so itself — *"The JAIL loader trusts the staged tree (the host
already applied the gate)"* — and then bypasses the jail loader. This is the identical shape
`gateAdmitsCrossing` was written to close for loopholes: *true of the decision, false of its
enforcement*.

**Consequence for the proposal: extending an unenforced refusal to more mechanisms adds rules, not
safety.**

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

### 💬 OQ-T1 — is §3.1 a bug to fix now, or a design change?

Carrying the origin decision into the jail could be a marker in the staged tree, an env var, or
staging a *modified* `pack.json` with the refused contribution removed. The third is the only one a
jail cannot ignore.

_Leaning:_ **Fix it now, by staging the refusal.** It is the one place where a documented guarantee
is false rather than weak, and it is load-bearing for every other rule that keys on origin.

**Answer:**
> _(empty — fill in when decided)_

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
Everything else is theatre until §3.1 is closed.

_Leaning:_ **Yes, but only #1, and only after the launcher can express a version.** The other two are
real and rarer; do them when their consumers exist.

> **The leaning's precondition is met as of 2026-08-17** and the question is therefore live rather
> than blocked. A `package` string may now carry a version, dist-tag or range, the launcher honours
> it, and a pinned launcher stops polling the registry
> ([`npmspec.go`](../../internal/entrypoint/npmspec.go)). That was a bug fix, **not** an answer to
> this question: nothing pins by default and every shipped pack still declares a bare name, so #1's
> risk is exactly what §1 describes. What is now decidable — and is what this OQ is asking — is
> whether yolo should pin its OWN packs, and whether a *fetched* pack should be required to.

**Answer:**
> _(empty — fill in when decided)_
