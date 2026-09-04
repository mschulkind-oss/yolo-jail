# Plan: evergreen agent updates — the launcher does the work

**Design:** [`../design/program-delivery.md`](../design/program-delivery.md) §3.5
(OQ-PD11–PD14, PD12a) · **Status:** ready, blocked on sequencing (see Blockers) ·
Written against `a25e718b`, 2026-09-03.

**Precedence:** the design wins on behavior; the tree wins on fact; this file is advice and
is the first thing to be wrong. Never twist the code to match it.

**Banked, re-verified in this jail 2026-09-03:** `claude.stamp` last touched 2026-08-25 — the
hourly poll has not fired in 9 days, because the install prefixes precede the launch dir on
`BootPath` and the launcher is unreachable after its own first install. `@openai/codex` 0.145.0
(2026-07-25), `@github/copilot` 1.0.48 (**2026-05-15**).

## Map

| Path | Change |
| :--- | :--- |
| `internal/entrypoint/boot.go` | `BootPath` (356–361): `LaunchDir` moves from last to **second**, after `BlockDir`. Rewrite the doc comment above it — it argues for the old position |
| `internal/entrypoint/shell.go` | the `.bashrc` `export PATH` (135–149) is a **second, independently-written** string. Move `$LAUNCH_DIR` there too; the comment block at 139–144 states the old rule |
| `internal/entrypoint/shims.go` | the generation-time collision check; the native template's update branch; delete `_poll_and_report` and the `"$REAL_BIN" install` no-op; splice the update verb, the policy bit, the re-entry guard, the prefix lock |
| `internal/packdecl/packdecl.go` | `Install.UpdateVerb` |
| `internal/packdecl/contributes.go` | `Contribution.Update` (program, ~line 38 block); project it in `InstallContributions` (437); no change to `knownVias` |
| `internal/config/agentupdates.go` | **new** — user-scope loader, mirroring `hostapplyonlaunch.go` |
| `internal/config/config.go` | `agent_updates` into `knownTopLevelConfigKeys` (46) |
| `internal/config/validate.go` | `validateAgentUpdates` — shape + workspace-scope refusal, mirroring `validateHostApplyOnLaunch` (496–515) |
| `internal/cli/run/assemble.go` | `-e YOLO_AGENT_UPDATES=` beside `YOLO_MCP_PRESETS` (773) |
| `internal/cli/check/entrypoint.go` | the same var into the preflight's fake env (45) |
| `internal/macosuser/runplan.go` | the same var into `bootstrapEnv` (176) |
| `internal/cli/packupdate.go` | drop the `inst.Kind != "npm"` skip (141); the npm-shaped identifiers get honest names |
| `internal/entrypoint/serverrefresh.go` | **new** — the transitive MCP/LSP refresh the launcher calls before `exec` |
| `internal/cli/config_ref.txt` | the `agent_updates` entry (this file is what `yolo config-ref` prints, not `configref.go`) |
| `packs/{claude,agy,copilot,codex,opencode,pi}/pack.json` | the `update` verb, per §3.5's per-agent table |
| `AGENTS.md` | the PATH-order bullet and the "two generated script dirs" bullet |

## Reuse

- `internal/config/hostapplyonlaunch.go` — the whole user-scope-only pattern: `UserScopeConfigOrEmpty()`,
  the split value-reader so validator and tests share one reading, and the validator's
  `"user-scope only"` error string that three test files already grep for.
- `execLauncherUpdate` (`internal/cli/packupdate.go:108`) and `refreshNpmPrograms` (129) already
  invoke launchers **by absolute path** with `YOLO_PACK_UPDATE=1` and dispatch off `HonoredInstalls`.
  Rename; do not rewrite.
