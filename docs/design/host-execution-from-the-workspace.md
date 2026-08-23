---
title: "Every path by which your jail's content runs on your host"
date: 2026-08-23
status: in-review
tags: [trust, security, workspace, mise, git, inventory]
summary: "The live /workspace bind is bidirectional. Nine paths let a jail session write a file the host later executes as the host user, and the two guardrails that look like they cover this — git's safe.directory and mise's trust prompt — are both structurally blind to it. Measured, not asserted."
---

# Every path by which your jail's content runs on your host

**Status:** INVENTORY + PROPOSAL, 2026-08-23. Nothing built. Every claim below about
runtime behaviour was **measured in this jail on 2026-08-23** (git 2.55.0, mise 2026.7.17);
claims about the code carry file:line anchors checked the same day.

**The short version.** [`trust-paths.md`](./trust-paths.md) enumerates the paths by which
*someone else's content runs in your jail*. This document is its mirror, and the direction
nobody wrote down: **the paths by which the jail's content runs on your host.** There are
nine. The mechanism is always the same — `/workspace` is a live bind of a host directory,
the agent writes a file there, and some *host* program later reads that file and executes
what it says, as you. **mise and git are the two sharpest**, because both are wired to run
on acts that feel like navigation rather than execution (`cd`, `git status`), and because
**both of the guardrails that appear to cover this case do not fire**: git's `safe.directory`
keys on *ownership*, and the jail writes as you; mise's trust keys on *path*, and the path
was trusted months ago.

