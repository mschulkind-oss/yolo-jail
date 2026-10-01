---
status: current
verified: 2026-10-01
verified_commit: eb415c7c
summary: "When yolo can't do what the user asked, it does the work itself, offers one command that does it, gives exact instructions for this platform, or names who can act. It never just reports the problem."
covers:
  - internal/cli/check/
  - internal/cli/run/srcskew.go
  - internal/cli/run/hostloopback.go
  - internal/cli/applyhostdepgate.go
  - internal/cli/hostapplyverdict.go
  - internal/cli/checkdeps.go
  - internal/cli/init.go
  - internal/cli/update.go
  - internal/cli/autocapture.go
  - internal/config/packs.go
  - internal/depcheck/
  - internal/entrypoint/shims.go
  - internal/packsrc/lock.go
  - internal/prune/probes.go
  - cmd/yolo-ps/main.go
  - cmd/yolo-serial/main.go
  - cmd/yolo-cglimit/main.go
  - packs/guardrails/pack.json
tags: [principle, cli, ux, errors, diagnostics]
---

# The Happy Path Principle: every stop leads back to the happy path

**Status:** PRINCIPLE, current as of 2026-10-01, verified against `eb415c7c`.

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
  stops and the user has no concrete next step. `nix found but could not be run: <path>`.
  `yolo-serial: endpoint file <path> is malformed.` Each reports a problem accurately and then
  leaves the user to work out the fix on their own.
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
| **3. Give exact instructions** | Prints the exact command or config line for *this* platform when yolo can't run it (it needs sudo, a package manager, or an edit to the user's config). | `yolo check-deps` prints `sudo dnf install -y <pkg>` on a dnf host and `brew install --cask <pkg>` for a cask. With no packs, a launch says to add `"packs": ["claude"]` to `~/.config/yolo-jail/config.jsonc`. |
| **4. Name the owner** | When the user's own action can't fix it, says why and who or what can. | In a jail, `yolo update` says it is the jail's copy of the host's yolo and to run `yolo update` on the host. A rootful podman can't forward loopback, and yolo says so and names the two setups that work instead. |

When a step is expensive, yolo says what it will cost before doing it. A launch's auto-capture
(one install per machine, reused by every workspace) names the download it is about to pay for,
then says `Set YOLO_NO_AUTO_CAPTURE=1 to skip.`

## The rules

1. **Every error names a next step.** Put it on the line right after the problem, in the same
   output. Don't send the user to a doc page to find it.
   *In yolo:* the refusal for a yolo older than its source tree (`refuseOnSourceSkew`) is the model:
   the fix, what to try if it doesn't take, then the `YOLO_ALLOW_SOURCE_SKEW=1` hatch (the
   variable that overrules the refusal). A hatch alone is not a next step, because it goes on
   without what was refused. Dead ends remain: `yolo check`'s
   `[FAIL] Could not resolve the yolo-jail repo root` has no note, though the launch's refusal for
   the same fault prints the fix, and `yolo-serial` stops where `yolo-ps` says "relaunch the jail."
