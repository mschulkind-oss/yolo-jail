# yolo as an environment manager — the shape I would want

**Status:** design, high level, 2026-07-27; **fact-checked against the code 2026-07-30** (the
vision is unchanged; a few claims about not-yet-built verbs are now partly shipped — see the
refresh note). Written in response to: *"we're going through an identity crisis… an environment
manager is really the thing. How is yolo macos-user mode different from SandVault? The answer is
this batteries-included approach — the jail is not the novel thing, it is what's staged inside
it. Maybe we redescribe ourselves as an agentic development environment describer that can
describe a jail."*

> **Refresh note (2026-07-30).** Two verbs this doc proposed as future now partly exist:
> **`yolo config dump`** ships (the canonical computed-config dump this doc folds into
> `describe`), and **`yolo config drift`** ships (compares the workspace config on disk against
> the one a running jail was built from — a narrower, in-jail cousin of the drift/sealing
> discussion in §3.3/§7). The pack manifest is now `contributes[]` with twelve **kinds**, not
> nine fields, and a fetched pack can access the host with **install-time approval** rather than
> never — which is the consent primitive §4.1's `--at host` escape valve would build on. The §5
> pack-field table uses the old field names; the mapping is: `surfaces`→`config`,
> `install`→`program`, `mounts`→`mount`, `hostFiles`→`reads-host`,
> `writableDirs`/`sharedDirs`→`state`, plus new `env`. The vision and the confinement dial are
> unaffected; nothing below is built beyond those two verbs. See
> [pack-system.md](pack-system.md).

**Scope:** what yolo *is* and how it feels to use, mostly from the user's side. **No migration
path here** — this describes the destination, not the route. The route is
[../plans/environment-manager-plan.md](../plans/environment-manager-plan.md), which sequences
this vision into buildable phases (foundation-first: the render-path collapse, then the dial,
then the verbs) and carries this doc's §8 open questions forward as "decide before phase N"
gates. Where today's behavior is cited below it is to say what changes shape, not to plan the
change.

**The name stays.** `jail` becomes the name of the strongest confinement level, so "yolo jail"
reads as "yolo, at the jail level." The name gets *more* accurate under this framing, not less.

**Reads with:** [what-yolo-is.md](what-yolo-is.md) (whose Part 1 answer this inverts),
[host-render-target.md](host-render-target.md) (the render side of the same idea; its §9.1 is
the question this doc answers), [happy-path-principle.md](happy-path-principle.md) (which
constrains how many knobs this may add),
[macos-no-vm-direction.md §"What makes it yolo"](macos-no-vm-direction.md) (the prior, narrower
version of this argument),
[environment-manager-user-stories.md](environment-manager-user-stories.md) (this design walked
through five users, which is where its output formats get pressure-tested and where §8's costs
show up as concrete failures).

---

## 1. The one-sentence answer to "how is this different from SandVault?"

It isn't, at the wall. `internal/macosuser/seatbelt.go` is **59 lines** and says so itself —
*"matching SandVault's structure"*, *"SandVault-parity"*. On the container side the actual
confinement is ~26 lines of flags in `assemble_parts.go:149-190` plus 8 in
`assemble.go:148-159`, out of 46,700 non-test lines. And the credential boundary is **code that
isn't there**: `~/.ssh` is absent because nothing mounts it. A boundary defined by omission is
necessarily small and necessarily commoditizable.

The difference is everything *inside*: a locked package set, composed agent config, skills and
house rules, credential brokers, host-capability bridges — described declaratively, per
workspace, reproducibly. `macos-no-vm-direction.md:115` already wrote this down as five items
and called the sandbox "the part we'd **borrow** from SandVault." Four of the five are staging.

So:

> **yolo describes the environment an agent works in — reproducibly, declaratively, per
> workspace — and confinement is one attribute of the description. A jail is the strongest
> setting of that attribute, and the default.**

