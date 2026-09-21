---
title: "An agent's interpreter belongs to the pack, not the workspace"
date: 2026-09-21
status: draft
tags: [packs, programs, mise, node, path-resolution, agent-clis, launchers]
summary: "A workspace `mise.toml` pin silently becomes the Node that every npm-delivered agent CLI runs under, so a repo pinning Node 20 makes pi unrunnable with an ESM SyntaxError. A `program` contribution should declare the Node floor its own package requires, and the generated launcher should exec a resolved interpreter instead — leaving the workspace pin governing everything except that one process."
vantage:
  status-chip: true
---

# An agent's interpreter belongs to the pack, not the workspace

**Status:** DESIGN, 2026-09-21. **Nothing built.** Every claim about the tree below was measured in a
jail at `753bcb88` on 2026-09-21; what could not be measured from inside a jail is marked UNVERIFIED.

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

**Needs your ruling:** [OQ-AR1](#OQ-AR1), [OQ-AR2](#OQ-AR2), [OQ-AR3](#OQ-AR3), [OQ-AR4](#OQ-AR4).

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
| 3 | An interpreter **installed for this program** | [OQ-AR2](#OQ-AR2) — whether, and when |
| 4 | Nothing satisfies it | [OQ-AR3](#OQ-AR3) — disclosed, and falling back to today's exec |

> [!IMPORTANT]
> **Candidate 1 is not currently knowable without executing it.** `/etc/yolo-jail-image-identity`
> holds a content sha only, and no node version is baked into the image env or into
> `internal/entrypoint`. So "does the image node satisfy the floor?" needs either a bounded
> `node --version` at boot — precedent exists, the entrypoint already execs `ldconfig`, `iptables`,
> `socat`, `supervise` and `mise uninstall` — or the image learning to publish its node version.
> That choice is [OQ-AR2](#OQ-AR2)'s neighbour and is called out in the sketch.

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

## 4. What this does not do

- **It does not change the workspace pin.** Bare `node`, `npx`, shebangs, the agent's bash children
  and project tooling keep resolving exactly as they do now.
- **It does not make the workspace pin illegal or discouraged.** Pinning Node 20 for a project is
  correct; the bug is that it currently determines an agent's runtime too.
- **It does not refuse a launch.** The operator rejected a startup refusal; see [§5](#5-alternatives-with-verdicts).
- **It does not read the vendor's `engines` field at runtime.** See [OQ-AR4](#OQ-AR4).
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
| **Refuse the launch** when the workspace's node cannot run a selected agent | **Rejected by the operator.** A workspace pin is legitimate, so this turns a valid project into an unlaunchable jail and asks the user to change a project file to get an agent running |
| **Change the workspace's mise pin from yolo** | **Rejected.** The pack would be writing the project's toolchain declaration to suit itself — the same single-writer violation, in the other direction |
| **Prepend the resolved node to PATH for the agent** | **Rejected.** Works for the agent and silently breaks its children: any `node`/`npx` the agent runs would stop matching what the project declares |
| **`MISE_NODE_VERSION=<floor>` in the agent's environment** | **Rejected** for the same reason; it is PATH-prepending by another name and leaks further |
| **`mise exec node@X --` around the exec** | **Rejected.** Convenient, and it installs its own environment for the whole child tree — the leak again |
| **Blanket "run every `via: npm` program under node"** | **Rejected by measurement.** `opencode-ai` installs a native ELF; wrapping it would break it. Opt-in per program |
| **Do nothing; document "don't pin an old Node"** | **Rejected.** The jail installs the program itself, so it owns the failure. A doc that asks the user to know pi's engine range to use an agent is the defect restated |
| **Read `engines.node` from the installed package instead of declaring** | Open — [OQ-AR4](#OQ-AR4) |

## 6. Risks

| Risk | Mitigation |
|---|---|
| A floor silently unsatisfied, and the agent crashes exactly as it does today | The disclosure in [OQ-AR3](#OQ-AR3) is the whole point of that question; a silent fallback would be worse than today because it would look handled |
| The pinned interpreter path does not exist at exec time (store pruned between boot and use) | The launcher's `[ -x ]` test is the last word today and must stay one; the failure mode is named in the sketch |
| `shquote` missed on the new splice, making a pack-declared string shell source | The splice contract already requires every value be quoted into a bare position, and `launchersplice_test.go`'s hostile-value table is the existing instrument |
| The new field is added to the manifest but nothing consumes it, so it reads as honored | The test that fails when the resolution call site is deleted from `GenerateAgentLaunchers` — see [§7](#7-what-done-looks-like) |

## 7. What done looks like

A human can check all of these without reading the implementation:

1. In a workspace whose `mise.toml` pins Node 20, `pi` starts. `node --version` in the agent's own
   bash tool still reports 20, and `node --version` at the jail shell still reports 20.
2. In the same jail, `opencode` still starts (its bin must never be wrapped).
3. A program whose pack declares no floor produces a launcher byte-identical to the pre-change one.
4. A floor nothing satisfies produces one launch line naming the pack, the program, the requirement
   and what is available — and the agent still runs (possibly failing the way it does today).
5. The launch's Node-resolution disclosure does not claim to have satisfied a floor it did not.

## Open Questions

1. 💬 <a id="OQ-AR1"></a>**[OQ-AR1](#OQ-AR1): what does the declared value mean — a floor, a selector, or an exact pin?**

   This is the schema decision and it fixes everything downstream: what resolution compares, whether a
   newer image node silently satisfies an old floor, and whether the manifest must be edited when the
   image's node moves.

   <!-- vantage: oq id=OQ-AR1 leaning="A minimum (floor), because the vendor's own constraint is a range floor and a floor survives the image moving to a newer Node without a manifest edit." -->

   _Leaning:_ A **minimum version** (`22.19`), compared against the candidate's version. A mise
   selector (`node@24`) would pin harder than the package asks and would need a manifest edit every
   time the image's node moved; an exact pin would freeze the agent to a patch release for no reason
   anyone has stated.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 <a id="OQ-AR2"></a>**[OQ-AR2](#OQ-AR2): where does an interpreter the image does not provide come from?**

   The image bakes `nodejs_24` (`flake.nix:1169`), which satisfies pi today, so the *immediate* case
   needs no install at all — and that is worth stating plainly, because it means the answer here can
   be conservative. But a pack declaring a floor the image does not meet needs a decision: install
   nothing and disclose; install at boot; install in provisioning; or install lazily from the
   launcher, the way the npm cold-install already works. Boot ordering is the complication: launcher
   generation runs **before** `generate_mise_config` and before the `mise install` provisioning step,
   so a cold boot has no mise-installed node at generation time.

   <!-- vantage: oq id=OQ-AR2 leaning="Lazily from the launcher, mirroring the npm cold-install, because generation runs before mise install and before network is expected." -->

   _Leaning:_ **Lazily, in the launcher**, mirroring the npm cold-install the same script already
   performs — rather than in the generator, which would make a content generator do network I/O and
   would place the install ahead of the step that installs everything else. Not installing at all is
   the fallback if a lazy mise call proves to fight the launcher's bounded-timeout discipline.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 <a id="OQ-AR3"></a>**[OQ-AR3](#OQ-AR3): what does the user see when no available interpreter meets the floor?**

   A warning plus today's unwrapped exec, or a clear per-program failure? This decides whether an
   unsatisfiable floor is degraded or loud.

   <!-- vantage: oq id=OQ-AR3 leaning="Warn and keep today's exec: a pack is more than its program, and the existing precedent declines one launcher rather than refusing a launch." -->

   _Leaning:_ **Warn, then run unwrapped.** `launcherUnpublished` already declines one launcher with
   one line rather than refusing a launch, and a pack is more than its program. The warning must name
   the pack, the program, the requirement and what *is* available — the failure it is replacing is
   exactly a message that names none of those.

   **Answer:**
   > _(empty — fill in when decided)_

4. 💬 <a id="OQ-AR4"></a>**[OQ-AR4](#OQ-AR4): is the floor declared in the manifest, or read from the installed package?**

   Reading `engines.node` from `$NPM_CONFIG_PREFIX/lib/node_modules/<pkg>/package.json` would require
   no schema change and would track the vendor automatically. Declaring it keeps core from guessing,
   makes the requirement visible in `yolo check` and in the pack footprint, and does not make a
   vendor's typo or a range grammar into a yolo outage.

   <!-- vantage: oq id=OQ-AR4 leaning="Declared — core does not guess, and the package does not exist at yolo check time." -->

   _Leaning:_ **Declared.** The repo's rule is that a pack puts a claim in its manifest rather than
   letting core infer it, and the package is not installed when `yolo check` runs, so a derived floor
   could never be validated. A future pack MAY also choose to declare a floor that differs from
   `engines` — that is a decision, which is the point of declaring it.

   **Answer:**
   > _(empty — fill in when decided)_

## Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| AR-L1 | The fix is a **declared Node floor plus a pinned interpreter in the generated launcher** — not a startup refusal, and not a change to the workspace's mise pin | 2026-09-21 | [§3](#3-the-shape) | — |
| AR-L2 | The pin is **opt-in per `program`**, never a blanket rule for `via: npm` | 2026-09-21 | [§3.1](#31-the-declaration) | — |
| AR-L3 | The workspace pin stays authoritative for **everything except the agent's own process** | 2026-09-21 | [§4](#4-what-this-does-not-do) | — |
