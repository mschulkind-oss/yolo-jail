# How a pack is specified, and how it loads

**Status:** design, for discussion, 2026-07-27. Describes the system **as built** (the
agents-as-packs transition, Stage A–D, landed `1a2df8d`). Written in response to:
*"write me a design doc for discussion showing exactly how packs are specified and load,
and include a couple worked examples."*

**Audience:** anyone about to author a pack, debug one, or change the load path. §8 is the
discussion part — the seams that are currently underspecified.

**Reads with:** [packs-and-the-prism.md](packs-and-the-prism.md) (the pre-implementation
sketch that argued the shape; conceptual, not a description of what shipped),
[third-party-pack-logic.md](third-party-pack-logic.md) (why a pack cannot ship Go),
[composed-file-permissions.md](composed-file-permissions.md) (the posture rules the render
obeys), [jail-home.md](jail-home.md) (the mount layout the persistence tiers live in).

**Authorities this doc summarizes rather than replaces:** the manifest schema is
`internal/packdecl/packdecl.go` (its doc comments *are* the reference); the config key is
`yolo config-ref`; the surface schema is `internal/agentcfg/manifest/`.

---

## 0. The one-sentence version

A pack is **a directory of content plus an optional `pack.json` that declares paths**; the
host resolves which packs are selected, stages their trees, and mounts them; the jail
renders whatever the mounted manifests declare, with **no switch on any tool name**
anywhere in either half.

Everything else in this doc is that sentence with the details filled in.

Two framings worth keeping in mind because most surprises reduce to one of them:

1. **Core does not know what an agent is.** There is no agent registry, no `agents` config
   key, no `YOLO_AGENTS`. `packs` is the only list. The six packs yolo ships
   (`claude`, `copilot`, `opencode`, `pi`, `codex`, `agy`) are ordinary packs whose content
   happens to be baked into the binary.
2. **The mount is the filter.** The jail renders *every* pack it finds under
   `YOLO_PACK_ROOT`. Selection is enforced by staging only the selected packs into that
   tree — not by filtering later.

---

## 1. What a pack IS on disk

A directory. Nothing in it is mandatory.

```
my-pack/
├── pack.json          # optional — the manifest (§2)
├── AGENTS.md          # optional — prose appended to every briefing
└── skills/            # optional — Agent Skills, one dir each
    └── rust-review/
        └── SKILL.md
```

A pack with only `skills/` and an `AGENTS.md` **needs no manifest at all**, and that is the
common case for a shared-conventions pack. `yolo pack init` scaffolds exactly those two
things and no `pack.json`, which is deliberate: the zero-ceremony end has to stay
zero-ceremony, or the mechanism only gets used by people who ship agents.

`AGENTS.md` and `CLAUDE.md` are both accepted at the pack root (`readPackBriefing`) — both
names are in the wild and an author shouldn't have to know which one yolo happens to read.

Content rules, enforced by `internal/packstage` at staging time and **fatal, not skipped**:

| Rule | Why an error rather than a skip |
|---|---|
| A file with any **exec bit** is refused unless the entry sets `allow_exec` | A pack is content; an executable arriving through a content channel is a different trust question. Silently dropping the one file an author cared about is worse than failing. |
| A **symlink escaping the pack root** is refused, never dereferenced | A pack comes from someone else's repo. `ln -s ~/.ssh/id_ed25519 skills/innocuous.md` must not exfiltrate a key into a mounted tree. |
| Staging **clears the dir's contents, never the dir** | A running jail's bind mount captured that inode; recreating the dir silently detaches the mount. |

(Skills staging *does* dereference symlinks, deliberately — its source is the user's own
home. Different source, different rule.)

---

## 2. The manifest: `pack.json`

Schema: `internal/packdecl/packdecl.go`. **Every field is optional.** Decoding is strict
(`DisallowUnknownFields`) and reports *every* problem rather than the first, so fixing a
manifest is one edit-check cycle instead of one per typo.

```jsonc
{
  "name": "my-pack",                    // default name; the config entry may override
  "description": "one line for `yolo pack ls`",

  "install":     { … },                 // §2.1 — a program the pack wants on PATH
  "mounts":      [ … ],                 // §2.2 — the pack's own files, mounted :ro
  "writableDirs":[ … ],                 // §2.3 — per-workspace state
  "sharedDirs":  [ … ],                 // §2.3 — machine-global state
  "hostFiles":   [ … ],                 // §2.4 — the credential boundary
  "surfaces":    [ … ],                 // §3   — composed config files
  "launchFlags": { … },                 // §2.5
  "flagAliases": { … },                 // §2.5
  "hooks":       [ … ],                 // §2.6 — named requests, never scripts
  "retireMiseTools": [ … ]              // cleanup for a tool once installed via mise
}
```

### 2.0 Every path is relative, and that is a security property

`appendPathProblems` rejects three things in *every* path field:

- an **absolute** path — each path is relative to the pack root, the jail home, or the host
  home, and a pack must not reach outside whichever one it was handed;
- a `..` **segment** — a fetched pack could otherwise name `../../etc/shadow`;
- a `:` — the container runtime would parse it as a mount-option separator, silently turning
  part of the path into a flag.

`TestEveryHostHomeReadIsHomeRelative` pins this across every shipped pack, walking all
three host-reading fields rather than just `hostFiles` — collapsing the boundary to one
field is the exact mistake it was written to prevent.

### 2.1 `install` — a declaration, not a command

```jsonc
"install": { "kind": "npm",    "bin": "opencode", "package": "opencode-ai", "flags": ["--ignore-scripts"] }
"install": { "kind": "native", "bin": "claude",   "installerUrl": "https://claude.ai/install.sh" }
```

`kind` is `npm` or `native`. `bin` is both the binary name on PATH and the **launcher
filename** in `~/.yolo-shims/`. Core decides how — and whether — to honor it.

Nothing is installed at boot. Core writes one **lazy launcher** per install declaration into
`~/.yolo-shims/<bin>`, and the **first invocation** of that binary installs it. This is by
design: installing every declared binary on every boot would add minutes to a jail start
(agy alone is 189 MB), and it means changing `packs` needs no image rebuild. A stamp file
(`~/.cache/yolo-agent-stamps/<bin>.stamp`) throttles update checks to hourly.

