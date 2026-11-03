---
title: "What to call the macOS middle ground: four terminology plans for guest, macos-user and the second account"
date: 2026-11-03
status: draft
stage: DESIGN
next: "Rule OQ-MT1 (which plan). OQ-MT2 to OQ-MT4 settle its spellings."
depends-on:
  - macos-user-model.md#OQ-MM10
tags: [design, macos-user, terminology, confinement, guest, naming]
summary: "One concept, the agent on your real Mac under Apple's sandbox with no container, has five names, and one word, guest, means three things. Guest was never dropped: it is the confinement setting's middle value, coined 2026-07-28. Account mode came from the 2026-10-22 prototype script's --mode self|account flag, which the plan copied into config without an owner ruling on the words. Once running as you becomes the default, guest's documented meaning, a separate user account, stops being true. Four plans, from fixing only the prose to renaming everything; the recommendation keeps guest as the one user-facing name and renames the who-runs-it setting."
vantage:
  status-chip: true
---

# What to call the macOS middle ground

**Status:** 2026-11-03. Draft; nothing renamed. The owner asked: *"why did we stop calling it a
guest? where did account mode come from? have the designer come up with a few clear terminology
plans for this area."* Origins come from `git log -S` on the tree at `ea3b0dc1e`.

**Needs your ruling:** [OQ-MT1](#OQ-MT1), [OQ-MT2](#OQ-MT2), [OQ-MT3](#OQ-MT3), [OQ-MT4](#OQ-MT4).

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

**The two facts every plan has to fit.**

- The planned Linux guest (bwrap plus Landlock) runs as you, with no separate user. So **running
  as you is the cross-platform guest**, and the separate account is a macOS-only extra
  boundary.
- `host` also runs as you. What separates "guest, as you" from `host` is the Seatbelt profile,
  so a name must not lean on "you" alone.

## 2. The plans

Each plan maps the seven concepts in play. The 2026-11-03 rule that old configs launch unchanged
covers only the switch to TOML ([legacy configs keep
working](legacy-config-compat.md#1-the-one-rule)), so it does not bind a rename. A renamed
spelling can either be read as an alias, which `yolo config migrate` rewrites, or be refused with a
message naming the new spelling, as a renamed pack is ([PE-D7](provider-enablement.md#PE-D7)). The
costs below assume the alias; either works with any plan.

### Plan A: keep every spelling, fix the meanings

*One-line idea:* no config or CLI change; one prose name per concept, and "guest" means only the
notch.

| Concept | Config | CLI | Messages and docs | Code |
| :--- | :--- | :--- | :--- | :--- |
| The middle notch | `confinement = "guest"` | `--at guest` | "guest", defined as "your real machine, its own home, the OS sandbox, no container" | unchanged |
| The macOS backend | `runtime = "macos-user"` | unchanged | "the `macos-user` backend" | unchanged |
| Who it runs as | `macos_user_mode = "self" \| "account"` | none | "runs as you" / "runs as the yolo account" | unchanged |
| `_yolojail` | (none) | none | "the yolo account (`_yolojail`)" everywhere | unchanged |
| Confined side | (none) | none | "inside the sandbox" | unchanged |
| Account setup | (none) | `yolo macos-setup` and siblings | help says it is needed only to run as the yolo account | unchanged |
| The profile | (none) | none | "the Seatbelt profile" | unchanged |

*Pros:* zero user cost; fixes the false definition. *Cons:* `"account"` stays ambiguous (whose
account?), and `macos-user` keeps naming a user that the default no longer involves.
*Cost:* the guide pages and the charter's definition; the message strings that say "Sandbox
user" or "second account".

### Plan B: guest is the one name, and the account is an option of it (recommended)

*One-line idea:* `guest` is the only user-facing name for the middle ground; who it runs as is a
guest setting phrased in plain words; "guest" never names the account.

| Concept | Config | CLI | Messages and docs | Code |
| :--- | :--- | :--- | :--- | :--- |
| The middle notch | `confinement = "guest"` (the front door) | `--at guest` | "guest", defined as in Plan A | unchanged |
| The macOS backend | `runtime = "macos-user"` kept, documented as "the guest backend on a Mac" ([OQ-MT3](#OQ-MT3)) | unchanged | the guide leads with `confinement = "guest"`, mentions the runtime once | unchanged |
| Who it runs as | `guest_runs_as = "you" \| "yolo-account"`; `macos_user_mode` read as an alias ([OQ-MT2](#OQ-MT2)) | none | "the guest runs as you" / "the guest runs as the yolo account" | `Mode` values `RunsAsYou` / `RunsAsAccount` |
| `_yolojail` | (none) | none | "the yolo account (`_yolojail`)" ([OQ-MT4](#OQ-MT4)) | `SandboxUser` → `AccountUser`, `SandboxHome()` → `AccountHome()` |
| Confined side | (none) | none | "inside the guest" | "guest" kept (`GuestRealHome`, guest binaries): it now means only "inside the notch" |
| Account setup | (none) | `yolo macos-setup` kept | help: "Create the yolo account, needed only for `guest_runs_as = "yolo-account"`" | unchanged |
| The profile | (none) | none | "the Seatbelt profile" | unchanged |

*Pros:* one word for the place, plain words for the choice; matches Linux, where the guest will
run as you; the existing internal "guest" usage becomes correct rather than renamed. *Cons:* a
second spelling of a key that shipped one release ago; "the guest runs as you" needs the profile
named beside it to tell it from `host`. *Cost:* one key alias plus migrate mapping
(`self` → `you`, `account` → `yolo-account`); the launch disclosure and refusal lines in
[`commands.go`](../../internal/cli/commands.go) and
[`orchestrator.go`](../../internal/macosuser/orchestrator.go); the guide pages; the config
reference.

### Plan C: drop the metaphor, name the mechanism

*One-line idea:* call things what they are: the notch `sandboxed`, the backend `seatbelt`, the
choice `run_as`.

| Concept | Config | CLI | Messages and docs | Code |
| :--- | :--- | :--- | :--- | :--- |
| The middle notch | `confinement = "sandboxed"`; `guest` an alias | `--at sandboxed` | "sandboxed: on your machine, in the OS sandbox" | `ConfinementSandboxed` |
| The macOS backend | `runtime = "seatbelt"`; `macos-user` an alias | unchanged | "the Seatbelt backend" | package rename optional |
| Who it runs as | `run_as = "me" \| "_yolojail"` | none | "runs as you" / "runs as `_yolojail`" | as Plan B |
| `_yolojail` | (none) | none | "`_yolojail`", by name only | as Plan B |
| Confined side | (none) | none | "inside the sandbox" | every "guest" identifier renamed |
| Account setup | (none) | `yolo seatbelt-setup`, old verbs kept as aliases | as Plan B | renamed |
| The profile | (none) | none | "the Seatbelt profile" | unchanged |

*Pros:* every name is literally true on a Mac. *Cons:* reverses a recorded ruling ([why
`guest`](../reference/what-yolo-is.md#why-guest-not-sandbox)): "sandbox" already names the jail
in this codebase and the whole field outside it, and `seatbelt` lies about Linux. *Cost:* the
highest: two shipped config values and a key aliased, a flag, the footer, four verbs, the
charter, and hundreds of code identifiers.

### Plan D: make the account a notch of its own

*One-line idea:* the dial names the agent's relationship to the machine, and a separate identity
*is* a relationship, so the dial gets four notches: `jail` / `account` / `guest` / `host`.

| Concept | Config | CLI | Messages and docs | Code |
| :--- | :--- | :--- | :--- | :--- |
| Separate account, Seatbelt | `confinement = "account"` (macOS only) | `--at account` | "its own account on your Mac" | new preset in `render` |
| You, Seatbelt (Linux: bwrap) | `confinement = "guest"` | `--at guest` | "guest: you, inside the OS sandbox" | unchanged |
| The macOS backend | `runtime = "macos-user"` serves both | unchanged | as Plan B | unchanged |
| Who it runs as | no setting: chosen by the notch; `macos_user_mode` read as an alias | none | none needed | `Mode` derived from the notch |
| `_yolojail`, confined side, setup, profile | as Plan B | as Plan B | as Plan B | as Plan B |

*Pros:* no separate "mode" setting at all; the footer and `yolo describe` state the identity.
*Cons:* a notch that exists on one platform only; the choice moves from user scope to a setting a
workspace may set, which reopens the rule that a workspace cannot choose the account
([MMP-D3](macos-user-model-plan.md#MMP-D3)). *Cost:* a new dial value and preset, the charter's
table, and an alias for the shipped key.

## 3. Recommendation: Plan B

Plan B fixes the actual defect, a definition that becomes false, by moving the one fact that
varies (who the agent runs as) out of the notch's name and into a guest setting. It keeps every
spelling a user typed for the notch, keeps the July ruling on "guest", and makes the
hundreds of internal "guest" identifiers correct as they stand instead of renaming them. Plan A is
the fallback if you would rather not alias a key that shipped one release ago; it leaves the
`"account"` ambiguity. Plan C costs the most to buy literal names the July ruling already weighed
and rejected. Plan D is coherent, but it spends a dial notch on a macOS-only option and turns a
machine-wide choice into a per-workspace one.

Whatever you pick, three things hold, and none of them needs your ruling: "guest" stops naming
`_yolojail` in docs ("the guest OS user" goes); "private mode" and "self mode" disappear from live
docs; "sandbox" is kept for Seatbelt alone.

## 4. Open Questions

1. 💬 **OQ-MT1: Which terminology plan?**

   Today a Mac user sets `confinement = "guest"`, reads that a guest is "a separate user account",
   and then sees a launch line saying the agent runs as them. The plan decides what they read
   instead, and whether any setting is respelled.

   - **A — Fix the meanings only.** No setting changes; the guide and messages are reworded.
   - **B — Guest is the one name; who it runs as is a guest setting.** One key gets a new spelling.
   - **C — Mechanism names.** `sandboxed`, `seatbelt`, `run_as`; reverses the July ruling.
   - **D — The account becomes its own notch.** Four dial values, no mode setting.

   <!-- vantage: question id=OQ-MT1 leaning="B: it fixes the false definition, keeps every notch spelling, and makes the internal uses of guest correct as they stand." -->

   _Leaning:_ B. It fixes the false definition, keeps every notch spelling, and makes the internal
   uses of "guest" correct as they stand.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 🤷 **OQ-MT2: Under Plan B, how is the who-runs-it setting spelled?**

   It is one line in the user config, and the old `macos_user_mode` line keeps working either way.
   For example, a user who wants the separate account writes one of these.

   - **`guest_runs_as = "yolo-account"`** (default `"you"`). It reads as a sentence and names
     both choices.
   - **`guest_account = true`** (default `false`). Shorter, but "guest account" is also the name
     of macOS's own built-in Guest User, which is a different thing.
   - **Keep `macos_user_mode = "account"`.** No alias, but "account" stays ambiguous.

   <!-- vantage: question id=OQ-MT2 leaning="guest_runs_as with you and yolo-account: the line says what it does, and it avoids the clash with the macOS Guest User." -->

   _Leaning:_ `guest_runs_as = "you" | "yolo-account"`. The line says what it does, and it avoids
   the clash with the macOS Guest User.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 **OQ-MT3: Should `runtime = "macos-user"` be renamed now?**

   Once the guest runs as you by default, the name describes a macOS user that is not there. But
   it is the value every macOS user who chose this backend typed, and Plan B makes
   `confinement = "guest"` the front door, so new users need not meet it.

   - **Keep it**, and document it as "the guest backend on a Mac".
   - **Rename it to `seatbelt`**, reading `macos-user` as an alias forever.

   <!-- vantage: question id=OQ-MT3 leaning="Keep it: with guest as the front door the runtime value fades from view, and a rename buys a second spelling of a value every existing user has." -->

   _Leaning:_ keep it. With `guest` as the front door, the runtime value fades from view, and a
   rename buys a second spelling of a value every existing user has.

   **Answer:**
   > _(empty — fill in when decided)_

4. 💬 🤷 **OQ-MT4: What do messages and docs call `_yolojail`?**

   Today `yolo macos-setup` prints "Sandbox user '_yolojail' exists", the launch says "with no
   second account", and the troubleshooting page says "files show as owned by `_yolojail`. That is
   the sandbox account".

   - **"the yolo account"**: says whose it is, and needs no other mode to make sense.
   - **"the sandbox account"**: today's most common wording, but "sandbox" also names Seatbelt.
   - **"the second account"**: true only next to a mention of yours.
   - **"the guest account"**: matches the notch, but collides with macOS's Guest User.

   <!-- vantage: question id=OQ-MT4 leaning="the yolo account: Sandbox user '_yolojail' exists becomes The yolo account _yolojail exists, which needs no other term to read." -->

   _Leaning:_ "the yolo account". "Sandbox user '_yolojail' exists" becomes "The yolo account
   `_yolojail` exists", which needs no other term to read.

   **Answer:**
   > _(empty — fill in when decided)_
