---
title: "The configurable workspace root, and why the root is a deny prefix"
date: 2026-09-16
status: in-review
tags: [macos-user, seatbelt, workspace, isolation, config, design]
summary: "macos-user hardcodes /Users/Shared/yolo as the only place a workspace may live, and the obvious relaxation — let the user configure the root — silently removes the sandbox's only protection against reading their OTHER projects. The reason is that the Seatbelt profile's read policy is (allow default) with a deny on /Users, so sibling projects are hidden by WHERE they sit rather than by anything the root does. The proposal is to derive a second read-deny from the configured root, which makes an arbitrary root safe and also closes a hole that exists under today's default. Includes the four lexical bypasses in the current neutral-ground check and the whitelist that closes them."
vantage:
  status-chip: true
---

# The configurable workspace root, and why the root is a deny prefix

**Status:** DESIGN, 2026-09-16 — four questions open, all in [§8](#8-open-questions). The finding in
[§3](#3-the-finding-reads-are-allow-default) is what makes this a design rather than a config-key
ticket, and it is verified against the profile generator rather than reasoned from the docs.

**Scope: `macos-user` only.** The container backends bind the workspace at a fixed destination and
place no restriction on where it lives on the host, so none of this applies to them. On `macos-user`
there is no mount and no namespace: the agent runs as a second local account reading the same
filesystem you do, and the Seatbelt profile is the whole boundary.

**Reads with:** [`workspace-path-mirroring.md` §12.8](workspace-path-mirroring.md) (the
neutral-ground ruling this builds on), [`macos-user-nix-and-features.md`](../reference/macos-user-nix-and-features.md)
(the backend as built), [`macos-user-home-tiers.md`](macos-user-home-tiers.md) (where per-workspace
state lives, which is a different question — see [§7](#7-what-this-does-not-propose)).

## 1. What this decides

Today a `macos-user` workspace must live under `/Users/Shared/yolo`, and that path is a Go constant
(`macosuser.SharedRootDefault`). Two changes are wanted, and they are independent:

1. **Let the root be configured**, so a team or a person can keep projects somewhere else.
2. **Make the check a closed whitelist**, so paths that are neither the root nor a home stop being
   accepted by accident ([§5](#5-the-tightening-is-separable-and-cheaper)).

The second is cheap and wants doing regardless. The first looks equally cheap and is not, because of
[§3](#3-the-finding-reads-are-allow-default).

## 2. What the profile actually does

`macosuser.SeatbeltProfile` emits SBPL with `(allow default)` at the top and targeted denies below
it, last-match-wins. Reduced to its boundaries, with the workspace as `WS` and the sandbox account
home as `HOME`:

```scheme
(allow default)

;; boundary 1 — writes
(deny  file-write* (subpath "/"))
(allow file-write* (subpath WS) (subpath HOME)
                   (subpath "/tmp") (subpath "/private/tmp")
                   (subpath "/var/folders") (subpath "/private/var/folders")
                   (subpath "/dev"))

;; boundary 2 — other people's homes, and your other projects
(deny  file-read* (subpath "/Users"))
(allow file-read* (literal "/Users") (literal "/Users/Shared")
                  <one (literal) per intermediate dir of WS>
                  (subpath WS) (subpath HOME))

;; plus: /Volumes except the boot volume, raw disk and bpf devices, /Library/Keychains
```

**Boundary 1 is anchored at `/` and is therefore root-agnostic.** Writes are denied everywhere and
re-allowed for the workspace, so wherever the workspace lives, the agent can only write inside it
(plus the temp dirs and its own home). Moving the root does not weaken this at all.

**Boundary 2 is anchored at `/Users`,** and that is the entire subject of this document.

## 3. The finding: reads are `(allow default)`

The profile does not deny reads and then allow a list. It **allows** reads and then denies four
regions: `/Users`, `/Volumes` (minus the boot volume), the raw-disk and bpf devices, and
`/Library/Keychains`. Everything else on the disk is readable — `/opt`, `/Library`,
`/Applications`, `/nix`, `/etc`, `/private/var`, all of it.

So the reason the agent cannot read your *other projects* is not that the shared root does anything.
It is that your other projects sit under `/Users`, which is denied, and only this workspace is
re-allowed out of it. The protection is a property of **where the projects are**, and the shared
root's only contribution is happening to be inside the denied region.

> [!IMPORTANT]
> **Move the root out of `/Users` and every sibling project becomes readable, with nothing in the
> profile, the launch or `yolo check` saying so.** A workspace at `/srv/projects/alpha` leaves
> `/srv/projects/beta` covered by `(allow default)`. The agent still cannot *write* it (boundary 1
> holds), and it can already read most of the disk — but the one region that was protected is the
> one the user just moved their work into.

That is why this is not a config-key ticket. The relaxation people ask for silently converts a
protected location into an unprotected one, which is the shape
[`seatbelt.go`](../../internal/macosuser/seatbelt.go) already names about itself: *"a security key
that lies is worse than one that refuses."*

### 3.1 A root inside `/Users` but outside `/Users/Shared` does not launch

`/Users/yolo` looks like the conservative choice — inside the denied region, not a user home — and
it is refused. `macosuser.HomeContaining` treats **any direct child of `/Users` other than
`Shared`** as a home, so `/Users/yolo/alpha` is rejected as living inside a home directory. The
profile's ancestor-literal helper is hardcoded the same way: it skips any path that is not under
`/Users/Shared/`, so such a workspace would also receive no traversal grants and tools that walk up
to a repository boundary would fail on the intermediate directories.

**So the root has exactly three shapes, and they are not equally good:**

| Root | Writes | Sibling projects | Launches? |
|---|---|---|---|
| Under `/Users/Shared/` (today's default) | confined | **not readable** | yes |
| Elsewhere under `/Users` (`/Users/yolo`) | confined | not readable | **no** — refused, and no traversal grants |
| Outside `/Users` (`/opt/src`, `/srv/projects`, a `/Volumes` mount) | confined | **readable, silently** | yes |

## 4. The proposal: derive a second deny from the root

The two things boundary 2 currently does are unrelated, and collapsing them into one `/Users` deny
is what couples the root's location to the isolation. Separate them:

```scheme
;; other people's homes — unchanged, and independent of the root
(deny  file-read* (subpath "/Users"))
(allow file-read* (literal "/Users") (literal "/Users/Shared") …)

;; your other projects — derived from the configured root
(deny  file-read* (subpath ROOT))
(allow file-read* <one (literal) per intermediate dir of WS under ROOT>
                  (subpath WS))
```

With that, an arbitrary root is as protected as `/Users/Shared/yolo` is today, and the ancestor
helper's base becomes the configured root instead of a constant. Two properties worth noting:

- **It is strictly better than the status quo even for the default root.** Today a sibling project
  under `/Users/Shared/yolo` is protected only *incidentally*, by the `/Users` deny. Under the
  proposal it is protected on purpose, by a rule that names the thing it is protecting.
- **It makes the config key honest.** The key then changes where projects live without changing what
  the sandbox can read, which is what a user assumes a path setting does.

The cost is that the profile grows one derived deny and one derived allow, and that
`ancestorLiterals` takes the root as a parameter rather than a constant. Both are small; the
argument for the doc is that nobody would have known to make them.

## 5. The tightening is separable, and cheaper

Independently of the key, the workspace check is an **open blacklist** — *"is this path inside a user
home?"* — where it wants to be a **closed whitelist** — *"is this path under the root?"*. A
blacklist admits anything nobody thought to exclude, and `HomeContaining` is a pure lexical compare
(`parent == "/Users"`), so four spellings pass it today. All four are confirmed by reading the
function; that each reaches the same bytes on real macOS is reasoned from platform behaviour and has
not been measured on hardware:

| Spelling | Why it passes | Why it is the same directory |
|---|---|---|
| `/USERS/alice/x`, `/users/alice/x` | `"/USERS" != "/Users"` | APFS is case-insensitive by default |
| `/System/Volumes/Data/Users/alice/x` | the parent is `/System/Volumes/Data/Users` | `/Users` is a firmlink to it |
| `/var/root/x` | not under `/Users` at all | it is root's home, which the check does not know about |

A whitelist closes all four at once and cannot be bypassed by a new spelling of a home, because it
stops asking about homes. `HomeContaining` still has work to do elsewhere and should stay for those
callers — `macos-fix-permissions` (which must be able to retrofit a root *before* it is the
configured one) and the capture store, which lives at a deliberate **sibling** of the shared root
that a tightened workspace check would refuse.

> [!WARNING]
> **Sequence the tightening before anyone relies on the blacklist.** A path like `/opt/src/alpha`
> launches today, so tightening later withdraws something people may already have built a workflow
> on. Landing the whitelist while the accepted set is still the default root costs nobody anything;
> landing it afterwards is a breaking change.

## 6. What else the key touches

Collected so an implementer does not discover them one at a time.

| Surface | What it needs |
|---|---|
| `macosuser.SharedRootDefault` | stays as the default; call sites stop treating it as the only value |
| `SharedRootProvisionCommands` | **already takes a `root` parameter** and defaults to the constant — the one caller passes `""`. The provisioning half is built |
| `MacosSetup`, `MacosFixPermissions` | take no config today; they need the resolved root threaded in |
| `ancestorLiterals` | its `const base = "/Users/Shared/"` becomes the root ([§4](#4-the-proposal-derive-a-second-deny-from-the-root)) |
| The refusal message | names the *configured* root, not the constant |
| `yolo check` | has no shared-root section; it should probe that the root exists, is ACL'd, and contains the workspace |
| Config scope | the key must be **user-scope only** and read from the user file directly, or a committed workspace config authorises its own legality and [§5](#5-the-tightening-is-separable-and-cheaper) is undone |
| Every test and fixture hardcoding `/Users/Shared/yolo` | a seam, not a rewrite — see the deferral option in [`OQ-CW1`](#OQ-CW1) |

## 7. What this does not propose

- **Not a change to where per-workspace state lives.** `<workspace>/.yolo/home` stays; the Seatbelt
  profile's `(subpath WS)` allow already covers it and `macos-fix-permissions` already walks it.
  That question belongs to [`macos-user-home-tiers.md`](macos-user-home-tiers.md), and a dotted
  sibling of the projects root is specifically a bad answer, because `.yolo` holds `launch.log`
  (which tees a `--dry-run`'s full argv, credentials included) and verbatim archives of the user's
  own pre-yolo agent config.
- **Not a relaxation of neutral ground.** A workspace inside a user home stays refused. The recorded
  reason still holds: the alternative threads traversal ACLs through `/Users/<you>`, which is
  *"exactly where a stray grant silently exposes `~/.ssh`"* (`29b00697`).
- **Not a claim that the sandbox is otherwise tight.** It reads most of the disk by design, sees the
  full host process table, and shares `/tmp` with the human. This document is about not making one
  of the few real read boundaries disappear by accident.
- **Not the single-shared-home question.** That `~/.ssh` and `~/Library` are machine-wide on this
  backend is a separate, already-withdrawn piece of SandVault parity
  ([`OQ-WP12`](workspace-path-mirroring.md#OQ-WP12)).

## 8. Open questions

1. <a id="OQ-CW1"></a>💬 **[`OQ-CW1`](#OQ-CW1) — Config key now, or a plumbed seam first?**

   <!-- vantage: oq id=OQ-CW1 leaning="Plumb the root through as a parameter and ship the derived deny plus the whitelist; hold the config key until someone needs a second root." -->

   The security work in [§4](#4-the-proposal-derive-a-second-deny-from-the-root) and
   [§5](#5-the-tightening-is-separable-and-cheaper) needs no config key — it needs the root to be a
   *parameter* rather than a constant. The key adds schema, validation, user-scope enforcement, a
   `yolo check` section, and a sweep of every fixture. `CaptureOptions.CaptureRoot` is the shipped
   precedent for stopping at the seam, and its own docstring says so: *"a test seam and an escape
   hatch, not a config key."*

   _Leaning:_ seam first; key when a second root has a claimant.

2. <a id="OQ-CW2"></a>💬 **[`OQ-CW2`](#OQ-CW2) — Is "outside any user home" enforced lexically or by enumerating real homes?**

   <!-- vantage: oq id=OQ-CW2 leaning="Lexically, against the configured root — a whitelist is what makes the four bypasses unreachable, and enumerating homes re-opens the blacklist it replaces." -->

   A whitelist answers *"is this under the root?"* and needs no notion of a home at all, which is
   what closes [§5](#5-the-tightening-is-separable-and-cheaper)'s four spellings. But a *configured*
   root still has to be validated as not-inside-a-home when it is set, and that validation is the
   blacklist again, now applied to one path at configuration time instead of every path at launch.
   Open: does that validation enumerate real home directories (correct, needs the directory service)
   or stay lexical (cheap, and wrong for an unusual home location)?

   _Leaning:_ lexical for the launch check, and decide the root's own validation with the key.

3. <a id="OQ-CW3"></a>💬 **[`OQ-CW3`](#OQ-CW3) — Does the derived deny cover the root, or the root's parent?**

   <!-- vantage: oq id=OQ-CW3 leaning="The root. Denying the parent protects unrelated neighbours the user did not ask yolo to hide and makes the rule harder to predict." -->

   Denying `(subpath ROOT)` hides sibling projects. Denying the root's *parent* would also hide
   whatever else lives beside the root — protective, but it extends yolo's policy over directories
   the user never mentioned, and on a root like `/srv/projects` the parent is `/srv`.

   _Leaning:_ the root, and say in the launch what became unreadable.

4. <a id="OQ-CW4"></a>💬 **[`OQ-CW4`](#OQ-CW4) — Does a root outside `/Users` need its own ACL story, or does the existing one carry?**

   <!-- vantage: oq id=OQ-CW4 leaning="Unknown until measured on hardware; the inheriting-ACE mechanism is not obviously /Users-specific, but /Users/Shared's own permissions are unusual and nothing has been run anywhere else." -->

   Workspace sharing is granted by inheriting `chmod +a` ACEs rather than ownership, and
   `macos-fix-permissions` applies them to any resolved non-home target — implemented, never
   measured for a non-default root. `/Users/Shared` has unusual stock permissions, so whether an
   arbitrary root needs extra setup (or refuses on a filesystem without ACL support, such as some
   `/Volumes` mounts) is unestablished.

   _Leaning:_ measure one non-default root on hardware before the key ships.

## 9. Decision ledger

| Ruling | Why it stays |
|---|---|
| **Neutral ground itself — a workspace never lives inside a user home** | `29b00697`'s reason is durable and independent of everything above: the alternative routes layered access control through the most sensitive directory on the machine. [§3](#3-the-finding-reads-are-allow-default) strengthens it rather than reopening it |
| **`/Users/Shared/yolo` remains the default** | It is inside the denied region, `macos-setup` provisions it, and it is where every existing user's projects already are. Changing the default would be a migration for no gain |
| **A3's 2026-07-23 drop of `macos_shared_root` is not a rejection of this design** | That decision removed a *dangling hint* — the error message advertised a key that was read nowhere — and its stated reason was *"`/Users/Shared/yolo` … covers the real need."* That is a judgement about demand, not about mechanism, and it is the judgement being revisited. Nothing in A3 argued the root should not be configurable |
| **Writes are not part of this problem** | Boundary 1 is anchored at `/` and re-allows the workspace by path, so it holds for any root. Only the read boundary is coupled to `/Users`, and conflating the two is what made this look like a cosmetic setting |
