---
status: current
verified: 2026-09-09
verified_commit: a3922298
covers:
  - internal/loopholes/
  - internal/loopholedecl/
  - internal/packload/loopholesource.go
  - internal/packload/hostaccess.go
  - internal/packstage/loopholeowners.go
  - internal/cli/run/packloopholes.go
  - internal/cli/run/loopholeinert.go
  - internal/cli/run/loopholeretire.go
  - internal/cli/run/loopholesruntime.go
  - internal/cli/check/sections_loopholes.go
  - internal/config/validate_loopholes.go
  - internal/config/loopholeplacement.go
  - internal/render/fieldset.go
  - packs/
tags: [loopholes, packs, activation, trust, config]
summary: "How a loophole gets onto a machine and how it turns on: the `loophole` contribution kind that lets a pack ship one, the three-predicate activation model (Enabled / Active / Honored) whose default is OFF, the pack-shipped subset a distributed manifest is held to, the total crossing enumeration behind `yolo pack footprint` and the per-launch disclosure, the install/enable scope split and the placement rule that makes it sound, and the reports for a loophole that will do nothing on this machine."
---

# The loophole system — packaging, activation and disclosure

**Status:** CURRENT as of 2026-09-09, verified against `a3922298`.

A **loophole** is a single controlled permeability point between a jail and the host: a
declared, narrow passage through the wall, and the only extension point that can put a
process on the real machine. This document covers how one *arrives* on a machine, how it
*turns on*, what the user is *told*, and where it does nothing at all. The wire it speaks
is [`loophole-protocol.md`](loophole-protocol.md); how a jail reaches it is
[`loophole-transport.md`](loophole-transport.md).

Two sentences carry most of the design. **Every loophole yolo ships is a pack's, and there
is no other channel** — there is no loophole registry, no reserved namespace, and no
built-in service compiled into the run pipeline. And **presence never activates**: a
loophole is on only because something said so, and a manifest that says nothing is off.

| Component | Lives in |
| :--- | :--- |
| The manifest schema as a leaf: decode, static validation, enums, the pack-shipped subset, claim-value sanitation | `internal/loopholedecl` (`Manifest`, `Decode`, `DecodeTolerant`, `Setting`, `packshipped.go`, `sanitize.go`) |
| Resolved records, the predicates, discovery, the converged set, supersession | `internal/loopholes` (`Loophole`, `Set`, `Discover`, `NewHostSet`, `Active`, `Honored`, `MayRunHostCode`, `Superseded`) |
| Container argv, daemon specs, `doctor_cmd` execution, the inert report | `internal/loopholes` (`RuntimeArgsFor`, `ManifestHostDaemonSpecs`, `RunDoctorChecks`, `InertNote`, `PlatformInertNotes`) |
| The placement rule, both faces | `internal/config` (`loopholeplacement.go`), `internal/loopholes` (`placement.go`) |
| Crossing enumeration for a pack's loophole | `internal/packload` (`loopholesource.go`, `LoopholeHostAccessClaims`), merged by `Pack.HostAccessClaims` |
| The launch disclosure, the spawn boundary, the inert lines, name exclusivity | `internal/cli/run` (`disclosureClasses`, `startLoopholesDisclosed`, `loopholeinert.go`, `PackLoopholeNameConflicts`) |
| State retirement on deselect, and its sweeper | `internal/packstage` (`RetireLoopholeState`), `internal/cli/run` (`loopholeretire.go`), `internal/prune` (`PruneRetiredLoopholeState`) |
| Config validation, scope refusals, workspace-switch disclosure, settings | `internal/config` (`validate_loopholes.go`, `validate_loopholesettings.go`, `WorkspaceLoopholeSwitches`) |
| The host-target refusal and the jail-census exclusion | `internal/render` (`fieldset.go`) |
| The shipped loopholes themselves | `packs/*/loopholes/*/manifest.jsonc` |

