# The `program` kind: three defects, and the questions they raise

**Status:** analysis + open questions, 2026-08-02 — **and all three defects are FIXED since.**
Re-checked 2026-08-23. 11.1 and 11.2 have dated UPDATE blocks below (the PATH split, and the
`requires` kind); **11.3 closed too** — `packstage` now clears a dropped pack's staged tree
**contents-only**, leaving the directory inode alone because a live jail's `/ctx/packs` bind
captured it (`internal/packstage/packstage.go:29,48,230` — "rule 3"), and the host-side half
shipped as [`../plans/host-pack-drop-cleanup.md`](../plans/host-pack-drop-cleanup.md)'s four
rulings on 2026-08-03. **The remaining value of this doc is §5's questions and the reasoning, not
its defect list.** Companion to
[`../plans/pack-host-management-plan.md`](../plans/pack-host-management-plan.md) Phase 11,
which lists these as work items; this doc carries the context and the decisions needed before
any of them can be implemented.

**How they were found:** by authoring a real pack
([`../examples/claude-fzf-pack/`](../examples/claude-fzf-pack/)) and trying to declare its
dependencies honestly. Not by any test. Every claim below was re-verified against source
before being written down; the file:line references are the evidence.

**Why they are grouped:** all three make a pack's `program` contribution *actively harmful* or
*silently partial*, and together they are the reason the fzf pack ships **no `program`
contribution at all** — a pack that declared its real deps came out worse than one that lied
by omission.

> **UPDATE 2026-08-02 — 11.1 is FIXED, and by removing its cause rather than handling it.**
> The lazy launchers moved out of `~/.yolo-shims` into their own dir, `~/.yolo-launchers`,
> ordered **last** on PATH (after `/bin`). §0's premise 1 below — *"`~/.yolo-shims` is FIRST on
> PATH, so a shim always wins over `/bin`"* — is still true of the **blockers**, and no longer
> true of the launchers, which is exactly the distinction the section was missing.
>
> That makes Q1.1/Q1.2 moot for the baked-binary case: an installer is now unreachable while
> any real binary of that name exists, so there is nothing to fall through to and nothing to
> skip generating. The launcher's exit-1 tail is unchanged and still correct for the case it
> was always about — a genuinely absent tool whose install genuinely failed. Q1.3
> (`requires`) survives untouched.
>
> Verified in a real container: a pack declaring `{"kind":"program","bin":"fzf",…}` now leaves
> `command -v fzf` → `/bin/fzf`, `fzf --version` → 0, while its launcher is still generated at
> `~/.yolo-launchers/fzf` (running it directly still exits 1, which is what proves ORDERING is
> the whole fix). See [`../plans/proposed-fixes-open-findings.md`](../plans/proposed-fixes-open-findings.md) §1.

