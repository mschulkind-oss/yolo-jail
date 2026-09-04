---
title: "Should the jail mount the workspace at the host's own path?"
date: 2026-09-04
status: draft
tags: [mounts, paths, backends, macos-user, state-separation, trust]
summary: "Mirroring the host workspace path into the jail is cheaper to build than it looks, because ${workspace} and Env.WorkspaceDir() already exist for macos-user. It still does not deliver the property it is sold on — HOME and the toolchain store stay at /home/agent and /mise — it re-opens a shared-store bug class the 2026-07 state-separation bundle closed on purpose, and macos-user refuses the very path it would produce on a Mac. Recommendation: no."
---

# Should the jail mount the workspace at the host's own path?

**Status:** DESIGN SKETCH, 2026-09-04. Nothing built, and my recommendation is that nothing
should be. This doc exists to make the *no* checkable rather than reflexive — the question
was asked once before, answered no for reasons that have since expired, and deserves a
fresh answer rather than a citation.

**The short version.** Mirroring means dropping `/workspace` and bind-mounting the host
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

**E. Mirror all three path spaces** — workspace *and* home *and* toolchain store. —
**Not evaluated here; genuinely different proposal.** P1 would not apply to it, which makes
it more coherent than A, and it would collide head-on with [`jail-home.md`](jail-home.md) and
[`jail-state-separation-design.md`](jail-state-separation-design.md), which makes it much
larger. Raised as `OQ-WP6` rather than dismissed.

**F. Do nothing.** — **Rejected**, narrowly: §3.1 is a real defect with a one-line fix and
should not ride on this verdict.

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

---

## Open Questions

1. 💬 **OQ-WP1: Accept the verdict, or is there a fourth problem I did not find?**
   The recommendation rests on the sweep in §3 being close to complete — three confirmed
   cases, seven candidates disproved. A single additional confirmed case with a real payload,
   especially in the machine-shared cache, changes the arithmetic materially. This is the
   closure question for the doc.

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

   _Leaning:_ No, and for a reason worth stating: the jail's separate home is not a cost the
   design tolerates, it is a feature it sells — credential isolation. Mirroring it would
   trade the product's premise for path convenience.

   <!-- vantage: oq id=OQ-WP6 leaning="No. The jail's separate home is not a cost the design tolerates, it is a feature it sells — credential isolation. Mirroring all three path spaces would trade the product's premise for path convenience." -->

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

---

## Decision Ledger

No decisions are settled yet — every question above is live. The one *inherited* ruling this
doc is built on top of, recorded so it is not silently re-litigated:

| ID | Ruling / Decision | Date | Settled in |
| :--- | :--- | :--- | :--- |
| OQ-MP1 | Same-path workspace mount ("option A") rejected; superseded by the state-separation bundle | 2026-07-03 | [`../research/mise-host-jail-path-mismatch.md`](../research/mise-host-jail-path-mismatch.md) · this doc §2.3 re-examines it, because two of its three reasons have expired |
