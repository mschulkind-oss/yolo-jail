---
title: "An agent's interpreter belongs to the pack, not the workspace"
date: 2026-09-21
status: accepted
tags: [packs, programs, mise, node, path-resolution, agent-clis, launchers]
summary: "A workspace `mise.toml` pin silently becomes the Node that every npm-delivered agent CLI runs under, so a repo pinning Node 20 makes pi unrunnable with an ESM SyntaxError. A `program` contribution should declare the Node floor its own package requires, and the generated launcher should exec a resolved interpreter instead — leaving the workspace pin governing everything except that one process."
vantage:
  status-chip: true
---

# An agent's interpreter belongs to the pack, not the workspace

**Status:** DECIDED, 2026-09-22. **Every ruling is in, and all of it is BUILT.** Claims about the tree were measured in a jail at `753bcb88` on 2026-09-21 and
re-checked at `b99ca9b4` on 2026-09-22; what could not be measured from inside a jail is marked
UNVERIFIED.

**Built:** the declaration (`node_floor` on a `program` contribution, refused on every other kind and
for a value the comparison cannot handle), the floor comparison, `packs/pi` declaring `22.19`, **and
the RESOLUTION**: `ResolveNodeForFloor` takes the image's node when it satisfies and otherwise the
newest satisfying version in the mise store — resolved offline by path, nothing executed, nothing
fetched — and the npm launcher bakes it as an exec prefix. **So a declared floor now governs the
interpreter.**

✅ **And the last two halves shipped 2026-09-22 as well.** The generated **bootstrap script** — which
already installs MCP/LSP tools eagerly over the network, after the CA bundle and after `mise install` —
carries the floors baked (not read from the environment, because `macos-user` runs that stage under
`env -i`), installs `node@<floor>` for any the tree does not satisfy, and **refuses** if one still is
not satisfied afterwards. The predicate is `yolo internal node-floor-satisfied`, a subcommand rather
than shell because a shell reimplementation of the version compare is the second implementation this
repo keeps deleting.

⚠ **`mise install node@<floor>` is the right call there even though a mise selector is a PREFIX rather
than a floor**, and the asymmetry is the point: a prefix is wrong for ACCEPTING an installed version —
it would fetch 22.19.0 while 22.23.2 sits there — and exactly right for INSTALLING one, because what
it fetches satisfies the floor.

> [!WARNING]
> **Two traps found while building it, both of which shipped wrong in a first pass.**
>
> - **`npmLauncherTemplate` has TWO exec paths**, not one: the main one and the **re-entry guard**'s,
>   taken when `_YOLO_LAUNCHER_ACTIVE` already names this binary. Splicing only the tail left a
>   re-entrant launch running under the workspace's node — the exact defect, surviving in the one
>   path nobody looks at. The test now asserts over **every** exec line, because its first draft
>   checked the first match and passed while the guard was unwrapped.
> - **A test fixture stood in for pi's bin with a bash script.** pi's real bin is a JS file with a
>   `#!/usr/bin/env node` shebang, so once the launcher began exec'ing `<node> "$REAL_BIN"` the
>   fixture died with a `SyntaxError`. The fixture was wrong about the thing it modelled; the
>   launcher was right.

> [!NOTE]
> **Re-measured 2026-09-22 against the installed pi 0.87.0**, which is the check the warning below
> asked for: `engines.node` is still `">=22.19.0"`. The floor survived the version bump, and
> `packs/pi` now declares `22.19`.

