---
title: "The agent's own session lives in .yolo, and nothing tells it so"
date: 2026-11-07
status: accepted
stage: DESIGN
next: "Move the three top-level locks under .yolo/run/ once no supported older yolo takes them at the old path (SD-D15)"
depends-on:
  - named-setups.md
tags: [design, storage, workspace-state, briefing, guardrails, recovery, setups]
summary: "An agent moved a workspace's .yolo to /tmp to rule it out while bisecting, and lost every Pi session of that workspace when the jail restarted, because the agents' homes are bind-mounted from .yolo and /tmp dies with the jail. No single guard reaches every setup, so this design layers five: a README and a current .gitignore inside .yolo, three sentences in every briefing, a refusing mv/rm guard in the guardrails pack, boot-stamped identity checks that notice a vanished .yolo, and an automatic rescue of the still-mounted homes before the jail exits. A --fresh-home launch is the supported way to do what the agent wanted."
---

# The agent's own session lives in `.yolo`, and nothing tells it so

**Status:** 2026-11-07. Nothing built. Evidence verified at `024b59206`. The seat's design, pending
the owner's review; every ledger row is the seat's.

> **In short.** The incident was not a careless agent; it was a directory that looks like build
> output, a briefing that never says the agent's own transcript is in it, and a jail that loses
> the bytes it is still holding open when it exits. Fix the legibility and the briefing first,
> notice the move while the jail still holds the bytes, and make the loss survivable with a rescue
> at stop.

**Why it matters.** Every agent's transcripts, logins and installs live under the workspace's
`.yolo`, beside logs and locks, and the jail shows that directory to the agent at a path it can
`mv`. The loss this time was a fresh jail's; the same `mv` in a six-month-old workspace takes
every session every agent ever ran there.

**The shape.** Five layers in two groups. **Unlikely:** a README and a refreshed `.gitignore` in
`.yolo`, three briefing sentences, and a `mv`/`rm` guard in the guardrails pack. **Survivable:** a boot-stamped identity the keeper
re-checks on every session event, and a rescue copy of the still-mounted homes when the bind
source is gone at stop. Plus `--fresh-home`, the supported way to start without the state.

**Cost.** The guard adds two wrappers to the PATH of every guardrails jail; the rescue can copy gigabytes, once, on an event that should
never fire.

