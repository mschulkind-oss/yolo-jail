# Proposed fixes for the open findings

**Status (2026-08-03, re-checked 2026-08-23):** **ALL TEN ITEMS DECIDED; NINE SHIPPED.** #1's PATH split, #4, #5, #7 landed
2026-08-02; #2, #3, #6 landed 2026-08-03; **#8** (parity table), **#9** (multi-arch builder +
GOARCH-derived builder system) and **#10** (host MCP servers) landed 2026-08-03 too.
**One item is genuinely open: #1's baked fallback** — and it is the LEAST urgent thing in this
doc, because the PATH split made the collision it guards against unrepresentable (see #1).
**#11** (new 2026-08-03): a dropped pack's host output was not retired — **SHIPPED the same day**
(`77663ae`, `ecf3564`, `af70f4c`). Written
2026-08-02 in answer to *"do you have proposed fixes for what you're pointing out?"*, then
resolved by nine review rulings the same day; each section carries a shipped note where the
implementation differed from the proposal.

Everything below started as an open item I flagged rather than fixed, each because it needed a
decision I did not think was mine. **Those decisions are now made.** Three rulings changed the
answer rather than confirming it, and each is marked at the item — a doc that quietly absorbed a
reversal would hide the most useful part of the review.

**What changed under review, in one place:**

| # | I proposed | Ruled |
|---|---|---|
| 1 | defer the shim-dir split ("wrong blast radius") | **do it now** — and it becomes the primary fix |
| 2 | decide `requires` *instead of* #1 | **both** — `requires` does not subsume a working fallback |
| 3 | a validation error (one `program` per pack) | **reversed** — no case for constricting packs; return a slice |
| 6 | annotate the nix remedy | **reframed** — prefer the pack's own installer; nix goes stale |
| 6b | keep nix hints "for pinning the closure" | **trimmed** — no such user exists; `detectManager` reaches nix only by elimination, so drop them |
| 9 | no proposal, "a product decision" | **corrected** — it is a CI capability constraint, so it *has* an answer |

Three of those were me reasoning from the wrong premise, each corrected by reading something I
should have read first: a stale identifier name (#3), an unread workflow comment (#9), and an
**invented user** (#6b — I justified keeping the nix hints for someone who "wants the closure
pinned", then `detectManager` turned out to reach nix only by elimination, so that user cannot
select it). All three are recorded at the item with the evidence.

**The #6b pattern is the one to watch for in this doc:** a justification that sounds
architectural but names no reachable user. It accretes exactly the junk the review was
guarding against.

**Reads with:** [`../design/program-kind-defects.md`](../design/program-kind-defects.md)
(Q1.1–Q3.1), [`pack-host-management-plan.md`](pack-host-management-plan.md) Phase 11 and
items 8.3/8.4, [`../design/pack-config-collaboration.md`](../design/pack-config-collaboration.md)
§8, and [`../design/noncontainer-nix-environment.md`](../design/noncontainer-nix-environment.md) OQ-6.

---

## Summary

| # | Finding | Proposal | Confidence |
|---|---|---|---|
**Every item is now decided.** Nine review rulings (2026-08-02) closed the last open questions;
three of them reversed or reframed what I had proposed, and those are marked. Nothing below is
waiting on a decision — this doc is ready to implement against.

| # | Finding | Decided approach | Ruling |
|---|---|---|---|
| 1 | `program` shadows a baked binary and breaks it (11.1 / Q1.1) | **Split the shim dir** — installers move *after* `/bin`, so shadowing is impossible. Baked-in fallback stays as the safety net for a real install failure | **RULED — do it now**; my "wrong blast radius" hedge overruled. **SPLIT SHIPPED 2026-08-02** (`~/.yolo-launchers`); the fallback is still open |
| 2 | Presence-vs-install conflated (Q1.3) | Add a `requires` kind (asserts presence, generates nothing) | **RULED — build it, AND keep #1**; they are not alternatives · **SHIPPED 2026-08-03** |
| 3 | Only the first `program` per pack installs (11.2 / Q2.1) | **Return a slice, generate N launchers** | **REVERSED** — I proposed a validation error; no case for constricting packs · **SHIPPED 2026-08-03** |
| 4 | A dropped pack's staged tree keeps rendering (11.3 / Q3.1) | Prune unconfigured slugs, contents-only, **never** clear-and-restage | context expanded on request |
| 5 | `depcheck.Manifest` cannot express a brew cask (8.4) | `brew-cask` key → `cask "<pkg>"` in the Brewfile | **RULED — just make it work** · **SHIPPED 2026-08-02** |
| 6 | `install_hints` routes agents through nix (8.3) | **Prefer the pack's OWN installer**; **DROP the nix hints for the six agent CLIs** (keep them for real deps like `fd`/`jq`) | **REFRAMED, then TRIMMED** — `copilot` is 16 releases behind, and the "pin the closure" case I invented is unreachable · **SHIPPED 2026-08-03** |
| 7 | `packages: ["claude-code"]` fails at build with a nix trace (nix OQ-6) | `meta.available` check beside `availableOn` | still stands — §6 removes the example, not the defect · **SHIPPED 2026-08-02** |
| 8 | `rmwProvenance` is a second "which layer won" | Parity table now; **unify at the third** derivation | **RULED — wait for 3** · **TABLE SHIPPED 2026-08-03** (`TestProvenanceParityAcrossBothDerivations`); unification still deferred, by ruling |
| 9 | Nightly macOS builder arch mismatch (BACKLOG E8) | Publish the builder multi-arch (or skip the two tests, recorded) | **CORRECTED** — a CI capability constraint, not platform support · **SHIPPED 2026-08-03**, and it was BIGGER than this row: the advertised system was hardcoded in three places, not one (see BACKLOG E8) |
| 10 | A pack cannot install Claude MCP servers on the host | Prune workspace-keyed subtrees instead of refusing the surface | **RULED — warn and wait for confirm** · **SHIPPED 2026-08-03** |
| 11 | A DROPPED pack's host output is never retired (new) | Archive skills/files and drop overlay keys, behind a confirm; stop provenance laundering the keys into `host` | **RULED 2026-08-03 — confirm before removing host files** · **SHIPPED 2026-08-03**, all four rulings · spec at [`host-pack-drop-cleanup.md`](host-pack-drop-cleanup.md) |

---

## 1. The `program` shim shadowing a baked binary (11.1)

> **Revised after review.** Two questions — *"why is a shim involved at all?"* and *"why would
> we allow shim recursion regardless?"* — and the second one **invalidated my original
> proposal.** What follows is the corrected analysis; the rejected design is kept at the end
> because the reason it was wrong is the useful part.

### Why a shim is involved at all — and it is a conflation, not a design

`~/.yolo-shims/` holds **two unrelated mechanisms under one name**, and that is the answer:

```console
$ ls ~/.yolo-shims/
claude   find   grep   pnpm
```

| file | what it is | what it does |
|---|---|---|
| `find`, `grep` | a **blocker** (`GenerateShims`) | refuses, prints a suggestion, `exit 127` |
| `claude`, `pnpm` | a **lazy installer** (`GenerateAgentLaunchers`) | installs on first use, then `exec`s the real binary |

A blocker *must* precede the real tool on PATH — interception is its entire job. A lazy
installer has no such requirement: it only needs to run **when the tool is absent**. They ended
up in one directory because both are "a script named after a binary, early on PATH", and the
generators even coordinate through it (`shims.go:182` skips writing a launcher when a
blocked-tool shim already owns the name).

So the honest framing of 11.1 is: **`program` reuses the blocker's mechanism for a job that is
not blocking.** A blocker that shadows the real binary is correct. An installer that shadows it
and then fails is the defect.

### Why recursion should not be possible — and my first proposal was wrong

My original fix had the launcher discover its own directory at runtime (`BASH_SOURCE`), strip it
from PATH, and re-search. It worked (measured, three cases), but the review question is the
right one: **that is defending against a recursion the design should not permit.**

`GenerateAgentLaunchers` **already knows both paths at write time**:

- `shimDir := e.ShimDir()` (`shims.go:162`), and it builds `shimPath` from it (`:180`);
- `REAL_BIN` is a template constant, `$NPM_CONFIG_PREFIX/bin/<bin>` (`:265`).

A generator that knows where it is writing can resolve the fallback **then**, and bake an
absolute path into the script. Runtime PATH arithmetic inside the script is a workaround for
information the generator had and discarded.

### The corrected proposal

**Resolve the fallback at generation time; bake it in; never search PATH at runtime.**

```bash
# generated, with FALLBACK_BIN substituted as an absolute path (or empty)
if [ -x "$REAL_BIN" ]; then exec "$REAL_BIN" "$@"; fi
if [ -n "__YOLO_FALLBACK__" ] && [ -x "__YOLO_FALLBACK__" ]; then
  echo "  ⚠ __YOLO_BIN__: install unavailable; using __YOLO_FALLBACK__ from the image" >&2
  exec "__YOLO_FALLBACK__" "$@"
fi
echo "  ⚠ __YOLO_BIN__ not available" >&2; exit 1
```

`__YOLO_FALLBACK__` is found by the generator with a PATH lookup **that excludes `shimDir`** —
a directory it already has in hand, so no self-discovery and **no recursion is representable**,
not merely avoided. The warning stays: falling through silently would substitute a different
binary than the pack named, which is the failure mode this codebase spent the night removing.

Two consequences worth stating:

- **Generation-time resolution can go stale** — the image is fixed for a jail's lifetime, so in
  practice it cannot, but a `program` whose fallback vanishes mid-session would exit 1 with the
  same message as before. Acceptable, and strictly better than today.
- **It composes with #2.** If `requires` lands, the fallback is not a rescue path but the
  *primary* path for a baked tool, and `program` narrows to "yolo installs this", where a
  fallback rarely exists at all.

### The deeper fix, which the review implies

If a lazy installer does not need to shadow the real binary, it arguably should not live in the
blocker's directory. **Separating the two mechanisms** — installers in their own dir, ordered
*after* `/bin` rather than before — would make 11.1 structurally impossible instead of handled:
an installer would only ever be reached when nothing else provides the name.

**RULED: do this now.** *"I want to do this now."* So the separation is the fix, and the baked
fallback below becomes a smaller safety net rather than the primary mechanism.

> **SHIPPED 2026-08-02 — the split is in.** Dir name: **`~/.yolo-launchers`** (the doc's own
> suggestion). `entrypoint.Env.LauncherDir()` is the accessor; `entrypoint.BootPath(e)` is now
> the single authority for the order (extracted from `execBash` so it is assertable), and the
> `.bashrc` export mirrors it. The four traps all held as written:
>
> 1. `AGENTS.md` updated in the same change (PATH line + a new invariant bullet naming both
>    mechanisms and the anchor rule).
> 2. The launcher-skip is gone, and the new semantics are pinned by
>    `TestBlockedAndDeclaredToolGetsBothAndBlockerWins`.
> 3. The new anchor needed its own `-v` (`podmanBaseMounts`), backing dir (`prepareWsState`),
>    `GlobalHome` mountpoint (`storage.EnsureGlobalStorage`), and reservation entry
>    (`config.reservedHomeDirRoots`) — plus contents-only clearing via a new shared
>    `resetAnchorDir`. **Trap 3 was the one that actually bit**: running the STALE baked
>    launcher against the new entrypoint aborted the boot with
>    `generate_agent_launchers: open /home/agent/.yolo-launchers/claude: read-only file system`,
>    which is exactly the predicted failure and only visible in a container.
> 4. `YOLO_BYPASS_SHIMS=1` untouched — it is a property of the blocker body, which did not move.
>
> **Verified in a nested jail** (fresh `dist-go` binary, `YOLO_REPO_ROOT=/workspace`): PATH ends
> `…:/bin:/usr/bin:/home/agent/.yolo-launchers`; `grep -r`/`find` still exit 127 with their
> suggestions while plain `grep` works; `YOLO_BYPASS_SHIMS=1 find --version` → GNU findutils
> 4.10.0; both dirs writable; and with a pack declaring `program fzf`, `command -v fzf` →
> `/bin/fzf`, `fzf --version` → 0.74.1. Running `~/.yolo-launchers/fzf` **directly** still
> prints `⚠ fzf not available` and exits 1 — which is the proof that ORDERING is the entire
> fix, and that the baked fallback below is still worth landing.
>
> **The baked-binary collision audit came back clean**: none of `claude`, `copilot`, `codex`,
> `opencode`, `pi`, `agy`, `pnpm` exists in `/bin` inside the jail, so no shipped pack loses its
> lazy-updating launcher to an image binary today. That is the caveat to re-check before baking
> a package whose name a pack claims; it is now recorded in `AGENTS.md` rather than only here.

What it means concretely. The documented order is (`AGENTS.md:204`):

```
$HOME/.yolo-shims : $HOME/.local/bin : $NPM_CONFIG_PREFIX/bin : <mise-shims> : $GOPATH/bin : /bin : /usr/bin
```

Split it into two directories with the installer side moved **after** `/bin`:

```
$HOME/.yolo-shims : $HOME/.local/bin : $NPM_CONFIG_PREFIX/bin : <mise-shims> : $GOPATH/bin : /bin : /usr/bin : $HOME/.yolo-launchers
                    ^ blockers only (find, grep)                                                              ^ lazy installers (claude, pnpm)
```

Then an installer is reached **only when nothing else provides the name** — 11.1 becomes
structurally impossible rather than handled, and no fallback logic is needed for the baked-tool
case at all.

Four things the implementation has to get right, and none is hard but all are easy to miss:

1. **`AGENTS.md:203-204` pins the order as documentation.** It must be updated in the same
   change or the doc becomes wrong — and this repo has already been bitten by a doc that
   described behavior the code stopped having (11.3).
2. **The generators currently coordinate through the shared directory.** `shims.go:182` skips
   writing a launcher when a blocked-tool shim already owns the name. With two directories that
   collision cannot happen — but the *semantics* change: a blocked tool that a pack also
   declares as `program` would now have both a blocker (early) and a launcher (late), and the
   blocker correctly wins. Worth an explicit test, since today the launcher is simply never
   written.
3. **`~/.yolo-shims` is a bind-mount ANCHOR** (`shims.go:20-22`: mounted from
   `<ws>/.yolo/home/yolo-shims`, with `/home/agent` read-only). A second directory needs the
   same treatment or it will not be writable — this is the mechanical part most likely to fail
   first, and it fails at boot.
4. **`YOLO_BYPASS_SHIMS=1` must keep working for blockers.** It is the documented escape hatch
   for installers and scripts; whatever it does today to `~/.yolo-shims` must still apply.

**One consequence worth stating plainly:** an installer after `/bin` means a tool the image
bakes is used *in preference to* the pack's declared version. For `fzf` that is exactly right.
For an **agent CLI** it may not be — if the image ever baked an older `claude`, the pack's
lazy-updating launcher would stop being reached. No shipped pack has that collision today (the
six agent CLIs are not in `corePackages`), but it is the case to check before shipping, and it
argues for the baked fallback below staying in place as the belt to this braces.

### The rejected design, and why

Runtime self-discovery (`BASH_SOURCE` + PATH stripping). It works — verified against a real
binary later on PATH, against the image's own `/bin/fzf`, and against a genuinely absent binary
(exits 1, no loop). **Rejected because it treats recursion as a hazard to survive rather than
one to make unrepresentable**, and because it re-derives at runtime what the generator already
knew. Kept here so the next reader does not rediscover it as an improvement.

**Why not Q1.2 (skip generation when the bin already resolves).** Still no: it trades a loud
break now for a silent one later — if a future image drops the package, the pack that declared
it gets nothing and there is no shim to install it. The baked fallback degrades; skipping
generation disappears.

---

## 2. A `requires` kind — and why it should be decided FIRST (Q1.3)

> **SHIPPED 2026-08-03, as proposed.** `packdecl.KindRequires` is the 14th kind:
> `{bin, install_hints}`, `CombineShared`, review-worthy never. `RequiredBins()` is the
> jail-side projection and `AssertRequiredBins` (`internal/entrypoint/requires.go`) is the
> boot step; `DepRequirements()` folds `requires` in beside `program` so the host probe is
> literally the same one. Three things that were decided during implementation rather than
> above:
>
> 1. **`HostFields()`: honored-and-REPORTED**, as the doc's own "decide and defend" leaned.
>    Trivially-honored would have been wrong: the kind's entire host-side purpose IS the
>    hints, so a target that "honors" it while printing nothing would recreate G1 — the
>    failure mode where `skills`/`briefing` were in the honored set with no renderer and
>    vanished with no output line. It shares `apply --host`'s dep-report path with `program`,
>    and the line names WHICH kind asked, because a user reading `program fzf MISSING` would
>    look for the install yolo was about to do and there isn't one.
> 2. **A missing required bin is a WARNING, not an A12 boot failure.** A12 is fatal because a
>    half-written config file hands the agent a broken home; nothing here is half-written — a
>    pack asserted something and the image disagrees, while the pack's other contributions are
>    fine. Fatal would mean one bad `requires` stops the jail you need in order to fix the
>    pack.
> 3. **The probe asks about `BootPath(e)`, not the process PATH.** At that point in the boot
>    the entrypoint's own PATH is still the container default, so `exec.LookPath` would answer
>    a question nobody asked — whether the tool is visible to the *entrypoint* rather than to
>    the agent.
>
> `via`/`package`/`url` on a `requires` are refused BY NAME, because otherwise the mistake is
> silent: the fields are simply never read, so the tool never installs and nothing says why.

**The observation.** The fzf pack's actual need is *"fzf must exist"*, not *"install fzf from
npm"*. `program` only expresses the second, so the pack had to either lie (declare an npm
install for a baked binary — which breaks it, per #1) or stay silent (ship no
`install_hints`, losing the host-notch capability 8.3 just added).

**Proposal — a `requires` contribution:**

```jsonc
{ "kind": "requires", "bin": "fzf",
  "install_hints": { "brew": "fzf", "apt": "fzf", "nix": "fzf" } }
```

- **In a jail:** assert presence at boot; report a missing bin by name; **generate no
  launcher**. Nothing to shadow, so #1 cannot occur for it.
- **At the host:** feeds `check-deps`/`apply --host` exactly as `program`'s hints do today,
  which is the whole reason a content-only pack wants to carry them.
- **Footprint:** a claim, but not `CombineExclusive` — many packs may require one binary. That
  is the difference from `program`, which is exclusive because it owns a launcher path.

**RULED: build `requires`, AND keep #1.** *"I want to build this, but also still keep #1."*
Right — I had framed them as alternatives on the theory that `requires` shrinks #1 to
near-nothing. It does shrink it, but not to nothing: a genuinely npm-installed agent whose
install fails should still degrade rather than exit 1, and that case survives `requires`
entirely. Both land; `requires` is not a substitute for a working fallback.

With the PATH split ruled in (§1), the three compose cleanly rather than overlapping:

| mechanism | what it does | reached when |
|---|---|---|
| PATH split | installers ordered after `/bin` | always — makes shadowing impossible |
| `requires` | asserts presence, generates nothing | a pack needs a tool it does not install |
| baked fallback | execs a resolved absolute path | an install genuinely fails |

**Sequencing note, now that all three are in:** the PATH split (§1) removes the *cause*, so it
should land first — the fallback is then a safety net for a real install failure rather than a
workaround for self-inflicted shadowing. `requires` is independent of both and can land in any
order.

---

## 3. Only the first `program` per pack installs (11.2) — **fix the loop**

> **SHIPPED 2026-08-03, as ruled.** `InstallContributions() []Install`;
> `GenerateAgentLaunchers` is a nested loop. Four callers, and the origin gate was the one
> that mattered:
>
> | caller | change |
> |---|---|
> | `packload.HonoredInstall` → **`HonoredInstalls() ([]Install, []string)`** | the gate is applied **per contribution**, returning the granted set plus one refusal string per refused entry — matching `HonoredHostFiles`/`HonoredMounts`, which already had that shape |
> | `entrypoint.GenerateAgentLaunchers` | nested loop; `pathExists` still guards per-name, which now also covers a pack repeating a bin across two of its own contributions |
> | `entrypoint.packAliases` (`shell.go`) | was silently first-only too — a pack with two programs and launchFlags for both got one alias |
> | `cli/run.stagePacks` | appends every refusal instead of one |
> | `packload/packproperties_test.go` (installer-URL liveness) | now checks EVERY installer URL, subtest per bin — it was only reaching the first one |
>
> **Why the per-contribution gate is the load-bearing part:** a pack may mix an `npm` install
> with a curl-to-shell `installer`, and only the second is origin-gated. A whole-pack decision
> would either refuse the innocent npm install or — the real hazard — let a fetched pack
> smuggle an installer URL through beside one. Pinned by
> `TestOriginGateIsPerInstallContribution` (packload) and
> `TestFetchedPackKeepsNpmInstallAndLosesOnlyTheInstaller` (entrypoint).
>
> Exclusivity was left exactly as it was: `program` is `CombineExclusive` **by bin**, so two
> packs claiming `fzf` still collide and one pack claiming two different bins still does not.

`InstallContribution()` returns inside its loop (`contributes.go:123`), so a pack declaring
`fd` and `fzf` gets a launcher for `fd` only — while `DepRequirements()` returns both.

> **Reversed after review.** *"why would we want to limit this? what is the case for
> constricting packs?"* There isn't one, and I did not have one — my argument was that the
> accessor's *name* is singular, which is evidence about history, not about what a pack should
> be allowed to do. Reasoning from a stale identifier to a product constraint is backwards.

**Corrected proposal: return a slice and generate N launchers.** A pack needing two tools is an
ordinary thing — a linting pack wanting `shellcheck` and `shfmt`, a data pack wanting `jq` and
`yq`. `DepRequirements()` already returns all of them and the HOST path already reports all of
them, so the jail is the side that is wrong, not the host.

My stated reason for preferring the restriction was that N launchers means N shadowing hazards.
**The PATH split (§1) removes that**, so the argument does not survive its own ruling: with
installers ordered after `/bin`, a launcher cannot shadow anything, and generating ten is no
riskier than generating one.

What the fix touches:

- `InstallContribution() *Install` → `InstallContributions() []Install` (the singular name goes
  with the singular behavior). Check every caller — `HonoredInstall` in `packload` applies the
  origin gate per install and must keep doing so **per contribution**, since a pack could mix an
  `npm` install with a curl-to-shell `installer` and only the second is origin-gated.
- `GenerateAgentLaunchers` (`shims.go:161`) loops packs and takes one install each; it becomes a
  nested loop. The existing `pathExists(shimPath)` guard already handles per-name collisions.
- **Exclusivity stays per `bin`, not per pack.** The footprint model already says `program` is
  `CombineExclusive` "by `bin` name" — two packs both installing `fzf` is still a collision, and
  that check is unaffected. One pack installing two *different* bins never was one.

---

## 4. A dropped pack's staged tree keeps rendering (11.3)

> Expanded after review (*"needs more context"*).

### What staging is, and why a leftover tree is not inert

A pack is not read from wherever it lives. On the host, `stagePacks`
(`internal/cli/run/packs.go`) **copies** each selected pack into a staging tree under
`paths.AgentsDir()/<cname>/packs/`, and that tree is bind-mounted into the jail at
`/ctx/packs` (`YOLO_PACK_ROOT`). Two dirs inside it:

```
<agents>/<cname>/packs/
├── _official/<name>/      # the packs yolo SHIPS, materialized out of the binary
└── <slug>/                # each CONFIGURED pack, copied from its source
```

**The mount is the filter** — and `AGENTS.md` says so explicitly, because it is load-bearing:
the in-jail entrypoint renders *every* pack it finds under `YOLO_PACK_ROOT`, with no idea which
ones the user selected (it cannot read the config — that is the credential boundary). So
"selected" is expressed entirely by "present in the staged tree".

Which means a leftover directory is not dead weight. It is **a fully active pack**: its surfaces
render, its hooks run, its shims generate.

### The defect

`stagePacks` clears `_official` and only `_official`:

```go
// internal/cli/run/packs.go:93-99
officialRoot := filepath.Join(stagingRoot, "_official")
// Clear it: a pack DROPPED from config must stop being mounted, and a leftover tree
// would keep rendering as if it were still selected.
if err := os.RemoveAll(officialRoot); err != nil { … }
```

The comment states the right rule. The code applies it to the shipped packs and **not** to
configured ones — so removing a *user's* pack from `packs` leaves its staged copy behind, and it
keeps rendering forever.

**Observed live**, which is how it was found: a deleted test pack kept regenerating a broken
`fzf` shim across launches until the staging dir was removed by hand. The user had already
deleted the pack *and* its config entry.

And it contradicts what `AGENTS.md` asserts as fact:

> *"`stagePacks` copies only the SELECTED packs into the mounted tree (**and clears it, so a
> dropped pack stops rendering**)."*

