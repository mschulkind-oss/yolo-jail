---
status: current
verified: 2026-09-09
verified_commit: 38873c0d
covers:
  - internal/cli/run/mounts.go
  - internal/macosuser/seatbelt.go
  - internal/macosuser/orchestrator.go
  - internal/config/validate.go
tags: [trust, security, workspace, mise, git, inventory]
summary: "The live workspace bind is bidirectional: a jail session can write files a HOST program later executes as you. The inventory of those channels, the two guardrails that structurally miss them, and the mechanisms that defend the ones worth defending — workspace_readonly, per-side shadowing, and moving a watcher into the jail."
---

# Host execution from the workspace

**Status:** CURRENT as of 2026-09-09, verified against `38873c0d`.

`/workspace` is a live bind of a host directory, and it is the product. The consequence nobody
writes down is that it points **both ways**: a jail session writes a file there, and some *host*
program later reads that file and executes what it says, as you — with your SSH keys, your `gh`
token and your cloud credentials, the exact things the jail is built not to have.

This is not a containment bug. No container boundary is crossed; the host walks over and runs
the payload voluntarily. So the answer is never "harden the sandbox" but **shrink the set of
files the host executes without looking**.

| Component | Lives in |
| :--- | :--- |
| `workspace_readonly` — the `:ro` overlay and its self-lock | `internal/cli/run` (`workspaceReadonlyMountArgs`) |
| Per-side shadowing of derived directories | `internal/cli/run` (`venvShadowMountArgs`) |
| The same policy as Seatbelt denies | `internal/macosuser` (`SeatbeltProfile`, `readonlyDenies`) |
| The "this backend cannot honor that" warnings | `internal/macosuser` (`orchestrator.go`) |
| Entry validation (relative, `..`-free, inside the workspace) | `internal/config` (`validate.go`), `internal/cli/run/mounts.go` |

**Reads with:** [`../design/trust-paths.md`](../design/trust-paths.md) — the **inbound** mirror
of this document: the paths by which *someone else's* content runs in *your jail*. Same shape,
opposite direction, and it is still a live question there.
[`security-shim.md`](security-shim.md) is the privilege-separation model this sits *outside* of;
[`composed-file-permissions.md`](composed-file-permissions.md) is what `:ro` does and does not
buy. For the config keys, run `yolo config-ref`.

---

## The two axes

Visibility is only one of them. The other is **whether you intended to execute anything at all**.

| | **You intended to execute** | **You did not** |
| :--- | :--- | :--- |
| **Visible in a diff** | **the product** — the `Justfile`, the flake, `package.json`, **and every source file in the repo** | **the navigation cell** — `mise.toml`, `.envrc`. Fires on `cd`; review is *possible* and does not happen in time |
| **Invisible** | ≈ empty (an untracked script you deliberately run — you chose it) | **the blind cell** — `.git/config`, `.git/hooks/*`, `.git/info/exclude`, agent settings files, `.mcp.json`. Fires on `git status`, `git commit`, or opening the folder |