**Start at [§3](#3-the-design-one-layer-at-a-time)** — the layers. [§2](#2-the-incident-and-why-each-existing-layer-missed-it)
says why each existing one missed.

**Ruled 2026-11-07:** every open question is settled ([SD-D10](#SD-D10) to [SD-D15](#SD-D15)): no mask,
the guard ships in the guardrails pack, the rescue runs automatically, `--fresh-home` is the
supported fresh start, and the logs and locks leave the top level. Nothing is built.

**Reads with:** [`jail-home.md`](../reference/jail-home.md) (the storage classes the briefing
renders, and [OQ-JH1](../reference/jail-home.md#OQ-JH1) on relocating `.yolo` by a link),
[`named-setups.md`](named-setups.md) (per-setup homes under `.yolo/setups/<name>/`,
[§9](named-setups.md#9-state-per-setup)),
[`disk-footprint.md`](../reference/disk-footprint.md) (`yolo stores`, where a fresh home and a
rescue would be listed).

---

## 1. Summary: the layers, ranked by value per cost

| Rank | Layer | Reaches | Cost | Value |
| :--- | :--- | :--- | :--- | :--- |
| 1 | **Briefing**: the home dirs are stored under `/workspace/.yolo`; your session is in there; never move, rename or delete `.yolo`; `/tmp` holds nothing you mean to keep ([§3.2](#32-the-briefing-and-the-skills)) | every agent, every setup | three sentences in a section every context loads | the one layer that names the agent's own stake |
| 2 | **`.yolo/README.md`** and a current `.gitignore` ([§3.1](#31-legibility-of-yolo-itself)) | every reader that opens the directory | one generated file; a ruling on the never-touch promise | turns "looks like build artifacts" into "says what it is" |
| 3 | **Rescue at stop** ([§3.5](#35-recovery-and-survivability)) | container setups | a copy, only when the bind source is gone | the incident loses nothing: the bytes were alive until Ctrl+C |
| 4 | **Detection**: boot-stamped identity, re-checked on session events ([§3.4](#34-detection)) | every setup | a stat per event | the human learns before the restart, with the next step named |
| 5 | **`mv`/`rm` guard** ([§3.3](#33-mechanical-guards-where-they-are-cheap-and-honest)) | every agent that runs the shell of a jail that selects the guardrails pack ([SD-D12](#SD-D12)) | two shims; bypassable by design | a speed bump with the right message on it |
| — | **`--fresh-home`** ([§3.6](#36-a-supported-way-to-do-what-the-agent-wanted)) | every setup | a flag and a stamped home dir | the agent's goal, without the `mv` |

My read: layers 1 and 2 would have changed the agent's decision and cost almost nothing; layer 3
would have saved the sessions anyway. No layer makes the incident impossible: a mask over the
homes was weighed and rejected ([SD-D11](#SD-D11)).

## 2. The incident and why each existing layer missed it

An agent in a fresh podman jail, bisecting, ran the equivalent of `mv /workspace/.yolo /tmp/`.
Three facts made that fatal, and each is a layer that exists today and did not fire:

| Existing layer | What it says | Why it missed |
| :--- | :--- | :--- |
| The `.yolo/.gitignore` ([`paths.go:694-702`](../../internal/paths/paths.go)) | "None of it belongs in version control … `home/` is the jail's home overlay" | Its only message is *don't commit*. It names `home/`, which the current layout no longer uses ([`setupstate.go:13-15`](../../internal/paths/setupstate.go)), and it is never refreshed ([`paths.go:713-714`](../../internal/paths/paths.go)) |
| The briefing's storage classes ([`persistencesection.go:153-155`](../../internal/jailcontent/persistencesection.go)) | `~/.pi`, `~/.claude` … are per workspace and survive restarts | It never says *where* they survive. The agent read `~/.pi` and `/workspace/.yolo` as two places. Only the macos-user header ties home to `.yolo` ([`briefing.go:350`](../../internal/jailcontent/briefing.go)) |
| The briefing's `/tmp` line ([`persistencesection.go:100-102`](../../internal/jailcontent/persistencesection.go)) | "Survives nothing … Throwaway files only, never a worktree" | Read as a rule about *new* files. Nothing says a file *moved there* is lost too |
| The durable dir ([`jail-home.md` DS-P3, DS-P4](../reference/jail-home.md#four-principles)) | `/tmp` is per launch; yolo never deletes your work | The agent had used `.yolo/durable`, so `.yolo` read as *its* scratch, movable. DS-P4 is about yolo's deletions, not the agent's |
| The top-level listing of `.yolo` | 40-odd entries: logs, `*.lock`, `build-*.log`, JSON records | The irreplaceable five (`home/` or `setups/`, `durable/`, `approvals/`, `archive/`, `nix-roots/`) are indistinguishable from the rest |

And why the loss was total: inside the jail `/workspace` and `/tmp` are different filesystems, so
`mv` copied and deleted; the live binds from `.yolo/home/<name>` kept the deleted inodes open, so
the session kept running on files no path reached; the copy sat on a per-launch scratch volume
the launcher's remover deletes after the jail exits
([`scratchremoval.go:15`](../../internal/cli/run/scratchremoval.go)). Ctrl+C ended the last session,
the keeper stopped the jail ([`jailmain.go:23`](../../internal/entrypoint/jailmain.go)), and both
copies were gone. **The bytes were alive, under the binds, until that moment.** That is the fact
[§3.5](#35-recovery-and-survivability) builds on.

## 3. The design, one layer at a time

### 3.1 Legibility of `.yolo` itself

**A README that any reader meets first.** `EnsureWorkspaceStateDir` writes a second file beside
the `.gitignore`, `README.md`, and unlike the `.gitignore` it **rewrites it on every launch**: it
is yolo's own text about yolo's own directory, with no user content to preserve, and a stale one
is the defect this design exists to remove ([SD-D1](#SD-D1)). The header says so. Its body is
short, and the first line is the one that matters:

```text
This directory is yolo-jail's state for this project. The agents' homes are in here:
every transcript, login and install of every agent that ran in this workspace.

Never move, rename, delete or `git clean` this directory or anything under it, except your
own files under durable/. To start an agent without this state, run `yolo --fresh-home`.

setups/<setup>/home/   the agents' homes (bind-mounted at ~/.claude, ~/.pi, ...)  IRREPLACEABLE
durable/               agents' scratch that survives restarts (worktrees, evidence) IRREPLACEABLE
approvals/, approval-waiters/   host approval state                                  IRREPLACEABLE
archive/               verbatim copies of your pre-yolo agent config, kept forever   IRREPLACEABLE
nix-roots/, receipts.jsonl, config-boot.json   what the reapers and drift checks read
*.log, *.lock, build-*.log, last-launch*.json   logs and locks: regenerated

yolo rewrites this file at every launch.
```

The per-setup table rows are rendered from the same `HomeSurface` list the binds come from, not
hand-written, so a pack that adds a home dir is listed without anyone remembering to.

**The `.gitignore` says the wrong layout and will say it forever.** Its bare `*` is load-bearing
([`paths.go:679-686`](../../internal/paths/paths.go)) and must stay. Its comment names `home/`,
which new workspaces no longer have. The standing promise is that an existing file is never
touched, whatever it says ([`paths.go:713-714`](../../internal/paths/paths.go)); the promise exists
for a user who edited it. A file whose bytes are exactly a text yolo itself shipped is not an
edit, so yolo keeps the hash of every version it has shipped and rewrites a matching file to the
current text; any other content is never touched ([SD-D10](#SD-D10)). Either way the warning lives in the README, not the `.gitignore`: git
reads one and humans and agents read the other.

**The top level reads as build output because it is half build output.** Twenty-odd logs and
locks sit beside the five irreplaceable directories. The named-setups work already moves each
setup's run state under `setups/<name>/` ([§9](named-setups.md#9-state-per-setup)); in the same slice
the project-level logs move under `logs/` and the locks and pids under `run/`, leaving a top level
of `setups/ durable/ approvals/ archive/ README.md` ([SD-D15](#SD-D15)).

### 3.2 The briefing and the skills

The storage-classes section is loaded into every context, so every word costs. Three sentences
change, all in the section the agent already reads ([SD-D2](#SD-D2), [SD-D3](#SD-D3)):

1. **The per-workspace line gains its location.** After the list of home dirs: *"All of these
   are stored under `/workspace/.yolo` (`setups/<setup>/home`), and so is your own session:
   never move, rename, delete or `git clean` `.yolo` or anything under it except your files in
   `$YOLO_DURABLE_DIR`."* The sentence is rendered from the same persistence map as the list
   (DS-P1), so it names the layout the launch actually bound.
2. **The per-launch line closes the move loophole.** After "Throwaway files only, never a
   worktree": *"and never a holding place: a file moved here is gone at the restart."*
3. **The durable lead names the supported diagnosis.** *"To rule out this workspace's state in
   a diagnosis, do not move it: ask the human for `yolo --fresh-home`, or launch a nested jail
   from a throwaway workspace."*

On macos-user there is no per-launch line ([`persistencesection.go:75-77`](../../internal/jailcontent/persistencesection.go)):
sentence 1 joins the home sentence that already names `.yolo/setups/<setup>/home`
([`briefing.go:350`](../../internal/jailcontent/briefing.go)), sentence 2 joins the Mac's own
`/tmp` line. At the host notch, the same.

**The `diagnosing-the-jail` skill** gets one recipe, *Ruling out the workspace's state*
([SD-D8](#SD-D8)): it cannot be ruled out from inside the jail that runs on it; copy nothing (the
durable dir is inside `.yolo`, so a copy recurses); the human runs `--fresh-home`
([§3.6](#36-a-supported-way-to-do-what-the-agent-wanted)), or a nested launch uses a throwaway
workspace, which has a `.yolo` of its own.

### 3.3 Mechanical guards where they are cheap and honest

The owner's premise holds: no one guard reaches every setup. One is worth its cost; three are
not.

**The mask: not built** ([SD-D11](#SD-D11)). An empty read-only mount over the home's
workspace-side path would stop a `mv` of `.yolo` from reaching the homes on container setups. It
protects the homes only: the same `mv` still deletes `durable/`, `approvals/`, `archive/` and the
records, which the agent and yolo write through `/workspace`, and leaves a half-moved `.yolo`.
Inside the jail the home would read as empty, which misleads the very reader the README and the
briefing address. It reaches no macos-user or host setup, and whether Apple Container can mount
over a subpath of the workspace bind is unverified. The rescue covers the homes on every
container setup without it.

**The `mv`/`rm` guard.** The guardrails mechanism already generates a blocker on PATH when its
replacement is present ([`guardrails/pack.toml:5-12`](../../packs/guardrails/pack.toml)); this
needs a second kind, a **guard** *(coined here)*: a shim that runs the real program unless an
argument names the workspace's `.yolo`, or something under it that is not under
`$YOLO_DURABLE_DIR`, in which case it refuses with the next step
([SD-D4](#SD-D4)):

```text
mv: /workspace/.yolo holds this workspace's agent homes, including this session's own files.
Nothing here is safe to move. To start without it, ask the human for `yolo --fresh-home`.
Your own files under /workspace/.yolo/durable are yours to move. (YOLO_BYPASS_SHIMS=1 bypasses this shim.)
```

It refuses; it never redirects to a trash, since a move the agent did not ask for is the silent
side effect the house rules forbid. Paths are resolved by name after `realpath` of the *parent*,
never by reading a link's target. It reaches every agent that runs the jail's shell with the
jail's PATH, on every setup including `yolo host --`, which the guardrails pack already covers.
It does not reach a shell function that outranks PATH (Claude Code defines `grep` and `find` as
functions; whether it defines `mv` or `rm` is unmeasured), `python -c 'shutil.move(...)'`,
`rsync --delete`, Git aliases or an agent's own file tool. It ships in the opt-in guardrails pack, so
"nothing is blocked by default" still holds; a workspace without that pack meets the README and
the briefing only ([SD-D12](#SD-D12)).

**The Git clean guard.** The same `workspace-state` guard also stands in front of `git`.
Other Git commands and clean dry-runs pass unchanged. Git resolves its own global options
(including chained `-C`, `--git-dir`, `--work-tree` and `-c`); a destructive clean is refused
when its scope names protected state, a parent holding it, or the durable directory itself.
Explicit ordinary paths and contents below the durable directory pass. The guard does not
inject `-e .yolo`: with `-X`, an exclusion can select ignored state for deletion instead.
Wildcard, escaped, magic and case-insensitive pathspecs are conservatively refused in a
worktree that can reach protected state; unknown clean options are also refused because
an abbreviation can negate dry-run. The next step is `git clean -n`, then an explicit
safe path. This is a bypassable PATH speed bump, not a security boundary: aliases, direct
binary paths and other deletion tools remain outside it.

**Claude Code `PreToolUse` hook: not pursued** ([SD-D6](#SD-D6)). yolo renders no such hook
today (`PreToolUse` appears in no `packs/*/pack.toml` and no non-test file under `internal/`),
it reaches one agent, and the shell command it would inspect is the one the shim already sees.
A pack may add one for its own agent; core gains nothing by it.

**File flags: rejected** ([SD-D7](#SD-D7)). `chattr +i` needs `CAP_LINUX_IMMUTABLE`, which a
rootless jail lacks; macOS `uchg` stops yolo's own writes as surely as the agent's; and a flag on
`.yolo` itself does not stop `mv` of its children.

### 3.4 Detection

**What is recorded.** The boot record already written into `.yolo` (`config-boot.json`) gains an
**identity stamp** *(coined here)*: the device and inode of `.yolo`, of `durable/`, and of each
home bind source, read from `/proc/self/mountinfo` in a container and by `stat` of the real
paths on macos-user. A check is a `stat` of the same names compared against the stamp.

**When it is checked**, event-driven and never a poll ([SD-D5](#SD-D5)):

| Event | Who checks | On mismatch |
| :--- | :--- | :--- |
| a session attaches | the in-jail keeper, before the session's shell starts | prints the warning below on that terminal |
| the last session ends (keeper stop) | the keeper | runs the rescue ([§3.5](#35-recovery-and-survivability)), then prints |
| `yolo check` and `yolo stores` | the host CLI, against the stamp of a running jail | reports it as a finding with the same next step |
| the next launch | the launcher | finds no `.yolo` or a foreign one: creates a fresh one and says what it did not find |

**What it says**, on the terminal of the person who can act, with the next step first:

```text
yolo: /workspace/.yolo is not the directory this jail booted on (moved or deleted under it).
The agents' homes are still mounted and intact. Do not stop the jail. Put the directory back
(`mv <where you put it> /workspace/.yolo`), or run `yolo rescue` to copy the mounted homes out.
```

A mismatch is never a refusal: stopping the jail is what loses the bytes. The next launch's line
is plain too: a workspace with no `.yolo` is a legitimate state, and the line names the rescue
directory if one exists.

### 3.5 Recovery and survivability

**The rescue.** On container setups the home bytes survive the deletion of their path for as long
as the bind mounts hold them, which is until the jail exits. So the keeper, at stop, when the
identity stamp mismatches, **copies each mounted home surface** (`~/.claude`, `~/.pi`, … as the
`HomeSurface` list names them) **into `<workspace>/.yolo/rescue/<stamp>/home/<surface>/`**,
recreating `.yolo` if it is gone, before it lets the container end. The copy is a plain recursive
copy of what is mounted; it never reads the old path. Its size is the home's, which can be
gigabytes with installs, and it runs exactly once, on an event that should never fire. It runs
automatically at stop, and the stop prints one line naming the rescue directory and the `mv` that
restores it ([SD-D13](#SD-D13)).

**What the rescue cannot do.** `durable/`, `approvals/`, `archive/` and the records are not
mounted separately; once their path is gone they are gone from the jail's view, and the rescue
cannot reach them. On macos-user and at the host notch nothing is mounted: a `mv` of `.yolo` moves
the home *with* it, so the files are wherever they went, and the detection line names the `mv`
back. The rescue is the container setups' extra, not a parity breach: the loss it repairs only
exists there.

**What the next launch does with a rescue.** Nothing but say it is there. `yolo stores` lists it
with its size and the `mv` that restores each surface into the setup's home, to be run with no
jail running; no launch reads it and no reaper deletes it ([SD-D9](#SD-D9)).

### 3.6 A supported way to do what the agent wanted

The agent wanted to run without the workspace's state. Today that means a user-defined setup,
whose home is its own ([§9](named-setups.md#9-state-per-setup)), but a setup is a choice of walls,
not a diagnosis, its definition lives in user config a jail cannot write, and the per-setup home
is designed, not built.

**`yolo --fresh-home`** *(coined here, for the flag)*: a launch of the selected setup whose home is
a **new empty directory** at `<workspace>/.yolo/setups/<setup>/home.fresh-<stamp>`, bound where the
setup's home would be. The regular home is untouched. The fresh home is **kept**, not deleted at
exit: the session in it may have found the answer, and a per-launch home would reproduce the
incident by design; `yolo stores` lists it with the `rm -r` that removes it ([SD-D9](#SD-D9)).
The flag is refused while the same setup runs in the workspace, naming `yolo stop`. This flag, not a
user-defined setup, is the supported way to run without the workspace's state ([SD-D14](#SD-D14)).

### 3.7 What this design does not license

- It does not stop a human on the host from deleting `.yolo`; the README is for them too.
- It does not make `durable/` or `approvals/` recoverable after deletion.
- It does not move the homes out of the workspace ([§4](#4-alternatives-considered)).
- It does not add a `YOLO_ALLOW_*` hatch anywhere: nothing here refuses a launch.

## 4. Alternatives considered

| Alternative | Verdict |
| :--- | :--- |
| **Homes outside the workspace**, under `~/.local/share/yolo-jail/workspaces/<key>/`, with `.yolo` holding a pointer | **Rejected for this design.** It would remove the whole class on every setup, but it touches the storage design across all three backends, `yolo capture`'s rename path, the macos-user ruling that the home is the workspace's own, and portability: a project's agent state would stop traveling with the project. Worth its own design if the incident repeats after these layers |
| **Routine snapshots** of the homes | **Rejected.** Disk and I/O on every launch against an event made rare; non-atomic writers (Pi's `settings.json`, `auth.json`) make a timed snapshot a half-state |
| **A trash instead of a refusal** in the shim | **Rejected.** A move the agent did not ask for is a side effect; refuse and name the step |
| **Immutable file flags** | **Rejected** ([§3.3](#33-mechanical-guards-where-they-are-cheap-and-honest)) |
| **A Claude Code `PreToolUse` hook rendered by core** | **Rejected** ([§3.3](#33-mechanical-guards-where-they-are-cheap-and-honest)): one agent, same command the shim sees |
| **Refuse to boot on a mismatch** | **Rejected.** Stopping is what loses the bytes; a mismatch is a warning and a rescue, never a refusal |
| **A read-only mask over the homes' workspace-side path** | **Rejected** ([SD-D11](#SD-D11), [§3.3](#33-mechanical-guards-where-they-are-cheap-and-honest)) |
| **Rewrite the `.gitignore` unconditionally** | **Rejected** ([SD-D10](#SD-D10)): a user who un-ignored something would lose it at the next launch |

## 5. Open Questions

None open. The six questions were ruled on 2026-11-07 and are [SD-D10](#SD-D10) to
[SD-D15](#SD-D15).

## 6. Decision Ledger

SD-D1 to SD-D9 are the seat's one-answer decisions; SD-D10 to SD-D15 are the owner's rulings of 2026-11-07, compacted from the six open questions. The Built column names the commit that landed each row; SD-D15 is built for the logs; its locks stay at the top level until no jail an older yolo started can still take them there, a move for a later release.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="SD-D1"></a>SD-D1 | `.yolo/README.md` is written by `EnsureWorkspaceStateDir` and rewritten on every launch; its rows for the homes are rendered from the `HomeSurface` list, not hand-written | 2026-11-07 | [§3.1](#31-legibility-of-yolo-itself) | `5fd9229b5` |
| <a id="SD-D2"></a>SD-D2 | The per-workspace briefing line says the home dirs are stored under `/workspace/.yolo` and that the agent's own session is among them, rendered from the persistence map | 2026-11-07 | [§3.2](#32-the-briefing-and-the-skills) | `5fd9229b5` |
| <a id="SD-D3"></a>SD-D3 | The per-launch line says a file *moved* to `/tmp` is lost at the restart; macos-user's `/tmp` line says the same of the workspace's state | 2026-11-07 | [§3.2](#32-the-briefing-and-the-skills) | `5fd9229b5` |
| <a id="SD-D4"></a>SD-D4 | The guard refuses and names `--fresh-home`; it never redirects to a trash; `$YOLO_DURABLE_DIR` is exempt; paths are resolved by the parent's real path, never a link's target | 2026-11-07 | [§3.3](#33-mechanical-guards-where-they-are-cheap-and-honest) | `832332b65` |
| <a id="SD-D5"></a>SD-D5 | Detection is an identity stamp in the boot record, checked on session attach, at keeper stop and by `yolo check`; never a poll, never a refusal | 2026-11-07 | [§3.4](#34-detection) | `6e041847b` |
| <a id="SD-D6"></a>SD-D6 | No Claude Code `PreToolUse` hook from core; a pack may add one for its agent | 2026-11-07 | [§3.3](#33-mechanical-guards-where-they-are-cheap-and-honest) | — |
| <a id="SD-D7"></a>SD-D7 | No immutable file flags on any setup | 2026-11-07 | [§3.3](#33-mechanical-guards-where-they-are-cheap-and-honest) | — |
| <a id="SD-D8"></a>SD-D8 | `diagnosing-the-jail` gains a "ruling out the workspace's state" recipe: never move or copy `.yolo`; `--fresh-home` or a throwaway workspace | 2026-11-07 | [§3.2](#32-the-briefing-and-the-skills) | `5fd9229b5` |
| <a id="SD-D9"></a>SD-D9 | A rescue and a fresh home are listed by `yolo stores` with their size and the command that removes or restores them; no reaper touches either | 2026-11-07 | [§3.5](#35-recovery-and-survivability), [§3.6](#36-a-supported-way-to-do-what-the-agent-wanted) | `6e041847b` (rescue half), `22da313c7` (fresh-home half) |
| <a id="SD-D10"></a>SD-D10 | **Question 1, ruled B.** yolo rewrites a `.yolo/.gitignore` whose bytes match a text yolo has shipped (it keeps those hashes) to the current text; any other content is never touched. The promise protects a user's edit, and a byte-identical yolo text is not one | 2026-11-07 | [§3.1](#31-legibility-of-yolo-itself) | `5fd9229b5` |
| <a id="SD-D11"></a>SD-D11 | **Question 2, ruled B.** No mask over the homes' workspace-side path. The owner: it protects a small share of the harm (the same `mv` still deletes `durable/`, `approvals/` and `archive/`) and makes the home read empty from inside, a likely source of confusion | 2026-11-07 | [§3.3](#33-mechanical-guards-where-they-are-cheap-and-honest) | — |
| <a id="SD-D12"></a>SD-D12 | **Question 3, ruled A.** The `mv`/`rm` guard ships in the opt-in guardrails pack only; nothing is blocked by default | 2026-11-07 | [§3.3](#33-mechanical-guards-where-they-are-cheap-and-honest) | `832332b65` |
| <a id="SD-D13"></a>SD-D13 | **Question 4, ruled A.** The rescue runs automatically at keeper stop when the identity stamp mismatches; the stop takes as long as the copy and prints the rescue directory and the restoring `mv`. A slow stop beats a lost session | 2026-11-07 | [§3.5](#35-recovery-and-survivability) | `6e041847b` |
| <a id="SD-D14"></a>SD-D14 | **Question 5, ruled A.** `yolo --fresh-home` runs the selected setup on a new empty home at `.yolo/setups/<setup>/home.fresh-<stamp>`, kept afterwards and listed by `yolo stores` | 2026-11-07 | [§3.6](#36-a-supported-way-to-do-what-the-agent-wanted) | `22da313c7` |
| <a id="SD-D15"></a>SD-D15 | **Question 6, ruled A.** Project-level logs move under `.yolo/logs/`, locks and pids under `.yolo/run/`, in the named-setups slice; the top level becomes `setups/ durable/ approvals/ archive/ README.md` | 2026-11-07 | [§3.1](#31-legibility-of-yolo-itself) | ef6f83ebd (logs; locks stay until no older-yolo jail takes them, and no pid file exists for `run/`) |

## 7. Appendix: evidence

Verified 2026-11-07 at `024b59206`.

| Claim | Evidence |
| :--- | :--- |
| `.yolo` is "the per-workspace directory yolo owns inside a user's project (boot log, assembled config, the receipt log, the home overlay)" | [`paths.go:665-670`](../../internal/paths/paths.go) |
| The `.gitignore` text names `home/`, and the bare `*` is the mechanism keeping `launch.log`'s argv and `archive/` out of commits | [`paths.go:675-702`](../../internal/paths/paths.go) |
| An existing `.gitignore` is never touched; the write is best-effort | [`paths.go:713-718`](../../internal/paths/paths.go) |
| Per-setup home and run state under `.yolo/setups/<name>/`; approvals, grants and `durable` stay at the top | [`setupstate.go:13-15`](../../internal/paths/setupstate.go); [`named-setups.md` §9](named-setups.md#9-state-per-setup) |
| The home's workspace-side path shares a mount with `/workspace`, which `yolo capture` uses for `rename(2)` | [`paths.go:760-762`](../../internal/paths/paths.go); [`capture.go:76`](../../internal/cli/capture.go) |
| The briefing's per-workspace line lists the home dirs and never their location | [`persistencesection.go:153-155`](../../internal/jailcontent/persistencesection.go) |
| The briefing's `/tmp` line: "Throwaway files only, never a worktree" | [`persistencesection.go:100-102`](../../internal/jailcontent/persistencesection.go) |
| macos-user has no per-launch class; its `/tmp` line is the Mac's own | [`persistencesection.go:75-77`](../../internal/jailcontent/persistencesection.go) |
| Only the macos-user header ties the home to `.yolo` | [`briefing.go:350`](../../internal/jailcontent/briefing.go) |
| Per-launch scratch volumes are deleted after the jail exits by a detached remover | [`scratchremoval.go:15`](../../internal/cli/run/scratchremoval.go) |
| The keeper stops the jail when its last session is gone | [`jailmain.go:23`](../../internal/entrypoint/jailmain.go) |
| The guardrails pack generates `grep` and `find` blockers; nothing is blocked by default | [`guardrails/pack.toml:5-12`](../../packs/guardrails/pack.toml) |
| `YOLO_BYPASS_SHIMS=1` bypasses a shim | [`shims.go:122`](../../internal/entrypoint/shims.go) |
| `/tmp` stays per launch; yolo never deletes the agent's work | [`jail-home.md` DS-P3, DS-P4](../reference/jail-home.md#four-principles) |
| A linked `.yolo` is refused, with the relocation question open | [`jail-home.md` OQ-JH1](../reference/jail-home.md#OQ-JH1) |
| No `PreToolUse` hook is rendered by yolo | `rg -n PreToolUse packs/*/pack.toml internal/ --glob '!*_test.go'`: no match, 2026-11-07 |
| The incident's listing of this repo's `.yolo`, and the ten home binds from `.yolo/home/<name>` | the owner's account and `/proc/self/mountinfo`, 2026-11-07 |
