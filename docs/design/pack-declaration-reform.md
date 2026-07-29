# Reforming the pack declaration: from a JSON field zoo to contributions with declared effects

**Status:** design, exploratory, 2026-07-28. Written in response to: *"I'm worried we're
complecting a bunch of stuff due to how we're defining packs as JSON files. Can we instead involve
Lua or some other interface, a build step, something? Less special-casing, more flexibility and
predictability built in. We're hiding a lot of dirty secrets by how many different JSON keys there
are. We need to be specific, yet still flexible, about how we impact the environment around us
with each bit of a pack, and how to be a good citizen."*

**Scope:** the *shape* of a pack declaration and what it is allowed to say. Not the fetch/lock/
origin machinery, which is settled and unaffected. No migration path — destination first.

**The conclusion up front, because it is not the obvious one.** The problem is not JSON, and
replacing JSON with Lua would make things worse in the specific way that matters. The problem is
that **the manifest names 59 fields and none of them names its effect on the environment.** Lua
belongs in this design — it is already here, sandboxed and vendored — but as the *escape hatch for
one narrow slot*, not as the manifest format. What actually fixes the complecting is a **uniform
contribution shape with a declared, checkable footprint.**

**Reads with:** [pack-specification-and-loading.md](pack-specification-and-loading.md) (the pack
system as built — this doc proposes changing its schema, not its loader),
[third-party-pack-logic.md](third-party-pack-logic.md) (whose §2.1/§2.2 two-tier answer this doc
adopts and narrows), [happy-path-principle.md](happy-path-principle.md) (the constraint on how
many knobs any of this may add), [three-decisions.md](three-decisions.md) (where the projection op
set came from).

---

## 1. The measurement: what is actually wrong

### 1.1 Fifty-nine keys across four packages

| Package | File | JSON keys |
|---|---|---|
| `packdecl` | `packdecl.go` | 25 |
| `agentcfg/manifest` | `load.go` (Decl) | 10 |
| `agentcfg/manifest` | `computed.go` (Computed + Flag) | 11 |
| `agentcfg/project` | `project.go` (Op + 3 op types) | 13 |
| | **total** | **59** |

`packs/claude/pack.json` is **144 lines** and uses about half of them. Two of the four packages are
*conditional expression languages* wearing JSON: `Computed` has `from`/`to`/`project`/`omitEmpty`/
`flags`/`reconcile`/`tombstone` where five of the seven are mutually-constraining modes, and
`Flag` has `whenPresent`/`whenAny` — a two-branch predicate DSL. `Computed.Validate()` is 53 lines
of "this field is invalid in combination with that one."

That is the "dirty secrets" observation, precisely located. **These keys are not describing a
pack; they are encoding a computation whose shape JSON cannot express, so the shape leaked into
key names and cross-field validation rules.**

### 1.2 The real complecting is not the key count — it is magic strings

Two lines of Go decide what a `mounts` entry *means*:

```go
// internal/cli/run/prepare.go:279
func isBriefingMount(from string) bool {
	return from == "AGENTS.md" || from == "CLAUDE.md"
}

// internal/cli/run/prepare.go:296
if mt.From != "skills" {
	continue
}
```

A pack writes `{"from": "skills", "to": ".claude/skills"}` and gets **merge semantics**
(built-in < pack < user, three trees layered). It writes `{"from": "AGENTS.md", "to":
".claude/CLAUDE.md"}` and gets **concatenation into a per-pack staged file**. It writes
`{"from": "prompts", "to": ".claude/prompts"}` and gets **a read-only bind mount, no merging,
last-writer-shadows**.

Same key. Same two subkeys. Three completely different effects, selected by a filename. A pack
author cannot know this from the schema, `yolo pack lint` cannot tell them, and the docstring's own
defense — *"keyed on the source NAME because that is what a pack author writes"* — is an admission
that the *type of contribution* has no field of its own.

**This is the complecting.** `mounts` conflates three contribution kinds. And it is not alone:

| Field | Actually says | Also silently decides |
|---|---|---|
| `mounts` | stage this path there | whether you get merge, concat, or shadow (by filename) |
| `writableDirs` | I need to write here | per-workspace state isolation |
| `sharedDirs` | I need to write here | **cross-workspace credential leakage, by design** |
| `hostFiles` | read my user's file | which side of the credential boundary you are on |
| `surfaces[].mode` | how to write | whether in-jail edits are captured, discarded, or preserved |
| `install.installerUrl` | I need this program | curl-to-shell at boot |

Six fields, six unrelated axes of environmental impact, no consistent way to ask "what does this
pack do to my machine?"

### 1.3 The hook set is closed, and the code says why that hurts

`internal/entrypoint/packhooks.go:23` states its own limitation:

> The honest limitation: the hook set is CLOSED (`packdecl.KnownHooks`), so a third-party pack
> needing a genuinely new side effect cannot ship one. That is the accepted cost of not executing
> pack code at boot.

Three hooks exist: `shared_credentials`, `per_jail_history`, `claude_plugins`. The third is
**named after a tool** and shells out to `claude plugins install`, which the file admits is
"a deliberate admission rather than an oversight." So the claim "core does not know what an agent
is" holds everywhere except the one file where it doesn't.

The closed set is the direct cause of the inflexibility. Every future imperative need is a yolo
release.

### 1.4 Third-party packs reserve nothing — the "good citizen" hole

`internal/packload/embedded.go:19` again says it plainly:

> Embedded only, which bounds the guarantee honestly: a configured pack's writable dir is not
> reserved, so a user who declares a `host_files` entry at that path gets a conflict rather than a
> clear error.

Conflict detection today is **within one pack** (`HostFileConflicts`, `packload.go:99`) and
**union-with-silent-dedup across packs** (`union()`, `packload.go:265`). Two packs that both
declare `writableDirs: [".config/foo"]` are silently merged. Two packs mounting different content
at the same `to` — one shadows the other, and `assemble.go:398` just appends both `-v` flags.

So: the citizenship problem is not that packs behave badly. It is that **nothing computes the
union of what packs claim and checks it for collisions**, because there is no single vocabulary in
which "what I claim" is expressible.

### 1.5 Lua is already here, and already load-bearing

Worth stating before proposing more of it: `internal/agentcfg/luahook` is **963 non-test lines** of
a working, vendored (`gopher-lua`, pure Go, cgo-free), *sandboxed* Lua 5.1 VM. The sandbox
(`sandbox.go`) strips `os`/`io`/`require`/`package`/`load`/`loadstring`/`loadfile`/`dofile`/
`dostring`/`collectgarbage`, allows only `string`/`table`/`math` plus a dozen pure builtins, and
contracts the transform to be a **pure function of its inputs** — no clock, no randomness, no I/O
— because the §5 overlay diff breaks otherwise.

It is wired: `Surface.Transform` names a per-surface hook, `entrypoint/prism.go:96` loads it, and a
**named-but-unreadable hook is a hard error, not a skip** (`prism.go:88`) — the fail-closed
discipline this reform should copy everywhere.

**So the question is not "can we have Lua." It is "what should Lua's job be."**

---

## 2. Why Lua-as-the-manifest is the wrong answer

The tempting version: `pack.lua` returns a table, or calls registration functions. It fixes the
key-zoo aesthetically and loses three things the current design has:

1. **Static analyzability.** `yolo pack lint` reads `pack.json` on the host *without executing
   anything* and reports every problem at once. A Lua manifest must be *run* to be known, and a
   manifest that computes its own fields cannot be diffed, hashed, or reviewed. This directly
   contradicts the sealing work in
   [environment-manager-user-stories.md](environment-manager-user-stories.md): a definition that
   binds has to be *readable* before it is realized.

2. **The origin gate.** `packdecl.NeedsHostAccess()` (`packdecl.go:276`) is a *static* predicate
   over the declaration — it decides whether a fetched pack may read the host home. If the
   manifest is code, "does this pack ask for a host file" becomes undecidable in general, and the
   credential boundary degrades from a check to a hope. `packhooks.go:14` already refuses pack-
   supplied boot code for exactly this reason: *"shipping content and executing code would be one
   grant."*

3. **Predictability**, which is the thing actually asked for. A general-purpose language in the
   *declaration* slot means two packs can express the same intent unrecognizably differently, and
   the union-of-claims check from §1.4 becomes impossible.

**The distinction that resolves it:** Lua is good at *computing values*, terrible at *declaring
effects*. The manifest is a declaration of effects. Keep it data. Put Lua where a value needs
computing — which is exactly the `Computed`/`project` DSL, the part that is a language pretending
not to be one.

**A build step is likewise the wrong lever here.** `packs/*/pack.json` files are already generated-
looking (alphabetized keys), and a build step that emits JSON from something nicer changes the
authoring ergonomics without touching any of §1.2–§1.4. The complecting is semantic, not
syntactic.

---

## 3. The reform: contributions, each with a declared footprint

### 3.1 One shape, repeated

The `kind` set is **closed and core-owned** — a pack *selects* from it, it does not *define* new
kinds. This is the same shape as every other extensibility point in the manifest today:
`knownModes` (`manifest/load.go:42`), `knownComputedSources` (`computed.go:47`), `knownCodecs`
(`manifest.go:225`), and `packdecl.KnownHooks` are all closed lists validated on load, and a value
outside the set is a loud error rather than a silent no-op. Kinds join that family. The reason is
the same one those give: an unknown kind is a typo that would otherwise render nothing, and — more
importantly here — **core has to know what each kind's environmental footprint is** (§3.2) to
check it, so a pack cannot invent a kind whose footprint core cannot reason about. A pack that
needs a genuinely new *effect* reaches for the escape hatches in §3.4 (a `derive` function, or a
subprocess projector), not a new kind. So "where are these kinds defined?" — in core, one place
(a `knownKinds`-style registry beside the footprint table), never in a pack.

