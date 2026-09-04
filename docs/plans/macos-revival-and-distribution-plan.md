# Plan: macOS revival + source-distribution fix (post-ejection)

**Status:** IN PROGRESS — restamped **2026-09-03** (written 2026-07-21; body
below is the original plan except where a dated annotation says otherwise).
Tracks J and M are done. Track D's engineering is done, but **two of its four
steps were later reverted or superseded** and the header this line replaces did
not say so. **A2 and Track L part 1 are still open**, so that header's closing
claim — *"nothing engineering-side fully open"* — was **false**; it is retracted
in §*Retracted claims* below.

> [!IMPORTANT]
> **2026-09-03: the Mac is back on the product, and this changes what "Mac-gated"
> means for everything below.** Measured on the maintainer's Apple Silicon Mac
> (macOS 26.5, arm64): the installed `yolo` is `0.8.0+881.ga6f61864` — **exactly
> HEAD**, not the 531-commits-stale build the 2026-08-19 note recorded — and the
> config has moved off the removed `agents` key. `yolo check --no-build` under
> `YOLO_RUNTIME=macos-user` reports **29 passed, 6 warnings, 0 failed**, with the
> macOS-user readiness section green throughout (`sandbox-exec`, `_yolojail`, nix
> daemon trusted, `flake.lock`), and `yolo run --dry-run` renders a complete,
> invariant-clean plan **in 0.093s**. The Mac-gated proofs in this plan are
> therefore *runnable* again; they are not yet *run*.
>
> **What still blocks a real launch there is not yolo.** The maintainer's user
> config selects packs by `file:///home/matt/.dotfiles/yolo-packs/…` — Linux
> paths, from a dotfiles tree shared with the Linux host — and `/home/matt` does
> not exist on macOS, so pack resolution fails before the backend is reached.
> yolo has no `~`/env expansion for a `file://` pack source, so the config cannot
> currently name one tree on both machines. Whether that is fixed in the dotfiles
> (a machine-local `overrides.jsonc`, which that config already includes) or in
> yolo (expansion in a local pack source) is a maintainer decision, and it is the
> single thing standing between this plan and a live macos-user session.
>
> **Two defects were found by that measurement and fixed the same day** — see the
> two `2026-09-03` rows in the table below.

**The short version.** The revival landed: macos-user is a real, real-HW-proven
backend and the distribution regression is closed by a baked prebuilt bundle
rather than by the source bundle this plan designed. What is left is not a
track, it is **two loose ends** — A2's hard-error half, and the loophole
framework on the macos-user launch path — plus a pile of Mac-gated *proofs* that
no Linux jail can perform.

> [!WARNING]
> **Two reversals live inside the track descriptions below, not in this header.**
> **D2 was REVERTED on 2026-07-29** (`5d34dece`) — a missing repo root is FATAL
> again on the container backends. Do not read the D2 "DONE" record as current;
> see §Track D / D2. And **D1's `repo_path` config key was RETIRED on
> 2026-07-23** (`20a8ce9f`) — `just install` no longer writes it
> (`Justfile:102`), `internal/config/inherit.go:198` marks it `RETIRED`, and
> `internal/config/validate.go:138-152` tolerates it with a deprecation warning
> only. Verified 2026-08-23.

**Track status, verified against the tree 2026-08-23.** Every row was checked by
reading code or `git log`; nothing here is carried over on trust.

| Item | Verdict | Evidence (checked 2026-08-23) |
| :--- | :--- | :--- |
| J1.1, J1.2, J1.4 | **landed** | see §J1 |
| J1.3 (builder reaping) | **landed, then DELETED with its host** | `internal/builder` no longer exists — Open Decision #3, 2026-07-23 |
| J2 (Go bootstrap) | **landed** | §J2 statuses; `internal/macosuser` is Python-free |
| J3 (container builder) | **landed** | `8abb67ce` + `c2f0b941`; `internal/image/autoload.go:13,59,219` imports and calls `containerbuilder` through `BuildOffload` |
| D1 (`repo_path`) | **landed, then RETIRED 2026-07-23** | `20a8ce9f`; `Justfile:102`; `internal/config/inherit.go:198` |
| D2 (graceful degradation) | **landed 2026-07-21, REVERTED 2026-07-29** | `8f1d612`/`07975c88` in, `5d34dece` out; regression `internal/cli/run/reporoot_fatal_test.go` |
| D3 (source bundle) | **landed 2026-07-20, SUPERSEDED 2026-07-23** | prebuilt-bundle cutover; `internal/reporoot`, `flake.nix` `installPrefix` |
| D4 (Cachix) | **substituter live; the human half is PART done** | `flake.nix:13-16` (`730c258`); `--accept-flake-config` on every nix call — `internal/image/nixflags.go:35`, `internal/darwinpkg/darwinpkg.go:91` |
| Track M (M0/M1/M2) | **landed 2026-07-21 on real HW; M2's dogfood has since lapsed** | see the M-track note below |
| A1 (config-diff on macos-user) | **DONE 2026-08-18, by the rejected alternative** | `bb825486`, `fb19e8ed`; `internal/cli/run/run.go:144` |
| A2 (hard error + `linux-only`) | **DONE 2026-09-04** | both pieces shipped: `platforms: ["linux"]` on the package object form filters in `EffectivePackages(cfg, platform)` BEFORE materialize, and a declared package still missing from the build aborts the launch naming every one at once (`internal/macosuser/orchestrator.go`). The plan's `linux-only` spelling became `platforms`, a list — see A2 below |
| A3 (drop `macos_shared_root`) | **DONE 2026-07-23** | `68026c61`; `rg macos_shared_root internal/` is empty; message at `internal/macosuser/runplan.go:286` |
| Track L part 1 (framework plumbing) | **NOT STARTED** | `startLoopholesDisclosed` is called once, at `internal/cli/run/run.go:569`, inside `runContainer` (`run.go:308`); `macosuser.EndpointGrantCommands` (`macosuser.go:430`) has **zero call sites** |
| Track L part 2 (scoping proxy) | **BLOCKED on OQ-L1** | unchanged |
| check's python3 probe | **DELETED 2026-09-03** | it hard-FAILed a python-less Mac for a requirement J2 dropped on 2026-07-21 (`544a8069`); `internal/cli/check/sections_macos.go` |
| macos-user repo-root gate for `packages:` | **FIXED 2026-09-03** | an unresolved root reached `darwinpkg.Materialize("")` → empty `cmd.Dir` → nix evaluated the user's cwd; `internal/cli/run/run.go`, `internal/darwinpkg/materialize.go` |
| macos-user self-hosting (jail-in-jail) | **STRUCTURALLY BLOCKED; the workaround was proposed and REJECTED** | measured 2026-09-03 — see §*Self-hosting*, OQ-SH-1 |

**D4, stated honestly.** The substituter is live and the flake's own cache is
honored on every nix invocation. `handoff-cachix-cache.md` records the cache,
the account and the `CACHIX_AUTH_TOKEN` secret as **all done (2026-07-20)** — so
the old header's "Cachix account/token … human-gated" is stale. What remains is
the **first push** and the **Mac download proof**. Two sibling docs disagree
about the first of those: `docs/plans/README.md:31` says *"CI has already pushed
data"*, while `handoff-cachix-cache.md` still lists the first push as remaining.
**Neither is checkable from this Linux jail** — it needs a look at the Cachix
cache or a release run — so both spellings are recorded rather than one being
picked.

**Track M, stated honestly.** M0/M1/M2 were genuinely verified on real Apple
Silicon on 2026-07-21 and that proof stands *for what it tested*. It is no
longer a description of that machine: measured 2026-08-19, the Mac's installed
`yolo` was **531 commits stale** and its `~/.config/yolo-jail/config.jsonc`
still used the **removed `agents` key**, so no current `yolo` launches there on
any backend. M2's "Mac agent sessions run under macos-user" is therefore true of
the 07-21 build and not of today's. See `roadmap.md`'s 🔒 macOS rows.

### Retracted claims

- **⚠ Retracted (2026-08-23): "nothing engineering-side fully open."** Written
  2026-07-21 and false by its own §"Active work", which listed A1/A2/A3 as live
  on 2026-07-23. A2 is still open today (row above). The claim was a summary of
  the *tracks*, and it silently annexed the A-items.
- **⚠ Retracted (2026-08-23): the header's flat "D1, D2, D3 … landed."** All
  three landed and then moved: D1 retired, D2 reverted, D3 superseded. A "landed"
  with no half-life is what let `docs/plans/README.md:30` still assert "D2
  landed" a month after the revert.

**Inputs:** `docs/research/repo-root-and-distribution.md` (the source-access
work), `docs/design/macos-no-vm-direction.md` (the settled "compose both
backends" direction), `docs/research/macos-support-matrix.md` (the status
tracker). The real-hardware audit findings and the earlier nix-shell/direction
docs that seeded this plan were archived once their conclusions landed here —
see git history (`docs/implementation/handoff-macos-post-ejection.md`,
`docs/plans/macos-nix-shell-backend-proposal.md`).

