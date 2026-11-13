---
title: "Your rules first, and a tenth of ours: the briefing diet"
date: 2026-11-13
status: in-review
stage: DESIGN
next: "Rule OQ-BD1; the core and topic split does not wait on it"
tags: [briefing, skills, packs, context, design]
summary: "yolo's always-loaded briefing shrinks to a budgeted core of about 2.5 KB that points at the generated jail-mechanics skill, shipped packs' prose moves into pack skills, and the user's own prose is composed ahead of yolo's."
---

# Your rules first, and a tenth of ours: the briefing diet

**Status:** 2026-11-13. Nothing built. Sizes measured in a running `podman-self` jail of this
repository at `450287258`; Claude Code's thresholds read from its 2.1.323 binary the same day.

> **In short.** yolo's always-loaded briefing is a manual, and it sits ahead of the user's own
> rules. It becomes a budgeted **core** of about 2.5 KB that gives an agent its mental model and
> one pointer, composed after the user's prose; everything else is read on demand from a skill.

**Why it matters.** The maintainer's Claude briefing is 42,386 bytes, of which 26,867 are yolo's
and 15,519 are his. His rules start at byte 25,717. An agent that cuts a long file cuts his
rules, and Claude Code warns about the file's size at every session.

**The shape.** Three parts: a **core** rendered from applied facts; **topic files** in the
generated `jail-mechanics` skill, one per subject; and a **pack skill** for each shipped pack
whose prose is a manual. A test holds each to a byte budget.

**Cost.** Facts an agent reads today without asking, it must now open a file for. Tests that
assert sentences of the current body are rewritten against the topic files.

