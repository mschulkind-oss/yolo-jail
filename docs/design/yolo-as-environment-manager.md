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
path** — this describes the destination, not the route. Where today's behavior is cited it is
to say what changes shape, not to plan the change.

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
apply` (and `yolo -- claude`) is the everyday path: it honors the three escapes below —
in-jail edits are captured, a `yolo-jail.local.jsonc` merges, a host settings file composes
in — because that is what makes an interactive session livable. You reach for `--sealed` at
the moments where "the same declaration, and *only* the declaration" has to be true and
provable: pinning an environment in CI, handing it to a colleague, or cutting a release you
want to reproduce a year later. Sealing trades the everyday conveniences for a guarantee you
do not need most of the time — so it is a flag you raise deliberately, not a wall the common
path runs into.

It is not the default for the same reason `nix build` is not `--pure` by default in every
context: the escapes it refuses are *good features*. Capture keeps an agent's mid-session edit
from being silently discarded; `yolo-jail.local.jsonc` lets you drop two packages on this one
machine without touching the shared config; the host layer is how your own Claude settings
reach the jail at all. Banning them everywhere would make the happy path hostile. `--sealed`
is for when you have decided, for this apply, that no unnamed input may participate.

With that established: the point of a description is not that you can compare two of them. It is
that **it is the only input.** Nix ships no "diff my two machines" verb because a comparison
tool is what you build when the definition does not bind; `describe --hash` above is a cache
key and a CI pin, not the guarantee.

Three inputs escape today's definition, and only the first is declared:

| Input | Declared? | The nix analogue |
|---|---|---|
| the `host` layer (`Surface.HostSource`) | ✓ by the pack that grants it — its *content* is machine state | a fixed-output derivation: impure, but named |
| the **capture overlay** | ✗ nothing declares it, and it outranks every declared layer | a store path that edited itself |
| `yolo-jail.local.jsonc` | ✗ untracked, auto-merged, no `include_if_found` needed | `--impure`, silently |

```
$ yolo apply --sealed
✗ refused: 3 keys captured in-jail outrank the definition
           claude/settings: enabledPlugins, extraKnownMarketplaces · mise/config: [tools]
           → promote them into a pack, or discard: yolo config reset claude
✗ refused: yolo-jail.local.jsonc is present and drops 2 packages (ripgrep, fd)
✓ declared impurity: claude/settings ← ~/.claude/settings.json (granted by the claude pack)
```

**Impurity is declared, not banned** — that is the whole rule, and it is why sealing cannot mean
"no host reads." The `host` layer is load-bearing: it is how a user's own Claude settings reach the
jail at all, which is a feature packs exist to provide. Sealing means **no *undeclared* input.**

Capture is also a good feature — humans and agents edit config in-jail, and silently discarding
that is hostile — so the resolution is that it becomes a *staging area* rather than a winning
layer: recorded, reported, and promotable into a pack or the workspace config, with `--sealed`
refusing while any are outstanding. That is what makes "captured, and I meant it" expressible.

This retires `diff` as a top-level verb. `yolo config diff` already reports open closure on one
surface (captured vs rendered) and keeps that job; whole-environment assurance is `--sealed`; a
cross-machine comparison is `describe --json` on each side and `diff(1)`.

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

The remedy is pack-declarable rather than a built-in attr→`brew`/`apt` table that goes stale on
every distro release, and the probe needs the *binaries* a package provides (`packages:
["postgresql"]` → `psql`), which nix can answer at `jail` and cannot at a lower notch — so a
declared `provides` list, with unprobeable entries reported as unprobeable rather than as present.
The same rule as `install` applies: **yolo names the remedy and never runs it below `jail`**
(§4.1). This is advice, not a second package manager.

Today none of this information exists, and its absence has a live cost: on `macos-user`, packs
render **zero surfaces every launch, silently** (`host-render-target.md` §9.7). A description that
cannot be honored must say which part, in the output, at the moment you ask.

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
container "a sandboxed container jail", `internal/agents/agentsmd.go:114` tells every agent it is
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

**Three notches, not a policy vector.** Underneath, confinement *is* composed policy —
filesystem write scope, credential visibility, network reach, process view, resource caps — and
the temptation is to expose that. [happy-path-principle.md](happy-path-principle.md) says
don't: "fill the matrix, support one path per cell." Three named levels, each a preset over
that vector, each with a documented meaning. The vector is an implementation fact, and hand-
assembling one is not a supported way to use yolo.

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

This is also the sharpest risk in the whole design, and it gets one hard rule: **`install` is
never honored below `jail`.** A pack's `installerUrl` is curl-to-shell; a pack managing *config*
on your real machine is a feature, and a pack *installing software* on your real machine is a
different product with its own threat model. Refused by name, always, not by default.

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
| `program` (install) | ✓ | ✓ | **never** |
| `mount`, `reads-host`, `state`, `files` | ✓ | — | — |

One row is the whole point and it is target-independent. Refusals name the field; nothing is
emulated. In particular **a copy is never a substitute for a mount** — it goes silently stale,
so a pack update appears to apply and doesn't. Skills are the exception that proves the rule:
they port because their delivery is a *merge* (built-in < pack < user), which no mount expresses
anyway.

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
  *reduced* environment that says out loud what it cannot do, and that `install` is refused
  there permanently — but this is a product-discipline risk, not a technical one, and it does
  not have a technical fix.
- **`guest` must actually work before any of this is honest.** One notch of the dial currently
  renders zero pack surfaces per launch. A three-notch story with a broken middle is worse than a
  one-notch story that works.

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