**Reads with:** [`../guides/loopholes.md`](../guides/loopholes.md) — the authoring guide,
and the user-facing half of everything here: the manifest keys one by one, the config
block, the CLI, and a worked example. This document does not restate it.
[`pack-system.md`](pack-system.md) is the framework a loophole is a contribution to
(kinds, collisions, selection, the credential boundary, a pack's own config keys);
[`loophole-transport.md`](loophole-transport.md) and
[`loophole-protocol.md`](loophole-protocol.md) are the layers below.
`yolo config-ref` is the authority for config keys.

---

## Principles

The six activation rulings keep their original ids, because sibling docs, code comments
and shipped manifests cite them by number.

**R1. Presence never activates.** A loophole is active only if something said so.

**R2. A pack declares its loophole's default state, and that declaration defaults to
disabled.** `default_enabled`, on the loophole manifest. **Absent means off.** This is
what lets a pack do the right thing by default without yolo guessing on its behalf.

**R3. The sniffing mechanism is not the way to decide activation.** `requires` answers
*"can this machine run it"*, never *"should it be on"*. A loophole whose program is
missing should fail loudly at spawn, not vanish silently from a list.

> [!WARNING]
> **R3 is narrower in the code than it reads.** `requires.command_on_path` is **alive** —
> parsed, type-checked, inside the pack-shippable subset, and declared by a shipped manifest
> (`packs/host-processes` requires `ps`). What was deleted is that key *from the broker's
> manifest*, where a host-`PATH` probe stood in for a dependency pack selection now
> expresses directly. Read R3 as being about that use, not about the schema.

**R4. Host access is never on by default.** Being useful is not a reason to be automatic.

**R5. Install is user-scope; enable is either scope.** `packs` is read from the user
config only, install-shaped keys are refused in workspace scope, and
`loopholes.<name>.enabled` is honored from both. So a workspace may switch on only what
the user already installed — the weak, agent-editable scope is bounded by the strong one,
which is what makes per-workspace enablement safe to offer at all.

> [!CAUTION]
> **R5 is FALSE for list-shaped settings, and that is why a setting declares its own scope.**
> Config merge union-merges every list at every depth, so a user-scope *ceiling* that a
> workspace *narrows* is inexpressible — a workspace can only **widen**, which for an allowlist
> inverts the intended property. R5 holds for a scalar switch and not for a list; the answer is
> the per-key `scope` field on a declared setting ([Settings](#settings)), and the same
> correction is recorded as `R5` in [`pack-system.md`](pack-system.md#why-its-this-way) — a
> different `R5` from this document's, in the one place the two vocabularies touch.

**R6. A loophole that exists only to serve one agent is a contribution of that agent's
pack.** The dependency is structural, so selecting the pack *is* the dependency — and no
`PATH` probe standing in for it can answer correctly. A separate pack would reinstate the
selection step this deletes.

Three more principles come from the packaging half.

**A claim-free crossing must be unrepresentable.** Every declaration that reaches the host
emits its own string: the daemon it runs, each host it intercepts, each path it mounts,
each socket it connects you to, each device it passes through, each CA it installs. This is
the load-bearing rule of the whole design, and the reason is a short-circuit: a
crossing-set that is empty reads as *"nothing to tell you about"*, so a loophole that
crosses the boundary and claims nothing would cross it silently.

**The transport belongs to the framework, not to the loophole.** A loophole never
implements TLS, never opens a port, and never publishes a credential — see
[`loophole-transport.md`](loophole-transport.md).

**A rule with one exception is two rules.** This is why nothing yolo ships is exempted
from activation, from the subset, or from the name pre-flight, and why removing the last
exemptions was the point of the work rather than tidying after it.

## Vocabulary

- **Loophole** — a host capability deliberately exposed into a jail through a mediated,
  declared channel. Not a sandbox escape (that is a bug, and undeclared) and not a plain
  network route (that carries no policy).
- **Module directory** *(coined in the packaging design this reference replaces)* — the
  on-disk unit a loophole *is*: a directory holding `manifest.jsonc` plus whatever else
  that loophole needs. The shape is identical wherever it comes from, which is what makes
  a loophole developable standalone and then droppable into a pack unchanged.
- **Contribution** — one entry in a pack's `contributes[]`. A loophole arrives as
  `{ "kind": "loophole", "from": "loopholes/<name>" }`, which **points at** a module
  directory rather than inlining a manifest.
- **The pack-shipped subset** — the narrower manifest vocabulary a *distributed* loophole
  is held to. Not a different schema: the same decoder plus a set of refusals.
- **Crossing** — one thing a loophole reaches on the host. **Claim** — the single string
  that discloses one crossing. The two are one-to-one by construction.
- **Inert** — a loophole that will do nothing on this machine, for a categorical reason
  (this platform, this backend) rather than a probe result. Distinct from *inactive*,
  which is the probe answer.
- **The two verbs** — **install** (this code may run on this machine at all) and **enable**
  (an installed loophole is active for this jail). The scope split between them is R5.

## Activation

### Three predicates, and what each one means

| Predicate | Means |
| :--- | :--- |
| `Enabled` | *the user's switch* |
| `Active` | `Enabled` **and** the machine can run it |
| `Honored` | `Active` **and** this record came from a resolved pack set |

`Active` is four gates — `Enabled`, then `!Superseded()` (a selected pack says the job no
longer needs doing: a field read over `serves` / `supersedes`, decided once at discovery, whose
mechanism is [`pack-system.md`](pack-system.md#capabilities-and-supersession)), then
`SupportedHere()` (the
`platforms` declaration), then `RequirementsMet()` (the `requires` probes, and the only
gate that touches the world). **The order is load-bearing and `InactiveReason` repeats
it**: cheapest and most categorical first, so no gate does work in service of a message a
later one would have replaced — and supersession before every machine fact, because an
unexplained disappearance must say *which* pack turned it off, not report a missing binary
that may also be true and is not the answer the reader needs.

> [!WARNING]
> **`Honored` no longer means what its name suggests.** It was `Active` plus a per-pack
> **origin gate**: a fetched pack ran nothing on the host until the user approved its claims.
> That gate — prompt, lockfile record and launch check — is **deleted**, because naming a pack
> in user-scope config already requires more authority than the approval withheld. What the
> predicate now refuses is a caller that assembled a slice of records **without resolving
> packs at all**: a slice carries no provenance, so the check cannot be forgotten only if it
> lives inside the function that acts on the records. Every production path passes. Do not
> read a `Honored` call site as an approval check, and do not delete it as vestigial.

**The surfaces that PERFORM a crossing do not filter on `Honored`** — the container argv
builder and the daemon-spec builder enforce the gate *inside themselves*, and so does
`doctor_cmd` execution, because a filter the caller must remember to apply is a filter the
next caller omits. `Honored` exists for the surfaces that **describe** what crossed, above
all the **briefing**, which is instructions the agent acts on: advertising a loophole that
never crossed sends the agent to debug host wiring that was deliberately withheld.

### `default_enabled` versus `enabled`

Two different keys with two different owners, and they used to share one spelling on
opposite defaults with nothing saying which won.

- **`default_enabled`**, on the **manifest** — the *pack author's* default. Absent means
  **false** (R2). Type-checked, not coerced.
- **`loopholes.<name>.enabled`**, in **config** — the *user's* answer. It outranks the
  author's default in **both** directions, from either scope, because the block it is read
  from is already user-plus-workspace with the workspace winning. Coerced truthily, because
  the config layer above has already refused a non-boolean.

Whether the key was **written at all** is load-bearing: *"the config said true"* and *"the
config said nothing"* are different answers, and only the second may leave the author's
default standing. **One shared reader** answers that for the launch and for `yolo check` —
when there were two, the reporting surface answered off the manifest default alone, so a
loophole the user had switched **on** rendered as the greenest line in the section while its
host daemon went unprobed.

> [!WARNING]
> **The manifest's old `enabled` key is recognized and REFUSED, never tolerated.** The
> rename flipped the default, so silently dropping `"enabled": true` would leave the
> loophole off, and silently dropping `"enabled": false` would make a loophole its author
> disabled look like one that merely said nothing. Both readings are defensible, which is
> exactly why the manifest must not be guessed at.

> [!WARNING]
> **Reverse skew is real and is not solved in this repo's code.** An *older* yolo reading a
> newer manifest does not know `default_enabled`, tolerates it as an unknown key, and falls
> back to enabled-defaults-**true** — so a manifest shipping a loophole meant to be off runs
> it **on** under a build that predates the rename. That is a security regression delivered
> by an upgrade of the *content* rather than of the binary. The generic unknown-key skew
> note cannot help: its wording tells the reader a *newer* build will read the key, which is
> the exact opposite of the truth for a removed one.

### Disclosure of the user's switch, in both directions

`enabled` is writable at workspace scope by design (R5), and a workspace file is
agent-editable — so **disclosure is the protection, not scope**. One function answers
*"what did workspace scope say about this switch"* in both directions, returning the file
and the value; absence means *"workspace scope said nothing"*, because a zero value would
read as a disable nobody wrote.

Two surfaces consume it: a launch-time line naming the loophole **and the file**, and
`yolo check`, which **warns rather than passing green**. Two details worth keeping. The
`yolo check` row for an ON **discloses and then falls through**, where the OFF row stops —
off means there is nothing left to measure, on means the loophole is about to run and its
`doctor_cmd` is the next thing a reader wants. And a workspace file that merely **restates**
the manifest default is disclosed too: the launch surface has no default in reach, and two
disclosures contradicting each other over one file is worse than one redundant line.

### `requires`, `platforms`, and the difference

`requires` is a **runtime probe** — *the thing I need is present* — and it does not decide
activation on its own. `platforms` is a **categorical declaration** — *I only exist for
this platform* — spelled in Go's own `<goos>` / `<goos>/<goarch>` vocabulary against a
**closed** list on both halves.

The closed list is the whole point: under an open one, `["darwins"]` is a loophole
supported nowhere, on every machine, forever, with no message — the silent-nothing shape
the field exists to end. Absent means every platform, so a manifest written before the key
keeps its meaning; an explicitly **empty** list is an error, because honoring it literally
makes the loophole inert everywhere and honoring it loosely ignores what the author wrote.
The declaration is static and its **evaluation** is a pure function of the target pair: the
schema package never reads the running platform, because a leaf that reads the world grows
an import.

> [!WARNING]
> **The unsupported-platform message must say that nothing is missing.** The failure the
> field exists to fix is a Linux-only daemon reported on macOS as an unmet `requires`,
> which reads as *"install the missing thing"* — advice that can never succeed. A reader
> who is not told otherwise spends the afternoon proving there is nothing to install.

> [!WARNING]
> **An unknown `platforms` KEY is tolerated; an unknown GOOS/GOARCH VALUE is not.** So a
> value only a newer Go knows is a refusal on an older build. That is the version-skew
> shape that has bricked jails before, and it is why the closed lists live beside the enums
> they resemble.

### Inside a jail, presence decides — deliberately

Inside a jail, `requires` is answered by whether the bind mount landed, which is presence
deciding activation. That is not an R1 violation: **the mount IS the host's decision made
visible.** Per-mount presence skipping is the same reasoning one level down — a declared
bind mount or device whose host path is absent is **skipped with a warning**, which is
adaptation inside a capability the user already consented to.

> [!WARNING]
> **Do not "fix" either branch.** Someone implementing R1 by grepping for presence checks
> will read them as violations and break every jail-side evaluation.

## The `loophole` contribution kind

`{ "kind": "loophole", "from": "loopholes/acme-proxy" }`. `from` is **required** and runs
through the traversal guard every path-bearing field of every kind gets, so absolute paths,
`..` and `:` are refused as a security property.

- **The loophole's `name` must equal the module directory's basename**, enforced by the
  loader. That is what lets the name be known — and a crossing keyed by it — **without
  decoding the manifest**, which is what makes the pre-flight and the fail-closed
  unreadable-manifest case possible.
- **Combine: Exclusive, by loophole NAME.** Exclusivity is per name, not per pack, so a
  pack shipping three loopholes is ordinary.
- **A name collision is FATAL**, naming both sides and both declared `from` values.

**A missing `from` refuses; an unloadable `manifest.jsonc` warns**, and that is a *layer*
split rather than an inconsistency. A missing `from` is a pack-manifest error, decidable
without loading any loophole, in a tree the user explicitly selected: refusing is a fix.
An unloadable manifest is discovered in the loophole loader, whose contract across every
source is warn-and-continue, because one bad manifest must not take the others down with
it.

> [!WARNING]
> **Name exclusivity cannot be enforced inside discovery, and the generic collision pass
> does not cover it.** Discovery has **no error channel** by contract, and the generic
> footprint pass skips single-pack groups — so one pack declaring two `from` paths with the
> same basename collides with *itself* and goes unreported there. Exclusivity is therefore its
> own **launch pre-flight**, per declaration, beside the file-destination, shadowed-surface
> and config-surface pre-flights, returning an error so the launch refuses. It reports the
> declared `from` beside the resolved directory, because a reader told *"two claims on
> `acme`"* needs the two manifest lines to edit.

### Where the schema lives, and why it is a leaf

`internal/loopholedecl` is the manifest schema as a **leaf**: decode plus static
validation, no `PATH` lookup, no `stat`, no predicate evaluation. `internal/loopholes`
reads manifests *through* it and re-exports the vocabulary as **type aliases**, not new
definitions — the call sites are talking about the same things, and two definitions would
be two things a manifest could disagree with.

The split is stated where a reader picks a package: reach for `loopholedecl` to **read** a
manifest (the footprint, `pack lint`, a host-side validator — none of which may import the
runtime), and for `loopholes` to get a **resolved** loophole (paths substituted, `requires`
evaluated, container argv emitted). The cycle is measured — `loopholes` → `config` →
`packload` — and it is why the extraction happened rather than the edge being broken.

### Strict and tolerant, and why both

- **Strict** refuses an unknown key: an author must hear about a typo.
- **Tolerant** skips an unknown key and **reports it by name**, so a version boundary
  degrades audibly. A manifest yolo cannot fully understand must not make a pack's loophole
  vanish.

The key census is one vocabulary per object, so the walk and the census cannot disagree
about what is known; environment-variable maps are outside it, because every key in them is
known by construction. `version` is *recognized and read by nothing*, which is a different
and better state than declared-and-unnoticed.

**Which strictness a surface uses is a decision, not a detail.** Crossing enumeration reads
**tolerantly** — refusing a manifest there would turn a working loophole into an unreadable
one, and tolerant enumerates exactly what *this* build understands, which is exactly what it
will honor, so the claim set and the effect cannot disagree. The strict read belongs to
`pack lint`.

> [!WARNING]
> **An unknown contribution KIND must stay tolerated at the jail boundary.** The in-jail
> entrypoint is baked into the image and can be a release behind the host CLI; when an
> unknown kind failed structural validation, the first pack to declare a new kind **bricked
> the jail** — the boot refused, three times in this repo's history for three different
> new kinds. The tolerant decoder skips it and reports it by name, and the boot warns each
> one so the degradation is audible. The alternative — "require a host reload first" — is
> not a mechanism, it is a hope, and it cannot be stated to a third-party author at all.

### Two module-dir tokens, and value sanitation

`{loophole_dir}` resolves **host-side** to the staged module directory;
`{jail_loophole_dir}` resolves to the container path the module directory is mounted at.
Two tokens rather than one, each **refused in the wrong half at load**, because one token
with two resolutions is the kind of asymmetry an author discovers by debugging.

**Every value that feeds a claim is sanitized at load**, not escaped at display: control
characters, DEL, the C1 range and invalid UTF-8 are refused in every field a claim is built
from, the loophole's own name included. The reason is a real attack — disclosure lines are
rendered through a style formatter that rewrites only recognized tags, so raw escape bytes
and newlines pass through, and a manifest could inject fake disclosure lines or erase the
header of the one screen the trust story rests on. Refusing at load is one gate, and the
author hears about it.

> [!WARNING]
> **A field that starts feeding a claim must join the sanitation list in the same change.**
> Description text and environment keys are deliberately *not* sanitized, because they feed
> no claim target today and widening the refusal to fields with no consumer rejects
> manifests for no reason.

## The pack-shipped subset

A *distributed* manifest is held to a narrower vocabulary than one yolo ships itself — and
since every loophole yolo ships is now a pack's, **the subset applies to every manifest yolo
reads.** The refusals live in `internal/loopholedecl/packshipped.go`, each carrying the
message that names what to write instead, and **every** problem is reported rather than just
the first, because these are independent declarations rather than a parse.

What is refused, and the reason each rule survives:

| Refused | Why | Write instead |
| :--- | :--- | :--- |
| `jail_env` | it emits container environment variables, colliding with the `env` kind's target namespace, and cross-kind collisions are not detected | the `env` kind — at the cost of the variable becoming **unconditional** |
| `host_bind_mounts[].readonly: false` | a writable bind is a wider grant than the claim vocabulary distinguishes — and see the socket caveat below | a read-only bind, or a daemon that mediates |
| `publishes: "endpoint"` (including the **default**, which decodes to it) | the enforcement asymmetry in [`loophole-transport.md`](loophole-transport.md#two-server-shapes-and-how-a-manifest-selects-one) | `publishes: "socket"` |
| an absolute or `$VAR` path in `ca_cert` or `requires.file_exists` | a `ca_cert` is a **trust install**, not a read: it is bind-mounted from the host *and* joined into the jail's node CA bundle, so an absolute value hands every node client in the jail a certificate authority the user never chose. A `file_exists` probe crosses nothing, but its **answer leaks** — the resolved path is printed as an inactive reason, turning an unscoped field into a host-filesystem probe with a readout | a relative path the pack ships, or a state-directory path |

`command_on_path` is untouched: it asks `PATH` whether a *program name* resolves, and the
answer names something installable.

**Pack-shippedness is the CALLER's fact, not the manifest's** — expressed by which loader is
called, never by a field, because a manifest cannot declare that a pack shipped it (it would
simply lie) and every reader already knows which source it came from. The reporting
projection goes back *through* the schema package rather than reimplementing the rules,
because two checkers over one subset is how a refusal and a disclosure string come to
disagree about what a pack may ship.

> [!WARNING]
> **`:ro` is not a boundary for a Unix socket. Measured, twice.** A read-only bind of an
> AF_UNIX socket is fully connectable and bidirectional — the kernel's read-only check exempts
> non-regular inodes; this is the well-known container-socket result. So the `readonly: false`
> refusal buys nothing for sockets, which is why a socket bind is **its own crossing class**
> and why the plain mount class's text carries the socket caveat verbatim instead of claiming
> "read-only" and stopping.

> [!WARNING]
> **Do not reintroduce a path rule over `host_bind_mounts[].host`.** One existed and its two
> cases were **inverted**: it permitted everything under `$HOME` and refused a session
> runtime-dir socket, so it admitted `~/.ssh` and blocked a PulseAudio socket. It was
> **withdrawn**, not widened — and the tempting fix, a closed yolo-resolved socket vocabulary,
> is an allowlist wearing an extension point's clothes, because every new socket would need a
> yolo release. What survives is a **correctness** rule, not a gate: normalize, resolve `..`,
> and refuse a declaration whose resolution is not stable between disclosure and mount — *"does
> what you were told equal what I mount"*, which yolo must guarantee, rather than *"is this path
> allowed"*, which yolo cannot judge for a user.

> [!WARNING]
> **"Or a daemon that mediates" is not an answer for a host socket the user's own session
> already exposes.** Spelled out, it means writing a proxy daemon — trading one read-only bind
> for **arbitrary host execution plus a disclosure that says so**. A rule whose escape hatch is
> "run code on the host instead" pushes authors toward the sharpest capability in the system in
> order to obtain the mildest one.

Both directions of the subset are pinned by test, so the finding cannot rot into an opinion: one
asserts an audio-shaped manifest draws exactly the refusals it should, another that the shipped
manifests stay inside the subset.

## Trust: what is gated, and what is not

### The two verbs

| | **INSTALL** | **ENABLE** |
| :--- | :--- | :--- |
| Decides | that this code may run on this machine **at all** | whether an installed loophole is **active for this jail** |
| Scope | **user only** | **user or workspace** |
| Performable by | a human editing a file no agent can write | anyone who can edit the workspace config — including an agent |
| Cost of getting it wrong | arbitrary host execution | a vetted daemon runs for a repo you did not intend |

**The line is drawn where the risk is.** The hazard was never *"a daemon runs"* — it is
*"code nobody vetted runs"*. Install is the vetting point; enable is routing **within an
already-vetted set**, so it is safe in a file an agent can write: the worst an agent
achieves is switching on something a human already looked at.

Applied to the config block's two entry shapes — an **inline** entry declares a loophole and
must carry a `command`; an **override** entry adjusts one that already exists:

| Key | Verb | Scope | Why |
| :--- | :--- | :--- | :--- |
| `command` (inline) | install | **user** | it *is* the host execution |
| `doctor_cmd` | install | **user** | a second host execution, run by two commands users treat as read-only preflight |
| `env` — **either shape** | install | **user** | changes **what** runs, not whether: an override entry reaches a **first-party** daemon's spawn environment, so a workspace file could inject a preload into a daemon yolo itself starts |
| `enabled` | enable | **either** | changes **whether** already-installed content is active |
| `jail_env` | — | **either** | container-side only; no host effect |

A workspace that enables an **uninstalled** loophole **fails the launch**, naming the file
that asked and what installing it would grant. That is better than the every-launch warning
it replaced, which neither worked nor stopped. Any offer to install must require a real
terminal and fail closed without one, or a workspace could drive its own promotion.

### The placement rule

**Every gate governs a DECLARATION; none governs the FILE the declaration names.** The
workspace is bind-mounted read-write and agent-writable, so the one artifact that actually
executes is the one no declaration-keyed gate reads.

**The answer is not hashing, and not a confirmation.** Writing user-scope config already
demands host access as the user, who could equally use a shell profile or a cron entry — so
a dialog guarding it protects nothing. The **user-scope edit is the confirmation.**

What that argument does *not* cover is the second actor: it speaks for the human who wrote
the declaration, and the agent that rewrites the named file has none of those permissions.
So the surviving rule is about **placement**: *installed content may not live where an agent
writes.* Refused, by name, when a loophole's module directory or argv target resolves inside
the workspace this launch mounts or inside the jail-home tree yolo manages.

Both faces — the config faces (an inline entry's `command`/`doctor_cmd`, and the spawn) and
the manifest faces (the module directory, the daemon argv, `doctor_cmd`) — go through **one**
tree comparison, because two comparisons is how they would come to disagree about what
"inside the workspace" means. It is enforced at the **spawn**, the last moment before the
code runs, for the same reason exclusivity cannot be enforced in discovery. **A refused
module directory suppresses the argv refusals under it**: `{loophole_dir}` resolves to that
directory, so a module directory in an agent-writable tree means every host-side field names
an agent-writable target — including the ones no rule can see, like a Python daemon's imports
— and checking the directory says that about all of them at once.

> [!WARNING]
> **The rule cannot be complete, and it has two edges.** yolo knows the workspace it is
> launching and the homes it manages, not that some other checkout is agent-writable in a
> different jail — so it catches the shape that actually occurs, a daemon sitting in the repo
> being worked on, and the permission argument covers the rest. And the check is
> deliberately conservative about what counts as a path (no whitespace, no shell
> metacharacters), because a false positive refuses a working loophole at **every** launch.

> [!WARNING]
> **A rule about how two REAL paths relate cannot be verified by a test that invents both of
> them.** This rule once shipped too broad and refused yolo's own shipped loopholes on every
> launch of yolo's own development jail — advice nobody can follow about content they did not
> install. Every unit test built its module directory under a temp dir, so the configuration
> where the two real paths coincide is the one nobody constructs; a real nested launch is what
> found it.

### What is deliberately NOT a gate

- **`default_enabled: true` is available to any pack, unrestricted.** *"A pack I fetched can
  declare itself on"* survives a second reading: a declaration about a default cannot widen
  what a pack may **do**, and a pack cannot be **selected** without a human editing user-scope
  config, so declaring yourself default-on changes nothing until someone installs you.
- **A jail-side daemon, `state_files` and `requires` get no crossing claim.** A jail daemon is
  a process inside the container, the one place a pack's code was always allowed to run;
  `state_files` resolves inside yolo's own state tree rather than a path the user would
  recognize as theirs; and a `requires` probe is a `stat` that crosses nothing, so a disclosure
  line for it would dilute a surface whose whole value is that every line is a real capability.
  `requires` is **path-scoped** instead, because the answer is readable even though nothing
  crosses.

> [!WARNING]
> **The install-time approval prompt for a fetched pack is GONE, together with its lockfile
> record and its launch gate.** What a user reads instead is `yolo pack footprint` before
> selecting a pack, and the per-launch disclosure lines. The **enumeration rule survives the
> deletion unchanged and is still total** — a crossing that emits no claim is a crossing
> nobody is told about, and the footprint is now the *only* place a user sees it. Do not
> re-derive the approval from the enumeration's shape: the strings are disclosure, not
> comparison keys. Why the gate went is
> [`pack-system.md`](pack-system.md#why-there-is-no-approval-gate).

> [!WARNING]
> **Following a mutable ref IS the trust decision.** A pack pinned to a branch re-fetches
> content nobody has seen, from an author who can change it at will; that is trust extended
> continuously rather than a permission anyone exercised. The answer is documentation, not
> re-prompting: a **tag pin** is the documented shape for a pack carrying host execution, and
> the two facts that size the risk are that a launch resolves from the **local mirror** and
> never touches the network, and that the mirror moves only at an explicit pack install or
> update.

## The crossing enumeration

One loophole contribution emits **several** claims — one per crossing — and the classes are
distinct because their texts have to be. What each declaration produces, and the two
non-obvious foldings:

| Declaration | Claims | Note |
| :--- | :--- | :--- |
| the host daemon's argv, plus `doctor_cmd` | one **base** claim, host EXECUTION | `doctor_cmd` folds in rather than getting its own line: it is host execution too, and one claim per program is the honest unit |
| each intercepted host | one per host, carrying **where the intercept points** | the destination is folded in rather than claimed separately, because it is not a second crossing — and leaving it out made two manifests differing only in it read as **one** disclosure |
| `ca_cert` | one claim, naming the **capability** | a crossing in its own right, not a detail of the intercept: the file is mounted from the host and joined into the jail's node CA bundle, so a module-relative CA with no intercepts still installs an authority every node client trusts |
| a bind that looks like a **socket** | one per bind, host IPC | its own class, because `:ro` is no boundary for a socket |
| every other bind | one per bind | carries the socket caveat verbatim |
| each device node | one per node | not weaker than a writable bind, and the path rules do not reach a device node — which is precisely why it needs a claim |
| a manifest yolo **cannot read** | one claim, **fail-closed**, treated as host execution | an unreadable declaration is not "no claims" — that is the empty set, and a manifest this build cannot parse may well declare a daemon |

**A refused declaration** (absent directory, name collision) yields **no** module and so no
claim, which is right: nothing crosses because nothing will be discovered, and the refusal is
reported by the paths that act on it.

Three rendering rules that look cosmetic and are not. **The daemon claim carries the RAW
argv** — placeholders unexpanded, nothing elided: an elided argv collapses two different
daemons onto one string, and an expanded one is machine-specific. **The argv is joined with
shell quoting for INJECTIVITY, not for a shell** — nothing execs the string, but a bare
space join is not injective. And **host execution reads differently from a host read**, in
the text and in the marker: the footprint's review tail counts executions separately and
**first**, so a pack that runs a daemon does not read as *"1 loophole"*.

> [!WARNING]
> **The socket class's discriminator is coarser than "it is a socket", and it could not be
> otherwise.** Nothing in the producer may `stat` the path: the declared value is **raw**, and
> resolving it would make the string machine-specific; and a `stat` is a fact about this
> machine at this moment, so a class that changed when the socket happened to be absent would
> read differently on the machine where it is missing. The static evidence is therefore
> `readonly: false` **or** a `.sock`/`.socket` basename — so a read-only bind of a socket with
> a non-obvious name lands in the plain mount class. Nothing is understated, only the
> discriminator is coarse; the precise fix is a **declared socket bit** in the schema.

> [!WARNING]
> **Producers are merged by ONE helper, called by every consumer, and a source-level test fails
> if a consumer reaches for a producer directly.** The union is **deduplicated**, because a
> crossing reached by two producers is one thing to disclose, and it returns nil rather than an
> empty slice, because "nothing to disclose" is a length test and both spellings must read the
> same there.

A source-level test that only pins *"the consumer calls the merged helper"* cannot see a
post-hoc filter dropping claims after the call, so the invariant's other half is
**behavioural** and per producer: a pack whose only claim comes from one producer must
behave differently with and without it.

### The per-launch disclosure

The classification of *which* claim kinds cross, and whether each crossing is a host **read**
or host **execution**, is **data** — exhaustive over the closed kind set **by test**, with an
unclassified kind defaulting to **exec**, the only fail-closed direction. It is data because
the printer's hardcoded set was exactly the defect: it dropped every kind it did not name,
with no test to catch the next one — and it was **already wrong for two shipped kinds** whose
host reads appeared at no launch.

**The read/exec split is per CLAIM, not per kind.** One loophole contribution emits several
claims and only some execute, so a kind-level answer is wrong in both directions.

**Host execution prints BEFORE the spawn.** For a read, printing afterwards is merely
cosmetic; for an execution, afterwards is not a disclosure, it is a notification that
something already happened. The ordering is **structural** rather than positional: one
wrapper is the sole call site of the spawn, pinned by a test that scans for a second one —
which is exactly how it broke before. That test reads the spawn side's own first side
effect, because asserting only that the line printed passes under the *old* ordering too.

> [!WARNING]
> **A banner showing a daemon that will NOT run is worse than silence.** The footprint reports
> what a pack **wants** — hiding a daemon argv from the report a reader opened it for would
> defeat it — while the launch answers what is **about to happen**, and the pre-spawn block's
> whole value is that every line in it is imminent. So the launch subtracts at the
> **disclosure**, never at the footprint, and as a rule over crossing classes rather than a
> special case for one kind.

## Selection and discovery

**Selection has to be enforced inside discovery**, because a pack-shipped loophole is read
**host-side, before the container exists** — the "the mount is the filter" rule that governs
every other pack surface does not reach it.

**The pack-aware loophole set is ONE constructed value, produced once on the host and read by
every consumer.** Before it was, seven surfaces assembled their own view: the briefing, the
broker predicate, the container argv, the daemon spawn, `yolo loopholes list`/`status`, config
validation, and `yolo check`'s manifest walker — and **two of them execute host code**
(`doctor_cmd`, from two commands users and the agent briefing treat as read-only preflight).

The constructor takes the pack modules *this process recorded* plus the config block, so a
consumer cannot assemble a different view, cannot forget a source, and cannot bypass the
gate. The convergence is asserted structurally by a test that walks the census files.

Three properties worth knowing:

- **Empty is fail-safe at every branch.** A process with neither a staged record nor a
  resolver sees **no** pack loopholes at all, because a pack loophole missing from a listing
  is a visible omission while an unaudited daemon self-check executing under a read-only
  preflight would not be.
- **The manifest walker stays a walker** rather than becoming a discovery call, because its
  whole job is reporting bad manifests and discovery swallows per-manifest failures by
  contract. It reads the *same* recorded modules and pairs with the gate, so a caller that
  both reports and executes gets both from one walk.
- **Two sources, and the ordering is `pack < config`.** A config entry overrides a pack's
  loophole. Name collisions never reach an ordering at all — the pre-flight is fatal.

> [!WARNING]
> **Do not reintroduce a source that discovers a loophole with no selection step.** A
> hand-placed directory in the user's home was exactly that — drop a directory in and every
> launch found it — which contradicts *"nothing is active by default"* in the one place it
> matters most. It is retired: not read by discovery, not by the health walker, and its source
> label is deleted. A populated directory is **reported, never silently dropped**.

> [!WARNING]
> **Do not reintroduce a reserved loophole namespace either.** There is none left. A reserved
> name and a pack-shipped name cannot be the same name, because the pre-flight is fatal and
> would refuse every launch that selects the pack. Reservation lists were also the mechanism
> behind a specific silent failure: a manifest under a reserved name loaded, was discovered,
> had its daemon skipped **without a word**, and still contributed its intercepts, mounts,
> devices and jail environment to the container argv — half a loophole, silently.

`yolo loopholes enable|disable` **toggles nothing today**: it prints the exact config key, the
file to write it in, and why workspace scope is the weaker place for it, then exits non-zero.
That is a deliberate interim, not a half-finished edit — the command's only ever mechanism was
rewriting a manifest in the retired directory, and the replacement is a read-modify-write of a
hand-commented user config that the current serializer would strip every comment from.

## Settings

A loophole's own settings are **declared, typed, and per-key scoped** in its manifest, supplied
by the user under `loopholes.<name>.settings`, and delivered through a **file core writes**
rather than an environment channel the workspace controls. The mechanism — the type set, the
scope field, and the rulings behind them — is
[`pack-system.md`](pack-system.md#a-packs-own-config-keys); an absent `scope` means **user
only**, the fail-closed direction R5's list correction requires. Two traps are
loophole-specific and belong here.

> [!WARNING]
> **Never accept an opaque `settings` map.** If core validates only *"it is an object"*, it
> cannot tell a visibility allowlist from a library-preload variable — which launders the
> user-scope-only refusal that exists to keep a preload out of a host daemon's spawn, letting a
> workspace file an agent can edit reach a host process's environment.

> [!WARNING]
> **A closed type set with no enum means a string-valued mode is unvalidatable, and a typo that
> silently WIDENS host access is the shape this whole design deletes.** The journal bridge's
> old three-valued `off | user | full` was two questions wearing one key: the declared key is a
> boolean and `off` is `enabled: false`.

## Retirement: what happens when a pack goes away

**Selection controls ACTIVATION, not REVOCATION.** Deselecting a pack stops the *next* launch
from starting its daemon and retires the state it left behind. It does not stop a daemon that
already ran: teardown kills the whole process **group**, so what survives is only what the
daemon deliberately placed outside its own group — a shell-profile line, a cron entry, a
double-forked reparented process. No packaging design changes that, and this one does not claim
to.

Per-loophole **state** is keyed by loophole **name**, which puts it outside the staged tree and
lets it survive restaging — the property that makes a pack-shipped CA possible at all, since a CA
regenerated on every launch would break every long-lived TLS client in the jail. The same
property makes it **unattributed**, so the answer is to write the attribution down: an ownership
record at staging, a detector on the launch path where deselection is actually observed, and a
`prune` sweeper. The state directory **and** its per-service log are **archived, not deleted** —
dated generations, keep-newest-few, carrying a marker naming the pack that owned them, because
*"whose key is this?"* is the first question anyone asks of an archived directory.

> [!WARNING]
> **Three refusals protect a private key rather than tidiness, and each is easy to get
> backwards.** **Retire before record**: one config edit can drop a pack and select a different
> one shipping the same loophole name, and recording first would hand the new pack the old
> pack's CA. An **unknown** configured-pack set retires nothing. A **corrupt** record is neither
> acted on nor overwritten.

**One gap, and it is the same tension:** a pack still selected that has *stopped declaring* a
loophole is not detected, because that evidence is indistinguishable from a momentarily
unreadable pack tree — and the cost of being wrong is a moved private key. Retirement keys only
on the signal the user typed: the pack leaving the selection list.

The **materialized embed cache** is deliberately unswept: it is content-addressed, derived from
the binary's embedded tree, and identical on every machine running that build, so it is
regenerable cache rather than state anyone owns.

## Where a loophole does nothing

Two axes make a loophole inert, and they share **one** mechanism and **one** message rendering,
deliberately: platform and backend both answer *this loophole does nothing here, and here is
why*, and two half-messages for one user-visible situation is how a whole backend once looked
provisioned while configuring nothing.

- **Platform** — the `platforms` declaration, evaluated as a pure function of the target pair.
- **Backend** — a container backend that starts no host services at all, and a no-VM
  user-level backend that never reaches loophole startup.

**Backend beats platform when both apply**: an inert backend starts no host service whatever the
platform says, so the platform answer would be a second reason for one outcome — and the
actionable line is "switch backends", not "get a different machine". An unreadable manifest
prints nothing here, because the discovery layer already reports that same file and a second
complaint would read as a second bug.

The inert report hangs off the **same spawn boundary** as the execution disclosure, because
everything a user must know before host code runs — *or before concluding that it did* — belongs
in one place.

### At the host target, there is no jail

`yolo host apply` refuses a loophole contribution, and the **naive reason is backwards**: a
loophole's effect *is* on the host, so "not applicable off-container" reads as obviously wrong.
The honest reason is the inverse, and it is spelled out rather than left to a generic line:

> A loophole is a host daemon whose only client is a container. With no jail there is no client,
> nothing to add a host entry for, no jail daemon payload, and nothing for the endpoint file to be
> mounted into.

Refused because its **counterparty** is missing, not because its mechanism is. **And the refusal
is a feature for the trust story**: the one command that mutates the real machine deliberately
runs no pack hooks either, so *"selecting this pack runs a daemon"* stays a statement about
launching a jail rather than about applying a config.

> [!WARNING]
> **The jail-side census must EXCLUDE `loophole` explicitly rather than derive it.** A
> loophole's jail-side effects are real — host entries, bind mounts, jail environment — but they
> are produced by the run pipeline in the host CLI **before the container exists**, so if
> anything renders a loophole in a jail, it is not the entrypoint's surface loop. A derived
> `true` would make the census assert something no code reads.

## What this does not license

- **Not** a second gate over host execution. A declaration about a default cannot widen what a
  pack may do.
- **Not** a change to the probe half. `requires.file_exists` stays: it answers *"can this machine
  run it"*, which is a real question, and it does not decide activation on its own.
- **Not** pack-level dependencies. R6 avoids needing them, and nothing here introduces a pack
  that depends on another pack.
- **Not** a change to the three-predicate model. `Enabled`/`Active`/`Honored` are right; only
  what feeds `Enabled` changed.
- **Not** a licence to "fix" the in-jail activation branch, or per-mount presence skipping. Both
  are presence deciding activation, deliberately, and both are explained above.
- **Not** infrastructure gating. The host nix-daemon socket is mounted because it exists, and
  stays ungated by ruling: it is infrastructure the *image* depends on rather than a capability a
  jail reaches for, so gating it is a separate no-nix-shaped feature. **The crossing stays named
  in any inventory** — an inventory that quietly omits the crossing it cannot justify is worth
  less than one that names it.

## Current values

Verified at `a3922298`. The prose above explains what each of these is for; this table is the
only place the values themselves are stated.

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| Contribution kind, and its `from` field | `{"kind": "loophole", "from": "<pack-relative dir>"}`, `from` required | `packdecl.KindLoophole`; the full kind enumeration is `internal/packdecl/kinds.go`, pinned by `kinds_test.go` |
| Combine rule | Exclusive, by loophole **name** | `internal/packdecl/kinds.go` |
| Manifest enablement key | `default_enabled`, absent ⇒ **false** | `loopholedecl.Manifest.DefaultEnabled` |
| Retired manifest key (recognized, refused) | `enabled` | `loopholedecl.RetiredKeyEnabled` |
| User's switch | `loopholes.<name>.enabled`, either scope | `loopholes.ConfigEnabledOverride` |
| Setting scopes, and the default | `user` (default), `workspace` | `loopholedecl.SettingScopeUser`, `SettingScopeWorkspace`, `DefaultSettingScope` |
| Module-dir tokens | `{loophole_dir}` (host), `{jail_loophole_dir}` (container) | `internal/loopholes/load.go` |
| Sources, in precedence order | `pack` < `config` | `loopholes.SourcePack`, `SourceConfig` |
| Retired discovery directory (named only by the migration notice) | `~/.local/share/yolo-jail/loopholes/` | `loopholes.RetiredUserLoopholesDir` |
| Module-dir mount point in the jail | `/etc/yolo-jail/loopholes/<name>` | `internal/loopholes/runtime.go` |
| Per-loophole state dir | `<global storage>/state/<name>` | `loopholes.StateDirFor` |
| Retired-state generations kept | 3 | `internal/prune/loopholestate.go` |
| Retired top-level config keys (now refusals naming their replacements) | `host_processes`, `journal`, `agents` | `internal/config/validate.go` |
| Shipped loopholes | one manifest per `packs/*/loopholes/*/` | `packs/` |

## Why it's this way

Forward-facing rulings a maintainer would otherwise undo. Every id keeps its original
spelling: they are cited from code comments, shipped manifests, sibling docs and the roadmap,
and after the design docs were deleted this table is where they resolve.

| ID | Ruling | Why it stays |
| :--- | :--- | :--- |
| <a id="oq-a1"></a>[`OQ-A1`](#oq-a1) | The Claude OAuth broker's loophole ships `default_enabled: true` inside the claude pack — selecting the pack is what turns it on | It must be on for a user running Claude Code whether or not they know they need it: the alternative is silently reintroducing the concurrent single-use-refresh-token race the broker exists to prevent. |
| <a id="oq-a2"></a>[`OQ-A2`](#oq-a2) | **Going dark is fine.** No migration machinery, no upgrade notice | A loophole you never listed behaving like an agent pack you never listed *is* the rule working. A migration writing the currently-active set into user config would make the ruling a no-op for precisely the people who already have host daemons running. |
| <a id="oq-a3"></a>[`OQ-A3`](#oq-a3) | `default_enabled: true` stays available to fetched packs, unrestricted | A declaration about a default cannot widen what a pack may do, and a pack cannot be selected without a human editing user-scope config. Adding an origin restriction *specifically* to the default key would be a halfway measure over the wrong thing. |
| <a id="oq-a4"></a>[`OQ-A4`](#oq-a4) | The cgroup delegate is **opt-in**, with no presence-activated exception — and `yolo-cglimit` therefore does not work out of the box | Its crossing is genuinely the weakest yolo has (it delegates the jail's *own* cgroup), so R4's argument is weaker here — but R1 is about the **mechanism**, not the severity. The moment one thing yolo ships stays presence-activated, "presence never activates" stops being a rule anyone can rely on while reading the code. |
| <a id="oq-a5"></a>[`OQ-A5`](#oq-a5) | Keep all three gates for the host-process view — select the pack, enable the loophole, list the processes | They answer different questions: is it installed, is it running, what may it show. Collapsing them would mean a non-empty allowlist **silently starting a host daemon**, which is presence-activation wearing a different hat. Where the cost should be softened is the *message*, not the mechanism. |
| <a id="oq-a6"></a>[`OQ-A6`](#oq-a6) | The two remaining "builtin services" become ordinary pack-shipped manifests | A channel emptied of everything except the two things yolo happened to have compiled in has not been emptied, it has been renamed — and the asymmetry was the evidence: one was opt-in and the other could not be turned off, and nobody decided that. |
| <a id="oq-a7"></a>[`OQ-A7`](#oq-a7) | A loophole-only pack still needs **selecting**. Shipped in the binary is not installed | A rule with one exception is two rules. The tempting third option — make every embedded pack default-selected — is presence-activation moved from the daemon to the surfaces: a pack's skills, config and briefing would land because it happened to be compiled in. |
| <a id="oq-a8"></a>[`OQ-A8`](#oq-a8) | A loophole's settings are **declared and typed** in its own manifest, with a per-key scope | See the two warnings under [Settings](#settings): an opaque map cannot tell a visibility allowlist from a preload variable, and a string mode with no enum is unvalidatable. |
| <a id="oq-a9"></a>[`OQ-A9`](#oq-a9) | **One key, renamed.** The manifest's default and the user's switch are different keys with different names, and the old spelling is refused | Two booleans over one state would give the manifest, the listing and the CLI three ways to disagree. The field names are the enforcement: they can no longer be confused at a call site. |
| <a id="oq-a10"></a>[`OQ-A10`](#oq-a10) | The broker's loophole is a **contribution of the claude pack**, not a pack of its own | R6's whole argument is that the dependency is structural; a separate pack reinstates the selection step R6 deletes, and hands back the every-jail default-on failure. A user on a different provider escapes with `supersedes` on the capability, not by deselection. |
| <a id="oq-a11"></a>[`OQ-A11`](#oq-a11) | Gate the broker's host daemons on the loophole record; leave the nix socket ungated and **say why** | A daemon none of yolo's own surfaces name is worse than one that is merely on. Nix is infrastructure the image depends on rather than a capability a jail reaches for. |
| <a id="oq-a12"></a>[`OQ-A12`](#oq-a12) | `yolo check` reads pack-shipped loopholes | Otherwise the command a user reaches for when a loophole is silently off prints a cheerful "nothing installed" while every `doctor_cmd` in the tree goes unreported. |
| <a id="oq-a13"></a>[`OQ-A13`](#oq-a13) | R5 stands for the ON direction; the existing OFF disclosure is **mirrored** onto ON | Restricting enablement to user scope would cost the per-workspace opt-in R5 exists for. One seam answers both directions rather than two, so there is one vocabulary rather than a second invented for the new danger. |
| <a id="oq-lp1"></a>[`OQ-LP1`](#oq-lp1) | The manifest schema lives in a leaf package; the runtime re-exports it as type **aliases** | The claim producer cannot import the runtime (cycle), and two definitions of one manifest type would be two things a manifest could disagree with. |
| <a id="oq-lp2"></a>[`OQ-LP2`](#oq-lp2) | The config block's install-shaped keys are user-scope-only, and a workspace enabling an uninstalled loophole is a **fatal** error with an install offer | The migration population is everyone who followed the shipped guide, and a scope error refuses the **whole launch** rather than one loophole — which is precisely why the message has to carry the fix. Warn-then-error assumes people have somewhere to go. |
| <a id="oq-lp3"></a>[`OQ-LP3`](#oq-lp3) | A local origin is not a trusted-forever bypass — install confirms every origin | A local-origin check that constrains the path in no way means a directory an **agent** writes counts as yours. Folded into the placement rule rather than special-cased. |
| <a id="oq-lp4"></a>[`OQ-LP4`](#oq-lp4) | The front is declared by `publishes` on the daemon, never by a manifest naming a yolo subcommand in its own argv | A manifest naming yolo's CLI is the pack knowing about yolo's internals — the workaround-becomes-API failure, and a one-way door. |
| <a id="oq-lp6"></a>[`OQ-LP6`](#oq-lp6) | Build the capability system (`serves` / `supersedes`) | A loophole manifest is a public surface regardless, so `serves` is a field third parties will write even if only first-party loopholes are ever superseded — and designing it once beats retrofitting it around whatever the first outside use turns out to be. |
| <a id="oq-lp8"></a>[`OQ-LP8`](#oq-lp8) | **Following a mutable ref IS the trust decision.** Accept it and document it — a **tag pin** is the shape for a pack carrying host execution — rather than building re-prompting | The content-anchored approval this would have replaced has nothing left to anchor: the approval it belonged to is deleted. The two documentation sentences are now the only thing between a user and a mutable ref, which is why they are user-facing rather than only here. |
| <a id="oq-lp9"></a>[`OQ-LP9`](#oq-lp9) | Nested jails **recurse** the scope model: the outer jail is user scope for the inner one | "User level" is not a fixed path; it is the scope that owns the machine the daemon runs on. Inside a jail that is the jail's own config, legitimately owned by the jail's agent, because the blast radius is a container you can throw away. Delivered as generated per-consumer scope files plus an explicit layer flag — never a conventionally-named auto-merged file, which activates because a file exists, invisibly at the call site. |
| <a id="oq-lp10"></a>[`OQ-LP10`](#oq-lp10) | Retire the hand-placed loopholes directory in the user's home | It was the one channel that started a host daemon with **no selection step at all**. What replaces it — a loophole in the conventional local pack — keeps the drop-a-directory ergonomics and gains a disclosure the directory never had. |
| <a id="oq-lp11"></a>[`OQ-LP11`](#oq-lp11) | Every loophole yolo ships is a **pack's**; the bundled channel is gone | *Agents are packs, and core does not know what an agent is* — a loophole registry plus a magic directory plus a config block is the world before that move. Accountability is a property of who wrote it, which an official pack already carries. |
| <a id="oq-lp12"></a>[`OQ-LP12`](#oq-lp12) | No request/grant machinery for per-workspace loopholes | The two verbs answer it: install once at user scope, and each workspace enables from the vetted set. The residual — a workspace can enable **any** installed loophole, not only the one that repo was installed for — is bounded by install-time vetting and visible in the per-launch disclosure. |
| <a id="oq-lp13"></a>[`OQ-LP13`](#oq-lp13) | **Not hashing, and no new confirmation** — a placement rule, and it must not judge yolo's own shipped content | *If you can edit user-level files, you have all the perms already.* A dialog guarding an act that already required the authority it protects is theatre. And the rule exists because installed content in an agent-writable tree can be swapped by an actor with **none** of the authority that installed it — so applying it to the very artifact that performs the check protects nothing it does not already presuppose. |
| <a id="oq-lp14"></a>[`OQ-LP14`](#oq-lp14) | The bind-host path rule is **withdrawn, not extended** | Its two cases were inverted: it admitted `~/.ssh` and refused a session runtime-dir socket. A gate with its cases inverted is not a weak gate, it is not a gate — and adding vocabulary to it keeps the inversion. See the warning under [The pack-shipped subset](#the-pack-shipped-subset). |
| <a id="oq-cap2"></a>[`OQ-CAP2`](#oq-cap2) | A pack **can** ship a loophole — the contribution kind, rather than working around the absence of one | The argument that a pack could not serve a capability rested on none of the contribution kinds being a daemon; this kind falsifies that premise, and the narrower conclusion survives for a better reason: selection is how a pack-shipped loophole turns on and off, so supersession is only ever needed for loopholes selection cannot remove. |
