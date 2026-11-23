---
title: "Plan: buildable pack programs"
status: accepted
stage: DECIDED
next: "Task 10 on a real Mac: fix the run-as-you capture bootstrap failure, then record the done steps under macos-user, Apple Container and yolo host"
tags: [plan, packs, programs, build, capture, overrides]
summary: "Build hand-off for the pack-buildable-programs design: via \"pack\" delivered through the fork route's build act and the capture store, programs.overrides in the user scope, the prebuilt download, and yolo pack build, in ten ordered slices with their tests."
---

# Plan: buildable pack programs

**Status:** 2026-10-23, audited at `64a6e2144`. Written against `c0a14f4e4`. The tasks carry the
three rulings of 2026-10-23 ([OQ-BG1 to OQ-BG3](pack-buildable-programs.md#decision-ledger)).
Tasks 1 to 9 are built. The design's ledger names the code and tests for each decision. Task 10's
Linux half is built: the darwin act's placement and the Mac floor have unit tests, and
`integration/macosuserprograms_test.go` holds the Mac tests. Its native evidence is still owed. On
a real Mac on 2026-10-23, account mode passed all four tests and run-as-you mode failed two of them at the capture
bootstrap; `71df3f2af` fixed that, and run-as-you mode awaits a Mac re-run at main `71df3f2af` or later.
Apple Container has not run yet.

**Design:** [`pack-buildable-programs.md`](pack-buildable-programs.md).

**Precedence:** the design wins on behavior. The tree wins on fact, for cosmetic drift only:
follow it and say so in the report. This plan is advice and the first thing to be wrong. **Stop
and report, do not commit**, when:

- a test does not fail the way its task predicts;
- a named file is dirty with someone else's work;
- a named symbol is gone;
- the fix needs a file outside the task's fence;
- a gate fails outside the task's scope.

Never twist code to match the plan.

**House rules this change touches**, by their enforcing command:

- `just format` before each commit;
- `just check-ci` once, at landing, on the combined tree;
- the integration tests named per task, plus the full `go test -count=1 -timeout 0 ./integration`
  at landing, because Tasks 4, 5, 7 and 8 change the launch path;
- nested-jail verification from `just build-dev-bundle` for every `cmd/`/`internal/` task
  ([AGENTS.md, Testing](../../AGENTS.md#testing)).

Never `--no-verify`; never `just install` in the jail.

## Map

| Path | Change | Task |
| :--- | :--- | :--- |
| `internal/packdecl/contributes.go` | `ViaPack` in `knownVias` (installKind `source`); `Run`, `Prebuilt` fields on `Contribution`; program branch of `validateContribution` (~`:4192`) calls `packTreeProblems`; `InstallContributions` (~`:1461`) projects `via: "pack"` | 1, 3 |
| `internal/packdecl/packtree.go` | **new** — `ViaPack`, `packTreeProblems`, `PrebuiltBuild`, `PackTreeRecipe`, `PackTreeProduces`, `PackTreeProgramDir` | 1, 3 |
| `internal/packdecl/fork.go` | `forkFieldPlacementProblems`: name `via "pack"` in the refusal text where it lists who reads a field | 1 |
| `internal/packdecl/packdecl.go` | `Install` gains `PackFrom`, `PackRun`, `Prebuilt`; `IsPackTree()`; `unknownViaSkip` needs no change (membership is `KnownVia`) | 3 |
| `internal/packdecl/packtree_test.go` | **new** | 1, 3 |
| `internal/packdecl/viaenum_test.go` | `viaList()` literal becomes `"npm, installer, source or pack"` — a **rewrite**, not a repair | 1 |
| `internal/packload/packtree.go` | **new** — `PackTreePrograms(packs) []Fork`, the source directory's digest | 3 |
| `internal/packload/forks.go` | `Fork` gains `From`, `Run`; `IsPackTree()`; `Key()` for one | 3 |
| `internal/config/programs.go` | `overrides` in `knownProgramsKeys`; `ProgramOverrides(warn)`; validation | 2 |
| `internal/config/programs_test.go` | **new or extended** | 2 |
| `internal/cli/config_ref.txt` | `programs.overrides` paragraph; `via: "pack"` where program vias are listed | 2, 1 |
| `internal/cli/forkbuild.go` | `forkBuild.recipe/id/buildSource/buildLine` branch on `Fork.IsPackTree()`; source staging copies `Root/From` (`copySourceTree`, `.git` skipped) where `checkOutForkSource` runs; placement script; entry pruning before admit; `admitPackTreeCopy` for no-build | 4 |
| `internal/capture/manifest.go` | export `ManifestForTree(tree, home, platform)` over `describeTree` | 4 |
| `internal/cli/run/forkbuild.go` | `forkPins` (~`:334`) appends pack-tree pins (Commit = digest, no network); `forkBuildPlan` passes them | 4 |
| `internal/cli/run/packtreepins.go` | **new** — digest each selected pack-tree program over the staged tree | 4 |
| `internal/entrypoint/forklauncher.go` | `sourceLauncherTemplate`: on a new key for a pack-tree program, remove `~/.local/lib/yolo-programs/<bin>` before materialize; reason lines | 5 |
| `internal/entrypoint/readiness.go`, `shell.go` | reason text carries log path, `yolo pack build <pack>` and the bypass | 5 |
| `internal/cli/packbuild.go` | **new** — `yolo pack build` | 6 |
| `internal/cli/pack.go` | `packMain` dispatch `case "build"` (~`:294`); help text | 6 |
| `internal/cli/packtreeprebuilt.go` | **new** — prebuilt fetch and host admission | 7 |
| `internal/cli/packbinaries.go` | `installPackBinaries` (~`:41`) also prefetches selected programs' `prebuilt` | 7 |
| `internal/cli/run/programoverrides.go` | the override puts its checkout in place of `from` (`ResolvePackTreeProgram`), its line, and the refusal of a non-pack-tree program | 8 |
| `internal/cli/run/prebuiltline.go`, `programoverrides.go`, `patchedforkline.go` | prebuilt and checkout disclosure lines, fresh launch and attach | 7, 8 |
| `internal/hostfloor/floor.go`, `ensure.go`, `built.go` | pack-tree arm (no pin), which builds an override's checkout the same way | 9 |
| `internal/cli/hostfloor.go` | `wireFloorBuild` (~`:176`) resolves pack-tree builds by digest | 9 |
| `integration/packtreeprogram_test.go` | **new** — the design's six done steps | 4, 5, 7, 8 |
| `docs/reference/pack-system.md` | program schema: `via: "pack"`, `from`, `build`, `run`, `prebuilt` | 1 |
| `CHANGELOG.md` | `[Unreleased]` → `### Added`, one bold-lead paragraph | 6 |

## Reuse before you write

- **The build act:** `buildFork` / `buildForkUnderLock` (`internal/cli/forkbuild.go:756`, `:799`).
  It already provides the lock, the hit after the lock, staging, the sealed jail, admission, the
  receipt and the settle. Add a source branch only. Never write a second build function.
- **Build identity:** `buildID(source, commit, recipe, platform)` (`forkbuild.go:337`). Pass
  `pack:<pack>/<from>` as source and the tree digest as commit.
- **The hit check:** `resolveForkBuild(store, bin, platform, source, revision, recipe)`
  (`internal/cli/capturematerialize.go:555`). The host floor calls it too (`hostfloor.go:179`).
- **The tree copy:** `copySourceTree(src, dst)` (`forkbuild.go:1398`). Add a skip for `.git`
  (advice: as a parameter, because the fork path copies a checkout that has no `.git` at its
  root anyway).
- **The digest:** `treedigest.OfSkipping(root, map[string]bool{".git": true})`
  (`internal/treedigest/treedigest.go:48`).
- **The build jail argv:** `forkBuildJailArgv(build)` (`forkbuild.go:1439`). It cds to
  `/workspace/src` and then runs the line. The placement script is the line (Task 4).
- **Seal narrowing:** a patched extension seals to its contributing pack alone (PPX-D5, in
  `forkBuildRunJail`). A pack-tree program does the same. It has no base.
- **Launch wiring:** `Options.forkPins` → `forkBuildPlan` → `BuildSlot` → `ForkDelivery`
  (`internal/cli/run/forkbuild.go:98-200`). `YOLO_FORK_BUILDS` carries the key per `bin`. Do not
  rename the variable.
- **The source launcher:** `sourceAgentLauncherSegments` (`internal/entrypoint/forklauncher.go:124`)
  and its install-only branch (`:275-283`). Readiness already calls it.
- **The download:** `packbin.Fetcher.Ensure(ctx, Want)` and `packbin.Path`
  (`internal/packbin/packbin.go:152`, `:59`). Platform-key and URL/sha256 shape checks live in
  `internal/loopholedecl/binaries.go:284-333`. Lift them into a shared helper rather than copying
  them.
- **User-scope-only pattern:** `validatePrograms` and `ProgramsAutoprune`
  (`internal/config/programs.go`) already read the user file directly and refuse a workspace
  `programs` key. `overrides` inherits both for free.
- **The scope predicate:** `paths.WorkspaceScopeBreach(path)`
  (`internal/paths/workspacescope.go:137`).
- **Integration fixtures:** `TestForkBuildDeliversTheForkInPlaceOfItsBase`
  (`integration/forkbuild_test.go:62`) for a `file://` fixture pack and a sh-only build line;
  `withReadiness()` (`integration/harness_test.go:1306`) to turn the readiness act back on;
  `integration/packbinary_test.go` for a local TLS server serving a digest-pinned file.

## Traps

- **`InstallKindSource` has 34 non-test sites across 15 files** (`rg -n InstallKindSource internal
  --type go`). A `via: "pack"` program projects to `Kind: "source"` so that it gets the source
  launcher, the materialize and readiness unchanged, but **every pin and fork-rewrite site must
  exclude it** with `Install.IsPackTree()`. The symptom of a missed site: the host floor says
  *"fork has no usable pin"* for a pack-tree program. The sites that read a pin:
  - `internal/hostfloor/floor.go` (`forkPin`, `awaitsPin`, `pinFork`, `recipeNoEntryReason`);
  - `internal/hostfloor/ensure.go`;
  - `internal/cli/hostfloor.go`;
  - `internal/cli/packupdate.go`;
  - `internal/cli/capturehost.go`.

  Audit every site and list each one with its verdict in the Task 3 report.
- **The `fallback` JSON key is taken** by `Contribution.Fallback` (a string, for an unmodified
  extension). The program's download key is `prebuilt`. An object under `fallback` fails to
  decode.
- **The integration suite turns readiness off** (`readinessEnvForSuite`). A test of step 6 that
  forgets `withReadiness()` passes vacuously: the launch starts and nothing is refused.
- **`git add` before a nested image rebuild.** nix sees tracked files only, so an untracked new
  file is silently missing from the nested jail.
- **Selection is newest-wins on a one-second receipt stamp.** The floor takes the entry the act
  returns, never a second lookup (FP-D16's reason). Keep that in `yolo pack build --force`.
- **The darwin symlink class:** resolve an override path once, through root's links only
  (`nofollow.Resolve`, [BG-D22](pack-buildable-programs.md#BG-D22)), *before* the scope check. On
  a Mac, `/tmp` is a link to `/private/tmp`. Reproduce on Linux with `TMPDIR=/tmp/link`
  ([AGENTS.md, Testing](../../AGENTS.md#testing)).
- **`from` is already a field on `Contribution`** (`contributes.go:328`, pack-relative). The
  per-kind field census (search `case "from"` near `contributes.go:4456`) must accept it on a
  program only when `via` is `"pack"`, or `{"kind":"program","via":"npm","from":"x"}` validates
  and silently does nothing.
- **The build jail's home is the capture home.** `HOME` in the placement script is
  `/home/agent`. The store records home-relative paths, and that is why building in place under
  `~/.local/lib` makes the entry relocatable.

## Tasks

### Task 1 — manifest vocabulary (independent)

- **Settled (do not re-open):** `via: "pack"` requires `from` and `run`. `build` is optional, one
  line. `prebuilt` is optional, a map from `<goos>/<goarch>` to `{url, sha256}` under `binaries`'
  rules. The delivery fields of other vias and the fork fields are refused by name. Behavior
  fields, `platforms` and `node_floor` are allowed. `prebuilt` is refused in an official pack.
- **Preconditions:** `go test ./internal/packdecl/...` is green at the start.
- **Tests first** (`internal/packdecl/packtree_test.go`):

```go
package packdecl

import (
	"strings"
	"testing"
)

func goodPackTree() Contribution {
	return Contribution{
		Kind: KindProgram, Bin: "board", Via: ViaPack,
		From: "tools/board-cli", Build: "npm ci && npm run bundle", Run: "dist/board.js",
	}
}

func TestAPackTreeProgramValidates(t *testing.T) {
	if p := validateContribution("contributes[0]", goodPackTree()); len(p) > 0 {
		t.Fatalf("refused:\n%s", strings.Join(p, "\n"))
	}
	c := goodPackTree()
	c.Build = "" // no build line: the tree runs as shipped
	c.Prebuilt = map[string]PrebuiltBuild{"linux/amd64": {
		URL: "https://example.invalid/board", SHA256: strings.Repeat("a", 64)}}
	c.Platforms, c.NodeFloor = []string{"linux"}, "22.19"
	if p := validateContribution("contributes[0]", c); len(p) > 0 {
		t.Fatalf("refused:\n%s", strings.Join(p, "\n"))
	}
}

func TestPackTreeRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		mut  func(*Contribution)
		want string
	}{
		"no from":        {func(c *Contribution) { c.From = "" }, `"from"`},
		"absolute from":  {func(c *Contribution) { c.From = "/etc" }, "relative"},
		"escaping from":  {func(c *Contribution) { c.From = "../x" }, ".."},
		"no run":         {func(c *Contribution) { c.Run = "" }, `"run"`},
		"escaping run":   {func(c *Contribution) { c.Run = "../../bin/sh" }, ".."},
		"two-line build": {func(c *Contribution) { c.Build = "a\nb" }, "one command line"},
		"fork_of":        {func(c *Contribution) { c.ForkOf = "pi" }, `"fork_of"`},
		"package":        {func(c *Contribution) { c.Package = "x" }, `"package"`},
		"produces":       {func(c *Contribution) { c.Produces = []string{".local/bin/board"} }, `"produces"`},
		"http prebuilt": {func(c *Contribution) {
			c.Prebuilt = map[string]PrebuiltBuild{"linux/amd64": {URL: "http://x", SHA256: strings.Repeat("a", 64)}}
		}, "https"},
		"bad platform": {func(c *Contribution) {
			c.Prebuilt = map[string]PrebuiltBuild{"linux": {URL: "https://x", SHA256: strings.Repeat("a", 64)}}
		}, "goos/goarch"},
		"empty prebuilt": {func(c *Contribution) { c.Prebuilt = map[string]PrebuiltBuild{} }, "at least one"},
	} {
		c := goodPackTree()
		tc.mut(&c)
		p := strings.Join(validateContribution("contributes[0]", c), "\n")
		if !strings.Contains(p, tc.want) {
			t.Errorf("%s: problems %q do not name %q", name, p, tc.want)
		}
	}
}

func TestRunAndPrebuiltOffAPackTreeAreRefused(t *testing.T) {
	c := Contribution{Kind: KindProgram, Bin: "x", Via: "npm", Package: "x", Run: "a", From: "b"}
	p := strings.Join(validateContribution("contributes[0]", c), "\n")
	for _, f := range []string{`"run"`, `"from"`} {
		if !strings.Contains(p, f) {
			t.Errorf("an npm program's %s validated: %q", f, p)
		}
	}
}
```

  Red: `go test ./internal/packdecl/ -run 'PackTree|RunAndPrebuilt'` fails to compile (`ViaPack`,
  `Run`, `Prebuilt`, `PrebuiltBuild` undefined).
- **Change:**
  - add `ViaPack = "pack"` to `knownVias`, with installKind `InstallKindSource`;
  - add `Run string \`json:"run,omitempty"\`` and `Prebuilt map[string]PrebuiltBuild
    \`json:"prebuilt,omitempty"\``, documented in the doc comment house style, which is the
    schema reference;
  - add a `packTreeProblems` arm in the program `switch` (~`contributes.go:4238`);
  - extend the placement refusal so that `run`/`prebuilt`/`from` on any other program is
    refused;
  - lift the platform, URL and sha256 checks out of `loopholedecl/binaries.go` into a helper that
    both callers use;
  - add an official-pack census beside `TestNoOfficialPackDeclaresABinary` that refuses
    `prebuilt` in any embedded `pack.json`.
- **Gates:**
  - `go test ./internal/packdecl/... ./internal/loopholedecl/... ./internal/packload/...`;
  - `TestKnownViasCoversTheShippedMechanisms`, rewritten so its literal names `pack`;
  - `just format`.
- **Fences:** nothing under `internal/cli`, `internal/entrypoint` or `internal/config`.
- **Ships with:** the `docs/reference/pack-system.md` program schema; `config_ref.txt` where it
  lists vias.
- **Commit:** `feat(packdecl): declare a program built from the pack's own tree`
- **Report:** the red compile error, the green run, and every test whose expected via list
  changed.

### Task 2 — `programs.overrides` (independent)

- **Settled:** `programs.overrides.<bin>` is `{path}`, user scope only. `path` has `~`
  expanded, must be absolute after expansion, and is checked at launch (Task 8) rather than here.
  `run` is retired ([BG-D19](pack-buildable-programs.md#BG-D19)): an entry carrying it is a
  validation error naming the pack's own `run`. A workspace `programs` key is already an error. Keep
  its message, and extend the reason so it covers choosing a program's bytes too. The entry's other
  form is `{"prebuilt": true}`, the prebuilt opt-in
  ([BG-D18](pack-buildable-programs.md#BG-D18)). It is mutually exclusive with `path`
  (a validation error naming both), is read by `ProgramPrebuiltOptIns`, and is never returned by
  `ProgramOverrides`.
- **Tests first** (`internal/config/programs_test.go`): with a user config of
  `{"programs":{"overrides":{"board":{"path":"~/x"},"pi":{"path":"/srv/pi"}}}}`,
  `ProgramOverrides(nil)` returns two entries, `board.Path == $HOME/x` and `pi.Path ==
  "/srv/pi"`. These shapes must each produce a validation error naming the field:
  - `{"programs":{"overrides":{"board":{"path":""}}}}`;
  - `{"programs":{"overrides":{"board":{"path":"rel/x"}}}}`;
  - `{"programs":{"overrides":{"board":{"path":"/x","run":"y"}}}}` (the retired `run`);
  - `{"programs":{"overrides":{"board":{"path":"/x","color":"red"}}}}` (unknown key).

  A workspace `yolo-jail.jsonc` carrying `programs.overrides` is an error containing
  `user-scope only`. `ProgramOverrides` returns an empty map on a malformed user file: it fails
  closed, so no override applies. Red: `go test ./internal/config/ -run Overrides`.
- **Change:** `knownProgramsKeys` gains `overrides`; `validatePrograms` validates it;
  `ProgramOverrides(warn) map[string]ProgramOverride` reads the user file the way
  `ProgramsAutoprune` does.
- **Gates:** `go test ./internal/config/...`; `yolo check --no-build` against a scratch user config
  under `HOME=$(mktemp -d)`.
- **Fences:** `internal/config/programs*.go`, `config_ref.txt` only.
- **Ships with:** `config_ref.txt` `programs` paragraph (scope line: "USER SCOPE ONLY").
- **Commit:** `feat(config): declare per-program dev overrides at user scope`

### Task 3 — identity and projection (after 1)

- **Settled:** source `pack:<pack>/<from>`; revision is the tree digest of the staged
  `<root>/<from>` with `.git` skipped; recipe is `PackTreeRecipe(from, build, run)`, a sha256 over
  JSON tagged `"pack-tree"` plus a layout version `1`; produces are synthesized as
  `[".local/bin/<bin>", ".local/lib/yolo-programs/<bin>"]`.
- **Tests first:**
  - `PackTreeRecipe` differs from `ForkSourceRecipe` for equal strings, and changes when any of
    `build`, `run` or `from` changes;
  - `packload.PackTreePrograms` returns one `Fork` with `IsPackTree()` true, `Base == ""` and
    `Root` the pack root;
  - its digest helper ignores `.git/` and changes when one byte or one exec bit under `from`
    changes;
  - an absent `from` directory yields an empty digest and no error;
  - `InstallContributions` projects `Kind == "source"`, `IsPackTree()`, and the synthesized
    `Produces`, with `ProgramPath() == ".local/bin/board"`.

  Red: `go test ./internal/packdecl/ ./internal/packload/ -run PackTree`.
- **Change:** see the Map rows for Task 3, and the `InstallKindSource` audit (Traps).
- **Gates:** `go test ./internal/packdecl/... ./internal/packload/... ./internal/hostfloor/...
  ./internal/entrypoint/...`.
- **Commit:** `feat(packload): key a pack-tree program on its source directory's digest`
- **Report:** the 34-site audit table, one line per site: *excluded*, *shared* or *n/a*.

### Task 4 — the build, and the jail launch's pins (after 3)

- **Settled:**
  - the build is the fork route's act, sealed to the one pack;
  - its source is the staged pack's `from` minus `.git`;
  - the placement script copies the source into `~/.local/lib/yolo-programs/<bin>/`, runs `build`
    there, sets `+x` on `run`, and links `~/.local/bin/<bin>` → `../lib/yolo-programs/<bin>/<run>`;
  - the entry holds only those two paths, and anything else written is dropped and named in the
    log;
  - with no `build`, the host admits the same layout with no jail;
  - a hit prints nothing.
- **Tests first:**
  - unit (`internal/cli/forkbuild_test.go` seams): the placement script for
    `{bin: board, run: dist/board.js, build: "make"}` is exactly the string below. Pin it, then
    run it in a temp `HOME` with a fake `/workspace/src` through `bash -c` and assert the link
    resolves:

```text
d="$HOME/.local/lib/yolo-programs/board" && mkdir -p "$d" && cp -a /workspace/src/. "$d"/ && cd "$d" && { make ; } && test -f "$d/dist/board" && chmod +x "$d/dist/board" && mkdir -p "$HOME/.local/bin" && ln -sfn '../lib/yolo-programs/board/dist/board' "$HOME/.local/bin/board"
```

  - `admitPackTreeCopy` on a fixture tree yields an entry whose manifest lists exactly the two
    paths, and whose receipt carries `source: "pack:fx/tools/x"` and the digest;
  - integration (`integration/packtreeprogram_test.go`, `TestPackTreeProgramBuildsOnceAndRebuildsOnChange`):
    a `file://` pack with `tools/x/VERSION` = `1` and `run: "x"`, with this build line (JSON-escaped
    in the fixture):

```text
printf '#!/bin/sh\necho x-%s\n' "$(cat VERSION)" > x
```

    then:
    1. launch `yolo -- x`, and expect output `x-1` plus one build result line;
    2. write `2` into `VERSION` and launch again: `x-2` and one build line;
    3. launch again: `x-2` and no build line.

    This covers the design's done steps 1–3.
- **Change:** see the Map rows for Task 4.
- **Gates:** `go test ./internal/cli/... ./internal/capture/... ./internal/cli/run/...`; then
  `git add -N` the new files and run `go test -count=1 -run 'TestPackTree|TestForkBuild' ./integration`
  in the jail.
- **Fences:** no change to fork recipes or fork ids. Plain and patched fork entries must keep
  hitting.
- **Commit:** `feat(build): build a pack-tree program in the sealed build act`
- **Report:** the three integration runs' build lines, and the store key of each.

### Task 5 — failure, replacement and lines (after 4)

- **Settled:**
  - a failed build refuses that program, naming the log, `yolo pack build <pack>` and
    `YOLO_ALLOW_MISSING_PROGRAMS=1` ([OQ-BG1](pack-buildable-programs.md#decision-ledger): the
    launch stops);
  - a waiter takes the winner's failure;
  - materializing a new key removes `~/.local/lib/yolo-programs/<bin>/` first.
- **Tests first:**
  - unit: the source launcher of a pack-tree program, given key B after key A, leaves no file of
    A's tree behind;
  - integration `TestPackTreeProgramBuildFailureRefusesOnlyIt`, using `withReadiness()`: a second
    program from an installer fixture beside a pack-tree program whose build is `exit 3`;
  - the launch exits `78` and its stderr names `.yolo/build-`, `yolo pack build fx` and
    `YOLO_ALLOW_MISSING_PROGRAMS`;
  - with the bypass, `yolo -- other` runs.

  This covers done step 6.
- **Gates:** `go test ./internal/entrypoint/... ./internal/cli/...`; the two integration tests.
- **Commit:** `feat(build): refuse an undeliverable pack-tree program, naming its log and retry`

### Task 6 — `yolo pack build` (after 4; parallel with 5)

- **Settled:** `yolo pack build [<pack>...] [--force] [--at jail|host]` on the host only.
  - No argument means every selected pack.
  - Each program prints one line: up to date, built (with digest and time), or failed (with its
    log).
  - A fork prints `yolo capture <bin>`, and an override's checkout is what it builds.
  - Exit 0, or 1 on any failure. In a jail it refuses with exit 2, naming the host command.
- **Tests first:** `internal/cli/packbuild_test.go`, against a fake build seam:
  - two calls build once and then report "up to date";
  - `--force` builds again;
  - an unknown pack exits 2, naming `yolo pack ls`;
  - `YOLO_IN_JAIL`-style detection (use the repo's `config.InJail` seam) refuses;
  - a fork prints `yolo capture`.
- **Gates:** `go test ./internal/cli/ -run PackBuild`; `yolo pack --help` lists `build`.
- **Ships with:** `CHANGELOG.md` `[Unreleased]` `### Added`, one bold-lead paragraph covering the
  whole feature, linking the reference doc once it exists (or the design until then).
- **Commit:** `feat(pack): add yolo pack build`

### Task 7 — the prebuilt artifact (after 5)

- **Settled:**
  - used when `from` is absent, or when the user opted the program into it
    (`programs.overrides.<bin>.prebuilt`, [BG-D18](pack-buildable-programs.md#BG-D18)). Never in
    place of a build that cannot run ([OQ-BG2](pack-buildable-programs.md#decision-ledger), ruled): that
    build is refused, naming the setup step and, when a prebuilt is declared for the platform, the
    opt-in;
  - fetched through `packbin` by the launch slot, `yolo pack install`/`update` and
    `yolo pack build`;
  - admitted on the host as `~/.local/lib/yolo-programs/<bin>/<bin>` plus the link;
  - disclosed on every fresh launch and attach with its sha256;
  - never after a failed build.
- **Tests first:**
  - unit: a digest mismatch caches nothing, and its message names both digests;
  - a present `from` never fetches without the opt-in, on a Mac with no sandbox account too
    (assert zero requests to the TLS test server), and fetches once with it;
  - integration `TestPackTreeProgramUsesThePrebuiltWithoutSources`: the step-1 fixture with
    `tools/x` deleted and a `prebuilt` served by the local TLS fixture; `yolo -- x` runs it, and
    the launch prints a line containing the first 12 hex digits of its sha256 (done step 5).
- **Commit:** `feat(build): fall back to a pack's verified prebuilt artifact when it has no source`

### Task 8 — the dev override builds its checkout (after 2 and 5)

Rewritten to [OQ-BG3](pack-buildable-programs.md#decision-ledger)'s ruling (2026-10-23) and
[BG-D19](pack-buildable-programs.md#BG-D19); the live delivery this task first built is deleted.

- **Settled:**
  - an override applies to a pack-tree program only, keyed by `bin`; one naming any other declared
    program refuses the launch, `yolo host` and `yolo check`, naming a fork pack as the next step;
  - the checkout replaces `from` in the fork route's build act: the same seal, the pack's own
    `build` and `run`, the same store, lock and fatal refusal, keyed by the checkout's tree digest
    (`.git` skipped, git-ignored files included, [BG-D20](pack-buildable-programs.md#BG-D20)) under
    the build source `checkout:<pack>:<path>`;
  - a path at the credential boundary is refused; the override's `run` is retired;
  - no mount, link, host-path run or run-time build; an edit is built at the next fresh launch;
  - every fresh launch names the checkout and whether its build was rebuilt or reused; an attach
    names the build its launch made.
- **Tests first:**
  - unit (`internal/cli/run/programoverrides_test.go`): the path is resolved and a credential
    boundary refused; the checkout's digest keys the build; a launch through the fake podman
    builds from the checkout and names it, rebuilt then reused; an override of an npm program
    refuses the launch with no argv; an attach names the checkout;
  - `yolo check` (`internal/cli/check/programoverride_test.go`) fails an override of a non-pack-tree
    program;
  - integration `TestPackTreeProgramOverrideBuildsTheCheckout` (done step 4): set the override and
    launch, and it builds from the checkout and the launch names it; relaunch unchanged, and
    nothing is rebuilt; edit the checkout and relaunch, and it rebuilds;
    `TestPackTreeProgramOverrideBuildFailureRefusesTheLaunch` covers a checkout the build fails on.
- **Fences:** `internal/macosuser` (Task 10), `internal/hostfloor` (Task 9).
- **Commit:** `feat(build): build a dev override's checkout like its pack's tree at each launch`

### Task 9 — `yolo host` on Linux (after 5 and 8)

- **Settled:** the floor builds a pack-tree program through the same act, resolved by digest with
  no pin, and materializes it relocated as for a fork (FP-D16). An override builds its checkout
  the same way, at each `yolo host` launch.
- **Tests first:** `internal/hostfloor/built_test.go` gains a pack-tree program that needs no pin
  and resolves by digest; `internal/cli/hostfloorpacktree_test.go` gains an override arm that
  builds the checkout, reuses it unchanged and rebuilds an edit at the next launch.
- **Gates:** `go test ./internal/hostfloor/... ./internal/cli/ -run HostFloor`; on a Linux host
  (not nested), done steps 1–6 with `yolo host -- x`.
- **Commit:** `feat(host): deliver pack-tree programs and dev overrides on the host floor`

### Task 10 — macos-user and a Mac's `yolo host` (after 9; **needs a real Mac**)

- **Settled:**
  - the darwin sealed act builds a pack-tree program (FP-D24's path);
  - the prebuilt takes its darwin entry where the source is absent or the user opted in. With
    no sandbox account a present source is refused, naming `yolo macos-setup` and the opt-in
    ([OQ-BG2](pack-buildable-programs.md#decision-ledger), ruled);
  - an override's checkout is built by the same darwin act; the profile grants it nothing.
- **Tests first:** on Linux, the darwin act's staging of a checkout. On a Mac,
  `integration/macosuserprograms_test.go` gains the six done steps under `macos-user`.
- **Gates:** Linux unit tests. The native evidence is a Mac run of `-run '^TestMacosUser.*PackTree'`,
  or a `macos-user.yml` dispatch, recorded with host and date.
- **Commit:** `feat(macosuser): deliver pack-tree programs and dev overrides in the sandbox`

Apple Container needs no task of its own: Tasks 4–8 run there unchanged. Its verification is a
real-Mac run of the integration tests under `YOLO_RUNTIME=container`, which is owed with Task 10.

## Ships with

- **Unit tests:** as listed per task. The degenerate cases the design enumerates:
  - `from: "."`;
  - an empty `from` directory, which fails with "produced no `<run>`";
  - `run` naming a directory;
  - an empty `prebuilt`;
  - a platform `prebuilt` lacks;
  - an override for an undeclared `bin`, reported by `yolo check` as information;
  - an override for a declared program that is not a pack-tree program, failed by `yolo check`.
- **Integration:** `integration/packtreeprogram_test.go` covers done steps 1–6. It is the test
  that catches a regression end to end; the unit tests do not.
- **Tests that must change, and be rewritten to the new behavior:** `TestKnownViasCoversTheShippedMechanisms`'s
  `viaList()` literal, and any `KnownVias()` golden.
- **Docs:**
  - `docs/reference/pack-system.md` (program schema);
  - `docs/reference/program-readiness.md` (a pack-tree program joins the act, so add one line);
  - `docs/reference/pack-binaries.md` *What this does not license* (point the build-step bullet
    at this route);
  - `internal/cli/config_ref.txt` (`programs.overrides`);
  - `yolo pack --help`;
  - `CHANGELOG.md`.

  When the design is built, it graduates into a `docs/reference/pack-tree-programs.md` system doc
  in the same commit that deletes this plan.
- **Error text** is the implementer's to word. The facts each refusal carries are fixed in the
  design's [precedence table](pack-buildable-programs.md#4-precedence-one-resolver-four-rungs).
- **Cheap, and the implementer's choice:** map types; whether `PackTreePrograms` lives in a new
  file.
- **Stop and ask:** anything that would change a fork's recipe or id, so that existing store
  entries stop hitting, or that would let a launch build inside a jail.

## Don't

- Don't add an `Install.Kind "pack"`. The source launcher, materialize and readiness are already
  correct for `"source"`, and a new kind means teaching all 34 sites instead of excluding the
  handful that read a pin.
- Don't digest the original pack directory. Digest the launch's staged copy, which nothing edits.
- Don't fetch a prebuilt for a present source unless the user opted in, and never substitute one
  for a build that cannot run ([P5](pack-buildable-programs.md#P5),
  [OQ-BG2](pack-buildable-programs.md#decision-ledger)).
- Don't add a stat cache for the digest before a measurement asks for one.
- Don't deliver an override live, or build one anywhere but the build act
  ([OQ-BG3](pack-buildable-programs.md#decision-ledger)).
