# Outstanding work

**What this is.** Everything still to do, and nothing else. Written 2026-08-05, after the
ten-item pack batch shipped. If an item is here it is **not built**; if it was built, it is in
[`shipped-2026-08-pack-batch.md`](shipped-2026-08-pack-batch.md) with the reasoning and the
defects that only surfaced by running it.

**Read this as today's forward plan.** Every claim below was checked against the code on
2026-08-05, not against a status marker — three stale "still open" markers turned up in these
docs during the batch, so verify-against-code is the house rule now.

**Nothing here is blocked on a decision except where a row says so.**

---

## The short version

| # | Item | Kind of work | Blocked on |
|---|---|---|---|
| **S1** | Skill collisions are SILENT at flat tier and produce two names at namespaced — unnamespaced by default, **fatal** on collision | 🔴 ruled, not built | nothing |
| **S2** | `tier` becomes an opt-in pack declaration, not a property yolo resolves per destination | ruled, mostly moot given S1 | S1 |
| **S3** | The jail's layer 4 reads the DESTINATION, which yolo now owns — circular | 🔴 live defect | S1 |
| **N1** | `nix` profile has no gcroot — a `packages:` build can be garbage-collected out from under a launch | 🔴 live defect | nothing |
| **N2** | Generalize `yoloDarwinPackages` → system-neutral, un-hardcode `aarch64-darwin`, report the resolved path | prerequisite for Phase 7.2 | nothing |
| **N3** | Non-container nix: pick Option 0/2/3 beyond N2 | **your decision** | you |
| **P7** | The `guest` notch (env-manager Phase 7) | feature | a Mac |
| **E1+E2** | `host_files` modes 4→3, `readonly` as a real `:ro` mount | behavior change on a shipped key | a design pass (E2 first) |
| **E3** | Capture on terminate (the `yolo config capture` half SHIPPED) | small | nothing |
| **E4** | Comment preservation on `json`/`toml` surfaces | small, decisions already made | nothing |
| **E5** | `managed`/`defaults` array-append pinning | small | **do not build speculatively** |
| **V1** | Reserved destinations miss symlink aliases | validation gap | nothing |
| **V2** | `apply --host` is not whole-home idempotent until apply 3 | pre-existing, in `config` | nothing |
| **V3** | Pack-set-wide archives land under `archive/skills/` even for `files` | cosmetic, migration-shaped | nothing |
| **C1** | Drop the now-redundant `"from"` literals from the six shipped packs | cleanup | nothing |
| **C2** | Briefing prose should read the Profile, not switch on a notch name | small | nothing |
| **C3** | `packoverlay.Collect`'s autonomy parameter — §6c's remaining half | small | nothing |

---

## S1 — skill collisions: unnamespaced by default, FATAL on collision 🔴

**Maintainer ruling, 2026-08-05:** *"I want unnamespaced by default with a fatal collision error if
skills collide on name. Namespacing should be possible by the pack's choice, but it should be a
positive choice."*

### What happens today, measured

A local pack and a shared pack both shipping a skill called `dup`, one apply, `claude` (namespaced)
plus `codex` (flat):

```
skills  dup  rendered  invoke as /sp:dup
skills  dup  rendered  invoke as /local:dup     <- .claude: BOTH survive, two names
skills  dup  rendered  invoke as /dup           <- .codex:  ONE survives
```

- At **namespaced** tier both coexist, so what the user thinks of as one skill has two invocations.
- At **flat** tier one silently wins. I checked for a warning on the loss: **there is none.** A
  pack's skill can vanish with no output line at all.

