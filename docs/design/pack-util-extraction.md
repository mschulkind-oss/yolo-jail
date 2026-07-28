# Pulling the pack system out of yolo — a separate util that yolo consumes, and that manages host configs too

**Status:** design, for discussion, 2026-07-27. Written in response to: *"I want you to
write up a doc with ideas how we could pull all of this pack stuff out of yolo, yet still
use it in yolo, but also manage the host configs. this would be a separate util."*

**Audience:** whoever decides whether this happens. §1 is measurement, §6 is the part that
needs a ruling, §9 is what I could not settle from the code.

**Reads with:** [pack-specification-and-loading.md](pack-specification-and-loading.md) (the
system as built — this doc assumes it), [what-yolo-is.md](what-yolo-is.md) (the earlier
"is the engine separable?" answer, which this extends from the engine to the pack system),
[../plans/agent-config-packs.md §9.3](../plans/agent-config-packs.md) (the previous round of
this exact question, answered "not yet, no second consumer" — §1.3 argues that test was the
wrong one),
[composed-file-permissions.md](composed-file-permissions.md) (the postures a host-side
writer would have to honor).

---

## 0. The one-paragraph version

**The motivation is one declaration, two environments.** A pack already says how `pi` is
configured — approval prompts, MCP servers, skills. You write it once and it renders only
inside a jail; the day you need that same agent to behave the same way *on the host*, none of
it is available. Nothing intrinsic to the mechanism requires that (§2).

**The code boundary already exists.** The pack system's five packages have zero edges to
anything jail-shaped, and the composition engine they sit on has zero edges to
`config`/`paths`/`cli`/`entrypoint`. Extraction is not an untangling job. **So the design
work is three other things:** (1) what the *interface* between yolo and the util is, (2) how
the four residual couplings — reservation lists, in-jail rendering, host-side staging,
storage paths — get re-expressed across it, and (3) what "manage the host configs" actually
means, which is the dangerous part.

On (3) there is a finding: **yolo already composes into the invoking human's real home, by
accident, and it is currently destructive.** Probes below truncate a real
`~/.config/mise/config.toml` and rewrite a real `~/.claude/settings.json`. So this is not a
posture we would be *adopting*; it is one we are *already in* without having designed it.
That reframes the proposal from "should we point this at the host?" to "we already do —
should it be deliberate?"

**And the capability turns out not to need the new repo.** Making the render target explicit
(§6.2, step 3) delivers the `pi`-on-the-host case inside today's binary. The extraction buys
a boundary — the tool editing your live dotfiles is not the one launching containers — and
that, not the feature, is the open decision (§9.1).

---

## 1. The measurement: what the boundary already is

Everything in this section is `go list -deps` and `wc -l`, not estimate.

### 1.1 The candidate module

| Package | Non-test | Test | First-party deps |
|---|---|---|---|
| `internal/packdecl` | 300 | — | **none** |
| `internal/packstage` | 256 | 225 | **none** |
| `internal/packsrc` | 638 | 503 | **none** |
| `internal/packload` | 451 | 524 | `agentcfg/{codec,manifest,project}`, `packdecl`, `jsonx`, `tomlx` |
| `internal/agentcfg` | 1103 | 2992 | `agentcfg/*`, `jsonx`, `tomlx` |
| `internal/agentcfg/manifest` | 753 | — | `codec`, `project`, `jsonx`, `tomlx` |
| `internal/agentcfg/codec` | 620 | — | `jsonx`, `tomlx` |
| `internal/agentcfg/project` | 230 | 200 | **none** |
| `internal/agentcfg/luahook` | 963 | — | (gopher-lua only) |
| `internal/jsonx` | 714 | — | none |
| `internal/tomlx` | 244 | — | `jsonx` |

**Totals: 5,314 non-test / 6,568 test lines**, out of 46,166 non-test lines in the repo —
about 12%. Third-party: `BurntSushi/toml` and `yuin/gopher-lua`, both vendored, both pure
Go. The only `os/exec` in the whole set is `packsrc`'s `git` invocation
(`internal/packsrc/store.go:94`, binary resolved by `store.go:81`).

**Zero edges** from any of it to `config`, `paths`, `cli`, `cli/run`, `entrypoint`,
`storage`, `image`, or `loopholes`. `internal/agentcfg` does import `packload` — but only
in `packmanifest_test.go`, so it is not a production coupling.

