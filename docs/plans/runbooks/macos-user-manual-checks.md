# What only a Mac with a password can verify

**Audience:** whoever has a Mac and five minutes. Every item here is something no
test can reach, and the list is deliberately short — everything that *could* be
automated was, in `internal/entrypoint/darwinbootstrap_darwin_test.go` and
`internal/macosuser/staging_darwin_test.go`, both of which run on any Mac with no
privilege at all.

**What those cover, so you do not re-check it by hand:** the generated home (shims,
launcher dir, login rc files, mise config), a blocker actually executing and
refusing with its message and suggestion, the composed home overlay landing at its
destinations and replacing a removed pack's subtree, a missing overlay degrading to
a warning, the staging commands really running, the a+rX mode, the replace-not-merge
shape, and the J2 fresh-inode rule (re-stage, then exec).

What remains needs either root or a kernel, and is a handful of facts rather than a
mechanism.

> [!IMPORTANT]
> **Items 5-9 are NEW (2026-09-12) and none has ever been run.** Three things shipped that day
> — the per-workspace home layout, the non-container package FLOOR, and the confined
> provisioning STAGE — and every runtime claim about all three is unmeasured. Item 3 is worth
> re-running with them, for the reason that item states.
>
> **Read items 6-9 in order and stop at the first failure.** They are a dependency chain, not
> a list: no floor means no `mise` and no `npm`, which means the stage's first line fails,
> which means `mise_tools` and `lsp_servers` install nothing. A failure at item 6 explains
> every later one, and reporting them as four bugs would be reporting one.

> [!NOTE]
> **ITEMS 1-4 ALL PASSED on 2026-09-10** — one session on the maintainer's Apple Silicon
> Mac (macOS 26.5, arm64), host `yolo` `0.8.0+1293.g520e848d`, the first live
> `macos-user` session on that machine. Per-item results are recorded under each
> heading below, including the two places the measurement corrected this file.
>
> **This does not retire the runbook.** Every item is still the only instrument for
> its fact, and none of them is pinned by a test — so a change to the privilege
> transition, the Seatbelt profile, the native nix chain or content staging needs
> these four run again. Treat the results as a dated measurement, not a checkbox.
>
> ⚠ **An agent cannot run any of them.** `sudo -n true` reports `a password is
> required` and every macos-user argv leads with `sudo --user=_yolojail`, so an agent
> attempting a launch hangs on the prompt rather than failing. These four are a
> human's to run, which is what "needs either root or a kernel" means in practice.

---

## 1. The privilege transition

```console
$ YOLO_RUNTIME=macos-user yolo -- bash -lc 'whoami; pwd'
```

**Expect:** `_yolojail`, and the workspace path. One sudo prompt.

This is the whole irreducible remainder: `sudo` running at all, the `-u _yolojail`
switch landing, and the staged binary self-exec'ing as that user. Everything before
and after it is covered by the harnesses above.

**PASSED 2026-09-10.** It printed `_yolojail` and the workspace path after one
password prompt — `sudo` ran, the `-u` switch landed, and the staged binary
self-exec'd as that user (`internal/macosuser/runplan.go` builds both argvs).

## 2. Seatbelt is actually applied

```console
$ YOLO_RUNTIME=macos-user yolo -- bash -lc 'ls /Users/$(logname)/.ssh; ls /Library/Keychains'
```

**Expect:** both refused — but **not with the same message**, and this line claimed
they were until it was first run on 2026-09-10. Measured:

```
ls: /Users/<host user>/.ssh: Permission denied
ls: /Library/Keychains: Operation not permitted
```

The home path denies with `EACCES` and the keychain path with `EPERM`. Either message
is a pass; what fails is `No such file or directory` (see the warning below) or a
listing. Do not read the mismatch as a half-failure — the original wording invited
exactly that.

> [!WARNING]
> **Not `~/.ssh`.** Inside the jail `~` is the SANDBOX's home, which has no `.ssh`,
> so that spelling returns "No such file or directory" — which proves nothing and
> looks like a pass. The check has to name the HOST user's path explicitly, because
> the thing being tested is that the sandbox cannot read a home that is not its own.
> This runbook said `~/.ssh` on its first outing and got exactly that non-answer.

The profile is generated as a pure string and pinned by unit tests; what no test can
check is that the kernel loaded it. A jail that looks right and confines nothing is
the failure this catches, and it is silent otherwise.

**PASSED 2026-09-10**, same session as item 1 — both paths refused, so the kernel
does load the profile. That is the fact no unit test in this repo can reach.

## 3. The acceptance bar — `packages:` reaches the agent

