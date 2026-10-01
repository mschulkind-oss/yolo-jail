---
status: current
verified: 2026-10-01
verified_commit: 3f5d9819
summary: "When yolo can't do what the user asked, it does the work itself, offers one command that does it, gives exact instructions for this platform, or names who can act. It never just reports the problem."
covers:
  - internal/cli/check/
  - internal/cli/run/srcskew.go
  - internal/cli/run/hostloopback.go
  - internal/cli/applyhostdepgate.go
  - internal/cli/hostapplyverdict.go
  - internal/cli/checkdeps.go
  - internal/cli/hintcommands_test.go
  - internal/cli/init.go
  - internal/cli/update.go
  - internal/cli/autocapture.go
  - internal/capture/manifest.go
  - internal/config/packs.go
  - internal/depcheck/
  - internal/entrypoint/shims.go
  - internal/hostfloor/floor.go
  - internal/packsrc/lock.go
  - internal/prune/probes.go
  - internal/storage/nixinstall.go
  - internal/updatehint/
  - cmd/yolo-ps/main.go
  - cmd/yolo-serial/main.go
  - cmd/yolo-cglimit/main.go
  - packs/guardrails/pack.json
tags: [principle, cli, ux, errors, diagnostics]
---

# The Happy Path Principle: every stop leads back to the happy path

**Status:** PRINCIPLE, current as of 2026-10-01, verified against `3f5d9819` and the next-step
fixes landed with this revision.

**Author:** Matt Schulkind, the maintainer; adopted 2026-10-01. This name used to belong to the
fill-the-matrix principle ([`fill-the-matrix-principle.md`](fill-the-matrix-principle.md)), renamed
on 2026-10-01 because "happy path" did not describe it.

**Audience:** anyone adding or editing a refusal, a `yolo check` finding, a launcher or in-jail
client message, or a command's last line of output. Read this before writing "X is not installed."

A principle keeps its rationale on purpose: the verdict alone ("add a hint") reads as polish, and
polish is the first thing cut. The reasoning is what makes a message with no next step a defect.