**Start at [§4](#4-the-core)**: the core's text is the design. The rest decides where the
removed text goes.

**Needs your ruling:** [OQ-BD1](#OQ-BD1).

**Reads with:** [`briefing-diet-plan.md`](briefing-diet-plan.md) (the implementation plan's
seed; its ordering task waits on [OQ-BD1](#OQ-BD1)),
[`agent-briefings.md`](../reference/agent-briefings.md) (how a briefing is composed today),
[`jail-mechanics-skill.md`](../reference/jail-mechanics-skill.md) (the generated skill this
extends), [`computed-briefings.md`](computed-briefings.md) (pack prose rendered from facts; it
composes into the same pack section and is unaffected).

---

## 1. Defined terms

- **Core** *(coined here)*: the part of yolo's jail-managed body that is in every briefing file
  an agent loads at session start. Not the whole briefing file, which also holds the user's
  prose and packs' prose.
- **Topic file** *(coined here)*: one Markdown file in the generated `jail-mechanics` skill that
  covers one subject of this jail, rendered from the same applied facts as the core. Not a
  recipe: a recipe says what to do after a change, a topic file says how one part of the jail
  works.
- **Pointer** *(coined here)*: a shipped pack's always-loaded prose after the diet, at most 250
  bytes, saying what the pack changes and which skill holds the rest.
- **User prose** *(coined here)*: briefing text the user wrote or chose: the host file a
  destination's `after` names, `agents_md_extra`, and the prose of every selected pack that is
  not shipped with yolo. Not a shipped pack's prose, which is yolo's.
- **Always-loaded** *(coined here)*: text an agent receives at session start without asking.
  For yolo that is the core, the pointers, and the `description` line of each skill yolo ships.
- **Alert** *(coined here)*: one of the three conditional sections that report an event rather
  than describe the jail: provisioning failed, the attach-skew notice, and the handoff.

## 2. What is loaded today

Measured 2026-11-13 in this jail, from `/home/agent/.claude/CLAUDE.md` (42,386 bytes), in file
order:

| Section | Bytes | Whose |
| :--- | ---: | :--- |
| Header (setup, confinement, tooling line) | 215 | yolo body |
| Environment | 1,970 | yolo body |
| Storage classes | 4,026 | yolo body |
| Loopholes | 2,872 | yolo body |
| Blocked Tools | 955 | yolo body |
| Additional Context Mounts | 217 | yolo body |
| Limitations | 622 | yolo body |
| Running a command on the host | 3,219 | yolo body |
| Skills | 579 | yolo body |
| `git` remotes in this jail | 5,741 | `packs/git` |
| `gh` in this jail | 5,300 | `packs/github` |
| The maintainer's personal packs | 15,519 | user prose |
| Waiting on the host; Claude Code's own worktrees | 1,151 | `packs/claude` |

Totals: the jail-managed body is 14,675 bytes, shipped packs add 12,192, so **yolo's share is
26,867 bytes (63%)** and the user's is 15,519.

The same composition reaches every agent. The other destinations in this jail: pi's `AGENTS.md`
is 49,933 bytes, and codex's `AGENTS.md` and copilot's `copilot-instructions.md` are 41,234
each.

The order comes from one rule: the jail-managed body, then `agents_md_extra`, then each selected
pack's prose in config order. Only a host file named by `after` goes ahead of yolo's body, and a
user whose rules live in packs has none.

### 2.1 What the agents do with a long file

| Agent | Finding | Status |
| :--- | :--- | :--- |
| Claude Code 2.1.323 | Warns "Large … will impact performance" when one instruction file is longer than `max(40,000, context window × 0.05 × a per-model factor)` characters, and when the files together pass `max(120,000, that)` | Read from the binary's source text. The per-model factor was not read. At a 200,000 window the floor of 40,000 applies, and the file is 42,386 |
| agy 1.2.9 | The maintainer reports that it silently drops the end of the file | UNMEASURED. The binary's strings name no limit, and no document in this tree records one |
| codex, copilot, opencode, pi, omp, muse | No limit recorded in this tree | UNMEASURED |

An unmeasured cap is not designed around. The design removes yolo's share and puts the user's
prose where a cut from the end cannot reach it; it does not claim that any agent then reads the
whole file. The one observation that would change a number here is in
[§9](#9-what-done-looks-like).

## 3. Principles

- **P1. The always-loaded text builds a model; it does not instruct a procedure.** A sentence
  stays in the core only if an agent that never read it would do damage or waste a turn
  *before* it had a reason to look anything up.
- **P2. A refusal already carries its next step.** Every yolo refusal names the way forward, so
  the briefing does not restate what a refusal will print.
- **P3. The user's prose is never the part yolo displaces.** yolo's bytes are bounded, and they
  are composed where they cannot push the user's text toward a cut.
- **P4. One store per fact.** A fact about this jail is rendered once, from the decision that
  made it: in the core, or in one topic file. No second surface renders the same fact.
- **P5. A budget that is not tested is a wish.** Every bound in this document is a test.

## 4. The core

The core for this jail, as it would render (1,555 bytes). Each line is conditional on the fact
it states, as the body is today: no forwarded port, no port clause.

```markdown
# YOLO Environment — setup podman-self

You are running under setup **podman-self**: Podman, as you; bridged.
You are running inside a container.

- **Workspace**: `/workspace` is the host directory `/home/matt/code/yolo-jail`, bind-mounted live: the same files, no sync step.
- **What survives a restart**: the workspace, and `$YOLO_DURABLE_DIR` (`/workspace/.yolo/durable`) for scratch, clones and worktrees. `/tmp` does not. Most of home is read-only. Never move or delete `/workspace/.yolo`, and never `git clean -x` the workspace.
- **Network**: `localhost` is the jail's own. The host is `host.containers.internal`. Host ports forwarded to `localhost` here: 8919.
- **Not in here**: the host's credentials, sudo, and the host's home. Host directories shared with you are under `/ctx`.
- **Asking the human**: a refused command prints every way to get it approved. Relay that whole menu, each command alone in a code block, then run `yolo wait <id>` in the background and retry once when it exits 0. `yolo approve`, `yolo config apply` and `yolo allow` are the human's to run. For anything else only the host can do, use `yolo host-run`.
- **Blocked here**: recursive `grep` (use `rg`, where `-r` means replace) and `find` (use `fd`); `mv`, `rm` and `git clean` refuse to touch `.yolo`.

Read the **jail-mechanics** skill (`~/.claude/skills/jail-mechanics/SKILL.md`) before you edit config, ask for a restart, or diagnose a refusal. Its index has one short file per topic: storage, network, mounts, host requests, loopholes, blocked tools, limits.
```

Why each line is there:

| Line | What goes wrong without it |
| :--- | :--- |
| Header | An agent that thinks it is on the real machine, or in a container when it is not, reasons wrongly about what is disposable. The title and the two framing sentences are today's bytes; the tooling line moves to the index, and the test that pins the header moves with it |
| Workspace | Agents invent a sync step, or treat `/workspace` as a copy they may rewrite |
| What survives | Work put in `/tmp` is lost at the restart; a `git clean -x` deletes the jail's own state. Neither announces itself beforehand |
| Network | `localhost` silently reaches the wrong machine. No refusal follows, only a connection error |
| Not in here | Agents spend turns hunting for `~/.ssh` or trying `sudo` |
| Asking the human | The three rules today's host section opens with: approvals are the human's, commands go on their own lines, and a background waiter follows. A refusal prints the menu; it cannot make the agent relay it |
| Blocked here | Present only when a selected pack blocks a tool. It names each block's replacement, and the `rg -r` trap only when `rg` is one |
| Pointer | Without it nothing else is found |

The pointer's path is the skills directory of the agent this destination belongs to. A
destination with no skills directory of the same agent identity gets the sentence without the
parenthesis.

What leaves the body, and where it goes:

| Today's section | Goes to |
| :--- | :--- |
| Storage classes | `topics/storage.md` |
| Network detail, both port sections | `topics/network.md` |
| Additional Context Mounts | `topics/mounts.md`, beside the existing `host-map.md` |
| Running a command on the host | `topics/host-requests.md` |
| Loopholes | `topics/loopholes.md`; `yolo loopholes list` stays the live answer |
| Blocked Tools, with each message | `topics/blocked-tools.md` |
| Limitations, resource limits, packages, nix | `topics/limits.md` |
| Skills | The index itself |
| The tooling line (`yolo --help`, `yolo config-ref`) | The index |

Alerts stay in the briefing, ahead of the core, and are outside the budget: each appears only
after an event and each is something the agent must see without asking.

At the host notch the body is the confinement header alone, as today.

## 5. Where the rest lives

**The store is the generated `jail-mechanics` skill, extended with topic files.** It already has
what the removed text needs: it is rendered per entry from applied facts, refreshed on an
attach, staged read-only into every agent's skills directory, and it cannot be replaced by a
pack or a workspace skill of the same name. Its index gains a Topics list above Recipes, one
line per file that applies to this jail.

- A topic file exists only when its subject does. No loophole is active: no
  `topics/loopholes.md`, and no index line for it.
- A topic file states the case that holds, like every other generated sentence
  ([JM-P1](../reference/jail-mechanics-skill.md#JM-P1)).
- The skill's `description` names the subjects, so an agent's skill listing matches it to a
  storage, network, approval or refusal question. It stays at or under 200 bytes.
- An entry writes the core and the topic files from one fact sheet. It never writes a core that
  points at a topic file it did not also write: a render failure fails the entry the way a
  mechanics render failure does today.

**No new `yolo` command.** A command that printed the same topics would be a second renderer of
the same facts, running from a binary frozen at launch while the skill refreshes at each attach.
The commands that already answer live questions are named in the topic files: `yolo loopholes
list`, `yolo access`, `yolo config drift`, `yolo wait`, `yolo config-ref`.

**The two other built-in skills are unchanged.** `configuring-the-jail` and
`diagnosing-the-jail` are already on demand. Their `description` lines are held to the same 200
bytes.

## 6. Packs

**A shipped pack's manual becomes that pack's skill, and its briefing prose becomes a pointer.**

| Pack | Today | After |
| :--- | :--- | :--- |
| `git` | 5,741 bytes of prose | A pointer, and a `yolo-git` skill holding today's text |
| `github` | 5,300 bytes | A pointer, and a `yolo-gh` skill |
| `claude` | 1,151 bytes in two files | One pointer of at most 250 bytes; what does not fit moves to a skill the pack ships |
| `pi` | 2,219 bytes in two files | The same |

The two pointers that carry the most, as proposed (234 and 190 bytes):

```markdown
**`git` remotes** go through a broker on the host, which holds the credentials. A refused fetch or push prints its own next steps. Push your work to a new branch of your own. Before a push or a new remote, read the **yolo-git** skill.

**`gh`** runs the host's GitHub login through a broker, for this workspace's repositories only. Exit 64 is a refusal that says what to do instead. Before a write, read the **yolo-gh** skill.
```

- **The skill is the pack's, not core's.** Core does not know what `git` is; the pack ships
  `skills/yolo-git/` beside its `briefing/`. The `yolo-` prefix keeps the name clear of a
  user's own `git` skill, since two packs shipping one skill name refuse the launch.
- **A pack skill about a jail-only mechanism is not delivered at the host notch.** The pointer's
  contribution already declares `describes = ["intercept"]`, which keeps it out of a real home.
  The same field is accepted on a `skills` contribution with the same meaning.
- **The budget binds shipped packs only.** Each shipped pack's prose for one destination is at
  most 250 bytes. A user's pack and a fetched pack are user prose: yolo does not cap, trim or
  reorder them.
- **A user who wants the same diet uses the same tool.** A pack can ship a skill today; the
  maintainer's 3,385-byte Go build cache section is the obvious candidate. That is his edit to
  make, not a mechanism to build.

## 7. Order

Each destination's file is composed as two blocks, and which comes first is
[OQ-BD1](#OQ-BD1):

- **The user block:** the host file `after` names (when it is the user's), then
  `agents_md_extra`, then each non-shipped pack's prose in config order.
- **yolo's block:** any alert, the core, then each shipped pack's pointer in config order.

A `---` rule separates the blocks when both are non-empty. With no user prose the file is
yolo's block alone, with no rule. With no selected pack there is no briefing, as today.

Whether a pack is shipped is the fact yolo already keeps about a pack's origin. A shipped pack
the user has forked is no longer shipped, and its prose is user prose.

**The host notch composes the same two blocks in the same order.** `yolo host apply` reads only
the user-scope `agents_md_extra`, as today.

Under either answer to [OQ-BD1](#OQ-BD1), yolo's block is at most about 2.6 KB, so the user's
prose moves by at most that much in either direction. The ruling decides what a cut from the end
removes.

## 8. Budgets, and the test that holds them

| Bound | Limit | Measured over |
| :--- | ---: | :--- |
| The core | 1,800 bytes | One reference fact sheet per setup: `podman-self`, `apple-container-self`, `seatbelt-self`, `seatbelt-yolo-account` |
| One shipped pack's prose for one destination | 250 bytes | Every shipped pack, every destination it reaches |
| yolo's block for Claude with `claude`, `git`, `github` and `guardrails` selected | 2,600 bytes | The composed file, through the launch's own composer |
| The `description` of a skill yolo ships | 200 bytes | The built-in suite, the generated skill, every shipped pack's skills |

2,600 is 9.7% of today's 26,867.

- **The reference fact sheet is a fixture, not this jail.** It has one forwarded port, one
  context mount, a durable directory, and the `guardrails` blocks: the facts that add lines.
- **A failure names the bytes over and the largest lines.** The fix it names is moving text to
  a topic file, not raising the number.
- **The composed-file bound runs through the function the launch calls**, so it fails if the
  launch stops composing in two blocks.
- **Unbounded facts have a fixed cost.** A jail with twenty forwarded ports must not grow the
  core by twenty entries: past four, the port clause gives the count and names
  `topics/network.md`. The blocked-tools line names at most the replaced tools; guards are one
  clause.

**A launch says when a file an agent will warn about is still too long.** An agent pack may
declare, on its briefing destination, the character count above which its agent warns. Only a
measured value is declared; today that is Claude's 40,000. When a composed file passes its
destination's value, the launch prints one line: the file, its size, the three largest
contributors by name, and the next step (move a section into a skill in that pack). It never
refuses, and it never trims.

## 9. What done looks like

- The Claude briefing in this jail is about 18.1 KB: 15,519 of the maintainer's and at most
  2,600 of yolo's. Claude Code prints no size warning for it.
- `~/.claude/skills/jail-mechanics/` holds `topics/` with the seven files above, and the index
  lists them.
- Every sentence an agent could read in today's body is in the core, a topic file, or a pack
  skill; none is deleted without a destination.
- The budget test fails when a line is added to the core past its limit.
- **The agy observation, which no automated test may make** (tests never start an agent): the
  maintainer puts a unique word on the last line of a personal pack's briefing, starts agy in a
  jail, and asks for the word. Before the build, with the 42 KB file, this also measures
  whether agy cuts and where. The result is recorded in the table in
  [§2.1](#21-what-the-agents-do-with-a-long-file).

## 10. What this does not do

- It does not shorten, summarize or reorder the user's own prose.
- It does not add a config key for the budget. The number is yolo's, and a test owns it.
- It does not declare a size limit for any agent whose limit was not measured.
- It does not merge the built-in skills into one.
- It does not change the host notch's body.
- It does not change what a pack may put in a briefing, or the audience fields.

## 11. Alternatives considered

| Alternative | Verdict |
| :--- | :--- |
| Move the user's prose first and leave yolo's body as it is | Rejected. It fixes the cut and leaves 26.9 KB in every context and Claude's warning in place |
| No always-loaded core at all; everything in the skill | Rejected. An agent that has not opened the skill does not know `/tmp` is lost or that `localhost` is not the host, and neither mistake produces a refusal that would send it there |
| A `yolo guide <topic>` command instead of topic files | Rejected: a second renderer of the same facts, from a binary frozen at launch |
| Topic files for `git` and `gh` inside `jail-mechanics` | Rejected. Core would have to know two packs by name |
| A briefing contribution marked on-demand, staged by yolo into a skill | Rejected for now. A pack can already ship a skill; a new kind earns its place when a user pack needs it |
| One static skill instead of the generated one | Rejected. The removed text is per-jail fact: paths, ports, the active loopholes |

## 12. Costs and risks

| Risk | Mitigation |
| :--- | :--- |
| An agent does not open the skill and misses a rule that was always-loaded | The core keeps every rule whose breach is silent (P1). The rest are behind a refusal that names its own next step (P2) |
| An agent with no skill mechanism cannot find the topics | The pointer gives the file's path, so reading it needs no skill support |
| A running jail attached by a newer yolo gets a core that points at topics its skill tree lacks | One entry writes both from one fact sheet; an entry that cannot write the topics does not write the new core |
| The budget is met by deleting facts | Done requires a destination for every sentence, and the budget test's failure names moving text, not cutting it |
| A user pack ships a skill named `yolo-git` | The existing collision refusal names both packs and the two remedies |

## 13. What I would build, in order

1. The core and the topic files, with the core and description budgets. This is where most of
   the bytes are and it waits on no ruling.
2. The two-block order, once [OQ-BD1](#OQ-BD1) is ruled, with the composed-file budget.
3. The four shipped packs' pointers and skills, with the pack budget.
4. The launch's size line and Claude's declared value.
5. The reference documents.

Steps 1 to 3 are the 0.18.0 slice.

## Open Questions

1. 💬 **OQ-BD1: Should your own rules come before yolo's text in the briefing file?**

   Matt keeps his working rules in personal packs. He starts agy in a jail; agy loads
   `~/.gemini/config/AGENTS.md`, where yolo's text comes first and his rules start 25 KB down,
   and he reports that agy drops the end of the file. After the diet yolo's text is about
   2.5 KB, so either order moves his rules by that much. The choice is what an agent that cuts
   from the end loses.

   - **A. Your rules first, yolo's text last.** A cut removes yolo's text first: the agent may
     not know `/tmp` is lost at a restart. Your rules are cut only if they alone exceed the
     agent's limit.
   - **B. yolo's text first, your rules after.** The agent always knows where it is. Your rules
     start 2.5 KB down, and a cut removes their last section first.

   <!-- vantage: question id=OQ-BD1 leaning="A. Your rules first: yolo's text is the part yolo can afford to lose, since the jail-mechanics skill's description still reaches the agent by another channel." -->

   _Leaning:_ A. Your rules first: yolo's text is the part yolo can afford to lose, since the
   jail-mechanics skill's description still reaches the agent by another channel.

   **Answer:**
   > _(empty — fill in when decided)_

## Decision Ledger

Implementation decisions taken by the design seat, not owner rulings; each is
reversible.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| BD-D1 | The on-demand store is the generated `jail-mechanics` skill with topic files; no new `yolo` command | 2026-11-13 | [§5](#5-where-the-rest-lives) | — |
| BD-D2 | A shipped pack's manual is that pack's own skill, named `yolo-<subject>`; its briefing prose is a pointer | 2026-11-13 | [§6](#6-packs) | — |
| BD-D3 | `describes` is accepted on a `skills` contribution, with the meaning it has on `briefing` | 2026-11-13 | [§6](#6-packs) | — |
| BD-D4 | Budgets: core 1,800 bytes; shipped pack prose 250 per destination; Claude's reference block 2,600; skill descriptions 200. Shipped content only | 2026-11-13 | [§8](#8-budgets-and-the-test-that-holds-them) | — |
| BD-D5 | Alerts stay always-loaded, ahead of the core, outside the budget | 2026-11-13 | [§4](#4-the-core) | — |
| BD-D6 | The pointer names the skill file's path for the destination's own agent | 2026-11-13 | [§4](#4-the-core) | — |
| BD-D7 | The `rg -r` warning is rendered only when a block names `rg` as a replacement | 2026-11-13 | [§4](#4-the-core) | — |
| BD-D8 | `agents_md_extra` and non-shipped packs' prose form the user block with the host file; the host notch composes the same two blocks | 2026-11-13 | [§7](#7-order) | — |
| BD-D9 | An over-long composed file is disclosed at launch against a pack-declared, measured value; never refused, never trimmed | 2026-11-13 | [§8](#8-budgets-and-the-test-that-holds-them) | — |
| BD-D10 | No size limit is declared for an agent whose limit was not measured; agy's is an owner observation | 2026-11-13 | [§9](#9-what-done-looks-like) | — |