The doc describes the behavior a reader would want; the code is the side producing the surprise.

### Why the asymmetry exists (and why it is not deliberate)

`_official` can be cleared wholesale because it is **derived** — materialized fresh from the
binary's `embed.FS` on every launch, so deleting it costs nothing. A configured pack's staging
dir is a **copy of an external source**, so wholesale clearing means re-copying every pack every
launch. The cheap incremental choice was made for the expensive side and the correctness rule
was only applied to the cheap one. Nothing in the comments suggests the gap was intended.

**Proposal: prune slugs not in the current config — contents-only, never the dir itself.**

Two constraints that decide the shape:

1. **`packstage` rule 3**: clear CONTENTS, never the dir — a running jail's bind mount
   captured the inode. So `os.RemoveAll(stagingRoot)` is wrong even though it is obvious.
2. **A configured-but-unresolvable pack must NOT be pruned.** A fetched pack that could not be
   reached this launch is still configured; clear-and-restage would silently discard it on
   every offline launch. This is the case that rules out the simpler option.

So: compute the live slug set from `entries` (before resolution, so an unreachable fetched
pack counts), and remove staging dirs whose slug is absent from it.

**Report what was pruned**, per the no-silent-caps rule — a user who dropped a pack should see
its tree go, not wonder whether it is still active.

