---
status: current
verified: 2026-09-09
verified_commit: 38873c0d
covers:
  - internal/config/hostfiles.go
  - internal/config/writablehome.go
  - internal/entrypoint/hostfiles.go
  - internal/entrypoint/helpers.go
  - internal/cli/configls.go
  - internal/cli/configdiff.go
tags: [config, prism, permissions, postures, host_files, capture]
summary: "Which files the composition engine produces are read-only, which are read-write, and how to tell: the Derived/Shared/State taxonomy, the host-linked axis that sharpens it, why `0o444` is not a posture and `:ro` buys non-persistence rather than immutability, and the two classes of writer a posture has to serve."
---

# Composed-file postures — what the prism makes read-only, and what it must not

**Status:** CURRENT as of 2026-09-09, verified against `38873c0d`.

Every file the composition engine writes has a *posture*: who may write it, and what happens to
their write. The instinct to keep as much read-only as possible is right in spirit and cannot be
applied uniformly, because composed files fall into **three genuinely different kinds** — and
treating them as one is how a credential-bearing file ends up rendered from defaults.

**One question decides the posture: does anything other than yolo write this file?**

| Kind | Who writes it | Correct posture |
| :--- | :--- | :--- |
| **Derived** — a pure function of host state and config | yolo only | **read-only**, kernel-enforced where possible |
| **Shared** — yolo composes it *and* the agent legitimately rewrites it | both | **read-write, capture required and visible** |
| **State** — the agent owns it; yolo injects a few keys | agent | **read-write, never composed wholesale** |

