# Migrating to packs, and managing your host from yolo

> **⚠ UNRELEASED — verify your version first.** The verbs this guide leans on —
> `describe`, `apply` (incl. `--host`/`--sealed`), `check-deps`, and the newer `pack`
> subcommands (`lint`'s manifest validation, `footprint`, `install`, `status`) and
> `config drift`/`dump` — are part of in-progress work that is **not in a released yolo
> yet**. On an older `yolo` they fail with `unknown command` / `unknown subcommand`
> (exit 2), not a helpful message. Before following any step, confirm the verb exists:
> `yolo apply --help`, `yolo describe --help`, `yolo pack --help`. If those error, your
> installed `yolo` predates this work — rebuild/reinstall from a build that carries it.

> **Autonomy is a confinement policy (how `apply --host` stays safe).** The shipped agent
> packs (`claude`, `codex`, `agy`, `opencode`) declare the jail-bypass settings
> (`acceptEdits`, `skipDangerousModePermissionPrompt`, `additionalDirectories: ["/"]`,
> `--dangerously-skip-permissions`, …) in an `autonomy` contribution's **autonomous**
> posture — rendered only at the contained notches (`jail`/`guest`). `apply --host` renders
> each pack's **guarded** posture instead: permission prompts stay **on**, and the bypass
> keys never reach your real `~/.claude/settings.json`. (The permissive-by-default `pi` pack
> is the mirror image — `host` *tightens* it from auto-trust to prompt.) `apply --host` also
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
- **Your personal `~/.claude/settings.json` is no longer a composed config *layer*.** yolo
  used to merge it *into* the jail's settings as a magic layer; that layer is gone. (The
  shipped `claude` pack still mounts it read-only into the jail via a `reads-host` entry so
  the agent can *see* it — but it no longer silently composes into what yolo writes.) The
  durable way to carry your settings is to put them in a **local pack** — declared, locked,
  and portable to every confinement level (see Part 2 for why this matters for host
  management).

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
from a closed set of thirteen:

| Kind | What it contributes |
|---|---|
| `program` | a tool on PATH (`via: npm`/`installer`) |
| `skills` / `briefing` | a skills tree / prose (usually the zero-ceremony dir + `AGENTS.md`) |
| `config` | a composed config surface (e.g. `~/.claude/settings.json`) |
| `config-overlay` | keys asserted onto *another* pack's surface |
| `env` | static environment variables |
| `state` | a persistent home subtree |
| `reads-host` / `mount` | read a host file / dir into the jail (`:ro`) |
| `launch` | flags injected after a binary |
| `hook` | a named capability (`shared_credentials`, …) |
| `autonomy` | the agent's autonomous/guarded permission postures (the notch selects which) |

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
[../design/pack-system.md](../design/pack-system.md).

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

`yolo pack install` clones it (host-side; the jail has no git credentials by design),
pins the commit in a lockfile, and — **if the pack reads your host** (a `reads-host`,
`mount`, or installer) — shows exactly what it reads and asks once:

```console
$ yolo pack install
me/dotpacks  v1 → a1b2c3d
  ⚠ pack dotpacks reads your host:
      reads-host .config/acme/key
  Approve host access for dotpacks? [y/N]
```

Approval is recorded per-commit; a later `pack update` that pulls a commit adding a *new*
host-access claim re-prompts. Static `env` values are never gated (they read nothing).

---

## Part 2 — Manage your host

Once your config is a pack, you can render it onto your **real machine**, not just into a
jail. This is the "invert the flow" the pack migration unlocks: the same declaration, two
places it can be realized.