---

## 5. Brew casks in the dep manifest (8.4) — **SHIPPED 2026-08-02**

> **Implemented as proposed**, with two findings worth recording. (1) All four tokens
> re-verified against `formulae.brew.sh` (cask 200 / formula 404), so none had to stay on
> `brew`; `opencode` and `pi-coding-agent` are genuine formulae and were left alone. (2) The
> one-liner grew an explicit `--cask` too, not just the Brewfile: bare `brew install <token>`
> falls back to a cask only when NO formula matches, so leaving it bare keeps the
> `copilot`-is-AWS's-ECS-CLI trap live on the printed path. `Result` gained a `Flavor` field
> because one brew host can need both verbs in one Brewfile, so "which verb" cannot be a
> property of the detected manager.


`depcheck.Manifest` writes every package as `brew "<pkg>"`, but a Brewfile `brew` entry
installs with `--formula`. Four of the six shipped hints are **casks**, so the generated
Brewfile fails. (The printed one-liner is fine — bare `brew install <token>` resolves either.)

> **RULED: just fix it.** *"don't follow totally, but fix it so it works."* Taking that as
> license to pick the mechanism, so here is the concrete change rather than the trade-off.

**The problem in one line.** A Brewfile has two different verbs for two different things:

```ruby
brew "postgresql@16"     # a FORMULA — command-line software
cask "claude-code"       # a CASK — an app bundle / binary distribution
```

