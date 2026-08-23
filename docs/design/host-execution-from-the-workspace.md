---
title: "Every path by which your jail's content runs on your host"
date: 2026-08-23
status: accepted
tags: [trust, security, workspace, mise, git, inventory]
summary: "The live /workspace bind is bidirectional. Nine paths let a jail session write a file the host later executes as the host user, and the two guardrails that look like they cover this — git's safe.directory and mise's trust prompt — are both structurally blind to it. Split by visibility x intent, only five are worth defending; the rest is code review doing its normal job — except under a host-side watcher, where the review window is zero and the answer is to move the watcher into the jail."
---

# Every path by which your jail's content runs on your host

**Status:** **PARTIALLY IMPLEMENTED, 2026-08-23** (commit `7fad359c`). Items 1 and 2 of
[§5.6](#56-what-i-would-actually-build--and-what-i-would-drop) shipped; everything else is
recorded in [Steps not taken](#steps-not-taken-2026-08-23) rather than left open, because it
was considered and declined rather than merely unfinished. Every claim below about runtime
behaviour was **measured in this jail on 2026-08-23** (git 2.55.0, mise 2026.7.17); claims
about the code carry file:line anchors checked the same day.

> [!NOTE]
> **What shipped is deliberately not a security feature.** Both items are defects that close a
> channel as a side effect — a config key that lied on one backend, and a derived directory two
> platforms could never safely share. That framing is the point: neither needed this threat
> model to be believed in order to be worth fixing, which is why they survived the "is this
> theatre?" test that killed the rest.

**The short version.** [`trust-paths.md`](./trust-paths.md) enumerates the paths by which
*someone else's content runs in your jail*. This document is its mirror, and the direction
nobody wrote down: **the paths by which the jail's content runs on your host.** There are
nine of them that are *files* — inventoried in [§2](#2-the-inventory) — plus one class that is
not a file at all: anything running in watch mode on the host.

The mechanism is always the same — `/workspace` is a live bind of a host directory,
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

**And the scope is deliberately tiny, because most of this surface is not a threat.** Every
tracked file the host eventually runs — the `Justfile`, the flake, *and every source file the
agent writes* — is gated by code review, which is not a security control being invented here
but software development working normally. [§1](#1-the-verdict) draws the line that separates
those from the five paths worth spending anything on, and states plainly what the fix does and
does not buy against prompt injection.

**Backend caveat up front:** everything in §5 that is a *mount* reaches podman only — Apple
Container ignores `:ro`, and `macos-user` has no mounts at all and silently ignores both keys.
The blind-cell control turns out to be natively expressible in that backend's Seatbelt profile
and the derived-directory one does not ([§5.5](#55-backend-portability--the-mounts-are-not-the-policy)).

**The one exception to "review covers it" is anything running in *watch* mode on the host** —
a dev server, a host-side LSP, a file-watching test runner. Those execute on save, so the
review window is not small but zero, and review stops being a control at all. That class does
not take a new mechanism: it takes moving the watcher into the jail, which the existing
`network.ports` and `per_side_paths` keys already support ([§5.4](#54-standing-execution-move-the-watcher-into-the-jail)).

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

> [!WARNING]
> **The first draft of this section split the paths by visibility alone, and that split was
> wrong.** It named `mise.toml`, `Justfile`, `flake.nix` and `package.json` as a "tracked
> config" tier gated by review — which implies the *rest* of the repo is not on that list. It
> is. The moment you run `just build` on the host, **every `.go` file the agent wrote is a
> host-execution path too.** "Tracked files whose content the host eventually executes" is not
> four files; it is the entire repository, and singling out four of them is exactly the
> theatre this document should be arguing against. The review challenge that produced this
> rewrite: *"the Justfile is basically just project code. I will eventually run the project on
> the host. So what is good enough here that isn't just theater?"*

**Visibility is only one of the two axes. The other is whether you intended to execute
anything at all.**

| | **You intended to execute** | **You did not** |
| :--- | :--- | :--- |
| **Visible in a diff** | **the product** — `Justfile`, `flake.nix`, `package.json`, **and every source file in the repo.** Not a threat surface | **the navigation cell** — `mise.toml`, `.envrc`. Fires on `cd`; review is *possible* but does not happen in time |
| **Invisible** | ≈ empty (an untracked script you deliberately run — you chose it) | **the blind cell** — `.git/config`, `.git/hooks/*`, `.git/info/exclude`, `.claude/**`, `.mcp.json`. Fires on `git status`, `git commit`, opening the folder |

Those three names — **the product**, **the navigation cell**, **the blind cell** — are used
throughout the rest of this document in place of the tier numbering the first draft used.

**The good-enough criterion, stated once:** *every execution the host performs should be one
you intended, on content you could have seen.* The two failure modes are the two axes, and
they take different fixes — **restore intent** where the trigger is navigation (mise
`paranoid` turns `cd` back into a decision point), and **restore visibility** where the
content is unreadable (deny the bottom-right cell, or redirect it somewhere tracked, [§5.1.1](#511-hooks-still-need-a-channel--redirect-rather-than-ban)).

**Top-left is not a security boundary and yolo should spend nothing on it.** The control there
is code review, it is the same control you already apply to `internal/foo.go`, and no
mechanism yolo adds improves it. The reviewer's instinct is correct: anything built for that
cell is theatre.

### The intent axis has two settings, and one of them collapses the product cell

> [!CAUTION]
> **"Review is the control" quietly assumed *batch* execution, and a host-side watcher makes
> it false.** Raised in review as *"also now wondering about live reloading dev servers on the
> host — this just seems unanswerable."* It is not unanswerable ([§5.4](#54-standing-execution-move-the-watcher-into-the-jail)),
> but it is a real hole in the model as drawn above, and the second time this doc's tiering has
> turned out too coarse.

Intent is not a boolean. It has two settings, and they behave completely differently:

| | **Per-act intent** | **Standing intent** |
| :--- | :--- | :--- |
| **Shape** | you re-decide every time — `just build`, `just test`, `npm install` | you decided once; every write after that executes with no new decision |
| **Examples** | the whole product cell as described above | a host `vite`/`webpack`/`air`/`cargo watch` dev server, a host-side LSP or formatter, an IDE background task, a file-watching test runner |
| **Review window** | non-zero — the diff exists before you choose to run it | **zero.** Execution fires on save, often before the agent has finished the task, certainly before you have read anything |

**Under standing intent the product cell's control is not weakened, it is absent.** The file
is *visible in principle* — it is right there in the working tree and `git diff` would show it
— but visibility only buys you anything if it precedes execution, and a watcher guarantees it
does not. That makes a host-side dev server strictly worse than the `Justfile` on the axis
that matters, while looking identical on the axis I originally drew.

> [!WARNING]
> **`node_modules/` is the worst-shaped instance of this and it is not in the inventory above
> because it is not a config file.** It is gitignored (invisible), auto-loaded by every
> host-side node tool that resolves a require, jail-writable, and far too large to review even
> if you wanted to. Same shape for `.venv/`, `target/`, `__pycache__/`. This class is
> **invisible *and* standing** — the blind cell's evasion with the watcher's zero review
> window — and it has a different fix from either ([§5.4](#54-standing-execution-move-the-watcher-into-the-jail)).

### Why the bottom-right cell is *not* theatre

Against the two threats worth naming:

1. **A misguided agent with a live collaborator.** Arrives through the product path — bad code
in a tracked file. Review catches it or doesn't, exactly as for any code. **yolo adds
nothing here, and shouldn't try.**
2. **Prompt injection escalating to the host.** This is the one the cell defends, and the
defence is specific: an injected agent *chooses its channel*, and its entire advantage is
choosing one you never look at. `.git/hooks/pre-commit` fires on your next commit with no
diff, no PR, no trace, in a file git will never show you.

> [!IMPORTANT]
> **The honest claim, with no inflation: this stops zero prompt injections.** It does not
> reduce what a compromised session can write — a payload in `internal/foo.go` is still a
> payload, and nothing short of not running the code prevents it. What it removes is the
> ability to act **unobserved**, forcing every payload onto the reviewed path.

**But "forcing it onto the reviewed path" is a weak argument and should not be the load-bearing
one**, because review of agent output is often nominal — a skim, on a branch you already
intended to merge. Pressed on exactly this (*"yes we can do something with git hooks, but such
a drop in the bucket"*), the argument that actually survives is not about review at all:

> [!IMPORTANT]
> **The blind cell is the only channel that survives every undo a developer has.** This is
> qualitative, not a matter of degree, and it is the real reason the cell earns a mechanism:
>
> | Undo | Removes a payload in `internal/foo.go`? | Removes `.git/hooks/pre-commit`? |
> | :--- | :--- | :--- |
> | `git checkout` / `git restore` | ✅ | ❌ — not tracked |
> | `git revert`, or dropping the branch | ✅ | ❌ — never was a commit |
> | `git clean -xdf` | ✅ | ❌ — does not touch `.git/` |
> | reverting the PR after you find it | ✅ | ❌ |
> | re-cloning | ✅ | ✅ — and nobody re-clones |
>
> A source payload executes when you run that code, and in this repo most of that runs **in
> the jail**. A `pre-commit` hook executes **on the host**, on your next commit in that repo,
> **forever** — and every instinct a developer reaches for to undo a bad agent session leaves
> it in place, because none of them can see it. That is the difference between a compromise you
> can revert and one you cannot see well enough to revert.

> [!WARNING]
> **`.git/info/exclude` is the load-bearing entry**, because it is the file that *decides* what
> is invisible. It is jail-writable and untracked (verified 2026-08-23). A session that appends
> a path to it moves that path from the top row to the bottom one — silently, and for every
> future session. **"Visible in a diff" is not a stable property unless the file defining
> visibility is itself locked.**

---

## 2. The inventory

Ordered by how little the host user has to do to trigger it. "Host act" is what *you* do;
in every row the code runs as **you**, unsandboxed, with your SSH keys, your `gh` token and
your cloud credentials — the exact things [the briefing promises are invisible to the jail](../../internal/jailcontent/briefing.go).

| # | Channel | Host act that fires it | Cell | Measured 2026-08-23 |
| :-- | :--- | :--- | :--- | :--- |
| 1 | **`mise.toml` → `[env]`, `[hooks]`, `[tasks]`** | **`cd` into the repo**, with `mise activate` in your shell | navigation | ✅ mutated a trusted config; new content honoured **silently** |
| 2 | **`.git/config` → `core.fsmonitor`** | **`git status`** — also `add`, `commit`, `checkout` | blind | ✅ fires on bare `git status` |
| 3 | `.git/config` → `alias.<x> = !sh` | `git <x>` | blind | ✅ |
| 4 | `.git/config` → `core.pager` | `git log`, `git diff` — anything paged at a TTY | blind | ✅ (via `--paginate`; off-TTY git skips the pager) |
| 5 | `.git/config` → `core.hooksPath` | any hooked command | blind | ✅ local value beats a global one |
| 6 | **`.git/hooks/pre-commit`** | **`git commit`** | blind | ✅ |
| 7 | `.git/config` → `core.sshCommand`, `gpg.program`+`log.showSignature`, `diff.*.textconv`, `filter.*.clean`, `credential.helper` | fetch/push, `log`, `diff`, `checkout` | blind | not individually re-measured; documented upstream (§7 sources) |
| 8 | **`.claude/settings.json` / `settings.local.json`** → `hooks`, `apiKeyHelper` | opening the repo in a host agent session | blind | ✅ writable; see the warning below |
| 9 | `Justfile` / `flake.nix` / `package.json` | `just done`, `just load`, `npm install` | product | ✅ writable |

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
entire basis of [§5.2](#52-the-navigation-cell-restoring-intent-host-side).

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

The genuinely new claims are three: the **direction** itself; the **two-axis split — visibility
× intent** ([§1](#1-the-verdict)), which is what keeps the fix to five paths instead of
declaring the whole repository a threat surface; and **`.git/info/exclude` as the dial that
moves a file from one cell to another.**

---

## 5. What we can actually do

Three options. I recommend **5.1 alone**, and I want to be direct that 5.2 and 5.3 are worth
less than they look.

### 5.1 The blind cell: deny — and the mechanism is already built

`workspace_readonly` overlays `-v <host-subpath>:/workspace/<rel>:ro` on top of the writable
workspace bind ([`mounts.go:20-52`](../../internal/cli/run/mounts.go)). It validates entries as
relative, `..`-free paths ([`validate.go:331-353`](../../internal/config/validate.go)), warns and
skips on non-existent or escaping paths, and — the detail that makes it self-consistent — **when
any entry is active it also locks `yolo-jail.jsonc` itself `:ro`**, so a jail session cannot
switch its own protection off.

It is not set in this repo ([`yolo-jail.jsonc`](../../yolo-jail.jsonc), checked 2026-08-23). The
proposed entry set is exactly the blind cell:

```jsonc
"workspace_readonly": [".git/config", ".git/hooks", ".git/info", ".claude", ".mcp.json"]
```

**Why this set and not more.** `.git/info` rather than `.git/info/exclude` because the
directory is the stable thing to bind. `.envrc` is absent because this repo has none; add it
where one exists. `.git` wholesale is **wrong** — the agent must write refs, objects, and the
index to commit at all.

#### 5.1.1 Hooks still need a channel — redirect rather than ban

> [!CAUTION]
> **`.git/hooks` is the one entry where "the agent has no legitimate reason to write it" is
> false, and the first draft asserted it anyway.** Installing a hook is ordinary work — this
> repo's own `.git/hooks/pre-commit` (`just check-ci`) had to be written by someone, and an
> agent scaffolding a new project should be able to add one. A flat deny with no alternative
> is a capability regression, not a security control.

The fix falls out of [§1](#1-the-verdict): **hooks do not need to be writable, they need to be
visible.** Point `core.hooksPath` at a tracked in-repo directory and the agent writes hooks
there like any other code — committed, diffed, reviewed, and reaching the host through the
same path as every other file it produces:

```console
$ git config core.hooksPath .githooks     # set once, host-side, before the lock
```

`.git/hooks` stays locked because it is the **invisible** copy, not because hooks are
dangerous. In the [§1](#1-the-verdict) table this is a promotion out of the bottom-right cell
into the top-left one — the whole design in miniature, and the reason the answer is "redirect"
rather than "ban". It is also standard practice independently of any of this (husky, the
`pre-commit` framework, and in-repo `core.hooksPath` generally).

**The wrinkle worth stating.** `core.hooksPath` is itself a `.git/config` key — row 5 of the
inventory, and one we are locking precisely so a session cannot repoint it. So the agent
cannot set this, by design; something host-side must, before the mount. That is the
[`security-shim.md`](./security-shim.md) shape exactly — the unsandboxed step configures, the
sandboxed side cannot revise — and whether yolo should do it idempotently at launch rather
than leaving it to the human is [OQ-HX4, now archived](#-have-yolo-set-corehookspath-itself-was-oq-hx4).

**Three caveats, stated rather than buried:**

1. **Apple Container silently ignores `:ro`** (apple/container#889). `workspace_readonly`
already prints a loud warning and cannot skip the paths, since they live inside the writable
`/workspace` ([`mounts.go:26-32`](../../internal/cli/run/mounts.go)). On that backend this
control does not exist. **On `macos-user` it does not exist either — but for a reason that
turns out to be fixable, and that is [§5.5](#55-backend-portability--the-mounts-are-not-the-policy).**
2. **`git config --local` and `git remote add` start failing in-jail.** Commit identity is
unaffected — the jail sets it globally in the jail home
([`identity.go:17-20`](../../internal/entrypoint/identity.go)) — but `git checkout -b --track`
and `git push -u` write `branch.*.remote`, so the *cost* of this ruling is real and lands on a
common workflow. That is [OQ-HX2, now archived](#-the-git-control-plane--workspace_readonly-over-gitconfig-githooks-gitinfo-was-oq-hx2).
3. **The bootstrap gap.** The self-lock protects a config that already has entries. A workspace
whose first-ever jail session had none is unprotected for that session. Setting the key at
**user scope** — outside the workspace, hence unwritable from any jail — would close it, and
whether that is honoured is [OQ-HX1, now archived](#-workspace_readonly-from-user-scope-config-was-oq-hx1).

### 5.2 The navigation cell: restoring intent, host-side

**The one that is worth doing:** `paranoid = true` in the host's **global** mise config. It
turns on content hashing, so a rewritten `mise.toml` re-prompts — closing row 1, the sharpest
navigation-cell channel, at its root. And it is durable: since **CVE-2026-35533** (fixed in mise 2026.6.4)
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

### 5.3 Tripwire: report changes to the danger set

Hash the danger set at launch, diff at exit, show the human. **Rejected as the primary
control**, for the reason `trust-paths.md` §1 gives about lockfiles: a mechanism that only
*reports* is a receipt, not a gate — and for the product cell it duplicates `git diff`, which already works
and which the human already reads. It has exactly one non-redundant use: **blind-cell paths on Apple
Container**, where `:ro` cannot be enforced and detection is all that is available. That is a
narrow enough case to defer until someone is on that backend.

### 5.4 Standing execution: move the watcher into the jail

**The answer is a workflow, not a mechanism — and both primitives it needs already ship.**
A watcher cannot be made safe on the host, because the thing that makes it dangerous (zero
latency from write to execution) is the thing it exists to do. So do not put it there.

| Sub-problem | Existing primitive | State |
| :--- | :--- | :--- |
| **The dev server itself** | `network.ports` (`"HOST:JAIL"`) publishes a jail port to the host, so the server runs in the sandbox and you browse it from the host as usual ([`config_ref.txt:602-604`](../../internal/cli/config_ref.txt)) | ships; already the documented intent in [`sandbox-comparison.md`](../research/sandbox-comparison.md) §"Example" — *"the developer sees ports on localhost; the agent sees container-internal hostnames"* |
| **`node_modules/`, `.venv/` and friends** | `per_side_paths` shadow-mounts a derived directory so **host and jail each get their own copy** and it *"never crosses the host↔jail boundary"* ([`mounts.go:54-69`](../../internal/cli/run/mounts.go)) | ships, and **`node_modules` joined the default set 2026-08-23** (OQ-HX5). Root-level only; a monorepo names `packages/*/node_modules` explicitly |

The connection worth making explicit: **`per_side_paths` was built for a correctness problem**
— interpreter symlinks and native builds that break when two platforms share a directory — and
it happens to be the exact right shape for this security one. Host tooling cannot load what
the jail wrote if the two sides never share the directory.

**What is genuinely irreducible, stated plainly.** Some work cannot move into the jail: a
native GUI app, an iOS or Android simulator, anything device- or GPU-bound on the host. For
those, the host *is* executing agent output continuously and no configuration changes that.
The honest posture is disclosure rather than mitigation — while such a watcher is running,
treat that jail session's output as already running on your host, because it is. That is a
narrower residue than "unanswerable", but it is not empty, and I would rather name it than
round it to zero.

> [!NOTE]
> Moving the server into the jail leaves your **browser** loading JS the jail served. That is
> the ordinary web threat model, not host code execution, and the browser sandbox is the
> control — worth stating only so the boundary is not overclaimed.

### 5.5 Backend portability — the mounts are not the policy

> [!CAUTION]
> **Everything proposed above is a bind mount, and `macos-user` has no mounts at all.** Raised
> in review as *"none of the mounts could work on a macos-user setup."* Verified 2026-08-23:
> `workspaceReadonlyMountArgs` and `venvShadowMountArgs` are called from exactly one place,
> [`assemble.go:396,399`](../../internal/cli/run/assemble.go) — the **container** run pipeline —
> and neither string appears anywhere in `internal/macosuser/`. So on that backend both keys
> are **silently accepted and silently do nothing.** They are not in the disabled-feature
> matrix in [`macos-user-nix-and-features.md`](./macos-user-nix-and-features.md) §Part 4
> either, which is its own small gap and is fixed in the same commit as this section.

**But the mount was only ever the delivery mechanism. The policy is "these paths are not
writable", and `macos-user` already expresses exactly that policy — natively, in the Seatbelt
profile it builds for every launch** ([`seatbelt.go:26-35`](../../internal/macosuser/seatbelt.go)):

```lisp
;; --- Writes: deny everywhere, then re-allow the agent's writable set ---
(deny file-write* (subpath "/"))
(allow file-write*
    (subpath "/Users/Shared/yolo/<ws>")
    ...)
```

**That profile is already a deny-list with re-allows, and SBPL is last-match-wins** — which is
not a guess: the shipped profile *depends* on it twice over. `(deny file-write* (subpath "/"))`
followed by an allow is the only reason the agent can write at all, and the `/Users` read-deny
followed by re-allowed literals ([`seatbelt.go:53-59`](../../internal/macosuser/seatbelt.go))
is the same trick again. A `workspace_readonly` entry is therefore one more line appended
*after* the workspace allow:

```lisp
(deny file-write* (subpath "/Users/Shared/yolo/<ws>/.git/hooks"))
```

**So the blind-cell control is not merely portable to `macos-user` — it is a better fit there
than the mount is anywhere else.** No `:ro` to be ignored (the Apple Container failure mode),
no mount at all, and it lands in the one file that is already the backend's whole write policy.
That was OQ-HX6, and it **shipped on 2026-08-23** — see the Decision Ledger.

**The honest asymmetry: `per_side_paths` does not port, and cannot.** It needs *two different
contents at one path* — the host's `node_modules` and the jail's, simultaneously. That is a
mount-namespace capability. Seatbelt is a permission filter: it can deny access to a path, but
it cannot make one path resolve to two different directories. There is no Seatbelt spelling of
`per_side_paths`, so **the invisible-and-standing class from [§5.4](#54-standing-execution-move-the-watcher-into-the-jail)
has no fix on `macos-user`.** I would rather record that than invent one.

| Control | `podman` | `container` (Apple) | `macos-user` |
| :--- | :--- | :--- | :--- |
| `workspace_readonly` (the blind cell) | ✅ enforced | ❌ `:ro` ignored, warns loudly | ✅ **wired 2026-08-23** as SBPL denies (OQ-HX6) |
| `per_side_paths` (`node_modules`, `.venv`) | ✅ enforced | ✅ (a mount, not a `:ro` mount) | ❌ **no equivalent exists** — needs namespaces; **warns since 2026-08-23** |
| `core.hooksPath` redirect ([§5.1.1](#511-hooks-still-need-a-channel--redirect-rather-than-ban)) | ✅ | ✅ | ✅ — a git config key, backend-independent |
| mise `paranoid` ([§5.2](#52-the-navigation-cell-restoring-intent-host-side)) | ✅ | ✅ | ✅ — host-side, backend-independent |
| Move the watcher into the jail ([§5.4](#54-standing-execution-move-the-watcher-into-the-jail)) | ✅ via `network.ports` | ✅ | ✅ **but for a different reason** — see below |

**Why the watcher recommendation survives on `macos-user` despite `ports` being unwired.**
That backend runs a native process on the host's real network, so there is no port to forward
and `ports` / `forward_host_ports` are documented as not wired
([`macos-user-nix-and-features.md`](./macos-user-nix-and-features.md) §3.3) — you reach the dev
server directly. What "in the jail" buys there is not network isolation but **the Seatbelt
profile and the `_yolojail` uid**: the watcher executes agent output as a confined,
low-privilege user instead of as you. That was the point of moving it in the first place, so
the recommendation holds — it just collects a different prize.

> [!WARNING]
> **Everything measured in this document was measured in a Linux container jail.** The
> `macos-user` claims in this section are **code-reading only** — I cannot run that backend
> from in here, and did not pretend to. In particular one question I could not settle is worth
> flagging: on `macos-user` the agent writes as **`_yolojail`, not as you**, so files it creates
> are owned by a different user — which is the precise condition git's `safe.directory` check
> looks for, and which the container case defeats ([§3](#3-why-the-two-guardrails-that-look-relevant-do-not-fire)).
> Whether that accidentally restores the guardrail depends on which paths git actually
> ownership-checks (the repo dir, the gitdir, individual config files) versus which ones
> `_yolojail` ends up owning under the shared-group ACL. **It could make `macos-user` the one
> backend where rows 2–7 fail closed by accident.** That would be good news and it is
> unverified; do not rely on it until someone measures it on a Mac.

---

### 5.6 What I would actually build — and what I would drop

Asked directly whether any of this is worth the code (*"so is there anything we can reasonably
do here?"*), the honest ranking is short, and **the two items at the top are not security
features at all — they are defects that happen to close a channel.** That is the strongest
form a recommendation like this can take, because neither needs the threat model to be
believed in order to be worth fixing.

| # | Do this | Why it survives the "is this theatre?" test | Cost |
| :-- | :--- | :--- | :--- |
| **1** ✅ | **Stop `workspace_readonly` lying on two backends** ([§5.5](#55-backend-portability--the-mounts-are-not-the-policy)) | A shipped, documented key that says *"protect host-executed code"* and silently does nothing on `macos-user`. **This is a bug whether or not anyone ever uses it for the blind cell** | tiny — wire the SBPL line, or refuse the key |
| **2** ✅ | **`node_modules` in the default per-side set** ([OQ-HX5](#decision-ledger)) | A `node_modules` shared between a macOS host and a Linux jail is **already broken** for any native build. Justified on correctness; the security benefit is a side effect | small, and no user changes behaviour |
| **3** ⬜ | **`workspace_readonly` over the `.git` control plane** | The persistence argument in [§1](#1-the-verdict) — the only channel no undo removes | one config line, and **measured below: ~zero workflow cost** |
| **4** ⬜ | mise `paranoid`, host-side ([§5.2](#52-the-navigation-cell-restoring-intent-host-side)) | Closes the `cd` channel at its root, durably (CVE-2026-35533's fix) | zero code; not yolo's to ship |
| — | **Everything else: drop it** | the tripwire ([§5.3](#53-tripwire-report-changes-to-the-danger-set)), the git-config wrapper ([§5.2](#52-the-navigation-cell-restoring-intent-host-side)), anything aimed at the product cell, and **any attempt to police watcher workflow** | — |

> [!CAUTION]
> **Locking `.git/hooks` alone is defeated by one line in `.git/config`.** `core.hooksPath`
> repoints hooks anywhere (measured 2026-08-23: a local value beat a global one), so a session
> that cannot write `.git/hooks/pre-commit` writes `.githooks-evil/pre-commit` and repoints
> instead. **It is the `.git` control plane as a unit, or item 3 is not worth doing at all.**
> There is no cheap half of this one.

**Which I had priced too high.** [OQ-HX2, now archived](#-the-git-control-plane--workspace_readonly-over-gitconfig-githooks-gitinfo-was-oq-hx2)
assumed locking `.git/config` costs a common workflow. Measured on git 2.55.0, 2026-08-23 —
which everyday operations actually write local config:

| Operation | Writes `.git/config`? |
| :--- | :--- |
| `git commit` | no |
| `git checkout -b feat` (from a **local** branch) | no |
| `git push origin HEAD` | no |
| `git fetch` / `git pull` | no |
| `git push -u origin feat` | **yes** — `branch.feat.{remote,merge}` |
| `git checkout -b t2 origin/main` (from a **remote-tracking** ref) | **yes** |

**Only two write, and both have a config-free equivalent** — `git push origin HEAD` instead of
`push -u`, and branching from a local ref. For *this* repo the cost rounds to zero, because the
agent guide already says to stay on the current branch and not to open PRs unless asked
([CLAUDE.md](../../CLAUDE.md)). For a repo whose agents do branch-and-push, the cost is one
convention, not a broken workflow.

**What this adds up to: one config key and two small fixes.** If the persistence argument does
not move you, items 1 and 2 still stand on their own, because they are defects. That is the
whole recommendation, and I would rather it be this small and defensible than larger and
partly ornamental.

---

## 6. What this does not license

- **Not a claim that the sandbox is broken.** No container boundary is crossed. Every path
  requires the *host* to run something, and the live workspace bind is the product working as
  designed.
- **Not an argument for a read-only workspace.** The agent's whole job is writing there.
- **Not a general "review everything the agent writes" policy.** the product cell is already covered by
  ordinary diff review; the proposal is deliberately six paths wide.
- **Not a fix for `macos-user`.** That backend has its own inversion already documented in
  [`macos-user-build-step-threat-model.md`](./macos-user-build-step-threat-model.md); §5.1's
  mechanism is a container mount and does not reach it.
- **Not a defence against a human who runs `just load` on unreviewed agent output.** That is
  the product cell, and the control is reading the diff. This repo's documented workflow actively asks for
  that command — [AGENTS.md](../../AGENTS.md) — so the review is not optional hygiene, it is
  the control.
- **Not a fix for host-side work that genuinely cannot move into the jail** — a native GUI
  app, a device-bound simulator. [§5.4](#54-standing-execution-move-the-watcher-into-the-jail)
  names that residue and offers disclosure, not mitigation. Nothing in this document makes a
  host-side watcher safe; it only makes the common case unnecessary.

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

## Decision Ledger

| ID | Ruling / Decision | Date | Settled in |
| :--- | :--- | :--- | :--- |
| **OQ-HX5** | **Yes — `node_modules` joins `.venv` in the default per-side set**, on the correctness argument (a `node_modules` shared between a macOS host and a Linux jail is already broken for any native build), with the host-execution benefit as a side effect. Root-level only. **Built 2026-08-23** (`7fad359c`) | 2026-08-23 | [§5.4](#54-standing-execution-move-the-watcher-into-the-jail), `internal/cli/run/mounts.go` |
| **OQ-HX6** | **Yes — wire it, and treat the silent no-op as the bug.** `workspace_readonly` entries render as `(deny file-write* (subpath …))` after the writable-set allow in the Seatbelt profile. The `per_side_paths` sub-question resolved to **warn, not refuse** — the key is inert there and cannot be made otherwise, but refusing it would break configs that carry it harmlessly for other backends. **Built 2026-08-23** (`7fad359c`) | 2026-08-23 | [§5.5](#55-backend-portability--the-mounts-are-not-the-policy), `internal/macosuser/seatbelt.go` |
| **OQ-HX2** | **Half-answered by measurement, then archived unruled.** The cost is far smaller than assumed — only `git push -u` and branching from a remote-tracking ref write local config, both with config-free equivalents — and the "lock hooks, leave config" option does not exist, because `core.hooksPath` defeats it. See [Steps not taken](#steps-not-taken-2026-08-23) | 2026-08-23 | [§5.6](#56-what-i-would-actually-build--and-what-i-would-drop) |
| **OQ-HX1**, **OQ-HX4**, **OQ-HX3** | **Not taken now.** Each is a live option with a recorded reopen trigger rather than a question awaiting a ruling | 2026-08-23 | [Steps not taken](#steps-not-taken-2026-08-23) |

---

## Steps not taken (2026-08-23)

**These are archived, not abandoned.** Each was considered against the *"is this theatre?"*
test in [§1](#1-the-verdict) and declined for a stated reason, with the condition that would
reopen it. Nothing here is blocked on a decision — picking any of them up is a choice, not an
unblocking.

**The through-line for why they were declined:** items 1 and 2 shipped because they are true
without the threat model. Everything below needs you to accept the threat model *first*, and
several also cost either a workflow change or a behaviour yolo cannot enforce. That is the
line, and it is worth restating because it is the one that will decide the next one of these
too.

### ⬜ The `.git` control plane — `workspace_readonly` over `.git/config`, `.git/hooks`, `.git/info` (was OQ-HX2)

**The strongest of the not-taken set, and the closest to worth doing.** Its argument is
[§1](#1-the-verdict)'s persistence table: `.git/hooks` is the only channel that survives every
undo a developer has — `checkout`, `revert`, dropping the branch, `clean -xdf`, reverting the
PR after finding it. A source payload dies when you revert; a `pre-commit` hook runs on the
host on your next commit, forever.

*Why not now:* it is a config-only change any user can make today
(`"workspace_readonly": [".git/config", ".git/hooks", ".git/info"]`), so shipping nothing does
not prevent it — and unlike items 1 and 2, it asks the user to accept the threat model before
the change makes sense. It also does not stand alone: it needs the `core.hooksPath` redirect
([§5.1.1](#511-hooks-still-need-a-channel--redirect-rather-than-ban)) shipped with it, or it is
a capability regression.

*Cost, measured rather than assumed:* `commit`, `checkout -b` from a local branch,
`push origin HEAD`, `fetch` and `pull` write nothing. Only `push -u` and branching from a
remote-tracking ref do, and both have config-free equivalents.

*What reopens it:* any real instance of a jail session touching `.git/` — or simply deciding
the persistence argument is enough. If it is picked up, **it must be the whole `.git` control
plane**: locking `.git/hooks` alone is defeated by one `core.hooksPath` line.

### ⬜ Have yolo set `core.hooksPath` itself (was OQ-HX4)

Hooks need somewhere tracked to live before `.git/hooks` can be locked, and the agent cannot
set the key by design. *Why not now:* it is only needed if the `.git` control plane above is
taken, and it is the piece I was least comfortable with — yolo writing into a repo it does not
own is the kind of quiet host-side mutation
[`config-safety.md`](./config-safety.md) exists to make loud. *What reopens it:* taking the
item above. The fallback if the objection stands is a runbook line plus a `yolo check` warning.

### ⬜ `workspace_readonly` from user-scope config (was OQ-HX1)

Would close the bootstrap gap — the workspace config self-locks only once it *has* entries —
and make the fix once rather than per-repo. *Why not now:* it only matters if the `.git`
control-plane item is taken, and I never verified whether the host merge path honours the key
at user scope; [`inherit.go:189-193`](../../internal/config/inherit.go) excludes it from the
*generated in-jail snapshot*, which reads like a statement about the snapshot rather than a ban
on user scope, but I did not confirm it and will not assert it. *What reopens it:* wanting the
protection in more than one repo.

### ⬜ Recommend host-side mise `paranoid` (was OQ-HX3)

Closes the `cd` channel at its root, durably — since CVE-2026-35533's fix a workspace config
cannot turn `paranoid` back off. *Why not now:* it is a change to the human's machine, not to
yolo, so there is nothing to ship; and documenting it means owning its friction (HTTPS
enforcement, full plugin URLs, the leaf-name hash collision in jdx/mise#4499) for every user.
*What reopens it:* the maintainer adopting it and finding the friction acceptable — at which
point it is a line of host-hardening prose, not a feature.

### ⬜ The tripwire, and the hardened `git` wrapper

Both were rejected on their merits in [§5.3](#53-tripwire-report-changes-to-the-danger-set) and
[§5.2](#52-the-navigation-cell-restoring-intent-host-side), and are recorded here so they are
not re-derived. The tripwire only *reports*, which is a receipt rather than a gate, and for the
product cell it duplicates `git diff`. The wrapper must enumerate a key set upstream is free to
extend, and cannot blanket-clear `alias.*` at all. *What reopens the tripwire, narrowly:*
someone actually running the Apple Container backend, where `:ro` is ignored and detection is
the only option left.

### ⬜ Anything aimed at the product cell, or at watcher workflow

Not deferred — **ruled out**. The product cell's control is code review, which is not a
security mechanism being invented here but software development working normally, and no
mechanism yolo adds improves it. Host-side watchers collapse that review window to zero, but
the response is to move the watcher into the jail, and yolo cannot enforce how someone runs
their own machine. Presenting either as a control would be the theatre this document argues
against. The one shippable piece of the watcher problem was item 2, and it shipped.