That inverts `what-yolo-is.md:93` ("a sandbox product with an unusually good
config-composition engine inside it"), which had it backwards, and whose own next paragraph
already flinched at the implication.

---

## 2. What a user thinks they are doing

Today the mental model is **"put the agent in a box."** The box is the noun; everything else is
what you furnish it with. That model is why `runtime` conflates two unrelated questions, why
half the config keys read as box-poking, and why "manage my host agent config too" sounds like
a category error rather than a feature.

The model I want is **"describe the environment; choose how confined it is."**

```
       DESCRIPTION                                    CONFINEMENT
  ┌──────────────────────────┐              ┌────────────────────────────┐
  │ tools      packages, mise│              │  jail     container        │
  │ agents     packs         │      ×       │  guest    own identity+LSM │
  │ knowledge  skills, briefs│              │  host     none             │
  │ config     composed files│              └────────────────────────────┘
  │ services   loopholes     │
  │ layout     workspace, env│                one description,
  └──────────────────────────┘                three places to realize it
```

The description is the product. The confinement column is a *setting*, and the three values
are presets, not backends (§4).

Everything a user already knows keeps working. **`yolo -- claude` is unchanged**, still means
"jail," still the default, still one command. This framing is not a new interface; it is the
existing interface with the axis it was always implicitly varying made visible.

---

## 3. The verbs

Five, and only two are new.

```
yolo -- <cmd>          run <cmd> in the described environment          (unchanged)
yolo --at <level> -- <cmd>   … at a different confinement level        (new)
yolo apply             make this environment match its description     (new)
yolo apply --sealed    … and fail if anything undeclared got in        (new; §3.3)
yolo describe          print the resolved description                  (new; would absorb the
                                                                        now-shipped `config dump`)
yolo check             is this description satisfiable here?           (unchanged in spirit)
```

There is deliberately **no top-level `diff`** — see §3.3. `yolo config diff` keeps its narrower,
real job.

`packs`, `config`, `ps`, `prune`, `loopholes`, `broker`, `init` are unaffected.

### 3.1 `apply` is the verb the current design is missing

Today `yolo` means *launch*, and provisioning is a side effect of launching. That is why there
is no answer to "set up my environment but don't run anything," and no answer at all at host
level, where there is nothing to enter.

`apply` splits them: **make it so**, then optionally run something in it. `yolo -- claude`
becomes "apply, then exec" — which is what it already does, now with a name.

At jail level `apply` builds the image, stages packs, renders config, and exits. At host level
`apply` *is* the whole feature: it renders your agent config into your real home
([host-render-target.md](host-render-target.md) §6). That doc had to argue hard that a host
render was a coherent thing to want; under this framing it is just `apply` with the confinement
dial at zero.

```
$ yolo apply
jail (podman)   image ✓ (cached)   packs claude,house-rules   surfaces 7 rendered
$ yolo apply --at host --dry-run
host            surfaces 4 would render, 3 refused (see below)
```

**When would a user run `apply` by hand, given `yolo -- <cmd>` already applies-then-execs?**
Worth enumerating, because it is the same question as "is `apply` a real verb or just an
internal step" — and the answer is that every case is one where you want the *make-it-so*
without the *run*:

- **Set up, don't launch.** Provision a fresh checkout — build the image, stage packs, render
  config — so the first `yolo -- claude` is instant, or so CI can `apply` in one step and run
  tests in another.
- **Apply at the host notch, where there is nothing to exec.** `yolo apply --at host` *is* the
  host-render feature (§4.1): it writes your agent config into your real home. There is no "run
  something in it" half — the environment you are configuring is the one you are already in.
- **Re-apply after editing the description.** You changed `yolo-jail.jsonc` or a pack and want
  the environment to match, without starting an agent. In a jail this overlaps with "restart to
  pick up config" (and `yolo config drift`, shipped, tells an in-jail agent a restart is owed);
  at the host notch, `apply` is the only way that edit ever takes effect.
- **`apply --sealed` as a gate** (§3.3): in CI or a release, you `apply --sealed` to *prove* the
  environment matches only its declaration, then run — the seal is the reason to split apply
  from run at all.

### 3.2 `describe` is the reproducibility claim, made checkable

If the pitch is "your environment is described and locked," the description has to be a thing
you can hold.

```
$ yolo describe
environment  yolo-jail @ /workspace              confinement  jail (podman)
tools        31 nix packages · flake.lock 8f2a1c…   mise: none
agents       claude (pack, embedded)               launcher --dangerously-skip-permissions
knowledge    3 skill trees (built-in < house-rules < user) · AGENTS.md 4 sources
config       7 composed surfaces                   2 with captured edits
services     claude-oauth-broker, host-processes
grants       /workspace rw · 2 host files ro · no network holes
description  sha256:4c1f…   ← same hash, same environment
```

`--json` for tooling; `--hash` for CI. Two machines printing the same hash have the same
environment, and that is the sentence SandVault, devcontainers, and "whatever's on the host"
structurally cannot say.

One caveat that §3.3 makes load-bearing: **a hash over an unsealed environment is worse than no
hash**, because it looks authoritative while moving for reasons the user cannot enumerate. Over a
sealed definition it is a `flake.lock` rev. Unsealed, it prints marked or not at all.

### 3.3 `apply --sealed`: the definition binds, or the apply fails

**`--sealed` is an opt-in flag, not the default, and the split is the point.** Plain `yolo
apply` (and `yolo -- claude`) is the everyday path: it tolerates a small set of inputs that are
not part of the committed definition — an in-jail edit is captured, a `yolo-jail.local.jsonc`
merges — because that is what makes an interactive session livable. You reach for `--sealed` at
the moments where "the same declaration, and *only* the declaration" has to be true and
provable: pinning an environment in CI, handing it to a colleague, or cutting a release you
want to reproduce a year later. Sealing trades the everyday conveniences for a guarantee you
do not need most of the time — so it is a flag you raise deliberately, not a wall the common
path runs into.

It is not the default for the same reason `nix build` is not `--pure` by default in every
context: the escapes it tolerates are *good features*. Capture keeps an agent's mid-session
edit from being silently discarded; `yolo-jail.local.jsonc` lets you drop two packages on this
one machine without touching the shared config. Banning them everywhere would make the happy
path hostile. `--sealed` is for when you have decided, for this apply, that no unnamed input
may participate.

#### The full closure

"What is the definition?" has to be answerable input by input, or `describe --hash` is a hash
over a set you cannot enumerate. So, nix-style — every input that shapes an environment,
classified by whether it is part of the definition:

| Tier | Inputs | nix analogue |
|---|---|---|
| **Locked** (reproducible, pinned) | nixpkgs + the image (`flake.lock`); the pack set with per-pack commit pins and host-access approvals (`packs.lock.json`) | `flake.lock` revs |
| **Declared** (in a tracked file) | `yolo-jail.jsonc`; pack `contributes[]` — surfaces, `defaults`/`managed`, `derive`; the workspace transform `yolo-jail.config.lua`; inline `env_sources` entries | `flake.nix` |
| **Declared-impure** (named, but the *content* is external machine state) | the user config `~/.config/yolo-jail/config.jsonc`; `include_if_found` targets; `env_sources` dotenv *files* (secret values); the user `config.lua`; `mise_tools` (versions declared, toolchains fetched); the **`host` layer** (§below) | a fixed-output derivation — impure, but *named* |
| **Undeclared** (participates, nothing names it) | `yolo-jail.local.jsonc` (auto-merged, gitignored); the **capture overlay** (outranks every declared layer, nothing declares *it*) | `--impure`, silently |

Sealing's rule is one line against this table: **`--sealed` refuses the Undeclared tier and
reports the Declared-impure tier; the Locked and Declared tiers are the definition.** It does
*not* mean "no host reads" — a named-but-impure input is nix's fixed-output derivation, and
banning it would break the point of packs. It means **no *un*declared input.**

```
$ yolo apply --sealed
✗ refused: 3 keys captured in-jail outrank the definition
           claude/settings: enabledPlugins, extraKnownMarketplaces · mise/config: [tools]
           → promote them into a pack, or discard: yolo config reset claude
✗ refused: yolo-jail.local.jsonc is present and drops 2 packages (ripgrep, fd)
✓ declared impurity: 31 nix packages @ flake.lock 8f2a1c…   packs @ packs.lock.json
```

#### The `host` layer is the input we should retire

One Declared-impure row is different from the rest, and a reviewer was right to flag it: the
**`host` layer** — where your real `~/.claude/settings.json` composes *into* the jail (a pack's
`reads-host` grant) — sits badly in this design for two independent reasons.

- **It fights host-config management.** The companion direction ([host-render-target.md](host-render-target.md))
  renders a pack's config *out* to your real home. A pipeline that both reads your live
  settings *in* and asserts config *out* over the same file is a loop: which one is the source?
  You cannot cleanly have both on one surface — it is an XOR at best.
- **It does not port.** It is the one input that needs a `:ro` `/ctx` mount to stay
  non-circular: on a host target the source file *is* the output (a fixpoint — §host-render §6.3),
  and on `macos-user` there is no `/ctx`, so the layer silently drops today. Every other input
  in the closure means the same thing at every confinement level; this one does not.

**Decided (2026-08-01, env-manager plan OQ-3): drop settings-inheritance and express it as a
pack instead.** If your personal Claude settings matter, they are a *local pack* — declared,
locked, portable to every notch — not a live read of a file yolo has to special-case. That
collapses the awkward Declared-impure row into the Declared tier, makes `--sealed` mean what
it should on every surface, and removes the read-in/write-out conflict. The cost is real and worth naming: today a
user's `~/.claude/settings.json` "just works" in the jail with zero setup, and under this
direction they would author (or `yolo config promote` into) a one-file local pack. Credentials
are unaffected — those cross as mounts, not as a compose layer, and stay their own mechanism.

#### Sealing is a host-side check — it needs no container

Because the whole closure is *assembled on the host before any container exists* (the render
core has zero jail dependencies; the jail only ever consumes a rendered snapshot), sealing is
not a jail feature. `yolo apply --sealed` can seal a `jail`, a `guest`, or a `host` apply with
the same logic — the container supplies blast-radius reduction and the `:ro` host-layer
separation, not the enumeration. Which is one more reason the `host` layer above is the odd one
out: it is the single input whose *meaning* depends on having a container.

**But be precise about what sealing proves.** `--sealed` verifies the **input closure of config
*generation*** — that the config was assembled only from declared inputs, nothing unnamed shaped
it. It does *not* by itself verify the **resulting *environment*** — that `psql` is actually on
PATH, that the toolchain the agent will invoke exists. Those are two different guarantees, and a
reviewer was right that the dep check (§3.5) is on the far side of that line. Whether the two
coincide is **level-dependent, and it is the same fact that makes dep-checking a separate
mechanism rather than a clause of sealing**:

- At **`jail`** they nearly coincide, because the toolchain is *inside* the sealed closure: system
  deps come from the locked image (nixpkgs @ `flake.lock`, the Locked tier above). Seal the
  inputs and you have effectively sealed the environment, because its tools were *built from*
  those inputs — nix's build-closure, where the derivation is pure and its dependencies are
  pinned in the store. The §3.5 dep probe at `jail` is nearly a formality: a declared package is
  present *because* it is a sealed input.
- At **`guest`/`host`** they split. yolo bakes no image, so the toolchain is whatever the machine
  happens to have — host state, outside any closure yolo can seal. That gap is exactly what the
  dep probe fills: `check --at host` (and every `apply`) probes for the binaries a pack needs and
  refuses the apply if one is missing. Dep-checking is the *resulting-environment* guarantee that
  sealing **structurally cannot** provide once the toolchain leaves the closure.

So it matters, and the design already answers it with two verbs rather than one: `--sealed` is
the input-closure guarantee; the dep probe is the environment-sufficiency guarantee. They are
distinct precisely because the single closure nix gets for free (pure eval *and* a pinned build)
splits in two the moment you drop below `jail` and lose the image.

Capture is the other Undeclared input, and it is a good feature — humans and agents edit config
in-jail, and silently discarding that is hostile — so the resolution is that it becomes a
*staging area* rather than a winning layer: recorded, reported, and promotable into a pack or
the workspace config, with `--sealed` refusing while any are outstanding. That is what makes
"captured, and I meant it" expressible.

The point of a description is not that you can compare two of them. It is that **it is the only
input.** Nix ships no "diff my two machines" verb because a comparison tool is what you build
when the definition does not bind; `describe --hash` above is a cache key and a CI pin, not the
guarantee. So this retires `diff` as a top-level verb: `yolo config diff` already reports open
closure on one surface (captured vs rendered) and keeps that job; `yolo config drift` (shipped)
reports whether a running jail's workspace config has drifted from what it was built with;
whole-environment assurance is `--sealed`; a cross-machine comparison is `describe --json` on
each side and `diff(1)`.

### 3.4 `check` becomes "is this description satisfiable *here*"

Two changes. **`check` reports what is inert at your confinement level, by name** — and then it
does something about it, because *an inert key is a handoff, not a verdict*:

```
$ yolo check --at host
✓  packs, surfaces, skills, briefing, env_sources    apply here
✗  mounts, host_files          needs a mount namespace — refused, never emulated
✗  network.*, resources        nothing to confine
!  security.blocked_tools      shims would land on your real PATH — opt in explicitly
✗  packages                    yolo does not manage packages here (no image to bake)
                               yolo needs these; here is what this machine has:
                                 postgresql   ✓ present   /opt/homebrew/bin/psql   16.4
                                 redis        ✗ MISSING   → brew install redis
                               re-checked on every apply; a missing dep refuses the apply.
```

"yolo can't manage this" is one sentence short of useful. The next sentence is whether the
dependency is present anyway — **probed, not inferred from config** — and the one after is the
command that fixes it. Where yolo hands off, it hands off with momentum.

The remedy is not a built-in attr→`brew`/`apt` table that goes stale on every distro release —
it is pack-declarable, the probe is a shared checker, and at lower notches it can emit a
runnable manifest (a Brewfile and its kin) rather than a wall of advice. That is a section of
its own: **§3.5**. The rule `check` keeps is only the detection half — probe, report, and hand
off; it never installs anything itself (§3.5 draws the line about who does, and with what
permission).

**`check` should also report host-render drift, when it is run with host access.** Once your
agent config can be applied to the real home (§4.1, once host-render ships), the machine gains
a new way to be wrong: the real `~/.claude/settings.json` no longer matches what the pack would
render, because someone hand-edited it or the pack moved. `check` is where that surfaces — not
as a diff it silently reconciles, but as a handoff with the same momentum as the missing-dep
case: *"host config has drifted from your description on 2 surfaces — run `yolo apply --host` to
reassert, or `yolo apply --host --dry-run` to see it first."* This is the host-side twin of the
in-jail `yolo config drift` (shipped), which already answers "has the workspace config drifted
from what this jail was built with." Same question, other side of the wall: is the environment
still what the description says. `check` never *writes* — it detects and points at `apply`,
which is the only verb that changes anything (§3.1). That keeps the "detect vs. apply" split
clean: `check --at host` tells you the host has drifted; `apply --host` is the deliberate act
that fixes it.

Today none of this information exists, and its absence has a live cost: on `macos-user`, packs
render **zero surfaces every launch, silently** (`host-render-target.md` §9.7). A description that
cannot be honored must say which part, in the output, at the moment you ask.

### 3.5 Dependency provisioning: declare once, check once, hand off with a manifest

At `jail` the toolchain is inside the sealed closure (§3.3): a `packages: [postgresql]` entry
becomes `psql` in the image, and there is nothing to provision. Below `jail` there is no image,
so the same declaration becomes a *question about the host* — is `psql` present, at what
version — and, if not, a handoff. Getting that handoff right without becoming a second package
manager or duplicating every tool's own dep-list is a real design problem, so it gets its own
section rather than a clause of `check`.

**One declaration, owned by whoever introduced the need.** A pack declares only the deps *it*
introduces: a pack that ships `packages: [postgresql]` owns the `psql` probe *because it is the
thing that added the dependency*. It does not re-declare deps some other tool already owns —
that is the drift the reviewer rightly worried about. A project that already encodes "needs
`psql` ≥ 16" in its own doctor keeps owning that; yolo does not copy it.

**One checker, shared by every caller.** The "is this binary present, at what version, what
installs it" logic is a **library plus a plain entry point** (`yolo check-deps`), used
identically by `check`, by every `apply`, and standalone by a project's own doctor script — so
the same checker runs over the same declared list rather than each caller re-implementing it.
The declaration lives once (in the pack that introduced the need); the checker lives once (in
the shared lib).

- **The boundary to decide** is what the two callers share. The lighter option is a **declared
  schema** — the `provides`/hint block below is plain data any doctor can read and probe with
  its own code, and yolo ships a reference checker over it. The heavier option is an importable
  **Go package** both link. The schema is more portable (a Rust project's doctor can honor it)
  and is the leaning; the Go package is only worth it if the probe logic gets subtle enough that
  a spec under-specifies it. Pick the schema unless that happens.

**Per-system install hints, authored by the pack.** A pack author knows how their dependency is
installed better than yolo's built-in table ever will, and a single `psql` maps to different
package names on `brew`, `apt`, `dnf`, `pacman`. So the pack declares them:

```jsonc
{ "kind": "program", "bin": "psql", "provides_from": "postgresql",
  "install_hints": {
    "brew":   "postgresql@16",
    "apt":    "postgresql-16",
    "dnf":    "postgresql-server",
    "nix":    "postgresql_16"        // the jail path; also the reproducible one
  } }
```

`check` picks the hint matching the detected host package manager, and reports the others as
alternatives. An unprobeable or unhinted entry is reported as **unprobeable**, never silently as
present — the same honesty rule the rest of `check` follows.

**Emit a runnable manifest as a composed surface, not a one-off command helper.** A wall of `→
brew install …` lines is one step short of useful when there are ten of them — but a manifest
dropped in whatever directory you happened to run `check` from is worse, because now there are
copies with no owner. So the package manager's own manifest is **another rendered surface**: a
single file at a known path — `~/.config/yolo/Brewfile` on macOS (and the `apt`/`dnf`/`pacman`
equivalents beside it) — composed from *every* pack's `install_hints`, regenerated **wholesale
on every `apply`** exactly like the config surfaces are. It is a total summary of what the
described environment wants from the host, not a per-invocation snippet. You point your package
manager at that one stable file (`brew bundle --file=~/.config/yolo/Brewfile`), and because it
is composed, adding a pack updates it in place the next `apply` — the same regenerate-don't-
accrete rule every other surface follows. yolo owns the file; a hand-edit is discarded on the
next render, so it never drifts into a hand-maintained list that disagrees with the packs.

```
$ yolo apply --at host
host   surfaces 4 rendered   deps: ~/.config/yolo/Brewfile updated (3 not yet installed)
                             → brew bundle --file=~/.config/yolo/Brewfile   (or let yolo run it, below)
     postgresql@16   redis   ripgrep
```

**Who may install, and the permission line.** This is the line the reviewer asked for, and it is
the load-bearing one:

- **The composed manifest is always the floor; running it is an offer on top.** yolo renders
  the manifest surface (`~/.config/yolo/Brewfile` and kin) on every `apply` regardless — that is
  the guaranteed handoff, and "I'll run it against that file myself" is always a valid outcome.
  On top of that floor yolo *offers* to run the not-yet-satisfied remedies for you, behind a
  confirm (below). Nothing installs *silently* below `jail`; the same confirm-gated,
  never-ambient rule §4.1 states for a pack's own `install` applies here.
- **yolo can offer to run either kind — including `sudo` — always behind a confirm.** Split the
  remedies into *(a)* things that need no elevation (a user-scope `brew install`, a `pip install
  --user`, dropping a file in `~`) and *(b)* things that need `sudo` or another grant (a system
  `apt install`, anything outside the user's own tree). Both are **offer-to-run behind a
  confirm**; the only difference is that a category-(b) step runs `sudo <cmd>` and **lets sudo's
  own password prompt show through normally.** yolo does not need to capture, inject, or store a
  password — the invocation is interactive, so the OS prompt is the right thing to see, and yolo
  never handles the credential itself. The confirm shows the exact command (with the `sudo`
  prefix) before anything runs, so the user is approving a specific elevation, not a category.
- **The invariants that bound "offer to run":** yolo never elevates *ambiently* — no step runs
  without the per-step confirm, and `sudo` appears only where the command visibly carries it,
  never wrapped silently around something the user thought was user-scope. No TTY (CI) means no
  confirm means the remedy is only printed, never run (fail-closed, §4.1). And yolo still writes
  the manifest regardless, so "print it, I'll run it myself" is always available — offering to
  run is a convenience over the handoff, not a replacement for it. At the `host` notch, category
  (a) is often "nothing to elevate" (the user already has the rights), so most host remedies run
  without ever reaching a `sudo` prompt.

**Open question for the design pass:** the exact schema of `provides`/`install_hints`/the
manifest emitters, and precisely where the checker-library boundary sits (schema vs. Go
package). Flagged in §8; this section fixes the *shape* (declare-once, check-once, hand-off, and
the permission line), not the field names.

---

## 4. Confinement: a dial with three notches

The config key that matters:

```jsonc
{
  "confinement": "jail",     // jail | guest | host        (default: jail)
  "runtime": "auto"          // podman | container | auto  — mechanism, not policy
}
```

This fixes a conflation that exists today. `runtime: "macos-user"` is simultaneously *how much
confinement* and *by what mechanism*, which is why `config-ref` has to explain that one of the
three "runtimes" is "a WEAKER isolation boundary than a container/VM." Split them and each
question has one answer:

| Level | Mechanism | What the agent gets | Credentials |
|---|---|---|---|
| **`jail`** (default) | podman / Apple Container | namespaces, disposable home, baked image | none of yours |
| **`guest`** | Seatbelt (macOS), bwrap+Landlock (Linux) | a real home on the real filesystem, no image | its own, separate identity |
| **`host`** | none | you, your machine, your dotfiles | yours |

`runtime` demotes to a mechanism hint inside `jail`, normally `auto`. A Linux middle row needs no
new concept — it is the same notch with a different enforcement primitive, which is the evidence
the dial is real rather than a story told about two backends
([host-render-target.md](host-render-target.md) §9.8).

### 4.0 Why the middle notch is not called `sandbox`

Worth pinning, because "sandbox" was the obvious name and it is the wrong one.

**"Sandbox" is the generic term for the whole column, not a point on it.** Kubernetes' CRI unit
is a `PodSandbox`; gVisor and Firecracker both sell themselves as sandboxes; Chrome's renderer
sandbox is seccomp on Linux and Seatbelt on macOS. Containers and VMs are sandboxes. Naming one
notch `sandbox` gives the notch that most needs a discriminating name the least discriminating
word available — and this codebase already proves it: `internal/cli/help.go:39` calls the
container "a sandboxed container jail", `internal/jailcontent/briefing.go` tells every agent it is
"inside a YOLO Jail — a sandboxed container", and `internal/macosuser/seatbelt.go` emits
`";; yolo-jail macOS-user sandbox profile"`. One word, currently naming both the jail and the
not-jail.

**`jail` survives the same test.** FreeBSD jails (2000) and chroot jails are OS-level
partitioning of one kernel — a container *is* a jail in the term's own lineage. Nothing about
`jail` implies a VM, so the strongest notch keeps its name honestly.

**The middle notch is also not "just a different user."** Two orthogonal things are bundled
there: the separate macOS user is the *credential* boundary (own home, own keychain reach), while
Seatbelt is the *confinement* (`(deny file-write* (subpath "/"))` plus re-allows;
`(deny file-read* (subpath "/Library/Keychains"))`). The Linux version needs **no separate user
at all** — bwrap uses namespaces, the same primitive podman uses, so the Linux middle notch is a
*weaker container*, not a second account. Any name drawn from either mechanism describes one
platform and lies about the other, which is the exact conflation this section demotes `runtime`
for.

So the notches are named for **the agent's relationship to the machine**, which is the one thing
true on every platform: it is jailed, it is a guest, or it is you. `guest` carries the right
connotation from "guest account" — its own identity, restricted reach, on the real machine — and
commits to no mechanism.

**One caveat the names cannot carry: the dial is ordinal within a platform, not absolute across
them.** `jail` spans podman (namespaces, shared kernel) and Apple Container (one VM per
container), and a VM is materially stronger than a user namespace. `jail` therefore means "the
strongest confinement available here," not one fixed strength. `describe` prints the mechanism
next to the notch for exactly this reason.

**Three notches on the surface, composable primitives underneath.** Underneath, confinement
*is* composed policy — filesystem write scope, credential visibility, network reach, process
view, resource caps — and, as a reviewer noted, the enforcement *primitives* also compose:
a separate OS user, a Seatbelt profile, a bwrap/Landlock sandbox, a user namespace are
independent knobs, and real combinations exist — a separate user *without* Seatbelt, Seatbelt
*without* a separate user, a namespace with neither. **The internal model should be built to
express those combinations from the start; the three notches are presets over it, not a
replacement for it.** Concretely: `guest` today bundles "separate macOS user (credential
boundary) + Seatbelt (confinement)" and its Linux form is "bwrap namespaces, no separate user"
— those are already *different compositions wearing one notch name*, which is the evidence the
primitive layer is real and needs a home.

What [happy-path-principle.md](happy-path-principle.md) rules out is *exposing* that vector as
the everyday interface — "fill the matrix, support one path per cell." So the design is
two-layer: a composable primitive model that the code assembles and `describe` can print
(so a non-standard combination is legible and testable), with **three named, documented
presets as the only thing a user normally selects**. Hand-assembling a custom combination is an
advanced, explicitly-opt-in path — planned for, not the front door — rather than something the
architecture forecloses by hard-coding three monoliths. The distinction from today: `runtime`
currently *is* a hard-coded monolith, and that is the conflation §4 exists to undo; planning the
primitive layer now is what keeps a fourth combination (a Linux `guest` variant, a
seatbelt-without-user posture) from being another special case bolted on later.

**The grants stay where they are.** `mounts`, `host_files`, `network.ports`, `devices`, `gpu`,
loopholes — these are *holes through a wall*, and they keep their current names and semantics.
What changes is that they are understood as negotiating a boundary rather than creating one, so
at lower notches they are inert, and `check` says which.

### 4.1 The escape valve, which is the actual user story

```
$ yolo -- claude                    # jail. the default. what you do all day.
$ yolo --at host -- claude          # same agent, same config, your real machine
```

The second line is the case that started this: you configured `pi` or `claude` through a pack —
approval posture, MCP servers, skills, house rules — and now you need to fix something *on the
host*, where today you get none of it and reproduce it by hand forever. `--at host` runs the
agent you already described, with the confinement dial at zero, and tells you what it could not
carry over.

This is also the sharpest risk in the whole design. The original rule here was "**`install` is
never honored below `jail`**, refused by name, always." On reflection that is stricter than the
trust model actually requires, and a reviewer pushed on it: **you already trust the pack** — you
approved its host access at install (see [pack-system.md](pack-system.md) §9, the fetched-pack
approval model), and at the `host` notch it is running as you regardless. So the honest rule is
not *refuse* but *confirm*:

- **Below `jail`, an `install` is confirm-gated, never silent.** A pack's `program via installer`
  is curl-to-shell and `npm -g`/`brew install` mutates a real toolchain — so yolo shows exactly
  what would run, from which pack, and asks. `yolo apply` with no TTY (CI) treats an unconfirmed
  install as a refusal (fail-closed), the same way pack host-access approval does. This reuses
  the machinery that already exists rather than inventing a second gate.
- **Curl-to-shell always shows the command first.** The one thing never done below `jail` is
  piping a fetched script into a shell *unseen*. The confirm prints the resolved URL (and, where
  cheap, the fetched script itself) so the human is approving a specific thing, not a category.
- **Permission still bounds it** (§3.5): a category-(a) user-scope install is offer-to-run behind
  the confirm; a category-(b) elevation requests `sudo` explicitly at that step, never ambiently.

The distinction that survives from the original rule: a pack managing *config* on your real
machine and a pack *installing software* on it are different postures, and the second is the
sharper one — so it is gated by an explicit confirm rather than riding the same silent path
config does. What changes is that "gated" replaces "forbidden," because forbidding what the user
already trusts and could run by hand anyway is friction, not safety. **This wants its own
threat-model pass before it ships** (flagged in §8); the design position is *confirm-gated, TTY
only, command shown, permission-bounded* — not *never*.

### 4.2 Agent autonomy is a confinement policy, not baked pack config

The escape valve above exposes a defect that `--at host` makes unavoidable. Every shipped
agent pack currently hard-codes, *unconditionally*, the settings that let the agent run
without permission prompts — because a jail contains it, so "accept every edit, skip the
dangerous-mode prompt, allow the whole filesystem" is the correct posture *there*. Today
that posture is expressed as ordinary `managed` config plus a `launch` flag, with nothing
saying "only because I am jailed":

| Pack | how it says "no prompts" today (config `managed` + `launch`) |
|---|---|
| `claude` | `permissions.defaultMode: acceptEdits`, `skipDangerousModePermissionPrompt: true`, `additionalDirectories: ["/"]`, `allow: []` · `--dangerously-skip-permissions` |
| `codex` | `approval_policy: "never"` · `--dangerously-bypass-approvals-and-sandbox` |
| `agy` | `permissionMode: "allow"` · `--dangerously-skip-permissions` |
| `opencode` | `permission: "allow"` |
| `pi` | *(nothing — pi is permissive by default)* |

`yolo apply --host --assert` renders a pack's `managed` keys into your **real**
`~/.claude/settings.json` (pure RMW; the managed layer wins — `hostrender.go`,
`compose.go` Enforce). So following the host-management guide today writes `acceptEdits` +
`skipDangerousModePermissionPrompt` + `additionalDirectories: ["/"]` onto your actual
machine and launches the agent with `--dangerously-skip-permissions` — **stripping the
very confirmation prompts that are the only thing protecting a machine with no jail around
it.** The keys that are *safe because* there is a jail travel, unlabelled, to the notch
that has no jail. That is the bug.

**The fix: autonomy is a policy the confinement decides, not config the pack asserts
everywhere.** Two halves, and they meet at render time:

1. **The confinement level carries an `agent-autonomy` policy.** It composes alongside the
   *enforcement* primitives of §4.0 (namespaces, Seatbelt, Landlock, separate-user) — it is
   simply a *policy* primitive rather than an *enforcement* one, and it belongs in the same
   `render.Profile`. The preset defaults follow the wall: `jail` → autonomy **on** (the
   container is the safety net — the existing "Claude YOLO" invariant), `guest` → **on**
   (still confined), `host` → **off** (nothing contains it; your prompts stay). Because it
   lives on the Profile, `describe` prints it ("Confinement: host — agents run with normal
   permission prompts"), and — the composability requirement — **whenever someone
   defines/composes a custom confinement mode, autonomy is one of the settable knobs**,
   exactly like "separate user? Seatbelt?". A locked-down `jail` with prompts on, or a
   `guest` with autonomy explicitly off, are then expressible without a new concept.

2. **Each pack declares its autonomy posture as data — and it is bidirectional.** This is
   the half the table above makes subtle. It is *not* "apply a bypass recipe when confined."
   Different agents have opposite defaults:
   - `claude`/`codex`/`agy`/`opencode` are **guarded by default**; confinement *loosens*
     them (jail adds the bypass recipe).
   - `pi` is **permissive by default**; there is nothing for confinement to add — instead
     the **`host` notch must add a restriction**, tightening pi back to prompting.

   So a pack does not declare "my bypass"; it declares **both postures** — the settings
   (config keys + launch flags) that mean *autonomous*, and the settings that mean
   *guarded* — and the confinement's `agent-autonomy` policy selects which set renders. The
   pack owns *how* each posture is expressed for its agent; the confinement owns *which* is
   in force. Neither the "always loosen" nor the "always tighten" framing is correct alone;
   the machinery is one selector run in whichever direction the pack's default sits.

At render: `profile.agentAutonomy ? pack.autonomousPosture : pack.guardedPosture`. The
benign managed keys that are safe at any notch (auto-updater off, trust-dialog accepted)
stay in ordinary `managed` and are untouched by this — the line is precisely *"safe
anywhere"* stays unconditional, *"safe only because something contains me"* (or its
inverse, *"unsafe unless something contains me"*) becomes autonomy-selected. A jail boot
must stay byte-identical to today (the `renderfingerprint_test.go` gate), so the jail-on
path reproduces exactly the current `managed`+`launch` output; only the host/guest paths
change.

**How a pack encodes the two postures (resolved).** A dedicated `autonomy` contribution
kind that bundles each posture's config patches + launch flags as one named block
(`autonomous` / `guarded`) — chosen over a `when`-discriminator on existing `config`/`launch`
entries because it keeps confinement-conditional keys physically out of the unconditional
`config` (so a jail-bypass key can't be left in the always-on part by accident) and prints
whole in `describe`/`footprint`. Both encodings were sketched against the real packs; see the
implementation plan §9.0 for the sketch and the decision (OQ-11).

---

## 5. Packs are the batteries, and the batteries are data

"Batteries included" as a pitch loses to whoever ships more batteries. The durable version is:
**the batteries are declarative, locked, and portable across confinement levels.**

Which is already true of the mechanism — a pack is data, read through one loader, rendered by
one loop with no switch on any tool name — and only accidentally false of its reach. A pack's
fields already sort cleanly by which notch they need:

| Pack declares (kind) | jail | guest | host |
|---|---|---|---|
| `config` / `config-overlay` (composed config) | ✓ | ✓ | ✓ |
| `skills`, `briefing` prose | ✓ | ✓ | ✓ |
| `env` (static vars) | ✓ | ✓ | ✓ |
| `hook` | 3 of 3 | 2 | 1 |
| `launch` (flags) | ✓ | ✓ | ✓ |
| `program` (install) | ✓ | confirm | confirm |
| `mount`, `reads-host`, `state`, `files` | ✓ | — | — |

One row is the whole point and it is target-independent. Where a kind cannot apply it is
refused by name, never emulated — in particular **a copy is never a substitute for a mount** (it
goes silently stale, so a pack update appears to apply and doesn't). Skills are the exception
that proves the rule: they port because their delivery is a *merge* (built-in < pack < user),
which no mount expresses anyway. `program` is the other special case: below `jail` it is neither
honored silently nor refused outright but **confirm-gated** (§4.1) — you already trusted the
pack at install, so the remedy is to show what would run and ask, not to forbid it.

Nothing above requires a new pack format, a version field, or a second repo. A pack author
writes one manifest and never thinks about notches; the environment that cannot honor a field
says so.

---

## 6. The environment describes itself to its own agent

A small thing with outsized value, and only possible because the briefing is generated.

**The agent should be told which notch it is in.** Today every briefing and the built-in
`jail-startup` skill assume a container — "you are in a sandboxed container," "no sudo,"
"host credentials are not propagated." At `guest` or `host` those sentences range from
misleading to actively wrong, and an agent that believes it is disposable when it is not will
take a disposable agent's risks.

So the briefing states the level, the grants, and the absences:

```markdown
## Your environment
Confinement: host — this is the human's real machine. Changes are not disposable.
You have: their real credentials, their real dotfiles, no snapshot to fall back on.
Absent: nothing is mounted read-only; there is no jail to restart.
```

Same generator, same pack prose, one accurate paragraph on top. This is the point where
"describes an environment" stops being an internal reframing and becomes something the agent
itself can act on.

---

## 7. What does not change

Worth stating plainly, because a reframing this size invites scope creep:

- **`yolo -- claude` in a fresh repo.** One command, jail, credentials omitted by default. If
  this framing costs the happy path anything, the framing is wrong.
- **Confinement defaults to the strongest notch.** `host` is never inferred, never auto-detected,
  never the fallback when something fails. Same rule `macos-user` has today ("EXPLICIT opt-in —
  never auto-detected") applied to the whole dial.
- **Credential omission stays the default posture** at every notch that can express it. The
  boundary being small code is not an argument for a weaker default.
- **Packs stay user scope.** A workspace config still cannot name one: it travels with the repo
  and is agent-editable, so it must not decide what prose an agent then follows. Nothing about
  environment-manager framing loosens this — if anything a `host` notch makes it more load-
  bearing.
- **Reproducibility stays the differentiator, and gets teeth.** Locked nixpkgs, locked packs, and
  now a definition that *binds* — `apply --sealed` (§3.3) refuses rather than reports. This is the
  item `macos-no-vm-direction.md` calls "*the* yolo differentiator," and it was the weakest claim
  in the product until sealing: "reproducible" meant "the same declaration produces the same jail,"
  while a user arriving from nix reads it as "the declaration is the only input." Three inputs said
  otherwise. Every part of this design is downstream of closing that gap.

---

## 8. What this costs

- **Every future feature needs a per-notch ruling.** Today "does it run in the container?"
  answers most scope questions for free. Three notches turn that into a matrix, and the cost is
  paid by whoever adds the *next* config key, not by this doc. The mitigation is that the matrix
  is *legible* (`check --at`) rather than implicit, which is strictly better than today, where
  all ~25 keys silently assume a container and one backend quietly renders nothing.
- **A wider identity has more neighbors.** As "the agent environment manager," yolo is adjacent
  to home-manager, chezmoi, devcontainers, and mise, and will be compared to all four. As "the
  agent jail" it was compared to SandVault and won on batteries. The bet is that the second
  comparison is the one we keep winning, because the batteries are the part that is ours.
- **`host` will be over-used.** It is faster and it works with your real credentials, and
  "why not just run at host?" is one step from existing. The counter is that `host` is a
  *reduced* environment that says out loud what it cannot do, and that a pack `install` there is
  confirm-gated with the command shown (§4.1) rather than silent — but this is a
  product-discipline risk, not a technical one, and it does not have a technical fix.
- **`guest` must actually work before any of this is honest.** One notch of the dial currently
  renders zero pack surfaces per launch. A three-notch story with a broken middle is worse than a
  one-notch story that works.
- **The dep-provisioning design needs its own pass (§3.5).** The *shape* is fixed (declare-once,
  check-once, hand off a runnable manifest, offer-to-run behind a confirm including `sudo` with
  the OS prompt shown through), but the field schema (`provides`/`install_hints`/the manifest
  emitters), the checker-library boundary (an importable Go package vs. a declared schema a
  third-party doctor reads — leaning schema), and the exact offer-to-run confirm UX (how a
  `sudo` step is presented, how a multi-step manifest is confirmed — once or per-step) are
  unresolved, and getting the boundary wrong reintroduces the duplication it is meant to avoid.
- **Confirm-gated `install` below `jail` needs a threat-model pass (§4.1).** Moving from "never"
  to "confirm-gated, TTY-only, command shown, permission-bounded" is the right trust model — you
  already approved the pack — but the exact confirm UX, what a curl-to-shell approval shows, and
  the category-(a)/(b) permission split want to be pinned before it ships, not designed at the
  call site.
- **The composable-isolation primitive layer (§4.0) is planned, not built.** The three notches
  are presets over a primitive model (separate user / Seatbelt / bwrap+Landlock / namespace) the
  code should assemble and `describe` should print. Building the notches as monoliths now would
  foreclose the fourth combination later; building the primitive layer is the up-front cost that
  keeps a Linux `guest` variant from being a bolted-on special case.

---

## 9. The pitch, rewritten

Before:

> A secure, isolated container environment for AI coding agents […] to safely modify codebases
> without compromising host security or identity.

After:

> **yolo describes the environment your coding agents work in — their tools, their config,
> their skills, their credentials — and realizes that description wherever you need it: in a
> disposable jail (the default), as a restricted guest on your machine, or as you. One
> description, locked and reproducible; you choose how confined it runs.**

The jail is still the headline, because it is still what nearly everyone wants nearly all the
time. It is just no longer the *claim*. The claim is the description.