> **Why express host settings as a pack (and retire the old inheritance).** yolo no longer
> *composes* your live `~/.claude/settings.json` into the jail's settings as a magic layer —
> because reading settings *in* and asserting config *out* over the same file is a
> contradiction. (The shipped `claude` pack still mounts that file read-only into the jail
> so the agent can see it; what's gone is the silent merge-into-what-yolo-writes.) A pack is
> the single *authored* source: declared, locked, and rendered *to* wherever you need it.
> Credentials are unaffected — those still cross as mounts, not as a config layer.

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

`apply --host` renders your packs' **config surfaces** into your real `$HOME`. It defaults
to **observe** (a dry-run) — it prints what it *would* do and writes nothing:

```console
$ yolo apply --host
apply --host  home /home/me  posture observe (dry-run)
  claude/settings          would render  /home/me/.claude/settings.json
  claude/config            refused: uses ${workspace}, which has no referent on the host
  reads-host refused — reads-host carries a host file INTO a jail — meaningless when there is no jail
  state      refused — state names a jail-writable home subtree — off-container the home simply is writable
observe only — nothing written. Re-run with --assert to apply.
```

Three things this tells you — but note the last is a real gap, not honesty:
- **Only config surfaces port.** Kinds that need a container (`reads-host`, `state`,
  `files`, and `mount` if a pack declares one) are **refused by name** — a copy is never
  a silent substitute for a mount. A `${workspace}`-derived surface is refused too (no
  workspace on the host).
- **`skills` and `briefing` are honored by the census but silently NOT written by
  `apply --host`** — it renders config surfaces only. So the `AGENTS.md` and skills you
  authored in Part 1 do **not** reach your real home this way, and they don't appear in the
  refused list either. Copy them by hand if you want them on the host.
- **`program` (install) is not run** by `apply --host`. Installing software on your real
  machine is a separate, sharper decision (see Step 4).

> **⚠ Observe hides the payload.** The preview prints only *paths* (`would render …`), not
> the keys and values that would land. So `claude/settings would render` looks innocuous
> while the actual content is the jail-bypass block from the security banner at the top of
> this guide. "Preview first" is **not** sufficient review here: before you ever `--assert`
> a shipped agent pack, read the pack's `managed` block yourself (`yolo pack lint <pack>`,
> or `packs/claude/pack.json`) and understand every key it will write.

### Step 3: apply it for real

When the preview looks right, write it with `--assert`:

```console
$ yolo apply --host --assert
apply --host  home /home/me  posture assert (writing)
  claude/settings          rendered  /home/me/.claude/settings.json
```

**This is read-modify-write, but be precise about what "untouched" means.** yolo preserves
only the keys your pack does **not** declare. Every key inside the pack's `managed` block
is **overwritten** with the pack's value. So:

- A sibling key the pack never mentions (e.g. a top-level `env` you set, or your editor
  theme) **survives** — that part of "RMW preserves your keys" is true.
- A key the pack manages is **overwritten** — but at the host notch, no longer *silently*.
  If a managed key's value differs from what you already have, `apply --host` prints a
  `⚠ would overwrite your existing value for: <key>` line (in the observe preview too), so
  you see the collision before writing. And because the guarded posture no longer manages
  the dangerous `permissions.allow`/`deny` at the host notch, a hand-authored
  `permissions.deny: ["Read(~/.ssh/**)"]` is **left alone** rather than wiped.

There is no `--revert` and no restore-to-previous: "undo yolo's management" is simply "stop
declaring the key and re-apply," which drops it (it does **not** bring back what was there
before yolo — nothing snapshots that).

Re-run `apply --host --assert` any time you change the pack — it re-asserts, idempotently.

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
- **`apply --host` offering to run installs for you.** Today it renders config and *names*
  missing deps (`check-deps`); it does not run installers. That confirm-gated offer-to-run
  is a planned follow-up.
- **`apply --host` rendering `skills`/`briefing`.** It writes config surfaces only; the
  skills tree and `AGENTS.md` you author do not reach the host through it (copy them by hand).
- **A provision-without-launch at the jail notch.** `yolo apply` at jail currently directs
  you to `yolo -- <cmd>` (or `yolo -- true` to provision and exit); a dedicated no-exec
  provision is a follow-up.
- **A `pack import`/`adopt` verb.** Migration is manual re-authoring (Part 1) — nothing
  reads your existing `~/.claude/settings.json` into a pack for you.

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
| Preview host config render | `yolo apply --host` (⚠ shows paths, not the keys — read the pack first) |
| Apply config to your real home | `yolo apply --host --assert` (⚠ writes jail-bypass keys from shipped agent packs — see banner) |
| Check host has the needed tools | `yolo check-deps` |
| Prove nothing undeclared crept in | `yolo apply --sealed` |
| In-jail: is a restart owed? | `yolo config drift` |

Full schema: `yolo config-ref`. The pack system in depth:
[../design/pack-system.md](../design/pack-system.md).
