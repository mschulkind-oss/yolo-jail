---
title: "Named setups: one name chooses every wall, and nothing follows from your OS"
date: 2026-11-03
status: in-review
stage: DESIGN
next: "Rule OQ-SU5 on safe handling of the existing legacy home; independent setup work can continue"
depends-on:
  - macos-user-model.md#OQ-MM10
tags: [design, setups, confinement, macos-user, seatbelt, podman, naming]
summary: "Where an agent runs is chosen by naming a setup: a named set of values, one per independent field, for which technology walls the agent in (Podman, Apple Container, Seatbelt, Landlock or none), and which account runs it (self or the yolo account). The network is not a field: it follows from the technology, and Podman's one real choice stays the project key network.mode. yolo ships built-in setups named by one rule, the field values joined with hyphens; a user may define more and choose one in user config, the command line picks one with --setup, and a project config that names or defines one is refused as invalid. No name changes meaning by OS; each OS has one named default setup. Each setup keeps its own home under the project's .yolo, so two run side by side. confinement, runtime, macos_user_mode, --at and YOLO_RUNTIME are removed, each refused naming its replacement."
vantage:
  status-chip: true
---

# Named setups: one name chooses every wall

**Status:** 2026-11-06. Setup naming and per-setup state remain the accepted direction, while
[OQ-SU5](#OQ-SU5) leaves legacy-home automatic adoption unresolved and [OQ-SU6](#OQ-SU6) leaves
the mixed-backend JSON contracts for both `yolo ps --format json` and `yolo stores --json` open.
A private 6a/6b prototype has focused synthetic-only closure of three held-bind findings; a separate
local rootful Linux Podman/crun probe establishes narrow bind-mount and descriptor-holder behavior.
Neither is accepted production source; the outside/foreign-pack path and legacy-home adoption reds
remain. See [plan Slice 6a](named-setups-plan.md#slice-6a--one-home-and-run-state-per-setup-after-4b-5-and-9-launch-path).

> **In short.** The question "where does this agent run?" has one answer: the name of a setup, a
> set of technology-named values in which no field's value depends on another's, and none on your OS.

**Why it matters.** Today one concept has five names (`guest`, `macos-user`, account mode,
"the sandbox account", the second account), and the run-as choice lives in a key one backend
reads. A project that wants both "run as me" and "run as the yolo account" has to edit config to
switch.

**The shape.** Built-in setups and user-defined setups, both `[setups.<name>]` tables; one
selection, `setup = "<name>"` or `--setup <name>`; one home per setup.

**Cost.** `confinement`, `runtime`, `macos_user_mode`, `--at` and `YOLO_RUNTIME` are removed and
refused. A bare "container, whichever is
installed here" can no longer be written; each OS has one default setup, which refuses when its
technology is missing.

**Start at [§4](#4-the-fields)**, the fields; everything else falls out of them.

**Needs your ruling:** [OQ-SU5](#OQ-SU5) — existing-home automatic movement only;
[OQ-SU6](#OQ-SU6) — mixed-backend JSON contracts for `yolo ps --format json` and
`yolo stores --json`.

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

The terminology this design replaces ([Appendix A](#appendix-a-where-todays-words-came-from) records its origins):

| Term / spelling | What it means | Where it shows |
| :--- | :--- | :--- |
| `confinement = "jail" \| "guest" \| "host"`, `--at` | A three-notch dial; `guest` selects macos-user on a Mac and refuses on Linux | Config, flag, footer, [the guide](../../userguide/guides/confinement.md) |
| "Guest" | The earlier guide used this for the in-development macOS `macos-user` setup, described as running under a separate account; the current guide distinguishes the two Seatbelt setups | [Seatbelt setups](../../userguide/guides/confinement.md#seatbelt-setups) |
| `runtime = "podman" \| "container" \| "macos-user"`, `YOLO_RUNTIME` | The backend; `container` is Apple Container | Config, env var, [settings per setup](../../userguide/reference/settings-per-setup.md) |
| `macos_user_mode = "account" \| "self"` | Which account macos-user runs as; user scope only | Config |
| "sandbox account", "second account", "hidden macOS user" | `_yolojail` | Setup output, launch lines, guides |
| `network.mode`, `--network` | Bridge (own loopback) or host (shared) networking; Seatbelt has only host | Config, flag |

Three faults follow: one word (`guest`) means three things; a value (`runtime`) means something only
beside one value of another key (`confinement = "jail"`); and switching how one project runs means
editing a file.

## 2. The idea

A **setup** *(the word is [settings per setup](../../userguide/reference/settings-per-setup.md)'s;
its precise sense here is coined in this doc)* is a named set of values, one for each of its two
fields ([§4](#4-the-fields)). A field it does not write takes **that field's own
default**, which never depends on another field's value. It is **not** an override of another
setup, a preset layered over other keys, or a provider choice. Selecting one is the whole answer to "where does this agent run?"

- **Built-in setups** ship with yolo, one per buildable combination, named by one rule
  ([§5](#5-the-built-in-setups)).
- **User setups** are `[setups.<name>]` tables in your user config.
- **The selection** names one, resolved in this order, first match wins:

  1. `--setup <name>` on the command line;
  2. `YOLO_SETUP=<name>` in the environment;
  3. `setup = "<name>"` in your user config (never a project's: [§8](#8-the-security-ceiling));
  4. **the default setup for this OS**, one named setup per OS and no probing: on Linux
     `podman-self`; on a Mac `apple-container-self`, today's Mac default. The Mac
     default becomes `seatbelt-self` when the owner says the Seatbelt path is ready, shipped with
     the macos-user plan's [slice 11 default flip](macos-user-model-plan.md#slice-11--the-rerun-gate-then-the-default-flips-after-6-to-10-and-12a-human-mac-run); slice 11 shipping does not
     move it by itself ([OQ-SU4](#OQ-SU4)). A default whose technology is not installed refuses, naming the
     install and the built-ins that are available here ([§6](#6-config-by-example)).

**What we lose, decided.** No setup name resolves per OS. `podman-self` is Podman on both systems
(directly on Linux, in the Podman Machine on a Mac), and `seatbelt-self` exists only on a Mac. The
one per-OS answer is step 4: each OS has a default setup, and an unset selection names it. A name that meant different technology on two machines would make a committed
`setup` line ambiguous, and `yolo describe` would print a different answer for the same file. A
user who wants one name everywhere leaves the selection unset; one who keeps a user config for a
Mac and a Linux machine selects per machine in a `config.d` fragment linked on that machine only.

Every launch line, the agent's footer and its briefing name the setup and spell its fields:

```text
yolo: setup seatbelt-self (Seatbelt, deny-by-default, as you; shares the Mac's network) - from --setup
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

The yolo account's verbs are `yolo yolo-account create`, `remove`, `share` and `unshare`, on both
systems ([OQ-MT8](#OQ-MT8)); the old `yolo macos-setup` collided with the noun. "Jail" names the
product and nothing else; where yolo's text meant the walls, it says "a container".

## 4. The fields

A setup has two fields, `confinement` and `run_as`, for every technology. The network is not one
of them ([SU-R3](#SU-R3)), and neither is a rule list ([SU-R4](#SU-R4)). Every value names a
technology or an account, never a strength.

| Field | Values | On macOS | On Linux | Refused where |
| :--- | :--- | :--- | :--- | :--- |
| `confinement` | `podman` | Podman Machine: Linux containers in one VM | Rootless Podman containers | Where Podman is not installed |
| | `apple-container` | One small Linux VM per container | Does not exist | Linux; an Intel Mac; macOS before 26 |
| | `seatbelt` | Your real Mac under yolo's deny-by-default Seatbelt rule list: Seatbelt denies everything, then allows the workspace, the agent's home, the tools and the system's read-only files ([§7 of the model](macos-user-model.md#7-the-ruled-model-two-modes-one-profile); `DenyDefaultProfile` in [`seatbeltdeny.go`](../../internal/macosuser/seatbeltdeny.go), built, not yet run on a Mac); no image, no VM | Does not exist | Linux |
| | `landlock` | Does not exist | Planned: bwrap plus Landlock, no image | macOS; Linux until built, naming `podman` |
| | `host` | No walls: you, your Mac, your credentials | The same | Every selection except `yolo host` ([§8](#8-the-security-ceiling)) |
| `run_as` | `self` | The agent runs as you | The same | Nowhere |
| | `yolo-account` | As `_yolojail`, crossed to by a sudo rule `yolo yolo-account create` installs | A Linux yolo account: not built ([MT-R2](#decision-ledger)) | With `podman` or `apple-container`: not built, refused naming `podman-self`, future work with no built-in name ([OQ-MT7](#OQ-MT7)); on Linux until built |

**Every field has one default of its own**, the same whatever the other fields say
([OQ-SU1](#OQ-SU1)):

| Field | Default | Why this one |
| :--- | :--- | :--- |
| `confinement` | `podman` | The one technology on both systems |
| `run_as` | `self` | Needs no admin step |

- **A default never bends to fit.** A default that followed `confinement` would be the dependency
  the rule forbids.
- **A field added in a later release** gets a default chosen so that every existing setup behaves
  exactly as before. A setup that wants something else writes the new field.

How to read it:

- **`confinement` names the technology that draws the walls.** Its values are the technologies
  themselves; `host` is the one value that names no technology, because none is used.
- **`run_as` names the account.** `self` is your own account; `yolo-account` is the yolo account. No
  value is a bare `account`, because running as yourself is an account too ([OQ-MT2](#OQ-MT2)).
- **Seatbelt has one rule list, deny-by-default** ([SU-R4](#SU-R4)). The older allow-by-default
  list, which needed a rule for every place a secret may live
  ([§1 of the model](macos-user-model.md#1-the-problem-in-one-example)), is not offered. Someone
  who wants fewer refusals than Seatbelt gives turns Seatbelt off and runs as the yolo account
  instead: `host-yolo-account`, no rule list, an account that holds none of your files. That
  setup is built by this design on macOS ([SU-R6](#SU-R6)), and runs only through `yolo host` ([§8](#8-the-security-ceiling)).
- **The network is not a field** ([SU-R3](#SU-R3)). Three technologies have exactly one network,
  and only Podman has a choice:

  | `confinement` | The network | `network.mode` (a project key, outside the setup) |
  | :--- | :--- | :--- |
  | `podman` | Bridged by default, or the host's | Honored: `bridge` or `host`. Podman inside a container still runs on the host's, as today |
  | `apple-container` | Always bridged: its own VM and loopback | `bridge` passes; `host` is refused |
  | `seatbelt` | Always the Mac's own network stack ([OQ-MM7](macos-user-model.md#OQ-MM7)) | `host` passes; `bridge` is refused |
  | `host` | Always the host's own | The same as `seatbelt` |
  | `landlock` | Its design's | Its design's |

  **A written value the technology cannot hold is refused, with its next step** ([SU-D8](#SU-D8));
  an unwritten one is that technology's network. Today Apple Container warns on `host` and
  Seatbelt says nothing on `bridge`; a project that asked for a network it does not get is told so
  before the agent starts. `--network` stays as that key's flag.
- **The same check runs in `yolo check` and at launch**, from the setup alone: a pure function of
  the setup's values and the technology installed here. No other key changes its answer.

## 5. The built-in setups

**The naming rule.** A built-in setup's name is `<confinement>-<run_as>`: its two field values,
joined with a hyphen. So a name always decodes into its fields, and there is exactly one built-in
per combination.

| Name | `confinement` | `run_as` | Platform | What it is for |
| :--- | :--- | :--- | :--- | :--- |
| `podman-self` | `podman` | `self` | Linux, macOS | The Linux default; on a Mac, the most features |
| `apple-container-self` | `apple-container` | `self` | macOS 26, Apple silicon | The Mac default |
| `seatbelt-self` | `seatbelt` | `self` | macOS | Your real Mac, as you, with no VM; the ruled model's default way to run Seatbelt |
| `seatbelt-yolo-account` | `seatbelt` | `yolo-account` | macOS | The same, as an account holding none of your files; what `macos_user_mode = "account"` runs since slice 12a |
| `landlock-self` | `landlock` | `self` | Linux: not built | Reserved for the Linux equivalent of `seatbelt-self` |
| `landlock-yolo-account` | `landlock` | `yolo-account` | Linux: not built | Reserved ([MT-R2](#decision-ledger)) |
| `host-self` | `host` | `self` | Linux, macOS | `yolo host`'s default |
| `host-yolo-account` | `host` | `yolo-account` | macOS: built here ([SU-R6](#SU-R6)); Linux: not built | `yolo host` as the yolo account, with no rule list: the looser choice for anyone Seatbelt refuses too often |

A built-in marked not built is still a name: selecting it refuses, naming the nearest built one.
Each built-in's network is its technology's ([§4](#4-the-fields)): Podman's follows
`network.mode`, Apple Container's is bridged, and Seatbelt's and host's are the machine's own.
The Landlock rows' network and final spelling are owned by the Linux design when it is written.

## 6. Config, by example

A built-in, as if written out. These tables are what yolo holds; nobody writes them, and
`yolo setup show podman-self` prints exactly this:

```toml
[setups.podman-self]
confinement = "podman"
run_as = "self"

[setups.seatbelt-yolo-account]
confinement = "seatbelt"
run_as = "yolo-account"
```

A user setup, in `~/.config/yolo-jail/config.toml`. Any name not a built-in's:

```toml
[setups.mac-as-me]
confinement = "seatbelt"
# run_as is not written, so it is its default, self
```

A project whose dev server must see the host's 127.0.0.1 says so in its own config, not in a setup.
Podman honors it; Seatbelt already shares the Mac's network; Apple Container refuses it:

```toml
# yolo-jail.toml
[network]
mode = "host"
```

Your own default, in your user config (`yolo config set setup seatbelt-yolo-account --user`
writes it). A project config cannot hold this line ([§8](#8-the-security-ceiling)):

```toml
setup = "seatbelt-yolo-account"
```

Two side by side in one project, from two terminals:

```console
$ yolo --setup seatbelt-self -- claude          # terminal 1: as you
$ yolo --setup seatbelt-yolo-account -- claude  # terminal 2: as the yolo account, beside it
$ yolo setup list
seatbelt-self               running  Seatbelt, as you                             built-in
seatbelt-yolo-account       running  Seatbelt, as the yolo account                built-in, your default
mac-as-me                   -        Seatbelt, as you                             yours
apple-container-self        -        Apple Container, as you                      built-in, this OS's default
$ yolo stop                                           # stops both, naming each
$ yolo stop --setup seatbelt-self                # stops one
```

The command line, every spelling:

```console
$ yolo --setup podman-self -- claude
$ YOLO_SETUP=podman-self yolo -- claude
$ yolo host -- claude                            # host-self
$ yolo host --setup host-yolo-account -- claude  # the yolo account, no Seatbelt
$ yolo describe --setup seatbelt-self       # what that setup would give, written nowhere
$ yolo setup list | show <name>
$ yolo setup new <name> --from <setup>           # copies that setup's values into a new table
```

Refusals, each with its next step ([the Happy Path Principle](../reference/happy-path-principle.md#the-rules)):

```text
setup "seatbelt-self" uses Seatbelt, which only macOS has. On this machine:
    yolo --setup podman-self -- claude

[setups.podman-self] redefines a built-in setup. Give yours its own name:
    yolo setup new my-podman --from podman-self

yolo-jail.toml:4: setups.fast: a project cannot define a setup; setups are yours.
Define it in your user config instead:
    yolo setup new fast --from seatbelt-self

yolo-jail.toml:3: setup: a project cannot choose where its agent runs; that is yours to choose.
For this launch:       yolo --setup seatbelt-yolo-account -- claude
As your own default:   yolo config set setup seatbelt-yolo-account --user
Then remove the line:  yolo config unset setup --project

setups.lab: Podman cannot run as the yolo account yet. Run it as you:
    yolo config set setups.lab.run_as self --user

setup "host-self" puts no walls around the agent, and only `yolo host` runs one:
    yolo host -- claude

yolo-jail.toml:7: network.mode = "host": setup apple-container-self always runs bridged, so the
jail cannot share this machine's network. For this launch, a setup that can:
    yolo --setup podman-self -- claude
Or, if the project does not need it:  yolo config unset network.mode --project

yolo-jail.toml:7: network.mode = "bridge": setup seatbelt-self always shares the Mac's network,
so the agent cannot have a loopback of its own. For this launch, a setup that can:
    yolo --setup apple-container-self -- claude
Or, if the project does not need it:  yolo config unset network.mode --project

setup "seatbelt-yolo-account" runs as the yolo account, which this Mac does not have yet.
Create it once (it asks for your admin password):
    yolo yolo-account create

setups.lab: `policy` is not a setup field; Seatbelt has one rule list. Remove it:
    yolo config unset setups.lab.policy --user
For fewer refusals, run as the yolo account with no Seatbelt:
    yolo host --setup host-yolo-account -- claude

The default setup on this Mac, apple-container-self, needs Apple Container, which is not
installed. Install it (https://github.com/apple/container), or choose an installed setup:
    yolo --setup podman-self -- claude          (yolo setup list shows the rest)

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
| Seatbelt's rule list (`seatbeltProfile` or `DenyDefaultProfile`) | **Out** ([SU-R4](#SU-R4)) | One list is offered, deny-by-default; a looser choice is `host-yolo-account`, not a second list |
| `network.mode`, `--network` | **Out**, stays the project's key ([SU-R3](#SU-R3)) | Three technologies have one network each, so as a field it only added impossible combinations; Podman's choice is the project's need (a dev server on the host's loopback), the same under any account |
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

1. **Setups are user scope only, defined and chosen.** A `[setups.*]` table or a `setup` line in a
   project file (`yolo-jail.toml` or `yolo-jail.local.toml`) is an invalid key there: `yolo check`
   reports it as an error and a launch refuses, naming `--setup`, `yolo config set setup <name>
   --user` and `yolo config unset setup --project` ([§6](#6-config-by-example)). It is never
   silently ignored. The workspace is writable by the agent, so a project line would be the agent
   choosing its own walls ([OQ-MT5](#OQ-MT5)).
2. **Built-in names cannot be redefined.** `podman-self` means the same on every machine.
3. **`host` confinement is never selected by config.** A `setup` line naming a `host` setup is
   refused naming `yolo host`; `--setup` with one outside `yolo host` is refused the
   same way. The charter's rule that host is never inferred and never a fallback holds unchanged.
4. **The command line and the environment are yours.** `--setup` and `YOLO_SETUP` need no
   approval: the person typing the command is the authority the config-change prompt
   stands in for.
5. **Grants do not move.** A setup changes the walls, not what the project is granted; the one
   grant record per project serves every setup in it.

## 9. State per setup

**Each setup in a project has its own home and run state**, under
`<project>/.yolo/setups/<name>/`. Two setups never share a home:

- **Why per setup, not one shared `<project>/.yolo/home`.** Each technology lays its home out
  differently, and the shared one is already refused when Apple Container left its copy there
  ([MMP-D32](macos-user-model-plan.md#MMP-D32)). Two setups running at once would also write one
  home concurrently. Per name is the one rule that needs no compatibility judgment.
- **What is per setup:** the agent's home (logins it keeps there, transcripts), the copies
  `per_side_paths` keeps, the running setup's staged pack tree and its boot-time config baseline.
  This design fixes the split; the implementation plan lists the files.
- **What stays per project:** the config-change approval record, the grant record,
  `.yolo/durable`, the handoff file and `launch.log`, whose every line names its setup.
- **Running at once.** Two different setups in one project run side by side. The same setup twice
  behaves as its technology does today: a second `yolo` joins a running container; Seatbelt starts
  another session.
- **Machine-wide state is unchanged.** Shared login directories and caches are keyed by
  technology and account as today, so one login serves every setup that shares them.
- **Existing homes.** [SU-D6](#SU-D6) accepted asking on a first launch to move an identifiable
  `.yolo/home` to the selected setup's home, and printing a manual move otherwise. The
  automatic yes branch is **not safe to land as currently implemented**: another writer of
  the workspace's writable `.yolo` can replace the source name after identification but before
  the rename. The Linux source-swap regression reaches the real destination-exclusive rename:
  it moves the replacement home while the identified home stays behind. Destination exclusion
  does not check the source identity. An open source directory handle or a yolo-only lock does
  not stop another writer changing the name; checking after the rename does not undo a transient
  move of the wrong private data or guarantee safe restoration. The analogous Darwin rename is
  destination-exclusive too, but has **not** been exercised on a Mac for this race. This is a
  demonstrated failure of the candidate, not proof that no safe kernel mechanism can exist.
  [OQ-SU5](#OQ-SU5) asks whether to revise only this accepted automatic-move requirement or
  retain it until an identity-safe mechanism is vetted. Until then, preserve the legacy home;
  do not silently turn the automatic yes into a manual-only flow. The source candidate is not
  landed, and the ordinary per-setup home split remains the direction.

**Implementation evidence is separate from this design.** Main still lacks slices 6a/6b. A
private prototype's focused review closes only three synthetic-source findings: strict phases bind
the complete selection and actual spawned process; the synthetic receiver validates descriptor
roles and kinds before ownership, requiring the lock at FD5 while preserving valid reserved sockets
at FD6/7; and withdrawal waits for operations holding real duplicated file descriptors before
acknowledgment. A file descriptor (FD) here means an open kernel handle, not a pathname.

A separate local nested Linux Podman 6.1.4/crun probe (`rootless=false`, `remote=false`) used
production-style bind-mount `-v` with owned dummy containers. In those cells the engine resolved
the source at START: a live directory-FD path mounted the original writable tree across a pathname
swap, while path-only and same-process descriptor-slot-reuse controls mounted the replacement; an
absent descriptor made START fail. A real Go `ExtraFiles` holder kept the original writable bind
usable after its producer exited; after holder death, a separate created-but-not-started container
failed at START. This does not determine what happens to an already-started bind after holder
death or prove production keeper/lease integration, all admission paths, rootless or remote Podman,
Apple Container, macOS or Seatbelt behavior. The outside/foreign-pack pathname red is separate from the
legacy-home source-swap red governed by [OQ-SU5](#OQ-SU5). Neither the source prototype nor this
probe authorizes activation; see [plan Slice 6a](named-setups-plan.md#slice-6a--one-home-and-run-state-per-setup-after-4b-5-and-9-launch-path)
for the remaining proof boundary.

**Cost.** Changing setup in a project starts with that setup's own transcripts: `claude --resume`
under `seatbelt-self` does not see a conversation held under `podman-self`.

## 10. What is removed

A removed spelling is refused, never aliased, and the refusal names its replacement
([MT-R3](#decision-ledger)):

| Removed | Refused with |
| :--- | :--- |
| `confinement = "jail"` | *"`confinement` now lives in a setup. For a container: `yolo config set setup podman-self --user` (or `apple-container-self` on a Mac), or leave it unset for the default."* In a project file, also `yolo config unset confinement --project` |
| `confinement = "guest"` | *"`guest` is now a setup: `yolo config set setup seatbelt-yolo-account --user` (as the yolo account) or `seatbelt-self` (as you)."* |
| `confinement = "host"` | *"Run on your own machine with `yolo host -- <cmd>`."* |
| `runtime = "podman"`, `"container"`, `"macos-user"` | *"`runtime` is now a setup: `yolo config set setup podman-self --user`"*, `"apple-container-self"`, or the Seatbelt names |
| `YOLO_RUNTIME` | *"Use `YOLO_SETUP=<name>`"*, naming the setup the old value meant |
| `macos_user_mode` | *"Choose the account by setup: `seatbelt-self` or `seatbelt-yolo-account`."* |
| `--at <notch>` | *"Use `--setup <name>`"*; `--at host` names `yolo host` |
| `yolo macos-setup`, `macos-teardown`, `macos-fix-permissions`, `macos-unshare` | *"Now `yolo yolo-account create`"*, `remove`, `share` (the old fix-permissions) and `unshare` |
| Footer `guest`, "second account", "sandbox account", "jail" for the walls | Not refused (no input); replaced by the setup name, "the yolo account" and "a container" |

A launch, `yolo check` and `yolo config migrate` all refuse the same way; `migrate` writes the
replacement where the old value names exactly one.

## 11. What done looks like

- `yolo setup list` shows every built-in and user setup, which are running in this project, and
  which the selection picks and why.
- Every launch's first yolo line names the setup and its fields; the footer and briefing say
  the same.
- `yolo --setup seatbelt-self` and `yolo --setup seatbelt-yolo-account` run at once in one project,
  each with its own home under `.yolo/setups/`.
- Every removed spelling in [§10](#10-what-is-removed) refuses with its replacement, in `yolo check`
  and at launch.
- No code path reads one setup field to decide another's meaning or default.

**Not in scope:** building Landlock, building the yolo account on Linux, building
`host-yolo-account` on Linux or a container run as the yolo account, or changing what any technology's walls allow. This design names them; their
own designs build them.

## 12. Alternatives considered

| Alternative | Verdict |
| :--- | :--- |
| Three independent keys (`confinement`, `runs_as`, `runtime`), the fourth draft's recommendation | Rejected: switching runs means editing files, and `runtime` meant something beside one `confinement` value only |
| The backend inside one value (`container:podman`) | Rejected: a mini-syntax nowhere else in config |
| The yolo account as a fourth notch | Rejected: no place for the yolo account with no rule list |
| One value per combination (`sandbox+yolo-account`) | Kept in spirit: a setup is that value, given a name and made definable |
| Names that resolve per OS (`container` meaning the best container here), or a probing default | Rejected in [§2](#2-the-idea); each OS has one named default setup |
| A user setup that inherits from another (`from = "podman-self"`), or must write every field | Rejected by [OQ-SU1](#OQ-SU1): inheriting makes one setup's meaning depend on another's, and writing every field breaks every user setup when a field is added; per-field defaults do neither |

## 13. Open Questions

1. 💬 **OQ-SU5: Should an existing home require an owner-managed move instead of automatic adoption?**

   The accepted first-launch prompt would move an identifiable `.yolo/home` after a yes.
   [§9](#9-state-per-setup) shows why the current rename can move a different home. This
   affects only existing-home movement, not fresh per-setup homes.

   - **A — Revise the automatic-move requirement.** Decline automatic adoption and give
     instructions for an owner-quiesced manual move, after all writers of that workspace state
     are stopped. Do not present a command that assumes an unchecked destination is safe.
   - **B — Keep the accepted automatic-move requirement.** Hold that portion unlanded until
     a source-identity-safe mechanism is demonstrated and reviewed on the relevant platforms.

   <!-- vantage: question id=OQ-SU5 leaning="A — revise only optional automatic legacy-home adoption to require an owner-quiesced manual move; keep the home and fresh per-setup behavior unchanged." -->

   _Leaning:_ A — revise only optional automatic legacy-home adoption to require an
   owner-quiesced manual move. It avoids a demonstrated wrong-home move; this is a proposal,
   not a new ruling or an implemented fallback.

   **Answer:**

   > _(empty — fill in when decided)_

2. 💬 **OQ-SU6: What mixed-backend JSON contract should `yolo ps --format json` and `yolo stores --json` expose?**

   [`psReport`](../../internal/cli/ps.go) has root `runtime`, `enumerated`, `jails`.
   Literal `yolo stores --json` dispatches through [`runStores`](../../internal/cli/commands.go),
   [`ParseArgs`](../../internal/cli/stores/stores.go) and [`Run`](../../internal/cli/stores/stores.go)
   to [`Report`](../../internal/cli/stores/inventory.go), with root `runtime` (omitempty) and
   `stores`, but no `enumerated`.

   - **A — Add per-backend coverage** to both, preserving homogeneous legacy fields. Mixed results
     must not invent one runtime; root `runtime` omission and `ps` completeness semantics are
     proposals, not rulings.
   - **B — Replace both root summaries** with per-backend envelopes, changing or migrating existing
     consumers.

   Resolve per-row backend provenance and reclaimer routing without duplicating machine, account,
   cache, or project-durable stores. Query only host-supported backends with configured or tracked
   setups; an unanswered backend is not empty. Preserve conservative pruning and existing project
   keys.

   <!-- vantage: question id=OQ-SU6 leaning="A — add per-backend coverage to both commands while preserving homogeneous legacy fields; do not invent one mixed runtime; omission and completeness semantics remain unruled." -->

   _Leaning:_ A — add per-backend coverage to both commands while preserving homogeneous legacy
   fields. Never invent one mixed runtime; root `runtime` omission and `ps` completeness semantics
   remain unruled. This proposal is not an owner-approved contract.

   **Answer:**

   > _(empty — fill in when decided)_

## Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="SU-R1"></a>SU-R1 | **Where an agent runs is a named set of values, selectable on the command line.** The owner: *"containment profiles, where you specify all of these things"*; *"we don't want to be shuffling config files. We need configurations that can be named on the command line."* No field's value is derived from another's, so *"there's no complex dependencies"*, and each field has its own default ([OQ-SU1](#OQ-SU1)); yolo ships built-in sets under *"some consistent naming policy"*; users may define their own. Not called "profile"; "sandbox" is avoided; values name the technologies (Seatbelt, Landlock, Podman, Apple Container); the account is a field with `self` for running as you. | 2026-11-03 | [§2](#2-the-idea), [§4](#4-the-fields) | — |
| <a id="OQ-SU1"></a>[OQ-SU1](#OQ-SU1) | **Every field has its own default, independent of every other field; a user setup may leave any field out and gets that default; a field added later gets a default that keeps every existing setup's behavior.** No `from =` inheritance and no fill command. A default that contradicts a setup's `confinement` is refused naming the value to write, never adjusted. The owner: *"I meant there aren't dependent fields, like filling in one field doesn't fill in the other, but we can have defaults for fields. So if we add a fourth and a fifth, those are going to be some defaults, and you can update your config if you need them differently. And we'll be careful, obviously, to not change behavior with the defaults if we add a new field."* *Its network part (a `bridge` default refused under Seatbelt) is superseded by [SU-R3](#SU-R3): no setup has a network field.* | 2026-11-03 | [§4](#4-the-fields) | — |
| <a id="SU-R3"></a>SU-R3 | **The network is not a setup field.** Seatbelt and host always share the host's network, Apple Container is always bridged, and only Podman has a real choice, which the project key `network.mode` (and its `--network` flag) keeps covering, outside the setup. A setup has three fields, `confinement`, `run_as` and `policy`, and no network combination is impossible. The owner, accepting that proposal: *"yes I like that solution for network."* | 2026-11-03 | [§4](#4-the-fields), [§7](#7-what-else-goes-in-and-what-stays-out) | — |
| <a id="SU-R4"></a>SU-R4 | **Seatbelt runs only yolo's deny-by-default rule list; allow-by-default is dropped, and with it the `policy` field.** A setup has two fields, `confinement` and `run_as`; the Seatbelt built-ins are `seatbelt-self` and `seatbelt-yolo-account`. Anyone who wanted allow-by-default's looseness runs `host-yolo-account` instead: no Seatbelt, as the yolo account (through `yolo host` only, by [SU-D5](#SU-D5); built on macOS by [SU-R6](#SU-R6)). The macos-user plan's slice 13, which deletes the allow-by-default profile (`seatbeltProfile` in [`seatbelt.go`](../../internal/macosuser/seatbelt.go)), stands again. Landlock gets no reserved `policy` either: it denies by default by construction, so there is nothing to choose. The owner: *"ok, so drop default allow, and if someone wants they can just turn seatbelt off totally and use the yolo-account."* | 2026-11-03 | [§4](#4-the-fields), [§5](#5-the-built-in-setups) | — |
| <a id="OQ-SU3"></a>[OQ-SU3](#OQ-SU3) | **Moot: allow-default dropped** by [SU-R4](#SU-R4), so there is no rule list to pair with an account. The owner: *"drop default allow."* | 2026-11-03 | [§5](#5-the-built-in-setups) | — |
| <a id="SU-R5"></a>SU-R5 | **A written `network.mode` the setup's technology cannot hold is refused**, confirming [SU-D8](#SU-D8). The owner: *"correct on refusing wrong network mode."* | 2026-11-03 | [§4](#4-the-fields), [§6](#6-config-by-example) | — |
| <a id="OQ-SU4"></a>[OQ-SU4](#OQ-SU4) | **The Mac default setup is `apple-container-self` for now; it becomes `seatbelt-self` when the owner says the Seatbelt path is ready.** The switch ships with the macos-user plan's [slice 11 default flip](macos-user-model-plan.md#slice-11--the-rerun-gate-then-the-default-flips-after-6-to-10-and-12a-human-mac-run), but the trigger is the owner's call, not slice 11 shipping by itself. The owner: *"for now apple-container-self, but we will switch to seatbelt-self when we're ready."* | 2026-11-03 | [§2](#2-the-idea) | — |
| <a id="OQ-MT5"></a>[OQ-MT5](#OQ-MT5) | **A project config may not choose a setup; `setup` and `[setups.*]` are user scope only, and in a project file they are an error, not ignored.** The owner: *"This should be user config only. Not only is that whatever ignored in a workspace and config, it's an error because it's an invalid thing to set."* | 2026-11-03 | [§8](#8-the-security-ceiling) | — |
| <a id="OQ-SU2"></a>[OQ-SU2](#OQ-SU2) | **Moot under [OQ-MT5](#OQ-MT5):** a project cannot choose a setup, so it cannot choose one per OS. One user config used on a Mac and on Linux selects per machine with a `config.d` fragment, which needs no new form. | 2026-11-03 | [§2](#2-the-idea) | — |
| <a id="OQ-MT7"></a>[OQ-MT7](#OQ-MT7) | **A container run as the yolo account is refused for now**, naming `podman-self`, and recorded as future work with no built-in name. The owner took the leaning: *"Refuse it for now: it is a real, stronger pairing, but nobody has asked for it."* | 2026-11-03 | [§4](#4-the-fields) | — |
| <a id="OQ-MT8"></a>[OQ-MT8](#OQ-MT8) | **The verbs are renamed and "jail" means the product only.** `yolo yolo-account create`, `remove`, `share`, `unshare` on both systems; the four `macos-*` verbs are refused naming them; the guide says "a container" where it meant the walls. The owner took the leaning: *"Rename both: each word then means one thing, and the verbs name what they make."* | 2026-11-03 | [§3](#3-the-noun), [§10](#10-what-is-removed) | — |
| <a id="SU-R2"></a>SU-R2 | **Seatbelt's deny-by-default and allow-by-default rule lists are both choices, paired with both accounts; and an unset setup is one default setup per OS.** The owner: *"for seatbelt, we'll want the default allow and the default deny policies as choices, pair with self and yolo as we already have them with the default setups. leaving the setup unset auto-detects means we just have a default setup by OS I guess."* Which pairings ship is [OQ-SU3](#OQ-SU3). *Its allow-by-default part is superseded by [SU-R4](#SU-R4); the default setup per OS holds.* | 2026-11-03 | [§4](#4-the-fields), [§5](#5-the-built-in-setups), [§2](#2-the-idea) | — |
| <a id="OQ-MT2"></a>[OQ-MT2](#OQ-MT2) | No value is called a bare `account`: running as you is an account too. The owner: *"I hate 'account' here because 'self' is also an account."* The separate user's value is `yolo-account`. | 2026-11-03 | [§4](#4-the-fields) | — |
| <a id="OQ-MT4"></a>[OQ-MT4](#OQ-MT4) | `_yolojail` is **the yolo account** in messages and docs. The owner: *"I like the YOLO account here … instead of account, just use YOLO account."* | 2026-11-03 | [§1](#1-the-problem) | — |
| MT-R1 | Superseded by [SU-R1](#SU-R1). It rejected naming by mechanism (a notch `sandboxed`, a runtime `seatbelt`) in a single dial; the owner's later idea names technologies as field values inside a setup, which is a different shape. | 2026-11-03 | — | — |
| MT-R2 | Every spelling is tested against a future Linux mode that runs the agent as a separate yolo user. The owner: *"what if we add a Linux guest user mode?"* Holds: `landlock-yolo-account` and `run_as = "yolo-account"` on Linux are reserved. | 2026-11-03 | [§5](#5-the-built-in-setups) | — |
| MT-R3 | **What a rename costs existing configs is not a criterion; current spellings may break.** The owner: *"are these costs migration from current? I don't care. we can break current."* A removed spelling is refused naming its replacement; no aliases. | 2026-11-03 | [§10](#10-what-is-removed) | — |
| OQ-MT1 | Superseded by [SU-R1](#SU-R1): no plan among the five; the backend is a `confinement` value in a named setup. | 2026-11-03 | [§12](#12-alternatives-considered) | — |
| OQ-MT3 | Superseded by [SU-R1](#SU-R1): `macos-user` is retired; the technology is named `seatbelt`, and `runtime = "macos-user"` is refused naming the two Seatbelt setups. | 2026-11-03 | [§10](#10-what-is-removed) | — |
| OQ-MT9 | Superseded by [SU-R1](#SU-R1): no value is `container` or `jail` (the technologies are named), and running as you is `self`, the owner's word: *"self spells better in a hyphenated thing."* | 2026-11-03 | [§4](#4-the-fields) | — |
| <a id="SU-R6"></a>SU-R6 | **`host-yolo-account` is built on macOS by this design**, under that name: no Seatbelt, run as the yolo account, through `yolo host` only ([SU-D5](#SU-D5)). It trades two exposures for fewer refusals: every workspace can read and write every other (all run as the one account), and the account can read whatever on the Mac is world-readable; your keychain and your processes' arguments and environments stay out of reach by the account boundary alone. Linux waits on the yolo account there. The owner: *"yes, let's include it, and host-yolo-account is the right name for it."* | 2026-11-03 | [§5](#5-the-built-in-setups), [§8](#8-the-security-ceiling) | — |
| <a id="SU-D1"></a>SU-D1 | **The noun is `setup`.** A design decision: the user guide already calls a backend on a host OS a setup, and the other candidates are a field's name, a near-synonym of it, or refused. | 2026-11-03 | [§3](#3-the-noun) | — |
| <a id="SU-D2"></a>SU-D2 | **No setup name resolves per OS; each OS has one named default setup, with no probing chain**: Linux `podman-self`, macOS `apple-container-self`, then `seatbelt-self` by [OQ-SU4](#OQ-SU4). A default whose technology is missing refuses, naming the install and the built-ins available. A design decision answering the owner's *"I'm not clear that that's hugely important"*, narrowed by [SU-R2](#SU-R2): a committed name must mean one thing on every machine. | 2026-11-03 | [§2](#2-the-idea) | — |
| <a id="SU-D3"></a>SU-D3 | **The fields are `confinement`, `run_as`, `network`, and `policy` where the technology has a rule-list choice** (Seatbelt; reserved for Landlock). `confinement` decides only which fields exist, so a `policy` written for any other technology is refused. A field joins only if it is a property of the walls; the rest stays out ([§7](#7-what-else-goes-in-and-what-stays-out)). *Its field list is superseded by [SU-R3](#SU-R3): `network` left, so the fields are `confinement`, `run_as` and `policy`.* *Its `policy` part is superseded by [SU-R4](#SU-R4): the fields are `confinement` and `run_as`.* | 2026-11-03 | [§4](#4-the-fields) | — |
| <a id="SU-D4"></a>SU-D4 | **Built-in names are `<confinement>[-<policy>]-<run_as>`**, `policy` shortened to `deny` or `allow`, the other values spelled exactly; one built-in per combination; built-ins cannot be redefined. *Its `policy` part is superseded by [SU-R4](#SU-R4): names are `<confinement>-<run_as>`.* | 2026-11-03 | [§5](#5-the-built-in-setups) | — |
| <a id="SU-D7"></a>SU-D7 | **The rule-list field is `policy = "deny-default" \| "allow-default"`.** A design decision: `deny-default` is already the profile's name in the code and its tests, and each value says what the rule list does with whatever it does not mention. `allow-default` keeps the `(allow default)` profile the model plan's slice 13 would delete. *Superseded by [SU-R4](#SU-R4): there is no `policy` field.* | 2026-11-03 | [§4](#4-the-fields) | — |
| <a id="SU-D8"></a>SU-D8 | **A written `network.mode` the setup's technology cannot hold is refused, naming a setup that can and the `unset` for the key**: `host` under Apple Container, `bridge` under Seatbelt or host. An unwritten one is the technology's network. A design decision: today Apple Container warns and Seatbelt is silent ([`assemble.go`](../../internal/cli/run/assemble.go), [`backendcaps.go`](../../internal/cli/run/backendcaps.go)), so a project could start an agent believing it has a network it does not. *Confirmed by the owner as [SU-R5](#SU-R5).* | 2026-11-03 | [§4](#4-the-fields), [§6](#6-config-by-example) | — |
| <a id="SU-D5"></a>SU-D5 | **Definitions are user scope only; `host` setups only through `yolo host`**; the command line needs no approval. Choosing is user scope only too, by [OQ-MT5](#OQ-MT5). | 2026-11-03 | [§8](#8-the-security-ceiling) | — |
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
