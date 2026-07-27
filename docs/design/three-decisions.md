# The three decisions, in depth

**Status:** design, 2026-07-26. Answers *"tell me more about these 3 decisions"* under the
now-fixed target: **no Go-owned agents at all; agent support exists only as official packs
shipped with yolo; a default jail has no agent.**

**Reads with:** [packs-and-the-prism.md](packs-and-the-prism.md) (phases, contribution kinds,
typed exports), [what-yolo-is.md](what-yolo-is.md) (boundaries),
[../plans/packs-rip-out.md](../plans/packs-rip-out.md) (the work framing),
[../plans/composed-config-work.md](../plans/composed-config-work.md) (the existing list).

**The target state changes two of the three answers.** That is the main finding, and it is
why this doc exists rather than just a longer bullet in the plan.

---

## 0. Two scope rulings that came in after the first draft

Both narrow the problem, and both correct an error above. Recorded first because everything
downstream depends on them.

### 0.1 Packs are USER-LEVEL ONLY

**Ruling:** packs exist at user scope. There is no such thing as a workspace/repo-level pack.
A repo that wants to configure its agents already has a git repo and can lay out whatever it
likes in the workspace — it does not need a distribution mechanism to reach files it already
owns.

This is a bigger simplification than it sounds, because a large fraction of the pack proposal
exists purely to make *workspace* packs safe:

| Machinery | Why it existed | Under user-only |
|---|---|---|
| `pack_requests` in workspace scope | a repo asking for a pack it may not name itself | **gone** |
| the "in-jail writer" hole (§7) | an agent editing the workspace could grant itself a pack | **gone** — the agent cannot write user config |
| source-bearing scope enforcement for packs | a workspace config must not name host files | **moot** — packs are only ever user scope |
| approvals / `approve --from-workspace` | promoting a workspace request to user scope | **gone** |
| two-pack collision arbitration across scopes | merge order between workspace and user layers | **simplified** to one scope |

**And it dissolves the `AgentSpec.HostFiles` tension entirely.** I proposed
"a pack may request a host file; only user-scope config may grant it." With packs *already*
user-scope-only, there is no request/grant split to build — a pack naming a host file is by
construction a user-scope declaration, which is exactly the trust level `host_files` already
requires. The existing rule (`config/hostfiles.go:865-877`: a source-bearing entry in
workspace scope is a hard error, *because* a workspace config travels with the repo and is
agent-editable) applies unchanged, and packs simply live on the correct side of it.

Note what the boundary actually is, since I described it imprecisely before: user config
**can** name `~/.ssh/id_ed25519` — `SourceBearing()` (`hostfiles.go:142`) gates on *authorship
trust*, not on capability. The boundary is "a human editing their own user config decided
this," not "yolo vetted the path."

**Consequence for the layer stack:** the prism's `workspace` layer has zero non-test
producers today, and packs were going to be its first. Under user-only packs, packs contribute
at **user** scope instead — so `Inputs.Workspace` stays unfilled, and the natural producer of
a workspace layer becomes *the workspace itself* (a repo laying out its own config), which is
a different and simpler feature.

### 0.2 A rebuild is not a release (again)

See the retraction under Decision 2. Official-pack logic can be **compiled at image-build
time**, so "adding an agent needs a yolo release" was false. This reopens Go as an
implementation option for official packs and un-forces Decision 2.

---

> **⚠ RULED 2026-07-26 — Decision 1 is settled, and not the way this section recommends.**
> Composition **stays in the container.** Only image-build inputs (pack `provision`
> contributions, `packages`) and host-file reads (the `host` layer, pack fetch, lockfile)
> run host-side. The rule is *what needs the host*, not a location preference.
>
> The reasoning that settles it: I argued host-side for its error-timing benefit, but once
> re-render-while-running is explicitly unsupported (it is), there is no reason to move
> composition at all — and not moving it deletes the largest port in the plan. My
> "host-side is the only coherent macos-user story" argument also fails: it assumed a mount
> step to compose into, and with composition staying in-jail, macos-user's lack of a
> host/jail filesystem split makes it the degenerate case that already works.
>
> The error-timing benefit has to be recovered another way: **host-side validation of pack
> contributions** at `yolo check` and run assembly. Tracked as BACKLOG D1.
> Full ruling: [../plans/open-rulings.md](../plans/open-rulings.md).

## Decision 1 — where does composition run?

### The question, restated precisely

Not "host or jail" as a binary. The real question is **which of three things happens on the
host**: (a) computing the composed bytes, (b) writing them to their destination, (c)
reconciling in-jail edits back into the overlay. They can be split.

