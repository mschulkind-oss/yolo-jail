# The `program` kind: three defects, and the questions they raise

**Status:** analysis + open questions, 2026-08-02. **No code changed.** Companion to
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

---

## 0. What `program` is supposed to do

A `program` contribution says *"this pack needs binary X on PATH"*. yolo honors it by
generating a **lazy launcher** at `~/.yolo-shims/<bin>` rather than installing at boot: the
launcher installs the tool on first use, then re-execs it. That laziness is the feature — a
jail starts fast and only pays for the agents you actually run.

Two facts make the defects below possible:

1. **`~/.yolo-shims` is FIRST on PATH.** The documented order is
   `$HOME/.yolo-shims:$HOME/.local/bin:$NPM_CONFIG_PREFIX/bin:<mise-shims>:$GOPATH/bin:/bin:/usr/bin`
   (AGENTS.md). So a shim always wins over `/bin`.
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

## 5. Open questions, collected

For a reader who wants only the decisions:

- **Q1.1** — install fails but the bin resolves on PATH: fall through, keep failing, or fall
  through with a loud warning?
- **Q1.2** — skip launcher generation entirely when the bin already resolves in the image?
- **Q1.3** — should there be a *presence-asserting* kind (`requires`?) distinct from
  `program`'s install-this semantics, so a pack can carry `install_hints` without generating a
  jail launcher?
- **Q2.1** — is one `program` per pack the intended rule (→ validation error) or a loop bug
  (→ return a slice)?
- **Q3.1** — on staging cleanup: clear-and-restage everything, or prune only unconfigured
  slugs? (And: should a configured-but-unresolvable fetched pack be pruned? I say no.)
