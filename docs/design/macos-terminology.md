---
title: "What to call the macOS middle ground: walls first, then who the agent runs as"
date: 2026-11-03
status: draft
stage: DESIGN
next: "Rule OQ-MT1 (adopt walls first). Then OQ-MT6 and OQ-MT9 (spellings), OQ-MT3, OQ-MT5, OQ-MT7 and OQ-MT8."
depends-on:
  - macos-user-model.md#OQ-MM10
tags: [design, macos-user, terminology, confinement, guest, naming]
summary: "One concept, the agent on your real Mac under Apple's sandbox with no container, has five names, and one word, guest, means three things. Guest was never dropped: it is the confinement setting's middle value, coined 2026-07-28. Account mode came from the 2026-10-22 prototype script's --mode self|account flag, copied into config without an owner ruling on the words. Third round, after two owner reviews: confinement names only the walls (container, sandbox, host), an independent runs_as names the account (you, or the yolo account), every combination is a cell on both macOS and Linux, and renames are free: a removed spelling is refused naming its replacement, with no aliases."
vantage:
  status-chip: true
---

# What to call the macOS middle ground

**Status:** 2026-11-03, third round, after the owner's reviews of `c4bb08284` and `90ff72f2c`. Draft; nothing renamed. The owner first asked: *"why did we stop calling it a
guest? where did account mode come from? have the designer come up with a few clear terminology
plans for this area."* Origins come from `git log -S` on the tree at `ea3b0dc1e`.

