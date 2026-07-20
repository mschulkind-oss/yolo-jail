# Plan: macOS revival + source-distribution fix (post-ejection)

**Date:** 2026-07-20. **Status:** PLANNED — no implementation started.
**Inputs:** `docs/implementation/handoff-macos-post-ejection.md` (real-hardware
state, audit findings), `docs/research/repo-root-and-distribution.md` (the
source-access regression), `docs/design/macos-no-vm-direction.md` +
`docs/plans/macos-nix-shell-backend-proposal.md` (settled direction),
`docs/research/macos-support-matrix.md` (the status tracker).

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
- Settled: mise stays as-is; Seatbelt is the accepted isolation level;
  sandbox-exec deprecation is an accepted long-term risk.
- **One settled decision diverged in shipping** and needs a maintainer call
  (Open Decision #5): the docs decided per-platform `packages` overrides + an
  aggregated "unavailable on macOS" **error** (never silently skip), but what
  shipped is **warn-and-skip** (`flake.nix:846-847` filters via
  `darwinUnavailablePackages`; `internal/macosuser/orchestrator.go:196-203`
  warns and continues — and argues why a hard error would abort the whole
  eval; the e2e runbook expects warn-and-skip), and per-platform overrides
  don't exist in the config surface at all (`internal/config/derived.go`
  `EffectivePackages` has no platform conditional).
- **`docs/research/macos-support-matrix.md` is the tracker.** Every green cell
  this plan produces gets recorded there, not in new docs.

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
   Wait-goroutine recording exit state. Note: the linux-builder VM is
   **fallback-track** (see Open Decisions #3); land this cheap correctness fix,
   build nothing else on it.
4. **`yolo --help` papercut.** `--help`/`-h`/`help` exit 1 "unknown command"
   (no top-level usage handler in `internal/cli/cli.go`). Small fix, queue it.

Per AGENTS.md, every `internal/` change above still gets a nested-jail sanity
run (`yolo -- bash`) before it's called done — unit tests don't catch
container-start regressions.

### J2. The core re-port: native Go bootstrap for macos-user (finding 1)

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
configureGit/configureJJ (`identity.go:12/:32`, unexported), per-agent writers
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

The Go port dropped the on-demand container-builder session from the image
path; `internal/containerbuilder` was deleted with zero importers
(support-matrix "roadmap" section). Resurrect it from git history and wire it
into `internal/image/autoload.go` so uncached `.#ociImage` builds on macOS get
the proven GHCR builder (runbook `mac-ac-container-builder.md` — zero-sudo,
agent-runnable, so Track M can verify it from inside a sandbox). Do this after
J2 — macos-user needs no builder at all.

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
   - Define the bundled layout: `share/yolo-jail/` must contain the `goSrc`
     fileset the flake needs (`flake.nix:65-80`: go.mod, go.sum, `vendor/`,
     `cmd/`, `internal/`, `bundled_loopholes/`) **plus** `flake.nix`/
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
4. **D4 (gated on the Cachix account): enable the substituter**
   (`flake.nix:17-20`, commented out). The `publish.yml` cache-push job
   already exists and self-enables once `CACHIX_AUTH_TOKEN`/`CACHIX_CACHE`
   are configured (`publish.yml:83-102`), so D4 is account creation + the
   uncomment. Removes the compile; composes with D3 (flake evaluation still
   needs a local tree).

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
  "was removed", lists uv/cli.py-era prerequisites) — deliberately **after**
  the launch works, so the guide never advertises a broken backend.

---

## Sequencing at a glance

```
jail:  J1.1 J1.2 J1.3 J1.4  D1 ──►  J2.1 J2.2 J2.3 J2.4 + D2 ──► D3 ──► J3
                              │                    │                      │
mac:                          └─ M0 (SandVault)    └─ M1 (e2e verify) ──► M2 (dogfood, docs)
```

Everything left of M1 is quota-light, self-contained commits in this jail.

## Open decisions (maintainer input wanted, none blocking J1/D1)

1. **Bootstrap vehicle** — plan recommends self-staged `yolo` +
   `yolo internal darwin-bootstrap`; alternative is a subcommand on
   `yolo-entrypoint` (not in the host ship set — would change distribution).
2. **D3 bundle scope** — full `git archive` source bundle vs flake-eval-only +
   Cachix. Measure the archive first.
3. **linux-builder VM** — support matrix already demoted it to
   roadmap/fallback; recommend parking it (keep the J1.3 correctness fix,
   skip idle-stop watchdog / lifecycle knob until someone needs the fallback).
4. **MCP presets on native macOS** — skip-and-document (recommended) vs
   building darwin wrapper variants.
5. **Darwin-unavailable packages: warn-and-skip vs aggregated error** (see
   §0) — the written decision says error + per-platform `packages` overrides;
   the shipped code warn-and-skips and the overrides were never built. Either
   bless warn-and-skip retroactively (doc-hygiene fix) or add a J-track item
   implementing the decision as written, with M1 verifying whichever wins.

## Risks / watch items

- OQ-1 (path_helper) stays the headline unknown until M1 — the login-rc fix
  is ported byte-for-byte but its runtime effect has never been observed.
- SandVault's profile may block nix-daemon or AC access (M0 smoke test
  decides the inside/outside split; worst case the human column grows).
- `dscl` empty-password semantics (finding 6) unknown until M1.
- sandbox-exec deprecation and AC's non-reclaiming memory ballon: accepted,
  on record, no action.