Nothing in that mechanism knows the binary is a coding agent — it is *a program a pack asked
for*, and the launcher is generated from `kind`/`bin`/`package`/`installerUrl` alone. The Go
identifiers still say otherwise (`GenerateAgentLaunchers`, `yolo-agent-stamps`), which is
naming residue from the deleted registry rather than a concept core retained; see §8.7.

`installerUrl` is the sharpest thing a manifest can name: a URL whose contents run as a
shell script. It is origin-gated (§4). The launcher downloads to a temp file, sniffs for
markup, and refuses to hand a web page to bash — because a moved installer endpoint
typically keeps answering `200` with a sign-in page, and piping that into bash produces
three error messages, none of which names the wrong URL. See
`internal/entrypoint/nativelauncher_test.go` for the reduced case.

### 2.2 `mounts` — the pack's own content

```jsonc
"mounts": [
  { "from": "AGENTS.md", "to": ".claude/CLAUDE.md", "hostOverlay": ".claude/CLAUDE.md" },
  { "from": "skills",    "to": ".claude/skills" }
]
```

Three fields, **three different coordinate systems**, which is worth stating explicitly
because they are all bare relative paths and nothing in the syntax distinguishes them:

| Field | Relative to | Resolved | Why that root |
|---|---|---|---|
| `from` | the **pack** root | host, at staging | It names the pack's own content |
| `to` | the **jail** home | host, as a mount dest | It names where the tool will look, inside the container |
| `hostOverlay` | the **host** home | host, at staging | It names the user's *own* copy of the same file, which lives in their real home |

`hostOverlay` is host-home-relative for the reason you'd guess, and it is worth being precise
about it: the file it names is the user's personal, pre-existing version — their own
`~/.claude/CLAUDE.md` on the host machine — and yolo never mounts the host home wholesale, so
a path is the only way to reach one. Its content is **prepended** to the staged file, which is
what makes "your own AGENTS.md first, then the pack's" work. Because it reads the host home it
is origin-gated (§4).

Note that in every shipped pack `to` and `hostOverlay` are the **same string**
(`.claude/CLAUDE.md`, `.copilot/AGENTS.md`, …) while meaning two different files on two
different filesystems. That is not redundancy: a tool reads its briefing from one path
regardless of who wrote it, so the jail destination and the host source naturally coincide.
They are separate fields because nothing *requires* them to.

Unlike `hostFiles` (§2.4), a `hostOverlay` is **not a mount**. It is read on the host and
baked into the staged content, so the jail never sees the host path and there is nothing to
compose in-jail.

#### The two special `from` values

**A `from` of `AGENTS.md`/`CLAUDE.md` or `skills` does not mean "mount my file there".** It
means "put the *accumulated* briefing / skills corpus there", and the accumulation spans
every pack in the jail:

| `from` | What actually lands at `to` |
|---|---|
| `AGENTS.md` / `CLAUDE.md` | `isBriefingMount` → **one composed document**: core's generated briefing + config `agents_md_extra` + *every* pack's root `AGENTS.md`, each under a `<!-- from pack: NAME -->` header |
| `skills` | `packSkillTargets` → **one merged directory**: yolo's built-in skills < *every* pack's `skills/` < the user's own `~/<to>` skills tree |
| anything else | **nothing at all** — no mount, no warning (see §8.2) |

Both destinations are mounted `:ro`.

So a pack that declares `{"from": "AGENTS.md", "to": ".claude/CLAUDE.md"}` is not publishing
its own prose to that path — it is **subscribing** to the briefing corpus and saying where its
tool reads one. Its *own* prose went into that corpus by virtue of existing at the pack root,
which is a separate act it did not declare.

That producer/consumer split is the real structure here, and it is currently implicit on both
sides — contributing happens by filename convention, consuming happens through a `mounts`
entry whose other meaning is something else entirely. §8.2 takes up whether to name it.

### 2.3 `writableDirs` vs `sharedDirs` — the two persistence tiers

```jsonc
"writableDirs": [".claude"],                      // per-workspace
"sharedDirs":   [".claude-shared-credentials"]    // machine-global
```

Home in the jail is `GlobalHome` bind-mounted **`:ro`** at `/home/agent`. A writable dir is a
rw bind nested inside it. The backing store is what distinguishes the tiers:

| Tier | Backed by | Meaning |
|---|---|---|
| `writableDirs` | `<workspace>/.yolo/home/<dir>` | Two workspaces get independent state. The default, and right for almost everything. |
| `sharedDirs` | `paths.GlobalHome()/<dir>` | One copy for every jail on the machine. **Leaks between workspaces by design.** |

`sharedDirs` is reserved for identity/credential state, where re-authenticating in every
workspace would be *wrong behavior* rather than an inconvenience. Exactly **one** entry
exists across all six shipped packs, and `TestMachineGlobalTierStaysNarrow` fails if a
second appears — not to forbid it, but to make adding one a decision rather than drift.

Two halves are needed and forgetting either fails: `prepareWsState` creates the **backing
dir** before the container starts (podman refuses to start on a missing bind source), and
`podmanBaseMounts`/the pack loop emits the **mount**.

### 2.4 `hostFiles` — the credential boundary

```jsonc
"hostFiles": [ { "from": ".claude/settings.json", "to": "host-claude/settings.json" } ]
```

`from` is host-home-relative; `to` is under `/ctx`. An empty `to` defaults to
`/ctx/host-<pack>/<basename>`. Mounted **`:ro`**.

This is how `claude` and `pi` compose the user's *own* settings into the jail. It is
origin-gated (§4), and a refusal is **printed, never silent** — a pack not getting what it
asked for changes what the jail contains.

`HostFileConflicts` catches two grants landing on the same `/ctx` path, because one would
silently shadow the other. `TestHostFileGrantsAreExactlyTwoSettingsFiles` pins the whole
list — the retired `host_claude_files`/`host_pi_files` config keys let it grow silently, and
that is why they were removed.

### 2.5 `launchFlags` / `flagAliases`

```jsonc
"launchFlags": { "copilot": ["--yolo", "--no-auto-update"] },
"flagAliases": { "--yolo": ["-y"] }
```

