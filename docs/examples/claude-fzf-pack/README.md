# claude-fzf — a personal pack for Claude Code's `@`-file finder

A complete, verified example pack: it replaces Claude Code's built-in `@`-file
completion with a custom `fd | fzf --filter` finder, at **both** the jail notch
and the host notch, from one declaration.

This is the concrete deliverable the pack/host-render work
([`../plans/pack-host-management-plan.md`](../../plans/pack-host-management-plan.md))
existed to enable, and the worked example behind
[`../design/pack-config-collaboration.md`](../../design/pack-config-collaboration.md).

**It is an EXAMPLE, not something yolo ships.** It deliberately does not live in
`packs/` — that directory holds the official packs baked into the binary, and
adding one would change the shipped product. *(That count has moved since this was
written: `packs/` holds **ten** as of 2026-08-23 — six that install an agent and four
that ship only a loophole. The argument is unaffected; the number was.)* Copy this directory to
`~/.dotfiles/claude-fzf/` (or anywhere) and point your config at it.

---

## What is in here

| File | Role |
|---|---|
| `pack.json` | the manifest: five contributions (two `requires`, `files`, `config-overlay`, `briefing`) |
| `bin/file-suggestion.sh` | the finder itself — **a reference implementation to replace** |
| `AGENTS.md` | briefing prose telling the agent the finder exists and not to edit it in place |

### ⚠ `bin/file-suggestion.sh` is a starting point, not your script

The real finder lives at `~/.dotfiles/claude/file-suggestion.sh` on the host,
which is invisible from inside a jail (the credential boundary — see
`AGENTS.md` "Limitations"), so it could not be copied in. What ships here is a
**working** `fd | fzf --filter` implementation written from scratch and verified
end to end.

Swapping in your own is a **one-file edit**: nothing else in the pack references
the script's internals, only its path.

**Read the contract comment at the top of the script before you do.** It was read
out of the Claude Code binary (v2.1.220), and it is probably not what a
hand-rolled script assumes:

| | contract (verified, not guessed) |
|---|---|
| input | **one line of JSON on stdin**, with the typed text in a `query` field — **no `$1`** |
| output | one path per line on stdout; **only the first 15 are shown** |
| cwd | the project dir, so relative paths are correct |
| exit | **must be 0** — a non-zero exit discards all output (so a no-match `fzf --filter`, which exits 1, must be swallowed) |
| timeout | 5s, then results are dropped |
| shell | run through `bash -c`, so `~` and pipelines work in the `command` value |
| trust | skipped entirely until the workspace trust dialog is accepted |

If your existing script reads `$1`, it has been silently receiving an empty query
and returning an unranked dump of the tree.

---

## Adopting it

**1. Copy it out of the repo** (it is an example; edit it as yours):

```console
$ cp -r docs/examples/claude-fzf-pack ~/.dotfiles/claude-fzf
$ chmod +x ~/.dotfiles/claude-fzf/bin/file-suggestion.sh
```

**2. Add it to `~/.config/yolo-jail/config.jsonc`** — user scope only; a
workspace config cannot name a pack:

```jsonc
{
  "packs": [
    "claude",
    "file:///home/matt/.dotfiles/claude-fzf"
  ]
}
```

**3. Nothing to opt into: a pack ships its tools.** The executable stages
executable and arrives that way, which is the whole point — a pack delivering a
script nothing can run is a pack that has delivered nothing.

This step used to read the other way. Until 2026-08-30 the entry needed
`"allow_exec": true`, and without it the launch was refused with *"pack file
bin/file-suggestion.sh is executable (mode 755)"*. That gate is gone, key and
all: it read as a trust boundary and was not one, since `bash file.sh` never
needed the bit. **A config still carrying `allow_exec` is now refused as an
unknown key** — delete it.

Two things replaced it, and this pack sits on the right side of both:

- **`into` is `.claude/bin`, which is not on PATH.** A pack destination that
  lands on the jail's PATH is refused in the manifest, because a name on PATH is
  something a pack *declares* with a `program` contribution. This script is
  invoked by an explicit configured path (the `fileSuggestion` key below), never
  by name, so the rule does not touch it.
- **`yolo pack footprint` says what you ship**: `executables  1 file
  bin/file-suggestion.sh`. Ungated, not invisible.

