---
title: "How executable content gets into a jail — and what makes two jails the same"
date: 2026-08-18
status: in-review
tags: [packs, uniformity, delivery, pinning, npm, mise, image]
summary: "Four delivery classes, one of which keeps no record and is never re-derived — and all divergence lives there. The fix is a receipt, removal and scope agreement, not an npm pin."
---

# How executable content gets into a jail — and what makes two jails the same

**Status:** DIAGNOSIS + PROPOSAL, in-review, 2026-08-18. **Nothing built.** Every measurement below
was taken in this development jail on 2026-08-18 and is labelled **MEASURED**, **READ FROM CODE**
(traced but not observed running) or **NOT MEASURED**. This document exists to raise questions, not
to settle them.

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
first — the artifact that says what this jail got — then removal, then the pin.**

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
is the image load sentinel (`BUILD_DIR/last-load-<runtime>`, ten store paths on this machine). No
mechanism in the installed-and-kept class writes one, which is why *"what did this jail actually
get?"* is currently unanswerable.

**P4. The unit is a delegated resolution, not an npm package.** Anywhere yolo declares a NAME and a
third party decides the bytes: `program via npm`, `program via installer`, the LSP recipes, mise
tools, claude plugins, an `npx -y` MCP argv. npm is simply the first one anyone looked at.
**Anything scoped to `via: npm` is scoped to one row of a table that already has six**
([§3](#3-four-delivery-classes-and-the-rule-that-falls-out)).

**P5. Regenerated beats installed.** Content re-derived every launch cannot diverge; content
installed once and kept always can. Where a mechanism can be moved into the regenerated class, that
beats pinning it in place — and where it cannot, the *reconciliation* half is still available
cheaply ([§5.4](#54-a4--regenerate-or-reconcile-every-launch)).

---

## 2. What "the same jail" would have to mean

The question has three candidate answers and they impose different designs. This is unsettled and
it is [OQ-PD2](#-oq-pd2--what-is-the-unit-of-sameness).

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
| **Baked** | nixpkgs (96.75 % of the closure), the four shipped Go binaries, `mise` itself | per image build, hermetic (`-mod=vendor`, committed `vendor/`) | `flake.lock` + the load sentinel | ✅ **provably** |
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
> the same place: they need no pin because they are **rebuilt**, not installed. **MEASURED:** 1519
> per-workspace staging dirs exist under `~/.local/share/yolo-jail/agents/`, and every one of them
> is cleared and re-rendered on its next launch.

---

## 4. How two jails diverge today (measured)

### 4.1 Freeze: an agent CLI is whatever `@latest` meant the day that workspace first ran it

The npm launcher's cold branch is unconditional and unversioned — `if [ ! -x "$REAL_BIN" ]` →
`_do_install || true` with `SPEC=<name>@latest` (`internal/entrypoint/shims.go`, `npmInstallSpec` in
`internal/entrypoint/npmspec.go:62-64`: *"an unversioned declaration still resolves to `@latest`"*).
The comment on that branch is explicit that the no-evergreen ruling deliberately does not touch it:
*"the FIRST install is not a poll … without this branch a fresh jail would simply have no agent CLI
at all."*

**MEASURED**, one workspace's `~/.npm-global` on this machine:

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
(`internal/cli/run/command.go:8-22`, a frozen-contract constant). The store is a single bind —
`-v <miseStore>:/mise` (`internal/cli/run/assemble_parts.go:117-122`), backed by
`paths.GlobalMise()` = `~/.local/share/yolo-jail/mise` (`internal/paths/paths.go:267`) — **one store
for every workspace and every nesting depth.**

**MEASURED**, `/mise/installs`:

```
node/24     -> ./24.19.0     node/latest -> ./24.19.0   (22.23.2 and 22.20.0 still on disk)
go/1.26     -> ./1.26.6                                 (1.26.2 still on disk)
…staticcheck/latest -> ./2026.1                         (0.7.0 still on disk, from 2026-07-29)
```

Those are **symlinks, repointed by an upgrade** — the old versions stay on disk and the alias moves.
And the aliases are not merely internal bookkeeping: **MEASURED**, this jail's live `PATH` contains
`/mise/installs/node/24/bin`, `/mise/installs/go/1.26.6/bin`,
`/mise/installs/just/latest`, `/mise/installs/go-honnef-co-go-tools-cmd-staticcheck/latest/bin`.
Three of those four entries resolve **through a fuzzy alias**, so an upgrade run by a jail in another
workspace changes what a *running* jail's PATH points at.

**MEASURED**: `mise settings` reports `locked = false`; no `mise.lock` exists anywhere in the tree
(`rg -n 'lockfile|mise.lock|MISE_LOCK'` returns only pack-lockfile hits). The baked mise (2026.7.17)
**does** ship a `mise lock` command and a `locked` setting — so the capability exists and we do not
use it. **NOT MEASURED:** what enabling `locked` actually does to this setup, or whether its
lockfile format survives a mise upgrade.

> [!IMPORTANT]
> **mise is not a hypothetical "second uncontrolled mechanism" — it is the one that already
> arrived, and it is worse than npm on every axis**: more registries, evergreen per *launch* rather
> than per hour, machine-global rather than per workspace, no lockfile, and no `update` verb of ours
> to hang a rule on. It also carries at least three resolvers beyond nixpkgs — **MEASURED** in
> `/mise/installs`: `go-honnef-co-go-tools-cmd-staticcheck` (Go module proxy),
> `pipx-mypy` and `pipx-swarf` (PyPI). Any design scoped to `program via npm` misses all of them.

### 4.3 History: a jail is the union of every pack ever selected, not the current pack set

**MEASURED:** this jail's user config selects `"packs": ["claude"]` and `~/.yolo-launchers/` holds
exactly two launchers (`claude`, `pnpm`) — yet `~/.npm-global/lib/node_modules` still holds
`@github/copilot`, `@openai/codex`, `@earendil-works/pi-coding-agent` and a stray `fzf` from a
deleted test pack, and `~/.local/bin` holds a **189 MB** `agy` binary.

Dropping a pack removes its **launcher** (`resetAnchorDir` clears the anchor dir contents-only every
boot, `internal/entrypoint/shims.go:13-29`) and now removes its **staged tree** (`packstage` rule 3,
the fix that closed [`program-kind-defects.md`](program-kind-defects.md) 11.3). It has never removed
the **installed program**. **MEASURED:** the only `npm uninstall -g` in the entire tree is in the LSP
bootstrap (`internal/entrypoint/shell.go:298`), keyed on the `~/.yolo-installed-lsps` sentinel
(`shell.go:245`).

Two consequences worth stating separately:

- **Two jails whose current config is byte-identical still differ by their config HISTORY.** Any
  claim of the form *"the pack set plus a lockfile makes jails uniform"* is false until removal is a
  real operation.
- **The LSP sentinel is the only install/uninstall reconciliation loop in the system, and it is one
  field short of being a receipt** — it stores `kind:identifier` lines (`npm:pyright`,
  `go:golang.org/x/tools/gopls@latest`, `internal/cli/run/lsp.go:16-20`) and never what the install
  *resolved to*. That makes it the cheapest available prototype for P3.

### 4.4 The scope mismatch: the maintainer's premise, corrected

*"This is user level, but the realization is workspace level"* is half right, and the wrong half is
the one that decides the design. **READ FROM CODE**, `internal/cli/run/assemble_parts.go:64-122`
and `internal/cli/run/assemble.go:564`:

| Thing | Scope | Backing |
| :--- | :--- | :--- |
| the declaration (`packs`) | **user** | `~/.config/yolo-jail/config.jsonc`, read directly and never from the merged config |
| npm programs, installer programs, Go bins | **per workspace** | `<ws>/.yolo/home/{npm-global,local,go}` (`:70-72`) |
| **the npm download cache** | **machine-global** | `NPM_CONFIG_CACHE=/home/agent/.cache/npm`; `.cache` is `paths.GlobalCache()` (`:81`) |
| **the update stamps and spec records** | **machine-global** | `~/.cache/yolo-agent-stamps` (`shims.go:190`) — beside a per-workspace `REAL_BIN` |
| **mise tools** | **machine-global, mutated in place** | `/mise` (`:117-122`) |
| the base home | machine-global, **read-only** | `paths.GlobalHome()` mounted `:ro` (`:69`) |
| the image | machine-global by **name**, per-config by **content** | one `localhost/yolo-jail:latest` tag; `packages:` is workspace-settable |
| the pack store / the pack lockfile | machine-global store / **user-scope** lockfile | `paths.PacksDir()` (`paths.go:303`) / `~/.config/yolo-jail/packs.lock.json` |
| pack trees, skills, surfaces | per workspace, **derived** | cleared and re-staged every launch |

**So the pin the premise imagines, the bytes it would govern, and the cache and stamps that mediate
them are three different lifetimes.** One file cannot be all three scopes, which is
[OQ-PD1](#-oq-pd1--where-does-the-receipt-live).

The stamp/spec split is the shape of the bug this causes, already in production:

- **MEASURED:** `~/.cache/yolo-agent-stamps/` holds `claude.stamp` (2026-08-05) and `fzf.stamp`
  (2026-08-02) and **no `.spec` file beside either**, while the launchers were regenerated
  2026-08-18. The stamps survive boots and are shared by every workspace on the machine — so the
  template's `elif [ ! -f "$STAMP" ]` branch, commented *"first run since jail boot"*, in fact fires
  at most once per **machine** per binary.
- **READ FROM CODE, NOT MEASURED:** `_do_install` ends with `touch "$STAMP"`
  (`shims.go:352-377`), so a **cold install in workspace B writes the stamp workspace A throttles
  on** — the most likely explanation for a `claude.stamp` dated two weeks after the binary it
  describes.
- **READ FROM CODE, NOT MEASURED:** if two workspaces ever carried *different* pinned specs for one
  bin, the `PINNED` branch compares `$SPEC` against a shared `SPEC_FILE`
  (`shims.go:483-494`), so each would reinstall on every launch, forever.

### 4.5 The record that does not exist

**MEASURED:** `~/.config/yolo-jail/packs.lock.json` is `{"schema": 1, "packs": {}}` on a machine
whose home holds four npm-installed agent CLIs. That is not a missing *field* — it is a missing
*row*: `LockEntry` (`internal/packsrc/lock.go:33-55`) exists per **fetched** pack, `Commit` is
documented *"empty for a local pack"*, and every npm-declaring pack is **embedded**. The file is
also, per [`trust-paths.md`](trust-paths.md) §1, display-only: its `Commit`/`Ref` readers all print
and none gate.

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
already machine-global and holds **645 MB** (`~/.cache/npm/_cacache`, plus an `_npx` tree — so
`npx -y` MCP packages cache there too). A second workspace installing a version this machine already
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
(`internal/cli/packupdate.go:108-127`).

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
| the machine-global artifact cache | `NPM_CONFIG_CACHE=/home/agent/.cache/npm` — 645 MB, shared by every workspace |
| the explicit update act | `yolo pack update` (npm-only and jail-only today, `packupdate.go:95-102`) |
| the reconcile loop | the LSP sentinel's install **and uninstall** (`shell.go:245-312`) |
| the receipt's file format | `packs.lock.json` — schema-versioned, already the place a resolution would go |

**What it costs.** A new artifact class and its retention policy — and this repo's track record there
is 404 GiB of image tars accrued in 24 days (`image-staging` §1.6), plus **MEASURED** 1.04 GB of
claude versions (4 builds at 245–275 MB) sitting in one workspace's `~/.local/share/claude/versions`.
It also has a genuine cold-start hole: the first resolve has no receipt to obey, so *something*
resolves, and the honest answer is that the update act does it in the open rather than a launcher
doing it silently.

**It is also the only option that answers the download question directly**: the installer class is
the one that downloads per workspace today (`~/.local` is a per-workspace bind), which is why claude
and `agy` are re-fetched per workspace while npm packages are not. Moving that class's artifacts into
the machine-global cache is a smaller change than baking and fixes the same complaint.

> **Verdict: adopt, in the order record → reconcile → remove → obey.** The record is cheap, is
> useful on its own (it makes divergence *visible* for the first time), and is what turns
> "is pinning worth it?" from an argument into a measurement.

### 5.4 A4 — Regenerate (or reconcile) every launch

Make class 3 behave like class 2: reinstall from the receipt on every boot, the way `_official/` is
cleared and re-staged.

> **Verdict: rejected as literal reinstall; adopted as reconcile.** A launch must not depend on a
> registry being reachable, and an install is not free. But the *comparison* is free and offline —
> "what the receipt says vs. what is on disk" — and the LSP sentinel already proves the loop can be
> written. Reconcile reports; it does not install.

### 5.5 A5 — Do nothing, and say so

Accept that jails are not uniform, and document it.

> **Verdict: rejected, but its honest half survives and belongs in the design.** No mechanism can
> cover the tier-3 class in [§6](#6-the-general-seam-one-ledger-many-resolvers), so whatever ships
> must **enumerate what it does not manage** rather than implying coverage it lacks. A seam that
> claims to cover an `npx -y` argv would be worse than one that names it as unmanaged.

---

## 6. The general seam: one ledger, many resolvers

**The unit is a delegated resolution** (P4): a tuple of *(declaration, resolver, resolved identity,
landing path, scope, time)*. The ledger stores the resolved identity as an **opaque string** and only
the resolver interprets it — that is the single design decision that keeps this out of npm's shape,
because a pin is inherently ecosystem-flavoured (`pkg@1.2.3`, a git SHA, a URL+hash, a mise
`backend:tool@version`).

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
| **Managed** | yolo runs the install | `program via npm`, `program via installer`, LSP recipes | all six verbs: record, obey, reconcile, remove |
| **Observed** | a third party installs into a store yolo mounts | **mise**, claude plugins (`installClaudePlugins`, `internal/entrypoint/boot.go:311-337`) | record and compare; obeying requires the third party's own pin (mise has one; we do not enable it) |
| **Unmanaged** | yolo never sees the resolution | `npx -y <pkg>` in an MCP argv (`config_ref.txt:757,769`), a vendor CLI's self-updater (claude's, `agy`'s) | **none** — the only honest act is to enumerate it |

The launcher template already states the tier-3 case exactly, about silent updates:
*"a silent change has no act to pin to, so no pin, lockfile field or approval prompt can ever cover
it"* (`shims.go:379-395`). That sentence is the boundary of the seam, written down before the seam
existed.

**Applied to mise, which is the test case that already exists:** it lands in tier 2. We can record
what `/mise` resolved to, and we can compare it. We cannot make it obey without enabling mise's own
`locked` — which is a third-party format on a third-party cadence, and which is machine-global, so
"pin it per workspace" is not even representable ([OQ-PD3](#-oq-pd3--does-no-evergreen-extend-to-mise-upgrade---yes)).

### 6.2 Pay the enum tolerance before the next mechanism arrives

The `via` field is a **closed two-value set** — `validateContribution` rejects anything but `npm`
and `installer` (`internal/packdecl/contributes.go:735-746`) — and the two halves of the system
disagree about what to do with a third value:

- `GenerateAgentLaunchers` drops an unrecognised `Kind` with a bare `default: continue` and **no
  message** (`internal/entrypoint/shims.go:214-221`).
- `DecodeTolerant` skips unknown *kinds* but still validates known ones, so an unknown `via` **value**
  on kind `program` is a validation problem — and the boot path treats any problem as fatal.

**READ FROM CODE, NOT MEASURED:** a pack declaring `via: "uv"` staged for an older baked entrypoint
is therefore a refused boot, not a skipped contribution — the same shape `packdecl`'s own comment
warns about for a third `tier` value. **Whoever adds the third `via` must extend the tolerance
first.** That is a prerequisite of this design, not a consequence of it.

---

## 7. What this does NOT cover

- **Security and trust.** [`trust-paths.md`](trust-paths.md) owns it. This doc does not re-argue
  OQ-TP5 (no evergreen npm), does not propose new gates, and **does not claim integrity**: a receipt
  records what you got, not that it is what a publisher signed. No digests, no attestation, no SBOM.
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
  superseded by [OQ-PD1](#-oq-pd1--where-does-the-receipt-live) (where the receipt lives) and
  [OQ-PD5](#-oq-pd5--one-general-seam-or-per-ecosystem-adapters). **What survives verbatim and must
  not be re-derived:** TP4's cost analysis of option (a) — pinning in the manifest makes yolo's
  release cadence the ceiling on agent-CLI freshness — is the same objection A1 hits in §5.1, and
  TP4's leaning toward the lockfile is preserved in A3's shape.
- **INHERITS [OQ-TP3](trust-paths.md#-oq-tp3--given-1-is-pinning-worth-building-at-all-and-where-first)'s still-open half** — *is yolo's own embedded pack required to
  pin, and is a fetched pack required or merely permitted?* It is restated at wider scope as
  [OQ-PD6](#-oq-pd6--is-a-declaration-required-to-carry-a-pin-or-is-the-receipt-the-pin), with one
  reframe: **once a receipt exists, a declaration need not carry a pin, because the receipt is the
  pin.** That reframe is the reason inheriting is worth more than answering TP3 as written.
- **Unchanged by this doc:** OQ-TP5's ruling and OQ-TP6's fatal. §5.2's finding that the launcher is
  PATH-shadowed after first use does not dispute the ruling — it questions whether the *reporting*
  half it built ever executes ([OQ-PD8](#-oq-pd8--is-the-launchers-informational-poll-reachable-at-all)).

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
| R1 | **A receipt that nothing enforces becomes another display-only field.** The precedent is exact: `LockEntry.Commit` has four readers and all of them print (`trust-paths.md` §1). | Decide enforcement in the same change that adds the record, even if the decision is "reports only, on purpose, and here is where it would gate" — [OQ-PD7](#-oq-pd7--does-the-receipt-gate-the-launch). |
| R2 | **A ledger in the wrong scope is worse than none.** The stamp/spec split is the live proof: a machine-global record describing a per-workspace install already produces cross-workspace throttle bleed (§4.4). | Settle [OQ-PD1](#-oq-pd1--where-does-the-receipt-live) before writing a file. A receipt describes a *realization*, and a realization has exactly one location. |
| R3 | **Removal is destructive and the bytes are large.** Uninstalling on pack-drop can delete a 189 MB binary a user still runs from another workspace's muscle memory. | Reconcile **reports** by default; removal happens on an explicit act, never at boot. The LSP sentinel's silent uninstall is the pattern *not* to copy at this size. |
| R4 | **An unbounded artifact cache.** 404 GiB of image tars accrued in 24 days with a hint firing and nothing pruning (`image-staging` §1.6); npm's cacache is already 645 MB and claude keeps 4 versions (1.04 GB) per workspace. | Retention lands with the cache, not after it, and hangs off `yolo prune`. |
| R5 | **A seam that implies tier-3 coverage is a lie.** An `npx -y` argv and a vendor self-updater cannot be recorded at all. | Enumerate unmanaged mechanisms in the same surface that reports the managed ones (§5.5, §6.1). |
| R6 | **The closed `via` enum turns the next mechanism into a boot refusal** on any pre-`just load` image (§6.2). | Extend the tolerance *before* adding a third value: skip-and-report under `DecodeTolerant`, refuse loudly under `Decode`. |
| R7 | **Tier-2 uniformity depends on a third party's lockfile.** Enabling mise's `locked` makes our guarantee a function of mise's format and cadence, and **NOT MEASURED** here is what enabling it does to a shared `/mise`. | Treat tier 2 as "record and compare" until a measurement exists; do not promise obedience we do not own. |
| R8 | **Every measurement is from one machine and one home.** The dates in §4.1 are this jail's history, not a general law. | The *mechanism* (cold-branch `@latest`, shared aliases, absent removal) is read from code and generalises; the dates are illustrative and labelled. |

---

## 10. What I would build, in order

**First, write the receipt for the managed tier only.** Asked-for declaration, resolver, resolved
identity, landing path, timestamp — for npm programs, installer programs and LSP servers, the three
places yolo runs the install itself. It changes no behaviour, so it can land while the questions are
still open, and it is what makes every later question answerable with a measurement instead of an
argument.

**Second, generalise the LSP sentinel into a reconcile.** It already does install *and* uninstall
against a declared set; what it lacks is the resolved version and a caller for anything but LSP
servers. Reconcile compares, offline, and reports. It installs nothing and removes nothing.

**Third, settle scope** ([OQ-PD1](#-oq-pd1--where-does-the-receipt-live),
[OQ-PD2](#-oq-pd2--what-is-the-unit-of-sameness)) — because removal and obedience are both
unimplementable until the record's reach matches the bytes'.

**Fourth, make removal real**, on an explicit act, with the sizes in R3 in mind.

**Fifth, and only then, decide what obeys** ([OQ-PD6](#-oq-pd6--is-a-declaration-required-to-carry-a-pin-or-is-the-receipt-the-pin),
[OQ-PD7](#-oq-pd7--does-the-receipt-gate-the-launch)). By this point the receipts say how much
divergence there actually is, and the answer stops being a matter of taste.

**In parallel, pay the enum tolerance** (§6.2). It is small, it is independent of everything above,
and it is only cheap while no one needs it.

**mise's `locked` is the cheapest single uniformity win in the inventory and it is deliberately last**
— it is a setting away, but it is machine-global and evergreen-by-launch, so flipping it before
[OQ-PD3](#-oq-pd3--does-no-evergreen-extend-to-mise-upgrade---yes) is answered changes every
workspace on the machine at once.

---

## Open Questions

### 💬 OQ-PD1 — where does the receipt live?

One file cannot be all three scopes (§4.4): npm/installer/LSP realizations are **per workspace**,
mise and the artifact cache and the stamps are **machine-global**, and the declaration is
**user-scope**. Three shapes: (a) one user-scope ledger beside `packs.lock.json`; (b) one receipt
beside each realization, with a single reader that merges them; (c) per-mechanism receipts with no
common reader.

**What it decides:** whether removal and enforcement are implementable at all — both need the record
and the bytes to have the same reach — and whether `yolo pack update` can remain the one update verb.

_Leaning:_ **(b).** A receipt describes a realization, and a realization has exactly one location; a
user-scope file describing a `/mise` store that three other workspaces mutate would be false the
moment it was written. The cost is that "what did this jail get?" becomes a query over several files
rather than a `cat` — which is what a reader is for.

**Answer:**
> _(empty — fill in when decided)_

### 💬 OQ-PD2 — what is the unit of "sameness"?

§2's three readings: same machine, same workspace over time, or same declaration on anyone's machine.
They are not refinements of each other — (c) requires a record **committed to the repo** and
resolvers that can obey it offline, which is a different product from (a) and (b).

**What it decides:** whether the receipt is user/workspace state or a checked-in artifact, and
therefore whether a colleague's jail is in scope at all.

_Leaning:_ **(a) and (b) first, (c) explicitly out of scope for now.** Today (a) fails on one machine
with one config, which is the surprising failure and the cheap one to fix. A committed lockfile is a
coherent product and a much larger one.

**Answer:**
> _(empty — fill in when decided)_

### 💬 OQ-PD3 — does "no evergreen" extend to `mise upgrade --yes`?

OQ-TP5 removed the evergreen npm poll. Nobody has proposed the same for mise, which runs on **every
launch**, is **machine-global**, and repoints alias symlinks a *running* jail already resolves
through (§4.2). mise ships `locked` and a `lock` command; we set neither.

**What it decides:** whether the no-evergreen rule is a principle or a fix applied to the one
mechanism that got audited — and whether tier 2 (§6.1) gets any uniformity guarantee at all.

_Leaning:_ **Yes in principle, via mise's own `locked`, and gated on OQ-PD2.** The rule should not be
npm-shaped. But `/mise` is shared, so a per-workspace pin is not representable, and enabling `locked`
changes every workspace at once — which is precisely why it needs a ruling rather than a commit.

**Answer:**
> _(empty — fill in when decided)_

### 💬 OQ-PD4 — should dropping a pack uninstall its program?

**MEASURED:** this jail holds copilot, codex, pi and a 189 MB `agy` under `"packs": ["claude"]`
(§4.3). The staged-tree half of this was fixed; the installed-program half never was. The LSP
sentinel proves the loop is buildable and also shows its danger — it uninstalls quietly.

**What it decides:** whether "the pack set plus a lockfile makes jails uniform" can ever be true, and
whether a jail's contents are its config or its config's history.

_Leaning:_ **Yes, but never at boot and never silently.** Reconcile reports the orphans; an explicit
act (`yolo pack update`, or a `prune`-shaped verb) removes them, naming sizes.

**Answer:**
> _(empty — fill in when decided)_

### 💬 OQ-PD5 — one general seam, or per-ecosystem adapters?

*"I'm also worried about getting too special case for npm. What happens when another mechanism comes
along we can't control?"* §6 proposes one ledger and one lifecycle with N thin resolvers, the ledger
storing an **opaque** resolved identity. The alternative is per-ecosystem records that each know
their own shape.

**What it decides:** how much work the fourth mechanism costs, and whether the third `via` value is a
config change or a redesign.

_Leaning:_ **One ledger, one lifecycle, N resolvers — with the three tiers made explicit**, because
the tier is what varies and it is what a user needs told. A pin is ecosystem-shaped, so only the
resolver may parse it; everything else in the record is common. And the enum tolerance (§6.2) is paid
up front regardless of which way this goes.

**Answer:**
> _(empty — fill in when decided)_

### 💬 OQ-PD6 — is a declaration required to carry a pin, or is the receipt the pin?

Inherits [OQ-TP3](trust-paths.md#-oq-tp3--given-1-is-pinning-worth-building-at-all-and-where-first)'s open half at wider scope. A `package` string may already carry a
version, tag or range (`npmspec.go`), and **no shipped pack does**. With a receipt in place, an
unpinned declaration is no longer evergreen — the receipt is what install obeys.

**What it decides:** whether pack authors must version their declarations (a compatibility burden on
every pack) or whether yolo's own record is sufficient — and whether embedded and fetched packs
answer differently.

_Leaning:_ **The receipt is the pin; a declaration may pin and is not required to.** Requiring it
puts yolo's release cadence on the critical path for embedded packs (TP4 option (a)'s cost) and asks
third-party authors to solve a problem the ledger solves once.

**Answer:**
> _(empty — fill in when decided)_

### 💬 OQ-PD7 — does the receipt gate the launch?

A record that nothing enforces is a receipt, not a gate — the lockfile's `Commit` field is the
standing proof (R1). But the enforcement options are not symmetric: refusing a launch because a
resolved version drifted would be a fatal in the class where the user is least able to act, and this
system already has three fatals.

**What it decides:** whether uniformity becomes a guarantee or stays an observation — and whether
`yolo check` must predict the outcome (the shape of OQ-TP7's first half).

_Leaning:_ **Report first, gate later if the reports justify it.** Land the record, look at real
drift for a while, then decide. A gate designed before the measurement is a gate designed against an
imagined distribution.

**Answer:**
> _(empty — fill in when decided)_

### 💬 OQ-PD8 — is the launcher's informational poll reachable at all?

OQ-TP5 kept an hourly poll and downgraded it to a message. **MEASURED:** both install destinations
precede `~/.yolo-launchers` on PATH and `type -a claude` confirms the shadowing, so in steady state
the launcher never runs again and the poll never fires. Contradicting evidence: `claude.stamp` is
dated 2026-08-05 while the binary it describes was linked 2026-07-25. **READ FROM CODE, NOT
CONFIRMED:** `_do_install` touches the stamp and the stamp dir is machine-global, so a *cold install
in another workspace* explains it without any poll running — but I did not confirm which act wrote
it.

**What it decides:** whether the half of OQ-TP5 that was built does anything. If the poll is
unreachable, the "a newer version is available" channel has to move — to the update verb, to boot, or
to the reconcile in §10 — and the freeze in §4.1 currently has **no** reporting path at all.

_Leaning:_ **It is unreachable in steady state, and the stamp is explained by the shared scope rather
than by a poll.** Confirmable cheaply: clear one stamp, launch, and see whether it reappears without
`yolo pack update`.

**Answer:**
> _(empty — fill in when decided)_