Keyed by binary name. `InjectLaunchFlags` inserts them at argv index 1 (after the binary,
before the user's args), reverse-order so declaration order is preserved, without mutating
the caller's slice. An alias suppresses its flag: a user who typed `-y` does not also get
`--yolo`.

The shell alias in `.bashrc` is **derived** from the same declaration (`packAliases`), not
declared separately. It used to be an `AgentSpec.Alias` string holding a whole command
line — two places to change, and a pack shipping only one of them would get a shell alias
silently disagreeing with the launcher.

### 2.6 `hooks` — named requests, never scripts

```jsonc
"hooks": [
  { "name": "shared_credentials", "file": ".claude/.credentials.json",
    "sharedDir": ".claude-shared-credentials" },
  { "name": "per_jail_history",   "file": ".claude/history.jsonl" },
  { "name": "claude_plugins" }
]
```

A **closed set** of three (`packdecl.KnownHooks`), each implemented in core
(`internal/entrypoint/packhooks.go`):

| Hook | What core does |
|---|---|
| `shared_credentials` | Symlink `file` out to the machine-global tier, so auth survives across workspaces. Refuses a `sharedDir` the pack did not itself declare. |
| `per_jail_history` | Redirect `file` to a per-workspace copy, keyed on `sha256(YOLO_HOST_DIR)[:12]`. No-op without `YOLO_HOST_DIR`. |
| `claude_plugins` | Run the bounded (30s) plugin-install CLI call. |

**A hook is a request, not a script, and the closed set is the point.** A pack that could
supply code to run at boot would collapse the origin gate: shipping content and executing
code would become one grant, and the whole §4 distinction would be decorative. An unknown
hook name is a validation error on the host, so a misplaced field is never a declaration
that silently does nothing.

---

## 3. `surfaces` — composed config files

A surface is one generated config file plus the layer data yolo composes for it. Schema:
`internal/agentcfg/manifest`. `packdecl` keeps `surfaces` as `json.RawMessage` so it stays
free of an engine dependency; `packload.Pack.Surfaces()` decodes.

```jsonc
{
  "agent": "claude",              // owner id; namespaces the surface and its sidecars
  "name": "settings",             // (agent, name) is the unique key
  "path": "~/.claude/settings.json",
  "codec": "json",                // json | toml | lines | raw
  "mode": "stateful",             // §3.1
  "defaults": { … },              // lowest precedence, user-overridable
  "managed":  { … },              // yolo's asserted keys — wins the merge
  "computed": [ … ],              // §3.2 — the dynamic layer, as data
  "retireOnFirstRender": [ … ]    // one-shot cleanup of pre-prism sidecars
}
```

The engine composes an ordered stack, RFC 7386 merge semantics at every depth (a `null`
deletes its key, a non-object replaces wholesale):

```
defaults  <  host  <  workspace  <  overlay  <  computed  <  [lua transform]  <  managed
```

`host` is the `/ctx` file from a `hostFiles` grant. The link is **derived, not declared**:
`hostSourceFor` matches a surface's `path` basename against the pack's granted host files
and fills in `Surface.HostSource`. So `claude`'s `hostFiles` grant of
`.claude/settings.json` is what gives its `~/.claude/settings.json` surface a host layer,
with no second declaration tying them together.

`overlay` is the capture-diff that carries in-jail edits across regeneration. Sidecars live
in `<workspace>/.yolo/prism/<agent>-<name>.{last_render,overlay.json}` — per-workspace,
gitignored, and never visible to the agent.

### 3.1 The four modes

`Surface.Mode`, defaulting to `stateful`. The taxonomy is **closed** because it was derived
from every surface that exists rather than designed in advance.

| Mode | Mechanism | Use when |
|---|---|---|
| `stateful` | Compose from layers; capture in-jail edits into the sidecars | The default. A file yolo owns but a user may tweak in-jail. |
| `computed` | Compose from layers; **overwrite every boot**, discarding in-jail edits | A file regenerated wholesale from live config (MCP/LSP tables). |
| `rmw` | Read-modify-write an **agent-owned** file: assert `managed`, fill `defaults` where absent, preserve everything else, **write no sidecars** | The file holds live agent state — credentials, sessions. Composition would put a secret on the capture path, and "regenerate from layers" describes the wrong operation. |
| `unrendered` | Declared but never written | So `yolo config ls` can describe the file and so a `host_files` entry cannot claim its path. |

Declaring the mode *in the manifest* is load-bearing. It used to be a hand-maintained Go
table beside the CLI (`prismSurfaceMode`) that had to stay in sync with which render helper
the boot path happened to call, pinned only by a drift test. A surface that is **data**
cannot participate in a Go table at all.

### 3.2 `computed` — the dynamic layer, as data

This is the piece that made "a pack is data" true rather than aspirational. Before it, the
dynamic layer of each surface was a hand-written Go function per agent — and a Go function
is precisely what a third-party pack cannot ship (the `goSrc` fileset would have to contain
it at image-build time). So core had to switch on an agent name to pick a builder.

The fix is to name the **source** and the **reshape** instead of the function:

```jsonc
"computed": [
  { "from": "mcp_servers",     // closed set: mcp_servers | lsp_servers — core owns these
    "to": "mcpServers",        // where it lands ("" merges at the surface root)
    "project": { "ops": [ … ] },// per-entry reshape into this agent's dialect
    "omitEmpty": true,         // drop `to` entirely when the table is empty
    "tombstone": false,        // emit null at `to`, DELETING a lower layer's value
    "reconcile": false,        // rmw only: managed dynamic table (see below)
    "flags": [ … ] }           // conditional individual keys
]
```

The source set is closed on purpose: each name is something *core* knows how to produce
(an MCP server is config, not an agent concept), so an unknown name is a typo that would
otherwise yield a silently empty layer — the failure mode that reads as "my MCP servers
stopped working" with nothing to grep for.

Four distinctions in here are easy to conflate and each exists because a real surface needs
exactly one side of it:

- **`omitEmpty` vs `tombstone`.** Omitting a key leaves a host-provided block **intact**; a
  tombstone **removes** it. `opencode` wants the omission (an empty `mcp` block is noise);
  `claude`'s `settings.json` wants the removal (MCP servers belong in `.claude.json`, so a
  host `settings.json` carrying one must be stripped).
- **`flags`.** "Assert a key *because of* something in a live table", where the key's name
  has nothing to do with the table's keys — so no rename, suffix, or projection reaches it.
  `whenPresent: "go"` holds when the table has a `go` entry; `whenAny: true` when it has
  any. A **false** condition emits a tombstone, not an omission: the key may be sitting in
  the user's host file from a boot when that LSP *was* configured, and leaving it would
  enable a plugin that is no longer installed.