Those three names — **the product**, **the navigation cell**, **the blind cell** *(coined in this
document's design predecessor, 2026-08-23)* — are the vocabulary for the rest of it.

**The good-enough criterion, stated once:** *every execution the host performs should be one you
intended, on content you could have seen.* The two failure modes are the two axes, and they take
different fixes — **restore intent** where the trigger is navigation, and **restore visibility**
where the content is unreadable.

> [!IMPORTANT]
> **The product cell is not a security boundary and yolo should spend nothing on it.** "Tracked
> files whose content the host eventually executes" is not four config files, it is the entire
> repository: the moment you run `just build` on the host, every source file the agent wrote is a
> host-execution path. The control there is code review — not a mechanism being invented here but
> software development working normally — and no mechanism yolo adds improves it. Singling out
> four files is the theatre this analysis exists to argue against.

### Intent has two settings, and one collapses the product cell

| | **Per-act intent** | **Standing intent** |
| :--- | :--- | :--- |
| Shape | you re-decide every time — `just build`, `npm install` | you decided once; every write after that executes with no new decision |
| Examples | the whole product cell | a host dev server, a host-side LSP or formatter, an IDE background task, a file-watching test runner |
| Review window | non-zero — the diff exists before you choose to run it | **zero.** Execution fires on save, often before the agent has finished the task |

**Under standing intent the product cell's control is not weakened, it is absent.** The file is
visible in principle and visibility only buys anything if it precedes execution. That makes a
host-side watcher strictly worse than the `Justfile` on the axis that matters while looking
identical on the axis that is easier to draw.

> [!WARNING]
> **`node_modules/` is the worst-shaped instance and it is not a config file.** It is gitignored
> (invisible), auto-loaded by every host-side Node tool that resolves a require, jail-writable,
> and far too large to review. Same shape for `.venv/`, `target/`, `__pycache__/`. This class is
> **invisible *and* standing**, and its fix is neither cell's — see
> [per-side shadowing](#move-the-watcher-in-and-shadow-the-derived-directories).

### Why the blind cell is not theatre

Against the two threats worth naming: a misguided agent with a live collaborator arrives through
the *product* path — bad code in a tracked file — and yolo adds nothing there. Prompt injection
escalating to the host is the one the blind cell defends, and the defence is specific: an injected
agent *chooses its channel*, and its whole advantage is choosing one you never look at.

**The honest claim, with no inflation: this stops zero prompt injections.** It does not reduce
what a compromised session can write. What it removes is the ability to act **unobserved**.

That framing is still the weaker argument, because review of agent output is often nominal. The
argument that survives is qualitative:

> **The blind cell is the only channel that survives every undo a developer has.**
>
> | Undo | Removes a payload in a tracked source file? | Removes `.git/hooks/pre-commit`? |
> | :--- | :--- | :--- |
> | `git checkout` / `git restore` | ✅ | ❌ — not tracked |
> | `git revert`, or dropping the branch | ✅ | ❌ — never was a commit |
> | `git clean -xdf` | ✅ | ❌ — does not touch `.git/` |
> | reverting the PR after you find it | ✅ | ❌ |
> | re-cloning | ✅ | ✅ — and nobody re-clones |
>
> A source payload executes when you run that code. A `pre-commit` hook executes **on the host,
> on your next commit in that repo, forever** — and every instinct a developer reaches for leaves
> it in place, because none of them can see it.

> [!WARNING]
> **`.git/info/exclude` is the load-bearing entry**, because it is the file that *decides* what is
> invisible. It is jail-writable and untracked, so a session that appends a path to it moves that
> path from the visible row to the invisible one — silently, and for every future session.
> "Visible in a diff" is not a stable property unless the file defining visibility is itself
> locked.

## The inventory

Ordered by how little the host user has to do to trigger it. In every row the code runs as
**you**, unsandboxed. The set is not exhaustive by construction — it is the set of channels whose
*trigger* is something a person does without deciding to execute anything.

| Channel | Host act that fires it | Cell |
| :--- | :--- | :--- |
| `mise.toml` → `[env]`, `[hooks]`, `[tasks]` | **`cd` into the repo**, with mise activated in your shell | navigation |
| `.git/config` → `core.fsmonitor` | **`git status`** — also `add`, `commit`, `checkout` | blind |
| `.git/config` → `alias.<x> = !sh` | `git <x>` | blind |
| `.git/config` → `core.pager` | `git log`, `git diff` — anything paged at a TTY | blind |
| `.git/config` → `core.hooksPath` | any hooked command; **a local value beats a global one** | blind |
| `.git/hooks/*` | `git commit`, and every other hooked command | blind |
| `.git/config` → `core.sshCommand`, `gpg.program` + `log.showSignature`, `diff.*.textconv`, `filter.*.clean`, `credential.helper` | fetch/push, `log`, `diff`, `checkout` | blind |
| An agent's own settings file → hooks, credential-helper scripts | **opening the repo in a host agent session** | blind |
| The `Justfile`, the flake, `package.json` | `just`, `npm install` | product |

> [!CAUTION]
> **The agent-settings row is the worst-shaped one, and it needs no git command at all** — no
> `cd` with mise active, no commit. Just opening the project on the host. Agent settings files
> carry hook commands and credential-helper scripts documented as executing through a shell, and
> a settings file reached through a symlink into a gitignored directory is jail-writable,
> host-read, and invisible to `git status` at the same time.

Everything in the table is writable from a jail: the jail runs as UID 0 in a user namespace that
maps to the invoking host user, so the bytes land owned by you.

## Why the two obvious guardrails do not fire

| Guardrail | What it keys on | Why it misses |
| :--- | :--- | :--- |
| git `safe.directory` | **ownership** — it refuses to parse `.git/config` or run hooks in a repo owned by *another* user | The jail writes through a user namespace that maps to **your** UID. Ownership never changes, so the check is satisfied on every `.git`-shaped row. **Structurally blind, not partial** |
| mise trust | **path** | The path was trusted before the agent existed and stays trusted through arbitrary rewrites. Content hashing exists only under mise's `paranoid` mode, which is off by default |

mise's trust record is a name derived from the *path*, with no content hash in it at all — its own
documentation is candid that in normal mode a config file needs trusting once, regardless of
modifications. So: you trusted this repo's `mise.toml` months ago, every jail session since has
had write access to it, and `cd`-ing into the directory is enough to run whatever it now says.

**And upstream is not coming.** Git's security team's stated position on config-driven execution
is that it belongs to integrators, not to git: tools should not run git opportunistically against
untrusted repositories. That is a reasonable line, and it makes **yolo the integrator**, since
yolo is the thing that turned a directory you trust into a directory an agent writes.

> [!NOTE]
> **The same shape as the inbound analysis, rotated 180°.**
> [`../design/trust-paths.md`](../design/trust-paths.md) found that every inbound gate keys on a
> *declaration* and none on *content*. Here the two outbound guardrails key on *ownership* and
> *path* — and again, neither on content. A jail session changes content and nothing else, which
> is precisely the axis nothing measures.

## Locking the blind cell: `workspace_readonly`

`workspace_readonly` overlays a `:ro` bind per listed path on top of the writable workspace bind.
Entries must be relative and `..`-free; one that escapes the workspace or does not exist is
skipped with a warning. **When any entry is active it also locks the workspace config file
itself**, so a jail session cannot switch its own protection off.

The entry set that matches the blind cell is the `.git` control plane plus the agent-settings
paths — `.git/config`, `.git/hooks`, `.git/info`, the agent settings directory, `.mcp.json`, and
`.envrc` where one exists. `.git/info` rather than `.git/info/exclude`, because the directory is
the stable thing to bind. **`.git` wholesale is wrong**: the agent must write refs, objects and
the index to commit at all.

> [!CAUTION]
> **Locking `.git/hooks` alone is defeated by one line in `.git/config`.** `core.hooksPath`
> repoints hooks anywhere, and a local value beats a global one, so a session that cannot write
> `.git/hooks/pre-commit` writes its own directory and repoints instead. **It is the `.git`
> control plane as a unit, or it is not worth doing.** There is no cheap half of this one.

### Hooks still need a channel — redirect rather than ban

"The agent has no legitimate reason to write `.git/hooks`" is false: installing a hook is
ordinary work, and a flat deny with no alternative is a capability regression rather than a
security control.

The fix falls out of the two axes: **hooks do not need to be writable, they need to be visible.**
Point `core.hooksPath` at a tracked in-repo directory and the agent writes hooks there like any
other code — committed, diffed, reviewed, reaching the host through the same path as every other
file it produces. `.git/hooks` stays locked because it is the **invisible** copy, not because
hooks are dangerous. In the two-axis table that is a promotion out of the blind cell into the
product cell, which is the whole design in miniature. It is also standard practice independently
of any of this.

**The wrinkle worth stating:** `core.hooksPath` is itself a `.git/config` key — one being locked
precisely so a session cannot repoint it — so the agent cannot set this, by design. Something
host-side must, before the mount. That is [`security-shim.md`](security-shim.md)'s shape exactly:
the unsandboxed step configures, the sandboxed side cannot revise.

### What it costs, measured rather than assumed

Locking `.git/config` was priced too high on the assumption that everyday git writes local
config. It mostly does not: `commit`, `checkout -b` from a *local* branch, `push origin HEAD`,
`fetch` and `pull` write nothing. Only two operations do — `push -u` (which writes
`branch.*.remote`) and branching from a *remote-tracking* ref — **and both have a config-free
equivalent.** For a repo whose convention is to stay on the current branch the cost rounds to
zero; for one whose agents branch and push it is one convention, not a broken workflow.

### The backends do not agree, and the mounts are not the policy

| Control | `podman` | `container` (Apple) | `macos-user` |
| :--- | :--- | :--- | :--- |
| `workspace_readonly` | ✅ enforced | ❌ `:ro` is ignored — **warns loudly**, and cannot skip the paths, since they live inside the writable workspace bind | ✅ enforced as Seatbelt denies |
| Per-side shadowing | ✅ enforced | ✅ (a mount, not a `:ro` mount) | ❌ **no equivalent exists** — warns |
| `core.hooksPath` redirect | ✅ | ✅ | ✅ — a git config key, backend-independent |
| mise `paranoid`, host-side | ✅ | ✅ | ✅ — host-side, backend-independent |

**The mount was only ever the delivery mechanism; the policy is "these paths are not writable".**
`macos-user` has no mount namespace at all and expresses that policy natively: its Seatbelt
profile is already a deny-list with re-allows (deny writes everywhere, then re-allow the agent's
writable set), and SBPL is last-match-wins, so a `workspace_readonly` entry is one more deny form
appended *after* the workspace allow. No `:ro` to be ignored, no mount at all, and it lands in
the one file that is already the backend's whole write policy — a better fit there than the mount
is anywhere else.

> [!WARNING]
> **The honest asymmetry: per-side shadowing does not port, and cannot.** It needs *two different
> contents at one path* — the host's `node_modules` and the jail's, simultaneously. That is a
> mount-namespace capability. Seatbelt is a permission filter: it can deny access to a path, but
> it cannot make one path resolve to two directories. So the invisible-and-standing class has no
> fix on `macos-user`, and the backend says so at launch rather than accepting the key silently.

> [!WARNING]
> **The config self-lock is a container-path behavior.** On `macos-user` the declared entries
> render as denies, but the workspace config file itself is not among them — so the "a session
> cannot switch its own protection off" property does not hold there. Do not assume the two
> backends enforce the same set.

> [!NOTE]
> **A `macos-user` agent writes as a different user than you**, which is the precise condition
> git's `safe.directory` check looks for and which the container case defeats. Whether that
> accidentally restores the guardrail depends on which paths git ownership-checks versus which
> ones the sandbox user ends up owning. It would be good news and it is **unverified** — every
> measurement behind this document was made in a Linux container jail.

## Move the watcher in, and shadow the derived directories

A watcher cannot be made safe on the host, because the thing that makes it dangerous (zero
latency from write to execution) is the thing it exists to do. So do not put it there. Both
primitives that need is served by already ship:

- **The dev server itself** — publish a jail port to the host, so the server runs in the sandbox
  and you browse it from the host as usual.
- **`node_modules/`, `.venv/` and friends** — per-side shadowing mounts a derived directory so
  **host and jail each get their own copy** and it never crosses the boundary. Host tooling cannot
  load what the jail wrote if the two sides never share the directory. `node_modules` and `.venv`
  are in the default set; the shadowing is root-level only, so a monorepo names its nested
  directories explicitly.

The connection worth making explicit: **per-side shadowing was built for a correctness problem** —
interpreter symlinks and native builds that break when two platforms share a directory — and it
happens to be the exact right shape for this security one. That is why it is justified on
correctness and the security benefit is the weaker of its two reasons.

**What is genuinely irreducible:** some work cannot move into the jail — a native GUI app, a
device- or GPU-bound simulator. For those the host *is* executing agent output continuously and
no configuration changes that. The honest posture is disclosure rather than mitigation: while
such a watcher runs, treat that jail session's output as already running on your host, because it
is.

> [!NOTE]
> Moving a server into the jail leaves your **browser** loading JS the jail served. That is the
> ordinary web threat model, not host code execution, and the browser sandbox is the control —
> worth stating only so the boundary is not overclaimed.

## What this does not license

- **Not a claim that the sandbox is broken.** No container boundary is crossed. Every path
  requires the *host* to run something, and the live workspace bind is the product working as
  designed.
- **Not an argument for a read-only workspace.** The agent's whole job is writing there.
- **Not a general "review everything the agent writes" policy.** The product cell is already
  covered by ordinary diff review; the defensible set is a handful of paths wide.
- **Not a defence against a human who runs a build recipe on unreviewed agent output.** That is
  the product cell, and the control is reading the diff.
- **Not a fix for host-side work that genuinely cannot move into the jail.** Nothing here makes a
  host-side watcher safe; it only makes the common case unnecessary.
- **Not a promise that a detector would help.** See below.

## Deliberately not built

Each of these was considered against the *"is this theatre?"* test and declined, with the
condition that would reopen it. The through-line: the two things that shipped are true **without**
the threat model — a config key that lied on one backend, and a derived directory two platforms
could never safely share. Everything below needs the threat model accepted first, and several
also cost either a workflow change or a behaviour yolo cannot enforce.

- **`workspace_readonly` over the `.git` control plane, in this repo's own config.** The strongest
  of the set and the closest to worth doing — its argument is the persistence table above. Not
  done because it is a config-only change any user can make today, it asks the user to accept the
  threat model first, and it needs the `core.hooksPath` redirect alongside it or it is a
  capability regression. *Reopens on:* any real instance of a jail session touching `.git/`, or
  simply deciding the persistence argument is enough. If taken, **it must be the whole control
  plane.**
- **yolo setting `core.hooksPath` itself.** Hooks need somewhere tracked to live before
  `.git/hooks` can be locked, and the agent cannot set the key by design. Not done because it is
  only needed if the item above is taken, and yolo writing into a repo it does not own is exactly
  the quiet host-side mutation the [config-approval gate](config-safety.md) exists to make loud.
  *Fallback if that objection stands:* a runbook line plus a `yolo check` warning.
- **`workspace_readonly` from user-scope config.** Would close the bootstrap gap — the workspace
  config self-locks only once it *has* entries, so a workspace whose first-ever session had none
  is unprotected for that session — and would make the fix once rather than per-repo. Not done,
  and whether the host merge path honours the key at user scope was never verified.
- **Recommending host-side mise `paranoid`.** It closes the `cd` channel at its root and is
  durable: mise now ignores trust-control settings that come from a non-global config, so a
  jail-written workspace config cannot turn it back off. Not done because it is a change to the
  human's machine rather than to yolo, and documenting it means owning its friction. *Reopens on:*
  the maintainer adopting it and finding the friction acceptable.

> [!WARNING]
> **A tripwire that hashes the danger set and reports changes is not a control.** It only
> *reports*, which is a receipt rather than a gate, and for the product cell it duplicates
> `git diff` — which already works and which the human already reads. Its one non-redundant use
> is blind-cell paths on Apple Container, where `:ro` cannot be enforced and detection is all
> that is available.

> [!WARNING]
> **A hardened `git` wrapper does not work.** `-c core.fsmonitor=` and `-c core.hooksPath=…` do
> override local config, but the defence has to enumerate `fsmonitor`, `pager`, `sshCommand`,
> `gpg.program`, `textconv`, `filter.*`, `credential.helper` and whatever git adds next — and
> **`alias.*` cannot be blanket-cleared by `-c` at all.** A defence that must enumerate a set
> upstream is free to extend is the halfway measure this repo keeps deleting.

## Why it's this way

| Ruling | Why it holds |
| :--- | :--- |
| <a id="oq-hx5"></a>[**OQ-HX5**](#oq-hx5) — `node_modules` joins `.venv` in the default per-side set, root-level only | Justified on **correctness** (a `node_modules` shared between a macOS host and a Linux jail is already broken for any native build), with the host-execution benefit as a side effect. A default that needs the threat model believed would not have shipped. |
| <a id="oq-hx6"></a>[**OQ-HX6**](#oq-hx6) — wire `workspace_readonly` into the Seatbelt profile, and treat the silent no-op as the bug | A shipped, documented key whose reference text promises to protect host-executed code, doing nothing on a backend, is a defect whether or not anyone uses it for the blind cell. The `per_side_paths` half resolved to **warn, not refuse**: the key is inert there and cannot be made otherwise, but refusing it would break configs that carry it harmlessly for other backends. |
| <a id="oq-hx2"></a>[**OQ-HX2**](#oq-hx2) — the `.git` control plane is a config choice, not a shipped default, and it is all-or-nothing | Half-answered by measurement: the cost is far smaller than assumed, and the "lock hooks, leave config" option does not exist because `core.hooksPath` defeats it. |
| **The product cell gets nothing** | Its control is code review, which is not a security mechanism being invented here, and no mechanism yolo adds improves it. Anything built for that cell is theatre — including anything aimed at policing how someone runs their own machine. |

## Current values

Verified at `38873c0d`. The prose above explains what each of these is for; this table is the
only place the values themselves are stated.

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| Blind-cell lock key | `workspace_readonly` (list of workspace-relative paths) | `yolo config-ref`, `internal/cli/run/mounts.go` |
| Self-lock | the workspace config file is bound `:ro` whenever any entry is active (container backends) | `Options.workspaceReadonlyMountArgs` |
| Per-side shadow key | `per_side_paths`; defaults `.venv` ∪ `node_modules` ∪ the mise-config venv path | `Options.venvShadowMountArgs` |
| Shadow backing store | `<workspace state>/venv-shadows/`, with `/` rendered `__` | `Options.venvShadowMountArgs` |
| Port publishing | `network.ports` (`"HOST:JAIL"`) | `yolo config-ref` |
| macos-user rendering | one `(deny file-write* …)` form, one `(subpath …)` clause per entry, appended after the writable-set allow | `macosuser.readonlyDenies` |
| Entry validation | relative, `..`-free, must exist, must stay inside the workspace; offenders skipped with a warning | `internal/config/validate.go`, `internal/cli/run/mounts.go` |

## Sources

- [mise — Paranoid mode](https://mise.jdx.dev/paranoid.html) and [mise trust](https://mise.jdx.dev/cli/trust.html) — *"in normal mode, a config file only needs trusting once, regardless of modifications"*
- [GHSA-436v-8fw5-4mj8 / CVE-2026-35533](https://github.com/jdx/mise/security/advisories/GHSA-436v-8fw5-4mj8) — local configs can no longer set trust-control settings
- [jdx/mise#4499](https://github.com/jdx/mise/discussions/4499) — paranoid hash-file name collision on shared leaf directory names
- [justinsteven — buried bare repos and fsmonitor abuses](https://github.com/justinsteven/advisories/blob/main/2022_git_buried_bare_repos_and_fsmonitor_various_abuses.md) — the config-key inventory, and git's "this is an integrator problem" position
- [Cobalt — exploiting git FSMonitor for initial access](https://www.cobalt.io/blog/red-team-technique-exploiting-git-fsmonitor-for-initial-access)
- [Sonar — Claude Code executed code before you click 'trust'](https://www.sonarsource.com/blog/claude-arbitrary-code-execution/) — `core.fsmonitor`, then `gpg.program` + `log.showSignature`, then an agent settings file's credential helper
- [GHSA-9ccr-r5hg-74gf / CVE-2026-45033](https://github.com/github/copilot-cli/security/advisories/GHSA-9ccr-r5hg-74gf) — the same fsmonitor channel in another agent CLI
- [git CVE-2022-24765 / `safe.directory`](https://github.com/git/git/security/advisories/GHSA-vm9j-46j9-qvq4) — the ownership check, and why it is blind here
- [clampdown](https://github.com/89luca89/clampdown) and [sandcat](https://github.com/VirtusLab/sandcat) — protected-path precedent: `clampdown` makes `.git/config`, `.git/hooks`, `.gitmodules`, agent config dirs and `.mcp.json` read-only by default, with the stated rationale *"prevent a compromised agent from altering its own runtime configuration"*
