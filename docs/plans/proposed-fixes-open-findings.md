# Proposed fixes for the open findings

**Status:** proposals, 2026-08-02. **No code changed.** Written in answer to *"do you have
proposed fixes for what you're pointing out?"*

Everything below is an open item I flagged rather than fixed, each because it needed a
decision I did not think was mine. This doc says what I would actually do, and — where a
proposal is testable without building it — shows the measurement. Where I still think the
decision is yours, I say so and say why rather than picking.

**Reads with:** [`../design/program-kind-defects.md`](../design/program-kind-defects.md)
(Q1.1–Q3.1), [`pack-host-management-plan.md`](pack-host-management-plan.md) Phase 11 and
items 8.3/8.4, [`../design/pack-config-collaboration.md`](../design/pack-config-collaboration.md)
§8, and [`../design/host-nix-environment.md`](../design/host-nix-environment.md) OQ-6.

---

## Summary

| # | Finding | Proposal | Confidence |
|---|---|---|---|
| 1 | `program` shadows a baked binary and breaks it (11.1 / Q1.1) | **PATH-stripping fallthrough + a loud warning** — prototyped and measured below | **high** |
| 2 | Presence-vs-install conflated (Q1.3) | A `requires` kind. **But answer this BEFORE #1**, since it shrinks #1's blast radius | medium — worth your call |
| 3 | Only the first `program` per pack installs (11.2 / Q2.1) | **Validation error**, not a loop — the accessor's name is the evidence | medium |
| 4 | A dropped pack's staged tree keeps rendering (11.3 / Q3.1) | Prune unconfigured slugs, contents-only, **never** clear-and-restage | **high** |
| 5 | `depcheck.Manifest` cannot express a brew cask (8.4) | A `brew-cask` pseudo-manager key | **high** — mechanical |
| 6 | Three nix hints are `unfree` (8.3) | Report the constraint in the remedy line; do not auto-enable | medium |
| 7 | `packages: ["claude-code"]` fails at build with a nix trace (nix OQ-6) | `meta.unfree` check beside `availableOn` | **high** |
| 8 | `rmwProvenance` is a second implementation of "which layer won" | Leave it; strengthen the parity test to a shared-corpus table | medium |
| 9 | Nightly macOS builder arch mismatch (BACKLOG E8) | **No proposal — genuinely yours** | — |
| 10 | A pack cannot install Claude MCP servers on the host (new handoff) | Prune workspace-keyed subtrees instead of refusing the surface; **warn-and-confirm before the first destructive apply — RULED** | **high** — in progress |

---

## 1. The `program` shim shadowing a baked binary (11.1)

**The defect.** `~/.yolo-shims/<bin>` is first on PATH; the launcher execs only
`$NPM_CONFIG_PREFIX/bin/<bin>` (`shims.go:265`) and **never consults PATH**, exiting 1 when
that single path is missing (`:306-311`). So a pack that honestly declares `program fzf`
makes the image's working `/bin/fzf` unreachable.

**Proposal: fall through to PATH, minus our own directory, and WARN.** The doc's third
option, and the warning is not decoration — it is what keeps this from becoming a silent
substitution, which is the failure mode this codebase spent all night removing.

**The trap, and why the fix is not one line.** A naive `command -v fzf` inside the launcher
finds *the launcher itself* and execs forever. The shim must strip its own directory from
PATH first — and it does not currently know its own directory (nothing in the template
carries it).

**Prototyped and measured** (three cases, in a temp dir):

```bash
SELF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FALLBACK="$(PATH="$(printf '%s' "$PATH" | tr ':' '\n' | grep -vxF "$SELF_DIR" | paste -sd:)" \
            command -v __YOLO_BIN__ || true)"
if [ -n "$FALLBACK" ] && [ -x "$FALLBACK" ]; then
  echo "  ⚠ __YOLO_BIN__: install failed; using $FALLBACK from the image" >&2
  exec "$FALLBACK" "$@"
fi
echo "  ⚠ __YOLO_BIN__ not available" >&2; exit 1
```

| case | result |
|---|---|
| shim + a real binary later on PATH | `⚠ fzf: install failed; using …/realbin/fzf from the image` then **`REAL-FZF-RAN`** |
| shim + the image's own `/bin/fzf` | falls through to `/bin/fzf` |
| shim + genuinely absent binary | `⚠ nosuchtool not available`, **exits, no recursion** |

**Where it goes:** both `npmLauncherTemplate` and `nativeLauncherTemplate` (they share the
tail), plus a `__YOLO_SELF_DIR__` substitution if `BASH_SOURCE` resolution is considered too
clever — `GenerateAgentLaunchers` already knows `e.ShimDir()`.

**Why not Q1.2 (skip generation when the bin already resolves).** It is cheaper, but it
trades a loud break now for a silent one later: if a future image drops the package, the pack
that declared it gets nothing and there is no shim to install it. The fallthrough degrades;
skipping generation disappears.

---

## 2. A `requires` kind — and why it should be decided FIRST (Q1.3)

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

