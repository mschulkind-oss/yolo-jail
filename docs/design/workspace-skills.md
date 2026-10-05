---
title: "Workspace skills — graduated; the host half is built"
date: 2026-09-17
status: accepted
stage: GRADUATED
next: "The maintainer confirms or revises OQ-WS6, decided on its leaning (a) on 2026-10-04 under the delegation; the host half's reference home (agent-briefings.md's workspace layer) takes WS-D19 to WS-D23's settled body. Delete this file once the WS-D ids are cited from the reference alone"
tags: [design, skills, packs, workspace, notch, git, trust, graduated]
summary: "A stub. v1 of workspace skills graduated on 2026-10-01 into docs/reference/agent-briefings.md's workspace layer: a repo's committed skills reach every agent through the staged mirror in containers and on macos-user, at the lowest layer, with escaping symlinks refused, the principles P1 to P5 and OQ-WS1 to OQ-WS5 and OQ-WS7 in that reference. What stays here: the host half, out of v1 by OQ-WS5 and built on 2026-10-04 under the maintainer's delegation as one link per `yolo host -- <agent>` (WS-D19 to WS-D23), with OQ-WS6 decided on its leaning (a) and open to revision; the measured table of where each agent reads skills, which the project_dirs probe cites; and the build record WS-D1 to WS-D23."
vantage:
  status-chip: true
---

# Workspace skills — graduated; the host half is built