> **UPDATE 2026-08-03 — Q1.3 and Q2.1 are both ANSWERED, and 11.2 is FIXED.**
>
> **Q1.3 → yes, build it.** The `requires` kind shipped: it asserts a binary is present,
> generates nothing (no launcher, nothing on PATH), reports a missing bin BY NAME at boot, and
> feeds `check-deps`/`apply --host` through the same `install_hints` plumbing `program` uses.
> It is the 14th kind. It is `CombineShared`, not `CombineExclusive` — many packs may require
> one binary, because none of them owns a path for it. The maintainer ruling was *"build this,
> but also still keep #1"*: `requires` shrinks the launcher-fallback case but does not remove
> it, since a genuinely npm-installed agent whose install fails should still degrade.
> `docs/examples/claude-fzf-pack/` adopted it the same day.
>
> **Q2.1 → it was a loop bug, and the ruling reversed my proposal.** I had leaned toward "an
> intended one-program-per-pack rule → make a second a validation error", reasoning from the
> accessor's singular NAME. That is evidence about history, not about what a pack should be
> allowed to do: *"why would we want to limit this? what is the case for constricting packs?"*
> There is none — `shellcheck` + `shfmt`, or `jq` + `yq`, is ordinary. So
> `InstallContribution() *Install` became `InstallContributions() []Install`, and
> `GenerateAgentLaunchers` is a nested loop. My own stated objection ("N launchers means N
> shadowing hazards") did not survive the §1 ruling above: with installers ordered after
> `/bin`, a launcher cannot shadow anything, so ten are no riskier than one.
>
> One thing the fix had to get right that the analysis below does not mention: `HonoredInstall`
> in `internal/packload` applies the ORIGIN gate, and it had to keep applying it **per
> contribution** — a pack may mix an npm install with a curl-to-shell installer, and only the
> second is gated. Deciding once for the whole pack would either refuse the innocent npm
> install or let a fetched pack smuggle an installer URL through beside one. It is
> `HonoredInstalls() ([]Install, []string)` now, returning the granted set and one refusal
> string per refused contribution.

---

## 0. What `program` is supposed to do

A `program` contribution says *"this pack needs binary X on PATH"*. yolo honors it by
generating a **lazy launcher** at `~/.yolo-launchers/<bin>` (was `~/.yolo-shims/<bin>` when
this was written) rather than installing at boot: the
launcher installs the tool on first use, then re-execs it. That laziness is the feature — a
jail starts fast and only pays for the agents you actually run.

Two facts make the defects below possible:

1. **`~/.yolo-shims` is FIRST on PATH.** The documented order *at the time this was written*
   was
   `$HOME/.yolo-shims:$HOME/.local/bin:$NPM_CONFIG_PREFIX/bin:<mise-shims>:$GOPATH/bin:/bin:/usr/bin`
   (AGENTS.md). So a shim always won over `/bin`. **No longer true for launchers** — see the
   update at the top: they live in `~/.yolo-launchers`, last on PATH. Still true for
   blockers, where it is the point.
2. **The image bakes a substantial toolchain.** `flake.nix` puts ~100 packages in
   `corePackages`, including `fd` (`flake.nix:658`) and `fzf` (`flake.nix:721`) — the two the
   fzf pack needs.

Those two facts collide, and nothing mediates the collision.

---

## 1. Defect 11.1 — a `program` contribution shadows a baked binary and breaks it

### What happens

A pack declares `{"kind":"program","bin":"fzf","via":"npm","package":"fzf"}`. yolo generates
`~/.yolo-shims/fzf`, which is now ahead of the perfectly good `/bin/fzf` the image baked.
The launcher's install then fails (there is no such npm package, or the network is down, or
the name is wrong), and the launcher's tail is:

```bash
# internal/entrypoint/shims.go:306-311
if [ -x "$REAL_BIN" ]; then
    exec "$REAL_BIN" "$@"
else
    echo "  ⚠ __YOLO_BIN__ not available" >&2
    exit 1
fi
```

`REAL_BIN` is `$NPM_CONFIG_PREFIX/bin/<bin>` (`shims.go:265`) — **a single hardcoded path.**
The launcher never consults PATH, so `/bin/fzf` is unreachable. The pack asked for fzf and got
a jail where `fzf` exits 1.

**Verified:** the template at `shims.go:258-312`; `REAL_BIN` at `:265`; the exit-1 tail at
`:306-311`. `fd`/`fzf` baked at `flake.nix:658`/`:721`.

### Why it matters more than it looks

The failure is **inverted and silent**. A pack declaring a dependency is the most honest thing
a pack author can do, and it is precisely what makes the jail worse. Nothing warns; the author
sees a working `fzf` on their machine and a broken one in the jail, with a `program`
contribution that looks correct in `pack lint` and `pack footprint`.

It also punishes the good citizen twice: `install_hints` (Phase 8.3) exists so `apply --host`
can tell a user what to install, and a pack that adds hints — the thing we just asked packs to
do — must declare `program` to carry them.

### The questions