> [!WARNING]
> **[§1](#1-the-failure-measured)'s failure was measured against pi 0.86.1, and the installed pi is now 0.87.0** — an
> evergreen launcher upgraded it on 2026-09-21. The `engines.node` floor is re-checked above; the
> `enableCompileCache` import is the vendor's and was not re-read.

> **In short.** Which Node runs an agent CLI is decided by whichever `mise.toml` the *workspace*
> ships — because the launcher execs a `#!/usr/bin/env node` script and mise's shims precede `/bin` on
> the jail PATH. That is the project's interpreter doing the agent's job. A `program` contribution
> should declare the Node floor its own package states, and the launcher should exec a resolved
> interpreter instead, leaving the workspace pin governing everything that is not that one process.

**Why it matters.** A workspace pinning Node 20 makes pi 0.86.1 — the agent this repo ships a pack
for — fail at `import` time with `SyntaxError: The requested module 'node:module' does not provide an
export named 'enableCompileCache'`. The message names a module export, not a version, and points at
neither the pack nor the workspace pin. Nothing in the launch says the two are related.

**The shape.** One optional `program` field; one interpreter resolved at launcher-generation time;
one changed line in the generated npm launcher.

**Cost.** A new manifest field with a validation rule, a resolution step in the launcher generator,
and one correction in [`tool-provisioning.md`](../research/tool-provisioning.md) whose Node table is
currently accurate. No shipped behavior changes for a program that declares nothing.

**Start at [§3](#3-the-shape)** — the declaration, the resolution order, and the exec line. The
diagnosis in [§1](#1-the-failure-measured) is only there to justify it.

**Needs your ruling:** **None** — all four were ruled 2026-09-22; see the
[Decision Ledger](#decision-ledger).

**Reads with:** [`agent-program-runtimes-plan.md`](agent-program-runtimes-plan.md) (the companion
sketch — incomplete, and not to be built from),
[`tool-provisioning.md`](../research/tool-provisioning.md) (owns the Node-resolution model this
amends), [`program-delivery.md`](../design/program-delivery.md) (owns the launcher and its decision
ledger), [`mise-node-dynamic-linking.md`](../reference/mise-node-dynamic-linking.md) (why there is
one node by default).

---

## 1. The failure, measured

Pi's own package states the requirement. Measured from the installed package
(`@earendil-works/pi-coding-agent@0.86.1`, `~/.npm-global/lib/node_modules/`):

- `package.json` declares `engines.node: ">=22.19.0"`.
- Its bundle entry (`dist/bundle/cli.js:2`) imports unconditionally:

  ```js
  import { createRequire, enableCompileCache } from "node:module";
  enableCompileCache();
  ```

`module.enableCompileCache` does not exist on the Node 20 line **at all** — this is the whole failure,
and it is a hard ESM resolution error rather than a deprecation:

| Interpreter | `typeof module.enableCompileCache` |
|---|---|
| `/mise/installs/node/20.20.2/bin/node` | `undefined` |
| `/mise/installs/node/22.23.2/bin/node` | `function` |
| `/bin/node` (v24.19.0) | `function` |

So a jail with Node 20 on PATH cannot run pi, and the error is raised by the module loader before any
of pi's code runs.

## 2. Why the workspace pin wins today

Nothing here is a mise bug or a pi bug; it is PATH precedence plus a shebang. Measured
2026-09-21:

1. `~/.yolo/bin/launch/pi` — the generated launcher — resolves `REAL_BIN` to
   `~/.npm-global/bin/pi` and ends with a bare `exec "$REAL_BIN"`.
2. `~/.npm-global/bin/pi` is a symlink to the JS bundle, whose shebang is `#!/usr/bin/env node`.
3. `node` therefore resolves through PATH, where the mise shims precede `/bin` (the order is owned by
   `internal/entrypoint/boot.go`'s `BootPath`, and stated in
   [`tool-provisioning.md` §3](../research/tool-provisioning.md#3-path-resolution--precedence)).
4. mise resolves `node` to the **workspace's** pin when one exists. `/workspace/mise.toml` pins
   `node = "24"`, so this repo does not reproduce the failure; a repo pinning `"20"` does, because
   Node 20 is in the shared `/mise` store and the shim resolves to it.

The model is documented, and its override table is the thing this design makes conditional:
[`tool-provisioning.md` §2](../research/tool-provisioning.md#2-the-node-question-resolved-one-by-default-two-only-on-override)
lists *"Bare `node`, shebangs, `npx`, agent CLIs → mise node"* under a workspace pin. That is
accurate today, and for bare `node` it is the point. **The defect is that the same row silently
covers agent CLIs, which are not project tooling** — they are programs the pack installed, whose
interpreter requirement is the vendor's and has nothing to do with the project's.

> [!WARNING]
> **The `engines` field is not enforced anywhere on this path.** npm warns about an engines mismatch
> and installs anyway, and the launcher never reads it. So the jail will install a program it cannot
> run and only discover it at exec. Fixing the resolution without also disclosing an unsatisfiable
> floor would reproduce that silence one layer up.

## 3. The shape

Three parts: what a pack declares, how an interpreter is chosen, and what the launcher does with it.

### 3.1 The declaration

A `program` contribution gains an optional **Node floor** — the minimum version this program's
entrypoint requires. Pi's pack would declare the floor its package already states (`>=22.19`).

The floor is a **property of the program**, declared beside `bin`/`via`/`package` in the pack that
installs it, and refused on any contribution kind that is not `program`. A program that declares
nothing keeps today's behavior byte-for-byte — which is load-bearing, not tidiness:
`@github/copilot` ships a JS loader (`npm-loader.js`), but `opencode-ai` ships a **native ELF**
(`bin/opencode.exe`) through the same `via: npm` route. A blanket "run npm programs under node"
would break opencode. The pin is opt-in per program.

### 3.2 Resolution

The launcher generator resolves one absolute interpreter path and bakes it. Preference order:

| # | Candidate | Why it is here |
|---|---|---|
| 1 | The **image's** node, when it satisfies the floor | Self-contained, always present, no install, and it is the node the MCP wrappers and Go tooling already target |
| 2 | The newest satisfying node in the **mise store** (`$MISE_DATA_DIR/installs/node/*`) | Already-resolved toolchains; the store keeps alias symlinks beside real version dirs (`24 -> ./24.19.0`, measured), so a selector resolves **offline by path** |
| 3 | An interpreter **installed for this program**, eagerly, at the jail's readiness act | An interpreter is *environment*, and the environment is provisioned before the agent runs — never on first use |
| 4 | Nothing satisfies it | **The launch refuses**, naming the pack, the program, the floor and what is available |

**Resolution splits from installation, and the split is forced by two constraints rather than chosen.**
The generator may resolve — read versions, compare, bake an absolute path — and may not install:

- **`GenerateAgentLaunchers` also runs on the HOST, as a dry run, under `yolo check`**
  ([`check/entrypoint.go:81-96`](../../internal/cli/check/entrypoint.go)). A generator that installed
  would install on `yolo check`, which is an observe verb.
- **It runs before `GenerateCABundle`** ([`boot.go:601-608`](../../internal/entrypoint/boot.go)), so a
  generator that fetched could not verify TLS.

So the install belongs to the **provisioning stage**, which already runs `mise install` before the
target command ([`provision.go:72-73`](../../internal/provision/provision.go)) — the same discipline
`launchercollision.go`'s *"DECLARED, NOT INSTALLED"* check already states.

> [!WARNING]
> **A mise selector is a PREFIX, not a floor**, so it cannot express this design's requirement.
> Measured 2026-09-22: `mise install --dry-run node@22.19` reports *"22.19.0 would install"* with
> 22.20.0 and 22.23.2 already present, and `node@24` reports *"24.21.0 would install"* with 24.19.0
> present. A selector therefore fetches a new version rather than accepting a satisfying one — which
> is why the floor is compared against candidates yolo enumerates itself. Separately,
> [`mise.go:13-28`](../../internal/entrypoint/mise.go) forbids yolo declaring a runtime in mise config
> at all, and a global pin loses to the workspace layer anyway, so "just pin node in mise" could not
> have fixed the bug either.

> [!IMPORTANT]
> **Candidate 1 is not currently knowable without executing it.** `/etc/yolo-jail-image-identity`
> holds a content sha only, and no node version is baked into the image env or into
> `internal/entrypoint`. So "does the image node satisfy the floor?" needs either a bounded
> `node --version` at boot — precedent exists, the entrypoint already execs `ldconfig`, `iptables`,
> `socat`, `supervise` and `mise uninstall` — or the image learning to publish its node version.
> That choice is [`OQ-AR2`](#decision-ledger)'s neighbour: the ruling installs eagerly during
> provisioning, so the generator must still learn the image node's version without installing it.

**What must not happen:** anything that changes `node` for anyone else. Prepending to PATH, exporting
`MISE_NODE_VERSION`, and `mise exec node@X --` all leak into every child the agent spawns, so the
agent's own `npm test` would run under yolo's node instead of the project's. The resolution result is
consumed by one `exec` and nothing else.

### 3.3 The launcher

The generated npm launcher's final line changes from the bin itself to the resolved interpreter:

```bash
exec <resolved-node> "$REAL_BIN" "$@"     # was: exec "$REAL_BIN" "$@"
```

`<resolved-node>` is an absolute path that is `shquote`'d into the generated script like every other
spliced value (the `npmLauncherTemplate` splice contract). For a program with no declared floor the
generated body must be **byte-identical to today's** — that is a test, not an intention.

### 3.4 An unsatisfiable floor refuses

**If a selected pack declares a program whose floor nothing satisfies — after provisioning has had its
chance to install one — the launch refuses.** A jail that cannot run what was selected is not a ready
environment, and reporting success while leaving it unready states a result that was not achieved. That
is the same rule the host notch already applies at
[`applyhostdepgate.go:9-11`](../../internal/cli/applyhostdepgate.go): *"an `--assert`'s promise is a
ready environment, so a posture that returns 0 having left it unready has stated a result it did not
achieve."*

**The predicate is keyed on a pack, not on an agent.** Core has no agent registry and cannot tell
`yolo -- claude` from `yolo -- bash`, so the test is *"a SELECTED pack declares a `program` whose
declared floor no available interpreter satisfies"*. Nothing here may ask whether the program is an
agent.

The refusal names four things, because the failure it replaces names none of them: the **pack**, the
**program**, the **floor**, and **what is available**.

> [!WARNING]
> **This is the first fatal in the provisioning class, and that cost is real.** Every neighbouring
> failure there degrades: `mise install` failing prints `PROVISIONING FAILED` and continues unless a
> human at a TTY says no ([`provision.go:117-143`](../../internal/provision/provision.go)); the
> bootstrap records that *"an offline boot fails here routinely and simply retries next launch"*
> ([`shell.go:448-449`](../../internal/entrypoint/shell.go)); the launcher's install is `|| true`;
> auto-capture warns and retries; an absent `requires` entry is a warning. So an offline boot that
> cannot fetch a satisfying interpreter now refuses where it used to continue.
>
> **What makes that affordable is that the path is rare, not that the cost is small.** The image bakes
> `nodejs_24` ([`flake.nix:1169`](../../flake.nix)), which satisfies pi's `>=22.19` today, so the
> immediate case resolves at candidate 1 with no install and no refusal. An interpreter is also shared
> across programs, so N declaring packs need at most a couple of interpreters rather than N.
>
> ⚠ **The residual objection is the one that killed the 2026-09-03 eager shape**: a refusal can still
> fire for a program this launch was never going to run. It is narrower here — it needs a *declared
> floor* that *nothing* satisfies, rather than a network round-trip per selected pack — but it is the
> same shape, and an implementer who finds it firing in practice should say so rather than widen the
> resolution to hide it.

**No escape hatch.** The repo's criterion is that a hatch exists for broken user configuration, never
to paper over a yolo defect — and the deleted 2026-09-03 shape's `YOLO_ALLOW_STALE_AGENTS` was
justified *"by consistency, not by need"*, which is the wrong reason. A user facing this refusal can
fix it where they are standing: drop the pack, or make an interpreter available. If one is wanted
later it is a separate ruling, not an implementation detail.

## 4. What this does not do

- **It does not change the workspace pin.** Bare `node`, `npx`, shebangs, the agent's bash children
  and project tooling keep resolving exactly as they do now.
- **It does not make the workspace pin illegal or discouraged.** Pinning Node 20 for a project is
  correct; the bug is that it currently determines an agent's runtime too.
- **It does not refuse a launch because a workspace pin is old** — that is the case this design
  exists to make irrelevant, and a refusal there would turn a legitimate project into an unlaunchable
  jail ([§5](#5-alternatives-with-verdicts)). It **does** refuse when *no* interpreter anywhere
  satisfies a declared floor, which is a different fact about the jail rather than about the project
  ([§3.4](#34-an-unsatisfiable-floor-refuses)).
- **It does not read the vendor's `engines` field at runtime** — the floor is declared
  ([`OQ-AR4`](#decision-ledger)).
- **It does not cover the native installer route** (`claude`, `codex`, `agy`), which installs a
  native binary with no interpreter to choose.
- **It does not settle macos-user.** See the UNVERIFIED note below.

> [!NOTE]
> **UNVERIFIED — the macos-user node.** `flake.nix:1169` puts `nodejs_24` in `coreFloorNames`, the
> list the comment above it says a second backend takes "without a second list", so macos-user should
> have a node. But [`macos-user-provisioning.md:224`](macos-user-provisioning.md) records a measured
> `npm: command not found` for `via: npm` on that backend, dated 2026-09-11 — the day before that
> floor shipped. Which is true today decides whether this design needs a backend carve-out. The
> implementer measures it first; nothing here should be built against the assumption that it works.

## 5. Alternatives, with verdicts

| Alternative | Verdict |
|---|---|
| **Refuse the launch** when the **workspace's** node cannot run a selected agent | **Rejected by the operator.** A workspace pin is legitimate, so this turns a valid project into an unlaunchable jail and asks the user to change a project file to get an agent running. ⚠ Not the same case as [§3.4](#34-an-unsatisfiable-floor-refuses)'s refusal, which fires when **nothing on the machine** satisfies the floor — there the jail genuinely cannot run what was selected, and no project file would fix it |
| **Change the workspace's mise pin from yolo** | **Rejected.** The pack would be writing the project's toolchain declaration to suit itself — the same single-writer violation, in the other direction |
| **Prepend the resolved node to PATH for the agent** | **Rejected.** Works for the agent and silently breaks its children: any `node`/`npx` the agent runs would stop matching what the project declares |
| **`MISE_NODE_VERSION=<floor>` in the agent's environment** | **Rejected** for the same reason; it is PATH-prepending by another name and leaks further |
| **`mise exec node@X --` around the exec** | **Rejected.** Convenient, and it installs its own environment for the whole child tree — the leak again |
| **Blanket "run every `via: npm` program under node"** | **Rejected by measurement.** `opencode-ai` installs a native ELF; wrapping it would break it. Opt-in per program |
| **Do nothing; document "don't pin an old Node"** | **Rejected.** The jail installs the program itself, so it owns the failure. A doc that asks the user to know pi's engine range to use an agent is the defect restated |
| **Read `engines.node` from the installed package instead of declaring** | **Rejected** ([`OQ-AR4`](#decision-ledger)) — core does not guess, and the package is not installed when `yolo check` runs, so a derived floor could never be validated |

## 6. Risks

| Risk | Mitigation |
|---|---|
| A floor silently unsatisfied, and the agent crashes exactly as it does today | [§3.4](#34-an-unsatisfiable-floor-refuses) refuses instead. A silent fallback would be worse than today, because it would *look* handled |
| The refusal fires for a program this launch was never going to run — the objection that killed the 2026-09-03 eager shape | Narrowed rather than eliminated: it needs a declared floor that nothing satisfies, and the baked `nodejs_24` satisfies the only declaring pack today. Stated as a residual in [§3.4](#34-an-unsatisfiable-floor-refuses) rather than engineered away |
| An offline boot that used to continue now refuses | Deliberate, and the one place this design departs from the provisioning class's five degrade precedents. Named in [§3.4](#34-an-unsatisfiable-floor-refuses) so it is a decision rather than a surprise |
| The pinned interpreter path does not exist at exec time (store pruned between boot and use) | The launcher's `[ -x ]` test is the last word today and must stay one; the failure mode is named in the sketch |
| `shquote` missed on the new splice, making a pack-declared string shell source | The splice contract already requires every value be quoted into a bare position, and `launchersplice_test.go`'s hostile-value table is the existing instrument |
| The new field is added to the manifest but nothing consumes it, so it reads as honored | The test that fails when the resolution call site is deleted from `GenerateAgentLaunchers` — see [§7](#7-what-done-looks-like) |

## 7. What done looks like

A human can check all of these without reading the implementation:

1. In a workspace whose `mise.toml` pins Node 20, `pi` starts. `node --version` in the agent's own
   bash tool still reports 20, and `node --version` at the jail shell still reports 20.
2. In the same jail, `opencode` still starts (its bin must never be wrapped).
3. A program whose pack declares no floor produces a launcher byte-identical to the pre-change one.
4. A floor nothing satisfies **refuses the launch**, in one message naming the pack, the program, the
   floor and what is available. The jail does not start.
5. The launch's Node-resolution disclosure does not claim to have satisfied a floor it did not.
6. A floor the image does not meet is satisfied by an interpreter installed **during provisioning** —
   so the first invocation of that program is not the moment anything is downloaded, and a jail that
   started has everything its selected packs declared.
7. `yolo check` reports what a floor would resolve to **without installing anything** — running it
   twice on a cold machine leaves the mise store unchanged.

## Decision Ledger

Every question this doc opened is ruled. The rulings live in the body sections named below; the
deliberation that produced them is in git.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| AR-L1 | The fix is a **declared Node floor plus a pinned interpreter in the generated launcher** — not a refusal over a legitimate workspace pin, and not a change to that pin. ⚠ Amended 2026-09-22: it *is* a refusal when nothing satisfies the floor ([`OQ-AR3`](#decision-ledger)), which is a different case | 2026-09-21 · amended 2026-09-22 | [§3](#3-the-shape), [§3.4](#34-an-unsatisfiable-floor-refuses) | — |
| AR-L2 | The pin is **opt-in per `program`**, never a blanket rule for `via: npm` | 2026-09-21 | [§3.1](#31-the-declaration) | — |
| AR-L3 | The workspace pin stays authoritative for **everything except the agent's own process** | 2026-09-21 | [§4](#4-what-this-does-not-do) | — |
| OQ-AR1 | The declared value is a **minimum (floor)**, compared against the candidate's version — because the vendor's own constraint is a range floor, and a floor survives the image moving to a newer Node without a manifest edit. ⚠ A mise SELECTOR cannot express it: selectors are prefixes that fetch rather than accept, measured | 2026-09-22 | [§3.2](#32-resolution) | — |
| OQ-AR2 | **Eager, at the jail's readiness act — there is no lazy path.** An interpreter is *environment*, and the environment is provisioned before the agent runs. Resolution splits from installation because `GenerateAgentLaunchers` also runs host-side under `yolo check` and runs before `GenerateCABundle`, so the generator resolves and the **provisioning stage** installs. ⚠ This does NOT reverse [`OQ-PD12a`](program-delivery.md#decision-ledger), which governs *currency* (is the agent's own binary current?) rather than *readiness*; `shell.go:420-423` already draws that line | 2026-09-22 | [§3.2](#32-resolution) | — |
| OQ-AR3 | **An unsatisfiable floor REFUSES the launch**, naming the pack, the program, the floor and what is available. If a pack is selected, the jail must be able to run what it declares. No escape hatch. ⚠ First fatal in the provisioning class, whose five neighbours all degrade — the cost and the residual objection are stated rather than engineered away | 2026-09-22 | [§3.4](#34-an-unsatisfiable-floor-refuses) | — |
| OQ-AR4 | The floor is **declared** in the manifest, not read from the installed package — core does not guess, and the package does not exist at `yolo check` time | 2026-09-22 | [§3.1](#31-the-declaration) | — |
