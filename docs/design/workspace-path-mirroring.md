---
title: "Should the jail mount the workspace at the host's own path?"
date: 2026-09-04
status: draft
tags: [mounts, paths, backends, macos-user, state-separation, trust]
summary: "Two questions, one verdict. Mirroring just the workspace is a good idea not worth the churn. MAXIMAL mirroring — jail user, home, workspace and toolchain store all matching the host — is mechanically expressible and churn is not the objection; it fails because you can mirror a path NAME but not its CONTENT, so a cross-boundary reference resolves to an ABI-incompatible artifact instead of failing loudly, the credential boundary loses its cheapest signal, and macos-user cannot express the mirrored home. The two arguments expected to carry it (capture relocation, notch portability) come back negative. Recommendation: no; if the goal is one environment at several notches, unify the userland instead."
---

# Should the jail mount the workspace at the host's own path?

**Status:** DESIGN SKETCH, 2026-09-04. **Reopened and extended the same day** — see the
postscript. Nothing built, and my recommendation is still that nothing should be, but the
reason has changed completely. This doc exists to make the *no* checkable rather than
reflexive — the question was asked once before, answered no for reasons that have since
expired, and deserves a fresh answer rather than a citation.

> **Postscript, 2026-09-04 — the question got bigger, and my central argument became a
> variable.** §1–§11 answer a **narrow** question: mount the *workspace* at the host path,
> leaving `HOME=/home/agent` and the toolchain at `/mise`. The maintainer's actual proposal is
> **maximal mirroring** — *"what if we changed anywhere in our design that we need to match
> paths? The user could have the same name as the host user, we could mirror things there, and
> elsewhere. Still no benefit? I don't care how much code churn there is."* That moves three
> things out of the cost column and into the design: the jail home, the toolchain store, and
> churn. [§12](#12-follow-up-maximal-mirroring) is the re-run, and it **retracts
> P1, P3 and half of P5**. §1–§11 keep their original tense; read them as the narrow answer,
> which still stands on its own terms. The verdict below covers both.
>
> A later input the same day — *"we may need to make it optional at first, this is going to be
> a big change"* — is answered in [§12.9](#129-optional-first-transition-or-permanent).

**The short version — maximal mirroring (the live question).** Make every path agree: a jail
user named after the host user, `HOME=/home/matt`, the workspace beneath it, the toolchain
store wherever it must be for references to resolve. It is **mechanically expressible** — I
measured the whole mount stack, including a read-write workspace nested inside a `:ro` home
base — and with churn off the table my original central objection is dead. **It still fails,
for a reason I did not have on the first pass: you can mirror the *name* of a path but not its
*content*.** The host is Arch and the jail is NixOS (measured: the jail's `rg` loads
`/nix/store/…-glibc-2.42-67/lib64/ld-linux-x86-64.so.2`); on macOS one side is Mach-O and the
other ELF. So a mirrored reference *resolves* on both sides and yields an ABI-incompatible
artifact, where today it fails loudly with ENOENT. yolo has shipped code whose entire job is
to catch exactly this (`internal/cli/run/retire.go`) and whose detection method is the path
prefix; mirroring degrades it to always-pass. The same argument already killed uv-cache
sharing (`jail-state-separation-design.md`:325-334 — *"silently reused … persistent, invisible
cache poisoning"*). **Verdict: no.** And the two arguments that were expected to cut *for* it
come back negative — mirroring does not delete capture relocation, it *creates* the need for
it ([§12.6](#126-capture-relocation--chased-hard-it-cuts-the-other-way)). If the real goal is
one environment at several confinement notches, the lever is **userland unification**
(`yoloNoncontainerPackages`), not the mount table — [§10 alternative G](#10-alternatives-each-with-a-verdict).

**The short version — the narrow question (§1–§11, superseded in scope but not retracted).**
Mirroring means dropping `/workspace` and bind-mounting the host
directory at its own absolute path (`/home/matt/code/yolo-jail` in the jail as well as on
the host). The build cost is smaller than folklore suggests — `${workspace}` and
`Env.WorkspaceDir()` already exist because the `macos-user` backend has no `/workspace`, so
the change is roughly two mount lines, one env var, and four probes. **The problem is that
it does not deliver the property it is sold on.** The jail's home stays at `/home/agent` and
its toolchain store at `/mise`, so "absolute paths are identical on both sides" would be
true of exactly one subtree — the workspace — which is the one subtree that is already the
same inode. Against that: the three absolute-path problems I could confirm are each small
and each has a cheaper targeted fix; ~273 lines of human- and agent-facing text become
wrong or become variables; mirroring re-opens a shared-store collision class that the
2026-07 state-separation bundle closed deliberately; and the proposal's best argument — that
`macos-user` already mirrors — inverts on inspection, because that backend *refuses* a
workspace under a user home. **Verdict: no — the "good idea, not worth the churn" flavour of
no**, with one confirmed problem worth fixing on its own terms today
([§3.1](#31-confirmed-jail-built-go-binaries-carry-workspace-source-paths)).

**Evidence labelling**, following [`program-delivery.md`](program-delivery.md)'s convention:
every fact below is **MEASURED** (observed running, in this development jail, 2026-09-04,
podman 5.8.4 + crun 1.27.1), **READ FROM CODE** (traced to a `file:line` but not observed
running), or **NOT MEASURED**.

**Reads with:**
[`../research/mise-host-jail-path-mismatch.md`](../research/mise-host-jail-path-mismatch.md)
(where this question was first asked, as "option A", and answered no on 2026-07-03),
[`jail-state-separation-design.md`](jail-state-separation-design.md) (the bundle that made
that answer right, and the corollary that makes mirroring actively harmful),
[`jail-home.md`](jail-home.md) (the mount stack this would edit),
[`host-execution-from-the-workspace.md`](host-execution-from-the-workspace.md) (the outbound
threat model, which this changes less than I expected),
[`../plans/install-capture.md`](../plans/install-capture.md) (relocation — the same family
of problem, solved the opposite way).

---

## 1. Verdict, and the five claims it rests on

**Recommendation: do not mirror.** Keep `/workspace`.

I want to be precise about the flavour of no, because there are three of them and they carry
different follow-ups. This is not "the idea is wrong" and it is not "the status quo is
fine". It is: **the problems are real but few, each has a cheaper independent fix, and the
blast radius is every sentence anyone has written about a jail path.** Five claims, each
argued below.

**P1. Mirroring cannot make the two sides agree, because the workspace is one of three path
spaces and the other two are not moving.** A jail path is drawn from `/workspace`,
`/home/agent`, or `/mise`. Mirroring unifies the first. The second cannot be unified —
`HOME` is set unconditionally to `/home/agent` (`internal/cli/run/assemble.go:745`) and the
jail's home being *not* the host's home is the whole [`jail-home.md`](jail-home.md) design —
and the third was deliberately *de*-unified in 2026-07. The result is a jail whose workspace
is `/home/matt/code/proj` and whose home is `/home/agent`: a mixed world, not a mirrored one.
So the sales pitch, "absolute paths are identical on both sides", is true only of the subtree
where the two sides already share an inode and a relative path. §5.

> ### ⚠ Retracted 2026-09-04: P1 does not survive the maximal proposal
>
> P1 treats `HOME=/home/agent` and `/mise` as fixed. Under maximal mirroring they are exactly
> what also moves, and I **measured** that the resulting mount stack works
> ([§12.3](#123-it-is-mechanically-expressible--measured)). P1 is a correct objection to the
> narrow proposal and a wrong one to the real proposal. Its replacement is
> [§12.4](#124-the-new-central-objection-you-can-mirror-a-name-but-not-its-content).

**P2. It re-opens a bug class that was closed on purpose, and the closing doc says so in as
many words.** `/workspace` is not merely a name; it is a *namespace-collapsing device*
*(coined here)* — every workspace on the machine gets the same string inside its own jail,
backed by a different directory. For machine-shared state keyed by something other than the
workspace, that collapse is what makes one entry resolve correctly in every jail.
[`jail-state-separation-design.md`](jail-state-separation-design.md):221-224 records the
corollary directly: *"option A would destroy this — real host paths make every project's
string unique, so any two same-version projects would conflict unconditionally. A is not
just weaker here; it's actively worse."* §2.2, §4.2.

**P3. The migration cost is not a one-time edit; it is a permanent tax on prose.** Today
`/workspace` is a constant that every doc, briefing, skill, and error message can name.
Under mirroring it becomes a per-machine variable that none of them can. `AGENTS.md` alone
carries 12 lines of `YOLO_REPO_ROOT=/workspace`, and the built-in `developing-yolo-jail`
skill carries 17 — all of them instructions to be *run inside the jail*, where they are
correct today and would become unwriteable. §6.3.

> ### ⚠ Retracted 2026-09-04: P3 is withdrawn from the verdict
>
> The maintainer removed churn from the cost side explicitly (*"I don't care how much code
> churn there is"*). §6 stays as **sizing** — it is the honest measure of the work — but it is
> no longer an argument, and none of §12's reasoning uses it. The one part of P3 that is not
> churn is that `/workspace` stops being a nameable constant; that is folded into
> [§12.5](#125-the-credential-boundary--the-argument-that-could-have-killed-it-and-does) as
> the side-ambiguity problem, where it belongs.

**P4. The confirmed benefit is narrow and mostly purchasable separately.** Three problems
survived the sweep. One (jail-built Go binaries carrying dead source paths) is fixed by
adding `-trimpath` to one build script. One (a machine-shared cache directory keyed by the
collapsed name) affects a log directory today. One (paths written in-jail that a human later
tries on the host) is the only thing mirroring uniquely fixes, and it is a documentation
problem the briefing already warns about. §3.

**P5. The proposal's best argument inverts on inspection.** This is the finding I did not
expect, and I would rank it above P4. The argument is that `macos-user` already uses the
host path, so mirroring would bring the container backends into line with a backend that
exists. It does not survive contact: `macos-user` **refuses a workspace inside any user
home** (`internal/macosuser/runplan.go:301-307`), so `/Users/matt/code/proj` — precisely the
path mirroring would produce on a Mac — is illegal on that backend today. Mirroring would
make podman-on-macOS and `macos-user` diverge *more*, not less. §7.2.

> ### ⚠ Partly retracted 2026-09-04: P5's constraint is a decision
>
> `macos-user`'s neutral-ground rule is something this repo *chose*, not something macOS
> imposes, so a maximal proposal may simply revisit it. The half of P5 that survives is
> narrower and is re-argued at [§12.8](#128-macos-users-neutral-ground-is-it-a-constraint-or-a-choice).

### 1.1 What would change my mind

Stated up front so the doc is falsifiable rather than merely argued. Any one of these flips
the arithmetic:

- A **second** machine-shared, workspace-path-keyed consumer appears under `~/.cache` with a
  real payload rather than logs (§3.2 found exactly one, holding logs).
- Debugging jail-built binaries **from the host** becomes routine rather than hypothetical,
  and `-trimpath` turns out not to cover it (§3.1).
- The jail home stops being `/home/agent` — i.e. someone proposes mirroring *all three* path
  spaces. That is a different, larger, and more coherent proposal than this one, and P1 would
  no longer apply to it. See [`OQ-WP6`](#open-questions).

---

## 2. What `/workspace` is today

### 2.1 The mount

Two lines, one per container backend — **READ FROM CODE**:

- `internal/cli/run/assemble_parts.go:106` — `"-v", workspace+":/workspace"` (podman)
- `internal/cli/run/assemble_parts.go:59` — the same, Apple Container

plus `--workdir /workspace` (`internal/cli/run/assemble.go:322`), the image's
`WorkingDir = "/workspace"` (`flake.nix:1040`), and a pre-created mountpoint in the image
(`flake.nix:998`, whose comment says it exists *"for --read-only root filesystem"*).

The host path is **not** hidden. `YOLO_HOST_DIR` carries it into every jail
(`internal/cli/run/assemble.go:765`; **MEASURED** — this jail's environment holds
`YOLO_HOST_DIR=/home/matt/code/yolo-jail`) and it is printed in the shell prompt
(`internal/entrypoint/shell.go:59`), in `yolo ps` (`internal/cli/ps.go:139`), and in the
agent briefing, which states both paths side by side
(`internal/jailcontent/briefing.go:337`). **Nothing depends on `/workspace` being opaque.**
The interesting dependencies are all on its being *stable* and *uniform*, which is a
different property.

### 2.2 The thing it quietly is: a namespace-collapsing device

Every jail on the machine sees its own workspace at the same string. That single fact cuts
in opposite directions depending on what is doing the keying, and the whole design question
reduces to which direction dominates.

| Consumer shape | `/workspace` (collapsed) | Mirrored (unique) |
| :--- | :--- | :--- |
| Machine-shared store keyed by **tool+version**, whose values are workspace-derived paths (the mise `installs/rust/<ver> → $CARGO_HOME/bin` shape) | One string, resolving per-jail to that jail's own backing. **No conflict.** | Every workspace writes a different value into one key. **Permanent last-writer-wins.** |
| Machine-shared store keyed by **the project path** (`~/.cache/claude-cli-nodejs/<mangled cwd>`) | Every workspace collides on one key. | Correct separation. |
| Per-workspace state (`<ws>/.yolo/home/**`, and everything mounted from it) | Unaffected — the backing is already per-workspace. | Unaffected. |

Row 3 is most of the state and is not in play. Rows 1 and 2 are a genuine trade, not a win
for either side, and §3 and §4.2 size each one.

The collapse is also used *deliberately*, which is the part that makes it a design element
rather than an accident. The per-side shadow mounts are exactly this trick applied on
purpose: `.venv` and `node_modules` appear at the same path in every jail and on the host,
each backed by a different directory (`internal/cli/run/mounts.go:78-109`;
[`jail-state-separation-design.md`](jail-state-separation-design.md) calls it
*"the same string-uniform/per-side-backing trick"*). `CARGO_HOME=/mise/cargo` is the same
move: [`storage-and-config.md`](storage-and-config.md):367 says it exists so the recorded
`installs/rust/<ver>` symlink *"resolve[s] identically in every jail"*.

### 2.3 The prior art, and why it is not the answer

This is **option A** in
[`../research/mise-host-jail-path-mismatch.md`](../research/mise-host-jail-path-mismatch.md),
asked as `OQ-MP1` and answered **no** on 2026-07-03. Two of its three stated reasons have
expired and should not be recycled:

- *"Its original main benefit — host↔jail resolution — is already delivered by the split."*
  Still true, and still the strongest half of the old answer.
- *"Changes a documented invariant (`/workspace` everywhere), needs a canonicalization test
  pass."* Substantially weaker now. The codebase was Python then, with the literal spread
  everywhere; today the seam exists (§6.1).
- `OQ-MP2` asked whether Apple Container supports arbitrary same-path bind targets and was
  closed **moot** without an answer. Still unanswered for AC (§7); answered for podman here
  (**MEASURED**, §7.1).

So the old *no* is not a citation I can lean on. The parts of it that survive are P1 and the
`jail-state-separation-design.md`:221-224 corollary — which is a stronger objection than
anything in the original list.

**And there is a second piece of prior art, running the other way, that the July discussion
only half-states.** yolo *used to* same-path-mount the mise store: `~/.local/share/mise` →
`/home/matt/.local/share/mise`, mirrored precisely so host-written absolute paths would
resolve in-jail ([`jail-state-separation-design.md`](jail-state-separation-design.md):67).
That mirroring was deleted in 2026-07 in favour of the fixed neutral `/mise` (`:61`), for
three reasons that read as a point-by-point rebuttal of the present proposal: it "keeps the
host username baked into every jail" (`:81`); it made jails host-layout-dependent where the
goal was *"identical jails on gauss, macOS, anywhere"*
([`../research/mise-host-jail-path-mismatch.md`](../research/mise-host-jail-path-mismatch.md)
§F+); and it required a dedicated nested-jail propagation variable, `YOLO_OUTER_MISE_PATH`,
which the neutral path deleted (`:132`).

**yolo has run this experiment on one path space and moved away from mirroring.** That is the
single most relevant fact in this document, and it is not an argument from authority: the
reasons it moved are mechanical, and all three apply again here.

---

## 3. The absolute-path problems, measured

I went looking for confirmed cases rather than plausible ones. Three survived; several
prominent candidates did not, and the disproved ones are recorded because a future reader
will re-derive them otherwise.

### 3.1 CONFIRMED: jail-built Go binaries carry `/workspace` source paths

**MEASURED.** `dist-go/linux-amd64/yolo` contains **394** strings beginning `/workspace/`;
`dist-go/linux-amd64/yolo-entrypoint` contains **122**. The only `-trimpath` in the tree is
`flake.nix:149`, in the hermetic image build; `scripts/build-go.sh` — the cross-compile step
`just build-go` runs — passes none.

**MEASURED**, on the mechanism: Go's *compile* action is already path-independent (it runs
with `-trimpath "$WORK/b001=>"`), but the link output is not. Identical source built at two
different directories under one `GOCACHE` produced different binaries, added 7 cache entries
on the second build, re-ran one action, and the second binary contained its own absolute
source path.

**Consequence.** A panic trace, a `delve` session, a pprof source view, or any
`runtime.Caller` output from a jail-built binary run *on the host* names a path that does
not exist there. Mirroring fixes it. **So does adding `-trimpath` to `scripts/build-go.sh`,
which is one line, matches what the image build already does, and is worth doing whatever
the verdict on mirroring** — see [`OQ-WP2`](#open-questions).

### 3.2 CONFIRMED: one machine-shared cache directory is keyed by the collapsed name

**READ FROM CODE:** `~/.cache` inside a jail is `paths.GlobalCache()`
(`internal/cli/run/assemble_parts.go:120`), which is
`~/.local/share/yolo-jail/cache` — **shared by every workspace on the machine**
([`jail-home.md`](jail-home.md) §2.1, layer 3).

**MEASURED:** of its 24 top-level entries, exactly one is keyed by a workspace path —
`~/.cache/claude-cli-nodejs/-workspace/`, the cwd-derived mangling Claude Code uses. Every
jail on the machine writes into that one key. Its current contents are three MCP log
directories. `go-build`, `uv`, `npm`, `pip`, `pex`, `pants`, `nce`, `gh` and the rest are
content-addressed or tool-keyed and are unaffected.

**Consequence.** Real, and structurally the right shape to worry about, but the blast radius
today is interleaved logs. Mirroring fixes it. So does binding a per-workspace directory over
that one path — a targeted fix for one vendor's layout, which is its own argument against
(§10, alternative D).

### 3.3 CONFIRMED: `/workspace` paths written in-jail are dead on the host

**MEASURED and self-demonstrating.** `AGENTS.md` — written in this jail, read on both sides —
carries 12 lines naming `/workspace` paths. `internal/cli/briefing.txt:39` tells every agent
*"Workspace path is `/workspace` (not the host's absolute path)"*, and
`internal/cli/config_ref.txt:1422` repeats it. Those two lines are the system admitting the
cost.

**This is the one mirroring uniquely fixes**, and it is the weakest of the three, because the
direction that hurts is narrow. A path an agent writes into a doc for a *jail* reader is
correct today and would become a per-machine string under mirroring (P3). Only an artifact
that genuinely *crosses* — a stack trace pasted into a host terminal, a
`compile_commands.json` a host editor reads, a `.pyvenv`-style recorded interpreter — gets
better. I could not find a `compile_commands.json`-class consumer in this repo, so the
crossing case here is §3.1 plus human copy-paste.

### 3.4 Candidates that did NOT survive

Recording these so the next reader does not re-derive them.

| Candidate | Finding |
| :--- | :--- |
| `~/.claude.json` records project dirs by absolute path | **True but harmless.** **MEASURED:** exactly one `projects` key, `/workspace`. The file lives in the *per-workspace* overlay (`<ws>/.yolo/home/claude/claude.json`, symlinked from the `:ro` base at `internal/storage/ensure.go:102`), so the collapsed name never collides. Same for `~/.claude/projects/-workspace`. |
| mise records workspace-derived absolute paths into a shared store | **Historical, and already fixed the other way.** This was the 2026-07 incident. **MEASURED:** `/mise` holds **zero** symlinks pointing into `/workspace` today, because `CARGO_HOME=/mise/cargo` moved the target out of the workspace ([`storage-and-config.md`](storage-and-config.md):367). Mirroring would *re-open* it — §4.2. |
| `node_modules` / `.venv` cross the boundary badly | **Real, and not a path problem.** Both are per-side shadow-mounted (`internal/cli/run/mounts.go:78-109`). The stated reason for `node_modules` is native builds and userland skew, not path spelling (`mounts.go:60-66`), and mirroring does not touch it. `.venv`'s path half is genuine but partial — see §5. |
| Go build cache poisoned by two projects sharing the name | **No.** Content-addressed; a same-path different-source build misses rather than false-hits (**MEASURED**, §3.1). |
| Prism sidecars and receipts under `<ws>/.yolo` | **No.** Per-workspace by construction, and already path-parameterized: sidecar root is `filepath.Join(t.Workspace, ".yolo", "prism")` (`internal/render/target.go:246`), receipts path is baked from `Env.WorkspaceDir()` at generation time (`internal/entrypoint/shims.go:448`). |
| LSP servers reporting absolute paths | **Not a crossing.** The editor runs in the jail too, so both ends agree. **NOT MEASURED** beyond that reasoning. |
| `mise` config paths | **Fixed already, and not by mirroring.** `MISE_TRUSTED_CONFIG_PATHS=/workspace` (`internal/cli/run/assemble.go:729`) is a jail-side value matching a jail-side mount; `MISE_DATA_DIR=/mise` is deliberately neutral. |

---

## 4. What mirroring would actually change

### 4.1 The ledger, honestly

| | Better | Worse | Unchanged |
| :--- | :--- | :--- | :--- |
| Jail-built binaries debugged on the host | ✅ §3.1 | | |
| Machine-shared, project-keyed caches | ✅ §3.2 | | |
| Paths in prose that a *host* reader tries | ✅ §3.3 | | |
| Paths in prose that a *jail* reader runs | | ❌ P3, §6.3 | |
| Machine-shared, tool-keyed stores holding workspace paths | | ❌ §4.2 | |
| Nested jails: path stability across depths | ✅ §4.3 | | |
| Nested jails: the same-workspace home-overlay footgun | | | ➖ §4.3 |
| Backend agreement (podman-on-macOS vs `macos-user`) | | ❌ §7.2 | |
| `.venv` / `node_modules` portability | | | ➖ §5 |
| Jail home and toolchain paths | | | ➖ §5 (P1) |
| Trust and scope model | | ⚠ §4.4 (one accidental fail-safe) | mostly ➖ |
| Host-username exposure | | | ➖ §4.5 |

### 4.2 The re-opened class

The mise incident's mechanism, restated in the mirrored world. A machine-shared store keyed
by `(tool, version)` holds one value per key. Today, two workspaces pinning the same version
with a `{{config_root}}`-relative `CARGO_HOME` both write `→ /workspace/.cargo/bin` — the
same string, resolving in each jail to that jail's own workspace, so the write is a
byte-identical no-op. Mirrored, workspace A writes `→ /home/matt/code/A/.cargo/bin` and
workspace B writes `→ /home/matt/code/B/.cargo/bin` into one key. Whichever wrote last owns
it; in the loser's jail the target is not mounted at all, so the symlink dangles — which is
precisely the failure the 2026-07 incident opened with.

Two honest qualifications. First, `CARGO_HOME=/mise/cargo` makes the common case neutral, so
the residual needs a workspace that overrides `CARGO_HOME` to a `config_root`-relative path
(polyclav did, which is how the incident happened). **MEASURED:** zero such entries in `/mise`
today. Second, this is one backend of one tool. But the *class* is the point, and the class
is one-directional: mirroring gives every machine-shared store a per-workspace fanout it did
not have, in exchange for the per-project separation of §3.2. Adding an escape hatch for the
next instance is the whack-a-mole the neutral-path decision was made to end.

### 4.3 Nested jails — a small plus, smaller than it first looks

**READ FROM CODE.** There is no nested-aware workspace translation anywhere: `o.Workspace`
is `os.Getwd()` (`internal/cli/run/runcmd.go:209`) and flows straight into the bind
source. So an inner jail launched from `/tmp/yolo-nested` *inside* the outer jail binds the
**outer container's** path, and `YOLO_HOST_DIR` in that inner jail is a container path, not
a host path (`internal/cli/run/assemble.go:765` is a pure echo of the bind source). Under
mirroring the two levels get distinct, stable strings, and this repo's own dev-loop
incantation (`YOLO_REPO_ROOT=/workspace …`, `AGENTS.md:281-287`) would become one string at
every depth.

Two things keep this from being worth much. The throwaway-workspace convention already
sidesteps the confusion; and the footgun AGENTS.md actually warns about — a nested launch
*on* the outer jail's own workspace, where `<ws>/.yolo/home/claude` and `/home/agent/.claude`
are the same inode — is a property of the bind **source**, so mirroring leaves it exactly as
dangerous. Note also the direction of history: the neutral-path migration *deleted* the
nested propagation variable it needed (§2.3), so mirroring is the direction that
historically required more nesting plumbing, not less.

### 4.4 Trust: one undocumented fail-safe, and nothing else

I expected to find a security control resting on the paths differing. There is not one.

- The "inside the workspace this launch bind-mounts" refusal
  (`internal/config/loopholeplacement.go:102-113`, comparison at `:284-286`) compares
  **host** paths on both sides — the host cwd against a host-side module dir. Unaffected.
- The user-scope-only boundaries (`packs`, `host_files`, `host_wrappers`,
  `host_apply_on_launch`) are enforced by **which file is read**
  (`internal/config/hostwrappers.go:33-35`, `internal/config/hostapplyonlaunch.go:47-49`),
  not by classifying path strings. [`trust-paths.md`](trust-paths.md) treats *workspace
  scope*, never the literal `/workspace`, as the untrusted marker.
- [`host-execution-from-the-workspace.md`](host-execution-from-the-workspace.md)'s outbound
  threat model nowhere relies on a jail-written path failing to resolve on the host; its
  §5.4 mechanism (`per_side_paths`) is the *opposite* idea — one path, two backings.
- The in-jail sentinel is `YOLO_VERSION` (`internal/config/load.go:315-317`), not a path.

**The one accidental mitigation.** `internal/config/validate.go:333-335` skips a `mounts`
entry whose host path does not exist. Workspace-scope `mounts` has no scope rule
([`trust-paths.md`](trust-paths.md):331), so an agent editing the workspace config can
declare one today — and a `/workspace/...` entry is silently skipped host-side because that
path is not there. Mirrored, it would resolve and be mounted at `/ctx/<basename>`
(`internal/cli/run/assemble.go:944`). **MEASURED:** the worst shape I could construct — a
workspace `mounts` entry whose basename collides with the pack staging root at
`/ctx/packs` (`internal/cli/run/packs.go:34`) — is refused by podman outright
(`Error: /ctx/packs: duplicate mount destination`, in either argv order), so it is a launch
failure, not a shadowing attack. What remains is that a workspace subdirectory the jail can
already read gains a second read-only appearance under `/ctx`. Low, but **no document claims
path divergence as the mitigation**, which makes this an undocumented control being removed —
see [`OQ-WP4`](#open-questions).

### 4.5 Leakage: nothing new

The host path is already in every jail (`YOLO_HOST_DIR`, **MEASURED**, §2.1), in the prompt,
in `yolo ps`, and in the briefing. Mirroring exposes no new information. The
[`mise-host-jail-path-mismatch.md`](../research/mise-host-jail-path-mismatch.md) note that it
*"[l]eaks the host username/layout into the jail (mild)"* was already true when written and is
fully true now. **This is not an argument against.**

---

## 5. What this does NOT fix, and what it does not license

The negative space, and P1's evidence.

**It does not make absolute paths agree.** Three path spaces cross the boundary; mirroring
unifies one:

| Space | Host | Jail | Under mirroring |
| :--- | :--- | :--- | :--- |
| Workspace | `/home/matt/code/proj` | `/workspace` | **unified** |
| Home | `/home/matt` | `/home/agent` (`assemble.go:745`) | still different — and deliberately so ([`jail-home.md`](jail-home.md)) |
| Toolchain store | `~/.local/share/mise` | `/mise` | still different — and deliberately so ([`jail-state-separation-design.md`](jail-state-separation-design.md)) |

**Concretely: it does not fix venv portability**, which is the case most often cited for it.
A jail-made `.venv` records `home = /mise/installs/python/<v>/bin`. Mirroring the workspace
leaves that string untouched, so the venv is still jail-only, and the per-side shadow mount
is still required. The same holds for anything whose *content* names an interpreter, a shim,
or a home-relative tool — which is most installed software.

**It does not license mirroring the home.** That is a different and larger proposal
(`OQ-WP6`), and every argument here about the *workspace* would have to be re-run for it.

**It does not bring the backends into line.** The opposite — §7.2. Anyone arguing for
mirroring on parity grounds is arguing against the evidence.

**It does not license a config knob.** Two spellings of the workspace path is a second way to
do one thing, which [`happy-path-principle.md`](happy-path-principle.md) exists to refuse
(*"Fill the matrix. Support one path per cell"*). §10, alternative C.

**It does not change the outbound host-execution threat model** (§4.4), and it must not be
argued for on security grounds in either direction.

### 5.1 Relocation, and the same problem answered the opposite way

[`../plans/install-capture.md`](../plans/install-capture.md) names **relocation** as the hard
part of its `macos-user` slice: an installer writes absolute self-references to the prefix it
ran under, so a capture made at one prefix cannot be materialized at another. Its answer is
to record every absolute reference to the staging prefix in the manifest and rewrite them at
materialize time — *"or the entry is admitted `relocatable:false` and refuses to materialize
elsewhere"* (`install-capture.md`, build order step 6).

This is the same underlying fact as the present proposal — **installed software embeds
absolute self-references** — and the two reach for opposite strategies. Mirroring says *make
the paths equal so nothing needs rewriting*. Capture says *record the references and rewrite
them*. Both are legitimate, and yolo has already shipped one of each: the neutral `/mise` path
is the third strategy (*make the path a constant nobody has to agree about*), and capture's
manifest rewrite is the second.

**Verdict: unchanged.** Mirroring neither helps nor hurts relocation, for a specific reason —
the prefix capture struggles with is the **home/staging** prefix
(`<CapturesDir>/staging/<id>` at capture time, the shared `/Users/_yolojail` home at
materialize time), not the workspace. Mirroring moves the workspace and nothing else (P1), so
every rewrite capture must perform is still there afterwards. The plan is explicit that a
capture is not supposed to touch the workspace at all: `SeatbeltCaptureProfile(stagingHome)`
grants **no workspace write** (`install-capture.md` Map, `internal/macosuser/seatbelt.go`).

One second-order note, pointing the wrong way. A capture entry is per-machine but *not*
per-workspace, because its surfaces are home-relative
(`dedupeSubtrees = {npm-global, local, go}`). If anything ever made a capture's content
workspace-path-dependent, mirroring would fan the CAS out per workspace — the §4.2 shape
again. That does not happen today, and the reason it does not is precisely that the capture
surfaces live in the *home* path space, which mirroring leaves alone.

---

## 6. What breaks — the census

`rg -n '/workspace'` over the tree: **470 matches, 451 matched lines, 142 files**
(**MEASURED**, 2026-09-04). Classified:

| Category | Lines | Load-bearing? |
| :--- | ---: | :--- |
| Go code — real literals | 19 | **yes** — §6.1 |
| Go tests | 94 | mechanical (goldens + fixtures) |
| Generated in-jail text (Go string literals) | 14 | **yes** — §6.2 |
| Built-in skills staged into the jail | 23 | **yes** — §6.3 |
| CLI text assets (`internal/cli/*.txt`) | 15 | **yes** — §6.3 |
| Test goldens of generated jail content | 2 | mechanical |
| `flake.nix` | 3 (2 real) | **yes** — §7.1 |
| Config schema prose | 2 | cosmetic |
| Go comments (prose about the mount) | 60 | cosmetic, but wrong if unedited |
| `docs/` + `AGENTS.md` + `README.md` + `scripts/` prose | 219 | **the tax** — §6.3 |

Headline: **~48% is docs prose and another ~13% is Go comments.** The executable surface is
19 literals.

### 6.1 The code side is smaller than folklore says

There is no `paths.JailWorkspace` constant — `internal/paths/paths.go` contains zero
occurrences. But the *seam* already exists, built for `macos-user`:

- `internal/agentcfg/builtin.go:32` — `const WorkspacePlaceholder = "${workspace}"`, whose
  rationale comment (`:20-31`) says outright: *"the workspace root is NOT always
  `/workspace`: `Env.WorkspaceDir` honors `YOLO_WORKSPACE`, and the macos-user backend has no
  `/workspace` at all. A literal in the manifest was therefore a latent correctness bug."*
- `internal/entrypoint/env.go:195-200` — `Env.WorkspaceDir()`, contract at `:38-42`:
  *"Generators that used to hardcode `/workspace` read this instead so the same code is
  correct on both platforms."*
- `internal/render/target.go:51` — `Target.Workspace`.

Consumers already literal-free include `shims.go:448`, `bootlog.go:82`, `prism.go:304,510,605`
and `hostrender.go:386,429,916`. What was never parameterized is a short list:

| Site | What it is |
| :--- | :--- |
| `assemble_parts.go:59,106` | the two mounts |
| `assemble.go:322` | `--workdir` |
| `assemble.go:532,535` | `/dev/null` shadow-outs of `.vscode/mcp.json` and `.overmind.sock` |
| `assemble.go:729` | `MISE_TRUSTED_CONFIG_PATHS` |
| `mounts.go:37,50,109` | `workspace_readonly` and per-side shadow joins |
| `command.go:32` | `const startupLog = "/workspace/.yolo/startup.log"` |
| `check/sections_misc.go:104`, `prune/probes.go:140` | find the bind by **destination** in `podman inspect` |
| `configls.go:391` | `workspaceRoot() == "/workspace"` — the `config reset`/`capture` locality guard |
| `config/load.go:349` | `jailOwnWorkspace` fallback |
| `entrypoint/env.go:185,197` | the in-jail default |
| `cli/config.go:239` | `const containerWorkspace`, the `yolo config render` preview value |
| `run/retire.go:27` | `/workspace/` in `jailPrefixes`, host-side jail-made-venv detection |
| `entrypoint/prism_mise.go:120` | `var workspaceMisePath` |

Two of those carry judgement rather than mechanics:

> [!WARNING]
> `internal/config/load.go:335-337` states the assumption this proposal inverts, verbatim:
> *"`YOLO_HOST_DIR` is deliberately NOT used: it is the HOST-side path of that mount, **which
> never matches the in-jail path** a caller passes here."* Under mirroring the two always
> match, and every reader of that comment has to re-derive why the code is still right. And
> `YOLO_WORKSPACE` — the override both `load.go:349` and `env.go:185` read — is **never set by
> any launcher** (`internal/entrypoint/shims.go:442-444`: *"a HOST-side launcher input that is
> absent inside a live container"*), so `/workspace` is the effective hardcode in both.

`configls.go:391` fails **closed** under mirroring (it would refuse `config reset`/`capture`
in-jail rather than permit them), which is the right direction for an unconverted call site
to fail.

### 6.2 Generated jail content

`internal/entrypoint/shell.go`'s `venvPrecreateScript` (`:415-483`) is the only *executable*
generated content with live `/workspace` uses — six of them (`:423,424,468,477,478,482`),
including the tera `config_root` resolution at `:462-468`. The rest is text:
`internal/jailcontent/briefing.go:301,302,337,415,423` (`:337` is inside the byte range pinned
by `TestBriefingJailHeaderIsUnchanged`, per the comment at `:320`) and one shell comment
at `shell.go:160`.

### 6.3 The prose tax — the part that does not end

This is P3, and it is the cost I would weight highest.

`internal/cli/briefing.txt:39` (*"Workspace path is `/workspace` (not the host's absolute
path)"*) and `internal/cli/config_ref.txt:1422` (*"The workspace path changes from the host
path to `/workspace`"*) become false verbatim; that much is a one-time edit. The permanent
cost is that **`/workspace` stops being nameable**. The built-in `developing-yolo-jail` skill
carries 17 lines of `YOLO_REPO_ROOT=/workspace`; `AGENTS.md` carries 12; both are
instructions to run *inside a jail*, where they are correct today. Under mirroring each must
become `$YOLO_HOST_DIR` or a per-machine literal — a variable where there was a constant, in
text whose entire job is to be copy-pasteable. Multiply across 60 doc files and every future
sentence about a jail path.

The counter-argument deserves stating: those `/workspace` strings are already wrong for a
*host* reader, and a variable is honest. I do not find it persuasive, because the readers of
those particular lines are in the jail.

---

## 7. Backends

### 7.1 podman: expressible, and the image constraint is not one

**MEASURED**, podman 5.8.4 + crun 1.27.1, this jail, 2026-09-04:

- `-v /tmp/x:/home/matt/code/proj` on a `--read-only` rootfs
  (`internal/cli/run/assemble.go:258`), alongside a `:ro` `/home/agent` — mounted, readable,
  both visible in `/proc/self/mountinfo`; `/home/matt` was created in the rootfs by the
  runtime.
- `-v /tmp/x:/Users/matt/code/proj` — a **brand-new top-level** destination, macOS-shaped —
  same.
- A destination nested *inside* a `:ro` parent bind, and one nested inside a `--tmpfs /tmp`
  — both work.
- Duplicate destinations are refused outright (`duplicate mount destination`), in either
  order.

Mount **ordering** is a non-issue: podman sorts by destination depth, which the code already
relies on and states (`internal/cli/run/assemble_parts.go:122-129`, *"reversing the two args
behaves identically"*). So a deep destination changes nothing there.

> [!NOTE]
> This retires a plausible objection before someone raises it. `flake.nix:998` pre-creates
> `./workspace` *"for --read-only root filesystem"*, which reads like a hard requirement that
> a mirrored (arbitrary, per-machine) destination could not satisfy. It is not — and the
> image itself already says so, in the F8 note directly below that line (`flake.nix:999-1006`):
> podman *"creates a nested mountpoint under `/ctx` on demand even with `--read-only` —
> verified live"*. The second measurement above extends that from a nested dir under a baked
> parent to a **new top-level** dir, which is the case F8 did not cover and which nobody had
> probed. The related EROFS caveat in the code
> (`internal/cli/run/assemble_parts.go:144-151`) is scoped to a `:ro` **bind** parent, not to
> the read-only rootfs, and [`jail-home.md`](jail-home.md):238-246 records that even that
> narrower claim resisted reproduction.

Two residual podman questions. **Collision with the jail's own fixed paths** — a host
workspace at `/home/agent/...`, `/mise/...`, `/bin`, `/opt/yolo-jail` or `/ctx` would be
refused as a duplicate destination or shadow something the jail needs; absurd in practice,
trivially detectable, but a yes owes an explicit refusal list (§9). And note the asymmetry
the code already documents: a missing mount **destination** auto-creates, but a missing
**source** is fatal and the container never starts
([`../plans/cache-relocation.md`](../plans/cache-relocation.md):93).

### 7.2 macos-user does not mirror the path a Mac user would want

This is the framing I most wanted to check, because it is the proposal's best argument. It
does not hold up, and the reason is sharper than "different backends differ".

**READ FROM CODE.** The `macos-user` backend is not a container; nothing is bind-mounted
(`internal/macosuser/` contains zero occurrences of `/workspace`), so the workspace path is
the real host path: the launch argv `cd`s into it directly
(`internal/macosuser/macosuser.go:357`), the Seatbelt profile grants `(subpath <workspace>)`
verbatim (`internal/macosuser/seatbelt.go:34-84`), and
`internal/macosuser/orchestrator.go:157` sets `MISE_TRUSTED_CONFIG_PATHS` to it where the
container branch sets the literal `/workspace` (`internal/cli/run/assemble.go:729`).
`internal/agentcfg/builtin.go:24-25` states it plainly — *"the macos-user backend has no
`/workspace` at all"*.

**But that host path is constrained to be neutral ground.** `internal/macosuser/runplan.go:301-307`
refuses a workspace inside any user home — *"the macos-user backend shares only neutral
ground. Move it under `/Users/Shared/yolo/…`"* — with `HomeContaining`
(`internal/macosuser/macosuser.go:233-245`) rejecting anything whose ancestor chain hits
`/Users/<non-Shared>`. So on macos-user, `/Users/matt/code/proj` — **exactly the path a
mirroring scheme would produce on a Mac** — is already illegal.

Two conclusions follow, and they run in the same direction:

- **`/workspace` is not the odd one out.** Two backends have a mount and collapse the name;
  one has no mount and therefore no name to choose. That is the mount existing or not, not a
  2-versus-1 anomaly — the same way macos-user has no `/home/agent` (one shared
  `/Users/_yolojail`), no image, and no `per_side_paths` (*"Seatbelt can deny a path, it
  cannot fork one"*, [`macos-user-nix-and-features.md`](macos-user-nix-and-features.md):363-366).
  [`happy-path-principle.md`](happy-path-principle.md) expects backends to fill different
  cells; `${workspace}` is the machinery that lets them.
- **macos-user is not a precedent for mirroring, because it forbids the mirrored path.**
  Adopting mirroring on the container backends would make podman-on-macOS and macos-user
  disagree *more* than they do today, not less.

[`backend-parity.md`](backend-parity.md) does not record the workspace path as a parity gap,
and structurally cannot: its census vocabulary is `packdecl.KnownKinds()` ∪
`config.knownTopLevelConfigKeys` (`:132-138`), and the workspace root is neither. The
difference is documented in
[`macos-user-nix-and-features.md`](macos-user-nix-and-features.md):286-292 and `:639` instead.

What macos-user *does* prove is worth something, and it is a cost argument, not a correctness
one: the `${workspace}` seam works, and the host-path form is already exercised in production
code.

### 7.3 Apple Container: probably fine, genuinely unverified

**NOT MEASURED**, and I have no hardware. The good news is that AC's three known limits are
not the ones a rename would trip. Mirroring is **mount-count-neutral** — it renames one
destination and adds none, which matters because the mount-count pressure is what forced the
single-writable-`/home/agent` shape (`internal/cli/run/assemble_parts.go:70-77`; the
often-quoted "~22" limit is [not something this repo measures](../guides/macos.md)). Nesting
depth is fine — the shared cache is already mounted at `/home/agent/.cache` inside the
`/home/agent` mount on every AC launch ([`../guides/macos.md`](../guides/macos.md):462-464).
The real AC limits — `:ro` binds ignored, single-file binds impossible — are untouched by a
destination rename.

What remains is that nobody has confirmed an arbitrary deep destination on AC. That is
exactly `OQ-MP2`, asked in July and closed **moot** rather than answered — and un-mooting
option A re-opens it. See [`OQ-WP5`](#open-questions). Second-order: `workspace_readonly` and
the per-side shadows build `"/workspace/"+rel` destinations
(`internal/cli/run/mounts.go:37,50,109`), so mirroring moves those strings too, on a backend
where they are already degraded.

---

## 8. Migration

**READ FROM CODE.** Existing state is keyed two ways, and only one of them moves.

- **Per-workspace state does not move.** The overlay root is
  `filepath.Join(o.Workspace, ".yolo", "home")` — computed host-side from the workspace
  *directory*, never from the in-jail name (`internal/cli/run/prepare.go:302`). Mirroring
  changes the mount **destination** and leaves the **source** alone, so overlays, shadow
  backings, prism sidecars and receipts all survive untouched. The live hazard AGENTS.md
  warns about — a nested launch on the outer jail's own workspace regenerating the running
  session's home, because `<ws>/.yolo/home/claude` and `/home/agent/.claude` are one inode —
  is a property of that source directory, so it is unchanged too.
- **Content that recorded the old name is stranded.** Anything holding `/workspace` as data:
  `~/.claude.json`'s `projects` key and `~/.claude/projects/-workspace` (session history keyed
  by the mangled path — **MEASURED**, one key), `~/.cache/claude-cli-nodejs/-workspace`, any
  `.venv` shadow whose `pyvenv.cfg` names a `/workspace` interpreter, and generated agent
  config already on disk. The claude case is the visible one: an agent's session history for
  the workspace would appear empty after the switch, because it is filed under the old key.

A transition period does not help, because the two spellings cannot be live at once — the
container has one mount, and a compatibility symlink `/workspace → <host path>` (the original
option A's shape) reintroduces exactly the canonicalization ambiguity that made option A need
*"a test pass"*: `pwd` and `pwd -P` disagree, and every tool that canonicalizes silently picks
the mirrored spelling while every doc says the other. **If this were done, it should be a
clean break with a storage-layout-version bump** (`internal/storage/ensure.go:22`, currently
2) and an announced one-time loss of path-keyed agent history — which is itself a decent
argument for not doing it for the benefits in §3.

---

## 9. If the answer were yes anyway — what the design would still owe

Recorded so a future *yes* does not start from zero, and so this doc's *no* is not resting on
unexamined difficulty. None of this is an implementation plan; each item is a behavioural
decision an implementer would otherwise make silently.

- **A refusal list for host paths that collide with jail-fixed paths** — `/home/agent`,
  `/mise`, `/ctx`, `/opt/yolo-jail`, `/bin`, `/usr`, `/run`, `/tmp`, `/var` and their
  ancestors — checked at launch, refusing rather than warning.
- **A rule for a host path that is not representable in the jail** — a case-insensitive
  macOS path, a path containing a newline, a path longer than the runtime accepts.
- **`YOLO_WORKSPACE` becomes a real launcher output** rather than the never-set input it is
  today, and every reader of the `/workspace` fallback loses the fallback.
- **The two `podman inspect`-by-destination probes** (`check/sections_misc.go:104`,
  `prune/probes.go:140`) need the destination computed, not constant — and `yolo prune`'s
  correctness depends on finding that mount.
- **A decision on the machine-shared stores** that §4.2 fans out, stated as a policy rather
  than per-tool patches.
- **What done looks like:** a jail whose `pwd` equals its `YOLO_HOST_DIR`; a jail-built binary
  whose embedded source paths resolve on the host; two workspaces with distinct
  `~/.cache/claude-cli-nodejs` keys; `yolo check` and `yolo prune` both still finding the
  workspace mount; and `yolo config reset` still refusing from the wrong workspace.

---

## 10. Alternatives, each with a verdict

**A. Mirror the host path (the proposal).** — **Rejected**, per §1. Cheaper to build than
believed, but it does not deliver its headline property (P1), re-opens a closed class (P2),
costs a permanent prose tax (P3) for three small benefits (P4), and widens the backend gap it
was supposed to close (P5).

**B. Mirror the path *and* keep `/workspace` as a symlink** (the original option A shape). —
**Rejected, and it is the worst of the set.** It pays the full prose tax *and* keeps the old
name alive, so the tree now documents two spellings of one directory; and it adds the
canonicalization ambiguity §8 describes, where `pwd -P`, watchers, and any tool that resolves
symlinks silently disagree with every doc. Strictly worse than A.

**C. Make it a config knob** (`workspace_mount: "fixed" | "mirror"`). — **Rejected.** A second
way to do one thing, which [`happy-path-principle.md`](happy-path-principle.md) refuses on
principle (*"A second option only earns its place if it covers a matrix cell the first
cannot"* — this covers no new cell). Worse than either pole in practice, because every doc,
skill, briefing and pack would have to hedge on a value it cannot know.

**D. Fix the three confirmed problems individually.** — **Accepted as the recommendation.**
`-trimpath` in `scripts/build-go.sh` closes §3.1 for one line (`OQ-WP2`). §3.2 is a log
directory and I would leave it (`OQ-WP3`). §3.3 is documentation, and the briefing already
says the true thing. This is the "targeted fix beats a general one when the general one has a
worse cost profile" call, and it is a judgement, not a derivation.

**E. Maximal mirroring** — workspace *and* home *and* toolchain store *and* the jail user's
name. — **Evaluated in [§12](#12-follow-up-maximal-mirroring) (2026-09-04); rejected.** It is
mechanically expressible (MEASURED, §12.3), so A's P1 does not apply to it, and churn is not
counted against it. It is rejected because it makes path *names* agree while leaving path
*contents* side-determined, converting loud ENOENT failures into silent wrong-artifact ones
(§12.4); because it deletes the cheapest signal the credential boundary has (§12.5); and
because on `macos-user` the mirrored home is inexpressible three ways over (§12.8). The two
arguments expected to carry it — capture relocation and notch portability — come back negative
(§12.6, §12.7). *This entry replaced "not evaluated here"; `OQ-WP6` is answered by §12 and
re-leaned accordingly.*

**F. Do nothing.** — **Rejected**, narrowly: §3.1 is a real defect with a one-line fix and
should not ride on this verdict.

**G. Unify the *userland*, and let path agreement follow.** — **Accepted as the direction, if
the goal is cross-notch portability.** §12.4's objection is that mirroring does the naming
half of a two-part problem and leaves the dangerous half undone. Doing the other half first
inverts that: if both sides run the same nix-provided userland, content agrees, and then
whatever path agreement is needed is safe rather than merely convenient. The mechanism exists
and is named — `yoloNoncontainerPackages` / `yoloUnavailablePackages`
(`flake.nix:1204,1210`), the subject of
[`noncontainer-nix-environment.md`](noncontainer-nix-environment.md), whose OQ-1 was answered
by events on 2026-09-02 (*"the host notch is a place agents run"*, `yolo host -- <cmd>`
shipped 2026-08-30). This is a much larger programme than mirroring and I am not proposing it
here; I am naming it as the place the maintainer's underlying goal actually lives, so that a
"no" to mirroring is not read as a "no" to that goal. See [`OQ-WP10`](#open-questions).

---

## 11. Risks

These are the risks *of adopting the proposal*, ranked by how badly they would be
underestimated.

| # | Risk | Severity | Mitigation |
| :--- | :--- | :--- | :--- |
| R1 | Machine-shared stores gain a per-workspace fanout; the mise dangling-symlink class returns in the jail↔jail direction (§4.2) | High — it is the class the 2026-07 bundle closed | None structural. Per-tool neutral paths (the `CARGO_HOME=/mise/cargo` move) treat each instance; that is the whack-a-mole the neutral-path decision ended |
| R2 | The prose tax is paid forever, not once (§6.3, P3) | High, and easiest to miss because it is not a code cost | None. It is inherent to replacing a constant with a per-machine variable |
| R3 | The container backends and `macos-user` diverge *more*, because macos-user refuses a workspace under a user home — the exact path mirroring would produce on a Mac (§7.2) | High, and I did not expect it | None. It is a direct conflict between two backends' path rules, not a gap to fill |
| R3a | Apple Container may not express an arbitrary deep destination (§7.3) | Unknown — unverified since July, though the known AC limits are not the ones a rename trips | Verify on hardware before committing (`OQ-WP5`); AC has no fallback spelling |
| R4 | Path-keyed agent history is stranded at the old key (§8) | Medium, one-time, user-visible | Storage-layout-version bump + an announced loss; no migration is possible for a vendor-owned key |
| R5 | Two unconverted call sites change behaviour rather than erroring: `configls.go:391` (fails closed) and `load.go:349` (stops recognising its own workspace) | Medium | Both are named in §6.1; convert with the mount, not after |
| R6 | Workspace-scope `mounts` entries that are inert today become live (§4.4) | Low — measured to fail closed on the worst shape | Give workspace `mounts` a scope rule, which [`trust-paths.md`](trust-paths.md):331 arguably already wants (`OQ-WP4`) |
| R7 | A host workspace path collides with a jail-fixed path (§7.1) | Low, absurd in practice | An explicit launch-time refusal list (§9) |

**Risks specific to maximal mirroring** (added 2026-09-04 with [§12](#12-follow-up-maximal-mirroring)).
R2 is withdrawn from this table by the churn ruling; R1, R4, R6 and R7 carry over unchanged.

| # | Risk | Severity | Mitigation |
| :--- | :--- | :--- | :--- |
| R8 | A cross-boundary reference resolves to an ABI-incompatible artifact instead of failing with ENOENT (§12.4) | **Critical, and it is the verdict** | None available. The two sides are different userlands by design; only alternative G addresses it |
| R9 | `retireJailMadeVenv` (`internal/cli/run/retire.go:60`) degrades from a working guard to always-pass (§12.4) | High — a shipped safety net stops working, silently | Replace the path-prefix test with something that survives mirroring. Nothing in the tree offers one; a content probe (`file`, ELF interpreter) would have to be invented |
| R10 | Host-credential paths and their jail namesakes become the same string, in agent-editable config (§12.5) | High | Scope-rule workspace `mounts` (`OQ-WP4`), which is worth doing regardless and is not sufficient alone |
| R11 | On `macos-user`, a sandbox home at `/Users/<hostuser>` hands the agent read-write over the human's home via `(subpath <sandboxHome>)` (§12.8) | **Critical, mechanical** | None. `dscl` also refuses the duplicate shortname, so the shape is inexpressible rather than dangerous — but only because macOS stops it, not because yolo does |
| R12 | Captures and other home-relative artifacts stop being machine-independent, foreclosing cross-machine reuse (§12.6) | Medium, and it is a foreclosure rather than a break | None. It is inherent: the mirrored home embeds the host username |
| R13 | A "temporary" optional flag becomes permanent, leaving two live path layouts forever (§12.9) | Medium | A removal condition stated as a checkable test, up front — not a date |

---

---

## 12. Follow-up: maximal mirroring

> Added 2026-09-04, after the maintainer read §1–§11 and said they answered a narrower
> question than he asked. He is right. This section re-runs the analysis with the home, the
> toolchain store, and the jail user's *name* all in play, and with churn removed from the
> cost side by explicit instruction.

### 12.1 What is actually being proposed

*Maximal mirroring* **(coined here, to distinguish it from §1–§11's workspace-only mirroring)**:
make every absolute path a jail can name equal to the path the host would use for the same
thing. Concretely, on a Linux host whose user is `matt`:

| | Today | Maximal mirroring |
| :--- | :--- | :--- |
| Jail user | `root` (uid 0), `/etc/passwd` baked (`flake.nix:1011`) | a user named `matt`, possibly the host's uid |
| `HOME` | `/home/agent` (`internal/cli/run/assemble.go:745`) | `/home/matt` |
| Workspace | `/workspace` | `/home/matt/code/proj` — **beneath the home** |
| Toolchain store | `/mise` | `/home/matt/.local/share/mise` |

It is not "the jail shares the host's home". The jail's `/home/matt` would still be
`~/.local/share/yolo-jail/home` on the host — a **different directory wearing the same name**.
That distinction is the hinge of everything below.

### 12.2 What changed about the evaluation

Three of my five claims are affected, and I have marked each at its original site:

| Claim | Status |
| :--- | :--- |
| P1 — "only the workspace would match" | **Retracted.** The other two spaces move too, and the mount stack works (§12.3) |
| P2 — re-opens the shared-store class | **Survives, and generalizes** (§12.7) |
| P3 — the prose tax | **Withdrawn from the verdict.** Churn is off the table by instruction; §6 is sizing only |
| P4 — narrow benefit | **Survives, but is no longer decisive** — §12.10 lists benefits maximal mirroring genuinely adds |
| P5 — macos-user | **Half retracted, half strengthened into a hard blocker** (§12.8) |

Nothing in §12 rests on file counts.

### 12.3 It is mechanically expressible — MEASURED

I built the maximal shape and ran it (podman 5.8.4 + crun 1.27.1, 2026-09-04): a `:ro`
GLOBAL_HOME base at `/home/matt`, a **read-write workspace bind nested inside it** at
`/home/matt/code/proj`, and a rw overlay at `/home/matt/.config` whose host source is itself
inside the workspace. All three mounted on a `--read-only` rootfs; the workspace and the
overlay were writable, the home base stayed read-only, and crun created `/home/matt/code`
inside the `:ro` bind without complaint — so the EROFS caveat at
`internal/cli/run/assemble_parts.go:144-151` did not bite, consistent with
[`jail-home.md`](jail-home.md):238-246's finding that it resists reproduction.

**P1 is therefore retracted: the mechanics are not the objection.** Two structural
consequences are worth recording, because they are new and neither is fatal on its own:

- **The workspace moves inside the home.** Today they are siblings; the whole
  [`jail-home.md`](jail-home.md) overlay stack assumes a `:ro` home base with rw punches. A rw
  workspace inside that base is a fourth kind of punch, and the deepest.
- **The `<ws>/.yolo/home` alias moves inside `$HOME` too, and I measured the cycle.** A file
  written through `/home/matt/.config` appeared at
  `/home/matt/code/proj/.yolo/home/config` in the same container. That aliasing exists today
  (`/workspace/.yolo/home/claude` and `/home/agent/.claude` are one inode — AGENTS.md,
  Testing), but today it spans two top-level trees. Mirrored, `$HOME` contains a second copy
  of itself several levels down, so anything that walks `$HOME` — `du`, a backup, an agent
  running `rg` over its own home — traverses the home twice.

### 12.4 The new central objection: you can mirror a name, but not its content

This is the argument that survives all three changes, and it is the one I did not have.

**The host/jail boundary is not only a naming boundary.** It is an ABI boundary, a userland
boundary, and a credential boundary. Mirroring can make the *names* agree. It cannot make the
*contents* agree, because the two sides are deliberately built from different userlands —
that is what the jail is for.

- **MEASURED, this jail:** `/bin/rg` loads
  `/nix/store/qqiqd3ah10x8hzsif4j1y4xc1miw23nx-glibc-2.42-67/lib64/ld-linux-x86-64.so.2` and
  `/nix/store/…-pcre2-10.47/lib/libpcre2-8.so.0`. The jail's mise-installed
  `/mise/installs/python/3.11.14/bin/python3` links against the jail's own `/lib` farm. None
  of that resolves on the Arch host.
- **The repo already says so:** *"host (Arch) and jail (NixOS) are different userlands — a
  source-built C extension is only correct on the side that built it"*
  ([`jail-state-separation-design.md`](jail-state-separation-design.md):91). On macOS one side
  is Mach-O and the other ELF.

So under maximal mirroring a cross-boundary reference **resolves and returns the wrong
thing**, where today it fails immediately with ENOENT. That is a strictly worse failure mode,
and it is not hypothetical in this repo:

> [!WARNING]
> **There is shipped code whose entire job is to catch this class, and mirroring degrades it
> to always-pass.** `retireJailMadeVenv` (`internal/cli/run/retire.go:18-66`) deletes a
> workspace venv the jail made, so the host does not use a jail interpreter. Its test is
> `hasAnyPrefix(home, jailPrefixes) && !fileExists(home)` (`retire.go:60`), where
> `jailPrefixes` is `{"/workspace/", "/mise/", <host mise dir>}` (`:26-30`) — i.e. **it reads
> side-ness off the path string, then confirms with an existence check.** Under maximal
> mirroring both halves fail: the prefix is the same on both sides *by construction*, and the
> host has a same-named interpreter at that path, so `fileExists` is true and the guard
> `continue`s. The NixOS-built venv is silently kept and handed to the Arch host.

The same argument, in the same words, is why yolo **refused to share the uv cache**
([`jail-state-separation-design.md`](jail-state-separation-design.md):325-334): a source-built
wheel from the Arch host *"would be silently reused inside NixOS jails, and a jail-built one
(referencing `/nix/store/…`) on the host — the venv cross-OS hazard again, but as persistent,
invisible cache poisoning."* That decision was made about *content* sharing. Maximal mirroring
does not share content, but it removes the thing that makes the content mismatch **loud**.

**Stated as a principle:** in a system whose job is a boundary, a path string is the cheapest
label saying which side something belongs to. Maximal mirroring deletes that label from every
path at once, and puts nothing in its place.

Two objections I considered and rejected:

- *"The ABI hazard is pre-existing; mirroring only exposes it."* No — today it is not
  reachable, because the reference does not resolve. Loudness is a mitigation, and
  `retire.go` is that mitigation shipped. Removing a working mitigation and calling the
  hazard pre-existing is precisely the reasoning the uv-cache decision rejected.
- *"Then make the content agree too — one nix userland on both sides."* That is a real and
  coherent answer, and it is [§10 alternative G](#10-alternatives-each-with-a-verdict). But
  then path agreement is a *consequence* of userland unification, not a cause of anything.
  Mirroring paths first is the dangerous half done alone.

### 12.5 The credential boundary — the argument that could have killed it, and does

The coordinator flagged this as the likeliest real objection. It is, and it is concrete.

AGENTS.md's first sentence sells the product as running agents *"without exposing host
credentials or identity"* (`AGENTS.md:4`). Today that is easy to reason about for a reason
nobody wrote down: **`/home/agent` and `/home/matt` are different strings, so every path in
every config, doc, error message and agent instruction is self-labelling about which side it
names.** A reviewer does not have to know the mount table to see it.

The concrete case, and it is live rather than invented:

- Workspace `mounts` is **agent-editable and not scope-ruled**
  ([`trust-paths.md`](trust-paths.md):331 classifies it *"host read … implicit at the config
  diff; **never on a fresh clone** … yes — `git pull`, the agent's own edit"*). Entries are
  host paths, mounted `:ro` at `/ctx/<basename>` (`internal/cli/run/assemble.go:944`).
- Today, `"mounts": ["/home/matt/.ssh"]` appearing in a jail's workspace config is
  *unmistakable*: no jail path begins `/home/matt`, so it can only mean the host's real
  `~/.ssh`. The review gate is a human skimming a config diff, and this is exactly the kind of
  line that survives skimming or not depending on whether it looks out of place.
- Under maximal mirroring the same line is **ambiguous by construction**. `/home/matt/.ssh`
  is also the jail's own ssh dir (`ws/ssh` → `$HOME/.ssh`, `assemble_parts.go`; **MEASURED**:
  empty in this jail). It reads as plausibly local and resolves host-side to the real key.

Generalized: mirroring makes *"a path that names host credentials"* and *"a path that names
the jail's own empty equivalent"* the same string. Every reviewer, every doc, every agent, and
`internal/config/validate.go:333-335`'s existence check all lose the same signal at once.

> [!NOTE]
> **yolo has already found and fixed this exact bug shape at a different layer**, which is why
> I weight it heavily. [`yolo-as-environment-manager.md`](yolo-as-environment-manager.md):640-641
> diagnoses the autonomy bug as *"The keys that are safe because there is a jail travel,
> unlabelled, to the notch that has no jail. That is the bug."* The fix was to make the notch
> decide (`:643`, shipped as env-manager Phase 9). Maximal mirroring re-creates that shape in
> the one datum that carries no notch label at all — and unlike a config key, a path cannot be
> tagged with the notch it came from without becoming a different string, which is the thing
> mirroring exists to abolish.

What does *not* get worse, to be fair: the scope-ruled boundaries are untouched, because they
are enforced by which file is read, not by classifying strings (`hostwrappers.go:33-35`,
`hostapplyonlaunch.go:47-49`); and `host_files` destinations are home-relative, with
`hostFileWritableRoots` keyed on the first home segment (`internal/config/hostfiles.go:1069`),
so they are mirroring-agnostic.

### 12.6 Capture relocation — chased hard, it cuts the other way

This was expected to be mirroring's strongest concrete win: if all paths agree, does
[`../plans/install-capture.md`](../plans/install-capture.md)'s slice-6 relocation machinery
become unnecessary? **No. Chased to the bottom it inverts, twice.**

**First: relocation exists on `macos-user` only, and its prefix mismatch is one yolo creates
on purpose.** Capture runs the installer under `HOME=<CapturesDir>/staging/<id>` with
`SeatbeltCaptureProfile(stagingHome)`; materialize lands in the shared `/Users/_yolojail`
home. The two prefixes differ because a capture **must not** see or write the real home —
that is the isolation the capture design requires. No amount of host↔jail path mirroring
dissolves a staging-vs-real distinction *within one side*.

**Second, and this is the inversion: on the container backends relocation is unnecessary
today precisely because of the property mirroring removes.** A capture is taken in a scratch
workspace through the ordinary run pipeline, so `HOME` is `/home/agent`; materialize happens
in a real workspace's jail, where `HOME` is *also* `/home/agent`. Same string, different
backing — §2.2's collapsing device, doing the work. The capture surfaces are the home-relative
`dedupeSubtrees` (`npm-global`, `local`, `go`), and the absolute references the plan worries
about are things like the symlink `~/.local/bin/claude`. Under `/home/agent` those are
**machine-independent constants**; under mirroring they become `/home/<hostuser>/…` and are
machine-specific.

Two consequences follow, and both are costs:

- **A capture stops being structurally portable between machines.**
  [`program-delivery.md`](program-delivery.md):1203-1205 declines cross-machine distribution
  as *"a provenance question for trust-paths.md"* — a **trust** reason, not a path reason.
  Today the path half already works. Mirroring adds a second, harder blocker to a future the
  design deliberately left open.
- **Mirroring would make slice 6 necessary on the container backends too**, the day anyone
  wants that future — which is the opposite of deleting it.

**Answer to the deadline question:** slice 6 is **not** deletable under mirroring, so the
capture work stream is **not** blocked on this decision. I have recorded the dependency
anyway, inverted, at [`OQ-WP11`](#open-questions) — it is worth one line in the plan that the
hoped-for deletion is not available, so nobody sequences around it.

### 12.7 The notch model — no statement anywhere names paths as the obstacle

The second argument expected to cut for mirroring. It comes back **negative**, and the
negative is well-evidenced rather than merely unfound.

A notch is a *confinement level*, deliberately decoupled from mechanism
(`internal/config/confinement.go:3-6`), and it is a preset over a six-primitive vector
(`internal/render/confinement.go:19-43`) — **not a path layout**. What the env-manager corpus
claims is portable is the **declaration**, re-rendered per notch by one renderer:
*"what is portable is the render"* ([`host-render-target.md`](host-render-target.md):102), and
*"the batteries are declarative, locked, and portable across confinement levels"*
([`yolo-as-environment-manager.md`](yolo-as-environment-manager.md):694-695). No built
artifact is designed to move between notches, and I could find no doc that wishes one would.

The reasons a thing *cannot* cross to the host notch are enumerated, and **none of them is a
path**. [`host-render-target.md`](host-render-target.md):587 heads the list — *"things whose
**definition** is jail-shaped"* — and `internal/render/fieldset.go:36-60` is the same census
in executable form: `mount` needs a mount namespace; `reads-host` *"carries a host file INTO a
jail — meaningless when there is no jail"* (`:40`); `install` must not mutate a real toolchain;
a loophole has no client without a container. Nine manifest fields, four meaningless without a
container, **exactly one target-independent** (`host-render-target.md`:98-100).

Two specifics worth recording because they look like path problems and are not:

- **`${workspace}` is refused at the host notch because the host notch has no workspace at
  all** — it is user-scoped by ruling: *"What `yolo host apply` asserts is a function of your
  **user** config + the packs **you** installed, never of the repo you ran it from"*
  (`docs/plans/environment-manager-plan.md:689-694`, OQ-2, resolved 2026-08-01). Mirroring
  cannot supply a referent that the design says must not exist.
- **The computed layer is the one place jail-absolute paths do block a cross-notch move** —
  `KindHost` is *"no computed layer (its values embed jail-absolute paths)"*
  (`internal/render/target.go:97-99`; `internal/entrypoint/hostrender.go:23-25`). It is dropped
  **by declaration** — `render.Host()` passes an empty table — not by a path check. And this is
  where §12.4 bites hardest: mirroring would make those values *resolve* on the host, turning a
  refusal that is correct by construction into a write that merely looks plausible. An MCP
  command at `/home/matt/.npm-global/bin/foo` would name the user's real npm prefix, which may
  hold nothing, an older version, or something else entirely.

And the practical point: `guest` — the notch that would most benefit from portability — **is
not built**. `internal/cli/apply.go:106-109` prints *"apply at the guest notch is not built yet
(env-manager plan Phase 7)"*; `internal/render/target.go:90-95` says it has *"NO constructor
yet"*. The three-notch story's broken middle is blocked on Phase 7, not on paths.

### 12.8 macos-user's neutral ground: is it a constraint or a choice?

Both, and the two halves point opposite ways. This is where P5 splits.

**The single shared home is a choice, and a weakly-founded one — I withdraw that half of P5.**
It arrived as SandVault parity with no argument recorded, and it contradicts a stated
must-keep in [`macos-no-vm-direction.md`](macos-no-vm-direction.md):127-128 (*"Per-workspace
isolation … not one shared home"*). `SandboxHome()` is a one-line constant. The
*"load-bearing"* framing in [`backend-parity.md`](backend-parity.md):295-298 is a later
justification on credential grounds — one login per machine — and it carries a documented
isolation leak ([`macos-user-nix-and-features.md`](macos-user-nix-and-features.md):341-346:
*"The denial and the leak are the same content reached two ways"*). A maximal proposal may
legitimately reopen it.

**Neutral ground is a security decision about grant *routing*, it does not depend on paths
differing, and under maximal mirroring it becomes a hard blocker.** The origin commit
`29b00697` (*"feat(macos-user): share only neutral ground, never the host home"*, 2026-07-13)
states the reason:

> Previously a workspace could live in place inside the host home and we threaded traversal
> ACLs + Seatbelt file-read-metadata grants through each home ancestor (`/Users/<you>`, …) so
> a different uid could descend to it — layered access control routed through the most
> sensitive dir on the machine, exactly where a stray grant silently exposes `~/.ssh`.

The same commit forecloses the "it's an artifact of paths differing" reading by name: macOS
has no clean overlay or bind, so *"dropping 'appear in two places' is what BUYS the clarity,
not a limitation"*. There is no path mismatch on that backend to remove — the sandbox path
already **is** the host path. Making paths match deliberately would only make the illegal path
legal.

**Then the maintainer's own words — "the user could have the same name as the host user" —
turn out to be inexpressible on macos-user, for three independent reasons:**

1. **Directory-service uniqueness.** The jail user is a real local account created with
   `dscl . -create /Users/_yolojail` (`internal/macosuser/macosuser.go:77`). `dscl -create` on
   the host user's shortname mutates the human's account; it does not make a second one.
2. **The sharing mechanism needs two distinct principals.** The whole model is "host user and
   sandbox user are two members of group `_yolojail`" (`macosuser.go:87-89`) plus an inheriting
   group ACE (`macosuser.go:262`). One identity leaves no cross-uid grant to express — and the tests
   already pin the sharper corollary that an endpoint grant must be a `user:` ACE, *"never a
   group one: SandboxGroup contains the HOST user"* (`macosuser_test.go:397-398`).
3. **Seatbelt would collapse outright.** The profile allows `(subpath <sandboxHome>)` for read
   *and* write (`internal/macosuser/seatbelt.go:48-50` write, `:76-80` read). A sandbox home at
   `/Users/matt` therefore grants the agent full read-write over the human's home — the exact
   outcome `29b00697` exists to prevent, arrived at from the other direction.

Note the shape of that third one: it is §12.4 again, in its purest and most mechanical form.
The grant is written against a *path*; mirroring changes what that path denotes on one side
and not the other; the grant stays syntactically identical and becomes catastrophic.

The narrowed invariant that actually survives in the code is worth quoting, because it shows
the rule is about homes specifically and not about neutrality in general: ancestor grants were
re-introduced after a measured failure (`2e327fa2`, *git cannot find the repo*), but
`internal/macosuser/seatbelt.go:155` hard-codes `const base = "/Users/Shared/"` because
*"a path elsewhere under /Users (a real user's home) must NOT gain traversal grants from
this"* (`:152`).

### 12.9 Optional first: transition or permanent?

The maintainer added: *"I think if we go with the mirror paths, we may need to make it
optional at first, this is going to be a big change."* Since my recommendation is **no**, this
section is conditional — it is the ruling that *would* apply, not an argument that the answer
is yes.

**Ruling: transition-optional only, never permanently-optional — and a transition with no
stated removal condition is a permanent option in a costume.** The repo has both patterns and
they are distinguishable: the five `YOLO_ALLOW_*` variables are permanent narrow exceptions to
a rule that still holds in every other case, whereas `internal/hostmigrate` and the retired
`YOLO_IMPL=go` gate are transitions with an end. A path layout is not an exception to a rule;
it is **the key every downstream artifact is filed under**, so two live layouts means every
capture, build cache, venv and compiled output must record which layout it was made under and
refuse or rewrite on mismatch — reinstating exactly the relocation machinery §12.6 shows
mirroring already fails to delete.

One sharpening the framing deserves, and it cuts against mirroring rather than for it:
**mandatory mirroring is already multi-layout.** The mirrored path embeds the host username, so
machine A and machine B have different layouts even with the flag mandatory everywhere.
"One layout" is not something mirroring can deliver; it is something `/home/agent` and `/mise`
deliver *today*, and it is what §12.6's portability finding rests on. Optionality adds a second
axis to a problem mirroring introduces on the first.

If it were pursued anyway, a transition must state all five of these up front, because each is
a behaviour someone will otherwise choose silently:

1. **The flag and its scope.** A user-scope config key, never workspace-scope — a workspace
   config is agent-editable and this key changes where every path points, which is precisely
   the `packs`-class reasoning in [`trust-paths.md`](trust-paths.md):250-256.
2. **Defaults per stage.** Stage 1 off by default, opt-in per machine. Stage 2 on by default
   with an opt-out. Stage 3 removed. Say the dates or the conditions, not "eventually".
3. **What happens to existing state.** `<ws>/.yolo` and the home overlay are keyed host-side by
   the workspace directory (`internal/cli/run/prepare.go:302`) and do **not** move — see §8.
   What breaks is content that *recorded* the old layout: `~/.claude.json`'s `projects` key and
   `~/.claude/projects/-workspace`, `~/.cache/claude-cli-nodejs/-workspace`, venv `pyvenv.cfg`
   interpreters, and any capture admitted under the old prefix.
4. **Automatic migration or re-create.** My reading: no automatic migration is possible for the
   vendor-owned keys (yolo does not own claude's on-disk layout), so the honest answer is a
   storage-layout-version bump (`internal/storage/ensure.go:22`, currently 2) plus an announced
   one-time loss of path-keyed agent history.
5. **The removal condition, stated as a test.** Something checkable — e.g. "no workspace on the
   maintainer's machines has booted under the old layout for 30 days, and `yolo prune` reports
   no captures admitted under it". Without a condition of that shape, stage 3 never arrives.

### 12.10 What maximal mirroring genuinely buys

Stated plainly, because the verdict should be judged against the real upside rather than a
strawman. Beyond §3's three confirmed items, which all still hold:

- **Venv and `node_modules` path agreement becomes possible** — the thing §5 said mirroring
  could not do. It is real, and it is bounded by §12.4: path agreement is necessary but not
  sufficient, and the insufficient half (userland/ABI) is the binding constraint for anything
  with a compiled component. It buys pure-Python and pure-JS trees on a Linux host, and
  nothing on macOS.
- **Toolchain-store agreement becomes possible.** Same caveat, harder: the store was split in
  2026-07 for *version skew* and *binary compatibility*, not for paths, so mirroring its path
  either re-merges two stores that were separated on purpose, or keeps them separate at one
  name — which is §12.4's silent-failure case in its most load-bearing location.
- **Nested-jail path stability** (§4.3), unchanged in strength.
- **The computed layer could in principle cross to the host notch** (§12.7) — though the
  design refuses it for a reason that is about correctness, not spelling.

That is a real list. It is not nothing, and if §12.4 and §12.5 did not exist I would call this
a close call rather than a clear no.

### 12.11 Verdict on maximal mirroring

**No — and now for a reason that does not depend on churn, on the workspace being the only
space that moves, or on any revisitable backend decision.**

Maximal mirroring makes every path *name* agree while leaving every path's *content*
determined by which side you are on. In a system whose entire premise is a boundary, the path
string is the last free signal of which side a thing came from — used by reviewers, by docs, by
agents, by Seatbelt grants written against literals, and by at least one shipped safety net
(`retire.go`) that would degrade to always-pass. Mirroring deletes that signal everywhere at
once and puts nothing in its place, converting a class of loud ENOENT failures into silent
wrong-artifact ones. yolo already rejected content sharing on exactly this reasoning
(the uv cache), already found and fixed this bug *shape* at the config layer (autonomy as a
notch policy), and on `macos-user` the mirrored home is not merely unwise but mechanically
inexpressible.

The two arguments that were expected to carry it do not: capture relocation is *created* by
mirroring rather than deleted by it, and nothing in the notch corpus names paths as the
obstacle to anything.

**If the goal is one environment at several confinement levels — which is a good goal — the
lever is making the *content* agree, not the names.** That is [alternative G](#10-alternatives-each-with-a-verdict),
and it already has a home in the tree.

## Open Questions

1. 💬 **OQ-WP1: Accept the verdict, or is there a fourth problem I did not find?**
   The recommendation rests on the sweep in §3 being close to complete — three confirmed
   cases, seven candidates disproved. A single additional confirmed case with a real payload,
   especially in the machine-shared cache, changes the arithmetic materially. **Scoped to the
   narrow question by the 2026-09-04 reopening** — the closure question for the doc as a whole
   is now `OQ-WP8`.

   _Leaning:_ Accept. But I am asking rather than asserting, because a maintainer's lived
   annoyance is evidence a repo sweep cannot produce, and "this bites me weekly" would
   outweigh my census.

   <!-- vantage: oq id=OQ-WP1 leaning="Accept the verdict. The sweep found three confirmed problems and disproved seven candidates, but a maintainer's lived annoyance is evidence a repo sweep cannot produce — a fourth confirmed case with a real payload would change the arithmetic." -->

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-WP2: Add `-trimpath` to `scripts/build-go.sh` regardless?**
   Independent of the verdict. It closes §3.1 (394 dead source paths in the shipped `yolo`
   binary), matches what `flake.nix:149` already does for the image build, and costs one
   line. The only thing it trades away is the ability to jump to source from a `dist-go`
   binary *inside* the jail, where the paths do resolve.

   _Leaning:_ Yes, do it, as its own commit with its own test. It is the one piece of the
   proposal's value that is available for nearly nothing.

   <!-- vantage: oq id=OQ-WP2 leaning="Yes, add `-trimpath` to `scripts/build-go.sh`, as its own commit with its own test. It closes §3.1 (394 dead `/workspace/` source paths in the shipped `yolo` binary) for one line, and it is the one piece of the proposal's value available for nearly nothing." -->

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 **OQ-WP3: Leave the `~/.cache/claude-cli-nodejs/-workspace` collision alone?**
   Every jail on the machine writes into that one key (§3.2). Today it holds MCP logs.
   Fixing it means binding a per-workspace directory over one vendor-specific path — a
   mount added for one tool's cache layout, which is the shape yolo usually refuses.

   _Leaning:_ Leave it, and note it in [`jail-home.md`](jail-home.md) §2.1 so the next person
   who finds interleaved MCP logs does not spend an afternoon on it. Revisit if a
   second consumer appears (§1.1).

   <!-- vantage: oq id=OQ-WP3 leaning="Leave the `~/.cache/claude-cli-nodejs/-workspace` collision alone and note it in `jail-home.md` §2.1. It holds MCP logs today, and fixing it means a mount added for one vendor's cache layout." -->

   **Answer:**
   > _(empty — fill in when decided)_

4. 💬 **OQ-WP4: Should workspace-scope `mounts` get a scope rule, separately from this?**
   §4.4 found that a `mounts` entry naming a workspace path is inert today only because the
   path does not resolve host-side — an undocumented fail-safe. That is a question about
   [`trust-paths.md`](trust-paths.md)'s scope model, which currently lists workspace `mounts`
   as un-scope-ruled, and it stands whether or not mirroring ever happens.

   _Leaning:_ Raise it in [`trust-paths.md`](trust-paths.md), not here. The measured blast
   radius is small (a workspace subdir gets a second read-only appearance under `/ctx`; the
   `/ctx/packs` shadow is refused by podman), so it is a model tidy-up rather than a fix.

   <!-- vantage: oq id=OQ-WP4 leaning="Raise workspace-scope `mounts` in `trust-paths.md`, not here. The measured blast radius is small — the `/ctx/packs` shadow is refused by podman outright — so it is a scope-model tidy-up rather than a fix." -->

   **Answer:**
   > _(empty — fill in when decided)_

5. 💬 **OQ-WP5: Does Apple Container support an arbitrary deep bind destination?**
   Unanswered since `OQ-MP2` closed it as moot in July. It does not block the *no*, but it
   would block a future *yes*, and it is cheap to measure for whoever next has AC hardware
   in front of them. `docs/design/backend-parity.md` is where the answer belongs.

   _Leaning:_ Unverified, and I would guess yes (AC does ordinary directory binds), but a
   guess is exactly what this repo's doc norms forbid recording as fact.

   <!-- vantage: oq id=OQ-WP5 leaning="Unverified. I would guess Apple Container does support an arbitrary deep bind destination, but a guess is exactly what this repo's doc norms forbid recording as fact. It blocks a future yes, not this no." -->

   **Answer:**
   > _(empty — fill in when decided)_

6. 💬 **OQ-WP6: Is the interesting proposal actually "mirror all three path spaces"?**
   P1 says mirroring the workspace alone cannot make the two sides agree, because home and
   the toolchain store do not move. The coherent version of the idea moves all three — and
   collides head-on with two shipped designs ([`jail-home.md`](jail-home.md),
   [`jail-state-separation-design.md`](jail-state-separation-design.md)) that exist because
   *not* sharing those was worth paying for. Worth a paragraph of intent before anyone
   invests in it.

   **Superseded in substance by [§12](#12-follow-up-maximal-mirroring), 2026-09-04** — the
   question was asked for real and is now analysed rather than deferred. Kept open because the
   ruling is the maintainer's, and folded into `OQ-WP8`.

   _Leaning (revised 2026-09-04):_ Still no, but my original reason was too glib. It is not
   that the separate home is sacred — §12.8 shows the *shared-home* half of macos-user's shape
   is weakly founded and reopenable. It is that mirroring makes names agree while contents
   stay side-determined (§12.4), which is a worse failure mode than the one it removes, and
   that on macos-user the mirrored home is mechanically inexpressible (§12.8).

   <!-- vantage: oq id=OQ-WP6 leaning="Still no, but the original reason was too glib. Not because the separate home is sacred — section 12.8 shows macos-user's shared-home half is weakly founded and reopenable. Because mirroring makes names agree while contents stay side-determined, which is a worse failure mode than the one it removes, and because on macos-user the mirrored home is mechanically inexpressible. Folded into OQ-WP8." -->

   **Answer:**
   > _(empty — fill in when decided)_

7. 💬 🤷 **OQ-WP7: Should this doc survive a `no`?**
   A rejected proposal's doc earns its place only if the next person would otherwise re-ask
   it. This one was re-asked once already (July → September), so my instinct is yes — but
   it is the maintainer's call how much of the planning tree is archaeology.

   _Leaning:_ Keep it, with the status re-stamped to `REJECTED (2026-09-04)`, and
   cross-link it from
   [`../research/mise-host-jail-path-mismatch.md`](../research/mise-host-jail-path-mismatch.md)'s
   `OQ-MP1` so the two answers are found together.

   <!-- vantage: oq id=OQ-WP7 leaning="Keep this doc, re-stamped `REJECTED (2026-09-04)`, and cross-link it from `mise-host-jail-path-mismatch.md`'s OQ-MP1 so the two answers are found together. The question was already re-asked once." -->

   **Answer:**
   > _(empty — fill in when decided)_

8. 💬 **OQ-WP8: Accept the verdict on MAXIMAL mirroring, and its reason?** This is the closure
   question for the doc. [§12](#12-follow-up-maximal-mirroring) says no on grounds that do not
   use churn, do not assume only the workspace moves, and do not lean on a revisitable backend
   decision: **you can mirror a path's name but not its content** (§12.4), the credential
   boundary loses its cheapest signal (§12.5), and `macos-user` cannot express the mirrored
   home at all (§12.8). Accepting this closes `OQ-WP1` and `OQ-WP6` with it.

   _Leaning:_ Accept. The reason I most want argued with is §12.4 — if the maintainer holds
   that the ABI hazard is acceptable because a mirrored reference is *usually* right, that is
   a coherent position and it would flip me, but it should be taken deliberately rather than
   by omission.

   <!-- vantage: oq id=OQ-WP8 leaning="Accept the no. Maximal mirroring makes path NAMES agree while CONTENTS stay side-determined (Arch host / NixOS jail, Mach-O / ELF), converting loud ENOENT failures into silent wrong-artifact ones; it deletes the credential boundary's cheapest signal; and macos-user cannot express the mirrored home. The point most worth arguing with is whether the ABI hazard is acceptable because a mirrored reference is usually right." -->

   **Answer:**
   > _(empty — fill in when decided)_

9. 💬 **OQ-WP9: Harden `retireJailMadeVenv` regardless of the verdict?**
   `internal/cli/run/retire.go:60` decides whether to delete a workspace venv by testing a
   **path prefix** plus an existence check. §12.4 uses its degradation under mirroring as
   evidence, but the guard is already thin: it misses any jail-made venv whose recorded
   interpreter happens to exist on the host at the same path.

   _Leaning:_ Yes, but as separate, small work, and only if a better oracle exists. Reading
   the ELF interpreter out of the recorded `home` binary would be one; I have not checked
   whether that is cheap enough to run on every fresh-container launch, so this is a question
   rather than a proposal.

   <!-- vantage: oq id=OQ-WP9 leaning="Yes, separately and only with a better oracle. The path-prefix plus existence test at retire.go:60 is already thin — it misses a jail-made venv whose interpreter exists at the same path on the host. Reading the ELF interpreter would be a content-based oracle, but I have not measured whether it is cheap enough for every fresh-container launch." -->

   **Answer:**
   > _(empty — fill in when decided)_

10. 💬 **OQ-WP10: Is the real goal alternative G — one userland at several notches?** §12.7
    found that nothing in the env-manager corpus names paths as the obstacle to notch
    portability, and §12.4 found that content, not naming, is what blocks an artifact from
    crossing. If the underlying want is "the same environment at different confinement
    levels", the lever is `yoloNoncontainerPackages` and
    [`noncontainer-nix-environment.md`](noncontainer-nix-environment.md), not the mount table.
    Worth knowing whether that is the want, because a no to mirroring should not read as a no
    to it.

    _Leaning:_ I suspect yes, and that mirroring was a plausible-looking route to it. But I am
    genuinely unsure whether the goal is portability or simply "paths that make sense to a
    human", which is a different and smaller want that §3.3 addresses.

    <!-- vantage: oq id=OQ-WP10 leaning="I suspect the underlying goal is cross-notch portability and mirroring looked like a route to it, in which case the lever is userland unification (yoloNoncontainerPackages, noncontainer-nix-environment.md) rather than the mount table. But the want might instead be the smaller one of human-legible paths, which is section 3.3. Worth asking which." -->

    **Answer:**
    > _(empty — fill in when decided)_

11. 💬 **OQ-WP11: Record the inverted capture finding in `install-capture.md`?** The hoped-for
    result was that mirroring makes slice 6 (relocation) deletable. §12.6 finds the opposite:
    relocation is a `macos-user`-only need created by capture's own staging isolation, and on
    the container backends it is unnecessary *because* `/home/agent` is a machine-independent
    constant that mirroring would remove. So slice 6 is not blocked on this decision and the
    two work streams do not collide.

    _Leaning:_ Yes — one line in [`../plans/install-capture.md`](../plans/install-capture.md)'s
    build-order step 6 saying the deletion is not available, so nobody sequences around a
    saving that is not coming. That is an edit to a file another agent is actively working in,
    so it belongs in their commit, not this one.

    <!-- vantage: oq id=OQ-WP11 leaning="Yes — one line in install-capture.md step 6 recording that mirroring does NOT make relocation deletable, so nobody sequences around a saving that is not coming. It belongs in the capture agent's commit, not this doc's, since they are actively editing that file." -->

    **Answer:**
    > _(empty — fill in when decided)_

12. 💬 🤷 **OQ-WP12: Reopen `macos-user`'s single shared home?** A side finding of §12.8,
    unrelated to mirroring's verdict. The single `/Users/_yolojail` home was inherited from
    SandVault with no argument recorded, contradicts a stated must-keep in
    [`macos-no-vm-direction.md`](macos-no-vm-direction.md):127-128 (*"Per-workspace isolation
    … not one shared home"*), and carries a documented cross-workspace transcript leak
    ([`macos-user-nix-and-features.md`](macos-user-nix-and-features.md):341-346). Its
    *"load-bearing"* framing ([`backend-parity.md`](backend-parity.md):295-298) is a later
    credential-tier justification, not the reason it exists.

    _Leaning:_ The maintainer's call — a tier tradeoff (one login per machine vs. per-workspace
    isolation), not a technical question. I note only that the bar `backend-parity.md` sets is
    the right one: restore **both** tiers explicitly, never just split the home.

    <!-- vantage: oq id=OQ-WP12 leaning="Maintainer's call — a tier tradeoff, not a technical question. Worth knowing the single shared home was inherited from SandVault with no argument recorded, contradicts a stated must-keep, and carries a cross-workspace transcript leak; its load-bearing framing is a later justification. If reopened, the bar is to restore both tiers explicitly, not just split the home." -->

    **Answer:**
    > _(empty — fill in when decided)_

---

## Decision Ledger

No decisions are settled yet — every question above is live. The one *inherited* ruling this
doc is built on top of, recorded so it is not silently re-litigated:

| ID | Ruling / Decision | Date | Settled in |
| :--- | :--- | :--- | :--- |
| OQ-MP1 | Same-path workspace mount ("option A") rejected; superseded by the state-separation bundle | 2026-07-03 | [`../research/mise-host-jail-path-mismatch.md`](../research/mise-host-jail-path-mismatch.md) · this doc §2.3 re-examines it, because two of its three reasons have expired |
| OQ-2 (env-manager) | Host management is user-scoped; the workspace contributes nothing, so `${workspace}` surfaces are refused at the host notch | 2026-08-01 | `docs/plans/environment-manager-plan.md:689-694` · §12.7 relies on it: mirroring cannot supply a referent the design says must not exist |
| — (`29b00697`) | `macos-user` shares only neutral ground, never the host home — because a foreign uid reaching a leaf inside `~` needs traversal on `/Users/<you>`, *"exactly where a stray grant silently exposes `~/.ssh`"* | 2026-07-13 | §12.8 · the reason is grant **routing**, independent of whether paths match |

**Withdrawn by the 2026-09-04 reopening**, recorded so they are not re-cited: **P1** (only the
workspace would match) and **P3** (the prose tax) are retracted at their original sites, and
the **churn census in §6 is sizing, not an argument**.
