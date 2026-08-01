# Managing host agent configs from yolo — the host as a reduced render target

**Status:** design, for discussion, 2026-07-27; **fact-checked against the code 2026-07-30**
(the design is unchanged; stale code references were corrected — see the refresh note below).
Started as *"how could we pull all of this pack stuff out of yolo, yet still use it in yolo,
but also manage the host configs — a separate util"*; the measurement said the extraction is
the wrong shape (§1.3, §2.3), so **this doc designs the capability inside yolo.** The
extraction analysis is kept as evidence, not as a proposal.

> **Refresh note (2026-07-30).** Since this was written, the pack manifest moved from nine
> effect fields to one `contributes[]` list of twelve typed **kinds**, the `computed[] + project`
> reshape DSL was replaced by a single `derive` Lua slot, in-jail reconcile became wholesale
> regeneration (`regenerateManagedTables`), two new kinds shipped (`mount` — a host-home dir
> read-only into a jail; `env` — static vars), and a fetched pack can now access the host with
> **install-time approval** rather than never. This doc's *design* survives all of that intact —
> the host is still a reduced render target, the fix is still collapsing two render paths — but
> its field-census and code-citation details were updated to the current vocabulary. The
> load-bearing sections (§2, §3, §6) are unaffected in substance. See
> [pack-system.md](pack-system.md) for the current kind set and the approval model.

**Audience:** whoever decides whether the host target happens. **§2 and §3 are the
load-bearing sections** — §2 measures how much of a pack even applies off-container and
concludes the host is a *reduced target*; §3 is the design, which turns out to be mostly
*deleting a duplicate renderer* yolo already has two copies of. §6 is the part that needs a
ruling, §9 is what I could not settle, §8 is the order I would build it in.