> **Q1.1 — When a `program`'s install fails but the binary already resolves on PATH, should
> the launcher fall through to it, or keep failing?**
>
> **Falling through** makes the jail work and matches the contribution's intent. But it
> **masks a genuinely broken install**: a pack whose npm package name is wrong would appear to
> work, silently running a *different* binary than the pack meant — which is its own class of
> bug, and this repo's whole recent history is about not silently substituting.
>
> **Keeping the failure** is honest but leaves the current behavior, where the honest pack
> loses.
>
> A third option: fall through **and warn loudly** ("`fzf`'s install failed; using the
> image-provided `/bin/fzf` instead"), which is the never-silent rule applied to both halves.
> I lean here, but the warning has to reach the user rather than only stderr inside a jail.

> **Q1.2 — Should yolo generate a launcher at all when the bin already resolves in the image?**
>
> Cheapest fix: skip generation, let `/bin/fzf` serve. But then the shim's *laziness* never
> applies to that bin — so if a later image drops the package, the pack that declared it gets
> nothing, with no shim to install it. It trades a loud break now for a silent one later.
>
> This also needs a decision on WHO checks: the entrypoint can `command -v` at generation
> time, but "resolves in the image" and "resolves on PATH" differ once other packs' shims
> exist.

> **Q1.3 — Is `program` even the right kind for "I need a tool the image probably has"?**
>
> The fzf pack's actual need is *"fzf must exist"*, not *"install fzf from npm"*. Those are
> different claims and `program` only expresses the second. A `requires`-style kind (assert
> presence, report if missing, install nothing) would let a pack carry `install_hints` for the
> host notch without generating a jail launcher at all — which is exactly what the fzf case
> wants. **Worth considering before patching the launcher**, since it may make Q1.1/Q1.2 moot
> for the common case.

---

## 2. Defect 11.2 — only the first `program` per pack installs in a jail

### What happens

```go
// internal/packdecl/contributes.go:123
func (m *Manifest) InstallContribution() *Install {
	for _, c := range m.Contributions() {
		if c.Kind != KindProgram { continue }
		in := &Install{Bin: c.Bin, Flags: c.Flags}
		…
		return in          // ← returns inside the loop
	}
	return nil
}
```

A pack declaring `fd` **and** `fzf` gets a launcher for `fd` only. Meanwhile
`DepRequirements()` (same file, `:139`) loops to completion and returns **both** — that is the
host path, used by `check-deps` and `apply --host`.

So the two notches disagree: the host reports two missing deps, the jail installs one. No
diagnostic either way.

### The ambiguity worth naming

`InstallContribution` is **singular**, and the accessor was written when a pack meant "an
agent" — one pack, one CLI. So this may be an *intended* one-program-per-pack rule that is
merely unenforced and undocumented, rather than a loop bug. The fix differs completely
depending on which:

- **Intended rule** → make a second `program` a validation error in
  `validateContribution`, and document it. Cheap, and it makes the host path's behavior the
  bug (it should also refuse).
- **Bug** → return a slice, generate N launchers, and audit callers.

### The question

> **Q2.1 — Is one `program` per pack the intended rule?**
>
> If yes, the pack model says "a pack installs at most one tool", and a pack needing two tools
> must be two packs (or use whatever Q1.3 produces). If no, this is a plain loop bug.
>
> I cannot tell from the code, and the answer changes both the fix and the pack-authoring
> story. **This is the one question here I genuinely cannot answer from evidence** — it is a
> product intent question.

---

## 3. Defect 11.3 — a dropped pack's staged tree is never cleared

### What happens

`stagePacks` clears the embedded-pack root only:

```go
// internal/cli/run/packs.go:93-99
officialRoot := filepath.Join(stagingRoot, "_official")
// Clear it: a pack DROPPED from config must stop being mounted, and a leftover tree
// would keep rendering as if it were still selected.
if err := os.RemoveAll(officialRoot); err != nil { … }
```

A **configured** pack's staging dir (`paths.AgentsDir()/<cname>/packs/<slug>`) is never
cleared. Removing the pack from config leaves its staged tree in place, and the entrypoint
renders every pack it finds under `YOLO_PACK_ROOT`.

**Observed live:** a deleted test pack kept regenerating its broken `fzf` shim across launches
until the staging dir was removed by hand.

### Why this one is unambiguous

It contradicts an invariant `AGENTS.md` states as fact:

> *"`stagePacks` copies only the SELECTED packs into the mounted tree (**and clears it, so a
> dropped pack stops rendering**)."*

So either the code or the doc is wrong, and here the code is the side producing the surprise —
the doc describes the behavior a reader would want. This is the least decision-laden of the
three.

### The constraint any fix must respect