Plus the pack *corpus*: `packs/` is 7 files, 28 KB, 504 lines of JSON.

### 1.2 The four residual couplings

These are the only places yolo reaches into the pack system, and they are the whole design
problem. Every one is a real call site, not a category.

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
breaks this, silently, in the reserve-nothing direction. **This is the sharpest constraint in
the whole extraction** and it is not obvious.

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
`hostFileArgs`. This is argv generation. It is the least extractable thing here and nobody
should try.

**(4) Storage paths — `internal/paths`.** `PacksDir()` (the pack store, three non-test call
sites, all constructing a `packsrc.Store`), `GlobalHome()` (the shared tier),
`UserConfigPath()` (which the lockfile sits beside, `packsrc/lock.go:57`). A handful of
constants, but they encode *policy*: where a fetched pack lives, and what "machine-global"
means.

### 1.3 What this measurement means

Nothing in §1 is an argument *for* extraction — it only says the bill is small. The argument
is in the next section, and it is not the one the previous round of this question was
answering.

[agent-config-packs.md §9.3](../plans/agent-config-packs.md) concluded "extractable, but it
has to be motivated by an external consumer, and there isn't one yet." **That test was the
wrong one, and it is worth saying so rather than inheriting it.** It looked for a *third
party* who wanted an `agentpack` binary. The real motivation was already sitting in the
product: we have built a mechanism for declaring how an agent is configured and how an
environment is set up, and **its reach stops at the container wall** — for no reason
intrinsic to what it does.

The consumer is not external. **It is the same person, on the other side of that wall.**

---

## 2. The motivation: an agent config that stops at the container wall

**The point is one definition, two environments.** A pack already says how `pi` is
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

That reframes the two halves of the ask. They are not "a refactor" and "a product":

**Half 1 — the pack engine becomes a thing that can run anywhere.** Mechanically a refactor
(§1 prices it), but the *point* is not tidiness. It is that a renderer welded to
`internal/entrypoint` can only ever render into a container. Extraction is how the
declaration stops being jail-only.

**Half 2 — a target that is the host.** §6. This is where the risk is, because the blast
radius is the human's real environment rather than a disposable one.

**They are separable, and the honest accounting is:** half 2 is *also* reachable without
half 1 (a host-config mode inside `yolo` itself) and would be strictly cheaper. What half 1
buys is that the thing writing your real dotfiles is not the same binary whose job is
launching containers — plus a pack author who can `lint` without a container runtime, and a
pack format with one implementation instead of two. Whether that is worth a second repo is
§3's question and §10's counter-argument; **it is no longer a question of whether anyone
wants the capability.**

---

## 3. The interface between yolo and the util

Four options. They are not equally good and the third is a trap.

### A. Vendored library — the util is a Go module, `yolo` imports it

```go
import "github.com/mschulkind-oss/agentpack/packload"
```

- **Reservation lists (coupling 1) keep working unchanged** — still init-time, still
  infallible, still an embedded FS. This is the only option where that is true for free.
- Nothing ships on the host but `yolo`. The host ship set stays `{yolo}`.
- `flake.nix`'s `goSrc` trap gets *easier*: an external module lives in `vendor/`, which is
  already in the fileset, so it can't silently vanish the way a new top-level dir can.
- **Cost:** a version bump is now a two-repo dance. And it does not, by itself, deliver the
  host target — it just makes it buildable elsewhere.

### B. Sidecar binary — `agentpack` on the host, `yolo` shells out

- Real separation, real independent release cadence, and a pack author can `agentpack lint`
  without yolo.
- **Doubles the host install and upgrade surface**, and the two binaries must agree on
  storage layout, mount syntax, and the origin gate. §9.3 already priced this and called it
  "a bill, not a wall."
- **Breaks coupling 1 badly.** An init-time package-level reservation list cannot come from
  a subprocess. It would become lazy and fallible, and the failure direction is
  *reserve-nothing* — a `host_files` entry silently permitted to claim `~/.claude`.
  Fixable, but the fix is "make the reservation lists explicit and fallible", which is its
  own change with its own tests.

### C. Declarative protocol — the util emits a manifest, `yolo` consumes it