There is no fourth answer, and "read-only-ish" is not one of the three: a `0o444` chmod is
[not a posture](#why-0o444-is-not-a-posture).

> [!IMPORTANT]
> **"Something else writes it" hides two writers with opposite needs**, and the posture question
> is only half the story. A *program* writing its own config is unsteerable, and capture is
> exactly right for it. A *human-directed agent* is steerable, and capture is often the wrong
> outcome, because the durable answer was a config key it should have edited instead. See
> [Who is writing](#who-is-writing-program-operation-vs-directed-agent).

| Component | Lives in |
| :--- | :--- |
| `host_files` modes, defaults, and destination validation | `internal/config` (`HostFileMode*`, `hostFileDefaultMode`, `checkHostFileDest`) |
| Reserved destinations, file and subtree | `internal/config` (`reservedHomeFiles`, `reservedHomeSubtrees`) |
| Applying a mode in the jail, and the lock/unlock pair | `internal/entrypoint` (`hostfiles.go`, `hostFileModes`) |
| Writing a composed file, and its mode | `internal/entrypoint` (`WriteStringInPlace`, `writeInPlaceString`, `writeExecutable`) |
| Making capture visible and discardable | `internal/cli` (`configls.go`, `configdiff.go` — `yolo config ls`/`diff`/`reset`) |
| Home-root aliases into a writable overlay | `internal/storage` (`EnsureSymlink`) |

**Reads with:** [`pack-system.md`](pack-system.md) (how a surface is declared, the four modes, and
the layer fold), [`config-migration-to-prism.md`](config-migration-to-prism.md) (the boot state
machine and the capture sidecars), [`jail-home.md`](jail-home.md) (the mount stack these postures
sit on), [`git-identity.md`](git-identity.md) (the worked Derived case),
[`../reference/composed-file-permissions.md`](../reference/composed-file-permissions.md) (the three
open questions this reference left behind).

---

## The second axis: is the surface host-linked?

Orthogonal to the three kinds, and it sharpens the rule rather than adding a fourth kind:

- **Host-linked** — the host's copy is `:ro`-mounted under `/ctx` and composed in as the `host`
  layer, so a host-side edit propagates on the next launch.
- **Jail-only** — no host file crosses; the surface is composed purely from yolo's own layers.

Which surfaces are host-linked is decided by the surface's own **`readsHost` declaration**, not
by config: it is a credential boundary no config key can widen. Most surfaces are jail-only.

Crossed with the three kinds, **two cells are deliberately empty, and naming them is the point of
the axis:**

- **Derived + host-linked is empty.** A file yolo regenerates from a host source with nothing
  else writing it does not need the engine at all — that is the composed git config, which is
  host-composed and `:ro`-mounted rather than being a surface.
- **State + host-linked is empty because it is forbidden.** Crossing the host's *identity* file
  is what caused a shared single-use refresh-token chain to burn itself — host and jail agent
  each refreshing and invalidating the other. The identities are separated permanently: an
  agent's preference file may be mirrored from the host, and its identity/session file must not
  be. Same directory, same agent, opposite treatment, for a reason.

**Host-linkage does change one thing: a host-linked surface must stay read-write with capture.**
Read-only would break the promise that host edits propagate *and* that in-jail edits survive, and
both are required because the host file is a real source of truth someone else maintains. It also
makes capture **more** dangerous there, and that asymmetry is worth stating plainly: on a
host-linked surface a captured overlay outranks the host layer forever, whereas on a jail-only
surface there is no host truth to fork from, so a stale capture is merely stale.

## What yolo injects into a State file, and why it must

The model State surface is the agent's own identity-and-session JSON. It holds dozens of
top-level keys of pure agent state — startup counters, onboarding flags, the OAuth account — and
yolo must still touch exactly three things:

| Injected | Why yolo must |
| :--- | :--- |
| the reconciled MCP server table | this is where the agent reads user-scoped MCP from; without it the `mcp_servers` config does nothing and MCP is simply absent |
| the workspace's trust-dialog acknowledgement | an agent cannot answer an interactive dialog |
| the workspace's project-scoped-MCP toggle | lets the workspace's own servers load without per-server approval |

The reconciliation is why this cannot be left alone: the MCP block must be **re-derived every
boot** from live config, so a server dropped from config disappears — while the rest of the file
is untouched. In the shipped mechanism that posture is a first-class surface mode: the surface is
declared **`rmw`** (read-modify-write), with the trust key as a `defaults` layer, the toggle as a
`managed` layer, and the MCP table riding the computed layer. No capture sidecar, no overlay
precedence, no first-migration hazard.

**The file being persistent is what makes State a safe posture rather than a compromise** — it
lives on the per-workspace home, survives every restart, and yolo never regenerates it. The
temptation the kind guards against is not touching it; it is **composing** it, which is what put
a live OAuth token in a render path once already.

## Why `0o444` is not a posture

`host_files` mode `readonly` chmods the destination `0o444`. That is DAC, not kernel enforcement —
a strong signal and a speed bump, not a sandbox — because an agent running as UID 0 ignores mode
bits. The finding this reference adds is that it is worse than weak: it is **asymmetric**, which
is the one thing a predictability-first design cannot tolerate.

- as **root**: the write succeeds. The mode accomplishes nothing.
- as a **non-root** agent: the composing writer opens the locked file `O_TRUNC`, gets `EACCES`,
  warns, and **the surface never updates again** — every boot.

So one declaration means "no protection" for one agent and "silently stops re-rendering" for
another. The three honest mechanisms, and what each actually buys:

| Mechanism | Enforced? | Root bypass | Composition available | Backends |
| :--- | :--- | :--- | :--- | :--- |
| `:ro` bind mount | **kernel** | no | **none** — you cannot compose into a `:ro` mount | podman only |
| `0o444` chmod | DAC only | **yes** | full | all |
| read-write + capture | no | n/a | full | all |

Apple Container ignores `:ro` and cannot do single-file binds, so every `:ro` surface degrades to
a writable materialized copy there. `macos-user` has no bind mounts at all, so `:ro` is
structurally absent and a mount-shaped control has to be re-expressed in its Seatbelt profile —
see [`host-execution-from-the-workspace.md`](host-execution-from-the-workspace.md), where exactly
that translation is done for `workspace_readonly`.

> [!WARNING]
> **Even `:ro` is only enforced at the mount path.** A composed file is staged inside the
> workspace's own state tree and then bound read-only into the home — the *same inode*, reachable
> through the writable workspace path. An in-jail agent can edit the file through the staging
> path and the change appears instantly at the `:ro` path. What `:ro` really buys is not
> immutability but **non-persistence**: the next run unconditionally rewrites the staged file.
> Surfaces staged *outside* the workspace (briefings, skills) are genuinely out of reach.

> [!WARNING]
> **A mode bit is not a security measure here; it is a routing signal**, and its asymmetry is bad
> precisely because it routes some agents and not others. If the signal matters, it needs a
> mechanism that reaches root too — a header in the file, or a line in the briefing — rather than
> a permission an agent running as root never sees.

## The model, posture by posture

### Derived → read-only, and say so

- **A `:ro` bind mount** where the content can be composed **host-side** and the backend supports
  it. Stage it *outside* the workspace so the inode is genuinely unreachable.
- **A header comment in the file itself** where the codec allows: a user who cannot write the
  file deserves to know why, and where to write instead. A structured JSON surface cannot carry
  one at all, which bounds how far this can go.
- **No sidecars.** There is nothing to capture.

### Shared → read-write with *visible* capture

Read-write in an overlay, with capture — plus the three things that make capture defensible
rather than a hidden precedence layer: a per-surface listing (path, codec, posture, contributing
layers, overlay key count), a boot notice when a surface renders with a non-empty overlay, and
commands to inspect and discard (`yolo config diff` / `yolo config reset`).

> [!NOTE]
> **Why a tool-version config is a composed surface at all.** The legitimate case for the
> `mise_tools` knob is "mine, in every jail, but not on my host" — a user-scope preference with
> nowhere else to live, which is why it stays a real knob even though yolo's own default list is
> empty. Anything bakeable belongs in the image instead, not in a layer.

### State → never composed wholesale

**If a file holds credentials or session state, yolo may inject keys but must never render it
from layers** — because a first-migration render composes from the layers alone, and on a fresh
workspace or a lost sidecar that means from defaults alone. The shipped expression of this rule
is the `rmw` surface mode.

## What this means for `host_files`' modes

`host_files` has four modes — `readonly`, `once`, `copy`, `capture` — and they collapse to **two
postures plus one seed flag**, which is the same taxonomy as above:

| Mode | Its posture | Note |
| :--- | :--- | :--- |
| `readonly` | Derived | The host file is the truth, so **non-persistence is the actual goal**, not unwriteability — which is why the honest implementation is a `:ro` mount where one is available, else a copy plus a header, never a bare chmod |
| `copy` | Derived | Differs from `readonly` only in the chmod, which is the part that does not work |
| `once` | seed-then-leave-alone | The genuine case for a source-less file: no sidecar, no precedence puzzle, the cheapest correct posture |
| `capture` | Shared | The only mode that writes sidecars, and never a default — a captured edit outranks the host layer forever |

The locked mode is derived per source rather than fixed: an executable host source locks to
`0o555` and unlocks to `0o755`, everything else `0o444`/`0o644`. That pairing is load-bearing —
unlocking an executable to `0o644` and re-locking it to `0o444` silently strips the execute bit.

> [!NOTE]
> **Whether `readonly` should become a real `:ro` mount, and `copy` then merge into it, is one
> open decision tracked outside this document** — as `E1`/`E2` in
> [`../plans/BACKLOG.md`](../plans/BACKLOG.md) and as `OQ-B` (the host-side twin) in
> [`../plans/pack-host-management-plan.md`](../plans/pack-host-management-plan.md). Those IDs are
> the API; the sections above are that cluster's *argument*. Anyone answering one of them is
> answering all of them, so do not mint a fourth name for it.

## Home-root destinations and new top-level directories

A `host_files` destination may name a home-root dotfile or a path under a directory that does not
exist yet, and both need a mechanism, because:

- the home root is `:ro`, so writing a file there gets `EROFS` and creating a directory there gets
  `EROFS`;
- the `writable_home_dirs` pattern cannot serve a home-root **file** — it makes the destination a
  *directory*, so the composed write fails "is a directory";
- pre-creating an empty backing file and bind-mounting it **breaks `once`**: a stat on a
  bind-mounted empty file succeeds, so the seed-if-absent guard returns early on the first boot
  and the file stays empty forever.

**A home-root destination therefore uses the `GlobalHome` relative symlink** — the same mechanism
the shell rc and the agent identity file already use: materialize `~/<name>` pointing into a
directory that resolves through the mount table into an already-writable overlay. It needs no new
mount, it reuses a mechanism with existing precedents, and — the detail that makes it correct —
**a dangling symlink keeps `once` honest**: the stat returns `ENOENT` on the seeding boot and
succeeds afterwards.

A **new top-level directory** destination uses `writable_home_dirs` staging for the destination's
parent, which is the case that mechanism was built for.

> [!NOTE]
> **Do not rely on the OCI runtime refusing to create a nested mountpoint inside a `:ro` bind.**
> Repeated experiments could not reproduce that refusal in any realistic variant, including with
> a read-only container; the only reproduction was when the `:ro` bind's *host source* was itself
> on a read-only filesystem. Keep the explicit pre-create — it is cheap, idempotent, and makes
> ownership and mode deterministic — but treat the "the runtime cannot create it" reasoning as
> version-dependent rather than a cross-runtime guarantee.

## Who is writing: program operation vs directed agent

| | **Class 1 — program operation** | **Class 2 — human-directed agent** |
| :--- | :--- | :--- |
| Who | the agent *binary*, as part of functioning | the agent *reasoning*, because a human asked |
| Example | a `/settings` command writing its own JSON; `npm config set`; a first-run write | "add tool X", "raise the memory limit", "add this MCP server" |
| Steerable? | **No.** You cannot ask a program to write elsewhere | **Yes.** A skill, an error message, or a header can point it at the right path |
| Right outcome | **preserve it** — this is precisely why capture exists | **redirect it** — the durable answer is usually a config key, not this file |
| Wrong outcome | reverting the user's own choice on the next boot | capturing an edit that should have been a config change |

Two consequences, pulling in opposite directions:

1. **Capture is correct for class 1 and is its whole motivation.** Nothing else can preserve a
   choice a program made about its own config across a regeneration. Removing capture to
   "simplify" would break the one case it was built for.
2. **Capture is a consolation prize for class 2.** The edit *works*, which is exactly why nobody
   notices it went to the wrong place: it is invisible to the workspace config, absent from the
   repo, and lost on the next `yolo config reset`. **Silent success in the wrong file is worse
   than a loud failure**, because it never gets corrected.

So the design goal is not "capture more" or "capture less" — it is **route by class**: make class
1 work silently, and make class 2 fail in a way that names the alternative.

### The three axes a directed agent actually needs

For any "change X" request the answer has three parts, and they are answered in different places:

| Axis | Question | Where the answer lives |
| :--- | :--- | :--- |
| **Source of truth** | which file should hold this durably? | `yolo config-ref`, for jail keys |
| **Application** | does it apply live, need a restart, or need a rebuild? | the `configuring-the-jail` skill, for jail keys |
| **Escape hatch** | is there a live invocation that avoids the restart? | that same skill (`npm i -g`, `mise use -g`) |

The load-bearing gap is the column those three share: **for agent-surface keys there is no config
source of truth at all.** A request to set an agent's model, theme or hooks has no jail config
key, so "edit the composed file and let capture hold it" is not the agent doing the wrong
thing — it is the only thing available, and it makes capture load-bearing for class 2 as well
until a knob exists.

The pattern worth copying for class-2 routing already exists in the tree: a blocked-tool blocker
prints the reason *and* a suggestion line, then exits 127. Reason, alternative, and a documented
bypass.

## The restart axis, precisely

Class-2 guidance depends on this, and the boundary is sharper than it looks:

- **Mounts and the entire environment block are frozen at container create.** The attach path
  execs into the existing container with no `-e` and no `-v`, so nothing carried by environment
  or mounts can change without a new container.
- **The entrypoint re-runs every generator on each attach — but reads only that frozen
  environment.** So composed surfaces *are* re-rendered live, from **stale config**. This is
  idempotent recomputation, not a config re-read, and it is the subtlety most likely to mislead:
  "the file regenerated" does not mean "my config change took".
- **One thing genuinely is live:** briefing and skills refresh runs on every invocation *before*
  the attach branch (inode-preserving writes, and the skills directory is cleared in place rather
  than recreated, precisely so the `:ro` bind keeps working). The consequence worth naming: the
  keys it re-reads have **live briefing text and frozen enforcement** — a jail can describe a
  limit it is not applying.
- **In-jail, config loading returns the assembled snapshot verbatim** and never re-reads the
  workspace config, while **`yolo check` bypasses that loader and validates the raw files.** That
  split is what makes the documented "edit → `yolo check --no-build`" loop work at all.
- **The approval prompt only fires on fresh launch**, after the attach branch has returned, so
  attaching never re-checks config — see [`config-safety.md`](config-safety.md).

## What this does not license

- **Not a claim that a mode bit contains an agent.** Everything here is about predictability and
  routing; containment is the mount namespace's job, and an agent running as root ignores DAC.
- **Not a uniform read-only policy.** Applying the read-only instinct to a Shared or State
  surface is how an OAuth token gets rendered away.
- **Not a licence to make `~/.gitconfig` writable.** The alias resolving to a `:ro` target is a
  legibility defect, not a reason to reopen the composition hole the mount closes.
- **Not a fourth `host_files` mode.** The four collapse to three; a fifth would be a fifth
  spelling of one of the two postures.
- **Not a promise that a captured edit is visible in the repo.** It is not, by construction: that
  is what makes class-2 routing a real problem rather than a cosmetic one.

## Why it's this way

| Ruling | Why it holds |
| :--- | :--- |
| **Three kinds, one question** — does anything other than yolo write this file? | The four `host_files` modes and the two prism postures are the same distinction spelled twice; a taxonomy that answers per-file rather than per-mechanism is what stops a credential surface being treated like a generated one. |
| **`0o444` is not a posture** | It is asymmetric — a no-op for root and a permanent silent render failure for a non-root agent — so the same declaration means two different things depending on who runs. |
| **`:ro` buys non-persistence, not immutability** | The staged file and the mounted file are one inode, and the staging path is writable. Anything that depends on immutability must stage outside the workspace. |
| **Host-linked surfaces stay read-write with capture** | Read-only would break both promises a host layer exists for: that host edits propagate and that in-jail edits survive. |
| **State surfaces are `rmw`, never rendered from layers** | A first-migration render composes from defaults alone, so rendering a token-bearing file wipes it on any boot with no trusted baseline. |
| **Home-root destinations go through a relative symlink into a writable overlay** | It needs no new mount, reuses three existing precedents, and a *dangling* symlink is what keeps `once` correct on the seeding boot. |
| **Capture is routed by writer class, not maximized or minimized** | Class 1 cannot be steered and needs capture; class 2 can be steered and is served worse by a silent success than by a loud failure. |

## Current values

Verified at `38873c0d`. The prose above explains what each of these is for; this table is the
only place the values themselves are stated.

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| `host_files` modes | `readonly`, `once`, `copy`, `capture` — the full key schema is in `yolo config-ref` | `internal/config/hostfiles.go` |
| Mode defaults | source-bearing → `readonly`; source-less → `once`; directory → `copy`; **never** `capture` | `config.hostFileDefaultMode` |
| Locked / unlocked mode pair | `0o555`/`0o755` for an executable source, else `0o444`/`0o644` | `entrypoint.hostFileModes` |
| Composed-file write mode | `0o644`, truncate-in-place — and **umask-masked**, not umask-independent | `entrypoint.writeInPlaceString`, `WriteStringInPlace` |
| Generated-script mode | explicit `0o755` chmod after the write | `entrypoint.writeExecutable` |
| Reserved destinations | `reservedHomeFiles` (single files) and `reservedHomeSubtrees` (directories), including symlink targets | `internal/config/writablehome.go` |
| Surface modes | `stateful`, `computed`, `rmw`, `unrendered` | `internal/agentcfg/manifest` |
| Capture visibility commands | `yolo config ls`, `yolo config diff`, `yolo config reset` | `internal/cli/configls.go`, `internal/cli/configdiff.go` |