Replace nine top-level effect fields with **one list of contributions**, each with an explicit
`kind`:

```jsonc
{
  "name": "claude",
  "description": "Claude Code, with yolo's approval posture",
  "contributes": [
    { "kind": "program",   "bin": "claude", "via": "installer",
      "url": "https://claude.ai/install.sh" },
    { "kind": "skills",    "into": "~/.claude/skills", "from": "skills" },
    { "kind": "briefing",  "into": "~/.claude/CLAUDE.md", "from": "AGENTS.md",
      "after": "host:~/.claude/CLAUDE.md" },
    { "kind": "files",     "into": "~/.claude/prompts", "from": "prompts" },
    { "kind": "config",    "into": "~/.claude/settings.json", "codec": "json",
      "write": "compose", "managed": { }, "derive": "derive.lua" },
    { "kind": "state",     "at": "~/.claude", "scope": "workspace" },
    { "kind": "state",     "at": "~/.claude-shared-credentials", "scope": "machine",
      "because": "one login must serve every workspace" },
    { "kind": "reads-host","from": "~/.claude/settings.json", "for": "config:settings" },
    { "kind": "launch",    "bin": "claude", "flags": ["--dangerously-skip-permissions"],
      "aliases": { "--dangerously-skip-permissions": ["--yolo"] } }
  ]
}
```

Three things changed, and each one kills a specific defect from §1:

- **`skills` / `briefing` / `files` are now distinct kinds** rather than three meanings of
  `mounts` selected by filename (§1.2). The merge semantics belong to the *kind*. A pack that
  wants a skills tree at a nonstandard path says `kind: skills`, and gets merging. A pack that
  wants an opaque tree says `kind: files`, and gets a bind mount. `isBriefingMount` is deleted, not
  relocated.
- **`state` replaces `writableDirs` + `sharedDirs`**, with the leak-by-design case demoted to a
  `scope` value that requires a `because` string. Today a pack silently opts into cross-workspace
  credential leakage by choosing one array over another (§1.2); here the sharp choice has a name,
  a required justification, and one place to grep.
- **`reads-host` replaces `hostFiles` and `mounts[].hostOverlay`**, unifying the two things
  `NeedsHostAccess()` has to keep in sync by hand. The origin gate becomes "does any contribution
  have kind `reads-host`" — one predicate over one kind, which is the shape that stops the
  two-of-three mistake `packdecl.go:271` says already happened once.

### 3.2 Every kind declares its footprint, and the footprint is checked

The point of a uniform shape is that a **footprint** becomes computable. Each kind maps to a set of
claims on the environment:

| Kind | Claims | Conflict rule |
|---|---|---|
| `program` | a name on `PATH`, a launcher in `~/.yolo-shims/` | two packs claiming one bin → **error** |
| `skills` | a merge target dir, in the canonical skills format (§3.2a) | two packs, one dir → **fine** (ordered merge) *if same format*; a format gap needs a reshape or is refused |
| `briefing` | a concat slot at a path | two packs, one path → **fine** (ordered concat) |
| `files` | exclusive ownership of a path | two packs, one path → **error** (one would shadow) |
| `config` | a surface identity + a file | two packs, one surface → **error** unless one declares `overrides` |
| `state` | a home subtree, at a scope | overlapping subtrees at *different* scopes → **error** |
| `reads-host` | a host path, read-only | two packs may read the same file → **fine** |
| `launch` | flags for a bin | two packs, one bin → **error** |
| `hook` | a named imperative capability | per-hook |

`yolo pack lint` and `yolo check` both compute the union across selected packs and apply this
table. That is the **good-citizen mechanism**, and it is only possible because footprints are
expressed in one vocabulary. Today the same information is spread across `HostFileConflicts` (one
pack, one kind), `union()` (silent dedup), and nothing at all for `mounts`.

Read this table as one rule, not nine: **every file has exactly one writer.** The `→ error` rows
are files a pack owns outright (two claimants is a collision); the `→ fine` rows are files no pack
writes — a neutral core owner combines the inputs and writes them (ordered merge for `skills`,
concat for `briefing`, compose for `config`). §3.6 is that rule stated directly, and it is the
shape the whole reform is really pointing at.

A new verb falls out for free:

