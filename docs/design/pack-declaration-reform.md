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

## 4. If we let the environment have opinions

Everything to here obeys a self-imposed rule: **"core does not know what an agent is."** The
manifest is maximally self-describing, and core renders it through one loop with no switch on any
tool name. That rule is load-bearing and this section does not propose deleting it. But it is worth
asking directly — because the doc's author flagged it as a possible misstep — **what improves if
yolo is allowed to hold opinions about the environment it manages, given that environment is always
an *agentic development* one, never a general-purpose container?**

### 4.1 The rule was never "be domain-blind" — separate the two things it conflates

"Core doesn't know what an agent is" bundles two very different commitments, and only one of them
is actually valuable:

1. **No switch on a tool *name*.** Core must not contain `if agent == "claude"`. This is the real
   win: it is why adding a seventh tool is a `pack.json` and not a Go change, why the six render
   paths collapsed into one loop, and why a third-party pack is a first-class citizen. **Keep this
   absolutely.**
2. **No opinion about the *domain*.** Core must not know what an MCP server, an LSP server, a
   skill, a briefing, or "approval posture" *is*. **This one is already false, and pretending
   otherwise is the misstep.** `computed.go:22` says it in as many words: *"Core owns the sources
   (it knows what an MCP server is; that is config, not an agent concept)."* `knownComputedSources`
   is a closed set of domain nouns core understands. The proposed `kind` vocabulary (§3.1) — `skills`,
   `briefing`, `config`, `reads-host` — is a *list of opinions about what an agentic dev environment
   is made of.* A generic container manager would not have a `skills` kind.

So the honest framing is not "should core have opinions" — it already does — but **"core may have
opinions about the DOMAIN (agentic dev environments), never about a specific TOOL."** Domain nouns
are shared, closed, and core-owned; tool names stay entirely in packs. That line is the one worth
drawing, and it is sharper and more defensible than the blanket rule.

### 4.2 What taking opinions buys

Once the environment is allowed a point of view, several things the self-describing manifest makes
awkward become clean:

- **A canonical MCP-server shape, pulled to a shared location.** This is the concrete win, and it
  is needed regardless of the rest of the section: today the MCP definition is redeclared per
  agent (six hand-written projections of one table — `packs-and-the-prism.md §2.6` documents them),
  which is the N×M problem. There is **one canonical MCP form** already (`mcp.go:120`,
  `name → {command, args, env}`); it just has no single home. Two places it could live, and the
  choice is a real fork worth stating:

  1. **In core.** Core owns the canonical `mcp_server` type and the reshape *ops*
     (`internal/agentcfg/project` already exists); an agent pack declares only its *dialect delta*.
     Pro: it is where `knownComputedSources` already puts MCP (§4.1 — a domain noun, not a tool), so
     this is the least new machinery. Con: the *set of servers* is data a user supplies, so core
     owning the *type* is fine but core must not own the *contents* — the type is core, the
     instances stay config/pack data.
  2. **In a shared dependency pack.** A dedicated pack `exports: { mcp_servers: … }`, and agent
     packs `import` the type (never the producing pack by name) with a projection — the typed-export
     graph from `packs-and-the-prism.md §2.6`. Pro: keeps core thinner and makes MCP composition a
     first-class pack feature (third parties ship MCP packs the same way); it is the more
     "self-describing" answer. Con: it introduces inter-pack dependencies (a resolution/ordering
     concern packs do not have today), and a shared dep pack that every agent pack needs is a
     de-facto part of the platform anyway — so the isolation win is partly illusory.

  The two are not exclusive: **the *type + ops* can be core (choice 1) while the *instances* travel
  as typed exports (choice 2's mechanism).** That split matches §4.1's rule exactly — core names the
  domain noun and how to reshape it, packs supply the tool mapping and the actual servers. Leaning
  there, but the fork is genuinely open; see open question 1 (the load-bearing one).
- **Example pack configs to copy-paste, not a baked-in default.** The empty-config-yields-no-agent
  default stays (nothing is silently on). What an opinionated environment offers instead is a set of
  **worked `packs` snippets** — "here is a sensible claude + house-rules config, paste it into
  `~/.config/yolo-jail/config.jsonc`" — so the happy path is copy-a-known-good-config, not
  assemble-from-scratch. Opinions ship as *documentation and examples*, never as behavior that turns
  on without the user writing it.
- **The briefing gets truer.** The self-describing briefing (see
  [yolo-as-environment-manager.md](yolo-as-environment-manager.md) §6) can only describe what it
  has words for. If core knows the domain, the briefing can say "you have these 3 MCP servers,
  these language servers, this approval posture" — not just "here are some files." This is the
  clearest payoff of core holding domain nouns: the description an agent reads about its own
  environment becomes specific instead of a file listing.

### 4.3 What it costs, and the line that keeps it safe

The danger is obvious and is exactly the instinct behind the original rule: **opinions calcify into
a switch on a tool name, and then the batteries-included environment manager quietly becomes six
hardcoded agents again.** The discipline that prevents it is the §3.1 rule restated:

> Core may name **domain nouns** (`mcp_server`, `lsp_server`, `skill`, `briefing`, `approval`) in a
> **closed, tool-independent** set. Core may never name a **tool** (`claude`, `copilot`). A pack
> maps its tool onto the domain nouns; core never maps a domain noun onto a specific tool.

Concretely: a `canonical MCP type` is fine (it mentions no agent); a `claudeMCPToggle` is the
forbidden thing. `knownComputedSources` already lives on the right side of this line, which is the
proof the line is holdable. And every opinion still ships as **overridable default**, never a
mandate — an opinionated baseline a pack can replace is batteries-included; an opinionated baseline
a pack *cannot* replace is the sandbox-clone the whole product is trying not to be
([yolo-as-environment-manager.md](yolo-as-environment-manager.md) §1).

### 4.4 The recommendation

Adopt the reframing, not a pile of new features: **replace "core does not know what an agent is"
with "core knows the DOMAIN, not the TOOL."** It costs nothing today (it merely describes what
`computed.go` already does) and it makes the `kind` vocabulary honest about what it is (a set of
domain opinions). The one concrete near-term step it justifies — and the one the reviewer confirmed
is needed regardless — is **pulling the canonical MCP-server definition out to a shared location**
(§4.2), whether that is core or a shared dependency pack (open question 1). Of the other threads,
only the **truer briefing** is worth pursuing (it is the clearest payoff of core holding domain
nouns); an opinionated default is explicitly *not* wanted as behavior — it ships only as
copy-paste example configs — and semantic lint is out of scope. Everything stays behind the same
override-not-mandate and no-tool-names discipline.

---

## 5. What this deliberately does not change

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

## 6. What this costs

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
- **Domain opinions (§4) are a slope with a real bottom.** "Core knows the domain, not the tool" is
  a discipline, not a compiler-enforced boundary — nothing stops a future PR from sneaking a tool
  name into a "domain" noun (`claudeMCPToggle` wearing a `mcp` hat). The mitigation is that the
  line is stated and the existing `knownComputedSources` sits on the right side of it as a worked
  example, but it wants a lint or a review reflex, because the failure mode is exactly the
  six-hardcoded-agents regression the whole reform exists to prevent.

---

## 7. The one-paragraph version

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

These are ordered by how much they constrain the schema: the ones at the top change what a
`pack.json` *is*, so they must be answered before any of it is built; the ones lower down are
policy choices that can move without a format break. Each says what is genuinely at stake — what
breaks if it is decided wrong — because a leaning without a cost is just an opinion.

1. **The wire shape of the canonical MCP type, and where it lives (§4.2, the load-bearing one).**
   This is the question the reviewer confirmed is needed regardless, and it is first because it
   sets the pattern every other domain noun (LSP, later) will copy — get it wrong and every agent
   pack inherits the mistake. Three sub-decisions hide inside it:

   - **The type.** The canonical form is `name → {command, args, env}` today (`packs-and-the-prism.md`
     §2.6, `mcp.go`). Is that *the* type, frozen, or does it need a version field so a future MCP
     transport (HTTP servers, not just stdio `command`) does not force a breaking change? An MCP
     server with a URL instead of a `command` already exists in the wild — if the type cannot hold
     one, choice-1's "core owns the type" becomes "core owns a type that is already behind."
   - **The split.** The leaning is **type + reshape ops in core, instances as typed exports** — but
     that split has a seam that must be specified: an agent pack's *projection* (its dialect delta)
     is core-adjacent data (it reshapes a core type), while the *server list* is user/pack data.
     Where does the projection live — in the agent pack (it is tool-specific) or in core (it is a
     reshape of a core type)? If in the pack, core owns the type but not the transform, and a
     malformed projection is a pack bug caught at lint; if in core, adding an agent's dialect is a
     core change, which reintroduces exactly the "core knows the tool" coupling §4.1 forbids. **The
     projection must live in the agent pack** for the §4.1 rule to hold — which means the shape of a
     projection is part of the pack format, not an internal detail.
   - **The example that is also the acceptance test.** opencode's projection renames `env →
     environment`, folds `command + args` into one array, and injects `type:"local"` +
     `enabled:true` (`agent_configs.go:131`). Any design that cannot express *that entry* in
     declared ops (`Copy` with rename, `Fold`, `Inject` — all already in `internal/agentcfg/project`)
     is under-powered and must fall back to a `derive` function. The open question is whether the
     four existing ops cover all four shipped agents or whether one needs `derive`; that is
     answerable now by porting them, and should be, before the type is frozen.

   _Leaning:_ freeze `name → {command, args, env, type?, url?}` (room for non-stdio) as the core
   type; ops + the reshape engine in core; the per-agent projection in the agent pack; server
   instances as typed exports (`packs-and-the-prism.md` §2.6). Prove it by porting all four shipped
   projections to declared ops first — if opencode's needs `derive`, the op set is the thing to fix,
   not the type.

   **Answer:**
   > _(empty — fill in when decided)_

2. **Whether two packs may contribute `config` to the same surface — and if so, how a conflict on
   one key resolves.**
   This is a schema question, not a policy one, because it decides whether a `config-overlay` kind
   exists at all. The motivating case is real and common: a `house-rules` pack wants to assert two
   `managed` keys in `claude/settings` without vendoring the whole surface (which would fork it and
   go stale the next time the `claude` pack updates its defaults). §3.2's table says
   error-unless-`overrides`; that is too blunt if overlays are a first-class need.

   The hard part is not *whether* to allow it but *key-level conflict*: if the `claude` pack sets
   `permissions.defaultMode: "acceptEdits"` and a `house-rules` overlay sets it to `"plan"`, who
   wins, and is the loser told? Silent last-wins is how you get a surface nobody can explain — the
   exact §1.2 failure this whole reform targets, reintroduced one layer up. This ties directly to
   §3.6: an overlay is another *input* to the neutral assembler, so the assembler's provenance
   manifest is what makes the resolution legible ("key X: claude pack lost to house-rules overlay").

   _Leaning:_ allow it as an explicit `kind: "config-overlay"` naming the target surface and ordered
   after the owning pack (later-wins, the precedent skills/bins already use), but **require the
   assembler to record per-key provenance and surface any overlay that overrode an owner's key** in
   `--footprint` and at lint. Refuse *silent* same-surface duplicates — an overlay must name what it
   targets. The overlay is cheap; the provenance is the part that must not be skipped.

   **Answer:**
   > _(empty — fill in when decided)_

3. **Whether the footprint (and the derive/projection logic) belongs in the description hash.**
   This decides whether `describe --hash` and `apply --sealed`
   ([yolo-as-environment-manager.md](yolo-as-environment-manager.md) §3.3) can actually detect a
   capability change — and it is more subtle than "hash the footprint." A pack can change what it
   *does* without changing any rendered byte today: add a machine-scoped `state` claim it does not
   yet write to, gain a `reads-host` grant, or — the sharp one — change its `derive.lua` so the same
   inputs produce the same output *now* but different output when the live MCP table changes next
   week. A byte-hash of the rendered files misses all three.

   So the real question is *what* to hash: the rendered output (misses latent capability changes),
   the declared footprint (catches claims but not logic), or the footprint **plus the hash of every
   `derive`/projection script** (catches logic changes too, at the cost of churn — editing a comment
   in `derive.lua` changes the hash). This matters because a re-approval gate
   (`third-party-pack-logic.md` §2.3) built on the wrong hash either misses a privilege escalation
   or cries wolf on every cosmetic edit.

   _Leaning:_ hash **footprint + derive-script contents**, not rendered output — a claim is a
   capability and a script is executable capability, both of which a sealed definition must notice;
   accept the cosmetic-edit churn because a false "this changed, re-approve" is safe and a missed
   capability change is not. Keep it a *separate* hash from the rendered-output hash (§3.3's two-hash
   idea) so CI can pin the strict one and drift-detection can use the loose one.

   **Answer:**
   > _(empty — fill in when decided)_

4. **How far to generalize the neutral assembler (§3.6) now vs. incrementally, and what the
   provenance manifest's format is.**
   §3.6 commits to one writer per file; this question is whether one *module* writes everything up
   front or the per-subsystem owners (compose for config, the stager for skills/briefing) unify
   later. The stakes are real either way: build the universal assembler before a third combine rule
   exists and you invent combine semantics no pack needs (the §3.4 over-building trap); defer too
   long and you get a third bespoke owner that has to be torn out, and the collision-checking that
   is the whole safety story stays partial in the meantime.

   The piece that is *not* deferrable is the **provenance manifest** — the record of which
   contribution produced which file/key — because three separate features depend on it: the §3.2
   cross-pack collision report, `yolo pack explain --footprint`, and question 2's key-level overlay
   resolution. Without it those are best-effort; with it they are exact. So its format is itself an
   open question: is it the existing `last_render` sidecar generalized to every kind, a single
   machine-wide manifest, or per-file? It has to be machine-readable (the collision checker consumes
   it) and it has to survive across runs (drift detection compares against it).

   _Leaning:_ commit to the one-writer rule now (nothing regresses — config and skills already obey
   it), generalize the *module* only on the first shared file that is neither config nor a
   skills/briefing tree, but **build the provenance manifest immediately** as a generalization of
   `last_render`: one entry per written file, listing each contributing pack and (for composed
   files) the winning source per key. It is small, it unblocks three features, and it is the thing
   that makes "one writer" *checkable* rather than merely asserted.

   **Answer:**
   > _(empty — fill in when decided)_

5. **Whether `skills` needs a reshape seam, or a canonical format is enough (§3.2a).**
   Skills today are copied as a plain `SKILL.md` tree into each agent's dir with no reshape — the
   merge assumes every agent reads the same format. This is important because it is the *first test*
   of whether the §4 "canonical domain type" idea generalizes past config: a skill is a domain noun
   exactly like an MCP server, and if two agents read incompatible skill formats, the "ordered
   merge" conflict rule in §3.2 quietly produces a tree half the agents cannot parse. The failure is
   silent — the merge succeeds, the files are wrong — which is the worst kind for this reform.

   The decision mirrors question 1's structure: is there a canonical skill type with per-agent
   projections (the general answer, reusing the `derive`/projection seam to emit a *file tree*
   instead of a config value), or is `SKILL.md` simply mandated and a non-conforming agent
   unsupported? The cost of the general answer is a second place `derive` emits (files, not values),
   which the assembler (question 4) would have to understand.

   _Leaning:_ declare `SKILL.md` + frontmatter the canonical format and keep skills a plain tree
   copy **now** (it is today's behavior, made explicit), but treat this as the *design probe* for
   question 1 — whatever projection shape MCP settles on is the shape a `skills` reshape reuses when
   a second format appears. Do not build the reshape before that second format exists; do make sure
   question 1's projection design is not accidentally config-only, so skills can adopt it later
   without a format break.

   **Answer:**
   > _(empty — fill in when decided)_

6. **Whether `state.scope: "machine"` should require more than a `because` string.**
   Important because it is the one contribution kind that *intends* to leak across the isolation
   boundary — a machine-scoped `state` claim is cross-workspace credential sharing by design
   (`packdecl.go:59`), which is the single sharpest thing a pack can do to the environment short of
   installing software. A `because` string is documentation, not a control, and the question is
   whether that is enough or whether machine scope needs origin-gating like `reads-host` and
   `install` do.

   The tension: the *legitimate* case (one Claude login serving every workspace) is exactly what a
   fetched agent pack legitimately needs, so origin-gating machine scope would break the common good
   case to stop a rare bad one — and the bad case (a pack quietly reading another workspace's state)
   is better caught by *visibility* than prohibition, because a user who is denied a shared
   credential dir will just re-authenticate per workspace, or worse, symlink it by hand.

   _Leaning:_ require the `because` string, surface every machine-scoped claim in `--footprint` and
   at first launch (loudly — it is in the "needs review" count), and fold it into the description
   hash (question 3) so gaining machine scope forces re-approval — but do **not** origin-gate it.
   Visibility + re-approval is the right weight; prohibition pushes users to defeat it.

   **Answer:**
   > _(empty — fill in when decided)_

7. **Whether `yolo pack lint` should refuse — not just report — a footprint collision with the
   user's own config.**
   §1.4's live hole: reservation lists cover only embedded packs, so a user `host_files` or
   `writable_home_dirs` entry can silently land on a path a configured pack needs, surfacing as an
   opaque mount conflict at boot. Footprints make the full union computable, so the mechanism is
   finally available; the open question is the *severity* — report (warn, proceed) or refuse (fail
   the check). It matters because this is the "good citizen" promise's teeth: a collision that only
   warns is a collision most users scroll past.

   The subtlety is *whose* collision. Two packs colliding is a pack-author error → refuse at lint.
   A pack colliding with the *user's own* config is arguably the user's call (their config, their
   machine) → report and let them decide. Conflating the two either nags authors' users about their
   own overrides or lets a genuine pack-vs-pack bug through as a warning.

   _Leaning:_ compute all three sources (embedded, configured, user config) in one pass at `yolo
   check`, **refuse** on pack-vs-pack collisions (an author bug, and the jail would break anyway),
   **report with the source named** on pack-vs-user-config collisions (the user owns that call), and
   retire the `packload.Embedded*`-is-not-selection-gated workaround, which exists only because the
   union could not be computed at the right time.

   **Answer:**
   > _(empty — fill in when decided)_

8. **What each kind means below `jail` — the per-notch matrix, not just `files`.**
   §3.2 enforces kinds with jail mechanisms (a `files` bind mount, a `state` overlay); at `guest`
   and `host` ([yolo-as-environment-manager.md](yolo-as-environment-manager.md) §4) those mechanisms
   do not exist. This is important because the kind vocabulary is supposed to make the confinement
   matrix *legible* (`check --at` names what is inert), and that only works if every kind has a
   ruling — a kind with no per-notch answer is exactly the silent-inert-surface bug (macos-user
   renders zero surfaces) reappearing under a new name.

   The clean cases are known: `files` refused below `jail` (a copy goes silently stale — §5 of the
   environment-manager doc), while `skills`/`briefing`/`config` port because their delivery is a
   render (merge/concat/compose), not a mount. The unclear ones are `state` (a machine-scoped claim
   at `host` *is* the user's real home — is that honored or refused?), `reads-host` (at `host` there
   is nothing to mount because the file is already there — inert or trivially satisfied?), and
   `program`/`install` (already ruled: `install` never below `jail`). Each needs a row.

   _Leaning:_ make the per-notch ruling a **required column of the kind registry**, so adding a kind
   forces answering it for all three notches or the kind does not compile — turning §8's "every
   feature needs a per-notch ruling" cost into a structural obligation rather than a thing someone
   remembers. `files` refused; `skills`/`briefing`/`config` render; `reads-host` trivially satisfied
   at `host` (no-op, the file is native) and a mount below; `state` at `host` maps to the real home
   and machine scope is a no-op there.

   **Answer:**
   > _(empty — fill in when decided)_

9. **Whether `derive` Lua is per-surface or per-pack — and whether the user keeps an override slot.**
   Smaller in blast radius than the above, but it fixes the authoring ergonomics and the review
   surface, so it is worth settling. `Surface.Transform` is per-surface today; `entrypoint/prism.go:80`
   concatenates the global `config.lua` pair with the surface's own hook so a per-surface hook can
   override a global. A per-pack `derive.lua` registering by surface name (the `yolo.transform(agent,
   fn)` idiom `luahook` already uses) is fewer files and one review target per pack.

   The part that makes it more than bikeshedding: whoever can supply a `derive` function can run
   sandboxed code at compose time, so "how many `derive` entry points exist and who owns each" is a
   security-surface question, not just a layout one. A per-pack file with clear ownership is easier
   to audit than N per-surface snippets scattered through the manifest; and the *user's* override
   (their own `config.lua`) must stay a distinct, higher-precedence slot so a pack's `derive` can
   never silently win over a user's local transform.

   _Leaning:_ per-pack `derive.lua` registering per-surface functions (matches the existing idiom,
   one audit target per pack), with the per-surface user `transform` key retained as the *user's*
   override — the precedence that already holds, made explicit in the kind vocabulary.

   **Answer:**
   > _(empty — fill in when decided)_

10. **Whether tier-2 effect Lua (capability-restricted boot code) is ever worth building.**
    This bounds how far the "packs can run code" story goes, so it is important to settle as a *no*
    explicitly rather than leave as tempting future work — an unanswered "maybe" here is what leads
    someone to build a capability sandbox on the boot path for two hooks that are 40 lines of Go.
    Tier 2 would let `shared_credentials` and `per_jail_history` leave core and open the closed hook
    set (§3.4), at the cost of a capability-by-declaration sandbox that is genuinely new,
    genuinely on the boot path, and genuinely expensive to get right.

    The reason this is not just "later": tier 3 (a subprocess projector over a frozen protocol,
    with re-approval — `third-party-pack-logic.md` §2.1) already covers *arbitrary* pack logic, and
    it makes no sandbox claim it cannot keep (it only ever sees the JSON handed to it). So tier 2 is
    a *middle* that may never earn its complexity — everything below it is data, everything above it
    is an honest subprocess.

    _Leaning:_ **no, not yet**, stated as a decision in the doc. `packhooks.go:24`'s wait-for-the-
    second-case rule is correct; tiers 1 (named hooks) and 3 (subprocess) bracket every known need,
    and the closed hook set is a feature (an auditable, finite list of imperative capabilities) until
    a concrete second case proves otherwise.

    **Answer:**
    > _(empty — fill in when decided)_

11. **Whether `retireMiseTools` (and `RetireOnFirstRender`) survive as kinds, or are ejected as
    one-shot migrations.**
    Lowest-stakes of the eleven, but it decides a principle worth stating: whether the manifest
    format carries *transitional cleanup* at all. `retireMiseTools` strips tokens from a workspace
    `mise.toml` for tools once installed that way; `Surface.RetireOnFirstRender` deletes pre-prism
    sidecars. Both are pure cleanup-of-yolo's-own-past, both fit no kind cleanly, and making either
    a permanent `kind` bakes a temporary job into the format forever — the schema-bloat this reform
    exists to reverse.

    _Leaning:_ do **not** give cleanup a kind. Either drop `retireMiseTools` entirely if no supported
    version still writes those tokens, or handle retirement as a one-shot host migration
    (`internal/hostmigrate` already does this class of thing) keyed on a version stamp, not a
    standing manifest field. A format should describe the environment, not carry a changelog of its
    own past mistakes.

    **Answer:**
    > _(empty — fill in when decided)_
