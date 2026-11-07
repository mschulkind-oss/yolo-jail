---
title: "Per-session profiles: implementation plan"
status: accepted
stage: BUILT
next: "None here; the design's Mac checks (§13) and per-agent unlocks (§7) carry what remains"
depends-on: [per-session-profiles.md]
tags: [plan, profiles, attach, sessions, pi]
---

# Per-session profiles: implementation plan

**Status:** 2026-10-19. Tasks 1-7 implemented against the tree. The host boundary and capture
work were grounded against `7941904b6` after checking the settled design and current host launch,
capture and reap call sites. 2026-11-06: the design's step 4 is built. macos-user's id landed in
`29e298ca5`, and omp's declaration and the session-home bindings landed in `839c23f6f`
([SP-D36](per-session-profiles.md#SP-D36), [SP-D37](per-session-profiles.md#SP-D37)). No task
remains in this plan.
**Design:** [`per-session-profiles.md`](per-session-profiles.md) (all filed rulings settled;
host boundary and capture are specified).

Precedence: the design wins on behavior, the tree wins on fact (cosmetic drift only: follow the
tree and say so in the report), this file is advice and the first thing to be wrong. **Stop and
report, do not commit** when: a test does not fail the way the task predicts; a file the task names
is dirty with someone else's work; a symbol named here is gone; the fix needs a file outside the
task's fence; a gate fails outside the task's scope.

## Terms (from the design, do not re-open)

- **Session id**: `YOLO_SESSION_ID` (`entrypoint.SessionIDEnv`), minted per exec by the host, 32
  lowercase hex characters, inherited by every descendant.
- **Entry snapshot**: one immutable directory per session id holding that entry's env files: the
  shared `yolo-user-env.sh`, `<agent>.sh`, `<agent>.<file>` and `provider-keys/<agent>.sh`.
- **Session home**: a per-session copy of the one directory a pack's program reads its config from:
  that session's render of the pack's surfaces in the directory, plus symbolic links to everything
  else in the shared directory.
- **Session-home declaration**: the pack's statement of the directory, the relocation variable and
  the shared names. Core reads it generically; core never names pi.
- **Stray** (coined in the design): an entry in a session home that is neither a link yolo made nor
  a file yolo rendered.

## Measurements behind the shared list (2026-10-18, offline, no model calls)

Read from stock pi 1.0.1 (`@earendil-works/pi-coding-agent/dist`) and pi-subagents' source:

| Name | Writer | By rename? | Consequence |
| :--- | :--- | :--- | :--- |
| `auth.json` | `core/auth-storage.js` `withLock`: `writeFileSync` in place under `proper-lockfile` | no | link works; `ensureFileExists` uses `existsSync`, so a dangling link is written through and creates the shared file |
| `models-store.json` | `core/models-store.js`, the same `FileAuthStorageBackend` | no | link works |
| `mcp-auth.json` | `extensions/mcp/oauth.js`, the same backend | no | link works |
| `settings.json` | `core/settings-manager.js` in place under a lock | no | rendered per session |
| `mcp.json` | `extensions/mcp/config.js` `editMcpServers`, in place | no | rendered per session |
| `trust.json` | `core/trust-manager.js`, in place | no | link (shared name) |
| `sessions/` | `core/session-manager.js` appends inside | inside only | link the directory |
| `bin/`, `tools/` | `utils/tools-manager.js` renames inside `bin/` | inside only | link the directory |
| `npm/`, `git/` | package manager, inside | inside only | link the directory |
| `run-history.jsonl` | pi-subagents `run-history.ts`, `writeFileSync` in place | no | link (shared name) |
| `agents/` | pi-subagents reads `<agentDir>/agents` and **no longer** `~/.agents` once `PI_CODING_AGENT_DIR` is set (`agents.ts`, `userDir = process.env.PI_CODING_AGENT_DIR ? userDirOld : …`) | — | link `agents` to `~/.agents` when it exists, else to the shared `agents/` |
| legacy migrations | `migrations.js` renames `oauth.json`, `commands/`, top-level `*.jsonl`, `tools/{fd,rg}` | yes, legacy names only | each acts on a link that points into the shared dir; harmless |

**Lock files are per path.** `proper-lockfile` with `realpath: false` locks `<path>.lock` by
`mkdir`, so a session home's `auth.json.lock` is its own; a linked lock would read as held forever
(`mkdir` on a dangling link is `EEXIST`, `stat` is `ENOENT`, and it retries as locked). So two
sessions in one jail no longer serialize `auth.json` writes through pi's lock, exactly as two jails
on one machine do not today. Ledgered as SP-D10; never link a `*.lock` name.

## Map

| Path | Change | Task |
| :--- | :--- | :--- |
| `internal/cli/run/agentenvfiles.go` | `writeSessionSnapshot` beside `deliverChannel`; `writeAgentEnvFiles` keeps the `sessions` subdir and takes the in-jail dir for agent-file lines | 1 |
| `internal/cli/run/sessionsnapshot.go` (new) | snapshot dir layout per backend, atomic publish, the SP-D8 older-jail line | 1 |
| `internal/cli/run/run.go` | fresh launch: mint the first session id before `deliverChannel` (~`:1887`) and reuse it at `:2320`; attach: mint before `deliverChannelOnAttach` (~`:3070`), reuse at `:3113`; call the SP-D8 note beside `noteBootedPackSetDiffers` (~`:3018`) | 1 |
| `internal/cli/run/contracttags.go`, `assemble_test.go:537`, `contracttags_test.go` | new tag `session-homes` | 1 |
| `internal/entrypoint/agentenv.go` | `SessionSnapshotsSubdir`, `SessionSnapshotDirRel(id)`; `agentEnvShellFn` sources the snapshot's files first | 1 |
| `internal/entrypoint/boot.go` | one `userEnvFilePath(e)` for `hydrateEnvFromUserEnvFile` (`:205`), `ParseEntryChannel` (`:142`), `activationPrefix` (`:675`) | 1 |
| `internal/entrypoint/callerauth.go:153` | same helper | 1 |
| `internal/entrypoint/shell.go:248` | `.bashrc` sources the snapshot's shared file when present | 1 |
| `internal/packdecl/sessionhome.go` (new), `packdecl.go` | top-level `session_homes` field, validation wired into `Manifest.Validate` (~`:881`) | 2 |
| `internal/render/surface.go`, `target.go` | a redirect (absolute dir → absolute dir) applied by `ExpandHome`; a session sidecar dir | 2 |
| `internal/entrypoint/sessionhome.go` (new) | build, link, export, SP-D5 note, SP-D6 refusal | 2 |
| `internal/entrypoint/bootsteps.go` | step `prepare_session_homes` before `configure_pack_surfaces` (`:439`), session pass only, `notDarwin` | 2 |
| `internal/entrypoint/prism.go`, `surfaceloop.go`, `packsurfaces.go`, `env.go` | redirect on a session pass; per-session baseline sidecars, shared overlay; render lock | 2 |
| `internal/entrypoint/prelaunchrefresh.go` (~`:160`, `:194`, `:247`) | a due-on-change / only-if file under a declared directory resolves under the relocation variable when it is set | 2 |
| `packs/pi/pack.json` | the declaration | 2 |
| `packs/pi/home/.pi/agent/extensions/yolo-model-lists.js` (`:36`), `yolo-openai-auth.js` (`:110`) | resolve under `PI_CODING_AGENT_DIR` | 3 |
| `internal/entrypoint/pi_model_lists_extension_test.go`, `pi_openai_auth_extension_test.go` | cases for the variable | 3 |
| `internal/entrypoint/sessionhome.go`, `prism.go` | capture every session home into the shared overlay; pid1 captures then deletes leftovers; strays named | 4 |
| `internal/cli/host.go`, `internal/cli/hostsessionhome.go` (new), `internal/prune/` | `yolo host` per-launch session home | 5 |
| `integration/sessionhome_test.go` (new) | attach-path integration | 6 |
| `docs/reference/pack-system.md`, `docs/reference/composed-file-permissions.md`, `internal/cli/config_ref.txt`, `CHANGELOG.md`, the design's ledger | docs | 6 |
| `internal/cli/host.go`, `internal/cli/hostsessionhome.go`, `internal/cli/configdiff.go`, `internal/prune/hostsessionhomes.go`, `internal/prune/prunecmd.go` | host command ownership gate and host managed-config capture for resident and reaped owned session homes | 7 |
| `internal/cli/hostsessionhome_test.go`, `internal/prune/hostsessionhomes_test.go` | boundary, capture semantics and cleanup regressions | 7 |
| `internal/cli/frontdoornotch_test.go`, `internal/cli/hostwrapperspath_test.go` | `--at host` execution spellings and generated-wrapper execution share the same ownership gate | 7 |

## Reuse before you write

- `deliverChannel` / `writeAgentEnvFiles` / `writeUserEnvFile` (`internal/cli/run`): the snapshot is
  the same two writers aimed at another directory. Never compose a second time: one `packChannel`.
- `openStateRoot`, `writeBeneath`, `mkdirAllBeneath`, `readDirBeneath` (`wsstatebeneath.go`): every
  host write under `wsState` goes through an `os.Root`.
- `stopRemedy(rt, cname)` and the `o.pr(o.Stderr).printf("[yellow]Notice: …")` shape in
  `noteBootedPackSetDiffers` (`packtree.go:509`): the SP-D8 line.
- `jailContractTags(envLines)` (`contracttags.go:125`): the tag read.
- `genStep` / `genStepAbout` (`boot.go:975`): a failed step is the A12 refusal ("refusing to start
  the jail: N boot step(s) failed"). SP-D6 is a failed step, nothing more.
- `render.Target.ExpandHome` (`internal/render/surface.go:200`) is the one place every surface path
  is expanded; `Target.SidecarDir` (`target.go:383`) the one sidecar root.
- `linkIntoSharedDir` (`packhooks.go:271`): shape of a home-relative link with refusal on a
  non-link occupant.
- Extension idiom: `subagentConfigPath(env)` in `packs/pi/home/.pi/agent/extensions/yolo-durable-worktrees.js:27-40`
  (handles `~`, `~/`, absent). Copy it; do not import across extensions.
- Extension tests run the JS under node via `requireNode` (`internal/entrypoint/requirenode_test.go:16`).
- Host: `entrypoint.RenderHostPack(p, homeDir, ownership, observe, overlays, in, only...)`
  (`hostrender.go:354`) renders into any `homeDir`; `HostInputs{Vars: c.wireTables()}` is the
  launch's `-p` selection (`hostmodelmenu.go:293`). The model-menu store's `Held.KeepAcrossExec`
  (`internal/modelmenu/shared.go:70`) is the lock that survives `syscall.Exec`; `sweepLeasedDirs`
  (`internal/packload/embeddedcache.go:708`) the reap shape.

- `hostExec` (`internal/cli/host.go`) is the shared command-launch doorway for `yolo host --`,
  `--at host`, and generated wrappers. Its earliest side-effect-free gate is after flag/command
  validation and before `hostProfileFor`/`startHostLaunchTrace`; the current `hostLaunch` starts
  pack refresh immediately, so gating only near session-home preparation is too late. Keep the
  separate host verbs on `hostMain`'s verb dispatch.
- `prepareHostSessionHomes` currently reaps at entry and its reaper removes unleased homes before
  the current launch proceeds. `hostSessionHomes.Close` removes resident homes; `KeepAcrossExec`
  leaves exec'd homes lease-protected. Both reaping callers are direct: this launch path and
  `prune.Run` (`internal/prune/prunecmd.go`). Capture must run only after a lease is proven free,
  and before `RemoveAll`; dry-run prune must not call it.
- `ownedHostRender.capture` (`internal/cli/configdiff.go`) is the normal host capture route: it
  observes one host render and persists only its `Capture` overlay/list records via `configTarget`.
  Construct its render against the ended scratch home while retaining the real owned host target
  for standard sidecar destinations; the scratch home was seeded with the launch's baselines and
  stores its own capture sidecars. Do not call jail `captureSurfaceAt` or copy session files back.
- In the host launcher, `prepareOpenAIAuthHost`, `hostWorkspaceSkills`, session-home preparation
  and service startup are downstream effects; the ownership refusal must precede all of them.
  Existing tests build host homes with temporary `HOME` (`hostGateHome`) and stub the exec
  boundary (`hostSelectionRun` / `hostGateRunIn`); extend these production-callsite fixtures.

## House style that is not obvious

- Messages: headline, blank line, facts, then the commands to copy. Every stop names the next step
  (`docs/reference/happy-path-principle.md`).
- Comments in this repo explain *why* at length; a new function gets a doc comment saying what it
  is for and which ruling it serves (cite `SP-Dn` / `OQ-SPn`).
- Tests must fail if the production call site is deleted (AGENTS.md). Drive `ConfigurePackSurfaces`
  / `runBootSteps` / `deliverChannel` / `hostMain`, not only the new helper.
- `just format` before each commit; stage by path (`git add -- <paths>`); never `--no-verify`.

## Traps

- **Prism sidecars are keyed by agent and surface name only** (`render/target.go:506`), not by home.
  A session render that keeps the default sidecar dir overwrites the shared copy's `.last_render`,
  and the next pid1 boot captures a phantom diff. Constraint: baseline sidecars (last render,
  selection record, provenance, insert record) go to a per-session dir; the overlay and list
  capture stay shared, which is what makes capture (task 4) work.
- **`writeAgentEnvFiles` deletes every entry it did not write** in its directory (`agentenvfiles.go`
  end of function). Without a keep for `sessions`, the next entry deletes every snapshot.
- **`agentFileLines` hard-codes `entrypoint.AgentEnvDirRel`** for the in-jail path of an agent file;
  a snapshot's env file would point at the shared copy. Pass the dir.
- **The attach's id is minted after delivery today** (`run.go:3113` vs `:3070`); the fresh launch's
  at `:2320`, long after `deliverChannel` at `:1887`. Mint once, earlier, and reuse; two ids for one
  session is the bug.
- **An attach to an older jail runs that jail's own mounted entrypoint**, so the entrypoint never
  needs an "older host" branch; only the host needs the tag check.
- **`/home/agent/.config/yolo-agent-env` is `:ro` on podman**: the jail can read snapshots but
  never write them, which is the point. Apple Container ignores `:ro`.
- **The session pass also runs on the first session** (`--yolo-first-session`), and pid1 (hold) is
  not a session pass. `bootRun.sessionPass` is the switch.
- **Rmw surfaces read the existing file as their base.** Pi's in-dir surfaces are stateful
  (`settings`, `mcp`) or computed (`models`, `codex-models`, `model-lists`), so none is rmw today;
  if a declared directory holds an rmw surface, seed it from the shared file's bytes before the
  render (SP-D11).
- **`DecodeTolerant` ignores an unknown top-level key** but skips a whole contribution for an unknown
  field inside it (`skew.go:221`): the declaration is top-level so an older yolo reading a newer
  pack tree still loads pi. `packs/releasedecode_test.go` pins this.
- **Integration tests reap the host store** unless `YOLO_NO_AUTO_IMAGE_REAP=1`.

## Tasks

### Task 1 — entry snapshots, environment half (independent)

- **Settled (do not re-open):** "The launcher composes the channel as today and writes it to the
  snapshot for this exec's session id, complete, before the exec. It still writes H1-H3." "The
  session pass reads its own snapshot's shared file instead of H1. No snapshot for the id: H1, as
  today." "An agent's launcher sources its env and provider-keys files from `$YOLO_SESSION_ID`'s
  snapshot when one exists, H2/H3 otherwise; `.bashrc` does the same for H1." Retained until the jail
  is known gone (SP-D7): `clearAgentEnvFiles` at teardown removes them with the rest. An older jail
  (no `session-homes` tag): "keeps today's last-writer behavior. An attach to one prints one line:
  a running pi session in it may now read this entry's profile, and `yolo stop` then a fresh launch
  removes the hazard" (SP-D8).
- **Layout (contract between host and jail):** in-jail `~/.config/yolo-agent-env/sessions/<id>/`
  holding `yolo-user-env.sh`, `<agent>.sh`, `<agent>.<name>`, `provider-keys/<agent>.sh`. Host:
  `<wsState>/agent-env/sessions/<id>` on podman, `<wsState>/.config/yolo-agent-env/sessions/<id>`
  on Apple Container. Publish by writing `sessions/.<id>.tmp` then renaming; a failed write
  **refuses the entry** (headline, the path and error, then `yolo check` / retry), because a session
  without its snapshot would read the shared files and bring the deadlock back.
- **Tests first:**
  - `internal/cli/run/sessionsnapshot_test.go`: `deliverChannel` with a session id writes both the
    shared files and `agent-env/sessions/<id>/{yolo-user-env.sh,pi.sh}` with equal bodies; a second
    entry with another id and another channel leaves the first snapshot byte-identical; the shared
    dir rewrite keeps `sessions/`; `clearAgentEnvFiles` removes it; an agent file line inside the
    snapshot names the snapshot path; Apple Container lands under `.config/yolo-agent-env/sessions`.
  - The SP-D8 line: an attach whose `envLines` carry `YOLO_CONTRACT_TAGS` without `session-homes`
    prints exactly one line naming `yolo stop`; with the tag, none.
  - `internal/entrypoint`: hydrate with `YOLO_SESSION_ID=<id>` and a snapshot present reads the
    snapshot (a key only it holds lands in `e.Vars`); absent snapshot reads the shared file;
    `agentEnvShellFn`'s text, run under bash, sources the snapshot's `pi.sh` first and falls back;
    the generated `.bashrc`, run under bash with a fake home, sources the snapshot's file.
  - Red: `go test ./internal/cli/run ./internal/entrypoint -run 'Snapshot|SessionHomesTag'`.
- **Gates:** `go test -short ./internal/cli/run ./internal/entrypoint`; `just lint-ci` if quick.
- **Fences:** do not touch `bootsteps.go`, `prism.go`, `surfaceloop.go`, `packsurfaces.go`,
  `internal/packdecl`, `internal/render`, `packs/`. Another agent works on the attach path's
  approval/gate code (`docs/design/live-config-apply.md`): keep changes in the env/snapshot code.
- **Commit:** `feat(run): write a per-session entry snapshot and read it in the jail`.
- **Report:** red and green runs; the exact in-jail path constants; anything the tree forced.

### Task 2 — session-home declaration, render-and-link pass, pi's declaration (independent)

- **Settled (do not re-open):** "For every selected pack with a session-home declaration, the pass
  creates the session home, renders the pack's surfaces whose paths lie in the declared directory
  into it, and links the rest. Surfaces outside every declared directory keep today's every-pass
  render." "The pass exports the declared variable, pointing at the session home, into the
  session's environment before it execs the shell." Linked: every entry present in the shared
  directory when the home is created, plus each declared shared name even when absent (a directory
  is created in the shared dir first; a file is linked dangling). Never a copy. SP-D2: a session
  pass renders in-directory surfaces only into its home; the shared copies are pid1's alone. SP-D5:
  a session whose environment already sets the variable gets no home for that pack, and one line
  says so. SP-D6: a home that cannot be made refuses the session (a failed boot step), no fallback.
  Location (SP-D7): `<parent(dir)>/.yolo-sessions/<id>/<base(dir)>`, for pi
  `~/.pi/.yolo-sessions/<id>/agent`. Only a session pass with a valid `YOLO_SESSION_ID` makes one.
- **Schema (contract, top-level, generic):**

  ```jsonc
  "session_homes": [
    {
      "dir": ".pi/agent",            // home-relative, under one of this pack's state dirs
      "env": "PI_CODING_AGENT_DIR",  // ValidEnvName
      "shared": ["auth.json", "models-store.json", "mcp-auth.json", "trust.json",
                 "run-history.jsonl", "sessions/", "bin/", "tools/", "npm/", "git/"],
      "prefer": {"agents/": "~/.agents"} // link to this home path when it exists, else to the shared name
    }
  ]
  ```

  A trailing `/` marks a directory. Validation refuses: an absolute or `..` path, a `dir` not under
  a `state` contribution's `at`, a name with `/` inside it, a `*.lock` name, an invalid env name, a
  `prefer` value outside `~/`. Problems in the house `[]string` style
  (`session_homes[0].dir: …`).
- **Tests first:**
  - `internal/packdecl/sessionhome_test.go`: each refusal above; the shipped pi manifest decodes
    strictly and declares one home.
  - `internal/entrypoint/sessionhome_test.go`, driving `runBootSteps` with `sessionPass: true` (or
    `ConfigurePackSurfaces` through the session entry point) over the pi pack with a fixture home:
    1. the home holds regular files `settings.json`, `models.json`, `yolo-model-lists.json`,
       `yolo-openai-codex-models.json`, `mcp.json` and links for `auth.json`, `sessions`, a
       pre-existing `extensions/`, and a dangling `models-store.json` whose first write creates the
       shared file;
    2. **the deadlock's unit form**: session A (profile A wire tables) then session B (profile B)
       leaves A's home byte-identical **and** the shared `~/.pi/agent/settings.json`
       byte-identical (fails if the session pass writes the shared file);
    3. `PI_CODING_AGENT_DIR` is in `e.Vars` and the process env, pointing at the home;
    4. SP-D5: a preset variable → no home, one line on stderr naming the variable;
    5. SP-D6: an unwritable `~/.pi/.yolo-sessions` → the step fails and the boot refuses, the error
       names the path;
    6. `agents` links to `~/.agents` when present, else to the shared `agents/`;
    7. no `YOLO_SESSION_ID` (pid1 or raw exec) → no home, shared render as today.
  - Prelaunch refresh: the generated launcher's due-on-change check reads
    `$PI_CODING_AGENT_DIR/settings.json` when the variable is set (run the generated bash).
  - Red: `go test ./internal/packdecl ./internal/entrypoint -run 'SessionHome'`.
- **Change notes:** advice — add `Redirects` to `render.Target` (applied in `ExpandHome` after `~`
  expansion: a path equal to or under `From` maps under `To`), and a session sidecar dir used by
  `prismLastRenderPath`, the selection record, provenance and the insert record, while
  `prismOverlayPath` and `prismListCapturePath` stay on the shared `SidecarDir` (trap above). The
  session sidecar dir: `<SidecarDir>/sessions/<id>/`. Hold an `flock` on
  `<SidecarDir>/.session-render.lock` across a session pass's in-directory renders (task 4 extends
  it over capture). Cheap and yours: function factoring, step label (`prepare_session_homes`).
- **Gates:** `go test -short ./internal/packdecl ./internal/render ./internal/entrypoint ./packs/...`;
  `TestRenderFingerprintStable` and `TestShippedPacksDecodeUnderTheLastRelease` stay green.
- **Fences:** do not touch `boot.go`, `agentenv.go`, `shell.go`, `callerauth.go`, `internal/cli/`,
  `packs/pi/extensions/`.
- **Commit:** `feat(entrypoint): render a pack's declared directory into a per-session home`.

### Task 3 — yolo's pi extensions read under `PI_CODING_AGENT_DIR` (independent; SP-D9)

- **Settled:** "`yolo-model-lists.js` and `yolo-openai-auth.js` resolve their list files under
  `PI_CODING_AGENT_DIR`, as `yolo-provider-policy.js` and `yolo-durable-worktrees.js` already do."
  The explicit `YOLO_PI_MODEL_LISTS` / `YOLO_PI_OPENAI_CODEX_MODELS` variables still win (the host
  vehicle sets them).
- **Tests first:** in the two extension tests, a case with `PI_CODING_AGENT_DIR=<tmp>` and the list
  file only there: the extension enforces it; `~` and `~/x` forms; unset falls back to
  `~/.pi/agent`. Red: `go test ./internal/entrypoint -run 'ModelLists|OpenAIAuth'`.
- **Fences:** only `packs/pi/home/.pi/agent/extensions/yolo-model-lists.js`, `yolo-openai-auth.js` and their tests.
- **Commit:** `fix(pi): resolve yolo's model-list files under PI_CODING_AGENT_DIR`.

### Task 4 — capture from session homes (after 2)

Ruled by [OQ-SP2](per-session-profiles.md#11-decision-ledger).

- **Settled:** "Every render pass captures each session home's rendered files into the shared
  overlay, by the existing capture rule for that surface, before it renders. The pass holds an
  in-jail render lock across capture and render." "A file that does not parse (pi mid-write) is
  skipped this pass, never treated as a deletion." "Two sessions that changed the same key: the one
  captured last wins." "A running session keeps its own copy." "The fresh launch's boot captures
  every session home left by the previous container, then deletes them." Strays are kept and named
  in one warning line per pass ("the pack's declaration should list it").
- **Shape (advice):** capture = `composeStatefulSurface` against the session's file and that
  session's baseline, persisting only the shared overlay/list capture and that session's baseline
  (set to the file's current bytes so an edit is captured once). Computed surfaces have no capture.
- **Tests first:** session A's home `settings.json` gains `"theme":"x"` and a package; session B's
  pass renders B's home with both; A's file is unchanged by B's pass; an unparseable A file is
  skipped and B still renders; pid1 boot over leftover homes captures them, deletes
  `.yolo-sessions/`, renders the shared copy with the captured keys; a stray regular file in A's
  home is named once.
- **Commit:** `feat(entrypoint): capture settings saved in a session home into the shared layer`.

### Task 5 — `yolo host` per-launch session home (after 2)

Ruled by [OQ-SP4](per-session-profiles.md#11-decision-ledger).

- **Settled:** "`yolo host -p` gives pi a per-launch session home: links to the user's real
  `~/.pi/agent` shared state, profile-dependent files rendered in the throwaway dir,
  `PI_CODING_AGENT_DIR` exported to that launch only; the user's real files untouched; removed when
  the launch ends, or reaped later if it dies." Same declaration; no pi knowledge in core. SP-D5
  holds at the host. These session-home cases run under `host_management: own`; SP-R1's separate
  `none` refusal and the old test that expected a launch under `none` are handled by Task 7.
- **Shape (advice):** in `hostLaunch` after composition (`host.go:~803`) and before the exec arms:
  for each selected pack with `session_homes`, make `os.MkdirTemp(<yolo state>/host-session-homes,
  "<pack>-")`, render the pack's in-directory surfaces with `RenderHostPack` into a temp home whose
  declared dir is the session home, inputs `HostInputs{Vars: wireTables}` so the `-p` governs,
  link every other entry and the shared names into the real dir, and export the variable into the
  child env only. Keep a lease fd across `syscall.Exec` (`Held.KeepAcrossExec` pattern); the next
  host launch and `yolo prune` reap dirs whose lease is free. Reuse task 2's link builder (keep it
  in a package both sides import, e.g. `internal/sessionhome`).
- **Tests first** (`internal/cli/hostsessionhome_test.go`, through `hostMain` with
  `hostSyscallExec` stubbed, `hostSelectionRun` / `piSettingsFixture`): the configured user scope
  explicitly sets `host_management: own`; `-p` → the child env carries `PI_CODING_AGENT_DIR` under
  the store, its `settings.json` holds the `-p`'s `defaultProvider`, `auth.json` is a link to the
  real one, the real `~/.pi/agent/*` bytes are unchanged; a preset variable wins; a dead launch's
  dir is reaped by the next launch.
- **Commit:** `feat(host): give each pi launch its own session home`.

### Task 6 — integration and docs (after 1-4)

- `integration/sessionhome_test.go`, modeled on `TestAttachDeliversTheSelectedProfile`
  (`integration/attachprofile_test.go:30`): a first session held open; attach with `-p A`, attach
  with `-p B`, and a bare attach running `bash -lc …` (no pi). Each prints
  `$PI_CODING_AGENT_DIR` and `jq .defaultProvider "$PI_CODING_AGENT_DIR/settings.json"`; assert
  three distinct homes, A's and B's providers, the bare attach's configured one, and A's file
  unchanged after B. No agent CLI runs.
- Docs: correct the two stale claims (`docs/reference/pack-system.md#open-rulings-3`, "a running
  jail is never re-rendered"; `docs/reference/composed-file-permissions.md` ~`:391`, "generators …
  read only that frozen environment"); document `session_homes` in `pack-system.md` beside
  `launch_computed`; `internal/cli/config_ref.txt` only if it lists top-level manifest keys; one
  `### Fixed` line in `CHANGELOG.md`; the design's ledger Built column and status.

### Task 7 — require ownership and capture edits from owned host session homes (after 5)

Ruled by [SP-R1](per-session-profiles.md#SP-R1) and [OQ-SP5](per-session-profiles.md#decision-ledger).

- **Settled (do not re-open):** a host command execution requires `host_management: own`; `none`
  or unset refuses before host launch assistance. Under `own`, user edits to a session home's
  stateful config surface go through ordinary host managed-config capture into the standard
  capture overlay, so later managed renders receive them. Generated/computed/managed values keep
  the existing surface and layer precedence; do not capture them as user edits. Do not copy the
  generated file wholesale or write it back to the native host settings file. No new schema or
  capture layer. `yolo host apply`, `yolo host env`, `yolo host wrappers status` and direct,
  non-yolo agent starts remain outside this command-launch boundary.
- **Change:** gate actual host command execution before pack refresh/preparation. Capture a
  resident `own` session's edits after its child ends and before its cleanup removes the home.
  For exec'd homes, add a pre-removal capture hook to `ReapHostSessionHomes`, supplied by both
  the next `yolo host` launch and `yolo prune --apply`. An active lease is never captured or
  reaped, and prune's dry run stays read-only. Extract or reuse the ordinary host capture path in
  `internal/cli/configdiff.go`: render/observe the ended home and its own sidecars as the source,
  but write through the standard capture overlay and list-capture sidecars under the existing
  owned-host lock. Respect existing surface/layer and refusal semantics. If capture cannot accept
  a home's changes, do not delete that source or report it captured; keep it retryable and use
  the normal refusal with its repair/retry command. Keep the user's real
  `~/.pi/agent/settings.json` unchanged by the session.
- **Tests first:** in `internal/cli/hostsessionhome_test.go`, rewrite
  `TestHostPiBareLaunchUnderNoneGetsNoSessionHome` to cover explicit `none` and unset: neither a
  bare launch nor `-p`/`--with-credentials` starts a command, composes an environment, creates a
  session home, or starts a service; `own` still launches. Use a direct harmless command and the
  exec stub, never an agent. Add a session-edit case: change a user key in the generated session
  `settings.json`, and cover both resident exit and an ended exec-home cleanup; verify the standard
  host capture overlay records that user edit but not profile-derived provider/model values, then
  launch under a different `-p` and verify the user's edit survives while the new profile's
  generated values win. The real settings file remains byte-identical. In
  `internal/cli/frontdoornotch_test.go`, exercise the none/unset refusal through every `--at host`
  command-launch spelling as well as `yolo host --`; verify a generated host wrapper still enters
  that same gated path. Keep `yolo host apply`, `yolo host env`, and `yolo host wrappers status`
  on their existing separate paths. Extend the `yolo prune --apply` case to prove capture precedes
  removal and normal capture failure retains the source. Keep existing session-home scenarios
  explicitly under `own`. Red:
  `go test ./internal/cli ./internal/prune -run 'Host.*(SessionHome|None)|HostSessionHomes|EveryAtHostSpellingLaunchesAtTheHost'`.
- **Gates:** `go test -short ./internal/cli ./internal/prune`; the specific red command above; existing
  host capture, host wrapper and host session-home tests.
- **Fences:** do not change `host env`, host-apply's ownership behavior, host wrapper status semantics,
  pack schemas, or the jail's capture rules. No whole-file copy-back, native host file writer,
  pi-specific capture schema, or edits to computed/managed surfaces' precedence.
- **Commit:** `fix(host): capture owned session-home edits normally`.

## Ships with

- Unit tests per task above; the integration test in task 6 is the one that catches a regression
  of the deadlock end to end.
- Tests that will break and should be **rewritten, not repaired**: `assemble_test.go:537` (the
  contract-tag literal); any entrypoint test asserting that a session pass rewrites
  `~/.pi/agent/settings.json`.
- Docs listed in task 6.

## Don't

- Don't add a pi branch anywhere in Go; `PI_CODING_AGENT_DIR` appears only in the declaring packs (`packs/pi/`, `packs/omp/`), never in Go outside tests.
- Don't copy a shared entry into a session home (SP-D3); don't link a `*.lock` (SP-D10).
- Don't detect the attach's command (P5): every session gets every declared home.
- Don't reap a session home or snapshot while the jail runs (SP-D7).
- Don't touch `docs/plans/roadmap.md` beyond this effort's own line.
