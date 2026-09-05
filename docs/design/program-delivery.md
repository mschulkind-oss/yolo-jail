---
title: "How executable content gets into a jail — and what makes two jails the same"
date: 2026-09-04
status: in-review
tags: [packs, uniformity, delivery, pinning, npm, mise, image, evergreen]
summary: "Four delivery classes, one of which keeps no record and is never re-derived — and all divergence lives there. Amended 2026-09-03 with a second axis: a dependency serves either the AGENT (evergreen, updated at launch) or the PROJECT (pinned, reproducible), and the delivery mechanism does not tell you which."
---

# How executable content gets into a jail — and what makes two jails the same

**Status:** DECIDED, 2026-08-24; implementation underway the same day. All ten questions are
ruled (the [Decision Ledger](#decision-ledger)). Shipped: the mise half of step one and the
evergreen removal (`a16403e2`), the §6.2 tolerance (`0a4d241c`), step one's receipts and step
four's informational catalog (`af46c9b4`), and **the reconcile verb (`43f28ce8`)**; the removal act,
obey, and capture remain — [§10](#10-what-i-would-build-in-order). ⚠ *This line listed the reconcile
as remaining until 2026-09-03; it had shipped (`internal/entrypoint/reconcile.go:83`). The roadmap
credited `0eabeabc`, which resolves as an object but is **not an ancestor of HEAD** — a pre-rebase
SHA left behind.* Every fact below is labelled
**MEASURED** (observed in this development jail, 2026-08-24), **READ FROM CODE** (traced but not
observed running) or **NOT MEASURED**.

> [!IMPORTANT]
> **AMENDED 2026-09-03 — the doc reopened, and §3.5 is the amendment.** This document ruled
> *no-evergreen* as a principle covering every resolver ([OQ-PD3](#decision-ledger)) and rejected
> install-at-launch ([§5.4](#54-a4--regenerate-or-reconcile-every-launch)). Six weeks of
> measurement say the principle reached across a boundary it could not see: **an agent CLI and a
> project toolchain are different kinds of dependency and want opposite policies.** §3.5 draws that
> boundary and rules agent dependencies **evergreen**. Four new rulings
> ([OQ-PD11](#decision-ledger)–[OQ-PD14](#decision-ledger)) and two further questions
> ([OQ-PD15](#decision-ledger), [OQ-PD16](#decision-ledger)) followed from it and were both
> ruled the same day, so the doc is `decided` again with **zero** open questions.
>
> **§1–§10 otherwise describe the design as ruled on 2026-08-24 and are unchanged.** Where §3.5
> narrows an earlier ruling, the ledger row says so in place — no row was deleted, and no section
> was renumbered, because both are cited from code.

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

> [!NOTE]
> **A sixth principle was added on 2026-09-03 and lives in [§3.5](#35-the-second-axis-who-the-dependency-serves-amendment-2026-09-03),
> not here** — *P6. Class decides policy; mechanism decides implementation.* It is numbered into the
> same sequence deliberately (the numbers are cited across docs), but it is stated where the axis it
> depends on is drawn. This heading still says "five" because renaming a section that sibling docs
> cite costs more than the inconsistency.

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

### 3.5 The second axis: who the dependency serves (**AMENDMENT, 2026-09-03**)

§3 classifies by **how** something is delivered and [§6.1](#61-three-tiers-of-control--the-answer-to-what-about-a-mechanism-we-cant-control)
by **how much control** yolo has over the resolution. Neither asks the question that decides the
update policy: **who is this dependency for?** That is the axis this amendment adds, and the two
answers want opposite things.

**Two terms, coined here:**

- An **agent dependency** is a program yolo installs so that a coding *agent* can run: the six agent
  CLIs, and the MCP and LSP servers those agents drive. It serves the agent, never the work in
  `/workspace`. Nobody's build reproduces against it, and a version six weeks old is a defect rather
  than a guarantee. **It wants to be current.**
- A **project dependency** is a program the work in `/workspace` needs: the language toolchains
  (`mise_tools`), the system packages yolo bakes or adds (`packages`, nixpkgs), and anything else
  whose version could change what the project builds or how it behaves. **It wants to be
  reproducible.**

> [!IMPORTANT]
> **The delivery mechanism does not tell you the class, and assuming it does is the trap this
> section exists to name.** npm carries both: `@github/copilot` is an agent dependency and
> `typescript-language-server` is argued about. nix carries project dependencies today and could
> carry an agent tomorrow. The overlap is **incidental** — an artefact of which distribution system
> a vendor happened to pick — so a policy attached to `via: npm` or to "the managed tier" attaches
> to the wrong thing. The class is a property of the dependency, declared, never inferred from how
> it arrives.

**P6. Class decides policy; mechanism decides implementation.** *(numbered so later sections and
sibling docs can cite it, as P1–P5 are.)*

| | Agent dependency | Project dependency |
| :--- | :--- | :--- |
| Wants | latest | reproducible |
| Pin | **none** — there is nothing to be reproducible against | required, at the declaration's home ([OQ-PD1](#decision-ledger)) |
| Resolution | at every launch | on an explicit act only |
| Record | a receipt, for *what did I run* | a lockfile, for *what must I run* |
| Members today | the six agent CLIs, MCP servers, LSP servers | `mise_tools`, `packages`, nixpkgs, `flake.lock` |

**This narrows [OQ-PD3](#decision-ledger) rather than reversing it.** That ruling said *"no-evergreen
extends to mise — it is a principle, not an npm fix."* The mise half stands and is untouched: mise
tools are project dependencies and they pin. What breaks is the word **principle** — the claim that
it reaches every resolver. It does not reach the agent class, and the four npm packages OQ-TP5
covered (pi, copilot, codex, opencode) are all in that class.

#### What "evergreen" means, precisely

**RULED ([OQ-PD11](#decision-ledger)–[OQ-PD14](#decision-ledger), 2026-09-03) and SHIPPED
2026-09-04** ([`../plans/evergreen-agent-updates.md`](../plans/evergreen-agent-updates.md)).
Everything below is behaviour in the tree, with two exceptions stated where they arise: the
MCP/LSP half of point 2 under *"The four things an implementer would otherwise have to guess"*
is still ruled-and-unbuilt, and `copilot` remains on npm ([OQ-PD13](#decision-ledger)).

- **Trigger: the AGENT'S OWN INVOCATION, not a jail launch.** Typing `claude` reaches a generated
  launcher, which decides whether to update, then `exec`s the real binary. A launch that starts no
  agent updates nothing. **This is the original design of `~/.yolo/bin/launch`, restored** — not a
  new mechanism.
- **Throttled by a stamp, not run every time.** The launcher checks at most once per
  `UPDATE_INTERVAL` (3600s today) per program, exactly as the npm template already does. A second
  `claude` a minute later does no work.
- **Cold start is the same act.** A program that is absent is installed; a present one past its
  stamp is updated. One path, so a fresh workspace and a six-week-old one converge.
- **The update verb is the pack's to declare** ([OQ-PD14](#decision-ledger)). Vendors disagree
  (`claude install`, `pi update --self`, `codex update`), so core cannot hardcode one. A `program`
  declaring no verb is refreshed by re-running its installer or `npm install -g`, per its `via`.
  **Built as `packdecl.Contribution.Update` → `Install.UpdateVerb`, projected for EVERY `via`**
  (the verb describes what the program does to itself, not how it arrived). Declared by the three
  installer-delivered packs — `claude install`, `agy update`, `codex update`. The three
  npm-delivered ones declare NONE on purpose: `npm install -g <pkg>` reaches the same registry a
  vendor verb would and is the path measured to work, so a second unmeasured path buys nothing.
  That corrects this table's claim that `pi` "is evergreen only through `pi update --self`",
  which assumed a flip to a native installer that did not happen (pi.dev/install.sh IS npm).
- **Failure is scoped to the invocation, and is not a jail-level fatal.** Offline with the agent
  **installed** → run what is there; the user asked to run it, not to update it. Offline with the
  agent **absent** → that command fails, loudly, naming the network. **No jail refuses to boot over
  this, and there is no `YOLO_ALLOW_STALE_AGENTS` escape hatch, because nothing global is being
  killed.**
- **Timeout:** 60 seconds for the update attempt, after which the launcher proceeds with whatever is
  installed. A hung vendor updater must not hang the command the user actually typed.
- **Forbidden:** never resolve a *project* dependency; never write outside the program's own install
  prefix; never run for a pack the config does not select; **never block on the network when a
  working binary is already present.**
- **Pre-existing state:** the first invocation of each agent after this ships pays one update. It is
  spread across agents and across time rather than landing in one boot.
- **Done looks like:** `claude` run a week apart is two different versions, nobody typed an update
  command, and a jail that never starts an agent never touches the network for one.

##### Why the launcher, and what B2 changes about PATH

**The launcher was always the right place; one placement decision defeated it.** It installs the
real binary into `~/.local/bin` or `$NPM_CONFIG_PREFIX/bin` — both **ahead of**
`~/.yolo/bin/launch` on `BootPath` — so it mediates the cold start and is unreachable forever after.
The hourly poll it carries was never wrong; it was in a house nobody visits twice. That is the whole
of [OQ-PD8](#decision-ledger)'s "unreachable in steady state", and it is a bug about where the
*installed binary* lands, never an argument for moving the work to boot.

**RULED 2026-09-03 (B2), SHIPPED 2026-09-04: the launch dir moves ahead of the install prefixes,
and a launcher is generated only for a name the image does not already provide.** The two halves
are one decision — the position is what makes the launcher reachable, and the generation-time
check is what keeps the position safe. THREE PATH strings moved, not one:
`entrypoint.BootPath` (the authority), the `.bashrc` export, and `macosuser.SandboxPath`; the
first two had drifted apart about `$HOME/.local/bin` and are now compared entry by entry. The
check is `internal/entrypoint/launchercollision.go`.

> [!IMPORTANT]
> **This converts a structural impossibility into a handled case, and that is the honest cost.**
> Today a pack declaring `program fzf` *cannot* shadow the image's `/bin/fzf`, because the launch dir
> sits after `/bin` — AGENTS.md describes the failure as "unrepresentable rather than handled". Under
> B2 the launch dir sits earlier, so the protection moves from **position** to a **check at
> generation time**: no launcher is written for a name the image provides. Same outcome, weaker
> guarantee — a bug in the check is now expressible, where before it was not. It needs a test that
> fails if the check is deleted, not a test that the check works.
>
> The blockers keep their position regardless: `~/.yolo/bin/block` stays **first**, ahead of the
> launch dir, so interception still wins over installation.

> [!WARNING]
> **"Provide" is scoped, and the natural reading is a silent kill switch.** *(Found while planning,
> 2026-09-03; the first version of this section left "a name the image does not already provide"
> undefined, and the omission is the difference between a working feature and a no-op.)*
>
> **The check covers the IMAGE's own contents (`/bin`, `/usr/bin`) and the DECLARED mise tools. It
> must never consider the install prefixes** — `$NPM_CONFIG_PREFIX/bin`, `$HOME/.local/bin`,
> `$GOBIN`. Spelled as the obvious "is this name already resolvable on `PATH`?", the feature
> destroys itself: after one successful install `~/.local/bin/claude` exists, so the next boot
> writes no launcher, so `PATH` resolves the installed binary directly, and evergreen works exactly
> once. Green, silent, and identical to the freeze this design exists to end.
>
> **Declared, not installed, for mise too.** `GenerateAgentLaunchers` runs at `boot.go:439` and
> `ConfigureMisePrism` at `:491`, so on a cold boot the mise shim directory is empty when the check
> runs. It must read the declared tool set, never the directory.

> [!IMPORTANT]
> **B2 makes the launcher reachable from inside its own update, and that is new.** With the launch
> dir ahead of the install prefixes, any **bare-name** call of the program during an update — a
> vendor installer that invokes `claude`, an npm postinstall that invokes `copilot` — resolves back
> to the launcher rather than to the binary. The launcher's own calls use an absolute `$REAL_BIN`
> and are safe; the vendor's are not, and yolo does not control them. **A launcher must therefore
> be a no-op re-entry when it is already running for that bin** — a per-bin guard in the
> environment. Symptom if missed: a fork bomb on the first invocation after the reorder, not a
> subtle wrong answer.

> [!IMPORTANT]
> **`pnpm` is a project dependency and must not win this reorder.** `GeneratePackageManagerLaunchers`
> writes a `pnpm` launcher unconditionally from a hardcoded list (`internal/entrypoint/shims.go:350`),
> **not** gated on mise. Moved ahead of the mise shims it would shadow a `mise_tools`-declared
> `pnpm` — an agent-class mechanism overriding a project dependency, which is exactly what
> [P6](#35-the-second-axis-who-the-dependency-serves-amendment-2026-09-03) forbids. Either the
> package-manager launchers stay behind mise, or the collision check covers declared mise tools for
> them too. This is the one place where the two dependency classes contend for a name.

##### The four things an implementer would otherwise have to guess

*Added 2026-09-03 after reading §3.5 back as the implementer rather than the author. Each of these
had a behaviour attached to it that the prose implied and did not state.*

**1. The knob is `agent_updates`, and it is USER-SCOPE ONLY.** Either a bool or a per-pack map, with
`"*"` as the default key:

```jsonc
"agent_updates": false                              // every agent frozen
"agent_updates": { "*": true, "claude": false }     // all but claude
```

Absent, it is `true`. **The key is a PACK name, not a bin name** — one pack may declare more than
one program, and the unit a user reasons about is the pack they selected. **A specific key beats
`"*"`**; `"*"` beats absence. **The default direction is OPEN**, which inverts the nearest precedent
in the tree (`host_apply_on_launch` fails closed) — a faithful copy of that file would silently
freeze every agent.

**User scope is not a stylistic choice**: a workspace config travels with the
repo and is agent-editable, which is the stated reason `packs` is read from `paths.UserConfigPath()`
directly rather than from the merged config (`internal/config/packs.go`). A key that lets whatever
is running in the jail freeze its own updates belongs to the same family, and a workspace-scoped
spelling would hand an agent the switch. **A pack may not opt itself out** — the pack declares how
to update (OQ-PD14), never whether to.

**2. MCP and LSP servers ARE in the class, and the mechanism has a second call site.** §3.5's
definition includes them, and its own test confirms it rather than merely permitting it: nobody's
build reproduces against `pyright`, and a six-week-old language server is a defect. But they are
**not** pack `program` contributions — they arrive through the bootstrap
(`YOLO_MCP_NPM`, `YOLO_LSP_NPM_INSTALL` in `internal/entrypoint/shell.go`), sentinel-tracked by
`~/.yolo-installed-lsps` and uninstalled when dropped from config. So evergreen has **two**
integration points, not one — and the second is **transitive**, which is what decides its trigger.

**RULED 2026-09-03: a server inherits the trigger of the agent that connects to it.** An MCP or LSP
server exists only to serve an agent; if no agent runs, nothing needs it current. So the refresh
happens when an agent whose config names that server is invoked — the same lazy moment as the agent's
own update, not a boot step. This was briefly written as eager-at-boot on the reasoning that "nobody
types their name"; the reasoning was right and the conclusion did not follow, because a transitive
dependency does not need a trigger of its own.

**Two things narrow the work, both verified 2026-09-03:**

- **Only the servers yolo INSTALLS are in scope.** An MCP entry whose argv is `npx -y <pkg>@latest`
  resolves on every spawn and is already current — measured here as
  `YOLO_MCP_SERVERS={"tavily": {"command": "npx", "args": ["-y", "tavily-mcp@latest"]}}`. That is
  [§6.1](#61-three-tiers-of-control--the-answer-to-what-about-a-mechanism-we-cant-control)'s
  **unmanaged** tier, and refreshing it would be inventing management this design deliberately
  declines. What freezes is the bootstrap-installed set (`YOLO_MCP_PRESETS` — chrome-devtools,
  sequential-thinking) and the LSP recipes, sentinel-tracked by `~/.yolo-installed-lsps`.
- **"Which servers does this agent connect to" is already computed.** MCP config is a **per-agent
  surface** (`yolo config ls` lists `agy/mcp` in its own right; claude's block lives in
  `~/.claude.json`, codex's in `~/.codex/config.toml`), so the set is a rendered fact rather than
  something the update path must infer.

**Stamps are per package, not per agent**, so two agents sharing `pyright` do not both pay: the first
invocation refreshes it, the second sees a fresh stamp. The sentinel keeps its removal job, which is
orthogonal to freshness.

> [!NOTE]
> **Where the walk lives is an implementation choice with one constraint.** Iterating a per-agent
> MCP/LSP set is JSON work, and the launcher templates are bash; the natural shape is for the
> launcher to call one throttled step that does the walk in Go and then `exec` the agent. The
> constraint is ordering, not language: **the refresh must complete before the agent is exec'd**,
> because the agent spawns its servers itself and a half-updated set at connect time is worse than
> a stale one.
>
> **ABSENT and STALE resolve differently, and the rule that looks like it covers both does not.**
> "Never block when a working binary is present" is about *staleness* — a server already installed
> is used as-is if the refresh times out. A server the agent's config names and that is **not
> installed at all** has no working copy to fall back to: it is installed synchronously, and a
> failure there is reported rather than swallowed, because the agent is about to try to connect to
> something that is not there. Same distinction as the agent's own cold start.

**3. One writer per install prefix, and only one backend can contend.** On `podman` and `container`
the prefixes (`~/.local`, `~/.npm-global`, `~/go`) are **per-workspace binds**, so two simultaneous
launches write different directories and cannot collide. **`macos-user` is the exception**: its home
is one persistent, machine-constant `/Users/_yolojail` shared by every workspace and every session
(`internal/macosuser/macosuser.go:53`) — deliberately, since the single home *is* its
shared-credentials mechanism. Two agents invoked at the same moment there would run vendor updaters
against the same prefix.

> [!IMPORTANT]
> **The contention rule, and the one thing it must not do.** Updates serialize on a lock held at the
> install prefix. An invocation that cannot take the lock **proceeds WITHOUT updating** and says so.
> It must **not** wait long and must **not** fail: the user typed an agent's name, and making them
> wait — or refusing — because another shell is mid-update would be worse than running the version
> already on disk. Under B2 this is cheap to state because the failure is already scoped to one
> command; the same rule under eager-at-boot would have had to argue its way out of a jail-level
> fatal.

**4. `yolo pack update` stays, with a smaller job.** It is no longer the only way an agent moves, but
it is still the way to move one **now** — without restarting the jail — and the only way to refresh
a pack whose `agent_updates` is `false`. Its current npm-only restriction
(`internal/cli/packupdate.go:141` skips every non-`npm` kind) goes away with OQ-PD14's declared
verb; it walks the same set the launchers do.

> [!NOTE]
> **Under B2, "evergreen" is bounded by INVOCATION, which is the bound you want.** An agent you run
> daily is current daily; one you have not run in a month updates the next time you run it, before
> it starts. A jail left running for weeks is no longer a problem, because the launcher mediates
> every invocation rather than only the boot. **This is strictly better than the restart-bound
> semantics eager-at-boot would have given**, and it is the second reason to prefer B2 after cost.

##### What runs the update — the launcher itself, and what that deletes

*This subsection replaced an earlier one that had boot invoking launchers by absolute path. That was
written against the eager-at-boot shape and is wrong under B2: **boot invokes nothing.** The earlier
version is preserved in git (`5930f6a5`); what follows is what survives it.*

**The launcher runs the update, in its own process, when the user invokes the agent.** Nothing needs
to reach into it. That deletes most of what eager-at-boot required:

| Eager-at-boot needed | Under B2 |
| :--- | :--- |
| a new boot genStep | **gone** — no boot step at all |
| careful ordering after catalog + reconcile | **gone** — they observe at boot, the update happens later, so their "disk = last launch" premise holds by construction |
| ordering after `GenerateCABundle` | **gone** — the launcher runs long after boot, with the bundle already in the environment |
| a jail-level fatal and its escape hatch | **gone** — failure is scoped to one command |

**What survives, and is still the work:**

1. **The native template has no update mode.** `nativeLauncherTemplate` only calls
   `"$REAL_BIN" install` on an hourly stamp — no `--force`, no target, so it is a **no-op when
   already installed**. It needs a real update branch running the declared verb.
2. **PATH order changes**, and the generation-time collision check comes with it (above).
3. **`yolo pack update` keeps a smaller job** and stops being npm-only
   (`internal/cli/packupdate.go:141`). It already invokes launchers by absolute path with
   `YOLO_PACK_UPDATE=1` (`internal/cli/packupdate.go:108-115`) and already dispatches off the
   **manifest** rather than the directory listing — reading `HonoredInstalls`, so the origin gate
   applies to a refresh as it does to an install. That machinery is correct and stays; only its
   `via`-filter goes.

> [!NOTE]
> **Vocabulary, because the two dirs blur and both come out of `shims.go`.** A **shim** is a
> *blocker* — `~/.yolo/bin/block/{grep,find}`, generated by `GenerateShims`, **first** on PATH
> because interception is its job. A **launcher** is the lazy installer/updater —
> `~/.yolo/bin/launch/*`, generated by `GenerateAgentLaunchers`. B2 moves the launch dir earlier but
> never ahead of the blockers.

##### Per-agent facts, verified 2026-09-03

Everything [OQ-PD13](#decision-ledger) and [OQ-PD14](#decision-ledger) need in order to say which
pack changes how. The three left unverified when §3.5 was first written are closed here.

| Pack | Today | Native installer | Pins? | Self-updates once native | Update verb |
| :--- | :--- | :--- | :--- | :--- | :--- |
| `claude` | installer | `claude.ai/install.sh` | ✅ `claude install <ver>` | ✅ (once the captured `DISABLE_AUTOUPDATER` is gone) | `claude install` |
| `agy` | installer | `antigravity.google/cli/install.sh` | not checked | ✅ — native ELF carrying `AUTO_UPDATE`/`Auto-update` strings | `agy update` |
| `copilot` | **npm** ⛔ | `gh.io/copilot-install` | ✅ `VERSION=` | ✅ — the `isSea()` gate flips true | `/update`, or re-run the installer |
| `codex` | **installer** ✅ | `chatgpt.com/codex/install.sh` | ✅ `CODEX_RELEASE=` | machinery present (`autoUpdateEnabled`, `managedCodexVersion`); **empirically off** — 0.145.0 since 2026-07-25 | `codex update` |
| `opencode` | **npm** | `opencode.ai/install` (HTTP 200, bash) | ✅ `VERSION=` | not checked | not checked |
| `pi` | **npm** | `pi.dev/install.sh` | not checked | ❌ — no auto-updater found in the shipped `dist/` | `pi update --self` |

**`pi` is the case that proves the verb is needed.** It has a native installer and no auto-updater
at all, so it is evergreen *only* through the boot-path update calling `pi update --self`. A design
that assumed "native installer ⇒ self-updating" would leave exactly one agent frozen and look
correct everywhere else.

> [!IMPORTANT]
> **SHIPPED 2026-09-04, and only `codex` flipped.** The ✅/⛔ marks in the *Today* column above are
> the outcome of [OQ-PD13](#decision-ledger)'s implementation. `codex` is the one clean case: its
> installer takes `${CODEX_INSTALL_DIR:-$HOME/.local/bin}` with no root branch, so its default
> landing path is exactly the native launcher's hardcoded `REAL_BIN="$HOME/.local/bin/$BIN"`
> (`internal/entrypoint/shims.go`).
>
> **`copilot` did NOT flip, and the reason is a THIRD constraint neither this table nor the plan
> had a column for: the installer's prefix depends on the UID it runs as.**
> `gh.io/copilot-install` reads
> `if [ "$(id -u)" -eq 0 ]; then PREFIX="${PREFIX:-/usr/local}"; else PREFIX="${PREFIX:-$HOME/.local}"; fi`,
> then `exit 1`s when it cannot `mkdir -p "$PREFIX/bin"`. **A container-backend jail runs as root**
> (`flake.nix` sets `USER=root`) **under an unconditional `--read-only` rootfs**
> (`internal/cli/run/assemble.go`), so `/usr/local` cannot even be created: measured in-jail
> 2026-09-04, `mkdir -p /usr/local` → `Read-only file system`. The flip would therefore make
> copilot **uninstallable**, not merely misfiled — and it cannot be repaired from the manifest,
> because `packdecl.Install` has no way to pass `PREFIX=` (see the *Don't* in
> [`native-installer-migration.md`](../plans/native-installer-migration.md), which is also why
> this is not fixed here).
>
> ⚠ **[OQ-PD14](#decision-ledger)'s update verb landed 2026-09-04 and did NOT open that door.**
> It added `Contribution.Update` / `Install.UpdateVerb` — an argv for the installed PROGRAM —
> and needed no environment for the installer, so no `env` field was added and copilot's flip
> still waits on one. Adding a field to that struct is routine now; what is undecided is the
> flip itself, which is a separate call with the measurement above under it.
>
> **The general rule the copilot case yields, worth applying to any future flip:** *self-updates
> once native* is a necessary condition and never a sufficient one. The sufficient one is **does
> the installer's DEFAULT prefix, under the UID and filesystem the jail actually runs with, equal
> the launcher's `REAL_BIN`** — three facts, of which this table's columns capture none.
> `internal/entrypoint/nativelauncher_test.go`'s
> `TestNativeLauncherReportsAnInstallerThatLandsNothing` pins both failure shapes.

#### Out of scope, stated so nobody builds it

**A project's own package manager is not yolo's business.** A repo's `package.json`, `go.mod`,
`Cargo.toml` or `requirements.txt` is resolved by that project's tooling with that project's
lockfile, and yolo neither pins nor updates it. yolo is responsible for what it *installs* — the
toolchain, the system packages, the agents — and the boundary is exactly that.

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

> [!IMPORTANT]
> **RE-MEASURED 2026-09-03, and this is the finding that reopened the doc.** The same workspace,
> ten days later, against what the registry serves today:
>
> | Package | On disk | Installed | Latest |
> | :--- | :--- | :--- | :--- |
> | `@github/copilot` | 1.0.48 | 2026-07-19 | **1.0.82** |
> | `@openai/codex` | 0.145.0 | 2026-07-25 | **0.153.1** |
> | `@earendil-works/pi-coding-agent` | 0.82.1 | 2026-07-25 | **0.84.4** |
> | `claude` (installer class) | 2.1.220 | 2026-07-24 | — |
>
> **Six weeks, every agent, including the installer class.** §4.1 predicted freeze; freeze is now
> the observed steady state of the whole fleet. Two mechanisms that were supposed to prevent it were
> each measured inert: the launcher is PATH-shadowed ([OQ-PD8](#decision-ledger)), and
> `"$REAL_BIN" install` — the installer launcher's hourly call — carries no `--force` and no target,
> so it is a **no-op when the program is already installed**. It was never an update.
>
> A third mechanism froze claude specifically and belongs to neither: `env.DISABLE_AUTOUPDATER=1`,
> captured from a one-time in-jail edit into `<workspace>/.yolo/prism/claude-settings.overlay.json`
> and outranking both the pack and the host layer on every boot since 2026-08-05. It is
> **per-workspace**, so it froze one jail and no other. Not a delivery defect — a config-capture one
> — but it is why claude's freeze looked like the vendor's doing. **Cleared 2026-09-03** from both
> the overlay and the surface file; `yolo config render claude --explain` now attributes `env` to no
> layer at all. **And the fix is confirmed by the outcome, not just by the render:** claude had sat
> at 2.1.220 since 2026-07-24, and self-updated to **2.1.260 at 21:44 the same evening the capture
> was cleared** — six weeks frozen, current within hours. That is the single strongest piece of
> evidence in this document that the freeze was configuration and not delivery.
>
> ⚠ **And the pack's own switch for this is DEAD CODE — the conclusion held, my first explanation
> of WHY did not.** `packs/claude/pack.json:60` renders `preferences.autoUpdaterStatus: "disabled"`
> as a **managed** key, so it wins its layer on every boot, and Claude Code ignores it. **The reason
> is the wrapper, not a retired probe:** the string `"preferences"` appears **zero** times in the
> shipped binary — measured in both 2.1.220 and 2.1.260 — so a key nested under it is unreachable
> whatever its name. `autoUpdaterStatus` itself does have a live reader, but it is a migration over
> `~/.claude.json`'s global-config object, a different file. *(An earlier version of this note
> blamed a `tengu_dead_probe_autoupdater_status` telemetry probe. That string is real in 2.1.220 —
> two hits — and gone by 2.1.260, so it was a straggler counter that has since been retired; it was
> never the reason the pack's key is dead.)*
>
> Removal is still correct, and it is narrow: delete `managed.preferences`, keep the surface, whose
> `retireOnFirstRender` is load-bearing.
>
> **The threat no-evergreen was built to stop never happened; the freeze it caused did.** That
> asymmetry is the whole argument for [§3.5](#35-the-second-axis-who-the-dependency-serves-amendment-2026-09-03).

### 4.2 Drift: mise is machine-global, evergreen every launch, and repoints aliases in place

`setupScript` ran `mise install --quiet && mise upgrade --yes` **unconditionally on every launch**
until `a16403e2` removed the upgrade — OQ-PD3's ruling landing; the launch now installs only, and
a workspace `mise.lock` governs resolution when present (`internal/cli/run/command.go:15-25`). The
diagnosis below is preserved because the store's shape is unchanged and the fossil record is the
evidence for everything else in this section. The store is a single bind —
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
> other workspace's launch can move. Until `a16403e2`, `mise upgrade --yes` ran on every launch, so
> the mutation was once per launch, per workspace, against shared state; this repo now also commits
> a `mise.lock`, so its own resolution no longer goes through the aliases at all. The failure the
> old cadence produced is the nastiest kind — a
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
bootstrap (`internal/entrypoint/shell.go:381`), keyed on the `~/.yolo-installed-lsps` sentinel
(`shell.go:312`). *(Anchors repinned 2026-09-02 — the receipts feature grew this file.)*

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
| npm programs, installer programs, Go bins | **per workspace** | `<ws>/.yolo/home/{npm-global,local,go}` (`assemble_parts.go:108-110`) |
| **the npm download cache** | **machine-global** | `NPM_CONFIG_CACHE=$HOME/.cache/npm` (`shims.go:349`); `~/.cache` is `paths.GlobalCache()` (`assemble_parts.go:120`) |
| **the update stamps and spec records** | **machine-global** | `~/.cache/yolo-agent-stamps` (`shims.go:190`) — beside a per-workspace `REAL_BIN` |
| **mise tools** | **machine-global, mutated in place** | `/mise` (`assemble_parts.go:156-161`) |
| the base home | machine-global, **read-only** | `paths.GlobalHome()` mounted `:ro` (`assemble_parts.go:107`) |
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
| the reconcile loop | the LSP sentinel's install **and uninstall** (`shell.go:312-381`) |
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

> [!WARNING]
> **AMENDED 2026-09-03 — this verdict now holds for PROJECT dependencies only.** For **agent**
> dependencies the rejected half is adopted: a launch *does* reinstall, and a registry it cannot
> reach *does* refuse the launch
> ([§3.5](#35-the-second-axis-who-the-dependency-serves-amendment-2026-09-03),
> [OQ-PD12](#decision-ledger)).
>
> **Both objections turned out to be answerable without contradicting either.** *"A launch must not
> depend on a registry being reachable"* — **it does not.** Under B2 ([OQ-PD12a](#decision-ledger))
> the update happens when the user invokes the agent, so a launch that starts no agent touches no
> registry, and an offline invocation of an installed agent just runs it. *"An install is not free"*
> — it is not, so it is paid by whoever asked for it rather than by every boot. An earlier ruling
> the same day put the update on the boot path and had to buy its way past both objections with a
> jail-level fatal and an escape hatch; the lazy shape simply does not incur them.
>
> **Reconcile is untouched and still the answer for project dependencies** — it stays offline, it
> still only reports, and it did not move.

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

**Paid (`0a4d241c`).** The `via` field is a **closed two-value set** — `validateContribution`
rejects anything but `npm` and `installer` — and until the tolerance landed, the two halves of the
system disagreed about a third value: `DecodeTolerant` skipped unknown *kinds* but validated known
ones, so an unknown `via` **value** on kind `program` was a validation problem, and the boot path
treats any problem as fatal — a pack declaring `via: "uv"` staged for an older baked entrypoint
was a refused boot. Now `DecodeTolerant` drops a `program` whose non-empty `via` it does not know,
with a skip note (`unknownViaSkip`, beside the unknown-kind rule it mirrors), strict `Decode`
still refuses loudly, and an **empty** `via` stays fatal on both paths — a program naming no
mechanism is a defect both ends of the version boundary agree on.

Two boundaries survive the payment. **The tolerance protects only images baked after it**: a third
`via` value must still wait for a `just load` on every host that will see it, because the jail
refusing the boot runs the *previous* image. And the sibling closed enums (`state.scope`, hook
names, `skills_tier`) carry the identical future-skew shape and are deliberately not widened —
each needs its own answer when its third value arrives.

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
materialize (per jail, offline):     REFLINK the capture into the home (see the amendment below)
update:                              a NEW capture, on an explicit act — never in place
remove:                              delete the materialized tree; CAS entry GC'd when unreferenced
```

**AMENDMENT, 2026-09-04 — *materialize* is REFLINK, and the CAS is MOUNTED**
([`install-capture.md`](../plans/install-capture.md) slice 4). The line above read
*"unpack/hardlink the capture into the version-addressed path"*, and the hardlink half cannot work
from where [§5.2](#52-a2--lazy-install-from-a-launcher-the-status-quo) requires materialize to
happen — inside the jail, from the launcher, so that "you pay nothing for a tool you never invoke"
survives. **`link(2)` compares the MOUNT, not the device.** MEASURED 2026-09-04 in this repo's own
jail: a hardlink from one bind of a btrfs into another bind of the same btrfs (identical `st_dev`)
returns `EXDEV`. The store is a host directory a jail can only reach through a bind, so wherever it
is mounted is one more mount and the hardlink is refused every time.

**`FICLONE` compares the FILESYSTEM** — the kernel's clone path refuses only when the two inodes
have different superblocks, and every bind of one filesystem shares one. That is exactly the gap
`link(2)` falls into. Measured the same day in a real podman container, against the two mounts a
materialize actually uses — a `:ro` bind of the store at `/ctx/captures` and a rw bind of a home
surface at `/home/agent/.local`, both on one btrfs:

| operation | result |
| :--- | :--- |
| `FICLONE` store → home, 256 MiB | **OK** — 3 ms, 32 KiB of new space, destination is its own inode (`nlink` 1) |
| `link(2)` store → home | `EXDEV` |
| `cp` store → home, 256 MiB | 98 ms, 262 MiB of new space |

So the verb is a three-step chain, each step measured at the moment it is used rather than
predicted from a mount table: **reflink → hardlink → copy**. Hardlink stays because it wins where
the store and the home share a mount; copy stays because ext4 has no reflink at all, which makes it
a path real machines take and is why it is loud and names both filesystems.

Three consequences this section did not anticipate. **The CAS is mounted into every jail**, `:ro` at
`/ctx/captures` — the *"no new containment machinery"* paragraph below assumed materialize needed no
mount, and it needs exactly one. **An installer that opens a materialized file for write is no
longer a machine-wide hazard**: a reflinked file is its own inode, so the write copies-on-write and
reaches nobody, where a hardlinked one would have been the running program's bytes in every
workspace at once. Entry files are still frozen read-only at admit, which is what keeps the
hardlink arm safe. And **the GC's reference oracle cannot be `st_nlink`**: a reflinked file leaves
the entry's link count at 1 while very much referencing it. That is a blocking correction to the
remove step above, recorded in the plan's slice 5.

**The prior art is Arch's.** An AUR `PKGBUILD` runs upstream's opaque payload in a clean chroot
(`makechrootpkg`), and the *output* is an ordinary pacman package with a file manifest — the
package manager never trusts the build script's environment, only its captured product. The pack's
`program via installer` contribution is already the PKGBUILD analogue: a name and a URL. Nix's
fixed-output derivations and `docker commit` are the same shape from other directions.

**The sandbox already exists, and it is yolo's own product.** On the container backends a capture
is an ephemeral jail whose per-workspace home binds start empty: the existing bind surfaces
(`~/.local`, `~/.npm-global`, `~/go` — `assemble_parts.go:108-110`) are natural capture surfaces,
so after the installer runs, **the bind-dir contents ARE the delta** — tar, hash, done. No new
containment machinery; a jail that writes outside its binds is a finding the capture run reports,
and a tool that does so is flagged genuinely unmanageable instead of silently half-captured.

**On `macos-user`, the same two properties come from different machinery** (READ FROM CODE — this
backend's installer pipeline is itself unverified on hardware, per
[`macos-user-nix-and-features.md`](macos-user-nix-and-features.md), so this is design against read
code, not a measurement). That backend has no binds and no ephemeral home: the jail home is one
persistent, machine-constant `/Users/_yolojail` shared by every workspace and every session
(`internal/macosuser/macosuser.go:52-53`, `internal/cli/run/run.go:235-250`) — deliberately, since
the single home *is* its shared-credentials mechanism and splitting it is a refused design point
(the same lines). ⚠ *This sentence cited `run.go:156-159` for the shared home until 2026-09-04;
that range is the profile/provider channel composition and says nothing about a home. The refusal
is at `:235-250`, which the next clause already cited — one range, cited twice, wrong once.* But a
capture does not need a bind; it needs a **fresh, enumerable, kernel-bounded write surface**, and
both control points are already this backend's own machinery: the Seatbelt profile is generated
fresh per session (`internal/macosuser/seatbelt.go:47-55` — `deny file-write*` on `/`, then an
explicit allow-list) and the launch is `env -i` under `sandbox-exec` (`macosuser.go`'s
`LaunchArgv` — cited as `:328-377` until a 2026-09-04 refactor shortened it, which is why it is
named rather than numbered now). So a capture run sets `HOME` to a fresh staging directory and
carries a narrowed profile in which that directory (plus `/tmp` and `/var/folders` scratch) is the
**only** writable path. The persistent home is denied for the duration — a capture cannot
touch the shared credential store — and an installer that writes elsewhere is refused by the
kernel up front, a sharper escape signal than a container overlay that silently swallows the stray
write. The one genuinely new problem is **relocation**: the staging path is not the final home
path, and installers embed absolute self-references (claude's `~/.local/bin/claude` is an absolute
symlink into its versions directory — MEASURED in this jail), so the manifest must record prefix
references and materialization must rewrite them — the move Homebrew bottles made routine on
exactly this OS — or flag the tool non-relocatable. On the container backends this problem is
absent by construction: the capture home and the materialize home are the same `/home/agent`.

**Two things the paragraph above did not foresee, found while building it** (2026-09-04,
[`install-capture.md`](../plans/install-capture.md) slice 6). *The staging tree cannot live
where the container backends' does.* `<CapturesDir>` is under the INVOKING user's home, which is
precisely the home this backend isolates the sandbox uid from — and a writable subtree there
would also put the machine-wide CAS in reach of a program yolo is running for the first time. So
a capture stages on neutral ground (`/Users/Shared/yolo-captures`) and the host moves the
finished proto-entry into the store afterwards, refusing rather than copying if the two are not
on one mount. *And the staging home needs a bootstrap of its own*: the capture must run the
GENERATED launcher, which only exists in a home `darwin-bootstrap` has rendered into, so the
capture bootstraps its throwaway home exactly as a launch bootstraps the shared one.

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
- **Whether `packages:` should stay workspace-scope.** Ruled 2026-08-25 — it does (`image-staging`
  OQ-4, *"yes, has to be"*); fix the cost, not the scope.
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
  [`../plans/roadmap.md`](../plans/roadmap.md). *(This line used to also name `docs/tasks/`, a
  directory that has never existed in this tree — corrected 2026-09-02.)*

---

## 8. Where this sits against the sibling docs

### 8.1 `trust-paths.md` — what this RETIRES and what it INHERITS

> [!IMPORTANT]
> This document does not edit `trust-paths.md`. The dispositions below are proposed here and are
> applied there by whoever lands this.

- **RETIRES [OQ-TP4](trust-paths.md#decision-ledger) — *"where does an EMBEDDED pack's npm version get pinned?"*** —
  as posed. All three of its options (manifest / lockfile / user config) are venues *inside the pack
  system*, and the measurement says the question is not the pack system's alone: the identical
  question is live for mise (no pack), the LSP recipes (no pack), and claude plugins (no pack). It is
  superseded by [OQ-PD1](#decision-ledger) (where the receipt lives — now ruled) and
  [OQ-PD5](#decision-ledger) (also ruled). **What survives verbatim and must
  not be re-derived:** TP4's cost analysis of option (a) — pinning in the manifest makes yolo's
  release cadence the ceiling on agent-CLI freshness — is the same objection A1 hits in §5.1, and
  TP4's leaning toward the lockfile is preserved in A3's shape.
- **INHERITS [OQ-TP3](trust-paths.md#decision-ledger)'s still-open half** — *is yolo's own embedded pack required to
  pin, and is a fetched pack required or merely permitted?* Restated at wider scope and **ruled** as
  [OQ-PD6](#decision-ledger): once a receipt exists, a declaration need not carry a pin, because
  the receipt is the pin — which is what answers TP3's inherited half.
- **Unchanged by this doc:** OQ-TP5's ruling and OQ-TP6's fatal. §5.2's finding that the launcher is
  PATH-shadowed after first use does not dispute the ruling — it showed the *reporting* half it
  built never executes, which is now settled ([OQ-PD8](#decision-ledger)): the channel moves to the
  boot catalog and the update verb.
  [OQ-PD3](#decision-ledger)'s ruling *extends* OQ-TP5 rather than touching it: no-evergreen is now
  a principle covering every resolver, not an npm fix.

> [!IMPORTANT]
> **SUPERSEDED 2026-09-03 by the amendment.** The bullet directly above is the one claim in this
> section the amendment reverses: **OQ-TP5 is no longer unchanged — it is superseded.** Its four
> packages (pi, copilot, codex, opencode) are all agent dependencies, so *"no evergreen npm"* is
> now false of every member it had ([§3.5](#35-the-second-axis-who-the-dependency-serves-amendment-2026-09-03),
> [OQ-PD12](#decision-ledger)). OQ-TP6's fatal is genuinely untouched — it is about consent, not
> cadence.
>
> **The dispositions this section proposed have now been applied in `trust-paths.md`** (2026-09-03),
> which is what the note at the head of §8.1 asked whoever landed this to do. OQ-TP3 and OQ-TP4 are
> retired there into its Decision Ledger with pointers here; OQ-TP5's row is marked superseded
> rather than deleted, and its `§1 row 1` anchor is preserved with a superseding note **because six
> code sites cite it** (`internal/cli/pack.go:190`, `internal/cli/packupdate.go:9`,
> `internal/entrypoint/shims.go:622` and `:729`, plus two tests). Those comments describe code that
> still behaves as written; they become wrong only when the evergreen work lands, and that is the
> commit that must update them.

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
| R6 | **The closed `via` enum turns the next mechanism into a boot refusal** on any pre-`just load` image (§6.2). | **Paid** (`0a4d241c`): skip-and-report under `DecodeTolerant`, refuse loudly under `Decode`. Residual: the sibling enums named in §6.2, and the `just load` ordering. |
| R7 | **Uniformity borrowed from a lockfile is a function of that lockfile's quality** — and §5.6 generalises the borrowing to every native lock we adopt. The core is measured (a workspace-local `mise.lock` governs resolution against the shared store); **NOT MEASURED**: format stability across mise's own upgrades, and full-launch behaviour under `MISE_LOCKFILE`. | Treat tier 2 as "record and compare" until the launch path is measured; do not promise obedience we do not own. |
| R8 | **Every measurement is from one machine and one home.** The dates in §4.1 are this jail's history, not a general law. | The *mechanism* (cold-branch `@latest`, shared aliases, absent removal) is read from code and generalises; the dates are illustrative and labelled. |
| R9 | **A repo-committed lock imports the repo's cadence.** Version bumps become PRs — reviewable and bot-automatable, which is the point, and a chore — and repo-less or ephemeral workspaces have no home for the project half. | Bots own the bump PRs; the user-scope half of §5.6's table is the fallback; a missing lock degrades to today's A2 behaviour plus a report line, never a refusal. |

---

## 10. What I would build, in order

**First, write the receipt for the managed tier only. — SHIPPED** (`af46c9b4` the receipts,
`a16403e2` the mise half). Every install yolo runs — npm launcher, installer launcher, LSP
bootstrap — appends declaration, resolved identity (version or digest), act and time to
`<workspace>/.yolo/receipts.jsonl`. That is a **workspace-scope observation log beside the
realization**, deliberately: the user-scope pin OQ-PD1 names (`packs.lock.json`) is what install
will *obey*, and it arrives with the fifth step, where obeying starts. The mise half needed no
building ([§5.6](#56-a6--borrow-the-ecosystems-lockfiles)): `mise.lock` is committed, the launch's
`install && upgrade --yes` became install-from-lock, and this repo's own toolchain no longer
resolves through the shared aliases.

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
by default. **The catalog half is SHIPPED** (`af46c9b4`) — and its first real boot named **five**
orphans in this workspace, not the two §4.3 measured: `pyright`, `typescript` and
`typescript-language-server` survive from a since-unconfigured `lsp_servers`, their sentinel
record lost — a live instance of the record-and-bytes divergence this whole design exists to
close. The removal act and the autoprune option remain.

**Fifth, wire the ruled enforcement** ([OQ-PD6](#decision-ledger), [OQ-PD7](#decision-ledger)): the
receipt is the pin, and it reports before it gates — by this point the receipts say how much
divergence there actually is, so any later gate is designed against a measured distribution. mise
is the exception to this sequencing: its lock records and obeys in one mechanism
([§5.6](#56-a6--borrow-the-ecosystems-lockfiles)), so it ships with the first step — what remains
here is the same wiring for everything else.

**Sixth as written — now SEVENTH and TRAILING, per [OQ-CP1](agent-cli-copies.md#-oq-cp1--is-the-disk-justification-retracted-and-is-oq-pd15-reversed--resolved-2026-09-04) (2026-09-04) — the installer capture** ([§6.3](#63-installers-that-just-do-whatever-capture-the-install-then-treat-the-capture-as-the-package),
ruled — [OQ-PD10](#decision-ledger)): it slots in as the installer resolver's implementation of
*record* + *materialize* and depends on nothing above except the receipt schema. **Slices one
through four are landed** ([`install-capture.md`](../plans/install-capture.md)). Slices one to
three changed nothing about a normal launch — a capture happened only when a human ran
`yolo capture <bin>`. **Slice four is where a launch changes**, and it is the slice that pays.

Slice one is substrate: `internal/treedigest` (the canonical tree digest, lifted out of
`hostskills`), `paths.CapturesDir()` and its boot `MkdirAll`, and `internal/capture`'s store —
admit by rename out of an in-store staging dir, entry files frozen read-only, completion marker
written last, and a strictly offline `Resolve`.

Slice two is the **inner driver**, `yolo internal capture-run` (`internal/capture/inner.go`): walk
a baseline of the three per-workspace program surfaces, run the installer, move everything it added
or changed into a scratch tree, and write the delta manifest beside it. It runs INSIDE for the
reason this section's sandbox paragraph implies but does not say: the boot writes into those same
surfaces before any installer does, so a host-side before/after diff would file yolo's own
bootstrap output as the vendor's. That also makes it backend-neutral in the strongest sense — a
process with a `HOME`, a scratch dir and an argv — which is what makes Apple Container work at all,
since that backend has no per-directory binds whose contents could BE the delta. One measurement
from building it corrects the plan's cost model rather than this section's: `rename(2)` compares the
MOUNT, not the device, so two bind mounts of one filesystem still fail `EXDEV`; the driver falls
back to a copy and reports it rather than paying for the bytes twice in silence.

Slice three is the **host act**, `yolo capture <bin>` (`internal/cli/capturehost.go`): resolve the
declaration through `packload.HonoredInstalls`, stage a scratch workspace inside the store, run the
ORDINARY run pipeline against it with the driver as the command, admit the finished proto-entry, and
append a `kind:"capture"` receipt beside it. Three things it settled that this section left open.
The scratch dir's siting is decided by `rename(2)` comparing the MOUNT: the capture workspace is
`<CapturesDir>/staging/<bin>`, and the driver reaches the surfaces through the WORKSPACE bind
(`/workspace/.yolo/home/<subtree>`) rather than through `$HOME`, because only that view shares a
mount with the scratch dir — MEASURED in a nested jail 2026-09-04, the same directory renaming
through one path and copying through the other. The installer it runs is **the generated launcher**,
under a new `YOLO_INSTALL_ONLY=1`, so a capture records exactly what a launch would have installed
and the tool is never executed into the surfaces being captured. And yolo's own state dir
(`~/.local/share/yolo-jail`, inside the `.local` surface) is excluded from every delta, or a
launcher's receipt append would be filed as the vendor's and hardlinked into every workspace.

Slice four is **materialize**, and it is where the subsystem starts paying: the machine store is
bound `:ro` into every jail at `/ctx/captures`, the boot bakes that path into each generated native
launcher, and `_do_install` now tries `yolo internal capture-materialize` BEFORE it downloads —
falling through to the vendor installer on any miss, because making a capture mandatory for this
class is a behaviour change [OQ-PD7](#decision-ledger)'s "report first; gate later" does not
license. The mechanism is the reflink chain the amendment above records, and the acceptance test is
two workspaces and one download (`integration/capturematerialize_test.go`). Two decisions worth
naming. The lookup from `(bin, platform)` to a content address is a **scan of each entry's own
receipt**, with no index: the question is asked once per program per workspace from a cold install
branch, and an index is a second record that admit and the GC would both have to keep true while
the receipts cannot go stale relative to the entry they live inside. And the CAPTURE JAIL is the
one launch that does NOT get the mount — the installer a capture runs is that same launcher, so a
store in reach would let it materialize the previous entry and record it as a fresh capture, which
would also make *update* ("a NEW capture, on an explicit act") impossible.

**Seventh as written — now SIXTH, and SHIPPED 2026-09-04, per
[OQ-CP1](agent-cli-copies.md#-oq-cp1--is-the-disk-justification-retracted-and-is-oq-pd15-reversed--resolved-2026-09-04)
— make agent dependencies evergreen**
([§3.5](#35-the-second-axis-who-the-dependency-serves-amendment-2026-09-03),
[OQ-PD12](#decision-ledger)–[OQ-PD14](#decision-ledger)). Four parts, and only the first is
mechanically interesting: **un-shadow the launcher** — move the launch dir ahead of the install
prefixes and add the generation-time collision check ([OQ-PD12a](#decision-ledger)); add the
pack-declared update verb; flip the agent CLIs that have a native installer off npm
([OQ-PD13](#decision-ledger)); give the native template a real update branch, replacing the
`"$REAL_BIN" install` no-op. The `agent_updates` knob comes with it, and so does
[A7's V-axis prune](agent-cli-copies.md#51-a7--prune-stale-versions-executed-by-whoever-installed-the-new-one).
**The MCP/LSP half rides the same trigger** — a server is refreshed when an agent that connects to
it is invoked (§3.5), so there is **no boot step anywhere in this design**.

> [!IMPORTANT]
> **SHIPPED 2026-09-04, except the MCP/LSP half.** In the tree: the PATH reorder and its
> generation-time collision check; the declared update verb and the three packs that carry one;
> real update branches in BOTH launcher templates (the npm one's `_poll_and_report` is deleted),
> each with a bounded attempt, a non-blocking install-prefix lock, a re-entry guard and the baked
> policy; the `agent_updates` key with its three wiring sites; A7's prune; and `yolo pack update`
> walking every kind rather than only npm.
>
> **What is ruled and NOT built: the MCP/LSP transitive refresh.** A yolo-installed MCP or LSP
> server still moves only when the bootstrap reinstalls it. The ruling — a server inherits the
> trigger of the agent that connects to it — is unchanged and its scope is already narrowed to the
> bootstrap-installed set; what is missing is the throttled step the launcher would call before
> `exec`. The agent CLIs themselves are evergreen without it.

> [!IMPORTANT]
> **This step was deliberately LAST and is now FIRST — reversed 2026-09-04 by
> [OQ-CP1](agent-cli-copies.md#-oq-cp1--is-the-disk-justification-retracted-and-is-oq-pd15-reversed--resolved-2026-09-04).**
> Nothing above it was ever a technical dependency; evergreen needs only the receipt schema.
> [OQ-PD15](#decision-ledger) had put it last on a disk argument, and both halves of that argument
> measured false: capture collapses **N** while evergreen multiplies **V**, and *"under capture there
> is nothing to prune"* is wrong because the vendor's self-updater keeps writing full-size version
> dirs into the workspace whatever the store holds.
>
> **[A7's V-axis prune](agent-cli-copies.md#51-a7--prune-stale-versions-executed-by-whoever-installed-the-new-one)
> ships INSIDE this step**, not as a stopgap to be deleted later: keep-newest-K over the vendor's own
> version dir, executed by whoever installed the new one. That is where **1018.6 of 1223.4 measured
> MiB** are, it works on every filesystem, and it is a hard prerequisite of
> [OQ-PD18](#decision-ledger) — auto-capture seeds a superseded version into every new workspace and
> A7 is what deletes it.
>
> **What the reversal ends:** the freeze this ordering was carrying on purpose
> ([§4.1](#41-freeze-an-agent-cli-is-whatever-latest-meant-the-day-that-workspace-first-ran-it)) —
> agent CLIs no longer wait on capture.

**In parallel, pay the enum tolerance** (§6.2) — **PAID** (`0a4d241c`), while no one needed it,
which was the point.

---

## Decision Ledger

| ID | Ruling / Decision | Date | Settled in |
| :--- | :--- | :--- | :--- |
| OQ-PD1 | **Shape (d): the record follows the declaration's scope.** Ecosystem-native lockfiles at the declaration's home (repo for `mise.toml`/`flake.nix`, user for `packs`); yolo-written receipts only where no native lock exists; bytes in machine-global content-addressed stores. | 2026-08-24 | §5.6, §4.4 |
| OQ-PD2 | **Same-machine and same-workspace ship first, through the user half; same-declaration-anywhere is in scope for the project toolchain** via repo-committed native locks, and out of scope for user tools, which no repo should pin. | 2026-08-24 | §2, §5.6 |
| OQ-PD3 | **No-evergreen extends to mise.** The per-launch `mise upgrade --yes` was a stopgap; whatever pins mise ships as **part of the general seam** (a tier-2 resolver whose *obey* goes through mise's own pinning), never as a standalone lockfile flip ahead of it. ⚠ **NARROWED 2026-09-03:** the mise half stands, but *"it is a principle, not an npm fix"* does not — it does not reach **agent dependencies**, which are evergreen (§3.5, OQ-PD11/PD12). | 2026-08-24 · amended 2026-09-03 | §4.2, §6.1, §10, **§3.5** |
| OQ-PD4 | **Dropping a pack does not auto-delete its program.** Orphans are cataloged informationally at boot; removal happens only on an explicit act; autoprune exists as an option, **default off**. | 2026-08-24 | §4.3, §9 R3, §10 |
| OQ-PD5 | **One lifecycle, N resolvers, N native records under ONE READER** — no ledger-as-store of opaque identities; only a resolver parses its own record; the tiers stay explicit. | 2026-08-24 | §6 |
| OQ-PD6 | **The receipt is the pin.** A declaration may carry a version and is not required to; install obeys the record. Also answers OQ-TP3's inherited half. ⚠ **SCOPED 2026-09-03** to **project** dependencies. An agent dependency has no pin to obey; its receipt answers *what did I run*, never *what must I run* (§3.5). | 2026-08-24 · amended 2026-09-03 | §6, §8.1, **§3.5** |
| OQ-PD7 | **Report first; gate later only if the reports justify it** — and the record names where a gate would live. | 2026-08-24 | §6, §9 R1 |
| OQ-PD8 | **The launcher's informational poll is unreachable in steady state** (nineteen days of unmoved stamps); the "newer version available" channel moves to the boot catalog and the update verb / reconcile. | 2026-08-24 | §4.4, §10 |
| OQ-PD9 | **Native lockfile formats whenever one exists; a yolo-own repo lockfile only when the work demonstrates the need** — permitted, never preemptive. | 2026-08-24 | §5.6, §6.3 |
| OQ-PD10 | **Capture-and-repackage adopted for the installer class**, sequenced last: an ephemeral jail plus a snapshot of its fresh home surfaces, a plain filesystem artifact in the machine CAS, never an image layer. The receipt ships first; capture replaces its guess at "what the installer did" with a manifest. ⚠ **Resequenced twice.** 2026-09-03 by OQ-PD15 (capture before evergreen), then **REVERSED 2026-09-04 by [OQ-CP1](agent-cli-copies.md#-oq-cp1--is-the-disk-justification-retracted-and-is-oq-pd15-reversed--resolved-2026-09-04)** — capture trails again, because the disk claim that moved it was measured false. Its value is the manifest, offline materialize and drift reference, none of which is a disk property. | 2026-08-24 | §6.3, §10 |
| **OQ-PD11** | **A dependency serves either the AGENT or the PROJECT, and the class — not the delivery mechanism — decides its update policy.** Declared, never inferred from `via` or from the §6.1 tier. Stated as **P6**. | 2026-09-03 | [§3.5](#35-the-second-axis-who-the-dependency-serves-amendment-2026-09-03) |
| **OQ-PD12** | **Agent dependencies are EVERGREEN, updated LAZILY at the agent's own invocation** (revised the same day — see the row below). The launcher checks at most once per `UPDATE_INTERVAL` per program, then `exec`s; `agent_updates` (user-scope, per-pack or global) opts out; failure is scoped to the command, never to the jail. ✅ **SHIPPED 2026-09-04** for the agent CLIs, in both launcher templates; the MCP/LSP half of the same ruling is still unbuilt. | 2026-09-03 · shipped 2026-09-04 | [§3.5](#35-the-second-axis-who-the-dependency-serves-amendment-2026-09-03), §5.4 |
| **OQ-PD12a** | **B2 — the launch dir moves AHEAD of the install prefixes, and a launcher is generated only for a name the image does not provide.** The two halves are one decision: the position makes the launcher reachable past the cold start, the generation-time check keeps the position safe. ⚠ Converts "a pack cannot shadow `/bin/fzf`" from a structural impossibility into a handled case — it needs a test that fails when the check is deleted. Blockers stay first. **Supersedes the eager-at-boot shape ruled earlier the same day**, which cost a jail-level fatal, an escape hatch, three ordering constraints and an update of every agent on every launch — all deleted. **MCP/LSP servers inherit the trigger of the agent that connects to them** (transitive dependencies need no trigger of their own), so the design has **no boot step at all**; only yolo-INSTALLED servers are in scope, since an `npx -y pkg@latest` argv is already current every spawn. ✅ **SHIPPED 2026-09-04**: three PATH strings moved (`BootPath`, the `.bashrc` export, `macosuser.SandboxPath`) and the check is `internal/entrypoint/launchercollision.go`, scoped to the image dirs plus the DECLARED mise tools and never to the install prefixes | 2026-09-03 · shipped 2026-09-04 | [§3.5](#35-the-second-axis-who-the-dependency-serves-amendment-2026-09-03) |
| **OQ-PD13** | **Prefer the native installer over npm for an agent CLI wherever the vendor ships one.** An npm-installed CLI structurally cannot self-update — measured: copilot's updater refuses with *"Update not supported when running js directly"* — while the vendors' own installers both self-update and accept a version. **All four npm packs have one, verified 2026-09-03** (§3.5's table). | 2026-09-03 | [§3.5](#35-the-second-axis-who-the-dependency-serves-amendment-2026-09-03) |
| **OQ-PD14** | **The update verb is declared by the pack**, on the `program` contribution. Vendors disagree (`claude install`, `pi update --self`, `codex update`); core hardcoding one is how `yolo pack update` came to skip the installer class entirely (`internal/cli/packupdate.go:141`). Absent a verb, re-run the declared installer or `npm install -g`. ✅ **SHIPPED 2026-09-04** as `Contribution.Update` → `Install.UpdateVerb`, projected for every `via`; the npm-only skip in `yolo pack update` is gone. Declared by claude/agy/codex; the three npm packs use the `via` fallback deliberately | 2026-09-03 · shipped 2026-09-04 | [§3.5](#35-the-second-axis-who-the-dependency-serves-amendment-2026-09-03) |
| **OQ-PD15** | ~~**Capture FIRST — build the complete version and sequence toward it.**~~ ⚠ **REVERSED 2026-09-04 by [OQ-CP1](agent-cli-copies.md#-oq-cp1--is-the-disk-justification-retracted-and-is-oq-pd15-reversed--resolved-2026-09-04): EVERGREEN FIRST, with A7's V-axis prune inside it; capture trails and continues in parallel.** Both premises measured false: capture collapses **N** while evergreen multiplies **V**, and *"under capture there is nothing to prune"* is wrong because the self-updater keeps writing full-size version dirs into the workspace. **The disk justification is RETRACTED** — on ext4 capture ADDS a machine-wide copy and saves no disk at all. *"Sooner was never the goal"* still stands: this reverses which ruled subsystem goes first, not the scope of either | 2026-09-03 · reversed 2026-09-04 | [§10](#10-what-i-would-build-in-order), §6.3, [`agent-cli-copies.md`](agent-cli-copies.md) |
| **OQ-PD16** | **Jail-only here; the host notch is owned by [`noncontainer-nix-environment.md`](noncontainer-nix-environment.md)**, which has analysed it since 2026-08-02 and keeps six live questions on it. The mechanism is already built and already named for the axis: `flake.nix`'s `yoloNoncontainerPackages` buildEnv, whose only caller today is `macos-user`. ⚠ **Not a `devShell`** — `print-dev-env` puts the whole stdenv ahead of the host userland (that doc's §4.1) | 2026-09-03 | §3.5, [`noncontainer-nix-environment.md`](noncontainer-nix-environment.md) |
| **OQ-PD17** | **No unreferenced oracle — the reap rule is the COMPLEMENT OF THE RESOLVER.** Reclaiming a capture entry is never a correctness event (measured: a reflinked destination survives its source's unlink byte-identical), and `resolveCaptureFor` already picks *newest-by-receipt-time per (bin, platform)* — so every other entry is already unreachable by the only reader. Delete what the resolver would not select; `K = 1`. Retires all three candidates, **and** `K = 2` and the age floor, which this doc had proposed. ⚠ Surfaced [OQ-PD18](#decision-ledger): nothing populates the store automatically, so materialize has never hit on any machine. | 2026-09-04 | §6.3, [`agent-cli-copies.md` §4.2](agent-cli-copies.md#42-reclaiming-a-capture-entry-is-never-unsafe--which-reframes-oq-pd17) |
| **OQ-PD18** | **(d), DEFAULT ON — auto-capture on first launch, host-side, in the throwaway jail, no knob.** ✅ Its prerequisite is met: A7's V-axis prune shipped 2026-09-04 with evergreen. Nothing populated the store before this: `yolo capture` was its only writer, no launch path called it, and it had never been run. ⚠ **Default-on makes a stale entry actively harmful** — the workspace pays a copy AND a download, and is left holding a dead version the vendor updater will not remove — so **A7's V-axis prune is a prerequisite, not a companion**, and [OQ-CP4](agent-cli-copies.md#-oq-cp4--does-an-evergreen-update-get-to-materialize-from-the-store--resolved-2026-09-04) becomes load-bearing. On ext4 capture costs `+S` at every N and buys `N−1` avoided downloads; the ext4 share of real installs is the unmeasured number that would revisit the default. | 2026-09-04 | §6.3, [`agent-cli-copies.md` §4.1](agent-cli-copies.md#41-the-ext4-inversion-in-the-terms-p2-asks-for) |

---

## Open Questions

**None open.**
Ten were ruled 2026-08-24, six on 2026-09-03, and two on 2026-09-04 — see the
[Decision Ledger](#decision-ledger). The two the amendment opened were both ruled the same day and
are kept below with their reasoning, pending the next compaction. OQ-PD17 was opened 2026-09-04 by
the capture build and ruled the same day; ruling it surfaced OQ-PD18, which is the reason the
subsystem it governs has never run on any machine.

### ✅ OQ-PD17 — what is the unreferenced oracle for a capture entry, now that reflink has retired `st_nlink`? — RESOLVED (2026-09-04)

Opened 2026-09-04 by capture slice 4. **This question was living as prose inside
[`install-capture.md`](../plans/install-capture.md)'s build order with no ID**, which made it
invisible to the roadmap and to the corpus-wide question count, and left this document claiming
zero open questions while a live design decision sat in its domain. Filed here, cited from there.

**What happened.** [OQ-PD10](#decision-ledger)'s capture design was written around *hardlink*
materialize, which gave GC a free and fail-safe oracle: a materialized hardlink keeps the entry's
link count above 1, so `st_nlink == 1` means unreferenced. Slice 4 measured that `link(2)` is
**mount-bound** — it returns `EXDEV` between two binds of one btrfs — and built materialize on
**reflink** (`FICLONE`) instead, which works cross-mount because it needs only the same filesystem.
**A reflinked file has `nlink == 1` while being fully referenced**, so the oracle inverts from
fail-safe to actively wrong: GC keyed on it would reap live entries. The copy arm always had the
same hole; it was written as an edge case and is the norm on every ext4 machine.

**What it decides:** whether captures can be reclaimed at all. Until it is answered, slice 5 is
unbuilt and **entries accumulate with no way to remove them** — which is the disk cost this whole
subsystem exists to delete, arriving from the other end.

| | Candidate | What it costs that `st_nlink` did not |
| :--- | :--- | :--- |
| **(a)** | The `act:"materialize"` receipts slice 4 already writes into each workspace's `.yolo/receipts.jsonl` | Names workspace → key exactly, but reading them means **enumerating workspaces** — and `FindYoloWorkspaces` is already refused for this, because it reads `podman ps -a` and a workspace whose container was removed is invisible |
| **(b)** | A store-side reference list | A second record that must survive a workspace deleted with `rm -rf`, i.e. it can go stale in the unsafe direction |
| **(c)** | `FIEMAP` extent sharing | Real, but per-filesystem, and answers nothing on the copy arm |

_Leaning (NOT TAKEN — see the Answer):_ **(b), with the staleness made safe rather than assumed** —
a reference list plus a cheap existence check of each referrer at GC time. All three candidates were
retired instead; the leaning was answering a question the system does not ask.

**Answer:**
> **No unreferenced oracle, and none is needed. The reap rule is the COMPLEMENT OF THE RESOLVER.**
>
> Two facts, both read out of the shipped code, retire the question rather than answering it.
>
> **(1) Reclaiming is never a correctness event.** MEASURED 2026-09-04
> ([`agent-cli-copies.md` §4.2](agent-cli-copies.md#42-reclaiming-a-capture-entry-is-never-unsafe--which-reframes-oq-pd17)):
> a reflinked destination survives its source's unlink byte-identical, and the hardlink and copy arms
> strand nothing either. The store is a **cache, not an allocator** — there is no dangling pointer in
> this system. The term *garbage collection* imported the heap's assumption that reclaiming something
> still referenced is a failure; here the worst case of reaping a live entry is that the next COLD
> install re-downloads. This document's own stakes line — *"whether captures can be reclaimed at
> all"* — was false when written.
>
> **(2) The unreachable set is already computable, exactly.** `resolveCaptureFor`
> (`internal/cli/capturematerialize.go:183`) selects an entry by scanning each entry's own receipts
> and taking **newest wins by receipt time per (bin, platform)**, greater key breaking the tie. So
> every entry that is not the newest for its `(bin, platform)` is **already unreachable by the only
> code that reads the store** — not *probably unreferenced*; unreachable, by the selection function,
> today. GC is therefore the complement of a rule that already exists, derived from the reader, and
> cannot disagree with it.
>
> **The rule: delete every entry `resolveCaptureFor` would not select.** `K = 1` per
> `(bin, platform)`. An entry with no readable receipt is already not a selection candidate, and is
> reapable on the same rule rather than on a second one.
>
> **What this deletes.** All three candidates — no workspace enumeration, no store-side reference
> list, no `FIEMAP`. And both idioms this question called safe under any oracle, neither of which
> survives contact with the trigger:
>
> - **`K = 2`'s rollback target has nowhere to be used.** The store is not a version history; it
>   holds whatever a human captured. A materialized older version is updated by evergreen within
>   `UPDATE_INTERVAL` anyway (`internal/entrypoint/shims.go:986`, 3600s). Real rollback is the
>   vendor's own per-workspace `~/.local/share/<bin>/versions/*` — the V axis, and A7's job
>   ([`agent-cli-copies.md` §5.1](agent-cli-copies.md#51-a7--prune-stale-versions-executed-by-whoever-installed-the-new-one)).
> - **The age floor guarded a window the completion marker already covers.** `Resolve` reads the
>   marker and nothing else, so an in-flight entry is invisible without it. The one real race — GC
>   unlinking an entry mid-materialize — is not fixed by an age floor and needs no fix: a failed
>   materialize is a MISS, and a miss falls through to the vendor installer silently, by design
>   (`internal/entrypoint/shims.go:999-1003`).
>
> **What survives.** An entry's `capture-manifest.json` sits BESIDE `tree/`, never inside it
> (`internal/capture/manifest.go:21`), so a reap can drop `tree/` and clear the completion marker
> while keeping the manifest. Drift comparison against a version no longer stored then costs
> kilobytes instead of a retained tree — which is the half of capture's value
> ([§6.3](#63-installers-that-just-do-whatever-capture-the-install-then-treat-the-capture-as-the-package))
> that was never a disk property.
>
> **The residual, filed as [OQ-PD18](#decision-ledger).** Every line above
> assumes entries exist. They do not: `yolo capture` is the store's only populating act, it is a
> manual host command, and the maintainer had not heard of it — so on every machine today the store
> is empty and slices 1–4 and 6 are shipped but unreachable.

### ✅ OQ-PD18 — what populates the capture store? — RESOLVED (2026-09-04)

Opened 2026-09-04, ruling OQ-PD17. **Nothing automatic does.** `yolo capture <bin>` is the store's
only writer (`internal/cli/capturehost.go`), it is a host command with no caller in the run pipeline,
and a materialize miss is deliberately **silent** — it falls through to the vendor installer without
saying the store was empty (`internal/entrypoint/shims.go:999-1003`). The consequence measured
2026-09-04: the maintainer, who commissioned the subsystem, did not know the command existed and has
never run it, so `_try_materialize` has never once hit on any machine. Corroborated on this
development jail the same day — `~/.local/share/yolo-jail/captures/entries/` exists (boot creates it,
`internal/storage/ensure.go:45`) and is **empty**.

**What it decides:** whether capture is a subsystem or a shipped-and-unused one. It is downstream of
[OQ-CP1](agent-cli-copies.md#-oq-cp1--is-the-disk-justification-retracted-and-is-oq-pd15-reversed--resolved-2026-09-04):
if capture is not the disk fix, auto-triggering it is less urgent, not more.

| | Candidate | Cost |
| :--- | :--- | :--- |
| **(a)** | Leave it manual, and document it | Free. Leaves a subsystem whose value appears only at N≥2 workspaces gated behind a command nobody discovers |
| **(b)** | **Capture-on-first-install** — when `_do_install` falls through and the vendor installer succeeds, snapshot the delta and admit it | The delta is taken against a **dirty** home, which is the exact thing the throwaway jail exists to avoid; admit is a host act and slice 4(f) gives the capture jail no store mount; and the first cold install on a machine pays a full-size store write for a saving that appears only at the second workspace |
| **(c)** | **Tell the human.** A selected pack installs `via: "installer"` and no entry resolves → say so, once, naming `yolo capture <bin>` | ~20 lines, no new trust surface, no automatic third-party installer run. Discovers the subsystem without deciding for the user |
| **(d)** | **Auto-capture on first launch**, host-side, behind a config knob | Preserves the clean-home delta and keeps admit where slice 4(f) put it. Costs an extra throwaway jail + installer download before a first launch the human is already waiting on |

_Leaning (NOT TAKEN — see the Answer):_ **(c) now, (d) behind a knob and default OFF.** The leaning
weighted the ext4 row as a reason to withhold the default; the ruling weights the download saving,
which is filesystem-independent, above it.

**Answer:**
> **(d), DEFAULT ON.** *"I want (d) default on."* — 2026-09-04.
>
> Auto-capture on first launch, host-side, in the throwaway jail, with no knob to turn it on. (c)'s
> message is subsumed: a human who never has to run the command does not need to be told about it.
> (b) stays refused — the dirty-home delta is the thing the throwaway jail exists to avoid, and the
> ruling does not ask for it.
>
> **The ext4 objection, restated honestly rather than dropped.** It is narrower than the leaning
> made it sound. Capture costs **exactly `+S` of disk on ext4, at every N** — it never saves disk
> there — and buys **`N−1` avoided downloads of ~205 MiB**, which it saves on every filesystem. At
> N=1 that is pure cost; from N=2 it is a bandwidth-for-disk trade that a machine running several
> jails wants. The one number that would revisit this is the **ext4 share of real installs**, still
> unmeasured; if it comes back high AND single-workspace machines turn out to be common, the fix is
> to gate the *automatic* trigger on reflink availability (`internal/capture/clone_linux.go` already
> reports the filesystem) and leave manual `yolo capture` always available. Not built now — recorded
> so the default can be revisited on evidence rather than re-argued.
>
> **The trust delta is smaller than this question implied.** The vendor installer runs either way —
> the launcher downloads and executes it on every cold install today. What (d) adds is an *extra*
> jailed run at a *different* moment without the human typing anything. It is still jailed, still
> the same script, still the same URL from the same pack.
>
> **⚠ THE CONSEQUENCE THAT IS NOT OPTIONAL: default-on makes a STALE ENTRY ACTIVELY HARMFUL, so
> [A7's V-axis prune](agent-cli-copies.md#51-a7--prune-stale-versions-executed-by-whoever-installed-the-new-one)
> becomes a PREREQUISITE of this ruling rather than a companion to it.**
>
> Walk a new workspace whose store entry is one release behind. Cold install materializes the stale
> version and touches the stamp; within `UPDATE_INTERVAL` evergreen runs the vendor updater and
> downloads the current one. The workspace has now paid **a copy AND a download** where no capture
> at all would have paid one download — and, because vendor updaters **retain** old builds rather
> than replacing them (measured 2026-09-03: five claude builds, 1.2 GB, four of them dead), it is
> left holding a dead version nothing will ever remove. The N-axis mechanism seeds V-axis garbage
> into every workspace it touches.
>
> Two ways to close it, and the cheap one is already recommended elsewhere:
> 1. **Land A7 with this** — keep-newest-K over the vendor's own version dir, executed by whoever
>    installed the new one. Deletes the seeded corpse at the moment the update creates it, on every
>    filesystem, with no store involvement. ~30 lines, and it is already the companion
>    [OQ-CP1](agent-cli-copies.md#-oq-cp1--is-the-disk-justification-retracted-and-is-oq-pd15-reversed--resolved-2026-09-04)
>    proposes shipping inside evergreen.
> 2. **Keep the store fresh** — re-capture when the vendor ships a new release. That is
>    [OQ-CP4](agent-cli-copies.md#-oq-cp4--does-an-evergreen-update-get-to-materialize-from-the-store--resolved-2026-09-04),
>    whose leaning was *"leave it one-off, and say so."* This ruling makes that leaning expensive:
>    one-off means the store rots, and a rotting store is exactly the stale-entry case above. **CP4
>    is now load-bearing and should be ruled with this.**
>
> Also required by default-on, all cheap and none of them design questions: the per-program capture
> lock the plan already specifies (two workspaces launching at once must not both capture), and a
> capture failure that **warns once and never fails the launch** — the same discipline materialize's
> silent miss already follows.

### ✅ OQ-PD15 — does capture GATE the evergreen rollout, or trail it? — RESOLVED (2026-09-03)

[OQ-PD10](#decision-ledger) sequenced capture **last**, on the reasoning that the receipt ships
first and capture merely upgrades its guess. Evergreen changes that arithmetic, because the cost
capture exists to remove is the one evergreen multiplies.

**The measurement (2026-09-03, this development jail).** `~/.local` is a **per-workspace** bind, so
every workspace downloads its own copy of every agent, and vendor updaters retain old builds rather
than replacing them. One workspace holds **four claude versions at 1019 MB**. Today that is a
one-time cost per workspace because nothing ever updates; under evergreen it becomes a *recurring*
one, on every launch that lands a new version, in every workspace.

**Answer:**
> **(a) — capture first. Build the complete version, and sequence toward it.**
> *"Is this a decision of implement more now vs later? I want the complete version. I can await for
> a sequence of things to implement if that gets us further/cleaner."*
>
> So the question was mis-posed: I framed it as ship-sooner versus ship-cleaner, and the ruling is
> that **sooner is not a goal here.** My leaning (b) traded a real subsystem for calendar time, and
> the trade was never asked for.
>
> **Consequences, all folded into [§10](#10-what-i-would-build-in-order):** capture moves ahead of
> evergreen, the two `§10` steps swap, and the prune stopgap (b) needed is **not built** — under
> (a) there is nothing to prune, because a materialized tree comes from the CAS and old versions
> were never per-workspace to begin with. That deletes an item rather than deferring one, which is
> the "further/cleaner" the ruling is buying.
>
> **What this does NOT license:** the freeze is still a live defect for as long as capture takes,
> and it stays visible rather than being quietly accepted. The `DISABLE_AUTOUPDATER` class of
> config-capture bug is separately fixable today and is not gated on any of this.

### ✅ OQ-PD16 — how does a PROJECT dependency get pinned on the host, where there is no jail? — RESOLVED (2026-09-03)

[§3.5](#35-the-second-axis-who-the-dependency-serves-amendment-2026-09-03) rules that project
dependencies pin. Inside a jail that is already true and already mechanised: nixpkgs through
`flake.lock`, toolchains through `mise.lock`. **On the host there is no equivalent** — a user
running the same project outside a jail gets whatever their machine has, and yolo has no seam to
pin through.

**What it decides:** whether "project dependencies are pinned" is a claim about the jail or a claim
about the project. If the latter, the pin has to be expressible somewhere the host also reads, and
the obvious candidate is a **nix devshell** generated from the same declarations the jail already
consumes — one source, two consumers.

**Answer:**
> **Jail-only in this document; the host notch gets its own design, now written.**
> *"Yes, then nixshell on the roadmap. Write a design doc for that. And won't we be able to share a
> ton of that between macos-user and host?"*
>
> **Yes — and considerably more than "a ton": the mechanism already exists and was named for this.**
> `flake.nix`'s `packages.yoloNoncontainerPackages` is a `buildEnv` profile of `packages:` realized
> natively, and its own comment states the axis in advance: *"this is what every notch BELOW `jail`
> needs … the attr resolves for `x86_64-linux` unchanged, and Linux `guest` is the next consumer.
> 'noncontainer', not 'host': `host` is one notch and `guest` needs the identical mechanism, so
> naming it after either one would be the same lie in a new spelling."* `macos-user` is its only
> caller today. The host notch is a **third consumer of an existing attribute**, not a new
> mechanism.
>
> ⚠ **And "nixshell" is the one spelling the design must not take.** The same comment records why,
> and it is a refuted objection worth keeping: a `devShell`'s `print-dev-env` dumps the entire
> stdenv toolchain — clang, GNU coreutils, sed, grep, make — onto PATH **ahead of the host
> userland**. A `buildEnv` contains only the declared packages. So the shape is a profile, and the
> word "shell" names the user-facing verb at most, never the nix primitive.
>
> **The design you asked for already exists** —
> [`noncontainer-nix-environment.md`](noncontainer-nix-environment.md), 2026-08-02, re-verified
> 2026-08-23. It owns this question and already carries the sharing analysis (§7: confinement and
> environment are *not* orthogonal), the devShell rejection (§4.1), the `nix profile` and
> `nix shell` alternatives (§4.2–4.3), platform coverage (§5), and `mise_tools`' status at each
> notch (§6's table). Six questions there are live. **What this amendment adds to it is a frame it
> predates:** it analysed the host notch as a *packaging* problem, and §3.5 now says what the
> packages are *for* — project dependencies, which pin. Recorded as a postscript there rather than
> re-argued here.
>
> This document keeps its own scope: the host stays **enumerated as unmanaged** per
> [§5.5](#55-a5--do-nothing-and-say-so)'s discipline until that work lands.
