---
title: "RUNBOOK — what a Mac session should measure about provisioners"
status: current
date: 2026-09-20
tags: [runbook, macos, macos-user, nix, brew, capture, provisioners]
summary: "Five items no Linux jail can reach, about how a binary actually gets provisioned on a Mac: whether the macos-user guest has any working program provisioner, whether the generated Brewfile applies with its casks, whether nix refuses the unfree agent CLIs on darwin, whether capture's recording half works on hardware, and whether Seatbelt resolves `..` physically. Each item carries the command, what it decides, and the 2026-09-11 measurement that answered it — four of which corrected the item that asked them. Also: which items need a human at a password prompt, and what the sibling manual-checks runbook already settled so you do not re-run it."
---

# RUNBOOK — what a Mac session should measure about provisioners

**Status:** CURRENT — **all five items have been RUN** (2026-09-11) and each carries its result
inline. Re-run an item when the code under it moves; read the result before the verdict, because
four of the five corrected the item that asked them.

**Audience:** an agent (or the maintainer) on the maintainer's Apple Silicon Mac.
**Needs:** a human at the keyboard for the first launch — [M1](#m1--does-the-guest-have-any-working-program-provisioner)
and [M4](#m4--does-the-capture-recording-half-work-on-hardware) begin with `sudo --user=_yolojail`.
**Writes:** M2 is `check`, never `install`; M3 and M5 touch only `/tmp` and a throwaway nix
profile. M1 and M4 install for real — M1 runs the guest's own launchers, M4 records a capture
entry into the invoking user's store.

**What owns the model.** These items exist to decide questions in
[`../../design/provisioner-sets.md`](../../design/provisioner-sets.md) — which manager covers
what, in what order, and whether `program` is a package. Each item below names what it decides;
that doc holds the argument, the open questions and the rulings. This file is the procedure and
the measurements.

---

## Why no Linux jail can answer these, and how the five are ordered

A runbook entry, not prose. Five items, **ordered by what each decides**, each runnable as
written by an agent on the maintainer's Mac. Every one of them is a fact no Linux jail can reach
— a nested jail is structurally blind to this backend, and to rootless podman.

> [!NOTE]
> **Do not re-run the four checks in
> [`macos-user-manual-checks.md`](macos-user-manual-checks.md).**
> They passed 2026-09-10 and that runbook says so itself. ⚠ Items M1 and M4 below need a
> `macos-user` launch, which begins with `sudo --user=_yolojail` — **an agent cannot answer a
> password prompt**, so those two need the human at the keyboard for the first launch of the
> session. M2, M3 and M5 need no privilege at all.

> [!NOTE]
> **ALL FIVE ARE RUN, and all five answered** — one session on the maintainer's Apple Silicon Mac
> (macOS 26.5, arm64) on **2026-09-11**, host `yolo` `0.8.0+1336.gecb17e8c`, taken to HEAD with
> `just install` first because two of the 43 pending commits were capture/`agentcfg` fixes M4
> exercises. Per-item results are under each heading, as **MEASURED** paragraphs. Four of the five
> corrected the item that asked them, so read the result and not just the verdict.
>
> **M5 needed no launch at all**, which is a correction to the sentence above rather than a result:
> the fact is kernel path resolution, observable unsandboxed, and a Seatbelt profile can only
> subtract permissions.
>
> ⚠ **NEVER hand a `macos-user` launch a MULTI-LINE command — the first M1 attempt measured nothing
> and reported success while doing so.** `sudo --login` concatenates the command it is given
> *"separated by spaces, after escaping each character (including white space) with a backslash"*
> (sudo(8) `-i`), so every newline arrives at the target shell as a `\`-continuation it removes:
> nine probes collapsed into five commands, each becoming an argument to the previous `echo`, and
> every one of them exited 0. ~~Single-line and semicolon-separated is immune.~~ This is a **defect in
> the launch, not in the method** — it is filed at
> [`macos-user-provisioning.md` — the forwarded command is not passed through faithfully](../../design/macos-user-provisioning.md#11-the-forwarded-command-is-not-passed-through-faithfully),
> which owns that argv.
>
> **FIXED 2026-09-12** ([that section](../../design/macos-user-provisioning.md#11-the-forwarded-command-is-not-passed-through-faithfully) has the measurement), and the struck sentence above was **wrong
> while it stood**: single-line is immune to the NEWLINE half only. sudo(8) leaves dollar signs
> unescaped too, so an intermediate login shell expanded every `$var` against an empty
> environment — runbook item 6's one-line `for b in …; do … "$b" …; done` probe printed nine
> BLANK lines and exited 0. Measured 2026-09-12; the item could not be run at all until its
> probe was rewritten without variables. If you are reading this on an older binary, the safe
> spelling is **no newlines AND no shell variables**.

## What the Mac runbook already settled, and one stale comment

[`macos-user-manual-checks.md`](macos-user-manual-checks.md)
records **all four of its checks PASSING on 2026-09-10**, in one session on the maintainer's
Apple Silicon Mac (macOS 26.5, arm64): the privilege transition, **Seatbelt actually applied**,
`packages:` reaching the agent through the full native nix chain, and content staging. Two of
those bear directly on the items below — the nix chain works end to end on hardware, and the
login-rc re-prepend **holds** against macOS `path_helper`.

> [!NOTE]
> **`internal/macosuser/capture.go`'s *"NOT MEASURED, anywhere: … No Seatbelt profile has been
> loaded by a kernel"* was stale in its general form when this was written, and is now stale
> WHOLESALE** — [M4](#m4--does-the-capture-recording-half-work-on-hardware) ran capture's own
> profile and its whole pipeline on hardware on 2026-09-11, which was the last narrow thing the
> comment was still right about. It said so accurately at the time: capture uses a different
> profile from the session one, and `CapturePlanInvariants` exists to fail *"if the profile is
> swapped for the session one"*, so runbook item 2 passing on 2026-09-10 did not reach it.
> **Corrected in the same pass that measured it** — this paragraph asked whoever next touched that
> file to fix the comment, and the Mac session that answered M4 was that pass.

## M1 — does the guest have *any* working `program` provisioner?

**Decides** [the provisioner inventory](../../design/provisioner-sets.md#3-the-provisioner-inventory-per-environment)'s
rows 5 and 6, and the guest row of [`OQ-PS6`](../../design/provisioner-sets.md#OQ-PS6).

```console
$ YOLO_RUNTIME=macos-user yolo -- bash -lc 'claude --version; copilot --version; command -v npm'
```

**Expect:** `claude` (a `via: installer` pack) **succeeds** — `curl` and `bash` are at
`/usr/bin`, so its launcher should install and run; `copilot` (a `via: npm` pack) **fails**, and
`command -v npm` finds nothing. That pairing is the whole result: it confirms the installer row
is a real guest provisioner and the npm row is not. If `claude` also fails, the guest has **no**
`program` provisioner and [the inventory](../../design/provisioner-sets.md#3-the-provisioner-inventory-per-environment)'s
guest column needs a row-6 correction to *absent*. Version probes only — never a session.

**MEASURED 2026-09-11 — the guest HAS a working `program` provisioner, and it is exactly the
installer row.** [The inventory](../../design/provisioner-sets.md#3-the-provisioner-inventory-per-environment)'s
rows 5 and 6 stand as written; no correction needed. **`copilot` was substituted**, because the
measuring host's `packs` does not select it — the pairing was run over all five agent packs that
host does select, three `via: installer` and two `via: npm`, which is a stronger test than the
one asked for:

| Probe | Result |
| :--- | :--- |
| `command -v curl` / `command -v bash` | `/usr/bin/curl` · `/bin/bash` |
| `command -v npm` / `command -v node` | **both empty** — the guest has neither |
| `claude --version` (installer) | `2.1.217`, rc=0 — via the already-installed copy; see the update note below |
| `codex --version` (installer) | installed 0.154.0 from scratch, `codex-cli 0.154.0`, rc=0 |
| `agy --version` (installer) | installed 1.2.1 from scratch, `1.2.1`, rc=0 |
| `opencode --version` (npm) | `launch/opencode: line 182: npm: command not found` → `⚠ opencode not available`, **rc=1** |
| `pi --version` (npm) | same shape, **rc=1** |

Three facts worth more than the verdict. (1) **The npm row does not fail silently** — the launcher
prints the missing interpreter and the pack name and exits non-zero, which is what
[`macos-user-provisioning.md` — what this costs today](../../design/macos-user-provisioning.md#2-what-this-costs-today)'s
"silent" cell claimed it did not do; that row is corrected there. (2) **`claude`'s launcher tried its hourly UPDATE and the
update FAILED** — `⚠ claude: update failed (status 124) — running the installed version`
(`internal/entrypoint/shims.go`), then ran `2.1.217` anyway. The fallback behaved exactly as
designed, so what is unproven on this backend is the **evergreen** half, not the install half. ⚠
**The 124 is the vendor's, not a yolo timeout**: `HAS_UPDATE_VERB=1` for claude (`update:
["install"]`), so `_bounded` ran `claude install`, and `_bounded` only wraps in `timeout(1)` *where
the platform has one* — `shims.go` says in as many words that the image bakes it and a
stock macOS does not, and this Mac confirms it (no `/usr/bin/timeout`; Homebrew's `gtimeout` is off
`SandboxPath` and denied by the profile besides). So the update ran **unbounded** and 124 is
`claude install`'s own exit status. Worth knowing before reading 124 as a bound anywhere on this
backend: **there is no wall-clock bound on a guest update at all**, by the ruling in that comment. (3) **Two installers write into the generated home** and one of them reorders PATH:
see [what a vendor installer does to the generated home](#what-a-vendor-installer-does-to-the-generated-home).

## M2 — does the generated Brewfile actually apply, casks included?

**Decided** [`OQ-PS2`](../../design/provisioner-sets.md#decision-ledger)'s precondition (drive or
keep hinting — ruled **drive**, 2026-09-11) and the macOS row of
[`OQ-PS6`](../../design/provisioner-sets.md#OQ-PS6). This is the first hardware exercise of the
`brew-cask` hint key shipped 2026-08-02.

```console
$ yolo check-deps                      # note which manager it names
$ cat ~/.config/yolo/Brewfile
$ brew bundle check --file ~/.config/yolo/Brewfile
```

**Expect:** `check-deps` names **brew** (confirming `DetectManager()` on a real Mac), the file
contains `cask "claude-code"`-style lines for the four casks and `brew "…"` for the rest, and
`brew bundle check` **parses the file** and reports what is missing rather than erroring. A parse
error means the manifest yolo hands users is not runnable, and
[`OQ-PS2`](../../design/provisioner-sets.md#decision-ledger) should
not be ruled "drive it" until it is. `check`, not `install` — this must not mutate the machine.

**MEASURED 2026-09-11 — the manifest is RUNNABLE, and the cask verb is right on hardware for the
first time.** `brew bundle check --verbose` parsed the generated file and reported per-entry
misses (`→ Cask codex needs to be installed or updated`, `→ Formula fd needs to be installed or
updated`), exiting 1 for "things are missing" rather than erroring on the syntax. That is
[`OQ-PS2`](../../design/provisioner-sets.md#decision-ledger)'s precondition met: the file yolo
hands a user is one `brew bundle`
understands — and it was met before the question was ruled **drive it** the same day.
Two corrections to the expectation:

- **Two casks appeared, not four**, and the reason is not a defect: the Brewfile lists **misses
  only**, so `claude-code` was absent because `claude` is already installed on that host, and
  `copilot-cli` because that host does not select the copilot pack. The `brew-cask` hint key is
  therefore exercised for `codex` and `antigravity-cli` — the other two go through the identical
  key (`packs/claude/pack.json`, `packs/copilot/pack.json`), so what is measured is the KEY,
  not four independent paths.
- **`check-deps` never prints the word "brew".** It names the manager only implicitly: it writes a
  file called **`Brewfile`** and appends `or via brew: brew install --cask …` to each miss, then
  closes with the manager-agnostic `install with the command for your manager`. `DetectManager()`
  is confirmed to have returned brew — by the artifact it chose, not by a statement. **That line
  is where the driving command belongs**, now that
  [`OQ-PS2`](../../design/provisioner-sets.md#decision-ledger) has ruled drive-it.

One unrelated observation, so the next reader does not chase it: `brew bundle check --verbose` also
printed `Formulae dependency graph sorting found a circular dependency: libtiff, webp`. That is
the measuring machine's own keg state, not anything in yolo's file.

## M3 — does `nix profile install` refuse the unfree agent CLIs on darwin?

**Also:** does a yolo-owned `--profile` dir work there? **Decides**
[`OQ-PS10`](../../design/provisioner-sets.md#OQ-PS10) (the old compound
[`OQ-PS1`](../../design/provisioner-sets.md#OQ-PS1)'s mechanism half, carved out 2026-09-11) and
[the `nix profile --profile <dir>` analysis](../../design/provisioner-evidence.md#33-nix-profile---profile-dir-the-only-candidate-that-reaches-a-users-own-path).

```console
$ nix profile add --profile /tmp/yolo-probe nixpkgs#claude-code
$ NIXPKGS_ALLOW_UNFREE=1 nix profile add --profile /tmp/yolo-probe nixpkgs#claude-code
$ nix profile list --profile /tmp/yolo-probe && rm -rf /tmp/yolo-probe
```

**Expect:** the first **refuses** with an unfree licence error; the second **succeeds**; the
listing shows a locked flake URL. All of this is asserted from a Linux jail today
([the facts inherited from the retired doc](../../design/provisioner-evidence.md#41-inherited-from-the-retired-doc-with-its-own-dates))
and never run on darwin. If the
refusal does not happen, the `unfree` half of
[the coverage matrix](../../design/provisioner-sets.md#4-the-coverage-matrix-which-manager-covers-what)
is wrong and nix ranks higher in the macOS default order than that doc assumes.

**MEASURED 2026-09-11 — the refusal is real on darwin, the profile dir works, and the SECOND
command as written does not lift the refusal.** In order:

1. Bare `nix profile add` **refused**:
   `error: Refusing to evaluate package 'claude-code-2.1.266' in …/pkgs/by-name/cl/claude-code/package.nix:94 because it has an unfree license (‘unfree’)`.
   The `unfree` half of [the coverage matrix](../../design/provisioner-sets.md#4-the-coverage-matrix-which-manager-covers-what)
   holds on macOS.
2. ⚠ **`NIXPKGS_ALLOW_UNFREE=1 nix profile add` ALSO refused** — identically. **Flake evaluation is
   pure, so the env var is not read at all**; nix's own error text says so
   (*"When using `nix shell`, `nix build`, `nix develop`, etc with a flake, then pass `--impure` in
   order to allow use of environment variables"*). `NIXPKGS_ALLOW_UNFREE=1 nix profile add --impure`
   **succeeds**. This is not a darwin fact — it is a flake fact the design doc had backwards on both
   platforms, and it matters for any design that plans to shell out to nix for an unfree agent CLI:
   **the escape hatch is a FLAG, not an environment variable**, and a `--profile` install of one of
   the three unfree CLIs must therefore run impure.
3. The yolo-owned `--profile` dir behaves as
   [the `nix profile --profile <dir>` analysis](../../design/provisioner-evidence.md#33-nix-profile---profile-dir-the-only-candidate-that-reaches-a-users-own-path)
   needs: `/tmp/yolo-probe` → a `yolo-probe-1-link` generation symlink, `bin/claude` inside it, and
   the binary runs (`2.1.266 (Claude Code)`). `nix profile list --profile` printed
   `Original flake URL: flake:nixpkgs` against a **locked** URL
   (`https://releases.nixos.org/nixpkgs/nixpkgs-26.11pre1071116.aff8a0b28396/nixexprs.tar.xz?narHash=sha256-…`)
   — locked to a channel tarball plus narHash, which is what a bare `nixpkgs#…` resolves to; a
   design that wants the closure pinned to the *jail's* nixpkgs must pass its own flake ref.
4. **Unfree means no binary cache**, so it BUILT locally on aarch64-darwin, pulling
   `apple-sdk-14.4` and a clang wrapper to do it. Cheap here, but a first-use cost worth knowing
   before ranking nix highly in the macOS default order
   ([`OQ-PS6`](../../design/provisioner-sets.md#OQ-PS6)): hydra does not build
   what it may not redistribute, so exactly the three unfree agent CLIs are the ones with no
   substitute.

## M4 — does the capture *recording* half work on hardware?

**Decides** [the capture payoff](../../design/provisioner-sets.md#72-the-capture-payoff)
and row 7's guest cell — whether capture can become the
floor-independent provisioner the reframing says it is.

```console
$ YOLO_RUNTIME=macos-user yolo capture claude
```

**Expect:** a staged install under `/Users/Shared/yolo-captures/claude/home` and a manifest. ⚠
**Do not expect materialize to work** — it cannot, and that is by design until
[`../install-capture.md`](../install-capture.md) hand-off H2 lands
(`internal/cli/run/autocapture.go`). This measures the half that exists, and it is the
first run of capture's **own Seatbelt profile**, which is the narrow thing
[the stale-comment note above](#what-the-mac-runbook-already-settled-and-one-stale-comment) says
has never been kernel-loaded.

**MEASURED 2026-09-11 — the recording half works end to end on hardware, in one pass, rc=0.** Every
stage of the pipeline `capture.go`'s header describes was observed: the staging tree on neutral
ground at `/Users/Shared/yolo-captures/claude`, the bootstrap into the **staging** home
(`yolo-jail macos-user bootstrap ok`), the **generated launcher** driving the real vendor installer
(`✔ Claude Code successfully installed! Version: 2.1.269`), then
`capture-run: 10 paths in /Users/Shared/yolo-captures/claude/out/tree (3 renamed, 0 copied)` and the
host act moving the finished proto-entry into the machine store:
`captured claude ceb51e9936131b0a 10 paths, 203.2 MB → ~/.local/share/yolo-jail/captures/entries/ceb51e9936131b0a`.
So capture **can** be the floor-independent provisioner
[the capture payoff](../../design/provisioner-sets.md#72-the-capture-payoff) says it is —
on this backend the recording half needs nothing from the guest but `curl` and `bash`.

One correction to the expectation: **`/Users/Shared/yolo-captures/claude/home` is a transient
state, not the artifact.** After a successful run that root is EMPTY — the entry is the durable
output, under `CapturesDir()` in the invoking user's home
(`internal/macosuser/capture.go`'s `CaptureRootDefault` is staging; the store is
`paths.CapturesDir()`). Someone checking this by `ls`-ing the staging path after the fact will read
a clean success as a failure.

## M5 — does Seatbelt resolve `..` through a symlinked directory?

**Decides** whether darwin matches the Linux measurement the layout rests on:
[`OQ-HT2`](../../design/macos-user-home-tiers.md#decision-ledger)'s layout — the A′ remedy in
[`macos-user-home-tiers.md` — what the credential tier then needs, precisely](../../design/macos-user-home-tiers.md#53-what-the-credential-tier-then-needs-precisely)
rests on a `..` resolution measured on a **Linux** jail on 2026-09-11, and kernel path semantics
under a sandbox profile cannot be checked from there.

```console
$ mkdir -p /tmp/yp/real/sub /tmp/yp/shared && echo ok > /tmp/yp/shared/f
$ ln -s /tmp/yp/real/sub /tmp/yp/link && ln -s ../shared/f /tmp/yp/real/sub/via
$ YOLO_RUNTIME=macos-user yolo -- bash -lc 'cat /tmp/yp/link/via'
```

**Expect:** the read **fails** — `..` resolves physically to `/tmp/yp/real`, not through the
symlink — which is what the Linux measurement found and what the chosen A′ mirror-the-shared-dir
remedy is built for. A **success** would mean darwin resolves it logically and the mirror is
unnecessary, which would simplify
[that doc's credential-tier section](../../design/macos-user-home-tiers.md#53-what-the-credential-tier-then-needs-precisely).

**MEASURED 2026-09-11 — darwin resolves `..` PHYSICALLY, same as Linux; the A′ mirror stands.**
`cat /tmp/yp/link/via` → `No such file or directory`. **And this item needs no launch, which is the
correction:** the read fails *unsandboxed*, as the invoking user, because the resolution happens in
the kernel's VFS before any policy is consulted — a Seatbelt profile can only deny an access, never
make a path that does not resolve resolve. So the sandboxed answer is entailed by the unsandboxed
one and the `macos-user` command in this item buys nothing. Stated because the reasoning
generalises: **an item is only worth a privileged launch when the sandbox could change the
answer.**

## Deliberately not asked

The runbook's four checks (passed 2026-09-10). Capture's
*materialize* half (H2-gated, so a failure would prove nothing). And *"which of the image's 36
core packages have native darwin builds"* for
[`OQ-P1`](../../design/macos-user-provisioning.md#decision-ledger) — that
is a per-attr `nix eval`, which is platform-independent and runs faster from a Linux jail than
from a Mac.

## What a vendor installer does to the generated home

Not asked for, and the most interesting thing M1 produced. **Both installers that ran wrote into
files yolo generates**, and the answers differ per vendor:

- **codex** prompted. `Start Codex now? [y/N]` — written to **`/dev/tty`** and read from it
  (`install.sh`, via a `prompt_yes_no` helper that falls back to stdin and, only
  when neither is a tty, declines). The human answered `N`; **`y` would have started an agent
  session inside a `--version` probe.** This answers a question
  [`../native-installer-migration.md`](../native-installer-migration.md) recorded as
  unverifiable — *"whether codex prompts without a TTY (`CODEX_NON_INTERACTIVE` defaults to
  `false`) — read but not exercised"* — with the sharper form: it prompts whenever a tty is
  reachable, which under a yolo launch it is. The installer honors
  `CODEX_NON_INTERACTIVE=1`, and **`packdecl.Install` has no field for passing it** (`kind`, `bin`,
  `package`, `flags`, `installerUrl`, `update` — `flags` is npm-only). So a pack cannot declare the
  one variable that makes its own installer non-interactive. Filed as
  [`OQ-PS8`](../../design/provisioner-sets.md#OQ-PS8).
- **agy** appended `export PATH="/Users/_yolojail/.local/bin:$PATH"` to **`.bashrc`, `.zshrc`,
  `.zprofile` and `.bash_profile`** — every file `WriteLoginRC` writes, plus `.bashrc`. It logged
  each one. ⚠ **A trailing prepend inverts the PATH order the launcher mechanism depends on**: an
  install prefix landing ahead of `~/.yolo/bin/block` and `~/.yolo/bin/launch` is exactly the B2 /
  [`OQ-PD12a`](../../design/program-delivery.md#decision-ledger) failure — blockers stop intercepting and the evergreen updater
  stops mediating, which on Linux cost nine days of silent non-updates. **Bounded, not harmless:**
  every one of those files is rewritten wholesale on the next launch (`WriteLoginRC` uses
  `os.WriteFile` over `.zprofile`/`.zshrc`/`.bash_profile`, and `GenerateBashrc` runs as a
  `genStep` on this path too), so the inversion lives from the install until the next launch — and
  returns every time agy updates itself. Worth knowing before the
  [`macos-user-provisioning.md` — the proposed shape](../../design/macos-user-provisioning.md#4-the-proposed-shape)-style
  provisioning stage runs installers on a schedule rather than on first use.

The general point: **a `via: installer` provisioner is a shell script the vendor
controls, and two of the three ran here reached past their own prefix into yolo's generated
files.** That is a property of the row, not of these two packs, and it is the one real asymmetry
against the npm row, which can only place a package.
