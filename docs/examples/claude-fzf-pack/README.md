# claude-fzf — a personal pack for Claude Code's `@`-file finder

A complete, verified example pack: it replaces Claude Code's built-in `@`-file
completion with a custom `fd | fzf --filter` finder, at **both** the jail notch
and the host notch, from one declaration.

This is the concrete deliverable the pack/host-render work
([`../plans/pack-host-management-plan.md`](../plans/pack-host-management-plan.md))
existed to enable, and the worked example behind
[`../design/pack-config-collaboration.md`](../design/pack-config-collaboration.md).

**It is an EXAMPLE, not something yolo ships.** It deliberately does not live in
`packs/` — that directory holds the six official packs baked into the binary, and
adding a seventh would change the shipped product. Copy this directory to
`~/.dotfiles/claude-fzf/` (or anywhere) and point your config at it.

---

## What is in here

| File | Role |
|---|---|
| `pack.json` | the manifest: three contributions (`files`, `config-overlay`, `briefing`) |
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
    { "source": "file:///home/matt/.dotfiles/claude-fzf", "allow_exec": true }
  ]
}
```

**3. `"allow_exec": true` is required, and it is not optional pedantry.** A pack
is *content* — skills, prose, config fragments — so an executable arriving
through a content channel is a different trust question, and **the pack cannot
grant itself the exec bit**. `allow_exec` lives in *your* config, never in
`pack.json`. Without it the launch is refused:

```
✗ pack file bin/file-suggestion.sh is executable (mode 755) but the pack does
  not set allow_exec
```

That looks like a bug and is not: the consumer grants host power, not the pack
author. (Putting `allow_exec` in `pack.json` instead earns a second, separate
error — `unknown field "allow_exec"` — which is the pair of messages telling you
the knob is on the other side.)

**4. Verify, then use it:**

```console
$ yolo pack lint --allow-exec ~/.dotfiles/claude-fzf   # clean?
$ yolo apply --host                                    # observe: what WOULD change
$ yolo apply --host --assert                           # write it to your real home
$ yolo -- claude                                       # or just launch a jail
```

---

## How the three contributions work

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
$ yolo pack lint --allow-exec <pack dir> | grep config
  config-overlay claude/settings  contributes keys (owner still wins)   # ← this pack
$ yolo config ls claude | grep settings
  claude/settings  stateful     # ← still the claude pack's, still capturing edits
```

#### This pack used to declare `config`, and that is worth knowing

Until 2026-08-02 it declared `agent: claude, name: settings` itself — Layout B in
[`../design/pack-config-collaboration.md`](../design/pack-config-collaboration.md).
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

One consequence you can see: `apply --host` prints **one** `claude/settings
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

## Host deps (`fd`, `fzf`): declaring them as `program` is safe again

> **FIXED 2026-08-02.** The lazy launchers moved out of `~/.yolo-shims` into
> `~/.yolo-launchers`, which is ordered **last** on PATH (after `/bin`). An
> installer is now reached only when nothing else provides the name, so declaring
> a dep the image already satisfies no longer breaks it. Re-verified in a real
> container: with a pack declaring `{"kind":"program","bin":"fzf",…}`,
> `command -v fzf` → `/bin/fzf` and `fzf --version` → 0.

What used to happen, kept because it is why the split exists:

```console
$ command -v fd
/home/agent/.yolo-shims/fd            # ← not /bin/fd
$ fd --type f .
  Installing fd...
  ⚠ fd not available                  # the real /bin/fd is now unreachable
```

A `program` contribution generated its **lazy-install launcher** into
`~/.yolo-shims/`, which sits first on PATH and so **shadowed the image's real
binary** — and the launcher execs one hardcoded install path, never consulting
PATH, so it could not fall through. `fd` and `fzf` are both baked into the jail
image, so declaring them converted a working tool into a broken one.

This pack still declares no `program`, for the reason in the next section rather
than this one (only the first `program` per pack installs, so a two-binary pack
cannot be expressed yet).

**For the host notch,** where nothing is baked, the tools genuinely may be
missing. Options, in order of preference:

1. **Check them with `yolo check-deps`** against a pack that legitimately
   introduces them, or just install them once (`brew install fd fzf`). The
   finder degrades honestly: `fd` missing ⇒ the picker returns nothing, and the
   script's `2>/dev/null` keeps it from spraying errors into the UI.
2. If you do want them declared for host reporting, put the `program` entries in
   a **separate pack you select only at the host notch** — that keeps the
   `install_hints` value (`apply --host` prints `MISSING → brew install fd`)
   without generating a jail shim that shadows the image.

Note that even then, only the **first** `program` contribution per pack produces a
jail install; the rest are dropped (another finding below). So one binary per pack
is the only shape that behaves predictably today.

---

## Verification performed

Re-verified after the `config-overlay` conversion (2026-08-02), against a fresh
`./dist-go/linux-amd64/yolo` — not the baked launcher:

| # | Check | Result |
|---|---|---|
| 1 | `yolo pack lint --allow-exec` | clean; 3 claims, `config-overlay claude/settings contributes keys (owner still wins)` |
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
2. **Only the FIRST `program` contribution per pack installs in a jail.**
   `Manifest.InstallContribution()` returns on the first match, so a pack
   declaring `fd` *and* `fzf` silently gets a launcher for `fd` only — while
   `DepRequirements()` (the host path) correctly returns both. Two kinds are
   asymmetric about the same declaration, and the jail side drops data with no
   diagnostic. `program` is documented as sole-owned *per bin*, not per pack, so
   this looks like an oversight rather than a design limit.
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