- **`reconcile`.** `rmw` only. Plain RMW asserts a *static* key set, so it cannot express a
  **removal** from a dynamic table: with no record of what yolo asserted last boot, "the
  agent added this server" and "yolo added it and config has since dropped it" look
  identical on disk. `reconcile` keeps that record in a
  `<agent>-<name>-<key>.managed.json` sidecar. A composed surface needs nothing like this —
  its `last_render` sidecar already *is* the record.
- **`project`.** Four ops (`copy`, `fold`, `inject`, `default`), plus `keySuffix`. Derived
  from the five real projections that existed, and confirmed sufficient for all of them.

### 3.3 `${workspace}`

`agentcfg.WorkspacePlaceholder` is substituted into `defaults`/`managed` **map keys** at
render time. A pack cannot ship a jail-specific absolute path, so this is the seam that let
`claude`'s `projects` table — keyed by absolute workspace path — move out of Go unchanged.
Keys only, not values: extending to values would be a templating language rather than one
named seam.

---

## 4. The origin gate

`config.PackEntry.Origin()` classifies where a pack's **content** came from, and that
decides what the content is allowed to declare.

| Origin | Source | May declare host access? |
|---|---|---|
| `OriginEmbedded` | Ships inside the yolo binary; reviewed with the release | **Yes** — a declaration from it *is* a yolo-shipped decision |
| `OriginLocal` | `file:///abs/path` on this machine | **Yes** — the user authored or vendored it, and can read those files without yolo's help |
| `OriginFetched` | `git+ssh://…`, `git+https://…` | **No** |

`MayGrantHostFiles()` is `Origin() != OriginFetched`. It gates the three declarations
`NeedsHostAccess()` collects — and they are collected in **one** predicate so a caller
cannot check two of the three and believe it covered the boundary:

1. `hostFiles` — reads the host home
2. `mounts[].hostOverlay` — reads the host home
3. `install.installerUrl` — runs a fetched script

(`install` of `kind: "npm"` is *not* gated: an npm package name is not a host read.)

**The rationale in one line: installing a third-party pack approves distributing content,
not handing that repository your host config.** `TestFetchedPacksGetNoHostAccess` asserts
*both* directions from one declaration — a test that only checked the refusal would pass on
an implementation that refused everything.

---

## 5. How a pack is SELECTED: the `packs` config key

```jsonc
// ~/.config/yolo-jail/config.jsonc
"packs": [
  "claude",                                                    // an embedded pack, by bare name
  "file:///home/me/code/acme/tools/agent-pack",                // local
  "git+ssh://git@github.com/org/repo//subdir?ref=main",        // fetched
  { "source": "file:///home/me/packs/big", "name": "big",      // object form
    "only": ["skills/rust-*"], "exclude": ["**/draft.md"], "allow_exec": false }
]
```

### 5.1 USER SCOPE ONLY — and it is inexpressible, not merely forbidden

`LoadPacks` reads `paths.UserConfigPath()` **directly**. It does not go through the
user-then-workspace merge, so a workspace `packs` key has nowhere to land. `validatePacks`
additionally hard-errors on one, naming the fix.

The reason: a workspace config travels with the repo and is **agent-editable**, so it must
not decide what content — skills and briefing prose an agent then *follows* — enters the
jail. A repo that wants to configure its own agents can just commit the files; it already
has a git repo.

The lockfile lives beside the user config (`~/.config/yolo-jail/packs.lock.json`) for the
same reason: a repo-committed lock would describe something the repo cannot influence.

### 5.2 Nothing is active by default

An empty `packs` list yields a jail with **no coding agent**, and says so at launch
(`run.warnIfNoPacks`). That is the honest default: six silently-active agent packs would
contradict the very warning telling the user they had none — a contradiction they'd only
discover by looking in `~/.yolo-shims`. `TestNoPacksMeansNoDeclarations` pins that every
union tolerates an empty set without inventing anything.

### 5.3 Addresses

`internal/packsrc`, Terraform's grammar in substance: `//` splits the repository from the
in-repo path, query params follow. `git+scheme://` (nix/pip style) rather than Terraform's
`git::` — parses identically, reads better.

**`?ref=` is mandatory for git sources.** An unpinned float is the top-ranked anti-pattern
in the precedent survey and the specific way kustomize bases and chezmoi externals go
wrong: the pack you audited is not the pack you get next week. Requiring it means a moving
target has to be *asked for by name* (`?ref=main`), not acquired by omission.

A ref and a commit answer different questions, so there is also a lockfile: `?ref=main`
records what you **asked for**, the lock records what you **got**.

A bare name that is not an embedded pack is an error naming the six, with a `pathShaped`
check so `./my-pack` gets told about `file://` rather than "unknown pack".

### 5.4 Fetching happens in `yolo pack install`. Never at launch.

`packRoot` resolves from the store and **never** fetches (C5). A jail start must not depend
on a reachable git server, and a missing pin must be a clear error pointing at
`yolo pack install` — rather than a surprise network call mid-boot, or worse, a 30-second
askpass hang that reads as yolo wedging.

Tooling: `yolo pack init | lint | ls | explain | install | update | status`. `lint` and
`explain` run the **real** stager (`internal/packstage`) rather than reimplementing its
rules — a linter that disagrees with the stager is worse than no linter.

---

## 6. The load path, end to end

### 6.1 Host side, before the container exists

Everything here runs on the host because that is where the inputs live: the pack store, the
user config, and git credentials. The jail never reads config and never fetches.

```
yolo run
 └─ run.go:196  refreshJailBriefings(cname, cfg, rt)
     ├─ stagePacks(cname)                                    ← internal/cli/run/packs.go
     │   ├─ config.LoadPacks(warn)                           ← user config only
     │   ├─ MaterializeEmbedded(packs.FS, scratch)           ← ALL six, into a temp dir
     │   ├─ RemoveAll(<staging>/_official)                   ← a DROPPED pack must stop mounting
     │   ├─ for each SELECTED embedded entry:
     │   │     copyTree(scratch/<name> → <staging>/_official/<name>)   (files 0o644)
     │   │     packload.LoadDir(dest, name, mayAccessHost=true)
     │   └─ for each configured entry:
     │         packRoot(entry)                               ← store only, offline
     │         packstage.Stage{Root, Dest, Only, Exclude, AllowExec}
     │         warn if 0 files staged                        ← almost always a filter typo
     │         packload.LoadDir(dest, name, entry.MayGrantHostFiles())
     │         print every refused declaration
     ├─ PrepareSkills  (built-ins < pack skills < the user's own)
     └─ write one composed briefing per pack-declared briefing mount
                 (prepending the host overlay, for a pack whose origin permits it)
 └─ prepareWsState(cfg, loadedPacks)                         ← backing dirs BEFORE the :ro bind
 └─ assemble()
     ├─ -v <ws>/.yolo/home/<d>:/home/agent/<d>               ← per writableDirs
     ├─ -v GlobalHome/<d>:/home/agent/<d>                    ← per sharedDirs
     ├─ -v <staging>/skills-<pack>:/home/agent/<dest>:ro     ← per skills mount
     ├─ -v <staging>/briefing-<pack>.md:/home/agent/<dest>:ro
     ├─ -v <staging>:/ctx/packs:ro  +  -e YOLO_PACK_ROOT=/ctx/packs
     └─ -v ~/<hostFile.from>:/ctx/<hostFile.to>:ro           ← per granted host file
```