```
$ yolo pack explain claude --footprint
program    claude                        → ~/.yolo-shims/claude, PATH
skills     ~/.claude/skills              MERGED (built-in < claude < user)
briefing   ~/.claude/CLAUDE.md           CONCAT after your own file
config     ~/.claude/settings.json       composed, 4 layers, captures in-jail edits
config     ~/.claude.json                read-modify-write, agent-owned, preserves
state      ~/.claude                     per-workspace
state      ~/.claude-shared-credentials  MACHINE-WIDE — leaks across workspaces
                                         "one login must serve every workspace"
reads-host ~/.claude/settings.json       read-only, granted (embedded pack)
launch     claude --dangerously-skip-permissions

3 claims need review: 1 machine-wide state, 1 host read, 1 installer URL.
```

That output is the answer to "how do we impact the environment around us." It does not exist today
and cannot be built today, because there is no field that means "impact."

### 3.2a When two agents disagree on a format: the `skills` case, made honest

A fair question the `skills` row hides: **what if two packs' agents read skills in different
on-disk formats?** Say claude reads `SKILL.md` with YAML frontmatter and some future agent reads
`skill.toml`. "Two packs, one dir → ordered merge" only works if the *files being merged are in a
format both readers accept*. Today they are, but by assumption, not by design.

Here is the true picture in the shipped code. `PrepareSkills` (`internal/agents/skills.go`) stages
a **separate dir per agent** (`~/.claude/skills`, `~/.copilot/skills`, `~/.pi/agent/skills`, …) and
copies the *same* three-layer stack into each — built-in `SKILL.md` suite < pack skills < the
user's own tree. So skills are already per-destination, and the "merge" is a merge of trees that
all happen to be `SKILL.md`. There is **no reshape step** — unlike `config`, which has `derive`
precisely because agents consume config in incompatible shapes.

So the reform has to make the latent assumption explicit, and it splits by *whose* skills:

- **A pack's own agent + its own skills** never conflict on format — the pack ships the skills its
  agent reads, into its agent's dir. That is the common case and it stays a plain tree copy.