**4. Verify, then use it:**

```console
$ yolo pack lint ~/.dotfiles/claude-fzf                # clean?
$ yolo host apply                                      # observe: what WOULD change
$ yolo host apply --assert                             # write it to your real home
$ yolo -- claude                                       # or just launch a jail
```

---

## How the contributions work

### `requires` → the two host tools (`fd`, `fzf`)

```jsonc
{ "kind": "requires", "bin": "fzf",
  "install_hints": { "brew": "fzf", "apt": "fzf", "dnf": "fzf", "pacman": "fzf", "nix": "fzf" } }
```

The finder shells out to `fd | fzf --filter`, so the pack needs both to **exist**. It does
not want yolo to install them — they are baked into the jail image, and on a host they are
the user's own package manager's business. That is exactly what `requires` says, and it is
the reason the kind exists (see the section further down for what this pack did before it).

`requires` **generates nothing**: no launcher, nothing on PATH, so it cannot shadow the very
binaries it asserts. What it buys is the two things silence cost:

- **in a jail**, an absent tool is named at boot — `pack claude-fzf requires fzf, which is
  not on PATH in this jail` — instead of the finder quietly returning no matches;
- **at the host**, the hints reach `yolo check-deps` and `yolo host apply`, which was the
  capability the old no-declaration workaround gave up:

```console
$ yolo host apply
  requires   ✓ fd               present at /usr/bin/fd
  requires   ✗ fzf              MISSING → brew install fzf
```

Note the `nix` hint is kept here and was **dropped** from the six shipped agent packs. That
asymmetry is the rule, not an inconsistency: an agent CLI ships its own installer *and*
updater, so the pack's own `via` is the better remedy; `fd`/`fzf` are ordinary third-party
deps where the user's package manager is right and nixpkgs is current.

### `files` → the script

```jsonc
{ "kind": "files", "from": "bin", "into": ".claude/bin" }
```

**`into` is `.claude/bin`, NOT `.claude`.** A `files` tree is a `:ro` bind mount
in the jail, so claiming the whole `.claude` directory shadows claude's own
`settings.json` surface and the boot is **refused** — the entrypoint would hit
`open /home/agent/.claude/settings.json: read-only file system`. There is a
pre-flight check for this now that names both packs, so you get a real error
rather than a mystery, but the fix is always a narrower `into`.

At the host notch the same declaration *writes the tree* instead of binding it,
read-only (`0o555`, executable preserved), refusing any path you own that yolo
has no record of writing.

### `config-overlay` → the `fileSuggestion` key

```jsonc
{ "kind": "config-overlay", "surface": "claude/settings",
  "config": { "managed": { "fileSuggestion": {
      "type": "command", "command": "~/.claude/bin/file-suggestion.sh" } } } }
```

The `claude` pack stays the **sole owner** of `~/.claude/settings.json`; this pack
is explicitly a **contributor**. It names the surface by identity and contributes
one key, folding in *below* the owner's `managed` layer — so claude's own
`preferences`, and its `permissions` from the `autonomy` kind, all coexist, and
the owner still wins a genuine conflict.

**What a contributor cannot do, mechanically:** the overlay body may carry only
`managed`. Every field that would redefine the *surface* — `agent`, `name`,
`path`, `codec`, `mode`, `transform`, `defaults`, `retireOnFirstRender` — is
refused **by name** at decode. So this pack cannot change where the file lands,
in what format, or how it is maintained across boots, even by accident.

That last one is the point. Confirm the owner's mode is untouched:

```console
$ yolo pack lint <pack dir> | grep config
  config-overlay claude/settings  contributes keys (owner still wins)   # ← this pack
$ yolo config ls claude | grep settings
  claude/settings  stateful     # ← still the claude pack's, still capturing edits
```

#### This pack used to declare `config`, and that is worth knowing

Until 2026-08-02 it declared `agent: claude, name: settings` itself — Layout B in
[`../design/pack-config-collaboration.md`](../../design/pack-config-collaboration.md).
That worked, by accident: two declarations of one surface identity resolved
*last-writer-wins, whole* (`manifest.Merge`), so the survivor brought its own
`mode`, `path`, `codec` and `defaults` with it. Adding `mode: "rmw"` would have
silently flipped claude's settings surface from `stateful` to `rmw`, **disabling
in-jail edit capture for `~/.claude/settings.json`** with nothing reported — which
is why the old version of this README carried a "🔒 DO NOT ADD `mode`" warning.