**Name a package your own config actually declares.** Check first:

```console
$ yolo config-dump 2>/dev/null | grep -A5 '"packages"'
```

then ask for one of those:

```console
$ YOLO_RUNTIME=macos-user yolo -- bash -lc 'which just'   # or any declared package
```

**Expect:** a `/nix/store/…` path.

> [!WARNING]
> **A tool macOS already ships proves nothing here.** `which jq` returning
> `/usr/bin/jq` is not a failure of the acceptance bar — it means `jq` was never in
> `packages:` and the system copy answered. This runbook suggested `jq` on its first
> outing and got that non-answer. Use a package the config declares, or the check
> cannot distinguish "nix delivered it" from "macOS already had it".

This is the backend's founding requirement — it honors `packages:` via native nix or
it does not ship. It exercises the whole native chain in one command: the build, the
GC root, the PATH prefix, and the login-rc re-prepend surviving macOS `path_helper`
(the [OQ-1](mac-go-port-verification.md#2-macos-user-backend--real-launch-oq-1-the-load-bearing-unknown) question). A Homebrew path here means the re-prepend lost.

**PASSED 2026-09-10**, same session as items 1 and 2, and it answers
[`OQ-1`](mac-go-port-verification.md#2-macos-user-backend--real-launch-oq-1-the-load-bearing-unknown):
the re-prepend HOLDS. Both declared packages resolved into the store profile —

```
/nix/store/…-yolo-noncontainer-packages/bin/just
/nix/store/…-yolo-noncontainer-packages/bin/fzf
```

— and the pair was chosen because **each had a competing host copy to beat**: `fzf`
at `/opt/homebrew/bin/fzf` and `just` at
`~/.local/share/mise/installs/just/1.58.0/just`. Either one answering would have
meant the re-prepend lost to `path_helper` or to mise. Neither did. **Pick the
package the same way when re-running this** — a declared package with no host rival
cannot distinguish a working re-prepend from a lucky PATH.

## 4. Content actually reached the agent

```console
$ YOLO_RUNTIME=macos-user yolo -- bash -lc 'ls ~/.claude/skills; head -3 ~/.claude/CLAUDE.md'
```

**Expect:** the built-in skills, and briefing prose.

The install is covered by test; what is not is that the launch composes and stages
it *for real*, through sudo, into the actual sandbox home.

**PASSED 2026-09-10**, same session. All fourteen built-in skills landed —
`brainstorming`, `configuring-the-jail`, `design-doc`, `developing-yolo-jail`,
`diagnosing-the-jail`, `headful-browser`, `implementation-plan`, `new-project`,
`open-source-project`, `research`, `roadmap`, `system-doc`, `user-stories`,
`vantage-docs` — and `~/.claude/CLAUDE.md` opened with the native-backend briefing
(*"You are confined by a Seatbelt sandbox on the human's REAL machine, not by a…"*).
Worth noting `developing-yolo-jail` is among them: it is the source-tree-only skill,
so its presence also confirms the source-tree probe fired correctly for this
workspace rather than the list being staged blind.

## 5. The per-workspace home layout — NEW 2026-09-12, NEVER RUN

```console
$ YOLO_RUNTIME=macos-user yolo -- bash -lc 'ls -ld ~/.claude ~/.config ~/.local; ls -l ~/.claude/.credentials.json; cat ~/.claude/.credentials.json | head -c 20'
```

**Expect:** the first three are **symlinks** into `<workspace>/.yolo/home`, the credential is a
**relative** symlink (`../.claude-shared-credentials/.credentials.json`) that nonetheless
**reads**, and a second workspace shows its own `~/.claude/projects` rather than this one's.

Everything about the layout is pinned on Linux against a real filesystem
([`../../design/macos-user-home-tiers.md` §10](../../design/macos-user-home-tiers.md#10-what-shipped)),
including the credential resolving through it. What no test here can reach is whether the
**sandbox uid can create `<workspace>/.yolo/home`** through the shared-group ACL. If it cannot,
the boot fails with the layout step named and the existing ACL hint attached —
`yolo macos-fix-permissions <workspace>` — which is the diagnosis to follow rather than a bug
to file.

> [!WARNING]
> **An account that predates this refuses to launch, by design.** There is no migration
> ([`OQ-HT2`](../../design/macos-user-home-tiers.md#decision-ledger)): a real directory where a
> layout symlink belongs is never removed, renamed or copied, so the launch names every
> offender and the reset. If that is what you get, it is the ruling working:
>
> ```console
> $ sudo rm -rf /Users/_yolojail && yolo macos-setup
> ```
>
> You lose that account's agent history, which was ruled affordable because the only session
> this backend ever had was the 2026-09-11 hardware run.

⚠ **Two more unmeasured facts ride along with this item.** `MISE_DATA_DIR` is now named
(`~/.yolo/mise`) rather than defaulted, so nothing lands in the per-workspace `~/.local`; and
the login rc files re-prepend `$YOLO_DARWIN_LOGIN_PATH` instead of a baked PATH, so **item 3's
`packages:` check is worth re-running** — it is the one that proves the re-prepend still beats
`path_helper`, and its value now arrives by variable.

---

## 6. The floor is on the sandbox's PATH — NEW 2026-09-12, NEVER RUN

**This is the root of the chain. If it fails, items 7-9 cannot pass and need no separate
report.**

```console
$ YOLO_RUNTIME=macos-user yolo -- bash -lc 'for b in mise node npm git rg fd jq gh curl; do printf "%-6s %s\n" "$b" "$(command -v $b || echo MISSING)"; done; which --version 2>&1 | head -1'
```

**Expect:** every name resolves, and each of the first nine resolves into a `/nix/store/…`
profile rather than `/usr/bin`. `which --version` must print **nothing useful** — macOS's own
`/usr/bin/which` has no `--version` — because GNU `which` was removed from the floor on
2026-09-12 and its presence would mean the policy exclusion did not take effect.

**Settles:** that [`OQ-P1`](../../design/macos-user-provisioning.md#decision-ledger)'s floor
actually reaches the sandbox — the single largest unmeasured claim of the whole pair — and
that [`OQ-P2`](../../design/macos-user-provisioning.md#decision-ledger)'s no-GNU-userland
ruling is true of the shipped article and not just of the exclusion list.

**If `mise` or `npm` is MISSING**, stop: nothing below can pass, and this is the bug to file,
with the output of `yolo --dry-run` (which names the profile path the launch built).

> [!NOTE]
> **This item now has an automated twin**: `integration/TestMacosUserFloorReachesTheSandboxPath`
> launches a real sandbox and makes the same two assertions (every floor binary resolves, and
> resolves into the store; `which` does not). It runs only on a Mac that has `sudo -n` and the
> `_yolojail` account, and SKIPS everywhere else — countably, through the gate in
> `integration/macosusergate_test.go`, so a job that was scheduled to run it and skipped it
> fails instead of passing green. Running it does not retire this entry: the runbook is still
> the spec, and a human re-running it is still the instrument for everything the gate cannot
> reach.

---

## 7. The provisioning stage runs, and is confined — NEW 2026-09-12, NEVER RUN

Use a workspace whose config declares one cheap tool, e.g. `{"mise_tools": {"jq": "latest"}}`.

```console
$ YOLO_RUNTIME=macos-user yolo -- true
$ cat <workspace>/.yolo/startup.log
```

**Expect:** the launch prints the provisioning banner and pauses before the agent; the log
exists, is **truncated to this launch** (its first line is `=== yolo provisioning <date> ===`),
and records `mise install` running. No `PROVISIONING FAILED` line.

**Settles:** [§10.8](../../design/macos-user-provisioning.md#108-what-a-mac-has-to-settle)
items 1, 2, 3 and 4 at once — that `sandbox-exec` accepts the stage process, that the confined
stage reaches the network, that it can write into the sidecar symlinks, and that
`sudo --user=… env -i … sandbox-exec …` forwards the script **verbatim** rather than mangling
it the way `sudo --login` does. Item 4 is the one to watch: a mangled script does not error,
it provisions nothing and exits 0, so the evidence is the LOG's content, never the exit code.

⚠ **Also time it** ([§10.8](../../design/macos-user-provisioning.md#108-what-a-mac-has-to-settle)
item 5): `time` the first launch of an LSP-configured workspace. Every `mise install` plus one
`npm install -g` per server runs in series before the agent starts, and nobody knows what that
costs.

⚠ **And note the working directory** (item 9). Run `yolo` once from **outside** the workspace
tree. The stage argv sets no cwd and `sudo` without `--login` does not change directory, so a
workspace-local `mise.toml` may go unread there — the container runs the same body under
`--workdir /workspace`.

---

## 8. A stage that cannot START does not kill the launch — NEW 2026-09-12, NEVER RUN

```console
$ sudo chmod 000 /usr/bin/sandbox-exec     # or point the profile path at a missing file
$ YOLO_RUNTIME=macos-user yolo -- true ; echo "rc=$?"
$ sudo chmod 755 /usr/bin/sandbox-exec     # PUT IT BACK
```

**Expect:** the launch prints *"The provisioning stage could not be started … Launching
anyway"* and **the agent still runs**. It must NOT print *"Provisioning was aborted"*, and
`rc` must not be 1 on account of the stage.

**Settles:** [§4](../../design/macos-user-provisioning.md#4-the-proposed-shape)'s bolded rule
— *a failing stage must not abort the launch* — which the code inverted until 2026-09-12 for
exactly this class (see
[§10.7](../../design/macos-user-provisioning.md#107-the-failure-policy-the-code-did-not-implement)).
The complementary half is the veto: make a declared tool fail to install (a `mise_tools`
version that does not exist), answer **n** at the prompt, and confirm the launch DOES stop.

⚠ **Pick a reversible way to break it.** The `chmod` above is one; restore it in the same
session. Nothing in yolo needs modifying to run this check.

---

## 9. `mise_tools` and `lsp_servers` actually arrive — NEW 2026-09-12, NEVER RUN

```console
$ YOLO_RUNTIME=macos-user yolo -- bash -lc 'mise ls --installed; ls ~/.yolo/mise/installs; ls ~/.npm-global/bin'
```

**Expect:** the declared tools are installed; the mise store is under **`~/.yolo/mise`**, in
the ACCOUNT home; and `~/.npm-global` (a symlink into `<workspace>/.yolo/home`) holds the LSP
binaries.

**Settles:** the two launch warnings retired on 2026-09-12 — which were removed on the
strength of code that had never run. If either tool is absent, that retirement was premature
and both warnings should come back
([§10.6](../../design/macos-user-provisioning.md#106-two-warnings-retired-and-the-rule-that-retired-them)).

⚠ **Check the TIER while you are here**, because it is the one thing a later launch cannot
undo: `~/.yolo/mise` must be a real directory in `/Users/_yolojail`, **not** a symlink into any
workspace. A per-workspace mise store is the inverse of every other backend, and a second
workspace's launch is what would reveal it.

---

## 10. The two layout defects a mutation pass found — NEW 2026-09-12, NEVER RUN

Both are REFUSAL-message quality rather than data loss, and both were found by reasoning
rather than measurement, so a Mac is what decides whether they are worth fixing.

```console
$ mkdir -p <workspace>/.yolo/home/.claude-shared-credentials   # occupy a MIRROR path
$ YOLO_RUNTIME=macos-user yolo -- true
```

**Expect (today):** the launch refuses, names that path, and prescribes
`sudo rm -rf /Users/_yolojail` — **a command that does not touch the offending path**, so
following it leaves the launch refusing forever. The fix is to remove the directory under the
workspace instead.

**Why it is reachable:** `run/prepare.go`'s `migrateOldOverlay` COPIES every
`packload.SharedDirs` entry into `<ws>/.yolo/home` and never deletes, so any workspace that
ever ran a container launch on this machine may already hold one.

**The second, which needs no command:** if `LoadJailPacks` ever fails transiently (a corrupt
or half-written pack root), the bootstrap continues anyway (A12) with an EMPTY link set, so
`install_home_overlay` writes a REAL `~/.claude` — and every later launch refuses forever,
with the same remedy, which here destroys the machine tier the shared-credentials hook exists
to preserve. If you ever see that refusal on an account you did not touch, this is the likely
route.

---

## Known-absent, do not report as bugs

- **`mcp_presets`** are not delivered here — the wrappers hardcode Linux paths this
  backend never provisions. The launch says so.
- ~~**`mise_tools` are not installed.**~~ ~~**`lsp_servers` are not installed.**~~
  **BOTH FIXED 2026-09-12, and unverified on hardware** — see items 6 and 7. All three
  grounds this entry gave are now false: the floor puts `mise` on the sandbox's PATH,
  `MISE_DATA_DIR` names a real machine-wide store, and the confined provisioning stage
  runs `mise install` and the generated bootstrap script before the agent starts. Until a
  Mac says otherwise, treat an absence here as a bug WORTH reporting, with the contents of
  `<workspace>/.yolo/startup.log`.
- **`per_side_paths`, `resources`, `cache_relocations`** are read and ignored, each
  for a structural reason (no mount namespace, no cgroups, no binds). Each warns.
- ~~**One home for every workspace.**~~ **FIXED 2026-09-12, and unverified on hardware** —
  see item 5, which is the check that settles it.
- **A jail here cannot launch a jail.** `sudo` cannot exec under Seatbelt and
  `sandbox_apply` refuses a second profile, so verify macos-user changes from an
  unsandboxed shell.