`depcheck.Manifest` writes `brew "<pkg>"` for every hint. Four of the six shipped hints
(`claude-code`, `copilot-cli`, `codex`, `antigravity-cli`) are **casks**, so
`brew bundle --file=<generated>` fails on them: it looks for a formula by that name and there
isn't one.

Only the *bundle file* is affected. The printed one-liner already works, because bare
`brew install <token>` falls back to a cask when no formula matches — which is exactly why the
bug was invisible until someone generated a Brewfile.

**The change:** add a `brew-cask` key alongside `brew` in `install_hints`, then in
`internal/depcheck/depcheck.go`:

```go
// installCmd (:84) — one new case
case "brew-cask":
    return "brew install --cask " + pkg
```

and in `Manifest`, emit `cask "<pkg>"` for a `brew-cask` hint instead of `brew "<pkg>"`.
`DetectManager` still returns `brew`; the lookup consults `brew-cask` first, then `brew`.

Then the four cask hints move key:

```jsonc
"install_hints": { "brew-cask": "claude-code", "nix": "claude-code" }
```

**Why a key rather than a struct.** `install_hints` is `map[string]string` and its whole virtue
is one line per manager. A key naming the *installer flavor* keeps that grain; a nested object
per hint would need every existing hint rewritten for one flag. `pacman`/`dnf` have no
equivalent split, so nothing else grows a variant.

