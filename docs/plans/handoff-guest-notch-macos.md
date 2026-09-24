# Handoff: the `guest` notch, and the macOS work only a Mac can finish

**Audience:** the maintainer on a Mac, and any agent working there. Written 2026-08-03 from
inside a Linux jail, which is exactly why this document exists: everything below was either
verified by reading code or is explicitly marked as unverifiable from here.

**Status:** DECIDED, 2026-09-23 — a handoff that owes work, not a ruling: all four of its questions
are answered ([§9](#9-open-questions)), and Phase 7 is unbuilt and host-gated. Written 2026-08-03 and
restamped 2026-08-23; [§§1](#1-what-the-three-notches-are-and-why-the-middle-one-matters), 3, 7, 8
are unchanged from the first date. `guest` is the one notch of three that does not work. Phases
0–6, 8, and 9 of [`environment-manager-plan.md`](environment-manager-plan.md) are shipped;
**Phase 7 is not built**, and it is host/Mac-gated rather than blocked on any design
decision. **What moved since 2026-08-03:** [§2](#2-the-bug-that-was-fixed-blind--your-first-job-is-to-run-it)'s item 1.4 is now *half*-answered rather than
wholly unverified (the confinement half was measured on a Mac 2026-08-19), and [§5](#5-the-nix-prerequisite--shipped-verified-2026-08-23)'s nix
prerequisite has **shipped** — it is no longer a prerequisite, it is done. The four questions
[§9](#9-open-questions) collected are all answered: a Mac session on 2026-09-11 settled the two only a
Mac could, and a commit settled the MCP-wrapper one.

**Needs your ruling:** None.

**Reads with:** [`environment-manager-plan.md`](environment-manager-plan.md) Phase 7 (the
spec), [`../reference/macos-user-nix-and-features.md`](../reference/macos-user-nix-and-features.md)
(the existing backend — see the correction in [§2](#2-the-bug-that-was-fixed-blind--your-first-job-is-to-run-it) before trusting it),
[`../guides/macos.md`](../guides/macos.md) (usage),
[`../design/provisioner-evidence.md`](../design/provisioner-evidence.md#37-macos-vs-linux-coverage-freshness-and-the-traps)
(macOS vs Linux coverage and freshness — split out of `provisioner-sets.md` on 2026-09-20)
and [`../design/provisioner-sets.md`](../design/provisioner-sets.md#10-alternatives-each-with-a-verdict)'s alternatives (alternative H, formerly Option 1, **was** a prerequisite for 7.2 and is now shipped — see [§5](#5-the-nix-prerequisite--shipped-verified-2026-08-23) below),
[`macos-revival-and-distribution-plan.md`](macos-revival-and-distribution-plan.md) (the other
Mac-gated ledger; its Track M and this doc's [§4](#4-what-else-on-the-mac-is-gated-beyond-phase-7) overlap), and [`roadmap.md`](roadmap.md).

> [!WARNING]
> **Every [`roadmap.md`](roadmap.md) ID this document cites has stopped resolving, verified 2026-08-23.**
> [`roadmap.md`](roadmap.md) has been rewritten since 2026-08-03 and contains **no** `B-0`, `B-1`, `N1`, `N2`,
> or numbered "ROADMAP item N" entries — `rg -n 'B-0|B-1|\bN1\b|\bN2\b' docs/plans/roadmap.md`
> returns nothing.
> The IDs are kept here rather than deleted, because they are the spelling the git history and
> the sibling design docs use; treat them as **archival labels, not live pointers**. Where the
> content survives, this restamp names the file:line instead. [`roadmap.md`](roadmap.md)'s current macOS
> state lives in its 🔒 *Waiting* section.

---

## 1. What the three notches are, and why the middle one matters

`confinement` has three notches. The dial is real and shipped; what varies is how much of it
each notch honors.

| Notch | What it is | Status |
|---|---|---|
| `jail` | container, disposable home, no host credentials | **works** — the default, unchanged |
| `guest` | a real home, LSM-confined, no image | **NOT BUILT** — this handoff |
| `host` | your actual `$HOME`, no confinement | **works** — `yolo apply --at host` |

The gap is not cosmetic. `jail` and `host` are the two extremes, so a user who needs *some*
isolation but real credentials has nothing to select — they take `host` and lose all of it.
The design calls this "a three-notch story with a broken middle" and Phase 7 is where it gets
fixed.

---

## 2. The bug that was fixed blind — your first job is to run it

**Item 1.4 is wired as of 2026-08-12, from Linux. It is now HALF-answered, and the half that
is still open is not the half this section originally worried about.** Do it before anything
else in this document: everything below assumes packs reach the sandbox.

**What the 2026-08-19 Mac session settled.** The measurement was made by extracting the
Seatbelt profile a real `--dry-run` emits for the Mac checkout and running the actual work
under `sandbox-exec` — so it exercises the *confinement*, not the launcher. Inside that
profile the sandbox **can read the staged pack root and run the toolchain**: `go build
./...`, the full `go test -short ./...` (all 58 packages) and `just test-fast` all pass, git
works, and nix reaches the daemon (`Trusted: 1`). Isolation holds where it matters — host SSH
keys, `~/.claude`, `~/.aws`, `~/.dotfiles` and the keychains are all `Operation not
permitted`. One real profile bug was found and fixed by that run ([§6](#6-how-to-verify-on-a-mac--the-traps), the ancestor-grant
trap).

**What is therefore STILL untested is the `sudo -u _yolojail` staging step ABOVE the
sandbox** — the root-owned copy into `/var/yolo-jail/packs/<session>` and the sandbox-uid
read of it — **not the confinement underneath.** That is a narrower target than "does 1.4
work at all", and it is the one thing to aim the first Mac session at.

> [!WARNING]
> **Do not read the 2026-08-19 result as "the macos-user launch works."** It proves the
> Seatbelt profile is good enough to develop inside. The **launch** — `sudo -u _yolojail` plus
> bootstrap — has never run end-to-end on a current build, and on the maintainer's Mac it
> **cannot**, because that machine's config still uses the removed `agents` key (see [§4](#4-what-else-on-the-mac-is-gated-beyond-phase-7)). The
> confinement half is done; the user-switch around it is not.

What was wrong: `internal/cli/run/run.go`'s `rt == "macos-user"` branch returned at the
`MacosUserRun` call, which sat **before `stagePacks`**, so `YOLO_PACK_ROOT` was never set and
`RunDarwinBootstrap`'s `LoadJailPacks` / `ConfigurePackSurfaces` / `RunPackHooks` loops each
ran over an empty list. A backend that looked provisioned and configured nothing.

What is there now (recorded at the time as [`roadmap.md`](roadmap.md) B-0, an ID that no longer resolves —
see the warning in the header):

- pack staging runs **above the backend dispatch**, so no backend is reachable without one;
- the staged tree is copied to `/var/yolo-jail/packs/<session>` (root-owned, `a+rX`) — the
  analogue of the container's `:ro` `/ctx/packs`, and NOT a pointer into the invoking user's
  home, which is the boundary this backend exists to enforce;
- `YOLO_PACK_ROOT` is baked into the bootstrap argv, and `PlanInvariants` refuses a plan
  where the staging and the variable disagree.

**How to check it in two commands.** `yolo run --dry-run` now prints a `packs:` line — the
staged root, or `none staged` (`internal/macosuser/orchestrator.go:326`, verified 2026-08-23).
Then a real launch: the sandbox home should hold the surfaces the selected packs declare
(`~/.claude/settings.json` for the `claude` pack). If the dry run names a root and the launch
renders nothing, the suspect is the **sudo stage commands or the sandbox-uid read** — those
are the only parts no Linux test can reach, and after 2026-08-19 they are the only parts left
unmeasured at all.

> **Two more things on this backend are still unfixed**, so a working pack render is not the
> whole story: skills and briefings do not reach a macos-user home at all (they cross into a
> container as bind mounts, and there are none here), and B-1's four defects stand.
>
> **Confirmed still true 2026-08-23, and now with a line number.** This backend has **no bind
> mounts of any kind** — `internal/macosuser/runplan.go:186` and
> `internal/macosuser/seatbelt.go:25` both say so, and
> a `host_files` entry that carries a `source` is FILTERED OUT rather than rendered with an
> empty host layer, because there is no `/ctx/host-user` to carry it. The same absence is why
> config `mounts` (`/ctx/...`) does not reach this backend either.
>
> **⚠ Superseded in part, 2026-09-13 — kept because it is a dated record of what was
> confirmed.** The mount half stands: this backend still has none. The *consequence* does
> not. DP-L1 delivers a source-bearing `host_files` entry, and every pack `reads-host`
> grant, by a host-side COPY into a root-owned tree the sandbox reads
> (`internal/cli/run/macosctxtree.go`) — no mount involved — so the filter is no longer
> an accepted deficiency for a FILE source. A DIRECTORY source is still undelivered, now
> with a warning that names it, and config `mounts` is still silent.

> The old version of this section warned that `macos-user-nix-and-features.md:174` claimed
> pack selection worked here when it did not. That row now reads ⚠️ with the Mac-unverified
> caveat. The lesson that produced it is the one to keep: **verify against code, not against
> markers** — and, now, against a marker that says a Linux agent wired something it could
> not run.

The abstraction question 1.4 was supposed to answer — can `render.Target` express a
non-container backend? — came back **not yet, and it did not need to**: macos-user renders at
the JAIL notch (`Env.renderTarget()` → `render.Jail`) with a real macOS home, exactly as
before, so no new Kind was required. [§3](#3-phase-7-as-specified)'s `guest` work is still where a Target has to describe
a non-container confinement for the first time.

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

Collected here so one trip to a Mac can close all of it. **Rechecked 2026-08-23** — two rows
have closed, one has changed shape, and one is new and blocks every other row on this list.

| Item | What is needed | Where |
|---|---|---|
| **🔴 The Mac's config** | **Do this first or nothing below can run.** Measured 2026-08-19: that machine's `~/.config/yolo-jail/config.jsonc` still uses the **removed `agents` key**, so every current `yolo` — every backend, `yolo check` included — refuses with the config-invalid fatal, and its installed `yolo` was **531 commits stale**. All four names it selects (`claude`, `pi`, `codex`, `agy`) exist as packs; the fix is renaming the key to `packs`. **Maintainer's config, maintainer's call** | [`roadmap.md`](roadmap.md) 🔒 macOS rows |
| **Item 1.4's staging step** | The `sudo -u _yolojail` copy into `/var/yolo-jail/packs/<session>` + the sandbox-uid read. The confinement half was measured 2026-08-19; **this half never has been** | [§2](#2-the-bug-that-was-fixed-blind--your-first-job-is-to-run-it) |
| **D4 Cachix** | ONE real download proof, and it is now genuinely the only item. **The push question is SETTLED (2026-09-02, [OQ-GN3](#9-open-questions)):** run `31749547095` (`v0.8.0`, both arches) pushed both variants and substituted the four this-repo-source paths back from the cache. Substituter live at `flake.nix:13-16`; cache + account + token all done. Note the cache holds `v0.8.0` only (tag-triggered push), and the CI `--accept-flake-config` omission that made off-release runs miss the cache entirely is fixed | [`handoff-cachix-cache.md`](handoff-cachix-cache.md), [`macos-revival-and-distribution-plan.md`](macos-revival-and-distribution-plan.md) D4 |
| ~~**E8's nightly**~~ | **CLOSED, and its stated cause was wrong.** `BACKLOG.md:208` marks E8 done 2026-08-03, and the macOS nightly is **GREEN** as of run `32623453131` (2026-08-23). The row said the nightly stayed red until the multi-arch builder image reached GHCR — but the nightly builds the image on `ubuntu-latest` and downloads it as an artifact (`nightly-macos.yml`, `build-image` → `integration-macos`); it never pulls the GHCR builder. **The 29 red nights were the flake throwing on `x86_64-darwin`**, fixed by `927fb9f` (2026-08-18). *(v0.8.0 did ship 2026-08-13, so `publish.yml` has run since E8's fix — whether GHCR carries the multi-arch index is not verifiable from here.)* | [`BACKLOG.md`](BACKLOG.md) E8 |
| **agent-auth macos-user parity** | 4 verified defects whose fixes need a Mac to verify. *(The "ROADMAP item 4" pointer is dead; the defects are in the agent-auth design doc.)* | [`../design/agent-auth-modes.md`](../design/agent-auth-modes.md) |
| **`cache_relocations`** | One real cross-filesystem move as an acceptance step. Still **held** — [`roadmap.md`](roadmap.md) keeps it in 🧊 Icebox as genuinely undecided, not merely unscheduled | [`cache-relocation.md`](cache-relocation.md) |
| ~~**`yoloDarwinPackages` rename**~~ | **SHIPPED — see [§5](#5-the-nix-prerequisite--shipped-verified-2026-08-23).** No longer Mac-gated to write *or* to prove on Linux; only a `packages:` launch on a Mac would exercise it there | [`../design/provisioner-sets.md` §10](../design/provisioner-sets.md#10-alternatives-each-with-a-verdict) alternative H (formerly Option 1) |
| **MCP wrappers on macOS** | *New, found 2026-08-23.* `internal/entrypoint/darwin.go:59` runs `GenerateMCPWrappers` unconditionally, and the bodies are Linux-absolute — `/usr/bin/chromium` (`mcp_wrappers.go:39`), `exec /bin/node` (`:74`), `/etc/fonts` (`:26-27`). A macos-user home gets three wrappers pointing at paths macOS does not have. Harmless until one is exec'd | revival plan, Open decision #4 |

---

## 5. The nix prerequisite — SHIPPED (verified 2026-08-23)

> **This section is no longer a prerequisite; it is a description of what exists.** All four
> bullets landed. It is kept, rather than deleted, because the reasoning below is why Phase
> 7.2 is unblocked, and because the trap at the end of the section is still live.

[`provisioner-sets.md` §10](../design/provisioner-sets.md#10-alternatives-each-with-a-verdict) **alternative H** (formerly `noncontainer-nix-environment.md`'s Option 1)
was a prerequisite for Phase 7.2, and NOT because of anything about the `host` notch —
`guest` is a real home with no image, so it needs a tool closure for exactly the reason `host`
does. Each bullet, with what it became:

- ~~rename `yoloDarwinPackages` → something system-neutral (`yoloHostPackages`)~~ — **done,
  under a different name than the one proposed here.** The attr is
  `packages.yoloNoncontainerPackages` (`flake.nix:1204`), surfaced as
  `darwinpkg.ProfileAttr` (`internal/darwinpkg/darwinpkg.go:30`); the skip list is
  `yoloUnavailablePackages` (`flake.nix:1210`, `darwinpkg.UnavailableAttr`).
  `internal/darwinpkg/flakeattr_test.go:55` pins **both** old spellings —
  `yoloDarwinPackages` and `darwinUnavailablePackages` — as dead, so a resurrected reference
  fails the suite rather than silently evaluating to nothing.
- ~~stop hardcoding `aarch64-darwin` in `internal/darwinpkg`~~ — **done.**
  `darwinpkg.NativeSystem()` derives the nix double from `runtime.GOOS`/`GOARCH`
  (`internal/darwinpkg/darwinpkg.go:46-55`), and its own comment records that it *"replaces a
  `DarwinSystem = \"aarch64-darwin\"` constant"*.
- ~~add a gcroot~~ — **done, and as an `--out-link` rather than a follow-up `nix-store
  --add-root`.** `internal/darwinpkg/gcroot.go` is the whole argument for *where* the root
  lives; `materialize.go:33` and `darwinpkg.go:122` record that this is the N1 fix.
- ~~make `describe` / `check --at host` **report** the resolved profile path~~ — **done, on
  both.** `internal/cli/describe.go` and `checkPackageProfile` (`internal/cli/check/section_packageprofile.go`) each
  read `darwinpkg.ProfileRootLink(paths.Home())`. Note the deliberate design in the check
  comment (`checkPackageProfile`'s doc comment): it reads the **GC-root symlink, never nix**, because
  check owns exactly one place a real build is allowed; and it splits PASS/WARN/FAIL by what
  the user can act on — an *absent* root is a WARN (it is also the normal pre-first-run
  state), while a root pointing at a **collected** store path is a FAIL, because that is
  precisely the defect N1 fixed.

One finding from that doc worth carrying: `packages.yoloDarwinPackages` was **already
per-system** and resolved for `x86_64-linux`, so the name was the lie, not the mechanism —
which is why the fix was a rename plus `NativeSystem()`, not new machinery.

> [!WARNING]
> **The trap that produced this section is still live, and it has now fired twice.**
> **This is the same class of bug as BACKLOG E8**, which was fixed 2026-08-03: a hardcoded
> `aarch64-*` string that was true of an Apple Silicon Mac and wrong everywhere else. E8
> turned out to have **three** instances of that assumption, not the one its entry named —
> found only by grepping the literal instead of trusting the entry's stated scope. Do the
> same here: `rg -n 'aarch64' internal/ flake.nix` before assuming `darwinpkg` is the only
> site.
>
> **And it fired a second time, from the opposite direction (2026-08-18).** `x86_64-darwin`
> is the assumption nobody made: nixpkgs 26.11 **throws** on that system, and because the
> flake's `pkgs` is evaluated for every system `flake-utils` enumerates, the throw took out
> *every* host-side nix call on an Intel Mac — including `nix eval .#installPrefix`, which is
> what the integration suite's staleness oracle runs. The macOS nightly went red for **29
> consecutive nights** and the recorded diagnosis was *"nix is broken on that runner, not in
> our tree"* — the exact opposite of true, and reproducible in 0.2s. The fix pins
> `nixpkgs-26.05-darwin` for that one system (`flake.nix:22-42`, `927fb9f`). **26.05 is the
> LAST branch supporting `x86_64-darwin` and is security-fixed only to the end of 2026**, so
> this is a deadline, not a fix — see [`../research/macos-support-matrix.md`](../research/macos-support-matrix.md)
> [§0](../research/macos-support-matrix.md#0-the-platform-deadline--x86_64-darwin-is-on-a-clock).

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
- **A failed nix build STOPS the jail** (fatal since 2026-08-15). `AutoLoadImage` used to fall back silently to the
  loaded image, so a broken flake looks like a working jail on stale code. Watch the build
  output.
- **The suite's darwin warmup is now SKIPPED, on purpose — do not "fix" it back** (added
  2026-08-23, `e5b60902`, `integration/harness_test.go:147-153`). A warmup exists to pre-pay a
  **container start**; on darwin every launch **realises an image**, because a loaded image can
  never match a darwin `nix eval`. So the warmup was a full nix build wearing a warmup's name —
  measured at **12m0s of waste** per nightly. `warmJail` now returns early on
  `GOOS == "darwin"` with that measurement in its log line, and the first container test
  absorbs the one-time cost. On Linux CI the premise holds and the warmup still earns its
  1m56s. **If a darwin run looks slow in its first container test, that is the design.**
- **A test fixture chosen for convenience can pick the one input that cannot fail**
  (`2e327fa2`, found by the 2026-08-19 sandbox run — the roadmap cited a `533ccc1` that does not
  resolve, and was **corrected to this SHA on 2026-08-23**). The Seatbelt profile granted `/Users`, `/Users/Shared` and the
  workspace subpath, and its comment asserted *"the workspace is NOT under any `/Users/<name>`
  home, so no ancestor grant is needed"* — true only at depth ONE, and the shipped test used
  `/Users/Shared/proj`, the single depth where the gap is invisible, **while asserting the
  absence of an ancestor grant as if it were the invariant**. A real workspace at
  `/Users/Shared/yolo/yolo-jail` therefore left `/Users/Shared/yolo` denied. Note how it
  presented: `git ls-files` walks up looking for the repo boundary and reported `fatal:
  Invalid path …` — a *broken repo* — and `just format` then died on the empty list. Two
  errors, neither naming the sandbox.

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
  (`host-render-target.md` [§2.3](../design/host-render-target.md#23-extraction-settled-and-the-answer-is-no), 2026-07-27). The field census puts the boundary through the
  middle of a single manifest.
- **`yolo cache relocate`** (cache-relocation item 11) — *held*, not deferred. The maintainer
  is not convinced it should exist.
- **`yolo --at host -- <cmd>`** ([`provisioner-sets.md` §10](../design/provisioner-sets.md#10-alternatives-each-with-a-verdict) alternative I) — a real option, but a
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

---

## 9. Open Questions

No question is open. All four this handoff filed are answered — three by a Mac session and one by
a commit — and each is a ledger row below. The ids are stable; commits and sibling docs cite them.

| ID | Ruling / Answer | Date | Settled by |
| :--- | :--- | :--- | :--- |
| OQ-GN1 | **Yes — the `sudo -u _yolojail` pack staging reaches the sandbox.** `StagePackCommands` staged the session's two local packs (44 + 3 files) and the surfaces rendered from them on real hardware, including a `config-overlay` key reaching `claude/settings`. So the pack system has one backend story, not two, and the statements in this document that assume packs reach the sandbox hold. | 2026-09-11 | A Mac session, recorded in [`macos-support-matrix.md`](../research/macos-support-matrix.md) ([§4](../research/macos-support-matrix.md#4-whats-proven-vs-whats-the-next-gate), item 2) |
| OQ-GN2 | **Renamed, by hand.** The Mac's config uses `packs`, `yolo check` is green there (41 passed, 4 warnings) and the installed binary is current. The reason it was a question still holds for the next one: an agent does not edit a maintainer's personal host config. | 2026-09-11 | The same session ([`macos-support-matrix.md`](../research/macos-support-matrix.md#4-whats-proven-vs-whats-the-next-gate), item 1) |
| OQ-GN3 | **Pushed, and the cache is also being read.** Run `31749547095` (`v0.8.0`, 2026-08-13), job `push-image-cache`, both arches green, logged `Pushed image closures to yolo-jail.cachix.org`, and the same run substituted four paths **from** the cache. So D4's remaining work is one item, the Mac download proof. | 2026-09-02 | The Actions log |
| OQ-GN4 | **Skip on macOS, and say so — not ported.** The darwin generation entry writes no MCP wrappers, because their bodies are Linux-absolute (`/usr/bin/chromium`, `exec /bin/node`, `/etc/fonts`) and a darwin variant would have to guess at Chrome, node and fontconfig on a machine yolo did not provision. | 2026-09-03 | `d28f951f` (`internal/entrypoint/darwin.go`, the `NO MCP WRAPPERS HERE` comment) |

> [!WARNING]
> **`--accept-flake-config` is load-bearing for the Mac download proof** (found chasing [OQ-GN3](#9-open-questions),
> now fixed). All six CI `nix build` calls lacked it, so nix discarded the flake's own substituter
> with a warning; the push job read the cache only because `cachix-action` adds the substituter to
> `nix.conf` itself, and `ci.yml`, `nightly-macos.yml` and `packs.yml` were rebuilding the closure
> from source every run. The flag is what makes `yolo check`'s "served from the binary cache" claim
> true off a release runner.