Tempting because a seam already exists: `MarshalPacks`/`UnmarshalPacks`
(`internal/config/packs.go:396,409`) define a `YOLO_PACKS` wire form, complete with json
tags. **It has no production producer and no production consumer** — only
`packs_test.go:205` references it. So there is a dormant, already-designed protocol sitting
right there.

Do not use it as the extraction interface. The wire form carries *selection* (which packs,
which filters), not *content*, and content is the hard part. A protocol boundary here means
serializing every `manifest.Surface`, every codec decision, and every layer — i.e.
reinventing `packdecl` as a second schema, with drift. Option C is the right shape for
*selection* and the wrong shape for *composition*.

### D. Library + thin CLI in one module — **recommended**

One new module exposing both a Go API and a `agentpack` binary. `yolo` imports the library
(option A, so coupling 1 is untouched); the binary exists for pack authors and for the host
target, and is **not** in yolo's ship set.

```
agentpack/                       # new module
  packdecl/  packstage/  packsrc/  packload/     # verbatim from internal/
  compose/   {codec,manifest,project,luahook}/   # was internal/agentcfg
  target/                                        # NEW — see §6
  cmd/agentpack/                                 # lint | ls | explain | install | render | apply
yolo-jail/
  internal/...                   # imports agentpack; keeps run/, entrypoint/, config/
  packs/                         # the six embedded packs — stays HERE (see §4.1)
```

The recommendation is D because it gets the host target's delivery vehicle without paying option B's
coupling-1 tax inside yolo, and because a shared module is the only arrangement where the
pack *format* has exactly one implementation.

---

## 4. Re-expressing the four couplings

### 4.1 Reservation lists — the packs stay in yolo, only the *reader* moves