**Why order matters.** If `requires` lands, the common case for #1 disappears — a pack
declaring what it *needs* stops generating a shim at all, and `program` narrows to "yolo
installs this tool", which is the only case the launcher was designed for. **Building the
fallthrough first risks building it for a case that then stops existing.** #1 is still worth
doing (a genuinely npm-installed agent whose install fails should still degrade), but it gets
smaller.

**Your call because it adds a kind.** The kind set is closed and core-owned on purpose; a
14th entry is a product decision, not a refactor. It also now costs a doc update in two
places plus the drift test I added in Phase 0.

---

## 3. One `program` per pack: validation error, not a loop (11.2)

`InstallContribution()` returns inside its loop (`contributes.go:123`), so a pack declaring
`fd` and `fzf` gets a launcher for `fd` only — while `DepRequirements()` returns both.

**Proposal: make a second `program` a validation error**, and document one-per-pack.

The evidence is the accessor's own name: **singular**. It was written when a pack meant "an
agent" — one pack, one CLI — and the pack model has not actually outgrown that for
*installs*. A pack needing two tools wants #2's `requires` for the second, or is two packs.

Also note which side is the bug: returning a slice would make the JAIL match the host, but it
would also mean a pack can generate N launchers, each carrying #1's shadowing hazard. The
validation error makes both notches agree in the *safer* direction.

**Do not do this before #2**, or a pack legitimately needing two tools has no expression at
all.

---

## 4. A dropped pack's staged tree (11.3)

`stagePacks` clears `_official` only (`packs.go:97`); a configured pack's staging dir survives
removal from config, and the entrypoint renders every pack under `YOLO_PACK_ROOT`.
Contradicts an invariant `AGENTS.md` states as fact.

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

## 5. Brew casks in the dep manifest (8.4)

`depcheck.Manifest` writes every package as `brew "<pkg>"`, but a Brewfile `brew` entry
installs with `--formula`. Four of the six shipped hints are **casks**, so the generated
Brewfile fails. (The printed one-liner is fine — bare `brew install <token>` resolves either.)

**Proposal: a `brew-cask` pseudo-manager key** in `install_hints`, which `installCmd` maps to
`brew install --cask <token>` and `Manifest` emits as `cask "<token>"`.

Cheaper and more honest than a per-hint struct: the hint map is `map[string]string` and its
whole virtue is that a pack author writes one line per manager. A key that names the
*installer flavor* fits that grain; a nested object per hint does not, and would need every
existing hint rewritten.

Detection stays as-is — `DetectManager` returns `brew`, and `installCmd` consults both keys.

---

## 6. The three unfree nix hints (8.3)

`claude-code`, `github-copilot-cli` and `antigravity-cli` are `unfree`, so
`nix profile install nixpkgs#<pkg>` refuses until the user allows unfree. **The package name
is right; the remedy alone is insufficient.**

**Proposal: say so in the line.** Something like

```
✗ claude   MISSING → nix profile install nixpkgs#claude-code
                     (unfree: needs NIXPKGS_ALLOW_UNFREE=1 or nixpkgs.config.allowUnfree)
```

**Do NOT auto-add `NIXPKGS_ALLOW_UNFREE=1` to the printed command.** Unfree is a licence
decision the user makes once, machine-wide; a tool that slips the override into a
copy-pasteable line makes it for them silently. Naming the constraint respects the same
consumer-grants-power invariant `allow_exec` follows.

Needs a per-hint annotation, so it probably wants #5's key-flavor mechanism or a small
`unfree` marker — worth doing them together.

---

## 7. `packages: ["claude-code"]` fails with a raw nix trace (nix OQ-6)

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

**This is the item I am least sure about.** If a third notch ever derives provenance a third
way, the duplication becomes the wrong call and a real abstraction is owed.

---

## 9. Nightly macOS builder arch (BACKLOG E8) — no proposal

`publish.yml` pushes the builder image `aarch64-linux` **only**, with a comment saying why;
`nightly-macos.yml` runs `macos-26-intel`. Both fixes — publish multi-arch, or drop those two
tests from the Intel nightly / move it to Apple Silicon — change what the product ships or
what it claims to test.

**I have no proposal here on purpose.** This is a product decision about supported platforms,
and the failure is honest as it stands.

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

0. **#10** (host MCP servers) — **in progress**, both rulings settled, and it is the one item
   with a user-visible capability behind it rather than a latent hazard.
1. **#7** (unfree eval) and **#5** (brew cask) — mechanical, isolated, no decisions pending.
2. **#4** (staging prune) — high confidence, closes a doc/code contradiction.
3. **Decide #2** (`requires`). Everything in the `program` cluster is shaped by the answer.
4. **#1** (fallthrough) and **#3** (validation error) — after #2, because #2 changes their
   scope.
5. **#6** (unfree annotation) — rides #5's mechanism.
6. **#8** (parity table) — whenever provenance is next touched.

**One dependency worth noting:** #10 threads `stdin` into `applyHost` for its confirm prompt.
Nothing else here needs it, but if #10 lands first that plumbing is already in place for any
future host-notch confirmation — including env-manager Phase 4.3's confirm-gated install, which
has been deferred partly for want of exactly that.
