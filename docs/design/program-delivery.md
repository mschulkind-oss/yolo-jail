---
title: "How executable content gets into a jail — and what makes two jails the same"
date: 2026-08-24
status: decided
tags: [packs, uniformity, delivery, pinning, npm, mise, image]
summary: "Four delivery classes, one of which keeps no record and is never re-derived — and all divergence lives there. The fix is a receipt, removal and scope agreement, not an npm pin."
---

# How executable content gets into a jail — and what makes two jails the same

**Status:** DECIDED, 2026-08-24. **Nothing built yet** — the build order is
[§10](#10-what-i-would-build-in-order). All ten questions are ruled (the
[Decision Ledger](#decision-ledger)). Every fact below is labelled
**MEASURED** (observed in this development jail, 2026-08-24), **READ FROM CODE** (traced but not
observed running) or **NOT MEASURED**.

**The short version.** Executable content reaches a jail through four mechanisms, and they are not
four flavours of one thing: content is **baked** (nix, hermetic, recorded), **regenerated** (packs,
skills, surfaces — cleared and rebuilt every launch), **installed-and-kept** (agent CLIs, LSP
servers, mise tools, claude plugins, `npx` MCP packages) or **mounted** (a pointer, not content).
The first two are uniform by construction; the fourth inherits whatever it points at. **All
divergence lives in the third class**, whose defining property is not "npm" but *"yolo declares a
name, a third party decides the bytes, and nothing writes down what came back."* So the maintainer's
premise — *"that'll make all jails uniform, given the pack set and lockfile"* — is half right in a
way that matters: a user-scope lockfile over the pack set can reach the npm half and cannot reach
mise, the image tag, the LSP recipes, the claude plugins, or an `npx -y` MCP argv, and it fixes only
one of the three properties uniformity actually needs. **My recommendation is to build the RECEIPT
first — the artifact that says what this jail got — then removal, then the pin.** And much of the
receipt is not yolo's to build: where an ecosystem already keeps a lockfile with a resolver behind
it, yolo adopts it ([§5.6](#56-a6--borrow-the-ecosystems-lockfiles), measured for mise) and writes
its own only for the gaps. All of this is now ruled — ten rulings in the
[Decision Ledger](#decision-ledger), the installer-capture design
([§6.3](#63-installers-that-just-do-whatever-capture-the-install-then-treat-the-capture-as-the-package))
among them.

**The most important section is [§3](#3-four-delivery-classes-and-the-rule-that-falls-out)**: the
four classes are the frame everything else hangs on. [§4](#4-how-two-jails-diverge-today-measured)
is the evidence; [§6](#6-the-general-seam-one-ledger-many-resolvers) is the answer to *"what happens
when another mechanism comes along we can't control?"*

**Scope note — this is a UNIFORMITY doc, not a security doc.** The trust half is already ruled:
[`trust-paths.md`](trust-paths.md) OQ-TP5 killed silent evergreen updates, OQ-TP6 made a refused
contribution a refused launch, and OQ-TP2 ruled that regenerated agent context needs no gate.
Nothing here re-argues any of that, and nothing here claims supply-chain integrity — a receipt that
records *what you got* is not an attestation that it is *what someone published*.
[§8](#8-where-this-sits-against-the-sibling-docs) says exactly which sibling questions this
supersedes.

**Reads with:** [`trust-paths.md`](trust-paths.md) (the trust half, and the two questions this doc
supersedes), [`image-staging-vs-baking.md`](image-staging-vs-baking.md) (the measured cost of
baking — every "why don't we just bake it" answer is priced there),
[`pack-system.md`](pack-system.md) (the `program` contribution kind),
[`program-kind-defects.md`](program-kind-defects.md) (the earlier pass at this mechanism, whose
staging-removal half shipped and whose install-removal half did not),
[`storage-and-config.md`](storage-and-config.md) (which directory has which lifetime).

---

## 1. The verdict, and five principles

**Two jails are not the same today, and a per-workspace lockfile over the pack set cannot make them
the same.** Not because pinning is wrong, but because pinning answers one of three questions and the
other two are unasked. Build in this order: **record what happened → make removal real → then
decide what obeys a pin.** The first step is also the cheapest and it is the one that makes the pin
question answerable with evidence instead of argument.

Five principles, numbered so later sections and sibling docs can cite them.

**P1. Uniformity is three properties, not one.**

| Property | Question it answers | Have it today? |
| :--- | :--- | :--- |
| **Determinism** | same declaration → same bytes | only for baked + regenerated content |
| **Removal** | dropping a declaration removes the bytes | only for LSP servers and staged pack *trees* |
| **Scope agreement** | the record and the bytes have the same reach and lifetime | **no** — three different scopes per mechanism ([§4.4](#44-the-scope-mismatch-the-maintainers-premise-corrected)) |

A lockfile buys determinism, on the mechanisms it can reach. It is a third of the answer.

**P2. Cadence decides venue.** Bake what moves on *yolo's own* release cadence; deliver at launch
what moves on someone else's. This is [`image-staging-vs-baking.md`](image-staging-vs-baking.md)
§1's stratification finding restated as a placement rule, and it is why "bake everything" is not
the answer to a class whose whole problem is that it moves when we do not
([§5.1](#51-a1--bake-everything-into-the-image)).

**P3. Every delivery must leave a receipt.** Asked-for name, resolver, resolved identity, landing
path, time. **MEASURED:** the *only* content-addressed record of a resolution anywhere in the system
is the image load sentinel (`BUILD_DIR/last-load-<runtime>`). No mechanism in the
installed-and-kept class writes one, which is why *"what did this jail actually get?"* is currently
unanswerable.

**P4. The unit is a delegated resolution, not an npm package.** Anywhere yolo declares a NAME and a
third party decides the bytes: `program via npm`, `program via installer`, the LSP recipes, mise
tools, claude plugins, an `npx -y` MCP argv. npm is simply the first one anyone looked at.
**Anything scoped to `via: npm` covers one of six delegated resolutions, every one of them in the
installed-and-kept row** ([§3](#3-four-delivery-classes-and-the-rule-that-falls-out)).

**P5. Regenerated beats installed.** Content re-derived every launch cannot diverge; content
installed once and kept always can. Where a mechanism can be moved into the regenerated class, that
beats pinning it in place — and where it cannot, the *reconciliation* half is still available
cheaply ([§5.4](#54-a4--regenerate-or-reconcile-every-launch)).

---

## 2. What "the same jail" would have to mean

The question has three candidate answers and they impose different designs. **Ruled**
([OQ-PD2](#decision-ledger)): (a) and (b) ship first, through the user half of
[§5.6](#56-a6--borrow-the-ecosystems-lockfiles); (c) is in scope for the project toolchain — a
repo-committed native lock makes it nearly free — and stays out of scope for user tools, which no
repo should pin.

| Reading | "Two jails are the same when…" | What it demands |
| :--- | :--- | :--- |
| **(a) same machine** | two workspaces on one machine, one user config, get the same tools | a machine-scope record; removal; no cross-workspace mutation |
| **(b) same workspace over time** | this workspace next month is this workspace today, unless someone asked | a per-realization record + an explicit update act |
| **(c) same declaration anywhere** | my jail and a colleague's, from the same repo and config, match | a record **committed to the repo**, plus resolvers that can obey it offline |

**Today all three fail, and (a) fails hardest** — the case nobody would expect to fail, because it
is one machine and one config. [§4](#4-how-two-jails-diverge-today-measured) is the evidence.

---

## 3. Four delivery classes, and the rule that falls out

| Class | What it is | Re-derived? | Record of what it resolved | Uniform? |
| :--- | :--- | :--- | :--- | :--- |
| **Baked** | nixpkgs (96.75 % of the closure), the shipped Go binaries (`flake.nix`'s `shippedBinaries`), `mise` itself | per image build, hermetic (`-mod=vendor`, committed `vendor/`) | `flake.lock` + the load sentinel | ✅ **provably** |
| **Regenerated** | pack trees (`_official/` cleared wholesale), skills, briefings, config surfaces, shims, launchers | **every launch** | none needed | ✅ given the binary + pack commit |
| **Installed-and-kept** | `program via npm`, `program via installer`, LSP servers, mise tools, claude plugins, `npx -y` MCP packages | **never** | **none** — one partial exception ([§4.3](#43-history-a-jail-is-the-union-of-every-pack-ever-selected-not-the-current-pack-set)) | ❌ **all divergence lives here** |
| **Mounted** | `/workspace`, `/home/agent`, `/mise`, `/ctx/*` | n/a | n/a | inherits its source |

**The rule that falls out — and it is the whole design in one line: make the third class behave like
the first or the second.** Either it is re-derived every launch (class 2), or it is a *recorded
resolution* materialized from a content-addressed cache (class 1). A mechanism that stays
"install once, forget, never remove" will diverge no matter what is written in a lockfile, because
nothing ever compares the lockfile to the disk.

> [!NOTE]
> **Class 2 is the existence proof that regeneration works, and it was ruled from the other
> direction.** [`trust-paths.md`](trust-paths.md) OQ-TP2 decided that skills and briefing prose need
> no gate because the commit pin closes over them. The uniformity reading is different and reaches
> the same place: they need no pin because they are **rebuilt**, not installed. **MEASURED:** over
> 1,600 per-workspace staging dirs exist under `~/.local/share/yolo-jail/agents/`, and every one of
> them is cleared and re-rendered on its next launch.

---

## 4. How two jails diverge today (measured)

### 4.1 Freeze: an agent CLI is whatever `@latest` meant the day that workspace first ran it

The npm launcher's cold branch is unconditional and unversioned — `if [ ! -x "$REAL_BIN" ]` →
`_do_install || true` with `SPEC=<name>@latest` (`internal/entrypoint/shims.go`, `npmInstallSpec` in
`internal/entrypoint/npmspec.go:58-64`: *"an unversioned declaration still resolves to `@latest`"*).
The comment on that branch is explicit that the no-evergreen ruling deliberately does not touch it:
*"the FIRST install is not a poll … without this branch a fresh jail would simply have no agent CLI
at all."*

**MEASURED**, one workspace's `~/.npm-global` on this machine — byte-identical across six days of
launches, which is the freeze holding under observation: in steady state nothing *can* move an
agent CLI, because the launcher that would notice is shadowed
([§5.2](#52-a2--lazy-install-from-a-launcher-the-status-quo)) and `yolo pack update` was not run.

| Package | Version on disk | Symlinked |
| :--- | :--- | :--- |
| `@github/copilot` | 1.0.48 | 2026-05-15 |
| `@openai/codex` | 0.145.0 | 2026-07-25 |
| `@earendil-works/pi-coding-agent` | 0.82.1 | 2026-07-25 |
| `pyright` · `typescript` · `typescript-language-server` | 1.1.409 · 6.0.3 · 5.1.3 | 2026-05-01 |
| `chrome-devtools-mcp` | 0.23.0 | 2026-05-01 |

**Three months of spread inside a single home**, and the mechanism generalises directly: a workspace
created today resolves `@latest` today. That is the concrete answer to *"do two jails a month apart
get the same bytes?"* — no, and nothing anywhere notices.

### 4.2 Drift: mise is machine-global, evergreen every launch, and repoints aliases in place

`setupScript` runs `mise install --quiet && mise upgrade --yes` **unconditionally on every launch**
(`internal/cli/run/command.go:15-25`, a frozen-contract constant). The store is a single bind —
`-v <miseStore>:/mise` (`internal/cli/run/assemble_parts.go:156-161`), backed by
`paths.GlobalMise()` = `~/.local/share/yolo-jail/mise` (`internal/paths/paths.go:323`) — **one store
for every workspace and every nesting depth.**

> [!IMPORTANT]
> **The drift is observed, not hypothesized.** Between two measurements five days apart, `go/1.26`
> — the alias this repo's own `mise.toml` resolves through — repointed from `1.26.6` to `1.26.7`
> (2026-08-20, per the symlink mtime), and `1.26.6` **left the disk entirely**. Two consequences,
> the second worse than the first:
>
> 1. Machine-global state that every workspace resolves through changed, with **no record anywhere**
>    of what the name used to mean.
> 2. The PATH entry measured before the repoint, `/mise/installs/go/1.26.6/bin`, now names a
>    directory that does not exist. **INFERRED, not held-alive to confirm:** a jail launched before
>    the repoint and still running therefore carries a **dangling** toolchain entry on its `PATH` —
>    broken, not merely stale. (The launch path prunes dangling store *symlinks* under
>    `YOLO_STORE_PRUNE_OK`, [`command.go:16-20`](../../internal/cli/run/command.go); it does nothing
>    for a `PATH` already exported into a live container.)
>
> So a jail's toolchain can change while it is running — and can also **disappear** while it is
> running: the mutable-pointer half of `installs/` is shared, and so is the garbage collection of
> what a pointer used to name. Nothing says which fossils survive, or for how long.

**MEASURED**, `/mise/installs`, 2026-08-24:

```
go/1.26     -> ./1.26.7      (repointed 2026-08-20; 1.26.2 and 1.24.13 still on disk, 1.26.6 GONE)
node/24     -> ./24.19.0     node/latest -> ./24.19.0   (22.23.2 and 22.20.0 still on disk)
…staticcheck/latest -> ./2026.1                         (0.7.0 still on disk, from 2026-07-29)
```

Those are **symlinks, repointed by an upgrade** — old versions linger as fossils while the alias
moves, until something garbage-collects them. And the aliases are not merely internal bookkeeping:
**MEASURED**, this jail's live `PATH` carries six `/mise/installs/*` entries and **five resolve
through a fuzzy alias** (`node/24`, `just/latest`, `staticcheck/latest`, `neovim/nightly`,
`pipx-swarf/latest`). The one version-exact entry, `go/1.26.7/bin`, is no safer — exactness is what
the deleted `1.26.6` had.

**MEASURED**: no `mise.lock` exists anywhere in the tree and nothing enables mise's lockfile mode
(`rg -n 'lockfile|mise.lock|MISE_LOCK'` returns only pack-lockfile hits). The baked mise (2026.7.17)
**does** ship a `mise lock` command and a lockfile mode (`MISE_LOCKFILE`) — unused today, and
[§5.6](#56-a6--borrow-the-ecosystems-lockfiles) measures what it would give us: exact versions,
per-platform checksums, and a lock that *governs* resolution against the shared store. **NOT
MEASURED:** whether the lockfile format survives a mise upgrade.

> [!IMPORTANT]
> **mise is not a hypothetical "second uncontrolled mechanism" — it is the one that already
> arrived, and it is worse than npm on every axis**: more registries, evergreen per *launch* rather
> than per hour, machine-global rather than per workspace, no lockfile, and no `update` verb of ours
> to hang a rule on. It also carries at least three resolvers beyond nixpkgs — **MEASURED** in
> `/mise/installs`: `go-honnef-co-go-tools-cmd-staticcheck` (Go module proxy),
> `pipx-mypy` and `pipx-swarf` (PyPI). Any design scoped to `program via npm` misses all of them.
>
> [OQ-PD3](#decision-ledger) is now **ruled** on exactly this: the per-launch `mise upgrade --yes`
> was a stopgap, no-evergreen is a principle rather than an npm fix, and whatever pins mise ships as
> part of the general seam ([§6](#6-the-general-seam-one-ledger-many-resolvers)) — not as a
> standalone lockfile flip.

#### 4.2.1 Exactly what mise shares — and the sharing is inverted

*"Aren't mise and similar only supposed to share CAS-type caches?" That is the right expectation,
and **the reality is its exact inverse**: yolo shares the mutable part and isolates the cacheable
one.*

**What yolo sets** (READ FROM CODE, [`assemble.go:603-604`](../../internal/cli/run/assemble.go),
and confirmed in this jail's live environment):

| Variable | Points at | Scope | Content |
| :--- | :--- | :--- | :--- |
| `MISE_DATA_DIR` | `/mise` → `<GlobalStorage>/mise` (`paths.GlobalMise`) | **machine-wide**, one bind for every workspace and every nesting depth | installs, shims, downloads |
| `MISE_CACHE_DIR` | `/tmp/mise-cache` | **per container, ephemeral** | mise's own metadata cache |

**So the download/metadata cache — the one thing that is safely shareable, because it is keyed by
content and never resolved through — is thrown away with the container. And the install tree, which
contains mutable pointers, is the thing every workspace shares.**

**What lives under the shared `/mise`:**

```
/mise/installs/       ← versioned dirs AND mutable alias symlinks  (the problem)
/mise/downloads/      ← fetched archives — genuinely cache-shaped
/mise/shims/          ← generated shims, regenerated on change
/mise/conda-packages/ /mise/migrations/
```

**`installs/` is two different things wearing one directory.** Measured, abridged and annotated:

```console
$ ls -l /mise/installs/node/
22        -> ./22.23.2          # alias — MUTABLE
22.20     -> ./22.20.0          # alias — MUTABLE
22.20.0/                        # content — immutable once written
22.23     -> ./22.23.2
22.23.2/                        # content
24        -> ./24.19.0
latest    -> ./24.19.0          # alias — MUTABLE
lts       -> ./24.19.0
```

The **versioned directories are effectively content-addressed**: `22.20.0/` means one thing forever,
and two workspaces sharing it is exactly the win a CAS gives you. The **aliases beside them are
not** — they are mutable pointers, shared machine-wide, and `mise upgrade --yes` repoints them. The
`go/1.26` repoint above is the observed proof — and it qualifies the *content* half too: a
versioned directory is immutable only until shared garbage collection takes it, as whatever pruned
`1.26.6` did out from under any name that still resolved there.

> [!WARNING]
> **This repo resolves through exactly those mutable aliases.** `/workspace/mise.toml` declares
> `node = "24"`, `go = "1.26"`, `just = "latest"` — three fuzzy pins, each of which is a symlink any
> other workspace's launch can move. `mise install && mise upgrade --yes` runs unconditionally on
> every launch (`internal/cli/run/command.go:21-22`), so the mutation is not rare: it is once per
> launch, per workspace, against shared state. The failure this produces is the nastiest kind — a
> jail's toolchain changes (or dangles) **while it is running**, because a *different* workspace's
> launch moved an alias the running jail resolves through. Nothing in the running jail was touched,
> and nothing recorded that anything changed.

**Why this matters for the design rather than being a mise complaint.** It splits the fix cleanly,
and the split is the same one §3 draws for every delivery class:

- the **content** half is already right — sharing immutable versioned installs across workspaces is
  the download-once-reuse-everywhere property this design wants everywhere else;
- the **resolution** half is what is missing — a name (`24`, `latest`) resolved per launch against
  shared mutable state, with no receipt of what it resolved to.

So mise does not need a different mechanism from the one this document proposes. It needs the same
one: **resolve once, record the exact version, and materialize from the record** — the aliases become
an implementation detail nobody resolves through, and the shared install tree stays shared, which is
where its value was all along. That is now the ruled position, not just this doc's argument:
[OQ-PD3](#decision-ledger) decided mise's pin is part of the general seam.

### 4.3 History: a jail is the union of every pack ever selected, not the current pack set

**MEASURED, 2026-08-24:** this jail's user config selects five agent packs (`claude`, `pi`, `codex`,
`agy`, `opencode`) plus three `file://` packs, and `~/.yolo-launchers/` holds a launcher for each of
the five (plus `pnpm`, a package-manager launcher). Most of what is installed is therefore
*legitimately* present. **What remains is the clean
form of the evidence — the residents no config line can explain:**

| Installed | Selected by a pack? | Has a launcher? |
| :--- | :--- | :--- |
| `@github/copilot` 1.0.48 (linked 2026-05-15) | ❌ **no** — `packs/copilot` exists but is not selected | ❌ no |
| `fzf` 0.5.2 (npm, installed 2026-08-02) | ❌ **no** — from a test pack deleted weeks ago | ❌ no |
| `agy`, 189 MB in `~/.local/bin` (2026-07-27) | ✅ yes, today | ✅ yes |

Two programs are installed in this jail that **nothing in the current configuration asks for**, and
the system has no way to say so. The `agy` row is the interesting one for the *opposite* reason: it
left the config and later re-entered it, so its 189 MB stopped being an orphan **by luck rather than
by any act** — which is exactly why "the jail is its config's history" is the accurate description
and "the jail is its config" is not.

The `fzf` orphan also shows the PATH rule doing its job, which is worth separating from the defect:
the image bakes `fzf 0.74.2` at `/bin/fzf` (a nixpkgs symlink) while the orphaned **npm** package is
a wholly unrelated `fzf 0.5.2`. Nobody ever reaches the orphan, because `~/.yolo-launchers` is
ordered last and there is no launcher for it anyway. **Unreachable is not removed** — it is
occupying a per-workspace home with no record of why.

Dropping a pack removes its **launcher** (`resetAnchorDir` clears the anchor dir contents-only every
boot, `internal/entrypoint/shims.go:24`) and now removes its **staged tree** (`packstage` rule 3,
the fix that closed [`program-kind-defects.md`](program-kind-defects.md) 11.3). It has never removed
the **installed program**. **MEASURED:** the only `npm uninstall -g` in the entire tree is in the LSP
bootstrap (`internal/entrypoint/shell.go:298`), keyed on the `~/.yolo-installed-lsps` sentinel
(`shell.go:245`).

Two consequences worth stating separately:

- **Two jails whose current config is byte-identical still differ by their config HISTORY.** Any
  claim of the form *"the pack set plus a lockfile makes jails uniform"* is false until removal is a
  real operation. **Ruled** ([OQ-PD4](#decision-ledger)): the orphans become an informational
  catalog at boot; removal happens only on an explicit act; autoprune exists as an option for those
  who want it, **default off**.
- **The LSP sentinel is the only install/uninstall reconciliation loop in the system, and it is one
  field short of being a receipt** — it stores `kind:identifier` lines (`npm:pyright`,
  `go:golang.org/x/tools/gopls@latest`; the format at `internal/entrypoint/shell.go:242-244`, the
  recipes at `internal/cli/run/lsp.go:17-19`) and never what the install *resolved to*. That makes
  it the cheapest available prototype for P3.

### 4.4 The scope mismatch: the maintainer's premise, corrected

*"This is user level, but the realization is workspace level"* is half right, and the wrong half is
the one that decides the design. **READ FROM CODE**, `internal/cli/run/assemble_parts.go:108-161`
and `internal/cli/run/assemble.go:603-604`:

| Thing | Scope | Backing |
| :--- | :--- | :--- |
| the declaration (`packs`) | **user** | `~/.config/yolo-jail/config.jsonc` (`internal/config/packs.go:192`; the file header there rules workspace scope inexpressible) |
| npm programs, installer programs, Go bins | **per workspace** | `<ws>/.yolo/home/{npm-global,local,go}` (`assemble_parts.go:109-111`) |
| **the npm download cache** | **machine-global** | `NPM_CONFIG_CACHE=$HOME/.cache/npm` (`shims.go:349`); `~/.cache` is `paths.GlobalCache()` (`assemble_parts.go:120`) |
| **the update stamps and spec records** | **machine-global** | `~/.cache/yolo-agent-stamps` (`shims.go:190`) — beside a per-workspace `REAL_BIN` |
| **mise tools** | **machine-global, mutated in place** | `/mise` (`assemble_parts.go:156-161`) |
| the base home | machine-global, **read-only** | `paths.GlobalHome()` mounted `:ro` (`assemble_parts.go:108`) |
| the image | machine-global by **name**, per-config by **content** | one `localhost/yolo-jail:latest` tag; `packages:` is workspace-settable |
| the pack store / the pack lockfile | machine-global store / **user-scope** lockfile | `paths.PacksDir()` (`paths.go:359`) / `~/.config/yolo-jail/packs.lock.json` |
| pack trees, skills, surfaces | per workspace, **derived** | cleared and re-staged every launch |

**So the pin the premise imagines, the bytes it would govern, and the cache and stamps that mediate
them are three different lifetimes.** One file cannot be all three scopes — which
[OQ-PD1](#decision-ledger) resolved by making the record follow the declaration's scope, with the
bytes content-addressed so their scope stops mattering ([§5.6](#56-a6--borrow-the-ecosystems-lockfiles)).

The stamp/spec split is the shape of the bug this causes, already in production:

- **MEASURED:** `~/.cache/yolo-agent-stamps/` holds `claude.stamp` (2026-08-05) and `fzf.stamp`
  (2026-08-02) and **no `.spec` file beside either** — and both stamps have sat unmoved through
  **nineteen days of launches**, while the launchers themselves are regenerated every boot. The
  stamps survive boots and are shared by every workspace on the
  machine, so the template's `elif [ ! -f "$STAMP" ]` branch, commented *"first run since jail
  boot"*, in fact fires at most once per **machine** per binary. The unmoved stamps are also what
  settled [OQ-PD8](#decision-ledger): the poll touches its stamp on **every** run, hit or miss
  ([`shims.go:429`](../../internal/entrypoint/shims.go)), so a stamp that has not moved in nineteen
  days is a poll that has not run in nineteen days — the launcher's informational channel is
  unreachable in steady state, and the "newer version available" report moves to the boot catalog
  and the update verb. (Clearing one stamp and launching remains a cheap end-to-end confirmation of
  the shadowing.)
- **READ FROM CODE, NOT MEASURED:** `_do_install` ends with `touch "$STAMP"`
  (`shims.go:379-402`), so a **cold install in workspace B writes the stamp workspace A throttles
  on** — the most likely explanation for a `claude.stamp` dated two weeks after the binary it
  describes.
- **READ FROM CODE, NOT MEASURED:** if two workspaces ever carried *different* pinned specs for one
  bin, the `PINNED` branch compares `$SPEC` against a shared `SPEC_FILE`
  (`shims.go:510-517`), so each would reinstall on every launch, forever.

### 4.5 The record that does not exist

**MEASURED:** `~/.config/yolo-jail/packs.lock.json` is `{"schema": 1, "packs": {}}` on a machine
whose homes hold four npm-installed agent CLIs. That is not a missing
*field* — it is a missing *row*: `LockEntry` (`internal/packsrc/lock.go:33-42`) exists per
**fetched** pack, `Commit` is documented *"empty for a local pack"*, and every npm-declaring pack is
**embedded**. The file is also, per [`trust-paths.md`](trust-paths.md) §1, display-only: its
`Commit`/`Ref` readers all print and none gate.

`/workspace/.yolo/boot.log` records the yolo version, runtime, loopback disposition and surface
renders; `/workspace/.yolo/startup.log` is truncated every launch. Neither records an installed
version. **There is nowhere in this system to look up what a jail got.**

---

## 5. Alternatives, each with a verdict

### 5.1 A1 — Bake everything into the image

*"I'm not entirely sure why we don't bake all these things."* Here is the honest bill, and every
number is [`image-staging-vs-baking.md`](image-staging-vs-baking.md)'s.

**What it gets right, and it is not small:** baking is the *only* class in the inventory that is
provably uniform. The input is `flake.lock`, not a registry; the build is hermetic; the resolution is
recorded. If uniformity were the only axis, this would win outright.

**Rebuild cadence — the maintainer's own objection, priced.** §1.1 there: **60.5 %** of a 200-commit
window already forces an image rebuild, and §1.4: the two most recently loaded images differ by
**one package, a 180.2 KiB delta**, for which the pipeline wrote a fresh **3.28 GiB** tar and ran a
full `podman load`. §1.5: `packages:` is workspace-settable and there is one `:latest` tag, so two
workspaces with different lists **reload the whole image on every alternation, forever**. Adding
agent CLIs to that set means every CLI bump is an image rebuild and a multi-gigabyte reload for
everyone on the machine — and it couples an agent vendor's release cadence to yolo's, which is
exactly the cost [`trust-paths.md`](trust-paths.md) OQ-TP4 option (a) named.

**The download problem inverts rather than disappears.** **MEASURED:** the npm download cache is
already machine-global and holds **672 MB** (`~/.cache/npm/_cacache`, plus an `_npx` tree — so
`npx -y` MCP packages cache there too), up 27 MB in the six days it was under observation. It only
grows; nothing prunes it, which is R4. A second workspace installing a version this machine already
fetched pays essentially no download. Baking moves that cost *into every image build*, where it is
paid per config and per rebuild instead of once per machine.

**Two traps specific to baking programs.** (i) PATH inversion: `~/.yolo-launchers` is ordered **last,
after `/bin`**, precisely so a pack's `program fzf` cannot shadow the image's `/bin/fzf`
([`pack-system.md`](pack-system.md) §`program`). Baking `claude` would make a pack's declared
`claude` unreachable, silently — and `image-staging`'s R2 warns that a half-migration (baked *and*
staged) silently runs the baked one. (ii) Expressibility: a vendor installer script with its own
updater is not a nixpkgs package, so baking it is a packaging project per tool — the very
"mechanism we can't control" problem, relocated.

> **Verdict: rejected as the general answer; kept for what is already there.** Bake what is stable
> and shared (P2). Anything moving on a third party's cadence does not belong in a 3.28 GiB artifact
> delivered whole.

### 5.2 A2 — Lazy install from a launcher (the status quo)

**Its virtues are real and any replacement must keep them:** you pay nothing for a tool you never
invoke; no image rebuild; per-workspace isolation; and it works on `macos-user`, which bakes no image
at all.

**Its contract is what fails.** [§4.1](#41-freeze-an-agent-cli-is-whatever-latest-meant-the-day-that-workspace-first-ran-it)
(freeze), [§4.3](#43-history-a-jail-is-the-union-of-every-pack-ever-selected-not-the-current-pack-set) (no removal),
[§4.5](#45-the-record-that-does-not-exist) (no record), plus a reporting channel that may never run:
**MEASURED**, `type -a claude` resolves `/home/agent/.local/bin/claude` **before**
`/home/agent/.yolo-launchers/claude`, and both install destinations
(`$NPM_CONFIG_PREFIX/bin`, `~/.local/bin`) precede the launcher dir in the live PATH. So the
launcher runs **once per (workspace × binary)** and is shadowed forever after; the only caller that
runs one afterwards is `yolo pack update`, by absolute path
(`internal/cli/packupdate.go:108-116`, resolved to an absolute path at `:145-146`).

> **Verdict: keep the mechanism, reject its current contract.** Lazy install is the right shape for
> a class-3 tool. What it lacks is a receipt, a reconcile and a removal — which is A3.

### 5.3 A3 — Pin-and-cache: declare → resolve → record → materialize (proposed)

A declaration names a tool. An explicit **update act** resolves it and writes a **receipt**. Every
subsequent launch **materializes** from a machine-global, content-addressed artifact cache and
**reconciles** offline against the receipt. Nothing resolves on a timer, and nothing resolves at
boot.

**Three quarters of it already exists**, which is the argument for it:

| Piece | Already built as |
| :--- | :--- |
| the machine-global artifact cache | `NPM_CONFIG_CACHE=$HOME/.cache/npm` — 672 MB and growing, shared by every workspace |
| the explicit update act | `yolo pack update` (npm-only and jail-only today, `packupdate.go:95-103`) |
| the reconcile loop | the LSP sentinel's install **and uninstall** (`shell.go:245-312`) |
| the receipt's file format | `packs.lock.json` — schema-versioned, already the place a resolution would go |

**What it costs.** A new artifact class and its retention policy — and this repo's track record there
is 404 GiB of image tars accrued in 24 days (`image-staging` §1.6), plus **MEASURED** just over 1 GB
of claude builds (4 versions, 2.1.165–2.1.220) sitting in one workspace's
`~/.local/share/claude/versions`. It also has a genuine cold-start hole: the first resolve has no
receipt to obey, so *something* resolves, and the honest answer is that the update act does it in
the open rather than a launcher doing it silently.

**It is also the only option that answers the download question directly**: the installer class is
the one that downloads per workspace today (`~/.local` is a per-workspace bind), which is why claude
and `agy` are re-fetched per workspace while npm packages are not. Moving that class's artifacts into
the machine-global cache is a smaller change than baking and fixes the same complaint.

> **Verdict: adopt, in the order record → reconcile → remove → obey.** The record is cheap, is
> useful on its own (it makes divergence *visible* for the first time), and is what turns
> "is pinning worth it?" from an argument into a measurement. Where an ecosystem already keeps the
> record, [A6](#56-a6--borrow-the-ecosystems-lockfiles) is how this ships — yolo writes its own
> receipt only for the gaps.

### 5.4 A4 — Regenerate (or reconcile) every launch

Make class 3 behave like class 2: reinstall from the receipt on every boot, the way `_official/` is
cleared and re-staged.

> **Verdict: rejected as literal reinstall; adopted as reconcile.** A launch must not depend on a
> registry being reachable, and an install is not free. But the *comparison* is free and offline —
> "what the receipt says vs. what is on disk" — and the LSP sentinel already proves the loop can be
> written. Reconcile reports; it does not install.

### 5.5 A5 — Do nothing, and say so

Accept that jails are not uniform, and document it.

> **Verdict: rejected, but its honest half survives and belongs in the design.** No mechanism
> covers the unmanaged tier as it stands ([§6.1](#61-three-tiers-of-control--the-answer-to-what-about-a-mechanism-we-cant-control)) —
> [§6.3](#63-installers-that-just-do-whatever-capture-the-install-then-treat-the-capture-as-the-package)
> proposes moving both of today's members out of it — so whatever ships must **enumerate what it
> does not manage** rather than implying coverage it lacks. A seam that silently claimed a vendor
> self-updater would be worse than one that names it as unmanaged.

### 5.6 A6 — Borrow the ecosystem's lockfiles

*The maintainer's reframe: yolo does not have to do this on its own. It is a tool that manages a
dev environment, so writing a config in the repo is not crazy — and it means help with resolving,
prebuilding and materializing.* Worked out, that observation is bigger than a venue choice.

**Class 1 is already the existence proof, inside yolo.** `flake.lock` is an ecosystem-native,
repo-committed lockfile; a third party resolves it; the bytes live in a machine-global
content-addressed store; materialization is on demand and offline once the store is warm. §3's rule
was "make class 3 behave like class 1" — and the cheapest implementation is not to rebuild class
1's machinery in a yolo ledger, but to **adopt each ecosystem's own `flake.lock`-equivalent** and
let yolo orchestrate.

**MEASURED, 2026-08-24, against a scratch copy of this repo's `mise.toml` — the help is larger than
expected:**

- `mise lock` resolved all four declared tools — including the `go:` backend
  (staticcheck via the Go module proxy) — to exact versions with a **sha256 checksum and download
  URL per platform**, seven platforms, one file. The receipt, the pin, and a content-address, from
  a binary the image already bakes. (The env var is not load-bearing: mise honors and maintains
  an existing lock by default — measured.)
- **The lock governs resolution rather than merely recording it.** With the lock repointed to
  `go 1.26.2` while `mise.toml` still declared `go = "1.26"`, `mise ls --current` resolved 1.26.2
  and `mise exec go -- go version` ran go1.26.2 — materialized from the shared `/mise` store, no
  download, no alias consulted. That is [§4.2.1](#421-exactly-what-mise-shares--and-the-sharing-is-inverted)'s
  ending — *the aliases become an implementation detail nobody resolves through* — delivered by
  mise itself, driven from a repo-committed file.

So for lockfile-capable ecosystems, four of §6's six verbs come free: *declare* is the config that
already exists, *resolve* + *record* are their lock command, *materialize* is their
install-from-lock. What yolo writes is orchestration: the launch runs install-from-lock instead of
`install && upgrade --yes` (which is exactly [OQ-PD3](#decision-ledger)'s ruling landing), the
update act runs the ecosystem's update and leaves a **reviewable repo diff** — a dependency bump
like any other, which bots can own — and the reconcile reads their lock.

**The record follows the declaration's scope — ruled ([OQ-PD1](#decision-ledger), shape (d)).** It
untangles §4.4's three-scope knot without inventing anything:

| Declaration | Scope | Its lock | The bytes |
| :--- | :--- | :--- | :--- |
| `mise.toml` (project toolchain), `flake.nix` | repo | `mise.lock`, `flake.lock` — **committed** | machine-global CAS (`/mise` versioned dirs, `/nix/store`) |
| `packs` (agent CLIs, installer programs) | user | a user-scope receipt — `packs.lock.json` is already the file | machine-global, version-addressed cache |

A repo must **not** pin the user half: pack selection is ruled user-level
(`internal/config/packs.go:20-24`), agent vendors release weekly (churn PRs in every repo that
pinned them), and a repo lock over a user declaration would recreate §4.4's scope mismatch in the
other direction. The split is also [OQ-PD2](#decision-ledger)'s ruling: reading (c) — colleague
parity — is in scope for the project toolchain and stays out for user tools. Scope agreement (P1)
is achieved differently than by co-locating receipt and bytes: the record sits with its
declaration, and the bytes are **immutable and content-keyed**, so their scope stops mattering —
the nix model, generalised.

**What yolo still builds, honestly:**

- the **installer class** (claude, `agy`) has no native lockfile — A3's yolo-written receipt
  survives there (user scope), and the version-addressed artifact cache for that class is yolo's to
  lay out ([§6.3](#63-installers-that-just-do-whatever-capture-the-install-then-treat-the-capture-as-the-package)
  proposes how: capture the install once, then the capture is the package);
- **npm layout**: registries resolve and fetch (and the machine-global cacache already dedups
  downloads), but `npm install -g` keeps one version per prefix — yolo must choose
  version-addressed install prefixes. Mechanical, not conceptual;
- the **report**: *"what did this jail get"* = read the native locks, plus yolo's gap receipts,
  plus the enumeration of the unmanaged tier (§6.1);
- the **cold start** is unchanged from A3: no lock yet → the first update act creates it, in the
  open.

> [!NOTE]
> **The trust surface narrows rather than widens.** A cloned repo's `mise.toml` already drives
> installs into the shared `/mise` at every launch today; under a lock, nothing resolves through
> the shared mutable aliases and a checksum bounds what a URL may deliver. Whether a lock should
> *gate* anything stays [`trust-paths.md`](trust-paths.md)'s question, not this doc's.

Two costs A3 did not have: the guarantee becomes a function of each ecosystem's lockfile quality
(R7, generalised — **NOT MEASURED**: whether `mise.lock`'s format survives mise's own upgrades, and
full-launch behaviour under `MISE_LOCKFILE`); and a repo-committed lock imports the repo's cadence
(R9) — bumps become PRs, and repo-less or ephemeral workspaces need the user-scope half as their
fallback.

> **Verdict: adopt, as the preferred realization of A3 wherever a native lockfile exists.** A3's
> yolo-owned receipt survives only for the gaps. This also settled one §6 design decision
> ([OQ-PD5](#decision-ledger)): the ledger is N native records under **one reader**, not one store
> of opaque identities. And whether yolo may add a repo-committed file of its *own* is ruled
> ([OQ-PD9](#decision-ledger)): native formats whenever one exists; yolo's own only when the work
> demonstrates the need, never preemptively.

---

## 6. The general seam: one ledger, many resolvers

**The unit is a delegated resolution** (P4): a tuple of *(declaration, resolver, resolved identity,
landing path, scope, time)*. The shape is **ruled** ([OQ-PD5](#decision-ledger)): **N native records
under one reader** — each resolver keeps its ecosystem's own lockfile or receipt and is the only
thing that parses it; what is common is the READER (the report that renders every tier) and the
lifecycle below. A pin is inherently ecosystem-flavoured (`pkg@1.2.3`, a git SHA, a URL+hash, a
mise `backend:tool@version`), which is why nothing common may interpret one.

> [!WARNING]
> An earlier draft of this section proposed the inverse — one ledger-as-store holding opaque
> resolved identities. Do not rebuild it:
> [§5.6](#56-a6--borrow-the-ecosystems-lockfiles)'s measurement is what killed it. mise's lockfile
> already *is* the record, checksums included, and a yolo store beside it would be two records with
> one truth.

Two more rulings bound the lifecycle. **The receipt is the pin** ([OQ-PD6](#decision-ledger)): a
declaration may carry a version and is not required to — install obeys the record, so an unpinned
declaration stops being evergreen the day the record exists. And **the record reports before it
gates** ([OQ-PD7](#decision-ledger)): enforcement starts as reporting, on purpose, and a gate comes
only if the reports justify one — the record still names where that gate would live, which is what
keeps it off R1's display-only path without adding a fourth fatal.

The lifecycle each resolver implements — six verbs, and a resolver may legitimately implement only
the first three:

```
declare → resolve (ONLY on an explicit update act) → record
        → materialize (from the shared artifact cache) → reconcile (offline, per boot) → remove
```

### 6.1 Three tiers of control — the answer to "what about a mechanism we can't control?"

A new mechanism does not need a new design; it needs to land in one of three tiers, **loudly**.

| Tier | Meaning | Members today | Uniformity we can promise |
| :--- | :--- | :--- | :--- |
| **Managed** | yolo runs the install | `program via npm`, `program via installer`, LSP recipes | all six verbs — declare, resolve, record, materialize, reconcile, remove |
| **Observed** | a third party installs into a store yolo mounts | **mise**, claude plugins (`installClaudePlugins`, `internal/entrypoint/boot.go:311`) | record and compare; obeying requires the third party's own pin (mise has one; we do not enable it) |
| **Unmanaged** | yolo never sees the resolution | `npx -y <pkg>` in an MCP argv (`internal/cli/config_ref.txt:917,929`), a vendor CLI's self-updater (claude's, `agy`'s) | **none today** — enumerate it; [§6.3](#63-installers-that-just-do-whatever-capture-the-install-then-treat-the-capture-as-the-package) proposes the escape hatch for both members |

The launcher template already states the tier-3 case exactly, about silent updates:
*"a silent change has no act to pin to, so no pin, lockfile field or approval prompt can ever cover
it"* (`shims.go:412`). That sentence is the boundary of the seam, written down before the seam
existed.

**Applied to mise, which is the test case that already exists — and the one case already ruled
([OQ-PD3](#decision-ledger)):** it lands in tier 2, *inside* this seam. We record what `/mise`
resolved to and we compare it; making it *obey* goes through mise's own lockfile — and
[§5.6](#56-a6--borrow-the-ecosystems-lockfiles) measures that the lockfile implements *resolve*,
*record* and *obey* in one mechanism, as an implementation detail of this lifecycle rather than a
standalone setting flipped ahead of it. The per-launch `mise upgrade --yes` was a stopgap and does
not survive the seam: `resolve` runs only on an explicit update act, for every resolver.
Representability is measured, not open: `/mise` is machine-global but mise's lock is per config
root, and §5.6 drove a workspace-local `mise.lock` against the shared store — so a per-workspace
pin is expressible today. For mechanisms with **no** native lock, the record is the user-scope gap
receipt ([OQ-PD1](#decision-ledger)).

### 6.2 Pay the enum tolerance before the next mechanism arrives

The `via` field is a **closed two-value set** — `validateContribution` rejects anything but `npm`
and `installer` (`internal/packdecl/contributes.go:743-745`) — and the two halves of the system
disagree about what to do with a third value:

- `GenerateAgentLaunchers` drops an unrecognised `Kind` with a bare `default: continue` and **no
  message** (`internal/entrypoint/shims.go:219`).
- `DecodeTolerant` skips unknown *kinds* but still validates known ones, so an unknown `via` **value**
  on kind `program` is a validation problem — and the boot path treats any problem as fatal.

**READ FROM CODE, NOT MEASURED:** a pack declaring `via: "uv"` staged for an older baked entrypoint
is therefore a refused boot, not a skipped contribution — the same shape `packdecl`'s own comment
warns about for a third `tier` value. **Whoever adds the third `via` must extend the tolerance
first.** That is a prerequisite of this design, not a consequence of it.

### 6.3 Installers that just do whatever: capture the install, then treat the capture as the package

*The standing question for the worst of the class: what is the plan for installers that just do
whatever — and for things we can't lock at all?*

**The problem, precisely.** A vendor installer is a program that runs arbitrary logic and leaves
arbitrary state. claude's keeps its own versions directory and self-updates (four builds, just over
1 GB, per workspace — §5.3); `agy` is a 189 MB opaque binary. There is nothing to lock because the
vendor never publishes a lockable artifact: **the installer run itself is the resolution.** A
receipt can record what the run left behind, but it cannot make a second run leave the same thing.
This is also the class [OQ-PD9](#decision-ledger) anticipated: no native lock exists to borrow, so
the record here is yolo's own — a machine-local receipt beside the CAS entry, not a repo-committed
lockfile.

**The move: make the resolution observable by containing it.** Run the installer once, in a
throwaway sandbox, against empty state; capture the delta; content-address the capture; from then
on **the capture is the package**:

```
capture (explicit act, network OK):  fresh sandbox → run installer → delta → tar+hash → machine CAS
                                     receipt = (declaration, installer URL, capture hash,
                                                file manifest, platform, time)
materialize (per jail, offline):     unpack/hardlink the capture into the version-addressed path
update:                              a NEW capture, on an explicit act — never in place
remove:                              delete the materialized tree; CAS entry GC'd when unreferenced
```

**The prior art is Arch's.** An AUR `PKGBUILD` runs upstream's opaque payload in a clean chroot
(`makechrootpkg`), and the *output* is an ordinary pacman package with a file manifest — the
package manager never trusts the build script's environment, only its captured product. The pack's
`program via installer` contribution is already the PKGBUILD analogue: a name and a URL. Nix's
fixed-output derivations and `docker commit` are the same shape from other directions.

**The sandbox already exists, and it is yolo's own product.** On the container backends a capture
is an ephemeral jail whose per-workspace home binds start empty: the existing bind surfaces
(`~/.local`, `~/.npm-global`, `~/go` — `assemble_parts.go:109-111`) are natural capture surfaces,
so after the installer runs, **the bind-dir contents ARE the delta** — tar, hash, done. No new
containment machinery; a jail that writes outside its binds is a finding the capture run reports,
and a tool that does so is flagged genuinely unmanageable instead of silently half-captured.

**On `macos-user`, the same two properties come from different machinery** (READ FROM CODE — this
backend's installer pipeline is itself unverified on hardware, per
[`macos-user-nix-and-features.md`](macos-user-nix-and-features.md), so this is design against read
code, not a measurement). That backend has no binds and no ephemeral home: the jail home is one
persistent, machine-constant `/Users/_yolojail` shared by every workspace and every session
(`internal/macosuser/macosuser.go:52-53`, `internal/cli/run/run.go:156-159`) — deliberately, since
the single home *is* its shared-credentials mechanism and splitting it is a refused design point
(`run.go:188-203`). But a capture does not need a bind; it needs a **fresh, enumerable,
kernel-bounded write surface**, and both control points are already this backend's own machinery:
the Seatbelt profile is generated fresh per session (`internal/macosuser/seatbelt.go:47-55` —
`deny file-write*` on `/`, then an explicit allow-list) and the launch is `env -i` under
`sandbox-exec` (`macosuser.go:328-377`). So a capture run sets `HOME` to a fresh staging directory
and carries a narrowed profile in which that directory (plus `/tmp` and `/var/folders` scratch) is
the **only** writable path. The persistent home is denied for the duration — a capture cannot
touch the shared credential store — and an installer that writes elsewhere is refused by the
kernel up front, a sharper escape signal than a container overlay that silently swallows the stray
write. The one genuinely new problem is **relocation**: the staging path is not the final home
path, and installers embed absolute self-references (claude's `~/.local/bin/claude` is an absolute
symlink into its versions directory — MEASURED in this jail), so the manifest must record prefix
references and materialization must rewrite them — the move Homebrew bottles made routine on
exactly this OS — or flag the tool non-relocatable. On the container backends this problem is
absent by construction: the capture home and the materialize home are the same `/home/agent`.

**Why not image layers** — the obvious-looking alternative, rejected three ways: `macos-user` has
no image at all; a layer couples every capture to the image rebuild/reload cadence that §5.1
priced (a 3.28 GiB tar for a 180.2 KiB delta); and a layer is container-shaped while the capture
must serve all three backends. The capture is a plain filesystem artifact — backend-neutral, CAS
resident, materialized the same way everywhere.

**What it buys beyond lockability:**

- the per-workspace refetch cost dies — today claude and `agy` are re-downloaded per workspace
  because `~/.local` is a per-workspace bind (§5.3); a capture is fetched once per machine and
  materialized by unpack;
- **vendor self-updaters are structurally neutered**: the materialized tree comes from the capture,
  so a self-update is either disabled or becomes *drift the reconcile reports* — and an update
  gains an act to pin to, which is the exact property the launcher comment says silent updates lack
  (`shims.go:412`);
- removal becomes safe: deleting a materialized tree loses nothing the CAS doesn't hold.

**Honest limits.** Captures are per-platform (and only for platforms we can run). An installer that
personalizes at install time — machine IDs, license activation — captures per machine, which
defeats the sharing and must be enumerated, not papered over. And the second "can't lock" family,
the `npx -y` argv, does not need capture at all: yolo renders the MCP config surface that contains
the argv, so a receipt can pin `pkg@version` into the render — the self-updater was the truly
unreachable member, and capture is its answer.

Adoption is **ruled** ([OQ-PD10](#decision-ledger)): capture ships as the installer resolver's
implementation of *record* + *materialize*, sequenced last (§10) — an ephemeral jail plus a
snapshot of its fresh home surfaces, a plain filesystem artifact in the machine CAS, never an
image layer. Distributing captures between machines is deliberately out of scope (§7): a capture
made here is used here, and publishing one is a provenance question for
[`trust-paths.md`](trust-paths.md).

---

## 7. What this does NOT cover

- **Security and trust.** [`trust-paths.md`](trust-paths.md) owns it. This doc does not re-argue
  OQ-TP5 (no evergreen npm), does not propose new gates, and **does not claim integrity**: a receipt
  records what you got, not that it is what a publisher signed. No digests of yolo's own, no
  attestation, no SBOM — a borrowed lockfile may carry its ecosystem's checksums (`mise.lock` does,
  [§5.6](#56-a6--borrow-the-ecosystems-lockfiles)), but verifying them is that ecosystem's
  behaviour, not a yolo guarantee.
- **The image cost model.** [`image-staging-vs-baking.md`](image-staging-vs-baking.md) owns rebuild
  frequency, tar sizes, content-addressed tags and the binary cache. §5.1 here *cites* those numbers
  and adds none.
- **Whether `packages:` should stay workspace-scope.** That is `image-staging` OQ-4.
- **Agent context** — skills, briefings, config surfaces. Regenerated every launch (class 2), ruled
  by OQ-TP2, and out of scope here for the same reason.
- **Offline or air-gapped operation as a goal.** Reconcile must work offline; *first* resolve need
  not.
- **Reproducible builds of the agents themselves.** We record what a registry served; we do not
  rebuild it.
- **Distribution of captured installer artifacts** ([§6.3](#63-installers-that-just-do-whatever-capture-the-install-then-treat-the-capture-as-the-package))
  between machines or people. A capture made here is used here; publishing one is a provenance
  question for [`trust-paths.md`](trust-paths.md).
- **`macos-user` package delivery.** It has no image and already resolves `packages:` as a store
  `buildEnv`.
- **A task list.** Sequencing is [§10](#10-what-i-would-build-in-order); ticket granularity lives in
  `docs/tasks/` and `roadmap.md`.

---

## 8. Where this sits against the sibling docs

### 8.1 `trust-paths.md` — what this RETIRES and what it INHERITS

> [!IMPORTANT]
> This document does not edit `trust-paths.md`. The dispositions below are proposed here and are
> applied there by whoever lands this.

- **RETIRES [OQ-TP4](trust-paths.md#-oq-tp4--where-does-an-embedded-packs-npm-version-get-pinned) — *"where does an EMBEDDED pack's npm version get pinned?"*** —
  as posed. All three of its options (manifest / lockfile / user config) are venues *inside the pack
  system*, and the measurement says the question is not the pack system's alone: the identical
  question is live for mise (no pack), the LSP recipes (no pack), and claude plugins (no pack). It is
  superseded by [OQ-PD1](#decision-ledger) (where the receipt lives — now ruled) and
  [OQ-PD5](#decision-ledger) (also ruled). **What survives verbatim and must
  not be re-derived:** TP4's cost analysis of option (a) — pinning in the manifest makes yolo's
  release cadence the ceiling on agent-CLI freshness — is the same objection A1 hits in §5.1, and
  TP4's leaning toward the lockfile is preserved in A3's shape.
- **INHERITS [OQ-TP3](trust-paths.md#-oq-tp3--given-1-is-pinning-worth-building-at-all-and-where-first)'s still-open half** — *is yolo's own embedded pack required to
  pin, and is a fetched pack required or merely permitted?* Restated at wider scope and **ruled** as
  [OQ-PD6](#decision-ledger): once a receipt exists, a declaration need not carry a pin, because
  the receipt is the pin — which is what answers TP3's inherited half.
- **Unchanged by this doc:** OQ-TP5's ruling and OQ-TP6's fatal. §5.2's finding that the launcher is
  PATH-shadowed after first use does not dispute the ruling — it showed the *reporting* half it
  built never executes, which is now settled ([OQ-PD8](#decision-ledger)): the channel moves to the
  boot catalog and the update verb.
  [OQ-PD3](#decision-ledger)'s ruling *extends* OQ-TP5 rather than touching it: no-evergreen is now
  a principle covering every resolver, not an npm fix.

### 8.2 `image-staging-vs-baking.md` — a framing inversion, not a contradiction

No factual claim here contradicts that document, and its numbers are used as authoritative. But the
two rank the same fact oppositely, and a reader moving between them should know:

| Fact | `image-staging` reads it as | this doc reads it as |
| :--- | :--- | :--- |
| Agent CLIs, mise tools and LSP servers are delivered at launch, never baked | a **virtue** — §5's "ALREADY DELIVERED" column, the reason the image is cheap | the **source of all divergence** (§3, §4) |

**Both are true, and P2 reconciles them:** delivery is right for cost and wrong for uniformity, and
the missing piece is not the venue but the receipt. Anyone proposing to bake more must clear
`image-staging` §1's cost model *and* its R2 (a package both baked and staged silently runs the baked
one) before this document's uniformity argument is even reached.

---

## 9. Risks

| # | Risk | Mitigation |
| :--- | :--- | :--- |
| R1 | **A receipt that nothing enforces becomes another display-only field.** The precedent is exact: `LockEntry.Commit` has four readers and all of them print (`trust-paths.md` §1). | **Ruled** ([OQ-PD7](#decision-ledger)): reports only, on purpose — and the record names where a gate would live if the reports ever justify one. |
| R2 | **A ledger in the wrong scope is worse than none.** The stamp/spec split is the live proof: a machine-global record describing a per-workspace install already produces cross-workspace throttle bleed (§4.4). | **Ruled** ([OQ-PD1](#decision-ledger)): the record follows the declaration's scope, and the bytes are content-addressed so their scope stops mattering (§5.6). |
| R3 | **Removal is destructive and the bytes are large.** Uninstalling on pack-drop can delete a 189 MB binary a user still runs from another workspace's muscle memory. | **Ruled** ([OQ-PD4](#decision-ledger)): boot catalogs orphans informationally; removal only on an explicit act; autoprune ships as an option, default off. The LSP sentinel's silent uninstall is the pattern *not* to copy at this size. |
| R4 | **An unbounded artifact cache.** 404 GiB of image tars accrued in 24 days with a hint firing and nothing pruning (`image-staging` §1.6); npm's cacache is at 672 MB and grew 27 MB in six days of observation with nothing pruning; claude keeps 4 versions (just over 1 GB) per workspace. | Retention lands with the cache, not after it, and hangs off `yolo prune`. |
| R5 | **A seam that implies tier-3 coverage is a lie.** A vendor self-updater cannot be recorded at all (until captured, §6.3), and an `npx -y` argv can be *pinned* in the render yolo writes but its resolution is still never recorded. | Enumerate unmanaged mechanisms in the same surface that reports the managed ones (§5.5, §6.1). |
| R6 | **The closed `via` enum turns the next mechanism into a boot refusal** on any pre-`just load` image (§6.2). | Extend the tolerance *before* adding a third value: skip-and-report under `DecodeTolerant`, refuse loudly under `Decode`. |
| R7 | **Uniformity borrowed from a lockfile is a function of that lockfile's quality** — and §5.6 generalises the borrowing to every native lock we adopt. The core is measured (a workspace-local `mise.lock` governs resolution against the shared store); **NOT MEASURED**: format stability across mise's own upgrades, and full-launch behaviour under `MISE_LOCKFILE`. | Treat tier 2 as "record and compare" until the launch path is measured; do not promise obedience we do not own. |
| R8 | **Every measurement is from one machine and one home.** The dates in §4.1 are this jail's history, not a general law. | The *mechanism* (cold-branch `@latest`, shared aliases, absent removal) is read from code and generalises; the dates are illustrative and labelled. |
| R9 | **A repo-committed lock imports the repo's cadence.** Version bumps become PRs — reviewable and bot-automatable, which is the point, and a chore — and repo-less or ephemeral workspaces have no home for the project half. | Bots own the bump PRs; the user-scope half of §5.6's table is the fallback; a missing lock degrades to today's A2 behaviour plus a report line, never a refusal. |

---

## 10. What I would build, in order

**First, write the receipt for the managed tier only.** Asked-for declaration, resolver, resolved
identity, landing path, timestamp — for npm programs, installer programs and LSP servers, the three
places yolo runs the install itself. It changes no behaviour and depends on nothing else here, so
it lands first — and it is what turns the one enforcement decision the rulings deliberately
deferred (OQ-PD7's gate) into a measurement instead of an argument. The mise half of the record
needs no building at all
([§5.6](#56-a6--borrow-the-ecosystems-lockfiles)): enable the lockfile, commit `mise.lock`, and the
launch's `install && upgrade --yes` becomes install-from-lock — [OQ-PD3](#decision-ledger)'s ruling
shipping inside the seam, in the first step rather than the last.

**Second, generalise the LSP sentinel into a reconcile.** It already does install *and* uninstall
against a declared set; what it lacks is the resolved version and a caller for anything but LSP
servers. Reconcile compares, offline, and reports — and it inherits the "newer version available"
channel that [OQ-PD8](#decision-ledger) found dead in the launcher. It installs nothing and removes
nothing.

**Third, implement the ruled scope split** ([OQ-PD1](#decision-ledger),
[OQ-PD2](#decision-ledger)): native locks at the declaration's home, gap receipts at user scope,
bytes content-addressed. Removal and obedience both need the record's reach to match the bytes' —
under the ruling it does, by construction.

**Fourth, make removal real** in [OQ-PD4](#decision-ledger)'s ruled shape: the boot catalog names
the orphans and their sizes, an explicit act removes them, and autoprune is an option nobody gets
by default.

**Fifth, wire the ruled enforcement** ([OQ-PD6](#decision-ledger), [OQ-PD7](#decision-ledger)): the
receipt is the pin, and it reports before it gates — by this point the receipts say how much
divergence there actually is, so any later gate is designed against a measured distribution. mise
is the exception to this sequencing: its lock records and obeys in one mechanism
([§5.6](#56-a6--borrow-the-ecosystems-lockfiles)), so it ships with the first step — what remains
here is the same wiring for everything else.

**Sixth, the installer capture** ([§6.3](#63-installers-that-just-do-whatever-capture-the-install-then-treat-the-capture-as-the-package),
ruled — [OQ-PD10](#decision-ledger)): it slots in as the installer resolver's implementation of
*record* + *materialize* and depends on nothing above except the receipt schema.

**In parallel, pay the enum tolerance** (§6.2). It is small, it is independent of everything above,
and it is only cheap while no one needs it.

---

## Decision Ledger

| ID | Ruling / Decision | Date | Settled in |
| :--- | :--- | :--- | :--- |
| OQ-PD1 | **Shape (d): the record follows the declaration's scope.** Ecosystem-native lockfiles at the declaration's home (repo for `mise.toml`/`flake.nix`, user for `packs`); yolo-written receipts only where no native lock exists; bytes in machine-global content-addressed stores. | 2026-08-24 | §5.6, §4.4 |
| OQ-PD2 | **Same-machine and same-workspace ship first, through the user half; same-declaration-anywhere is in scope for the project toolchain** via repo-committed native locks, and out of scope for user tools, which no repo should pin. | 2026-08-24 | §2, §5.6 |
| OQ-PD3 | **No-evergreen extends to mise — it is a principle, not an npm fix.** The per-launch `mise upgrade --yes` was a stopgap; whatever pins mise ships as **part of the general seam** (a tier-2 resolver whose *obey* goes through mise's own pinning), never as a standalone lockfile flip ahead of it. | 2026-08-24 | §4.2, §6.1, §10 |
| OQ-PD4 | **Dropping a pack does not auto-delete its program.** Orphans are cataloged informationally at boot; removal happens only on an explicit act; autoprune exists as an option, **default off**. | 2026-08-24 | §4.3, §9 R3, §10 |
| OQ-PD5 | **One lifecycle, N resolvers, N native records under ONE READER** — no ledger-as-store of opaque identities; only a resolver parses its own record; the tiers stay explicit. | 2026-08-24 | §6 |
| OQ-PD6 | **The receipt is the pin.** A declaration may carry a version and is not required to; install obeys the record. Also answers OQ-TP3's inherited half. | 2026-08-24 | §6, §8.1 |
| OQ-PD7 | **Report first; gate later only if the reports justify it** — and the record names where a gate would live. | 2026-08-24 | §6, §9 R1 |
| OQ-PD8 | **The launcher's informational poll is unreachable in steady state** (nineteen days of unmoved stamps); the "newer version available" channel moves to the boot catalog and the update verb / reconcile. | 2026-08-24 | §4.4, §10 |
| OQ-PD9 | **Native lockfile formats whenever one exists; a yolo-own repo lockfile only when the work demonstrates the need** — permitted, never preemptive. | 2026-08-24 | §5.6, §6.3 |
| OQ-PD10 | **Capture-and-repackage adopted for the installer class**, sequenced last: an ephemeral jail plus a snapshot of its fresh home surfaces, a plain filesystem artifact in the machine CAS, never an image layer. The receipt ships first; capture replaces its guess at "what the installer did" with a manifest. | 2026-08-24 | §6.3, §10 |

---

## Open Questions

None. All ten are ruled — see the [Decision Ledger](#decision-ledger).
