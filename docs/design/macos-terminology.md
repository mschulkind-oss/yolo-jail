---
title: "What to call the macOS middle ground: five terminology plans for guest, macos-user and the yolo account"
date: 2026-11-03
status: draft
stage: DESIGN
next: "Rule OQ-MT1 (which plan); OQ-MT5 follows from Plans 3 and 4, OQ-MT3 from any plan."
depends-on:
  - macos-user-model.md#OQ-MM10
tags: [design, macos-user, terminology, confinement, guest, naming]
summary: "One concept, the agent on your real Mac under Apple's sandbox with no container, has five names, and one word, guest, means three things. Guest was never dropped: it is the confinement setting's middle value, coined 2026-07-28. Account mode came from the 2026-10-22 prototype script's --mode self|account flag, copied into config without an owner ruling on the words. Second round, after the owner's review: no bare account value, the separate user is the yolo account, mechanism names are rejected, and every plan is tested against a future Linux mode with a yolo account. Five plans, from respelling two values to splitting walls and identity into two settings; the recommendation makes the yolo account a fourth value of the confinement dial."
vantage:
  status-chip: true
---

# What to call the macOS middle ground

**Status:** 2026-11-03, second round after the owner's review of the first (`c4bb08284`). Draft; nothing renamed. The owner asked: *"why did we stop calling it a
guest? where did account mode come from? have the designer come up with a few clear terminology
plans for this area."* Origins come from `git log -S` on the tree at `ea3b0dc1e`.