- **Shared/house-rules skills** (the cross-agent corpus a user wants everywhere) are the only place
  a format gap can bite, because one tree lands in every agent's dir. Two honest options, and the
  doc should pick one rather than pretend the gap away:
  1. **Declare a canonical skills format** (`SKILL.md` + frontmatter, which is already the de-facto
     standard) and make an agent whose reader differs supply a `skills`-kind **reshape** — the
     exact `derive` seam from §3.3, but emitting a file tree instead of a config value. This is the
     general answer and it is why `derive` is a *slot*, not a config-only feature.
  2. **Keep skills format-agnostic and per-agent** (today's behavior): a shared skill is only
     merged into an agent's dir when the pack that owns that agent opts in, and a format mismatch
     is simply not offered rather than mis-rendered. Simpler, ships now, punts the reshape until a
     second real format exists.

_Leaning: (2) now, (1) when a second skills format actually appears_ — the same "don't build the
general mechanism before the second case" rule §3.4 applies to hooks. The point for this doc is
that a `kind` carries not just a footprint but a **format contract**, and `skills` is where that
contract is currently implicit. Naming it is the reform; choosing the reshape is deferrable.

### 3.3 Lua takes exactly one slot: `derive`

Delete `computed[]`, `Flag`, and `project.Op` — **24 of the 59 JSON keys, 41%**, and both
conditional DSLs — and replace them with a Lua function per surface:

```lua
-- packs/claude/derive.lua
return function(ctx)
  local out = {}

  -- was: computed[].tombstone
  out.mcpServers = ctx.tombstone

  -- was: computed[].flags[].whenPresent  (three entries)
  local plugin = { python = "pyright-lsp@claude-plugins-official",
                   typescript = "typescript-lsp@claude-plugins-official",
                   go = "gopls-lsp@claude-plugins-official" }
  out.enabledPlugins = {}
  for lang, id in pairs(plugin) do
    out.enabledPlugins[id] = ctx.lsp_servers[lang] and true or ctx.tombstone
  end

  -- was: computed[].flags[].whenAny
  out.env = { ENABLE_LSP_TOOL = next(ctx.lsp_servers) and "1" or ctx.tombstone }

  return out
end
```

**Why this is the right slot and the manifest is not.** A `derive` function is a **pure function
from live tables to a config value** — which is precisely the contract `luahook`'s sandbox already
enforces and tests (`vm_test.go` proves forbidden globals absent, fail-closed on error and timeout,
`ctx.managed` read-only, nested round-trip). It computes a *value*; it declares no *effect*. The
footprint of a `config` contribution is its path, codec, and write mode — all still static, all
still lintable, all still hashable — regardless of what `derive` computes.

What this buys beyond deleting keys:

- **`omitEmpty` vs `tombstone` stops being a schema puzzle.** `computed.go:66-91` spends 25 lines
  of doc comment explaining that omitting a key leaves a host block intact while a tombstone
  removes it, and that conflating them is "the mistake this pair exists to prevent." In Lua,
  `out.x = nil` and `out.x = ctx.tombstone` are visibly different lines. The distinction moves
  from prose-you-must-read into syntax-you-can-see.
- **`Computed.Validate()`'s 53 lines of cross-field rules evaporate**, along with the class of bug
  where a valid-looking combination silently yields an empty layer.
- **`project.Op`'s closed op set stops being a ceiling.** `project.go:25` says *"a projection that
  needs more than this is the signal to reach for the subprocess projector"* — i.e. the op set was
  always known to be insufficient in general. A sandboxed Lua function is strictly more capable
  than eight ops and strictly less dangerous than a subprocess (no fs, no net, no clock).
- **`sources` stays closed and that is correct.** `ctx` exposes `mcp_servers` and `lsp_servers`
  because core knows what those are; an unknown source name should still be a load error, for the
  reason `computed.go:37` gives — a typo would otherwise read as "my MCP servers stopped working"
  with nothing to grep.

### 3.4 Hooks: open the set, but only through the same sandbox

§1.3's closed hook set is the remaining inflexibility, and the reason it is closed is sound: a
pack that runs arbitrary code at boot collapses the origin gate. But *arbitrary* is doing the work
in that sentence. Three tiers, ordered by what they can touch:

| Tier | Mechanism | Can touch | Who may ship one |
|---|---|---|---|
| **1. named hook** | core implements, pack requests by name | whatever core's implementation does | any pack |
| **2. `derive` / effect Lua** | sandboxed VM, declared targets only | only the paths its contribution already claims | any pack |
| **3. projector** | subprocess over the frozen stdin/stdout protocol (`third-party-pack-logic.md` §2.1) | only the JSON handed to it | pack whose origin permits, with re-approval |

Tier 2 is new and is the interesting one. Two of the three existing hooks are *path manipulations
inside a subtree the pack already declared*: `shared_credentials` symlinks `~/.claude/.credentials.json`
into a `sharedDirs` entry the pack declared; `per_jail_history` repoints `~/.claude/history.jsonl`.
Both are expressible as a Lua function whose filesystem verbs are **restricted to the pack's own
declared `state` claims** — a capability-by-declaration model, where the sandbox's allowed operations
are computed from the manifest rather than fixed in Go.

That is what "flexible yet specific" looks like mechanically: **a pack may run code, but only
against the footprint it declared and lint approved.** A pack that wants to symlink outside its
claims does not get a runtime error — it fails `yolo pack lint`, on the host, before anything runs.

`claude_plugins` stays tier 1, and stays honest: it shells out to a specific binary with a specific
plugin-id mapping. Tier 2 has no subprocess, deliberately. When a second tool wants the same thing,
`packhooks.go:24` already says the right rule — the shape they share becomes the thing worth
naming.

### 3.5 What the numbers look like after

| | Now | Proposed |
|---|---|---|
| Effect-declaring top-level fields | 9 (`install`, `mounts`, `writableDirs`, `sharedDirs`, `hostFiles`, `surfaces`, `launchFlags`, `flagAliases`, `hooks`, `retireMiseTools`) | 1 (`contributes`) |
| Contribution kinds | implicit, filename-selected | 9, named |
| JSON keys total | 59 | ~35 — **not the win; see below** |
| Value-computation keys | 24 (`computed` + `Flag` + `project`) | 0 (one Lua file) |
| Magic-string dispatch sites | 2 (`isBriefingMount`, `from != "skills"`) | 0 |
| Cross-pack conflict checks | 1 kind, 1 pack at a time | 9 kinds, union across selection |
| Imperative extensibility | closed set of 3 | 3 tiers, 2 open |

**Be honest about that first row.** Consolidating nine effect fields into one `contributes` list
does not shrink the schema much — the kinds need their own keys (`kind`, `into`, `from`, `at`,
`scope`, `because`), so the total lands around 35 and the *raw key count* is a weak argument.
Two rows below it are the actual case: **both conditional DSLs are gone**, and **magic-string
dispatch is gone**. A schema with 35 honest keys and no hidden semantics is not the same object as
one with 59 where a filename picks your merge strategy.

### 3.6 The deeper shape: one file, one writer — and a neutral owner for the rest

Everything above is really circling one rule, so state it directly:

> **Every file on disk has exactly one writer. A pack either OWNS a file outright, or it does not
> write that file at all — it contributes typed INPUTS to a neutral owner that writes it.**

Two ownership modes, and the conflict table in §3.2 is just this rule applied per kind:

- **Sole ownership.** A pack declares the files it owns; a file may be owned by **at most one
  pack**. `files`, `program`, a pack's own `skills`/`briefing` tree — these the pack writes
  directly, and two packs claiming one path is an **error**, caught at `yolo pack lint` before
  anything runs. This is the part the reviewer already affirmed: declare ownership, one owner
  per file.

- **Shared production via a neutral owner.** The moment two packs need to affect *one* file, no
  pack may write it. They emit **typed contributions** and a **core-owned assembler** consumes all
  of them and produces the file. This is the "scripting somewhere else" the design keeps reaching
  for — but located correctly: not in a pack (which would re-collapse the origin gate, §1.3), and
  not as a general language in the manifest (§2), but as a **core module that is the sole writer of
  any shared file.** `~/.claude/settings.json` is the worked example — `defaults`, `managed`, the
  `host` layer, and `derive` are four inputs from different sources, and the compose engine, not
  any of them, writes the bytes.

**This already exists, half-built, and the reform is mostly about generalizing it.** Two pieces of
core are already neutral owners:

1. **The compose engine** (`internal/agentcfg/compose.go`) takes layered inputs
   (`defaults`/`host`/`computed`/`overlay`/`managed`) and produces one file, deterministically,
   with a `last_render` sidecar recording exactly what it wrote (§5 of the composition design).
   That sidecar *is* provenance tracking — it already answers "who last wrote this key."
2. **The staging-then-layer-in pattern** the reviewer intuited already ships for skills and
   briefings: `PrepareSkills` builds a tree under `AgentsDir()/<cname>/` and the run assembler
   `:ro`-mounts it into the jail (`internal/agents/skills.go`, `cli/run/prepare.go`). Nothing is
   written to the live home directly; a neutral staging dir is assembled on the host and layered in
   read-only. That is exactly "write it to another directory that then gets layered in so we can
   make sure we aren't overwriting the same files and can track things."

So the target architecture is: **make that the universal write path, not a per-subsystem one.**

```
   packs' typed contributions                 core assembler                 the jail
  ┌────────────────────────────┐          (the ONLY writer)            ┌──────────────────┐
  │ claude:  config inputs ─────┼──┐                                   │                  │
  │ house-rules: config overlay─┼──┼──►  compose ──► staging/ ──:ro──► │ ~/.claude/       │
  │ claude:  skills tree ───────┼──┼──►  merge   ──► staging/ ──:ro──► │   settings.json  │
  │ house-rules: skills tree ───┼──┘                  │                │   skills/        │
  │ claude:  briefing prose ────┼─────►  concat  ─────┘  (collision-   │   CLAUDE.md      │
  │ house-rules: briefing ──────┼─────►             checked, tracked)  │                  │
  └────────────────────────────┘                                      └──────────────────┘
        declare inputs                  one module writes every file      never written in place
```

Every arrow into the assembler is a **typed contribution keyed by kind**; the assembler picks the
combine rule from the kind (compose / ordered-merge / concat / sole-copy) and writes into a staging
tree it wholly owns; the staging tree is layered in read-only. Three properties fall out, and they
are the three the reviewer asked for:

- **No file is ever written twice.** The assembler is the sole writer, so "two packs overwrote each
  other" is structurally impossible — it is a collision *detected among inputs*, not a race on disk.
- **Provenance is free.** Because one module writes everything from declared inputs, it can emit a
  manifest of "which contribution produced which file/key" — a generalization of the `last_render`
  sidecar to every kind, which is what makes `yolo pack explain --footprint` and the §3.2
  collision report exact rather than best-effort.
- **The staging layer is the safety boundary.** Assembling into a neutral dir and mounting it `:ro`
  means a bad contribution corrupts a throwaway staging tree, never the live home — the same reason
  the skills path already works this way.

**What is still open is the shared-input case beyond config.** Config already has its neutral owner
(compose + `derive`). Skills and briefings have a neutral *stager* but only a trivial combine
(concat / ordered-merge), because no second producer has needed a real reshape yet (§3.2a). The
honest statement: the sole-owner rule ships now (it is just the §3.2 conflict table), the neutral
assembler for config exists now, and the *general* neutral owner — one module, every kind, a
provenance manifest, one staging tree — is the direction this section commits to, with the reshape
seams (`derive`, and a subprocess projector for genuinely arbitrary combine logic, §3.4) as the
extension points where a shared file needs computed production rather than a fixed combine rule.

---

## 4. What this deliberately does not change

- **The manifest stays static data.** Every claim is readable without executing anything, which is
  what keeps `pack lint`, the origin gate, and `describe --hash` honest.
- **The origin gate keeps its current teeth.** `reads-host` and `program.via: installer` remain
  refused for fetched packs. Unifying two fields into one kind makes the gate *easier* to state,
  not weaker.
- **Packs stay user scope.** Nothing here loosens it; a footprint table makes it more load-bearing,
  since it is now possible to *print* what a workspace-scoped pack would have claimed.
- **The closed source list stays closed.** `ctx.mcp_servers` / `ctx.lsp_servers` are core concepts;
  Lua does not get to invent a table core cannot supply.
- **Determinism stays mandatory.** The `derive` contract is the existing sandbox contract: pure,
  no clock, no randomness. The overlay diff depends on it.

## 5. What this costs

- **It is a breaking rewrite of every pack manifest, including six shipped ones.** No migration
  path is in scope here, but this is the largest single cost and it is not small: `pack.json` is a
  documented, third-party-authored format.
- **Lua in `derive` moves a class of error from load time to render time.** Today a bad `computed`
  block fails `pack lint` on the host with a field-level message; a bad `derive.lua` fails when it
  runs. Mitigations exist (`ValidateSandbox` static lint, a `pack lint --derive` dry-run against
  empty tables) and none is as good as a schema check. **This is the reform's real trade**, and it
  is worth taking only because the schema it replaces has 60 lines of cross-field rules that were
  themselves a source of silent-empty-layer bugs.
- **Nine kinds is nine per-notch rulings**, in the sense
  [yolo-as-environment-manager.md](yolo-as-environment-manager.md) §8 warns about: each kind needs
  an answer for `jail` / `guest` / `host`. That is a matrix, but it is the matrix that already
  exists implicitly — §5 of that doc had to enumerate it by field anyway, and doing it by *kind* is
  strictly fewer cells.
- **Tier-2 capability-by-declaration is genuinely new security surface.** "Lua restricted to the
  pack's declared state claims" is a claim about a sandbox, and sandbox claims are where bugs are
  expensive. It should ship last, behind tiers 1 and 3, and only when a real second case for it
  exists.
- **`yolo pack explain --footprint` invites a false sense of completeness.** A footprint lists
  declared claims; a pack's *installed program* can do anything the jail permits. The output must
  say that, or it reads as a sandbox report.

---

## 6. The one-paragraph version

The manifest's 59 keys are not the disease; they are the rash. The disease is that a pack declares
*paths* and core infers *effects* — from filenames (`isBriefingMount`), from which array a path
appears in (`writableDirs` vs `sharedDirs`), from field combinations that a 60-line validator has
to police. Give every contribution a `kind`, make each kind's environmental footprint explicit and
checkable, and the special-casing has nowhere to hide: one vocabulary, one union, one conflict
table, one `--footprint` view. Underneath the vocabulary is a single rule (§3.6): **every file has
exactly one writer** — a pack owns a file outright, or it feeds typed inputs to a neutral core
owner that writes it, never both, never two packs on one path. Config already works this way (the
compose engine is that owner) and skills already stage-then-layer-in read-only; the reform makes
that the universal write path. Then put Lua in the single slot where a *value* genuinely needs
computing — deleting both conditional DSLs, 24 keys' worth — and keep it out of the slot
where *effects* are declared, because that slot's whole job is to be readable before it runs.

---

## Open Questions

1. **Whether `derive` Lua is per-surface or per-pack.**
   `Surface.Transform` is per-surface today and `entrypoint/prism.go:80` concatenates the global
   `config.lua` pair with the surface's own hook, so a per-surface hook can override a global. A
   per-pack `derive.lua` registering by surface name (the `yolo.transform(agent, fn)` shape
   `luahook` already uses) is fewer files.

   _Leaning:_ per-pack file, registering per-surface functions — matches the existing
   `yolo.transform` registration idiom, and one file per pack is easier to review than N. Keep the
   per-surface `transform` key as the *user's* override slot, which is what it already is.

   **Answer:**
   > _(empty — fill in when decided)_

2. **Whether `state.scope: "machine"` should require more than a `because` string.**
   A machine-scoped state claim is cross-workspace credential leakage by design
   (`packdecl.go:59`). A required justification string is documentation, not a control.

   _Leaning:_ require the string *and* surface it in `yolo pack explain --footprint` and at first
   launch, but do not gate it on origin — the legitimate case (one Claude login serving every
   workspace) is exactly what a fetched agent pack would need, and refusing it would push users to
   local packs for no security gain. The control that matters is visibility, since the alternative
   to a shared credential dir is re-authenticating per workspace, which users will defeat by hand.

   **Answer:**
   > _(empty — fill in when decided)_

3. **Whether two packs may contribute `config` to the same surface.**
   The table in §3.2 says error-unless-`overrides`. But a plausible real case exists: a `house-rules`
   pack that wants to add two `managed` keys to `claude/settings` without vendoring the whole
   surface.

   _Leaning:_ allow it as an explicit `kind: "config-overlay"` naming the target surface, ordered
   after the owning pack, and refuse silent same-surface duplicates. Ordering across packs is
   already "later wins" for skills and bins, so the precedent exists — but a *silent* second writer
   to one config file is how you get a surface nobody can explain.

   **Answer:**
   > _(empty — fill in when decided)_

4. **Whether tier-2 effect Lua is worth building at all.**
   It would let `shared_credentials` and `per_jail_history` leave core, opening the hook set. It
   also puts a capability sandbox on the boot path, and the two hooks it would replace are ~40
   lines of Go that work.

   _Leaning:_ **no, not yet** — and say so in the doc rather than leaving it as future work.
   `packhooks.go:24`'s rule is correct: wait for a real second case. Tiers 1 and 3 cover everything
   known today, and tier 3 (subprocess, frozen protocol, re-approval) is the honest place for
   genuinely arbitrary pack logic because it makes no sandbox claim it cannot keep.

   **Answer:**
   > _(empty — fill in when decided)_

5. **Whether the footprint belongs in the description hash.**
   If `describe --hash` covers the environment, a change in what packs *claim* is a change in the
   environment even when no rendered byte differs — e.g. a pack adding a machine-scoped state claim
   it does not yet write to.

   _Leaning:_ yes, hash the footprint. A claim is a capability, and a capability change is exactly
   what a sealed definition should notice. It also makes the re-approval gate in
   `third-party-pack-logic.md` §2.3 mechanical rather than a judgment call.

   **Answer:**
   > _(empty — fill in when decided)_

6. **Whether `retireMiseTools` survives the reform.**
   It is transitional by nature — tokens to strip from a workspace `mise.toml` for tools that used
   to be installed that way. It fits no kind cleanly, and it is the one field that is pure
   cleanup-of-yolo's-own-past.

   _Leaning:_ make it `kind: "retire"` with an explicit expiry note, or drop it entirely if no
   supported version still writes those tokens. It should not become a permanent kind for a
   temporary job — `Surface.RetireOnFirstRender` has the same smell and the same eventual deletion.

   **Answer:**
   > _(empty — fill in when decided)_

7. **What a `files` contribution means at `guest` / `host`.**
   §3.2 gives `files` exclusive path ownership enforced by a read-only bind mount. Off-container
   there is no mount namespace, and
   [yolo-as-environment-manager.md](yolo-as-environment-manager.md) §5 already ruled that **a copy
   is never a substitute for a mount** (it goes silently stale).

   _Leaning:_ `files` is refused by name below `jail`, while `skills` and `briefing` port — because
   their delivery is a *merge*/*concat*, which is a render, not a mount. That is the existing
   ruling restated per kind, and it is a good sign that the kind vocabulary makes it expressible in
   one row instead of a paragraph.

   **Answer:**
   > _(empty — fill in when decided)_

8. **Whether `yolo pack lint` should refuse a pack whose footprint collides with the user's own
   config.**
   §1.4's hole is that `host_files` reservations cover only embedded packs. With footprints, the
   union is computable across *all* selected packs — but the user's own `writable_home_dirs` and
   `host_files` entries are a third party to that union.

   _Leaning:_ compute all three (embedded, configured, user config) in one pass at `yolo check`
   time and report collisions with the *source* of each claim named. That retires the
   `packload.Embedded*`-is-not-selection-gated workaround, which exists precisely because the union
   could not be computed at the right time.

   **Answer:**
   > _(empty — fill in when decided)_

9. **Whether `skills` needs a reshape seam, or a canonical format is enough (§3.2a).**
   Skills today are copied as a plain `SKILL.md` tree into each agent's dir, with no reshape — the
   "merge" assumes every agent reads the same format. A second on-disk skills format (e.g. an agent
   wanting `skill.toml`) breaks the shared-skills merge, since one tree lands in every agent's dir.
   `config` already solved the analogous problem with `derive`; `skills` has no equivalent.

   _Leaning:_ declare `SKILL.md` + frontmatter the canonical format and keep skills a plain tree
   copy for now (today's behavior, made explicit); add a `skills`-kind reshape — the same `derive`
   slot emitting a file tree instead of a config value — only when a second real format appears. A
   `kind` carries a format contract, not just a footprint; the reform's job is to *name* that
   contract, not to pre-build the reshape.

   **Answer:**
   > _(empty — fill in when decided)_

10. **How far to generalize the neutral assembler (§3.6) now vs. incrementally.**
    The one-writer rule and the config assembler (compose + `derive`) exist today; skills/briefing
    have a neutral stager with only a trivial combine. The question is whether to build the
    *general* single-writer assembler — one module, every kind, a provenance manifest, one staging
    tree layered in `:ro` — up front, or to keep growing the existing per-subsystem owners until a
    real second shared-file case forces unification.

    _Leaning:_ commit to the rule and the direction now (it is already how config and skills behave,
    so nothing regresses), but generalize the *module* incrementally — unify on the first shared
    file that is neither config nor a skills/briefing tree, since that is the case the current two
    owners cannot express. Building a universal assembler before a third combine rule exists risks
    inventing combine semantics no pack needs, the same speculation §3.4 warns against for hooks.
    The provenance manifest is the piece worth pulling forward regardless, because it is what makes
    the §3.2 collision report and `--footprint` exact.

    **Answer:**
    > _(empty — fill in when decided)_
