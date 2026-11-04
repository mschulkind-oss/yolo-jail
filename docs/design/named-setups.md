---
title: "Named setups: one name chooses every wall, and nothing follows from your OS"
date: 2026-11-03
status: draft
stage: DESIGN
next: "Rule OQ-SU1 (must a user setup write every field). Then OQ-MT5, OQ-SU2, OQ-MT7 and OQ-MT8."
depends-on:
  - macos-user-model.md#OQ-MM10
tags: [design, setups, confinement, macos-user, seatbelt, podman, naming]
summary: "Where an agent runs is chosen by naming a setup: a named, complete set of values for which technology walls the agent in (Podman, Apple Container, Seatbelt, Landlock or none), which account runs it (self or the yolo account), and its network. yolo ships built-in setups named by one rule, the field values joined with hyphens; a user may define more in user config; a project may name one but never define one; the command line picks one with --setup. No name changes meaning by OS; only the absent selection resolves per OS. Each setup keeps its own home under the project's .yolo, so two run side by side. confinement, runtime, macos_user_mode, network.mode, --at and --network are removed, each refused naming its replacement."
vantage:
  status-chip: true
---

# Named setups: one name chooses every wall

**Status:** 2026-11-03. Nothing built; this replaces the five-plan terminology draft (`eafad3a1c`)
with the owner's idea of the same day. Config keys checked against `yolo config-ref` at `14cc75609`.

> **In short.** The question "where does this agent run?" has one answer: the name of a setup, a
> complete set of technology-named values, so no value depends on another and none on your OS.

**Why it matters.** Today one concept has five names (`guest`, `macos-user`, account mode,
"the sandbox account", the second account), and the run-as choice lives in a key one backend
reads. A project that wants both "run as me" and "run as the yolo account" has to edit config to
switch.

**The shape.** Built-in setups and user-defined setups, both `[setups.<name>]` tables; one
selection, `setup = "<name>"` or `--setup <name>`; one home per setup.

**Cost.** `confinement`, `runtime`, `macos_user_mode`, `network.mode`, `--at`, `--network` and
`YOLO_RUNTIME` are removed and refused. A bare "container, whichever is
best here" can no longer be written; only leaving the selection unset still means that.