Three details are load-bearing and each has drawn blood:

- **Embedded packs are materialized to a scratch dir first**, then only the selected ones
  are copied into the mounted tree. Staging all six and filtering later renders packs
  nobody asked for. That failed *loudly* (unselected packs' config dirs aren't writable, so
  A12 halted the boot) — but "the jail refuses to start because of a pack you did not ask
  for" is not a fix, it is the same bug with a better error message.
- **`_official` is cleared**, or a pack dropped from config keeps rendering.
- **Staging runs before skills preparation and mount assembly.** `stagePacks` sets
  `agents.SetPackSkillDirs`/`SetPackSkillTargets` as a side effect, which `PrepareSkills`
  consumes on the next call. Ordering is not stylistic.

`/ctx/packs` is `:ro`, and that too is load-bearing rather than tidy: a pack manifest is an
**input** to composition, and an agent that could rewrite one in-jail could grant its own
pack a host file on the next boot.

Fail-closed throughout (A12). A pack that cannot be staged is an error, not a warning — a
jail that comes up silently missing a pack the user asked for is the failure mode this
whole cluster of work exists to remove. A broken *embedded* pack is fatal as a yolo bug,
since the user can do nothing about it.

### 6.2 Jail side, at boot

```
yolo-entrypoint
 ├─ boot.go:406  generate_shims                    (blocked tools; unconditional)
 ├─ boot.go:408  generate_agent_launchers          → LoadJailPacks → HonoredInstall
 ├─ boot.go:425  generate_bashrc                   → packAliases (derived from launchFlags)
 ├─ boot.go:449  LoadJailPacks(e)                  ← walks YOLO_PACK_ROOT
 ├─ boot.go:456  ConfigurePackSurfaces(e, packs)   ← one genStep per surface
 └─ boot.go:457  RunPackHooks(e, packs)            ← one genStep per hook
```

`LoadJailPacks` walks two levels — `<root>/_official/<name>` and `<root>/<slug>` — and
loads every pack with **`mayAccessHost = true`**. That is not a hole: the host already
applied the gate by deciding which `/ctx` mounts exist. The jail has no config and no way
to re-derive origin, so re-deciding there would mean duplicating the trust model in the
half that has less information.

Also: a parse failure **in-jail** is an error, not a warning. On the host a malformed
manifest is a user or author mistake; by the time it is in `/ctx/packs` it passed host
validation, so failing to parse means the mounted tree is corrupt.

`ConfigurePackSurfaces` renders every surface in **one loop with no switch on any tool
name**:

```go
for _, p := range packs {
    surfaces, problems := p.Surfaces()
    for _, prob := range problems { genStep(e, "pack_"+p.Name+"_surfaces", …fail…) }
    for _, s := range surfaces {
        genStep(e, "configure_"+s.Agent+"_"+s.Name, func() error {
            return renderDeclaredSurface(e, s, tables)   // mkdir, BuildComputed, dispatch on mode
        })
    }
}
```

`liveTables(e)` supplies `mcp_servers` and `lsp_servers` — **core owns the sources**, a pack
only picks from them.

Every step goes through `genStep`, which is the A12 fail-closed collector: a failure is
fatal **but collected**, so one boot reports every problem instead of stopping at the first
and making the user iterate.

### 6.3 What the jail deliberately does NOT do

