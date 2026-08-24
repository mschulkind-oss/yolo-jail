# Handoff: the `guest` notch, and the macOS work only a Mac can finish

**Audience:** the maintainer on a Mac, and any agent working there. Written 2026-08-03 from
inside a Linux jail, which is exactly why this document exists: everything below was either
verified by reading code or is explicitly marked as unverifiable from here.

**Status:** **HANDOFF — HOST-GATED, restamped 2026-08-23** (written 2026-08-03; §§1, 3, 7, 8
are unchanged from that date). `guest` is the one notch of three that does not work. Phases
0–6, 8, and 9 of [`environment-manager-plan.md`](environment-manager-plan.md) are shipped;
**Phase 7 is not built**, and it is host/Mac-gated rather than blocked on any design
decision. **What moved since 2026-08-03:** §2's item 1.4 is now *half*-answered rather than
wholly unverified (the confinement half was measured on a Mac 2026-08-19), and §5's nix
prerequisite has **shipped** — it is no longer a prerequisite, it is done. Four live
questions are collected in §9; three of them are questions only a Mac can answer.

**Reads with:** [`environment-manager-plan.md`](environment-manager-plan.md) Phase 7 (the
spec), [`../design/macos-user-nix-and-features.md`](../design/macos-user-nix-and-features.md)
(the existing backend — see the correction in §2 before trusting it),
[`../guides/macos.md`](../guides/macos.md) (usage),
[`../design/noncontainer-nix-environment.md`](../design/noncontainer-nix-environment.md) §5
and §8 (Option 1 **was** a prerequisite for 7.2 and is now shipped — see §5 below),
[`macos-revival-and-distribution-plan.md`](macos-revival-and-distribution-plan.md) (the other
Mac-gated ledger; its Track M and this doc's §4 overlap), and [`roadmap.md`](roadmap.md).

> [!WARNING]
> **Every `roadmap.md` ID this document cites has stopped resolving, verified 2026-08-23.**
> `roadmap.md` has been rewritten since 2026-08-03 and contains **no** `B-0`, `B-1`, `N1`, `N2`,
> or numbered "ROADMAP item N" entries — `rg -n 'B-0|B-1|\bN1\b|\bN2\b' docs/plans/roadmap.md`
> returns nothing.
> The IDs are kept here rather than deleted, because they are the spelling the git history and
> the sibling design docs use; treat them as **archival labels, not live pointers**. Where the
> content survives, this restamp names the file:line instead. `roadmap.md`'s current macOS
> state lives in its 🔒 *Waiting* section.

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
permitted`. One real profile bug was found and fixed by that run (§6, the ancestor-grant
trap).

**What is therefore STILL untested is the `sudo -u _yolojail` staging step ABOVE the
sandbox** — the root-owned copy into `/var/yolo-jail/packs/<session>` and the sandbox-uid
read of it — **not the confinement underneath.** That is a narrower target than "does 1.4
work at all", and it is the one thing to aim the first Mac session at.

> [!WARNING]
> **Do not read the 2026-08-19 result as "the macos-user launch works."** It proves the
> Seatbelt profile is good enough to develop inside. The **launch** — `sudo -u _yolojail` plus
> bootstrap — has never run end-to-end on a current build, and on the maintainer's Mac it
> **cannot**, because that machine's config still uses the removed `agents` key (see §4). The
> confinement half is done; the user-switch around it is not.

What was wrong: `internal/cli/run/run.go`'s `rt == "macos-user"` branch returned at the
`MacosUserRun` call, which sat **before `stagePacks`**, so `YOLO_PACK_ROOT` was never set and
`RunDarwinBootstrap`'s `LoadJailPacks` / `ConfigurePackSurfaces` / `RunPackHooks` loops each
ran over an empty list. A backend that looked provisioned and configured nothing.

What is there now (recorded at the time as `roadmap.md` B-0, an ID that no longer resolves —
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
> `internal/macosuser/macosuser_test.go:294-300` pins it as an *accepted deficiency*: a
> `host_files` entry that carries a `source` is FILTERED OUT rather than rendered with an
> empty host layer, because there is no `/ctx/host-user` to carry it. The same absence is why
> config `mounts` (`/ctx/...`) does not reach this backend either.

> The old version of this section warned that `macos-user-nix-and-features.md:174` claimed
> pack selection worked here when it did not. That row now reads ⚠️ with the Mac-unverified
> caveat. The lesson that produced it is the one to keep: **verify against code, not against
> markers** — and, now, against a marker that says a Linux agent wired something it could
> not run.

The abstraction question 1.4 was supposed to answer — can `render.Target` express a
non-container backend? — came back **not yet, and it did not need to**: macos-user renders at
the JAIL notch (`Env.renderTarget()` → `render.Jail`) with a real macOS home, exactly as
before, so no new Kind was required. §3's `guest` work is still where a Target has to describe
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
| **🔴 The Mac's config** | **Do this first or nothing below can run.** Measured 2026-08-19: that machine's `~/.config/yolo-jail/config.jsonc` still uses the **removed `agents` key**, so every current `yolo` — every backend, `yolo check` included — refuses with the config-invalid fatal, and its installed `yolo` was **531 commits stale**. All four names it selects (`claude`, `pi`, `codex`, `agy`) exist as packs; the fix is renaming the key to `packs`. **Maintainer's config, maintainer's call** | `roadmap.md` 🔒 macOS rows |
| **Item 1.4's staging step** | The `sudo -u _yolojail` copy into `/var/yolo-jail/packs/<session>` + the sandbox-uid read. The confinement half was measured 2026-08-19; **this half never has been** | §2 |
| **D4 Cachix** | ONE real download proof. Substituter live at `flake.nix:13-16`; cache + account + `CACHIX_AUTH_TOKEN` all done. **Sources disagree on whether the first push has happened** — `README.md:31` says CI already pushed, `handoff-cachix-cache.md` still lists it as remaining; not checkable from a Linux jail | [`handoff-cachix-cache.md`](handoff-cachix-cache.md), [`macos-revival-and-distribution-plan.md`](macos-revival-and-distribution-plan.md) D4 |
| ~~**E8's nightly**~~ | **CLOSED, and its stated cause was wrong.** `BACKLOG.md:208` marks E8 done 2026-08-03, and the macOS nightly is **GREEN** as of run `32623453131` (2026-08-23). The row said the nightly stayed red until the multi-arch builder image reached GHCR — but the nightly builds the image on `ubuntu-latest` and downloads it as an artifact (`nightly-macos.yml`, `build-image` → `integration-macos`); it never pulls the GHCR builder. **The 29 red nights were the flake throwing on `x86_64-darwin`**, fixed by `927fb9f` (2026-08-18). *(v0.8.0 did ship 2026-08-13, so `publish.yml` has run since E8's fix — whether GHCR carries the multi-arch index is not verifiable from here.)* | [`BACKLOG.md`](BACKLOG.md) E8 |
| **agent-auth macos-user parity** | 4 verified defects whose fixes need a Mac to verify. *(The "ROADMAP item 4" pointer is dead; the defects are in the agent-auth design doc.)* | [`../design/agent-auth-modes.md`](../design/agent-auth-modes.md) |
| **`cache_relocations`** | One real cross-filesystem move as an acceptance step. Still **held** — `roadmap.md` keeps it in 🧊 Icebox as genuinely undecided, not merely unscheduled | [`cache-relocation.md`](cache-relocation.md) |
| ~~**`yoloDarwinPackages` rename**~~ | **SHIPPED — see §5.** No longer Mac-gated to write *or* to prove on Linux; only a `packages:` launch on a Mac would exercise it there | [`../design/noncontainer-nix-environment.md`](../design/noncontainer-nix-environment.md) §8 Option 1 |
| **MCP wrappers on macOS** | *New, found 2026-08-23.* `internal/entrypoint/darwin.go:59` runs `GenerateMCPWrappers` unconditionally, and the bodies are Linux-absolute — `/usr/bin/chromium` (`mcp_wrappers.go:39`), `exec /bin/node` (`:74`), `/etc/fonts` (`:26-27`). A macos-user home gets three wrappers pointing at paths macOS does not have. Harmless until one is exec'd | revival plan, Open decision #4 |

---

## 5. The nix prerequisite — SHIPPED (verified 2026-08-23)

> **This section is no longer a prerequisite; it is a description of what exists.** All four
> bullets landed. It is kept, rather than deleted, because the reasoning below is why Phase
> 7.2 is unblocked, and because the trap at the end of the section is still live.

[`noncontainer-nix-environment.md`](../design/noncontainer-nix-environment.md) §8 **Option 1**
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
  both.** `internal/cli/describe.go:94` and `internal/cli/check/sections_macos.go:119` each
  read `darwinpkg.ProfileRootLink(paths.Home())`. Note the deliberate design in the check
  comment (`sections_macos.go:105-118`): it reads the **GC-root symlink, never nix**, because
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
> §0.

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

---

## 9. Open Questions

Four questions this handoff cannot answer from a Linux jail. Three of them need a Mac; the
fourth needs a ruling. IDs are stable — cite them from commits and sibling docs.

1. 💬 **OQ-GN1: Does the `sudo -u _yolojail` pack staging actually reach the sandbox?**
   This is the surviving half of item 1.4 (§2). The confinement half was measured on
   2026-08-19 — the sandbox reads the staged pack root and runs the toolchain — so what is
   left is the root-owned copy into `/var/yolo-jail/packs/<session>` (`a+rX`) and the
   sandbox-uid read of it. **What it decides:** whether *every other statement in this
   document* holds, since all of them assume packs reach the sandbox; and whether the pack
   system has one backend or two.

   _Leaning:_ It works. `PlanInvariants` already refuses a plan where the staging path and
   `YOLO_PACK_ROOT` disagree, and the 2026-08-19 run proves the read side of the boundary.
   The residual risk is ACL/ownership on the copy, not the design — which is why the check is
   cheap (`yolo run --dry-run` for the `packs:` line, then look for
   `~/.claude/settings.json` in the sandbox home).

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-GN2: Rename the Mac's `agents` key to `packs`, or leave that machine as-is?**
   Measured 2026-08-19: the Mac's `~/.config/yolo-jail/config.jsonc` still uses the **removed**
   `agents` key, so no current `yolo` launches there on any backend — `yolo check` included —
   and its installed `yolo` was 531 commits stale. All four selected names (`claude`, `pi`,
   `codex`, `agy`) exist as packs, so the rename is the entire fix. **What it decides:**
   whether the next Mac session can run *anything* on this list. It gates OQ-GN1, D4's
   download proof, and the whole of §4.

   _Leaning:_ Rename it — but **an agent should not touch a maintainer's personal host
   config**, which is the only reason this is a question rather than a commit. Doing it as the
   first step of the next Mac session, by hand, costs one minute.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 **OQ-GN3: Has the Cachix cache actually been pushed to?** Two docs in this repo
   disagree: `docs/plans/README.md:31` says CI has already pushed data;
   [`handoff-cachix-cache.md`](handoff-cachix-cache.md) still lists the first push as
   remaining. **Neither is checkable from a Linux jail** — it needs a look at the cache or at
   a release run. **What it decides:** whether D4's remaining work is one item (the Mac
   download proof) or two (push, then download), and therefore whether a Mac visit can close
   D4 at all.

   _Leaning:_ Pushed. v0.8.0 shipped 2026-08-13 and the `push-image-cache` job is
   release-gated on the `CACHIX_AUTH_TOKEN` secret alone, which is set — so a release has run
   under the conditions the job needs. But that is inference from the workflow, not
   observation of the cache, and the two docs disagreeing is itself the evidence that nobody
   has looked.

   **Answer:**
   > _(empty — fill in when decided)_

4. 💬 **OQ-GN4: The three Linux-path MCP wrappers a macos-user home gets — skip, port, or
   document as dead?** `internal/entrypoint/darwin.go:59` runs `GenerateMCPWrappers`
   unconditionally, and the bodies hardcode `/usr/bin/chromium`, `exec /bin/node` and
   `/etc/fonts` (`internal/entrypoint/mcp_wrappers.go:26-27,39,72-74`) with no `GOOS` guard.
   The revival plan's J2 step 2 *decided* to skip them natively and document the gap; the tree
   does neither. **What it decides:** a small correctness question now, and a real one once
   `guest` renders the same surfaces on both platforms — §3's "portable surface set" cannot
   include a wrapper that is portable in name only.

   _Leaning:_ Guard the generation on `GOOS` and say so, rather than porting. A darwin
   chromium wrapper is a real feature with real paths to get right; three dead files are a
   defect with a one-line fix. Port later if someone actually wants chrome-devtools-mcp on a
   Mac.

   **Answer:**
   > _(empty — fill in when decided)_
