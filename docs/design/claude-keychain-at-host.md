---
title: "Claude's sign-ins in a Mac's Keychain at yolo host: edit one entry of Claude's own item, or leave it alone"
date: 2026-11-12
status: in-review
stage: DESIGN
next: "Rule OQ-KH1 and OQ-KH2; the owner's Mac agent runs W-KH1 to W-KH3, which size OQ-KH2"
depends-on: []
tags: [design, macos, keychain, claude, mcp, oauth, credentials, yolo-host]
summary: "On a Mac, host Claude keeps its login and its MCP sign-ins in one login-Keychain item, so the one MCP sign-in that Linux yolo host adopts from ~/.claude/.credentials.json has nothing to adopt from there. This design keeps the Linux mechanism and changes only the store adapter: the launcher reads Claude's own Keychain item through /usr/bin/security, which the item already trusts, and writes back one mcpOAuth entry with the same command Claude itself uses, under Claude's own write lock. The write is bounded by what security takes on stdin, about 4 KB, and yolo never puts a secret on an argv. Two rulings are owed: whether yolo may edit Claude's own Keychain item at all, and what happens when the item is too large to write that way."
vantage:
  status-chip: true
---

# Claude's sign-ins in a Mac's Keychain at `yolo host`

**Status:** 2026-11-12. Nothing built. Claude's storage code read from the Claude Code 2.1.323
bundle installed in this jail (its JavaScript is the same on every platform; [KE3](#KE3)); the
Keychain item's facts come from the [2026-10-25 Mac lab report](../lab-reports/2026-10-25-mac-claude-login-session.md)
and the sources in [Appendix A](#appendix-a--evidence). No Claude was run and no Keychain was
touched for this design.

> **In short.** The one MCP sign-in at a Mac's `yolo host` is the Linux mechanism with a second
> store adapter: Claude keeps its MCP sign-ins in one Keychain item that already trusts
> `/usr/bin/security`, so yolo can read it and write back one entry exactly as Claude does, under
> Claude's own lock. What it cannot do is write an item larger than `security` takes on stdin
> without putting the secret on a command line.

**Why it matters.** [OQ-MA10](mcp-auth-sharing.md#OQ-MA10) put the one MCP sign-in at `yolo host`
on Linux by default and left the Mac unsupported until this design exists. Today a Mac user who
signed in to Slack in host Claude sees the launch say the one sign-in is not kept there.

**The shape.** One new *Keychain source store* beside the file source of the
[MCP sign-in broker](mcp-auth-sharing.md#93-adopting-a-sign-in-an-agent-already-has); the
broker's adoption, refresh and sign-out rules are unchanged ([§3](#3-the-design)).

**Cost.** yolo writes into another program's Keychain item, which nothing in yolo does today
([§3.4](#34-what-yolo-never-does)), and the tier is lost on a Mac whose item is too large to write
([OQ-KH2](#OQ-KH2)).

**Start at [§2](#2-what-claude-does-on-a-mac)** — Claude's own Keychain path decides everything
after it.

**Needs your ruling:** [OQ-KH1](#OQ-KH1), [OQ-KH2](#OQ-KH2).

**Reads with:** [`mcp-auth-sharing.md`](mcp-auth-sharing.md) (the broker this adds a store to, and
[§10](mcp-auth-sharing.md#10-parity-every-backend-and-notch)'s Mac row it closes),
[`keychain-from-a-jail.md`](keychain-from-a-jail.md) (the container route to the host keychain,
and the rule that its host half touches no item without yolo's marker),
[`claude-login-without-interception.md`](claude-login-without-interception.md#CL-D27) (the host
Claude *login*, which this design leaves where CL-D27 put it), and the plan seed,
[`claude-keychain-at-host-plan.md`](claude-keychain-at-host-plan.md) (a sketch: not a hand-off,
incomplete while either question is open).

---

## 1. Scope

**In scope:** the one MCP sign-in ([`sign_in = "once"`](mcp-auth-sharing.md#93-adopting-a-sign-in-an-agent-already-has))
for `yolo host -- claude` on macOS, where host Claude's store is its Keychain item.

**Not in scope** (non-goals):

- **Claude's login (`claudeAiOauth`).** Host Claude keeps its own login
  ([OQ-NC7](../plans/notch-convergence.md#7-decision-ledger)); the opt-in credential view moves
  the whole store to a file yolo manages and already hides the view's Keychain item from Claude
  ([CL-D27](claude-login-without-interception.md#CL-D27),
  [the Mac follow-ups](mac-claude-lab-followups.md)). Nothing here reads, writes or refreshes
  `claudeAiOauth`; a write-back carries it through byte-for-byte.
- **A host view.** With the credential view switched on, host Claude's store is a file, and the
  tier follows the view's own rules ([ID-16](mcp-auth-sharing-plan.md#id-16)), not this design.
- **Other agents' Keychain stores.** Codex stays on its own store by
  [OQ-MA6](mcp-auth-sharing.md#decision-ledger); Copilot's macOS MCP sign-ins stay `unsupported`
  ([§10](mcp-auth-sharing.md#10-parity-every-backend-and-notch)).
- **Jails and macos-user.** Every container jail runs Linux Claude on a file, and macos-user's
  Claude uses its bridge file ([ID-20](mcp-auth-sharing-plan.md#id-20)).
- **A Claude yolo did not launch.** As on Linux ([ID-21](mcp-auth-sharing-plan.md#id-21)), the
  tier runs only for the length of a `yolo host -- claude` session.

### 1.1 Terms

| Term | Meaning | Origin |
| :--- | :--- | :--- |
| **Claude's item** | The generic-password item in the user's login Keychain that holds host Claude's whole secure storage, `Claude Code-credentials` by default | Claude Code ([KE1](#KE1)) |
| **Keychain source store** | The broker's adapter that reads and writes one `mcpOAuth` entry inside Claude's item. Not a keychain yolo owns, and not a sealed file | *(coined here)* |
| **stdin cap** | The longest `security -i` command line yolo will send, 4032 bytes, the threshold Claude itself uses ([KE4](#KE4)) | *(coined here)* |

## 2. What Claude does on a Mac

Read from the 2.1.323 bundle unless marked; [W-KH1](#w-kh1) to [W-KH3](#w-kh3) confirm each on a Mac.

1. **One item holds the whole store.** Claude's secure storage is one JSON object, `claudeAiOauth`
   and `mcpOAuth` together, read and written whole ([KE2](#KE2)). On macOS that object is one
   Keychain item; that `mcpOAuth` sits in it is INFERRED ([W-KH1](#w-kh1) lists its top-level keys).
2. **Its name follows the config directory.** Service `Claude Code-credentials` when neither
   `CLAUDE_SECURESTORAGE_CONFIG_DIR` nor `CLAUDE_CONFIG_DIR` is set; otherwise that name plus `-`
   and the first eight hex digits of the SHA-256 of the directory. Account: `$USER` if it is
   `[a-zA-Z0-9._-]+`, else `claude-code-user` ([KE1](#KE1)).
3. **It trusts `/usr/bin/security`.** Claude creates and reads the item through `security`, and the
   item's access list holds only `security`, so any same-user process reads it with no dialog
   (SOURCED, [KE5](#KE5)).
4. **It writes through `security -i`, then the argv.** `add-generic-password -U -a … -s … -X <hex>`
   goes on stdin when the command is at most 4032 bytes, and on the argv otherwise ([KE4](#KE4)).
   Hex doubles the JSON, so stdin carries a store of at most about 1.9 KB.
5. **Every write takes the file lock, Keychain or not.** Each read-modify-write runs inside one
   wrapper that takes `<storage dir>/.storage-write.lock` (proper-lockfile, ten retries, stale
   after 15 s), skipped only for a diskless launch ([KE2](#KE2), INFERRED: the skip's condition is
   a minified name, read as diskless because the same predicate also nulls the plain-text fallback
   path, which the Mac measurably uses). The storage dir is `CLAUDE_SECURESTORAGE_CONFIG_DIR`, else
   the config dir, `~/.claude` by default.
6. **Reads are cached for 30 s**, and a write re-reads first, so a write never starts from a stale
   cache ([KE2](#KE2)).

**What follows:** yolo can share Claude's lock, need open no dialog, and need hold nothing Claude
does not already expose to every same-user process. The one thing it cannot match without
breaking a standing rule is item 4's argv branch.

## 3. The design

### 3.1 The component

The [ID-21](mcp-auth-sharing-plan.md#id-21) host session — the broker's poll while
`yolo host -- claude` runs, then one last pass — gains a second source store on darwin. Everything
above the store is the broker's, unchanged: matching, the adoption check, moving the refresh token
out, the single refresher, write-back after each refresh, sign-out, the audit log.

| Operation | File source (Linux) | Keychain source (macOS) |
| :--- | :--- | :--- |
| Identify | the store directory | the service and account of [§2](#2-what-claude-does-on-a-mac) item 2, from the launched Claude's environment |
| Lock | `<dir>/.storage-write.lock` | the same lock, in the storage dir of [§2](#2-what-claude-does-on-a-mac) item 5 |
| Read | parse the file | `security find-generic-password -a <account> -s <service> -w`, stdout only |
| Write | temp file, `fsync`, rename | one `add-generic-password -U -a <account> -s <service> -X <hex>` on `security -i`'s stdin, within the stdin cap |
| Record's `source_dir` ([ID-18](mcp-auth-sharing-plan.md#id-18)) | the directory | the item, named by service and account, so a jail's bridge file never counts as this store |

**A write is a read-modify-write of one `mcpOAuth` entry, under the lock.** Every other key,
`claudeAiOauth` included, is carried through unchanged, as on Linux. The write is byte-for-byte the
command Claude issues for the same item, so the item's access list is whatever Claude's own
writes leave it.

### 3.2 Reads, outcomes and sign-out

| `security` result | Meaning to the broker |
| :--- | :--- |
| exit 0, JSON object | a clean read |
| exit 0, not a JSON object | unparsed: never written, never a sign-out ([ID-12](mcp-auth-sharing-plan.md#id-12)) |
| exit 44, item not found | a clean, empty store: entries adopted from this item are signed out ([OQ-MA4](mcp-auth-sharing.md#decision-ledger)), as for an absent file |
| exit 36 (no dialog allowed), 51, 128, timeout, anything else | unreadable: never a sign-out, and the session stops reading the Keychain ([§3.3](#33-failure-paths)) |

### 3.3 Failure paths

- **Locked Keychain at launch.** The first read may raise macOS's unlock dialog while the user is
  at the terminal. It waits up to 2 minutes, as the existing host Keychain read does. Unanswered,
  denied, or with no window server (an SSH session, exit 36): the launch prints one line and the
  tier is off for this session; the poll does not ask again, so no dialog returns every 30 s.

  ```text
  yolo host: MCP one sign-in is off for this session: macOS did not unlock your login keychain (Claude Code-credentials). slack signs in on its own until the next `yolo host -- claude`.
  ```

- **Lock held.** Claude's lock is taken as [ID-12](mcp-auth-sharing-plan.md#id-12) takes it; a pass
  that cannot take it skips the write and retries at the next poll.
- **Item too large.** A write whose command would exceed the stdin cap is not sent. What happens
  next is [OQ-KH2](#OQ-KH2).
- **Item disappears between read and write.** `-U` would recreate it; the write therefore re-reads
  under the lock and writes only when the item is still present.
- **Claude's config dir changed mid-session.** Not possible: the identity is fixed from the
  launched Claude's environment at session start.

### 3.4 What yolo never does

- Never puts a secret, or the hex of one, on an argv ([KC rule](keychain-from-a-jail.md#33-the-host-side-the-doorway-into-the-real-keychain)).
- Never reads, writes or deletes any Keychain item other than the launched Claude's own, and
  never lists the Keychain. This is the second named exception to "only items yolo created",
  after the host Copilot's login item ([KC-D17](keychain-from-a-jail.md#KC-D17)).
- Never deletes Claude's item, and never creates one that was absent.
- Never adds an application to the item's access list, and never calls Security.framework itself
  under [OQ-KH2](#OQ-KH2)'s leaning.
- Never runs at a host launch of any program but Claude, or when the credential view is on.

### 3.5 Disclosure

The ID-21 start line is reused with the store named, never a value:

```text
yolo host: MCP one sign-in: slack, adopted from Claude Code in your login keychain (Claude Code-credentials). yolo refreshes it while this session runs.
```

### 3.6 Existing state

A Mac with Claude signed in to Slack has a full `mcpOAuth` entry, refresh token included. The
first session adopts it and removes that refresh token from Claude's item, exactly as Linux does
to `~/.claude/.credentials.json`. A Claude the user later starts without yolo then has no refresh
token for that server and asks for a sign-in once its access token expires (12 hours for Slack);
that sign-in is adopted at the next `yolo host -- claude`. This is the Linux cost, unchanged.

### 3.7 What done looks like

1. On a Mac with host Claude signed in to Slack, `yolo host -- claude` prints the line of
   [§3.5](#35-disclosure), and `yolo mcp-auth status` names the item as the record's source.
2. After the first pass, [W-KH1](#w-kh1)'s key listing shows the Slack entry without
   `refreshToken`, and `claudeAiOauth` unchanged.
3. Codex in a jail uses the same Slack sign-in.
4. No `security` process ever shows the item's contents on its argv.
5. No macOS dialog appears at any launch on an unlocked Keychain.

### 3.8 Alternatives considered

| Alternative | Verdict |
| :--- | :--- |
| Move host Claude's whole store to a file (the credential view, or a private store seeded from the item) | Rejected for this design: the view changes whose *login* host Claude uses, which is CL-D27's opt-in, not this tier's; a private seeded store forks the user's login into two refreshers |
| A `security` shim on host Claude's PATH that fails the Keychain so Claude falls back to its file | Rejected: it moves the user's login out of the Keychain, which [OQ-MA6](mcp-auth-sharing.md#decision-ledger)'s reason forbids — do not relax another program's store |
| Call Security.framework directly (no cgo, dynamic import) | Held as [OQ-KH2](#OQ-KH2)'s option B |

### 3.9 Risks

| Risk | Mitigation |
| :--- | :--- |
| Claude does not take the lock for a Keychain write on macOS ([§2](#2-what-claude-does-on-a-mac) item 5 is INFERRED) | [W-KH2](#w-kh2) reads the darwin build; if it does not, a write races Claude's and the tier stays off at a Mac host until a new design |
| A Claude update renames the item or changes its encoding | A read that does not parse is never written and never a sign-out ([§3.2](#32-reads-outcomes-and-sign-out)) |
| Typical items exceed the stdin cap, so the tier rarely works | [W-KH1](#w-kh1) measures the size; it is the input to [OQ-KH2](#OQ-KH2) |

## 4. Open questions

1. 💬 <a id="OQ-KH1"></a>**OQ-KH1: May `yolo host` on a Mac edit the MCP sign-ins inside Claude Code's own Keychain item?**

   Dana signed in to Slack in Claude on her Mac, then runs `yolo host -- claude`. Today the
   launch says the one sign-in is not kept at `yolo host` and names `yolo -- claude`; her Codex
   jail cannot use that Slack sign-in. On Linux the same launch edits one entry of Claude's own
   credentials file, by your ruling of 2026-11-09. On a Mac that store is a Keychain item, and
   yolo has never written another program's Keychain item.

   - **A — Edit it, the way Claude does.** yolo reads the item and writes back one entry with
     the same `security` command Claude uses, under Claude's lock. Dana sees the adoption line
     and her jails share the Slack sign-in. The item's access list does not change.
   - **B — Leave it unsupported.** Dana keeps seeing the "not kept here" line; the one sign-in
     works only in jails, and host Claude signs in on its own.

   <!-- vantage: question id=OQ-KH1 leaning="A — it is the Linux ruling on the same store, and the item already trusts security, so yolo gains nothing Claude does not already expose." -->

   _Leaning:_ A — it is the Linux ruling on the same store, and the item already trusts security, so yolo gains nothing Claude does not already expose.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 <a id="OQ-KH2"></a>**OQ-KH2: What happens when Claude's Keychain item is too large for yolo to write safely?**

   Dana's item holds her Claude login and her Slack sign-in. Claude writes the item through
   `security`, sending it on stdin when it is short and on the command line when it is not,
   where for that moment any process of hers can read it. yolo's rule is never to put a secret
   on a command line, which limits it to an item of about 1.9 KB of JSON. Whether a typical item
   with one Slack sign-in fits is not measured yet.

   - **A — Stop for that session, and say so.** The launch prints `the one sign-in is off for
     this session: your Claude Code keychain item is too large to update without putting it on
     a command line`, and Slack signs in on its own until the item shrinks.
   - **B — Call the Keychain directly.** No size limit. macOS asks Dana to let `yolo` use the
     item; after *Always Allow*, `yolo` joins the item's access list, and each new `yolo` build
     likely asks again.
   - **C — Do what Claude does.** Use the command line above the limit; the token is visible to
     her other processes for the length of one `security` call.

   <!-- vantage: question id=OQ-KH2 leaning="A, weakly — it breaks no rule and adds no dialog; if the Mac measurement shows most items are too large, A is nearly B of the previous question and this should be re-asked." -->

   _Leaning:_ A, weakly — it breaks no rule and adds no dialog; if the Mac measurement shows most items are too large, A is nearly B of the previous question and this should be re-asked.

   **Answer:**
   > _(empty — fill in when decided)_

## Mac checks

For the owner's Mac agent, on any main commit at or after the one that adds this document: no yolo
code is involved yet. Each measures Claude's own behavior and changes no Keychain item but
[W-KH3](#w-kh3)'s throwaway. Never print a secret: every command below prints names, keys or byte
counts only.

### W-KH1

**Item identity, contents and size.** Settles [§2](#2-what-claude-does-on-a-mac)
items 1 to 3 and sizes [OQ-KH2](#OQ-KH2). **Setup:** host Claude signed in, and signed in to
Slack through its plugin (`/mcp` in a host `claude`). If Slack is not signed in, the agent records
that and stops; the sign-in is the owner's, at the Mac.

1. Name the item:

   ```sh
   security find-generic-password -s 'Claude Code-credentials' | grep -E '"(svce|acct)"'
   ```

   Expected: `"svce"<blob>="Claude Code-credentials"` and an `"acct"` line with your user name,
   with no dialog.
2. Its size in bytes:

   ```sh
   security find-generic-password -s 'Claude Code-credentials' -w | wc -c
   ```

3. Its top-level keys and its `mcpOAuth` keys, never values:

   ```sh
   security find-generic-password -s 'Claude Code-credentials' -w | python3 -c 'import json,sys; d=json.load(sys.stdin); print(sorted(d)); print(sorted(d.get("mcpOAuth",{})))'
   ```

   Expected: a list containing `claudeAiOauth` and `mcpOAuth`, then one key starting
   `plugin:slack:slack|`. If `json.load` fails, record the first 16 characters' class only
   (`hex` or `other`) and stop.

**Report:** the three outputs and the Claude version (`claude --version`). **Decides:** whether
[§2](#2-what-claude-does-on-a-mac) items 1–3 hold; the byte count against the 1.9 KB limit is [OQ-KH2](#OQ-KH2)'s stakes.

### W-KH2

**Claude takes its file lock around a Keychain write.** Settles
[§2](#2-what-claude-does-on-a-mac) item 5 by reading the darwin build:

```sh
B="$(readlink -f "$(command -v claude)")"
grep -a -o '.\{0,120\}\.storage-write.\{0,240\}' "$B" | head -3
```

Expected: text containing `if(await r,K())return await` just before the `.storage-write` lock
call, as in the Linux build. Then the import that names that `K` in the storage chunk:

```sh
grep -a -o 'import{F,K}from"[^"]*".\{0,400\}var hzr="-credentials"' "$B" | head -1
```

Minified names such as `K` and `hzr` are those of 2.1.323 and change between versions; if the
second pattern finds nothing, report the first command's output alone.

**Report:** both outputs, verbatim (they hold code, no secret). **Decides:** a Linux agent
compares them with the Linux build; if the guard is not there, or `K` turns out not to be the
diskless-launch check, [§3.9](#39-risks)'s first risk is real and the design stops.

### W-KH3

**`security -i`'s stdin limit, on a throwaway item.** Settles the stdin cap.

```sh
for n in 1900 2100; do h=$(head -c $n /dev/zero | tr '\0' 'a' | xxd -p | tr -d '\n'); printf 'add-generic-password -U -a yolo-kh3 -s yolo-kh3-probe -X %s\n' "$h" | security -i; echo "n=$n exit=$?"; security find-generic-password -a yolo-kh3 -s yolo-kh3-probe -w | wc -c; done
security delete-generic-password -a yolo-kh3 -s yolo-kh3-probe
```

Expected: `n=1900` stores 1900 bytes; `n=2100` (a 4.2 KB command) fails or stores something
shorter. **Report:** both lines and the read-back counts. **Decides:** the stdin cap's real value.

## Decision Ledger

| ID | Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="KH-D1"></a>KH-D1 | *Implementation decision.* The Keychain source reuses ID-21's session and the broker's code; only the store adapter is new | 2026-11-12 | [§3.1](#31-the-component) | — |
| <a id="KH-D2"></a>KH-D2 | *Implementation decision.* An absent item is a clean, empty store; every other failed read is unreadable, never a sign-out, and stops Keychain reads for the session | 2026-11-12 | [§3.2](#32-reads-outcomes-and-sign-out) | — |
| <a id="KH-D3"></a>KH-D3 | *Implementation decision.* A write re-reads under Claude's lock and never recreates an absent item | 2026-11-12 | [§3.3](#33-failure-paths) | — |

## Appendix A — Evidence

| ID | Kind | Claim | Source |
| :--- | :--- | :--- | :--- |
| <a id="KE1"></a>KE1 | SOURCED | Service and account naming | Claude Code 2.1.323 bundle, `var hzr="-credentials"` at byte 300148478: `GOe` and `SY` |
| <a id="KE2"></a>KE2 | SOURCED, INFERRED | One store object; the lock wrapper around every write; the 30 s read cache | 2.1.323 bundle at byte 300186544 (`fgn`, `Re`), and `var TTa=30000` before it |
| <a id="KE3"></a>KE3 | INFERRED | The bundle's storage JavaScript is the same on macOS | The macOS branches (`security`, `O()==="macos"`) are present in the Linux build; [W-KH2](#w-kh2) checks the darwin binary |
| <a id="KE4"></a>KE4 | SOURCED, MEASURED | `security -i` with hex on stdin up to 4032 bytes, the argv beyond | [keychain-from-a-jail E10](keychain-from-a-jail.md#E10); [lab report, defect 3](../lab-reports/2026-10-25-mac-claude-login-session.md#3-a-sandbox-login-cannot-save-in-account-mode-with-the-view-on-or-off-c1) |
| <a id="KE5"></a>KE5 | SOURCED | The item's access list holds only `/usr/bin/security` | [`macos-keychain-exposure-by-program.md`](../research/macos-keychain-exposure-by-program.md#table) |
