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

1. **N1** (gcroot) — a live defect, no ruling needed.
2. **V1** (symlink aliases) and **C1**/**C3** — small, independent, no decisions.
3. **N2** — mechanical, and unblocks P7.2.
4. **Your call on N3**, which decides whether Option 2 joins the list.
5. **E2's design pass**, then **E1**.
6. **E3**'s terminate half and **E4**'s header step.
7. **P7** when you are on a Mac. **V2/V3/C2** as anyone passes through.
8. **E5** only when a real surface needs it.