Per **ruling R1** that politeness was a workaround, not a fix: the hazard belonged
to the mechanism. Both halves are now shipped — `config-overlay` applies at both
render paths (Option 2), and a second `config` declaration of one identity is a
**refused collision** naming both packs (Option 1). So the old shape is no longer
merely discouraged; selecting this pack alongside `claude` with a `config`
declaration would refuse the launch, with the conversion above in the error.

One consequence you can see: `yolo host apply` prints **one** `claude/settings
rendered` line, not two. Two lines for one file used to be the tell that two packs
were fighting over it (ruling R4).

### `briefing` → telling the agent it exists

```jsonc
{ "kind": "briefing", "from": "AGENTS.md", "into": ".claude/CLAUDE.md" }
```

**Worth including, for one specific reason:** both paths this pack owns are
*traps for an agent that does not know they are managed*. `~/.claude/bin/` is a
read-only mount (an edit fails outright), and `fileSuggestion` is a `managed` key
(a hand-written value is re-asserted next boot). An agent that reaches for either
wastes a turn discovering this. The prose says "the pack is the source of truth,
edit it instead," which is exactly the class of thing briefings are for. It costs
three lines, and pack prose is attributed to its source, so it stays traceable.

---

## Host deps (`fd`, `fzf`): now declared as `requires` — and why they were not

> **RESOLVED 2026-08-03.** The pack declares `requires` for both tools, with
> `install_hints`. Three separate defects had to be fixed first, and each one is
> why the obvious declaration was wrong at the time:

**1. A `program` launcher shadowed the image's real binary** (fixed 2026-08-02 by
splitting the shim dir). The launcher went into `~/.yolo-shims/`, first on PATH,
and execs one hardcoded install path without ever consulting PATH — so declaring
a dep the image already satisfied converted a working tool into a broken one:

```console
$ command -v fd
/home/agent/.yolo-shims/fd            # ← not /bin/fd
$ fd --type f .
  Installing fd...
  ⚠ fd not available                  # the real /bin/fd is now unreachable
```

Launchers now live in `~/.yolo-launchers`, ordered **after** `/bin`, so an
installer is reached only when nothing else provides the name.

**2. Only the first `program` per pack installed** (fixed 2026-08-03).
`InstallContributions()` returned inside its loop, so a pack declaring `fd` *and*
`fzf` got a launcher for `fd` only — while the host path reported both. So even
after fix 1, a two-binary pack could not be expressed. It returns a slice now and
the generator writes N launchers.

**3. `program` was the only kind that carried `install_hints`** — and `program`
means *"yolo installs this"*, which is not what this pack wants. Fixed 2026-08-03
by the **`requires`** kind: presence-not-install, generates nothing, hints reach
the host notch. That is the declaration this pack now makes, and the gap it closes
is the one the old workaround left open — the pack carried no dependency
declaration at all, so `yolo host apply` could not tell a host user to install
`fd`/`fzf`, and the omission was invisible to anyone who did not know why.

For the **host notch**, where nothing is baked, the tools genuinely may be
missing, and now yolo says so with the command for your manager. The finder also
degrades honestly if you ignore it: `fd` missing ⇒ the picker returns nothing, and
the script's `2>/dev/null` keeps it from spraying errors into the UI.

---

## Verification performed

Re-verified after the `config-overlay` conversion (2026-08-02), against a fresh
`./dist-go/linux-amd64/yolo` — not the baked launcher:

