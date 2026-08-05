# Handoff: the `guest` notch, and the macOS work only a Mac can finish

**Audience:** the maintainer on a Mac, and any agent working there. Written 2026-08-03 from
inside a Linux jail, which is exactly why this document exists: everything below was either
verified by reading code or is explicitly marked as unverifiable from here.

**Status:** `guest` is the one notch of three that does not work. Phases 0–6, 8, and 9 of
[`environment-manager-plan.md`](environment-manager-plan.md) are shipped; **Phase 7 is not
built**, and it is host/Mac-gated rather than blocked on any design decision.

**Reads with:** [`environment-manager-plan.md`](environment-manager-plan.md) Phase 7 (the
spec), [`../design/macos-user-nix-and-features.md`](../design/macos-user-nix-and-features.md)
(the existing backend — see the correction in §2 before trusting it),
[`../guides/macos.md`](../guides/macos.md) (usage),
[`../design/noncontainer-nix-environment.md`](../design/noncontainer-nix-environment.md) §5
and §8 (Option 1 is a **prerequisite** for 7.2 — see §5 below), and
[`outstanding-work.md`](outstanding-work.md) — which now holds N1 (the unrooted nix profile, a live
defect) and N2 (the rename that is this handoff's §5 prerequisite).

---

## 1. What the three notches are, and why the middle one matters

`confinement` has three notches. The dial is real and shipped; what varies is how much of it
each notch honors.

| Notch | What it is | Status |
|---|---|---|
| `jail` | container, disposable home, no host credentials | **works** — the default, unchanged |
| `guest` | a real home, LSM-confined, no image | **NOT BUILT** — this handoff |
| `host` | your actual `$HOME`, no confinement | **works** — `yolo apply --host` |

The gap is not cosmetic. `jail` and `host` are the two extremes, so a user who needs *some*
isolation but real credentials has nothing to select — they take `host` and lose all of it.
The design calls this "a three-notch story with a broken middle" and Phase 7 is where it gets
fixed.

---

## 2. The bug to fix first, and a doc that lies about it

**`macos-user` renders ZERO pack surfaces, every launch, silently.** Verified still live
2026-08-03 by reading the code:

- `internal/entrypoint/darwin.go` — `RunDarwinBootstrap` does call
  `LoadJailPacks` / `ConfigurePackSurfaces` / `RunPackHooks`. The machinery is wired.
- `internal/cli/run/run.go:60-76` — the `rt == "macos-user"` branch returns at the
  `MacosUserRun` call, which is **before `stagePacks`**. So `YOLO_PACK_ROOT` is never set,
  and every one of those loops runs over an empty list.

The result is a backend that looks provisioned and configures nothing. No error, no warning —
the loops simply have nothing to iterate.

> **`macos-user-nix-and-features.md:174` still claims pack selection works there. It does
> not.** Do not trust that line; fix it when you fix the bug. This is the third stale
> status claim found in these docs this week (the other two were an "unbuilt" autonomy fix
> that had shipped, and an out-of-scope note for a collision already fixed), so the general
> lesson holds: **verify against code, not against markers.**

This is plan item **1.4**, and it is deliberately the first thing to do: it is the cheapest
real test of whether `render.Target` can express a non-container backend at all. If `Target`
cannot express macos-user cleanly, it will not express `guest` either.

---

## 3. Phase 7, as specified

### 7.1 — macOS `guest`

The existing macos-user backend, but actually rendering surfaces: a separate user plus
Seatbelt, treated as **composed primitives** rather than one monolithic backend. The
primitive layer (plan Phase 2.2) was built to express exactly this.

Where the pieces already are: `internal/macosuser/` — `seatbelt.go` (the profile),
`runplan.go` (the launch plan), `orchestrator.go` (the sequencing), `real.go` (the real-system
implementations behind the seams).

### 7.2 — Linux `guest`

`bwrap` + Landlock: a real home, no image — "a weaker container, no separate user." The
design calls this the missing fourth composition the primitive layer was built for, needing
no new concept.

**Why this is in a macOS handoff:** 7.2 is Linux and therefore *not* Mac-gated, but it shares
the `guest` Target plumbing with 7.1, and 7.1 is where the abstraction gets proven against a
genuinely different confinement mechanism. Doing 7.2 first risks fitting the abstraction to
one implementation.

**Done when:** `confinement: guest` renders a pack's full portable surface set into a real,
LSM-confined home on both platforms, and `describe` prints the composed primitives.

---

## 4. What else on the Mac is gated, beyond Phase 7

Collected here so one trip to a Mac can close all of it.

| Item | What is needed | Where |
|---|---|---|
| **D4 Cachix** | ONE real download proof. Substituter enabled, account/cache/CI-push all done 2026-07-22. Only the Mac download path is unproven | [`handoff-cachix-cache.md`](handoff-cachix-cache.md), ROADMAP item 2 |
| **E8's nightly** | The first nightly AFTER the next release. `publish.yml` is tag-triggered, so the multi-arch builder image does not reach GHCR until then — the nightly stays red regardless of the code being correct | [`BACKLOG.md`](BACKLOG.md) E8 |
| **agent-auth macos-user parity** | 4 verified defects whose fixes need a Mac to verify | ROADMAP item 4 |
| **`cache_relocations`** | One real cross-filesystem move as an acceptance step | [`cache-relocation.md`](cache-relocation.md) |
| **`yoloDarwinPackages` rename** | Not Mac-gated to *write*, but Mac-gated to *prove*. See §5 | [`../design/noncontainer-nix-environment.md`](../design/noncontainer-nix-environment.md) §8 Option 1 |

---

## 5. The nix prerequisite — do not skip this

[`noncontainer-nix-environment.md`](../design/noncontainer-nix-environment.md) §8 **Option 1**
is a prerequisite for Phase 7.2, and NOT because of anything about the `host` notch — `guest`
is a real home with no image, so it needs a tool closure for exactly the reason `host` does:

- rename `yoloDarwinPackages` → something system-neutral (`yoloHostPackages`),
- stop hardcoding `aarch64-darwin` in `internal/darwinpkg`,
- add a gcroot (the missing one is a real gap: an unrooted profile is garbage-collectable),
- make `describe` / `check --at host` **report** the resolved profile path.

One finding from that doc worth carrying: `packages.yoloDarwinPackages` is **already
per-system** and resolves for `x86_64-linux`, so the name is the lie, not the mechanism.

**This is the same class of bug as BACKLOG E8**, which was fixed 2026-08-03: a hardcoded
`aarch64-*` string that was true of an Apple Silicon Mac and wrong everywhere else. E8 turned
out to have **three** instances of that assumption, not the one its entry named — found only
by grepping the literal instead of trusting the entry's stated scope. Do the same here:
`rg -n 'aarch64' internal/ flake.nix` before assuming `darwinpkg` is the only site.

---

## 6. How to verify on a Mac — the traps

From `AGENTS.md`, plus what this session learned:

- **`just deploy` does NOT cross-compile.** It is `just install` (host `go install ./cmd/yolo`)
  plus Claude-broker priming.
- **Never `just install` in-jail** — it refuses (`YOLO_VERSION` set), because `go install`
  shadows the baked `/bin/yolo` with a stale GOBIN copy.
- **Run the freshly built binary BY PATH** for `cmd/`/`internal/` changes:
  `./dist-go/darwin-$(go env GOARCH)/yolo -- bash`. Bare `yolo` is the baked launcher and
  will not carry a launcher/argv-side change.
- **`git add` before any nix-visible verification** — nix sees TRACKED files only, so an
  untracked new file silently vanishes from the build and the image-skew check reports a false
  "matches".
- **The integration suite refuses to run against a stale image**, comparing
  `nix eval .#installPrefix.outPath` to `readlink /bin/yolo-entrypoint` in the loaded image.
  On darwin this auto-downgrades to `warn`, because a Linux-runner-built image can never match
  a darwin eval — so **on a Mac you do not get that protection**; check by hand.
- **A failed nix build does not stop the jail.** `AutoLoadImage` silently falls back to the
  loaded image, so a broken flake looks like a working jail on stale code. Watch the build
  output.

### If you are an agent doing this work

Two constraints that have burned agents in this repo repeatedly:

1. **Use `mktemp -d` for `$HOME` and `XDG_CONFIG_HOME` in every probe**, including probes run
   inside a nested jail — nested jails SHARE the outer home, so a nested probe with a default
   `$HOME` hits live state. At risk: `~/.claude.json`, `~/.claude/settings.json`,
   `~/.local/share/yolo-jail/`. Three agents have corrupted live config this way.
2. **Mutation-test, and check WHERE the mutation landed.** Twice in one session a mutation
   "passed" because it hit the wrong function or caused a compile error rather than a
   behavioral change. A green suite under mutation means the test is not testing what you
   think.

---

## 7. What is explicitly NOT in scope

- **Extracting `render` into a separate util** — settled *no*
  (`host-render-target.md` §2.3, 2026-07-27). The field census puts the boundary through the
  middle of a single manifest.
- **`yolo cache relocate`** (cache-relocation item 11) — *held*, not deferred. The maintainer
  is not convinced it should exist.
- **`yolo --at host -- <cmd>`** (noncontainer-nix-environment §8 Option 2) — a real option, but a
  bigger product claim ("yolo launches your host agent"). Not required for Phase 7.

---

## 8. The one hard risk, restated

Phase 1.4 and Phase 7 both touch the **A12-fatal boot path**. A regression there does not
misconfigure an agent — it stops jails from *starting*, including the one you are working in.

The retirement method is a **byte-equality check of every shipped pack's rendered surfaces,
before and after**. That mechanism already exists as the render fingerprint gate
(`internal/entrypoint/renderfingerprint_test.go`) and it is the primary safety net for this
work: a change that is supposed to be macOS-only or host-only must leave the jail fingerprint
**unchanged**. If it moves, you changed what packs write in a jail.

Two structural constraints to preserve: `entrypoint` must **not** gain a `cli` dependency (the
edge runs `cli`→`entrypoint`; `internal/render` sits below both), and `liveTables` + `genStep`'s
A12 fail-closed policy stay in the *caller*, not the renderer — that split is what lets a host
target's refusal be a message while the jail stays loud-and-halting.