**The verdict is in [§5](#5-what-we-can-actually-do):** the mechanism to fix the sharp half
already exists and is switched off. `workspace_readonly` ([`mounts.go:20-52`](../../internal/cli/run/mounts.go))
was built for precisely this — its own reference text says *"protect host-executed code that
lives in the workspace repo"* ([`config_ref.txt:578-583`](../../internal/cli/config_ref.txt)) —
and this repo does not set it.

> [!IMPORTANT]
> **This is not a containment bug, and I want to be exact about that.** A live bind-mounted
> workspace is the product. Nothing here escapes the container; the *host* walks over and
> executes the payload voluntarily. That is why the answer is not "harden the sandbox" but
> "shrink the set of files the host executes without looking" — and why [§6](#6-what-this-does-not-license)
> exists.

**Reads with:** [`trust-paths.md`](./trust-paths.md) (the inbound direction; this doc is
its mirror and borrows its "declaration vs. content" finding) ·
[`macos-user-build-step-threat-model.md`](./macos-user-build-step-threat-model.md) (the same
inversion for one backend — a host-side `nix build` on agent-influenced inputs) ·
[`config-safety.md`](./config-safety.md) (the config-diff prompt) ·
[`security-shim.md`](./security-shim.md) (the privilege-separation model this sits outside of).

---

## 1. The verdict

**Nine paths. Two tiers, split by one property: whether `git diff` shows you the change.**

| | Tier | Paths | Why the tier | Control |
| :-- | :--- | :--- | :--- | :--- |
| **T1** | **Invisible control plane** | `.git/config`, `.git/hooks/*`, `.git/info/exclude`, `.claude/**`, `.mcp.json`, `.envrc` | Git never shows them — untracked, ignored, or inside `.git/` — and **the agent has no legitimate reason to write any of them** | **Deny.** `workspace_readonly` |
| **T2** | **Tracked build/dev config** | `mise.toml`, `Justfile`, `flake.nix`, `package.json` | Git *does* show them, and the agent edits them as ordinary work | **Review.** Your existing diff review already is the gate |

**The whole finding compresses to this:** for T2, code review is a real control and it is
already running — a `Justfile` change lands in a commit you read. For T1, **there is no
control at all**, because there is nothing to read: the change never appears in `git status`,
never appears in a diff, and never appears in a PR. T1 is six files wide and closing it costs
one config key.

> [!WARNING]
> **`.git/info/exclude` is in T1 and it is the load-bearing one**, because it is the file that
> *decides* what is invisible. It is jail-writable and untracked (verified 2026-08-23). A
> session that appends a path to it moves that path from T2 to T1 — silently, and for every
> future session. **"Visible to `git diff`" is not a stable property unless the file that
> defines visibility is itself locked.** Lock `.git` and you get this for free; lock only
> `.git/config` and `.git/hooks` and you have left the blind-spot dial writable.

---

## 2. The inventory

Ordered by how little the host user has to do to trigger it. "Host act" is what *you* do;
in every row the code runs as **you**, unsandboxed, with your SSH keys, your `gh` token and
your cloud credentials — the exact things [the briefing promises are invisible to the jail](../../internal/jailcontent/briefing.go).

| # | Channel | Host act that fires it | Tier | Measured 2026-08-23 |
| :-- | :--- | :--- | :--- | :--- |
| 1 | **`mise.toml` → `[env]`, `[hooks]`, `[tasks]`** | **`cd` into the repo**, with `mise activate` in your shell | T2 | ✅ mutated a trusted config; new content honoured **silently** |
| 2 | **`.git/config` → `core.fsmonitor`** | **`git status`** — also `add`, `commit`, `checkout` | T1 | ✅ fires on bare `git status` |
| 3 | `.git/config` → `alias.<x> = !sh` | `git <x>` | T1 | ✅ |
| 4 | `.git/config` → `core.pager` | `git log`, `git diff` — anything paged at a TTY | T1 | ✅ (via `--paginate`; off-TTY git skips the pager) |
| 5 | `.git/config` → `core.hooksPath` | any hooked command | T1 | ✅ local value beats a global one |
| 6 | **`.git/hooks/pre-commit`** | **`git commit`** | T1 | ✅ |
| 7 | `.git/config` → `core.sshCommand`, `gpg.program`+`log.showSignature`, `diff.*.textconv`, `filter.*.clean`, `credential.helper` | fetch/push, `log`, `diff`, `checkout` | T1 | not individually re-measured; documented upstream (§7 sources) |
| 8 | **`.claude/settings.json` / `settings.local.json`** → `hooks`, `apiKeyHelper` | opening the repo in a host agent session | T1 | ✅ writable; see the warning below |
| 9 | `Justfile` / `flake.nix` / `package.json` | `just done`, `just load`, `npm install` | T2 | ✅ writable |

**Writability probe, all nine, 2026-08-23:** every path above is writable from this jail, and
new files can be created in `.git/hooks/`. The jail runs as UID 0 in a userns that maps to the
invoking host user, so the bytes land owned by you.

> [!CAUTION]
> **Row 8 is live in this repository right now, and it is the worst-shaped one.**
> `/workspace/.claude/settings.local.json` is a symlink into `swarf/`, and `swarf/` is excluded
> by `.git/info/exclude:25` — so it is jail-writable, host-read, and
> **invisible to `git status`**. Claude Code settings carry `hooks` and `apiKeyHelper`, and
> `apiKeyHelper` is documented as *"custom script, to be executed in `/bin/sh`"*; Sonar's
> disclosure ([§7](#7-prior-art-and-sources)) is exactly this file being executed pre-trust.
> Row 8 needs no git command, no `cd` with mise active, and no commit — **just opening the
> project on the host.**

### Row 1 in full, because it is the one that surprised me

mise's trust prompt looks like the control here, and it is not:

```console
$ mise trust ./mise.toml          # trusted
$ mise env -s bash | grep DEMO
export DEMO=v1
                                  # ← a jail session rewrites mise.toml
$ mise env -s bash | grep DEMO
export DEMO=v2-MUTATED            # no prompt, no warning, no re-trust
```

**Trust is keyed by path, not by content.** The record is a symlink named
`<leaf>-<hash-of-the-path>` pointing at the directory — there is no content hash in it at all
(`~/.local/state/mise/trusted-configs/`, inspected 2026-08-23). mise's own docs are candid:
*"In normal mode, a config file only needs trusting once, regardless of modifications."*
Content hashing exists **only under `paranoid`**, which is off by default — and that is the
entire basis of [§5.2](#52-t2-host-side-hardening--partial-and-honest-about-it).

So: you trusted `~/code/yolo-jail/mise.toml` some months ago. Every jail session since has had
write access to it, and `cd`-ing into the directory is enough to run whatever it now says.

---

## 3. Why the two guardrails that look relevant do not fire

This is the part worth internalising, because both are *good* mechanisms aimed at a
neighbouring threat.

| Guardrail | What it keys on | Why it misses | Verdict |
| :--- | :--- | :--- | :--- |
| **git `safe.directory`** (CVE-2022-24765) | **ownership** — refuses to parse `.git/config` or run hooks in a repo owned by *another* user | The jail writes through a userns that maps to **your** UID. Ownership never changes, so the check is satisfied on every one of rows 2–7 | **Structurally blind.** Not a partial mitigation — a zero one |
| **mise trust** | **path** | The path was trusted before the agent existed and stays trusted through arbitrary rewrites (§2) | **Blind by default**, closeable via `paranoid` (§5.2) |

**And upstream is not coming.** Git's security team's stated position on config-driven
execution is that it belongs to integrators, not to git: tools should not run git
opportunistically against untrusted repositories. That is a reasonable line — and it makes
**yolo the integrator**, since yolo is the thing that made a directory you trust into a
directory an agent writes.

> [!NOTE]
> **The same shape as `trust-paths.md`'s central finding, rotated 180°.** That doc found that
> every inbound gate keys on a **declaration** and none on **content**. Here the two outbound
> guardrails key on **ownership** and **path** — and again, neither on content. A jail session
> changes content and nothing else, which is precisely the axis nothing measures.

---

## 4. What is actually new here versus `trust-paths.md`

`trust-paths.md` row 14 already notes workspace `mise.toml` — as **in-jail** execution, trust
*asserted for you* on the podman argv via `MISE_TRUSTED_CONFIG_PATHS=/workspace`
([`boot.go:192-210`](../../internal/entrypoint/boot.go)). That is correct and it is a different
row from this one. **The inbound row asks "whose code runs in my jail"; this document's row 1
asks "whose code runs on my laptop."** Same file, opposite direction, and only the inbound half
had been written down.

The genuinely new claims are three: the **direction** itself; the **T1/T2 split by git
visibility** ([§1](#1-the-verdict)), which is what makes the fix small instead of hopeless; and
**`.git/info/exclude` as the dial that moves files between the tiers.**

---

## 5. What we can actually do

Three options. I recommend **5.1 alone**, and I want to be direct that 5.2 and 5.3 are worth
less than they look.

### 5.1 T1: deny — and the mechanism is already built

`workspace_readonly` overlays `-v <host-subpath>:/workspace/<rel>:ro` on top of the writable
workspace bind ([`mounts.go:20-52`](../../internal/cli/run/mounts.go)). It validates entries as
relative, `..`-free paths ([`validate.go:331-353`](../../internal/config/validate.go)), warns and
skips on non-existent or escaping paths, and — the detail that makes it self-consistent — **when
any entry is active it also locks `yolo-jail.jsonc` itself `:ro`**, so a jail session cannot
switch its own protection off.

It is not set in this repo ([`yolo-jail.jsonc`](../../yolo-jail.jsonc), checked 2026-08-23). The
proposed entry set is exactly T1:

```jsonc
"workspace_readonly": [".git/config", ".git/hooks", ".git/info", ".claude", ".mcp.json"]
```

**Why this set and not more.** Every entry is a file the agent has no legitimate reason to
write, so the false-positive cost is zero — which is the property that lets this be a *deny*
rather than a prompt. `.git/info` rather than `.git/info/exclude` because the directory is the
stable thing to bind. `.envrc` is absent because this repo has none; add it where one exists.
`.git` wholesale is **wrong** — the agent must write refs, objects, and the index to commit at
all.

**Three caveats, stated rather than buried:**

1. **Apple Container silently ignores `:ro`** (apple/container#889). `workspace_readonly`
   already prints a loud warning and cannot skip the paths, since they live inside the writable
   `/workspace` ([`mounts.go:26-32`](../../internal/cli/run/mounts.go)). On that backend this
   control does not exist. `macos-user` needs its own answer and does not have one here.
2. **`git config --local` and `git remote add` start failing in-jail.** Commit identity is
   unaffected — the jail sets it globally in the jail home
   ([`identity.go:17-20`](../../internal/entrypoint/identity.go)) — but `git checkout -b --track`
   and `git push -u` write `branch.*.remote`, so the *cost* of this ruling is real and lands on a
   common workflow. That is [OQ-HX2](#-oq-hx2--does-locking-gitconfig-break-enough-of-the-agents-git-workflow-to-matter).
3. **The bootstrap gap.** The self-lock protects a config that already has entries. A workspace
   whose first-ever jail session had none is unprotected for that session. Setting the key at
   **user scope** — outside the workspace, hence unwritable from any jail — would close it, and
   whether that is honoured is [OQ-HX1](#-oq-hx1--is-workspace_readonly-honoured-from-user-scope-config).

### 5.2 T2: host-side hardening — partial, and honest about it

**The one that is worth doing:** `paranoid = true` in the host's **global** mise config. It
turns on content hashing, so a rewritten `mise.toml` re-prompts — closing row 1, the sharpest
T2 channel, at its root. And it is durable: since **CVE-2026-35533** (fixed in mise 2026.6.4)
mise **ignores `paranoid`, `trusted_config_paths`, `yes` and `ci` when they come from a
non-global config**, so a jail-written workspace config cannot turn it back off. That fix is
what makes this a control rather than a suggestion.

*Caveats:* paranoid also forces HTTPS endpoints, full plugin URLs, and per-install provenance
re-verification. And its hash-file naming has a known collision on directories sharing a leaf
name (jdx/mise#4499), which presents as "I trusted this and it says untrusted."

**The one that is not worth doing:** a hardened `git` shell wrapper. `git -c core.fsmonitor=`
**does** override local config — measured, and `-c core.hooksPath=/nonexistent` defeated a
local `core.hooksPath` in the same run. But it is whack-a-mole across `fsmonitor`, `pager`,
`sshCommand`, `gpg.program`, `textconv`, `filter.*`, `credential.helper` and more, and
**`alias.*` cannot be blanket-cleared by `-c` at all.** A defence that must enumerate a set
upstream is free to extend is the halfway measure this repo keeps deleting. **Rejected**, and
recorded here so it is not re-derived.

### 5.3 Tripwire: report changes to the T1/T2 set

Hash the danger set at launch, diff at exit, show the human. **Rejected as the primary
control**, for the reason `trust-paths.md` §1 gives about lockfiles: a mechanism that only
*reports* is a receipt, not a gate — and for T2 it duplicates `git diff`, which already works
and which the human already reads. It has exactly one non-redundant use: **T1 paths on Apple
Container**, where `:ro` cannot be enforced and detection is all that is available. That is a
narrow enough case to defer until someone is on that backend.

---

## 6. What this does not license

- **Not a claim that the sandbox is broken.** No container boundary is crossed. Every path
  requires the *host* to run something, and the live workspace bind is the product working as
  designed.
- **Not an argument for a read-only workspace.** The agent's whole job is writing there.
- **Not a general "review everything the agent writes" policy.** T2 is already covered by
  ordinary diff review; the proposal is deliberately six paths wide.
- **Not a fix for `macos-user`.** That backend has its own inversion already documented in
  [`macos-user-build-step-threat-model.md`](./macos-user-build-step-threat-model.md); §5.1's
  mechanism is a container mount and does not reach it.
- **Not a defence against a human who runs `just load` on unreviewed agent output.** That is
  T2, and the control is reading the diff. This repo's documented workflow actively asks for
  that command — [AGENTS.md](../../AGENTS.md) — so the review is not optional hygiene, it is
  the control.

---

## 7. Prior art and sources

**Every comparable agent sandbox already does §5.1**, which is the strongest argument that the
entry set is right and not paranoid: `clampdown` makes `.git/config`, `.git/hooks`,
`.gitmodules`, `.claude`, `.codex`, `.devcontainer`, `.idea` and `.mcp.json` read-only by
default, and masks `.env`/`.envrc` outright — stated rationale, *"prevent a compromised agent
from altering its own runtime configuration."* Anthropic's own hardening after the Sonar
disclosure was the same move in a different place: gate the dangerous reads behind trust.

- [mise — Paranoid mode](https://mise.jdx.dev/paranoid.html) · [mise trust](https://mise.jdx.dev/cli/trust.html) — *"in normal mode, a config file only needs trusting once, regardless of modifications"*
- [GHSA-436v-8fw5-4mj8 / CVE-2026-35533](https://github.com/jdx/mise/security/advisories/GHSA-436v-8fw5-4mj8) — local configs can no longer set trust-control settings (fixed 2026.6.4)
- [jdx/mise#4499](https://github.com/jdx/mise/discussions/4499) — paranoid hash-file name collision on shared leaf directory names
- [justinsteven — buried bare repos and fsmonitor abuses (2022)](https://github.com/justinsteven/advisories/blob/main/2022_git_buried_bare_repos_and_fsmonitor_various_abuses.md) — the config-key inventory, and git's "this is an integrator problem" position
- [Cobalt — exploiting git FSMonitor for initial access](https://www.cobalt.io/blog/red-team-technique-exploiting-git-fsmonitor-for-initial-access)
- [Sonar — Claude Code executed code before you click 'trust'](https://www.sonarsource.com/blog/claude-arbitrary-code-execution/) — `core.fsmonitor`, then `gpg.program`+`log.showSignature`, then `.claude/settings.json` `apiKeyHelper`
- [GHSA-9ccr-r5hg-74gf / CVE-2026-45033](https://github.com/github/copilot-cli/security/advisories/GHSA-9ccr-r5hg-74gf) — the same fsmonitor channel in Copilot CLI
- [git CVE-2022-24765 / `safe.directory`](https://github.com/git/git/security/advisories/GHSA-vm9j-46j9-qvq4) — the ownership check, and why it is blind here
- [clampdown](https://github.com/89luca89/clampdown) · [sandcat](https://github.com/VirtusLab/sandcat) — protected-path precedent

---

## Open Questions

1. 💬 **OQ-HX1: Is `workspace_readonly` honoured from user-scope config?**
   [§5.1](#51-t1-deny--and-the-mechanism-is-already-built) recommends setting it in the
   workspace config, which self-locks — but only once it has entries. A user-scope setting
   (`~/.config/yolo-jail/config.jsonc`) lives outside every workspace, so no jail could ever
   write it, and generic entries like `.git/hooks` are meaningful in every repo. This decides
   whether the bootstrap gap is closeable and whether the fix is per-repo or once.
   [`inherit.go:189-193`](../../internal/config/inherit.go) excludes the key from the
   *generated in-jail snapshot* and calls it *"workspace-relative paths that ride the live
   /workspace bind from the workspace config"* — which reads like a statement about the jail
   snapshot, not a ban on user scope, but I did not verify the host merge path and will not
   assert it.

   _Leaning:_ It is honoured, and user scope is the better home for the git entries. Worth ten
   minutes with the host config assembly before we rely on it.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-HX2: Does locking `.git/config` break enough of the agent's git workflow to matter?**
   Commit identity is safe ([`identity.go:17-20`](../../internal/entrypoint/identity.go)), but
   `git checkout -b --track`, `git push -u` and `git remote add` all write `branch.*` /
   `remote.*` into local config. This is the whole cost of the T1 ruling, and it decides
   whether the entry set is five paths or four.

   _Leaning:_ Lock `.git/hooks` and `.git/info` unconditionally — zero legitimate writes, and
   between them they cover the fsmonitor-adjacent hook surface and the visibility dial. Treat
   `.git/config` as the one entry to trial and back out of if `push -u` becomes a daily
   irritation. Note this leaves rows 2–5 (`fsmonitor`, aliases, `pager`, `hooksPath`) open,
   which is a real reduction in coverage, not a rounding error.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 🤷 **OQ-HX3: Do we recommend host-side mise `paranoid` in the user guide, or just adopt it?**
   [§5.2](#52-t2-host-side-hardening--partial-and-honest-about-it) is a change to the *human's*
   machine, not to yolo. yolo can document it, or stay silent and let the maintainer set it.
   Documenting it means owning its friction (HTTPS enforcement, plugin URLs, the leaf-name hash
   collision) for every user.

   _Leaning:_ Subjective. My weak preference is a line in
   [`loopholes.md`](../guides/loopholes.md)-adjacent host-hardening prose rather than the user
   guide proper — it is advice about mise, not about yolo.

   **Answer:**
   > _(empty — fill in when decided)_