**Needs your ruling:** [OQ-MT1](#OQ-MT1), [OQ-MT3](#OQ-MT3), [OQ-MT5](#OQ-MT5). **Ruled 2026-11-03:** [OQ-MT2](#OQ-MT2), [OQ-MT4](#OQ-MT4), MT-R1 and MT-R2 ([ledger](#decision-ledger)).

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

**The facts every plan has to fit.**

- **Two things vary, not one.** The *walls* (a container, the OS sandbox, or nothing) and the
  *identity* (you, or a separate user yolo owns). Today's dial names only the walls, and the
  macOS mode setting carries the identity off to one side.
- **The identity choice may reach Linux.** The planned Linux guest (bwrap plus Landlock) runs as
  you. The owner raised a Linux mode that runs the agent as a separate yolo user too, so every plan
  below shows how it spells that on both operating systems.
- **Same walls plus a separate user is strictly stronger.** The yolo account runs the same Seatbelt
  profile as running as you ([MM-R2](macos-user-model.md#decision-ledger)), and adds a different
  Unix user. So the two choices order cleanly: jail, then the yolo account, then you in the OS
  sandbox, then the host. The macOS guest preset in the code already says "a separate user
  (credential boundary) + Seatbelt" ([`confinement.go`](../../internal/render/confinement.go)).
- **`host` also runs as you.** What separates "you, in the OS sandbox" from `host` is the profile,
  so no name may lean on "you" alone.
- **No value is called a bare "account".** Running as you is an account too
  ([OQ-MT2](#OQ-MT2)). The separate user is **the yolo account** in prose and `yolo-account` in
  config ([OQ-MT4](#OQ-MT4)); on a Mac it is `_yolojail`, and its Linux name is the Linux
  design's to choose.

## 2. The plans

Each plan maps the seven concepts in play. The 2026-11-03 rule that old configs launch unchanged
covers only the switch to TOML ([legacy configs keep
working](legacy-config-compat.md#1-the-one-rule)), so it does not bind a rename. A renamed
spelling can either be read as an alias, which `yolo config migrate` rewrites, or be refused with a
message naming the new spelling, as a renamed pack is ([PE-D7](provider-enablement.md#PE-D7)). The
costs below assume the alias; either works with any plan.

Every plan shares these, so the tables leave them out: `runtime = "macos-user"` stays the macOS
mechanism ([OQ-MT3](#OQ-MT3)); `yolo macos-setup` and its siblings keep their names, and their help
says they create or remove the yolo account; "sandbox" names only Seatbelt; "guest" in code keeps
meaning "inside the sandbox" wherever the plan still has a `guest` value; `SandboxUser` and
`SandboxHome()` become `AccountUser` and `AccountHome()`.

### Plan 1: keep the key, respell the values

*Idea:* the smallest change that obeys both rulings: `macos_user_mode = "you" | "yolo-account"`.

| Concept | Config | CLI | Messages and docs |
| :--- | :--- | :--- | :--- |
| You, in the OS sandbox | `confinement = "guest"`, `macos_user_mode = "you"` (default after the flip) | `--at guest` | "guest, running as you" |
| The yolo account, in the OS sandbox | `confinement = "guest"`, `macos_user_mode = "yolo-account"` | none | "guest, running as the yolo account" |
| Who may choose | user config only, as today | — | — |
| Linux, as you | `confinement = "guest"` | `--at guest` | as on a Mac |
| Linux, as the yolo account | a second key, `linux_user_mode = "yolo-account"`, or a rename then | none | as on a Mac |

*Pros:* one value respelled; no new concept. *Cons:* the key's name is macOS-only, so the Linux
mode forces the rename later anyway; identity stays off to one side of the dial. *Cost:* aliases
for `"self"` and `"account"`; the launch disclosure and the refusal line that spell them; guide
pages.

### Plan 2: guest is the one name; who it runs as is a guest setting (A and B merged)

*Idea:* the notch stays three values; one cross-platform guest setting picks the identity:
`guest_runs_as = "you" | "yolo-account"`.

| Concept | Config | CLI | Messages and docs |
| :--- | :--- | :--- | :--- |
| You, in the OS sandbox | `confinement = "guest"` (default `guest_runs_as = "you"`) | `--at guest` | "the guest runs as you" |
| The yolo account, in the OS sandbox | `confinement = "guest"`, `guest_runs_as = "yolo-account"` | none | "the guest runs as the yolo account" |
| Who may choose | user config only, as today ([MMP-D3](macos-user-model-plan.md#MMP-D3)) | — | — |
| Linux, as you | `confinement = "guest"` | `--at guest` | as on a Mac |
| Linux, as the yolo account | `guest_runs_as = "yolo-account"`, the same line | none | as on a Mac |

*Pros:* reads as a sentence; one line works on both systems; keeps the machine-wide choice where
it is. *Cons:* the stronger choice hides in a side setting, so the footer and `yolo describe` say
`guest` for two different boundaries; there is no per-launch flag for it. *Cost:* one key aliased
(`macos_user_mode`, `self` → `you`, `account` → `yolo-account`); the same messages and pages as
Plan 1.

### Plan 3: the yolo account is a notch of its own (recommended)

*Idea:* the dial gets the fourth value the ordering already implies: `jail` / `yolo-account` /
`guest` / `host`. `guest` keeps meaning "you, in the OS sandbox", the meaning running as you by
default already gives it.

| Concept | Config | CLI | Messages and docs |
| :--- | :--- | :--- | :--- |
| You, in the OS sandbox | `confinement = "guest"` | `--at guest` | "guest: you, inside the OS sandbox, no container" |
| The yolo account, in the OS sandbox | `confinement = "yolo-account"` | `--at yolo-account` | "the yolo account: a separate user yolo owns, inside the OS sandbox" |
| Who may choose | whoever may set `confinement` ([OQ-MT5](#OQ-MT5)) | per launch | the footer names the notch |
| Linux, as you | `confinement = "guest"` | `--at guest` | as on a Mac |
| Linux, as the yolo account | `confinement = "yolo-account"` | `--at yolo-account` | as on a Mac |

*Pros:* the breakdown the dial was built for, its notches named by the agent's relationship to
the machine; one spelling on both systems; the footer, `yolo describe` and the briefing state the
identity with no extra key; the code's macOS guest preset becomes this notch as written. *Cons:* a
four-value dial to explain; a notch whose name is a noun phrase beside three single words; until
the Linux mode exists, `yolo-account` is refused on Linux by name, as Linux `guest` is today.
*Cost:* a new dial value and preset, `--at` and the footer; the charter's table; the alias
`macos_user_mode = "account"` → `confinement = "yolo-account"` wherever the setting was `guest`
or `macos-user`; `"self"` becomes a no-op.

### Plan 4: guest regains its first meaning, and running as you gets a new notch

*Idea:* the July name meant "a guest account: its own identity"
([why `guest`](../reference/what-yolo-is.md#why-guest-not-sandbox)), so give it back that meaning:
`jail` / `guest` (the yolo account) / `fenced` (you, in the OS sandbox) / `host`.

| Concept | Config | CLI | Messages and docs |
| :--- | :--- | :--- | :--- |
| You, in the OS sandbox | `confinement = "fenced"` | `--at fenced` | "fenced: you, inside the OS sandbox" |
| The yolo account, in the OS sandbox | `confinement = "guest"` | `--at guest` | "guest: the yolo account, inside the OS sandbox" |
| Who may choose | whoever may set `confinement` ([OQ-MT5](#OQ-MT5)) | per launch | the footer names the notch |
| Linux, as you | `confinement = "fenced"` | `--at fenced` | as on a Mac |
| Linux, as the yolo account | `confinement = "guest"` | `--at guest` | as on a Mac |

*Pros:* every definition in today's guide becomes true again; "guest" means a guest. *Cons:* it
changes what an existing `confinement = "guest"` line will do. Today that line runs as
`_yolojail`, and after the [default flip](macos-user-model.md#OQ-MM10) it would run as you; under
this plan it stays on the yolo account, so the flip skips exactly the users who chose the notch by
name. The default macOS middle ground becomes a brand-new word. *Cost:* a new value and preset;
the code's "guest" identifiers, which today mean the sandbox side in both identities, renamed; the
guide's guest section rewritten.

### Plan 5: walls and identity as two settings

*Idea:* the true grid: `confinement = jail | guest | host` names the walls only, and a top-level
`runs_as = "you" | "yolo-account"` names the identity, wherever that pair is meaningful.

| Concept | Config | CLI | Messages and docs |
| :--- | :--- | :--- | :--- |
| You, in the OS sandbox | `confinement = "guest"` | `--at guest` | "guest, as you" |
| The yolo account, in the OS sandbox | `confinement = "guest"`, `runs_as = "yolo-account"` | `--at guest --as yolo-account` | "guest, as the yolo account" |
| The yolo account, no sandbox | `confinement = "host"`, `runs_as = "yolo-account"`: a new cell, the second-account-only model macos-user started with | `yolo host --as yolo-account` | "host, as the yolo account" |
| Linux, as you | `confinement = "guest"` | `--at guest` | as on a Mac |
| Linux, as the yolo account | `runs_as = "yolo-account"`, the same line | `--as yolo-account` | as on a Mac |

*Pros:* each setting means one thing, and the grid also places a separate user with no sandbox. *Cons:* two settings to combine, which the
[fill-the-matrix rule](../reference/fill-the-matrix-principle.md) warns against as a front door;
`jail` with `runs_as` means nothing and is refused; a new host cell to build or refuse. *Cost:*
the key aliased as in Plan 2; a new flag; the host cell's refusal or build.

## 3. Recommendation: Plan 3

Plan 3 is the breakdown the owner pointed at, with the fewest moving parts. Identity really is a
step on the confinement ladder, since the yolo account is the same walls plus a credential
boundary, so it belongs on the dial rather than in a side setting. Its spelling is the same on
macOS and Linux, and the footer and `yolo describe` then say which boundary is in force. It keeps
`guest` for what the default already makes it mean, so no existing line changes behavior beyond
what [OQ-MM10](macos-user-model.md#OQ-MM10) ruled. Plan 4 is the same idea with the names swapped,
and it breaks that: the users who typed `guest` would stop following the flip. Plan 2 is the
fallback if a fourth notch is one too many; Plan 1 only defers the rename to the day Linux needs
it; Plan 5 is the honest grid but makes the user assemble it.

## 4. Open Questions

1. 💬 **OQ-MT1: Which terminology plan?**

   The answer decides what a Mac user types to get the yolo account, and what a Linux user will
   type for the same thing later.

   - **1 — Keep the key:** `macos_user_mode = "yolo-account"`.
   - **2 — A guest setting:** `guest_runs_as = "yolo-account"`.
   - **3 — A fourth notch:** `confinement = "yolo-account"`.
   - **4 — Guest is the account:** `confinement = "guest"`, and `fenced` for you.
   - **5 — Two settings:** `runs_as = "yolo-account"` beside `confinement`.

   <!-- vantage: question id=OQ-MT1 leaning="Plan 3: identity is a step on the ladder, the spelling is the same on both systems, and no existing line changes beyond the ruled default flip." -->

   _Leaning:_ Plan 3. Identity is a step on the ladder, the spelling is the same on both systems,
   and no existing line changes beyond the ruled default flip. A Mac user who wants the stronger
   boundary for one launch types `yolo --at yolo-account -- claude`.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-MT3: Should `runtime = "macos-user"` be renamed now?**

   Once the default runs as you, the name describes a macOS user that is not there. But it is the
   value every existing user of this backend typed, and every plan makes `confinement` the front
   door, so new users need not meet it.

   - **Keep it**, documented as "the mechanism behind guest and the yolo account on a Mac".
   - **Rename it**, reading `macos-user` as an alias.

   <!-- vantage: question id=OQ-MT3 leaning="Keep it: with confinement as the front door the runtime value fades from view, and a rename buys a second spelling of a value every existing user has." -->

   _Leaning:_ keep it. With `confinement` as the front door the runtime value fades from view, and
   a rename buys a second spelling of a value every existing user has.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 **OQ-MT5: Under Plans 3 and 4, may a workspace config choose between you and the yolo account?**

   Today only the user config picks the identity, because a workspace is agent-editable
   ([MMP-D3](macos-user-model-plan.md#MMP-D3)). A notch is something a workspace may set, so these
   plans let it. For example, a client repository's `yolo-jail.toml` says
   `confinement = "yolo-account"`, and a teammate's Mac that never ran `yolo macos-setup` stops with
   the setup command.

   - **Yes, through the config-change approval** a workspace's `confinement` edit already takes.
     Choosing the yolo account only strengthens the boundary; choosing `guest` over it weakens
     it, and the approval prompt names that.
   - **Only the user config may set either value**, so the dial gains a value a workspace cannot
     use.

   <!-- vantage: question id=OQ-MT5 leaning="Yes, through the existing approval: a project that wants the stronger boundary should be able to say so, and the weakening direction is already gated." -->

   _Leaning:_ yes, through the existing approval. A project that wants the stronger boundary should
   be able to say so, and the weakening direction is already gated.

   **Answer:**
   > _(empty — fill in when decided)_

## Decision Ledger

| ID | Ruling | Date | Settled in |
| :--- | :--- | :--- | :--- |
| <a id="OQ-MT2"></a>[OQ-MT2](#OQ-MT2) | No value is called a bare `account`: running as you is an account too. The owner: *"I hate 'account' here because 'self' is also an account."* The separate user's value is `yolo-account`. | 2026-11-03 | [§1](#1-the-problem-every-term-in-use-today) |
| <a id="OQ-MT4"></a>[OQ-MT4](#OQ-MT4) | `_yolojail` is **the yolo account** in messages and docs. The owner: *"I like the YOLO account here … instead of account, just use YOLO account."* | 2026-11-03 | [§1](#1-the-problem-every-term-in-use-today) |
| MT-R1 | Naming by mechanism (notch `sandboxed`, runtime `seatbelt`, `run_as = "me"`) is rejected. The owner: *"don't like."* | 2026-11-03 | [§2](#2-the-plans) |
| MT-R2 | Every plan is tested against a future Linux mode that runs the agent as a separate yolo user. The owner, on the account as its own notch: *"what if we add a Linux guest user mode? then we wouldn't want it to be organized in the way it is right now."* | 2026-11-03 | [§2](#2-the-plans) |
