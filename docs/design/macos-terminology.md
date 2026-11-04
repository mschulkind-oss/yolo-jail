---
title: "What to call the macOS middle ground: walls first, then who the agent runs as"
date: 2026-11-03
status: draft
stage: DESIGN
next: "Rule OQ-MT1 (which plan). Then OQ-MT9 (spellings), OQ-MT3, OQ-MT5, OQ-MT7 and OQ-MT8."
depends-on:
  - macos-user-model.md#OQ-MM10
tags: [design, macos-user, terminology, confinement, guest, naming]
summary: "One concept, the agent on your real Mac under Apple's sandbox with no container, has five names, and one word, guest, means three things. Guest was never dropped: it is the confinement setting's middle value, coined 2026-07-28. Account mode came from the 2026-10-22 prototype script's --mode self|account flag, copied into config without an owner ruling on the words. Fourth draft, in the second draft's format: five plans, three of them walls first (confinement names container, sandbox or host; runs_as names you or the yolo account) differing in how the container backend is chosen, one with the yolo account as a fourth notch, and one with a single value per combination. Renames are free: a removed spelling is refused naming its replacement, with no aliases. The recommendation is walls first with runtime naming the container runtime."
vantage:
  status-chip: true
---

# What to call the macOS middle ground

**Status:** 2026-11-03, fourth draft, in the second draft's format (`90ff72f2c`), carrying the third round's rulings. Draft; nothing renamed. The owner first asked: *"why did we stop calling it a
guest? where did account mode come from? have the designer come up with a few clear terminology
plans for this area."* Origins come from `git log -S` on the tree at `ea3b0dc1e`.