- read config (it gets a generated snapshot; `validate.go` warns rather than errors on
  `agents` there, because in-jail the config is not the user's to fix)
- fetch anything
- know which packs are embedded vs configured
- switch on a tool name, anywhere

### 6.4 The reservation lists are NOT selection-gated

`packload.Embedded*` (`EmbeddedWritableDirs`, `EmbeddedSharedDirs`,
`EmbeddedRetireMiseTools`) is the union over every pack yolo **ships**, regardless of what
this jail loaded. It feeds: which home roots a `host_files` entry may write into, which path
segments `writable_home_dirs` may not claim, which `GlobalHome` subdirs to create.

Gating these on the loaded packs is a mistake that has been made before: a `host_files`
entry could then claim a path that a pack added tomorrow needs, and the collision would
surface as a mount conflict with no obvious cause.

Bounded honestly: **embedded only.** A configured pack's writable dir is *not* reserved, so
a user declaring a `host_files` entry at that path gets a conflict rather than a clear
error. Reading the pack store inside config validation would put a filesystem dependency —
and a new failure mode — into a function that only inspects config values.

---

## 7. Worked examples

### 7.1 `claude` — the full-featured case

`packs/claude/pack.json` uses nearly every field, so it is the useful one to read whole.

```jsonc
{
  "name": "claude",
  "install": { "kind": "native", "bin": "claude",
               "installerUrl": "https://claude.ai/install.sh" },
  "launchFlags": { "claude": ["--dangerously-skip-permissions"] },

  "mounts": [
    { "from": "AGENTS.md", "to": ".claude/CLAUDE.md", "hostOverlay": ".claude/CLAUDE.md" },
    { "from": "skills",    "to": ".claude/skills" }
  ],

  "writableDirs": [".claude"],
  "sharedDirs":   [".claude-shared-credentials"],
  "hostFiles":    [ { "from": ".claude/settings.json", "to": "host-claude/settings.json" } ],

  "hooks": [
    { "name": "shared_credentials", "file": ".claude/.credentials.json",
      "sharedDir": ".claude-shared-credentials" },
    { "name": "per_jail_history",   "file": ".claude/history.jsonl" },
    { "name": "claude_plugins" }
  ],

  "retireMiseTools": ["\"npm:@anthropic-ai/claude-code\""],
  "surfaces": [ /* two — below */ ]
}
```

**What happens, in order:**

1. **Host.** `packs: ["claude"]` selects it. `MayGrantHostFiles()` is true (embedded), so
   all three gated declarations are honored.
2. **Host.** `.claude/settings.json` is mounted at `/ctx/host-claude/settings.json:ro`;
   `~/.claude/CLAUDE.md` is prepended to the composed briefing; `<ws>/.yolo/home/claude`
   backs `~/.claude`; `GlobalHome/.claude-shared-credentials` is mounted rw.
3. **Jail.** `~/.yolo-shims/claude` is written as a lazy native launcher; `.bashrc` gets
   `alias claude='claude --dangerously-skip-permissions'`, derived from `launchFlags`.
4. **Jail.** Two surfaces render.
5. **Jail.** Three hooks run: credentials symlink out to the machine-global tier, history
   redirects per-workspace, plugins install.

**Surface 1 — `claude/config` at `~/.claude.json`, mode `rmw`:**

```jsonc
{ "agent": "claude", "name": "config", "path": "~/.claude.json", "codec": "json",
  "mode": "rmw",
  "defaults": { "projects": { "${workspace}": { "hasTrustDialogAccepted": true } } },
  "managed":  { "projects": { "${workspace}": { "enableAllProjectMcpServers": true } } },
  "computed": [ { "from": "mcp_servers", "to": "mcpServers", "reconcile": true } ] }
```

`rmw` because this file holds **live agent state** — OAuth account info, session data.
Composing it would put a secret on the capture path, and "regenerate from layers" describes
the wrong operation. So: assert `managed`, fill `defaults` where absent, preserve everything
else, write no sidecars.

`reconcile: true` is what makes the MCP table removable anyway. The
`claude-config-mcpServers.managed.json` sidecar records the names yolo asserted last boot;
next boot those are removed and the current table added, so a server the *agent* added
survives while one yolo added and config has since dropped goes away. This generalizes
exactly what the hand-written `.claude.json` writer did with
`yolo-managed-mcp-servers.json`, which is what let that writer be deleted.

`${workspace}` keys the `projects` table by the real workspace path. A literal
`/workspace` was a latent bug on any run whose workspace is elsewhere.

**Surface 2 — `claude/settings` at `~/.claude/settings.json`, mode `stateful`:**

```jsonc
{ "agent": "claude", "name": "settings", "path": "~/.claude/settings.json", "codec": "json",
  "managed": { "permissions": { "allow": [], "deny": [], "defaultMode": "acceptEdits",
                                "additionalDirectories": ["/"] },
               "preferences": { "autoUpdaterStatus": "disabled" },
               "skipDangerousModePermissionPrompt": true },
  "computed": [
    { "to": "mcpServers", "tombstone": true },
    { "from": "lsp_servers", "to": "enabledPlugins", "flags": [
        { "key": "pyright-lsp@claude-plugins-official",    "value": true, "whenPresent": "python" },
        { "key": "typescript-lsp@claude-plugins-official", "value": true, "whenPresent": "typescript" },
        { "key": "gopls-lsp@claude-plugins-official",      "value": true, "whenPresent": "go" } ] },
    { "from": "lsp_servers", "to": "env", "flags": [
        { "key": "ENABLE_LSP_TOOL", "value": "1", "whenAny": true } ] } ],
  "retireOnFirstRender": ["yolo-host-synced-settings.json"] }
```

This one has a **host layer** — derived, because the `hostFiles` grant's basename
(`settings.json`) matches this surface's path basename. So the user's own settings compose
in above `defaults` and below `managed`.

Three things worth noticing:

- The `tombstone` on `mcpServers` **deletes** it. MCP servers belong in `.claude.json`, so a
  host `settings.json` carrying one must be stripped — `omitEmpty` would leave it intact,
  which is the conflation §3.2 exists to prevent.
- `permissions.allow` is `[]` with `defaultMode: acceptEdits`. This is **not** an allowlist
  mechanism; YOLO mode is `--dangerously-skip-permissions` plus `IS_SANDBOX=1`.
- `retireOnFirstRender` deletes one pre-prism sidecar, once. Transitional by nature: once no
  jail can still be carrying one, the whole field goes. It is data rather than Go because it
  was the last thing in the per-agent render functions besides the computed layer, and
  leaving one in Go would keep those functions alive for a one-shot cleanup.

### 7.2 `opencode` — the projection case

Small, and it shows the reshape doing real work.

```jsonc
{
  "name": "opencode",
  "install": { "kind": "npm", "bin": "opencode", "package": "opencode-ai" },
  "mounts": [ { "from": "AGENTS.md", "to": ".config/opencode/AGENTS.md",
                "hostOverlay": ".config/opencode/AGENTS.md" } ],
  "surfaces": [ {
    "agent": "opencode", "name": "config", "codec": "json",
    "path": "~/.config/opencode/opencode.json",
    "defaults": { "$schema": "https://opencode.ai/config.json" },
    "managed":  { "permission": "allow" },
    "computed": [ { "from": "mcp_servers", "to": "mcp", "omitEmpty": true,
      "project": { "ops": [
        { "fold":   { "froms": ["command", "args"], "to": "command" } },
        { "copy":   { "from": "env", "to": "environment", "omitEmpty": true } },
        { "inject": { "to": "type",    "value": "local" } },
        { "inject": { "to": "enabled", "value": true } } ] } } ],
    "retireOnFirstRender": ["yolo-managed-mcp-servers.json"] } ]
}
```

The canonical MCP entry yolo holds is `{command, args, env}`. opencode wants
`{command: [argv…], environment, type, enabled}`. The four ops get there:

```
{ "command": "npx", "args": ["-y", "srv"], "env": { "TOKEN": "x" } }
   fold command+args → command      { "command": ["npx","-y","srv"] }
   copy env → environment           + "environment": { "TOKEN": "x" }
   inject type = "local"            + "type": "local"
   inject enabled = true            + "enabled": true
```

`omitEmpty: true` on the outer declaration and on the `env` copy, because there is no host
layer to protect here and an empty `mcp` block (or `environment` key) is just noise. Contrast
`claude`'s tombstone: same-looking choice, opposite semantics, driven by whether a lower
layer exists.

Note also what's **absent**: no `writableDirs`. opencode's config lives under `~/.config`,
which is already a rw bind in the base mount set. Declaring one anyway would be harmless but
misleading.

### 7.3 A minimal third-party pack — the zero-ceremony end

House conventions, shared across every project, no agent involved:

```
acme-conventions/
├── AGENTS.md
└── skills/
    ├── acme-rust-review/SKILL.md
    └── acme-sql-style/SKILL.md
```

No `pack.json`. Consumed with:

```jsonc
"packs": ["claude", "git+ssh://git@github.com/acme/mono//tools/agent-conventions?ref=v2.1.0"]
```

Then `yolo pack install` (which writes the lock), and launch.

What it gets: its `AGENTS.md` appended to every briefing under a
`<!-- from pack: acme-conventions -->` header, and its two skills merged into **each**
selected pack's skills destination — so `claude` finds them at `~/.claude/skills`, and a
jail that also selected `copilot` finds them at `~/.copilot/skills`. Skills precedence is
built-ins < pack skills < the user's own, so a pack may override a yolo built-in but never a
skill the user wrote.

What it does **not** get, because it is fetched: any host file, any `hostOverlay`, any
curl-piped installer. It declares none of those, so nothing is refused and nothing is
printed. Had it declared one, the refusal would be printed at launch — not silent.

Narrowing per project is not available (`packs` is user scope). The intended move is
`only`/`exclude` on the entry, or a second entry pointing at a different subpath:

```jsonc
{ "source": "git+ssh://git@github.com/acme/mono//tools/agent-conventions?ref=v2.1.0",
  "name": "acme", "only": ["skills/acme-rust-*", "AGENTS.md"] }
```

A `only`/`exclude` pair that matches nothing warns with the excluded count, because
otherwise the user just sees a pack that "does nothing".

---

## 8. Open questions — the discussion part

Each of these is a real gap in what shipped, not a hypothetical.

### 8.1 `install.bin` has no collision detection

`hostFiles` has `HostFileConflicts`, which catches two grants landing on one `/ctx` path and
explains the shadowing. `install.bin` has **no equivalent**. Two packs declaring
`"bin": "claude"` both write `~/.yolo-shims/claude`, last write wins, silently — and since
`GenerateAgentLaunchers` iterates packs in load order, which one wins depends on config
order. The blocked-tool shims *are* protected (`pathExists` skips, so a shim is never
overwritten by a launcher), but pack-vs-pack is unguarded.

Not urgent for the six shipped packs, which have distinct bins. It matters the moment a
third-party pack ships a tool with a common name. **Proposal:** a `PackInstallConflicts`
mirroring `HostFileConflicts`, fatal at staging, naming both packs and the bin.

### 8.2 `mounts` conflates two operations, and the interesting one is unnamed

The immediate defect is small: the schema says "stages one of the pack's own files or
directories and mounts it read-only", but only two `from` values do anything. A pack
declaring `{"from": "templates", "to": ".config/mytool/templates"}` gets **silently
nothing** — no mount, no warning. That is precisely the "a declaration that silently does
nothing" failure mode `DisallowUnknownFields` exists to prevent, one level up.

The interesting part is *why* those two are special, because it points at a mechanism the
codebase already has and has not generalized.

#### The shape that's actually there

`mounts` is doing two unrelated jobs under one key:

1. **Mount my file at that path.** Never implemented.
2. **Put the accumulated CORPUS at that path.** Implemented twice, ad hoc, once for briefing
   prose and once for skills.

Job 2 is a **producer/consumer fan-in**: many packs contribute, one pack consumes and names
the destination + format. And core already has a first-class version of exactly that pattern
— the `computed` layer (§3.2):

```jsonc
"computed": [ { "from": "mcp_servers", "to": "mcpServers", "project": { … } } ]
```

`mcp_servers` is a **named collection** core owns. A surface says "give me that collection,
reshaped into my dialect, at this key". The source set is closed, so a typo is an error
rather than a silently empty layer.

Briefing and skills are the same shape with none of the machinery: contribution is by
**filename convention** at the pack root (`AGENTS.md`, `skills/`), consumption is a `mounts`
entry, the merge order is hardcoded in Go, and the "projection" is a fixed concatenation or
directory copy. Three mechanisms for one idea.

#### The proposal: named collections as the one fan-in

Make the collection a first-class thing packs both **contribute to** and **consume from**,
and let `computed.from`'s closed source set carry them:

```jsonc
// any pack — CONTRIBUTE
"contributes": [
  { "to": "briefing", "from": "AGENTS.md" },              // was: filename convention
  { "to": "skills",   "from": "skills" },                 // was: filename convention
  { "to": "mcp_servers", "from": "mcp/servers.json" }      // new: a pack can ship MCP servers
]

// an agent pack — CONSUME
"surfaces": [ { "agent": "claude", "name": "settings", …,
                "computed": [ { "from": "mcp_servers", "to": "mcpServers" } ] } ],
"consumes": [
  { "from": "briefing", "to": ".claude/CLAUDE.md", "codec": "markdown",
    "hostOverlay": ".claude/CLAUDE.md" },
  { "from": "skills",   "to": ".claude/skills",    "codec": "skilldir" }
]
```

What this buys, in rough order of value:

- **The two ad-hoc cases become instances of one mechanism**, so a third collection (prompt
  templates, lint configs, command definitions, subagent definitions) is a pack file rather
  than a Go change — the same bar the rest of the pack system already meets.
- **An unrecognized name is an error**, inheriting the closed-set property that already makes
  `computed.from` safe. That fixes the silent-nothing defect as a side effect.
- **Contribution stops being a filename convention.** Today a pack contributes prose by
  happening to have `AGENTS.md` at its root — invisible in the manifest, undiscoverable from
  `yolo pack ls`, and un-narrowable (`only: ["skills/**"]` silently drops the briefing too).
- **A non-agent pack can contribute to an agent-facing table.** `mcp_servers` is core-owned
  today, so a shared team pack cannot ship an MCP server; it has to tell people to edit their
  own config. That is a real gap and this closes it.

#### The four questions it has to answer

Worth working through before committing, because each has a wrong answer that looks fine:

1. **Where does the format knowledge live?** Briefing is markdown-with-attribution-headers;
   skills is a directory merge with three-tier precedence; MCP is a JSON table with
   projections. `computed` already solved this for tables (`project.ops`), and the
   deliberate refusal there was to invent a templating language. A directory merge is not
   expressible in `project.ops` and probably shouldn't be — which suggests **core owns a
   small closed set of collection *kinds*** (`table`, `document`, `dir`), each with fixed
   merge semantics, and a pack picks a kind rather than describing one. That keeps §8.6's
   "closed set, no executable content" posture intact.
2. **What is the merge order, and can a pack influence it?** Skills has a real answer today
   (built-ins < packs in config order < the user's own) that took thought and should survive.
   Config order is the only ordering signal a pack can't fake, so the safe rule is
   *contribution order = config order, later wins*, with **no** per-pack priority field —
   a priority number is how a shared pack quietly outranks the user's own.
3. **Does contribution cross the origin gate?** A fetched pack contributing to `briefing`
   means its prose becomes instructions an agent follows. That is already true today and is
   the *intended* semantics of installing a pack — §4 gates reading the host home, not
   shipping content. But `mcp_servers` is different in kind: an MCP server is a **command
   line the agent executes**. A fetched pack contributing one is much closer to
   `installerUrl` than to prose, and should probably be gated with it. **This is the one
   question I'd want settled first**, because getting it wrong turns a content grant into an
   execution grant — the exact collapse §2.6 refuses for hooks.
4. **Who consumes when nothing does?** A jail with a conventions pack and no agent pack has
   contributions and no consumer. Today that is silent (no briefing is written at all, since
   nothing declares a briefing mount). Under named collections it stays silent, which is
   correct — but `yolo pack ls` should be able to *say* "3 skills contributed, 0 consumers",
   because that is the same no-silent-caps rule the 0-files-staged warning already follows.

#### The narrow alternative

If named collections are too much for now: **split `mounts` into what it actually does** —
`content: { briefing: {...}, skills: {...} }` — so the manifest cannot express the
unimplemented job 1. Less capable, honest, and a smaller step. It does not close the
non-agent-pack-can't-ship-an-MCP-server gap.

**Either beats the status quo.** If neither happens soon, the minimum is making an
unrecognized `from` a validation error rather than silence.

### 8.7 The Go identifiers still say "agent"

Core no longer has an agent concept, but the names kept from the deleted registry:
`GenerateAgentLaunchers`, `~/.cache/yolo-agent-stamps/`, `nativeAgentLauncher`,
`agents.SetPackSkillDirs`, and `internal/agents` itself (now holding only skills staging,
briefing composition, loophole descriptions, and the source-tree probe).

Cosmetic, but not harmless: the invariant is "core does not know what an agent is", and a
reader checking that claim against the code finds a function named for the concept it is
supposed to have shed — which reads as the refactor being incomplete rather than the naming
being stale. A rename (`GeneratePackLaunchers`, `yolo-pack-stamps`, …) is mechanical; the
stamp-dir path change would orphan existing stamps, which is harmless (a stale stamp just
means one extra update check).

### 8.3 A native install's destination is core-fixed, not pack-declared

`nativeLauncherTemplate` hardcodes `REAL_BIN="$HOME/.local/bin/<bin>"`. A native installer
that lands its binary anywhere else produces a launcher that installs successfully and then
reports `⚠ <bin> not available`. `agy`'s installer happens to target `~/.local/bin`; the
next one might not.

The declaration is also asymmetric: `kind: "npm"` gets `$NPM_CONFIG_PREFIX/bin/<bin>`, which
npm genuinely guarantees. Native has no such guarantee. **Proposal:** an optional
`install.binPath` (home-relative, path-validated like everything else), defaulting to
`.local/bin/<bin>`.

### 8.4 Installer scripts can edit the shell environment, and only self-heal by luck

`agy`'s real installer strips yolo's `alias agy=…` from `.bashrc` and prepends
`~/.local/bin` to `PATH`. Both happen to be harmless: `GenerateBashrc` truncates in place
every boot, and `~/.yolo-shims` stays first on PATH in a new login shell. Verified in nested
jails.

But that is a property of the current boot order, not something declared or tested. A
regression test asserting "a boot restores `.bashrc` after an installer mangles it" would
turn luck into a guarantee. Worth having; cheap.

### 8.5 The reservation lists cover only embedded packs

Stated as a known bound in §6.4. A configured pack's `writableDirs` are not reserved, so a
`host_files` entry can claim that path and the user gets a mount conflict rather than an
explanation. The fix means reading the pack store during config validation — a filesystem
dependency in a pure function. **Currently judged not worth it**, but it is the kind of thing
that reads as a bug the first time someone hits it, so it deserves a better error message at
minimum.

### 8.6 Is the three-hook closed set the right size?

The closed set is well-argued (§2.6): executable pack content would collapse the origin
gate. But all three hooks exist for `claude` specifically, and two of them
(`shared_credentials`, `per_jail_history`) describe patterns any agent could want. Nothing
is wrong today. The question is what happens at hook four and five — whether the set grows
one named request at a time (fine, if each is genuinely general) or whether the pressure
should instead go into making `sharedDirs` + surfaces expressive enough that a hook isn't
needed. Worth deciding *before* the next hook, not after.

---

## 9. Cheat sheet

| Question | Answer |
|---|---|
| Where does a pack come from? | A bare name (embedded), `file://` (local), or `git+ssh://`/`git+https://` with a mandatory `?ref=` (fetched) |
| Who may select one? | The **user config only**. A workspace `packs` key is a hard error |
| What's active by default? | Nothing |
| When does fetching happen? | `yolo pack install`. Never at launch |
| What may a fetched pack NOT do? | Read the host home (`hostFiles`, `mounts[].hostOverlay`) or run a curl-piped installer |
| When is a tool installed? | On first invocation, via `~/.yolo-shims/<bin>` — not at boot |
| Where does a pack's state persist? | `<workspace>/.yolo/home/<dir>` (`writableDirs`) or `GlobalHome/<dir>` (`sharedDirs`) |
| How does a pack ship logic? | It doesn't. Declarations only: surfaces, computed sources, projection ops, named hooks |
| How is selection enforced? | By staging only selected packs into `YOLO_PACK_ROOT`. The mount is the filter |
| What happens when a pack is broken? | Fatal, but collected — one boot reports every problem (A12) |
| Which files are the authority? | `internal/packdecl/packdecl.go`, `internal/config/packs.go`, `internal/cli/run/packs.go`, `internal/packload/`, `internal/entrypoint/pack{surfaces,hooks}.go`, `internal/agentcfg/manifest/` |