The second is the defect that matters. It is the exact class of silent failure the batch existed
to remove, and it survived because tiers made the collision unrepresentable in the case anyone
tested (namespaced) while leaving it silent in the case that ships by default (flat — `agy`,
`codex`, `pi` all leave `tier` unset, and `tier.go:52` documents the zero value as "the SAFE
treatment").

### And a third spelling nobody had named: the jail

`internal/agents/skills.go` has **no tier concept at all** — the jail is flat-only. So one skill is
`/mine` in a jail and `/local:mine` at host-claude. The notch changes the invocation name. The
divergence audit (`shipped-2026-08-pack-batch.md` §6b) missed this because it audited *mechanism*
per kind, not user-visible *naming*.

### The ruling, and why it is better than resolving collisions

Unnamespaced by default; a name collision is **fatal at apply time**; namespacing is a positive
per-pack opt-in.

This makes the failure **unrepresentable rather than negotiated**, which is the same move as the
doubly-declared-config-surface refusal: today a flat collision picks a winner and says nothing,
and a namespaced one invents a second name. A fatal error surfaces it when the user can still fix
it — rename, or opt one pack into namespacing.

**It will fire on a real case**, and that is correct rather than unfortunate: a personal pack and a
shipped pack both shipping `agent-standards` is a genuine ambiguity the user should resolve. So the
error has to name **both packs, both source paths, and both remedies** — a fatal error the user
cannot act on would be worse than the silence it replaces.

Note this also means the migration's suffix-on-differing-content behavior (shipped in Q6) becomes
the *migration-only* path: adopting a user's pre-existing tree still keeps both copies under
distinct names, because that content already exists and refusing to adopt it would be the data
loss the migration exists to avoid. Two different situations, deliberately: **adoption preserves,
declaration refuses.**

## S2 — `tier` becomes an opt-in pack declaration

Mostly a consequence of S1, and the maintainer's own read: *"maybe moot by (2)?"* — largely yes.

The problem `tier` has today is that it is a **per-contribution** declaration controlling a
**global** property (what a skill is called), so it cannot express a consistent name. Worse, a
zero-ceremony pack declares no destinations, borrows them from the other selected packs, and
inherits each destination's tier (`internal/packload/mergedest.go`) — which is how the local pack
ends up namespaced in Claude and flat everywhere else without ever choosing either.

Under S1 there is nothing to inherit: unnamespaced is the default, and a pack that wants a
namespace says so once, for itself, at every destination. That is what makes it a positive choice
rather than a property of whichever agent pack happened to name the directory.

**What survives of `tier`:** the pack's own opt-in. **What goes:** yolo resolving it per
destination, and the inheritance path.

## S3 — the jail's layer 4 reads a directory yolo now owns 🔴

**Verified 2026-08-05.** `internal/cli/run/prepare.go:312` sets `t.HostSource = c.Into` — so the
jail's fourth composition layer ("the user's OWN skills tree", `skills.go:104`) reads the
**destination**, e.g. `~/.claude/skills`.

That was correct when the destination held loose user files. It is circular now: after Q6, the host
`apply` **generates** that directory, so a jail launch reads yolo's own composed output back in as
"the user's tree." And because the local pack is an ordinary pack entry
(`internal/config/packs.go:215`), its content is already **layer 2** — so in a jail it arrives
twice, by two paths, with only the flat last-writer-wins rule making that invisible.

**The fix follows from the local pack's real purpose** (see below): layer 4 should read
`paths.LocalPackDir()/skills`, not the destination. Then the layers are disjoint — built-ins,
packs, and the user's own home — and each appears exactly once.

**Do S1 first**: with collisions fatal, arriving twice stops being invisible and becomes an error,
so the ordering matters.

## The local pack IS layer 4 — the rationale, corrected

An earlier note in this file conceded that a local pack needing a manifest is "just a pack in an
awkward location." **The maintainer pushed back and was right:** *"we need somewhere for user
contributions to go, so where else could they go now that we control briefings and skills
completely?"*

The manifest-avoidance argument was the weak one. The load-bearing argument is that yolo now owns
`~/.claude/skills` and `~/.claude/CLAUDE.md` wholesale, so **a user contribution has nowhere else
to live.** "Commit it to a repo pack" is not an answer for a half-baked skill, a machine-specific
one, or scratch space you do not want in git.

The jail already had this slot — layer 4, "the user's OWN skills tree, written last so a
same-named local skill wins" (`skills.go:102-104`). The local pack is not a new concept; it is
**that slot given a home yolo does not overwrite.** Which is also why S3 is a defect and not a
design choice: layer 4 pointing at the destination was how the slot worked before it had a home.

And S1/S2 remove the manifest need anyway — no tier declaration, no split-brain, no `pack.json`.

## N1 — the nix profile has no gcroot 🔴

**Verified 2026-08-05:** `rg -n 'gcroot' flake.nix internal/darwinpkg internal/macosuser` finds
nothing but the image's own `/nix/var/nix/gcroots` directory creation. So the `buildEnv` profile
that `packages:` materializes is **not rooted**, and `nix store gc` can collect it out from under
a launch that depends on it.

This is the only live defect in the non-container nix area, and it is independent of every option
in N3 — whichever provisioning story you pick, an unrooted profile is wrong. Fix is
`nix-store --add-root --indirect` (or the flake-era equivalent) at materialization time, plus
somewhere for the root to live under `paths.GlobalStorage()`.

**Do this one first**, because it is a real failure mode today and needs no ruling.

## N2 — the mechanism is already cross-platform; only the NAME is darwin

Checked all four sub-items on 2026-08-05:

| Sub-item | State |
|---|---|
| `yoloDarwinPackages` → system-neutral name | ❌ `flake.nix:994` |
| Un-hardcode `aarch64-darwin` in the caller | ❌ `internal/darwinpkg/darwinpkg.go:19` `DarwinSystem = "aarch64-darwin"` |
| Add a gcroot | ❌ (that is N1) |
| `describe` / `check --at host` report the resolved profile path | ❌ not reported |

The mechanism itself is fine: `flake.nix:303` confirms `lib.meta.availableOn` is a per-system
predicate, and the package set resolves for `x86_64-linux`. **The name is the lie, not the
mechanism.** The single caller is `internal/cli/commands.go:369`, reached only from
`macosUserRun`, which is why nothing else can use it today.

**This is a prerequisite for env-manager Phase 7.2, not an option** — `guest` is a real home with
no image, so it needs a tool closure for exactly the reason `host` does. See
[`handoff-guest-notch-macos.md`](handoff-guest-notch-macos.md) §5.

**Same bug class as BACKLOG E8**, which was fixed 2026-08-03 and turned out to have **three**
instances of one hardcoded-arch assumption rather than the one its entry named — found only by
grepping the literal instead of trusting the entry's scope. So: `rg -n 'aarch64' internal/ flake.nix`
before assuming `darwinpkg` is the only site.

## N3 — non-container nix: the part that is YOUR decision

Full study: [`../design/noncontainer-nix-environment.md`](../design/noncontainer-nix-environment.md).
Its §7 is the load-bearing finding — a tool environment is **not** orthogonal to confinement; it
is the missing provisioning primitive below the `jail` notch, and `guest` needs the identical
mechanism. Its §8 offers four options; N1 and N2 are Option 1 and are queued above regardless.

What is left to decide:

- **Option 0** — stop here. `install_hints` keeps printing a remedy the user runs by hand.
  Honest, and leaves `install_hints` covering 0–1 of six agent CLIs on a non-Arch Linux host.
- **Option 2** — `yolo --at host -- <cmd>`, the design's own §4.1 escape valve. Verified
  2026-08-05 that `--at` is `apply`-only (`internal/cli/apply.go:54`). This is the shape the
  codebase is already built for, and it would make `env` and `launch` renderable at the host
  immediately — see the D3 note in the shipped doc: those two kinds are refused for a reason
  that is true of `apply --host` and false of the notch. **Against:** "yolo launches your host
  agent" is a bigger product claim than "yolo configures it."
- **Option 3** — a yolo-owned `nix profile` as a confirm-gated Phase 4.3 install remedy, so the
  install story has a reproducible option beside `brew install`.

**My recommendation:** N1 + N2 now (they are not optional), then Option 2 — because it is the one
that unblocks two refused kinds rather than adding a mechanism. Not started; awaiting your call.

## P7 — the `guest` notch

Not built, host/Mac-gated rather than design-blocked. Everything the batch shipped was in service
of making this addable rather than bolted on:

- `render.KindGuest` exists with no behavior, so a `switch` on Kind must handle it.
- `render.UndecidedModes(reason)` is guest's mode census — fail-closed, with a test that tells
  Phase 7 to rewrite rather than delete it.
- `render.GuestProfileMacOS()` / `GuestProfileLinux()` are the primitive presets, and `describe`
  now prints the vector.

Full handoff, including the bug to fix first (macos-user renders ZERO pack surfaces, silently):
[`handoff-guest-notch-macos.md`](handoff-guest-notch-macos.md).

---

## E1 + E2 — the `host_files` mode collapse

Still four modes (`internal/config/hostfiles.go:59-61`: `readonly`, `copy`, …). The proposal
([composed-file-permissions.md §7.4](../design/composed-file-permissions.md)) collapses to three:
`copy` merges into `readonly`, and `readonly` becomes a real `:ro` mount rather than a `0o444`
chmod.

**Why `0o444` is the wrong mechanism:** it is **asymmetric**. Root ignores it; a non-root agent
gets `EACCES` — and the failure is silent, because the surface simply stops re-rendering. The same
declaration means "advisory" for one user and "broken" for another.

**E1 cannot land first.** Two reasons, the second being the real one:

1. `host_files` is a shipped key, so changing what a mode means changes existing configs.
2. **You cannot compose *into* a `:ro` mount.** Every current `readonly` user must be classified
   as either a surface yolo renders or a file yolo mounts read-only — a per-surface judgment call,
   not a coding problem. That is the E2 design pass.

Note `:ro` is only *non-persistence*, not confidentiality. Do not let the collapse imply a
security property it does not have.

## E3 — capture on terminate (half shipped)

`yolo config capture` **exists** (`internal/cli/config.go:112`) — that was the cheap half and the
one that names the mechanism to a user who did not know capture existed. What remains: capture in
the existing `onTerminate` hook, so the observability window closes without an explicit command.

**Nothing is lost today**, which is why this stays small: every surface sits under a host-backed
rw bind and its baseline lives in the workspace, so both survive `--rm` and the next boot captures
normally. What lags is *observability* — a host-side `yolo config diff` inside the window
under-reports.

**Explicitly rejected: an inotify watcher.** It solves staleness, not loss, at the price of
debounce plus a sidecar race, and the cost lands on the boot path.

## E4 — comment preservation

A `json`/`toml` surface is re-emitted canonically, so comments do not survive. Already *reported*
before the write as a `Formatting` line (`internal/entrypoint/hostrender.go:113`), so it is
visible rather than silent.

The three sub-questions — staleness, attachment, in-jail additions — are answered in
`host-file-staging.md`, so this starts from decisions. `raw` is already lossless. **The cheapest
useful step needs no comment parsing at all:** a yolo-authored header pointing at the `:ro`
original.

## E5 — array-append pinning

Object merge only; arrays replace wholesale, matching RFC-7386 and the render engine's `deepMerge`.
No user surface has needed it, and a config expecting append semantics fails **loudly** at
shape-check time, which is what makes waiting safe.

**The one item to name as do-not-build-speculatively.** An append mode is a second merge semantics
for the same field, and the layer model's legibility is its main asset.

---

## V1 — reserved destinations miss symlink aliases

`~/.config/git/config`, `~/.config/bashrc` and `~/.claude/claude.json` validate while their aliases
are rejected. `internal/config/helpers.go:87-95` resolves symlinks lexically with an
`EvalSymlinks` fallback, but the reserved-destination guard (`writablehome.go`) matches on the
declared path. Tracked at
[composed-file-permissions.md §4.5](../design/composed-file-permissions.md).

## V2 — `apply --host` is not whole-home idempotent until apply 3

Found while verifying Q6, and **pre-existing** — reproduced against a binary built from before that
work. A surface's provenance record classifies a key `default` on apply 1 and `host` on apply 2,
converging from apply 3. Not skills (skills and the local pack are byte-identical from apply 2);
it belongs to `config`. Harmless today, but it means "a second apply is a no-op" is not yet true
of the whole home, and any future test asserting that will be flaky.

## V3 — pack-set archives land under `archive/skills/`

`internal/cli/applyhostskills.go:78` puts every archive under
`<state>/archive/skills/<stamp>/<pack>/…`, including `files` and briefing content. The path implies
a skill was archived. Cosmetic, but renaming it orphans existing archives from `yolo prune`'s
sweep, so it needs a migration rather than a rename — which is why it is recorded rather than
already done.

---

## C1 — drop the redundant `"from"` literals from the shipped packs

`from` is now optional for `skills` and `briefing` (defaults `skills/`, then
`AGENTS.md`/`CLAUDE.md`), but all six shipped `pack.json` files still declare the literal the
resolver would have supplied. Removing them exercises the default and shortens the reference
examples. **Check the render fingerprint when you do** — it should not move, and if it does, the
default is not resolving the way the validator now permits.

## C2 — the briefing's per-notch prose should read the Profile

`internal/agents/agentsmd.go` switches on the notch NAME to pick its prose. Q10 deliberately left
it: the text genuinely differs per notch and a human reads it, so it is a legitimate boundary. But
it would be **better** reading the Profile — describing the primitives the notch composes plus the
autonomy bit — because then the prose is accurate for a notch nobody has enumerated yet. Same
argument that motivated `KindGuest`. Deferred at the time only because another agent held that
file.

## C3 — `packoverlay.Collect`'s autonomy parameter

Three of four autonomy literals are gone. `Collect` was already parameterized, and its three
callers each pass a literal — so converting it without deriving those callers' values from a
`Target` just moves the same literals one frame out. §6c's remaining half; small, and worth doing
when someone is next in those three callers.

---

## Suggested order

1. **S1** (collisions fatal + unnamespaced default) — it is a ruling, it is a live silent-loss
   defect, and S2/S3 both depend on it.
2. **S3** (layer 4 reads the local pack) immediately after, since S1 turns its double-arrival from
   invisible into an error.
3. **S2** falls out of S1 — mostly deletion.
4. **N1** (gcroot) — a live defect, no ruling needed.
5. **V1** (symlink aliases) and **C1**/**C3** — small, independent, no decisions.
6. **N2** — mechanical, and unblocks P7.2.
7. **Your call on N3**, which decides whether Option 2 joins the list.
8. **E2's design pass**, then **E1**.
9. **E3**'s terminate half and **E4**'s header step.
10. **P7** when you are on a Mac. **V2/V3/C2** as anyone passes through.
11. **E5** only when a real surface needs it.