The temptation is to move `packs/*/pack.json` into the util. **Don't.** The reservation
lists are the union over every pack *yolo ships*, deliberately not the selected set
(`hostfiles.go:1022-1024`: "a reservation gated on selection would let a `host_files` entry
claim a path a pack added tomorrow needs"). That union is a statement about *yolo's release*,
not about the pack format. If the util owned the corpus, a util upgrade could change what a
yolo config validates — the reservation lists would drift from the binary enforcing them.

So: `packs/` and `internal/packreg`'s `init()` stay in yolo. The util supplies
`packload.MaterializeEmbedded(fs.FS, dest)` and yolo hands it `packs.FS`. That is already
the signature (`packload.go:195`) — the embedded FS is *injected*, not owned
(`SetEmbeddedFS`, `embedded.go:47`). **The dependency-injection seam that makes this work
already exists, for an unrelated reason** (a cycle-in-test with `packload`'s own embed-drift
test). Nice accident; the extraction should not disturb it.

### 4.2 In-jail rendering — moves as-is, parameterized on a home

`renderDeclaredSurface` and the three writers in `prism.go` are already written against
`e.Home`, resolved as `$JAIL_HOME || $HOME || /home/agent` (`env.go:76`), deliberately not
the process `$HOME`. **That is exactly the parameter a host target needs** (§6). So this code moves
into the util behind an explicit target instead of an `*entrypoint.Env`, and yolo's
entrypoint becomes a caller that supplies `{home, workspace, sidecarDir, ctxRoot,
liveTables}`.

Two things must *not* move: `liveTables` (`packsurfaces.go:107`) is core's own — "an MCP
server is a yolo config concept, not an agent concept" — and `genStep`'s A12 fatal-collecting
behavior, which is yolo's boot policy, not the util's.

`RunPackHooks` is the awkward one. The three hooks are all *jail* concepts —
`shared_credentials` links into the machine-global tier, `per_jail_history` keys on
`YOLO_HOST_DIR`, `claude_plugins` runs the claude CLI. The util can own the *dispatch and
validation* (the closed set is already in `packdecl.KnownHooks` precisely so the host can
validate without importing the entrypoint); yolo keeps the *implementations*. That is an
interface: `Hooks map[string]func(HookRequest) error`, supplied by the caller. It also
answers §8.6 of the spec doc from the other direction — a host-config target would supply a
*different* hook table, and `per_jail_history` simply wouldn't be in it.

### 4.3 Host-side staging and mount assembly — stays in yolo, entirely

`stagePacks` splits cleanly along a line that is already drawn: the *staging* half
(materialize, filter, copy, load) is `packstage` + `packload`, which move; the *mount* half
(`/ctx/packs`, `YOLO_PACK_ROOT`, writable-dir binds, skills targets, briefing composition)
is argv, which does not.

The one subtlety is **"the mount is the filter"**. `stagePacks` clears `_official` and
copies in only selected packs, because the entrypoint renders *everything* under
`YOLO_PACK_ROOT` (`packs.go:70`). That invariant spans both halves: the util's stager
produces a tree, and yolo's assembler decides that the tree *is* the selection. Write it
down at the interface or someone will "optimize" the stager to stage all six and filter
later, which renders packs nobody asked for.

### 4.4 Storage paths — inject, don't reimplement

`packsrc.Store{Dir}` is already injected (three call sites all pass
`paths.PacksDir()`), and `packsrc.LockPath(userConfigPath)` already takes the config path as
an argument rather than calling `paths` — because the lockfile lives beside the user config
*on purpose*, packs being user scope. So this coupling is four constants passed at
construction. The only decision is whether the util gets its *own* default layout for
standalone use (`~/.local/share/agentpack/`) or reuses yolo's. It needs its own, or a
`agentpack install` run outside a jail would write into yolo's store and the two would
disagree about the lockfile.

---

## 5. What the util cannot own, no matter how it is built

These are not implementation obstacles; they are things whose *definition* lives in yolo.
§9.3 established most of this and it still holds. Restated because an extraction is exactly
when someone tries to move them.

- **The credential boundary is defined by yolo's mount table**, not by a policy a sidecar
  adopts. `MayGrantHostFiles()` is enforceable only because `LoadPacks` reads
  `paths.UserConfigPath()` *directly, while knowing* the workspace config is agent-writable.
  A util with no jail has no file to privilege.
- **The user-scope rule is inexpressible-not-forbidden**, and that is a property of yolo's
  config loader. The util can *validate* an entry; it cannot be the thing that decides
  workspace scope doesn't exist.
- **The origin gate needs a notion of who shipped the content.** `OriginEmbedded` means "in
  the yolo release." A standalone util has no release to be embedded in — everything is
  local or fetched. So the gate's *three tiers collapse to two* outside a jail, which is a
  real semantic difference and §6 has to answer it.
- **The disposability premise.** The whole threat model is "the container is blast-radius
  reduction, never authorization." The util inherits no blast radius.

---

## 6. Managing host configs — the half that is actually new

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

**Why this matters to the extraction rather than being a separate bug report.** The root
cause is that "which home am I composing into?" is an *implicit* `paths.Home()` host-side
and an *explicit* `e.Home` in-jail. The entrypoint got this right on purpose
(`env.go:76`, "deliberately not the process `$HOME`"); the host CLI never had to decide,
because it was only ever meant to *preview*. A host target forces the parameter to be explicit
everywhere — which is precisely the fix. So the destructive host-side writes are not an
argument against pointing this at the host; they are an argument that we already did, by
omission, and should do it on purpose.

(`configls.go` already knows the shape of the problem — `surfacesAreLocal()` at
`configls.go:341` exists so `ls` won't *claim* a host file is a jail surface. The knowledge
is there; `reset` and `capture` just don't consult it.)

### 6.2 The target: making the home an explicit, typed parameter

Today's implicit answer becomes one value. Call it a **target**:

```go
type Target struct {
    Home       string            // where surfaces are written
    Workspace  string            // ${workspace} substitution + sidecar root
    SidecarDir string            // capture/last_render/managed sidecars
    HostLayer  HostLayerSource   // where the `host` layer comes from — see below
    Tables     map[string]map[string]any  // the computed layer's sources
    Hooks      map[string]HookFunc        // §4.2
    Posture    Posture           // §6.4
}
```

Three targets exist, and the third is the new one:

| Target | Home | `host` layer | Output | Blast radius |
|---|---|---|---|---|
| **jail** | `/home/agent` (`$JAIL_HOME`) | `/ctx/host-<pack>/…` (`:ro` mount) | jail overlay | disposable |
| **preview** | any (a temp dir) | the real host file, read-only | stdout | none |
| **host** | the human's real `$HOME` | **the output file itself** | the human's real dotfiles | **their live environment** |

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
3. **Read-modify-write with a memory of what you asserted** — which yolo *already built*, as
   `mode: rmw` + the `reconcile` sidecar (`reconcileRMWTables`, `prism.go:404`;
   `rmwManagedPath`, `:462`). The sidecar records what yolo put there so a removal is
   distinguishable from a user's own entry, and a missing sidecar removes *nothing* — "a
   stale entry is a wrong config the user can see and fix, while deleting an agent's own MCP
   server is data loss they cannot" (`prism.go:402-403`).

**(3) is the answer, and it is already the shipped design for exactly this problem.**
`claude/config` (`~/.claude.json`, agent-owned, yolo asserts keys into it) is a host-target
surface in every respect except which home it lives in. So the host target is not a new
engine mode; **it is the existing `rmw` mode applied to every surface** rather than to the
two that needed it in a jail.

That has a crisp consequence worth stating as a rule:

> **On a host target, every surface is `rmw`.** `stateful`/`capture` is meaningless there —
> capture exists to distinguish *the agent's* in-jail edits from yolo's render, and on a
> host target those are the same person. `computed`-mode overwrite-every-boot is
> unacceptable there for the same reason probe 2 is a bug.

### 6.4 What else changes on a host target

- **`${workspace}` has no referent.** claude's `projects["${workspace}"]` is a per-jail
  assertion. A host target must either refuse a surface that uses the placeholder or bind it
  to the cwd, and refusing is the honest option.
- **`hostFiles` is meaningless and must be refused.** It names a host file to mount into a
  jail. On a host target the source and the destination are the same filesystem. Honoring it
  would be a copy the user did not ask for.
- **`mounts` becomes a copy.** No mount namespace. Both non-podman backends already
  materialize rather than bind (`macosuser.MaterializeDarwin`, and the `n()` copy helper the
  container backend uses at `assemble.go`), so the mechanism exists — but `:ro` protection
  does not, so a pack's skills tree on a host target is *writable*, and yolo's precedence
  guarantee (built-in < pack < user) becomes advisory.
- **The origin gate loses its top tier** (§5): nothing is "embedded in the release". So the
  rule becomes local-may / fetched-may-not, and `install.installerUrl` — a curl-piped shell
  script — is now running against the human's real home rather than a disposable one.
  **This is the single sharpest thing in the whole proposal.** A host target should refuse
  `install` entirely, at least at first: the util manages *config*, and installing tools
  into a real machine is a different product.
- **Hooks: two of three must be off.** `shared_credentials` symlinks a credentials file into
  a machine-global dir — on a host target the credentials file *is* already machine-global,
  so the hook is a no-op at best and a broken symlink at worst. `per_jail_history` keys on
  `YOLO_HOST_DIR` and has no meaning. `claude_plugins` runs `claude` against the user's real
  config. The `Hooks` map in §6.2 makes this a data decision instead of a code branch.

### 6.5 The posture, stated as a table

Since §6.3 collapses the four modes to one on a host target, what remains is *how much the
util is allowed to do*:

| Posture | Reads | Writes | Use |
|---|---|---|---|
| `observe` | host files, sidecars | nothing | `agentpack ls`/`diff` — what would change |
| `assert` | host files, sidecars | only keys the pack declares `managed`, recorded in a sidecar | the real product: keep your MCP servers in sync across five agents |
| `own` | host files | the whole file, backing up first | opt-in per surface, for someone who wants home-manager semantics |

`observe` is the default and `own` needs a per-surface opt-in. Note what this makes
possible that nothing currently does: **`assert` across every agent from one declaration**.
That is the actual unmet need — you have five agents that each want the same MCP server in
a different dialect, `computed` + `project` already expresses the reshape as data
(`agentcfg/project`), and today that machinery only ever runs inside a jail.

---

## 7. Three walkthroughs

### 7.1 yolo launches a jail (nothing observable changes)

```
yolo -- claude
  config.LoadPacks(paths.UserConfigPath())                 # yolo: user-scope rule
  agentpack/packload.MaterializeEmbedded(packs.FS, scratch) # util, yolo's corpus
  agentpack/packstage.Stage(...)                           # util
  <clear _official; copy selected>                         # yolo: the mount IS the filter
  -v .../packs:/ctx/packs:ro  -e YOLO_PACK_ROOT=/ctx/packs # yolo: argv
  ─── container ───
  agentpack/packload.LoadDir(...)                          # util
  agentpack/compose.Render(Target{jail}, surfaces, tables) # util, was entrypoint/prism.go
  yolo's hook table                                        # yolo: the three implementations
```

The only user-visible difference is that `yolo pack lint` and `agentpack lint` are the same
code, so they cannot disagree.

### 7.2 The human manages their own machine (the host target)

```
$ cat ~/.config/agentpack/config.jsonc
{ "packs": ["file:///home/me/code/house-rules"],
  "target": { "posture": "assert" },
  "mcp_servers": { "sequential-thinking": { "command": "npx", "args": ["-y", "@mcp/seq"] } } }

$ agentpack ls
TARGET  host (/home/me)   posture: assert
SURFACE          PATH                              MODE  ASSERTS
claude/settings  ~/.claude/settings.json           rmw   permissions, preferences
codex/config     ~/.codex/config.toml              rmw   mcp_servers.sequential-thinking
copilot/mcp      ~/.copilot/mcp-config.json        rmw   mcpServers.sequential-thinking
mise/config      ~/.config/mise/config.toml        —     refused: no host-target layer
claude/config    ~/.claude.json                    rmw   projects — refused: ${workspace}

$ agentpack diff                 # observe, before touching anything
$ agentpack apply                # assert: writes only the declared keys, records a sidecar
$ agentpack apply --revert       # removes exactly what the sidecar says it added
```

Two things to notice. `mise/config` is *refused* rather than truncated — that is probe 2
turned into a designed outcome. And `--revert` is meaningful *only* because the sidecar
mechanism already exists; without it, "undo" is indistinguishable from "delete the user's
keys."

### 7.3 One pack, both targets

`house-rules` has `skills/`, an `AGENTS.md`, and a `pack.json` naming a `computed` MCP
projection. In a jail it arrives as `:ro` mounts and composed surfaces. On the host it
arrives as a copied skills dir and asserted keys. **Same manifest, no target-specific
fields** — because the manifest only ever declared paths and reshapes, which was the whole
point of "core does not know what an agent is." A manifest that needs to know which target
it is on is the signal that this design went wrong.

---

## 8. What I would actually do, in order

**Ordered so that each step is independently valuable and the risky part comes last.**

1. **Fix probes 1–3 in yolo, now, independent of any extraction.** Host-side `reset`
   (`configReset`, `configdiff.go:220`) and `capture` (`configCapture`, `:420`) must refuse
   (or require `--force`) when `surfacesAreLocal()` is false — the predicate already exists
   at `configls.go:341` and is currently consulted only by `composedFileExists` (`:330`).
   This is a live data-loss path on the maintainer's own machine and it should not wait for
   an architecture decision. It is also the cheapest possible down-payment on §6.2: it makes
   the implicit target explicit at the one place it currently misfires.
2. **Split `builtin.go` into its own in-repo package.** §9.3 identified this as strictly
   smaller than extraction and as extraction's only *named* beneficiary. Doing it first
   means step 4 is a move, not a redesign — and if we stop after this, we still deleted a
   duplicate table.
3. **Introduce `Target` inside yolo**, replacing `*entrypoint.Env` in the render path and
   `paths.Home()` in the host path. No new module. **This is the step that delivers §2's
   motivating case** — a host target is what lets your `pi` pack fix the approval prompts on
   the host — so it is also where the real design risk lives, and it is cheaper to get wrong
   in one repo than across two.
4. **Extract the module** (option D), packs staying in yolo (§4.1). Mechanical once step 3
   lands. Verify per the nested-jail rule — a pack-staging change is exactly the class of
   thing unit tests miss.
5. **Then, and only then, build the host target** — `observe` first, `assert` second, `own`
   maybe never. With `install` and `hostFiles` refused outright.

**If only one of these ever happens, it should be #1.**

---

## 9. Open questions — the discussion part

**9.1 Does the host case need a separate repo, or just a separate target?** §2 settles that
the *capability* is wanted (one declaration, both environments). What it does not settle is
whether that requires extraction at all: a `Target` with `Home = $HOME` inside today's
`yolo` binary would deliver the `pi`-on-the-host case with no new module, no version skew,
and no §9.5 schema problem. The case for a real module is that a tool which edits your live
dotfiles should not be the tool whose other job is launching containers — a boundary
argument, not a capability one. **This is the actual open decision**, and §8 sequences the
work so it can be deferred: steps 1–3 deliver the capability, step 4 decides the repo.

**9.2 Does a host target defeat the sandbox's purpose, or is it a separate product?** The
threat model is "the container is blast-radius reduction, never authorization." A util that
writes the human's real dotfiles has no blast radius — which is fine *if it is understood
as a different tool*, and corrosive if it becomes the recommended way to configure agents
because it is more convenient. Refusing `install` (§6.4) is the load-bearing mitigation, and
I would want it stated as an invariant rather than a default.

**9.3 Do the reservation lists survive contact with a lazy pack set?** §4.1's answer keeps
them init-time by keeping the corpus in yolo. But if a *configured* (non-embedded) pack ever
needs to participate in a reservation — and `hostfiles.go:704-710` already documents that as
a known gap — then the lists become fallible, and the failure direction is permissive. I do
not think the extraction should try to fix this, but it should not make it worse.

**9.4 What does `install` mean on a host target, if not "never"?** "Never" is my
recommendation and it is also a real limitation: the most useful thing a pack does for a new
machine is *install the agent*. If the answer is eventually "yes, with an explicit
per-invocation grant", that grant is a new security surface and needs its own design.

**9.5 Does the pack format need a version field?** `packdecl.Manifest` has none, and
`Decode` uses `DisallowUnknownFields` (`packdecl.go:170`) — so a pack written for a newer
util is a hard error in an older one, with a decent message. Inside one repo that is
correct. Across two repos with independent release cadences, a pack author now has a
compatibility matrix and no way to express "needs agentpack ≥ N". This is the one schema
change extraction actually forces.

**9.6 Who owns `jsonx`/`tomlx`?** Twenty-two yolo packages import `jsonx`. If it moves into
the util, yolo imports the util for its JSON helpers, which is backwards. If it is
duplicated, the ordered-map semantics can drift and RFC-7386 tombstone handling is exactly
where a subtle divergence would hide. Third module? That is three repos for 958 lines.

**9.7 macos-user is the existing host-shaped target, and it currently gets no packs at
all.** `RunDarwinBootstrap` calls `LoadJailPacks` → `ConfigurePackSurfaces` →
`RunPackHooks` (`darwin.go:57-62`), but the macos-user run path returns at `run.go:57`
*before* `stagePacks`, and `YOLO_PACK_ROOT` is never set on that backend — verified, zero
occurrences outside the container path. So on macos-user the pack loop runs over an empty
list every launch, silently. That backend is the closest thing to a host target we already
ship (a real macOS home, no container, composed by the same writers), which makes it either
the natural first implementation of §6 or a gap to close first. Either way it should not
stay a silent no-op.

---

## 10. The case against, stated fairly

- **The capability does not require the extraction.** §2's `pi`-on-the-host case is delivered
  by an explicit render target, which is step 3 and lives entirely inside today's `yolo`.
  Everything after that buys a boundary, not a feature.
- **Two repos cost more than the duplication they remove.** Version skew, a compatibility
  matrix for pack authors (§9.5), and a second place to look when a pack misbehaves.
- **The strongest argument for extraction — deleting the duplicate surface-path table — was
  already achieved without it.** `builtinSurfacePaths()` now reads the real declarations
  through `packload` (`hostfiles.go:709-712`); the hand-maintained list and its drift test
  are gone.
- **A host target is a genuinely new risk surface** and the sandbox is the product. Pointing
  the same pipeline — including pack-supplied `installerUrl` — at a human's live environment
  is a different posture than everything else here, and §6.4's refusals are what stand
  between the two.
- **And the most uncomfortable one:** §6.1's defects are fixable in about twenty lines
  inside yolo, today. If the real motivation for extraction is "host-side composition is
  currently wrong", extraction is a very large answer to a small question.

The counter, and it is the reason I would still do steps 1–3: the *reason* those defects
exist is that the target was never a parameter. That is an architectural fact, it will keep
producing bugs of this shape, and making it explicit is worth doing whether or not anything
ever moves to another repo.