**TL;DR:** Pick the macOS work back up in three interleaved tracks. Track J
(Linux jail): fix the four confirmed host-agnostic audit findings, then re-port
the macos-user bootstrap from generated-Python to native Go — all of it
unit/golden-testable here. Track D (distribution): make an installed `yolo`
able to find/build its source again, in four complementary steps. Track M
(Mac): short, scripted real-hardware sessions where the agent runs under
SandVault for the approval-free dev loop and the human drives only the sudo
steps — bootstrapping toward Mac agent sessions running under yolo's own
macos-user backend, at which point SandVault retires.

---

## 0. Standing decisions — do not relitigate

- **Composed product** (2026-07-16, `docs/design/macos-no-vm-direction.md`):
  macos-user (native user + Seatbelt, no VM) is the fast default; Apple
  Container is the fallback cell for Linux-only packages or VM-grade isolation.
- **Acceptance bar:** macos-user must honor `packages:` via native
  aarch64-darwin nix from day one, or it doesn't ship. The mechanism is now a
  **buildEnv** (`flake.nix:848 packages.yoloDarwinPackages`, realized by
  `internal/darwinpkg`) — the direction docs' "devShell / print-dev-env"
  wording was superseded in the Python era (commit `4751f05`); a doc-hygiene
  pass should note that, the decision itself stands.
  **Names and lines moved (verified 2026-08-23), the bar did not.** The attr is
  now `packages.yoloNoncontainerPackages` (`flake.nix:1204`, `darwinpkg.ProfileAttr`
  at `internal/darwinpkg/darwinpkg.go:30`), and *"aarch64-darwin"* is no longer
  hardcoded anywhere in `internal/darwinpkg` — `NativeSystem()` derives the nix
  double from `runtime.GOOS`/`GOARCH` (`darwinpkg.go:46-55`), which explicitly
  replaced a `DarwinSystem = "aarch64-darwin"` constant. So the bar should now
  be read as *"native nix for the system the Mac actually is"* — an Intel Mac is
  in scope, and that is exactly the assumption class BACKLOG E8 was made of.
- Settled: mise stays as-is; Seatbelt is the accepted isolation level;
  sandbox-exec deprecation is an accepted long-term risk.