`packstage`'s rule 3 (and `PrepareSkills`, and `GenerateShims`, all independently):
**clear the CONTENTS, never the dir itself.** A running jail's bind mount captured the staging
dir's inode; recreating it silently detaches the mount. `os.RemoveAll(stagingRoot)` is
therefore the wrong shape even though it is the obvious one.

### The question

> **Q3.1 — Clear the whole staging root's contents, or remove only the slugs no longer
> configured?**
>
> **Clear-then-restage** is simplest and matches `_official`'s existing treatment, but it
> re-copies every configured pack's tree on every launch (a cost that scales with pack size,
> and `packstage` already walks the source anyway).
>
> **Prune-unknown-slugs** preserves the incremental staging but needs the set of live slugs,
> and has to be careful about a slug that is configured-but-unresolvable (a fetched pack not
> yet `pack install`ed should probably NOT be pruned, or every offline launch would discard
> it).
>
> I lean prune-unknown, precisely because of the fetched-pack case: clear-then-restage would
> delete a pack the user still wants merely because it could not be fetched this launch.

---

## 4. What I would do, if the questions were mine

Not decisions — a starting position to argue with:

| # | Position | Confidence |
|---|---|---|
| 11.3 | Fix it. Prune unconfigured slugs, contents-only, never the dir. The doc already promises it and the fetched-pack case decides the shape. | **high** — least ambiguous |
| 11.1 | Do **Q1.3 first**: a presence-asserting kind probably dissolves the common case, and it is what the fzf pack actually wanted. Only then decide the launcher's fallthrough. | medium |
| 11.2 | Ask before touching. A validation error and a loop are opposite answers and the accessor's name hints at the former. | low — needs intent |

The ordering matters: patching the launcher (11.1) before answering Q1.3 risks building a
fallthrough that a new kind makes unnecessary.

---

## 5. Open questions, collected — **none of them are open any more**

For a reader who wants only the decisions. **Every one closed between 2026-08-02 and 2026-08-03,
and three of the five closed by being made unaskable rather than by being answered** — which is
why they are compacted here rather than left as live questions.

| ID | Ruling / Decision | Date | Settled in |
| :--- | :--- | :--- | :--- |
| **Q1.1** | **Moot.** Launchers moved to `~/.yolo-launchers`, ordered LAST on PATH, so an installer is unreachable while any real binary of that name exists — there is nothing to fall through to. The exit-1 tail is unchanged and still right for a genuinely absent tool | 2026-08-02 | §1 UPDATE, [`../plans/proposed-fixes-open-findings.md`](../plans/proposed-fixes-open-findings.md) §1 |
| **Q1.2** | **Moot, same cause.** Generation is harmless once ordering decides the winner; the launcher is still generated and still exits 1 when run directly, which is what proves ORDERING is the whole fix | 2026-08-02 | §1 UPDATE |
| **Q1.3** | **Yes — build it.** The `requires` kind shipped: asserts a binary is present, generates nothing, names a missing bin at boot, feeds `check-deps`/`apply --host`. `CombineShared`, not `CombineExclusive` | 2026-08-03 | §1 UPDATE |
| **Q2.1** | **A loop bug, not a rule** — 11.2 fixed | 2026-08-03 | §2 UPDATE |
| **Q3.1** | **Prune only unconfigured slugs, contents-only** — and a configured-but-unresolvable fetched pack is **KEPT**, exactly as this doc leaned. The staging root's own inode is never removed, because a live jail's `/ctx/packs` bind captured it | 2026-08-03 | `internal/packstage/packstage.go:29,48,230` (rule 3) |

> [!WARNING]
> **Q1.1/Q1.2 are moot, not answered — and the distinction is load-bearing.** What makes them
> unaskable is **PATH ORDER**: `~/.yolo-shims` (blockers) precedes the real tool because
> interception is its whole job, and `~/.yolo-launchers` (lazy installers) comes last, after
> `/bin`, so a launcher is reached only when nothing else provides the name. Reorder those two dirs
> and both questions come straight back, along with the defect they describe. The consequence to
> hold: **a name the image bakes now beats a pack's declared version** — right for `fzf`, and worth
> re-checking before baking any package whose name a pack also claims.