- `lookPathIn` (`internal/entrypoint/requires.go:67`) resolves against an **explicit** PATH — the
  check's probe, already written. Constraint: at generation time the process PATH is still the
  container default (that function's own comment says so), so pass the dir list in.
  `loadInjectedTools` (`internal/entrypoint/mise.go:13`) is the declared mise tool set it consults.
- `catalog.go:338 readLSPSentinel`, `splitLSPInstallList` (`catalog.go:107`), `mcpPresetNpmPackages`
  (`shell.go:213`) — the yolo-**installed** server set is already enumerated. The refresh reads it
  from these, never from the MCP server table.
- `shquote.Quote`/`Join` under the splice contract documented at `shims.go:596-620`; `stampMtimeFn`
  (390) for BSD/GNU `stat`; `receiptsFile` (447) as the precedent for **baking** a value into the
  script rather than reading it from the environment at run time.

## Traps

1. **Scope the collision check to `/bin`, `/usr/bin` and the declared mise tools — never the
   install prefixes.** A check spelled "is this name already resolvable" destroys the feature:
   after the first successful install `~/.local/bin/claude` exists, the next boot writes no
   launcher, PATH resolves the installed binary, and evergreen works exactly once. Symptom: all
   tests green, agents frozen again in a week. This is the single highest-risk item in the plan.
2. **The check converts a structural impossibility into a handled case** — the design names that as
   the honest cost. AGENTS.md's *"a test that pins the CALLEE while the CALL SITE is unpinned is not
   a test"* applies hard: the test must fail when the **check** is deleted, not merely show the check
   works. See Ships with for the shape.
3. **Two PATH strings, and only their ends are pinned.** `BootPath` and the `.bashrc` export already
   disagree about `$HOME/.local/bin` (5th vs 2nd) and `TestBashrcPathMatchesBootPathOrder`
   (`launcherdir_test.go:171`) asserts only "block first, launch last" — so moving one and not the
   other passes today. Both change; that test becomes a real order comparison.
4. **Re-entrancy is new.** With the launch dir ahead of the install prefixes, any bare-name call of
   the program from inside the update — a vendor installer that runs `claude`, an npm postinstall
   that runs `copilot` — now resolves back to the launcher. Absolute `$REAL_BIN` covers the
   launcher's own calls, not the vendor's. Bake a per-bin guard var so a re-entered launcher execs
   `$REAL_BIN` with no update logic. Symptom: a fork bomb on the first invocation after the reorder.
5. **`pnpm` is generated unconditionally** (hardcoded list, `shims.go:350`), not gated on mise. Ahead
   of the mise shims its launcher shadows a `mise_tools`-declared pnpm — a project dependency losing
   to an agent-class mechanism, which P6 forbids. And `GenerateAgentLaunchers` (`boot.go:439`) runs
   before `ConfigureMisePrism` (491), so on a cold boot the shim dir is empty: consult the declared
   tools, not the directory.
6. **No `flock` in the image** (verified; macOS ships none either). The install-prefix lock is an
   atomic `mkdir`, which is what the design's "cannot take the lock ⇒ proceed without updating and
   say so" wants anyway — non-blocking, never fatal.
7. **`MarshalPacks` has no production caller** — packs reach the jail through the staged **mount**.
   Do not copy it as the wire precedent; `YOLO_MCP_PRESETS` at `assemble.go:773` is the one that works.
8. **Three env-wiring sites, not one.** Miss `check/entrypoint.go:45` and `yolo check` renders
   launchers under a policy boot does not have; miss `macosuser/runplan.go:176` and macos-user keeps
   the old behavior — the one backend with no image to hide it (`shims.go:390` records the last time
   that bit).
9. **The default direction inverts the precedent.** `host_apply_on_launch` fails closed (unreadable
   config ⇒ false). `agent_updates` absent or unreadable means **true**. A copy-paste freezes every
   agent silently.
10. `_do_install`'s return status is load-bearing for update mode alone (`shims.go:676-694`). The
    native branch needs the same split or `yolo pack update` reports success for a no-op again.

## Build order

1. **Update verb.** `packdecl` field + projection + `validateContribution`'s `KindProgram` arm, then
   the six `pack.json`s per §3.5's table. → `go test ./internal/packdecl`
2. **Native template update branch**, plus the re-entry guard and the prefix lock; delete the
   `"$REAL_BIN" install` no-op and `_poll_and_report`. → `go test ./internal/entrypoint`
3. **`yolo pack update`:** drop the npm-only skip. It now walks the same set the launchers do.
   → `go test ./internal/cli`
4. **The collision check, BEFORE the reorder.** Landing it first makes step 5 a one-line change to an
   already-guarded system, and its test is meaningful under today's PATH. → `go test ./internal/entrypoint`
5. **The PATH reorder**, both strings, with the order tests rewritten. → `just test-fast`, then
   `go test -count=1 -timeout 0 -run 'Launcher|GeneratedDirs' ./integration`
6. **`agent_updates`:** key, validator, user-scope loader, three wiring sites, baked into each
   launcher. → `go test ./internal/config ./internal/cli/...`
7. **MCP/LSP transitive refresh**, completing before the `exec`. → `go test ./internal/entrypoint`
8. **Nested-jail verification** (mandatory, `cmd/`+`internal/`): `git add` the new files first, then
   `just build-go` and AGENTS.md's by-path launch from `/tmp/yolo-nested`. Not bare `yolo`.

## Ships with

- **The load-bearing unit test:** drive `GenerateAgentLaunchers` with a pack declaring `program <n>`
  where `<n>` is provided by a fake image dir on the probe list, and assert **no file** at
  `LaunchDir()/<n>`. Deleting the check makes the file appear → red. Same shape for the mise half.
- **The call-site half:** `integration/launcherdir_test.go:129 TestPackProgramDoesNotShadowABakedBinary`
  passes today by *position*. After step 5 it can only pass by the check, which is what makes it the
  integration test that would catch this breaking. Keep it; do not weaken it.
- **Rewrites, not repairs** (the ruling they pin is superseded for the agent class):
  `TestBootPathOrdersBlockersFirstAndInstallersLast` (`launcherdir_test.go:26`),
  `TestBashrcPathMatchesBootPathOrder` (:171), `integration/launcherdir_test.go:25`, and in
  `npmlauncher_test.go`: `TestUnpinnedNpmLauncherPollsButNeverReinstalls` (:408),
  `TestUnpinnedNpmLauncherTimeline` (:451), `TestUnpinnedNpmLauncherDoesNotReinstallWhenCurrent`
  (:711), `TestNpmLauncherUpdateModeIsTheOnlyResolver` (:501).
- **Unit cases the design already enumerates:** offline + installed ⇒ exec anyway; offline + absent ⇒
  fail naming the network; fresh stamp ⇒ no work; the 60s timeout ⇒ proceed with what is installed;
  lock held ⇒ proceed without updating **and say so**; `agent_updates: false` ⇒ the emitted launcher
  carries no update branch at all; an `npx -y pkg@latest` MCP entry ⇒ untouched.
- **Config surface:** `agent_updates` in `internal/cli/config_ref.txt` and `knownTopLevelConfigKeys`;
  the workspace-scope error must say `user-scope only` and name `paths.UserConfigPath()`.
- **Docs describing the old behavior**, by path: AGENTS.md's PATH-order bullet and its "two generated
  script dirs" bullet (the *"ordered LAST … unrepresentable rather than handled"* sentence is exactly
  what B2 deletes); the same claim duplicated in `shell.go:139-144` and `boot.go:343-355`;
  `docs/design/trust-paths.md` §3 rows 2 and 3 and OQ-TP5, which say evergreen happens *"on the boot
  path at every launch"* — the superseded eager shape, wrong under B2; the `Install.Package` doc
  comment in `packdecl.go:124` (*"re-checked hourly by the launcher"*).