**Sibling principles:** [`fill-the-matrix-principle.md`](fill-the-matrix-principle.md) (one path
per cell, so there is one fix to name),
[`information-at-the-point-of-need.md`](information-at-the-point-of-need.md) (where information
lives), [`gate-placement-principle.md`](gate-placement-principle.md) (whether a prompt earns its
place), [`stringly-typed-references-principle.md`](stringly-typed-references-principle.md) (a
refusal's message is most of its value), [`extension-point-principle.md`](extension-point-principle.md)
(who designs an extension point).

---

## The principle

> **A user who tries to do something should never be left stuck.** If yolo can't do it yet, it
> says what's in the way and gives the next step that removes it: a command to run, or exact
> instructions when no command exists. Following those steps always leads to the thing they
> originally asked for.

yolo manages the environment a coding agent works in, so many of its stops come while that
environment is being put together, and the user who meets one is often the agent itself.

**This is a guiding principle, not a ruling on mechanism.** In the maintainer's words: if yolo can
apply the fix, it applies it; if it needs to ask the user, it asks; if it needs to give them a
command, it gives the command. It picks the easiest of these that does not violate the user's
trust. It does not overrule a decision a design doc has ruled on, such as
[the dependency rule](report-tiers.md#the-dependency-rule)'s install confirm or
[`OQ-D2`](config-safety.md#oq-d2)'s config prompt.

The name is **the Happy Path Principle** *(coined here, by the maintainer, 2026-10-01)*. It builds
on a standard term:

- **Happy path**: the standard software term for the default route through a feature, where
  nothing goes wrong and the user gets what they asked for
  ([Wikipedia](https://en.wikipedia.org/wiki/Happy_path)). It is not "the only path we support,"
  and it is not "the path we test."
- **The Happy Path Principle** *(coined here)*: every point where the user leaves the happy path (a
  missing dependency, a runtime that isn't running, a command they can't run yet) comes with a way
  back onto it. Usually the happy path is described and tested on its own. This principle is about
  everything *off* it: each failure is a way back onto the happy path, not the end of it.
- **Dead end** *(coined here)*: the failure this principle prevents. It is any point where yolo
  stops and the user has no concrete next step. Two that yolo printed until 2026-10-01:
  `nix found but could not be run: <path>`, and `yolo-serial: endpoint file <path> is malformed.`
  Each reported a problem accurately and then left the user to work out the fix on their own.
- **Next step** *(coined here)*: something the user can do right away without any research. It is
  either a command they can copy and paste, or exact instructions written for their platform.
  "Install it with your package manager" is **not** a next step. It points the user at a task
  instead of doing the work for them.

> [!WARNING]
> **Don't read "happy path" in its usual narrow sense.** On its own, "happy path" often means "we
> only handled the case where nothing goes wrong." This principle means the opposite: the error
> cases get the *most* care, because each one has to lead back to the happy path.

> [!NOTE]
> **Why not "fail forward" or "fall forward."** Those phrases already mean other things: "keep
> going past errors" and "fix forward instead of rolling back." This principle is not about
> continuing after an error. It is about what the error *says*.

### What it is not

- **Not "never fail."** Failing loudly and early is correct. The rule is about what the failure
  message contains, not whether the failure happens.
- **Not "silently fix things."** Doing the work is the best response, but only when that work is
  safe, cheap and easy to undo, and yolo says what it did ([rung 1](#the-next-step-ladder)).
- **Not "never report OK over broken" in disguise.** That is a separate rule that this one depends
  on ([rule 5](#the-rules)). A tool that gives a wrong verdict gives a wrong next step too.

## The next-step ladder

**The next-step ladder** *(coined here)* is the four responses below, best first. It is not the
network-stack ladder in [`loopback-tls-reachability.md`](loopback-tls-reachability.md#the-ladder).
When yolo can't proceed, it takes the **highest rung it can honestly reach**, and never stops below
rung 4. Cite rungs and rules by number, and never renumber them.

| Rung | What yolo does | Example |
| :--- | :--- | :--- |
| **1. Do it** | Fixes the missing piece itself when that's safe, cheap and can be undone, and says what it did. When the fix needs the user's OK, it asks first, then does it. | A [pack](pack-system.md)'s agent installs the first time it runs: its launcher prints `Installing <package>...`, then runs it. A rootless podman whose pasta cannot forward the host's loopback is launched on slirp4netns instead, with a note on what changed, what it costs and how to get back. |
| **2. Offer it as one command** | Names a single command that makes the fix. | `yolo prune` is a dry run ending `Re-run with --apply to execute.` A yolo older than its source tree refuses with `Fix:  (cd <repoRoot> && just install)`. |
| **3. Give exact instructions** | Prints the exact command or config line for *this* platform when yolo can't run it (it needs sudo, a package manager, or an edit to the user's config). | `yolo check-deps` prints `sudo dnf install -y <pkg>` on a dnf host and `brew install --cask <pkg>` for a cask. With no packs, a launch says to add `"packs": ["claude"]` to `~/.config/yolo-jail/config.jsonc`. With no nix, `yolo check` prints the install the [getting-started guide](../../userguide/getting-started.md) gives for this machine. |
| **4. Name the owner** | When the user's own action can't fix it, says why and who or what can. | In a jail, `yolo update` says it is the jail's copy of the host's yolo, kept until the jail is relaunched, and to run `yolo update` on the host and relaunch the jail. A rootful podman can't forward loopback, and yolo says so and names the two setups that work instead. |

When a step is expensive, yolo says what it will cost before doing it. A launch's auto-capture
(one install per machine, reused by every workspace) names the download it is about to pay for,
then says `Set YOLO_NO_AUTO_CAPTURE=1 to skip.`

## The rules

1. **Every error names a next step.** Put it on the line right after the problem, in the same
   output. Don't send the user to a doc page to find it.
   *In yolo:* the refusal for a yolo older than its source tree (`refuseOnSourceSkew`) is the model:
   the fix, what to try if it doesn't take, then the `YOLO_ALLOW_SOURCE_SKEW=1` hatch (the
   variable that overrules the refusal). A hatch alone is not a next step, because it goes on
   without what was refused. No `yolo check` finding may be written with a literal empty note
   (`TestNoFindingIsWrittenWithAnEmptyNote`), and `nix found but could not be run` now names the
   command that shows nix's own error, then the reinstall. A nix whose `nix --version` exits
   non-zero gets the reinstall after nix's own error, where it got that error alone, or nothing
   when nix printed none; only when nix printed none does the command that shows the error come
   first (`TestANixThatExitsNonzeroNamesItsErrorAndTheReinstall`). In a `macos-user` jail, whose
   nix is the host's and which that jail's account cannot reinstall, both name `yolo check` on the
   host and a relaunch instead (rung 4). `yolo check-deps` follows each pack it could not resolve
   with that pack's fix, in the words `yolo check` gives for it, and ends with `yolo check-deps` to
   check again, once; it used to exit 1 after the problem alone
   (`TestCheckDepsNamesTheFixForAnUnresolvedPack`). The in-jail clients name one too:
   `yolo-serial` says to relaunch the jail, as `yolo-ps` does, and `yolo-cglimit` names the two
   config lines that turn on the cgroup delegate.
2. **Every success points forward too.** A command that finishes normally ends by naming the likely
   next command. Without that, a successful run is a dead end as well: it leaves the user on the
   happy path but gives no next step along it.
   *In yolo:* `yolo init` names the next command (`Tell them to run: yolo -- claude`), and
   `yolo init-user-config` ends with the line that selects an agent, `yolo check`, and the
   command that launches it. A successful `yolo host apply --assert` ends at its verdict and
   counts and names no next command. Adding one waits on a ruling: its
   [verdict block](report-tiers.md#the-verdict-block) is the verdict line and the counts, and
   "Only the dry run has a footer", so an `--assert` ends at its counts.
3. **Advice must be specific and verified.** Give the exact package name, flag or path for the
   user's platform. Generic advice can be just as wrong as no advice. Every platform-specific entry
   records where its name came from, and a test rejects any entry that doesn't.
   *In yolo:* the mechanism is tested: no remedy names a package manager the lookup did not find
   (`TestNixIsOfferedOnlyWhereTheLookupFindsIt`, `TestEveryProbeReadsTheCallersLookup`). The
   install lines `yolo check` prints for a missing container runtime and for a missing nix are
   read against the getting-started guide they come from (`TestPodmanInstallHintsMatchTheGuide`,
   `TestNixInstallHintsMatchTheGuide`). `yolo check-deps` prints each install command byte for
   byte, as the pack wrote it, square brackets included, where a bracketed word the output's color
   markup reads as a style used to vanish from the command. Escaping the bracket for the markup is
   not enough: it keeps an invisible character after the `[`, which a pasted command carries into
   the install (`TestCheckDepsPrintsABracketedCommandAsWritten`).
   **yolo does not yet enforce rule 3 for a pack's
   `install_hints`:** a pack records no source for them, except the `guardrails` pack, whose
   comments cite where each of its names came from, and no test checks that an entry has a
   source or that the names it gives are right.
4. **Hints are tested so they can't go stale.** A test checks that every command a hint prints is a
   real command. A hint naming a deleted command is a dead end that *looks* like a next step, which
   is worse than no hint.
   *In yolo:* every backticked `yolo …` command in a string literal under `internal/` and `cmd/`,
   or in literals joined with `+` (a piece that is not a literal reads as a placeholder), and every
   one-command line of a command's help examples, is resolved against the dispatchers' own source:
   its command through the router `yolo` dispatches with, its verb in that command's verb switch,
   and each `--flag` after it in what that command parses (`TestEveryHintedYoloCommandExists`,
   `TestEveryHelpExampleExists`, in `internal/cli/hintcommands_test.go`). It caught `yolo host codex`
   and `yolo host check-deps`, neither a command, and a help example for a `yolo programs` verb
   that does not exist. A command a message spells without backticks, such as the
   `then: yolo check` many `yolo check` notes end with, is not read. Every command's help carries an example that routes to that command
   (`TestEveryCommandShowsACopyableExample`), and every `docs/` path or anchor cited from Go
   resolves (`TestEveryDocCitationFromGoResolves`). **No test checks that a named command does what
   the hint says:** that is each hint's own test. `yolo check`'s piped orphan note used to name
   `yolo prune --apply`, a real command that removes only stopped containers, so the running jails
   it listed stayed. Its test now follows the removal the note names, against a fake runtime, and
   then the re-check (`TestPipedOrphanHintRemovesWhatTheTerminalsYesRemoves`).
5. **Re-check after a fix, and never report OK over broken.** After a repair yolo checks again for
   real and doesn't take the fix on faith. A false green sends the user down the wrong path with
   full confidence.
   *In yolo:* `yolo host apply --assert` re-probes each binary it installed and refuses if one is
   still missing ([the dependency rule](report-tiers.md#the-dependency-rule);
   `TestApplyHostAssertRefusesWhenTheInstallProducesNothing`), and `yolo check` counts `[SKIP]`
   apart from passes ([`OQ-3`](claude-oauth-interposition.md#oq-3); `TestSkipIsNotAPass`). Every
   `yolo check-deps` run that finds something missing, or a pack it could not resolve, ends with
   `yolo check-deps` to check again. A user config it cannot parse is one of those problems: the
   run used to say there was nothing to check and exit 0
   (`TestCheckDepsRefusesAnUnreadableUserConfig`).
6. **Re-running is always safe.** The next step for nearly any interrupted command is to run it
   again, and that only works if a second run can't make things worse.
   *In yolo:* a failed auto-capture says `The next launch retries.`, and a launcher whose update is
   busy or fails runs the installed version. An agent's launcher whose first-use install fails
   ends `⚠ <name> not available: its install failed, above. Run <name> again to retry the install.`,
   and the next run does retry it. The pnpm launcher retries a failed install only an hour after
   the last try, so it says when a run retries and prints the command that retries now
   (`launcherretry_test.go` runs each launcher again, and the pnpm command as printed).
7. **Don't send the user to find something yolo could figure out itself.** If yolo can work out the
   value, fork it from an existing copy or fetch it, it does that instead of asking the user to go
   and get it.
   *In yolo:* the macOS nix-daemon restart reads the daemon's label from `/Library/LaunchDaemons`.
   `yolo check-deps` ends with the command that installs its package list for the manager it
   found, and a pack lockfile, a fork lock, a capture or a host floor record written by a newer
   yolo names `yolo update`, which knows this install's channel, or in a jail says to run it on the
   host and relaunch the jail, which keeps the yolo it was launched with until then. `yolo update`
   itself, run in a jail, says the same.

## Before and after

Each pair below shows what yolo printed before 2026-10-01, then what it prints now. **Each "Now"
is current output.**

**Before:** `yolo check` on a Linux host with no nix (`sectionNix`):

```text
  [FAIL] nix not found
       -> Install Nix: https://nixos.org/download/
```

**Now:** the install the [getting-started guide](../../userguide/getting-started.md) recommends
for Linux and Apple silicon Macs. An Intel Mac gets the guide's nixos.org script and its two
trust lines instead.

```text
  [FAIL] nix not found
       -> Install Nix with the NixOS Nix installer (its --extra-conf makes the Nix daemon trust you):
            curl -sSfL https://artifacts.nixos.org/nix-installer | sh -s -- install --extra-conf "extra-trusted-users = $(whoami)"
          then, in a new terminal: yolo check
```

**Before:** `yolo check` with its output piped, and two running jails it judges orphaned:

```text
  2 orphaned jail(s) are still here. Run `yolo prune --apply` to remove them; this check will not.
```

**Now:** the same removal a terminal's `y` makes (`nonTTYOrphansNote`). That answer also records
why each jail stopped, which a removal from outside yolo cannot.

```text
  [WARN] 2 orphaned jail(s)
       -> These containers are stuck or have lost their workspace, and this check removes them
          only when a terminal answers its question:
          fix:  podman rm -f yolo-api-3f2a91c0 yolo-web-9c1d47e2
          then: yolo check
```

**Before:** a jail's first `codex` whose install fails (the launcher in `~/.yolo/bin/launch`):

```text
  ⚠ codex not available
```

**Now:** the launcher installs again on the next run, so it says so.

```text
  ⚠ codex not available: its install failed, above. Run codex again to retry the install.
```

## Review checklist

Use these on any change that adds or edits an error, a `yolo check` finding or a command's final
output:

- [ ] Every failure path prints a next step: a command, or exact platform instructions.
- [ ] yolo reached the highest [rung](#the-next-step-ladder) it honestly could.
- [ ] The step is the easiest one that keeps the user's trust: yolo acts alone only when that is safe, cheap and undoable, and asks first otherwise.
- [ ] Package names, flags and paths are specific to the platform and have a recorded source.
- [ ] A test asserts that every command a hint names exists and does what the hint says.
- [ ] After a repair, yolo checks again and doesn't take the fix on faith.
- [ ] A successful run ends by naming what to do next.

## Where this already lives

Parts of this principle are already written down in yolo-jail:

- [`report-tiers.md`](report-tiers.md#principles): P2, every loss names its remedy in copy-paste
  form (rules 1 and 3), and P7, the command states its own result (beside rule 2).
- [`loopback-tls-reachability.md`](loopback-tls-reachability.md#oq-r3): [`OQ-R3`](loopback-tls-reachability.md#oq-r3), a host yolo cannot
  fix still launches, and the requirement lands on the message: what breaks, what fixes it, and the
  command that checks where there is one (rung 4).
- [`config-safety.md`](config-safety.md#oq-d2): [`OQ-D2`](config-safety.md#oq-d2), a changed workspace config with no terminal
  refuses, and `--accept-config-changes` is the flag form of the prompt.
- [`AGENTS.md`](../../AGENTS.md): a `YOLO_*` hatch is documented where it is enforced, often in the
  refusal that offers it.
- [`setup-support-gaps.md`](../plans/setup-support-gaps.md): the backend gap tracker. Its
  silent-drop table names the file that should print each missing notice, which is the case below a
  dead end: no message at all.
- In code: the refusal for a yolo older than its source tree
  ([`srcskew.go`](../../internal/cli/run/srcskew.go)), `depcheck`'s per-manager install lines
  ([`depcheck.go`](../../internal/depcheck/depcheck.go)), the guide's Nix install
  ([`nixinstall.go`](../../internal/storage/nixinstall.go)), the slirp4netns fallback note
  ([`hostloopback.go`](../../internal/cli/run/hostloopback.go)), `yolo-ps`'s three-step message
  for when the host-processes loophole is not turned on ([`main.go`](../../cmd/yolo-ps/main.go)),
  and rule 4's check of every hinted command
  ([`hintcommands_test.go`](../../internal/cli/hintcommands_test.go)).