2. **Every success points forward too.** A command that finishes normally ends by naming the likely
   next command. Without that, a successful run is a dead end as well: it leaves the user on the
   happy path but gives no next step along it.
   *In yolo:* `yolo init` names the next command (`Tell them to run: yolo -- claude`).
   `yolo init-user-config` ends at `Created <path>`, though the template it writes selects no
   packs. A successful `yolo host apply --assert` ends at its verdict and counts and names no next
   command; a line naming one would belong to its
   [verdict block](report-tiers.md#the-verdict-block).
3. **Advice must be specific and verified.** Give the exact package name, flag or path for the
   user's platform. Generic advice can be just as wrong as no advice. Every platform-specific entry
   records where its name came from, and a test rejects any entry that doesn't.
   *In yolo:* the mechanism is tested: no remedy names a package manager the lookup did not find
   (`TestNixIsOfferedOnlyWhereTheLookupFindsIt`, `TestEveryProbeReadsTheCallersLookup`). **yolo
   does not yet enforce rule 3 for the names themselves:** a pack's `install_hints` record no
   source, and no test checks the package names they give. The `guardrails` apt hint for `fd` is
   `fd-find`, which Debian installs as `fdfind`, so following it leaves `fd` missing (per Debian's
   packaging, not measured here). And for a missing runtime `yolo check` offers "your package
   manager, e.g. `sudo apt install podman`", which is wrong on Fedora or Arch.
4. **Hints are tested so they can't go stale.** A test checks that every command a hint prints is a
   real command. A hint naming a deleted command is a dead end that *looks* like a next step, which
   is worse than no hint.
   *In yolo:* help is tested: every command's help carries an example that routes to that command
   (`TestEveryCommandShowsACopyableExample`), and every `docs/` path or anchor cited from Go
   resolves (`TestEveryDocCitationFromGoResolves`). **yolo does not yet enforce rule 4 for
   messages:** no test checks that a command named in a refusal or a check note exists, or that it
   does what the hint says. The tests that read such a hint pin its string. So `yolo check` tells a
   pipe to remove orphaned jails with `yolo prune --apply`, but those jails are running and prune
   removes only stopped containers, and its test checks only that the string is there. And
   `yolo-cglimit` says the host runs the cgroup delegate "automatically," though the delegate is
   now an opt-in [loophole](loophole-system.md) shipped by the `cgroup-delegate` pack, and
   `TestMissingSocketFailsClosed` pins that word.
5. **Re-check after a fix, and never report OK over broken.** After a repair yolo checks again for
   real and doesn't take the fix on faith. A false green sends the user down the wrong path with
   full confidence.
   *In yolo:* `yolo host apply --assert` re-probes each binary it installed and refuses if one is
   still missing ([the dependency rule](report-tiers.md#the-dependency-rule);
   `TestApplyHostAssertRefusesWhenTheInstallProducesNothing`), and `yolo check` counts `[SKIP]`
   apart from passes ([`OQ-3`](claude-oauth-interposition.md#oq-3); `TestSkipIsNotAPass`).
6. **Re-running is always safe.** The next step for nearly any interrupted command is to run it
   again, and that only works if a second run can't make things worse.
   *In yolo:* a failed auto-capture says `The next launch retries.`, and a launcher whose update is
   busy or fails runs the installed version. The gap: a failed first-use install ends at
   `⚠ <name> not available`, without saying that running it again retries.
7. **Don't send the user to find something yolo could figure out itself.** If yolo can work out the
   value, fork it from an existing copy or fetch it, it does that instead of asking the user to go
   and get it.
   *In yolo:* the macOS nix-daemon restart reads the daemon's label from `/Library/LaunchDaemons`.
   Dead ends remain: `yolo check-deps` writes a package list and says to install it "with the
   command for your manager," though it knows the manager, and a lockfile from a newer yolo says
   `upgrade yolo`, though `yolo update` knows this install's channel.

## Before and after

Each pair shows what yolo prints today, then a target. **No target below is current output.** The
nix target names the installer the [getting-started guide](../../userguide/getting-started.md)
recommends for Linux and Apple silicon Macs; an Intel Mac takes the nixos.org script instead.

**Today:** `yolo check` on a Linux host with no nix (`sectionNix`):

```text
  [FAIL] nix not found
       -> Install Nix: https://nixos.org/download/
```

**Target:**

```text
  [FAIL] nix not found
       -> Install Nix: curl -sSfL https://artifacts.nixos.org/nix-installer | sh -s -- install
          then, in a new terminal: yolo check
```

**Today:** `yolo check` with its output piped, and two running jails it judges orphaned
(`nonTTYOrphansLine`):

```text
  2 orphaned jail(s) are still here. Run `yolo prune --apply` to remove them; this check will not.
```

**Target:** the same removal a terminal's `y` makes. That answer also records why each jail
stopped, which a bare `rm -f` does not.

```text
  2 orphaned jail(s) are still here; this check will not remove them.
     fix:  podman rm -f yolo-api-3f2a91c0 yolo-web-9c1d47e2
     then: yolo check
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
  ([`depcheck.go`](../../internal/depcheck/depcheck.go)), the slirp4netns fallback note
  ([`hostloopback.go`](../../internal/cli/run/hostloopback.go)) and `yolo-ps`'s three-step message
  for when the host-processes loophole is not turned on ([`main.go`](../../cmd/yolo-ps/main.go)).