- **One settled decision diverged in shipping** (Open Decision #5): the docs
  decided per-platform `packages` overrides + an aggregated "unavailable on
  macOS" **error** (never silently skip), but what shipped is **warn-and-skip**
  (`flake.nix:846-847` filters via `darwinUnavailablePackages`;
  `internal/macosuser/orchestrator.go:196-203` warns and continues), and
  per-platform overrides don't exist in the config surface at all
  (`internal/config/derived.go` `EffectivePackages` has no platform conditional).
  **RESOLVED 2026-07-23:** implement the written design (hard error + `linux-only`
  override) — now tracked as **A2** in "Active work" below.
- **`docs/research/macos-support-matrix.md` is the tracker.** Every green cell
  this plan produces gets recorded there, not in new docs.

---

## Active work — decided 2026-07-23 (do these now)

> **Status recheck 2026-08-23: A1 ✅ · A2 ⚠ HALF DONE · A3 ✅.** Only A2 is
> still live, and only its *second* half. Verdicts and evidence are per-item
> below; the header table carries the same rows.

Three items promoted from the "Open items" list in
[macos-user-nix-and-features.md](../design/macos-user-nix-and-features.md) once
the maintainer resolved them. All three are pure-Go / flake-only and
Linux-jail-developable + testable; none needs Mac hardware. Do them before any
remaining fallback/roadmap work below.

### A1. Config-diff approval prompt on the macos-user path (security fix)

> **DONE 2026-08-18** (`bb825486` *the approval gate reaches the macos-user
> backend too*, plus `fb19e8ed` *a call is not a gate*). Verified 2026-08-23 at
> `internal/cli/run/run.go:144`.

> [!WARNING]
> **It shipped by the alternative this plan REJECTED, and the rejection was
> wrong.** The text below recommends hoisting `checkConfigChanges` above the
> runtime split and calls "call it inside the macos-user handler" an invitation
> to per-path drift. What shipped is the second one — the arm's own call site —
> because the two paths are **not** symmetric: the container arm gates the
> **fresh-launch** path only, since attaching to a running jail deliberately
> skips the check (the container was already started with its config), and
> macos-user **has no attach**. A hoist would therefore have made attach start
> prompting. The reasoning is preserved in the code comment at
> `internal/cli/run/run.go:130-144`; do not "fix" it back to a hoist.
> Two further details worth keeping: `--dry-run` is exempt (it launches
> nothing, and refusing a plan render would hide the very diff a user asked to
> inspect), and `fb19e8ed` exists because the first version of this gate was
> pinned by a test that a bare `_ = o.checkConfigChanges(cfg)` mutation walked
> straight through — *a call is not a gate*
> (`internal/cli/run/configapproval_test.go:214-236`).

> **Decided 2026-07-23: fix it.** (Was J4.) The threat model relies on this prompt
> as the Vector A mitigation; macos-user is the one backend where the poisoned
> build runs *unconfined as the invoking user*, so losing the prompt there is the
> worst case.

`checkConfigChanges` (the startup y/N config-diff prompt) is called **only** in
`runContainer` (`internal/cli/run/run.go:144`), but the macos-user branch returns
at `run.go:56-68` — *before* that call. Poisoned `packages:` on macos-user is fed
straight into a host-side `nix build --impure`
(`docs/design/macos-user-build-step-threat-model.md` Vector A), with no prompt.

**Fix:** hoist `checkConfigChanges(cfg)` to run **before** the runtime split in
`run.Run`, so both the container and macos-user paths gate on it. (Alternative:
call it at the top of the macos-user handler before `MaterializeDarwin` — rejected,
it invites per-path drift.) The one care: it must still fire **exactly once** on
the container path — `runContainer` calls it today, so remove that call when you
hoist, and add a test asserting single invocation on both paths. Pure-Go,
unit-testable; land threat-model H3 (surface the resolved `repoRoot` in the diff)
opportunistically if cheap. Closes Open item #2 in the design doc.

### A2. Darwin-unavailable packages: hard error + per-platform `linux-only` overrides

> **✅ DONE 2026-09-04.** Both pieces shipped, and the design chose `platforms`
> (a list of GOOS values) over the plan's `linux-only` boolean: the same field
> then answers "darwin-only" and anything later, and it reads as a fact about the
> package rather than as a flag about one platform. Filtering happens in
> `EffectivePackages(cfg, platform)` BEFORE materialize, so nix never evaluates an
> excluded entry and never reports it skipped — which is what lets the aggregated
> error treat everything still missing as genuine, with no second list of
> "absences that are fine" to maintain. `PackagesExcludedOn` supplies the excluded
> names to the message only, so the escape hatch is visible at the moment it is
> needed. The eval still does not abort: the flake filters, and the CLI decides
> after it, which was the original in-code objection answered.
>
> **The record below is the 2026-08-23 state, kept for the reasoning.**
>
> - **Piece 1 (aggregated hard error): NOT BUILT.** The shipped behaviour is
>   still **warn-and-skip**, at `internal/macosuser/orchestrator.go:258-268` —
>   *"Skipped packages with no `<system>` build … an unknown attr is skipped, not
>   errored, because a hard error would abort the whole eval."* That parenthetical
>   is arguing the *original* in-code objection, which A2 already answered:
>   raise the error **host-side, after the eval**, from the returned skip list.
> - **Piece 2 (`platforms` / `linux-only` override): NOT BUILT.**
>   `internal/config/derived.go:15-28` `EffectivePackages` still takes no target
>   platform and has no platform conditional. `rg -n 'platforms' internal/config`
>   is empty.
> - **What DID land, and changes the target's spelling:** the flake side was
>   rebuilt system-neutral. `darwinUnavailablePackages` is gone; the attr is now
>   `yoloUnavailablePackages` (`flake.nix:1210`, exposed as
>   `darwinpkg.UnavailableAttr`, `internal/darwinpkg/darwinpkg.go:31`) and the
>   profile is `yoloNoncontainerPackages` (`flake.nix:1204`,
>   `darwinpkg.ProfileAttr`). `internal/darwinpkg/flakeattr_test.go:55` pins both
>   old names as **dead**. The paragraph below still says
>   `darwinUnavailablePackages` and `orchestrator.go:216-223`; read those as the
>   2026-07-23 spelling — the *mechanism* it describes (flake filters, CLI
>   decides) is unchanged, only the attr names and line numbers moved.
>
> **Also still true:** the darwin no-build path has never been exercised on Mac
> hardware, so the Track M checklist line this item asks for is still unwritten.

> **Decided 2026-07-23: implement the designed behavior** (resolves Open Decision
> #5 in favor of the written design, retiring the shipped warn-and-skip). A silently
> dropped tool that the config *declared* is a footgun — it masks typos and diverges
> from the documented contract. The maintainer wants the hard error, plus a way to
> legitimately mark a package Linux-only.

Two coupled pieces:

1. **Aggregated hard error.** When any `packages:` entry has no aarch64-darwin
   build **and is not marked Linux-only** (see #2), the macos-user run **aborts**
   with a message listing *every* such package at once (not one-at-a-time), rather
   than warning and continuing (`internal/macosuser/orchestrator.go:216-223`
   today). **Keep the flake filtering as-is** — `flake.nix`'s
   `darwinUnavailablePackages` still computes the skip list, and the buildEnv still
   builds only the available set, so the nix eval does **not** abort (that was the
   original in-code objection to a hard error). The error is raised **host-side,
   after** the eval, from the returned skip list minus the Linux-only allowlist.
   That ordering is the whole trick: eval stays green, the CLI decides.
2. **Per-platform override in the config surface.** A way for a config to declare a
   package Linux-only, so it's *expected*-absent on darwin and does not trip the
   error. `internal/config/derived.go` `EffectivePackages` has **no platform
   conditional today** — add one. Recommended shape (nail this in review — it's the
   one real design choice here): extend the existing object-form package spec
   (`flake.nix` already accepts `{"nixpkgs": …}` / `{"url":…,"hash":…}`) with a
   `"platforms": ["linux"]` field; `EffectivePackages` takes the target platform and
   drops non-matching entries **before** materialize, and the aggregated-error check
   treats "declared Linux-only + absent on darwin" as fine. Both the darwin filter
   and the linux container path must read the same field so a Linux-only package
   still installs in the container.

RED-then-GREEN: a config with a genuinely darwin-less package errors and names it;
the same package marked `platforms:["linux"]` launches clean on darwin and still
installs on Linux; a typo'd attr still errors (that's the point). The
darwin no-build path was never exercised on M1 hardware (only `jq`/`just`, which
*do* build), so add a Track M checklist line to confirm the error fires live.
Update `internal/macosuser/orchestrator.go`, the e2e runbook (which currently
"expects warn-and-skip"), and `yolo config-ref` for the new field. Closes Open
item #4 in the design doc.

### A3. Drop `macos_shared_root` from the plan-invariant error message

> **DONE 2026-07-23** (`68026c61` *drop dead macos_shared_root hint from
> plan-invariant error*). Verified 2026-08-23: `rg -n macos_shared_root
> internal/` returns nothing, and the surviving message at
> `internal/macosuser/runplan.go:286` reads only *"Move it under
> `SharedRootDefault()`"*. The key was never wired, as decided.

> **Decided 2026-07-23: drop the mention, do not implement the key.**
> `/Users/Shared/yolo` is the OS-blessed neutral location and covers the real need;
> the key is read nowhere, so the message advertises a knob that does nothing.

Tiny doc-hygiene-in-code fix: in `internal/macosuser/runplan.go:235-236`, remove
the "(or set config `macos_shared_root` to another non-home path)" clause so the
message only tells the user to move the workspace under `SharedRootDefault()`.
Do **not** wire the key. (The dead `root` parameter on
`SharedRootProvisionCommands` can stay for now, or be dropped as a trivial
cleanup — not required.) A golden/string test on the invariant message guards the
wording. Closes Open item #1 in the design doc.

---

## Track J — Linux-jail work (no Mac required)

Everything here is developable and testable in this jail. Per the handoff's
classification, findings 2–5 are pure-Go fixes; finding 1 (the big re-port) and
finding 6 are jail-developable with Mac-side verification deferred to Track M.

### J1. Small confirmed fixes (independent, one commit each)

> **Status (2026-07-20): all four DONE + committed.** J1.1 runtime unification
> (`fix(runtime): unify config+platform-aware runtime resolution for ps/prune`),
> J1.2 darwinpkg drain (`fix(darwinpkg): drain nix stderr before Wait`), J1.3
> builder reaping (`fix(builder): reap the detached VM child`), J1.4 `--help`
> (`feat(cli): add top-level yolo --help/-h/help usage`). Each landed with
> RED-then-GREEN tests; J1.1 verified end-to-end in a nested jail.

1. **Runtime resolution unification** (findings 4+5).
   `internal/runtime/probe.go:29` `DetectRuntime` is env-or-`podman`,
   darwin-blind; `probe.go:44` `PsRuntime` ignores the config `runtime` key.
   `yolo ps` loads no config at all (`internal/cli/commands.go:228-236`) and
   its stale-tracking prune can delete live jails' tracking files when it
   picks the wrong runtime; `yolo prune` (`internal/prune/prunecmd.go:100`,
   `:141`) enumerates via podman on an Apple Container host. Fix: one resolver
   with run's precedence (env > config > platform probe, cf.
   `internal/cli/run/preflight.go:89-95`), plumbed into ps and prune wiring.
   Unit tests in the jail; no Mac needed.
2. **darwinpkg stderr drain** (finding 3).
   `internal/darwinpkg/materialize.go:83` calls `cmd.Wait()` before the
   stderr-pump goroutine finishes draining, truncating captured error tails,
   plus an unlocked `stderrTail` race after the 5s timeout. Fix: drain-then-Wait
   (or locked MultiWriter); add a `-race` test with a helper process.
3. **Builder detached-VM reaping** (finding 2).
   `internal/builder/real.go:168-189` never `Wait()`s the detached child, so
   `realProc.Poll` (`real.go:22-34`) can never report `done=true` and the
   "builder process exited early" fast-fail branch is dead code. A Signal(0)
   probe is not enough (unreaped zombie still signals) — fix with a
   Wait-goroutine recording exit state. Note: this landed, but the linux-builder
   VM it lived in was **removed** (Open Decisions #3, RESOLVED 2026-07-23) — the
   whole of `internal/builder` is gone, this fix with it.
4. **`yolo --help` papercut.** `--help`/`-h`/`help` exit 1 "unknown command"
   (no top-level usage handler in `internal/cli/cli.go`). Small fix, queue it.

Per AGENTS.md, every `internal/` change above still gets a nested-jail sanity
run (`yolo -- bash`) before it's called done — unit tests don't catch
container-start regressions.

### J2. The core re-port: native Go bootstrap for macos-user (finding 1)

> **Status (2026-07-21): DONE + committed.** J2.1 `12d27cb`, J2.2 `731dbe5`,
> J2.3 `1e68e24`+`544a806`, J2.4/finding-6 `e65993a`.

The dead piece: `internal/macosuser/bootstrap.go` emits a `#!/usr/bin/env
python3` script (`:77`) that `import entrypoint`s (`:101-102`) a tree staged by
`StageEntrypointCommands` (`macosuser.go:175-189`) from `RepoSrc =
repoRoot/src` (`internal/cli/commands.go:345`) — and `src/` no longer exists
anywhere.

**Design (recommended):** replace the Python bootstrap with **self-exec of the
`yolo` binary**: stage a copy of the running darwin `yolo`
(`os.Executable()`) into root-owned, world-readable `/var/yolo-jail/`
(direct analog of today's staging, same privilege rationale — the host
checkout may be unreadable to the sandbox uid, `bootstrap.go:99-100`), then run
`sudo --user=_yolojail /usr/bin/env K=V… /var/yolo-jail/yolo internal
darwin-bootstrap`. Staging must always create a **fresh inode** (`rm -f` +
`cp`, or copy-to-temp + `mv`) — macOS caches code signatures per vnode, and
overwriting a previously staged Mach-O in place gets the next exec killed
(SIGKILL, invalid signature); today's Python-text staging never hit this.
Env-on-argv visibility matches the existing exposure (LaunchArgv already
passes the full sandbox env via `/usr/bin/env -i K=V…`, `macosuser.go:317-335`,
and today's bootstrap env is baked into a 0444 root-owned file); secrets
normally ride `${VAR}` placeholders.
Why `yolo` and not `yolo-entrypoint`: the host ship set is `{yolo}` only —
an installed-only Mac (brew/release) has no other binary, and self-staging
removes the checkout dependency from the launch path entirely (which also
serves Track D). Plain-args subcommand, mirroring the existing daemon pattern.
Alternative considered: a Go-generated stdlib-only script — rejected as a
second implementation of a surface that already exists in Go.

**The generation surface already exists in Go** and is pure in
`*entrypoint.Env` (`internal/entrypoint/env.go:27-106` — JAIL_HOME-derived,
exactly the rebinding the Python bootstrap did): GenerateShims (`shims.go:19`),
GenerateAgentLaunchers (`shims.go:156`), GenerateBashrc (`shell.go:46`),
GenerateMiseConfig (`mise.go:37`), GenerateMCPWrappers (`mcp_wrappers.go:7`),
configureGit (`identity.go:12`, unexported), per-agent writers
via configureAgent (`boot.go:505-522`, unexported). The env-var contract:
`runplan.go:116-127` assembles six keys (HOST_DIR/BLOCK_CONFIG/MISE_TOOLS/
LSP_SERVERS/MCP_SERVERS/MCP_PRESETS, matching the container's `-e` contract,
`internal/cli/run/assemble.go:386-401`) — the full contract additionally
carries the git-identity vars and `YOLO_AGENTS`, and the darwin-bootstrap
subcommand must **self-set** `JAIL_HOME`/`HOME` before invoking the generators
(the rebinding today's script does at `bootstrap.go:92-96`; sudo without
`--set-home` is not a reliable HOME source).

Work items, commit-sized, in order:

1. `refactor(entrypoint):` thread the container literals through `Env` so
   generators are correct for a native home — workspace path (literal
   `/workspace` in `shell.go:124` bashrcPart3, `mise.go:148`,
   `agent_configs.go:292/328` gemini, `claude.go:108`), platform-correct shim
   realBin (`shims.go:71-73` hardcodes `/bin/`; macOS uses `/usr/bin`), BSD
   `stat -f` vs GNU `stat -c` in launcher templates (`shims.go:282,327,361`).
   No behavior change on Linux — existing goldens prove it.
2. `feat(entrypoint):` a darwin-native generation entry: export (or wrap) the
   generator set + configureGit/JJ/configureAgent; add Go writers for the two
   pieces that today exist only inside the generated Python text — the
   `yolo-log` helper (`bootstrap.go:129-133`, content already in Go as
   `MacosLogWrapperScript`, `macosuser.go:360-384`) and the
   `.zprofile`/`.zshrc`/`.bash_profile` login-rc PATH re-prepend
   (`bootstrap.go:141-144` — this carries the unverified OQ-1 path_helper fix).
   MCP wrappers: **skip the container presets natively** for now (bodies
   hardcode `/usr/bin/chromium`, `/bin/node`, `/etc/fonts` etc. —
   `mcp_wrappers.go`); document the gap rather than fake darwin variants.
   Decide mise parity here too (SandboxPath already includes mise shims,
   `macosuser.go:275`; generating the config is cheap — keep parity).
3. `feat(macosuser):` swap the launch path: stage-binary commands replace
   `StageEntrypointCommands`; `BootstrapArgv` becomes the self-exec form; drop
   the Python interpreter machinery (pythonCandidates/ResolvePython,
   `macosuser.go:60-64,148-158`, interp fallback `runplan.go:107-112`);
   replace plan invariants B2/B3 (`runplan.go:173-190`) with Go-shaped ones;
   extend the dry-run plan assertions (`orchestrator_test.go` — note there is
   no byte-golden for the macos-user plan today; creating one is a J2.3
   deliverable, with §1 of the verification runbook staying the manual
   anchor); update
   `internal/cli/check/sections_macos.go` interpreter probes and the
   macos-setup python3 warning (`internal/macosuser/commands.go:53-63`);
   remove `RepoSrc` plumbing (`commands.go:345` — keep the repoRoot handoff to
   darwinpkg's `MaterializeDarwin(parentDir(...))`, `orchestrator.go:186-188`,
   which still needs the flake when `packages:` is non-empty).
4. `fix(macosuser):` finding 6 — `setRandomPasswordReal` (`real.go:123-135`)
   passes the password via parent env that sudo's `env_reset` strips, so the
   sandbox user gets an **empty** password. Fix direction: pass via stdin to
   the root shell (`sudo /bin/sh -c 'read -r pw; dscl . -passwd … "$pw"'` with
   a `strings.NewReader` stdin — the exact pattern `installRootFileReal`
   already uses, `real.go:86-92`); never via argv (leaks in `ps`). No
   credential dance needed: SetRandomPassword runs right after ~18 consecutive
   sudo commands in the create-user branch (`commands.go:29-36`), and sudo
   prompts on `/dev/tty` anyway. Also wire the **discarded return value**
   (`commands.go:36` drops SetRandomPassword's boolean) so failure is loud —
   without that, even the fixed mechanism fails silently. Argv-construction
   unit tests in the jail; behavioral verification (password actually applied;
   `dscl` empty-string semantics) is a Track M checklist item.

**Jail exit criteria for J2:** `just test-fast` green; dry-run plan
assertions/golden show the new shape; `GOOS=darwin` cross-build of all
binaries green (`scripts/build-go.sh`); no `src/` references left under
`internal/macosuser` or `internal/cli` (grep gate); **nested-jail run**
(`yolo -- bash`) after J2.1 confirming the shared Linux entrypoint still
generates shims/bashrc/launchers and boots a container — AGENTS.md makes this
mandatory for `internal/` changes, and J2.1 touches generators the Linux
container path shares.

### J3. Container-builder rewiring (AC fallback cell, lower priority)

> **Status (2026-07-21): DONE + committed.** Resurrected `8abb67c`; wired into
> AutoLoadImage `c2f0b94`.

The Go port dropped the on-demand container-builder session from the image
path; `internal/containerbuilder` was deleted with zero importers
(support-matrix "roadmap" section). Resurrect it from git history and wire it
into `internal/image/autoload.go` so uncached `.#ociImage` builds on macOS get
the proven GHCR builder (runbook `mac-ac-container-builder.md` — zero-sudo,
agent-runnable, so Track M can verify it from inside a sandbox). Do this after
J2 — macos-user needs no builder at all.

*(J4 — config-diff prompt on macos-user — was promoted to the "Active work"
section above as A1 once the maintainer decided to fix it.)*

---

## Track D — source access for image building (the repo-root regression)

Per `docs/research/repo-root-and-distribution.md`: the Python wheel bundled
and rehydrated the source tree; the Go port kept the staging code but no Go
channel ships a bundle, so resolution step 3 is structurally dead and
installed-only binaries exit at `internal/cli/run/run.go:30-32` ("Cannot find
yolo-jail repo root") before doing anything — including before the macos-user
branch at `run.go:51-63`, which doesn't even need an OCI image. The doc's fix
options are complementary; sequence them:

1. **D1 (now, tiny): `just deploy` writes `repo_path`** into user config,
   idempotently and loudly (print what was written). Fixes every from-source
   install — which is all current installs. Also **align `yolo check`'s
   repo-root resolver** (`internal/cli/check/probes.go:320-351`, steps 1–2
   only) with run's five steps so check and run stop disagreeing for
   repo_path-only users.
   **Status (2026-07-20): DONE + committed** (`feat(install): just deploy
   records repo_path; check honors it too`). New `internal/repopath` package +
   `yolo internal write-repo-path <dir>` (idempotent, comment-preserving),
   wired into the install recipe; check's resolveRepoRoot gained run's step 4
   (user-config repo_path). Step 3 (bundle staging) stays run-owned — that is
   D3 below.
2. **D2: make the launch path degrade gracefully.**
   **REVERTED (2026-07-29).** Graceful degradation on a missing repo root was
   removed: a missing flake is once again FATAL on the container backends
   (`run.go`, the `!repoRootOK` exit after the macos-user branch), because
   silently running a possibly-stale loaded/cached image hides that the
   environment does not match the config — judged a worse failure than exiting.
   Paired with `just install` now staging the prebuilt bundle beside the binary
   (Track D / D3), so a from-source install resolves checkout-less and the fatal
   case is genuinely rare. `image.AutoLoadOptions.SkipBuild` remains a dormant
   seam (its fallback + `autoload_test.go` regressions stand), but the run path
   never sets it. Regression: `internal/cli/run/reporoot_fatal_test.go`.
   `macos-user` with empty `packages:` is still un-gated. See
   `docs/research/repo-root-and-distribution.md` §6. Original D2 record below.

   **Status (2026-07-21): DONE + committed** (`8f1d612`). Repo-root resolution is
   no longer a hard gate: `run.go` resolves it, and on a miss the launch proceeds
   degraded. `image.AutoLoadOptions.SkipBuild` (set when `repoRoot==""`) skips the
   nix build and jumps straight to the existing-image / cached-tar fallback
   (`autoload.go:133-167`, now reachable in this scenario); the assembler drops
   the `/opt/yolo-jail:ro` bind + `YOLO_REPO_ROOT` env behind one `repoBound`
   gate; `Run` prints a soft notice instead of exiting 1. Nested-jail verified
   both paths (normal binds + rebuild; degraded → cached image with neither).
   **Superseded (2026-07-23):** the prebuilt-bundle cutover removed the
   `repoBound`-gated `/opt/yolo-jail:ro` bind and the `YOLO_REPO_ROOT` env
   entirely — `/opt/yolo-jail` is now a **baked** install prefix (`flake.nix`
   `installPrefix`), no `YOLO_REPO_ROOT` is injected into the jail, and the
   in-jail CLI resolves the flake exe-relative to the baked bundle
   (`internal/reporoot`). The `SkipBuild` degradation itself is unchanged. The
   design as originally planned:
   - `macos-user` with empty `packages:` needs no repo at all once J2 lands
     (self-exec bootstrap): defer the repo-root hard-exit until a consumer
     actually needs the tree (image build, darwinpkg materialize, `/opt`
     bind), instead of unconditionally at `run.go:30`.
   - Container path: when resolution fails but `autoLoadImage`'s existing
     fallbacks would succeed (already-loaded runtime image, newest cached tar —
     `internal/image/autoload.go:133-162`, currently unreachable in this
     scenario), warn and run on the cached image rather than exiting. The
     degraded launch must **skip the nix build entirely** (never run
     `nix build` with an empty `cmd.Dir`, i.e. in the user's cwd —
     `autoload.go:227-241`), skip the `/opt/yolo-jail:ro` bind and its
     `YOLO_REPO_ROOT` env (`assemble.go:180`, `:403` — an empty repoRoot
     yields a malformed `-v` arg), and let the banner fall back to the
     ldflags-stamped buildVersion. Verify with a nested-jail run — this is a
     container-start behavior change.
3. **D3: Go-era source bundle** (the only path to checkout-less installs, and
   prerequisite for Cachix being useful to them).
   **Status (2026-07-20): DONE + committed** (`feat(dist): ship a Go source
   bundle so checkout-less installs build the image`). `scripts/stage-source-
   bundle.sh` (`git archive`, ~11MB tracked-tree superset) + `just stage-bundle`;
   `stageInstalledWheel` stages FLAT with a go.mod+flake.nix marker (frozen
   rename-aside invariant untouched); `bundledSourceDir` gains the release-
   archive `<exe>/share/yolo-jail` candidate; goreleaser stages to `bundle/`
   (outside the dist/ it wipes) + ships it in the archive; the source-build brew
   formula pkgshare-installs the fileset; check gained a read-only bundle probe.
   Verified the staged tree evaluates (`nix eval .#ociImage.drvPath`).
   Adversarially reviewed — frozen invariant clean; a goreleaser dist/-wipe
   packaging bug was caught (reproduced against goreleaser built from source) and
   fixed. D2 landed 2026-07-21 (`8f1d612`); only D4's human-gated Cachix
   account/push/download remains in Track D.
   **⚠ Both halves of that last sentence are stale (checked 2026-08-23).** D2
   was reverted on 2026-07-29 (`5d34dece`, see D2 above), and D4's Cachix
   *account* is done — what remains there is the first push + the Mac download
   proof.
   **Superseded (2026-07-23) by the prebuilt-bundle cutover.** The source-tree /
   `git archive` bundle and `stageInstalledWheel` FLAT staging (and the
   `nix-build-root` staging dir) are **gone**. The shipped/baked bundle is now
   "two files and a binary" — `flake.nix` + `flake.lock` + prebuilt
   `bin/linux-{amd64,arm64}/{yolo,yolo-entrypoint,yolo-jaild,yolo-ps}` — and the
   flake's **prebuilt short-circuit** (`builtins.pathExists ./bin/linux-<arch>`)
   copies those binaries with no Go toolchain and no source tree. Resolution is
   the pure `internal/reporoot.Resolve` (exe-relative `BundledSourceDirFrom`,
   no staging). The design points below are the historical D3 plan; read them
   as the *source-bundle* era, not the current model
   (`docs/research/repo-root-and-distribution.md` is authoritative):
   - Define the bundled layout: `share/yolo-jail/` must contain the `goSrc`
     fileset the flake needs (`flake.nix:65-80`: go.mod, go.sum, `vendor/`,
     `cmd/`, `internal/`, `bundled_loopholes/` — *that last entry no longer
     exists; the directory and its embed were deleted 2026-08-19 when every
     loophole became a pack contribution, and `packs/` took its place in the
     fileset*) **plus** `flake.nix`/
     `flake.lock`. Simplest producer is `git archive` of the full tracked
     tree — a superset of the fileset, measured ~9.9MB raw with vendor/ at
     ~7.4MB; prune to the fileset pathspecs if size matters. (vendor/ is
     committed and the flake references nothing outside the fileset, no
     export-ignore attrs, no self.rev usage — a non-git archive tree
     evaluates fine as a path flake.)
   - Rewrite `stageInstalledWheel`'s wheel-era pieces: the
     `src/cli/__init__.py` idempotence marker (`probes.go:138-139`) can never
     match a Go bundle (today staging re-runs every launch if a bundle ever
     appears), and staging into `buildRoot/src` (`probes.go:161`) is a
     Python-shaped layout. New marker: `flake.nix` + `go.mod` + a version
     stamp; re-stage on version change.
   - Ship the bundle in the goreleaser archive + brew formula; measure size
     first (vendor/ dominates — if it's ugly, flake-eval-only bundling +
     Cachix-served closures is the fallback, per the research doc).
   - Regression tests per the research doc's recommendation
     (`internal/cli/run/probes_test.go`): a bundled `share/yolo-jail/`
     resolves via step 3; the no-bundle case still errors actionably.
4. **D4 (gated on the Cachix account): the substituter is enabled**
   (`flake.nix:13-16`, `730c258`). The `publish.yml` cache-push job
   already exists and self-enables once `CACHIX_AUTH_TOKEN`/`CACHIX_CACHE`
   are configured (`publish.yml:83-102`), so remaining D4 = human Cachix
   account + `CACHIX_AUTH_TOKEN` secret + first push + Mac download. Removes
   the compile; composes with D3 (flake evaluation still needs a local tree).
   **Rechecked 2026-08-23 — the gate has narrowed.** The substituter block is
   still live at `flake.nix:13-16`, and yolo now passes `--accept-flake-config`
   on every nix invocation so the flake's own cache is actually consulted
   (`internal/image/nixflags.go:35`, `internal/darwinpkg/darwinpkg.go:91`).
   `handoff-cachix-cache.md` records the **cache, the account and the
   `CACHIX_AUTH_TOKEN` secret as all done (2026-07-20)** — so "gated on the
   Cachix account" is no longer true. Left: the **first push** and the **Mac
   download proof**. Sources disagree on the first (`docs/plans/README.md:31`
   says CI has already pushed; `handoff-cachix-cache.md` still lists it as
   remaining) and **neither is verifiable from a Linux jail** — it needs eyes on
   the cache or on a release run.

D1 is a today-sized commit. D2 pairs naturally with J2 step 3 (both touch the
run front door and the RepoSrc contract). D3 is independent and jail-testable
end-to-end; only its brew/goreleaser packaging leg needs a release cycle.

---

## Track M — Mac sessions: SandVault-bootstrapped, yolo-dogfooded exit

Goal ladder: **SandVault-wrapped agent sessions** (approval-free dev loop on
the Mac immediately) → **verify macos-user e2e** (human drives sudo) → **flip
Mac agent sessions to yolo's own macos-user backend** and retire SandVault.

The division of labor per session is fixed by what Seatbelt allows: an agent
confined by SandVault cannot sudo, and yolo's own provisioning self-escalates
per-op (`yolo macos-setup` does dscl/ACL work; the e2e runbook is explicitly
"you-drive, agent-advises" and refuses to run under sudo). So: the **agent
inside SandVault** edits, builds, tests, runs `--dry-run`/`yolo check`, and
runs the zero-sudo AC-builder runbook; the **human outside** runs the few
privileged one-shots and pastes output back.

**Track M status (2026-07-21): M0 ✅ · M1 ✅ · M2 ✅ — all verified on real
Apple Silicon (macOS 26.5).** Recipe + e2e results:
[runbooks/mac-sandvault-session.md](runbooks/mac-sandvault-session.md) (§6b).
The bullets below are the original plan; see that runbook for what actually ran.

> [!WARNING]
> **M2's dogfood flip lapsed (2026-08-19) and has since been half-recovered
> (measured 2026-09-03).** The 07-21 proof is real and stands for the build it
> tested. As of today the machine is back on current code — installed `yolo` is
> `0.8.0+881.ga6f61864`, matching HEAD, and the config is on the `packs` key — so
> the "531 commits stale / removed `agents` key" reading of this row is **retired**.
> What has NOT been recovered is a live session: the config's `file:///home/matt/…`
> pack sources do not resolve on macOS (see the header), so the launch still stops
> at pack resolution rather than at the backend. **M2 is one config edit away, and
> that edit is the maintainer's.**
>
> What the 2026-08-19 session *did* prove, by extracting the profile a real
> `--dry-run` emits and running the work under `sandbox-exec`: the **Seatbelt
> confinement half is done**. `go build ./...`, the full `go test -short ./...`
> and `just test-fast` all pass inside it; host SSH keys, `~/.claude`, `~/.aws`,
> `~/.dotfiles` and the keychains are all `Operation not permitted`. What is
> **not** proven end-to-end is the launch itself — the `sudo -u _yolojail` +
> bootstrap path around that confinement. See
> [`handoff-guest-notch-macos.md`](handoff-guest-notch-macos.md) §2 and
> `roadmap.md`'s 🔒 macOS rows.

- **M0 — bootstrap (human, ~30 min):** on the Mac: nix (flakes) + a git
  checkout with its own push credentials (deploy key — host creds stay
  invisible, same rule as jails) + `just deploy` + `repo_path` set (D1 makes
  this automatic) + install SandVault (github.com/webcoyote/sandvault) and
  smoke-test: can the sandboxed agent build Go, run `go test`, talk to the nix
  daemon socket, and run `container`/AC CLI? Whatever the profile blocks moves
  to the human column. Deliverable: a short `docs/guides/runbooks/`
  mac-sandvault-session.md recording the working recipe.
- **M1 — verification pass (after J2 lands):** agent under SandVault pulls,
  cross-checks build + dry-run goldens on darwin; human drives
  `mac-macos-user-e2e.md` §3–§7: macos-setup, first real Seatbelt launch
  (whoami→`_yolojail`), **§5 acceptance bar** (`which jq` →
  `/nix/store/...`), **OQ-1** (login-shell PATH survives path_helper), real
  agent launch + host-creds-invisible check, finding-6 password check
  (`dscl . -read` authentication actually set), teardown idempotence. Also
  verify the staged self-exec binary runs clean under Gatekeeper/quarantine
  (copied ad-hoc-signed Go binary — expected fine, verify anyway) **and that
  re-staging over a prior stage still execs** (the fresh-inode rule from J2;
  an in-place overwrite dies with SIGKILL from the vnode signature cache).
  Findings
  come back as a handoff doc; fixes happen in the jail; repeat as needed.
- **M2 — dogfood flip:** once e2e is green, Mac agent sessions become
  `YOLO_RUNTIME=macos-user yolo -- claude` — yolo is now its own SandVault
  with the nix layer. Retire SandVault from the loop. Update the support
  matrix cells (macos-user "run agent" [M], AC "run agent in jail" [M] if
  exercised), then rewrite `docs/guides/macos.md` (it still says macos-user
  "was removed", lists uv/cli.py-era prerequisites — done in `43bd846`) —
  deliberately **after** the launch works, so the guide never advertises a
  broken backend.

---

## Track L — loophole framework on macos-user (future; use-case-gated)

> **Status: NOT STARTED. Sequencing UNCHANGED** — recorded 2026-07-23 from the
> `macos-user-nix-and-features.md` §3.5 discussion, still a forward-looking
> capability and not a revival blocker.
>
> **2026-09-03, and read this before reusing part 1 for anything:** a revision of
> §*Self-hosting* earlier that day re-sequenced this track to "dev-loop enabler",
> on the theory that a host-side daemon is how a macos-user jail could start a jail.
> **That was retracted the same day** — a daemon that starts jails on request from
> inside a jail is the `docker.sock` antipattern, and the CVE history of the closest
> real analogue (`com.docker.vmnetd`, CVE-2020-15360) is the failure mode. Track L
> part 1 remains what it was: transport plumbing for loopholes whose host daemon
> mediates a *narrow, named* resource. **"Start a jail" is not such a resource**, and
> the distinction — a daemon that does one bounded thing vs. one that does whatever
> the caller describes — is the line part 1 must not cross.
>
> **Still NOT STARTED, rechecked 2026-08-23.** The loophole host-service
> lifecycle is called exactly once, at `internal/cli/run/run.go:569`
> (`startLoopholesDisclosed`), which is inside `runContainer`
> (`internal/cli/run/run.go:308`) — so the macos-user arm, which returns above
> it, starts no host service at all. One primitive of part 1 was built ahead of
> the rest and is **uncalled**: `macosuser.EndpointGrantCommands`
> (`internal/macosuser/macosuser.go:430`) grants the sandbox uid READ on a
> published endpoint file by ACE, and `rg -n EndpointGrantCommands` finds no
> non-test caller.

> [!WARNING]
> **The "three bundled loopholes" framing below is superseded, and the count is
> wrong now.** As of 2026-08-19 `bundled_loopholes/` is deleted and every
> loophole is a **pack** contribution — `audio`, `host-processes`, `journal` and
> `cgroup-delegate` are packs of their own, and `claude-oauth-broker` is
> contributed by `packs/claude`. The paragraph's *argument* survives the rename
> intact (a native process makes `audio`/`host-processes` moot, and the shared
> `/Users/_yolojail` home makes the OAuth broker redundant); only the noun
> "bundled" and the number "three" are stale. See `AGENTS.md`.

The three *bundled* loopholes don't need porting to macos-user (see
[macos-user-nix-and-features.md](../design/macos-user-nix-and-features.md) §3.5:
`audio`/`host-processes` are moot on a native process, and `claude-oauth-broker`
is redundant with the shared `/Users/_yolojail` home). But the **loophole
framework** — "a host-side daemon mediates the jail's access to a resource" — is
backend-agnostic and worth carrying onto macos-user, because a native jailed
process is arguably a *better* fit than a container: it reaches host `localhost`
sockets/ports **directly** (the Seatbelt profile is `(allow default)` for
network), so a loophole collapses to *host daemon on a localhost socket/port + a
launch-env var pointing the jail's clients at it* — no bind mount, no `--add-host`
redirection plumbing.

**The motivating use case** (the reason to build this at all): a host-side
**access-scoping / auditing proxy** — e.g. a daemon that intercepts the jail's
outbound GitHub traffic, scopes a broad PAT down to a least-privilege token, and
logs/filters requests. On macos-user the wiring is cheap: set
`HTTPS_PROXY=http://127.0.0.1:PORT` (or a scoped `GH_*`/`GITHUB_*` env) in the
launch env and `git`/`gh`/`curl` all honor it, while the host daemon owns the real
credential and never lets it cross into the jail.

**Two-part shape:**

1. **Framework plumbing (unblocked, mechanical).** Generalize the loophole
   host-service start/stop so it runs on the macos-user launch path (today it lives
   only in `runContainer`; see §3.6), emitting a localhost socket/port + the
   launch-env var per active loophole instead of a mount + `--add-host`. Reuse the
   existing manifest/`Discover` machinery; the transport just changes.
2. **The specific access-scoping proxy (BLOCKED — see OQ-L1).** The daemon that
   does the actual GitHub token-scoping + request filtering. Do **not** build this
   until OQ-L1 is resolved — getting the scoping model wrong ships a false security
   boundary, which is worse than none.

---

## Self-hosting: can a macos-user jail launch a macos-user jail?

> **Measured 2026-09-03** on the maintainer's Mac (macOS 26.5, arm64), against the
> Seatbelt profile a real `--dry-run` emits. Every table row below is an observed
> result, not a reading of Apple's docs.

**The ask.** The Linux dev loop verifies `internal/` changes by launching a nested
jail, and AGENTS.md makes that mandatory. If macos-user could do the same, the
loop would be dramatically faster — this backend builds no image at all, and its
whole plan renders in **0.093s** against ~45s+ for a nested Linux image rebuild.
It would also be fully agent-driven, with no human at a sudo prompt.

**It cannot, and there are two independent structural reasons.**

### 1. `sudo` cannot exec inside any Seatbelt sandbox

Not "is not authorized" — the exec itself is refused, and it is refused under a
profile that allows everything:

```console
$ sandbox-exec -p '(version 1)(allow default)' /bin/sh -c '/usr/bin/sudo -n true'
/bin/sh: /usr/bin/sudo: Operation not permitted
```

Seatbelt denies setuid exec regardless of profile content, so no profile edit
reaches it. Every macos-user launch step is privileged — the root-owned staging
into `/var/yolo-jail` (`StageBinaryCommands`, `StagePackCommands`), the 0444
profile install, and `sudo --user=_yolojail` itself — so the entire privileged
half of the launch is unreachable from inside a jail.

### 2. Seatbelt does not nest a *different* profile

`sandbox_apply` refuses any profile that is not effectively identical to the one
already applied. This is an **equality** constraint, not a "may only narrow" one —
which is the surprise, and it is why "just hand the inner jail a stricter profile"
is not an option:

| outer profile | inner profile | result |
| :--- | :--- | :--- |
| `(allow default)` | `(allow default)` | applies |
| `(allow default)` | `(allow default)` + one extra `deny` | `sandbox_apply: Operation not permitted` |
| yolo's profile | yolo's profile | applies (and the outer denies still hold in the child) |
| yolo's profile | yolo's profile + one extra `deny` | `Operation not permitted` |
| yolo's profile | `(allow default)` | `Operation not permitted` |

Row 3 is why an early reading of this looked like "nesting works": re-applying the
*same* profile succeeds, because it is a no-op. Nothing that would make the inner
jail different from the outer one is permitted. This matches the general finding
that macOS sandboxes do not nest.

### The helper-daemon idea, and why it is REJECTED

> **⚠ Retracted 2026-09-03, the same day it was written.** An earlier revision of
> this section recommended a host-side helper daemon as the unblocker, on the
> strength of a working measurement. The measurement stands; the recommendation
> was wrong, and it was wrong on the axis the measurement could not see.

The mechanism does work. A process under yolo's profile can connect to a unix
socket, and an unsandboxed helper on the other end can spawn a `sandbox-exec` child
under a *different* profile — because that child is a sibling of the caller, not a
descendant, so no `sandbox_apply` happens in a sandboxed process at all:

```
sandboxed client ──unix socket──► unsandboxed helper ──sandbox-exec──► fresh jail
   (profile A)                      (no profile)                        (profile B)
```

**And that is precisely the shape of the worst-known container antipattern.**
Bind-mounting `/var/run/docker.sock` into a container is root-equivalent for exactly
this reason: the container needs no privilege, no capability and no namespace
escape — *it asks a privileged daemon to do the work*. A yolo helper that accepts
"start a jail" from inside a jail is the same object with a different noun. Three
specific problems, none of which the socket experiment touches:

1. **The policy must not come from the caller, and it is not obvious what else it
   can come from.** If the jail supplies the profile, it supplies `(allow default)`
   and the confinement is over. If the helper derives the profile, it derives it
   from a workspace path the jail names — so the jail names `/Users/matt` and the
   confinement is over again. The only sound rule is *inner ⊆ outer*, which is the
   check Seatbelt would have performed if it could nest, now reimplemented in our
   code, in SBPL, correctly, forever.
2. **On this backend the helper cannot tell WHICH jail is calling.** `SandboxUser`
   is the constant `_yolojail`; every jail on the machine is that one uid, so peer
   credentials on the socket identify the account and not the session. Per-session
   endpoint files (which `EndpointGrantCommands` grants by ACE) are grants to the
   same uid, so any jail can read any of them. This is OQ-L1's "how does it
   authenticate which jail is calling" question, arriving early and with no answer.
3. **The precedent is bad.** `com.docker.vmnetd` — the closest real analogue, a
   macOS privileged helper for a container runtime — shipped a local privilege
   escalation *for lack of client verification* (CVE-2020-15360), which is failure
   mode 2 above, in production, in the product that invented this pattern.

A root helper would additionally be a permanent, always-listening escalation path
on the host, added to buy a *development convenience*. That trade is not close.

### What podman-in-podman actually gives, and the macOS shape of it

The Linux loop does not delegate. **The jail runs its own container engine** —
`--userns=host`, `--net=host`, `--cgroups=disabled`, and the inner container is a
child of the outer, bounded by it. No host daemon is asked for anything, so the
host gains no request surface. That difference — *run your own engine* vs *ask a
privileged one* — is the whole security story, and it is why the Linux loop is fine
and the helper is not.

It is also what makes loophole development work there: the outer jail IS the host
for the inner container, which is exactly the trick AGENTS.md documents for the
reachability carve-out (bind a listener on the jail's own loopback, dial it from a
`podman run` container). A helper-spawned sibling reproduces none of that — it
shares the outer jail's uid AND its home (`SandboxHome()` is a constant), so the
boundary under test would not be the boundary that exists.

**There is a macOS analogue, and it is the same shape.** A Seatbelt-sandboxed
process CAN start a VM through `Virtualization.framework` — no helper, no daemon,
no privilege change — once the profile grants what the framework needs. The
reported denial is an XPC lookup, and the reported fix is four SBPL rules:

```scheme
(allow mach-lookup (global-name "com.apple.Virtualization.VirtualMachine"))
(allow mach-task-name)
(allow generic-issue-extension (extension-class "com.apple.virtualization.extension.fuse") …)
;; plus writable paths for the VM's own disk/cache dirs
```

Verified here only that a yolo profile carrying the first two **parses and applies**
(2026-09-03); the VM start itself is NOT reproduced on this machine and the source
is a single write-up ([brentfitzgerald.com](https://brentfitzgerald.com/posts/lima-vms-from-a-sandboxed-agent/),
2026-05-27). `mach-task-name` is broad and cannot be scoped to one executable —
that author says so too.

If it holds, a macos-user jail can host a **podman machine or Apple Container**
inside itself, and a nested `yolo` on those runtimes behaves exactly as it does on
Linux. The authority stays with the jail's own uid; nothing outside the sandbox
gains a new thing it will do on request. That is the property the helper lacks.

### What CAN be developed inside a macos-user jail — measured, and it is most of it

The nesting answer above is about launching a *whole* jail. That is not the same
question as "can this backend be developed from inside itself", and the second
question has a much better answer. Measured 2026-09-03, from inside a real
macos-user Seatbelt profile:

| Piece of a macos-user launch | Inside a jail? | How |
| :--- | :--- | :--- |
| config load, validation, pack staging, plan build | **yes** | `yolo run --dry-run` runs unmodified — 0.093s, invariants and all |
| **the entire bootstrap** — shims, `.bashrc`/`.zshrc`/`.zprofile`, mise config, MCP wrappers, `yolo-log`, launcher dirs | **yes** | `yolo internal darwin-bootstrap` with `HOME`/`JAIL_HOME` pointed at a writable temp dir. It took its whole contract from env vars already |
| the generated blockers actually blocking | **yes** | exec the generated `~/.yolo/bin/block/grep` — it refused and printed its suggestion |
| **the staging commands, really executed** | **yes** | take the argv the plan prints, drop the `sudo` prefix, redirect `/var/yolo-jail` → a writable path. Verifies the commands, the `a+rX` mode, AND the J2 fresh-inode rule (re-staging over a prior stage still execs — the macOS vnode signature-cache trap) |
| profile GENERATION | **yes** | a pure function over `(workspace, home, readonly)` |
| profile SEMANTICS | **partly** | not for an arbitrary profile (no nesting), but the jail you are sitting in is a live specimen of one — which is exactly what the 2026-08-19 session measured |
| `sudo` itself | **no** | setuid exec is denied in any Seatbelt sandbox |
| the `sudo -u _yolojail` identity switch | **no** | same |
| applying a NEW Seatbelt profile | **no** | the equality constraint above |

The irreducible remainder is therefore narrow and nameable: **"does the privilege
transition work"** — three lines of the launch, not a development loop. Everything
around it is exercisable in-jail today, with no product changes, because these
pieces already take their inputs from env and argv rather than from ambient state.

**The gap worth closing is automated coverage, not capability.** `rg darwin-bootstrap
--type go` finds tests in `internal/entrypoint` only, and `integration/` has no
macos-user file at all — so none of the three green rows above is pinned by anything
that runs in CI. A darwin-gated harness that (1) bootstraps into a temp home and
asserts the generated tree, and (2) runs the staging argv against a temp dest and
asserts mode + fresh inode, would convert this section from "an agent can check by
hand" into a regression suite, and it needs no privilege to run.

**One thing NOT to build:** a `--no-confine` launch mode that skips the sudo and
sandbox-exec steps so the "whole thing" appears to run in-jail. It would be a mode
that looks like a jail and is not one, which is the exact failure class
`workspace_readonly`-on-macos-user was fixed for (`d0961f2c`, *stop
`workspace_readonly` lying*). Keep the remainder visible as a remainder.

### And the option that needs no new code at all

**Develop yolo on the Mac in a podman jail, not a macos-user jail.** The Mac has
`podman` (applehv provider) and Apple Container; a container-backend jail there
gives the Linux dev loop unchanged, podman-in-podman included, under the agent's
full control and with no sudo. macos-user then stops being the *development*
environment and is only the backend *under test* — which is what it is anyway,
since a macos-user jail cannot verify a macos-user change no matter how the nesting
question is answered. Requires `podman machine init` on that Mac (measured
2026-09-03: the binary is installed, no machine is created).

### OQ-SH-1 — is macos-user self-hosting worth pursuing at all? (maintainer)

Ranked, after the retraction above:

0. **Develop the PIECES in a macos-user jail — available today, nothing to build.**
   Bootstrap, staging, plan and profile generation all run in there (§*What CAN be
   developed inside a macos-user jail*). This is the answer to "I want to develop
   macos-user inside a jail"; what it cannot cover is the privilege transition, and
   nothing can.
1. **Develop in a podman jail on the Mac** when a WHOLE nested jail is the point —
   loophole development, launch-path end-to-end. No new code, no new attack surface,
   genuine nesting. One `podman machine init`.
2. **VM-in-sandbox** (the four SBPL rules), if a macos-user jail specifically needs
   to host an inner jail. Needs reproducing on real hardware first; widens the
   profile in named, bounded ways; keeps authority inside the jail's own uid.
3. **Scoped `NOPASSWD` sudoers drop-in** — orthogonal to all of the above. It buys
   a prompt-free OUTER launch and nothing else, and it is NOT free: the staging argv
   includes `cp -f <source> /var/yolo-jail/yolo.new` followed by a root `mv`, which
   is a write-anything-as-root primitive unless the rule pins the source path
   exactly. Worth doing, worth pinning.
4. **The helper daemon — do not build.** See the retraction.

**Not blocking anything.** Every Mac-gated proof in this plan is performed from an
unsandboxed Mac shell, which needs none of this.

---

## Sequencing at a glance

```
DONE:  J1.1 J1.2 J1.3† J1.4  D1‡ ─►  J2.1 J2.2 J2.3 J2.4 + D2✗ ──► D3‡ ──► J3
                               │                     │                       │
mac:                           └─ M0 (SandVault)     └─ M1 (e2e verify) ──► M2 (dogfood, docs)

DONE (A-track):  A1 ✅ 2026-08-18      A3 ✅ 2026-07-23
NOW:             A2 ⚠ HALF — the aggregated hard error + `platforms` override are still unbuilt
LATER:           Track L part 1 (framework plumbing on the macos-user launch path) — NOT STARTED
                 Track L part 2 (the scoping proxy) — gated on OQ-L1

† J1.3's fix landed and was then deleted with `internal/builder` (Open Decision #3).
‡ D1's `repo_path` key was RETIRED 2026-07-23; D3's source bundle was SUPERSEDED by the prebuilt bundle.
✗ D2 was REVERTED 2026-07-29 — a missing repo root is fatal again.
```

**The one live engineering item is A2's second half** — independent, pure-Go /
flake-only, Linux-jail-developable + testable. *(Amended 2026-09-03: still true of
the items this plan had ALREADY scoped, but Track L part 1 is now a live candidate
too — see §Self-hosting. And two smaller defects were found and fixed on
2026-09-03; they were never in this list because nobody had measured them.)* It carries a Track M checklist
line (confirm the hard error fires live on a genuinely darwin-less package —
never exercised on M1). *The DONE row is "landed at some point", not "in the
tree today": read the daggers.* Verified 2026-08-23.

## Open decisions (maintainer input wanted, none blocking J1/D1)

> **Rechecked 2026-08-23: only #4 is genuinely open, and #5 is decided-but-not-shipped.**
> #1 and #2 were settled by shipping rather than by a ruling — annotated in place
> below; #3 was resolved and executed.

1. **Bootstrap vehicle** — plan recommends self-staged `yolo` +
   `yolo internal darwin-bootstrap`; alternative is a subcommand on
   `yolo-entrypoint` (not in the host ship set — would change distribution).
   **SETTLED BY SHIPPING (J2, 2026-07-21):** the self-staged `yolo` won.
   `internal/macosuser` carries no Python interpreter machinery, and the host
   ship set is still `{yolo}` alone (`AGENTS.md`). Verified 2026-08-23.
2. **D3 bundle scope** — full `git archive` source bundle vs flake-eval-only +
   Cachix. Measure the archive first.
   **MOOT (2026-07-23):** the prebuilt-bundle cutover chose neither. The shipped
   bundle is `flake.nix` + `flake.lock` + prebuilt `bin/linux-<arch>/` binaries,
   and the flake short-circuits on `builtins.pathExists ./bin/linux-<arch>` — no
   source tree, no Go toolchain. See D3's superseded note above.
3. **linux-builder VM** — **RESOLVED 2026-07-23: remove it entirely.** The
   container builder (J3, `internal/containerbuilder`) is proven on **both**
   runtimes — podman end-to-end in-jail and Apple Container on real HW
   (2026-07-17, `AC-CONTAINER-BUILDER-WORKS`) — and is wired into the real run
   path (`autoload.go` `BuildOffload`) as an automatic, zero-setup offload: no
   `yolo builder` command, no `sudo`, no QEMU, no idle RAM. It covers every
   matrix cell the VM builder did, in a strictly more happy-path way (the VM
   builder's foreground-QEMU trap, per-build first-boot `sudo`, and CWD-relative
   `KEYS` reconcile — the last of which is a live wedge, see
   [../design/linux-builder-lifecycle.md](../design/linux-builder-lifecycle.md) —
   are precisely the complexity the container path avoids). The only reason the
   VM builder was ever kept as a fallback (AC couldn't be shown to host an sshd
   container) is discharged. **Action:** delete `internal/builder` + the `yolo
   builder {setup,start,stop,status}` commands, and rewire `yolo check`'s Image
   Build section onto the container-builder reality (it currently points at the
   VM builder while a real build uses the container). The user's *own* nix-darwin
   `linux-builder` remains a valid orthogonal escape hatch (their nix config, not
   ours to install) — removal is of *our* VM-builder machinery, not of the user's
   ability to point nix at any remote builder. Tracked on the
   [sequencing-2026-07](sequencing-2026-07.md). **DONE 2026-07-23:** `internal/builder` + the `yolo
   builder {setup,start,stop,status}` commands are deleted; `yolo check`'s Image
   Build section and the run-path failure remedy (`nixdiag.LinuxBuilderRemedy`)
   are rewired onto the container builder; user-facing docs reconciled.
4. **MCP presets on native macOS** — skip-and-document (recommended) vs
   building darwin wrapper variants.
   **STILL OPEN, and the tree took neither branch (found 2026-08-23).** J2 step 2
   below says *"skip the container presets natively for now … document the gap
   rather than fake darwin variants."* That is not what ships:
   `internal/entrypoint/darwin.go:59` runs `GenerateMCPWrappers` as an
   unconditional `genStep`, and the wrapper bodies are Linux-absolute —
   `/usr/bin/chromium` (`mcp_wrappers.go:39`), `exec /bin/node`
   (`mcp_wrappers.go:74`), `/etc/fonts/fonts.conf` (`:26-27`, `:72-73`) — with no
   `GOOS` guard anywhere in the file. So a macos-user home gets three executable
   wrappers pointing at paths that do not exist on macOS. **Harmless until
   something execs one**, which is why it has not surfaced; the decision is now
   *"skip, or port, or leave the dead wrappers and say so"* rather than the
   two-way choice recorded in 2026-07.
   **RESOLVED 2026-09-03 — SKIP AND SAY SO**, which is what this plan's own J2 step 2
   recommended in the first place. `GenerateMCPWrappers` no longer runs in the darwin
   bootstrap, and a config that declares `mcp_presets` is told they are not delivered
   here and pointed at `mcp_servers`. Porting was rejected: a darwin variant would have
   to locate Chrome, node and a fontconfig on a machine yolo did not provision and would
   guess wrong on most of them — an absent wrapper that says so beats a present one that
   lies, the same ruling `workspace_readonly` got on this backend (`d0961f2c`). Verified
   on macOS 26.5, where all three hardcoded paths are absent.
5. **Darwin-unavailable packages: warn-and-skip vs aggregated error** (see
   §0) — the written decision says error + per-platform `packages` overrides;
   the shipped code warn-and-skips and the overrides were never built.
   **RESOLVED 2026-07-23 in favor of the written design:** implement the
   aggregated hard error + a per-platform `linux-only` override. Now tracked as
   **A2** in the "Active work" section above.
   **⚠ Decided but NOT SHIPPED, rechecked 2026-08-23.** The code still
   warn-and-skips (`internal/macosuser/orchestrator.go:258-268`) and the override
   does not exist (`internal/config/derived.go:15-28`). This is the one decision
   in this list whose ruling the tree does not yet reflect.

## Open questions (blocking)

- **OQ-L1 — the access-scoping model for the Track L proxy.** *Blocks Track L
  part 2 (the specific proxy), not part 1 (framework plumbing).* Before building
  the GitHub-scoping/auditing daemon, the maintainer needs to pin down what
  "scoped access" precisely means: which credential the host daemon holds and how
  it mints the narrowed one (fine-grained PAT vs GitHub App installation token vs
  short-lived OIDC exchange); the scoping axes it must enforce (repo/org
  allowlist, read-vs-write, which API surfaces); how it authenticates *which jail*
  is calling so per-jail scopes don't leak across jails on a shared host; and what
  the audit log captures and where it lives. Getting this wrong ships a **false**
  security boundary — an agent believing it is sandboxed to one repo while the
  token reaches others — which is worse than shipping nothing. Resolve deliberately
  (a short design note), then build. Until then part 2 stays parked; part 1 can
  proceed independently since it's just transport plumbing.

## Risks / watch items

> **Rechecked 2026-08-23.** The first three closed on real hardware in M1
> (2026-07-21); the fourth stands, and two new ones have joined it.

- ~~OQ-1 (path_helper) stays the headline unknown until M1~~ — **CLOSED
  2026-07-21 on real HW.** The login-rc PATH re-prepend holds: `which just` →
  `/nix/store/…/bin/just`, not Homebrew's `/usr/local/bin`. Recorded in
  `../research/macos-support-matrix.md` §5 item 2.
- ~~SandVault's profile may block nix-daemon or AC access~~ — **CLOSED
  2026-07-21** (M0 passed; recipe in `runbooks/mac-sandvault-session.md`). And
  as of 2026-08-19 the successor question is answered too: yolo's **own**
  Seatbelt profile runs `go build ./...`, the full `go test -short ./...` and
  `just test-fast`, and reaches the nix daemon (`Trusted: 1`).
- ~~`dscl` empty-password semantics (finding 6) unknown until M1~~ — **CLOSED
  2026-07-21**: the password is actually set (matrix §1).
- **sandbox-exec deprecation and AC's non-reclaiming memory balloon:** accepted,
  on record, no action. *(Unchanged.)*
- **NEW — `x86_64-darwin` is on a clock.** nixpkgs 26.11 has **dropped** it, so
  the flake pins `nixpkgs-26.05-darwin` for that one system (`flake.nix:22-42`,
  `927fb9f`). 26.05 is the last supporting branch and is security-fixed only to
  **end of 2026**. The macOS nightly must run on `macos-26-intel`
  (`.github/workflows/nightly-macos.yml:50`) because GitHub's Apple Silicon
  runners cannot nest a VM for Podman Machine — so when 26.05 lapses the choice
  is a self-hosted arm64 Mac runner or macos-user-only macOS tests. A deadline,
  not a bug.
- ~~**NEW — the Mac that proved Track M has drifted off the product.**~~ —
  **LARGELY CLOSED 2026-09-03.** The binary is current (HEAD) and the config is
  on `packs`. What survives is narrower and different in kind: the config's
  `file://` pack sources are absolute **Linux** paths in a dotfiles tree shared
  with the Linux host, and yolo expands neither `~` nor env vars in a local pack
  source — so one config genuinely cannot name one pack tree on both machines.
  The failure is loud and early (`local pack /home/matt/… is not a directory`),
  which is why it is a risk item and not a defect. **Still the maintainer's
  config, the maintainer's call** — but it is now the ONLY thing between this
  plan and a live macos-user session.
- **NEW — `sandbox-exec` deprecation is now load-bearing, not just accepted.**
  The self-hosting analysis below turns on Seatbelt's inability to nest, and any
  successor confinement API Apple ships would reopen that question. The risk was
  already on this list as "accepted, no action"; what changed is that a decision
  now depends on it.