**Needs your ruling:** [OQ-MT1](#OQ-MT1), [OQ-MT3](#OQ-MT3), [OQ-MT5](#OQ-MT5), [OQ-MT7](#OQ-MT7), [OQ-MT8](#OQ-MT8), [OQ-MT9](#OQ-MT9). **Ruled 2026-11-03:** [OQ-MT2](#OQ-MT2), [OQ-MT4](#OQ-MT4), MT-R1 to MT-R3 ([ledger](#decision-ledger)).

> **In short.** Nothing stopped being called a guest. `guest` is still the value of the
> `confinement` setting, and asking for it is what launches `macos-user`. What changed is
> underneath: since 2026-10-22 the backend can run the agent *as you* rather than as a separate
> account, and that work named its two ways of running *self* and *account*, words picked by an
> agent, not by you. Meanwhile "guest" has also been applied to the separate account and to the
> confined side of a launch. So one word means three things, and its user-facing definition ("a
> separate user account") becomes false the day running as you becomes the default
> ([OQ-MM10](macos-user-model.md#OQ-MM10)).

Two terms of art used throughout: **Seatbelt** is Apple's built-in sandbox, which runs a program
under a profile listing what it may touch (`sandbox-exec`). **The confinement dial** is the
`confinement` setting's three values, `jail` / `guest` / `host`, from strongest to none
([the charter's dial](../reference/what-yolo-is.md#the-confinement-dial)).

## 1. The problem: every term in use today

"Visible" means a user meets it: a config key or value, a CLI flag or verb, a message, or the user
guide. "Internal" means only code, design docs and tests.

| Term | What it actually means | Where it shows | Origin |
| :--- | :--- | :--- | :--- |
| `confinement = "guest"`, `--at guest`, footer `guest`, `YOLO_CONFINEMENT=guest` | The middle notch of the dial: no container, a real home, an OS sandbox. On a Mac it selects `macos-user`; on Linux it refuses (planned as bwrap plus Landlock, with **no** separate user) | Visible: config value, flag, footer, [Where Agents Run](../../userguide/guides/confinement.md#guest-in-development) | `ce65b68dc`, 2026-07-28, renamed from `sandbox` because "sandbox" names the whole field ([why `guest`](../reference/what-yolo-is.md#why-guest-not-sandbox)); code `3c99dcd3b`, 2026-08-01; shipped by v0.15.1 |
| "Guest: a separate user account on your machine" | The user guide's and charter's definition of that notch | Visible: [confinement guide](../../userguide/guides/confinement.md), [features](../../userguide/features.md); [charter table](../reference/what-yolo-is.md#the-confinement-dial) | Same rename; true then, false once running as you is the default |
| `runtime = "macos-user"`, `YOLO_RUNTIME=macos-user` | The macOS backend: Seatbelt, no VM. Runs as `_yolojail` or as you | Visible: config value, env var, every macOS guide page | `4c85023fd`, 2026-07-12, when the backend was *only* "run as a separate macOS user" |
| `macos_user_mode = "account"` (default) | Run the agent as the `_yolojail` account through a sudo rule | Visible: user-scope config key ([settings per setup](../../userguide/reference/settings-per-setup.md)) | Value from the prototype script's `--mode self\|account` (`5a57b9c42`, 2026-10-22); key and spelling chosen by the plan ([MMP-D3](macos-user-model-plan.md#MMP-D3)), code `f5bc5fd77`; shipped v0.16.0 |
| `macos_user_mode = "self"` | Run the agent as you, under the deny-by-default profile | Visible: same key; launch line "macos-user runs the agent as you (macos_user_mode "self", experimental)" | Ruled as a model in [OQ-MM1](macos-user-model.md#OQ-MM1), 2026-10-22; the word is the prototype's |
| "account mode", "self mode", "run-as-you mode" | Prose names for the two values | Mostly internal (design docs, code comments, test names); "run-as-you" reaches one precondition message | [§7 of the model doc](macos-user-model.md#7-the-ruled-model-two-modes-one-profile) says *"Its name and spelling are the plan's to choose"* |
| "the second account" | `_yolojail`, seen from the model choice | Visible in the launch disclosure ("with no second account") and the macOS guide | [MM-R2](macos-user-model.md#decision-ledger), 2026-10-22 |
| "sandbox account", "sandbox user", "hidden macOS user" | Also `_yolojail` | Visible: `yolo macos-setup` output ("Sandbox user '_yolojail' exists"), help line "Provision the native macOS sandbox user", storage, troubleshooting, patch-series and settings guides | From the 2026-07-12 backend (`30f91946e`) |
| `_yolojail`, `SandboxUser`, `SandboxHome()` (`/Users/_yolojail`) | The account itself and its home | `_yolojail` visible (file owners, setup output); the identifiers internal | `4c85023fd` (account), `f7ab09c78`, 2026-07-18 (`SandboxHome`) |
| "the guest OS user" | `_yolojail` again | Internal: [home tiers](../reference/macos-user-home-tiers.md) | `5366bd864`, 2026-10-20 |
| "guest yolo", "guest scope", "guest binaries", `GuestRealHome`, `guest_binaries` stage | The yolo processes *inside* the sandbox, whichever user they run as | Internal | [MCWP-D10](macos-user-model-plan.md#MCWP-D10), 2026-10-22; `GuestRealHome` `d2a747296`, 2026-10-23 |
| "private mode" | The retired concurrent-workspaces plan's name for each workspace getting its own home under one account | Internal: one ledger row carried into [MCWP-D10](macos-user-model-plan.md#MCWP-D10) | `2e0e0cf34`, 2026-10-22; the design it named is retired ([MMP-D28](macos-user-model-plan.md#MMP-D28)) |
| "native" (`nativehome`, `.yolo-native-sessions`) | The macOS backend, as opposed to a VM | Internal, and the `macos-setup` help line | Help line `8430a85db`, 2026-07-20; `nativehome` `29e298ca5`, 2026-10-19 |
| `yolo macos-setup`, `macos-teardown`, `macos-fix-permissions`, `macos-unshare` | Create or remove `_yolojail`, its group, sudo rule and workspace sharing. Only account mode needs them | Visible: verbs | `30f91946e`, 2026-07-12 |
| `YOLO_DARWIN_*` (`REAL_HOME`, `MACHINE_HOME`, `WORKSPACE`, `ENV_FILE`, …) | Host-to-sandbox plumbing | Internal | Grew with the backend; `REAL_HOME` is [MMP-D7](macos-user-model-plan.md#MMP-D7) |
| "the macos-user sandbox" | The backend, in the user guide | Visible | Guide prose |

**Where "account mode" came from.** The 2026-10-22 prototype script took `--mode self` and
`--mode account --account _yolojail`. The next day's plan made those the config values so the
prototype and the setting matched ([MMP-D3](macos-user-model-plan.md#MMP-D3)). Your rulings that
day ([OQ-MM1](macos-user-model.md#OQ-MM1), [MM-R2](macos-user-model.md#decision-ledger)) decided
the model, running as you by default with the account kept as an option, and left the name to the
plan. Nobody ruled on the words.

**Why "guest" seems to have gone.** It did not go. It names the dial's notch, and the macOS work
since October is about a different question, *who* the agent runs as, which the dial never asked.
That work never mentioned the dial, so its new words (self, account, run-as-you, second account)
sit beside "guest" rather than under it. Meanwhile "guest" was reused for the account ("the guest OS
user") and for the confined side ("guest yolo").

**What the third round changed.** The owner, reviewing the second round: *"I'm now leaning
towards confinement = <the walls> as the first thing to base all this off of."* So in Plans 1 to 3
the setting names only the walls, and who the agent runs as is a second, independent setting.

- **Walls:** `container` (an isolated Linux container: its own filesystem, home and tools; your
  files reach it only as mounts), `sandbox` (your real machine under an operating-system rule
  set that denies by default: Seatbelt on macOS, the planned Landlock on Linux), `host` (no walls).
- **Who runs it:** `you`, or `yolo-account`, a separate OS user yolo creates once with an admin
  step (`_yolojail` on macOS). No value is a bare "account" ([OQ-MT2](#OQ-MT2)).
- **No ladder.** The yolo account with no sandbox and you inside the sandbox are different walls,
  not stronger and weaker versions of one. Only Plan 4 keeps a ladder, by giving up that cell.
- **Renames are free** ([MT-R3](#decision-ledger)): plans are judged on clarity, how they read on
  macOS and Linux, and room for future modes. A removed spelling is refused, never aliased, and
  the refusal names its replacement, the minimum the
  [Happy Path Principle](../reference/happy-path-principle.md#the-rules) asks of a stop: for
  example, *"`guest` is now `sandbox`: set `confinement = "sandbox"`"*.

## 2. The plans

Each table maps every concept to its config, CLI, messages (including the footer and `yolo
describe`) and code, then says what works on a Mac and on Linux. A cell that is not built on a
system stops naming the nearest one that is. Round two's Plans 1, 2 and 4 are dropped: each kept
identity tied to one wall, so a Linux yolo account or a yolo account with no sandbox had no
spelling. Its Plan 3 is Plan 4 here.

### Plan 1: walls first, `runtime` names the container runtime

*Idea:* `confinement` picks the walls, `runs_as` the account, and `runtime` survives only as the
choice of container runtime, the industry's own term.

| Concept | Config | CLI | Messages, footer, describe | Code |
| :--- | :--- | :--- | :--- | :--- |
| Container, as you | `confinement = "container"` (default), `runs_as = "you"` (default) | `--at container` | `container · as you`; describe `container (Podman)` | `ConfinementContainer` |
| Container backend | `runtime = "podman" \| "apple-container"`, refused beside any other `confinement` | `YOLO_RUNTIME` | describe names it | Apple Container leaves the value `container` |
| Container, as the yolo account | `runs_as = "yolo-account"` | `--as yolo-account` | refused, naming `runs_as = "you"` ([OQ-MT7](#OQ-MT7)) | — |
| Sandbox, as you | `confinement = "sandbox"` | `--at sandbox` | `sandbox · as you`; describe `sandbox (Seatbelt)` | `ConfinementSandbox`; `RunsAs` replaces `macosuser.Mode` |
| Sandbox, as the yolo account | `confinement = "sandbox"`, `runs_as = "yolo-account"` | `--at sandbox --as yolo-account` | `sandbox · as the yolo account` | the one crossing (`RunAs`), as today |
| Host, as you | `confinement = "host"` | `--at host`, `yolo host -- <cmd>` | `host · as you` | `ConfinementHost` |
| Host, as the yolo account | `confinement = "host"`, `runs_as = "yolo-account"` | `yolo host --as yolo-account -- <cmd>` | `host · as the yolo account` | new: the crossing with no profile |
| The yolo account | (none) | setup verbs ([OQ-MT8](#OQ-MT8)) | "the yolo account (`_yolojail`)" | `AccountUser`, `AccountHome()` |
| Retired | `guest`, `jail`, `macos-user`, `macos_user_mode`, `runtime = "container"`: each refused naming its replacement | `--at guest`, `--at jail` refused the same way | "guest", "second account", "sandbox account" gone | "guest" identifiers become "sandbox side" |
| On a Mac | every cell but container with the yolo account | the same spellings | the same | — |
| On Linux | container is Podman; sandbox is bwrap plus Landlock, refused until built; the yolo account needs a Linux account and crossing, future work; host with it is `sudo -u` | the same spellings | the same | — |

*Pros:* each setting means one thing; one flat line per choice; identical spellings on both
systems; every mode raised so far is a cell, not a new word. *Cons:* three settings, one of which
(`runtime`) is meaningful beside only one value of another.

### Plan 2: walls first, a table per kind of walls

*Idea:* as Plan 1, but the backend lives under the walls it belongs to, so a second sandbox
backend could join later without a new key.

| Concept | Config | CLI | Messages, footer, describe | Code |
| :--- | :--- | :--- | :--- | :--- |
| Container, as you | `confinement = "container"`, `runs_as = "you"` (both defaults) | `--at container` | as Plan 1 | as Plan 1 |
| Container backend | `[container] backend = "podman" \| "apple-container"` | `--backend apple-container`; `YOLO_CONTAINER_BACKEND` | describe `container (Apple Container)` | `runtime` key gone |
| Sandbox backend | `[sandbox] backend`, reserved, one value per system today | none | describe `sandbox (Seatbelt)` | reserved |
| Container, as the yolo account | as Plan 1 | as Plan 1 | refused ([OQ-MT7](#OQ-MT7)) | — |
| Sandbox, as you / as the yolo account | as Plan 1 | as Plan 1 | as Plan 1 | as Plan 1 |
| Host, as you / as the yolo account | as Plan 1 | as Plan 1 | as Plan 1 | as Plan 1 |
| The yolo account; retired | as Plan 1, plus `runtime` refused naming `[container] backend` | as Plan 1 | as Plan 1 | as Plan 1 |
| On a Mac / on Linux | as Plan 1 | as Plan 1 | as Plan 1 | — |

*Pros:* the config file shows which setting each backend belongs to; room for a second sandbox
backend. *Cons:* a table holding one key; a reserved key with no second value in sight.

### Plan 3: walls first, the backend inside the value

*Idea:* one value names the walls and, optionally, their backend: `container:podman`.

| Concept | Config | CLI | Messages, footer, describe | Code |
| :--- | :--- | :--- | :--- | :--- |
| Container, as you | `confinement = "container"` (the system's default runtime) or `"container:podman"` / `"container:apple-container"` | `--at container:apple-container` | footer `container:apple-container · as you` | `Confinement` gains a backend field |
| Sandbox, as you / as the yolo account | `confinement = "sandbox"`, `runs_as` as Plan 1 | `--at sandbox`, `--as` | as Plan 1 | as Plan 1 |
| Host, as you / as the yolo account | as Plan 1 | as Plan 1 | as Plan 1 | as Plan 1 |
| Container, as the yolo account | as Plan 1 | as Plan 1 | refused ([OQ-MT7](#OQ-MT7)) | — |
| The yolo account; retired | as Plan 1, plus `runtime` and `YOLO_RUNTIME` refused naming the compound value | as Plan 1 | as Plan 1 | as Plan 1 |
| On a Mac / on Linux | as Plan 1; Linux has only `container:podman` | as Plan 1 | as Plan 1 | — |

*Pros:* one setting and one flag pick walls and backend together. *Cons:* a mini-syntax inside a
string; the colon form appears nowhere else in yolo's config.

### Plan 4: the yolo account as a fourth notch

*Idea:* keep one ordered dial, `container` / `yolo-account` / `sandbox` / `host`, where
`yolo-account` is the sandbox plus a separate user; no `runs_as`.

| Concept | Config | CLI | Messages, footer, describe | Code |
| :--- | :--- | :--- | :--- | :--- |
| Container | `confinement = "container"` (default); backend as Plan 1 | `--at container` | `container` | `ConfinementContainer` |
| Sandbox, as the yolo account | `confinement = "yolo-account"` | `--at yolo-account` | "the yolo account: a separate user, inside the OS sandbox" | the macOS preset the code already calls "a separate user + Seatbelt" ([`confinement.go`](../../internal/render/confinement.go)) |
| Sandbox, as you | `confinement = "sandbox"` | `--at sandbox` | `sandbox` | `ConfinementSandbox` |
| Host | `confinement = "host"` | `--at host`, `yolo host` | `host` | `ConfinementHost` |
| Host, as the yolo account | no spelling | — | — | — |
| The yolo account; retired | as Plan 1 | as Plan 1 | as Plan 1 | as Plan 1 |
| On a Mac / on Linux | the same four values; Linux `sandbox` and `yolo-account` refused until built | the same | the same | — |

*Pros:* one setting, an honest strength order, and a single word in the footer. *Cons:* a yolo
account with no sandbox, which the owner wants to be able to specify, has nowhere to go; a
notch named for an account beside three named for walls.

### Plan 5: one value per cell

*Idea:* walls first, but the account is part of the one value: `sandbox`, `sandbox+yolo-account`,
`host+yolo-account`. A user picks one preset from a short list, as the
[fill-the-matrix rule](../reference/fill-the-matrix-principle.md) prefers.

| Concept | Config | CLI | Messages, footer, describe | Code |
| :--- | :--- | :--- | :--- | :--- |
| Container | `confinement = "container"`; backend as Plan 1 | `--at container` | `container` | as Plan 1 |
| Sandbox, as you / as the yolo account | `"sandbox"` / `"sandbox+yolo-account"` | `--at sandbox+yolo-account` | the value as written | `Confinement` carries the account |
| Host, as you / as the yolo account | `"host"` / `"host+yolo-account"` | `--at host+yolo-account` | the value as written | as above |
| Container, as the yolo account | no such value until built ([OQ-MT7](#OQ-MT7)) | — | — | — |
| The yolo account; retired | as Plan 1 | as Plan 1 | as Plan 1 | as Plan 1 |
| On a Mac / on Linux | the same five values | the same | the same | — |

*Pros:* one setting covers every cell; the footer is the value. *Cons:* five values to learn where
Plan 1 has three and two; a user who wants "the yolo account wherever I run" restates it in every
value.

## 3. Recommendation: Plan 1

Plan 1 is the owner's favored shape with the plainest spelling. Each setting means one thing, so
the footer, `yolo describe` and every message can say both halves (`sandbox · as the yolo
account`). It reads the same on macOS and Linux, and both future modes raised so far, a Linux
yolo account and a yolo account with no sandbox, are cells rather than new words. `runtime` stays
the industry's term for what it now names. Plan 2 is right only if a second sandbox backend is
ever likely; Plan 3 trades a key for a syntax; Plan 4 is the fallback if the host-plus-yolo-account
cell is dropped; Plan 5 is the fallback if two settings prove one too many.

## 4. Open Questions

1. 💬 **OQ-MT1: Which plan, and so how is the container backend chosen?**

   The answer fixes the vocabulary of the guide, the footer and every message in this area. On a
   Mac a container is Podman or Apple Container, and today's `runtime = "container"` (Apple
   Container) collides with the new value.

   - **Plan 1:** `confinement`, `runs_as`, and `runtime = "apple-container"`.
   - **Plan 2:** the same, with `[container] backend = "apple-container"`.
   - **Plan 3:** the same, with `confinement = "container:apple-container"`.
   - **Plan 4:** a fourth notch, `confinement = "yolo-account"`, and no host with the yolo account.
   - **Plan 5:** one value per cell, such as `confinement = "sandbox+yolo-account"`.

   <!-- vantage: question id=OQ-MT1 leaning="Plan 1: each setting means one thing, both systems spell every cell alike, and runtime keeps the industry's meaning." -->

   _Leaning:_ Plan 1. Each setting means one thing and both systems spell every cell alike: a
   Linux yolo account is `confinement = "sandbox"` plus `runs_as = "yolo-account"`, and an Apple
   Container launch is `runtime = "apple-container"`.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-MT3: Does `macos-user` survive anywhere?**

   It named a backend that ran as a macOS user. In every plan the walls are `sandbox` and the account is
   named on its own, so the name describes neither.

   - **Retire it:** `runtime = "macos-user"` is refused, naming `confinement = "sandbox"`.
   - **Keep it as the sandbox backend's name**, shown only in `describe`.

   <!-- vantage: question id=OQ-MT3 leaning="Retire it: the sandbox has one backend per system, and describe can say Seatbelt." -->

   _Leaning:_ retire it. The sandbox has one backend per system, and `describe` can say Seatbelt.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 **OQ-MT5: May a workspace config choose the yolo account (`runs_as`, or the value that carries it in Plans 4 and 5)?**

   Today only the user config picks the account, because a workspace is agent-editable
   ([MMP-D3](macos-user-model-plan.md#MMP-D3)). For example, a client repository wants
   `runs_as = "yolo-account"` so its agent never touches your files.

   - **Yes, through the config-change approval** that a workspace's `confinement` edit takes;
     a Mac without the account stops naming its setup verb.
   - **User config only**, as today.

   <!-- vantage: question id=OQ-MT5 leaning="Yes, through the approval: a project that wants a separate user should be able to say so, and the approval shows any change." -->

   _Leaning:_ yes, through the approval. A project that wants a separate user should be able to
   say so, and the approval shows every change before it applies.

   **Answer:**
   > _(empty — fill in when decided)_

4. 💬 **OQ-MT7: Is `container` with the yolo account built, or refused for good?**

   It would run the container runtime itself as the yolo account, so even an escape from the
   container lands in an account holding none of your files. It is real work: a second rootless
   Podman store, or Apple Container under another user.

   - **Refuse it for now**, naming `runs_as = "you"`, and record it as future work.
   - **Refuse it for good:** a container already has its own user.

   <!-- vantage: question id=OQ-MT7 leaning="Refuse it for now: it is a real, stronger cell, but nobody has asked for it." -->

   _Leaning:_ refuse it for now. It is a real, stronger cell, but nobody has asked for it.

   **Answer:**
   > _(empty — fill in when decided)_

5. 💬 🤷 **OQ-MT8: What happens to the words "jail" and "guest", and what are the setup verbs called?**

   The product is yolo-jail and the guide says "the jail" everywhere. `yolo macos-setup` creates
   an account Linux will also need.

   - **"jail":** the product's name and nothing else; "a container" where the guide meant the level.
   - **"guest":** gone from every surface and from code.
   - **Setup verbs:** `yolo yolo-account setup`, `remove`, `share`, `unshare`, on both systems,
     replacing the four `macos-*` verbs.

   <!-- vantage: question id=OQ-MT8 leaning="All three as listed: one meaning per word, and the verbs name what they make." -->

   _Leaning:_ all three as listed: one meaning per word, and the verbs name what they make.

   **Answer:**
   > _(empty — fill in when decided)_

6. 💬 🤷 **OQ-MT9: `container` or `jail` for the value, and `you` or `me`?**

   The owner suggested *"jail could be called container."* `you` matches how yolo's messages speak;
   `me` matches how a user writes their own config.

   - **`container` and `you`.**
   - **`jail` and `me`.**

   <!-- vantage: question id=OQ-MT9 leaning="container and you: container says what it is, and you matches every message yolo prints." -->

   _Leaning:_ `container` and `you`. `container` says what it is, and `you` matches every message
   yolo prints.

   **Answer:**
   > _(empty — fill in when decided)_

## Decision Ledger

| ID | Ruling | Date | Settled in |
| :--- | :--- | :--- | :--- |
| <a id="OQ-MT2"></a>[OQ-MT2](#OQ-MT2) | No value is called a bare `account`: running as you is an account too. The owner: *"I hate 'account' here because 'self' is also an account."* The separate user's value is `yolo-account`. | 2026-11-03 | [§1](#1-the-problem-every-term-in-use-today) |
| <a id="OQ-MT4"></a>[OQ-MT4](#OQ-MT4) | `_yolojail` is **the yolo account** in messages and docs. The owner: *"I like the YOLO account here … instead of account, just use YOLO account."* | 2026-11-03 | [§2](#2-the-plans) |
| MT-R1 | The second round's Plan C, naming by mechanism (the notch `sandboxed`, the runtime `seatbelt`), is rejected. The owner: *"don't like."* The third round's `sandbox` names a kind of walls, which the owner proposed. | 2026-11-03 | [§2](#2-the-plans) |
| MT-R2 | Every plan is tested against a future Linux mode that runs the agent as a separate yolo user. The owner: *"what if we add a Linux guest user mode? then we wouldn't want it to be organized in the way it is right now."* | 2026-11-03 | [§2](#2-the-plans) |
| MT-R3 | **What a rename costs existing configs is not a criterion; current spellings may break.** The owner: *"are these costs migration from current? I don't care. we can break current."* A removed spelling is refused with a message naming its replacement, the [happy-path](../reference/happy-path-principle.md) minimum; there are no aliases. | 2026-11-03 | [§2](#2-the-plans) |