---

## 6. The three unfree nix hints (8.3) — and the better question underneath

> **SHIPPED 2026-08-03, as ruled and trimmed.** No new schema, exactly as predicted:
> `packdecl.selfInstallCommand` derives the remedy from the `via`/`url`/`package` a `program`
> already declares (`npm install -g <pkg>` / `curl -fsSL <url> | sh`), carried to the checker
> as `DepRequirement.SelfInstall`. `depcheck.Check` prefers it over a package-manager hint and
> tags it `Flavor: "self"`. All six `packs/*/pack.json` lost their `nix` key;
> `brew-cask`/`brew` untouched.
>
> Two decisions the proposal did not cover:
>
> 1. **The manager hint is KEPT as a secondary line, not dropped.** `Result.Fallback` (with a
>    `FallbackFlavor`, since Manifest needs the brew formula-vs-cask verb) prints as
>    `or via brew: brew install --cask claude-code` under the primary remedy. Dropping it
>    entirely would mean a user who prefers their own package manager has to read `pack.json`
>    to find the token — and the ruling was about which remedy LEADS, not about hiding one.
> 2. **A self-install NEVER enters the generated bundle.** There is no way to spell
>    `curl … | sh` in a Brewfile, and splicing the URL in as a token yields a file that fails
>    on a line the user cannot fix. `Manifest` contributes such a dep's `Fallback` token if it
>    has one and nothing otherwise; the printed remedy already names the command, so nothing
>    is lost. That is what the `"self"` flavor is FOR — a manager name there would have made
>    this undetectable.
>
> **A side effect worth naming, because it deleted a message.** `noRemedyReason`'s third case
> — *"its JAIL install (via npm) is not run against a real host"* — is gone, and its test with
> it. It existed because every shipped pack was missing-with-no-usable-hint, so the honest
> thing was to say yolo knew an install it would not run. A well-formed `program` now derives
> its remedy FROM that very `via`, so having a `via` means having a remedy and the branch is
> unreachable. What is left there is a `requires` (installs nothing by definition, and its line
> says so) or a malformed manifest (`pack lint`'s job). The two `apply --host` tests that
> covered the no-remedy branch were rewritten onto `requires`, which is now the only kind that
> can reach it — that rewrite is the measure of the change, not collateral damage.
>
> **Not pre-empted:** yolo still runs none of these. `curl … | sh` is printed as a suggestion
> the user runs, and the report says `apply --host reports host deps; it installs nothing`.
> Phase 4.3's confirm-gated install is untouched.

> **Review question, and it reframes this item:** *"why are we pulling these through nix? don't
> they have their own installers we can use? nix will get out of date for this stuff."*

### The premise is right, and I measured it

Every one of the six agents **already has a first-party installer**, which is what the pack
declares for the jail:

| pack | `via` | source |
|---|---|---|
| claude | `installer` | `https://claude.ai/install.sh` |
| agy | `installer` | `https://antigravity.google/cli/install.sh` |
| codex, copilot, opencode, pi | `npm` | their npm packages |

So `install_hints` sends a host user to nixpkgs for tools that **ship their own updater** — and
in the agents' case that updater is the mechanism they are designed around (Claude Code
self-updates; the jail's own launcher checks npm hourly).

**Measured staleness, 2026-08-02** — mixed, and the spread is the point:

| tool | nixpkgs | upstream | lag |
|---|---|---|---|
| claude-code | 2.1.220 | 2.1.220 | **none** |
| codex | 0.146.0 | 0.146.0 | **none** |
| pi-coding-agent | 0.83.0 | 0.83.0 | **none** |
| opencode | 1.18.9 | 1.18.11 | 2 releases |
| **github-copilot-cli** | **1.0.61** | **1.0.77** | **16 releases** |

So nixpkgs is *not* uniformly stale — but it is badly stale for at least one, and nothing about
the packaging tells a user which. A remedy line that says `nix profile install
nixpkgs#github-copilot-cli` hands them a version 16 releases old **without saying so**, which is
the same class of quiet wrongness as the provenance misreport in §8 of the collaboration doc.

### There is no "pin-the-closure" case — I invented it. **Drop the nix hints for agent CLIs.**

> **Reviewed:** *"what is the 'pin-the-closure' case? when would anybody use that? I don't want
> to accrete junk."* Correct, and reading `detectManager` settles it against me.

I had justified keeping the nix hints for "a user who wants the closure pinned rather than the
latest". **That user cannot reach them.** `detectManager`
(`internal/depcheck/depcheck.go`) is not a preference — it is a **first-match probe with nix as
the terminal fallback**:

```go
func detectManager() string {
	if runtime.GOOS == "darwin" { if brew present { return "brew" } }
	for _, m := range []string{"apt", "dnf", "pacman", "brew"} {
		if m present { return m }
	}
	return "nix"          // ← reached only when NOTHING else was found
}
```

So a `nix` hint is printed **only on a host with no apt, dnf, pacman, or brew** — in practice a
NixOS machine, where nix is not a *choice* the user makes but the only manager they have. And
the remedy is selected *for* them; there is no flag to ask for the nix one. Verified on this
host: all four absent, so `nix` by elimination.

That kills the case I described. A NixOS user is not "pinning a closure" — they are just
installing, with the only tool available. And on **every other** host the nix hint is dead
weight: never selected, never printed, one more string to keep current.

**So: drop `nix` from `install_hints` for the six agent CLIs**, and let the upstream installer be
the remedy on every host including NixOS. Three reasons it is the right call rather than merely
the smaller one:

- **The upstream installers work on NixOS.** `npm install -g` and Claude's `install.sh` both
  install into the user's home, not the system — no FHS assumption to break. A NixOS user
  installing an npm CLI with npm is doing the ordinary thing.
- **It removes the staleness trap entirely.** `github-copilot-cli` at 16 releases behind stops
  being reachable, so nobody is handed it silently — no version-labeling machinery needed.
- **It removes the unfree caveat from the primary path.** All three unfree packages are agent
  CLIs; drop their nix hints and the caveat has nowhere left to fire.

**Keep `nix` hints for genuine third-party dependencies** — `fd`, `fzf`, `jq`, `postgresql`.
Those are exactly the case `install_hints` was designed for: a tool the pack does *not* install,
where the user's own package manager is right and nixpkgs is not meaningfully stale. That is the
distinction I should have drawn instead of inventing a user.

**Where a real "pin the closure" want belongs**, if it ever shows up: not a hint that looks like
a plain install command, but
[`../design/noncontainer-nix-environment.md`](../design/noncontainer-nix-environment.md)'s `buildEnv` — a whole
declared closure, which is a different feature with a different UI. **Not building it on
speculation.**

Two things the change still needs:

1. **The `via`/`package`/`url` fields already carry everything** — no new schema. The remedy is
   derivable from the contribution the pack already declares.
2. **`curl … | sh` is a different trust proposition from `brew install`.** yolo already treats an
   installer URL as review-worthy (origin-gated, flagged `⚠ review` in the footprint), so
   printing it as *a suggestion the user runs themselves* is consistent — but it must not become
   something yolo runs unprompted. That is env-manager Phase 4.3's confirm-gated territory and
   this item must not pre-empt it.

### The unfree annotation — mostly deleted by the above, kept only for the general case

All three unfree packages (`claude-code`, `github-copilot-cli`, `antigravity-cli`) are **agent
CLIs**, so dropping their nix hints removes the caveat from every path a user actually hits. That
is the tidiest outcome: no annotation mechanism, no per-hint marker, nothing to keep in sync.

It survives only as a rule for a **future** unfree hint on a genuine dependency (nothing shipped
today qualifies). If one is ever added, the remedy must say so:

```
(unfree: needs NIXPKGS_ALLOW_UNFREE=1 or nixpkgs.config.allowUnfree)
```

**Do NOT auto-add `NIXPKGS_ALLOW_UNFREE=1` to the printed command.** Unfree is a licence
decision the user makes once, machine-wide; slipping the override into a copy-pasteable line
makes it for them silently — the same consumer-grants-power invariant `allow_exec` follows.

**Net effect on the work:** with the agent-CLI nix hints dropped, this item stops needing any
per-hint annotation mechanism at all. It becomes a one-line rule in the doc for whoever adds the
next hint, not code. That is a real reduction in scope from what I first proposed.

---

## 7. `packages: ["claude-code"]` fails with a raw nix trace (nix OQ-6) — **SHIPPED 2026-08-02**

> **Implemented, with one deliberate substitution and one bug the doc's framing would have
> introduced.** (1) The check is `drv.meta.available`, not `meta.unfree`/`meta.license.free`:
> it is nixpkgs' own verdict (`validity.valid != "no"`), reads without throwing, and — unlike a
> bare unfree test — flips back to true under `NIXPKGS_ALLOW_UNFREE=1` / `allowUnfreePredicate`,
> so a user who deliberately opted in still gets the package instead of a silent skip. yolo does
> not set that variable for them. (2) **Reason precedence is load-bearing:** `meta.available`
> folds `unsupported` in *with* the licence checks, so testing it before `availableOn` labels a
> plain platform miss ("no aarch64-darwin build" — `iptables`) as "broken or blocklisted". Caught
> by measurement on the darwin system, not by inspection. (3) The warning rides on
> `darwinPackages` (the BUILD path, whose stderr is streamed to the user) rather than on the skip
> list alone, which is read by a separate `nix eval` whose stderr is discarded — so the user
> learns which package was dropped and why in the same invocation that used to abort.

Re-measured: eval **succeeds**, build fails. The `tryEval` around `availableOn`
(`flake.nix:301-303`) absorbs the unfree assertion during eval, so the package is reported
*available*, `darwinUnavailablePackages`' warn-and-skip never runs, and the abort surfaces
inside `buildEnv`.

**Proposal: check `meta.unfree` (or `meta.license.free`) beside `availableOn`**, and route a
failure into the existing skip-and-warn path. Not a change to `availableOn`'s use — unfree is
not a platform fact, so no amount of platform probing will catch it.

**High confidence and independent of every nix-env question** — a user who puts an agent CLI
in `packages:` today gets a `check-meta` trace instead of the warn-and-skip the mechanism
promises. Worth fixing whether or not any host-nix work happens.

> **Review noted:** *"I think I covered this above"* — meaning the §6 answer (don't route agents
> through nix; they have their own installers). Worth separating the two, because they only look
> like the same item: §6 is about **which remedy `install_hints` prints**, and is fully resolved
> by preferring the upstream installer. **This one is about `packages:`** — the config key where
> a user names extra nix packages to bake — and it fires for **any** unfree package, not just an
> agent CLI. `packages: ["vscode"]` or `["terraform"]` aborts the same way with the same raw
> trace, and no change to `install_hints` touches that path.
>
> So §6's answer removes the *motivating example* but not the defect. Still worth the one-line
> fix — and it is the cheapest item in this doc.

---

## 8. `rmwProvenance` as a second "which layer won" (§8 caveat)

Host provenance derives the winner by **replaying write order**; `Compose` derives it by
**folding layers**. Two implementations of one concept.

**Proposal: leave both, and strengthen the parity test into a shared-corpus table.** Today
parity is pinned for *granularity*. I would extend it to a table of layer/key fixtures asserted
against **both** implementations, so a divergence in *outcome* fails rather than only a
divergence in shape.

**Why not unify.** They answer the same question about genuinely different mechanisms: a fold
has all layers in hand and produces a winner per key; an RMW write has no fold at all —
precedence *is* write order. Forcing one implementation means either giving RMW a synthetic
layer stack it does not have, or making `Compose` simulate sequential writes. Both are more
fiction than the duplication.

**RULED: wait for a third.** *"yes, wait for 3 to unify."* So the parity table is the whole of
the work here, and the unification is explicitly deferred until a third derivation exists —
which is the rule-of-three applied rather than an abstraction guessed at from two cases.

Concretely that means: build the shared-corpus parity table now (so a divergence in *outcome*
fails, not just one in shape), and leave a note at both implementations saying the third
occurrence is the trigger. The `guest` notch (env-manager Phase 7) is the likely third — it
renders into a real home like the host but keeps a workspace like the jail, so it may well need
its own derivation and would be the moment to unify all three.

---

## 9. Nightly macOS builder arch (BACKLOG E8) — no proposal

`publish.yml` pushes the builder image `aarch64-linux` **only**, with a comment saying why;
`nightly-macos.yml` runs `macos-26-intel`. Both fixes — publish multi-arch, or drop those two
tests from the Intel nightly / move it to Apple Silicon — change what the product ships or
what it claims to test.

> **Review corrected me, and the workflow confirms it:** *"I don't care about supporting macos
> intel, but I think it was a CI runner compromise?"* — **yes, and I had the framing wrong.**
> It is not a platform-support decision at all. `nightly-macos.yml:44-49` says so in a comment I
> should have read before calling it one:
>
> > *"macOS Intel runners support nested virtualization via Podman Machine; GitHub's Apple
> > Silicon runners do not, so stay on an `-intel` label. macos-13 was retired Dec 2025.
> > macOS 26 (Tahoe) is the last Intel macOS, and GitHub plans to retire Intel runner images
> > around late 2027 — after that this job needs a self-hosted runner or arm64 nested-virt
> > support. Note: x86_64 not ARM — tests the macOS code paths, not native ARM performance."*
>
> So the Intel runner is there **because it is the only hosted runner that can nest a VM**, and
> it exists to exercise the macOS *code paths*, not Intel as a target. Nobody is choosing to
> support Intel Macs.

**That changes the answer.** The two lib-farm tests need a Linux builder image, `publish.yml`
pushes `aarch64-linux` only, and the runner is x86_64 — so it pulls an arm64 image and the farm
never materializes. Given the constraint above, the options reduce to:

1. **Publish the builder multi-arch** (add `x86_64-linux` to that matrix cell). The runner is
   x86_64 *by necessity*, so an x86_64 builder is not Intel-Mac support — it is what makes the
   only viable runner work. `publish.yml`'s own comment (*"an x86_64 mac … can build locally or
   use QEMU"*) was written about **users**, and CI is not a user.
2. **Skip the two lib-farm tests on this job**, with the reason recorded. Cheapest, and it loses
   coverage of `/lib`-farm behavior on macOS entirely — but note that farm is a **Linux-container**
   mechanism, so what is actually being tested there is the *builder path*, not anything
   macOS-specific.

**I lean (1)**, because it makes the nightly test what it claims to test, and the marginal cost
is one more matrix cell on a release-gated workflow. (2) is defensible if the builder push is
expensive or the coverage is judged redundant.

**Not proposing further** — this is still a CI-cost judgment, and the 2027 runner retirement
means both options have a shelf life. But the framing is now right: **a CI capability
constraint, not a platform-support question**, and my earlier "no proposal, it's a product
decision" was simply wrong.

---

## 10. A pack cannot install Claude MCP servers on the host — **in progress**

Full analysis in [`handoff-host-mcp-servers.md`](handoff-host-mcp-servers.md); this entry is
the proposal plus the one ruling that settled its hardest question.

**The defect, reproduced.** Claude keeps user-scope MCP servers in `~/.claude.json` under
`mcpServers` — the `claude/config` surface. `usesWorkspacePlaceholder`
(`internal/entrypoint/hostrender.go:239`) is a **surface-level** predicate, and the builtin
`claude` pack uses `${workspace}` in two *unrelated* keys
(`projects.${workspace}.hasTrustDialogAccepted` and `.enableAllProjectMcpServers` — verified
as the only `${workspace}` users on that surface). So the whole file is off-limits at the host
notch:

```console
$ yolo apply --host --assert
  claude/config   refused: uses ${workspace}, which has no referent on the host
$ ls .claude.json   →  does not exist
```

Correct in intent, too coarse in granularity: a key with nothing to do with `${workspace}` is
unreachable because a *different* key on the same surface is workspace-keyed.

**Proposal: prune the workspace-keyed branches, render the rest, and NAME what was pruned.**
Replace the boolean predicate with a prune returning the surface minus workspace-keyed
branches plus the dotted paths removed. If nothing survives, *then* skip the surface — with a
reason naming the pruned keys, never a bare "uses `${workspace}`". Same never-silent discipline
the G1 fix established for skills/briefing.

### Ruling — the first destructive apply WARNS AND WAITS FOR CONFIRM

Two maintainer rulings govern this, and the second answers the question I had flagged as the
one genuine one-way door.

**Ruling A (2026-08-02):** *"if you manage mcpServers through yolo, you give up `claude mcp
add`, that's fine."* This makes wholesale table regeneration **correct policy** rather than
destructive — yolo is the sole author, so an undeclared server is stale by definition. No
merge-on-host is needed. `noteDroppedManagedEntries` (`prism.go:635`) already exists to
announce drops.

**Ruling B (2026-08-02):** *"let's just warn during the first apply that things will be lost
and wait for confirm."*

So **warn-and-confirm, not warn-and-refuse.** Refusing would leave a user with no path forward
short of hand-editing `~/.claude.json`; proceeding silently would destroy a hand-added server.
The confirm is the only option that both protects the file and lets the user proceed.

Four constraints on the implementation, three of which are about not devaluing the prompt:

- **Reuse `promptYesNo`** (`internal/cli/pack.go:903`), the same shape the fetched-pack
  host-access approval uses. Do not invent a second prompt idiom.
- **FAIL-CLOSED on a nil stdin**, exactly as `packMain` documents (`pack.go:136-137`): a
  non-interactive run means *"no approval given"*, never consent. A scripted or CI
  `apply --host --assert` with no TTY must not destroy a server because nobody was there to
  answer. This needs stdin threaded into `applyHost`, which does not currently take it.
- **Only prompt when something would actually be lost.** A confirmation that fires on every
  clean apply trains people to hit `y` without reading, which defeats its purpose.
- **`observe` must never prompt** — it writes nothing, so there is nothing to confirm. It
  should *report* what an `--assert` would drop, so the information arrives before the prompt
  ever does.

### Two more things this work carries

- **`~/.claude.json` is live agent state**, not just config: ~40K, 32 top-level keys, 17
  per-project entries, history and onboarding flags. RMW touches only declared keys, but the
  blast radius dwarfs `settings.json`. A round-trip test proving an untouched multi-key file
  comes back byte-identical apart from the asserted key is not optional here.
- **`${VAR}` interpolation covers `env` values ONLY** — verified: `interpolateEnv` has exactly
  one call site (`mcp.go:197`), on `cfg.Get("env")`. So `${TAVILY_API_KEY}` inside a server's
  `url` is written literally and the server 401s silently. Extending interpolation to `url`
  (warning on unresolved, as `interpolateEnv` already does at `mcp.go:63`) is the right call —
  the `http` transport is otherwise unusable with any secret. The key must keep coming from
  `env_sources`, never from pack content: a pack's `env` kind is static-strings-only by design
  and must not become a secret carrier.

  *Caveat on the handoff:* it states the maintainer's **host** `~/.claude.json` uses the
  URL-embedded form. I could not verify that from inside the jail — the file visible here is
  the JAIL's, which uses `command`+`env`. Treat that as plausible-but-unconfirmed; the `url`
  interpolation is right regardless of that one file.

**Why this matters beyond Claude:** `copilot/mcp`, `agy/mcp` and `opencode/config` carry MCP
tables and **already render at the host**. Claude is the odd one out purely because of where
Claude Code chose to store user MCP config. So this is not a new capability — it makes an
existing one uniform, which is the actual goal.

---

## Suggested order

No item is gated on a decision any more. The order below is about **dependency and blast
radius**, not about waiting for answers.

0. **#10** (host MCP servers) — **already in progress**; the only item with a user-visible
   capability behind it rather than a latent hazard.
1. ~~**#7** (unfree eval), **#5** (brew cask)~~ — **DONE 2026-08-02.** Isolated as expected;
   see each section's shipped note for what differed from the proposal.
2. **#4** (staging prune) — closes a doc/code contradiction, no interactions.
3. ~~**#1 the PATH split**~~ — **DONE 2026-08-02** (`~/.yolo-launchers`, ordered after `/bin`;
   nested-jail verified). It removed the *cause* that #1's fallback and #3's caution were both
   working around, so both are now smaller than described.
4. **#1 the baked fallback** (still open) and ~~**#3** (slice + N launchers)~~ — **#3 DONE
   2026-08-03.** Trivial once the split landed, as predicted, and its only objection
   (N launchers = N shadowing hazards) was indeed gone by then. The fallback is the remaining
   half of #1.
5. ~~**#2** (`requires`)~~ — **DONE 2026-08-03.** Independent of everything else, as expected.
   The 14th-kind cost was exactly the three places named (`config_ref.txt`, `pack --help`, the
   Phase 0 drift test) plus one the list missed: `applyhostcensus_test.go`'s `writeCensusPack`,
   which `t.Fatalf`s until the new kind has a census contribution — working as designed.
6. ~~**#6** (upstream-installer remedies)~~ — **DONE 2026-08-03**, after #5 as sequenced, and it
   did reuse the hint plumbing.
7. **#9** (multi-arch builder) — independent; a CI change, verifiable only by running the
   nightly.
8. **#8** (parity table) — whenever provenance is next touched; unification waits for a third
   derivation.

**Two dependencies worth naming:**

- **#10 threads `stdin` into `applyHost`** for its confirm prompt. Nothing else here needs it,
  but once it lands that plumbing serves any future host-notch confirmation — including
  env-manager **Phase 4.3's confirm-gated install**, which has been deferred partly for want of
  exactly that. #6 pushes toward printing `curl … | sh` remedies, which makes 4.3 more likely to
  be wanted soon.
- **#1's split and #3's slice both touch `GenerateAgentLaunchers`.** Doing the split first means
  #3 is a nested loop in already-correct code rather than a nested loop plus a re-layout.

---

## 11. A dropped pack's host output is never retired (new, 2026-08-03)

Full spec: [`host-pack-drop-cleanup.md`](host-pack-drop-cleanup.md). Summarized here because
this doc is the index of open findings.

Reported by a subagent as "host pack-drop cleanup is briefing-only". Reproduced in a throwaway
`$HOME`, which both **confirmed and corrected** it:

- **Confirmed and widened.** The report named `files` and `config-overlay`. **Skills leak too**
  — `.claude/skills/demo` survived a drop, still invocable, with the ownership manifest still
  naming the dropped pack. A live orphaned skill an agent will load is sharper than an orphaned
  script.
- **Corrected.** The report's sharpest claim — that `fileSuggestion` keeps pointing at a script
  the drop removes, a broken hook — **is wrong**. `files` COPIES the tree into `$HOME`; deleting
  the pack source entirely left the script present and working. It is an orphan that silently
  keeps FUNCTIONING: stale config, not breakage. That is why the fix is confirm-gated rather
  than urgent.
- **The real defect neither of us named: provenance laundering.** While the pack is active the
  record says `fileSuggestion  config-overlay:dropme`; the next apply after the drop rewrites it
  to `fileSuggestion  host`. The record built so yolo could tell its own output from the user's
  reclassifies yolo's key as the user's — and once a key reads `host`, every mechanism that asks
  "did yolo write this?" answers no, forever.

**Why it is not a duplicate of the manifest/archive work** (`pack-host-management-plan.md`
Phase 6). That shipped per-pack *stale-entry* retirement: `EntriesFor(pack, dir)` = "what I
wrote last time minus what this pack ships now". It is keyed on the pack being ITERATED, and the
apply loop is `for _, p := range loaded`. It solves *the pack changed*; it cannot see *the pack
left*. `PruneHostBriefings` is the only path that reasons over the inactive set — which is
exactly why briefing is the one kind that works.

**Ruled 2026-08-03:** *"I want to confirm before removing host files."* So retirement is
confirm-gated (the `confirmHostLosses` contract: only when something is lost, never in observe,
fail-closed on EOF stdin), archives rather than deletes, and provenance stops laundering
regardless of whether the key is dropped. Briefing keeps its current unconditional behavior —
removing a delimited managed block restores the file's own bytes and loses nothing.