| # | Check | Result |
|---|---|---|
| 1 | `yolo pack lint` | clean, 4 files stage; 6 claims, including `config-overlay claude/settings contributes keys (owner still wins)` and `executables 1 file bin/file-suggestion.sh` |
| 2 | `apply --host` observe, then `--assert`, on a throwaway `$HOME` | script at `0o555`; `fileSuggestion` alongside claude's `preferences`/`permissions`/`skipDangerousModePermissionPrompt`; **exactly one** `claude/settings rendered` line, annotated `config-overlay keys from: claude-fzf` |
| 3 | second `--assert` | **byte-identical** (sha256, all three files); briefing block count stays 1 |
| 4 | nested jail (`yolo -- …`, `YOLO_REPO_ROOT=/workspace`) | script present, executable, and **runs**; `fileSuggestion` wired; prose delivered; boot announces `claude/settings: config-overlay keys from claude-fzf` |
| 5 | the owner's mode is intact | capture sidecars (`claude-settings.overlay.json`, `.last_render`, `.provenance`) present and populated in the jail — `rmw` writes none at all, so their presence is the proof — and the provenance names the contributor: `fileSuggestion → config-overlay:claude-fzf` |

That last line is the whole difference the conversion makes. Under Layout B the same
key showed as `fileSuggestion → managed` with no record of *which* pack asserted it;
now the sidecar attributes it, which is what `yolo config diff claude` reads out.

The guarded-posture check also passes: at the host notch,
`skipDangerousModePermissionPrompt` is `false` and `permissions.defaultMode` is
`default`, so claude's jail-bypass keys do **not** reach the real home.

**Counterfactual, now enforced rather than merely documented** — the pre-conversion
`config` form (or any second `config` declaration of `claude/settings`) refuses the
launch:

```console
$ yolo -- bash
packs: config surface claude/settings claimed by claude, claude-fzf — a config surface
has exactly ONE owner. … these already disagree: mode (claude: "stateful" vs
claude-fzf: "rmw").
    To contribute keys to a surface another pack owns, declare `config-overlay` …
```

---

## Product findings from building this

Reported, not fixed (they live in files under concurrent development):

1. **A `program` contribution shadows an image-provided binary and breaks it.**
   ~~The generated `~/.yolo-shims/<bin>` launcher precedes `/bin` on PATH~~ —
   **FIXED 2026-08-02 by splitting the dir**: launchers now live in
   `~/.yolo-launchers`, ordered after `/bin`, so an installer is unreachable while
   a real binary of that name exists. Neither candidate fix was needed (fall
   through on failure / skip generation) — removing the shadowing removed the
   cause. The launcher's exit-1 tail is unchanged and still right for a genuinely
   absent tool. Original symptom, verified for both `fd` and `fzf`: a pack
   declaring a dep the image already satisfied made the jail *worse*, silently.
2. ~~**Only the FIRST `program` contribution per pack installs in a jail.**~~ —
   **FIXED 2026-08-03.** `Manifest.InstallContribution()` returned on the first
   match, so a pack declaring `fd` *and* `fzf` silently got a launcher for `fd`
   only — while `DepRequirements()` (the host path) correctly returned both. It is
   now `InstallContributions()`, returning a slice, and the generator writes one
   launcher per contribution. The reading that made it a bug rather than a design
   limit held up: `program` is sole-owned *per bin*, not per pack. The origin gate
   moved with it — `HonoredInstalls()` applies it **per contribution**, so a pack
   mixing an npm install with a curl-to-shell installer loses only the second.
   (A separate gap this exposed: `install_hints` lived only on `program`, which
   *implies* an install. That is what the new `requires` kind fixes, and it is why
   this pack can finally declare its deps at all.)
3. **A dropped pack's staged tree is never cleared, so it keeps rendering.**
   `stagePacks` clears only the `_official` subtree
   (`internal/cli/run/packs.go`); a *configured* pack's staging dir at
   `…/agents/<cname>/packs/<slug>/` survives being removed from config, and the
   in-jail loader walks every dir it finds under the pack root. Symptom hit
   during this work: a test pack deleted from config kept generating its
   `fzf` launcher (which then broke `fzf` per finding #1) across
   several launches, and the only fix was deleting the staging dir by hand. This
   contradicts the invariant stated in `AGENTS.md` ("`stagePacks` copies only the
   SELECTED packs into the mounted tree (and clears it, so a dropped pack stops
   rendering)") — the clear covers `_official` only.
4. **`yolo config ls` cannot show a configured pack's surface mode.** It merges
   *embedded* packs only (documented in `internal/cli/surfaces.go`), so the
   `stateful`-vs-`rmw` question this pack used to turn on is not answerable from
   `config ls` for a `file://` pack — `pack lint` is the working check. Worth
   knowing before pointing a user at `config ls` to verify R1.