**Status:** 2026-10-01 — v1 is built (`7df13e51`, with the cap of 2026-09-28), and its settled body
is now [`agent-briefings.md`'s workspace layer](../reference/agent-briefings.md#the-workspace-layer),
which is the authority, with the layer's principles [`P1`](../reference/agent-briefings.md#ws-p1)
to [`P5`](../reference/agent-briefings.md#ws-p5) and [`OQ-WS1`](../reference/agent-briefings.md#oq-ws1)
to [`OQ-WS5`](../reference/agent-briefings.md#oq-ws5) and [`OQ-WS7`](../reference/agent-briefings.md#oq-ws7)
in its why-appendix. UNMEASURED: no observed launch is recorded;
`TestWorkspaceSkillsReachAContainerJail` and, on a Mac,
`TestMacosUserWorkspaceSkillsArriveThroughTheComposedTree` (`integration/workspaceskills_test.go`)
are the instruments that would show it. 2026-10-04 — the host half is built behind no switch, as
one link per `yolo host -- <agent>` ([WS-D19](#WS-D19) to [WS-D23](#WS-D23)); unit tests pin its
call site, and no installed agent has been seen loading skills through the link.

**What stays here:**

- **The host half, built after v1.** [OQ-WS5](#OQ-WS5) put the host notch out of v1: a staged
  mirror into a real home is ruled out there, so the host half is links written into the repo under
  `yolo host -- <agent>` or nothing. It was taken up on 2026-10-04 under the maintainer's
  delegation (*"make them and build it … adjust later"*) and built as one link per launch,
  [WS-D19](#WS-D19) to [WS-D23](#WS-D23). [OQ-WS6](#OQ-WS6), how the link stays out of git, was
  decided on its leaning (a) for that build and is open to the maintainer's revision.
- **[Where each agent reads skills](#21-where-each-agent-reads-skills--measured)**, the measured
  table the shipped `project_dirs` declarations and their probe rest on.
- **The build record**, the [ledger](#12-decision-ledger)'s `WS-D1` to `WS-D23`, which code
  comments cite.

The design's argument (the gap, the four candidate mechanisms, both notches, the risks and the
build order) is in git history (`git log --follow -- docs/design/workspace-skills.md`).

**Needs your confirmation:** [OQ-WS6](#OQ-WS6)'s answer, decided on its own leaning (a) for the
host half's build; the host half itself ([WS-D19](#WS-D19) to [WS-D23](#WS-D23)) is a set of
reversible implementation decisions under the same delegation.

| Was | Now |
| :--- | :--- |
| <a id="1-verdict-and-the-principles-it-rests-on"></a>Section 1, the verdict and principles P1 to P5 | [the layer's principles](../reference/agent-briefings.md#ws-p1) |
| <a id="2-what-exists-today--measured"></a>Section 2: 2.1 is [below](#21-where-each-agent-reads-skills--measured); <a id="22-what-yolo-composes-today"></a>2.2 the composition, <a id="23-the-writability-asymmetry--measured"></a>2.3 the writability asymmetry, <a id="24-what-yolo-writes-into-a-workspace-today"></a>2.4 what yolo wrote into a workspace, <a id="25-the-packs-boundary-and-the-ruling-behind-it"></a>2.5 the `packs` boundary | [the skills composition](../reference/agent-briefings.md#skills) and [P3](../reference/agent-briefings.md#ws-p3); the rest is git history |
| <a id="3-the-gap-stated"></a>Section 3, the gap; <a id="4-the-candidate-mechanisms"></a>section 4, <a id="41-mechanism-a--the-staged-mirror"></a>the staged mirror, <a id="42-mechanism-b--in-workspace-links"></a>the links, <a id="43-baseline-c--conventions-only"></a>the conventions, <a id="44-baseline-d--the-reader-points-a-local-pack-at-the-checkout"></a>a local pack, <a id="45-side-by-side"></a>side by side | [the workspace layer](../reference/agent-briefings.md#the-workspace-layer), and [`OQ-WS4`](../reference/agent-briefings.md#oq-ws4) |
| <a id="5-the-pack-declares-what-its-agent-reads"></a>Section 5, the pack declares what its agent reads | [the workspace layer](../reference/agent-briefings.md#the-workspace-layer) and [the `skills` kind](../reference/pack-system.md#skills) (`project_dirs`) |
| <a id="6-both-notches-honestly"></a>Section 6, both notches | [`OQ-WS5`](../reference/agent-briefings.md#oq-ws5), and the host half [below](#OQ-WS5) |
| <a id="7-what-this-does-not-propose"></a>Section 7, what this does not propose; <a id="8-risks"></a>section 8, risks; <a id="9-what-i-would-build-in-order"></a>section 9, the build order; <a id="10-what-done-looks-like"></a>section 10, what done looks like | [the workspace layer](../reference/agent-briefings.md#the-workspace-layer) |
| <a id="11-open-questions"></a>Section 11, the questions: <a id="OQ-WS1"></a>the workspace may contribute | [`OQ-WS1`](../reference/agent-briefings.md#oq-ws1) |
| <a id="OQ-WS2"></a>The lowest layer | [`OQ-WS2`](../reference/agent-briefings.md#oq-ws2) |
| <a id="OQ-WS3"></a>The source set | [`OQ-WS3`](../reference/agent-briefings.md#oq-ws3) |
| <a id="OQ-WS4"></a>The mirror alone | [`OQ-WS4`](../reference/agent-briefings.md#oq-ws4) |
| <a id="OQ-WS7"></a>The copy's cap | [`OQ-WS7`](../reference/agent-briefings.md#oq-ws7) |

## The host half

5. ✅ <a id="OQ-WS5"></a>**OQ-WS5: Is the host notch in scope for v1 — and by B only?** Decides whether
   `yolo host -- <agent>` writes links into the cwd repo. The mirror is closed on the host by
   two standing rulings this doc does not reopen ([§6](#6-both-notches-honestly)); the question
   is only whether B is worth shipping there now, given that a host user can commit the same
   links by hand and that the agent's own trust gate still governs the read. A *yes* makes
   [OQ-WS6](#OQ-WS6) load-bearing; a *no* parks it.


   _Leaning:_ **v2.** The container answer stands alone; the host half is a single exec-time
   step that can land later without reshaping anything, and it should not delay A.

   <!-- vantage: question id=OQ-WS5 -->

   **Answer:**
   > **As leaned, not in v1**, ruled 2026-09-27 in review: *"Out of scope for v1 — ship A in containers first; the host half is B or nothing, is one exec-time step, and can follow once [`OQ-WS6`](#OQ-WS6) is ruled."*
   >
   > **Taken up after v1, 2026-10-04**, under the maintainer's delegation to build the host gaps
   > that were deferred rather than ruled out (*"make a pass for all features that don't yet work
   > on the host, and implement the ones that are possible that we've just delayed"*): B, as the
   > ruling named it, one exec-time step ([WS-D19](#WS-D19) to [WS-D23](#WS-D23)).

6. ✅ <a id="OQ-WS6"></a>**OQ-WS6: For the links, root `.gitignore`, `.git/info/exclude`, or a yolo-owned
   parent's `.gitignore`?** Decides what `git status` shows after a launch under B, and which
   file yolo becomes a writer of.

   - **(a) root `.gitignore` append**, write-once by content check
     — `yolo init`'s precedent; visible as `M .gitignore` once, then quiet for every clone once
     committed; but it is a launch editing a tracked file.
   - **(b) `.git/info/exclude` append** —
     invisible, per clone, never committed; but it is the blind cell, the file
     [`../reference/host-execution-from-the-workspace.md`](../reference/host-execution-from-the-workspace.md#the-two-axes)
     calls load-bearing precisely because writing it moves things from visible to invisible; it
     is shared across worktrees; and `workspace_readonly` may lock it.
   - **(c) a `.gitignore` inside
     each parent directory yolo itself created** — the `.yolo/` trick; clean when yolo made
     `.codex/`, impossible when the repo already has `.codex/`, so it needs (a) or (b) as a
     fallback anyway.


   _Leaning:_ **(a).** P4 decides it: a visible one-line edit the user commits once beats an
   invisible per-clone write to the file that defines invisibility. The write-once rule
   `.yolo/.gitignore` already follows — a user who removes the line is not fought — carries
   over unchanged.

   ⚠ **Note, 2026-09-24, on that last sentence's premise.** `.yolo/.gitignore` is keyed on the
   FILE: it is written whenever the file is absent, so emptying it is not fought and deleting it
   is. (a)'s "write-once by content check" is keyed on the LINE, and run on every launch it
   re-adds a line the user removed, as `yolo init`'s own "append if the file lacks it" would if it
   ran more than once. So "not fought" does not carry over by itself; (a) needs its own record of
   having written the line once. The leaning itself is unchanged.

   <!-- vantage: question id=OQ-WS6 -->

   **Answer:**
   > **Deferred with [OQ-WS5](#OQ-WS5)**, 2026-09-27: the maintainer confirmed it is moot for v1
   > (*"and OQ6 is moot, right?"*). The links are the host half's mechanism, which v1 does not
   > build; the question returns, unchanged, when the host half is taken up.
   >
   > **Decided on its leaning, (a), 2026-10-04, when the host half was taken up under the
   > maintainer's delegation; open to the maintainer's revision.** The launch appends one line,
   > `/<link>`, to the `.gitignore` of the directory it runs in, once, and only inside a git work
   > tree. The 2026-09-24 note's premise is met by a record: yolo remembers that the line stood
   > there, whether it wrote the line or found it, so a line the user removed is not added back
   > ([WS-D22](#WS-D22)).


### 2.1 Where each agent reads skills — measured

Every row was read from the bundle installed in this jail on 2026-09-17, by grepping its
strings; the vendor docs were not trusted, following the lesson
[`../plans/pack-host-management-plan.md`](../plans/pack-host-management-plan.md#n6-new--copilot-reads-claudes-plugin-manifests-and-namespaces-plugin-skills)
records (a docs-only pass got three facts wrong about `copilot`). "Home scope" is the
directory under `$HOME`; "project scope" is relative to the directory the agent is started in.
Those are plain words, not terms — the vendors say *personal*, *user* and *global* for the
first and *project* for the second.

| Agent, version | Project-scope skills dirs the binary names | Home-scope dir (= the pack's `into`) | Collision rule, as documented | Follows symlinks |
| :--- | :--- | :--- | :--- | :--- |
| `claude` 2.1.275 | `.claude/skills` only. `.agents/skills` appears in the binary solely inside an **importer** that copies it into `.claude/skills` | `~/.claude/skills` | personal > project, same name = higher wins **silently**; plugin skills namespaced `plugin:skill` | yes (documented; verified 2026-08-01) |
| `copilot` 1.0.48 | `.github/skills`, `.agents/skills`, `.claude/skills` | `~/.copilot/skills` (also reads `~/.agents/skills`, `~/.claude/skills`, `COPILOT_SKILLS_DIRS`) | project first; **first-found-wins, deduplicated by bare name**, loser silently dropped; plugin skills namespaced but deduplicated flat | not verified |
| `codex` 0.145.0 (row corrected against 0.157.0, 2026-09-27) | `.codex/skills` (the `skills` dir of every project config layer's `.codex/` folder) **and `.agents/skills`** (every directory from the project root down to the cwd). Read from the skills loader's source at the `rust-v0.157.0` tag, `codex-rs/ext/skills/src/host_roots.rs`: the binary carries neither as text, and this row first read its strings and missed the second ([WS-D12](#12-decision-ledger)) | `$CODEX_HOME/skills` = `~/.codex/skills` (deprecated there in favor of `~/.agents/skills`) | not verified | not verified |
| `opencode` 1.18.31 | `.opencode/skills`, `.claude/skills`, `.agents/skills` | `~/.config/opencode/skills` | not verified | not verified |
| `pi` 0.85.1 (row corrected against 0.87.1, 2026-09-27) | `.pi/skills` (its `CONFIG_DIR_NAME` is `.pi`) **and `.agents/skills`**, the latter discovered from the cwd up to the repository root — its `docs/skills.md` and the package manager's ancestor walk both say so; this row first read `CONFIG_DIR_NAME` alone and missed it ([WS-D12](#12-decision-ledger)) | `~/.pi/agent/skills` | loads both scopes; **deduplicates by real path**, so a symlink to an already-loaded file loads once | yes (follows symlinked dirs, skips broken ones) |
| `agy` 1.1.7 | `.agents/skills` | `.gemini/config/skills` (the pack's `into`; the binary's strings do not name it) | not verified | not verified |
| `oh-omp` | not installed here — unverified | `.oh-omp/agent/skills` | — | — |

> [!NOTE]
> **Codex and `.agents/skills` — settled 2026-09-27: codex reads it.**
> [`../research/agent-config-distribution.md`](../research/agent-config-distribution.md#part-1--where-agent-configuration-lives-per-agent)
> (gathered 2026-07-25) listed `.agents/skills/` for Codex, and this note once called the row
> *unconfirmed* because the binary names no such literal. The loader's source at the tag of the
> installed 0.157.0 settles it: `repo_agents_skill_roots` joins the constants `.agents` and
> `skills` onto every directory between the project root and the cwd. The binary's strings agree
> once read closely — its only `.codex/skills` literals are prose about the home dir, and its one
> `.agents/skills` literal belongs to an external-agent migration — which is why a string grep
> could neither confirm nor refute the row.

**The fact this table settles:** there is **no project-scope path every agent reads.**
`.agents/skills/` — the Agent Skills standard's emerging interop path — reaches `copilot`,
`opencode`, `agy`, `pi` and `codex`, and misses `claude`. `.claude/skills/` reaches `claude`,
`copilot` and `opencode` and misses `codex`, `pi` and `agy`. A repo that wants all six ships two
trees, or one tree and a symlink, and has to know that. (The sentence first said `.agents/skills/`
missed `pi` and, unconfirmed, `codex`, from the two rows corrected above.)

## 12. Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| WS-D1 | *Implementation decision.* The declaration is `project_dirs` on the agent pack's `skills` **destination** (`agent` + `into`), in the agent's own precedence order, read through one accessor (`Manifest.ProjectSkillDirs`). Entries must be clean, relative, inside the workspace and outside `.git`/`.yolo`, each once; the field is refused on any other kind and on a content entry. The in-jail decode tolerates it as it tolerates any newer field, so the strict-decoder trap the sketch warned of does not arise | 2026-09-27 | [§5](#5-the-pack-declares-what-its-agent-reads) | ✅ `bd79aed3` |
| WS-D2 | *Implementation decision.* The source set is the **selected** packs' declarations in config order, then every **shipped** pack's by name, each path at its first appearance. The selected packs are read too so a configured agent pack yolo does not ship counts: its destination's skip rule already names its paths. This order is the collision rule (WS-D3) | 2026-09-27 | [OQ-WS3](#OQ-WS3) | ✅ `7df13e51` |
| WS-D3 | *Implementation decision.* Two source dirs with a same-named skill: the **first** in WS-D2's order wins in every destination the mirror delivers to, the loser is never delivered, and one line names both. One real directory reached by two spellings (a committed link) is one skill, not a collision, and a source dir that is a link to another source is one source. *Amended by WS-D13:* a destination whose agent natively reads a losing copy is sent neither, and the line names it — this row first said the first wins "in every destination", which that agent made false | 2026-09-27 | [§4.1](#41-mechanism-a--the-staged-mirror) | ✅ `7df13e51`, `debb2d98` |
| WS-D4 | *Implementation decision.* The skip rule works at two grains: a destination gets no copy of a source dir its own `project_dirs` resolve to, **nor** of a skill whose real directory it already reaches through another source it reads (`.claude/skills/x → ../../.agents/skills/x` reaches `pi` natively). A destination declaring none receives every source. The third grain, the name, is WS-D13 | 2026-09-27 | [§5](#5-the-pack-declares-what-its-agent-reads) | ✅ `7df13e51` |
| WS-D5 | *Implementation decision.* "Lowest" is built as **fill the free names**: the built-ins, the packs and yolo's LSP plugin compose first, then the workspace takes only names none of them took. The LSP plugin's name is always taken, because its writer removes a same-named dir when nothing is configured. Each shadowed name is one line naming who took it and in which destinations | 2026-09-27 | [OQ-WS2](#OQ-WS2) | ✅ `7df13e51` |
| WS-D6 | *Implementation decision.* P5's reader is `confinedTree`: links are resolved lexically through an `os.Root` on the workspace, with the root's own `Lstat`/`Readlink`, before anything is opened, so an escape is named without anything outside being touched; every open is `O_NONBLOCK` (`O_DIRECTORY` for a dir), since an unflagged `os.Root` open blocked forever on a FIFO (measured). *Amended by WS-D15:* reads no longer go through the `os.Root`. Skipped and named, never fatal: an escape, a dangling link, a chain over 40 links, a cycle, a second link to one directory within a skill, a special file, an unreadable entry. The second-link rule bounds one skill's copy by its links times the largest tree one of them reaches; this row first called that "linear in the links", which hid that the bound is not the workspace's size ([R10](#8-risks)). A refusal names the entry, never its target — its reason included, since WS-D16 | 2026-09-27 | [§4.1](#41-mechanism-a--the-staged-mirror) | ✅ `7df13e51`, `c011321e` |
| WS-D7 | *Implementation decision.* Also never read: anything resolving under `.git` or `.yolo` (yolo's state — reading it would be a layer reading generated output, [§2.2](#22-what-yolo-composes-today)), and in a container the per-side shadow set `venvShadowMountArgs` mounts. The jail sees its own copy there, and the mirror must grant no authority the repo lacks ([OQ-WS1](#OQ-WS1)'s answer). macos-user shadows nothing, so nothing is excluded there | 2026-09-27 | [OQ-WS1](#OQ-WS1) | ✅ `7df13e51` |
| WS-D8 | *Implementation decision.* An absolute link is inside the workspace when it names the root as given, its real path, or, in a container, the jail's mount path `/workspace`: the agent there writes links that way. Nothing at that path on the host is read. Every other absolute target is an escape | 2026-09-27 | [§4.1](#41-mechanism-a--the-staged-mirror) | ✅ `7df13e51` |
| WS-D9 | *Implementation decision.* The layer is re-read on **every** entry, attach included, though an attach keeps the pack tree the jail booted with ([`OQ-PK2`](../reference/pack-system.md#oq-pk2)). The jail's agents already read the same tree natively and live, so a frozen mirror would be the stale half; each entry's `mirrored into` line discloses it. Closes R6's open half | 2026-09-27 | [§8](#8-risks) R6 | ✅ `7df13e51` |
| WS-D10 | *Implementation decision.* The disclosure goes to stderr, where an attach's other lines go, and has no quiet mode ([`OQ-RO3`](../reference/report-tiers.md#why-its-this-way)). One line each per refusal, collision and shadowed name, plus one `Workspace skills from <dir> mirrored into <destinations>: <skills>` line per delivering source dir. Silent when there is nothing to say. A workspace-supplied name that is not plain text is printed Go-quoted with `[` escaped, so a directory name cannot forge a line or restyle one; a refusal's reason gets the same treatment since WS-D16, and the held-back and native-reading lines are WS-D13's and WS-D14's | 2026-09-27 | [§4.1](#41-mechanism-a--the-staged-mirror) | ✅ `7df13e51`, `c011321e`, `debb2d98` |
| WS-D11 | *Implementation decision.* The layer is copied **once** per invocation into a private scratch tree and handed to each destination from there, so a refusal is said once and every destination gets the same bytes. The execute bit is carried, as packstage carries it. A top-level file in a source dir is not a skill, as for every layer; a skill with no `SKILL.md` is copied as-is, but one of which **no file** could be staged (every entry refused, or none there) is not delivered at all, so no empty `x` stands where a refused link was ([§10](#10-what-done-looks-like)'s fourth bullet) and a later source's skill of that name may win; absent and empty dirs are silent; a source dir that is a file, the workspace root, or a link out is named. The path bound and scratch-write refusals are WS-D17. *Amended by WS-D18:* this row first said there was no byte bound, which [OQ-WS7](#OQ-WS7) ruled and WS-D18 built | 2026-09-27 | [§4.1](#41-mechanism-a--the-staged-mirror) | ✅ `7df13e51`, `7219dcf6` (the no-empty-skill rule) |
| WS-D12 | *Implementation decision.* The shipped declarations come from the agents installed on 2026-09-27. `pi` declares `.agents/skills` too, correcting [§2.1](#21-where-each-agent-reads-skills--measured). `codex` declares `.codex/skills` and `.agents/skills`, from its skills loader's source at the tag of the installed 0.157.0 (`codex-rs/ext/skills/src/host_roots.rs`). This row first kept `.codex/skills` alone because the binary's one `.agents/skills` literal sits beside an external-agent migration — true, and beside the point, since the loader builds that path from segments; until corrected, every skill in a repo's `.agents/skills` reached `codex` twice. `omp` declares nothing, because it was not measured, so it receives every source. The witness is `internal/packload`'s census test and a real-install integration probe that greps each installed agent, from its launcher's `REAL_BIN`, for every declared path **at project scope** — not preceded by `/`, `~` or a path character. It first matched a bare substring, which `codex` passed on its home-dir prose alone. The rows no installed bundle carries as text are listed in the probe with where each was measured instead, and logged rather than checked: `codex`'s (built from segments), `copilot`'s (1.0.88 ships its code compressed) and `opencode`'s `.claude/skills` and `.agents/skills` (named only as home dirs). No string probe can find a path an agent reads and its pack does not declare ([R5](#8-risks)) | 2026-09-27 | [§5](#5-the-pack-declares-what-its-agent-reads) | ✅ `bd79aed3`, `22daf939` |
| WS-D13 | *Implementation decision.* The skip rule's third grain, the **name**: a destination is sent no copy of a skill whose name any directory its agent reads natively carries — a collision's losing copy, or an entry the reader refused, which the agent resolves in the jail where its target may be a skill. The collision line names the agents that read a losing copy natively (`…; pi reads the copy in .agents/skills natively and is sent no other`); a hold-back no collision explains gets a line of its own. Before this, with `.codex/skills/lint` and `.agents/skills/lint` and `packs: ["codex", "pi"]`, `pi` was sent `codex`'s `lint` beside the one it reads | 2026-09-27 | [§5](#5-the-pack-declares-what-its-agent-reads), [R4](#8-risks) | ✅ `debb2d98` |
| WS-D14 | *Implementation decision.* An agent that natively reads a workspace skill under a name a higher layer delivers to it sees both, which no layer order reaches; the launch says so — one line per name and directory, naming the agents and who took the name — and yolo still stages its own skill there. This is how [§10](#10-what-done-looks-like)'s fifth bullet is kept for those agents: the mirror never shadows, and native reading is disclosed rather than presented as covered | 2026-09-27 | [OQ-WS2](#OQ-WS2), [R2](#8-risks) | ✅ `debb2d98` |
| WS-D15 | *Implementation decision.* The reader READS by a walk from the workspace root's own descriptor, one component at a time, every open `O_NOFOLLOW` and `O_NONBLOCK` (and `O_DIRECTORY` for a directory), along the real path the `os.Root` classified; the `os.Root` only classifies. It follows a link that stays inside it, so a file flipped to a link into `node_modules` between its lstat and its open copied the host's per-side bytes into the staging, breaking [OQ-WS1](#OQ-WS1)'s "no authority the repo lacks" (P5 held: nothing outside the root was ever read). A swapped entry is refused as changed while it was being read | 2026-09-27 | [§4.1](#41-mechanism-a--the-staged-mirror) | ✅ `c011321e` |
| WS-D16 | *Implementation decision.* A refusal's reason is fixed text, and for a system error only the errno's own description — never the error's message, whose path can be a link's target, a newline and a counterfeit disclosure line included. The launch also quotes a reason that is not plain text, as it does a name, because the per-side reason names a path the repo's own `mise.toml` can set | 2026-09-27 | [§4.1](#41-mechanism-a--the-staged-mirror) | ✅ `c011321e` |
| WS-D17 | *Implementation decision.* A clone cannot fail a launch through the scratch copy either. An entry whose path inside its skill passes 512 bytes is refused: the reader reaches any depth one component at a time, while the copy is written by absolute path under the scratch tree, a compose dir, a staging dir and on `macos-user` the sandbox home, and macOS's `PATH_MAX` is 1024 — a `git clone` of a path past `PATH_MAX` failed every launch and attach of its workspace with `ENAMETOOLONG`. A write into scratch that fails refuses that entry. The one error left is failing to create the scratch root, and nothing the layer made outlives its call | 2026-09-27 | [§4.1](#41-mechanism-a--the-staged-mirror) | ✅ `c011321e` |
| WS-D18 | *Implementation decision.* [OQ-WS7](#OQ-WS7)'s cap is **32 MiB and 4096 files and directories per launch**, counted over everything the layer writes into scratch, and the refusal names the skill, the cap and what the skill would add. The numbers come from the largest real skill sets available on 2026-09-28, counting regular-file bytes with links followed and entries as files plus directories: yolo's built-in skills are 21,770 bytes in 5 entries; two personal skill packs, 217,524 bytes in 29 and 199,410 in 22; one user's composed `~/.claude/skills`, 437,859 in 55; the largest single plugin skills dir in Claude's official plugin marketplace, 488,003 in 76; and all eighteen of that marketplace's plugin skills dirs taken as ONE set, which no repository carries, 1,389,298 bytes in 210 entries. The caps sit about 24× and 19× above that last figure, which leaves room for a vendored tool of a few megabytes. No shipped pack carries skills of its own. Four mechanism choices: (1) **stage, then commit**: each skill is copied into a directory of its own and joins the layer only once the whole of it is there, so a refused skill leaves nothing behind and the skills before it stay; (2) **every write is charged before it is made**, a file its size before a byte of it is read, so a skill refused on one oversized file costs nothing more, and a file that grows past its charge is refused as changed while it was being read; (3) **the walk stops at the crossing**, so what a skill "would add" is a lower bound (what it had copied, plus the write that crossed): counting the rest would cost the reads the cap exists to prevent; (4) **the charge never falls**: a refused skill's partial copy stays spent, so a later skill that still fits is delivered, but a clone of many skills each just past the cap cannot make the host write the cap once per skill. The bound on one launch is therefore the cap into scratch plus the cap into each destination ([R10](#8-risks)). macos-user stages through the same `PrepareSkillsWith` call, so the cap applies there too | 2026-09-28 | [OQ-WS7](#OQ-WS7), [R10](#8-risks) | ✅ `60ceac43` |
| <a id="WS-D19"></a>WS-D19 | *Implementation decision, taken under the maintainer's 2026-10-04 delegation ("make them and build it … adjust later"); reversible.* **The host half is B, one step of `yolo host -- <agent>`, and nothing else.** It is the launch's last write, made just before the hand-over on both of its paths: after every pre-flight, the OpenAI prelaunch and every launch-owned service and doorway, so a launch refused at any of them, a bridge that cannot bind included, writes nothing into the workspace. It is a no-op, and silent, in a jail ([OQ-WS4](#OQ-WS4): the mirror alone there), in a directory that is or holds the credential boundary (`paths.WorkspaceScopeBreach`: the home itself), and for a program no selected pack gives a skills destination declaring `project_dirs`. `yolo host apply` and `yolo host env` never write it: the cwd selects nothing for a render ([OQ-WS5](#OQ-WS5)), and `env` launches nothing | 2026-10-04 | [OQ-WS5](#OQ-WS5), [§4.2](#42-mechanism-b--in-workspace-links) | ✅ `hostWorkspaceSkills` (`internal/cli/hostworkspaceskills.go`), called in `hostLaunch` before each hand-over; `TestHostLaunchLinksTheRepositorysSkillsIntoTheAgentsPathAndIgnoresItOnce` (the exec path), `TestAHostLaunchBesideALaunchOwnedServiceLinksTheSkills` (the resident path), `TestARefusedHostLaunchWritesNoWorkspaceSkillsLink`, `TestAHostLaunchRefusedAtServiceStartWritesNoWorkspaceSkillsLink`, `TestHostEnvWritesNoWorkspaceSkillsLink`, `TestHostWorkspaceSkillsWritesNothingInTheHomeOrInAJail`, `TestHostWorkspaceSkillsWritesNothingForAnAgentWithNoProjectDirs` |
| <a id="WS-D20"></a>WS-D20 | *Implementation decision, under the same delegation; reversible.* **One link, at the agent's first declared path, to the first source present.** When no path in the destination's `project_dirs` exists in any form (a directory, a committed link, a file, a link out of the tree), the launch writes a RELATIVE symlink at the first of them (`.codex/skills -> ../.claude/skills`). Its target is the first source present in the jail's own order ([WS-D2](#12-decision-ledger), through `run.WorkspaceSkillDirs`), less the agent's own paths and less any link yolo itself recorded for another agent; every later source present is named as not handed to this agent, since a link points at one directory (a second spelling of the chosen one, a committed link to it, is not another source), which is [§4.2](#42-mechanism-b--in-workspace-links)'s stated weakness of B against the mirror. A missing parent (`.codex/`) is created and recorded, and the link and its parents are written by a walk from the workspace root in which every directory open is `O_NOFOLLOW`, so a parent the repository made a link refuses the write instead of being written through | 2026-10-04 | [§4.2](#42-mechanism-b--in-workspace-links) | ✅ `hostWorkspaceSkillsIn`, `placeLinkNoFollow`; `TestHostLaunchLeavesARepositorysOwnAgentPathAlone`, `TestHostLaunchLeavesAnAgentDirThatLeavesTheWorkspaceAlone`, `TestHostLaunchWritesThroughNoLink`, `TestHostLaunchDoesNotNameASecondSpellingOfItsSourceAsDropped` |
| <a id="WS-D21"></a>WS-D21 | *Implementation decision, under the same delegation; reversible.* **P5 fails closed: the source is checked by the mirror's own reader before a link names it.** `jailcontent.CheckWorkspaceSkillSource` runs the staging reader over that one directory, and any refusal (a link out of the workspace, a FIFO, the [OQ-WS7](#OQ-WS7) cap) writes no link and removes a link yolo wrote before. Each refused entry is named by its workspace path and fixed reason through the jail's `displaySafe`, never by a link's target | 2026-10-04 | [P5](../reference/agent-briefings.md#ws-p5) | ✅ `CheckWorkspaceSkillSource`; `TestCheckWorkspaceSkillSourceRefusesAnEscapingLink`, `TestHostLaunchRefusesASourceWithAnEscapingLinkAndRemovesItsOwnLink` |
| <a id="WS-D22"></a>WS-D22 | *Implementation decision, building [OQ-WS6](#OQ-WS6) as decided on its leaning (a); open to the maintainer's revision.* **One `.gitignore` line, once, in a git work tree.** Whether the workspace is in one is an `Lstat` of `.git` at the directory and every directory above it: git is never run, since a repository's `.git/config` can name a program git runs. The line is `/<link>`, appended on a line of its own to the `.gitignore` of the directory the launch ran in, which must be a regular file and is opened `O_NOFOLLOW` and `O_NONBLOCK`. A line already there is left alone, and so is its later removal: a line that stood in `.gitignore` at a launch that placed or kept the link, whether yolo wrote it or a teammate's commit or the user's own edit put it there, and that is gone at a later launch, is not added back. The record's `ignore_seen` is the record the 2026-09-24 note asked for, and that launch says `git status` will list the link without saying who wrote the line; so does a launch whose `.gitignore` is a link, not a regular file, or larger than the 1 MiB yolo reads, and one that cannot open it for the append, whose line names the errno. The record outlives the link: a line the user removed stays out when the link goes and comes back | 2026-10-04 | [OQ-WS6](#OQ-WS6) | ✅ `ensureHostSkillsIgnoreLine`, `insideGitWorkTree`; `TestHostLaunchDoesNotAddBackAnIgnoreLineTheUserRemoved`, `TestHostLaunchDoesNotAddBackAnIgnoreLineItFoundAndTheUserRemoved`, `TestHostLaunchKeepsARemovedIgnoreLineOutAfterItsLinkWentAndCameBack`, `TestHostLaunchWritesThroughNoLink`, `TestHostLaunchOutsideAGitWorkTreeWritesNoIgnoreFile`, `TestHostLaunchInASubdirectoryOfAWorkTreeIgnoresTheLinkThere`, `TestHostLaunchDoesNotAppendToAnIgnoreFileLargerThanItReads`, `TestHostLaunchSaysWhenItCannotOpenTheIgnoreFileForTheAppend` (unprivileged only: root opens a read-only file for writing, so it skips as root and runs in CI) |
| <a id="WS-D23"></a>WS-D23 | *Implementation decision, under the same delegation; reversible.* **A link is yolo's only by a record, and every launch says what it did.** The record is a file under yolo's state dir (`paths.HostWorkspaceSkillsDir`), named by a digest of the workspace's resolved path and the link's path, never in the workspace's `.yolo/`, which the repository's agent can write. A link is refreshed or removed only while its `readlink` equals the record. It is removed, with any parents yolo made that are left empty, when its source vanishes, when another path the agent reads appears, or when the check refuses its source; an unrecorded link at the same path is the repository's and is untouched. Each launch prints, after a `yolo host: workspace skills:` prefix, what it placed, kept, refreshed or removed, the sources it did not hand the agent, every refusal, the ignore line, and each skill name the agent's home-scope directory also has. A line saying yolo could not do something also names the next step: make a linked parent a real directory or create the link by hand, remove a link it could not refresh, fix the temporary folder its check copies into, or add the ignore line by hand. No line claims the agent reads the link: whether `codex`, `copilot`, `opencode` and `agy` follow a symlinked project skills directory is unmeasured ([§2.1](#21-where-each-agent-reads-skills--measured)) | 2026-10-04 | [§4.2](#42-mechanism-b--in-workspace-links) | ✅ `loadHostSkillsRecord`, `removeOwnLink`; `TestHostLaunchRemovesItsLinkWhenTheSourceVanishesAndLeavesOthersAlone`, `TestHostLaunchRefreshesItsLinkWhenTheChosenSourceChanges`, `TestHostLaunchNeverTakesAnotherAgentsLinkAsItsSource`, `TestHostLaunchNamesTheSourcesItDropsAndTheHomeSkillsOfTheSameName`, `TestHostLaunchThatCannotCheckASourceSaysWhereTheCheckRuns`, `TestHostWorkspaceSkillsRecordLivesInTheStateDir`, `TestHostLaunchKeysItsLinkByTheWorkspacesResolvedPath`. UNMEASURED: an installed agent loading skills through the link |