### What the target state changes

Under the old framing (agents built into yolo, always present) host-side composition was
attractive mainly for *error timing*. Under **no-agent-by-default** it becomes something
stronger: **the set of things to compose is now dynamic**, derived from which packs are
enabled. A jail with no agent composes nothing. So composition needs a *plan* before the
container starts, which is host-side work no matter where the bytes get written.

That is a real shift: the question moves from "would host-side be nicer?" to "the host
already has to decide what exists, so how much more should it do?"

### The recommendation

**Compose on the host; write on the host; reconcile on the host at next assembly.** With one
carve-out that is not negotiable: **anything that needs the jail's own filesystem stays
in-jail**, and that set is small and identifiable — the lazy agent-CLI launchers, npm/LSP
installs, mise, and anything reading a nix-store path.

The clean split is **config vs binaries**:

| | Where | Why |
|---|---|---|
| composed config files | host | pure function of config; nothing probes the container |
| capture/reconcile of in-jail edits | host, at next assembly | the files are in a host directory already (`GlobalHome`) |
| agent CLI install | in-jail, lazily | needs the jail's node/npm/PATH; already works this way |
| MCP/LSP server install | in-jail | same |

**Verified, and it is the load-bearing fact:** an empty-agent jail boots today.
`agents: []` validates with a warning
(`[WARN] config.agents: empty list — no coding agents will be installed in the jail`) and a
real nested run reached `BOOT_OK`. So "no agent by default" is not a new capability to
build — it is a supported state already.

### The thing that surprised me — and my first reading of it was wrong

That same probe showed **`~/.claude`, `~/.codex` and `~/.copilot` all exist in an
empty-agent jail.** I concluded from that "agent state is machine-global, not per-jail."
**Retracted — that is backwards.** Re-probing:

- `<workspace>/.yolo/home/claude/` holds **most** state (`claude.json`, `history.jsonl`,
  `debug`, `cache`);
- `~/.local/share/yolo-jail/home/.claude/` is an **empty mountpoint** (`total 0`);
- **but `.claude-shared-credentials` is genuinely machine-global** — `~/.claude/.credentials.json`
  is a symlink *out* to it (`entrypoint/claude.go:106-108`), the dir is bind-mounted from
  `GlobalHome` (`assemble.go:174-175`), and maintaining claude auth across workspaces and jails
  is a designed feature with five supporting call sites. So the answer is **two tiers**, not
  one: per-workspace by default, machine-global for identity/credential state.

`prepareWsState` (`cli/run/prepare.go:135-142`) creates a per-workspace dir per selected agent
and `assemble.go:169-171` binds each over `/home/agent/.<subdir>`. The `GlobalHome` entries
exist *only* so the OCI runtime has a mountpoint to bind onto — it cannot `mkdirat` inside a
`:ro` bind. So **agent state is per-workspace**, and what I saw was scaffolding, not state.

The one genuine machine-wide exception is `.claude-shared-credentials`
(`assemble.go:173-176`, mounted only when claude is selected) — which is what lets a login
survive across workspaces, and is exactly the thing ruling 2 generalizes into a pack-declared
field (BACKLOG B5).

What survives of the original observation:

- Lazy in-jail install works because the install outlives the container — still true.
- Pack removal has no natural cleanup point — still true, and now **ruled**: leave abandoned
  per-workspace state in place, deliberately and with a report.