**Reads with:** [pack-system.md](pack-system.md) (the pack system as built — this doc
assumes it, including the compose engine and its layer stack),
[what-yolo-is.md](what-yolo-is.md) (the earlier "is the
engine separable?" answer, which this reaches the same conclusion as from the pack side),
[composed-file-permissions.md](composed-file-permissions.md) (the postures a host-side writer
must honor).

---

## 0. The one-paragraph version

**The capability is one declaration, several environments.** A pack already says how `pi` is
configured — approval prompts, MCP servers, skills. You write it once and it renders only
inside a jail; the day you need that same agent to behave the same way *on the host*, none of
it is available. Nothing intrinsic to the mechanism requires that (§2).

**Most of a pack does not apply to a host, and that resizes the whole idea.** A field census
(§2.1) says four of nine manifest fields are meaningless without a container, one (`install`)
must be refused outright, `mounts` is simply unavailable, and exactly one — `surfaces` — is
target-independent. **A pack is mostly a jail-provisioning format with a config format inside
it.** So "move the packs out" was the wrong move (§2.3): what is portable is the *render*, and
it never needed to leave the repo to become portable.

**The axis is not jail-vs-host, it is how much confinement the environment has** — and we
already ship two points on it (`podman`/`container` and `macos-user`, which has no container
and no bind mounts at all), with `surfaces` the one capability every row supports (§2.2). That
makes the host a third, *reduced* target rather than a separate product.

**And there is a finding that makes this urgent rather than speculative: yolo already composes
into the invoking human's real home, by accident, and it is currently destructive** (§6.1).
Probes below truncate a real `~/.config/mise/config.toml` and rewrite a real
`~/.claude/settings.json`. This is not a posture we would be *adopting*; it is one we are
*already in* without having designed it.

**The design (§3) is smaller than the problem sounds, because the hard part is a deletion.**
`Compose`/`ComposeStateful` have two independent callers — the boot render in
`internal/entrypoint` and the host-side `config` verbs in `internal/cli` — and three code
comments admit the second mirrors the first. Every §6.1 defect is a drift between those two
copies. Collapse them into one renderer parameterized by an explicit `Target`, and the host
becomes a third target rather than a third copy.

---

## 1. The measurement: the render core has no jail dependencies

Everything in this section is `go list -deps` and `wc -l`, not estimate. **Read it as
evidence, not as a proposal** (§2.3): it is why a non-jail target is reachable at all. A
renderer that already imports nothing jail-shaped is one that can be pointed at a different
home without untangling anything first.

### 1.1 The dependency-free set

| Package | Non-test | Test | First-party deps |
|---|---|---|---|
| `internal/packdecl` | 300 | — | **none** |
| `internal/packstage` | 256 | 225 | **none** |
| `internal/packsrc` | 638 | 503 | **none** |
| `internal/packload` | ~450 | ~520 | `agentcfg/{codec,manifest}`, `packdecl`, `jsonx`, `tomlx` |
| `internal/agentcfg` | ~1100 | ~3000 | `agentcfg/*`, `jsonx`, `tomlx` |
| `internal/agentcfg/manifest` | ~750 | — | `codec`, `jsonx`, `tomlx` |
| `internal/agentcfg/codec` | ~620 | — | `jsonx`, `tomlx` |
| `internal/agentcfg/luahook` | ~1000 | — | (gopher-lua only) — now carries `derive` |
| `internal/jsonx` | ~714 | — | none |
| `internal/tomlx` | ~244 | — | `jsonx` |

**Roughly 5,000 non-test lines out of ~46,000 in the repo — about 12%.** Third-party:
`BurntSushi/toml` and `yuin/gopher-lua`, both vendored, both pure Go. The only `os/exec` in
the whole set is `packsrc`'s `git` invocation (`internal/packsrc/store.go`).

(Refresh note: the former `internal/agentcfg/project` — the `computed[] + project` op DSL —
was deleted; its job is now the `derive` Lua slot in `internal/agentcfg/luahook`. The point of
this section is unchanged: the render core still has zero edges to `config`/`paths`/`cli`/
`entrypoint`, so a non-jail target can call it. Line counts above are approximate and not
worth re-measuring for a design doc.)

**Zero edges** from any of it to `config`, `paths`, `cli`, `cli/run`, `entrypoint`,
`storage`, `image`, or `loopholes`. `internal/agentcfg` does import `packload` — but only
in `packmanifest_test.go`, so it is not a production coupling.

Plus the pack *corpus*: `packs/` is 7 files, 28 KB, 504 lines of JSON.

### 1.2 The four places yolo reaches into it

These are the only couplings, and every one is a real call site rather than a category. They
matter to §3 for a different reason than they used to: each one is a place where *the target is
currently implicit*, so each is a place a `Target` parameter either fixes or must not disturb.

**(1) Reservation lists — 9 call sites, all `packload.Embedded*`.**

```
internal/config/writablehome.go:84,88,108     reserved home dirs / segments  (EmbeddedWritableDirs)
internal/config/hostfiles.go:712              builtinSurfacePaths            (Embedded)
internal/config/hostfiles.go:1032             hostFileWritableRoots          (EmbeddedWritableDirs)
internal/config/packs.go:422,444              name validation + suggestions   (EmbeddedNames)
internal/storage/ensure.go:53                 GlobalHome mountpoint pre-creation
```

`hostFileWritableRoots` is a **package-level value built by a func literal evaluated at init
time** (`hostfiles.go:1028`), which is why the embedded FS is registered by an `init()` in
`internal/packreg` rather than from `main` — a main-time registration "would arrive too late
and they would silently see no packs — reserving nothing, with no error"
(`internal/packreg/packreg.go:5`). Any interface that makes the pack set *lazy* or *fallible*
breaks this, silently, in the reserve-nothing direction. **This is the sharpest constraint on
any refactor here** and it is not obvious.

(`builtinSurfacePaths` is a `sync.Once` behind a function, so it is already lazy and would
survive; the init-time one is the constraint.)

**(2) In-jail rendering — `internal/entrypoint`, 1,027 non-test lines of `prism*.go` +
`pack*.go`.** `LoadJailPacks` → `ConfigurePackSurfaces` → `renderDeclaredSurface`
(`packsurfaces.go:48,83,115`), the three surface writers in `prism.go`, `RunPackHooks`
(`packhooks.go:60`), `GenerateAgentLaunchers` (`shims.go:161`). This reads pack data and
writes files; it does *not* know about mounts.

**(3) Host-side staging and mount assembly — `internal/cli/run`.** `stagePacks`
(`packs.go:45`), the writable/shared-dir mount loop (`assemble.go:175-186`), skills
(`prepare.go:68-81`), `/ctx/packs` + `YOLO_PACK_ROOT` (`packs.go:30`, `assemble.go:369-372`),
`hostFileArgs`. This is argv generation — it exists to fill mounts, and a mount is exactly what
§2.2 says the reduced targets do not have. So it is not something a `Target` generalizes; it
is something the jail target alone runs (§4.3).

**(4) Storage paths — `internal/paths`.** `PacksDir()` (the pack store, three non-test call
sites, all constructing a `packsrc.Store`), `GlobalHome()` (the shared tier),
`UserConfigPath()` (which the lockfile sits beside, `packsrc/lock.go:57`). A handful of
constants, but they encode *policy*: where a fetched pack lives, and what "machine-global"
means.

### 1.3 What this measurement means

[agent-config-packs.md §9.3](../plans/agent-config-packs.md) asked whether this was
extractable and answered "yes, but it needs an external consumer, and there isn't one." **That
test was the wrong one, and it is worth saying so rather than inheriting it.** It looked for a
*third party* who wanted an `agentpack` binary. The real gap was already sitting in the
product: we built a mechanism for declaring how an agent is configured, and **its reach stops
at the container wall** — for no reason intrinsic to what it does.

The consumer is not external. **It is the same person, on the other side of that wall.** Which
is also why the answer is not a second repo (§2.3): the same-person case is served by giving
the existing renderer a second target, and §1 says nothing stands in the way of that.

---

## 2. The motivation: an agent config that stops at the container wall

**The point is one declaration, several environments.** A pack already says how `pi` is
configured — its settings surface, its approval-prompt posture, its skills, its MCP servers,
its launch flags. You write that once. Today it renders **only** inside a jail.

So the concrete case, which is the whole argument in one sentence:

> You configure `pi` through a pack — trust posture, approval prompts, MCP servers — and it
> works beautifully in the jail. Now you need to fix something **on the host**, where the
> same agent is running against the same repos with none of it. You want *that* config,
> the one you already wrote and already trust, over there.

Today the answer is: reproduce it by hand in `~/.pi/agent/settings.json`, and keep the two
in sync forever by remembering to. **That is the gap.** It is not a missing external
consumer; it is the same user, the same agent, the same declaration, arbitrarily unavailable
in one of the two places they run it.

### 2.1 But measure how much of a pack the host actually wants

The `pi` case above is about **configuration**. A pack declares much more than that, and the
obvious objection is that most of it is meaningless on a host: you do not want a pack
installing agents into your real machine, and there are no mounts without a mount namespace.

That objection is correct, and it is *measurable*. Every **contribution kind** a pack can
declare (`packdecl` — twelve since the `contributes[]` reform), against the question "does this
mean anything with no container?":

| Kind | On the host | Why |
|---|---|---|
| `config` | ✅ **the whole point** | composed config surfaces. The one kind whose meaning is target-independent |
| `config-overlay` | ✅ | a contribution to another pack's surface — same story as `config` (it lands in a composed surface) |
| `skills` | ✅ **ports (as a merge)** | built-in < pack < user is a *composition*, not a mount, so it is written as an artifact (§2.2), not bound |
| `briefing` | ✅ ports | concatenated prose — also a composition result, written not mounted |
| `env` | ✅ | static environment variables — literal strings, no container needed |
| `mount` | ❌ **unavailable** | reads a host-home dir into a jail via a `:ro` `/ctx` mount. No mount namespace off-container, and a copy goes silently stale (§2.2). macos-user already *filters* rather than degrades |
| `reads-host` | ❌ meaningless | it exists to carry a host file *into* a jail. Off-container the source and destination are one filesystem |
| `hook` | ⚠ 1 of 3 | `shared_credentials` is a no-op (host creds are *already* machine-global); `per_jail_history` has no jail to key on; `claude_plugins` works |
| `launch` | ⚠ only with a launcher | `--dangerously-skip-permissions` is a *jail* posture. Meaningful only if yolo also launches the host agent, which is the §2.2 question |
| `program` | ❌ **refuse** | `via: installer` is curl-to-shell; `via: npm` mutates a real toolchain. §6.4 |
| `state` | ❌ meaningless | names a writable home subtree (per-workspace or machine). Off-container the home dir simply *is* writable, and there is no per-jail home to escape |
| `files` | ❌ meaningless | a pack-owned tree bound into a jail. Off-container there is nothing to bind into |

**Roughly: the config/skills/briefing/env kinds port, `program` must be refused, `mount`/
`reads-host`/`state`/`files` are unavailable or meaningless, `hook`/`launch` degrade.** The
crisp line is unchanged from the original nine-field census: **the composed-config kinds are
target-independent; the provisioning kinds are not.** Against the shipped packs, `claude`
uses most kinds; `opencode` uses `program` + `briefing` + `config` — so on a host target
`opencode` would render its config surface and its briefing, and refuse `program` by name.

**So a pack is not a config format that yolo happens to consume** — it is mostly a
*jail-provisioning* format with a config format inside it. That is the crux of the confusion
this doc started in, and it cuts two ways. It is why moving the packs out was the wrong move
(§2.3): the module would have a dominant vocabulary that is inapplicable in the environment it
was extracted for. And it is why the host target is *narrow*: it renders the one field that
means something everywhere, and refuses the rest by name.

### 2.2 So which is it: a command, or a mode?

Two shapes follow, and they are genuinely different products.

**(a) `yolo config apply --host` — a command.** Renders the applicable subset (`surfaces`,
the one hook) into the real home; refuses the rest by name. No new binary, no new module. The
pack stays a jail-provisioning format; the host is a *reduced target* of it.

**(b) A yolo "mode" that manages the environment you are already in.** Which, as you say,
is weird — you are *on* the host; there is nothing to enter. yolo stops being "a jail" and
becomes an interface for *describing an environment an agent runs in*, of which a jail is
the strongest instance.

**(a) is what I would build, and (b) is what it means.** They are not alternatives at the
level of code — (a) *is* the first increment of (b). The distinction that matters is which
one we *name*, because naming (b) commits us to a much larger surface (§9.1).

**The strongest evidence that (b) is the real structure is already shipped:
`macos-user`.** That backend runs a real agent as a real macOS user with **no container, no
image, no bind mounts** — and the framing in
[macos-no-vm-direction.md](macos-no-vm-direction.md) already isolated why:
*"almost everything on the disabled list follows mechanically from no container."* A
Seatbelt profile is a weaker confinement than a namespace, `packages:` materializes as
native darwin nix rather than image layers, and yolo already treats that as one product
with the container backends rather than a special case.

So the axis is not jail-vs-host. **It is how much confinement the environment has** — and two
of the three rows below already ship:

| Environment | Confinement | `program` | `mount` | `config` |
|---|---|---|---|---|
| `podman` / `container` | namespaces, disposable | ✅ | ✅ binds | ✅ |
| `macos-user` | Seatbelt, real user, real home | ✅ (native nix) | ❌ **not available** | ✅ *(should — see §9.7)* |
| **host** (proposed) | **none** | ❌ refuse | ❌ **not available** | ✅ |

(Column names are the current kinds: `program` was `install`, `mount` is the host-read kind,
`config` was `surfaces`.) Read down the `config` column: **it is the one row-independent
capability.** That is the same conclusion §2.1 reached kind by kind, arrived at from the
runtime side instead. And
the "something like macos-user on Linux" idea is the missing fourth row — a bwrap/Landlock
confined-but-not-containerized environment. **It needs no new concept**; it is another
confinement level, which is precisely the evidence that the axis is real rather than
something imposed to make the table look tidy.

**A copy is not an acceptable substitute for a mount, in any environment.** This is worth
stating as a rule, because "degrade `mounts` to copies off-container" is the obvious wrong
answer and it is the one this doc reached for first. The shipped code already rules it out,
twice:

- **macos-user filters rather than degrades.** A source-bearing `host_files` entry is
  *dropped* on that backend, with the reason recorded in code: there is no `/ctx/host-user`,
  so passing it through "would render with an empty host layer and silently serve its
  defaults instead of the host file the user named"
  (`macosuser/runplan.go:160-166`, pinned by `TestSourceLessHostFilesWireExcludesSourceBearing`).
  **Filtering out is the precedent, not copying.**
- **The one place yolo does copy, it copies into something still bind-mounted.**
  `acMaterialize` (`cli/run/helpers.go:48`) exists only because Apple Container trips on
  *single-file* mounts (apple/container#1089); it writes into `ws_state`, which is itself a
  live bind. So the file is still shared, not snapshotted. That is a mount-shape workaround,
  not a copy semantic.

The distinction matters because a copy is silently stale: edit the source and the
environment keeps the old bytes with nothing to indicate it. For `mounts` — `AGENTS.md`
and skills trees — that means a pack update that appears to apply and doesn't. So
`mounts` is **unavailable** without a mount namespace, and a target that cannot honor it
must say so by name (§6.2's `FieldSet`), exactly as macos-user already does for
`host_files`.

**Skills are the interesting exception**, and worth being precise about because they look
like a counter-example. `PrepareSkills` *does* copy — `copySkillSubdirs` layers built-ins,
then pack skills, then the user's own tree into one staging dir (`agents/skills.go:88-102`),
dereferencing symlinks. But that is a **merge**, not a mount substitute: three sources
collapse into one directory with a defined precedence, which no mount can express. The
result is then bind-mounted. So skills survive on a host target as a *composed artifact*,
the same way a surface does — which is to say the composition is what ports, and the
delivery mechanism is what doesn't.

### 2.3 Extraction: settled, and the answer is no

**Decided 2026-07-27 — this doc no longer proposes a separate util.** The field census is
what settles it. A separate module would own `surfaces` and yolo would keep
`install`/`mounts`/`writableDirs`/`sharedDirs`/`hostFiles`/`retireMiseTools` — six of nine
fields — because those are provisioning, and provisioning is what yolo *is*. So the module
we'd be extracting is not "the pack system"; it is the config core, and the pack format
would then live in two repos with its schema split across a boundary that the census says
falls in the middle of a single manifest.

The rest of this doc designs the capability **inside yolo**. §3 is that design.

What the earlier extraction analysis is kept for, since it was measured and remains true:
the pack packages have **zero edges** to `config`/`paths`/`cli`/`entrypoint` (§1). That is
not an argument for a second repo — it is the reason the in-yolo work is tractable at all.
A renderer with no jail dependencies is one that a non-jail target can call. §1 stays as
that evidence, not as a proposal.

---

## 3. The design, inside yolo

The capability is: **one renderer, several targets, one of which is the host.** Everything
below already half-exists; the work is mostly deleting a duplicate.

### 3.1 The core problem: there are already TWO render paths

This is the fact that makes the design almost write itself. `agentcfg.Compose` /
`ComposeStateful` have **four non-test call sites in two packages**:

```
internal/entrypoint/prism.go:167   ComposeStateful    <- the BOOT render (in-jail)
internal/entrypoint/prism.go:322   Compose            <- the BOOT render (in-jail)
internal/cli/config.go:253         Compose            <- `yolo config render`  (host-side)
internal/cli/configdiff.go:394     Compose            <- `yolo config reset`   (host-side)
internal/cli/configdiff.go:476     ComposeStateful    <- `yolo config capture` (host-side)
```

Two independent implementations of "render a surface", one per side of the container wall.
**The code already says the two are copies of each other**, in three places:

- `entrypoint/prism.go:351-353`: *"Mirrors `internal/cli.expandHome` but keyed on the Env
  rather than the process `$HOME`."*
- `entrypoint/prism.go:61`: *"Mirrors `internal/cli.loadTransformScript`."*
- `cli/config.go:3-4`: the package header, describing `config render` as running *"the SAME
  engine the entrypoint boot render calls."*

Two mirrored helpers and a header promising equivalence. The first two are honest about being
duplicates; the third is a claim that has to be re-earned by hand every time either side
changes.

**Every §6.1 defect is a drift between these two paths**, not an isolated bug. `reset`
truncates `~/.config/mise/config.toml` because the host path composes with no computed layer
while the boot path always has one. `surfaceHasHostLayer` is a hand-maintained 2-entry map in
`cli` restating what the entrypoint knows structurally. The host path resolves `~` against
the process `$HOME`; the boot path deliberately does not.

So the host capability is not a new feature bolted on. **It is what falls out of collapsing
two renderers into one parameterized renderer** — the third target becomes reachable because
the second one stopped being a hand-copy of the first.

### 3.2 Where the code goes

No new module, no new repo. One new package, and it is small:

```
internal/agentcfg/          UNCHANGED — Compose/ComposeStateful stay the pure engine
internal/render/            NEW  — the parameterized renderer + Target/FieldSet (§6.2)
  target.go                   Target, FieldSet, the three constructors
  render.go                   was entrypoint/prism.go's writers, keyed on Target
  reconcile.go                the rmw/managed-sidecar logic (unchanged semantics)
internal/entrypoint/        SHRINKS — calls render.Jail(e); keeps liveTables + genStep policy
internal/cli/               SHRINKS — calls render.Preview()/render.Host(); the mirrored
                              helpers and both hand-maintained layer tables DELETED
```

`internal/render` importing `agentcfg` and being imported by both `entrypoint` and `cli` is
the right direction: **`cli` already imports `entrypoint`** (`cli/internal.go:10`,
`cli/check/entrypoint.go:8`) while `entrypoint` imports `cli` nowhere — so the dependency
edge this needs is the one that already exists. No cycle, no `packreg`-style init dance.

**What does not move:** `liveTables` (`packsurfaces.go:107`) stays core's own — "an MCP server
is a yolo config concept, not an agent concept" — and `genStep`'s A12 fail-closed policy stays
yolo's boot behavior. The renderer reports; the caller decides whether a failure is fatal.
That split is what lets the host target be non-fatal (a refused field is a message, not a
halt) without weakening the jail's "loud and halting" rule.

### 3.3 What each target supplies

```go
// A Target is everything the renderer cannot infer.
type Target struct {
    Home       string       // jail: $JAIL_HOME | host: the real $HOME | preview: a temp dir
    Workspace  string       // ${workspace} substitution; empty => refuse those surfaces
    SidecarDir string       // last_render / overlay / managed sidecars
    HostLayer  HostLayer    // CtxMount{dir} | SameFile | None   — §6.3
    Tables     Tables       // the computed layer. Jail-derived => host target gets none
    Hooks      map[string]HookFunc  // jail: 3 | host: 1 (§2.1)
    Fields     FieldSet     // which manifest fields apply here
    Posture    Posture      // observe | assert | own  (§6.5)
}

func Jail(e *entrypoint.Env) Target      // the boot render, behavior-identical to today
func Preview(dir string) Target          // `config render` — writes nothing outside dir
func Host(home string) Target            // the new one
```

The three constructors are the whole API surface. **`HostLayer` is the subtle field** and
§6.3 is about it: in a jail the `host` layer comes from a `:ro` `/ctx` mount, so it is a
*different file* from the output; on a host target it is the output file itself, which makes
composition a fixpoint over its own result. That is why every host-target surface is `rmw`.

### 3.4 What this buys immediately, before any host target exists

Worth separating, because these land at step 3 with no new user-facing feature and no risk:

- **The §6.1 data-loss bugs stop being possible by construction.** `reset` cannot compose
  without a computed layer, because `Tables` is a field a target must fill in — and
  `Host()` declares it empty, which makes `mise/config` *refused* rather than truncated.
- **Two hand-maintained tables die.** `surfaceHasHostLayer` and `surfaceHasComputedLayer`
  (`cli/configls.go:197,204`) are Go-side maps restating what the boot path knows
  structurally, and they become derived from `Target`. This is the same pattern the project
  has already retired twice: `prismSurfaceMode` moved into the manifest as `Surface.Mode`
  ("declaring it HERE rather than in a lookup table beside the CLI is the point",
  `manifest/manifest.go:123-128`), and `builtinSurfacePaths` now reads the real declarations
  through `packload`. These two are the ones still standing.
- **`yolo config render` becomes a faithful preview.** Today it is documented as approximating
  the boot render; with one renderer it *is* the boot render against a temp home.
- **macos-user gets a row instead of a silent no-op** (§9.7).

### 3.5 The two places this could go wrong

Being explicit, because both are load-bearing:

1. **`entrypoint` must not gain a `cli` dependency.** The edge runs one way today and the
   new package sits below both. If `render` ever needs something from `cli`, that is the
   signal the split is wrong — not an invitation to add the import.
2. **The jail target must be behavior-identical, provably.** This is a refactor of the boot
   path, which A12 made fatal: a regression here does not misconfigure an agent, it stops
   the jail from starting. The existing prism tests plus a byte-equality check of every
   shipped pack's rendered surfaces before/after are the bar — the same method Stage D used
   to prove the ten Go surface literals equalled their generated pack declarations.

---

## 4. The four couplings under a `Target`

§1.2 listed them. Here is what each one does when the target becomes explicit — **two are
untouched, one is where the whole design lives, and one is the one that must not be
generalized.**

### 4.1 Reservation lists — untouched, and deliberately so

The reservation lists are the union over every pack *yolo ships*, deliberately not the selected
set (`hostfiles.go:1022-1024`: "a reservation gated on selection would let a `host_files` entry
claim a path a pack added tomorrow needs"). That union is a statement about *yolo's release*.

**A `Target` changes nothing here, and that is worth stating as a constraint rather than an
observation.** The lists are consumed at init time — `hostFileWritableRoots` is a package-level
value built by a func literal (`hostfiles.go:1028`), which is why `packreg`'s `init()` exists at
all. **Nothing in §3 may make the pack set lazier or more fallible than it is today**, because
the failure direction is silent and permissive: reserve nothing, no error
(`internal/packreg/packreg.go:5`). `internal/render` takes a `Target`, not a pack *source*; it
never touches this path.

One asymmetry worth remembering when reading that code: `builtinSurfacePaths` is a `sync.Once`
behind a function and therefore already lazy; only the `hostFileWritableRoots` literal is
init-time. They look alike and are not.

### 4.2 In-jail rendering — this is the coupling that becomes the design

`renderDeclaredSurface` and the three writers in `prism.go` are already written against
`e.Home`, resolved as `$JAIL_HOME || $HOME || /home/agent` (`env.go:76`), deliberately not the
process `$HOME`. **That is already the parameter a host target needs** — it is simply reached
through an `*entrypoint.Env` that carries twenty other things with it.

So §3.2's move is small in code and large in meaning: the writers take a `Target` instead of an
`*Env`, and `entrypoint` supplies `render.Jail(e)`. The `*Env` stops being a hidden target
declaration and becomes one of three explicit ones.

Two things must *not* move with them. `liveTables` (`packsurfaces.go:107`) is core's own — "an
MCP server is a yolo config concept, not an agent concept" — and `genStep`'s A12
fatal-collecting behavior is yolo's **boot policy**, not a property of rendering. Keeping the
policy in the caller is what lets the jail stay loud-and-halting while a host target's refusal
is a message.

`RunPackHooks` is the awkward one, and it resolves the same way. The three hooks are all *jail*
concepts — `shared_credentials` links into the machine-global tier, `per_jail_history` keys on
`YOLO_HOST_DIR`, `claude_plugins` runs the claude CLI. `packdecl.KnownHooks` already exists as
a closed set precisely so the host can validate without importing the entrypoint. So the
renderer owns *dispatch and validation*; the target supplies `Hooks map[string]HookFunc`, and
on a host target `per_jail_history` is simply absent from the map. A data decision, not a code
branch.

### 4.3 Staging and mount assembly — the one that must NOT be generalized

`stagePacks`, the writable/shared-dir mount loop, `/ctx/packs`, `YOLO_PACK_ROOT`,
`hostFileArgs`: **this is jail-target-only code and should stay recognizably so.** It exists to
fill mounts, and §2.2 says the reduced targets have none. A `Target` that grew a "how do I
stage" method would be inviting exactly the emulation §2.2 rules out.

Which also disposes of the **"the mount is the filter"** worry: it stays entirely inside
`cli/run/packs.go`, where it is already documented (`packs.go:69`). Nobody can "optimize" the
stager into rendering packs nobody asked for without editing the file that explains why not.

The macos-user backend is the live proof that this separation is the right one: it renders
surfaces (or should — §9.7) while running none of the staging path, because it has no mounts.
That is a target with a render and no provisioning, which is precisely the host target's shape,
already shipping.

### 4.4 Storage paths — an argument, not a constant

`PacksDir()`, `GlobalHome()`, `UserConfigPath()`. These encode *policy* — where a fetched pack
lives, what "machine-global" means — and the seams were already built for injection:
`packsrc.Store{Dir}` is handed `paths.PacksDir()` at all three call sites, and
`packsrc.LockPath(userConfigPath)` takes the config path as a parameter rather than calling
`paths`, because the lockfile lives beside the user config *on purpose* (packs being user
scope).

For §3 the only one that matters is **`SidecarDir`**, and it matters more than it looks. The
`rmw` reconcile sidecar (§6.3) is what makes a host-target write reversible; if the host target
and the jail target ever shared a sidecar path, a `--revert` on one would consult the other's
memory of what it asserted. So `SidecarDir` is a `Target` field, not a `paths` lookup, and the
host target's must be its own — the first genuinely new storage decision this design forces
(§9.5).

---

## 5. What a host target cannot inherit

These are not implementation obstacles. They are things whose *definition* is jail-shaped, so a
host target does not get a weaker version of them — it gets none, and has to say so.

- **The credential boundary is defined by yolo's mount table**, not by a policy the renderer
  adopts. `MayGrantHostFiles()` is enforceable only because `LoadPacks` reads
  `paths.UserConfigPath()` *directly, while knowing* the workspace config is agent-writable. On
  a host target there is no mount table, so `hostFiles` is not "less safe" — it is meaningless
  (§6.4).
- **The user-scope rule is inexpressible-not-forbidden**, and that is a property of yolo's
  config loader rather than of the pack format. It keeps working on a host target for the same
  reason: the loader is the same loader. Worth noting because it is the one jail-shaped rule
  that ports for free.
- **The origin gate loses its top tier.** `OriginEmbedded` means "in the yolo release", which
  still has meaning on a host target — but the *reason* the gate is tolerable in a jail
  (whatever runs, runs in a disposable environment) does not. So the gate needs to be stricter
  off-container, not merely equivalent (§6.4, §9.2).
- **The disposability premise.** The whole threat model is "the container is blast-radius
  reduction, never authorization." A host target has no blast radius, and the postures in §6.5
  exist because of that, not in spite of it.
- **Provisioning, per §2.1.** `install`, `writableDirs`, `sharedDirs`, `hostFiles`,
  `retireMiseTools`, and `mounts` are six of nine manifest fields, and all six are statements
  about *building an environment*. A host target does not build one — it is handed one it did
  not make and cannot describe. Every one of the six must be refused by name (§6.2's
  `FieldSet`), because the alternative is a silent skip, and §9.7 is what a silent skip looks
  like after a year in production.

---

## 6. The host as a reduced target

### 6.1 Finding: yolo already does this, and it is destructive

I expected to argue this as a posture change. It is not: **four host-side `yolo config`
verbs already resolve `~` against the invoking human's real home** (`expandHome`,
`internal/cli/config.go:333`, which is `paths.Home()`), and two of them write.

Probed on this machine, against a scratch `HOME`, with `YOLO_VERSION` unset (i.e. exactly
what a host-side invocation looks like):

**Probe 1 — `reset` rewrites a real host file, injecting yolo's managed layer.**

```
$ cat $HOME/.claude/settings.json
{"hello":"my own host setting"}
$ yolo config reset claude            # in a workspace with sidecars present
Cleared claude/settings — no captured edits (baseline re-seeded).
$ cat $HOME/.claude/settings.json
{ "hello": "my own host setting",
  "permissions": { "additionalDirectories": ["/"], "allow": [], "defaultMode": "acceptEdits", "deny": [] },
  "preferences": { "autoUpdaterStatus": "disabled" },
  "skipDangerousModePermissionPrompt": true }
```

**Probe 2 — `reset` truncates an unrelated real config to nothing.** `mise/config`'s content
comes entirely from the `computed` layer (`configls.go:208`), and
`truncateSurfaceToPureRender` (`configdiff.go:381-404`) calls `agentcfg.Compose` with **only**
`HostBytes` — no `Tables`. So the "pure render" it writes is empty:

```
$ printf '[tools]\nnode = "22"\n' > $HOME/.config/mise/config.toml     # 20 bytes
$ yolo config reset mise
Cleared mise/config — no captured edits (baseline re-seeded).
$ wc -c < $HOME/.config/mise/config.toml
1                                                                       # just "\n"
```

That is **data loss on a file that has nothing to do with any agent**, from a command whose
documented meaning is "discard the captured edits so the surface returns to what its layers
produce." Same for `opencode` (real `theme`/`model` keys replaced by yolo's two managed
keys) and `codex`.

The sibling function already knows this is wrong. `configCapture`'s docstring
(`configdiff.go:415-419`) says it "deliberately does NOT re-render the surface, because
re-rendering needs the computed layer, which is built from jail paths — so a host-side
re-render would write host paths into the file." **`reset` does exactly the host-side
re-render that `capture` documents itself as refusing to do.** The reasoning was written
down; it just wasn't applied one function over.

**Probe 3 — `capture` copies real host config into the workspace.**

```
$ printf 'model = "gpt-private"\napi_key_hint = "acct-12345"\n' > $HOME/.codex/config.toml
$ yolo config capture codex
Captured codex/config — 3 keys now recorded.
$ cat .yolo/prism/codex-config.overlay.json
{ "api_key_hint": "acct-12345", "approval_policy": null, "model": "gpt-private" }
```

The path to trigger any of these: run the verb host-side, in a workspace that has launched
a jail (so `<workspace>/.yolo/prism/*.last_render` exists — `reset` no-ops without it).
`.yolo/` is gitignored, so probe 3 is not a commit-leak; and `<workspace>/.yolo` is the
same live-bound directory the jail writes, so the sidecars are routinely present. `render`
is correctly read-only and `claude/config` correctly refuses (it's `rmw`).

**Why this is a design finding and not just a bug report.** The root cause is that "which home
am I composing into?" is an *implicit* `paths.Home()` host-side and an *explicit* `e.Home`
in-jail — i.e. it is §3.1's duplication, seen from the failure end. The entrypoint got this
right on purpose
(`env.go:76`, "deliberately not the process `$HOME`"); the host CLI never had to decide,
because it was only ever meant to *preview*. A host target forces the parameter to be explicit
everywhere — which is precisely the fix. So the destructive host-side writes are not an
argument against pointing this at the host; they are an argument that we already did, by
omission, and should do it on purpose.

(`configls.go` already knows the shape of the problem — `surfacesAreLocal()` at
`configls.go:341` exists so `ls` won't *claim* a host file is a jail surface. The knowledge
is there; `reset` and `capture` just don't consult it.)

### 6.2 The four targets, and what `FieldSet` is for

§3.3 gives the struct. What §6 adds is the *set of targets it has to express*, ordered by
confinement (§2.2's axis):

| Target | Home | `host` layer | Output | Blast radius |
|---|---|---|---|---|
| **jail** | `/home/agent` (`$JAIL_HOME`) | `/ctx/host-<pack>/…` (`:ro` mount) | jail overlay | disposable |
| **macos-user** | `/Users/_yolojail` | (host read, no mount) | a real user's home | that user, Seatbelt-confined |
| **preview** | a temp dir | the real host file, read-only | stdout | none |
| **host** | the human's real `$HOME` | **the output file itself** | the human's real dotfiles | **their live environment** |

Only the last row is new. `preview` is `yolo config render` with its target finally stated;
`macos-user` **ships today** and is supposed to render surfaces into a real home already
(§9.7). That is what makes "target" a description of the code rather than a concept invented
for the host case: yolo already renders into a real home on a real OS with no container.

**`FieldSet` is what makes §2.1's census executable rather than a table in a doc.** The host
target declares `install`/`writableDirs`/`sharedDirs`/`hostFiles`/`retireMiseTools`/`mounts`
inapplicable, so a pack using one gets a **refusal naming the field** instead of a silent skip.
Two reasons to insist on the naming rather than the skipping:

- §9.7 is what a silent skip looks like after a year in production — a backend rendering zero
  surfaces every launch, with nothing in the output to say so.
- macos-user's `host_files` filter already does it the right way, and the reason is written in
  the code: a passed-through entry "would render with an empty host layer and silently serve
  its defaults instead of the host file the user named" (`macosuser/runplan.go:160-166`).
  Refusing loudly is the shipped precedent; the failure mode of not doing so is documented.

### 6.3 The structural problem: on a host target, the `host` layer *is* the output

This is the one genuinely hard thing, and it is not a detail. In a jail, the layer stack
reads two different files:

```
   ~/.claude/settings.json  (host, :ro at /ctx)  ──►  layer `host`
                                                       │
                     defaults < host < workspace < overlay < computed < lua < managed
                                                       │
                                                       ▼
                              /home/agent/.claude/settings.json  (output)
```

On a host target there is only one file, so the composition becomes a **fixpoint over its
own output**. Compose twice and yolo's managed keys are indistinguishable from the user's
own. This is the exact bug A7 fixed for `render` host-side — "every key yolo had written
came back labelled `host`" (`config.go:240-247`) — and it is *structural* for a host
target, not a mistake to avoid.

There are three known answers and the ecosystem has picked two of them:

1. **Own the file outright** (home-manager): the output is generated; a hand edit is either
   refused or backed up. Clean semantics, hostile to the actual situation — these agents
   rewrite their own config files constantly (`npm config set`, any CLI's first run).
2. **Own a source, generate the destination** (chezmoi): the user edits `~/.local/share/…`,
   the tool renders `~/.claude/settings.json`. Correct, and a big ask — the user now has two
   files and must learn which one to edit.
3. **Read-modify-write, regenerating the keys yolo owns** — which yolo *already built*, as
   `mode: rmw`: the boot render reads the agent-owned file and `regenerateManagedTables`
   (`entrypoint/prism.go`) replaces only the dynamic managed tables yolo owns, leaving the
   agent's own keys. (Refresh note: the original text cited a `reconcile` sidecar
   `reconcileRMWTables`/`rmwManagedPath`; that was superseded — yolo now REGENERATES the
   managed keys wholesale each boot rather than reconciling against a sidecar, so a UI-added
   entry in a yolo-owned key is overwritten with a drop notice. The design point below still
   holds: the agent's own keys survive because yolo only rewrites the keys it declares.) A
   host-target `--revert` (§7.2) is the one place that still wants a memory of what was
   asserted — see the open question in §9.5.

**(3) is the answer, and it is already the shipped design for exactly this problem.**
`claude/config` (`~/.claude.json`, agent-owned, yolo asserts keys into it) is a host-target
surface in every respect except which home it lives in. So the host target is not a new
engine mode; **it is the existing `rmw` mode applied to every surface** rather than to the
two that needed it in a jail.

That has a crisp consequence worth stating as a rule:

> **On a host target, every surface is `rmw`.** The reason is not "the editor and yolo are
> the same person" (a loose framing — a host agent edits its own config constantly, and that
> is fine). It is that **`rmw` only ever rewrites the keys yolo declares** (`managed` +
> dynamic tables), filling absent `defaults` and touching nothing else — so an agent's own
> keys are preserved *for free*, with no whole-file compose and therefore no capture overlay
> to protect them. Capture exists only to make *whole-file* composition non-destructive, and
> a host target does no whole-file composition. A key yolo *does* manage is overwritten on
> the next `apply` (regenerate-don't-reconcile) — which is correct and the only workable
> option for a key yolo owns. `computed`-mode overwrite-every-*whole-file* is unacceptable
> here for the same reason probe 2 is a bug. *(Confirmed 2026-08-01, env-manager plan OQ-4.)*

### 6.4 What else changes on a host target

- **`${workspace}` has no referent.** claude's `projects["${workspace}"]` is a per-jail
  assertion. A host target must either refuse a surface that uses the placeholder or bind it
  to the cwd, and refusing is the honest option.
- **`reads-host` (was `hostFiles`) is meaningless and must be refused.** It names a host file
  to mount into a jail. On a host target the source and the destination are the same
  filesystem. Honoring it would be a copy the user did not ask for.
- **`mount` is unavailable, and must be refused rather than emulated** (§2.2). No mount
  namespace means no `:ro`, and a copy goes silently stale — a pack update that appears to
  apply and doesn't. macos-user's `reads-host`/`host_files` filter is the precedent. The
  *composed* artifacts a pack delivers — the merged skills tree, `AGENTS.md` — are a separate
  question: those are composition results (their own `skills`/`briefing` kinds now) and port
  like config surfaces do, which is why §7.3's walkthrough writes them and §6.5's `assert`
  posture covers them.
- **The origin gate keeps its tiers, and off-container it needs to stay strict** (§5).
  `OriginEmbedded` still means "shipped in the yolo release." Since this was written, a
  *fetched* pack's host access stopped being an outright refusal and became **install-time
  approval** (recorded per-commit in the lockfile — see [pack-system.md](pack-system.md) §9).
  That is the right primitive to build a host target on: the consent step already exists. But
  the reason the gate is *tolerable* in a jail — whatever runs, runs in something disposable —
  is gone off-container, so a host target must gate at least as tightly as the jail, and
  `program via installer` (a curl-piped shell script) would be running against the human's real
  home.
  **This is the single sharpest thing in the whole proposal.** A host target should refuse
  `program` (both `installer` and `npm -g`) entirely, at least at first — even for an
  otherwise-approved pack: the host target manages *config*, and installing tools into a real
  machine is a different feature with its own design.
- **Hooks: two of three must be off.** `shared_credentials` symlinks a credentials file into
  a machine-global dir — on a host target the credentials file *is* already machine-global,
  so the hook is a no-op at best and a broken symlink at worst. `per_jail_history` keys on
  `YOLO_HOST_DIR` and has no meaning. `claude_plugins` runs `claude` against the user's real
  config. The `Hooks` map in §3.3 makes this a data decision instead of a code branch.

### 6.5 The posture, stated as a table

Since §6.3 collapses the four modes to one on a host target, what remains is *how much the
renderer is allowed to do*. This is `Target.Posture` from §3.3:

| Posture | Reads | Writes | Use |
|---|---|---|---|
| `observe` | host files, sidecars | nothing | `yolo config apply --host --dry-run` — what would change |
| `assert` | host files, sidecars | only keys the pack declares `managed`, recorded in a sidecar | the real product: keep your MCP servers in sync across five agents |
| `own` | host files | the whole file, backing up first | opt-in per surface, for someone who wants home-manager semantics |

`observe` is the default and `own` needs a per-surface opt-in. Note what this makes
possible that nothing currently does: **`assert` across every agent from one declaration**.
That is the actual unmet need — you have five agents that each want the same MCP server in
a different dialect, and the per-agent `derive` Lua slot already expresses the reshape
(`packs/<agent>/derive.lua`, run through `internal/agentcfg/luahook`; this replaced the
`computed[] + project` DSL the original text referenced), and today that machinery only ever
runs inside a jail.

### 6.6 A host target is user-scoped, not workspace-scoped

**Added 2026-07-31, in response to a review of the env-manager plan.** An earlier framing
of the reconcile/sidecar question asked "when two *workspaces* `apply --host` into the same
machine file, whose keys win?" — and that framing is wrong at its root. **A workspace should
not be an input to host management at all.** Stating it plainly, because it dissolves a
question rather than answering it, and because it follows from a rule the pack system already
enforces:

> **What yolo asserts into your real `$HOME` is a function of your *user* configuration and
> the packs *you* have installed — never of which repository you happened to run `apply
> --host` from.**

This is the same boundary packs already draw. Packs are **user scope only**: a workspace
config cannot name one (`pack-system.md` §8), *precisely because* a workspace config travels
with a repo and is agent-editable, so it must not decide what crosses into your environment.
The host is your realest environment, so that rule is *most* load-bearing there, not least: a
repo you `cd` into must not be able to reach into `~/.claude/settings.json`.

Three consequences, each of which removes a problem the workspace-scoped framing created:

- **The "two workspaces collide" question disappears.** There are not two writers racing into
  one file — there is one description (your user config + your packs) with one owner (you).
  `apply --host` renders *that*, identically, from wherever you invoke it. The `cwd` selects
  nothing.
- **`${workspace}` surfaces are refused, not bound.** A surface keyed on `${workspace}` (e.g.
  claude's `projects["${workspace}"]`) is inherently per-repo; on a host target it has no
  referent and is refused by `FieldSet` (§6.4) — consistent with "no workspace input," not a
  special case.
- **A host sidecar, if one is ever needed** (for capture — the agent's own between-applies
  edits, not a `--revert`; see the env-manager plan's reframed OQ-A), lives at a
  **user/machine-scoped** path like `~/.local/state/yolo-jail/host-render/`, keyed by the
  target file, **never by workspace.** §4.4's `SidecarDir` becomes a user-scope constant for
  the host target, not a per-workspace path.

The one honest exception is the *invocation*: you might be standing in a repo when you type
`yolo apply --host`. That is fine — the command reads your **user** config to decide what to
assert; the repo you are standing in contributes nothing to the host render. If a user ever
wants a genuinely repo-specific host tweak, the answer is the same as for a jail: put it in a
**local pack** they have chosen to install at user scope, not in a workspace config that any
`cd` would silently activate.

---

## 7. Three walkthroughs

### 7.1 yolo launches a jail (nothing observable changes)

**This walkthrough exists to show that the jail path is a refactor with no behavior delta.**
The only line that differs from today is the one that names its target.

```
yolo -- claude
  config.LoadPacks(paths.UserConfigPath())        # unchanged: user-scope rule
  packload.MaterializeEmbedded(packs.FS, scratch) # unchanged: yolo's own corpus
  packstage.Stage(...)                            # unchanged: PROVISIONING (§4.3)
  <clear _official; copy selected>                # unchanged: the mount IS the filter
  -v .../packs:/ctx/packs:ro -e YOLO_PACK_ROOT=…  # unchanged: argv
  ─── container ───
  packdecl.Decode(pack.json)                      # unchanged: the schema, all 12 kinds
  render.Render(render.Jail(e), surfaces)         # CHANGED: was an implicit *entrypoint.Env
  entrypoint: program, mount, state, files, …     # unchanged: the jail-only kinds
```

That single changed line is the entire jail-side risk, and §3.5 says how to retire it:
byte-equality of every shipped pack's rendered surfaces, before and after. If the diff is
empty, the refactor is done — the boot path cannot have gotten quieter or louder, because
`genStep`'s A12 policy never moved (§4.2).

### 7.2 The human manages their own machine

At §8 step 5, with `render.Host($HOME)` as the target:

```
$ yolo config apply --host --dry-run
TARGET  host (/home/me)   posture: assert
SURFACE          PATH                              MODE  ASSERTS
claude/settings  ~/.claude/settings.json           rmw   permissions, preferences
codex/config     ~/.codex/config.toml              rmw   mcp_servers.sequential-thinking
copilot/mcp      ~/.copilot/mcp-config.json        rmw   mcpServers.sequential-thinking
mise/config      ~/.config/mise/config.toml        —     refused: computed layer is jail-only
claude/config    ~/.claude.json                    rmw   projects — refused: ${workspace}

INAPPLICABLE (FieldSet, §2.1)
claude  program       refused: pack installs are jail-only
claude  state:machine skipped: host credentials are already machine-global
claude  reads-host    skipped: no jail to carry a host file into

$ yolo config apply --host              # assert: only declared keys, records a sidecar
$ yolo config apply --host --revert     # removes exactly what the sidecar says it added
```

Three things to notice. `mise/config` is *refused* rather than truncated — probe 2 turned into
a designed outcome, and it is refused because `render.Host()` declares `Tables` empty rather
than because someone remembered to add a check (§3.4). The INAPPLICABLE block is `FieldSet`
being legible instead of silent, which is the §9.7 lesson applied. And `--revert` is meaningful
*only* because the reconcile sidecar already exists; without it, "undo" is indistinguishable
from "delete the user's keys."

### 7.3 One pack, three environments

`house-rules` has a `skills` tree, a `briefing` (`AGENTS.md`), and a `config` surface whose
dynamic layer is produced by a `derive.lua` MCP projection. In a jail the surfaces are composed
and the trees arrive as `:ro` mounts. On macos-user and on the host the surfaces are asserted
into the real home, the merged skills tree is *written* (a composition result, §2.2), and
anything that needed a real mount is **refused by name**.
**Same manifest, no target-specific kinds** — the manifest declares paths and reshapes, and
the *target* decides what is honored. That is the whole content of "core does not know what an
agent is," extended one level: **a manifest that has to know which environment it is on is the
signal this design went wrong.**

A `program` contribution is where it gets tested. `house-rules` has none, but `claude` does —
and on the host it is refused *by the target*, not by anything in the pack. A pack author
writes one `program` spec and never thinks about confinement levels; the target that cannot
honor it says so by name.

---

## 8. What I would actually do, in order

**Ordered so that each step is independently valuable and the destructive path is fixed
first.** Nothing here needs a new module or a decision about one.

1. **Fix probes 1–3, now, ahead of any refactor.** Host-side `reset` (`configReset`,
   `configdiff.go:220`) and `capture` (`configCapture`, `:420`) must refuse (or require
   `--force`) when `surfacesAreLocal()` is false — the predicate already exists at
   `configls.go:341` and is currently consulted only by `composedFileExists` (`:330`). This is a
   live data-loss path on the maintainer's own machine and it should not wait for an
   architecture decision. It is also the cheapest possible down-payment on step 3: it makes the
   implicit target explicit at the one place it currently misfires.
2. **Decide the capture-privacy question** (§9.3) — cheap, and the other thing probe 3
   surfaced. Independent of everything below.
3. **Introduce `internal/render` with `Target`** (§3.2, §3.3), collapsing the two render paths
   into one. **This is the load-bearing step**, and its value does not depend on a host target
   ever shipping: it deletes the duplicated writers, retires `surfaceHasHostLayer` and
   `surfaceHasComputedLayer`, and makes `yolo config render` a faithful preview instead of an
   approximation (§3.4). Retire the risk with §3.5's byte-equality check.
4. **Fix macos-user by making it a target row** (§9.7), not a special case. This is the cheapest
   possible proof that step 3's abstraction is the right one: an existing backend that is
   *supposed* to render surfaces into a real home, currently rendering none. If `Target` cannot
   express macos-user cleanly it will not express the host either — and we learn that for the
   price of a bug fix.
5. **Add `FieldSet`** (§6.2) so an inapplicable field is a refusal naming the field. Sequenced
   after step 4 deliberately: macos-user is the case that tells us what the refusal messages
   need to say, because it is the one where we already know silence was the wrong answer.
6. **Ship `yolo config apply --host`** (§2.2 option a) — `observe`, then `assert`, `own` maybe
   never, `install` refused outright (§6.4). **This is where the §2 motivating case lands**: the
   `pi` pack you already trust, applied to the host you are about to debug on.

**If only one of these ever happens, it should be #1.** If two, #1 and #3 — because #3 is the
reason #1's class of bug exists, and it pays for itself in deleted code before the host target
is even reachable.

---

## 9. Open questions — the discussion part

**9.1 Is yolo "a jail" or "an interface for describing environments agents run in"?**
**Answered 2026-07-27, in the second sense** — see
[yolo-as-environment-manager.md](yolo-as-environment-manager.md), which takes §2.2's confinement
axis as the product's organizing idea (a `confinement: jail|sandbox|host` dial, with `runtime`
demoted to a mechanism hint) and makes this doc's host target one notch of it rather than a
special case. The reasoning below is what the question looked like before that ruling, and its
cost analysis still applies. §2.2's
confinement axis says the second is already true *descriptively* — `macos-user` ships, has no
container, and is documented as one product with the container backends. What is unsettled is
whether we say so **normatively**, because that changes the scope of everything: a "jail" has
one obvious answer to "should this manage my host config?" (no), and an "environment interface"
has the opposite one (that is the job).

The cost of naming it is that the boundary stops being self-evident. Today "does it run in the
container?" answers most scope questions for free. Adopt the wider framing and every future
feature needs an explicit ruling about which confinement levels it applies to — that is what
`FieldSet` (§6.2) is for, and it is real ongoing work, not a one-time rename. My recommendation
is to build §8 steps 1–5, which are correct under *either* framing, and let the naming follow
from whether the host target actually gets used.

**9.2 Does a host target defeat the sandbox's purpose?** The threat model is "the container is
blast-radius reduction, never authorization." A render that writes the human's real dotfiles has
no blast radius — fine *if it is understood as a distinct, narrower feature*, corrosive if it
becomes the recommended way to configure agents because it is more convenient. Refusing
`install` (§6.4) is the load-bearing mitigation, and I would want it stated as an invariant
rather than a default. **The strongest version of the worry:** once `apply --host` exists,
"why not just run the agent on the host with the good config?" is one step away, and the answer
has to be a product decision rather than a missing feature.

**9.3 Should `capture` redact?** Probe 3 copies whatever is in the real file — including an API
key hint — into `<workspace>/.yolo/prism/*.overlay.json`. `.yolo/` is gitignored so it is not a
commit leak, but it is agent-readable, and the whole point of the credential boundary is that
the agent's workspace does not see host secrets. Options: refuse when `surfacesAreLocal()` is
false (which is §8 step 1 anyway, and probably sufficient), or key-level redaction, which needs
a notion of which keys are sensitive and I do not think we should invent one.

**9.4 What does `install` mean on a host target, if not "never"?** "Never" is my recommendation
and also a real limitation: the most useful thing a pack could do for a fresh machine is
*install the agent*. If the answer is eventually "yes, with an explicit per-invocation grant",
that grant is a new security surface and needs its own design — it is not a flag.

**9.5 Where does a host target's sidecar live, and who arbitrates?** §4.4's problem. The jail
target's reconcile sidecars live in `<workspace>/.yolo/prism/`, which is workspace-scoped
because a jail is. A host target's assertions are *machine*-scoped — the config it wrote is not
about any workspace — so the sidecar wants to be somewhere like
`~/.local/state/yolo-jail/host-render/`. That is a new storage location with a real question
attached: if two workspaces both `apply --host`, they are asserting into one file, and the
sidecar is the only record of who put what there. Last-writer-wins with a shared sidecar is
probably right, but it should be *decided*, because the alternative failure is a `--revert` that
removes another workspace's keys.

**9.6 Do the reservation lists survive contact with a *configured* pack?** §4.1 keeps them
init-time and untouched, which is correct for the embedded corpus. But if a non-embedded pack
ever needs to participate in a reservation — and `hostfiles.go:704-710` already documents that as
a known gap — the lists become fallible, and the failure direction is permissive. §3 must not
make this worse; it should not try to fix it either.

**9.7 macos-user is the existing host-shaped target, and it currently gets no packs at all.**
`RunDarwinBootstrap` calls `LoadJailPacks` → `ConfigurePackSurfaces` → `RunPackHooks`
(`darwin.go:57-62`), but the macos-user run path returns at `cli/run/run.go:73` *before* `stagePacks`,
and `YOLO_PACK_ROOT` is never set on that backend — verified, zero occurrences outside the
container path. So on macos-user the pack loop runs over an empty list every launch, silently.
That backend is the closest thing to a host target we already ship (a real macOS home, no
container, composed by the same writers), which makes it the natural first non-jail target — and
the reason §8 puts it before `FieldSet` rather than after.

**9.8 Is a "macos-user for Linux" a real fourth row, or a distraction?** A bwrap/Landlock
environment — real user, real home, no container — would sit between `macos-user` and the bare
host on §2.2's axis. **The relevant point for this doc is that it needs no new concept**: it is
another confinement level with the same `surfaces`-yes / `install`-maybe / `mounts`-unavailable
profile, which is evidence the axis is real rather than a framing imposed to make the table
tidy. Whether anyone wants it on Linux — where a container is cheap and already works, unlike on
macOS where it costs a VM — is a genuinely different question, and I would not build it on this
doc's motivation. It belongs on the roadmap only if someone wants agent confinement *without* a
container runtime installed.

---

## 10. The case against, stated fairly

Against the whole thing:

- **§6.1's defects are fixable in about twenty lines, today.** If the only real motivation is
  "host-side composition is currently destructive", then §8 step 1 is the entire answer and
  steps 3–6 are a large response to a small question. This is the most uncomfortable objection
  and the honest reply is: step 1 should ship regardless, and whether anything follows it should
  depend on whether anyone actually wants the host target — not on this doc.
- **The host target may have no users.** The `pi`-on-the-host case is one person's workflow, and
  the alternative ("copy the settings once by hand") costs minutes, not hours. Everything in §6
  is designed against a need that has been *stated* but not yet *felt repeatedly*.
- **A host target is a genuinely new risk surface** and the sandbox is the product. Pointing the
  same pipeline — including pack-supplied `installerUrl` — at a human's live environment is a
  different posture from everything else here, and §6.4's refusals are the only thing standing
  between the two. Refusals are a weaker guarantee than "there is no code path."
- **`Target` could become the thing that makes every field a matrix.** Today a pack field either
  works or the code doesn't compile. With four targets and a `FieldSet`, every new manifest field
  needs a ruling per row, and the rulings live in a different package from the field. That is
  real ongoing cost (§9.1), paid by whoever adds the *next* pack feature rather than by this
  design.

Against §3 specifically, which is the part I would defend hardest:

- **It refactors the boot path, and the boot path is fatal by policy.** A12 made a pack failure
  halt the jail. A regression in `internal/render` does not misconfigure an agent — it stops
  jails from starting, including the one the maintainer is reading this in. §3.5's byte-equality
  check is the mitigation, and it needs to actually be written, not intended.
- **Two implementations that mirror each other are not obviously worse than one with four
  parameters.** The duplication is legible: each path is readable on its own, and the three
  "Mirrors internal/cli…" comments mean nobody is confused about it. A single renderer with a
  `Target` struct trades that for a place where every caller's assumptions coexist. My answer is
  §6.1 — the duplication has already produced data loss, and it did so precisely *because* each
  side was readable on its own — but "the drift was documented" is a fair rebuttal to "the drift
  was invisible."

The counter to all of it, and the reason I would still do §8 steps 1 and 3: the *reason* the
§6.1 defects exist is that the target was never a parameter. That is an architectural fact
rather than a bug, it will keep producing bugs of this shape, and making it explicit is worth
doing even if no host target is ever shipped.