**Needs your ruling:** [OQ-MT1](#OQ-MT1), [OQ-MT6](#OQ-MT6), [OQ-MT3](#OQ-MT3), [OQ-MT5](#OQ-MT5), [OQ-MT7](#OQ-MT7), [OQ-MT8](#OQ-MT8), [OQ-MT9](#OQ-MT9). **Ruled 2026-11-03:** [OQ-MT2](#OQ-MT2), [OQ-MT4](#OQ-MT4), MT-R1 to MT-R3 ([ledger](#decision-ledger)).

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

**What the third round changes.** The owner, reviewing the second round: *"I'm now leaning
towards confinement = <the walls> as the first thing to base all this off of."* So the setting
names only the walls, and who the agent runs as becomes a second, independent setting. Two
consequences shape everything below:

- **There is no ladder any more.** The yolo account with no sandbox and you inside the sandbox
  are different walls, not stronger and weaker versions of one. Each setting is ordered on its
  own; their combinations are not.
- **Renames are free** ([MT-R3](#decision-ledger)): a plan is judged on clarity, how it reads on
  macOS and Linux, and room for future modes, never on what it costs today's configs.

## 2. The lead plan: walls first, then who runs it

*Idea:* `confinement` says what walls surround the agent; `runs_as` says whose account it runs
in; a backend is named only where there is a real choice of one.

**Removed spellings.** Every spelling this plan drops is refused, not aliased: the refusal names
the spelling that replaces it, which is the minimum the
[Happy Path Principle](../reference/happy-path-principle.md#the-rules) asks of a stop. For example,
`confinement = "guest"` stops with *"`guest` is now `sandbox`: set `confinement = "sandbox"`"*.

### 2.1 The two settings

| Setting | Values | Default | Means |
| :--- | :--- | :--- | :--- |
| `confinement` | `container` | yes | An isolated Linux container: its own filesystem, home and tools; your files reach it only as mounts |
| | `sandbox` | | Your real machine, under an operating-system rule set that denies by default: Seatbelt on macOS; on Linux a kernel rule set of the same kind (Landlock, the planned one) |
| | `host` | | No walls: your machine as it is |
| `runs_as` | `you` | yes | The agent runs in your own account |
| | `yolo-account` | | The agent runs in the yolo account, a separate OS user yolo creates once with an admin step (`_yolojail` on macOS) |

### 2.2 Every combination

| | `runs_as = "you"` | `runs_as = "yolo-account"` |
| :--- | :--- | :--- |
| `container` | Today's jail. Works on both systems | Not built; refused naming `runs_as = "you"` ([OQ-MT7](#OQ-MT7)) |
| `sandbox` | macOS: today's run-as-you mode, the default after [the flip](macos-user-model.md#OQ-MM10). Linux: the planned bwrap plus Landlock | macOS: today's account mode. Linux: a future mode, the same profile plus a yolo account |
| `host` | Today's `yolo host` | New: the separate user is the only wall. On macOS it is the model macos-user started with; on Linux, `sudo -u` into the account |

A cell that is not built on a system stops naming the nearest cell that is; none is silently
swapped.

### 2.3 What each surface becomes

| Concept | Config | CLI | Messages, footer, `yolo describe` | Code |
| :--- | :--- | :--- | :--- | :--- |
| The walls | `confinement = "container" \| "sandbox" \| "host"` | `--at container\|sandbox\|host`; `yolo host -- <cmd>` stays as `--at host` | footer `sandbox · as you`; describe `confinement: sandbox (Seatbelt)`; briefing heading `YOLO Environment — sandbox, as you` | `ConfinementContainer`, `ConfinementSandbox`; `YOLO_CONFINEMENT` takes the new values |
| Who runs it | `runs_as = "you" \| "yolo-account"` | `--as you\|yolo-account` | footer `· as the yolo account`; describe `runs as: the yolo account (_yolojail)` | `macosuser.Mode` becomes `RunsAs`, one value for every backend; `YOLO_RUNS_AS` |
| The container backend | see [OQ-MT6](#OQ-MT6): `runtime = "podman" \| "apple-container"` in the leaning | unchanged `YOLO_RUNTIME` | describe `container (Apple Container)` | Apple Container's value is no longer `container`, which now names the walls |
| The sandbox backend | none: one per system, so nothing to choose | none | describe names it (`Seatbelt`) | internal |
| `macos-user` | refused, naming `confinement = "sandbox"` ([OQ-MT3](#OQ-MT3)) | — | — | package name is internal |
| The yolo account | (none) | setup and removal verbs ([OQ-MT8](#OQ-MT8)) | "the yolo account (`_yolojail`)" | `SandboxUser`, `SandboxHome()` become `AccountUser`, `AccountHome()`: "sandbox" now names a setting value |
| "guest" | refused, naming `sandbox` | `--at guest` refused | gone | "guest" identifiers become "sandbox side" ([OQ-MT8](#OQ-MT8)) |
| "jail" | not a value; `jail` refused naming `container` | — | the product's name ([OQ-MT8](#OQ-MT8)) | — |

**Linux, end to end.** `container` is Podman, as today. `sandbox` is the planned bwrap plus
Landlock; until it ships, `confinement = "sandbox"` on Linux stops naming `container` and
`host`. `runs_as = "yolo-account"` needs a Linux yolo account and a crossing into it, both future
work, and the same two spellings cover it with nothing renamed.

### 2.4 Variants of the details

The settings above are fixed by the owner's lean; three details have real alternatives.

- **The backend choice ([OQ-MT6](#OQ-MT6)).**
  - **V1, `runtime` narrowed to container runtimes:** `runtime = "podman" | "apple-container"`,
    refused with any other confinement. "Container runtime" is the industry's own term.
  - **V2, one table per confinement:** `[container] backend = "podman"`, leaving room for
    `[sandbox] backend` if a system ever offers two.
  - **V3, one compound value:** `confinement = "container:podman"`; plain `container` picks the
    platform default.
- **The value spellings ([OQ-MT9](#OQ-MT9)):** `container` or keep `jail`; `you` or `me`.
- **Where `runs_as` may be set ([OQ-MT5](#OQ-MT5)):** the user config only, as the mode is today,
  or a workspace too.

### 2.5 Why this plan

It names the two things that vary separately, so every setting means one thing and the footer can
say both. It reads the same on macOS and Linux, and every future mode the owner has raised (a
Linux yolo account, a separate user with no sandbox) is a cell in the grid rather than a new
word. Its cost is the one the fill-the-matrix rule warns about, two settings to combine; it is
small here because both have safe defaults (`container`, `you`) and a user changes one at a time.

## 3. The other plans, briefly

From the second round, judged again on clarity, cross-platform fit and future modes:

- **1, respell `macos_user_mode`:** a macOS-only key for a choice Linux will need. Loses.
- **2, `guest_runs_as`:** the same split as the lead plan, but scoped to one wall, so
  host plus the yolo account has no spelling. Loses.
- **3, the yolo account as a fourth notch:** a ladder that the host-plus-yolo-account cell breaks,
  since that cell is neither stronger nor weaker than the sandbox. The best alternative if the
  owner drops that cell.
- **4, `guest` as the account, `fenced` for you:** two metaphors where the lead plan needs none.
  Loses.
- **5, walls and identity:** became the lead plan, with the walls renamed.
- **Mechanism names** (`sandboxed`, `seatbelt`): the owner rejected them ([MT-R1](#decision-ledger)). The
  lead plan's `sandbox` is the kind of walls, not a mechanism, and Seatbelt appears only in
  `describe`.

## 4. Open Questions

1. 💬 **OQ-MT1: Adopt the walls-first plan, `confinement` plus `runs_as`?**

   The owner called it *"my favorite"* while leaning, not ruling. Ruling it sets the vocabulary
   for the guide, the footer and every message in this area.

   - **Adopt it** as [§2](#2-the-lead-plan-walls-first-then-who-runs-it) states it.
   - **Plan 3 instead**, if host plus the yolo account is not wanted.

   <!-- vantage: question id=OQ-MT1 leaning="Adopt it: each setting means one thing, and every mode raised so far is a cell, not a new word." -->

   _Leaning:_ adopt it. Each setting means one thing, and every mode raised so far is a cell, not a
   new word: a Linux yolo account is `confinement = "sandbox"` plus `runs_as = "yolo-account"`.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-MT6: How is the container backend chosen?**

   On a Mac a container is either Podman or Apple Container; on Linux it is Podman. Today's
   `runtime = "container"` means Apple Container, which collides with the new value.

   - **V1:** `runtime = "podman" | "apple-container"`, valid only with `container`.
   - **V2:** `[container] backend = "apple-container"`.
   - **V3:** `confinement = "container:apple-container"`.

   <!-- vantage: question id=OQ-MT6 leaning="V1: one flat line, the industry's word for it, and the sandbox has nothing to choose." -->

   _Leaning:_ V1. It is one flat line in the industry's word, and the sandbox has nothing to
   choose, so a per-confinement table would hold one key.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 **OQ-MT3: Does `macos-user` survive anywhere?**

   It named a backend that ran as a macOS user. Under this plan the walls are `sandbox` and the
   user is `runs_as`, so the name describes neither.

   - **Retire it:** `runtime = "macos-user"` is refused, naming `confinement = "sandbox"`.
   - **Keep it as the sandbox backend's name**, shown only in `describe`.

   <!-- vantage: question id=OQ-MT3 leaning="Retire it: the sandbox has one backend per system, and describe can say Seatbelt." -->

   _Leaning:_ retire it. The sandbox has one backend per system, and `describe` can say Seatbelt.

   **Answer:**
   > _(empty — fill in when decided)_

4. 💬 **OQ-MT5: May a workspace config set `runs_as`?**

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

5. 💬 **OQ-MT7: Is `container` with the yolo account built, or refused for good?**

   It would run the container runtime itself as the yolo account, so even an escape from the
   container lands in an account holding none of your files. It is real work: a second rootless
   Podman store, or Apple Container under another user.

   - **Refuse it for now**, naming `runs_as = "you"`, and record it as future work.
   - **Refuse it for good:** a container already has its own user.

   <!-- vantage: question id=OQ-MT7 leaning="Refuse it for now: it is a real, stronger cell, but nobody has asked for it." -->

   _Leaning:_ refuse it for now. It is a real, stronger cell, but nobody has asked for it.

   **Answer:**
   > _(empty — fill in when decided)_

6. 💬 🤷 **OQ-MT8: What happens to the words "jail" and "guest", and what are the setup verbs called?**

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

7. 💬 🤷 **OQ-MT9: `container` or `jail` for the value, and `you` or `me`?**

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
| <a id="OQ-MT2"></a>[OQ-MT2](#OQ-MT2) | No value is called a bare `account`: running as you is an account too. The owner: *"I hate 'account' here because 'self' is also an account."* The separate user's value is `yolo-account`. | 2026-11-03 | [§2.1](#21-the-two-settings) |
| <a id="OQ-MT4"></a>[OQ-MT4](#OQ-MT4) | `_yolojail` is **the yolo account** in messages and docs. The owner: *"I like the YOLO account here … instead of account, just use YOLO account."* | 2026-11-03 | [§2.3](#23-what-each-surface-becomes) |
| MT-R1 | The second round's Plan C, naming by mechanism (the notch `sandboxed`, the runtime `seatbelt`), is rejected. The owner: *"don't like."* The third round's `sandbox` names a kind of walls, which the owner proposed. | 2026-11-03 | [§3](#3-the-other-plans-briefly) |
| MT-R2 | Every plan is tested against a future Linux mode that runs the agent as a separate yolo user. The owner: *"what if we add a Linux guest user mode? then we wouldn't want it to be organized in the way it is right now."* | 2026-11-03 | [§2.3](#23-what-each-surface-becomes) |
| MT-R3 | **What a rename costs existing configs is not a criterion; current spellings may break.** The owner: *"are these costs migration from current? I don't care. we can break current."* A removed spelling is refused with a message naming its replacement, the [happy-path](../reference/happy-path-principle.md) minimum; there are no aliases. | 2026-11-03 | [§2](#2-the-lead-plan-walls-first-then-who-runs-it) |