**RULED 2026-07-26** (see [../plans/open-rulings.md](../plans/open-rulings.md) ruling 2). The
question as originally posed — is agent/pack state
per-machine (today's behavior, shared `GlobalHome`) or per-jail? Today's answer is defensible
— it is what makes an agent's login survive a restart — but it should be a decision rather
than an accident.

### Also verified: the MCP bootstrap is not agent-gated

`internal/entrypoint/shell.go:167` installs `chrome-devtools-mcp` and
`server-sequential-thinking` unconditionally — `if ! command -v chrome-devtools-mcp`. In the
empty-agent probe it installed **112 npm packages for zero agents.** Under an all-packs
model this is exactly what should become a pack contribution (an MCP pack), gated on
something actually wanting it. Small, concrete, and it is a real win of the target state
rather than a hypothetical one.

### Backends

I argued `macos-user` was the acid test and *favored* host-side composition: no mounts, no
`/ctx`, so "compose then mount `:ro`" is meaningless there while "compose then write" works.

**That argument is void under the ruling**, and it is worth seeing why, because the error is
instructive. It assumed the alternative was "compose *into* a mount" — but with composition
staying in the container, macos-user's lack of a host/jail filesystem split makes it the
*degenerate* case that already works: there is nothing to bridge. The backend I claimed forced
host-side composition is in fact the one least affected by the choice.

Apple Container's inability to bind-mount single files (`acMaterialize`) is likewise unchanged
from today rather than solved — it is a staging concern for pack *content*, which is
host-side either way.

### Cost, honestly

In-jail re-render dies. Today an agent edits a composed file and the next boot reconciles
from inside. Host-side means reconciliation happens at *assembly*, so a running jail cannot
re-render itself — you would need `yolo config render` to be invokable in-jail and to write
through, or accept that re-render requires a restart. **That is the one genuine loss**, and
it is the sub-question worth deciding explicitly rather than discovering.

---

## Decision 2 — are projections data or code?

### The spec, enumerated

Five real projections exist. I read each one; these are the exact operations:

| Projection | Location | Operations required |
|---|---|---|
| **codex** MCP | `codex.go:15` | passthrough, default-empty (`args`), conditional-include (`env` only if non-empty) |
| **opencode** MCP | `agent_configs.go:131` | **rename** (`env`→`environment`), **fold** (`command`+`args` → one array), **inject constants** (`type:"local"`, `enabled:true`) |
| **gemini** MCP | `agent_configs.go:167` | **cross-type derive** (LSP defs → MCP entries), key-suffix (`<name>-lsp`), arg templating |
| **claude** MCP | `prism_claude.go:104` | **route to a different surface** (`.claude.json`, not `settings.json`) + **tombstone** (`mcpServers: nil` to strip a host block) |
| **copilot** LSP | `prism.go:400` | default-empty ×2, **conditional-OMIT** — and note *omit ≠ null*, because a null leaf is an RFC-7386 tombstone the engine would drop |

That is **eight distinct operations**, and two of them are the kind that break naive designs:

- **`conditional-OMIT` vs `tombstone-null` is a semantic distinction**, not a formatting one.
  A mapping language that renders "absent" as null silently converts "leave this alone" into
  "delete this." Any candidate must be able to express both.
- **cross-type derivation** (gemini building MCP entries out of LSP definitions) is a
  projection whose input is *a different export type than the one it emits*. This is the
  case that most resembles a program.

### The recommendation

**A declarative projection with a typed operation set — not a general language, not Go.**
Specifically: a projection is data listing named operations (`rename`, `fold`, `inject`,
`default`, `omit_if_absent`, `suffix_key`, `route_to`, `tombstone`), with Lua as an escape
hatch for the residue.

Why this is the answer:

**The eight operations are a closed set derived from five real cases, not a guess.** A
language designed to cover them is small. A language designed "for projections in general"
is unbounded — that is the failure mode to avoid.

### ⚠ Retracted: "the target state removes Go as an option"

An earlier version of this section argued Decision 2 was **forced**, because projections in
Go would mean "adding an agent requires a yolo release." **That is wrong**, and the error
was conflating a *release* with an *image build* — the same mistake made once already in
[packs-and-the-prism.md §2.5](packs-and-the-prism.md), now made a second time in a different
costume.

The distinction:

| | What it is | Who it affects | Cost |
|---|---|---|---|
| **release** | ship a new yolo to other people | everyone | version bump, distribution |
| **image build** | local nix build of the image derivation | one machine | one slow run, already automatic |

An official pack's logic — Go *or* Lua — can be **compiled at image-build time**, because
official packs are embedded in the image anyway (the `bundled_loopholes/` model:
`go:embed`, one `Discover`, bundled and user dirs unioned). Adding an agent then costs an
image build, not a release. And an image build is the ordinary path: `packages` already feeds
`YOLO_EXTRA_PACKAGES` into the flake and the next run rebuilds and reloads itself.

**So Go remains a live option for official-pack logic, and Decision 2 is a genuine choice
again.** What it turns on is no longer "is Go available" but:

- **who authors an agent pack.** If only the yolo repo does, compiled-Go projections are fine
  and cheapest — the pack is data plus a compiled projection, both embedded. If a third party
  should be able to ship an agent pack *without* touching the yolo repo, its logic cannot be
  Go, because it would have to be inside `goSrc` at build time.
- **the goSrc fileset trap** (`flake.nix:61`): the hermetic build only sees `go.mod`,
  `go.sum`, `vendor/`, `cmd/`, `internal/`, `bundled_loopholes/`. Go pack logic must live in
  that set. That is fine for official packs (add `packs/` to the fileset, exactly as
  `bundled_loopholes/` is) and impossible for fetched ones.

**Revised recommendation:** the typed operation set is still the right primary answer, because
it keeps official and user packs expressing projections the *same way* — which is the whole
"structurally identical" claim. But **Go-at-image-build-time is the correct escape hatch for
official packs**, in place of Lua, and it is strictly better than Lua for that role: it is
type-checked, it is testable with `go test`, and it fails at build rather than at boot. Lua
remains the escape hatch for *fetched* packs, which cannot compile.

That is a better answer than the forced one, and it exists only because the release/rebuild
distinction was corrected.

**One projection dies on its own:** gemini is being removed (tranche 0), which deletes the
cross-type-derivation case. That does *not* mean the design can ignore it — copilot's
LSP surface has the same shape — but it does mean the hardest case is not load-bearing for
any *shipping* agent, which lowers the risk of getting it slightly wrong at first.

### Prerequisite that is currently broken

If Lua is the escape hatch, note that **the per-surface Lua seam has never run**:
`manifest.Surface.Transform` is populated from `host_files` config
(`entrypoint/hostfiles.go:135`) and validated, but the compose path passes only `in.Script`
— the global `config.lua` pair — and the field's sole readers are two display checks in
`configls.go`. Filed as work item 1.9. **Do not design a projection language on top of a
hook that has never executed**; fix it first, cheaply, and learn from it.

---

## Decision 3 — what do "agents as packs" mean for the stateful boot writers?

### This is the decision the target state changes most, and in a good way

The framing has been "2,200 lines of per-agent logic, some of it not data." (That figure also **counts test files** — non-test `prism*.go` is **917 lines**.) I went through
the actual call sites and **that framing overstates the problem substantially.**

**Every single surface already routes through exactly two mechanisms:**

- `renderSurfaceStateful` (`prism.go:101`) — the overlay-capturing path
- `renderSurfaceComputed` (`prism.go:258`) — the pure per-boot overwrite

Grepping the call sites: mise, codex, claude, opencode, pi, copilot, gemini, agy and the
`host_files` dynamic surfaces are **all** one of those two calls. There is no third
mechanism, and no agent has a bespoke render path.

So what is the per-agent code actually doing? Three things:

1. **building a `computed` map** — that is the projection, decision 2
2. **`os.MkdirAll`** of the agent's config dir (`prism.go:338, 372, 442, 485`)
3. **a one-shot migration `os.Remove`** guarded by `out.FirstMigration`
   (`prism.go:351-352, 459-460`) — deleting a dead bespoke sidecar

All three are **declarable.** A directory to create is data. A one-time file to delete on
first migration is data (and is transitional anyway — those removals exist to clean up
pre-prism sidecars and can eventually be deleted outright).

### The recommendation

**Reframe from data-vs-logic to per-agent *facts* (pack data) vs reconciliation *mechanisms*
(engine, agent-agnostic, selected by name).** A pack declares:

```
surface:
  path: ~/.codex/config.toml
  codec: toml
  mode: stateful          # names an ENGINE mechanism; the pack does not implement it
  defaults: {...}
  managed: {...}
  computed: <projection>
```

Counting against the inventory: **two mechanisms cover every existing surface.** That is a
small enough number to be the answer. The engine keeps `stateful` and `computed`; packs
supply facts.

### The one genuinely hard case, and why it does not break this

`writeClaudeJSON` (`claude.go:54-72`) is a read-modify-write over `~/.claude.json`, a file
claude owns and actively writes (~33 keys). It must merge yolo's assertions without wiping
live state: `loadObject` → `setDefaultMap(mcpServers)` → prune managed names → `updateFrom` →
`Set(projects[ws].enableAllProjectMcpServers, true)` → `setDefault(hasTrustDialogAccepted)`.
It is not a transform because its output depends on what the agent wrote since last boot.

But it is a **third mechanism**, not a special case:
`mode: read_modify_write` + `assert: {...}` + `preserve_unknown: true`. The engine implements
it once; the pack declares which keys yolo asserts. And
[composed-config-work.md](../plans/composed-config-work.md) item **2.2b already wants to move
`copilot/config` onto this same pattern for an independent correctness reason** (it can wipe a
live OAuth token). So the mechanism earns its place regardless of packs — which is the
strongest kind of argument for adding one.

**Three engine mechanisms, then: `stateful`, `computed`, `read_modify_write`.** That is the
whole surface taxonomy.

### What must stay in Go — the hard rule

`AgentSpec.HostFiles` (`agents.go:31-35`). Its own doc comment states the rule:

> It is a CREDENTIAL BOUNDARY: which host files cross into the jail is decided here, in
> yolo-shipped code, and can never be widened by a user/workspace config (that is what
> retiring `host_claude_files`/`host_pi_files` bought).

Its only mount consumer is `hostFileArgs` (`cli/run/hostclaude.go`), which reads *no config
key* and emits *no env var* — deliberately. Two entries exist today: claude's
`.claude/settings.json` and pi's `.pi/agent/settings.json`.

**"Stays in Go" is still the wrong resolution** — a hardcoded Go allowlist keyed by agent name
means agent support isn't entirely packs. But the fix is smaller than the request/grant
mechanism I first proposed.

**Settled by §0.1: packs are user-scope only, so a pack naming a host file IS a user-scope
declaration.** That is precisely the trust level `host_files` already demands. The existing
rule needs no extension:

- a source-bearing entry in **workspace** scope is a hard error, *because* a workspace config
  travels with the repo and is agent-editable (`config/hostfiles.go:865-877`);
- `SourceLessHostFilesFrom` (`hostfiles.go:945`) enforces it by construction — it parses
  workspace scope and returns only source-**less** entries, so a workspace config physically
  cannot name a host file.

Packs live on the permitted side of that line by definition. **No request/grant split, no new
mechanism, and `AgentSpec.HostFiles` can become pack data without reopening anything.**

**⚠ Narrowed 2026-07-27.** That conclusion is right for an EMBEDDED OFFICIAL pack and
wrong for a FETCHED one, and the difference matters more than the scope rule. Packs
being user-scope means a *workspace* cannot name a pack — it does not mean a user who
installed a third-party pack agreed to hand that repo their `~/.claude/settings.json`.
Installing a pack approves distributing skills and prose; it is not consent to a
host-file grant. So the rule is "a host-file grant may come only from a yolo-shipped or
user-authored source, never from fetched content", not "user scope makes it safe". See
[packs-and-the-prism.md §5](packs-and-the-prism.md).

One precision worth keeping straight, because I stated it loosely before: user config **can**
name `~/.ssh/id_ed25519`. `SourceBearing()` (`hostfiles.go:142`) gates on *authorship trust* —
"a human editing their own user config chose this" — not on a vetted path list. The boundary
was never that yolo approves the path; it is that an agent-editable file cannot choose it.

---

## Order of play

1. **Decision 3's reframe is free — take it now.** Three named engine mechanisms
   (`stateful`, `computed`, `read_modify_write`) covering every surface is not a research
   project; two of the three already exist and the third is already wanted for a correctness
   fix (2.2b). This also *shrinks* decisions 1 and 2 by removing the "irreducible logic"
   fear from both.
2. **Decision 2 is a real choice again — and it reduces to one question:** may a third party
   ship an agent pack without touching the yolo repo? If no, official-pack projections can be
   compiled Go and this is nearly free. If yes, they need the typed operation set plus Lua.
   Design against the eight enumerated operations either way; fix work item 1.9 first so the
   Lua path is real rather than notional.
3. **Decision 1 last of the three** — it is the largest port, and its main new argument
   (the compose set is dynamic under no-agent-by-default) only became visible once the target
   state was fixed.
4. **Unchanged: the tranche 0–2 prerequisites still come before all of it.** Nothing above
   removes the need to delete gemini, fix the correctness cluster, un-hardcode `/workspace`,
   and de-compose the credential surfaces.

## Still needs a human ruling

- ~~**May a third party author an agent pack without touching the yolo repo?**~~ **ANSWERED
  2026-07-26: yes, explicitly wanted.** And "then it can't be Go" was too fast — the `goSrc`
  fileset forbids *linking* third-party Go into yolo, not third-party Go as such. The design
  is declarative projections by default plus a **subprocess projector** escape hatch, which
  may be written in any language including Go, with the binary sourced either as an in-pack
  script, a nix package via the existing `{name, version, url, hash}` build-from-source form,
  or a prebuilt artifact. Official packs use the identical seam with a `yolo` subcommand as
  the projector, exactly as loopholes already do. Full design:
  [third-party-pack-logic.md](third-party-pack-logic.md).
- **Is agent/pack state per-machine or per-jail?** Today it is per-machine by accident of a
  shared `GlobalHome` — verified: agent dirs exist in an empty-agent jail. It should be a
  decision, and it determines whether pack removal can ever clean up.
- **Does a running jail need to re-render?** Host-side composition means reconcile happens at
  assembly. If in-jail re-render must survive, it needs an explicit mechanism.
- **First-migration vs user-asked-to-discard** — already open as
  `composed-config-work.md` §2.1, and it gates tranche 2, which gates the rip-out.