**Start at [§4](#4-the-fields)**, the fields; everything else falls out of them.

**Needs your ruling:** [OQ-SU1](#OQ-SU1), [OQ-MT5](#OQ-MT5), [OQ-SU2](#OQ-SU2), [OQ-MT7](#OQ-MT7), [OQ-MT8](#OQ-MT8).

**Reads with:** [the macos-user model](macos-user-model.md) (the two ways of running Seatbelt this
names), [its plan](macos-user-model-plan.md#MMP-D25) (slice 12a's per-project home), [the
charter's confinement dial](../reference/what-yolo-is.md#the-confinement-dial) (which this
replaces), [loophole grants](../reference/loophole-grants.md) (the allow and request model the
ceiling reuses).

## 1. The problem

Terms of art used throughout:

- **Walls**: whatever stops the agent reaching the rest of your machine: a container, a rule list
  the kernel enforces, a separate account, or nothing.

- **Podman**: a container runtime. On Linux it runs containers directly; on a Mac, inside one Linux
  virtual machine (the Podman Machine).
- **Apple Container**: Apple's container runtime for macOS 26 on Apple silicon, one small Linux
  virtual machine per container. Its CLI is `container`.
- **Seatbelt**: macOS's built-in confinement, which runs a program under a rule list of what it may
  touch (`sandbox-exec`). yolo's rule list denies by default.
- **Landlock**: the Linux kernel's rule list of what a process may touch; yolo's planned Linux
  equivalent applies it through **bwrap** (Bubblewrap, a tool that starts a process in new Linux
  namespaces). Not built.
- **The yolo account**: a separate OS user yolo creates once with an admin step, `_yolojail` on a
  Mac ([OQ-MT4](#OQ-MT4)). It holds none of your files.

What a user meets today ([Appendix A](#appendix-a-where-todays-words-came-from) has origins):

| Term today | What it means | Where it shows |
| :--- | :--- | :--- |
| `confinement = "jail" \| "guest" \| "host"`, `--at` | A three-notch dial; `guest` selects macos-user on a Mac and refuses on Linux | Config, flag, footer, [the guide](../../userguide/guides/confinement.md) |
| "Guest: a separate user account" | The guide's definition of `guest`, false when it runs as you | [Where Agents Run](../../userguide/guides/confinement.md#guest-in-development) |
| `runtime = "podman" \| "container" \| "macos-user"`, `YOLO_RUNTIME` | The backend; `container` is Apple Container | Config, env var, [settings per setup](../../userguide/reference/settings-per-setup.md) |
| `macos_user_mode = "account" \| "self"` | Which account macos-user runs as; user scope only | Config |
| "sandbox account", "second account", "hidden macOS user" | `_yolojail` | Setup output, launch lines, guides |
| `network.mode`, `--network` | Bridge (own loopback) or host (shared) networking; Seatbelt has only host | Config, flag |

Three faults follow: one word (`guest`) means three things; a value (`runtime`) means something only
beside one value of another key (`confinement = "jail"`); and switching how one project runs means
editing a file.

## 2. The idea

A **setup** *(the word is [settings per setup](../../userguide/reference/settings-per-setup.md)'s;
its precise sense here is coined in this doc)* is a named, complete set of values for every field
in [§4](#4-the-fields). It is **not** a partial override, a preset layered over other keys, or a
provider choice. Selecting one is the whole answer to "where does this agent run?"

- **Built-in setups** ship with yolo, one per buildable combination, named by one rule
  ([§5](#5-the-built-in-setups)).
- **User setups** are `[setups.<name>]` tables in your user config.
- **The selection** names one, resolved in this order, first match wins:

  1. `--setup <name>` on the command line;
  2. `YOLO_SETUP=<name>` in the environment;
  3. `setup = "<name>"` in the project's config, once approved ([§8](#8-the-security-ceiling));
  4. `setup = "<name>"` in your user config;
  5. **the platform default**: on Linux `podman-self`; on a Mac `apple-container-self` when the
     `container` CLI is installed, else `podman-self` when Podman is, else a refusal naming both
     installs. This is today's auto-detection, kept.

**What we lose, decided.** No setup name resolves per OS. `podman-self` is Podman on both systems
(directly on Linux, in the Podman Machine on a Mac), and `seatbelt-self` exists only on a Mac. The
one per-OS resolution is step 5, the absent selection, which already means "the strongest walls
installed here." A name that meant different technology on two machines would make a committed
`setup` line ambiguous, and `yolo describe` would print a different answer for the same file. A
user who wants one name everywhere leaves the selection unset; a project that runs differently per
OS is [OQ-SU2](#OQ-SU2).

Every launch line, the agent's footer and its briefing name the setup and spell its fields:

```text
yolo: setup seatbelt-self (Seatbelt, as you, host network) - from --setup
```

## 3. The noun

**`setup`**, chosen over the alternatives:

| Candidate | Verdict |
| :--- | :--- |
| **setup** | Chosen. [Settings per setup](../../userguide/reference/settings-per-setup.md) already calls a backend on a host OS a setup, and that table's columns become built-in setups. Short as a key, a flag (`--setup`) and a verb (`yolo setup list`). |
| profile | Refused by the owner, and taken twice: the Seatbelt rule list is a profile, and so is the provider `[[profile]]` yolo is retiring. |
| confinement | It is one field's name ([§4](#4-the-fields)); reusing it for the bundle would make `confinement = "podman-self"` and `confinement = "seatbelt"` mean different things in two tables. |
| containment | A near-synonym of the field name; two words that close in one config read as a typo. |
| sandbox | Avoided by ruling: it names the whole category, and today it names three different things in yolo's own text. |

The `yolo macos-setup` verb collides with the noun; [OQ-MT8](#OQ-MT8) renames it.

## 4. The fields

A setup has exactly three fields. Every value names a technology or an account, never a strength.

| Field | Values | On macOS | On Linux | Refused where |
| :--- | :--- | :--- | :--- | :--- |
| `confinement` | `podman` | Podman Machine: Linux containers in one VM | Rootless Podman containers | Where Podman is not installed |
| | `apple-container` | One small Linux VM per container | Does not exist | Linux; an Intel Mac; macOS before 26 |
| | `seatbelt` | Your real Mac under yolo's deny-by-default Seatbelt rule list; no image, no VM | Does not exist | Linux |
| | `landlock` | Does not exist | Planned: bwrap plus Landlock, no image | macOS; Linux until built, naming `podman` |
| | `host` | No walls: you, your Mac, your credentials | The same | Every selection except `yolo host` ([§8](#8-the-security-ceiling)) |
| `run_as` | `self` | The agent runs as you | The same | Nowhere |
| | `yolo-account` | As `_yolojail`, crossed to by a sudo rule `yolo macos-setup` installs | A Linux yolo account: not built ([MT-R2](#decision-ledger)) | With `podman` or `apple-container` until [OQ-MT7](#OQ-MT7); on Linux until built |
| `network` | `bridge` | Its own network namespace and loopback | The same | With `seatbelt` and `host`, which have no namespace of their own |
| | `host` | Shares the Mac's network stack | Shares the host's | With `apple-container`, which takes no network selector |

How to read it:

- **`confinement` names the technology that draws the walls.** Its values are the technologies
  themselves; `host` is the one value that names no technology, because none is used.
- **`run_as` names the account.** `self` is your own account; `yolo-account` is the yolo account. No
  value is a bare `account`, because running as yourself is an account too ([OQ-MT2](#OQ-MT2)).
- **`network` is a field because only the walls decide what it can be.** Seatbelt shares the Mac's
  stack whatever is written ([OQ-MM7](macos-user-model.md#OQ-MM7)); Apple Container runs bridged
  whatever is written. Today both cases warn or say nothing; as a field, a contradiction is refused
  when the setup is read, not discovered at launch.
- **The same check runs in `yolo check` and at launch**, from the setup alone: a pure function of
  the three values and the technology installed here. No other key changes its answer.

## 5. The built-in setups

**The naming rule.** A built-in setup's name is its `confinement` value and its `run_as` value
joined with a hyphen, in that order, spelled exactly as the values are. `network` takes the
technology's own default and is not in the name. So a name always decodes into its fields, and
there is exactly one built-in per (`confinement`, `run_as`) pair.

| Name | `confinement` | `run_as` | `network` | Platform | What it is for |
| :--- | :--- | :--- | :--- | :--- | :--- |
| `podman-self` | `podman` | `self` | `bridge` | Linux, macOS | The Linux default; on a Mac, the most features |
| `apple-container-self` | `apple-container` | `self` | `bridge` | macOS 26, Apple silicon | The Mac default |
| `seatbelt-self` | `seatbelt` | `self` | `host` | macOS | Your real Mac, as you, with no VM; fastest start |
| `seatbelt-yolo-account` | `seatbelt` | `yolo-account` | `host` | macOS | The same, as an account holding none of your files; today's macos-user default |
| `landlock-self` | `landlock` | `self` | its design's | Linux: not built | Reserved for the Linux equivalent of `seatbelt-self` |
| `landlock-yolo-account` | `landlock` | `yolo-account` | its design's | Linux: not built | Reserved ([MT-R2](#decision-ledger)) |
| `host-self` | `host` | `self` | `host` | Linux, macOS | `yolo host`'s default |
| `host-yolo-account` | `host` | `yolo-account` | `host` | macOS: not built; Linux: not built | `yolo host` as the yolo account, with no rule list |

A built-in marked not built is still a name: selecting it refuses, naming the nearest built one.
The Landlock rows' `network` and final spelling are owned by the Linux design when it is written.

## 6. Config, by example

A built-in, as if written out. These tables are what yolo holds; nobody writes them, and
`yolo setup show podman-self` prints exactly this:

```toml
[setups.podman-self]
confinement = "podman"
run_as = "self"
network = "bridge"
```

A user setup, in `~/.config/yolo-jail/config.toml`. Any name not a built-in's:

```toml
[setups.podman-shared-net]
confinement = "podman"
run_as = "self"
network = "host"   # this project's dev server must see the host's 127.0.0.1
```

A selection: in `yolo-jail.toml` for the project, shown at the config-change prompt like any
project edit ([§8](#8-the-security-ceiling)), or in your user config as your own default:

```toml
setup = "seatbelt-yolo-account"
```

Two side by side in one project, from two terminals:

```console
$ yolo --setup seatbelt-self -- claude          # terminal 1: as you
$ yolo --setup seatbelt-yolo-account -- claude  # terminal 2: as the yolo account, beside it
$ yolo setup list
seatbelt-self          running   Seatbelt, as you, host network         built-in
seatbelt-yolo-account  running   Seatbelt, as the yolo account, host    built-in, project's selection
podman-shared-net      -         Podman, as you, host network           yours
apple-container-self   -         Apple Container, as you, bridge        built-in, the default here
$ yolo stop                                      # stops both, naming each
$ yolo stop --setup seatbelt-self                # stops one
```

The command line, every spelling:

```console
$ yolo --setup podman-self -- claude
$ YOLO_SETUP=podman-self yolo -- claude
$ yolo host -- claude                            # host-self
$ yolo host --setup host-yolo-account -- claude  # refuses until built
$ yolo describe --setup seatbelt-self            # what that setup would give, written nowhere
$ yolo setup list | show <name>
$ yolo setup new <name> --from <setup>           # writes a full table into your user config
```

Refusals, each with its next step ([the Happy Path Principle](../reference/happy-path-principle.md#the-rules)):

```text
setup "seatbelt-self" uses Seatbelt, which only macOS has. On this machine:
    yolo --setup podman-self -- claude

[setups.podman-self] redefines a built-in setup. Give yours its own name:
    yolo setup new my-podman --from podman-self

yolo-jail.toml:4: setups.fast: a project may name a setup but not define one.
Define it in your user config instead:
    yolo setup new fast --from seatbelt-self

setup "host-self" puts no walls around the agent, and only `yolo host` runs one:
    yolo host -- claude

setups.lab: Seatbelt shares the Mac's network, so network = "bridge" cannot hold. Write:
    yolo config set setups.lab.network host --user

setups.lab: Apple Container takes no network selector, so network = "host" cannot hold. Write:
    yolo config set setups.lab.network bridge --user

setup "seatbelt-yolo-account" runs as the yolo account, which this Mac does not have yet.
Create it once (it asks for your admin password):
    yolo macos-setup

setup "landlock-self" is not built yet. The nearest built setup on Linux:
    yolo --setup podman-self -- claude

setup "fast" is not defined. Defined here: podman-self, seatbelt-self, ... (yolo setup list)
```

## 7. What else goes in, and what stays out

A field joins a setup only if **it is a property of the walls**: either the technology decides
what it can be, or choosing it changes what the agent can reach on your machine. Everything else
belongs to the project, the agent or you.

| Candidate (config key) | Verdict | Why |
| :--- | :--- | :--- |
| `confinement`, `runtime` | **In**, as `confinement` | The technology is the walls |
| `macos_user_mode` | **In**, as `run_as` | The account is the other half of who can reach what |
| `network.mode` | **In**, as `network` | The technology decides its legal values ([§4](#4-the-fields)) |
| `macos_user_shared_tmp` | Out for now | A hole in one technology's walls; a candidate field once a second technology has an equivalent |
| `YOLO_NO_HOST_LOOPBACK` | Out | An escape hatch for a broken host, not a choice of walls |
| `workspace_readonly`, `per_side_paths` | Out | About the repository's files, the same under any walls |
| `mounts`, `host_files`, `devices`, `gpu`, `kvm`, `machine.ports` | Out | Holes the project needs; already inert where a technology has no wall to hole |
| `resources` | Out | About the project's workload, not the walls |
| `[allow.*]`, `[requests.*]` (loophole and broker grants) | Out | Who may do what is the same under every technology ([parity](../reference/loophole-grants.md#7-parity-across-backends-and-notches)) |
| `active_provider`, `packs` | Out | About the agent, not where it runs |
| `share_host_fonts` | Out | A convenience copy; it follows from `run_as` already |
| Which home and state directory | Out, as a field | Decided per setup by name ([§9](#9-state-per-setup)) |
| Which yolo account | Out | There is one; a second would be a new `run_as` value, not a field |

## 8. The security ceiling

1. **Definitions are user scope only.** A `[setups.*]` table in a project file is refused
   ([§6](#6-config-by-example)): the workspace is writable by the agent, so a project defining
   `network = "host"` would be the agent loosening its own walls.
2. **Built-in names cannot be redefined.** `podman-self` means the same on every machine.
3. **A project's `setup` line is approved before it takes effect** *(pending
   [OQ-MT5](#OQ-MT5))*. It is a workspace edit, so the existing config-change prompt shows it; until
   approved the launch uses step 4 or 5 of [the selection](#2-the-idea) and says so.
4. **`host` confinement is never selected by config.** A `setup` naming a `host` setup, in either
   scope, is refused naming `yolo host`; `--setup` with one outside `yolo host` is refused the
   same way. The charter's rule that host is never inferred and never a fallback holds unchanged.
5. **The command line and the environment are yours.** `--setup` and `YOLO_SETUP` need no
   approval: the person typing the command is the authority the config-change prompt
   stands in for.
6. **Grants do not move.** A setup changes the walls, not what the project is granted; the one
   grant record per project serves every setup in it.

## 9. State per setup

**Each setup in a project has its own home and run state**, under
`<project>/.yolo/setups/<name>/`. Two setups never share a home:

- **Why per setup, not one shared `<project>/.yolo/home`.** Each technology lays its home out
  differently, and the shared one is already refused when Apple Container left its copy there
  ([MMP-D32](macos-user-model-plan.md#MMP-D32)). Two setups running at once would also write one
  home concurrently. Per name is the one rule that needs no compatibility judgment.
- **What is per setup:** the agent's home (logins it keeps there, transcripts), the copies
  `per_side_paths` keeps, the running jail's staged pack tree and its boot-time config baseline.
  This design fixes the split; the implementation plan lists the files.
- **What stays per project:** the config-change approval record, the grant record,
  `.yolo/durable`, the handoff file and `launch.log`, whose every line names its setup.
- **Running at once.** Two different setups in one project run side by side. The same setup twice
  behaves as its technology does today: a second `yolo` joins a running container; Seatbelt starts
  another session.
- **Machine-wide state is unchanged.** Shared login directories and caches are keyed by
  technology and account as today, so one login serves every setup that shares them.
- **Existing homes.** A project's `.yolo/home` from before this ships is left in place. The first
  launch of a setup whose layout yolo can identify there (Apple Container's header, as MMP-D32
  reads it) asks once to move it to that setup's home; otherwise it prints the one `mv` that adopts
  it. Nothing is deleted.

**Cost.** Changing setup in a project starts with that setup's own transcripts: `claude --resume`
under `seatbelt-self` does not see a conversation held under `podman-self`.

## 10. What is removed

A removed spelling is refused, never aliased, and the refusal names its replacement
([MT-R3](#decision-ledger)):

| Removed | Refused with |
| :--- | :--- |
| `confinement = "jail"` | *"`confinement` now lives in a setup. For a container: `setup = "podman-self"` (or `apple-container-self` on a Mac), or leave it unset for the default."* |
| `confinement = "guest"` | *"`guest` is now a setup: `setup = "seatbelt-yolo-account"` (as the yolo account) or `"seatbelt-self"` (as you)."* |
| `confinement = "host"` | *"Run on your own machine with `yolo host -- <cmd>`."* |
| `runtime = "podman"`, `"container"`, `"macos-user"` | *"`runtime` is now a setup: `setup = "podman-self"`"*, `"apple-container-self"`, or the two Seatbelt names |
| `YOLO_RUNTIME` | *"Use `YOLO_SETUP=<name>`"*, naming the setup the old value meant |
| `macos_user_mode` | *"Choose the account by setup: `seatbelt-self` or `seatbelt-yolo-account`."* |
| `network.mode` | *"Networking is part of a setup:"* `yolo setup new <name> --from <current>`, then set `network` |
| `--at <notch>` | *"Use `--setup <name>`"*; `--at host` names `yolo host` |
| `--network <mode>` | *"Define a setup with that network:"* `yolo setup new …` |
| Footer `guest`, "second account", "sandbox account" | Not refused (no input); replaced by the setup name and "the yolo account" |

A launch, `yolo check` and `yolo config migrate` all refuse the same way; `migrate` writes the
replacement where the old value names exactly one.

## 11. What done looks like

- `yolo setup list` shows every built-in and user setup, which are running in this project, and
  which the selection picks and why.
- Every launch's first yolo line names the setup and its three fields; the footer and briefing say
  the same.
- `yolo --setup seatbelt-self` and `yolo --setup seatbelt-yolo-account` run at once in one project,
  each with its own home under `.yolo/setups/`.
- Every removed spelling in [§10](#10-what-is-removed) refuses with its replacement, in `yolo check`
  and at launch.
- No code path reads a setup field to decide another field's meaning.

**Not in scope:** building Landlock, building the yolo account on Linux, building
`host-yolo-account`, or changing what any technology's walls allow. This design names them; their
own designs build them.

## 12. Alternatives considered

| Alternative | Verdict |
| :--- | :--- |
| Three independent keys (`confinement`, `runs_as`, `runtime`), the fourth draft's recommendation | Rejected: switching runs means editing files, and `runtime` meant something beside one `confinement` value only |
| The backend inside one value (`container:podman`) | Rejected: a mini-syntax nowhere else in config |
| The yolo account as a fourth notch | Rejected: no place for the yolo account with no rule list |
| One value per combination (`sandbox+yolo-account`) | Kept in spirit: a setup is that value, given a name and made definable |
| Names that resolve per OS (`container` meaning the best container here) | Rejected in [§2](#2-the-idea); only the absent selection resolves per OS |
| A user setup that overrides only some fields of a built-in | [OQ-SU1](#OQ-SU1) |

## 13. Open Questions

1. 💬 **OQ-SU1: When a new yolo release adds a setup field, what happens to my own setups?**

   Dana defined `podman-shared-net` in her user config, writing all three fields. A later release
   adds a fourth field, so her table no longer says every value, and the owner wants every value
   written, with nothing filled in behind it. That leaves a choice between completeness and her
   config surviving an upgrade.

   - **Every field, always.** Her launch refuses: *"setups.podman-shared-net has no `x`; add
     it: yolo setup fill podman-shared-net"*, which writes the new field's built-in value.
   - **Start from a built-in.** She writes `from = "podman-self"` plus only what differs; a new
     field arrives from the built-in and her table never breaks, but reading it means reading two.

   <!-- vantage: question id=OQ-SU1 leaning="Every field, always: the file says everything, and one refusal per new field, with a command that fills it, is cheap." -->

   _Leaning:_ Every field, always: the file says everything, and one refusal per new field, with a
   command that fills it, is cheap.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-MT5: May a project's committed config choose the setup?**

   A client repository wants its agent to run as the yolo account, so it commits
   `setup = "seatbelt-yolo-account"`. Today a project cannot choose the account at all: that key
   is read from user config only, because the agent can edit the project's files. Someone cloning
   the repository would otherwise get your own default.

   - **Yes, through the config-change prompt.** The clone's first launch shows the `setup` line in
     the diff and asks; a Mac without the yolo account refuses naming `yolo macos-setup`.
   - **User config only.** The repository's README says which `--setup` to use; the file is ignored.

   <!-- vantage: question id=OQ-MT5 leaning="Yes, through the prompt: a project that needs other walls should be able to say so, and the prompt shows the change before it applies." -->

   _Leaning:_ Yes, through the prompt: a project that needs other walls should be able to say so,
   and the prompt shows the change before it applies.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 **OQ-SU2: Can a project choose a different setup on a Mac and on Linux?**

   Sam's team commits `setup = "seatbelt-self"` because most of them use Macs. Sam runs Linux; his
   launch refuses, naming `podman-self`, every time, unless he passes `--setup`. No setup name
   means different things per OS, so the project has no way to say "Seatbelt on a Mac, Podman on
   Linux."

   - **A per-OS selection.** `setup = { macos = "seatbelt-self", linux = "podman-self" }`, explicit
     and shown in `yolo setup list`; a plain string still means every OS.
   - **One name only.** Sam keeps passing `--setup`, or sets `setup` in his own user config, which
     the project's line then outranks.

   <!-- vantage: question id=OQ-SU2 leaning="A per-OS selection: it keeps every name meaning one thing while letting a mixed team commit one line." -->

   _Leaning:_ A per-OS selection: it keeps every name meaning one thing while letting a mixed team
   commit one line.

   **Answer:**
   > _(empty — fill in when decided)_

4. 💬 **OQ-MT7: Should a container be able to run as the yolo account?**

   Lee wants even an escape from a Podman container to land in an account holding none of their
   files, so they define a setup with `confinement = "podman"` and `run_as = "yolo-account"`.
   Today that refuses as not built. Building it means a second rootless Podman store, or Apple
   Container under another user.

   - **Refuse it for now.** The refusal names `podman-self`; the pair is recorded as future work and
     no built-in name exists for it.
   - **Refuse it for good.** The refusal says a container already has its own users inside.

   <!-- vantage: question id=OQ-MT7 leaning="Refuse it for now: it is a real, stronger pairing, but nobody has asked for it." -->

   _Leaning:_ Refuse it for now: it is a real, stronger pairing, but nobody has asked for it.

   **Answer:**
   > _(empty — fill in when decided)_

5. 💬 🤷 **OQ-MT8: What are the yolo account's setup commands called, and what does "jail" mean now?**

   A new Mac user selects `seatbelt-yolo-account` and is told to run `yolo macos-setup`, a name
   that now reads like it sets up a setup, and Linux will need the same account one day. The guide
   still says "the jail" both for the product and for a container.

   - **Rename both.** `yolo yolo-account create`, `remove`, `share`, `unshare` on both systems;
     "jail" means the product only, and the guide says "a container" where it meant the walls.
   - **Keep the verbs.** `yolo macos-setup` and its three siblings stay; only the prose changes.

   <!-- vantage: question id=OQ-MT8 leaning="Rename both: each word then means one thing, and the verbs name what they make." -->

   _Leaning:_ Rename both: each word then means one thing, and the verbs name what they make.

   **Answer:**
   > _(empty — fill in when decided)_

## Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="SU-R1"></a>SU-R1 | **Where an agent runs is a named, complete set of values, selectable on the command line.** The owner: *"containment profiles, where you specify all of these things"*; *"we don't want to be shuffling config files. We need configurations that can be named on the command line."* Every value is in the named set, so *"there's no complex dependencies"*; yolo ships built-in sets under *"some consistent naming policy"*; users may define their own. Not called "profile"; "sandbox" is avoided; values name the technologies (Seatbelt, Landlock, Podman, Apple Container); the account is a field with `self` for running as you. | 2026-11-03 | [§2](#2-the-idea), [§4](#4-the-fields) | — |
| <a id="OQ-MT2"></a>[OQ-MT2](#OQ-MT2) | No value is called a bare `account`: running as you is an account too. The owner: *"I hate 'account' here because 'self' is also an account."* The separate user's value is `yolo-account`. | 2026-11-03 | [§4](#4-the-fields) | — |
| <a id="OQ-MT4"></a>[OQ-MT4](#OQ-MT4) | `_yolojail` is **the yolo account** in messages and docs. The owner: *"I like the YOLO account here … instead of account, just use YOLO account."* | 2026-11-03 | [§1](#1-the-problem) | — |
| MT-R1 | Superseded by [SU-R1](#SU-R1). It rejected naming by mechanism (a notch `sandboxed`, a runtime `seatbelt`) in a single dial; the owner's later idea names technologies as field values inside a setup, which is a different shape. | 2026-11-03 | — | — |
| MT-R2 | Every spelling is tested against a future Linux mode that runs the agent as a separate yolo user. The owner: *"what if we add a Linux guest user mode?"* Holds: `landlock-yolo-account` and `run_as = "yolo-account"` on Linux are reserved. | 2026-11-03 | [§5](#5-the-built-in-setups) | — |
| MT-R3 | **What a rename costs existing configs is not a criterion; current spellings may break.** The owner: *"are these costs migration from current? I don't care. we can break current."* A removed spelling is refused naming its replacement; no aliases. | 2026-11-03 | [§10](#10-what-is-removed) | — |
| OQ-MT1 | Superseded by [SU-R1](#SU-R1): no plan among the five; the backend is a `confinement` value in a named setup. | 2026-11-03 | [§12](#12-alternatives-considered) | — |
| OQ-MT3 | Superseded by [SU-R1](#SU-R1): `macos-user` is retired; the technology is named `seatbelt`, and `runtime = "macos-user"` is refused naming the two Seatbelt setups. | 2026-11-03 | [§10](#10-what-is-removed) | — |
| OQ-MT9 | Superseded by [SU-R1](#SU-R1): no value is `container` or `jail` (the technologies are named), and running as you is `self`, the owner's word: *"self spells better in a hyphenated thing."* | 2026-11-03 | [§4](#4-the-fields) | — |
| <a id="SU-D1"></a>SU-D1 | **The noun is `setup`.** A design decision: the user guide already calls a backend on a host OS a setup, and the other candidates are a field's name, a near-synonym of it, or refused. | 2026-11-03 | [§3](#3-the-noun) | — |
| <a id="SU-D2"></a>SU-D2 | **No setup name resolves per OS; only the absent selection does**, by today's auto-detection. A design decision answering the owner's *"I'm not clear that that's hugely important"*: a committed name must mean one thing on every machine. | 2026-11-03 | [§2](#2-the-idea) | — |
| <a id="SU-D3"></a>SU-D3 | **Three fields: `confinement`, `run_as`, `network`.** A field joins only if it is a property of the walls; the rest stays out ([§7](#7-what-else-goes-in-and-what-stays-out)). | 2026-11-03 | [§4](#4-the-fields) | — |
| <a id="SU-D4"></a>SU-D4 | **Built-in names are `<confinement>-<run_as>`**, values spelled exactly; one built-in per pair; built-ins cannot be redefined. | 2026-11-03 | [§5](#5-the-built-in-setups) | — |
| <a id="SU-D5"></a>SU-D5 | **Definitions are user scope only; `host` setups only through `yolo host`**; the command line needs no approval. | 2026-11-03 | [§8](#8-the-security-ceiling) | — |
| <a id="SU-D6"></a>SU-D6 | **One home and run state per setup**, under `<project>/.yolo/setups/<name>/`; approvals, grants and durable state stay per project; an existing `.yolo/home` is adopted on a yes or by a printed `mv`, never deleted. | 2026-11-03 | [§9](#9-state-per-setup) | — |

## Appendix A: where today's words came from

From `git log -S`. **`guest`**: `ce65b68dc` (2026-07-28), renamed from `sandbox` because that word
names the whole field ([the charter](../reference/what-yolo-is.md#why-guest-not-sandbox)).
**`macos-user`, "sandbox account", the setup verbs**: `4c85023fd` and `30f91946e` (2026-07-12),
when the backend only ran as a separate user. **`macos_user_mode = "account" | "self"`**: the
2026-10-22 prototype's `--mode self|account` (`5a57b9c42`), copied into config by
[MMP-D3](macos-user-model-plan.md#MMP-D3) with no ruling on the words; the model itself is
[OQ-MM1](macos-user-model.md#OQ-MM1). **"the second account"**:
[MM-R2](macos-user-model.md#decision-ledger). **"guest yolo", `GuestRealHome`**:
[MCWP-D10](macos-user-model-plan.md#MCWP-D10), the confined side's processes.
