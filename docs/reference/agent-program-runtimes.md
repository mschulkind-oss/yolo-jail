---
status: current
verified: 2026-09-25
verified_commit: 5a44129d
covers:
  - internal/packdecl/nodefloor.go
  - internal/entrypoint/nodefloor.go
  - internal/entrypoint/shims.go
  - internal/entrypoint/shell.go
  - internal/provision/provision.go
  - internal/cli/run/command.go
  - internal/macosuser/orchestrator.go
  - packs/pi/pack.json
  - integration/nodefloor_test.go
tags: [packs, programs, mise, node, path-resolution, agent-clis, launchers, provisioning]
summary: "Which Node runs an npm-delivered program. A `program` contribution may declare a Node floor; the launcher generator resolves one absolute interpreter that meets it (the image's node, then the newest satisfying one in the mise store) and execs the program under it, so a workspace's mise pin keeps governing everything except that one process. The provisioning stage installs an interpreter when none satisfies the floor, and refuses the launch when one still does not."
---

# Agent program runtimes — which Node runs an npm-delivered program

**Status:** CURRENT as of 2026-09-25, verified against `5a44129d` plus the uncommitted
Node-floor refusal change in the working tree that day (the refusal's exit status, the bootstrap's
four-part message, the macos-user package-floor candidate), which lands in the commit before this
doc's. **UNMEASURED:** no run of the shipped behavior is recorded. The failure it prevents was
measured in a jail on 2026-09-21. The two launches that would measure the fix are integration
tests that have not run yet ([What is measured](#what-is-measured)). The macos-user half is
[UNVERIFIED](#macos-user-unverified).

A **Node floor** is the minimum Node version a program's own entrypoint needs. A pack declares it
on the `program` contribution that installs the program. When a floor is declared, the
**launcher generator** resolves one absolute Node interpreter that meets it. The generator is the
boot step that writes `~/.yolo/bin/launch/<bin>`. The generated launcher then execs the program
under that interpreter instead of letting the program's `#!/usr/bin/env node` shebang pick one off
`PATH`. The provisioning stage installs an interpreter when nothing satisfies the floor. If one
still does not, the launch **refuses**: the jail does not start.

| Component | Lives in |
| :--- | :--- |
| The `node_floor` field, its validation, the version comparison | `internal/packdecl` (`Contribution.NodeFloor`, `Install.NodeFloor`, `ValidNodeFloor`, `SatisfiesNodeFloor`, `CompareVersions`) |
| Interpreter resolution and the availability report | `internal/entrypoint` (`ResolveNodeForFloor`, `AvailableNodes`, `DescribeAvailableNodes`) |
| The launcher splice | `internal/entrypoint` (`npmAgentLauncher`, `nodeExecPrefix`, `npmLauncherTemplate`) |
| The eager install and the refusal | `internal/entrypoint` (`GenerateBootstrapScript`, `declaredNodeFloors`, `nodeFloorChecks`) |
| The predicate the bootstrap calls | `yolo internal node-floor-satisfied` (`runNodeFloorSatisfied`, `internal/cli`) |
| The status that stops the launch | `internal/provision` (`RefusedStatus`, `Script`) |
| The two backends' handling of it | `internal/cli/run` (the stage's step order), `internal/macosuser` (`runProvisionStage`) |
| The one declaring pack today | `packs/pi/pack.json` |

**Reads with:** [`pack-system.md`](pack-system.md) (the `program` contribution kind),
[`macos-user-provisioning.md`](macos-user-provisioning.md) (the stage, its failure policy, and
the package floor), [`../research/tool-provisioning.md`](../research/tool-provisioning.md) (every
other Node-resolution layer, and why there are several),
[`../design/program-delivery.md`](../design/program-delivery.md) (the launcher and its evergreen
update), [`mise-node-dynamic-linking.md`](mise-node-dynamic-linking.md) (why the image has one node by
default). Two questions about where the refusal reaches are still open, in
[`../design/agent-program-runtimes.md`](../design/agent-program-runtimes.md).

---

## The principle — an agent's interpreter belongs to the pack

Which Node runs a program is the vendor's requirement, and the pack that installs the program is
where yolo learns it. The workspace's `mise.toml` pin is the project's requirement. It governs
bare `node`, `npx`, shebangs, the agent's own shell children and every piece of project tooling,
and nothing here changes that. It stops governing exactly **one process**: the declared program
itself.

Without a floor, the workspace pin decides both, by accident of `PATH` order. A generated npm
launcher ends by exec'ing the npm bin, which is a JavaScript file with a `#!/usr/bin/env node`
shebang. `node` then resolves through `PATH`, where the mise shims come before `/bin` (`BootPath`
in `internal/entrypoint` owns that order), and mise answers with the workspace's pin.

**The failure this prevents.** Measured 2026-09-21 against pi 0.86.1: pi's package states
`engines.node: ">=22.19.0"`, and its bundle entry imports `enableCompileCache` from `node:module`
at load time. That export does not exist on the Node 20 line (absent on 20.20.2, present on
22.23.2 and 24.19.0). So a workspace pinning Node 20 made pi fail at `import` with
`SyntaxError: The requested module 'node:module' does not provide an export named
'enableCompileCache'`. That message names a module export. It names no version, no pack and no
pin.

> [!WARNING]
> **npm does not enforce `engines`.** It warns about a mismatch and installs anyway, and the
> launcher never reads the field. That is why an unsatisfiable floor refuses the launch
> ([The refusal](#the-refusal)) rather than falling back silently: a fallback reproduces the
> original silence one layer up, while looking handled.

## The declaration

`node_floor` is an optional field on a `program` contribution, and only there. The schema is
documented in [`pack-system.md`](pack-system.md) and in `packdecl`'s doc comments, which are the
manifest reference. The value is one to three dot-separated non-negative integers, optionally
`v`-prefixed. A prerelease or build suffix is refused, because a floor the comparison cannot
model would silently accept the wrong interpreter. The field is also refused on every other kind,
because only a `program` has an entrypoint to run under an interpreter.

**The value is a minimum** ([`OQ-AR1`](#oq-ar1)). A candidate satisfies it when its version is at
least the floor, compared numerically, part by part. A missing part counts as zero, so `22.19`
means "22.19.0 or newer" and `22` means "22.0.0 or newer". A newer interpreter always satisfies an
older floor, so the image moving to a newer Node needs no manifest edit.

> [!WARNING]
> **Never compare versions as strings.** Lexically `"20.20.2" > "22.19"`, because `'0' < '2'` at
> the second character. A string compare accepts Node 20 against a floor of 22.19, which is
> exactly the failure the floor exists to prevent. `packdecl.CompareVersions` is the one
> comparison, and the shell side reaches it only through `yolo internal node-floor-satisfied`
> rather than reimplementing it.

**The floor is declared, never derived** ([`OQ-AR4`](#oq-ar4)). Core does not read the vendor's
`engines` field, because the package is not installed when `yolo check` runs, so a derived floor
could never be validated.

**It is opt-in per program** ([`AR-L2`](#ar-l2)). A program that declares no floor gets a launcher
**byte-identical** to one generated before the field existed. That is load-bearing, not tidiness:
`opencode-ai` ships a native ELF through the same `via: npm` route, and exec'ing it under `node`
would break it.

The field is projected onto `packdecl.Install` for every `via`, because it describes the
program's entrypoint rather than how the program arrived. Only the npm launcher consumes it. A
native installer's binary is not run through an interpreter.

## Resolution

The resolver returns one absolute interpreter path for a floor, or nothing. It considers
candidates in this order and takes the first that satisfies:

| # | Candidate | Why it is here |
| :--- | :--- | :--- |
| 1 | **The image's node**, at `/bin/node`, read by a bounded `--version` exec. On macos-user, which has no image, the stand-in is the **package floor's node**: each `node` in an entry of the sandbox's real `PATH` that does not lie under `$HOME`, in `PATH` order (`packageFloorNodes`) | Self-contained, always present, no install, and the node the MCP wrappers already target. It is read at an absolute path, never through `PATH`, because `PATH` would hit the mise shims first. On macos-user, dropping every entry under the home drops the sandbox's own prefixes, above all the mise shims, whose node *is* the workspace pin. `imageProbePath` in `internal/entrypoint` applies the same rule when it asks what the launch provides on that backend |
| 2 | **The newest satisfying node in the mise store**, `$MISE_DATA_DIR/installs/node/*` | Already-installed toolchains, resolved offline by path. The store's directory names are versions, so nothing is executed and nothing is fetched |
| 3 | **An interpreter the provisioning stage installs** for this floor. ⚠ It reaches the floor *check* on the launch that installs it, and the *launcher* only from the next boot on ([below](#a-stage-installed-interpreter-reaches-the-launcher-one-boot-late)) | An interpreter is environment, and the environment is provisioned before the agent runs, never on first use ([`OQ-AR2`](#oq-ar2)) |
| 4 | Nothing satisfies it | **The launch refuses** ([The refusal](#the-refusal)) |

The resolver sees candidates 1 and 2 only. Candidate 3 is not a separate lookup: the stage
installs into the mise store, and the stage's check asks the resolver again afterwards.

<a id="a-stage-installed-interpreter-reaches-the-launcher-one-boot-late"></a>

> [!WARNING]
> **A stage-installed interpreter reaches the launcher one boot late.** The launcher's
> interpreter is resolved once, when the launcher is generated, and generation is a boot step
> (`generate_agent_launchers` in `internal/entrypoint`'s `boot.go`, and in `darwin.go` on
> macos-user) that runs *before* the provisioning stage. So on the launch that installs a
> satisfying node, the floor check passes and nothing refuses, but the program's launcher was
> already written with a plain `exec "$REAL_BIN"`, and the program runs under whatever node
> `PATH` gives it, which in a workspace pinning an old Node is the failure the floor exists to
> prevent. The next boot regenerates the launcher against the now-populated store and bakes the
> interpreter. `TestAStageInstalledNodeReachesTheLauncherOnlyAtTheNextGeneration`
> (`internal/entrypoint`) pins this. No shipped floor reaches it today, because candidate 1
> satisfies pi's floor on both backends (the image's `nodejs_24`, and the package floor's on
> macos-user); a floor above the image's node would. The fix is open as
> [`OQ-AR7`](../design/agent-program-runtimes.md#OQ-AR7).

**Reading the mise store.** mise keeps alias directories beside real version directories (`24`
pointing at `24.19.0`, measured), and a name that parses as a version is treated as that version.
So an alias compares as its own floor and its `bin/node` names the same binary. On a tie, the
more specific spelling wins, because the baked path is read by a human debugging a launcher and
`24.19.0` says more than `24`. A directory whose `bin/node` is missing or not executable is
skipped: mise leaves one behind after a failed or partial install, and baking a path to a binary
that is not there turns a resolution success into an exec failure at the worst moment.

The store is `$MISE_DATA_DIR/installs/node`, falling back to the container's fixed store when the
variable is unset. That matters on macos-user, whose store is under the sandbox home: with a fixed
path, a node the stage had just installed would be invisible to the check that runs right after
it, and every such launch would refuse however the install went.

### Why resolving and installing are split

The generator may resolve (read versions, compare, bake a path) and may **not** install. Two
constraints force that split:

- **Launcher generation also runs on the host, as a dry run, under `yolo check`** (the generator
  list in `internal/cli/check`). A generator that installed would install on an observe verb.
- **Launcher generation runs before the CA bundle is generated** (both are boot steps in
  `internal/entrypoint`, launchers first), so a generator that fetched could not verify TLS.

So the install belongs to the provisioning stage, which already runs `mise install` before the
target command and is the one place that already installs over the network.

> [!WARNING]
> **A mise selector is a prefix, not a floor.** Measured 2026-09-22: `mise install --dry-run
> node@22.19` reports that 22.19.0 would install while 22.20.0 and 22.23.2 are already present,
> and `node@24` would install 24.21.0 with 24.19.0 present. A selector fetches rather than
> accepts, so it must never be used to *accept* an installed interpreter. That is why yolo
> enumerates candidates and compares them itself. The same asymmetry makes a selector exactly
> right for *installing* one, because whatever it fetches satisfies the floor, and that is how
> the bootstrap uses it.

> [!WARNING]
> **Never change `node` for anyone but the declared program.** Prepending the resolved node to
> `PATH`, exporting `MISE_NODE_VERSION`, and wrapping the exec in `mise exec node@X --` all leak
> into every child the agent spawns, so the agent's own `npm test` would stop running under the
> project's node. The resolution result is consumed by one `exec` and nothing else. A global
> mise pin would not help either: yolo owns no default runtime in mise, node being baked into the
> image (the `loadInjectedTools` comment in `internal/entrypoint`), and a global pin loses to the
> workspace layer anyway.

> [!NOTE]
> **The image's node is only knowable by running it.** The image identity file holds a content
> hash, and nothing bakes the node version into the image environment, so candidate 1 is a
> bounded `--version` exec. The probe is a package-level variable (`imageNodeVersion`) that tests
> stub. The host-side dry run under `yolo check` does **not** stub it: there the probe reads the
> host's `/bin/node` if it has one, and the store falls back to the container's fixed path. So a
> dry-run launcher reflects the host, not the jail. That is harmless to content validation, and it
> is also why the dry run is not a report of what a floor would resolve to in the jail (that
> report is [not built](#not-built)).

## The launcher

For a program that declares a floor and resolves, the npm launcher's exec gains the resolved
interpreter as an **exec prefix**, a term used in this doc and the code for the shell-quoted
absolute path plus one space, spliced in front of `"$REAL_BIN"`. For a program that declares no
floor, the prefix is empty and the exec line is unchanged byte for byte. The trailing space is
part of the value rather than the template for exactly that reason. `TestNoFloorRendersAByteIdenticalLauncher`
in `internal/entrypoint` holds it.

> [!WARNING]
> **The npm launcher has two exec paths, and both take the prefix.** Besides the main exec there
> is the **re-entry guard**'s. The re-entry guard is the branch taken when `_YOLO_LAUNCHER_ACTIVE`
> already names this binary, meaning the launcher is being re-entered from inside its own
> program. A first build spliced only the main exec, and a re-entrant launch kept running under
> the workspace's node: the exact defect, surviving in the one path nobody looks at. Any test of
> the splice asserts over *every* exec line, never the first match.

The program's **pre-launch refresh** (a pack-declared step the launcher runs just before the
exec, [`../design/pi-extension-lifecycle.md`](../design/pi-extension-lifecycle.md)) also runs
under the same prefix, so a `#!/usr/bin/env node` program is refreshed under the interpreter it
is launched under. The native launcher renders the prefix empty everywhere.

The resolved path is `shquote`'d into a bare position like every other spliced value
(`nodeExecPrefix`), so a path with a space stays one word.

**A declared floor that resolves to nothing renders a plain exec.** The generator does not
refuse, because it also runs under `yolo check`, which is an observe verb. The refusal belongs to
the launch. If the interpreter the generator baked is gone by exec time (the store was pruned
between boot and use), the launcher's existing `[ -x "$REAL_BIN" ]` test and the exec's own
failure are the last word.

## The refusal

**If a selected pack declares a program whose floor nothing satisfies, after the provisioning
stage has had its chance to install one, the launch refuses** ([`OQ-AR3`](#oq-ar3)). A jail that
cannot run what was selected is not a ready environment, and reporting success while leaving it
unready states a result that was not achieved. The host notch applies the same rule in
`internal/cli/applyhostdepgate.go`'s header.

**The predicate is keyed on a pack, not on an agent.** Core has no agent registry and cannot tell
`yolo -- claude` from `yolo -- bash`, so the test is "a selected pack declares a `program` whose
declared floor no available interpreter satisfies". Nothing here may ask whether the program is
an agent.

**The message names four things**, because the failure it replaces names none of them: the
**pack**, the **program**, the **floor**, and **what is available**.

How it works, on both backends:

1. **The generator bakes the checks.** The bootstrap script carries one check per *distinct*
   floor, sorted, with every declaring `program <bin> (pack <name>)` beside it. Duplicate floors
   merge into one install, but the declarers do not merge away: a refusal naming only the first
   of two programs would send the user to drop one pack and meet the same refusal again. The
   checks are baked, not read from the environment, because macos-user runs the stage under
   `env -i`, where an inherited variable is silently empty.
2. **The bootstrap installs, then asks again.** For each floor it asks
   `yolo internal node-floor-satisfied <floor>`. If the answer is no, it runs
   `mise install node@<floor>` and asks again. The predicate exits 0 when satisfied, and 1 when
   not, printing on stdout what *is* available (`DescribeAvailableNodes`, which lists the same
   candidates in the same order the resolver considers, one line per distinct binary). It exits 2
   on misuse, including a floor it cannot compare. Any status other than 0 or 1 is reported as
   "the predicate could not answer" rather than as "none available", because "none" would be a
   claim nobody measured.
3. **The refusal is an exit status.** A floor still unmet sets a flag, and the bootstrap's last
   act is to exit `provision.RefusedStatus`, which is rendered from the Go constant into the
   script so the producer and the consumer cannot disagree about the number. It is the one
   status `provision.Script` passes through **unconditionally**: non-interactively, and at a
   terminal without offering to continue. Every other stage failure still degrades as before.
   The `PROVISIONING FAILED` marker is still written first, since a refusal is a failed
   provision and both readers of the log should say so.
4. **The target is never reached.** On the container, the stage is spliced into the top-level
   `bash -c`, so the wrapper's `exit` ends the command before the Executing banner and the target,
   and the container's exit status is the refusal's. On macos-user, `runProvisionStage` reads the
   refusal status plus the marker in the log as a refusal, stops the launch, and says it was a
   refusal rather than reporting a veto nobody gave
   ([`macos-user-provisioning.md`](macos-user-provisioning.md#a-failing-stage-does-not-abort-the-launch)).

**The floor check is last, and so is the bootstrap.** The check sits at the end of the bootstrap
script, and the bootstrap is the last step of the stage on both backends. So a refusal costs no
unrelated work: the MCP preset installs above it still run, and the container's venv step runs
before the bootstrap rather than after it. The steps are joined with `&&`, so a step placed after
the bootstrap would be skipped by a refusal.

> [!WARNING]
> **This is the one fatal in the provisioning class, and the cost is real.** Every neighbouring
> failure there degrades: a failed `mise install` prints `PROVISIONING FAILED` and continues
> unless a human at a terminal says no, an offline boot's bootstrap failure simply retries next
> launch, and the launcher's own install is best-effort. So an offline boot that cannot fetch a
> satisfying interpreter refuses where it used to continue. What makes that affordable is that
> the path is rare: the image bakes `nodejs_24` (`coreFloorNames` in `flake.nix`), which
> satisfies the only declared floor today at candidate 1, with no install and no refusal.

> [!WARNING]
> **The refusal can fire for a program this launch was never going to run.** It needs a declared
> floor that *nothing* satisfies, which is narrow, but it is keyed on selection, not on use. If it
> is seen firing in practice, report that. Do not widen the resolution to hide it.

**There is no escape hatch.** A hatch exists here for broken user configuration, never to paper
over a yolo defect, and a user facing this refusal can fix it where they are standing: drop the
pack, or make a satisfying interpreter available (which needs the network to install one). A
terminal prompt offering to continue anyway would be that hatch, which is why `provision.Script`
tests for the refusal before it would prompt.

### Where the refusal does not reach

Two cases start a jail the ruling says should not start, and a third starts one whose launcher
does not yet honor the floor. All three are open questions, held in the design stub:

- [`OQ-AR5`](../design/agent-program-runtimes.md#OQ-AR5): **macos-user starts no provisioning
  stage without `mise_tools`** (`ProvisionNeeded` in `internal/macosuser`), so a workspace
  selecting a floor-declaring pack with no `mise_tools` gets neither the install nor the refusal.
- [`OQ-AR6`](../design/agent-program-runtimes.md#OQ-AR6): **a failed `mise install` skips the
  bootstrap**, because the stage's steps are joined with `&&`, so no floor is checked and the
  launch degrades like any failed stage.
- [`OQ-AR7`](../design/agent-program-runtimes.md#OQ-AR7): **the launch that installs a
  satisfying interpreter passes the check with a launcher that does not exec it**
  ([one boot late](#a-stage-installed-interpreter-reaches-the-launcher-one-boot-late)).

## What this does not do

- **It does not change the workspace pin.** Bare `node`, `npx`, shebangs, the agent's shell
  children and project tooling resolve exactly as they did.
- **It does not discourage pinning an old Node.** Pinning Node 20 for a project is correct.
- **It does not refuse because a workspace pin is old.** That case is what the floor makes
  irrelevant, and a refusal there would turn a legitimate project into an unlaunchable jail. It
  refuses only when *no* interpreter anywhere satisfies a declared floor, which is a fact about
  the jail rather than about the project, and which no project file would fix.
- **It does not read the vendor's `engines` field**, at runtime or at generation
  ([`OQ-AR4`](#oq-ar4)).
- **It does not cover the native installer route** (`via: installer` programs), which installs a
  native binary with no interpreter to choose.
- **It does not change a program with no declared floor**, in any byte of its launcher.

## macos-user: UNVERIFIED

`flake.nix` puts `nodejs_24` in `coreFloorNames`, the image's core list that macos-user's native
package floor is derived from, so that backend should have a node that satisfies today's floor at
candidate 1, found through the sandbox's `PATH` (the `YOLO_DARWIN_LOGIN_PATH` variable). That is
inferred from `flake.nix` and the sandbox's `PATH` composition, **not measured on a Mac**. The
only hardware measurement on record for `via: npm` there, `npm: command not found`, is dated
2026-09-11, the day before the floor shipped, and
[`macos-user-provisioning.md`](macos-user-provisioning.md#what-each-imperative-config-key-delivers-here)
still marks the npm route there as not measured on hardware. No backend-specific carve-out was
added, and nothing has shown whether one is needed. Settle it on a Mac, with a floor-declaring
program, before building anything backend-shaped here.

## What is measured

Nothing about the shipped behavior has been watched running. The failure it prevents, and each
measured claim above, carry their own dates. These are the checks that would measure it:

| Check | Instrument | State |
| :--- | :--- | :--- |
| A floor nothing satisfies refuses the launch in one message naming pack, program, floor and what is available, and the target never runs | `TestAnUnsatisfiableNodeFloorRefusesTheLaunch` (`integration/nodefloor_test.go`) | written, not yet run |
| In a workspace whose `mise.toml` pins Node 20, pi's launcher execs an interpreter meeting pi's floor on both exec paths, while the shell's `node` stays at 20 | `TestANode20WorkspacePinDoesNotChooseThePiInterpreter` (same file) | written, not yet run |
| `opencode` still starts in that same jail (its bin is never wrapped) | none; the unit tier pins the byte-identical launcher | not measured |
| A floor the image does not meet is satisfied by an interpreter installed during provisioning, so a program's first invocation downloads nothing. On that launch the launcher does not yet exec it ([one boot late](#a-stage-installed-interpreter-reaches-the-launcher-one-boot-late)) | the unit tier runs the bootstrap against fakes, and pins the one-boot-late launcher (`internal/entrypoint`) | not measured in a launch |
| The macos-user node | a Mac | [UNVERIFIED](#macos-user-unverified) |

<a id="not-built"></a>

### Not built

- **A launch disclosure of the chosen Node.** The launch does not say which interpreter a floor
  resolved to, so the only record is the generated launcher itself.
- **A `yolo check` report of what a floor would resolve to**, without installing anything. The
  host-side dry run resolves against the host rather than the jail and reports nothing
  ([the note under Resolution](#why-resolving-and-installing-are-split)).

## Why it's this way

Each row is a ruling a maintainer could undo on purpose, kept under its original id because Go
comments and sibling docs cite it.

| Ruling | Why |
| :--- | :--- |
| <a id="ar-l2"></a>[`AR-L2`](#ar-l2): the interpreter pin is **opt-in per `program`**, never a blanket rule for `via: npm` | `opencode-ai` installs a native ELF through `via: npm`, measured. Wrapping every npm program in `node` would break it. |
| <a id="oq-ar1"></a>[`OQ-AR1`](#oq-ar1): the declared value is a **minimum**, compared against each candidate's version | The vendor's own constraint is a range floor, and a floor survives the image moving to a newer Node without a manifest edit. A mise selector cannot express it: selectors are prefixes that fetch rather than accept, measured. |
| <a id="oq-ar2"></a>[`OQ-AR2`](#oq-ar2): the install is **eager, in the provisioning stage; there is no lazy path**, and resolution is split from installation | An interpreter is environment, and the environment is provisioned before the agent runs. The generator cannot install because it also runs under `yolo check` and before the CA bundle exists. This governs *readiness* and does not reverse [`OQ-PD12a`](../design/program-delivery.md#decision-ledger), which governs whether the program's own binary is current. |
| <a id="oq-ar3"></a>[`OQ-AR3`](#oq-ar3): an unsatisfiable floor **refuses the launch**, naming pack, program, floor and what is available, with **no escape hatch** | If a pack is selected, the jail must be able to run what it declares. It is the one fatal in a class whose neighbours all degrade, and both that cost and the residual objection ([The refusal](#the-refusal)) were accepted rather than engineered away. |
| <a id="oq-ar4"></a>[`OQ-AR4`](#oq-ar4): the floor is **declared** in the manifest, not read from the installed package | Core does not guess, and the package does not exist when `yolo check` runs, so a derived floor could never be validated. |

## Current values

Verified at the commit in the header. The prose above explains what each of these is for; this
table is the only place the exact values are stated.

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| pi's declared floor | `22.19` | `packs/pi/pack.json` |
| The image's node | `/bin/node` | `imageNodePath`, `internal/entrypoint` |
| Image and package-floor node probe timeout | 2s | `nodeVersionAt`, `internal/entrypoint` |
| The mise node store | `$MISE_DATA_DIR/installs/node`, else `/mise/installs/node` | `miseNodeStoreDir`, `internal/entrypoint` |
| The container's `MISE_DATA_DIR` | `/mise` | the launch's `-e` list, `internal/cli/run` |
| macos-user's sandbox `PATH` variable | `YOLO_DARWIN_LOGIN_PATH` | `DarwinLoginPathEnv`, `internal/entrypoint` |
| The refusal's exit status | `78` (`sysexits.h`'s `EX_CONFIG`) | `provision.RefusedStatus` |
| `yolo internal node-floor-satisfied` exit statuses | `0` satisfied, `1` not (availability on stdout), `2` misuse | `runNodeFloorSatisfied`, `internal/cli` |
| The launcher's exec-prefix sentinel | `__YOLO_EXEC_PREFIX__` | `npmLauncherTemplate`, `internal/entrypoint` |
| The image's baked node package | `nodejs_24` | `coreFloorNames`, `flake.nix` |
