# Migrating to packs, and managing your host from yolo

**Status:** GUIDE — **spot-checked 2026-08-23**: the kind count (fifteen) and the loophole section
match the tree. **The UNRELEASED warning below is now WRONG and is corrected in place** — every verb
it lists shipped in **v0.8.0** (tagged 2026-08-13; `git show v0.8.0:internal/cli/dispatch.go` has
`describe`, `apply`, `check-deps` and `pack`). **Corrected again 2026-09-09**: two passages
claimed your host `~/.claude/settings.json` no longer composes into a jail. It does — see the
`host`-layer bullet under "Before you start" and the note opening Part 2.

> **⚠ Verify your version first.** The verbs this guide leans on — `describe`, `apply` (incl.
> `--at host`/`--sealed`), `check-deps`, the newer `pack` subcommands (`lint`'s manifest validation,
> `footprint`, `install`, `status`) and `config drift`/`dump` — are **in `v0.8.0` and later**. On an
> **older** `yolo` they fail with `unknown command` / `unknown subcommand` (exit 2), not a helpful
> message. Before following any step, confirm the verb exists: `yolo apply --help`,
> `yolo describe --help`, `yolo pack --help`. If those error, your installed `yolo` predates the
> work — reinstall.
>
> **Newer than v0.8.0: the `yolo host` namespace.** This guide spells the host render
> `yolo host apply`, which is the ergonomic form; `yolo apply --at host` is the same operation
> and is the spelling v0.8.0 has. `yolo apply --host` was **removed** — a `yolo` that still
> accepts it predates this change.
>
> *(Corrected 2026-08-23. This block said the verbs were "not in a released yolo yet", which was
> true when it was written and stopped being true at v0.8.0 — the version cutoff is the useful
> instruction, not the absence of a release.)*

> **Autonomy is a confinement policy (how `yolo host apply` stays safe).** The shipped agent
> packs (`claude`, `codex`, `agy`, `opencode`) declare the jail-bypass settings
> (`acceptEdits`, `skipDangerousModePermissionPrompt`, `additionalDirectories: ["/"]`,
> `--dangerously-skip-permissions`, …) in an `autonomy` contribution's **autonomous**
> posture — rendered only at the contained notches (`jail`/`guest`). `yolo host apply` renders
> each pack's **guarded** posture instead: permission prompts stay **on**, and the bypass
> keys never reach your real `~/.claude/settings.json`. (The permissive-by-default `pi` pack
> is the mirror image — `host` *tightens* it from auto-trust to prompt.) `yolo host apply` also
> **warns before overwriting** any existing value you set yourself. So the earlier hazard
> here — `--assert` writing jail-bypass keys onto a real machine — is fixed; you still
> review what it writes (next).

This guide takes you from "yolo just runs claude in a jail with my hand-tuned
`~/.claude/settings.json`" to "my agent environment is a **pack** I own — declared,
locked, portable — and I can render that same config onto my **real machine**, not just
into a jail."

Two journeys, in order:

1. **[Move your setup into a pack](#part-1--move-your-setup-into-a-pack)** — the thing
   every later step builds on.
2. **[Manage your host from that pack](#part-2--manage-your-host)** — render your
   config into your real `$HOME`, and check the host has the tools your packs need.

Every command here is real (not aspirational), but several are **unreleased** — see the
version banner above and verify with `--help` before you run them. Where a capability is
not built yet, this guide says so plainly rather than showing you a command that does
nothing.

> **The one-line mental model.** yolo *describes* an environment — tools, agent config,
> skills, credentials — and *confinement* is one attribute of that description. A **jail**
> is the strongest (and default) confinement; **host** is the weakest (your real machine).
> A **pack** is how you write the description down. Migrating means: stop hand-editing the
> environment, start declaring it.

---

## Before you start: what changed

- **Agents are packs.** There is no `agents` config key any more. The coding agent, its
  config, its skills — all arrive as a **pack**. yolo ships six by name (`claude`,
  `copilot`, `codex`, `opencode`, `pi`, `agy`); you add your own.
- **Nothing is active by default.** An empty config gives you a jail with a shell and no
  agent. You opt in with the `packs` key.
- **Your personal `~/.claude/settings.json` is still a composed config layer — but it is no
  longer the *durable* place to keep settings.** The shipped `claude` pack's `claude/settings`
  surface declares `"readsHost": true`; the launcher mounts your own copy of the file under
  `/ctx`, and the boot render reads it every launch as the surface's `host` layer. So a key
  you put there does reach every jail, and
  `yolo config ls` names `host` among the surface's layers. What that layer cannot do is
  travel: it is one file on one machine, it sits below every other layer, and — now that
  [`yolo host apply`](#part-2--manage-your-host) *writes* the same file — part of it is a
  render output rather than something you authored. The durable way to carry your settings
  is a **local pack** — declared, locked, and portable to every confinement level (see
  Part 2 for why this matters for host management).

  *(Corrected 2026-09-09. This bullet said the layer "is gone" and that the file "no longer
  silently composes into what yolo writes". Both were wrong, and had been since they were
  written: `TestConfigureClaudePrismComposesTheHostLayer` and
  `TestConfigureClaudePrismStripsHostMCPServers` in `internal/entrypoint` both fail if
  either half of the wiring is removed. Re-corrected 2026-09-12: the binding used to be a
  separate `reads-host` contribution matched to the surface by BASENAME, through
  `packload.hostSourceFor`. That match is gone — a surface declares its own host layer now,
  and the `/ctx` path is derived from the surface's own path — so a grant that stopped
  matching can no longer un-bind a host layer in silence. The `reads-host` KIND stays, for
  the `host_files` key below, whose entries have no mirrored twin to derive from.)*

Check where you are today:

```console
$ yolo pack ls          # what packs are configured (probably just a built-in agent)
$ yolo describe         # the resolved environment: confinement, packs, a description hash
```

---

## Part 1 — Move your setup into a pack

### Step 1: scaffold a pack

A pack is just a directory. `pack init` writes a valid skeleton — a house-rules
`AGENTS.md` and one example skill, no manifest needed:

```console
$ yolo pack init ~/code/my-agent-pack
  create AGENTS.md
  create skills/example/SKILL.md
  create README.md

Pack scaffolded at ~/code/my-agent-pack
Next: yolo pack lint ~/code/my-agent-pack
```

That directory now looks like:

```
my-agent-pack/
├── AGENTS.md              # prose appended to every jail's briefing, attributed to the pack
├── skills/
│   └── example/SKILL.md   # a skill (needs YAML frontmatter: name + description)
└── README.md
```

Edit `AGENTS.md` to hold your house rules; add real skills under `skills/<name>/SKILL.md`.
The `skills/` + `AGENTS.md` layout is the **zero-ceremony** path — it works with no
`pack.json` at all.

> **Migration is manual re-authoring — there is no import.** `pack init` scaffolds an
> empty skeleton; it does **not** read, convert, or adopt your existing
> `~/.claude/settings.json`, your current skills, or anything else. "Move your setup into
> a pack" means: open your current config and **transcribe by hand** the keys you want
> yolo to manage into the pack's `managed` block (Step 2). There is no `pack import` /
> `adopt` / `extract` verb.

### Step 2: add a manifest when you need more than prose + skills

If your pack should also carry composed config, set env vars, or install a tool, add a
`pack.json` with a `contributes` list — one typed entry per effect, each with a `kind`
from a closed set of fifteen:

| Kind | What it contributes |
|---|---|
| `program` | a tool on PATH that **yolo installs** (`via: npm`/`installer`) |
| `requires` | a tool that must **already** be on PATH — asserted, never installed |
| `skills` / `briefing` | a skills tree / prose (usually the zero-ceremony dir + `AGENTS.md`) |
| `files` | an opaque tree the pack owns, bind-mounted `:ro` in the jail |
| `config` | a composed config surface (e.g. `~/.claude/settings.json`) |
| `config-overlay` | keys asserted onto *another* pack's surface |
| `env` | static environment variables |
| `state` | a persistent home subtree |
| `reads-host` / `mount` | read a host file / dir into the jail (`:ro`) |
| `launch` | flags injected after a binary |
| `hook` | a named capability (`shared_credentials`, …) |
| `autonomy` | the agent's autonomous/guarded permission postures (the notch selects which) |
| `loophole` | a host-capability **loophole module** the pack ships (a dir with a `manifest.jsonc`) |

`loophole` is the sharpest of the fifteen and the only one whose claim is host code
**execution** rather than a host read: its module may declare a daemon that runs on your
machine, TLS intercepts, host bind mounts and host devices, each approved separately at
`yolo pack install`. See [Loopholes](loopholes.md).

Example — a pack that carries your Claude settings as a **composed config surface** and a
static env var:

```jsonc
{
  "name": "my-agent-pack",
  "contributes": [
    { "kind": "config", "config": [ {
        "agent": "claude", "name": "settings", "codec": "json",
        "path": "~/.claude/settings.json", "mode": "rmw",
        "managed": { "preferences": { "autoUpdaterStatus": "disabled" } }
    } ] },
    { "kind": "env", "vars": { "MY_FLAG": "on" } }
  ]
}
```

`mode: "rmw"` means yolo owns only the keys it declares (`managed`) and leaves the rest
of the file alone — the key property that makes host management safe (Part 2).

The manifest schema is documented in full by `yolo config-ref` (the `packs` section) and
[../reference/pack-system.md](../reference/pack-system.md).

### Step 3: lint it — before you ever launch a jail

`pack lint` validates both the file tree **and** the `pack.json` manifest (unknown kind,
missing field, bad path — every problem, not the first), then prints the pack's
footprint so you see exactly what it claims:

```console
$ yolo pack lint ~/code/my-agent-pack
✓ pack ok — 3 file(s) stage
declares 2 claim(s):
  config   claude/settings   rmw → ~/.claude/settings.json
  env      MY_FLAG           =on
```

`yolo pack footprint ~/code/my-agent-pack` shows the same claims plus any collision, and
works on a pack you are still authoring (not just the shipped ones).

### Step 4: configure it and install

Packs live in **your user config only** — `~/.config/yolo-jail/config.jsonc` — never a
workspace config (a repo you `cd` into must not decide what enters your environment). Add
your pack alongside the agent you want:

```jsonc
// ~/.config/yolo-jail/config.jsonc
{
  "packs": [
    "claude",                              // a pack yolo ships, by name
    "file:///home/me/code/my-agent-pack"   // your local pack
  ]
}
```

Then install (this is the **only** step that ever fetches; launch never does):

```console
$ yolo pack install
claude               (ships with yolo)
my-agent-pack: local, nothing to fetch
$ yolo pack status   # locked commits + config/lock drift
```

### Step 5: launch, and confirm it took effect

```console
$ yolo -- claude
```

Your `AGENTS.md` is appended to the briefing (attributed to your pack), your skills are
merged into the agent's skills dir, and your `config` surface is rendered. Inside the
jail, an agent can confirm the running config matches what's on disk — and whether a
restart is owed after an edit — with:

```console
$ yolo config drift    # exit 0 in sync, 3 drifted (prints the diff), 4 no baseline
$ yolo describe        # the full resolved description
```

### Sharing a pack with other people (optional)

Push the pack directory to a git repo and reference it by address — the ref is
**mandatory** (an unpinned pack is the pack you audited today, not the one you get next
week):

```jsonc
"packs": ["claude", "git+ssh://git@github.com/me/dotpacks//agent?ref=v1"]
```

> [!IMPORTANT]
> **Following a mutable ref *is* the trust decision.** A `?ref=main` re-fetches whatever the
> author has pushed since you last looked, and nothing asks you again — putting a branch in
> your config is the consent, given once, for every commit that ever lands on it.
>
> So **pin a tag** for any pack that carries code, and pin it hardest for the kind that runs
> **on your own machine**: a `loophole` whose module declares a `host_daemon` or a
> `doctor_cmd`. A `program` matters too — an installer script yolo pipes to a shell, or an npm
> package whose `postinstall` runs — though that one executes inside the jail. A tag pin is
> the documented shape for all of them. `?ref=` also takes a full commit SHA, which is the
> same guarantee spelled out; what a branch name buys you is convenience, and what it costs is
> this.
>
> Nothing refuses a branch ref, and you are not exposed between installs either way: a launch
> resolves the pack from the **local mirror** and never touches the network, and that mirror
> only moves when you run `yolo pack install` or `yolo pack update`. The pin is what decides
> whether *that* command hands you code you have looked at.

`yolo pack install` clones it (host-side; the jail has no git credentials by design) and
pins the commit in a lockfile:

```console
$ yolo pack install
me/dotpacks  v1 → a1b2c3d
```

**It asks nothing, and there is nothing to approve.** A fetched pack's `reads-host`, `mount`,
installer, host-prepending briefing, wrapped-plugin hooks and shipped loopholes are all
honored — the same as a pack yolo ships. There used to be a y/N prompt here, and it was
deleted on 2026-09-04 as theatre: to install this pack at all you wrote `packs` in
`~/.config/yolo-jail/config.jsonc` as yourself, which already grants strictly more than the
prompt withheld. What replaces it is the pin above and two reports:

```console
$ yolo pack footprint git+ssh://git@github.com/me/dotpacks//agent?ref=v1
```

`yolo pack footprint` lists every claim a pack makes **before** you put it in your config, and
every launch prints what each loaded pack reads from your host — with anything that RUNS on
your machine printed just before it starts, not after.

---

## Part 2 — Manage your host

Once your config is a pack, you can render it onto your **real machine**, not just into a
jail. This is the "invert the flow" the pack migration unlocks: the same declaration, two
places it can be realized.

> **Why express host settings as a pack.** Your live `~/.claude/settings.json` *does* still
> compose into a jail — it is the `claude/settings` surface's `host` layer
> ([above](#before-you-start-what-changed)). The reason to author a pack instead is that
> `yolo host apply` also **writes** that file, so it is an input to every jail and an output
> of a host render at once, and the half yolo owns is not yours to version. That is a mixed
> authorship rather than the contradiction this note used to claim, and `rmw` is what makes
> the two directions coexist: an apply rewrites only the keys a pack declares, warns before
> overwriting a value you set, and leaves every other key byte-identical. A pack is the
> single *authored* source: declared, locked, and rendered *to* wherever you need it.
> Credentials are unaffected — those still cross as mounts, not as a config layer.
>
> *(Corrected 2026-09-09, with the bullet in "Before you start" — this said yolo "no longer
> composes" the file and called read-in-and-assert-out a contradiction.)*

### Step 1: describe what you'd apply

`describe` is the reproducibility claim made checkable — the description is a thing you
can hold:

```console
$ yolo describe
environment  confinement jail
packs        claude, my-agent-pack
description  sha256:0000…example  (unsealed — describe --hash for the pin, --json for the full config)

$ yolo describe --json    # the full canonical computed config (supersedes `config dump`)
$ yolo describe --hash    # a sha256 pin, for CI / cache keys
```

### Step 2: preview the host render (writes nothing)

`yolo host apply` renders your packs' **config surfaces** into your real `$HOME`. It is a
**dry run** unless you pass `--assert` — it prints what it *would* do and writes nothing:

```console
$ yolo host apply
host apply — dry run into /home/me; nothing is written
  6 kinds do not apply at the host notch: env, hook, loophole, profile, provider, state (`yolo config-ref` says why)
  autonomy   guarded posture — permission prompts stay ON; folded into the config surfaces below
  claude/settings      would render  /home/me/.claude/settings.json
    ⚠ would overwrite your existing value for: permissions.defaultMode
  ⚠ 3 skills in your agent skill dirs are yours, not yolo's, and would move into your local pack: house-rules, review, triage
    → to keep one out of yolo's hands, remove it from the agent dir before applying
  1 of your values would be replaced in 1 file: permissions.defaultMode
    no remedy: these keys are managed by the packs that declare them, and a config-overlay folds BELOW the managed layer, which still wins — so there is no way to keep your value at this notch yet
An --assert would complete.
  2 config files would change · 3 skills would move into your local pack · 5 destinations already in sync
  1 of your values would be replaced in 1 file · 2 declared dependencies present · first apply into this home
dry run — nothing was written into /home/me. `--assert` applies; `--verbose` lists every destination.
```

**The report ends with its own result** and states everything above it in the units you care
about — files, keys, skills. Every line is either a change, a loss, or a blocker; a destination
that is already in sync is counted, not listed. `--verbose` prints the per-destination view
instead (every surface, every `program` probe, every pruned key) — that is where the paths below
show up, and it is the long form this compressed report replaced on 2026-09-12.

Three things this tells you — but note the last is a real gap, not honesty:
- **Not every kind ports, and the ones that do not are named once.** Two different reasons
  fold into one line. A kind the host notch cannot honor at all — `state`, `mount`,
  `reads-host`, `loophole` — has no meaning without a container, and a copy is never a silent
  substitute for a mount. A kind it *could* express but has no host renderer for — `env`,
  `launch`, `hook`, `provider`, `profile` — is named in the same breath, because the reader's
  question and its answer are the same either way. (`files` is NOT in either group: since
  2026-08-02 a pack's owned tree is **written** into your real home rather than bound into a
  jail.) The word is `do not apply`, never *refused* — a kind that stopped nothing did not
  refuse anything — and `yolo config-ref` carries the reason per kind, which is where the
  ~40-word paragraphs this line replaced now live. A `${workspace}`-KEYED *key* has no
  host referent, so it is **pruned by name** under `--verbose` — but only that key: the rest of
  the surface still renders. The shipped `claude` pack's `config` surface carries nothing *but*
  those two per-jail trust flags, so with no other pack contributing to it the whole surface is
  skipped. Add a pack that contributes, say, `mcpServers` to `claude/config` and the same
  surface renders, still naming the two pruned keys. (This used to be a surface-level *refusal*,
  which made all of `~/.claude.json` — including user-scope MCP servers — unreachable at the
  host notch because of two unrelated keys.)
- **`skills` and `briefing` ARE written**, and yolo owns those destinations **outright** — this
  changed on 2026-08-04 and the earlier text here (saying they were silently skipped) is no
  longer true. Each skills directory and each briefing file is **composed wholesale** from your
  pack set, exactly as a jail composes them, so the `AGENTS.md` and skills you authored in Part 1
  do reach your real home. Two consequences worth knowing before your first `--assert`:
  - **Skills and prose you already had are MIGRATED, once, behind a confirmation.** They move into
    `~/.config/yolo-jail/local/` (the conventional *local pack*), and yolo composes them back into
    **every** agent's destination from there — so the same skills reach the same agents, and now
    reach all of them instead of drifting per agent. The prompt lists every path first and fails
    closed on a non-interactive stdin. Nothing is ever deleted: anything that cannot be moved is
    archived under the state dir.
  - **After that, hand-editing an agent's skills dir does not stick.** A skill you drop into
    `~/.claude/skills/foo/` by hand is composed away on the next apply — it is offered for
    migration into the local pack instead, and the report says so. Edit
    `~/.config/yolo-jail/local/skills/` and every agent gets it.
- **A `program` install never runs in the dry run** — and since 2026-09-12 it *can* run under
  `--assert`, behind a confirm. A declared dependency that is missing from your host is a
  **blocker**: the dry run reports it and exits 0, and `--assert` stops at a prompt that lists
  every missing binary and the exact install command for each. Answering no is fatal — the run
  writes nothing rather than continuing into an environment it already knows is incomplete —
  and **silence is no**, so a scripted `--assert` refuses instead of installing. Only a
  `program` is offered an install; a missing `requires` refuses with the remedy named, because
  offering to install one would contradict what that kind means. Installing software on your
  real machine is still the sharper decision (see Step 4).

> **⚠ The dry run does not show you the payload.** It names the keys it would **overwrite**,
> and nothing else about the content — not the values, and not the keys you do not already
> have. So `claude/settings would render` can look nearly innocuous while the actual content is
> the jail-bypass block from the security banner at the top of this guide. "Preview first" is
> **not** sufficient review here, and neither is `--verbose`: before you ever `--assert` a
> shipped agent pack, read the pack's `managed` block yourself (`yolo pack lint <pack>`, or
> `packs/claude/pack.json`) and understand every key it will write.

### Step 3: apply it for real

When the preview looks right, write it with `--assert`:

```console
$ yolo host apply --assert
host apply — applying into /home/me
  claude/settings      rendered  /home/me/.claude/settings.json
Applied: 2 config files, 3 skills moved into your local pack.
  2 config files changed · 3 skills moved into your local pack · 5 destinations already in sync
assert — this posture writes into /home/me. Without --assert it is a dry run.
```

**This is read-modify-write, but be precise about what "untouched" means.** yolo preserves
only the keys your pack does **not** declare. Every key inside the pack's `managed` block
is **overwritten** with the pack's value. So:

- A sibling key the pack never mentions (e.g. a top-level `env` you set, or your editor
  theme) **survives** — that part of "RMW preserves your keys" is true.
- A key the pack manages is **overwritten** — but at the host notch, no longer *silently*.
  If a managed key's value differs from what you already have, `yolo host apply` prints a
  `⚠ would overwrite your existing value for: <key>` line (in the dry run too), so
  you see the collision before writing. And because the guarded posture no longer manages
  the dangerous `permissions.allow`/`deny` at the host notch, a hand-authored
  `permissions.deny: ["Read(~/.ssh/**)"]` is **left alone** rather than wiped.

There is **no restore-to-previous**, and that is the part to internalise: nothing snapshots
what a key held before yolo first wrote it, so no verb can bring it back.

What there IS, since 2026-09-11, is **`yolo host apply --revert`** — take yolo back *out* of
this home. It removes the keys yolo asserted, on the authority of the per-key provenance
record yolo wrote beside them, and deletes that record; a key you set yourself (recorded
`host`) is never touched. It is a dry run until you pass `--assert`, and it lists every key
with the attribution the removal rests on. It needs `host_management: "assert"` — at `"none"`
yolo wrote nothing to withdraw, and at `"own"` the file is derived output you delete rather
than retreat from key by key.

The narrower move is unchanged and still the right one most of the time: "stop managing this
one key" is "stop declaring it and re-apply," which drops it.

Re-run `yolo host apply --assert` any time you change the pack — it re-asserts, idempotently.

#### Dynamic tables (`mcpServers`) are REPLACED, not merged

A **dynamic managed table** — the `mcpServers` block, whatever the agent calls it — is the
one exception to "RMW merges." yolo owns the key outright and **regenerates it wholesale**,
per the rule that config is the source of truth: an entry present in the file but absent
from your config is either stale from a previous apply or one you added through the agent's
UI, and either way the fix is to declare it. **If you manage `mcpServers` through yolo, you
give up `claude mcp add`.**

Replacement rather than a deep merge is deliberate, and the reason is a bug it prevents: a
merge of your `{"type":"http","url":"…"}` entry with a pack's
`{"command":"npx","args":[…]}` entry of the same name loses *nothing* and produces a record
carrying **both transports**, which no client can use. Every incoming key is an add, so a
key-level overwrite warning sees nothing to report — a "safe" merge that silently breaks the
server.

Two guardrails, since replacement is the sharper behavior:

- **Every casualty is named**, per entry and by kind:
  `mcpServers.handAdded (dropped — not in your config)` versus
  `mcpServers.tavily (replaced — your version is not kept)`.
- **The first apply into a home asks first.** If a `--assert` would drop or replace an entry
  in a home yolo has never managed, it lists them and **waits for confirmation** — you have
  not opted into the policy yet, so replacing a hand-added server before you have declared
  it anywhere is data loss rather than policy. Later applies re-assert without prompting
  (they still report). With **no TTY** the confirmation is a **no**, so a scripted or CI
  `yolo host apply --assert` aborts rather than destroying a server unattended.

To keep an entry, declare it under `mcp_servers` in your config — which reaches every agent,
not just the one — and re-run.

> **⚠ `${VAR}` does not expand at the host.** `yolo host apply` resolves no variables: it renders
> files and launches nothing, so no `env_sources` pass runs in the render — hydrating secrets is
> the host notch's *exec* half, `yolo host -- <cmd>`. So a `"url": "…?apiKey=${TAVILY_API_KEY}"`
> in pack content is written **literally** into `~/.claude.json`. The apply warns per surface
> (`⚠ ${TAVILY_API_KEY} written LITERALLY`) rather than resolving it, because putting the
> plaintext secret in a file yolo does not own defeats the point of `env_sources`. In the
> **jail**, the same entry expands correctly.

### Step 4: make sure the host has the tools your packs need

At the jail notch, tools come from the baked image. On your host, they're whatever you've
installed — so a pack can declare **`install_hints`** on a `program`, and `check-deps`
probes for them and hands you a runnable install manifest:

```jsonc
// in a pack.json
{ "kind": "program", "bin": "psql", "via": "npm", "package": "x",
  "install_hints": { "brew": "postgresql@16", "apt": "postgresql-16", "nix": "postgresql_16" } }
```

```console
$ yolo check-deps
✓ psql             /opt/homebrew/bin/psql
✗ redis            MISSING → brew install redis

wrote ~/.config/yolo/Brewfile — install with the command for your manager
```

`check-deps` **detects and hands off** — it never installs anything itself. It picks the
package name for your detected package manager, writes the manager's own manifest
(`Brewfile` and kin), and exits non-zero if a declared dep is missing (so CI can gate on
it). You run the install command; yolo stays out of mutating your machine unprompted.

### Step 5 (optional): seal it for reproducibility

When you want "the same declaration, and *only* the declaration" — pinning an environment
in CI, handing it to a colleague — `apply --sealed` refuses if any **undeclared** input
shaped the environment:

```console
$ yolo apply --sealed
✗ refused: yolo-jail.local.jsonc is present and merges into the config, but nothing
  declares it. Fold its keys into yolo-jail.jsonc or remove it to seal.
```

Sealing does **not** ban host reads — a named-but-impure input (your user config, a pack's
`reads-host`) is *declared* and fine. It bans inputs that *nothing* names: a
`yolo-jail.local.jsonc`, or an outstanding captured in-jail edit. Once it seals clean,
`describe --hash` is a real reproducibility pin rather than just a cache key.

---

## What is not built yet (so you're not surprised)

A few things the design calls for are **not built**:

- **The commands themselves are unreleased.** As the version banner at the top says,
  `describe`, `apply`, `check-deps`, the newer `pack` subcommands, and `config drift`/`dump`
  are in-progress and not in a released `yolo`. Verify with `--help` before relying on any
  step.
- **The `guest` confinement notch** — a real home under an LSM boundary (macOS Seatbelt /
  Linux bwrap+Landlock), between `jail` and `host`. `confinement: guest` validates but the
  backend is not implemented; use `jail` or `host`.
- ~~**`yolo host apply` offering to run installs for you.**~~ **Built 2026-09-12** — see Step 2.
  `--assert` offers to run a missing `program`'s install behind one confirm, and a decline stops
  the run. What is still unbuilt is batching those confirms by elevation class (`sudo` first,
  shown through), so today it is one prompt for everything. `yolo check-deps` still installs
  nothing, by design.
- **A provision-without-launch at the jail notch.** `yolo apply` at jail currently directs
  you to `yolo -- <cmd>` (or `yolo -- true` to provision and exit); a dedicated no-exec
  provision is a follow-up.
- **A `pack import`/`adopt` verb for CONFIG.** Config surfaces are still manual re-authoring
  (Part 1) — nothing reads your existing `~/.claude/settings.json` into a pack for you. Your
  existing `skills` and briefing prose ARE migrated for you, on the first `yolo host apply --assert`
  (see Part 2) — that half is no longer manual.

Tracking for all of it: [../plans/environment-manager-plan.md](../plans/environment-manager-plan.md).

---

## Quick reference

| You want to… | Command |
|---|---|
| Start a pack | `yolo pack init <dir>` |
| Check a pack before using it | `yolo pack lint <dir>` · `yolo pack footprint <dir>` |
| Turn packs on | edit `~/.config/yolo-jail/config.jsonc` `packs`, then `yolo pack install` |
| See what packs stage / drifted | `yolo pack ls` · `yolo pack status` |
| See the resolved environment | `yolo describe` (`--json`, `--hash`) |
| Preview host config render | `yolo host apply` (⚠ names the keys it would overwrite, never the payload — read the pack first) |
| Apply config to your real home | `yolo host apply --assert` (⚠ writes jail-bypass keys from shipped agent packs — see banner) |
| Check host has the needed tools | `yolo check-deps` |
| Prove nothing undeclared crept in | `yolo apply --sealed` |
| In-jail: is a restart owed? | `yolo config drift` |

Full schema: `yolo config-ref`. The pack system in depth:
[../reference/pack-system.md](../reference/pack-system.md).