- **Norms:** `just format` then `just check-ci` per commit; other agents are live in this tree, so
  stage by path (`git add -N <path> && git commit -- <path>`).
- **Cheap and yours:** the guard-var spelling, the lock-dir name, and whether the refresh step is a
  `yolo-jaild` subcommand or a hidden `yolo internal` one — the design fixes only its ordering.

## Don't

- Don't flip any pack from `via: npm` to `via: installer` (OQ-PD13) — another agent owns it, and the
  npm launcher's `_update` already resolves, so the four npm packs go evergreen without the flip.
- Don't build install capture (§6.3) — another agent; see Blockers for how it constrains landing.
- Don't reintroduce the eager-at-boot ordering constraints (after catalog/reconcile, after
  `GenerateCABundle`, a boot genStep, a jail-level fatal and its escape hatch). §3.5's table lists all
  of them as **deleted**: there is no boot step anywhere in this design.
- Don't make `update` a closed enum. It is a vendor's argv, not a mechanism name, so §6.2's `via`
  tolerance does not apply and `unknownViaSkip` gains no twin. An absent verb falls back per `via`.
- Don't `RemoveAll` either generated dir — both share one bind-mount anchor (`resetAnchorDir`,
  `shims.go:26`).
- Don't refresh a server yolo did not install; the sentinel and the preset list are the scope.

## Blockers

- **OQ-PD15 sequences this AFTER capture**, because evergreen multiplies the cost capture removes —
  measured: one workspace holding four claude versions at **1019 MB**, one-time today and recurring
  under evergreen. Capture is another agent's. **Stop and ask** whether this lands behind it or ahead
  of it; ahead means shipping the recurring-disk regression the ruling exists to avoid.
- **Stop and ask** before widening the collision check past `/bin`, `/usr/bin` and the declared mise
  tools (trap 1). The wrong scope is silent and turns the whole feature off.
